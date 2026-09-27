package typedsandbox

import "context"

// HostCapacity contains facts from the opened base descriptor, not a reservation
// or pressure decision. The controller groups actual devices before admission.
type HostCapacity struct {
	Device, Inode, BlockSize, TotalBytes, FreeBytes, AvailableBytes, TotalInodes, FreeInodes uint64
}

// HostJournalObservation is canonical journal data, not observed native state.
// Even Phase "ready" proves neither live containment nor cleanup. The controller
// must match Options to its authenticated, durable per-lease growth holder.
type HostJournalObservation struct {
	Name                    string
	Options                 HostScratchOptions
	Phase                   string
	ImageDevice, ImageInode uint64
	Loop                    int
}

type HostObservation struct {
	Capacity HostCapacity
	Names    []string
	Overflow bool
	Held     bool
	Selected *HostJournalObservation
}

// ObserveHostScratch reads the fixed root-provisioned host namespace. The
// operator must also provision its existing root:root 0600, empty, single-link
// .lock; observation never creates or repairs it. Empty selectedName requests
// only names/capacity; otherwise select one exact 64-character root basename.
// It returns at most two non-lock names (three raw entries), five selected direct
// entries and two 8KiB controls. Overflow cannot prove an omitted name absent.
// Unknown/legacy/partial/pending custody is held. No pending journal is promoted,
// scratch contents or devices inspected, daemon contacted, or child launched.
// Held=false is only a closed observation, never growth or native authorization.
func ObserveHostScratch(ctx context.Context, selectedName string) (HostObservation, error) {
	return observeHostNamespace(ctx, selectedName)
}
