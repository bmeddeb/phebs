package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestExecutionEpochStaleBounds(t *testing.T) {
	for _, mode := range []string{"valid", "v2", "missing", "phase6", "phase7", "health"} {
		t.Run(mode, func(t *testing.T) {
			plan := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
			switch mode {
			case "v2":
				plan.Schema = PlanV2Schema
			case "missing":
				plan.PhaseDeadlines = nil
			case "phase6":
				plan.PhaseDeadlines[5].DeadlineMS++
			case "phase7":
				plan.PhaseDeadlines[6].DeadlineMS++
			case "health":
				plan.SafetyEnvelope.ServerHealthDeadlineMS++
			}
			bounds, err := returnStaleEpochBounds(plan)
			if (err == nil) != (mode == "valid") {
				t.Fatal("altered frozen window accepted", err)
			}
			if err == nil && (bounds.lifetime != 8*time.Hour || bounds.health != 15*time.Minute || bounds.controlPairs*2*dispatchadmission.FrameBytes != 1792 || bounds.outputBytes != 64<<20) {
				t.Fatal(bounds)
			}
			if mode == "valid" {
				old, err := returnEpochBounds(plan)
				if err != nil || old.lifetime != 4*time.Hour || old.controlPairs != 5 {
					t.Fatal("old mode widened", old, err)
				}
			}
		})
	}
}

func TestExecutionEpochStaleExtendedHandoffDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		flow := &ExecutionEpochOne{plan: Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}}
		run := &ExecutionEpochOneRun{flow: flow, epoch: ExecutionEpochConfig{Epoch: 2}, stop: make(chan struct{}), done: make(chan struct{}),
			logicalUsed: true, logicalDone: done, inspection: &executionEpochInspection{}, phaseDeadline: time.Now().Add(10 * time.Hour)}
		started := time.Now()
		if next, err := run.StartReturnAStale(t.Context()); next != nil || err == nil || time.Since(started) != 4*time.Hour {
			t.Fatal("phase6 borrowed phase7 time", time.Since(started), err)
		}
	})
}

func TestExecutionEpochStaleAdmissionAndDeadline(t *testing.T) {
	for _, mode := range []string{"unselected", "unfinished", "used", "canceled", "wrong_epoch", "stopping", "expired", "closed_control"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, child, err := dispatchadmission.NewPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = parent.Close(); _ = child.Close() }()
				control, err := dispatchadmission.NewPhaseControl(t.Context(), parent, [32]byte{1}, dispatchadmission.PhaseControlConfig{
					OwnerControl: true, Phases: []uint32{6, 7, 8}, InitialPhase: 6, MaximumPhases: 3, MaximumWireBytes: 1792, Timeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				_ = control.Close()
				done := make(chan struct{})
				if mode != "unfinished" {
					close(done)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				reader, value := epochReturnTestFinal(t)
				reader.returnAuthority, _, err = reader.decodeFinal(epochTestJSON(t, value, true))
				if err != nil {
					t.Fatal(err)
				}
				reader.finalUsed = true
				run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: reader.plan}, control: control, epoch: ExecutionEpochConfig{Epoch: 3}, stop: make(chan struct{}),
					staleAllowed: true, returnUsed: true, returnDone: done, inspection: reader, lifetimeDeadline: time.Now().Add(3 * time.Second), cancelRun: func() {}}
				run.setPhaseDeadlineLocked(time.Now().Add(time.Second))
				defer run.stopPhaseDeadline()
				switch mode {
				case "unselected":
					run.staleAllowed = false
				case "used":
					run.staleUsed = true
				case "canceled":
					cancel()
				case "wrong_epoch":
					run.epoch.Epoch = 2
				case "stopping":
					run.stopping = true
				case "expired":
					time.Sleep(2 * time.Second)
				}
				if run.StaleLease(ctx) == nil {
					t.Fatal("invalid operation accepted")
				}
				if mode == "closed_control" {
					if !run.staleUsed || run.err == nil || run.phaseDeadline != run.lifetimeDeadline {
						t.Fatal("deadline installed after handoff or total widened")
					}
					select {
					case <-run.staleDone:
					default:
						t.Fatal("refused handoff operation not joined")
					}
				} else if mode != "used" && run.staleUsed {
					t.Fatal("refusal consumed operation")
				}
			})
		})
	}
}

func epochStaleTestPreparation(t *testing.T) (*executionEpochInspection, epochFinalResponse, epochStalePreparation) {
	t.Helper()
	reader, value := epochReturnTestFinal(t)
	prior, _, err := reader.decodeFinal(epochTestJSON(t, value, true))
	if err != nil {
		t.Fatal(err)
	}
	reader.returnAuthority, reader.finalUsed = prior, true
	reader.run = &ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Repository: "example.com/mono"}}
	if reader.beginStale() != nil {
		t.Fatal("begin stale fixture")
	}
	root := prior.ExtractionRoots[0]
	if root.Domain != "grpc-caller" {
		t.Fatal("fixture root order")
	}
	prepared := epochStalePreparation{Schema: "t422-stale-preparation-observation-v1", Authority: value.Authority, TargetGeneration: root.GenerationSHA256,
		PriorSchedule: testDigest("native-predecessor"), Domain: root.Domain, Ordinal: 6, Offset: 6, PlanDigest: root.PlanSHA256,
		ResultIdentity: root.PartitionResults[6].ResultIdentitySHA256, ControlFileReads: 116, StoreReadAttempts: 24, StoreWriteAttempts: 1}
	prepared.RecoveryGeneration = SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + prepared.TargetGeneration + "\x00" + prepared.PriorSchedule))
	prepared.RecoverySchedule, err = store.GenerationScheduleDigest(store.GenerationScheduleSpec{Repository: reader.run.epoch.Repository,
		Stage: extractionpublication.ScheduleStage, Generation: prepared.RecoveryGeneration, ResourceClass: store.GenerationResourceExtraction,
		TotalItems: 56, ChunkItems: extractionpublication.ScheduleChunkItems, MaxAttempts: extractionpublication.ScheduleMaxAttempts, RepositoryTokens: extractionpublication.ScheduleRepositoryTokens})
	if err != nil {
		t.Fatal(err)
	}
	return reader, value, prepared
}

func TestEpochStalePreparationIdentityAndActualCounts(t *testing.T) {
	reader, _, prepared := epochStaleTestPreparation(t)
	if err := reader.validateStalePreparation(prepared); err != nil {
		t.Fatal("native-shaped preparation refused", err)
	}
	for _, test := range []struct {
		name   string
		change func(*epochStalePreparation)
	}{
		{"domain", func(v *epochStalePreparation) { v.Domain = "proto-contract" }}, {"ordinal", func(v *epochStalePreparation) { v.Ordinal++ }},
		{"offset", func(v *epochStalePreparation) { v.Offset++ }}, {"target", func(v *epochStalePreparation) { v.TargetGeneration = testDigest("other") }},
		{"prior", func(v *epochStalePreparation) { v.PriorSchedule = testDigest("other") }}, {"recovery", func(v *epochStalePreparation) { v.RecoveryGeneration = v.TargetGeneration }},
		{"schedule", func(v *epochStalePreparation) { v.RecoverySchedule = v.PriorSchedule }}, {"plan", func(v *epochStalePreparation) { v.PlanDigest = testDigest("other") }},
		{"result", func(v *epochStalePreparation) { v.ResultIdentity = testDigest("other") }}, {"authority", func(v *epochStalePreparation) { v.Authority.CatalogRootSHA256 = testDigest("other") }},
		{"missing_C", func(v *epochStalePreparation) { v.ControlFileReads-- }}, {"excess_C", func(v *epochStalePreparation) { v.ControlFileReads = 119 }},
		{"missing_S", func(v *epochStalePreparation) { v.StoreReadAttempts = 23 }}, {"excess_S", func(v *epochStalePreparation) { v.StoreReadAttempts = 340 }},
		{"missing_W", func(v *epochStalePreparation) { v.StoreWriteAttempts = 0 }}, {"excess_W", func(v *epochStalePreparation) { v.StoreWriteAttempts = 65 }},
		{"excess_M", func(v *epochStalePreparation) { v.MemberReads = 353296641 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := prepared
			test.change(&value)
			if reader.validateStalePreparation(value) == nil {
				t.Fatal("changed native binding/count accepted")
			}
		})
	}
	prepared.ControlFileReads, prepared.StoreReadAttempts, prepared.StoreWriteAttempts, prepared.MemberReads = 118, 339, 64, 353296640
	if reader.validateStalePreparation(prepared) != nil {
		t.Fatal("existing retry/cold maximum refused")
	}
}

func TestEpochStalePreparationHTTP(t *testing.T) {
	fixture, _, good := epochStaleTestPreparation(t)
	for _, mode := range []string{"success", "target", "unknown_field", "trailer", "exact_header", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/t422/stale-lease/prepare" || r.Header.Get("X-Phebs-T421-Exact-Reads") != "" || r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "" || r.Header.Get(dispatchadmission.ProductionRequestHeader) == "" {
					t.Error("private preparation route changed")
				}
				value := good
				if mode == "target" {
					value.ResultIdentity = testDigest("wrong")
				}
				raw := epochTestJSON(t, value, false)
				if mode == "unknown_field" {
					raw = append([]byte(`{"unexpected":1,`), raw[1:]...)
				}
				if mode == "oversize" {
					raw = bytes.Repeat([]byte{'x'}, (16<<10)+1)
				}
				if mode == "trailer" {
					w.Header().Set("Trailer", epochReadTrailer)
				}
				if mode == "exact_header" {
					w.Header().Set(epochReadTrailer, "other")
				}
				_, _ = w.Write(raw)
				if mode == "trailer" {
					w.Header().Set(epochReadTrailer, "other")
				}
			}))
			reader.plan, reader.projection, reader.returnAuthority = fixture.plan, fixture.projection, fixture.returnAuthority
			// The supplied HTTP fixture uses the same repository; no native work
			// or genuine selected request reservation is claimed by this test.
			before := reader.next
			err := reader.prepareStale(t.Context())
			want := mode == "success"
			if (err == nil) != want || reader.next != before || reader.reports != 0 || reader.stalePrepared != want {
				t.Fatal("preparation joined the exact-read ordinal or false success", err)
			}
			if mode == "target" && reader.stalePreparation.StoreReadAttempts != good.StoreReadAttempts {
				t.Fatal("actual preparation prefix lost")
			}
			if reader.prepareStale(t.Context()) == nil || calls != 1 {
				t.Fatal("one-shot preparation retried")
			}
		})
	}
}

func epochStaleTestTransition(t *testing.T, p epochStalePreparation, point store.GenerationStaleLeaseTransitionPoint) extractionpublication.StaleLeaseTransition {
	t.Helper()
	chunk, err := store.GenerationChunkIdentity(p.RecoverySchedule, int64(p.Offset), 0)
	if err != nil {
		t.Fatal(err)
	}
	return extractionpublication.StaleLeaseTransition{Point: point, TargetGeneration: p.TargetGeneration, ScheduleGeneration: p.RecoveryGeneration,
		PriorScheduleDigest: p.PriorSchedule, ScheduleDigest: p.RecoverySchedule, ChunkIdentity: chunk, Domain: p.Domain, Ordinal: p.Ordinal, PlanDigest: p.PlanDigest, ResultIdentity: p.ResultIdentity}
}

func TestEpochStaleNativeWireAndSharedOrdinal(t *testing.T) {
	fixture, _, prepared := epochStaleTestPreparation(t)
	for _, mode := range []string{"success", "generation", "result", "chunk", "changed_recovered", "S_short", "C_excess", "M_nonzero"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				point := store.GenerationStaleLeaseTransitionHit
				if calls == 2 {
					point = store.GenerationStaleLeaseTransitionRecovered
				}
				value := epochStaleTestTransition(t, prepared, point)
				if mode == "generation" {
					value.ScheduleGeneration = value.TargetGeneration
				}
				if mode == "result" {
					value.ResultIdentity = testDigest("wrong")
				}
				if mode == "chunk" {
					value.ChunkIdentity = testDigest("wrong")
				}
				if mode == "changed_recovered" && calls == 2 {
					value.PlanDigest = testDigest("wrong")
				}
				ordinal, _ := strconv.ParseUint(r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
				if ordinal != uint64(40+calls) {
					t.Error("ordinal reset across phase")
				}
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: ordinal, ControlFileReads: 4, StoreReadAttempts: 4}
				if mode == "S_short" {
					report.StoreReadAttempts = 3
				}
				if mode == "C_excess" {
					report.ControlFileReads = 5
				}
				if mode == "M_nonzero" {
					report.MemberVisits = 1
				}
				w.Header().Set("Trailer", epochReadTrailer)
				_, _ = w.Write(epochTestJSON(t, value, false))
				w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
			}))
			reader.plan, reader.projection, reader.returnAuthority, reader.stalePreparation, reader.stalePrepared = fixture.plan, fixture.projection, fixture.returnAuthority, prepared, true
			reader.next = 41
			reader.run.epoch.Epoch = 3
			reader.run.staleAllowed = true
			err := reader.stale(t.Context(), store.GenerationStaleLeaseTransitionHit)
			if err == nil {
				err = reader.stale(t.Context(), store.GenerationStaleLeaseTransitionRecovered)
			}
			if (err == nil) != (mode == "success") {
				t.Fatal("native transition accepted/refused", err)
			}
			if mode == "success" && (reader.reports != 2 || reader.totals.ControlFileReads != 8 || reader.totals.StoreReadAttempts != 8 || reader.next != 43) {
				t.Fatal("scopedR actual counters lost")
			}
			before := calls
			if reader.stale(t.Context(), store.GenerationStaleLeaseTransitionHit) == nil || calls != before {
				t.Fatal("nativeR replayed")
			}
		})
	}
}

func TestEpochStaleFinalFullAuthorityEquality(t *testing.T) {
	reader, value, prepared := epochStaleTestPreparation(t)
	reader.stalePrepared, reader.stalePreparation = true, prepared
	reader.staleRecovered = epochStaleTestTransition(t, prepared, store.GenerationStaleLeaseTransitionRecovered)
	raw, _ := json.Marshal(reader.projection)
	if json.Unmarshal(raw, &value.Projection) != nil {
		t.Fatal("projection")
	}
	value.Projection.Schema = "t421-final-state-projection-source-free-v1"
	reader.tail = epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: value.Authority.RelationshipGenerationSHA256, RelationshipRootSHA256: value.Authority.RelationshipRootSHA256, CallerGenerationSHA256: value.Authority.CallerGenerationSHA256, CallerRootSHA256: value.Authority.CallerRootSHA256}
	if _, _, err := reader.decodeFinal(epochTestJSON(t, value, true)); err != nil {
		t.Fatal("unchanged actual return-A refused", err)
	}
	for _, field := range []string{"PhysicalCommit", "PhysicalTree", "SourceGenerationSHA256", "CatalogRootSHA256", "RelationshipRootSHA256", "ExtractionRootsSHA256"} {
		t.Run(field, func(t *testing.T) {
			bad := value
			reflect.ValueOf(&bad.Authority).Elem().FieldByName(field).SetString(testDigest("wrong"))
			if _, _, err := reader.decodeFinal(epochTestJSON(t, bad, true)); err == nil {
				t.Fatal("changed authority accepted")
			}
		})
	}
	value.ExtractionRoots[0].PartitionResults[6].ResultIdentitySHA256 = testDigest("wrong")
	if _, _, err := reader.decodeFinal(epochTestJSON(t, value, true)); err == nil {
		t.Fatal("changed detailed root accepted")
	}
}
