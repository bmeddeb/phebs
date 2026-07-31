package resolvercatalog

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/store"
)

func testDigest(fill byte) string {
	return "sha256:" + strings.Repeat(string(fill), 64)
}

func testGenerationDigest(fill byte) string {
	return "extraction_generation_v1_" + strings.Repeat(string(fill), 64)
}

func testIdentity(t *testing.T, repository string) Identity {
	t.Helper()
	identity, err := NewIdentity(
		repository, strings.Repeat("1", 40), "", testDigest('a'),
		nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func testPrepared(
	t *testing.T,
	root, repository string,
	withMember bool,
) *Prepared {
	t.Helper()
	stage, err := NewStage(root, testIdentity(t, repository))
	if err != nil {
		t.Fatal(err)
	}
	if withMember {
		err = stage.AddMember(
			t.Context(), "go.ndjson", json.RawMessage(`{"pack":"neutral"}`),
			func(write func(json.RawMessage) error) error {
				for _, raw := range []string{
					`{"name":"a","schema":"phebs-resolver-catalog-record-v1"}`,
					`{"name":"b","schema":"phebs-resolver-catalog-record-v1"}`,
				} {
					if err := write(json.RawMessage(raw)); err != nil {
						return err
					}
				}
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := stage.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func testInstall(
	t *testing.T,
	root, repository string,
	withMember bool,
) State {
	t.Helper()
	state, err := testPrepared(t, root, repository, withMember).Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestNeutralCatalogPublicationAndWarmCurrent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resolver-catalogs")
	repository := "github.com/acme/neutral"
	state := testInstall(t, root, repository, false)
	if _, err := Open(t.Context(), root, state); !errors.Is(err, ErrPublishing) {
		t.Fatalf("Open while marked = %v, want ErrPublishing", err)
	}
	publishing, err := OpenPublishing(t.Context(), root, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(publishing.Manifest().Members) != 0 {
		t.Fatalf("neutral members = %+v, want empty", publishing.Manifest().Members)
	}
	if err := ClearPublishing(root, repository); err != nil {
		t.Fatal(err)
	}
	publication, err := Open(t.Context(), root, state)
	if err != nil {
		t.Fatal(err)
	}
	if !publication.Current() {
		t.Fatal("fresh publication is not current")
	}
	manifestPath := filepath.Join(root, state.Manifest)
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(
		manifestPath, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if publication.Current() {
		t.Fatal("warm check accepted changed file identity")
	}
}

func TestPublishCommitsStoreBeforeMarkerClear(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resolver-catalogs")
	repository := "github.com/acme/publish-order"
	prepared := testPrepared(t, root, repository, false)
	committed := false
	state, err := prepared.Publish(
		t.Context(),
		func(ctx context.Context, state State) error {
			if !IsPublishing(root, repository) {
				t.Fatal("marker cleared before store commit")
			}
			if _, err := OpenPublishing(ctx, root, state); err != nil {
				t.Fatalf("manifest not durable before store commit: %v", err)
			}
			committed = true
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !committed || IsPublishing(root, repository) {
		t.Fatalf("committed=%v marker=%v", committed, IsPublishing(root, repository))
	}
	if _, err := Open(t.Context(), root, state); err != nil {
		t.Fatal(err)
	}
}

func TestPublishCleansOnlyRetiredLocalMembers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resolver-catalogs")
	repository := "github.com/acme/cleanup"
	firstState := testInstall(t, root, repository, true)
	if err := ClearPublishing(root, repository); err != nil {
		t.Fatal(err)
	}
	firstManifest := readTestManifest(t, root, firstState)
	oldMember := filepath.Join(
		root, memberArtifactName(firstManifest.Identity, firstManifest.Members[0].Name),
	)
	foreign := filepath.Join(
		root, resolvercatalogid.ArtifactBase("github.com/other/repository")+"-v1-keep",
	)
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	nextIdentity, err := NewIdentity(
		repository, strings.Repeat("1", 40), "", testDigest('a'), nil,
		[]ResolverPack{{Name: "neutral", Version: "1.0.0"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := NewStage(root, nextIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.AddMember(
		t.Context(), "go.ndjson", json.RawMessage(`{"pack":"neutral"}`),
		func(write func(json.RawMessage) error) error {
			return write(json.RawMessage(
				`{"schema":"phebs-resolver-catalog-record-v1"}`,
			))
		},
	); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	nextState, err := prepared.Publish(
		t.Context(), func(context.Context, State) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldMember); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired member remains: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign member removed: %v", err)
	}
	if _, err := Open(t.Context(), root, nextState); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogColdValidationRejectsTamperSymlinkAndDescriptorSwap(t *testing.T) {
	t.Run("tamper", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		state := testInstall(t, root, "github.com/acme/tamper", true)
		if err := ClearPublishing(root, state.Repository); err != nil {
			t.Fatal(err)
		}
		manifest := readTestManifest(t, root, state)
		memberPath := filepath.Join(
			root, memberArtifactName(manifest.Identity, manifest.Members[0].Name),
		)
		if err := os.WriteFile(memberPath, []byte(
			`{"name":"x","schema":"phebs-resolver-catalog-record-v1"}`+"\n",
		), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(t.Context(), root, state); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("tampered Open = %v, want ErrInvalidManifest", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		state := testInstall(t, root, "github.com/acme/symlink", true)
		if err := ClearPublishing(root, state.Repository); err != nil {
			t.Fatal(err)
		}
		manifest := readTestManifest(t, root, state)
		memberPath := filepath.Join(
			root, memberArtifactName(manifest.Identity, manifest.Members[0].Name),
		)
		realPath := filepath.Join(root, "outside")
		raw, err := os.ReadFile(memberPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(realPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(memberPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realPath, memberPath); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(t.Context(), root, state); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("symlink Open = %v, want ErrInvalidManifest", err)
		}
	})
	t.Run("descriptor swap", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		state := testInstall(t, root, "github.com/acme/swap", true)
		if err := ClearPublishing(root, state.Repository); err != nil {
			t.Fatal(err)
		}
		manifest := readTestManifest(t, root, state)
		memberPath := filepath.Join(
			root, memberArtifactName(manifest.Identity, manifest.Members[0].Name),
		)
		replacement := filepath.Join(root, "replacement")
		raw, err := os.ReadFile(memberPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(replacement, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		swapped := false
		testAfterStableOpen = func(opened string) {
			if opened == memberPath && !swapped {
				swapped = true
				if err := os.Rename(replacement, memberPath); err != nil {
					t.Fatalf("swap descriptor path: %v", err)
				}
			}
		}
		t.Cleanup(func() { testAfterStableOpen = nil })
		if _, err := Open(t.Context(), root, state); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("descriptor-swap Open = %v, want ErrInvalidManifest", err)
		}
	})
}

func TestCatalogBoundsAtCapAndCapPlusOne(t *testing.T) {
	if got := FrozenPolicy().MaxDirectoryEntries; got != MaxDirectoryEntries {
		t.Fatalf(
			"policy directory entries = %d, want %d",
			got, MaxDirectoryEntries,
		)
	}
	declarations := make([]DeclarationPublication, MaxDeclarationPublications)
	for index := range declarations {
		declarations[index] = DeclarationPublication{
			Domain:           strings.Repeat("a", 4) + string(rune('a'+index)),
			RunID:            "run-" + string(rune('a'+index)),
			GenerationDigest: testGenerationDigest('b'),
		}
	}
	if _, err := NewIdentity(
		"github.com/acme/cap", strings.Repeat("1", 40), "", testDigest('a'),
		declarations, nil,
	); err != nil {
		t.Fatalf("identity at cap: %v", err)
	}
	over := append(slices.Clone(declarations), DeclarationPublication{
		Domain: "zzzz", RunID: "run-z",
		GenerationDigest: testGenerationDigest('b'),
	})
	if _, err := NewIdentity(
		"github.com/acme/over", strings.Repeat("1", 40), "", testDigest('a'),
		over, nil,
	); !errors.Is(err, ErrLimit) {
		t.Fatalf("identity cap+1 = %v, want ErrLimit", err)
	}

	root := filepath.Join(t.TempDir(), "catalogs")
	stage, err := NewStage(root, testIdentity(t, "github.com/acme/records"))
	if err != nil {
		t.Fatal(err)
	}
	err = stage.AddMember(
		t.Context(), "cap.ndjson", json.RawMessage(`{}`),
		func(write func(json.RawMessage) error) error {
			for range MaxRecordsPerMember {
				if err := write(json.RawMessage(
					`{"schema":"phebs-resolver-catalog-record-v1"}`,
				)); err != nil {
					return err
				}
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("records at cap: %v", err)
	}

	directory := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(
			filepath.Join(directory, name), nil, 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := readDirectoryUpTo(
		directory, 2,
	); !errors.Is(err, ErrLimit) {
		t.Fatalf("directory cap+1 = %v, want ErrLimit", err)
	}
	if err := os.Remove(filepath.Join(directory, "c")); err != nil {
		t.Fatal(err)
	}
	if entries, err := readDirectoryUpTo(
		directory, 2,
	); err != nil || len(entries) != 2 {
		t.Fatalf("directory at cap = %d, %v; want 2", len(entries), err)
	}
}

func TestCleanupStagesAndReconcileCrashBoundaries(t *testing.T) {
	t.Run("prior stage", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		if _, err := NewStage(root, testIdentity(t, "github.com/acme/stage")); err != nil {
			t.Fatal(err)
		}
		removed, err := CleanupStages(t.Context(), root)
		if err != nil || removed != 1 {
			t.Fatalf("CleanupStages = %d, %v; want 1", removed, err)
		}
	})
	t.Run("manifest durable before store", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/recover"
		state := testInstall(t, root, repository, true)
		fake := newFakeReconcileStore()
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.MarkersRecovered != 1 || IsPublishing(root, repository) {
			t.Fatalf("report = %+v, marker=%v", report, IsPublishing(root, repository))
		}
		if got := fake.publications[repository]; got.ManifestDigest != state.ManifestDigest {
			t.Fatalf("recovered pointer = %+v, want %s", got, state.ManifestDigest)
		}
	})
	t.Run("store committed before marker clear", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/store-first"
		state := testInstall(t, root, repository, false)
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(state)
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.MarkersRecovered != 1 || report.ReplacementsQueued != 0 {
			t.Fatalf("report = %+v", report)
		}
	})
	t.Run("replacement manifest durable before store", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/replacement-recover"
		oldState := testInstall(t, root, repository, false)
		if err := ClearPublishing(root, repository); err != nil {
			t.Fatal(err)
		}
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(oldState)

		identity, err := NewIdentity(
			repository, strings.Repeat("1", 40), "", testDigest('a'), nil,
			[]ResolverPack{{Name: "neutral", Version: "1.0.0"}},
		)
		if err != nil {
			t.Fatal(err)
		}
		stage, err := NewStage(root, identity)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := stage.Seal(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		replacementState, err := prepared.Install(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		report, err := Reconcile(
			t.Context(), root, fake,
			[]ResolverPack{{Name: "neutral", Version: "1.0.0"}},
		)
		if err != nil {
			t.Fatal(err)
		}
		if report.MarkersRecovered != 1 ||
			report.ReplacementsQueued != 0 ||
			IsPublishing(root, repository) {
			t.Fatalf("report = %+v, marker=%v", report, IsPublishing(root, repository))
		}
		if got := fake.publications[repository]; got.ManifestDigest !=
			replacementState.ManifestDigest {
			t.Fatalf(
				"recovered replacement = %+v, want %s",
				got, replacementState.ManifestDigest,
			)
		}
		if !slices.Equal(fake.operations, []string{"publish:" + repository}) {
			t.Fatalf("operations = %v", fake.operations)
		}
	})
	t.Run("marker before manifest queues before cleanup", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/marker-only"
		if err := ensureRealDirectory(root); err != nil {
			t.Fatal(err)
		}
		if err := installMarker(root, repository); err != nil {
			t.Fatal(err)
		}
		owned := filepath.Join(root, resolvercatalogid.ArtifactBase(repository)+"-partial")
		if err := os.WriteFile(owned, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(root, resolvercatalogid.ArtifactBase("github.com/other/repo")+"-keep")
		if err := os.WriteFile(other, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		fake := newFakeReconcileStore()
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.ReplacementsQueued != 1 || !slices.Equal(fake.operations, []string{"queue:" + repository}) {
			t.Fatalf("report=%+v operations=%v", report, fake.operations)
		}
		if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned partial remains: %v", err)
		}
		if _, err := os.Stat(other); err != nil {
			t.Fatalf("other repository artifact removed: %v", err)
		}
	})
	t.Run("invalid pointer queues before clear", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		if err := ensureRealDirectory(root); err != nil {
			t.Fatal(err)
		}
		repository := "github.com/acme/invalid-pointer"
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(
			testIdentityManifest(t, repository).State(),
		)
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.PointersCleared != 1 || !slices.Equal(fake.operations, []string{
			"queue:" + repository, "clear:" + repository,
		}) {
			t.Fatalf("report=%+v operations=%v", report, fake.operations)
		}
	})
}

func TestCatalogArchiveExactAndBoundedOmissions(t *testing.T) {
	t.Run("exact restore", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		state := testInstall(t, root, "github.com/acme/archive", true)
		if err := ClearPublishing(root, state.Repository); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(t.TempDir(), "catalog.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.Publications != 1 || report.OmittedPublications != 0 {
			t.Fatalf("archive report = %+v", report)
		}
		restored := filepath.Join(t.TempDir(), "restored")
		if err := RestoreArchive(archive, restored); err != nil {
			t.Fatal(err)
		}
		assertDirectoryBytesEqual(t, root, restored)
	})
	t.Run("marker omission", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		_ = testInstall(t, root, "github.com/acme/marked", false)
		archive := filepath.Join(t.TempDir(), "catalog.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.Publications != 0 || report.OmittedPublications != 1 ||
			report.StaleMarkers != 1 {
			t.Fatalf("archive report = %+v", report)
		}
		assertEmptyTar(t, archive)
	})
	t.Run("invalid publication omission", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		state := testInstall(t, root, "github.com/acme/invalid-archive", true)
		if err := ClearPublishing(root, state.Repository); err != nil {
			t.Fatal(err)
		}
		manifest := readTestManifest(t, root, state)
		memberPath := filepath.Join(
			root, memberArtifactName(manifest.Identity, manifest.Members[0].Name),
		)
		if err := os.WriteFile(memberPath, []byte("damage\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(t.TempDir(), "catalog.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.Publications != 0 || report.OmittedPublications != 1 ||
			report.OmittedArtifacts < 2 {
			t.Fatalf("archive report = %+v", report)
		}
		assertEmptyTar(t, archive)
	})
	t.Run("bounded report", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		if err := ensureRealDirectory(root); err != nil {
			t.Fatal(err)
		}
		for index := range MaxOmissionDetails + 6 {
			name := filepath.Join(root, "phebs-resolver-catalog-orphan-"+string(rune('a'+index%26))+
				strings.Repeat("x", index/26))
			if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		archive := filepath.Join(t.TempDir(), "catalog.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.OmittedArtifacts != MaxOmissionDetails+6 ||
			len(report.Details) != MaxOmissionDetails ||
			report.TruncatedDetails != 6 {
			t.Fatalf("archive report = %+v", report)
		}
	})
}

func readTestManifest(t *testing.T, root string, state State) Manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, state.Manifest))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func testIdentityManifest(t *testing.T, repository string) Manifest {
	t.Helper()
	manifest := Manifest{Schema: ManifestSchema, Identity: testIdentity(t, repository)}
	unsigned := manifest
	unsigned.Digest = ""
	digest, err := digestCanonical(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Digest = digest
	return manifest
}

func assertDirectoryBytesEqual(t *testing.T, left, right string) {
	t.Helper()
	leftEntries, err := os.ReadDir(left)
	if err != nil {
		t.Fatal(err)
	}
	rightEntries, err := os.ReadDir(right)
	if err != nil {
		t.Fatal(err)
	}
	leftNames := make([]string, len(leftEntries))
	rightNames := make([]string, len(rightEntries))
	for index := range leftEntries {
		leftNames[index] = leftEntries[index].Name()
	}
	for index := range rightEntries {
		rightNames[index] = rightEntries[index].Name()
	}
	if !slices.Equal(leftNames, rightNames) {
		t.Fatalf("entry names differ: %v != %v", leftNames, rightNames)
	}
	for _, name := range leftNames {
		leftRaw, err := os.ReadFile(filepath.Join(left, name))
		if err != nil {
			t.Fatal(err)
		}
		rightRaw, err := os.ReadFile(filepath.Join(right, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(leftRaw) != string(rightRaw) {
			t.Fatalf("artifact %q differs", name)
		}
	}
}

func assertEmptyTar(t *testing.T, archive string) {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := tar.NewReader(file).Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty archive first entry = %v", err)
	}
}

type fakeReconcileStore struct {
	publications map[string]store.ResolverCatalogPublication
	jobs         []store.Job
	operations   []string
}

func newFakeReconcileStore() *fakeReconcileStore {
	return &fakeReconcileStore{
		publications: make(map[string]store.ResolverCatalogPublication),
	}
}

func (fake *fakeReconcileStore) ListResolverCatalogPublications(
	context.Context,
) ([]store.ResolverCatalogPublication, error) {
	values := make([]store.ResolverCatalogPublication, 0, len(fake.publications))
	for _, publication := range fake.publications {
		values = append(values, publication)
	}
	slices.SortFunc(values, func(left, right store.ResolverCatalogPublication) int {
		return strings.Compare(left.Repository, right.Repository)
	})
	return values, nil
}

func (fake *fakeReconcileStore) GetResolverCatalogPublication(
	_ context.Context,
	repository string,
) (*store.ResolverCatalogPublication, error) {
	publication, ok := fake.publications[repository]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &publication, nil
}

func (fake *fakeReconcileStore) ResolverCatalogPublicationCurrent(
	context.Context,
	store.ResolverCatalogPublication,
) (bool, error) {
	return true, nil
}

func (fake *fakeReconcileStore) PublishResolverCatalog(
	_ context.Context,
	publication store.ResolverCatalogPublication,
) error {
	publication.ControlRevision = 1
	publication.WriterSchema = "phebs-resolver-catalog-store-v1"
	publication.PublishedAt = time.Now().UTC()
	fake.publications[publication.Repository] = publication
	fake.operations = append(fake.operations, "publish:"+publication.Repository)
	return nil
}

func (fake *fakeReconcileStore) ClearResolverCatalogPublication(
	_ context.Context,
	repository string,
) error {
	delete(fake.publications, repository)
	fake.operations = append(fake.operations, "clear:"+repository)
	return nil
}

func (fake *fakeReconcileStore) EnqueuePending(
	_ context.Context,
	kind store.JobKind,
	target string,
	force bool,
) (*store.Job, error) {
	if kind != store.JobResolverCatalog || !force {
		return nil, errors.New("unexpected replacement request")
	}
	fake.operations = append(fake.operations, "queue:"+target)
	job := store.Job{ID: "job", Target: target, Status: store.StatusPending, Force: force}
	fake.jobs = append(fake.jobs, job)
	return &job, nil
}
