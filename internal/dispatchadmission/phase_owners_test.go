//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestOwnerRequestTokenAndSlotAreOneAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	owners, err := NewOwners(ctx, OwnerLimits{Owners: 1, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	close(ready)
	client := &Client{ctx: ctx, binding: [32]byte{1}, phase: 1, ownersRequired: true, owners: owners,
		ownersReady: ready, ownerRequestsOpen: true}
	token := ownerRequestToken(client.binding, 1, 0)
	if turn, err := client.enterOwnerRequest(ctx, owners, "invalid"); err == nil || turn.owners != nil || owners.requests != 0 {
		t.Fatal("invalid token consumed an owner slot")
	}
	type admitted struct {
		turn OwnerTurn
		err  error
	}
	entry := make(chan admitted, 1)
	owners.mu.Lock()
	go func() {
		turn, err := client.enterOwnerRequest(ctx, owners, token)
		entry <- admitted{turn, err}
	}()
	// Hold the actual reservation mutex until entry holds the token mutex.
	// This schedules the exact former check/reservation gap without time sleeps.
	for client.mu.TryLock() {
		client.mu.Unlock()
		if ctx.Err() != nil {
			owners.mu.Unlock()
			t.Fatal("request did not reach atomic reservation")
		}
		runtime.Gosched()
	}
	drained := make(chan error, 1)
	go func() { drained <- client.controlOwners(ctx, phaseControlFrame{op: phaseOwnerDrain}) }()
	owners.mu.Unlock()
	result := <-entry
	if result.err != nil {
		t.Fatal(result.err)
	}
	ended := false
	defer func() {
		if !ended {
			result.turn.End()
		}
	}()
	for {
		owners.mu.Lock()
		fenced := owners.requestsFenced
		owners.mu.Unlock()
		if fenced {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("concurrent drain did not fence request admission")
		}
		runtime.Gosched()
	}
	select {
	case err := <-drained:
		t.Fatal("drained before the token-bound request tail", err)
	default:
	}
	result.turn.End()
	ended = true
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	if err := client.controlOwners(ctx, phaseControlFrame{op: phaseRequestsOpen, sequence: 1}); err != nil {
		t.Fatal(err)
	}
	if turn, err := client.enterOwnerRequest(ctx, owners, token); err == nil || turn.owners != nil {
		t.Fatal("old token entered the later observation window")
	}
	turn, err := client.enterOwnerRequest(ctx, owners, ownerRequestToken(client.binding, 1, 1))
	if err != nil {
		t.Fatal("current observation token was refused", err)
	}
	turn.End()
	if err := client.controlOwners(ctx, phaseControlFrame{op: phaseRequestsFence}); err != nil {
		t.Fatal(err)
	}
}

func phaseOwnersPair(t *testing.T, client *Client, timeout time.Duration) (*PhaseControl, <-chan error) {
	t.Helper()
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	config := phaseTestConfig()
	config.OwnerControl, config.Timeout = true, timeout
	control, err := NewPhaseControl(t.Context(), parent, client.binding, config)
	if err != nil {
		_ = child.Close()
		t.Fatal(err)
	}
	done, err := StartPhaseControl(t.Context(), child, client, config)
	if err != nil {
		_ = control.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	return control, done
}

func TestOwnerControlCompleteTailsAndPreparedPhase(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	controller, client, server := paired(t, testConfig())
	control, done := phaseOwnersPair(t, client, time.Second)
	owners, err := NewOwners(ctx, OwnerLimits{Owners: 2, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	startup, err := owners.Enter(ctx)
	if err != nil || client.bindOwners(owners) != nil {
		t.Fatal("startup owner registration failed")
	}
	initialToken := control.RequestToken()
	if initialToken == "" || !client.ownerRequestAllowed(initialToken) || client.ownerRequestAllowed("") || client.ownerRequestAllowed(initialToken[:63]+"z") {
		t.Fatal("initial request binding is not closed")
	}
	request, err := owners.EnterRequest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan error, 1)
	go func() { drained <- control.DrainOwners(ctx) }()
	// Owner drainage cannot bypass a full request tail or an unfinished startup.
	for _, held := range []string{"request", "startup"} {
		select {
		case err := <-drained:
			t.Fatalf("drained before %s tail: %v", held, err)
		case <-time.After(10 * time.Millisecond):
		}
		if held == "request" {
			request.End()
		}
	}
	// A second child inside the already-admitted owner must remain possible.
	for range 2 {
		if err := client.Run(ctx, 1, exec.CommandContext(ctx, "/usr/bin/true")); err != nil {
			t.Fatal("owner was fenced before its successive command joined", err)
		}
	}
	startup.End()
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	if client.ownerRequestAllowed(initialToken) || control.RequestToken() != "" {
		t.Fatal("drained request window remained authorized")
	}
	if err := control.OpenRequests(ctx); err != nil {
		t.Fatal(err)
	}
	probeToken := control.RequestToken()
	if probeToken == initialToken || !client.ownerRequestAllowed(probeToken) || client.ownerRequestAllowed(initialToken) {
		t.Fatal("old request token survived probe-window rotation")
	}
	probe, err := owners.EnterRequest(ctx)
	if err != nil || client.Run(ctx, 1, exec.CommandContext(ctx, "/usr/bin/true")) != nil {
		t.Fatal("exact observation could not dispatch its owned child")
	}
	probe.End()
	if err := control.FenceRequests(ctx); err != nil {
		t.Fatal(err)
	}
	if err := control.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := control.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := control.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	parkedCtx, stopParked := context.WithTimeout(ctx, 20*time.Millisecond)
	if turn, err := owners.Enter(parkedCtx); !errors.Is(err, context.DeadlineExceeded) || turn.owners != nil {
		t.Fatal("dispatch Resume reopened ordinary work before preparation")
	}
	stopParked()
	if err := control.OpenRequests(ctx); err != nil {
		t.Fatal(err)
	}
	prepareToken := control.RequestToken()
	if prepareToken == probeToken || !client.ownerRequestAllowed(prepareToken) || client.ownerRequestAllowed(probeToken) {
		t.Fatal("prior-phase token admitted preparation")
	}
	if err := client.Run(ctx, 1, exec.CommandContext(ctx, "/usr/bin/true")); err != nil {
		t.Fatal("new-phase preparation dispatch was fenced", err)
	}
	if err := control.FenceRequests(ctx); err != nil {
		t.Fatal(err)
	}
	if err := control.ReopenOwners(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err := owners.Enter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	turn.End()
	if control.RequestToken() == prepareToken || client.ownerRequestAllowed(prepareToken) || !client.ownerRequestAllowed(control.RequestToken()) {
		t.Fatal("preparation token survived owner reopening")
	}
	if err := control.DrainOwners(ctx); err != nil {
		t.Fatal(err)
	}
	if err := control.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := finishPair(t, controller, client, server)
	if snapshot.Attempts != 4 || phaseTestResult(t, done) != nil {
		t.Fatal("owner control lost its joined accounting prefix")
	}
}

func TestOwnerControlRefusesMissingRegistrationAndHeldTail(t *testing.T) {
	for _, mode := range []string{"unregistered", "held-owner", "held-request"} {
		t.Run(mode, func(t *testing.T) {
			controller, client, server := paired(t, testConfig())
			control, done := phaseOwnersPair(t, client, 50*time.Millisecond)
			if mode != "unregistered" {
				owners, err := NewOwners(t.Context(), OwnerLimits{Owners: 1, Requests: 1})
				if err != nil || client.bindOwners(owners) != nil {
					t.Fatal("registration failed")
				}
				var turn OwnerTurn
				if mode == "held-owner" {
					turn, err = owners.Enter(t.Context())
				} else {
					turn, err = owners.EnterRequest(t.Context())
				}
				if err != nil {
					t.Fatal(err)
				}
				defer turn.End()
			}
			if err := control.DrainOwners(t.Context()); err == nil {
				t.Fatal("unproven owner drain succeeded")
			}
			if phaseTestResult(t, done) == nil || phaseTestResult(t, server) == nil {
				t.Fatal("owner refusal lost terminal failure")
			}
			if snapshot, err := controller.Snapshot(); err == nil || snapshot.Complete || snapshot.Attempts != 0 {
				t.Fatal("owner refusal invented a complete admission")
			}
		})
	}
}

func TestOwnerControlClosedOperationOrdering(t *testing.T) {
	config := phaseTestConfig()
	config.OwnerControl = true
	for _, test := range []struct {
		state, op byte
	}{
		{0, phasePause}, {0, phaseRequestsOpen}, {phaseOwnerDrain, phaseCheckpoint},
		{phaseRequestsOpen, phasePause}, {phaseCheckpoint, phaseRequestsOpen},
		{phaseCheckpoint, phaseOwnersReopen}, {phaseResume, phasePause},
		{phasePreparingRequests, phaseOwnersReopen},
	} {
		if _, _, err := nextConfiguredControlState(test.state, 0, test.op, config); err == nil {
			t.Fatalf("invalid owner sequence state=%d op=%d", test.state, test.op)
		}
	}
	config.OwnerControl = false
	for _, op := range []byte{phaseOwnerDrain, phaseRequestsOpen, phaseRequestsFence, phaseOwnersReopen} {
		if _, _, err := nextConfiguredControlState(0, 0, op, config); err == nil {
			t.Fatal("mechanical-only control accepted owner operation")
		}
	}
	record := productionTestRecord()
	record.Program, record.Producer.Sites, record.Tools = ProgramCorpusAuthor, AuthorSites(), record.Tools[:1]
	record.InputSHA256, record.Control.OwnerControl = [32]byte{1}, true
	if record.validate() == nil {
		t.Fatal("author program accepted Phebs owner control")
	}
}
