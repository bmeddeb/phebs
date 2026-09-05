package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

var (
	ErrRunnerControlBusy     = errors.New("lifecycle control already has an operation")
	ErrRunnerControlStopped  = errors.New("lifecycle controlled runner is unavailable")
	ErrRunnerControlState    = errors.New("lifecycle control operation is not valid in this state")
	ErrRunnerControlDeadline = errors.New("lifecycle control requires a request deadline")
)

// RunnerControl is an optional, single-runner exact-mode rendezvous. It owns
// no goroutine and admits at most one operation, with no waiting-caller queue.
// This is mechanical coordination, not proof of request or tool admission.
// A newly attached runner starts parked; Resume is explicit, not readiness.
// Each operation requires a caller deadline; cancellation is cooperative and
// never waives joining an already executing native callback.
// Callers must Park before draining ordinary owners. Every Drive and pressure
// read must have separate complete request ownership while those owners are
// paused. The collector must not also be registered as a runner reporter.
type RunnerControl struct {
	commands chan *runnerCommand
	done     chan struct{}
	attached atomic.Bool
	busy     atomic.Bool
}

func NewRunnerControl() *RunnerControl {
	return &RunnerControl{commands: make(chan *runnerCommand), done: make(chan struct{})}
}

type runnerOperation uint8

const (
	runnerPark runnerOperation = iota + 1
	runnerResume
	runnerDriveNormal
	runnerDriveFresh
	runnerDriveRecovery
	runnerPressure80
	runnerPressure90
	runnerPressure75
	runnerPressureNormal
)

type runnerCommand struct {
	ctx       context.Context
	operation runnerOperation
	collector *CycleCollector
	fence     time.Time
	reply     chan runnerReply
}

type runnerReply struct {
	cycle      CycleObservation
	pressure80 Pressure80Observation
	pressure90 Pressure90Observation
	pressure75 Pressure75Observation
	normal     Pressure75RecoveryObservation
	err        error
}

// Park acknowledges only between complete owner/capacity/report turns. It
// interrupts an idle timer but cannot preempt a callback that ignores context.
func (control *RunnerControl) Park(ctx context.Context) error {
	return control.call(ctx, runnerPark, nil, time.Time{}).err
}

// Resume preserves the previously selected ordinary deadline and cycle state.
// It does not reopen the caller's ordinary-owner or HTTP admission gates.
func (control *RunnerControl) Resume(ctx context.Context) error {
	return control.call(ctx, runnerResume, nil, time.Time{}).err
}

// DriveNormal synchronously arms a fresh collector, drives the same runner
// until its terminal owner/capacity pair, and leaves the runner parked. The
// first turn wakes immediately; subsequent pending turns use the lesser of
// the selected native delay and the existing backlog delay, not hourly idle.
// No turn exceeds the collector's admitted bound. Cancellation after handoff
// joins the executing callback cooperatively before returning.
func (control *RunnerControl) DriveNormal(ctx context.Context, collector *CycleCollector) (CycleObservation, error) {
	result := control.call(ctx, runnerDriveNormal, collector, time.Time{})
	return result.cycle, result.err
}

func (control *RunnerControl) DriveFresh(ctx context.Context, collector *CycleCollector) (CycleObservation, error) {
	result := control.call(ctx, runnerDriveFresh, collector, time.Time{})
	return result.cycle, result.err
}

func (control *RunnerControl) DrivePressure75Recovery(ctx context.Context, collector *CycleCollector, ballastFence time.Time) (CycleObservation, error) {
	result := control.call(ctx, runnerDriveRecovery, collector, ballastFence)
	return result.cycle, result.err
}

// The pressure operations require Park and use only this runner's existing
// Gate. They execute no owner turn and leave the runner parked on completion.
func (control *RunnerControl) ReadPressure80Collect(ctx context.Context, collector *CycleCollector, ballastFence time.Time) (Pressure80Observation, error) {
	result := control.call(ctx, runnerPressure80, collector, ballastFence)
	return result.pressure80, result.err
}

func (control *RunnerControl) ReadPressure90Refusal(ctx context.Context, collector *CycleCollector, ballastFence time.Time) (Pressure90Observation, error) {
	result := control.call(ctx, runnerPressure90, collector, ballastFence)
	return result.pressure90, result.err
}

func (control *RunnerControl) ReadPressure75Refusal(ctx context.Context, collector *CycleCollector, ballastFence time.Time) (Pressure75Observation, error) {
	result := control.call(ctx, runnerPressure75, collector, ballastFence)
	return result.pressure75, result.err
}

func (control *RunnerControl) ReadPressure75Normal(ctx context.Context, collector *CycleCollector) (Pressure75RecoveryObservation, error) {
	result := control.call(ctx, runnerPressureNormal, collector, time.Time{})
	return result.normal, result.err
}

func (control *RunnerControl) call(ctx context.Context, operation runnerOperation, collector *CycleCollector, fence time.Time) runnerReply {
	if ctx == nil || control == nil || control.commands == nil || control.done == nil {
		return runnerReply{err: ErrRunnerControlStopped}
	}
	if err := ctx.Err(); err != nil {
		return runnerReply{err: err}
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return runnerReply{err: ErrRunnerControlDeadline}
	}
	if !control.busy.CompareAndSwap(false, true) {
		return runnerReply{err: ErrRunnerControlBusy}
	}
	defer control.busy.Store(false)
	command := &runnerCommand{ctx: ctx, operation: operation, collector: collector, fence: fence, reply: make(chan runnerReply, 1)}
	select {
	case <-ctx.Done():
		return runnerReply{err: ctx.Err()}
	case <-control.done:
		return runnerReply{err: ErrRunnerControlStopped}
	case control.commands <- command:
	}
	// After handoff, do not return while the runner still owns this operation.
	// Cancellation reaches its context; joining remains cooperative native I/O.
	select {
	case result := <-command.reply:
		return result
	case <-control.done:
		select {
		case result := <-command.reply:
			return result
		default:
			return runnerReply{err: ErrRunnerControlStopped}
		}
	}
}

func (control *RunnerControl) attach() bool {
	return control.commands != nil && control.done != nil && control.attached.CompareAndSwap(false, true)
}

func (control *RunnerControl) next(ctx context.Context, due time.Time, parked bool) (*runnerCommand, bool) {
	select {
	case <-ctx.Done():
		return nil, false
	case command := <-control.commands:
		return command, true
	default:
	}
	if parked {
		select {
		case <-ctx.Done():
			return nil, false
		case command := <-control.commands:
			return command, true
		}
	}
	delay := time.Until(due)
	if delay <= 0 {
		return nil, true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, false
	case command := <-control.commands:
		return command, true
	case <-timer.C:
		return nil, true
	}
}

func (control *RunnerControl) execute(runnerCtx context.Context, command *runnerCommand, parked *bool,
	state *runnerState, due *time.Time, controller *Controller, gate *Gate, report Reporter, reportCapacity CapacityReporter,
) (keepRunning bool) {
	ctx, cancel := context.WithCancel(command.ctx)
	stop := context.AfterFunc(runnerCtx, cancel)
	if runnerCtx.Err() != nil {
		cancel()
	}
	defer func() { stop(); cancel() }()
	// Keep ordinary panic propagation, but an unwound native callback must
	// never publish a nil-error control acknowledgement before runner death.
	result := runnerReply{err: ErrRunnerControlStopped}
	defer func() {
		if err := ctx.Err(); err != nil {
			result = runnerReply{err: err}
			keepRunning = false
		}
		command.reply <- result
	}()
	if err := ctx.Err(); err != nil {
		result.err = err
		return true
	}
	switch command.operation {
	case runnerPark:
		*parked = true
		result.err = nil
		return true
	case runnerResume:
		if !*parked {
			result.err = ErrRunnerControlState
		} else {
			*parked = false
			result.err = nil
		}
		return true
	}
	if !*parked || gate == nil || command.collector == nil || !command.collector.matchesController(controller) {
		result.err = ErrRunnerControlState
		return true
	}
	switch command.operation {
	case runnerDriveNormal, runnerDriveFresh, runnerDriveRecovery:
		result.cycle, result.err = state.drive(ctx, command, due, controller, gate, report, reportCapacity)
		// A canceled partial turn cannot silently resume with uncertain native
		// cadence/cursor state. End this controlled runner; its owner must stop.
		return ctx.Err() == nil
	case runnerPressure80:
		result.pressure80, result.err = command.collector.ReadPressure80Collect(ctx, gate, command.fence)
	case runnerPressure90:
		result.pressure90, result.err = command.collector.ReadPressure90Refusal(ctx, gate, command.fence)
	case runnerPressure75:
		result.pressure75, result.err = command.collector.ReadPressure75Refusal(ctx, gate, command.fence)
	case runnerPressureNormal:
		result.normal, result.err = command.collector.ReadPressure75Normal(ctx, gate)
	default:
		result.err = ErrRunnerControlState
	}
	return true
}

func (collector *CycleCollector) matchesController(controller *Controller) bool {
	if len(collector.owners) != len(controller.owners) {
		return false
	}
	for index, name := range collector.owners {
		if name != controller.owners[index].name {
			return false
		}
	}
	return true
}

func (state *runnerState) drive(ctx context.Context, command *runnerCommand, due *time.Time, controller *Controller,
	gate *Gate, report Reporter, reportCapacity CapacityReporter,
) (CycleObservation, error) {
	collector := command.collector
	var done <-chan cycleObservationResult
	var err error
	if command.operation == runnerDriveRecovery {
		done, err = collector.armPressure75Recovery(gate, command.fence)
	} else {
		done, err = collector.arm(command.operation == runnerDriveFresh)
	}
	if err != nil {
		return CycleObservation{}, err
	}
	for {
		if !state.turn(ctx, controller, gate, report, reportCapacity, collector) || ctx.Err() != nil {
			collector.cancel()
			return CycleObservation{}, ctx.Err()
		}
		*due = time.Now().Add(state.delay)
		// Check after the actual report/capacity tail, before admitting another
		// turn. An unavailable cycle at its limit never executes a cap+1 turn.
		collector.mu.Lock()
		if !collector.finished && collector.turns == collector.maxTurns {
			collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle observation exceeded its bound"))
		}
		collector.mu.Unlock()
		select {
		case result := <-done:
			return cloneCycleObservation(result.value), result.err
		default:
		}
		timer := time.NewTimer(min(state.delay, state.backlogDelay))
		select {
		case <-ctx.Done():
			timer.Stop()
			collector.cancel()
			return CycleObservation{}, ctx.Err()
		case <-timer.C:
		}
	}
}
