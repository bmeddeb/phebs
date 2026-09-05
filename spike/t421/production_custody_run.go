package t421

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
)

const productionServeAttempts = 10_000

// ExecutionProductionResult is a source-free, controller-committed prefix.
// Start and native join are separate observations, not inferred from attempts.
// This small serve path supplies no semantic-readiness or full-flow receipt.
type ExecutionProductionResult struct {
	RootStarted  bool
	RootJoined   bool
	SessionEmpty bool
	Accounting   dispatchadmission.Snapshot
}

// ExecutionProductionRun retains one launched server and exactly one native
// Wait. Stop drains work owners before pausing dispatch and fencing accounting.
// The caller's context and a twenty-minute local ceiling also request Stop;
// neither cancellation nor an accounting failure silently releases custody.
type ExecutionProductionRun struct {
	mu         sync.Mutex
	custody    *ExecutionProductionCustody
	controller *dispatchadmission.Controller
	control    *dispatchadmission.PhaseControl
	command    *exec.Cmd
	output     *checkoutCommandOutput
	stop       chan struct{}
	stopOnce   sync.Once
	done       chan struct{}
	result     ExecutionProductionResult
	err        error
}

func productionServeConfig(binding [32]byte) dispatchadmission.Config {
	// Two pairs per attempt (permission and settlement), at most one carry,
	// then terminal close and one unused EOF reservation. These small-rehearsal
	// refusal ceilings are not accepted frozen corpus/flow figures.
	return dispatchadmission.Config{
		Limits: dispatchadmission.Limits{Producers: 1, Sites: 16, Roles: 4, Phases: 1,
			ActivePerProducer: 64, Attempts: productionServeAttempts,
			WireBytes: (3*productionServeAttempts + 2) * 2 * dispatchadmission.FrameBytes, AckTimeout: 5 * time.Second},
		Producers: []dispatchadmission.Producer{{ID: 1, Binding: binding, Sites: dispatchadmission.ProductionSites()}},
		Phases: []dispatchadmission.Phase{{ID: 1, Roles: []dispatchadmission.RoleBudget{
			{Role: dispatchadmission.RoleGit, Attempts: productionServeAttempts - 6},
			{Role: dispatchadmission.RoleSurreal, Attempts: 2},
			{Role: dispatchadmission.RoleZoekt, Attempts: 4},
			{Role: dispatchadmission.RoleCompatibility},
		}}},
	}
}

// StartServe creates only the fixed ordinary Phebs serve command. It consumes
// this custody once, owns both private bootstrap endpoints and closes the parent
// copies of both inherited descriptors immediately after successful native Start.
// A nonnil run on error still owns cleanup; Stop/Wait expose its stopped prefix.
func (custody *ExecutionProductionCustody) StartServe(ctx context.Context) (_ *ExecutionProductionRun, retErr error) {
	if custody == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrExecutionProductionCustody
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if custody.used || custody.active || custody.check(ctx, dispatchadmission.Site{}) != nil {
		return nil, ErrExecutionProductionCustody
	}
	custody.used = true
	var binding [32]byte
	if _, err := rand.Read(binding[:]); err != nil || binding == ([32]byte{}) {
		return nil, ErrExecutionProductionCustody
	}
	config := productionServeConfig(binding)
	lifetime, releaseLifetime := context.WithCancel(context.Background())
	controller, err := dispatchadmission.New(lifetime, config)
	if err != nil {
		releaseLifetime()
		return nil, ErrExecutionProductionCustody
	}
	admission, childAdmission, err := dispatchadmission.NewPipe()
	if err != nil {
		releaseLifetime()
		return nil, ErrExecutionProductionCustody
	}
	phase, childPhase, err := dispatchadmission.NewPipe()
	if err != nil {
		_ = admission.Close()
		_ = childAdmission.Close()
		releaseLifetime()
		return nil, ErrExecutionProductionCustody
	}
	// Ownership transfers explicitly below; these local copies close on every
	// construction failure, including failure before a receiver is attached.
	defer func() {
		for _, file := range []*os.File{admission, childAdmission, phase, childPhase} {
			if file != nil {
				_ = file.Close()
			}
		}
	}()
	runCtx, cancelRun := context.WithTimeout(ctx, 20*time.Minute)
	run := &ExecutionProductionRun{custody: custody, controller: controller, stop: make(chan struct{}), done: make(chan struct{})}
	run.output = &checkoutCommandOutput{remaining: 1 << 20, cancel: cancelRun}
	run.command = exec.Command(custody.phebsPath, "serve", "--config", custody.configPath)
	run.command.Dir = custody.parent
	run.command.Env = append([]string(nil), custody.environment...)
	run.command.ExtraFiles = []*os.File{childAdmission, childPhase}
	run.command.Stdout, run.command.Stderr = run.output, run.output
	run.command.WaitDelay = 5 * time.Second
	prepareProductionSession(run.command)
	if runCtx.Err() != nil {
		cancelRun()
		releaseLifetime()
		return nil, ErrExecutionProductionCustody
	}
	if err := run.command.Start(); err != nil {
		cancelRun()
		releaseLifetime()
		return nil, ErrExecutionProductionCustody
	}
	run.result.RootStarted = true
	custody.active = true
	waited := make(chan error, 1)
	go func() { waited <- run.command.Wait() }()
	closeErr := childAdmission.Close()
	closePhaseErr := childPhase.Close()
	childAdmission, childPhase = nil, nil
	controlConfig := dispatchadmission.PhaseControlConfig{Phases: []uint32{1}, InitialPhase: 1, MaximumPhases: 1,
		OwnerControl: true, MaximumWireBytes: 4 * 2 * dispatchadmission.FrameBytes, Timeout: 30 * time.Second}
	bootstrap := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramPhebs,
		Producer: config.Producers[0], Phase: 1, Limits: config.Limits, Control: controlConfig, Tools: custody.tools}
	var served <-chan error
	if closeErr != nil || closePhaseErr != nil || dispatchadmission.SendProductionBootstrap(ctx, admission, phase, bootstrap) != nil {
		retErr = ErrExecutionProductionCustody
	} else {
		run.control, err = dispatchadmission.NewPhaseControl(lifetime, phase, binding, controlConfig)
		phase = nil // NewPhaseControl adopts even on refusal.
		if err != nil {
			retErr = ErrExecutionProductionCustody
		} else {
			completion := make(chan error, 1)
			serverFile := admission
			admission = nil
			go func() {
				completion <- controller.ServeChecked(lifetime, 1, run.command.Process.Pid, serverFile, func(checkCtx context.Context, site dispatchadmission.Site) error {
					custody.mu.Lock()
					defer custody.mu.Unlock()
					if custody.check(checkCtx, site) != nil {
						custody.err = ErrExecutionProductionCustody
						return custody.err
					}
					return nil
				})
			}()
			served = completion
		}
	}
	go run.finish(runCtx, cancelRun, releaseLifetime, waited, served, retErr)
	return run, retErr
}

func (run *ExecutionProductionRun) finish(runCtx context.Context, cancelRun, releaseLifetime context.CancelFunc, waited, served <-chan error, failure error) {
	defer close(run.done)
	defer cancelRun()
	defer releaseLifetime()
	joined := false
	var waitErr error
	if failure == nil {
		select {
		case <-run.stop:
		case <-runCtx.Done():
			failure = ErrExecutionProductionCustody
		case <-run.controller.Context().Done():
			failure = ErrExecutionProductionCustody
		case waitErr = <-waited:
			joined, failure = true, ErrExecutionProductionCustody
		}
	}
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStop()
	if !joined && run.control != nil && failure == nil {
		if run.control.DrainOwners(stopCtx) != nil || run.control.Pause(stopCtx) != nil {
			failure = ErrExecutionProductionCustody
		}
	}
	if run.controller.Fence() != nil {
		failure = ErrExecutionProductionCustody
	}
	if served == nil {
		_ = run.controller.CancelUnused(1)
	}
	if !joined {
		if signalProductionStop(run.command.Process) != nil {
			failure = ErrExecutionProductionCustody
		}
		select {
		case waitErr = <-waited:
			joined = true
		case <-stopCtx.Done():
			failure = ErrExecutionProductionCustody
			_ = run.command.Process.Kill()
			joinTimer := time.NewTimer(6 * time.Second)
			select {
			case waitErr = <-waited:
				joined = true
			case <-joinTimer.C:
			}
			joinTimer.Stop()
		}
	}
	if waitErr != nil || !joined {
		failure = ErrExecutionProductionCustody
	}
	if served != nil {
		joinTimer := time.NewTimer(5 * time.Second)
		select {
		case err := <-served:
			if err != nil {
				failure = ErrExecutionProductionCustody
			}
		case <-joinTimer.C:
			failure = ErrExecutionProductionCustody
			releaseLifetime()
			// Socket cancellation does not interrupt a synchronous custody
			// metadata callback. Join it cooperatively and retain custody.
			<-served
		}
		joinTimer.Stop()
	}
	if run.control != nil && run.control.Close() != nil {
		failure = ErrExecutionProductionCustody
	}
	sessionEmpty := false
	if joined {
		members, err := t4013.PrivateProcessSessionMembers(run.command.Process.Pid)
		sessionEmpty = err == nil && members == 0
	}
	if !sessionEmpty {
		failure = ErrExecutionProductionCustody
	}
	prefix, err := run.controller.Snapshot()
	if err != nil || !prefix.Complete {
		failure = ErrExecutionProductionCustody
	}
	run.mu.Lock()
	run.result.RootJoined, run.result.SessionEmpty, run.result.Accounting = joined, sessionEmpty, prefix
	run.err = failure
	run.mu.Unlock()
	run.custody.mu.Lock()
	run.custody.active = !joined || !sessionEmpty
	if failure != nil {
		run.custody.err = ErrExecutionProductionCustody
	}
	run.custody.mu.Unlock()
}

// Stop is idempotent. A caller deadline bounds only its wait: retained cleanup
// continues after bounded stop/join attempts, without releasing custody. A
// synchronous metadata callback can prolong the cooperative receiver join.
func (run *ExecutionProductionRun) Stop(ctx context.Context) (ExecutionProductionResult, error) {
	if run == nil || ctx == nil || run.stop == nil || run.done == nil || run.controller == nil {
		return ExecutionProductionResult{}, ErrExecutionProductionCustody
	}
	run.stopOnce.Do(func() { close(run.stop) })
	return run.Wait(ctx)
}

func (run *ExecutionProductionRun) Wait(ctx context.Context) (ExecutionProductionResult, error) {
	if run == nil || ctx == nil || run.done == nil || run.controller == nil {
		return ExecutionProductionResult{}, ErrExecutionProductionCustody
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
		return result, run.err
	case <-ctx.Done():
		prefix, _ := run.controller.Snapshot()
		return ExecutionProductionResult{RootStarted: true, Accounting: prefix}, ErrExecutionProductionCustody
	}
}
