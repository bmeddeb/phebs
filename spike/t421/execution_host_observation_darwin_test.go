//go:build darwin

package t421

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestExecutionHostNativeScalars(t *testing.T) {
	requireExternalToolFrozenHost(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	host, err := observeExecutionHostScalars(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Independent native reads, not configured or expected host values.
	product, productErr := unix.Sysctl("kern.osproductversion")
	build, buildErr := unix.Sysctl("kern.osversion")
	cpus, cpuErr := unix.SysctlUint32("hw.logicalcpu")
	memory, memoryErr := unix.SysctlUint64("hw.memsize")
	if productErr != nil || buildErr != nil || cpuErr != nil || memoryErr != nil || host.GOOS != "darwin" || host.GOARCH != "arm64" ||
		host.OSProductVersion != product || host.OSBuildVersion != build || host.LogicalCPUs != uint64(cpus) || host.MemoryBytes != memory {
		t.Fatal("host facts do not match actual native reads")
	}
	if host.BackingVolumeIdentity != "" || host.PressureTotalDiskBytes != 0 {
		t.Fatal("scalar reader manufactured volume facts")
	}
	for _, mode := range []string{"nil", "unbounded", "canceled", "expired"} {
		t.Run(mode, func(t *testing.T) {
			var selected context.Context
			switch mode {
			case "unbounded":
				selected = context.Background()
			case "canceled":
				var stop context.CancelFunc
				selected, stop = context.WithCancel(ctx)
				stop()
			case "expired":
				var stop context.CancelFunc
				selected, stop = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer stop()
			}
			if got, err := observeExecutionHostScalars(selected); err == nil || got != (ExecutionHost{}) {
				t.Fatal("invalid context produced facts")
			}
		})
	}
}

// Supplied statfs values prove arithmetic/refusals, not native capacity or
// profile admission. The separate optional native volume fixture reads real FDs.
func TestExecutionHostFilesystemFacts(t *testing.T) {
	host := executionFreezeTestHost()
	backing := unix.Statfs_t{Bsize: 4096, Blocks: (512 << 30) / 4096, Bavail: (160 << 30) / 4096, Fsid: unix.Fsid{Val: [2]int32{1, -2}}}
	pressure := unix.Statfs_t{Bsize: 4096, Blocks: (96 << 30) / 4096, Bavail: (90 << 30) / 4096, Fsid: unix.Fsid{Val: [2]int32{3, -4}}}
	copy(pressure.Fstypename[:], "apfs")
	base := [3]unix.Statfs_t{backing, pressure, pressure}
	plan := Plan{SafetyEnvelope: frozenSafetyEnvelope(), ToolPolicy: frozenToolPolicy()}
	for _, mode := range []string{"valid", "below_floor", "zero_available", "zero_fsid", "zero_block_size", "zero_blocks", "overflow", "available_overflow",
		"same_backing", "different_ballast", "pressure_size", "pressure_unit", "readonly", "owners_off", "filesystem"} {
		t.Run(mode, func(t *testing.T) {
			stats := base
			switch mode {
			case "below_floor":
				stats[0].Bavail = (plan.SafetyEnvelope.MinimumAvailableDiskBytes - 4096) / 4096
			case "zero_available":
				stats[0].Bavail = 0
				stats[1].Bavail = 0
				stats[2].Bavail = 0
			case "zero_fsid":
				stats[0].Fsid.Val = [2]int32{}
			case "zero_block_size":
				stats[0].Bsize = 0
			case "zero_blocks":
				stats[0].Blocks = 0
			case "overflow":
				stats[0].Blocks = math.MaxUint64/4096 + 1
			case "available_overflow":
				stats[0].Bavail = stats[0].Blocks + 1
			case "same_backing":
				stats[0].Fsid = stats[1].Fsid
			case "different_ballast":
				stats[2].Fsid.Val[0]++
			case "pressure_size":
				stats[1].Blocks--
			case "pressure_unit":
				stats[1].Bsize = 2048
			case "readonly":
				stats[1].Flags = unix.MNT_RDONLY
			case "owners_off":
				stats[2].Flags = unix.MNT_IGNORE_OWNERSHIP
			case "filesystem":
				stats[2].Fstypename[0] = 'x'
			}
			observed, err := executionHostFilesystemFacts(host, stats)
			valid := mode == "valid" || mode == "below_floor" || mode == "zero_available"
			if (err == nil) != valid {
				t.Fatal("filesystem projection classification", err)
			}
			if !valid {
				if observed != (executionHostObservation{}) {
					t.Fatal("refusal retained partial facts")
				}
				return
			}
			if observed.Host.BackingAvailableDiskBytes != stats[0].Bavail*4096 ||
				observed.Host.PressureAvailableDiskBytes != stats[1].Bavail*4096 ||
				observed.FSIDs != ([3][2]int32{stats[0].Fsid.Val, stats[1].Fsid.Val, stats[2].Fsid.Val}) ||
				observed.Host.BackingVolumeIdentity != executionFSIDIdentity(stats[0].Fsid.Val) ||
				observed.Host.DataVolumeIdentity != executionFSIDIdentity(stats[1].Fsid.Val) ||
				observed.Host.BallastVolumeIdentity != executionFSIDIdentity(stats[2].Fsid.Val) {
				t.Fatal("actual supplied facts overwritten")
			}
			admissionErr := validateExecutionHost(observed.Host, plan)
			if (admissionErr == nil) != (mode == "valid") {
				t.Fatal("observation weakened separate host admission", admissionErr)
			}
		})
	}
}

func TestExecutionHostOwnerRefusals(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for _, mode := range []string{"nil", "closed", "used", "started", "repeat", "missing_ballast", "wrong_flow", "unborrowed", "active_author", "released", "owned_check"} {
		t.Run(mode, func(t *testing.T) {
			// Inert modeled bookkeeping deliberately cannot supply a positive native
			// root observation. No authoritative constructor or volume is fabricated.
			flow := &ExecutionEpochOne{plan: Plan{Schema: PlanV3Schema}, epochs: &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{}, roots: make([]productionRoot, 4)}}
			v := &executionPressureVolume{ready: true, borrowed: true, flow: flow}
			flow.workspace = &v.workspace
			v.ballast = &executionPressureBallast{volume: v}
			switch mode {
			case "nil":
				v = nil
			case "closed":
				flow.closed = true
			case "used":
				flow.used = true
			case "started":
				flow.authorStarted = time.Now()
			case "repeat":
				flow.profileHostUsed = true
			case "missing_ballast":
				v.ballast = nil
			case "wrong_flow":
				v.flow = nil
			case "unborrowed":
				v.borrowed = false
			case "active_author":
				flow.epochs.author.active = true
			case "released":
				flow.epochs.released = 1
			}
			if v.observeProfileHost(ctx, flow) == nil || flow.profileHost != nil {
				t.Fatal("invalid owner produced host evidence")
			}
			if flow.profileHostUsed != (mode == "repeat" || mode == "owned_check") {
				t.Fatal("prework/owned-check attempt distinction changed")
			}
		})
	}
}

func checkExecutionHostNativeVolume(t *testing.T, ctx context.Context, v *executionPressureVolume, probe string) {
	t.Helper()
	// Existing real probe inode is only a test ballast role. This tests actual
	// native reads, not production data/ballast binding or complete admission.
	file, err := os.Open(probe)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	observed, err := observeExecutionHost(ctx, [3]*os.File{v.parent.file, v.workspace.file, file})
	if err != nil || observed.FSIDs != ([3][2]int32{v.parent.volume, v.workspace.volume, v.workspace.volume}) ||
		observed.Host.PressureTotalDiskBytes != 96<<30 || observed.Host.PressureAllocationUnitBytes != 4096 {
		t.Fatal("actual volume observation", err)
	}
	// Capacity may change between reads; do not demand equality with a later
	// census or claim an atomic historical filesystem snapshot.
	if _, err := observeExecutionHost(ctx, [3]*os.File{v.parent.file, nil, file}); err == nil {
		t.Fatal("missing data descriptor accepted")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	// Prevent a deferred double-close error while explicitly testing a closed FD.
	if _, err := observeExecutionHost(ctx, [3]*os.File{v.parent.file, v.workspace.file, file}); err == nil {
		t.Fatal("closed ballast descriptor accepted")
	}
}
