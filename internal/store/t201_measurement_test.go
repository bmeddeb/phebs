package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/spike/t201"
)

const (
	t201TargetFacts          = t201.ScaleTotalCalls
	t201AdmittedRunRows      = maxEvidenceRowsPerRun
	t201AdmittedReferenceMax = maxEvidenceReferenceEdges
	t201BatchFacts           = 500
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
	PublishWallMilliseconds     int64  `json:"publish_wall_ms"`
	PublishGoAllocatedBytes     uint64 `json:"publish_go_allocated_bytes"`
	PublishSurrealPeakRSSBytes  int64  `json:"publish_surreal_peak_rss_bytes"`
	PublishSurrealRSSDeltaBytes int64  `json:"publish_surreal_rss_delta_bytes"`
	FirstPageRows               int    `json:"first_page_rows_including_sentinel"`
	FirstPageWallMilliseconds   int64  `json:"first_page_wall_ms"`
	SweepDeletedRuns            int    `json:"sweep_deleted_runs"`
	SweepWallMilliseconds       int64  `json:"sweep_wall_ms"`
	SweepSurrealPeakRSSBytes    int64  `json:"sweep_surreal_peak_rss_bytes"`
	SweepSurrealRSSDeltaBytes   int64  `json:"sweep_surreal_rss_delta_bytes"`
	AtomicSupersessionVerified  bool   `json:"atomic_supersession_verified"`
	CompleteTargetSweepVerified bool   `json:"complete_target_sweep_verified"`
	QueryPlan                   any    `json:"query_plan"`
}

func TestT203ProductionEvidenceCeilings(t *testing.T) {
	if evidenceFormatVersion != "t12-evidence-v1" ||
		maxEvidenceRowsPerRun != 25_000 ||
		maxEvidenceRefsPerAssertion != 4_096 ||
		maxEvidenceReferenceEdges != 20_000 ||
		maxEvidenceOccurrences != 100 ||
		maxEvidenceBatchRows != 10_000 ||
		maxEvidenceIdentityBytes != 64<<10 ||
		maxEvidencePathBytes != 4_096 ||
		maxCoverageFileCount != 10_000_000 ||
		maxCoverageReadBytes != 1<<50 ||
		evidenceSweepBatchSize != 1 {
		t.Fatalf("T20.3 evidence ceilings changed; review and remeasure")
	}
}

func TestT204ReverseEvidenceSchemaIdentities(t *testing.T) {
	if evidenceStoreSchemaVersion != "t12-store-v6" ||
		evidencePreviousStoreSchemaVersion != "t12-store-v5" ||
		evidenceMigrationVersion != "t12-evidence-migration-v4" ||
		evidencePreviousMigrationVersion != "t12-evidence-migration-v3" ||
		evidenceWriterGuardEvent != "extraction_run_writer_v6" ||
		reverseAssertionIndexName != "assertion_reverse_v6" ||
		defaultReverseAssertionPage != 50 ||
		maxReverseAssertionPage != 100 {
		t.Fatal("T20.4 reverse schema identities or page bounds changed; review and remeasure")
	}
}

// TestT201TargetPublicationAndSweepMeasurement is deliberately opt-in: it
// stages two 20,020-row evidence runs under the current 25,000-row production
// admission and completely sweeps the superseded one, rather than becoming a
// multi-minute tax on every package test. The statements and limit variables
// under measurement are the exact production addEvidenceSQL,
// publishExtractionRunSQL, and sweepRunSQL inputs; no SQL copy, relaxed
// admission seam, or alternate algorithm exists here.
func TestT201TargetPublicationAndSweepMeasurement(t *testing.T) {
	if os.Getenv("T201_MEASURE_STORE") != "1" {
		t.Skip("set T201_MEASURE_STORE=1 to measure the current store writer")
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

	first, err := s.BeginExtractionRun(ctx, repo, commit, domain, extractor)
	if err != nil {
		t.Fatal(err)
	}
	stageT201Run(t, ctx, s, first)
	if _, err := publishT201Run(ctx, s, first); err != nil {
		t.Fatalf("publish first target run: %v", err)
	}

	second, err := s.BeginExtractionRun(ctx, repo, commit, domain, extractor)
	if err != nil {
		t.Fatal(err)
	}
	stageT201Run(t, ctx, s, second)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	publishStart := time.Now()
	publishPeak, publishRSSBefore, publishRSSAfter, err := measureRSS(localRuntime.PID, func() error {
		_, err := publishT201Run(ctx, s, second)
		return err
	})
	publishWall := time.Since(publishStart)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("publish second target run: %v", err)
	}
	latest, err := s.LatestPublishedRun(ctx, repo, domain)
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
	sweepPeak, sweepRSSBefore, sweepRSSAfter, err := measureRSS(localRuntime.PID, func() error {
		deleted, err := s.SweepEvidence(ctx, time.Now().UTC(), 0)
		if err != nil {
			return err
		}
		if deleted != 1 {
			return fmt.Errorf("deleted runs = %d, want 1", deleted)
		}
		return nil
	})
	sweepWall := time.Since(sweepStart)
	if err != nil {
		t.Fatalf("sweep target run: %v", err)
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
		Schema:    "t20-store-measurement-v2",
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
		PublishWallMilliseconds:     publishWall.Milliseconds(),
		PublishGoAllocatedBytes:     after.TotalAlloc - before.TotalAlloc,
		PublishSurrealPeakRSSBytes:  publishPeak,
		PublishSurrealRSSDeltaBytes: publishRSSAfter - publishRSSBefore,
		FirstPageRows:               len(page),
		FirstPageWallMilliseconds:   pageWall.Milliseconds(),
		SweepDeletedRuns:            1,
		SweepWallMilliseconds:       sweepWall.Milliseconds(),
		SweepSurrealPeakRSSBytes:    sweepPeak,
		SweepSurrealRSSDeltaBytes:   sweepRSSAfter - sweepRSSBefore,
		AtomicSupersessionVerified:  true,
		CompleteTargetSweepVerified: true,
		QueryPlan:                   plan,
	}
	encoded, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("current store-writer measurement:\n%s", encoded)
	if target := os.Getenv("T201_RESULTS_PATH"); target != "" {
		if err := os.WriteFile(target, append(encoded, '\n'), 0o600); err != nil {
			t.Fatalf("write T201_RESULTS_PATH: %v", err)
		}
	}
}

func stageT201Run(t *testing.T, ctx context.Context, s *Surreal, run *ExtractionRun) {
	t.Helper()
	for offset := 0; offset < t201TargetFacts; offset += t201BatchFacts {
		end := min(offset+t201BatchFacts, t201TargetFacts)
		atoms, associations, assertions := t201EvidenceBatch(run, offset, end)
		if err := addT201Evidence(ctx, s, run, atoms, associations, assertions); err != nil {
			t.Fatalf("stage batch %d: %v", offset/t201BatchFacts, err)
		}
	}
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
) error {
	batch, err := normalizeEvidenceBatch(run, atoms, assocs, assertions, time.Now().UTC())
	if err != nil {
		return err
	}
	vars := map[string]any{
		"run": extractionRunID(run.ID), "run_id": run.ID, "atoms": batch.atoms,
		"assocs": batch.assocs, "asserts": batch.asserts,
		"max_run_rows": t201AdmittedRunRows, "max_reference_edges": t201AdmittedReferenceMax,
		"migration_rid":              evidenceMigrationStateID(),
		"store_schema_version":       evidenceStoreSchemaVersion,
		"evidence_format_version":    evidenceFormatVersion,
		"evidence_migration_version": evidenceMigrationVersion,
	}
	addProbeVars(vars, run.ID)
	results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db, addEvidenceSQL, vars)
	if err != nil {
		return err
	}
	if len(firstExtractionRows(results)) != 1 {
		return errors.New("exact addEvidenceSQL did not retain the staged run")
	}
	return nil
}

func publishT201Run(ctx context.Context, s *Surreal, run *ExtractionRun) (*ExtractionRun, error) {
	coverage := CoverageManifest{
		Protocols:       []string{"t20-neutral-scale-v1"},
		CorpusFileCount: 103, CandidateFileCount: 101, ReadFileCount: 103,
		ReadBytes: 1, SourceScopeDigest: "sha256:" + strings.Repeat("0", 64),
		AssertionCount: t201TargetFacts, AtomCount: t201TargetFacts,
	}
	vars := map[string]any{
		"rid": extractionRunID(run.ID), "run_id": run.ID,
		"attempt_rid": extractionAttemptID(run.Repo, run.Domain),
		"repo_rid":    repoID(run.Repo), "repo": run.Repo, "commit": run.Commit,
		"domain": run.Domain, "published_key": publishedKey(run.Repo, run.Domain),
		"now": time.Now().UTC(), "coverage": coverage,
		"want_assertions": coverage.AssertionCount, "want_atoms": coverage.AtomCount,
		"want_unresolved":             coverage.UnresolvedCount,
		"max_occurrences_per_atom":    maxEvidenceOccurrences,
		"max_run_rows":                t201AdmittedRunRows,
		"max_reference_edges":         t201AdmittedReferenceMax,
		"migration_rid":               evidenceMigrationStateID(),
		"store_schema_version":        evidenceStoreSchemaVersion,
		"evidence_format_version":     evidenceFormatVersion,
		"evidence_migration_version":  evidenceMigrationVersion,
		"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
	}
	addProbeVars(vars, run.ID)
	results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db, publishExtractionRunSQL, vars)
	if err != nil {
		return nil, err
	}
	rows := firstExtractionRows(results)
	if len(rows) != 1 {
		return nil, errors.New("exact publishExtractionRunSQL did not publish one run")
	}
	published := rows[0].run()
	return &published, nil
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
