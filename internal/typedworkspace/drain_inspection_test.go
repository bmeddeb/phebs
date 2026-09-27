//go:build linux

package typedworkspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDrainInspectionInterruptedPrefixes(t *testing.T) {
	for _, event := range []string{"marker-pending", "marker-renamed", "removed:owner.json", "removed:.phebs-index-publication.lock", "removed:collecting.json"} {
		t.Run(event, func(t *testing.T) {
			base, a, attempt := drainFixture(t)
			live, e := InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a)
			if e != nil || live.Live == nil || live.Resume || live.Absent {
				t.Fatal("live", live, e)
			}
			stopped := false
			for turn := 0; turn < 20 && !stopped; turn++ {
				_, e = drainOwner(t.Context(), base, a, func(name string) error {
					if name == event && !stopped {
						stopped = true
						return errors.New("crash")
					}
					return nil
				})
				if e != nil && !stopped {
					t.Fatal(e)
				}
			}
			if !stopped {
				t.Fatal("unreached prefix", event)
			}
			before, e := os.ReadDir(attempt)
			if e != nil {
				t.Fatal(e)
			}
			got, e := InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a)
			if e != nil || !got.Resume || got.Live != nil || got.Absent {
				t.Fatal("resume", got, e)
			}
			after, e := os.ReadDir(attempt)
			if e != nil || len(after) != len(before) {
				t.Fatal("inspection mutated", e)
			}
			finishDrain(t, base, a)
			got, e = InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a)
			if e != nil || !got.Absent {
				t.Fatal("absent", got, e)
			}
			entries, absent, e := InspectDrainNamespace(t.Context(), base, a.PlanningDigest, inspectionBase(t, base))
			if e != nil || !absent || len(entries) != 0 {
				t.Fatal(entries, absent, e)
			}
		})
	}
}
func TestDrainInspectionRefusesUnknownAndReplaced(t *testing.T) {
	base, a, attempt := drainFixture(t)
	unknown := filepath.Join(attempt, "unknown")
	if e := os.WriteFile(unknown, []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a); e == nil {
		t.Fatal("unknown accepted")
	}
	if e := os.Remove(unknown); e != nil {
		t.Fatal(e)
	}
	if _, e := InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a); e != nil {
		t.Fatal("restored", e)
	}
	if e := os.Rename(attempt, attempt+"-saved"); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(attempt, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a); e == nil {
		t.Fatal("replaced empty inode")
	}
	if e := os.Remove(attempt); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(attempt+"-saved", attempt); e != nil {
		t.Fatal(e)
	}
	if _, e := InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a); e != nil {
		t.Fatal("restored inode", e)
	}
	request := filepath.Dir(attempt)
	if e := os.Chmod(request, 0777); e != nil {
		t.Fatal(e)
	}
	if _, e := InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a); e == nil {
		t.Fatal("writable parent accepted")
	}
	if e := os.Chmod(request, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a); e != nil {
		t.Fatal("restored parent", e)
	}

}
func TestDrainInspectionMaximumTopPrefix(t *testing.T) {
	base, a, attempt := drainFixture(t)
	_, e := drainOwner(t.Context(), base, a, func(event string) error {
		if event == "marker-renamed" {
			return errors.New("crash")
		}
		return nil
	})
	if e == nil {
		t.Fatal("no interruption")
	}
	// Five owner controls + lock + two input + two publication + four phase
	// directories + two drain markers =16. The current valid marker has no next;
	// pending adds the16th after its exact chain is constructed.
	for _, name := range []string{ownerPending, "inventory.json", "input-receipt.json", "publication-receipt.json"} {
		if e = os.WriteFile(filepath.Join(attempt, name), []byte("x"), 0444); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"inputs-" + strings.Repeat("a", 32), "inputs-" + strings.Repeat("a", 32) + ".stage", "bundle-" + strings.Repeat("b", 32), "bundle-" + strings.Repeat("b", 32) + ".stage", "controls-plan", "controls-plan.stage", "controls-execute", "controls-execute.stage"} {
		if e = os.Mkdir(filepath.Join(attempt, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := os.ReadFile(filepath.Join(attempt, drainMarkerName))
	if e != nil {
		t.Fatal(e)
	}
	m, e := decodeDrainMarker(raw, a)
	if e != nil {
		t.Fatal(e)
	}
	m.Revision++
	m.Previous = publicationDigest(raw)
	next, e := encodeDrainMarker(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(attempt, drainPendingName), next, 0444); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(attempt)
	if e != nil || len(entries) != 16 {
		t.Fatal(len(entries), e)
	}
	if got, e := InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a); e != nil || !got.Resume {
		t.Fatal("maximum admitted prefix", got, e)
	}
	if e = os.Mkdir(filepath.Join(attempt, "inputs-"+strings.Repeat("c", 32)), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = InspectDrainOwner(t.Context(), base, inspectionBase(t, base), a); e == nil {
		t.Fatal("unexpected repeated random stage accepted")
	}
}

func inspectionBase(t *testing.T, base string) Node {
	t.Helper()
	o, e := ObserveCapacity(t.Context(), base)
	if e != nil {
		t.Fatal(e)
	}
	return Node{Device: o.Device, Inode: o.Inode, Directory: true}
}
func TestDrainInspectionBaseReplacement(t *testing.T) {
	base, a, _ := drainFixture(t)
	expected := inspectionBase(t, base)
	replacement := t.TempDir()
	if e := os.WriteFile(filepath.Join(replacement, publicationLock), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := InspectDrainOwner(t.Context(), replacement, expected, a); e == nil {
		t.Fatal("replacement absence accepted")
	}
	if _, _, e := InspectDrainNamespace(t.Context(), replacement, a.PlanningDigest, expected); e == nil {
		t.Fatal("replacement root absence accepted")
	}
	if _, e := InspectDrainOwner(t.Context(), base, expected, a); e != nil {
		t.Fatal("positive", e)
	}
}
