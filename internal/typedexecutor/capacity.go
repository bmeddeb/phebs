package typedexecutor

import (
	"context"
	"math"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

func sameBase(a, b typedworkspace.CapacityObservation) bool {
	return a.Device == b.Device && a.Inode == b.Inode && sameGeometry(a, b)
}
func sameGeometry(a, b typedworkspace.CapacityObservation) bool {
	return a.BlockSize == b.BlockSize && a.TotalBytes == b.TotalBytes && a.TotalInodes == b.TotalInodes
}
func validCapacity(o typedworkspace.CapacityObservation) bool {
	return o.Device != 0 && o.Inode != 0 && o.BlockSize > 0 && o.BlockSize <= 1<<20 && o.BlockSize&(o.BlockSize-1) == 0 && o.TotalBytes > 0 && o.TotalBytes <= math.MaxInt64 && o.FreeBytes <= o.TotalBytes && o.AvailableBytes <= o.FreeBytes && o.TotalBytes%o.BlockSize == 0 && o.FreeBytes%o.BlockSize == 0 && o.AvailableBytes%o.BlockSize == 0 && o.TotalInodes > 0 && o.FreeInodes <= o.TotalInodes
}
func (c *Controller) observe(ctx context.Context) (typedworkspace.CapacityObservation, typedworkspace.CapacityObservation, error) {
	w, err := typedworkspace.ObserveCapacity(ctx, c.config.Workspace)
	if err != nil {
		return w, typedworkspace.CapacityObservation{}, err
	}
	h, err := c.observeHost(ctx, "")
	host := typedworkspace.CapacityObservation(h.Capacity)
	if err != nil {
		return w, host, err
	}
	// Growth admission requires an empty native namespace. Recovery separately
	// authenticates retained journals before this check can admit new work.
	if h.Held || h.Overflow || len(h.Names) != 0 || h.Selected != nil || !validCapacity(w) || !validCapacity(host) {
		return w, host, ErrHeld
	}
	if c.workspace.Inode != 0 && (!sameBase(w, c.workspace) || !sameBase(host, c.host)) {
		return w, host, ErrHeld
	}
	if w.Device == host.Device && !sameGeometry(w, host) {
		return w, host, ErrHeld
	}
	if c.workspace.Inode == 0 {
		c.workspace, c.host = w, host
		c.gates[w.Device] = lifecycle.NewGate(c.config.Workspace)
		if c.gates[host.Device] == nil {
			c.gates[host.Device] = lifecycle.NewGate(typedsandbox.HostScratchBase)
		}
	}
	return w, host, nil
}
func domain(o typedworkspace.CapacityObservation, bytes int64, inodes uint64) store.TypedIndexGrowthDomain {
	return store.TypedIndexGrowthDomain{Device: o.Device, BaseInode: o.Inode, BlockBytes: o.BlockSize, TotalBytes: o.TotalBytes, AvailableBytes: o.AvailableBytes, TotalInodes: o.TotalInodes, FreeInodes: o.FreeInodes, FutureBytes: uint64(bytes), FutureInodes: inodes}
}
func (c *Controller) admit(ctx context.Context, w, h typedworkspace.CapacityObservation, wb typedworkspace.OwnerBudget, hb typedsandbox.HostScratchBudget) (store.TypedIndexGrowthSpec, error) {
	if !validCapacity(w) || !validCapacity(h) || wb.Bytes <= 0 || hb.Bytes <= 0 || wb.Inodes == 0 || hb.Inodes == 0 {
		return store.TypedIndexGrowthSpec{}, ErrHeld
	}
	spec := store.TypedIndexGrowthSpec{Workspace: domain(w, wb.Bytes, wb.Inodes), Host: domain(h, hb.Bytes, hb.Inodes)}
	if w.Device == h.Device {
		if !sameGeometry(w, h) || hb.Bytes > lifecycle.MaxPressureDependentAdmissionBytes-wb.Bytes || hb.Inodes > math.MaxUint64-wb.Inodes {
			return spec, ErrHeld
		}
		w.AvailableBytes = min(w.AvailableBytes, h.AvailableBytes)
		w.FreeInodes = min(w.FreeInodes, h.FreeInodes)
		if err := c.check(ctx, w, wb.Bytes+hb.Bytes, wb.Inodes+hb.Inodes); err != nil {
			return spec, err
		}
		return spec, inodeFloor(w, wb.Inodes+hb.Inodes)
	}
	if err := c.check(ctx, w, wb.Bytes, wb.Inodes); err != nil {
		return spec, err
	}
	if err := c.check(ctx, h, hb.Bytes, hb.Inodes); err != nil {
		return spec, err
	}
	return spec, inodeFloor(h, hb.Inodes)
}
func (c *Controller) check(ctx context.Context, o typedworkspace.CapacityObservation, bytes int64, inodes uint64) error {
	// Observe pressure first, including a completely full filesystem, so the
	// persistent latch cannot be bypassed by an early free-space refusal.
	result, err := c.gates[o.Device].CheckObserved(ctx, lifecycle.Capacity{TotalBytes: int64(o.TotalBytes), AvailableBytes: int64(o.AvailableBytes), UsedBytes: int64(o.TotalBytes - o.AvailableBytes)}, bytes)
	if err != nil {
		return err
	}
	if result.Pressure != lifecycle.PressureNormal {
		return lifecycle.ErrPressureRefusal
	}
	if uint64(bytes) > o.AvailableBytes || inodes > o.FreeInodes {
		return lifecycle.ErrCapacityUnavailable
	}
	return nil
}

func inodeFloor(o typedworkspace.CapacityObservation, promised uint64) error {
	if promised > o.FreeInodes || o.FreeInodes-promised <= o.TotalInodes/5 {
		return lifecycle.ErrCapacityUnavailable
	}
	return nil
}
