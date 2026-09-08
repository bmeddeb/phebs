//go:build darwin || linux

package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
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
	checkpoint := os.Getenv("PHEBS_EPOCH_HANDOFF_CHECKPOINT") == "1"
	var terminalTurn dispatchadmission.OwnerTurn
	terminalReady := make(chan struct{})
	if checkpoint && dispatchadmission.BindProductionTerminalQuiescence(func(ctx context.Context) error {
		select {
		case <-terminalReady:
			return terminalTurn.FenceTerminal(ctx)
		case <-ctx.Done():
			return ctx.Err()
		}
	}) != nil {
		t.Fatal("fixture terminal capability binding")
	}
	epochHandoffByte(t, os.Stdout, 'R')
	epochHandoffRead(t, os.Stdin, 'V')
	state, err := dispatchadmission.ProductionSemanticState()
	wantPhase, wantProducer := uint32(3), uint32(2)
	if os.Getenv("PHEBS_EPOCH_HANDOFF_STALE") == "1" {
		wantPhase, wantProducer = 7, 4
	}
	if err != nil || state.Phase != wantPhase || state.ProducerID != wantProducer || !state.OrdinaryOwnersDrained || owner.Check(ctx) != nil {
		t.Fatalf("resume lost actual phase/SDK/owner fence: %+v / %v", state, err)
	}
	if _, err := owners.EnterRequest(ctx); err == nil {
		t.Fatal("resume reopened requests")
	}
	epochHandoffByte(t, os.Stdout, 'W')
	if checkpoint {
		epochHandoffRead(t, os.Stdin, 'H')
		terminalTurn, err = owners.Enter(ctx)
		if err != nil {
			t.Fatal("genuine held fixture owner", err)
		}
		close(terminalReady)
		epochHandoffByte(t, os.Stdout, 'h')
		<-ctx.Done() // Owned kill, never an ordinary End/Close claim.
		return
	}
	if os.Getenv("PHEBS_EPOCH_HANDOFF_WARM") == "1" {
		epochHandoffRead(t, os.Stdin, 'O')
		state, err := dispatchadmission.ProductionSemanticState()
		if err != nil || state.Phase != 3 || state.OrdinaryOwnersDrained || owner.Check(ctx) != nil {
			t.Fatalf("warm reopen lost phase/SDK/live-owner state: %+v / %v", state, err)
		}
		turn, err := owners.Enter(ctx)
		if err != nil {
			t.Fatalf("warm reopen did not admit actual owner entry: %v", err)
		}
		turn.End()
		request, err := owners.EnterRequest(ctx)
		if err != nil {
			t.Fatalf("ordinary owner reopen did not reopen request entry: %v", err)
		}
		request.End()
		epochHandoffByte(t, os.Stdout, 'o')
		epochHandoffRead(t, os.Stdin, 'Q')
		state, err = dispatchadmission.ProductionSemanticState()
		if err != nil || state.Phase != 3 || !state.OrdinaryOwnersDrained {
			t.Fatalf("warm request window lost owner drain: %+v / %v", state, err)
		}
		request, err = owners.EnterRequest(ctx)
		if err != nil {
			t.Fatalf("warm request window did not admit actual request entry: %v", err)
		}
		request.End()
		epochHandoffByte(t, os.Stdout, 'q')
		epochHandoffRead(t, os.Stdin, 'F')
		if _, err := owners.EnterRequest(ctx); err == nil {
			t.Fatal("warm request fence left request entry open")
		}
		epochHandoffByte(t, os.Stdout, 'f')
	}
	if os.Getenv("PHEBS_EPOCH_HANDOFF_PHYSICAL") == "1" {
		epochHandoffRead(t, os.Stdin, 'P')
		state, err := dispatchadmission.ProductionSemanticState()
		if err != nil || state.Phase != 4 || !state.OrdinaryOwnersDrained || owner.Check(ctx) != nil {
			t.Fatal("phase-four pin lost actual phase/SDK/owner fence", state, err)
		}
		request, err := owners.EnterRequest(ctx)
		if err != nil {
			t.Fatal("phase-four pin window refused request", err)
		}
		request.End()
		epochHandoffByte(t, os.Stdout, 'p')
		epochHandoffRead(t, os.Stdin, 'J')
		if _, err := owners.EnterRequest(ctx); err == nil {
			t.Fatal("pin tail was not joined")
		}
		epochHandoffByte(t, os.Stdout, 'j')
		if os.Getenv("PHEBS_EPOCH_HANDOFF_STOP_JOIN") != "1" {
			epochHandoffRead(t, os.Stdin, 'B')
			state, err = dispatchadmission.ProductionSemanticState()
			if err != nil || state.Phase != 4 || state.OrdinaryOwnersDrained || owner.Check(ctx) != nil {
				t.Fatal("phase-four reopen lost owners", state, err)
			}
			turn, err := owners.Enter(ctx)
			if err != nil {
				t.Fatal(err)
			}
			turn.End()
			epochHandoffByte(t, os.Stdout, 'b')
			epochHandoffRead(t, os.Stdin, 'D')
			state, err = dispatchadmission.ProductionSemanticState()
			if err != nil || state.Phase != 4 || !state.OrdinaryOwnersDrained {
				t.Fatal("phase-four final lost owner fence", state, err)
			}
			request, err := owners.EnterRequest(ctx)
			if err != nil {
				t.Fatal(err)
			}
			request.End()
			epochHandoffByte(t, os.Stdout, 'd')
			epochHandoffRead(t, os.Stdin, 'Z')
			if _, err := owners.EnterRequest(ctx); err == nil {
				t.Fatal("phase-four final request tail not joined")
			}
			epochHandoffByte(t, os.Stdout, 'z')
		}
	}
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
	for _, mode := range []string{"healthy", "canceled", "warm", "warm_pending_x", "warm_pending_t", "warm_canceled_read", "warm_physical", "warm_physical_pin_refused", "warm_physical_stop_join"} {
		t.Run(mode, func(t *testing.T) { testEpochInheritedHandoff(t, mode) })
	}
}

func TestExecutionEpochStaleInheritedHandoff(t *testing.T) {
	for _, mode := range []string{"stale", "stale_observe", "stale_stop_join"} {
		t.Run(mode, func(t *testing.T) { testEpochInheritedHandoff(t, mode) })
	}
}

func TestExecutionEpochCheckpointInheritedTerminal(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("owned terminal session helper is Darwin-only")
	}
	for _, mode := range []string{"stale_checkpoint", "stale_checkpoint_partial"} {
		t.Run(mode, func(t *testing.T) { testEpochInheritedHandoff(t, mode) })
	}
}

func testEpochInheritedHandoff(t *testing.T, mode string) {
	canceled, warm := mode == "canceled", strings.HasPrefix(mode, "warm")
	physical := strings.HasPrefix(mode, "warm_physical")
	stale := strings.HasPrefix(mode, "stale")
	checkpoint := strings.HasPrefix(mode, "stale_checkpoint")
	phaseIDs, serverID := []uint32{2, 3, 4}, uint32(2)
	if stale {
		phaseIDs, serverID = []uint32{6, 7, 8}, 4
	}
	initialPhase, resumedPhase := phaseIDs[0], phaseIDs[1]
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	rootSite := dispatchadmission.Site{ID: executionSiteServe, Role: executionRolePhebs, Persistent: true}
	root := dispatchadmission.Producer{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{rootSite}}
	server := dispatchadmission.Producer{ID: serverID, Binding: [32]byte{2}, Sites: dispatchadmission.ProductionSites()}
	limits := dispatchadmission.Limits{Producers: 2, Sites: 17, Roles: 5, Phases: len(phaseIDs),
		ActivePerProducer: 1, Attempts: 1, WireBytes: 4096, AckTimeout: 5 * time.Second}
	var phases []dispatchadmission.Phase
	for _, phase := range phaseIDs {
		roles := []dispatchadmission.RoleBudget{{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal},
			{Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility}, {Role: executionRolePhebs}}
		if phase == initialPhase {
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
	var storePhases []storeaccounting.Phase
	var phaseMask uint16
	for _, id := range phaseIDs {
		storePhases = append(storePhases, storeaccounting.Phase{ID: id})
		phaseMask |= 1 << (id - 1)
	}
	storePhases[0].Transactions, storePhases[0].Rows = 1, 1
	store, err := storeaccounting.New(ctx, storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: serverID, Calls: 40, Transactions: 2}},
		Phases:    storePhases,
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(ctx, store, storeaccounting.WireConfig{
		Producers: []storeaccounting.WireProducer{{ID: serverID, Binding: server.Binding, Phases: phaseMask}}, AckTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Close() }()
	storeChild, storeConfig, err := transport.Open(serverID)
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
	if warm {
		command.Env = append(command.Env, "PHEBS_EPOCH_HANDOFF_WARM=1")
	}
	if physical {
		command.Env = append(command.Env, "PHEBS_EPOCH_HANDOFF_PHYSICAL=1")
	}
	if stale {
		command.Env = append(command.Env, "PHEBS_EPOCH_HANDOFF_STALE=1")
	}
	if checkpoint {
		command.Env = append(command.Env, "PHEBS_EPOCH_HANDOFF_CHECKPOINT=1")
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if mode == "warm_physical_stop_join" || mode == "stale_stop_join" {
		command.Env = append(command.Env, "PHEBS_EPOCH_HANDOFF_STOP_JOIN=1")
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
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
	handle, err := parent.StartInPhase(ctx, initialPhase, rootSite, command)
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
	pairs := uint64(8)
	if warm {
		pairs = 12
	}
	if physical {
		pairs = 21
	}
	if mode == "stale_observe" {
		pairs = 14
	}
	if checkpoint {
		pairs = 21
	}
	pcConfig := dispatchadmission.PhaseControlConfig{OwnerControl: true, Phases: phaseIDs, InitialPhase: initialPhase,
		MaximumPhases: len(phaseIDs), MaximumWireBytes: pairs * 2 * dispatchadmission.FrameBytes, Timeout: 5 * time.Second}
	if checkpoint {
		pcConfig.TerminalPhase = 8
	}
	record := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramPhebs,
		SemanticMode: dispatchadmission.ProductionSemanticV3, InputSHA256: [32]byte{3}, Producer: server, Phase: initialPhase,
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
	go func() { served <- dispatch.Serve(ctx, serverID, command.Process.Pid, daParent) }()
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
	advance := run.advanceCold
	if stale {
		advance = run.advanceStale
		run.epoch.Epoch = 3
	}
	if mode == "stale_observe" {
		advance = func(ctx context.Context) error {
			testEpochInheritedStaleObservation(t, ctx, run)
			return nil
		}
	}
	if err := advance(ctx); err != nil {
		t.Fatalf("actual parent handoff: %v", err)
	}
	da, daErr := dispatch.Snapshot()
	sa, saErr := transport.Snapshot()
	if daErr != nil || saErr != nil || da.Producers[1].Checkpoint != initialPhase || sa.Store.Phase != resumedPhase ||
		sa.Store.Producers[0].Checkpoint != initialPhase || da.Producers[0].Active != 1 || control.RequestToken() != "" ||
		da.Attempts != 1 || sa.Store.Transactions != 0 || da.Complete || sa.Complete {
		t.Fatalf("handoff lost actual carried/checkpoint/fenced prefix: %+v / %+v / %v / %v", da, sa, daErr, saErr)
	}
	epochHandoffByte(t, input, 'V')
	epochHandoffRead(t, output, 'W')
	if checkpoint {
		// Exercise actual PC/DA/SDK socket transitions and process cleanup,
		// not a source/author/HTTP F claim. The absent prior authors therefore
		// deliberately keep finish's full epoch prefix unavailable.
		if control.OpenRequests(ctx) != nil || control.FenceRequests(ctx) != nil || control.ReopenOwners(ctx) != nil ||
			control.DrainOwners(ctx) != nil || control.OpenRequests(ctx) != nil || control.FenceRequests(ctx) != nil ||
			run.advanceReturnPhase(ctx, 8) != nil || control.OpenRequests(ctx) != nil || control.FenceRequests(ctx) != nil || control.ReopenOwners(ctx) != nil {
			t.Fatal("phase8 inherited preparation sequence")
		}
		epochHandoffByte(t, input, 'H')
		epochHandoffRead(t, output, 'h')
		op, stopOperation := context.WithCancel(ctx)
		defer stopOperation()
		run.stop, run.done, run.checkpointDone = make(chan struct{}), make(chan struct{}), make(chan struct{})
		run.checkpointAllowed, run.checkpointUsed, run.checkpointCancel, run.phaseDeadline = true, true, stopOperation, time.Now().Add(10*time.Second)
		if run.enterTerminal(op) != nil || control.TerminalQuiesce(op) != nil {
			t.Fatal("actual terminal owner ACK")
		}
		if mode == "stale_checkpoint" {
			if parent.Pause(op) != nil || dispatch.Fence() != nil || transport.Fence() != nil || control.Checkpoint(op) != nil || transport.ArmTerminalEOF(4, 8) != nil || dispatch.ExpectHardDeath(4) != nil {
				t.Fatal("actual terminal SDK checkpoint/arm")
			}
			run.terminalRequested, run.terminalContext, run.retainParent = true, op, true
		} else {
			stopOperation()
		}
		operationCanceled := make(chan struct{})
		if mode == "stale_checkpoint_partial" {
			run.checkpointCancel = func() { stopOperation(); close(operationCanceled) }
		} else {
			close(run.checkpointDone)
		}
		custody := &ExecutionAuthorCustody{borrowedBy: run}
		run.flow.epochs = &ExecutionEpochConfigCustody{author: custody, active: true}
		run.flow.release = cancel
		run.command = command
		waited := make(chan error, 1)
		go func() { waited <- handle.Wait() }()
		joined = true // finish, not fixture defer, owns the sole Wait.
		run.stopOnce.Do(func() { close(run.stop) })
		go run.finish(ctx, func() {}, waited, served, nil)
		if mode == "stale_checkpoint_partial" {
			<-operationCanceled
			select {
			case <-run.done:
				t.Fatal("terminal failure skipped its operation join")
			default:
			}
			close(run.checkpointDone)
		}
		<-run.done
		serverJoined = true
		if run.err == nil || !run.result.RootJoined || !run.result.SessionEmpty {
			t.Fatal("terminal fixture cleanup or absent-author refusal", run.err, run.result)
		}
		wantPairs := uint64(20)
		if mode == "stale_checkpoint_partial" {
			wantPairs = 19
		}
		if control.ReservedWireBytes() != wantPairs*128 {
			t.Fatal("terminal cleanup issued an ordinary PC operation")
		}
		if mode == "stale_checkpoint" {
			if run.nativeStopErr != nil || !run.result.Store.Store.Producers[0].TerminalFencedEOF || run.result.Store.Store.Producers[0].Closed || !run.result.Accounting.Producers[1].Closed {
				t.Fatal("actual terminal EOF/native join lost", run.nativeStopErr, run.result)
			}
		} else if run.nativeStopErr == nil || run.result.Store.Store.Producers[0].TerminalFencedEOF {
			t.Fatal("partial terminal failure fabricated closure")
		}
		return
	}
	if mode == "stale_stop_join" {
		custody := &ExecutionAuthorCustody{borrowedBy: run}
		run.flow.epochs = &ExecutionEpochConfigCustody{author: custody, active: true}
		run.command, run.done, run.staleDone = command, make(chan struct{}), make(chan struct{})
		operationCanceled := make(chan struct{})
		run.staleCancel = func() { close(operationCanceled) }
		waited := make(chan error, 1)
		go func() { waited <- handle.Wait() }()
		go run.finish(ctx, func() {}, waited, served, ErrExecutionEpochOne)
		<-operationCanceled
		if custody.Close() == nil || custody.closed || !run.flow.epochs.active {
			t.Fatal("Stop released custody before stale operation joined")
		}
		select {
		case <-run.done:
			t.Fatal("Stop skipped stale operation join")
		default:
		}
		epochHandoffByte(t, input, 'C')
		close(run.staleDone)
		<-run.done
		joined, serverJoined = true, true
		if run.err == nil || !run.result.RootJoined || !run.result.SessionEmpty || custody.borrowedBy != nil || custody.Close() != nil {
			t.Fatal("stale stop lost sticky failure or joined source custody", run.result, run.err)
		}
		return
	}
	if warm {
		warmMode := mode
		if physical {
			warmMode = "warm"
		}
		testEpochInheritedWarmObservation(t, ctx, run, input, output, warmMode)
		if warmMode != "warm" {
			return // Joined fixture kill/receiver cleanup; no successful stop claim.
		}
		epochHandoffByte(t, input, 'F')
		epochHandoffRead(t, output, 'f')
	}
	if physical {
		if run.advancePhysical(ctx) != nil {
			t.Fatal("actual phase-four DA/SA/PC handoff failed")
		}
		da, daErr := dispatch.Snapshot()
		sa, saErr := transport.Snapshot()
		if daErr != nil || saErr != nil || da.Producers[1].Checkpoint != 3 || sa.Store.Phase != 4 || sa.Store.Producers[0].Checkpoint != 3 || control.RequestToken() != "" {
			t.Fatal("physical handoff lost exact checkpoint or reopened requests", da, sa)
		}
		pinServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodPost || request.URL.Path != "/api/t422/retention/pin" || request.Header.Get("Authorization") != "Bearer private-key" ||
				request.Header.Get(dispatchadmission.ProductionRequestHeader) != control.RequestToken() || control.RequestToken() == "" || request.Header.Get("X-Phebs-T421-Exact-Reads") != "" {
				t.Error("pin lost fixed authenticated owner-drained window")
			}
			epochHandoffByte(t, input, 'P')
			epochHandoffRead(t, output, 'p')
			body := `{"status":"complete"}`
			if mode == "warm_physical_pin_refused" {
				body += "\n"
			}
			_, _ = io.WriteString(w, body)
		}))
		defer pinServer.Close()
		run.epoch.Listen = strings.TrimPrefix(pinServer.URL, "http://")
		err := run.pinPhysical(ctx)
		if mode == "warm_physical_pin_refused" {
			if err == nil || run.physicalPinned || !run.pinStarted.IsZero() {
				t.Fatal("bad pin body admitted author permission")
			}
			return // Forced fixture join, not a successful phase stop.
		}
		if err != nil || !run.physicalPinned || run.pinStarted.IsZero() || run.pinJoined.Before(run.pinStarted) || control.RequestToken() != "" {
			t.Fatal("actual pin protocol did not join before author permission", err)
		}
		epochHandoffByte(t, input, 'J')
		epochHandoffRead(t, output, 'j')
		if mode == "warm_physical_stop_join" {
			// The operation channel models outstanding author work, not an
			// author process. Stop must join it before native/control cleanup.
			custody := &ExecutionAuthorCustody{borrowedBy: run, active: true}
			run.flow.epochs = &ExecutionEpochConfigCustody{author: custody, active: true}
			run.command, run.done = command, make(chan struct{})
			run.physicalDone = make(chan struct{})
			operationCanceled := make(chan struct{})
			run.physicalCancel = func() { close(operationCanceled) }
			waited := make(chan error, 1)
			go func() { waited <- handle.Wait() }()
			go run.finish(ctx, func() {}, waited, served, ErrExecutionEpochOne)
			<-operationCanceled
			if custody.Close() == nil || custody.closed || !run.flow.epochs.active {
				t.Fatal("Stop released custody before physical operation joined")
			}
			select {
			case <-run.done:
				t.Fatal("Stop skipped physical operation join")
			default:
			}
			epochHandoffByte(t, input, 'C')
			// Retain the modeled author survivor even after the server joins.
			close(run.physicalDone)
			<-run.done
			joined, serverJoined = true, true // finish owns the sole Wait/Serve joins.
			if got := run.stopDiagnostic; got.Wake != "supplied_failure" || got.Before.WakeError != ErrExecutionEpochOne ||
				got.Before.Dispatch != nil || got.Before.Store != nil || got.Before.Control != nil {
				t.Fatal("actual finish lost healthy pre-cleanup accounting beside supplied stop failure", got)
			}
			if run.err == nil || !run.result.RootJoined || !run.result.SessionEmpty || custody.borrowedBy != nil || !custody.active || custody.Close() == nil {
				t.Fatal("failed stop weakened sticky failure or author survivor custody", run.result, run.err)
			}
			custody.active = false
			if custody.Close() != nil {
				t.Fatal("joined author fixture cannot release")
			}
			return
		}
		// No actual B author or semantic body is manufactured in this control
		// fixture. It checks the precise post-author owner/request operations.
		if control.ReopenOwners(ctx) != nil {
			t.Fatal("physical reopen failed")
		}
		epochHandoffByte(t, input, 'B')
		epochHandoffRead(t, output, 'b')
		if control.DrainOwners(ctx) != nil || control.OpenRequests(ctx) != nil {
			t.Fatal("physical final window failed")
		}
		epochHandoffByte(t, input, 'D')
		epochHandoffRead(t, output, 'd')
		if control.FenceRequests(ctx) != nil {
			t.Fatal("physical final fence failed")
		}
		epochHandoffByte(t, input, 'Z')
		epochHandoffRead(t, output, 'z')
	}
	// Resume kept the owners fenced: a second DrainOwners would be invalid.
	if control.Pause(ctx) != nil || parent.Pause(ctx) != nil || dispatch.Fence() != nil {
		t.Fatal("quiet resumed-phase stop failed")
	}
	if control.ReservedWireBytes() != (pairs-1)*2*dispatchadmission.FrameBytes {
		t.Fatal("handoff added a PC01 operation")
	}
	epochHandoffByte(t, input, 'C')
	err = handle.Wait()
	joined = true
	if err != nil || transport.Wait(ctx, serverID) != nil || control.Close() != nil || parent.Close(ctx) != nil {
		t.Fatalf("fixture native/protocol join failed: %v", err)
	}
	// Eleven warm exchanges plus the receiver's terminal EOF reservation fit
	// exactly twelve pairs; the unchanged healthy path uses seven plus one.
	if control.ReservedWireBytes()+2*dispatchadmission.FrameBytes != pcConfig.MaximumWireBytes {
		t.Fatal("terminal EOF did not retain its exact PC01 pair budget")
	}
	err = <-served
	serverJoined = true
	if err != nil {
		t.Fatalf("fixture dispatch receiver failed: %v", err)
	}
	sa, err = transport.Snapshot()
	wantPhase := resumedPhase
	if physical {
		wantPhase = 4
	}
	if err != nil || sa.Store.Phase != wantPhase || sa.Opened != 1 || sa.TerminalEOF != 1 || !physical && !stale && sa.Complete {
		t.Fatalf("early closed prefix invented phase-four completion: %+v / %v", sa, err)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
}

// The inherited endpoints and owner/request entry are real; HTTP bodies,
// authority digests and read reports are explicit source-free test models.
// This proves ObserveWarm choreography, not a production-image/native pass.
func testEpochInheritedWarmObservation(t *testing.T, ctx context.Context, run *ExecutionEpochOneRun, input io.Writer, output io.Reader, mode string) {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	inspection, final := epochTestFinal(t)
	finalBytes := epochTestJSON(t, final, true)
	cold, _, err := inspection.decodeFinal(finalBytes)
	if err != nil {
		t.Fatal(err)
	}
	inspection.cold, inspection.finalUsed, inspection.next = cold, true, 4
	inspection.run = run
	tail := inspection.tail
	tail.Schema, tail.SelectedRuntimeSHA256 = "t421-tail-readiness-source-free-v1", testDigest("warm-selected-runtime")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		count := requests.Add(1)
		ordinal, err := strconv.ParseUint(request.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
		if err != nil || ordinal != uint64(count+3) || request.Header.Get("Authorization") != "Bearer private-key" ||
			request.Header.Get(dispatchadmission.ProductionRequestHeader) != run.control.RequestToken() {
			t.Error("warm request lost ordinal/auth/control binding")
		}
		report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: ordinal}
		var body []byte
		switch request.URL.Path {
		case api.ExtractionProgressPath:
			epochHandoffByte(t, input, 'O')
			epochHandoffRead(t, output, 'o')
			if mode == "warm_canceled_read" {
				cancel()
				<-request.Context().Done()
				return
			}
			total, domains := int(inspection.plan.Profile.Physical.CombinedModeledPartitions), len(inspection.plan.Profile.Pipeline.ExtractionDomains)
			value := struct {
				Schema string `json:"$schema"`
				extractionpublication.Progress
			}{Schema: "http://" + run.epoch.Listen + "/schemas/ExtractionProgress.json",
				Progress: extractionpublication.Progress{State: "current", Total: total, Materialized: total, Succeeded: total, Domains: domains, CurrentDomains: domains}}
			if mode == "warm_pending_x" {
				value.Progress = extractionpublication.Progress{State: "unavailable"}
			}
			body = epochTestJSON(t, value, false)
			report.ControlFileReads, report.StoreReadAttempts = 2+uint64(domains), 4
		case "/api/t421/tail-readiness":
			value := tail
			if mode == "warm_pending_t" {
				value = epochTailReadiness{Schema: tail.Schema, Status: "pending"}
			}
			body = epochTestJSON(t, value, false)
			report.ControlFileReads, report.StoreReadAttempts = 4, 4
		case "/api/t421/final-authority":
			epochHandoffByte(t, input, 'Q')
			epochHandoffRead(t, output, 'q')
			body = finalBytes
			report.ControlFileReads, report.StoreReadAttempts, report.MemberVisits = correctedFinalAuthorityControlReadMaximum, correctedFinalAuthorityStoreReadMaximum, correctedFinalAuthorityMemberReadMaximum
		default:
			t.Error("unexpected warm route")
		}
		w.Header().Set("Trailer", epochReadTrailer)
		if _, err := w.Write(body); err != nil {
			t.Error(err)
		}
		w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
	}))
	defer server.Close()
	run.epoch = ExecutionEpochConfig{Listen: strings.TrimPrefix(server.URL, "http://"), APIKey: "private-key", Repository: "example.com/mono"}
	run.stop, run.done, run.coldDone = make(chan struct{}), make(chan struct{}), make(chan struct{})
	close(run.coldDone)
	run.inspection, run.warm, run.warmAllowed = inspection, true, true
	run.phaseDeadline = time.Now().Add(10 * time.Second)
	err = run.ObserveWarm(ctx)
	if mode != "warm" {
		want := int32(1)
		if mode == "warm_pending_t" {
			want = 2
		}
		if err == nil || run.err != ErrExecutionEpochOne || !run.warmUsed || inspection.finalUsed || requests.Load() != want {
			t.Fatal("pending/canceled warm read continued or lost its sticky prefix", err, requests.Load())
		}
		select {
		case <-run.warmDone:
		default:
			t.Fatal("failed warm operation not joined")
		}
		if run.ObserveWarm(t.Context()) == nil || requests.Load() != want {
			t.Fatal("failed warm observation retried")
		}
		return
	}
	if err != nil {
		t.Fatalf("actual inherited-control warm observation: %v", err)
	}
	if requests.Load() != 3 || inspection.reports != 3 || inspection.next != 7 || !run.warmUsed || run.control.RequestToken() != "" {
		t.Fatal("warm observation lost one-shot X/T/F or final request fence")
	}
	select {
	case <-run.warmDone:
	default:
		t.Fatal("warm observation returned before its operation joined")
	}
}
