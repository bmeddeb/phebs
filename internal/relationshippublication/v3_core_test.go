package relationshippublication

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

type testPublishPinsV3 struct {
	relationshipPins int
	runs             []string
	unpins           int
	failRelationship bool
	failRun          bool
	failUnpin        bool
}

func (pins *testPublishPinsV3) PinRelationshipPublicationV3(
	_ context.Context,
	_, _, _, _ string,
	_, _ uint64,
	_ string,
) error {
	pins.relationshipPins++
	if pins.failRelationship {
		return errors.New("relationship pin unavailable")
	}
	return nil
}

func (pins *testPublishPinsV3) RecoverRelationshipPublicationV3(
	ctx context.Context,
	repository, generation, root, catalogRoot string,
	catalogRevision, stateRevision uint64,
	stateSummary string,
) error {
	return pins.PinRelationshipPublicationV3(
		ctx, repository, generation, root, catalogRoot,
		catalogRevision, stateRevision, stateSummary,
	)
}

func (pins *testPublishPinsV3) UnpinRelationshipPublicationV3(
	_ context.Context,
	_, _, _, _ string,
	_, _ uint64,
	_ string,
) error {
	pins.unpins++
	if pins.failUnpin {
		return errors.New("relationship unpin unavailable")
	}
	return nil
}

func (pins *testPublishPinsV3) PinPartitionedExtractionRun(
	_ context.Context,
	runID, owner string,
) error {
	pins.runs = append(pins.runs, runID+"\x00"+owner)
	if pins.failRun {
		return errors.New("extraction pin unavailable")
	}
	return nil
}

func (pins *testPublishPinsV3) UnpinPartitionedExtractionRun(
	_ context.Context,
	_, _ string,
) error {
	pins.unpins++
	if pins.failUnpin {
		return errors.New("extraction unpin unavailable")
	}
	return nil
}

func TestProjectionBucketsV3PreserveMaximumAlignedClaims(t *testing.T) {
	claims := make([]ServiceClaim, MaxClaimsPerPlacement)
	for index := range claims {
		disposition := servicecatalog.DispositionAccepted
		if index%2 != 0 {
			disposition = servicecatalog.DispositionConflict
		}
		claims[index] = ServiceClaim{
			ServiceKey:  "service-" + leftPadDecimalV3(index, 4),
			Disposition: disposition,
			Roles:       []RoleClaim{{Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase}},
		}
	}
	projection, err := finalizeProjectionV3(Projection{
		Schema: ProjectionSchema, Kind: "rpc", PostingDigest: fixedDigest("a"),
		Class: "resolved", Plane: "grpc", LookupKey: "example.v1/Get",
		Source: Placement{Path: "source/file.go", Claims: claims},
		Target: &Placement{Path: "target/file.go", Claims: append([]ServiceClaim(nil), claims...)},
	})
	if err != nil {
		t.Fatal(err)
	}
	fragments, encodedBytes, err := projectionBucketsV3(projection)
	if err != nil {
		t.Fatal(err)
	}
	if len(fragments) != MaxProjectionBucketsV3 {
		t.Fatalf("fragment count = %d, want %d", len(fragments), MaxProjectionBucketsV3)
	}
	observedBytes := 0
	for ordinal, fragment := range fragments {
		raw, marshalErr := json.Marshal(fragment)
		observedBytes += len(raw)
		if marshalErr != nil || fragment.Ordinal != ordinal || fragment.Count != len(fragments) ||
			len(fragment.Source.Claims) > MaxClaimsPerProjectionBucketV3 ||
			len(fragment.Target.Claims) > MaxClaimsPerProjectionBucketV3 ||
			len(raw) > MaxProjectionBucketBytesV3 {
			t.Fatalf("fragment %d = source %d target %d bytes %d error %v",
				ordinal, len(fragment.Source.Claims), len(fragment.Target.Claims), len(raw), marshalErr)
		}
	}
	if encodedBytes != observedBytes {
		t.Fatalf("fragment encoded bytes = %d, want %d", encodedBytes, observedBytes)
	}
	reconstructed, err := flattenProjectionBucketsV3(fragments)
	if err != nil || !reflect.DeepEqual(reconstructed, projection) {
		t.Fatalf("reconstructed maximum projection mismatch: %v", err)
	}
	if reconstructed.Source.Claims[1].Disposition != servicecatalog.DispositionConflict {
		t.Fatal("nonaccepted placement evidence was lost")
	}
	overflow := fragments[0]
	overflow.Source.Claims = append(slices.Clone(overflow.Source.Claims), ServiceClaim{
		ServiceKey: "service-overflow", Disposition: servicecatalog.DispositionAccepted,
		Roles: []RoleClaim{{Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase}},
	})
	if len(overflow.Source.Claims) != MaxClaimsPerProjectionBucketV3+1 ||
		len(overflow.Target.Claims) != MaxClaimsPerProjectionBucketV3 {
		t.Fatal("projection claim one-over fixture did not isolate the source-claim bound")
	}
	if err := validatePlacementBucketV3(overflow.Source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("projection bucket claim one-over = %v", err)
	}
}

func TestRepositoryMemberAdmissionRefusesConcentratedBucketBeforeMarshal(t *testing.T) {
	envelopeBytes, err := repositoryMemberEnvelopeBytesV3(17)
	if err != nil {
		t.Fatal(err)
	}
	const fragmentBytes = 37
	// The scaled limit admits either fragment by itself, but not both in the
	// same hash bucket because the second fragment and its comma exceed it.
	limit := envelopeBytes + 2*fragmentBytes
	count, encoded, total, err := admitRepositoryFragmentsV3(
		envelopeBytes, 0, 0, 1, fragmentBytes, limit,
	)
	if err != nil || count != 1 || encoded != fragmentBytes || total != envelopeBytes+fragmentBytes {
		t.Fatalf("first concentrated fragment = count %d bytes %d total %d error %v", count, encoded, total, err)
	}
	if _, _, _, err := admitRepositoryFragmentsV3(
		envelopeBytes, count, encoded, 1, fragmentBytes, limit,
	); !errors.Is(err, ErrLimit) {
		t.Fatalf("concentrated member limit error = %v", err)
	}
	_, _, total, err = admitRepositoryFragmentsV3(
		envelopeBytes, count, encoded, 1, fragmentBytes, limit+1,
	)
	if err != nil || total != limit+1 {
		t.Fatalf("exact concentrated member boundary = total %d error %v", total, err)
	}
}

func TestValidateRootV3RejectsOverflowShapedCounters(t *testing.T) {
	base := preparedRelationshipV3Test(
		t, t.TempDir(), "example.com/acme/v3-overflow-root",
	).Root()
	maxInt := int(^uint(0) >> 1)
	large := int(uint(1) << (strconv.IntSize - 5))

	tests := []struct {
		name   string
		mutate func(*RootV3)
	}{
		{
			name: "root disposition sum overflow",
			mutate: func(root *RootV3) {
				root.CompleteServiceCount = maxInt
				root.EmptyServiceCount = 3
				root.FailedServiceCount = maxInt
				root.AllServicesComplete = false
				root.ServiceMembers[0].CompleteCount = maxInt
				root.ServiceMembers[0].EmptyCount = 3
				root.ServiceMembers[0].FailedCount = maxInt
			},
		},
		{
			name: "repository receipt accumulation overflow",
			mutate: func(root *RootV3) {
				root.RepositoryMembers = make([]RepositoryReceiptV3, 0, 33)
				for bucket := range 32 {
					root.RepositoryMembers = append(root.RepositoryMembers, RepositoryReceiptV3{
						Bucket: bucket, Name: repositoryMemberName(bucket),
						ProjectionCount: large, FragmentCount: large,
						ContentBytes: 1, ContentDigest: fixedDigest("a"),
					})
				}
				root.RepositoryMembers = append(root.RepositoryMembers, RepositoryReceiptV3{
					Bucket: 32, Name: repositoryMemberName(32),
					ProjectionCount: 1, FragmentCount: 1,
					ContentBytes: 1, ContentDigest: fixedDigest("b"),
				})
				root.ProjectionCount = 1
				root.ProjectionFragmentCount = 1
				root.EncodedRepositoryBytes = 33
			},
		},
		{
			name: "service receipt accumulation overflow",
			mutate: func(root *RootV3) {
				const count = 32
				root.ServiceMembers = make([]ServiceRangeReceiptV3, 0, count)
				for ordinal := range count {
					key := "overflow-service-" + leftPadDecimalV3(ordinal, 2)
					root.ServiceMembers = append(root.ServiceMembers, ServiceRangeReceiptV3{
						Ordinal: ordinal, Count: count, FirstKey: key, LastKey: key,
						ServiceCount: 1, CompleteCount: large, EmptyCount: 1, FailedCount: -large,
						Name: serviceRangeMemberNameV3(ordinal), ContentBytes: 1,
						ContentDigest: fixedDigest("c"),
					})
				}
				root.ServiceCount = count
				root.CompleteServiceCount = 0
				root.EmptyServiceCount = count
				root.FailedServiceCount = 0
				root.ServiceReferenceCount = 0
				root.EncodedServiceBytes = count
				root.AllServicesComplete = true
			},
		},
		{
			name: "oversized service receipt last key",
			mutate: func(root *RootV3) {
				root.ServiceMembers[0].LastKey = strings.Repeat("z", MaxTextBytes+1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := cloneRootV3(base)
			test.mutate(&root)
			root.GenerationDigest, root.Digest = "", ""
			var err error
			root.GenerationDigest, err = generationDigestV3(root)
			if err != nil {
				t.Fatal(err)
			}
			root.Digest, err = rootDigestV3(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateRootV3(root); !errors.Is(err, ErrInvalid) {
				t.Fatalf("overflow-shaped root validation error = %v", err)
			}
		})
	}
}

func TestServiceRecordsV3PreserveLocalFailure(t *testing.T) {
	services := map[string]*serviceAccumulator{
		"failed": {
			state: servicecatalog.ServiceState{
				ServiceKey: "failed", Incarnation: 1, DesiredGeneration: fixedDigest("a"),
			},
			failed: true, reason: "reference_limit",
		},
		"healthy": {
			state: servicecatalog.ServiceState{
				ServiceKey: "healthy", Incarnation: 1, DesiredGeneration: fixedDigest("b"),
			},
			refs: []ServiceReference{},
		},
	}
	records, err := serviceRecordsV3(services)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].State != "failed" ||
		records[0].Reason != "reference_limit" || records[1].State != "empty" {
		t.Fatalf("service-local records = %+v", records)
	}
}

func TestBuildPublishRecoverAndSparseReadsV3(t *testing.T) {
	const serviceCount = MaxServicesPerServiceMemberV3 + 1
	rootPath := t.TempDir()
	repository := "example.com/acme/v3-relationships"
	catalog, generation := relationshipCatalogV3Test(t, repository, serviceCount)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	upstream := relationshipUpstreamV3Test(t, repository)
	resolver := relationshipResolverV3Test(t, repository, upstream)
	rpcValues := []rpccallerposting.Posting{
		rpcPosting("a", "resolved", "grpc", "first.v1/Get", "services/00000/call.go", ""),
		rpcPosting("b", "resolved", "grpc", "last.v1/Get", "services/00512/call.go", ""),
	}
	rpc := fakeRPC{root: relationshipRPCV3Test(t, repository, resolver.root, upstream, rpcValues), values: rpcValues}
	kafka := fakeKafka{root: relationshipKafkaV3Test(t, repository, rpc.root.Authority, upstream, nil)}
	prepared, err := BuildV3(t.Context(), BuildRequestV3{
		Root: rootPath, Catalog: generation, States: states, ServiceSummary: summary,
		Resolver: resolver, RPC: rpc, Kafka: kafka, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	upstream.Domains[0].RunID = "mutated-after-build"
	if got := prepared.Root().Authority.Upstream.Domains[0].RunID; got != "v3-run" {
		t.Fatalf("prepared v3 upstream aliases caller input: %q", got)
	}
	pins := &testPublishPinsV3{}
	publication, err := PublishV3(t.Context(), prepared, pins)
	if err != nil {
		t.Fatal(err)
	}
	root := publication.Root()
	if len(root.ServiceMembers) != 2 || root.ServiceCount != serviceCount ||
		root.FailedServiceCount != 0 || pins.relationshipPins != 1 || len(pins.runs) != 1 ||
		pins.runs[0] != "v3-run\x00relationship:"+root.GenerationDigest {
		t.Fatalf("published v3 root/pins = root %+v pins %+v", root, pins)
	}
	first, err := publication.OpenService(t.Context(), "service-00000")
	if err != nil || first.State != "complete" || len(first.References) != 1 {
		t.Fatalf("first sparse service = %+v, %v", first, err)
	}
	last, err := publication.OpenService(t.Context(), "service-00512")
	if err != nil || last.State != "complete" || len(last.References) != 1 {
		t.Fatalf("last sparse service = %+v, %v", last, err)
	}
	cache := &CacheV3{}
	lease, err := cache.AcquireGeneration(
		t.Context(), rootPath, repository, root.GenerationDigest, root.Digest,
	)
	if err != nil || !cache.Pinned(repository, root.GenerationDigest) {
		t.Fatalf("v3 cache lease = %v pinned=%t", err, cache.Pinned(repository, root.GenerationDigest))
	}
	if _, err := cache.AcquireGeneration(
		t.Context(), rootPath, repository, root.GenerationDigest, fixedDigest("f"),
	); err == nil {
		t.Fatal("v3 cache reused a generation under a mismatched root digest")
	}
	lease.Release()

	// Simulate a crash after the durable marker and before current.json.
	_, markerRaw, pointerRaw, err := publicationControlsV3(publication.pointer, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(publication.base, "publishing.json"), markerRaw); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(publication.base, "current.json")); err != nil {
		t.Fatal(err)
	}
	pins.relationshipPins, pins.runs = 0, nil
	recovered, err := RecoverV3(t.Context(), rootPath, repository, pins)
	if err != nil || !recovered || pins.relationshipPins != 1 || len(pins.runs) != 1 {
		t.Fatalf("v3 recovery = recovered=%t pins=%+v err=%v", recovered, pins, err)
	}
	noncanonicalMarker := append(slices.Clone(markerRaw), ' ')
	if err := replaceFile(filepath.Join(publication.base, "publishing.json"), noncanonicalMarker); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(publication.base, "current.json")); err != nil {
		t.Fatal(err)
	}
	noncanonicalPins := &testPublishPinsV3{}
	if recovered, recoverErr := RecoverV3(t.Context(), rootPath, repository, noncanonicalPins); recovered || !errors.Is(recoverErr, ErrInvalid) {
		t.Fatalf("noncanonical marker recovery = recovered %t error %v", recovered, recoverErr)
	}
	if noncanonicalPins.relationshipPins != 0 || len(noncanonicalPins.runs) != 0 {
		t.Fatalf("noncanonical marker acquired pins: %+v", noncanonicalPins)
	}
	if _, statErr := os.Stat(filepath.Join(publication.base, "current.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("noncanonical marker installed current pointer: %v", statErr)
	}
	if err := replaceFile(filepath.Join(publication.base, "current.json"), pointerRaw); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name             string
		failRelationship bool
		failRun          bool
	}{
		{name: "relationship pin", failRelationship: true},
		{name: "extraction pin", failRun: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := replaceFile(filepath.Join(publication.base, "publishing.json"), markerRaw); err != nil {
				t.Fatal(err)
			}
			failingPins := &testPublishPinsV3{
				failRelationship: test.failRelationship,
				failRun:          test.failRun,
			}
			recovered, recoverErr := RecoverV3(t.Context(), rootPath, repository, failingPins)
			var storeErr recoveryStoreError
			if recovered || !errors.As(recoverErr, &storeErr) {
				t.Fatalf("recovery store failure = recovered %t error %v", recovered, recoverErr)
			}
			if _, statErr := os.Stat(filepath.Join(publication.base, "publishing.json")); statErr != nil {
				t.Fatalf("recovery removed marker after pin failure: %v", statErr)
			}
		})
	}
	if err := os.Remove(filepath.Join(publication.base, "publishing.json")); err != nil {
		t.Fatal(err)
	}

	// Corruption outside the selected range does not widen a sparse read, but
	// the independent full-generation validator rejects it.
	unrelated := root.ServiceMembers[1]
	if err := os.WriteFile(
		filepath.Join(publication.directory, unrelated.Name), []byte("{}"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if record, err := publication.ReadService(t.Context(), "service-00000"); err != nil ||
		record.ServiceKey != "service-00000" {
		t.Fatalf("sparse sibling read = %+v, %v", record, err)
	}
	if _, err := ValidateGenerationV3(
		t.Context(), rootPath, repository, root.GenerationDigest, root.Digest,
	); err == nil {
		t.Fatal("complete v3 validation accepted corrupt unrelated range")
	}
}

func TestRecoverV3InstallsMarkerOwnedStage(t *testing.T) {
	rootPath := t.TempDir()
	repository := "example.com/acme/v3-marker-stage"
	prepared := preparedRelationshipV3Test(t, rootPath, repository)
	stage := prepared.directory
	pointer, err := newPointerV3(prepared.rootValue)
	if err != nil {
		t.Fatal(err)
	}
	marker, markerRaw, _, err := publicationControlsV3(pointer, filepath.Base(stage))
	if err != nil {
		t.Fatal(err)
	}
	base, err := RepositoryRootV3(rootPath, repository)
	if err != nil {
		t.Fatal(err)
	}
	target, err := GenerationPathV3(rootPath, repository, pointer.GenerationDigest)
	if err != nil {
		t.Fatal(err)
	}
	initialPins := &testPublishPinsV3{}
	if err := pinRelationshipV3(t.Context(), initialPins, prepared.rootValue); err != nil {
		t.Fatal(err)
	}
	owner := "relationship:" + prepared.rootValue.GenerationDigest
	for _, domain := range prepared.rootValue.Authority.Upstream.Domains {
		if err := initialPins.PinPartitionedExtractionRun(t.Context(), domain.RunID, owner); err != nil {
			t.Fatal(err)
		}
	}
	if err := replaceFile(filepath.Join(base, "publishing.json"), markerRaw); err != nil {
		t.Fatal(err)
	}
	publishingTemporary := filepath.Join(base, "publishing.json.tmp")
	if err := writeExclusive(publishingTemporary, markerRaw); err != nil {
		t.Fatal(err)
	}
	prepared.closed = true // the simulated process can no longer abort marker-owned bytes
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generation exists before marker recovery: %v", err)
	}

	pins := &testPublishPinsV3{}
	recovered, err := RecoverV3(t.Context(), rootPath, repository, pins)
	if err != nil || !recovered {
		t.Fatalf("marker-owned stage recovery = %t, %v", recovered, err)
	}
	if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker-owned stage survived generation rename: %v", err)
	}
	publication, err := ValidateGenerationV3(
		t.Context(), rootPath, repository, pointer.GenerationDigest, pointer.RootDigest,
	)
	if err != nil || publication.Root().Digest != pointer.RootDigest {
		t.Fatalf("recovered generation = %+v, %v", publication, err)
	}
	current, err := ReadPointerV3(t.Context(), rootPath, repository)
	if err != nil || current != marker.Pointer {
		t.Fatalf("recovered pointer = %+v, %v", current, err)
	}
	if _, err := os.Stat(filepath.Join(base, "publishing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed recovery retained marker: %v", err)
	}
	if _, err := os.Stat(publishingTemporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed recovery retained marker temporary: %v", err)
	}
}

func TestPublishV3PinFailuresAbortBeforeMarker(t *testing.T) {
	tests := []struct {
		name             string
		failRelationship bool
		failRun          bool
		failUnpin        bool
	}{
		{name: "relationship pin", failRelationship: true},
		{name: "extraction pin", failRun: true},
		{name: "rollback", failRun: true, failUnpin: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rootPath := t.TempDir()
			repository := "example.com/acme/v3-pin-" + strings.ReplaceAll(test.name, " ", "-")
			prepared := preparedRelationshipV3Test(t, rootPath, repository)
			root := prepared.Root()
			stage := prepared.directory
			failing := &testPublishPinsV3{
				failRelationship: test.failRelationship,
				failRun:          test.failRun,
				failUnpin:        test.failUnpin,
			}
			if publication, err := PublishV3(t.Context(), prepared, failing); err == nil || publication != nil {
				t.Fatalf("failed publish = %+v, %v", publication, err)
			}
			base, err := RepositoryRootV3(rootPath, repository)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(base, "publishing.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pin failure installed recovery marker: %v", err)
			}
			target, err := GenerationPathV3(rootPath, repository, root.GenerationDigest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pin failure installed generation: %v", err)
			}
			if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pin failure retained stage after abort: %v", err)
			}
			if _, err := ReadPointerV3(t.Context(), rootPath, repository); !errors.Is(err, ErrNotFound) {
				t.Fatalf("pin failure swapped current pointer: %v", err)
			}
			if failing.unpins == 0 {
				t.Fatal("pin failure did not attempt bounded rollback")
			}
		})
	}
}

func TestPublishV3DefiniteMarkerInstallFailureAborts(t *testing.T) {
	rootPath := t.TempDir()
	repository := "example.com/acme/v3-marker-install-failure"
	prepared := preparedRelationshipV3Test(t, rootPath, repository)
	root := prepared.Root()
	stage := prepared.directory
	base, err := RepositoryRootV3(rootPath, repository)
	if err != nil {
		t.Fatal(err)
	}
	// A nonempty exact temp path makes replaceFile fail before publishing.json
	// can exist. The returned-error path must undo pins and the private stage.
	temporary := filepath.Join(base, "publishing.json.tmp")
	if err := os.Mkdir(temporary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temporary, "blocked"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins := &testPublishPinsV3{}
	if publication, err := PublishV3(t.Context(), prepared, pins); err == nil || publication != nil {
		t.Fatalf("marker install failure = %+v, %v", publication, err)
	}
	if pins.relationshipPins != 1 || len(pins.runs) != 1 || pins.unpins != 2 {
		t.Fatalf("marker install rollback pins = %+v", pins)
	}
	if _, err := os.Stat(filepath.Join(base, "publishing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed marker install created final marker: %v", err)
	}
	if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed marker install retained stage: %v", err)
	}
	target, err := GenerationPathV3(rootPath, repository, root.GenerationDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed marker install created generation: %v", err)
	}
}

func TestPublishV3RejectsInvalidCurrentBeforePins(t *testing.T) {
	tests := []struct {
		name string
		raw  func(*testing.T, *PreparedV3) []byte
	}{
		{name: "corrupt", raw: func(*testing.T, *PreparedV3) []byte { return []byte("{}") }},
		{name: "noncanonical", raw: func(t *testing.T, prepared *PreparedV3) []byte {
			t.Helper()
			pointer, err := newPointerV3(prepared.rootValue)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(pointer)
			if err != nil {
				t.Fatal(err)
			}
			return append(raw, ' ')
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rootPath := t.TempDir()
			repository := "example.com/acme/v3-invalid-current-" + test.name
			prepared := preparedRelationshipV3Test(t, rootPath, repository)
			root := prepared.Root()
			base, err := RepositoryRootV3(rootPath, repository)
			if err != nil {
				t.Fatal(err)
			}
			currentRaw := test.raw(t, prepared)
			if err := replaceFile(filepath.Join(base, "current.json"), currentRaw); err != nil {
				t.Fatal(err)
			}
			pins := &testPublishPinsV3{}
			if publication, err := PublishV3(t.Context(), prepared, pins); err == nil || publication != nil {
				t.Fatalf("invalid-current publish = %+v, %v", publication, err)
			}
			if pins.relationshipPins != 0 || len(pins.runs) != 0 || pins.unpins != 0 {
				t.Fatalf("invalid current reached pin boundary: %+v", pins)
			}
			if _, err := os.Stat(filepath.Join(base, "publishing.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid current installed marker: %v", err)
			}
			gotCurrent, err := readRegular(filepath.Join(base, "current.json"), MaxRootBytesV3)
			if err != nil || !slices.Equal(gotCurrent, currentRaw) {
				t.Fatalf("invalid current was replaced: %q, %v", gotCurrent, err)
			}
			target, err := GenerationPathV3(rootPath, repository, root.GenerationDigest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid current installed generation: %v", err)
			}
		})
	}
}

func TestPublishV3RetainedTargetFailurePreservesPins(t *testing.T) {
	for _, test := range []struct {
		name          string
		markerFailure bool
	}{
		{name: "extraction pin"},
		{name: "marker install", markerFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootPath := t.TempDir()
			repository := "example.com/acme/v3-retained-" + strings.ReplaceAll(test.name, " ", "-")
			first := preparedRelationshipV3Test(t, rootPath, repository)
			published, err := PublishV3(t.Context(), first, &testPublishPinsV3{})
			if err != nil {
				t.Fatal(err)
			}
			retainedRoot := published.Root()
			prepared := preparedRelationshipV3Test(t, rootPath, repository)
			stage := prepared.directory
			base, err := RepositoryRootV3(rootPath, repository)
			if err != nil {
				t.Fatal(err)
			}
			different := PointerV3{
				Schema: PointerSchemaV3, Repository: repository,
				GenerationDigest: fixedDigest("d"), RootDigest: fixedDigest("e"),
				RootName: "root.json",
			}
			different.Digest, err = digestValue(different)
			if err != nil || validatePointerV3(different) != nil {
				t.Fatalf("different current = %+v, %v", different, err)
			}
			currentRaw, err := json.Marshal(different)
			if err != nil {
				t.Fatal(err)
			}
			if err := replaceFile(filepath.Join(base, "current.json"), currentRaw); err != nil {
				t.Fatal(err)
			}
			if test.markerFailure {
				temporary := filepath.Join(base, "publishing.json.tmp")
				if err := os.Mkdir(temporary, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(temporary, "blocked"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			pins := &testPublishPinsV3{failRun: !test.markerFailure}
			if publication, err := PublishV3(t.Context(), prepared, pins); err == nil || publication != nil {
				t.Fatalf("retained-target failure = %+v, %v", publication, err)
			}
			if pins.unpins != 0 {
				t.Fatalf("retained-target failure removed preexisting pins: %+v", pins)
			}
			if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("retained-target failure retained private stage: %v", err)
			}
			if _, err := os.Stat(filepath.Join(base, "publishing.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("retained-target failure installed marker: %v", err)
			}
			gotCurrent, err := readRegular(filepath.Join(base, "current.json"), MaxRootBytesV3)
			if err != nil || !slices.Equal(gotCurrent, currentRaw) {
				t.Fatalf("retained-target failure changed current: %q, %v", gotCurrent, err)
			}
			if _, err := ValidateGenerationV3(
				t.Context(), rootPath, repository,
				retainedRoot.GenerationDigest, retainedRoot.Digest,
			); err != nil {
				t.Fatalf("retained target changed after failure: %v", err)
			}
		})
	}
}

func TestRemovePublishingTemporaryV3(t *testing.T) {
	rootPath := t.TempDir()
	repository := "example.com/acme/v3-marker-temporary"
	prepared := preparedRelationshipV3Test(t, rootPath, repository)
	base, err := RepositoryRootV3(rootPath, repository)
	if err != nil {
		t.Fatal(err)
	}
	pointer, err := newPointerV3(prepared.rootValue)
	if err != nil {
		t.Fatal(err)
	}
	_, markerRaw, _, err := publicationControlsV3(pointer, filepath.Base(prepared.directory))
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(base, "publishing.json.tmp")
	if err := writeExclusive(temporary, markerRaw); err != nil {
		t.Fatal(err)
	}
	if err := removePublishingTemporaryV3(base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uncommitted publishing temporary survived cleanup: %v", err)
	}
}

func TestOpenDirectoryCompleteV3BoundsInventory(t *testing.T) {
	rootPath := t.TempDir()
	prepared := preparedRelationshipV3Test(
		t, rootPath, "example.com/acme/v3-inventory-overflow",
	)
	entries, err := os.ReadDir(prepared.directory)
	if err != nil {
		t.Fatal(err)
	}
	for index := len(entries); index < MaxGenerationFilesV3+1; index++ {
		name := "extra-" + leftPadDecimalV3(index, 4)
		if err := os.WriteFile(filepath.Join(prepared.directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := openDirectoryCompleteV3(
		t.Context(), prepared.directory, prepared.rootValue,
	); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrLimit) {
		t.Fatalf("overflow generation inventory error = %v", err)
	}
}

func TestV3ReadersPreserveFilesystemErrors(t *testing.T) {
	rootPath := t.TempDir()
	missingDirectory := filepath.Join(rootPath, "missing")
	if _, err := openDirectoryCompleteV3(
		t.Context(), missingDirectory, RootV3{},
	); !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrInvalid) {
		t.Fatalf("generation stat error taxonomy = %v", err)
	}
	if _, err := ValidateGenerationV3(
		t.Context(), rootPath, "example.com/acme/v3-missing-generation",
		fixedDigest("1"), fixedDigest("2"),
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing generation error taxonomy = %v", err)
	}

	prepared := preparedRelationshipV3Test(
		t, rootPath, "example.com/acme/v3-reader-filesystem-error",
	)
	publication, err := PublishV3(t.Context(), prepared, &testPublishPinsV3{})
	if err != nil {
		t.Fatal(err)
	}
	serviceReceipt := prepared.rootValue.ServiceMembers[0]
	if err := os.Remove(filepath.Join(publication.directory, serviceReceipt.Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := publication.openServiceMemberV3(
		t.Context(), serviceReceipt,
	); !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrInvalid) {
		t.Fatalf("service member filesystem error taxonomy = %v", err)
	}
	if _, err := publication.openRepositoryMemberV3(
		t.Context(), RepositoryReceiptV3{Name: "missing-repository-member.json"},
	); !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrInvalid) {
		t.Fatalf("repository member filesystem error taxonomy = %v", err)
	}
	if _, err := ValidateGenerationV3(
		t.Context(), rootPath, prepared.repository,
		prepared.rootValue.GenerationDigest, prepared.rootValue.Digest,
	); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
		t.Fatalf("complete generation missing-member taxonomy = %v", err)
	}
}

func TestCacheV3CurrentAcquireReservesBeforeOpen(t *testing.T) {
	rootPath := t.TempDir()
	repository := "example.com/acme/v3-cache-reservation"
	prepared := preparedRelationshipV3Test(t, rootPath, repository)
	publication, err := PublishV3(t.Context(), prepared, &testPublishPinsV3{})
	if err != nil {
		t.Fatal(err)
	}
	root := publication.Root()
	cache := &CacheV3{}

	const reservations = 16
	releases := make(chan func(), reservations)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range reservations {
		wait.Add(1)
		go func() {
			defer wait.Done()
			release, ok := cache.reserveGeneration(repository, root.GenerationDigest)
			if !ok {
				releases <- nil
				return
			}
			releases <- release
			<-start
			release()
		}()
	}
	held := make([]func(), 0, reservations)
	for range reservations {
		held = append(held, <-releases)
	}
	allReserved := true
	for _, release := range held {
		if release == nil {
			allReserved = false
		}
	}
	if !allReserved {
		close(start)
		wait.Wait()
		t.Fatal("concurrent cache reservation was refused")
	}
	if finish, ok := cache.BeginRetire(repository, root.GenerationDigest); ok {
		finish()
		t.Fatal("retirement crossed concurrent current-acquire reservations")
	}
	close(start)
	wait.Wait()
	if finish, ok := cache.BeginRetire(repository, root.GenerationDigest); !ok {
		t.Fatal("released cache reservations remained pinned")
	} else {
		finish()
	}

	lease, err := cache.Acquire(t.Context(), rootPath, repository)
	if err != nil || lease.Publication() == nil || lease.Publication().ConfirmCurrent() != nil {
		t.Fatalf("current cache acquisition = %+v, %v", lease, err)
	}
	if finish, ok := cache.BeginRetire(repository, root.GenerationDigest); ok {
		finish()
		t.Fatal("retirement crossed an acquired current lease")
	}
	lease.Release()
	if finish, ok := cache.BeginRetire(repository, root.GenerationDigest); !ok {
		t.Fatal("released current lease remained pinned")
	} else {
		finish()
	}
}

func preparedRelationshipV3Test(
	t *testing.T,
	rootPath, repository string,
) *PreparedV3 {
	t.Helper()
	if err := os.MkdirAll(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog, generation := relationshipCatalogV3Test(t, repository, 1)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	upstream := relationshipUpstreamV3Test(t, repository)
	resolver := relationshipResolverV3Test(t, repository, upstream)
	rpc := fakeRPC{root: relationshipRPCV3Test(t, repository, resolver.root, upstream, nil)}
	kafka := fakeKafka{root: relationshipKafkaV3Test(t, repository, rpc.root.Authority, upstream, nil)}
	prepared, err := BuildV3(t.Context(), BuildRequestV3{
		Root: rootPath, Catalog: generation, States: states, ServiceSummary: summary,
		Resolver: resolver, RPC: rpc, Kafka: kafka, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func relationshipCatalogV3Test(
	t *testing.T,
	repository string,
	count int,
) (servicecatalog.Catalog, servicecatalogv3.Generation) {
	t.Helper()
	commit := strings.Repeat("a", 40)
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: commit,
	}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services:    make([]servicecatalog.Service, 0, count),
		Memberships: make([]servicecatalog.Membership, 0, count),
	}
	for index := range count {
		key := "service-" + leftPadDecimalV3(index, 5)
		path := "services/" + leftPadDecimalV3(index, 5)
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: key, DisplayName: key, Disposition: servicecatalog.DispositionAccepted,
			Origin: servicecatalog.OriginBase,
		})
		catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
			ServiceKey: key, Path: path, Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		})
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: repository,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceCommitted, Path: "/catalog.json", Commit: commit,
			CensusDigest: fixedDigest("c"), FileCount: count, AcceptedFileCount: count,
		},
		Authority: authority,
	}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := generation.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	return opened, generation
}

func relationshipStatesV3Test(
	t *testing.T,
	root servicecatalogv3.Root,
	catalog servicecatalog.Catalog,
) ([]servicecatalog.ServiceState, servicecatalog.RepositoryState) {
	t.Helper()
	sourceGeneration, err := servicecatalogv3.SourceGenerationDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	states := make([]servicecatalog.ServiceState, 0, len(catalog.Services))
	for _, service := range catalog.Services {
		projection, err := projectCatalogServiceV3(
			root, sourceGeneration, service,
			membershipsForCatalogServiceV3(catalog.Memberships, service.Key),
		)
		if err != nil {
			t.Fatal(err)
		}
		desired, err := servicecatalogv3.ServiceDesiredGeneration(projection, 1)
		if err != nil {
			t.Fatal(err)
		}
		state := servicecatalog.ServiceState{
			Schema: servicecatalogv3.ServiceStateSchema, Repository: root.Binding.Repository,
			ServiceKey: service.Key, DisplayName: service.DisplayName,
			Disposition: service.Disposition, Origin: service.Origin, Successors: []string{},
			Incarnation: 1, DesiredGeneration: desired,
			DesiredSourceGeneration:  projection.SourceGeneration,
			DesiredCatalogGeneration: projection.CatalogGeneration,
			Status:                   servicecatalog.StatusUnavailable, ControlRevision: 1, ChangedAt: now,
		}
		if err := servicecatalogv3.SetServiceStateDigest(&state); err != nil {
			t.Fatal(err)
		}
		states = append(states, state)
	}
	summary := servicecatalog.RepositoryState{
		Schema: servicecatalogv3.RepositoryStateSchema, Repository: root.Binding.Repository,
		CatalogGeneration: root.Digest, CatalogControlRevision: 1,
		CatalogServiceCount: len(states), LiveServiceCount: len(states),
		UnavailableCount: len(states), ControlRevision: 1, UpdatedAt: now,
	}
	if err := servicecatalogv3.SetRepositoryStateDigest(&summary); err != nil {
		t.Fatal(err)
	}
	return states, summary
}

func relationshipUpstreamV3Test(t *testing.T, repository string) downstreamauthority.Authority {
	t.Helper()
	observation := observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: repository,
		SourceGenerationDigest: fixedDigest("5"), SourceRootDigest: fixedDigest("6"),
		ObservationGenerationDigest: fixedDigest("3"), ObservationRootDigest: fixedDigest("4"),
		PartitionPolicyDigest: fixedDigest("7"), ObservationPolicyDigest: sourceobservation.PolicyDigest(),
		InventoryPolicyDigest: fixedDigest("8"), RecordCount: 2, ObservedCount: 2,
	}
	domain := candidate.DownstreamDomainAuthority{
		Domain: "proto-contract", Version: "v1", PlanDigest: fixedDigest("9"),
		RootDigest: fixedDigest("a"), RunID: "v3-run",
		Disposition:             candidate.PartitionResultEmpty,
		CandidateManifestDigest: fixedDigest("b"), CandidatePartitionRootDigest: fixedDigest("c"),
		CandidatePolicyDigest: fixedDigest("d"), SourceGenerationDigest: observation.SourceGenerationDigest,
		ObservationGenerationDigest: observation.ObservationGenerationDigest,
		ExtractionPolicyDigest:      fixedDigest("e"), DomainIndexDigest: fixedDigest("f"),
		DomainScheduleDigest: fixedDigest("1"),
	}
	value, err := downstreamauthority.Build(
		observation, []candidate.DownstreamDomainAuthority{domain},
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func relationshipResolverV3Test(
	t *testing.T,
	repository string,
	upstream downstreamauthority.Authority,
) fakeResolver {
	t.Helper()
	value := resolverRoot(t, repository)
	value.Schema = resolvernamespace.RootSchemaV2
	value.Authority.Upstream = &upstream
	value.GenerationDigest, value.Digest = "", ""
	value.GenerationDigest = mustDigest(t, value)
	value.Digest = mustDigest(t, value)
	if err := resolvernamespace.ValidateRoot(value); err != nil {
		t.Fatal(err)
	}
	return fakeResolver{root: value}
}

func relationshipRPCV3Test(
	t *testing.T,
	repository string,
	resolver resolvernamespace.Root,
	upstream downstreamauthority.Authority,
	values []rpccallerposting.Posting,
) rpccallerposting.Root {
	t.Helper()
	value := rpcRoot(t, repository, resolver, values)
	value.Schema = rpccallerposting.RootSchemaV2
	value.Authority.ObservationV2 = &upstream.Observation
	value.Authority.Upstream = &upstream
	value.GenerationDigest, value.Digest = "", ""
	value.GenerationDigest = mustDigest(t, value)
	value.Digest = mustDigest(t, value)
	if err := rpccallerposting.ValidateRoot(value); err != nil {
		t.Fatal(err)
	}
	return value
}

func relationshipKafkaV3Test(
	t *testing.T,
	repository string,
	observation rpccallerposting.Authority,
	upstream downstreamauthority.Authority,
	values []kafkatopicposting.Posting,
) kafkatopicposting.Root {
	t.Helper()
	value := kafkaRoot(t, repository, observation, values)
	value.Schema = kafkatopicposting.RootSchemaV2
	value.Authority.ObservationV2 = &upstream.Observation
	value.Authority.Upstream = &upstream
	value.GenerationDigest, value.Digest = "", ""
	value.GenerationDigest = mustDigest(t, value)
	value.Digest = mustDigest(t, value)
	if err := kafkatopicposting.ValidateRoot(value); err != nil {
		t.Fatal(err)
	}
	return value
}

func leftPadDecimalV3(value, width int) string {
	digits := strconv.Itoa(value)
	return strings.Repeat("0", max(0, width-len(digits))) + digits
}
