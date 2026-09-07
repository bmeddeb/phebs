//go:build darwin || linux

package dispatchadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

func terminalPhaseTestConfig() PhaseControlConfig {
	return PhaseControlConfig{OwnerControl: true, TerminalPhase: 8, Phases: []uint32{6, 7, 8}, InitialPhase: 6,
		MaximumPhases: 3, MaximumWireBytes: 21 * 2 * FrameBytes, Timeout: time.Second}
}

func terminalPhaseTestRecord() ProductionBootstrap {
	record := productionStoreTestRecord()
	record.Producer.ID, record.Phase, record.Limits.Phases = 4, 6, 3
	record.Control = terminalPhaseTestConfig()
	record.SemanticMode, record.InputSHA256 = ProductionSemanticV3, [32]byte{7}
	record.Store.Producer, record.Store.Phase, record.Store.Phases = 4, 6, 224
	record.Store.Calls, record.Store.Transactions = 40, 2
	return record
}

func TestTerminalPhaseBootstrapAndLegacyOmission(t *testing.T) {
	record := terminalPhaseTestRecord()
	if err := record.validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(*ProductionBootstrap)
	}{
		{"phase", func(r *ProductionBootstrap) { r.Control.TerminalPhase = 7 }},
		{"producer", func(r *ProductionBootstrap) { r.Producer.ID = 5 }},
		{"program", func(r *ProductionBootstrap) { r.Program = ProgramCorpusAuthor }},
		{"author", func(r *ProductionBootstrap) { r.Control.TerminalAuthor = true }},
		{"legacy", func(r *ProductionBootstrap) { r.SemanticMode = "" }},
		{"digest", func(r *ProductionBootstrap) { r.InputSHA256 = [32]byte{} }},
		{"store", func(r *ProductionBootstrap) { r.Store = nil }},
		{"initial", func(r *ProductionBootstrap) { r.Control.InitialPhase = 7 }},
		{"owners", func(r *ProductionBootstrap) { r.Control.OwnerControl = false }},
		{"phases", func(r *ProductionBootstrap) { r.Control.Phases = []uint32{6, 8} }},
		{"cap", func(r *ProductionBootstrap) { r.Control.MaximumPhases = 4 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := terminalPhaseTestRecord()
			test.edit(&record)
			if err := record.validate(); !errors.Is(err, ErrProductionBootstrap) {
				t.Fatal("unbound terminal mode admitted", err)
			}
		})
	}
	legacy := productionTestRecord()
	raw, err := json.Marshal(legacy)
	if err != nil || bytes.Contains(raw, []byte("TerminalPhase")) || legacy.validate() != nil {
		t.Fatal("legacy bytes/config changed", err)
	}
	config := terminalPhaseTestConfig()
	for _, index := range []int{0, 1} {
		if _, _, err := nextConfiguredControlState(0, index, phaseTerminalQuiesce, config); err == nil {
			t.Fatal("early terminal phase admitted")
		}
	}
	for _, state := range []byte{phaseTerminalQuiesce, phaseTerminalCheckpoint} {
		for op := phasePause; op <= phaseTerminalQuiesce; op++ {
			_, _, err := nextConfiguredControlState(state, 2, op, config)
			if (err == nil) != (state == phaseTerminalQuiesce && op == phaseCheckpoint) {
				t.Fatal("terminal control reopened", state, op, err)
			}
		}
	}
}

type terminalPhaseFixture struct {
	ctx       context.Context
	dispatch  *Controller
	client    *Client
	control   *PhaseControl
	owners    *Owners
	lifetime  *ProductionLifetime
	store     *storeaccounting.Controller
	transport *storeaccounting.Transport
	owner     *storeaccounting.SDKOwner
}

// Genuine PC/DA/SA sockets and SDK wrapper, but no admitted executable, native
// held-claim callback, heartbeat, owning kill or same-phase successor proof.
func newTerminalPhaseFixture(t *testing.T, quiesce func(context.Context) error) *terminalPhaseFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	config := testConfig()
	config.Producers[0].ID, config.Limits.Phases = 4, 3
	config.Phases[0].ID, config.Phases[1].ID = 6, 7
	config.Phases = append(config.Phases, Phase{ID: 8, Roles: config.Phases[1].Roles})
	dispatch, client, _ := paired(t, config)
	lifetime, store, transport := storePhaseLifetimeFor(t, client, 4, []storeaccounting.Phase{
		{ID: 6, Transactions: 2, Rows: 4}, {ID: 7, Transactions: 2, Rows: 4}, {ID: 8, Transactions: 2, Rows: 4}}, 224)
	lifetime.program, lifetime.semanticMode, lifetime.producerID, lifetime.inputSHA256 = ProgramPhebs, ProductionSemanticV3, 4, [32]byte{7}
	owner, err := lifetime.TakeStoreOwner()
	if err != nil {
		t.Fatal(err)
	}
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	control, err := NewPhaseControl(ctx, parent, client.binding, terminalPhaseTestConfig())
	if err != nil {
		_ = child.Close()
		t.Fatal(err)
	}
	done, err := StartPhaseControl(ctx, child, client, terminalPhaseTestConfig())
	if err != nil {
		_ = control.Close()
		t.Fatal(err)
	}
	lifetime.controlDone = done
	t.Cleanup(func() { _ = control.Close(); _ = phaseTestResult(t, done) })
	owners, err := NewOwners(ctx, OwnerLimits{Owners: 2, Requests: 1})
	if err != nil || client.bindOwners(owners) != nil {
		t.Fatal("owners not bound", err)
	}
	prior := productionRuntime.Swap(lifetime)
	t.Cleanup(func() { productionRuntime.Store(prior) })
	if phase, err := ProductionTerminalPhase(); phase != 8 || err != nil {
		t.Fatal("terminal mode missing", phase, err)
	}
	if quiesce != nil {
		if err := BindProductionTerminalQuiescence(quiesce); err != nil {
			t.Fatal(err)
		}
	}
	return &terminalPhaseFixture{ctx: ctx, dispatch: dispatch, client: client, control: control, owners: owners,
		lifetime: lifetime, store: store, transport: transport, owner: owner}
}

func (fixture *terminalPhaseFixture) advance(t *testing.T) {
	t.Helper()
	for range 2 {
		for _, op := range []func() error{
			func() error { return fixture.control.DrainOwners(fixture.ctx) }, func() error { return fixture.control.Pause(fixture.ctx) },
			fixture.dispatch.Fence, fixture.transport.Fence, func() error { return fixture.control.Checkpoint(fixture.ctx) },
			fixture.dispatch.Advance, fixture.transport.Advance, func() error { return fixture.control.Resume(fixture.ctx) },
			func() error { return fixture.control.ReopenOwners(fixture.ctx) },
		} {
			if err := op(); err != nil {
				t.Fatal("ordinary handoff failed", err)
			}
		}
	}
}

func TestTerminalPhaseQuiesceThenGenuineSDKCheckpoint(t *testing.T) {
	var held OwnerTurn
	entered, released := make(chan struct{}), make(chan struct{})
	fixture := newTerminalPhaseFixture(t, func(ctx context.Context) error {
		close(entered)
		if err := held.FenceTerminal(ctx); err != nil {
			return err
		}
		close(released)
		return nil
	})
	fixture.advance(t)
	var err error
	held, err = fixture.owners.Enter(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := fixture.client.enterOwnerRequest(fixture.ctx, fixture.owners, fixture.control.RequestToken())
	if err != nil {
		t.Fatal(err)
	}
	db, _, _ := phaseStoreDB(t, fixture.ctx, fixture.owner, nil, false)
	if _, err := storeaccounting.SDKQuery[[]int](fixture.ctx, fixture.owner, db, "write", nil, storeaccounting.SDKWrite(2)); err != nil {
		t.Fatal(err)
	}
	quiesced := make(chan error, 1)
	go func() { quiesced <- fixture.control.TerminalQuiesce(fixture.ctx) }()
	select {
	case <-entered:
	case <-fixture.ctx.Done():
		t.Fatal("terminal callback missing")
	}
	if fixture.client.ownerRequestAllowed(fixture.control.RequestToken()) {
		t.Fatal("terminal callback ran before closing request entry")
	}
	select {
	case <-released:
		t.Fatal("request tail did not hold quiescence")
	default:
	}
	tail.End()
	if err := <-quiesced; err != nil {
		t.Fatal(err)
	}
	state, err := ProductionSemanticState()
	if err != nil || state.OrdinaryOwnersDrained || fixture.control.RequestToken() != "" {
		t.Fatal("retained owner relabeled ordinary drainage", state, err)
	}
	before, err := fixture.store.Snapshot()
	if err != nil || before.Producers[0].Checkpoint != 7 || before.Transactions != 1 || before.Rows != 2 {
		t.Fatal("quiescence fabricated SDK checkpoint", before, err)
	}
	if fixture.dispatch.Fence() != nil || fixture.transport.Fence() != nil || fixture.control.Checkpoint(fixture.ctx) != nil {
		t.Fatal("genuine terminal SDK checkpoint failed")
	}
	after, err := fixture.store.Snapshot()
	dispatch, dispatchErr := fixture.dispatch.Snapshot()
	if err != nil || dispatchErr != nil || after.Producers[0].Checkpoint != 8 || dispatch.Producers[0].Checkpoint != 8 ||
		after.Complete || dispatch.Complete || after.Transactions != 1 || after.Rows != 2 || fixture.control.ReservedWireBytes() != 12*2*FrameBytes {
		t.Fatal("checkpoint lost prefix or fabricated closure", after, dispatch, err, dispatchErr)
	}
	if err := fixture.client.Resume(9); err == nil {
		t.Fatal("retiring client resumed")
	}
}

func TestTerminalPhaseRefusesCallbackAndReadUncertainty(t *testing.T) {
	for _, mode := range []string{"missing", "callback", "panic", "cancel", "readTail", "failedRead", "uuid"} {
		t.Run(mode, func(t *testing.T) {
			var callback func(context.Context) error
			if mode != "missing" {
				callback = func(ctx context.Context) error {
					switch mode {
					case "callback":
						return ErrIncomplete
					case "panic":
						panic("private test callback")
					case "cancel":
						<-ctx.Done()
						return ctx.Err()
					}
					return nil // No owner/native readiness claim in these refusal fixtures.
				}
			}
			fixture := newTerminalPhaseFixture(t, callback)
			fixture.advance(t)
			var gate *phaseStoreDecodeGate
			var readDone chan error
			if mode == "readTail" || mode == "failedRead" {
				gate = &phaseStoreDecodeGate{Codec: surrealcbor.New(), ctx: fixture.client.Context(), entered: make(chan struct{}), release: make(chan struct{})}
			}
			if gate != nil || mode == "uuid" {
				db, _, _ := phaseStoreDB(t, fixture.ctx, fixture.owner, gate, false)
				if mode == "uuid" {
					if _, err := storeaccounting.SDKBegin(fixture.ctx, fixture.owner, db); err != nil {
						t.Fatal(err)
					}
				} else {
					readCtx, cancel := context.WithCancel(fixture.ctx)
					defer cancel()
					if mode == "failedRead" {
						gate.ctx = readCtx
					}
					readDone = make(chan error, 1)
					go func() {
						_, err := storeaccounting.SDKQuery[[]int](readCtx, fixture.owner, db, "read", nil, storeaccounting.SDKRead())
						readDone <- err
					}()
					select {
					case <-gate.entered:
					case <-fixture.ctx.Done():
						t.Fatal("read not reached")
					}
					if mode == "failedRead" {
						cancel()
						if err := <-readDone; err == nil {
							t.Fatal("canceled native read succeeded")
						}
						readDone = nil
					}
				}
			}
			ctx, cancel := context.WithTimeout(fixture.ctx, 50*time.Millisecond)
			defer cancel()
			err := fixture.control.TerminalQuiesce(ctx)
			if mode == "readTail" || mode == "failedRead" || mode == "uuid" {
				storeFence := fixture.transport.Fence()
				if err != nil || fixture.dispatch.Fence() != nil || mode != "failedRead" && storeFence != nil {
					t.Fatal("quiesce before SDK refusal failed", err)
				}
				err = fixture.control.Checkpoint(fixture.ctx)
			}
			if err == nil {
				t.Fatal("uncertain terminal operation acknowledged")
			}
			if readDone != nil {
				select {
				case err := <-readDone:
					if err == nil {
						t.Fatal("unsettled typed read succeeded")
					}
				case <-fixture.ctx.Done():
					t.Fatal("failed read did not join")
				}
			}
			prefix, _ := fixture.store.Snapshot()
			dispatch, _ := fixture.dispatch.Snapshot()
			if prefix.Producers[0].Checkpoint != 7 || dispatch.Producers[0].Checkpoint != 7 || prefix.Complete || dispatch.Complete {
				t.Fatal("failed terminal PC advanced old checkpoints", prefix, dispatch)
			}
		})
	}
}

func TestTerminalPhaseBindingIsSingleAndSelected(t *testing.T) {
	prior := productionRuntime.Swap(nil)
	t.Cleanup(func() { productionRuntime.Store(prior) })
	if phase, err := ProductionTerminalPhase(); err != nil || phase != 0 || BindProductionTerminalQuiescence(func(context.Context) error { return nil }) == nil {
		t.Fatal("ordinary process supplied terminal authority", phase, err)
	}
	for _, mode := range []string{"nil", "duplicate", "unselected", "owners", "phase", "canceled", "program", "producer", "input", "store"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newTerminalPhaseFixture(t, nil)
			callback := func(context.Context) error { return nil }
			switch mode {
			case "nil":
				callback = nil
			case "duplicate":
				if err := BindProductionTerminalQuiescence(callback); err != nil {
					t.Fatal(err)
				}
			case "unselected":
				fixture.client.mu.Lock()
				fixture.client.controlTerminalPhase = 0
				fixture.client.mu.Unlock()
			case "owners":
				fixture.client.mu.Lock()
				fixture.client.owners = nil
				fixture.client.mu.Unlock()
			case "phase":
				fixture.advance(t)
			case "canceled":
				_ = fixture.client.fail(ErrCanceled)
				if _, err := ProductionTerminalPhase(); err == nil {
					t.Fatal("failed selected mode fell back")
				}
			case "program":
				fixture.lifetime.program = ProgramCorpusAuthor
			case "producer":
				fixture.lifetime.producerID = 5
			case "input":
				fixture.lifetime.inputSHA256 = [32]byte{}
			case "store":
				fixture.lifetime.storeClient = nil
			}
			if err := BindProductionTerminalQuiescence(callback); err == nil {
				t.Fatal("unbound/repeated callback admitted")
			}
		})
	}
}

func TestTerminalPhaseLostACKAndMalformedRequest(t *testing.T) {
	for _, mode := range []string{"lostACK", "binding", "sequence", "phase", "partial"} {
		t.Run(mode, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			fixture := newTerminalPhaseFixture(t, func(context.Context) error {
				close(entered)
				<-release
				return nil
			})
			fixture.advance(t)
			frame := phaseControlFrame{op: phaseTerminalQuiesce, phase: 8, sequence: fixture.control.sequence + 1, binding: fixture.client.binding}
			switch mode {
			case "binding":
				frame.binding[0]++
			case "sequence":
				frame.sequence--
			case "phase":
				frame.phase = 7
			}
			raw := frame.encode()
			request := raw[:]
			if mode == "partial" {
				request = raw[:3]
			}
			if _, err := fixture.control.conn.Write(request); err != nil {
				t.Fatal(err)
			}
			if mode == "lostACK" {
				select {
				case <-entered:
				case <-fixture.ctx.Done():
					t.Fatal("terminal callback not entered")
				}
				if err := fixture.control.conn.Close(); err != nil {
					t.Fatal(err)
				}
				close(release)
			} else {
				close(release)
				if err := fixture.control.conn.CloseWrite(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-fixture.client.Context().Done():
			case <-fixture.ctx.Done():
				t.Fatal("bad terminal control did not fail client")
			}
			if err := fixture.control.Checkpoint(fixture.ctx); err == nil {
				t.Fatal("missing terminal ACK continued to checkpoint")
			}
			dispatch, _ := fixture.dispatch.Snapshot()
			prefix, _ := fixture.store.Snapshot()
			if dispatch.Producers[0].Checkpoint != 7 || prefix.Producers[0].Checkpoint != 7 || dispatch.Complete || prefix.Complete {
				t.Fatal("uncertain terminal command advanced checkpoint")
			}
			if mode != "lostACK" {
				select {
				case <-entered:
					t.Fatal("invalid frame invoked terminal callback")
				default:
				}
			}
		})
	}
}
