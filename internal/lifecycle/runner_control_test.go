package lifecycle

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

type controlledTestOwner struct {
	name string
	work func(context.Context) OwnerResult
}

func (owner controlledTestOwner) Name() string { return owner.name }

func (owner controlledTestOwner) Sweep(ctx context.Context, _ time.Time, _ string, _ Limits) OwnerResult {
	return owner.work(ctx)
}

type controlledTestRunner struct {
	ctx        context.Context
	cancel     context.CancelFunc
	control    *RunnerControl
	controller *Controller
	gate       *Gate
	owners     *dispatchadmission.Owners
	done       chan struct{}
	used       atomic.Int64
	probes     atomic.Int64
}

func newControlledTestRunner(t *testing.T, owners []Owner, reportCapacity CapacityReporter) *controlledTestRunner {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	runner := &controlledTestRunner{ctx: ctx, cancel: cancel, control: NewRunnerControl(), done: make(chan struct{})}
	var err error
	runner.controller, err = NewController(newMemoryCursorStore(), owners...)
	if err != nil {
		t.Fatal(err)
	}
	runner.owners, err = dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	runner.used.Store(700)
	runner.gate = NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		runner.probes.Add(1)
		used := runner.used.Load()
		return Capacity{TotalBytes: 1000, UsedBytes: used, AvailableBytes: 1000 - used}, nil
	})
	go func() {
		defer close(runner.done)
		RunWithControl(ctx, runner.controller, runner.gate, time.Hour, time.Millisecond,
			func(OwnerResult) {}, reportCapacity, runner.owners, runner.control)
	}()
	t.Cleanup(func() { cancel(); <-runner.done })
	if err := runner.control.Park(ctx); err != nil {
		t.Fatal(err)
	}
	return runner
}

func testControlCollector(t *testing.T, owners []Owner, limit CycleTurnLimit) *CycleCollector {
	t.Helper()
	collector, err := NewCycleCollector(owners, limit)
	if err != nil {
		t.Fatal(err)
	}
	return collector
}

func TestRunnerControlDriveArmsBeforeFirstTurnAndStopsAtPair(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "fresh"}[fresh], func(t *testing.T) {
			var calls []string
			owners := []Owner{recordingOwner{name: "a", calls: &calls}, recordingOwner{name: "b", calls: &calls}}
			runner := newControlledTestRunner(t, owners, nil)
			if runner.probes.Load() != 0 {
				t.Fatal("initial Park admitted a turn")
			}
			if err := runner.owners.Pause(runner.ctx); err != nil {
				t.Fatal(err)
			}
			collector := testControlCollector(t, owners, 2)
			var result CycleObservation
			var err error
			if fresh {
				result, err = runner.control.DriveFresh(runner.ctx, collector)
			} else {
				result, err = runner.control.DriveNormal(runner.ctx, collector)
			}
			if err != nil || result.OwnerTurns != 2 || len(result.Owners) != 2 || runner.probes.Load() != 2 {
				t.Fatalf("drive = %+v / %v, probes %d", result, err, runner.probes.Load())
			}
			if !slices.Equal(calls, []string{"a:", "b:"}) {
				t.Fatalf("calls = %v", calls)
			}
			if err := runner.control.Park(runner.ctx); err != nil {
				t.Fatal(err)
			}
			if runner.probes.Load() != 2 {
				t.Fatal("terminal pair admitted another probe")
			}
			if _, err := runner.control.DriveNormal(runner.ctx, collector); err == nil {
				t.Fatal("rearmed used collector")
			}
			if runner.probes.Load() != 2 {
				t.Fatal("rearm refusal admitted a turn")
			}
			if err := runner.owners.Resume(); err != nil {
				t.Fatal(err)
			}
			if err := runner.control.Resume(runner.ctx); err != nil {
				t.Fatal(err)
			}
			// The completed native cycle still selected hourly idle. Park must
			// interrupt that timer without resetting it or advancing a new turn.
			if err := runner.control.Park(runner.ctx); err != nil {
				t.Fatal(err)
			}
			if runner.probes.Load() != 2 {
				t.Fatal("Resume reset completed cycle")
			}
		})
	}
}

func TestRunnerControlParkWaitsForActiveCapacityTail(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	var calls []string
	owners := []Owner{recordingOwner{name: "a", calls: &calls}, recordingOwner{name: "b", calls: &calls}}
	var reports atomic.Int32
	runner := newControlledTestRunner(t, owners, func(Capacity, error) {
		if reports.Add(1) == 1 {
			close(started)
			<-release
		}
	})
	t.Cleanup(finish)
	if err := runner.control.Resume(runner.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-runner.ctx.Done():
		t.Fatal(runner.ctx.Err())
	}
	parked := make(chan error, 1)
	go func() { parked <- runner.control.Park(runner.ctx) }()
	for !runner.control.busy.Load() {
		runtime.Gosched()
	}
	select {
	case err := <-parked:
		t.Fatalf("Park skipped report tail: %v", err)
	default:
	}
	finish()
	if err := <-parked; err != nil {
		t.Fatal(err)
	}
	if err := runner.owners.Pause(runner.ctx); err != nil {
		t.Fatal(err)
	}
	if runner.probes.Load() != 1 {
		t.Fatal("Park did not stop after a")
	}
	collector := testControlCollector(t, owners, 3)
	result, err := runner.control.DriveNormal(runner.ctx, collector)
	if err != nil || result.OwnerTurns != 3 || !slices.Equal(calls, []string{"a:", "b:", "a:x", "b:x"}) {
		t.Fatalf("preserved cursor / fresh cycle = %+v / %v / %v", result, err, calls)
	}
}

func TestRunnerControlDriveCancellationJoinsActiveWork(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	owners := []Owner{controlledTestOwner{name: "a", work: func(context.Context) OwnerResult {
		close(started)
		<-release
		return OwnerResult{Completeness: Exact}
	}}}
	runner := newControlledTestRunner(t, owners, nil)
	t.Cleanup(finish)
	ctx, cancel := context.WithCancel(runner.ctx)
	defer cancel()
	result := make(chan error, 1)
	collector := testControlCollector(t, owners, 1)
	go func() { _, err := runner.control.DriveNormal(ctx, collector); result <- err }()
	select {
	case <-started:
	case <-runner.ctx.Done():
		t.Fatal(runner.ctx.Err())
	}
	if err := runner.control.Park(runner.ctx); !errors.Is(err, ErrRunnerControlBusy) {
		t.Fatalf("busy = %v", err)
	}
	cancel()
	select {
	case err := <-result:
		t.Fatalf("cancellation left executing callback: %v", err)
	default:
	}
	finish()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	<-runner.done
	if runner.probes.Load() != 0 {
		t.Fatal("canceled partial turn emitted capacity")
	}
	if err := runner.control.Resume(runner.ctx); !errors.Is(err, ErrRunnerControlStopped) {
		t.Fatalf("dead runner = %v", err)
	}
}

func TestRunnerControlUnacceptedCancellationDoesNotStartRunner(t *testing.T) {
	control := NewRunnerControl()
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if err := control.Park(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Park = %v", err)
	}
	if control.busy.Load() || control.attached.Load() {
		t.Fatal("canceled waiter retained admission")
	}
}

func TestRunnerControlRequiresDeadlineAndSingleAttachment(t *testing.T) {
	control := NewRunnerControl()
	if err := control.Park(context.Background()); !errors.Is(err, ErrRunnerControlDeadline) {
		t.Fatalf("unbounded operation = %v", err)
	}
	if control.busy.Load() || control.attached.Load() {
		t.Fatal("unbounded operation retained admission")
	}
	if !control.attach() || control.attach() {
		t.Fatal("control did not enforce exactly one runner attachment")
	}
}

func TestRunnerControlAlreadyCanceledRunnerAdmitsNoTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	runnerCtx, cancelRunner := context.WithCancel(ctx)
	cancelRunner()
	var turns atomic.Int32
	owners := []Owner{controlledTestOwner{name: "a", work: func(context.Context) OwnerResult {
		turns.Add(1)
		return OwnerResult{Completeness: Exact}
	}}}
	controller, err := NewController(newMemoryCursorStore(), owners...)
	if err != nil {
		t.Fatal(err)
	}
	command := &runnerCommand{ctx: ctx, operation: runnerDriveNormal,
		collector: testControlCollector(t, owners, 1), reply: make(chan runnerReply, 1)}
	parked := true
	state := runnerState{idleInterval: time.Hour, backlogDelay: time.Second}
	var due time.Time
	if NewRunnerControl().execute(runnerCtx, command, &parked, &state, &due, controller, NewGate(t.TempDir()), nil, nil) {
		t.Fatal("canceled runner remained available")
	}
	if result := <-command.reply; !errors.Is(result.err, context.Canceled) || turns.Load() != 0 {
		t.Fatalf("canceled runner admitted work: %v / %d", result.err, turns.Load())
	}
}

func TestRunnerControlPanicCannotAcknowledgeSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var turns atomic.Int32
	owners := []Owner{controlledTestOwner{name: "a", work: func(context.Context) OwnerResult {
		turns.Add(1)
		return OwnerResult{Completeness: Exact}
	}}}
	controller, err := NewController(newMemoryCursorStore(), owners...)
	if err != nil {
		t.Fatal(err)
	}
	control := NewRunnerControl()
	gate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		return Capacity{TotalBytes: 1000, UsedBytes: 700, AvailableBytes: 300}, nil
	})
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		RunWithControl(ctx, controller, gate, time.Hour, time.Second, nil,
			func(Capacity, error) { panic("native report panic") }, nil, control)
	}()
	result, err := control.DriveNormal(ctx, testControlCollector(t, owners, 1))
	if !errors.Is(err, ErrRunnerControlStopped) || result.Schema != "" {
		t.Fatalf("panic acknowledged success: %+v / %v", result, err)
	}
	select {
	case value := <-panicked:
		if value != "native report panic" {
			t.Fatalf("ordinary panic changed: %v", value)
		}
	case <-ctx.Done():
		t.Fatal("panicking controlled runner did not unwind")
	}
	if turns.Load() != 1 {
		t.Fatalf("native owner turns = %d", turns.Load())
	}
}

func TestRunnerControlCanceledTerminalCapacityDoesNotPass(t *testing.T) {
	var calls []string
	owners := []Owner{recordingOwner{name: "a", calls: &calls}}
	var cancelRead context.CancelFunc
	runner := newControlledTestRunner(t, owners, func(Capacity, error) { cancelRead() })
	ctx, cancel := context.WithCancel(runner.ctx)
	defer cancel()
	cancelRead = cancel
	collector := testControlCollector(t, owners, 1)
	result, err := runner.control.DriveNormal(ctx, collector)
	if !errors.Is(err, context.Canceled) || result.Schema != "" {
		t.Fatalf("canceled terminal observation = %+v / %v", result, err)
	}
	<-runner.done
	if runner.probes.Load() != 1 || collector.observation.Schema != "" {
		t.Fatal("canceled terminal pair passed or admitted extra work")
	}
}

func TestRunnerControlDrivePreservesNativeRecoveryState(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var calls []string
	owners := []Owner{recordingOwner{name: "a", calls: &calls}, recordingOwner{name: "b", calls: &calls}}
	controller, err := NewController(newMemoryCursorStore(), owners...)
	if err != nil {
		t.Fatal(err)
	}
	used := int64(900)
	probes := 0
	gate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		probes++
		return Capacity{TotalBytes: 1000, UsedBytes: used, AvailableBytes: 1000 - used}, nil
	})
	state := runnerState{idleInterval: time.Hour, backlogDelay: DefaultBacklogDelay}
	if !state.turn(ctx, controller, gate, nil, nil, nil) || !state.pressureRecoveryCycle || state.delay != DefaultPressureRecoveryDelay {
		t.Fatal("ordinary pressure turn did not select recovery cadence")
	}
	used = 700
	if !state.turn(ctx, controller, gate, nil, nil, nil) || !state.pressureRecoveryCycle || state.delay != DefaultPressureRecoveryDelay {
		t.Fatal("partial normal cycle incorrectly cleared native recovery")
	}
	collector := testControlCollector(t, owners, 2)
	command := &runnerCommand{ctx: ctx, operation: runnerDriveNormal, collector: collector}
	due := time.Now()
	result, err := state.drive(ctx, command, &due, controller, gate, nil, nil)
	if err != nil || result.OwnerTurns != 2 || state.pressureRecoveryCycle || state.delay != time.Hour || state.cycleStarted {
		t.Fatalf("same state after controlled clean cycle = %+v / %v / %+v", result, err, state)
	}
	if probes != 4 || !slices.Equal(calls, []string{"a:", "b:", "a:x", "b:x"}) {
		t.Fatalf("native recovery work = %v / %d", calls, probes)
	}
	priorDue := due
	if _, err := state.drive(ctx, command, &due, controller, gate, nil, nil); err == nil || due != priorDue || probes != 4 {
		t.Fatal("rejected rearm changed cadence or performed native work")
	}
}

func TestRunnerControlUnavailableCycleNeverExecutesOverflowTurn(t *testing.T) {
	var turns atomic.Int32
	owners := []Owner{controlledTestOwner{name: "a", work: func(context.Context) OwnerResult {
		turns.Add(1)
		return OwnerResult{Completeness: Unavailable, Err: errors.New("unavailable")}
	}}}
	runner := newControlledTestRunner(t, owners, nil)
	if _, err := runner.control.DriveNormal(runner.ctx, testControlCollector(t, owners, 2)); err == nil {
		t.Fatal("unavailable cycle passed")
	}
	if turns.Load() != 2 || runner.probes.Load() != 2 {
		t.Fatalf("overflow work %d / %d", turns.Load(), runner.probes.Load())
	}
}

func TestRunnerControlClockBackstepCannotHideActualTurns(t *testing.T) {
	for _, recoverClock := range []bool{false, true} {
		t.Run(map[bool]string{false: "always_before_fence", true: "later_current"}[recoverClock], func(t *testing.T) {
			var turns atomic.Int32
			owners := []Owner{controlledTestOwner{name: "a", work: func(context.Context) OwnerResult {
				turns.Add(1)
				return OwnerResult{Completeness: Exact}
			}}}
			runner := newControlledTestRunner(t, owners, nil)
			collector := testControlCollector(t, owners, 2)
			fence := time.Now().Add(time.Hour)
			collector.now = func() time.Time { return fence }
			clockCalls := 0
			runner.controller.now = func() time.Time {
				clockCalls++
				if recoverClock && clockCalls > 1 {
					return fence
				}
				return fence.Add(-time.Second)
			}
			result, err := runner.control.DriveNormal(runner.ctx, collector)
			if err == nil || result.Schema != "" || turns.Load() != 1 || runner.probes.Load() != 1 {
				t.Fatalf("clock backstep undercounted work: %+v / %v / turns %d / probes %d",
					result, err, turns.Load(), runner.probes.Load())
			}
			if _, err := runner.control.DriveNormal(runner.ctx, collector); err == nil || turns.Load() != 1 {
				t.Fatal("failed controlled observation resumed after clock recovery")
			}
		})
	}
}

func TestRunnerControlPressureUsesSameGateWithoutExtraTurns(t *testing.T) {
	var calls []string
	owners := []Owner{recordingOwner{name: "a", calls: &calls}}
	runner := newControlledTestRunner(t, owners, nil)
	collector := testControlCollector(t, owners, 4)
	if _, err := runner.control.DriveNormal(runner.ctx, collector); err != nil {
		t.Fatal(err)
	}
	runner.used.Store(800)
	if result, err := runner.control.ReadPressure80Collect(runner.ctx, collector, time.Now()); err != nil || result.Capacity.Pressure != PressureCollect {
		t.Fatalf("80 = %+v / %v", result, err)
	}
	runner.used.Store(900)
	if result, err := runner.control.ReadPressure90Refusal(runner.ctx, collector, time.Now()); err != nil || result.Capacity.Pressure != PressureRefuse {
		t.Fatalf("90 = %+v / %v", result, err)
	}
	runner.used.Store(750)
	if result, err := runner.control.ReadPressure75Refusal(runner.ctx, collector, time.Now()); err != nil || result.Capacity.Pressure != PressureRefuse {
		t.Fatalf("75 = %+v / %v", result, err)
	}
	if runner.probes.Load() != 4 || len(calls) != 1 {
		t.Fatalf("pressure added a turn: %v / %d", calls, runner.probes.Load())
	}
	runner.used.Store(700)
	result, err := runner.control.DrivePressure75Recovery(runner.ctx, collector, time.Now())
	// First turn supplies the required preceding exact-normal capacity; the
	// second is the clean cycle. No Await goroutine or extra manual probe.
	if err != nil || result.OwnerTurns != 2 {
		t.Fatalf("recovery = %+v / %v", result, err)
	}
	if result, err := runner.control.ReadPressure75Normal(runner.ctx, collector); err != nil || result.Capacity.Pressure != PressureNormal {
		t.Fatalf("normal = %+v / %v", result, err)
	}
	if runner.probes.Load() != 7 || len(calls) != 3 {
		t.Fatalf("terminal probes = %d / turns %v", runner.probes.Load(), calls)
	}
}

func TestRunnerControlRejectsUnparkedAndWrongInventory(t *testing.T) {
	var calls []string
	owners := []Owner{recordingOwner{name: "a", calls: &calls}}
	runner := newControlledTestRunner(t, owners, nil)
	wrong := testControlCollector(t, []Owner{recordingOwner{name: "other", calls: &calls}}, 1)
	if _, err := runner.control.DriveNormal(runner.ctx, wrong); !errors.Is(err, ErrRunnerControlState) {
		t.Fatalf("inventory = %v", err)
	}
	if _, err := runner.control.DriveFresh(runner.ctx, nil); !errors.Is(err, ErrRunnerControlState) {
		t.Fatalf("nil = %v", err)
	}
	if runner.probes.Load() != 0 {
		t.Fatal("refusal admitted work")
	}
	if err := runner.control.Resume(runner.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.control.ReadPressure75Normal(runner.ctx, testControlCollector(t, owners, 1)); !errors.Is(err, ErrRunnerControlState) {
		t.Fatalf("unparked = %v", err)
	}
}

func TestRunnerControlNilPathAddsNoAdmissionAllocation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	controller, err := NewController(newMemoryCursorStore(), controlledTestOwner{name: "a", work: func(context.Context) OwnerResult { return OwnerResult{} }})
	if err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(100, func() {
		RunWithControl(ctx, controller, nil, time.Hour, time.Second, nil, nil, nil, nil)
	})
	if allocations != 0 {
		t.Fatalf("nil canceled/no-op path allocated %v", allocations)
	}
}
