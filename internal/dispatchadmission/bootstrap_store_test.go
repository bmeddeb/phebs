//go:build darwin || linux

package dispatchadmission

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func productionStoreTestRecord() ProductionBootstrap {
	record := productionTestRecord()
	record.Producer.ID, record.Phase = 10, 12
	record.Control.Phases, record.Control.InitialPhase = []uint32{12}, 12
	record.Store = &storeaccounting.ClientConfig{Producer: 10, Binding: [32]byte{9}, Phase: 12,
		Phases: 2048, Calls: 1, Transactions: 1, WireBytes: 4096, AckTimeout: time.Second}
	return record
}

func TestProductionStoreRecord(t *testing.T) {
	for _, id := range []uint32{2, 3, 4, 5, 6, 10, 11} {
		record := productionStoreTestRecord()
		record.Producer.ID, record.Store.Producer = id, id
		record.Store.Phases = productionStorePhases(id)
		record.Control.Phases = nil
		for phase := uint32(1); phase <= 15; phase++ {
			if record.Store.Phases&(1<<(phase-1)) != 0 {
				record.Control.Phases = append(record.Control.Phases, phase)
			}
		}
		record.Phase = record.Control.Phases[0]
		record.Store.Phase, record.Control.InitialPhase = record.Phase, record.Phase
		record.Limits.Phases, record.Control.MaximumPhases = 15, 15
		if id <= 6 {
			record.Store.Calls, record.Store.Transactions = 40, 2
			record.SemanticMode, record.InputSHA256, record.Control.OwnerControl = ProductionSemanticV3, [32]byte{1}, true
		}
		if err := record.validate(); err != nil {
			t.Fatalf("producer %d: %v", id, err)
		}
	}
	for _, test := range []struct {
		name string
		edit func(*ProductionBootstrap)
	}{
		{"root", func(r *ProductionBootstrap) { r.Producer.ID, r.Store.Producer = 1, 1 }},
		{"author", func(r *ProductionBootstrap) { r.Program = ProgramCorpusAuthor }},
		{"identity", func(r *ProductionBootstrap) { r.Store.Producer = 11 }},
		{"phase", func(r *ProductionBootstrap) { r.Store.Phase = 11 }},
		{"zero-phase", func(r *ProductionBootstrap) { r.Store.Phase, r.Phase = 0, 0 }},
		{"mask", func(r *ProductionBootstrap) { r.Store.Phases |= 1 }},
		{"control-mask", func(r *ProductionBootstrap) { r.Control.Phases = []uint32{11, 12} }},
		{"binding", func(r *ProductionBootstrap) { r.Store.Binding = [32]byte{} }},
		{"calls", func(r *ProductionBootstrap) { r.Store.Calls = 40 }},
		{"transactions", func(r *ProductionBootstrap) { r.Store.Transactions = 2 }},
		{"wire", func(r *ProductionBootstrap) { r.Store.WireBytes = 511 }},
		{"deadline", func(r *ProductionBootstrap) { r.Store.AckTimeout = 0 }},
		{"cli-owners", func(r *ProductionBootstrap) { r.Control.OwnerControl = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := productionStoreTestRecord()
			test.edit(&record)
			if !errors.Is(record.validate(), ErrProductionBootstrap) {
				t.Fatal("invalid store record accepted")
			}
		})
	}
	// Retain existing numeric ceilings: the extra field's maximal JSON width
	// is bounded independently from the unchanged complete-record byte cap.
	config := storeaccounting.ClientConfig{Producer: 11, Phase: 15, Phases: 32767,
		Calls: 40, Transactions: 2, WireBytes: math.MaxUint64, AckTimeout: time.Duration(math.MaxInt64)}
	for i := range config.Binding {
		config.Binding[i] = 255
	}
	raw, err := json.Marshal(config)
	if err != nil || len(raw)+len(`,"Store":`) != 284 {
		t.Fatalf("store record width=%d: %v", len(raw), err)
	}
	legacy := productionTestRecord()
	raw, err = json.Marshal(legacy)
	if err != nil || bytes.Contains(raw, []byte(`"Store"`)) {
		t.Fatal("legacy canonical record gained a store field")
	}
	var lifetime *ProductionLifetime
	if client, err := lifetime.TakeStoreOwner(); client != nil || err != nil {
		t.Fatal("ordinary path acquired store state")
	}
}

func TestProductionStoreFDIsolation(t *testing.T) {
	if os.Getenv("DISPATCH_STORE_FD_ISOLATION") != "1" {
		return
	}
	// This deliberately tiny child opens no sockets. Check all low descriptors,
	// including transferred poller duplicates, not merely the original FD5.
	for fd := 3; fd < 64; fd++ {
		if _, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE); err == nil {
			t.Fatalf("bootstrap socket inherited at fd %d", fd)
		}
	}
}

func TestProductionStoreAttachCancellation(t *testing.T) {
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	parentConn, err := adopt(parent)
	if err != nil {
		_ = child.Close()
		t.Fatal(err)
	}
	defer func() { _ = parentConn.Close() }()
	conn, err := adopt(child)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	config := *productionStoreTestRecord().Store
	config.AckTimeout = time.Duration(math.MaxInt64)
	if err := parentConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		client, stop, err := bootstrapStoreClient(ctx, t.Context(), conn, config)
		if client != nil || stop != nil {
			if stop != nil {
				stop()
			}
			result <- errors.New("canceled handshake returned a live owner")
			return
		}
		result <- err
	}()
	// Observe the genuine Attach bytes but deliberately withhold the ACK.
	var raw [storeaccounting.FrameBytes]byte
	if _, err := io.ReadFull(parentConn, raw[:]); err != nil || string(raw[:4]) != "SA01" {
		cancel()
		<-result
		t.Fatalf("Attach: %v", err)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrProductionBootstrap) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		// Close the original socket too, then join before failing this test.
		_ = parentConn.Close()
		<-result
		t.Fatal("bootstrap cancellation did not interrupt Attach")
	}
}

func TestProductionStoreHelper(t *testing.T) {
	mode := os.Getenv("DISPATCH_STORE_HELPER")
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	lifetime, err := BootstrapProduction(ctx)
	if strings.HasPrefix(mode, "refuse-") {
		if !errors.Is(err, ErrProductionBootstrap) || lifetime != nil || productionRuntime.Load() != nil {
			t.Fatalf("selected refusal: %v", err)
		}
		return
	}
	if err != nil || lifetime == nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if owner, err := ProcessStoreOwner(); owner != nil || !errors.Is(err, ErrProductionBootstrap) {
		t.Fatal("selected factory acquired an untaken owner")
	}
	if mode != "unclaimed" {
		owner, err := lifetime.TakeStoreOwner()
		if err != nil || owner == nil || owner.Check(ctx) != nil {
			t.Fatalf("store lifetime expired with bootstrap: %v", err)
		}
		if again, err := lifetime.TakeStoreOwner(); again != nil || !errors.Is(err, ErrProductionBootstrap) {
			t.Fatal("duplicate handoff accepted")
		}
		if actual, err := ProcessStoreOwner(); actual != owner || err != nil {
			t.Fatal("factory did not receive the retained exact owner")
		}
	}
	probe := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProductionStoreFDIsolation$")
	probe.Env = []string{"DISPATCH_STORE_FD_ISOLATION=1", "GORACE=atexit_sleep_ms=0"}
	if output, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("descendant FD isolation: %s %v", output, err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
		t.Fatal(err)
	}
	if mode == "failed-store" {
		_ = lifetime.storeClient.Fail(ctx, storeaccounting.ErrTransport)
		if owner, err := ProcessStoreOwner(); owner != nil || !errors.Is(err, ErrProductionBootstrap) {
			t.Fatal("failed store owner fell back to ordinary execution")
		}
	}
	err = lifetime.Close(ctx)
	if mode == "healthy" && err != nil || mode != "healthy" && err == nil {
		t.Fatalf("terminal %s: %v", mode, err)
	}
	if _, err := lifetime.TakeStoreOwner(); !errors.Is(err, ErrProductionBootstrap) {
		t.Fatal("closed lifetime handed out a store client")
	}
	if owner, err := ProcessStoreOwner(); owner != nil || !errors.Is(err, ErrProductionBootstrap) {
		t.Fatal("closed lifetime handed out its store owner")
	}
}

func TestProductionStoreInheritedBootstrap(t *testing.T) {
	for _, mode := range []string{"healthy", "unclaimed", "failed-store", "refuse-missing-fd", "refuse-old-selector", "refuse-missing-record", "refuse-binding", "refuse-alias-admission", "refuse-alias-control"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			record := productionStoreTestRecord()
			storeController, err := storeaccounting.New(ctx, storeaccounting.Config{
				Producers: []storeaccounting.Producer{{ID: 10, Calls: 1, Transactions: 1}},
				Phases:    []storeaccounting.Phase{{ID: 12, Transactions: 1, Rows: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			transport, err := storeaccounting.NewTransport(ctx, storeController, storeaccounting.WireConfig{
				Producers: []storeaccounting.WireProducer{{ID: 10, Binding: record.Store.Binding, Phases: 2048}}, AckTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = transport.Close() }()
			storeChild, config, err := transport.Open(10)
			if err != nil {
				t.Fatal(err)
			}
			record.Store = &config
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			controlParent, controlChild, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = parent.Close()
				_ = child.Close()
				_ = controlParent.Close()
				_ = controlChild.Close()
				_ = storeChild.Close()
			}()
			selector := ProductionStoreSelector
			files := []*os.File{child, controlChild, storeChild}
			switch mode {
			case "refuse-missing-fd":
				files = files[:2]
			case "refuse-old-selector":
				selector = ProductionSelector
			case "refuse-missing-record":
				record.Store = nil
			case "refuse-binding":
				record.Store.Binding[0]++
			case "refuse-alias-admission":
				files[2] = child
			case "refuse-alias-control":
				files[2] = controlChild
			}
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProductionStoreHelper$")
			command.Env = []string{"DISPATCH_STORE_HELPER=" + mode, ProductionEnvironment + "=" + selector, "GORACE=atexit_sleep_ms=0"}
			command.ExtraFiles, command.Stderr, command.WaitDelay = files, os.Stderr, time.Second
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			_ = child.Close()
			_ = controlChild.Close()
			_ = storeChild.Close()
			defer func() {
				_ = input.Close()
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			err = SendProductionBootstrap(ctx, parent, controlParent, record)
			if strings.HasPrefix(mode, "refuse-") {
				if err == nil {
					t.Fatal("invalid bootstrap acknowledged")
				}
				if err := command.Wait(); err != nil {
					t.Fatal(err)
				}
				_ = transport.Close()
				if snapshot, _ := transport.Snapshot(); snapshot.Complete || snapshot.Store.Transactions != 0 {
					t.Fatalf("invalid bootstrap fabricated work/completion: %+v", snapshot)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			control, err := NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = control.Close() }()
			dispatch, err := New(ctx, Config{Limits: record.Limits, Producers: []Producer{record.Producer},
				Phases: []Phase{{ID: 12, Roles: []RoleBudget{{Role: RoleGit}, {Role: RoleSurreal}, {Role: RoleZoekt}, {Role: RoleCompatibility}}}}})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- dispatch.Serve(ctx, 10, command.Process.Pid, parent) }()
			ready, err := bufio.NewReader(output).ReadString('\n')
			if err != nil || ready != "ready\n" {
				t.Fatalf("helper readiness %q: %v", ready, err)
			}
			if err := control.Pause(ctx); err != nil {
				t.Fatal(err)
			}
			if dispatch.Fence() != nil || transport.Fence() != nil {
				t.Fatal("mechanical test fence failed")
			}
			checkpointErr := control.Checkpoint(ctx)
			if (checkpointErr != nil) != (mode == "unclaimed") {
				t.Fatalf("SDK-bound checkpoint: %v", checkpointErr)
			}
			if _, err := input.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; (err == nil) != (mode == "healthy") {
				t.Fatalf("dispatch closure: %v", err)
			}
			storeErr := transport.Wait(ctx, 10)
			snapshot, snapshotErr := transport.Snapshot()
			if mode == "healthy" {
				if storeErr != nil || snapshotErr != nil || !snapshot.Complete || snapshot.Store.Transactions != 0 {
					t.Fatalf("healthy mechanical closure: %+v %v/%v", snapshot, storeErr, snapshotErr)
				}
			} else if storeErr == nil || snapshotErr == nil || snapshot.Complete {
				t.Fatal("unclosed SDK-owner path claimed complete")
			}
		})
	}
}
