package relationshippublication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
)

type semanticSnapshotV3Fixture struct {
	rootPath    string
	repository  string
	cache       *CacheV3
	publication *PublicationV3
	expected    SemanticSnapshotV3
}

func TestReadCurrentSemanticSnapshotV3ReturnsExactCompleteGeneration(t *testing.T) {
	fixture := newSemanticSnapshotV3Fixture(t)
	root := fixture.publication.Root()
	memberFiles := uint64(len(root.ServiceMembers) + len(root.RepositoryMembers))
	wantVisits := uint64(2 * (root.ServiceCount + root.ProjectionFragmentCount))
	ctx, ledger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{
			ControlFileReads: 7 + 2*memberFiles, MemberVisits: wantVisits,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	current, err := fixture.cache.ReadCurrentSemanticSnapshot(
		ctx, fixture.rootPath, fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(current, fixture.expected) {
		t.Fatalf("current semantic snapshot mismatch:\n got: %+v\nwant: %+v", current, fixture.expected)
	}
	if len(current.Projections) == 0 {
		t.Fatal("current semantic snapshot has no projection to mutate")
	}
	current.Projections[0].Digest = fixedDigest("0")
	current.Projections[0].Source.Path = "mutated/source.go"
	if len(current.Projections[0].Source.Claims) > 0 {
		current.Projections[0].Source.Claims[0].ServiceKey = "mutated-service"
		if len(current.Projections[0].Source.Claims[0].Roles) > 0 {
			current.Projections[0].Source.Claims[0].Roles[0].Role = "mutated-role"
		}
	}
	snapshot, err := fixture.cache.ReadCurrentSemanticSnapshot(
		ctx, fixture.rootPath, fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, fixture.expected) {
		t.Fatalf("generation semantic snapshot mismatch:\n got: %+v\nwant: %+v", snapshot, fixture.expected)
	}
	if len(snapshot.Projections) != snapshot.Root.ProjectionCount {
		t.Fatalf(
			"semantic snapshot totals = projections %d/%d",
			len(snapshot.Projections), snapshot.Root.ProjectionCount,
		)
	}
	counts, finishErr := ledger.Finish()
	want := readaccounting.Counts{ControlFileReads: 7 + 2*memberFiles, MemberVisits: wantVisits}
	if finishErr != nil || counts != want {
		t.Fatalf("successful semantic snapshot counts = %+v, %v", counts, finishErr)
	}
}

func TestConfirmCurrentSemanticSnapshotV3RechecksOnlyCurrentPointer(t *testing.T) {
	fixture := newSemanticSnapshotV3Fixture(t)
	snapshot, err := fixture.cache.ReadCurrentSemanticSnapshot(
		t.Context(), fixture.rootPath, fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, ledger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{ControlFileReads: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.cache.ConfirmCurrentSemanticSnapshot(
		ctx, fixture.rootPath, fixture.repository, snapshot,
	); err != nil {
		t.Fatal(err)
	}
	counts, finishErr := ledger.Finish()
	if finishErr != nil || counts != (readaccounting.Counts{ControlFileReads: 1}) {
		t.Fatalf("semantic snapshot confirmation counts = %+v, %v", counts, finishErr)
	}
}

func TestConfirmCurrentSemanticSnapshotV3RefusesInvalidOrSupersededSnapshot(t *testing.T) {
	fixture := newSemanticSnapshotV3Fixture(t)
	snapshot, err := fixture.cache.ReadCurrentSemanticSnapshot(
		t.Context(), fixture.rootPath, fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}

	invalid := snapshot
	invalid.Projections = nil
	if err := fixture.cache.ConfirmCurrentSemanticSnapshot(
		t.Context(), fixture.rootPath, fixture.repository, invalid,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid semantic snapshot confirmation = %v", err)
	}

	pointer := fixture.publication.pointer
	pointer.GenerationDigest = fixedDigest("e")
	pointer.RootDigest = fixedDigest("f")
	pointer.Digest = ""
	pointer.Digest, err = digestValue(pointer)
	if err != nil || validatePointerV3(pointer) != nil {
		t.Fatalf("replacement pointer = %+v, %v", pointer, err)
	}
	raw, err := json.Marshal(pointer)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(fixture.publication.base, "current.json"), raw); err != nil {
		t.Fatal(err)
	}
	if err := fixture.cache.ConfirmCurrentSemanticSnapshot(
		t.Context(), fixture.rootPath, fixture.repository, snapshot,
	); !errors.Is(err, ErrPublishing) {
		t.Fatalf("superseded semantic snapshot confirmation = %v", err)
	}
}

func TestAppendSemanticProjectionV3ExactResidentAdmission(t *testing.T) {
	fixture := newSemanticSnapshotV3Fixture(t)
	if len(fixture.expected.Projections) == 0 {
		t.Fatal("semantic snapshot fixture has no projection")
	}
	projection := fixture.expected.Projections[0]
	raw, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	charge := int64(len(raw) + projectionResidentOverhead)
	if charge < 1 {
		t.Fatalf("semantic projection resident charge = %d", charge)
	}

	var snapshot SemanticSnapshotV3
	var residentCharge int64
	if err := appendSemanticProjectionV3(
		&snapshot, &residentCharge, charge-1, projection,
	); !errors.Is(err, ErrLimit) {
		t.Fatalf("one-byte-short resident admission = %v", err)
	}
	if residentCharge != 0 || len(snapshot.Projections) != 0 {
		t.Fatalf(
			"refused resident admission = charge %d projections %d",
			residentCharge, len(snapshot.Projections),
		)
	}
	if err := appendSemanticProjectionV3(
		&snapshot, &residentCharge, charge, projection,
	); err != nil {
		t.Fatalf("exact resident admission = %v", err)
	}
	if residentCharge != charge ||
		!reflect.DeepEqual(snapshot.Projections, []Projection{projection}) {
		t.Fatalf(
			"exact resident admission = charge %d projections %+v",
			residentCharge, snapshot.Projections,
		)
	}
}

func TestReadCurrentSemanticSnapshotV3RefusesCorruptFinalMemberAfterChargingDecodedRecords(t *testing.T) {
	fixture := newSemanticSnapshotV3Fixture(t)
	root := fixture.publication.Root()
	if len(root.RepositoryMembers) == 0 {
		t.Fatal("semantic snapshot fixture has no final repository member")
	}
	memberFiles := uint64(len(root.ServiceMembers) + len(root.RepositoryMembers))
	receipt := root.RepositoryMembers[len(root.RepositoryMembers)-1]
	path := filepath.Join(fixture.publication.directory, receipt.Name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	schema := []byte(RepositoryMemberSchemaV3)
	offset := bytes.Index(raw, schema)
	if offset < 0 {
		t.Fatal("repository member schema is absent")
	}
	raw[offset] = 'x'
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	wantVisits := uint64(root.ServiceCount + root.ProjectionFragmentCount)
	ctx, ledger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{
			ControlFileReads: 3 + memberFiles, MemberVisits: wantVisits,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.cache.ReadCurrentSemanticSnapshot(
		ctx, fixture.rootPath, fixture.repository,
	)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("corrupt final member error = %v", err)
	}
	assertZeroSemanticSnapshotV3(t, snapshot)
	counts, finishErr := ledger.Finish()
	want := readaccounting.Counts{ControlFileReads: 3 + memberFiles, MemberVisits: wantVisits}
	if finishErr != nil || counts != want {
		t.Fatalf("corrupt final member counts = %+v, %v", counts, finishErr)
	}
}

func TestReadCurrentSemanticSnapshotV3MemberLimitRefusesBeforeDelivery(t *testing.T) {
	fixture := newSemanticSnapshotV3Fixture(t)
	root := fixture.publication.Root()
	memberFiles := uint64(len(root.ServiceMembers) + len(root.RepositoryMembers))
	totalVisits := uint64(root.ServiceCount + root.ProjectionFragmentCount)
	if totalVisits < 2 {
		t.Fatalf("semantic snapshot fixture visits = %d, want at least 2", totalVisits)
	}
	limit := totalVisits - 1
	ctx, ledger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{
			ControlFileReads: 3 + memberFiles, MemberVisits: limit,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.cache.ReadCurrentSemanticSnapshot(
		ctx, fixture.rootPath, fixture.repository,
	)
	if !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("semantic snapshot member limit error = %v", err)
	}
	assertZeroSemanticSnapshotV3(t, snapshot)
	counts, finishErr := ledger.Finish()
	want := readaccounting.Counts{ControlFileReads: 3 + memberFiles, MemberVisits: limit + 1}
	if !errors.Is(finishErr, readaccounting.ErrLimit) || counts != want {
		t.Fatalf("semantic snapshot limit counts = %+v, %v; want %+v", counts, finishErr, want)
	}
}

func TestReadCurrentSemanticSnapshotV3CancellationReturnsNothing(t *testing.T) {
	fixture := newSemanticSnapshotV3Fixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	snapshot, err := fixture.cache.ReadCurrentSemanticSnapshot(
		ctx, fixture.rootPath, fixture.repository,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("semantic snapshot cancellation = %v", err)
	}
	assertZeroSemanticSnapshotV3(t, snapshot)
}

func TestReadCurrentSemanticSnapshotV3RefusesSupersededPointer(t *testing.T) {
	fixture := newSemanticSnapshotV3Fixture(t)
	pointer := fixture.publication.pointer
	pointer.GenerationDigest = fixedDigest("e")
	pointer.RootDigest = fixedDigest("f")
	pointer.Digest = ""
	var err error
	pointer.Digest, err = digestValue(pointer)
	if err != nil || validatePointerV3(pointer) != nil {
		t.Fatalf("replacement pointer = %+v, %v", pointer, err)
	}
	raw, err := json.Marshal(pointer)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(fixture.publication.base, "current.json"), raw); err != nil {
		t.Fatal(err)
	}

	snapshot, err := fixture.publication.readCurrentSemanticSnapshotV3(t.Context())
	if !errors.Is(err, ErrPublishing) {
		t.Fatalf("superseded semantic snapshot error = %v", err)
	}
	assertZeroSemanticSnapshotV3(t, snapshot)
}

func newSemanticSnapshotV3Fixture(t *testing.T) semanticSnapshotV3Fixture {
	t.Helper()
	rootPath := t.TempDir()
	repository := "example.com/acme/v3-semantic-snapshot"
	catalog, generation := relationshipCatalogV3Test(t, repository, 2)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	upstream := relationshipUpstreamV3Test(t, repository)
	resolver := relationshipResolverV3Test(t, repository, upstream)
	rpcValues := []rpccallerposting.Posting{
		rpcPosting(
			"a", "resolved", "grpc", "snapshot.v1.Reader/Get",
			"services/00000/call.go", "",
		),
		rpcPosting(
			"b", "resolved", "grpc", "snapshot.v1.Writer/Put",
			"services/00001/call.go", "",
		),
	}
	rpc := fakeRPC{
		root:   relationshipRPCV3Test(t, repository, resolver.root, upstream, rpcValues),
		values: rpcValues,
	}
	kafka := fakeKafka{
		root: relationshipKafkaV3Test(t, repository, rpc.root.Authority, upstream, nil),
	}
	prepared, err := BuildV3(t.Context(), BuildRequestV3{
		Root: rootPath, Catalog: generation, States: states, ServiceSummary: summary,
		Resolver: resolver, RPC: rpc, Kafka: kafka, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublishV3(t.Context(), prepared, &testPublishPinsV3{}); err != nil {
		t.Fatal(err)
	}
	cache := &CacheV3{}
	publication, err := OpenCurrentV3(t.Context(), rootPath, repository)
	if err != nil {
		t.Fatal(err)
	}
	root := publication.Root()
	if len(root.ServiceMembers) == 0 || len(root.RepositoryMembers) == 0 ||
		root.ServiceCount != 2 || root.ProjectionCount != 2 {
		t.Fatalf("semantic snapshot fixture root = %+v", root)
	}

	projections := make([]Projection, 0, root.ProjectionCount)
	for _, receipt := range root.RepositoryMembers {
		member, openErr := publication.openRepositoryMemberV3(t.Context(), receipt)
		if openErr != nil {
			t.Fatal(openErr)
		}
		for offset := 0; offset < len(member.Fragments); {
			count := member.Fragments[offset].Count
			end := offset + count
			if count < 1 || end > len(member.Fragments) {
				t.Fatalf("invalid fixture fragment range at %d", offset)
			}
			projection, flattenErr := flattenProjectionBucketsV3(member.Fragments[offset:end])
			if flattenErr != nil {
				t.Fatal(flattenErr)
			}
			projections = append(projections, projection)
			offset = end
		}
	}
	return semanticSnapshotV3Fixture{
		rootPath: rootPath, repository: repository, cache: cache,
		publication: publication,
		expected: SemanticSnapshotV3{
			Root: root, Projections: projections,
		},
	}
}

func assertZeroSemanticSnapshotV3(t *testing.T, snapshot SemanticSnapshotV3) {
	t.Helper()
	if !reflect.DeepEqual(snapshot, SemanticSnapshotV3{}) {
		t.Fatalf("failed semantic snapshot leaked content: %+v", snapshot)
	}
}
