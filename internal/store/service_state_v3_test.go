package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

type serviceStateV3ReadCounter struct {
	*Surreal
	pointers  atomic.Int64
	roots     atomic.Int64
	members   atomic.Int64
	summaries atomic.Int64
	points    atomic.Int64
	pages     atomic.Int64
	accepted  atomic.Int64
	confirms  atomic.Int64
}

func (counter *serviceStateV3ReadCounter) GetServiceCatalogV3CandidatePointer(
	ctx context.Context, repository string,
) (ServiceCatalogV3Pointer, error) {
	counter.pointers.Add(1)
	return counter.Surreal.GetServiceCatalogV3CandidatePointer(ctx, repository)
}

func (counter *serviceStateV3ReadCounter) ReadServiceCatalogV3Root(
	ctx context.Context, repository, digest string,
) (servicecatalogv3.Root, error) {
	counter.roots.Add(1)
	return counter.Surreal.ReadServiceCatalogV3Root(ctx, repository, digest)
}

func (counter *serviceStateV3ReadCounter) ReadServiceCatalogV3Member(
	ctx context.Context, descriptor servicecatalogv3.MemberDescriptor,
) ([]byte, error) {
	counter.members.Add(1)
	return counter.Surreal.ReadServiceCatalogV3Member(ctx, descriptor)
}

func (counter *serviceStateV3ReadCounter) GetServiceStateV3SummaryPoint(
	ctx context.Context, repository string,
) (servicecatalog.RepositoryState, error) {
	counter.summaries.Add(1)
	return counter.Surreal.GetServiceStateV3SummaryPoint(ctx, repository)
}

func (counter *serviceStateV3ReadCounter) GetServiceStateV3Point(
	ctx context.Context, repository, key string,
) (servicecatalog.ServiceState, error) {
	counter.points.Add(1)
	return counter.Surreal.GetServiceStateV3Point(ctx, repository, key)
}

func (counter *serviceStateV3ReadCounter) ListServiceStateV3Rows(
	ctx context.Context, repository, after string, limit int,
) ([]servicecatalog.ServiceState, error) {
	counter.pages.Add(1)
	return counter.Surreal.ListServiceStateV3Rows(ctx, repository, after, limit)
}

func (counter *serviceStateV3ReadCounter) ListAcceptedServiceStateV3Rows(
	ctx context.Context, repository string, limit int,
) ([]servicecatalog.ServiceState, error) {
	counter.accepted.Add(1)
	return counter.Surreal.ListAcceptedServiceStateV3Rows(ctx, repository, limit)
}

func (counter *serviceStateV3ReadCounter) ConfirmServiceStateV3Snapshot(
	ctx context.Context,
	pointer ServiceCatalogV3Pointer,
	summary servicecatalog.RepositoryState,
) error {
	counter.confirms.Add(1)
	return counter.Surreal.ConfirmServiceStateV3Snapshot(ctx, pointer, summary)
}

func newServiceStateV3CountingReader(
	t *testing.T, s *Surreal,
) (*serviceStateV3ReadCounter, *ServiceStateV3Reader, *servicecatalogv3.ReadCache) {
	t.Helper()
	counter := &serviceStateV3ReadCounter{Surreal: s}
	cache := servicecatalogv3.NewDefaultReadCache()
	reader, err := NewServiceStateV3Reader(counter, cache)
	if err != nil {
		t.Fatal(err)
	}
	return counter, reader, cache
}

func TestServiceStateV3ReconcileActivationAndSuccessorFence(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3"
	commit := strings.Repeat("7", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	first := serviceStateV3Generation(t, repository, commit, "a", []servicecatalog.Service{{
		Key: "orders", DisplayName: "Orders", Disposition: servicecatalog.DispositionAccepted,
		Origin: servicecatalog.OriginBase,
	}})
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || reconcile.Noop || reconcile.Plan == nil {
		t.Fatalf("begin reconcile = %+v, %v", reconcile, err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || summary.UnavailableCount != 1 || summary.CurrentCount != 0 {
		t.Fatalf("reconciled summary = %+v, %v", summary, err)
	}
	search := "sha256:" + strings.Repeat("9", 64)
	activation, err := s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil || activation.Noop || activation.Plan == nil {
		t.Fatalf("begin activation = %+v, %v", activation, err)
	}
	runServiceStateV3Plan(t, s, activation)
	summary, err = s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || summary.CurrentCount != 1 || summary.UnavailableCount != 0 {
		t.Fatalf("activated summary = %+v, %v", summary, err)
	}
	noop, err := s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil || !noop.Noop || noop.Plan != nil || noop.Schedule != nil {
		t.Fatalf("activation no-op = %+v, %v", noop, err)
	}
	report, err := s.ValidateServiceCatalogV3Precious(ctx)
	if err != nil || report.StateRows != 1 || report.StateSummaries != 1 || report.StatePlans != 2 {
		t.Fatalf("precious state report = %+v, %v", report, err)
	}

	second := serviceStateV3Generation(t, repository, commit, "b", []servicecatalog.Service{{
		Key: "orders", DisplayName: "Orders B", Disposition: servicecatalog.DispositionAccepted,
		Origin: servicecatalog.OriginBase,
	}})
	if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
		t.Fatal(err)
	}
	unsettled, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || unsettled.Plan == nil {
		t.Fatalf("begin successor = %+v, %v", unsettled, err)
	}
	third := serviceStateV3Generation(t, repository, commit, "c", []servicecatalog.Service{{
		Key: "orders", DisplayName: "Orders C", Disposition: servicecatalog.DispositionAccepted,
		Origin: servicecatalog.OriginBase,
	}})
	if err := s.PublishServiceCatalogV3Candidate(ctx, third); !errors.Is(err, ErrConflict) {
		t.Fatalf("unsettled successor publication = %v", err)
	}
}

func TestServiceStateV3PreciousRejectsCrossRepositoryCatalogRoots(t *testing.T) {
	t.Run("current and snapshot rows", func(t *testing.T) {
		s, repository, otherRoot, summary := newServiceStateV3ProvenanceFixture(t)
		ctx := t.Context()
		rowResults, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_current
	WHERE repository = $repository ORDER BY service_key`, map[string]any{
			"repository": repository,
		})
		if err != nil {
			t.Fatal(err)
		}
		rows := firstDomainRows(rowResults)
		if len(rows) != 1 {
			t.Fatalf("current rows = %+v", rows)
		}
		original, err := serviceStateV3FromRec(rows[0])
		if err != nil {
			t.Fatal(err)
		}
		replaceCurrent := func(state servicecatalog.ServiceState) {
			t.Helper()
			content := serviceStateContent(state)
			content["visible_from"] = rows[0].VisibleFrom
			if _, updateErr := surrealdb.Query[any](ctx, s.db,
				"UPDATE $rid CONTENT $content RETURN NONE",
				map[string]any{"rid": *rows[0].RecID, "content": content},
			); updateErr != nil {
				t.Fatal(updateErr)
			}
		}
		assertRejected := func(label string) {
			t.Helper()
			if report, validateErr := s.ValidateServiceCatalogV3Precious(ctx); !errors.Is(validateErr, ErrInvalidServiceStateV3) {
				t.Fatalf("%s = %+v, %v", label, report, validateErr)
			}
		}

		crossDesired := *original
		crossDesired.DesiredCatalogGeneration = otherRoot
		if err := servicecatalogv3.SetServiceStateDigest(&crossDesired); err != nil {
			t.Fatal(err)
		}
		replaceCurrent(crossDesired)
		assertRejected("cross-repository current desired root")
		replaceCurrent(*original)

		crossActive := *original
		crossActive.ActiveCatalogGeneration = otherRoot
		if err := servicecatalogv3.SetServiceStateDigest(&crossActive); err != nil {
			t.Fatal(err)
		}
		replaceCurrent(crossActive)
		assertRejected("cross-repository current active root")
		replaceCurrent(*original)

		crossSummary := summary
		crossSummary.CatalogGeneration = otherRoot
		if err := servicecatalogv3.SetRepositoryStateDigest(&crossSummary); err != nil {
			t.Fatal(err)
		}
		if _, err := surrealdb.Query[any](ctx, s.db,
			"UPDATE $rid CONTENT $content RETURN NONE",
			map[string]any{
				"rid":     serviceStateV3RepositoryID(repository),
				"content": serviceRepositoryStateContent(crossSummary),
			},
		); err != nil {
			t.Fatal(err)
		}
		assertRejected("cross-repository current summary root")
		if _, err := surrealdb.Query[any](ctx, s.db,
			"UPDATE $rid CONTENT $content RETURN NONE",
			map[string]any{
				"rid":     serviceStateV3RepositoryID(repository),
				"content": serviceRepositoryStateContent(summary),
			},
		); err != nil {
			t.Fatal(err)
		}

		preimageSummaryContent := serviceRepositoryStateContent(summary)
		preimageSummaryContent["snapshot_revision"] = summary.ControlRevision
		preimageSummaryContent["snapshot_digest"] = summary.SummaryDigest
		if _, err := surrealdb.Query[any](ctx, s.db, `
CREATE service_state_v3_repository_preimage CONTENT $content RETURN NONE`, map[string]any{
			"content": preimageSummaryContent,
		}); err != nil {
			t.Fatal(err)
		}
		preimageContent := serviceStateContent(*original)
		preimageContent["visible_from"] = rows[0].VisibleFrom
		preimageContent["snapshot_revision"] = summary.ControlRevision
		preimageContent["snapshot_digest"] = summary.SummaryDigest
		if _, err := surrealdb.Query[any](ctx, s.db, `
CREATE service_state_v3_preimage CONTENT $content RETURN NONE`, map[string]any{
			"content": preimageContent,
		}); err != nil {
			t.Fatal(err)
		}
		preimageResults, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_preimage WHERE repository = $repository`, map[string]any{
			"repository": repository,
		})
		if err != nil {
			t.Fatal(err)
		}
		preimages := firstDomainRows(preimageResults)
		if len(preimages) != 1 {
			t.Fatalf("preimage rows = %+v", preimages)
		}
		replacePreimage := func(state servicecatalog.ServiceState, snapshotDigest string) {
			t.Helper()
			content := serviceStateContent(state)
			content["visible_from"] = rows[0].VisibleFrom
			content["snapshot_revision"] = summary.ControlRevision
			content["snapshot_digest"] = snapshotDigest
			if _, updateErr := surrealdb.Query[any](ctx, s.db,
				"UPDATE $rid CONTENT $content RETURN NONE",
				map[string]any{"rid": *preimages[0].RecID, "content": content},
			); updateErr != nil {
				t.Fatal(updateErr)
			}
		}

		replacePreimage(crossDesired, summary.SummaryDigest)
		assertRejected("cross-repository preimage desired root")
		replacePreimage(*original, summary.SummaryDigest)
		replacePreimage(crossActive, summary.SummaryDigest)
		assertRejected("cross-repository preimage active root")
		replacePreimage(*original, summary.SummaryDigest)

		crossPreimageSummary := summary
		crossPreimageSummary.CatalogGeneration = otherRoot
		if err := servicecatalogv3.SetRepositoryStateDigest(&crossPreimageSummary); err != nil {
			t.Fatal(err)
		}
		crossPreimageSummaryContent := serviceRepositoryStateContent(crossPreimageSummary)
		crossPreimageSummaryContent["snapshot_revision"] = summary.ControlRevision
		crossPreimageSummaryContent["snapshot_digest"] = crossPreimageSummary.SummaryDigest
		if _, err := surrealdb.Query[any](ctx, s.db, `
UPDATE service_state_v3_repository_preimage CONTENT $content RETURN NONE`, map[string]any{
			"content": crossPreimageSummaryContent,
		}); err != nil {
			t.Fatal(err)
		}
		replacePreimage(*original, crossPreimageSummary.SummaryDigest)
		assertRejected("cross-repository preimage summary root")
	})

	t.Run("plan", func(t *testing.T) {
		s, repository, otherRoot, summary := newServiceStateV3ProvenanceFixture(t)
		ctx := t.Context()
		other, err := s.GetServiceCatalogV3CandidatePointer(
			ctx, "example.com/acme/service-state-v3-provenance-b",
		)
		if err != nil || other.RootDigest != otherRoot {
			t.Fatalf("other pointer = %+v, %v", other, err)
		}
		planDigest := serviceStateV3PlanDigest(
			repository, serviceStateV3Reconcile, otherRoot,
			other.ControlRevision, "", 0,
		)
		schedule, err := s.EnqueueGenerationSchedule(ctx, GenerationScheduleSpec{
			Repository: repository, Stage: ServiceStateV3ReconcileStage,
			Generation: planDigest, ResourceClass: GenerationResourceCPU,
			TotalItems: 1, ChunkItems: 1, MaxAttempts: MaxGenerationAttempts,
			RepositoryTokens: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		plan := ServiceStateV3Plan{
			Schema: serviceStateV3PlanSchema, Digest: planDigest,
			Repository: repository, Phase: serviceStateV3Reconcile,
			CatalogRoot: otherRoot, CatalogControlRevision: other.ControlRevision,
			ScheduleDigest: schedule.Digest, State: serviceStateV3Running,
			TotalChunks: 1, CatalogServiceCount: 1, LiveServiceCount: 1,
			CurrentCount: 1, SummaryControlRevision: summary.ControlRevision,
			SummaryDigest: summary.SummaryDigest, CreatedAt: now, UpdatedAt: now,
		}
		if err := validateServiceStateV3Plan(plan); err != nil {
			t.Fatalf("cross-repository plan fixture: %v", err)
		}
		if _, err := surrealdb.Query[any](ctx, s.db,
			"CREATE $rid CONTENT $content RETURN NONE",
			map[string]any{
				"rid":     serviceStateV3PlanID(plan.Digest),
				"content": serviceStateV3PlanContent(plan),
			},
		); err != nil {
			t.Fatal(err)
		}
		if report, err := s.ValidateServiceCatalogV3Precious(ctx); !errors.Is(err, ErrInvalidServiceStateV3) {
			t.Fatalf("cross-repository plan root = %+v, %v", report, err)
		}
	})

	t.Run("state reference", func(t *testing.T) {
		s, repository, otherRoot, _ := newServiceStateV3ProvenanceFixture(t)
		if _, err := surrealdb.Query[any](t.Context(), s.db, `
CREATE service_catalog_v3_state_reference:cross_repository CONTENT {
	repository: $repository, root_digest: $root_digest, kind: 'current',
	service_key: '', state_root_digest: '', recorded_at: time::now()
} RETURN NONE`, map[string]any{
			"repository": repository, "root_digest": otherRoot,
		}); err != nil {
			t.Fatal(err)
		}
		if report, err := s.ValidateServiceCatalogV3Precious(t.Context()); !errors.Is(err, ErrInvalidServiceCatalogV3Lifecycle) {
			t.Fatalf("cross-repository state reference root = %+v, %v", report, err)
		}
	})
}

func newServiceStateV3ProvenanceFixture(
	t *testing.T,
) (*Surreal, string, string, servicecatalog.RepositoryState) {
	t.Helper()
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	const repository = "example.com/acme/service-state-v3-provenance-a"
	const otherRepository = "example.com/acme/service-state-v3-provenance-b"
	commit := strings.Repeat("7", 40)
	for _, name := range []string{repository, otherRepository} {
		seedServiceCatalogV3Repo(t, s, name, commit)
	}
	generation := serviceStateV3Generation(
		t, repository, commit, "provenance-a", []servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
	)
	otherGeneration := serviceStateV3Generation(
		t, otherRepository, commit, "provenance-b", []servicecatalog.Service{{
			Key: "other", DisplayName: "Other",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
	)
	for _, candidate := range []servicecatalogv3.Generation{generation, otherGeneration} {
		if err := s.PublishServiceCatalogV3Candidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	activation, err := s.BeginServiceStateV3Activation(
		ctx, repository, "sha256:"+strings.Repeat("9", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)
	summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := s.ValidateServiceCatalogV3Precious(ctx); err != nil {
		t.Fatalf("baseline precious = %+v, %v", report, err)
	}
	return s, repository, otherGeneration.Root.Digest, *summary
}

func runServiceStateV3Plan(t *testing.T, s *Surreal, begin ServiceStateV3Begin) {
	t.Helper()
	ctx := t.Context()
	schedule := begin.Schedule
	for schedule.NextOffset < schedule.TotalItems {
		var err error
		schedule, err = s.ExpandGenerationSchedule(
			ctx, schedule.Repository, schedule.Stage, schedule.Generation,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	for {
		chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "service-state-v3-test")
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.ProcessServiceStateV3Chunk(ctx, *chunk)
		if err != nil {
			t.Fatalf("process chunk offset %d: %v", chunk.Offset, err)
		}
		if err := s.CompleteGenerationChunk(ctx, *chunk); err != nil {
			t.Fatalf("complete chunk offset %d: %v", chunk.Offset, err)
		}
		settled, err := s.GetGenerationSchedule(ctx, schedule.Repository, schedule.Stage)
		if err != nil {
			t.Fatal(err)
		}
		if settled.Status == GenerationScheduleSettled {
			return
		}
	}
}

func serviceStateV3Generation(
	t *testing.T,
	repository, commit, version string,
	services []servicecatalog.Service,
) servicecatalogv3.Generation {
	t.Helper()
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "service-state-v3", Version: version,
	}
	memberships := make([]servicecatalog.Membership, 0, len(services))
	for index, service := range services {
		if service.Disposition == servicecatalog.DispositionRejected {
			continue
		}
		memberships = append(memberships, servicecatalog.Membership{
			ServiceKey: service.Key, Path: fmt.Sprintf("svc/%05d", index),
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: repository,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceOperator, Path: "/catalog.json", Commit: commit,
			CensusDigest: "sha256:" + strings.Repeat("c", 64),
			FileCount:    1, AcceptedFileCount: 1,
		},
		Authority: authority,
	}, servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services: services, Memberships: memberships,
	})
	if err != nil {
		t.Fatal(err)
	}
	return generation
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

func serviceStateV2Publication(
	t *testing.T,
	repository, commit string,
) servicecatalog.Publication {
	t.Helper()
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "service-state-v2", Version: "v1",
	}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: "V2 Orders",
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "orders", Path: "svc", Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		}},
		Unowned: []servicecatalog.UnownedPlacement{},
	}
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest, err := servicecatalog.Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication := servicecatalog.Publication{
		Schema: servicecatalog.PublicationSchema, Repository: repository,
		SourceKind: servicecatalog.SourceOperator, SourcePath: "/catalog-v2.json",
		SourceCommit:       commit,
		SourceCensusDigest: "sha256:" + strings.Repeat("4", 64),
		SourceFileCount:    1, AcceptedFileCount: 1,
		Authority: authority, CatalogDigest: catalogDigest, Canonical: canonical,
	}
	publication.GenerationDigest, err = servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := servicecatalog.ValidatePublication(publication, false); err != nil {
		t.Fatal(err)
	}
	return publication
}

func TestServiceStateV3SchemaMigrationIdempotent(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	results, err := surrealdb.Query[any](t.Context(), s.db, `
DELETE $marker;
DEFINE FIELD OVERWRITE max_chunk_rows ON service_state_v3_plan TYPE string;`, map[string]any{
		"marker": serviceStateV3SchemaMigrationID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if result.Error != nil {
			t.Fatal(result.Error.Message)
		}
	}
	if err := s.migrateServiceStateV3Schema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.getRawServiceStateV3Summary(t.Context(), "example.com/acme/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing v3 summary = %v", err)
	}
}

func TestServiceStateV3SnapshotSchemaMigrationBackfillsVisibleRevision(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-snapshot-migration"
	commit := strings.Repeat("5", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	generation := serviceStateV3Generation(
		t,
		repository,
		commit,
		"snapshot-migration",
		[]servicecatalog.Service{{
			Key:         "orders",
			DisplayName: "Orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
	)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	results, err := surrealdb.Query[any](ctx, s.db, `
DELETE $marker;
UPDATE $compatibility SET version = $prior RETURN NONE;
DEFINE FIELD OVERWRITE visible_from ON service_state_v3_current TYPE option<int>;
UPDATE service_state_v3_current UNSET visible_from;`, map[string]any{
		"marker":        serviceStateV3SnapshotSchemaMigrationID(),
		"compatibility": candidateControlRevisionMigrationID(),
		"prior":         serviceRuntimeSelectorCompatibilityMigrationVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if result.Error != nil {
			t.Fatal(result.Error.Message)
		}
	}
	if err := s.migrateServiceStateV3SnapshotSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateServiceStateV3SnapshotSchema(ctx); err != nil {
		t.Fatalf("idempotent snapshot migration: %v", err)
	}
	version := serviceRuntimeCompatibilityMarker(t, s)
	if version != serviceStateV3SnapshotCompatibilityMigrationVersion ||
		version == candidateControlRevisionMigrationVersion ||
		version == serviceRuntimeSelectorCompatibilityMigrationVersion {
		t.Fatalf("snapshot compatibility latch = %q", version)
	}
	rows, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_current WHERE repository = $repository`, map[string]any{
		"repository": repository,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := firstDomainRows(rows)
	if len(got) != 1 || got[0].VisibleFrom != 1 {
		t.Fatalf("migrated visible revision = %+v", got)
	}

	broken, err := surrealdb.Query[any](ctx, s.db, `
DELETE $marker;
UPDATE $compatibility SET version = $prior RETURN NONE;`, map[string]any{
		"marker":        serviceStateV3SnapshotSchemaMigrationID(),
		"compatibility": candidateControlRevisionMigrationID(),
		"prior":         serviceRuntimeSelectorCompatibilityMigrationVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *broken {
		if result.Error != nil {
			t.Fatal(result.Error.Message)
		}
	}
	if err := s.migrateServiceStateV3SnapshotSchemaWithDefinition(
		ctx,
		"THROW 'forced snapshot schema failure';",
	); err == nil {
		t.Fatal("forced snapshot schema failure unexpectedly completed migration")
	}
	if version := serviceRuntimeCompatibilityMarker(t, s); version != serviceStateV3SnapshotCompatibilityMigrationVersion {
		t.Fatalf("failed schema migration did not preserve compatibility latch: %q", version)
	}
}

func TestServiceCatalogV3SourceGenerationCompatibilityMigration(t *testing.T) {
	t.Run("snapshot advances and self is idempotent", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		if _, err := surrealdb.Query[any](t.Context(), s.db, `
UPDATE $rid SET version = $version RETURN NONE`, map[string]any{
			"rid":     candidateControlRevisionMigrationID(),
			"version": serviceStateV3SnapshotCompatibilityMigrationVersion,
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.migrateServiceSourceGenerationCompatibility(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := s.migrateServiceSourceGenerationCompatibility(t.Context()); err != nil {
			t.Fatalf("idempotent source-generation compatibility migration: %v", err)
		}
		if version := serviceRuntimeCompatibilityMarker(t, s); version != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
			t.Fatalf("source-generation compatibility marker = %q", version)
		}
	})

	t.Run("snapshot migration preserves latest", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		if err := s.migrateServiceStateV3SnapshotSchema(t.Context()); err != nil {
			t.Fatalf("snapshot migration rejected latest compatibility marker: %v", err)
		}
		if version := serviceRuntimeCompatibilityMarker(t, s); version != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
			t.Fatalf("snapshot migration downgraded compatibility marker to %q", version)
		}
	})

	t.Run("unknown refuses without mutation", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		const unknown = "t41.10-service-catalog-v3-source-generation-compat-v999"
		if _, err := surrealdb.Query[any](t.Context(), s.db, `
UPDATE $rid SET version = $version RETURN NONE`, map[string]any{
			"rid":     candidateControlRevisionMigrationID(),
			"version": unknown,
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.migrateServiceSourceGenerationCompatibility(t.Context()); err == nil {
			t.Fatal("unknown source-generation compatibility marker was accepted")
		}
		if version := serviceRuntimeCompatibilityMarker(t, s); version != unknown {
			t.Fatalf("unknown compatibility marker changed to %q", version)
		}
	})
}

func TestServiceStateV3LeavesV1V2AuthorityByteIdentical(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-isolation"
	commit := strings.Repeat("6", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	v2 := serviceStateV2Publication(t, repository, commit)
	if err := s.PublishServiceCatalog(ctx, v2); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileServiceStates(ctx, *current); err != nil {
		t.Fatal(err)
	}
	beforeCatalog, _ := s.GetServiceCatalog(ctx, repository)
	beforeState, _ := s.GetServiceState(ctx, repository, "orders")
	beforeSummary, _ := s.GetServiceStateSummary(ctx, repository)

	v3 := serviceStateV3Generation(t, repository, commit, "isolation", []servicecatalog.Service{{
		Key: "orders", DisplayName: "V3 Orders",
		Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
	}})
	if err := s.PublishServiceCatalogV3Candidate(ctx, v3); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	activation, err := s.BeginServiceStateV3Activation(
		ctx, repository, "sha256:"+strings.Repeat("5", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)

	afterCatalog, _ := s.GetServiceCatalog(ctx, repository)
	afterState, _ := s.GetServiceState(ctx, repository, "orders")
	afterSummary, _ := s.GetServiceStateSummary(ctx, repository)
	if !reflect.DeepEqual(afterCatalog, beforeCatalog) ||
		!reflect.DeepEqual(afterState, beforeState) ||
		!reflect.DeepEqual(afterSummary, beforeSummary) {
		t.Fatal("v3 reconcile or activation changed v1/v2 catalog/state authority")
	}
}

func TestServiceStateV3ChunkFailureRollsBackRowsAndPlan(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-rollback"
	commit := strings.Repeat("3", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	generation := serviceStateV3Generation(t, repository, commit, "rollback", []servicecatalog.Service{
		{Key: "orders", DisplayName: "Orders", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
		{Key: "users", DisplayName: "Users", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
	})
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, begin)
	results, err := surrealdb.Query[any](ctx, s.db, `
DEFINE EVENT service_state_v3_rollback_trap ON TABLE service_state_v3_current
	WHEN $event != 'DELETE' AND $after.service_key = 'users'
	THEN { THROW 'phebs-permanent: injected service state v3 chunk failure' };`, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if result.Error != nil {
			t.Fatal(result.Error.Message)
		}
	}
	chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessServiceStateV3Chunk(ctx, *chunk); err == nil ||
		!strings.Contains(err.Error(), "injected service state v3 chunk failure") {
		t.Fatalf("injected chunk failure = %v", err)
	}
	countResults, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](ctx, s.db, `RETURN [{ count: array::len(
		SELECT id FROM service_state_v3_current WHERE repository = $repository
	) }];`, map[string]any{"repository": repository})
	if err != nil {
		t.Fatal(err)
	}
	counts := firstDomainRows(countResults)
	plan, err := s.getServiceStateV3Plan(ctx, begin.Plan.Digest)
	if err != nil || len(counts) != 1 || counts[0].Count != 0 || plan.NextChunk != 0 {
		t.Fatalf("rolled-back chunk rows=%+v plan=%+v err=%v", counts, plan, err)
	}
}

func TestServiceStateV3ChunkRefusesDeletingRepository(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-deleting"
	commit := strings.Repeat("4", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	generation := serviceStateV3Generation(
		t, repository, commit, "deleting", []servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
	)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, begin)
	chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "deleting")
	if err != nil {
		t.Fatal(err)
	}
	shadow, err := s.EnqueueGenerationSchedule(ctx, GenerationScheduleSpec{
		Repository: repository, Stage: ServiceRelationshipV3ScheduleStage,
		Generation:    "sha256:" + strings.Repeat("e", 64),
		ResourceClass: GenerationResourceMemory, TotalItems: 1, ChunkItems: 1,
		MaxAttempts: MaxGenerationAttempts, RepositoryTokens: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExpandGenerationSchedule(
		ctx, repository, ServiceRelationshipV3ScheduleStage, shadow.Generation,
	); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoDeleting(ctx, repository, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessServiceStateV3Chunk(ctx, *chunk); err == nil {
		t.Fatal("deleting repository accepted a service-state v3 chunk")
	}
	plan, err := s.getServiceStateV3Plan(ctx, begin.Plan.Digest)
	if err != nil || plan.NextChunk != 0 {
		t.Fatalf("deleting repository advanced plan = %+v, %v", plan, err)
	}
	rows, err := s.ListServiceStateV3Rows(ctx, repository, "", 1)
	if err != nil || len(rows) != 0 {
		t.Fatalf("deleting repository wrote state rows = %+v, %v", rows, err)
	}
	if err := s.DeleteRepo(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetGenerationSchedule(
		ctx, repository, ServiceRelationshipV3ScheduleStage,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted repository retained current v3 relationship schedule: %v", err)
	}
	retiredShadow, err := s.generationScheduleByDigest(ctx, shadow.Digest)
	if err != nil || retiredShadow.Status != GenerationScheduleSuperseded {
		t.Fatalf("deleted repository shadow schedule = %+v, %v", retiredShadow, err)
	}
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessServiceStateV3Chunk(ctx, *chunk); err == nil {
		t.Fatal("recreated repository accepted a pre-deletion chunk")
	}
	plan, err = s.getServiceStateV3Plan(ctx, begin.Plan.Digest)
	if err != nil || plan.State != serviceStateV3Superseded || plan.NextChunk != 0 {
		t.Fatalf("retired deletion plan = %+v, %v", plan, err)
	}
	fresh, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || fresh.Plan == nil || fresh.Schedule == nil ||
		fresh.Plan.Repair != begin.Plan.Repair+1 ||
		fresh.Plan.Digest == begin.Plan.Digest ||
		fresh.Schedule.Digest == begin.Schedule.Digest ||
		fresh.Schedule.Status != GenerationScheduleActive {
		t.Fatalf("fresh post-deletion reconcile = %+v, %v", fresh, err)
	}
	if _, err := s.ProcessServiceStateV3Chunk(ctx, *chunk); err == nil {
		t.Fatal("fresh post-deletion schedule accepted a pre-deletion chunk")
	}
	if _, err := s.ClaimGenerationChunk(
		ctx, GenerationResourceMemory, "recreated-shadow",
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fresh repository exposed pre-deletion shadow work: %v", err)
	}
	rows, err = s.ListServiceStateV3Rows(ctx, repository, "", 1)
	if err != nil || len(rows) != 0 {
		t.Fatalf("pre-deletion chunk changed recreated state = %+v, %v", rows, err)
	}
}

func TestServiceStateV3PlanUsesGenerationScheduleLifecycle(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-lifecycle"
	commit := strings.Repeat("2", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	for index := range 4 {
		generation := serviceStateV3Generation(
			t, repository, commit, fmt.Sprintf("lifecycle-%d", index),
			[]servicecatalog.Service{{
				Key: "orders", DisplayName: fmt.Sprintf("Orders %d", index),
				Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
			}},
		)
		if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
			t.Fatal(err)
		}
		begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
		if err != nil {
			t.Fatal(err)
		}
		runServiceStateV3Plan(t, s, begin)
	}
	if count := serviceStateV3PlanCount(t, s); count != 4 {
		t.Fatalf("plan count before lifecycle = %d", count)
	}
	cursor := ""
	for range 16 {
		sweep, err := s.SweepGenerationScheduleLifecycle(ctx, cursor, 16, 64, 3)
		if err != nil {
			t.Fatal(err)
		}
		cursor = sweep.Cursor
		if serviceStateV3PlanCount(t, s) == 3 {
			break
		}
	}
	if count := serviceStateV3PlanCount(t, s); count != 3 {
		t.Fatalf("plan count after lifecycle = %d", count)
	}
}

func TestServiceStateV3CatalogSuccessorSupersedesActivationLease(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-activation-supersede"
	commit := strings.Repeat("1", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	service := func(display string) []servicecatalog.Service {
		return []servicecatalog.Service{{
			Key: "orders", DisplayName: display,
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}}
	}
	first := serviceStateV3Generation(t, repository, commit, "a", service("Orders A"))
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	activation, err := s.BeginServiceStateV3Activation(
		ctx, repository, "sha256:"+strings.Repeat("2", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, activation)
	claimed, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "superseded-activation")
	if err != nil {
		t.Fatal(err)
	}
	second := serviceStateV3Generation(t, repository, commit, "b", service("Orders B"))
	if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessServiceStateV3Chunk(ctx, *claimed); err == nil {
		t.Fatal("superseded activation lease mutated the successor catalog state")
	}
	plan, err := s.getServiceStateV3Plan(ctx, activation.Plan.Digest)
	if err != nil || plan.State != serviceStateV3Superseded {
		t.Fatalf("superseded activation plan = %+v, %v", plan, err)
	}
	if _, _, err := s.currentServiceStateV3Plan(ctx, repository, serviceStateV3Activate); !errors.Is(err, ErrNotFound) && err != nil {
		t.Fatalf("superseded activation current = %v", err)
	}
	if _, err := s.GetServiceStateV3Summary(ctx, repository); err == nil {
		t.Fatal("catalog successor exposed the prior summary")
	}
}

func TestServiceStateV3CrashReplay(t *testing.T) {
	ctx := t.Context()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	dataDir := t.TempDir()
	s, err := OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close(context.Background())
		}
	})
	repository := "example.com/acme/service-state-v3-replay"
	commit := strings.Repeat("8", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	services := make([]servicecatalog.Service, 600)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key:         fmt.Sprintf("service-%04d", index),
			DisplayName: fmt.Sprintf("Service %04d", index),
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}
	}
	generation := serviceStateV3Generation(t, repository, commit, "repair", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, begin)
	first, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "crash-replay")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ProcessServiceStateV3Chunk(ctx, *first)
	if err != nil || result.Applied == 0 {
		t.Fatalf("pre-crash apply = %+v, %v", result, err)
	}
	if _, err := s.GetServiceStateV3Summary(ctx, repository); err == nil {
		t.Fatal("partial reconcile exposed a strict v3 summary")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	s = nil
	s, err = OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairServiceCatalogV3Startup(ctx); err != nil {
		t.Fatal(err)
	}
	persisted, err := s.generationChunkByIdentity(ctx, first.Identity)
	if err != nil || persisted.Status != GenerationChunkRunning || persisted.HeartbeatAt == nil {
		t.Fatalf("persisted crashed service-state lease = %+v, %v", persisted, err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		"UPDATE $chunk SET heartbeat_at = $old RETURN NONE", map[string]any{
			"chunk": generationChunkRecordID(*persisted),
			"old":   time.Now().UTC().Add(-time.Hour),
		}); err != nil {
		t.Fatal(err)
	}
	if reaped, err := s.ReapStaleGenerationChunks(
		ctx, GenerationResourceCPU, time.Minute,
	); err != nil || reaped != 1 {
		t.Fatalf("reap crashed service-state lease = %d, %v", reaped, err)
	}
	if stale, err := s.ProcessServiceStateV3Chunk(ctx, *first); err != nil || stale.Applied != 0 {
		t.Fatalf("crashed service-state worker replay mutated rows: %+v, %v", stale, err)
	}
	if err := s.CompleteGenerationChunk(ctx, *first); !errors.Is(err, ErrGenerationLeaseLost) {
		t.Fatalf("crashed service-state worker retained completion authority: %v", err)
	}
	replayed := false
	for {
		chunk, claimErr := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "crash-replay")
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		result, err = s.ProcessServiceStateV3Chunk(ctx, *chunk)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteGenerationChunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
		if chunk.Offset == first.Offset {
			if chunk.ID != first.ID || chunk.LeaseToken == first.LeaseToken {
				t.Fatalf("replayed chunk lease = %+v, prior %+v", chunk, first)
			}
			if result.Applied != 0 {
				t.Fatalf("replayed chunk applied rows = %+v", result)
			}
			replayed = true
		}
		schedule, scheduleErr := s.GetGenerationSchedule(
			ctx, begin.Schedule.Repository, begin.Schedule.Stage,
		)
		if scheduleErr != nil {
			t.Fatal(scheduleErr)
		}
		if schedule.Status == GenerationScheduleSettled {
			break
		}
	}
	if !replayed {
		t.Fatal("released committed chunk was not replayed")
	}
	summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || summary.LiveServiceCount != len(services) {
		t.Fatalf("replayed summary = %+v, %v", summary, err)
	}
}

func TestServiceStateV3TerminalHandoffCanRetryBeforeLeaseSettlement(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-handoff-retry"
	commit := strings.Repeat("9", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	generation := serviceStateV3Generation(
		t, repository, commit, "handoff-retry", []servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
	)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, begin)
	var terminal *GenerationChunk
	for terminal == nil {
		chunk, claimErr := s.ClaimGenerationChunk(
			ctx, GenerationResourceCPU, "handoff-first",
		)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		result, processErr := s.ProcessServiceStateV3Chunk(ctx, *chunk)
		if processErr != nil {
			t.Fatal(processErr)
		}
		if result.Settled {
			terminal = chunk
			break
		}
		if completeErr := s.CompleteGenerationChunk(ctx, *chunk); completeErr != nil {
			t.Fatal(completeErr)
		}
	}
	running, err := s.generationChunkByIdentity(ctx, terminal.Identity)
	if err != nil || running.Status != GenerationChunkRunning || running.LeaseToken == "" {
		t.Fatalf("terminal lease after state apply = %+v, %v", running, err)
	}
	noop, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || !noop.Noop {
		t.Fatalf("terminal active plan = %+v, %v", noop, err)
	}
	active, err := s.GetGenerationSchedule(ctx, repository, terminal.Stage)
	if err != nil || active.Status != GenerationScheduleActive || active.Running != 1 {
		t.Fatalf("terminal schedule before handoff = %+v, %v", active, err)
	}
	if _, err := s.RetryGenerationChunk(
		ctx, *terminal, "injected runtime handoff failure", time.Now().UTC().Add(-time.Second),
	); err != nil {
		t.Fatal(err)
	}
	retry, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "handoff-retry")
	if err != nil || retry.Offset != terminal.Offset || retry.Attempt != terminal.Attempt+1 {
		t.Fatalf("handoff retry lease = %+v, %v", retry, err)
	}
	result, err := s.ProcessServiceStateV3Chunk(ctx, *retry)
	if err != nil || !result.Settled || result.Applied != 0 {
		t.Fatalf("handoff retry result = %+v, %v", result, err)
	}
	if err := s.CompleteGenerationChunk(ctx, *retry); err != nil {
		t.Fatal(err)
	}
	settled, err := s.GetGenerationSchedule(ctx, repository, retry.Stage)
	if err != nil || settled.Status != GenerationScheduleSettled {
		t.Fatalf("settled retry schedule = %+v, %v", settled, err)
	}
	noop, err = s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || !noop.Noop {
		t.Fatalf("settled terminal plan = %+v, %v", noop, err)
	}
	if _, err := s.GetGenerationSchedule(ctx, repository, retry.Stage); !errors.Is(err, ErrNotFound) {
		t.Fatalf("settled terminal schedule remained current: %v", err)
	}
}

func TestServiceStateV3TerminalRepairContinues(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-repair"
	commit := strings.Repeat("b", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	services := make([]servicecatalog.Service, 600)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%04d", index), DisplayName: fmt.Sprintf("Service %04d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	generation := serviceStateV3Generation(t, repository, commit, "repair", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, begin)
	first, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "repair-first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessServiceStateV3Chunk(ctx, *first); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteGenerationChunk(ctx, *first); err != nil {
		t.Fatal(err)
	}
	for {
		schedule, scheduleErr := s.GetGenerationSchedule(ctx, repository, ServiceStateV3ReconcileStage)
		if scheduleErr != nil {
			t.Fatal(scheduleErr)
		}
		if schedule.Status == GenerationScheduleSettled {
			break
		}
		chunk, claimErr := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "terminal-failure")
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if failErr := s.FailGenerationChunk(ctx, *chunk, "injected terminal failure"); failErr != nil {
			t.Fatal(failErr)
		}
	}
	repair, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || repair.Plan == nil || repair.Plan.Repair != 1 ||
		repair.Plan.BaseChunk != 1 || repair.Plan.NextChunk != 1 {
		t.Fatalf("repair plan = %+v, %v", repair, err)
	}
	runServiceStateV3Plan(t, s, repair)
	summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || summary.LiveServiceCount != len(services) ||
		summary.UnavailableCount != len(services) {
		t.Fatalf("repaired summary = %+v, %v", summary, err)
	}
}

func TestServiceStateV3RemovalReaddAndABA(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-aba"
	commit := strings.Repeat("a", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	service := func(key, display string) servicecatalog.Service {
		return servicecatalog.Service{
			Key: key, DisplayName: display,
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}
	}
	first := serviceStateV3Generation(t, repository, commit, "a", []servicecatalog.Service{
		service("orders", "Orders A"), service("users", "Users A"),
	})
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	ordersA := serviceStateV3Row(t, s, repository, "orders")
	usersA := serviceStateV3Row(t, s, repository, "users")

	second := serviceStateV3Generation(t, repository, commit, "b", []servicecatalog.Service{
		service("orders", "Orders B"),
	})
	if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
		t.Fatal(err)
	}
	begin, err = s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	removed := serviceStateV3Row(t, s, repository, "users")
	if !removed.Removed || removed.Incarnation != usersA.Incarnation {
		t.Fatalf("removed users = %+v", removed)
	}

	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	begin, err = s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	ordersAgain := serviceStateV3Row(t, s, repository, "orders")
	usersAgain := serviceStateV3Row(t, s, repository, "users")
	if ordersAgain.Incarnation != ordersA.Incarnation ||
		ordersAgain.DesiredGeneration != ordersA.DesiredGeneration ||
		usersAgain.Incarnation != usersA.Incarnation+1 || usersAgain.Removed {
		t.Fatalf("A-B-A states orders=%+v users=%+v", ordersAgain, usersAgain)
	}
}

func TestServiceStateV3PartialActivationKeepsMatchingSummaryReadable(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-partial-activation"
	commit := strings.Repeat("f", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	services := make([]servicecatalog.Service, 600)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%04d", index), DisplayName: fmt.Sprintf("Service %04d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	generation := serviceStateV3Generation(t, repository, commit, "partial-activation", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	activation, err := s.BeginServiceStateV3Activation(
		ctx, repository, "sha256:"+strings.Repeat("1", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, activation)
	chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "partial-activation")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ProcessServiceStateV3Chunk(ctx, *chunk)
	if err != nil || result.Applied != servicecatalogv3.MaxServicesPerMember || result.Settled {
		t.Fatalf("first activation chunk = %+v, %v", result, err)
	}
	if err := s.CompleteGenerationChunk(ctx, *chunk); err != nil {
		t.Fatal(err)
	}
	summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || summary.CurrentCount != servicecatalogv3.MaxServicesPerMember ||
		summary.UnavailableCount != len(services)-servicecatalogv3.MaxServicesPerMember {
		t.Fatalf("partial activation summary = %+v, %v", summary, err)
	}
	runServiceStateV3Plan(t, s, activation)
}

func TestServiceStateV3SparseSuccessorKeepsUnchangedCatalogProvenance(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-sparse-successor"
	commit := strings.Repeat("6", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	const serviceCount = 100
	services := make([]servicecatalog.Service, serviceCount)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key:         fmt.Sprintf("service-%03d", index),
			DisplayName: fmt.Sprintf("Service %03d", index),
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}
	}
	legacy := legacyServiceCatalogV3Generation(
		t,
		serviceStateV3Generation(t, repository, commit, "sparse-legacy", services),
	)
	if err := s.PublishServiceCatalogV3Candidate(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	search := "sha256:" + strings.Repeat("7", 64)
	activation, err := s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)

	first := serviceStateV3Generation(t, repository, commit, "sparse-v2", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	reconcile, err = s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	assertServiceStateV3PlanBounds(t, s, reconcile.Plan.Digest, serviceCount*2, serviceCount)
	migrating := serviceStateV3Row(t, s, repository, "service-000")
	if migrating.DesiredCatalogGeneration != first.Root.Digest ||
		migrating.ActiveCatalogGeneration != legacy.Root.Digest {
		t.Fatalf("legacy transition state = %+v", migrating)
	}
	_, migratingReader, _ := newServiceStateV3CountingReader(t, s)
	migratingRead, err := migratingReader.OpenService(ctx, repository, "service-000")
	if err != nil {
		t.Fatal(err)
	}
	if migratingRead.Root.Digest != first.Root.Digest ||
		migratingRead.ActiveRoot.Digest != legacy.Root.Digest {
		t.Fatalf("legacy transition read = %+v", migratingRead)
	}
	if err := migratingReader.Confirm(ctx, migratingRead); err != nil {
		t.Fatal(err)
	}
	migratingRead.Close()
	if _, err := s.ValidateServiceCatalogV3Precious(ctx); err != nil {
		t.Fatalf("legacy transition precious validation: %v", err)
	}
	activation, err = s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)
	assertServiceStateV3PlanBounds(t, s, activation.Plan.Digest, serviceCount, serviceCount)
	unchangedA := serviceStateV3Row(t, s, repository, "service-000")

	successorServices := slices.Clone(services)
	successorServices[serviceCount-1].DisplayName = "Changed service"
	second := serviceStateV3Generation(
		t, repository, commit, "sparse-b", successorServices,
	)
	if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
		t.Fatal(err)
	}
	reconcile, err = s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	assertServiceStateV3PlanBounds(t, s, reconcile.Plan.Digest, serviceCount*2, 1)

	unchangedB := serviceStateV3Row(t, s, repository, "service-000")
	changedB := serviceStateV3Row(t, s, repository, "service-099")
	if unchangedB.StateDigest != unchangedA.StateDigest ||
		unchangedB.DesiredCatalogGeneration != first.Root.Digest ||
		changedB.DesiredCatalogGeneration != second.Root.Digest {
		t.Fatalf("sparse successor states: unchanged=%+v changed=%+v", unchangedB, changedB)
	}
	_, reader, _ := newServiceStateV3CountingReader(t, s)
	snapshot, err := reader.AcceptedSnapshot(ctx, repository)
	if err != nil || len(snapshot.States) != serviceCount {
		t.Fatalf("sparse successor relationship snapshot = %d states, %v", len(snapshot.States), err)
	}

	activation, err = s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)
	assertServiceStateV3PlanBounds(t, s, activation.Plan.Digest, serviceCount, 1)
	unchangedRead, err := reader.OpenService(ctx, repository, "service-000")
	if err != nil {
		t.Fatal(err)
	}
	defer unchangedRead.Close()
	if unchangedRead.Entry.State.ActiveCatalogGeneration != first.Root.Digest ||
		unchangedRead.Root.Digest != second.Root.Digest ||
		unchangedRead.ActiveRoot.Digest != first.Root.Digest {
		t.Fatalf("unchanged successor active provenance = %+v", unchangedRead)
	}
	if _, err := s.ValidateServiceCatalogV3Precious(ctx); err != nil {
		t.Fatalf("mixed-schema precious validation: %v", err)
	}
}

func TestServiceStateV3TenThousandBoundedColdNoopDeltaAndActivation(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-ten-thousand"
	commit := strings.Repeat("d", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	const servicesCount = 10_000
	services := make([]servicecatalog.Service, servicesCount)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%05d", index), DisplayName: fmt.Sprintf("Service %05d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	buildGeneration := func(version string, values []servicecatalog.Service) servicecatalogv3.Generation {
		generation := serviceStateV3Generation(t, repository, commit, version, values)
		catalog, err := generation.Catalog()
		if err != nil {
			t.Fatal(err)
		}
		binding := generation.Root.Binding
		binding.Source.FileCount = servicesCount
		binding.Source.AcceptedFileCount = servicesCount
		generation, err = servicecatalogv3.Build(binding, catalog)
		if err != nil {
			t.Fatal(err)
		}
		return generation
	}
	started := time.Now()
	first := buildGeneration("ten-thousand-a", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	cold, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, cold)
	assertServiceStateV3PlanBounds(t, s, cold.Plan.Digest, servicesCount*2, servicesCount)
	if time.Since(started) > 10*time.Minute {
		t.Fatalf("10,000-service cold reconcile exceeded ten minutes: %s", time.Since(started))
	}
	noop, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || !noop.Noop {
		t.Fatalf("10,000-service reconcile no-op = %+v, %v", noop, err)
	}
	search := "sha256:" + strings.Repeat("e", 64)
	activation, err := s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)
	assertServiceStateV3PlanBounds(t, s, activation.Plan.Digest, servicesCount, servicesCount)

	counter, reader, cache := newServiceStateV3CountingReader(t, s)
	const concurrentReaders = 8
	errs := make([]error, concurrentReaders)
	var wait sync.WaitGroup
	for index := range concurrentReaders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			read, readErr := reader.OpenService(ctx, repository, "service-05123")
			if readErr == nil {
				readErr = reader.Confirm(ctx, read)
			}
			if read != nil {
				read.Close()
			}
			errs[index] = readErr
		}()
	}
	wait.Wait()
	for _, readErr := range errs {
		if readErr != nil {
			t.Fatal(readErr)
		}
	}
	stats := cache.Stats()
	if counter.pointers.Load() != concurrentReaders || counter.roots.Load() != 1 ||
		counter.members.Load() != 1 || counter.summaries.Load() != concurrentReaders ||
		counter.points.Load() != concurrentReaders || counter.confirms.Load() != concurrentReaders ||
		stats.RootReads != 1 || stats.MemberReads != 1 ||
		stats.RootValidations != 1 || stats.MemberValidations != 1 {
		t.Fatalf("10,000-service concurrent cold counts = %+v", stats)
	}
	warm, err := reader.OpenService(ctx, repository, "service-05123")
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Confirm(ctx, warm); err != nil {
		t.Fatal(err)
	}
	warm.Close()
	stats = cache.Stats()
	if counter.roots.Load() != 1 || counter.members.Load() != 1 ||
		stats.RootReads != 1 || stats.MemberReads != 1 ||
		stats.RootLeases != 0 || stats.MemberLeases != 0 {
		t.Fatalf("10,000-service warm/cache counts = %+v", stats)
	}

	pageCounter, pageReader, pageCache := newServiceStateV3CountingReader(t, s)
	anchor, err := s.GetServiceStateV3Point(ctx, repository, "service-00499")
	if err != nil {
		t.Fatal(err)
	}
	after, err := serviceStateV3Position(first.Root, anchor)
	if err != nil {
		t.Fatal(err)
	}
	page, err := pageReader.ListServices(
		ctx, repository, ServiceStateFilter{}, after, MaxServiceStateReadPage,
	)
	if err != nil {
		t.Fatal(err)
	}
	pageStats := pageCache.Stats()
	if len(page.Entries) != MaxServiceStateReadPage ||
		page.Entries[0].State.ServiceKey != "service-00500" ||
		page.Continuation == nil || page.Continuation.MemberRangeDigest == "" ||
		pageCounter.pointers.Load() != 1 || pageCounter.roots.Load() != 1 ||
		pageCounter.members.Load() != 2 || pageCounter.summaries.Load() != 1 ||
		pageCounter.points.Load() != 1 || pageCounter.pages.Load() != 1 ||
		pageCounter.confirms.Load() != 1 || pageStats.MemberReads != 2 ||
		pageStats.RootLeases != 1 || pageStats.MemberLeases != 2 {
		t.Fatalf("10,000-service page counts = %+v / %+v", pageStats, page)
	}
	forged := *page.Continuation
	page.Close()
	closedStats := pageCache.Stats()
	if closedStats.RootLeases != 0 || closedStats.MemberLeases != 0 {
		t.Fatalf("closed page retained leases = %+v", closedStats)
	}
	forged.MemberRangeDigest = "sha256:" + strings.Repeat("0", 64)
	if _, err := pageReader.ListServices(
		ctx, repository, ServiceStateFilter{}, forged, MaxServiceStateReadPage,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("forged v3 member-range cursor = %v", err)
	}

	batchCounter, batchReader, batchCache := newServiceStateV3CountingReader(t, s)
	snapshot, err := batchReader.AcceptedSnapshot(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	batchStats := batchCache.Stats()
	if len(snapshot.States) != servicesCount || batchCounter.pointers.Load() != 1 ||
		batchCounter.roots.Load() != 1 ||
		batchCounter.members.Load() != int64(len(first.Root.ServiceMembers)) ||
		batchCounter.summaries.Load() != 1 || batchCounter.accepted.Load() != 1 ||
		batchCounter.confirms.Load() != 1 ||
		batchStats.MemberValidations != uint64(len(first.Root.ServiceMembers)) {
		t.Fatalf("10,000-service batch counts = %+v", batchStats)
	}

	_, heldReader, heldCache := newServiceStateV3CountingReader(t, s)
	held, err := heldReader.OpenService(ctx, repository, "service-09999")
	if err != nil {
		t.Fatal(err)
	}

	deltaServices := append([]servicecatalog.Service(nil), services...)
	deltaServices[len(deltaServices)-1].DisplayName = "Changed service"
	second := buildGeneration("ten-thousand-b", deltaServices)
	if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
		t.Fatal(err)
	}
	_, seamReader, _ := newServiceStateV3CountingReader(t, s)
	if opened, seamErr := seamReader.OpenService(
		ctx, repository, "service-09999",
	); seamErr == nil {
		opened.Close()
		t.Fatal("concurrent catalog publication escaped unreconciled summary")
	}
	if err := heldReader.Confirm(ctx, held); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked v3 read confirmation = %v", err)
	}
	if stats := heldCache.Stats(); stats.RootLeases != 1 || stats.MemberLeases != 1 {
		t.Fatalf("revoked v3 read retired before lease close = %+v", stats)
	}
	held.Close()
	if stats := heldCache.Stats(); stats.RootLeases != 0 || stats.MemberLeases != 0 {
		t.Fatalf("revoked v3 read lease remained = %+v", stats)
	}
	delta, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, delta)
	assertServiceStateV3PlanBounds(t, s, delta.Plan.Digest, servicesCount*2, 1)

	staleCounter, staleReader, staleCache := newServiceStateV3CountingReader(t, s)
	stale, err := staleReader.OpenService(ctx, repository, "service-09999")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Entry.State.Status != servicecatalog.StatusStale ||
		staleCounter.roots.Load() != 2 || staleCounter.members.Load() != 2 {
		t.Fatalf("stale v3 detail counts = %+v / %+v", staleCache.Stats(), stale.Entry.State)
	}
	stale.Close()

	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	aba, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, aba)
	assertServiceStateV3PlanBounds(t, s, aba.Plan.Digest, servicesCount*2, 1)

	baseCatalog, err := first.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	siblingBefore := serviceStateV3Row(t, s, repository, "service-00000")
	removedBefore := serviceStateV3Row(t, s, repository, "service-09999")
	removedCatalog := baseCatalog
	removedCatalog.Services = slices.Clone(baseCatalog.Services)
	removedCatalog.Memberships = slices.Clone(baseCatalog.Memberships)
	removedCatalog.Authority.Version = "ten-thousand-remove"
	removedCatalog.Services = slices.DeleteFunc(
		removedCatalog.Services,
		func(service servicecatalog.Service) bool { return service.Key == "service-09999" },
	)
	removedCatalog.Memberships = slices.DeleteFunc(
		removedCatalog.Memberships,
		func(membership servicecatalog.Membership) bool {
			return membership.ServiceKey == "service-09999"
		},
	)
	removedCatalog.Unowned = []servicecatalog.UnownedPlacement{{
		Path: "svc/09999", Origin: servicecatalog.OriginBase,
	}}
	removedBinding := first.Root.Binding
	removedBinding.Authority = removedCatalog.Authority
	removedBinding.Source.AcceptedFileCount = servicesCount - 1
	removedBinding.Source.UnownedFileCount = 1
	removedGeneration, err := servicecatalogv3.Build(removedBinding, removedCatalog)
	if err != nil {
		t.Fatal(err)
	}
	firstSource, err := servicecatalogv3.SourceGenerationDigest(first.Root)
	if err != nil {
		t.Fatal(err)
	}
	removedSource, err := servicecatalogv3.SourceGenerationDigest(removedGeneration.Root)
	if err != nil || removedSource != firstSource {
		t.Fatalf("complement-only source generation = %q, want %q: %v", removedSource, firstSource, err)
	}
	if err := s.PublishServiceCatalogV3Candidate(ctx, removedGeneration); err != nil {
		t.Fatal(err)
	}
	removedPlan, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, removedPlan)
	assertServiceStateV3PlanBounds(t, s, removedPlan.Plan.Digest, servicesCount*2, 1)
	removedState := serviceStateV3Row(t, s, repository, "service-09999")
	if !removedState.Removed || removedState.Incarnation != removedBefore.Incarnation {
		t.Fatalf("10,000-service honest removal = %+v", removedState)
	}

	readdCatalog := baseCatalog
	readdCatalog.Services = slices.Clone(baseCatalog.Services)
	readdCatalog.Memberships = slices.Clone(baseCatalog.Memberships)
	readdCatalog.Authority.Version = "ten-thousand-readd"
	readdBinding := first.Root.Binding
	readdBinding.Authority = readdCatalog.Authority
	readdGeneration, err := servicecatalogv3.Build(readdBinding, readdCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishServiceCatalogV3Candidate(ctx, readdGeneration); err != nil {
		t.Fatal(err)
	}
	readdPlan, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, readdPlan)
	assertServiceStateV3PlanBounds(t, s, readdPlan.Plan.Digest, servicesCount*2, 1)
	readdActivation, err := s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, readdActivation)
	assertServiceStateV3PlanBounds(t, s, readdActivation.Plan.Digest, servicesCount, 1)
	siblingAfter := serviceStateV3Row(t, s, repository, "service-00000")
	readdedState := serviceStateV3Row(t, s, repository, "service-09999")
	if siblingAfter.StateDigest != siblingBefore.StateDigest ||
		siblingAfter.DesiredCatalogGeneration != first.Root.Digest ||
		siblingAfter.ActiveCatalogGeneration != first.Root.Digest ||
		readdedState.Removed || readdedState.Incarnation != removedBefore.Incarnation+1 {
		t.Fatalf("10,000-service honest re-add sibling=%+v readded=%+v", siblingAfter, readdedState)
	}
	if time.Since(started) > 10*time.Minute {
		t.Fatalf("10,000-service cold/no-op/delta/A-B-A/removal/re-add exceeded ten minutes: %s", time.Since(started))
	}
}

func TestServiceStateV3ReadKeepsMaximumSuccessorRowBounded(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-successors"
	commit := strings.Repeat("8", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	services := make([]servicecatalog.Service, 1, servicecatalogv3.MaxServiceSuccessors+1)
	services[0] = servicecatalog.Service{
		Key: "owner", DisplayName: "Owner", Disposition: servicecatalog.DispositionRejected,
		Origin: servicecatalog.OriginBase, Reason: "split",
		Successors: make([]string, servicecatalogv3.MaxServiceSuccessors),
	}
	for index := range servicecatalogv3.MaxServiceSuccessors {
		key := fmt.Sprintf("successor-%03d", index)
		services[0].Successors[index] = key
		services = append(services, servicecatalog.Service{
			Key: key, DisplayName: key, Disposition: servicecatalog.DispositionAccepted,
			Origin: servicecatalog.OriginBase,
		})
	}
	generation := serviceStateV3Generation(
		t, repository, commit, "maximum-successors", services,
	)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	_, reader, _ := newServiceStateV3CountingReader(t, s)
	read, err := reader.OpenService(ctx, repository, "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	encoded, err := json.Marshal(read.Entry.State)
	if err != nil || len(read.Entry.State.Successors) != servicecatalogv3.MaxServiceSuccessors ||
		len(encoded) > 64<<10 {
		t.Fatalf("maximum-successor state = %d successors, %d bytes, %v",
			len(read.Entry.State.Successors), len(encoded), err)
	}
}

func assertServiceStateV3PlanBounds(
	t *testing.T,
	s *Surreal,
	digest string,
	maxReads int64,
	writes int64,
) {
	t.Helper()
	plan, err := s.getServiceStateV3Plan(t.Context(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MaxChunkRows > MaxServiceStateV3ChunkRows || plan.RowsRead > maxReads ||
		plan.RowsWritten != writes || plan.BytesWritten > 64<<20 ||
		plan.TotalChunks > servicecatalogv3.MaxMembers*2+1 {
		t.Fatalf("service state v3 plan bounds = %+v", plan)
	}
}

func expandServiceStateV3Plan(t *testing.T, s *Surreal, begin ServiceStateV3Begin) {
	t.Helper()
	schedule := begin.Schedule
	for schedule.NextOffset < schedule.TotalItems {
		var err error
		schedule, err = s.ExpandGenerationSchedule(
			t.Context(), schedule.Repository, schedule.Stage, schedule.Generation,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func serviceStateV3Row(
	t *testing.T,
	s *Surreal,
	repository, serviceKey string,
) servicecatalog.ServiceState {
	t.Helper()
	results, err := surrealdb.Query[[]serviceStateRec](
		t.Context(), s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceStateV3ID(repository, serviceKey)},
	)
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		t.Fatalf("service state v3 row %q = %+v", serviceKey, rows)
	}
	state, err := serviceStateV3FromRec(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	return *state
}

func serviceStateV3PlanCount(t *testing.T, s *Surreal) int {
	t.Helper()
	results, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](t.Context(), s.db, `RETURN [{ count: array::len(
		SELECT id FROM service_state_v3_plan
	) }];`, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		t.Fatalf("service state v3 plan count = %+v", rows)
	}
	return rows[0].Count
}
