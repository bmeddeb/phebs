//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func ownerDrainDeadlineTestConfig() PhaseControlConfig {
	return PhaseControlConfig{
		OwnerControl: true, Phases: []uint32{12, 13, 14}, InitialPhase: 12, MaximumPhases: 3,
		MaximumWireBytes: 64 << 10, Timeout: 400 * time.Millisecond,
	}
}

func TestOwnerControlInitialPhase12DrainDeadline(t *testing.T) {
	legacy := ownerDrainDeadlineTestConfig()
	raw, err := json.Marshal(legacy)
	if err != nil || string(raw) != `{"OwnerControl":true,"Phases":[12,13,14],"InitialPhase":12,"MaximumPhases":3,"MaximumWireBytes":65536,"Timeout":400000000}` {
		t.Fatalf("legacy bootstrap control bytes changed: %s, %v", raw, err)
	}
	for _, test := range []struct {
		name string
		edit func(*PhaseControlConfig)
	}{
		{"no-owner-control", func(c *PhaseControlConfig) { c.OwnerControl = false }},
		{"wrong-initial-phase", func(c *PhaseControlConfig) { c.InitialPhase = 13 }},
		{"wrong-phase-sequence", func(c *PhaseControlConfig) { c.Phases = []uint32{12, 13, 15} }},
		{"long-ordinary-timeout", func(c *PhaseControlConfig) { c.Timeout = 31 * time.Second }},
		{"expired", func(c *PhaseControlConfig) { c.OwnerDrainDeadlineUnixNano = time.Now().Add(-time.Second).UnixNano() }},
		{"over-four-hours", func(c *PhaseControlConfig) {
			c.OwnerDrainDeadlineUnixNano = time.Now().Add(4*time.Hour + time.Minute).UnixNano()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := ownerDrainDeadlineTestConfig()
			config.OwnerDrainDeadlineUnixNano = time.Now().Add(time.Hour).UnixNano()
			test.edit(&config)
			if _, err := config.validate(); !errors.Is(err, ErrConfig) {
				t.Fatalf("invalid owner-drain deadline admitted: %v", err)
			}
		})
	}

	for _, test := range []struct {
		name             string
		absolute         time.Duration
		cancelCaller     bool
		wantSuccess      bool
		wantPastOrdinary bool
	}{
		{"extended", 4 * time.Second, false, true, true},
		{"absolute-expired", 2 * time.Second, false, false, true},
		{"legacy-timeout", 0, false, false, false},
		{"caller-canceled", 4 * time.Second, true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			admissionParent, admissionChild, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = admissionParent.Close() }()
			binding := [32]byte{1}
			client, err := NewClient(ctx, admissionChild, Producer{
				ID: 6, Binding: binding, Sites: []Site{{ID: 1, Role: 1}},
			}, 12, Limits{Sites: 1, ActivePerProducer: 1, AckTimeout: time.Second, WireBytes: 2 * FrameBytes})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.conn.Close() }()
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			config := ownerDrainDeadlineTestConfig()
			if test.absolute != 0 {
				config.OwnerDrainDeadlineUnixNano = time.Now().Add(test.absolute).UnixNano()
			}
			control, err := NewPhaseControl(ctx, parent, binding, config)
			if err != nil {
				_ = child.Close()
				t.Fatal(err)
			}
			defer func() { _ = control.Close() }()
			done, err := StartPhaseControl(ctx, child, client, config)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cancel()
				_ = phaseTestResult(t, done)
			}()
			owners, err := NewOwners(ctx, OwnerLimits{Owners: 1, Requests: 1})
			if err != nil || client.bindOwners(owners) != nil {
				t.Fatal("owner registration failed", err)
			}
			turn, err := owners.Enter(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ended := false
			defer func() {
				if !ended {
					turn.End()
				}
			}()
			caller := ctx
			stopCaller := func() {}
			if test.cancelCaller {
				caller, stopCaller = context.WithCancel(ctx)
				defer stopCaller()
			}
			drained := make(chan error, 1)
			started := time.Now()
			go func() { drained <- control.DrainOwners(caller) }()
			ownerTestWait(t, owners, func() bool { return owners.paused })
			if test.wantSuccess || test.cancelCaller {
				time.Sleep(850 * time.Millisecond) // Longer than the ordinary exchange timeout.
			}
			if test.wantSuccess {
				select {
				case err := <-drained:
					t.Fatalf("owner drain returned before the held turn ended: %v", err)
				default:
				}
				turn.End()
				ended = true
				if err := <-drained; err != nil {
					t.Fatal(err)
				}
				if err := control.OpenRequests(ctx); err != nil {
					t.Fatalf("next ordinary exchange failed: %v", err)
				}
			} else {
				if test.cancelCaller {
					stopCaller()
				}
				if err := <-drained; err == nil {
					t.Fatal("held owner received a false drain ACK")
				}
				if test.wantPastOrdinary && time.Since(started) <= config.Timeout {
					t.Fatal("selected drain refused at the ordinary exchange timeout")
				}
				control.mu.Lock()
				state, index := control.state, control.index
				control.mu.Unlock()
				if state != 0 || index != 0 || control.RequestToken() != "" {
					t.Fatal("failed drain advanced or retained request authority")
				}
				owners.mu.Lock()
				ready := owners.pausedReady
				owners.mu.Unlock()
				if ready {
					t.Fatal("held owner was reported drained")
				}
				turn.End()
				ended = true
			}
		})
	}
}

func TestOwnerDrainDeadlineBootstrapSelection(t *testing.T) {
	record := productionStoreTestRecord()
	record.Producer.ID, record.Phase = 6, 12
	record.Limits.Phases = 3
	record.SemanticMode, record.InputSHA256 = ProductionSemanticV3, [32]byte{7}
	record.Control = ownerDrainDeadlineTestConfig()
	record.Control.OwnerDrainDeadlineUnixNano = time.Now().Add(time.Hour).UnixNano()
	record.Store.Producer, record.Store.Phase, record.Store.Phases = 6, 12, productionStorePhases(6)
	record.Store.Calls, record.Store.Transactions = storeaccounting.MaximumCalls, storeaccounting.MaximumTransactions
	if err := record.validate(); err != nil {
		t.Fatalf("selected phase-12 bootstrap refused: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*ProductionBootstrap)
	}{
		{"wrong-producer", func(r *ProductionBootstrap) { r.Producer.ID = 5 }},
		{"missing-store", func(r *ProductionBootstrap) { r.Store = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := record
			test.edit(&candidate)
			if err := candidate.validate(); !errors.Is(err, ErrProductionBootstrap) {
				t.Fatalf("unselected owner-drain deadline admitted: %v", err)
			}
		})
	}
}
