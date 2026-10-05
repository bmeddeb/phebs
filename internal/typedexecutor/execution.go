package typedexecutor

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

// Outcome contains no source or raw worker diagnostics. A successful pointer
// still requires the scheduler's exact settlement and AfterSettlement hook.
type Outcome struct {
	AttemptDigest string
	Pointer       typedindex.PublicationPointer
	Check         *typedindex.CheckedSummary
	Reports       [2]PhaseReport
}

// PhaseReport is bounded private operational evidence, never execution or
// publication authority. Raw worker output is not retained or persisted here.
type PhaseReport struct {
	Phase      typedindex.Action
	ExitCode   int
	Removed    bool
	StopReason string
	Resources  typedsandbox.Resources
	Watchdog   *typedsandbox.WatchdogReport
	Failure    *provider.Failure
	// Closed controller operation names preserve the original failure separately
	// from cleanup. Cleanup names its first failed operation in execution order.
	// Empty means that side of the phase returned no error.
	FailureOperation string `json:",omitempty"`
	CleanupOperation string `json:",omitempty"`
}

func phaseReport(ctx context.Context, i provider.Invocation, selection []byte, r typedsandbox.Result, out PhaseReport) PhaseReport {
	out.Phase, out.ExitCode, out.Removed, out.StopReason, out.Resources, out.Watchdog = i.Phase, r.ExitCode, r.Removed, r.StopReason, r.Resources, r.Watchdog
	// Failed workers may carry a strict bounded diagnostic envelope. A successful
	// decoded payload without a completion token is deliberately not adopted.
	if ctx.Err() == nil && r.ExitCode != 0 && len(r.Stdout) > 0 {
		decoded, _ := provider.DecodeResult(ctx, i, selection, r.Stdout)
		out.Failure = decoded.Failure()
	}
	return out
}

type nativeOperations struct {
	begin     func(context.Context, string, string) (typedsandbox.Allowance, error)
	prepare   func(context.Context, typedsandbox.HostScratchOptions, *lifecycle.Gate) (typedsandbox.HostScratchReceipt, error)
	verify    func(context.Context, typedsandbox.HostScratchOptions) (typedsandbox.HostScratchReceipt, error)
	cleanup   func(context.Context, typedsandbox.HostScratchOptions) error
	run       func(context.Context, typedsandbox.Options, typedsandbox.ScratchAuthority) (typedsandbox.Result, error)
	complete  func(typedsandbox.Allowance, typedsandbox.ControlIdentity, typedsandbox.Result) error
	advance   func(typedsandbox.Allowance, typedsandbox.Result) (typedsandbox.Allowance, error)
	quiescent func(context.Context, typedsandbox.RecoveryOptions) error
}

func productionNative() nativeOperations {
	return nativeOperations{typedsandbox.BeginAllowance, typedsandbox.PrepareHostScratch, typedsandbox.VerifyHostScratch, typedsandbox.CleanupHostScratch, typedsandbox.Run, typedsandbox.VerifyCompletion, typedsandbox.AdvanceAllowance, typedsandbox.QuiescentAttempt}
}

type executionTurn struct {
	c       *Controller
	release func()
}

func (c *Controller) startTurn(ctx context.Context) (*executionTurn, error) {
	if c == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	select {
	case c.serial <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	t := &executionTurn{c: c}
	if err := t.acquire(ctx); err != nil {
		<-c.serial
		return nil, err
	}
	return t, nil
}
func (t *executionTurn) acquire(ctx context.Context) error {
	if t.release != nil {
		return nil
	}
	r, e := t.c.config.Acquire(ctx)
	if e == nil {
		t.release = r
	}
	return e
}
func (t *executionTurn) unlock() {
	if t.release != nil {
		t.release()
		t.release = nil
	}
}
func (t *executionTurn) close() { t.unlock(); <-t.c.serial }
func stopContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
}

// Execute runs one new or verified-preflight attempt once. Interrupted planning
// or later stages are never replayed and never receive a fresh allowance.
func (c *Controller) Execute(ctx context.Context, chunk store.GenerationChunk, source string, inventoryRaw []byte) (out Outcome, err error) {
	callerCtx := ctx
	t, err := c.startTurn(ctx)
	if err != nil {
		return out, err
	}
	defer t.close()
	if c.config.Socket == "" || c.config.Image == "" {
		return out, ErrUnavailable
	}
	p, err := c.prepare(ctx, chunk, source, inventoryRaw)
	if err != nil {
		return out, err
	}
	id := p.Manifest.Identity
	out.AttemptDigest = id.AttemptDigest
	w, err := c.config.Store.BeginTypedIndex(ctx, chunk)
	if err != nil {
		return out, err
	}
	if w.Stage != store.TypedPreflight || w.Custody == nil || *w.Custody != custody(p.Manifest) {
		return out, ErrHeld
	}
	metadata, err := typedworkspace.LoadOwnerExecutionMetadata(ctx, c.config.Workspace, id, p.Manifest.Digest())
	if err != nil {
		return out, err
	}
	intent, err := c.config.Store.GetTypedIndexIntent(ctx, chunk.Repository)
	if err != nil {
		return out, err
	}
	profile, err := typedindex.DecodeProfile(ctx, []byte(intent.ProfileJSON))
	if err != nil || profile.Digest() != w.Parent.Request().ProfileDigest || profile.Definition().ImageDigest != c.config.Image {
		return out, typedindex.Stale
	}
	tools, err := typedindex.BindHostTools(ctx, w.Parent, metadata.Inventory, metadata.HostTools)
	if err != nil {
		return out, err
	}
	expected, err := c.config.Store.ReadExpectedTypedIndexCurrent(ctx, chunk)
	if err != nil {
		return out, err
	}
	if err = c.config.Store.AdvanceTypedIndex(ctx, chunk, store.TypedPreflight); err != nil {
		return out, err
	}
	// Everything after the durable stage advance is one-shot. Failure cannot
	// return to preflight, including a crash before allowance/control persistence.
	var cancelWork context.CancelFunc
	defer func() {
		if cancelWork != nil {
			defer cancelWork()
		}
		if err != nil && t.release != nil {
			failureCtx, cancel := stopContext(ctx)
			defer cancel()
			reason := refusal(err)
			if callerCtx.Err() == nil && ctx.Err() != nil {
				reason = typedindex.WallLimit
			}
			_ = c.config.Store.FailTypedIndex(failureCtx, chunk, reason)
		}
	}()
	allowance, err := c.native.begin(ctx, id.PlanningDigest, id.AttemptDigest)
	if err != nil {
		return out, err
	}
	workCtx, cancel, err := typedsandbox.AllowanceContext(ctx, allowance)
	if err != nil {
		return out, err
	}
	cancelWork = cancel
	ctx = workCtx
	host := typedsandbox.HostScratchOptions{Base: typedsandbox.HostBaseIdentity{Device: c.host.Device, Inode: c.host.Inode, BlockSize: c.host.BlockSize}, RequestDigest: id.PlanningDigest, AttemptDigest: id.AttemptDigest, Socket: c.config.Socket, MkfsDigest: tools.MkfsDigest}
	invocation := provider.Invocation{Parent: w.Parent, Profile: profile, Inventory: metadata.Inventory, Allowance: allowance, Phase: typedindex.Plan}
	spec := typedworkspace.ControlSpec{Allowance: allowance, Phase: typedworkspace.ControlsPlanning, Parent: w.Parent, Profile: profile, InventoryRaw: metadata.InventoryRaw}
	planning, report, err := c.phase(ctx, t, chunk, p.Manifest, metadata.Inputs, spec, host)
	out.Reports[0] = phaseReport(ctx, invocation, metadata.Selection, planning, report)
	if err != nil {
		return out, err
	}
	advanced, err := c.native.advance(allowance, planning)
	if err != nil {
		return out, err
	}
	decoded, err := provider.DecodeResult(ctx, invocation, metadata.Selection, planning.Stdout)
	if err != nil {
		return out, err
	}
	plan := decoded.Plan()
	execution, err := c.config.Store.SealTypedIndexPlan(ctx, chunk, plan)
	if err != nil {
		return out, err
	}
	invocation.Execution, invocation.Plan, invocation.Allowance, invocation.Phase = execution, plan, advanced, typedindex.Execute
	spec.Execution, spec.Plan, spec.Allowance, spec.Phase = execution, plan, advanced, typedworkspace.ControlsExecution
	result, report, err := c.phase(ctx, t, chunk, p.Manifest, metadata.Inputs, spec, host)
	out.Reports[1] = phaseReport(ctx, invocation, metadata.Selection, result, report)
	if err != nil {
		return out, err
	}
	if err = c.config.Store.AdvanceTypedIndex(ctx, chunk, store.TypedExecution); err != nil {
		return out, err
	}
	decoded, err = provider.DecodeResult(ctx, invocation, metadata.Selection, result.Stdout)
	if err != nil {
		return out, err
	}
	bundle, err := decoded.Finalize(ctx)
	if err != nil {
		return out, err
	}
	if _, err = c.config.Store.BeginTypedIndex(ctx, chunk); err != nil {
		return out, err
	}
	if execution.Purpose() != typedindex.Publish {
		checked, e := c.config.Store.CompleteTypedIndexCheck(ctx, chunk, bundle)
		if e == nil {
			out.Check = &checked
		}
		return out, e
	}
	attempt := filepath.Join(c.config.Workspace, id.RelativeName())
	publication, err := typedworkspace.InstallPublication(ctx, attempt, w.Parent, execution, plan, bundle, c.gates[c.workspace.Device])
	if err != nil {
		return out, err
	}
	if _, err = c.config.Store.BeginTypedIndex(ctx, chunk); err != nil {
		return out, err
	}
	next, err := typedworkspace.SaveOwnerPublication(ctx, c.config.Workspace, id, p.Manifest.Digest(), w.Parent, execution, publication, c.gates[c.workspace.Device])
	if err != nil {
		return out, err
	}
	ref := custody(next)
	ref.PublicationRequestDigest = execution.Digest()
	ref.PublicationPlanDigest = plan.Digest()
	ref.PublicationRootDigest = bundle.RootDigest()
	if err = c.config.Store.SaveTypedIndexCustody(ctx, chunk, p.Manifest.Digest(), ref); err != nil {
		return out, err
	}
	pin, err := typedworkspace.OpenOwnerPublication(ctx, c.config.Workspace, id, w.Parent, execution, plan.Digest(), bundle.RootDigest())
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, pin.Close()) }()
	if err = c.config.Store.AdvanceTypedIndex(ctx, chunk, store.TypedValidation); err != nil {
		return out, err
	}
	out.Pointer, err = c.config.Store.PublishTypedIndexReplacement(ctx, chunk, expected, bundle)
	return out, err
}
func refusal(err error) typedindex.Refusal {
	var r typedindex.Refusal
	if errors.As(err, &r) {
		return r
	}
	if errors.Is(err, context.Canceled) {
		return typedindex.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return typedindex.WallLimit
	}
	return typedindex.Containment
}

func sandboxControl(r typedworkspace.ControlRef) typedsandbox.ControlIdentity {
	return typedsandbox.ControlIdentity{Phase: string(r.Identity.Phase), PlanningDigest: r.Identity.PlanningDigest, AttemptDigest: r.Identity.AttemptDigest, RequestDigest: r.Identity.RequestDigest, Device: r.Directory.Device, Inode: r.Directory.Inode, SealDigest: r.Seal.Digest}
}

func (c *Controller) phase(ctx context.Context, t *executionTurn, chunk store.GenerationChunk, m typedworkspace.OwnerManifest, inputs typedworkspace.Receipt, spec typedworkspace.ControlSpec, host typedsandbox.HostScratchOptions) (result typedsandbox.Result, report PhaseReport, err error) {
	id := m.Identity
	recovery := typedsandbox.RecoveryOptions{Socket: c.config.Socket, ImageID: c.config.Image, Inputs: filepath.Join(c.config.Workspace, id.RelativeName(), inputs.Name), PlanningDigest: id.PlanningDigest, AttemptDigest: id.AttemptDigest}
	var pin *typedworkspace.Controls
	operation := "phase_fence"
	// One cleanup obligation, one stop deadline. Never mutate custody without
	// reacquiring the lifecycle guard; close the attempt pin last on every path.
	defer func() {
		if err != nil {
			report.FailureOperation = operation
		}
		cleanupCtx, cancel := stopContext(ctx)
		defer cancel()
		lockErr := t.acquire(cleanupCtx)
		var cleanupErr error
		if lockErr == nil {
			cleanupErr = c.cleanNative(cleanupCtx, recovery, host)
		}
		var closeErr error
		if pin != nil {
			closeErr = pin.Close()
		}
		switch {
		case lockErr != nil:
			report.CleanupOperation = "mutation_lock"
		case cleanupErr != nil:
			report.CleanupOperation = "native_cleanup"
		case closeErr != nil:
			report.CleanupOperation = "control_close"
		}
		err = errors.Join(err, lockErr, cleanupErr, closeErr)
	}()
	if _, err = c.config.Store.BeginTypedIndex(ctx, chunk); err != nil {
		return result, report, err
	}
	operation = "host_prepare"
	spec.Scratch, err = c.native.prepare(ctx, host, c.gates[c.host.Device])
	if err != nil {
		return result, report, err
	}
	operation = "host_verify"
	observed, err := c.native.verify(ctx, host)
	if err != nil || observed != spec.Scratch {
		return result, report, errors.Join(ErrHeld, err)
	}
	operation = "control_install"
	ref, err := typedworkspace.InstallControls(ctx, c.config.Workspace, id, spec, c.gates[c.workspace.Device])
	if err != nil {
		return result, report, err
	}
	operation = "control_open"
	pin, err = typedworkspace.OpenControls(ctx, c.config.Workspace, id, spec, ref)
	if err != nil {
		return result, report, err
	}
	operation = "launch_fence"
	if _, err = c.config.Store.BeginTypedIndex(ctx, chunk); err != nil {
		return result, report, err
	}
	options := typedsandbox.Options{Socket: c.config.Socket, ImageID: c.config.Image, Inputs: recovery.Inputs, Controls: pin.Path(), Control: sandboxControl(pin.Reference()), Allowance: spec.Allowance}
	t.unlock()
	operation = "native_run"
	result, err = c.native.run(ctx, options, spec.Scratch.Authority)
	if result.StopReason == "wall_limit" {
		err = errors.Join(typedindex.WallLimit, err)
	}
	if err == nil {
		operation = "completion_verify"
		err = c.native.complete(spec.Allowance, options.Control, result)
	}
	return result, report, err
}
