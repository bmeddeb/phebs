package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
)

func pressureInspectionStatus() lifecycle.Status {
	value := lifecycle.Status{SchemaVersion: lifecycle.StatusSchema, Policy: lifecycle.StatusPolicy{Enabled: true, Owners: 16, SoftWatermarkPercent: lifecycle.SoftWatermarkPercent, HardWatermarkPercent: lifecycle.HardWatermarkPercent, ResumeWatermarkPercent: lifecycle.ResumeWatermarkPercent, MaxCandidatesPerTurn: lifecycle.MaxCandidatesPerTick, MaxDeletesPerTurn: lifecycle.MaxDeletesPerTick, MaxQueriesPerTurn: lifecycle.MaxQueriesPerTick}, Capacity: lifecycle.CapacityStatus{Completeness: lifecycle.Unavailable, Pressure: lifecycle.PressureUnavailable}}
	for _, name := range correctedLifecycleOwners() {
		value.Owners = append(value.Owners, lifecycle.OwnerStatus{Name: name, State: "not_run", Completeness: lifecycle.Unavailable})
	}
	value.Policy.MaxDeletesPerTurn = lifecycle.SelectedCleanupObservationDeletes
	return value
}

func pressureInspectionTrailer(t *testing.T, w http.ResponseWriter, r *http.Request, report epochInspectionReport) {
	t.Helper()
	ordinal, err := strconv.ParseUint(r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
	if err != nil {
		t.Error(err)
	}
	report.Schema, report.Status, report.RequestOrdinal = "t421-source-free-read-accounting-v1", "complete", ordinal
	w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
}

func TestExecutionEpochPressureLifecycleStatusTransport(t *testing.T) {
	for _, mode := range []string{"valid", "phase11", "phase10", "last_allowed", "exhausted", "disabled", "owner", "malformed", "nonzero", "missing", "http", "canceled", "after_final"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			status := pressureInspectionStatus()
			if mode == "disabled" {
				status.Policy.Enabled = false
			}
			if mode == "owner" {
				status.Owners[0].Name = "unknown"
			}
			// The real Huma endpoint supplies its actual schema envelope and
			// serialization; no presumed body shape stands in for that route.
			handler := api.New(api.Options{IsAdmin: func(context.Context) bool { return true }, SelectedLifecycleCleanup: true, LifecycleStatusSource: func(context.Context) lifecycle.Status { return status }})
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != api.LifecycleStatusPath || r.URL.RawQuery != "" {
					t.Error("wrong L route")
				}
				if mode != "missing" {
					w.Header().Set("Trailer", epochReadTrailer)
				}
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, r)
				body := recorder.Body.Bytes()
				if mode == "malformed" {
					body = append(body, body...)
				}
				code := recorder.Code
				if mode == "http" {
					code = http.StatusConflict
				}
				w.WriteHeader(code)
				_, _ = w.Write(body)
				if mode != "missing" {
					report := epochInspectionReport{}
					if mode == "nonzero" {
						report.StoreReadAttempts = 1
					}
					pressureInspectionTrailer(t, w, r, report)
				}
			}))
			reader.run.epoch.Epoch, reader.run.pressureAllowed = 4, true
			reader.projection.Phase = "pressure_80"
			if mode == "phase11" {
				reader.projection.Phase = "pressure_75"
			}
			if mode == "phase10" {
				reader.projection.Phase = "pressure_90"
			}
			rows, _, _ := correctedInspectionInventory(reader.plan.Profile)
			reader.bounds = rows[8]
			if mode == "last_allowed" {
				reader.lifecycleCalls = reader.bounds.LifecycleStatusCalls.Maximum - 1
			}
			if mode == "exhausted" {
				reader.lifecycleCalls = reader.bounds.LifecycleStatusCalls.Maximum
			}
			if mode == "after_final" {
				reader.finalUsed = true
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			got, _, err := reader.LifecycleStatus(ctx)
			valid := mode == "valid" || mode == "phase11" || mode == "last_allowed"
			if (err == nil) != valid {
				t.Fatal(got, reader.failureStatus, string(reader.failureBody), err)
			}
			if valid && (got.Capacity.Completeness != lifecycle.Unavailable || got.Owners[0].State != "not_run") {
				t.Fatal("truthful pending status erased", got)
			}
			noHTTP := mode == "phase10" || mode == "exhausted" || mode == "canceled" || mode == "after_final"
			if noHTTP && (calls.Load() != 0 || reader.next != 1) {
				t.Fatal("preflight consumed HTTP", calls.Load(), reader.next)
			}
			if !noHTTP && (calls.Load() != 1 || reader.next != 2) {
				t.Fatal(calls.Load(), reader.next)
			}
			if !noHTTP && mode != "missing" && mode != "nonzero" && reader.reports != 1 {
				t.Fatal("accepted zero-unit report lost", reader.reports)
			}
			if !valid && reader.err == nil {
				t.Fatal("refusal not latched")
			}
		})
	}
}

func pressureInspectionRecoveredFixture(t *testing.T) (*executionEpochInspection, epochFinalResponse) {
	t.Helper()
	reader, final, hit := epochCheckpointTestPreparation(t)
	recovered := hit
	recovered.Point, recovered.Priority, recovered.ChunkStatus, recovered.Leased = store.GenerationStaleLeaseTransitionRecovered, store.GenerationPriorityStale, store.GenerationChunkDone, false
	recovered.ScheduleStatus = store.GenerationScheduleSettled
	recovered.CompletionBitSet, recovered.RootExists, recovered.Current = true, true, true
	for _, root := range reader.staleAuthority.ExtractionRoots {
		if root.Domain == hit.Domain {
			recovered.RootDigest = root.RootSHA256
		}
	}
	reader.checkpointRecovered = recovered
	reader.tail = epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: final.Authority.RelationshipGenerationSHA256, RelationshipRootSHA256: final.Authority.RelationshipRootSHA256, CallerGenerationSHA256: final.Authority.CallerGenerationSHA256, CallerRootSHA256: final.Authority.CallerRootSHA256}
	projection, _ := json.Marshal(reader.projection)
	if json.Unmarshal(projection, &final.Projection) != nil {
		t.Fatal("projection")
	}
	final.Projection.Schema = "t421-final-state-projection-source-free-v1"
	return reader, final
}

func TestExecutionEpochPressureFinalActualRecoveredAnchor(t *testing.T) {
	for _, mode := range []string{"valid", "changed_root", "changed_authority", "noncanonical", "missing_L", "missing_R", "unselected", "wrong_epoch", "returned_alias", "failed_checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			fixture, final := pressureInspectionRecoveredFixture(t)
			good := epochTestJSON(t, final, true)
			var changed epochFinalResponse
			if json.Unmarshal(good, &changed) != nil {
				t.Fatal("fixture")
			}
			if mode == "changed_root" {
				changed.ExtractionRoots[0].PartitionResults[0].ResultDigestSHA256 = testDigest("different actual root")
			}
			if mode == "changed_authority" {
				changed.Authority.SourceGenerationSHA256 = testDigest("different actual source")
			}
			bad := epochTestJSON(t, changed, true)
			if mode == "noncanonical" {
				bad = bytes.Replace(bad, []byte("  \"schema\""), []byte(" \"schema\""), 1)
			}
			var calls atomic.Int32
			var lifecycleCalls atomic.Int32
			lifecycleHandler := api.New(api.Options{IsAdmin: func(context.Context) bool { return true }, SelectedLifecycleCleanup: true, LifecycleStatusSource: func(context.Context) lifecycle.Status { return pressureInspectionStatus() }})
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == api.LifecycleStatusPath {
					lifecycleCalls.Add(1)
					w.Header().Set("Trailer", epochReadTrailer)
					lifecycleHandler.ServeHTTP(w, r)
					pressureInspectionTrailer(t, w, r, epochInspectionReport{})
					return
				}
				call := calls.Add(1)
				w.Header().Set("Trailer", epochReadTrailer)
				if call == 1 {
					_, _ = w.Write(good)
				} else {
					_, _ = w.Write(bad)
				}
				pressureInspectionTrailer(t, w, r, epochInspectionReport{ControlFileReads: 10, StoreReadAttempts: 3, MemberVisits: 2})
			}))
			reader.run.epoch.Epoch, reader.run.pressureAllowed = 4, true
			reader.run.epoch.CatalogSHA256 = fixture.projection.CatalogSource.SHA256
			reader.projection, reader.tail, reader.staleAuthority = fixture.projection, fixture.tail, fixture.staleAuthority
			reader.checkpointRecovered = fixture.checkpointRecovered
			reader.progressReady = true
			if mode == "failed_checkpoint" {
				reader.checkpointRecovered.Point = ""
			}
			authority, projection, _, err := reader.Final(t.Context())
			if mode == "failed_checkpoint" {
				if err == nil || reader.pressureBaseline != nil {
					t.Fatal("failed F made baseline")
				}
				return
			}
			if err != nil || reader.pressureBaseline == nil {
				t.Fatal("actual recovered F missing", err)
			}
			baseline := *reader.pressureBaseline
			if mode == "returned_alias" {
				authority.ExtractionRoots[0].PartitionResults[0].ResultDigestSHA256 = testDigest("caller mutation")
				projection.ExtractionRoots[0].ApplicablePartitions++
			}
			reader.pressure.step = 1
			for _, phase := range []uint32{9, 10, 11} {
				if err := reader.beginPressure(phase); err != nil {
					t.Fatal(err)
				}
				reader.progressReady, reader.tail = true, fixture.tail
				if phase != 10 && mode != "missing_L" {
					if _, _, err := reader.LifecycleStatus(t.Context()); err != nil {
						t.Fatal("actual required L", err)
					}
				}
				reader.pressure.step = []uint8{4, 5, 9}[phase-9]
				if mode == "missing_L" {
					reader.lifecycleCalls = 0
				}
				if mode == "missing_R" {
					reader.pressure.step--
				}
				if mode == "unselected" {
					reader.run.pressureAllowed = false
				}
				if mode == "wrong_epoch" {
					reader.run.epoch.Epoch = 3
				}
				actual, _, _, err := reader.Final(t.Context())
				valid := mode == "valid" || mode == "returned_alias"
				if (err == nil) != valid {
					t.Fatal(phase, actual, err)
				}
				if *reader.pressureBaseline != baseline {
					t.Fatal("baseline changed")
				}
				if !valid {
					return
				}
				if actual.Phase != reader.plan.PhaseOrder[phase-1] || !actual.Current {
					t.Fatal(actual)
				}
			}
			if calls.Load() != 4 || lifecycleCalls.Load() != 2 || reader.reports != 6 {
				t.Fatal("actual L/F call inventory", calls.Load(), lifecycleCalls.Load(), reader.reports)
			}
		})
	}
}

func TestExecutionEpochPressureFinalNoHTTPWithoutBaseline(t *testing.T) {
	reader := epochTestHTTPReader(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("missing baseline read") }))
	reader.run.epoch.Epoch, reader.run.pressureAllowed = 4, true
	reader.projection.Phase, reader.progressReady, reader.tail.Status = "pressure_80", true, "ready"
	if _, _, _, err := reader.Final(t.Context()); err == nil || reader.next != 1 || reader.finalUsed {
		t.Fatal(err, reader.next, reader.finalUsed)
	}
	if strings.Contains(reader.readFailure.Stage, "http") {
		t.Fatal(reader.readFailure)
	}
}
