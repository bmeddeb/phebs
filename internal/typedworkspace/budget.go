package typedworkspace

import (
	"context"
	"errors"
	"math"
	"os"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

// CapacityObservation describes one actual opened private filesystem root.
// AvailableBytes excludes reserved blocks; FreeBytes includes them. Linux
// statfs supplies Ffree for FreeInodes. This is neither a reservation against
// unrelated writers nor permission to launch native work or grow custody.
type CapacityObservation struct {
	Device, Inode, BlockSize              uint64
	TotalBytes, FreeBytes, AvailableBytes uint64
	TotalInodes, FreeInodes               uint64
}

func validBudgetBlock(block uint64) bool { return block > 0 && block <= 1<<20 && block&(block-1) == 0 }
func (o CapacityObservation) valid() bool {
	return o.Inode != 0 && validBudgetBlock(o.BlockSize) && o.TotalBytes > 0 && o.TotalBytes <= math.MaxInt64 && o.FreeBytes <= o.TotalBytes && o.AvailableBytes <= o.FreeBytes && o.TotalBytes%o.BlockSize == 0 && o.FreeBytes%o.BlockSize == 0 && o.AvailableBytes%o.BlockSize == 0 && o.TotalInodes > 0 && o.FreeInodes <= o.TotalInodes
}

// ObserveCapacity performs two bounded private ancestry opens, fstat/fstatfs and
// an exact same-directory recheck. At most four descriptors coexist; no lock,
// file, directory, subprocess, namespace census or pressure latch is created.
// A later controller must group this device with the actual host backing root,
// apply its persistent destination Gate, and acquire the durable growth holder.
func ObserveCapacity(ctx context.Context, privateBase string) (CapacityObservation, error) {
	return observeCapacity(ctx, privateBase, capacity)
}
func observeCapacity(ctx context.Context, base string, probe func(*os.File) (space, error)) (_ CapacityObservation, err error) {
	if ctx == nil {
		return CapacityObservation{}, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return CapacityObservation{}, err
	}
	dir, err := openDirectory(base, true)
	if err != nil {
		return CapacityObservation{}, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	node, err := directoryInfo(dir, ".", false)
	if err != nil {
		return CapacityObservation{}, err
	}
	actual, err := probe(dir)
	if err != nil {
		return CapacityObservation{}, err
	}
	if err = ctx.Err(); err != nil {
		return CapacityObservation{}, err
	}
	if err = ownerSameDirectory(dir, base); err != nil {
		return CapacityObservation{}, err
	}
	if err = ctx.Err(); err != nil {
		return CapacityObservation{}, err
	}
	o := CapacityObservation{Device: node.Device, Inode: node.Inode, BlockSize: actual.block, TotalBytes: actual.total, FreeBytes: actual.free, AvailableBytes: actual.bytes, TotalInodes: actual.totalInodes, FreeInodes: actual.inodes}
	if !o.valid() {
		return CapacityObservation{}, ErrCustody
	}
	return o, nil
}

// DeriveOwnerBudget is the complete prospective workspace envelope for ONE new
// attempt. Admit it once against actual free capacity before any attempt growth;
// retained files are already in statfs and their old budgets are never added.
// Later Copy/Install/control writes retain their existing incremental checks.
//
// Inputs use their exact admitted inventory, including rounded extents and one
// metadata unit per node. Publication reserves its existing aggregate+plan+two
// request ceilings, all allowed files' rounding slack and directory metadata.
// Controls reserve inventory/input/publication receipts, owner main+pending,
// collecting main+pending, container journal main+pending and both standalone
// phase snapshots. Those occupy two fixed, create-only phase directories, with
// inventory/parent/profile/scratch duplicated, one execution request/plan and two seals.
// No control
// may be injected into an already sealed copied inventory.
//
// Fixed metadata covers two lock files and request/attempt/two-phase directories;
// shared namespace entries may already exist, so this is a conservative future
// ceiling, not an estimate of new actual usage. Copy/publication stage-to-final
// renames do not duplicate their trees. Host image, host journal and loop inode
// geometry belong to the separately observed backing domain, not this budget.
//
// Cost: one bounded inventory clone/layout (<=50,000 files/20,001 directories),
// directory sort, a linear extent sum and constant control arithmetic. No source
// reads, hashes, filesystem access, persistent state, locks or children. Shared
// layout construction also adds per-file/per-entry cancellation checks to
// existing Copy/Verify/input-receipt paths, without new I/O or allocation shape.
func DeriveOwnerBudget(ctx context.Context, inventory typedindex.Inventory, block uint64) (OwnerBudget, error) {
	if ctx == nil || !validBudgetBlock(block) {
		return OwnerBudget{}, ErrCustody
	}
	if err := ctx.Err(); err != nil {
		return OwnerBudget{}, err
	}
	tree, err := inventoryLayout(ctx, inventory)
	if err != nil {
		return OwnerBudget{}, err
	}
	inputs, err := neededSpace(tree, block)
	if err != nil {
		return OwnerBudget{}, err
	}
	// Sum ceil(fileBytes/block) is at most ceil(total/block)+(fileCount-1).
	// Reuse the same extent/metadata arithmetic without inventing publication files.
	publicationNodes := uint64(maxPublicationFiles + typedindex.MaxInventoryDirectories + 1)
	publication, err := allocationBytes([]typedindex.BundleFile{{Bytes: maxInstalledPublicationBytes}}, publicationNodes+uint64(maxPublicationFiles-1), block)
	if err != nil {
		return OwnerBudget{}, err
	}
	controls := []typedindex.BundleFile{
		{Bytes: typedindex.MaxInventoryBytes}, {Bytes: MaxInputReceiptBytes}, {Bytes: MaxPublicationReceiptBytes},
		{Bytes: MaxOwnerBytes}, {Bytes: MaxOwnerBytes},
		{Bytes: MaxDrainMarkerBytes}, {Bytes: MaxDrainMarkerBytes},
		{Bytes: typedsandbox.MaxContainerJournalBytes}, {Bytes: typedsandbox.MaxContainerJournalBytes},
		{Bytes: typedsandbox.MaxScratchAuthorityBytes},
		{Bytes: typedindex.MaxRequestBytes}, {Bytes: typedindex.MaxRequestBytes},
		{Bytes: typedindex.MaxProfileBytes}, {Bytes: typedindex.MaxPlanBytes},
		// Second retained phase duplicates parent, profile and fresh scratch authority.
		{Bytes: typedindex.MaxRequestBytes}, {Bytes: typedindex.MaxProfileBytes}, {Bytes: typedsandbox.MaxScratchAuthorityBytes},
		{Bytes: MaxControlSealBytes}, {Bytes: MaxControlSealBytes},
		{Bytes: typedindex.MaxInventoryBytes}, {Bytes: typedindex.MaxInventoryBytes},
	}
	// Twenty-one controls, two lock files, request/attempt and two phase directories.
	controlNodes := uint64(len(controls) + 2 + 4)
	controlBytes, err := allocationBytes(controls, controlNodes, block)
	if err != nil {
		return OwnerBudget{}, err
	}
	total := inputs
	for _, n := range []uint64{publication, controlBytes} {
		if n > math.MaxUint64-total {
			return OwnerBudget{}, ErrCustody
		}
		total += n
	}
	if err = ctx.Err(); err != nil {
		return OwnerBudget{}, err
	}
	if total > uint64(lifecycle.MaxPressureDependentAdmissionBytes) {
		return OwnerBudget{}, ErrCustody
	}
	budget := OwnerBudget{Bytes: int64(total), Inodes: uint64(len(tree.entries)) + publicationNodes + controlNodes}
	if !budget.valid() {
		return OwnerBudget{}, ErrCustody
	}
	return budget, nil
}
