package search

import (
	"context"
	"fmt"
	"time"

	"github.com/sourcegraph/zoekt"
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
	q, err := Compile(ctx, s.st, s.Contexts, raw)
	if err != nil {
		return nil, err
	}
	res, err := s.z.Search(ctx, q, opts.zoekt())
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return toResult(res), nil
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
	q, err := Compile(ctx, s.st, s.Contexts, raw)
	if err != nil {
		return nil, err
	}
	var agg Stats
	err = s.z.StreamSearch(ctx, q, opts.zoekt(), zoekt.SenderFunc(func(r *zoekt.SearchResult) {
		agg.MatchCount += r.MatchCount
		agg.FileCount += r.FileCount
		agg.DurationMS += r.Duration.Milliseconds()
		if len(r.Files) > 0 {
			sink(toResult(r))
		}
	}))
	if err != nil {
		return nil, fmt.Errorf("stream search: %w", err)
	}
	return &agg, nil
}

func toResult(res *zoekt.SearchResult) *Result {
	out := &Result{
		Files: make([]FileResult, 0, len(res.Files)),
		Stats: Stats{
			MatchCount: res.MatchCount,
			FileCount:  res.FileCount,
			DurationMS: res.Duration.Milliseconds(),
		},
	}
	for _, f := range res.Files {
		fr := FileResult{
			Repo:     f.Repository,
			Path:     f.FileName,
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
			}
			fr.Chunks = append(fr.Chunks, ch)
		}
		out.Files = append(out.Files, fr)
	}
	return out
}
