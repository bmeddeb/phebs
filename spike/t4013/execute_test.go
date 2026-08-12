package t4013

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestStoppedExecutionDestroysOnlyExactCustodyAndRemainsReceiptable(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	outside := filepath.Join(root, "outside")
	for _, path := range []string{module, workspace, outside} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "private"), []byte("destroy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "retained"), []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	observation := emptyObservation(EnvironmentObservation{
		OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
		FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30, InitialUsedPercent: 72,
	})
	run := &execution{
		moduleRoot: module, workspace: workspace, observation: observation,
		plan: Plan{Safety: frozenSafety}, phase: 5,
	}
	var removeCalls int
	run.custodyDestroy = func(workspace, moduleRoot string) error {
		return destroyCustodyWith(workspace, moduleRoot, func(path string) error {
			removeCalls++
			if removeCalls == 1 {
				if err := os.RemoveAll(path); err != nil {
					return err
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(path, "late-writer"), []byte("destroy"), 0o600); err != nil {
					return err
				}
				return &os.PathError{Op: "unlinkat", Path: path, Err: syscall.ENOTEMPTY}
			}
			return os.RemoveAll(path)
		}, func(time.Duration) {})
	}
	run.startPhase(5)
	stopped, err := run.stopAfterFailure(directRecovery(errors.New("injected recovery failure")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatal("exact custody survived stopped-run teardown")
	}
	if removeCalls != 2 {
		t.Fatalf("stopped-run transient cleanup calls = %d, want 2", removeCalls)
	}
	if _, err := os.Lstat(filepath.Join(outside, "retained")); err != nil {
		t.Fatal("stopped-run teardown crossed custody boundary")
	}
	if stopped.Outcome != "stopped" || stopped.Decision.Selected != "p6_investigation" ||
		!stopped.Decision.Substantiated || !stopped.Teardown.Completed || len(stopped.Failures) != 1 ||
		stopped.Phases[5].Metrics.DataAllocatedBytes == 0 {
		t.Fatalf("stopped observation = %+v", stopped)
	}
}

func TestMissingFailedPhaseMeterCannotSelectFrozenDecision(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	for _, path := range []string{module, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	observation := emptyObservation(EnvironmentObservation{
		OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
		FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
	})
	run := &execution{
		moduleRoot: module, workspace: workspace, observation: observation,
		plan: Plan{Safety: frozenSafety},
	}
	run.startPhase(1)
	run.metersExpected = 1
	stopped, err := run.stopAfterFailure(errors.New("injected cold failure"))
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Decision.Selected != "unclassified" || stopped.Decision.Substantiated ||
		stopped.Failures[0].Code != "failed_phase_measurement_unavailable" {
		t.Fatalf("stopped decision = %+v, failure = %+v", stopped.Decision, stopped.Failures)
	}
}

func TestStoppedExecutionResultPreservesObservationWhenShutdownFails(t *testing.T) {
	observation := Observation{Schema: ObservationSchema, Outcome: "stopped"}
	got, err := stoppedExecutionResult(
		observation, errors.New("phase failed"), errors.New("server shutdown failed"),
	)
	if got.Schema != ObservationSchema || got.Outcome != "stopped" ||
		!errors.Is(err, ErrGateStopped) || !strings.Contains(err.Error(), "server shutdown failed") {
		t.Fatalf("stopped result = %+v, %v", got, err)
	}
}

func TestV2ObservationStartsWithFrozenHostToolchain(t *testing.T) {
	plan, err := frozenV2PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	observation := emptyObservationForPlan(EnvironmentObservation{
		OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
		FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
	}, plan)
	if observation.Schema != ObservationSchemaV2 ||
		len(observation.HostToolchain) != len(plan.HostToolchain) {
		t.Fatalf("v2 observation = %+v", observation)
	}
	plan.HostToolchain[0].Version = "mutated after observation creation"
	if observation.HostToolchain[0].Version == plan.HostToolchain[0].Version {
		t.Fatal("v2 observation aliases the mutable plan host toolchain")
	}
}

func TestV3ObservationStartsWithFrozenHostToolchainAndStartupSchema(t *testing.T) {
	plan, err := frozenPlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	observation := emptyObservationForPlan(EnvironmentObservation{
		OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
		FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
	}, plan)
	if observation.Schema != ObservationSchemaV3 ||
		len(observation.HostToolchain) != len(plan.HostToolchain) || len(observation.ServerStartups) != 0 {
		t.Fatalf("v3 observation = %+v", observation)
	}
}

func TestServerHealthDeadlineRetainsLaunchMeterAndStartupDiagnostic(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	profileRoot := filepath.Join(workspace, "structural")
	for _, path := range []string{module, workspace, profileRoot} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	serverPath := filepath.Join(root, "fake-phebs")
	if err := os.WriteFile(serverPath, []byte("#!/bin/sh\ntrap 'exit 0' INT TERM\nwhile :; do :; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(profileRoot, "credential")
	configPath := filepath.Join(profileRoot, "phebs.yaml")
	if err := os.WriteFile(credential, []byte("private-test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := &execution{
		ctx: t.Context(), moduleRoot: module, workspace: workspace,
		plan:      Plan{Schema: PlanSchemaV3, Safety: SafetyEnvelope{ServerHealthDeadlineMS: 300}},
		toolchain: privateToolchain{Schema: privateToolchainSchema, Phebs: serverPath},
		observation: emptyObservationForPlan(EnvironmentObservation{
			OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
			FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
		}, Plan{Schema: PlanSchemaV3}),
	}
	run.startPhase(1)
	server, meter, err := run.startServer(PreparedProfile{
		Name: "structural-2m-v1", Config: configPath, Credential: credential,
		Address: "127.0.0.1:1",
	}, "cold", nil)
	if err == nil || server == nil || meter == nil {
		t.Fatalf("deadline start = %v, %v, %v", server, meter, err)
	}
	if stopErr := run.stopServers(); stopErr != nil {
		t.Fatal(stopErr)
	}
	if _, finishErr := run.finishMeter(meter, nil); finishErr != nil {
		t.Fatal(finishErr)
	}
	if captureErr := run.captureFailedPhase(); captureErr != nil {
		t.Fatalf("failed startup measurement was not complete: %v", captureErr)
	}
	if len(run.observation.ServerStartups) != 1 {
		t.Fatalf("startup inventory = %+v", run.observation.ServerStartups)
	}
	startup := run.observation.ServerStartups[0]
	if startup.Outcome != "deadline" || startup.LastHealthClass != "transport" ||
		startup.HealthAttempts == 0 || startup.WallMS < 250 || !digestIdentity(startup.LogSHA256) ||
		run.observation.Phases[1].Metrics.WallMS < 250 {
		t.Fatalf("startup = %+v, phase = %+v", startup, run.observation.Phases[1])
	}
	if time.Duration(startup.WallMS)*time.Millisecond < 250*time.Millisecond {
		t.Fatalf("startup wall = %dms", startup.WallMS)
	}
}

func TestConvergenceDeadlineRetainsClosedLastProgress(t *testing.T) {
	credential := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credential, []byte("private-test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `[]`)
	}))
	defer server.Close()
	plan, err := frozenV4PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	run := &execution{
		ctx: t.Context(), plan: plan,
		observation: emptyObservationForPlan(EnvironmentObservation{
			OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
			FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
		}, plan),
	}
	profile := PreparedProfile{
		Name: "structural-2m-v1", Address: strings.TrimPrefix(server.URL, "http://"),
		Credential: credential, RepositoryName: "example.invalid/repository",
		Revisions: map[string]string{"a": testSourceCommit},
	}
	_, err = run.waitSnapshot(profile, "a", "cold", 25*time.Millisecond, &privateServer{done: make(chan error, 1)})
	if !errors.Is(err, errConvergenceDeadline) || len(run.observation.ConvergenceWaits) != 1 {
		t.Fatalf("deadline = %v, waits=%+v", err, run.observation.ConvergenceWaits)
	}
	wait := run.observation.ConvergenceWaits[0]
	if wait.Outcome != "deadline" || wait.LastStage != "repository_visibility" ||
		wait.Attempts == 0 || wait.ProgressChanges != 0 || !digestIdentity(wait.FirstProgressSHA256) ||
		wait.FirstProgressSHA256 != wait.LastProgressSHA256 || wait.DeadlineMS != 25 || wait.WallMS < 25 {
		t.Fatalf("wait = %+v", wait)
	}
}

func TestObservationProgressConvergenceAllowsSuccessfulRetryAttempts(t *testing.T) {
	const sourceDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	progress := observationpublication.Progress{
		State: "current",
		Publication: &observationpublication.PublicationProgress{
			State: "current", SourceGenerationDigest: sourceDigest,
		},
		Schedule: &observationpublication.ScheduleProgress{
			State: "settled", TotalPartitions: 2, Materialized: 3, Succeeded: 2,
		},
	}
	if !observationProgressConverged(progress, sourceDigest) {
		t.Fatal("a settled schedule with one successful retry was rejected")
	}
	progress.Schedule.Succeeded = 1
	if observationProgressConverged(progress, sourceDigest) {
		t.Fatal("an incompletely settled schedule was accepted")
	}
}

func TestConvergenceProgressTrackerCountsOnlyIdentityChanges(t *testing.T) {
	first := convergenceProbe("repository_index", "queued")
	second := convergenceProbe("repository_index", "running")
	third := convergenceProbe("source_generation", "ready")
	var tracker convergenceProgressTracker
	tracker.observe(first, convergenceInspectionDiagnostic{class: "pending"}, time.Second)
	tracker.observe(first, convergenceInspectionDiagnostic{class: "pending"}, 2*time.Second)
	tracker.observe(second, convergenceInspectionDiagnostic{class: "pending"}, 3*time.Second)
	tracker.observe(second, convergenceInspectionDiagnostic{class: "pending"}, 4*time.Second)
	tracker.observe(third, convergenceInspectionDiagnostic{class: "pending"}, 5*time.Second)
	if tracker.attempts != 5 || tracker.progressChanges != 2 || tracker.stageChanges != 1 ||
		tracker.lastProgressChange != 5*time.Second || tracker.first != first || tracker.last != third {
		t.Fatalf("tracker = %+v", tracker)
	}
}

func TestV5ConvergenceTrackerRetainsOnlySourceFreeProgressProjection(t *testing.T) {
	progress := observationpublication.Progress{
		State: "building",
		Planning: &observationpublication.PlanningProgress{
			State: "settled", Pending: 0, Running: 0, Succeeded: 1,
		},
		Schedule: &observationpublication.ScheduleProgress{
			State: "active", TotalPartitions: 62, Materialized: 62,
			Pending: 20, Running: 2, Succeeded: 40,
		},
	}
	probe := observationConvergenceProbe(progress)
	var tracker convergenceProgressTracker
	tracker.observe(probe, convergenceInspectionDiagnostic{class: "pending"}, 90*time.Minute)
	if tracker.observationProgress == nil ||
		tracker.observationProgress.ScheduleTotalPartitions != 62 ||
		tracker.observationProgress.ScheduleSucceeded != 40 ||
		tracker.observationProgressAtWall != 90*time.Minute {
		t.Fatalf("tracker = %+v", tracker)
	}
}

func TestCanceledConvergenceAttemptDoesNotReplaceLastSuccessfulProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	tracker := convergenceProgressTracker{}
	first := convergenceProbe("observation_publication", "running")
	tracker.observe(first, convergenceInspectionDiagnostic{class: "pending"}, time.Second)
	cancel()
	if completed, _ := observeCompletedConvergenceAttempt(
		ctx, &tracker, convergenceProbe("repository_visibility"),
		convergenceInspectionDiagnostic{class: "transport"}, 2*time.Second,
	); completed {
		t.Fatal("canceled convergence attempt was retained")
	}
	if tracker.attempts != 1 || tracker.last != first || tracker.progressChanges != 0 ||
		len(tracker.inspectionTransitions) != 1 {
		t.Fatalf("tracker = %+v", tracker)
	}
}

func TestFailedInspectionDoesNotReplaceLastSuccessfulProbe(t *testing.T) {
	tracker := convergenceProgressTracker{}
	successful := convergenceProbe("observation_publication", "running")
	failed := convergenceProbe("repository_visibility", "transport-failure")
	tracker.observe(successful, convergenceInspectionDiagnostic{class: "pending"}, time.Second)
	tracker.observe(failed, convergenceInspectionDiagnostic{class: "transport"}, 2*time.Second)
	if tracker.attempts != 2 || tracker.last != successful || tracker.lastSuccessfulAt != time.Second ||
		tracker.progressChanges != 0 || len(tracker.inspectionTransitions) != 2 {
		t.Fatalf("tracker = %+v", tracker)
	}
	last := tracker.inspectionTransitions[len(tracker.inspectionTransitions)-1]
	if last.Class != "transport" || last.ProgressSHA256 != failed.SHA256 || last.WallMS != 2_000 {
		t.Fatalf("failure transition = %+v", last)
	}
	run := &execution{plan: Plan{Schema: PlanSchemaV6}}
	run.recordConvergenceWait(
		PreparedProfile{Name: "structural-2m-v1"}, "a", "cold", "canceled",
		time.Minute, time.Now().Add(-3*time.Second), tracker,
	)
	if len(run.observation.ConvergenceWaits) != 1 {
		t.Fatalf("waits = %+v", run.observation.ConvergenceWaits)
	}
	wait := run.observation.ConvergenceWaits[0]
	if wait.LastSuccessfulProbeSHA256 != successful.SHA256 ||
		wait.LastSuccessfulProbeWallMS != 1_000 ||
		wait.LastProgressSHA256 != successful.SHA256 ||
		wait.InspectionTransitions[len(wait.InspectionTransitions)-1].Class != "transport" {
		t.Fatalf("wait = %+v", wait)
	}
	if err := validateConvergenceWaits(run.observation.ConvergenceWaits, 2); err != nil {
		t.Fatalf("pending-then-transport wait failed validation: %v", err)
	}
}

func TestV7RepeatedHTTPStatusRetainsClosedLastInspection(t *testing.T) {
	var tracker convergenceProgressTracker
	successful := convergenceProbe("observation_publication", "63-of-64")
	failed := convergenceProbe("observation_publication", "selected-search-generation")
	status := convergenceInspectionDiagnostic{
		class: "status", httpStatus: 500, httpReason: httpReason500Projection,
	}
	tracker.observe(successful, convergenceInspectionDiagnostic{class: "pending"}, time.Second)
	tracker.observe(failed, status, 2*time.Second)
	tracker.observe(failed, status, 3*time.Second)
	if len(tracker.inspectionTransitions) != 2 {
		t.Fatalf("transitions = %+v", tracker.inspectionTransitions)
	}
	plan, err := frozenV7PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	run := &execution{plan: plan}
	run.recordConvergenceWait(
		PreparedProfile{Name: "structural-2m-v1"}, "a", "cold", "deadline",
		time.Minute, time.Now().Add(-4*time.Second), tracker,
	)
	if len(run.observation.ConvergenceWaits) != 1 {
		t.Fatalf("waits = %+v", run.observation.ConvergenceWaits)
	}
	wait := run.observation.ConvergenceWaits[0]
	if wait.LastSuccessfulProbeSHA256 != successful.SHA256 ||
		wait.LastInspectionStage != failed.Stage || wait.LastInspectionClass != "status" ||
		wait.LastInspectionHTTPStatus != 500 || wait.LastInspectionHTTPReason != httpReason500Projection ||
		wait.LastInspectionSHA256 != failed.SHA256 || wait.LastInspectionWallMS != 3_000 {
		t.Fatalf("wait = %+v", wait)
	}
	if err := validateConvergenceWaits(run.observation.ConvergenceWaits, 3); err != nil {
		t.Fatalf("v7 repeated status wait failed: %v", err)
	}
}

func TestServerExitWinsOverSimultaneousConvergenceDeadline(t *testing.T) {
	phase, cancel := context.WithCancel(t.Context())
	cancel()
	server := &privateServer{done: make(chan error, 1)}
	server.done <- errors.New("synthetic server exit")
	tracker := convergenceProgressTracker{}
	tracker.observe(
		convergenceProbe("observation_publication", "running"),
		convergenceInspectionDiagnostic{class: "pending"}, time.Second,
	)
	if completed, _ := observeCompletedConvergenceAttempt(
		phase, &tracker, convergenceProbe("repository_visibility"),
		convergenceInspectionDiagnostic{class: "transport"}, 2*time.Second,
	); completed {
		t.Fatal("canceled convergence attempt was retained")
	}
	exitErr, exited := retainServerExit(server)
	if !exited || exitErr == nil {
		t.Fatalf("server exit = %v, exited=%t", exitErr, exited)
	}
	if tracker.last.Stage != "observation_publication" || tracker.attempts != 1 {
		t.Fatalf("tracker = %+v", tracker)
	}
	select {
	case <-server.done:
	default:
		t.Fatal("server exit result was not preserved for cleanup")
	}
}

func TestConvergenceTransitionInventoryFailsClosedAtBound(t *testing.T) {
	var tracker convergenceProgressTracker
	digest := "sha256:" + strings.Repeat("a", 64)
	var exceeded bool
	for index := 0; index <= maxConvergenceTransitions; index++ {
		class := "pending"
		if index%2 == 1 {
			class = "transport"
		}
		exceeded = tracker.observeTransition(
			"repository_visibility", convergenceInspectionDiagnostic{class: class}, digest,
			time.Duration(index)*time.Millisecond,
		)
		if exceeded != (index == maxConvergenceTransitions) {
			t.Fatalf("transition %d exceeded=%t", index, exceeded)
		}
	}
	if len(tracker.inspectionTransitions) != maxConvergenceTransitions ||
		tracker.inspectionTransitions[0].WallMS != 0 ||
		tracker.inspectionTransitions[len(tracker.inspectionTransitions)-1].WallMS != maxConvergenceTransitions-1 {
		t.Fatalf("transitions = %+v", tracker.inspectionTransitions)
	}
}

func TestV13ConvergenceProgressChurnCoalescesWithoutWeakeningTransitionBound(t *testing.T) {
	tracker := convergenceProgressTracker{coalesceTransitionProgress: true}
	first := convergenceProbe("extraction_publication", "progress", 0)
	for index := 0; index < 100; index++ {
		probe := convergenceProbe("extraction_publication", "progress", index)
		if tracker.observe(probe, convergenceInspectionDiagnostic{class: "pending"}, time.Duration(index+1)*time.Second) {
			t.Fatalf("healthy progress %d exceeded the transition bound", index)
		}
	}
	if len(tracker.inspectionTransitions) != 1 {
		t.Fatalf("transitions = %+v", tracker.inspectionTransitions)
	}
	transition := tracker.inspectionTransitions[0]
	if transition.FirstProgressSHA256 != first.SHA256 ||
		transition.ProgressSHA256 != tracker.last.SHA256 ||
		transition.ProgressChanges != 99 || transition.LastProgressChangeWallMS != 100_000 {
		t.Fatalf("coalesced transition = %+v", transition)
	}
	plan, err := frozenV13PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	run := &execution{plan: plan}
	run.recordConvergenceWait(
		PreparedProfile{Name: "structural-2m-v1"}, "a", "cold", "canceled",
		2*time.Minute, time.Now().Add(-101*time.Second), tracker,
	)
	if err := validateConvergenceWaits(run.observation.ConvergenceWaits, 9); err != nil {
		t.Fatalf("v13 coalesced wait failed validation: %v", err)
	}
	if err := validateConvergenceWaits(run.observation.ConvergenceWaits, 8); err == nil {
		t.Fatal("v12 detail contract accepted v13 coalesced transition fields")
	}

	// Alternating classes are genuine transition diversity and retain the
	// original fail-closed 33rd-transition behavior.
	tracker = convergenceProgressTracker{coalesceTransitionProgress: true}
	for index := 0; index <= maxConvergenceTransitions; index++ {
		class := "pending"
		if index%2 == 1 {
			class = "transport"
		}
		exceeded := tracker.observeTransition(
			"extraction_publication", convergenceInspectionDiagnostic{class: class},
			convergenceProbe("extraction_publication", index).SHA256,
			time.Duration(index+1)*time.Millisecond,
		)
		if exceeded != (index == maxConvergenceTransitions) {
			t.Fatalf("transition %d exceeded=%t", index, exceeded)
		}
	}
}

func TestV7DiagnosticLimitRetainsBoundedOverflowInspection(t *testing.T) {
	plan, err := frozenV7PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	var tracker convergenceProgressTracker
	first := convergenceProbe("observation_publication", "first-success")
	if tracker.observe(first, convergenceInspectionDiagnostic{class: "pending"}, time.Millisecond) {
		t.Fatal("first transition unexpectedly exceeded the bound")
	}
	status := convergenceInspectionDiagnostic{
		class: "status", httpStatus: 500, httpReason: httpReason500Projection,
	}
	var overflow privateConvergenceProbe
	for index := 1; index <= maxConvergenceTransitions; index++ {
		overflow = convergenceProbe("observation_publication", index)
		exceeded := tracker.observe(overflow, status, time.Duration(index+1)*time.Millisecond)
		if exceeded != (index == maxConvergenceTransitions) {
			t.Fatalf("inspection %d exceeded=%t", index, exceeded)
		}
	}
	if len(tracker.inspectionTransitions) != maxConvergenceTransitions ||
		tracker.inspectionTransitions[len(tracker.inspectionTransitions)-1].ProgressSHA256 == overflow.SHA256 {
		t.Fatalf("tracker = %+v", tracker)
	}
	run := &execution{plan: plan}
	run.recordConvergenceWait(
		PreparedProfile{Name: "structural-2m-v1"}, "a", "cold", "diagnostic_limit",
		time.Minute, time.Now().Add(-time.Second), tracker,
	)
	if len(run.observation.ConvergenceWaits) != 1 {
		t.Fatalf("waits = %+v", run.observation.ConvergenceWaits)
	}
	wait := run.observation.ConvergenceWaits[0]
	if !wait.TransitionLimitExceeded || wait.Attempts != maxConvergenceTransitions+1 ||
		len(wait.InspectionTransitions) != maxConvergenceTransitions ||
		wait.LastInspectionSHA256 != overflow.SHA256 || wait.LastInspectionClass != "status" ||
		wait.LastInspectionHTTPStatus != 500 || wait.LastInspectionHTTPReason != httpReason500Projection {
		t.Fatalf("wait = %+v", wait)
	}
	if err := validateConvergenceWaits(run.observation.ConvergenceWaits, 3); err != nil {
		t.Fatalf("v7 bounded overflow wait failed validation: %v", err)
	}

	invalid := wait
	tail := invalid.InspectionTransitions[len(invalid.InspectionTransitions)-1]
	invalid.LastInspectionStage = tail.Stage
	invalid.LastInspectionClass = tail.Class
	invalid.LastInspectionHTTPStatus = tail.HTTPStatus
	invalid.LastInspectionHTTPReason = tail.HTTPReason
	invalid.LastInspectionSHA256 = tail.ProgressSHA256
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{invalid}, 3); err == nil {
		t.Fatal("v7 diagnostic limit accepted the timeline tail as the overflow inspection")
	}
}

func TestConvergenceWaitStopsBeforeFirstInspectionWhenServerAlreadyExited(t *testing.T) {
	credential := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credential, []byte("private-test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requestObserved := make(chan struct{}, 1)
	httpServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		select {
		case requestObserved <- struct{}{}:
		default:
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `[]`)
	}))
	defer httpServer.Close()
	plan, err := frozenV6PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	run := &execution{
		ctx: t.Context(), plan: plan,
		observation: emptyObservationForPlan(EnvironmentObservation{
			OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
			FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
		}, plan),
	}
	profile := PreparedProfile{
		Name: "structural-2m-v1", Address: strings.TrimPrefix(httpServer.URL, "http://"),
		Credential: credential, RepositoryName: "example.invalid/repository",
		Revisions: map[string]string{"a": testSourceCommit},
	}
	server := &privateServer{done: make(chan error, 1)}
	server.done <- errors.New("synthetic server exit")
	_, err = run.waitSnapshot(profile, "a", "cold", time.Second, server)
	if !errors.Is(err, errConvergenceServerExit) || len(run.observation.ConvergenceWaits) != 1 {
		t.Fatalf("server exit = %v, waits=%+v", err, run.observation.ConvergenceWaits)
	}
	wait := run.observation.ConvergenceWaits[0]
	if wait.Outcome != "server_exited" || wait.Attempts != 0 ||
		wait.FirstStage != convergenceNotInspected || wait.LastStage != convergenceNotInspected ||
		len(wait.InspectionTransitions) != 0 || wait.LastSuccessfulProbeSHA256 != "" ||
		wait.LastSuccessfulProbeWallMS != 0 {
		t.Fatalf("wait = %+v", wait)
	}
	if err := validateConvergenceWaits(run.observation.ConvergenceWaits, 2); err != nil {
		t.Fatalf("uninspected server-exit wait failed validation: %v", err)
	}
	select {
	case <-requestObserved:
		t.Fatal("inspection ran after server exit was already known")
	default:
	}
	select {
	case <-server.done:
	default:
		t.Fatal("server exit result was not preserved for cleanup")
	}
}

func TestConvergenceWaitCancelsBlockedInspectionOnServerExit(t *testing.T) {
	credential := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credential, []byte("private-test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requestStarted := make(chan struct{}, 1)
	httpServer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		requestStarted <- struct{}{}
		<-request.Context().Done()
	}))
	defer httpServer.Close()
	plan, err := frozenV6PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	run := &execution{
		ctx: t.Context(), plan: plan,
		observation: emptyObservationForPlan(EnvironmentObservation{
			OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
			FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
		}, plan),
	}
	profile := PreparedProfile{
		Name: "structural-2m-v1", Address: strings.TrimPrefix(httpServer.URL, "http://"),
		Credential: credential, RepositoryName: "example.invalid/repository",
		Revisions: map[string]string{"a": testSourceCommit},
	}
	server := &privateServer{done: make(chan error, 1)}
	result := make(chan error, 1)
	go func() {
		_, waitErr := run.waitSnapshot(profile, "a", "cold", time.Minute, server)
		result <- waitErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("blocked inspection did not start")
	}
	started := time.Now()
	server.done <- errors.New("synthetic in-flight server exit")
	select {
	case waitErr := <-result:
		if !errors.Is(waitErr, errConvergenceServerExit) {
			t.Fatalf("blocked inspection result = %v", waitErr)
		}
		if elapsed := time.Since(started); elapsed >= time.Second {
			t.Fatalf("server exit cancellation took %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server exit did not cancel the blocked inspection")
	}
	if len(run.observation.ConvergenceWaits) != 1 ||
		run.observation.ConvergenceWaits[0].Attempts != 0 ||
		len(run.observation.ConvergenceWaits[0].InspectionTransitions) != 0 {
		t.Fatalf("blocked wait = %+v", run.observation.ConvergenceWaits)
	}
}

func TestServerExitSelectsTerminalBeforeSynchronousControlReadDrains(t *testing.T) {
	run := &execution{}
	server := &privateServer{done: make(chan error, 1)}
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	type attemptResult struct {
		exitErr error
		exited  bool
	}
	result := make(chan attemptResult, 1)
	go func() {
		_, _, _, exitErr, exited := run.inspectConvergenceAttempt(
			t.Context(), server,
			func(context.Context) (privateProfileSnapshot, privateConvergenceProbe, error) {
				close(readStarted)
				// Model a synchronous bounded filesystem/control read: its syscall
				// cannot observe context cancellation until it returns.
				<-releaseRead
				return privateProfileSnapshot{}, convergenceProbe("source_generation"), nil
			},
		)
		result <- attemptResult{exitErr: exitErr, exited: exited}
	}()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("synchronous control read did not start")
	}
	started := time.Now()
	server.done <- errors.New("synthetic in-flight server exit")
	select {
	case attempt := <-result:
		if !attempt.exited || attempt.exitErr == nil {
			t.Fatalf("attempt = %+v", attempt)
		}
		if elapsed := time.Since(started); elapsed >= time.Second {
			t.Fatalf("terminal selection waited for synchronous read: %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server exit did not select a terminal result")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- run.stopServers() }()
	select {
	case err := <-stopped:
		t.Fatalf("teardown crossed an active control reader: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRead)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("drained teardown = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("teardown did not finish after the control reader drained")
	}
}

func TestRepositoryIndexTerminalStopsConvergenceWithoutWaitingForDeadline(t *testing.T) {
	const credential = "private-test-token"
	httpServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/repo-status" ||
			request.Header.Get("Authorization") != "Bearer "+credential {
			http.Error(response, "unexpected request", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `[{"name":"example.invalid/repository","last_index_job_state":"exact","last_index_job":{"status":"failed","attempts":3,"error":"heartbeat: context deadline exceeded"}}]`)
	}))
	defer httpServer.Close()
	credentialPath := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credentialPath, []byte(credential+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := frozenV10PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	run := &execution{
		ctx: t.Context(), plan: plan,
		observation: emptyObservationForPlan(EnvironmentObservation{
			OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
			FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
		}, plan),
	}
	profile := PreparedProfile{
		Name: "structural-2m-v1", Address: strings.TrimPrefix(httpServer.URL, "http://"),
		Credential: credentialPath, RepositoryName: "example.invalid/repository",
		Revisions: map[string]string{"a": testSourceCommit},
	}
	server := &privateServer{done: make(chan error, 1)}
	started := time.Now()
	_, waitErr := run.waitSnapshot(profile, "a", "cold", time.Minute, server)
	if !errors.Is(waitErr, errRepositoryIndexTerminal) {
		t.Fatalf("terminal wait = %v", waitErr)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("terminal wait took %s", elapsed)
	}
	if len(run.observation.ConvergenceWaits) != 1 {
		t.Fatalf("wait inventory = %+v", run.observation.ConvergenceWaits)
	}
	wait := run.observation.ConvergenceWaits[0]
	if wait.Outcome != "repository_index_terminal" || wait.Attempts != 1 ||
		wait.LastInspectionClass != "terminal" || len(wait.InspectionTransitions) != 1 ||
		wait.InspectionTransitions[0].Class != "terminal" ||
		wait.RepositoryIndexFailureClass != "lease_heartbeat" {
		t.Fatalf("terminal wait = %+v", wait)
	}
	if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 6); err != nil {
		t.Fatalf("terminal wait does not satisfy v10 receipt contract: %v", err)
	}
}

func TestMeterFinalizationFailureRemainsSticky(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	for _, path := range []string{module, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	run := &execution{
		moduleRoot: module, workspace: workspace, plan: Plan{Safety: frozenSafety},
		observation: emptyObservation(EnvironmentObservation{
			OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
			FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
		}),
	}
	run.startPhase(1)
	meter := &phaseMeter{}
	run.trackMeter(meter)
	if _, err := run.finishMeter(meter, nil); err == nil {
		t.Fatal("invalid meter unexpectedly finalized")
	}
	if run.measurementErr == nil {
		t.Fatal("meter finalization failure was not retained")
	}
	if _, present := run.activeMeters[meter]; !present {
		t.Fatal("failed meter was removed before stopped-phase capture")
	}
	stopped, err := run.stopAfterFailure(exactOracle("injected oracle failure"))
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Decision.Selected != "unclassified" || stopped.Decision.Substantiated ||
		stopped.Failures[0].Code != "failed_phase_measurement_unavailable" {
		t.Fatalf("stopped decision = %+v, failure = %+v", stopped.Decision, stopped.Failures)
	}
}

func TestStoppedFailureClassificationIsClosed(t *testing.T) {
	tests := []struct {
		name          string
		cause         error
		measurement   error
		ceiling       error
		code          string
		decision      string
		substantiated bool
	}{
		{name: "operational", cause: errors.New("build failed"), code: "operational_failure", decision: "unclassified"},
		{name: "convergence deadline", cause: errConvergenceDeadline, code: "convergence_deadline_expired", decision: "unclassified"},
		{name: "convergence server exit", cause: errConvergenceServerExit, code: "server_exited_during_convergence", decision: "unclassified"},
		{name: "convergence transition limit", cause: errConvergenceTimeline, code: "convergence_transition_limit_exceeded", decision: "unclassified"},
		{name: "repository index terminal", cause: errRepositoryIndexTerminal, code: "repository_index_terminal", decision: "unclassified"},
		{name: "extraction bound refusal", cause: errExtractionBoundRefusal, code: "extraction_production_bound_refused", decision: "reduce", substantiated: true},
		{name: "extraction job terminal", cause: errExtractionJobTerminal, code: "extraction_job_terminal", decision: "unclassified"},
		{name: "server exit overrides missing measurement", cause: errConvergenceServerExit, measurement: errors.New("meter failed"), code: "server_exited_during_convergence", decision: "unclassified"},
		{name: "transition limit overrides missing measurement", cause: errConvergenceTimeline, measurement: errors.New("meter failed"), code: "convergence_transition_limit_exceeded", decision: "unclassified"},
		{name: "repository index terminal overrides missing measurement", cause: errRepositoryIndexTerminal, measurement: errors.New("meter failed"), code: "repository_index_terminal", decision: "unclassified"},
		{name: "exact oracle", cause: exactOracle("mismatch"), code: "exact_gate_failed", decision: "reduce", substantiated: true},
		{name: "pressure refusal", cause: errProductionPressure, code: "production_pressure_gate_refused", decision: "reduce", substantiated: true},
		{name: "direct recovery", cause: directRecovery(errors.New("did not converge")), code: "direct_recovery_failed", decision: "p6_investigation", substantiated: true},
		{name: "recovery deadline remains recovery", cause: directRecovery(errConvergenceDeadline), code: "direct_recovery_failed", decision: "p6_investigation", substantiated: true},
		{name: "recovery server exit remains distinct", cause: directRecovery(errConvergenceServerExit), code: "server_exited_during_convergence", decision: "unclassified"},
		{name: "review ceiling", cause: errReviewCeiling, code: "review_ceiling_crossed", decision: "cohort_experiment", substantiated: true},
		{name: "parent ceiling overrides missing measurement", cause: context.DeadlineExceeded, measurement: errors.New("meter failed"), ceiling: errTotalWallDeadline, code: "review_ceiling_crossed", decision: "cohort_experiment", substantiated: true},
		{name: "missing measurement overrides metered ceiling", cause: errReviewCeiling, measurement: errors.New("meter failed"), code: "failed_phase_measurement_unavailable", decision: "unclassified"},
		{name: "missing measurement overrides non-ceiling failure", cause: exactOracle("mismatch"), measurement: errors.New("meter failed"), code: "failed_phase_measurement_unavailable", decision: "unclassified"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyStoppedFailure(test.cause, test.measurement, test.ceiling)
			if got.code != test.code || got.decision != test.decision || got.substantiated != test.substantiated {
				t.Fatalf("classification = %+v", got)
			}
		})
	}
}

func TestObservationWriterIsExclusiveAndAbsolute(t *testing.T) {
	value := completedObservation()
	path := filepath.Join(t.TempDir(), "observation.json")
	if err := WriteObservation(path, value); err != nil {
		t.Fatal(err)
	}
	if err := WriteObservation(path, value); err == nil {
		t.Fatal("observation writer replaced an existing output")
	}
	if err := WriteObservation("relative.json", value); err == nil {
		t.Fatal("observation writer accepted a relative path")
	}
}

func TestExecutionSafetyUsesPhaseGaugeMaximaAndTotalWall(t *testing.T) {
	run := &execution{plan: Plan{Safety: frozenSafety}, observation: emptyObservation(EnvironmentObservation{
		OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
		FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
	})}
	run.observation.Phases[0] = succeededPhase("preflight", PhaseMetrics{
		WallMS: frozenSafety.MaximumTotalWallMS,
	})
	if err := run.enforceSafety(); err != nil {
		t.Fatalf("exact wall ceiling failed: %v", err)
	}
	run.observation.Phases[1] = succeededPhase("cold", PhaseMetrics{WallMS: 1})
	if err := run.enforceSafety(); !errors.Is(err, errReviewCeiling) {
		t.Fatalf("wall overflow = %v", err)
	}
	run.observation.Phases[1] = succeededPhase("cold", PhaseMetrics{
		PeakRSSBytes: frozenSafety.MaximumPeakRSSBytes + 1,
	})
	if err := run.enforceSafety(); !errors.Is(err, errReviewCeiling) {
		t.Fatalf("RSS overflow = %v", err)
	}
	run.observation.Phases[1] = succeededPhase("cold", PhaseMetrics{
		DataAllocatedBytes: frozenSafety.MaximumDataAllocatedBytes + 1,
	})
	if err := run.enforceSafety(); !errors.Is(err, errReviewCeiling) {
		t.Fatalf("allocation overflow = %v", err)
	}
	run.observation.Phases[1] = PhaseObservation{
		Name: "cold", Outcome: "failed",
		Metrics: PhaseMetrics{PeakRSSBytes: frozenSafety.MaximumPeakRSSBytes + 1},
	}
	if err := run.enforceSafety(); !errors.Is(err, errReviewCeiling) {
		t.Fatalf("failed-phase RSS overflow = %v", err)
	}
}

func TestExecutionSafetyRecognizesFrozenTotalDeadlineCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	run := &execution{
		ctx:  ctx,
		plan: Plan{Safety: frozenSafetyV5},
		observation: Observation{Phases: []PhaseObservation{{
			Name: "cold", Outcome: "failed", Metrics: PhaseMetrics{
				WallMS: frozenSafetyV5.MaximumTotalWallMS - 1,
			},
		}}},
	}
	cancel(errTotalWallDeadline)
	ceilingErr := run.enforceSafety()
	if !errors.Is(ceilingErr, errTotalWallDeadline) || !errors.Is(ceilingErr, errReviewCeiling) {
		t.Fatalf("frozen total deadline = %v", ceilingErr)
	}
	got := classifyStoppedFailure(context.DeadlineExceeded, errors.New("meter failed"), ceilingErr)
	if got.code != "review_ceiling_crossed" || got.decision != "cohort_experiment" || !got.substantiated {
		t.Fatalf("deadline plus meter failure classification = %+v", got)
	}
}

func TestVerifyCleanCheckoutRejectsTrackedAndUntrackedChanges(t *testing.T) {
	root := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(context.Background(), "git", args...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return string(bytesTrimSpace(output))
	}
	runGit("init")
	runGit("config", "user.email", "t4013@example.invalid")
	runGit("config", "user.name", "T40.13")
	tracked := filepath.Join(root, "tracked.go")
	if err := os.WriteFile(tracked, []byte("package frozen\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tracked.go")
	runGit("commit", "-m", "freeze")
	commit := runGit("rev-parse", "HEAD")
	if err := verifyCleanCheckout(t.Context(), root, commit); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("package changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyCleanCheckout(t.Context(), root, commit); err == nil {
		t.Fatal("modified source checkout passed")
	}
	if err := os.WriteFile(tracked, []byte("package frozen\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.go"), []byte("package hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyCleanCheckout(t.Context(), root, commit); err == nil {
		t.Fatal("untracked source checkout passed")
	}
}

func TestToolchainObservationBindsEveryExecutable(t *testing.T) {
	root := t.TempDir()
	paths := []string{"phebs", "zoekt-git-index", "phebs-focused-index", "buf"}
	for index, name := range paths {
		if err := os.WriteFile(filepath.Join(root, name), []byte{byte(index + 1)}, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := observeToolchain(privateToolchain{
		Schema: privateToolchainSchema,
		Phebs:  filepath.Join(root, paths[0]), Zoekt: filepath.Join(root, paths[1]),
		Focused: filepath.Join(root, paths[2]), Buf: filepath.Join(root, paths[3]),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateToolchainObservation(observed, true); err != nil {
		t.Fatal(err)
	}
	if observed[0].SHA256 == observed[1].SHA256 {
		t.Fatal("distinct executables produced one toolchain identity")
	}
}

func TestFrozenSourceExportIgnoresWorkingTreeMutation(t *testing.T) {
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	runGit("init")
	runGit("config", "user.email", "t4013@example.invalid")
	runGit("config", "user.name", "T40.13")
	tracked := filepath.Join(root, "source.go")
	if err := os.WriteFile(tracked, []byte("package frozen\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "source.go")
	runGit("commit", "-m", "freeze")
	if err := os.WriteFile(tracked, []byte("package changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "export")
	if err := exportFrozenSource(t.Context(), root, output); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(output, "source.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package frozen\n" {
		t.Fatalf("exported source = %q", got)
	}
}

func TestAuthorizedQueryRequiresMatchesAndCitableRelationship(t *testing.T) {
	credential := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credential, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rows := true
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		schema := `"$schema":"http://` + request.Host + `/schemas/TestResponse.json",`
		switch request.URL.Path {
		case "/api/search":
			_, _ = io.WriteString(response, `{`+schema+`"files":[{"repo":"github.com/example/repo","path":"fixture.go","chunks":[{"content":"T401","start_line":1,"ranges":[{"start_line":1,"start_col":1,"end_line":1,"end_col":5}]}]}],"stats":{}}`)
		case "/api/services":
			_, _ = io.WriteString(response, `{`+schema+`"schema":"test","repository":{"catalog_service_count":1},"filters":{},"services":[{}],"pagination":{"order":"key","page_size":100,"returned":1}}`)
		case "/api/service-relationships":
			if rows {
				_, _ = io.WriteString(response, `{`+schema+`"schema":"test","query":{},"rows_state":"nonempty","roots":[{"generation":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","root_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}],"rows":[{"citation":"token"}],"coverage":{},"pagination":{},"caveat":""}`)
			} else {
				_, _ = io.WriteString(response, `{`+schema+`"schema":"test","query":{},"rows_state":"empty","roots":[{"generation":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","root_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}],"rows":[],"coverage":{},"pagination":{},"caveat":""}`)
			}
		case "/api/service-relationship-citation":
			_, _ = io.WriteString(response, `{`+schema+`"schema":"test","repository":"github.com/example/repo","root_schema":"test","generation":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","root_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","authority_digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","projection":{},"evidence":{},"content":"citation"}`)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	profile := PreparedProfile{
		RepositoryName: "github.com/example/repo", Credential: credential,
		Address: strings.TrimPrefix(server.URL, "http://"),
	}
	count, exact, err := queryProfile(t.Context(), profile, "semantic", true)
	if err != nil || !exact || count != 2 {
		t.Fatalf("query = %d/%t, %v", count, exact, err)
	}
	rows = false
	if _, _, err := queryProfile(t.Context(), profile, "semantic", true); err == nil {
		t.Fatal("citation-required query accepted zero relationship rows")
	}
}

func TestDerivedPartialScanReadsOnlyBoundedControlLevel(t *testing.T) {
	dataDir := t.TempDir()
	repository := filepath.Join(dataDir, "observations", "repository")
	deep := filepath.Join(repository, "immutable-generation", "objects")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, ".stage-not-a-control"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := derivedPartialPresent(dataDir)
	if err != nil || found {
		t.Fatalf("deep immutable member = %t, %v", found, err)
	}
	v2 := filepath.Join(repository, observationpublication.InventoryPublicationDirectoryV2)
	if err := os.Mkdir(v2, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v2, "publishing.json"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err = derivedPartialPresent(dataDir)
	if err != nil || !found {
		t.Fatalf("bounded v2 publication marker = %t, %v", found, err)
	}
}

func TestChunkLifecycleReaderBindsOneStartedAttempt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	startedLine := `generation chunk lifecycle: {"schema":"phebs-generation-chunk-lifecycle-v1","event":"started","identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","stage":"extraction-partitions","generation":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","attempt":2,"outcome":"running"}` + "\n"
	settledLine := `generation chunk lifecycle: {"schema":"phebs-generation-chunk-lifecycle-v1","event":"settled","identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","stage":"extraction-partitions","generation":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","attempt":2,"outcome":"stale_fenced"}` + "\n"
	if err := os.WriteFile(path, []byte(startedLine), 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	reports, err := cursor.poll()
	if err != nil || len(reports) != 1 || reports[0].Event != "started" || reports[0].Attempt != 2 {
		t.Fatalf("initial reports = %+v, err=%v", reports, err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(settledLine); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	reports, err = cursor.poll()
	if err != nil || len(reports) != 1 || reports[0].Event != "settled" || reports[0].Outcome != "stale_fenced" {
		t.Fatalf("incremental reports = %+v, err=%v", reports, err)
	}
	if reports, err := cursor.poll(); err != nil || len(reports) != 0 {
		t.Fatalf("cursor rescanned prior reports = %+v, err=%v", reports, err)
	}
}

func TestChunkLifecycleActiveSetRejectsAlreadySettledAttempt(t *testing.T) {
	started := generationscheduler.ChunkLifecycleReport{
		Schema: generationscheduler.ChunkLifecycleSchema, Event: "started",
		Identity: "sha256:" + strings.Repeat("a", 64), Stage: "extraction-partitions",
		Generation: "sha256:" + strings.Repeat("b", 64), Attempt: 2, Outcome: "running",
	}
	settled := started
	settled.Event, settled.Outcome = "settled", "completed"
	active := make(map[string]generationscheduler.ChunkLifecycleReport)
	updateActiveChunkLifecycles(active, []generationscheduler.ChunkLifecycleReport{started, settled})
	if len(active) != 0 {
		t.Fatalf("settled historical attempt remained selectable: active=%v", active)
	}
}

func TestRunningLeaseMatchRequiresExactAuthoritativeAttempt(t *testing.T) {
	report := generationscheduler.ChunkLifecycleReport{
		Schema: generationscheduler.ChunkLifecycleSchema, Event: "started",
		Identity: "sha256:" + strings.Repeat("a", 64), Stage: "extraction-partitions",
		Generation: "sha256:" + strings.Repeat("b", 64), Attempt: 2, Outcome: "running",
	}
	state := store.GenerationChunkLeaseState{
		Identity: report.Identity, Repository: "example.invalid/semantic",
		Stage: report.Stage, Generation: report.Generation, Attempt: report.Attempt,
		Status: store.GenerationChunkRunning,
	}
	if !runningLeaseMatchesReport(state, report, state.Repository) {
		t.Fatal("exact running lease did not match its discovery report")
	}
	state.Status = store.GenerationChunkDone
	if runningLeaseMatchesReport(state, report, state.Repository) {
		t.Fatal("settled attempt remained authoritative for supersession")
	}
	state.Status, state.Attempt = store.GenerationChunkRunning, report.Attempt+1
	if runningLeaseMatchesReport(state, report, state.Repository) {
		t.Fatal("different running attempt matched the discovery report")
	}
}

func TestRecoveryHelpersRequireLiveBackupBeforeOfflineRestore(t *testing.T) {
	workspace := t.TempDir()
	profileRoot := filepath.Join(workspace, "semantic")
	dataDir := filepath.Join(profileRoot, "data")
	for _, path := range []string{profileRoot, dataDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(profileRoot, "phebs.yaml")
	if err := os.WriteFile(configPath, []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(profileRoot, "live")
	if err := os.WriteFile(marker, []byte("live\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(workspace, "fake-phebs")
	scriptBody := `#!/bin/sh
set -eu
mode="$1"
shift
if [ "$mode" = "backup" ]; then
  test -f "$T4013_LIVE_MARKER"
  output=""
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "-output" ]; then
      output="$2"
      break
    fi
    shift
  done
  test -n "$output"
  mkdir "$output"
  printf 'backup\n' > "$output/artifact"
elif [ "$mode" = "restore" ]; then
  test ! -f "$T4013_LIVE_MARKER"
  mkdir "$T4013_DATA_DIR"
  printf 'restored\n' > "$T4013_DATA_DIR/restored"
else
  exit 2
fi
`
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("T4013_LIVE_MARKER", marker)
	t.Setenv("T4013_DATA_DIR", dataDir)
	profile := PreparedProfile{Config: configPath, DataDir: dataDir}
	toolchain := privateToolchain{Phebs: script}
	backup, backupMetrics, err := createLiveBackup(
		t.Context(), toolchain, profile, workspace, "test",
	)
	if err != nil || backupMetrics.OtherChildren < 1 {
		t.Fatalf("live backup = %+v, metrics=%+v, err=%v", backup, backupMetrics, err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	restoreMetrics, err := restoreBackup(
		t.Context(), toolchain, profile, workspace, backup, "test",
	)
	if err != nil || restoreMetrics.OtherChildren < 1 {
		t.Fatalf("offline restore metrics=%+v, err=%v", restoreMetrics, err)
	}
	if _, err := os.Lstat(filepath.Join(dataDir, "restored")); err != nil {
		t.Fatalf("restored data is absent: %v", err)
	}
	if _, err := os.Lstat(dataDir + ".prior-test"); !os.IsNotExist(err) {
		t.Fatalf("prior data survived successful restore: %v", err)
	}
}

func TestChunkLifecycleCursorDrainsSettledReportThroughCurrentEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	startedLine := `generation chunk lifecycle: {"schema":"phebs-generation-chunk-lifecycle-v1","event":"started","identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","stage":"extraction-partitions","generation":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","attempt":2,"outcome":"running"}` + "\n"
	settledLine := `generation chunk lifecycle: {"schema":"phebs-generation-chunk-lifecycle-v1","event":"settled","identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","stage":"extraction-partitions","generation":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","attempt":2,"outcome":"completed"}` + "\n"
	raw := []byte(startedLine + strings.Repeat("ordinary log line\n", 5_000) + settledLine)
	if len(raw) <= 64<<10 {
		t.Fatal("test log did not cross the cursor read block")
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	reports, err := cursor.poll()
	if err != nil || len(reports) != 2 || reports[0].Event != "started" || reports[1].Event != "settled" {
		t.Fatalf("drained reports = %+v, %v", reports, err)
	}
	active := make(map[string]generationscheduler.ChunkLifecycleReport)
	updateActiveChunkLifecycles(active, reports)
	if len(active) != 0 {
		t.Fatalf("settled report beyond first block left a false live lease: %v", active)
	}
}
