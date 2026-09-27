//go:build linux

package typedexecutor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
	"golang.org/x/sys/unix"
)

func TestTypedLifecycleDrainRestartAndPins(t *testing.T) {
	endpoint := testServer(t)
	f, w := completeFixture(t, endpoint, "lifecycle")
	events, begins := installNeutralNative(t, f, w, "")
	ctx := t.Context()
	prepared, e := f.c.Prepare(ctx, f.chunk, f.source, f.raw)
	if e != nil {
		t.Fatal(e)
	}
	id := prepared.Manifest.Identity
	owner := LifecycleOwner{Controller: f.c}
	sweep := func(cursor string) lifecycle.OwnerResult {
		t.Helper()
		r := owner.Sweep(ctx, time.Now(), cursor, lifecycle.DefaultLimits())
		if r.Deleted > 16 || r.Deleted < 0 {
			t.Fatal("cap", r)
		}
		return r
	}
	// Running lease and active desired request cannot authorize retirement.
	protected := sweep("")
	if protected.Err == nil || protected.Deleted != 0 {
		t.Fatal("running holder", protected)
	}
	if e = f.s.FailGenerationChunk(ctx, f.chunk, "finished"); e != nil {
		t.Fatal(e)
	}
	release := sweep("")
	if release.Err != nil || release.Deleted != 1 {
		t.Fatal("release turn", release)
	}
	protected = sweep("")
	if protected.Err != nil || protected.Deleted != 0 {
		t.Fatal("desired", protected)
	}
	if e = f.s.CancelTypedIndex(ctx, f.chunk.Repository, id.PlanningDigest); e != nil {
		t.Fatal(e)
	}
	mark := sweep("")
	if mark.Err != nil || mark.Deleted != 3 {
		t.Fatal("retirement turn", mark)
	}
	attempt := filepath.Join(f.c.config.Workspace, id.RelativeName())
	lock, e := os.Open(filepath.Join(attempt, ".phebs-index-publication.lock"))
	if e != nil {
		t.Fatal(e)
	}
	if e = unix.Flock(int(lock.Fd()), unix.LOCK_SH|unix.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	pinned := sweep(mark.Cursor)
	if pinned.Err == nil || pinned.Deleted != 0 {
		t.Fatal("reader pin", pinned)
	}
	_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	_ = lock.Close()
	// A controlled extra private regular-file tree forces multiple physical turns;
	// source content is never rehashed by retirement.
	input := filepath.Join(attempt, prepared.Manifest.InputName)
	if e = os.Chmod(input, 0700); e != nil {
		t.Fatal(e)
	}
	for n := range 40 {
		if e = os.WriteFile(filepath.Join(input, fmt.Sprintf("owned-%02d", n)), []byte("private"), 0444); e != nil {
			t.Fatal(e)
		}
	}
	unknown := filepath.Join(attempt, "unknown")
	if e = os.WriteFile(unknown, []byte("held"), 0600); e != nil {
		t.Fatal(e)
	}
	blocked := sweep(mark.Cursor)
	if blocked.Err == nil || blocked.Deleted != 0 {
		t.Fatal("unknown", blocked)
	}

	suffix := sweep(encodeLifecycleCursor(lifecycleCursor{After: id.PlanningDigest}))
	if suffix.Err != nil || !suffix.More || suffix.Completeness == lifecycle.Exact || suffix.Cursor != "" {
		t.Fatal("held suffix claimed drained", suffix)
	}
	freshPass := sweep(suffix.Cursor)
	if freshPass.Err == nil || freshPass.Deleted != 0 {
		t.Fatal("unknown lost on next pass", freshPass)
	}
	if e = os.Remove(unknown); e != nil {
		t.Fatal(e)
	}
	cursor := mark.Cursor
	physical, dbTurns := 0, 0
	for n := 0; n < 30; n++ {
		r := sweep(cursor)
		if r.Err != nil {
			t.Fatal("drain", n, r)
		}
		cursor = r.Cursor
		_, absent, e := typedworkspace.InspectDrainNamespace(ctx, f.c.config.Workspace, id.PlanningDigest, drainBase(f.c.workspace))
		if e != nil {
			t.Fatal(e)
		}
		if !absent {
			physical++
		} else if r.Deleted > 0 {
			dbTurns++
		}
		// Real startup from a fresh controller must accept exact durable collecting
		// prefixes without reloading deleted input/publication receipt controls.
		fresh, e := New(f.c.config)
		if e != nil {
			t.Fatal(e)
		}
		fresh.observeHost = f.c.observeHost
		fresh.native = f.c.native
		if e = fresh.Startup(ctx); e != nil {
			t.Fatal("fresh collecting restart", n, e)
		}
		f.c = fresh
		owner.Controller = fresh
		if absent {
			selected, e := f.s.InspectTypedIndexCollection(ctx, id.PlanningDigest)
			if e != nil {
				t.Fatal(e)
			}
			if len(selected.Attempts()) == 0 {
				break
			}
		}
		if n == 29 {
			t.Fatal("did not drain")
		}
	}
	if physical < 2 || dbTurns < 1 || *begins != 0 {
		t.Fatal("wrong work", physical, dbTurns, *begins, *events)
	}
	selected, e := f.s.InspectTypedIndexCollection(ctx, id.PlanningDigest)
	if e != nil || len(selected.Attempts()) != 0 {
		t.Fatal("controls retained", e)
	}
	if _, e = f.s.InspectTypedIndexTombstoneExpiry(ctx, id.PlanningDigest); e == nil {
		t.Fatal("cancellation incorrectly expires")
	}
	// A replaced workspace cannot turn missing files into collection authority.
	old := f.c.config.Workspace
	replacement := t.TempDir()
	if e = os.WriteFile(filepath.Join(replacement, ".phebs-index-publication.lock"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	f.c.config.Workspace = replacement
	if r := sweep(cursor); r.Err == nil || r.Deleted != 0 {
		t.Fatal("replaced base", r)
	}
	f.c.config.Workspace = old
	if r := sweep(cursor); r.Err != nil {
		t.Fatal("restored base", r)
	}
}

func TestTypedLifecycleEarlyCustodyHeld(t *testing.T) {
	endpoint := testServer(t)
	f, w := completeFixture(t, endpoint, "lifecycle-early")
	_, _ = installNeutralNative(t, f, w, "")
	ctx := t.Context()
	prepared, e := f.c.Prepare(ctx, f.chunk, f.source, f.raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.FailGenerationChunk(ctx, f.chunk, "finished"); e != nil {
		t.Fatal(e)
	}
	// Native absence refusal preserves held growth and every physical byte.
	original := f.c.native.quiescent
	f.c.native.quiescent = func(context.Context, typedsandbox.RecoveryOptions) error { return ErrHeld }
	r := (LifecycleOwner{f.c}).Sweep(ctx, time.Now(), "", lifecycle.DefaultLimits())
	if r.Err == nil || r.Deleted != 0 {
		t.Fatal(r)
	}
	if _, e = f.s.GetTypedIndexGrowth(ctx); e != nil {
		t.Fatal("lost promise", e)
	}
	if _, e = typedworkspace.LoadOwner(ctx, f.c.config.Workspace, prepared.Manifest.Identity); e != nil {
		t.Fatal("lost owner", e)
	}
	f.c.native.quiescent = original
	r = (LifecycleOwner{f.c}).Sweep(ctx, time.Now(), "", lifecycle.DefaultLimits())
	if r.Err != nil || r.Deleted != 1 {
		t.Fatal("restored", r)
	}
	if _, e = f.s.GetTypedIndexGrowth(ctx); !errors.Is(e, store.ErrNotFound) {
		t.Fatal(e)
	}
}

func TestTypedLifecycleStartupTerminalPrefixes(t *testing.T) {
	endpoint := testServer(t)
	f, w := completeFixture(t, endpoint, "lifecycle-terminal")
	installNeutralNative(t, f, w, "")
	ctx := t.Context()
	prepared, e := f.c.Prepare(ctx, f.chunk, f.source, f.raw)
	if e != nil {
		t.Fatal(e)
	}
	id := prepared.Manifest.Identity
	if e = f.s.FailGenerationChunk(ctx, f.chunk, "finished"); e != nil {
		t.Fatal(e)
	}
	if e = f.c.AfterSettlement(ctx, id.AttemptDigest); e != nil {
		t.Fatal(e)
	}
	if e = f.s.CancelTypedIndex(ctx, f.chunk.Repository, id.PlanningDigest); e != nil {
		t.Fatal(e)
	}
	retired, e := f.s.InspectTypedIndexRetirement(ctx, id.PlanningDigest)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.BeginTypedIndexRetirement(ctx, retired); e != nil {
		t.Fatal(e)
	}
	attempt := filepath.Join(f.c.config.Workspace, id.RelativeName())
	// Reproduce each terminal crash prefix using exact canonical marker authority.
	// Only this test constructs synthetic persisted states; production inspection
	// cannot create/adopt a marker and production DrainOwner has crash-hook tests.
	entries, e := os.ReadDir(attempt)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.Name() == "owner.json" || entry.Name() == ".phebs-index-publication.lock" {
			continue
		}
		p := filepath.Join(attempt, entry.Name())
		_ = filepath.WalkDir(p, func(path string, d os.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
		if e = os.RemoveAll(p); e != nil {
			t.Fatal(e)
		}
	}
	a, e := typedworkspace.RetirementAuthority(prepared.Manifest)
	if e != nil {
		t.Fatal(e)
	}
	marker := struct {
		Schema      string                        `json:"schema"`
		Authority   typedworkspace.DrainAuthority `json:"authority"`
		Revision    uint64                        `json:"revision"`
		Previous    string                        `json:"previous"`
		Terminal    bool                          `json:"terminal"`
		Cursor      string                        `json:"cursor"`
		CursorInode uint64                        `json:"cursor_inode"`
	}{"phebs-typed-collecting-v1", a, 2, testHash([]byte("previous durable marker")), true, ".", a.DirectoryInode}
	if e = os.WriteFile(filepath.Join(attempt, "collecting.json"), encode(t, marker), 0444); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"", "owner.json", ".phebs-index-publication.lock", "collecting.json", "attempt", "root"} {
		switch name {
		case "":
		case "attempt":
			e = os.Remove(attempt)
		case "root":
			e = os.Remove(filepath.Dir(attempt))
		default:
			e = os.Remove(filepath.Join(attempt, name))
		}
		if e != nil {
			t.Fatal(name, e)
		}
		fresh, err := New(f.c.config)
		if err != nil {
			t.Fatal(err)
		}
		fresh.observeHost = f.c.observeHost
		fresh.native = f.c.native
		if err = fresh.Startup(ctx); err != nil {
			t.Fatal("startup prefix", name, err)
		}
	}
}

func TestTypedLifecycleCurrentProtected(t *testing.T) {
	endpoint := testServer(t)
	f, w := completeFixture(t, endpoint, "lifecycle-current")
	installNeutralNative(t, f, w, "")
	ctx := t.Context()
	outcome, e := f.c.Execute(ctx, f.chunk, f.source, f.raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.CompleteGenerationChunk(ctx, f.chunk); e != nil {
		t.Fatal(e)
	}
	if e = f.c.AfterSettlement(ctx, outcome.AttemptDigest); e != nil {
		t.Fatal(e)
	}
	request, e := f.s.ResolveTypedIndexCurrent(ctx, f.chunk.Repository)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.CancelTypedIndex(ctx, f.chunk.Repository, request.Binding.RequestDigest); e != nil {
		t.Fatal(e)
	}
	r := (LifecycleOwner{f.c}).Sweep(ctx, time.Now(), "", lifecycle.DefaultLimits())
	if r.Err != nil || r.Deleted != 0 {
		t.Fatal("current protection", r)
	}
	current, e := f.s.ResolveTypedIndexCurrent(ctx, f.chunk.Repository)
	if e != nil || current != request {
		t.Fatal("current changed", e)
	}
}
