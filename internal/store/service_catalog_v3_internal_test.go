package store

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

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
