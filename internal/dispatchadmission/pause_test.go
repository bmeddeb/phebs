//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

type pauseStartResult struct {
	handle Handle
	err    error
}

func awaitPauseWaiters(t *testing.T, client *Client, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		client.mu.Lock()
		got := client.waiters
		client.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("paused callers = %d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPauseParksBeforeOrdinalAndResumeReopens(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	if err := client.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/usr/bin/true")
	started := make(chan pauseStartResult, 1)
	go func() {
		handle, err := client.Start(t.Context(), 1, command)
		started <- pauseStartResult{handle, err}
	}()
	awaitPauseWaiters(t, client, 1)
	snapshot, err := controller.Snapshot()
	if err != nil || snapshot.Attempts != 0 || snapshot.Producers[0].Ordinal != 0 || snapshot.Producers[0].Active != 0 || command.Process != nil {
		t.Fatalf("paused caller consumed admission: %+v / %v", snapshot, err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := client.Checkpoint(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := client.Resume(2); err != nil {
		t.Fatal(err)
	}
	result := <-started
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := result.handle.Wait(); err != nil {
		t.Fatal(err)
	}
	awaitPauseWaiters(t, client, 0)
	snapshot = finishPair(t, controller, client, server)
	if snapshot.Phases[0].Attempts != 0 || snapshot.Phases[1].Attempts != 1 {
		t.Fatalf("wrong phase admission: %+v", snapshot)
	}
}

func TestPauseCanceledUnadmittedCallerDoesNotLatch(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	if err := client.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	command := exec.CommandContext(ctx, "/usr/bin/true")
	started := make(chan error, 1)
	go func() { _, err := client.Start(ctx, 1, command); started <- err }()
	awaitPauseWaiters(t, client, 1)
	cancel()
	if err := <-started; !errors.Is(err, context.Canceled) {
		t.Fatalf("parked cancellation: %v", err)
	}
	awaitPauseWaiters(t, client, 0)
	if client.Context().Err() != nil || command.Process != nil {
		t.Fatal("unadmitted cancellation failed producer or started command")
	}
	if got := finishPair(t, controller, client, server).Attempts; got != 0 {
		t.Fatalf("canceled admission counted: %d", got)
	}
}

func TestPauseWaiterBoundIsSeparateFromActiveTokens(t *testing.T) {
	config := testConfig()
	config.Limits.ActivePerProducer = 1
	controller, client, _ := paired(t, config)
	if err := client.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() { _, err := client.Start(t.Context(), 1, exec.Command("/usr/bin/true")); first <- err }()
	awaitPauseWaiters(t, client, 1)
	// Change notifications must not temporarily release this reservation.
	client.mu.Lock()
	client.notifyLocked()
	client.mu.Unlock()
	command := exec.Command("/usr/bin/true")
	if _, err := client.Start(t.Context(), 1, command); !errors.Is(err, ErrLimit) {
		t.Fatalf("waiter overflow: %v", err)
	}
	if err := <-first; !errors.Is(err, ErrLimit) {
		t.Fatalf("overflow was not sticky for parked caller: %v", err)
	}
	awaitPauseWaiters(t, client, 0)
	snapshot, _ := controller.Snapshot()
	if snapshot.Attempts != 0 || snapshot.Producers[0].Active != 0 || command.Process != nil {
		t.Fatalf("waiter reservation became a token: %+v", snapshot)
	}
}

func TestPauseWaitsForRequestGate(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	// Hold the actual request gate to model an RPC awaiting its ACK. Pause
	// must close the local admission gate before waiting for this owner.
	release, err := client.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	paused := make(chan error, 1)
	go func() { paused <- client.Pause(t.Context()) }()
	select {
	case err := <-paused:
		release()
		t.Fatalf("pause crossed an owned request gate: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	finishPair(t, controller, client, server)
}

func TestPauseTerminalCloseWakesUnadmittedCaller(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	if err := client.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/usr/bin/true")
	started := make(chan error, 1)
	go func() { _, err := client.Start(t.Context(), 1, command); started <- err }()
	awaitPauseWaiters(t, client, 1)
	if got := finishPair(t, controller, client, server).Attempts; got != 0 {
		t.Fatalf("terminal close admitted a parked caller: %d", got)
	}
	if err := <-started; err == nil {
		t.Fatal("terminal close resumed a caller")
	}
	awaitPauseWaiters(t, client, 0)
	client.mu.Lock()
	err := client.err
	client.mu.Unlock()
	if err != nil || command.Process != nil {
		t.Fatalf("terminal cancellation changed successful closure: %v", err)
	}
}

func TestPauseCancellationCauseRemainsSourceFree(t *testing.T) {
	_, client, _ := paired(t, testConfig())
	if err := client.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/usr/bin/true")
	started := make(chan error, 1)
	go func() { _, err := client.Start(t.Context(), 1, command); started <- err }()
	awaitPauseWaiters(t, client, 1)
	// A parent's WithCancelCause can carry private diagnostics even though the
	// producer has not latched a closed admission failure class of its own.
	client.cancel(errors.New("/private/unreturned-cancellation-cause"))
	if err := <-started; err != context.Canceled {
		t.Fatalf("private cancellation cause escaped: %v", err)
	}
	awaitPauseWaiters(t, client, 0)
	if command.Process != nil {
		t.Fatal("canceled producer started a parked command")
	}
}

func TestPausePendingStartStillBlocksCheckpoint(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	command := exec.Command("/usr/bin/true")
	ordinal, err := client.admit(t.Context(), 1, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	checkpoint := make(chan error, 1)
	go func() { checkpoint <- client.Checkpoint(t.Context()) }()
	select {
	case err := <-checkpoint:
		t.Fatalf("checkpoint crossed ACK-to-Start gap: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := controller.Advance(); !errors.Is(err, ErrBusy) {
		t.Fatalf("advance crossed pending handle: %v", err)
	}
	if err := client.settle(ordinal); err != nil {
		t.Fatal(err)
	}
	if err := <-checkpoint; err != nil {
		t.Fatal(err)
	}
	if command.Process != nil {
		t.Fatal("test's canceled pending permission started")
	}
	if got := finishPair(t, controller, client, server).Attempts; got != 1 {
		t.Fatalf("positive admission was lost: %d", got)
	}
}

func TestPausePersistentHandleCarriesAndStillSettles(t *testing.T) {
	controller, client, server := paired(t, testConfig())
	command := exec.Command("/bin/cat")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	handle, err := client.Start(t.Context(), 2, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	if err := client.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := client.Checkpoint(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	if err := handle.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := finishPair(t, controller, client, server).Attempts; got != 1 {
		t.Fatalf("persistent handle recounted: %d", got)
	}
}

func TestPauseDoesNotClaimNestedOwnerReadiness(t *testing.T) {
	controller, client, _ := paired(t, testConfig())
	command := exec.Command("/bin/cat")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	handle, err := client.Start(t.Context(), 1, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	// An owner that cannot finish before spawning another paused child cannot
	// claim a completed checkpoint. Its existing command remains owned.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := client.Checkpoint(ctx); !errors.Is(err, ErrCanceled) {
		t.Fatalf("unfinished owner checkpoint: %v", err)
	}
	_ = input.Close()
	if err := handle.Wait(); err == nil || command.ProcessState == nil {
		t.Fatalf("failed accounting hid native join: %v", err)
	}
	snapshot, _ := controller.Snapshot()
	if snapshot.Attempts != 1 || snapshot.Complete {
		t.Fatalf("unfinished owner falsified prefix: %+v", snapshot)
	}
}
