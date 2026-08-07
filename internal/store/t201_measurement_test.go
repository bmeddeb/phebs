package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

const (
	t201TargetFacts          = maxEvidenceFactsPerRun
	t201AdmittedRunRows      = maxEvidenceRowsPerRun
	t201AdmittedReferenceMax = maxEvidenceReferenceEdges
	t201BatchFacts           = maxEvidenceFactsPerChunk
)

type t201Metrics struct {
	Schema                      string `json:"schema"`
	GoVersion                   string `json:"go_version"`
	GOOS                        string `json:"goos"`
	GOARCH                      string `json:"goarch"`
	GOMAXPROCS                  int    `json:"gomaxprocs"`
	SurrealVersion              string `json:"surreal_version"`
	SurrealSHA256               string `json:"surreal_sha256"`
	StoreSchema                 string `json:"store_schema"`
	EvidenceFormat              string `json:"evidence_format"`
	WriterGuardEvent            string `json:"writer_guard_event"`
	AdmissionRows               int    `json:"admission_rows"`
	ReferenceEdgeLimit          int    `json:"reference_edge_limit"`
	TargetFacts                 int    `json:"target_facts"`
	TargetRows                  int    `json:"target_rows"`
	ReferenceEdges              int    `json:"reference_edges"`
	AppendQueryCount            int    `json:"append_query_count"`
	AppendMaxTransactionBytes   int    `json:"append_max_transaction_bytes"`
	AppendWallMilliseconds      int64  `json:"append_wall_ms"`
	AppendGoAllocatedBytes      uint64 `json:"append_go_allocated_bytes"`
	AppendSurrealPeakRSSBytes   int64  `json:"append_surreal_peak_rss_bytes"`
	AppendSurrealRSSDeltaBytes  int64  `json:"append_surreal_rss_delta_bytes"`
	PublishWallMilliseconds     int64  `json:"publish_wall_ms"`
	PublishGoAllocatedBytes     uint64 `json:"publish_go_allocated_bytes"`
	PublishSurrealPeakRSSBytes  int64  `json:"publish_surreal_peak_rss_bytes"`
	PublishSurrealRSSDeltaBytes int64  `json:"publish_surreal_rss_delta_bytes"`
	FirstPageRows               int    `json:"first_page_rows_including_sentinel"`
	FirstPageWallMilliseconds   int64  `json:"first_page_wall_ms"`
	SweepDeletedRuns            int    `json:"sweep_deleted_runs"`
	SweepSteps                  int    `json:"sweep_steps"`
	SweepAssociationRows        int    `json:"sweep_association_rows"`
	SweepAssertionRows          int    `json:"sweep_assertion_rows"`
	SweepChunkRows              int    `json:"sweep_chunk_rows"`
	SweepAtomRows               int    `json:"sweep_atom_rows"`
	SweepWallMilliseconds       int64  `json:"sweep_wall_ms"`
	SweepSurrealPeakRSSBytes    int64  `json:"sweep_surreal_peak_rss_bytes"`
	SweepSurrealRSSDeltaBytes   int64  `json:"sweep_surreal_rss_delta_bytes"`
	AtomicSupersessionVerified  bool   `json:"atomic_supersession_verified"`
	CompleteSupersededSweep     bool   `json:"complete_superseded_sweep_verified"`
	QueryPlan                   any    `json:"query_plan"`
}

func TestT203ProductionEvidenceCeilings(t *testing.T) {
	if evidenceFormatVersion != "t12-evidence-v1" ||
		maxEvidenceRowsPerRun != 25_000 ||
		maxEvidenceRefsPerAssertion != 4_096 ||
		maxEvidenceReferenceEdges != 20_000 ||
		maxEvidenceFactsPerRun != 12_500 ||
		maxEvidenceFactsPerChunk != 256 ||
		maxEvidenceOccurrences != 100 ||
		maxEvidenceBatchRows != 10_000 ||
		maxEvidenceIdentityBytes != 64<<10 ||
		maxEvidencePathBytes != 4_096 ||
		maxCoverageFileCount != 10_000_000 ||
		maxCoverageReadBytes != 1<<50 ||
		evidenceSweepCandidateBatchSize != 1 ||
		evidenceSweepRowBatchSize != 512 {
		t.Fatalf("T20.3 evidence ceilings changed; review and remeasure")
	}
}

func TestT204ReverseEvidenceSchemaIdentities(t *testing.T) {
	if reverseAssertionIndexName != "assertion_reverse_v6" ||
		defaultReverseAssertionPage != 50 ||
		maxReverseAssertionPage != 100 {
		t.Fatal("T20.4 reverse index identity or page bounds changed; review and remeasure")
	}
}

func TestT205RetentionSchemaIdentities(t *testing.T) {
	if evidenceStoreSchemaVersion != "t12-store-v10" ||
		evidencePreviousStoreSchemaVersion != "t12-store-v9" ||
		evidenceLegacyUpgradableStoreSchemaVersion != "t12-store-v8" ||
		evidencePreUnitUpgradableStoreSchemaVersion != "t12-store-v7" ||
		evidenceMigrationVersion != "t12-evidence-migration-v9" ||
		evidencePreviousMigrationVersion != "t12-evidence-migration-v8" ||
		evidenceWriterGuardEvent != "extraction_run_writer_v10" {
		t.Fatal("evidence scope schema identities changed; review and remeasure")
	}
	// T40.7 retains the two directly preceding compatible writers plus the
	// explicit released pre-unit v7 bridge. All lack chunk accounting and are
	// readable/migratable but never writeable.
	// A retired generation that is still reachable by an upgrade path would be
	// migrated and quarantined at the same time.
	for _, upgradable := range []string{
		evidenceStoreSchemaVersion,
		evidencePreviousStoreSchemaVersion,
		evidenceLegacyUpgradableStoreSchemaVersion,
		evidencePreUnitUpgradableStoreSchemaVersion,
	} {
		if slices.Contains(retiredEvidenceStoreSchemas, upgradable) {
			t.Fatalf("upgradable generation %q is also quarantined", upgradable)
		}
		if !evidenceWriterIsUpgradable(upgradable) {
			t.Fatalf("generation %q is not reachable by any upgrade path", upgradable)
		}
	}
	// Every generation this binary refuses to write must be either quarantined
	// or upgradable — a generation in neither set is silently invisible, which
	// is exactly how an upgrade loses evidence without reporting anything.
	for _, retired := range retiredEvidenceWriterGenerations() {
		if !slices.Contains(retiredEvidenceStoreSchemas, retired) &&
			!evidenceWriterIsUpgradable(retired) {
			t.Fatalf("retired generation %q has no migration branch", retired)
		}
	}
}

// TestT407MaximumShapeEvidenceAccountingMeasurement is deliberately opt-in:
// it stages one exact 12,500-fact/25,000-row evidence run over a one-fact
// published baseline, then completely sweeps that baseline rather than
// becoming a multi-minute package-test tax. The append transaction and all
// limits are the production inputs; the receipt is evidence, not an SLO.
func TestT407MaximumShapeEvidenceAccountingMeasurement(t *testing.T) {
	if os.Getenv("T407_MEASURE_STORE") != "1" {
		t.Skip("set T407_MEASURE_STORE=1 to measure maximum-shape evidence accounting")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	dataDir := t.TempDir()
	s, err := OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	localRuntime, err := ReadLocalRuntime(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	const (
		repo      = "synthetic.invalid/t201-scale"
		commit    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		domain    = "t20-caller-measurement"
		extractor = "t201-measurement-v1"
	)
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "file:///synthetic/t201-scale.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	first, err := beginExtractionRun(s, ctx, repo, commit, domain, extractor)
	if err != nil {
		t.Fatal(err)
	}
	firstAtoms, firstAssociations, firstAssertions := t201EvidenceBatch(first, 0, 1)
	if _, err := addT201Evidence(
		ctx, s, first, firstAtoms, firstAssociations, firstAssertions,
	); err != nil {
		t.Fatalf("stage baseline run: %v", err)
	}
	if _, err := publishT201Run(ctx, s, first, 1); err != nil {
		t.Fatalf("publish first target run: %v", err)
	}

	second, err := beginExtractionRun(s, ctx, repo, commit, domain, extractor)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var appendBefore, appendAfter runtime.MemStats
	runtime.ReadMemStats(&appendBefore)
	appendStarted := time.Now()
	var appendMetrics t407AppendMetrics
	appendPeak, appendRSSBefore, appendRSSAfter, err := measureRSS(localRuntime.PID, func() error {
		appendMetrics = stageT201Run(t, ctx, s, second)
		return nil
	})
	appendWall := time.Since(appendStarted)
	runtime.ReadMemStats(&appendAfter)
	if err != nil {
		t.Fatalf("measure target append: %v", err)
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	publishStart := time.Now()
	publishPeak, publishRSSBefore, publishRSSAfter, err := measureRSS(localRuntime.PID, func() error {
		_, err := publishT201Run(ctx, s, second, t201TargetFacts)
		return err
	})
	publishWall := time.Since(publishStart)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("publish second target run: %v", err)
	}
	latest, err := latestPublishedRun(s, ctx, repo, domain)
	if err != nil || latest.ID != second.ID {
		t.Fatalf("latest run = %+v, %v; want %s", latest, err, second.ID)
	}
	old, err := s.getRun(ctx, first.ID)
	if err != nil || old.Status != "superseded" {
		t.Fatalf("old run = %+v, %v; want superseded", old, err)
	}

	pageStart := time.Now()
	page, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repo, RunID: second.ID, Predicate: "CALLS_OPERATION",
		Object: "/synthetic.orders.v1.Orders/Get",
		Limit:  100, AllowTruncate: true,
	})
	pageWall := time.Since(pageStart)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 101 {
		t.Fatalf("first page rows = %d, want 101 including continuation sentinel", len(page))
	}
	plan, err := t201ReverseQueryPlan(ctx, s, second.ID, repo)
	if err != nil {
		t.Fatalf("capture reverse query plan: %v", err)
	}

	sweepStart := time.Now()
	var sweepProgress EvidenceSweepProgress
	sweepSteps := 0
	sweepPeak, sweepRSSBefore, sweepRSSAfter, err := measureRSS(localRuntime.PID, func() error {
		for sweepProgress.RunsDeleted == 0 {
			step, err := s.SweepEvidence(ctx, time.Now().UTC(), 0)
			if err != nil {
				return err
			}
			if !step.DidWork() {
				return errors.New("target sweep drained before deleting its logical run")
			}
			addEvidenceSweepProgress(&sweepProgress, step)
			sweepSteps++
		}
		return nil
	})
	sweepWall := time.Since(sweepStart)
	if err != nil {
		t.Fatalf("sweep target run: %v", err)
	}
	if sweepProgress.RunsMarkedDeleting != 1 || sweepProgress.RunsDeleted != 1 ||
		sweepProgress.AssociationRowsDeleted != 1 ||
		sweepProgress.AssertionRowsDeleted != 1 ||
		sweepProgress.AtomRowsDeleted != 0 {
		t.Fatalf("target sweep accounting = %+v", sweepProgress)
	}
	if _, err := s.getRun(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("swept run still exists: %v", err)
	}
	current, err := s.ListAssertions(ctx, AssertionQuery{
		Repo: repo, RunID: second.ID, Predicate: "CALLS_OPERATION",
		Object: "/synthetic.orders.v1.Orders/Get", Limit: 100, AllowTruncate: true,
	})
	if err != nil || len(current) != 101 {
		t.Fatalf("current run after sweep = %d rows, %v", len(current), err)
	}

	metrics := t201Metrics{
		Schema:    "t40.7-evidence-accounting-measurement-v1",
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		GOMAXPROCS:                  runtime.GOMAXPROCS(0),
		SurrealVersion:              localRuntime.Surreal.Version,
		SurrealSHA256:               localRuntime.Surreal.SHA256,
		StoreSchema:                 evidenceStoreSchemaVersion,
		EvidenceFormat:              evidenceFormatVersion,
		WriterGuardEvent:            evidenceWriterGuardEvent,
		AdmissionRows:               t201AdmittedRunRows,
		ReferenceEdgeLimit:          t201AdmittedReferenceMax,
		TargetFacts:                 t201TargetFacts,
		TargetRows:                  t201TargetFacts * 2,
		ReferenceEdges:              t201TargetFacts,
		AppendQueryCount:            appendMetrics.QueryCount,
		AppendMaxTransactionBytes:   appendMetrics.MaxTransactionBytes,
		AppendWallMilliseconds:      appendWall.Milliseconds(),
		AppendGoAllocatedBytes:      appendAfter.TotalAlloc - appendBefore.TotalAlloc,
		AppendSurrealPeakRSSBytes:   appendPeak,
		AppendSurrealRSSDeltaBytes:  appendRSSAfter - appendRSSBefore,
		PublishWallMilliseconds:     publishWall.Milliseconds(),
		PublishGoAllocatedBytes:     after.TotalAlloc - before.TotalAlloc,
		PublishSurrealPeakRSSBytes:  publishPeak,
		PublishSurrealRSSDeltaBytes: publishRSSAfter - publishRSSBefore,
		FirstPageRows:               len(page),
		FirstPageWallMilliseconds:   pageWall.Milliseconds(),
		SweepDeletedRuns:            1,
		SweepSteps:                  sweepSteps,
		SweepAssociationRows:        sweepProgress.AssociationRowsDeleted,
		SweepAssertionRows:          sweepProgress.AssertionRowsDeleted,
		SweepChunkRows:              sweepProgress.ChunkRowsDeleted,
		SweepAtomRows:               sweepProgress.AtomRowsDeleted,
		SweepWallMilliseconds:       sweepWall.Milliseconds(),
		SweepSurrealPeakRSSBytes:    sweepPeak,
		SweepSurrealRSSDeltaBytes:   sweepRSSAfter - sweepRSSBefore,
		AtomicSupersessionVerified:  true,
		CompleteSupersededSweep:     true,
		QueryPlan:                   plan,
	}
	encoded, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("current store-writer measurement:\n%s", encoded)
	if target := os.Getenv("T407_RESULTS_PATH"); target != "" {
		if err := os.WriteFile(target, append(encoded, '\n'), 0o600); err != nil {
			t.Fatalf("write T201_RESULTS_PATH: %v", err)
		}
	}
}

type t407AppendMetrics struct {
	QueryCount          int
	MaxTransactionBytes int
}

func stageT201Run(t *testing.T, ctx context.Context, s *Surreal, run *ExtractionRun) t407AppendMetrics {
	t.Helper()
	var metrics t407AppendMetrics
	for offset := 0; offset < t201TargetFacts; offset += t201BatchFacts {
		end := min(offset+t201BatchFacts, t201TargetFacts)
		atoms, associations, assertions := t201EvidenceBatch(run, offset, end)
		transactionBytes, err := addT201Evidence(ctx, s, run, atoms, associations, assertions)
		if err != nil {
			t.Fatalf("stage batch %d: %v", offset/t201BatchFacts, err)
		}
		// AddEvidenceChunk performs one exact-run lookup and one append
		// transaction. Neither count grows with prior staged rows.
		metrics.QueryCount += 2
		metrics.MaxTransactionBytes = max(metrics.MaxTransactionBytes, transactionBytes)
	}
	return metrics
}

func t201EvidenceBatch(run *ExtractionRun, offset, end int) (
	[]EvidenceAtom, []SnapshotEvidence, []Assertion,
) {
	atoms := make([]EvidenceAtom, 0, end-offset)
	associations := make([]SnapshotEvidence, 0, end-offset)
	assertions := make([]Assertion, 0, end-offset)
	for index := offset; index < end; index++ {
		atom := EvidenceAtom{
			SchemaVersion: "t20-measurement-v1",
			BlobDigest:    fmt.Sprintf("sha256:%064x", index),
			StartByte:     index * 8, EndByte: index*8 + 3,
			RuleID: "t201-call-v1", ExtractorVersion: "t201-measurement-v1",
			AdapterConfigDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			FactFingerprint:     fmt.Sprintf("call-%05d", index),
		}
		atom.ID = ComputeAtomID(atom)
		atoms = append(atoms, atom)
		path := fmt.Sprintf("src/unit_%03d/callers.go", index/100)
		associations = append(associations, SnapshotEvidence{
			AtomID: atom.ID, Repo: run.Repo, Commit: run.Commit, Path: path,
			StartLine: 3 + index%100, EndLine: 3 + index%100,
			VisibilityScope: "repo:" + run.Repo,
		})
		assertions = append(assertions, Assertion{
			Predicate: "CALLS_OPERATION",
			Subject:   fmt.Sprintf("%s#L%d", path, 3+index%100),
			Object:    "/synthetic.orders.v1.Orders/Get",
			Lineage:   "synthetic.invalid/t201:idl/proto/orders/v1/orders.proto",
			Tier:      TierDerived, CodeRole: "production", Repo: run.Repo,
			Supporting: []string{atom.ID},
		})
	}
	return atoms, associations, assertions
}

func addT201Evidence(ctx context.Context, s *Surreal, run *ExtractionRun,
	atoms []EvidenceAtom, assocs []SnapshotEvidence, assertions []Assertion,
) (int, error) {
	batch, err := normalizeEvidenceBatch(run, atoms, assocs, assertions, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	digest, err := evidenceBatchDigest(batch)
	if err != nil {
		return 0, err
	}
	chunkID := "sha256:" + hashIdentity("", run.ID, digest)
	vars := map[string]any{
		"run": extractionRunID(run.ID), "run_id": run.ID, "atoms": batch.atoms,
		"assocs": batch.assocs, "asserts": batch.asserts,
		"chunk_rid": evidenceChunkRecordID(run.ID, chunkID), "chunk_id": chunkID,
		"content_digest": digest, "fact_count": len(assertions), "now": time.Now().UTC(),
		"max_run_rows": t201AdmittedRunRows, "max_run_facts": maxEvidenceFactsPerRun,
		"max_reference_edges":        t201AdmittedReferenceMax,
		"migration_rid":              evidenceMigrationStateID(),
		"store_schema_version":       evidenceStoreSchemaVersion,
		"evidence_format_version":    evidenceFormatVersion,
		"evidence_migration_version": evidenceMigrationVersion,
	}
	addProbeVars(vars, run.ID)
	encodedVars, err := json.Marshal(vars)
	if err != nil {
		return 0, err
	}
	if err := s.AddEvidenceChunk(
		ctx, run.ID, chunkID, len(assertions), atoms, assocs, assertions,
	); err != nil {
		return 0, err
	}
	return len(addEvidenceSQL) + len(encodedVars), nil
}

func publishT201Run(
	ctx context.Context, s *Surreal, run *ExtractionRun, facts int,
) (*ExtractionRun, error) {
	coverage := CoverageManifest{
		Protocols:       []string{"t20-neutral-scale-v1"},
		CorpusFileCount: 103, CandidateFileCount: 101, ReadFileCount: 103,
		ReadBytes: 1, SourceScopeDigest: "sha256:" + strings.Repeat("0", 64),
		AssertionCount: facts, AtomCount: facts,
	}
	if err := s.PublishExtractionRun(ctx, run.ID, coverage); err != nil {
		return nil, err
	}
	published, err := s.getRun(ctx, run.ID)
	return published, err
}

func t201ReverseQueryPlan(ctx context.Context, s *Surreal, runID, repo string) (any, error) {
	results, err := surrealdb.Query[any](ctx, s.db,
		`SELECT * FROM assertion
			WHERE run_id = $run_id
			  AND predicate = $predicate
			  AND object = $object
			  AND repo = $repo
			ORDER BY predicate, subject, object, assertion_id, run_id
			LIMIT 101 EXPLAIN FULL`,
		map[string]any{
			"run_id": runID, "predicate": "CALLS_OPERATION",
			"object": "/synthetic.orders.v1.Orders/Get", "repo": repo,
		})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func measureRSS(pid int, measured func() error) (peak, before, after int64, err error) {
	before, err = processRSS(pid)
	if err != nil {
		return 0, 0, 0, err
	}
	peak = before
	done := make(chan struct{})
	var mu sync.Mutex
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				current, sampleErr := processRSS(pid)
				if sampleErr == nil {
					mu.Lock()
					if current > peak {
						peak = current
					}
					mu.Unlock()
				}
			}
		}
	}()
	err = measured()
	close(done)
	sampler.Wait()
	after, rssErr := processRSS(pid)
	if err == nil {
		err = rssErr
	}
	mu.Lock()
	if after > peak {
		peak = after
	}
	mu.Unlock()
	return peak, before, after, err
}

func processRSS(pid int) (int64, error) {
	output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	kib, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, err
	}
	return kib * 1024, nil
}
