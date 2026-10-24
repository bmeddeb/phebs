//go:build linux

package t421

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// These state controls issue no native Start, wait, signal or custody authority.
func TestLinuxEpochTraceTurnDiagnosticDoesNotReusePriorTask(t *testing.T) {
	for _, gate := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-gate", true: "empty-turn"}[gate], func(t *testing.T) {
			root := &linuxEpochTraceRoot{gateOwned: gate, currentTID: 77, events: 3}
			root.recent[0] = linuxEpochTraceEvent{tid: 77, group: 70, event: 6, birth: "1"}
			before := root.recent
			terminal, err := root.turn(t.Context())
			if terminal || root.currentTID != 0 || root.gateOwned != gate || root.events != 3 || root.recent != before || root.unknown || root.waitReturned {
				t.Fatal("diagnostic changed ownership or historical evidence", terminal, root.currentTID, err)
			}
			if gate && err != nil || !gate && (!errors.Is(err, ErrExecutionEpochOne) || !strings.Contains(err.Error(), "turn/gate")) {
				t.Fatal("closed gate refusal lost its private branch", err)
			}
		})
	}
}

func TestLinuxEpochTraceDetachedDiagnosticRetainsWitness(t *testing.T) {
	original, err := linuxEpochTraceIdentityOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*linuxEpochTraceIdentity)
	}{
		{"parent", func(value *linuxEpochTraceIdentity) { value.parent++ }},
		{"session", func(value *linuxEpochTraceIdentity) { value.sid++ }},
		{"name", func(value *linuxEpochTraceIdentity) { value.name += "-wrong" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			witness := original
			test.change(&witness)
			admission := &linuxEpochResourceAdmission{}
			owner := &linuxEpochTraceOwner{admission: admission, tids: 1}
			root := &linuxEpochTraceRoot{owner: owner, gateOwned: true, currentTID: 77,
				detached: map[int]linuxEpochTraceIdentity{witness.pid: witness}}
			terminal, err := root.turn(t.Context())
			if terminal || !errors.Is(err, ErrExecutionEpochOne) || !strings.Contains(err.Error(), "turn/detached: detached/current tid=") || !strings.Contains(err.Error(), "detached/"+test.name) {
				t.Fatal("binding refusal lost private branch", terminal, err)
			}
			if root.currentTID != 0 || !root.gateOwned || len(root.detached) != 1 || owner.tids != 1 || !root.detached[witness.pid].sameLifetime(witness) || root.waitReturned {
				t.Fatal("failed witness was retired or stale task attributed")
			}
			root.refuse("event-turn", err)
			first := owner.failure()
			if first.CurrentTID != 0 || first.Stage != "event-turn" || first.Cause != err.Error() || first.ActiveTIDs != 1 || first.Tasks != 0 {
				t.Fatal("first private snapshot lost operation or witness scope", first)
			}
			publicError := root.err.Error()
			if publicError != "linux epoch trace event-turn: "+ErrExecutionEpochOne.Error() || !errors.Is(root.err, ErrExecutionEpochOne) {
				t.Fatal("private context changed public trace refusal", root.err)
			}
			root.currentTID = 88
			root.refuse("later-cleanup", errors.New("later diagnostic"))
			if owner.failure() != first || root.err.Error() != publicError || admission.privateRefusal() != "trace/event-turn" {
				t.Fatal("later cleanup replaced first refusal")
			}
		})
	}
}

func TestLinuxEpochTraceTerminalShapeDiagnosticGrantsNoAuthority(t *testing.T) {
	var root *linuxEpochTraceRoot
	value, err := root.terminalProof(t.Context(), nil)
	if !errors.Is(err, ErrExecutionEpochOne) || !strings.Contains(err.Error(), "terminal/shape") || value.pid != 0 || value.namespace != nil {
		t.Fatal("closed terminal shape issued authority or lost private branch", value, err)
	}
}
