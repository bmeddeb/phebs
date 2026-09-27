//go:build linux

package typedworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func drainFixture(t *testing.T) (string, DrainAuthority, string) {
	t.Helper()
	f, _, _, _, _, m := ownerFixture(t)
	a, err := RetirementAuthority(m)
	if err != nil {
		t.Fatal(err)
	}
	attempt := filepath.Join(f.dir, m.RelativeName())
	return f.dir, a, attempt
}
func makeDrainTree(t *testing.T, attempt string, deep, wide int) string {
	t.Helper()
	top := "inputs-" + strings.Repeat("a", 32) + ".stage"
	dir := filepath.Join(attempt, top)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < deep; i++ {
		dir = filepath.Join(dir, "d")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < wide; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d", i)), []byte("owned"), 0444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	return top
}
func finishDrain(t *testing.T, base string, a DrainAuthority) []DrainReport {
	t.Helper()
	var reports []DrainReport
	for i := 0; i < 200; i++ {
		r, err := DrainOwner(t.Context(), base, a)
		if err != nil {
			t.Fatal("drain held", i, r, err)
		}
		if r.Deleted > MaxDrainDeletes || r.Steps > MaxDrainSteps {
			t.Fatal("operation cap", r)
		}
		reports = append(reports, r)
		if r.Done {
			return reports
		}
	}
	t.Fatal("bounded turns made no eventual progress")
	return nil
}
func TestDrainDeepWideBoundedCustody(t *testing.T) {
	base, a, attempt := drainFixture(t)
	makeDrainTree(t, attempt, 200, 85)
	beforeFDs, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	maxExtra := 0
	for i := 0; i < 200; i++ {
		r, err := drainOwner(t.Context(), base, a, func(string) error {
			fds, e := os.ReadDir("/proc/self/fd")
			if e != nil {
				return e
			}
			maxExtra = max(maxExtra, len(fds)-len(beforeFDs))
			return nil
		})
		if err != nil {
			t.Fatal(i, r, err)
		}
		if r.Deleted > 16 || r.Steps > MaxDrainSteps || r.DirectoryEntries > 80 || r.TraversalStats > MaxDrainTraversalStats || r.ControlBytesCharged > 5*(MaxDrainMarkerBytes+1) {
			t.Fatal("work counters", r)
		}
		if r.Done {
			if _, err = os.Stat(attempt); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("root retained", err)
			}
			if maxExtra > 8 {
				t.Fatal("FD envelope", maxExtra)
			}
			return
		}
	}
	t.Fatal("deep cursor failed to progress")
}
func TestDrainEveryInterruptedMutationPrefix(t *testing.T) {
	// Each event follows a completed durable mutation, including terminal owner,
	// lock, marker and root removal. Recreate the same small shape for every cut.
	baseline, a, attempt := drainFixture(t)
	makeDrainTree(t, attempt, 2, 2)
	var events []string
	for i := 0; i < 10; i++ {
		r, err := drainOwner(t.Context(), baseline, a, func(e string) error { events = append(events, e); return nil })
		if err != nil {
			t.Fatal(err)
		}
		if r.Done {
			break
		}
	}
	if len(events) < 10 {
		t.Fatal("insufficient crash boundaries", events)
	}
	for cut := range events {
		t.Run(fmt.Sprintf("%02d-%s", cut, events[cut]), func(t *testing.T) {
			base, a, attempt := drainFixture(t)
			makeDrainTree(t, attempt, 2, 2)
			seen := 0
			interrupted := false
			stop := errors.New("simulated interrupted owner")
			for turn := 0; turn < 10 && !interrupted; turn++ {
				r, err := drainOwner(t.Context(), base, a, func(string) error {
					if seen == cut {
						interrupted = true
						return stop
					}
					seen++
					return nil
				})
				if err != nil && !errors.Is(err, stop) {
					t.Fatal(err)
				}
				if r.Done {
					break
				}
			}
			if !interrupted {
				t.Fatal("cut not reached")
			}
			finishDrain(t, base, a)
		})
	}
}
func TestDrainReaderPinAndCancellation(t *testing.T) {
	base, a, attempt := drainFixture(t)
	makeDrainTree(t, attempt, 0, 2)
	release, err := publicationLease(t.Context(), attempt, false, false)
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	r, err := DrainOwner(blocked, base, a)
	if err == nil || r.Deleted != 0 {
		release()
		t.Fatal("reader bypass", r, err)
	}
	release()
	canceled, cancelNow := context.WithCancel(t.Context())
	cancelNow()
	r, err = DrainOwner(canceled, base, a)
	if !errors.Is(err, context.Canceled) || r.Deleted != 0 {
		t.Fatal(r, err)
	}
	live, cancelLive := context.WithCancel(t.Context())
	r, err = drainOwner(live, base, a, func(e string) error {
		if strings.HasPrefix(e, "removed:") {
			cancelLive()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || r.Deleted > 16 {
		t.Fatal("cancellation prefix", r, err)
	}
	finishDrain(t, base, a)
}
func TestDrainRefusesUnsafeOrUnboundCustody(t *testing.T) {
	for _, kind := range []string{"wrong-manifest", "wrong-inode", "unknown", "symlink", "hardlink", "fifo", "wrong-mode", "substituted-cursor", "malformed-pending"} {
		t.Run(kind, func(t *testing.T) {
			base, a, attempt := drainFixture(t)
			top := makeDrainTree(t, attempt, 0, 1)
			if err := os.Chmod(filepath.Join(attempt, top), 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "wrong-manifest":
				a.ManifestDigest = "sha256:" + strings.Repeat("f", 64)
			case "wrong-inode":
				a.DirectoryInode++
			case "unknown":
				if err := os.WriteFile(filepath.Join(attempt, "unannounced"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("/", filepath.Join(attempt, top, "link")); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				file := filepath.Join(attempt, top, "f000")
				if err := os.Link(file, filepath.Join(attempt, top, "linked")); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(filepath.Join(attempt, top, "fifo"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong-mode":
				if err := os.Chmod(filepath.Join(attempt, top), 0777); err != nil {
					t.Fatal(err)
				}
			case "substituted-cursor":
				// Create a valid durable deep cursor, then replace exactly that directory.
				other := filepath.Join(attempt, "bundle-"+strings.Repeat("b", 32)+".stage")
				if err := os.Mkdir(other, 0700); err != nil {
					t.Fatal(err)
				}
				m := drainMarker{Schema: drainMarkerSchema, Authority: a, Revision: 1, Cursor: filepath.Base(other), CursorInode: 1}
				raw, err := encodeDrainMarker(m)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(attempt, drainMarkerName), raw, 0444); err != nil {
					t.Fatal(err)
				}
			case "malformed-pending":
				if err := os.WriteFile(filepath.Join(attempt, drainPendingName), []byte("partial"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r, err := DrainOwner(t.Context(), base, a)
			if err == nil || !r.Held {
				t.Fatal("unsafe custody accepted", r, err)
			}
			if _, err = os.Stat(attempt); err != nil {
				t.Fatal("unsafe root removed", err)
			}
		})
	}
}
func TestDrainTerminalEmptyRequiresTrustedInode(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(fmt.Sprint(wrong), func(t *testing.T) {
			base, a, attempt := drainFixture(t)
			// Reproduce the exact prefix after marker unlink, with the authenticated
			// controller reference retained outside the filesystem.
			for _, name := range []string{ownerManifest, publicationLock} {
				if err := os.Remove(filepath.Join(attempt, name)); err != nil {
					t.Fatal(err)
				}
			}
			if wrong {
				a.DirectoryInode++
			}
			r, err := DrainOwner(t.Context(), base, a)
			if wrong {
				if err == nil || !r.Held {
					t.Fatal("wrong terminal inode accepted", r, err)
				}
			} else if err != nil || !r.Done {
				t.Fatal("terminal empty prefix stuck", r, err)
			}
		})
	}
}
func TestDrainOpenat2MountBoundaryAndMarkerBounds(t *testing.T) {
	// Ordinary unprivileged /proc is already a different mount; no mount operation.
	base, a, attempt := drainFixture(t)
	root, err := os.Open("/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	d := drainTurn{ctx: t.Context(), authority: a}
	if f, err := d.open(root, "proc", true); !errors.Is(err, unix.EXDEV) {
		if f != nil {
			_ = f.Close()
		}
		t.Fatal("mount traversal did not return EXDEV", err)
	}
	// Valid inventory paths with worst JSON escaping still fit the cursor marker.
	top := "inputs-" + strings.Repeat("a", 32) + ".stage"
	cursor := top + "/" + strings.Repeat("<", 255) + "/" + strings.Repeat("<", 256-1)
	m := drainMarker{Schema: drainMarkerSchema, Authority: a, Revision: 2, Previous: "sha256:" + strings.Repeat("a", 64), Cursor: cursor, CursorInode: 42}
	raw, err := encodeDrainMarker(m)
	if err != nil || len(raw) > 4096 {
		t.Fatal("admitted escaped path refused", len(raw), err)
	}
	if _, err = decodeDrainMarker(append(raw, ' '), a); err == nil {
		t.Fatal("noncanonical marker")
	}
	_ = base
	_ = attempt
}

func TestDrainAnnouncedResidueAndContainerHold(t *testing.T) {
	for _, name := range []string{ownerPending, "inventory.json", "input-receipt.json", "publication-receipt.json", "inputs-" + strings.Repeat("a", 32) + ".typed-container.json"} {
		t.Run(name, func(t *testing.T) {
			base, a, attempt := drainFixture(t)
			if err := os.WriteFile(filepath.Join(attempt, name), []byte("abandoned partial control"), 0600); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(name, ".typed-container.json") {
				r, err := DrainOwner(t.Context(), base, a)
				if err == nil || !r.Held {
					t.Fatal("container custody erased", r, err)
				}
				return
			}
			finishDrain(t, base, a)
		})
	}
}

func TestDrainWrongTerminalAttemptAndReferencedInode(t *testing.T) {
	t.Run("wrong-attempt", func(t *testing.T) {
		base, a, original := drainFixture(t)
		for _, name := range []string{ownerManifest, publicationLock} {
			if err := os.Remove(filepath.Join(original, name)); err != nil {
				t.Fatal(err)
			}
		}
		a.AttemptDigest = "sha256:" + strings.Repeat("e", 64)
		other := filepath.Join(base, a.relative())
		if err := os.Mkdir(other, 0700); err != nil {
			t.Fatal(err)
		}
		if r, err := DrainOwner(t.Context(), base, a); err == nil || !r.Held {
			t.Fatal("wrong terminal attempt accepted", r, err)
		}
		if _, err := os.Stat(original); err != nil {
			t.Fatal("original was touched", err)
		}
	})
	t.Run("referenced-control", func(t *testing.T) {
		f, src, raw, inv, id, m := ownerFixture(t)
		attempt := filepath.Join(f.dir, id.RelativeName())
		receipt, err := Copy(t.Context(), src, attempt, inv, f.gate)
		if err != nil {
			t.Fatal(err)
		}
		m, err = SaveOwnerInputs(t.Context(), f.dir, id, m.Digest(), raw, receipt, f.gate)
		if err != nil {
			t.Fatal(err)
		}
		a, err := RetirementAuthority(m)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(attempt, "input-receipt.json")
		if err = os.Rename(name, name+"-old"); err != nil {
			t.Fatal(err)
		}
		bytes, err := os.ReadFile(name + "-old")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(name, bytes, 0444); err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(name + "-old"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 10; i++ {
			r, e := DrainOwner(t.Context(), f.dir, a)
			if e != nil {
				if !r.Held {
					t.Fatal(r, e)
				}
				return
			}
			if r.Done {
				t.Fatal("substituted referenced receipt deleted")
			}
		}
		t.Fatal("substituted receipt never inspected")
	})
}

func TestDrainInterruptedDeepCursorCheckpoint(t *testing.T) {
	for _, afterRename := range []bool{false, true} {
		t.Run(fmt.Sprint(afterRename), func(t *testing.T) {
			base, a, attempt := drainFixture(t)
			makeDrainTree(t, attempt, 70, 1)
			r, err := DrainOwner(t.Context(), base, a)
			if err != nil || r.Done {
				t.Fatal("deep first turn", r, err)
			}
			raw, err := os.ReadFile(filepath.Join(attempt, drainMarkerName))
			if err != nil {
				t.Fatal(err)
			}
			marker, err := decodeDrainMarker(raw, a)
			if err != nil || marker.Cursor == "." {
				t.Fatal("cursor not checkpointed", marker, err)
			}
			stop := errors.New("interrupt checkpoint prefix")
			renamed := false
			cut := false
			for turn := 0; turn < 5 && !cut; turn++ {
				_, err = drainOwner(t.Context(), base, a, func(e string) error {
					if !afterRename && e == "marker-pending" {
						cut = true
						return stop
					}
					if e == "marker-renamed" {
						renamed = true
					}
					if afterRename && renamed && e == "removed:d" {
						cut = true
						return stop
					}
					return nil
				})
				if err != nil && !errors.Is(err, stop) {
					t.Fatal(err)
				}
			}

			if !cut || !errors.Is(err, stop) {
				t.Fatal("deep checkpoint cut not reached", cut, err)
			}
			finishDrain(t, base, a)
		})
	}
}

func TestDrainBusyAttemptReleasesNamespacePromptly(t *testing.T) {
	base, a, attempt := drainFixture(t)
	release, err := publicationLease(t.Context(), attempt, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	outer, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := time.Now()
	r, err := DrainOwner(outer, base, a)
	if err == nil || r.Deleted != 0 || time.Since(started) > time.Second {
		t.Fatal("pinned child held namespace too long", r, err, time.Since(started))
	}
	free, cancelFree := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancelFree()
	unlock, err := AcquirePublicationMutation(free, base)
	if err != nil {
		t.Fatal("namespace pin leaked", err)
	}
	unlock()
}

func TestDrainFreshRootCensusBeforeTerminal(t *testing.T) {
	base, a, attempt := drainFixture(t)
	injected := false
	r, err := drainOwner(t.Context(), base, a, func(e string) error {
		if e == "root-eof" && !injected {
			injected = true
			name := filepath.Join(attempt, "inputs-"+strings.Repeat("c", 32)+".stage")
			if err := os.Mkdir(name, 0700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(name, "leftover"), []byte("owned"), 0444)
		}
		return nil
	})
	if err != nil || r.Done || !injected {
		t.Fatal("stale EOF became terminal", r, err)
	}
	raw, err := os.ReadFile(filepath.Join(attempt, drainMarkerName))
	if err != nil {
		t.Fatal(err)
	}
	marker, err := decodeDrainMarker(raw, a)
	if err != nil || marker.Terminal {
		t.Fatal("remaining content stranded", marker, err)
	}
	finishDrain(t, base, a)
}
