package t4013

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
)

const repositoryScaleTimingEnvironment = "PHEBS_T4013_REPOSITORY_SCALE_TIMING"

type repositoryScaleTimingReview struct {
	Schema                   string                      `json:"schema"`
	SourceCommit             string                      `json:"source_commit"`
	ColdDeadlineMS           int64                       `json:"cold_deadline_ms"`
	ObservationCurrentWallMS int64                       `json:"observation_current_wall_ms"`
	SampleTarget             int                         `json:"sample_target"`
	Samples                  int                         `json:"samples"`
	ScheduleTotal            int                         `json:"schedule_total"`
	ScheduleMaterialized     int                         `json:"schedule_materialized"`
	SchedulePending          int                         `json:"schedule_pending"`
	ScheduleRunning          int                         `json:"schedule_running"`
	ScheduleSucceeded        int                         `json:"schedule_succeeded"`
	ScheduleFailed           int                         `json:"schedule_failed"`
	Timing                   ExtractionTimingObservation `json:"timing"`
	RuntimeMinMS             int64                       `json:"runtime_min_ms"`
	RuntimeP50MS             int64                       `json:"runtime_p50_ms"`
	RuntimeP95MS             int64                       `json:"runtime_p95_ms"`
	RuntimeMaxMS             int64                       `json:"runtime_max_ms"`
	ProjectedExtractionP50MS int64                       `json:"projected_extraction_p50_ms"`
	ProjectedExtractionP95MS int64                       `json:"projected_extraction_p95_ms"`
	ProjectedColdP50MS       int64                       `json:"projected_cold_p50_ms"`
	ProjectedColdP95MS       int64                       `json:"projected_cold_p95_ms"`
	P50HeadroomMS            int64                       `json:"p50_headroom_ms"`
	P95HeadroomMS            int64                       `json:"p95_headroom_ms"`
}

// TestRepositoryScaleTimingReview is an opt-in engineering diagnostic, not a
// ceremony. It authors the exact frozen corpus, observes only bounded public
// progress plus source-free timing logs, and destroys all custody on exit.
func TestRepositoryScaleTimingReview(t *testing.T) {
	if os.Getenv(repositoryScaleTimingEnvironment) != "1" {
		t.Skip("set " + repositoryScaleTimingEnvironment + "=1 to run the repository-scale timing review")
	}
	sampleTarget := 32
	if raw := os.Getenv("PHEBS_T4013_TIMING_SAMPLES"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 8 || value > 256 {
			t.Fatal("PHEBS_T4013_TIMING_SAMPLES must be between 8 and 256")
		}
		sampleTarget = value
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Hour)
	defer cancel()
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot, err = filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	commitBytes, err := exec.CommandContext(ctx, "git", "-C", moduleRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	commit := string(commitBytes[:len(commitBytes)-1])
	plan, err := FrozenHostPlanAtCheckout(ctx, moduleRoot, commit)
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	parent := os.Getenv("PHEBS_T4013_TIMING_PARENT")
	if parent == "" {
		parent = filepath.Dir(moduleRoot)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	planFile, err := os.CreateTemp(parent, "t4013-timing-plan-*.json")
	if err != nil {
		t.Fatal(err)
	}
	planPath := planFile.Name()
	defer func() { _ = os.Remove(planPath) }()
	if err := planFile.Chmod(0o600); err != nil {
		_ = planFile.Close()
		t.Fatal(err)
	}
	if _, err := planFile.Write(planBytes); err != nil {
		_ = planFile.Close()
		t.Fatal(err)
	}
	if err := planFile.Close(); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(parent, "t4013-repository-scale-timing-"+commit[:12])
	prepared, err := Prepare(ctx, PrepareRequest{
		ModuleRoot: moduleRoot, Workspace: workspace, PlanPath: planPath,
		Confirm: PrepareConfirm, BasePort: 42731,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cleanupErr := DestroyPrepared(prepared, moduleRoot); cleanupErr != nil {
			t.Errorf("destroy repository-scale timing custody: %v", cleanupErr)
		}
	}()
	toolchain, err := buildWorkingTreeToolchain(ctx, moduleRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	profile := prepared.Profiles[0]
	server, err := launchPrivateServer(ctx, profile, toolchain, "repository-scale-timing")
	if err != nil {
		t.Fatal(err)
	}
	running := true
	defer func() {
		if running {
			_ = server.stop(30 * time.Second)
		}
	}()
	if _, err := awaitPrivateServerHealth(ctx, server, profile, "repository-scale-timing", 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	timingCursor, err := newPartitionTimingCursor(server.logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = timingCursor.Close() }()
	lifecycleCursor, err := newChunkLifecycleCursor(server.logPath, 0, chunkLifecycleValidationV17)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lifecycleCursor.Close() }()
	inspector, err := newProfileInspector(profile, profileInspectionLegacy)
	if err != nil {
		t.Fatal(err)
	}
	started := server.started
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var observationCurrent time.Duration
	var reports []extractionpublication.PartitionTimingReport
	var timing ExtractionTimingObservation
	var progress extractionpublication.Progress
	for len(reports) < sampleTarget {
		_, probe, inspectErr := inspector.inspectWithProgress(ctx, profile, "a")
		if observationCurrent == 0 && probe.Stage == "extraction_publication" {
			observationCurrent = time.Since(started)
		}
		if inspectErr == nil {
			t.Fatal("repository-scale diagnostic converged before its timing sample closed")
		}
		newReports, pollErr := timingCursor.poll()
		if pollErr != nil {
			t.Fatal(pollErr)
		}
		for _, report := range newReports {
			addPartitionTiming(&timing, report)
			reports = append(reports, report)
		}
		lifecycleReports, pollErr := lifecycleCursor.poll()
		if pollErr != nil {
			t.Fatal(pollErr)
		}
		for _, report := range lifecycleReports {
			addSchedulerTiming(&timing, report)
		}
		if observationCurrent > 0 {
			path := "/api/extraction-progress?repository=" + url.QueryEscape(profile.RepositoryName)
			if getErr := inspector.get(ctx, profile, path, &progress); getErr != nil {
				t.Fatal(getErr)
			}
			if progress.Failed != 0 {
				t.Fatalf("repository-scale extraction reported %d failed partitions", progress.Failed)
			}
		}
		if len(reports) >= sampleTarget {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := server.stop(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	running = false
	if observationCurrent <= 0 || progress.Total <= 0 || timing.SchedulerSettled == 0 {
		t.Fatalf("incomplete timing evidence: observation=%s progress=%+v timing=%+v", observationCurrent, progress, timing)
	}
	durations := make([]int64, len(reports))
	for index, report := range reports {
		durations[index] = report.TotalMS
	}
	slices.Sort(durations)
	p50 := percentileNearestRank(durations, 50)
	p95 := percentileNearestRank(durations, 95)
	deadline := plan.Safety.FullConvergenceDeadlineMS
	review := repositoryScaleTimingReview{
		Schema: "t4013-repository-scale-timing-review-v1", SourceCommit: commit,
		ColdDeadlineMS: deadline, ObservationCurrentWallMS: observationCurrent.Milliseconds(),
		SampleTarget: sampleTarget, Samples: len(reports),
		ScheduleTotal: progress.Total, ScheduleMaterialized: progress.Materialized,
		SchedulePending: progress.Pending, ScheduleRunning: progress.Running,
		ScheduleSucceeded: progress.Succeeded, ScheduleFailed: progress.Failed,
		Timing: timing, RuntimeMinMS: durations[0], RuntimeP50MS: p50,
		RuntimeP95MS: p95, RuntimeMaxMS: durations[len(durations)-1],
		ProjectedExtractionP50MS: int64(progress.Total) * p50,
		ProjectedExtractionP95MS: int64(progress.Total) * p95,
	}
	review.ProjectedColdP50MS = review.ObservationCurrentWallMS + review.ProjectedExtractionP50MS
	review.ProjectedColdP95MS = review.ObservationCurrentWallMS + review.ProjectedExtractionP95MS
	review.P50HeadroomMS = deadline - review.ProjectedColdP50MS
	review.P95HeadroomMS = deadline - review.ProjectedColdP95MS
	raw, err := json.Marshal(review)
	if err != nil || len(raw) > MaxObservationBytes {
		t.Fatal(errors.Join(err, fmt.Errorf("timing review bytes=%d", len(raw))))
	}
	t.Logf("REPOSITORY_SCALE_TIMING_REVIEW %s", raw)
}

func percentileNearestRank(sorted []int64, percentile int) int64 {
	if len(sorted) == 0 || percentile < 1 || percentile > 100 {
		return 0
	}
	index := (percentile*len(sorted) + 99) / 100
	return sorted[index-1]
}
