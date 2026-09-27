// Package typedexecutor owns the unregistered typed-index controller.
// Installation and scheduler registration remain separate from this package.
package typedexecutor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

var ErrHeld = errors.New("typed preparation custody requires reconciliation")
var ErrUnavailable = errors.New("typed preparation startup unavailable")

// Config is trusted installation configuration. Acquire must be the existing
// shared lifecycle-mutation guard. Both filesystem bases and their locks must
// already be provisioned; construction and startup create nothing.
type Config struct {
	Store     *store.Surreal
	Workspace string
	Acquire   func(context.Context) (func(), error)
	// Socket/Image are fixed trusted installation identities, not request fields.
	Socket, Image string
}

type Controller struct {
	config          Config
	serial          chan struct{}
	ready           bool
	workspace, host typedworkspace.CapacityObservation
	gates           map[uint64]*lifecycle.Gate
	observeHost     func(context.Context, string) (typedsandbox.HostObservation, error)
	native          nativeOperations
}

func New(config Config) (*Controller, error) {
	if config.Store == nil || config.Acquire == nil || !filepath.IsAbs(config.Workspace) || filepath.Clean(config.Workspace) != config.Workspace {
		return nil, ErrUnavailable
	}
	return &Controller{config: config, serial: make(chan struct{}, 1), gates: make(map[uint64]*lifecycle.Gate), observeHost: typedsandbox.ObserveHostScratch, native: productionNative()}, nil
}
func (c *Controller) enter(ctx context.Context) (func(), error) {
	if c == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case c.serial <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release, err := c.config.Acquire(ctx)
	if err != nil {
		<-c.serial
		return nil, err
	}
	return func() { release(); <-c.serial }, nil
}

// Prepared is verified immutable input custody, not native execution readiness.
// No pin escapes this call; a consumer must reopen/verify under its own pin.
type Prepared struct {
	Manifest typedworkspace.OwnerManifest
	Inputs   typedworkspace.Receipt
}

// Prepare admits growth exactly once. A retained holder may only reopen a
// completed revision-2 preparation; all interrupted prefixes remain held. The
// scheduler owns settlement and later cleanup owns growth release.
func (c *Controller) Prepare(ctx context.Context, chunk store.GenerationChunk, source string, inventoryRaw []byte) (Prepared, error) {
	release, err := c.enter(ctx)
	if err != nil {
		return Prepared{}, err
	}
	defer release()
	return c.prepare(ctx, chunk, source, inventoryRaw)
}
func (c *Controller) prepare(ctx context.Context, chunk store.GenerationChunk, source string, inventoryRaw []byte) (Prepared, error) {
	if !c.ready {
		return Prepared{}, ErrUnavailable
	}
	held, err := c.config.Store.GetTypedIndexGrowth(ctx)
	exists := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return Prepared{}, err
	}
	if exists && (held.ChunkIdentity != chunk.Identity || held.LeaseDigest != store.GenerationLeaseTokenDigest(chunk.LeaseToken)) {
		return Prepared{}, store.WithDeferral(ErrHeld)
	}
	disposition, err := c.config.Store.InspectTypedIndexDisposition(ctx, chunk)
	if err != nil {
		return Prepared{}, err
	}
	if disposition.State() != store.TypedIndexFresh {
		return Prepared{}, ErrHeld
	}
	work, err := c.config.Store.BeginTypedIndex(ctx, chunk)
	if err != nil {
		return Prepared{}, err
	}
	id, err := typedworkspace.NewOwnerIdentity(work.Parent, chunk.Identity, chunk.LeaseToken)
	if err != nil || id.AttemptDigest != work.AttemptDigest {
		return Prepared{}, ErrHeld
	}
	inv, err := typedindex.DecodeInventory(ctx, inventoryRaw, work.Parent.Request().BundleDigest)
	if err != nil {
		return Prepared{}, err
	}
	disposition, err = c.config.Store.InspectTypedIndexDisposition(ctx, chunk)
	if err != nil {
		return Prepared{}, err
	}
	if disposition.State() != store.TypedIndexFresh {
		return Prepared{}, ErrHeld
	}
	if exists || work.Growth != nil || work.Custody != nil {
		if work.Growth == nil {
			return Prepared{}, ErrHeld
		}
		if _, err = c.config.Store.AcquireFreshTypedIndexGrowth(ctx, chunk, work.Growth.Spec, disposition); err != nil {
			return Prepared{}, err
		}
		return c.reopen(ctx, chunk, work, id)
	}
	if work.Stage != store.TypedPreflight {
		return Prepared{}, ErrHeld
	}
	workspace, host, err := c.observe(ctx)
	if err != nil {
		return Prepared{}, err
	}
	budget, err := typedworkspace.DeriveOwnerBudget(ctx, inv, workspace.BlockSize)
	if err != nil {
		return Prepared{}, err
	}
	hostBudget, err := typedsandbox.DeriveHostScratchBudget(host.BlockSize)
	if err != nil {
		return Prepared{}, err
	}
	spec, err := c.admit(ctx, workspace, host, budget, hostBudget)
	if err != nil {
		return Prepared{}, store.WithDeferral(err)
	}
	if _, err = c.config.Store.AcquireFreshTypedIndexGrowth(ctx, chunk, spec, disposition); err != nil {
		return Prepared{}, err
	}
	m, err := typedworkspace.CreateOwner(ctx, c.config.Workspace, workspace, id, budget, c.gates[workspace.Device])
	if err != nil {
		return Prepared{}, fmt.Errorf("create typed owner: %w", err)
	}
	if err = c.config.Store.SaveTypedIndexCustody(ctx, chunk, "", custody(m)); err != nil {
		return Prepared{}, err
	}
	// Repeating Begin is a bounded live lease/source/profile fence, not another
	// attempt creation. Each following store CAS repeats that fence atomically.
	if _, err = c.config.Store.BeginTypedIndex(ctx, chunk); err != nil {
		return Prepared{}, err
	}
	attempt := filepath.Join(c.config.Workspace, id.RelativeName())
	receipt, err := typedworkspace.Copy(ctx, source, attempt, inv, c.gates[workspace.Device])
	if err != nil {
		return Prepared{}, fmt.Errorf("copy typed inputs: %w", err)
	}
	if _, err = c.config.Store.BeginTypedIndex(ctx, chunk); err != nil {
		return Prepared{}, err
	}
	// SaveOwnerInputs performs the full immutable-byte Verify before recording
	// receipts; avoid a redundant third corpus read here.
	next, err := typedworkspace.SaveOwnerInputs(ctx, c.config.Workspace, id, m.Digest(), inventoryRaw, receipt, c.gates[workspace.Device])
	if err != nil {
		return Prepared{}, err
	}
	if err = c.config.Store.SaveTypedIndexCustody(ctx, chunk, m.Digest(), custody(next)); err != nil {
		return Prepared{}, err
	}
	return Prepared{next, receipt}, nil
}
func (c *Controller) reopen(ctx context.Context, chunk store.GenerationChunk, work store.TypedIndexWork, id typedworkspace.OwnerIdentity) (Prepared, error) {
	if work.Stage != store.TypedPreflight || work.Custody == nil || work.Custody.Revision != 2 || work.Growth == nil || work.Growth.State != "held" {
		return Prepared{}, ErrHeld
	}
	if _, _, err := c.observe(ctx); err != nil {
		return Prepared{}, err
	}
	m, err := typedworkspace.LoadOwner(ctx, c.config.Workspace, id)
	if err != nil {
		return Prepared{}, err
	}
	if custody(m) != *work.Custody {
		return Prepared{}, ErrHeld
	}
	_, r, err := typedworkspace.LoadOwnerInputs(ctx, c.config.Workspace, id)
	if err != nil {
		return Prepared{}, err
	}
	if _, err = c.config.Store.BeginTypedIndex(ctx, chunk); err != nil {
		return Prepared{}, err
	}
	return Prepared{m, r}, nil
}
func custody(m typedworkspace.OwnerManifest) store.TypedIndexCustody {
	r := store.TypedIndexCustody{PlanningDigest: m.Identity.PlanningDigest, AttemptDigest: m.Identity.AttemptDigest, ManifestDigest: m.Digest(), Revision: m.Revision, DirectoryDevice: m.Directory.Device, DirectoryInode: m.Directory.Inode}
	if m.Inputs != nil {
		r.InputReceiptDigest = m.Inputs.Digest
	}
	// Preparation never creates revision 3. Startup compares its publication
	// reference separately using the store's authenticated request/plan/root.
	if m.Publication != nil {
		r.PublicationReceiptDigest = m.Publication.Digest
	}
	return r
}
