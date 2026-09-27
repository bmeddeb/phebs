package typedworkspace

import (
	"context"
	"time"
)

const MaxDrainDeletes = 16
const MaxDrainSteps = 48
const DrainLockWait = 100 * time.Millisecond

// Four measured stats per traversal step plus a conservative 24 fixed stats for
// authority/cursor checks and terminal cleanup (guard helper stats are separate).
const MaxDrainTraversalStats = 4*MaxDrainSteps + 24
const MaxDrainMarkerBytes = 4096

// DrainAuthority comes from trusted controller state, never from an HTTP request
// or an untrusted filesystem census. The controller MUST already have fenced the
// request irreversibly collecting and proved native child/container quiescence.
// These fields identify custody; by themselves they do not grant deletion rights.
type DrainAuthority struct {
	PlanningDigest  string `json:"planning_digest"`
	AttemptDigest   string `json:"attempt_digest"`
	ManifestDigest  string `json:"manifest_digest"`
	DirectoryDevice uint64 `json:"directory_device"`
	DirectoryInode  uint64 `json:"directory_inode"`
}

func RetirementAuthority(m OwnerManifest) (DrainAuthority, error) {
	digest := m.Digest()
	if digest == "" {
		return DrainAuthority{}, ErrCustody
	}
	return DrainAuthority{m.Identity.PlanningDigest, m.Identity.AttemptDigest, digest, m.Directory.Device, m.Directory.Inode}, nil
}

// DrainReport counts actual owner-issued work, excluding the reused guard/private
// ancestry helpers' fixed path validation. Deleted includes successful marker
// replacements as well as unlink/rmdir, so it never exceeds 16. TraversalStats and
// Openat2Calls are separate from GuardAcquisitions/PrivateRootOpens; no 256-total-
// stat claim is made. The two kernel guards plus transient helpers and traversal
// descriptors have a static maximum of 8 live descriptors. No child is launched.
type DrainReport struct {
	Deleted             int
	DeleteCalls         int
	Steps               int
	TraversalStats      int
	Openat2Calls        int
	DirectoryReads      int
	DirectoryEntries    int
	MarkerWrites        int
	ControlBytesCharged int
	Syncs               int
	Chmods              int
	GuardAcquisitions   int
	PrivateRootOpens    int
	Done                bool
	Held                bool
}

// DrainOwner drains one exact private attempt, at most 16 deletes and 48 traversal
// steps. It retains owner.json until a durable terminal marker; every initial
// collection matches its canonical bytes to ManifestDigest. After terminal
// marker removal, only the trusted exact EMPTY directory inode permits final
// rmdir. Each kernel guard acquisition has a 100ms deadline; a pinned attempt
// releases the base guard promptly. This does not bound filesystem syscall time.
// A missing attempt is synced as absent. Request parents are removed only
// empty, never renamed wholesale. Unsupported openat2/kernel/platform refuses.
//
// Below the exact private root, announced inputs-/bundle- trees (including stage
// residue) admit service-owned, same-device regular files and directories only;
// no symlinks, hardlinks, mount crossings or special files. Known receipt controls
// retain exact inode binding. Interior files are not rehashed or re-inventoried
// against giant receipts: the controller's retirement/quiescence fence and the
// private directory boundary are the destructive authority. Announced abandoned
// owner.next and unreferenced inventory/receipt controls admit safe regular-file
// residue without interpreting their contents. Referenced receipts retain inode
// binding; owner.json and collecting authority controls require strict decoding.
// Unknown siblings and malformed collecting.next remain held. Container journal
// siblings are deliberately unknown: exact native cleanup must remove them first.
// Privileged host scratch is outside this drainer's namespace.
func DrainOwner(ctx context.Context, base string, a DrainAuthority) (DrainReport, error) {
	return drainOwner(ctx, base, a, nil)
}

// DrainInspection is metadata-only. Resume is meaningful only with the caller's
// irreversible collecting fence and exact original persisted DrainAuthority.
// It never grants native quiescence to a live owner. Absent proves only this
// attempt's absence, not the complete request namespace's absence.
type DrainInspection struct {
	Live   *OwnerManifest
	Resume bool
	Absent bool
}

// InspectDrainOwner authenticates live custody or an interrupted drain prefix
// without changing files. The same bounded marker/inode grammar as DrainOwner
// applies. Unknown top-level entries, conflicting pending markers and replaced
// inodes refuse. No source or receipt payload is read or hashed.
func InspectDrainOwner(ctx context.Context, base string, expected Node, authority DrainAuthority) (DrainInspection, error) {
	return inspectDrainOwner(ctx, base, expected, authority)
}

// InspectDrainNamespace inventories at most64 attempts plus one overflow sentinel
// beneath one exact request. Absent is true only when the request directory is
// absent, never merely empty. Entry names are discovery, not deletion authority.
func InspectDrainNamespace(ctx context.Context, base, planning string, expected Node) (entries []OwnerCensusEntry, absent bool, err error) {
	return inspectDrainNamespace(ctx, base, planning, expected)
}
