package typedexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

// LifecycleOwner is deliberately unregistered. It uses the controller's existing
// serialization and lifecycle guard; Startup must complete the global census
// before selected bounded turns. No turn launches or replays a worker.
type LifecycleOwner struct{ Controller *Controller }

func (LifecycleOwner) Name() string { return lifecycle.TypedIndexOwner }

type lifecycleCursor struct {
	After string `json:"after"`
	Root  string `json:"root"`
}

func encodeLifecycleCursor(v lifecycleCursor) string {
	if v == (lifecycleCursor{}) {
		return ""
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}
func decodeLifecycleCursor(raw string) (lifecycleCursor, error) {
	var v lifecycleCursor
	if raw == "" {
		return v, nil
	}
	if len(raw) > 180 || json.Unmarshal([]byte(raw), &v) != nil || encodeLifecycleCursor(v) != raw || v.After != "" && !lifecycleDigest(v.After) || v.Root != "" && !lifecycleDigest(v.Root) || v.Root != "" && v.Root <= v.After {
		return v, ErrHeld
	}
	return v, nil
}
func lifecycleDigest(v string) bool {
	if len(v) != 71 || !strings.HasPrefix(v, "sha256:") {
		return false
	}
	for _, r := range v[7:] {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return false
		}
	}
	return true
}

// Sweep performs one release, retirement, physical drain, control batch or
// tombstone expiry, never combining their mutation budgets. A protected/held
// root advances discovery to avoid starving unrelated roots. LowerBound/More
// preserve truthful backlog rather than claiming an empty installation.
func (owner LifecycleOwner) Sweep(ctx context.Context, _ time.Time, raw string, limits lifecycle.Limits) (result lifecycle.OwnerResult) {
	result = lifecycle.OwnerResult{Owner: owner.Name(), Cursor: raw, Completeness: lifecycle.Unavailable}
	cursor, err := decodeLifecycleCursor(raw)
	if err != nil || owner.Controller == nil || limits.Candidates < 1 || limits.Deletes < 16 || limits.Descriptors < 8 {
		result.Err = ErrHeld
		return result
	}
	c := owner.Controller
	release, err := c.enter(ctx)
	if err != nil {
		result.Err = err
		return result
	}
	defer release()
	if !c.ready {
		result.Err = ErrUnavailable
		return result
	}
	observed, err := typedworkspace.ObserveCapacity(ctx, c.config.Workspace)
	if err != nil || !sameBase(c.workspace, observed) {
		result.Err = errors.Join(ErrHeld, err)
		return result
	}

	// Hard death can lose the scheduler callback. Reconcile exactly one retained
	// holder first; release itself is this turn's sole database mutation.
	holder, err := c.config.Store.GetTypedIndexGrowth(ctx)
	if err == nil {
		result.Scanned = 1
		result.More = true
		if err = c.recoverHeld(ctx, holder.AttemptDigest); err != nil {
			result.Err = err
			return result
		}
		result.Deleted = 1
		result.Completeness = lifecycle.LowerBound
		return result
	}
	if !errors.Is(err, store.ErrNotFound) {
		result.Err = err
		return result
	}
	if cursor.Root == "" {
		page, e := c.config.Store.ScanTypedIndexRoots(ctx, cursor.After, 1)
		if e != nil {
			result.Err = e
			return result
		}
		if len(page.Rows) == 0 {
			result.Cursor = ""
			result.Completeness = lifecycle.Exact
			// An empty keyset suffix is not a full-installation absence proof.
			// Retained/protected/held roots keep a conservative backlog until a
			// subsequent first-page probe proves the whole table empty.
			if cursor.After != "" {
				result.More = true
				result.Completeness = lifecycle.LowerBound
			}
			return result
		}
		row := page.Rows[0]
		result.Scanned = 1
		if !row.Parent {
			result.Cursor = encodeLifecycleCursor(lifecycleCursor{After: row.ID})
			result.More = true
			result.Completeness = lifecycle.LowerBound
			return result
		}
		cursor.Root = row.ID
	}
	result.Scanned = 1
	result.More = true
	result.Cursor = encodeLifecycleCursor(cursor)
	held := func(e error) lifecycle.OwnerResult {
		result.Cursor = encodeLifecycleCursor(lifecycleCursor{After: cursor.Root})
		result.Completeness = lifecycle.Unavailable
		result.Err = errors.Join(ErrHeld, e)
		result.AdvanceOnError = result.Cursor != raw
		return result
	}
	retirement, err := c.config.Store.InspectTypedIndexRetirement(ctx, cursor.Root)
	if err != nil {
		return held(err)
	}
	if retirement.Protected() {
		result.Cursor = encodeLifecycleCursor(lifecycleCursor{After: cursor.Root})
		result.Completeness = lifecycle.LowerBound
		return result
	}
	if !retirement.Collecting() {
		if err = c.config.Store.BeginTypedIndexRetirement(ctx, retirement); err != nil {
			return held(err)
		}
		result.Deleted = 3 // exact three supplied operands, including conditional schedule retirement
		result.Completeness = lifecycle.LowerBound
		return result
	}
	selected, err := c.config.Store.InspectTypedIndexCollection(ctx, cursor.Root)
	if err != nil {
		return held(err)
	}
	entries, absent, err := typedworkspace.InspectDrainNamespace(ctx, c.config.Workspace, cursor.Root, drainBase(c.workspace))
	if err != nil {
		return held(err)
	}
	attempts := selected.Attempts()
	if !absent {
		// Authenticate the entire bounded namespace before choosing one directory;
		// unknown entries must not be skipped because their name sorts later.
		for _, entry := range entries {
			if entry.Held || !entry.Directory || !containsAttempt(attempts, "sha256:"+entry.Name) {
				return held(ErrHeld)
			}
		}
		if len(entries) == 0 {
			// DrainOwner safely removes an empty request after an interrupted final
			// attempt rmdir. Retain one exact custody row until that prefix is gone.
			if len(attempts) == 0 {
				return held(ErrHeld)
			}
			entries = []typedworkspace.OwnerCensusEntry{{Name: attempts[0][7:], Directory: true}}
		}
		attempt, err := c.config.Store.InspectTypedIndexAttempt(ctx, "sha256:"+entries[0].Name)
		if err != nil || attempt.Custody == nil || attempt.Growth == nil || attempt.Growth.State != "released" {
			return held(err)
		}
		authority := drainAuthority(*attempt.Custody)
		inspected, err := typedworkspace.InspectDrainOwner(ctx, c.config.Workspace, drainBase(c.workspace), authority)
		if err != nil {
			return held(err)
		}
		if inspected.Live != nil {
			if err = c.quiescentCollectingOwner(ctx, *inspected.Live); err != nil {
				return held(err)
			}
		} else if !inspected.Resume && !inspected.Absent {
			return held(ErrHeld)
		}
		report, err := typedworkspace.DrainOwner(ctx, c.config.Workspace, authority)
		result.Deleted = report.Deleted
		if err != nil {
			return held(err)
		}
		result.Completeness = lifecycle.LowerBound
		return result
	}
	// A fresh complete physical-root absence observation precedes EVERY batch.
	batch, err := c.config.Store.CollectDrainedTypedIndexControlsBatch(ctx, selected, attempts)
	if err != nil {
		return held(err)
	}
	if batch.Mutations > 0 {
		result.Deleted = batch.Mutations
		result.Completeness = lifecycle.LowerBound
		return result
	}
	expiry, err := c.config.Store.InspectTypedIndexTombstoneExpiry(ctx, cursor.Root)
	if errors.Is(err, typedindex.Stale) {
		result.Cursor = encodeLifecycleCursor(lifecycleCursor{After: cursor.Root})
		result.Completeness = lifecycle.LowerBound
		return result
	}
	if err != nil {
		return held(err)
	}
	if err = c.config.Store.ExpireDrainedTypedIndexTombstone(ctx, expiry); err != nil {
		return held(err)
	}
	result.Deleted = 1
	result.Cursor = encodeLifecycleCursor(lifecycleCursor{After: cursor.Root})
	result.Completeness = lifecycle.LowerBound
	return result
}
func containsAttempt(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
func drainAuthority(a store.TypedIndexCustody) typedworkspace.DrainAuthority {
	return typedworkspace.DrainAuthority{PlanningDigest: a.PlanningDigest, AttemptDigest: a.AttemptDigest, ManifestDigest: a.ManifestDigest, DirectoryDevice: a.DirectoryDevice, DirectoryInode: a.DirectoryInode}
}
func (c *Controller) quiescentCollectingOwner(ctx context.Context, m typedworkspace.OwnerManifest) error {
	if m.InputName == "" || c.config.Socket == "" || c.config.Image == "" {
		return ErrHeld
	}
	a := m.Identity
	host, err := c.observeHost(ctx, "")
	if err != nil || host.Held || host.Overflow {
		return errors.Join(ErrHeld, err)
	}
	options := typedsandbox.HostScratchOptions{Base: typedsandbox.HostBaseIdentity{Device: host.Capacity.Device, Inode: host.Capacity.Inode, BlockSize: host.Capacity.BlockSize}, RequestDigest: a.PlanningDigest, AttemptDigest: a.AttemptDigest, Socket: c.config.Socket}
	name, err := typedsandbox.HostScratchRootName(a.PlanningDigest, a.AttemptDigest)
	if err != nil {
		return err
	}
	selected, err := c.observeHost(ctx, name)
	if err != nil || selected.Overflow {
		return errors.Join(ErrHeld, err)
	}
	if selected.Selected != nil {
		recorded := selected.Selected.Options
		if recorded.Base != options.Base || recorded.RequestDigest != options.RequestDigest || recorded.AttemptDigest != options.AttemptDigest || recorded.Socket != options.Socket {
			return ErrHeld
		}
		options = recorded
	}

	pinCtx, pinCancel := context.WithTimeout(ctx, typedworkspace.DrainLockWait)
	defer pinCancel()
	unlock, err := typedworkspace.AcquirePublicationMutation(pinCtx, filepath.Join(c.config.Workspace, m.RelativeName()))
	if err != nil {
		return err
	}
	defer unlock()
	stop, cancel := stopContext(ctx)
	defer cancel()
	return c.cleanNative(stop, typedsandbox.RecoveryOptions{Socket: c.config.Socket, ImageID: c.config.Image, Inputs: filepath.Join(c.config.Workspace, m.RelativeName(), m.InputName), PlanningDigest: a.PlanningDigest, AttemptDigest: a.AttemptDigest}, options)
}

func drainBase(o typedworkspace.CapacityObservation) typedworkspace.Node {
	return typedworkspace.Node{Device: o.Device, Inode: o.Inode, Directory: true}
}
