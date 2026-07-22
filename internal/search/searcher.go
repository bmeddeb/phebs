package search

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
	zoektsearch "github.com/sourcegraph/zoekt/search"

	"github.com/bmeddeb/phebs/internal/store"
)

// Searcher serves queries over the shard directory, in-process
// (zoekt/search.NewDirectorySearcher — the package upstream renamed from
// shards). One instance lives for the process lifetime; the underlying
// searcher watches the directory and picks up new shards as the indexer
// writes them.
type Searcher struct {
	z  zoekt.Streamer
	st store.Store
	// Contexts backs `context:<name>` filters (T8.1); assigned once at
	// startup from config.
	Contexts map[string][]string
	// Usage, when set, receives one event per successfully completed search
	// (T10.2). This single hook covers REST, SSE, and MCP, which all funnel
	// through Search/Stream. Assigned once at startup; the callback resolves
	// the actor and must never block on failure.
	Usage func(ctx context.Context, event store.UsageEvent)
	// Visible is the per-user RepoSet hook (T10.3), filling the reservation
	// noted in CLAUDE.md. Called once per query with the request context, it
	// returns the caller's repo predicate — or nil when the caller may see
	// everything (administrators). A nil field disables permission filtering.
	// Filtering happens here, in the pre-pass, so REST, SSE, and MCP inherit
	// it and nothing is post-filtered.
	Visible func(ctx context.Context) func(store.Repo) bool
}

// usageRepoCap bounds the distinct repo names recorded per search, in result
// (relevance) order.
const usageRepoCap = 20

func Open(indexDir string, st store.Store) (*Searcher, error) {
	// NewDirectorySearcher uses filepath.Glob internally. Reject metacharacters
	// at this boundary too so library callers cannot mount sibling shards.
	if strings.ContainsAny(indexDir, `*?[\`) {
		return nil, fmt.Errorf("open shard dir: path contains glob metacharacters")
	}
	if info, err := os.Lstat(indexDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("open shard dir: managed index path is not a real directory")
		}
		entries, readErr := os.ReadDir(indexDir)
		if readErr != nil {
			return nil, fmt.Errorf("open shard dir: %w", readErr)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".zoekt") && entry.Type()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("open shard dir: symlinked shard %q", entry.Name())
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("open shard dir: %w", err)
	}
	z, err := zoektsearch.NewDirectorySearcher(indexDir)
	if err != nil {
		return nil, fmt.Errorf("open shard dir %s: %w", indexDir, err)
	}
	return &Searcher{z: z, st: st}, nil
}

func (s *Searcher) Close() { s.z.Close() }

// Options bound the work a single query may do.
type Options struct {
	MaxMatches   int // documents shown; default 50, cap 500
	ContextLines int // lines around each match; default 0, cap 10
}

func (o Options) zoekt() *zoekt.SearchOptions {
	max := clamp(o.MaxMatches, 50, 500)
	return &zoekt.SearchOptions{
		ChunkMatches:       true,
		MaxDocDisplayCount: max, // documents returned to the client
		// TotalMaxMatchCount bounds total matches COLLECTED across shards, not
		// documents shown. It must be >> MaxDocDisplayCount or a common term
		// fills the budget inside the first shard and the remaining
		// shards/repos are never searched (zoekt's own default is 1,000,000).
		// Generous safety cap; MaxWallTime is the real backstop.
		TotalMaxMatchCount: 100000,
		NumContextLines:    clamp(o.ContextLines, 0, 10),
		MaxWallTime:        10 * time.Second,
	}
}

func clamp(v, def, max int) int {
	switch {
	case v <= 0:
		return def
	case v > max:
		return max
	}
	return v
}

// Result is the wire shape of a search response.
type Result struct {
	Files []FileResult `json:"files"`
	Stats Stats        `json:"stats"`
}

type FileResult struct {
	Repo     string  `json:"repo"`
	Path     string  `json:"path"`
	Ref      string  `json:"ref,omitempty"` // immutable commit indexed by zoekt
	Language string  `json:"language,omitempty"`
	Chunks   []Chunk `json:"chunks"`
}

// Chunk is a contiguous run of lines containing one or more matches.
type Chunk struct {
	Content   string  `json:"content"`
	StartLine int     `json:"start_line"` // 1-based line of Content's first line
	Ranges    []Range `json:"ranges"`
}

// Range is a match location, 1-based, relative to the file.
type Range struct {
	StartLine int `json:"start_line"`
	StartCol  int `json:"start_col"`
	EndLine   int `json:"end_line"`
	EndCol    int `json:"end_col"`
}

type Stats struct {
	MatchCount int   `json:"match_count"`
	FileCount  int   `json:"file_count"`
	DurationMS int64 `json:"duration_ms"`
}

// Search compiles raw (T4.1 pre-pass included) and runs one bounded search.
func (s *Searcher) Search(ctx context.Context, raw string, opts Options) (*Result, error) {
	q, versions, err := s.compile(ctx, raw)
	if err != nil {
		return nil, err
	}
	res, err := s.z.Search(ctx, q, opts.zoekt())
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	result := toResult(res, versions)
	if s.Usage != nil {
		repos := newRepoCollector()
		for _, f := range result.Files {
			repos.add(f.Repo)
		}
		s.Usage(ctx, usageEvent(result.Stats, repos))
	}
	return result, nil
}

// Stream compiles raw and forwards each zoekt result batch to sink as it
// arrives, returning the aggregate stats.
//
// Flush cadence (T4.3 decision): per-chunk — every shard-level batch zoekt
// emits goes straight out, no timers. zoekt already batches internally, so
// event volume is bounded by shard count, and latency-to-first-result stays
// minimal. Revisit with time-batching only if fleet-scale fan-in (P6) makes
// event volume a problem.
func (s *Searcher) Stream(ctx context.Context, raw string, opts Options, sink func(*Result)) (*Stats, error) {
	q, versions, err := s.compile(ctx, raw)
	if err != nil {
		return nil, err
	}
	var agg Stats
	repos := newRepoCollector()
	err = s.z.StreamSearch(ctx, q, opts.zoekt(), zoekt.SenderFunc(func(r *zoekt.SearchResult) {
		batch := toResult(r, versions)
		agg.MatchCount += batch.Stats.MatchCount
		agg.FileCount += batch.Stats.FileCount
		agg.DurationMS += batch.Stats.DurationMS
		for _, f := range batch.Files {
			repos.add(f.Repo)
		}
		if len(batch.Files) > 0 {
			sink(batch)
		}
	}))
	if err != nil {
		return nil, fmt.Errorf("stream search: %w", err)
	}
	if s.Usage != nil {
		s.Usage(ctx, usageEvent(agg, repos))
	}
	return &agg, nil
}

// repoCollector keeps the first usageRepoCap distinct repo names in result
// order (zoekt relevance order, so the cap drops the least relevant tail).
type repoCollector struct {
	seen  map[string]bool
	names []string
}

func newRepoCollector() *repoCollector {
	return &repoCollector{seen: make(map[string]bool)}
}

func (c *repoCollector) add(name string) {
	if len(c.names) >= usageRepoCap || c.seen[name] {
		return
	}
	c.seen[name] = true
	c.names = append(c.names, name)
}

func usageEvent(stats Stats, repos *repoCollector) store.UsageEvent {
	return store.UsageEvent{
		Kind: "search", Repos: repos.names,
		MatchCount: stats.MatchCount, FileCount: stats.FileCount,
		DurationMS: stats.DurationMS,
	}
}

// compile applies the user query and then fails closed to repository rows the
// store still considers searchable. Stale/untracked shards can remain on disk
// after an interrupted cleanup, but they must never leak into search results.
func (s *Searcher) compile(ctx context.Context, raw string) (query.Q, map[string]string, error) {
	q, revision, err := compileQuery(ctx, s.st, s.Contexts, raw)
	if err != nil {
		return nil, nil, err
	}
	if revision == "" {
		revision = "HEAD"
	}
	repos, err := s.st.ListRepos(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve searchable repos: %w", err)
	}
	var allow func(store.Repo) bool
	if s.Visible != nil {
		allow = s.Visible(ctx)
	}
	versions := make(map[string]string, len(repos))
	branchRepos := make(map[string][]string)
	for _, repo := range repos {
		if repo.Deleting || repo.IndexedCommitHash == "" {
			continue
		}
		// T10.3: filtering versions too makes toResult's revision check a
		// second fail-closed gate over the permission boundary.
		if allow != nil && !allow(repo) {
			continue
		}
		indexed, ok := indexedRevision(repo, revision)
		if !ok {
			continue
		}
		branchRepos[indexed.Branch] = append(branchRepos[indexed.Branch], repo.Name)
		versions[repo.Name] = indexed.Commit
	}
	if len(branchRepos) == 0 {
		if revision != "HEAD" {
			return nil, nil, fmt.Errorf("revision %q is not indexed in any visible repository", revision)
		}
		return query.Simplify(query.NewAnd(query.NewRepoSet(), q)), versions, nil
	}
	branches := make([]string, 0, len(branchRepos))
	for branch := range branchRepos {
		branches = append(branches, branch)
	}
	sort.Strings(branches)
	var scopes []query.Q
	for _, branch := range branches {
		names := branchRepos[branch]
		sort.Strings(names)
		scopes = append(scopes, query.NewAnd(
			query.NewRepoSet(names...),
			&query.Branch{Pattern: branch, Exact: true},
		))
	}
	scope := scopes[0]
	if len(scopes) > 1 {
		scope = query.NewOr(scopes...)
	}
	return query.Simplify(query.NewAnd(scope, q)), versions, nil
}

func indexedRevision(repo store.Repo, selector string) (store.IndexedRevision, bool) {
	if len(repo.IndexedRevisions) == 0 {
		if selector == "HEAD" && repo.IndexedCommitHash != "" {
			return store.IndexedRevision{Selector: "HEAD", Branch: "HEAD", Commit: repo.IndexedCommitHash}, true
		}
		return store.IndexedRevision{}, false
	}
	if len(repo.IndexedRevisions) > 8 {
		return store.IndexedRevision{}, false
	}
	bySelector := make(map[string]store.IndexedRevision, len(repo.IndexedRevisions))
	branches := make(map[string]bool, len(repo.IndexedRevisions))
	validDefault := false
	for _, revision := range repo.IndexedRevisions {
		if revision.Selector == "" || revision.Branch == "" || revision.Commit == "" ||
			bySelector[revision.Selector].Selector != "" || branches[revision.Branch] {
			return store.IndexedRevision{}, false
		}
		if revision.Selector == "HEAD" {
			validDefault = revision.Branch == "HEAD" && revision.Commit == repo.IndexedCommitHash
		}
		bySelector[revision.Selector] = revision
		branches[revision.Branch] = true
	}
	if !validDefault {
		return store.IndexedRevision{}, false
	}
	revision, ok := bySelector[selector]
	return revision, ok
}

func toResult(res *zoekt.SearchResult, versions map[string]string) *Result {
	out := &Result{
		Files: make([]FileResult, 0, len(res.Files)),
		Stats: Stats{
			DurationMS: res.Duration.Milliseconds(),
		},
	}
	for _, f := range res.Files {
		if versions[f.Repository] != f.Version {
			continue
		}
		fr := FileResult{
			Repo:     f.Repository,
			Path:     f.FileName,
			Ref:      f.Version,
			Language: f.Language,
			Chunks:   make([]Chunk, 0, len(f.ChunkMatches)),
		}
		for _, c := range f.ChunkMatches {
			if c.FileName {
				continue // filename-only matches carry no content chunk worth showing
			}
			ch := Chunk{
				Content:   string(c.Content),
				StartLine: int(c.ContentStart.LineNumber),
				Ranges:    make([]Range, 0, len(c.Ranges)),
			}
			for _, r := range c.Ranges {
				ch.Ranges = append(ch.Ranges, Range{
					StartLine: int(r.Start.LineNumber), StartCol: int(r.Start.Column),
					EndLine: int(r.End.LineNumber), EndCol: int(r.End.Column),
				})
				out.Stats.MatchCount++
			}
			fr.Chunks = append(fr.Chunks, ch)
		}
		out.Files = append(out.Files, fr)
		out.Stats.FileCount++
	}
	return out
}
