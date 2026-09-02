package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

func TestServiceCatalogV3RelationshipReferencePinsExactCurrentAuthority(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	reference := currentServiceCatalogV3RelationshipReference(
		t, s, "example.com/acme/v3-relationship-reference",
	)
	ctx := t.Context()
	if err := s.PinRelationshipPublicationV3(
		ctx,
		reference.Repository,
		reference.RelationshipGenerationDigest,
		reference.RelationshipRootDigest,
		reference.CatalogRootDigest,
		reference.CatalogControlRevision,
		reference.StateControlRevision,
		reference.StateSummaryDigest,
	); err != nil {
		t.Fatalf("pin exact reference: %v", err)
	}
	if err := s.PinServiceCatalogV3RelationshipReference(ctx, reference); err != nil {
		t.Fatalf("repeat exact pin: %v", err)
	}
	report, err := s.ValidateServiceCatalogV3Precious(ctx)
	if err != nil || report.RelationshipReferences != 1 {
		t.Fatalf("validate pinned reference = %+v, %v", report, err)
	}

	collision := reference
	collision.RelationshipRootDigest = "sha256:" + strings.Repeat("f", 64)
	if err := s.PinServiceCatalogV3RelationshipReference(
		ctx, collision,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("immutable collision = %v", err)
	}
	if err := s.UnpinServiceCatalogV3RelationshipReference(
		ctx, collision,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched unpin = %v", err)
	}

	stale := reference
	stale.RelationshipGenerationDigest = "sha256:" + strings.Repeat("e", 64)
	stale.StateControlRevision++
	if err := s.PinServiceCatalogV3RelationshipReference(
		ctx, stale,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale state fence = %v", err)
	}
	if count := serviceCatalogV3RelationshipReferenceCount(t, s); count != 1 {
		t.Fatalf("stale pin changed reference count to %d", count)
	}
	staleCatalog := reference
	staleCatalog.RelationshipGenerationDigest = "sha256:" + strings.Repeat("d", 64)
	staleCatalog.CatalogControlRevision++
	if err := s.PinServiceCatalogV3RelationshipReference(
		ctx, staleCatalog,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale catalog fence = %v", err)
	}

	if err := s.UnpinServiceCatalogV3RelationshipReference(ctx, reference); err != nil {
		t.Fatalf("exact unpin: %v", err)
	}
	if err := s.UnpinServiceCatalogV3RelationshipReference(ctx, reference); err != nil {
		t.Fatalf("repeat exact unpin: %v", err)
	}
	if count := serviceCatalogV3RelationshipReferenceCount(t, s); count != 0 {
		t.Fatalf("unpinned reference count = %d", count)
	}
}

func TestServiceCatalogV3RelationshipReferenceReconcileAndLifecycleFence(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/v3-relationship-lifecycle"
	commit := strings.Repeat("d", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	generations := make([]servicecatalogv3.Generation, 0, 5)
	for index := range 5 {
		generation := internalOperatorServiceCatalogV3Generation(
			t, repository, commit, fmt.Sprintf("relationship-v%d", index),
		)
		if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
			t.Fatal(err)
		}
		generations = append(generations, generation)
	}
	oldestGeneration := oldestServiceCatalogV3Prior(t, s, generations)
	reference := ServiceCatalogV3RelationshipReference{
		Repository:                   repository,
		RelationshipGenerationDigest: "sha256:" + strings.Repeat("a", 64),
		RelationshipRootDigest:       "sha256:" + strings.Repeat("b", 64),
		CatalogRootDigest:            oldestGeneration.Root.Digest,
		CatalogControlRevision:       1,
		StateControlRevision:         1,
		StateSummaryDigest:           "sha256:" + strings.Repeat("c", 64),
	}
	if err := s.ReconcileServiceCatalogV3RelationshipReferences(
		ctx, []ServiceCatalogV3RelationshipReference{reference},
	); err != nil {
		t.Fatalf("reconstruct historical reference: %v", err)
	}
	oldest := serviceCatalogV3LifecycleRecord(
		t, s, oldestGeneration.Root.Digest,
	)
	if retired, err := s.retireServiceCatalogV3Generation(
		ctx, oldest, ServiceCatalogV3Retained,
	); err != nil || retired {
		t.Fatalf("relationship-pinned retirement = %t, %v", retired, err)
	}

	converged := false
	cursor := ""
	for range 256 {
		sweep, err := s.SweepServiceCatalogV3Lifecycle(
			ctx, cursor, 11, 16, ServiceCatalogV3Retained,
		)
		if err != nil {
			t.Fatal(err)
		}
		cursor = sweep.Cursor
		report, err := s.ValidateServiceCatalogV3Precious(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if report.HistoricalRoots == ServiceCatalogV3Retained+1 &&
			report.CollectingRoots == 0 {
			converged = true
			break
		}
	}
	if !converged {
		t.Fatal("relationship reference did not preserve the old catalog root")
	}

	collision := reference
	candidate, err := s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	collision.CatalogRootDigest = candidate.Generation.Root.Digest
	if collision.CatalogRootDigest == reference.CatalogRootDigest ||
		serviceCatalogV3LifecycleRecord(t, s, collision.CatalogRootDigest).State != serviceCatalogV3Historical {
		t.Fatal("immutable collision fixture lacks a distinct retained candidate")
	}
	if err := s.ReconcileServiceCatalogV3RelationshipReferences(
		ctx, []ServiceCatalogV3RelationshipReference{collision},
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("reconcile immutable collision = %v", err)
	}
	if count := serviceCatalogV3RelationshipReferenceCount(t, s); count != 1 {
		t.Fatalf("collision changed reference count to %d", count)
	}

	if err := s.ReconcileServiceCatalogV3RelationshipReferences(ctx, nil); err != nil {
		t.Fatalf("remove stale reference set: %v", err)
	}
	for range 256 {
		sweep, err := s.SweepServiceCatalogV3Lifecycle(
			ctx, cursor, 11, 16, ServiceCatalogV3Retained,
		)
		if err != nil {
			t.Fatal(err)
		}
		cursor = sweep.Cursor
		report, err := s.ValidateServiceCatalogV3Precious(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if report.HistoricalRoots == ServiceCatalogV3Retained &&
			report.CollectingRoots == 0 && report.RelationshipReferences == 0 {
			return
		}
	}
	t.Fatal("catalog lifecycle did not resume after relationship reconciliation")
}

func TestServiceCatalogV3RelationshipReferenceRecoveryIsAddOnly(t *testing.T) {
	t.Run("historical reconstruction and immutable collision", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		ctx := t.Context()
		repository := "example.com/acme/v3-relationship-recovery"
		reference := currentServiceCatalogV3RelationshipReference(t, s, repository)
		next := internalOperatorServiceCatalogV3Generation(
			t, repository, strings.Repeat("8", 40), "recovery-next",
		)
		if err := s.PublishServiceCatalogV3Candidate(ctx, next); err != nil {
			t.Fatal(err)
		}
		if err := s.PinServiceCatalogV3RelationshipReference(
			ctx, reference,
		); !errors.Is(err, ErrConflict) {
			t.Fatalf("ordinary pin accepted historical authority: %v", err)
		}
		if err := s.RecoverServiceCatalogV3RelationshipReference(
			ctx, reference,
		); err != nil {
			t.Fatalf("recover historical relationship reference: %v", err)
		}
		if err := s.RecoverServiceCatalogV3RelationshipReference(
			ctx, reference,
		); err != nil {
			t.Fatalf("repeat historical recovery: %v", err)
		}
		if count := serviceCatalogV3RelationshipReferenceCount(t, s); count != 1 {
			t.Fatalf("historical recovery reference count = %d", count)
		}
		collision := reference
		collision.RelationshipRootDigest = "sha256:" + strings.Repeat("f", 64)
		if err := s.RecoverServiceCatalogV3RelationshipReference(
			ctx, collision,
		); !errors.Is(err, ErrConflict) {
			t.Fatalf("historical immutable collision = %v", err)
		}
		if count := serviceCatalogV3RelationshipReferenceCount(t, s); count != 1 {
			t.Fatalf("collision changed recovered reference count to %d", count)
		}
	})

	t.Run("collecting refusal", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		reference := currentServiceCatalogV3RelationshipReference(
			t, s, "example.com/acme/v3-relationship-recovery-collecting",
		)
		if _, err := surrealdb.Query[any](t.Context(), s.db, `
UPDATE $lifecycle SET state = 'collecting', tombstoned_at = time::now()
	RETURN NONE;`, map[string]any{
			"lifecycle": serviceCatalogV3LifecycleID(reference.CatalogRootDigest),
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.RecoverServiceCatalogV3RelationshipReference(
			t.Context(), reference,
		); !errors.Is(err, ErrConflict) {
			t.Fatalf("collecting catalog root recovery = %v", err)
		}
		if count := serviceCatalogV3RelationshipReferenceCount(t, s); count != 0 {
			t.Fatalf("collecting refusal reference count = %d", count)
		}
	})

	t.Run("absent refusal", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		reference := ServiceCatalogV3RelationshipReference{
			Repository:                   "example.com/acme/v3-relationship-absent",
			RelationshipGenerationDigest: "sha256:" + strings.Repeat("1", 64),
			RelationshipRootDigest:       "sha256:" + strings.Repeat("2", 64),
			CatalogRootDigest:            "sha256:" + strings.Repeat("3", 64),
			CatalogControlRevision:       1,
			StateControlRevision:         1,
			StateSummaryDigest:           "sha256:" + strings.Repeat("4", 64),
		}
		if err := s.RecoverServiceCatalogV3RelationshipReference(
			t.Context(), reference,
		); !errors.Is(err, ErrConflict) {
			t.Fatalf("absent catalog root recovery = %v", err)
		}
		if count := serviceCatalogV3RelationshipReferenceCount(t, s); count != 0 {
			t.Fatalf("absent refusal reference count = %d", count)
		}
	})
}

func TestServiceCatalogV3RelationshipReferenceDrainAndFinalizeFences(t *testing.T) {
	for _, test := range []struct {
		name     string
		finalize bool
	}{
		{name: "drain"},
		{name: "finalize", finalize: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newServiceCatalogV3InternalStore(t)
			record, reference, _ := collectingServiceCatalogV3RelationshipReference(t, s)
			ctx := t.Context()
			if err := s.ReconcileServiceCatalogV3RelationshipReferences(
				ctx, []ServiceCatalogV3RelationshipReference{reference},
			); !errors.Is(err, ErrConflict) {
				t.Fatalf("reconcile collecting catalog root = %v", err)
			}
			if test.finalize {
				if _, err := surrealdb.Query[any](ctx, s.db, `
UPDATE $lifecycle SET member_cursor = member_count RETURN NONE;
DELETE service_catalog_v3_root_member
	WHERE root_digest = $root_digest RETURN NONE;`, map[string]any{
					"lifecycle":   serviceCatalogV3LifecycleID(record.RootDigest),
					"root_digest": record.RootDigest,
				}); err != nil {
					t.Fatal(err)
				}
				record = serviceCatalogV3LifecycleRecord(t, s, record.RootDigest)
			}
			createServiceCatalogV3RelationshipReference(t, s, reference)
			if _, err := s.ValidateServiceCatalogV3Precious(ctx); !errors.Is(
				err, ErrInvalidServiceCatalogV3Lifecycle,
			) {
				t.Fatalf("collecting reference precious validation = %v", err)
			}
			if test.finalize {
				if _, err := s.finalizeServiceCatalogV3Generation(
					ctx, record,
				); err == nil || !strings.Contains(err.Error(), "finalize fence changed") {
					t.Fatalf("relationship-pinned finalize = %v", err)
				}
				return
			}
			if _, _, _, _, err := s.drainServiceCatalogV3Generation(
				ctx, record, 16,
			); err == nil || !strings.Contains(err.Error(), "collecting fence changed") {
				t.Fatalf("relationship-pinned drain = %v", err)
			}
		})
	}
}

func TestServiceCatalogV3RelationshipReferenceMigrationRepairsDrift(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	if _, err := surrealdb.Query[any](ctx, s.db, `
DELETE $marker RETURN NONE;
DEFINE FIELD OVERWRITE state_control_revision
	ON service_catalog_v3_relationship_reference TYPE string;`, map[string]any{
		"marker": serviceCatalogV3RelationshipReferenceSchemaMigrationID(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateServiceCatalogV3RelationshipReferenceSchema(ctx); err != nil {
		t.Fatalf("repair relationship reference migration: %v", err)
	}
	if err := s.migrateServiceCatalogV3RelationshipReferenceSchema(ctx); err != nil {
		t.Fatalf("repeat relationship reference migration: %v", err)
	}
	reference := currentServiceCatalogV3RelationshipReference(
		t, s, "example.com/acme/v3-relationship-migration",
	)
	if err := s.PinServiceCatalogV3RelationshipReference(ctx, reference); err != nil {
		t.Fatalf("pin after migration repair: %v", err)
	}
}

func currentServiceCatalogV3RelationshipReference(
	t *testing.T,
	s *Surreal,
	repository string,
) ServiceCatalogV3RelationshipReference {
	t.Helper()
	commit := strings.Repeat("8", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	generation := internalServiceCatalogV3Generation(t, repository, commit)
	if err := s.PublishServiceCatalogV3Candidate(t.Context(), generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	pointer, err := s.GetServiceCatalogV3CandidatePointer(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.GetServiceStateV3Summary(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	return ServiceCatalogV3RelationshipReference{
		Repository:                   repository,
		RelationshipGenerationDigest: "sha256:" + strings.Repeat("9", 64),
		RelationshipRootDigest:       "sha256:" + strings.Repeat("a", 64),
		CatalogRootDigest:            pointer.RootDigest,
		CatalogControlRevision:       pointer.ControlRevision,
		StateControlRevision:         summary.ControlRevision,
		StateSummaryDigest:           summary.SummaryDigest,
	}
}

func serviceCatalogV3RelationshipReferenceCount(t *testing.T, s *Surreal) int {
	t.Helper()
	results, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](t.Context(), s.db, `RETURN [{ count: array::len(
		SELECT id FROM service_catalog_v3_relationship_reference
	) }];`, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		t.Fatalf("relationship reference count = %+v", rows)
	}
	return rows[0].Count
}

func serviceCatalogV3LifecycleRecord(
	t *testing.T,
	s *Surreal,
	rootDigest string,
) serviceCatalogV3LifecycleRec {
	t.Helper()
	results, err := surrealdb.Query[[]serviceCatalogV3LifecycleRec](
		t.Context(), s.db, "SELECT * FROM $rid", map[string]any{
			"rid": serviceCatalogV3LifecycleID(rootDigest),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		t.Fatalf("lifecycle record %q = %+v", rootDigest, rows)
	}
	return rows[0]
}

func collectingServiceCatalogV3RelationshipReference(
	t *testing.T,
	s *Surreal,
) (
	serviceCatalogV3LifecycleRec,
	ServiceCatalogV3RelationshipReference,
	servicecatalogv3.Generation,
) {
	t.Helper()
	repository := "example.com/acme/v3-relationship-collecting-" + strings.ToLower(t.Name())
	commit := strings.Repeat("7", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	generations := make([]servicecatalogv3.Generation, 0, 5)
	for index := range 5 {
		generation := internalOperatorServiceCatalogV3Generation(
			t, repository, commit, fmt.Sprintf("collecting-v%d", index),
		)
		if err := s.PublishServiceCatalogV3Candidate(t.Context(), generation); err != nil {
			t.Fatal(err)
		}
		generations = append(generations, generation)
	}
	oldest := oldestServiceCatalogV3Prior(t, s, generations)
	record := serviceCatalogV3LifecycleRecord(t, s, oldest.Root.Digest)
	retired, err := s.retireServiceCatalogV3Generation(
		t.Context(), record, ServiceCatalogV3Retained,
	)
	if err != nil || !retired {
		t.Fatalf("prepare collecting root = %t, %v", retired, err)
	}
	record = serviceCatalogV3LifecycleRecord(t, s, oldest.Root.Digest)
	return record, ServiceCatalogV3RelationshipReference{
		Repository:                   repository,
		RelationshipGenerationDigest: "sha256:" + strings.Repeat("4", 64),
		RelationshipRootDigest:       "sha256:" + strings.Repeat("5", 64),
		CatalogRootDigest:            oldest.Root.Digest,
		CatalogControlRevision:       1,
		StateControlRevision:         1,
		StateSummaryDigest:           "sha256:" + strings.Repeat("6", 64),
	}, oldest
}

func createServiceCatalogV3RelationshipReference(
	t *testing.T,
	s *Surreal,
	reference ServiceCatalogV3RelationshipReference,
) {
	t.Helper()
	if _, err := surrealdb.Query[any](t.Context(), s.db, `CREATE $rid CONTENT {
	repository: $repository,
	relationship_generation_digest: $relationship_generation_digest,
	relationship_root_digest: $relationship_root_digest,
	catalog_root_digest: $catalog_root_digest,
	catalog_control_revision: $catalog_control_revision,
	state_control_revision: $state_control_revision,
	state_summary_digest: $state_summary_digest,
	recorded_at: time::now()
} RETURN NONE`, map[string]any{
		"rid": serviceCatalogV3RelationshipReferenceID(
			reference.RelationshipGenerationDigest,
		),
		"repository":                     reference.Repository,
		"relationship_generation_digest": reference.RelationshipGenerationDigest,
		"relationship_root_digest":       reference.RelationshipRootDigest,
		"catalog_root_digest":            reference.CatalogRootDigest,
		"catalog_control_revision":       reference.CatalogControlRevision,
		"state_control_revision":         reference.StateControlRevision,
		"state_summary_digest":           reference.StateSummaryDigest,
	}); err != nil {
		t.Fatal(err)
	}
}
