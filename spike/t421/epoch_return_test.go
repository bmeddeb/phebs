package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// Source binding plus actual context behavior, not a successful native
// StartReturnA proof: admitted author/tool custody remains a separate gate.
func TestExecutionEpochReturnLaunchContextBinding(t *testing.T) {
	fset := token.NewFileSet()
	load := func(path, name string) *ast.FuncDecl {
		t.Helper()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == name {
				return function
			}
		}
		t.Fatalf("missing actual function %s", name)
		return nil
	}
	render := func(node ast.Node) string {
		t.Helper()
		var value bytes.Buffer
		if err := format.Node(&value, fset, node); err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(value.String()), " ")
	}
	start := load("epoch_return.go", "startReturnA")
	// Ordered statements bind the tested contexts to the actual constructor,
	// including the phase-six duration captured before the extended bounds.
	want := []string{
		"bounds, err := returnEpochBounds(flow.plan)",
		"phaseDuration := bounds.lifetime",
		"if stale { bounds, err = returnStaleEpochBounds(flow.plan) }",
		"started := time.Now()",
		"deadline, lifetimeDeadline := started.Add(phaseDuration), started.Add(bounds.lifetime)",
		"lifetime, cancel := context.WithDeadline(ctx, lifetimeDeadline)",
		"phaseContext, phaseCancel := context.WithDeadline(lifetime, deadline)",
		"defer phaseCancel()",
		"result, err := flow.launchEpoch(lifetime, phaseContext, cancel, next, bounds, 3)",
	}
	index := 0
	for _, statement := range start.Body.List {
		if index < len(want) && render(statement) == want[index] {
			index++
		}
	}
	if index != len(want) {
		t.Fatalf("return launch lifetime/phase binding missing or reordered at %q", want[index])
	}
	launch := load("epoch_launch.go", "launchEpoch")
	parameters := launch.Type.Params.List
	if len(parameters) == 0 || len(parameters[0].Names) != 2 || parameters[0].Names[0].Name != "runCtx" || parameters[0].Names[1].Name != "launchCtx" {
		t.Fatal("actual launch signature changed its runtime/bootstrap context order")
	}
	seen := map[string]int{}
	ast.Inspect(launch.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		for name, argument := range map[string]string{"run.finish": "runCtx", "dispatchadmission.SendProductionBootstrap": "launchCtx", "flow.parent.StartInPhase": "launchCtx", "writeAuthorCustodyRequest": "launchCtx"} {
			if render(call.Fun) == name {
				if len(call.Args) == 0 || render(call.Args[0]) != argument {
					t.Errorf("%s must use %s", name, argument)
				}
				seen[name]++
			}
		}
		return true
	})
	for _, name := range []string{"run.finish", "dispatchadmission.SendProductionBootstrap", "flow.parent.StartInPhase", "writeAuthorCustodyRequest"} {
		if seen[name] != 1 {
			t.Fatalf("actual %s call not uniquely covered", name)
		}
	}
}

func TestExecutionEpochReturnLaunchContextLifetime(t *testing.T) {
	for _, mode := range []string{"return_only", "return_stale"} {
		for _, point := range []string{"successful_return", "bootstrap_deadline"} {
			t.Run(mode+"/"+point, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					plan := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
					bounds, err := returnEpochBounds(plan)
					phaseDuration := bounds.lifetime
					if mode == "return_stale" {
						bounds, err = returnStaleEpochBounds(plan)
					}
					if err != nil {
						t.Fatal(err)
					}
					started := time.Now()
					deadline, lifetimeDeadline := started.Add(phaseDuration), started.Add(bounds.lifetime)
					lifetime, cancel := context.WithDeadline(t.Context(), lifetimeDeadline)
					defer cancel()
					phaseContext, phaseCancel := context.WithDeadline(lifetime, deadline)
					defer phaseCancel()
					launchDeadline, _ := phaseContext.Deadline()
					runDeadline, _ := lifetime.Deadline()
					if launchDeadline.Sub(started) != 4*time.Hour || runDeadline.Sub(started) != bounds.lifetime {
						t.Fatal("bootstrap borrowed stale-phase time")
					}
					if point == "successful_return" {
						// Model startReturnA's successful return defer, after the
						// source-bound launch has selected its independent runCtx.
						func() { defer phaseCancel() }()
						if phaseContext.Err() != context.Canceled || lifetime.Err() != nil {
							t.Fatal("successful launch-context release canceled server lifetime")
						}
					} else {
						<-phaseContext.Done()
						synctest.Wait() // Join same-deadline parent and child timer callbacks.
						if time.Since(started) != 4*time.Hour || phaseContext.Err() != context.DeadlineExceeded || mode == "return_stale" && lifetime.Err() != nil || mode == "return_only" && lifetime.Err() != context.DeadlineExceeded {
							t.Fatal("bootstrap/runtime deadline relation changed")
						}
					}
					cancel()
					if lifetime.Err() == nil {
						t.Fatal("explicit server stop lost lifetime cancellation")
					}
				})
			})
		}
	}
}

func TestExecutionEpochReturnBounds(t *testing.T) {
	for _, mode := range []string{"valid", "v2", "deadline", "health", "missing"} {
		t.Run(mode, func(t *testing.T) {
			plan := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
			switch mode {
			case "v2":
				plan.Schema = PlanV2Schema
			case "deadline":
				plan.PhaseDeadlines[5].DeadlineMS++
			case "health":
				plan.SafetyEnvelope.ServerHealthDeadlineMS++
			case "missing":
				plan.PhaseDeadlines = nil
			}
			bounds, err := returnEpochBounds(plan)
			if (err == nil) != (mode == "valid") {
				t.Fatal("changed contract accepted", err)
			}
			if err == nil && (bounds.lifetime != 4*time.Hour || bounds.health != 15*time.Minute || bounds.controlPairs != 5 || bounds.outputBytes != 64<<20) {
				t.Fatal(bounds)
			}
		})
	}
}

func TestExecutionEpochReturnAdmissionRefusal(t *testing.T) {
	for _, mode := range []string{"unfinished", "stopping", "stopped", "expired", "used", "canceled", "wrong_epoch"} {
		t.Run(mode, func(t *testing.T) {
			flow := &ExecutionEpochOne{plan: Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}}
			run := &ExecutionEpochOneRun{flow: flow, epoch: ExecutionEpochConfig{Epoch: 2}, stop: make(chan struct{}), logicalUsed: true,
				logicalDone: make(chan struct{}), inspection: &executionEpochInspection{}, phaseDeadline: time.Now().Add(time.Hour)}
			if mode != "unfinished" {
				close(run.logicalDone)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "stopping":
				run.stopping = true
			case "stopped":
				close(run.stop)
			case "expired":
				run.phaseDeadline = time.Now().Add(-time.Second)
			case "used":
				flow.returnUsed = true
			case "canceled":
				cancel()
			case "wrong_epoch":
				run.epoch.Epoch = 1
			}
			if next, err := run.StartReturnA(ctx); next != nil || err == nil || run.returnStarting || run.retainParent || flow.retained != nil {
				t.Fatal("invalid return acquired custody", err)
			}
		})
	}
}

func TestExecutionEpochReturnCloseRefusesAuthorGap(t *testing.T) {
	for _, mode := range []string{"author_active", "handoff_active"} {
		t.Run(mode, func(t *testing.T) {
			run := &ExecutionEpochOneRun{done: make(chan struct{}), result: ExecutionEpochOneResult{RootJoined: true, SessionEmpty: true}, returnStarting: mode == "handoff_active"}
			close(run.done)
			author := &ExecutionAuthorCustody{borrowedBy: run, active: mode == "author_active"}
			epochs := &ExecutionEpochConfigCustody{author: author, active: true}
			flow := &ExecutionEpochOne{epochs: epochs, used: true, retained: run, release: func() { t.Error("released active source") }}
			if flow.Close() == nil || flow.closed || author.borrowedBy != run || !epochs.active {
				t.Fatal("active return gap released custody")
			}
		})
	}
}

// No author is launched: this isolates Stop's required cancellation/join of
// the already-joined predecessor's still-running author operation.
func TestExecutionEpochReturnStopJoinsAuthorOperation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{}, stop: make(chan struct{}), done: make(chan struct{}),
		returnStartCancel: cancel, returnStartDone: make(chan struct{})}
	close(run.done)
	joined := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(joined)
		close(run.returnStartDone)
	}()
	if _, err := run.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("Stop returned before author joined")
	}
}

func TestExecutionEpochReturnCanceledAtRetainedJoin(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	completed := make(chan struct{})
	close(completed)
	author := &ExecutionAuthorCustody{}
	epochs := &ExecutionEpochConfigCustody{author: author, active: true}
	flow := &ExecutionEpochOne{epochs: epochs, used: true, plan: Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}, release: func() {}}
	run := &ExecutionEpochOneRun{flow: flow, epoch: ExecutionEpochConfig{Epoch: 2}, stop: make(chan struct{}), done: make(chan struct{}), logicalUsed: true, logicalDone: completed,
		inspection: &executionEpochInspection{finalUsed: true, logicalAuthority: AuthorityPhaseResult{Phase: "logical_delta_b"}}, phaseDeadline: time.Now().Add(time.Hour)}
	author.borrowedBy = run
	joined := make(chan struct{})
	go func() {
		<-run.stop
		run.mu.Lock()
		run.result = ExecutionEpochOneResult{RootJoined: true, SessionEmpty: true}
		run.mu.Unlock()
		cancel()
		close(run.done)
		close(joined)
	}()
	if next, err := run.StartReturnA(ctx); next != nil || err == nil {
		t.Fatal("canceled handoff launched", err)
	}
	<-joined
	if !flow.returnUsed || flow.retained != run || author.borrowedBy != run || !epochs.active || run.returnStarting || run.err == nil {
		t.Fatal("failed handoff lost custody or sticky failure")
	}
	if flow.Close() != nil || author.borrowedBy != nil || epochs.active {
		t.Fatal("joined canceled gap could not be explicitly abandoned")
	}
}

func TestExecutionEpochReturnDeadlineIncludesHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		completed := make(chan struct{})
		close(completed)
		flow := &ExecutionEpochOne{plan: Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}}
		run := &ExecutionEpochOneRun{flow: flow, epoch: ExecutionEpochConfig{Epoch: 2}, stop: make(chan struct{}), done: make(chan struct{}),
			logicalUsed: true, logicalDone: completed, inspection: &executionEpochInspection{}, phaseDeadline: time.Now().Add(8 * time.Hour)}
		start := time.Now()
		if next, err := run.StartReturnA(t.Context()); err == nil || next != nil || time.Since(start) != 4*time.Hour {
			t.Fatal("predecessor handoff escaped phase-six deadline", time.Since(start), err)
		}
		if !run.retainParent || flow.retained != run || run.returnStarting || run.err == nil {
			t.Fatal("deadline discarded retained failed prefix")
		}
	})
}

func TestExecutionAuthorReturnBorrowGuard(t *testing.T) {
	for _, mode := range []string{"exact_guard", "public", "wrong_run", "wrong_epoch", "wrong_revision", "wrong_producer", "active"} {
		t.Run(mode, func(t *testing.T) {
			run := &ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Epoch: 2}}
			custody := &ExecutionAuthorCustody{borrowedBy: run, next: 2,
				expected: [3]AuthoredExecutionRevision{{Name: "a"}, {Name: "b"}, {Name: "a-return"}}}
			custody.deadlines[2] = time.Hour
			borrower, producer := run, uint32(9)
			switch mode {
			case "public":
				borrower = nil
			case "wrong_run":
				borrower = &ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Epoch: 2}}
			case "wrong_epoch":
				run.epoch.Epoch = 1
			case "wrong_revision":
				custody.next = 1
			case "wrong_producer":
				producer = 8
			case "active":
				custody.active = true
			}
			// Only the exact private tuple reaches the intentionally absent
			// custody roots. No test author or source mutation is supplied.
			result, err := custody.authorNext(t.Context(), nil, nil, producer, borrower)
			if err == nil || result.RootStarted || (custody.err != nil) != (mode == "exact_guard") || custody.borrowedBy != run {
				t.Fatal("return borrow guard bypassed or did not reach custody validation", err)
			}
		})
	}
}

func TestEpochReturnMarkerNativeWire(t *testing.T) {
	for _, mode := range []string{"valid", "prior", "same_target", "changed_target", "changed_authority", "changed_plan", "cost", "recovered_first", "after_x", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				point := relationshippublication.PublicationTransitionHitV3
				if calls == 2 {
					point = relationshippublication.PublicationTransitionRecoveredV3
				}
				// The existing native transition reader returns the SAME prior
				// and target identities at hit/recovered; only Point changes.
				native := relationshippublication.PublicationTransitionSnapshotV3{Point: point,
					PriorGenerationDigest: testDigest("prior-gen"), PriorRootDigest: testDigest("prior-root"),
					TargetGenerationDigest: testDigest("target-gen"), TargetRootDigest: testDigest("target-root"), TargetAuthorityDigest: testDigest("authority")}
				value := epochMarkerObservation{Schema: "t422-relationship-marker-observation-v3", Point: string(native.Point), PlanDigest: testDigest("plan"), ScheduleDigest: testDigest("schedule"),
					PriorGenerationDigest: native.PriorGenerationDigest, PriorRootDigest: native.PriorRootDigest,
					TargetGenerationDigest: native.TargetGenerationDigest, TargetRootDigest: native.TargetRootDigest, TargetAuthorityDigest: native.TargetAuthorityDigest}
				if mode == "prior" {
					value.PriorRootDigest = testDigest("wrong-prior")
				}
				if mode == "same_target" {
					value.TargetRootDigest = value.PriorRootDigest
				}
				if calls == 2 {
					switch mode {
					case "changed_target":
						value.TargetGenerationDigest = testDigest("third")
					case "changed_authority":
						value.TargetAuthorityDigest = testDigest("third")
					case "changed_plan":
						value.PlanDigest = testDigest("third")
					}
				}
				ordinal, _ := strconv.ParseUint(r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", RequestOrdinal: ordinal, Status: "complete", ControlFileReads: 5}
				if mode == "cost" {
					report.ControlFileReads = 4
				}
				w.Header().Add("Trailer", epochReadTrailer)
				_, _ = w.Write(epochTestJSON(t, value, false))
				raw, _ := json.Marshal(report)
				w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(raw))
			}))
			reader.projection.Phase = "return_a"
			reader.logicalAuthority.RelationshipGenerationSHA256, reader.logicalAuthority.RelationshipRootSHA256 = testDigest("prior-gen"), testDigest("prior-root")
			if mode == "recovered_first" || mode == "after_x" {
				point := "recovered"
				if mode == "after_x" {
					reader.progressCalls, point = 1, "hit"
				}
				if reader.marker(t.Context(), point) == nil || calls != 0 {
					t.Fatal("wrong order issued native request")
				}
				return
			}
			err := reader.marker(t.Context(), "hit")
			if mode == "prior" || mode == "same_target" || mode == "cost" {
				if err == nil || calls != 1 {
					t.Fatal("invalid hit accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "duplicate" {
				if reader.marker(t.Context(), "hit") == nil || calls != 1 {
					t.Fatal("duplicate consumed second read")
				}
				return
			}
			err = reader.marker(t.Context(), "recovered")
			if (err == nil) != (mode == "valid") || calls != 2 {
				t.Fatal("changed transition accepted", err)
			}
		})
	}
}

func epochReturnTestFinal(t *testing.T) (*executionEpochInspection, epochFinalResponse) {
	t.Helper()
	reader, value := epochPhysicalTestFinal(t)
	physical, _, err := reader.decodeFinal(epochTestJSON(t, value, true))
	if err != nil {
		t.Fatal(err)
	}
	reader.physicalAuthority = physical
	logical := physical
	logical.Phase, logical.LogicalRevision = "logical_delta_b", "b"
	for _, field := range []string{"CatalogRootSHA256", "CatalogActivationPlanSHA256", "CatalogActivationScheduleSHA256", "CatalogActivationUnitSHA256", "RelationshipGenerationSHA256", "RelationshipRootSHA256"} {
		reflect.ValueOf(&logical.AuthorityState).Elem().FieldByName(field).SetString(testDigest("logical", field))
	}
	reader.logicalAuthority = logical
	reader.projection, err = expectedStateProjectionForPhase(reader.plan, "return_a")
	if err != nil {
		t.Fatal(err)
	}
	revision := reader.plan.Revisions.Physical[2]
	reader.authored = AuthoredExecutionRevision{Name: "a-return", Commit: revision.ExpectedCommit, Tree: revision.ExpectedTree}
	state := logical.AuthorityState
	state.PhysicalCommit, state.PhysicalTree = revision.ExpectedCommit, revision.ExpectedTree
	state.SearchInventory, state.ObservationInputInventory = revision.ExpectedTreeInventory, revision.ExpectedObservationInputInventory
	v, typ := reflect.ValueOf(&state).Elem(), reflect.TypeOf(state)
	for index := 0; index < v.NumField(); index++ {
		if v.Field(index).Kind() == reflect.String && strings.HasSuffix(typ.Field(index).Name, "SHA256") {
			v.Field(index).SetString(testDigest("return", typ.Field(index).Name))
		}
	}
	roots := testExtractionRoots(t, reader.plan, revision, state, "return")
	for index := range roots {
		roots[index].ScheduleSHA256 = ""
		roots[index].Members, err = extractionResultMembers(roots[index].PartitionResults)
		if err != nil {
			t.Fatal(err)
		}
	}
	state.ExtractionRootsSHA256 = mustReceiptSHA256(t, roots)
	value.ExtractionRoots = roots
	raw, _ := json.Marshal(state)
	if json.Unmarshal(raw, &value.Authority) != nil {
		t.Fatal("authority conversion")
	}
	raw, _ = json.Marshal(reader.projection)
	if json.Unmarshal(raw, &value.Projection) != nil {
		t.Fatal("projection conversion")
	}
	value.Projection.Schema = "t421-final-state-projection-source-free-v1"
	reader.tail = epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: state.RelationshipGenerationSHA256, RelationshipRootSHA256: state.RelationshipRootSHA256,
		CallerGenerationSHA256: state.CallerGenerationSHA256, CallerRootSHA256: state.CallerRootSHA256}
	reader.markerRecovered = epochMarkerObservation{Schema: "t422-relationship-marker-observation-v3", TargetGenerationDigest: state.RelationshipGenerationSHA256, TargetRootDigest: state.RelationshipRootSHA256}
	return reader, value
}

func TestEpochReturnFinalContinuity(t *testing.T) {
	reader, value := epochReturnTestFinal(t)
	logical := reader.logicalAuthority
	if _, _, err := reader.decodeFinal(epochTestJSON(t, value, true)); err != nil {
		t.Fatal("valid return model", err)
	}
	for _, field := range []string{"SearchGenerationSHA256", "CatalogRootSHA256", "CallerGenerationSHA256", "RelationshipProvenanceSHA256", "PhysicalTree", "RelationshipRootSHA256"} {
		t.Run(field, func(t *testing.T) {
			bad := value
			reflect.ValueOf(&bad.Authority).Elem().FieldByName(field).Set(reflect.ValueOf(logical.AuthorityState).FieldByName(field))
			if _, _, err := reader.decodeFinal(epochTestJSON(t, bad, true)); err == nil {
				t.Fatal("unchanged return authority accepted")
			}
		})
	}
	reader.markerRecovered.TargetRootDigest = testDigest("other-target")
	if _, _, err := reader.decodeFinal(epochTestJSON(t, value, true)); err == nil {
		t.Fatal("F did not bind recovered marker root")
	}
}

func TestExecutionEpochReturnClosedPrefix(t *testing.T) {
	for _, mode := range []string{"valid", "root_open", "author_active", "server_active", "missing_eof", "survivor"} {
		t.Run(mode, func(t *testing.T) {
			result := ExecutionEpochOneResult{RootStarted: true, RootJoined: true, SessionEmpty: true, Store: storeaccounting.WireSnapshot{Opened: 3, TerminalEOF: 3}}
			for _, id := range []uint32{1, 2, 3, 4, 7, 8, 9} {
				count := dispatchadmission.ProducerCount{Producer: id, Attached: true, Closed: true}
				if id == 1 {
					count.Ordinal = 6
				} else if id >= 7 {
					count.Ordinal = authorCustodyAttempts(int(id - 7))
				}
				result.Accounting.Producers = append(result.Accounting.Producers, count)
			}
			for _, id := range []uint32{2, 3, 4} {
				result.Store.Store.Producers = append(result.Store.Store.Producers, storeaccounting.ProducerCount{Producer: id, Attached: true, Closed: true})
			}
			switch mode {
			case "root_open":
				result.Accounting.Producers[0].Closed = false
			case "author_active":
				result.Accounting.Producers[6].Active = 1
			case "server_active":
				result.Store.Store.Producers[2].Calls = 1
			case "missing_eof":
				result.Store.TerminalEOF--
			case "survivor":
				result.SessionEmpty = false
			}
			if epochReturnClosedPrefix(t.Context(), result) != (mode == "valid") {
				t.Fatal("wrong closed prefix")
			}
		})
	}
}
