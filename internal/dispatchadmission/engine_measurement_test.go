//go:build darwin || linux

package dispatchadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func engineMeasurementTestBinding() EngineMeasurementBinding {
	return EngineMeasurementBinding{ProducerID: 3, ProducerBinding: [32]byte{3}, InputSHA256: [32]byte{7}}
}

func engineMeasurementTestIdentity() EngineMeasurementIdentity {
	return EngineMeasurementIdentity{PID: 42, Birth: 91, Tracer: 17}
}

func engineMeasurementTestRecord(producer uint32) ProductionBootstrap {
	record := foregroundGitTestRecord(producer)
	record.Workspace = &ProductionWorkspaceBinding{Path: "/private/workspace", Inode: 1, FSID: [2]int32{1, 2}}
	record.EngineMeasurements = engineMeasurementMaximum(producer)
	return record
}

func TestEngineMeasurementBootstrapSelection(t *testing.T) {
	for _, producer := range []uint32{2, 3, 4} {
		record := engineMeasurementTestRecord(producer)
		if (record.validate() == nil) != (runtime.GOOS == "linux" && runtime.GOARCH == "amd64") {
			t.Fatal("platform or closed producer shape", producer)
		}
	}
	for _, test := range []struct {
		name string
		edit func(*ProductionBootstrap)
	}{
		{"archive-conflict", func(r *ProductionBootstrap) { r.ArchiveMeasurements = 1 }},
		{"missing-workspace", func(r *ProductionBootstrap) { r.Workspace = nil }},
		{"missing-store", func(r *ProductionBootstrap) { r.Store = nil }},
		{"missing-input", func(r *ProductionBootstrap) { r.InputSHA256 = [32]byte{} }},
		{"author", func(r *ProductionBootstrap) { r.Program = ProgramCorpusAuthor }},
		{"ordinary", func(r *ProductionBootstrap) { r.SemanticMode = "" }},
		{"wrong-producer", func(r *ProductionBootstrap) { r.Producer.ID = 5 }},
		{"wrong-phase", func(r *ProductionBootstrap) { r.Phase++ }},
		{"extra-phase", func(r *ProductionBootstrap) { r.Control.Phases = append(r.Control.Phases, 9) }},
		{"wrong-maximum", func(r *ProductionBootstrap) { r.EngineMeasurements++ }},
		{"missing-git-recipe", func(r *ProductionBootstrap) { r.Tools[0].RuntimeRecipe = "" }},
		{"missing-index-recipe", func(r *ProductionBootstrap) { r.Tools[2].RuntimeRecipe = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := engineMeasurementTestRecord(2)
			test.edit(&record)
			if record.validate() == nil {
				t.Fatal("invalid selection admitted")
			}
		})
	}
	for _, record := range []ProductionBootstrap{productionTestRecord(), productionStoreTestRecord(), foregroundGitTestRecord(4)} {
		raw, err := json.Marshal(record)
		if err != nil || bytes.Contains(raw, []byte("EngineMeasurements")) {
			t.Fatal("historical omitted bytes changed", err)
		}
	}
	for _, maximum := range []uint32{1, 2, 6} {
		got, err := EngineMeasurementWireBytes(maximum)
		if err != nil || got != 152+80*uint64(maximum) {
			t.Fatal("wire budget", got, err)
		}
	}
	for _, maximum := range []uint32{0, 7, ^uint32(0)} {
		if _, err := EngineMeasurementWireBytes(maximum); err == nil {
			t.Fatal("unbounded wire reservation")
		}
	}
	if engineMeasurementPhase(4, 7) || engineMeasurementPhase(4, 8) || !engineMeasurementPhase(4, 6) {
		t.Fatal("return-A hold scope widened")
	}
}

func TestEngineMeasurementReleaseAfterActualGuard(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	parent, child := archiveMeasurementTestPipe(t)
	var held atomic.Bool
	finishing, resume := make(chan struct{}), make(chan struct{})
	server := make(chan error, 1)
	go func() {
		server <- ServeEngineMeasurement(ctx, parent, engineMeasurementTestBinding(), 1, func(ctx context.Context, phase uint32, measure func(context.Context, EngineMeasurementIdentity) error) error {
			if phase != 5 {
				return ErrProtocol
			}
			held.Store(true)
			err := measure(ctx, engineMeasurementTestIdentity())
			close(finishing)
			select {
			case <-resume:
			case <-ctx.Done():
				err = errors.Join(err, ctx.Err())
			}
			held.Store(false)
			return err
		})
	}()
	client, err := newEngineMeasurementClient(ctx, ctx, child, engineMeasurementTestBinding(), 1)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- client.measure(ctx, 5, engineMeasurementTestIdentity(), func(operation context.Context) error {
			if operation != ctx || !held.Load() {
				return ErrProtocol
			}
			return nil
		})
	}()
	<-finishing
	select {
	case err := <-result:
		t.Fatal("release ACK preceded actual resume", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := client.close(ctx); !errors.Is(err, ErrBusy) {
		t.Fatal("active callback endpoint retired", err)
	}
	close(resume)
	if err := <-result; err != nil || held.Load() {
		t.Fatal("guard release", err)
	}
	if err := client.measure(ctx, 5, engineMeasurementTestIdentity(), func(context.Context) error { return nil }); !errors.Is(err, ErrLimit) {
		t.Fatal("finite point budget", err)
	}
	if err := client.close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
}

func TestEngineMeasurementFailedCallbackStillReleases(t *testing.T) {
	for _, mode := range []string{"error", "panic", "cancel", "identity", "lost-ack"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			operation, stop := context.WithCancel(ctx)
			defer stop()
			parent, child := archiveMeasurementTestPipe(t)
			var resumed, invoked atomic.Bool
			server := make(chan error, 1)
			go func() {
				server <- ServeEngineMeasurement(ctx, parent, engineMeasurementTestBinding(), 1, func(ctx context.Context, _ uint32, measure func(context.Context, EngineMeasurementIdentity) error) error {
					err := measure(ctx, engineMeasurementTestIdentity())
					resumed.Store(true)
					if mode == "lost-ack" {
						_ = parent.Close()
					}
					return err
				})
			}()
			client, err := newEngineMeasurementClient(ctx, ctx, child, engineMeasurementTestBinding(), 1)
			if err != nil {
				t.Fatal(err)
			}
			expected := engineMeasurementTestIdentity()
			if mode == "identity" {
				expected.Birth++
			}
			sentinel := errors.New("fixed measurement failure")
			err = client.measure(operation, 5, expected, func(context.Context) error {
				invoked.Store(true)
				switch mode {
				case "error":
					return sentinel
				case "panic":
					panic(sentinel)
				case "cancel":
					stop()
				}
				return nil
			})
			if err == nil || !resumed.Load() || mode == "error" && !errors.Is(err, sentinel) || mode == "panic" && !errors.Is(err, ErrPanic) || mode == "cancel" && !errors.Is(err, context.Canceled) || mode == "identity" && invoked.Load() {
				t.Fatal("failed callback became success or escaped release", err)
			}
			_ = client.close(ctx)
			serverErr := <-server
			if mode != "lost-ack" && serverErr != nil {
				t.Fatal("known child error corrupted parent release", serverErr)
			}
		})
	}
}

func TestEngineMeasurementGuardContainment(t *testing.T) {
	for _, mode := range []string{"skipped", "double", "escaped", "panic", "unbounded-context", "later-context", "shortened-context"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			parent, child := archiveMeasurementTestPipe(t)
			server, escaped := make(chan error, 1), make(chan error, 1)
			entered, ended := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(ended)
				server <- ServeEngineMeasurement(ctx, parent, engineMeasurementTestBinding(), 1, func(ctx context.Context, _ uint32, measure func(context.Context, EngineMeasurementIdentity) error) error {
					switch mode {
					case "skipped":
						return nil
					case "double":
						_ = measure(ctx, engineMeasurementTestIdentity())
						_ = measure(ctx, engineMeasurementTestIdentity())
						return nil
					case "escaped":
						go func() { escaped <- measure(ctx, engineMeasurementTestIdentity()) }()
						<-entered
						return nil
					case "panic":
						_ = measure(ctx, engineMeasurementTestIdentity())
						panic("fixed guard failure")
					case "unbounded-context":
						return measure(context.Background(), engineMeasurementTestIdentity())
					case "later-context":
						later, stop := context.WithTimeout(context.Background(), 2*time.Second)
						defer stop()
						return measure(later, engineMeasurementTestIdentity())
					default:
						shorter, stop := context.WithTimeout(ctx, 30*time.Millisecond)
						defer stop()
						return measure(shorter, engineMeasurementTestIdentity())
					}
				})
			}()
			client, err := newEngineMeasurementClient(ctx, ctx, child, engineMeasurementTestBinding(), 1)
			if err != nil {
				t.Fatal(err)
			}
			err = client.measure(ctx, 5, engineMeasurementTestIdentity(), func(context.Context) error {
				if mode == "escaped" {
					close(entered)
					<-ended
				}
				if mode == "shortened-context" {
					<-time.After(50 * time.Millisecond)
				}
				return nil
			})
			if err == nil {
				t.Fatal("invalid guard admitted")
			}
			if err := <-server; err == nil {
				t.Fatal("guard containment failure ignored")
			}
			if mode == "escaped" {
				if err := <-escaped; err == nil {
					t.Fatal("escaped callback succeeded")
				}
			}
		})
	}
}

func TestEngineMeasurementRejectsWire(t *testing.T) {
	for _, mode := range []string{"archive-magic", "reserved", "phase", "sequence", "release", "partial", "extra"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			parent, child := archiveMeasurementTestPipe(t)
			var calls atomic.Int32
			server := make(chan error, 1)
			go func() {
				server <- ServeEngineMeasurement(ctx, parent, engineMeasurementTestBinding(), 1, func(ctx context.Context, _ uint32, measure func(context.Context, EngineMeasurementIdentity) error) error {
					calls.Add(1)
					return measure(ctx, engineMeasurementTestIdentity())
				})
			}()
			client, err := newEngineMeasurementClient(ctx, ctx, child, engineMeasurementTestBinding(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "extra" {
				if err := client.measure(ctx, 5, engineMeasurementTestIdentity(), func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
			}
			frame := engineMeasurementFrame(engineMeasurementHold, 1, 5)
			switch mode {
			case "archive-magic":
				copy(frame[:4], "AM01")
			case "reserved":
				frame[6] = 1
			case "phase":
				frame = engineMeasurementFrame(engineMeasurementHold, 1, 6)
			case "sequence":
				frame = engineMeasurementFrame(engineMeasurementHold, 2, 5)
			case "release":
				frame[4] = engineMeasurementRelease
			}
			payload := frame[:]
			if mode == "partial" {
				payload = payload[:7]
			}
			if _, err := child.Write(payload); err != nil {
				t.Fatal(err)
			}
			_ = child.CloseWrite()
			if err := <-server; err == nil {
				t.Fatal("invalid frame admitted")
			}
			want := int32(0)
			if mode == "extra" {
				want = 1
			}
			if calls.Load() != want {
				t.Fatal("invalid frame entered guard", calls.Load())
			}
		})
	}
}

func TestEngineMeasurementControllerCurrentPhase(t *testing.T) {
	config := testConfig()
	config.Producers[0].ID = 3
	config.Phases[0].ID, config.Phases[1].ID = 5, 6
	controller, client, server := paired(t, config)
	// A real DA child admission/Wait synchronizes attach before inspecting phase.
	if err := client.Run(t.Context(), 1, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	if err := client.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if phase, err := controller.EngineMeasurementPhase(3); err != nil || phase != 5 {
		t.Fatal("paused producer phase", phase, err)
	}
	if _, err := controller.EngineMeasurementPhase(2); err == nil {
		t.Fatal("foreign producer admitted")
	}
	finishPair(t, controller, client, server)
	if _, err := controller.EngineMeasurementPhase(3); err == nil {
		t.Fatal("closed producer admitted")
	}
}

func TestEngineMeasurementProductionSelection(t *testing.T) {
	setPipedTestRuntime(t, nil)
	if selected, err := ProductionEngineMeasurement(t.Context(), EngineMeasurementIdentity{}, nil); selected || err != nil {
		t.Fatal("ordinary path selected")
	}
	lifetime := &ProductionLifetime{program: ProgramPhebs, semanticMode: ProductionSemanticV3}
	productionRuntime.Store(lifetime)
	if selected, err := ProductionEngineMeasurement(t.Context(), EngineMeasurementIdentity{}, nil); selected || err != nil {
		t.Fatal("untraced ordinary lifetime selected")
	}
	if selected, err := ProductionEngineMeasurement(t.Context(), engineMeasurementTestIdentity(), nil); !selected || err == nil {
		t.Fatal("traced missing endpoint fell back")
	}
	lifetime.engineMeasurements = 1
	if selected, err := ProductionEngineMeasurement(t.Context(), engineMeasurementTestIdentity(), nil); !selected || err == nil {
		t.Fatal("selected missing endpoint fell back")
	}
}

func TestEngineMeasurementHeaderMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	parent, child := archiveMeasurementTestPipe(t)
	server := make(chan error, 1)
	go func() {
		server <- ServeEngineMeasurement(ctx, parent, engineMeasurementTestBinding(), 1, func(context.Context, uint32, func(context.Context, EngineMeasurementIdentity) error) error {
			return nil
		})
	}()
	binding := engineMeasurementTestBinding()
	binding.InputSHA256[0]++
	if _, err := newEngineMeasurementClient(ctx, ctx, child, binding, 1); err == nil {
		t.Fatal("foreign input accepted")
	}
	_ = child.Close()
	if err := <-server; err == nil {
		t.Fatal("foreign header echoed")
	}
}

func TestEngineMeasurementCancellationJoinIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	parent, child := archiveMeasurementTestPipe(t)
	join := engineMeasurementCancellation(ctx, parent)
	join()
	join()
	cancel()
	join()
	if _, err := child.Write([]byte{9}); err != nil {
		t.Fatal(err)
	}
	var raw [1]byte
	if err := parent.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(parent, raw[:]); err != nil || raw[0] != 9 {
		t.Fatal("joined cancellation retired endpoint", err)
	}
}

func TestEngineMeasurementProductionBoundPhase(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	config := testConfig()
	config.Producers[0].ID = 3
	config.Phases[0].ID, config.Phases[1].ID = 5, 6
	controller, client, controlDone := paired(t, config)
	if err := client.Run(ctx, 1, exec.Command("/usr/bin/true")); err != nil {
		t.Fatal(err)
	}
	parent, child := archiveMeasurementTestPipe(t)
	server := make(chan error, 1)
	go func() {
		server <- ServeEngineMeasurement(ctx, parent, engineMeasurementTestBinding(), 1, func(ctx context.Context, phase uint32, measure func(context.Context, EngineMeasurementIdentity) error) error {
			actual, err := controller.EngineMeasurementPhase(3)
			if err != nil || actual != phase {
				return errors.Join(ErrProtocol, err)
			}
			return measure(ctx, engineMeasurementTestIdentity())
		})
	}()
	measurement, err := newEngineMeasurementClient(ctx, ctx, child, engineMeasurementTestBinding(), 1)
	if err != nil {
		t.Fatal(err)
	}
	setPipedTestRuntime(t, &ProductionLifetime{program: ProgramPhebs, semanticMode: ProductionSemanticV3, producerID: 3,
		client: client, engineMeasurements: 1, engineMeasurement: measurement})
	var calls int
	selected, err := ProductionEngineMeasurement(ctx, engineMeasurementTestIdentity(), func(operation context.Context) error {
		calls++
		if operation != ctx {
			return ErrProtocol
		}
		return nil
	})
	if !selected || err != nil || calls != 1 {
		t.Fatal("authenticated phase did not reach one measurement", selected, calls, err)
	}
	if err := measurement.close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
	finishPair(t, controller, client, controlDone)
}

// These are the six existing workspace points, not six additional walks:
// phase2 finish; phase3 start and finish; phase4 start, post-author and finish.
func TestEngineMeasurementSixExistingPoints(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	parent, child := archiveMeasurementTestPipe(t)
	binding := engineMeasurementTestBinding()
	binding.ProducerID = 2
	phases := []uint32{2, 3, 3, 4, 4, 4}
	var points atomic.Uint32
	server := make(chan error, 1)
	go func() {
		server <- ServeEngineMeasurement(ctx, parent, binding, 6, func(ctx context.Context, phase uint32, measure func(context.Context, EngineMeasurementIdentity) error) error {
			index := points.Add(1) - 1
			if index >= uint32(len(phases)) || phase != phases[index] {
				return ErrProtocol
			}
			return measure(ctx, engineMeasurementTestIdentity())
		})
	}()
	client, err := newEngineMeasurementClient(ctx, ctx, child, binding, 6)
	if err != nil {
		t.Fatal(err)
	}
	var measured uint32
	for _, phase := range phases {
		if err := client.measure(ctx, phase, engineMeasurementTestIdentity(), func(context.Context) error { measured++; return nil }); err != nil {
			t.Fatal("existing point refused", phase, err)
		}
	}
	if err := client.measure(ctx, 4, engineMeasurementTestIdentity(), func(context.Context) error { measured++; return nil }); !errors.Is(err, ErrLimit) {
		t.Fatal("seventh point admitted", err)
	}
	if measured != 6 || points.Load() != 6 {
		t.Fatal("points erased or added", measured, points.Load())
	}
	if err := client.close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
}
