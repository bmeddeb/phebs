//go:build darwin || linux

package t421

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// Inherited test-binary protocol fixture only. The generic tool bindings are
// never executed and do not establish admitted Phebs or native logical work.
func TestExecutionEpochLogicalTransportHelper(t *testing.T) {
	if os.Getenv("PHEBS_LOGICAL_TRANSPORT_HELPER") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := lifetime.TakeStoreOwner()
	if err != nil || owner.Check(ctx) != nil {
		t.Fatal("store owner", err)
	}
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal("owners", err)
	}
	epochHandoffByte(t, os.Stdout, 'R')
	epochHandoffRead(t, os.Stdin, 'C')
	if lifetime.Close(ctx) != nil {
		t.Fatal("terminal EOF")
	}
}

func TestExecutionEpochLogicalInheritedSuccessor(t *testing.T) {
	for _, mode := range []string{"joined", "canceled_handoff"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			site := dispatchadmission.Site{ID: executionSiteServe, Role: executionRolePhebs, Persistent: true}
			root := dispatchadmission.Producer{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{site}}
			servers := []dispatchadmission.Producer{{ID: 2, Binding: [32]byte{2}, Sites: dispatchadmission.ProductionSites()}, {ID: 3, Binding: [32]byte{3}, Sites: dispatchadmission.ProductionSites()}}
			limits := dispatchadmission.Limits{Producers: 3, Sites: 33, Roles: 5, Phases: 4, ActivePerProducer: 1, Attempts: 2, WireBytes: 8192, AckTimeout: 5 * time.Second}
			phases := []dispatchadmission.Phase{}
			for _, id := range []uint32{2, 3, 4, 5} {
				phases = append(phases, dispatchadmission.Phase{ID: id, Roles: []dispatchadmission.RoleBudget{{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal}, {Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility}, {Role: executionRolePhebs, Attempts: 1}}})
			}
			da, err := dispatchadmission.New(ctx, dispatchadmission.Config{Limits: limits, Producers: append([]dispatchadmission.Producer{root}, servers...), Phases: phases})
			if err != nil {
				t.Fatal(err)
			}
			parent, err := da.NewLocalProducer(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Close(context.Background()) }()
			sa, err := storeaccounting.New(ctx, storeaccounting.Config{Producers: []storeaccounting.Producer{{ID: 2, Calls: 40, Transactions: 2}, {ID: 3, Calls: 40, Transactions: 2}}, Phases: []storeaccounting.Phase{{ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}})
			if err != nil {
				t.Fatal(err)
			}
			transport, err := storeaccounting.NewTransport(ctx, sa, storeaccounting.WireConfig{Producers: []storeaccounting.WireProducer{{ID: 2, Binding: servers[0].Binding, Phases: 14}, {ID: 3, Binding: servers[1].Binding, Phases: 1 << 4}}, AckTimeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = transport.Close() }()
			flow := &ExecutionEpochOne{controller: da, parent: parent, store: transport}
			for phase := uint32(2); phase < 4; phase++ {
				if parent.Pause(ctx) != nil || da.Fence() != nil || parent.Checkpoint(ctx) != nil || transport.Fence() != nil || da.Advance() != nil || transport.Advance() != nil || parent.Resume(phase+1) != nil {
					t.Fatal("empty fixture phase advance")
				}
			}
			for index, server := range servers {
				phase := uint32(index + 4)
				childSA, saConfig, err := transport.Open(server.ID)
				if err != nil {
					t.Fatal(err)
				}
				daParent, daChild, err := dispatchadmission.NewPipe()
				if err != nil {
					t.Fatal(err)
				}
				pcParent, pcChild, err := dispatchadmission.NewPipe()
				if err != nil {
					t.Fatal(err)
				}
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutionEpochLogicalTransportHelper$")
				command.Env = []string{"PHEBS_LOGICAL_TRANSPORT_HELPER=1", "GORACE=atexit_sleep_ms=0", dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionStoreSelector}
				command.ExtraFiles = []*os.File{daChild, pcChild, childSA}
				command.Stderr = os.Stderr
				command.WaitDelay = time.Second
				input, err := command.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = input.Close() }()
				output, err := command.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = output.Close() }()
				handle, err := parent.StartInPhase(ctx, phase, site, command)
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
				if daChild.Close() != nil || pcChild.Close() != nil || childSA.Close() != nil {
					t.Fatal("child endpoint close")
				}
				base := []string{"HOME=/tmp", "TMPDIR=/tmp", "TMP=/tmp", "TEMP=/tmp", "PATH=/tmp", "LANG=C", "LC_ALL=C", "TZ=UTC"}
				git := append(slices.Clone(base), "GIT_EXEC_PATH=/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR=/dev/null", "GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_KEY_1=core.untrackedCache", "GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_VALUE_2=/dev/null")
				pc := dispatchadmission.PhaseControlConfig{OwnerControl: true, Phases: []uint32{phase}, InitialPhase: phase, MaximumPhases: 1, MaximumWireBytes: 5 * 2 * dispatchadmission.FrameBytes, Timeout: 5 * time.Second}
				if index == 0 {
					pc.Phases, pc.MaximumPhases = []uint32{2, 3, 4}, 3
				}
				bootstrap := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramPhebs, SemanticMode: dispatchadmission.ProductionSemanticV3, InputSHA256: [32]byte{byte(phase)}, Producer: server, Phase: phase, Limits: limits, Control: pc, Store: &saConfig, Tools: []dispatchadmission.ProductionToolBinding{{Role: "git", Path: "/bin/sh", Environment: git}, {Role: "surreal", Path: "/bin/sh", Environment: base}, {Role: "zoekt-git-index", Path: "/bin/sh", Environment: git}}}
				if dispatchadmission.SendProductionBootstrap(ctx, daParent, pcParent, bootstrap) != nil {
					t.Fatal("bootstrap")
				}
				control, err := dispatchadmission.NewPhaseControl(ctx, pcParent, server.Binding, pc)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = control.Close() }()
				served := make(chan error, 1)
				go func() { served <- da.Serve(ctx, server.ID, command.Process.Pid, daParent) }()
				epochHandoffRead(t, output, 'R')
				if control.DrainOwners(ctx) != nil || control.OpenRequests(ctx) != nil || control.FenceRequests(ctx) != nil || control.Pause(ctx) != nil || parent.Pause(ctx) != nil || da.Fence() != nil {
					t.Fatal("drained pause")
				}
				if control.RequestToken() != "" || control.ReservedWireBytes() != 4*2*dispatchadmission.FrameBytes {
					t.Fatal("unexpected PC request count/token")
				}
				if index == 0 {
					if transport.Fence() != nil || transport.Advance() != storeaccounting.ErrBusy {
						t.Fatal("ending SA lifetime advanced before EOF")
					}
				}
				epochHandoffByte(t, input, 'C')
				if err := handle.Wait(); err != nil {
					t.Fatal(err)
				}
				joined = true
				if err := <-served; err != nil {
					t.Fatal(err)
				}
				if transport.Wait(ctx, server.ID) != nil || control.Close() != nil {
					t.Fatal("joined EOF")
				}
				if index == 0 {
					handoffCtx := ctx
					if mode == "canceled_handoff" {
						var stop context.CancelFunc
						handoffCtx, stop = context.WithCancel(ctx)
						stop()
					}
					err := (&ExecutionEpochOneRun{flow: flow}).advanceLogical(handoffCtx)
					if mode == "canceled_handoff" {
						if err == nil {
							t.Fatal("canceled handoff advanced")
						}
						return
					}
					if err != nil {
						t.Fatal("retained parent advance", err)
					}
					view, err := da.ProducerLaunch(3)
					if err != nil || view.Phase != 5 {
						t.Fatal("wrong successor phase", view, err)
					}
					prefix, _ := da.Snapshot()
					for _, p := range prefix.Producers {
						if p.Producer == 1 && (p.Closed || p.Active != 0) {
							t.Fatal("root not retained open")
						}
					}
				}
			}
			if parent.Close(ctx) != nil {
				t.Fatal("final root close")
			}
			prefix, err := transport.Snapshot()
			if err != nil || prefix.Opened != 2 || prefix.TerminalEOF != 2 {
				t.Fatal("two EOFs", prefix, err)
			}
		})
	}
}
