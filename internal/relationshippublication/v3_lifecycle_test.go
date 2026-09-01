package relationshippublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/observationpublication"
)

type lifecyclePinStoreV3 struct {
	pinnedCatalogs   int
	pinnedRuns       []string
	unpinnedRuns     []string
	unpinnedCatalogs []lifecycleCatalogIdentityV3
	failRun          string
}

type lifecycleCatalogIdentityV3 struct {
	repository, generation, root, catalogRoot, stateSummary string
	catalogRevision, stateRevision                          uint64
}

type pinnedLifecycleV3 struct{ released string }

func (pins *pinnedLifecycleV3) Pinned(_ string, generation string) bool {
	return generation != pins.released
}
func (pins *pinnedLifecycleV3) BeginRetire(_ string, generation string) (func(), bool) {
	if generation == pins.released {
		return func() {}, true
	}
	return nil, false
}

func (store *lifecyclePinStoreV3) PinRelationshipPublicationV3(
	_ context.Context,
	_, _, _, _ string,
	_, _ uint64,
	_ string,
) error {
	store.pinnedCatalogs++
	return nil
}

func (store *lifecyclePinStoreV3) UnpinRelationshipPublicationV3(
	_ context.Context,
	repository, generation, root, catalogRoot string,
	catalogRevision, stateRevision uint64,
	stateSummary string,
) error {
	store.unpinnedCatalogs = append(store.unpinnedCatalogs, lifecycleCatalogIdentityV3{
		repository: repository, generation: generation, root: root, catalogRoot: catalogRoot,
		catalogRevision: catalogRevision, stateRevision: stateRevision,
		stateSummary: stateSummary,
	})
	return nil
}

func (store *lifecyclePinStoreV3) PinPartitionedExtractionRun(
	_ context.Context, runID, owner string,
) error {
	store.pinnedRuns = append(store.pinnedRuns, runID+"\x00"+owner)
	return nil
}

func (store *lifecyclePinStoreV3) UnpinPartitionedExtractionRun(
	_ context.Context, runID, owner string,
) error {
	store.unpinnedRuns = append(store.unpinnedRuns, runID+"\x00"+owner)
	if runID == store.failRun {
		return errors.New("injected extraction unpin failure")
	}
	return nil
}

func TestBoundedLifecycleDirectoryStopsAtOneOverAndSorts(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := boundedLifecycleDirectory(directory, 3)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	if !reflect.DeepEqual(names, []string{"alpha", "bravo", "charlie"}) {
		t.Fatalf("bounded directory order = %q", names)
	}
	if err := os.WriteFile(filepath.Join(directory, "delta"), []byte("delta"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := boundedLifecycleDirectory(directory, 3); !errors.Is(err, ErrLimit) {
		t.Fatalf("one-over bounded directory = %v", err)
	}
}

func TestLifecycleV3PreservesCurrentPriorAndCacheLease(t *testing.T) {
	dataDir := t.TempDir()
	relationshipRoot := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(relationshipRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/v3-lifecycle"
	publishPins := &lifecyclePinStoreV3{}
	var roots []RootV3
	for index, suffix := range []string{"1", "2", "3", "4"} {
		root := publishLifecycleGenerationV3(
			t, relationshipRoot, repository, suffix, "run-"+suffix, publishPins,
		)
		roots = append(roots, root)
		directory, err := GenerationPathV3(
			relationshipRoot, repository, root.GenerationDigest,
		)
		if err != nil {
			t.Fatal(err)
		}
		modified := time.Unix(int64(index+1), 0)
		if err := os.Chtimes(directory, modified, modified); err != nil {
			t.Fatal(err)
		}
	}

	cache := &CacheV3{}
	lease, err := cache.AcquireGeneration(
		t.Context(), relationshipRoot, repository,
		roots[1].GenerationDigest, roots[1].Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := SweepLifecycleV3(
		t.Context(), dataDir, time.Now().UTC(), "", cache, 1,
	)
	if err != nil || result.ReleasedPinOwner != "relationship:"+roots[0].GenerationDigest ||
		result.ReleasedRootV3 == nil || result.ReleasedRootV3.Digest != roots[0].Digest ||
		result.Deleted != 0 {
		t.Fatalf("first v3 retirement = %+v, %v", result, err)
	}
	if _, err := cache.AcquireGeneration(
		t.Context(), relationshipRoot, repository,
		roots[0].GenerationDigest, roots[0].Digest,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renamed collecting generation remained readable: %v", err)
	}
	retry, err := SweepLifecycleV3(
		t.Context(), dataDir, time.Now().UTC(), result.Cursor, cache, 1,
	)
	if err != nil || retry.ReleasedPinOwner != result.ReleasedPinOwner ||
		retry.ReleasedRootV3 == nil || retry.ReleasedRootV3.Digest != roots[0].Digest {
		t.Fatalf("unconfirmed v3 retirement retry = %+v, %v", retry, err)
	}
	if err := ConfirmLifecycleUnpinV3(
		t.Context(), dataDir, result.Cursor, result.ReleasedPinOwner,
	); err != nil {
		t.Fatal(err)
	}
	for turn := 0; turn < 3; turn++ {
		result, err = SweepLifecycleV3(
			t.Context(), dataDir, time.Now().UTC(), result.Cursor, cache, 1,
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.Deleted > 1 {
			t.Fatalf("v3 collection exceeded delete bound: %+v", result)
		}
		if _, statErr := os.Lstat(mustGenerationPathV3(
			t, relationshipRoot, repository, roots[0].GenerationDigest,
		)); errors.Is(statErr, os.ErrNotExist) {
			break
		}
	}
	if _, err := os.Lstat(mustGenerationPathV3(
		t, relationshipRoot, repository, roots[0].GenerationDigest,
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired v3 generation remains: %v", err)
	}

	// B remains non-collecting while its historical lease is held. D is current
	// and C is the one prior floor, so no other generation may be selected.
	result, err = SweepLifecycleV3(
		t.Context(), dataDir, time.Now().UTC(), result.Cursor, cache, 1,
	)
	if err != nil || result.ReleasedPinOwner != "" {
		t.Fatalf("leased v3 generation retired = %+v, %v", result, err)
	}
	lease.Release()
	result, err = SweepLifecycleV3(
		t.Context(), dataDir, time.Now().UTC(), result.Cursor, cache, 1,
	)
	if err != nil || result.ReleasedPinOwner != "relationship:"+roots[1].GenerationDigest {
		t.Fatalf("released historical generation retirement = %+v, %v", result, err)
	}
	for _, root := range roots[2:] {
		if _, err := os.Lstat(mustGenerationPathV3(
			t, relationshipRoot, repository, root.GenerationDigest,
		)); err != nil {
			t.Fatalf("current/prior v3 floor missing: %v", err)
		}
	}
}

func TestUnpinLifecycleV3UsesExactRunsThenCatalogIdentity(t *testing.T) {
	dataDir := t.TempDir()
	relationshipRoot := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(relationshipRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	publishPins := &lifecyclePinStoreV3{}
	root := publishLifecycleGenerationV3(
		t, relationshipRoot, "example.com/acme/v3-unpin", "5", "run-five", publishPins,
	)
	store := &lifecyclePinStoreV3{}
	if err := UnpinLifecycleV3(t.Context(), store, root); err != nil {
		t.Fatal(err)
	}
	owner := "relationship:" + root.GenerationDigest
	if !reflect.DeepEqual(store.unpinnedRuns, []string{"run-five\x00" + owner}) {
		t.Fatalf("exact extraction unpins = %q", store.unpinnedRuns)
	}
	want := lifecycleCatalogIdentityV3{
		repository: root.Authority.Repository, generation: root.GenerationDigest,
		root: root.Digest, catalogRoot: root.Authority.CatalogRootDigest,
		catalogRevision: root.Authority.CatalogControlRevision,
		stateRevision:   root.Authority.ServiceStateControlRevision,
		stateSummary:    root.Authority.ServiceStateSummaryDigest,
	}
	if !reflect.DeepEqual(store.unpinnedCatalogs, []lifecycleCatalogIdentityV3{want}) {
		t.Fatalf("exact catalog unpin = %+v, want %+v", store.unpinnedCatalogs, want)
	}

	store = &lifecyclePinStoreV3{failRun: "run-five"}
	if err := UnpinLifecycleV3(t.Context(), store, root); err == nil ||
		len(store.unpinnedCatalogs) != 0 {
		t.Fatalf("catalog released after extraction failure: %+v, %v", store, err)
	}
}

func TestLifecycleV3StageDrainIsFlatAndDeleteBounded(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/v3-stage"
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(base, ".stage-crash")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.json", "b.json", "c.json"} {
		if err := os.WriteFile(filepath.Join(stage, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var deletedByTurn []int
	for turn := 0; turn < 5; turn++ {
		result, sweepErr := SweepLifecycleV3(
			t.Context(), dataDir, time.Now().UTC(), "", &CacheV3{}, 1,
		)
		if sweepErr != nil || result.Deleted > 2 {
			t.Fatalf("bounded v3 stage turn %d = %+v, %v", turn, result, sweepErr)
		}
		deletedByTurn = append(deletedByTurn, result.Deleted)
		entries, readErr := os.ReadDir(stage)
		if errors.Is(readErr, os.ErrNotExist) {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) < 3-turn-1 {
			t.Fatalf("turn %d removed more than one flat member: %d remain", turn, len(entries))
		}
	}
	if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("v3 stage remains: %v", err)
	}
	// Like the legacy collector, each flat-file turn consumes one delete;
	// the final metadata-only turn counts both the stage and empty repository.
	if !reflect.DeepEqual(deletedByTurn, []int{1, 1, 1, 2}) {
		t.Fatalf("v3 stage delete accounting = %v", deletedByTurn)
	}
}

func TestLifecycleV3DefersFinalMarkerOwnedStageToRecovery(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	repository := "example.com/acme/v3-marker-owned-lifecycle"
	prepared := preparedRelationshipV3Test(t, root, repository)
	pointer, err := newPointerV3(prepared.rootValue)
	if err != nil {
		t.Fatal(err)
	}
	_, markerRaw, _, err := publicationControlsV3(
		pointer, filepath.Base(prepared.directory),
	)
	if err != nil {
		t.Fatal(err)
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(base, "publishing.json"), markerRaw); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(base, "publishing.json.tmp")
	if err := os.WriteFile(temporary, []byte("interrupted-replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared.closed = true
	orphanStage := filepath.Join(base, ".stage-orphan")
	if err := os.Mkdir(orphanStage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanStage, "orphan"), []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphanComponent := filepath.Join(
		dataDir, "relationship-resolver-namespaces", "resolver-namespaces",
		repositoryHash(repository), "generation-"+strings.Repeat("e", 64),
	)
	if err := os.MkdirAll(orphanComponent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanComponent, "root.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SweepLifecycleV3(
		t.Context(), dataDir, time.Now().UTC(), "", &CacheV3{}, 8,
	)
	if err != nil || result.Scanned != 1 || !result.More || result.Deleted != 0 ||
		result.ReleasedPinOwner != "" {
		t.Fatalf("marker-owned lifecycle backlog = %+v, %v", result, err)
	}
	for _, path := range []string{prepared.directory, temporary, orphanStage, orphanComponent} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("marker lifecycle mutated %q: %v", path, err)
		}
	}

	recovered, err := RecoverV3(t.Context(), root, repository, &testPublishPinsV3{})
	if err != nil || !recovered {
		t.Fatalf("recover marker-owned stage = %t, %v", recovered, err)
	}
	if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered marker retained publishing temporary: %v", err)
	}
	result, err = SweepLifecycleV3(
		t.Context(), dataDir, time.Now().UTC(), result.Cursor, &CacheV3{}, 8,
	)
	if err != nil || result.Scanned != 1 || result.Deleted != 2 {
		t.Fatalf("post-recovery orphan stage drain = %+v, %v", result, err)
	}
	if _, err := os.Lstat(orphanStage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-recovery orphan stage remains: %v", err)
	}
}

func TestLegacyComponentSweepRetainsV3OnlyReference(t *testing.T) {
	dataDir := t.TempDir()
	relationshipRoot := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(relationshipRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/v3-component-union"
	legacyAuthority := testAuthority(t, repository)
	legacyAccumulator := &buildAccumulator{
		repository: map[int][]Projection{}, services: map[string]*serviceAccumulator{},
		seen: map[string]struct{}{}, serviceRefLimit: MaxServiceReferences,
		totalRefLimit: MaxTotalServiceReferences, residentLimit: MaxResidentChargeBytes,
	}
	legacyStage, err := writePublicationStage(
		t.Context(), relationshipRoot, legacyAuthority,
		mustDigest(t, legacyAuthority), legacyAccumulator,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyStage.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	publishPins := &lifecyclePinStoreV3{}
	v3Root := publishLifecycleGenerationV3(
		t, relationshipRoot, repository, "5", "run-component", publishPins,
	)

	repositoryHashValue := repositoryHash(repository)
	referenced := []string{
		filepath.Join(
			dataDir, "relationship-resolver-namespaces", "resolver-namespaces",
			repositoryHashValue, "generation-"+strings.TrimPrefix(
				v3Root.Authority.ResolverGenerationDigest, "sha256:",
			),
		),
		filepath.Join(
			dataDir, "relationship-rpc-postings", "rpc-caller-postings",
			repositoryHashValue,
			strings.TrimPrefix(v3Root.Authority.RPCGenerationDigest, "sha256:"),
		),
		filepath.Join(
			dataDir, "relationship-kafka-postings", "kafka-topic-postings",
			repositoryHashValue,
			strings.TrimPrefix(v3Root.Authority.KafkaGenerationDigest, "sha256:"),
		),
	}
	orphan := filepath.Join(
		dataDir, "relationship-kafka-postings", "kafka-topic-postings",
		repositoryHashValue, strings.Repeat("e", 64),
	)
	for _, directory := range append(append([]string(nil), referenced...), orphan) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "root.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := SweepLifecycle(
		t.Context(), dataDir, time.Now().UTC(), "", &Cache{}, 8,
	)
	if err != nil || result.Deleted == 0 {
		t.Fatalf("legacy union component sweep = %+v, %v", result, err)
	}
	for _, directory := range referenced {
		if _, err := os.Lstat(directory); err != nil {
			t.Fatalf("v3-referenced shared component removed: %v", err)
		}
	}
	if _, err := os.Lstat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unreferenced shared component remains: %v", err)
	}
}

func TestLegacyComponentSweepDefersForMarkerOwnedV3Stage(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/v3-marker-component-union"
	legacyAuthority := testAuthority(t, repository)
	legacyStage, err := writePublicationStage(
		t.Context(), root, legacyAuthority, mustDigest(t, legacyAuthority),
		&buildAccumulator{
			repository: map[int][]Projection{}, services: map[string]*serviceAccumulator{},
			seen: map[string]struct{}{}, serviceRefLimit: MaxServiceReferences,
			totalRefLimit: MaxTotalServiceReferences, residentLimit: MaxResidentChargeBytes,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyStage.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	prepared := preparedRelationshipV3Test(t, root, repository)
	pointer, err := newPointerV3(prepared.rootValue)
	if err != nil {
		t.Fatal(err)
	}
	_, markerRaw, _, err := publicationControlsV3(
		pointer, filepath.Base(prepared.directory),
	)
	if err != nil {
		t.Fatal(err)
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(base, "publishing.json"), markerRaw); err != nil {
		t.Fatal(err)
	}
	prepared.closed = true
	if legacyAuthority.ResolverGenerationDigest ==
		prepared.rootValue.Authority.ResolverGenerationDigest {
		t.Fatal("marker-stage resolver generation is not v3-only")
	}
	stageOnlyComponent := filepath.Join(
		dataDir, "relationship-resolver-namespaces", "resolver-namespaces",
		repositoryHash(repository), "generation-"+strings.TrimPrefix(
			prepared.rootValue.Authority.ResolverGenerationDigest, "sha256:",
		),
	)
	if err := os.MkdirAll(stageOnlyComponent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageOnlyComponent, "root.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SweepLifecycle(
		t.Context(), dataDir, time.Now().UTC(), "", &Cache{}, 8,
	)
	if err != nil || result.Deleted != 0 || !result.More {
		t.Fatalf("legacy marker-stage component defer = %+v, %v", result, err)
	}
	if _, err := os.Lstat(stageOnlyComponent); err != nil {
		t.Fatalf("legacy sweep removed marker-stage-only component: %v", err)
	}
}

func TestLifecycleV3DefersComponentGCAboveGenerationBound(t *testing.T) {
	dataDir := t.TempDir()
	relationshipRoot := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(relationshipRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/v3-bounded-component-union"
	publishPins := &lifecyclePinStoreV3{}
	roots := make([]RootV3, 0, MaxRepositoryGenerations+1)
	for index := 0; index < MaxRepositoryGenerations+1; index++ {
		root := publishLifecycleGenerationV3(
			t, relationshipRoot, repository, "5",
			fmt.Sprintf("bounded-run-%03d", index), publishPins,
		)
		roots = append(roots, root)
		directory := mustGenerationPathV3(
			t, relationshipRoot, repository, root.GenerationDigest,
		)
		modified := time.Unix(int64(index+1), 0)
		if err := os.Chtimes(directory, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	repositoryHashValue := repositoryHash(repository)
	orphan := filepath.Join(
		dataDir, "relationship-resolver-namespaces", "resolver-namespaces",
		repositoryHashValue, "generation-"+strings.Repeat("e", 64),
	)
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "root.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins := &pinnedLifecycleV3{}
	result, err := SweepLifecycleV3(
		t.Context(), dataDir, time.Now().UTC(), "", pins, 8,
	)
	if err != nil || !result.More || result.Scanned > MaxRepositoryGenerations ||
		result.Deleted != 0 ||
		result.ReleasedPinOwner != "" {
		t.Fatalf("over-bound all-pinned v3 lifecycle = %+v, %v", result, err)
	}
	if _, err := os.Lstat(orphan); err != nil {
		t.Fatalf("component GC ran before bounded v3 root union: %v", err)
	}
	pins.released = roots[0].GenerationDigest
	result, err = SweepLifecycleV3(
		t.Context(), dataDir, time.Now().UTC(), result.Cursor, pins, 8,
	)
	if err != nil || result.Scanned > MaxRepositoryGenerations ||
		result.ReleasedPinOwner != "relationship:"+roots[0].GenerationDigest {
		t.Fatalf("bounded oldest v3 retirement = %+v, %v", result, err)
	}
}

func publishLifecycleGenerationV3(
	t *testing.T,
	root, repository, suffix, runID string,
	pins PublishPinStoreV3,
) RootV3 {
	t.Helper()
	prepared := prepareLifecycleGenerationV3(t, root, repository, suffix, runID)
	publication, err := PublishV3(t.Context(), prepared, pins)
	if err != nil {
		t.Fatal(err)
	}
	return publication.Root()
}

func prepareLifecycleGenerationV3(
	t *testing.T,
	root, repository, suffix, runID string,
) *PreparedV3 {
	t.Helper()
	upstream := lifecycleUpstreamV3(t, repository, runID)
	emptySet, err := digestServiceSetV3([]serviceSetIdentityV3{})
	if err != nil {
		t.Fatal(err)
	}
	authority := AuthorityV3{
		Repository:        repository,
		CatalogRootDigest: fixedDigest("1"), CatalogLogicalDigest: fixedDigest("2"),
		CatalogSourceGeneration: fixedDigest("3"), CatalogControlRevision: 7,
		ServiceStateSetDigest: emptySet, ServiceStateSummaryDigest: fixedDigest("4"),
		ServiceStateControlRevision: 9,
		ObservationGenerationDigest: upstream.Observation.ObservationGenerationDigest,
		ObservationManifestDigest:   fixedDigest("5"),
		ObservationSourceDigest:     upstream.Observation.SourceRootDigest,
		ResolverGenerationDigest:    fixedDigest(suffix), ResolverRootDigest: fixedDigest("6"),
		RPCGenerationDigest: fixedDigest("7"), RPCRootDigest: fixedDigest("8"),
		KafkaGenerationDigest: fixedDigest("9"), KafkaRootDigest: fixedDigest("a"),
		Upstream: upstream, UpstreamDigest: upstream.Digest,
		PolicyDigest: mustDigest(t, FrozenPolicyV3()),
	}
	accumulator := &buildAccumulator{
		repository: map[int][]Projection{}, services: map[string]*serviceAccumulator{},
		seen: map[string]struct{}{}, serviceRefLimit: MaxServiceReferences,
		totalRefLimit: MaxTotalServiceReferences, residentLimit: MaxResidentChargeBytes,
	}
	prepared, err := writePublicationStageV3(
		t.Context(), root, authority, mustDigest(t, authority), nil, accumulator,
	)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func lifecycleUpstreamV3(
	t *testing.T,
	repository, runID string,
) downstreamauthority.Authority {
	t.Helper()
	observation := observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: repository,
		SourceGenerationDigest: fixedDigest("b"), SourceRootDigest: fixedDigest("c"),
		ObservationGenerationDigest: fixedDigest("d"), ObservationRootDigest: fixedDigest("e"),
		PartitionPolicyDigest: fixedDigest("f"), ObservationPolicyDigest: fixedDigest("1"),
		InventoryPolicyDigest: fixedDigest("2"), RecordCount: 1, ObservedCount: 1,
	}
	domain := candidate.DownstreamDomainAuthority{
		Domain: "proto-contract", Version: "v1", PlanDigest: fixedDigest("3"),
		RootDigest: fixedDigest("4"), RunID: runID,
		Disposition:             candidate.PartitionResultEmpty,
		CandidateManifestDigest: fixedDigest("5"), CandidatePartitionRootDigest: fixedDigest("6"),
		CandidatePolicyDigest:       fixedDigest("7"),
		SourceGenerationDigest:      observation.SourceGenerationDigest,
		ObservationGenerationDigest: observation.ObservationGenerationDigest,
		ExtractionPolicyDigest:      fixedDigest("8"), DomainIndexDigest: fixedDigest("9"),
		DomainScheduleDigest: fixedDigest("a"),
	}
	value, err := downstreamauthority.Build(
		observation, []candidate.DownstreamDomainAuthority{domain},
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGenerationPathV3(
	t *testing.T,
	root, repository, generation string,
) string {
	t.Helper()
	path, err := GenerationPathV3(root, repository, generation)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
