//go:build darwin || linux

package t421

import (
	"context"
	"io"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// This inherited test-binary child proves protocol mechanics only. Its generic
// tool locations are never executed and are not admitted production images.
func TestExecutionEpochHandoffHelper(t *testing.T) {
	if os.Getenv("PHEBS_EPOCH_HANDOFF_HELPER") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatalf("fixture bootstrap: %v", err)
	}
	owner, err := lifetime.TakeStoreOwner()
	if err != nil || owner == nil || owner.Check(ctx) != nil {
		t.Fatalf("actual SDK owner: %v", err)
	}
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatalf("actual owner control: %v", err)
	}
	epochHandoffByte(t, os.Stdout, 'R')
	epochHandoffRead(t, os.Stdin, 'V')
	state, err := dispatchadmission.ProductionSemanticState()
	if err != nil || state.Phase != 3 || state.ProducerID != 2 || !state.OrdinaryOwnersDrained || owner.Check(ctx) != nil {
		t.Fatalf("resume lost actual phase/SDK/owner fence: %+v / %v", state, err)
	}
	if _, err := owners.EnterRequest(ctx); err == nil {
		t.Fatal("resume reopened requests")
	}
	epochHandoffByte(t, os.Stdout, 'W')
	epochHandoffRead(t, os.Stdin, 'C')
	if err := lifetime.Close(ctx); err != nil {
		t.Fatalf("joined fixture close: %v", err)
	}
}

func epochHandoffByte(t *testing.T, writer io.Writer, value byte) {
	t.Helper()
	if n, err := writer.Write([]byte{value}); err != nil || n != 1 {
		t.Fatalf("bounded fixture signal write: %d / %v", n, err)
	}
}

func epochHandoffRead(t *testing.T, reader io.Reader, want byte) {
	t.Helper()
	var raw [1]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil || raw[0] != want {
		t.Fatalf("bounded fixture signal: %q, want %q: %v", raw, want, err)
	}
}

func TestExecutionEpochAdvanceColdInheritedHandoff(t *testing.T) {
	for _, mode := range []string{"healthy", "canceled"} {
		t.Run(mode, func(t *testing.T) { testEpochInheritedHandoff(t, mode == "canceled") })
	}
}

func testEpochInheritedHandoff(t *testing.T, canceled bool) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	rootSite := dispatchadmission.Site{ID: executionSiteServe, Role: executionRolePhebs, Persistent: true}
	root := dispatchadmission.Producer{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{rootSite}}
	server := dispatchadmission.Producer{ID: 2, Binding: [32]byte{2}, Sites: dispatchadmission.ProductionSites()}
	limits := dispatchadmission.Limits{Producers: 2, Sites: 17, Roles: 5, Phases: 3,
		ActivePerProducer: 1, Attempts: 1, WireBytes: 4096, AckTimeout: 5 * time.Second}
	var phases []dispatchadmission.Phase
	for _, phase := range []uint32{2, 3, 4} {
		roles := []dispatchadmission.RoleBudget{{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal},
			{Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility}, {Role: executionRolePhebs}}
		if phase == 2 {
			roles[4].Attempts = 1 // The actual fixture child, not a Phebs launch claim.
		}
		phases = append(phases, dispatchadmission.Phase{ID: phase, Roles: roles})
	}
	dispatch, err := dispatchadmission.New(ctx, dispatchadmission.Config{Limits: limits, Producers: []dispatchadmission.Producer{root, server}, Phases: phases})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := dispatch.NewLocalProducer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(context.Background()) }()
	// Bootstrap requires the genuine fixed server capacity and phase mask;
	// no store query/transaction or phase-four work is fabricated here.
	store, err := storeaccounting.New(ctx, storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: 2, Calls: 40, Transactions: 2}},
		Phases:    []storeaccounting.Phase{{ID: 2, Transactions: 1, Rows: 1}, {ID: 3}, {ID: 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(ctx, store, storeaccounting.WireConfig{
		Producers: []storeaccounting.WireProducer{{ID: 2, Binding: server.Binding, Phases: 14}}, AckTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Close() }()
	storeChild, storeConfig, err := transport.Open(2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeChild.Close() }()
	daParent, daChild, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = daParent.Close(); _ = daChild.Close() }()
	pcParent, pcChild, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pcParent.Close(); _ = pcChild.Close() }()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutionEpochHandoffHelper$")
	command.Env = []string{"PHEBS_EPOCH_HANDOFF_HELPER=1", "GORACE=atexit_sleep_ms=0",
		dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionStoreSelector}
	command.ExtraFiles, command.Stderr, command.WaitDelay = []*os.File{daChild, pcChild, storeChild}, os.Stderr, time.Second
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
	handle, err := parent.StartInPhase(ctx, 2, rootSite, command)
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
	if daChild.Close() != nil || pcChild.Close() != nil || storeChild.Close() != nil {
		t.Fatal("fixture inherited descriptor release")
	}
	base := []string{"HOME=/tmp", "TMPDIR=/tmp", "TMP=/tmp", "TEMP=/tmp", "PATH=/tmp", "LANG=C", "LC_ALL=C", "TZ=UTC"}
	git := append(slices.Clone(base), "GIT_EXEC_PATH=/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR=/dev/null", "GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_KEY_1=core.untrackedCache", "GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_VALUE_2=/dev/null")
	pcConfig := dispatchadmission.PhaseControlConfig{OwnerControl: true, Phases: []uint32{2, 3, 4}, InitialPhase: 2,
		MaximumPhases: 3, MaximumWireBytes: 8 * 2 * dispatchadmission.FrameBytes, Timeout: 5 * time.Second}
	record := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramPhebs,
		SemanticMode: dispatchadmission.ProductionSemanticV3, InputSHA256: [32]byte{3}, Producer: server, Phase: 2,
		Limits: limits, Control: pcConfig, Store: &storeConfig,
		Tools: []dispatchadmission.ProductionToolBinding{{Role: "git", Path: "/bin/sh", Environment: git},
			{Role: "surreal", Path: "/bin/sh", Environment: base}, {Role: "zoekt-git-index", Path: "/bin/sh", Environment: git}},
	}
	if err := dispatchadmission.SendProductionBootstrap(ctx, daParent, pcParent, record); err != nil {
		t.Fatal(err)
	}
	control, err := dispatchadmission.NewPhaseControl(ctx, pcParent, server.Binding, pcConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = control.Close() }()
	served := make(chan error, 1)
	go func() { served <- dispatch.Serve(ctx, 2, command.Process.Pid, daParent) }()
	serverJoined := false
	defer func() {
		cancel()
		if !serverJoined {
			<-served
		}
	}()
	epochHandoffRead(t, output, 'R')
	if control.DrainOwners(ctx) != nil || control.OpenRequests(ctx) != nil || control.FenceRequests(ctx) != nil {
		t.Fatal("fixture owner/request fences failed")
	}
	run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{controller: dispatch, parent: parent, store: transport}, control: control}
	if canceled {
		canceledCtx, stop := context.WithCancel(ctx)
		stop()
		if run.advanceCold(canceledCtx) == nil {
			t.Fatal("canceled handoff succeeded")
		}
		da, _ := dispatch.Snapshot()
		sa, _ := transport.Snapshot()
		if da.Producers[1].Checkpoint != 0 || sa.Store.Phase != 2 || sa.Store.Producers[0].Checkpoint != 0 || da.Complete || sa.Complete {
			t.Fatalf("canceled handoff advanced or invented closure: %+v / %+v", da, sa)
		}
		return
	}
	if err := run.advanceCold(ctx); err != nil {
		t.Fatalf("actual parent handoff: %v", err)
	}
	da, daErr := dispatch.Snapshot()
	sa, saErr := transport.Snapshot()
	if daErr != nil || saErr != nil || da.Producers[1].Checkpoint != 2 || sa.Store.Phase != 3 ||
		sa.Store.Producers[0].Checkpoint != 2 || da.Producers[0].Active != 1 || control.RequestToken() != "" ||
		da.Attempts != 1 || sa.Store.Transactions != 0 || da.Complete || sa.Complete {
		t.Fatalf("handoff lost actual carried/checkpoint/fenced prefix: %+v / %+v / %v / %v", da, sa, daErr, saErr)
	}
	epochHandoffByte(t, input, 'V')
	epochHandoffRead(t, output, 'W')
	// Resume kept the owners fenced: a second DrainOwners would be invalid.
	if control.Pause(ctx) != nil || parent.Pause(ctx) != nil || dispatch.Fence() != nil {
		t.Fatal("quiet resumed-phase stop failed")
	}
	if control.ReservedWireBytes() != 7*2*dispatchadmission.FrameBytes {
		t.Fatal("handoff added a PC01 operation")
	}
	epochHandoffByte(t, input, 'C')
	err = handle.Wait()
	joined = true
	if err != nil || transport.Wait(ctx, 2) != nil || control.Close() != nil || parent.Close(ctx) != nil {
		t.Fatalf("fixture native/protocol join failed: %v", err)
	}
	err = <-served
	serverJoined = true
	if err != nil {
		t.Fatalf("fixture dispatch receiver failed: %v", err)
	}
	sa, err = transport.Snapshot()
	if err != nil || sa.Store.Phase != 3 || sa.Opened != 1 || sa.TerminalEOF != 1 || sa.Complete {
		t.Fatalf("early closed prefix invented phase-four completion: %+v / %v", sa, err)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
}
