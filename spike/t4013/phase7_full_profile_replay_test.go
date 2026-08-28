package t4013

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

const (
	fullProfilePhase7ReplayEnvironment = "PHEBS_T4013_FULL_PROFILE_PHASE7_REPLAY"
	fullProfilePhase7ReplayRoot        = "PHEBS_T4013_PHASE7_REPLAY_ROOT"
	fullProfilePhase7ReplayCommit      = "PHEBS_T4013_PHASE7_REPLAY_COMMIT"
	fullProfilePhase7ReplayGoSHA256    = "PHEBS_T4013_PHASE7_REPLAY_GO_SHA256"
	fullProfilePhase7ReplayGitSHA256   = "PHEBS_T4013_PHASE7_REPLAY_GIT_SHA256"
	fullProfilePhase7ReplaySourceRoot  = "PHEBS_T4013_PHASE7_REPLAY_SOURCE_ROOT"
	fullProfilePhase7HostAttestation   = "PHEBS_T4013_HOST_STABILITY_ATTESTATION"
	fullProfilePhase7Attestation       = "dedicated-single-operator-host-with-tool-mutation-disabled"
	fullProfilePhase7ReplaySchema      = "t4013-full-profile-phase7-replay-v2"
	fullProfilePhase7ReplayBoundary    = "after_stale_worker_before_pressure"
	fullProfilePhase7PreparationLimit  = 4 * time.Hour
)

type fullProfilePhase7ReplayResult struct {
	Schema                       string             `json:"schema"`
	Outcome                      string             `json:"outcome"`
	SourceCommit                 string             `json:"source_commit"`
	PlanSchema                   string             `json:"plan_schema"`
	PlanDigest                   string             `json:"plan_digest"`
	Profiles                     []string           `json:"profiles"`
	Phases                       []PhaseObservation `json:"phases"`
	Boundary                     string             `json:"boundary"`
	PressureStarted              bool               `json:"pressure_started"`
	TerminalDataGaugeDeadlineMS  int64              `json:"terminal_data_gauge_deadline_ms"`
	CleanupObservationSHA256     string             `json:"cleanup_observation_sha256"`
	CustodyAndSupervisionRetired bool               `json:"custody_and_supervision_retired"`
	EstablishesCeremonyPass      bool               `json:"establishes_ceremony_pass"`
	EstablishesScaleOrSLO        bool               `json:"establishes_scale_or_slo"`
	AuthorizesFreezeOrRelease    bool               `json:"authorizes_freeze_or_release"`
}

// TestProductionFullProfilePhase7Replay authors both frozen profiles and runs
// the exact production prefix through stale_worker. It is intentionally opt-in
// because the corpus is large and the prefix can consume most of the frozen
// twelve-hour execution ceiling.
func TestProductionFullProfilePhase7Replay(t *testing.T) {
	if os.Getenv(fullProfilePhase7ReplayEnvironment) != "1" {
		t.Skip("set " + fullProfilePhase7ReplayEnvironment + "=1 to run the exact full-profile Phase 7 replay")
	}
	if runtime.GOOS != "darwin" {
		t.Fatal("full-profile Phase 7 replay requires macOS")
	}
	if os.Getenv(fullProfilePhase7HostAttestation) != fullProfilePhase7Attestation {
		t.Fatal("full-profile Phase 7 replay requires the exact dedicated-host attestation")
	}
	if os.Getenv(runRootLockEnv) != "" {
		t.Fatal("full-profile Phase 7 replay refuses an ambient run-root lock descriptor")
	}
	commit := os.Getenv(fullProfilePhase7ReplayCommit)
	if !hexIdentity(commit, 40) {
		t.Fatal("full-profile Phase 7 replay requires one lowercase 40-hex source commit")
	}
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot, err = filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	if moduleRoot != os.Getenv(fullProfilePhase7ReplaySourceRoot) {
		t.Fatal("full-profile Phase 7 replay is not running from its exact source export")
	}
	runRoot, err := validateFullProfilePhase7RunRoot(os.Getenv(fullProfilePhase7ReplayRoot), moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("full-profile Phase 7 replay root: %s", runRoot)

	outerLock, err := lockRunRoot(runRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := outerLock.Close(); err != nil {
			t.Errorf("close full-profile Phase 7 run-root lock: %v", err)
		}
	}()
	lockDescriptor, err := outerLock.inheritedDescriptorValue()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(runRootLockEnv, lockDescriptor)

	preparationContext, cancelPreparation := context.WithTimeout(
		t.Context(), fullProfilePhase7PreparationLimit,
	)
	defer cancelPreparation()
	plan, err := FrozenHostPlanAtCheckout(preparationContext, moduleRoot, commit)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchemaV32 {
		t.Fatalf("full-profile Phase 7 replay plan schema = %s", plan.Schema)
	}
	if err := validateFullProfilePhase7ToolBinding(plan, "go", os.Getenv(fullProfilePhase7ReplayGoSHA256)); err != nil {
		t.Fatal(err)
	}
	if err := validateFullProfilePhase7ToolBinding(plan, "git", os.Getenv(fullProfilePhase7ReplayGitSHA256)); err != nil {
		t.Fatal(err)
	}
	planRaw, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(runRoot, "plan.json")
	if err := stageAtomicOutput(planPath, planRaw, MaxPlanBytes, false); err != nil {
		t.Fatal(err)
	}
	if err := publishAtomicOutput(planPath, planRaw, MaxPlanBytes, false); err != nil {
		t.Fatal(err)
	}

	workspace := filepath.Join(runRoot, "custody")
	preparedPath := filepath.Join(runRoot, "prepared.json")
	prepared, err := PrepareToOutput(preparationContext, PrepareRequest{
		ModuleRoot: moduleRoot, Workspace: workspace, PlanPath: planPath,
		Confirm: PrepareConfirm, BasePort: 41731,
	}, preparedPath)
	if err != nil {
		t.Fatalf("prepare full-profile Phase 7 replay; private root retained: %v",
			errors.Join(err, context.Cause(preparationContext)))
	}
	if err := context.Cause(preparationContext); err != nil {
		t.Fatalf("full-profile Phase 7 preparation deadline crossed before execution; private root retained: %v", err)
	}
	cancelPreparation()

	cleanupObservationPath := filepath.Join(runRoot, "cleanup-observation.json")
	run, err := newExecution(t.Context(), ExecuteRequest{
		ModuleRoot: moduleRoot, PlanPath: planPath, Prepared: preparedPath,
		Observation: cleanupObservationPath, Confirm: ExecuteConfirm,
	}, time.Now())
	if err != nil {
		if custodyRetentionCause(t.Context(), err) == nil {
			err = errors.Join(err, CleanupPrepared(moduleRoot, planPath, preparedPath, CleanupConfirm))
		}
		t.Fatalf("admit full-profile Phase 7 replay; private root retained: %v", err)
	}
	defer func() {
		if run.executionCancel != nil {
			run.executionCancel()
		}
		if err := run.supervision.Close(); err != nil {
			t.Errorf("close full-profile Phase 7 supervision: %v", err)
		}
		if err := run.runRootLock.Close(); err != nil {
			t.Errorf("close full-profile Phase 7 admission lock: %v", err)
		}
	}()
	stopAfterReplayFailure := func(summary string, cause error) {
		t.Helper()
		stopped, cleanupErr := run.stopAfterFailure(cause)
		if stopped.Schema != "" {
			t.Logf("source-free stopped observation: %s", cleanupObservationPath)
		}
		t.Fatalf("%s; private root retained unless teardown completed: %v",
			summary, errors.Join(cause, cleanupErr))
	}

	if err := run.executeThroughStaleWorker(); err != nil {
		stopAfterReplayFailure("full-profile Phase 7 prefix failed", err)
	}
	if err := validateFullProfilePhase7Prefix(run); err != nil {
		stopAfterReplayFailure("full-profile Phase 7 prefix is invalid", err)
	}
	if err := verifyFullProfilePhase7ExactSourceWithBoundGit(
		run.ctx, moduleRoot, commit, run.hostTools.gitCore,
		executionEnvironmentForControls(run.toolchain.controls, false),
	); err != nil {
		stopAfterReplayFailure("full-profile Phase 7 checkout changed before cleanup", err)
	}

	// The production stopped-teardown protocol supplies the resumable external
	// checkpoint and descendant-absence proof. This deliberate boundary is
	// classified separately from a real pressure failure, and no pressure code
	// is called.
	run.startPhase(7)
	stopped, cleanupErr := run.stopAfterFailure(errFullProfilePhase7Boundary)
	if cleanupErr != nil {
		t.Fatalf("retire full-profile Phase 7 custody; private root retained if cleanup is unproven: %v", cleanupErr)
	}
	if err := validateFullProfilePhase7Cleanup(stopped); err != nil {
		t.Fatalf("full-profile Phase 7 cleanup observation is invalid: %v", err)
	}
	cleanupRaw, err := readAtomicRegular(cleanupObservationPath, MaxObservationBytes)
	if err != nil {
		t.Fatal(err)
	}
	wantCleanupRaw, err := MarshalObservation(stopped)
	if err != nil || !bytes.Equal(cleanupRaw, wantCleanupRaw) {
		t.Fatalf("full-profile Phase 7 cleanup observation changed: %v", err)
	}
	if err := confirmFullProfilePhase7Retirement(run, preparedPath, cleanupObservationPath); err != nil {
		t.Fatal(err)
	}
	if err := verifyFullProfilePhase7ExactSourceWithBoundGit(
		t.Context(), moduleRoot, commit, run.hostTools.gitCore, gitEnvironmentForContract(true),
	); err != nil {
		t.Fatal(err)
	}

	result := fullProfilePhase7ReplayResult{
		Schema: fullProfilePhase7ReplaySchema, Outcome: "passed",
		SourceCommit: commit, PlanSchema: plan.Schema, PlanDigest: PlanDigest(planRaw),
		Profiles: []string{prepared.Profiles[0].Name, prepared.Profiles[1].Name},
		Phases:   slices.Clone(stopped.Phases[:7]), Boundary: fullProfilePhase7ReplayBoundary,
		TerminalDataGaugeDeadlineMS: frozenDataMeasurementDeadlineV31MS,
		CleanupObservationSHA256:    digest(cleanupRaw), CustodyAndSupervisionRetired: true,
	}
	resultRaw, err := marshalFullProfilePhase7ReplayResult(result)
	if err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(runRoot, "phase7-replay.json")
	if err := stageAtomicOutput(resultPath, resultRaw, MaxObservationBytes, false); err != nil {
		t.Fatal(err)
	}
	if err := publishAtomicOutput(resultPath, resultRaw, MaxObservationBytes, false); err != nil {
		t.Fatal(err)
	}
	published, err := readAtomicRegular(resultPath, MaxObservationBytes)
	if err != nil || !bytes.Equal(published, resultRaw) {
		t.Fatalf("full-profile Phase 7 result changed during publication: %v", err)
	}
	t.Logf("full-profile Phase 7 source-free result: %s", resultPath)
}

func validateFullProfilePhase7RunRoot(path, moduleRoot string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return "", errors.New("full-profile Phase 7 run root must be absolute and non-root")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != filepath.Clean(path) || real == moduleRoot || isWithin(real, moduleRoot) || isWithin(moduleRoot, real) {
		return "", errors.Join(err, errors.New("full-profile Phase 7 run root is invalid"))
	}
	info, err := os.Lstat(real)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return "", errors.Join(err, errors.New("full-profile Phase 7 run root must be a private real directory"))
	}
	entries, err := os.ReadDir(real)
	if err != nil || len(entries) != 0 {
		return "", errors.Join(err, errors.New("full-profile Phase 7 run root must be empty"))
	}
	return real, nil
}

func validateFullProfilePhase7ToolBinding(plan Plan, name, expected string) error {
	if !digestIdentity(expected) {
		return fmt.Errorf("full-profile Phase 7 replay %s driver digest is invalid", name)
	}
	for _, tool := range plan.HostToolchain {
		if tool.Name == name {
			if tool.SHA256 != expected {
				return fmt.Errorf("full-profile Phase 7 replay %s driver differs from the frozen plan", name)
			}
			return nil
		}
	}
	return fmt.Errorf("full-profile Phase 7 replay frozen %s driver is absent", name)
}

func verifyFullProfilePhase7ExactSourceWithBoundGit(
	ctx context.Context, moduleRoot, sourceCommit string, git boundExecutable, environment []string,
) error {
	if err := verifyCleanCheckoutWithBoundGit(ctx, moduleRoot, sourceCommit, git, environment); err != nil {
		return err
	}
	path, err := git.pathForLaunch(ctx)
	if err != nil {
		return err
	}
	status, err := gitOutputWithExecutableEnvironment(ctx, moduleRoot, path, environment,
		"status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching")
	if err != nil || status != "" {
		return errors.New("full-profile Phase 7 exact source has extra build inputs")
	}
	return nil
}

func validateFullProfilePhase7Prefix(run *execution) error {
	if run == nil || run.phase != 6 || len(run.observation.Phases) != len(phaseOrder) ||
		len(run.activeMeters) != 0 || run.metersTracked != run.metersExpected || run.measurementErr != nil {
		return errors.New("full-profile Phase 7 execution accounting is incomplete")
	}
	for index, phase := range run.observation.Phases {
		want := "not_run"
		if index <= 6 {
			want = "succeeded"
		}
		if phase.Name != phaseOrder[index] || phase.Outcome != want || index <= 6 && !phase.OracleExact {
			return fmt.Errorf("full-profile Phase 7 phase %d is invalid", index)
		}
	}
	terminal := run.observation.Phases[6].Metrics
	if terminal.WallMS <= 0 || terminal.DataLogicalBytes <= 0 || terminal.DataAllocatedBytes <= 0 {
		return errors.New("full-profile Phase 7 terminal gauge is absent")
	}
	return nil
}

func validateFullProfilePhase7Cleanup(value Observation) error {
	if err := ValidateObservation(value); err != nil {
		return err
	}
	if value.Outcome != "stopped" || len(value.Failures) != 1 || value.Failures[0] != (FailureObservation{
		Phase: "pressure", Class: "execution", Code: "phase7_replay_boundary_reached",
	}) || value.Decision != (DecisionObservation{
		Selected: "unclassified", Reason: "phase7_replay_boundary_reached",
	}) || !value.Teardown.Completed || value.Teardown.DerivedDataRetained || value.Teardown.ScratchSourceRetained {
		return errors.New("full-profile Phase 7 cleanup boundary is invalid")
	}
	for index, phase := range value.Phases {
		want := "not_run"
		switch {
		case index <= 6, index == 11:
			want = "succeeded"
		case index == 7:
			want = "failed"
		}
		if phase.Name != phaseOrder[index] || phase.Outcome != want {
			return fmt.Errorf("full-profile Phase 7 cleanup phase %d is invalid", index)
		}
	}
	return nil
}

func confirmFullProfilePhase7Retirement(run *execution, preparedPath, cleanupObservationPath string) error {
	if run == nil || run.supervision == nil || !digestIdentity(run.checkpointDigest) {
		return errors.New("full-profile Phase 7 retirement identity is unavailable")
	}
	if err := confirmCustodyDeletionDurable(run.workspace); err != nil {
		return err
	}
	if err := confirmCustodySupervisionRetired(
		run.workspace, PlanDigest(run.planBytes), run.supervision.Token(),
		custodyOperationExecute, run.checkpointDigest,
	); err != nil {
		return err
	}
	for _, path := range []string{
		preparedPath, preparedPath + ".tmp", preparedPath + ".preparing",
		cleanupObservationPath + ".tmp", cleanupObservationPath + ".teardown",
		cleanupObservationPath + ".teardown.tmp",
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.Join(err, fmt.Errorf("full-profile Phase 7 retirement left %s", filepath.Base(path)))
		}
	}
	addresses := make([]string, 0, len(run.prepared.Profiles))
	for _, profile := range run.prepared.Profiles {
		addresses = append(addresses, profile.Address)
	}
	listeners, err := reserveLoopbackAddresses(addresses...)
	if err != nil {
		return fmt.Errorf("rebind full-profile Phase 7 ports: %w", err)
	}
	return releaseLoopbackAddresses(listeners)
}

func marshalFullProfilePhase7ReplayResult(value fullProfilePhase7ReplayResult) ([]byte, error) {
	if err := validateFullProfilePhase7ReplayResult(value); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if len(raw) > MaxObservationBytes {
		return nil, errors.New("full-profile Phase 7 result exceeds its fixed byte bound")
	}
	return raw, nil
}

func validateFullProfilePhase7ReplayResult(value fullProfilePhase7ReplayResult) error {
	if value.Schema != fullProfilePhase7ReplaySchema || value.Outcome != "passed" ||
		!hexIdentity(value.SourceCommit, 40) || value.PlanSchema != PlanSchemaV32 ||
		!digestIdentity(value.PlanDigest) || value.Boundary != fullProfilePhase7ReplayBoundary ||
		value.PressureStarted || value.TerminalDataGaugeDeadlineMS != frozenDataMeasurementDeadlineV31MS ||
		!digestIdentity(value.CleanupObservationSHA256) || !value.CustodyAndSupervisionRetired ||
		value.EstablishesCeremonyPass || value.EstablishesScaleOrSLO || value.AuthorizesFreezeOrRelease ||
		!slices.Equal(value.Profiles, []string{"structural-2m-v1", "semantic-262144-v1"}) ||
		len(value.Phases) != 7 {
		return errors.New("full-profile Phase 7 result identity is invalid")
	}
	for index, phase := range value.Phases {
		if phase.Name != phaseOrder[index] || phase.Outcome != "succeeded" || !phase.OracleExact ||
			!nonnegativeMetrics(phase.Metrics) || !validProcessClassTransitions(phase.Metrics, true) {
			return errors.New("full-profile Phase 7 result phase inventory is invalid")
		}
	}
	terminal := value.Phases[6].Metrics
	if terminal.WallMS <= 0 || terminal.DataLogicalBytes <= 0 || terminal.DataAllocatedBytes <= 0 {
		return errors.New("full-profile Phase 7 result lacks its terminal data gauge")
	}
	return nil
}

func TestFullProfilePhase7ReplayResultValidation(t *testing.T) {
	digestValue := "sha256:" + fmt.Sprintf("%064x", 1)
	valid := func() fullProfilePhase7ReplayResult {
		phases := make([]PhaseObservation, 7)
		for index := range phases {
			phases[index] = succeededPhase(phaseOrder[index], PhaseMetrics{WallMS: 1})
		}
		phases[6].Metrics.DataLogicalBytes = 1
		phases[6].Metrics.DataAllocatedBytes = 1
		return fullProfilePhase7ReplayResult{
			Schema: fullProfilePhase7ReplaySchema, Outcome: "passed",
			SourceCommit: "0123456789abcdef0123456789abcdef01234567",
			PlanSchema:   PlanSchemaV32, PlanDigest: digestValue,
			Profiles: []string{"structural-2m-v1", "semantic-262144-v1"}, Phases: phases,
			Boundary:                     fullProfilePhase7ReplayBoundary,
			TerminalDataGaugeDeadlineMS:  frozenDataMeasurementDeadlineV31MS,
			CleanupObservationSHA256:     digestValue,
			CustodyAndSupervisionRetired: true,
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*fullProfilePhase7ReplayResult)
		want   bool
	}{
		{name: "valid", want: true},
		{name: "pressure started", mutate: func(value *fullProfilePhase7ReplayResult) { value.PressureStarted = true }},
		{name: "missing terminal gauge", mutate: func(value *fullProfilePhase7ReplayResult) { value.Phases[6].Metrics.DataAllocatedBytes = 0 }},
		{name: "missing phase", mutate: func(value *fullProfilePhase7ReplayResult) { value.Phases = value.Phases[:6] }},
		{name: "ceremony claim", mutate: func(value *fullProfilePhase7ReplayResult) { value.EstablishesCeremonyPass = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := valid()
			if test.mutate != nil {
				test.mutate(&value)
			}
			if err := validateFullProfilePhase7ReplayResult(value); (err == nil) != test.want {
				t.Fatalf("validate result = %v, want success %t", err, test.want)
			}
		})
	}
}

func TestFullProfilePhase7ToolBinding(t *testing.T) {
	digestValue := "sha256:" + fmt.Sprintf("%064x", 1)
	for _, name := range []string{"go", "git"} {
		t.Run(name, func(t *testing.T) {
			plan := Plan{HostToolchain: []HostToolObservation{{Name: name, SHA256: digestValue}}}
			if err := validateFullProfilePhase7ToolBinding(plan, name, digestValue); err != nil {
				t.Fatal(err)
			}
			if err := validateFullProfilePhase7ToolBinding(plan, name, "sha256:"+fmt.Sprintf("%064x", 2)); err == nil {
				t.Fatalf("full-profile Phase 7 replay accepted a different %s driver", name)
			}
		})
	}
}

func TestV31FullProfilePhase7BoundaryClassification(t *testing.T) {
	classification := classifyStoppedFailureForPlan(
		Plan{Schema: PlanSchemaV31}, errFullProfilePhase7Boundary, nil, nil,
	)
	if classification != (stoppedClassification{
		class: "execution", code: "phase7_replay_boundary_reached",
		decision: "unclassified", reason: "phase7_replay_boundary_reached",
	}) {
		t.Fatalf("V31 Phase 7 boundary classification = %+v", classification)
	}
	legacy := classifyStoppedFailureForPlan(
		Plan{Schema: PlanSchemaV30}, errFullProfilePhase7Boundary, nil, nil,
	)
	if legacy.code != "operational_failure" {
		t.Fatalf("V30 Phase 7 boundary classification = %+v", legacy)
	}
	value := Receipt{
		Schema: ReceiptSchemaV31, Outcome: "stopped",
		Failures: []FailureObservation{{Phase: "pressure", Class: "execution", Code: "phase7_replay_boundary_reached"}},
		Decision: DecisionObservation{Selected: "unclassified", Reason: "phase7_replay_boundary_reached"},
		Teardown: TeardownObservation{Completed: true}, Phases: make([]PhaseObservation, len(phaseOrder)),
	}
	for index, name := range phaseOrder {
		outcome := "not_run"
		switch {
		case index <= 6, index == 11:
			outcome = "succeeded"
		case index == 7:
			outcome = "failed"
		}
		value.Phases[index] = PhaseObservation{Name: name, Outcome: outcome}
	}
	if err := validateStopped(value); err != nil {
		t.Fatalf("validate V31 Phase 7 boundary: %v", err)
	}
	value.Schema = ReceiptSchemaV30
	if err := validateStopped(value); err == nil {
		t.Fatal("historical receipt accepted the V31 Phase 7 boundary")
	}
}

func TestV31FullProfilePhase7BoundaryUsesResumableStoppedTeardown(t *testing.T) {
	run, workspace, observationPath := newFullProfilePhase7BoundaryExecution(t)
	run.startPhase(7)
	stopped, err := run.stopAfterFailure(errFullProfilePhase7Boundary)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFullProfilePhase7Cleanup(stopped); err != nil {
		t.Fatal(err)
	}
	raw, err := readAtomicRegular(observationPath, MaxObservationBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildReceipt(run.planBytes, raw, PlanDigest(run.planBytes)); err != nil {
		t.Fatalf("build full-profile Phase 7 cleanup receipt: %v", err)
	}
	if _, err := os.Lstat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("full-profile Phase 7 stopped teardown retained custody: %v", err)
	}
	if _, err := os.Lstat(observationPath + ".teardown"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("full-profile Phase 7 stopped teardown retained its checkpoint: %v", err)
	}
}

func TestV31FullProfilePhase7BoundaryRejectsServerStopError(t *testing.T) {
	run, _, _ := newFullProfilePhase7BoundaryExecution(t)
	run.serverShutdownErr = errors.New("late server stop failure")
	run.startPhase(7)
	stopped, err := run.stopAfterFailure(errFullProfilePhase7Boundary)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFullProfilePhase7Cleanup(stopped); err == nil {
		t.Fatal("full-profile Phase 7 boundary accepted a server stop failure")
	}
	if len(stopped.Failures) != 1 || stopped.Failures[0].Code != "operational_failure" {
		t.Fatalf("server stop classification = %+v", stopped.Failures)
	}
}

func newFullProfilePhase7BoundaryExecution(t *testing.T) (*execution, string, string) {
	t.Helper()
	root := t.TempDir()
	module := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	for _, path := range []string{module, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	run := newV31FailureExecution(t, module, workspace)
	run.observation = completedV25TeardownObservation(run.plan)
	for index := 7; index < len(run.observation.Phases); index++ {
		run.observation.Phases[index] = PhaseObservation{Name: phaseOrder[index], Outcome: "not_run"}
	}
	run.observation.Failures = nil
	run.observation.Collection = nil
	run.observation.AuthorizedQuery = nil
	run.observation.Decision = DecisionObservation{
		Selected: "continue", Reason: "execution_in_progress", Substantiated: true,
	}
	run.observation.Teardown = TeardownObservation{}
	observationPath := setExecutionObservationPath(t, run)
	return run, workspace, observationPath
}
