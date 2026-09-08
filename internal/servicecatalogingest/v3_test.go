package servicecatalogingest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestV3ReconcilerLiveSurrealCensusPublication(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	repository := "example.com/acme/v3-live-census"
	dataDir, _, commit := testMirror(t, repository, map[string]string{
		"README.md": "mono\n", "shared/schema.proto": "schema\n",
		"svc/main.go": "package main\n",
	})
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	writeCatalog(t, catalogPath, testCatalog(commit, "Orders"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	s, err := store.OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/v3-live-census.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	reconciler := V3Reconciler{
		DataDir: dataDir, Store: s,
		Selections: map[string]config.ServiceCatalog{
			repository: {
				Kind: servicecatalog.AuthorityCommitted,
				ID:   "build-catalog", Path: catalogPath,
			},
		},
	}
	outcome, err := reconciler.ReconcileRepository(ctx, repository)
	if err != nil || outcome != OutcomePublished {
		t.Fatalf("live v3 reconcile = %q, %v", outcome, err)
	}
	opened, err := s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil || opened.Generation.Root.Binding.Source.FileCount != 3 ||
		opened.Generation.Root.Binding.Source.AcceptedFileCount != 2 ||
		opened.Generation.Root.Binding.Source.UnownedFileCount != 1 {
		t.Fatalf("live v3 candidate = %+v, %v", opened, err)
	}
	if _, err := s.GetServiceCatalog(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dark v3 publication changed v2 current: %v", err)
	}
}

func TestV3ReconcilerCommittedCensusNoopAndVersionRefusal(t *testing.T) {
	repository := "example.com/acme/v3-ingest"
	dataDir, mirror, commit := testMirror(t, repository, map[string]string{
		"README.md": "mono\n", "shared/schema.proto": "schema\n",
		"svc/main.go": "package main\n",
	})
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	catalog := testCatalog(commit, "Orders")
	writeCatalog(t, catalogPath, catalog)
	state := &v3MemoryStore{memoryStore: memoryStore{repositories: map[string]store.Repo{
		repository: {Name: repository, IndexedCommitHash: commit},
	}}}
	reconciler := V3Reconciler{
		DataDir: dataDir, Store: state,
		Selections: map[string]config.ServiceCatalog{
			repository: {
				Kind: servicecatalog.AuthorityCommitted,
				ID:   "build-catalog", Path: catalogPath,
			},
		},
	}
	outcome, err := reconciler.ReconcileRepository(t.Context(), repository)
	if err != nil || outcome != OutcomePublished {
		t.Fatalf("first v3 reconcile = %q, %v", outcome, err)
	}
	root := state.current[repository].Root
	if root.Binding.Source.Commit != commit || root.Binding.Source.FileCount != 3 ||
		root.Binding.Source.AcceptedFileCount != 2 ||
		root.Binding.Source.UnownedFileCount != 1 ||
		root.Binding.Authority.Version != commit || state.revisions[repository] != 1 {
		t.Fatalf("v3 binding = %+v", root.Binding)
	}
	legacy := legacyServiceCatalogV3Generation(t, state.history[root.Digest])
	current := state.current[repository]
	current.Root = legacy.Root
	state.current[repository] = current
	state.history[legacy.Root.Digest] = legacy
	root = legacy.Root

	hidden := mirror + ".hidden"
	if err := os.Rename(mirror, hidden); err != nil {
		t.Fatal(err)
	}
	outcome, err = reconciler.ReconcileRepository(t.Context(), repository)
	if err != nil || outcome != OutcomeCurrent || state.revisions[repository] != 1 {
		t.Fatalf("metadata-only v3 no-op = %q, %v, revision %d", outcome, err, state.revisions[repository])
	}
	catalog.Services[0].DisplayName = "Orders API"
	writeCatalog(t, catalogPath, catalog)
	if outcome, err = reconciler.ReconcileRepository(t.Context(), repository); outcome != "" || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same-version replacement = %q, %v", outcome, err)
	}
	if state.revisions[repository] != 1 || state.current[repository].Root.Digest != root.Digest {
		t.Fatal("same-version refusal changed the v3 candidate")
	}
}

func legacyServiceCatalogV3Generation(
	t *testing.T,
	generation servicecatalogv3.Generation,
) servicecatalogv3.Generation {
	t.Helper()
	root := generation.Root
	root.Schema = servicecatalogv3.RootSchema
	root.Digest = "sha256:" + strings.Repeat("0", 64)
	for range 4 {
		raw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		root.RootBytes = len(raw) + 1
		root.EncodedBytes = root.RootBytes + root.EncodedMemberBytes
	}
	digest, err := servicecatalogv3.RootDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	root.Digest = digest
	generation.Root = root
	if err := servicecatalogv3.ValidateGeneration(generation); err != nil {
		t.Fatal(err)
	}
	return generation
}

func TestV3ReconcilerOperatorAndCensusRefusal(t *testing.T) {
	repository := "example.com/acme/v3-operator"
	dataDir, _, commit := testMirror(t, repository, map[string]string{
		"README.md": "mono\n", "shared/schema.proto": "schema\n",
		"svc/main.go": "package main\n",
	})
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	catalog := testCatalog(commit, "Orders")
	catalog.Authority = servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "platform", Version: "v1",
	}
	catalog.Unowned = nil
	writeCatalog(t, catalogPath, catalog)
	state := &v3MemoryStore{memoryStore: memoryStore{repositories: map[string]store.Repo{
		repository: {Name: repository, IndexedCommitHash: commit},
	}}}
	reconciler := V3Reconciler{
		DataDir: dataDir, Store: state,
		Selections: map[string]config.ServiceCatalog{
			repository: {
				Kind: servicecatalog.AuthorityOperator, ID: "platform",
				Version: "v1", Path: catalogPath,
			},
		},
	}
	if _, err := reconciler.ReconcileRepository(t.Context(), repository); err == nil {
		t.Fatal("census gap was admitted")
	}
	if len(state.current) != 0 {
		t.Fatal("census refusal published a v3 candidate")
	}
	catalog.Unowned = []servicecatalog.UnownedPlacement{{
		Path: "README.md", Origin: servicecatalog.OriginBase,
	}}
	writeCatalog(t, catalogPath, catalog)
	outcome, err := reconciler.ReconcileRepository(t.Context(), repository)
	if err != nil || outcome != OutcomePublished {
		t.Fatalf("operator v3 reconcile = %q, %v", outcome, err)
	}
	root := state.current[repository].Root
	if root.Binding.Authority.Version != "v1" || root.Binding.Source.Commit != commit ||
		root.Binding.Source.AcceptedFileCount != 2 || root.Binding.Source.UnownedFileCount != 1 {
		t.Fatalf("operator v3 root = %+v", root)
	}
}

func TestV3ReconcilerWaitsForRequiredIndexedCommit(t *testing.T) {
	const repository = "example.com/acme/return-startup"
	dataDir, mirror, prior := testMirror(t, repository, map[string]string{
		"README.md": "mono\n", "shared/schema.proto": "schema\n", "svc/main.go": "package main\n",
	})
	tree := strings.TrimSpace(testGit(t, mirror, "rev-parse", prior+"^{tree}"))
	required := strings.TrimSpace(testGit(t, mirror, "-c", "user.name=Phebs Test", "-c", "user.email=test@example.com",
		"commit-tree", tree, "-p", prior, "-m", "return source identity"))
	path := filepath.Join(t.TempDir(), "catalog.json")
	catalog := testCatalog(prior, "Orders")
	catalog.Authority = servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: "platform", Version: "b"}
	writeCatalog(t, path, catalog)
	state := &v3MemoryStore{memoryStore: memoryStore{repositories: map[string]store.Repo{
		repository: {Name: repository, IndexedCommitHash: prior},
	}}}
	reconciler := V3Reconciler{DataDir: dataDir, Store: state, Selections: map[string]config.ServiceCatalog{
		repository: {Kind: servicecatalog.AuthorityOperator, ID: "platform", Version: "b", Path: path},
	}}
	if outcome, err := reconciler.ReconcileRepository(t.Context(), repository); err != nil || outcome != OutcomePublished {
		t.Fatal("prior publication", outcome, err)
	}
	old := state.current[repository]
	catalog.Authority.Version = "a-return"
	writeCatalog(t, path, catalog)
	selection := reconciler.Selections[repository]
	selection.Version = "a-return"
	reconciler.Selections[repository] = selection
	reconciler.RequiredIndexedCommit = required
	if err := os.Rename(path, path+".hidden"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(mirror, mirror+".hidden"); err != nil {
		t.Fatal(err)
	}
	// Both public entry points and repeated callbacks must wait without even
	// opening the new catalog or source, while retaining the old authority.
	for range 3 {
		if outcome, err := reconciler.ReconcileRepository(t.Context(), repository); err != nil || outcome != OutcomeNotReady {
			t.Fatal("old indexed source did not wait", outcome, err)
		}
		if report, err := reconciler.Reconcile(t.Context()); err != nil || report.NotReady != 1 || len(report.Failures) != 0 {
			t.Fatal("bulk reconciliation escaped the guard", report, err)
		}
		if state.current[repository].Root.Digest != old.Root.Digest || state.revisions[repository] != 1 {
			t.Fatal("waiting replaced prior authority")
		}
	}
	if err := os.Rename(path+".hidden", path); err != nil {
		t.Fatal(err)
	}
	state.repositories[repository] = store.Repo{Name: repository, IndexedCommitHash: required}
	if _, err := reconciler.ReconcileRepository(t.Context(), repository); err == nil || state.revisions[repository] != 1 {
		t.Fatal("matching commit skipped the actual census")
	}
	if err := os.Rename(mirror+".hidden", mirror); err != nil {
		t.Fatal(err)
	}
	if outcome, err := reconciler.ReconcileRepository(t.Context(), repository); err != nil || outcome != OutcomePublished || state.revisions[repository] != 2 {
		t.Fatal("single correct-source publication", outcome, err)
	}
	if root := state.current[repository].Root; root.Binding.Source.Commit != required || root.Binding.Authority.Version != "a-return" {
		t.Fatal("publication did not bind the required source")
	}
	if err := os.Rename(mirror, mirror+".hidden"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if outcome, err := reconciler.ReconcileRepository(t.Context(), repository); err != nil || outcome != OutcomeCurrent || state.revisions[repository] != 2 {
			t.Fatal("exact-current callback repeated census/publication", outcome, err)
		}
	}
}

type v3MemoryStore struct {
	memoryStore
	current   map[string]store.ServiceCatalogV3CandidateRoot
	history   map[string]servicecatalogv3.Generation
	versions  map[string]string
	revisions map[string]uint64
}

func (s *v3MemoryStore) GetServiceCatalogV3CandidateRoot(
	_ context.Context,
	repository string,
) (*store.ServiceCatalogV3CandidateRoot, error) {
	value, ok := s.current[repository]
	if !ok {
		return nil, store.ErrNotFound
	}
	copy := value
	return &copy, nil
}

func (s *v3MemoryStore) GetServiceCatalogV3Candidate(
	_ context.Context,
	repository string,
) (*store.ServiceCatalogV3Candidate, error) {
	root, ok := s.current[repository]
	if !ok {
		return nil, store.ErrNotFound
	}
	generation := s.history[root.Root.Digest]
	return &store.ServiceCatalogV3Candidate{
		Generation: generation, ControlRevision: root.ControlRevision,
		PublishedAt: root.PublishedAt,
	}, nil
}

func (s *v3MemoryStore) PublishServiceCatalogV3Candidate(
	_ context.Context,
	generation servicecatalogv3.Generation,
) error {
	if err := servicecatalogv3.ValidateGeneration(generation); err != nil {
		return err
	}
	root := generation.Root
	repository := s.repositories[root.Binding.Repository]
	if repository.Deleting || repository.IndexedCommitHash != root.Binding.Source.Commit {
		return store.ErrConflict
	}
	if s.current == nil {
		s.current = make(map[string]store.ServiceCatalogV3CandidateRoot)
		s.history = make(map[string]servicecatalogv3.Generation)
		s.versions = make(map[string]string)
		s.revisions = make(map[string]uint64)
	}
	versionKey := root.Binding.Repository + "\x00" + root.Binding.Authority.Kind +
		"\x00" + root.Binding.Authority.ID + "\x00" + root.Binding.Authority.Version
	if root.Binding.Override != nil {
		versionKey += "\x00" + root.Binding.Override.ID + "\x00" + root.Binding.Override.Version
	}
	if prior, exists := s.versions[versionKey]; exists && prior != root.LogicalDigest {
		return store.ErrConflict
	}
	if prior, exists := s.history[root.Digest]; exists &&
		!slices.Equal(membersBytes(prior), membersBytes(generation)) {
		return store.ErrConflict
	}
	current := s.current[root.Binding.Repository]
	if current.Root.Digest == root.Digest {
		return nil
	}
	s.versions[versionKey] = root.LogicalDigest
	s.history[root.Digest] = generation
	s.revisions[root.Binding.Repository]++
	s.current[root.Binding.Repository] = store.ServiceCatalogV3CandidateRoot{
		Root: root, ControlRevision: s.revisions[root.Binding.Repository],
		PublishedAt: time.Now().UTC(),
	}
	return nil
}

func membersBytes(generation servicecatalogv3.Generation) []byte {
	var result []byte
	for _, member := range generation.Members {
		result = append(result, member.Content...)
	}
	return result
}
