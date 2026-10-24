//go:build linux

package t421

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// The adapter selects the native workspace shape only; it issues no protected
// input, resource, process, SDK, checkpoint or successor authority.
func linuxRecoveryWorkspaceRun(epoch uint64, stale bool) *ExecutionEpochOneRun {
	return &ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Epoch: epoch}, staleAllowed: stale,
		flow: &ExecutionEpochOne{native: &linuxEpochCoordinator{}}}
}

func TestLinuxEpochRecoveryWorkspaceSelection(t *testing.T) {
	for _, test := range []struct {
		name string
		run  *ExecutionEpochOneRun
		want bool
	}{
		{"native-stale", linuxRecoveryWorkspaceRun(3, true), true},
		{"native-recovery", linuxRecoveryWorkspaceRun(4, false), true},
		{"native-return-only", linuxRecoveryWorkspaceRun(3, false), false},
		{"native-physical", linuxRecoveryWorkspaceRun(1, false), false},
		{"unbound", &ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Epoch: 3}, staleAllowed: true, flow: &ExecutionEpochOne{}}, false},
		{"nil-flow", &ExecutionEpochOneRun{}, false}, {"nil-run", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.run.recoveryWorkspaceSelected() != test.want {
				t.Fatal("native workspace presence was confused with legacy root presence")
			}
		})
	}
}

func TestLinuxEpochRecoveryPreparationRequiresNativeSample(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		for _, mode := range []string{"zero", "missing", "excess", "no-prior", "duplicate"} {
			t.Run(fmt.Sprintf("%t/%s", checkpoint, mode), func(t *testing.T) {
				reader := &executionEpochInspection{plan: accountingTestPlan(t), run: linuxRecoveryWorkspaceRun(3, true)}
				point := uint8(1)
				if checkpoint {
					point = 4
				}
				reader.recoverySamples.Points[point-1] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
				value := &epochRecoveryWorkspaceSample{}
				switch mode {
				case "missing":
					value = nil
				case "excess":
					value.AllocatedBytes = reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes + 1
				case "no-prior":
					reader.recoverySamples.Points[point-1] = ExecutionWorkspaceBytePhase{}
				case "duplicate":
					reader.recoverySamples.Points[point].Attempts = 1
				}
				err := reader.recordRecoveryPreparationWorkspace(value, checkpoint)
				if (err == nil) != (mode == "zero") || reader.recoverySamples.Unavailable != (err != nil && mode != "excess") || reader.recoverySamples.LimitExceeded != (mode == "excess") {
					t.Fatal("native required sample became absent/success", mode, err, reader.recoverySamples)
				}
				if mode == "zero" && (reader.recoverySamples.Points[point].Attempts != 1 || reader.recoverySamples.Points[point].Completed != 1) {
					t.Fatal("bound zero sample lost")
				}
			})
		}
	}
}

// HTTP exchanges are real and bounded; bodies and prior owner facts are
// supplied. These controls do not measure an engine or execute recovery.
func TestLinuxEpochRecoveryWorkspaceHTTP(t *testing.T) {
	for _, point := range []uint8{0, 2, 3, 6} {
		for _, mode := range []string{"valid", "conflict", "excess", "no-control", "no-prior", "duplicate"} {
			t.Run(fmt.Sprintf("%d/%s", point, mode), func(t *testing.T) {
				calls := 0
				reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					calls++
					name := "start"
					if point == 2 || point == 6 {
						name = "finish"
					}
					if request.Method != http.MethodPost || request.URL.Path != "/api/t422/lifecycle/sample-workspace" || request.Header.Get("X-Phebs-T422-Workspace-Point") != name {
						t.Error("native request binding changed")
					}
					if mode == "conflict" {
						w.WriteHeader(http.StatusConflict)
					}
					_, _ = fmt.Fprint(w, `{"logical_bytes":50,"allocated_bytes":100}`)
				}))
				reader.run.flow = linuxRecoveryWorkspaceRun(3, true).flow
				reader.run.staleAllowed, reader.run.epoch.Epoch = true, 3
				reader.projection.Phase = "stale_lease"
				if point >= 3 {
					reader.projection.Phase = "process_restart"
				}
				if point == 6 {
					reader.run.epoch.Epoch = 4
				}
				reader.finalUsed, reader.selectorCleanupPhase = point == 2 || point == 6, reader.projection.Phase
				for i := uint8(0); i < point; i++ {
					reader.recoverySamples.Points[i] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
				}
				switch mode {
				case "no-control":
					reader.run.control = nil
				case "no-prior":
					if point == 0 {
						reader.stalePrepared = true
					} else {
						reader.recoverySamples.Points[point-1] = ExecutionWorkspaceBytePhase{}
					}
				case "duplicate":
					reader.recoverySamples.Points[point].Attempts = 1
				case "excess":
					reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes = 99
				}
				err := reader.sampleRecoveryWorkspace(t.Context(), point)
				wantCalls := 0
				if mode == "valid" || mode == "conflict" || mode == "excess" {
					wantCalls = 1
				}
				if (err == nil) != (mode == "valid") || calls != wantCalls || reader.recoverySamples.LimitExceeded != (mode == "excess") || reader.recoverySamples.Unavailable != (err != nil && mode != "excess") {
					t.Fatal("native HTTP sampling bypassed required evidence", mode, err, calls, reader.recoverySamples)
				}
				if mode == "valid" || mode == "excess" {
					row := reader.recoverySamples.Points[point]
					if row.Attempts != 1 || row.Completed != 1 || row.Maximum != (custodybytes.Sample{LogicalBytes: 50, AllocatedBytes: 100}) {
						t.Fatal("actual response prefix lost", row)
					}
				}
			})
		}
	}
}

func TestLinuxEpochRecoveryParentRequiresObserver(t *testing.T) {
	run := linuxRecoveryWorkspaceRun(3, true)
	run.flow.mu.Lock()
	err := run.sampleRecoveryParentLocked(t.Context())
	run.flow.mu.Unlock()
	if err == nil || !run.result.RecoverySamples.Unavailable || run.result.RecoverySamples.Points[5] != (ExecutionWorkspaceBytePhase{}) {
		t.Fatal("native workspace silently skipped missing observation authority", err, run.result.RecoverySamples)
	}
}

func TestLinuxEpochRecoveryParentRefusalRetainsActualWalk(t *testing.T) {
	for _, mode := range []string{"canceled", "missing-accounting"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "workspace")
			if os.Mkdir(path, 0o700) != nil {
				t.Fatal("workspace fixture")
			}
			root, err := openLinuxAuthorOwnedRoot(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = root.file.Close() })
			if err := os.WriteFile(filepath.Join(path, "checkpoint"), []byte("actual tiny Linux recovery workspace"), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			observer := custodybytes.NewBorrowed(root.file, root.path, root.info, root.identity.FSID)
			value, err := observer.Sample(ctx, 8)
			if err != nil || value.LogicalBytes == 0 || value.AllocatedBytes == 0 {
				t.Fatal("actual tiny traversal", value, err)
			}
			run := recoveryParentModel()
			run.flow.native, run.flow.workspaceBytes = &linuxEpochCoordinator{}, observer
			if mode == "canceled" {
				// These empty values only allow the canceled-context structural
				// refusal; no DA/SA call or successful join is fabricated.
				run.flow.controller, run.flow.store = &dispatchadmission.Controller{}, &storeaccounting.Transport{}
				cancel()
			}
			run.flow.mu.Lock()
			err = run.sampleRecoveryParentLocked(ctx)
			run.flow.mu.Unlock()
			observed := observer.Snapshot()
			if err == nil || !observed.Unavailable || !observed.Phases[7].Completed || observed.Phases[7].Maximum != value || !run.result.RecoverySamples.Unavailable || run.result.RecoverySamples.Points[5] != (ExecutionWorkspaceBytePhase{}) {
				t.Fatal("refusal lost completed traversal or invented a parent sample", err, observed, run.result.RecoverySamples)
			}
		})
	}
}

func TestLinuxEpochRecoveryParentInputRefusals(t *testing.T) {
	for _, run := range []*ExecutionEpochOneRun{nil, {}} {
		if run.sampleRecoveryParentLocked(t.Context()) == nil {
			t.Fatal("missing run/flow became unselected success")
		}
	}
	run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{}}
	if run.sampleRecoveryParentLocked(t.Context()) != nil {
		t.Fatal("legacy unselected workspace changed")
	}
}
