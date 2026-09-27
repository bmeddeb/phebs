package typedexecutor

import (
	"context"
	"errors"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

// Startup must finish before typed claims are enabled. It visits all six tables
// one page at a time, then both filesystem ownership directions. Configured
// recovery may stop/remove exact native custody and release settled promises;
// it never adopts owners, rewrites publication authority or hashes large receipts.
// Failed startup clears readiness but preserves pressure latches and base identities.
func (c *Controller) Startup(ctx context.Context) error {
	release, err := c.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	c.ready = false
	if c.config.Socket != "" || c.config.Image != "" {
		if err = c.recoverHeld(ctx, ""); err != nil {
			return err
		}
	}
	if _, _, err = c.observe(ctx); err != nil {
		return err
	}
	growth, e := c.config.Store.GetTypedIndexGrowth(ctx)
	if e != nil && !errors.Is(e, store.ErrNotFound) {
		return e
	}
	if e == nil && (!growthBase(growth.Spec.Workspace, c.workspace) || !growthBase(growth.Spec.Host, c.host)) {
		return ErrHeld
	}
	if c.config.Socket == "" && c.config.Image == "" {
		if err = c.censusOwnership(ctx); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	c.ready = true
	return nil
}

// censusOwnership checks every durable relation and both custody directions
// before recovery can mutate native state. The caller holds the lifecycle guard.
func (c *Controller) censusOwnership(ctx context.Context) error {
	kinds := []store.TypedIndexControlKind{store.TypedIndexIntents, store.TypedIndexRequests, store.TypedIndexPlans, store.TypedIndexAttempts, store.TypedIndexStates, store.TypedIndexCurrents}
	for _, kind := range kinds {
		after := ""
		for {
			page, e := c.config.Store.ScanTypedIndexControls(ctx, kind, after, 64)
			if e != nil {
				return e
			}
			for _, row := range page.Rows {
				if kind != store.TypedIndexAttempts {
					if e = c.config.Store.InspectTypedIndexControlRelations(ctx, kind, row.ID); e != nil {
						return e
					}
				}
				if kind == store.TypedIndexRequests && row.Parent {
					if e = c.config.Store.InspectTypedIndexRetainedLimits(ctx, row.Repository, row.ID); e != nil {
						return e
					}
				}
				if kind == store.TypedIndexAttempts {
					a, e := c.config.Store.InspectTypedIndexAttempt(ctx, row.ID)
					if e != nil {
						return e
					}
					if a.Custody != nil {
						if e = c.inspectOwner(ctx, a); e != nil {
							return e
						}
					}
				}
			}
			if page.Next == "" {
				break
			}
			if page.Next <= after {
				return ErrHeld
			}
			after = page.Next
		}
	}
	roots, err := typedworkspace.CensusOwners(ctx, c.config.Workspace, "")
	if err != nil {
		return err
	}
	for _, root := range roots {
		if root.Held || !root.Directory {
			return ErrHeld
		}
		planning := "sha256:" + root.Name
		if err = c.config.Store.InspectTypedIndexControlRelations(ctx, store.TypedIndexRequests, planning); err != nil {
			return err
		}
		attempts, e := typedworkspace.CensusOwners(ctx, c.config.Workspace, planning)
		if e != nil {
			return e
		}
		if len(attempts) == 0 {
			return ErrHeld
		}
		for _, attempt := range attempts {
			if attempt.Held || !attempt.Directory {
				return ErrHeld
			}
			a, e := c.config.Store.InspectTypedIndexAttempt(ctx, "sha256:"+attempt.Name)
			if e != nil {
				return e
			}
			if a.PlanningDigest != planning || a.Custody == nil {
				return ErrHeld
			}
			if e = c.inspectOwner(ctx, a); e != nil {
				return e
			}
		}
	}
	return ctx.Err()
}
func growthBase(d store.TypedIndexGrowthDomain, o typedworkspace.CapacityObservation) bool {
	return d.Device == o.Device && d.BaseInode == o.Inode && d.BlockBytes == o.BlockSize && d.TotalBytes == o.TotalBytes && d.TotalInodes == o.TotalInodes
}
func (c *Controller) inspectOwner(ctx context.Context, a store.TypedIndexAttemptInspection) error {
	id := typedworkspace.OwnerIdentity{PlanningDigest: a.PlanningDigest, AttemptDigest: a.AttemptDigest, ChunkIdentity: a.ChunkIdentity, LeaseDigest: a.LeaseDigest, Request: a.Parent}
	m, err := typedworkspace.LoadOwner(ctx, c.config.Workspace, id)
	if err != nil {
		return err
	}
	actual := custody(m)
	expected := *a.Custody
	// Publication's authenticated identities are already checked by the store
	// inspector; metadata census only binds the immutable receipt reference.
	actual.PublicationRequestDigest = expected.PublicationRequestDigest
	actual.PublicationPlanDigest = expected.PublicationPlanDigest
	actual.PublicationRootDigest = expected.PublicationRootDigest
	if actual != expected {
		return ErrHeld
	}
	return nil
}
