//go:build darwin || linux

package dispatchadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

type backupMeasurementFixture struct {
	ctx           context.Context
	cancel        context.CancelFunc
	control       *PhaseControl
	lifetime      *ProductionLifetime
	retire        func()
	paused        atomic.Bool
	calls         atomic.Uint32
	resumed       chan struct{}
	escapeStarted chan struct{}
	escapeDone    chan struct{}
}

// Real PC/DA/SA transports and actual owner Close; the engine hold is a
// synchronous ownership model, not a native database or byte observation.
func newBackupMeasurementFixture(t *testing.T, guardMode string) *backupMeasurementFixture {
	t.Helper()
	f := &backupMeasurementFixture{resumed: make(chan struct{}, 1), escapeStarted: make(chan struct{}), escapeDone: make(chan struct{})}
	f.ctx, f.cancel = context.WithTimeout(t.Context(), 5*time.Second)
	config := testConfig()
	config.Producers[0].ID, config.Limits.Phases = 5, 5
	roles := config.Phases[0].Roles
	config.Phases = nil
	var phases []storeaccounting.Phase
	for phase := uint32(8); phase <= 12; phase++ {
		config.Phases = append(config.Phases, Phase{ID: phase, Roles: roles})
		phases = append(phases, storeaccounting.Phase{ID: phase, Transactions: 2, Rows: 4})
	}
	da, client, served := paired(t, config)
	lifetime, _, sa := storePhaseLifetimeFor(t, client, 5, phases, 1920)
	f.lifetime = lifetime
	lifetime.program, lifetime.semanticMode, lifetime.producerID, lifetime.inputSHA256 = ProgramPhebs, ProductionSemanticV3, 5, [32]byte{7}
	if _, err := lifetime.TakeStoreOwner(); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lifetime.workspace = &productionWorkspace{file: file}
	t.Cleanup(func() { _ = file.Close() })
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	pc := backupControlConfig()
	pc.BackupMeasurementMaximum = 2
	// Eleven actual setup pairs below, one existing terminal EOF reservation,
	// and exactly four frames for each of the two measurement checkpoints.
	pc.MaximumWireBytes = 12*2*FrameBytes + 4*FrameBytes*uint64(pc.BackupMeasurementMaximum)
	pc.Timeout = 100 * time.Millisecond
	f.control, err = NewPhaseControl(f.ctx, parent, client.binding, pc)
	if err != nil {
		t.Fatal(err)
	}
	done, err := StartPhaseControl(f.ctx, child, client, pc)
	if err != nil {
		t.Fatal(err)
	}
	lifetime.controlDone = done
	t.Cleanup(func() {
		_ = f.control.Close()
		f.cancel()
		_ = phaseTestResult(t, done)
		_ = client.fail(ErrCanceled)
		_ = phaseTestResult(t, served)
	})
	guard := func(ctx context.Context, measure func(context.Context) error) error {
		f.calls.Add(1)
		if guardMode == "skip" {
			return nil
		}
		f.paused.Store(true)
		defer func() { f.paused.Store(false); f.resumed <- struct{}{} }()
		if guardMode == "escape" || guardMode == "escape_panic" {
			go func() { defer close(f.escapeDone); _ = measure(ctx) }()
			<-f.escapeStarted
			if guardMode == "escape_panic" {
				panic("fixture escaped guard")
			}
			return nil
		}
		err := measure(ctx)
		if guardMode == "double" {
			_ = measure(ctx)
		}
		if guardMode == "ignore" {
			return nil
		}
		return err
	}
	if err := lifetime.bindRetiredBackupMeasurement(guard); err != nil {
		t.Fatal(err)
	}
	owners, err := NewOwners(f.ctx, OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || client.bindOwners(owners) != nil {
		t.Fatal("owners", err)
	}
	f.retire = func() {
		if f.control.DrainOwners(f.ctx) != nil {
			t.Fatal("drain")
		}
		for phase := uint32(9); phase <= 11; phase++ {
			if f.control.Pause(f.ctx) != nil || da.Fence() != nil || sa.Fence() != nil || f.control.Checkpoint(f.ctx) != nil ||
				da.Advance() != nil || sa.Advance() != nil || f.control.Resume(f.ctx) != nil {
				t.Fatal("handoff", phase)
			}
		}
		if da.Fence() != nil || sa.Fence() != nil || f.control.Pause(f.ctx) != nil || sa.Wait(f.ctx, 5) != nil ||
			da.RetireBackupEndpoint() != nil || da.Advance() != nil || sa.Advance() != nil {
			t.Fatal("retirement")
		}
	}
	return f
}

func TestBackupMeasurementRetiredTransport(t *testing.T) {
	f := newBackupMeasurementFixture(t, "")
	f.retire()
	before := f.control.ReservedWireBytes()
	for range 2 {
		if err := f.control.WithRetiredBackupMeasurement(f.ctx, func(ctx context.Context) error {
			if !f.paused.Load() {
				t.Error("HOLD acknowledged before guard")
			}
			// Longer than the ordinary ACK timeout, shorter than the original
			// phase deadline: a hold must not acquire a fresh ACK-duration clock.
			select {
			case <-time.After(120 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if f.paused.Load() {
			t.Fatal("RELEASE acknowledged before resume")
		}
		<-f.resumed
	}
	if f.calls.Load() != 2 || f.control.ReservedWireBytes() != before+8*FrameBytes {
		t.Fatal("measurement allowance")
	}
	client := f.lifetime.client
	client.mu.Lock()
	phase, retiring, paused := client.phase, client.backupRetiring, client.paused
	client.mu.Unlock()
	if phase != 11 || !retiring || !paused {
		t.Fatal("measurement reopened producer work")
	}
	if err := f.control.WithRetiredBackupMeasurement(f.ctx, func(context.Context) error { t.Error("excess callback"); return nil }); err == nil || f.calls.Load() != 2 {
		t.Fatal("measurement maximum admitted excess")
	}
}

func TestBackupMeasurementRetiredEOFReservation(t *testing.T) {
	f := newBackupMeasurementFixture(t, "")
	f.retire()
	if f.control.ReservedWireBytes() != 11*2*FrameBytes {
		t.Fatal("setup frame count")
	}
	for range 2 {
		if err := f.control.WithRetiredBackupMeasurement(f.ctx, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		<-f.resumed
	}
	if f.control.ReservedWireBytes()+2*FrameBytes != f.control.config.MaximumWireBytes {
		t.Fatal("existing EOF pair not reserved separately")
	}
	if err := f.lifetime.Close(f.ctx); err != nil {
		t.Fatal("exact-cap retired close", err)
	}
	if err := f.lifetime.Close(f.ctx); err != nil {
		t.Fatal("retired close not idempotent", err)
	}
}

func TestBackupMeasurementFailureCleanup(t *testing.T) {
	for _, mode := range []string{"callback_error", "callback_panic", "cancel", "lost_release", "wrong_release", "release_deadline", "release_phase", "skip", "double", "ignore", "escape", "escape_panic"} {
		t.Run(mode, func(t *testing.T) {
			f := newBackupMeasurementFixture(t, mode)
			f.retire()
			want := errors.New("measurement failed")
			err := f.control.WithRetiredBackupMeasurement(f.ctx, func(context.Context) error {
				switch mode {
				case "callback_error":
					return want
				case "callback_panic":
					panic("fixture callback")
				case "cancel":
					f.cancel()
					return context.Canceled
				case "lost_release":
					_ = f.control.Close()
					return want
				case "escape", "escape_panic":
					close(f.escapeStarted)
					// Withhold RELEASE until the receiver detects the invalid
					// escaped guard and closes, rather than waiting five seconds.
					var one [1]byte
					_, _ = f.control.conn.Read(one[:])
					return want
				case "wrong_release", "release_deadline", "release_phase", "ignore":
					f.control.mu.Lock()
					frame := phaseControlFrame{op: phaseBackupMeasurementRelease, phase: 12, sequence: f.control.sequence + 1,
						binding: f.control.binding, deadlineUnixNano: f.control.backupMeasurementDeadline}
					f.control.mu.Unlock()
					if mode == "release_deadline" {
						frame.sequence--
						frame.deadlineUnixNano--
					}
					if mode == "release_phase" {
						frame.sequence--
						frame.phase = 11
					}
					raw := frame.encode()
					_, _ = f.control.conn.Write(raw[:])
					return want
				}
				return nil
			})
			if err == nil {
				t.Fatal("failure accepted")
			}
			if mode == "callback_error" && !errors.Is(err, want) {
				t.Fatal("callback cause lost", err)
			}
			if mode != "skip" {
				select {
				case <-f.resumed:
				case <-time.After(time.Second):
					t.Fatal("failed hold did not resume")
				}
			}
			if f.paused.Load() {
				t.Fatal("failed hold retained engine pause")
			}
			if mode == "escape" || mode == "escape_panic" {
				select {
				case <-f.escapeDone:
				case <-time.After(time.Second):
					t.Fatal("escaped callback not joined")
				}
				if f.ctx.Err() != nil {
					t.Fatal("invalid guard waited for original deadline")
				}
			}
			if f.control.WithRetiredBackupMeasurement(f.ctx, func(context.Context) error { t.Error("sticky failure resumed"); return nil }) == nil {
				t.Fatal("failure not sticky")
			}
		})
	}
}

func TestBackupMeasurementAdmissionAndDeadline(t *testing.T) {
	for _, mode := range []string{"unretired", "unbounded", "deadline_changed", "receiver_deadline_changed", "binding_twice", "missing_store", "missing_guard"} {
		t.Run(mode, func(t *testing.T) {
			f := newBackupMeasurementFixture(t, "")
			if mode == "binding_twice" {
				if f.lifetime.bindRetiredBackupMeasurement(func(context.Context, func(context.Context) error) error { return nil }) == nil {
					t.Fatal("binding replaced")
				}
				return
			}
			ctx := f.ctx
			if mode != "unretired" {
				f.retire()
			}
			if mode == "unbounded" {
				ctx = context.Background()
			}
			if mode == "deadline_changed" || mode == "receiver_deadline_changed" {
				if err := f.control.WithRetiredBackupMeasurement(ctx, func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
				<-f.resumed
				deadline, _ := ctx.Deadline()
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, deadline.Add(-time.Millisecond))
				defer cancel()
				if mode == "receiver_deadline_changed" {
					// Bypass only the parent model's remembered value to exercise
					// independent receiver enforcement on the actual PC transport.
					f.control.mu.Lock()
					f.control.backupMeasurementDeadline = 0
					f.control.mu.Unlock()
				}
			}
			if mode == "missing_store" {
				f.lifetime.storeMu.Lock()
				f.lifetime.storeRetired = false
				f.lifetime.storeMu.Unlock()
			}
			if mode == "missing_guard" {
				f.lifetime.backupMeasurementMu.Lock()
				f.lifetime.backupMeasurementGuard = nil
				f.lifetime.backupMeasurementMu.Unlock()
			}
			if f.control.WithRetiredBackupMeasurement(ctx, func(context.Context) error { t.Error("invalid admission measured"); return nil }) == nil {
				t.Fatal("invalid admission accepted")
			}
		})
	}
}

func TestBackupMeasurementCanonicalFrames(t *testing.T) {
	legacy := phaseControlFrame{op: phasePause, phase: 11, sequence: 1, binding: [32]byte{7}}
	raw := legacy.encode()
	if !bytes.Equal(raw[24:32], make([]byte, 8)) {
		t.Fatal("legacy reserved bytes changed")
	}
	raw[24] = 1
	if _, err := decodePhaseControl(raw); err == nil {
		t.Fatal("legacy deadline accepted")
	}
	for _, op := range []byte{phaseBackupMeasurementHold, phaseBackupMeasurementRelease} {
		frame := legacy
		frame.op, frame.phase, frame.deadlineUnixNano = op, 12, time.Now().Add(time.Minute).UnixNano()
		if got, err := decodePhaseControl(frame.encode()); err != nil || got != frame {
			t.Fatal(got, err)
		}
		frame.deadlineUnixNano = 0
		if _, err := decodePhaseControl(frame.encode()); err == nil {
			t.Fatal("missing deadline")
		}
	}
	pc := backupControlConfig()
	rawJSON, err := json.Marshal(pc)
	if err != nil || bytes.Contains(rawJSON, []byte("BackupMeasurement")) {
		t.Fatal("legacy config bytes changed")
	}
	pc.BackupMeasurementMaximum = 1
	if _, err := pc.validate(); err != nil {
		t.Fatal(err)
	}
	pc.BackupEndpointCarry = false
	if _, err := pc.validate(); err == nil {
		t.Fatal("unselected measurement allowance")
	}
	pc.BackupEndpointCarry = true
	pc.MaximumWireBytes = 6*FrameBytes - 1
	if _, err := pc.validate(); err == nil {
		t.Fatal("unbounded measurement config")
	}
}
