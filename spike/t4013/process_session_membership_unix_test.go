//go:build darwin || linux

package t4013

import (
	"errors"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateProcessSessionMemberLifetimeFence(t *testing.T) {
	for _, scope := range [][2]int{{0, 7}, {20, 0}, {-1, 7}, {20, -1}} {
		if record, present, err := privateProcessSessionMember(scope[0], scope[1], nil, nil, nil); err == nil || present || record != (NativeProcessRecord{}) {
			t.Fatal("invalid scope reached observation or returned authority")
		}
	}
	type fixture struct {
		rows              [2]processSnapshot
		observationErrors [2]error
		sessions          [2]int
		sessionErrors     [2]error
		defunct           bool
		defunctPresent    bool
		defunctErr        error
	}
	for _, test := range []struct {
		name            string
		change          func(*fixture)
		present, failed bool
	}{
		{name: "stable member", present: true},
		{name: "reparented same lifetime", present: true, change: func(f *fixture) { f.rows[1].parent = 1 }},
		{name: "vanished before observation", change: func(f *fixture) { f.observationErrors[0] = errProcessIdentityMissing }},
		{name: "vanished after membership", change: func(f *fixture) { f.observationErrors[1] = errProcessIdentityMissing }},
		{name: "departed before membership", change: func(f *fixture) { f.sessions[0] = 8 }},
		{name: "departed after liveness", change: func(f *fixture) { f.sessions[1] = 8 }},
		{name: "session disappeared", change: func(f *fixture) { f.sessionErrors[1] = unix.ESRCH }},
		{name: "defunct", change: func(f *fixture) { f.defunct = true }},
		{name: "missing liveness record", change: func(f *fixture) { f.defunctPresent = false }},
		{name: "PID recycled", change: func(f *fixture) { f.rows[1].identityToken = "new-lifetime" }},
		{name: "observation denied", failed: true, change: func(f *fixture) { f.observationErrors[0] = unix.EPERM }},
		{name: "confirmation denied", failed: true, change: func(f *fixture) { f.observationErrors[1] = unix.EPERM }},
		{name: "session denied", failed: true, change: func(f *fixture) { f.sessionErrors[0] = unix.EPERM }},
		{name: "session confirmation denied", failed: true, change: func(f *fixture) { f.sessionErrors[1] = unix.EPERM }},
		{name: "liveness denied", failed: true, change: func(f *fixture) { f.defunctErr = unix.EPERM }},
		{name: "incoherent first record", failed: true, change: func(f *fixture) { f.rows[0].coherent = false }},
		{name: "invalid final record", failed: true, change: func(f *fixture) { f.rows[1].identityToken = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := processSnapshot{parent: 10, rssBytes: 1024, identityToken: "same-lifetime", name: "owned", coherent: true}
			f := fixture{rows: [2]processSnapshot{row, row}, sessions: [2]int{7, 7}, defunctPresent: true}
			if test.change != nil {
				test.change(&f)
			}
			var calls []string
			observations, checks := 0, 0
			got, found, err := privateProcessSessionMember(20, 7,
				func(pid int) (processSnapshot, error) {
					if pid != 20 || observations >= 2 {
						t.Fatal("unexpected observation")
					}
					calls = append(calls, "identity")
					i := observations
					observations++
					return f.rows[i], f.observationErrors[i]
				},
				func(pid int) (int, error) {
					if pid != 20 || checks >= 2 {
						t.Fatal("unexpected membership check")
					}
					calls = append(calls, "session")
					i := checks
					checks++
					return f.sessions[i], f.sessionErrors[i]
				},
				func(pid int) (bool, bool, error) {
					if pid != 20 {
						t.Fatal("unexpected liveness check")
					}
					calls = append(calls, "defunct")
					return f.defunct, f.defunctPresent, f.defunctErr
				})
			if found != test.present || (err != nil) != test.failed {
				t.Fatalf("present=%t error=%v", found, err)
			}
			if found {
				want := NativeProcessRecord{PID: 20, ParentPID: f.rows[1].parent, RSSBytes: f.rows[1].rssBytes, StartIdentity: f.rows[1].identityToken, ObservedName: f.rows[1].name}
				if got != want || !reflect.DeepEqual(calls, []string{"identity", "session", "defunct", "session", "identity"}) {
					t.Fatalf("unfenced observation: %+v / %v", got, calls)
				}
			} else if got != (NativeProcessRecord{}) {
				t.Fatal("refused or vanished member returned authority")
			}
			if test.failed && (f.observationErrors[0] != nil || f.observationErrors[1] != nil || f.sessionErrors[0] != nil || f.sessionErrors[1] != nil || f.defunctErr != nil) && !errors.Is(err, unix.EPERM) {
				t.Fatal("lost native refusal", err)
			}
		})
	}
}
