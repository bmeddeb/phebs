package t4013

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLogAccountingUsesOnlyClosedDiagnosticReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	content := []byte(`2026/08/08 job lifecycle: {"schema":"phebs-job-lifecycle-v1","event":"claimed","job_id":"x","kind":"candidate_manifest_job","target":"private","attempt":1,"queue_wait_ms":0,"handle_ms":0,"outcome":"claimed"}
2026/08/08 job lifecycle: {"schema":"phebs-job-lifecycle-v1","event":"started","job_id":"x","kind":"candidate_manifest_job","target":"private","attempt":1,"queue_wait_ms":0,"handle_ms":0,"outcome":"running"}
2026/08/08 job lifecycle: {"schema":"phebs-job-lifecycle-v1","event":"requeued","job_id":"x","kind":"candidate_manifest_job","target":"private","attempt":1,"queue_wait_ms":0,"handle_ms":1,"outcome":"retryable","next_not_before":"2026-08-08T00:00:00Z"}
2026/08/08 candidate operation: {"schema":"phebs-candidate-operation-v1","repository":"private","attempt":{"number":1},"decision":"warm_noop","outcome":"success","queue_wait_ms":0,"mirror_lock_wait_ms":0,"tree_ms":0,"spooling_ms":0,"external_sort_ms":0,"peak_spool_bytes":0,"publish_ms":0,"fingerprint_ms":0,"database_commit_ms":0,"marker_finish_ms":0,"total_ms":0,"declared_source_bytes":0,"planes":{"repository":{"records":0,"members":0,"canonical_bytes":0,"declared_bytes":0},"local":{"records":0,"members":0,"canonical_bytes":0,"declared_bytes":0},"caller":{"records":0,"members":0,"canonical_bytes":0,"declared_bytes":0}},"typed_input":{"configured":0,"present":0,"declared_bytes":0},"manifest_summary_present":false}
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	metrics, err := parseLogMetrics(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.OrchestrationTransactions != 2 || metrics.Retries != 1 || metrics.ReusedControls != 1 {
		t.Fatalf("metrics = %+v", metrics)
	}
	v30, err := parseLogMetricsForPlan(path, 0, PlanSchemaV30)
	if err != nil {
		t.Fatal(err)
	}
	if v30.ReusedControls != 0 {
		t.Fatalf("V30 counted non-production reuse outcome: %+v", v30)
	}
}

func TestV30CandidateReuseCursorWaitsForSuccessfulQuiescence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newCandidateReuseCursor(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	appendLog := func(lines string) {
		t.Helper()
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.WriteString(lines)
		if closeErr := file.Close(); writeErr != nil || closeErr != nil {
			t.Fatal(errors.Join(writeErr, closeErr))
		}
	}
	appendLog("job lifecycle: {\"schema\":\"phebs-job-lifecycle-v1\",\"event\":\"claimed\",\"job_id\":\"candidate:1\",\"outcome\":\"claimed\"}\n" +
		"candidate operation: {\"schema\":\"phebs-candidate-operation-v1\",\"decision\":\"cold_reuse\",\"outcome\":\"done\"}\n")
	if settled, err := cursor.poll(); err != nil || settled {
		t.Fatalf("active reuse settled=%t err=%v", settled, err)
	}
	if settled, err := cursor.poll(); err != nil || settled {
		t.Fatalf("unresolved reuse settled=%t err=%v", settled, err)
	}
	appendLog("job lifecycle: {\"schema\":\"phebs-job-lifecycle-v1\",\"event\":\"done\",\"job_id\":\"candidate:1\",\"outcome\":\"success\"}\n")
	if settled, err := cursor.poll(); err != nil || settled {
		t.Fatalf("newly completed reuse settled=%t err=%v", settled, err)
	}
	if settled, err := cursor.poll(); err != nil || !settled {
		t.Fatalf("quiet reuse settled=%t err=%v", settled, err)
	}
}

func TestV30CandidateReuseRejectsFailedAndRetriedWork(t *testing.T) {
	for _, line := range []string{
		"candidate operation: {\"schema\":\"phebs-candidate-operation-v1\",\"decision\":\"warm_noop\",\"outcome\":\"failed\"}\n",
		"job lifecycle: {\"schema\":\"phebs-job-lifecycle-v1\",\"event\":\"requeued\",\"job_id\":\"candidate:1\",\"outcome\":\"retryable\"}\n",
	} {
		t.Run(strings.Fields(line)[0], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.log")
			if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			cursor, err := newCandidateReuseCursor(path, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cursor.Close() }()
			if _, err := cursor.poll(); err == nil {
				t.Fatal("V30 accepted failed warm work")
			}
		})
	}
}

func TestV30CandidateReuseKeepsDeferredWorkUnresolved(t *testing.T) {
	for _, event := range []string{"deferred", "yielded"} {
		t.Run(event, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.log")
			lines := "job lifecycle: {\"schema\":\"phebs-job-lifecycle-v1\",\"event\":\"claimed\",\"job_id\":\"candidate:1\",\"outcome\":\"claimed\"}\n" +
				"candidate operation: {\"schema\":\"phebs-candidate-operation-v1\",\"decision\":\"cold_reuse\",\"outcome\":\"done\"}\n" +
				"job lifecycle: {\"schema\":\"phebs-job-lifecycle-v1\",\"event\":\"" + event + "\",\"job_id\":\"candidate:1\",\"outcome\":\"pending\"}\n"
			if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
				t.Fatal(err)
			}
			cursor, err := newCandidateReuseCursor(path, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cursor.Close() }()
			if settled, err := cursor.poll(); err != nil || settled {
				t.Fatalf("initial settled=%t err=%v", settled, err)
			}
			if settled, err := cursor.poll(); err != nil || settled {
				t.Fatalf("deferred settled=%t err=%v", settled, err)
			}
		})
	}
}

func TestV30CandidateReuseRejectsMixedContentWork(t *testing.T) {
	for _, decision := range []string{"rebuild", "repair"} {
		t.Run(decision, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.log")
			lines := "candidate operation: {\"schema\":\"phebs-candidate-operation-v1\",\"decision\":\"" + decision + "\",\"outcome\":\"done\"}\n" +
				"candidate operation: {\"schema\":\"phebs-candidate-operation-v1\",\"decision\":\"cold_reuse\",\"outcome\":\"done\"}\n"
			if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
				t.Fatal(err)
			}
			cursor, err := newCandidateReuseCursor(path, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cursor.Close() }()
			if _, err := cursor.poll(); err == nil {
				t.Fatalf("V30 accepted %s followed by reuse", decision)
			}
		})
	}
}

func TestV30CandidateReuseWaitsForCompleteLogLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	line := "candidate operation: {\"schema\":\"phebs-candidate-operation-v1\",\"decision\":\"cold_reuse\",\"outcome\":\"done\"}"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	opened, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	cursor, err := newCandidateReuseCursor(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	if settled, err := cursor.poll(); err != nil || settled {
		t.Fatalf("partial line settled=%t err=%v", settled, err)
	}
	if _, err := opened.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	if settled, err := cursor.poll(); err != nil || settled {
		t.Fatalf("completed line settled without quiet poll=%t err=%v", settled, err)
	}
	if settled, err := cursor.poll(); err != nil || !settled {
		t.Fatalf("quiet complete line settled=%t err=%v", settled, err)
	}
}

func TestV30PhaseLogHandoffRetainsPostBoundaryReports(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "server.log")
	logFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logFile.Close() }()
	reuse := "candidate operation: {\"schema\":\"phebs-candidate-operation-v1\",\"decision\":\"cold_reuse\",\"outcome\":\"done\"}\n"
	if _, err := logFile.WriteString(reuse); err != nil {
		t.Fatal(err)
	}
	cursor, err := newCandidateReuseCursor(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if settled, err := cursor.poll(); err != nil || settled {
		t.Fatalf("new reuse settled=%t err=%v", settled, err)
	}
	if settled, err := cursor.poll(); err != nil || !settled {
		t.Fatalf("quiet reuse settled=%t err=%v", settled, err)
	}
	boundary, err := cursor.settledOffset()
	if closeErr := cursor.Close(); err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	server := &privateServer{log: logFile, logPath: path, sampler: newSyntheticRSSSampler(10)}
	if err := server.primeLogOffset(boundary); err != nil {
		t.Fatal(err)
	}
	if _, err := server.sampler.phaseMetricsAndResetWindow(); err != nil {
		t.Fatal(err)
	}
	ordinary := "ordinary log line\n"
	if _, err := logFile.WriteString(ordinary); err != nil {
		t.Fatal(err)
	}
	settledBoundary, err := server.settlePrimedLogHandoff(boundary)
	if err != nil || settledBoundary != boundary+int64(len(ordinary)) {
		t.Fatalf("settled boundary = %d, err=%v", settledBoundary, err)
	}
	postBoundary := "candidate operation: {\"schema\":\"phebs-candidate-operation-v1\",\"decision\":\"rebuild\",\"outcome\":\"done\"}\n" +
		"job lifecycle: {\"schema\":\"phebs-job-lifecycle-v1\",\"event\":\"requeued\",\"job_id\":\"candidate:2\",\"outcome\":\"retryable\"}\n"
	if _, err := logFile.WriteString(postBoundary); err != nil {
		t.Fatal(err)
	}
	warm, err := parseLogMetricsForPlanThrough(path, 0, settledBoundary, PlanSchemaV30)
	if err != nil || warm.ReusedControls != 1 || warm.OrchestrationTransactions != 0 || warm.Retries != 0 {
		t.Fatalf("bounded warm metrics = %+v, err=%v", warm, err)
	}

	meter, err := beginPhaseMeter(server, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = meter.allocation.close() }()
	if meter.logOffset != settledBoundary {
		t.Fatalf("next phase offset = %d, want %d", meter.logOffset, settledBoundary)
	}
	delta, err := parseLogMetricsForPlan(path, meter.logOffset, PlanSchemaV30)
	if err != nil || delta.OrchestrationTransactions != 1 || delta.Retries != 1 {
		t.Fatalf("delta metrics = %+v, err=%v", delta, err)
	}
	deltaCursor, err := newCandidateReuseCursor(path, meter.logOffset)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = deltaCursor.Close() }()
	if _, err := deltaCursor.poll(); err == nil {
		t.Fatal("post-boundary candidate report was dropped")
	}
}

func TestV30PhaseLogHandoffRejectsWorkBetweenPrimeAndProcessReset(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "server.log")
	logFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logFile.Close() }()
	server := &privateServer{log: logFile, logPath: path, sampler: newSyntheticRSSSampler(10)}
	meter, err := beginPhaseMeter(server, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.sampler.peakRSS = 41
	server.sampler.strictGitChildren = 1
	meter.beforeProcessSnapshot = func() error {
		if err := server.primeLogOffset(meter.logOffset); err != nil {
			return err
		}
		_, err := logFile.WriteString(
			"job lifecycle: {\"schema\":\"phebs-job-lifecycle-v1\",\"event\":\"claimed\",\"job_id\":\"candidate:2\",\"outcome\":\"claimed\"}\n",
		)
		return err
	}
	metrics, err := meter.finish(nil, PlanSchemaV30)
	resetMetrics, resetErr := server.sampler.phaseMetrics()
	if !errors.Is(err, errExactOracle) || metrics.PeakRSSBytes != 41 ||
		metrics.GitChildren != 1 || resetErr != nil || resetMetrics.PeakRSSBytes != 0 ||
		resetMetrics.GitChildren != 0 {
		t.Fatalf("straddled exact work metrics=%+v reset=%+v resetErr=%v err=%v",
			metrics, resetMetrics, resetErr, err)
	}
}

func TestV30PhaseLogHandoffRejectsExactTailAfterProcessReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	logFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logFile.Close() }()
	server := &privateServer{logPath: path, sampler: newSyntheticRSSSampler(10)}
	if err := server.primeLogOffset(0); err != nil {
		t.Fatal(err)
	}
	if _, err := server.sampler.phaseMetricsAndResetWindow(); err != nil {
		t.Fatal(err)
	}
	if _, err := logFile.WriteString(
		"job lifecycle: {\"schema\":\"phebs-job-lifecycle-v1\",\"event\":\"claimed\",\"job_id\":\"candidate:2\",\"outcome\":\"claimed\"}\n",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := server.settlePrimedLogHandoff(0); !errors.Is(err, errExactOracle) {
		t.Fatalf("post-reset exact tail = %v, want exact refusal", err)
	}
}

func TestV30PhaseLogHandoffRejectsPartialTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, []byte("job life"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &privateServer{logPath: path}
	if err := server.primeLogOffset(0); err != nil {
		t.Fatal(err)
	}
	if _, err := server.settlePrimedLogHandoff(0); err == nil {
		t.Fatal("partial handoff tail passed")
	}
}

func TestAuthorityChangeAccountingIsPlaneExact(t *testing.T) {
	before := privateProfileSnapshot{
		SourceGeneration: "source-a", SearchGeneration: "search-a", ObservationGeneration: "observation-a",
		ExtractionGeneration: "extract-a", CallerGeneration: "caller-a",
		RelationshipGeneration: "relationship-a", RelationshipRootDigest: "root-a",
	}
	after := before
	after.SearchGeneration = "search-b"
	after.ObservationGeneration = "observation-b"
	after.CallerGeneration = "caller-b"
	if got := authorityChanges(before, after); got != 3 {
		t.Fatalf("authority changes = %d", got)
	}
}

func TestMergeMetricsSumsEventsButKeepsAbsoluteGaugeMaxima(t *testing.T) {
	got, err := mergeMetrics(
		PhaseMetrics{WallMS: 2, PeakRSSBytes: 8, DataLogicalBytes: 12, DataAllocatedBytes: 6, Retries: 1, OtherToGitTransitions: 1},
		PhaseMetrics{WallMS: 3, PeakRSSBytes: 7, DataLogicalBytes: 10, DataAllocatedBytes: 9, Retries: 2, OtherToGitTransitions: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.WallMS != 5 || got.PeakRSSBytes != 8 || got.DataLogicalBytes != 12 ||
		got.DataAllocatedBytes != 9 || got.Retries != 3 || got.OtherToGitTransitions != 3 {
		t.Fatalf("merged metrics = %+v", got)
	}
}

func TestMergeConcurrentMetricsUsesOuterWallAndConservativeRSSSum(t *testing.T) {
	outer := PhaseMetrics{
		WallMS: 100, PeakRSSBytes: 8, DataLogicalBytes: 12, DataAllocatedBytes: 6,
		OtherChildren: 2, ControlReads: 3,
	}
	concurrent := PhaseMetrics{
		WallMS: 30, PeakRSSBytes: 7, DataLogicalBytes: 10, DataAllocatedBytes: 9,
		OtherChildren: 1, ControlReads: 4,
	}
	got, err := mergeConcurrentMetrics(outer, concurrent)
	if err != nil || got.WallMS != 100 || got.PeakRSSBytes != 15 ||
		got.DataLogicalBytes != 12 || got.DataAllocatedBytes != 9 ||
		got.OtherChildren != 3 || got.ControlReads != 7 {
		t.Fatalf("concurrent metrics = %+v, %v", got, err)
	}
	concurrent.PeakRSSBytes = 1<<63 - 1
	if _, err := mergeConcurrentMetrics(outer, concurrent); err == nil {
		t.Fatal("concurrent RSS overflow was accepted")
	}
}

func TestMeasuredCommandSanitizationRetainsOnlyCustodySentinels(t *testing.T) {
	private := errors.New("private command detail")
	for _, retained := range []error{
		errProcessSamplingFailed,
		errAllocationSamplingFailed,
		errPrivateServerShutdownUnproven,
	} {
		got := sanitizeMeasuredCommandFailure("measured command failed", errors.Join(private, retained), false)
		if !errors.Is(got, retained) || errors.Is(got, private) || strings.Contains(got.Error(), private.Error()) {
			t.Fatalf("sanitized %v = %v", retained, got)
		}
	}
	deadline := newDataMeasurementDeadlineError(false)
	historical := sanitizeMeasuredCommandFailure("measured command failed", deadline, false)
	current := sanitizeMeasuredCommandFailure("measured command failed", deadline, true)
	if projectDataMeasurementDeadline(historical) != nil ||
		projectDataMeasurementDeadline(current) == nil {
		t.Fatalf("versioned data-measurement sanitization = historical:%v current:%v", historical, current)
	}
}

func TestStrictMeasuredCommandRetainsSignaledShutdownUncertainty(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("strict process sessions require Linux or macOS")
	}
	for _, test := range []struct {
		name   string
		strict bool
		want   bool
	}{
		{name: "historical", strict: false},
		{name: "V25", strict: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := runMeasuredCommand(
				exec.CommandContext(t.Context(), "/bin/sh", "-c", "kill -KILL $$"), t.TempDir(), test.strict,
			)
			if errors.Is(err, errPrivateServerShutdownUnproven) != test.want {
				t.Fatalf("signaled measured command = %v", err)
			}
		})
	}
}

func TestAllocationSamplerRetainsCapacityTroughAfterSpaceReturns(t *testing.T) {
	root := t.TempDir()
	sampler, err := newAllocationSampler(root, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	sampler.mu.Lock()
	sampler.minimumAvailable = sampler.baselineAvailable - 8192
	sampler.mu.Unlock()
	peak, err := sampler.close()
	if err != nil {
		t.Fatal(err)
	}
	if peak < 4096+8192 {
		t.Fatalf("peak allocation = %d, want at least %d", peak, 4096+8192)
	}
	second, err := sampler.close()
	if err != nil || second != peak {
		t.Fatalf("repeated close = %d, %v; want %d", second, err, peak)
	}
}

func TestV27RawEndBoundaryUsesRawGaugeAndIsOneShotForOneWorkspace(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("exact data-byte measurement is supported on Linux and macOS")
	}
	root := filepath.Clean(t.TempDir())
	logPath := filepath.Join(root, "server.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, rawAllocated, err := measureDataBytesForContract(root, true)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := newAllocationSampler(root, rawAllocated, true)
	if err != nil {
		t.Fatal(err)
	}
	allocation.mu.Lock()
	drop := int64(4096)
	if allocation.baselineAvailable < drop {
		drop = allocation.baselineAvailable
	}
	allocation.minimumAvailable = allocation.baselineAvailable - drop
	allocation.mu.Unlock()
	meter := &phaseMeter{
		started: time.Now(), server: &privateServer{logPath: logPath}, dataDir: root,
		allocation: allocation, strict: true, captureRaw: true,
	}
	metrics, err := meter.finish(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := meter.takeRawEndBoundary()
	if err != nil {
		t.Fatal(err)
	}
	allocated, err := boundary.consume(root)
	if err != nil {
		t.Fatal(err)
	}
	if allocated != rawAllocated || metrics.DataAllocatedBytes < allocated+drop {
		t.Fatalf("raw/peak allocation = %d/%d, want raw %d and peak >= %d",
			allocated, metrics.DataAllocatedBytes, rawAllocated, allocated+drop)
	}
	if _, err := boundary.consume(root); err == nil {
		t.Fatal("raw-end boundary was consumed twice")
	}
	if _, err := meter.takeRawEndBoundary(); err == nil {
		t.Fatal("raw-end boundary was handed off twice")
	}
	other, err := newDataMeasurementBoundary(root, rawAllocated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.consume(filepath.Clean(t.TempDir())); err == nil {
		t.Fatal("raw-end boundary crossed workspaces")
	}
}

func TestDataMeasurementDeadlineProjectionIsTypedAndSourceFree(t *testing.T) {
	for _, test := range []struct {
		name     string
		apparent bool
		gauge    string
	}{
		{name: "allocated", gauge: dataMeasurementAllocated},
		{name: "logical", apparent: true, gauge: dataMeasurementLogical},
	} {
		t.Run(test.name, func(t *testing.T) {
			private := errors.New("/private/custody/secret")
			projection := projectDataMeasurementDeadline(errors.Join(
				private, newDataMeasurementDeadlineError(test.apparent),
			))
			if projection == nil || projection.Schema != dataMeasurementFailureSchemaV1 ||
				projection.Scope != dataMeasurementScope || projection.Gauge != test.gauge ||
				projection.Reason != dataMeasurementDeadline ||
				projection.DeadlineMS != 30_000 {
				t.Fatalf("deadline projection = %+v", projection)
			}
			raw, err := json.Marshal(projection)
			if err != nil || strings.Contains(string(raw), private.Error()) {
				t.Fatalf("source-free projection = %s, %v", raw, err)
			}
		})
	}
	if got := projectDataMeasurementDeadline(errors.New("ordinary failure")); got != nil {
		t.Fatalf("ordinary failure projected as deadline: %+v", got)
	}
	var typedNil *dataMeasurementDeadlineError
	if got := projectDataMeasurementDeadline(typedNil); got != nil {
		t.Fatalf("typed nil projected as deadline: %+v", got)
	}
}

func TestAllocationSamplerRetainsBoundedFirstFailure(t *testing.T) {
	sampler, err := newAllocationSampler(t.TempDir(), 0, true)
	if err != nil {
		t.Fatal(err)
	}
	first := errors.New("first capacity failure")
	later := errors.New("later capacity failure")
	sampler.recordFailure(first)
	for range 10_000 {
		sampler.recordFailure(later)
	}
	_, err = sampler.close()
	if !errors.Is(err, errAllocationSamplingFailed) || !errors.Is(err, first) || errors.Is(err, later) ||
		sampler.failedSamples != 10_001 || strings.Count(err.Error(), first.Error()) != 1 ||
		strings.Contains(err.Error(), later.Error()) {
		t.Fatalf("bounded allocation failure = first:%v count:%d err:%v",
			sampler.err, sampler.failedSamples, err)
	}
}

func TestV30BoundaryJoinsProcessAndAllocationFailures(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "server.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, allocated, err := measureDataBytesForContract(root, true)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := newAllocationSampler(root, allocated, true)
	if err != nil {
		t.Fatal(err)
	}
	allocationCause := errors.New("synthetic allocation failure")
	allocation.recordFailure(allocationCause)
	processCause := errors.New("synthetic process failure")
	sampler := newSyntheticRSSSampler(10)
	sampler.recordFailure(processCause)
	server := &privateServer{logPath: logPath, sampler: sampler}
	if err := server.primeLogOffset(0); err != nil {
		t.Fatal(err)
	}
	meter := &phaseMeter{
		started: time.Now(), server: server, dataDir: root, allocation: allocation, strict: true,
		beforeProcessSnapshot: func() error { return nil },
	}
	_, err = meter.finish(nil, PlanSchemaV30)
	if !errors.Is(err, errProcessSamplingFailed) || !errors.Is(err, processCause) ||
		!errors.Is(err, errAllocationSamplingFailed) || !errors.Is(err, allocationCause) {
		t.Fatalf("joined boundary measurement error = %v", err)
	}
}

func TestV30BoundarySemanticFailureIsNotMeasurementFailure(t *testing.T) {
	for _, withAllocationFailure := range []bool{false, true} {
		for _, withProcessFailure := range []bool{false, true} {
			name := strconv.FormatBool(withAllocationFailure) + "/" + strconv.FormatBool(withProcessFailure)
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				_, allocated, err := measureDataBytesForContract(root, true)
				if err != nil {
					t.Fatal(err)
				}
				allocation, err := newAllocationSampler(root, allocated, true)
				if err != nil {
					t.Fatal(err)
				}
				if withAllocationFailure {
					allocation.recordFailure(errors.New("synthetic allocation failure"))
				}
				sampler := newSyntheticRSSSampler(10)
				if withProcessFailure {
					sampler.recordFailure(errors.New("synthetic process failure"))
				}
				semantic := errConvergenceDeadline
				meter := &phaseMeter{
					started: time.Now(), dataDir: root, allocation: allocation, strict: true,
					server:                &privateServer{sampler: sampler},
					beforeProcessSnapshot: func() error { return semantic },
				}
				run := execution{plan: Plan{Schema: PlanSchemaV30}}
				_, err = run.finishMeter(meter, nil)
				if !errors.Is(err, semantic) || errors.Is(run.measurementErr, semantic) {
					t.Fatalf("boundary error=%v measurement=%v", err, run.measurementErr)
				}
				if got := errors.Is(run.measurementErr, errAllocationSamplingFailed); got != withAllocationFailure {
					t.Fatalf("allocation measurement retained=%t, want %t: %v", got, withAllocationFailure, run.measurementErr)
				}
				if got := errors.Is(run.measurementErr, errProcessSamplingFailed); got != withProcessFailure {
					t.Fatalf("process measurement retained=%t, want %t: %v", got, withProcessFailure, run.measurementErr)
				}
				classification := classifyStoppedFailureForPlan(
					run.plan, err, run.measurementErr, nil,
				)
				want := "convergence_deadline_expired"
				switch {
				case withAllocationFailure && withProcessFailure:
					want = "failed_phase_measurement_unavailable"
				case withAllocationFailure:
					want = "failed_phase_allocation_sampling_unavailable"
				case withProcessFailure:
					want = "failed_phase_process_sampling_unavailable"
				}
				if classification.code != want {
					t.Fatalf("classification = %+v, want %s", classification, want)
				}
			})
		}
	}
}

func TestV30BoundarySemanticFailureRetainsSafetyMetrics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, allocated, err := measureDataBytesForContract(root, true)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := newAllocationSampler(root, allocated, true)
	if err != nil {
		t.Fatal(err)
	}
	allocation.mu.Lock()
	allocation.minimumAvailable -= 4096
	allocation.mu.Unlock()
	wantAllocated := allocated + 4096
	sampler := newSyntheticRSSSampler(10)
	sampler.peakRSS = 101
	sampler.strictGitChildren = 1
	cause := exactOracle("synthetic boundary refusal")
	meter := &phaseMeter{
		started: time.Now().Add(-time.Millisecond), dataDir: root, allocation: allocation, strict: true,
		server: &privateServer{sampler: sampler}, beforeProcessSnapshot: func() error { return cause },
	}
	plan := Plan{Schema: PlanSchemaV30, Safety: SafetyEnvelope{
		MaximumTotalWallMS: 1 << 60, MaximumPeakRSSBytes: 100,
		MaximumDataAllocatedBytes: 1 << 60, MaximumPrePressureBytes: 1 << 60,
	}}
	run := execution{
		plan: plan, workspace: root,
		observation: emptyObservationForPlan(EnvironmentObservation{}, plan),
	}
	run.startPhase(2)
	run.trackMeter(meter)
	metrics, err := run.finishMeter(meter, nil)
	if !errors.Is(err, errExactOracle) || run.measurementErr != nil ||
		metrics.PeakRSSBytes != 101 || metrics.GitChildren != 1 ||
		metrics.WallMS <= 0 || metrics.DataLogicalBytes <= 0 ||
		metrics.DataAllocatedBytes < wantAllocated || run.partialMetrics.PeakRSSBytes != 101 ||
		run.partialMetrics.GitChildren != 1 {
		t.Fatalf("boundary metrics=%+v partial=%+v measurement=%v err=%v",
			metrics, run.partialMetrics, run.measurementErr, err)
	}
	measurementErr := run.captureFailedPhase()
	ceilingErr := run.enforceSafety()
	classification := classifyStoppedFailureForPlan(plan, cause, measurementErr, ceilingErr)
	if measurementErr != nil || !errors.Is(ceilingErr, errReviewCeiling) ||
		classification.code != "review_ceiling_crossed" ||
		run.observation.Phases[2].Metrics.PeakRSSBytes != 101 ||
		run.observation.Phases[2].Metrics.GitChildren != 1 ||
		run.observation.Phases[2].Metrics.WallMS <= 0 ||
		run.observation.Phases[2].Metrics.DataLogicalBytes <= 0 ||
		run.observation.Phases[2].Metrics.DataAllocatedBytes < wantAllocated {
		t.Fatalf("failed phase=%+v measurement=%v ceiling=%v classification=%+v",
			run.observation.Phases[2], measurementErr, ceilingErr, classification)
	}
}

func TestV30CleanupFailureRetainsSafetyMetrics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "server.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, allocated, err := measureDataBytesForContract(root, true)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := newAllocationSampler(root, allocated, true)
	if err != nil {
		t.Fatal(err)
	}
	sampler := newSyntheticRSSSampler(10)
	sampler.peakRSS = 101
	sampler.strictGitChildren = 1
	cause := errors.New("synthetic finish cleanup failure")
	meter := &phaseMeter{
		started: time.Now().Add(-time.Millisecond), dataDir: root, allocation: allocation, strict: true,
		server: &privateServer{logPath: logPath, sampler: sampler}, finishCleanup: func() error { return cause },
	}
	plan := Plan{Schema: PlanSchemaV30, Safety: SafetyEnvelope{
		MaximumTotalWallMS: 1 << 60, MaximumPeakRSSBytes: 100,
		MaximumDataAllocatedBytes: 1 << 60, MaximumPrePressureBytes: 1 << 60,
	}}
	run := execution{
		plan: plan, workspace: root,
		observation: emptyObservationForPlan(EnvironmentObservation{}, plan),
	}
	run.startPhase(2)
	run.trackMeter(meter)
	metrics, err := run.finishMeter(meter, nil)
	if !errors.Is(err, cause) || run.measurementErr != nil || !meter.boundaryFailed ||
		metrics.PeakRSSBytes != 101 || metrics.GitChildren != 1 ||
		run.partialMetrics.PeakRSSBytes != 101 || run.partialMetrics.GitChildren != 1 {
		t.Fatalf("cleanup metrics=%+v partial=%+v measurement=%v err=%v",
			metrics, run.partialMetrics, run.measurementErr, err)
	}
	measurementErr := run.captureFailedPhase()
	ceilingErr := run.enforceSafety()
	classification := classifyStoppedFailureForPlan(plan, err, measurementErr, ceilingErr)
	if measurementErr != nil || !errors.Is(ceilingErr, errReviewCeiling) ||
		classification.code != "review_ceiling_crossed" ||
		run.observation.Phases[2].Metrics.PeakRSSBytes != 101 ||
		run.observation.Phases[2].Metrics.GitChildren != 1 {
		t.Fatalf("failed phase=%+v measurement=%v ceiling=%v classification=%+v",
			run.observation.Phases[2], measurementErr, ceilingErr, classification)
	}
}

func TestPhaseMeterRetainsProcessFailureWhenDataGaugeFails(t *testing.T) {
	root := t.TempDir()
	_, allocated, err := measureDataBytesForContract(root, true)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := newAllocationSampler(root, allocated, true)
	if err != nil {
		t.Fatal(err)
	}
	processCause := errors.New("synthetic process failure")
	cleanupCause := errors.New("synthetic finish cleanup failure")
	sampler := newSyntheticRSSSampler(10)
	sampler.recordFailure(processCause)
	meter := &phaseMeter{
		started: time.Now(), server: &privateServer{sampler: sampler},
		dataDir: "relative", allocation: allocation, strict: true,
		finishCleanup: func() error { return cleanupCause },
	}
	_, err = meter.finish(nil, PlanSchemaV30)
	if err == nil || !errors.Is(err, errProcessSamplingFailed) ||
		!errors.Is(err, processCause) || !errors.Is(err, cleanupCause) {
		t.Fatalf("data/process measurement error = %v", err)
	}
}

func TestHistoricalAllocationSamplerRetainsJoinedFailures(t *testing.T) {
	sampler, err := newAllocationSampler(t.TempDir(), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	first := errors.New("first historical capacity failure")
	later := errors.New("later historical capacity failure")
	sampler.recordFailure(first)
	sampler.recordFailure(later)
	_, err = sampler.close()
	if !errors.Is(err, first) || !errors.Is(err, later) || errors.Is(err, errAllocationSamplingFailed) ||
		sampler.failedSamples != 0 {
		t.Fatalf("historical allocation failure changed: count=%d err=%v", sampler.failedSamples, err)
	}
}

func TestDataMeasurementProcessContractChangesOnlyAtV25(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("exact data-byte measurement is supported on Linux and macOS")
	}
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	if _, _, err := measureDataBytesForPlan(Plan{Schema: PlanSchemaV24}, root); err == nil {
		t.Fatal("historical measurement ignored its ambient du contract")
	}
	if _, _, err := measureDataBytesForPlan(Plan{Schema: PlanSchemaV25}, root); err != nil {
		t.Fatalf("V25 measurement did not use its absolute bounded du contract: %v", err)
	}
	if _, err := measureDataAllocatedBytesForContract(root, true); err != nil {
		t.Fatalf("V27 allocated-only fallback did not use its absolute bounded du contract: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := duKilobytesContext(canceled, root, false); err == nil ||
		projectDataMeasurementDeadline(err) != nil {
		t.Fatal("V25 du measurement ignored its context bound")
	}
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	if _, err := duKilobytesContext(expired, root, false); err == nil ||
		errors.Is(err, context.DeadlineExceeded) ||
		projectDataMeasurementDeadline(err).Gauge != dataMeasurementAllocated {
		t.Fatalf("typed gauge deadline changed its historical context identity: %v", err)
	}
}
