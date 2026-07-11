package search

import (
	"context"
	"fmt"
	"os"
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
}

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
	return toResult(res, versions), nil
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
	err = s.z.StreamSearch(ctx, q, opts.zoekt(), zoekt.SenderFunc(func(r *zoekt.SearchResult) {
		batch := toResult(r, versions)
		agg.MatchCount += batch.Stats.MatchCount
		agg.FileCount += batch.Stats.FileCount
		agg.DurationMS += batch.Stats.DurationMS
		if len(batch.Files) > 0 {
			sink(batch)
		}
	}))
	if err != nil {
		return nil, fmt.Errorf("stream search: %w", err)
	}
	return &agg, nil
}

// compile applies the user query and then fails closed to repository rows the
// store still considers searchable. Stale/untracked shards can remain on disk
// after an interrupted cleanup, but they must never leak into search results.
func (s *Searcher) compile(ctx context.Context, raw string) (query.Q, map[string]string, error) {
	q, err := Compile(ctx, s.st, s.Contexts, raw)
	if err != nil {
		return nil, nil, err
	}
	repos, err := s.st.ListRepos(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve searchable repos: %w", err)
	}
	names := make([]string, 0, len(repos))
	versions := make(map[string]string, len(repos))
	for _, repo := range repos {
		if !repo.Deleting && repo.IndexedCommitHash != "" {
			names = append(names, repo.Name)
			versions[repo.Name] = repo.IndexedCommitHash
		}
	}
	return query.Simplify(query.NewAnd(query.NewRepoSet(names...), q)), versions, nil
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
