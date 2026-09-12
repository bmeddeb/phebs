//go:build darwin

package t421

import (
	"slices"
	"testing"
)

// Supplied integer IDs exercise only the closed inventory shape. No host
// census, process start, phase15 admission or native teardown is performed.
func TestExecutionPressureRecordedSessionInventory(t *testing.T) {
	for _, mode := range []string{"all_thirteen", "closed_owners", "duplicate_retained"} {
		t.Run(mode, func(t *testing.T) {
			author := &ExecutionAuthorCustody{sessions: [3]int{41, 42, 43}}
			flow := &ExecutionEpochOne{epochs: &ExecutionEpochConfigCustody{author: author},
				serverSessions: [5]int{21, 22, 23, 24, 25}, archiveSessions: [2]int{31, 32}}
			volume := &executionPressureVolume{sessions: []int{11, 12, 13}, flow: flow}
			want := []int{11, 12, 13, 21, 22, 23, 24, 25, 31, 32, 41, 42, 43}
			switch mode {
			case "closed_owners":
				flow.closed, author.closed = true, true
			case "duplicate_retained":
				flow.archiveSessions[1], want[9] = 21, 21
			}
			volume.mu.Lock()
			got := volume.recordedSessionsLocked()
			volume.mu.Unlock()
			if !slices.Equal(got, want) || len(got) != 13 {
				t.Fatal("closed inventory omitted, shrank or deduplicated actual-start slots")
			}
			got[0] = 99
			if volume.sessions[0] != 11 {
				t.Fatal("returned scope aliases retained preparation IDs")
			}
		})
	}
}

// These invalid first IDs hit the existing <=0 native-session preflight;
// no arbitrary positive modeled ID is passed to a host process census.
func TestExecutionPressureRecordedInvalidSessionRefusesClose(t *testing.T) {
	for _, mode := range []string{"preparation_zero", "operational_negative"} {
		t.Run(mode, func(t *testing.T) {
			volume := &executionPressureVolume{sessions: []int{0}}
			if mode == "operational_negative" {
				volume.sessions = nil
				volume.flow = &ExecutionEpochOne{serverSessions: [5]int{-1}}
			}
			if volume.Close() == nil || volume.closed || !volume.unsettled {
				t.Fatal("invalid retained ID released custody")
			}
		})
	}
}
