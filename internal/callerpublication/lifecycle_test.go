package callerpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/candidate"
)

type publicationFixture struct {
	root       string
	generation callerleaf.GenerationIdentity
	pair       callerleaf.PairIdentity
	receipt    callerleaf.Receipt
	leaf       *callerleaf.Publication
	manifest   Manifest
}

func newPublicationFixture(
	t *testing.T,
	root, repository string,
	digestByte byte,
) publicationFixture {
	t.Helper()
	digest := func(value byte) string {
		return "sha256:" + strings.Repeat(string([]byte{value}), 64)
	}
	generation, err := callerleaf.NewGenerationIdentity(callerleaf.GenerationIdentity{
		Repository:               repository,
		HeadCommit:               strings.Repeat(string([]byte{digestByte}), 40),
		DeclarationSetDigest:     digest('a'),
		CandidateManifestDigest:  digest('b'),
		CandidatePolicyDigest:    digest('c'),
		ResolverGenerationDigest: digest(digestByte),
		ResolverManifestDigest:   digest('d'),
		Extractors: []callerleaf.ExtractorIdentity{{
			Domain: "grpc-caller", Version: "1.5.0",
			LeafAdapterVersion: callerleaf.LeafAdapterV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := callerleaf.NewPairIdentity(
		generation, "grpc-caller", "1.5.0", callerleaf.LeafDescriptor{
			Name: "candidate-caller-000000.ndjson", Ordinal: 0,
			Prefix: "00", PrefixBits: 2, RecordCount: 1,
			DeclaredBytes: 256, ContentBytes: 128, ContentDigest: digest('e'),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := callerleaf.NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Discard() })
	if err := stage.Add(callerleaf.Record{
		Schema: callerleaf.RecordSchema, Kind: callerleaf.RecordAbstention,
		Path: "service/client.go", ObjectID: strings.Repeat("1", 40),
		SourceLane: candidate.SourceLaneBase, Reason: "unresolved_caller",
	}); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Discard() })
	leaf, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildManifest(generation, []PairReceipt{{
		Pair: pair, Receipt: leaf.Receipt(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return publicationFixture{
		root: root, generation: generation, pair: pair,
		receipt: leaf.Receipt(), leaf: leaf, manifest: manifest,
	}
}

func publishFixture(
	t *testing.T,
	fixture publicationFixture,
) *Publication {
	t.Helper()
	prepared, err := PrepareWithValidated(
		t.Context(), fixture.root, fixture.manifest,
		map[string]*callerleaf.Publication{fixture.pair.Digest: fixture.leaf},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Discard() })
	publication, err := prepared.Publish(
		t.Context(), func(context.Context, State) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	return publication
}

func resetLifecycleHooks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		testAfterStableOpen = nil
		testArtifactColdOpen = nil
		testAfterCurrentRepositoryOpen = nil
		testAfterCurrentLeafChecks = nil
		testAfterMarkerInstall = nil
		testAfterManifestInstall = nil
		testAfterStoreCommit = nil
	})
}

func TestWarmCurrentReusesOnePinnedRepositoryForLeafIdentity(t *testing.T) {
	resetLifecycleHooks(t)
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixture := newPublicationFixture(
		t, root, "github.com/acme/current-batch", '0',
	)
	publication := publishFixture(t, fixture)
	repositoryDirectory := filepath.Join(
		root,
		callerpublicationid.RepositoryDirectory(fixture.generation.Repository),
	)
	moved := repositoryDirectory + ".admitted"
	external := t.TempDir()
	swapped := false
	restored := false
	restore := func() {
		if !swapped || restored {
			return
		}
		if err := os.Remove(repositoryDirectory); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(moved, repositoryDirectory); err != nil {
			t.Fatal(err)
		}
		restored = true
	}
	testAfterCurrentRepositoryOpen = func(path string) {
		if path != repositoryDirectory {
			t.Fatalf("current repository = %q, want %q", path, repositoryDirectory)
		}
		if err := os.Rename(repositoryDirectory, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, repositoryDirectory); err != nil {
			t.Fatal(err)
		}
		swapped = true
	}
	testAfterCurrentLeafChecks = func(path string) {
		if path != repositoryDirectory {
			t.Fatalf("checked repository = %q, want %q", path, repositoryDirectory)
		}
		restore()
	}
	t.Cleanup(restore)
	current := publication.Current()
	if !restored {
		restore()
	}
	if !current || !swapped || !restored {
		t.Fatalf(
			"descriptor-batched Current = current:%v swapped:%v restored:%v",
			current, swapped, restored,
		)
	}
}

func TestCurrentResultPreservesOperationalFilesystemFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixture := newPublicationFixture(
		t, root, "github.com/acme/current-io", 'f',
	)
	publication := publishFixture(t, fixture)
	repositoryDirectory := filepath.Join(
		root,
		callerpublicationid.RepositoryDirectory(fixture.generation.Repository),
	)
	if err := os.Chmod(repositoryDirectory, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(repositoryDirectory, 0o700) })
	current, err := publication.CurrentResult()
	if current || !errors.Is(err, ErrPublicationIO) {
		t.Fatalf(
			"CurrentResult under unreadable authority = current:%v err:%v",
			current, err,
		)
	}
}

func TestCompleteLifecycleOrdersMarkerManifestStoreAndWarmIdentity(t *testing.T) {
	resetLifecycleHooks(t)
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixture := newPublicationFixture(t, root, "github.com/acme/service", '1')
	coldOpens := 0
	testArtifactColdOpen = func(string) { coldOpens++ }
	events := make([]string, 0, 3)
	testAfterMarkerInstall = func(state State) error {
		events = append(events, "marker")
		if marked, err := Publishing(root, state.Generation.Repository); err != nil || !marked {
			t.Fatalf("Publishing after marker = %v, %v", marked, err)
		}
		if _, err := os.Lstat(filepath.Join(
			root, callerpublicationid.RepositoryDirectory(state.Generation.Repository),
			state.Manifest,
		)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("manifest visible before rename: %v", err)
		}
		return nil
	}
	testAfterManifestInstall = func(State) error {
		events = append(events, "manifest")
		return nil
	}
	prepared, err := PrepareWithValidated(
		t.Context(), root, fixture.manifest,
		map[string]*callerleaf.Publication{fixture.pair.Digest: fixture.leaf},
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context(), func(_ context.Context, state State) error {
		events = append(events, "store")
		if marked, _ := Publishing(root, state.Generation.Repository); !marked {
			t.Fatal("store callback ran without marker")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "marker,manifest,store" {
		t.Fatalf("publication order = %v", events)
	}
	if coldOpens != 0 {
		t.Fatalf("reused prepared leaves were rehashed %d time(s)", coldOpens)
	}
	for range 3 {
		if !publication.Current() {
			t.Fatal("warm publication is not current")
		}
	}
	if coldOpens != 0 {
		t.Fatalf("warm Current hashed %d leaf artifact(s)", coldOpens)
	}
	if marked, err := Publishing(root, fixture.generation.Repository); err != nil || marked {
		t.Fatalf("Publishing after commit = %v, %v", marked, err)
	}
}

func TestCrashBoundariesRecoverOrAbandonMarkedPublication(t *testing.T) {
	t.Run("marker before manifest", func(t *testing.T) {
		resetLifecycleHooks(t)
		root := filepath.Join(t.TempDir(), "caller-leaves")
		fixture := newPublicationFixture(t, root, "github.com/acme/marker-crash", '2')
		prepared, err := Prepare(t.Context(), root, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		crash := errors.New("crash after marker")
		testAfterMarkerInstall = func(State) error { return crash }
		if _, err := prepared.Publish(
			t.Context(), func(context.Context, State) error { return nil },
		); !errors.Is(err, crash) {
			t.Fatalf("Publish = %v, want crash", err)
		}
		if marked, _ := Publishing(root, fixture.generation.Repository); !marked {
			t.Fatal("marker-before-manifest crash lost marker")
		}
		if err := AbandonPublishing(
			t.Context(), root, fixture.generation.Repository, nil,
		); err != nil {
			t.Fatal(err)
		}
		if marked, _ := Publishing(root, fixture.generation.Repository); marked {
			t.Fatal("abandoned marker remains")
		}
	})

	t.Run("manifest before store", func(t *testing.T) {
		resetLifecycleHooks(t)
		root := filepath.Join(t.TempDir(), "caller-leaves")
		fixture := newPublicationFixture(t, root, "github.com/acme/store-crash", '3')
		prepared, err := Prepare(t.Context(), root, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		commitFailure := errors.New("store unavailable")
		if _, err := prepared.Publish(
			t.Context(), func(context.Context, State) error { return commitFailure },
		); !errors.Is(err, commitFailure) {
			t.Fatalf("Publish = %v, want store failure", err)
		}
		if _, err := OpenMarked(
			t.Context(), root, fixture.generation.Repository,
		); err != nil {
			t.Fatalf("OpenMarked: %v", err)
		}
		commits := 0
		publication, err := RecoverPublishing(
			t.Context(), root, fixture.generation.Repository,
			fixture.generation.Extractors,
			func(context.Context, State) error { commits++; return nil },
		)
		if err != nil || commits != 1 || !publication.Current() {
			t.Fatalf("RecoverPublishing = current:%v commits:%d err:%v", publication != nil && publication.Current(), commits, err)
		}
	})
}

func TestColdOpenRejectsDescriptorSwapAndCrossRepositoryPlacement(t *testing.T) {
	resetLifecycleHooks(t)
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixture := newPublicationFixture(t, root, "github.com/acme/swap", '4')
	publication := publishFixture(t, fixture)
	state := publication.State()
	swapped := false
	testAfterStableOpen = func(filePath string) {
		if swapped || filepath.Base(filePath) != state.Manifest {
			return
		}
		swapped = true
		raw, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filePath, filePath+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Open(t.Context(), root, state); err == nil {
		t.Fatal("Open accepted descriptor-swapped manifest")
	}
	testAfterStableOpen = nil

	// Put the valid manifest bytes beneath another repository hash. Discovery
	// must reject decoded repository ownership rather than following it.
	other := "github.com/acme/other"
	otherDirectory := filepath.Join(
		root, callerpublicationid.RepositoryDirectory(other),
	)
	if err := os.Mkdir(otherDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(
		root, callerpublicationid.RepositoryDirectory(fixture.generation.Repository),
		state.Manifest+".old",
	)
	raw, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDirectory, state.Manifest), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, report, err := Discover(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if report.OmittedPublications == 0 {
		t.Fatalf("cross-repository manifest was not omitted: %+v", report)
	}
}

func TestRegistryDefersRetiredLeafRemovalUntilFinalRelease(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	repository := "github.com/acme/leases"
	firstFixture := newPublicationFixture(t, root, repository, '5')
	first := publishFixture(t, firstFixture)
	registry := NewRegistry(root)
	if err := registry.Observe(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	lease, err := registry.Acquire(t.Context(), first.State())
	if err != nil {
		t.Fatal(err)
	}
	firstPath, err := callerleaf.ArtifactPath(root, repository, firstFixture.receipt)
	if err != nil {
		t.Fatal(err)
	}
	secondFixture := newPublicationFixture(t, root, repository, '6')
	second := publishFixture(t, secondFixture)
	if err := registry.Observe(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(firstPath); err != nil {
		t.Fatalf("leased retired leaf removed early: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(
		root, callerpublicationid.RepositoryDirectory(repository),
		first.State().Manifest,
	)); err != nil {
		t.Fatalf("retired cleanup manifest was not retained with lease: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired leaf remains after final release: %v", err)
	}
	if !second.Current() {
		t.Fatal("replacement publication was disturbed by retirement")
	}
}

func TestRegistryBoundsCrossRepositoryColdAdmissions(t *testing.T) {
	resetLifecycleHooks(t)
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixtures := make([]publicationFixture, MaxConcurrentColdAdmissions+1)
	for index := range fixtures {
		fixtures[index] = newPublicationFixture(
			t, root, fmt.Sprintf("github.com/acme/cold-%d", index),
			byte('1'+index),
		)
		publishFixture(t, fixtures[index])
	}

	var active atomic.Int32
	var maximum atomic.Int32
	entered := make(chan struct{}, len(fixtures))
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	testArtifactColdOpen = func(string) {
		current := active.Add(1)
		for observed := maximum.Load(); current > observed; observed = maximum.Load() {
			if maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
	}

	registry := NewRegistry(root)
	t.Cleanup(func() { _ = registry.Close() })
	type result struct {
		lease *Lease
		err   error
	}
	results := make(chan result, len(fixtures))
	start := make(chan struct{})
	for _, fixture := range fixtures {
		state := fixture.manifest.State()
		go func() {
			<-start
			lease, err := registry.Acquire(t.Context(), state)
			results <- result{lease: lease, err: err}
		}()
	}
	close(start)
	for range MaxConcurrentColdAdmissions {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("cold admissions did not reach the configured concurrency")
		}
	}
	select {
	case <-entered:
		t.Fatal("cold admission exceeded its cross-repository concurrency bound")
	case <-time.After(100 * time.Millisecond):
	}
	releaseAll()
	for range fixtures {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if err := result.lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() != MaxConcurrentColdAdmissions {
		t.Fatalf("maximum cold admissions = %d", maximum.Load())
	}
}

func TestRegistryAcquireClassifiesPostOpenTransition(t *testing.T) {
	resetLifecycleHooks(t)
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixture := newPublicationFixture(
		t, root, "github.com/acme/cold-transition", '3',
	)
	publishFixture(t, fixture)

	var removeOnce sync.Once
	testAfterCurrentRepositoryOpen = func(path string) {
		removeOnce.Do(func() {
			if err := os.Remove(filepath.Join(path, fixture.receipt.Name)); err != nil {
				t.Errorf("remove caller leaf during admission: %v", err)
			}
		})
	}
	registry := NewRegistry(root)
	t.Cleanup(func() { _ = registry.Close() })
	lease, err := registry.Acquire(t.Context(), fixture.manifest.State())
	if lease != nil || !errors.Is(err, ErrRegistryConflict) {
		t.Fatalf("post-open transition = lease:%v err:%v, want registry conflict", lease, err)
	}
}

func TestRegistryLeaseScansAndRereadsExactRecords(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixture := newPublicationFixture(
		t, root, "github.com/acme/reader-lease", '4',
	)
	publication := publishFixture(t, fixture)
	registry := NewRegistry(root)
	if err := registry.Observe(t.Context(), publication); err != nil {
		t.Fatal(err)
	}
	lease, err := registry.Acquire(t.Context(), publication.State())
	if err != nil {
		t.Fatal(err)
	}
	var references []RecordReference
	if err := lease.ScanRecords(t.Context(), func(
		pair PairReceipt,
		reference RecordReference,
		record callerleaf.Record,
	) error {
		if pair != fixture.manifest.Pairs[0] ||
			reference.PairDigest != fixture.pair.Digest ||
			reference.ArtifactName != fixture.receipt.Name ||
			record.Path != "service/client.go" {
			t.Fatalf("unexpected leased record: %+v %+v %+v", pair, reference, record)
		}
		references = append(references, reference)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 {
		t.Fatalf("record references = %+v", references)
	}
	pair, record, err := lease.ReadRecord(t.Context(), references[0])
	if err != nil || pair != fixture.manifest.Pairs[0] ||
		record.Path != "service/client.go" {
		t.Fatalf("ReadRecord = %+v %+v, %v", pair, record, err)
	}
	wrongPair := references[0]
	wrongPair.PairDigest = "sha256:" + strings.Repeat("0", 64)
	if _, _, err := lease.ReadRecord(t.Context(), wrongPair); !errors.Is(
		err, callerleaf.ErrInvalidArtifact,
	) {
		t.Fatalf("ReadRecord cross-pair error = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lease.ScanRecords(t.Context(), func(
		PairReceipt, RecordReference, callerleaf.Record,
	) error {
		return nil
	}); err == nil {
		t.Fatal("ScanRecords accepted a released lease")
	}
	if _, _, err := lease.ReadRecord(t.Context(), references[0]); !errors.Is(
		err, callerleaf.ErrInvalidArtifact,
	) {
		t.Fatalf("ReadRecord released-lease error = %v", err)
	}
}

func TestRegistryRefreshesSameStateDescriptorWithoutRemovingBytes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixture := newPublicationFixture(t, root, "github.com/acme/same-state", '7')
	publication := publishFixture(t, fixture)
	registry := NewRegistry(root)
	if err := registry.Observe(t.Context(), publication); err != nil {
		t.Fatal(err)
	}
	oldLease, err := registry.Acquire(t.Context(), publication.State())
	if err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(
		root,
		callerpublicationid.RepositoryDirectory(fixture.generation.Repository),
		publication.State().Manifest,
	)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(manifestPath, manifestPath+".replaced"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if oldLease.Publication().Current() {
		t.Fatal("old lease descriptor remained current after same-state replacement")
	}

	newLease, err := registry.Acquire(t.Context(), publication.State())
	if err != nil {
		t.Fatalf("Acquire same-state replacement: %v", err)
	}
	if newLease.Publication() == oldLease.Publication() ||
		!newLease.Publication().Current() {
		t.Fatal("registry did not install the refreshed descriptor snapshot")
	}
	leafPath, err := callerleaf.ArtifactPath(
		root, fixture.generation.Repository, fixture.receipt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(leafPath); err != nil {
		t.Fatalf("same-state refresh removed shared leaf bytes: %v", err)
	}
	if err := oldLease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := newLease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(leafPath); err != nil {
		t.Fatalf("current same-state leaf removed after releases: %v", err)
	}
}

func TestRegistryColdAcquireCannotReplaceAnotherCurrentGeneration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	repository := "github.com/acme/registry-conflict"
	first := publishFixture(t, newPublicationFixture(t, root, repository, '9'))
	registry := NewRegistry(root)
	if err := registry.Observe(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second := publishFixture(t, newPublicationFixture(t, root, repository, 'a'))
	if _, err := registry.Acquire(
		t.Context(), second.State(),
	); !errors.Is(err, ErrRegistryConflict) {
		t.Fatalf("Acquire different generation = %v, want ErrRegistryConflict", err)
	}
	lease, err := registry.Acquire(t.Context(), first.State())
	if err != nil {
		t.Fatalf("Acquire retained current generation: %v", err)
	}
	if !lease.Publication().Current() {
		t.Fatal("conflicting cold acquisition disturbed current generation")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryRecurringGenerationKeepsCurrentLeavesOnOldRelease(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	repository := "github.com/acme/recurring-generation"
	firstFixture := newPublicationFixture(t, root, repository, 'b')
	first := publishFixture(t, firstFixture)
	registry := NewRegistry(root)
	if err := registry.Observe(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	oldLease, err := registry.Acquire(t.Context(), first.State())
	if err != nil {
		t.Fatal(err)
	}
	second := publishFixture(t, newPublicationFixture(t, root, repository, 'c'))
	if err := registry.Observe(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	republished := publishFixture(t, firstFixture)
	if err := registry.Observe(t.Context(), republished); err != nil {
		t.Fatal(err)
	}
	if err := oldLease.Release(); err != nil {
		t.Fatal(err)
	}
	leafPath, err := callerleaf.ArtifactPath(
		root, repository, firstFixture.receipt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(leafPath); err != nil {
		t.Fatalf("old recurring lease removed current leaf: %v", err)
	}
	if !republished.Current() {
		t.Fatal("old recurring lease disturbed republished generation")
	}
}

func TestRegistryRepositoryRemovalWaitsForLeasesAndHonorsRecreation(t *testing.T) {
	t.Run("final release removes directory", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "caller-leaves")
		repository := "github.com/acme/delete-leased"
		publication := publishFixture(
			t, newPublicationFixture(t, root, repository, 'd'),
		)
		registry := NewRegistry(root)
		if err := registry.Observe(t.Context(), publication); err != nil {
			t.Fatal(err)
		}
		lease, err := registry.Acquire(t.Context(), publication.State())
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.RemoveRepository(t.Context(), repository); err != nil {
			t.Fatal(err)
		}
		directory := filepath.Join(
			root, callerpublicationid.RepositoryDirectory(repository),
		)
		if _, err := os.Lstat(directory); err != nil {
			t.Fatalf("leased repository directory removed early: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(
			directory, callerpublicationid.DeletingName,
		)); err != nil {
			t.Fatalf("leased deletion tombstone is missing: %v", err)
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("repository directory remains after final release: %v", err)
		}
	})

	t.Run("new authority cancels deferred removal", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "caller-leaves")
		repository := "github.com/acme/delete-recreated"
		first := publishFixture(t, newPublicationFixture(t, root, repository, 'e'))
		registry := NewRegistry(root)
		if err := registry.Observe(t.Context(), first); err != nil {
			t.Fatal(err)
		}
		lease, err := registry.Acquire(t.Context(), first.State())
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.RemoveRepository(t.Context(), repository); err != nil {
			t.Fatal(err)
		}
		if err := registry.ActivateRepository(t.Context(), repository); err != nil {
			t.Fatal(err)
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
		second := publishFixture(t, newPublicationFixture(t, root, repository, 'f'))
		if err := registry.Observe(t.Context(), second); err != nil {
			t.Fatal(err)
		}
		if !second.Current() {
			t.Fatal("old lease release removed recreated repository authority")
		}
	})
}

func TestDeletionMarkerReconciliationCompletesCrashOrPreservesRecreation(
	t *testing.T,
) {
	for _, test := range []struct {
		name     string
		eligible bool
	}{
		{name: "deleted repository", eligible: false},
		{name: "same-name recreation", eligible: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "caller-leaves")
			repository := "github.com/acme/deletion-restart"
			publication := publishFixture(
				t, newPublicationFixture(t, root, repository, 'a'),
			)
			registry := NewRegistry(root)
			if err := registry.Observe(t.Context(), publication); err != nil {
				t.Fatal(err)
			}
			lease, err := registry.Acquire(t.Context(), publication.State())
			if err != nil {
				t.Fatal(err)
			}
			if err := registry.RemoveRepository(t.Context(), repository); err != nil {
				t.Fatal(err)
			}
			// Model a process exit: the in-memory lease vanishes without running
			// Release, while the canonical tombstone survives on disk.
			lease.registry = nil
			if err := ReconcileDeletionMarkers(
				t.Context(), root,
				func(context.Context, string) (bool, error) {
					return test.eligible, nil
				},
			); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(
				root, callerpublicationid.RepositoryDirectory(repository),
			)
			_, statErr := os.Lstat(directory)
			if test.eligible {
				if statErr != nil || !publication.Current() {
					t.Fatalf("recreated repository was not preserved: %v", statErr)
				}
				if _, err := os.Lstat(filepath.Join(
					directory, callerpublicationid.DeletingName,
				)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("recreated repository tombstone remains: %v", err)
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("deleted repository remains after restart: %v", statErr)
			}
		})
	}
}

func TestDeletionMarkerReconciliationDropsInvalidMarkerWithoutDeletingLiveBytes(
	t *testing.T,
) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	repository := "github.com/acme/deletion-invalid-recreated"
	publication := publishFixture(
		t, newPublicationFixture(t, root, repository, 'b'),
	)
	directory := filepath.Join(
		root, callerpublicationid.RepositoryDirectory(repository),
	)
	markerPath := filepath.Join(directory, callerpublicationid.DeletingName)
	if err := os.WriteFile(markerPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eligibilityCalls := 0
	if err := ReconcileDeletionMarkers(
		t.Context(), root,
		func(context.Context, string) (bool, error) {
			eligibilityCalls++
			return true, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if eligibilityCalls != 0 {
		t.Fatalf("invalid marker payload selected repository %d time(s)", eligibilityCalls)
	}
	if _, err := os.Lstat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid deletion marker remains: %v", err)
	}
	if !publication.Current() {
		t.Fatal("invalid deletion marker repair removed live publication bytes")
	}
}

func TestRegistryEvictedAuthorityRetiresLeavesBeforeManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	registry := NewRegistry(root)
	publications := make([]*Publication, 0, MaxRegistryPublications+1)
	for index := range MaxRegistryPublications + 1 {
		publication := publishFixture(t, newPublicationFixture(
			t, root, fmt.Sprintf("github.com/acme/evict-%02d", index), 'b',
		))
		publications = append(publications, publication)
		lease, err := registry.Acquire(t.Context(), publication.State())
		if err != nil {
			t.Fatal(err)
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}

	var evicted *Publication
	registry.mu.Lock()
	if got := len(registry.authorities); got != len(publications) {
		registry.mu.Unlock()
		t.Fatalf("compact authorities = %d, want %d", got, len(publications))
	}
	if got := len(registry.slots); got > MaxRegistryPublications {
		registry.mu.Unlock()
		t.Fatalf("transition slots = %d, want at most %d", got, MaxRegistryPublications)
	}
	for _, publication := range publications {
		repository := publication.state.Generation.Repository
		slot := registry.slots[repository]
		authority, present := registry.authorityLocked(repository)
		if slot == nil && present &&
			authority.manifest == publication.state.Manifest {
			evicted = publication
			break
		}
	}
	registry.mu.Unlock()
	if evicted == nil {
		t.Fatal("cache admission did not evict an authoritative publication")
	}
	refs := evicted.ArtifactRefs()
	if err := registry.Retire(
		t.Context(), evicted.state.Generation.Repository,
	); err != nil {
		t.Fatal(err)
	}
	for _, reference := range refs {
		if _, err := os.Lstat(filepath.Join(
			root, filepath.FromSlash(reference.RelativePath()),
		)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("evicted retirement left %q: %v", reference.RelativePath(), err)
		}
	}
}

func TestRegistryBoundsCompactAuthorityTokens(t *testing.T) {
	registry := NewRegistry(t.TempDir())
	authority := func(value byte) registryAuthority {
		return registryAuthority{manifest: callerpublicationid.ManifestName(
			"sha256:"+strings.Repeat(string(value), 64),
			"sha256:"+strings.Repeat(string(value), 64),
		)}
	}

	registry.mu.Lock()
	if !registry.setAuthorityWithinLimitLocked(
		"github.com/acme/one", authority('1'), 2,
	) || !registry.setAuthorityWithinLimitLocked(
		"github.com/acme/two", authority('2'), 2,
	) {
		registry.mu.Unlock()
		t.Fatal("authority token below the bound was refused")
	}
	if registry.setAuthorityWithinLimitLocked(
		"github.com/acme/three", authority('3'), 2,
	) {
		registry.mu.Unlock()
		t.Fatal("authority token above the bound was admitted")
	}
	if !registry.setAuthorityWithinLimitLocked(
		"github.com/acme/one", authority('4'), 2,
	) {
		registry.mu.Unlock()
		t.Fatal("bounded authority replacement was refused")
	}
	got := len(registry.authorities)
	registry.mu.Unlock()
	if got != 2 {
		t.Fatalf("authority tokens = %d, want 2", got)
	}
	if MaxRegistryAuthorityTokens !=
		callerpublicationid.InstallationPublicationRepositories {
		t.Fatalf(
			"authority token bound = %d, want installation bound %d",
			MaxRegistryAuthorityTokens,
			callerpublicationid.InstallationPublicationRepositories,
		)
	}
	wantIdentityBytes := MaxRegistryAuthorityTokens * (len(authorityKey("github.com/acme/identity-bound")) +
		len(authority('5').manifest))
	if MaxRegistryAuthorityIdentityBytes != wantIdentityBytes ||
		MaxRegistryAuthorityIdentityBytes != 15<<20 {
		t.Fatalf(
			"authority identity bytes = %d, want %d (15 MiB)",
			MaxRegistryAuthorityIdentityBytes, wantIdentityBytes,
		)
	}
}

func TestRegistryBoundsParsedPublicationsAndRetriesAdmission(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	registry := NewRegistry(root)
	publications := make([]*Publication, 0, MaxRegistryPublications+1)
	leases := make([]*Lease, 0, MaxRegistryPublications)
	for index := range MaxRegistryPublications + 1 {
		publication := publishFixture(t, newPublicationFixture(
			t, root, fmt.Sprintf("github.com/acme/cache-%02d", index), '1',
		))
		publications = append(publications, publication)
		if index == MaxRegistryPublications {
			continue
		}
		if err := registry.Observe(t.Context(), publication); err != nil {
			t.Fatal(err)
		}
		lease, err := registry.Acquire(t.Context(), publication.State())
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}

	if err := registry.Observe(t.Context(), publications[MaxRegistryPublications]); !errors.Is(err, callerleaf.ErrCapacity) {
		t.Fatalf("Observe over pinned cache = %v, want ErrCapacity", err)
	}
	registry.mu.Lock()
	if got := len(registry.entries); got != MaxRegistryPublications {
		registry.mu.Unlock()
		t.Fatalf("cached publications = %d, want %d", got, MaxRegistryPublications)
	}
	if got := registry.pairRefs; got != MaxRegistryPublications {
		registry.mu.Unlock()
		t.Fatalf("cached pair references = %d, want %d", got, MaxRegistryPublications)
	}
	registry.mu.Unlock()

	if err := leases[0].Release(); err != nil {
		t.Fatal(err)
	}
	if err := registry.Observe(t.Context(), publications[MaxRegistryPublications]); err != nil {
		t.Fatalf("Observe after unpinning one cache entry: %v", err)
	}
	lease, err := registry.Acquire(
		t.Context(), publications[MaxRegistryPublications].State(),
	)
	if err != nil {
		t.Fatalf("Acquire newly admitted publication: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	for _, held := range leases[1:] {
		if err := held.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublicationResidueCleanupIsResumablyBounded(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixture := newPublicationFixture(t, root, "github.com/acme/residue-batch", '2')
	publication := publishFixture(t, fixture)
	directory := filepath.Join(
		root, callerpublicationid.RepositoryDirectory(fixture.generation.Repository),
	)
	for index := range MaxTransitionCleanupManifests + 1 {
		name := callerpublicationid.ManifestName(
			"sha256:"+strings.Repeat("f", 64),
			fmt.Sprintf("sha256:%064x", index+1),
		)
		if err := os.WriteFile(filepath.Join(directory, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	countResidue := func() int {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, entry := range entries {
			if entry.Name() != publication.state.Manifest &&
				callerpublicationid.ValidManifestName(entry.Name()) {
				count++
			}
		}
		return count
	}
	if err := cleanupPublicationResidue(
		t.Context(), root, publication.State(),
		map[string]struct{}{fixture.receipt.Name: {}}, nil,
	); err != nil {
		t.Fatal(err)
	}
	if got := countResidue(); got != 1 {
		t.Fatalf("residue after one bounded batch = %d, want 1", got)
	}
	if err := cleanupPublicationResidue(
		t.Context(), root, publication.State(),
		map[string]struct{}{fixture.receipt.Name: {}}, nil,
	); err != nil {
		t.Fatal(err)
	}
	if got := countResidue(); got != 0 {
		t.Fatalf("residue after resumed batch = %d, want 0", got)
	}
	if !publication.Current() {
		t.Fatal("residue cleanup disturbed current publication")
	}
}

func TestRegistryClosePreservesAuthoritativePublication(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	fixture := newPublicationFixture(t, root, "github.com/acme/close", '8')
	publication := publishFixture(t, fixture)
	registry := NewRegistry(root)
	if err := registry.Observe(t.Context(), publication); err != nil {
		t.Fatal(err)
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Acquire(t.Context(), publication.State()); err == nil {
		t.Fatal("closed registry admitted a new lease")
	}
	manifestPath := filepath.Join(
		root,
		callerpublicationid.RepositoryDirectory(fixture.generation.Repository),
		publication.State().Manifest,
	)
	leafPath, err := callerleaf.ArtifactPath(
		root, fixture.generation.Repository, fixture.receipt,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, filePath := range []string{manifestPath, leafPath} {
		if _, err := os.Lstat(filePath); err != nil {
			t.Fatalf("Close removed authoritative %s: %v", filepath.Base(filePath), err)
		}
	}
}
