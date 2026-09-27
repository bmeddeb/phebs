package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

func typedNavigationJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func typedNavigationTestProfile(t *testing.T, bundle string) typedindex.Profile {
	t.Helper()
	tool := typedindex.Tool{Version: "0.2.7", Digest: typedNavigationHash("tool")}
	p, e := typedindex.DecodeProfile(t.Context(), typedNavigationJSON(t, typedindex.ProfileDefinition{Schema: typedindex.GeneratedProfileSchema, Name: "reader", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.GeneratedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: bundle, ImageDigest: typedNavigationHash("image")}))
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestTypedNavigationConstructor(t *testing.T) {
	for _, p := range []string{"", "relative", "/tmp/../other"} {
		if _, e := newTypedCodeNavigationResolver(&store.Surreal{}, p); e == nil {
			t.Fatal("untrusted base", p)
		}
	}
	if _, e := newTypedCodeNavigationResolver(nil, t.TempDir()); e == nil {
		t.Fatal("nil store")
	}
	if _, _, e := typedNavigationAuthority(store.TypedIndexCurrentCustody{}); !errors.Is(e, codenav.ErrTypedIndexBinding) {
		t.Fatal(e)
	}
}
func TestTypedNavigationSelection(t *testing.T) {
	s, e := store.OpenLocalMemory(t.Context(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	r, e := newTypedCodeNavigationResolver(s, filepath.Join(t.TempDir(), "not-opened"))
	if e != nil {
		t.Fatal(e)
	}
	repo := "example.invalid/selection"
	b, e := r.ResolveRoutedIndex(t.Context(), repo, strings.Repeat("a", 40))
	if e != nil || b.Selected {
		t.Fatal("absent selection", b, e)
	}
	if e = s.UpsertRepo(t.Context(), store.Repo{Name: repo}); e != nil {
		t.Fatal(e)
	}
	if e = s.SetRepoIndexed(t.Context(), repo, strings.Repeat("a", 40), time.Now()); e != nil {
		t.Fatal(e)
	}
	p := typedNavigationTestProfile(t, typedNavigationHash("bundle"))
	if _, e = s.InstallTypedProfile(t.Context(), repo, p, typedNavigationHash("universe"), 0); e != nil {
		t.Fatal(e)
	}
	b, e = r.ResolveRoutedIndex(t.Context(), repo, strings.Repeat("a", 40))
	if e != nil || !b.Selected || b.RootDigest != "" {
		t.Fatal("selected unavailable", b, e)
	}
	if e = s.SetRepoDeleting(t.Context(), repo, true); e != nil {
		t.Fatal(e)
	}
	b, e = r.ResolveRoutedIndex(t.Context(), repo, strings.Repeat("a", 40))
	if e != nil || !b.Selected || b.RootDigest != "" {
		t.Fatal("deleting fell through legacy", b, e)
	}
	if e = s.SetRepoDeleting(t.Context(), repo, false); e != nil {
		t.Fatal(e)
	}
	if e = s.ClearTypedIndexForRestore(t.Context()); e != nil {
		t.Fatal(e)
	}
	b, e = r.ResolveRoutedIndex(t.Context(), repo, strings.Repeat("a", 40))
	if e != nil || !b.Selected || b.RootDigest != "" {
		t.Fatal("restored fell through legacy", b, e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e = r.ResolveRoutedIndex(ctx, repo, strings.Repeat("a", 40)); e == nil {
		t.Fatal("cancellation ignored")
	}
}

// The real adapter preserves committed index.scip for an unselected repository,
// then refuses that same valid legacy index once the typed provider is selected.
func TestTypedNavigationLegacy(t *testing.T) {
	s, e := store.OpenLocalMemory(t.Context(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	if e = os.Mkdir(origin, 0700); e != nil {
		t.Fatal(e)
	}
	run := func(dir string, args ...string) string {
		t.Helper()
		c := exec.CommandContext(t.Context(), "git", args...)
		c.Dir = dir
		raw, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v: %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	run(origin, "init", "-b", "main")
	run(origin, "config", "user.name", "Phebs Test")
	run(origin, "config", "user.email", "phebs@example.invalid")
	symbol := "scip-go gomod example.invalid/legacy v1 p/F()."
	raw, e := proto.Marshal(&scip.Index{Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7"}}, Documents: []*scip.Document{{RelativePath: "main.go", PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 1}, Symbol: symbol, SymbolRoles: int32(scip.SymbolRole_Definition)}}, Symbols: []*scip.SymbolInformation{{Symbol: symbol}}}}})
	if e != nil {
		t.Fatal(e)
	}
	for name, data := range map[string][]byte{"main.go": []byte("F\n"), "index.scip": raw} {
		if e = os.WriteFile(filepath.Join(origin, name), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	run(origin, "add", "main.go", "index.scip")
	run(origin, "commit", "-m", "fixture")
	revision := run(origin, "rev-parse", "HEAD")
	repo := "example.invalid/legacy"
	dataDir := filepath.Join(root, "data")
	if e = phebssync.Mirror(t.Context(), "file://"+origin, phebssync.RepoDir(dataDir, repo)); e != nil {
		t.Fatal(e)
	}
	r, e := newTypedCodeNavigationResolver(s, filepath.Join(root, "unopened"))
	if e != nil {
		t.Fatal(e)
	}
	service := codenav.New(codenav.Options{DataDir: dataDir, RoutedResolver: r})
	q := codenav.Query{Repo: repo, Revision: revision, Path: "main.go", Encoding: codenav.EncodingUTF16}
	ctx, ledger, e := readaccounting.Start(t.Context(), readaccounting.Counts{StoreReadAttempts: 2, ControlFileReads: 100, MemberVisits: 100})
	if e != nil {
		t.Fatal(e)
	}
	result, e := service.Definition(ctx, q)
	counts, countErr := ledger.Finish()
	if e != nil || result.Location == nil || countErr != nil || counts.StoreReadAttempts != 2 || counts.StoreWriteAttempts != 0 {
		t.Fatal("legacy positive", result, e, counts, countErr)
	}
	if e = s.UpsertRepo(t.Context(), store.Repo{Name: repo}); e != nil {
		t.Fatal(e)
	}
	if e = s.SetRepoIndexed(t.Context(), repo, revision, time.Now()); e != nil {
		t.Fatal(e)
	}
	p := typedNavigationTestProfile(t, typedNavigationHash("bundle"))
	if _, e = s.InstallTypedProfile(t.Context(), repo, p, typedNavigationHash("universe"), 0); e != nil {
		t.Fatal(e)
	}
	result, e = service.Definition(t.Context(), q)
	if e != nil || result.Available || result.Location != nil {
		t.Fatal("selected legacy fallback", result, e)
	}
}
