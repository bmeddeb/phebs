package servicecatalogingest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestCommittedCatalogReconcileAndFailedReplacementPreservesPrior(t *testing.T) {
	repository := "example.com/acme/mono"
	dataDir, mirror, commit := testMirror(t, repository, map[string]string{
		"README.md":           "mono\n",
		"shared/schema.proto": "message Order {}\n",
		"svc/main.go":         "package main\n",
	})
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	catalog := testCatalog(commit, "Orders")
	writeCatalog(t, catalogPath, catalog)
	state := &memoryStore{repositories: map[string]store.Repo{
		repository: {
			Name: repository, IndexedCommitHash: commit,
		},
	}}
	reconciler := Reconciler{
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
		t.Fatalf("first reconcile = %q, %v", outcome, err)
	}
	prior := state.current[repository]
	if prior.SourceCommit != commit || prior.SourceFileCount != 3 ||
		prior.AcceptedFileCount != 2 || prior.UnownedFileCount != 1 ||
		prior.ControlRevision != 1 || state.stateReconciles != 1 {
		t.Fatalf("published binding = %+v", prior)
	}

	// Exact catalog/source identity reads the small selected input to detect
	// version reuse, but performs no Git/corpus read on a restart no-op.
	hiddenMirror := mirror + ".hidden"
	if err := os.Rename(mirror, hiddenMirror); err != nil {
		t.Fatal(err)
	}
	outcome, err = reconciler.ReconcileRepository(t.Context(), repository)
	if err != nil || outcome != OutcomeCurrent {
		t.Fatalf("restart no-op without mirror = %q, %v", outcome, err)
	}
	if state.stateReconciles != 2 {
		t.Fatalf("restart state reconciles = %d, want 2", state.stateReconciles)
	}
	if err := os.Rename(hiddenMirror, mirror); err != nil {
		t.Fatal(err)
	}

	// Reusing the same committed version for different canonical bytes is an
	// immutable-authority conflict. The prior pointer is unchanged.
	catalog.Services[0].DisplayName = "Orders API"
	writeCatalog(t, catalogPath, catalog)
	outcome, err = reconciler.ReconcileRepository(t.Context(), repository)
	if !errors.Is(err, store.ErrConflict) || outcome != "" {
		t.Fatalf("reused-version reconcile = %q, %v", outcome, err)
	}
	after := state.current[repository]
	if after.GenerationDigest != prior.GenerationDigest ||
		after.ControlRevision != prior.ControlRevision {
		t.Fatalf("failed replacement changed prior authority: before=%+v after=%+v", prior, after)
	}
}

func TestAnalysisUnitV1ImportPreservesDigestRolesAndComplement(t *testing.T) {
	repository := "example.com/acme/legacy"
	dataDir, mirror, commit := testMirror(t, repository, map[string]string{
		"README.md":      "legacy\n",
		"svc/index.scip": "typed\n",
		"svc/main.go":    "package main\n",
	})
	unit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "orders",
		Primary:    []string{"svc/main.go"},
		Supporting: []string{"svc/index.scip"},
		TypedIndex: &analysisunit.TypedIndex{
			Kind: analysisunit.TypedIndexKindSCIP, Path: "svc/index.scip",
		},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	state := &memoryStore{repositories: map[string]store.Repo{
		repository: {
			Name: repository, IndexedCommitHash: commit,
			IndexedAnalysisUnit: unit,
		},
	}}
	reconciler := Reconciler{DataDir: dataDir, Store: state}
	outcome, err := reconciler.ReconcileRepository(t.Context(), repository)
	if err != nil || outcome != OutcomeLegacyImported {
		t.Fatalf("legacy import = %q, %v", outcome, err)
	}
	publication := state.current[repository]
	if publication.LegacyAnalysisUnitDigest != unit.Digest ||
		publication.Authority.Version != unit.Digest ||
		publication.SourceFileCount != 3 || publication.AcceptedFileCount != 2 ||
		publication.UnownedFileCount != 1 {
		t.Fatalf("legacy publication = %+v", publication)
	}
	catalog, err := servicecatalog.Decode(publication.Canonical)
	if err != nil {
		t.Fatal(err)
	}
	roles := make([]string, 0, len(catalog.Memberships))
	for _, membership := range catalog.Memberships {
		roles = append(roles, membership.Path+":"+membership.Role)
	}
	slices.Sort(roles)
	if want := []string{
		"svc/index.scip:supporting", "svc/index.scip:typed", "svc/main.go:primary",
	}; !slices.Equal(roles, want) {
		t.Fatalf("legacy roles = %v, want %v", roles, want)
	}
	if len(catalog.Unowned) != 1 || catalog.Unowned[0].Path != "README.md" {
		t.Fatalf("legacy unowned complement = %+v", catalog.Unowned)
	}
	if state.stateReconciles != 1 {
		t.Fatalf("legacy state reconciles = %d, want 1", state.stateReconciles)
	}

	// Exact v1/source identity is metadata-only on restart.
	if err := os.Rename(mirror, mirror+".hidden"); err != nil {
		t.Fatal(err)
	}
	outcome, err = reconciler.ReconcileRepository(t.Context(), repository)
	if err != nil || outcome != OutcomeCurrent {
		t.Fatalf("legacy restart no-op = %q, %v", outcome, err)
	}
	if state.stateReconciles != 2 {
		t.Fatalf("legacy restart state reconciles = %d, want 2", state.stateReconciles)
	}
}

func TestCatalogCensusRefusesGapsAndSpecialPlacements(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		edit  func(*servicecatalog.Catalog)
		want  string
	}{
		{
			name: "unclassified regular file",
			files: map[string]string{
				"README.md": "gap\n", "shared/schema.proto": "schema\n",
				"svc/main.go": "package main\n",
			},
			edit: func(catalog *servicecatalog.Catalog) { catalog.Unowned = nil },
			want: "accepted/unowned complement",
		},
		{
			name: "selected symlink is not a regular file",
			files: map[string]string{
				"README.md": "mono\n", "shared/schema.proto": "schema\n",
				"svc/main.go": "package main\n", "svc/link": "SYMLINK:main.go",
			},
			edit: func(catalog *servicecatalog.Catalog) {
				catalog.Memberships[0].Path = "svc/main.go"
				catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
					ServiceKey: "orders", Path: "svc/link",
					Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase,
				})
			},
			want: "absent from the source census",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := "example.com/acme/refusal"
			dataDir, _, commit := testMirror(t, repository, tt.files)
			catalog := testCatalog(commit, "Orders")
			tt.edit(&catalog)
			catalogPath := filepath.Join(t.TempDir(), "catalog.json")
			writeCatalog(t, catalogPath, catalog)
			state := &memoryStore{repositories: map[string]store.Repo{
				repository: {Name: repository, IndexedCommitHash: commit},
			}}
			reconciler := Reconciler{
				DataDir: dataDir, Store: state,
				Selections: map[string]config.ServiceCatalog{
					repository: {
						Kind: servicecatalog.AuthorityCommitted,
						ID:   "build-catalog", Path: catalogPath,
					},
				},
			}
			if _, err := reconciler.ReconcileRepository(t.Context(), repository); err == nil ||
				!strings.Contains(err.Error(), tt.want) {
				t.Fatalf("reconcile error = %v, want %q", err, tt.want)
			}
			if len(state.current) != 0 {
				t.Fatalf("refused catalog published authority: %+v", state.current)
			}
		})
	}
}

func TestReconcileIsRepositoryIndependentAndDoesNotGuess(t *testing.T) {
	state := &memoryStore{repositories: map[string]store.Repo{
		"example.com/acme/unselected": {
			Name:              "example.com/acme/unselected",
			IndexedCommitHash: "1111111111111111111111111111111111111111",
		},
		"example.com/acme/not-ready": {Name: "example.com/acme/not-ready"},
	}}
	report, err := (&Reconciler{Store: state}).Reconcile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Unselected != 1 || report.NotReady != 1 || len(report.Failures) != 0 ||
		len(state.current) != 0 {
		t.Fatalf("non-guessing report = %+v, current=%+v", report, state.current)
	}
}

type memoryStore struct {
	repositories    map[string]store.Repo
	current         map[string]servicecatalog.Publication
	history         []servicecatalog.Publication
	stateReconciles int
}

func (s *memoryStore) GetRepo(_ context.Context, repository string) (*store.Repo, error) {
	value, ok := s.repositories[repository]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &value, nil
}

func (s *memoryStore) ListRepos(context.Context) ([]store.Repo, error) {
	values := make([]store.Repo, 0, len(s.repositories))
	for _, repository := range s.repositories {
		values = append(values, repository)
	}
	slices.SortFunc(values, func(left, right store.Repo) int {
		return strings.Compare(left.Name, right.Name)
	})
	return values, nil
}

func (s *memoryStore) GetServiceCatalog(
	_ context.Context,
	repository string,
) (*servicecatalog.Publication, error) {
	publication, ok := s.current[repository]
	if !ok {
		return nil, store.ErrNotFound
	}
	clone := publication
	clone.Canonical = slices.Clone(publication.Canonical)
	return &clone, nil
}

func (s *memoryStore) PublishServiceCatalog(
	_ context.Context,
	publication servicecatalog.Publication,
) error {
	if err := servicecatalog.ValidatePublication(publication, false); err != nil {
		return err
	}
	repository := s.repositories[publication.Repository]
	if repository.IndexedCommitHash != publication.SourceCommit || repository.Deleting ||
		publication.LegacyAnalysisUnitDigest != "" &&
			(repository.IndexedAnalysisUnit == nil ||
				repository.IndexedAnalysisUnit.Digest != publication.LegacyAnalysisUnitDigest) {
		return store.ErrConflict
	}
	for _, existing := range s.history {
		if existing.Repository == publication.Repository &&
			existing.Authority == publication.Authority &&
			overrideEqual(existing.Override, publication.Override) &&
			existing.CatalogDigest != publication.CatalogDigest {
			return store.ErrConflict
		}
	}
	if s.current == nil {
		s.current = make(map[string]servicecatalog.Publication)
	}
	prior, exists := s.current[publication.Repository]
	if exists && prior.GenerationDigest == publication.GenerationDigest {
		return nil
	}
	publication.ControlRevision = 1
	if exists {
		publication.ControlRevision = prior.ControlRevision + 1
	}
	publication.PublishedAt = time.Now().UTC()
	s.current[publication.Repository] = publication
	s.history = append(s.history, publication)
	return nil
}

func (s *memoryStore) ReconcileServiceStates(
	_ context.Context,
	publication servicecatalog.Publication,
) error {
	current, exists := s.current[publication.Repository]
	if !exists || current.GenerationDigest != publication.GenerationDigest ||
		current.ControlRevision != publication.ControlRevision {
		return store.ErrConflict
	}
	s.stateReconciles++
	return nil
}

func overrideEqual(left, right *servicecatalog.OperatorOverride) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func testCatalog(commit, displayName string) servicecatalog.Catalog {
	return servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityCommitted,
			ID:   "build-catalog", Version: commit,
		},
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: displayName,
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{
			{ServiceKey: "orders", Path: "svc", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase},
			{ServiceKey: "orders", Path: "shared/schema.proto", Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase},
		},
		Unowned: []servicecatalog.UnownedPlacement{{
			Path: "README.md", Origin: servicecatalog.OriginBase,
		}},
	}
}

func writeCatalog(t *testing.T, destination string, catalog servicecatalog.Catalog) {
	t.Helper()
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
}

func testMirror(
	t *testing.T,
	repository string,
	files map[string]string,
) (dataDir, mirror, commit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	work := t.TempDir()
	testGit(t, work, "init", "-q")
	for name, content := range files {
		full := filepath.Join(work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(content, "SYMLINK:") {
			if err := os.Symlink(strings.TrimPrefix(content, "SYMLINK:"), full); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, work, "add", ".")
	testGit(t, work, "-c", "user.name=Phebs Test", "-c", "user.email=test@example.com",
		"commit", "-q", "-m", "fixture")
	commit = strings.TrimSpace(testGit(t, work, "rev-parse", "HEAD"))
	dataDir = t.TempDir()
	mirror = filepath.Join(dataDir, "repos", filepath.FromSlash(repository)+".git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	testGit(t, "", "clone", "-q", "--bare", work, mirror)
	return dataDir, mirror, commit
}

func testGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
