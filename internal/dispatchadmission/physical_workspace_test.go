//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestPhysicalWorkspaceCanonicalFrame(t *testing.T) {
	for _, test := range []struct {
		name  string
		op    byte
		phase uint32
		nanos int64
		valid bool
	}{
		{"omitted", phaseOwnersReopen, 4, 0, true},
		{"selected", phaseOwnersReopen, 4, 123, true},
		{"negative", phaseOwnersReopen, 4, -1, false},
		{"wrong_phase", phaseOwnersReopen, 3, 123, false},
		{"wrong_op", phaseRequestsOpen, 4, 123, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			frame := phaseControlFrame{op: test.op, phase: test.phase, sequence: 1, binding: [32]byte{1}}
			raw := frame.encode()
			binary.BigEndian.PutUint64(raw[24:32], uint64(test.nanos))
			got, err := decodePhaseControl(raw)
			if (err == nil) != test.valid || err == nil && got.encode() != raw {
				t.Fatal("closed reserved payload")
			}
		})
	}
	config := phaseTestConfig()
	config.PhysicalPostAuthorWorkspace = true
	if _, err := config.validate(); err == nil {
		t.Fatal("non-full or warm-omitted profile admitted")
	}
}

// Real DA/SA/PC sockets and owner/request barriers. The callback and held-root
// identity are supplied; this is not author-B, native-engine or byte proof.
func TestPhysicalWorkspacePostACKJoin(t *testing.T) {
	for _, mode := range []string{"delayed_success", "cancel", "reopen_failure", "missing_reopen", "duplicate_reopen", "panic", "missing_deadline", "missing_payload", "unexpected_payload"} {
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
			pc := PhaseControlConfig{WarmStartWorkspace: true, PhysicalPostAuthorWorkspace: true,
				OwnerControl: true, Phases: []uint32{2, 3, 4}, InitialPhase: 2, MaximumPhases: 3,
				MaximumWireBytes: 21 * 2 * FrameBytes, Timeout: 200 * time.Millisecond}
			receiverConfig := pc
			if mode == "missing_payload" {
				pc.PhysicalPostAuthorWorkspace = false
			}
			if mode == "unexpected_payload" {
				receiverConfig.PhysicalPostAuthorWorkspace = false
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
				t.Fatal("actual owners")
			}
			lifetime.warmWorkspace.callback = func(callback context.Context) error {
				_, err := lifetime.warmStartWorkspaceState(callback)
				return err
			}
			entered, release, exited := make(chan time.Time, 1), make(chan struct{}), make(chan struct{})
			if lifetime.physicalWorkspace != nil {
				lifetime.physicalWorkspace.physical = func(callback context.Context, reopen func(context.Context) error) error {
					defer close(exited)
					deadline, _ := callback.Deadline()
					entered <- deadline
					select {
					case <-callback.Done():
						return callback.Err()
					case <-release:
					}
					if mode == "panic" {
						panic("supplied continuation panic")
					}
					if mode == "missing_reopen" {
						return nil
					}
					if mode == "reopen_failure" {
						owners.mu.Lock()
						owners.err = ErrIncomplete
						owners.mu.Unlock()
					}
					if err := reopen(callback); err != nil {
						return err
					}
					if mode == "duplicate_reopen" {
						_ = reopen(callback) // Even a swallowed duplicate must latch.
					}
					return nil
				}
			}
			if control.DrainOwners(ctx) != nil {
				t.Fatal("drain")
			}
			for phase := 3; phase <= 4; phase++ {
				if control.Pause(ctx) != nil || dispatch.Fence() != nil || transport.Fence() != nil ||
					control.Checkpoint(ctx) != nil || dispatch.Advance() != nil || transport.Advance() != nil || control.Resume(ctx) != nil {
					t.Fatal("actual phase advance", phase)
				}
			}
			// Model the existing pin window with real request control. The
			// final Fence remains before the author-completed Reopen signal.
			if control.OpenRequests(ctx) != nil || control.FenceRequests(ctx) != nil || control.RequestToken() != "" {
				t.Fatal("pin window fence")
			}
			if mode == "missing_deadline" {
				if control.ReopenOwners(context.WithoutCancel(ctx)) == nil {
					t.Fatal("deadline omitted")
				}
				return
			}
			err = control.ReopenOwners(ctx)
			if mode == "missing_payload" || mode == "unexpected_payload" {
				if err == nil {
					t.Fatal("unnegotiated payload accepted")
				}
				return
			}
			if err != nil {
				t.Fatal("continuation ACK", err)
			}
			wantDeadline, _ := ctx.Deadline()
			select {
			case got := <-entered:
				if !got.Equal(wantDeadline) {
					t.Fatal("phase deadline replaced", got, wantDeadline)
				}
			case <-ctx.Done():
				t.Fatal("continuation absent")
			}
			if turn, err := client.enterOwnerRequest(ctx, owners, control.RequestToken()); err == nil {
				turn.End()
				t.Fatal("ACK alone opened request")
			}
			if mode == "delayed_success" {
				// A real socket ACK has already joined; keep the callback
				// pending longer than that unchanged exchange allowance.
				select {
				case <-time.After(2 * pc.Timeout):
				case <-ctx.Done():
					t.Fatal("original phase expired")
				}
				if client.ownerRequestAllowed(control.RequestToken()) {
					t.Fatal("callback delay opened admission")
				}
			}
			if mode == "cancel" {
				if lifetime.closePhysicalPostAuthorWorkspace(ctx) != nil {
					t.Fatal("callback close did not join")
				}
			} else {
				close(release)
			}
			select {
			case <-exited:
			case <-ctx.Done():
				t.Fatal("callback not joined")
			}
			if mode == "delayed_success" {
				request, err := client.enterOwnerRequest(ctx, owners, control.RequestToken())
				if err != nil {
					t.Fatal("actual reopen absent", err)
				}
				request.End()
				if control.DrainOwners(ctx) != nil || control.Pause(ctx) != nil {
					t.Fatal("receiver did not finish continuation")
				}
				if lifetime.runPhysicalPostAuthorWorkspace(ctx, phaseControlFrame{op: phaseOwnersReopen, phase: 4, deadlineUnixNano: wantDeadline.UnixNano()}) == nil {
					t.Fatal("one-shot replayed")
				}
			} else if result := phaseTestResult(t, done); result == nil {
				t.Fatal("failed continuation accepted")
			}
		})
	}
}
