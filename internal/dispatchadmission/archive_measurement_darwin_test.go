//go:build darwin

package dispatchadmission

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestArchiveMeasurementBootstrapHelper(t *testing.T) {
	mode := os.Getenv("DISPATCH_ARCHIVE_MEASUREMENT_HELPER")
	if mode == "" {
		return
	}
	prior := captureInheritedArchiveMeasurement()
	lifetime, err := BootstrapProduction(context.Background())
	if mode == "missing" || mode == "wrong" {
		if err == nil || lifetime != nil || productionRuntime.Load() != nil {
			t.Fatal("missing original FD7 was admitted")
		}
		a, b, err := NewPipe()
		if err != nil || a.Close() != nil || b.Close() != nil {
			t.Fatal("refusal corrupted newly allocated descriptors", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx := ProcessContext()
	maximum, err := ProductionArchiveMeasurements()
	want := uint32(16)
	if mode == "omitted" {
		want = 0
	}
	if err != nil || maximum != want {
		t.Fatal("bootstrap count changed", maximum, err)
	}
	if _, err := lifetime.TakeStoreOwner(); err != nil {
		t.Fatal(err)
	}
	if mode == "valid" {
		if err := ProductionArchiveMeasurement(ctx, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := ProductionArchiveMeasurement(ctx, func(context.Context) error { t.Error("unselected measurement ran"); return nil }); err == nil {
			t.Fatal("unselected FD7 authority")
		}
		current := captureInheritedArchiveMeasurement()
		if prior == nil || current == nil || *current != *prior {
			t.Fatal("restore or legacy omission adopted FD7")
		}
	}
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
		t.Fatal(err)
	}
	closeContext, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := lifetime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
	if mode == "omitted" || mode == "restore" {
		current := captureInheritedArchiveMeasurement()
		if current == nil || prior == nil || *current != *prior {
			t.Fatal("lifetime close touched omitted FD7")
		}
	}
}

// Real inherited FD3..7, bootstrap and terminal joins, but no native database,
// production image or workspace high-water proof. The parent guard is modeled.
func TestArchiveMeasurementInherited(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "wrong", "omitted", "restore"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			workspace, workspaceBinding := workspaceTestRoot(t)
			r := productionStoreTestRecord()
			r.Control.MaximumPhases, r.Limits.Phases = 1, 3
			r.Workspace, r.ArchiveMeasurements = &workspaceBinding, 16
			deadline, _ := ctx.Deadline()
			r.ArchiveDeadlineUnixNano = deadline.UnixNano()
			if mode == "restore" {
				r.Producer.ID, r.Store.Producer = 11, 11
			}
			if mode == "omitted" {
				r.Workspace, r.ArchiveMeasurements, r.ArchiveDeadlineUnixNano = nil, 0, 0
			}
			store, err := storeaccounting.New(ctx, storeaccounting.Config{
				Producers: []storeaccounting.Producer{{ID: r.Producer.ID, Calls: 1, Transactions: 1}},
				Phases:    []storeaccounting.Phase{{ID: 12, Transactions: 1, Rows: 1}},
			})
			if err != nil {
				t.Fatal(err)
			}
			transport, err := storeaccounting.NewTransport(ctx, store, storeaccounting.WireConfig{
				Producers: []storeaccounting.WireProducer{{ID: r.Producer.ID, Binding: r.Store.Binding, Phases: r.Store.Phases}}, AckTimeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = transport.Close() }()
			storeChild, config, err := transport.Open(r.Producer.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = storeChild.Close() }()
			r.Store = &config
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Close(); _ = child.Close() }()
			pcParent, pcChild, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = pcParent.Close(); _ = pcChild.Close() }()
			archiveParent, archiveChild, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = archiveParent.Close(); _ = archiveChild.Close() }()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArchiveMeasurementBootstrapHelper$")
			command.Env = []string{"DISPATCH_ARCHIVE_MEASUREMENT_HELPER=" + mode, ProductionEnvironment + "=" + ProductionStoreSelector, "GORACE=atexit_sleep_ms=0"}
			command.ExtraFiles = []*os.File{child, pcChild, storeChild, workspace, archiveChild}
			command.Stderr, command.WaitDelay = os.Stderr, time.Second
			switch mode {
			case "missing":
				command.ExtraFiles = command.ExtraFiles[:4]
			case "wrong":
				command.ExtraFiles[4] = workspace
			}
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = input.Close() }()
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			_ = child.Close()
			_ = pcChild.Close()
			_ = storeChild.Close()
			_ = archiveChild.Close()
			var calls atomic.Uint32
			var archiveDone chan error
			if mode == "valid" {
				conn, err := adopt(archiveParent)
				if err != nil {
					t.Fatal(err)
				}
				archiveDone = make(chan error, 1)
				go func() {
					archiveDone <- ServeArchiveMeasurement(ctx, conn, ArchiveMeasurementBinding{ProducerID: 10, ProducerBinding: r.Producer.Binding, InputSHA256: r.InputSHA256}, 16,
						func(ctx context.Context, measure func(context.Context) error) error {
							calls.Add(1)
							return measure(ctx)
						})
				}()
				defer func() {
					cancel()
					if err := <-archiveDone; err != nil && !t.Failed() {
						t.Error("archive server terminal join", err)
					}
				}()
			}
			bootstrapErr := SendProductionBootstrap(ctx, parent, pcParent, r)
			if mode == "missing" || mode == "wrong" {
				if bootstrapErr == nil {
					t.Fatal("invalid inherited FD7 acknowledged")
				}
				if err := command.Wait(); err != nil {
					t.Fatal(err)
				}
				return
			}
			if bootstrapErr != nil {
				t.Fatal(bootstrapErr)
			}
			control, err := NewPhaseControl(ctx, pcParent, r.Producer.Binding, r.Control)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = control.Close() }()
			dispatch, err := New(ctx, Config{Limits: r.Limits, Producers: []Producer{r.Producer}, Phases: []Phase{{ID: 12, Roles: []RoleBudget{{Role: RoleGit, Attempts: 1}, {Role: RoleSurreal}, {Role: RoleZoekt}, {Role: RoleCompatibility}}}}})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- dispatch.Serve(ctx, r.Producer.ID, command.Process.Pid, parent) }()
			defer func() { cancel(); <-done }()
			ready, err := bufio.NewReader(output).ReadString('\n')
			if err != nil || ready != "ready\n" {
				t.Fatal("child not ready", ready, err)
			}
			if control.Pause(ctx) != nil || dispatch.Fence() != nil || transport.Fence() != nil {
				t.Fatal("parent fence")
			}
			if _, err := input.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err != nil {
				t.Fatal(err)
			}
			if err := transport.Wait(ctx, r.Producer.ID); err != nil {
				t.Fatal(err)
			}
			if mode == "valid" && calls.Load() != 1 {
				t.Fatal("wrong checkpoint count", calls.Load())
			}
		})
	}
}
