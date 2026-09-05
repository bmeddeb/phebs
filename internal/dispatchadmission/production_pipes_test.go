//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func assertCommandPipesClosed(t *testing.T, pipes *CommandPipes) {
	t.Helper()
	for index, file := range []*os.File{pipes.stdin[0], pipes.stdin[1], pipes.stdout[0], pipes.stdout[1]} {
		if file != nil {
			if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("owned pipe end %d still open: %v", index, err)
			}
		}
	}
	// Keep every command and both ends reachable through the assertion; never
	// force GC or depend on exec.Cmd/os.File finalizers to release a refused pipe.
	runtime.KeepAlive(pipes.command)
	runtime.KeepAlive(pipes)
}

func setPipedTestRuntime(t *testing.T, lifetime *ProductionLifetime) {
	t.Helper()
	prior := productionRuntime.Swap(lifetime)
	t.Cleanup(func() { productionRuntime.Store(prior) })
}

func TestCommandPipesRefusesReplacingOwnedPair(t *testing.T) {
	for _, stdin := range []bool{false, true} {
		t.Run(map[bool]string{false: "stdout", true: "stdin"}[stdin], func(t *testing.T) {
			command := exec.Command("/usr/bin/true")
			var pipes CommandPipes
			defer func() { _ = pipes.Close() }()
			var err error
			if stdin {
				_, err = pipes.StdinPipe(command)
			} else {
				_, err = pipes.StdoutPipe(command)
			}
			if err != nil {
				t.Fatal(err)
			}
			original := pipes // Retain the original descriptors without GC.
			if stdin {
				command.Stdin = nil
				_, err = pipes.StdinPipe(command)
			} else {
				command.Stdout = nil
				_, err = pipes.StdoutPipe(command)
			}
			if !errors.Is(err, ErrConfig) {
				t.Fatal("replacement pipe pair admitted", err)
			}
			assertCommandPipesClosed(t, &original)
			assertCommandPipesClosed(t, &pipes)
		})
	}
}

func TestPipedProductionOrdinaryAndAdmittedWait(t *testing.T) {
	for _, exact := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "admitted"}[exact], func(t *testing.T) {
			var controller *Controller
			var client *Client
			var server <-chan error
			if exact {
				controller, client, server = paired(t, testConfig())
				setPipedTestRuntime(t, &ProductionLifetime{program: ProgramPhebs, client: client,
					tools: map[string]ProductionToolBinding{"git": productionTestRecord().Tools[0]}})
			} else {
				setPipedTestRuntime(t, nil)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/bin/sh", "-c", `IFS= read -r line; printf '%s\n' "$line"`)
			borrowed, err := os.CreateTemp(t.TempDir(), "borrowed-stderr-")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = borrowed.Close() }()
			command.Stderr = borrowed
			var pipes CommandPipes
			defer func() { _ = pipes.Close() }()
			input, err := pipes.StdinPipe(command)
			if err != nil {
				t.Fatal(err)
			}
			output, err := pipes.StdoutPipe(command)
			if err != nil {
				t.Fatal(err)
			}
			handle, err := StartPipedProduction(ctx, SiteCandidateTree, command, &pipes)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = handle.Wait()
				}
			}()
			for _, file := range []*os.File{pipes.stdin[0], pipes.stdout[1]} {
				if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("parent retained child-side pipe copy: %v", err)
				}
			}
			// Refusing a second Start must not close the first live child's IO.
			if _, err := StartPipedProduction(ctx, SiteCandidateTree, command, &pipes); !errors.Is(err, ErrConfig) {
				t.Fatalf("second start: %v", err)
			}
			if _, err := io.WriteString(input, "pipe-owner\n"); err != nil {
				t.Fatal(err)
			}
			if err := input.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(output)
			if err != nil || string(data) != "pipe-owner\n" {
				t.Fatalf("ordinary stream semantics changed: %q, %v", data, err)
			}
			if err := handle.Wait(); err != nil {
				t.Fatal(err)
			}
			assertCommandPipesClosed(t, &pipes)
			if _, err := borrowed.Stat(); err != nil {
				t.Fatalf("borrowed stderr closed: %v", err)
			}
			if exact {
				snapshot := finishPair(t, controller, client, server)
				if snapshot.Attempts != 1 || snapshot.Producers[0].Active != 0 {
					t.Fatalf("pipe handle did not settle exactly once: %+v", snapshot)
				}
			}
		})
	}
}

func TestPipedProductionRefusalRetainsCommandsWithoutGC(t *testing.T) {
	for _, mode := range []string{"admission-refused", "ordinary-start-failed", "canceled-before-start"} {
		t.Run(mode, func(t *testing.T) {
			var controller *Controller
			var server <-chan error
			if mode == "admission-refused" {
				config := testConfig()
				config.Phases[0].Roles[0].Attempts = 0
				var client *Client
				controller, client, server = paired(t, config)
				setPipedTestRuntime(t, &ProductionLifetime{program: ProgramPhebs, client: client,
					tools: map[string]ProductionToolBinding{"git": productionTestRecord().Tools[0]}})
			} else {
				setPipedTestRuntime(t, nil)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled-before-start" {
				cancel()
			}
			var retained []*CommandPipes
			for range 64 {
				path := "/bin/sh"
				if mode == "ordinary-start-failed" {
					path = "/dispatch-admission-no-such-executable"
				}
				command := exec.CommandContext(ctx, path, "-c", "exit 0")
				pipes := &CommandPipes{}
				retained = append(retained, pipes)
				if _, err := pipes.StdinPipe(command); err != nil {
					t.Fatal(err)
				}
				if _, err := pipes.StdoutPipe(command); err != nil {
					t.Fatal(err)
				}
				if _, err := StartPipedProduction(ctx, SiteCandidateTree, command, pipes); err == nil {
					t.Fatal("refused command started")
				}
				if command.Process != nil {
					t.Fatal("refusal reached native Start")
				}
				assertCommandPipesClosed(t, pipes)
			}
			for _, pipes := range retained {
				assertCommandPipesClosed(t, pipes)
			}
			runtime.KeepAlive(retained)
			if controller != nil {
				select {
				case err := <-server:
					if !errors.Is(err, ErrLimit) {
						t.Fatalf("parent did not reject zero budget: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("refused parent did not terminate")
				}
				snapshot, err := controller.Snapshot()
				if err == nil || snapshot.Complete || snapshot.Attempts != 0 {
					t.Fatalf("refusal hidden by cleanup: %+v, %v", snapshot, err)
				}
			}
		})
	}
}

func TestPipedProductionCanceledChildIsJoined(t *testing.T) {
	setPipedTestRuntime(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/cat")
	var pipes CommandPipes
	defer func() { _ = pipes.Close() }()
	if _, err := pipes.StdinPipe(command); err != nil {
		t.Fatal(err)
	}
	if _, err := pipes.StdoutPipe(command); err != nil {
		t.Fatal(err)
	}
	handle, err := StartPipedProduction(ctx, SiteCandidateTree, command, &pipes)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := handle.Wait(); err == nil || command.ProcessState == nil {
		t.Fatalf("canceled child was not joined: %v", err)
	}
	assertCommandPipesClosed(t, &pipes)
}

func TestCommandPipesSetupRefusalsCloseOwnedNotBorrowed(t *testing.T) {
	for _, mode := range []string{"occupied-stdin", "wrong-command-setup", "nil-command-setup", "wrong-command-start", "nil-command-start", "replaced-stdout"} {
		t.Run(mode, func(t *testing.T) {
			setPipedTestRuntime(t, nil)
			command := exec.Command("/bin/sh", "-c", "exit 0")
			borrowed, err := os.CreateTemp(t.TempDir(), "borrowed-stdio-")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = borrowed.Close() }()
			command.Stdin, command.Stderr = borrowed, borrowed
			var pipes CommandPipes
			defer func() { _ = pipes.Close() }()
			if _, err := pipes.StdoutPipe(command); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "occupied-stdin":
				_, err = pipes.StdinPipe(command)
			case "wrong-command-setup":
				_, err = pipes.StdinPipe(exec.Command("/bin/sh"))
			case "nil-command-setup":
				_, err = pipes.StdinPipe(nil)
			case "wrong-command-start":
				_, err = StartPipedProduction(t.Context(), SiteCandidateTree, exec.Command("/bin/sh"), &pipes)
			case "nil-command-start":
				_, err = StartPipedProduction(t.Context(), SiteCandidateTree, nil, &pipes)
			case "replaced-stdout":
				command.Stdout = borrowed
				_, err = StartPipedProduction(t.Context(), SiteCandidateTree, command, &pipes)
			}
			if !errors.Is(err, ErrConfig) || command.Process != nil {
				t.Fatalf("setup was not refused: %v", err)
			}
			assertCommandPipesClosed(t, &pipes)
			if _, err := borrowed.Stat(); err != nil {
				t.Fatalf("borrowed stdio closed: %v", err)
			}
		})
	}
}

func TestCommandPipesCloseAttemptsEveryOwnedEnd(t *testing.T) {
	command := exec.Command("/bin/sh")
	var pipes CommandPipes
	defer func() { _ = pipes.Close() }()
	if _, err := pipes.StdinPipe(command); err != nil {
		t.Fatal(err)
	}
	if _, err := pipes.StdoutPipe(command); err != nil {
		t.Fatal(err)
	}
	// Induce EBADF on the first owned close, without allocating another FD
	// between the raw close and Close. Later ends must still all be attempted.
	if err := syscall.Close(int(pipes.stdin[0].Fd())); err != nil {
		t.Fatal(err)
	}
	if err := pipes.Close(); !errors.Is(err, ErrTransport) {
		t.Fatalf("owned close failure hidden: %v", err)
	}
	assertCommandPipesClosed(t, &pipes)
}
