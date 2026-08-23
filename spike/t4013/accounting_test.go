package t4013

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
	got := mergeMetrics(
		PhaseMetrics{WallMS: 2, PeakRSSBytes: 8, DataLogicalBytes: 12, DataAllocatedBytes: 6, Retries: 1},
		PhaseMetrics{WallMS: 3, PeakRSSBytes: 7, DataLogicalBytes: 10, DataAllocatedBytes: 9, Retries: 2},
	)
	if got.WallMS != 5 || got.PeakRSSBytes != 8 || got.DataLogicalBytes != 12 ||
		got.DataAllocatedBytes != 9 || got.Retries != 3 {
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

func TestAllocationSamplerRetainsCapacityTroughAfterSpaceReturns(t *testing.T) {
	root := t.TempDir()
	sampler, err := newAllocationSampler(root, 4096)
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
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := duKilobytesContext(canceled, root, false); err == nil {
		t.Fatal("V25 du measurement ignored its context bound")
	}
}
