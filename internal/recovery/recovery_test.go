package recovery_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/indexer"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

func TestLiveBackupRestoreAndStartupReindex(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	backupDir := filepath.Join(root, "backup")
	origin := makeOrigin(t, root)
	configBytes := []byte(fmt.Sprintf(`server:
  data_dir: %s
sync:
  poll_interval: 10ms
connections:
  - name: recovery-fixture
    type: git
    url: file://%s
`, dataDir, origin))
	cfg, err := config.Parse(configBytes)
	if err != nil {
		t.Fatal(err)
	}
	bin := zoektBinary(t)

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenLocalWithConfig(ctx, dataDir, recovery.ConfigDigest(configBytes))
	if err != nil {
		t.Fatal(err)
	}
	storeClosed := false
	t.Cleanup(func() {
		if !storeClosed {
			_ = st.Close(context.Background())
		}
	})
	names, err := phebssync.SyncConnection(ctx, st, dataDir, cfg.Connections[0], nil)
	if err != nil || len(names) != 1 {
		t.Fatalf("initial sync = %v, %v", names, err)
	}
	if err := (&indexer.Indexer{DataDir: dataDir, Bin: bin, Store: st}).Index(ctx, store.Repo{Name: names[0]}, false); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	indexedBefore, err := st.GetRepo(ctx, names[0])
	if err != nil || indexedBefore.IndexedCommitHash == "" {
		t.Fatalf("initial indexed repo = %+v, %v", indexedBefore, err)
	}
	candidatePointer := store.CandidateManifestPublication{
		Repository:       names[0],
		HeadCommit:       indexedBefore.IndexedCommitHash,
		PolicyDigest:     "sha256:" + strings.Repeat("a", 64),
		ManifestDigest:   "sha256:" + strings.Repeat("b", 64),
		GenerationDigest: "sha256:" + strings.Repeat("c", 64),
		ManifestPath:     candidate.ManifestName(names[0]),
	}
	if err := st.PublishCandidateManifest(ctx, candidatePointer); err != nil {
		t.Fatalf("publish pre-backup candidate pointer: %v", err)
	}
	publishedPointer, err := st.GetCandidateManifestPublication(
		ctx, names[0],
	)
	if err != nil {
		t.Fatalf("read pre-backup candidate pointer: %v", err)
	}
	outcomeScope := store.ExtractionScope{
		Repository: names[0],
		Commit:     indexedBefore.IndexedCommitHash,
		Domain:     "proto-contract",
	}
	outcome := store.ExtractionDomainOutcome{
		Scope:       outcomeScope,
		Disposition: store.DomainOutcomeRetryableFailure,
		Generation: store.ExtractionGenerationIdentity{
			Extractor:        "restore-v1",
			InventoryPolicy:  "gitlink-boundary-v2",
			DependencyDigest: "sha256:" + strings.Repeat("d", 64),
		},
		ReceiptSchema: store.ExtractionOutcomeReceiptSchema,
		Receipt: `{"schema":"` +
			store.ExtractionOutcomeReceiptSchema + `"}`,
	}
	if err := st.RecordExtractionDomainOutcome(ctx, outcome); err != nil {
		t.Fatalf("record pre-backup extraction outcome: %v", err)
	}
	controlScope := outcomeScope
	controlScope.Domain = "grpc-consumer"
	controlOutcome := store.ExtractionDomainOutcome{
		Scope:                   controlScope,
		Disposition:             store.DomainOutcomeTerminalGenerationRefusal,
		CandidateControlFailure: true,
		Generation: store.ExtractionGenerationIdentity{
			CandidateManifestDigest:  publishedPointer.ManifestDigest,
			CandidatePolicyDigest:    publishedPointer.PolicyDigest,
			CandidateControlRevision: publishedPointer.ControlRevision,
			Extractor:                "restore-control-v1",
			InventoryPolicy: "candidate-manifest-v3-" +
				strings.Repeat("b", 64),
			DependencyDigest: "sha256:" + strings.Repeat("e", 64),
		},
		ReceiptSchema: store.ExtractionOutcomeReceiptSchema,
		Receipt: `{"schema":"` +
			store.ExtractionOutcomeReceiptSchema + `"}`,
	}
	if err := st.RecordExtractionDomainOutcome(
		ctx, controlOutcome,
	); err != nil {
		t.Fatalf("record pre-backup control outcome: %v", err)
	}

	manifest, err := recovery.Create(ctx, recovery.BackupOptions{
		Options: recovery.Options{
			DataDir: dataDir, Config: configBytes, PhebsVersion: "test-version",
		},
		Output: backupDir,
		Now:    func() time.Time { return time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("live backup: %v", err)
	}
	assertManifestDigests(t, backupDir, manifest, dataDir, origin, "SURREAL_PASS", `"root"`)
	if _, err := recovery.Verify(backupDir, recovery.Options{
		DataDir: dataDir, Config: configBytes, PhebsVersion: "test-version",
	}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := recovery.Create(ctx, recovery.BackupOptions{
		Options: recovery.Options{DataDir: dataDir, Config: configBytes, PhebsVersion: "test-version"},
		Output:  backupDir,
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second backup error = %v, want existing-output refusal", err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	storeClosed = true
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := recovery.Restore(ctx, recovery.RestoreOptions{
		Options: recovery.Options{
			DataDir: dataDir, Config: configBytes, PhebsVersion: "test-version",
		},
		Backup: backupDir,
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close(context.Background()) }()
	got, err := restored.GetRepo(ctx, names[0])
	if err != nil || got.IndexedCommitHash != indexedBefore.IndexedCommitHash {
		t.Fatalf("restored repo = %+v, %v; want commit %q", got, err, indexedBefore.IndexedCommitHash)
	}
	if _, err := restored.GetCandidateManifestPublication(
		ctx, names[0],
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("restored derived candidate pointer = %v, want ErrNotFound", err)
	}
	restoredOutcome, err := restored.LatestExtractionDomainOutcome(
		ctx, outcomeScope,
	)
	if err != nil ||
		restoredOutcome.Disposition != store.DomainOutcomeRetryableFailure ||
		restoredOutcome.Generation.Extractor != "restore-v1" {
		t.Fatalf("restored extraction outcome = %+v, %v", restoredOutcome, err)
	}
	if _, err := restored.LatestExtractionDomainOutcome(
		ctx, controlScope,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("restored control outcome = %v, want ErrNotFound", err)
	}

	// This is the same boot seam serve executes: reconcile clears an index
	// claim whose derived shard is absent, boot sync recreates the excluded
	// mirror and queues indexing, and the index worker rebuilds the shard.
	report, err := phebssync.ReconcileArtifacts(ctx, restored, dataDir, false)
	if err != nil || report.RevisionRepairs != 1 {
		t.Fatalf("startup reconcile = %+v, %v", report, err)
	}
	if err := phebssync.EnqueueMissing(ctx, restored, cfg); err != nil {
		t.Fatal(err)
	}
	syncJob, err := restored.ClaimJob(ctx, store.JobSync, "restore-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := phebssync.Handler(cfg, restored)(ctx, *syncJob); err != nil {
		t.Fatalf("startup sync: %v", err)
	}
	indexJob, err := restored.ClaimJob(ctx, store.JobIndex, "restore-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&indexer.Indexer{DataDir: dataDir, Bin: bin, Store: restored}).Handle(ctx, *indexJob); err != nil {
		t.Fatalf("startup index: %v", err)
	}
	got, err = restored.GetRepo(ctx, names[0])
	if err != nil || got.IndexedCommitHash != indexedBefore.IndexedCommitHash {
		t.Fatalf("automatically reindexed repo = %+v, %v", got, err)
	}
	shards, err := filepath.Glob(filepath.Join(dataDir, "index", "*.zoekt"))
	if err != nil || len(shards) == 0 {
		t.Fatalf("rebuilt shards = %v, %v", shards, err)
	}

	if _, err := recovery.Restore(ctx, recovery.RestoreOptions{
		Options: recovery.Options{DataDir: dataDir, Config: configBytes, PhebsVersion: "test-version"},
		Backup:  backupDir,
	}); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("second restore error = %v, want non-empty-target refusal", err)
	}
}

func TestFocusedBackupRestorePreservesPublicationBytes(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	backupDir := filepath.Join(root, "backup")
	origin := makeOrigin(t, root)
	repository, err := phebssync.RepoName("file://" + origin)
	if err != nil {
		t.Fatal(err)
	}
	configBytes := []byte(fmt.Sprintf(`server:
  data_dir: %s
analysis_units:
  %q:
    name: recovery-service
    primary: [main.go]
`, dataDir, repository))
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenLocalWithConfig(ctx, dataDir, recovery.ConfigDigest(configBytes))
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = st.Close(context.Background())
		}
	})
	connection := config.Connection{Name: "focused-recovery", Type: "git", URL: "file://" + origin}
	names, err := phebssync.SyncConnection(ctx, st, dataDir, connection, nil)
	if err != nil || len(names) != 1 || names[0] != repository {
		t.Fatalf("sync = %v, %v", names, err)
	}
	scope := analysisunit.Scope{
		Repository: repository, Name: "recovery-service", Primary: []string{"main.go"},
	}
	index := &indexer.Indexer{
		DataDir: dataDir,
		Bin:     zoektBinary(t), FocusedBin: focusedBinary(t),
		Store: st, AnalysisUnits: map[string]analysisunit.Scope{repository: scope},
	}
	if err := index.Index(ctx, store.Repo{Name: repository}, false); err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(dataDir, "index")
	before := focusedPublicationBytes(t, indexDir, repository)
	if err := os.WriteFile(
		filepath.Join(indexDir, focusedindex.PublishingName(repository)),
		[]byte(repository+"\nstale-process\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(
			indexDir,
			"phebs-focus-"+strings.Repeat("f", 64)+".manifest.json",
		),
		[]byte("{invalid\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	backupManifest, err := recovery.Create(ctx, recovery.BackupOptions{
		Options: recovery.Options{
			DataDir: dataDir, Config: configBytes, PhebsVersion: "test-version",
		},
		Output: backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if backupManifest.FocusedIndex != (recovery.FocusedIndexArchiveReport{
		Schema:              recovery.FocusedIndexArchiveReportSchema,
		Publications:        1,
		OmittedPublications: 1,
		StaleMarkers:        1,
	}) {
		t.Fatalf(
			"durable focused-index archive report = %+v",
			backupManifest.FocusedIndex,
		)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(backupDir, recovery.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(
		manifestBytes,
		[]byte(`"omitted_publications":1`),
	) {
		t.Fatalf("backup manifest omitted durable archive report: %s", manifestBytes)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	closed = true
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatal(err)
	}
	if _, err := recovery.Restore(ctx, recovery.RestoreOptions{
		Options: recovery.Options{
			DataDir: dataDir, Config: configBytes, PhebsVersion: "test-version",
		},
		Backup: backupDir,
	}); err != nil {
		t.Fatal(err)
	}
	after := focusedPublicationBytes(t, filepath.Join(dataDir, "index"), repository)
	if len(before) != len(after) {
		t.Fatalf("focused restore inventory = %d, want %d", len(after), len(before))
	}
	for name, want := range before {
		if !bytes.Equal(after[name], want) {
			t.Fatalf("focused restore changed %q", name)
		}
	}
	if _, err := os.Lstat(filepath.Join(
		dataDir, "index", focusedindex.PublishingName(repository),
	)); !os.IsNotExist(err) {
		t.Fatalf("stale focused publication marker was restored: %v", err)
	}
	restored, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close(context.Background()) }()
	report, err := phebssync.ReconcileArtifacts(ctx, restored, dataDir, false)
	if err != nil || report.RevisionRepairs != 0 {
		t.Fatalf("focused startup reconcile = %+v, %v", report, err)
	}
}

func TestRecoveryRefusalsPrecedeExternalWork(t *testing.T) {
	root := t.TempDir()
	existingBackup := filepath.Join(root, "backup")
	if err := os.Mkdir(existingBackup, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := recovery.Create(t.Context(), recovery.BackupOptions{
		Options: recovery.Options{
			DataDir: filepath.Join(root, "missing-data"), PhebsVersion: "test-version",
		},
		Output: existingBackup,
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("backup refusal = %v", err)
	}

	partialTarget := filepath.Join(root, "partial-target")
	if err := os.Mkdir(partialTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partialTarget, "partial"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := recovery.Restore(t.Context(), recovery.RestoreOptions{
		Options: recovery.Options{DataDir: partialTarget, PhebsVersion: "test-version"},
		Backup:  filepath.Join(root, "missing-backup"),
	}); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("restore refusal = %v", err)
	}
}

func TestBackupRefusesConfigThatDidNotStartServer(t *testing.T) {
	dataDir := t.TempDir()
	runtime := store.LocalRuntime{
		Schema: "phebs-surreal-runtime-v1", Token: strings.Repeat("a", 32), PID: os.Getpid(),
		Endpoint: "ws://127.0.0.1:32123", ConfigSHA256: recovery.ConfigDigest([]byte("server config")),
		Surreal: store.SurrealIdentity{
			Path: "/not/reached", Version: "3.2.0", SHA256: "sha256:" + strings.Repeat("b", 64),
		},
	}
	cleanup, err := store.PublishLocalRuntime(dataDir, runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	output := filepath.Join(filepath.Dir(dataDir), "backup")
	_, err = recovery.Create(t.Context(), recovery.BackupOptions{
		Options: recovery.Options{DataDir: dataDir, Config: []byte("different config"), PhebsVersion: "test"},
		Output:  output,
	})
	if err == nil || !strings.Contains(err.Error(), "differs from the live server") {
		t.Fatalf("Create error = %v", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("config mismatch created output: %v", statErr)
	}
}

func assertManifestDigests(t *testing.T, backupDir string, manifest recovery.Manifest, forbidden ...string) {
	t.Helper()
	if manifest.Schema != recovery.ManifestSchema || manifest.Store != store.CurrentStoreIdentity() ||
		manifest.Phebs.SHA256 == "" || manifest.Surreal.SHA256 == "" {
		t.Fatalf("manifest identities = %+v", manifest)
	}
	artifact, err := os.ReadFile(filepath.Join(backupDir, recovery.DatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	artifactSum := sha256.Sum256(artifact)
	if got := "sha256:" + hex.EncodeToString(artifactSum[:]); got != manifest.Inventory[0].SHA256 {
		t.Fatalf("artifact digest = %s, want %s", got, manifest.Inventory[0].SHA256)
	}
	focusedArtifact, err := os.ReadFile(filepath.Join(backupDir, recovery.FocusedIndexName))
	if err != nil {
		t.Fatal(err)
	}
	focusedSum := sha256.Sum256(focusedArtifact)
	if got := "sha256:" + hex.EncodeToString(focusedSum[:]); got != manifest.Inventory[1].SHA256 {
		t.Fatalf("focused artifact digest = %s, want %s", got, manifest.Inventory[1].SHA256)
	}
	digestManifest := manifest
	digestManifest.ManifestSHA256 = ""
	canonical, err := json.Marshal(digestManifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestSum := sha256.Sum256(canonical)
	if got := "sha256:" + hex.EncodeToString(manifestSum[:]); got != manifest.ManifestSHA256 {
		t.Fatalf("manifest digest = %s, want %s", got, manifest.ManifestSHA256)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(backupDir, recovery.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range forbidden {
		if strings.Contains(string(manifestBytes), value) {
			t.Fatalf("manifest leaked %q: %s", value, manifestBytes)
		}
	}
}

func makeOrigin(t *testing.T, root string) string {
	t.Helper()
	origin := filepath.Join(root, "origin")
	if err := os.Mkdir(origin, 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		base := []string{"-c", "user.name=Recovery Test", "-c", "user.email=recovery@example.test", "-C", origin}
		output, err := exec.CommandContext(t.Context(), "git", append(base, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "main.go"), []byte("package main\n\nfunc RecoveryNeedle() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "main.go")
	git("commit", "-m", "recovery fixture")
	return origin
}

func zoektBinary(t *testing.T) string {
	t.Helper()
	if bin, err := indexer.FindBinary(); err == nil {
		return bin
	}
	bin := filepath.Join(t.TempDir(), "zoekt-git-index")
	command := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "github.com/sourcegraph/zoekt/cmd/zoekt-git-index")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build zoekt-git-index: %v\n%s", err, output)
	}
	return bin
}

func focusedBinary(t *testing.T) string {
	t.Helper()
	if bin, err := focusedindex.FindBinary(); err == nil {
		return bin
	}
	bin := filepath.Join(t.TempDir(), "phebs-focused-index")
	command := exec.CommandContext(
		t.Context(), "go", "build", "-o", bin,
		"github.com/bmeddeb/phebs/cmd/phebs-focused-index",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build phebs-focused-index: %v\n%s", err, output)
	}
	return bin
}

func focusedPublicationBytes(
	t *testing.T,
	indexDir, repository string,
) map[string][]byte {
	t.Helper()
	manifest, err := focusedindex.ValidateSelfContained(indexDir, repository)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{focusedindex.ManifestName(repository)}
	for _, member := range manifest.Members {
		names = append(names, member.Name, member.Name+focusedindex.MemberSuffix)
	}
	result := make(map[string][]byte, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(indexDir, name))
		if err != nil {
			t.Fatal(err)
		}
		result[name] = raw
	}
	return result
}

func TestVerifyRejectsUndeclaredEntry(t *testing.T) {
	// The complete live path above supplies a valid fixture. This smaller test
	// pins the inventory check before any manifest or executable inspection.
	backup := t.TempDir()
	for _, name := range []string{recovery.DatabaseName, recovery.ManifestName, "extra"} {
		if err := os.WriteFile(filepath.Join(backup, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := recovery.Verify(backup, recovery.Options{})
	if err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("Verify error = %v", err)
	}
}
