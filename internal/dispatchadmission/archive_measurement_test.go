//go:build darwin || linux

package dispatchadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func archiveMeasurementTestBinding() ArchiveMeasurementBinding {
	return ArchiveMeasurementBinding{ProducerID: 10, ProducerBinding: [32]byte{3}, InputSHA256: [32]byte{7}}
}

func archiveMeasurementTestPipe(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	a, b, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := adopt(a)
	if err != nil {
		t.Fatal(err)
	}
	child, err := adopt(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })
	return parent, child
}

func TestArchiveMeasurementSelection(t *testing.T) {
	for _, mode := range []string{"backup", "restore", "no_workspace", "no_deadline", "server", "wrong_producer", "wrong_phase", "no_store", "no_input"} {
		t.Run(mode, func(t *testing.T) {
			r := productionStoreTestRecord()
			r.Control.MaximumPhases = 1
			r.Workspace = &ProductionWorkspaceBinding{Path: "/private/workspace", Inode: 1, FSID: [2]int32{1, 2}}
			r.ArchiveDeadlineUnixNano, r.ArchiveMeasurements = time.Now().Add(time.Minute).UnixNano(), 16
			switch mode {
			case "restore":
				r.Producer.ID, r.Store.Producer = 11, 11
			case "no_workspace":
				r.Workspace = nil
			case "no_deadline":
				r.ArchiveDeadlineUnixNano = 0
			case "server":
				r.SemanticMode = ProductionSemanticV3
			case "wrong_producer":
				r.Producer.ID = 6
			case "wrong_phase":
				r.Phase = 13
			case "no_store":
				r.Store = nil
			case "no_input":
				r.InputSHA256 = [32]byte{}
			}
			if (r.validate() == nil) != (mode == "backup" || mode == "restore") {
				t.Fatal("archive selection", mode)
			}
		})
	}
	for _, r := range []ProductionBootstrap{productionTestRecord(), productionStoreTestRecord()} {
		raw, err := json.Marshal(r)
		if err != nil || bytes.Contains(raw, []byte("ArchiveMeasurements")) {
			t.Fatal("omitted legacy bytes changed", err)
		}
	}
	for _, maximum := range []uint32{1, 16, math.MaxUint32} {
		got, err := ArchiveMeasurementWireBytes(maximum)
		if err != nil || got != 152+64*uint64(maximum) {
			t.Fatal("wire arithmetic", got, err)
		}
	}
	if _, err := ArchiveMeasurementWireBytes(0); err == nil {
		t.Fatal("unbounded traffic admitted")
	}
}

func TestArchiveMeasurementReleaseAfterGuard(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	parent, child := archiveMeasurementTestPipe(t)
	var held atomic.Bool
	finishing, resume := make(chan struct{}), make(chan struct{})
	server := make(chan error, 1)
	go func() {
		server <- ServeArchiveMeasurement(ctx, parent, archiveMeasurementTestBinding(), 1, func(ctx context.Context, measure func(context.Context) error) error {
			held.Store(true)
			defer held.Store(false)
			err := measure(ctx)
			close(finishing)
			select {
			case <-resume:
			case <-ctx.Done():
				return ctx.Err()
			}
			return err
		})
	}()
	client, err := newArchiveMeasurementClient(ctx, ctx, child, archiveMeasurementTestBinding(), 1)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- client.measure(ctx, func(context.Context) error {
			if !held.Load() {
				return errors.New("measurement escaped parent guard")
			}
			return nil
		})
	}()
	<-finishing
	select {
	case err := <-result:
		t.Fatal("release acknowledged before guard resumed", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(resume)
	if err := <-result; err != nil || held.Load() {
		t.Fatal("guard not released", err)
	}
	if err := client.close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
}

func TestArchiveMeasurementFailureUnwinds(t *testing.T) {
	for _, mode := range []string{"error", "panic", "cancel", "eof"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			parent, child := archiveMeasurementTestPipe(t)
			var held atomic.Bool
			server := make(chan error, 1)
			go func() {
				server <- ServeArchiveMeasurement(ctx, parent, archiveMeasurementTestBinding(), 1, func(ctx context.Context, measure func(context.Context) error) error {
					held.Store(true)
					defer held.Store(false)
					return measure(ctx)
				})
			}()
			client, err := newArchiveMeasurementClient(ctx, ctx, child, archiveMeasurementTestBinding(), 1)
			if err != nil {
				t.Fatal(err)
			}
			sentinel := errors.New("local measurement failure")
			func() {
				err = client.measure(ctx, func(context.Context) error {
					if !held.Load() {
						return errors.New("parent not holding")
					}
					switch mode {
					case "error":
						return sentinel
					case "panic":
						panic(sentinel)
					case "cancel":
						cancel()
					case "eof":
						_ = child.Close()
					}
					return nil
				})
			}()
			if mode == "error" && !errors.Is(err, sentinel) || mode == "panic" && !errors.Is(err, ErrPanic) || (mode == "cancel" || mode == "eof") && err == nil {
				t.Fatal("failure became success", err)
			}
			if mode == "panic" && client.err == nil {
				t.Fatal("callback panic did not latch failure")
			}
			serverErr := <-server
			if held.Load() || (mode == "cancel" || mode == "eof") && serverErr == nil {
				t.Fatal("parent guard did not unwind", serverErr)
			}
		})
	}
}

func TestArchiveMeasurementRejectsWire(t *testing.T) {
	for _, mode := range []string{"input", "producer", "maximum", "release_without_hold", "ordinal", "reserved", "truncated", "extra_checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			parent, child := archiveMeasurementTestPipe(t)
			var calls atomic.Int32
			server := make(chan error, 1)
			go func() {
				server <- ServeArchiveMeasurement(ctx, parent, archiveMeasurementTestBinding(), 1, func(ctx context.Context, measure func(context.Context) error) error {
					calls.Add(1)
					return measure(ctx)
				})
			}()
			binding, maximum := archiveMeasurementTestBinding(), uint32(1)
			switch mode {
			case "input":
				binding.InputSHA256[0]++
			case "producer":
				binding.ProducerBinding[0]++
			case "maximum":
				maximum++
			}
			client, err := newArchiveMeasurementClient(ctx, ctx, child, binding, maximum)
			if mode == "input" || mode == "producer" || mode == "maximum" {
				if err == nil {
					t.Fatal("foreign header admitted")
				}
				_ = child.Close()
			} else {
				if err != nil {
					t.Fatal(err)
				}
				frame := archiveMeasurementFrame(archiveMeasurementHold, 1)
				switch mode {
				case "release_without_hold":
					frame[4] = archiveMeasurementRelease
				case "ordinal":
					frame = archiveMeasurementFrame(archiveMeasurementHold, 2)
				case "reserved":
					frame[5] = 1
				case "extra_checkpoint":
					if err := client.measure(ctx, func(context.Context) error { return nil }); err != nil {
						t.Fatal(err)
					}
					frame = archiveMeasurementFrame(archiveMeasurementHold, 2)
				}
				if mode == "truncated" {
					_, _ = child.Write(frame[:1])
					_ = child.CloseWrite()
				} else {
					_, _ = child.Write(frame[:])
				}
				var ack [archiveMeasurementFrameBytes]byte
				if _, err := io.ReadFull(child, ack[:]); err == nil {
					t.Fatal("invalid frame acknowledged")
				}
			}
			if err := <-server; err == nil {
				t.Fatal("invalid transport succeeded")
			}
			want := int32(0)
			if mode == "extra_checkpoint" {
				want = 1
			}
			if calls.Load() != want {
				t.Fatal("invalid traffic reached guard", calls.Load())
			}
		})
	}
}

func TestArchiveMeasurementGuardContainment(t *testing.T) {
	for _, mode := range []string{"skipped", "double", "escaped", "escaped_panic", "panic_after_callback", "replaced_context"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			parent, child := archiveMeasurementTestPipe(t)
			server, escaped := make(chan error, 1), make(chan error, 1)
			measurementEntered, serverEnded := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(serverEnded)
				server <- ServeArchiveMeasurement(ctx, parent, archiveMeasurementTestBinding(), 1, func(ctx context.Context, measure func(context.Context) error) error {
					switch mode {
					case "skipped":
						return nil
					case "double":
						_ = measure(ctx)
						_ = measure(ctx)
						return nil // An ignored second-callback error still refuses.
					case "escaped", "escaped_panic":
						go func() { escaped <- measure(ctx) }()
						<-measurementEntered
						if mode == "escaped_panic" {
							panic("invalid asynchronous guard")
						}
						return nil
					case "panic_after_callback":
						_ = measure(ctx)
						panic("invalid guard")
					default:
						return measure(context.Background())
					}
				})
			}()
			client, err := newArchiveMeasurementClient(ctx, ctx, child, archiveMeasurementTestBinding(), 1)
			if err != nil {
				t.Fatal(err)
			}
			err = client.measure(ctx, func(operation context.Context) error {
				if mode == "escaped" || mode == "escaped_panic" {
					close(measurementEntered)
					<-serverEnded // Parent must close the read and join before release.
				}
				if mode == "replaced_context" {
					if operation != ctx {
						return errors.New("guard replaced original context")
					}
					cancel()
					return operation.Err()
				}
				return nil
			})
			if err == nil {
				t.Fatal("invalid guard produced completed measurement")
			}
			if err := <-server; err == nil {
				t.Fatal("invalid guard server succeeded")
			}
			if mode == "escaped" || mode == "escaped_panic" {
				if err := <-escaped; err == nil {
					t.Fatal("escaped callback succeeded")
				}
			}
		})
	}
}
