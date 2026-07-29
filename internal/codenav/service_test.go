package codenav

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const fixtureRepo = "example.com/acme/fixture"

func TestDefinitionReferencesAndHover(t *testing.T) {
	fixture := newFixture(t, true)
	service := New(Options{DataDir: fixture.dataDir})
	ctx := context.Background()

	definition, err := service.Definition(ctx, Query{
		Repo: fixtureRepo, Revision: fixture.revision, Path: "use/use.go",
		Line: 1, Character: 29, Encoding: EncodingUTF16,
	})
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if !definition.Available || definition.Location == nil {
		t.Fatalf("Definition = %#v, want available location", definition)
	}
	if got, want := definition.Location.Path, "lib/rocket.go"; got != want {
		t.Fatalf("definition path = %q, want %q", got, want)
	}
	if got, want := definition.Location.Revision, fixture.revision; got != want {
		t.Fatalf("definition revision = %q, want %q", got, want)
	}
	if got, want := definition.Location.Range, (Range{Start: Position{Line: 1, Character: 5}, End: Position{Line: 1, Character: 11}}); got != want {
		t.Fatalf("definition range = %#v, want %#v", got, want)
	}

	references, err := service.References(ctx, Query{
		Repo: fixtureRepo, Revision: fixture.revision, Path: "lib/rocket.go",
		Line: 1, Character: 6, Encoding: EncodingUTF16,
	})
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if !references.Available || len(references.Locations) != 1 {
		t.Fatalf("References = %#v, want one location", references)
	}
	if got, want := references.Locations[0].Range, (Range{Start: Position{Line: 1, Character: 28}, End: Position{Line: 1, Character: 34}}); got != want {
		t.Fatalf("reference UTF-16 range = %#v, want %#v", got, want)
	}

	hover, err := service.Hover(ctx, Query{
		Repo: fixtureRepo, Revision: fixture.revision, Path: "use/use.go",
		Line: 1, Character: 29, Encoding: EncodingUTF16,
	})
	if err != nil {
		t.Fatalf("Hover: %v", err)
	}
	if !hover.Available || hover.Hover == nil {
		t.Fatalf("Hover = %#v, want hover data", hover)
	}
	if hover.Hover.DisplayName != "Rocket" || hover.Hover.Signature != "func Rocket() string" ||
		len(hover.Hover.Documentation) != 1 || hover.Hover.Documentation[0] != "Rocket returns a launch status." {
		t.Fatalf("Hover = %#v", hover.Hover)
	}
	if got, want := hover.Hover.Range, (Range{Start: Position{Line: 1, Character: 28}, End: Position{Line: 1, Character: 34}}); got != want {
		t.Fatalf("hover UTF-16 range = %#v, want %#v", got, want)
	}
}

func TestUnsupportedSCIPDocumentDoesNotPoisonNavigation(t *testing.T) {
	fixture := newFixture(t, true)
	index := readFixtureIndex(t)
	validOccurrences := 0
	for _, doc := range index.GetDocuments() {
		validOccurrences += len(doc.GetOccurrences())
	}
	index.Documents = append([]*scip.Document{{
		RelativePath:     "bad/unknown.go",
		PositionEncoding: scip.PositionEncoding_UnspecifiedPositionEncoding,
		Occurrences: []*scip.Occurrence{{
			Range:  []int32{0, 0, 1},
			Symbol: index.GetDocuments()[0].GetOccurrences()[0].GetSymbol(),
		}},
	}}, index.Documents...)
	if err := os.MkdirAll(filepath.Join(fixture.origin, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.origin, "bad", "unknown.go"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeIndex(t, fixture.origin, index)
	revision := fixture.commit(t, "unsupported SCIP document encoding")
	fixture.fetch(t)

	service := New(Options{DataDir: fixture.dataDir})
	availability, err := service.Ingest(context.Background(), fixtureRepo, revision)
	if err != nil || !availability.Available {
		t.Fatalf("Ingest = %#v, %v", availability, err)
	}
	if availability.Documents != len(index.GetDocuments())-1 ||
		availability.Occurrences != validOccurrences {
		t.Fatalf("availability = %#v", availability)
	}

	definition, err := service.Definition(context.Background(), Query{
		Repo: fixtureRepo, Revision: revision, Path: "use/use.go",
		Line: 1, Character: 29,
	})
	if err != nil || definition.Location == nil ||
		definition.Location.Path != "lib/rocket.go" {
		t.Fatalf("Definition = %#v, %v", definition, err)
	}
	hover, err := service.Hover(context.Background(), Query{
		Repo: fixtureRepo, Revision: revision, Path: "use/use.go",
		Line: 1, Character: 29,
	})
	if err != nil || hover.Hover == nil || hover.Hover.DisplayName != "Rocket" {
		t.Fatalf("Hover = %#v, %v", hover, err)
	}
	skipped, err := service.Definition(context.Background(), Query{
		Repo: fixtureRepo, Revision: revision, Path: "bad/unknown.go",
		Line: 0, Character: 0,
	})
	if err != nil || !skipped.Available || skipped.Location != nil {
		t.Fatalf("skipped document Definition = %#v, %v", skipped, err)
	}
}

func TestAbsentIndexIsGraceful(t *testing.T) {
	fixture := newFixture(t, false)
	service := New(Options{DataDir: fixture.dataDir})
	q := Query{Repo: fixtureRepo, Revision: fixture.revision, Path: "lib/rocket.go", Line: 1, Character: 6}

	definition, err := service.Definition(context.Background(), q)
	if err != nil {
		t.Fatalf("Definition without index: %v", err)
	}
	if definition.Available || definition.Location != nil {
		t.Fatalf("Definition without index = %#v", definition)
	}
	references, err := service.References(context.Background(), q)
	if err != nil {
		t.Fatalf("References without index: %v", err)
	}
	if references.Available || len(references.Locations) != 0 {
		t.Fatalf("References without index = %#v", references)
	}
	hover, err := service.Hover(context.Background(), q)
	if err != nil {
		t.Fatalf("Hover without index: %v", err)
	}
	if hover.Available || hover.Hover != nil {
		t.Fatalf("Hover without index = %#v", hover)
	}
}

func TestRevisionAndPathValidation(t *testing.T) {
	fixture := newFixture(t, true)
	service := New(Options{DataDir: fixture.dataDir})
	base := Query{Repo: fixtureRepo, Revision: fixture.revision, Path: "lib/rocket.go", Line: 1, Character: 6}
	tests := []struct {
		name  string
		query Query
	}{
		{name: "symbolic revision", query: withQuery(base, func(q *Query) { q.Revision = "HEAD" })},
		{name: "short revision", query: withQuery(base, func(q *Query) { q.Revision = fixture.revision[:12] })},
		{name: "path traversal", query: withQuery(base, func(q *Query) { q.Path = "../index.scip" })},
		{name: "absolute path", query: withQuery(base, func(q *Query) { q.Path = "/etc/passwd" })},
		{name: "noncanonical path", query: withQuery(base, func(q *Query) { q.Path = "lib//rocket.go" })},
		{name: "negative line", query: withQuery(base, func(q *Query) { q.Line = -1 })},
		{name: "unknown encoding", query: withQuery(base, func(q *Query) { q.Encoding = "bytes" })},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Definition(context.Background(), test.query); err == nil {
				t.Fatal("Definition accepted invalid query")
			} else if !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrUnsupportedEncoding) {
				t.Fatalf("Definition error = %v", err)
			}
		})
	}
}

func TestReindexAndCleanupReplaceSnapshot(t *testing.T) {
	fixture := newFixture(t, true)
	service := New(Options{DataDir: fixture.dataDir})
	ctx := context.Background()
	q := Query{Repo: fixtureRepo, Revision: fixture.revision, Path: "use/use.go", Line: 1, Character: 29}

	first, err := service.Hover(ctx, q)
	if err != nil || first.Hover == nil || first.Hover.DisplayName != "Rocket" {
		t.Fatalf("initial Hover = %#v, err %v", first, err)
	}

	index := readFixtureIndex(t)
	for _, doc := range index.GetDocuments() {
		for _, occurrence := range doc.GetOccurrences() {
			occurrence.Symbol = strings.ReplaceAll(occurrence.Symbol, "Rocket", "Launch")
		}
		for _, info := range doc.GetSymbols() {
			info.Symbol = strings.ReplaceAll(info.Symbol, "Rocket", "Launch")
			info.DisplayName = strings.ReplaceAll(info.DisplayName, "Rocket", "Launch")
			info.Documentation = []string{"Launch returns a launch status."}
			if info.SignatureDocumentation != nil {
				info.SignatureDocumentation.Text = strings.ReplaceAll(info.SignatureDocumentation.Text, "Rocket", "Launch")
			}
		}
	}
	replaceInFile(t, filepath.Join(fixture.origin, "lib/rocket.go"), "Rocket", "Launch")
	replaceInFile(t, filepath.Join(fixture.origin, "use/use.go"), "Rocket", "Launch")
	writeIndex(t, fixture.origin, index)
	revision2 := fixture.commit(t, "replace SCIP snapshot")
	fixture.fetch(t)

	availability, err := service.Ingest(ctx, fixtureRepo, revision2)
	if err != nil || !availability.Available {
		t.Fatalf("Ingest revision 2 = %#v, err %v", availability, err)
	}
	q.Revision = revision2
	second, err := service.Hover(ctx, q)
	if err != nil || second.Hover == nil || second.Hover.DisplayName != "Launch" {
		t.Fatalf("replacement Hover = %#v, err %v", second, err)
	}

	git(t, fixture.origin, "rm", DefaultIndexPath)
	revision3 := fixture.commit(t, "remove SCIP index")
	fixture.fetch(t)
	availability, err = service.Ingest(ctx, fixtureRepo, revision3)
	if err != nil || availability.Available {
		t.Fatalf("Ingest absent revision = %#v, err %v", availability, err)
	}
	q.Revision = revision3
	absent, err := service.Definition(ctx, q)
	if err != nil || absent.Available {
		t.Fatalf("Definition after index removal = %#v, err %v", absent, err)
	}
	if err := service.Remove(fixtureRepo); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := service.Remove(fixtureRepo); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

func TestReferencesAreDeterministicallyBounded(t *testing.T) {
	fixture := newFixture(t, true)
	index := readFixtureIndex(t)
	symbol := index.GetDocuments()[0].GetOccurrences()[0].GetSymbol()
	referenceCount := MaxReferenceLocations*4 + 1
	for _, doc := range index.GetDocuments() {
		if doc.GetRelativePath() != "use/use.go" {
			continue
		}
		doc.Occurrences = make([]*scip.Occurrence, 0, referenceCount)
		for i := int32(referenceCount - 1); i >= 0; i-- {
			doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{
				Range: []int32{1, i, i + 1}, Symbol: symbol,
			})
		}
	}
	if err := os.WriteFile(
		filepath.Join(fixture.origin, "use/use.go"),
		[]byte("package use\n"+strings.Repeat("R", referenceCount+1)+"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	writeIndex(t, fixture.origin, index)
	revision := fixture.commit(t, "many SCIP references")
	fixture.fetch(t)

	service := New(Options{DataDir: fixture.dataDir})
	result, err := service.References(context.Background(), Query{
		Repo: fixtureRepo, Revision: revision, Path: "lib/rocket.go",
		Line: 1, Character: 6,
	})
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if !result.Available || !result.Truncated || len(result.Locations) != MaxReferenceLocations {
		t.Fatalf("References = available %t, truncated %t, count %d", result.Available, result.Truncated, len(result.Locations))
	}
	last := result.Locations[len(result.Locations)-1]
	if last.Range.Start.Character != MaxReferenceLocations-1 {
		t.Fatalf("last deterministic reference starts at %d, want %d", last.Range.Start.Character, MaxReferenceLocations-1)
	}
}

func TestUTF16PositionConversion(t *testing.T) {
	line := []byte(`"🚀" Rocket`)
	got, err := convertCharacter(line, 5, EncodingUTF16, EncodingUTF8)
	if err != nil {
		t.Fatalf("convert UTF-16 to UTF-8: %v", err)
	}
	if got != 7 {
		t.Fatalf("UTF-8 offset = %d, want 7", got)
	}
	got, err = convertCharacter(line, 7, EncodingUTF8, EncodingUTF16)
	if err != nil || got != 5 {
		t.Fatalf("UTF-16 offset = %d, err %v, want 5", got, err)
	}
	if _, err := convertCharacter(line, 2, EncodingUTF16, EncodingUTF8); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("split surrogate error = %v, want ErrInvalidInput", err)
	}
	if _, err := convertCharacter(line, 2, EncodingUTF16, EncodingUTF16); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("same-encoding split surrogate error = %v, want ErrInvalidInput", err)
	}
}

func TestCanceledParseAndIngestPreserveUsableSnapshot(t *testing.T) {
	index := readFixtureIndex(t)
	data, err := proto.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parseSnapshot(canceled, data, newParseLimits(Options{})); !errors.Is(err, context.Canceled) {
		t.Fatalf("parseSnapshot cancellation = %v", err)
	}

	fixture := newFixture(t, true)
	service := New(Options{DataDir: fixture.dataDir})
	q := Query{Repo: fixtureRepo, Revision: fixture.revision, Path: "lib/rocket.go", Line: 1, Character: 6}
	if result, err := service.Hover(context.Background(), q); err != nil || result.Hover == nil {
		t.Fatalf("prime Hover = %#v, err %v", result, err)
	}
	if _, err := service.Ingest(canceled, fixtureRepo, fixture.revision); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Ingest error = %v", err)
	}
	if result, err := service.Hover(context.Background(), q); err != nil || result.Hover == nil {
		t.Fatalf("cached Hover after cancellation = %#v, err %v", result, err)
	}
}

type fixture struct {
	t        *testing.T
	dataDir  string
	origin   string
	revision string
}

func newFixture(t *testing.T, withIndex bool) *fixture {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir("testdata/repo", func(source string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel("testdata/repo", source)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(origin, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}); err != nil {
		t.Fatalf("copy fixture repo: %v", err)
	}
	git(t, origin, "init", "-b", "main")
	git(t, origin, "config", "user.name", "Phebs Test")
	git(t, origin, "config", "user.email", "phebs@example.invalid")
	if withIndex {
		writeIndex(t, origin, readFixtureIndex(t))
	}
	git(t, origin, "add", ".")
	git(t, origin, "commit", "-m", "fixture")
	revision := git(t, origin, "rev-parse", "HEAD")
	dataDir := filepath.Join(root, "data")
	if err := phebssync.Mirror(context.Background(), "file://"+origin, phebssync.RepoDir(dataDir, fixtureRepo)); err != nil {
		t.Fatalf("mirror fixture: %v", err)
	}
	return &fixture{t: t, dataDir: dataDir, origin: origin, revision: revision}
}

func (f *fixture) commit(t *testing.T, message string) string {
	t.Helper()
	git(t, f.origin, "add", "-A")
	git(t, f.origin, "commit", "-m", message)
	return git(t, f.origin, "rev-parse", "HEAD")
}

func (f *fixture) fetch(t *testing.T) {
	t.Helper()
	if err := phebssync.Mirror(context.Background(), "file://"+f.origin, phebssync.RepoDir(f.dataDir, fixtureRepo)); err != nil {
		t.Fatalf("fetch fixture: %v", err)
	}
}

func readFixtureIndex(t *testing.T) *scip.Index {
	t.Helper()
	data, err := os.ReadFile("testdata/index.scip.json")
	if err != nil {
		t.Fatal(err)
	}
	index := &scip.Index{}
	if err := protojson.Unmarshal(data, index); err != nil {
		t.Fatalf("parse fixture SCIP JSON: %v", err)
	}
	return index
}

func writeIndex(t *testing.T, repo string, index *scip.Index) {
	t.Helper()
	data, err := proto.Marshal(index)
	if err != nil {
		t.Fatalf("marshal fixture SCIP: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, DefaultIndexPath), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceInFile(t *testing.T, filePath, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(strings.ReplaceAll(string(data), old, replacement)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withQuery(input Query, update func(*Query)) Query {
	update(&input)
	return input
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
