package t421

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/bmeddeb/phebs/spike/t4013"
)

var ErrExecutionEpochOne = errors.New("execution epoch-one launch unavailable or incomplete")

// ExecutionEpochOne owns the genuine shared reducers for one author/start/stop
// slice. Later producer slots remain unused: snapshots are prefixes, never a
// fifteen-phase completion or a host/profile/ceremony admission.
type ExecutionEpochOne struct {
	mu                     sync.Mutex
	epochs                 *ExecutionEpochConfigCustody
	plan                   Plan // Privately decoded once by this constructor.
	authorStarted          time.Time
	phebs, zoekt, surreal  *ExecutionToolCustody
	controller             *dispatchadmission.Controller
	parent                 *dispatchadmission.LocalProducer
	store                  *storeaccounting.Transport
	release                context.CancelFunc
	used, authored, closed bool
	closeErr               error
	retained               *ExecutionEpochOneRun
	logicalUsed            bool
	returnUsed             bool
}

// PrepareExecutionEpochOne starts no child. It rechecks the author's admitted
// protected plan, derives both unchanged V3 accounting ceilings, and advances
// only the empty phase-one accounting boundary. It does not repeat DecodePlan's
// physical regeneration or infer that preflight has passed.
func PrepareExecutionEpochOne(ctx context.Context, epochs *ExecutionEpochConfigCustody, phebs, zoekt, surreal *ExecutionToolCustody) (_ *ExecutionEpochOne, retErr error) {
	if ctx == nil || ctx.Err() != nil || epochs == nil || epochs.author == nil || phebs == nil || zoekt == nil || surreal == nil {
		return nil, ErrExecutionEpochOne
	}
	author := epochs.author
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if author.active || author.borrowedBy != nil || epochs.active || author.next != 0 || epochs.checkLocked(ctx, 1) != nil ||
		phebs.referenceInputs != author.request.Builds || zoekt.referenceInputs != author.request.Builds {
		return nil, ErrExecutionEpochOne
	}
	_, raw, err := readAuthorCustodyPlan(ctx, author.request.Plan)
	var plan Plan
	if err != nil || SHA256(raw) != author.planSHA256 || json.Unmarshal(raw, &plan) != nil {
		return nil, ErrExecutionEpochOne
	}
	var bindings [executionProducerCount][32]byte
	for index := range bindings {
		if _, err := rand.Read(bindings[index][:]); err != nil {
			return nil, ErrExecutionEpochOne
		}
	}
	da, err := executionDispatchConfig(plan, bindings)
	sa, wire, storeErr := executionStoreConfig(plan, bindings)
	if err != nil || storeErr != nil {
		return nil, ErrExecutionEpochOne
	}
	lifetime, release := context.WithCancel(context.Background())
	flow := &ExecutionEpochOne{epochs: epochs, plan: plan, phebs: phebs, zoekt: zoekt, surreal: surreal, release: release}
	defer func() {
		if retErr != nil {
			_ = flow.Close()
		}
	}()
	flow.controller, err = dispatchadmission.New(lifetime, da)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	flow.parent, err = flow.controller.NewLocalProducer(lifetime, executionRootProducer)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	store, err := storeaccounting.New(lifetime, sa)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	flow.store, err = storeaccounting.NewTransport(lifetime, store, wire)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	if flow.parent.Pause(ctx) != nil || flow.controller.Fence() != nil || flow.parent.Checkpoint(ctx) != nil ||
		flow.store.Fence() != nil || flow.store.Advance() != nil || flow.controller.Advance() != nil || flow.parent.Resume(2) != nil {
		return nil, ErrExecutionEpochOne
	}
	return flow, nil
}

func (flow *ExecutionEpochOne) AuthorA(ctx context.Context) (ExecutionAuthorResult, error) {
	if flow == nil || flow.epochs == nil || flow.controller == nil || flow.parent == nil {
		return ExecutionAuthorResult{}, ErrExecutionEpochOne
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.closed || flow.used || flow.authored {
		return ExecutionAuthorResult{}, ErrExecutionEpochOne
	}
	flow.authorStarted = time.Now()
	result, err := flow.epochs.author.AuthorNextOn(ctx, flow.controller, flow.parent, 7)
	flow.authored = err == nil && result.Completed
	return result, err
}

// Close cancels unused future lifetimes, rather than manufacturing their
// terminal EOFs. A live/unjoined server retains the borrowed input owners.
func (flow *ExecutionEpochOne) Close() error {
	if flow == nil {
		return nil
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.closed {
		return flow.closeErr
	}
	// Constructor failures already hold the input locks; only a used flow can
	// own a launched child and require this check.
	if flow.used {
		flow.epochs.author.mu.Lock()
		flow.epochs.mu.Lock()
		active := flow.epochs.active || flow.epochs.author.active
		if active && !flow.epochs.author.active && flow.retained != nil && !flow.retained.returnStarting && flow.epochs.author.borrowedBy == flow.retained && flow.retained.joinedEmpty() {
			flow.epochs.author.borrowedBy, flow.epochs.active = nil, false
			active = false
		}
		flow.epochs.mu.Unlock()
		flow.epochs.author.mu.Unlock()
		if active {
			return ErrExecutionEpochOne
		}
	}
	flow.closed = true
	if flow.parent != nil {
		if flow.parent.Close(context.Background()) != nil {
			flow.closeErr = ErrExecutionEpochOne
		}
	}
	if flow.store != nil {
		// Future unopened lifetimes deliberately make Close incomplete. Only
		// that exact expected refusal is harmless after every opened receiver
		// has supplied terminal EOF and the pre-close prefix is unlatched.
		prefix, err := flow.store.Snapshot()
		closeErr := flow.store.Close()
		if err != nil || prefix.Opened != prefix.TerminalEOF || closeErr != nil && closeErr != storeaccounting.ErrIncomplete {
			flow.closeErr = ErrExecutionEpochOne
		}
	}
	if flow.release != nil {
		flow.release()
	} else {
		flow.closeErr = ErrExecutionEpochOne
	}
	return flow.closeErr
}

type ExecutionEpochOneResult struct {
	RootStarted, RootJoined, SessionEmpty bool
	Accounting                            dispatchadmission.Snapshot
	Store                                 storeaccounting.WireSnapshot
	Attempts                              ExecutionAttemptObservation
	// Separate actual joined offline streams. Producer-local completeness is
	// not aggregate phase acceptance; epoch five must also be composed.
	BackupWork, RestoreWork ExecutionAttemptObservation
	IndexOffers             ExecutionIndexObservation
	ServerProcesses         ExecutionServerProcessObservation // Actual server roots only, not whole ceremony metrics.
	Inspection              []ExecutionPhaseInspection
}

type ExecutionEpochOneRun struct {
	mu                    sync.Mutex
	flow                  *ExecutionEpochOne
	epoch                 ExecutionEpochConfig
	control               *dispatchadmission.PhaseControl
	command               *exec.Cmd
	output                *checkoutCommandOutput // Read only after native Wait joins stdout/stderr copies.
	attemptInput          [32]byte
	processObservation    *epochProcessObservation
	processPrior          *ProcessObservation // Actual joined earlier root in checkpoint phase eight.
	stop, done            chan struct{}
	stopOnce              sync.Once
	healthUsed            bool
	healthCancel          context.CancelFunc
	healthDone            chan struct{}
	healthy               bool
	healthLimit           time.Duration
	healthDeadline        time.Time
	healthTimer           *time.Timer
	healthTimerDone       chan struct{}
	coldDeadline          time.Time
	coldUsed              bool
	coldCancel            context.CancelFunc
	coldDone              chan struct{}
	warmAllowed           bool
	warmUsed              bool
	warmCancel            context.CancelFunc
	warmDone              chan struct{}
	physicalAllowed       bool
	physicalUsed          bool
	physicalPinned        bool // Only the successful pin response plus fenced request tail sets this.
	physicalCancel        context.CancelFunc
	physicalDone          chan struct{}
	physicalLimit         time.Duration
	pinStarted, pinJoined time.Time
	physicalResult        epochRetentionObservation // Private native observation, not a measured receipt.
	phaseTimer            *time.Timer
	phaseDone             chan struct{}
	phaseDeadline         time.Time
	lifetimeDeadline      time.Time
	warmLimit             time.Duration
	cancelRun             context.CancelFunc
	warm                  bool // Only the completed coordinated handoff sets this.
	inspection            *executionEpochInspection
	stopping              bool
	result                ExecutionEpochOneResult
	err                   error
	nativeStopErr         error               // Private diagnostic, never a public evidence classification.
	stopDiagnostic        epochStopDiagnostic // Written only by finish; read after done.
	admissionFailure      atomic.Pointer[epochAdmissionFailure]
	retainParent          bool
	logicalUsed           bool
	logicalCancel         context.CancelFunc
	logicalDone           chan struct{}
	priorPhysical         *epochLogicalPrior // Detached actual authorities; never retains old output.
	returnStarting        bool               // Protected by flow.mu; retained source cannot be abandoned during authoring.
	returnStartCancel     context.CancelFunc
	returnStartDone       chan struct{}
	returnUsed            bool
	returnCancel          context.CancelFunc
	returnDone            chan struct{}
	staleAllowed          bool
	staleUsed             bool
	staleCancel           context.CancelFunc
	staleDone             chan struct{}
	priorLogical          *epochReturnPrior
	checkpointAllowed     bool
	checkpointUsed        bool
	checkpointCancel      context.CancelFunc
	checkpointDone        chan struct{}
	terminalRequested     bool
	terminalEntered       bool            // Irreversible PC attempted; ordinary shutdown is no longer valid.
	terminalContext       context.Context // Actual operation lifetime, not an asserted health flag.
	checkpointRecovery    *epochCheckpointRecoveryInput
	checkpointPrior       *AuthorityPhaseResult
	pressureAllowed       bool
	backupAllowed         bool
	backupUsed            bool
	backupRetired         bool
	backupComplete        bool
	backupStarted         bool
	backupJoined          bool
	backupSessionEmpty    bool
	backupManifestSHA256  string
	backupWork            ExecutionAttemptObservation
	restoreUsed           bool
	restoredStartUsed     bool
	restoreStarted        bool
	restoreJoined         bool
	restoreSessionEmpty   bool
	restoreComplete       bool
	restoreManifestSHA256 string
	backupCancel          context.CancelFunc
	backupDone            chan struct{}
	backupOutput          *epochBackupOutput
}

func (flow *ExecutionEpochOne) checkEpochTools(ctx context.Context, number uint64) (string, []dispatchadmission.ProductionToolBinding, []string, error) {
	author := flow.epochs.author
	epoch := flow.epochs.epochs[number-1]
	phebs, path, err := flow.phebs.Check(ctx, "phebs")
	zoekt, zoektPath, zoektErr := flow.zoekt.Check(ctx, "zoekt-git-index")
	surreal, surrealPath, surrealErr := flow.surreal.Check(ctx, "surreal")
	gitEnv, gitErr := author.request.Git.Environment(ctx, epoch.Home, epoch.Temporary)
	if err != nil || zoektErr != nil || surrealErr != nil || gitErr != nil || phebs.BuildVCSRevision != author.request.Builds.reference.source ||
		zoekt.Provenance != zoektOfferProvenance || zoekt.BuildRecipeSHA256 != zoektOfferRecipe(flow.plan.ToolPolicy, author.request.Builds.reference.source) {
		return "", nil, nil, ErrExecutionEpochOne
	}
	environment := externalToolEnvironment(epoch.Temporary)
	for index, value := range environment {
		if strings.HasPrefix(value, "HOME=") {
			environment[index] = "HOME=" + epoch.Home
		}
		if strings.HasPrefix(value, "PATH=") {
			environment[index] = "PATH=" + author.request.Git.Directory()
		}
	}
	tools := []dispatchadmission.ProductionToolBinding{
		{Role: "git", Path: author.gitPath, Environment: gitEnv},
		{Role: "surreal", Path: surrealPath, Environment: environment},
		{Role: "zoekt-git-index", Path: zoektPath, Environment: append(slices.Clone(gitEnv), dispatchadmission.IndexOfferEnvironment+"=v1", "ZOEKT_DISABLE_CATFILE_BATCH=true")},
	}
	environment = append(environment, dispatchadmission.ProductionEnvironment+"="+dispatchadmission.ProductionStoreSelector,
		"PHEBS_SURREAL="+surrealPath, "PHEBS_SURREAL_SHA256="+surreal.SHA256,
		"PHEBS_ZOEKT_GIT_INDEX="+zoektPath, "PHEBS_ZOEKT_GIT_INDEX_SHA256="+zoekt.SHA256,
		"PHEBS_T421_EXACT_READS=source-free-v1", "PHEBS_T4013_EXACT_REPORTS=source-free-v1")
	return path, tools, environment, nil
}

// Start launches exactly producer two in phase two. The twenty-minute and
// one-MiB output refusal ceilings are inherited from the small native serve
// rehearsal, not frozen full-cold budgets. PC01 reserves DrainOwners, Pause,
// and one terminal EOF pair; no phase transition is implemented by this slice.
func (flow *ExecutionEpochOne) Start(ctx context.Context) (_ *ExecutionEpochOneRun, retErr error) {
	return flow.start(ctx, epochOneStartup)
}

func (flow *ExecutionEpochOne) start(ctx context.Context, mode epochOneMode) (_ *ExecutionEpochOneRun, retErr error) {
	if flow == nil || flow.epochs == nil || flow.controller == nil || flow.parent == nil || flow.store == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrExecutionEpochOne
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.closed || flow.used || !flow.authored {
		return nil, ErrExecutionEpochOne
	}
	bounds, err := epochOneBounds(flow.plan, mode)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(bounds.lifetime)
	var coldDeadline time.Time
	if bounds.cold != 0 {
		if flow.authorStarted.IsZero() {
			return nil, ErrExecutionEpochOne
		}
		coldDeadline = flow.authorStarted.Add(bounds.cold)
		deadline = flow.authorStarted.Add(bounds.lifetime)
		if !time.Now().Before(coldDeadline) {
			return nil, ErrExecutionEpochOne
		}
	}
	flow.used = true
	runCtx, cancel := context.WithDeadline(ctx, deadline)
	run := &ExecutionEpochOneRun{flow: flow, stop: make(chan struct{}), done: make(chan struct{}),
		healthLimit: bounds.health, coldDeadline: coldDeadline, lifetimeDeadline: deadline,
		warmLimit: bounds.lifetime - bounds.cold - bounds.physical, physicalLimit: bounds.physical,
		cancelRun: cancel, warmAllowed: mode == epochOneColdWarm || mode == epochOnePhysicalB, physicalAllowed: mode == epochOnePhysicalB}
	launchCtx := runCtx
	if bounds.cold != 0 {
		run.mu.Lock()
		run.setPhaseDeadlineLocked(coldDeadline)
		run.mu.Unlock()
		var launchCancel context.CancelFunc
		launchCtx, launchCancel = context.WithDeadline(runCtx, coldDeadline)
		defer launchCancel()
	}
	launched, err := flow.launchEpoch(runCtx, launchCtx, cancel, run, bounds, 1)
	if launched == nil {
		run.stopPhaseDeadline()
		cancel()
	}
	return launched, err
}

// Callers hold flow.mu and select one of the five implemented epochs.
func (flow *ExecutionEpochOne) launchEpoch(runCtx, launchCtx context.Context, cancel context.CancelFunc, run *ExecutionEpochOneRun, bounds epochOneLimits, number uint64) (_ *ExecutionEpochOneRun, retErr error) {
	if number < 1 || number > 5 || number == 4 && (run.checkpointRecovery == nil || run.checkpointPrior == nil) {
		return nil, ErrExecutionEpochOne
	}
	producer, phase := uint32(number+1), uint32(2)
	switch number {
	case 2:
		phase = 5
	case 3:
		phase = 6
	case 4:
		phase = 8
	case 5:
		phase = 12
	}
	started := false
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	epochs.mu.Lock()
	borrowOK := author.borrowedBy == nil && !epochs.active
	if number > 1 {
		borrowOK = flow.retained != nil && author.borrowedBy == flow.retained && epochs.active && flow.retained.joinedEmpty()
	}
	if author.active || !borrowOK || author.next != min(int(number), 3) || number == 3 && author.previous == nil ||
		epochs.checkLocked(launchCtx, number) != nil || author.checkSource(launchCtx, author.previous) != nil {
		epochs.mu.Unlock()
		author.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	path, tools, environment, err := flow.checkEpochTools(launchCtx, number)
	epoch := epochs.epochs[number-1]
	if number == 3 {
		epoch.ReturnSourceCommit = author.previous.Result.Commit
	}
	run.epoch = epoch
	if err == nil && (epochs.released != number-1 || epochs.listeners[number-1].Close() != nil) {
		err = ErrExecutionEpochOne
	}
	if err == nil {
		epochs.listeners[number-1] = nil
		epochs.released = number
		epochs.active = true
		author.borrowedBy = run
		if number > 1 {
			flow.retained = nil
		}
	}
	epochs.mu.Unlock()
	author.mu.Unlock()
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	defer func() {
		if !started {
			author.mu.Lock()
			epochs.mu.Lock()
			author.borrowedBy, epochs.active = nil, false
			epochs.mu.Unlock()
			author.mu.Unlock()
		}
	}()
	view, err := flow.controller.ProducerLaunch(producer)
	if err != nil || view.Phase != phase {
		return nil, ErrExecutionEpochOne
	}
	epoch.SelectorHandoffCleanup = ""
	if flow.plan.SelectorHandoffCleanup != nil {
		epoch.SelectorHandoffCleanup = flow.plan.SelectorHandoffCleanup.Schema
	}
	epoch.LogicalStoreWork = ""
	if epoch.Epoch == 2 && flow.plan.LogicalStoreWork != nil {
		epoch.LogicalStoreWork = flow.plan.LogicalStoreWork.Schema
	}
	raw, err := epochSemanticInput(author.planSHA256, epoch, run.checkpointRecovery)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	var files [6]*os.File
	defer func() {
		for _, file := range files {
			if file != nil {
				_ = file.Close()
			}
		}
	}()
	for index := 0; index < len(files); index += 2 {
		files[index], files[index+1], err = dispatchadmission.NewPipe()
		if err != nil {
			return nil, ErrExecutionEpochOne
		}
	}
	input, err := adoptAuthorCustodySocket(files[4])
	files[4] = nil
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	defer func() { _ = input.Close() }() // Explicit success-path close is checked below.
	storeFile, storeConfig, err := flow.store.Open(producer)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	defer func() { _ = storeFile.Close() }() // Explicit post-Start close is checked below.
	output := &checkoutCommandOutput{remaining: bounds.outputBytes, cancel: cancel}
	run.output = output
	command := exec.Command(path, "serve", "--config", epoch.ConfigPath)
	command.Dir, command.Env = author.parent, environment
	command.Stdin, command.Stdout, command.Stderr = files[5], output, output
	if run.backupAllowed {
		run.backupOutput = &epochBackupOutput{remaining: bounds.outputBytes, server: output,
			backup: &checkoutCommandOutput{remaining: bounds.outputBytes, cancel: cancel}}
		command.Stdout, command.Stderr = run.backupOutput, run.backupOutput
	}
	command.ExtraFiles = []*os.File{files[1], files[3], storeFile}
	command.WaitDelay = 5 * time.Second
	prepareProductionSession(command)
	run.command = command
	processNames, err := epochProcessNames(path, tools)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	// Capture before admitted Start so its latency cannot extend readiness.
	// Arm only after an actual launch, before any bootstrap or stdin delivery.
	launchStarted := time.Now()
	handle, err := flow.parent.StartInPhase(launchCtx, phase, dispatchadmission.Site{ID: executionSiteServe, Role: executionRolePhebs, Persistent: true}, command)
	if err != nil {
		cancel()
		return nil, ErrExecutionEpochOne
	}
	started, run.result.RootStarted = true, true
	run.mu.Lock()
	run.setHealthDeadlineLocked(launchCtx, launchStarted)
	run.mu.Unlock()
	// Capture the actual native birth before the sole Wait can reap this PID.
	// Even capture failure retains the owned Start and follows normal cleanup.
	run.processObservation, err = startEpochProcessObservation(launchCtx, command.Process.Pid, phase, "phebs", processNames,
		func() { run.stopOnce.Do(func() { close(run.stop) }) })
	if err != nil {
		retErr = ErrExecutionEpochOne
	}
	if run.processObservation != nil && run.processPrior != nil {
		run.processObservation.mu.Lock()
		run.processObservation.prefix = run.processPrior
		run.processObservation.mu.Unlock()
	}
	waited := make(chan error, 1)
	go func() {
		waitErr := handle.Wait()
		run.processObservation.exited()
		waited <- waitErr
	}()
	for _, index := range []int{1, 3, 5} {
		if files[index].Close() != nil {
			retErr = ErrExecutionEpochOne
		}
		files[index] = nil
	}
	if storeFile.Close() != nil {
		retErr = ErrExecutionEpochOne
	}
	controlConfig := dispatchadmission.PhaseControlConfig{OwnerControl: true, Phases: []uint32{2, 3, 4}, InitialPhase: 2, MaximumPhases: 3, MaximumWireBytes: bounds.controlPairs * 2 * dispatchadmission.FrameBytes, Timeout: 30 * time.Second}
	switch number {
	case 2:
		controlConfig.Phases, controlConfig.InitialPhase, controlConfig.MaximumPhases = []uint32{5}, 5, 1
	case 3:
		controlConfig.Phases, controlConfig.InitialPhase, controlConfig.MaximumPhases = []uint32{6, 7, 8}, 6, 3
		if run.checkpointAllowed {
			controlConfig.TerminalPhase = 8
		}
	case 4:
		controlConfig.Phases, controlConfig.InitialPhase, controlConfig.MaximumPhases = []uint32{8, 9, 10, 11}, 8, 4
		controlConfig.BackupEndpointCarry = run.backupAllowed
	case 5:
		controlConfig.Phases, controlConfig.InitialPhase, controlConfig.MaximumPhases = []uint32{12, 13, 14}, 12, 3
	}
	bootstrap := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramPhebs, SemanticMode: dispatchadmission.ProductionSemanticV3,
		InputSHA256: sha256.Sum256(raw), Producer: view.Producer, Phase: phase, Limits: view.Limits, Control: controlConfig, Tools: tools, Store: &storeConfig}
	run.attemptInput = bootstrap.InputSHA256
	var served <-chan error
	if retErr == nil && dispatchadmission.SendProductionBootstrap(launchCtx, files[0], files[2], bootstrap) != nil {
		retErr = ErrExecutionEpochOne
	}
	if retErr == nil {
		run.control, err = dispatchadmission.NewPhaseControl(flow.controller.Context(), files[2], view.Producer.Binding, controlConfig)
		files[2] = nil
		if err != nil {
			retErr = ErrExecutionEpochOne
		} else {
			completion := make(chan error, 1)
			file := files[0]
			files[0] = nil
			go func() {
				completion <- flow.controller.ServeChecked(flow.controller.Context(), producer, command.Process.Pid, file, func(checkCtx context.Context, site dispatchadmission.Site) error {
					author.mu.Lock()
					defer author.mu.Unlock()
					epochs.mu.Lock()
					defer epochs.mu.Unlock()
					if err := epochs.checkLocked(checkCtx, number); err != nil {
						run.recordAdmissionFailure(checkCtx, site.ID, "epoch_config", err)
						return ErrExecutionEpochOne
					}
					_, _, _, err := flow.checkEpochTools(checkCtx, number)
					if err != nil || checkCtx.Err() != nil {
						run.recordAdmissionFailure(checkCtx, site.ID, "epoch_tools", err)
					}
					return err
				})
			}()
			served = completion
		}
	}
	if retErr == nil && writeAuthorCustodyRequest(launchCtx, input, raw) != nil {
		retErr = ErrExecutionEpochOne
	}
	// Bind parent stdin release to the same terminal result before handing
	// cleanup to finish. The deferred Close remains only a refusal-path guard.
	if input.Close() != nil {
		retErr = ErrExecutionEpochOne
	}
	go run.finish(runCtx, cancel, waited, served, retErr)
	return run, retErr
}

// The caller holds run.mu. This one-shot timer also covers an absent Health
// call; successful readiness or finish retires and joins it, like phaseTimer.
func (run *ExecutionEpochOneRun) setHealthDeadlineLocked(ctx context.Context, started time.Time) {
	deadline := started.Add(run.healthLimit)
	if !run.coldDeadline.IsZero() && run.coldDeadline.Before(deadline) {
		deadline = run.coldDeadline
	}
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	done := make(chan struct{})
	run.healthDeadline, run.healthTimerDone = deadline, done
	run.healthTimer = time.AfterFunc(time.Until(deadline), func() {
		defer close(done)
		run.mu.Lock()
		run.err = ErrExecutionEpochOne
		run.mu.Unlock()
		run.cancelRun()
		run.stopOnce.Do(func() { close(run.stop) })
	})
}

// A started timeout callback wins even if it is still waiting for run.mu.
func (run *ExecutionEpochOneRun) completeHealth(ctx context.Context) error {
	run.mu.Lock()
	defer run.mu.Unlock()
	if ctx.Err() != nil || run.stopping || run.err != nil || run.healthTimer == nil ||
		!time.Now().Before(run.healthDeadline) || !run.healthTimer.Stop() {
		return ErrExecutionEpochOne
	}
	close(run.healthTimerDone) // Stop won: no callback owns this completion.
	run.healthTimer, run.healthTimerDone = nil, nil
	if !time.Now().Before(run.healthDeadline) {
		return ErrExecutionEpochOne
	}
	select {
	case <-run.stop:
		return ErrExecutionEpochOne
	default:
	}
	run.healthy = true
	return nil
}

func (run *ExecutionEpochOneRun) stopHealthDeadline() {
	run.mu.Lock()
	timer, done := run.healthTimer, run.healthTimerDone
	run.healthTimer, run.healthTimerDone = nil, nil
	if timer != nil && !time.Now().Before(run.healthDeadline) {
		run.err = ErrExecutionEpochOne
	}
	run.mu.Unlock()
	if timer != nil {
		if timer.Stop() {
			close(done)
		} else {
			<-done
		}
	}
}

// Health waits only for TCP readiness (no repeated HTTP requests), then makes
// one authenticated, parent-token-bound health request. It proves no index or
// pipeline convergence. Its launch-relative deadline is never renewed. Any
// failure ends this one-shot run; no automatic retry.
func (run *ExecutionEpochOneRun) Health(ctx context.Context) (retErr error) {
	if run == nil || ctx == nil || run.stop == nil || run.done == nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.healthUsed || run.control == nil || run.stopping || run.err != nil ||
		run.healthDeadline.IsZero() || run.healthTimer == nil {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.stop:
		run.mu.Unlock()
		return ErrExecutionEpochOne
	default:
	}
	ctx, cancel := context.WithDeadline(ctx, run.healthDeadline)
	run.healthCancel, run.healthDone = cancel, make(chan struct{})
	run.healthUsed = true
	run.mu.Unlock()
	defer func() {
		defer close(run.healthDone)
		cancel()
		if retErr != nil {
			run.mu.Lock()
			run.err = ErrExecutionEpochOne
			run.mu.Unlock()
			run.stopOnce.Do(func() { close(run.stop) })
		}
	}()
	for {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp4", run.epoch.Listen)
		if err == nil {
			_ = conn.Close()
			break
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			run.stopOnce.Do(func() { close(run.stop) })
			return ErrExecutionEpochOne
		case <-run.done:
			timer.Stop()
			return ErrExecutionEpochOne
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+run.epoch.Listen+"/api/health", nil)
	if err != nil {
		return ErrExecutionEpochOne
	}
	request.Header.Set("Authorization", "Bearer "+run.epoch.APIKey)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, run.control.RequestToken())
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		run.stopOnce.Do(func() { close(run.stop) })
		return ErrExecutionEpochOne
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || ctx.Err() != nil || len(raw) > 4096 || response.StatusCode != http.StatusOK {
		run.stopOnce.Do(func() { close(run.stop) })
		return ErrExecutionEpochOne
	}
	return run.completeHealth(ctx)
}

func (run *ExecutionEpochOneRun) finish(ctx context.Context, cancel context.CancelFunc, waited, served <-chan error, failure error) {
	defer close(run.done)
	defer cancel()
	joined := false
	var waitErr error
	wake := "supplied_failure"
	if failure == nil {
		select {
		case <-run.stop:
			wake = "stop_requested"
		case <-ctx.Done():
			wake = "run_context"
			failure = ErrExecutionEpochOne
		case <-run.flow.controller.Context().Done():
			wake = "dispatch_context"
			failure = ErrExecutionEpochOne
		case waitErr = <-waited:
			wake = "native_wait"
			joined = true
			failure = ErrExecutionEpochOne
		}
	}
	// Observe before canceling phase work, signaling children or closing peers.
	// Concurrent failures can already coexist; the selected wake is not proof
	// of which subsystem failed first. Never acquire the HTTP reader lock here.
	diagnostic := run.observeStopDiagnostic(ctx, wake, failure, waitErr)
	// Stop and the lifetime deadline may both be ready. Cleanup still owns the
	// process, but whichever select arm won cannot turn expiry into success.
	if ctx.Err() != nil {
		failure = ErrExecutionEpochOne
	}
	run.mu.Lock()
	run.stopping = true
	healthCancel, healthDone := run.healthCancel, run.healthDone
	coldCancel, coldDone := run.coldCancel, run.coldDone
	warmCancel, warmDone := run.warmCancel, run.warmDone
	physicalCancel, physicalDone := run.physicalCancel, run.physicalDone
	logicalCancel, logicalDone := run.logicalCancel, run.logicalDone
	returnCancel, returnDone := run.returnCancel, run.returnDone
	staleCancel, staleDone := run.staleCancel, run.staleDone
	checkpointCancel, checkpointDone := run.checkpointCancel, run.checkpointDone
	backupCancel, backupDone := run.backupCancel, run.backupDone
	terminal, terminalRequested := run.terminalEntered, run.terminalRequested
	run.mu.Unlock()
	if backupCancel != nil {
		backupCancel()
	}
	if backupDone != nil {
		<-backupDone
	}
	run.stopHealthDeadline()
	if healthCancel != nil {
		healthCancel()
		<-healthDone
	}
	if coldCancel != nil {
		coldCancel()
		<-coldDone
	}
	if warmCancel != nil {
		warmCancel()
		<-warmDone
	}
	if physicalCancel != nil {
		physicalCancel()
		<-physicalDone
	}
	if logicalCancel != nil {
		logicalCancel()
		<-logicalDone
	}
	if returnCancel != nil {
		returnCancel()
		<-returnDone
	}
	if staleCancel != nil {
		staleCancel()
		<-staleDone
	}
	if checkpointCancel != nil && !terminalRequested {
		checkpointCancel()
	}
	if checkpointDone != nil {
		<-checkpointDone
	}
	run.stopPhaseDeadline()
	run.mu.Lock()
	warm := run.warm
	if run.err != nil {
		failure = ErrExecutionEpochOne
	}
	run.mu.Unlock()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()
	if !terminal && !joined && failure == nil && !run.backupRetired && (!warm && run.control.DrainOwners(stopCtx) != nil || run.control.Pause(stopCtx) != nil) {
		failure = ErrExecutionEpochOne
	}
	// A successful backup already joined producer ten while the retired server
	// cannot admit work. Keep the parent active in twelve: same-phase Resume is
	// forbidden, and a later restore may start only after this server join.
	if !terminal && (!run.backupRetired || !run.backupComplete || failure != nil) && (run.flow.parent.Pause(stopCtx) != nil || run.flow.controller.Fence() != nil) {
		failure = ErrExecutionEpochOne
	}
	// No sampler survives the owned signal/kill. Its last required live census
	// closes before intentional death, not after Wait has removed the root.
	_, processErr := run.processObservation.close()
	if processErr != nil {
		failure = ErrExecutionEpochOne
	}
	if !joined {
		run.processObservation.armStop()
	}
	if !terminal && !joined {
		if signalProductionStop(run.command.Process) != nil {
			failure = ErrExecutionEpochOne
		}
	}
	stopDeadline, _ := stopCtx.Deadline()
	var sessionEmpty bool
	var nativeStopErr error
	var death executionProcessDeath
	if terminal && terminalRequested && !joined && failure == nil {
		death, nativeStopErr = killExecutionProcessSession(run.terminalContext, run.command, waited)
		joined, sessionEmpty, waitErr = death.RootJoined, death.SessionEmpty, death.WaitErr
		// Invalid/dead operation contexts refuse before Kill; still own cleanup.
		if !joined || !sessionEmpty {
			joined, sessionEmpty, _ = finishExecutionProcessSession(run.command.Process.Pid, waited, joined, waitErr, stopDeadline)
		}
	} else if terminal {
		// Partial terminal setup cannot return to ordinary PC or SIGTERM.
		// Force cleanup only; no terminal admission/closure is fabricated.
		failure = ErrExecutionEpochOne
		killErr := t4013.KillPrivateProcessSession(run.command.Process.Pid)
		joined, sessionEmpty, nativeStopErr = finishExecutionProcessSession(run.command.Process.Pid, waited, joined, waitErr, stopDeadline)
		nativeStopErr = errors.Join(ErrExecutionEpochOne, killErr, nativeStopErr)
	} else {
		joined, sessionEmpty, nativeStopErr = finishExecutionProcessSession(run.command.Process.Pid, waited, joined, waitErr, stopDeadline)
	}
	if nativeStopErr != nil {
		failure = ErrExecutionEpochOne
	}
	if served != nil {
		joinCtx, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
		select {
		case err := <-served:
			diagnostic.DispatchReceiver = err
			if err != nil {
				failure = ErrExecutionEpochOne
			}
		case <-joinCtx.Done():
			diagnostic.DispatchJoin = joinCtx.Err()
			failure = ErrExecutionEpochOne
			run.flow.release()
			diagnostic.DispatchReceiver = <-served
		}
		joinCancel()
	}
	if terminal && (!terminalRequested || nativeStopErr != nil || death.ProcessState == nil || run.flow.controller.CloseHardDeath(run.producer(), death.ProcessState) != nil) {
		failure = ErrExecutionEpochOne
	}
	joinCtx, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
	diagnostic.StoreJoin = run.flow.store.Wait(joinCtx, run.producer())
	if diagnostic.StoreJoin != nil {
		failure = ErrExecutionEpochOne
	}
	joinCancel()
	if run.control != nil && run.control.Close() != nil {
		failure = ErrExecutionEpochOne
	}
	if !run.retainParent && run.flow.parent.Close(context.Background()) != nil {
		failure = ErrExecutionEpochOne
	}
	serverProcesses, processErr := run.processObservation.snapshot()
	if processErr != nil {
		failure = ErrExecutionEpochOne
	}
	result := ExecutionEpochOneResult{RootStarted: true, RootJoined: joined, SessionEmpty: sessionEmpty, ServerProcesses: serverProcesses,
		BackupWork: run.backupWork, RestoreWork: run.result.RestoreWork}
	// The retained installation also belongs to the separate backup session.
	// A joined server alone cannot release that custody or expose shared output.
	if run.backupStarted && (!run.backupJoined || !run.backupSessionEmpty) {
		result.SessionEmpty = false
		failure = ErrExecutionEpochOne
	}
	// Selectors have joined; inspection snapshot safety does not claim that
	// native process/output teardown succeeded (RootJoined remains separate).
	if run.inspection != nil {
		run.inspection.mu.Lock()
		result.Inspection = cloneInspectionEvidence(run.inspection.evidence.rows)
		run.inspection.mu.Unlock()
	}
	if !result.SessionEmpty {
		failure = ErrExecutionEpochOne
	}
	var err error
	result.Accounting, err = run.flow.controller.Snapshot()
	diagnostic.DispatchFinal = err
	if err != nil {
		failure = ErrExecutionEpochOne
	}
	result.Store, err = run.flow.store.Snapshot()
	diagnostic.StoreFinal = err
	if err != nil {
		failure = ErrExecutionEpochOne
	}
	author, epochs := run.flow.epochs.author, run.flow.epochs
	author.mu.Lock()
	epochs.mu.Lock()
	// A retained physical run keeps its existing borrow through the gap. The
	// one-shot successor or an explicit joined abandonment owns its release.
	epochs.active = !joined || !result.SessionEmpty || run.retainParent
	if !epochs.active {
		author.borrowedBy = nil
	}
	epochs.mu.Unlock()
	author.mu.Unlock()
	run.mu.Lock()
	prefixOK := epochClosedPrefix(ctx, result, run.physicalUsed, run.retainParent, run.epoch.Epoch == 2)
	if run.epoch.Epoch == 3 {
		prefixOK = epochReturnClosedPrefix(ctx, result)
	}
	if terminal || run.epoch.Epoch == 4 {
		prefixOK = epochCheckpointClosedPrefix(ctx, result, terminal)
	}
	if run.backupRetired {
		prefixOK = run.backupComplete && epochBackupClosedPrefix(ctx, result)
	}
	if run.epoch.Epoch == 5 {
		prefixOK = epochRestoredClosedPrefix(ctx, result)
	}
	if ctx.Err() != nil || terminal && (run.terminalContext == nil || run.terminalContext.Err() != nil) {
		failure = ErrExecutionEpochOne
	}
	if run.err != nil || !prefixOK {
		failure = ErrExecutionEpochOne
	}
	if run.finishAttemptObservation(ctx, &result, death, failure) != nil {
		failure = ErrExecutionEpochOne
	}
	if failure != nil && run.backupRetired && run.backupComplete {
		// Final sampling, native/protocol joins and parsing can fail after the
		// earlier successful-backup fence exemption. Never leave admission open.
		run.mu.Unlock()
		run.fenceFailedBackup(stopCtx)
		run.mu.Lock()
	}
	run.result, run.err = result, failure
	run.nativeStopErr = nativeStopErr
	diagnostic.NativeStop = nativeStopErr
	diagnostic.AdmissionAfterJoin = run.admissionFailure.Load()
	if result.RootJoined && (!run.backupStarted || run.backupJoined) && run.output != nil {
		diagnostic.Output = run.output.err // The existing native join owns copier EOF.
	}
	run.stopDiagnostic = diagnostic
	run.mu.Unlock()
}

func (run *ExecutionEpochOneRun) Stop(ctx context.Context) (ExecutionEpochOneResult, error) {
	if run == nil || run.stop == nil || run.done == nil || run.flow == nil || ctx == nil {
		return ExecutionEpochOneResult{}, ErrExecutionEpochOne
	}
	run.stopOnce.Do(func() { close(run.stop) })
	// StartReturnA has already joined the old server before its author runs.
	// Its separate join keeps Stop from releasing that retained source early.
	run.flow.mu.Lock()
	cancel, done := run.returnStartCancel, run.returnStartDone
	run.flow.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-ctx.Done():
			return ExecutionEpochOneResult{RootStarted: true}, ErrExecutionEpochOne
		}
	}
	return run.Wait(ctx)
}

func (run *ExecutionEpochOneRun) Wait(ctx context.Context) (ExecutionEpochOneResult, error) {
	if run == nil || ctx == nil || run.done == nil || run.flow == nil {
		return ExecutionEpochOneResult{}, ErrExecutionEpochOne
	}
	select {
	case <-run.done:
		run.mu.Lock()
		defer run.mu.Unlock()
		result := run.result
		result.Accounting.Phases = slices.Clone(result.Accounting.Phases)
		for index := range result.Accounting.Phases {
			result.Accounting.Phases[index].Roles = slices.Clone(result.Accounting.Phases[index].Roles)
		}
		result.Accounting.Producers = slices.Clone(result.Accounting.Producers)
		result.Store.Store.Phases = slices.Clone(result.Store.Store.Phases)
		result.Store.Store.Producers = slices.Clone(result.Store.Store.Producers)
		result.ServerProcesses = cloneServerProcessObservation(result.ServerProcesses)
		result.Inspection = cloneInspectionEvidence(result.Inspection)
		return result, run.err
	case <-ctx.Done():
		return ExecutionEpochOneResult{RootStarted: true}, ErrExecutionEpochOne
	}
}

func epochOneClosedPrefix(ctx context.Context, result ExecutionEpochOneResult) bool {
	return epochOneClosedPrefixForMode(ctx, result, false)
}

func epochOneClosedPrefixForMode(ctx context.Context, result ExecutionEpochOneResult, physical bool) bool {
	return epochClosedPrefix(ctx, result, physical, false, false)
}

func epochClosedPrefix(ctx context.Context, result ExecutionEpochOneResult, physical, retained, logical bool) bool {
	opened := 1
	if logical {
		opened = 2
	}
	if ctx == nil || ctx.Err() != nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || result.Store.Opened != opened || result.Store.TerminalEOF != opened {
		return false
	}
	root, server, store, author := false, false, false, !physical
	rootAttempts := uint64(2)
	if physical {
		rootAttempts++
	}
	if logical {
		rootAttempts, author = 4, false
	}
	second, secondStore := !logical, !logical
	for _, producer := range result.Accounting.Producers {
		if producer.Producer == executionRootProducer {
			root = producer.Attached && producer.Closed != retained && producer.Active == 0 && producer.Ordinal == rootAttempts
		}
		if producer.Producer == 2 {
			server = producer.Attached && producer.Closed && producer.Active == 0
		}
		if producer.Producer == 3 && logical {
			second = producer.Attached && producer.Closed && producer.Active == 0
		}
		if (physical || logical) && producer.Producer == 8 {
			author = producer.Attached && producer.Closed && producer.Active == 0 && producer.Ordinal == 3
		}
	}
	for _, producer := range result.Store.Store.Producers {
		if producer.Producer == 3 && logical {
			secondStore = producer.Attached && producer.Closed && producer.Calls == 0 && producer.Transactions == 0
		}
		if producer.Producer == 2 {
			store = producer.Attached && producer.Closed && producer.Calls == 0 && producer.Transactions == 0
		}
	}
	return root && server && store && author && second && secondStore
}
