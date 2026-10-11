package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
)

func TestMergedMetadataNamesTheMergePin(t *testing.T) {
	meta := canonicalMetadata("v0.2.7", "example", "github.com/example/repo", "full-commit", []string{".", "api"})
	want := []string{"t472-merge", "--remote", "github.com/example/repo", "--pin", "full-commit", "--module-rels", ".,api"}
	if !slices.Equal(meta.GetToolInfo().GetArguments(), want) {
		t.Fatalf("merged metadata arguments = %q, want %q", meta.GetToolInfo().GetArguments(), want)
	}
}

func TestVersionSkewCountsOnlyUnboundInRepoReferences(t *testing.T) {
	const (
		defined = "scip-go gomod example.com/api aaa `example.com/api/pb`/Client#Call()."
		skewed  = "scip-go gomod example.com/api bbb `example.com/api/pb`/Client#Call()."
		foreign = "scip-go gomod example.com/other bbb `example.com/other/pb`/Client#Call()."
	)
	definition := int32(scip.SymbolRole_Definition)
	docs := []*scip.Document{
		{RelativePath: "api/pb/client.go", Occurrences: []*scip.Occurrence{
			{Symbol: defined, SymbolRoles: definition},
		}},
		{RelativePath: "client/call.go", Occurrences: []*scip.Occurrence{
			{Symbol: defined}, // binds exactly
			{Symbol: skewed},  // same symbol, other module version
			{Symbol: foreign}, // not an in-repo symbol
			{Symbol: "local 1"},
		}},
	}
	count, sample := versionSkew(docs)
	if count != 1 || len(sample) != 1 || sample[0] != "client/call.go "+skewed {
		t.Fatalf("versionSkew = %d %q, want the one skewed reference", count, sample)
	}
}

// TestClassifyDocumentsRegularAliasAndCanonicalPaths covers the pinned-tree
// rule on one stream: regular and executable paths are kept, a committed
// symlink path is dropped as an alias, and its canonical counterpart stays.
func TestClassifyDocumentsRegularAliasAndCanonicalPaths(t *testing.T) {
	tree := map[string]treeEntry{
		"api/client.go":    {mode: "100644"},
		"tools/gen.sh":     {mode: "100755"},
		"alias/example.go": {mode: "120000", sha: "symlinkblob"},
		"real/example.go":  {mode: "100644"},
	}
	docs := []*scip.Document{
		{RelativePath: "alias/example.go"},
		{RelativePath: "real/example.go"},
		{RelativePath: "api/client.go"},
		{RelativePath: "tools/gen.sh"},
	}
	kept, aliases, err := classifyDocuments(docs, tree)
	if err != nil {
		t.Fatal(err)
	}
	var keptPaths []string
	for _, d := range kept {
		keptPaths = append(keptPaths, d.RelativePath)
	}
	wantKept := []string{"real/example.go", "api/client.go", "tools/gen.sh"}
	if !slices.Equal(keptPaths, wantKept) {
		t.Fatalf("kept = %q, want %q", keptPaths, wantKept)
	}
	if len(aliases) != 1 || aliases[0].Path != "alias/example.go" ||
		aliases[0].Mode != "120000" || aliases[0].sha != "symlinkblob" {
		t.Fatalf("aliases = %+v, want the one inventoried symlink alias", aliases)
	}
}

func TestClassifyDocumentsRefusesUnexpectedPaths(t *testing.T) {
	tree := map[string]treeEntry{
		"ok/file.go":  {mode: "100644"},
		"submodule":   {mode: "160000", sha: "gitlink"},
		"odd/file.go": {mode: "100664"},
	}
	for _, path := range []string{"missing/file.go", "submodule", "odd/file.go"} {
		t.Run(path, func(t *testing.T) {
			if _, _, err := classifyDocuments([]*scip.Document{{RelativePath: path}}, tree); err == nil {
				t.Fatalf("classifyDocuments accepted %q", path)
			}
		})
	}
}

// TestLoadTreeAndResolveAliasTargets exercises the real git commands on a
// throwaway repository with one regular file and one committed symlink.
func TestLoadTreeAndResolveAliasTargets(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	clone := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = clone
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(clone, "file.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.go", filepath.Join(clone, "alias.go")); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file.go", "alias.go")
	runGit("-c", "commit.gpgsign=false", "commit", "-q", "-m", "t")

	tree, err := loadTree(clone, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if entry := tree["file.go"]; entry.mode != "100644" {
		t.Fatalf("file.go mode = %q, want 100644", entry.mode)
	}
	entry, ok := tree["alias.go"]
	if !ok || entry.mode != "120000" {
		t.Fatalf("alias.go entry = %+v, want mode 120000", entry)
	}
	aliases := []aliasDocument{{Path: "alias.go", Mode: entry.mode, sha: entry.sha}}
	if err := resolveAliasTargets(clone, aliases); err != nil {
		t.Fatal(err)
	}
	if aliases[0].Target != "file.go" {
		t.Fatalf("alias target = %q, want file.go", aliases[0].Target)
	}
}
