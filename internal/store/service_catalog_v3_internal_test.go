package store

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

func serviceCatalogV3LifecycleCount(t *testing.T, s *Surreal) int {
	t.Helper()
	results, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](t.Context(), s.db,
		"RETURN [{ count: array::len(SELECT id FROM service_catalog_v3_lifecycle) }]", nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		t.Fatalf("lifecycle count = %+v", rows)
	}
	return rows[0].Count
}

func TestServiceCatalogV3CrashGapRollsBackAllRows(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	generation := internalServiceCatalogV3Generation(t, "example.com/acme/v3-crash", strings.Repeat("5", 40))
	seedServiceCatalogV3Repo(t, s, generation.Root.Binding.Repository, generation.Root.Binding.Source.Commit)
	results, err := surrealdb.Query[any](ctx, s.db, `
DEFINE EVENT service_catalog_v3_crash_trap ON TABLE service_catalog_v3_candidate
	WHEN $event != 'DELETE' THEN { THROW 'phebs-permanent: injected candidate crash gap' };`, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf("trap statement %d: %s", index, result.Error.Message)
		}
	}
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err == nil ||
		!strings.Contains(err.Error(), "injected candidate crash gap") {
		t.Fatalf("crash-gap publication = %v", err)
	}
	counts := serviceCatalogV3TableCounts(t, s)
	if counts != [4]int{} {
		t.Fatalf("crash-gap retained rows = %v", counts)
	}
	if serviceCatalogV3LifecycleCount(t, s) != 0 {
		t.Fatal("crash-gap retained lifecycle metadata")
	}
}

func TestServiceCatalogV3CollisionAndPartialInventoryRefuse(t *testing.T) {
	t.Run("root digest collision", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		ctx := t.Context()
		generation := internalServiceCatalogV3Generation(t, "example.com/acme/v3-root-collision", strings.Repeat("9", 40))
		seedServiceCatalogV3Repo(t, s, generation.Root.Binding.Repository, generation.Root.Binding.Source.Commit)
		_, err := surrealdb.Query[any](ctx, s.db, `CREATE $rid CONTENT {
			root_digest: $digest, repository: $repository,
			root_bytes: 3, root_json: '{}\n', recorded_at: time::now()
		}`, map[string]any{
			"rid":        serviceCatalogV3RootID(generation.Root.Digest),
			"digest":     generation.Root.Digest,
			"repository": generation.Root.Binding.Repository,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PublishServiceCatalogV3Candidate(ctx, generation); !errors.Is(err, ErrConflict) {
			t.Fatalf("root collision = %v", err)
		}
		counts := serviceCatalogV3TableCounts(t, s)
		if counts != [4]int{1, 0, 0, 0} {
			t.Fatalf("root collision rows = %v", counts)
		}
	})

	t.Run("member digest collision", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		ctx := t.Context()
		generation := internalServiceCatalogV3Generation(t, "example.com/acme/v3-collision", strings.Repeat("6", 40))
		seedServiceCatalogV3Repo(t, s, generation.Root.Binding.Repository, generation.Root.Binding.Source.Commit)
		descriptor := generation.Root.ServiceMembers[0]
		_, err := surrealdb.Query[any](ctx, s.db, `CREATE $rid CONTENT {
			member_digest: $digest, kind: 'service', ordinal: 0,
			content_bytes: 1, content: 'x', recorded_at: time::now()
		}`, map[string]any{
			"rid":    serviceCatalogV3MemberID(descriptor.Digest),
			"digest": descriptor.Digest,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PublishServiceCatalogV3Candidate(ctx, generation); !errors.Is(err, ErrConflict) {
			t.Fatalf("member collision = %v", err)
		}
		counts := serviceCatalogV3TableCounts(t, s)
		if counts != [4]int{0, 1, 0, 0} {
			t.Fatalf("collision rows = %v", counts)
		}
	})

	t.Run("missing referenced member", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		ctx := t.Context()
		generation := internalServiceCatalogV3Generation(t, "example.com/acme/v3-partial", strings.Repeat("7", 40))
		seedServiceCatalogV3Repo(t, s, generation.Root.Binding.Repository, generation.Root.Binding.Source.Commit)
		if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
			t.Fatal(err)
		}
		descriptor := generation.Root.ServiceMembers[0]
		if _, err := surrealdb.Query[any](ctx, s.db, "DELETE $rid", map[string]any{
			"rid": serviceCatalogV3MemberID(descriptor.Digest),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetServiceCatalogV3Candidate(ctx, generation.Root.Binding.Repository); !errors.Is(err, ErrInvalidServiceCatalogV3Candidate) {
			t.Fatalf("partial candidate open = %v", err)
		}
	})
}

func TestServiceCatalogV3SchemaMigrationRepairsDriftIdempotently(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	if _, err := surrealdb.Query[any](ctx, s.db, `
DELETE $marker;
DEFINE FIELD OVERWRITE root_bytes ON service_catalog_v3_root TYPE string;`, map[string]any{
		"marker": serviceCatalogV3SchemaMigrationID(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateServiceCatalogV3Schema(ctx); err != nil {
		t.Fatalf("repair schema migration: %v", err)
	}
	if err := s.migrateServiceCatalogV3Schema(ctx); err != nil {
		t.Fatalf("idempotent schema migration: %v", err)
	}
	generation := internalServiceCatalogV3Generation(t, "example.com/acme/v3-migration", strings.Repeat("8", 40))
	seedServiceCatalogV3Repo(t, s, generation.Root.Binding.Repository, generation.Root.Binding.Source.Commit)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatalf("publish after migration repair: %v", err)
	}
}

func TestServiceCatalogV3LifecycleSchemaMigrationRepairsDriftIdempotently(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	if _, err := surrealdb.Query[any](ctx, s.db, `
DELETE $marker RETURN NONE;
DEFINE FIELD OVERWRITE member_cursor ON service_catalog_v3_lifecycle TYPE string;`, map[string]any{
		"marker": serviceCatalogV3LifecycleSchemaMigrationID(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateServiceCatalogV3LifecycleSchema(ctx); err != nil {
		t.Fatalf("repair lifecycle schema migration: %v", err)
	}
	if err := s.migrateServiceCatalogV3LifecycleSchema(ctx); err != nil {
		t.Fatalf("repeat lifecycle schema migration: %v", err)
	}
	generation := internalServiceCatalogV3Generation(
		t, "example.com/acme/v3-lifecycle-schema", strings.Repeat("b", 40),
	)
	seedServiceCatalogV3Repo(
		t, s, generation.Root.Binding.Repository,
		generation.Root.Binding.Source.Commit,
	)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatalf("publish after lifecycle schema migration: %v", err)
	}
	if _, err := s.ValidateServiceCatalogV3Precious(ctx); err != nil {
		t.Fatalf("validate after lifecycle schema migration: %v", err)
	}
}

func TestServiceCatalogV3StartupRepairIsStrictBoundedAndRemovesOrphan(t *testing.T) {
	t.Run("repairs complete candidate and removes orphan", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		ctx := t.Context()
		generation := internalServiceCatalogV3Generation(
			t, "example.com/acme/v3-startup-repair", strings.Repeat("c", 40),
		)
		seedServiceCatalogV3Repo(
			t, s, generation.Root.Binding.Repository,
			generation.Root.Binding.Source.Commit,
		)
		if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
			t.Fatal(err)
		}
		if _, err := surrealdb.Query[any](ctx, s.db, `
DELETE service_catalog_v3_root_member RETURN NONE;
DELETE service_catalog_v3_lifecycle RETURN NONE;
CREATE $rid CONTENT {
	member_digest: $digest, kind: 'service', ordinal: 0,
	content_bytes: 1, content: 'x', recorded_at: time::now()
} RETURN NONE;`, map[string]any{
			"rid":    serviceCatalogV3MemberID("sha256:" + strings.Repeat("0", 64)),
			"digest": "sha256:" + strings.Repeat("0", 64),
		}); err != nil {
			t.Fatal(err)
		}
		report, err := s.RepairServiceCatalogV3Startup(ctx)
		if err != nil || report.Repaired != 1 || report.OrphansDeleted != 1 {
			t.Fatalf("startup repair = %+v, %v", report, err)
		}
		if _, err := s.GetServiceCatalogV3Candidate(
			ctx, generation.Root.Binding.Repository,
		); err != nil {
			t.Fatalf("repaired candidate: %v", err)
		}
	})

	t.Run("partial candidate refuses without metadata", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		ctx := t.Context()
		generation := internalServiceCatalogV3Generation(
			t, "example.com/acme/v3-startup-partial", strings.Repeat("d", 40),
		)
		seedServiceCatalogV3Repo(
			t, s, generation.Root.Binding.Repository,
			generation.Root.Binding.Source.Commit,
		)
		if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
			t.Fatal(err)
		}
		descriptor := generation.Root.ServiceMembers[0]
		if _, err := surrealdb.Query[any](ctx, s.db, `
DELETE service_catalog_v3_root_member RETURN NONE;
DELETE service_catalog_v3_lifecycle RETURN NONE;
DELETE $member RETURN NONE;`, map[string]any{
			"member": serviceCatalogV3MemberID(descriptor.Digest),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RepairServiceCatalogV3Startup(ctx); !errors.Is(
			err, ErrInvalidServiceCatalogV3Lifecycle,
		) {
			t.Fatalf("partial startup repair = %v", err)
		}
		rows := serviceCatalogV3LifecycleCount(t, s)
		if rows != 0 {
			t.Fatalf("partial startup repaired %d lifecycle rows", rows)
		}
	})

	t.Run("one over upgrade roots refuses before decode", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		ctx := t.Context()
		for index := 0; index <= MaxServiceCatalogV3UpgradeRoots; index++ {
			digest := fmt.Sprintf("sha256:%064x", index+1)
			if _, err := surrealdb.Query[any](ctx, s.db, `CREATE $rid CONTENT {
				root_digest: $digest, repository: 'example.com/acme/one-over',
				root_bytes: 3, root_json: '{}\n', recorded_at: time::now()
			} RETURN NONE`, map[string]any{
				"rid": serviceCatalogV3RootID(digest), "digest": digest,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.RepairServiceCatalogV3Startup(ctx); !errors.Is(
			err, ErrInvalidServiceCatalogV3Lifecycle,
		) {
			t.Fatalf("one-over startup repair = %v", err)
		}
	})
}

func TestServiceCatalogV3LifecyclePinsRestartAndMalformedIsolation(t *testing.T) {
	t.Run("active reference pins and restart resumes collection", func(t *testing.T) {
		if _, err := exec.LookPath("surreal"); err != nil {
			t.Skip("surreal binary not installed")
		}
		ctx := t.Context()
		dataDir := t.TempDir()
		s, err := OpenLocalMemory(ctx, dataDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close(context.Background()) })
		repository := "example.com/acme/v3-lifecycle-pins"
		commit := strings.Repeat("e", 40)
		seedServiceCatalogV3Repo(t, s, repository, commit)
		generations := make([]servicecatalogv3.Generation, 0, 5)
		for index := range 5 {
			generation := internalOperatorServiceCatalogV3Generation(
				t, repository, commit, fmt.Sprintf("pin-v%d", index),
			)
			if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
				t.Fatal(err)
			}
			generations = append(generations, generation)
			time.Sleep(time.Millisecond)
		}
		pinned := generations[0].Root.Digest
		if _, err := surrealdb.Query[any](ctx, s.db, `CREATE service_catalog_v3_state_reference:active_pin CONTENT {
			repository: $repository, root_digest: $root_digest, kind: 'active',
			service_key: 'orders', state_root_digest: $state_root_digest,
			recorded_at: time::now()
		} RETURN NONE`, map[string]any{
			"repository": repository, "root_digest": pinned,
			"state_root_digest": "sha256:" + strings.Repeat("f", 64),
		}); err != nil {
			t.Fatal(err)
		}
		cursor := ""
		pinnedConverged := false
		for range 256 {
			sweep, sweepErr := s.SweepServiceCatalogV3Lifecycle(
				ctx, cursor, 11, 16, ServiceCatalogV3Retained,
			)
			if sweepErr != nil {
				t.Fatal(sweepErr)
			}
			cursor = sweep.Cursor
			report, validateErr := s.ValidateServiceCatalogV3Precious(ctx)
			if validateErr != nil {
				t.Fatal(validateErr)
			}
			if report.HistoricalRoots == 4 && report.CollectingRoots == 0 {
				pinnedConverged = true
				break
			}
		}
		if !pinnedConverged {
			t.Fatal("pinned lifecycle did not converge")
		}
		if _, err := surrealdb.Query[any](ctx, s.db,
			"DELETE service_catalog_v3_state_reference:active_pin RETURN NONE", nil,
		); err != nil {
			t.Fatal(err)
		}
		interrupted := false
		for range 256 {
			sweep, sweepErr := s.SweepServiceCatalogV3Lifecycle(
				ctx, cursor, 11, 16, ServiceCatalogV3Retained,
			)
			if sweepErr != nil {
				t.Fatal(sweepErr)
			}
			cursor = sweep.Cursor
			rows, queryErr := surrealdb.Query[[]serviceCatalogV3LifecycleRec](ctx, s.db, `
				SELECT * FROM service_catalog_v3_lifecycle
				WHERE state = 'collecting' AND member_cursor > 0 LIMIT 1`, nil)
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			if len(firstDomainRows(rows)) == 1 {
				interrupted = true
				break
			}
		}
		if !interrupted {
			t.Fatal("lifecycle did not reach an interrupted member cursor")
		}
		runtime, err := ReadLocalRuntime(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(ctx, runtime.Endpoint, "root", "root", "phebs", "phebs")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close(context.Background()) })
		for range 256 {
			sweep, sweepErr := reopened.SweepServiceCatalogV3Lifecycle(
				ctx, cursor, 11, 16, ServiceCatalogV3Retained,
			)
			if sweepErr != nil {
				t.Fatal(sweepErr)
			}
			cursor = sweep.Cursor
			report, validateErr := reopened.ValidateServiceCatalogV3Precious(ctx)
			if validateErr != nil {
				t.Fatal(validateErr)
			}
			if report.HistoricalRoots == ServiceCatalogV3Retained &&
				report.CollectingRoots == 0 {
				return
			}
		}
		t.Fatal("reopened lifecycle did not converge")
	})

	t.Run("malformed row returns an advancing cursor", func(t *testing.T) {
		s := newServiceCatalogV3InternalStore(t)
		digest := "sha256:" + strings.Repeat("1", 64)
		if _, err := surrealdb.Query[any](t.Context(), s.db, `CREATE $rid CONTENT {
			root_digest: $digest, repository: 'example.com/acme/malformed',
			authority_version_id: 'missing', state: 'historical',
			member_cursor: 0, member_count: 0, logical_bytes: 1,
			root_bytes: 1, member_bytes: 0, recorded_at: time::now(),
			tombstoned_at: NONE
		} RETURN NONE`, map[string]any{
			"rid": serviceCatalogV3LifecycleID(digest), "digest": digest,
		}); err != nil {
			t.Fatal(err)
		}
		sweep, err := s.SweepServiceCatalogV3Lifecycle(
			t.Context(), "", 11, 16, ServiceCatalogV3Retained,
		)
		if !errors.Is(err, ErrInvalidServiceCatalogV3Lifecycle) ||
			sweep.Cursor != digest {
			t.Fatalf("malformed lifecycle = %+v, %v", sweep, err)
		}
	})
}

func newServiceCatalogV3InternalStore(t *testing.T) *Surreal {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	s, err := OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func seedServiceCatalogV3Repo(
	t *testing.T,
	s *Surreal,
	repository, commit string,
) {
	t.Helper()
	if err := s.UpsertRepo(t.Context(), Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(t.Context(), repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func internalServiceCatalogV3Generation(
	t *testing.T,
	repository, commit string,
) servicecatalogv3.Generation {
	t.Helper()
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: commit,
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: repository,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceCommitted, Path: "/catalog.json", Commit: commit,
			CensusDigest: "sha256:" + strings.Repeat("c", 64),
			FileCount:    1, AcceptedFileCount: 1,
		},
		Authority: authority,
	}, servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "orders", Path: "svc", Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return generation
}

func internalOperatorServiceCatalogV3Generation(
	t *testing.T,
	repository, commit, version string,
) servicecatalogv3.Generation {
	t.Helper()
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "catalog-owner", Version: version,
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
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders " + version,
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "orders", Path: "svc", Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return generation
}

func serviceCatalogV3TableCounts(t *testing.T, s *Surreal) [4]int {
	t.Helper()
	results, err := surrealdb.Query[[]struct {
		Roots      int `json:"roots"`
		Members    int `json:"members"`
		Versions   int `json:"versions"`
		Candidates int `json:"candidates"`
	}](t.Context(), s.db, `RETURN [{
		roots: array::len(SELECT id FROM service_catalog_v3_root),
		members: array::len(SELECT id FROM service_catalog_v3_member),
		versions: array::len(SELECT id FROM service_catalog_v3_authority_version),
		candidates: array::len(SELECT id FROM service_catalog_v3_candidate)
	}];`, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		t.Fatalf("count rows = %+v", rows)
	}
	return [4]int{rows[0].Roots, rows[0].Members, rows[0].Versions, rows[0].Candidates}
}
