package t421

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestExecutionEpochLogicalBounds(t *testing.T) {
	for _, mode := range []string{"valid", "v2", "deadline", "health", "missing"} {
		t.Run(mode, func(t *testing.T) {
			plan := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
			switch mode {
			case "v2":
				plan.Schema = PlanV2Schema
			case "deadline":
				plan.PhaseDeadlines[4].DeadlineMS++
			case "health":
				plan.SafetyEnvelope.ServerHealthDeadlineMS++
			case "missing":
				plan.PhaseDeadlines = nil
			}
			bounds, err := logicalEpochBounds(plan)
			if mode != "valid" {
				if err == nil {
					t.Fatal("changed bound admitted")
				}
				return
			}
			if err != nil || bounds.lifetime != 4*time.Hour || bounds.health != 15*time.Minute || bounds.controlPairs != 5 || bounds.outputBytes != 64<<20 {
				t.Fatal(bounds, err)
			}
		})
	}
}

func TestExecutionEpochLogicalRetainedPrefix(t *testing.T) {
	for _, mode := range []string{"retained", "terminal", "logical", "closed_root", "live_server", "missing_eof", "survivor", "active_author", "second_live"} {
		t.Run(mode, func(t *testing.T) {
			value := ExecutionEpochOneResult{RootStarted: true, RootJoined: true, SessionEmpty: true,
				Accounting: dispatchadmission.Snapshot{Producers: []dispatchadmission.ProducerCount{{Producer: 1, Attached: true, Ordinal: 3}, {Producer: 2, Attached: true, Closed: true}, {Producer: 8, Attached: true, Closed: true, Ordinal: 3}}},
				Store:      storeaccounting.WireSnapshot{Opened: 1, TerminalEOF: 1, Store: storeaccounting.Snapshot{Producers: []storeaccounting.ProducerCount{{Producer: 2, Attached: true, Closed: true}}}}}
			retained, logical := true, false
			switch mode {
			case "terminal":
				retained = false
				value.Accounting.Producers[0].Closed = true
			case "closed_root":
				value.Accounting.Producers[0].Closed = true
			case "live_server":
				value.Accounting.Producers[1].Closed = false
			case "missing_eof":
				value.Store.TerminalEOF = 0
			case "survivor":
				value.SessionEmpty = false
			case "active_author":
				value.Accounting.Producers[2].Active = 1
			case "logical", "second_live":
				retained, logical = false, true
				value.Accounting.Producers[0].Closed, value.Accounting.Producers[0].Ordinal = true, 4
				value.Accounting.Producers = append(value.Accounting.Producers, dispatchadmission.ProducerCount{Producer: 3, Attached: true, Closed: mode == "logical"})
				value.Store.Opened, value.Store.TerminalEOF = 2, 2
				value.Store.Store.Producers = append(value.Store.Store.Producers, storeaccounting.ProducerCount{Producer: 3, Attached: true, Closed: true})
			}
			want := mode == "retained" || mode == "terminal" || mode == "logical"
			if epochClosedPrefix(t.Context(), value, true, retained, logical) != want {
				t.Fatal("wrong retained/terminal prefix", mode)
			}
		})
	}
}

func TestExecutionEpochLogicalBorrowAbandonment(t *testing.T) {
	for _, mode := range []string{"joined", "failed_joined", "active", "survivor", "successor"} {
		t.Run(mode, func(t *testing.T) {
			run := &ExecutionEpochOneRun{done: make(chan struct{}), result: ExecutionEpochOneResult{RootJoined: true, SessionEmpty: true}}
			if mode != "active" {
				close(run.done)
			}
			if mode == "survivor" {
				run.result.SessionEmpty = false
			}
			if mode == "failed_joined" {
				run.err = ErrExecutionEpochOne
			}
			author := &ExecutionAuthorCustody{borrowedBy: run}
			epochs := &ExecutionEpochConfigCustody{author: author, active: true}
			if mode == "successor" {
				author.borrowedBy = &ExecutionEpochOneRun{}
			}
			released := false
			flow := &ExecutionEpochOne{epochs: epochs, used: true, retained: run, release: func() { released = true }}
			if author.Close() == nil || epochs.Close() == nil {
				t.Fatal("borrow did not prevent direct close")
			}
			err := flow.Close()
			want := mode == "joined" || mode == "failed_joined"
			if (err == nil) != want || released != want {
				t.Fatal("wrong abandonment", err, released)
			}
			if want && (author.borrowedBy != nil || epochs.active) {
				t.Fatal("joined abandoned borrow retained")
			}
			if !want && (author.borrowedBy == nil || !epochs.active) {
				t.Fatal("surviving successor borrow released")
			}
		})
	}
}

func TestExecutionEpochLogicalAdmissionRefusal(t *testing.T) {
	for _, mode := range []string{"unfinished", "stopping", "stopped", "expired", "used", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			flow := &ExecutionEpochOne{plan: Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}}
			run := &ExecutionEpochOneRun{flow: flow, stop: make(chan struct{}), physicalUsed: true, physicalDone: make(chan struct{}), inspection: &executionEpochInspection{}, phaseTimer: time.NewTimer(time.Hour), phaseDeadline: time.Now().Add(time.Hour)}
			defer run.phaseTimer.Stop()
			if mode != "unfinished" {
				close(run.physicalDone)
			}
			ctx := t.Context()
			switch mode {
			case "stopping":
				run.stopping = true
			case "stopped":
				close(run.stop)
			case "expired":
				run.phaseDeadline = time.Now().Add(-time.Second)
			case "used":
				flow.logicalUsed = true
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if next, err := run.StartLogicalB(ctx); next != nil || err == nil || run.retainParent || flow.retained != nil {
				t.Fatal("invalid continuation acquired borrow", err)
			}
		})
	}
}

// A joined-stop model isolates cancellation in the gap: no successor tool or
// native Start is available, so falling through to launch would fail the test.
func TestExecutionEpochLogicalCanceledAfterRetainedStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	close(done)
	author := &ExecutionAuthorCustody{}
	epochs := &ExecutionEpochConfigCustody{author: author, active: true}
	flow := &ExecutionEpochOne{epochs: epochs, used: true, plan: Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}, release: func() {}}
	run := &ExecutionEpochOneRun{flow: flow, stop: make(chan struct{}), done: make(chan struct{}), physicalUsed: true, physicalDone: done,
		inspection: &executionEpochInspection{finalUsed: true, retentionUsed: true, physicalAuthority: AuthorityPhaseResult{Phase: "physical_delta_b"}}, phaseTimer: time.NewTimer(time.Hour), phaseDeadline: time.Now().Add(time.Hour)}
	defer run.phaseTimer.Stop()
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
	next, err := run.StartLogicalB(ctx)
	<-joined
	if next != nil || err == nil || !flow.logicalUsed || flow.retained != run || author.borrowedBy != run || !epochs.active {
		t.Fatal("canceled gap lost borrow or admitted successor", err)
	}
	if flow.Close() != nil || author.borrowedBy != nil || epochs.active {
		t.Fatal("joined canceled gap could not be explicitly abandoned")
	}
}

func TestEpochLogicalActivationHTTP(t *testing.T) {
	for _, mode := range []string{"valid", "wrong_cost", "wrong_hit", "changed_unit", "duplicate", "recovered_first"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			search, oldCatalog := testDigest("search"), testDigest("old-catalog")
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				point := "hit"
				if calls == 2 {
					point = "recovered"
				}
				value := epochActivationObservation{Schema: "t422-activation-observation-v1", Point: point, SelectorDigest: testDigest(point), CatalogRootDigest: oldCatalog, SearchGenerationDigest: search, PlanDigest: testDigest("plan"), ScheduleDigest: testDigest("schedule"), UnitDigest: testDigest("unit")}
				if point == "recovered" {
					value.CatalogRootDigest = testDigest("new-catalog")
				}
				if mode == "wrong_hit" {
					value.CatalogRootDigest = testDigest("wrong")
				}
				if mode == "changed_unit" && point == "recovered" {
					value.UnitDigest = testDigest("other-unit")
				}
				ordinal, _ := strconv.ParseUint(r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", RequestOrdinal: ordinal, Status: "complete", StoreReadAttempts: 5}
				if mode == "wrong_cost" {
					report.StoreReadAttempts = 4
				}
				w.Header().Add("Trailer", epochReadTrailer)
				_, _ = w.Write(epochTestJSON(t, value, false))
				reportRaw, _ := json.Marshal(report)
				w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(reportRaw))
			}))
			reader.projection.Phase = "logical_delta_b"
			reader.physicalAuthority.SearchGenerationSHA256, reader.physicalAuthority.CatalogRootSHA256 = search, oldCatalog
			if mode == "recovered_first" {
				if reader.activation(t.Context(), "recovered") == nil || calls != 0 {
					t.Fatal("recovery preceded hit")
				}
				return
			}
			err := reader.activation(t.Context(), "hit")
			if mode == "wrong_hit" || mode == "wrong_cost" {
				if err == nil {
					t.Fatal("bad hit accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "duplicate" {
				if reader.activation(t.Context(), "hit") == nil || calls != 1 {
					t.Fatal("duplicate hit retried")
				}
				return
			}
			reader.tail = epochTailReadiness{Status: "ready", SelectedRuntimeSHA256: testDigest("recovered")}
			err = reader.activation(t.Context(), "recovered")
			if (err == nil) != (mode == "valid") || calls != 2 {
				t.Fatal("wrong recovered outcome", err, calls)
			}
		})
	}
}

func TestEpochLogicalFinalContinuity(t *testing.T) {
	reader, value := epochPhysicalTestFinal(t)
	physical, _, err := reader.decodeFinal(epochTestJSON(t, value, true))
	if err != nil {
		t.Fatal(err)
	}
	reader.physicalAuthority = physical
	reader.projection, err = expectedStateProjectionForPhase(reader.plan, "logical_delta_b")
	if err != nil {
		t.Fatal(err)
	}
	value.Authority.CatalogRootSHA256 = testDigest("logical-catalog")
	value.Authority.CatalogActivationPlanSHA256 = testDigest("logical-plan")
	value.Authority.CatalogActivationScheduleSHA256 = testDigest("logical-schedule")
	value.Authority.CatalogActivationUnitSHA256 = testDigest("logical-unit")
	value.Authority.RelationshipGenerationSHA256 = testDigest("logical-relationship")
	value.Authority.RelationshipRootSHA256 = testDigest("logical-root")
	raw, _ := json.Marshal(reader.projection)
	if json.Unmarshal(raw, &value.Projection) != nil {
		t.Fatal("projection")
	}
	value.Projection.Schema = "t421-final-state-projection-source-free-v1"
	reader.tail.RelationshipGenerationSHA256, reader.tail.RelationshipRootSHA256 = value.Authority.RelationshipGenerationSHA256, value.Authority.RelationshipRootSHA256
	reader.activationRecovered = epochActivationObservation{Schema: "t422-activation-observation-v1", CatalogRootDigest: value.Authority.CatalogRootSHA256, PlanDigest: value.Authority.CatalogActivationPlanSHA256, ScheduleDigest: value.Authority.CatalogActivationScheduleSHA256, UnitDigest: value.Authority.CatalogActivationUnitSHA256}
	if _, _, err := reader.decodeFinal(epochTestJSON(t, value, true)); err != nil {
		t.Fatal("valid logical model", err)
	}
	for _, mode := range []string{"physical", "resolver", "caller", "extraction", "catalog", "activation"} {
		t.Run(mode, func(t *testing.T) {
			bad := value
			switch mode {
			case "physical":
				bad.Authority.PhysicalCommit = reader.cold.PhysicalCommit
			case "resolver":
				bad.Authority.ResolverCatalogRootSHA256 = testDigest("bad")
			case "caller":
				bad.Authority.CallerRootSHA256 = testDigest("bad")
			case "extraction":
				bad.Authority.ExtractionRootsSHA256 = testDigest("bad")
			case "catalog":
				bad.Authority.CatalogRootSHA256 = physical.CatalogRootSHA256
			case "activation":
				bad.Authority.CatalogActivationUnitSHA256 = testDigest("bad")
			}
			if _, _, err := reader.decodeFinal(epochTestJSON(t, bad, true)); err == nil {
				t.Fatal("changed logical continuity accepted")
			}
		})
	}
}
