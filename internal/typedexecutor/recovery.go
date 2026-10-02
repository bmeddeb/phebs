package typedexecutor

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

func (c *Controller) cleanNative(ctx context.Context, r typedsandbox.RecoveryOptions, expected typedsandbox.HostScratchOptions) error {
	if err := c.native.quiescent(ctx, r); err != nil {
		return err
	}
	name, err := typedsandbox.HostScratchRootName(r.PlanningDigest, r.AttemptDigest)
	if err != nil {
		return err
	}
	observed, err := c.observeHost(ctx, name)
	if err != nil || observed.Overflow {
		return errors.Join(ErrHeld, err)
	}
	if len(observed.Names) == 0 && observed.Selected == nil && !observed.Held {
		return nil
	}
	if len(observed.Names) != 1 || observed.Names[0] != name || observed.Selected == nil || observed.Selected.Options != expected {
		return ErrHeld
	}
	if err = c.native.cleanup(ctx, expected); err != nil {
		return err
	}
	observed, err = c.observeHost(ctx, "")
	if err != nil || observed.Held || observed.Overflow || len(observed.Names) != 0 || observed.Selected != nil {
		return errors.Join(ErrHeld, err)
	}
	return ctx.Err()
}

// Recover stops only authenticated old native custody and releases a settled
// holder. It never runs a worker or creates a new allowance. A matching running
// scheduler lease remains held until the scheduler settles/reaps it; call again
// through AfterSettlement. Claims remain disabled until Startup succeeds.
func (c *Controller) Recover(ctx context.Context) error {
	t, err := c.startTurn(ctx)
	if err != nil {
		return err
	}
	defer t.close()
	c.ready = false
	return c.recoverHeld(ctx, "")
}

// AfterSettlement must be called by the scheduler after its exact chunk
// transition. Merely returning from Execute cannot release a running lease's
// promise. This hook also handles publication committed before settlement loss.
func (c *Controller) AfterSettlement(ctx context.Context, attempt string) error {
	if attempt == "" {
		return ErrHeld
	}
	t, err := c.startTurn(ctx)
	if err != nil {
		return err
	}
	defer t.close()
	return c.recoverHeld(ctx, attempt)
}
func (c *Controller) recoverHeld(ctx context.Context, expectedAttempt string) error {
	if c.config.Socket == "" || c.config.Image == "" {
		return ErrUnavailable
	}
	// Startup/ambiguous recovery needs the complete bidirectional census. An
	// already-ready controller's settlement hook inspects only its exact holder.
	if !c.ready || expectedAttempt == "" {
		if err := c.censusOwnership(ctx); err != nil {
			return err
		}
	}
	return c.recoverCensusedHeld(ctx, expectedAttempt)
}

// recoverCensusedHeld is only called under the serial/lifecycle guards after
// a successful startup census (selected ready settlement), or after this
// same turn completed the full bidirectional census (startup/recovery).
func (c *Controller) recoverCensusedHeld(ctx context.Context, expectedAttempt string) error {
	if c.config.Socket == "" || c.config.Image == "" {
		return ErrUnavailable
	}
	holder, err := c.config.Store.GetTypedIndexGrowth(ctx)
	if errors.Is(err, store.ErrNotFound) {
		_, _, e := c.observe(ctx)
		return e
	}
	if err != nil {
		return err
	}
	if expectedAttempt != "" && holder.AttemptDigest != expectedAttempt {
		return ErrHeld
	}
	w, err := typedworkspace.ObserveCapacity(ctx, c.config.Workspace)
	if err != nil {
		return err
	}
	host, err := c.observeHost(ctx, "")
	if err != nil || host.Overflow || !growthBase(holder.Spec.Workspace, w) || !growthBase(holder.Spec.Host, typedworkspace.CapacityObservation(host.Capacity)) {
		return errors.Join(ErrHeld, err)
	}
	if c.workspace.Inode != 0 && (!sameBase(c.workspace, w) || !sameBase(c.host, typedworkspace.CapacityObservation(host.Capacity))) {
		return ErrHeld
	}
	a, err := c.config.Store.InspectTypedIndexAttempt(ctx, holder.AttemptDigest)
	if err != nil || a.Custody == nil {
		return errors.Join(ErrHeld, err)
	}
	if err = c.inspectOwner(ctx, a); err != nil {
		return err
	}
	id := typedworkspace.OwnerIdentity{PlanningDigest: a.PlanningDigest, AttemptDigest: a.AttemptDigest, ChunkIdentity: a.ChunkIdentity, LeaseDigest: a.LeaseDigest, Request: a.Parent}
	m, err := typedworkspace.LoadOwnerWithNativeCustody(ctx, c.config.Workspace, id)
	if err != nil || m.InputName == "" {
		return errors.Join(ErrHeld, err)
	}
	guardCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	release, err := typedworkspace.AcquirePublicationMutation(guardCtx, filepath.Join(c.config.Workspace, id.RelativeName()))
	if err != nil {
		return errors.Join(ErrHeld, err)
	}
	defer release()
	recovery := typedsandbox.RecoveryOptions{Socket: c.config.Socket, ImageID: c.config.Image, Inputs: filepath.Join(c.config.Workspace, id.RelativeName(), m.InputName), PlanningDigest: id.PlanningDigest, AttemptDigest: id.AttemptDigest}
	name, err := typedsandbox.HostScratchRootName(id.PlanningDigest, id.AttemptDigest)
	if err != nil {
		return err
	}
	selected, err := c.observeHost(ctx, name)
	if err != nil || selected.Overflow {
		return errors.Join(ErrHeld, err)
	}
	options := typedsandbox.HostScratchOptions{Base: typedsandbox.HostBaseIdentity{Device: host.Capacity.Device, Inode: host.Capacity.Inode, BlockSize: host.Capacity.BlockSize}, RequestDigest: id.PlanningDigest, AttemptDigest: id.AttemptDigest, Socket: c.config.Socket}
	if selected.Selected != nil {
		recorded := selected.Selected.Options
		if recorded.Base != options.Base || recorded.RequestDigest != options.RequestDigest || recorded.AttemptDigest != options.AttemptDigest || recorded.Socket != options.Socket {
			return ErrHeld
		}
		options = recorded
	}
	cleanupCtx, stop := stopContext(ctx)
	defer stop()
	if err = c.cleanNative(cleanupCtx, recovery, options); err != nil {
		return err
	}
	snapshot, err := c.config.Store.InspectTypedIndexGrowthRelease(ctx, id.AttemptDigest)
	if err != nil {
		return errors.Join(ErrHeld, err)
	}
	return c.config.Store.ReleaseTypedIndexGrowth(ctx, snapshot)
}
