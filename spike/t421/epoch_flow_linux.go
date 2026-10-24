//go:build linux

package t421

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"sync"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// LinuxExecutionEpochRequest selects genuine owners, never launch paths or
// caller assertions. Preparation must already have joined and retired scratch.
type LinuxExecutionEpochRequest struct {
	Epochs                *LinuxExecutionEpochConfigCustody
	Phebs, Zoekt, Surreal *LinuxExecutionToolCustody
}

// LinuxExecutionEpochFlow retains one whole loan until all execution and cleanup
// join. A deadline returns unavailable state; Close must still join this owner.
// Never copy this owner. This scope supplies the implemented three-epoch path, not ceremony admission.
type LinuxExecutionEpochFlow struct {
	mu                sync.Mutex
	request           LinuxExecutionEpochRequest
	lifetime          context.Context
	cancel            context.CancelFunc
	settlement        context.Context
	settleCancel      context.CancelFunc
	ready             chan error
	done              chan struct{}
	requests          chan linuxEpochOperation
	core              *ExecutionEpochOne
	held              *linuxEpochHeldFlow
	err               error
	step, failureStep string
	admitted          bool
	closing           bool
}
type LinuxExecutionEpochRun struct {
	flow *LinuxExecutionEpochFlow
	run  *ExecutionEpochOneRun
}
type linuxEpochOperation struct {
	ctx   context.Context
	kind  uint8
	run   *ExecutionEpochOneRun
	reply chan linuxEpochReply
}
type linuxEpochReply struct {
	author ExecutionAuthorResult
	run    *ExecutionEpochOneRun
	result ExecutionEpochOneResult
	err    error
}

const (
	linuxEpochAuthorA uint8 = iota + 1
	linuxEpochStart
	linuxEpochHealth
	linuxEpochWarm
	linuxEpochWarmObserve
	linuxEpochPhysical
	linuxEpochLogicalStart
	linuxEpochLogical
	linuxEpochReturnStart
	linuxEpochReturn
	linuxEpochStop
	linuxEpochWait
)

func PrepareLinuxExecutionEpochFlow(ctx context.Context, request LinuxExecutionEpochRequest) (*LinuxExecutionEpochFlow, error) {
	if ctx == nil || ctx.Err() != nil || request.Epochs == nil || request.Epochs.author == nil || request.Phebs == nil || request.Zoekt == nil || request.Surreal == nil {
		return nil, ErrExecutionEpochOne
	}
	author, epochs := request.Epochs.author, request.Epochs
	author.mu.Lock()
	epochs.mu.Lock()
	valid := author.borrowedBy == nil && author.active == nil && author.next == 0 && epochs.borrowedBy == nil && !epochs.active && epochs.released == 0 && epochs.checkLocked(ctx, 1) == nil && author.request.Builds != nil && author.request.Git != nil && author.request.Author != nil && author.request.Plan != nil && request.Phebs.referenceInputs == author.request.Builds && request.Zoekt.referenceInputs == author.request.Builds
	if !valid {
		epochs.mu.Unlock()
		author.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	lifetime, cancel := context.WithCancel(context.Background())
	settlement, settleCancel := context.WithCancel(context.Background())
	flow := &LinuxExecutionEpochFlow{request: request, lifetime: lifetime, cancel: cancel, settlement: settlement, settleCancel: settleCancel, ready: make(chan error, 1), done: make(chan struct{}), requests: make(chan linuxEpochOperation)}
	author.borrowedBy = flow
	epochs.active = true
	epochs.borrowedBy = flow
	epochs.mu.Unlock()
	author.mu.Unlock()
	go flow.work()
	select {
	case err := <-flow.ready:
		return flow, err
	case <-ctx.Done():
		cancel()
		return flow, ErrExecutionEpochOne
	}
}

func (flow *LinuxExecutionEpochFlow) work() {
	defer close(flow.done)
	defer flow.settleCancel() // Also covers refusal before any control was made.
	flow.step = "loans"
	author := flow.request.Epochs.author
	held := &linuxEpochHeldFlow{builds: author.request.Builds, git: author.request.Git, epochs: flow.request.Epochs, author: author, flow: flow, controllerPID: os.Getpid(), tools: [4]*LinuxExecutionToolCustody{author.request.Author, flow.request.Phebs, flow.request.Zoekt, flow.request.Surreal}}
	err := held.withLoans(func() error {
		flow.step = "initial-census"
		rows, e := t4013.ObserveProcessTreeRecords(flow.lifetime, held.controllerPID)
		if e != nil || len(rows) != 1 {
			return ErrExecutionEpochOne
		}
		held.controllerStart = rows[0].StartIdentity
		flow.step = "plan"
		var raw []byte
		raw, e = io.ReadAll(executionInputReader{flow.lifetime, io.NewSectionReader(held.plan, 0, MaxPlanBytes+1)})
		var plan Plan
		if e != nil || len(raw) > MaxPlanBytes || SHA256(raw) != author.planSHA256 || json.Unmarshal(raw, &plan) != nil || !processAccountingPlanSemantics(plan.Schema) || validatePlan(plan, &plan.Revisions) != nil {
			return ErrExecutionEpochOne
		}
		flow.step = "coordinator"
		core, e := newLinuxEpochCoordinator(flow, held, plan)
		if e != nil {
			return e
		}
		flow.mu.Lock()
		flow.core, flow.held = core, held
		flow.mu.Unlock()
		flow.step = "preparation-memory-release"
		if e = held.releasePreparationMemory(flow.lifetime); e != nil {
			return &linuxEpochOperationFailure{e}
		}
		flow.step = "resource-admission"
		held.resources, e = admitLinuxEpochResources(flow.lifetime, held, flow.cancel)
		if e != nil {
			return &linuxEpochOperationFailure{e}
		}
		flow.step = "trace-owner"
		held.resources.trace, e = newLinuxEpochTraceOwner(held.resources)
		if e != nil {
			return &linuxEpochOperationFailure{e}
		}
		flow.step = "whole-observation"
		if e = held.startWholeObservation(flow.lifetime); e != nil {
			return &linuxEpochOperationFailure{e}
		}
		flow.mu.Lock()
		flow.admitted = true
		flow.mu.Unlock()
		flow.step = "ready"
		flow.ready <- nil
		for {
			select {
			case operation := <-flow.requests:
				reply := flow.execute(operation)
				operation.reply <- reply
				if reply.err != nil {
					flow.mu.Lock()
					flow.err = reply.err
					flow.mu.Unlock()
					held.resources.failDetail("operation-return/" + flow.step)
					return &linuxEpochOperationFailure{reply.err}
				}
			case <-flow.lifetime.Done():
				flow.mu.Lock()
				closing := flow.closing
				flow.mu.Unlock()
				if closing {
					return nil
				}
				return &linuxEpochOperationFailure{ErrExecutionEpochOne}
			}
		}
	})
	flow.mu.Lock()
	flow.err = err
	if err != nil && flow.failureStep == "" {
		flow.failureStep = flow.step
	}
	flow.mu.Unlock()
	select {
	case flow.ready <- ErrExecutionEpochOne:
	default:
	}
	author.mu.Lock()
	flow.request.Epochs.mu.Lock()
	author.borrowedBy = nil
	flow.request.Epochs.active = false
	flow.request.Epochs.borrowedBy = nil
	flow.request.Epochs.mu.Unlock()
	author.mu.Unlock()
}

// Retire unused preparation heap once, with all real loan owners still rooted.
// This is not admission credit; the following observation must independently fit.
func (held *linuxEpochHeldFlow) releasePreparationMemory(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || held == nil {
		return ErrExecutionEpochOne
	}
	debug.FreeOSMemory() // Includes GC; synchronous and not context-interruptible.
	runtime.KeepAlive(held)
	if ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

func newLinuxEpochCoordinator(owner *LinuxExecutionEpochFlow, held *linuxEpochHeldFlow, plan Plan) (_ *ExecutionEpochOne, retErr error) {
	if owner == nil || owner.lifetime == nil || owner.lifetime.Err() != nil || owner.settlement == nil || owner.settlement.Err() != nil || owner.settleCancel == nil {
		return nil, ErrExecutionEpochOne
	}
	adapter := &linuxEpochCoordinator{held: held}
	author := &ExecutionAuthorCustody{native: adapter, parent: held.author.roots[0].path, planSHA256: held.author.planSHA256, expected: held.author.projection.expected, deadlines: held.author.projection.deadlines, phases: held.author.projection.phases}
	epochs := &ExecutionEpochConfigCustody{native: &linuxEpochConfigAdapter{adapter}, author: author, epochs: held.epochs.epochs, listeners: held.epochs.listeners, queryCatalog: held.epochs.queryCatalog, parsedConfigs: held.epochs.parsedConfigs, logicalChanges: held.epochs.logicalChanges}
	core := &ExecutionEpochOne{native: adapter, epochs: epochs, plan: plan, release: owner.cancel}
	adapter.core = core
	adapter.recipe = issueLinuxEpochLegacyRecipe(adapter)
	defer func() {
		if retErr != nil {
			_ = core.Close()
		}
	}()
	workspace := held.workspace
	core.workspaceBytes = custodybytes.NewBorrowed(workspace.file, workspace.path, workspace.info, workspace.identity.FSID)
	var bindings [executionProducerCount][32]byte
	for i := range bindings {
		if _, e := rand.Read(bindings[i][:]); e != nil {
			return nil, ErrExecutionEpochOne
		}
	}
	da, e := executionDispatchConfig(plan, bindings)
	sa, wire, se := executionStoreConfig(plan, bindings)
	if e != nil || se != nil {
		return nil, ErrExecutionEpochOne
	}
	core.controller, e = dispatchadmission.New(owner.settlement, da)
	if e != nil {
		return nil, ErrExecutionEpochOne
	}
	core.parent, e = core.controller.NewLocalProducer(owner.settlement, executionRootProducer)
	if e != nil {
		return nil, ErrExecutionEpochOne
	}
	store, e := storeaccounting.New(owner.settlement, sa)
	if e != nil {
		return nil, ErrExecutionEpochOne
	}
	core.store, e = storeaccounting.NewTransport(owner.settlement, store, wire)
	if e != nil {
		return nil, ErrExecutionEpochOne
	}
	if core.parent.Pause(owner.lifetime) != nil || core.controller.Fence() != nil || core.parent.Checkpoint(owner.lifetime) != nil || core.store.Fence() != nil || core.store.Advance() != nil || core.controller.Advance() != nil || core.parent.Resume(2) != nil {
		return nil, ErrExecutionEpochOne
	}
	return core, nil
}

func (flow *LinuxExecutionEpochFlow) execute(operation linuxEpochOperation) (reply linuxEpochReply) {
	if operation.ctx == nil || operation.ctx.Err() != nil {
		reply.err = ErrExecutionEpochOne
		return
	}
	run := operation.run
	if run != nil && run.flow != flow.core {
		reply.err = ErrExecutionEpochOne
		return
	}
	link := newLinuxEpochOperationLink(operation.ctx, flow.lifetime)
	operation.ctx = link.ctx
	defer func() {
		if reply.run != nil {
			// Persistent server contexts descend from this launch context. Keep
			// its cancellation link until that real server has joined.
			flow.held.operationLinks = append(flow.held.operationLinks, link)
		} else {
			link.close()
		}
	}()
	switch operation.kind {
	case linuxEpochAuthorA:
		flow.step = "author-a"
		reply.author, reply.err = flow.core.AuthorA(operation.ctx)
	case linuxEpochStart:
		flow.step = "start-physical"
		reply.run, reply.err = flow.core.StartPhysicalB(operation.ctx)
	case linuxEpochHealth:
		flow.step = "health"
		reply.err = run.Health(operation.ctx)
	case linuxEpochWarm:
		flow.step = "cold-to-warm"
		reply.err = run.ColdToWarm(operation.ctx)
	case linuxEpochWarmObserve:
		flow.step = "warm"
		reply.err = run.ObserveWarm(operation.ctx)
	case linuxEpochPhysical:
		flow.step = "physical-b"
		reply.err = run.PhysicalB(operation.ctx)
	case linuxEpochLogicalStart:
		flow.step = "logical-start"
		reply.run, reply.err = run.StartLogicalB(operation.ctx)
	case linuxEpochLogical:
		flow.step = "logical"
		reply.err = run.LogicalB(operation.ctx)
	case linuxEpochReturnStart:
		flow.step = "return-start"
		reply.run, reply.err = run.StartReturnA(operation.ctx)
	case linuxEpochReturn:
		flow.step = "return"
		reply.err = run.ReturnA(operation.ctx)
	case linuxEpochStop:
		flow.step = "stop"
		reply.result, reply.err = run.Stop(operation.ctx)
	case linuxEpochWait:
		flow.step = "wait"
		reply.result, reply.err = run.Wait(operation.ctx)
	default:
		reply.err = ErrExecutionEpochOne
	}
	return
}

// An operation stops immediately on caller or flow cancellation while the
// separate settlement lifetime remains available to genuine Wait/EOF owners.
type linuxEpochOperationLink struct {
	ctx    context.Context
	cancel context.CancelFunc
	stop   func() bool
	done   chan struct{}
	once   sync.Once
}

func newLinuxEpochOperationLink(ctx, lifetime context.Context) *linuxEpochOperationLink {
	operation, cancel := context.WithCancel(ctx)
	link := &linuxEpochOperationLink{ctx: operation, cancel: cancel, done: make(chan struct{})}
	link.stop = context.AfterFunc(lifetime, func() { defer close(link.done); cancel() })
	return link
}

func (link *linuxEpochOperationLink) close() {
	link.once.Do(func() {
		if !link.stop() {
			<-link.done
		}
		link.cancel()
	})
}
func (flow *LinuxExecutionEpochFlow) call(ctx context.Context, kind uint8, run *ExecutionEpochOneRun) linuxEpochReply {
	failure := linuxEpochReply{err: ErrExecutionEpochOne}
	if flow == nil || ctx == nil || ctx.Err() != nil {
		return failure
	}
	flow.mu.Lock()
	valid := flow.lifetime != nil && flow.lifetime.Err() == nil && flow.admitted && !flow.closing && flow.err == nil && flow.core != nil
	flow.mu.Unlock()
	if !valid {
		return failure
	}
	operation := linuxEpochOperation{ctx: ctx, kind: kind, run: run, reply: make(chan linuxEpochReply, 1)}
	select {
	case flow.requests <- operation:
	case <-ctx.Done():
		return failure
	case <-flow.done:
		return failure
	}
	select {
	case reply := <-operation.reply:
		return reply
	case <-ctx.Done():
		select {
		case reply := <-operation.reply:
			return reply
		default:
		}
		flow.cancel()
		return failure
	case <-flow.done:
		select {
		case reply := <-operation.reply:
			return reply
		default:
			return failure
		}
	}
}
func (flow *LinuxExecutionEpochFlow) AuthorA(ctx context.Context) (ExecutionAuthorResult, error) {
	r := flow.call(ctx, linuxEpochAuthorA, nil)
	return r.author, r.err
}
func (flow *LinuxExecutionEpochFlow) StartPhysicalB(ctx context.Context) (*LinuxExecutionEpochRun, error) {
	r := flow.call(ctx, linuxEpochStart, nil)
	if r.run == nil {
		return nil, r.err
	}
	return &LinuxExecutionEpochRun{flow, r.run}, r.err
}
func (run *LinuxExecutionEpochRun) operation(ctx context.Context, kind uint8) linuxEpochReply {
	if run == nil {
		return linuxEpochReply{err: ErrExecutionEpochOne}
	}
	return run.flow.call(ctx, kind, run.run)
}
func (run *LinuxExecutionEpochRun) Health(ctx context.Context) error {
	return run.operation(ctx, linuxEpochHealth).err
}
func (run *LinuxExecutionEpochRun) ColdToWarm(ctx context.Context) error {
	return run.operation(ctx, linuxEpochWarm).err
}
func (run *LinuxExecutionEpochRun) ObserveWarm(ctx context.Context) error {
	return run.operation(ctx, linuxEpochWarmObserve).err
}
func (run *LinuxExecutionEpochRun) PhysicalB(ctx context.Context) error {
	return run.operation(ctx, linuxEpochPhysical).err
}
func (run *LinuxExecutionEpochRun) LogicalB(ctx context.Context) error {
	return run.operation(ctx, linuxEpochLogical).err
}
func (run *LinuxExecutionEpochRun) ReturnA(ctx context.Context) error {
	return run.operation(ctx, linuxEpochReturn).err
}
func (run *LinuxExecutionEpochRun) StartLogicalB(ctx context.Context) (*LinuxExecutionEpochRun, error) {
	r := run.operation(ctx, linuxEpochLogicalStart)
	if r.run == nil {
		return nil, r.err
	}
	return &LinuxExecutionEpochRun{run.flow, r.run}, r.err
}
func (run *LinuxExecutionEpochRun) StartReturnA(ctx context.Context) (*LinuxExecutionEpochRun, error) {
	r := run.operation(ctx, linuxEpochReturnStart)
	if r.run == nil {
		return nil, r.err
	}
	return &LinuxExecutionEpochRun{run.flow, r.run}, r.err
}
func (run *LinuxExecutionEpochRun) Stop(ctx context.Context) (ExecutionEpochOneResult, error) {
	r := run.operation(ctx, linuxEpochStop)
	return r.result, r.err
}
func (run *LinuxExecutionEpochRun) Wait(ctx context.Context) (ExecutionEpochOneResult, error) {
	r := run.operation(ctx, linuxEpochWait)
	return r.result, r.err
}
func (flow *LinuxExecutionEpochFlow) Close(ctx context.Context) error {
	if flow == nil {
		return nil
	}
	if ctx == nil {
		return ErrExecutionEpochOne
	}
	flow.mu.Lock()
	if flow.lifetime == nil || flow.cancel == nil || flow.done == nil {
		flow.mu.Unlock()
		return ErrExecutionEpochOne
	}
	flow.closing = true
	flow.cancel()
	flow.mu.Unlock()
	select {
	case <-flow.done:
		flow.mu.Lock()
		defer flow.mu.Unlock()
		return flow.err
	case <-ctx.Done():
		return ErrExecutionEpochOne
	}
}
