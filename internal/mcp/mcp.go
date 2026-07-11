// Package mcp exposes phebs to agents over the Model Context Protocol
// (T8.2, PLAN P4 — the MCP-first product layer). Official go-sdk; tools map
// 1:1 onto the existing search/read/store internals.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

type Options struct {
	Version string
	Store   store.Store
	Search  *search.Searcher // nil = search_code reports unavailable
	DataDir string           // bare mirrors for read_file
}

// maxFileBytes caps read_file output: a whole-file dump larger than this
// wastes an agent's context window; ranged reads cover the rest.
const maxFileBytes = 200_000

// NewServer builds the phebs MCP server: search_code, read_file, list_repos.
func NewServer(opts Options) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "phebs", Version: opts.Version}, nil)

	type searchIn struct {
		Query        string `json:"query" jsonschema:"zoekt query syntax: plain terms AND together and patterns are regex; filters include repo: file: lang: sym: case:yes content: plus phebs' archived:/fork:/public:yes|no and context:<name> (named repo set); prefix any atom with - to negate"`
		MaxMatches   int    `json:"max_matches,omitempty" jsonschema:"maximum files returned; default 50, cap 500"`
		ContextLines int    `json:"context_lines,omitempty" jsonschema:"lines of context around each match; default 0, cap 10"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "search_code",
		Description: "Search code across every indexed repository. Returns matched files with line-numbered content chunks and match ranges.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in searchIn) (*sdk.CallToolResult, search.Result, error) {
		if opts.Search == nil {
			return nil, search.Result{}, errors.New("search unavailable: no index open")
		}
		res, err := opts.Search.Search(ctx, in.Query,
			search.Options{MaxMatches: in.MaxMatches, ContextLines: in.ContextLines})
		if err != nil {
			return nil, search.Result{}, err
		}
		return nil, *res, nil
	})

	type readIn struct {
		Repo      string `json:"repo" jsonschema:"full repo name as returned by list_repos or search_code, e.g. github.com/acme/api"`
		Path      string `json:"path" jsonschema:"file path within the repo"`
		Ref       string `json:"ref,omitempty" jsonschema:"commit-ish; default HEAD (the indexed revision)"`
		StartLine int    `json:"start_line,omitempty" jsonschema:"1-based first line to return; default 1"`
		EndLine   int    `json:"end_line,omitempty" jsonschema:"1-based last line to return, inclusive; default end of file"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "read_file",
		Description: "Read a file (or a line range of it) from an indexed repository at its indexed revision.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in readIn) (*sdk.CallToolResult, readOut, error) {
		if _, err := opts.Store.GetRepo(ctx, in.Repo); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, readOut{}, fmt.Errorf("unknown repo %q (use list_repos for names)", in.Repo)
			}
			return nil, readOut{}, err
		}
		content, err := phebssync.CatFile(ctx, opts.DataDir, in.Repo, in.Ref, in.Path)
		if err != nil {
			return nil, readOut{}, err
		}
		if !utf8.Valid(content) {
			return nil, readOut{}, fmt.Errorf("%s is a binary file (%d bytes)", in.Path, len(content))
		}
		return nil, sliceLines(string(content), in.StartLine, in.EndLine), nil
	})

	type reposOut struct {
		Repos []repoInfo `json:"repos"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "list_repos",
		Description: "List every indexed repository with its metadata (name, branch, visibility, last index time).",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, reposOut, error) {
		repos, err := opts.Store.ListRepos(ctx)
		if err != nil {
			return nil, reposOut{}, err
		}
		out := reposOut{Repos: make([]repoInfo, 0, len(repos))}
		for _, r := range repos {
			out.Repos = append(out.Repos, repoInfo{
				Name: r.Name, DefaultBranch: r.DefaultBranch, WebURL: r.WebURL,
				IsPublic: r.IsPublic, IsFork: r.IsFork, IsArchived: r.IsArchived,
				IndexedAt: r.IndexedAt,
			})
		}
		return nil, out, nil
	})

	return s
}

type repoInfo struct {
	Name          string     `json:"name"`
	DefaultBranch string     `json:"default_branch,omitempty"`
	WebURL        string     `json:"web_url,omitempty"`
	IsPublic      bool       `json:"is_public"`
	IsFork        bool       `json:"is_fork"`
	IsArchived    bool       `json:"is_archived"`
	IndexedAt     *time.Time `json:"indexed_at,omitempty"`
}

// readOut is read_file's result shape.
type readOut struct {
	Content   string `json:"content"`
	StartLine int    `json:"start_line"`          // first line of Content, 1-based
	EndLine   int    `json:"end_line"`            // last line of Content, inclusive
	TotalLine int    `json:"total_lines"`         // lines in the whole file
	Truncated bool   `json:"truncated,omitempty"` // cut at the size cap; re-request with start_line/end_line
}

// sliceLines applies an optional 1-based inclusive line range, then a size
// cap on whole-line boundaries (T8.3: a multi-MB dump only wastes agent
// context — the Truncated flag points at ranged re-reads).
func sliceLines(content string, start, end int) readOut {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	total := len(lines)
	if start < 1 {
		start = 1
	}
	if start > total {
		start = total
	}
	if end < start || end > total {
		end = total
	}

	size := 0
	last := start - 1 // index one past the final included line
	for i := start - 1; i < end; i++ {
		if size+len(lines[i])+1 > maxFileBytes && last > start-1 {
			break
		}
		size += len(lines[i]) + 1
		last = i + 1
	}
	return readOut{
		Content:   strings.Join(lines[start-1:last], "\n"),
		StartLine: start,
		EndLine:   last,
		TotalLine: total,
		Truncated: last < end,
	}
}
