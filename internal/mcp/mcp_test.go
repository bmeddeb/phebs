package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

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
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "fixture")

	dataDir := t.TempDir()
	repo := store.Repo{Name: "example.com/demo", DefaultBranch: "main", IsPublic: true}
	if err := phebssync.Mirror(t.Context(), "file://"+origin, phebssync.RepoDir(dataDir, repo.Name)); err != nil {
		t.Fatal(err)
	}
	st := fakeStore{repos: []store.Repo{repo}}
	s := NewServer(Options{Version: "test", Store: st, DataDir: dataDir})

	t.Run("list_repos", func(t *testing.T) {
		out, res := callTool[reposOut](t, s, "list_repos", nil)
		if res.IsError {
			t.Fatalf("list_repos errored: %v", res.Content)
		}
		if len(out.Repos) != 1 || out.Repos[0].Name != "example.com/demo" {
			t.Errorf("list_repos = %+v", out.Repos)
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

// TestToolSchemas: the server exposes exactly the three tools with object
// schemas (the SDK enforces the rest of the contract).
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
	for _, tool := range tools.Tools {
		got[tool.Name] = true
		js, _ := json.Marshal(tool.InputSchema)
		if !strings.Contains(string(js), `"object"`) {
			t.Errorf("%s input schema not an object: %s", tool.Name, js)
		}
	}
	for _, want := range []string{"search_code", "read_file", "list_repos"} {
		if !got[want] {
			t.Errorf("tool %s missing (have %v)", want, got)
		}
	}
}
