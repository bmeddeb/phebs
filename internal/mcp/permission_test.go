package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/store"
)

// T10.3: MCP tools that bypass the searcher enforce the same visibility —
// list_repos filters, and a forbidden repo reads exactly like a missing one.
func TestToolPermissionFiltering(t *testing.T) {
	st := fakeStore{repos: []store.Repo{
		{Name: "example.com/visible", IndexedCommitHash: "aaa"},
		{Name: "example.com/hidden", IndexedCommitHash: "bbb"},
	}}
	s := NewServer(Options{
		Version: "t", Store: st, DataDir: t.TempDir(),
		Visible: func(context.Context) func(store.Repo) bool {
			return func(r store.Repo) bool { return r.Name == "example.com/visible" }
		},
	})

	repos, res := callTool[reposOut](t, s, "list_repos", nil)
	if res.IsError {
		t.Fatalf("list_repos errored: %+v", res)
	}
	if len(repos.Repos) != 1 || repos.Repos[0].Name != "example.com/visible" {
		t.Fatalf("list_repos = %+v, want only the granted repo", repos.Repos)
	}

	// read_file on the forbidden repo: same message as a nonexistent repo
	_, res = callTool[readOut](t, s, "read_file",
		map[string]any{"repo": "example.com/hidden", "path": "f.go"})
	if !res.IsError {
		t.Fatal("read_file on a forbidden repo succeeded")
	}
	forbidden := textContent(t, res)
	_, res = callTool[readOut](t, s, "read_file",
		map[string]any{"repo": "example.com/absent", "path": "f.go"})
	if !res.IsError {
		t.Fatal("read_file on a missing repo succeeded")
	}
	missing := textContent(t, res)
	// identical shape modulo the echoed name — anything else discloses existence
	shape := func(s, repo string) string { return strings.ReplaceAll(s, repo, "R") }
	if shape(forbidden, "example.com/hidden") != shape(missing, "example.com/absent") {
		t.Errorf("forbidden (%q) and missing (%q) response shapes differ — existence leak", forbidden, missing)
	}
	if !strings.Contains(forbidden, "unknown repo") {
		t.Errorf("forbidden read error = %q, want unknown-repo shape", forbidden)
	}

	// history and SCIP tools resolve repos through the same gate
	_, res = callTool[any](t, s, "list_commits", map[string]any{"repo": "example.com/hidden"})
	if !res.IsError || !strings.Contains(textContent(t, res), "unknown repo") {
		t.Errorf("list_commits on forbidden repo = %+v, want unknown-repo error", res)
	}
}

func textContent(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	var parts []string
	for _, content := range res.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}
