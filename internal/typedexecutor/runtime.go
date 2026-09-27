package typedexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

// BundleLookup belongs to trusted installation composition. It selects an
// already provisioned immutable source and original inventory by admitted parent
// identity. It is never populated by request paths, commands or user callbacks.
type BundleLookup func(context.Context, typedindex.Admission) (source string, inventory []byte, err error)

// Runtime adapts the existing coordinator and generation scheduler. Construction
// registers nothing. The installation must arrange periodic Reconcile after an
// ambiguous settlement; it never replays a worker or refreshes an allowance.
type Runtime struct {
	controller       *Controller
	bundle           BundleLookup
	heartbeat, stale time.Duration
	// Report receives bounded private operational evidence after joined execution.
	// It must not publish source diagnostics or perform authority mutations.
	Report func(Outcome, error)
}

func NewRuntime(c *Controller, bundle BundleLookup) (*Runtime, error) {
	if c == nil || bundle == nil || c.config.Socket == "" || c.config.Image == "" {
		return nil, ErrUnavailable
	}
	return &Runtime{controller: c, bundle: bundle, heartbeat: 5 * time.Second, stale: 20 * time.Second}, nil
}
func (r *Runtime) Coordinator(ctx context.Context, job store.Job) error {
	if r == nil || job.Kind != store.JobTypedIndex || job.TargetTruncated {
		return store.WithTerminal(ErrUnavailable)
	}
	spec, err := r.controller.config.Store.TypedIndexSchedule(ctx, job.Target)
	if err != nil {
		return err
	}
	_, err = r.controller.config.Store.EnqueueGenerationSchedule(ctx, spec)
	return err
}
func (r *Runtime) Class() generationscheduler.Class {
	return generationscheduler.Class{Concurrency: 1, Budget: generationscheduler.TypedIndexBudget(), Handle: r.handle, AfterSettlement: r.afterSettlement}
}

// Scheduler returns the existing bounded scheduler with the same heartbeat and
// stale threshold used by Reconcile. No goroutine starts until Run is called.
func (r *Runtime) Scheduler(ctx context.Context) (*generationscheduler.Scheduler, error) {
	if err := r.ready(ctx); err != nil {
		return nil, err
	}
	return &generationscheduler.Scheduler{Store: r.controller.config.Store, Classes: map[store.GenerationResourceClass]generationscheduler.Class{store.GenerationResourceTypedIndex: r.Class()}, HeartbeatEvery: r.heartbeat, StaleAfter: r.stale}, nil
}

// Reconcile runs before claims are enabled and after ambiguous settlement. It
// checks the full ownership census before the ordinary bounded stale reaper,
// then performs exact startup recovery under the same guards. A live
// lease or unknown custody remains held; callers retry at their existing cadence.
func (r *Runtime) Reconcile(ctx context.Context) error {
	if r == nil || r.heartbeat <= 0 || r.stale <= r.heartbeat {
		return ErrUnavailable
	}
	return r.controller.startup(ctx, func(ctx context.Context) error {
		_, err := r.controller.config.Store.ReapStaleGenerationChunks(ctx, store.GenerationResourceTypedIndex, r.stale)
		return err
	})
}
func (r *Runtime) handle(ctx context.Context, chunk store.GenerationChunk, budget generationscheduler.Budget) error {
	if r == nil || budget != generationscheduler.TypedIndexBudget() {
		return store.WithTerminal(ErrUnavailable)
	}
	if err := r.ready(ctx); err != nil {
		return store.WithDeferral(err)
	}
	d, err := r.controller.config.Store.InspectTypedIndexDisposition(ctx, chunk)
	if err != nil {
		return err
	}
	switch d.State() {
	case store.TypedIndexAlreadyPublished:
		return nil
	case store.TypedIndexInterrupted:
		return store.WithTerminal(ErrHeld)
	case store.TypedIndexFresh:
	default:
		return store.WithTerminal(ErrUnavailable)
	}
	source, raw, err := r.bundle(ctx, d.Parent())
	if err != nil {
		return err
	}
	outcome, err := r.controller.Execute(ctx, chunk, source, raw)
	if r.Report != nil {
		r.Report(outcome, err)
	}
	if err == nil || store.IsDeferral(err) {
		return err
	}
	// A transient failure before native planning remains retryable. If the
	// follow-up read itself fails, retry remains safe: the common acquisition
	// fence must inspect durable history before any new allowance can be minted.
	after, inspectErr := r.controller.config.Store.InspectTypedIndexDisposition(ctx, chunk)
	if inspectErr == nil && after.State() == store.TypedIndexInterrupted {
		return store.WithTerminal(err)
	}
	return err
}
func (r *Runtime) afterSettlement(ctx context.Context, chunk store.GenerationChunk) error {
	sum := sha256.Sum256([]byte(chunk.Identity + "\x00" + chunk.LeaseToken))
	attempt := "sha256:" + hex.EncodeToString(sum[:])
	err := r.controller.AfterSettlement(ctx, attempt)
	if errors.Is(err, store.ErrNotFound) {
		return ErrHeld
	}
	return err
}

func (r *Runtime) ready(ctx context.Context) error {
	if r == nil {
		return ErrUnavailable
	}
	release, err := r.controller.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	if !r.controller.ready {
		return ErrUnavailable
	}
	return nil
}
