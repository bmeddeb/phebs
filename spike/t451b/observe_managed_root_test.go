package t451b

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

func TestRootManagedObservationBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ctx        func() context.Context
		pid        uint32
		euid       int
		proc       func() fs.FS
		startError bool
		sticky     bool
	}{
		{name: "root-fixed-UID-and-bounds", pid: 2, proc: func() fs.FS {
			m := observationProc()
			addProc(m, 3, 101, 65534, typedsandbox.DescriptorLimit)
			for pid := 5; pid <= typedsandbox.TaskLimit; pid++ {
				addProc(m, pid, uint64(pid), 0, 0)
			}
			return m
		}},
		{name: "nonroot", pid: 2, euid: 65534, startError: true},
		{name: "PID1", pid: 1, startError: true},
		{name: "zero-PID", startError: true},
		{name: "nil-context", pid: 2, ctx: func() context.Context { return nil }, startError: true},
		{name: "nil-proc", pid: 2, proc: func() fs.FS { return nil }, startError: true},
		{name: "canceled", pid: 2, ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, startError: true},
		{name: "process-ceiling", pid: 2, sticky: true, proc: func() fs.FS {
			m := observationProc()
			for pid := 5; pid <= typedsandbox.TaskLimit+1; pid++ {
				addProc(m, pid, uint64(pid), 0, 0)
			}
			return m
		}},
		{name: "descriptor-ceiling", pid: 2, sticky: true, proc: func() fs.FS {
			m := observationProc()
			addProc(m, 3, 101, 65534, typedsandbox.DescriptorLimit+1)
			return m
		}},
		{name: "first-live-denial-stays-sticky", pid: 2, sticky: true, proc: func() fs.FS {
			m := observationProc()
			denied := false
			return procOpenFS{FS: m, open: func(name string) (fs.File, error) {
				if name == "3/fd" && !denied {
					denied = true
					return nil, fs.ErrPermission
				}
				return m.Open(name)
			}}
		}},
		{name: "worker-still-required", pid: 5, sticky: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.ctx != nil {
				ctx = tc.ctx()
			}
			var proc fs.FS = observationProc()
			if tc.proc != nil {
				proc = tc.proc()
			}
			stop, err := startRootManagedObservations(ctx, proc, tc.pid, tc.euid)
			if tc.startError {
				if err == nil || stop != nil {
					t.Fatal("invalid root observer authority admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			facts, err := stop()
			if tc.sticky {
				if err == nil || !facts.Unavailable || ValidateManagedObservations(facts) == nil {
					t.Fatal("root observer relaxed a sticky failure", err)
				}
				if tc.name == "first-live-denial-stays-sticky" && (!errors.Is(err, fs.ErrPermission) || facts.UnexpectedErrors != 1) {
					t.Fatal("first error was replaced or retried", err)
				}
				return
			}
			if err != nil || ValidateManagedObservations(facts) != nil || facts.SampledProcessFDPeak != typedsandbox.DescriptorLimit || facts.SampledChildLifetimes != 1 {
				t.Fatal("root method changed fixed UID or qualified bounds", err)
			}
		})
	}
}
