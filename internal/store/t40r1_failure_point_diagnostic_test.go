package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	t40r1KafkaActualFacts         = 131_072
	t40r1KafkaActualRows          = 262_144
	t40r1KafkaActualReferences    = 131_072
	t40r1KafkaEmittingPartitions  = 32
	t40r1KafkaFactsPerPartition   = 4_096
	t40r1KafkaWriterWorkers       = 2
	t40r1ClientRequestBoundary    = 30 * time.Second
	t40r1KafkaWriterResultSchema  = "phebs-t40r1-kafka-writer-failure-point-v1"
	t40r1KafkaWriterDiagnosticEnv = "T40R1_KAFKA_WRITER_DIAGNOSTIC"
)

type t40r1KafkaWriterFailure struct {
	Phase             string `json:"phase"`
	Class             string `json:"class"`
	RequestWallMillis int64  `json:"request_wall_ms"`
}

type t40r1KafkaWriterPhase struct {
	Requests         int   `json:"requests"`
	Completed        int   `json:"completed"`
	DeadlineFailures int   `json:"deadline_failures"`
	CanceledFailures int   `json:"canceled_failures"`
	OtherFailures    int   `json:"other_failures"`
	WallLT1Second    int   `json:"wall_lt_1s"`
	WallLT10Seconds  int   `json:"wall_lt_10s"`
	WallLT30Seconds  int   `json:"wall_lt_30s"`
	WallGE30Seconds  int   `json:"wall_ge_30s"`
	MaxWallMillis    int64 `json:"max_wall_ms"`
}

type t40r1KafkaWriterDiagnostic struct {
	Schema                       string                   `json:"schema"`
	GoVersion                    string                   `json:"go_version"`
	GOOS                         string                   `json:"goos"`
	GOARCH                       string                   `json:"goarch"`
	GOMAXPROCS                   int                      `json:"gomaxprocs"`
	SurrealVersion               string                   `json:"surreal_version"`
	SurrealSHA256                string                   `json:"surreal_sha256"`
	StoreSchema                  string                   `json:"store_schema"`
	EvidenceFormat               string                   `json:"evidence_format"`
	PlanSchema                   string                   `json:"plan_schema"`
	Domain                       string                   `json:"domain"`
	Workers                      int                      `json:"workers"`
	ClientRequestBoundaryMillis  int64                    `json:"client_request_boundary_ms"`
	ChunkFacts                   int                      `json:"chunk_facts"`
	TargetPartitions             int                      `json:"target_partitions"`
	FactsPerPartition            int                      `json:"facts_per_partition"`
	TargetChunks                 int                      `json:"target_chunks"`
	TargetFacts                  int                      `json:"target_facts"`
	TargetRows                   int                      `json:"target_rows"`
	TargetReferences             int                      `json:"target_references"`
	ReservedFacts                int64                    `json:"reserved_facts"`
	ReservedRows                 int64                    `json:"reserved_rows"`
	ReservedReferences           int64                    `json:"reserved_references"`
	AttemptedChunks              int                      `json:"attempted_chunks"`
	CompletedChunks              int                      `json:"completed_chunks"`
	CompletedFacts               int64                    `json:"completed_facts"`
	CompletedRows                int64                    `json:"completed_rows"`
	CompletedReferences          int64                    `json:"completed_references"`
	Append                       t40r1KafkaWriterPhase    `json:"append"`
	Accounting                   t40r1KafkaWriterPhase    `json:"accounting"`
	GoAllocatedBytes             uint64                   `json:"go_allocated_bytes"`
	SurrealPeakRSSBytes          int64                    `json:"surreal_peak_rss_bytes"`
	SurrealRSSDeltaBytes         int64                    `json:"surreal_rss_delta_bytes"`
	WallMillis                   int64                    `json:"wall_ms"`
	StoppedAtFirstRequestFailure bool                     `json:"stopped_at_first_request_failure"`
	Complete                     bool                     `json:"complete"`
	Failure                      *t40r1KafkaWriterFailure `json:"failure,omitempty"`
}

// TestT40R1KafkaWriterFailurePointDiagnostic isolates the post-envelope
// writer failure from repository authoring, indexing, observation, unrelated
// extraction domains, relationship publication, backup, and product replay.
// It stages the measured Kafka v3 actual shape through the production
// AddEvidenceChunk/GetEvidenceChunkAccounting pair with the production
// two-worker contention and stops as soon as either request fails.
//
// The test is deliberately opt-in because a complete pass writes 512 exact
// 256-fact chunks to a real supervised SurrealDB child. Ordinary CI skips it.
func TestT40R1KafkaWriterFailurePointDiagnostic(t *testing.T) {
	if os.Getenv(t40r1KafkaWriterDiagnosticEnv) != "1" {
		t.Skip("set T40R1_KAFKA_WRITER_DIAGNOSTIC=1 to exercise the exact Kafka writer boundary")
	}
	resultsPath := os.Getenv("T40R1_KAFKA_WRITER_RESULTS_PATH")
	if resultsPath != "" && !filepath.IsAbs(resultsPath) {
		t.Fatal("T40R1_KAFKA_WRITER_RESULTS_PATH must be absolute")
	}
	if t40r1KafkaActualFacts%maxEvidenceFactsPerChunk != 0 ||
		t40r1KafkaActualRows != t40r1KafkaActualFacts*2 ||
		t40r1KafkaActualReferences != t40r1KafkaActualFacts ||
		t40r1KafkaEmittingPartitions*t40r1KafkaFactsPerPartition != t40r1KafkaActualFacts ||
		t40r1KafkaFactsPerPartition%maxEvidenceFactsPerChunk != 0 {
		t.Fatal("T40.R1 Kafka actual shape no longer matches the retained measurement")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}

	dataDir := t.TempDir()
	s, err := OpenLocal(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	localRuntime, err := ReadLocalRuntime(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	run, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: "synthetic.invalid/t40r1-writer", Commit: strings.Repeat("a", 40),
		Domain: PartitionedV3Domain,
	}, "t40r1-kafka-writer-diagnostic-v1",
		"sha256:"+strings.Repeat("1", 64), "sha256:"+strings.Repeat("2", 64),
		PartitionedPlanSchemaV3, PartitionedExtractionRunLimits{
			Facts: MaxPartitionedFactsV3, Rows: MaxPartitionedRowsV3,
			References: MaxPartitionedReferencesV3,
		})
	if err != nil {
		t.Fatal(err)
	}

	report := t40r1KafkaWriterDiagnostic{
		Schema:    t40r1KafkaWriterResultSchema,
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		GOMAXPROCS: runtime.GOMAXPROCS(0), SurrealVersion: localRuntime.Surreal.Version,
		SurrealSHA256: localRuntime.Surreal.SHA256, StoreSchema: evidenceStoreSchemaVersion,
		EvidenceFormat: evidenceFormatVersion,
		PlanSchema:     PartitionedPlanSchemaV3, Domain: PartitionedV3Domain,
		Workers:                     t40r1KafkaWriterWorkers,
		ClientRequestBoundaryMillis: t40r1ClientRequestBoundary.Milliseconds(),
		ChunkFacts:                  maxEvidenceFactsPerChunk,
		TargetPartitions:            t40r1KafkaEmittingPartitions,
		FactsPerPartition:           t40r1KafkaFactsPerPartition,
		TargetChunks:                t40r1KafkaActualFacts / maxEvidenceFactsPerChunk,
		TargetFacts:                 t40r1KafkaActualFacts, TargetRows: t40r1KafkaActualRows,
		TargetReferences: t40r1KafkaActualReferences,
		ReservedFacts:    MaxPartitionedFactsV3, ReservedRows: MaxPartitionedRowsV3,
		ReservedReferences:           MaxPartitionedReferencesV3,
		StoppedAtFirstRequestFailure: true,
	}
	jobs := make(chan int, report.TargetPartitions)
	for ordinal := range report.TargetPartitions {
		jobs <- ordinal
	}
	close(jobs)

	started := time.Now()
	var mutex sync.Mutex
	var firstFailure sync.Once
	recordFailure := func(phase string, requestWall time.Duration, requestErr error) {
		firstFailure.Do(func() {
			mutex.Lock()
			report.Failure = &t40r1KafkaWriterFailure{
				Phase: phase, Class: t40r1WriterFailureClass(requestErr),
				RequestWallMillis: requestWall.Milliseconds(),
			}
			mutex.Unlock()
			cancel()
		})
	}
	worker := func() {
		for {
			select {
			case <-ctx.Done():
				return
			case partitionOrdinal, ok := <-jobs:
				if !ok {
					return
				}
				partitionStart := partitionOrdinal * t40r1KafkaFactsPerPartition
				chunksPerPartition := t40r1KafkaFactsPerPartition / maxEvidenceFactsPerChunk
				for partitionChunk := range chunksPerPartition {
					if ctx.Err() != nil {
						return
					}
					chunkOrdinal := partitionOrdinal*chunksPerPartition + partitionChunk
					offset := partitionStart + partitionChunk*maxEvidenceFactsPerChunk
					end := offset + maxEvidenceFactsPerChunk
					atoms, associations, assertions := t201EvidenceBatch(run, offset, end)
					chunkID := fmt.Sprintf("sha256:%064x", chunkOrdinal+1)

					mutex.Lock()
					report.AttemptedChunks++
					mutex.Unlock()
					requestStarted := time.Now()
					err := s.AddEvidenceChunk(
						ctx, run.ID, chunkID, maxEvidenceFactsPerChunk,
						atoms, associations, assertions,
					)
					requestWall := time.Since(requestStarted)
					mutex.Lock()
					t40r1RecordWriterPhase(&report.Append, requestWall, err)
					mutex.Unlock()
					if err != nil {
						recordFailure("append", requestWall, err)
						return
					}

					requestStarted = time.Now()
					receipt, err := s.GetEvidenceChunkAccounting(ctx, run.ID, chunkID)
					requestWall = time.Since(requestStarted)
					if err == nil && (receipt.FactCount != maxEvidenceFactsPerChunk ||
						receipt.RowDelta != maxEvidenceFactsPerChunk*2 ||
						receipt.ReferenceDelta != maxEvidenceFactsPerChunk) {
						err = errors.New("unexpected bounded accounting")
					}
					mutex.Lock()
					t40r1RecordWriterPhase(&report.Accounting, requestWall, err)
					mutex.Unlock()
					if err != nil {
						recordFailure("accounting", requestWall, err)
						return
					}
					mutex.Lock()
					report.CompletedChunks++
					report.CompletedFacts += receipt.FactCount
					report.CompletedRows += receipt.RowDelta
					report.CompletedReferences += receipt.ReferenceDelta
					mutex.Unlock()
				}
			}
		}
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	peak, rssBefore, rssAfter, err := measureRSS(localRuntime.PID, func() error {
		var workers sync.WaitGroup
		workers.Add(t40r1KafkaWriterWorkers)
		for range t40r1KafkaWriterWorkers {
			go func() {
				defer workers.Done()
				worker()
			}()
		}
		workers.Wait()
		return nil
	})
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("measure Kafka writer: %v", err)
	}
	report.WallMillis = time.Since(started).Milliseconds()
	report.GoAllocatedBytes = after.TotalAlloc - before.TotalAlloc
	report.SurrealPeakRSSBytes = peak
	report.SurrealRSSDeltaBytes = rssAfter - rssBefore
	report.Complete = report.Failure == nil &&
		report.CompletedChunks == report.TargetChunks &&
		report.CompletedFacts == int64(report.TargetFacts) &&
		report.CompletedRows == int64(report.TargetRows) &&
		report.CompletedReferences == int64(report.TargetReferences)
	if err := report.validate(); err != nil {
		t.Fatalf("validate Kafka writer diagnostic: %v", err)
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("T40.R1 Kafka writer failure-point diagnostic:\n%s", encoded)
	if resultsPath != "" {
		if err := os.WriteFile(resultsPath, append(encoded, '\n'), 0o600); err != nil {
			t.Fatalf("write Kafka writer diagnostic: %v", err)
		}
	}
	if report.Failure != nil {
		t.Fatalf("Kafka writer stopped in %s after %d completed chunks: %s",
			report.Failure.Phase, report.CompletedChunks, report.Failure.Class)
	}
	if !report.Complete {
		t.Fatal("Kafka writer diagnostic ended without an exact completion or classified request failure")
	}
}

func (report t40r1KafkaWriterDiagnostic) validate() error {
	appendFailures := report.Append.DeadlineFailures + report.Append.CanceledFailures +
		report.Append.OtherFailures
	accountingFailures := report.Accounting.DeadlineFailures + report.Accounting.CanceledFailures +
		report.Accounting.OtherFailures
	if report.Schema != t40r1KafkaWriterResultSchema ||
		report.Domain != PartitionedV3Domain || report.PlanSchema != PartitionedPlanSchemaV3 ||
		report.Workers != t40r1KafkaWriterWorkers ||
		report.ClientRequestBoundaryMillis != t40r1ClientRequestBoundary.Milliseconds() ||
		report.ChunkFacts != maxEvidenceFactsPerChunk ||
		report.TargetPartitions != t40r1KafkaEmittingPartitions ||
		report.FactsPerPartition != t40r1KafkaFactsPerPartition ||
		report.TargetChunks != t40r1KafkaActualFacts/maxEvidenceFactsPerChunk ||
		report.ReservedFacts != MaxPartitionedFactsV3 ||
		report.ReservedRows != MaxPartitionedRowsV3 ||
		report.ReservedReferences != MaxPartitionedReferencesV3 ||
		!report.StoppedAtFirstRequestFailure ||
		report.Append.Requests != report.AttemptedChunks ||
		report.Append.Completed+appendFailures != report.Append.Requests ||
		t40r1WriterPhaseBuckets(report.Append) != report.Append.Requests ||
		report.Accounting.Requests != report.Append.Completed ||
		report.Accounting.Completed+accountingFailures != report.Accounting.Requests ||
		t40r1WriterPhaseBuckets(report.Accounting) != report.Accounting.Requests ||
		report.Accounting.Completed != report.CompletedChunks ||
		(report.Complete != (report.Failure == nil && report.CompletedChunks == report.TargetChunks)) {
		return errors.New("writer report does not reconcile")
	}
	if report.Failure != nil &&
		((report.Failure.Phase != "append" && report.Failure.Phase != "accounting") ||
			(report.Failure.Class != "deadline" && report.Failure.Class != "canceled" &&
				report.Failure.Class != "other") || report.Failure.RequestWallMillis < 0) {
		return errors.New("writer report has an invalid closed failure")
	}
	return nil
}

func t40r1WriterPhaseBuckets(summary t40r1KafkaWriterPhase) int {
	return summary.WallLT1Second + summary.WallLT10Seconds +
		summary.WallLT30Seconds + summary.WallGE30Seconds
}

func t40r1WriterFailureClass(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "other"
	}
}

func t40r1RecordWriterPhase(summary *t40r1KafkaWriterPhase, wall time.Duration, err error) {
	if summary == nil {
		return
	}
	summary.Requests++
	summary.MaxWallMillis = max(summary.MaxWallMillis, wall.Milliseconds())
	switch {
	case wall < time.Second:
		summary.WallLT1Second++
	case wall < 10*time.Second:
		summary.WallLT10Seconds++
	case wall < t40r1ClientRequestBoundary:
		summary.WallLT30Seconds++
	default:
		summary.WallGE30Seconds++
	}
	if err == nil {
		summary.Completed++
		return
	}
	switch t40r1WriterFailureClass(err) {
	case "deadline":
		summary.DeadlineFailures++
	case "canceled":
		summary.CanceledFailures++
	default:
		summary.OtherFailures++
	}
}

func TestT40R1WriterPhaseAttributionIsClosed(t *testing.T) {
	var summary t40r1KafkaWriterPhase
	t40r1RecordWriterPhase(&summary, 500*time.Millisecond, nil)
	t40r1RecordWriterPhase(&summary, 5*time.Second, context.DeadlineExceeded)
	t40r1RecordWriterPhase(&summary, 20*time.Second, context.Canceled)
	t40r1RecordWriterPhase(&summary, t40r1ClientRequestBoundary, errors.New("closed other"))
	if summary.Requests != 4 || summary.Completed != 1 || summary.DeadlineFailures != 1 ||
		summary.CanceledFailures != 1 || summary.OtherFailures != 1 ||
		summary.WallLT1Second != 1 || summary.WallLT10Seconds != 1 ||
		summary.WallLT30Seconds != 1 || summary.WallGE30Seconds != 1 ||
		summary.MaxWallMillis != t40r1ClientRequestBoundary.Milliseconds() {
		t.Fatalf("closed writer phase attribution = %+v", summary)
	}
}
