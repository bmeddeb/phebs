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
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/spike/t401"
)

const readinessRehearsalEnvironment = "PHEBS_T4013_READINESS_REHEARSAL"
const exactSemanticTimingEnvironment = "PHEBS_T4013_EXACT_SEMANTIC_TIMING"

func retainFailedDiagnosticWorkspace(t *testing.T, workspace string) {
	t.Helper()
	if t.Failed() {
		t.Logf("real-binary diagnostic custody retained at %s", workspace)
		return
	}
	if err := os.RemoveAll(workspace); err != nil {
		t.Errorf("remove successful real-binary diagnostic custody: %v", err)
	}
}

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
	sourceCommit, err := gitOutputForContract(ctx, moduleRoot, true, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyCleanCheckoutWithGit(ctx, moduleRoot, sourceCommit, true); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp("", "phebs-t4013-semantic-timing-")
	if err != nil {
		t.Fatal(err)
	}
	defer retainFailedDiagnosticWorkspace(t, workspace)
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
			if err := server.stop(30 * time.Second); err != nil {
				t.Errorf("stop exact semantic diagnostic; retained at %s: %v", workspace, err)
			}
		}
	}()
	if _, err := awaitPrivateServerHealth(ctx, server, profile, "exact-semantic-cold", 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result := captureSemanticColdConvergence(t, ctx, profile, server, "a", 4*time.Hour)
	captureWall := time.Since(started)
	coldWall := time.Duration(result.coldWallMS) * time.Millisecond
	peakRSS, _, _, _, samplerErr := server.sampler.metrics()
	if samplerErr != nil {
		t.Fatal(samplerErr)
	}
	logical, allocated, err := measureDataBytesForContract(workspace, true)
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
	inspector, err := newProfileInspector(profile, profileInspectionV16)
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
	lifecycleCursor, lifecycleErr := newChunkLifecycleCursor(server.logPath, 0, chunkLifecycleValidationV17)
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
					if timingErr := addPartitionTiming(&tail.Timing, report); timingErr != nil {
						tail.TimingCaptureOK = false
						if tail.TimingErrorClass == "" {
							tail.TimingErrorClass = "partition_timing_aggregation_failed"
						}
						partitionErr = timingErr
						break
					}
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
					if timingErr := addSchedulerTiming(&tail.Timing, report); timingErr != nil {
						tail.TimingCaptureOK = false
						if tail.TimingErrorClass == "" {
							tail.TimingErrorClass = "scheduler_timing_aggregation_failed"
						}
						lifecycleErr = timingErr
						break
					}
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
	workspace, err := os.MkdirTemp("", "phebs-t4013-readiness-")
	if err != nil {
		t.Fatal(err)
	}
	defer retainFailedDiagnosticWorkspace(t, workspace)
	toolchain, err := buildWorkingTreeToolchain(ctx, moduleRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"semantic", "structural"} {
		t.Run(kind, func(t *testing.T) {
			rehearseProductionPath(t, ctx, moduleRoot, workspace, toolchain, kind)
		})
	}
	t.Run("semantic-stale-worker", func(t *testing.T) {
		rehearseSemanticStaleWorkerBoundary(t, ctx, moduleRoot, workspace, toolchain)
	})
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
		Schema:            privateToolchainSchema,
		Phebs:             filepath.Join(output, "phebs"),
		Zoekt:             filepath.Join(output, "zoekt-git-index"),
		Focused:           filepath.Join(output, "phebs-focused-index"),
		Buf:               filepath.Join(output, "buf"),
		ClosedEnvironment: true,
	}
	hostToolchain, err := observeHostToolchain(ctx, true)
	if err != nil {
		return privateToolchain{}, err
	}
	toolchain.host, err = bindHostToolchainForPlan(ctx, Plan{
		Schema:        PlanSchemaV26,
		HostToolchain: hostToolchain,
	})
	if err != nil {
		return privateToolchain{}, err
	}
	controlPath := filepath.Join(workspace, executionControlsFilename)
	if raw, readErr := os.ReadFile(controlPath); readErr == nil {
		toolchain.controlsDigest = digest(raw)
		toolchain.controls, err = openExecutionControls(
			workspace, toolchain.controlsDigest, toolchain.host, true,
		)
	} else if errors.Is(readErr, os.ErrNotExist) {
		toolchain.controls, toolchain.controlsDigest, err = createExecutionControls(workspace, toolchain.host)
	} else {
		err = readErr
	}
	if err != nil {
		return privateToolchain{}, err
	}
	toolchain.TempDir = toolchain.controls.Temp
	for _, path := range []string{toolchain.controls.ModuleCache, toolchain.controls.BuildCache} {
		if err := os.Mkdir(path, 0o700); err != nil {
			return privateToolchain{}, err
		}
	}
	goPath, err := toolchain.host.goDriver.pathForLaunch(ctx)
	if err != nil {
		return privateToolchain{}, err
	}
	hydrate := exec.CommandContext(ctx, goPath, "list", "-deps",
		"./cmd/phebs", "github.com/sourcegraph/zoekt/cmd/zoekt-git-index",
		"./cmd/phebs-focused-index", "github.com/bufbuild/buf/cmd/buf")
	hydrate.Dir = moduleRoot
	hydrate.Env = executionEnvironmentForControls(toolchain.controls, true)
	if output, err := runCustodyCombinedOutput(hydrate); err != nil {
		return privateToolchain{}, fmt.Errorf("hydrate readiness modules: %w: %s", err, output)
	}
	goPath, err = toolchain.host.goDriver.pathForLaunch(ctx)
	if err != nil {
		return privateToolchain{}, err
	}
	verify := exec.CommandContext(ctx, goPath, "mod", "verify")
	verify.Dir = moduleRoot
	verify.Env = executionEnvironmentForControls(toolchain.controls, true)
	if output, err := runCustodyCombinedOutput(verify); err != nil {
		return privateToolchain{}, fmt.Errorf("verify readiness module cache: %w: %s", err, output)
	}
	moduleDigest, err := privateCacheDigest(ctx, toolchain.controls.ModuleCache)
	if err != nil {
		return privateToolchain{}, err
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
		goPath, err := toolchain.host.goDriver.pathForLaunch(ctx)
		if err != nil {
			return privateToolchain{}, err
		}
		command := exec.CommandContext(ctx, goPath, "build", "-trimpath", "-o", build.output, build.path)
		command.Dir = moduleRoot
		command.Env = executionEnvironmentForControls(toolchain.controls, false)
		command.Env = append(command.Env, build.env...)
		if output, err := runCustodyCombinedOutput(command); err != nil {
			return privateToolchain{}, fmt.Errorf("build readiness tool %s: %w: %s", build.path, err, output)
		}
	}
	after, err := privateCacheDigest(ctx, toolchain.controls.ModuleCache)
	if err != nil || after != moduleDigest {
		return privateToolchain{}, errors.Join(err, errors.New("readiness module cache changed during build"))
	}
	for _, path := range []string{toolchain.controls.ModuleCache, toolchain.controls.BuildCache} {
		if err := removePrivateGoCache(path); err != nil {
			return privateToolchain{}, err
		}
	}
	if err := validateToolchain(toolchain); err != nil {
		return privateToolchain{}, err
	}
	if _, err := bindPrivateToolchain(ctx, &toolchain); err != nil {
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
			if err := server.stop(30 * time.Second); err != nil {
				t.Errorf("stop readiness diagnostic; retained at %s: %v", workspace, err)
			}
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
	if kind == "semantic" {
		server, a = rehearseSemanticInterruptionBoundary(t, ctx, profile, toolchain, server, a)
		t.Log("semantic interruption boundary passed")
	}

	if kind == "structural" {
		if err := updateSourceRevision(ctx, profile.Repository, profile.Revisions["b"], true); err != nil {
			t.Fatal(err)
		}
		b := awaitReadinessSnapshot(t, ctx, profile, "b", 12*time.Minute)
		t.Log("structural revision B converged")
		if snapshotAuthority(a) == snapshotAuthority(b) || changedSourceMembers(a, b) <= 0 {
			t.Fatal("structural B did not change exact source and derived authority")
		}
		if err := updateSourceRevision(ctx, profile.Repository, profile.Revisions["a-return"], true); err != nil {
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
	if err := verifyRestoredBoundary(ctx, profile, a, true); err != nil {
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
	if !privateRestoreProductEqual(restored, a) {
		t.Fatal("live backup and offline restore changed recovered semantic authority")
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

func rehearseSemanticInterruptionBoundary(
	t *testing.T,
	ctx context.Context,
	profile PreparedProfile,
	toolchain privateToolchain,
	server *privateServer,
	a privateProfileSnapshot,
) (*privateServer, privateProfileSnapshot) {
	t.Helper()
	beforeRelationship, err := relationshippublication.OpenCurrent(
		ctx, filepath.Join(profile.DataDir, "relationships"), profile.RepositoryName,
	)
	if err != nil {
		t.Fatal(err)
	}
	beforeRelationshipAuthority := beforeRelationship.Root().Authority

	// The observer is fully armed before the source update; after selection the
	// graceful stop may settle or release the lease, so restart proves its exact
	// non-running fate and unchanged A authority.
	meter, err := beginPhaseMeter(server, profile.DataDir, &a)
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{Schema: PlanSchemaV26}
	run := &execution{
		ctx: ctx, plan: plan,
		observation: emptyObservationForPlan(EnvironmentObservation{}, plan),
	}
	observer, err := run.newInterruptionTriggerV18Observer(
		server, meter, profile, profile.Revisions["b"],
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateSourceRevision(ctx, profile.Repository, profile.Revisions["b"], true); err != nil {
		t.Fatal(errors.Join(err, observer.Close()))
	}
	if err := waitExactActiveGenerationSchedule(
		ctx, observer.progressReader, profile, profile.Revisions["b"], 3*time.Minute,
	); err != nil {
		t.Fatal(errors.Join(err, observer.Close()))
	}
	releaseFence, err := focusedindex.AcquireExclusiveMutationLock(
		ctx, filepath.Join(profile.DataDir, "index"),
	)
	if err != nil {
		t.Fatal(errors.Join(err, observer.Close()))
	}
	fenceHeld := true
	defer func() {
		if fenceHeld {
			releaseFence()
		}
	}()
	trigger, err := observer.Wait(profile.Revisions["b"], 3*time.Minute)
	if err != nil {
		t.Fatal(errors.Join(err, observer.Close()))
	}
	triggerScheduleDigest := observer.scheduleDigest
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := meter.finish(nil); err != nil {
		t.Fatal(err)
	}
	if err := server.stop(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	releaseFence()
	fenceHeld = false
	if err := updateSourceRevision(ctx, profile.Repository, profile.Revisions["a"], true); err != nil {
		t.Fatal(err)
	}
	restarted, err := launchPrivateServer(ctx, profile, toolchain, "rehearsal-semantic-interruption")
	if err != nil {
		t.Fatal(err)
	}
	handedOff := false
	defer func() {
		if !handedOff {
			if err := restarted.stop(30 * time.Second); err != nil {
				t.Errorf("stop interruption diagnostic; retained at %s: %v", filepath.Dir(profile.DataDir), err)
			}
		}
	}()
	if _, err := awaitPrivateServerHealth(
		ctx, restarted, profile, "rehearsal-semantic-interruption", 2*time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	recovered, err := waitInterruptionLeaseRecoveryV22(
		ctx, profile, trigger, triggerScheduleDigest,
		interruptionRecoveryContractForPlan(plan), 3*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readGenerationLifecycleObservationForPlan(
		ctx, plan, profile, 30*time.Second,
	); err != nil {
		t.Fatal(err)
	}
	diagnosticReader, err := store.OpenLocalGenerationChunkReader(ctx, profile.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	progress, progressErr := diagnosticReader.GenerationScheduleProgress(
		ctx, profile.RepositoryName, extractionpublication.ScheduleStage,
	)
	if closeErr := diagnosticReader.Close(context.WithoutCancel(ctx)); closeErr != nil {
		t.Fatal(errors.Join(progressErr, closeErr))
	}
	t.Logf("interruption trigger recovered=%s current extraction progress=%+v error=%v",
		recovered, progress, progressErr)
	afterRestart := awaitReadinessSnapshot(
		t, ctx, profile, "a", 12*time.Minute, restarted.logPath,
	)
	if snapshotRecoveryAuthority(afterRestart) != snapshotRecoveryAuthority(a) {
		afterRelationship, relationshipErr := relationshippublication.OpenCurrent(
			ctx, filepath.Join(profile.DataDir, "relationships"), profile.RepositoryName,
		)
		if relationshipErr == nil {
			t.Logf("semantic interruption relationship authority before=%+v after=%+v",
				beforeRelationshipAuthority, afterRelationship.Root().Authority)
		}
		t.Logf("semantic interruption A authority before=%q after=%q",
			snapshotAuthority(a), snapshotAuthority(afterRestart))
		t.Fatal("semantic interruption rehearsal changed exact A authority")
	}
	if err := waitForDerivedPartialClear(
		ctx, Plan{Schema: PlanSchemaV26}, profile.DataDir, 3*time.Minute,
	); err != nil {
		t.Logf("semantic interruption partial controls: %v", rehearsalPartialControls(profile.DataDir))
		t.Logf("semantic interruption derived inventory: %v", rehearsalDerivedInventory(profile.DataDir))
		t.Fatal(err)
	}
	handedOff = true
	return restarted, afterRestart
}

func rehearsalPartialControls(dataDir string) []string {
	controls := make([]string, 0, 8)
	for _, name := range []string{
		"observations", "extraction-publications", "relationships",
		"relationship-resolver-namespaces", "relationship-rpc-postings",
		"relationship-kafka-postings",
	} {
		root := filepath.Join(dataDir, name)
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || len(controls) >= 16 {
				return filepath.SkipAll
			}
			if entry.Name() == "publishing.json" || strings.HasPrefix(entry.Name(), ".stage-") {
				if relative, relativeErr := filepath.Rel(dataDir, path); relativeErr == nil {
					controls = append(controls, relative)
				}
			}
			return nil
		})
	}
	return controls
}

func rehearsalDerivedInventory(dataDir string) []string {
	result := make([]string, 0, 16)
	for _, name := range []string{
		"observations", "extraction-publications", "relationships",
		"relationship-resolver-namespaces", "relationship-rpc-postings",
		"relationship-kafka-postings",
	} {
		entries, err := os.ReadDir(filepath.Join(dataDir, name))
		if err != nil {
			result = append(result, name+": "+err.Error())
			continue
		}
		for _, entry := range entries {
			result = append(result, name+"/"+entry.Name())
		}
	}
	return result
}

func rehearseSemanticStaleWorkerBoundary(
	t *testing.T,
	ctx context.Context,
	moduleRoot string,
	workspace string,
	toolchain privateToolchain,
) {
	t.Helper()
	profile, err := prepareProjectionProfileNamed(
		ctx, moduleRoot, workspace, "semantic", "semantic-stale-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	server, err := launchPrivateServer(ctx, profile, toolchain, "rehearsal-stale-cold")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := server.stop(30 * time.Second); err != nil {
			t.Errorf("stop stale-worker diagnostic; retained at %s: %v", filepath.Dir(profile.DataDir), err)
		}
	}()
	if _, err := awaitPrivateServerHealth(
		ctx, server, profile, "rehearsal-stale-cold", 2*time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	a := awaitReadinessSnapshot(t, ctx, profile, "a", 12*time.Minute)
	meter, err := beginPhaseMeter(server, profile.DataDir, &a)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(
		server.logPath, meter.logOffset, chunkLifecycleValidationV23,
	)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := store.OpenLocalGenerationChunkReader(ctx, profile.DataDir)
	if err != nil {
		t.Fatal(errors.Join(err, cursor.Close()))
	}
	if err := updateSourceRevision(ctx, profile.Repository, profile.Revisions["b"], true); err != nil {
		t.Fatal(err)
	}
	if err := waitExactActiveGenerationSchedule(
		ctx, reader, profile, profile.Revisions["b"], 3*time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	releaseFence, err := focusedindex.AcquireExclusiveMutationLock(
		ctx, filepath.Join(profile.DataDir, "index"),
	)
	if err != nil {
		t.Fatal(err)
	}
	fenceHeld := true
	defer func() {
		if fenceHeld {
			releaseFence()
		}
	}()
	trigger, err := waitCurrentRunningGenerationChunk(
		ctx, cursor, reader, profile, profile.Revisions["b"],
		3*time.Minute, extractionGenerationBindsRevision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateSourceRevision(ctx, profile.Repository, profile.Revisions["a"], true); err != nil {
		t.Fatal(err)
	}
	trigger, selectedScheduleDigest, err := fenceRunningGenerationChunkForDiagnostic(
		ctx, cursor, reader, profile, profile.Revisions["b"], trigger, 3*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	releaseFence()
	fenceHeld = false
	after := awaitReadinessSnapshot(t, ctx, profile, "a", 12*time.Minute)
	if snapshotRecoveryAuthority(after) != snapshotRecoveryAuthority(a) {
		t.Fatal("semantic stale-worker rehearsal changed exact A authority")
	}
	settled, err := waitStaleChunkFence(
		ctx, cursor, reader, trigger, profile.RepositoryName,
		selectedScheduleDigest, 3*time.Minute,
	)
	if err != nil || settled.Outcome != "stale_fenced" {
		t.Fatalf("semantic stale-worker settlement = %+v, %v", settled, err)
	}
	if err := errors.Join(cursor.Close(), reader.Close(context.WithoutCancel(ctx))); err != nil {
		t.Fatal(err)
	}
	if _, err := meter.finish(&after); err != nil {
		t.Fatal(err)
	}
	t.Log("semantic stale-worker boundary passed")
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
	lifecycle, err := newChunkLifecycleCursor(logPath, 0, chunkLifecycleValidationV23)
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
	inspector, err := newProfileInspector(profile, profileInspectionV21)
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
	return prepareProjectionProfileNamed(ctx, moduleRoot, workspace, kind, kind)
}

func prepareProjectionProfileNamed(
	ctx context.Context,
	moduleRoot string,
	workspace string,
	kind string,
	name string,
) (PreparedProfile, error) {
	profile, err := t401.ProjectionProfile(kind)
	if kind == "structural" {
		profile, err = t401.StructuralPartitionProfile()
	}
	if err != nil {
		return PreparedProfile{}, err
	}
	profileRoot := filepath.Join(workspace, name)
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
	if err := updateSourceRevision(ctx, repository, revisions["a"], true); err != nil {
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
	if err := updateSourceRevision(ctx, repository, revisions["a"], true); err != nil {
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
	failureLog ...string,
) privateProfileSnapshot {
	t.Helper()
	inspector, err := newProfileInspector(profile, profileInspectionV21)
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
	lastRelationshipDiagnostic := time.Time{}
	lastExtractionDiagnostic := time.Time{}
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
		if probe.Stage == "relationship_publication" &&
			time.Since(lastRelationshipDiagnostic) >= 30*time.Second {
			progress, progressErr := readRelationshipScheduleProgress(wait, profile)
			t.Logf("relationship readiness progress=%+v error=%v", progress, progressErr)
			lastRelationshipDiagnostic = time.Now()
		}
		if probe.Stage == "extraction_publication" &&
			time.Since(lastExtractionDiagnostic) >= 30*time.Second {
			reader, openErr := store.OpenLocalGenerationChunkReader(wait, profile.DataDir)
			progress := store.GenerationScheduleProgress{}
			progressErr := openErr
			if openErr == nil {
				progress, progressErr = reader.GenerationScheduleProgress(
					wait, profile.RepositoryName, extractionpublication.ScheduleStage,
				)
				progressErr = errors.Join(progressErr, reader.Close(context.WithoutCancel(wait)))
			}
			t.Logf("extraction readiness progress=%+v error=%v", progress, progressErr)
			lastExtractionDiagnostic = time.Now()
		}
		if errors.Is(inspectErr, errRepositoryIndexTerminal) ||
			errors.Is(inspectErr, errObservationBoundRefusal) ||
			errors.Is(inspectErr, errObservationTerminal) ||
			errors.Is(inspectErr, errExtractionBoundRefusal) ||
			errors.Is(inspectErr, errExtractionJobTerminal) ||
			errors.Is(inspectErr, errExtractionScheduleTerminal) {
			var progress store.GenerationScheduleProgress
			var progressErr error
			reader, openErr := store.OpenLocalGenerationChunkReader(wait, profile.DataDir)
			if openErr == nil {
				progress, progressErr = reader.GenerationScheduleProgress(
					wait, profile.RepositoryName, extractionpublication.ScheduleStage,
				)
				progressErr = errors.Join(progressErr, reader.Close(context.WithoutCancel(wait)))
			} else {
				progressErr = openErr
			}
			failure := store.GenerationScheduleFailure{}
			if progress.Failure != nil {
				failure = *progress.Failure
			}
			t.Fatalf(
				"production path terminated at %s (repository_index_class=%s, extraction=%+v, failure=%+v, refusal=%+v, extraction_error=%v, server_tail=%q): %v",
				probe.Stage, probe.RepositoryIndexFailureClass, progress, failure,
				failure.Refusal, progressErr, readinessFailureTail(failureLog), inspectErr,
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

func readinessFailureTail(paths []string) string {
	if len(paths) != 1 || paths[0] == "" {
		return ""
	}
	return rehearsalLogTail(paths[0])
}
