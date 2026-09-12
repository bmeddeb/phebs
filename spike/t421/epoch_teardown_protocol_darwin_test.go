//go:build darwin

package t421

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// This child only speaks the existing DA/SA clients. It is not Phebs, an SDK
// query, an author, an archive command, a PC receiver, or hdiutil. The parent
// uses the real frozen configuration and counts actual helper Start/Waits.
type teardownProtocolInput struct {
	Producer dispatchadmission.Producer
	Limits   dispatchadmission.Limits
	Phase    uint32
	Store    *storeaccounting.ClientConfig
	Deadline time.Time
}

func TestExecutionTeardownProtocolHelper(t *testing.T) {
	mode := os.Getenv("PHEBS_TEARDOWN_PROTOCOL_HELPER")
	if mode == "" {
		return
	}
	if mode == "detach" {
		return
	}
	if mode == "detach-fail" {
		t.Fatal("intentional admitted helper failure")
	}
	if mode != "peer" {
		t.Fatal("unknown helper mode")
	}
	raw, err := base64.StdEncoding.DecodeString(os.Getenv("PHEBS_TEARDOWN_PROTOCOL_INPUT"))
	var input teardownProtocolInput
	if err != nil || len(raw) > 8192 || json.Unmarshal(raw, &input) != nil ||
		input.Producer.ID < 2 || input.Producer.ID > 11 || input.Phase < 1 || input.Phase > 14 ||
		!input.Deadline.After(time.Now()) || time.Until(input.Deadline) > time.Minute {
		t.Fatal("invalid private helper input")
	}
	var stat unix.Stat_t
	if unix.Fstat(3, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFSOCK {
		t.Fatal("no inherited DA socket")
	}
	if input.Store != nil && (unix.Fstat(4, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFSOCK) {
		t.Fatal("no inherited SA socket")
	}
	ctx, cancel := context.WithDeadline(t.Context(), input.Deadline)
	defer cancel()
	client, err := dispatchadmission.NewClient(ctx, os.NewFile(3, "teardown-da"), input.Producer, input.Phase, input.Limits)
	if err != nil {
		t.Fatal("DA adoption refused")
	}
	var store *storeaccounting.SDKOwner
	if input.Store != nil {
		storeClient, err := storeaccounting.NewClient(ctx, os.NewFile(4, "teardown-sa"), *input.Store)
		if err != nil {
			t.Fatal("SA adoption refused")
		}
		store, err = storeaccounting.NewSDKOwner(storeClient)
		if err != nil {
			t.Fatal("actual SDK owner claim refused")
		}
	}
	epochHandoffByte(t, os.Stdout, 'R')
	for {
		var command [1]byte
		if _, err := io.ReadFull(os.Stdin, command[:]); err != nil {
			t.Fatal("control EOF")
		}
		switch command[0] {
		case 'C':
			if store != nil && store.Checkpoint(ctx) != nil {
				t.Fatal("SA checkpoint refused")
			}
			if client.Pause(ctx) != nil || client.Checkpoint(ctx) != nil {
				t.Fatal("DA checkpoint refused")
			}
		case 'R':
			var phase [1]byte
			if _, err := io.ReadFull(os.Stdin, phase[:]); err != nil {
				t.Fatal("resume EOF")
			}
			if store != nil && store.Resume(uint32(phase[0])) != nil {
				t.Fatal("SA resume refused")
			}
			if client.Resume(uint32(phase[0])) != nil {
				t.Fatal("DA resume refused")
			}
		case 'E':
			if store != nil && store.Close(ctx) != nil {
				t.Fatal("SA close refused")
			}
			if client.Close(ctx) != nil {
				t.Fatal("DA close refused")
			}
			epochHandoffByte(t, os.Stdout, 'E')
			return
		default:
			t.Fatal("unknown closed control")
		}
		epochHandoffByte(t, os.Stdout, command[0])
	}
}

type teardownProtocolPeer struct {
	id           uint32
	command      *exec.Cmd
	handle       dispatchadmission.Handle
	input        io.WriteCloser
	output       io.ReadCloser
	served       <-chan error
	store        bool
	joined       bool
	servedJoined bool
}

func (p *teardownProtocolPeer) control(t *testing.T, command byte, phase uint32) {
	t.Helper()
	raw := []byte{command}
	if command == 'R' {
		raw = append(raw, byte(phase))
	}
	if _, err := p.input.Write(raw); err != nil {
		t.Fatal("private peer control write", err)
	}
	epochHandoffRead(t, p.output, command)
}

// The only modeled fact is what executable implements each closed source role:
// every role launches this test binary, never the named production tool. All
// ordinals, receiver EOFs, hard-death retirement and native session joins below
// arise from actual sockets and processes. No mounted custody or byte result is
// supplied, and success here is not finishRestored/receipt acceptance.
func TestExecutionTeardownProtocol(t *testing.T) {
	for _, mode := range []string{"complete", "live_last_sdk", "canceled_detach", "detach_failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			var plan Plan
			for _, candidate := range lifecyclePolicyPlans(t) {
				if candidate.Schema == PlanV3Schema {
					plan = candidate
				}
			}
			var bindings [executionProducerCount][32]byte
			for i := range bindings {
				bindings[i][0] = byte(i + 1)
			}
			daConfig, err := executionDispatchConfig(plan, bindings)
			if err != nil {
				t.Fatal(err)
			}
			saConfig, wire, err := executionStoreConfig(plan, bindings)
			if err != nil {
				t.Fatal(err)
			}
			da, err := dispatchadmission.New(ctx, daConfig)
			if err != nil {
				t.Fatal(err)
			}
			parent, err := da.NewLocalProducer(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Close(ctx) }()
			sa, err := storeaccounting.New(ctx, saConfig)
			if err != nil {
				t.Fatal(err)
			}
			transport, err := storeaccounting.NewTransport(ctx, sa, wire)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = transport.Close() }()
			var peers []*teardownProtocolPeer
			defer func() {
				for _, peer := range peers {
					_ = peer.input.Close()
					if !peer.joined {
						_ = peer.command.Process.Kill()
						_ = peer.handle.Wait()
						peer.joined = true
					}
					if !peer.servedJoined {
						<-peer.served
						peer.servedJoined = true
					}
					_ = peer.output.Close()
				}
			}()
			deadline, _ := ctx.Deadline()
			launch := func(id, phase, siteID uint32) *teardownProtocolPeer {
				t.Helper()
				remote, child, err := dispatchadmission.NewPipe()
				if err != nil {
					t.Fatal(err)
				}
				input := teardownProtocolInput{Producer: daConfig.Producers[id-1], Limits: daConfig.Limits, Phase: phase, Deadline: deadline}
				files := []*os.File{child}
				t.Cleanup(func() {
					_ = remote.Close()
					for _, file := range files {
						_ = file.Close()
					}
				})
				if id < 7 || id > 9 {
					file, config, err := transport.Open(id)
					if err != nil {
						t.Fatal(err)
					}
					input.Store = &config
					files = append(files, file)
				}
				raw, err := json.Marshal(input)
				if err != nil || len(raw) > 8192 {
					t.Fatal("helper input bound")
				}
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutionTeardownProtocolHelper$")
				command.Env = []string{"PHEBS_TEARDOWN_PROTOCOL_HELPER=peer", "PHEBS_TEARDOWN_PROTOCOL_INPUT=" + base64.StdEncoding.EncodeToString(raw), "GORACE=atexit_sleep_ms=0"}
				command.ExtraFiles, command.Stderr, command.WaitDelay = files, os.Stderr, time.Second
				prepareProductionSession(command)
				stdin, err := command.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stdin.Close() })
				stdout, err := command.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stdout.Close() })
				var site dispatchadmission.Site
				for _, candidate := range executionRootSites() {
					if candidate.ID == siteID {
						site = candidate
					}
				}
				handle, err := parent.StartInPhase(ctx, phase, site, command)
				for _, file := range files {
					_ = file.Close()
				}
				if err != nil {
					_ = remote.Close()
					_ = stdin.Close()
					_ = stdout.Close()
					t.Fatal("actual helper Start refused", id, phase, err)
				}
				served := make(chan error, 1)
				go func() { served <- da.Serve(ctx, id, command.Process.Pid, remote) }()
				peer := &teardownProtocolPeer{id: id, command: command, handle: handle, input: stdin, output: stdout, served: served, store: input.Store != nil}
				peers = append(peers, peer)
				epochHandoffRead(t, stdout, 'R')
				return peer
			}
			closePeer := func(peer *teardownProtocolPeer) {
				t.Helper()
				peer.control(t, 'E', 0)
				waitErr := peer.handle.Wait()
				peer.joined = true
				serveErr := <-peer.served
				peer.servedJoined = true
				var storeErr error
				if peer.store {
					storeErr = transport.Wait(ctx, peer.id)
				}
				if waitErr != nil || serveErr != nil || storeErr != nil ||
					t4013.WaitPrivateProcessSession(peer.command.Process.Pid, deadline) != nil {
					t.Fatal("actual helper closure refused", peer.id)
				}
			}
			var live *teardownProtocolPeer
			advance := func(next uint32) {
				t.Helper()
				if parent.Pause(ctx) != nil || da.Fence() != nil || transport.Fence() != nil {
					t.Fatal("phase fence refused")
				}
				if live != nil {
					live.control(t, 'C', 0)
				}
				if parent.Checkpoint(ctx) != nil || transport.Advance() != nil || da.Advance() != nil ||
					parent.Resume(next) != nil {
					t.Fatal("actual phase advance refused", next)
				}
				if live != nil {
					live.control(t, 'R', next)
				}
			}
			advance(2)
			closePeer(launch(7, 2, executionSiteAuthor))
			live = launch(2, 2, executionSiteServe)
			advance(3)
			advance(4)
			closePeer(launch(8, 4, executionSiteAuthor))
			closePeer(live)
			live = nil
			advance(5)
			closePeer(launch(3, 5, executionSiteServe))
			advance(6)
			closePeer(launch(9, 6, executionSiteAuthor))
			live = launch(4, 6, executionSiteServe)
			advance(7)
			advance(8)
			if parent.Pause(ctx) != nil || da.Fence() != nil || transport.Fence() != nil {
				t.Fatal("terminal fence refused")
			}
			live.control(t, 'C', 0)
			if parent.Checkpoint(ctx) != nil || transport.ArmTerminalEOF(4, 8) != nil || da.ExpectHardDeath(4) != nil {
				t.Fatal("terminal arm refused")
			}
			if err := live.command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			waitErr := live.handle.Wait()
			live.joined = true
			serveErr := <-live.served
			live.servedJoined = true
			storeErr := transport.Wait(ctx, 4)
			if waitErr == nil || serveErr != nil || storeErr != nil ||
				t4013.WaitPrivateProcessSession(live.command.Process.Pid, deadline) != nil ||
				da.CloseHardDeath(4, live.command.ProcessState) != nil {
				t.Fatal("actual terminal EOF/Wait refused")
			}
			if transport.ReopenAfterTerminalEOF(4, 5, 8) != nil || parent.ReopenAfterHardDeath(ctx, 4, 5, 8) != nil {
				t.Fatal("same-phase successor refused")
			}
			live = launch(5, 8, executionSiteServe)
			advance(9)
			advance(10)
			advance(11)
			closePeer(live)
			live = nil
			advance(12)
			closePeer(launch(10, 12, executionSiteBackup))
			closePeer(launch(11, 12, executionSiteRestore))
			live = launch(6, 12, executionSiteServe)
			advance(13)
			advance(14)
			if mode == "live_last_sdk" {
				if parent.Pause(ctx) != nil || da.Fence() != nil || transport.Fence() != nil {
					t.Fatal("last-phase fence refused")
				}
				live.control(t, 'C', 0)
				if !errors.Is(transport.Advance(), storeaccounting.ErrBusy) {
					t.Fatal("checkpointed live last SDK peer did not block phase15")
				}
				prefix, err := transport.Snapshot()
				if err != nil || prefix.Store.Phase != 14 || prefix.TerminalEOF != 6 {
					t.Fatal("nonterminal refusal changed closure", prefix, err)
				}
			}
			closePeer(live)
			live = nil
			before, err := parent.Count()
			sdk, sdkErr := transport.Snapshot()
			if err != nil || sdkErr != nil || before.Ordinal != 10 || before.Active != 0 || before.Closed ||
				sdk.Opened != 7 || sdk.TerminalEOF != 7 || sdk.Store.Phase != 14 || sdk.PrefixesClosed ||
				!sdk.Store.Producers[2].TerminalFencedEOF || sdk.Store.Producers[2].Closed {
				t.Fatal("genuine pre-detach prefix missing", before, sdk)
			}
			// This is the draft's exact parent/SDK transition, without volume I/O.
			// The last-SDK refusal already parked the parent; do not repeat Pause
			// or reopen/reset its preserved admission state after the real close.
			if mode != "live_last_sdk" && parent.Pause(ctx) != nil {
				t.Fatal("final parent pause refused")
			}
			if da.Fence() != nil || transport.Fence() != nil ||
				parent.Checkpoint(ctx) != nil || transport.Advance() != nil || da.Advance() != nil ||
				transport.Fence() != nil || parent.Resume(15) != nil {
				t.Fatal("actual phase15 transition refused")
			}
			detachCtx := ctx
			if mode == "canceled_detach" {
				canceled, stop := context.WithCancel(ctx)
				stop()
				detachCtx = canceled
			}
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutionTeardownProtocolHelper$")
			helperMode := "detach"
			if mode == "detach_failure" {
				helperMode = "detach-fail"
			}
			command.Env = []string{"PHEBS_TEARDOWN_PROTOCOL_HELPER=" + helperMode, "GORACE=atexit_sleep_ms=0"}
			command.Stdout, command.Stderr = io.Discard, io.Discard
			command.WaitDelay = time.Second
			prepareProductionSession(command)
			handle, err := parent.StartInPhase(detachCtx, 15, dispatchadmission.Site{ID: executionSiteDetach, Role: executionRoleHdiutil}, command)
			if err == nil {
				defer func() {
					if command.ProcessState == nil {
						_ = command.Process.Kill()
						_ = handle.Wait()
					}
				}()
			}
			if mode == "canceled_detach" {
				count, _ := parent.Count()
				if err == nil || command.Process != nil || count.Ordinal != 10 || count.Active != 0 || count.Closed {
					t.Fatal("canceled detach invented work", count)
				}
				var result executionTeardownResult
				flow := &ExecutionEpochOne{controller: da, store: transport}
				_ = flow.teardownAccounting(&result)
				if result.Accounting.Attempts != 10 || result.Store.Opened != 7 || result.Store.TerminalEOF != 7 || result.Store.Store.Phase != 15 {
					t.Fatal("failed admission discarded actual phase15 accounting", result)
				}
				return
			}
			if err != nil {
				t.Fatal("admitted final helper refused", err)
			}
			waitErr = handle.Wait() // Actual eleventh Start/Wait, not an hdiutil outcome.
			if (waitErr != nil) != (mode == "detach_failure") ||
				t4013.WaitPrivateProcessSession(command.Process.Pid, deadline) != nil {
				t.Fatal("final native helper result differs", waitErr)
			}
			count, countErr := parent.Count()
			if countErr != nil || count.Ordinal != 11 || count.Active != 0 || count.Closed {
				t.Fatal("final positive dispatch prefix lost", count)
			}
			if parent.Pause(ctx) != nil || da.Fence() != nil || parent.Close(ctx) != nil {
				t.Fatal("actual root receiver close refused")
			}
			dispatch, err := da.Snapshot()
			sdk, sdkErr = transport.Snapshot()
			if err != nil || sdkErr != nil || !dispatch.Complete || dispatch.Attempts != 11 ||
				sdk.Opened != 7 || sdk.TerminalEOF != 7 || sdk.Store.Phase != 15 || !sdk.PrefixesClosed ||
				sdk.Complete || transport.Close() != nil {
				t.Fatal("final actual transport closure missing", dispatch, sdk)
			}
			// Even complete accounting does not turn the failed final command into
			// teardown success; native command result remains separately non-nil.
			if mode == "complete" {
				// Real canceled-controller snapshots still return their accepted
				// prefix. No failed sample, ordinal, or completed flag is supplied.
				cancel()
				var result executionTeardownResult
				flow := &ExecutionEpochOne{controller: da, store: transport}
				primary := errors.New("prior operation failure")
				joined := errors.Join(primary, flow.teardownAccounting(&result))
				if !errors.Is(joined, primary) || result.AccountingError == nil || result.StoreError == nil ||
					result.Accounting.Attempts != 11 || result.Accounting.Complete || result.Store.Opened != 7 ||
					result.Store.TerminalEOF != 7 || result.Store.Store.Phase != 15 || result.Store.Complete || result.Store.PrefixesClosed {
					t.Fatal("error-bearing actual snapshot prefix lost", result)
				}
			}
		})
	}
}
