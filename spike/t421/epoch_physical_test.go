package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestExecutionEpochPhysicalBounds(t *testing.T) {
	plan := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
	got, err := epochOneBounds(plan, epochOnePhysicalB)
	if err != nil || got.lifetime != 500*time.Minute || got.physical != 4*time.Hour || got.cold != 4*time.Hour ||
		got.health != 15*time.Minute || got.outputBytes != 64<<20 || got.controlPairs != 21 {
		t.Fatalf("physical private construction changed: %+v / %v", got, err)
	}
	for _, change := range []func(*Plan){
		func(p *Plan) { p.Schema = PlanV2Schema },
		func(p *Plan) { p.PhaseDeadlines[3].DeadlineMS++ },
		func(p *Plan) { p.PhaseDeadlines[3].Phase = "warm_noop" },
		func(p *Plan) { p.PhaseDeadlines = p.PhaseDeadlines[:3] },
	} {
		plan := plan
		plan.PhaseDeadlines = frozenPhaseDeadlines()
		change(&plan)
		if _, err := epochOneBounds(plan, epochOnePhysicalB); err == nil {
			t.Fatal("changed physical phase contract admitted")
		}
	}
}

func TestExecutionEpochPhysicalRefusesBeforeOperation(t *testing.T) {
	for _, name := range []string{"nil", "not_allowed", "already_used", "warm_unjoined", "missing_warm", "stopping", "failed", "canceled", "expired"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			close(done)
			run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{epochs: &ExecutionEpochConfigCustody{}},
				control: &dispatchadmission.PhaseControl{}, stop: make(chan struct{}), inspection: &executionEpochInspection{},
				physicalAllowed: true, physicalLimit: time.Hour, warmUsed: true, warmDone: done,
				phaseDeadline: time.Now().Add(time.Minute), phaseTimer: time.AfterFunc(time.Minute, func() {})}
			defer run.phaseTimer.Stop()
			switch name {
			case "nil":
				run = nil
			case "not_allowed":
				run.physicalAllowed = false
			case "already_used":
				run.physicalUsed = true
			case "warm_unjoined":
				run.warmDone = make(chan struct{})
			case "missing_warm":
				run.warmUsed = false
			case "stopping":
				run.stopping = true
			case "failed":
				run.err = ErrExecutionEpochOne
			case "canceled":
				cancel()
			case "expired":
				run.phaseDeadline = time.Now().Add(-time.Second)
			}
			if run.PhysicalB(ctx) == nil || run != nil && (run.physicalDone != nil || run.physicalPinned) {
				t.Fatal("unavailable physical operation advanced")
			}
		})
	}
}

func TestExecutionEpochPhysicalClosedPrefix(t *testing.T) {
	base := func() ExecutionEpochOneResult {
		return ExecutionEpochOneResult{RootStarted: true, RootJoined: true, SessionEmpty: true,
			Accounting: dispatchadmission.Snapshot{Producers: []dispatchadmission.ProducerCount{
				{Producer: 1, Attached: true, Closed: true, Ordinal: 3}, {Producer: 2, Attached: true, Closed: true},
				{Producer: 8, Attached: true, Closed: true, Ordinal: 3}}},
			Store: storeaccounting.WireSnapshot{Opened: 1, TerminalEOF: 1, Store: storeaccounting.Snapshot{
				Producers: []storeaccounting.ProducerCount{{Producer: 2, Attached: true, Closed: true}}}}}
	}
	if !epochOneClosedPrefixForMode(t.Context(), base(), true) || epochOneClosedPrefix(t.Context(), base()) {
		t.Fatal("physical prefix lost exact third root attempt or changed old-mode closure")
	}
	for _, change := range []func(*ExecutionEpochOneResult){
		func(r *ExecutionEpochOneResult) { r.Accounting.Producers[0].Ordinal-- },
		func(r *ExecutionEpochOneResult) { r.Accounting.Producers[2].Closed = false },
		func(r *ExecutionEpochOneResult) { r.Accounting.Producers[2].Active = 1 },
		func(r *ExecutionEpochOneResult) { r.Accounting.Producers[2].Ordinal-- },
		func(r *ExecutionEpochOneResult) { r.Accounting.Producers = r.Accounting.Producers[:2] },
		func(r *ExecutionEpochOneResult) { r.SessionEmpty = false },
	} {
		value := base()
		change(&value)
		if epochOneClosedPrefixForMode(t.Context(), value, true) {
			t.Fatal("physical prefix admitted unjoined/incomplete author or server")
		}
	}
}

func TestExecutionEpochPhysicalDeadlineStartsBeforeHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		parent, child, err := dispatchadmission.NewPipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = child.Close() }()
		control, err := dispatchadmission.NewPhaseControl(t.Context(), parent, [32]byte{1}, dispatchadmission.PhaseControlConfig{
			OwnerControl: true, Phases: []uint32{2, 3, 4}, InitialPhase: 2, MaximumPhases: 3, MaximumWireBytes: 1024, Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_ = control.Close() // Constructed, then unavailable before any exchange.
		warmDone := make(chan struct{})
		close(warmDone)
		run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{epochs: &ExecutionEpochConfigCustody{}},
			control: control, stop: make(chan struct{}), inspection: &executionEpochInspection{},
			physicalAllowed: true, physicalLimit: 4 * time.Hour, warmUsed: true, warmDone: warmDone,
			lifetimeDeadline: now.Add(3 * time.Second), cancelRun: func() {}}
		run.setPhaseDeadlineLocked(now.Add(time.Second))
		defer run.stopPhaseDeadline()
		time.Sleep(500 * time.Millisecond)
		// The deliberately unavailable control refuses the first handoff call.
		// The physical deadline must already be installed and clipped to the
		// inherited total deadline; failure is sticky and the operation joined.
		if run.PhysicalB(t.Context()) == nil || !run.physicalUsed || run.err == nil || run.phaseDeadline != run.lifetimeDeadline {
			t.Fatal("physical deadline was deferred until after handoff/pin or widened total lifetime")
		}
		select {
		case <-run.physicalDone:
		default:
			t.Fatal("failed handoff did not join physical operation")
		}
	})
}

// Model the exact native body shape, not actual production custody or a phase
// pass. The existing full authority validator checks the A -> B continuity.
func epochPhysicalTestFinal(t *testing.T) (*executionEpochInspection, epochFinalResponse) {
	t.Helper()
	reader, value := epochTestFinal(t)
	cold, _, err := reader.decodeFinal(epochTestJSON(t, value, true))
	if err != nil {
		t.Fatal(err)
	}
	reader.cold, reader.finalUsed = cold, true
	if reader.beginWarm() != nil {
		t.Fatal("model warm boundary")
	}
	reader.warmAuthority, reader.finalUsed = withPhase(cold, "warm_noop"), true
	physical := reader.plan.Revisions.Physical[1]
	if reader.beginPhysical(AuthoredExecutionRevision{Name: "b", Commit: physical.ExpectedCommit, Tree: physical.ExpectedTree}) != nil {
		t.Fatal("model physical boundary")
	}
	state := cold.AuthorityState
	state.PhysicalCommit, state.PhysicalTree = physical.ExpectedCommit, physical.ExpectedTree
	state.SearchInventory, state.ObservationInputInventory = physical.ExpectedTreeInventory, physical.ExpectedObservationInputInventory
	v := reflect.ValueOf(&state).Elem()
	for index := 0; index < v.NumField(); index++ {
		if v.Field(index).Kind() == reflect.String && strings.HasSuffix(v.Type().Field(index).Name, "SHA256") {
			v.Field(index).SetString(testDigest("epoch-physical-fixture", v.Type().Field(index).Name))
		}
	}
	roots := testExtractionRoots(t, reader.plan, physical, state, "epoch-physical-fixture")
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
		t.Fatal("physical authority wire conversion")
	}
	raw, _ = json.Marshal(reader.projection)
	if json.Unmarshal(raw, &value.Projection) != nil {
		t.Fatal("physical projection wire conversion")
	}
	value.Projection.Schema = "t421-final-state-projection-source-free-v1"
	reader.tail = epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: state.RelationshipGenerationSHA256,
		RelationshipRootSHA256: state.RelationshipRootSHA256, CallerGenerationSHA256: state.CallerGenerationSHA256, CallerRootSHA256: state.CallerRootSHA256}
	return reader, value
}

func TestEpochInspectionPhysicalAuthorityContinuity(t *testing.T) {
	reader, value := epochPhysicalTestFinal(t)
	got, projection, err := reader.decodeFinal(epochTestJSON(t, value, true))
	if err != nil || got.Phase != "physical_delta_b" || got.PhysicalRevision != "b" || got.LogicalRevision != "a" || projection.Phase != got.Phase {
		t.Fatal("physical model failed complete authority validation", err)
	}
	if reader.beginPhysical(reader.authored) == nil {
		t.Fatal("physical boundary repeated")
	}
	for _, field := range []string{"SourceGenerationSHA256", "SearchGenerationSHA256", "ObservationGenerationSHA256", "RelationshipGenerationSHA256", "CallerGenerationSHA256"} {
		t.Run(field, func(t *testing.T) {
			changed := value
			old := reflect.ValueOf(reader.cold.AuthorityState).FieldByName(field)
			if !old.IsValid() {
				t.Fatal("fixture field missing", field)
			}
			reflect.ValueOf(&changed.Authority).Elem().FieldByName(field).Set(old)
			if _, _, err := reader.decodeFinal(epochTestJSON(t, changed, true)); err == nil {
				t.Fatal("unchanged source-bound authority admitted")
			}
		})
	}
	reader.warmAuthority = AuthorityPhaseResult{}
	if _, _, err := reader.decodeFinal(epochTestJSON(t, value, true)); err == nil {
		t.Fatal("missing actual warm authority admitted")
	}
}

func epochRetentionTestValue(reader *executionEpochInspection, now time.Time) epochRetentionObservation {
	probe := reader.plan.ReaderProbe
	return epochRetentionObservation{Schema: "t422-current-prior-observation-v1",
		OldSearchGenerationSHA256: reader.warmAuthority.SearchGenerationSHA256, NewSearchGenerationSHA256: reader.physicalAuthority.SearchGenerationSHA256,
		QuerySHA256: probe.QuerySHA256, OldProjectionSHA256: probe.OldProjectionSHA256, NewProjectionSHA256: probe.NewProjectionSHA256,
		PostReleaseProjectionSHA256: probe.OldProjectionSHA256, OldRecords: probe.ExpectedRecords, NewRecords: probe.ExpectedRecords, PostReleaseRecords: probe.ExpectedRecords,
		PinnedAtUnixNano: now.Add(time.Millisecond).UnixNano(), ReleasedAtUnixNano: now.Add(3 * time.Millisecond).UnixNano(),
		Held: epochRetentionSweep{Attempt: 1, Completeness: "exact"}, Released: epochRetentionSweep{Attempt: 2, Completeness: "exact"}, OldReaderHeldThroughReprobe: true}
}

func TestEpochInspectionPhysicalRetentionStrict(t *testing.T) {
	reader, final := epochPhysicalTestFinal(t)
	reader.physicalAuthority, _, _ = reader.decodeFinal(epochTestJSON(t, final, true))
	now := time.Now()
	good := epochRetentionTestValue(reader, now)
	for _, test := range []struct {
		name   string
		change func(*epochRetentionObservation)
	}{
		{"complete", func(*epochRetentionObservation) {}},
		{"schema", func(v *epochRetentionObservation) { v.Schema += "x" }},
		{"old_generation", func(v *epochRetentionObservation) { v.OldSearchGenerationSHA256 = v.NewSearchGenerationSHA256 }},
		{"new_generation", func(v *epochRetentionObservation) { v.NewSearchGenerationSHA256 = v.OldSearchGenerationSHA256 }},
		{"query", func(v *epochRetentionObservation) { v.QuerySHA256 = testDigest("wrong") }},
		{"old_projection", func(v *epochRetentionObservation) { v.OldProjectionSHA256 = testDigest("wrong") }},
		{"new_projection", func(v *epochRetentionObservation) { v.NewProjectionSHA256 = testDigest("wrong") }},
		{"reprobe", func(v *epochRetentionObservation) { v.PostReleaseProjectionSHA256 = testDigest("wrong") }},
		{"old_records", func(v *epochRetentionObservation) { v.OldRecords++ }},
		{"new_records", func(v *epochRetentionObservation) { v.NewRecords++ }},
		{"reprobe_records", func(v *epochRetentionObservation) { v.PostReleaseRecords++ }},
		{"pin_before_request", func(v *epochRetentionObservation) { v.PinnedAtUnixNano = now.Add(-time.Nanosecond).UnixNano() }},
		{"pin_after_join", func(v *epochRetentionObservation) { v.PinnedAtUnixNano = now.Add(3 * time.Millisecond).UnixNano() }},
		{"release_before_join", func(v *epochRetentionObservation) { v.ReleasedAtUnixNano = now.Add(time.Millisecond).UnixNano() }},
		{"release_after_response", func(v *epochRetentionObservation) { v.ReleasedAtUnixNano = now.Add(time.Second).UnixNano() }},
		{"old_reader_released", func(v *epochRetentionObservation) { v.OldReaderHeldThroughReprobe = false }},
		{"legacy_delete", func(v *epochRetentionObservation) { v.Released.Deleted = 1 }},
		{"scanned", func(v *epochRetentionObservation) { v.Held.Scanned = 1 }},
		{"lower_bound", func(v *epochRetentionObservation) { v.Held.Completeness = "lower_bound" }},
		{"backlog", func(v *epochRetentionObservation) { v.Released.More = true }},
		{"failed", func(v *epochRetentionObservation) { v.Released.Failed = true }},
		{"attempt", func(v *epochRetentionObservation) { v.Released.Attempt = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := good
			test.change(&value)
			err := reader.validateRetention(value, now, now.Add(2*time.Millisecond), now.Add(4*time.Millisecond))
			if (err == nil) != (test.name == "complete") {
				t.Fatal("incorrect retention admission", err)
			}
		})
	}
}

func TestEpochInspectionPhysicalRetentionHTTP(t *testing.T) {
	for _, mode := range []string{"complete", "short_control", "short_members", "store_read", "store_write", "extra_field", "duplicate_field", "newline", "missing_report"} {
		t.Run(mode, func(t *testing.T) {
			model, final := epochPhysicalTestFinal(t)
			model.physicalAuthority, _, _ = model.decodeFinal(epochTestJSON(t, final, true))
			now := time.Now().Add(-time.Second)
			value := epochRetentionTestValue(model, now)
			calls := 0
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls++
				ordinal, _ := strconv.ParseUint(request.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
				if request.URL.Path != "/api/t422/retention/current-prior" || ordinal != 1 {
					t.Error("incorrect single R request")
				}
				bound, _ := correctedPhysicalTransitionReadBound(model.plan.Profile)
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: ordinal,
					ControlFileReads: bound.ControlFileReads.Maximum, MemberVisits: bound.MemberReads.Maximum}
				raw := bytes.TrimSuffix(epochTestJSON(t, value, false), []byte{'\n'})
				switch mode {
				case "short_control":
					report.ControlFileReads--
				case "short_members":
					report.MemberVisits--
				case "store_read":
					report.StoreReadAttempts++
				case "store_write":
					report.StoreWriteAttempts++
				case "extra_field":
					raw = append([]byte(`{"extra":true,`), raw[1:]...)
				case "duplicate_field":
					raw = bytes.Replace(raw, []byte(`"old_records":1`), []byte(`"old_records":1,"old_records":1`), 1)
				case "newline":
					raw = append(raw, '\n')
				}
				if mode != "missing_report" {
					w.Header().Set("Trailer", epochReadTrailer)
				}
				_, _ = w.Write(raw)
				if mode != "missing_report" {
					w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
				}
			}))
			reader.projection, reader.warmAuthority, reader.physicalAuthority, reader.finalUsed = model.projection, model.warmAuthority, model.physicalAuthority, true
			_, err := reader.retention(t.Context(), now, now.Add(2*time.Millisecond))
			if (err == nil) != (mode == "complete") || calls != 1 || !reader.retentionUsed {
				t.Fatal("R admission/body/accounting mismatch", err, calls)
			}
			if _, err := reader.retention(t.Context(), now, now.Add(2*time.Millisecond)); err == nil || calls != 1 {
				t.Fatal("R retried")
			}
		})
	}
}

func TestEpochInspectionPhysicalTailWaitsForBothChangedRoots(t *testing.T) {
	model, _ := epochPhysicalTestFinal(t)
	prior := model.warmAuthority
	for _, mode := range []string{"unchanged", "relationship_only", "caller_only", "partial_pair", "changed"} {
		t.Run(mode, func(t *testing.T) {
			tail := model.tail
			tail.Schema, tail.SelectedRuntimeSHA256 = "t421-tail-readiness-source-free-v1", testDigest("selected")
			switch mode {
			case "unchanged":
				tail.RelationshipGenerationSHA256, tail.RelationshipRootSHA256 = prior.RelationshipGenerationSHA256, prior.RelationshipRootSHA256
				tail.CallerGenerationSHA256, tail.CallerRootSHA256 = prior.CallerGenerationSHA256, prior.CallerRootSHA256
			case "relationship_only":
				tail.CallerGenerationSHA256, tail.CallerRootSHA256 = prior.CallerGenerationSHA256, prior.CallerRootSHA256
			case "caller_only":
				tail.RelationshipGenerationSHA256, tail.RelationshipRootSHA256 = prior.RelationshipGenerationSHA256, prior.RelationshipRootSHA256
			case "partial_pair":
				tail.RelationshipRootSHA256 = prior.RelationshipRootSHA256
			}
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ordinal, _ := strconv.ParseUint(r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: ordinal, ControlFileReads: 4, StoreReadAttempts: 4}
				w.Header().Set("Trailer", epochReadTrailer)
				_, _ = w.Write(epochTestJSON(t, tail, false))
				w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
			}))
			reader.projection, reader.bounds, reader.warmAuthority, reader.progressReady = model.projection, model.bounds, prior, true
			got, report, err := reader.Tail(t.Context())
			if err != nil || (got.Status == "ready") != (mode == "changed") || report.ControlFileReads != 4 || report.StoreReadAttempts != 4 || reader.reports != 1 {
				t.Fatal("physical T freshness or charged report lost", got, report, err)
			}
			if mode != "changed" && (reader.tail.Status == "ready" || reader.err != nil) {
				t.Fatal("unchanged ready tail became final-ready or latched instead of bounded polling")
			}
		})
	}
}
