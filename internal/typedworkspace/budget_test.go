package typedworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

func budgetInventory(t *testing.T, files []typedindex.BundleFile) typedindex.Inventory {
	t.Helper()
	hash := func(b []byte) string { h := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(h[:]) }
	for i := range files {
		files[i].Digest = hash([]byte(files[i].Path))
	}
	raw, err := json.Marshal(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := typedindex.DecodeInventory(t.Context(), raw, hash(raw))
	if err != nil {
		t.Fatal(err)
	}
	return inventory
}
func TestDeriveOwnerBudgetKnownAllocations(t *testing.T) {
	inv := budgetInventory(t, []typedindex.BundleFile{{Path: "a", Bytes: 1}, {Path: "d/b", Bytes: 4097}})
	// Hand-counted input: two files, two directories. At 4 KiB, extents are
	// 4 KiB + 8 KiB and metadata 16 KiB = 28672. The publication reserves 50348032
	// payload bytes  + 20278 allocation units (20140 nodes  + 138 rounding slack).
	// Fourteen control files round to 190074880 bytes; their 19 metadata nodes
	// add 77824. Total=28672+133406720+190152704=323588096; inodes=4+20140+19.
	for _, tc := range []struct {
		block uint64
		bytes int64
	}{
		{512, 250818048}, {4096, 323588096}, {1 << 20, 21541945344},
	} {
		got, err := DeriveOwnerBudget(t.Context(), inv, tc.block)
		if err != nil || got.Bytes != tc.bytes || got.Inodes != 20163 {
			t.Fatalf("block %d: %+v %v", tc.block, got, err)
		}
	}
	// Zero data still consumes a file inode and metadata block. Crossing one
	// extent boundary changes only that file's payload allocation unit.
	empty := budgetInventory(t, []typedindex.BundleFile{{Path: "a", Bytes: 0}})
	one := budgetInventory(t, []typedindex.BundleFile{{Path: "a", Bytes: 1}})
	exact := budgetInventory(t, []typedindex.BundleFile{{Path: "a", Bytes: 4096}})
	next := budgetInventory(t, []typedindex.BundleFile{{Path: "a", Bytes: 4097}})
	zero, _ := DeriveOwnerBudget(t.Context(), empty, 4096)
	first, _ := DeriveOwnerBudget(t.Context(), one, 4096)
	boundary, _ := DeriveOwnerBudget(t.Context(), exact, 4096)
	beyond, _ := DeriveOwnerBudget(t.Context(), next, 4096)
	if zero.Inodes != 20161 || first.Bytes-zero.Bytes != 4096 || boundary != first || beyond.Bytes-boundary.Bytes != 4096 {
		t.Fatalf("extent accounting: %+v %+v %+v %+v", zero, first, boundary, beyond)
	}
}
func TestDeriveOwnerBudgetMaximumInventory(t *testing.T) {
	files := make([]typedindex.BundleFile, 50000)
	for i := range files {
		name := fmt.Sprintf("f%05d", i)
		if i < 20000 {
			name = fmt.Sprintf("d%05d/f", i)
		}
		files[i].Path = name
		if i < 8 {
			files[i].Bytes = 256 << 20
		}
	}
	inventory := budgetInventory(t, files)
	// Eight 256 MiB extents, 50000 file nodes, 20000 nested directories plus root:
	// 2147483648+70001*4096=2434207744 input bytes. Add the independently
	// counted 133406720 publication and 190152704 controls = 2757767168 bytes.
	got, err := DeriveOwnerBudget(t.Context(), inventory, 4096)
	if err != nil || got.Bytes != 2757767168 || got.Inodes != 90160 {
		t.Fatalf("maximum: %+v %v", got, err)
	}
	if _, err = DeriveOwnerBudget(t.Context(), inventory, 1<<20); err == nil {
		t.Fatal("envelope beyond existing 48 GiB gate ceiling accepted")
	}
}
func TestWorkspaceBudgetBoundsAndCancellation(t *testing.T) {
	inv := budgetInventory(t, []typedindex.BundleFile{{Path: "a", Bytes: 1}})
	for _, block := range []uint64{0, 3, 1<<20 + 1, math.MaxUint64} {
		if _, err := DeriveOwnerBudget(t.Context(), inv, block); err == nil {
			t.Fatalf("block %d accepted", block)
		}
	}
	if _, err := DeriveOwnerBudget(t.Context(), typedindex.Inventory{}, 4096); err == nil {
		t.Fatal("unadmitted inventory accepted")
	}
	//nolint:staticcheck // Deliberate malformed-context refusal regression.
	if _, err := DeriveOwnerBudget(nil, inv, 4096); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := DeriveOwnerBudget(ctx, inv, 4096); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := encodeOwnerInputs(ctx, inv, Receipt{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("input receipt cancellation lost: %v", err)
	}
	for _, tc := range []struct {
		files        []typedindex.BundleFile
		nodes, block uint64
	}{
		{nil, math.MaxUint64, 2},
		{[]typedindex.BundleFile{{Bytes: math.MaxInt64}, {Bytes: math.MaxInt64}}, 0, 4096},
		{[]typedindex.BundleFile{{Bytes: -1}}, 1, 4096},
	} {
		if _, err := allocationBytes(tc.files, tc.nodes, tc.block); err == nil {
			t.Fatal("invalid allocation arithmetic accepted")
		}
	}
}
func TestCapacityObservationGeometry(t *testing.T) {
	valid := CapacityObservation{Device: 1, Inode: 2, BlockSize: 4096, TotalBytes: 40960, FreeBytes: 32768, AvailableBytes: 28672, TotalInodes: 100, FreeInodes: 80}
	if !valid.valid() {
		t.Fatal("valid geometry refused")
	}
	for _, mutate := range []func(*CapacityObservation){
		func(o *CapacityObservation) { o.Inode = 0 }, func(o *CapacityObservation) { o.BlockSize = 0 }, func(o *CapacityObservation) { o.BlockSize = 3 },
		func(o *CapacityObservation) { o.TotalBytes = 0 }, func(o *CapacityObservation) { o.TotalBytes = math.MaxUint64 }, func(o *CapacityObservation) { o.TotalBytes++ },
		func(o *CapacityObservation) { o.FreeBytes = o.TotalBytes + 4096 }, func(o *CapacityObservation) { o.AvailableBytes = o.FreeBytes + 4096 }, func(o *CapacityObservation) { o.AvailableBytes++ },
		func(o *CapacityObservation) { o.TotalInodes = 0 }, func(o *CapacityObservation) { o.FreeInodes = o.TotalInodes + 1 },
	} {
		bad := valid
		mutate(&bad)
		if bad.valid() {
			t.Fatalf("bad geometry accepted: %+v", bad)
		}
	}
}
func TestWorkspaceBudgetUsesSharedControlCeilings(t *testing.T) {
	// These are frozen existing container decoder limits, now exported for shared
	// budgeting. The arithmetic goldens above must change consciously with them.
	if typedsandbox.MaxContainerJournalBytes != 8192 || typedsandbox.MaxScratchAuthorityBytes != 4096 {
		t.Fatal("native control ceiling changed")
	}
}
