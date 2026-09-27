package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func TestTypedIndexManifestVersions(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		m := archiveTransitionManifestFixture(t)
		if legacy {
			m.Schema = legacyManifestSchema
			m.ExportCommand = slices.Clone(legacyExportCommand)
			m.DerivedExclusions = slices.Clone(legacyDerivedExclusions)
		}
		if err := validateManifest(m); err != nil {
			t.Fatal(err)
		}
		m.Schema = ManifestSchema
		if !legacy {
			m.Schema = legacyManifestSchema
		}
		if err := validateManifest(m); err == nil {
			t.Fatal("accepted a recipe from the other manifest version")
		}
	}
}

// This is an actual six-artifact backup and restore, not a command-string
// assertion. Adversarial derived rows and workspace bytes must not travel.
func TestTypedIndexRegenerateOnRestore(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	config := []byte("neutral typed restore fixture")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenLocalWithConfig(ctx, data, ConfigDigest(config))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	const repo = "example.invalid/typed-restore"
	if err := st.UpsertRepo(ctx, store.Repo{Name: repo}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoIndexed(ctx, repo, strings.Repeat("a", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	tool := typedindex.Tool{Version: "neutral", Digest: archiveTransitionDigest("b")}
	profileRaw, _ := json.Marshal(typedindex.ProfileDefinition{
		Schema: typedindex.ProfileSchema, Name: "reduced", Provider: typedindex.ProviderID,
		Tools:  typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool},
		Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(),
		BundleDigest: archiveTransitionDigest("c"), ImageDigest: archiveTransitionDigest("d"),
	})
	profile, err := typedindex.DecodeProfile(ctx, profileRaw)
	if err != nil {
		t.Fatal(err)
	}
	universe := archiveTransitionDigest("e")
	intent, err := st.InstallTypedProfile(ctx, repo, profile, universe, 0)
	if err != nil {
		t.Fatal(err)
	}
	source, err := st.GetTypedSource(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(typedindex.NewRequest(source, profile, uint64(intent.ProfileEpoch), universe, "before-backup"))
	if _, err := st.EnqueueTypedIndex(ctx, repo, raw); err != nil {
		t.Fatal(err)
	}
	runtime, err := store.ReadLocalRuntime(data)
	if err != nil {
		t.Fatal(err)
	}
	db, err := surrealdb.FromEndpointURLString(ctx, runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: runtime.Pass}); err != nil {
		t.Fatal(err)
	}
	if err := db.Use(ctx, "phebs", "phebs"); err != nil {
		t.Fatal(err)
	}
	const poison = "derived-custody-must-never-be-transported"
	for _, table := range []string{"typed_index_plan", "typed_index_attempt", "typed_index_current", "typed_index_state"} {
		if _, err := surrealdb.Query[any](ctx, db, "CREATE $row SET repository=$repo, body=$body;", map[string]any{
			"row": models.NewRecordID(table, repo), "repo": repo, "body": poison,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(data, "typed-index"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "typed-index", "generated.scip"), []byte(poison), 0o600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(root, "backup")
	opts := Options{DataDir: data, Config: config, PhebsVersion: "typed-restore-test"}
	manifest, err := Create(ctx, BackupOptions{Options: opts, Output: backup})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != ManifestSchema || !slices.Equal(manifest.ExportCommand, exportCommand) {
		t.Fatal("new backup did not bind the derived exclusion recipe")
	}
	export, err := os.ReadFile(filepath.Join(backup, DatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(export), "typed_index_intent") || !strings.Contains(string(export), profile.Digest()) {
		t.Fatal("precious profile intent is missing")
	}
	for _, table := range strings.Split(derivedExportTables, ",") {
		if strings.Contains(string(export), table) {
			t.Fatalf("derived table %s traveled in backup", table)
		}
	}
	for _, artifact := range manifest.Inventory {
		b, err := os.ReadFile(filepath.Join(backup, artifact.Path))
		if err != nil || strings.Contains(string(b), poison) {
			t.Fatalf("derived payload transported in %s: %v", artifact.Path, err)
		}
	}
	opts.DataDir = filepath.Join(root, "restored")
	if _, err := Restore(ctx, RestoreOptions{Options: opts, Backup: backup}); err != nil {
		t.Fatal(err)
	}
	restored, err := store.OpenLocal(ctx, opts.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close(context.Background()) }()
	status, err := restored.GetTypedIndexStatus(ctx, repo)
	if err != nil || !status.RestoreRequired || status.Current != nil || status.Desired != "" {
		t.Fatalf("restored authority is not unavailable: %+v %v", status, err)
	}
	if _, err := restored.EnqueueTypedIndex(ctx, repo, raw); !errors.Is(err, typedindex.Disabled) {
		t.Fatalf("imported request replay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(opts.DataDir, "typed-index")); !os.IsNotExist(err) {
		t.Fatalf("generated filesystem custody survived restore: %v", err)
	}
	next, err := restored.InstallTypedProfile(ctx, repo, profile, universe, intent.ProfileEpoch)
	if err != nil || next.ProfileEpoch != intent.ProfileEpoch+1 || next.RestoreRequired {
		t.Fatalf("trusted profile revalidation: %+v %v", next, err)
	}
	if _, err := restored.EnqueueTypedIndex(ctx, repo, raw); !errors.Is(err, typedindex.Stale) {
		t.Fatalf("old authority replay after revalidation: %v", err)
	}
	fresh, _ := json.Marshal(typedindex.NewRequest(source, profile, uint64(next.ProfileEpoch), universe, "after-restore"))
	if _, err := restored.EnqueueTypedIndex(ctx, repo, fresh); err != nil {
		t.Fatal(err)
	}
}
