package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func TestTypedIndexOwnedReplayDeclarations(t *testing.T) {
	// The pre-existing generic nonnegative optional-int recipe stays readable.
	legacy := "DEFINE FIELD OVERWRITE old_epoch ON repo TYPE none | int ASSERT $value = NONE OR $value >= 0 PERMISSIONS FULL;"
	path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT;\n"+legacy+"\n")
	prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
	if err != nil {
		t.Fatal("legacy recipe refused", err)
	}
	if err := prepared.close(); err != nil {
		t.Fatal(err)
	}
	for _, declaration := range []string{
		"DEFINE FIELD OVERWRITE typed_source_epoch ON repo TYPE none | int ASSERT $value = NONE OR $value > 0 PERMISSIONS FULL;",
		"DEFINE FIELD OVERWRITE body ON typed_index_intent TYPE string ASSERT bytes::len(<bytes> $value) <= 24576 PERMISSIONS FULL;",
		"DEFINE FIELD OVERWRITE repository ON typed_index_intent TYPE string ASSERT bytes::len(<bytes> $value) <= 512 PERMISSIONS FULL;",
	} {
		for _, changed := range []string{
			declaration,
			strings.Replace(declaration, " ON ", " ON wrong_", 1),
			strings.Replace(declaration, " OVERWRITE ", " OVERWRITE wrong_", 1),
			strings.Replace(declaration, " ASSERT ", " ASSERT false AND ", 1),
			strings.Replace(declaration, " PERMISSIONS FULL;", " OR true PERMISSIONS FULL;", 1),
			strings.NewReplacer("> 0", "> 1", "<= 24576", "<= 24577", "<= 512", "<= 513").Replace(declaration),
		} {
			path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT;\n"+changed+"\n")
			prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
			if changed == declaration {
				if err != nil {
					t.Fatal("owned declaration refused", err)
				}
				if err := prepared.close(); err != nil {
					t.Fatal(err)
				}
			} else {
				var unsupported *restoreReplayUnsupported
				if prepared != nil || !errors.As(err, &unsupported) {
					t.Fatalf("changed declaration admitted: %q: %v", changed, err)
				}
			}
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
	if !strings.Contains(string(export), "DEFINE TABLE typed_index_intent ") ||
		!strings.Contains(string(export), "id: typed_index_intent:") ||
		!strings.Contains(string(export), profile.Digest()) {
		t.Fatal("precious profile intent is missing")
	}
	// T45.8b's optional repo.latest_typed_job link may name typed_index_job;
	// no derived table definition or row may travel.
	for _, table := range strings.Split(derivedExportTables, ",") {
		for _, marker := range []string{"DEFINE TABLE " + table + " ", "TABLE DATA: " + table + "\n", "id: " + table + ":"} {
			if strings.Contains(string(export), marker) {
				t.Fatalf("derived table %s traveled in backup (%q)", table, marker)
			}
		}
	}
	// Exercise the strict selected path as well as ordinary restore's native
	// fallback. New precious schema must be recognized before any replay starts.
	var database Artifact
	for _, artifact := range manifest.Inventory {
		if artifact.Path == DatabaseName {
			database = artifact
		}
	}
	prepared, err := prepareRestoreReplay(ctx, filepath.Join(backup, DatabaseName), database)
	if err != nil {
		t.Fatal("new backup strict replay preflight", err)
	}
	defer func() { _ = prepared.close() }()
	for {
		if _, err := prepared.next(ctx); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal("new backup strict replay", err)
		}
	}
	if prepared.census.Definitions == 0 || prepared.census.Records == 0 {
		t.Fatal("empty native replay proof")
	}
	if err := prepared.close(); err != nil {
		t.Fatal(err)
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
