//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestWarmWorkspaceCanonicalFrame(t *testing.T) {
	for _, test := range []struct {
		name  string
		op    byte
		phase uint32
		nanos int64
		valid bool
	}{
		{"legacy_resume3", phaseResume, 3, 0, true},
		{"warm_resume3", phaseResume, 3, 123456789, true},
		{"negative", phaseResume, 3, -1, false},
		{"other_phase", phaseResume, 4, 123, false},
		{"other_op", phasePause, 3, 123, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			frame := phaseControlFrame{op: test.op, phase: test.phase, sequence: 1, binding: [32]byte{1}}
			raw := frame.encode()
			binary.BigEndian.PutUint64(raw[24:32], uint64(test.nanos))
			got, err := decodePhaseControl(raw)
			if (err == nil) != test.valid || err == nil && got.encode() != raw {
				t.Fatal("canonical reserved payload")
			}
		})
	}
	config := phaseTestConfig()
	raw, err := json.Marshal(config)
	if err != nil || string(raw) != `{"OwnerControl":false,"Phases":[1,2],"InitialPhase":1,"MaximumPhases":2,"MaximumWireBytes":65536,"Timeout":1000000000}` || strings.Contains(string(raw), "WarmStartWorkspace") {
		t.Fatal("legacy omission changed")
	}
	config.WarmStartWorkspace = true
	if _, err := config.validate(); err == nil {
		t.Fatal("non-full profile admitted")
	}
}

// Actual DA/PC/SA sockets and fences with a supplied callback/root identity;
// TestProductionWorkspaceWarmInherited separately proves native FD6/bootstrap.
// No database, workspace walk, or phase receipt is modeled as measured here.
func TestWarmWorkspacePostACKJoin(t *testing.T) {
	for _, mode := range []string{"success", "cancel", "panic", "replay", "missing_deadline", "missing_payload", "unexpected_deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			config := testConfig()
			config.Limits.Phases = 3
			config.Producers[0].ID = 2
			config.Phases[0].ID, config.Phases[1].ID = 2, 3
			last := config.Phases[1]
			last.ID = 4
			config.Phases = append(config.Phases, last)
			dispatch, client, server := paired(t, config)
			lifetime, _, transport := storePhaseLifetimeFor(t, client, 2,
				[]storeaccounting.Phase{{ID: 2}, {ID: 3}, {ID: 4}}, 14)
			lifetime.program, lifetime.semanticMode, lifetime.producerID = ProgramPhebs, ProductionSemanticV3, 2
			lifetime.inputSHA256, lifetime.workspace = [32]byte{1}, &productionWorkspace{}
			if _, err := lifetime.TakeStoreOwner(); err != nil {
				t.Fatal(err)
			}
			a, b, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			pc := PhaseControlConfig{WarmStartWorkspace: true, OwnerControl: true, Phases: []uint32{2, 3, 4},
				InitialPhase: 2, MaximumPhases: 3, MaximumWireBytes: 21 * 2 * FrameBytes, Timeout: time.Second}
			receiverConfig := pc
			if mode == "missing_payload" {
				pc.WarmStartWorkspace = false
			}
			if mode == "unexpected_deadline" {
				receiverConfig.WarmStartWorkspace = false
			}
			control, err := NewPhaseControl(ctx, a, client.binding, pc)
			if err != nil {
				_ = b.Close()
				t.Fatal(err)
			}
			done, err := StartPhaseControl(ctx, b, client, receiverConfig)
			if err != nil {
				_ = control.Close()
				t.Fatal(err)
			}
			defer func() {
				cancel()
				_ = control.Close()
				_ = client.fail(ErrCanceled)
				_ = phaseTestResult(t, done)
				_ = phaseTestResult(t, server)
			}()
			owners, err := NewOwners(ctx, OwnerLimits{Owners: 1, Requests: 1})
			if err != nil || client.bindOwners(owners) != nil {
				t.Fatal("owners")
			}
			entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
			if lifetime.warmWorkspace == nil {
				lifetime.warmWorkspace = &warmStartWorkspace{}
			}
			lifetime.warmWorkspace.callback = func(callback context.Context) error {
				defer close(exited)
				if _, err := lifetime.warmStartWorkspaceState(callback); err != nil {
					return err
				}
				close(entered)
				select {
				case <-callback.Done():
					return callback.Err()
				case <-release:
				}
				if mode == "panic" {
					panic("supplied callback panic")
				}
				return nil
			}
			if control.DrainOwners(ctx) != nil || control.Pause(ctx) != nil || dispatch.Fence() != nil ||
				transport.Fence() != nil || control.Checkpoint(ctx) != nil || dispatch.Advance() != nil || transport.Advance() != nil {
				t.Fatal("actual phase checkpoint")
			}
			if mode == "missing_deadline" {
				if control.Resume(context.WithoutCancel(ctx)) == nil {
					t.Fatal("unbounded warm frame admitted")
				}
				return
			}
			warm, stopWarm := context.WithTimeout(ctx, time.Second)
			defer stopWarm()
			if mode == "missing_payload" || mode == "unexpected_deadline" {
				if control.Resume(warm) == nil {
					t.Fatal("unnegotiated reserved payload admitted")
				}
				return
			}
			if control.Resume(warm) != nil {
				t.Fatal("Resume ACK")
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("post-ACK callback absent")
			}
			client.mu.Lock()
			requestsOpen := client.ownerRequestsOpen
			client.mu.Unlock()
			if control.RequestToken() != "" || requestsOpen {
				t.Fatal("Resume reopened requests")
			}
			reopened := make(chan error, 1)
			go func() { reopened <- control.ReopenOwners(ctx) }()
			select {
			case err := <-reopened:
				t.Fatal("receiver acknowledged reopen during callback", err)
			default:
			}
			if mode == "cancel" {
				// Close cancels and joins this owned callback before SDK/FD6
				// release; no replacement deadline is given to the callback.
				if lifetime.closeWarmStartWorkspace(ctx) != nil {
					t.Fatal("callback join")
				}
			} else {
				close(release)
			}
			select {
			case <-exited:
			case <-ctx.Done():
				t.Fatal("callback did not join")
			}
			reopenErr := <-reopened
			if (reopenErr == nil) != (mode == "success" || mode == "replay") {
				t.Fatal(mode, reopenErr)
			}
			if mode == "replay" && control.Resume(warm) == nil {
				t.Fatal("second warm Resume admitted")
			}
			if mode == "success" && lifetime.runWarmStartWorkspace(ctx, time.Now().Add(time.Second).UnixNano()) == nil {
				t.Fatal("one-shot callback replayed")
			}
		})
	}
}
