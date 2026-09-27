package typedexecutor

import (
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

func TestGroupedAdmission(t *testing.T) {
	capacity := typedworkspace.CapacityObservation{Device: 1, Inode: 1, BlockSize: 4096, TotalBytes: 100 * 4096, FreeBytes: 100 * 4096, AvailableBytes: 100 * 4096, TotalInodes: 1000, FreeInodes: 1000}
	c := &Controller{gates: map[uint64]*lifecycle.Gate{1: lifecycle.NewGate("unused")}}
	for _, tc := range []struct {
		name      string
		available uint64
		want      bool
	}{{"full", 0, false}, {"latched85", 15, false}, {"resume73", 27, true}, {"refuse95", 5, false}, {"still85", 15, false}, {"recover73", 27, true}} {
		t.Run(tc.name, func(t *testing.T) {
			o := capacity
			o.AvailableBytes = tc.available * 4096
			err := c.check(t.Context(), o, 0, 1)
			if (err == nil) != tc.want {
				t.Fatalf("pressure: %v", err)
			}
		})
	}
	wb := typedworkspace.OwnerBudget{Bytes: 30 * 4096, Inodes: 10}
	hb := typedsandbox.HostScratchBudget{Bytes: 30 * 4096, Inodes: 10}
	h := capacity
	h.Inode = 2
	h.AvailableBytes = 50 * 4096
	if _, err := c.admit(t.Context(), capacity, h, wb, hb); !errors.Is(err, lifecycle.ErrPressureRefusal) {
		t.Fatalf("sum/minimum not charged: %v", err)
	}
	h.TotalInodes++
	if _, err := c.admit(t.Context(), capacity, h, wb, hb); !errors.Is(err, ErrHeld) {
		t.Fatalf("geometry: %v", err)
	}
	h = capacity
	h.Device = 2
	h.Inode = 2
	c.gates[2] = lifecycle.NewGate("unused")
	c.gates[1] = lifecycle.NewGate("unused")
	if _, err := c.admit(t.Context(), capacity, h, wb, hb); err != nil {
		t.Fatal("separate domains", err)
	}
	h.FreeInodes = 1
	if _, err := c.admit(t.Context(), capacity, h, wb, hb); !errors.Is(err, lifecycle.ErrCapacityUnavailable) {
		t.Fatalf("inodes: %v", err)
	}
}

func TestHostInodeFloor(t *testing.T) {
	for _, tc := range []struct {
		free uint64
		want bool
	}{{215, false}, {216, false}, {217, true}} {
		o := typedworkspace.CapacityObservation{TotalInodes: 1000, FreeInodes: tc.free}
		if err := inodeFloor(o, 16); (err == nil) != tc.want {
			t.Fatalf("free %d: %v", tc.free, err)
		}
	}
	o := typedworkspace.CapacityObservation{Device: 1, TotalBytes: 100 * 4096, AvailableBytes: 20 * 4096}
	c := &Controller{gates: map[uint64]*lifecycle.Gate{1: lifecycle.NewGate("unused")}}
	if err := c.check(t.Context(), o, 0, 0); !errors.Is(err, lifecycle.ErrPressureRefusal) {
		t.Fatalf("soft collect admitted: %v", err)
	}
}
