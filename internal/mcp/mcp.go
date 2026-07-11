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

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

type Options struct {
	Version string
	Store   store.Store
	Search  *search.Searcher          // nil = search_code reports unavailable
	DataDir string                    // bare mirrors for read_file
	CodeNav *codenav.Service          // nil = SCIP tools report unavailable
	History *phebssync.HistoryService // nil = construct from DataDir
	// Visible resolves the caller's repo visibility (T10.3); nil disables
	// permission filtering. search_code is covered inside the searcher; this
	// hook gates the tools that bypass it (read_file, list_repos, SCIP,
	// history). Requires per-request principals — serve runs the streamable
	// handler stateless so tool contexts carry the current caller.
	Visible func(ctx context.Context) func(store.Repo) bool
}

// maxFileBytes caps read_file output: a whole-file dump larger than this
// wastes an agent's context window; ranged reads cover the rest.
const maxFileBytes = 200_000

// NewServer builds the phebs MCP server over search, immutable source reads,
// SCIP navigation, and bounded Git history plumbing.
func NewServer(opts Options) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "phebs", Version: opts.Version}, nil)
	history := opts.History
	if history == nil {
		history = phebssync.NewHistoryService(opts.DataDir)
	}

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
		Ref       string `json:"ref,omitempty" jsonschema:"commit-ish; defaults to the repository's indexed commit"`
		StartLine int    `json:"start_line,omitempty" jsonschema:"1-based first line to return; default 1"`
		EndLine   int    `json:"end_line,omitempty" jsonschema:"1-based last line to return, inclusive; default end of file"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "read_file",
		Description: "Read a file (or a line range of it) from an indexed repository at its indexed revision.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in readIn) (*sdk.CallToolResult, readOut, error) {
		ref, err := indexedRevision(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, readOut{}, err
		}
		content, err := phebssync.CatFile(ctx, opts.DataDir, in.Repo, ref, in.Path)
		if err != nil {
			return nil, readOut{}, err
		}
		if !utf8.Valid(content) {
			return nil, readOut{}, fmt.Errorf("%s is a binary file (%d bytes)", in.Path, len(content))
		}
		return nil, sliceLines(string(content), in.StartLine, in.EndLine), nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "list_repos",
		Description: "List every indexed repository with its metadata (name, branch, visibility, last index time).",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, reposOut, error) {
		repos, err := opts.Store.ListRepos(ctx)
		if err != nil {
			return nil, reposOut{}, err
		}
		allow := repoFilter(ctx, opts)
		out := reposOut{Repos: make([]repoInfo, 0, len(repos))}
		for _, r := range repos {
			if r.Deleting || r.IndexedCommitHash == "" {
				continue
			}
			if allow != nil && !allow(r) {
				continue
			}
			out.Repos = append(out.Repos, repoInfo{
				Name: r.Name, DefaultBranch: r.DefaultBranch, WebURL: r.WebURL,
				IsPublic: r.IsPublic, IsFork: r.IsFork, IsArchived: r.IsArchived,
				IndexedAt: r.IndexedAt,
			})
		}
		return nil, out, nil
	})

	registerCodeNavigationTools(s, opts)
	registerHistoryTools(s, opts, history)

	return s
}

type positionIn struct {
	Repo      string `json:"repo" jsonschema:"full repository name as returned by list_repos"`
	Path      string `json:"path" jsonschema:"source file path within the repository"`
	Ref       string `json:"ref,omitempty" jsonschema:"full indexed commit object ID; defaults to the repository's indexed commit"`
	Line      int32  `json:"line" jsonschema:"zero-based source line"`
	Character int32  `json:"character" jsonschema:"zero-based UTF-16 code-unit offset from line start"`
}

func registerCodeNavigationTools(s *sdk.Server, opts Options) {
	query := func(ctx context.Context, in positionIn) (codenav.Query, error) {
		if opts.CodeNav == nil {
			return codenav.Query{}, errors.New("code navigation unavailable: no SCIP service configured")
		}
		ref, err := indexedRevision(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return codenav.Query{}, err
		}
		return codenav.Query{
			Repo: in.Repo, Revision: ref, Path: in.Path,
			Line: in.Line, Character: in.Character, Encoding: codenav.EncodingUTF16,
		}, nil
	}

	sdk.AddTool(s, &sdk.Tool{
		Name:        "find_definitions",
		Description: "Find the precise SCIP definition for the symbol at a zero-based UTF-16 source position. Returns available=false when the indexed revision has no committed SCIP index.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in positionIn) (*sdk.CallToolResult, codenav.DefinitionResult, error) {
		q, err := query(ctx, in)
		if err != nil {
			return nil, codenav.DefinitionResult{}, err
		}
		result, err := opts.CodeNav.Definition(ctx, q)
		return nil, result, err
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "find_references",
		Description: "Find precise SCIP references for the symbol at a zero-based UTF-16 source position. Returned ranges use zero-based UTF-16 positions.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in positionIn) (*sdk.CallToolResult, codenav.ReferencesResult, error) {
		q, err := query(ctx, in)
		if err != nil {
			return nil, codenav.ReferencesResult{}, err
		}
		result, err := opts.CodeNav.References(ctx, q)
		return nil, result, err
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "hover",
		Description: "Return SCIP signature, documentation, and symbol metadata at a zero-based UTF-16 source position.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in positionIn) (*sdk.CallToolResult, codenav.HoverResult, error) {
		q, err := query(ctx, in)
		if err != nil {
			return nil, codenav.HoverResult{}, err
		}
		result, err := opts.CodeNav.Hover(ctx, q)
		return nil, result, err
	})
}

func registerHistoryTools(s *sdk.Server, opts Options, history *phebssync.HistoryService) {
	type blameIn struct {
		Repo string `json:"repo" jsonschema:"full repository name as returned by list_repos"`
		Path string `json:"path" jsonschema:"file path within the repository"`
		Ref  string `json:"ref,omitempty" jsonschema:"commit-ish; defaults to the repository's indexed commit"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "blame",
		Description: "Attribute each source line to a commit at an immutable revision, following moves and renames. Large results report truncated=true.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in blameIn) (*sdk.CallToolResult, phebssync.BlameResult, error) {
		ref, err := indexedRevision(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, phebssync.BlameResult{}, err
		}
		result, err := history.Blame(ctx, phebssync.BlameRequest{Repo: in.Repo, Ref: ref, Path: in.Path})
		return nil, result, err
	})

	type commitsIn struct {
		Repo   string `json:"repo" jsonschema:"full repository name as returned by list_repos"`
		Ref    string `json:"ref,omitempty" jsonschema:"commit-ish; defaults to the repository's indexed commit"`
		Path   string `json:"path,omitempty" jsonschema:"optional file path; history follows renames"`
		Limit  int    `json:"limit,omitempty" jsonschema:"commits per page; default 50, cap 200"`
		Offset int    `json:"offset,omitempty" jsonschema:"zero-based commit offset, cap 10000"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "list_commits",
		Description: "List commits reachable from an immutable revision, optionally scoped to a file and followed across renames.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in commitsIn) (*sdk.CallToolResult, phebssync.CommitListResult, error) {
		ref, err := indexedRevision(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, phebssync.CommitListResult{}, err
		}
		result, err := history.Commits(ctx, phebssync.CommitListRequest{
			Repo: in.Repo, Ref: ref, Path: in.Path, Limit: in.Limit, Offset: in.Offset,
		})
		return nil, result, err
	})

	type commitIn struct {
		Repo string `json:"repo" jsonschema:"full repository name as returned by list_repos"`
		Ref  string `json:"ref,omitempty" jsonschema:"commit-ish; defaults to the repository's indexed commit"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "get_commit",
		Description: "Get commit metadata, parents, and first-parent file changes for one immutable revision.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in commitIn) (*sdk.CallToolResult, phebssync.CommitResult, error) {
		ref, err := indexedRevision(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, phebssync.CommitResult{}, err
		}
		result, err := history.Commit(ctx, phebssync.CommitRequest{Repo: in.Repo, Ref: ref})
		return nil, result, err
	})

	type diffIn struct {
		Repo         string `json:"repo" jsonschema:"full repository name as returned by list_repos"`
		Head         string `json:"head,omitempty" jsonschema:"head commit-ish; defaults to the repository's indexed commit"`
		Base         string `json:"base,omitempty" jsonschema:"base commit-ish; defaults to head's first parent"`
		Path         string `json:"path,omitempty" jsonschema:"optional exact path filter"`
		ContextLines *int   `json:"context_lines,omitempty" jsonschema:"unified context lines; default 3, maximum 20"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "diff",
		Description: "Return a bounded unified diff and structured file statistics. Binary files are summarized and large patches report truncated=true.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in diffIn) (*sdk.CallToolResult, phebssync.DiffResult, error) {
		head, err := indexedRevision(ctx, opts, in.Repo, in.Head)
		if err != nil {
			return nil, phebssync.DiffResult{}, err
		}
		request := phebssync.DiffRequest{Repo: in.Repo, Base: in.Base, Head: head, Path: in.Path}
		if in.ContextLines != nil {
			request.ContextLines = *in.ContextLines
			request.ContextLinesSet = true
		}
		result, err := history.Diff(ctx, request)
		return nil, result, err
	})
}

func indexedRevision(ctx context.Context, opts Options, repoName, requested string) (string, error) {
	if opts.Store == nil {
		return "", errors.New("repository store unavailable")
	}
	repo, err := opts.Store.GetRepo(ctx, repoName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", fmt.Errorf("unknown repo %q (use list_repos for names)", repoName)
		}
		return "", err
	}
	// T10.3: a permission denial reads exactly like a missing repo —
	// disclosing existence would itself be a leak.
	if allow := repoFilter(ctx, opts); allow != nil && !allow(*repo) {
		return "", fmt.Errorf("unknown repo %q (use list_repos for names)", repoName)
	}
	if repo.Deleting {
		return "", fmt.Errorf("repo %q is being deleted", repoName)
	}
	if repo.IndexedCommitHash == "" {
		return "", fmt.Errorf("repo %q has no indexed revision yet", repoName)
	}
	if requested != "" {
		return requested, nil
	}
	return repo.IndexedCommitHash, nil
}

// repoFilter resolves the request's visibility predicate; nil = everything.
func repoFilter(ctx context.Context, opts Options) func(store.Repo) bool {
	if opts.Visible == nil {
		return nil
	}
	return opts.Visible(ctx)
}

// reposOut is list_repos' result shape.
type reposOut struct {
	Repos []repoInfo `json:"repos"`
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
// cap (T8.3: a multi-MB dump only wastes agent context — the Truncated flag
// points at ranged re-reads). Whole lines are kept while they fit; a single
// line that alone exceeds the cap is byte-truncated (UTF-8-safe) so progress
// is always made — the pre-fix code let the first line through uncapped.
func sliceLines(content string, start, end int) readOut {
	if content == "" {
		return readOut{TotalLine: 0} // empty file: zero lines, not one
	}
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

	var b strings.Builder
	last := start - 1 // index one past the final included line
	truncated := false
	for i := start - 1; i < end; i++ {
		line := lines[i]
		sep := 0
		if b.Len() > 0 {
			sep = 1 // the '\n' joining this line to the previous
		}
		if b.Len()+sep+len(line) > maxFileBytes {
			if b.Len() == 0 { // first line alone overflows: deliver a safe prefix
				b.WriteString(truncateUTF8(line, maxFileBytes))
				last = i + 1
			}
			truncated = true
			break
		}
		if sep == 1 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		last = i + 1
	}
	return readOut{
		Content:   b.String(),
		StartLine: start,
		EndLine:   last,
		TotalLine: total,
		Truncated: truncated,
	}
}

// truncateUTF8 cuts s to at most max bytes without splitting a rune.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
