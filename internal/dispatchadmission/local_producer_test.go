//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestLocalProducerActualStartWaitAndPhase(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	controller, err := New(ctx, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	view, err := controller.ProducerLaunch(1)
	if err != nil || view.Phase != 1 || view.Producer.Binding != ([32]byte{1}) {
		t.Fatal("missing configured view", err)
	}
	view.Producer.Sites[0].Role = 99
	parent, err := controller.NewLocalProducer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close(context.Background()) })
	if !parent.OnController(controller) || parent.OnController(&Controller{}) {
		t.Fatal("local owner lost actual controller identity")
	}
	count, err := parent.Count()
	if err != nil || !count.Attached || count.Ordinal != 0 || count.Closed {
		t.Fatal("constructor returned before fresh attachment", err)
	}
	if _, err := controller.ProducerLaunch(1); err == nil {
		t.Fatal("attached producer returned fresh launch authority")
	}
	wrongSite := exec.Command("/usr/bin/true")
	if _, err := parent.StartInPhase(ctx, 1, Site{ID: 1, Role: 99}, wrongSite); err == nil || wrongSite.Process != nil {
		t.Fatal("wrong configured role started")
	}
	if _, err := parent.Start(ctx, 1, exec.Command("/dispatch-admission-no-such-executable")); err == nil {
		t.Fatal("missing image started")
	}
	command := exec.Command("/usr/bin/true")
	handle, err := parent.StartInPhase(ctx, 1, Site{ID: 1, Role: 1}, command)
	if err != nil {
		t.Fatal(err)
	}
	count, err = parent.Count()
	if err != nil || count.Ordinal != 2 || count.Active != 1 {
		t.Fatal("root Start did not commit actual active token", err)
	}
	if err := handle.Wait(); err != nil || command.ProcessState == nil {
		t.Fatal("actual root handle not joined", err)
	}
	if parent.Pause(ctx) != nil || controller.Fence() != nil || parent.Checkpoint(ctx) != nil || controller.Advance() != nil || parent.Resume(2) != nil {
		t.Fatal("existing phase choreography failed")
	}
	handle, err = parent.StartInPhase(ctx, 2, Site{ID: 1, Role: 1}, exec.Command("/usr/bin/true"))
	if err != nil || handle.Wait() != nil {
		t.Fatal("second actual phase failed", err)
	}
	if err := parent.Close(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := controller.Snapshot()
	if err != nil || snapshot.Complete || snapshot.Attempts != 3 || snapshot.Producers[0].Active != 0 || !snapshot.Producers[0].Closed {
		t.Fatal("local close invented global fence or lost root attempts", err)
	}
	if controller.Fence() != nil {
		t.Fatal("global fence failed")
	}
	if snapshot, err := controller.Snapshot(); err != nil || !snapshot.Complete {
		t.Fatal("actual final fence incomplete", err)
	}
}

func TestLocalProducerRefusesWrongPhaseAndCanceledSetup(t *testing.T) {
	for _, mode := range []string{"wrong-phase", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			controller, err := New(ctx, testConfig())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "canceled" {
				cancel()
			}
			parent, err := controller.NewLocalProducer(ctx, 1)
			if mode == "canceled" {
				if err == nil || parent != nil {
					t.Fatal("canceled setup returned local owner")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Close(context.Background()) }()
			command := exec.Command("/usr/bin/true")
			if _, err := parent.StartInPhase(ctx, 2, Site{ID: 1, Role: 1}, command); err == nil || command.Process != nil {
				t.Fatal("wrong phase launched")
			}
			count, _ := parent.Count()
			if count.Ordinal != 0 || count.Active != 0 {
				t.Fatal("wrong phase consumed an ordinal")
			}
		})
	}
}

func TestLocalProducerConcurrentDuplicateAttachment(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	controller, err := New(ctx, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	owners := make(chan *LocalProducer, 2)
	for range 2 {
		wg.Go(func() { parent, _ := controller.NewLocalProducer(ctx, 1); owners <- parent })
	}
	wg.Wait()
	close(owners)
	attached := 0
	for owner := range owners {
		if owner != nil {
			attached++
			_ = owner.Close(context.Background())
		}
	}
	if attached != 1 {
		t.Fatalf("duplicate attachment returned %d local owners", attached)
	}
}

func TestLocalProducerRetainedOwnersCloseDescriptorsWithoutGC(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var retained []*LocalProducer
	createAndClose := func() {
		t.Helper()
		controller, err := New(ctx, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		owner, err := controller.NewLocalProducer(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		retained = append(retained, owner)
		if err := owner.Close(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case <-owner.done:
		default:
			t.Fatal("Close returned before receiver descriptor cleanup")
		}
		if _, err := owner.client.conn.Write([]byte{1}); err == nil {
			t.Fatal("closed retained client socket still writable")
		}
	}
	createAndClose() // Initialize the runtime network poller before the baseline.
	countFDs := func() int {
		t.Helper()
		directory, err := os.Open("/dev/fd")
		if err != nil {
			t.Fatal(err)
		}
		// Names only: Darwin's synthetic directory cannot reliably fstatat
		// each numeric entry, particularly the enumeration FD after closure.
		names, readErr := directory.Readdirnames(4097)
		closeErr := directory.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(names) > 4096 {
			t.Fatal("bounded native descriptor inventory unavailable", readErr, closeErr)
		}
		return len(names)
	}
	before := countFDs()
	for range 32 {
		createAndClose()
	}
	after := countFDs()
	if after != before {
		t.Fatalf("retained local owners leaked descriptors: before %d after %d", before, after)
	}
	runtime.KeepAlive(retained) // No GC/finalizer can close these reachable owners.
}

func TestLocalProducerCanceledCloseKeepsUnjoinedPrefix(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	controller, err := New(ctx, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	owner, err := controller.NewLocalProducer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "/usr/bin/true")
	handle, err := owner.Start(ctx, 1, command)
	if err != nil {
		t.Fatal(err)
	}
	closing, stop := context.WithCancel(ctx)
	stop()
	if err := owner.Close(closing); err == nil {
		t.Fatal("canceled Close joined an unjoined native handle")
	}
	if count, _ := owner.Count(); count.Ordinal != 1 || count.Active != 1 || count.Closed {
		t.Fatal("canceled Close erased incomplete committed root prefix")
	}
	if err := handle.Wait(); err == nil || command.ProcessState == nil {
		t.Fatal("terminal failure hid the subsequent actual native Wait", err)
	}
}
