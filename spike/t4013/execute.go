package t4013

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	apiresponse "github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

const ExecuteConfirm = "execute-neutral-t4013-and-destroy-custody"

const (
	teardownCheckpointSchema       = "t4013-teardown-checkpoint-v1"
	teardownCheckpointSchemaV2     = "t4013-teardown-checkpoint-v2"
	maximumTeardownCheckpointBytes = MaxObservationBytes + 4<<10
	maximumPreparedConfigBytes     = 64 << 10
	maximumCredentialBytes         = 257
	teardownPersistenceReserve     = 30 * time.Second
	teardownRetirementReserve      = 30 * time.Second

	// The frozen ceremony enables nine extractor domains: four protobuf,
	// three Thrift, and two Kafka domains. The T40.1 extractor aggregate
	// covers only the four IDL template families. The production Kafka
	// producer domain additionally emits one fact for each of the two
	// 65,536-input Kafka Go families (literal and dynamic-topic); the Kafka
	// consumer and the typed-input domains emit no facts for this corpus.
	frozenExtractorDomainCount         = 9
	frozenSemanticIDLExtractionFacts   = int64(49_152)
	frozenSemanticKafkaExtractionFacts = int64(2 * 65_536)
	frozenSemanticExtractionFacts      = frozenSemanticIDLExtractionFacts + frozenSemanticKafkaExtractionFacts
	frozenSemanticExtractionRows       = 2 * frozenSemanticExtractionFacts
)

var ErrGateStopped = errors.New("T40.13 exact mechanics gate stopped")

var (
	errReviewCeiling                = errors.New("T40.13 frozen review ceiling crossed")
	errTotalWallDeadline            = fmt.Errorf("T40.13 frozen total-wall deadline crossed: %w", errReviewCeiling)
	errExactOracle                  = errors.New("T40.13 exact oracle refused")
	errDirectRecovery               = errors.New("T40.13 direct recovery refused")
	errProductionPressure           = errors.New("T40.13 production pressure gate refused")
	errConvergenceDeadline          = errors.New("T40.13 frozen convergence deadline expired")
	errConvergenceServerExit        = errors.New("T40.13 server exited during convergence")
	errConvergenceTimeline          = errors.New("T40.13 convergence transition limit exceeded")
	errRepositoryIndexTerminal      = errors.New("T40.13 repository index job terminated before publication")
	errObservationBoundRefusal      = errors.New("T40.13 observation planning refused a frozen production bound")
	errObservationTerminal          = errors.New("T40.13 observation planning terminated before publication")
	errExtractionBoundRefusal       = errors.New("T40.13 extraction planning refused a frozen production bound")
	errExtractionJobTerminal        = errors.New("T40.13 extraction job terminated before publication")
	errExtractionScheduleTerminal   = errors.New("T40.13 extraction schedule settled with failed partitions")
	errCallerGenerationBoundRefusal = errors.New("T40.13 caller generation refused a frozen production bound")
	errCallerGenerationTerminal     = errors.New("T40.13 caller generation terminated before publication")
	errCallerPublicationMissing     = fmt.Errorf("T40.13 caller work completed without publication: %w", errCallerGenerationTerminal)
	errRelationshipBoundRefusal     = errors.New("T40.13 relationship generation refused a frozen production bound")
	errRelationshipTerminal         = errors.New("T40.13 relationship generation terminated before publication")
	errInterruptionTriggerDeadline  = errors.New("T40.13 B-bound interruption trigger deadline expired")
	errInterruptionProgressTerminal = errors.New("T40.13 B pipeline reached a terminal state before interruption trigger")
	errLifecycleCycleDeadline       = errors.New("T40.13 lifecycle cycle deadline expired")
	errPressureRecoveryDeadline     = errors.New("T40.13 pressure recovery deadline expired")
	errAuthorizedQuery              = errors.New("T40.13 authorized query failed")
	errObservationPersistence       = errors.New("T40.13 observation persistence failed")
	errTeardownRecovery             = errors.New("T40.13 execution interrupted during teardown")
)

func exactOracle(message string) error { return fmt.Errorf("%w: %s", errExactOracle, message) }

func directRecovery(cause error) error {
	return errors.Join(errDirectRecovery, cause)
}

func directRecoveryIfError(cause error) error {
	if cause == nil {
		return nil
	}
	return directRecovery(cause)
}

type ExecuteRequest struct {
	ModuleRoot  string
	PlanPath    string
	Prepared    string
	Observation string
	Confirm     string
}

type execution struct {
	ctx                         context.Context
	moduleRoot                  string
	workspace                   string
	preparedPath                string
	preparedDigest              string
	plan                        Plan
	planBytes                   []byte
	prepared                    Prepared
	toolchain                   privateToolchain
	observation                 Observation
	structural                  *privateServer
	semantic                    *privateServer
	structA                     privateProfileSnapshot
	structB                     privateProfileSnapshot
	structAR                    privateProfileSnapshot
	semanticA                   privateProfileSnapshot
	phase                       int
	phaseStarted                time.Time
	activeMeters                map[*phaseMeter]struct{}
	partialMetrics              PhaseMetrics
	metersTracked               int
	metersExpected              int
	measurementErr              error
	custodyDestroy              func(string, string) error
	terminalDrain               func() error
	observationPath             string
	checkpointPersist           func(string, teardownCheckpoint) error
	observationStage            func(string, Observation) error
	observationPublish          func(string, Observation) error
	checkpointPersisted         bool
	observationStaged           bool
	observationPersisted        bool
	runRootLock                 *runRootLock
	executionStarted            time.Time
	executionCancel             context.CancelFunc
	hostToolchainVerify         func() error
	hostTools                   hostToolchainBinding
	hostTerminalVerified        bool
	liveServers                 []*privateServer
	serverShutdownErr           error
	portReservations            map[string]net.Listener
	inspectionWork              sync.WaitGroup
	supervision                 *custodySupervision
	checkpointDigest            string
	admissionMetrics            PhaseMetrics
	admissionAccountingComplete bool
}

func Execute(ctx context.Context, request ExecuteRequest) (observation Observation, retErr error) {
	executionStarted := time.Now()
	run, err := newExecution(ctx, request, executionStarted)
	if err != nil {
		return Observation{}, err
	}
	if run.runRootLock != nil {
		defer func() {
			retErr = errors.Join(retErr, run.runRootLock.Close())
		}()
	}
	if run.supervision != nil {
		defer func() {
			retErr = errors.Join(retErr, run.supervision.Close())
		}()
	}
	executionContext := run.ctx
	cancel := run.executionCancel
	if cancel == nil {
		executionContext, cancel = context.WithTimeoutCause(
			ctx, time.Duration(run.plan.Safety.MaximumTotalWallMS)*time.Millisecond, errTotalWallDeadline,
		)
	}
	defer cancel()
	run.ctx = executionContext
	if err := run.execute(); err != nil {
		// Once teardown has a durable checkpoint, only its resume protocol may
		// publish or revise the source-free terminal observation.
		if run.checkpointPersisted || run.observationStaged || errors.Is(err, errObservationPersistence) {
			if planSchemaVersion(run.plan.Schema) >= 25 && custodyRetentionCause(run.ctx, err) != nil {
				return stoppedExecutionResult(Observation{}, err, nil)
			}
			return stoppedExecutionResult(run.observation, err, nil)
		}
		observation, cleanupErr := run.stopAfterFailure(err)
		return stoppedExecutionResult(observation, err, cleanupErr)
	}
	return run.observation, nil
}

func stoppedExecutionResult(observation Observation, cause, cleanupErr error) (Observation, error) {
	return observation, errors.Join(ErrGateStopped, cause, cleanupErr)
}

func newExecution(
	ctx context.Context, request ExecuteRequest, executionStarted time.Time,
) (result *execution, retErr error) {
	if ctx == nil || request.Confirm != ExecuteConfirm || !filepath.IsAbs(request.ModuleRoot) ||
		!filepath.IsAbs(request.PlanPath) || !filepath.IsAbs(request.Prepared) || !filepath.IsAbs(request.Observation) {
		return nil, errors.New("T40.13 execution request is invalid")
	}
	moduleRoot, err := filepath.EvalSymlinks(request.ModuleRoot)
	if err != nil {
		return nil, err
	}
	planIdentity, plan, err := readPlanIdentity(request.PlanPath)
	if err != nil {
		return nil, err
	}
	planBytes := planIdentity.raw
	version := planSchemaVersion(plan.Schema)
	var admissionSampler *rssSampler
	admissionStarted := time.Now()
	if version >= 25 {
		admissionSampler = newRSSSampler(os.Getpid(), true)
		admissionSampler.captureRootIdentity()
		admissionSampler.sample()
		go admissionSampler.run()
		defer func() {
			admissionCloseErr := admissionSampler.close()
			admissionMetrics := PhaseMetrics{WallMS: time.Since(admissionStarted).Milliseconds()}
			processMetrics, processMetricsErr := admissionSampler.phaseMetrics()
			admissionMetrics, admissionMetricsErr := mergeMetrics(admissionMetrics, processMetrics)
			admissionMetricsErr = errors.Join(processMetricsErr, admissionMetricsErr)
			admissionErr := errors.Join(admissionCloseErr, admissionMetricsErr)
			if result != nil {
				result.admissionMetrics = admissionMetrics
				result.admissionAccountingComplete = admissionErr == nil
				result.measurementErr = errors.Join(result.measurementErr, admissionErr)
			}
		}()
	}
	ctx, executionCancel := executionAdmissionContext(ctx, plan, executionStarted)
	defer func() {
		if retErr != nil && executionCancel != nil {
			executionCancel()
		}
	}()
	if version >= 2 && version < 25 {
		if err := verifyHostToolchainForPlan(ctx, plan); err != nil {
			return nil, fmt.Errorf("verify frozen host toolchain before execution: %w", err)
		}
	}
	preparedPath := request.Prepared
	preparedDigest := ""
	var preparedIdentity exactFileIdentity
	var prepared Prepared
	if version >= 25 {
		preparedIdentity, prepared, err = readPreparedIdentity(request.Prepared, PlanDigest(planBytes))
		if err != nil {
			return nil, err
		}
		preparedPath = preparedIdentity.path
		preparedDigest = digest(preparedIdentity.raw)
	} else {
		preparedBytes, readErr := os.ReadFile(preparedPath)
		if readErr != nil {
			return nil, readErr
		}
		prepared, err = DecodePrepared(preparedBytes, PlanDigest(planBytes))
		if err != nil {
			return nil, err
		}
	}
	if version >= 25 && prepared.Schema != PreparedSchemaV2 ||
		version < 25 && prepared.Schema != PreparedSchema {
		return nil, errors.New("T40.13 prepared custody schema differs from the plan")
	}
	workspace := filepath.Dir(filepath.Dir(prepared.Profiles[0].Config))
	boundaryWorkspace := workspace
	observationPath := request.Observation
	var admissionLock *runRootLock
	if planSchemaVersion(plan.Schema) >= 25 {
		locatedWorkspace, _, locateErr := custodyControlDirectory(workspace)
		if locateErr != nil {
			return nil, errors.New("T40.13 execution custody is invalid")
		}
		var lockErr error
		admissionLock, lockErr = lockRunRoot(filepath.Dir(locatedWorkspace))
		if lockErr != nil {
			return nil, lockErr
		}
		defer func() {
			if retErr != nil {
				retErr = errors.Join(retErr, admissionLock.Close())
			}
		}()
		if err := errors.Join(planIdentity.revalidate(), preparedIdentity.revalidate()); err != nil {
			return nil, err
		}
		boundaryWorkspace, err = filepath.EvalSymlinks(workspace)
		if err != nil || boundaryWorkspace != locatedWorkspace || boundaryWorkspace != filepath.Clean(workspace) {
			return nil, errors.New("T40.13 execution custody is invalid")
		}
		workspace = boundaryWorkspace
		observationPath, err = canonicalNewOutputPath(request.Observation)
		if err != nil {
			return nil, fmt.Errorf("T40.13 observation output is invalid: %w", err)
		}
	}
	if filepath.Dir(filepath.Dir(prepared.Profiles[1].Config)) != workspace ||
		boundaryWorkspace == moduleRoot || isWithin(boundaryWorkspace, moduleRoot) || isWithin(moduleRoot, boundaryWorkspace) ||
		observationPath == boundaryWorkspace || isWithin(observationPath, boundaryWorkspace) {
		return nil, errors.New("T40.13 execution custody boundary is invalid")
	}
	if err := validatePreparedFiles(prepared, workspace); err != nil {
		return nil, err
	}
	info, err := os.Lstat(workspace)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("T40.13 execution custody is not a real directory")
	}
	outputPaths := []string{observationPath}
	if planSchemaVersion(plan.Schema) >= 25 {
		outputPaths = append(outputPaths, observationPath+".tmp",
			observationPath+".teardown", observationPath+".teardown.tmp")
	}
	for _, path := range outputPaths {
		if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
			return nil, errors.New("T40.13 observation output or durable stage must not exist")
		}
	}
	var supervision *custodySupervision
	restorePreparedCustody := false
	if planSchemaVersion(plan.Schema) >= 25 {
		if !hexIdentity(prepared.SupervisionToken, 64) {
			return nil, errors.New("T40.13 prepared custody lacks durable supervision")
		}
		supervision, err = beginExecuteCustody(
			boundaryWorkspace, PlanDigest(planBytes), prepared.SupervisionToken,
		)
		if err != nil {
			return nil, err
		}
		restorePreparedCustody = true
		defer func() {
			if retErr == nil || supervision == nil {
				return
			}
			if restorePreparedCustody {
				retErr = errors.Join(retErr, supervision.AbortExecuteAdmission())
			}
			retErr = errors.Join(retErr, supervision.Close())
		}()
	}
	var hostTools hostToolchainBinding
	var controls executionControls
	if version >= 25 {
		var bindErr error
		hostTools, bindErr = bindHostToolchainForPlan(ctx, plan)
		if bindErr != nil {
			return nil, fmt.Errorf("verify frozen host toolchain before execution: %w", bindErr)
		}
		controls, bindErr = openExecutionControls(
			workspace, prepared.ExecutionControlsSHA256, hostTools, true,
		)
		if bindErr != nil {
			return nil, bindErr
		}
	}
	if err := VerifyInputs(moduleRoot); err != nil {
		return nil, err
	}
	var checkoutErr error
	if version >= 25 {
		checkoutErr = verifyCleanCheckoutWithBoundGit(
			ctx, moduleRoot, plan.SourceCommit, hostTools.gitCore,
			executionEnvironmentForControls(controls, false),
		)
	} else {
		checkoutErr = verifyCleanCheckoutForPlan(ctx, moduleRoot, plan)
	}
	if checkoutErr != nil {
		return nil, checkoutErr
	}
	workspaceAllocated := int64(0)
	if planSchemaVersion(plan.Schema) >= 23 {
		_, workspaceAllocated, err = measureDataBytesForPlan(plan, workspace)
		if err != nil {
			return nil, err
		}
	}
	environment, err := hostPreflight(ctx, filepath.Dir(workspace), workspaceAllocated, plan)
	if err != nil {
		return nil, err
	}
	if err := preflightAtomicEvidenceProtocol(filepath.Dir(observationPath), plan); err != nil {
		return nil, fmt.Errorf("probe T40.13 observation filesystem protocol: %w", err)
	}
	var portReservations map[string]net.Listener
	if planSchemaVersion(plan.Schema) >= 25 {
		addresses := make([]string, 0, len(prepared.Profiles))
		for _, profile := range prepared.Profiles {
			addresses = append(addresses, profile.Address)
		}
		portReservations, err = reserveLoopbackAddresses(addresses...)
		if err != nil {
			return nil, err
		}
		defer func() {
			if portReservations != nil {
				_ = releaseLoopbackAddresses(portReservations)
			}
		}()
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	// Custody retained by an unsealable stop still carries the executed marker:
	// re-running against dirty, previously-executed state would seal evidence
	// with false cold/warm provenance. Create it atomically only after every
	// read-only input, checkout, output, and host preflight has passed; a
	// refused preflight therefore remains safely retryable.
	if err := writeExecutionMarkerForPlan(workspace, PlanDigest(planBytes), plan); err != nil {
		return nil, err
	}
	restorePreparedCustody = false
	observation := emptyObservationForPlan(environment, plan)
	result = &execution{
		ctx: ctx, moduleRoot: moduleRoot, workspace: workspace,
		plan: plan, planBytes: planBytes, prepared: prepared, observation: observation,
		observationPath: observationPath, runRootLock: admissionLock,
		portReservations: portReservations, executionStarted: executionStarted,
		executionCancel: executionCancel, supervision: supervision,
		preparedPath: preparedPath, preparedDigest: preparedDigest,
		hostTools: hostTools,
	}
	portReservations = nil
	return result, nil
}

func executionAdmissionContext(
	parent context.Context, plan Plan, started time.Time,
) (context.Context, context.CancelFunc) {
	if planSchemaVersion(plan.Schema) < 25 {
		return parent, nil
	}
	return context.WithDeadlineCause(
		parent,
		started.Add(time.Duration(plan.Safety.MaximumTotalWallMS)*time.Millisecond),
		errTotalWallDeadline,
	)
}

// executedMarkerName marks custody that an execution has already started on.
const executedMarkerName = ".t4013-executed"

func writeExecutionMarker(workspace, planDigest string) error {
	marker := filepath.Join(workspace, executedMarkerName)
	file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return errors.New("T40.13 execution custody was already executed; a reviewed purge and fresh preparation are required")
	}
	if err != nil {
		return fmt.Errorf("create T40.13 execution marker: %w", err)
	}
	_, writeErr := io.WriteString(file, planDigest+"\n")
	closeErr := file.Close()
	if writeErr == nil && closeErr == nil {
		return nil
	}
	return fmt.Errorf("write T40.13 execution marker: %w", errors.Join(writeErr, closeErr, os.Remove(marker)))
}

func writeExecutionMarkerForPlan(workspace, planDigest string, plan Plan) error {
	if planSchemaVersion(plan.Schema) < 25 {
		return writeExecutionMarker(workspace, planDigest)
	}
	return writeExecutionMarkerWith(workspace, planDigest,
		func(file *os.File) error { return file.Sync() }, syncDirectory)
}

func writeExecutionMarkerWith(
	workspace, planDigest string,
	syncFile func(*os.File) error,
	syncParent func(string) error,
) error {
	if syncFile == nil || syncParent == nil {
		return errors.New("T40.13 execution marker durability is unavailable")
	}
	marker := filepath.Join(workspace, executedMarkerName)
	file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return errors.New("T40.13 execution custody was already executed; a reviewed purge and fresh preparation are required")
	}
	if err != nil {
		return fmt.Errorf("create T40.13 execution marker: %w", err)
	}
	_, writeErr := io.WriteString(file, planDigest+"\n")
	syncErr := syncFile(file)
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return fmt.Errorf("persist T40.13 execution marker: %w", errors.Join(writeErr, syncErr, closeErr))
	}
	if err := syncParent(workspace); err != nil {
		return fmt.Errorf("persist T40.13 execution marker directory entry: %w", err)
	}
	return nil
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
		credential, err := readAtomicRegular(profile.Credential, maximumCredentialBytes)
		if err != nil {
			return err
		}
		configRaw, err := readAtomicRegular(profile.Config, maximumPreparedConfigBytes)
		if err != nil {
			return err
		}
		parsed, err := config.Parse(configRaw)
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
		return exactOracle("production retry ceiling differs from the frozen plan")
	}
	preflightStarted := time.Now()
	if planSchemaVersion(run.plan.Schema) >= 25 && !run.executionStarted.IsZero() {
		preflightStarted = run.executionStarted
		run.phaseStarted = run.executionStarted
	}
	toolchain, preflightMetrics, err := buildPrivateToolchain(
		run.ctx, run.moduleRoot, run.workspace, run.prepared.ExecutionControlsSHA256,
		run.plan, run.hostTools,
	)
	mergedMetrics, combinedErr := mergeMetricsPreservingError(
		err, run.admissionMetrics, preflightMetrics,
	)
	run.partialMetrics = mergedMetrics
	if combinedErr != nil {
		return combinedErr
	}
	if err := validateToolchain(toolchain); err != nil {
		return err
	}
	run.toolchain = toolchain
	run.observation.Toolchain, err = observeToolchain(toolchain)
	if err != nil {
		return err
	}
	if planSchemaVersion(run.plan.Schema) < 25 {
		preflightMetrics = PhaseMetrics{WallMS: time.Since(preflightStarted).Milliseconds(), OtherChildren: 4}
	} else {
		preflightMetrics = mergedMetrics
	}
	run.observation.Phases[0] = succeededPhase("preflight", preflightMetrics)
	if planSchemaVersion(run.plan.Schema) >= 25 && !run.admissionAccountingComplete {
		return errors.New("T40.13 admission phase accounting is incomplete")
	}
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
	if err := run.verifyFrozenHostToolchain(); err != nil {
		return err
	}
	if err := run.finalizeObservation(); err != nil {
		return err
	}
	if err := run.validateCompletedObservationBeforeTeardown(); err != nil {
		return fmt.Errorf("T40.13 completed observation is unsealable; custody retained: %w", err)
	}
	run.startPhase(11)
	if err := run.teardown(); err != nil {
		return err
	}
	if planSchemaVersion(run.plan.Schema) < 25 {
		if err := run.verifyFrozenHostToolchain(); err != nil {
			return err
		}
		return ValidateObservation(run.observation)
	}
	return nil
}

func (run *execution) stopAfterFailure(cause error) (Observation, error) {
	if planSchemaVersion(run.plan.Schema) < 25 {
		return run.stopAfterFailureLegacy(cause)
	}
	primaryCause := cause
	started := time.Now()
	stopErr := run.stopServers()
	cause = errors.Join(cause, stopErr)
	if retentionErr := custodyRetentionCause(run.ctx, cause); retentionErr != nil {
		return Observation{}, errors.Join(cause, retentionErr)
	}
	measurementErr := run.captureFailedPhase()
	ceilingErr := run.enforceSafety()
	run.observation.Outcome = "stopped"
	classification := classifyStoppedFailureForPlan(run.plan, cause, measurementErr, ceilingErr)
	run.setStoppedClassification(phaseOrder[run.phase], classification)
	if planSchemaVersion(run.plan.Schema) >= 27 &&
		classification.code == "failed_phase_measurement_unavailable" {
		run.observation.DataMeasurement = projectDataMeasurementDeadline(primaryCause)
	}
	run.observation.Teardown = TeardownObservation{}
	run.observation.Checks[len(run.observation.Checks)-1].Passed = false
	// Validate the stopped observation BEFORE destroying custody: an
	// unsealable observation must fail closed with custody retained for the
	// separately reviewed purge, never destroy hours of evidence first.
	projected, projectionErr := run.projectedTeardownObservation(started, 0, 0)
	if projectionErr != nil {
		return Observation{}, projectionErr
	}
	if err := run.validateReceiptBeforeTeardown(projected); err != nil {
		return Observation{}, fmt.Errorf(
			"T40.13 stopped observation is unsealable; custody retained for reviewed purge: %w", err)
	}
	if retentionErr := custodyRetentionCause(run.ctx, cause); retentionErr != nil {
		return Observation{}, errors.Join(cause, retentionErr)
	}
	if err := run.persistTeardownCheckpoint(started, 0, 0); err != nil {
		return Observation{}, err
	}
	if retentionErr := custodyRetentionCause(run.ctx, cause); retentionErr != nil {
		return Observation{}, errors.Join(cause, retentionErr)
	}
	if err := run.beginCustodyFinalization(); err != nil {
		return run.observation, fmt.Errorf(
			"T40.13 stopped custody descendants are not durably drained; teardown checkpoint retained: %w", err,
		)
	}
	destroy := destroyCustody
	if run.custodyDestroy != nil {
		destroy = run.custodyDestroy
	}
	destroyErr := destroy(run.workspace, run.moduleRoot)
	if destroyErr != nil {
		return run.observation, fmt.Errorf(
			"T40.13 stopped custody deletion is not durable; teardown checkpoint retained: %w", destroyErr)
	}
	if err := confirmCustodyDeletionDurable(run.workspace); err != nil {
		return run.observation, fmt.Errorf(
			"T40.13 stopped custody deletion is not durable; teardown checkpoint retained: %w", err)
	}
	if retentionErr := custodyRetentionCause(run.ctx, cause); retentionErr != nil {
		return Observation{}, errors.Join(cause, retentionErr)
	}
	if err := run.completeTeardown(started, 0, 0, nil); err != nil {
		return run.observation, err
	}
	return run.observation, nil
}

func (run *execution) stopAfterFailureLegacy(cause error) (Observation, error) {
	started := time.Now()
	stopErr := run.stopServers()
	if errors.Is(stopErr, errPrivateServerShutdownUnproven) || errors.Is(cause, errPrivateServerShutdownUnproven) {
		return Observation{}, errors.Join(cause, stopErr)
	}
	measurementErr := errors.Join(run.captureFailedPhase(), run.verifyFrozenHostToolchain())
	ceilingErr := run.enforceSafety()
	teardownAlreadyCompleted := run.observation.Teardown.Completed
	if run.phase != len(run.observation.Phases)-1 {
		run.observation.Phases[len(run.observation.Phases)-1] = succeededPhase("teardown", PhaseMetrics{
			WallMS: time.Since(started).Milliseconds(),
		})
	}
	run.observation.Outcome = "stopped"
	classification := classifyStoppedFailureForPlan(run.plan, cause, measurementErr, ceilingErr)
	run.observation.Failures = []FailureObservation{{
		Phase: phaseOrder[run.phase], Class: classification.class, Code: classification.code,
	}}
	run.observation.Decision = DecisionObservation{
		Selected: classification.decision, Reason: classification.reason,
		Substantiated: classification.substantiated,
	}
	run.observation.Teardown = TeardownObservation{Completed: true}
	run.observation.Checks[len(run.observation.Checks)-1].Passed = false
	if err := run.validateReceiptBeforeTeardown(run.observation); err != nil {
		return Observation{}, fmt.Errorf(
			"T40.13 stopped observation is unsealable; custody retained for reviewed purge: %w", err)
	}
	if _, err := os.Lstat(run.workspace); !errors.Is(err, os.ErrNotExist) || !teardownAlreadyCompleted {
		destroy := destroyCustody
		if run.custodyDestroy != nil {
			destroy = run.custodyDestroy
		}
		if err := destroy(run.workspace, run.moduleRoot); err != nil {
			return Observation{}, err
		}
	}
	return run.observation, stopErr
}

type teardownCheckpoint struct {
	Schema             string      `json:"schema"`
	PlanDigest         string      `json:"plan_digest"`
	ModuleRoot         string      `json:"module_root,omitempty"`
	Workspace          string      `json:"workspace"`
	PreparedPath       string      `json:"prepared_path,omitempty"`
	PreparedDigest     string      `json:"prepared_digest,omitempty"`
	SupervisionToken   string      `json:"supervision_token,omitempty"`
	StartedAt          string      `json:"started_at"`
	DeadlineAt         string      `json:"deadline_at,omitempty"`
	DataLogicalBytes   int64       `json:"data_logical_bytes"`
	DataAllocatedBytes int64       `json:"data_allocated_bytes"`
	Observation        Observation `json:"observation"`
}

func (run *execution) projectedTeardownObservation(
	started time.Time,
	logical, allocated int64,
) (Observation, error) {
	value := run.observation
	value.Phases = slices.Clone(run.observation.Phases)
	last := len(value.Phases) - 1
	metrics := PhaseMetrics{
		WallMS:             0,
		DataLogicalBytes:   logical,
		DataAllocatedBytes: allocated,
	}
	if value.Outcome == "stopped" && value.Phases[last].Outcome == "failed" {
		mergedMetrics, err := mergeMetrics(value.Phases[last].Metrics, metrics)
		if err != nil {
			return Observation{}, err
		}
		metrics = mergedMetrics
		value.Phases[last] = PhaseObservation{Name: "teardown", Outcome: "failed", Metrics: metrics}
	} else {
		value.Phases[last] = succeededPhase("teardown", metrics)
	}
	value.Teardown = TeardownObservation{Completed: true}
	wallMS, err := conservativeTeardownWallMS(started, time.Now())
	if err != nil {
		return Observation{}, err
	}
	value.Phases[last].Metrics.WallMS = wallMS
	return value, nil
}

func conservativeTeardownWallMS(started, finished time.Time) (int64, error) {
	elapsed := finished.Sub(started)
	if elapsed < 0 {
		elapsed = 0
	}
	var err error
	elapsedMS, err := checkedAddInt64(int64(elapsed), int64(teardownPersistenceReserve+teardownRetirementReserve))
	if err != nil {
		return 0, err
	}
	elapsedMS, err = checkedAddInt64(elapsedMS, int64(time.Millisecond-1))
	if err != nil {
		return 0, err
	}
	return max(int64(1), elapsedMS/int64(time.Millisecond)), nil
}

func (run *execution) persistTeardownCheckpoint(
	started time.Time,
	logical, allocated int64,
) error {
	wantSupervision := planSchemaVersion(run.plan.Schema) >= 25
	if wantSupervision != (run.supervision != nil) {
		return fmt.Errorf("%w: teardown checkpoint supervision differs from the plan", errObservationPersistence)
	}
	checkpoint := teardownCheckpoint{
		Schema: teardownCheckpointSchemaForPlan(run.plan), PlanDigest: PlanDigest(run.planBytes),
		Workspace: run.workspace, StartedAt: started.UTC().Format(time.RFC3339Nano),
		DataLogicalBytes: logical, DataAllocatedBytes: allocated,
		Observation: run.observation,
	}
	if run.supervision != nil {
		checkpoint.Schema = teardownCheckpointSchemaV2
		checkpoint.ModuleRoot = run.moduleRoot
		checkpoint.PreparedPath = run.preparedPath
		checkpoint.PreparedDigest = run.preparedDigest
		checkpoint.SupervisionToken = run.supervision.Token()
	}
	if run.ctx != nil {
		if deadline, ok := run.ctx.Deadline(); ok {
			checkpoint.DeadlineAt = deadline.UTC().Format(time.RFC3339Nano)
		}
	}
	checkpointRaw, err := marshalTeardownCheckpoint(checkpoint)
	if err != nil {
		return fmt.Errorf("%w: bind teardown checkpoint: %w", errObservationPersistence, err)
	}
	persist := writeTeardownCheckpoint
	if run.checkpointPersist != nil {
		persist = run.checkpointPersist
	}
	if err := persist(run.observationPath, checkpoint); err != nil {
		return fmt.Errorf("%w: teardown checkpoint: %w", errObservationPersistence, err)
	}
	run.checkpointDigest = digest(checkpointRaw)
	run.checkpointPersisted = true
	return nil
}

func (run *execution) updateSourceRevision(repository, commit string) error {
	if planSchemaVersion(run.plan.Schema) >= 25 {
		return updateSourceRevisionWithGit(
			run.ctx, repository, commit, run.hostTools.gitCore, run.toolchain.controls,
		)
	}
	return updateSourceRevision(run.ctx, repository, commit, false)
}

func (run *execution) completeTeardown(
	started time.Time,
	logical, allocated int64,
	cleanupErr error,
) error {
	var projectionErr error
	run.observation, projectionErr = run.projectedTeardownObservation(started, logical, allocated)
	if projectionErr != nil {
		return projectionErr
	}
	toolchainErr := run.verifyFrozenHostToolchain()
	last := len(run.observation.Phases) - 1
	wallMS, err := conservativeTeardownWallMS(started, time.Now())
	if err != nil {
		return err
	}
	run.observation.Phases[last].Metrics.WallMS = max(run.observation.Phases[last].Metrics.WallMS, wallMS)
	ceilingErr := errors.Join(
		run.teardownDeadlineError(teardownPersistenceReserve+teardownRetirementReserve),
		run.enforceSafety(),
	)
	postDeleteErr := errors.Join(cleanupErr, toolchainErr, ceilingErr, run.completedCancellationError())

	if run.observation.Outcome == "completed" && postDeleteErr != nil {
		run.stopCompletedTeardown(postDeleteErr, ceilingErr)
	} else if run.observation.Outcome == "stopped" {
		if cleanupErr != nil || toolchainErr != nil {
			return fmt.Errorf("T40.13 final teardown validation failed; teardown checkpoint retained: %w",
				errors.Join(cleanupErr, toolchainErr))
		}
		if ceilingErr != nil {
			classification := classifyStoppedFailureForPlan(run.plan, ceilingErr, nil, ceilingErr)
			failurePhase := run.observation.Failures[0].Phase
			run.setStoppedClassification(failurePhase, classification)
		}
	}
	publicationErr := run.persistFinalTeardown(started)
	var terminalErr error
	if publicationErr == nil && run.observationPersisted {
		terminalErr = run.commitTerminalTeardown()
	}
	return errors.Join(postDeleteErr, publicationErr, terminalErr)
}

func (run *execution) persistFinalTeardown(started time.Time) error {
	var observedErr error
	for attempt := 0; attempt < 2; attempt++ {
		if retentionErr := custodyRetentionCause(run.ctx, nil); retentionErr != nil {
			return fmt.Errorf("T40.13 execution canceled before terminal publication; teardown checkpoint retained: %w", retentionErr)
		}
		if err := run.validateReceiptBeforeTeardown(run.observation); err != nil {
			return fmt.Errorf("T40.13 final observation is unsealable; teardown checkpoint retained: %w", err)
		}
		if err := run.stageObservation(); err != nil {
			return err
		}
		if retentionErr := custodyRetentionCause(run.ctx, nil); retentionErr != nil {
			if err := removeProvisionalObservation(run.observationPath); err != nil {
				return errors.Join(retentionErr,
					fmt.Errorf("%w: retire canceled provisional observation: %w", errObservationPersistence, err))
			}
			run.observationStaged = false
			return fmt.Errorf("T40.13 execution canceled before terminal publication; teardown checkpoint retained: %w", retentionErr)
		}
		if err := run.publishObservation(); err != nil {
			return err
		}
		if retentionErr := custodyRetentionCause(run.ctx, nil); retentionErr != nil {
			if err := removeProvisionalObservation(run.observationPath); err != nil {
				return errors.Join(retentionErr,
					fmt.Errorf("%w: retire canceled terminal observation: %w", errObservationPersistence, err))
			}
			run.observationStaged = false
			run.observationPersisted = false
			return fmt.Errorf("T40.13 execution canceled across terminal publication; teardown checkpoint retained: %w", retentionErr)
		}

		postErr, ceilingErr := run.postPublicationValidation(started)
		if postErr == nil {
			if retentionErr := custodyRetentionCause(run.ctx, nil); retentionErr != nil {
				if err := removeProvisionalObservation(run.observationPath); err != nil {
					return errors.Join(retentionErr,
						fmt.Errorf("%w: retire canceled terminal observation: %w", errObservationPersistence, err))
				}
				run.observationStaged = false
				run.observationPersisted = false
				return fmt.Errorf("T40.13 execution canceled before terminal retirement; teardown checkpoint retained: %w", retentionErr)
			}
			return observedErr
		}
		observedErr = errors.Join(observedErr, postErr)
		if err := removeProvisionalObservation(run.observationPath); err != nil {
			return errors.Join(observedErr,
				fmt.Errorf("%w: retire provisional observation: %w", errObservationPersistence, err))
		}
		run.observationStaged = false
		run.observationPersisted = false
		if run.observation.Outcome == "completed" {
			run.stopCompletedTeardown(postErr, ceilingErr)
		} else if ceilingErr != nil {
			classification := classifyStoppedFailureForPlan(run.plan, ceilingErr, nil, ceilingErr)
			failurePhase := run.observation.Failures[0].Phase
			run.setStoppedClassification(failurePhase, classification)
		}
		last := len(run.observation.Phases) - 1
		wallMS, wallErr := conservativeTeardownWallMS(started, time.Now())
		if wallErr != nil {
			return wallErr
		}
		run.observation.Phases[last].Metrics.WallMS = max(run.observation.Phases[last].Metrics.WallMS, wallMS)
	}
	return errors.Join(observedErr,
		fmt.Errorf("%w: final observation persistence exceeded its conservative reserve", errObservationPersistence))
}

func (run *execution) commitTerminalTeardown() error {
	if run == nil || !run.observationPersisted || !run.checkpointPersisted {
		return fmt.Errorf("%w: terminal teardown authority is incomplete", errObservationPersistence)
	}
	if run.supervision == nil {
		if err := removeTeardownCheckpoint(run.observationPath); err != nil {
			return fmt.Errorf("%w: retire teardown checkpoint: %w", errObservationPersistence, err)
		}
		run.checkpointPersisted = false
		return nil
	}
	present, err := validatePreparedPublicationDigest(run.preparedPath, run.preparedDigest)
	if err != nil {
		return fmt.Errorf("revalidate T40.13 private prepared publication: %w", err)
	}
	if !present {
		return fmt.Errorf("revalidate T40.13 private prepared publication: %w", os.ErrNotExist)
	}
	drainTerminal := run.supervision.DrainTerminal
	if run.terminalDrain != nil {
		drainTerminal = run.terminalDrain
	}
	if err := drainTerminal(); err != nil {
		return fmt.Errorf(
			"T40.13 finalizer descendants are not durably drained; teardown checkpoint retained: %w",
			err,
		)
	}
	if err := run.supervision.Retire(); err != nil {
		if retiredErr := confirmCustodySupervisionRetired(
			run.workspace, PlanDigest(run.planBytes), run.supervision.Token(),
			custodyOperationExecute, run.checkpointDigest,
		); retiredErr != nil {
			return fmt.Errorf("retire T40.13 terminal custody supervision: %w",
				errors.Join(err, retiredErr))
		}
	}
	if err := removePreparedPublication(run.preparedPath); err != nil {
		return fmt.Errorf("retire T40.13 private prepared publication: %w", err)
	}
	if err := removeTeardownCheckpoint(run.observationPath); err != nil {
		return fmt.Errorf("%w: retire teardown checkpoint: %w", errObservationPersistence, err)
	}
	run.checkpointPersisted = false
	return nil
}

func (run *execution) postPublicationValidation(started time.Time) (error, error) {
	last := len(run.observation.Phases) - 1
	covered := time.Duration(run.observation.Phases[last].Metrics.WallMS) * time.Millisecond
	toolchainErr := run.verifyFrozenHostToolchain()
	var coverageErr error
	if elapsed := time.Since(started); elapsed < 0 || elapsed+teardownRetirementReserve > covered {
		coverageErr = fmt.Errorf("T40.13 teardown persistence exceeded its recorded wall: %w", errReviewCeiling)
	}
	ceilingErr := errors.Join(run.teardownDeadlineError(teardownRetirementReserve), run.enforceSafety())
	if run.observation.Outcome == "stopped" && len(run.observation.Failures) == 1 &&
		run.observation.Failures[0].Code == "review_ceiling_crossed" {
		ceilingErr = nil
	}
	return errors.Join(coverageErr, toolchainErr, ceilingErr, run.completedCancellationError()), ceilingErr
}

func (run *execution) completedCancellationError() error {
	if run == nil || run.ctx == nil || run.observation.Outcome != "completed" {
		return nil
	}
	cause := context.Cause(run.ctx)
	if cause == nil || errors.Is(cause, errReviewCeiling) {
		return nil
	}
	return fmt.Errorf("T40.13 execution canceled before terminal publication: %w", cause)
}

func (run *execution) stopCompletedTeardown(cause, ceilingErr error) {
	last := len(run.observation.Phases) - 1
	metrics := run.observation.Phases[last].Metrics
	run.observation.Outcome = "stopped"
	run.observation.Phases[last] = PhaseObservation{
		Name: "teardown", Outcome: "failed", Metrics: metrics,
		AuthorityChanged: false, OracleExact: false,
	}
	classification := classifyStoppedFailureForPlan(run.plan, cause, nil, ceilingErr)
	run.setStoppedClassification("teardown", classification)
	run.observation.Checks[len(run.observation.Checks)-1].Passed = false
}

func (run *execution) teardownDeadlineError(reserve time.Duration) error {
	if run.ctx == nil {
		return nil
	}
	if errors.Is(context.Cause(run.ctx), errReviewCeiling) {
		return context.Cause(run.ctx)
	}
	if deadline, ok := run.ctx.Deadline(); ok && time.Now().Add(reserve).After(deadline) {
		return errTotalWallDeadline
	}
	return nil
}

func (run *execution) stageObservation() error {
	if run == nil || run.observationPath == "" {
		return fmt.Errorf("%w: output path is unavailable", errObservationPersistence)
	}
	stage := StageObservation
	if run.observationStage != nil {
		stage = run.observationStage
	}
	if err := stage(run.observationPath, run.observation); err != nil {
		return fmt.Errorf("%w: stage: %w", errObservationPersistence, err)
	}
	run.observationStaged = true
	return nil
}

func (run *execution) publishObservation() error {
	if run == nil || !run.observationStaged || run.observationPath == "" {
		return fmt.Errorf("%w: staged output is unavailable", errObservationPersistence)
	}
	publish := PublishObservation
	if run.observationPublish != nil {
		publish = run.observationPublish
	}
	if err := publish(run.observationPath, run.observation); err != nil {
		return fmt.Errorf("%w: publish: %w", errObservationPersistence, err)
	}
	run.observationPersisted = true
	return nil
}

func (run *execution) verifyFrozenHostToolchain() error {
	if run.hostToolchainVerify != nil {
		return run.hostToolchainVerify()
	}
	if planSchemaVersion(run.plan.Schema) < 2 {
		return nil
	}
	verificationContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if planSchemaVersion(run.plan.Schema) >= 25 && run.hostTerminalVerified {
		return run.hostTools.verifyExecutables(verificationContext)
	}
	err := verifyHostToolchainForPlan(verificationContext, run.plan)
	if err == nil && planSchemaVersion(run.plan.Schema) >= 25 {
		run.hostTerminalVerified = true
	}
	return err
}

type stoppedClassification struct {
	class         string
	code          string
	decision      string
	reason        string
	substantiated bool
}

func (run *execution) setStoppedClassification(phase string, classification stoppedClassification) {
	run.observation.Failures = []FailureObservation{{
		Phase: phase, Class: classification.class, Code: classification.code,
	}}
	run.observation.Decision = DecisionObservation{
		Selected: classification.decision, Reason: classification.reason,
		Substantiated: classification.substantiated,
	}
	run.observation.DataMeasurement = nil
}

func classifyStoppedFailure(cause, measurementErr, ceilingErr error) stoppedClassification {
	result := stoppedClassification{
		class: "execution", code: "operational_failure",
		decision: "unclassified", reason: "operational_failure",
	}
	switch {
	case errors.Is(cause, errTotalWallDeadline) || errors.Is(ceilingErr, errTotalWallDeadline):
		result = stoppedClassification{
			class: "environment", code: "review_ceiling_crossed",
			decision: "cohort_experiment", reason: "frozen_review_ceiling_crossed", substantiated: true,
		}
	case errors.Is(cause, errConvergenceServerExit):
		result = stoppedClassification{
			class: "execution", code: "server_exited_during_convergence",
			decision: "unclassified", reason: "server_exited_during_convergence",
		}
	case errors.Is(cause, errConvergenceTimeline):
		result = stoppedClassification{
			class: "oracle", code: "convergence_transition_limit_exceeded",
			decision: "unclassified", reason: "convergence_transition_limit_exceeded",
		}
	case errors.Is(cause, errRepositoryIndexTerminal):
		result = stoppedClassification{
			class: "execution", code: "repository_index_terminal",
			decision: "unclassified", reason: "repository_index_terminal",
		}
	case errors.Is(cause, errObservationBoundRefusal):
		result = stoppedClassification{
			class: "pipeline", code: "observation_production_bound_refused",
			decision: "reduce", reason: "observation_production_bound_refused", substantiated: true,
		}
	case errors.Is(cause, errObservationTerminal):
		result = stoppedClassification{
			class: "pipeline", code: "observation_terminal",
			decision: "unclassified", reason: "observation_terminal",
		}
	case errors.Is(cause, errExtractionBoundRefusal):
		result = stoppedClassification{
			class: "pipeline", code: "extraction_production_bound_refused",
			decision: "reduce", reason: "extraction_production_bound_refused", substantiated: true,
		}
	case errors.Is(cause, errExtractionJobTerminal):
		result = stoppedClassification{
			class: "pipeline", code: "extraction_job_terminal",
			decision: "unclassified", reason: "extraction_job_terminal",
		}
	case errors.Is(cause, errExtractionScheduleTerminal):
		result = stoppedClassification{
			class: "pipeline", code: "extraction_schedule_terminal",
			decision: "unclassified", reason: "extraction_schedule_terminal",
		}
	case errors.Is(cause, errCallerGenerationBoundRefusal):
		result = stoppedClassification{
			class: "pipeline", code: "caller_generation_production_bound_refused",
			decision: "reduce", reason: "caller_generation_production_bound_refused", substantiated: true,
		}
	case errors.Is(cause, errCallerGenerationTerminal):
		result = stoppedClassification{
			class: "pipeline", code: "caller_generation_terminal",
			decision: "unclassified", reason: "caller_generation_terminal",
		}
	case errors.Is(cause, errRelationshipBoundRefusal):
		result = stoppedClassification{
			class: "pipeline", code: "relationship_production_bound_refused",
			decision: "reduce", reason: "relationship_production_bound_refused", substantiated: true,
		}
	case errors.Is(cause, errRelationshipTerminal):
		result = stoppedClassification{
			class: "pipeline", code: "relationship_terminal",
			decision: "unclassified", reason: "relationship_terminal",
		}
	case errors.Is(cause, errInterruptionTriggerUnsatisfiable):
		result = stoppedClassification{
			class: "execution", code: "interruption_trigger_unsatisfiable",
			decision: "unclassified", reason: "interruption_trigger_unsatisfiable",
		}
	case errors.Is(cause, errInterruptionTriggerDeadline):
		result = stoppedClassification{
			class: "execution", code: "interruption_trigger_deadline",
			decision: "unclassified", reason: "interruption_trigger_deadline",
		}
	case errors.Is(cause, errInterruptionProgressTerminal):
		result = stoppedClassification{
			class: "pipeline", code: "interruption_progress_terminal",
			decision: "unclassified", reason: "interruption_progress_terminal",
		}
	case measurementErr != nil:
		result = stoppedClassification{
			class: "oracle", code: "failed_phase_measurement_unavailable",
			decision: "unclassified", reason: "failed_phase_measurement_unavailable",
		}
	case errors.Is(cause, errReviewCeiling) || errors.Is(ceilingErr, errReviewCeiling):
		result = stoppedClassification{
			class: "environment", code: "review_ceiling_crossed",
			decision: "cohort_experiment", reason: "frozen_review_ceiling_crossed", substantiated: true,
		}
	case errors.Is(cause, errDirectRecovery):
		result = stoppedClassification{
			class: "recovery", code: "direct_recovery_failed",
			decision: "p6_investigation", reason: "direct_recovery_failed", substantiated: true,
		}
	case errors.Is(cause, errProductionPressure):
		result = stoppedClassification{
			class: "lifecycle", code: "production_pressure_gate_refused",
			decision: "reduce", reason: "production_pressure_gate_refused", substantiated: true,
		}
	case errors.Is(cause, errExactOracle):
		result = stoppedClassification{
			class: "oracle", code: "exact_gate_failed",
			decision: "reduce", reason: "exact_mechanics_oracle_failed", substantiated: true,
		}
	case errors.Is(cause, errConvergenceDeadline):
		result = stoppedClassification{
			class: "execution", code: "convergence_deadline_expired",
			decision: "unclassified", reason: "convergence_deadline_expired",
		}
	}
	return result
}

func classifyStoppedFailureForPlan(
	plan Plan,
	cause, measurementErr, ceilingErr error,
) stoppedClassification {
	if planSchemaVersion(plan.Schema) >= 25 {
		measurementErr = errors.Join(measurementErr, retainedMeasurementFailure(cause))
	}
	if planSchemaVersion(plan.Schema) >= 27 {
		measurementErr = errors.Join(measurementErr, dataMeasurementDeadlineCause(cause))
	}
	if planSchemaVersion(plan.Schema) < 23 {
		return classifyStoppedFailure(cause, measurementErr, ceilingErr)
	}
	if measurementErr != nil || errors.Is(cause, errTotalWallDeadline) ||
		errors.Is(ceilingErr, errTotalWallDeadline) || errors.Is(ceilingErr, errReviewCeiling) {
		return classifyStoppedFailure(cause, measurementErr, ceilingErr)
	}
	// V23 preserves P6 attribution for every inner archive-recovery failure.
	// Historical classifiers inspected several inner sentinels first and could
	// otherwise let each newly added classification steal the outer decision.
	if errors.Is(cause, errDirectRecovery) {
		return stoppedClassification{
			class: "recovery", code: "direct_recovery_failed",
			decision: "p6_investigation", reason: "direct_recovery_failed", substantiated: true,
		}
	}
	if errors.Is(cause, errLifecycleCycleDeadline) {
		if errors.Is(cause, errProductionPressure) {
			return stoppedClassification{
				class: "lifecycle", code: "production_pressure_gate_refused",
				decision: "reduce", reason: "production_pressure_gate_refused", substantiated: true,
			}
		}
		return stoppedClassification{
			class: "lifecycle", code: "lifecycle_cycle_deadline_expired",
			decision: "cohort_experiment", reason: "frozen_collection_review_ceiling_crossed", substantiated: true,
		}
	}
	if errors.Is(cause, errPressureRecoveryDeadline) {
		return stoppedClassification{
			class: "environment", code: "pressure_recovery_deadline_expired",
			decision: "unclassified", reason: "pressure_recovery_deadline_expired",
		}
	}
	if errors.Is(cause, errAuthorizedQuery) {
		return stoppedClassification{
			class: "authorization", code: "authorized_query_failed",
			decision: "unclassified", reason: "authorized_query_failed",
		}
	}
	return classifyStoppedFailure(cause, measurementErr, ceilingErr)
}

func (run *execution) startPhase(index int) {
	run.phase = index
	run.phaseStarted = time.Now()
	run.activeMeters = make(map[*phaseMeter]struct{})
	run.partialMetrics = PhaseMetrics{}
	run.metersTracked = 0
	run.metersExpected = 0
	run.measurementErr = nil
}

func (run *execution) trackMeter(meter *phaseMeter) {
	if meter != nil {
		run.activeMeters[meter] = struct{}{}
		run.metersTracked++
		run.metersExpected++
	}
}

func (run *execution) trackExpectedMeter(meter *phaseMeter) {
	if meter != nil {
		run.activeMeters[meter] = struct{}{}
		run.metersTracked++
	}
}

func (run *execution) startServer(
	profile PreparedProfile,
	label string,
	before *privateProfileSnapshot,
) (*privateServer, *phaseMeter, error) {
	if planSchemaVersion(run.plan.Schema) >= 27 {
		return run.startServerV27(profile, label, before, nil)
	}
	return run.startServerLegacy(profile, label, before)
}

func (run *execution) startServerLegacy(
	profile PreparedProfile,
	label string,
	before *privateProfileSnapshot,
) (*privateServer, *phaseMeter, error) {
	run.metersExpected++
	if listener := run.portReservations[profile.Address]; listener != nil {
		if err := listener.Close(); err != nil {
			return nil, nil, fmt.Errorf("release T40.13 reserved server address: %w", err)
		}
		delete(run.portReservations, profile.Address)
	}
	server, err := launchPrivateServer(run.ctx, profile, run.toolchain, label)
	if err != nil {
		return nil, nil, err
	}
	run.liveServers = append(run.liveServers, server)
	meter, err := beginInitialPhaseMeter(server, run.workspace, before)
	if err != nil {
		return server, nil, err
	}
	run.trackExpectedMeter(meter)
	deadline := 90 * time.Second
	if planSchemaVersion(run.plan.Schema) >= 3 {
		deadline = time.Duration(run.plan.Safety.ServerHealthDeadlineMS) * time.Millisecond
	}
	startup, healthErr := awaitPrivateServerHealth(run.ctx, server, profile, label, deadline)
	if (planSchemaVersion(run.plan.Schema) >= 3) &&
		startup.Profile != "" {
		run.observation.ServerStartups = append(run.observation.ServerStartups, startup)
	}
	return server, meter, healthErr
}

func (run *execution) startServerV27(
	profile PreparedProfile,
	label string,
	before *privateProfileSnapshot,
	boundary *dataMeasurementBoundary,
) (*privateServer, *phaseMeter, error) {
	if planSchemaVersion(run.plan.Schema) < 27 {
		return nil, nil, errors.New("T40.13 coherent server start requires V27")
	}
	started := time.Now()
	var (
		allocated int64
		err       error
	)
	if boundary != nil {
		allocated, err = boundary.consume(run.workspace)
	} else {
		allocated, err = measureDataAllocatedBytesForContract(run.workspace, true)
	}
	if err != nil {
		run.measurementErr = errors.Join(run.measurementErr, err)
		return nil, nil, err
	}
	allocation, err := newAllocationSampler(run.workspace, allocated, true)
	if err != nil {
		run.measurementErr = errors.Join(run.measurementErr, err)
		return nil, nil, err
	}
	if listener := run.portReservations[profile.Address]; listener != nil {
		if err := listener.Close(); err != nil {
			_, allocationErr := allocation.close()
			run.measurementErr = errors.Join(run.measurementErr, allocationErr)
			return nil, nil, errors.Join(
				fmt.Errorf("release T40.13 reserved server address: %w", err), allocationErr,
			)
		}
		delete(run.portReservations, profile.Address)
	}
	server, err := launchPrivateServer(run.ctx, profile, run.toolchain, label)
	if err != nil {
		_, allocationErr := allocation.close()
		run.measurementErr = errors.Join(run.measurementErr, allocationErr)
		return nil, nil, errors.Join(err, allocationErr)
	}
	run.liveServers = append(run.liveServers, server)
	meter := &phaseMeter{
		started: started, server: server, dataDir: run.workspace, logOffset: 0,
		before: before, allocation: allocation, strict: true, captureRaw: true,
	}
	run.trackMeter(meter)
	deadline := time.Duration(run.plan.Safety.ServerHealthDeadlineMS) * time.Millisecond
	startup, healthErr := awaitPrivateServerHealth(run.ctx, server, profile, label, deadline)
	if startup.Profile != "" {
		run.observation.ServerStartups = append(run.observation.ServerStartups, startup)
	}
	return server, meter, healthErr
}

func (run *execution) finishMeter(meter *phaseMeter, after *privateProfileSnapshot) (PhaseMetrics, error) {
	metrics, err := meter.finish(after)
	if err != nil {
		run.measurementErr = errors.Join(run.measurementErr, err)
		if planSchemaVersion(run.plan.Schema) >= 25 {
			delete(run.activeMeters, meter)
		}
		return metrics, err
	}
	delete(run.activeMeters, meter)
	mergedMetrics, mergeErr := mergeMetrics(run.partialMetrics, metrics)
	if mergeErr != nil {
		run.measurementErr = errors.Join(run.measurementErr, mergeErr)
		return metrics, mergeErr
	}
	run.partialMetrics = mergedMetrics
	return metrics, nil
}

func (run *execution) captureFailedPhase() error {
	if run.phase < 0 || run.phase >= len(run.observation.Phases) {
		return errors.New("T40.13 failed phase is invalid")
	}
	metrics := run.partialMetrics
	if run.observation.Phases[run.phase].Outcome != "not_run" {
		metrics = run.observation.Phases[run.phase].Metrics
	}
	captureErr := run.measurementErr
	if run.metersTracked < run.metersExpected {
		captureErr = errors.Join(captureErr,
			errors.New("T40.13 failed phase lacks its complete meter inventory"))
	}
	for meter := range run.activeMeters {
		measured, err := meter.finish(nil)
		delete(run.activeMeters, meter)
		captureErr = errors.Join(captureErr, err)
		if err == nil {
			mergedMetrics, mergeErr := mergeMetrics(metrics, measured)
			if mergeErr != nil {
				captureErr = errors.Join(captureErr, mergeErr)
			} else {
				metrics = mergedMetrics
			}
		}
	}
	if metrics.DataAllocatedBytes == 0 {
		logical, allocated, err := measureDataBytesForPlan(run.plan, run.workspace)
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

func (run *execution) cold() error {
	started := time.Now()
	structural, semantic := run.prepared.Profiles[0], run.prepared.Profiles[1]
	server, meter, err := run.startServer(structural, "cold", nil)
	if err != nil {
		return err
	}
	run.structural = server
	run.structA, err = run.waitSnapshot(structural, "a", "cold", run.fullConvergenceDeadline(), server)
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
	server, meter, err = run.startServer(semantic, "cold", nil)
	if err != nil {
		return err
	}
	run.semantic = server
	run.semanticA, err = run.waitSnapshot(semantic, "a", "cold", run.fullConvergenceDeadline(), server)
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
	metrics, err := mergeMetrics(structMetrics, semanticMetrics)
	if err != nil {
		return err
	}
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
	server, meter, err := run.startServer(profile, "warm-noop", &run.structA)
	if err != nil {
		return err
	}
	run.structural = server
	after, err := run.waitSnapshot(profile, "a", "warm-noop", run.revalidationDeadline(), server)
	if err != nil {
		return err
	}
	metrics, err := run.finishMeter(meter, &after)
	if err != nil {
		return err
	}
	if !privateSnapshotEqual(run.structA, after) || metrics.GitChildren != 0 || metrics.IndexChildren != 0 ||
		metrics.PublicationWrites != 0 || metrics.PublicationTransactions != 0 || metrics.ReusedControls == 0 {
		return exactOracle("warm no-op moved authority or performed content work")
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
	if err := run.updateSourceRevision(profile.Repository, profile.Revisions["b"]); err != nil {
		return err
	}
	run.structB, err = run.waitSnapshot(profile, "b", "delta-b", run.fullConvergenceDeadline(), run.structural)
	if err != nil {
		return err
	}
	if changedSourceMembers(run.structA, run.structB) != 1 {
		return exactOracle("B changed other than one source partition")
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
	if err := run.updateSourceRevision(profile.Repository, profile.Revisions["a-return"]); err != nil {
		return err
	}
	run.structAR, err = run.waitSnapshot(profile, "a-return", "return-a", run.fullConvergenceDeadline(), run.structural)
	if err != nil {
		return err
	}
	if changedSourceMembers(run.structB, run.structAR) != 1 ||
		!equalStringSlices(run.structA.SourceMemberDigests, run.structAR.SourceMemberDigests) {
		return exactOracle("A return did not reproduce the frozen source partitions")
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
	run.setInterruptionSubstage("backup_start")
	if run.semantic != nil {
		if err := run.semantic.stop(30 * time.Second); err != nil {
			return err
		}
		run.semantic = nil
	}
	backupServer, backupMeter, err := run.startServer(profile, "interruption-backup", &run.semanticA)
	if err != nil {
		return err
	}
	run.semantic = backupServer
	run.setInterruptionSubstage("backup_revalidation")
	backupSnapshot, err := run.waitSnapshot(
		profile, "a", "interruption-backup", run.revalidationDeadline(), backupServer,
	)
	if err != nil {
		return err
	}
	if snapshotAuthority(backupSnapshot) != snapshotAuthority(run.semanticA) {
		return directRecovery(errors.New("interruption backup restart changed exact authority"))
	}
	run.setInterruptionSubstage("backup_create")
	backup, backupCommandMetrics, err := createLiveBackup(
		run.ctx, run.toolchain, profile, run.workspace, "interruption",
	)
	mergedMetrics, combinedErr := mergeMetricsPreservingError(
		directRecoveryIfError(err), run.partialMetrics, backupCommandMetrics,
	)
	run.partialMetrics = mergedMetrics
	if combinedErr != nil {
		return combinedErr
	}
	backupServerMetrics, err := run.finishMeter(backupMeter, &backupSnapshot)
	if err != nil {
		return err
	}
	backupMetrics, err := mergeConcurrentMetrics(backupServerMetrics, backupCommandMetrics)
	if err != nil {
		return err
	}
	run.setInterruptionSubstage("backup_stop")
	if err := run.semantic.stop(30 * time.Second); err != nil {
		return err
	}
	run.semantic = nil
	run.setInterruptionSubstage("restore")
	restoreMetrics, err := restoreBackup(
		run.ctx, run.toolchain, profile, run.workspace, backup, "interruption",
	)
	mergedMetrics, combinedErr = mergeMetricsPreservingError(
		directRecoveryIfError(err), run.partialMetrics, restoreMetrics,
	)
	run.partialMetrics = mergedMetrics
	if combinedErr != nil {
		return combinedErr
	}
	run.setInterruptionSubstage("restored_boundary")
	if err := verifyRestoredBoundary(
		run.ctx, profile, run.semanticA, planSchemaVersion(run.plan.Schema) >= 19,
	); err != nil {
		return directRecovery(err)
	}
	run.setInterruptionSubstage("first_start")
	server, meter, err := run.startServer(profile, "interruption-first", &run.semanticA)
	if err != nil {
		return err
	}
	run.semantic = server
	var trigger generationscheduler.ChunkLifecycleReport
	if planSchemaVersion(run.plan.Schema) >= 17 {
		var triggerObserver *interruptionTriggerV18Observer
		if planSchemaVersion(run.plan.Schema) >= 18 {
			triggerObserver, err = run.newInterruptionTriggerV18Observer(
				server, meter, profile, profile.Revisions["b"],
			)
			if err != nil {
				return err
			}
		}
		run.setInterruptionSubstage("delta_trigger")
		if err := run.updateSourceRevision(profile.Repository, profile.Revisions["b"]); err != nil {
			if triggerObserver != nil {
				return errors.Join(err, triggerObserver.Close())
			}
			return err
		}
		run.setInterruptionSubstage("active_lease_wait")
		var triggerErr error
		var triggerScheduleDigest string
		if triggerObserver != nil {
			trigger, triggerErr = triggerObserver.Wait(profile.Revisions["b"], 90*time.Minute)
			triggerScheduleDigest = triggerObserver.scheduleDigest
			triggerErr = errors.Join(triggerErr, triggerObserver.Close())
		} else {
			trigger, triggerErr = waitInterruptionChunkLifecycle(
				run.ctx, server, meter, profile, profile.Revisions["b"], 90*time.Minute,
			)
		}
		if triggerErr != nil {
			return triggerErr
		}
		run.recordInterruptionTrigger(trigger, triggerScheduleDigest, time.Since(started))
	} else if err := waitForDerivedPartial(run.ctx, run.plan, profile.DataDir, 90*time.Minute); err != nil {
		_ = server.stop(30 * time.Second)
		return err
	}
	run.setInterruptionSubstage("first_stop")
	if err := server.stop(30 * time.Second); err != nil {
		return err
	}
	run.semantic = nil
	if planSchemaVersion(run.plan.Schema) >= 17 {
		run.setInterruptionSubstage("source_return")
		if err := run.updateSourceRevision(profile.Repository, profile.Revisions["a"]); err != nil {
			return err
		}
	}
	firstMetrics, err := run.finishMeter(meter, nil)
	if err != nil {
		return err
	}
	var restartBoundary *dataMeasurementBoundary
	if planSchemaVersion(run.plan.Schema) >= 27 {
		restartBoundary, err = meter.takeRawEndBoundary()
		if err != nil {
			return err
		}
	}
	run.setInterruptionSubstage("restart_start")
	var restartMeter *phaseMeter
	if planSchemaVersion(run.plan.Schema) >= 27 {
		server, restartMeter, err = run.startServerV27(
			profile, "interruption-restart", &run.semanticA, restartBoundary,
		)
	} else {
		server, restartMeter, err = run.startServer(profile, "interruption-restart", &run.semanticA)
	}
	if err != nil {
		return err
	}
	run.semantic = server
	if planSchemaVersion(run.plan.Schema) >= 22 {
		run.setInterruptionSubstage("recovery_verification")
		recovered, recoverErr := waitInterruptionLeaseRecoveryV22(
			run.ctx, profile, trigger, run.observation.Interruption.TriggerScheduleSHA256,
			interruptionRecoveryContractForPlan(run.plan), 5*time.Minute,
		)
		if recoverErr != nil {
			return directRecovery(recoverErr)
		}
		run.observation.Interruption.TriggerRecoveredState = recovered
		recoveryLifecycle, lifecycleErr := readGenerationLifecycleObservationForPlan(
			run.ctx, run.plan, profile, 30*time.Second,
		)
		if lifecycleErr != nil {
			return directRecovery(lifecycleErr)
		}
		run.observation.Interruption.RecoveryLifecycle = recoveryLifecycle
	}
	run.setInterruptionSubstage("restart_convergence")
	after, err := run.waitSnapshot(profile, "a", "interruption-restart", run.fullConvergenceDeadline(), server)
	if err != nil {
		return directRecovery(err)
	}
	if recoveryAuthorityForPlan(run.plan, after) != recoveryAuthorityForPlan(run.plan, run.semanticA) {
		return directRecovery(errors.New("interruption recovery changed exact authority"))
	}
	if planSchemaVersion(run.plan.Schema) >= 22 {
		convergenceLifecycle, lifecycleErr := readGenerationLifecycleObservationForPlan(
			run.ctx, run.plan, profile, 30*time.Second,
		)
		if lifecycleErr != nil {
			return directRecovery(lifecycleErr)
		}
		run.observation.Interruption.ConvergenceLifecycle = convergenceLifecycle
		run.setInterruptionSubstage("partial_verification")
		if partialErr := waitForDerivedPartialClear(run.ctx, run.plan, profile.DataDir, 5*time.Minute); partialErr != nil {
			return directRecovery(partialErr)
		}
	} else if planSchemaVersion(run.plan.Schema) >= 17 {
		// The graceful stop drain may have settled or released the selected
		// lease before process exit, so exact-A convergence alone proves
		// nothing about the trigger chunk. Re-project it and require a
		// recovered (non-running) fate, then require that no partial derived
		// publication state survived the restart.
		run.setInterruptionSubstage("recovery_verification")
		recovered, recoverErr := waitInterruptionLeaseRecovery(
			run.ctx, profile, trigger, 5*time.Minute,
		)
		if recoverErr != nil {
			return directRecovery(recoverErr)
		}
		if run.observation.Interruption != nil {
			run.observation.Interruption.TriggerRecoveredState = recovered
		}
		if planSchemaVersion(run.plan.Schema) >= 19 {
			if partialErr := waitForDerivedPartialClear(run.ctx, run.plan, profile.DataDir, 5*time.Minute); partialErr != nil {
				return directRecovery(partialErr)
			}
		} else {
			partial, partialErr := derivedPartialPresentV18(profile.DataDir)
			if partialErr == nil && !partial {
				partial, partialErr = relationshipPartialPresentLegacy(profile.DataDir)
			}
			if partialErr != nil {
				return directRecovery(partialErr)
			}
			if partial {
				return directRecovery(errors.New(
					"T40.13 interruption restart retained partial derived publication state",
				))
			}
		}
	}
	run.setInterruptionSubstage("teardown")
	restartMetrics, err := run.finishMeter(restartMeter, &after)
	if err != nil {
		return err
	}
	metrics, err := mergeMetrics(backupMetrics, restoreMetrics, firstMetrics, restartMetrics)
	if err != nil {
		return err
	}
	run.semanticA = after
	if err := run.semantic.stop(30 * time.Second); err != nil {
		return err
	}
	run.semantic = nil
	metrics.WallMS = time.Since(started).Milliseconds()
	run.observation.Phases[5] = succeededPhase("interruption", metrics)
	if err := run.enforceSafety(); err != nil {
		return err
	}
	run.setInterruptionSubstage("complete")
	return nil
}

// waitInterruptionLeaseRecovery re-projects the interruption trigger chunk
// after restart until it leaves the running state. A healthy restart releases,
// reaps, cancels, or completes the interrupted lease within the stale window;
// a lease still running at the deadline is a recovery regression the exact-A
// snapshot cannot see.
func waitInterruptionLeaseRecovery(
	ctx context.Context,
	profile PreparedProfile,
	trigger generationscheduler.ChunkLifecycleReport,
	limit time.Duration,
) (string, error) {
	reader, err := store.OpenLocalGenerationChunkReader(ctx, profile.DataDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = reader.Close(context.WithoutCancel(ctx)) }()
	return waitLeaseRecoveryWithReader(ctx, reader, profile, trigger, limit)
}

func waitLeaseRecoveryWithReader(
	ctx context.Context,
	reader generationChunkLeaseReader,
	profile PreparedProfile,
	trigger generationscheduler.ChunkLifecycleReport,
	limit time.Duration,
) (string, error) {
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastErr error
	for {
		callCtx, callCancel := context.WithTimeout(phase, 30*time.Second)
		state, stateErr := reader.GenerationChunkLeaseState(callCtx, trigger.Identity)
		callCancel()
		switch {
		case stateErr != nil:
			// Transient projection errors retry until the recovery deadline.
			lastErr = stateErr
		case state.Identity != trigger.Identity ||
			state.Repository != profile.RepositoryName ||
			state.Stage != trigger.Stage ||
			state.Generation != trigger.Generation ||
			state.Attempt < trigger.Attempt:
			return "", errors.New("T40.13 interruption trigger chunk identity changed")
		case state.Status != store.GenerationChunkRunning:
			return string(state.Status), nil
		}
		select {
		case <-phase.Done():
			return "", errors.Join(lastErr, errors.New(
				"T40.13 interrupted lease was not recovered after restart",
			))
		case <-ticker.C:
		}
	}
}

// waitInterruptionLeaseRecoveryV22 runs immediately after restart readiness,
// before a new extraction incarnation can make the selected historical row
// eligible for collection. If collection nevertheless wins, the absence is
// accepted only after the exact selected schedule digest is independently
// fenced as non-current and retired.
func waitInterruptionLeaseRecoveryV22(
	ctx context.Context,
	profile PreparedProfile,
	trigger generationscheduler.ChunkLifecycleReport,
	scheduleDigest string,
	contract interruptionRecoveryContract,
	limit time.Duration,
) (string, error) {
	reader, err := store.OpenLocalGenerationChunkReader(ctx, profile.DataDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = reader.Close(context.WithoutCancel(ctx)) }()
	return waitLeaseRecoveryV22WithReader(
		ctx, reader, profile, trigger, scheduleDigest, contract, limit,
	)
}

type interruptionRecoveryContract uint8

const (
	interruptionRecoveryV22 interruptionRecoveryContract = iota + 1
	interruptionRecoveryV23
	interruptionRecoveryV24
)

func interruptionRecoveryContractForPlan(plan Plan) interruptionRecoveryContract {
	if planSchemaVersion(plan.Schema) >= 24 {
		return interruptionRecoveryV24
	}
	if planSchemaVersion(plan.Schema) >= 23 {
		return interruptionRecoveryV23
	}
	return interruptionRecoveryV22
}

func waitLeaseRecoveryV22WithReader(
	ctx context.Context,
	reader generationScheduleRetentionReader,
	profile PreparedProfile,
	trigger generationscheduler.ChunkLifecycleReport,
	scheduleDigest string,
	contract interruptionRecoveryContract,
	limit time.Duration,
) (string, error) {
	if reader == nil || !digestIdentity(scheduleDigest) ||
		contract < interruptionRecoveryV22 || contract > interruptionRecoveryV24 {
		return "", errors.New("T40.13 interruption recovery request is invalid")
	}
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastErr error
	collectedConfirmations := 0
	requeuedConfirmations := 0
	for {
		if phase.Err() != nil {
			return "", errors.Join(lastErr, errors.New(
				"T40.13 interrupted lease was not recovered after restart",
			))
		}
		callCtx, callCancel := context.WithTimeout(phase, 30*time.Second)
		state, stateErr := reader.GenerationChunkLeaseState(callCtx, trigger.Identity)
		callCancel()
		switch {
		case stateErr == nil:
			collectedConfirmations = 0
			if state.Identity != trigger.Identity || state.ScheduleDigest != scheduleDigest ||
				state.Repository != profile.RepositoryName || state.Stage != trigger.Stage ||
				state.Generation != trigger.Generation || state.Attempt < trigger.Attempt {
				return "", errors.New("T40.13 interruption trigger chunk identity changed")
			}
			switch state.Status {
			case store.GenerationChunkDone, store.GenerationChunkFailed, store.GenerationChunkCanceled:
				return string(state.Status), nil
			case store.GenerationChunkPending:
				if contract == interruptionRecoveryV22 {
					return string(state.Status), nil
				}
				if contract == interruptionRecoveryV24 && state.Attempt == trigger.Attempt &&
					state.Priority == store.GenerationPriorityStale && !state.Leased {
					requeuedConfirmations++
					if requeuedConfirmations >= 2 {
						return interruptionFateRequeued, nil
					}
					break
				}
				fallthrough
			case store.GenerationChunkRunning:
				requeuedConfirmations = 0
				// Pending on a still-current schedule is reclaimable and therefore
				// is not proof that the interrupted lease recovered. Wait for a
				// closed fate or corroborated retirement/collection.
			default:
				return "", errors.New("T40.13 interruption trigger chunk state is invalid")
			}
			lastErr = nil
		case errors.Is(stateErr, store.ErrNotFound):
			requeuedConfirmations = 0
			retentionCtx, retentionCancel := context.WithTimeout(phase, 30*time.Second)
			retention, retentionErr := reader.GenerationScheduleRetentionState(
				retentionCtx, profile.RepositoryName, trigger.Stage, scheduleDigest,
			)
			retentionCancel()
			switch {
			case retentionErr != nil:
				collectedConfirmations = 0
				lastErr = retentionErr
			case retention.ScheduleDigest != scheduleDigest:
				return "", errors.New("T40.13 interruption schedule identity changed")
			case retention.Current && !retention.CurrentPresent ||
				retention.Present && retention.Status != store.GenerationScheduleActive &&
					retention.Status != store.GenerationScheduleSuperseded &&
					retention.Status != store.GenerationScheduleSettled ||
				!retention.Present && retention.Status != "":
				return "", errors.New("T40.13 interruption schedule retention projection is invalid")
			case retention.CurrentPresent && !retention.Current && (!retention.Present ||
				retention.Status == store.GenerationScheduleSuperseded ||
				retention.Status == store.GenerationScheduleSettled):
				collectedConfirmations++
				if collectedConfirmations >= 2 {
					return interruptionFateCollected, nil
				}
				lastErr = stateErr
			default:
				collectedConfirmations = 0
				lastErr = stateErr
			}
		default:
			collectedConfirmations = 0
			requeuedConfirmations = 0
			lastErr = stateErr
		}
		select {
		case <-phase.Done():
			return "", errors.Join(lastErr, errors.New(
				"T40.13 interrupted lease was not recovered after restart",
			))
		case <-ticker.C:
		}
	}
}

func readGenerationLifecycleObservation(
	ctx context.Context,
	profile PreparedProfile,
	limit time.Duration,
) (*InterruptionLifecycleObservation, error) {
	inspector, err := newProfileInspector(profile, profileInspectionLegacy)
	if err != nil {
		return nil, err
	}
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	var status lifecycle.Status
	if err := inspector.get(phase, profile, "/api/lifecycle-status", &status); err != nil {
		return nil, err
	}
	if err := lifecycle.ValidateStatus(status); err != nil {
		return nil, err
	}
	return generationLifecycleObservation(status)
}

func readGenerationLifecycleObservationForPlan(
	ctx context.Context,
	plan Plan,
	profile PreparedProfile,
	limit time.Duration,
) (*InterruptionLifecycleObservation, error) {
	if planSchemaVersion(plan.Schema) < 23 {
		return readGenerationLifecycleObservation(ctx, profile, limit)
	}
	inspector, err := newProfileInspector(profile, profileInspectionLegacy)
	if err != nil {
		return nil, err
	}
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastErr error
	for {
		if phase.Err() != nil {
			return nil, errors.Join(lastErr, errors.New("T40.13 generation lifecycle projection deadline expired"))
		}
		attempt, attemptCancel := context.WithTimeout(phase, 5*time.Second)
		var status lifecycle.Status
		lastErr = inspector.get(attempt, profile, "/api/lifecycle-status", &status)
		attemptCancel()
		if lastErr == nil {
			lastErr = lifecycle.ValidateStatus(status)
		}
		if lastErr == nil {
			observation, observationErr := generationLifecycleObservation(status)
			if observationErr == nil {
				return observation, nil
			}
			lastErr = observationErr
		}
		select {
		case <-phase.Done():
			return nil, errors.Join(lastErr, errors.New("T40.13 generation lifecycle projection deadline expired"))
		case <-ticker.C:
		}
	}
}

func generationLifecycleObservation(status lifecycle.Status) (*InterruptionLifecycleObservation, error) {
	for _, owner := range status.Owners {
		if owner.Name != lifecycle.GenerationScheduleOwner {
			continue
		}
		return &InterruptionLifecycleObservation{
			State: owner.State, Completeness: owner.Completeness,
			Scanned: owner.Scanned, Deleted: owner.Deleted, Backlog: owner.Backlog,
		}, nil
	}
	return nil, errors.New("T40.13 generation lifecycle owner is absent")
}

// relationshipPartialPresentLegacy preserves the exact V17-V24 composite-root
// traversal and its historical os.ReadDir behavior.
func relationshipPartialPresentLegacy(dataDir string) (bool, error) {
	root := filepath.Join(dataDir, "relationships", "relationship-publications")
	rootInfo, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return false, errors.Join(err, errors.New("T40.13 relationship publication root is invalid"))
	}
	repositories, err := os.ReadDir(root)
	if err != nil {
		return false, err
	}
	if len(repositories) > 1 {
		return false, errors.New("T40.13 relationship publication repository inventory exceeds its bound")
	}
	for _, repository := range repositories {
		if !repository.IsDir() || repository.Type()&os.ModeSymlink != 0 {
			return false, errors.New("T40.13 relationship publication repository is invalid")
		}
		if found, err := derivedControlPresentLegacy(filepath.Join(root, repository.Name())); err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// relationshipPartialPresent checks every relationship-owned hashed
// publication namespace. All four writers can create scanner-visible stages;
// the atomic root alone is not a complete interruption-cleanliness oracle.
func relationshipPartialPresent(dataDir string) (bool, error) {
	roots := []string{
		filepath.Join(dataDir, "relationships", "relationship-publications"),
		filepath.Join(dataDir, "relationship-resolver-namespaces", "resolver-namespaces"),
		filepath.Join(dataDir, "relationship-rpc-postings", "rpc-caller-postings"),
		filepath.Join(dataDir, "relationship-kafka-postings", "kafka-topic-postings"),
	}
	for _, root := range roots {
		rootInfo, err := os.Lstat(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
			return false, errors.Join(err, errors.New("T40.13 relationship publication root is invalid"))
		}
		repositories, err := readDirectoryBounded(root, 1)
		if err != nil {
			return false, err
		}
		if len(repositories) > 1 {
			return false, errors.New("T40.13 relationship publication repository inventory exceeds its bound")
		}
		for _, repository := range repositories {
			if !repository.IsDir() || repository.Type()&os.ModeSymlink != 0 {
				return false, errors.New("T40.13 relationship publication repository is invalid")
			}
			if found, err := derivedControlPresent(filepath.Join(root, repository.Name())); err != nil || found {
				return found, err
			}
		}
	}
	return false, nil
}

func (run *execution) setInterruptionSubstage(substage string) {
	if run == nil || planSchemaVersion(run.plan.Schema) < 17 || run.observation.Interruption == nil {
		return
	}
	// The closed vocabulary lives in interruptionSubstages, shared with the
	// validator. An unknown name keeps the prior substage: less precise, but
	// the stopped observation stays sealable — a mismatch must never destroy
	// evidence after custody teardown.
	if interruptionSubstageIndex(run.observation.Schema, substage) < 0 {
		return
	}
	run.observation.Interruption.LastSubstage = substage
}

func (run *execution) recordInterruptionTrigger(
	report generationscheduler.ChunkLifecycleReport,
	scheduleDigest string,
	elapsed time.Duration,
) {
	if run == nil || run.observation.Interruption == nil {
		return
	}
	attempt := report.Attempt
	run.observation.Interruption.TriggerStage = report.Stage
	run.observation.Interruption.TriggerGenerationSHA256 = report.Generation
	run.observation.Interruption.TriggerChunkSHA256 = report.Identity
	run.observation.Interruption.TriggerScheduleSHA256 = scheduleDigest
	run.observation.Interruption.TriggerAttempt = &attempt
	run.observation.Interruption.TriggerWallMS = max(elapsed.Milliseconds(), 1)
}

func waitInterruptionChunkLifecycle(
	ctx context.Context,
	server *privateServer,
	meter *phaseMeter,
	profile PreparedProfile,
	revision string,
	limit time.Duration,
) (generationscheduler.ChunkLifecycleReport, error) {
	if server == nil || meter == nil {
		return generationscheduler.ChunkLifecycleReport{},
			errors.New("T40.13 interruption lifecycle reader is invalid")
	}
	cursor, err := newChunkLifecycleCursor(
		server.logPath, meter.logOffset, chunkLifecycleValidationV17,
	)
	if err != nil {
		return generationscheduler.ChunkLifecycleReport{}, err
	}
	leaseReader, err := store.OpenLocalGenerationChunkReader(ctx, profile.DataDir)
	if err != nil {
		return generationscheduler.ChunkLifecycleReport{}, errors.Join(err, cursor.Close())
	}
	report, waitErr := waitActiveChunkLifecycle(
		ctx, cursor, leaseReader, profile, revision, limit,
	)
	closeErr := errors.Join(cursor.Close(), leaseReader.Close(context.WithoutCancel(ctx)))
	return report, errors.Join(waitErr, closeErr)
}

type currentRunningGenerationChunkReader interface {
	generationChunkLeaseReader
	CurrentGenerationRunningChunk(
		context.Context, string, string,
	) (store.GenerationChunkLeaseState, error)
}

type interruptionTriggerV18Observer struct {
	run            *execution
	server         *privateServer
	cursor         *chunkLifecycleCursor
	leaseReader    *store.LocalGenerationChunkReader
	progressReader *store.LocalGenerationChunkReader
	profile        PreparedProfile
	inspect        convergenceInspection
	scheduleDigest string
}

// waitInterruptionTriggerV18 uses the exact current schedule as discovery
// authority. Lifecycle reports are still parsed on every short poll, but only
// to preserve structural corroboration; a start and settlement in one log
// drain cannot hide a lease that is running in the store. The exact inspector
// separately attributes an upstream/no-lease stop without retaining private
// responses or raw errors.
func (run *execution) newInterruptionTriggerV18Observer(
	server *privateServer,
	meter *phaseMeter,
	profile PreparedProfile,
	revision string,
) (*interruptionTriggerV18Observer, error) {
	if run == nil || run.ctx == nil || server == nil || meter == nil {
		return nil,
			errors.New("T40.13 V18 interruption trigger reader is invalid")
	}
	lifecycleContract, inspectionContract := interruptionContractsForPlan(run.plan.Schema)
	cursor, err := newChunkLifecycleCursor(
		server.logPath, meter.logOffset, lifecycleContract,
	)
	if err != nil {
		return nil, err
	}
	leaseReader, err := store.OpenLocalGenerationChunkReader(run.ctx, profile.DataDir)
	if err != nil {
		return nil, errors.Join(err, cursor.Close())
	}
	progressReader, err := store.OpenLocalGenerationChunkReader(run.ctx, profile.DataDir)
	if err != nil {
		return nil, errors.Join(
			err, cursor.Close(), leaseReader.Close(context.WithoutCancel(run.ctx)),
		)
	}
	inspector, err := newProfileInspector(profile, inspectionContract)
	if err != nil {
		return nil, errors.Join(
			err, cursor.Close(), leaseReader.Close(context.WithoutCancel(run.ctx)),
			progressReader.Close(context.WithoutCancel(run.ctx)),
		)
	}
	return &interruptionTriggerV18Observer{
		run: run, server: server, cursor: cursor,
		leaseReader: leaseReader, progressReader: progressReader, profile: profile,
		inspect: func(attempt context.Context) (privateProfileSnapshot, privateConvergenceProbe, error) {
			return inspector.inspectWithProgress(attempt, profile, revision)
		},
	}, nil
}

func (observer *interruptionTriggerV18Observer) Wait(
	revision string,
	limit time.Duration,
) (generationscheduler.ChunkLifecycleReport, error) {
	if observer == nil {
		return generationscheduler.ChunkLifecycleReport{},
			errors.New("T40.13 V18 interruption trigger observer is invalid")
	}
	var selected func(store.GenerationChunkLeaseState) error
	if planSchemaVersion(observer.run.plan.Schema) >= 22 {
		selected = func(state store.GenerationChunkLeaseState) error {
			if !digestIdentity(state.ScheduleDigest) {
				return errors.New("T40.13 interruption trigger schedule identity is invalid")
			}
			observer.scheduleDigest = state.ScheduleDigest
			return nil
		}
	}
	return observer.run.waitInterruptionTriggerV18WithReader(
		observer.server, observer.cursor, observer.leaseReader, observer.progressReader,
		observer.profile, revision, limit, observer.inspect, extractionGenerationBindsRevision,
		selected,
	)
}

func (observer *interruptionTriggerV18Observer) Close() error {
	if observer == nil {
		return nil
	}
	closeCtx := context.Background()
	if observer.run != nil && observer.run.ctx != nil {
		closeCtx = context.WithoutCancel(observer.run.ctx)
	}
	err := errors.Join(
		observer.cursor.Close(), observer.leaseReader.Close(closeCtx),
		observer.progressReader.Close(closeCtx),
	)
	observer.cursor = nil
	observer.leaseReader = nil
	observer.progressReader = nil
	return err
}

type interruptionInspectionResult struct {
	probe       privateConvergenceProbe
	err         error
	progress    store.GenerationScheduleProgress
	progressErr error
}

func (run *execution) waitInterruptionTriggerV18WithReader(
	server *privateServer,
	cursor *chunkLifecycleCursor,
	leaseReader currentRunningGenerationChunkReader,
	progressReader generationChunkLeaseReader,
	profile PreparedProfile,
	revision string,
	limit time.Duration,
	inspect convergenceInspection,
	binder extractionGenerationBinder,
	selected func(store.GenerationChunkLeaseState) error,
) (generationscheduler.ChunkLifecycleReport, error) {
	if run == nil || run.ctx == nil || server == nil || cursor == nil || leaseReader == nil ||
		progressReader == nil || inspect == nil || binder == nil || limit <= 0 {
		return generationscheduler.ChunkLifecycleReport{},
			errors.New("T40.13 V18 interruption trigger authority is invalid")
	}
	phase, cancel := phaseContext(run.ctx, limit)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	started := time.Now()
	nextInspection := started
	var lastErr error
	inspectionResult := make(chan interruptionInspectionResult, 1)
	var (
		inspectionCancel  context.CancelFunc
		inspectionRunning bool
		inspectionWork    sync.WaitGroup
	)
	defer func() {
		if inspectionCancel != nil {
			inspectionCancel()
		}
		inspectionWork.Wait()
	}()
	for {
		if exitErr, exited := retainServerExit(server); exited {
			return generationscheduler.ChunkLifecycleReport{},
				errors.Join(exitErr, errConvergenceServerExit)
		}
		// The log is deliberately non-authoritative in V18, but parsing it
		// keeps the frozen structural/vocabulary checks exercised.
		if _, err := cursor.poll(); err != nil {
			return generationscheduler.ChunkLifecycleReport{}, err
		}
		callCtx, callCancel := context.WithTimeout(phase, 2*time.Second)
		state, stateErr := leaseReader.CurrentGenerationRunningChunk(
			callCtx, profile.RepositoryName, extractionpublication.ScheduleStage,
		)
		callCancel()
		switch {
		case stateErr == nil:
			bound, bindErr := binder(profile, state.Generation, revision)
			if bindErr != nil {
				lastErr = bindErr
			} else if bound && state.Status == store.GenerationChunkRunning {
				if selected != nil {
					if selectedErr := selected(state); selectedErr != nil {
						return generationscheduler.ChunkLifecycleReport{}, selectedErr
					}
				}
				return generationscheduler.ChunkLifecycleReport{
					Schema: generationscheduler.ChunkLifecycleSchema,
					Event:  "started", Identity: state.Identity, Stage: state.Stage,
					Generation: state.Generation, Attempt: state.Attempt, Outcome: "running",
				}, nil
			}
		case errors.Is(stateErr, store.ErrNotFound),
			errors.Is(stateErr, store.ErrGenerationStale),
			errors.Is(stateErr, store.ErrGenerationLeaseLost):
			// The exact current schedule has no selectable running chunk yet.
		default:
			lastErr = stateErr
		}
		if !inspectionRunning && !time.Now().Before(nextInspection) {
			inspectionRunning = true
			inspectionCtx, cancelInspection := context.WithTimeout(phase, 30*time.Second)
			inspectionCancel = cancelInspection
			inspectionWork.Add(1)
			run.inspectionWork.Add(1)
			go func() {
				defer inspectionWork.Done()
				defer run.inspectionWork.Done()
				_, probe, inspectErr := inspect(inspectionCtx)
				progress, progressErr := progressReader.GenerationScheduleProgress(
					inspectionCtx, profile.RepositoryName, extractionpublication.ScheduleStage,
				)
				inspectionResult <- interruptionInspectionResult{
					probe: probe, err: inspectErr, progress: progress, progressErr: progressErr,
				}
			}()
		}
		select {
		case inspected := <-inspectionResult:
			inspectionCancel()
			inspectionCancel = nil
			inspectionRunning = false
			nextInspection = time.Now().Add(5 * time.Second)
			diagnostic := classifyConvergenceInspection(inspected.err)
			probe := inspected.probe
			run.recordInterruptionProgress(probe, diagnostic, time.Since(started))
			if diagnostic.class == "terminal" {
				return generationscheduler.ChunkLifecycleReport{}, errInterruptionProgressTerminal
			}
			if inspected.err == nil {
				return generationscheduler.ChunkLifecycleReport{}, errInterruptionTriggerUnsatisfiable
			}
			lastErr = inspected.err

			// Retain V17's exact fast-fail fence for the narrow case in which
			// extraction settled between the store selector and inspection.
			if inspected.progressErr == nil &&
				inspected.progress.Status == store.GenerationScheduleSettled {
				bound, bindErr := binder(
					profile, inspected.progress.Generation, revision,
				)
				if bindErr == nil && bound {
					return generationscheduler.ChunkLifecycleReport{},
						errInterruptionTriggerUnsatisfiable
				}
			}
		default:
		}
		select {
		case <-phase.Done():
			if run.ctx.Err() != nil {
				return generationscheduler.ChunkLifecycleReport{}, errors.Join(
					lastErr, context.Cause(run.ctx),
				)
			}
			return generationscheduler.ChunkLifecycleReport{}, errors.Join(
				lastErr, errInterruptionTriggerDeadline,
			)
		case <-ticker.C:
		}
	}
}

func (run *execution) recordInterruptionProgress(
	probe privateConvergenceProbe,
	diagnostic convergenceInspectionDiagnostic,
	elapsed time.Duration,
) {
	if run == nil || run.observation.Interruption == nil ||
		probe.Stage == "" || !digestIdentity(probe.SHA256) || diagnostic.class == "" {
		return
	}
	interruption := run.observation.Interruption
	if interruption.LastProgressSHA256 != "" && interruption.LastProgressSHA256 != probe.SHA256 {
		interruption.ProgressChanges++
	}
	interruption.LastProgressStage = probe.Stage
	interruption.LastProgressClass = diagnostic.class
	interruption.LastProgressSHA256 = probe.SHA256
	interruption.LastProgressWallMS = max(elapsed.Milliseconds(), 1)
}

func (run *execution) staleWorker() error {
	profile := run.prepared.Profiles[1]
	server, meter, err := run.startServer(profile, "stale-worker", &run.semanticA)
	if err != nil {
		return err
	}
	run.semantic = server
	cursor, err := newChunkLifecycleCursor(
		server.logPath, meter.logOffset, lifecycleValidationForPlan(run.plan.Schema),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cursor.Close() }()
	leaseReader, err := store.OpenLocalGenerationChunkReader(run.ctx, profile.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = leaseReader.Close(context.Background()) }()
	if err := run.updateSourceRevision(profile.Repository, profile.Revisions["b"]); err != nil {
		return err
	}
	var started generationscheduler.ChunkLifecycleReport
	var selectedScheduleDigest string
	var releaseFence func()
	defer func() {
		if releaseFence != nil {
			releaseFence()
		}
	}()
	if planSchemaVersion(run.plan.Schema) >= 18 {
		// Arm the stale-fence contention in the readiness rehearsal's verified
		// order BEFORE selecting a chunk: the exclusive index mutation lock
		// freezes the A re-index across the fence window, and the diagnostic
		// fence below removes the current pointer beneath the selected worker
		// so its ordinary completion settles stale_fenced, the outcome this
		// phase's oracle requires. Without the arming the selected ~1-second
		// chunk settles completed against a still-current pointer.
		if err := waitExactActiveGenerationSchedule(
			run.ctx, leaseReader, profile, profile.Revisions["b"], 90*time.Minute,
		); err != nil {
			return err
		}
		releaseFence, err = focusedindex.AcquireExclusiveMutationLock(
			run.ctx, filepath.Join(profile.DataDir, "index"),
		)
		if err != nil {
			return err
		}
		started, err = waitCurrentRunningGenerationChunk(
			run.ctx, cursor, leaseReader, profile, profile.Revisions["b"],
			90*time.Minute, extractionGenerationBindsRevision,
		)
	} else {
		started, err = waitActiveChunkLifecycle(
			run.ctx, cursor, leaseReader, profile, profile.Revisions["b"], 90*time.Minute,
		)
	}
	if err != nil {
		return err
	}
	if err := run.updateSourceRevision(profile.Repository, profile.Revisions["a"]); err != nil {
		return err
	}
	if planSchemaVersion(run.plan.Schema) >= 18 {
		// The selected chunk can settle or defer inside the small selection-
		// to-fence window (production chunks settle in about a second, and
		// non-domain-final completions never touch the index lock). With the
		// exclusive lock held the B schedule keeps issuing running chunks, so
		// re-select and fence again instead of aborting the four-hour run.
		started, selectedScheduleDigest, err = fenceRunningGenerationChunkForDiagnostic(
			run.ctx, cursor, leaseReader, profile, profile.Revisions["b"], started, 10*time.Minute,
		)
		if err != nil {
			return err
		}
		releaseFence()
		releaseFence = nil
	} else {
		afterSupersession, err := leaseReader.GenerationChunkLeaseState(run.ctx, started.Identity)
		if err != nil || !runningLeaseMatchesReport(afterSupersession, started, profile.RepositoryName) {
			return errors.Join(err, errors.New("T40.13 selected B lease did not remain active across supersession"))
		}
	}
	after, err := run.waitSnapshot(profile, "a", "stale-worker", run.fullConvergenceDeadline(), server)
	if err != nil {
		return err
	}
	if recoveryAuthorityForPlan(run.plan, after) != recoveryAuthorityForPlan(run.plan, run.semanticA) {
		return exactOracle("stale worker moved final authority")
	}
	var settled generationscheduler.ChunkLifecycleReport
	if planSchemaVersion(run.plan.Schema) >= 23 {
		settled, err = waitStaleChunkFence(
			run.ctx, cursor, leaseReader, started, profile.RepositoryName,
			selectedScheduleDigest, 20*time.Minute,
		)
	} else {
		settled, err = waitSettledChunkLifecycle(
			run.ctx, cursor, started, 20*time.Minute,
		)
	}
	if err != nil {
		return err
	}
	if settled.Generation != started.Generation || settled.Attempt != started.Attempt ||
		settled.Outcome != "stale_fenced" {
		return errors.New("T40.13 selected B lease settled before the stale-fence exercise")
	}
	if err := leaseReader.Close(run.ctx); err != nil {
		return err
	}
	metrics, err := run.finishMeter(meter, &after)
	if err != nil {
		return err
	}
	if err := run.semantic.stop(30 * time.Second); err != nil {
		return err
	}
	run.semantic = nil
	run.semanticA = after
	run.observation.Phases[6] = succeededPhase("stale_worker", metrics)
	return run.enforceSafety()
}

func fenceRunningGenerationChunkForDiagnostic(
	ctx context.Context,
	cursor *chunkLifecycleCursor,
	reader *store.LocalGenerationChunkReader,
	profile PreparedProfile,
	revision string,
	started generationscheduler.ChunkLifecycleReport,
	reselectionLimit time.Duration,
) (generationscheduler.ChunkLifecycleReport, string, error) {
	const selectionAttempts = 8
	for attempt := 0; attempt < selectionAttempts; attempt++ {
		state, stateErr := reader.GenerationChunkLeaseState(ctx, started.Identity)
		if stateErr == nil && runningLeaseMatchesReport(state, started, profile.RepositoryName) &&
			digestIdentity(state.ScheduleDigest) {
			fenceErr := reader.FenceCurrentGenerationScheduleForDiagnostic(ctx, state)
			if fenceErr == nil {
				return started, state.ScheduleDigest, nil
			}
			if !errors.Is(fenceErr, store.ErrGenerationStale) {
				return generationscheduler.ChunkLifecycleReport{}, "", fenceErr
			}
		} else if stateErr != nil && !errors.Is(stateErr, store.ErrNotFound) {
			return generationscheduler.ChunkLifecycleReport{}, "", stateErr
		}
		if attempt+1 == selectionAttempts {
			break
		}
		var err error
		started, err = waitCurrentRunningGenerationChunk(
			ctx, cursor, reader, profile, revision,
			reselectionLimit, extractionGenerationBindsRevision,
		)
		if err != nil {
			return generationscheduler.ChunkLifecycleReport{}, "", err
		}
	}
	return generationscheduler.ChunkLifecycleReport{}, "", errors.New(
		"T40.13 selected B lease did not remain active across supersession",
	)
}

const (
	maxChunkLifecyclePendingBytes   = 1 << 20
	maxChunkLifecycleReportsPerPoll = 400_000
)

// chunkLifecycleValidation selects the frozen validation contract for one
// cursor. V16 keeps the exact historical behavior (extraction stage only,
// no outcome inspection, violations fatal); V17 checks structure fatally but
// treats vocabulary drift — an unknown stage, event pairing, or settled
// outcome — as validated-then-discarded, so a future scheduler outcome cannot
// abort a healthy ceremony.
type chunkLifecycleValidation uint8

const (
	chunkLifecycleValidationV16 chunkLifecycleValidation = iota
	chunkLifecycleValidationV17
	chunkLifecycleValidationV23
)

func lifecycleValidationForPlan(schema string) chunkLifecycleValidation {
	if planSchemaVersion(schema) >= 23 {
		return chunkLifecycleValidationV23
	}
	if planSchemaVersion(schema) >= 17 {
		return chunkLifecycleValidationV17
	}
	return chunkLifecycleValidationV16
}

func interruptionContractsForPlan(
	schema string,
) (chunkLifecycleValidation, profileInspectionContract) {
	if planSchemaVersion(schema) >= 25 {
		return lifecycleValidationForPlan(schema), profileInspectionForPlan(schema)
	}
	return chunkLifecycleValidationV17, profileInspectionV16
}

type chunkLifecycleCursor struct {
	file       *os.File
	pending    []byte
	buffer     []byte
	validation chunkLifecycleValidation
}

func newChunkLifecycleCursor(
	logPath string,
	offset int64,
	validation chunkLifecycleValidation,
) (*chunkLifecycleCursor, error) {
	file, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &chunkLifecycleCursor{
		file: file, buffer: make([]byte, 64<<10), validation: validation,
	}, nil
}

func (cursor *chunkLifecycleCursor) Close() error {
	if cursor == nil || cursor.file == nil {
		return nil
	}
	err := cursor.file.Close()
	cursor.file = nil
	return err
}

func (cursor *chunkLifecycleCursor) poll() ([]generationscheduler.ChunkLifecycleReport, error) {
	if cursor == nil || cursor.file == nil {
		return nil, errors.New("T40.13 chunk lifecycle cursor is invalid")
	}
	position, err := cursor.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	info, err := cursor.file.Stat()
	if err != nil || info.Size() < position {
		return nil, errors.Join(err, errors.New("T40.13 chunk lifecycle log changed identity or shrank"))
	}
	remaining := info.Size() - position
	reports := make([]generationscheduler.ChunkLifecycleReport, 0, 8)
	for remaining > 0 {
		readSize := min(int64(len(cursor.buffer)), remaining)
		count, readErr := io.ReadFull(cursor.file, cursor.buffer[:readSize])
		if readErr != nil {
			return nil, readErr
		}
		if count > 0 {
			remaining -= int64(count)
			cursor.pending = append(cursor.pending, cursor.buffer[:count]...)
			lastNewline := bytes.LastIndexByte(cursor.pending, '\n')
			if lastNewline >= 0 {
				lines := bytes.Split(cursor.pending[:lastNewline], []byte{'\n'})
				for _, line := range lines {
					report, found, parseErr := parseChunkLifecycleLine(line, cursor.validation)
					if parseErr != nil {
						return nil, parseErr
					}
					if found {
						reports = append(reports, report)
						if len(reports) > maxChunkLifecycleReportsPerPoll {
							return nil, errors.New("T40.13 chunk lifecycle drain exceeds its report bound")
						}
					}
				}
				tail := lastNewline + 1
				copy(cursor.pending, cursor.pending[tail:])
				cursor.pending = cursor.pending[:len(cursor.pending)-tail]
			}
			if len(cursor.pending) > maxChunkLifecyclePendingBytes {
				return nil, errors.New("T40.13 chunk lifecycle line exceeds its bound")
			}
		}
	}
	return reports, nil
}

func parseChunkLifecycleLine(
	line []byte,
	validation chunkLifecycleValidation,
) (generationscheduler.ChunkLifecycleReport, bool, error) {
	const prefix = "generation chunk lifecycle: "
	if !bytes.Contains(line, []byte(prefix)) {
		return generationscheduler.ChunkLifecycleReport{}, false, nil
	}
	var report generationscheduler.ChunkLifecycleReport
	if err := decodeLogObject(line, prefix, &report); err != nil {
		return generationscheduler.ChunkLifecycleReport{}, false,
			errors.New("T40.13 chunk lifecycle report is malformed")
	}
	if validation == chunkLifecycleValidationV16 {
		// Frozen V16 execution semantics: extraction stage only, no outcome
		// inspection, every violation fatal.
		if report.Stage != extractionpublication.ScheduleStage ||
			validateChunkLifecycleShape(report) != nil {
			return generationscheduler.ChunkLifecycleReport{}, false,
				errors.New("T40.13 chunk lifecycle report is invalid")
		}
		return report, true, nil
	}
	if err := validateChunkLifecycleShape(report); err != nil {
		return generationscheduler.ChunkLifecycleReport{}, false, err
	}
	// V17-V22 validate vocabulary drift and then discard it because the store
	// lease projection is their load-bearing authority. V23 closes a settled
	// unknown into an internal outcome so its stale-worker wait fails promptly.
	if !validChunkLifecycleStage(report.Stage) ||
		report.Event == "started" && report.Outcome != "running" ||
		report.Event == "settled" && !validSettledChunkLifecycleOutcome(report.Outcome) {
		if validation == chunkLifecycleValidationV23 &&
			validChunkLifecycleStage(report.Stage) && report.Event == "settled" {
			report.Outcome = chunkLifecycleOutcomeUnknown
			return report, true, nil
		}
		return generationscheduler.ChunkLifecycleReport{}, false, nil
	}
	return report, true, nil
}

const chunkLifecycleOutcomeUnknown = "unknown"

// validateChunkLifecycleShape is the structural (vocabulary-free) validation
// shared by both contracts: schema, event, digests, and scalar bounds.
func validateChunkLifecycleShape(report generationscheduler.ChunkLifecycleReport) error {
	if report.Schema != generationscheduler.ChunkLifecycleSchema ||
		(report.Event != "started" && report.Event != "settled") ||
		!digestIdentity(report.Identity) || !digestIdentity(report.Generation) || report.Attempt < 0 ||
		report.DurationMS < 0 || report.Event == "started" && report.DurationMS != 0 {
		return errors.New("T40.13 chunk lifecycle report is invalid")
	}
	return nil
}

// validSettledChunkLifecycleOutcome mirrors the outcome literals emitted by
// internal/generationscheduler/runner.go's execute(). An outcome added there
// and missed here is discarded (never fatal) under the V17 contract, and the
// store lease projection still governs candidate selection.
func validSettledChunkLifecycleOutcome(outcome string) bool {
	switch outcome {
	case "handler_failed", "heartbeat_failed", "stale_fenced", "released",
		"completed", "completion_failed", "terminal", "terminal_record_failed",
		"deferred", "deferral_failed", "retried", "exhausted":
		return true
	default:
		return false
	}
}

func validChunkLifecycleStage(stage string) bool {
	switch stage {
	case observationpublication.PlanningScheduleStage,
		observationpublication.InventoryScheduleStageV2,
		observationpublication.ScheduleStage,
		extractionpublication.ScheduleStage,
		relationshippublication.ScheduleStage:
		return true
	default:
		return false
	}
}

func chunkLifecycleKey(report generationscheduler.ChunkLifecycleReport) string {
	return fmt.Sprintf("%s\x00%s\x00%d", report.Generation, report.Identity, report.Attempt)
}

type generationChunkLeaseReader interface {
	GenerationChunkLeaseState(context.Context, string) (store.GenerationChunkLeaseState, error)
	GenerationScheduleProgress(context.Context, string, string) (store.GenerationScheduleProgress, error)
}

type generationScheduleRetentionReader interface {
	generationChunkLeaseReader
	GenerationScheduleRetentionState(
		context.Context, string, string, string,
	) (store.GenerationScheduleRetentionState, error)
}

// errInterruptionTriggerUnsatisfiable is the typed fast-fail for a B pipeline
// that settled before any lease was selectable: waiting longer cannot succeed,
// so the phase stops immediately instead of idling to its deadline.
var errInterruptionTriggerUnsatisfiable = errors.New(
	"T40.13 B extraction settled before a live lease was selectable",
)

type extractionGenerationBinder func(PreparedProfile, string, string) (bool, error)

func waitActiveChunkLifecycle(
	ctx context.Context,
	cursor *chunkLifecycleCursor,
	leaseReader generationChunkLeaseReader,
	profile PreparedProfile,
	revision string,
	limit time.Duration,
) (generationscheduler.ChunkLifecycleReport, error) {
	return waitActiveChunkLifecycleWithBinder(
		ctx, cursor, leaseReader, profile, revision, limit, extractionGenerationBindsRevision,
	)
}

func waitActiveChunkLifecycleWithBinder(
	ctx context.Context,
	cursor *chunkLifecycleCursor,
	leaseReader generationChunkLeaseReader,
	profile PreparedProfile,
	revision string,
	limit time.Duration,
	binder extractionGenerationBinder,
) (generationscheduler.ChunkLifecycleReport, error) {
	if ctx == nil || cursor == nil || leaseReader == nil || binder == nil || limit <= 0 {
		return generationscheduler.ChunkLifecycleReport{},
			errors.New("T40.13 chunk lifecycle authority is invalid")
	}
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	active := make(map[string]generationscheduler.ChunkLifecycleReport)
	// Transient binder or lease-projection failures retry on later ticks: one
	// store hiccup while the live pipeline mutates state must not abort a
	// multi-hour ceremony. Only the deadline surfaces the retained error.
	var lastErr error
	// The first settled probe runs on the first pass: a schedule settled for
	// a prior revision does not bind the commanded one, so an early read is
	// harmless and an already-settled commanded pipeline fails fast.
	settledProbe := time.Now().Add(-5 * time.Second)
	for {
		reports, err := cursor.poll()
		if err != nil {
			return generationscheduler.ChunkLifecycleReport{}, err
		}
		updateActiveChunkLifecycles(active, reports)
		keys := make([]string, 0, len(active))
		for key := range active {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			report := active[key]
			if report.Stage != extractionpublication.ScheduleStage {
				continue
			}
			bound, bindErr := binder(profile, report.Generation, revision)
			if bindErr != nil {
				lastErr = bindErr
				break
			}
			if !bound {
				continue
			}
			callCtx, callCancel := context.WithTimeout(phase, 30*time.Second)
			state, stateErr := leaseReader.GenerationChunkLeaseState(callCtx, report.Identity)
			callCancel()
			if stateErr != nil {
				lastErr = stateErr
				break
			}
			if !runningLeaseMatchesReport(state, report, profile.RepositoryName) {
				delete(active, key)
				continue
			}
			return report, nil
		}
		if time.Since(settledProbe) >= 5*time.Second {
			settledProbe = time.Now()
			callCtx, callCancel := context.WithTimeout(phase, 30*time.Second)
			progress, progressErr := leaseReader.GenerationScheduleProgress(
				callCtx, profile.RepositoryName, extractionpublication.ScheduleStage,
			)
			callCancel()
			// A settled schedule that binds the commanded revision cannot
			// produce another selectable lease: stop with a typed error
			// instead of idling to the deadline. A settled schedule for a
			// prior revision keeps waiting for the commanded pipeline.
			if progressErr == nil && progress.Status == store.GenerationScheduleSettled {
				if bound, bindErr := binder(profile, progress.Generation, revision); bindErr == nil && bound {
					return generationscheduler.ChunkLifecycleReport{}, errInterruptionTriggerUnsatisfiable
				}
			}
		}
		select {
		case <-phase.Done():
			return generationscheduler.ChunkLifecycleReport{}, errors.Join(
				lastErr, errors.New("T40.13 live B-bound chunk deadline expired"),
			)
		case <-ticker.C:
		}
	}
}

// waitExactActiveGenerationSchedule waits until the exact current extraction
// schedule is active and bound to the requested revision, so the stale-fence
// contention can be armed before a chunk is selected.
func waitExactActiveGenerationSchedule(
	ctx context.Context,
	reader generationChunkLeaseReader,
	profile PreparedProfile,
	revision string,
	limit time.Duration,
) error {
	wait, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		call, callCancel := context.WithTimeout(wait, 2*time.Second)
		progress, err := reader.GenerationScheduleProgress(
			call, profile.RepositoryName, extractionpublication.ScheduleStage,
		)
		callCancel()
		if err == nil {
			bound, bindErr := extractionGenerationBindsRevision(
				profile, progress.Generation, revision,
			)
			switch {
			case bindErr != nil:
				lastErr = bindErr
			case bound && progress.Status == store.GenerationScheduleActive:
				return nil
			case bound && progress.Status == store.GenerationScheduleSettled:
				return errors.New("T40.13 schedule settled before contention was armed")
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			lastErr = err
		}
		select {
		case <-wait.Done():
			return errors.Join(lastErr, errors.New("T40.13 active schedule deadline expired"))
		case <-ticker.C:
		}
	}
}

func waitCurrentRunningGenerationChunk(
	ctx context.Context,
	cursor *chunkLifecycleCursor,
	reader currentRunningGenerationChunkReader,
	profile PreparedProfile,
	revision string,
	limit time.Duration,
	binder extractionGenerationBinder,
) (generationscheduler.ChunkLifecycleReport, error) {
	if ctx == nil || cursor == nil || reader == nil || binder == nil || limit <= 0 {
		return generationscheduler.ChunkLifecycleReport{},
			errors.New("T40.13 current generation chunk authority is invalid")
	}
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	settledProbe := time.Now().Add(-5 * time.Second)
	var lastErr error
	for {
		if _, err := cursor.poll(); err != nil {
			return generationscheduler.ChunkLifecycleReport{}, err
		}
		callCtx, callCancel := context.WithTimeout(phase, 2*time.Second)
		state, stateErr := reader.CurrentGenerationRunningChunk(
			callCtx, profile.RepositoryName, extractionpublication.ScheduleStage,
		)
		callCancel()
		switch {
		case stateErr == nil:
			bound, bindErr := binder(profile, state.Generation, revision)
			if bindErr != nil {
				lastErr = bindErr
			} else if bound && state.Status == store.GenerationChunkRunning {
				return generationscheduler.ChunkLifecycleReport{
					Schema: generationscheduler.ChunkLifecycleSchema,
					Event:  "started", Identity: state.Identity, Stage: state.Stage,
					Generation: state.Generation, Attempt: state.Attempt, Outcome: "running",
				}, nil
			}
		case errors.Is(stateErr, store.ErrNotFound),
			errors.Is(stateErr, store.ErrGenerationStale),
			errors.Is(stateErr, store.ErrGenerationLeaseLost):
		default:
			lastErr = stateErr
		}
		if time.Since(settledProbe) >= 5*time.Second {
			settledProbe = time.Now()
			progressCtx, progressCancel := context.WithTimeout(phase, 2*time.Second)
			progress, progressErr := reader.GenerationScheduleProgress(
				progressCtx, profile.RepositoryName, extractionpublication.ScheduleStage,
			)
			progressCancel()
			if progressErr == nil && progress.Status == store.GenerationScheduleSettled {
				bound, bindErr := binder(profile, progress.Generation, revision)
				if bindErr == nil && bound {
					return generationscheduler.ChunkLifecycleReport{},
						errors.New("T40.13 current B-bound schedule settled before lease selection")
				}
			}
		}
		select {
		case <-phase.Done():
			return generationscheduler.ChunkLifecycleReport{}, errors.Join(
				lastErr, errors.New("T40.13 current B-bound chunk deadline expired"),
			)
		case <-ticker.C:
		}
	}
}

func updateActiveChunkLifecycles(
	active map[string]generationscheduler.ChunkLifecycleReport,
	reports []generationscheduler.ChunkLifecycleReport,
) {
	for _, report := range reports {
		if report.Stage != extractionpublication.ScheduleStage {
			continue
		}
		key := chunkLifecycleKey(report)
		if report.Event == "started" {
			active[key] = report
		} else {
			delete(active, key)
		}
	}
}

func leaseStateMatchesReport(
	state store.GenerationChunkLeaseState,
	report generationscheduler.ChunkLifecycleReport,
	repository string,
) bool {
	return state.Identity == report.Identity && state.Repository == repository &&
		state.Stage == report.Stage &&
		state.Generation == report.Generation && state.Attempt == report.Attempt
}

func runningLeaseMatchesReport(
	state store.GenerationChunkLeaseState,
	report generationscheduler.ChunkLifecycleReport,
	repository string,
) bool {
	return leaseStateMatchesReport(state, report, repository) &&
		state.Status == store.GenerationChunkRunning
}

func waitSettledChunkLifecycle(
	ctx context.Context,
	cursor *chunkLifecycleCursor,
	started generationscheduler.ChunkLifecycleReport,
	limit time.Duration,
) (generationscheduler.ChunkLifecycleReport, error) {
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	want := chunkLifecycleKey(started)
	for {
		reports, err := cursor.poll()
		if err != nil {
			return generationscheduler.ChunkLifecycleReport{}, err
		}
		for _, report := range reports {
			if report.Event == "settled" && chunkLifecycleKey(report) == want {
				return report, nil
			}
		}
		select {
		case <-phase.Done():
			return generationscheduler.ChunkLifecycleReport{}, errors.New("T40.13 selected chunk settlement deadline expired")
		case <-ticker.C:
		}
	}
}

func waitStaleChunkFence(
	ctx context.Context,
	cursor *chunkLifecycleCursor,
	leaseReader generationScheduleRetentionReader,
	started generationscheduler.ChunkLifecycleReport,
	repository string,
	scheduleDigest string,
	limit time.Duration,
) (generationscheduler.ChunkLifecycleReport, error) {
	if !digestIdentity(scheduleDigest) {
		return generationscheduler.ChunkLifecycleReport{},
			errors.New("T40.13 selected chunk schedule identity is invalid")
	}
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	want := chunkLifecycleKey(started)
	storeFallback := false
	for {
		reports, err := cursor.poll()
		if err != nil {
			return generationscheduler.ChunkLifecycleReport{}, err
		}
		for _, report := range reports {
			if report.Event != "settled" || chunkLifecycleKey(report) != want {
				continue
			}
			switch report.Outcome {
			case "stale_fenced":
				return report, nil
			case "completion_failed", "heartbeat_failed":
				// The chunk is still store-authoritative; the reaper may close
				// the stale lease in place after this persistence failure.
				storeFallback = true
			case chunkLifecycleOutcomeUnknown:
				return generationscheduler.ChunkLifecycleReport{},
					errors.New("T40.13 selected chunk settled with an unknown lifecycle outcome")
			default:
				return generationscheduler.ChunkLifecycleReport{},
					errors.New("T40.13 selected B lease settled before the stale-fence exercise")
			}
		}
		if !storeFallback {
			select {
			case <-phase.Done():
				return generationscheduler.ChunkLifecycleReport{},
					errors.New("T40.13 selected chunk settlement deadline expired")
			case <-ticker.C:
				continue
			}
		}
		callCtx, callCancel := context.WithTimeout(phase, 30*time.Second)
		state, stateErr := leaseReader.GenerationChunkLeaseState(callCtx, started.Identity)
		callCancel()
		switch {
		case stateErr == nil && !leaseStateMatchesReport(state, started, repository):
			return generationscheduler.ChunkLifecycleReport{},
				errors.New("T40.13 selected chunk store identity changed")
		case stateErr == nil && state.Status == store.GenerationChunkCanceled:
			settled := started
			settled.Event = "settled"
			settled.Outcome = "stale_fenced"
			return settled, nil
		case stateErr == nil && state.Status != store.GenerationChunkRunning:
			return generationscheduler.ChunkLifecycleReport{},
				errors.New("T40.13 selected B lease settled before the stale-fence exercise")
		case errors.Is(stateErr, store.ErrNotFound):
			retentionCtx, retentionCancel := context.WithTimeout(phase, 30*time.Second)
			retention, retentionErr := leaseReader.GenerationScheduleRetentionState(
				retentionCtx, repository, started.Stage, scheduleDigest,
			)
			retentionCancel()
			if retentionErr != nil {
				return generationscheduler.ChunkLifecycleReport{}, retentionErr
			}
			if retention.ScheduleDigest != scheduleDigest || !retention.CurrentPresent || retention.Current ||
				retention.Present && retention.Status != store.GenerationScheduleSuperseded &&
					retention.Status != store.GenerationScheduleSettled ||
				!retention.Present && retention.Status != "" {
				return generationscheduler.ChunkLifecycleReport{},
					errors.New("T40.13 selected chunk disappeared before stale-fence proof")
			}
			settled := started
			settled.Event = "settled"
			settled.Outcome = "stale_fenced"
			return settled, nil
		case stateErr != nil:
			return generationscheduler.ChunkLifecycleReport{}, stateErr
		}
		select {
		case <-phase.Done():
			return generationscheduler.ChunkLifecycleReport{},
				errors.New("T40.13 selected chunk settlement deadline expired")
		case <-ticker.C:
		}
	}
}

func (run *execution) pressure() error {
	started := time.Now()
	profile := run.prepared.Profiles[0]
	priorMeter, err := beginPhaseMeter(run.structural, run.workspace, &run.structAR)
	if err != nil {
		return err
	}
	run.trackMeter(priorMeter)
	if err := run.structural.stop(30 * time.Second); err != nil {
		return err
	}
	run.structural = nil
	priorMetrics, err := run.finishMeter(priorMeter, &run.structAR)
	if err != nil {
		return err
	}
	ballast, pressureLogical, pressureAllocated, err := createPressureBallast(
		run.ctx, run.workspace, run.plan.Safety, pressureBallastContractForPlan(run.plan),
	)
	if err != nil {
		return err
	}
	run.partialMetrics.DataLogicalBytes = max(run.partialMetrics.DataLogicalBytes, pressureLogical)
	run.partialMetrics.DataAllocatedBytes = max(run.partialMetrics.DataAllocatedBytes, pressureAllocated)
	ballastPresent := true
	defer func() {
		if ballastPresent {
			_ = ballast.remove(run.workspace)
		}
	}()
	server, pressureMeter, err := run.startServer(profile, "pressure-restart", &run.structAR)
	if err != nil {
		return err
	}
	run.structural = server
	if _, err := waitLifecyclePressureForPlan(
		run.ctx, run.plan, profile, true, lifecycle.PressureCollect,
		pressureWaitEnter, 10*time.Minute,
	); err != nil {
		return err
	}
	after, err := run.waitSnapshot(profile, "a-return", "pressure", run.revalidationDeadline(), run.structural)
	if err != nil {
		return err
	}
	if stablePhaseAuthorityForPlan(run.plan, after) != stablePhaseAuthorityForPlan(run.plan, run.structAR) {
		return exactOracle("pressure collection changed protected authority")
	}
	if err := ballast.remove(run.workspace); err != nil {
		return err
	}
	ballastPresent = false
	if _, err := waitLifecyclePressureForPlan(
		run.ctx, run.plan, profile, true, lifecycle.PressureNormal,
		pressureWaitRecover, 10*time.Minute,
	); err != nil {
		return err
	}
	pressureMetrics, err := run.finishMeter(pressureMeter, &after)
	if err != nil {
		return err
	}
	metrics, err := mergeMetrics(priorMetrics, pressureMetrics)
	if err != nil {
		return err
	}
	metrics.WallMS = time.Since(started).Milliseconds()
	metrics.DataLogicalBytes = max(metrics.DataLogicalBytes, pressureLogical)
	metrics.DataAllocatedBytes = max(metrics.DataAllocatedBytes, pressureAllocated)
	run.structAR = after
	run.observation.Phases[7] = succeededPhase("pressure", metrics)
	return run.enforceSafety()
}

func (run *execution) archiveRestore() error {
	profile := run.prepared.Profiles[0]
	started := time.Now()
	backupServerMeter, err := beginPhaseMeter(run.structural, run.workspace, &run.structAR)
	if err != nil {
		return err
	}
	run.trackMeter(backupServerMeter)
	backup, backupCommandMetrics, err := createLiveBackup(
		run.ctx, run.toolchain, profile, run.workspace, "archive-restore",
	)
	mergedMetrics, combinedErr := mergeMetricsPreservingError(
		directRecoveryIfError(err), run.partialMetrics, backupCommandMetrics,
	)
	run.partialMetrics = mergedMetrics
	if combinedErr != nil {
		return combinedErr
	}
	backupServerMetrics, err := run.finishMeter(backupServerMeter, &run.structAR)
	if err != nil {
		return err
	}
	backupMetrics, err := mergeConcurrentMetrics(backupServerMetrics, backupCommandMetrics)
	if err != nil {
		return err
	}
	if err := run.structural.stop(30 * time.Second); err != nil {
		return err
	}
	run.structural = nil
	restoreMetrics, err := restoreBackup(
		run.ctx, run.toolchain, profile, run.workspace, backup, "archive-restore",
	)
	mergedMetrics, combinedErr = mergeMetricsPreservingError(
		directRecoveryIfError(err), run.partialMetrics, restoreMetrics,
	)
	run.partialMetrics = mergedMetrics
	if combinedErr != nil {
		return combinedErr
	}
	if err := verifyRestoredBoundary(
		run.ctx, profile, run.structAR, planSchemaVersion(run.plan.Schema) >= 19,
	); err != nil {
		return directRecovery(err)
	}
	server, meter, err := run.startServer(profile, "archive-restore", &run.structAR)
	if err != nil {
		return err
	}
	run.structural = server
	after, err := run.waitSnapshot(profile, "a-return", "archive-restore", run.fullConvergenceDeadline(), server)
	if err != nil {
		return directRecovery(err)
	}
	restoredEqual := after.ObservationGeneration == run.structAR.ObservationGeneration &&
		after.RelationshipGeneration == run.structAR.RelationshipGeneration
	if planSchemaVersion(run.plan.Schema) >= 19 {
		restoredEqual = after.ObservationGeneration == run.structAR.ObservationGeneration &&
			privateRestoreProductEqual(after, run.structAR)
	}
	if !restoredEqual {
		return directRecovery(errors.New("archive restore changed precious authority"))
	}
	metrics, err := run.finishMeter(meter, &after)
	if err != nil {
		return err
	}
	metrics, err = mergeMetrics(backupMetrics, restoreMetrics, metrics)
	if err != nil {
		return err
	}
	metrics.WallMS = time.Since(started).Milliseconds()
	metrics, err = archiveRestoreDataMetricsForPlan(run.plan, run.workspace, metrics)
	if err != nil {
		return err
	}
	run.structAR = after
	run.observation.Phases[8] = succeededPhase("archive_restore", metrics)
	return run.enforceSafety()
}

func archiveRestoreDataMetricsForPlan(
	plan Plan,
	workspace string,
	metrics PhaseMetrics,
) (PhaseMetrics, error) {
	if planSchemaVersion(plan.Schema) >= 27 {
		return metrics, nil
	}
	logical, allocated, err := measureDataBytesForPlan(plan, workspace)
	if err != nil {
		return metrics, err
	}
	metrics.DataLogicalBytes = logical
	metrics.DataAllocatedBytes = allocated
	return metrics, nil
}

func (run *execution) collection() error {
	profile := run.prepared.Profiles[0]
	meter, err := beginPhaseMeter(run.structural, run.workspace, &run.structAR)
	if err != nil {
		return err
	}
	run.trackMeter(meter)
	if _, err := waitLifecycleForPlan(run.ctx, run.plan, profile, true, 10*time.Minute); err != nil {
		return err
	}
	after, err := run.waitSnapshot(profile, "a-return", "collection", run.revalidationDeadline(), run.structural)
	if err != nil {
		return err
	}
	if stablePhaseAuthorityForPlan(run.plan, after) != stablePhaseAuthorityForPlan(run.plan, run.structAR) {
		return exactOracle("collection changed protected authority")
	}
	metrics, err := run.finishMeter(meter, &after)
	if err != nil {
		return err
	}
	run.observation.Phases[9] = succeededPhase("collection", metrics)
	if planSchemaVersion(run.plan.Schema) >= 23 {
		run.structAR = after
	}
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
	semanticServer, semanticMeter, err := run.startServer(semanticProfile, "authorized-query", &run.semanticA)
	if err != nil {
		return err
	}
	run.semantic = semanticServer
	semanticAfter, err := run.waitSnapshot(semanticProfile, "a", "authorized-query-semantic", run.revalidationDeadline(), semanticServer)
	if err != nil {
		return err
	}
	if stablePhaseAuthorityForPlan(run.plan, semanticAfter) != stablePhaseAuthorityForPlan(run.plan, run.semanticA) {
		return exactOracle("authorized-query restart changed semantic authority")
	}
	query := queryProfile
	if planSchemaVersion(run.plan.Schema) >= 23 {
		query = func(
			ctx context.Context,
			profile PreparedProfile,
			serviceKey string,
			requireCitation bool,
		) (int, bool, error) {
			count, exact, failure, queryErr := queryProfileV23(
				ctx, profile, serviceKey, requireCitation,
			)
			if queryErr != nil {
				run.observation.AuthorizedQuery = failure
			}
			return count, exact, queryErr
		}
	}
	structCount, structExact, err := query(run.ctx, run.prepared.Profiles[0], "service-000", false)
	if err != nil {
		return err
	}
	semanticCount, semanticExact, err := query(run.ctx, run.prepared.Profiles[1], "semantic", true)
	if err != nil {
		return err
	}
	if structCount < 0 || semanticCount < 0 || !structExact || !semanticExact {
		return exactOracle("authorized query oracle failed")
	}
	structAfter, err := run.waitSnapshot(run.prepared.Profiles[0], "a-return", "authorized-query-structural", run.revalidationDeadline(), run.structural)
	if err != nil {
		return err
	}
	if planSchemaVersion(run.plan.Schema) >= 23 &&
		stablePhaseAuthorityForPlan(run.plan, structAfter) != stablePhaseAuthorityForPlan(run.plan, run.structAR) {
		return exactOracle("authorized-query changed structural authority")
	}
	structMetrics, err := run.finishMeter(structMeter, &structAfter)
	if err != nil {
		return err
	}
	semanticMetrics, err := run.finishMeter(semanticMeter, &semanticAfter)
	if err != nil {
		return err
	}
	metrics, err := mergeMetrics(structMetrics, semanticMetrics)
	if err != nil {
		return err
	}
	metrics.ControlReads, err = checkedAddInt64(metrics.ControlReads, 8)
	if err != nil {
		return errors.New("T40.13 authorized-query control accounting overflowed")
	}
	queryMembers, err := checkedAddInt64(int64(structCount), int64(semanticCount))
	if err != nil {
		return errors.New("T40.13 authorized-query member accounting overflowed")
	}
	metrics.MemberReads, err = checkedAddInt64(metrics.MemberReads, queryMembers)
	if err != nil {
		return errors.New("T40.13 authorized-query member accounting overflowed")
	}
	run.semanticA = semanticAfter
	if planSchemaVersion(run.plan.Schema) >= 23 {
		run.structAR = structAfter
	}
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
		structural.ExtractionFacts != 0 || structural.ExtractionRows != 0 ||
		structural.PublishedDomains != frozenExtractorDomainCount ||
		semantic.ObservationRecords != 262_144 || semantic.ObservationUnsupported != 131_072 ||
		semantic.PublishedDomains != frozenExtractorDomainCount ||
		semantic.ExtractionFacts != frozenSemanticExtractionFacts ||
		semantic.ExtractionRows != frozenSemanticExtractionRows {
		return exactOracle("source-free semantic totals differ from the frozen oracle")
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

// validateCompletedObservationBeforeTeardown checks every completed-run field
// that is already known before destructive custody removal. The real teardown
// metrics are still recorded and revalidated afterward, but a schema/validator
// drift can no longer destroy the only state that explains the failure.
func (run *execution) validateCompletedObservationBeforeTeardown() error {
	if run == nil || len(run.observation.Phases) != len(phaseOrder) {
		return errors.New("T40.13 completed observation phase inventory is invalid")
	}
	value := run.observation
	value.Phases = slices.Clone(run.observation.Phases)
	value.Phases[len(value.Phases)-1] = succeededPhase("teardown", PhaseMetrics{WallMS: 1})
	value.Teardown = TeardownObservation{Completed: true}
	return run.validateReceiptBeforeTeardown(value)
}

func (run *execution) validateReceiptBeforeTeardown(value Observation) error {
	if run == nil {
		return errors.New("T40.13 execution is unavailable")
	}
	// Production executions always retain the exact frozen bytes. The shape
	// fallback preserves historical unit fixtures that construct an execution
	// directly without passing through newExecution.
	if len(run.planBytes) == 0 {
		return ValidateObservation(value)
	}
	observationBytes, err := MarshalObservation(value)
	if err != nil {
		return err
	}
	_, err = BuildReceipt(run.planBytes, observationBytes, PlanDigest(run.planBytes))
	return err
}

func (run *execution) teardown() error {
	if planSchemaVersion(run.plan.Schema) < 25 {
		return run.teardownLegacy()
	}
	started := time.Now()
	if err := run.stopServers(); err != nil {
		return err
	}
	logical, allocated, err := measureDataBytesForPlan(run.plan, run.workspace)
	if err != nil {
		return err
	}
	if err := run.completedCancellationError(); err != nil {
		return err
	}
	projected, projectionErr := run.projectedTeardownObservation(started, logical, allocated)
	if projectionErr != nil {
		return projectionErr
	}
	if err := run.validateReceiptBeforeTeardown(projected); err != nil {
		return fmt.Errorf("T40.13 completed observation is unsealable; custody retained: %w", err)
	}
	if err := run.persistTeardownCheckpoint(started, logical, allocated); err != nil {
		return fmt.Errorf("T40.13 completed observation checkpoint failed; custody retained: %w", err)
	}
	if err := run.completedCancellationError(); err != nil {
		return err
	}
	if err := run.beginCustodyFinalization(); err != nil {
		return fmt.Errorf(
			"T40.13 custody descendants are not durably drained; teardown checkpoint retained: %w", err,
		)
	}
	destroy := destroyCustody
	if run.custodyDestroy != nil {
		destroy = run.custodyDestroy
	}
	destroyErr := destroy(run.workspace, run.moduleRoot)
	if destroyErr != nil {
		return fmt.Errorf("T40.13 custody deletion is not durable; teardown checkpoint retained: %w", destroyErr)
	}
	if err := confirmCustodyDeletionDurable(run.workspace); err != nil {
		return fmt.Errorf("T40.13 custody deletion is not durable; teardown checkpoint retained: %w", err)
	}
	if retentionErr := custodyRetentionCause(run.ctx, nil); retentionErr != nil {
		return fmt.Errorf("T40.13 execution canceled across custody deletion; teardown checkpoint retained: %w", retentionErr)
	}
	return run.completeTeardown(started, logical, allocated, nil)
}

func (run *execution) beginCustodyFinalization() error {
	if run == nil || run.supervision == nil {
		return nil
	}
	if !digestIdentity(run.checkpointDigest) {
		return errors.New("T40.13 teardown checkpoint identity is unavailable")
	}
	if err := run.supervision.Drain(run.checkpointDigest); err != nil {
		return err
	}
	return run.supervision.BeginFinalization(run.checkpointDigest)
}

func (run *execution) teardownLegacy() error {
	started := time.Now()
	if err := run.stopServers(); err != nil {
		return err
	}
	logical, allocated, err := measureDataBytesForPlan(run.plan, run.workspace)
	if err != nil {
		return err
	}
	destroy := destroyCustody
	if run.custodyDestroy != nil {
		destroy = run.custodyDestroy
	}
	if err := destroy(run.workspace, run.moduleRoot); err != nil {
		return err
	}
	if _, err := os.Lstat(run.workspace); !errors.Is(err, os.ErrNotExist) {
		return errors.New("T40.13 teardown left custody behind")
	}
	run.observation.Teardown = TeardownObservation{Completed: true}
	run.observation.Phases[len(run.observation.Phases)-1] = succeededPhase("teardown", PhaseMetrics{
		WallMS: time.Since(started).Milliseconds(), DataLogicalBytes: logical, DataAllocatedBytes: allocated,
	})
	return run.enforceSafety()
}

func (run *execution) enforceSafety() error {
	if run.ctx != nil && errors.Is(context.Cause(run.ctx), errReviewCeiling) {
		return context.Cause(run.ctx)
	}
	var totalWall int64
	for _, phase := range run.observation.Phases {
		if phase.Outcome == "not_run" {
			continue
		}
		var err error
		totalWall, err = checkedAddInt64(totalWall, phase.Metrics.WallMS)
		if err != nil {
			return errors.Join(errReviewCeiling, err)
		}
		if phase.Metrics.PeakRSSBytes > run.plan.Safety.MaximumPeakRSSBytes ||
			phase.Metrics.DataAllocatedBytes > run.plan.Safety.MaximumDataAllocatedBytes {
			return errReviewCeiling
		}
	}
	if totalWall > run.plan.Safety.MaximumTotalWallMS {
		return errReviewCeiling
	}
	if prePressureAllocationCrossed(run.plan, run.observation.Phases) {
		return errReviewCeiling
	}
	return nil
}

func (run *execution) stopServers() error {
	result := errors.Join(run.serverShutdownErr, releaseLoopbackAddresses(run.portReservations))
	retry := run.liveServers[:0]
	var unproven error
	for _, server := range run.liveServers {
		stopErr := server.stop(30 * time.Second)
		if errors.Is(stopErr, errPrivateServerShutdownUnproven) {
			retry = append(retry, server)
			unproven = errors.Join(unproven, stopErr)
			continue
		}
		result = errors.Join(result, stopErr)
	}
	// A process exit or deadline selects its terminal result without waiting for
	// an already-started synchronous control read. Keep custody intact until that
	// bounded reader has observed cancellation and returned.
	run.inspectionWork.Wait()
	run.liveServers = retry
	run.structural = nil
	run.semantic = nil
	run.serverShutdownErr = result
	return errors.Join(result, unproven)
}

type convergenceProgressTracker struct {
	coalesceTransitionProgress        bool
	attempts                          int64
	progressChanges                   int64
	stageChanges                      int64
	first                             privateConvergenceProbe
	last                              privateConvergenceProbe
	lastProgressChange                time.Duration
	lastSuccessfulAt                  time.Duration
	observationProgress               *ObservationProgressObservation
	observationProgressAtWall         time.Duration
	extractionProgress                *ExtractionProgressObservation
	callerProgress                    *CallerProgressObservation
	callerProgressAtWall              time.Duration
	extractionProgressAtWall          time.Duration
	extractionTiming                  ExtractionTimingObservation
	inspectionTransitions             []ConvergenceTransitionObservation
	lastInspection                    convergenceInspectionDiagnostic
	lastInspectionProbe               privateConvergenceProbe
	lastInspectionAt                  time.Duration
	relationshipTerminalConfirmations int64
}

type convergenceInspectionDiagnostic struct {
	class      string
	httpStatus int
	httpReason string
}

func (tracker *convergenceProgressTracker) observe(
	probe privateConvergenceProbe,
	diagnostic convergenceInspectionDiagnostic,
	elapsed time.Duration,
) bool {
	if elapsed < time.Millisecond {
		elapsed = time.Millisecond
	}
	tracker.attempts++
	if diagnostic.class == "terminal" && probe.Stage == "relationship_publication" &&
		probe.RelationshipFailureClass != "" {
		if tracker.lastInspection.class == diagnostic.class &&
			tracker.lastInspectionProbe.SHA256 == probe.SHA256 &&
			tracker.lastInspectionProbe.RelationshipFailureClass == probe.RelationshipFailureClass {
			tracker.relationshipTerminalConfirmations++
		} else {
			tracker.relationshipTerminalConfirmations = 1
		}
	} else {
		tracker.relationshipTerminalConfirmations = 0
	}
	tracker.lastInspection = diagnostic
	tracker.lastInspectionProbe = probe
	tracker.lastInspectionAt = elapsed
	transitionLimitExceeded := tracker.observeTransition(probe, diagnostic, elapsed)
	// Terminal probes are not successful convergence progress, but their typed
	// projections are the evidence needed to classify and validate the stop.
	// Retain them before excluding terminal/error classes from the monotonic
	// successful-progress counters below.
	if probe.ObservationProgress != nil {
		progress := *probe.ObservationProgress
		tracker.observationProgress = &progress
		tracker.observationProgressAtWall = elapsed
	}
	if probe.ExtractionProgress != nil {
		progress := *probe.ExtractionProgress
		tracker.extractionProgress = &progress
		tracker.extractionProgressAtWall = elapsed
	}
	if probe.CallerProgress != nil {
		progress := *probe.CallerProgress
		tracker.callerProgress = &progress
		tracker.callerProgressAtWall = elapsed
	}
	if diagnostic.class != "pending" && diagnostic.class != "complete" {
		return transitionLimitExceeded
	}
	if tracker.first.SHA256 == "" {
		tracker.first = probe
	} else if tracker.last.SHA256 != probe.SHA256 {
		tracker.progressChanges++
		if tracker.last.Stage != probe.Stage {
			tracker.stageChanges++
		}
		tracker.lastProgressChange = elapsed
	}
	tracker.last = probe
	tracker.lastSuccessfulAt = elapsed
	return transitionLimitExceeded
}

func (tracker *convergenceProgressTracker) observeTransition(
	probe privateConvergenceProbe,
	diagnostic convergenceInspectionDiagnostic,
	elapsed time.Duration,
) bool {
	transition := ConvergenceTransitionObservation{
		WallMS: elapsed.Milliseconds(), Stage: probe.Stage, Class: diagnostic.class,
		HTTPStatus: diagnostic.httpStatus, HTTPReason: diagnostic.httpReason,
		RelationshipFailureClass: probe.RelationshipFailureClass,
		ProgressSHA256:           probe.SHA256,
	}
	if tracker.coalesceTransitionProgress {
		transition.FirstProgressSHA256 = probe.SHA256
	}
	if count := len(tracker.inspectionTransitions); count > 0 {
		last := &tracker.inspectionTransitions[count-1]
		if last.Stage == transition.Stage && last.Class == transition.Class &&
			last.HTTPStatus == transition.HTTPStatus && last.HTTPReason == transition.HTTPReason &&
			last.RelationshipFailureClass == transition.RelationshipFailureClass {
			if last.ProgressSHA256 == transition.ProgressSHA256 {
				return false
			}
			if tracker.coalesceTransitionProgress {
				last.ProgressSHA256 = transition.ProgressSHA256
				last.ProgressChanges++
				last.LastProgressChangeWallMS = transition.WallMS
				return false
			}
		}
	}
	if len(tracker.inspectionTransitions) < maxConvergenceTransitions {
		tracker.inspectionTransitions = append(tracker.inspectionTransitions, transition)
		return false
	}
	return true
}

func observeCompletedConvergenceAttempt(
	phase context.Context,
	tracker *convergenceProgressTracker,
	probe privateConvergenceProbe,
	diagnostic convergenceInspectionDiagnostic,
	elapsed time.Duration,
) (bool, bool) {
	if phase == nil || tracker == nil || phase.Err() != nil {
		return false, false
	}
	return true, tracker.observe(probe, diagnostic, elapsed)
}

func classifyConvergenceInspection(err error) convergenceInspectionDiagnostic {
	if err == nil {
		return convergenceInspectionDiagnostic{class: "complete"}
	}
	if errors.Is(err, errRepositoryIndexTerminal) || errors.Is(err, errObservationBoundRefusal) ||
		errors.Is(err, errObservationTerminal) {
		return convergenceInspectionDiagnostic{class: "terminal"}
	}
	if errors.Is(err, errExtractionBoundRefusal) || errors.Is(err, errExtractionJobTerminal) ||
		errors.Is(err, errExtractionScheduleTerminal) ||
		errors.Is(err, errCallerGenerationBoundRefusal) ||
		errors.Is(err, errCallerGenerationTerminal) ||
		errors.Is(err, errRelationshipBoundRefusal) ||
		errors.Is(err, errRelationshipTerminal) {
		return convergenceInspectionDiagnostic{class: "terminal"}
	}
	var statusErr *privateHTTPStatusError
	if errors.As(err, &statusErr) {
		return convergenceInspectionDiagnostic{
			class: "status", httpStatus: statusErr.Status, httpReason: statusErr.Reason,
		}
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "HTTP request failed"):
		return convergenceInspectionDiagnostic{class: "transport"}
	case strings.Contains(message, "HTTP response"):
		return convergenceInspectionDiagnostic{class: "response"}
	case errors.Is(err, os.ErrNotExist), errors.Is(err, relationshippublication.ErrNotFound),
		strings.Contains(message, "not visible"),
		strings.Contains(message, "has not converged"), strings.Contains(message, "is not exact"):
		return convergenceInspectionDiagnostic{class: "pending"}
	default:
		return convergenceInspectionDiagnostic{class: "control"}
	}
}

func retainServerExit(server *privateServer) (error, bool) {
	if server == nil || server.done == nil {
		return nil, false
	}
	select {
	case exitErr := <-server.done:
		server.done <- exitErr
		return exitErr, true
	default:
		return nil, false
	}
}

type convergenceInspection func(
	context.Context,
) (privateProfileSnapshot, privateConvergenceProbe, error)

type convergenceInspectionResult struct {
	snapshot privateProfileSnapshot
	probe    privateConvergenceProbe
	err      error
}

func (run *execution) inspectConvergenceAttempt(
	ctx context.Context,
	server *privateServer,
	inspect convergenceInspection,
) (privateProfileSnapshot, privateConvergenceProbe, error, error, bool) {
	if run == nil || ctx == nil || server == nil || server.done == nil || inspect == nil {
		return privateProfileSnapshot{}, privateConvergenceProbe{},
			errors.New("T40.13 convergence inspection is invalid"), nil, false
	}
	if exitErr, exited := retainServerExit(server); exited {
		return privateProfileSnapshot{}, privateConvergenceProbe{}, nil, exitErr, true
	}
	attempt, cancel := context.WithCancel(ctx)
	result := make(chan convergenceInspectionResult, 1)
	run.inspectionWork.Add(1)
	go func() {
		defer run.inspectionWork.Done()
		snapshot, probe, inspectErr := inspect(attempt)
		result <- convergenceInspectionResult{snapshot: snapshot, probe: probe, err: inspectErr}
	}()
	select {
	case inspection := <-result:
		cancel()
		if exitErr, exited := retainServerExit(server); exited {
			return privateProfileSnapshot{}, privateConvergenceProbe{}, nil, exitErr, true
		}
		return inspection.snapshot, inspection.probe, inspection.err, nil, false
	case exitErr := <-server.done:
		server.done <- exitErr
		cancel()
		return privateProfileSnapshot{}, privateConvergenceProbe{}, nil, exitErr, true
	case <-ctx.Done():
		cancel()
		if exitErr, exited := retainServerExit(server); exited {
			return privateProfileSnapshot{}, privateConvergenceProbe{}, nil, exitErr, true
		}
		return privateProfileSnapshot{}, privateConvergenceProbe{}, ctx.Err(), nil, false
	}
}

func (run *execution) waitSnapshot(
	profile PreparedProfile,
	revision, label string,
	limit time.Duration,
	server *privateServer,
) (privateProfileSnapshot, error) {
	if run == nil || run.ctx == nil || limit <= 0 || server == nil || server.done == nil {
		return privateProfileSnapshot{}, errors.New("T40.13 convergence wait is invalid")
	}
	contract := profileInspectionForPlan(run.plan.Schema)
	inspector, err := newProfileInspector(profile, contract)
	if err != nil {
		return privateProfileSnapshot{}, err
	}
	return run.waitSnapshotWithInspection(
		profile, revision, label, limit, server,
		func(attempt context.Context) (privateProfileSnapshot, privateConvergenceProbe, error) {
			return inspector.inspectWithProgress(attempt, profile, revision)
		},
	)
}

func (run *execution) waitSnapshotWithInspection(
	profile PreparedProfile,
	revision, label string,
	limit time.Duration,
	server *privateServer,
	inspect convergenceInspection,
) (privateProfileSnapshot, error) {
	if run == nil || run.ctx == nil || limit <= 0 || server == nil || server.done == nil || inspect == nil {
		return privateProfileSnapshot{}, errors.New("T40.13 convergence wait is invalid")
	}
	var err error
	var timingCursor *partitionTimingCursor
	var lifecycleTimingCursor *chunkLifecycleCursor
	if planSchemaVersion(run.plan.Schema) >= 13 {
		timingCursor, err = newPartitionTimingCursor(server.logPath)
		if err != nil {
			return privateProfileSnapshot{}, err
		}
		defer func() { _ = timingCursor.Close() }()
		lifecycleTimingCursor, err = newChunkLifecycleCursor(
			server.logPath, 0, lifecycleValidationForPlan(run.plan.Schema),
		)
		if err != nil {
			return privateProfileSnapshot{}, err
		}
		defer func() { _ = lifecycleTimingCursor.Close() }()
	}
	phase, cancel := phaseContext(run.ctx, limit)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	started := time.Now()
	progress := convergenceProgressTracker{
		coalesceTransitionProgress: planSchemaVersion(run.plan.Schema) >= 13,
	}
	for {
		snapshot, probe, inspectErr, exitErr, exited := run.inspectConvergenceAttempt(
			phase, server, inspect,
		)
		elapsed := time.Since(started)
		if timingCursor != nil {
			reports, timingErr := timingCursor.poll()
			if timingErr != nil {
				return privateProfileSnapshot{}, timingErr
			}
			for _, report := range reports {
				if err := addPartitionTiming(&progress.extractionTiming, report); err != nil {
					return privateProfileSnapshot{}, err
				}
			}
		}
		if lifecycleTimingCursor != nil {
			reports, lifecycleErr := lifecycleTimingCursor.poll()
			if lifecycleErr != nil {
				return privateProfileSnapshot{}, lifecycleErr
			}
			for _, report := range reports {
				if err := addSchedulerTiming(&progress.extractionTiming, report); err != nil {
					return privateProfileSnapshot{}, err
				}
			}
		}
		if exited {
			run.recordConvergenceWait(profile, revision, label, "server_exited", limit, started, progress)
			return privateProfileSnapshot{}, errors.Join(exitErr, errConvergenceServerExit)
		}
		priorProbeSHA := progress.lastInspectionProbe.SHA256
		completed, transitionLimitExceeded := observeCompletedConvergenceAttempt(
			phase, &progress, probe, classifyConvergenceInspection(inspectErr), elapsed,
		)
		if !completed {
			if exitErr, exited := retainServerExit(server); exited {
				run.recordConvergenceWait(profile, revision, label, "server_exited", limit, started, progress)
				return privateProfileSnapshot{}, errors.Join(exitErr, errConvergenceServerExit)
			}
			if run.ctx.Err() == nil && errors.Is(phase.Err(), context.DeadlineExceeded) {
				run.recordConvergenceWait(profile, revision, label, "deadline", limit, started, progress)
				return privateProfileSnapshot{}, errConvergenceDeadline
			}
			run.recordConvergenceWait(profile, revision, label, "canceled", limit, started, progress)
			return privateProfileSnapshot{}, errors.Join(phase.Err(), errors.New("T40.13 convergence wait canceled"))
		}
		if transitionLimitExceeded {
			run.recordConvergenceWait(profile, revision, label, "diagnostic_limit", limit, started, progress)
			return privateProfileSnapshot{}, errConvergenceTimeline
		}
		if errors.Is(inspectErr, errRepositoryIndexTerminal) {
			run.recordConvergenceWait(profile, revision, label, "repository_index_terminal", limit, started, progress)
			return privateProfileSnapshot{}, errRepositoryIndexTerminal
		}
		if errors.Is(inspectErr, errObservationBoundRefusal) {
			run.recordConvergenceWait(profile, revision, label, "observation_bound_refusal", limit, started, progress)
			return privateProfileSnapshot{}, errObservationBoundRefusal
		}
		if errors.Is(inspectErr, errObservationTerminal) {
			run.recordConvergenceWait(profile, revision, label, "observation_terminal", limit, started, progress)
			return privateProfileSnapshot{}, errObservationTerminal
		}
		if errors.Is(inspectErr, errExtractionBoundRefusal) || errors.Is(inspectErr, errExtractionJobTerminal) {
			// V15 job-plane stops confirm on a second identical probe. The
			// job row is repository-keyed, so a single poll's shape can race
			// the schedule enqueuer or the final promotion write; a
			// converging pipeline changes the probe digest within one tick,
			// a dead one repeats it five seconds later.
			if planSchemaVersion(run.plan.Schema) < 15 || probe.SHA256 == priorProbeSHA {
				if errors.Is(inspectErr, errExtractionBoundRefusal) {
					run.recordConvergenceWait(profile, revision, label, "extraction_bound_refusal", limit, started, progress)
					return privateProfileSnapshot{}, errExtractionBoundRefusal
				}
				run.recordConvergenceWait(profile, revision, label, "extraction_job_terminal", limit, started, progress)
				return privateProfileSnapshot{}, errExtractionJobTerminal
			}
		}
		if errors.Is(inspectErr, errExtractionScheduleTerminal) {
			run.recordConvergenceWait(profile, revision, label, "extraction_schedule_terminal", limit, started, progress)
			return privateProfileSnapshot{}, errExtractionScheduleTerminal
		}
		if errors.Is(inspectErr, errCallerGenerationBoundRefusal) {
			run.recordConvergenceWait(profile, revision, label, "caller_generation_bound_refusal", limit, started, progress)
			return privateProfileSnapshot{}, errCallerGenerationBoundRefusal
		}
		if errors.Is(inspectErr, errCallerGenerationTerminal) {
			// V20 confirms caller terminal projections on a second identical
			// source-free probe. In particular, all pairs can settle immediately
			// before the publication transaction commits; a live publisher changes
			// the probe on the next tick, while a rejected transaction repeats it.
			if !errors.Is(inspectErr, errCallerPublicationMissing) ||
				planSchemaVersion(run.plan.Schema) < 20 || probe.SHA256 == priorProbeSHA {
				run.recordConvergenceWait(profile, revision, label, "caller_generation_terminal", limit, started, progress)
				return privateProfileSnapshot{}, errCallerGenerationTerminal
			}
		}
		if errors.Is(inspectErr, errRelationshipBoundRefusal) {
			run.recordConvergenceWait(profile, revision, label, "relationship_bound_refusal", limit, started, progress)
			return privateProfileSnapshot{}, errRelationshipBoundRefusal
		}
		if errors.Is(inspectErr, errRelationshipTerminal) {
			// V16 confirms relationship integrity stops on a second identical
			// source-free probe. A schedule enqueuer, current-pointer swap, or
			// final settlement changes the probe; a stranded pair repeats it.
			if planSchemaVersion(run.plan.Schema) < 16 || probe.SHA256 == priorProbeSHA {
				run.recordConvergenceWait(profile, revision, label, "relationship_terminal", limit, started, progress)
				return privateProfileSnapshot{}, errRelationshipTerminal
			}
		}
		if inspectErr == nil {
			run.recordConvergenceWait(profile, revision, label, "converged", limit, started, progress)
			return snapshot, nil
		}
		select {
		case exitErr := <-server.done:
			server.done <- exitErr
			run.recordConvergenceWait(profile, revision, label, "server_exited", limit, started, progress)
			return privateProfileSnapshot{}, errors.Join(exitErr, errConvergenceServerExit)
		case <-phase.Done():
			if run.ctx.Err() == nil && errors.Is(phase.Err(), context.DeadlineExceeded) {
				run.recordConvergenceWait(profile, revision, label, "deadline", limit, started, progress)
				return privateProfileSnapshot{}, errConvergenceDeadline
			}
			run.recordConvergenceWait(profile, revision, label, "canceled", limit, started, progress)
			return privateProfileSnapshot{}, errors.Join(phase.Err(), errors.New("T40.13 convergence wait canceled"))
		case <-ticker.C:
		}
	}
}

func (run *execution) recordConvergenceWait(
	profile PreparedProfile,
	revision, label, outcome string,
	limit time.Duration,
	started time.Time,
	progress convergenceProgressTracker,
) {
	if (planSchemaVersion(run.plan.Schema) < 4) ||
		(progress.attempts == 0 && planSchemaVersion(run.plan.Schema) < 6) ||
		(progress.attempts == 0 && outcome != "server_exited") {
		return
	}
	if progress.attempts == 0 {
		run.observation.ConvergenceWaits = append(run.observation.ConvergenceWaits, ConvergenceWaitObservation{
			Profile: profile.Name, Label: label, Revision: revision, Outcome: outcome,
			FirstStage: convergenceNotInspected, LastStage: convergenceNotInspected,
			DeadlineMS: limit.Milliseconds(), WallMS: max(time.Since(started).Milliseconds(), 1),
		})
		return
	}
	wait := ConvergenceWaitObservation{
		Profile: profile.Name, Label: label, Revision: revision, Outcome: outcome,
		LastStage: progress.last.Stage, Attempts: progress.attempts,
		ProgressChanges:     progress.progressChanges,
		FirstProgressSHA256: progress.first.SHA256, LastProgressSHA256: progress.last.SHA256,
		DeadlineMS: limit.Milliseconds(), WallMS: time.Since(started).Milliseconds(),
	}
	if planSchemaVersion(run.plan.Schema) >= 5 {
		wait.FirstStage = progress.first.Stage
		wait.StageChanges = progress.stageChanges
		wait.LastProgressChangeWallMS = progress.lastProgressChange.Milliseconds()
		wait.ObservationProgress = progress.observationProgress
		wait.ObservationProgressWallMS = progress.observationProgressAtWall.Milliseconds()
		if planSchemaVersion(run.plan.Schema) >= 12 {
			wait.ExtractionProgress = progress.extractionProgress
			wait.ExtractionProgressWallMS = progress.extractionProgressAtWall.Milliseconds()
		}
		if planSchemaVersion(run.plan.Schema) >= 21 {
			wait.CallerProgress = progress.callerProgress
			wait.CallerProgressWallMS = progress.callerProgressAtWall.Milliseconds()
		}
		if (planSchemaVersion(run.plan.Schema) >= 13) && progress.extractionTiming.Attempts > 0 {
			timing := progress.extractionTiming
			wait.ExtractionTiming = &timing
		}
	}
	if run.plan.Schema == PlanSchemaV6 {
		wait.WallMS = max(wait.WallMS, 1)
		wait.InspectionTransitions = slices.Clone(progress.inspectionTransitions)
		for index := range wait.InspectionTransitions {
			wait.InspectionTransitions[index].HTTPStatus = 0
			wait.InspectionTransitions[index].HTTPReason = ""
		}
		if progress.last.SHA256 == "" {
			wait.FirstStage = convergenceNotInspected
			wait.LastStage = convergenceNotInspected
		} else {
			wait.LastSuccessfulProbeSHA256 = progress.last.SHA256
			wait.LastSuccessfulProbeWallMS = progress.lastSuccessfulAt.Milliseconds()
		}
		wait.TransitionLimitExceeded = outcome == "diagnostic_limit"
	}
	if planSchemaVersion(run.plan.Schema) >= 7 {
		wait.WallMS = max(wait.WallMS, 1)
		wait.InspectionTransitions = slices.Clone(progress.inspectionTransitions)
		if progress.last.SHA256 == "" {
			wait.FirstStage = convergenceNotInspected
			wait.LastStage = convergenceNotInspected
		} else {
			wait.LastSuccessfulProbeSHA256 = progress.last.SHA256
			wait.LastSuccessfulProbeWallMS = progress.lastSuccessfulAt.Milliseconds()
		}
		if progress.attempts > 0 {
			wait.LastInspectionStage = progress.lastInspectionProbe.Stage
			wait.LastInspectionClass = progress.lastInspection.class
			wait.LastInspectionHTTPStatus = progress.lastInspection.httpStatus
			wait.LastInspectionHTTPReason = progress.lastInspection.httpReason
			wait.LastInspectionSHA256 = progress.lastInspectionProbe.SHA256
			wait.LastInspectionWallMS = progress.lastInspectionAt.Milliseconds()
		}
		if (planSchemaVersion(run.plan.Schema) >= 10) && outcome == "repository_index_terminal" {
			wait.RepositoryIndexFailureClass = progress.lastInspectionProbe.RepositoryIndexFailureClass
		}
		if planSchemaVersion(run.plan.Schema) >= 16 {
			wait.RelationshipFailureClass = progress.lastInspectionProbe.RelationshipFailureClass
			if outcome == "relationship_terminal" {
				wait.RelationshipTerminalConfirmations = progress.relationshipTerminalConfirmations
			}
		}
		wait.TransitionLimitExceeded = outcome == "diagnostic_limit"
	}
	run.observation.ConvergenceWaits = append(run.observation.ConvergenceWaits, wait)
}

func (run *execution) fullConvergenceDeadline() time.Duration {
	if planSchemaVersion(run.plan.Schema) >= 4 {
		return time.Duration(run.plan.Safety.FullConvergenceDeadlineMS) * time.Millisecond
	}
	return 2 * time.Hour
}

func (run *execution) revalidationDeadline() time.Duration {
	if planSchemaVersion(run.plan.Schema) >= 4 {
		return time.Duration(run.plan.Safety.RevalidationDeadlineMS) * time.Millisecond
	}
	return 20 * time.Minute
}

func waitForDerivedPartial(ctx context.Context, plan Plan, dataDir string, limit time.Duration) error {
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		found, err := derivedPartialPresentForPlan(plan, dataDir)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
		select {
		case <-phase.Done():
			return errors.New("T40.13 derived interruption point was not observed")
		case <-ticker.C:
		}
	}
}

func waitForDerivedPartialClear(ctx context.Context, plan Plan, dataDir string, limit time.Duration) error {
	phase, cancel := phaseContext(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		partial, err := derivedPartialPresentForPlan(plan, dataDir)
		if planSchemaVersion(plan.Schema) < 25 && err == nil && !partial {
			partial, err = relationshipPartialPresentLegacy(dataDir)
		}
		if err != nil {
			return err
		}
		if !partial {
			return nil
		}
		select {
		case <-phase.Done():
			return errors.New("T40.13 interruption restart retained partial derived publication state")
		case <-ticker.C:
		}
	}
}

func recoveryAuthorityForPlan(plan Plan, snapshot privateProfileSnapshot) string {
	if planSchemaVersion(plan.Schema) >= 19 {
		return snapshotRecoveryAuthority(snapshot)
	}
	return snapshotAuthority(snapshot)
}

// stablePhaseAuthorityForPlan preserves the exact historical phase oracles.
// V23 compares semantic authority so a relationship transition re-minted by a
// cascade between phases can be adopted without weakening source, extraction,
// caller, or relationship-content equality.
func stablePhaseAuthorityForPlan(plan Plan, snapshot privateProfileSnapshot) string {
	if planSchemaVersion(plan.Schema) >= 23 {
		return recoveryAuthorityForPlan(plan, snapshot)
	}
	return snapshotAuthority(snapshot)
}

// derivedPartialPresentV18 preserves the historical shallow scanner used by
// retained V17/V18 execution contracts. V19 owns the corrected fixed candidate
// namespace and hashed relationship-publication traversal.
func derivedPartialPresentV18(dataDir string) (bool, error) {
	if !filepath.IsAbs(dataDir) {
		return false, errors.New("T40.13 derived interruption scope is invalid")
	}
	for _, name := range []string{"observations", "extraction-publications", "relationships"} {
		root := filepath.Join(dataDir, name)
		rootInfo, err := os.Lstat(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
			return false, errors.Join(err, errors.New("T40.13 derived interruption root is invalid"))
		}
		repositories, err := os.ReadDir(root)
		if err != nil {
			return false, err
		}
		if len(repositories) > 1 {
			return false, errors.New("T40.13 derived interruption repository inventory exceeds its bound")
		}
		for _, repository := range repositories {
			if !repository.IsDir() || repository.Type()&os.ModeSymlink != 0 {
				return false, errors.New("T40.13 derived interruption repository is invalid")
			}
			repositoryPath := filepath.Join(root, repository.Name())
			if found, err := derivedControlPresentLegacy(repositoryPath); err != nil || found {
				return found, err
			}
			if name == "observations" {
				v2 := filepath.Join(repositoryPath, observationpublication.InventoryPublicationDirectoryV2)
				info, statErr := os.Lstat(v2)
				switch {
				case os.IsNotExist(statErr):
					continue
				case statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
					return false, errors.Join(statErr, errors.New("T40.13 derived interruption v2 control is invalid"))
				}
				if found, err := derivedControlPresentLegacy(v2); err != nil || found {
					return found, err
				}
			}
		}
	}
	return false, nil
}

func derivedPartialPresent(dataDir string) (bool, error) {
	if !filepath.IsAbs(dataDir) {
		return false, errors.New("T40.13 derived interruption scope is invalid")
	}
	for _, name := range []string{"observations", "extraction-publications"} {
		root := filepath.Join(dataDir, name)
		rootInfo, err := os.Lstat(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
			return false, errors.Join(err, errors.New("T40.13 derived interruption root is invalid"))
		}
		repositoryLimit := 1
		if name == "extraction-publications" {
			repositoryLimit = 2
		}
		repositories, err := readDirectoryBounded(root, repositoryLimit)
		if err != nil {
			return false, err
		}
		if name == "extraction-publications" {
			repositories = slices.DeleteFunc(repositories, func(entry os.DirEntry) bool {
				return entry.Name() == "candidates"
			})
		}
		if len(repositories) > 1 {
			return false, errors.New("T40.13 derived interruption repository inventory exceeds its bound")
		}
		for _, repository := range repositories {
			if !repository.IsDir() || repository.Type()&os.ModeSymlink != 0 {
				return false, errors.New("T40.13 derived interruption repository is invalid")
			}
			repositoryPath := filepath.Join(root, repository.Name())
			if found, err := derivedControlPresent(repositoryPath); err != nil || found {
				return found, err
			}
			if name == "observations" {
				v2 := filepath.Join(repositoryPath, observationpublication.InventoryPublicationDirectoryV2)
				info, statErr := os.Lstat(v2)
				switch {
				case os.IsNotExist(statErr):
					continue
				case statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
					return false, errors.Join(statErr, errors.New("T40.13 derived interruption v2 control is invalid"))
				}
				if found, err := derivedControlPresent(v2); err != nil || found {
					return found, err
				}
			}
		}
	}
	return relationshipPartialPresent(dataDir)
}

func derivedPartialPresentForPlan(plan Plan, dataDir string) (bool, error) {
	if planSchemaVersion(plan.Schema) >= 25 {
		return derivedPartialPresent(dataDir)
	}
	return derivedPartialPresentLegacy(dataDir)
}

func derivedPartialPresentLegacy(dataDir string) (bool, error) {
	if !filepath.IsAbs(dataDir) {
		return false, errors.New("T40.13 derived interruption scope is invalid")
	}
	for _, name := range []string{"observations", "extraction-publications"} {
		root := filepath.Join(dataDir, name)
		rootInfo, err := os.Lstat(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
			return false, errors.Join(err, errors.New("T40.13 derived interruption root is invalid"))
		}
		repositories, err := os.ReadDir(root)
		if err != nil {
			return false, err
		}
		if name == "extraction-publications" {
			repositories = slices.DeleteFunc(repositories, func(entry os.DirEntry) bool {
				return entry.Name() == "candidates"
			})
		}
		if len(repositories) > 1 {
			return false, errors.New("T40.13 derived interruption repository inventory exceeds its bound")
		}
		for _, repository := range repositories {
			if !repository.IsDir() || repository.Type()&os.ModeSymlink != 0 {
				return false, errors.New("T40.13 derived interruption repository is invalid")
			}
			repositoryPath := filepath.Join(root, repository.Name())
			if found, err := derivedControlPresentLegacy(repositoryPath); err != nil || found {
				return found, err
			}
			if name == "observations" {
				v2 := filepath.Join(repositoryPath, observationpublication.InventoryPublicationDirectoryV2)
				info, statErr := os.Lstat(v2)
				switch {
				case os.IsNotExist(statErr):
					continue
				case statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
					return false, errors.Join(statErr, errors.New("T40.13 derived interruption v2 control is invalid"))
				}
				if found, err := derivedControlPresentLegacy(v2); err != nil || found {
					return found, err
				}
			}
		}
	}
	return relationshipPartialPresentLegacy(dataDir)
}

func derivedControlPresentLegacy(directory string) (bool, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, err
	}
	if len(entries) > 4096 {
		return false, errors.New("T40.13 derived interruption control inventory exceeds its bound")
	}
	for _, entry := range entries {
		if entry.Name() == "publishing.json" || strings.HasPrefix(entry.Name(), ".stage-") {
			return true, nil
		}
	}
	return false, nil
}

func derivedControlPresent(directory string) (bool, error) {
	entries, err := readDirectoryBounded(directory, 4096)
	if err != nil {
		return false, err
	}
	if len(entries) > 4096 {
		return false, errors.New("T40.13 derived interruption control inventory exceeds its bound")
	}
	for _, entry := range entries {
		if entry.Name() == "publishing.json" || strings.HasPrefix(entry.Name(), ".stage-") {
			return true, nil
		}
	}
	return false, nil
}

func readDirectoryBounded(path string, limit int) ([]os.DirEntry, error) {
	if limit < 0 {
		return nil, errors.New("T40.13 directory entry bound is invalid")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(err, errors.New("T40.13 bounded directory is invalid"))
	}
	directory, err := openNoFollowDirectory(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := directory.Stat()
	if statErr != nil || !opened.IsDir() || !sameFileSnapshot(before, opened) {
		return nil, errors.Join(
			statErr, errors.New("T40.13 bounded directory changed during open"), directory.Close(),
		)
	}
	entries, readErr := directory.ReadDir(limit + 1)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	afterOpen, afterStatErr := directory.Stat()
	afterPath, lstatErr := os.Lstat(path)
	closeErr := directory.Close()
	if readErr != nil || afterStatErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, afterStatErr, closeErr)
	}
	if lstatErr != nil {
		return nil, fmt.Errorf("T40.13 bounded directory changed during inspection: %v", lstatErr)
	}
	if !afterPath.IsDir() || afterPath.Mode()&os.ModeSymlink != 0 ||
		!sameFileSnapshot(opened, afterOpen) || !sameFileSnapshot(opened, afterPath) {
		return nil, errors.New("T40.13 bounded directory changed during inspection")
	}
	if len(entries) > limit {
		return nil, errors.New("T40.13 directory exceeds its entry bound")
	}
	return entries, nil
}

type privateRecoveryBackup struct {
	path    string
	logPath string
}

func createLiveBackup(
	ctx context.Context,
	toolchain privateToolchain,
	profile PreparedProfile,
	workspace string,
	label string,
) (privateRecoveryBackup, PhaseMetrics, error) {
	base := filepath.Dir(profile.Config)
	backup := filepath.Join(base, "backup-"+label)
	logPath := filepath.Join(base, "recovery-"+label+".log")
	if ctx == nil || !filepath.IsAbs(workspace) || !isWithin(backup, workspace) ||
		!isWithin(logPath, workspace) || label == "" || strings.ContainsAny(label, "/\\: ") {
		return privateRecoveryBackup{}, PhaseMetrics{}, errors.New("T40.13 live backup scope is invalid")
	}
	if err := revalidatePrivateToolchain(ctx, toolchain); err != nil {
		return privateRecoveryBackup{}, PhaseMetrics{}, err
	}
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return privateRecoveryBackup{}, PhaseMetrics{}, err
	}
	command := exec.CommandContext(ctx, toolchain.Phebs, "backup", "-config", profile.Config, "-output", backup)
	command.Stdout, command.Stderr = logFile, logFile
	if err := validatePrivateTemporaryDirectory(toolchain); err != nil {
		_ = logFile.Close()
		return privateRecoveryBackup{}, PhaseMetrics{}, err
	}
	command.Env = executionEnvironmentForToolchain(toolchain)
	metrics, commandErr := runMeasuredCommand(command, workspace, toolchain.ClosedEnvironment)
	closeErr := logFile.Close()
	if commandErr != nil {
		commandErr = sanitizeMeasuredCommandFailure(
			"T40.13 live backup command failed", commandErr, toolchain.dataMeasurementV27,
		)
	}
	if commandErr != nil || closeErr != nil {
		return privateRecoveryBackup{}, metrics, errors.Join(commandErr, closeErr)
	}
	return privateRecoveryBackup{path: backup, logPath: logPath}, metrics, nil
}

func restoreBackup(
	ctx context.Context,
	toolchain privateToolchain,
	profile PreparedProfile,
	workspace string,
	backup privateRecoveryBackup,
	label string,
) (PhaseMetrics, error) {
	base := filepath.Dir(profile.Config)
	prior := profile.DataDir + ".prior-" + label
	if ctx == nil || !filepath.IsAbs(workspace) || !isWithin(prior, workspace) ||
		!isWithin(backup.path, workspace) || !isWithin(backup.logPath, workspace) ||
		filepath.Dir(backup.path) != base || filepath.Dir(backup.logPath) != base {
		return PhaseMetrics{}, errors.New("T40.13 restore scope is invalid")
	}
	if err := revalidatePrivateToolchain(ctx, toolchain); err != nil {
		return PhaseMetrics{}, err
	}
	backupInfo, backupErr := os.Lstat(backup.path)
	logInfo, logErr := os.Lstat(backup.logPath)
	if backupErr != nil || logErr != nil || !backupInfo.IsDir() || backupInfo.Mode()&os.ModeSymlink != 0 ||
		!logInfo.Mode().IsRegular() || logInfo.Mode()&os.ModeSymlink != 0 {
		return PhaseMetrics{}, errors.Join(backupErr, logErr, errors.New("T40.13 restore controls are invalid"))
	}
	if _, err := os.Lstat(prior); err == nil || !os.IsNotExist(err) {
		return PhaseMetrics{}, errors.New("T40.13 recovery prior path already exists")
	}
	if err := os.Rename(profile.DataDir, prior); err != nil {
		return PhaseMetrics{}, err
	}
	logFile, err := os.OpenFile(backup.logPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return PhaseMetrics{}, err
	}
	command := exec.CommandContext(ctx, toolchain.Phebs, "restore", "-config", profile.Config, "-backup", backup.path)
	command.Stdout, command.Stderr = logFile, logFile
	if err := validatePrivateTemporaryDirectory(toolchain); err != nil {
		_ = logFile.Close()
		return PhaseMetrics{}, err
	}
	command.Env = executionEnvironmentForToolchain(toolchain)
	metrics, commandErr := runMeasuredCommand(command, workspace, toolchain.ClosedEnvironment)
	closeErr := logFile.Close()
	if commandErr != nil {
		commandErr = sanitizeMeasuredCommandFailure(
			"T40.13 restore command failed", commandErr, toolchain.dataMeasurementV27,
		)
	}
	if commandErr != nil || closeErr != nil {
		return metrics, errors.Join(commandErr, closeErr)
	}
	if !isWithin(prior, base) || filepath.Clean(prior) == filepath.Clean(base) {
		return metrics, errors.New("T40.13 recovery prior path escaped custody")
	}
	if err := os.RemoveAll(prior); err != nil {
		return metrics, err
	}
	return metrics, nil
}

func waitLifecycle(ctx context.Context, profile PreparedProfile, requireCycle bool, limit time.Duration) (lifecycle.Status, error) {
	return waitLifecycleWithContract(ctx, profile, requireCycle, "", limit, lifecycleWaitLegacy)
}

type lifecycleWaitContract uint8

const (
	lifecycleWaitLegacy lifecycleWaitContract = iota
	lifecycleWaitV23
	lifecycleWaitV23PressureRecovery
)

type pressureWaitPurpose uint8

const (
	pressureWaitEnter pressureWaitPurpose = iota + 1
	pressureWaitRecover
)

func lifecycleWaitContractForPlan(plan Plan) lifecycleWaitContract {
	if planSchemaVersion(plan.Schema) >= 23 {
		return lifecycleWaitV23
	}
	return lifecycleWaitLegacy
}

func waitLifecycleForPlan(
	ctx context.Context,
	plan Plan,
	profile PreparedProfile,
	requireCycle bool,
	limit time.Duration,
) (lifecycle.Status, error) {
	return waitLifecycleWithContract(
		ctx, profile, requireCycle, "", limit, lifecycleWaitContractForPlan(plan),
	)
}

func waitLifecyclePressureForPlan(
	ctx context.Context,
	plan Plan,
	profile PreparedProfile,
	requireCycle bool,
	pressure lifecycle.Pressure,
	purpose pressureWaitPurpose,
	limit time.Duration,
) (lifecycle.Status, error) {
	contract := lifecycleWaitContractForPlan(plan)
	if contract == lifecycleWaitV23 && purpose == pressureWaitRecover {
		contract = lifecycleWaitV23PressureRecovery
	}
	return waitLifecycleWithContract(
		ctx, profile, requireCycle, pressure, limit, contract,
	)
}

func waitLifecycleWithContract(
	ctx context.Context,
	profile PreparedProfile,
	requireCycle bool,
	pressure lifecycle.Pressure,
	limit time.Duration,
	contract lifecycleWaitContract,
) (lifecycle.Status, error) {
	inspector, err := newProfileInspector(profile, profileInspectionLegacy)
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
			if complete && (pressure == "" || status.Capacity.Pressure == pressure) {
				return status, nil
			}
		}
		select {
		case <-phase.Done():
			if contract >= lifecycleWaitV23 && ctx.Err() != nil {
				return lifecycle.Status{}, ctx.Err()
			}
			if contract == lifecycleWaitV23PressureRecovery {
				return lifecycle.Status{}, errPressureRecoveryDeadline
			}
			if contract == lifecycleWaitV23 && pressure != "" {
				return lifecycle.Status{}, errors.Join(errLifecycleCycleDeadline, errProductionPressure)
			}
			if contract == lifecycleWaitV23 {
				return lifecycle.Status{}, errLifecycleCycleDeadline
			}
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
	inspector, err := newProfileInspector(profile, profileInspectionLegacy)
	if err != nil {
		return 0, false, err
	}
	runner := &authorizedQueryRunner{inspector: inspector, profile: profile, maxAttempts: 1}
	return queryProfileWithRunner(ctx, runner, serviceKey, requireCitation)
}

func queryProfileV23(
	ctx context.Context,
	profile PreparedProfile,
	serviceKey string,
	requireCitation bool,
) (int, bool, *AuthorizedQueryObservation, error) {
	inspector, err := newProfileInspector(profile, profileInspectionLegacy)
	if err != nil {
		return 0, false, nil, err
	}
	runner := &authorizedQueryRunner{
		inspector: inspector, profile: profile, maxAttempts: 3,
		retryDelay: time.Second, retainFailure: true,
	}
	count, exact, err := queryProfileWithRunner(ctx, runner, serviceKey, requireCitation)
	return count, exact, runner.failure, err
}

type authorizedQueryRunner struct {
	inspector     *profileInspector
	profile       PreparedProfile
	maxAttempts   int
	retryDelay    time.Duration
	retainFailure bool
	failure       *AuthorizedQueryObservation
}

func (runner *authorizedQueryRunner) fail(query string, attempt int, cause error) error {
	if !runner.retainFailure {
		return cause
	}
	projection := &AuthorizedQueryObservation{
		Schema: authorizedQuerySchemaV1, Profile: runner.profile.Name,
		Query: query, Class: "response", Attempts: attempt,
	}
	var status *privateHTTPStatusError
	switch {
	case errors.As(cause, &status):
		projection.Class = "status"
		projection.Status = status.Status
	case errors.Is(cause, errHTTPTransport):
		projection.Class = "transport"
	}
	runner.failure = projection
	return errors.Join(errAuthorizedQuery, cause)
}

func (runner *authorizedQueryRunner) retryable(cause error) bool {
	if errors.Is(cause, errHTTPTransport) {
		return true
	}
	var status *privateHTTPStatusError
	return errors.As(cause, &status) && status.Status == http.StatusConflict
}

func (runner *authorizedQueryRunner) waitRetry(ctx context.Context) error {
	if runner.retryDelay <= 0 {
		return nil
	}
	timer := time.NewTimer(runner.retryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errHTTPTransport
	case <-timer.C:
		return nil
	}
}

func (runner *authorizedQueryRunner) get(
	ctx context.Context,
	query, path string,
	target any,
) error {
	for attempt := 1; attempt <= runner.maxAttempts; attempt++ {
		err := runner.inspector.get(ctx, runner.profile, path, target)
		if err == nil {
			return nil
		}
		if attempt < runner.maxAttempts && runner.retryable(err) {
			if waitErr := runner.waitRetry(ctx); waitErr == nil {
				continue
			}
		}
		return runner.fail(query, attempt, err)
	}
	return runner.fail(query, runner.maxAttempts, errHTTPTransport)
}

func (runner *authorizedQueryRunner) unauthorized(ctx context.Context) error {
	for attempt := 1; attempt <= runner.maxAttempts; attempt++ {
		request, err := http.NewRequestWithContext(
			ctx, http.MethodGet, "http://"+runner.profile.Address+"/api/repos", nil,
		)
		if err != nil {
			return runner.fail("unauthorized_repositories", attempt, errHTTPResponse)
		}
		response, err := runner.inspector.client.Do(request)
		if err != nil {
			err = errHTTPTransport
		} else {
			_, _ = io.CopyN(io.Discard, response.Body, 4096)
			_ = response.Body.Close()
			if !runner.retainFailure && response.StatusCode != http.StatusUnauthorized {
				return exactOracle("unauthorized query was not denied")
			}
			switch response.StatusCode {
			case http.StatusUnauthorized:
				return nil
			case http.StatusConflict:
				err = &privateHTTPStatusError{Status: response.StatusCode, Reason: httpReason409Stale}
			default:
				if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
					return exactOracle("unauthorized query was not denied")
				}
				err = &privateHTTPStatusError{Status: response.StatusCode, Reason: httpReasonOther}
			}
		}
		if attempt < runner.maxAttempts && runner.retryable(err) {
			if waitErr := runner.waitRetry(ctx); waitErr == nil {
				continue
			}
		}
		return runner.fail("unauthorized_repositories", attempt, err)
	}
	return runner.fail("unauthorized_repositories", runner.maxAttempts, errHTTPTransport)
}

func queryProfileWithRunner(
	ctx context.Context,
	runner *authorizedQueryRunner,
	serviceKey string,
	requireCitation bool,
) (int, bool, error) {
	if err := runner.unauthorized(ctx); err != nil {
		return 0, false, err
	}
	var searchResult search.Result
	if err := runner.get(ctx, "search", "/api/search?q=T401&max_matches=1", &searchResult); err != nil {
		return 0, false, err
	}
	if len(searchResult.Files) == 0 || len(searchResult.Files[0].Chunks) == 0 ||
		len(searchResult.Files[0].Chunks[0].Ranges) == 0 {
		return 0, false, exactOracle("authorized search returned no exact match")
	}
	var services apiresponse.ServiceInventory
	servicePath := "/api/services?repository=" + url.QueryEscape(runner.profile.RepositoryName) + "&page_size=100"
	if err := runner.get(ctx, "services", servicePath, &services); err != nil {
		return 0, false, err
	}
	if services.Repository.CatalogServiceCount < 1 || services.Pagination.Returned != len(services.Services) {
		return 0, false, exactOracle("authorized service inventory is invalid")
	}
	var relationships apiresponse.RelationshipPage
	relationshipPath := "/api/service-relationships?repository=" + url.QueryEscape(runner.profile.RepositoryName) +
		"&service_key=" + url.QueryEscape(serviceKey) + "&page_size=100"
	if err := runner.get(ctx, "relationships", relationshipPath, &relationships); err != nil {
		return 0, false, err
	}
	if len(relationships.Roots) != 1 || relationships.Roots[0].Generation == "" || relationships.Roots[0].RootDigest == "" {
		return 0, false, exactOracle("relationship response lacks exact root authority")
	}
	if requireCitation && len(relationships.Rows) == 0 {
		return 0, false, exactOracle("relationship query returned no citable row")
	}
	if len(relationships.Rows) > 0 {
		if relationships.Rows[0].Citation == "" {
			return 0, false, exactOracle("relationship row lacks a citation token")
		}
		var citation apiresponse.RelationshipCitation
		path := "/api/service-relationship-citation?citation=" + url.QueryEscape(relationships.Rows[0].Citation)
		if err := runner.get(ctx, "citation", path, &citation); err != nil {
			return 0, false, err
		}
		if citation.Repository != runner.profile.RepositoryName || citation.Generation != relationships.Roots[0].Generation ||
			citation.RootDigest != relationships.Roots[0].RootDigest || citation.AuthorityDigest == "" ||
			citation.Content == "" {
			return 0, false, exactOracle("citation differs from rendered root authority")
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

func emptyObservationForPlan(environment EnvironmentObservation, plan Plan) Observation {
	value := emptyObservation(environment)
	version := planSchemaVersion(plan.Schema)
	if version > 0 {
		value.Schema = ceremonySchemaLadder[version].observation
	}
	if version >= 2 {
		value.HostToolchain = slices.Clone(plan.HostToolchain)
	}
	if version >= 17 {
		interruptionSchema := interruptionSchemaV1
		if version >= 22 {
			interruptionSchema = interruptionSchemaV2
		}
		value.Interruption = &InterruptionObservation{
			Schema: interruptionSchema, LastSubstage: "not_started",
		}
	}
	return value
}

func succeededPhase(name string, metrics PhaseMetrics) PhaseObservation {
	return PhaseObservation{
		Name: name, Outcome: "succeeded", Metrics: metrics,
		AuthorityChanged: metrics.PublicationTransactions > 0, OracleExact: true,
	}
}

func mergeMetrics(values ...PhaseMetrics) (PhaseMetrics, error) {
	var result PhaseMetrics
	for _, value := range values {
		var err error
		result.WallMS, err = checkedAddInt64(result.WallMS, value.WallMS)
		if err != nil {
			return PhaseMetrics{}, errors.New("T40.13 phase wall aggregation overflowed")
		}
		result.PeakRSSBytes = max(result.PeakRSSBytes, value.PeakRSSBytes)
		result.DataLogicalBytes = max(result.DataLogicalBytes, value.DataLogicalBytes)
		result.DataAllocatedBytes = max(result.DataAllocatedBytes, value.DataAllocatedBytes)
		if err := addMetricScalars(&result, value); err != nil {
			return PhaseMetrics{}, err
		}
	}
	return result, nil
}

func mergeMetricsPreservingError(operationErr error, values ...PhaseMetrics) (PhaseMetrics, error) {
	metrics, mergeErr := mergeMetrics(values...)
	if mergeErr != nil && len(values) > 0 {
		metrics = values[0]
	}
	return metrics, errors.Join(operationErr, mergeErr)
}

// mergeConcurrentMetrics keeps the outer wall interval and conservatively sums
// process-tree RSS peaks for work that ran underneath it. All other gauges use
// the same max/add rules as sequential phase merging.
func mergeConcurrentMetrics(outer, concurrent PhaseMetrics) (PhaseMetrics, error) {
	var err error
	result := outer
	result.PeakRSSBytes, err = checkedAddInt64(outer.PeakRSSBytes, concurrent.PeakRSSBytes)
	if err != nil {
		return PhaseMetrics{}, errors.New("T40.13 concurrent RSS accounting overflowed")
	}
	result.DataLogicalBytes = max(result.DataLogicalBytes, concurrent.DataLogicalBytes)
	result.DataAllocatedBytes = max(result.DataAllocatedBytes, concurrent.DataAllocatedBytes)
	if err := addMetricScalars(&result, concurrent); err != nil {
		return PhaseMetrics{}, err
	}
	return result, nil
}

func addMetricScalars(result *PhaseMetrics, value PhaseMetrics) error {
	if result == nil {
		return errors.New("T40.13 phase metric destination is invalid")
	}
	var err error
	if result.GitChildren, err = checkedAddInt64(result.GitChildren, value.GitChildren); err != nil {
		return err
	}
	if result.IndexChildren, err = checkedAddInt64(result.IndexChildren, value.IndexChildren); err != nil {
		return err
	}
	if result.OtherChildren, err = checkedAddInt64(result.OtherChildren, value.OtherChildren); err != nil {
		return err
	}
	if result.ControlReads, err = checkedAddInt64(result.ControlReads, value.ControlReads); err != nil {
		return err
	}
	if result.MemberReads, err = checkedAddInt64(result.MemberReads, value.MemberReads); err != nil {
		return err
	}
	if result.PublicationWrites, err = checkedAddInt64(result.PublicationWrites, value.PublicationWrites); err != nil {
		return err
	}
	if result.PublicationTransactions, err = checkedAddInt64(result.PublicationTransactions, value.PublicationTransactions); err != nil {
		return err
	}
	if result.OrchestrationTransactions, err = checkedAddInt64(result.OrchestrationTransactions, value.OrchestrationTransactions); err != nil {
		return err
	}
	if result.Retries, err = checkedAddInt64(result.Retries, value.Retries); err != nil {
		return err
	}
	if result.ReusedControls, err = checkedAddInt64(result.ReusedControls, value.ReusedControls); err != nil {
		return err
	}
	if result.ReusedMembers, err = checkedAddInt64(result.ReusedMembers, value.ReusedMembers); err != nil {
		return err
	}
	if result.OtherToGitTransitions, err = checkedAddInt64(result.OtherToGitTransitions, value.OtherToGitTransitions); err != nil {
		return err
	}
	if result.OtherToIndexTransitions, err = checkedAddInt64(result.OtherToIndexTransitions, value.OtherToIndexTransitions); err != nil {
		return err
	}
	if result.GitToOtherTransitions, err = checkedAddInt64(result.GitToOtherTransitions, value.GitToOtherTransitions); err != nil {
		return err
	}
	if result.GitToIndexTransitions, err = checkedAddInt64(result.GitToIndexTransitions, value.GitToIndexTransitions); err != nil {
		return err
	}
	if result.IndexToOtherTransitions, err = checkedAddInt64(result.IndexToOtherTransitions, value.IndexToOtherTransitions); err != nil {
		return err
	}
	if result.IndexToGitTransitions, err = checkedAddInt64(result.IndexToGitTransitions, value.IndexToGitTransitions); err != nil {
		return err
	}
	return nil
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
	if observationSchemaVersion(value.Schema) >= 25 {
		if err := StageObservation(path, value); err != nil {
			return err
		}
		return PublishObservation(path, value)
	}
	return writeObservationLegacy(path, value)
}

// WriteReturnedObservation preserves the V1-V24 command contract. V25 owns
// durable publication inside Execute so the command must not write after it.
func WriteReturnedObservation(path string, value Observation) error {
	if observationSchemaVersion(value.Schema) >= 25 {
		return nil
	}
	return writeObservationLegacy(path, value)
}

func writeObservationLegacy(path string, value Observation) error {
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

// StageObservation durably writes complete candidate bytes beside the output.
// A surviving teardown checkpoint keeps those bytes provisional.
func StageObservation(path string, value Observation) error {
	raw, err := MarshalObservation(value)
	if err != nil {
		return err
	}
	return stageAtomicOutput(path, raw, MaxObservationBytes, false)
}

// PublishObservation atomically links bytes that were durably staged after
// custody absence and final validation into their output path.
func PublishObservation(path string, value Observation) error {
	raw, err := MarshalObservation(value)
	if err != nil {
		return err
	}
	return publishAtomicOutput(path, raw, MaxObservationBytes, true)
}

// WriteReceipt durably publishes one validated receipt without exposing a
// partial final file. Repeating the write with identical bytes completes an
// interrupted publication; different existing bytes always fail closed.
func WriteReceipt(path string, raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxReceiptBytes {
		return errors.New("T40.13 receipt output is invalid")
	}
	if err := stageAtomicOutput(path, raw, MaxReceiptBytes, true); err != nil {
		return err
	}
	return publishAtomicOutput(path, raw, MaxReceiptBytes, true)
}

// ResumeObservation validates a final or staged source-free observation
// against the frozen plan, then completes an interrupted atomic publication.
func ResumeObservation(path string, planBytes []byte, planDigest string) (raw []byte, retErr error) {
	return resumeObservation(path, planBytes, planDigest, nil)
}

func resumeObservation(
	path string,
	planBytes []byte,
	planDigest string,
	hostToolchainVerify func() error,
) (raw []byte, retErr error) {
	finalPath, err := canonicalNewOutputPath(path)
	if err != nil {
		return nil, err
	}
	plan, err := DecodePlan(planBytes)
	if err != nil || planDigest != PlanDigest(planBytes) {
		return nil, errors.Join(err, errors.New("T40.13 observation resume plan binding is invalid"))
	}
	planBytes = slices.Clone(planBytes)
	var checkpoint teardownCheckpoint
	var checkpointErr error
	if planSchemaVersion(plan.Schema) >= 25 {
		locator, locatedCheckpoint, locateErr := readTeardownCheckpointIdentity(finalPath)
		if errors.Is(locateErr, os.ErrNotExist) {
			return readSettledV25Observation(finalPath, planBytes, planDigest)
		}
		if locateErr != nil {
			return nil, locateErr
		}
		workspace, _, rootErr := custodyControlDirectory(locatedCheckpoint.Workspace)
		if rootErr != nil {
			return nil, rootErr
		}
		admissionLock, lockErr := lockRunRoot(filepath.Dir(workspace))
		if lockErr != nil {
			return nil, lockErr
		}
		defer func() {
			retErr = errors.Join(retErr, admissionLock.Close())
		}()
		lockedIdentity, lockedCheckpoint, lockedErr := readTeardownCheckpointIdentity(finalPath)
		if lockedErr != nil {
			return nil, lockedErr
		}
		if locator.path != lockedIdentity.path || !bytes.Equal(locator.raw, lockedIdentity.raw) {
			return nil, errors.New("T40.13 teardown admission changed before locking")
		}
		checkpoint = lockedCheckpoint
	} else {
		checkpoint, checkpointErr = readTeardownCheckpoint(finalPath)
	}
	if checkpointErr == nil {
		if checkpoint.PlanDigest != planDigest {
			return nil, errors.New("T40.13 teardown checkpoint plan digest differs")
		}
		if checkpoint.Schema != teardownCheckpointSchemaForPlan(plan) {
			return nil, errors.New("T40.13 teardown checkpoint schema differs from the plan")
		}
		if checkpoint.Schema == teardownCheckpointSchema {
			if err := confirmCustodyDeletionDurable(checkpoint.Workspace); err != nil {
				return nil, fmt.Errorf("T40.13 teardown checkpoint lacks durable custody deletion: %w", err)
			}
			if err := removeProvisionalObservation(finalPath); err != nil {
				return nil, fmt.Errorf("retire provisional T40.13 observation: %w", err)
			}
			return resumeTeardownCheckpoint(
				finalPath, planBytes, plan, checkpoint, "", nil, hostToolchainVerify,
			)
		}
		checkpointRaw, err := marshalTeardownCheckpoint(checkpoint)
		if err != nil {
			return nil, err
		}
		checkpointDigest := digest(checkpointRaw)
		if !hexIdentity(checkpoint.SupervisionToken, 64) || checkpoint.ModuleRoot == "" ||
			checkpoint.PreparedPath == "" {
			return nil, errors.New("T40.13 teardown checkpoint lacks durable custody supervision")
		}
		preparedPresent, err := validatePreparedPublicationDigest(
			checkpoint.PreparedPath, checkpoint.PreparedDigest,
		)
		if err != nil {
			return nil, fmt.Errorf("revalidate T40.13 prepared admission: %w", err)
		}
		status, supervision, err := inspectCustodySupervision(
			checkpoint.Workspace, planDigest, checkpoint.SupervisionToken,
			custodyOperationExecute, checkpointDigest,
		)
		if err != nil {
			if retiredErr := confirmCustodySupervisionRetired(
				checkpoint.Workspace, planDigest, checkpoint.SupervisionToken,
				custodyOperationExecute, checkpointDigest,
			); retiredErr == nil {
				return completeRetiredTeardownCheckpoint(
					finalPath, planBytes, checkpoint, checkpointDigest,
				)
			}
			return nil, fmt.Errorf(
				"T40.13 teardown custody is indeterminate and retained for reviewed purge: %w", err,
			)
		}
		if status == custodyStatusLive {
			return nil, errors.New("T40.13 teardown custody remains live")
		}
		if status == custodyStatusIndeterminate || supervision == nil {
			return nil, errors.New("T40.13 teardown custody is indeterminate and retained for reviewed purge")
		}
		defer func() {
			retErr = errors.Join(retErr, supervision.Close())
		}()
		// The checkpoint is the authority until it is durably retired. Any
		// observation beside it is only a provisional publication that may
		// precede the final wall/toolchain check.
		if status == custodyStatusTerminal {
			raw, err := readTerminalTeardownCheckpoint(finalPath, planBytes, checkpoint)
			if err != nil {
				return nil, err
			}
			if err := supervision.Retire(); err != nil {
				if retiredErr := confirmCustodySupervisionRetired(
					checkpoint.Workspace, planDigest, checkpoint.SupervisionToken,
					custodyOperationExecute, checkpointDigest,
				); retiredErr != nil {
					return nil, fmt.Errorf("retire terminal T40.13 custody supervision: %w",
						errors.Join(err, retiredErr))
				}
			}
			if err := removePreparedPublication(checkpoint.PreparedPath); err != nil {
				return nil, fmt.Errorf("retire terminal T40.13 prepared publication: %w", err)
			}
			if err := removeTeardownCheckpoint(finalPath); err != nil {
				return nil, fmt.Errorf("retire terminal T40.13 teardown checkpoint: %w", err)
			}
			return raw, nil
		}
		if status != custodyStatusDrained {
			return nil, errors.New("T40.13 teardown custody state is invalid")
		}
		if !preparedPresent {
			return nil, errors.New("T40.13 drained teardown lacks its exact prepared admission")
		}
		if err := removeProvisionalObservation(finalPath); err != nil {
			return nil, fmt.Errorf("retire provisional T40.13 observation: %w", err)
		}
		return resumeTeardownCheckpoint(
			finalPath, planBytes, plan, checkpoint, checkpointDigest, supervision,
			hostToolchainVerify,
		)
	} else if !errors.Is(checkpointErr, os.ErrNotExist) {
		return nil, checkpointErr
	}

	for _, candidate := range []string{finalPath, finalPath + ".tmp"} {
		raw, readErr := readAtomicRegular(candidate, MaxObservationBytes)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, fmt.Errorf("read T40.13 observation for resume: %w", readErr)
		}
		if _, err := BuildReceipt(planBytes, raw, planDigest); err != nil {
			return nil, fmt.Errorf("validate T40.13 observation before resume: %w", err)
		}
		if err := publishAtomicOutput(finalPath, raw, MaxObservationBytes, true); err != nil {
			return nil, fmt.Errorf("resume T40.13 observation publication: %w", err)
		}
		if err := removeTeardownCheckpoint(finalPath); err != nil {
			return nil, fmt.Errorf("retire resumed T40.13 teardown checkpoint: %w", err)
		}
		return raw, nil
	}
	return nil, fmt.Errorf("read T40.13 observation for resume: %w", os.ErrNotExist)
}

func readSettledV25Observation(path string, planBytes []byte, planDigest string) ([]byte, error) {
	if _, err := readAtomicRegular(path+".tmp", MaxObservationBytes); err == nil {
		return nil, errors.New("T40.13 staged observation lacks run-root teardown authority")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read T40.13 observation for resume: %w", err)
	}
	raw, err := readAtomicRegular(path, MaxObservationBytes)
	if err != nil {
		return nil, fmt.Errorf("read T40.13 observation for resume: %w", err)
	}
	if _, err := BuildReceipt(planBytes, raw, planDigest); err != nil {
		return nil, fmt.Errorf("validate T40.13 observation before resume: %w", err)
	}
	return raw, nil
}

func teardownCheckpointSchemaForPlan(plan Plan) string {
	if planSchemaVersion(plan.Schema) >= 25 {
		return teardownCheckpointSchemaV2
	}
	return teardownCheckpointSchema
}

func completeRetiredTeardownCheckpoint(
	path string, planBytes []byte, checkpoint teardownCheckpoint, checkpointDigest string,
) ([]byte, error) {
	if err := confirmCustodySupervisionRetired(
		checkpoint.Workspace, checkpoint.PlanDigest, checkpoint.SupervisionToken,
		custodyOperationExecute, checkpointDigest,
	); err != nil {
		return nil, err
	}
	raw, err := readTerminalTeardownCheckpoint(path, planBytes, checkpoint)
	if err != nil {
		return nil, err
	}
	if err := removePreparedPublication(checkpoint.PreparedPath); err != nil {
		return nil, fmt.Errorf("retire terminal T40.13 prepared publication: %w", err)
	}
	if err := removeTeardownCheckpoint(path); err != nil {
		return nil, fmt.Errorf("retire terminal T40.13 teardown checkpoint: %w", err)
	}
	return raw, nil
}

func readTerminalTeardownCheckpoint(
	path string, planBytes []byte, checkpoint teardownCheckpoint,
) ([]byte, error) {
	raw, err := readAtomicRegular(path, MaxObservationBytes)
	if err != nil {
		return nil, fmt.Errorf("read terminal T40.13 observation: %w", err)
	}
	if _, err := BuildReceipt(planBytes, raw, checkpoint.PlanDigest); err != nil {
		return nil, fmt.Errorf("validate terminal T40.13 observation: %w", err)
	}
	if err := confirmCustodyDeletionDurable(checkpoint.Workspace); err != nil {
		return nil, fmt.Errorf("terminal T40.13 custody deletion is not durable: %w", err)
	}
	return raw, nil
}

func writeTeardownCheckpoint(path string, value teardownCheckpoint) error {
	raw, err := marshalTeardownCheckpoint(value)
	if err != nil {
		return err
	}
	checkpointPath := path + ".teardown"
	if err := stageAtomicOutput(
		checkpointPath, raw, maximumTeardownCheckpointBytes, false,
	); err != nil {
		return err
	}
	return publishAtomicOutput(checkpointPath, raw, maximumTeardownCheckpointBytes, true)
}

func marshalTeardownCheckpoint(value teardownCheckpoint) ([]byte, error) {
	if err := validateTeardownCheckpoint(value); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	return raw, nil
}

func readTeardownCheckpoint(path string) (teardownCheckpoint, error) {
	_, value, err := readTeardownCheckpointIdentity(path)
	return value, err
}

func readTeardownCheckpointIdentity(path string) (exactFileIdentity, teardownCheckpoint, error) {
	checkpointPath := path + ".teardown"
	raw, err := readAtomicRegular(checkpointPath, maximumTeardownCheckpointBytes)
	if errors.Is(err, os.ErrNotExist) {
		checkpointPath += ".tmp"
		raw, err = readAtomicRegular(checkpointPath, maximumTeardownCheckpointBytes)
	}
	if err != nil {
		return exactFileIdentity{}, teardownCheckpoint{}, err
	}
	var value teardownCheckpoint
	if err := decodeStrict(raw, &value); err != nil {
		return exactFileIdentity{}, teardownCheckpoint{}, fmt.Errorf("decode T40.13 teardown checkpoint: %w", err)
	}
	if err := validateSerializedDataMeasurementField(
		raw, observationSchemaVersion(value.Observation.Schema), true,
	); err != nil {
		return exactFileIdentity{}, teardownCheckpoint{}, err
	}
	if err := validateTeardownCheckpoint(value); err != nil {
		return exactFileIdentity{}, teardownCheckpoint{}, err
	}
	if value.Schema == teardownCheckpointSchemaV2 {
		canonical, err := marshalTeardownCheckpoint(value)
		if err != nil || !bytes.Equal(raw, canonical) {
			return exactFileIdentity{}, teardownCheckpoint{}, errors.Join(
				err, errors.New("T40.13 V25 teardown checkpoint is not canonical"),
			)
		}
	}
	return exactFileIdentity{
		path: checkpointPath, raw: raw, maximum: maximumTeardownCheckpointBytes,
		description: "teardown checkpoint",
	}, value, nil
}

func validateTeardownCheckpoint(value teardownCheckpoint) error {
	_, err := time.Parse(time.RFC3339Nano, value.StartedAt)
	if err != nil ||
		value.Schema != teardownCheckpointSchema && value.Schema != teardownCheckpointSchemaV2 ||
		!digestIdentity(value.PlanDigest) ||
		!filepath.IsAbs(value.Workspace) || filepath.Clean(value.Workspace) == string(filepath.Separator) ||
		value.SupervisionToken != "" && !hexIdentity(value.SupervisionToken, 64) ||
		value.DataLogicalBytes < 0 || value.DataAllocatedBytes < 0 ||
		value.Observation.Teardown.Completed ||
		len(value.Observation.Phases) != len(phaseOrder) ||
		value.Observation.Phases[len(phaseOrder)-1].Outcome != "not_run" &&
			(value.Observation.Outcome != "stopped" ||
				value.Observation.Phases[len(phaseOrder)-1].Outcome != "failed") {
		return errors.New("T40.13 teardown checkpoint is invalid")
	}
	if value.Schema == teardownCheckpointSchema {
		if value.SupervisionToken != "" || value.ModuleRoot != "" || value.PreparedPath != "" ||
			value.PreparedDigest != "" {
			return errors.New("T40.13 historical teardown checkpoint acquired supervision")
		}
	} else if !hexIdentity(value.SupervisionToken, 64) ||
		!filepath.IsAbs(value.ModuleRoot) || filepath.Clean(value.ModuleRoot) == string(filepath.Separator) ||
		!filepath.IsAbs(value.PreparedPath) || filepath.Clean(value.PreparedPath) == string(filepath.Separator) ||
		!digestIdentity(value.PreparedDigest) ||
		value.ModuleRoot == value.Workspace || isWithin(value.ModuleRoot, value.Workspace) ||
		isWithin(value.Workspace, value.ModuleRoot) || value.PreparedPath == value.Workspace ||
		isWithin(value.PreparedPath, value.Workspace) {
		return errors.New("T40.13 teardown checkpoint custody boundary is invalid")
	}
	if value.DeadlineAt != "" {
		_, deadlineErr := time.Parse(time.RFC3339Nano, value.DeadlineAt)
		if deadlineErr != nil {
			return errors.New("T40.13 teardown checkpoint deadline is invalid")
		}
	}
	return ValidateObservation(value.Observation)
}

func resumeTeardownCheckpoint(
	path string,
	planBytes []byte,
	plan Plan,
	checkpoint teardownCheckpoint,
	checkpointDigest string,
	supervision *custodySupervision,
	hostToolchainVerify func() error,
) ([]byte, error) {
	started, _ := time.Parse(time.RFC3339Nano, checkpoint.StartedAt)
	ctx := context.Background()
	var cancel context.CancelFunc
	if checkpoint.DeadlineAt != "" {
		deadline, _ := time.Parse(time.RFC3339Nano, checkpoint.DeadlineAt)
		ctx, cancel = context.WithDeadlineCause(ctx, deadline, errTotalWallDeadline)
		defer cancel()
	}
	run := &execution{
		ctx: ctx, moduleRoot: checkpoint.ModuleRoot, preparedPath: checkpoint.PreparedPath,
		preparedDigest: checkpoint.PreparedDigest,
		plan:           plan, planBytes: planBytes, workspace: checkpoint.Workspace,
		observation: checkpoint.Observation, observationPath: path, checkpointPersisted: true,
		supervision: supervision, checkpointDigest: checkpointDigest,
		hostToolchainVerify: hostToolchainVerify,
	}
	if supervision != nil {
		if retentionErr := custodyRetentionCause(run.ctx, nil); retentionErr != nil {
			return nil, fmt.Errorf("resume T40.13 teardown before deletion: %w", retentionErr)
		}
		if err := supervision.BeginFinalization(checkpointDigest); err != nil {
			return nil, fmt.Errorf("resume T40.13 drained custody: %w", err)
		}
	}
	if planSchemaVersion(plan.Schema) >= 25 && hostToolchainVerify == nil {
		binding, bindErr := bindHostToolchainForPlan(ctx, plan)
		if bindErr != nil {
			return nil, fmt.Errorf("resume T40.13 frozen host toolchain: %w", bindErr)
		}
		run.hostTools = binding
		run.hostTerminalVerified = true
	}
	if supervision != nil {
		if err := destroyCustody(run.workspace, run.moduleRoot); err != nil {
			return nil, fmt.Errorf("resume T40.13 custody deletion: %w", err)
		}
		if err := confirmCustodyDeletionDurable(run.workspace); err != nil {
			return nil, fmt.Errorf("resume T40.13 custody deletion: %w", err)
		}
	}
	var recoveryErr error
	if run.observation.Outcome == "completed" {
		recoveryErr = errTeardownRecovery
	}
	completeErr := run.completeTeardown(
		started, checkpoint.DataLogicalBytes, checkpoint.DataAllocatedBytes, recoveryErr,
	)
	if run.checkpointPersisted || !run.observationPersisted {
		return nil, fmt.Errorf("resume T40.13 teardown checkpoint: %w",
			errors.Join(completeErr, errors.New("teardown checkpoint remains authoritative")))
	}
	raw, err := MarshalObservation(run.observation)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func validatePreparedPublicationDigest(path, expected string) (bool, error) {
	if !digestIdentity(expected) {
		return false, errors.New("T40.13 prepared admission digest is invalid")
	}
	raw, err := readPreparedPublicationBytes(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if digest(raw) != expected {
		return true, errors.New("T40.13 prepared admission bytes changed")
	}
	return true, nil
}

func removeTeardownCheckpoint(path string) error {
	checkpointPath := path + ".teardown"
	parent := filepath.Dir(checkpointPath)
	removeErr := removeAtomicTemporary(checkpointPath+".tmp", parent)
	if removeErr == nil {
		removeErr = removeAtomicTemporary(checkpointPath, parent)
	}
	syncErr := syncDirectory(parent)
	_, verifyErr := readTeardownCheckpoint(path)
	if removeErr != nil {
		if verifyErr != nil {
			verifyErr = fmt.Errorf("T40.13 teardown checkpoint authority became unreadable: %w", verifyErr)
		} else {
			verifyErr = nil
		}
	} else if verifyErr == nil {
		verifyErr = errors.New("T40.13 teardown checkpoint survived retirement")
	} else if errors.Is(verifyErr, os.ErrNotExist) {
		verifyErr = nil
	}
	return errors.Join(removeErr, syncErr, verifyErr)
}

func removeProvisionalObservation(path string) error {
	parent := filepath.Dir(path)
	if err := removeAtomicTemporary(path+".tmp", parent); err != nil {
		return err
	}
	return removeAtomicTemporary(path, parent)
}

func canonicalNewOutputPath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return "", errors.New("output path must be absolute and non-root")
	}
	cleaned := filepath.Clean(path)
	parent, err := filepath.EvalSymlinks(filepath.Dir(cleaned))
	if err != nil {
		return "", fmt.Errorf("resolve output directory: %w", err)
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("output directory is not a real directory")
	}
	return filepath.Join(parent, filepath.Base(cleaned)), nil
}

func stageAtomicOutput(path string, raw []byte, maximumBytes int, allowIdenticalExisting bool) error {
	if len(raw) == 0 || maximumBytes <= 0 || len(raw) > maximumBytes {
		return errors.New("T40.13 atomic output bytes are invalid")
	}
	finalPath, err := canonicalNewOutputPath(path)
	if err != nil {
		return err
	}
	parent := filepath.Dir(finalPath)
	temporaryPath := finalPath + ".tmp"

	if existing, readErr := readAtomicRegular(finalPath, maximumBytes); readErr == nil {
		if !allowIdenticalExisting {
			return fmt.Errorf("create T40.13 atomic output: %w", os.ErrExist)
		}
		if !bytes.Equal(existing, raw) {
			return errors.New("T40.13 existing atomic output differs")
		}
		if err := syncRegularFile(finalPath); err != nil {
			return err
		}
		if err := validateAndRemoveAtomicTemporary(temporaryPath, parent, raw, maximumBytes); err != nil {
			return err
		}
		return syncDirectory(parent)
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if err := removeAtomicTemporary(temporaryPath, parent); err != nil {
		return err
	}

	file, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create T40.13 atomic temporary output: %w", err)
	}
	written, writeErr := io.Copy(file, bytes.NewReader(raw))
	if writeErr == nil && written != int64(len(raw)) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		return cleanupFailedAtomicStage(file, temporaryPath, parent,
			fmt.Errorf("write T40.13 atomic temporary output: %w", writeErr))
	}
	if err := file.Sync(); err != nil {
		return cleanupFailedAtomicStage(file, temporaryPath, parent,
			fmt.Errorf("sync T40.13 atomic temporary output: %w", err))
	}
	if err := file.Close(); err != nil {
		return cleanupFailedAtomicStage(nil, temporaryPath, parent,
			fmt.Errorf("close T40.13 atomic temporary output: %w", err))
	}
	if err := syncDirectory(parent); err != nil {
		// The complete fsynced stage remains available for diagnosis/resume,
		// but the caller must retain custody because its directory entry was
		// not proven durable.
		return fmt.Errorf("sync staged T40.13 output: %w", err)
	}
	return nil
}

func publishAtomicOutput(path string, raw []byte, maximumBytes int, allowIdenticalExisting bool) error {
	if len(raw) == 0 || maximumBytes <= 0 || len(raw) > maximumBytes {
		return errors.New("T40.13 atomic output bytes are invalid")
	}
	finalPath, err := canonicalNewOutputPath(path)
	if err != nil {
		return err
	}
	parent := filepath.Dir(finalPath)
	temporaryPath := finalPath + ".tmp"
	if existing, readErr := readAtomicRegular(finalPath, maximumBytes); readErr == nil {
		if !allowIdenticalExisting || !bytes.Equal(existing, raw) {
			return errors.New("T40.13 existing atomic output differs")
		}
		if err := validateAndRemoveAtomicTemporary(temporaryPath, parent, raw, maximumBytes); err != nil {
			return err
		}
		if err := syncRegularFile(finalPath); err != nil {
			return err
		}
		return syncDirectory(parent)
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	staged, err := readAtomicRegular(temporaryPath, maximumBytes)
	if err != nil {
		return fmt.Errorf("read staged T40.13 atomic output: %w", err)
	}
	if !bytes.Equal(staged, raw) {
		return errors.New("T40.13 staged atomic output differs")
	}
	if err := syncRegularFile(temporaryPath); err != nil {
		return err
	}

	if err := os.Link(temporaryPath, finalPath); err != nil {
		if !allowIdenticalExisting || !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("publish T40.13 atomic output: %w", err)
		}
		existing, readErr := readAtomicRegular(finalPath, maximumBytes)
		if readErr != nil || !bytes.Equal(existing, raw) {
			return fmt.Errorf("validate concurrently published T40.13 output: %w",
				errors.Join(readErr, errors.New("output differs")))
		}
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("sync published T40.13 output: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove T40.13 atomic temporary output: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("sync cleaned T40.13 output directory: %w", err)
	}
	return nil
}

func canonicalDurabilityRoot(durabilityRoot string) (string, error) {
	root, err := filepath.EvalSymlinks(durabilityRoot)
	if err != nil || !filepath.IsAbs(durabilityRoot) || root != filepath.Clean(durabilityRoot) {
		return "", errors.Join(err, errors.New("T40.13 staged durability root is invalid"))
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.Join(err, errors.New("T40.13 staged durability root is invalid"))
	}
	return root, nil
}

// SyncStagedFile makes one bounded source-free shell stage and its directory
// chain durable before that exact stage can become resumable authority.
func SyncStagedFile(path, durabilityRoot string) error {
	const maximumBytes = 4 << 20
	path, err := canonicalNewOutputPath(path)
	if err != nil {
		return fmt.Errorf("T40.13 staged sync input is invalid: %w", err)
	}
	root, err := canonicalDurabilityRoot(durabilityRoot)
	if err != nil {
		return err
	}
	if !isWithin(path, root) {
		return errors.New("T40.13 staged sync input is outside its root")
	}
	raw, err := readAtomicRegular(path, maximumBytes)
	if err != nil || len(raw) == 0 {
		return errors.Join(err, errors.New("T40.13 staged sync input is empty or invalid"))
	}
	if err := syncRegularFile(path); err != nil {
		return err
	}
	return syncDirectoryChain(filepath.Dir(path), root)
}

// DiscardStagedFile durably removes one incomplete non-authority stage.
func DiscardStagedFile(path, durabilityRoot string) error {
	const maximumBytes = 4 << 20
	path, err := canonicalNewOutputPath(path)
	if err != nil {
		return fmt.Errorf("T40.13 staged discard input is invalid: %w", err)
	}
	root, err := canonicalDurabilityRoot(durabilityRoot)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 0 || info.Size() > maximumBytes || !isWithin(path, root) {
		return errors.Join(err, errors.New("T40.13 staged discard input is invalid or outside its root"))
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove incomplete T40.13 stage: %w", err)
	}
	return syncDirectoryChain(filepath.Dir(path), root)
}

// PromoteStagedFile durably publishes one bounded source-free shell artifact
// without overwriting a differing authority file.
func PromoteStagedFile(temporaryPath, outputPath, durabilityRoot string) error {
	const maximumBytes = 4 << 20
	temporaryPath, err := canonicalNewOutputPath(temporaryPath)
	if err != nil {
		return fmt.Errorf("T40.13 staged promotion input is invalid: %w", err)
	}
	outputPath, err = canonicalNewOutputPath(outputPath)
	if err != nil {
		return fmt.Errorf("T40.13 staged promotion output is invalid: %w", err)
	}
	root, err := canonicalDurabilityRoot(durabilityRoot)
	if err != nil {
		return err
	}
	if filepath.Dir(temporaryPath) != filepath.Dir(outputPath) || temporaryPath == outputPath ||
		!isWithin(outputPath, root) {
		return errors.New("T40.13 staged promotion scope is invalid")
	}
	raw, err := readAtomicRegular(temporaryPath, maximumBytes)
	if err != nil || len(raw) == 0 {
		return errors.Join(err, errors.New("T40.13 staged promotion input is empty or invalid"))
	}
	temporaryInfo, err := os.Lstat(temporaryPath)
	if err != nil {
		return err
	}
	if outputInfo, outputErr := os.Lstat(outputPath); outputErr == nil {
		if !os.SameFile(temporaryInfo, outputInfo) {
			existing, readErr := readAtomicRegular(outputPath, maximumBytes)
			if readErr != nil || !bytes.Equal(raw, existing) {
				return fmt.Errorf("publish T40.13 staged file: %w",
					errors.Join(readErr, os.ErrExist))
			}
		}
	} else if !errors.Is(outputErr, os.ErrNotExist) {
		return outputErr
	} else if err := os.Link(temporaryPath, outputPath); err != nil {
		return fmt.Errorf("publish T40.13 staged file: %w", err)
	}
	if err := syncRegularFile(outputPath); err != nil {
		return err
	}
	if err := syncDirectoryChain(filepath.Dir(outputPath), root); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove T40.13 promoted stage: %w", err)
	}
	return syncDirectoryChain(filepath.Dir(outputPath), root)
}

func syncDirectoryChain(directory, root string) error {
	for {
		if err := syncDirectory(directory); err != nil {
			return err
		}
		if directory == root {
			return nil
		}
		parent := filepath.Dir(directory)
		if parent == directory || !isWithin(parent, root) && parent != root {
			return errors.New("T40.13 durability directory escaped its root")
		}
		directory = parent
	}
}

func cleanupFailedAtomicStage(file *os.File, temporaryPath, parent string, cause error) error {
	if file != nil {
		cause = errors.Join(cause, file.Close())
	}
	if info, err := os.Lstat(temporaryPath); err == nil && info.Mode().IsRegular() &&
		info.Mode()&os.ModeSymlink == 0 {
		cause = errors.Join(cause, os.Remove(temporaryPath), syncDirectory(parent))
	}
	return cause
}

func validateAndRemoveAtomicTemporary(path, parent string, raw []byte, maximumBytes int) error {
	staged, err := readAtomicRegular(path, maximumBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(staged, raw) {
		return errors.New("T40.13 staged and final atomic outputs differ")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove resumed T40.13 atomic temporary output: %w", err)
	}
	return syncDirectory(parent)
}

func readAtomicRegular(path string, maximumBytes int) ([]byte, error) {
	if maximumBytes <= 0 {
		return nil, errors.New("T40.13 exact-file read bound is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("T40.13 exact file is not regular")
	}
	if info.Size() < 0 || info.Size() > int64(maximumBytes) {
		return nil, errors.New("T40.13 exact file exceeds its byte bound")
	}
	file, err := openNoFollowRegular(path)
	if err != nil {
		return nil, fmt.Errorf("open T40.13 exact file: %w", err)
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil || !openedInfo.Mode().IsRegular() || !sameFileSnapshot(info, openedInfo) {
		return nil, errors.Join(errors.New("T40.13 exact file changed during open"), statErr, file.Close())
	}
	return readOpenedAtomicRegular(path, maximumBytes, openedInfo, file)
}

func readOpenedAtomicRegular(
	path string, maximumBytes int, openedInfo os.FileInfo, file *os.File,
) ([]byte, error) {
	raw, readErr := io.ReadAll(io.LimitReader(file, int64(maximumBytes)+1))
	afterOpen, afterStatErr := file.Stat()
	afterPath, lstatErr := os.Lstat(path)
	closeErr := file.Close()
	if readErr != nil || afterStatErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read T40.13 exact file: %w", errors.Join(
			readErr, afterStatErr, closeErr,
		))
	}
	if lstatErr != nil {
		// A post-open disappearance is an unstable authority, not the clean
		// pre-open absence that callers may handle as an empty state.
		return nil, fmt.Errorf("read T40.13 exact file: published path changed: %v", lstatErr)
	}
	if !afterPath.Mode().IsRegular() || afterPath.Mode()&os.ModeSymlink != 0 ||
		!sameFileSnapshot(openedInfo, afterOpen) || !sameFileSnapshot(openedInfo, afterPath) ||
		int64(len(raw)) != afterOpen.Size() {
		return nil, errors.New("T40.13 exact file changed during read")
	}
	if len(raw) > maximumBytes {
		return nil, errors.New("T40.13 exact file exceeds its byte bound")
	}
	return raw, nil
}

func sameFileSnapshot(first, second os.FileInfo) bool {
	return first != nil && second != nil && os.SameFile(first, second) &&
		first.Mode() == second.Mode() && first.Size() == second.Size() &&
		first.ModTime().Equal(second.ModTime())
}

func removeAtomicTemporary(path, parent string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect T40.13 atomic temporary output: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("T40.13 atomic temporary output is not a regular file")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale T40.13 atomic temporary output: %w", err)
	}
	return syncDirectory(parent)
}

func syncRegularFile(path string) error {
	file, err := openNoFollowRegular(path)
	if err != nil {
		return fmt.Errorf("open T40.13 atomic output for sync: %w", err)
	}
	return errors.Join(file.Sync(), file.Close())
}

func syncDirectory(path string) error {
	directory, err := openNoFollowDirectory(path)
	if err != nil {
		return fmt.Errorf("open T40.13 output directory for sync: %w", err)
	}
	return errors.Join(directory.Sync(), directory.Close())
}
