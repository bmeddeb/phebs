package generationscheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t40r1ScheduleDiagnosticEnvironment = "T40R1_GENERATION_SCHEDULE_DIAGNOSTIC"
	t40r1ScheduleDiagnosticSchema      = "phebs-t40r1-generation-schedule-recovery-v1"
	t40r1ScheduleDiagnosticChunks      = 272
)

type t40r1ScheduleRetryCounts struct {
	Expand    int `json:"expand"`
	Complete  int `json:"complete"`
	Heartbeat int `json:"heartbeat"`
	Reap      int `json:"reap"`
	Other     int `json:"other"`
}

type t40r1ScheduleDiagnostic struct {
	Schema               string                   `json:"schema"`
	GoVersion            string                   `json:"go_version"`
	SurrealVersion       string                   `json:"surreal_version"`
	PhysicalStore        string                   `json:"physical_store"`
	Chunks               int                      `json:"chunks"`
	ChunkItems           int                      `json:"chunk_items"`
	RepositoryTokens     int                      `json:"repository_tokens"`
	Workers              int                      `json:"workers"`
	HandlerExecutions    int                      `json:"handler_executions"`
	Materialized         int                      `json:"materialized"`
	Pending              int                      `json:"pending"`
	Running              int                      `json:"running"`
	Succeeded            int                      `json:"succeeded"`
	Failed               int                      `json:"failed"`
	StoreErrors          int                      `json:"store_errors"`
	ConflictRetries      t40r1ScheduleRetryCounts `json:"conflict_retries"`
	WallMillis           int64                    `json:"wall_ms"`
	Settled              bool                     `json:"settled"`
	ExactCounters        bool                     `json:"exact_counters"`
	RepositoryTokenClear bool                     `json:"repository_token_clear"`
}

// TestT40R1GenerationScheduleRecoveryDiagnostic isolates the neutral-21
// scheduler boundary from Git, indexing, observation building, extractors,
// evidence writers, relationship publication, and product replay. It uses the
// exact 272 one-item schedule shape, the production repository-token value,
// two workers, and a real supervised surrealkv child. Ordinary CI skips it.
func TestT40R1GenerationScheduleRecoveryDiagnostic(t *testing.T) {
	if os.Getenv(t40r1ScheduleDiagnosticEnvironment) != "1" {
		t.Skip("set T40R1_GENERATION_SCHEDULE_DIAGNOSTIC=1 to exercise schedule recovery")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	resultsPath := os.Getenv("T40R1_GENERATION_SCHEDULE_RESULTS_PATH")
	if resultsPath != "" && !filepath.IsAbs(resultsPath) {
		t.Fatal("T40R1_GENERATION_SCHEDULE_RESULTS_PATH must be absolute")
	}

	dataDir := t.TempDir()
	state, err := store.OpenLocal(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	runtimeState, err := store.ReadLocalRuntime(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	var captured bytes.Buffer
	priorLogOutput := log.Writer()
	log.SetOutput(&captured)
	t.Cleanup(func() { log.SetOutput(priorLogOutput) })

	spec := store.GenerationScheduleSpec{
		Repository: "example.invalid/t40r1-schedule-recovery",
		Stage:      "extraction-partitions", Generation: "sha256:" + strings.Repeat("4", 64),
		ResourceClass: store.GenerationResourceExtraction,
		TotalItems:    t40r1ScheduleDiagnosticChunks, ChunkItems: 1,
		MaxAttempts: 5, RepositoryTokens: 1,
	}
	if _, err := state.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var handlerExecutions atomic.Int64
	var errorMu sync.Mutex
	var storeErrors []error
	scheduler := &Scheduler{
		Store: state,
		Classes: map[store.GenerationResourceClass]Class{
			store.GenerationResourceExtraction: {
				Concurrency: 2,
				Budget:      Budget{MaxMemoryBytes: 1 << 20, MaxDescriptors: 4},
				Handle: func(context.Context, store.GenerationChunk, Budget) error {
					handlerExecutions.Add(1)
					return nil
				},
			},
		},
		MaxConcurrency: 2, MaxMemoryBytes: 2 << 20, MaxDescriptors: 8,
		PollEvery: time.Millisecond, HeartbeatEvery: 50 * time.Millisecond,
		StaleAfter: 200 * time.Millisecond, StoreCallTimeout: 5 * time.Second,
		WorkerPrefix: "t40r1-schedule-diagnostic",
		Report: func(err error) {
			errorMu.Lock()
			defer errorMu.Unlock()
			storeErrors = append(storeErrors, err)
		},
	}
	runDone := make(chan error, 1)
	started := time.Now()
	go func() { runDone <- scheduler.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Minute)
	var final *store.GenerationSchedule
	for time.Now().Before(deadline) {
		current, currentErr := state.GetGenerationSchedule(
			t.Context(), spec.Repository, spec.Stage,
		)
		if currentErr == nil {
			final = current
			if current.Status == store.GenerationScheduleSettled {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if final == nil || final.Status != store.GenerationScheduleSettled {
		t.Fatalf("schedule did not settle before diagnostic deadline: %+v", final)
	}

	errorMu.Lock()
	reportedErrors := append([]error(nil), storeErrors...)
	errorMu.Unlock()
	retries := t40r1ScheduleRetryCounts{
		Expand:    strings.Count(captured.String(), "operation=expand "),
		Complete:  strings.Count(captured.String(), "operation=complete "),
		Heartbeat: strings.Count(captured.String(), "operation=heartbeat "),
		Reap:      strings.Count(captured.String(), "operation=reap "),
	}
	retryTotal := strings.Count(captured.String(), "generation schedule store retry:")
	retries.Other = retryTotal - retries.Expand - retries.Complete - retries.Heartbeat - retries.Reap
	report := t40r1ScheduleDiagnostic{
		Schema: t40r1ScheduleDiagnosticSchema, GoVersion: runtime.Version(),
		SurrealVersion: runtimeState.Surreal.Version, PhysicalStore: "surrealkv",
		Chunks: t40r1ScheduleDiagnosticChunks, ChunkItems: spec.ChunkItems,
		RepositoryTokens: spec.RepositoryTokens, Workers: 2,
		HandlerExecutions: int(handlerExecutions.Load()), Materialized: final.Materialized,
		Pending: final.Pending, Running: final.Running, Succeeded: final.Succeeded,
		Failed: final.Failed, StoreErrors: len(reportedErrors), ConflictRetries: retries,
		WallMillis: time.Since(started).Milliseconds(), Settled: true,
	}
	report.ExactCounters = final.Materialized == t40r1ScheduleDiagnosticChunks &&
		final.Pending == 0 && final.Running == 0 &&
		final.Succeeded == t40r1ScheduleDiagnosticChunks && final.Failed == 0 &&
		report.HandlerExecutions == t40r1ScheduleDiagnosticChunks
	report.RepositoryTokenClear = final.Running == 0
	if !report.ExactCounters || !report.RepositoryTokenClear || len(reportedErrors) != 0 {
		t.Fatalf("schedule recovery report = %+v, errors = %v", report, reportedErrors)
	}
	if resultsPath != "" {
		encoded, encodeErr := json.MarshalIndent(report, "", "  ")
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		encoded = append(encoded, '\n')
		if writeErr := os.WriteFile(resultsPath, encoded, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
}
