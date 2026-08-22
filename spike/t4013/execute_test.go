package t4013

import (
	"context"
	"errors"
	"fmt"
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

	apiresponse "github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t401"
)

func TestFinalizeObservationPinsEveryEnabledExtractionDomain(t *testing.T) {
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	var semantic t401.Profile
	for _, profile := range profiles {
		if profile.Kind == "semantic" {
			semantic = profile
		}
	}
	if semantic.Name == "" {
		t.Fatal("frozen semantic profile is absent")
	}
	wantFacts := int64(semantic.Aggregate.ExtractorFacts)
	for _, family := range semantic.Families {
		switch family.Name {
		case "go_kafka_literal", "go_dynamic_topic":
			wantFacts += int64(family.Inputs * family.RecordsPerInput)
		}
	}
	if wantFacts != frozenSemanticExtractionFacts || 2*wantFacts != frozenSemanticExtractionRows {
		t.Fatalf("derived extraction oracle = %d/%d, constants = %d/%d",
			wantFacts, 2*wantFacts, frozenSemanticExtractionFacts, frozenSemanticExtractionRows)
	}

	validRun := func() *execution {
		return &execution{
			structAR: privateProfileSnapshot{
				Name: "structural-2m-v1", ObservationRecords: 512,
				PublishedDomains: frozenExtractorDomainCount,
			},
			semanticA: privateProfileSnapshot{
				Name: "semantic-262144-v1", ObservationRecords: 262_144,
				ObservationUnsupported: 131_072, PublishedDomains: frozenExtractorDomainCount,
				ExtractionFacts: frozenSemanticExtractionFacts, ExtractionRows: frozenSemanticExtractionRows,
			},
			observation: Observation{Checks: make([]CheckObservation, 1)},
		}
	}
	if err := validRun().finalizeObservation(); err != nil {
		t.Fatalf("complete-domain oracle refused: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*execution)
	}{
		{
			name: "stale IDL-only totals",
			mutate: func(run *execution) {
				run.semanticA.ExtractionFacts = int64(semantic.Aggregate.ExtractorFacts)
				run.semanticA.ExtractionRows = int64(semantic.Aggregate.ExtractorStagingRows)
			},
		},
		{
			name: "missing enabled domain",
			mutate: func(run *execution) {
				run.semanticA.PublishedDomains--
			},
		},
		{
			name: "unexpected structural fact",
			mutate: func(run *execution) {
				run.structAR.ExtractionFacts = 1
				run.structAR.ExtractionRows = 2
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := validRun()
			test.mutate(run)
			if err := run.finalizeObservation(); !errors.Is(err, errExactOracle) {
				t.Fatalf("oracle error = %v, want %v", err, errExactOracle)
			}
		})
	}
}

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
			privateConvergenceProbe{Stage: "repository_visibility", SHA256: digest},
			convergenceInspectionDiagnostic{class: class}, time.Duration(index)*time.Millisecond,
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
			convergenceProbe("extraction_publication", index),
			convergenceInspectionDiagnostic{class: class}, time.Duration(index+1)*time.Millisecond,
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

func TestTerminalProgressSealsThroughStoppedReceipt(t *testing.T) {
	hostToolchain, err := ObserveHostToolchain(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := frozenV15PlanWithHostToolchain(testSourceCommit, hostToolchain)
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	v16Plan, err := frozenV16PlanWithHostToolchain(testSourceCommit, hostToolchain)
	if err != nil {
		t.Fatal(err)
	}
	v16PlanBytes, err := MarshalPlan(v16Plan)
	if err != nil {
		t.Fatal(err)
	}
	v20Plan, err := frozenV20PlanWithHostToolchain(testSourceCommit, hostToolchain)
	if err != nil {
		t.Fatal(err)
	}
	v20PlanBytes, err := MarshalPlan(v20Plan)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	v20CallerTotal := 8
	v20CallerProbe, v20CallerErr := classifyCallerGeneration(
		apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
			State: "missing", PartitionProgress: &apiresponse.CallerMapPartitionProgress{
				State: "complete", SettledPairCount: v20CallerTotal,
				SucceededPairCount: v20CallerTotal, TotalPairCount: &v20CallerTotal,
			},
		}},
		callerJobProbe{},
		profileInspectionV20,
	)
	if !errors.Is(v20CallerErr, errCallerPublicationMissing) {
		t.Fatalf("v20 missing caller publication = %v, want terminal", v20CallerErr)
	}
	tests := []struct {
		name          string
		cause         error
		outcome       string
		historicalV14 bool
		v16           bool
		v20           bool
		failureClass  string
		confirmations int64
		attempts      int64
		projection    privateConvergenceProbe
	}{
		{
			name: "observation bound refusal", cause: errObservationBoundRefusal,
			outcome: "observation_bound_refusal",
			projection: privateConvergenceProbe{
				Stage: "observation_publication", SHA256: digest,
				ObservationProgress: &ObservationProgressObservation{
					State: "failed", SelectedVersion: "v2", PlanningState: "failed", PlanningFailed: 1,
					RefusalStage:          string(pipelinerefusal.StageObservationPublication),
					RefusalGenerationKind: string(pipelinerefusal.GenerationObservation),
					RefusalClassification: string(pipelinerefusal.ClassificationLimit),
					RefusalDimension:      string(pipelinerefusal.DimensionGenerationRecords),
					RefusalObserved:       262_144, RefusalLimit: observationpublication.MaxGenerationRecords,
				},
			},
		},
		{
			name: "extraction bound refusal", cause: errExtractionBoundRefusal,
			outcome: "extraction_bound_refusal",
			projection: privateConvergenceProbe{
				Stage: "extraction_publication", SHA256: digest,
				ExtractionProgress: &ExtractionProgressObservation{
					State: "unavailable", JobState: "failed", JobAttempts: 1,
					RefusalStage:          string(pipelinerefusal.StageDomainInventory),
					RefusalGenerationKind: string(pipelinerefusal.GenerationExtractionDomain),
					RefusalClassification: string(pipelinerefusal.ClassificationLimit),
					RefusalDimension:      string(pipelinerefusal.DimensionCandidateMemberBytes),
					RefusalObserved:       792_000_000, RefusalLimit: 67_108_864,
				},
			},
		},
		{
			name: "extraction job terminal with unavailable schedule", cause: errExtractionJobTerminal,
			outcome: "extraction_job_terminal",
			projection: privateConvergenceProbe{
				Stage: "extraction_publication", SHA256: digest,
				ExtractionProgress: &ExtractionProgressObservation{
					State: "unavailable", JobState: string(store.StatusFailed), JobAttempts: 2,
				},
			},
		},
		{
			name: "historical v14 failed job supersedes current schedule", cause: errExtractionJobTerminal,
			outcome: "extraction_job_terminal", historicalV14: true,
			projection: privateConvergenceProbe{
				Stage: "extraction_publication", SHA256: digest,
				ExtractionProgress: &ExtractionProgressObservation{
					State: "current", Total: 32, Materialized: 32, Succeeded: 32,
					Domains: 4, CurrentDomains: 4,
					JobState: string(store.StatusFailed), JobAttempts: 2,
				},
			},
		},
		{
			name: "historical v14 bound refusal supersedes current schedule", cause: errExtractionBoundRefusal,
			outcome: "extraction_bound_refusal", historicalV14: true,
			projection: privateConvergenceProbe{
				Stage: "extraction_publication", SHA256: digest,
				ExtractionProgress: &ExtractionProgressObservation{
					State: "current", Total: 32, Materialized: 32, Succeeded: 32,
					Domains: 4, CurrentDomains: 4,
					JobState: string(store.StatusFailed), JobAttempts: 2,
					RefusalStage:          string(pipelinerefusal.StageDomainInventory),
					RefusalGenerationKind: string(pipelinerefusal.GenerationExtractionDomain),
					RefusalClassification: string(pipelinerefusal.ClassificationLimit),
					RefusalDimension:      string(pipelinerefusal.DimensionCandidateMemberBytes),
					RefusalObserved:       2, RefusalLimit: 1,
				},
			},
		},
		{
			name:  "extraction bound refusal supersedes failed schedule",
			cause: errExtractionBoundRefusal, outcome: "extraction_bound_refusal",
			projection: privateConvergenceProbe{
				Stage: "extraction_publication", SHA256: digest,
				ExtractionProgress: &ExtractionProgressObservation{
					State: string(store.GenerationScheduleSettled), Total: 32, Materialized: 32,
					Failed: 32, Domains: 4, JobState: "failed", JobAttempts: 1,
					RefusalStage:          string(pipelinerefusal.StageDomainInventory),
					RefusalGenerationKind: string(pipelinerefusal.GenerationExtractionDomain),
					RefusalClassification: string(pipelinerefusal.ClassificationLimit),
					RefusalDimension:      string(pipelinerefusal.DimensionCandidateMemberBytes),
					RefusalObserved:       792_000_000, RefusalLimit: 67_108_864,
				},
			},
		},
		{
			name: "extraction schedule terminal", cause: errExtractionScheduleTerminal,
			outcome: "extraction_schedule_terminal",
			projection: privateConvergenceProbe{
				Stage: "extraction_publication", SHA256: digest,
				ExtractionProgress: &ExtractionProgressObservation{
					State: string(store.GenerationScheduleSettled), Total: 32, Materialized: 32,
					Failed: 32, Domains: 4, JobState: string(store.StatusDone), JobAttempts: 1,
				},
			},
		},
		{
			name:  "extraction schedule terminal supersedes unbound failed job",
			cause: errExtractionScheduleTerminal, outcome: "extraction_schedule_terminal",
			projection: privateConvergenceProbe{
				Stage: "extraction_publication", SHA256: digest,
				ExtractionProgress: &ExtractionProgressObservation{
					State: string(store.GenerationScheduleSettled), Total: 32, Materialized: 32,
					Failed: 32, Domains: 4, JobState: string(store.StatusFailed), JobAttempts: 2,
				},
			},
		},
		{
			name: "caller generation bound refusal", cause: errCallerGenerationBoundRefusal,
			outcome: "caller_generation_bound_refusal",
			projection: privateConvergenceProbe{
				Stage: "caller_generation", SHA256: digest,
			},
		},
		{
			name: "caller generation terminal", cause: errCallerGenerationTerminal,
			outcome: "caller_generation_terminal",
			projection: privateConvergenceProbe{
				Stage: "caller_generation", SHA256: digest,
			},
		},
		{
			name: "v20 confirmed missing caller publication", cause: v20CallerErr,
			outcome: "caller_generation_terminal", v20: true, attempts: 2,
			projection: v20CallerProbe,
		},
		{
			name: "relationship bound refusal", cause: errRelationshipBoundRefusal,
			outcome: "relationship_bound_refusal",
			projection: privateConvergenceProbe{
				Stage: "relationship_publication", SHA256: digest,
			},
		},
		{
			name: "relationship terminal", cause: errRelationshipTerminal,
			outcome: "relationship_terminal",
			projection: privateConvergenceProbe{
				Stage: "relationship_publication", SHA256: digest,
			},
		},
		{
			name: "v16 relationship pair terminal", cause: errRelationshipTerminal,
			outcome: "relationship_terminal", v16: true,
			failureClass: relationshipFailureAuthorityDrift, confirmations: 2, attempts: 2,
			projection: privateConvergenceProbe{
				Stage: "relationship_publication", SHA256: digest,
				RelationshipFailureClass: relationshipFailureAuthorityDrift,
			},
		},
		{
			name: "v16 relationship pair bound refusal", cause: errRelationshipBoundRefusal,
			outcome: "relationship_bound_refusal", v16: true,
			failureClass: relationshipFailureSuccessorAbsent, attempts: 1,
			projection: privateConvergenceProbe{
				Stage: "relationship_publication", SHA256: digest,
				RelationshipFailureClass: relationshipFailureSuccessorAbsent,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selectedPlan, selectedPlanBytes := plan, planBytes
			if test.v20 {
				selectedPlan, selectedPlanBytes = v20Plan, v20PlanBytes
			} else if test.v16 {
				selectedPlan, selectedPlanBytes = v16Plan, v16PlanBytes
			} else if test.historicalV14 {
				selectedPlan, err = frozenV14PlanWithHostToolchain(testSourceCommit, hostToolchain)
				if err != nil {
					t.Fatal(err)
				}
				selectedPlanBytes, err = MarshalPlan(selectedPlan)
				if err != nil {
					t.Fatal(err)
				}
			}
			root := t.TempDir()
			module, workspace := filepath.Join(root, "module"), filepath.Join(root, "custody")
			for _, path := range []string{module, workspace} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			logPath := filepath.Join(workspace, "server.log")
			if err := os.WriteFile(logPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			run := &execution{
				ctx: t.Context(), moduleRoot: module, workspace: workspace, plan: selectedPlan,
				observation: emptyObservationForPlan(EnvironmentObservation{
					OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
					FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
				}, selectedPlan),
			}
			run.observation.Toolchain = []ToolchainObservation{
				{Name: "phebs", SHA256: digest}, {Name: "zoekt-git-index", SHA256: digest},
				{Name: "phebs-focused-index", SHA256: digest}, {Name: "buf", SHA256: digest},
			}
			run.observation.Phases[0] = succeededPhase("preflight", PhaseMetrics{WallMS: 1})
			run.startPhase(1)
			profile := PreparedProfile{Name: "semantic-262144-v1"}
			server := &privateServer{done: make(chan error, 1), logPath: logPath}
			_, waitErr := run.waitSnapshotWithInspection(
				profile, "a", "cold", time.Duration(selectedPlan.Safety.FullConvergenceDeadlineMS)*time.Millisecond, server,
				func(context.Context) (privateProfileSnapshot, privateConvergenceProbe, error) {
					return privateProfileSnapshot{}, test.projection, test.cause
				},
			)
			wantWaitErr := test.cause
			if test.v20 {
				wantWaitErr = errCallerGenerationTerminal
			}
			if !errors.Is(waitErr, wantWaitErr) {
				t.Fatalf("terminal wait = %v", waitErr)
			}
			if len(run.observation.ConvergenceWaits) != 1 ||
				run.observation.ConvergenceWaits[0].Outcome != test.outcome {
				t.Fatalf("terminal waits = %+v", run.observation.ConvergenceWaits)
			}
			if test.v20 && run.observation.ConvergenceWaits[0].Attempts != test.attempts {
				t.Fatalf("v20 caller confirmation = %+v", run.observation.ConvergenceWaits[0])
			}
			if test.v16 &&
				(run.observation.ConvergenceWaits[0].RelationshipFailureClass != test.failureClass ||
					run.observation.ConvergenceWaits[0].RelationshipTerminalConfirmations != test.confirmations ||
					run.observation.ConvergenceWaits[0].Attempts != test.attempts) {
				t.Fatalf("v16 relationship confirmation = %+v", run.observation.ConvergenceWaits[0])
			}
			if test.v16 && validateConvergenceWaits(
				run.observation.ConvergenceWaits, observationDetailV15,
			) == nil {
				t.Fatal("v15 contract accepted v16 relationship evidence")
			}
			if test.outcome == "extraction_job_terminal" && !test.historicalV14 {
				for _, progress := range v15InconclusiveTerminalJobProgress() {
					invalid := run.observation.ConvergenceWaits[0]
					invalid.ExtractionProgress = &progress
					if err := validateConvergenceWaits([]ConvergenceWaitObservation{invalid}, 11); err == nil {
						t.Fatalf("V15 job terminal accepted schedule state %q", progress.State)
					}
				}
			}
			if test.outcome == "extraction_bound_refusal" && !test.historicalV14 {
				for _, progress := range v15InconclusiveExtractionBoundProgress() {
					invalid := run.observation.ConvergenceWaits[0]
					invalid.ExtractionProgress = &progress
					if err := validateConvergenceWaits([]ConvergenceWaitObservation{invalid}, 11); err == nil {
						t.Fatalf("V15 bound refusal accepted schedule state %q", progress.State)
					}
				}
			}
			stopped, stopErr := run.stopAfterFailure(waitErr)
			if stopErr != nil {
				t.Fatal(stopErr)
			}
			if err := ValidateObservation(stopped); err != nil {
				t.Fatalf("stopped observation did not seal: %v", err)
			}
			receiptBytes, err := BuildReceipt(
				selectedPlanBytes, marshal(t, stopped), PlanDigest(selectedPlanBytes),
			)
			if err != nil {
				t.Fatalf("stopped receipt did not seal: %v", err)
			}
			receipt, err := DecodeReceipt(receiptBytes, selectedPlan)
			if err != nil {
				t.Fatalf("stopped receipt did not decode: %v", err)
			}
			if receipt.Outcome != stopped.Outcome || len(receipt.Failures) != 1 ||
				receipt.Failures[0] != stopped.Failures[0] || receipt.Decision != stopped.Decision ||
				len(receipt.ConvergenceWaits) != 1 || receipt.ConvergenceWaits[0].Outcome != test.outcome {
				t.Fatalf("stopped receipt parity = %+v", receipt)
			}
			if test.v16 {
				wait := receipt.ConvergenceWaits[0]
				if wait.RelationshipFailureClass != test.failureClass ||
					wait.RelationshipTerminalConfirmations != test.confirmations {
					t.Fatalf("v16 relationship receipt parity = %+v", wait)
				}
				if test.outcome == "relationship_terminal" {
					invalid := stopped
					invalid.ConvergenceWaits = append(
						[]ConvergenceWaitObservation(nil), stopped.ConvergenceWaits...,
					)
					invalid.ConvergenceWaits[0].RelationshipTerminalConfirmations = 1
					if _, err := BuildReceipt(
						selectedPlanBytes, marshal(t, invalid), PlanDigest(selectedPlanBytes),
					); err == nil {
						t.Fatal("v16 receipt accepted an unconfirmed relationship terminal")
					}
				}
			}
			if test.projection.Stage == "caller_generation" {
				if err := validateConvergenceWaits(
					run.observation.ConvergenceWaits, 9,
				); err == nil {
					t.Fatal("pre-v14 convergence contract accepted caller terminal state")
				}
			}
		})
	}
}

// v15InconclusiveTerminalJobProgress enumerates the schedule states beside
// which a terminal job row must NOT classify extraction_job_terminal under
// the V15 contract. Shared so the runtime, seal, and predicate assertions
// exercise one fixture.
func v15InconclusiveTerminalJobProgress() []ExtractionProgressObservation {
	return []ExtractionProgressObservation{
		{
			State: string(store.GenerationScheduleActive), Total: 1, Materialized: 1,
			Pending: 1, Domains: 1,
			JobState: string(store.StatusFailed), JobAttempts: 2,
		},
		{
			State: "current", Total: 1, Materialized: 1, Succeeded: 1,
			Domains: 1, CurrentDomains: 1,
			JobState: string(store.StatusFailed), JobAttempts: 2,
		},
	}
}

func v15InconclusiveExtractionBoundProgress() []ExtractionProgressObservation {
	progress := v15InconclusiveTerminalJobProgress()
	for index := range progress {
		progress[index].RefusalStage = string(pipelinerefusal.StageDomainInventory)
		progress[index].RefusalGenerationKind = string(pipelinerefusal.GenerationExtractionDomain)
		progress[index].RefusalClassification = string(pipelinerefusal.ClassificationLimit)
		progress[index].RefusalDimension = string(pipelinerefusal.DimensionCandidateMemberBytes)
		progress[index].RefusalObserved = 2
		progress[index].RefusalLimit = 1
	}
	return progress
}

func TestDiagnosticLimitRetainsTerminalJobProjectionAndSeals(t *testing.T) {
	tracker := convergenceProgressTracker{coalesceTransitionProgress: true}
	for index := range maxConvergenceTransitions {
		stage := "observation_publication"
		if index%2 == 1 {
			stage = "extraction_publication"
		}
		tracker.observe(
			privateConvergenceProbe{Stage: stage, SHA256: fmt.Sprintf("sha256:%064d", index)},
			convergenceInspectionDiagnostic{class: "pending"},
			time.Duration(index+1)*time.Millisecond,
		)
	}
	terminal := ExtractionProgressObservation{
		State: "unavailable", JobState: string(store.StatusFailed), JobAttempts: 2,
	}
	if !tracker.observe(
		privateConvergenceProbe{
			Stage: "extraction_publication", SHA256: "sha256:" + strings.Repeat("f", 64),
			ExtractionProgress: &terminal,
		},
		convergenceInspectionDiagnostic{class: "terminal"},
		time.Duration(maxConvergenceTransitions+2)*time.Millisecond,
	) {
		t.Fatal("transition limit was not exceeded")
	}
	run := &execution{plan: Plan{Schema: PlanSchemaV15}}
	run.recordConvergenceWait(
		PreparedProfile{Name: "semantic-262144-v1"}, "a", "cold",
		"diagnostic_limit", time.Minute, time.Now().Add(-time.Second), tracker,
	)
	if len(run.observation.ConvergenceWaits) != 1 {
		t.Fatalf("wait inventory = %+v", run.observation.ConvergenceWaits)
	}
	wait := run.observation.ConvergenceWaits[0]
	if wait.ExtractionProgress == nil ||
		wait.ExtractionProgress.JobState != string(store.StatusFailed) {
		t.Fatalf("diagnostic-limit wait dropped the terminal projection: %+v", wait)
	}
	// A terminal-shaped final projection is evidence, not an outcome claim:
	// coherence checks are outcome-restricted, and historical V12-V14
	// receipts keep their pre-V15 validation.
	for _, detail := range []int{10, observationDetailV15} {
		if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, detail); err != nil {
			t.Fatalf("diagnostic-limit wait with terminal job projection (detail %d) = %v", detail, err)
		}
	}
}

func TestV15ExtractionJobPlaneTerminalRequiresConclusiveScheduleAfterSuccessfulProbe(t *testing.T) {
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	active := ExtractionProgressObservation{
		State: string(store.GenerationScheduleActive), Total: 1, Materialized: 1,
		Pending: 1, Domains: 1, JobState: string(store.StatusRunning), JobAttempts: 1,
	}
	tests := []struct {
		name         string
		outcome      string
		terminal     ExtractionProgressObservation
		inconclusive []ExtractionProgressObservation
	}{
		{
			name: "job terminal", outcome: "extraction_job_terminal",
			terminal: ExtractionProgressObservation{
				State: "unavailable", JobState: string(store.StatusFailed), JobAttempts: 2,
			},
			inconclusive: v15InconclusiveTerminalJobProgress(),
		},
		{
			name: "bound refusal", outcome: "extraction_bound_refusal",
			terminal: ExtractionProgressObservation{
				State: "unavailable", JobState: string(store.StatusFailed), JobAttempts: 2,
				RefusalStage:          string(pipelinerefusal.StageDomainInventory),
				RefusalGenerationKind: string(pipelinerefusal.GenerationExtractionDomain),
				RefusalClassification: string(pipelinerefusal.ClassificationLimit),
				RefusalDimension:      string(pipelinerefusal.DimensionCandidateMemberBytes),
				RefusalObserved:       2, RefusalLimit: 1,
			},
			inconclusive: v15InconclusiveExtractionBoundProgress(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tracker := convergenceProgressTracker{coalesceTransitionProgress: true}
			tracker.observe(
				privateConvergenceProbe{
					Stage: "extraction_publication", SHA256: digestA, ExtractionProgress: &active,
				},
				convergenceInspectionDiagnostic{class: "pending"},
				time.Millisecond,
			)
			tracker.observe(
				privateConvergenceProbe{
					Stage: "extraction_publication", SHA256: digestB,
					ExtractionProgress: &test.terminal,
				},
				convergenceInspectionDiagnostic{class: "terminal"},
				2*time.Millisecond,
			)
			run := &execution{plan: Plan{Schema: PlanSchemaV15}}
			run.recordConvergenceWait(
				PreparedProfile{Name: "semantic-262144-v1"}, "a", "cold",
				test.outcome, time.Minute, time.Now().Add(-3*time.Millisecond), tracker,
			)
			if len(run.observation.ConvergenceWaits) != 1 {
				t.Fatalf("wait inventory = %+v", run.observation.ConvergenceWaits)
			}
			wait := run.observation.ConvergenceWaits[0]
			if wait.LastStage != "extraction_publication" || wait.LastInspectionClass != "terminal" ||
				wait.ExtractionProgress == nil || wait.ExtractionProgress.State != "unavailable" {
				t.Fatalf("successful-probe terminal wait = %+v", wait)
			}
			if err := validateConvergenceWaits([]ConvergenceWaitObservation{wait}, 11); err != nil {
				t.Fatalf("V15 unavailable-schedule terminal wait = %v", err)
			}
			for _, progress := range test.inconclusive {
				invalid := wait
				invalid.ExtractionProgress = &progress
				if err := validateConvergenceWaits([]ConvergenceWaitObservation{invalid}, 11); err == nil {
					t.Fatalf("V15 successful-probe %s accepted schedule state %q", test.outcome, progress.State)
				}
			}
		})
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
		{name: "observation bound refusal", cause: errObservationBoundRefusal, code: "observation_production_bound_refused", decision: "reduce", substantiated: true},
		{name: "observation terminal", cause: errObservationTerminal, code: "observation_terminal", decision: "unclassified"},
		{name: "extraction bound refusal", cause: errExtractionBoundRefusal, code: "extraction_production_bound_refused", decision: "reduce", substantiated: true},
		{name: "extraction job terminal", cause: errExtractionJobTerminal, code: "extraction_job_terminal", decision: "unclassified"},
		{name: "extraction schedule terminal", cause: errExtractionScheduleTerminal, code: "extraction_schedule_terminal", decision: "unclassified"},
		{name: "caller generation bound refusal", cause: errCallerGenerationBoundRefusal, code: "caller_generation_production_bound_refused", decision: "reduce", substantiated: true},
		{name: "caller generation terminal", cause: errCallerGenerationTerminal, code: "caller_generation_terminal", decision: "unclassified"},
		{name: "relationship bound refusal", cause: errRelationshipBoundRefusal, code: "relationship_production_bound_refused", decision: "reduce", substantiated: true},
		{name: "relationship terminal", cause: errRelationshipTerminal, code: "relationship_terminal", decision: "unclassified"},
		{name: "interruption trigger unsatisfiable", cause: errInterruptionTriggerUnsatisfiable, code: "interruption_trigger_unsatisfiable", decision: "unclassified"},
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

func TestV23StoppedFailureClassificationPreservesRecoveryAndDiagnostics(t *testing.T) {
	plan := Plan{Schema: PlanSchemaV23}
	for _, test := range []struct {
		name     string
		cause    error
		code     string
		decision string
	}{
		{
			name:  "direct recovery outranks pipeline terminal",
			cause: directRecovery(errRelationshipTerminal),
			code:  "direct_recovery_failed", decision: "p6_investigation",
		},
		{
			name:  "direct recovery recognizes interruption pipeline terminal",
			cause: directRecovery(errInterruptionProgressTerminal),
			code:  "direct_recovery_failed", decision: "p6_investigation",
		},
		{
			name:  "direct recovery outranks a V23 lifecycle sentinel",
			cause: directRecovery(errLifecycleCycleDeadline),
			code:  "direct_recovery_failed", decision: "p6_investigation",
		},
		{
			name:  "direct recovery outranks a V23 query sentinel",
			cause: directRecovery(errAuthorizedQuery),
			code:  "direct_recovery_failed", decision: "p6_investigation",
		},
		{
			name: "collection lifecycle deadline", cause: errLifecycleCycleDeadline,
			code: "lifecycle_cycle_deadline_expired", decision: "cohort_experiment",
		},
		{
			name:  "pressure lifecycle deadline",
			cause: errors.Join(errLifecycleCycleDeadline, errProductionPressure),
			code:  "production_pressure_gate_refused", decision: "reduce",
		},
		{
			name: "pressure recovery deadline", cause: errPressureRecoveryDeadline,
			code: "pressure_recovery_deadline_expired", decision: "unclassified",
		},
		{
			name: "authorized query", cause: errAuthorizedQuery,
			code: "authorized_query_failed", decision: "unclassified",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := classifyStoppedFailureForPlan(plan, test.cause, nil, nil)
			if got.code != test.code || got.decision != test.decision {
				t.Fatalf("classification = %+v", got)
			}
		})
	}
	historical := classifyStoppedFailureForPlan(
		Plan{Schema: PlanSchemaV22}, directRecovery(errRelationshipTerminal), nil, nil,
	)
	if historical.code != "relationship_terminal" {
		t.Fatalf("V22 classification changed: %+v", historical)
	}
	unmeasured := classifyStoppedFailureForPlan(
		plan, errAuthorizedQuery, errors.New("meter failed"), nil,
	)
	if unmeasured.code != "failed_phase_measurement_unavailable" {
		t.Fatalf("V23 diagnostics overrode missing measurement: %+v", unmeasured)
	}
	ceiling := classifyStoppedFailureForPlan(
		plan, directRecovery(errRelationshipTerminal), nil, errTotalWallDeadline,
	)
	if ceiling.code != "review_ceiling_crossed" {
		t.Fatalf("V23 recovery overrode the total ceiling: %+v", ceiling)
	}
	queryCeiling := classifyStoppedFailureForPlan(
		plan, errAuthorizedQuery, nil, errReviewCeiling,
	)
	if queryCeiling.code != "review_ceiling_crossed" {
		t.Fatalf("V23 query diagnostics overrode the review ceiling: %+v", queryCeiling)
	}
}

func TestV23LifecycleWaitTypesOnlyItsOwnDeadline(t *testing.T) {
	credential := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credential, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	profile := PreparedProfile{
		Credential: credential, Address: strings.TrimPrefix(server.URL, "http://"),
	}
	if _, err := waitLifecycleForPlan(
		t.Context(), Plan{Schema: PlanSchemaV23}, profile, true, 20*time.Millisecond,
	); !errors.Is(err, errLifecycleCycleDeadline) {
		t.Fatalf("V23 lifecycle deadline = %v", err)
	}
	if _, err := waitLifecycleForPlan(
		t.Context(), Plan{Schema: PlanSchemaV22}, profile, true, 20*time.Millisecond,
	); errors.Is(err, errLifecycleCycleDeadline) {
		t.Fatalf("V22 lifecycle wait acquired V23 sentinel: %v", err)
	}
	if _, err := waitLifecyclePressureForPlan(
		t.Context(), Plan{Schema: PlanSchemaV23}, profile, true, lifecycle.PressureNormal,
		pressureWaitRecover, 20*time.Millisecond,
	); !errors.Is(err, errPressureRecoveryDeadline) || errors.Is(err, errProductionPressure) {
		t.Fatalf("V23 pressure recovery deadline = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := waitLifecycleForPlan(
		canceled, Plan{Schema: PlanSchemaV23}, profile, true, time.Second,
	); !errors.Is(err, context.Canceled) || errors.Is(err, errLifecycleCycleDeadline) {
		t.Fatalf("canceled V23 lifecycle wait = %v", err)
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

func TestCompletedObservationIsPrevalidatedBeforeCustodyDeletion(t *testing.T) {
	observation := completedObservation()
	observation.Phases[len(observation.Phases)-1] = PhaseObservation{
		Name: "teardown", Outcome: "not_run",
	}
	observation.Teardown = TeardownObservation{}
	run := &execution{observation: observation}
	if err := run.validateCompletedObservationBeforeTeardown(); err != nil {
		t.Fatalf("valid completed shape refused before teardown: %v", err)
	}
	run.observation.AuthorizedQuery = &AuthorizedQueryObservation{
		Schema: authorizedQuerySchemaV1, Profile: "semantic-262144-v1",
		Query: "search", Class: "status", Status: 503, Attempts: 1,
	}
	if err := run.validateCompletedObservationBeforeTeardown(); err == nil {
		t.Fatal("unsealable completed shape reached destructive teardown")
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

func TestV23AuthorizedQueryRetriesAndRetainsSourceFreeFailure(t *testing.T) {
	missingProfile := PreparedProfile{
		Name: "semantic-262144-v1", RepositoryName: "github.com/example/repo",
		Credential: filepath.Join(t.TempDir(), "absent"), Address: "127.0.0.1:1",
	}
	if _, _, failure, err := queryProfileV23(
		t.Context(), missingProfile, "semantic", true,
	); err == nil || failure != nil || errors.Is(err, errAuthorizedQuery) {
		t.Fatalf("local setup failure was mislabeled as a query: failure=%+v err=%v", failure, err)
	}
	credential := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credential, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") == "" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		searchCalls++
		response.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()
	profile := PreparedProfile{
		Name: "semantic-262144-v1", RepositoryName: "github.com/example/repo",
		Credential: credential, Address: strings.TrimPrefix(server.URL, "http://"),
	}
	inspector, err := newProfileInspector(profile, profileInspectionLegacy)
	if err != nil {
		t.Fatal(err)
	}
	runner := &authorizedQueryRunner{
		inspector: inspector, profile: profile, maxAttempts: 3,
		retryDelay: time.Millisecond, retainFailure: true,
	}
	_, _, err = queryProfileWithRunner(t.Context(), runner, "semantic", true)
	if !errors.Is(err, errAuthorizedQuery) || searchCalls != 3 || runner.failure == nil ||
		runner.failure.Query != "search" || runner.failure.Class != "status" ||
		runner.failure.Status != http.StatusConflict || runner.failure.Attempts != 3 {
		t.Fatalf("calls=%d failure=%+v err=%v", searchCalls, runner.failure, err)
	}

	terminalCalls := 0
	terminal := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") == "" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		terminalCalls++
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer terminal.Close()
	profile.Address = strings.TrimPrefix(terminal.URL, "http://")
	inspector, err = newProfileInspector(profile, profileInspectionLegacy)
	if err != nil {
		t.Fatal(err)
	}
	runner = &authorizedQueryRunner{
		inspector: inspector, profile: profile, maxAttempts: 3,
		retryDelay: time.Millisecond, retainFailure: true,
	}
	_, _, err = queryProfileWithRunner(t.Context(), runner, "semantic", true)
	if !errors.Is(err, errAuthorizedQuery) || terminalCalls != 1 || runner.failure == nil ||
		runner.failure.Query != "search" || runner.failure.Class != "status" ||
		runner.failure.Status != http.StatusServiceUnavailable || runner.failure.Attempts != 1 {
		t.Fatalf("terminal calls=%d failure=%+v err=%v", terminalCalls, runner.failure, err)
	}
	unauthorizedCalls := 0
	unauthorizedFailure := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		unauthorizedCalls++
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unauthorizedFailure.Close()
	profile.Address = strings.TrimPrefix(unauthorizedFailure.URL, "http://")
	inspector, err = newProfileInspector(profile, profileInspectionLegacy)
	if err != nil {
		t.Fatal(err)
	}
	legacy := &authorizedQueryRunner{inspector: inspector, profile: profile, maxAttempts: 1}
	_, _, err = queryProfileWithRunner(t.Context(), legacy, "semantic", true)
	if !errors.Is(err, errExactOracle) || unauthorizedCalls != 1 || legacy.failure != nil {
		t.Fatalf("legacy unauthorized calls=%d failure=%+v err=%v", unauthorizedCalls, legacy.failure, err)
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
	removed := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		removed <- os.Remove(filepath.Join(v2, "publishing.json"))
	}()
	if err := waitForDerivedPartialClear(t.Context(), dataDir, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
}

func TestDerivedPartialScanIgnoresCandidateNamespace(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "extraction-publications", "candidates"), 0o700); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(dataDir, "extraction-publications", strings.Repeat("a", 64))
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	found, err := derivedPartialPresent(dataDir)
	if err != nil || found {
		t.Fatalf("candidate namespace plus one repository = %t, %v", found, err)
	}
}

func TestRecoveryAuthorityIsVersionFenced(t *testing.T) {
	left := privateProfileSnapshot{
		SourceGeneration: "source", SearchGeneration: "search",
		ObservationGeneration: "observation", ExtractionGeneration: "extraction",
		CallerGeneration: "caller", RelationshipGeneration: "relationship-a",
		RelationshipSemanticDigest: "semantic",
	}
	right := left
	right.RelationshipGeneration = "relationship-b"
	if recoveryAuthorityForPlan(Plan{Schema: PlanSchemaV18}, left) ==
		recoveryAuthorityForPlan(Plan{Schema: PlanSchemaV18}, right) {
		t.Fatal("V18 recovery accepted reminted relationship authority")
	}
	if recoveryAuthorityForPlan(Plan{Schema: PlanSchemaV19}, left) !=
		recoveryAuthorityForPlan(Plan{Schema: PlanSchemaV19}, right) {
		t.Fatal("V19 recovery rejected stable relationship semantics")
	}
}

func TestStablePhaseAuthorityIsVersionFenced(t *testing.T) {
	left := privateProfileSnapshot{
		SourceGeneration: "source", SearchGeneration: "search",
		ObservationGeneration: "observation", ExtractionGeneration: "extraction",
		CallerGeneration: "caller", RelationshipGeneration: "relationship-a",
		RelationshipRootDigest: "root-a", RelationshipSemanticDigest: "semantic",
	}
	right := left
	right.RelationshipGeneration = "relationship-b"
	right.RelationshipRootDigest = "root-b"
	if stablePhaseAuthorityForPlan(Plan{Schema: PlanSchemaV22}, left) ==
		stablePhaseAuthorityForPlan(Plan{Schema: PlanSchemaV22}, right) {
		t.Fatal("V22 phase oracle accepted reminted relationship authority")
	}
	if stablePhaseAuthorityForPlan(Plan{Schema: PlanSchemaV23}, left) !=
		stablePhaseAuthorityForPlan(Plan{Schema: PlanSchemaV23}, right) {
		t.Fatal("V23 phase oracle rejected stable relationship semantics")
	}
	right.CallerGeneration = "caller-b"
	if stablePhaseAuthorityForPlan(Plan{Schema: PlanSchemaV23}, left) ==
		stablePhaseAuthorityForPlan(Plan{Schema: PlanSchemaV23}, right) {
		t.Fatal("V23 phase oracle accepted caller authority drift")
	}
}

func TestChunkLifecycleReaderBindsOneStartedAttempt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	startedLine := `generation chunk lifecycle: {"schema":"phebs-generation-chunk-lifecycle-v1","event":"started","identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","stage":"extraction-partitions","generation":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","attempt":2,"outcome":"running"}` + "\n"
	settledLine := `generation chunk lifecycle: {"schema":"phebs-generation-chunk-lifecycle-v1","event":"settled","identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","stage":"extraction-partitions","generation":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","attempt":2,"outcome":"stale_fenced"}` + "\n"
	if err := os.WriteFile(path, []byte(startedLine), 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
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

func TestV17ObservationStartsWithClosedInterruptionState(t *testing.T) {
	plan, err := frozenV17PlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
	if err != nil {
		t.Fatal(err)
	}
	value := emptyObservationForPlan(EnvironmentObservation{}, plan)
	if value.Schema != ObservationSchemaV17 || value.Interruption == nil ||
		value.Interruption.Schema != interruptionSchemaV1 ||
		value.Interruption.LastSubstage != "not_started" {
		t.Fatalf("V17 empty interruption state = %+v", value.Interruption)
	}
	run := &execution{plan: plan, observation: value}
	run.setInterruptionSubstage("backup_start")
	run.setInterruptionSubstage("misspelled-substage")
	if run.observation.Interruption.LastSubstage != "backup_start" {
		t.Fatalf("unknown substage replaced closed evidence: %+v", run.observation.Interruption)
	}
}

func TestChunkLifecycleReaderAcceptsOnlyPipelineStages(t *testing.T) {
	for _, stage := range []string{
		observationpublication.PlanningScheduleStage,
		observationpublication.InventoryScheduleStageV2,
		observationpublication.ScheduleStage,
		extractionpublication.ScheduleStage,
		relationshippublication.ScheduleStage,
	} {
		t.Run(stage, func(t *testing.T) {
			line := lifecycleTestLine(t, "started", stage, "running")
			_, found, err := parseChunkLifecycleLine(line, chunkLifecycleValidationV17)
			if err != nil || !found {
				t.Fatalf("valid stage refused: found=%t, %v", found, err)
			}
		})
	}
	// Vocabulary drift is validated then discarded under V17 — an unknown
	// stage, a started report without 'running', or a future settled outcome
	// must never abort a poll.
	for name, line := range map[string][]byte{
		"unknown stage":       lifecycleTestLine(t, "started", "unbounded-stage", "running"),
		"non-running started": lifecycleTestLine(t, "started", extractionpublication.ScheduleStage, "completed"),
		"open settled":        lifecycleTestLine(t, "settled", extractionpublication.ScheduleStage, "future-outcome"),
	} {
		if _, found, err := parseChunkLifecycleLine(line, chunkLifecycleValidationV17); err != nil || found {
			t.Fatalf("%s was not discarded: found=%t, %v", name, found, err)
		}
	}
	// Structural breakage stays fatal in both contracts.
	malformed := []byte(`generation chunk lifecycle: {"schema":"phebs-generation-chunk-lifecycle-v1","event":"started","identity":"bad","stage":"extraction-partitions","generation":"bad","attempt":0,"outcome":"running"}`)
	if _, _, err := parseChunkLifecycleLine(malformed, chunkLifecycleValidationV17); err == nil {
		t.Fatal("structural breakage passed the V17 contract")
	}
	// Frozen V16 execution semantics: extraction stage only, violations
	// fatal, no outcome inspection.
	planningLine := lifecycleTestLine(t, "started", observationpublication.PlanningScheduleStage, "running")
	if _, _, err := parseChunkLifecycleLine(planningLine, chunkLifecycleValidationV16); err == nil {
		t.Fatal("V16 contract accepted a non-extraction stage")
	}
	openOutcome := lifecycleTestLine(t, "settled", extractionpublication.ScheduleStage, "future-outcome")
	if _, found, err := parseChunkLifecycleLine(openOutcome, chunkLifecycleValidationV16); err != nil || !found {
		t.Fatalf("V16 contract inspected settled outcomes: found=%t, %v", found, err)
	}
}

func lifecycleTestLine(t *testing.T, event, stage, outcome string) []byte {
	t.Helper()
	return fmt.Appendf(nil,
		`generation chunk lifecycle: {"schema":%q,"event":%q,"identity":"sha256:%s","stage":%q,"generation":"sha256:%s","attempt":0,"outcome":%q}`,
		generationscheduler.ChunkLifecycleSchema, event,
		strings.Repeat("a", 64), stage, strings.Repeat("b", 64), outcome,
	)
}

type fixedGenerationChunkLeaseReader struct {
	states   map[string]store.GenerationChunkLeaseState
	retained map[string]store.GenerationScheduleRetentionState
	progress *store.GenerationScheduleProgress
	running  *store.GenerationChunkLeaseState
	current  func() (store.GenerationChunkLeaseState, error)
	state    func(string) (store.GenerationChunkLeaseState, error)
}

func (reader *fixedGenerationChunkLeaseReader) GenerationScheduleRetentionState(
	_ context.Context,
	_, _, scheduleDigest string,
) (store.GenerationScheduleRetentionState, error) {
	state, ok := reader.retained[scheduleDigest]
	if !ok {
		return store.GenerationScheduleRetentionState{}, store.ErrNotFound
	}
	return state, nil
}

func (reader *fixedGenerationChunkLeaseReader) CurrentGenerationRunningChunk(
	context.Context, string, string,
) (store.GenerationChunkLeaseState, error) {
	if reader.current != nil {
		return reader.current()
	}
	if reader.running != nil {
		return *reader.running, nil
	}
	return store.GenerationChunkLeaseState{}, store.ErrNotFound
}

func (reader *fixedGenerationChunkLeaseReader) GenerationScheduleProgress(
	context.Context, string, string,
) (store.GenerationScheduleProgress, error) {
	if reader.progress != nil {
		return *reader.progress, nil
	}
	return store.GenerationScheduleProgress{}, store.ErrNotFound
}

func (reader *fixedGenerationChunkLeaseReader) GenerationChunkLeaseState(
	_ context.Context,
	identity string,
) (store.GenerationChunkLeaseState, error) {
	if reader.state != nil {
		return reader.state(identity)
	}
	state, ok := reader.states[identity]
	if !ok {
		return store.GenerationChunkLeaseState{}, store.ErrNotFound
	}
	return state, nil
}

func TestV23StaleWorkerWaitsForReaperAfterCompletionPersistenceFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	line := lifecycleTestLine(t, "settled", extractionpublication.ScheduleStage, "completion_failed")
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV23)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	started := generationscheduler.ChunkLifecycleReport{
		Schema: generationscheduler.ChunkLifecycleSchema,
		Event:  "started", Identity: "sha256:" + strings.Repeat("a", 64),
		Stage:      extractionpublication.ScheduleStage,
		Generation: "sha256:" + strings.Repeat("b", 64), Attempt: 0, Outcome: "running",
	}
	repository := "example.invalid/semantic"
	scheduleDigest := "sha256:" + strings.Repeat("c", 64)
	reads := 0
	reader := &fixedGenerationChunkLeaseReader{state: func(string) (store.GenerationChunkLeaseState, error) {
		reads++
		status := store.GenerationChunkRunning
		if reads > 1 {
			status = store.GenerationChunkCanceled
		}
		return store.GenerationChunkLeaseState{
			Identity: started.Identity, ScheduleDigest: scheduleDigest,
			Repository: repository, Stage: started.Stage,
			Generation: started.Generation, Attempt: started.Attempt, Status: status,
		}, nil
	}}
	settled, err := waitStaleChunkFence(
		t.Context(), cursor, reader, started, repository, scheduleDigest, time.Second,
	)
	if err != nil || settled.Outcome != "stale_fenced" || reads < 2 {
		t.Fatalf("settled = %+v, reads = %d, err = %v", settled, reads, err)
	}
	collected := &fixedGenerationChunkLeaseReader{
		states: map[string]store.GenerationChunkLeaseState{},
		retained: map[string]store.GenerationScheduleRetentionState{
			scheduleDigest: {
				ScheduleDigest: scheduleDigest, CurrentPresent: true,
			},
		},
	}
	collectedCursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV23)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = collectedCursor.Close() }()
	settled, err = waitStaleChunkFence(
		t.Context(), collectedCursor, collected, started, repository, scheduleDigest, time.Second,
	)
	if err != nil || settled.Outcome != "stale_fenced" {
		t.Fatalf("collected settled = %+v, err = %v", settled, err)
	}
}

func TestV23LifecycleVocabularyDriftStopsPromptly(t *testing.T) {
	line := lifecycleTestLine(t, "settled", extractionpublication.ScheduleStage, "future-outcome")
	if _, found, err := parseChunkLifecycleLine(line, chunkLifecycleValidationV17); err != nil || found {
		t.Fatalf("V17 drift contract changed: found=%t err=%v", found, err)
	}
	report, found, err := parseChunkLifecycleLine(line, chunkLifecycleValidationV23)
	if err != nil || !found || report.Outcome != chunkLifecycleOutcomeUnknown {
		t.Fatalf("V23 drift projection = %+v, found=%t err=%v", report, found, err)
	}
}

func TestInterruptionLifecycleSelectsExactLiveExtractionLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	observationIdentity := "sha256:" + strings.Repeat("a", 64)
	extractionIdentity := "sha256:" + strings.Repeat("b", 64)
	observationGeneration := "sha256:" + strings.Repeat("c", 64)
	extractionGeneration := "sha256:" + strings.Repeat("d", 64)
	lines := fmt.Sprintf(
		"generation chunk lifecycle: {\"schema\":%q,\"event\":\"started\",\"identity\":%q,\"stage\":%q,\"generation\":%q,\"attempt\":0,\"outcome\":\"running\"}\n"+
			"generation chunk lifecycle: {\"schema\":%q,\"event\":\"started\",\"identity\":%q,\"stage\":%q,\"generation\":%q,\"attempt\":1,\"outcome\":\"running\"}\n",
		generationscheduler.ChunkLifecycleSchema, observationIdentity,
		observationpublication.PlanningScheduleStage, observationGeneration,
		generationscheduler.ChunkLifecycleSchema, extractionIdentity,
		extractionpublication.ScheduleStage, extractionGeneration,
	)
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	profile := PreparedProfile{RepositoryName: "example.invalid/semantic"}
	reader := &fixedGenerationChunkLeaseReader{states: map[string]store.GenerationChunkLeaseState{
		extractionIdentity: {
			Identity: extractionIdentity, Repository: profile.RepositoryName,
			Stage: extractionpublication.ScheduleStage, Generation: extractionGeneration,
			Attempt: 1, Status: store.GenerationChunkRunning,
		},
	}}
	binderCalls := 0
	selected, err := waitActiveChunkLifecycleWithBinder(
		t.Context(), cursor, reader, profile, "revision-b", time.Second,
		func(_ PreparedProfile, generation, revision string) (bool, error) {
			binderCalls++
			return generation == extractionGeneration && revision == "revision-b", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Identity != extractionIdentity || selected.Attempt != 1 || binderCalls != 1 {
		t.Fatalf("selected lifecycle = %+v, binder calls = %d", selected, binderCalls)
	}
}

func TestInterruptionTriggerWaitFastFailsOnSettledCommandedSchedule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	// A stale prior-revision start must not suppress the authoritative
	// settled-commanded-schedule probe. Its settled lifecycle line may be
	// absent or vocabulary-discarded, so the store remains load-bearing.
	staleLine := lifecycleTestLine(t, "started", extractionpublication.ScheduleStage, "running")
	if err := os.WriteFile(path, append(staleLine, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	profile := PreparedProfile{RepositoryName: "example.invalid/semantic"}
	settledGeneration := "sha256:" + strings.Repeat("e", 64)
	reader := &fixedGenerationChunkLeaseReader{progress: &store.GenerationScheduleProgress{
		Generation: settledGeneration, Status: store.GenerationScheduleSettled,
	}}
	_, err = waitActiveChunkLifecycleWithBinder(
		t.Context(), cursor, reader, profile, "revision-b", 30*time.Second,
		func(_ PreparedProfile, generation, revision string) (bool, error) {
			return generation == settledGeneration && revision == "revision-b", nil
		},
	)
	if !errors.Is(err, errInterruptionTriggerUnsatisfiable) {
		t.Fatalf("settled commanded schedule did not fast-fail: %v", err)
	}
	// A schedule settled for a prior revision does not satisfy the fast-fail:
	// the wait keeps polling for the commanded pipeline until its deadline.
	staleReader := &fixedGenerationChunkLeaseReader{progress: &store.GenerationScheduleProgress{
		Generation: "sha256:" + strings.Repeat("f", 64), Status: store.GenerationScheduleSettled,
	}}
	staleCursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = staleCursor.Close() }()
	_, err = waitActiveChunkLifecycleWithBinder(
		t.Context(), staleCursor, staleReader, profile, "revision-b", 300*time.Millisecond,
		func(_ PreparedProfile, generation, revision string) (bool, error) {
			return generation == settledGeneration, nil
		},
	)
	if err == nil || errors.Is(err, errInterruptionTriggerUnsatisfiable) {
		t.Fatalf("prior-revision settled schedule tripped the fast-fail: %v", err)
	}
}

func TestInterruptionTriggerWaitRetriesTransientReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	line := lifecycleTestLine(t, "started", extractionpublication.ScheduleStage, "running")
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	profile := PreparedProfile{RepositoryName: "example.invalid/semantic"}
	reader := &fixedGenerationChunkLeaseReader{}
	transient := errors.New("injected transient binder failure")
	_, err = waitActiveChunkLifecycleWithBinder(
		t.Context(), cursor, reader, profile, "revision-b", 300*time.Millisecond,
		func(PreparedProfile, string, string) (bool, error) {
			return false, transient
		},
	)
	// The transient error is retained for the deadline report instead of
	// aborting the wait on its first occurrence.
	if err == nil || !errors.Is(err, transient) ||
		!strings.Contains(err.Error(), "deadline expired") {
		t.Fatalf("transient binder failure aborted the trigger wait: %v", err)
	}
}

func TestV22InterruptionTriggerRetainsStoreScheduleAcrossLifecycleStartAndSettle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	// A same-drain started/settled pair leaves no active lifecycle candidate.
	// V18 still discovers the independently running current-schedule chunk
	// from the store instead of treating log timing as authority.
	started := lifecycleTestLine(t, "started", extractionpublication.ScheduleStage, "running")
	settled := lifecycleTestLine(t, "settled", extractionpublication.ScheduleStage, "completed")
	lines := append(append(append([]byte{}, started...), '\n'), settled...)
	lines = append(lines, '\n')
	if err := os.WriteFile(path, lines, 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	profile := PreparedProfile{RepositoryName: "example.invalid/semantic"}
	state := store.GenerationChunkLeaseState{
		Identity:       "sha256:" + strings.Repeat("c", 64),
		ScheduleDigest: "sha256:" + strings.Repeat("e", 64), Repository: profile.RepositoryName,
		Stage:      extractionpublication.ScheduleStage,
		Generation: "sha256:" + strings.Repeat("d", 64), Attempt: 2,
		Status: store.GenerationChunkRunning,
	}
	run := &execution{
		ctx: t.Context(), plan: Plan{Schema: PlanSchemaV22},
		observation: emptyObservationForPlan(EnvironmentObservation{}, Plan{Schema: PlanSchemaV22}),
	}
	server := &privateServer{done: make(chan error, 1)}
	var selectedSchedule string
	report, err := run.waitInterruptionTriggerV18WithReader(
		server, cursor, &fixedGenerationChunkLeaseReader{running: &state},
		&fixedGenerationChunkLeaseReader{}, profile,
		"revision-b", time.Second,
		func(context.Context) (privateProfileSnapshot, privateConvergenceProbe, error) {
			t.Fatal("exact inspector ran after an immediately selectable store lease")
			return privateProfileSnapshot{}, privateConvergenceProbe{}, nil
		},
		func(PreparedProfile, string, string) (bool, error) { return true, nil },
		func(selected store.GenerationChunkLeaseState) error {
			selectedSchedule = selected.ScheduleDigest
			return nil
		},
	)
	if err != nil || report.Identity != state.Identity || report.Attempt != state.Attempt ||
		report.Outcome != "running" || selectedSchedule != state.ScheduleDigest {
		t.Fatalf("store-selected V18 trigger = %+v, %v", report, err)
	}
}

func TestV18InterruptionTriggerSealsLastUpstreamProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	plan := Plan{Schema: PlanSchemaV18}
	run := &execution{
		ctx: t.Context(), plan: plan,
		observation: emptyObservationForPlan(EnvironmentObservation{}, plan),
	}
	probe := convergenceProbe("observation_publication", "pending")
	_, err = run.waitInterruptionTriggerV18WithReader(
		&privateServer{done: make(chan error, 1)}, cursor,
		&fixedGenerationChunkLeaseReader{}, &fixedGenerationChunkLeaseReader{},
		PreparedProfile{RepositoryName: "example.invalid/semantic"},
		"revision-b", 300*time.Millisecond,
		func(context.Context) (privateProfileSnapshot, privateConvergenceProbe, error) {
			return privateProfileSnapshot{}, probe,
				errors.New("T40.13 observation publication has not converged")
		},
		func(PreparedProfile, string, string) (bool, error) { return false, nil },
		nil,
	)
	if !errors.Is(err, errInterruptionTriggerDeadline) {
		t.Fatalf("V18 upstream wait = %v, want typed trigger deadline", err)
	}
	got := run.observation.Interruption
	if got.LastProgressStage != probe.Stage || got.LastProgressClass != "pending" ||
		got.LastProgressSHA256 != probe.SHA256 || got.LastProgressWallMS <= 0 {
		t.Fatalf("V18 retained progress = %+v", got)
	}
}

func TestV18InterruptionTriggerSamplesLeaseWhileInspectionIsBlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	profile := PreparedProfile{RepositoryName: "example.invalid/semantic"}
	state := store.GenerationChunkLeaseState{
		Identity: "sha256:" + strings.Repeat("e", 64), Repository: profile.RepositoryName,
		Stage:      extractionpublication.ScheduleStage,
		Generation: "sha256:" + strings.Repeat("f", 64), Attempt: 1,
		Status: store.GenerationChunkRunning,
	}
	calls := 0
	leaseReader := &fixedGenerationChunkLeaseReader{current: func() (store.GenerationChunkLeaseState, error) {
		calls++
		if calls == 1 {
			return store.GenerationChunkLeaseState{}, store.ErrNotFound
		}
		return state, nil
	}}
	plan := Plan{Schema: PlanSchemaV18}
	run := &execution{
		ctx: t.Context(), plan: plan,
		observation: emptyObservationForPlan(EnvironmentObservation{}, plan),
	}
	report, err := run.waitInterruptionTriggerV18WithReader(
		&privateServer{done: make(chan error, 1)}, cursor, leaseReader,
		&fixedGenerationChunkLeaseReader{}, profile, "revision-b", 3*time.Second,
		func(ctx context.Context) (privateProfileSnapshot, privateConvergenceProbe, error) {
			<-ctx.Done()
			return privateProfileSnapshot{}, convergenceProbe("observation_publication"), ctx.Err()
		},
		func(PreparedProfile, string, string) (bool, error) { return true, nil },
		nil,
	)
	if err != nil || report.Identity != state.Identity || calls < 2 {
		t.Fatalf("lease during blocked inspection = %+v, calls=%d, err=%v", report, calls, err)
	}
}

func TestV18InterruptionTriggerStopsOnTerminalProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	plan := Plan{Schema: PlanSchemaV18}
	run := &execution{
		ctx: t.Context(), plan: plan,
		observation: emptyObservationForPlan(EnvironmentObservation{}, plan),
	}
	probe := convergenceProbe("extraction_publication", "failed")
	_, err = run.waitInterruptionTriggerV18WithReader(
		&privateServer{done: make(chan error, 1)}, cursor,
		&fixedGenerationChunkLeaseReader{}, &fixedGenerationChunkLeaseReader{},
		PreparedProfile{RepositoryName: "example.invalid/semantic"},
		"revision-b", time.Second,
		func(context.Context) (privateProfileSnapshot, privateConvergenceProbe, error) {
			return privateProfileSnapshot{}, probe, errExtractionScheduleTerminal
		},
		func(PreparedProfile, string, string) (bool, error) { return false, nil },
		nil,
	)
	if !errors.Is(err, errInterruptionProgressTerminal) ||
		run.observation.Interruption.LastProgressClass != "terminal" {
		t.Fatalf("V18 terminal progress = %+v, %v", run.observation.Interruption, err)
	}
}

func TestV18StaleWorkerSelectsCurrentStoreLeaseWithoutLifecycleStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cursor.Close() }()
	profile := PreparedProfile{RepositoryName: "example.invalid/semantic"}
	state := store.GenerationChunkLeaseState{
		Identity: "sha256:" + strings.Repeat("1", 64), Repository: profile.RepositoryName,
		Stage:      extractionpublication.ScheduleStage,
		Generation: "sha256:" + strings.Repeat("2", 64), Attempt: 1,
		Status: store.GenerationChunkRunning,
	}
	report, err := waitCurrentRunningGenerationChunk(
		t.Context(), cursor, &fixedGenerationChunkLeaseReader{running: &state}, profile,
		"revision-b", time.Second,
		func(PreparedProfile, string, string) (bool, error) { return true, nil },
	)
	if err != nil || report.Identity != state.Identity || report.Outcome != "running" {
		t.Fatalf("V18 stale-worker store trigger = %+v, %v", report, err)
	}
}

func TestInterruptionLeaseRecoveryRequiresNonRunningFate(t *testing.T) {
	profile := PreparedProfile{RepositoryName: "example.invalid/semantic"}
	trigger := generationscheduler.ChunkLifecycleReport{
		Identity:   "sha256:" + strings.Repeat("a", 64),
		Stage:      extractionpublication.ScheduleStage,
		Generation: "sha256:" + strings.Repeat("b", 64), Attempt: 1,
	}
	recovered := &fixedGenerationChunkLeaseReader{states: map[string]store.GenerationChunkLeaseState{
		trigger.Identity: {
			Identity: trigger.Identity, Repository: profile.RepositoryName,
			Stage: trigger.Stage, Generation: trigger.Generation, Attempt: trigger.Attempt,
			Status: store.GenerationChunkPending,
		},
	}}
	fate, err := waitLeaseRecoveryWithReader(t.Context(), recovered, profile, trigger, time.Second)
	if err != nil || fate != string(store.GenerationChunkPending) {
		t.Fatalf("recovered lease fate = %q, %v", fate, err)
	}
	stranded := &fixedGenerationChunkLeaseReader{states: map[string]store.GenerationChunkLeaseState{
		trigger.Identity: {
			Identity: trigger.Identity, Repository: profile.RepositoryName,
			Stage: trigger.Stage, Generation: trigger.Generation, Attempt: trigger.Attempt,
			Status: store.GenerationChunkRunning,
		},
	}}
	if _, err := waitLeaseRecoveryWithReader(
		t.Context(), stranded, profile, trigger, 300*time.Millisecond,
	); err == nil || !strings.Contains(err.Error(), "was not recovered") {
		t.Fatalf("stranded running lease passed recovery verification: %v", err)
	}
	foreign := &fixedGenerationChunkLeaseReader{states: map[string]store.GenerationChunkLeaseState{
		trigger.Identity: {
			Identity: trigger.Identity, Repository: "example.invalid/other",
			Stage: trigger.Stage, Generation: trigger.Generation, Attempt: trigger.Attempt,
			Status: store.GenerationChunkPending,
		},
	}}
	if _, err := waitLeaseRecoveryWithReader(
		t.Context(), foreign, profile, trigger, time.Second,
	); err == nil {
		t.Fatal("foreign-repository lease passed recovery verification")
	}
	drifted := &fixedGenerationChunkLeaseReader{states: map[string]store.GenerationChunkLeaseState{
		trigger.Identity: {
			Identity: trigger.Identity, Repository: profile.RepositoryName,
			Stage: trigger.Stage, Generation: "sha256:" + strings.Repeat("c", 64),
			Attempt: trigger.Attempt, Status: store.GenerationChunkPending,
		},
	}}
	if _, err := waitLeaseRecoveryWithReader(
		t.Context(), drifted, profile, trigger, time.Second,
	); err == nil {
		t.Fatal("changed-generation lease passed recovery verification")
	}
}

func TestV22InterruptionLeaseRecoveryCorroboratesCollection(t *testing.T) {
	profile := PreparedProfile{RepositoryName: "example.invalid/semantic"}
	trigger := generationscheduler.ChunkLifecycleReport{
		Identity:   "sha256:" + strings.Repeat("a", 64),
		Stage:      extractionpublication.ScheduleStage,
		Generation: "sha256:" + strings.Repeat("b", 64), Attempt: 1,
	}
	scheduleDigest := "sha256:" + strings.Repeat("c", 64)
	singleCollection := &fixedGenerationChunkLeaseReader{
		states: map[string]store.GenerationChunkLeaseState{},
		retained: map[string]store.GenerationScheduleRetentionState{
			scheduleDigest: {
				ScheduleDigest: scheduleDigest, CurrentPresent: true,
			},
		},
	}
	if fate, err := waitLeaseRecoveryV22WithReader(
		t.Context(), singleCollection, profile, trigger, scheduleDigest,
		interruptionRecoveryV23, 100*time.Millisecond,
	); err == nil {
		t.Fatalf("one collected projection passed recovery verification with fate %q", fate)
	}
	for _, test := range []struct {
		name      string
		retention store.GenerationScheduleRetentionState
		want      string
		wantErr   bool
	}{
		{
			name: "absent retired schedule",
			retention: store.GenerationScheduleRetentionState{
				ScheduleDigest: scheduleDigest, CurrentPresent: true,
			},
			want: interruptionFateCollected,
		},
		{
			name: "retained superseded schedule",
			retention: store.GenerationScheduleRetentionState{
				ScheduleDigest: scheduleDigest, Present: true, CurrentPresent: true,
				Status: store.GenerationScheduleSuperseded,
			},
			want: interruptionFateCollected,
		},
		{
			name: "current schedule",
			retention: store.GenerationScheduleRetentionState{
				ScheduleDigest: scheduleDigest, Present: true, CurrentPresent: true, Current: true,
				Status: store.GenerationScheduleActive,
			},
			wantErr: true,
		},
		{
			name: "noncurrent active schedule",
			retention: store.GenerationScheduleRetentionState{
				ScheduleDigest: scheduleDigest, Present: true, CurrentPresent: true,
				Status: store.GenerationScheduleActive,
			},
			wantErr: true,
		},
		{
			name: "missing current authority",
			retention: store.GenerationScheduleRetentionState{
				ScheduleDigest: scheduleDigest,
			},
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &fixedGenerationChunkLeaseReader{
				states: map[string]store.GenerationChunkLeaseState{},
				retained: map[string]store.GenerationScheduleRetentionState{
					scheduleDigest: test.retention,
				},
			}
			limit := 100 * time.Millisecond
			if !test.wantErr {
				limit = 1500 * time.Millisecond
			}
			fate, err := waitLeaseRecoveryV22WithReader(
				t.Context(), reader, profile, trigger, scheduleDigest, interruptionRecoveryV23, limit,
			)
			if test.wantErr {
				if err == nil {
					t.Fatalf("uncorroborated collection passed with fate %q", fate)
				}
				return
			}
			if err != nil || fate != test.want {
				t.Fatalf("collection fate = %q, %v", fate, err)
			}
		})
	}

	pending := &fixedGenerationChunkLeaseReader{
		states: map[string]store.GenerationChunkLeaseState{
			trigger.Identity: {
				Identity: trigger.Identity, ScheduleDigest: scheduleDigest,
				Repository: profile.RepositoryName, Stage: trigger.Stage,
				Generation: trigger.Generation, Attempt: trigger.Attempt,
				Status: store.GenerationChunkPending,
			},
		},
	}
	if fate, err := waitLeaseRecoveryV22WithReader(
		t.Context(), pending, profile, trigger, scheduleDigest,
		interruptionRecoveryV22, 100*time.Millisecond,
	); err != nil || fate != string(store.GenerationChunkPending) {
		t.Fatalf("historical pending fate = %q, %v", fate, err)
	}
	if fate, err := waitLeaseRecoveryV22WithReader(
		t.Context(), pending, profile, trigger, scheduleDigest,
		interruptionRecoveryV23, 100*time.Millisecond,
	); err == nil {
		t.Fatalf("reclaimable pending fate passed recovery verification as %q", fate)
	}
	reads := 0
	recovered := &fixedGenerationChunkLeaseReader{state: func(string) (store.GenerationChunkLeaseState, error) {
		reads++
		status := store.GenerationChunkPending
		if reads > 1 {
			status = store.GenerationChunkCanceled
		}
		return store.GenerationChunkLeaseState{
			Identity: trigger.Identity, ScheduleDigest: scheduleDigest,
			Repository: profile.RepositoryName, Stage: trigger.Stage,
			Generation: trigger.Generation, Attempt: trigger.Attempt, Status: status,
		}, nil
	}}
	fate, err := waitLeaseRecoveryV22WithReader(
		t.Context(), recovered, profile, trigger, scheduleDigest,
		interruptionRecoveryV23, 1500*time.Millisecond,
	)
	if err != nil || fate != string(store.GenerationChunkCanceled) || reads < 2 {
		t.Fatalf("closed recovered fate = %q, reads=%d, %v", fate, reads, err)
	}
	pending.states[trigger.Identity] = store.GenerationChunkLeaseState{
		Identity: trigger.Identity, ScheduleDigest: "sha256:" + strings.Repeat("d", 64),
		Repository: profile.RepositoryName, Stage: trigger.Stage,
		Generation: trigger.Generation, Attempt: trigger.Attempt,
		Status: store.GenerationChunkPending,
	}
	if _, err := waitLeaseRecoveryV22WithReader(
		t.Context(), pending, profile, trigger, scheduleDigest,
		interruptionRecoveryV23, time.Second,
	); err == nil {
		t.Fatal("changed schedule digest passed recovery verification")
	}
}

func TestRelationshipPartialPresentFollowsOneHashedRepository(t *testing.T) {
	dataDir := t.TempDir()
	publication := filepath.Join(
		dataDir, "relationships", "relationship-publications", strings.Repeat("a", 64),
	)
	if err := os.MkdirAll(publication, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(publication, ".stage-test"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := relationshipPartialPresent(dataDir)
	if err != nil || !found {
		t.Fatalf("hashed relationship partial = %t, %v", found, err)
	}
	if err := os.MkdirAll(
		filepath.Join(dataDir, "relationships", "relationship-publications", strings.Repeat("b", 64)),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := relationshipPartialPresent(dataDir); err == nil {
		t.Fatal("multi-repository relationship partial inventory passed its bound")
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
	cursor, err := newChunkLifecycleCursor(path, 0, chunkLifecycleValidationV17)
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

// A V20 terminal probe deliberately waits for its identical confirming
// re-probe; the wall deadline landing inside that window must seal as a
// deadline stop retaining the terminal-shaped final transition — and a
// stopped observation that cannot seal must fail closed BEFORE custody is
// destroyed, never after.
func TestUnconfirmedTerminalDeadlineSealsAndRetainsCustodyOnUnsealable(t *testing.T) {
	hostToolchain, err := ObserveHostToolchain(t.Context())
	if err != nil {
		t.Skipf("host toolchain unavailable: %v", err)
	}
	v21Plan, err := frozenV21PlanWithHostToolchain(testSourceCommit, hostToolchain)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	total := 8
	probe, cause := classifyCallerGeneration(
		apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
			State: "missing", PartitionProgress: &apiresponse.CallerMapPartitionProgress{
				State: "complete", SettledPairCount: total,
				SucceededPairCount: total, TotalPairCount: &total,
			},
		}},
		callerJobProbe{
			State:         store.JobProjectionUnavailable,
			ResolverState: store.JobProjectionUnavailable,
		},
		profileInspectionV21,
	)
	if !errors.Is(cause, errCallerPublicationMissing) {
		t.Fatalf("v21 missing caller publication = %v, want terminal", cause)
	}

	newRun := func(t *testing.T) *execution {
		t.Helper()
		root := t.TempDir()
		module, workspace := filepath.Join(root, "module"), filepath.Join(root, "custody")
		for _, path := range []string{module, workspace} {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		logPath := filepath.Join(workspace, "server.log")
		if err := os.WriteFile(logPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		run := &execution{
			ctx: t.Context(), moduleRoot: module, workspace: workspace, plan: v21Plan,
			observation: emptyObservationForPlan(EnvironmentObservation{
				OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
				FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
			}, v21Plan),
		}
		run.observation.Toolchain = []ToolchainObservation{
			{Name: "phebs", SHA256: digest}, {Name: "zoekt-git-index", SHA256: digest},
			{Name: "phebs-focused-index", SHA256: digest}, {Name: "buf", SHA256: digest},
		}
		run.observation.Phases[0] = succeededPhase("preflight", PhaseMetrics{WallMS: 1})
		run.startPhase(1)
		return run
	}

	t.Run("deadline inside confirmation window seals", func(t *testing.T) {
		run := newRun(t)
		server := &privateServer{done: make(chan error, 1), logPath: filepath.Join(run.workspace, "server.log")}
		profile := PreparedProfile{Name: "semantic-262144-v1"}
		_, waitErr := run.waitSnapshotWithInspection(
			profile, "a", "cold", 100*time.Millisecond, server,
			func(context.Context) (privateProfileSnapshot, privateConvergenceProbe, error) {
				return privateProfileSnapshot{}, probe, cause
			},
		)
		if !errors.Is(waitErr, errConvergenceDeadline) {
			t.Fatalf("unconfirmed terminal deadline wait = %v", waitErr)
		}
		if len(run.observation.ConvergenceWaits) != 1 {
			t.Fatalf("wait inventory = %+v", run.observation.ConvergenceWaits)
		}
		wait := run.observation.ConvergenceWaits[0]
		transitions := wait.InspectionTransitions
		if wait.Outcome != "deadline" || len(transitions) == 0 ||
			transitions[len(transitions)-1].Class != "terminal" {
			t.Fatalf("torn terminal wait = %+v", wait)
		}
		if err := validateConvergenceWaits(
			run.observation.ConvergenceWaits, observationDetailV17,
		); err != nil {
			t.Fatalf("unconfirmed terminal deadline wait did not validate: %v", err)
		}
		// V15..V20 keep their sealed historical semantics: the old executor's
		// seal-time validation refused this shape, so its rejection remains a
		// fence proving any such earlier artifact inauthentic.
		if err := validateConvergenceWaits(
			run.observation.ConvergenceWaits, observationDetailV16,
		); err == nil {
			t.Fatal("historical contract accepted the torn terminal shape")
		}
		claimed := wait
		claimed.Outcome = "caller_generation_terminal"
		if err := validateConvergenceWaits(
			[]ConvergenceWaitObservation{claimed}, observationDetailV17,
		); err != nil {
			// The claimed-terminal direction of the fence must stay closed;
			// only the deadline exemption is new. (A claimed terminal with a
			// terminal-shaped last transition remains valid.)
			t.Fatalf("claimed terminal with terminal transition = %v", err)
		}
		claimedTorn := wait
		claimedTorn.InspectionTransitions = append(
			[]ConvergenceTransitionObservation(nil), wait.InspectionTransitions...,
		)
		claimedTorn.InspectionTransitions[len(claimedTorn.InspectionTransitions)-1].Class = "pending"
		claimedTorn.LastInspectionClass = "pending"
		claimedTorn.Outcome = "caller_generation_terminal"
		if err := validateConvergenceWaits(
			[]ConvergenceWaitObservation{claimedTorn}, observationDetailV17,
		); err == nil {
			t.Fatal("claimed terminal without terminal transition escaped the fence")
		}
		stopped, stopErr := run.stopAfterFailure(waitErr)
		if stopErr != nil {
			t.Fatal(stopErr)
		}
		if err := ValidateObservation(stopped); err != nil {
			t.Fatalf("stopped observation did not seal: %v", err)
		}
		// Receipt sealing additionally cross-checks DeadlineMS against the
		// plan's frozen four-hour deadline, which a fast wait cannot carry;
		// the frozen-deadline parity fence has its own coverage.
	})

	t.Run("unsealable stop retains custody", func(t *testing.T) {
		run := newRun(t)
		destroyed := false
		run.custodyDestroy = func(workspace, moduleRoot string) error {
			destroyed = true
			return nil
		}
		// An invalid wait makes the stopped observation unsealable.
		run.observation.ConvergenceWaits = []ConvergenceWaitObservation{{
			Profile: "semantic-262144-v1", Label: "cold", Revision: "a",
			Outcome: "bogus_outcome",
		}}
		_, stopErr := run.stopAfterFailure(errors.New("injected failure"))
		if stopErr == nil {
			t.Fatal("unsealable stopped observation did not fail")
		}
		if destroyed {
			t.Fatal("custody destroyed before the stopped observation sealed")
		}
	})
}

// A V21 caller wait seals the dead-job terminal with the caller job
// projection recorded, and the V17 detail fence refutes a terminal whose
// recorded job row is still active.
func TestV21CallerTerminalRecordsJobProjectionAndSeals(t *testing.T) {
	hostToolchain, err := ObserveHostToolchain(t.Context())
	if err != nil {
		t.Skipf("host toolchain unavailable: %v", err)
	}
	v21Plan, err := frozenV21PlanWithHostToolchain(testSourceCommit, hostToolchain)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	total := 8
	probe, cause := classifyCallerGeneration(
		apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
			State: "missing", PartitionProgress: &apiresponse.CallerMapPartitionProgress{
				State: "partial", SettledPairCount: 3,
				SucceededPairCount: 3, TotalPairCount: &total,
			},
		}},
		callerJobProbe{State: store.JobProjectionExact,
			Job:           &store.CallerJobProjection{Status: store.StatusFailed, Attempts: 3},
			ResolverState: store.JobProjectionUnavailable},
		profileInspectionV21,
	)
	if !errors.Is(cause, errCallerPublicationMissing) {
		t.Fatalf("v21 dead-job classification = %v, want terminal", cause)
	}
	root := t.TempDir()
	module, workspace := filepath.Join(root, "module"), filepath.Join(root, "custody")
	for _, path := range []string{module, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	logPath := filepath.Join(workspace, "server.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run := &execution{
		ctx: t.Context(), moduleRoot: module, workspace: workspace, plan: v21Plan,
		observation: emptyObservationForPlan(EnvironmentObservation{
			OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
			FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
		}, v21Plan),
	}
	run.observation.Toolchain = []ToolchainObservation{
		{Name: "phebs", SHA256: digest}, {Name: "zoekt-git-index", SHA256: digest},
		{Name: "phebs-focused-index", SHA256: digest}, {Name: "buf", SHA256: digest},
	}
	run.observation.Phases[0] = succeededPhase("preflight", PhaseMetrics{WallMS: 1})
	run.startPhase(1)
	server := &privateServer{done: make(chan error, 1), logPath: logPath}
	profile := PreparedProfile{Name: "semantic-262144-v1"}
	_, waitErr := run.waitSnapshotWithInspection(
		profile, "a", "cold", time.Duration(v21Plan.Safety.FullConvergenceDeadlineMS)*time.Millisecond, server,
		func(context.Context) (privateProfileSnapshot, privateConvergenceProbe, error) {
			return privateProfileSnapshot{}, probe, cause
		},
	)
	if !errors.Is(waitErr, errCallerGenerationTerminal) {
		t.Fatalf("v21 dead-job wait = %v, want terminal", waitErr)
	}
	if len(run.observation.ConvergenceWaits) != 1 {
		t.Fatalf("wait inventory = %+v", run.observation.ConvergenceWaits)
	}
	wait := run.observation.ConvergenceWaits[0]
	if wait.Outcome != "caller_generation_terminal" || wait.Attempts != 2 ||
		wait.CallerProgress == nil || wait.CallerProgress.JobState != "failed" ||
		wait.CallerProgress.JobAttempts != 3 || wait.CallerProgress.PartitionState != "partial" {
		t.Fatalf("v21 caller terminal wait = %+v (progress %+v)", wait, wait.CallerProgress)
	}
	if err := validateConvergenceWaits(
		run.observation.ConvergenceWaits, observationDetailV17,
	); err != nil {
		t.Fatalf("v21 caller terminal did not validate: %v", err)
	}
	// The V16 detail must refuse the new projection so historical schemas
	// cannot silently acquire it.
	if err := validateConvergenceWaits(
		run.observation.ConvergenceWaits, observationDetailV16,
	); err == nil {
		t.Fatal("v16 contract accepted v17 caller diagnostics")
	}
	// An active recorded job row refutes the claimed terminal.
	refuted := wait
	refuted.CallerProgress = &CallerProgressObservation{
		State: "missing", PartitionState: "partial",
		SettledPairCount: 3, SucceededPairCount: 3, JobState: "running", JobAttempts: 1,
	}
	if err := validateConvergenceWaits(
		[]ConvergenceWaitObservation{refuted}, observationDetailV17,
	); err == nil {
		t.Fatal("active caller job escaped the terminal coherence fence")
	}
	missingProjection := wait
	missingProjection.CallerProgress = nil
	missingProjection.CallerProgressWallMS = 0
	if err := validateConvergenceWaits(
		[]ConvergenceWaitObservation{missingProjection}, observationDetailV17,
	); err == nil {
		t.Fatal("v17 caller terminal sealed without its job projection")
	}
	boundTotal := 1
	boundRefusal := wait
	boundRefusal.Outcome = "caller_generation_bound_refusal"
	boundRefusal.CallerProgress = &CallerProgressObservation{
		State: "failed", PartitionState: "complete", SettledPairCount: 1,
		RefusedPairCount: 1, TotalPairCount: &boundTotal,
		Refusals: []CallerRefusalObservation{{
			Stage:          pipelinerefusal.StageCallerGenerationAdmission,
			GenerationKind: pipelinerefusal.GenerationCaller,
			Classification: pipelinerefusal.ClassificationLimit,
			Dimension:      pipelinerefusal.DimensionCallerGenerationAbstentions,
			Observed:       callerleaf.MaxAggregateAbstentionRecords + 1,
			Limit:          callerleaf.MaxAggregateAbstentionRecords, OutcomeCount: 1,
		}},
		JobState: "done", JobAttempts: 1,
	}
	if err := validateConvergenceWaits(
		[]ConvergenceWaitObservation{boundRefusal}, observationDetailV17,
	); err != nil {
		t.Fatalf("exact caller bound refusal did not seal: %v", err)
	}
	currentTotal := 3
	forgedCurrent := wait
	forgedCurrent.CallerProgress = &CallerProgressObservation{
		State: "current", GenerationDigestValid: true, PartitionState: "complete",
		SettledPairCount: 3, SucceededPairCount: 3, TotalPairCount: &currentTotal,
		JobState: "done", JobAttempts: 1,
	}
	if err := validateConvergenceWaits(
		[]ConvergenceWaitObservation{forgedCurrent}, observationDetailV17,
	); err == nil {
		t.Fatal("successful current caller projection sealed as terminal")
	}
	partialTotal := 8
	forgedPartial := wait
	forgedPartial.CallerProgress = &CallerProgressObservation{
		State: "missing", PartitionState: "partial", SettledPairCount: 3,
		SucceededPairCount: 3, TotalPairCount: &partialTotal,
	}
	if err := validateConvergenceWaits(
		[]ConvergenceWaitObservation{forgedPartial}, observationDetailV17,
	); err == nil {
		t.Fatal("partial caller progress without a dead job sealed as terminal")
	}
	forgedRefusal := wait
	forgedRefusal.Outcome = "caller_generation_bound_refusal"
	if err := validateConvergenceWaits(
		[]ConvergenceWaitObservation{forgedRefusal}, observationDetailV17,
	); err == nil {
		t.Fatal("untyped caller terminal sealed as a bound refusal")
	}
	stopped, stopErr := run.stopAfterFailure(waitErr)
	if stopErr != nil {
		t.Fatal(stopErr)
	}
	if err := ValidateObservation(stopped); err != nil {
		t.Fatalf("stopped v21 observation did not seal: %v", err)
	}
}

func TestExecutionMarkerIsAtomicAndRefusesReuse(t *testing.T) {
	workspace := t.TempDir()
	digest := "sha256:" + strings.Repeat("a", 64)
	if err := writeExecutionMarker(workspace, digest); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(workspace, executedMarkerName))
	if err != nil || string(raw) != digest+"\n" {
		t.Fatalf("execution marker = %q, %v", raw, err)
	}
	if err := writeExecutionMarker(workspace, digest); err == nil {
		t.Fatal("existing execution marker was overwritten")
	}
}
