package callerpublication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/reponame"
)

type reconcileTestStore struct {
	repositories              []string
	pointers                  map[string]State
	forced                    []string
	cleared                   []string
	pageCalls                 int
	rejectInvalidRepositories bool
}

func (store *reconcileTestStore) ListCallerPublicationRepositoriesPage(
	_ context.Context,
	after string,
	limit int,
) ([]string, error) {
	store.pageCalls++
	result := make([]string, 0, limit)
	for _, repository := range store.repositories {
		if repository <= after {
			continue
		}
		result = append(result, repository)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func TestStartupReconcilePagesEligibleRepositoriesWithoutTotalRetention(t *testing.T) {
	state := &reconcileTestStore{pointers: make(map[string]State)}
	for index := range reconcileRepositoryPageSize + 1 {
		state.repositories = append(
			state.repositories,
			fmt.Sprintf("github.com/acme/reconcile-%04d", index),
		)
	}
	root := filepath.Join(t.TempDir(), "caller-leaves")
	if _, err := Reconcile(
		t.Context(), root, state,
		Expected{Extractors: []callerleaf.ExtractorIdentity{}}, NewRegistry(root),
	); err != nil {
		t.Fatal(err)
	}
	if state.pageCalls != 2 {
		t.Fatalf("eligible repository page calls = %d, want 2", state.pageCalls)
	}
}

func (store *reconcileTestStore) CallerPublicationRepositoryEligible(
	_ context.Context,
	repository string,
) (bool, error) {
	if store.rejectInvalidRepositories {
		if err := reponame.Validate(repository); err != nil {
			return false, fmt.Errorf("production-shaped eligibility: %w", err)
		}
	}
	return slices.Contains(store.repositories, repository), nil
}

func (store *reconcileTestStore) ListCallerPublications(context.Context) ([]State, error) {
	result := make([]State, 0, len(store.pointers))
	for _, pointer := range store.pointers {
		result = append(result, pointer)
	}
	return result, nil
}

func (store *reconcileTestStore) CallerPublicationCurrent(
	_ context.Context,
	pointer State,
) (bool, error) {
	current, ok := store.pointers[pointer.Generation.Repository]
	return ok && reflect.DeepEqual(current, pointer), nil
}

func (store *reconcileTestStore) ClearCallerPublication(
	_ context.Context,
	repository string,
) error {
	delete(store.pointers, repository)
	store.cleared = append(store.cleared, repository)
	return nil
}

func (store *reconcileTestStore) ForceCallerReplacement(
	_ context.Context,
	repository string,
) error {
	store.forced = append(store.forced, repository)
	return nil
}

func TestStartupReconcileNeverPromotesPointerlessMarkedPublication(t *testing.T) {
	root := t.TempDir() + "/caller-leaves"
	repository := "github.com/acme/pointerless-marker"
	fixture := newPublicationFixture(t, root, repository, '7')
	prepared, err := Prepare(t.Context(), root, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Publish(
		t.Context(), func(context.Context, State) error {
			return errors.New("store unavailable before commit")
		},
	); err == nil {
		t.Fatal("Publish unexpectedly succeeded")
	}
	state := &reconcileTestStore{
		repositories: []string{repository}, pointers: make(map[string]State),
	}
	report, err := Reconcile(
		t.Context(), root, state,
		Expected{Extractors: fixture.generation.Extractors}, NewRegistry(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.pointers) != 0 || len(state.forced) != 1 ||
		report.ReplacementsQueued != 1 || report.MarkersRecovered != 0 {
		t.Fatalf("startup promoted marker: report=%+v pointers=%v forced=%v", report, state.pointers, state.forced)
	}
	if marked, err := Publishing(root, repository); err != nil || !marked {
		t.Fatalf("claimed-worker marker was not retained: %v, %v", marked, err)
	}
	commits := 0
	publication, err := RecoverPublishing(
		t.Context(), root, repository, fixture.generation.Extractors,
		func(context.Context, State) error { commits++; return nil },
	)
	if err != nil || commits != 1 || !publication.Current() {
		t.Fatalf("claimed recovery = current:%v commits:%d err:%v", publication != nil && publication.Current(), commits, err)
	}
}

func TestStartupReconcileClearsOnlyExactPostCommitMarker(t *testing.T) {
	root := t.TempDir() + "/caller-leaves"
	repository := "github.com/acme/post-commit-marker"
	fixture := newPublicationFixture(t, root, repository, '8')
	prepared, err := Prepare(t.Context(), root, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	state := &reconcileTestStore{
		repositories: []string{repository}, pointers: make(map[string]State),
	}
	lostAck := errors.New("lost store acknowledgement")
	if _, err := prepared.Publish(
		t.Context(), func(_ context.Context, pointer State) error {
			state.pointers[repository] = pointer
			return lostAck
		},
	); !errors.Is(err, lostAck) {
		t.Fatalf("Publish = %v, want lost ack", err)
	}
	report, err := Reconcile(
		t.Context(), root, state,
		Expected{Extractors: fixture.generation.Extractors}, NewRegistry(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.MarkersRecovered != 1 || report.PublicationsCurrent != 1 ||
		len(state.forced) != 0 || len(state.cleared) != 0 {
		t.Fatalf("post-commit reconcile = report:%+v forced:%v cleared:%v", report, state.forced, state.cleared)
	}
	if marked, err := Publishing(root, repository); err != nil || marked {
		t.Fatalf("exact post-commit marker remains: %v, %v", marked, err)
	}
}

func TestStartupReconcileHashesCurrentLeavesOnce(t *testing.T) {
	resetLifecycleHooks(t)
	root := t.TempDir() + "/caller-leaves"
	repository := "github.com/acme/single-cold-open"
	fixture := newPublicationFixture(t, root, repository, '9')
	publication := publishFixture(t, fixture)
	state := &reconcileTestStore{
		repositories: []string{repository},
		pointers:     map[string]State{repository: publication.State()},
	}
	coldOpens := 0
	testArtifactColdOpen = func(string) { coldOpens++ }
	report, err := Reconcile(
		t.Context(), root, state,
		Expected{Extractors: fixture.generation.Extractors}, NewRegistry(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.PublicationsCurrent != 1 || coldOpens != len(fixture.manifest.Pairs) {
		t.Fatalf(
			"reconcile report=%+v cold leaf opens=%d, want %d",
			report, coldOpens, len(fixture.manifest.Pairs),
		)
	}
}

func TestStartupReconcileReclaimsOnlyPointerlessIneligiblePublication(t *testing.T) {
	root := t.TempDir() + "/caller-leaves"
	eligibleRepository := "github.com/acme/eligible-pointerless"
	ineligibleRepository := "github.com/acme/ineligible-pointerless"
	eligible := publishFixture(t, newPublicationFixture(
		t, root, eligibleRepository, 'b',
	))
	ineligible := publishFixture(t, newPublicationFixture(
		t, root, ineligibleRepository, 'c',
	))
	ineligibleRefs := ineligible.ArtifactRefs()
	if len(ineligibleRefs) == 0 {
		t.Fatal("ineligible publication has no artifact references")
	}
	ineligibleDirectory := filepath.Join(
		root, ineligibleRefs[0].RepositoryDirectory,
	)
	foreignPath := filepath.Join(ineligibleDirectory, "operator-owned.txt")
	if err := os.WriteFile(foreignPath, []byte("retain me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	state := &reconcileTestStore{
		repositories: []string{eligibleRepository},
		pointers:     make(map[string]State),
	}
	report, err := Reconcile(
		t.Context(), root, state,
		Expected{Extractors: eligible.State().Generation.Extractors},
		NewRegistry(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.OrphansObserved != 2 || report.ReplacementsQueued != 1 ||
		!reflect.DeepEqual(state.forced, []string{eligibleRepository}) {
		t.Fatalf(
			"pointerless reconciliation = report:%+v forced:%v",
			report, state.forced,
		)
	}
	if !eligible.Current() {
		t.Fatal("eligible pointerless publication was reclaimed")
	}
	for _, reference := range ineligibleRefs {
		artifactPath := filepath.Join(root, filepath.FromSlash(reference.RelativePath()))
		if _, err := os.Lstat(artifactPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ineligible artifact %q remains: %v", reference.RelativePath(), err)
		}
	}
	if raw, err := os.ReadFile(foreignPath); err != nil || string(raw) != "retain me\n" {
		t.Fatalf("foreign repository entry = %q, %v", raw, err)
	}
}

func TestStartupReconcileResumesIneligibleMarkedCleanup(t *testing.T) {
	root := t.TempDir() + "/caller-leaves"
	repository := "github.com/acme/ineligible-marked"
	fixture := newPublicationFixture(t, root, repository, 'd')
	prepared, err := Prepare(t.Context(), root, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Publish(
		t.Context(), func(context.Context, State) error {
			return errors.New("store unavailable")
		},
	); err == nil {
		t.Fatal("marked publication unexpectedly committed")
	}
	refs := ArtifactRefs(fixture.manifest)
	state := &reconcileTestStore{pointers: make(map[string]State)}
	report, err := Reconcile(
		t.Context(), root, state,
		Expected{Extractors: fixture.generation.Extractors}, NewRegistry(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReplacementsQueued != 0 || len(state.forced) != 0 {
		t.Fatalf("ineligible marked residue queued work: %+v, %v", report, state.forced)
	}
	if marked, err := Publishing(root, repository); err != nil || marked {
		t.Fatalf("ineligible marker remains: %v, %v", marked, err)
	}
	for _, reference := range refs {
		if _, err := os.Lstat(filepath.Join(
			root, filepath.FromSlash(reference.RelativePath()),
		)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ineligible marked artifact remains: %s: %v", reference.RelativePath(), err)
		}
	}
}

func TestStartupReconcileDiscardsIneligibleMarkerBeforeManifest(t *testing.T) {
	resetLifecycleHooks(t)
	root := t.TempDir() + "/caller-leaves"
	repository := "github.com/acme/ineligible-marker-before-manifest"
	fixture := newPublicationFixture(t, root, repository, 'e')
	prepared, err := Prepare(t.Context(), root, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	crash := errors.New("crash after marker link")
	testAfterMarkerInstall = func(State) error { return crash }
	if _, err := prepared.Publish(
		t.Context(), func(context.Context, State) error { return nil },
	); !errors.Is(err, crash) {
		t.Fatalf("Publish = %v, want crash", err)
	}
	testAfterMarkerInstall = nil
	manifestPath := filepath.Join(
		root, callerpublicationid.RepositoryDirectory(repository),
		fixture.manifest.State().Manifest,
	)
	if _, err := os.Lstat(manifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest exists before reconcile: %v", err)
	}

	state := &reconcileTestStore{pointers: make(map[string]State)}
	report, err := Reconcile(
		t.Context(), root, state,
		Expected{Extractors: fixture.generation.Extractors}, NewRegistry(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReplacementsQueued != 0 || len(state.forced) != 0 {
		t.Fatalf("ineligible marker queued work: report=%+v forced=%v", report, state.forced)
	}
	if marked, err := Publishing(root, repository); err != nil || marked {
		t.Fatalf("marker-before-manifest remains: %v, %v", marked, err)
	}
	if prepared.stageName != "" {
		stagePath := filepath.Join(
			root, callerpublicationid.RepositoryDirectory(repository), prepared.stageName,
		)
		if _, err := os.Lstat(stagePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("marker-before-manifest stage remains: %v", err)
		}
	}
}

func TestStartupReconcileRepairsMarkerWithInvalidRepositoryIdentity(t *testing.T) {
	root := t.TempDir() + "/caller-leaves"
	invalidRepository := "../invalid"
	directory := filepath.Join(
		root, callerpublicationid.RepositoryDirectory(invalidRepository),
	)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	generationDigest := "sha256:" + strings.Repeat("a", 64)
	manifestDigest := "sha256:" + strings.Repeat("b", 64)
	value := marker{
		Schema: MarkerSchema, Repository: invalidRepository,
		GenerationDigest: generationDigest, ManifestDigest: manifestDigest,
		Manifest: callerpublicationid.ManifestName(generationDigest, manifestDigest),
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(directory, callerpublicationid.PublishingName)
	if err := os.WriteFile(markerPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	state := &reconcileTestStore{
		pointers: make(map[string]State), rejectInvalidRepositories: true,
	}
	if _, err := Reconcile(
		t.Context(), root, state,
		Expected{Extractors: []callerleaf.ExtractorIdentity{}}, NewRegistry(root),
	); err != nil {
		t.Fatalf("startup did not repair invalid marker repository: %v", err)
	}
	if _, err := os.Lstat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid repository marker remains: %v", err)
	}
}

func TestStartupReconcileRepairsIncompleteDeletionMarker(t *testing.T) {
	root := t.TempDir() + "/caller-leaves"
	repository := "github.com/acme/incomplete-deletion-marker"
	fixture := newPublicationFixture(t, root, repository, 'f')
	publication := publishFixture(t, fixture)
	references := publication.ArtifactRefs()
	directory := filepath.Join(
		root, callerpublicationid.RepositoryDirectory(repository),
	)
	deletingPath := filepath.Join(directory, callerpublicationid.DeletingName)
	if err := os.WriteFile(deletingPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	state := &reconcileTestStore{pointers: make(map[string]State)}
	report, err := Reconcile(
		t.Context(), root, state,
		Expected{Extractors: fixture.generation.Extractors}, NewRegistry(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReplacementsQueued != 0 || len(state.forced) != 0 {
		t.Fatalf("ineligible deletion residue queued work: report=%+v forced=%v", report, state.forced)
	}
	if _, err := os.Lstat(deletingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete deletion marker remains: %v", err)
	}
	for _, reference := range references {
		if _, err := os.Lstat(filepath.Join(
			root, filepath.FromSlash(reference.RelativePath()),
		)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ineligible deletion residue remains: %s: %v", reference.RelativePath(), err)
		}
	}
}

func TestStartupReconcileRepairsIncompletePublishingMarker(t *testing.T) {
	root := t.TempDir() + "/caller-leaves"
	repository := "github.com/acme/incomplete-publishing-marker"
	fixture := newPublicationFixture(t, root, repository, '0')
	publication := publishFixture(t, fixture)
	references := publication.ArtifactRefs()
	directory := filepath.Join(
		root, callerpublicationid.RepositoryDirectory(repository),
	)
	publishingPath := filepath.Join(directory, callerpublicationid.PublishingName)
	if err := os.WriteFile(publishingPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	state := &reconcileTestStore{pointers: make(map[string]State)}
	report, err := Reconcile(
		t.Context(), root, state,
		Expected{Extractors: fixture.generation.Extractors}, NewRegistry(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReplacementsQueued != 0 || len(state.forced) != 0 {
		t.Fatalf("ineligible publishing residue queued work: report=%+v forced=%v", report, state.forced)
	}
	if _, err := os.Lstat(publishingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete publishing marker remains: %v", err)
	}
	for _, reference := range references {
		if _, err := os.Lstat(filepath.Join(
			root, filepath.FromSlash(reference.RelativePath()),
		)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ineligible publishing residue remains: %s: %v", reference.RelativePath(), err)
		}
	}
}

func TestDiscoveryRefusesGlobalArtifactSetBeforeColdOpen(t *testing.T) {
	resetLifecycleHooks(t)
	root := t.TempDir() + "/caller-leaves"
	fixture := newPublicationFixture(
		t, root, "github.com/acme/discovery-cap", 'a',
	)
	publishFixture(t, fixture)
	coldOpens := 0
	testArtifactColdOpen = func(string) { coldOpens++ }
	if _, _, err := discoverWithAdmitted(
		t.Context(), root, nil, 1,
	); !errors.Is(err, callerleaf.ErrLimit) {
		t.Fatalf("Discover over global artifact cap = %v, want ErrLimit", err)
	}
	if coldOpens != 0 {
		t.Fatalf("discovery cold-opened %d leaves after exceeding its cap", coldOpens)
	}
}
