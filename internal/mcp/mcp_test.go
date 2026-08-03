package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

// fakeStore serves list_repos/read_file without a real SurrealDB — the tool
// handlers only need GetRepo and ListRepos. (search_code's corpus path is
// covered by the searcher package e2e and the live Claude Code run, T8.2.)
type fakeStore struct {
	store.Store
	repos []store.Repo
}

func (f fakeStore) ListRepos(context.Context) ([]store.Repo, error) { return f.repos, nil }
func (f fakeStore) GetRepo(_ context.Context, name string) (*store.Repo, error) {
	for i := range f.repos {
		if f.repos[i].Name == name {
			return &f.repos[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func gitc(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// callTool runs one tool over an in-memory client/server session and returns
// the decoded structured output.
func callTool[T any](t *testing.T, s *sdk.Server, name string, args map[string]any) (T, *sdk.CallToolResult) {
	t.Helper()
	ctx := t.Context()
	st, ct := sdk.NewInMemoryTransports()
	go func() { _, _ = s.Connect(ctx, st, nil) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	var out T
	if res.StructuredContent != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("decode %s output: %v", name, err)
		}
	}
	return out, res
}

// TestToolCalls exercises the actual handlers (not just their schemas):
// list_repos returns fixtures, read_file reads a real bare mirror with line
// ranges, and error paths (unknown repo, binary file) surface as tool errors.
func TestToolCalls(t *testing.T) {
	// a real bare mirror so read_file's git plumbing runs for real
	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "hello.txt"), []byte("l1\nl2\nl3\nl4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "bin.dat"), []byte{0x00, 0x01, 0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	writeCodeNavFixture(t, origin)
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "fixture")

	dataDir := t.TempDir()
	indexedRef := strings.TrimSpace(func() string {
		out, err := exec.Command("git", "-C", origin, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}())
	unit, err := (analysisunit.Scope{
		Repository: "example.com/demo",
		Name:       "payments-api",
		Primary:    []string{"services/payments"},
		Supporting: []string{"api/payments.proto"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	repo := store.Repo{
		Name: "example.com/demo", DefaultBranch: "main", IsPublic: true,
		IndexedCommitHash: indexedRef, IndexedAnalysisUnit: unit,
	}
	if err := phebssync.Mirror(t.Context(), "file://"+origin, phebssync.RepoDir(dataDir, repo.Name)); err != nil {
		t.Fatal(err)
	}
	st := fakeStore{repos: []store.Repo{repo}}
	s := NewServer(Options{
		Version: "test", Store: st, DataDir: dataDir,
		CodeNav: codenav.New(codenav.Options{DataDir: dataDir}),
		History: phebssync.NewHistoryService(dataDir),
	})
	var advancedRef string

	t.Run("list_repos", func(t *testing.T) {
		out, res := callTool[reposOut](t, s, "list_repos", nil)
		if res.IsError {
			t.Fatalf("list_repos errored: %v", res.Content)
		}
		if len(out.Repos) != 1 || out.Repos[0].Name != "example.com/demo" {
			t.Errorf("list_repos = %+v", out.Repos)
		}
		if got := out.Repos[0].AnalysisUnit; got == nil ||
			got.Name != "payments-api" ||
			!slices.Equal(got.PrimaryPaths, []string{"services/payments"}) ||
			!slices.Equal(got.SupportingPaths, []string{"api/payments.proto"}) {
			t.Errorf("list_repos analysis unit = %+v", got)
		}
	})

	t.Run("read_file range", func(t *testing.T) {
		out, res := callTool[readOut](t, s, "read_file", map[string]any{
			"repo": "example.com/demo", "path": "hello.txt", "start_line": 2, "end_line": 3,
		})
		if res.IsError {
			t.Fatalf("read_file errored: %v", res.Content)
		}
		if out.Content != "l2\nl3" || out.StartLine != 2 || out.EndLine != 3 || out.TotalLine != 4 {
			t.Errorf("read_file range = %+v", out)
		}
	})

	t.Run("read_file defaults to indexed revision", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(origin, "hello.txt"), []byte("l1\nchanged\nl3\nl4\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitc(t, origin, "add", "hello.txt")
		gitc(t, origin, "commit", "-m", "advance mirror")
		gitOut, err := exec.Command("git", "-C", origin, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		advancedRef = strings.TrimSpace(string(gitOut))
		if err := phebssync.Mirror(t.Context(), "file://"+origin, phebssync.RepoDir(dataDir, repo.Name)); err != nil {
			t.Fatal(err)
		}

		out, res := callTool[readOut](t, s, "read_file", map[string]any{
			"repo": "example.com/demo", "path": "hello.txt",
		})
		if res.IsError {
			t.Fatalf("read_file errored: %v", res.Content)
		}
		if out.Content != "l1\nl2\nl3\nl4" {
			t.Errorf("default read followed mutable HEAD: %q", out.Content)
		}
	})

	t.Run("read_file unknown repo is a tool error", func(t *testing.T) {
		_, res := callTool[readOut](t, s, "read_file", map[string]any{"repo": "no/such", "path": "x"})
		if !res.IsError {
			t.Error("expected tool error for unknown repo")
		}
	})

	t.Run("read_file binary is a tool error", func(t *testing.T) {
		_, res := callTool[readOut](t, s, "read_file", map[string]any{"repo": "example.com/demo", "path": "bin.dat"})
		if !res.IsError {
			t.Error("expected tool error for a binary file")
		}
	})

	t.Run("code navigation uses indexed revision and UTF-16", func(t *testing.T) {
		definition, res := callTool[codenav.DefinitionResult](t, s, "find_definitions", map[string]any{
			"repo": repo.Name, "path": "use/use.go", "line": 1, "character": 29,
		})
		if res.IsError || !definition.Available || definition.Location == nil {
			t.Fatalf("find_definitions = %+v, error=%v content=%v", definition, res.IsError, res.Content)
		}
		if definition.Location.Revision != indexedRef || definition.Location.Path != "lib/rocket.go" ||
			definition.Location.Encoding != codenav.EncodingUTF16 || definition.Location.Range.Start.Character != 5 {
			t.Errorf("definition = %+v", definition)
		}

		references, res := callTool[codenav.ReferencesResult](t, s, "find_references", map[string]any{
			"repo": repo.Name, "path": "lib/rocket.go", "line": 1, "character": 6,
		})
		if res.IsError || len(references.Locations) != 1 {
			t.Fatalf("find_references = %+v, error=%v content=%v", references, res.IsError, res.Content)
		}
		if references.Locations[0].Revision != indexedRef ||
			references.Locations[0].Range.Start.Character != 28 ||
			references.Locations[0].Encoding != codenav.EncodingUTF16 {
			t.Errorf("reference = %+v", references.Locations[0])
		}

		hover, res := callTool[codenav.HoverResult](t, s, "hover", map[string]any{
			"repo": repo.Name, "path": "use/use.go", "line": 1, "character": 29,
		})
		if res.IsError || hover.Hover == nil || hover.Hover.DisplayName != "Rocket" ||
			hover.Hover.Encoding != codenav.EncodingUTF16 {
			t.Fatalf("hover = %+v, error=%v content=%v", hover, res.IsError, res.Content)
		}
	})

	t.Run("history tools default to indexed revision", func(t *testing.T) {
		blame, res := callTool[phebssync.BlameResult](t, s, "blame", map[string]any{
			"repo": repo.Name, "path": "hello.txt",
		})
		if res.IsError || blame.Revision != indexedRef || len(blame.Lines) != 4 {
			t.Fatalf("blame = %+v, error=%v content=%v", blame, res.IsError, res.Content)
		}
		for _, line := range blame.Lines {
			if line.CommitID != indexedRef {
				t.Errorf("blame followed mutable HEAD: %+v", line)
			}
		}

		commits, res := callTool[phebssync.CommitListResult](t, s, "list_commits", map[string]any{
			"repo": repo.Name,
		})
		if res.IsError || commits.Revision != indexedRef || len(commits.Commits) != 1 || commits.Commits[0].ID != indexedRef {
			t.Fatalf("list_commits = %+v, error=%v content=%v", commits, res.IsError, res.Content)
		}

		commit, res := callTool[phebssync.CommitResult](t, s, "get_commit", map[string]any{
			"repo": repo.Name,
		})
		if res.IsError || commit.Revision != indexedRef || commit.Commit.ID != indexedRef {
			t.Fatalf("get_commit = %+v, error=%v content=%v", commit, res.IsError, res.Content)
		}

		diff, res := callTool[phebssync.DiffResult](t, s, "diff", map[string]any{
			"repo": repo.Name,
		})
		if res.IsError || diff.Head != indexedRef || diff.Truncated || diff.Patch == "" {
			t.Fatalf("diff = %+v, error=%v content=%v", diff, res.IsError, res.Content)
		}

		zeroContext, res := callTool[phebssync.DiffResult](t, s, "diff", map[string]any{
			"repo": repo.Name, "base": indexedRef, "head": advancedRef, "context_lines": 0,
		})
		if res.IsError || !strings.Contains(zeroContext.Patch, "-l2\n+changed") ||
			strings.Contains(zeroContext.Patch, "\n l1\n") || strings.Contains(zeroContext.Patch, "\n l3\n") {
			t.Fatalf("zero-context MCP diff = %+v, error=%v content=%v", zeroContext, res.IsError, res.Content)
		}
	})
}

func TestEpic9ToolsRejectDeletingAndUnindexedRepos(t *testing.T) {
	repos := []store.Repo{
		{Name: "example.com/deleting", IndexedCommitHash: strings.Repeat("a", 40), Deleting: true},
		{Name: "example.com/unindexed"},
	}
	st := fakeStore{repos: repos}
	dataDir := t.TempDir()
	s := NewServer(Options{
		Version: "test", Store: st, DataDir: dataDir,
		CodeNav: codenav.New(codenav.Options{DataDir: dataDir}),
	})

	tools := []struct {
		name string
		args map[string]any
	}{
		{"find_definitions", map[string]any{"path": "x.go", "line": 0, "character": 0, "ref": strings.Repeat("b", 40)}},
		{"find_references", map[string]any{"path": "x.go", "line": 0, "character": 0, "ref": strings.Repeat("b", 40)}},
		{"hover", map[string]any{"path": "x.go", "line": 0, "character": 0, "ref": strings.Repeat("b", 40)}},
		{"blame", map[string]any{"path": "x.go", "ref": "main"}},
		{"list_commits", map[string]any{"ref": "main"}},
		{"get_commit", map[string]any{"ref": "main"}},
		{"diff", map[string]any{"head": "main"}},
	}
	for _, repo := range repos {
		for _, tool := range tools {
			t.Run(repo.Name+"/"+tool.name, func(t *testing.T) {
				args := make(map[string]any, len(tool.args)+1)
				for key, value := range tool.args {
					args[key] = value
				}
				args["repo"] = repo.Name
				_, result := callTool[map[string]any](t, s, tool.name, args)
				if !result.IsError {
					t.Fatalf("%s accepted repo %+v with explicit revision", tool.name, repo)
				}
			})
		}
	}
}

func writeCodeNavFixture(t *testing.T, origin string) {
	t.Helper()
	for _, path := range []string{"lib/rocket.go", "use/use.go"} {
		data, err := os.ReadFile(filepath.Join("..", "codenav", "testdata", "repo", path))
		if err != nil {
			t.Fatal(err)
		}
		full := filepath.Join(origin, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	jsonIndex, err := os.ReadFile(filepath.Join("..", "codenav", "testdata", "index.scip.json"))
	if err != nil {
		t.Fatal(err)
	}
	index := &scip.Index{}
	if err := protojson.Unmarshal(jsonIndex, index); err != nil {
		t.Fatal(err)
	}
	data, err := proto.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, codenav.DefaultIndexPath), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// sliceLines is pure — the range/cap arithmetic is the fiddly part of
// read_file, so it gets its own table.
func TestSliceLines(t *testing.T) {
	content := "one\ntwo\nthree\nfour\nfive\n"
	tests := []struct {
		name       string
		start, end int
		want       string
		wantStart  int
		wantEnd    int
	}{
		{"whole file", 0, 0, "one\ntwo\nthree\nfour\nfive", 1, 5},
		{"range", 2, 4, "two\nthree\nfour", 2, 4},
		{"single line", 3, 3, "three", 3, 3},
		{"start beyond EOF clamps", 99, 0, "five", 5, 5},
		{"end beyond EOF clamps", 4, 99, "four\nfive", 4, 5},
		{"end before start clamps", 4, 2, "four\nfive", 4, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sliceLines(content, tt.start, tt.end)
			if got.Content != tt.want || got.StartLine != tt.wantStart || got.EndLine != tt.wantEnd || got.TotalLine != 5 {
				t.Errorf("sliceLines(%d,%d) = %+v, want content %q lines %d-%d/5",
					tt.start, tt.end, got, tt.want, tt.wantStart, tt.wantEnd)
			}
			if got.Truncated {
				t.Error("unexpectedly truncated")
			}
		})
	}
}

func TestSliceLinesCap(t *testing.T) {
	long := strings.Repeat(strings.Repeat("x", 999)+"\n", 300) // 300 KB of 1 KB lines
	got := sliceLines(long, 0, 0)
	if !got.Truncated {
		t.Fatal("300KB file not truncated at the 200KB cap")
	}
	if len(got.Content) > maxFileBytes {
		t.Errorf("content %d bytes exceeds cap %d", len(got.Content), maxFileBytes)
	}
	if got.EndLine >= got.TotalLine {
		t.Errorf("EndLine %d should stop short of TotalLine %d", got.EndLine, got.TotalLine)
	}
	// the delivered range must be re-requestable
	if got.StartLine != 1 || got.EndLine < 1 {
		t.Errorf("bad delivered range %d-%d", got.StartLine, got.EndLine)
	}
}

// Epic 8 review: a single line larger than the cap must NOT be returned
// whole — the pre-fix first-line escape hatch bypassed the cap entirely.
func TestSliceLinesGiantLine(t *testing.T) {
	giant := strings.Repeat("y", 5<<20) + "\n" // one 5 MiB line
	got := sliceLines(giant, 0, 0)
	if !got.Truncated {
		t.Fatal("5MiB single line reported as not truncated")
	}
	if len(got.Content) > maxFileBytes {
		t.Fatalf("content %d bytes exceeds cap %d — giant line bypassed the cap", len(got.Content), maxFileBytes)
	}
	if !utf8.ValidString(got.Content) {
		t.Error("truncation split a rune")
	}
}

// Epic 8 review: an empty file has zero lines, not one.
func TestSliceLinesEmpty(t *testing.T) {
	got := sliceLines("", 0, 0)
	if got.TotalLine != 0 || got.Content != "" || got.Truncated {
		t.Errorf("empty file = %+v, want zero lines / empty / not truncated", got)
	}
}

// TestToolSchemas: every tool has an object schema (the SDK enforces the rest
// of the contract), including Epic 9's code-navigation and history tools.
func TestToolSchemas(t *testing.T) {
	ctx := t.Context()
	s := NewServer(Options{Version: "test"})
	st, ct := sdk.NewInMemoryTransports()
	go func() { _, _ = s.Connect(ctx, st, nil) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	tools, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	schemas := map[string]string{}
	for _, tool := range tools.Tools {
		got[tool.Name] = true
		js, _ := json.Marshal(tool.InputSchema)
		schemas[tool.Name] = string(js)
		if !strings.Contains(string(js), `"object"`) {
			t.Errorf("%s input schema not an object: %s", tool.Name, js)
		}
	}
	wantTools := []string{
		"search_code", "read_file", "list_repos",
		"find_definitions", "find_references", "hover",
		"blame", "list_commits", "get_commit", "diff",
	}
	for _, want := range wantTools {
		if !got[want] {
			t.Errorf("tool %s missing (have %v)", want, got)
		}
	}
	if len(got) != len(wantTools) {
		t.Errorf("tool set = %v, want exactly %v", got, wantTools)
	}
	for _, name := range []string{"find_definitions", "find_references", "hover"} {
		if !strings.Contains(schemas[name], "UTF-16") {
			t.Errorf("%s schema does not specify UTF-16 positions: %s", name, schemas[name])
		}
		if strings.Contains(schemas[name], `"encoding"`) {
			t.Errorf("%s exposes a caller-selectable encoding: %s", name, schemas[name])
		}
	}
}
