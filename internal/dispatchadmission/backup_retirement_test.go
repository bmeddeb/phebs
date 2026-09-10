//go:build darwin || linux

package dispatchadmission

import (
	"bytes"
	"context"
	"encoding/json"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func backupControlConfig() PhaseControlConfig {
	return PhaseControlConfig{BackupEndpointCarry: true, OwnerControl: true, Phases: []uint32{8, 9, 10, 11}, InitialPhase: 8, MaximumPhases: 4, MaximumWireBytes: 24 * 2 * FrameBytes, Timeout: time.Second}
}

func TestBackupRetirementBootstrap(t *testing.T) {
	makeRecord := func() ProductionBootstrap {
		r := productionStoreTestRecord()
		r.Producer.ID, r.Phase, r.Limits.Phases = 5, 8, 15
		r.SemanticMode, r.InputSHA256, r.Control = ProductionSemanticV3, [32]byte{7}, backupControlConfig()
		r.Store.Producer, r.Store.Phase, r.Store.Phases, r.Store.Calls, r.Store.Transactions = 5, 8, 1920, 40, 2
		return r
	}
	if err := makeRecord().validate(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"producer", "store", "phase", "owners", "mask", "terminal", "ordinary"} {
		t.Run(mode, func(t *testing.T) {
			r := makeRecord()
			switch mode {
			case "producer":
				r.Producer.ID = 6
			case "store":
				r.Store = nil
			case "phase":
				r.Phase = 11
			case "owners":
				r.Control.OwnerControl = false
			case "mask":
				r.Control.Phases = append(r.Control.Phases, 12)
			case "terminal":
				r.Control.TerminalPhase = 8
			case "ordinary":
				r.SemanticMode = ""
			}
			if r.validate() == nil {
				t.Fatal("unbound retirement admitted")
			}
		})
	}
	raw, err := json.Marshal(productionTestRecord())
	if err != nil || bytes.Contains(raw, []byte("BackupEndpointCarry")) {
		t.Fatal("legacy bytes changed", err)
	}
	for op := phasePause; op <= phaseTerminalQuiesce; op++ {
		if _, _, err := nextConfiguredControlState(phasePause, 3, op, backupControlConfig()); err == nil {
			t.Fatal("retirement reopened", op)
		}
	}
}

// Real PC/DA/SA sockets, SDK owner and one native persistent child. The child
// is a sleeping fixture, not a database or proof of successful native backup.
func TestBackupRetirementInheritedCarry(t *testing.T) {
	for _, mode := range []string{"close", "fresh_dispatch", "fresh_store", "active_store", "resume", "unretired_close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			config := testConfig()
			config.Producers[0].ID = 5
			config.Limits.Phases = 5
			roles := config.Phases[0].Roles
			config.Phases = nil
			var phases []storeaccounting.Phase
			for phase := uint32(8); phase <= 12; phase++ {
				config.Phases = append(config.Phases, Phase{ID: phase, Roles: roles})
				phases = append(phases, storeaccounting.Phase{ID: phase, Transactions: 2, Rows: 4})
			}
			da, client, served := paired(t, config)
			lifetime, _, sa := storePhaseLifetimeFor(t, client, 5, phases, 1920)
			lifetime.program, lifetime.semanticMode, lifetime.producerID, lifetime.inputSHA256 = ProgramPhebs, ProductionSemanticV3, 5, [32]byte{7}
			if _, err := lifetime.TakeStoreOwner(); err != nil {
				t.Fatal(err)
			}
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			control, err := NewPhaseControl(ctx, parent, client.binding, backupControlConfig())
			if err != nil {
				t.Fatal(err)
			}
			done, err := StartPhaseControl(ctx, child, client, backupControlConfig())
			if err != nil {
				t.Fatal(err)
			}
			lifetime.controlDone = done
			t.Cleanup(func() { _ = control.Close(); _ = phaseTestResult(t, done) })
			owners, err := NewOwners(ctx, OwnerLimits{Owners: 1, Requests: 1})
			if err != nil || client.bindOwners(owners) != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sleep", "30")
			handle, err := client.Start(ctx, 2, command)
			if err != nil {
				t.Fatal(err)
			}
			joined := false
			defer func() {
				if !joined {
					_ = command.Process.Kill()
					_ = handle.Wait()
				}
			}()
			if err := control.DrainOwners(ctx); err != nil {
				t.Fatal(err)
			}
			for phase := uint32(9); phase <= 11; phase++ {
				if control.Pause(ctx) != nil || da.Fence() != nil || sa.Fence() != nil || control.Checkpoint(ctx) != nil || da.Advance() != nil || sa.Advance() != nil || control.Resume(ctx) != nil {
					t.Fatal("handoff", phase)
				}
			}
			if mode == "active_store" {
				db, _, _ := phaseStoreDB(t, ctx, lifetime.storeOwner, nil, false)
				if _, err := storeaccounting.SDKBegin(ctx, lifetime.storeOwner, db); err != nil {
					t.Fatal(err)
				}
				if da.Fence() != nil || sa.Fence() != nil {
					t.Fatal("fence")
				}
				if control.Pause(ctx) == nil {
					t.Fatal("retired an active SDK transaction")
				}
				return
			}
			if da.Fence() != nil || sa.Fence() != nil || control.Pause(ctx) != nil || sa.Wait(ctx, 5) != nil {
				t.Fatal("final retirement")
			}
			if mode != "unretired_close" {
				if err := da.RetireBackupEndpoint(); err != nil {
					t.Fatal(err)
				}
			}
			if da.Advance() != nil || sa.Advance() != nil {
				t.Fatal("retired advance")
			}
			if mode == "fresh_store" {
				if _, err := storeaccounting.SDKQuery[any](ctx, lifetime.storeOwner, (*surrealdb.DB)(nil), "RETURN 1", nil, storeaccounting.SDKRead()); err == nil {
					t.Fatal("retired SDK read admitted")
				}
				return
			}
			if mode == "fresh_dispatch" {
				attempt, stop := context.WithTimeout(ctx, 20*time.Millisecond)
				_, err := client.Start(attempt, 1, exec.Command("/usr/bin/true"))
				stop()
				if err == nil {
					t.Fatal("retired dispatch admitted")
				}
				return
			}
			if mode == "resume" {
				if err := client.Resume(12); err == nil {
					t.Fatal("retired SDK/server resumed")
				}
				return
			}
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = handle.Wait()
			joined = true
			err = lifetime.Close(ctx)
			if (err == nil) != (mode == "close") {
				t.Fatal(mode, err)
			}
			if mode == "close" {
				if err := <-served; err != nil {
					t.Fatal(err)
				}
				if err := lifetime.Close(ctx); err != nil {
					t.Fatal("retired close not idempotent", err)
				}
				snapshot, err := da.Snapshot()
				if err != nil || snapshot.Attempts != 1 || !snapshot.Producers[0].Closed {
					t.Fatal(snapshot, err)
				}
			}
		})
	}
}
