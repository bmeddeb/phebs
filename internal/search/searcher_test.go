package search_test

// End-to-end searcher tests: fixture repos → mirrors → real zoekt-git-index
// child → shards → Searcher. Goldens under testdata/; refresh with -update.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/indexer"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/sync"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestOpenRejectsGlobMetacharacters(t *testing.T) {
	if _, err := search.Open(filepath.Join(t.TempDir(), "data[*]", "index"), nil); err == nil {
		t.Fatal("Open accepted a glob-bearing index path")
	}
}

func TestOpenRejectsSymlinkedShard(t *testing.T) {
	indexDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.zoekt")
	if err := os.WriteFile(target, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(indexDir, "linked.zoekt")); err != nil {
		t.Fatal(err)
	}
	if _, err := search.Open(indexDir, nil); err == nil {
		t.Fatal("Open accepted a symlinked shard")
	}
}

func TestMain(m *testing.M) {
	flag.Parse()
	if _, err := indexer.FindBinary(); err != nil {
		dir, err := os.MkdirTemp("", "phebs-zoekt")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		bin := filepath.Join(dir, "zoekt-git-index")
		out, err := exec.Command("go", "build", "-o", bin,
			"github.com/sourcegraph/zoekt/cmd/zoekt-git-index").CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build zoekt-git-index: %v\n%s", err, out)
			os.Exit(1)
		}
		_ = os.Setenv("PHEBS_ZOEKT_GIT_INDEX", bin)
	}
	os.Exit(m.Run())
}

func gitc(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// corpus builds two indexed repos: "plain" and "forked" (IsFork in the DB).
func corpus(t *testing.T, ctx context.Context) *search.Searcher {
	s, _ := corpusWithStore(t, ctx)
	return s
}

func corpusWithStore(t *testing.T, ctx context.Context) (*search.Searcher, store.Store) {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	dataDir := t.TempDir()
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	bin, err := indexer.FindBinary()
	if err != nil {
		t.Fatal(err)
	}
	ix := &indexer.Indexer{DataDir: dataDir, Bin: bin, Store: st}

	fixtures := []struct {
		repo  string
		fork  bool
		files map[string]string
	}{
		{"example.com/plain", false, map[string]string{
			"greet.go": "package main\n\n// phebsNeedle lives here\nfunc greet() string { return \"hi\" }\n",
			"doc.md":   "# docs\n\nphebsNeedle appears in prose too.\n",
		}},
		{"example.com/forked", true, map[string]string{
			"fork.go": "package fork\n\nvar phebsNeedle = 42\n",
		}},
	}
	for _, f := range fixtures {
		origin := t.TempDir()
		gitc(t, origin, "init", "-b", "main")
		names := make([]string, 0, len(f.files))
		for name := range f.files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(origin, name), []byte(f.files[name]), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		gitc(t, origin, "add", ".")
		gitc(t, origin, "commit", "-m", "fixture")

		if err := sync.Mirror(ctx, "file://"+origin, sync.RepoDir(dataDir, f.repo)); err != nil {
			t.Fatal(err)
		}
		repo := store.Repo{Name: f.repo, CloneURL: "file://" + origin, IsFork: f.fork, IsPublic: true}
		if err := st.UpsertRepo(ctx, repo); err != nil {
			t.Fatal(err)
		}
		if err := ix.Index(ctx, repo, false); err != nil {
			t.Fatalf("index %s: %v", f.repo, err)
		}
	}

	s, err := search.Open(filepath.Join(dataDir, "index"), st)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, st
}

// T4.2 AC: golden-file tests over a fixture corpus.
func TestSearchGolden(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	s := corpus(t, ctx)

	tests := []struct {
		name   string
		q      string
		golden string
	}{
		{"basic", "phebsNeedle", "golden_basic.json"},
		{"repo filtered", "phebsNeedle fork:no", "golden_no_forks.json"},
		{"lang filtered", "phebsNeedle lang:go repo:plain", "golden_lang_go.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := s.Search(ctx, tt.q, search.Options{})
			if err != nil {
				t.Fatal(err)
			}
			for i := range res.Files {
				if res.Files[i].Ref == "" {
					t.Errorf("%s: search result %s/%s has no indexed ref", tt.q, res.Files[i].Repo, res.Files[i].Path)
				}
				res.Files[i].Ref = "" // commit hashes include fixture commit time; goldens cover the remaining wire shape
			}
			res.Stats.DurationMS = 0 // nondeterministic
			sort.Slice(res.Files, func(i, j int) bool {
				a, b := res.Files[i], res.Files[j]
				return a.Repo+a.Path < b.Repo+b.Path
			})
			got, err := json.MarshalIndent(res, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", tt.golden)
			if *update {
				if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing golden (run with -update): %v", err)
			}
			if string(append(got, '\n')) != string(want) {
				t.Errorf("golden mismatch for %q:\n got: %s\nwant: %s", tt.q, got, want)
			}
		})
	}
}

// Regression: a NEGATED metadata filter must exclude the matching repos, not
// collapse the whole query to FALSE. Pre-fix, Compile hoisted the RawConfig
// atom globally and `-fork:yes` simplified to zero results.
func TestSearchNegatedFilter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	s := corpus(t, ctx) // fixtures: example.com/plain (not fork), example.com/forked (fork)

	// -fork:yes == "not a fork": the fork must be excluded, plain kept.
	res, err := s.Search(ctx, "phebsNeedle -fork:yes", search.Options{})
	if err != nil {
		t.Fatal(err)
	}
	repos := map[string]bool{}
	for _, f := range res.Files {
		repos[f.Repo] = true
	}
	if len(res.Files) == 0 {
		t.Fatal("negated filter returned zero results (the pre-fix FALSE-collapse bug)")
	}
	if !repos["example.com/plain"] {
		t.Error("expected example.com/plain in results")
	}
	if repos["example.com/forked"] {
		t.Error("example.com/forked should be excluded by -fork:yes")
	}

	// An OR'd metadata atom must keep matches from the non-metadata branch.
	res, err = s.Search(ctx, "(fork:yes or lang:go) phebsNeedle", search.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) == 0 {
		t.Error("OR'd metadata query returned zero results")
	}
}

// T8.1 AC: context:<name> restricts results to the named repo set, end to
// end over real shards.
func TestSearchContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	s := corpus(t, ctx)
	s.Contexts = map[string][]string{
		"plainset": {"example.com/plain"},
		"all":      {"example.com/*"},
	}

	res, err := s.Search(ctx, "phebsNeedle context:plainset", search.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) == 0 {
		t.Fatal("context-scoped search returned nothing")
	}
	for _, f := range res.Files {
		if f.Repo != "example.com/plain" {
			t.Errorf("context:plainset leaked %s/%s", f.Repo, f.Path)
		}
	}

	// glob context matches both fixture repos
	res, err = s.Search(ctx, "phebsNeedle context:all", search.Options{})
	if err != nil {
		t.Fatal(err)
	}
	repos := map[string]bool{}
	for _, f := range res.Files {
		repos[f.Repo] = true
	}
	if !repos["example.com/plain"] || !repos["example.com/forked"] {
		t.Errorf("context:all matched %v, want both fixture repos", repos)
	}

	if _, err := s.Search(ctx, "phebsNeedle context:missing", search.Options{}); err == nil {
		t.Error("unknown context should error")
	}
}

func TestSearchExcludesShardWithoutRepoRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	s, st := corpusWithStore(t, ctx)
	repo, err := st.GetRepo(ctx, "example.com/forked")
	if err != nil {
		t.Fatal(err)
	}
	indexedHash := repo.IndexedCommitHash
	if err := st.SetRepoIndexed(ctx, "example.com/forked", "mismatched-revision", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertHidden := func(stage string) {
		res, err := s.Search(ctx, "phebsNeedle", search.Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range res.Files {
			if file.Repo == "example.com/forked" {
				t.Fatalf("%s shard leaked into results: %+v", stage, file)
			}
		}
	}
	assertHidden("revision mismatch")
	if err := st.ClearRepoIndexState(ctx, "example.com/forked"); err != nil {
		t.Fatal(err)
	}
	assertHidden("unindexed")
	if err := st.SetRepoIndexed(ctx, "example.com/forked", indexedHash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoDeleting(ctx, "example.com/forked", true); err != nil {
		t.Fatal(err)
	}
	assertHidden("deleting")
	if err := st.DeleteRepo(ctx, "example.com/forked"); err != nil {
		t.Fatal(err)
	}
	assertHidden("untracked")
}

// T4.3: streaming forwards batches progressively and aggregates stats;
// a cancelled context stops the search.
func TestStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	s := corpus(t, ctx)

	var batches []*search.Result
	stats, err := s.Stream(ctx, "phebsNeedle", search.Options{}, func(r *search.Result) {
		batches = append(batches, r)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) == 0 {
		t.Fatal("no batches streamed")
	}
	if stats.MatchCount != 3 {
		t.Errorf("aggregate MatchCount = %d, want 3", stats.MatchCount)
	}

	// cancellation propagates
	dead, kill := context.WithCancel(ctx)
	kill()
	if _, err := s.Stream(dead, "phebsNeedle", search.Options{}, func(*search.Result) {}); err == nil {
		t.Error("cancelled context: want error, got nil")
	}
}

// T4.2 AC: p50 latency on the fixture corpus, recorded in PLAN.md.
func TestSearchLatencyP50(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	s := corpus(t, ctx)

	const runs = 100
	lat := make([]time.Duration, runs)
	for i := range runs {
		t0 := time.Now()
		if _, err := s.Search(ctx, "phebsNeedle", search.Options{}); err != nil {
			t.Fatal(err)
		}
		lat[i] = time.Since(t0)
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p50, p95 := lat[runs/2], lat[runs*95/100]
	t.Logf("fixture corpus search: p50=%v p95=%v", p50, p95)
	if p50 > 50*time.Millisecond {
		t.Errorf("p50 = %v exceeds the 50ms budget (PLAN.md)", p50)
	}
}
