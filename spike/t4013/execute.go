package t4013

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	apiresponse "github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

const ExecuteConfirm = "execute-neutral-t4013-and-destroy-custody"

var ErrGateStopped = errors.New("T40.13 exact mechanics gate stopped")

var errReviewCeiling = errors.New("T40.13 frozen review ceiling crossed")

type ExecuteRequest struct {
	ModuleRoot  string
	PlanPath    string
	Prepared    string
	Observation string
	Confirm     string
}

type execution struct {
	ctx            context.Context
	moduleRoot     string
	workspace      string
	plan           Plan
	planBytes      []byte
	prepared       Prepared
	toolchain      privateToolchain
	observation    Observation
	structural     *privateServer
	semantic       *privateServer
	structA        privateProfileSnapshot
	structB        privateProfileSnapshot
	structAR       privateProfileSnapshot
	semanticA      privateProfileSnapshot
	phase          int
	phaseStarted   time.Time
	activeMeters   map[*phaseMeter]struct{}
	partialMetrics PhaseMetrics
	metersTracked  int
}

func Execute(ctx context.Context, request ExecuteRequest) (Observation, error) {
	run, err := newExecution(ctx, request)
	if err != nil {
		return Observation{}, err
	}
	executionContext, cancel := context.WithTimeout(
		ctx, time.Duration(run.plan.Safety.MaximumTotalWallMS)*time.Millisecond,
	)
	defer cancel()
	run.ctx = executionContext
	if err := run.execute(); err != nil {
		observation, cleanupErr := run.stopAfterFailure(err)
		if cleanupErr != nil {
			return Observation{}, errors.Join(err, cleanupErr)
		}
		return observation, ErrGateStopped
	}
	return run.observation, nil
}

func newExecution(ctx context.Context, request ExecuteRequest) (*execution, error) {
	if ctx == nil || request.Confirm != ExecuteConfirm || !filepath.IsAbs(request.ModuleRoot) ||
		!filepath.IsAbs(request.PlanPath) || !filepath.IsAbs(request.Prepared) || !filepath.IsAbs(request.Observation) {
		return nil, errors.New("T40.13 execution request is invalid")
	}
	moduleRoot, err := filepath.EvalSymlinks(request.ModuleRoot)
	if err != nil {
		return nil, err
	}
	planBytes, err := os.ReadFile(request.PlanPath)
	if err != nil {
		return nil, err
	}
	plan, err := DecodePlan(planBytes)
	if err != nil {
		return nil, err
	}
	preparedBytes, err := os.ReadFile(request.Prepared)
	if err != nil {
		return nil, err
	}
	prepared, err := DecodePrepared(preparedBytes, PlanDigest(planBytes))
	if err != nil {
		return nil, err
	}
	workspace := filepath.Dir(filepath.Dir(prepared.Profiles[0].Config))
	if filepath.Dir(filepath.Dir(prepared.Profiles[1].Config)) != workspace ||
		workspace == moduleRoot || isWithin(workspace, moduleRoot) || isWithin(moduleRoot, workspace) ||
		isWithin(request.Observation, workspace) {
		return nil, errors.New("T40.13 execution custody boundary is invalid")
	}
	if err := validatePreparedFiles(prepared, workspace); err != nil {
		return nil, err
	}
	info, err := os.Lstat(workspace)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("T40.13 execution custody is not a real directory")
	}
	if _, err := os.Lstat(request.Observation); err == nil || !os.IsNotExist(err) {
		return nil, errors.New("T40.13 observation output must not exist")
	}
	if err := VerifyInputs(moduleRoot); err != nil {
		return nil, err
	}
	if err := verifyCleanCheckout(ctx, moduleRoot, plan.SourceCommit); err != nil {
		return nil, err
	}
	environment, err := HostPreflight(ctx, filepath.Dir(workspace), plan)
	if err != nil {
		return nil, err
	}
	observation := emptyObservation(environment)
	return &execution{
		ctx: ctx, moduleRoot: moduleRoot, workspace: workspace,
		plan: plan, planBytes: planBytes, prepared: prepared, observation: observation,
	}, nil
}

func validatePreparedFiles(prepared Prepared, workspace string) error {
	for _, profile := range prepared.Profiles {
		for _, path := range []string{profile.Repository, profile.Config, profile.Credential, profile.DataDir, profile.Catalog} {
			if !isWithin(path, workspace) {
				return errors.New("T40.13 prepared path escaped custody")
			}
		}
		for _, path := range []string{profile.Config, profile.Credential, profile.Catalog} {
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("T40.13 prepared control is not a regular file")
			}
		}
		for _, path := range []string{profile.Repository, profile.DataDir} {
			info, err := os.Lstat(path)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("T40.13 prepared directory is invalid")
			}
		}
		credential, err := os.ReadFile(profile.Credential)
		if err != nil {
			return err
		}
		parsed, err := config.Load(profile.Config)
		if err != nil || parsed.Server.DataDir != profile.DataDir || parsed.Server.Addr != profile.Address ||
			parsed.Auth.APIKey != string(bytesTrimSpace(credential)) || len(parsed.Connections) != 1 ||
			parsed.Connections[0].Type != "git" || parsed.Connections[0].URL != profile.Repository ||
			len(parsed.ServiceCatalogs) != 1 {
			return errors.New("T40.13 prepared config differs from custody")
		}
		catalogConfig, ok := parsed.ServiceCatalogs[profile.RepositoryName]
		if !ok || catalogConfig.Path != profile.Catalog || catalogConfig.Kind != servicecatalog.AuthorityOperator {
			return errors.New("T40.13 prepared catalog selection differs from custody")
		}
	}
	return nil
}

func (run *execution) execute() error {
	run.startPhase(0)
	if extractionpublication.ScheduleMaxAttempts != run.plan.Safety.MaximumRetriesPerUnit {
		return errors.New("T40.13 production retry ceiling differs from the frozen plan")
	}
	preflightStarted := time.Now()
	toolchain, err := buildPrivateToolchain(run.ctx, run.moduleRoot, run.workspace)
	if err != nil {
		return err
	}
	if err := validateToolchain(toolchain); err != nil {
		return err
	}
	run.toolchain = toolchain
	run.observation.Toolchain, err = observeToolchain(toolchain)
	if err != nil {
		return err
	}
	run.observation.Phases[0] = succeededPhase("preflight", PhaseMetrics{
		WallMS: time.Since(preflightStarted).Milliseconds(), OtherChildren: 4,
	})
	if err := run.enforceSafety(); err != nil {
		return err
	}

	run.startPhase(1)
	if err := run.cold(); err != nil {
		return err
	}
	run.startPhase(2)
	if err := run.warmNoop(); err != nil {
		return err
	}
	run.startPhase(3)
	if err := run.deltaAndReturn(); err != nil {
		return err
	}
	run.startPhase(5)
	if err := run.interruption(); err != nil {
		return err
	}
	run.startPhase(6)
	if err := run.staleWorker(); err != nil {
		return err
	}
	run.startPhase(7)
	if err := run.pressure(); err != nil {
		return err
	}
	run.startPhase(8)
	if err := run.archiveRestore(); err != nil {
		return err
	}
	run.startPhase(9)
	if err := run.collection(); err != nil {
		return err
	}
	run.startPhase(10)
	if err := run.authorizedQueries(); err != nil {
		return err
	}
	if err := run.finalizeObservation(); err != nil {
		return err
	}
	run.startPhase(11)
	if err := run.teardown(); err != nil {
		return err
	}
	return ValidateObservation(run.observation)
}

func (run *execution) stopAfterFailure(cause error) (Observation, error) {
	started := time.Now()
	stopErr := run.stopServers()
	measurementErr := run.captureFailedPhase()
	ceilingErr := run.enforceSafety()
	info, err := os.Lstat(run.workspace)
	if errors.Is(err, os.ErrNotExist) && run.observation.Teardown.Completed {
		// A ceiling crossed only after the successful destructive teardown.
	} else {
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
			run.workspace == run.moduleRoot || isWithin(run.moduleRoot, run.workspace) {
			return Observation{}, errors.New("T40.13 stopped-run custody changed")
		}
		if err := os.RemoveAll(run.workspace); err != nil {
			return Observation{}, err
		}
		if _, err := os.Lstat(run.workspace); !os.IsNotExist(err) {
			return Observation{}, errors.New("T40.13 stopped-run teardown left custody behind")
		}
	}
	if run.phase != len(run.observation.Phases)-1 {
		run.observation.Phases[len(run.observation.Phases)-1] = succeededPhase("teardown", PhaseMetrics{
			WallMS: time.Since(started).Milliseconds(),
		})
	}
	run.observation.Outcome = "stopped"
	failureClass, failureCode := "oracle", "exact_gate_failed"
	decision, reason, substantiated := "reduce", "exact_mechanics_oracle_failed", true
	switch {
	case errors.Is(cause, errReviewCeiling) || errors.Is(ceilingErr, errReviewCeiling):
		failureClass, failureCode = "environment", "review_ceiling_crossed"
		decision, reason, substantiated = "cohort_experiment", "frozen_review_ceiling_crossed", true
	case measurementErr != nil:
		failureCode = "failed_phase_measurement_unavailable"
		decision, reason, substantiated = "unclassified", "failed_phase_measurement_unavailable", false
	case run.phase == 5 || run.phase == 8:
		failureClass, failureCode = "recovery", "direct_recovery_failed"
		decision, reason, substantiated = "p6_investigation", "direct_recovery_failed", true
	}
	run.observation.Failures = []FailureObservation{{
		Phase: phaseOrder[run.phase], Class: failureClass, Code: failureCode,
	}}
	run.observation.Decision = DecisionObservation{
		Selected: decision, Reason: reason, Substantiated: substantiated,
	}
	run.observation.Teardown = TeardownObservation{Completed: true}
	run.observation.Checks[len(run.observation.Checks)-1].Passed = false
	if err := ValidateObservation(run.observation); err != nil {
		return Observation{}, err
	}
	return run.observation, stopErr
}

func (run *execution) startPhase(index int) {
	run.phase = index
	run.phaseStarted = time.Now()
	run.activeMeters = make(map[*phaseMeter]struct{})
	run.partialMetrics = PhaseMetrics{}
	run.metersTracked = 0
}

func (run *execution) trackMeter(meter *phaseMeter) {
	if meter != nil {
		run.activeMeters[meter] = struct{}{}
		run.metersTracked++
	}
}

func (run *execution) finishMeter(meter *phaseMeter, after *privateProfileSnapshot) (PhaseMetrics, error) {
	metrics, err := meter.finish(after)
	delete(run.activeMeters, meter)
	if err == nil {
		run.partialMetrics = mergeMetrics(run.partialMetrics, metrics)
	}
	return metrics, err
}

func (run *execution) captureFailedPhase() error {
	if run.phase < 0 || run.phase >= len(run.observation.Phases) {
		return errors.New("T40.13 failed phase is invalid")
	}
	metrics := run.partialMetrics
	if run.observation.Phases[run.phase].Outcome != "not_run" {
		metrics = run.observation.Phases[run.phase].Metrics
	}
	var captureErr error
	if run.metersTracked < expectedPhaseMeters(run.phase) {
		captureErr = errors.New("T40.13 failed phase lacks its complete meter inventory")
	}
	for meter := range run.activeMeters {
		measured, err := meter.finish(nil)
		delete(run.activeMeters, meter)
		captureErr = errors.Join(captureErr, err)
		if err == nil {
			metrics = mergeMetrics(metrics, measured)
		}
	}
	if metrics.DataAllocatedBytes == 0 {
		logical, allocated, err := measureDataBytes(run.workspace)
		captureErr = errors.Join(captureErr, err)
		if err == nil {
			metrics.DataLogicalBytes, metrics.DataAllocatedBytes = logical, allocated
		}
	}
	if !run.phaseStarted.IsZero() {
		metrics.WallMS = time.Since(run.phaseStarted).Milliseconds()
	}
	run.activeMeters = nil
	run.observation.Phases[run.phase] = PhaseObservation{
		Name: phaseOrder[run.phase], Outcome: "failed", Metrics: metrics,
		AuthorityChanged: metrics.PublicationTransactions > 0, OracleExact: false,
	}
	return captureErr
}

func expectedPhaseMeters(phase int) int {
	switch phase {
	case 1, 5, 10:
		return 2
	case 2, 3, 4, 6, 7, 8, 9:
		return 1
	default:
		return 0
	}
}

func (run *execution) cold() error {
	started := time.Now()
	structural, semantic := run.prepared.Profiles[0], run.prepared.Profiles[1]
	server, err := startPrivateServer(run.ctx, structural, run.toolchain, "cold")
	if err != nil {
		return err
	}
	run.structural = server
	meter, err := beginInitialPhaseMeter(server, run.workspace, nil)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	run.structA, err = waitSnapshot(run.ctx, structural, "a", 2*time.Hour)
	if err != nil {
		return err
	}
	structMetrics, err := run.finishMeter(meter, &run.structA)
	if err != nil {
		return err
	}
	if err := run.structural.stop(30 * time.Second); err != nil {
		return err
	}
	run.structural = nil
	server, err = startPrivateServer(run.ctx, semantic, run.toolchain, "cold")
	if err != nil {
		return err
	}
	run.semantic = server
	meter, err = beginInitialPhaseMeter(server, run.workspace, nil)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	run.semanticA, err = waitSnapshot(run.ctx, semantic, "a", 2*time.Hour)
	if err != nil {
		return err
	}
	semanticMetrics, err := run.finishMeter(meter, &run.semanticA)
	if err != nil {
		return err
	}
	if err := run.semantic.stop(30 * time.Second); err != nil {
		return err
	}
	run.semantic = nil
	metrics := mergeMetrics(structMetrics, semanticMetrics)
	metrics.WallMS = time.Since(started).Milliseconds()
	run.observation.Phases[1] = succeededPhase("cold", metrics)
	return run.enforceSafety()
}

func (run *execution) warmNoop() error {
	profile := run.prepared.Profiles[0]
	if run.structural != nil {
		if err := run.structural.stop(30 * time.Second); err != nil {
			return err
		}
		run.structural = nil
	}
	server, err := startPrivateServer(run.ctx, profile, run.toolchain, "warm-noop")
	if err != nil {
		return err
	}
	run.structural = server
	meter, err := beginInitialPhaseMeter(server, run.workspace, &run.structA)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	after, err := waitSnapshot(run.ctx, profile, "a", 20*time.Minute)
	if err != nil {
		return err
	}
	metrics, err := run.finishMeter(meter, &after)
	if err != nil {
		return err
	}
	if !privateSnapshotEqual(run.structA, after) || metrics.GitChildren != 0 || metrics.IndexChildren != 0 ||
		metrics.PublicationWrites != 0 || metrics.PublicationTransactions != 0 || metrics.ReusedControls == 0 {
		return errors.New("T40.13 warm no-op moved authority or performed content work")
	}
	run.observation.Phases[2] = succeededPhase("warm_noop", metrics)
	return run.enforceSafety()
}

func (run *execution) deltaAndReturn() error {
	profile := run.prepared.Profiles[0]
	meter, err := beginPhaseMeter(run.structural, run.workspace, &run.structA)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	if err := updateSourceRevision(run.ctx, profile.Repository, profile.Revisions["b"]); err != nil {
		return err
	}
	run.structB, err = waitSnapshot(run.ctx, profile, "b", 2*time.Hour)
	if err != nil {
		return err
	}
	if changedSourceMembers(run.structA, run.structB) != 1 {
		return errors.New("T40.13 B changed other than one source partition")
	}
	metrics, err := run.finishMeter(meter, &run.structB)
	if err != nil {
		return err
	}
	run.observation.Phases[3] = succeededPhase("delta_b", metrics)
	if err := run.enforceSafety(); err != nil {
		return err
	}

	run.startPhase(4)
	meter, err = beginPhaseMeter(run.structural, run.workspace, &run.structB)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	if err := updateSourceRevision(run.ctx, profile.Repository, profile.Revisions["a-return"]); err != nil {
		return err
	}
	run.structAR, err = waitSnapshot(run.ctx, profile, "a-return", 2*time.Hour)
	if err != nil {
		return err
	}
	if changedSourceMembers(run.structB, run.structAR) != 1 ||
		!equalStringSlices(run.structA.SourceMemberDigests, run.structAR.SourceMemberDigests) {
		return errors.New("T40.13 A return did not reproduce the frozen source partitions")
	}
	metrics, err = run.finishMeter(meter, &run.structAR)
	if err != nil {
		return err
	}
	run.observation.Phases[4] = succeededPhase("return_a", metrics)
	return run.enforceSafety()
}

func (run *execution) interruption() error {
	started := time.Now()
	profile := run.prepared.Profiles[1]
	if run.semantic != nil {
		if err := run.semantic.stop(30 * time.Second); err != nil {
			return err
		}
		run.semantic = nil
	}
	if err := backupAndReset(run.ctx, run.toolchain, profile, "interruption"); err != nil {
		return err
	}
	if err := verifyRestoredBoundary(run.ctx, profile, run.semanticA); err != nil {
		return err
	}
	server, err := startPrivateServer(run.ctx, profile, run.toolchain, "interruption-first")
	if err != nil {
		return err
	}
	meter, err := beginInitialPhaseMeter(server, run.workspace, &run.semanticA)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	if err := waitForDerivedPartial(run.ctx, profile.DataDir, 90*time.Minute); err != nil {
		_ = server.stop(30 * time.Second)
		return err
	}
	if err := server.stop(30 * time.Second); err != nil {
		return err
	}
	firstMetrics, err := run.finishMeter(meter, nil)
	if err != nil {
		return err
	}
	server, err = startPrivateServer(run.ctx, profile, run.toolchain, "interruption-restart")
	if err != nil {
		return err
	}
	run.semantic = server
	restartMeter, err := beginInitialPhaseMeter(server, run.workspace, &run.semanticA)
	if err != nil {
		return err
	}
	run.trackMeter(restartMeter)
	after, err := waitSnapshot(run.ctx, profile, "a", 2*time.Hour)
	if err != nil {
		return err
	}
	if snapshotAuthority(after) != snapshotAuthority(run.semanticA) {
		return errors.New("T40.13 interruption recovery changed exact authority")
	}
	restartMetrics, err := run.finishMeter(restartMeter, &after)
	if err != nil {
		return err
	}
	metrics := mergeMetrics(firstMetrics, restartMetrics)
	metrics.OtherChildren += 2
	run.semanticA = after
	if err := run.semantic.stop(30 * time.Second); err != nil {
		return err
	}
	run.semantic = nil
	metrics.WallMS = time.Since(started).Milliseconds()
	run.observation.Phases[5] = succeededPhase("interruption", metrics)
	return run.enforceSafety()
}

func (run *execution) staleWorker() error {
	profile := run.prepared.Profiles[0]
	meter, err := beginPhaseMeter(run.structural, run.workspace, &run.structAR)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	if err := updateSourceRevision(run.ctx, profile.Repository, profile.Revisions["b"]); err != nil {
		return err
	}
	if err := waitIndexedCommit(run.ctx, profile, profile.Revisions["b"], 90*time.Minute); err != nil {
		return err
	}
	started, err := waitChunkLifecycle(
		run.ctx, run.structural.logPath, meter.logOffset, "", "started", 90*time.Minute,
	)
	if err != nil {
		return err
	}
	if err := updateSourceRevision(run.ctx, profile.Repository, profile.Revisions["a-return"]); err != nil {
		return err
	}
	after, err := waitSnapshot(run.ctx, profile, "a-return", 2*time.Hour)
	if err != nil {
		return err
	}
	if after.SourceGeneration != run.structAR.SourceGeneration ||
		after.RelationshipGeneration != run.structAR.RelationshipGeneration {
		return errors.New("T40.13 stale worker moved final authority")
	}
	settled, err := waitChunkLifecycle(
		run.ctx, run.structural.logPath, meter.logOffset, started.Identity, "settled", 20*time.Minute,
	)
	if err != nil {
		return err
	}
	if settled.Generation != started.Generation || settled.Attempt != started.Attempt ||
		settled.Outcome != "stale_fenced" {
		return errors.New("T40.13 selected worker was not fenced stale after supersession")
	}
	metrics, err := run.finishMeter(meter, &after)
	if err != nil {
		return err
	}
	run.structAR = after
	run.observation.Phases[6] = succeededPhase("stale_worker", metrics)
	return run.enforceSafety()
}

func waitChunkLifecycle(
	ctx context.Context,
	logPath string,
	offset int64,
	identity string,
	event string,
	limit time.Duration,
) (generationscheduler.ChunkLifecycleReport, error) {
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		report, found, err := readChunkLifecycle(logPath, offset, identity, event)
		if err != nil {
			return generationscheduler.ChunkLifecycleReport{}, err
		}
		if found {
			return report, nil
		}
		select {
		case <-phase.Done():
			return generationscheduler.ChunkLifecycleReport{}, errors.New("T40.13 chunk lifecycle deadline expired")
		case <-ticker.C:
		}
	}
}

func readChunkLifecycle(
	logPath string,
	offset int64,
	identity string,
	event string,
) (generationscheduler.ChunkLifecycleReport, bool, error) {
	file, err := os.Open(logPath)
	if err != nil {
		return generationscheduler.ChunkLifecycleReport{}, false, err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(offset, 0); err != nil {
		return generationscheduler.ChunkLifecycleReport{}, false, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var report generationscheduler.ChunkLifecycleReport
		if decodeLogObject(scanner.Bytes(), "generation chunk lifecycle: ", &report) != nil ||
			report.Schema != generationscheduler.ChunkLifecycleSchema || report.Event != event ||
			report.Stage != extractionpublication.ScheduleStage ||
			identity != "" && report.Identity != identity {
			continue
		}
		if !digestIdentity(report.Identity) || !digestIdentity(report.Generation) || report.Attempt < 0 {
			return generationscheduler.ChunkLifecycleReport{}, false, errors.New("T40.13 chunk lifecycle report is invalid")
		}
		return report, true, nil
	}
	return generationscheduler.ChunkLifecycleReport{}, false, scanner.Err()
}

func (run *execution) pressure() error {
	profile := run.prepared.Profiles[0]
	meter, err := beginPhaseMeter(run.structural, run.workspace, &run.structAR)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	status, err := waitLifecycle(run.ctx, profile, false, 10*time.Minute)
	if err != nil {
		return err
	}
	if status.Capacity.Completeness != lifecycle.Exact || status.Capacity.Pressure == lifecycle.PressureUnavailable {
		return errors.New("T40.13 lifecycle capacity was not exact")
	}
	if status.Capacity.Pressure != lifecycle.PressureCollect && status.Capacity.Pressure != lifecycle.PressureRefuse {
		return errors.New("T40.13 frozen run did not reach the pressure exercise")
	}
	after, err := waitSnapshot(run.ctx, profile, "a-return", 20*time.Minute)
	if err != nil {
		return err
	}
	metrics, err := run.finishMeter(meter, &after)
	if err != nil {
		return err
	}
	run.observation.Phases[7] = succeededPhase("pressure", metrics)
	return run.enforceSafety()
}

func (run *execution) archiveRestore() error {
	profile := run.prepared.Profiles[0]
	if err := run.structural.stop(30 * time.Second); err != nil {
		return err
	}
	run.structural = nil
	started := time.Now()
	if err := backupAndReset(run.ctx, run.toolchain, profile, "archive-restore"); err != nil {
		return err
	}
	if err := verifyRestoredBoundary(run.ctx, profile, run.structAR); err != nil {
		return err
	}
	server, err := startPrivateServer(run.ctx, profile, run.toolchain, "archive-restore")
	if err != nil {
		return err
	}
	run.structural = server
	meter, err := beginInitialPhaseMeter(server, run.workspace, &run.structAR)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	after, err := waitSnapshot(run.ctx, profile, "a-return", 2*time.Hour)
	if err != nil {
		return err
	}
	if after.ObservationGeneration != run.structAR.ObservationGeneration ||
		after.RelationshipGeneration != run.structAR.RelationshipGeneration {
		return errors.New("T40.13 archive restore changed precious authority")
	}
	metrics, err := run.finishMeter(meter, &after)
	if err != nil {
		return err
	}
	metrics.WallMS = time.Since(started).Milliseconds()
	metrics.OtherChildren += 2
	metrics.DataLogicalBytes, metrics.DataAllocatedBytes, err = measureDataBytes(run.workspace)
	if err != nil {
		return err
	}
	run.structAR = after
	run.observation.Phases[8] = succeededPhase("archive_restore", metrics)
	return run.enforceSafety()
}

func (run *execution) collection() error {
	profile := run.prepared.Profiles[0]
	meter, err := beginPhaseMeter(run.structural, run.workspace, &run.structAR)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	if _, err := waitLifecycle(run.ctx, profile, true, 10*time.Minute); err != nil {
		return err
	}
	after, err := waitSnapshot(run.ctx, profile, "a-return", 20*time.Minute)
	if err != nil {
		return err
	}
	if snapshotAuthority(after) != snapshotAuthority(run.structAR) {
		return errors.New("T40.13 collection changed protected authority")
	}
	metrics, err := run.finishMeter(meter, &after)
	if err != nil {
		return err
	}
	run.observation.Phases[9] = succeededPhase("collection", metrics)
	return run.enforceSafety()
}

func (run *execution) authorizedQueries() error {
	started := time.Now()
	structMeter, err := beginPhaseMeter(run.structural, run.workspace, &run.structAR)
	if err != nil {
		return err
	}
	run.trackMeter(structMeter)
	semanticProfile := run.prepared.Profiles[1]
	semanticServer, err := startPrivateServer(run.ctx, semanticProfile, run.toolchain, "authorized-query")
	if err != nil {
		return err
	}
	run.semantic = semanticServer
	semanticMeter, err := beginInitialPhaseMeter(semanticServer, run.workspace, &run.semanticA)
	if err != nil {
		return err
	}
	run.trackMeter(semanticMeter)
	semanticAfter, err := waitSnapshot(run.ctx, semanticProfile, "a", 20*time.Minute)
	if err != nil {
		return err
	}
	if snapshotAuthority(semanticAfter) != snapshotAuthority(run.semanticA) {
		return errors.New("T40.13 authorized-query restart changed semantic authority")
	}
	structCount, structExact, err := queryProfile(run.ctx, run.prepared.Profiles[0], "service-000", false)
	if err != nil {
		return err
	}
	semanticCount, semanticExact, err := queryProfile(run.ctx, run.prepared.Profiles[1], "semantic", true)
	if err != nil {
		return err
	}
	if structCount < 0 || semanticCount < 0 || !structExact || !semanticExact {
		return errors.New("T40.13 authorized query oracle failed")
	}
	structAfter, err := waitSnapshot(run.ctx, run.prepared.Profiles[0], "a-return", 20*time.Minute)
	if err != nil {
		return err
	}
	structMetrics, err := run.finishMeter(structMeter, &structAfter)
	if err != nil {
		return err
	}
	semanticMetrics, err := run.finishMeter(semanticMeter, &semanticAfter)
	if err != nil {
		return err
	}
	metrics := mergeMetrics(structMetrics, semanticMetrics)
	metrics.ControlReads += 8
	metrics.MemberReads += int64(structCount + semanticCount)
	run.semanticA = semanticAfter
	if err := run.semantic.stop(30 * time.Second); err != nil {
		return err
	}
	run.semantic = nil
	metrics.WallMS = time.Since(started).Milliseconds()
	run.observation.Phases[10] = succeededPhase("authorized_query", metrics)
	return run.enforceSafety()
}

func (run *execution) finalizeObservation() error {
	structural := run.structAR
	semantic := run.semanticA
	if structural.ObservationRecords != 512 || structural.ObservationUnsupported != 0 ||
		semantic.ObservationRecords != 262_144 || semantic.ObservationUnsupported != 131_072 ||
		semantic.ExtractionFacts != 49_152 || semantic.ExtractionRows != 98_304 {
		return errors.New("T40.13 source-free semantic totals differ from the frozen oracle")
	}
	run.observation.Profiles = []ProfileObservation{
		{Name: structural.Name, RegularFiles: structural.RegularFiles, PhysicalOwners: structural.PhysicalOwners,
			EligibleGoFiles: 2_000_000, DeclaredSourceBytes: structural.DeclaredSourceBytes,
			SearchPublished: true, ApplicablePartitions: structural.ApplicablePartitions,
			SettledPartitions: structural.SettledPartitions, PublishedDomains: structural.PublishedDomains,
			RelationshipPublished: structural.RelationshipPublished},
		{Name: semantic.Name, RegularFiles: semantic.RegularFiles, PhysicalOwners: semantic.PhysicalOwners,
			EligibleGoFiles: 262_144, IDLCandidates: 32_768, DeclaredSourceBytes: semantic.DeclaredSourceBytes,
			SearchPublished: true, ApplicablePartitions: semantic.ApplicablePartitions,
			SettledPartitions: semantic.SettledPartitions, PublishedDomains: semantic.PublishedDomains,
			RelationshipPublished: semantic.RelationshipPublished},
	}
	run.observation.BlobReaders = []BlobReaderObservation{
		run.structA.BlobReader, run.structB.BlobReader, run.structAR.BlobReader, run.semanticA.BlobReader,
	}
	run.observation.Service = ServiceControlObservation{
		AcceptedServices: structural.AcceptedServices, Memberships: structural.Memberships,
		DistinctPaths: structural.Memberships + structural.UnownedPrefixes, UnownedPrefixes: structural.UnownedPrefixes,
		WithinV2PathLimit:     structural.Memberships+structural.UnownedPrefixes <= servicecatalog.MaxDistinctPaths,
		ExactMembershipOracle: structural.AcceptedServices == 100 && structural.Memberships == 100,
		ExactUnownedOracle:    structural.UnownedPrefixes == 101,
	}
	unavailable := structural.UnavailableDomains + semantic.UnavailableDomains
	run.observation.Explicit = ExplicitStateObservation{
		AbsentTypedInputs: unavailable, UnavailableDomains: unavailable,
		UnsupportedSyntaxFacts: 16_384, GapFacts: semantic.ObservationUnsupported,
		NoSilentEmpty: unavailable > 0 && semantic.ObservationUnsupported > 0,
	}
	for index := range run.observation.Checks {
		run.observation.Checks[index].Passed = true
	}
	run.observation.Decision = DecisionObservation{
		Selected: "continue", Reason: "all_exact_mechanics_passed", Substantiated: true,
	}
	return nil
}

func (run *execution) teardown() error {
	started := time.Now()
	if err := run.stopServers(); err != nil {
		return err
	}
	logical, allocated, err := measureDataBytes(run.workspace)
	if err != nil {
		return err
	}
	info, err := os.Lstat(run.workspace)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		run.workspace == run.moduleRoot || isWithin(run.moduleRoot, run.workspace) {
		return errors.New("T40.13 teardown custody changed")
	}
	if err := os.RemoveAll(run.workspace); err != nil {
		return err
	}
	if _, err := os.Lstat(run.workspace); !os.IsNotExist(err) {
		return errors.New("T40.13 teardown left custody behind")
	}
	run.observation.Teardown = TeardownObservation{Completed: true}
	run.observation.Phases[11] = succeededPhase("teardown", PhaseMetrics{
		WallMS: time.Since(started).Milliseconds(), DataLogicalBytes: logical, DataAllocatedBytes: allocated,
	})
	return run.enforceSafety()
}

func (run *execution) enforceSafety() error {
	var totalWall int64
	for _, phase := range run.observation.Phases {
		if phase.Outcome == "not_run" {
			continue
		}
		totalWall += phase.Metrics.WallMS
		if phase.Metrics.PeakRSSBytes > run.plan.Safety.MaximumPeakRSSBytes ||
			phase.Metrics.DataAllocatedBytes > run.plan.Safety.MaximumDataAllocatedBytes {
			return errReviewCeiling
		}
	}
	if totalWall > run.plan.Safety.MaximumTotalWallMS {
		return errReviewCeiling
	}
	return nil
}

func (run *execution) stopServers() error {
	var result error
	if run.structural != nil {
		result = errors.Join(result, run.structural.stop(30*time.Second))
		run.structural = nil
	}
	if run.semantic != nil {
		result = errors.Join(result, run.semantic.stop(30*time.Second))
		run.semantic = nil
	}
	return result
}

func waitSnapshot(ctx context.Context, profile PreparedProfile, revision string, limit time.Duration) (privateProfileSnapshot, error) {
	inspector, err := newProfileInspector(profile)
	if err != nil {
		return privateProfileSnapshot{}, err
	}
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if snapshot, inspectErr := inspector.inspect(phase, profile, revision); inspectErr == nil {
			return snapshot, nil
		}
		select {
		case <-phase.Done():
			return privateProfileSnapshot{}, errors.New("T40.13 convergence deadline expired")
		case <-ticker.C:
		}
	}
}

func waitIndexedCommit(ctx context.Context, profile PreparedProfile, commit string, limit time.Duration) error {
	inspector, err := newProfileInspector(profile)
	if err != nil {
		return err
	}
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if repository, currentErr := inspector.currentRepository(phase, profile); currentErr == nil && repository.IndexedCommitHash == commit {
			return nil
		}
		select {
		case <-phase.Done():
			return errors.New("T40.13 indexed commit deadline expired")
		case <-ticker.C:
		}
	}
}

func waitForDerivedPartial(ctx context.Context, dataDir string, limit time.Duration) error {
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		for _, root := range []string{"observations", "extraction-publications", "relationships"} {
			found := false
			observed := 0
			_ = filepath.WalkDir(filepath.Join(dataDir, root), func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return filepath.SkipDir
				}
				observed++
				if observed > 500_000 {
					return errors.New("inventory limit")
				}
				if entry.Name() == "publishing.json" || strings.HasPrefix(entry.Name(), ".stage-") {
					found = true
					return errors.New("found")
				}
				return nil
			})
			if found {
				return nil
			}
		}
		select {
		case <-phase.Done():
			return errors.New("T40.13 derived interruption point was not observed")
		case <-ticker.C:
		}
	}
}

func backupAndReset(ctx context.Context, toolchain privateToolchain, profile PreparedProfile, label string) (retErr error) {
	base := filepath.Dir(profile.Config)
	backup := filepath.Join(base, "backup-"+label)
	prior := profile.DataDir + ".prior-" + label
	logPath := filepath.Join(base, "recovery-"+label+".log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, logFile.Close()) }()
	run := func(args ...string) error {
		command := exec.CommandContext(ctx, toolchain.Phebs, args...)
		command.Stdout, command.Stderr = logFile, logFile
		command.Env = scrubExecutionEnvironment()
		if err := command.Run(); err != nil {
			return errors.New("T40.13 recovery command failed")
		}
		return nil
	}
	if err := run("backup", "-config", profile.Config, "-output", backup); err != nil {
		return err
	}
	if _, err := os.Lstat(prior); err == nil || !os.IsNotExist(err) {
		return errors.New("T40.13 recovery prior path already exists")
	}
	if err := os.Rename(profile.DataDir, prior); err != nil {
		return err
	}
	if err := run("restore", "-config", profile.Config, "-backup", backup); err != nil {
		return err
	}
	if !isWithin(prior, base) || filepath.Clean(prior) == filepath.Clean(base) {
		return errors.New("T40.13 recovery prior path escaped custody")
	}
	if err := os.RemoveAll(prior); err != nil {
		return err
	}
	return nil
}

func waitLifecycle(ctx context.Context, profile PreparedProfile, requireCycle bool, limit time.Duration) (lifecycle.Status, error) {
	inspector, err := newProfileInspector(profile)
	if err != nil {
		return lifecycle.Status{}, err
	}
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var status lifecycle.Status
		if readErr := inspector.get(phase, profile, "/api/lifecycle-status", &status); readErr == nil && lifecycle.ValidateStatus(status) == nil {
			complete := true
			for _, owner := range status.Owners {
				if owner.State == "error" || requireCycle && owner.State == "not_run" {
					complete = false
					break
				}
			}
			if complete {
				return status, nil
			}
		}
		select {
		case <-phase.Done():
			return lifecycle.Status{}, errors.New("T40.13 lifecycle cycle deadline expired")
		case <-ticker.C:
		}
	}
}

func queryProfile(
	ctx context.Context,
	profile PreparedProfile,
	serviceKey string,
	requireCitation bool,
) (int, bool, error) {
	inspector, err := newProfileInspector(profile)
	if err != nil {
		return 0, false, err
	}
	unauthorized, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+profile.Address+"/api/repos", nil)
	if err != nil {
		return 0, false, err
	}
	response, err := inspector.client.Do(unauthorized)
	if err != nil {
		return 0, false, err
	}
	_, _ = io.CopyN(io.Discard, response.Body, 4096)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		return 0, false, errors.New("T40.13 unauthorized query was not denied")
	}
	var searchResult search.Result
	if err := inspector.get(ctx, profile, "/api/search?q=T401&max_matches=1", &searchResult); err != nil {
		return 0, false, err
	}
	if len(searchResult.Files) == 0 || len(searchResult.Files[0].Chunks) == 0 ||
		len(searchResult.Files[0].Chunks[0].Ranges) == 0 {
		return 0, false, errors.New("T40.13 authorized search returned no exact match")
	}
	var services apiresponse.ServiceInventory
	servicePath := "/api/services?repository=" + url.QueryEscape(profile.RepositoryName) + "&page_size=100"
	if err := inspector.get(ctx, profile, servicePath, &services); err != nil {
		return 0, false, err
	}
	if services.Repository.CatalogServiceCount < 1 || services.Pagination.Returned != len(services.Services) {
		return 0, false, errors.New("T40.13 authorized service inventory is invalid")
	}
	var relationships apiresponse.RelationshipPage
	relationshipPath := "/api/service-relationships?repository=" + url.QueryEscape(profile.RepositoryName) +
		"&service_key=" + url.QueryEscape(serviceKey) + "&page_size=100"
	if err := inspector.get(ctx, profile, relationshipPath, &relationships); err != nil {
		return 0, false, err
	}
	if len(relationships.Roots) != 1 || relationships.Roots[0].Generation == "" || relationships.Roots[0].RootDigest == "" {
		return 0, false, errors.New("T40.13 relationship response lacks exact root authority")
	}
	if requireCitation && len(relationships.Rows) == 0 {
		return 0, false, errors.New("T40.13 relationship query returned no citable row")
	}
	if len(relationships.Rows) > 0 {
		if relationships.Rows[0].Citation == "" {
			return 0, false, errors.New("T40.13 relationship row lacks a citation token")
		}
		var citation apiresponse.RelationshipCitation
		path := "/api/service-relationship-citation?citation=" + url.QueryEscape(relationships.Rows[0].Citation)
		if err := inspector.get(ctx, profile, path, &citation); err != nil {
			return 0, false, err
		}
		if citation.Repository != profile.RepositoryName || citation.Generation != relationships.Roots[0].Generation ||
			citation.RootDigest != relationships.Roots[0].RootDigest || citation.AuthorityDigest == "" ||
			citation.Content == "" {
			return 0, false, errors.New("T40.13 citation differs from rendered root authority")
		}
	}
	return len(searchResult.Files) + len(relationships.Rows), true, nil
}

func emptyObservation(environment EnvironmentObservation) Observation {
	phases := make([]PhaseObservation, len(phaseOrder))
	for index, name := range phaseOrder {
		phases[index] = PhaseObservation{Name: name, Outcome: "not_run"}
	}
	checks := make([]CheckObservation, len(checkNames))
	for index, name := range checkNames {
		checks[index] = CheckObservation{Name: name}
	}
	return Observation{
		Schema: ObservationSchema, MeasuredOn: time.Now().UTC().Format("2006-01-02"), Outcome: "completed",
		Environment: environment,
		Profiles: []ProfileObservation{
			{Name: "structural-2m-v1", RegularFiles: 2_000_002, PhysicalOwners: 2_000_002, DeclaredSourceBytes: 9_216_000_076},
			{Name: "semantic-262144-v1", RegularFiles: 294_914, PhysicalOwners: 294_914, DeclaredSourceBytes: 146_800_716},
		},
		Phases: phases, Checks: checks, Decision: DecisionObservation{
			Selected: "continue", Reason: "execution_in_progress", Substantiated: true,
		},
	}
}

func succeededPhase(name string, metrics PhaseMetrics) PhaseObservation {
	return PhaseObservation{
		Name: name, Outcome: "succeeded", Metrics: metrics,
		AuthorityChanged: metrics.PublicationTransactions > 0, OracleExact: true,
	}
}

func mergeMetrics(values ...PhaseMetrics) PhaseMetrics {
	var result PhaseMetrics
	for _, value := range values {
		result.WallMS += value.WallMS
		result.PeakRSSBytes = max(result.PeakRSSBytes, value.PeakRSSBytes)
		result.DataLogicalBytes = max(result.DataLogicalBytes, value.DataLogicalBytes)
		result.DataAllocatedBytes = max(result.DataAllocatedBytes, value.DataAllocatedBytes)
		result.GitChildren += value.GitChildren
		result.IndexChildren += value.IndexChildren
		result.OtherChildren += value.OtherChildren
		result.ControlReads += value.ControlReads
		result.MemberReads += value.MemberReads
		result.PublicationWrites += value.PublicationWrites
		result.PublicationTransactions += value.PublicationTransactions
		result.OrchestrationTransactions += value.OrchestrationTransactions
		result.Retries += value.Retries
		result.ReusedControls += value.ReusedControls
		result.ReusedMembers += value.ReusedMembers
	}
	return result
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func WriteObservation(path string, value Observation) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return errors.New("T40.13 observation output path is invalid")
	}
	raw, err := MarshalObservation(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
