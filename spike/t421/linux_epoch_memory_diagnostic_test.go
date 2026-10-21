//go:build linux

package t421

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func TestLinuxEpochInitialMemoryDiagnosticPreservesExactRSSAndRefusal(t *testing.T) {
	for _, test := range []struct {
		name      string
		rows      []t4013.NativeProcessRecord
		available bool
		accepted  bool
	}{
		{"missing", nil, false, false},
		{"wrong-pid", []t4013.NativeProcessRecord{{PID: 43, StartIdentity: "birth", RSSBytes: 12345}}, false, false},
		{"wrong-birth", []t4013.NativeProcessRecord{{PID: 42, StartIdentity: "other", RSSBytes: 12345}}, false, false},
		{"zero", []t4013.NativeProcessRecord{{PID: 42, StartIdentity: "birth"}}, false, false},
		{"negative", []t4013.NativeProcessRecord{{PID: 42, StartIdentity: "birth", RSSBytes: -1}}, false, false},
		{"bytes-not-kib", []t4013.NativeProcessRecord{{PID: 42, StartIdentity: "birth", RSSBytes: 12345}}, true, true},
		{"exact-limit", []t4013.NativeProcessRecord{{PID: 42, StartIdentity: "birth", RSSBytes: int64(linuxEpochRSSReservation)}}, true, true},
		{"overshoot-saved", []t4013.NativeProcessRecord{{PID: 42, StartIdentity: "birth", RSSBytes: int64(linuxEpochRSSReservation + 1)}}, true, false},
		{"extra-process", []t4013.NativeProcessRecord{{PID: 42, StartIdentity: "birth", RSSBytes: 12345}, {PID: 43, RSSBytes: 1}}, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var diagnostic linuxEpochInitialMemoryDiagnostic
			if got := diagnostic.recordController(test.rows, 42, "birth"); got != test.accepted || diagnostic.ControllerRSSAvailable != test.available {
				t.Fatalf("accept/availability = %v/%v", got, diagnostic.ControllerRSSAvailable)
			}
			if test.available && diagnostic.ControllerRSSBytes != uint64(test.rows[0].RSSBytes) || !test.available && diagnostic.ControllerRSSBytes != 0 {
				t.Fatal("RSS units changed or unavailable evidence invented", diagnostic)
			}
			var cancels atomic.Int32
			a := &linuxEpochResourceAdmission{initialMemory: diagnostic, cancel: func() { cancels.Add(1) }}
			a.failDetail("initial-memory-admission")
			a.failDetail("later-cleanup")
			if a.initialMemory != diagnostic || a.privateRefusal() != "initial-memory-admission" || cancels.Load() != 1 {
				t.Fatal("cleanup changed the initial diagnostic or first refusal")
			}
		})
	}
}

func TestLinuxEpochHeapSnapshotIsPrivateAndDoesNotCreditAdmission(t *testing.T) {
	before := time.Now().UTC()
	heap := readLinuxEpochHeapSnapshot()
	if !heap.Available || heap.StartedUTC.Before(before) || heap.StartedUTC.After(time.Now().UTC()) || heap.WallNS < 0 || heap.HeapObjectsBytes == 0 || heap.HeapStackBytes == 0 {
		t.Fatal("fixed runtime heap snapshot unavailable or malformed", heap)
	}
	diagnostic := linuxEpochInitialMemoryDiagnostic{Heap: heap}
	a := &linuxEpochResourceAdmission{initialMemory: diagnostic}
	observation := linuxEpochResourceTestMemory()
	calculation, err := evaluateLinuxEpochResources(observation, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	observation.AvailableBytes = calculation.RequiredBytes - 1
	a.calculation, err = evaluateLinuxEpochResources(observation, 1<<30)
	if err == nil || a.calculation != (t4013.LinuxMemoryCalculation{}) || a.initialMemory != diagnostic {
		t.Fatal("diagnostic credited occupied memory or changed a rejected calculation", err)
	}
	a.failDetail("initial-memory-admission")
	if a.initialMemory.Heap != heap {
		t.Fatal("admission refusal lost the heap snapshot")
	}
}
