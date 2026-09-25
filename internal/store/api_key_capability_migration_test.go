package store

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

type apiKeyCapabilityMigrationMarker struct {
	Version     string    `json:"version"`
	CompletedAt time.Time `json:"completed_at"`
}

func TestAPIKeyCapabilityMigrationIsAdditiveIdempotentAndImmutable(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	createdAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	lastUsedAt := createdAt.Add(time.Hour)
	expiresAt := createdAt.Add(30 * 24 * time.Hour)
	revokedAt := createdAt.Add(2 * time.Hour)
	s, err := OpenLocal(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	results, err := surrealdb.Query[any](ctx, s.db, `
REMOVE EVENT IF EXISTS api_key_capabilities_immutable ON TABLE api_key;
REMOVE EVENT IF EXISTS api_key_legacy_identity_v1 ON TABLE api_key;
REMOVE FIELD capabilities ON api_key;
DELETE $marker;
CREATE $named SET user_id = 'migration-user', name = 'Existing key',
	prefix = 'phebs_existing', hash = 'existing-hash-bytes',
	created_at = $created_at, last_used_at = $last_used_at,
	expires_at = $expires_at;
CREATE $revoked SET user_id = 'migration-user', name = 'Revoked key',
	prefix = 'phebs_revoked', hash = 'revoked-hash-bytes',
	created_at = $created_at, revoked_at = $revoked_at;
CREATE $legacy SET user_id = '', name = 'Legacy config key',
	prefix = 'legacy', hash = 'legacy-hash-bytes',
	created_at = $created_at;`, map[string]any{
		"marker":       models.NewRecordID("store_migration", "api_key_capabilities"),
		"named":        apiKeyID("existing"),
		"revoked":      apiKeyID("revoked"),
		"legacy":       apiKeyID(legacyAPIKeyID),
		"created_at":   createdAt,
		"last_used_at": lastUsedAt,
		"expires_at":   expiresAt,
		"revoked_at":   revokedAt,
	})
	if err != nil {
		t.Fatalf("write pre-capability fixtures: %v", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf("write pre-capability fixture statement %d: %s", index, result.Error.Message)
		}
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	s, err = OpenLocal(ctx, dir)
	if err != nil {
		t.Fatalf("first capability migration reopen: %v", err)
	}
	named, err := s.GetAPIKey(ctx, "existing")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.GetAPIKey(ctx, legacyAPIKeyID)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := s.GetAPIKey(ctx, "revoked")
	if err != nil {
		t.Fatal(err)
	}
	if named.Hash != "existing-hash-bytes" || named.Capabilities == nil ||
		len(named.Capabilities) != 0 || named.ID != "existing" ||
		named.LastUsedAt == nil || !named.LastUsedAt.Equal(lastUsedAt) ||
		named.ExpiresAt == nil || !named.ExpiresAt.Equal(expiresAt) ||
		named.RevokedAt != nil {
		t.Fatalf("migrated existing key = %+v", named)
	}
	if legacy.Hash != "legacy-hash-bytes" || legacy.Capabilities == nil ||
		len(legacy.Capabilities) != 0 || legacy.UserID != "" {
		t.Fatalf("migrated legacy key = %+v", legacy)
	}
	if err := s.SetLegacyAPIKey(ctx, legacy.Hash, createdAt.Add(3*time.Hour)); err != nil {
		t.Fatalf("heal migrated legacy identity: %v", err)
	}
	legacy, err = s.GetAPIKey(ctx, legacyAPIKeyID)
	if err != nil || legacy.UserID != LegacyAPIKeyUserID {
		t.Fatalf("healed legacy key = %+v, %v", legacy, err)
	}
	if revoked.Hash != "revoked-hash-bytes" || revoked.Capabilities == nil ||
		len(revoked.Capabilities) != 0 || revoked.RevokedAt == nil ||
		!revoked.RevokedAt.Equal(revokedAt) {
		t.Fatalf("migrated revoked key = %+v", revoked)
	}
	firstMarker := readAPIKeyCapabilityMigrationMarker(t, s)
	if firstMarker.Version != apiKeyCapabilityMigrationVersion ||
		firstMarker.CompletedAt.IsZero() {
		t.Fatalf("first migration marker = %+v", firstMarker)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	s, err = OpenLocal(ctx, dir)
	if err != nil {
		t.Fatalf("idempotent capability migration reopen: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	secondMarker := readAPIKeyCapabilityMigrationMarker(t, s)
	if secondMarker.Version != firstMarker.Version ||
		!secondMarker.CompletedAt.Equal(firstMarker.CompletedAt) {
		t.Fatalf("idempotent marker changed: first=%+v second=%+v", firstMarker, secondMarker)
	}
	for id, hash := range map[string]string{
		"existing":     "existing-hash-bytes",
		"revoked":      "revoked-hash-bytes",
		legacyAPIKeyID: "legacy-hash-bytes",
	} {
		key, getErr := s.GetAPIKey(ctx, id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if key.Hash != hash || key.Capabilities == nil || len(key.Capabilities) != 0 {
			t.Fatalf("key %q changed after second migration: %+v", id, key)
		}
	}

	if _, err := s.CreateAPIKey(ctx, APIKey{
		ID: "write-capable", UserID: "migration-user",
		Name: "Write-capable", Prefix: "phebs_write", Hash: "write-hash",
		Capabilities: []APIKeyCapability{"investigation:write"},
		CreatedAt:    time.Now().UTC(),
	}); err == nil {
		t.Fatal("retired Investigation write capability was issued")
	}
	assertAPIKeyCapabilityUpdateRejected(t, s, named.ID, []APIKeyCapability{"investigation:write"})
	if err := s.TouchAPIKey(ctx, named.ID, time.Now().UTC()); err != nil {
		t.Fatalf("ordinary immutable-key metadata update: %v", err)
	}
}

func TestLegacyAPIKeyWriterFenceSurvivesPreviousSchemaAndRowDeletion(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	s, err := OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	const hash = "legacy-digest"
	if err := s.SetLegacyAPIKey(ctx, hash, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAPIKey(ctx, legacyAPIKeyID, LegacyAPIKeyUserID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Reapply the exact schema known by the previous writer generation. Its
	// IF NOT EXISTS definition must not remove the unknown identity fence.
	const previousSchema = `
DEFINE FIELD OVERWRITE capabilities ON api_key TYPE array<string> DEFAULT []
	ASSERT $value = [] OR $value = ['investigation:write'];
DEFINE EVENT IF NOT EXISTS api_key_capabilities_immutable ON TABLE api_key
	WHEN $event = 'UPDATE'
	  AND $before.capabilities != NONE
	  AND $before.capabilities != $after.capabilities
	THEN {
		THROW 'phebs-permanent: API key capabilities are immutable'
	};`
	results, err := surrealdb.Query[any](ctx, s.db, "BEGIN;\n"+previousSchema+"\nCOMMIT;", nil)
	if err != nil {
		t.Fatalf("reapply previous schema: %v", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf("reapply previous schema statement %d: %s", index, result.Error.Message)
		}
	}

	previousSet := func(label string) {
		t.Helper()
		results, queryErr := surrealdb.Query[any](ctx, s.db,
			`UPSERT $rid SET user_id = '', name = 'Legacy config key', prefix = 'legacy',
            hash = $hash, capabilities = [],
            created_at = IF created_at = NONE THEN $at ELSE created_at END,
            revoked_at = NONE`, map[string]any{
				"rid": apiKeyID(legacyAPIKeyID), "hash": hash, "at": now,
			})
		failure := ""
		if queryErr != nil {
			failure = queryErr.Error()
		}
		if results != nil {
			for _, result := range *results {
				if result.Error != nil {
					failure += " " + result.Error.Message
				}
			}
		}
		if !strings.Contains(failure, "retired legacy API key writer generation") {
			t.Fatalf("%s previous writer result = %q, want identity-fence refusal", label, failure)
		}
	}

	previousSet("same-hash revoked update")
	key, err := s.GetAPIKey(ctx, legacyAPIKeyID)
	if err != nil {
		t.Fatal(err)
	}
	if key.UserID != LegacyAPIKeyUserID || key.Hash != hash || key.RevokedAt == nil {
		t.Fatalf("previous writer changed revoked legacy key: %+v", key)
	}

	if err := s.SetLegacyAPIKey(ctx, "", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	previousSet("recreate after delete")
	if _, err := s.GetAPIKey(ctx, legacyAPIKeyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("previous writer recreated deleted legacy key: %v", err)
	}

	if err := s.SetLegacyAPIKey(ctx, hash, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("current writer recreate: %v", err)
	}
	key, err = s.GetAPIKey(ctx, legacyAPIKeyID)
	if err != nil {
		t.Fatal(err)
	}
	if key.UserID != LegacyAPIKeyUserID || key.Hash != hash || key.RevokedAt != nil {
		t.Fatalf("current writer recreated legacy key incorrectly: %+v", key)
	}
}

func TestAPIKeyCapabilityMigrationMarkerSkipsAndRefusesFutureVersion(
	t *testing.T,
) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	s, err := OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	marker := readAPIKeyCapabilityMigrationMarker(t, s)
	if marker.Version != apiKeyCapabilityMigrationVersion {
		t.Fatalf("initial migration marker = %+v", marker)
	}
	results, err := surrealdb.Query[any](ctx, s.db, `
REMOVE EVENT IF EXISTS api_key_capabilities_immutable ON TABLE api_key;
REMOVE EVENT IF EXISTS api_key_legacy_identity_v1 ON TABLE api_key;
REMOVE FIELD capabilities ON api_key;
CREATE $probe SET user_id = 'migration-user', name = 'Scan probe',
	prefix = 'phebs_probe', hash = 'probe-hash',
	created_at = time::now();
DEFINE EVENT api_key_capability_migration_scan_trap ON TABLE api_key
	WHEN $event = 'UPDATE'
	THEN {
		THROW 'steady-state migration scanned API keys'
	};`, map[string]any{
		"probe": apiKeyID("steady-state-scan-probe"),
	})
	if err != nil {
		t.Fatalf("install steady-state scan trap: %v", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf(
				"install steady-state scan trap statement %d: %s",
				index,
				result.Error.Message,
			)
		}
	}
	if err := s.migrateAPIKeyCapabilities(ctx); err != nil {
		t.Fatalf("completed migration touched API keys: %v", err)
	}

	const futureVersion = "t21.12-api-key-capabilities-v2"
	results, err = surrealdb.Query[any](
		ctx,
		s.db,
		"UPDATE $marker SET version = $version RETURN NONE",
		map[string]any{
			"marker":  apiKeyCapabilityMigrationStateID(),
			"version": futureVersion,
		},
	)
	if err != nil {
		t.Fatalf("install future migration marker: %v", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf(
				"install future migration marker statement %d: %s",
				index,
				result.Error.Message,
			)
		}
	}
	err = s.migrateAPIKeyCapabilities(ctx)
	if err == nil || !strings.Contains(
		err.Error(),
		`unsupported completion marker version "`+futureVersion+`"`,
	) {
		t.Fatalf("future migration marker error = %v", err)
	}
	future := readAPIKeyCapabilityMigrationMarker(t, s)
	if future.Version != futureVersion ||
		!future.CompletedAt.Equal(marker.CompletedAt) {
		t.Fatalf(
			"future marker was overwritten: before=%+v after=%+v",
			marker,
			future,
		)
	}
}

func TestRetiredInvestigationWriteCapabilityIsClearedOnUpgrade(t *testing.T) {
	ctx := t.Context()
	s, err := OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	created, err := s.CreateAPIKey(ctx, APIKey{
		ID: "retired-write", UserID: "owner", Name: "Existing key",
		Prefix: "phebs_existing", Hash: "retained-hash", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := surrealdb.Query[any](ctx, s.db, `
REMOVE EVENT IF EXISTS api_key_capabilities_immutable ON TABLE api_key;
DEFINE FIELD OVERWRITE capabilities ON api_key TYPE array<string> DEFAULT []
	ASSERT $value = [] OR $value = ['investigation:write'];
UPDATE $key SET capabilities = ['investigation:write'] RETURN NONE;
DELETE $marker RETURN NONE;`, map[string]any{
		"key":    apiKeyID(created.ID),
		"marker": models.NewRecordID("store_migration", "retired_investigation_write"),
	})
	if err != nil || results == nil || len(*results) != 4 {
		t.Fatalf("seed retired key: results=%v err=%v", results, err)
	}
	for index, result := range *results {
		if result.Status != "OK" || result.Error != nil {
			t.Fatalf("seed retired key statement %d: %+v", index, result)
		}
	}
	for pass := 0; pass < 2; pass++ {
		if err := s.applySchema(ctx); err != nil {
			t.Fatalf("upgrade pass %d: %v", pass, err)
		}
		key, err := s.GetAPIKey(ctx, created.ID)
		if err != nil || key.Hash != "retained-hash" || key.Capabilities == nil || len(key.Capabilities) != 0 {
			t.Fatalf("upgraded key pass %d: key=%+v err=%v", pass, key, err)
		}
		assertAPIKeyCapabilityUpdateRejected(t, s, created.ID, []APIKeyCapability{"investigation:write"})
	}
}

func readAPIKeyCapabilityMigrationMarker(
	t *testing.T,
	s *Surreal,
) apiKeyCapabilityMigrationMarker {
	t.Helper()
	results, err := surrealdb.Query[[]apiKeyCapabilityMigrationMarker](
		context.Background(),
		s.db,
		"SELECT version, completed_at FROM $rid",
		map[string]any{
			"rid": models.NewRecordID("store_migration", "api_key_capabilities"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 {
			return result.Result[0]
		}
	}
	t.Fatal("API key capability migration marker not found")
	return apiKeyCapabilityMigrationMarker{}
}

func assertAPIKeyCapabilityUpdateRejected(
	t *testing.T,
	s *Surreal,
	id string,
	capabilities []APIKeyCapability,
) {
	t.Helper()
	results, err := surrealdb.Query[any](
		context.Background(),
		s.db,
		"UPDATE $rid SET capabilities = $capabilities RETURN AFTER",
		map[string]any{
			"rid": apiKeyID(id), "capabilities": capabilities,
		},
	)
	if err == nil {
		for _, result := range *results {
			if result.Error != nil {
				err = errors.New(result.Error.Message)
				break
			}
		}
	}
	if err == nil {
		t.Fatalf("capabilities for %q were mutable", id)
	}
	key, getErr := s.GetAPIKey(context.Background(), id)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if len(key.Capabilities) != 0 {
		t.Fatalf("failed update changed read key: %+v", key)
	}
}
