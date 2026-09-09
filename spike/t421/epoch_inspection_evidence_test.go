package t421

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestEpochInspectionEvidenceFinalAndRefusedBoundary(t *testing.T) {
	for _, mode := range []string{"observed", "bad_final", "open_requests"} {
		t.Run(mode, func(t *testing.T) {
			fixture, value := epochTestFinal(t)
			if mode == "bad_final" {
				value.Projection.Catalog.Records++
			}
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: 1,
					ControlFileReads: 100, StoreReadAttempts: 10, MemberVisits: 1000}
				w.Header().Set("Trailer", epochReadTrailer)
				_, _ = w.Write(epochTestJSON(t, value, true))
				w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
			}))
			reader.run.epoch.Epoch = 1
			reader.authored, reader.projection, reader.tail, reader.progressReady = fixture.authored, fixture.projection, fixture.tail, true
			authority, projection, _, err := reader.Final(t.Context())
			if (err != nil) != (mode == "bad_final") {
				t.Fatal(err)
			}
			row := reader.evidence.rows[0]
			if row.ServerEpoch != 1 || row.FirstOrdinal != 1 || row.NextOrdinal != 2 || row.AcceptedReports != 1 || row.Reads != reader.totals || row.SelectorAccepted || (row.Final == nil) != (mode == "bad_final") {
				t.Fatal(row)
			}
			if row.Final != nil {
				if row.Final.Authority != authority.AuthorityState || !reflect.DeepEqual(row.Final.Projection, projection) {
					t.Fatal("native F discarded")
				}
				projection.ExtractionRoots[0].Domain = "returned-mutated"
				reader.projection.ExtractionRoots[0].Domain = "expected-mutated"
				if row.Final.Projection.ExtractionRoots[0].Domain == "returned-mutated" || row.Final.Projection.ExtractionRoots[0].Domain == "expected-mutated" {
					t.Fatal("final evidence aliases caller or expected projection")
				}
			}
			// This real HTTP fixture has no completed request fence. Even a
			// successful F body/trailer cannot accept its selector on its own.
			if reader.acceptInspectionPhase(t.Context()) == nil || reader.evidence.rows[0].SelectorAccepted {
				t.Fatal("unfenced or refused boundary accepted")
			}
		})
	}
}

func TestEpochInspectionEvidencePhaseAndCheckpointPrefixes(t *testing.T) {
	newReader := func(epoch uint64) *executionEpochInspection {
		reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ordinal, _ := strconv.ParseUint(r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
			report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: ordinal,
				ControlFileReads: ordinal, StoreReadAttempts: 2 * ordinal, MemberVisits: 3 * ordinal}
			w.Header().Set("Trailer", epochReadTrailer)
			_, _ = w.Write([]byte("{}"))
			w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
		}))
		reader.run.epoch.Epoch = epoch
		return reader
	}
	reader := newReader(3)
	for _, phase := range []string{"return_a", "return_a", "stale_lease", "process_restart"} {
		reader.projection.Phase = phase
		if _, _, _, err := reader.read(t.Context(), "/modeled", 16, epochInspectionReport{ControlFileReads: 20, StoreReadAttempts: 20, MemberVisits: 20}); err != nil {
			t.Fatal(err)
		}
	}
	rows := reader.evidence.rows
	if len(rows) != 3 || rows[0].FirstOrdinal != 1 || rows[0].NextOrdinal != 3 || rows[0].AcceptedReports != 2 ||
		rows[1].FirstOrdinal != 3 || rows[2].FirstOrdinal != 4 || rows[2].NextOrdinal != 5 {
		t.Fatal(rows)
	}
	var sum readaccounting.Counts
	for _, row := range rows {
		if row.Final != nil || row.SelectorAccepted {
			t.Fatal("read-only prefix invented final/acceptance")
		}
		sum.ControlFileReads += row.Reads.ControlFileReads
		sum.StoreReadAttempts += row.Reads.StoreReadAttempts
		sum.MemberVisits += row.Reads.MemberVisits
	}
	if sum != reader.totals {
		t.Fatal(sum, reader.totals)
	}
	// The same phase resumes with a new ordinal owner, not a copy of epoch3's
	// cumulative counts (and no DA/SA/process snapshot aggregation here).
	next := newReader(4)
	next.projection.Phase = "process_restart"
	if _, _, _, err := next.read(t.Context(), "/modeled", 16, epochInspectionReport{ControlFileReads: 20, StoreReadAttempts: 20, MemberVisits: 20}); err != nil {
		t.Fatal(err)
	}
	if next.evidence.rows[0].ServerEpoch != 4 || next.evidence.rows[0].FirstOrdinal != 1 || next.evidence.rows[0].Reads.ControlFileReads != 1 || rows[2].Reads.ControlFileReads != 4 {
		t.Fatal(rows, next.evidence.rows)
	}
}

func TestEpochInspectionEvidenceWaitDetached(t *testing.T) {
	rows := []ExecutionPhaseInspection{{Phase: "cold", Final: &ExecutionInspectionFinal{Projection: PhaseStateProjection{
		ExtractionRoots: []ExtractionRootProjection{{Domain: "go"}}, RelationshipResults: []RelationshipResult{{Name: "rpc"}},
	}}}}
	run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{}, done: make(chan struct{}), result: ExecutionEpochOneResult{Inspection: rows}}
	close(run.done)
	first, err := run.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first.Inspection[0].Phase = "mutated"
	first.Inspection[0].Final.Authority.PhysicalCommit = "mutated"
	first.Inspection[0].Final.Projection.ExtractionRoots[0].Domain = "mutated"
	first.Inspection[0].Final.Projection.RelationshipResults[0].Name = "mutated"
	second, err := run.Wait(t.Context())
	if err != nil || !reflect.DeepEqual(second.Inspection, rows) || second.Inspection[0].Final.Projection.ExtractionRoots[0].Domain != "go" {
		t.Fatal(second.Inspection, err)
	}
}
