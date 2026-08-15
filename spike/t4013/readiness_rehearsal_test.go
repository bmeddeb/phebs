package t4013

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/spike/t401"
)

const readinessRehearsalEnvironment = "PHEBS_T4013_READINESS_REHEARSAL"
const exactSemanticTimingEnvironment = "PHEBS_T4013_EXACT_SEMANTIC_TIMING"

// The exact semantic profile has 264 partitions under the historical shared
// whole-repository shape. The versioned Proto and Thrift execution-subrange
// policies add four partitions apiece without changing candidate ownership.
const exactSemanticExtractionPartitions = 272

// TestExactSemanticColdTiming measures the frozen 262,144-blob semantic shape
// through the ordinary production binary before a Take 19 freeze. It retains
// no authored source or derived custody and reports only cold wall/RSS/disk
// scalars against the unchanged v14 ceilings.
func TestExactSemanticColdTiming(t *testing.T) {
	if os.Getenv(exactSemanticTimingEnvironment) != "1" {
		t.Skip("set " + exactSemanticTimingEnvironment + "=1 to run the exact semantic timing gate")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Hour)
	defer cancel()
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot, err = filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	sourceCommit, err := gitOutput(ctx, moduleRoot, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyCleanCheckout(ctx, moduleRoot, sourceCommit); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	toolchain, err := buildWorkingTreeToolchain(ctx, moduleRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	var semantic t401.Profile
	for _, profile := range profiles {
		if profile.Kind == "semantic" {
			semantic = profile
			break
		}
	}
	if semantic.Name != "semantic-262144-v1" || semantic.Aggregate.UniqueGoBlobs != 262_144 {
		t.Fatalf("exact semantic profile = %+v", semantic)
	}
	profile, err := prepareTimingProfile(ctx, moduleRoot, workspace, semantic)
	if err != nil {
		t.Fatal(err)
	}
	server, err := launchPrivateServer(ctx, profile, toolchain, "exact-semantic-cold")
	if err != nil {
		t.Fatal(err)
	}
	running := true
	defer func() {
		if running {
			_ = server.stop(30 * time.Second)
		}
	}()
	if _, err := awaitPrivateServerHealth(ctx, server, profile, "exact-semantic-cold", 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result := captureSemanticColdConvergence(t, ctx, profile, server, "a", 4*time.Hour)
	captureWall := time.Since(started)
	coldWall := time.Duration(result.coldWallMS) * time.Millisecond
	peakRSS, _, _, _ := server.sampler.metrics()
	logical, allocated, err := measureDataBytes(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.stop(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	running = false

	freezeReady := result.converged && coldWall < 4*time.Hour && result.tail.TimingCaptureOK &&
		result.snapshot.ObservationRecords == 262_144 &&
		exactSemanticScheduleReady(result.snapshot) &&
		peakRSS <= frozenSafetyV14.MaximumPeakRSSBytes &&
		allocated <= frozenSafetyV14.MaximumDataAllocatedBytes
	fit := semanticColdTimingFit{
		Schema:                         "t4013-take19-semantic-fit-v4",
		SourceCommit:                   sourceCommit,
		Profile:                        profile.Name,
		Diagnostic:                     "TestExactSemanticColdTiming",
		Converged:                      result.converged,
		FreezeReady:                    freezeReady,
		TerminalClass:                  result.terminal,
		ColdDeadlineMS:                 (4 * time.Hour).Milliseconds(),
		WallMS:                         result.coldWallMS,
		CaptureWallMS:                  captureWall.Milliseconds(),
		ColdHeadroomMS:                 (4*time.Hour - coldWall).Milliseconds(),
		LastStage:                      result.tail.LastStage,
		LastErrorClass:                 result.tail.LastErrorClass,
		FinalCaptureState:              result.tail.FinalCaptureState,
		TimingCaptureOK:                result.tail.TimingCaptureOK,
		TimingSemantics:                "validated_report_floor",
		TimingErrorClass:               result.tail.TimingErrorClass,
		StageEntryMS:                   result.tail.StageEntryMS,
		ExtractionEntryMS:              result.tail.StageEntryMS["extraction_publication"],
		RelationshipEntryMS:            result.tail.StageEntryMS["relationship_publication"],
		ExpectedRecords:                262_144,
		ObservationRecords:             result.snapshot.ObservationRecords,
		ExpectedExtractionPartitions:   exactSemanticExtractionPartitions,
		ApplicableExtractionPartitions: result.snapshot.ApplicablePartitions,
		SettledExtractionPartitions:    result.snapshot.SettledPartitions,
		SelectedV2PublicationCompleted: result.tail.StageEntryMS["extraction_publication"] != 0,
		LastExtractionWallMS:           result.tail.LastExtractionWall,
		LastExtraction:                 result.tail.LastExtraction,
		ExtractionTiming:               result.tail.Timing,
		PeakRSSBytes:                   peakRSS,
		LogicalBytes:                   logical,
		AllocatedBytes:                 allocated,
		RSSCeilingBytes:                frozenSafetyV14.MaximumPeakRSSBytes,
		AllocationCeilingBytes:         frozenSafetyV14.MaximumDataAllocatedBytes,
	}
	raw, marshalErr := json.Marshal(fit)
	if marshalErr != nil || len(raw) > MaxObservationBytes {
		t.Fatalf("semantic cold timing fit encoding: error=%v bytes=%d", marshalErr, len(raw))
	}
	// The operator seals this logged line as the source-free fit record; it
	// carries only counts, enum states, and millisecond timings.
	t.Logf("SEMANTIC_COLD_TIMING_FIT %s", raw)

	if !freezeReady {
		t.Fatalf(
			"exact semantic timing refused: converged=%t wall_ms=%d records=%d applicable_partitions=%d settled_partitions=%d last_stage=%s extraction_entry_ms=%d terminal=%q peak_rss_bytes=%d allocated_bytes=%d",
			result.converged, result.coldWallMS, result.snapshot.ObservationRecords,
			result.snapshot.ApplicablePartitions, result.snapshot.SettledPartitions,
			result.tail.LastStage, result.tail.StageEntryMS["extraction_publication"], result.terminal,
			peakRSS, allocated,
		)
	}
}

func exactSemanticScheduleReady(snapshot privateProfileSnapshot) bool {
	return snapshot.ApplicablePartitions == exactSemanticExtractionPartitions &&
		snapshot.SettledPartitions == exactSemanticExtractionPartitions &&
		snapshot.RetryExhaustedPartitions == 0
}

func TestExactSemanticScheduleReady(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		snapshot privateProfileSnapshot
		want     bool
	}{
		{name: "exact", snapshot: privateProfileSnapshot{ApplicablePartitions: 272, SettledPartitions: 272}, want: true},
		{name: "historical-shape", snapshot: privateProfileSnapshot{ApplicablePartitions: 264, SettledPartitions: 264}},
		{name: "not-settled", snapshot: privateProfileSnapshot{ApplicablePartitions: 272, SettledPartitions: 271}},
		{name: "retry-exhausted", snapshot: privateProfileSnapshot{ApplicablePartitions: 272, SettledPartitions: 272, RetryExhaustedPartitions: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := exactSemanticScheduleReady(test.snapshot); got != test.want {
				t.Fatalf("exactSemanticScheduleReady() = %t, want %t", got, test.want)
			}
		})
	}
}

// semanticColdTimingFit is the source-free record the exact-semantic cold
// diagnostic logs on both convergence and deadline. Other than the exact
// source commit binding, every field is a count, enum state, or millisecond
// duration; it names no path, repository, or raw error.
type semanticColdTimingFit struct {
	Schema                         string                         `json:"schema"`
	SourceCommit                   string                         `json:"source_commit"`
	Profile                        string                         `json:"profile"`
	Diagnostic                     string                         `json:"diagnostic"`
	Converged                      bool                           `json:"converged"`
	FreezeReady                    bool                           `json:"freeze_ready"`
	TerminalClass                  string                         `json:"terminal_class,omitempty"`
	ColdDeadlineMS                 int64                          `json:"cold_deadline_ms"`
	WallMS                         int64                          `json:"wall_ms"`
	CaptureWallMS                  int64                          `json:"capture_wall_ms"`
	ColdHeadroomMS                 int64                          `json:"cold_headroom_ms"`
	LastStage                      string                         `json:"last_stage"`
	LastErrorClass                 string                         `json:"last_error_class,omitempty"`
	FinalCaptureState              string                         `json:"final_capture_state"`
	TimingCaptureOK                bool                           `json:"timing_capture_ok"`
	TimingSemantics                string                         `json:"timing_semantics"`
	TimingErrorClass               string                         `json:"timing_error_class,omitempty"`
	StageEntryMS                   map[string]int64               `json:"stage_entry_ms"`
	ExtractionEntryMS              int64                          `json:"extraction_entry_ms"`
	RelationshipEntryMS            int64                          `json:"relationship_entry_ms"`
	ExpectedRecords                int                            `json:"expected_records"`
	ObservationRecords             uint64                         `json:"observation_records"`
	ExpectedExtractionPartitions   int                            `json:"expected_extraction_partitions"`
	ApplicableExtractionPartitions int                            `json:"applicable_extraction_partitions"`
	SettledExtractionPartitions    int                            `json:"settled_extraction_partitions"`
	SelectedV2PublicationCompleted bool                           `json:"selected_v2_publication_completed"`
	LastExtractionWallMS           int64                          `json:"last_extraction_wall_ms"`
	LastExtraction                 *ExtractionProgressObservation `json:"last_extraction,omitempty"`
	ExtractionTiming               ExtractionTimingObservation    `json:"extraction_timing"`
	PeakRSSBytes                   int64                          `json:"peak_rss_bytes"`
	LogicalBytes                   int64                          `json:"logical_bytes"`
	AllocatedBytes                 int64                          `json:"allocated_bytes"`
	RSSCeilingBytes                int64                          `json:"rss_ceiling_bytes"`
	AllocationCeilingBytes         int64                          `json:"allocation_ceiling_bytes"`
}

// semanticColdTail is the source-free tail evidence the plain timeout path
// discarded: per-stage first-entry walls, the last decoded extraction schedule
// counters, and the v13 partition/scheduler timing aggregate. It never retains
// a path, repository name, digest, or raw error string.
type semanticColdTail struct {
	StageEntryMS       map[string]int64
	LastExtraction     *ExtractionProgressObservation
	LastExtractionWall int64
	Timing             ExtractionTimingObservation
	LastStage          string
	LastErrorClass     string
	FinalCaptureState  string
	TimingCaptureOK    bool
	TimingErrorClass   string
}

type semanticColdResult struct {
	snapshot   privateProfileSnapshot
	converged  bool
	terminal   string
	coldWallMS int64
	tail       semanticColdTail
}

func semanticColdTerminalClass(err error) string {
	switch {
	case errors.Is(err, errRepositoryIndexTerminal):
		return "repository_index_terminal"
	case errors.Is(err, errObservationBoundRefusal):
		return "observation_bound_refusal"
	case errors.Is(err, errObservationTerminal):
		return "observation_terminal"
	case errors.Is(err, errExtractionBoundRefusal):
		return "extraction_bound_refusal"
	case errors.Is(err, errExtractionJobTerminal):
		return "extraction_job_terminal"
	case errors.Is(err, errExtractionScheduleTerminal):
		return "extraction_schedule_terminal"
	default:
		return ""
	}
}

// captureSemanticColdConvergence polls the ordinary convergence ladder like
// awaitReadinessSnapshot, but additionally retains the tail evidence the plain
// timeout path drops: per-stage first-entry walls, the last decoded extraction
// schedule counters (from the ordinary probe, already source-free), and the
// v13 partition/scheduler timing aggregate drained from the server log. On the
// convergence deadline it takes one final bounded capture under a fresh short
// context — the main wait context is already expired, which is exactly why the
// last ordinary probe is severed at its first request and mislabels its stage —
// before returning. It never calls Fatalf: the caller seals the record and
// decides pass/fail.
func captureSemanticColdConvergence(
	t *testing.T,
	ctx context.Context,
	profile PreparedProfile,
	server *privateServer,
	revision string,
	limit time.Duration,
) semanticColdResult {
	t.Helper()
	inspector, err := newProfileInspector(profile, true)
	if err != nil {
		t.Fatal(err)
	}
	tail := semanticColdTail{
		StageEntryMS:      map[string]int64{},
		FinalCaptureState: "not_needed",
		TimingCaptureOK:   true,
	}

	// Diagnostics{Extraction:true} in the timing config emits per-partition and
	// chunk-lifecycle timing to the server log; drain both as the run advances.
	partitionCursor, partitionErr := newPartitionTimingCursor(server.logPath)
	if partitionErr != nil {
		tail.TimingCaptureOK = false
		tail.TimingErrorClass = "partition_timing_cursor_initialization_failed"
	} else {
		defer func() { _ = partitionCursor.Close() }()
	}
	lifecycleCursor, lifecycleErr := newChunkLifecycleCursor(server.logPath, 0)
	if lifecycleErr != nil {
		tail.TimingCaptureOK = false
		if tail.TimingErrorClass == "" {
			tail.TimingErrorClass = "scheduler_timing_cursor_initialization_failed"
		}
	} else {
		defer func() { _ = lifecycleCursor.Close() }()
	}
	started := time.Now()
	if !tail.TimingCaptureOK {
		tail.LastErrorClass = tail.TimingErrorClass
		return semanticColdResult{coldWallMS: time.Since(started).Milliseconds(), tail: tail}
	}
	drain := func() bool {
		if partitionErr == nil {
			if reports, pollErr := partitionCursor.poll(); pollErr != nil {
				tail.TimingCaptureOK = false
				if tail.TimingErrorClass == "" {
					tail.TimingErrorClass = "partition_timing_poll_failed"
				}
				partitionErr = pollErr
			} else {
				for _, report := range reports {
					addPartitionTiming(&tail.Timing, report)
				}
			}
		}
		if lifecycleErr == nil {
			if reports, pollErr := lifecycleCursor.poll(); pollErr != nil {
				tail.TimingCaptureOK = false
				if tail.TimingErrorClass == "" {
					tail.TimingErrorClass = "scheduler_timing_poll_failed"
				}
				lifecycleErr = pollErr
			} else {
				for _, report := range reports {
					addSchedulerTiming(&tail.Timing, report)
				}
			}
		}
		return tail.TimingCaptureOK
	}

	observe := func(probe privateConvergenceProbe) {
		if probe.Stage != "" {
			if _, seen := tail.StageEntryMS[probe.Stage]; !seen {
				tail.StageEntryMS[probe.Stage] = time.Since(started).Milliseconds()
			}
			tail.LastStage = probe.Stage
		}
		if probe.ExtractionProgress != nil {
			progress := *probe.ExtractionProgress
			tail.LastExtraction = &progress
			tail.LastExtractionWall = time.Since(started).Milliseconds()
		}
	}

	wait, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, probe, inspectErr := inspector.inspectWithProgress(wait, profile, revision)
		observe(probe)
		if !drain() {
			tail.LastErrorClass = tail.TimingErrorClass
			return semanticColdResult{coldWallMS: time.Since(started).Milliseconds(), tail: tail}
		}
		if inspectErr == nil {
			return semanticColdResult{
				snapshot: snapshot, converged: true,
				coldWallMS: time.Since(started).Milliseconds(), tail: tail,
			}
		}
		if terminal := semanticColdTerminalClass(inspectErr); terminal != "" {
			tail.LastErrorClass = terminal
			return semanticColdResult{
				terminal: terminal, coldWallMS: time.Since(started).Milliseconds(), tail: tail,
			}
		}
		select {
		case <-wait.Done():
			coldWallMS := time.Since(started).Milliseconds()
			tail.LastErrorClass = "http_request_failed_after_context_deadline"
			// The main window closed. Take one final bounded capture under a
			// fresh short deadline before teardown: refresh the extraction
			// counters and flush any timing lines written since the last poll.
			final, finalCancel := context.WithTimeout(ctx, 30*time.Second)
			finalSnapshot, finalProbe, finalErr := inspector.inspectWithProgress(final, profile, revision)
			observe(finalProbe)
			if finalErr == nil {
				tail.FinalCaptureState = "converged_after_deadline"
				if !drain() {
					tail.LastErrorClass = tail.TimingErrorClass
				}
				finalCancel()
				return semanticColdResult{snapshot: finalSnapshot, coldWallMS: coldWallMS, tail: tail}
			}
			finalTerminal := semanticColdTerminalClass(finalErr)
			if finalTerminal != "" {
				tail.FinalCaptureState = finalTerminal
			}
			if finalProbe.ExtractionProgress != nil {
				if finalTerminal == "" {
					tail.FinalCaptureState = "extraction_progress_captured"
				}
			} else if finalTerminal == "" {
				tail.FinalCaptureState = "inspection_incomplete"
			}
			if !drain() {
				tail.LastErrorClass = tail.TimingErrorClass
			}
			finalCancel()
			return semanticColdResult{terminal: finalTerminal, coldWallMS: coldWallMS, tail: tail}
		case <-ticker.C:
		}
	}
}

// TestProductionPathReadinessRehearsal is an opt-in, bounded rehearsal of the
// production paths and recovery boundaries that failed Takes 11-18. It
// deliberately builds the current working tree rather than HEAD so a correction
// can cross this bar before its readiness commit. The frozen ceremony still
// builds only its committed source.
func TestProductionPathReadinessRehearsal(t *testing.T) {
	if os.Getenv(readinessRehearsalEnvironment) != "1" {
		t.Skip("set " + readinessRehearsalEnvironment + "=1 to run the real-binary readiness rehearsal")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot, err = filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	toolchain, err := buildWorkingTreeToolchain(ctx, moduleRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"semantic", "structural"} {
		t.Run(kind, func(t *testing.T) {
			rehearseProductionPath(t, ctx, moduleRoot, workspace, toolchain, kind)
		})
	}
}

func buildWorkingTreeToolchain(
	ctx context.Context,
	moduleRoot string,
	workspace string,
) (privateToolchain, error) {
	output := filepath.Join(workspace, "working-tree-toolchain")
	if err := os.Mkdir(output, 0o700); err != nil {
		return privateToolchain{}, err
	}
	toolchain := privateToolchain{
		Schema:  privateToolchainSchema,
		Phebs:   filepath.Join(output, "phebs"),
		Zoekt:   filepath.Join(output, "zoekt-git-index"),
		Focused: filepath.Join(output, "phebs-focused-index"),
		Buf:     filepath.Join(output, "buf"),
	}
	builds := []struct {
		output string
		path   string
		env    []string
	}{
		{toolchain.Phebs, "./cmd/phebs", nil},
		{toolchain.Zoekt, "github.com/sourcegraph/zoekt/cmd/zoekt-git-index", nil},
		{toolchain.Focused, "./cmd/phebs-focused-index", nil},
		{toolchain.Buf, "github.com/bufbuild/buf/cmd/buf", []string{"CGO_ENABLED=0"}},
	}
	for _, build := range builds {
		command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", build.output, build.path)
		command.Dir = moduleRoot
		command.Env = append(scrubExecutionEnvironment(), build.env...)
		if output, err := command.CombinedOutput(); err != nil {
			return privateToolchain{}, fmt.Errorf("build readiness tool %s: %w: %s", build.path, err, output)
		}
	}
	if err := validateToolchain(toolchain); err != nil {
		return privateToolchain{}, err
	}
	return toolchain, nil
}

func rehearseProductionPath(
	t *testing.T,
	ctx context.Context,
	moduleRoot string,
	workspace string,
	toolchain privateToolchain,
	kind string,
) {
	t.Helper()
	profile, err := prepareProjectionProfile(ctx, moduleRoot, workspace, kind)
	if err != nil {
		t.Fatal(err)
	}
	server, err := launchPrivateServer(ctx, profile, toolchain, "rehearsal-cold")
	if err != nil {
		t.Fatal(err)
	}
	running := true
	defer func() {
		if running {
			_ = server.stop(30 * time.Second)
		}
	}()
	if _, err := awaitPrivateServerHealth(ctx, server, profile, "rehearsal-cold", 2*time.Minute); err != nil {
		t.Logf("source-free server tail:\n%s", rehearsalLogTail(server.logPath))
		t.Fatal(err)
	}
	if kind == "structural" {
		measurement := measureProjectionExtraction(t, ctx, profile)
		t.Logf(
			"structural extraction after observation current: duration=%s max_completion_gap=%s completion_gaps=%v completion_deltas=%v partitions=%d domains=%d",
			measurement.Duration, measurement.MaxCompletionGap,
			measurement.CompletionGaps, measurement.CompletionDeltas,
			measurement.Progress.Total, measurement.Progress.Domains,
		)
		if os.Getenv("PHEBS_T4013_THROUGHPUT_ONLY") == "1" {
			if err := server.stop(30 * time.Second); err != nil {
				t.Fatal(err)
			}
			running = false
			return
		}
	}
	a := awaitReadinessSnapshot(t, ctx, profile, "a", 12*time.Minute)
	t.Log("cold revision A converged")
	verifyPartitionTimingDiagnostics(t, server.logPath)
	if _, err := waitLifecycle(ctx, profile, true, 3*time.Minute); err != nil {
		t.Fatal(err)
	}
	serviceKey, requireCitation := "service-000", false
	if kind == "semantic" {
		serviceKey, requireCitation = "semantic", true
	}
	if _, exact, err := queryProfile(ctx, profile, serviceKey, requireCitation); err != nil || !exact {
		t.Fatalf("cold authorized query exact=%t: %v", exact, err)
	}
	t.Log("cold lifecycle and authorized query passed")

	if kind == "structural" {
		if err := updateSourceRevision(ctx, profile.Repository, profile.Revisions["b"]); err != nil {
			t.Fatal(err)
		}
		b := awaitReadinessSnapshot(t, ctx, profile, "b", 12*time.Minute)
		t.Log("structural revision B converged")
		if snapshotAuthority(a) == snapshotAuthority(b) || changedSourceMembers(a, b) <= 0 {
			t.Fatal("structural B did not change exact source and derived authority")
		}
		if err := updateSourceRevision(ctx, profile.Repository, profile.Revisions["a-return"]); err != nil {
			t.Fatal(err)
		}
		a = awaitReadinessSnapshot(t, ctx, profile, "a-return", 12*time.Minute)
		t.Log("structural revision A-return converged")
	}

	backup, _, err := createLiveBackup(ctx, toolchain, profile, workspace, "rehearsal-"+kind)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.stop(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	running = false
	if _, err := restoreBackup(ctx, toolchain, profile, workspace, backup, "rehearsal-"+kind); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestoredBoundary(ctx, profile, a); err != nil {
		t.Fatal(err)
	}
	t.Log("live backup and offline restore boundary passed")

	server, err = launchPrivateServer(ctx, profile, toolchain, "rehearsal-restored")
	if err != nil {
		t.Fatal(err)
	}
	running = true
	if _, err := awaitPrivateServerHealth(ctx, server, profile, "rehearsal-restored", 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	revision := "a"
	if kind == "structural" {
		revision = "a-return"
	}
	restored := awaitReadinessSnapshot(t, ctx, profile, revision, 12*time.Minute)
	if snapshotAuthority(restored) != snapshotAuthority(a) {
		t.Fatal("live backup and offline restore changed exact authority")
	}
	if _, err := waitLifecycle(ctx, profile, false, 3*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, exact, err := queryProfile(ctx, profile, serviceKey, requireCitation); err != nil || !exact {
		t.Fatalf("restored authorized query exact=%t: %v", exact, err)
	}
	t.Log("restored convergence, lifecycle, and authorized query passed")
	if err := server.stop(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	running = false
}

func verifyPartitionTimingDiagnostics(t *testing.T, logPath string) {
	t.Helper()
	timing, err := newPartitionTimingCursor(logPath)
	if err != nil {
		t.Fatal(err)
	}
	reports, err := timing.poll()
	_ = timing.Close()
	if err != nil || len(reports) == 0 {
		t.Fatalf("partition timing reports = %d, error=%v", len(reports), err)
	}
	lifecycle, err := newChunkLifecycleCursor(logPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	lifecycleReports, err := lifecycle.poll()
	_ = lifecycle.Close()
	if err != nil {
		t.Fatal(err)
	}
	settledWithTiming := 0
	for _, report := range lifecycleReports {
		if report.Event == "settled" && report.DurationMS > 0 {
			settledWithTiming++
		}
	}
	if settledWithTiming == 0 {
		t.Fatal("partition scheduler settlement timing is absent")
	}
}

func rehearsalLogTail(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	if len(raw) > 8<<10 {
		raw = raw[len(raw)-(8<<10):]
	}
	lines := strings.Split(string(raw), "\n")
	kept := lines[:0]
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "token") || strings.Contains(lower, "credential") ||
			strings.Contains(lower, "api key") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

type extractionThroughputMeasurement struct {
	Duration         time.Duration
	MaxCompletionGap time.Duration
	CompletionGaps   []time.Duration
	CompletionDeltas []int
	Progress         extractionpublication.Progress
}

func measureProjectionExtraction(
	t *testing.T,
	ctx context.Context,
	profile PreparedProfile,
) extractionThroughputMeasurement {
	t.Helper()
	inspector, err := newProfileInspector(profile, true)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(12 * time.Minute)
	repository := url.QueryEscape(profile.RepositoryName)
	var observationCurrentAt time.Time
	var lastCompletionAt time.Time
	lastSucceeded := 0
	maxCompletionGap := time.Duration(0)
	var completionGaps []time.Duration
	var completionDeltas []int
	for time.Now().Before(deadline) {
		var observation observationpublication.Progress
		if err := inspector.get(
			ctx, profile, "/api/observation-progress?repository="+repository, &observation,
		); err == nil {
			if observation.SchemaVersion != observationpublication.ProgressSchemaV2 ||
				observation.SelectedVersion != "v2" {
				t.Fatalf("readiness observation route = %q/%q", observation.SchemaVersion, observation.SelectedVersion)
			}
			if terminal := observationTerminal(observation); terminal != nil {
				t.Fatalf("readiness observation planning terminated: %v", terminal)
			}
			if observation.State == "current" && observationCurrentAt.IsZero() {
				observationCurrentAt = time.Now()
			}
		}
		if !observationCurrentAt.IsZero() {
			var extraction extractionpublication.Progress
			if err := inspector.get(
				ctx, profile, "/api/extraction-progress?repository="+repository, &extraction,
			); err == nil {
				now := time.Now()
				if lastCompletionAt.IsZero() {
					lastCompletionAt = observationCurrentAt
				}
				if extraction.Succeeded > lastSucceeded {
					gap := now.Sub(lastCompletionAt)
					maxCompletionGap = max(maxCompletionGap, gap)
					completionGaps = append(completionGaps, gap)
					completionDeltas = append(completionDeltas, extraction.Succeeded-lastSucceeded)
					lastCompletionAt = now
					lastSucceeded = extraction.Succeeded
				}
				if extraction.State == "current" {
					return extractionThroughputMeasurement{
						Duration:         time.Since(observationCurrentAt),
						MaxCompletionGap: maxCompletionGap,
						CompletionGaps:   completionGaps, CompletionDeltas: completionDeltas,
						Progress: extraction,
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("structural extraction measurement deadline expired")
	return extractionThroughputMeasurement{}
}

func prepareProjectionProfile(
	ctx context.Context,
	moduleRoot string,
	workspace string,
	kind string,
) (PreparedProfile, error) {
	profile, err := t401.ProjectionProfile(kind)
	if kind == "structural" {
		profile, err = t401.StructuralPartitionProfile()
	}
	if err != nil {
		return PreparedProfile{}, err
	}
	profileRoot := filepath.Join(workspace, kind)
	if err := os.Mkdir(profileRoot, 0o700); err != nil {
		return PreparedProfile{}, err
	}
	authored := filepath.Join(profileRoot, "authored")
	receipt, err := t401.Author(ctx, t401.AuthorRequest{
		ModuleRoot: moduleRoot,
		Output:     authored,
		Profile:    profile,
	})
	if err != nil {
		return PreparedProfile{}, err
	}
	revisions := make(map[string]string, len(receipt.Revisions))
	for _, revision := range receipt.Revisions {
		revisions[revision.Revision] = revision.Commit
	}
	repository := filepath.Join(authored, "repository.git")
	if err := updateSourceRevision(ctx, repository, revisions["a"]); err != nil {
		return PreparedProfile{}, err
	}
	repositoryName, err := phebssync.RepoName(repository)
	if err != nil {
		return PreparedProfile{}, err
	}
	catalogPath := filepath.Join(profileRoot, "service-catalog.json")
	catalog, err := catalogForShape(kind, profile.Shape.Cells)
	if err != nil {
		return PreparedProfile{}, err
	}
	if err := writePrivateNew(catalogPath, catalog); err != nil {
		return PreparedProfile{}, err
	}
	credential, err := randomCredential()
	if err != nil {
		return PreparedProfile{}, err
	}
	credentialPath := filepath.Join(profileRoot, "api-key")
	if err := writePrivateNew(credentialPath, []byte(credential+"\n")); err != nil {
		return PreparedProfile{}, err
	}
	dataDir := filepath.Join(profileRoot, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		return PreparedProfile{}, err
	}
	address, err := reserveLoopbackAddress()
	if err != nil {
		return PreparedProfile{}, err
	}
	configPath := filepath.Join(profileRoot, "phebs.yaml")
	config, err := configFor(repository, repositoryName, catalogPath, dataDir, address, credential)
	if err != nil {
		return PreparedProfile{}, err
	}
	if err := writePrivateNew(configPath, config); err != nil {
		return PreparedProfile{}, err
	}
	return PreparedProfile{
		Name:           profile.Name,
		Repository:     repository,
		RepositoryName: repositoryName,
		Config:         configPath,
		Credential:     credentialPath,
		DataDir:        dataDir,
		Address:        address,
		Catalog:        catalogPath,
		Revisions:      revisions,
	}, nil
}

func prepareTimingProfile(
	ctx context.Context,
	moduleRoot string,
	workspace string,
	profile t401.Profile,
) (PreparedProfile, error) {
	profileRoot := filepath.Join(workspace, "exact-semantic")
	if err := os.Mkdir(profileRoot, 0o700); err != nil {
		return PreparedProfile{}, err
	}
	authored := filepath.Join(profileRoot, "authored")
	receipt, err := t401.Author(ctx, t401.AuthorRequest{
		ModuleRoot: moduleRoot, Output: authored, Profile: profile, ConfirmFrozen: true,
	})
	if err != nil {
		return PreparedProfile{}, err
	}
	revisions := make(map[string]string, len(receipt.Revisions))
	for _, revision := range receipt.Revisions {
		revisions[revision.Revision] = revision.Commit
	}
	repository := filepath.Join(authored, "repository.git")
	if err := updateSourceRevision(ctx, repository, revisions["a"]); err != nil {
		return PreparedProfile{}, err
	}
	repositoryName, err := phebssync.RepoName(repository)
	if err != nil {
		return PreparedProfile{}, err
	}
	catalogPath := filepath.Join(profileRoot, "service-catalog.json")
	catalog, err := catalogForShape(profile.Kind, profile.Shape.Cells)
	if err != nil {
		return PreparedProfile{}, err
	}
	if err := writePrivateNew(catalogPath, catalog); err != nil {
		return PreparedProfile{}, err
	}
	credential, err := randomCredential()
	if err != nil {
		return PreparedProfile{}, err
	}
	credentialPath := filepath.Join(profileRoot, "api-key")
	if err := writePrivateNew(credentialPath, []byte(credential+"\n")); err != nil {
		return PreparedProfile{}, err
	}
	dataDir := filepath.Join(profileRoot, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		return PreparedProfile{}, err
	}
	address, err := reserveLoopbackAddress()
	if err != nil {
		return PreparedProfile{}, err
	}
	configPath := filepath.Join(profileRoot, "phebs.yaml")
	config, err := configFor(repository, repositoryName, catalogPath, dataDir, address, credential)
	if err != nil {
		return PreparedProfile{}, err
	}
	if err := writePrivateNew(configPath, config); err != nil {
		return PreparedProfile{}, err
	}
	return PreparedProfile{
		Name: profile.Name, Repository: repository, RepositoryName: repositoryName,
		Config: configPath, Credential: credentialPath, DataDir: dataDir, Address: address,
		Catalog: catalogPath, Revisions: revisions,
	}, nil
}

func reserveLoopbackAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", err
	}
	return address, nil
}

func awaitReadinessSnapshot(
	t *testing.T,
	ctx context.Context,
	profile PreparedProfile,
	revision string,
	limit time.Duration,
) privateProfileSnapshot {
	t.Helper()
	inspector, err := newProfileInspector(profile, true)
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := convergenceProbe("not_started")
	var lastErr error
	lastDiagnostic := ""
	for {
		snapshot, probe, inspectErr := inspector.inspectWithProgress(wait, profile, revision)
		last = probe
		if inspectErr == nil {
			return snapshot
		}
		lastErr = inspectErr
		diagnostic := probe.Stage + ": " + inspectErr.Error()
		if diagnostic != lastDiagnostic {
			t.Logf("readiness pending at %s: %v", probe.Stage, inspectErr)
			lastDiagnostic = diagnostic
		}
		if errors.Is(inspectErr, errRepositoryIndexTerminal) ||
			errors.Is(inspectErr, errObservationBoundRefusal) ||
			errors.Is(inspectErr, errObservationTerminal) ||
			errors.Is(inspectErr, errExtractionBoundRefusal) ||
			errors.Is(inspectErr, errExtractionJobTerminal) ||
			errors.Is(inspectErr, errExtractionScheduleTerminal) {
			t.Fatalf(
				"production path terminated at %s (repository_index_class=%s): %v",
				probe.Stage, probe.RepositoryIndexFailureClass, inspectErr,
			)
		}
		select {
		case <-wait.Done():
			t.Fatalf(
				"readiness convergence expired at %s: deadline=%v last_error=%v",
				last.Stage, wait.Err(), lastErr,
			)
		case <-ticker.C:
		}
	}
}
