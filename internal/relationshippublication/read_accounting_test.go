package relationshippublication

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
)

type relationshipReadAccountingFixture struct {
	rootPath          string
	repository        string
	serviceKey        string
	projectionDigest  string
	publication       *PublicationV3
	repositoryReceipt RepositoryReceiptV3
	serviceReceipt    ServiceRangeReceiptV3
	repositoryAt      int
	serviceAt         int
}

func TestV3MemberVisitAccounting(t *testing.T) {
	t.Run("cache controls and member rereads are charged", func(t *testing.T) {
		fixture := newRelationshipReadAccountingFixture(t)
		cache := &CacheV3{}
		controlCtx, controlLedger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{ControlFileReads: 5},
		)
		if err != nil {
			t.Fatal(err)
		}
		first, err := cache.Acquire(controlCtx, fixture.rootPath, fixture.repository)
		if err != nil {
			t.Fatal(err)
		}
		second, err := cache.Acquire(controlCtx, fixture.rootPath, fixture.repository)
		if err != nil {
			t.Fatal(err)
		}
		controlCounts, err := controlLedger.Finish()
		if err != nil || controlCounts != (readaccounting.Counts{ControlFileReads: 5}) {
			t.Fatalf("cache/control counts = %+v, %v", controlCounts, err)
		}

		want := uint64(2*fixture.serviceReceipt.ServiceCount + 2*fixture.repositoryReceipt.FragmentCount)
		readCtx, readLedger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{ControlFileReads: 4, MemberVisits: want},
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, publication := range []*PublicationV3{first.Publication(), second.Publication()} {
			if _, err := publication.ReadService(readCtx, fixture.serviceKey); err != nil {
				t.Fatal(err)
			}
			if _, err := publication.ReadProjection(readCtx, fixture.projectionDigest); err != nil {
				t.Fatal(err)
			}
		}
		counts, err := readLedger.Finish()
		if err != nil || counts != (readaccounting.Counts{ControlFileReads: 4, MemberVisits: want}) {
			t.Fatalf("member reread counts = %+v, %v", counts, err)
		}
		first.Release()
		second.Release()
	})

	t.Run("member file limit refuses before read", func(t *testing.T) {
		fixture := newRelationshipReadAccountingFixture(t)
		ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.publication.ReadService(ctx, fixture.serviceKey); !errors.Is(err, readaccounting.ErrLimit) {
			t.Fatalf("zero control-read budget = %v", err)
		}
		want := readaccounting.Counts{ControlFileReads: 1}
		if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != want {
			t.Fatalf("member file refusal = %+v, %v", counts, err)
		}
	})

	t.Run("decoded service member rejected later is charged", func(t *testing.T) {
		fixture := newRelationshipReadAccountingFixture(t)
		path := filepath.Join(fixture.publication.directory, fixture.serviceReceipt.Name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, ' ')
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		fixture.publication.rootValue.ServiceMembers[fixture.serviceAt].ContentBytes = int64(len(raw))
		want := uint64(fixture.serviceReceipt.ServiceCount)
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{ControlFileReads: 1, MemberVisits: want},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.publication.ReadService(ctx, fixture.serviceKey); !errors.Is(err, ErrInvalid) {
			t.Fatalf("noncanonical decoded service member = %v", err)
		}
		counts, err := ledger.Finish()
		if err != nil || counts != (readaccounting.Counts{ControlFileReads: 1, MemberVisits: want}) {
			t.Fatalf("rejected service counts = %+v, %v", counts, err)
		}
	})

	t.Run("decoded repository member rejected later is charged", func(t *testing.T) {
		fixture := newRelationshipReadAccountingFixture(t)
		path := filepath.Join(fixture.publication.directory, fixture.repositoryReceipt.Name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, ' ')
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		fixture.publication.rootValue.RepositoryMembers[fixture.repositoryAt].ContentBytes = int64(len(raw))
		want := uint64(fixture.repositoryReceipt.FragmentCount)
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{ControlFileReads: 1, MemberVisits: want},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.publication.ReadProjection(ctx, fixture.projectionDigest); !errors.Is(err, ErrInvalid) {
			t.Fatalf("noncanonical decoded repository member = %v", err)
		}
		counts, err := ledger.Finish()
		if err != nil || counts != (readaccounting.Counts{ControlFileReads: 1, MemberVisits: want}) {
			t.Fatalf("rejected repository counts = %+v, %v", counts, err)
		}
	})

	t.Run("service limit refuses before record delivery", func(t *testing.T) {
		fixture := newRelationshipReadAccountingFixture(t)
		limit := uint64(fixture.serviceReceipt.ServiceCount - 1)
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{ControlFileReads: 1, MemberVisits: limit},
		)
		if err != nil {
			t.Fatal(err)
		}
		record, err := fixture.publication.ReadService(ctx, fixture.serviceKey)
		if !errors.Is(err, readaccounting.ErrLimit) || record.ServiceKey != "" {
			t.Fatalf("limited service read = %+v, %v", record, err)
		}
		counts, finishErr := ledger.Finish()
		if !errors.Is(finishErr, readaccounting.ErrLimit) ||
			counts != (readaccounting.Counts{ControlFileReads: 1, MemberVisits: limit + 1}) {
			t.Fatalf("service limit counts = %+v, %v", counts, finishErr)
		}
	})

	t.Run("repository limit refuses before projection delivery", func(t *testing.T) {
		fixture := newRelationshipReadAccountingFixture(t)
		limit := uint64(fixture.repositoryReceipt.FragmentCount - 1)
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{ControlFileReads: 1, MemberVisits: limit},
		)
		if err != nil {
			t.Fatal(err)
		}
		projection, err := fixture.publication.ReadProjection(ctx, fixture.projectionDigest)
		if !errors.Is(err, readaccounting.ErrLimit) || projection.Digest != "" {
			t.Fatalf("limited projection read = %+v, %v", projection, err)
		}
		counts, finishErr := ledger.Finish()
		if !errors.Is(finishErr, readaccounting.ErrLimit) ||
			counts != (readaccounting.Counts{ControlFileReads: 1, MemberVisits: limit + 1}) {
			t.Fatalf("repository limit counts = %+v, %v", counts, finishErr)
		}
	})
}

func newRelationshipReadAccountingFixture(t *testing.T) relationshipReadAccountingFixture {
	t.Helper()
	rootPath := t.TempDir()
	repository := "example.com/acme/v3-read-accounting"
	catalog, generation := relationshipCatalogV3Test(t, repository, 1)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	upstream := relationshipUpstreamV3Test(t, repository)
	resolver := relationshipResolverV3Test(t, repository, upstream)
	rpcValues := []rpccallerposting.Posting{
		rpcPosting(
			"a", "resolved", "grpc", "read.v1.Reader/Get",
			"services/00000/call.go", "",
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
	publication, err := PublishV3(t.Context(), prepared, &testPublishPinsV3{})
	if err != nil {
		t.Fatal(err)
	}
	root := publication.Root()
	if len(root.RepositoryMembers) != 1 || len(root.ServiceMembers) != 1 {
		t.Fatalf("read-accounting fixture members = repositories %d services %d",
			len(root.RepositoryMembers), len(root.ServiceMembers))
	}
	repositoryMember, err := publication.openRepositoryMemberV3(
		t.Context(), root.RepositoryMembers[0],
	)
	if err != nil || len(repositoryMember.Fragments) == 0 {
		t.Fatalf("read-accounting repository member = %d, %v", len(repositoryMember.Fragments), err)
	}
	return relationshipReadAccountingFixture{
		rootPath: rootPath, repository: repository, serviceKey: "service-00000",
		projectionDigest: repositoryMember.Fragments[0].ProjectionDigest,
		publication:      publication, repositoryReceipt: root.RepositoryMembers[0],
		serviceReceipt: root.ServiceMembers[0],
	}
}
