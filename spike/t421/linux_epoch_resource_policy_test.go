//go:build linux

package t421

import (
	"math"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func linuxEpochResourceTestMemory() t4013.LinuxMemoryObservation {
	return t4013.LinuxMemoryObservation{PhysicalBytes: 16 << 30, AvailableBytes: 15 << 30, CgroupAncestryComplete: true,
		CgroupAncestors: []t4013.LinuxMemoryCgroupObservation{
			{Identity: SHA256([]byte("synthetic-root")), HierarchyRoot: true},
			{Identity: SHA256([]byte("synthetic-leaf")), ParentIdentity: SHA256([]byte("synthetic-root")), LimitBytes: 15 << 30},
		}}
}

func TestLinuxEpochResourceInitialReservationAndLiveSlack(t *testing.T) {
	initial := linuxEpochResourceTestMemory()
	calculation, err := evaluateLinuxEpochResources(initial, 1<<30)
	if err != nil || calculation.RequiredBytes != 7<<30 || calculation.ReservedBytes != 6<<30 || calculation.HostHeadroomBytes != 1<<30 {
		t.Fatal("closed full reservation", calculation, err)
	}
	exact := initial
	exact.PhysicalBytes, exact.AvailableBytes = 7<<30, 7<<30
	if _, err := evaluateLinuxEpochResources(exact, 1<<30); err != nil {
		t.Fatal("exact-fit refusal", err)
	}
	if _, err := evaluateLinuxEpochResources(exact, 1<<30+1); err == nil {
		t.Fatal("one-byte excess admitted")
	}
	for _, payload := range []uint64{0, math.MaxUint64, math.MaxInt64} {
		if _, err := evaluateLinuxEpochResources(initial, payload); err == nil {
			t.Fatal("invalid or overflowing payload admitted", payload)
		}
	}
	// Owned work is not credited or reserved a second time. The independently
	// frozen live slack floor remains 2GiB, with the initial full capacity bound.
	for _, test := range []struct {
		name   string
		change func(*t4013.LinuxMemoryObservation)
		ok     bool
	}{
		{"exact-live-slack", func(o *t4013.LinuxMemoryObservation) { o.AvailableBytes = 2 << 30 }, true},
		{"one-byte-short", func(o *t4013.LinuxMemoryObservation) { o.AvailableBytes = 2<<30 - 1 }, false},
		{"ancestor-slack", func(o *t4013.LinuxMemoryObservation) { o.CgroupAncestors[1].CurrentBytes = 13 << 30 }, true},
		{"ancestor-short", func(o *t4013.LinuxMemoryObservation) { o.CgroupAncestors[1].CurrentBytes = 13<<30 + 1 }, false},
		{"physical-drift", func(o *t4013.LinuxMemoryObservation) { o.PhysicalBytes-- }, false},
		{"limit-drift-up", func(o *t4013.LinuxMemoryObservation) { o.CgroupAncestors[1].LimitBytes++ }, false},
		{"limit-drift-down", func(o *t4013.LinuxMemoryObservation) { o.CgroupAncestors[1].LimitBytes-- }, false},
		{"membership-drift", func(o *t4013.LinuxMemoryObservation) { o.CgroupAncestors[1].Identity = SHA256([]byte("different")) }, false},
		{"missing-ancestry", func(o *t4013.LinuxMemoryObservation) { o.CgroupAncestryComplete = false }, false},
		{"unavailable-current", func(o *t4013.LinuxMemoryObservation) { o.CgroupAncestors[1].CurrentBytes = -1 }, false},
		{"capacity-still-required", func(o *t4013.LinuxMemoryObservation) {}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := initial
			current.CgroupAncestors = slices.Clone(initial.CgroupAncestors)
			test.change(&current)
			if err := checkLinuxEpochLiveMemory(initial, calculation.RequiredBytes, current); (err == nil) != test.ok {
				t.Fatal("wrong independent live rule", err)
			}
		})
	}
	if checkLinuxEpochLiveMemory(initial, 16<<30, initial) == nil {
		t.Fatal("held reservation exceeded effective capacity")
	}
	if frozenSafetyEnvelope().MaximumPeakRSSBytes != 20<<30 || linuxEpochPolicy().RSSReservationBytes != 4<<30 || linuxEpochPolicy().Schema != "t42-linux-epoch-operational-resources-v2" || !validExecutionSHA256(linuxEpochPolicySHA256()) {
		t.Fatal("prospective constraint changed historical envelope")
	}
}

func TestLinuxEpochResourcePayloadArithmetic(t *testing.T) {
	for _, test := range []struct {
		name               string
		size, blocks, page uint64
		want               uint64
		ok                 bool
	}{
		{"page-rounded", 4097, 9, 4096, 8192, true},
		{"allocated-larger", 1, 16, 4096, 8192, true},
		{"empty", 0, 0, 4096, 0, false},
		{"bad-page", 1, 1, 3, 0, false},
		{"zero-page", 1, 1, 0, 0, false},
		{"round-overflow", math.MaxInt64, 0, 4096, 0, false},
		{"block-overflow", 1, math.MaxUint64, 4096, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := linuxEpochImageBytes(test.size, test.blocks, test.page)
			if (err == nil) != test.ok || got != test.want {
				t.Fatal("wrong sealed payload charge", got, err)
			}
		})
	}
	if got, err := linuxEpochAllocatedTreeBytes(10, 8, 4096); err != nil || got != 8192 {
		t.Fatal("statfs units", got, err)
	}
	for _, value := range [][3]uint64{{1, 2, 4096}, {1, 0, 0}, {math.MaxUint64, 0, 4096}} {
		if _, err := linuxEpochAllocatedTreeBytes(value[0], value[1], value[2]); err == nil {
			t.Fatal("invalid tree allocation admitted", value)
		}
	}
	if _, err := linuxEpochChargeAdd(math.MaxInt64, 1); err == nil {
		t.Fatal("aggregate overflow admitted")
	}
}

func TestLinuxEpochResourceRefusalCancelsOnce(t *testing.T) {
	var calls atomic.Int64
	a := &linuxEpochResourceAdmission{cancel: func() { calls.Add(1) }}
	var joined sync.WaitGroup
	for range 16 {
		joined.Go(func() {
			if a.Check(t.Context()) == nil {
				t.Error("unissued binding accepted")
			}
			a.Fail()
		})
	}
	joined.Wait()
	if calls.Load() != 1 || a.Close() == nil || a.RSSLimit() != 4<<30 {
		t.Fatal("failure lost its latch or cleanup prefix", calls.Load())
	}
	if a, err := admitLinuxEpochResources(t.Context(), nil, func() {}); err == nil || a != nil {
		t.Fatal("missing genuine held scope issued authority")
	}
}

func TestLinuxEpochResourceStricterProspectiveBudget(t *testing.T) {
	// Retained v1 refusal facts exercise arithmetic only, never fresh admission.
	observation := linuxEpochResourceTestMemory()
	observation.PhysicalBytes = 15_991_848_960
	observation.AvailableBytes = 8_633_675_776
	got, err := evaluateLinuxEpochResources(observation, 1_914_691_584)
	if err != nil || got.RequiredBytes != 8_357_142_528 || got.HostHeadroomBytes != 1<<30 {
		t.Fatal("prospective stricter constraint", got, err)
	}
	observation.AvailableBytes = got.RequiredBytes - 1
	if got, err := evaluateLinuxEpochResources(observation, 1_914_691_584); err == nil || got != (t4013.LinuxMemoryCalculation{}) {
		t.Fatal("one-byte shortage lost its zero refusal", got, err)
	}
}

func TestLinuxEpochResourceOperationFailureAndKeeperClose(t *testing.T) {
	a := &linuxEpochResourceAdmission{err: errLinuxEpochResources}
	if a.closeCustody() != nil || !a.closed || a.Close() == nil || a.Check(t.Context()) == nil {
		t.Fatal("joined keeper retirement cleared the operational refusal")
	}
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	if file.Close() != nil {
		t.Fatal("fixture close")
	}
	b := &linuxEpochResourceAdmission{}
	b.namespaces[0].file = file
	if b.closeCustody() == nil || b.closeCustody() == nil || b.Close() == nil || !b.closed || b.namespaces[0].file != nil {
		t.Fatal("actual keeper-close failure lost its sticky status")
	}
}

func TestLinuxEpochResourceNamespacePins(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pins, err := captureLinuxEpochResourceNamespaces()
	defer func() { _ = pins.close() }()
	if err != nil || pins.check() != nil {
		t.Fatal("current native namespace pins", err)
	}
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	forged := pins
	forged[0].file = file
	if forged.check() == nil {
		t.Fatal("numeric FD or nonnamespace object accepted")
	}
	if pins.close() != nil || pins.check() == nil || pins.close() != nil {
		t.Fatal("closed/reused namespace pin accepted")
	}
}

func TestLinuxEpochResourceInitialFDInventoryRejectsExtraMemfd(t *testing.T) {
	fd, err := unix.MemfdCreate("extra-resource-test", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	var footprint linuxEpochResourceFootprint
	if checkLinuxEpochInitialAnonymousObjects(footprint) == nil {
		t.Fatal("unbound anonymous keeper omitted")
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil {
		t.Fatal("native fixture identity")
	}
	footprint.Images[0] = linuxEpochImageCharge{Device: stat.Dev, Inode: stat.Ino}
	if err := checkLinuxEpochInitialAnonymousObjects(footprint); err != nil {
		t.Fatal("known object not recognized by inventory", err)
	}
	other, err := unix.MemfdCreate("distinct-extra-resource-test", unix.MFD_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(other) }()
	if checkLinuxEpochInitialAnonymousObjects(footprint) == nil {
		t.Fatal("distinct unbound copy was deduplicated")
	}
}
