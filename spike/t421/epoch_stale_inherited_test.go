//go:build darwin || linux

package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

// Real inherited DA/PC/SA controllers, but source-free HTTP response models:
// this proves parent ordering and joins, not native preparation or stale reap.
func testEpochInheritedStaleObservation(t *testing.T, ctx context.Context, run *ExecutionEpochOneRun) {
	t.Helper()
	reader, final, prepared := epochStaleTestPreparation(t)
	raw, _ := json.Marshal(reader.projection)
	if json.Unmarshal(raw, &final.Projection) != nil {
		t.Fatal("stale projection fixture")
	}
	final.Projection.Schema = "t421-final-state-projection-source-free-v1"
	tail := epochTailReadiness{Schema: "t421-tail-readiness-source-free-v1", Status: "ready", SelectedRuntimeSHA256: testDigest("stale-runtime"),
		RelationshipGenerationSHA256: final.Authority.RelationshipGenerationSHA256, RelationshipRootSHA256: final.Authority.RelationshipRootSHA256,
		CallerGenerationSHA256: final.Authority.CallerGenerationSHA256, CallerRootSHA256: final.Authority.CallerRootSHA256}
	reader.projection.Phase, reader.finalUsed, reader.next = "return_a", true, 41
	reader.run = run
	wantPaths := []string{"/api/t422/stale-lease/prepare", "/api/t422/stale-lease/hit", "/api/t422/stale-lease/recovered", api.ExtractionProgressPath, "/api/t421/tail-readiness", "/api/t421/final-authority"}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		index := int(calls.Add(1)) - 1
		if index >= len(wantPaths) || request.URL.Path != wantPaths[index] || request.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("stale parent reordered a held R behind X/T or changed its route")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		pairs := uint64(9) // Reopened owners and their token-authorized requests.
		switch index {
		case 0:
			pairs = 7 // First owner-drained preparation window.
		case 5:
			pairs = 11 // Second owner-drained final-authority window.
		}
		if run.control.RequestToken() == "" || request.Header.Get(dispatchadmission.ProductionRequestHeader) != run.control.RequestToken() ||
			run.control.ReservedWireBytes() != pairs*2*dispatchadmission.FrameBytes {
			t.Error("stale request escaped the exact control window")
		}
		if index == 0 {
			if request.Method != http.MethodPost || request.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "" {
				t.Error("preparation fabricated an exact-read ordinal")
			}
			_, _ = w.Write(epochTestJSON(t, prepared, false))
			return
		}
		ordinal, err := strconv.ParseUint(request.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
		if err != nil || ordinal != uint64(40+index) || request.Method != http.MethodGet {
			t.Error("stale read lost shared ordinal")
		}
		report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: ordinal, ControlFileReads: 4, StoreReadAttempts: 4}
		var body []byte
		switch index {
		case 1, 2:
			point := store.GenerationStaleLeaseTransitionHit
			if index == 2 {
				point = store.GenerationStaleLeaseTransitionRecovered
			}
			body = epochTestJSON(t, epochStaleTestTransition(t, prepared, point), false)
		case 3:
			total, domains := int(reader.plan.Profile.Physical.CombinedModeledPartitions), len(reader.plan.Profile.Pipeline.ExtractionDomains)
			value := struct {
				Schema string `json:"$schema"`
				extractionpublication.Progress
			}{Schema: "http://" + run.epoch.Listen + "/schemas/ExtractionProgress.json", Progress: extractionpublication.Progress{
				State: "current", Total: total, Materialized: total, Succeeded: total, Domains: domains, CurrentDomains: domains}}
			body = epochTestJSON(t, value, false)
			report.ControlFileReads = 2 + uint64(domains)
		case 4:
			body = epochTestJSON(t, tail, false)
		case 5:
			body = epochTestJSON(t, final, true)
			report.ControlFileReads, report.StoreReadAttempts, report.MemberVisits = correctedFinalAuthorityControlReadMaximum, correctedFinalAuthorityStoreReadMaximum, correctedFinalAuthorityMemberReadMaximum
		}
		w.Header().Set("Trailer", epochReadTrailer)
		_, _ = w.Write(body)
		w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
	}))
	defer server.Close()
	run.epoch.Listen, run.epoch.APIKey, run.epoch.Repository = strings.TrimPrefix(server.URL, "http://"), "private-key", "example.com/mono"
	run.flow.plan, run.inspection = reader.plan, reader
	run.stop, run.returnDone = make(chan struct{}), make(chan struct{})
	close(run.returnDone)
	run.returnUsed, run.staleAllowed, run.warm = true, true, true
	run.lifetimeDeadline = time.Now().Add(20 * time.Second)
	run.setPhaseDeadlineLocked(time.Now().Add(10 * time.Second))
	defer run.stopPhaseDeadline()
	if err := run.StaleLease(ctx); err != nil {
		t.Fatal("actual inherited stale parent choreography", err, reader.err, calls.Load())
	}
	prior := reader.returnAuthority
	prior.Phase = "stale_lease"
	if calls.Load() != 6 || reader.next != 46 || reader.reports != 5 || reader.stalePreparation != prepared ||
		!reflect.DeepEqual(prior, reader.staleAuthority) || run.control.RequestToken() != "" {
		t.Fatal("stale parent lost bounded observations or full authority equality")
	}
	select {
	case <-run.staleDone:
	default:
		t.Fatal("stale operation returned before join")
	}
	if run.StaleLease(ctx) == nil || calls.Load() != 6 {
		t.Fatal("stale operation replayed")
	}
}
