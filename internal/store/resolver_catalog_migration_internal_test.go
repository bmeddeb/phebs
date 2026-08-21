package store

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestResolverCatalogV1MigrationRetiresCurrentAuthority(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	directory := t.TempDir()
	s, err := OpenLocal(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = s.Close(context.Background())
		}
	})
	repository := "github.com/acme/resolver-v1-migration"
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	requireCandidateRawQuery(t, ctx, s, `
CREATE $publication CONTENT {
	repository: $repository,
	head_commit: '1111111111111111111111111111111111111111',
	unit_digest: '', declarations: [], declaration_set_digest: $empty_set,
	candidate_manifest_digest: $digest,
	source_lane_policy: 'candidate-source-lane-base-v1',
	resolver_packs: [], resolver_pack_set_digest: $empty_set,
	catalog_policy_digest: $digest, generation_digest: $digest,
	manifest_digest: $digest, manifest_path: $manifest,
	control_revision: 1, writer_schema: $writer, published_at: time::now()
};
UPDATE $marker SET version = $migration RETURN NONE;`, map[string]any{
		"publication": resolverCatalogPublicationID(repository),
		"repository":  repository,
		"empty_set":   resolverCatalogEmptySetDigestForTest(t),
		"digest":      digest,
		"manifest":    "phebs-resolver-catalog-v1-migration.manifest.json",
		"writer":      resolverCatalogPriorWriterSchema,
		"marker":      resolverCatalogMigrationID(),
		"migration":   resolverCatalogPriorMigration,
	})
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	closed = true

	reopened, err := OpenLocal(ctx, directory)
	if err != nil {
		t.Fatalf("reopen v1 resolver store: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(context.Background()); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	if _, err := reopened.GetResolverCatalogPublication(ctx, repository); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retired resolver pointer = %v, want ErrNotFound", err)
	}
	repo, err := reopened.GetRepo(ctx, repository)
	if err != nil || repo.CallerPublicationRevision != 1 {
		t.Fatalf("retired caller authority revision = %+v, %v; want 1", repo, err)
	}
	type markerRow struct {
		Version string `json:"version"`
	}
	rows, err := surrealdb.Query[[]markerRow](ctx, reopened.db,
		"SELECT version FROM $rid;", map[string]any{"rid": resolverCatalogMigrationID()})
	if err != nil {
		t.Fatal(err)
	}
	values := firstDomainRows(rows)
	if len(values) != 1 || values[0].Version != resolverCatalogWriterMigrationVersion {
		t.Fatalf("migration marker = %+v", values)
	}
	if err := reopened.migrateResolverCatalogWriter(ctx); err != nil {
		t.Fatalf("replay v2 resolver migration: %v", err)
	}
	repo, err = reopened.GetRepo(ctx, repository)
	if err != nil || repo.CallerPublicationRevision != 1 {
		t.Fatalf("replayed caller authority revision = %+v, %v; want unchanged", repo, err)
	}

	requireCandidateRawQuery(t, ctx, reopened, `
CREATE $publication CONTENT {
	repository: 'github.com/acme/resolver-mixed-writer',
	head_commit: '1111111111111111111111111111111111111111',
	unit_digest: '', declarations: [], declaration_set_digest: $empty_set,
	candidate_manifest_digest: $digest,
	source_lane_policy: 'candidate-source-lane-base-v1',
	resolver_packs: [], resolver_pack_set_digest: $empty_set,
	catalog_policy_digest: $digest, generation_digest: $digest,
	manifest_digest: $digest, manifest_path: $manifest,
	control_revision: 1, writer_schema: $writer, published_at: time::now()
};`, map[string]any{
		"publication": resolverCatalogPublicationID("github.com/acme/resolver-mixed-writer"),
		"empty_set":   resolverCatalogEmptySetDigestForTest(t),
		"digest":      "sha256:" + strings.Repeat("b", 64),
		"manifest":    "phebs-resolver-catalog-mixed.manifest.json",
		"writer":      resolverCatalogPriorWriterSchema,
	})
	if err := reopened.migrateResolverCatalogWriter(ctx); err == nil ||
		!strings.Contains(err.Error(), "mixed resolver catalog publication generations") {
		t.Fatalf("mixed resolver writer migration = %v, want permanent refusal", err)
	}
}

func resolverCatalogEmptySetDigestForTest(t *testing.T) string {
	t.Helper()
	digest, err := resolverCatalogDeclarationSetDigest(
		[]ResolverCatalogDeclarationPublication{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
