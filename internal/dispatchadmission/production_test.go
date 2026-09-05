//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCheckedServeRevalidatesAndBoundsResourceCheck(t *testing.T) {
	for _, mode := range []string{"fence", "check-deadline", "controller-cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			config := testConfig()
			config.Limits.AckTimeout = 100 * time.Millisecond
			controller, err := New(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			client, err := NewClient(ctx, child, config.Producers[0], 1, config.Limits)
			if err != nil {
				_ = parent.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = parent.Close(); _ = client.conn.Close() })
			entered, released := make(chan struct{}), make(chan struct{})
			server := make(chan error, 1)
			go func() {
				server <- controller.ServeChecked(ctx, 1, os.Getpid(), parent, func(checkCtx context.Context, _ Site) error {
					close(entered)
					if mode == "fence" {
						select {
						case <-released:
						case <-checkCtx.Done():
						}
					} else {
						<-checkCtx.Done()
					}
					// Even a check that returns nil after expiration cannot commit.
					return nil
				})
			}()
			command := exec.Command("/usr/bin/true")
			started := make(chan error, 1)
			go func() { _, err := client.Start(ctx, 1, command); started <- err }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("resource check not reached")
			}
			switch mode {
			case "fence":
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
				close(released)
			case "controller-cancel":
				_ = controller.fail(ErrLimit)
			}
			if err := <-started; err == nil || command.Process != nil {
				t.Fatal("refused resource check launched a command")
			}
			if err := <-server; err == nil {
				t.Fatal("refused resource check passed")
			}
			snapshot, err := controller.Snapshot()
			if err == nil || snapshot.Attempts != 0 || snapshot.Complete {
				t.Fatalf("uncommitted resource check invented admission: %+v, %v", snapshot, err)
			}
		})
	}
}

func TestProductionLifetimeCloseBoundsPendingToken(t *testing.T) {
	config := testConfig()
	config.Limits.AckTimeout = 50 * time.Millisecond
	controller, client, server := paired(t, config)
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close() }()
	done, err := StartPhaseControl(t.Context(), child, client, PhaseControlConfig{
		Phases: []uint32{1, 2}, InitialPhase: 1, MaximumPhases: 2, MaximumWireBytes: 4096, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/usr/bin/true")
	if _, err := client.admit(t.Context(), 1, command); err != nil {
		t.Fatal(err)
	}
	lifetime := &ProductionLifetime{client: client, controlDone: done}
	closed := make(chan error, 1)
	go func() { closed <- lifetime.Close(context.Background()) }()
	select {
	case err := <-closed:
		if err == nil {
			t.Fatal("pending token closed successfully")
		}
	case <-time.After(time.Second):
		t.Fatal("background close was not bounded")
	}
	if err := <-server; err == nil {
		t.Fatal("unsettled producer closed")
	}
	snapshot, err := controller.Snapshot()
	if err == nil || snapshot.Complete || snapshot.Attempts != 1 || command.Process != nil {
		t.Fatalf("pending prefix changed: %+v, %v", snapshot, err)
	}
}

func TestProductionBootstrapSendFailureClosesOnlyOwnedEndpoints(t *testing.T) {
	first, firstChild, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	second, secondChild, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	untouched, untouchedChild, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, file := range []*os.File{first, firstChild, second, secondChild, untouched, untouchedChild} {
			_ = file.Close()
		}
	})
	var absentContext context.Context
	if err := SendProductionBootstrap(absentContext, first, second, productionTestRecord()); !errors.Is(err, ErrProductionBootstrap) {
		t.Fatal("nil context accepted")
	}
	for _, file := range []*os.File{first, second} {
		if _, err := file.Stat(); err == nil {
			t.Fatal("failed bootstrap kept owned endpoint")
		}
	}
	for _, file := range []*os.File{firstChild, secondChild, untouched, untouchedChild} {
		if _, err := file.Stat(); err != nil {
			t.Fatal("failed bootstrap closed another owner's endpoint")
		}
	}
}

func TestProductionDisabledResolverAllocations(t *testing.T) {
	if productionRuntime.Load() != nil {
		t.Fatal("test requires ordinary process")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if ProductionTool("git") != "git" || ProcessContext() != context.Background() {
			panic("ordinary resolver changed")
		}
	}); allocations != 0 {
		t.Fatalf("ordinary resolver allocations: %v", allocations)
	}
}

func TestProductionBootstrapRejectsAliasedAndSwappedEndpoints(t *testing.T) {
	for _, mode := range []string{"alias", "swap"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			controlParent, controlChild, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			owned := []*os.File{parent, child, controlParent, controlChild}
			t.Cleanup(func() {
				for _, file := range owned {
					_ = file.Close()
				}
			})
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProductionBootstrapHelper$", "--", "healthy")
			command.Env = []string{"DISPATCH_PRODUCTION_TEST_HELPER=1", ProductionEnvironment + "=" + ProductionSelector, "GORACE=atexit_sleep_ms=0"}
			command.WaitDelay = time.Second
			if mode == "alias" {
				duplicate, err := unix.FcntlInt(parent.Fd(), unix.F_DUPFD_CLOEXEC, 0)
				if err != nil {
					t.Fatal(err)
				}
				controlParent = os.NewFile(uintptr(duplicate), "owned-aliased-parent")
				owned = append(owned, controlParent)
				command.ExtraFiles = []*os.File{child, child}
			} else {
				command.ExtraFiles = []*os.File{controlChild, child}
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			_ = child.Close()
			_ = controlChild.Close()
			defer func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			if err := SendProductionBootstrap(ctx, parent, controlParent, productionTestRecord()); !errors.Is(err, ErrProductionBootstrap) {
				t.Fatalf("incorrect endpoints bootstrapped: %v", err)
			}
			var exited *exec.ExitError
			if err := command.Wait(); !errors.As(err, &exited) || exited.ExitCode() != 1 || ctx.Err() != nil {
				t.Fatalf("incorrect endpoints did not refuse safely: %v, deadline=%v", err, ctx.Err())
			}
		})
	}
}
