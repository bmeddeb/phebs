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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/indexer"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

func TestLiveBackupRestoreAndStartupExactSearchRecovery(t *testing.T) {
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
	serviceCatalogBeforeRestore := recoveryServiceCatalogPublication(
		t, names[0], indexedBefore.IndexedCommitHash,
		filepath.Join(root, "service-catalog.json"),
	)
	if err := st.PublishServiceCatalog(ctx, serviceCatalogBeforeRestore); err != nil {
		t.Fatalf("publish pre-backup service catalog: %v", err)
	}
	serviceCatalogBeforeRestore, err = requireRecoveryServiceCatalog(
		ctx, st, names[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReconcileServiceStates(ctx, serviceCatalogBeforeRestore); err != nil {
		t.Fatalf("reconcile pre-backup service state: %v", err)
	}
	serviceStateBeforeRestore, err := st.GetServiceState(ctx, names[0], "recovery")
	if err != nil {
		t.Fatalf("read pre-backup service state: %v", err)
	}
	serviceStateSummaryBeforeRestore, err := st.GetServiceStateSummary(ctx, names[0])
	if err != nil {
		t.Fatalf("read pre-backup service summary: %v", err)
	}
	if err := st.ActivateService(ctx, servicecatalog.ServiceActivation{
		Repository: names[0], ServiceKey: serviceStateBeforeRestore.ServiceKey,
		Incarnation:               serviceStateBeforeRestore.Incarnation,
		DesiredGeneration:         serviceStateBeforeRestore.DesiredGeneration,
		StateControlRevision:      serviceStateBeforeRestore.ControlRevision,
		RepositoryControlRevision: serviceStateSummaryBeforeRestore.ControlRevision,
	}); err != nil {
		t.Fatalf("activate pre-backup service state: %v", err)
	}
	serviceStateBeforeRestore, err = st.GetServiceState(ctx, names[0], "recovery")
	if err != nil {
		t.Fatal(err)
	}
	serviceStateSummaryBeforeRestore, err = st.GetServiceStateSummary(ctx, names[0])
	if err != nil {
		t.Fatal(err)
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
	catalogIdentity, err := resolvercatalog.NewIdentity(
		names[0], indexedBefore.IndexedCommitHash, "",
		publishedPointer.ManifestDigest, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalogRoot := filepath.Join(dataDir, "resolver-catalogs")
	catalogStage, err := resolvercatalog.NewStage(catalogRoot, catalogIdentity)
	if err != nil {
		t.Fatal(err)
	}
	catalogPrepared, err := catalogStage.Seal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	catalogState, err := catalogPrepared.Install(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishResolverCatalog(ctx, store.ResolverCatalogPublication{
		Repository: names[0], HeadCommit: indexedBefore.IndexedCommitHash,
		Declarations:            []store.ResolverCatalogDeclarationPublication{},
		DeclarationSetDigest:    catalogState.DeclarationSetDigest,
		CandidateManifestDigest: catalogState.CandidateManifestDigest,
		SourceLanePolicy:        catalogState.SourceLanePolicy,
		ResolverPacks:           []store.ResolverCatalogPack{},
		ResolverPackSetDigest:   catalogState.ResolverPackSetDigest,
		CatalogPolicyDigest:     catalogState.CatalogPolicyDigest,
		GenerationDigest:        catalogState.GenerationDigest,
		ManifestDigest:          catalogState.ManifestDigest,
		AuthorityDigest:         catalogState.AuthorityDigest,
		ManifestPath:            catalogState.Manifest,
	}); err != nil {
		t.Fatalf("publish pre-backup resolver catalog pointer: %v", err)
	}
	publishedCatalog, err := st.GetResolverCatalogPublication(ctx, names[0])
	if err != nil {
		t.Fatalf("read pre-backup resolver catalog pointer: %v", err)
	}
	if err := resolvercatalog.ClearPublishing(catalogRoot, names[0]); err != nil {
		t.Fatal(err)
	}
	callerLeafGeneration, err := callerleaf.NewGenerationIdentity(
		callerleaf.GenerationIdentity{
			Repository: names[0], HeadCommit: indexedBefore.IndexedCommitHash,
			UnitDigest:               publishedPointer.UnitDigest,
			DeclarationSetDigest:     publishedCatalog.DeclarationSetDigest,
			CandidateManifestDigest:  publishedPointer.ManifestDigest,
			CandidatePolicyDigest:    publishedPointer.PolicyDigest,
			ResolverGenerationDigest: publishedCatalog.GenerationDigest,
			ResolverManifestDigest:   publishedCatalog.AuthorityDigest,
			Extractors: []callerleaf.ExtractorIdentity{{
				Domain: "grpc-caller", Version: "1.0.0",
				LeafAdapterVersion: callerleaf.LeafAdapterV1,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	callerLeafPair, err := callerleaf.NewPairIdentity(
		callerLeafGeneration, "grpc-caller", "1.0.0",
		callerleaf.LeafDescriptor{
			Name:    "phebs-candidate-neutral-v4-caller-000001.ndjson",
			Ordinal: 0, Prefix: "00", PrefixBits: 2, RecordCount: 1,
			DeclaredBytes: 32, ContentBytes: 64,
			ContentDigest: "sha256:" + strings.Repeat("8", 64),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	callerGeneration := store.CallerGenerationIdentity{
		Repository: names[0], HeadCommit: indexedBefore.IndexedCommitHash,
		UnitDigest:               callerLeafGeneration.UnitDigest,
		DeclarationSetDigest:     callerLeafGeneration.DeclarationSetDigest,
		CandidateManifestDigest:  callerLeafGeneration.CandidateManifestDigest,
		CandidatePolicyDigest:    callerLeafGeneration.CandidatePolicyDigest,
		CandidateControlRevision: publishedPointer.ControlRevision,
		ResolverGenerationDigest: callerLeafGeneration.ResolverGenerationDigest,
		ResolverManifestDigest:   callerLeafGeneration.ResolverManifestDigest,
		ResolverControlRevision:  publishedCatalog.ControlRevision,
		ResolverWriterSchema:     publishedCatalog.WriterSchema,
		SourceLanePolicy:         callerLeafGeneration.SourceLanePolicy,
		CallerPolicyDigest:       callerLeafGeneration.CallerPolicyDigest,
		ExtractorSetDigest:       callerLeafGeneration.ExtractorSetDigest,
	}
	callerPair := store.CallerLeafPair{
		Domain: callerLeafPair.Domain, ExtractorVersion: callerLeafPair.ExtractorVersion,
		LeafAdapterVersion: callerLeafPair.LeafAdapterVersion,
		LeafOrdinal:        callerLeafPair.Leaf.Ordinal, LeafPrefix: callerLeafPair.Leaf.Prefix,
		LeafPrefixBits:         callerLeafPair.Leaf.PrefixBits,
		CandidateMemberName:    callerLeafPair.Leaf.Name,
		CandidateRecordCount:   callerLeafPair.Leaf.RecordCount,
		CandidateDeclaredBytes: callerLeafPair.Leaf.DeclaredBytes,
		CandidateContentBytes:  callerLeafPair.Leaf.ContentBytes,
		CandidateContentDigest: callerLeafPair.Leaf.ContentDigest,
	}
	callerJob, err := st.ClaimJob(ctx, store.JobCallerLeaf, "recovery-caller")
	if err != nil {
		t.Fatalf("claim pre-backup caller leaf job: %v", err)
	}
	if err := st.SetJobStatus(ctx, *callerJob, store.StatusRunning, ""); err != nil {
		t.Fatalf("run pre-backup caller leaf job: %v", err)
	}
	callerJob.Status = store.StatusRunning
	callerArtifactRoot := filepath.Join(dataDir, "caller-leaves")
	callerStage, err := callerleaf.NewStage(
		callerArtifactRoot, callerLeafGeneration, callerLeafPair,
	)
	if err != nil {
		t.Fatal(err)
	}
	callerPreparedLeaf, err := callerStage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	callerLeafPublication, err := callerPreparedLeaf.Install(ctx)
	if err != nil {
		t.Fatal(err)
	}
	callerLeafReceipt := callerLeafPublication.Receipt()
	callerStoreReceipt := store.CallerLeafArtifactReceipt{
		ArtifactName: callerLeafReceipt.Name, ArtifactCount: 1,
		ResultCount:           callerLeafReceipt.ResultCount,
		AbstentionCount:       callerLeafReceipt.AbstentionCount,
		CanonicalBytes:        callerLeafReceipt.ContentBytes,
		StagingBytes:          callerLeafReceipt.StagingBytes,
		ContentDigest:         callerLeafReceipt.ContentDigest,
		MetadataDigest:        callerLeafReceipt.MetadataDigest,
		ExcludedGoTestRecords: callerLeafReceipt.ExcludedGoTestRecords,
		SourceBlobReads:       callerLeafReceipt.SourceBlobReads,
		SourceBlobBytes:       callerLeafReceipt.SourceBlobBytes,
		OutOfLeafReads:        callerLeafReceipt.OutOfLeafReads,
	}
	if err := st.RecordCallerLeafOutcome(ctx, *callerJob, store.CallerLeafOutcome{
		Generation: callerGeneration, Pair: callerPair,
		Disposition: store.CallerLeafSucceeded, Receipt: &callerStoreReceipt,
	}); err != nil {
		t.Fatalf("record pre-backup caller leaf outcome: %v", err)
	}
	if err := st.RecordCallerGenerationAdmission(
		ctx,
		*callerJob,
		store.CallerGenerationAdmission{
			Generation:  callerGeneration,
			Disposition: store.CallerGenerationAdmitted,
		},
		[]store.CallerLeafPair{callerPair},
	); err != nil {
		t.Fatalf("record pre-backup caller generation admission: %v", err)
	}
	callerManifest, err := callerpublication.BuildManifest(
		callerLeafGeneration, []callerpublication.PairReceipt{{
			Pair: callerLeafPair, Receipt: callerLeafReceipt,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	callerPrepared, err := callerpublication.Prepare(ctx, callerArtifactRoot, callerManifest)
	if err != nil {
		t.Fatal(err)
	}
	callerPublication, err := callerPrepared.Publish(
		ctx, func(ctx context.Context, state callerpublication.State) error {
			return st.PublishCallerGeneration(ctx, *callerJob, store.CallerGenerationPublication{
				Generation: callerGeneration,
				Pairs: []store.CallerGenerationPairPublication{{
					Pair: callerPair, Receipt: callerStoreReceipt,
				}},
				ManifestDigest: state.ManifestDigest, ManifestPath: state.Manifest,
			})
		},
	)
	if err != nil || callerPublication == nil || !callerPublication.Current() {
		t.Fatalf("publish pre-backup caller generation = %+v, %v", callerPublication, err)
	}
	publishedCaller, err := st.GetCallerGenerationPublication(ctx, names[0])
	if err != nil {
		t.Fatalf("read pre-backup caller pointer: %v", err)
	}
	callerRevisionBeforeRestore := publishedCaller.PublicationRevision
	callerBytesBeforeRestore := callerPublicationBytes(t, callerArtifactRoot, callerManifest)
	if err := os.WriteFile(
		filepath.Join(callerArtifactRoot, "must-not-be-archived.ndjson"),
		[]byte("derived caller bytes\n"), 0o600,
	); err != nil {
		t.Fatal(err)
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
			InventoryPolicy: "candidate-manifest-v4-" +
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
	const (
		retainedJobTarget = "recovery-fixture/retained-terminal-job"
		retainedJobError  = "retained backup diagnostic: exact terminal failure"
	)
	createdRetainedJob, err := st.CreateJob(
		ctx, store.JobFetch, retainedJobTarget,
	)
	if err != nil {
		t.Fatalf("create retained terminal job: %v", err)
	}
	claimedRetainedJob, err := st.ClaimJob(
		ctx, store.JobFetch, "recovery-terminal-fixture",
	)
	if err != nil {
		t.Fatalf("claim retained terminal job: %v", err)
	}
	if claimedRetainedJob.ID != createdRetainedJob.ID {
		t.Fatalf(
			"claimed retained job %q, want created job %q",
			claimedRetainedJob.ID, createdRetainedJob.ID,
		)
	}
	if err := st.SetJobStatus(
		ctx, *claimedRetainedJob, store.StatusRunning, "",
	); err != nil {
		t.Fatalf("run retained terminal job: %v", err)
	}
	if err := st.SetJobStatus(
		ctx, *claimedRetainedJob, store.StatusFailed, retainedJobError,
	); err != nil {
		t.Fatalf("fail retained terminal job: %v", err)
	}
	retainedJobBeforeRestore := requireRecoveryJob(
		t, ctx, st, store.JobFetch, store.StatusFailed, createdRetainedJob.ID,
	)
	if retainedJobBeforeRestore.Target != retainedJobTarget ||
		retainedJobBeforeRestore.Status != store.StatusFailed ||
		retainedJobBeforeRestore.Error != retainedJobError ||
		retainedJobBeforeRestore.FinishedAt == nil {
		t.Fatalf("pre-backup retained terminal job = %+v", retainedJobBeforeRestore)
	}
	if err := st.CompareAndSwapLifecycleCursor(
		ctx, "rotation", 0, "generation-schedules",
	); err != nil {
		t.Fatalf("persist pre-backup lifecycle cursor: %v", err)
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
	if _, err := os.Lstat(filepath.Join(backupDir, "caller-leaves")); !os.IsNotExist(err) {
		t.Fatalf("backup copied caller-leaf root outside its archive: %v", err)
	}
	if manifest.CallerPublication.Schema != recovery.CallerPublicationArchiveReportSchema ||
		manifest.CallerPublication.Publications != 1 ||
		manifest.CallerPublication.OmittedPublications != 0 ||
		manifest.CallerPublication.OmittedArtifacts != 0 ||
		manifest.CallerPublication.StaleMarkers != 0 {
		t.Fatalf("backup caller-publication report = %+v", manifest.CallerPublication)
	}
	exclusions := strings.Join(manifest.DerivedExclusions, "\n")
	if !strings.Contains(exclusions, "caller-leaves/ invalid") ||
		strings.Contains(exclusions, "caller-leaves/ (derived direct") {
		t.Fatalf("backup manifest caller classification = %+v", manifest.DerivedExclusions)
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
	serviceCatalogAfterRestore, err := requireRecoveryServiceCatalog(
		ctx, restored, names[0],
	)
	if err != nil ||
		serviceCatalogAfterRestore.GenerationDigest != serviceCatalogBeforeRestore.GenerationDigest ||
		serviceCatalogAfterRestore.ControlRevision != serviceCatalogBeforeRestore.ControlRevision ||
		!serviceCatalogAfterRestore.PublishedAt.Equal(serviceCatalogBeforeRestore.PublishedAt) ||
		!bytes.Equal(serviceCatalogAfterRestore.Canonical, serviceCatalogBeforeRestore.Canonical) {
		t.Fatalf(
			"restored service catalog = %+v, %v; want %+v",
			serviceCatalogAfterRestore, err, serviceCatalogBeforeRestore,
		)
	}
	serviceStateAfterRestore, err := restored.GetServiceState(
		ctx, names[0], "recovery",
	)
	if err != nil || !reflect.DeepEqual(serviceStateAfterRestore, serviceStateBeforeRestore) {
		t.Fatalf(
			"restored service state = %+v, %v; want %+v",
			serviceStateAfterRestore, err, serviceStateBeforeRestore,
		)
	}
	serviceStateSummaryAfterRestore, err := restored.GetServiceStateSummary(ctx, names[0])
	if err != nil || !reflect.DeepEqual(
		serviceStateSummaryAfterRestore, serviceStateSummaryBeforeRestore,
	) {
		t.Fatalf(
			"restored service summary = %+v, %v; want %+v",
			serviceStateSummaryAfterRestore, err, serviceStateSummaryBeforeRestore,
		)
	}
	retainedJobAfterRestore := requireRecoveryJob(
		t, ctx, restored, store.JobFetch, store.StatusFailed,
		retainedJobBeforeRestore.ID,
	)
	assertRecoveryJobEqual(
		t, retainedJobBeforeRestore, retainedJobAfterRestore,
	)
	lifecycleCursor, lifecycleRevision, err := restored.GetLifecycleCursor(ctx, "rotation")
	if err != nil || lifecycleCursor != "generation-schedules" || lifecycleRevision != 1 {
		t.Fatalf("restored lifecycle cursor = %q/%d, %v", lifecycleCursor, lifecycleRevision, err)
	}
	if got.CallerPublicationRevision != int64(callerRevisionBeforeRestore)+1 {
		t.Fatalf(
			"restored caller publication revision = %d, want exactly %d",
			got.CallerPublicationRevision, callerRevisionBeforeRestore+1,
		)
	}
	if _, err := restored.GetCandidateManifestPublication(
		ctx, names[0],
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("restored derived candidate pointer = %v, want ErrNotFound", err)
	}
	if _, err := restored.GetResolverCatalogPublication(
		ctx, names[0],
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("restored derived resolver catalog pointer = %v, want ErrNotFound", err)
	}
	if _, err := restored.GetCallerGenerationPublication(
		ctx, names[0],
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("restored derived caller pointer = %v, want ErrNotFound", err)
	}
	if got, err := restored.ListCallerLeafOutcomes(
		ctx, callerGeneration,
	); err != nil || len(got) != 0 {
		t.Fatalf("restored derived caller leaf outcomes = %+v, %v; want none", got, err)
	}
	if got, err := restored.ListCallerGenerationAdmissions(
		ctx, names[0],
	); err != nil || len(got) != 0 {
		t.Fatalf("restored derived caller admissions = %+v, %v; want none", got, err)
	}
	if _, err := os.Lstat(filepath.Join(
		dataDir, "caller-leaves", "must-not-be-archived.ndjson",
	)); !os.IsNotExist(err) {
		t.Fatalf("restore materialized excluded caller-leaf artifact: %v", err)
	}
	callerBytesAfterRestore := callerPublicationBytes(
		t, filepath.Join(dataDir, "caller-leaves"), callerManifest,
	)
	if len(callerBytesAfterRestore) != len(callerBytesBeforeRestore) {
		t.Fatalf(
			"restored caller byte inventory = %d, want %d",
			len(callerBytesAfterRestore), len(callerBytesBeforeRestore),
		)
	}
	for name, want := range callerBytesBeforeRestore {
		if !bytes.Equal(callerBytesAfterRestore[name], want) {
			t.Fatalf("restored caller publication changed %q", name)
		}
	}
	if opened, err := callerpublication.Open(
		ctx, filepath.Join(dataDir, "caller-leaves"), callerManifest.State(),
	); err != nil || !opened.Current() {
		t.Fatalf("restored pointerless caller publication = current:%v, %v", opened != nil && opened.Current(), err)
	}
	if _, err := resolvercatalog.Open(
		ctx, filepath.Join(dataDir, "resolver-catalogs"), catalogState,
	); err != nil {
		t.Fatalf("restored exact resolver catalog bytes: %v", err)
	}
	catalogReport, err := resolvercatalog.Reconcile(
		ctx, filepath.Join(dataDir, "resolver-catalogs"), restored, nil,
	)
	if err != nil || catalogReport.ReplacementsQueued != 1 ||
		catalogReport.OrphansObserved != 1 {
		t.Fatalf("restored catalog reconciliation = %+v, %v", catalogReport, err)
	}
	catalogJobs, err := restored.ListJobs(
		ctx, store.JobResolverCatalog, store.StatusPending,
	)
	if err != nil || len(catalogJobs) != 1 || !catalogJobs[0].Force {
		t.Fatalf("restored catalog jobs = %+v, %v", catalogJobs, err)
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

	// This is the same boot seam serve executes: the exact v2 source/search
	// generation survives restore, so reconcile retains the indexed claim and
	// boot sync recreates the excluded mirror and queues the ordinary index
	// handoff, whose exact no-op path must retain the restored generation.
	report, err := phebssync.ReconcileArtifacts(ctx, restored, dataDir, false)
	if err != nil || report.RevisionRepairs != 0 {
		t.Fatalf("startup reconcile = %+v, %v", report, err)
	}
	revisions := indexedBefore.IndexedRevisions
	if len(revisions) == 0 {
		revisions = []store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD", Commit: indexedBefore.IndexedCommitHash,
		}}
	}
	restoredSearch, err := focusedindex.ValidateRepositorySearchGeneration(
		ctx, filepath.Join(dataDir, "index"), names[0], revisions,
	)
	if err != nil {
		t.Fatalf("restored exact repository search generation: %v", err)
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
	if err := (&indexer.Indexer{DataDir: dataDir, Bin: bin, Store: restored}).Handle(
		ctx, *indexJob,
	); err != nil {
		t.Fatalf("startup index no-op: %v", err)
	}
	restoredRoot, err := focusedindex.ReadSearchGenerationRoot(
		filepath.Join(dataDir, "index"), names[0],
	)
	if err != nil || restoredRoot.Current.GenerationDigest != restoredSearch.Digest ||
		restoredRoot.Prior != nil {
		t.Fatalf("restored search lifecycle root = %+v, %v", restoredRoot, err)
	}
	afterSearch, err := focusedindex.ValidateRepositorySearchGeneration(
		ctx, filepath.Join(dataDir, "index"), names[0], revisions,
	)
	if err != nil || afterSearch.Digest != restoredSearch.Digest {
		t.Fatalf("post-sync restored search = %+v, %v; want %q", afterSearch, err, restoredSearch.Digest)
	}
	got, err = restored.GetRepo(ctx, names[0])
	if err != nil || got.IndexedCommitHash != indexedBefore.IndexedCommitHash {
		t.Fatalf("exactly restored indexed repo = %+v, %v", got, err)
	}
	shards, err := filepath.Glob(filepath.Join(dataDir, "index", "*.zoekt"))
	if err != nil || len(shards) == 0 {
		t.Fatalf("restored shards = %v, %v", shards, err)
	}

	if _, err := recovery.Restore(ctx, recovery.RestoreOptions{
		Options: recovery.Options{DataDir: dataDir, Config: configBytes, PhebsVersion: "test-version"},
		Backup:  backupDir,
	}); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("second restore error = %v, want non-empty-target refusal", err)
	}
}

func requireRecoveryJob(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
	kind store.JobKind,
	status store.JobStatus,
	id string,
) store.Job {
	t.Helper()
	jobs, err := s.ListJobs(ctx, kind, status)
	if err != nil {
		t.Fatalf("list %s %s jobs: %v", kind, status, err)
	}
	for _, job := range jobs {
		if job.ID == id {
			return job
		}
	}
	t.Fatalf("%s job %q is missing from %s history", status, id, kind)
	return store.Job{}
}

func assertRecoveryJobEqual(t *testing.T, before, after store.Job) {
	t.Helper()
	equalOptionalTime := func(left, right *time.Time) bool {
		if left == nil || right == nil {
			return left == nil && right == nil
		}
		return left.Equal(*right)
	}
	if before.ID != after.ID || before.Kind != after.Kind ||
		before.Target != after.Target ||
		before.TargetTruncated != after.TargetTruncated ||
		before.Status != after.Status ||
		before.Attempts != after.Attempts || before.Error != after.Error ||
		before.ErrorTruncated != after.ErrorTruncated ||
		!before.CreatedAt.Equal(after.CreatedAt) ||
		!equalOptionalTime(before.NotBefore, after.NotBefore) ||
		before.ClaimedBy != after.ClaimedBy ||
		before.ClaimedByTruncated != after.ClaimedByTruncated ||
		!equalOptionalTime(before.ClaimedAt, after.ClaimedAt) ||
		!equalOptionalTime(before.HeartbeatAt, after.HeartbeatAt) ||
		!equalOptionalTime(before.FinishedAt, after.FinishedAt) ||
		before.Force != after.Force || before.LeaseToken != after.LeaseToken {
		t.Fatalf("restored terminal job changed: before=%+v after=%+v", before, after)
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
	resolverArtifact, err := os.ReadFile(filepath.Join(backupDir, recovery.ResolverCatalogName))
	if err != nil {
		t.Fatal(err)
	}
	resolverSum := sha256.Sum256(resolverArtifact)
	if got := "sha256:" + hex.EncodeToString(resolverSum[:]); got != manifest.Inventory[2].SHA256 {
		t.Fatalf("resolver artifact digest = %s, want %s", got, manifest.Inventory[2].SHA256)
	}
	callerArtifact, err := os.ReadFile(filepath.Join(backupDir, recovery.CallerPublicationName))
	if err != nil {
		t.Fatal(err)
	}
	callerSum := sha256.Sum256(callerArtifact)
	if got := "sha256:" + hex.EncodeToString(callerSum[:]); got != manifest.Inventory[3].SHA256 {
		t.Fatalf("caller artifact digest = %s, want %s", got, manifest.Inventory[3].SHA256)
	}
	observationArtifact, err := os.ReadFile(filepath.Join(backupDir, recovery.ObservationPublicationName))
	if err != nil {
		t.Fatal(err)
	}
	observationSum := sha256.Sum256(observationArtifact)
	if got := "sha256:" + hex.EncodeToString(observationSum[:]); got != manifest.Inventory[4].SHA256 {
		t.Fatalf("observation artifact digest = %s, want %s", got, manifest.Inventory[4].SHA256)
	}
	relationshipArtifact, err := os.ReadFile(filepath.Join(backupDir, recovery.RelationshipPublicationName))
	if err != nil {
		t.Fatal(err)
	}
	relationshipSum := sha256.Sum256(relationshipArtifact)
	if got := "sha256:" + hex.EncodeToString(relationshipSum[:]); got != manifest.Inventory[5].SHA256 {
		t.Fatalf("relationship artifact digest = %s, want %s", got, manifest.Inventory[5].SHA256)
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

func recoveryServiceCatalogPublication(
	t *testing.T,
	repository, commit, sourcePath string,
) servicecatalog.Publication {
	t.Helper()
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityCommitted,
			ID:   "recovery-catalog", Version: commit,
		},
		Services: []servicecatalog.Service{{
			Key: "recovery", DisplayName: "Recovery",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "recovery", Path: "main.go",
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
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
		SourceKind: servicecatalog.SourceCommitted, SourcePath: sourcePath,
		SourceCommit:       commit,
		SourceCensusDigest: "sha256:" + strings.Repeat("e", 64),
		SourceFileCount:    1, AcceptedFileCount: 1,
		Authority: catalog.Authority, CatalogDigest: catalogDigest,
		Canonical: canonical,
	}
	publication.GenerationDigest, err =
		servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	return publication
}

func requireRecoveryServiceCatalog(
	ctx context.Context,
	state store.ServiceCatalogPublicationStore,
	repository string,
) (servicecatalog.Publication, error) {
	publication, err := state.GetServiceCatalog(ctx, repository)
	if err != nil {
		return servicecatalog.Publication{}, err
	}
	return *publication, nil
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

func callerPublicationBytes(
	t *testing.T,
	root string,
	manifest callerpublication.Manifest,
) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	for _, reference := range callerpublication.ArtifactRefs(manifest) {
		name := reference.RelativePath()
		if name == "" {
			t.Fatal("caller publication returned an invalid artifact reference")
		}
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
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

func TestVerifyContextHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := recovery.VerifyContext(
		ctx, t.TempDir(), recovery.Options{},
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyContext cancellation = %v", err)
	}
}
