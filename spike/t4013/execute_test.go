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
	"testing"

	"github.com/bmeddeb/phebs/internal/generationscheduler"
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
	run.startPhase(5)
	run.metersTracked = expectedPhaseMeters(5)
	stopped, err := run.stopAfterFailure(directRecovery(errors.New("injected recovery failure")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatal("exact custody survived stopped-run teardown")
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
	plan, err := frozenPlanWithHostToolchain(testSourceCommit, fakeHostToolchain())
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
	run.metersTracked = expectedPhaseMeters(1)
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
		{name: "exact oracle", cause: exactOracle("mismatch"), code: "exact_gate_failed", decision: "reduce", substantiated: true},
		{name: "pressure refusal", cause: errProductionPressure, code: "production_pressure_gate_refused", decision: "reduce", substantiated: true},
		{name: "direct recovery", cause: directRecovery(errors.New("did not converge")), code: "direct_recovery_failed", decision: "p6_investigation", substantiated: true},
		{name: "review ceiling", cause: errReviewCeiling, code: "review_ceiling_crossed", decision: "cohort_experiment", substantiated: true},
		{name: "missing measurement overrides exact and ceiling", cause: exactOracle("mismatch"), measurement: errors.New("meter failed"), ceiling: errReviewCeiling, code: "failed_phase_measurement_unavailable", decision: "unclassified"},
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
		switch request.URL.Path {
		case "/api/search":
			_, _ = io.WriteString(response, `{"files":[{"repo":"github.com/example/repo","path":"fixture.go","chunks":[{"content":"T401","start_line":1,"ranges":[{"start_line":1,"start_col":1,"end_line":1,"end_col":5}]}]}],"stats":{}}`)
		case "/api/services":
			_, _ = io.WriteString(response, `{"schema":"test","repository":{"catalog_service_count":1},"filters":{},"services":[{}],"pagination":{"order":"key","page_size":100,"returned":1}}`)
		case "/api/service-relationships":
			if rows {
				_, _ = io.WriteString(response, `{"schema":"test","query":{},"rows_state":"nonempty","roots":[{"generation":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","root_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}],"rows":[{"citation":"token"}],"coverage":{},"pagination":{},"caveat":""}`)
			} else {
				_, _ = io.WriteString(response, `{"schema":"test","query":{},"rows_state":"empty","roots":[{"generation":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","root_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}],"rows":[],"coverage":{},"pagination":{},"caveat":""}`)
			}
		case "/api/service-relationship-citation":
			_, _ = io.WriteString(response, `{"schema":"test","repository":"github.com/example/repo","root_schema":"test","generation":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","root_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","authority_digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","projection":{},"evidence":{},"content":"citation"}`)
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
