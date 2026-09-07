package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
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
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
)

type epochProgressRepoStore struct {
	store.Store
	store.GenerationSchedulerStore
	repository               store.Repo
	repoReads, scheduleReads int
}

func (*epochProgressRepoStore) RetireCurrentGenerationSchedule(context.Context, store.GenerationSchedule) error {
	panic("unexpected schedule mutation in read-only progress test")
}

func (state *epochProgressRepoStore) GetRepo(_ context.Context, name string) (*store.Repo, error) {
	state.repoReads++
	if name != state.repository.Name {
		return nil, store.ErrNotFound
	}
	value := state.repository
	return &value, nil
}

func (state *epochProgressRepoStore) GetGenerationSchedule(context.Context, string, string) (*store.GenerationSchedule, error) {
	state.scheduleReads++
	return nil, store.ErrNotFound
}

type epochUnavailableProgress struct{}

func (epochUnavailableProgress) Read(context.Context, string) (observationpublication.Progress, error) {
	return observationpublication.Progress{}, nil
}

func TestEpochInspectionProgressAcceptsCompleteAPIUnavailable(t *testing.T) {
	now := time.Now()
	state := &epochProgressRepoStore{repository: store.Repo{Name: "example.com/mono", IndexedCommitHash: strings.Repeat("a", 40), IndexedAt: &now}}
	opts := api.Options{Version: "test", Store: state}
	opts.ExtractionProgress = api.NewExtractionProgressService(opts, &extractionpublication.Runtime{Store: state})
	opts.ObservationProgress = api.NewObservationProgressService(opts, epochUnavailableProgress{})
	handler := api.New(opts)
	base := "http://127.0.0.1:12345"
	request := httptest.NewRequest(http.MethodGet, base+api.ExtractionProgressPath+"?repository="+state.repository.Name, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	value, err := decodeEpochProgress(recorder.Body.Bytes(), recorder.Code, base)
	if err != nil || value == nil || value.State != "unavailable" {
		t.Fatalf("complete API progress status=%d body=%q value=%+v error=%v", recorder.Code, recorder.Body.Bytes(), value, err)
	}
	if state.repoReads != 2 || state.scheduleReads != 1 {
		t.Fatalf("indexed/no-schedule reads = repo %d schedule %d", state.repoReads, state.scheduleReads)
	}
}

func epochTestJSON(t *testing.T, value any, indent bool) []byte {
	t.Helper()
	var raw []byte
	var err error
	if indent {
		raw, err = json.MarshalIndent(value, "", "  ")
	} else {
		raw, err = json.Marshal(value)
	}
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func TestEpochInspectionReportStrict(t *testing.T) {
	good := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", RequestOrdinal: 1, Status: "complete", ControlFileReads: 4, StoreReadAttempts: 4}
	maximum := epochInspectionReport{ControlFileReads: 4, StoreReadAttempts: 4}
	raw := bytes.TrimSuffix(epochTestJSON(t, good, false), []byte{'\n'})
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if got, err := decodeEpochReport([]string{encoded}, 1, maximum); err != nil || got != good {
		t.Fatal(got, err)
	}
	for _, test := range []struct {
		name    string
		values  []string
		ordinal uint64
	}{
		{"missing", nil, 1}, {"duplicate", []string{encoded, encoded}, 1}, {"ordinal", []string{encoded}, 2},
		{"padding", []string{encoded + "="}, 1}, {"oversize", []string{strings.Repeat("a", 1025)}, 1},
		{"duplicate field", []string{base64.RawURLEncoding.EncodeToString(bytes.Replace(raw, []byte(`"request_ordinal":1`), []byte(`"request_ordinal":1,"request_ordinal":1`), 1))}, 1},
		{"unknown field", []string{base64.RawURLEncoding.EncodeToString(append([]byte(`{"private":"no",`), raw[1:]...))}, 1},
		{"noncanonical", []string{base64.RawURLEncoding.EncodeToString(append(raw, '\n'))}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeEpochReport(test.values, test.ordinal, maximum); err == nil {
				t.Fatal("invalid report accepted")
			}
		})
	}
	for _, change := range []func(*epochInspectionReport){func(r *epochInspectionReport) { r.Status = "accounting_refused" }, func(r *epochInspectionReport) { r.ControlFileReads++ }, func(r *epochInspectionReport) { r.StoreReadAttempts++ }, func(r *epochInspectionReport) { r.MemberVisits++ }, func(r *epochInspectionReport) { r.StoreWriteAttempts++ }} {
		bad := good
		change(&bad)
		encoded := base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, bad, false), []byte{'\n'}))
		if _, err := decodeEpochReport([]string{encoded}, 1, maximum); err == nil {
			t.Fatal("invalid counts/status accepted", bad)
		}
	}
}

func TestEpochInspectionProgressClosedHumaShape(t *testing.T) {
	base := "http://127.0.0.1:12345"
	good := []byte(`{"$schema":"http://127.0.0.1:12345/schemas/ExtractionProgress.json","state":"current","total_partitions":2,"materialized":2,"pending":0,"running":0,"succeeded":2,"failed":0,"domains":1,"current_domains":1}` + "\n")
	if value, err := decodeEpochProgress(good, 200, base); err != nil || value.State != "current" {
		t.Fatal(value, err)
	}
	for _, detail := range []struct {
		status int
		detail string
	}{{404, "extraction progress not found"}, {409, "extraction progress changed; retry"}, {409, "extraction authority changed while building the response; retry"}} {
		raw := []byte(fmt.Sprintf(`{"$schema":%q,"title":%q,"status":%d,"detail":%q}`+"\n", base+"/schemas/ErrorModel.json", http.StatusText(detail.status), detail.status, detail.detail))
		if value, err := decodeEpochProgress(raw, detail.status, base); value != nil || err != nil {
			t.Fatal("known pending rejected", string(raw), err)
		}
		if _, err := decodeEpochProgress(bytes.Replace(raw, []byte(detail.detail), []byte("other error"), 1), detail.status, base); err == nil {
			t.Fatal("unknown status detail accepted")
		}
	}
	for _, raw := range [][]byte{bytes.Replace(good, []byte(`"current_domains":1`), []byte(`"current_domains":0`), 1), bytes.Replace(good, []byte(`"failed":0`), []byte(`"failed":0,"failed":0`), 1), append(good, good...), bytes.Replace(good, []byte(`"state":"current"`), []byte(`"state":"unknown"`), 1), bytes.Replace(good, []byte("127.0.0.1"), []byte("example.com"), 1)} {
		if _, err := decodeEpochProgress(raw, 200, base); err == nil {
			t.Fatal("invalid progress accepted")
		}
	}
}

// HTTP mechanics use genuine PC01 token generation but no native child or
// caller-provided constructor admission. Only this test builds a reader value.
func epochTestHTTPReader(t *testing.T, handler http.Handler) *executionEpochInspection {
	t.Helper()
	parent, child, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	control, err := dispatchadmission.NewPhaseControl(t.Context(), parent, [32]byte{1}, dispatchadmission.PhaseControlConfig{OwnerControl: true, Phases: []uint32{2, 3, 4}, InitialPhase: 2, MaximumPhases: 3, MaximumWireBytes: 1024, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close(); _ = child.Close() })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	plan := accountingTestPlan(t)
	rows, _, err := correctedInspectionInventory(plan.Profile)
	if err != nil {
		t.Fatal(err)
	}
	return &executionEpochInspection{run: &ExecutionEpochOneRun{control: control, stop: make(chan struct{}), done: make(chan struct{}), epoch: ExecutionEpochConfig{Listen: strings.TrimPrefix(server.URL, "http://"), APIKey: "private-key", Repository: "example.com/mono"}}, plan: plan, bounds: rows[1], next: 1}
}

func TestEpochInspectionHTTPOrdinalAndTrailerRefusal(t *testing.T) {
	for _, mode := range []string{"complete", "missing", "header-only", "duplicate", "redirect", "truncated", "wrong-ordinal", "write", "body-invalid", "body-oversize", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			var reader *executionEpochInspection
			reader = epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer private-key" || r.Header.Get(dispatchadmission.ProductionRequestHeader) != reader.run.control.RequestToken() || r.Header.Get("X-Phebs-T421-Exact-Reads") != "source-free-v1" || r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "1" || r.URL.Path != "/api/extraction-progress" || r.URL.Query().Get("repository") != "example.com/mono" {
					t.Error("request binding differs")
				}
				ordinal, _ := strconv.ParseUint(r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: ordinal, StoreReadAttempts: 1}
				if mode == "wrong-ordinal" {
					report.RequestOrdinal++
				}
				if mode == "write" {
					report.StoreWriteAttempts = 1
				}
				encoded := base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'}))
				if mode == "header-only" {
					w.Header().Set(epochReadTrailer, encoded)
				} else if mode != "missing" {
					w.Header().Add("Trailer", epochReadTrailer)
				}
				if mode == "redirect" {
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(http.StatusFound)
				}
				if mode == "truncated" {
					w.Header().Set("Content-Length", "10000")
				}
				state := "unavailable"
				if mode == "body-invalid" {
					state = "not-a-state"
				}
				_, _ = fmt.Fprintf(w, `{"$schema":%q,"state":%q,"total_partitions":0,"materialized":0,"pending":0,"running":0,"succeeded":0,"failed":0,"domains":0,"current_domains":0}`+"\n", "http://"+r.Host+"/schemas/ExtractionProgress.json", state)
				if mode == "body-oversize" {
					_, _ = w.Write(bytes.Repeat([]byte{' '}, api.ExtractionProgressResponseLimit))
				}
				if mode != "missing" && mode != "header-only" {
					w.Header().Set(epochReadTrailer, encoded)
					if mode == "duplicate" {
						w.Header().Add(epochReadTrailer, encoded)
					}
				}
			}))
			if mode == "overflow" {
				reader.totals.StoreReadAttempts = math.MaxUint64
			}
			result, report, err := reader.Progress(t.Context())
			if (err == nil) != (mode == "complete") {
				t.Fatal(mode, result, report, err)
			}
			if mode == "complete" {
				if reader.failureStatus != 0 || reader.failureBody != nil {
					t.Fatal("successful response retained as failed diagnostic")
				}
			} else if reader.failureStatus != result.HTTPStatus || len(reader.failureBody) == 0 || len(reader.failureBody) > api.ExtractionProgressResponseLimit+1 {
				t.Fatal("failed bounded response diagnostic missing", reader.failureStatus, len(reader.failureBody))
			}
			if mode == "body-oversize" && len(reader.failureBody) != api.ExtractionProgressResponseLimit+1 {
				t.Fatal("overflow diagnostic did not retain exactly one sentinel")
			}
			if reader.next != 2 || calls.Load() != 1 {
				t.Fatal("request ordinal/calls differs", reader.next, calls.Load())
			}
			if mode != "complete" {
				if _, _, err := reader.Progress(t.Context()); err == nil || calls.Load() != 1 || reader.run.err != ErrExecutionEpochOne {
					t.Fatal("uncertain request retried or failed latch lost")
				}
			}
			if mode == "complete" || mode == "redirect" || mode == "body-invalid" {
				if reader.reports != 1 || reader.totals.StoreReadAttempts != 1 {
					t.Fatal("accepted ledger prefix discarded", reader.reports, reader.totals)
				}
			}
			if mode == "overflow" && (reader.totals.StoreReadAttempts != math.MaxUint64 || reader.reports != 0) {
				t.Fatal("overflow changed prior exact prefix", reader.totals, reader.reports)
			}
			if mode == "complete" {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, _, err := reader.Progress(ctx); err == nil || reader.failureStatus != 0 || reader.failureBody != nil || calls.Load() != 1 {
					t.Fatal("pre-request failure inherited successful response")
				}
			}
		})
	}
}

func TestEpochInspectionFinalOnceAndAcceptedLedger(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			fixture, value := epochTestFinal(t)
			if bad {
				value.Projection.Catalog.Records++
			}
			var calls atomic.Int32
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/api/t421/final-authority" || r.URL.RawQuery != "" {
					t.Error("final route changed")
				}
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: 1, ControlFileReads: 100, StoreReadAttempts: 10, MemberVisits: 1000}
				w.Header().Set("Trailer", epochReadTrailer)
				_, _ = w.Write(epochTestJSON(t, value, true))
				w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
			}))
			reader.authored, reader.projection, reader.tail, reader.progressReady = fixture.authored, fixture.projection, fixture.tail, true
			_, _, _, err := reader.Final(t.Context())
			if (err != nil) != bad || reader.reports != 1 || reader.totals.ControlFileReads != 100 || reader.totals.StoreReadAttempts != 10 || reader.totals.MemberVisits != 1000 {
				t.Fatal("final admission or positive ledger differs", err, reader.totals)
			}
			if _, _, _, err := reader.Final(t.Context()); err == nil || calls.Load() != 1 {
				t.Fatal("final inspection retried", calls.Load(), err)
			}
		})
	}
}

func TestEpochInspectionTailClosedReadiness(t *testing.T) {
	for _, mode := range []string{"pending", "ready", "missing-digest", "incomplete-reads", "pending-with-digest", "wrong-status", "member-read"} {
		t.Run(mode, func(t *testing.T) {
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/t421/tail-readiness" || r.URL.RawQuery != "" {
					t.Error("tail route changed")
				}
				value := epochTailReadiness{Schema: "t421-tail-readiness-source-free-v1", Status: "ready", SelectedRuntimeSHA256: testDigest("runtime"), RelationshipGenerationSHA256: testDigest("relationship"), RelationshipRootSHA256: testDigest("relationship-root"), CallerGenerationSHA256: testDigest("caller"), CallerRootSHA256: testDigest("caller-root")}
				if mode == "pending" || mode == "pending-with-digest" {
					value = epochTailReadiness{Schema: value.Schema, Status: "pending"}
					if mode == "pending-with-digest" {
						value.CallerRootSHA256 = testDigest("unexpected")
					}
				}
				if mode == "missing-digest" {
					value.CallerRootSHA256 = ""
				}
				if mode == "wrong-status" {
					value.Status = "unknown"
				}
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: 1, ControlFileReads: 4, StoreReadAttempts: 4}
				if mode == "incomplete-reads" {
					report.StoreReadAttempts = 3
				}
				if mode == "member-read" {
					report.MemberVisits = 1
				}
				w.Header().Set("Trailer", epochReadTrailer)
				_, _ = w.Write(epochTestJSON(t, value, false))
				w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
			}))
			reader.progressReady = true
			value, _, err := reader.Tail(t.Context())
			if (err == nil) != (mode == "pending" || mode == "ready") {
				t.Fatal(mode, value, err)
			}
		})
	}
}

func epochTestFinal(t *testing.T) (*executionEpochInspection, epochFinalResponse) {
	t.Helper()
	plan := accountingTestPlan(t)
	physical := plan.Revisions.Physical[0]
	projection, err := expectedStateProjectionForPhase(plan, "cold")
	if err != nil {
		t.Fatal(err)
	}
	state := AuthorityState{PhysicalCommit: physical.ExpectedCommit, PhysicalTree: physical.ExpectedTree, SearchInventory: physical.ExpectedTreeInventory, ObservationInputInventory: physical.ExpectedObservationInputInventory, Current: true}
	v := reflect.ValueOf(&state).Elem()
	typ := v.Type()
	for index := 0; index < v.NumField(); index++ {
		if v.Field(index).Kind() == reflect.String && strings.HasSuffix(typ.Field(index).Name, "SHA256") {
			v.Field(index).SetString(testDigest("epoch-fixture", typ.Field(index).Name))
		}
	}
	roots := testExtractionRoots(t, plan, physical, state, "epoch-fixture")
	for index := range roots {
		roots[index].ScheduleSHA256 = ""
		roots[index].Members, err = extractionResultMembers(roots[index].PartitionResults)
		if err != nil {
			t.Fatal(err)
		}
	}
	state.ExtractionRootsSHA256 = mustReceiptSHA256(t, roots)
	reader := &executionEpochInspection{plan: plan, authored: AuthoredExecutionRevision{Name: "a", Commit: physical.ExpectedCommit, Tree: physical.ExpectedTree}, projection: projection, tail: epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: state.RelationshipGenerationSHA256, RelationshipRootSHA256: state.RelationshipRootSHA256, CallerGenerationSHA256: state.CallerGenerationSHA256, CallerRootSHA256: state.CallerRootSHA256}}
	value := epochFinalResponse{Schema: "t421-final-authority-source-free-v1", ExtractionRoots: roots}
	raw, _ := json.Marshal(state)
	if json.Unmarshal(raw, &value.Authority) != nil {
		t.Fatal("authority fixture conversion")
	}
	raw, _ = json.Marshal(projection)
	if json.Unmarshal(raw, &value.Projection) != nil {
		t.Fatal("projection fixture conversion")
	}
	value.Projection.Schema = "t421-final-state-projection-source-free-v1"
	return reader, value
}

func TestEpochInspectionFinalFullOracleAndWire(t *testing.T) {
	reader, value := epochTestFinal(t)
	raw := epochTestJSON(t, value, true)
	t.Logf("full frozen-oracle F wire fixture: %d bytes; modeled runtime digests, not native evidence", len(raw))
	if len(raw) > epochFinalResponseBytes {
		t.Fatal("full oracle exceeds native F capture")
	}
	if _, _, err := reader.decodeFinal(raw); err != nil {
		t.Fatal("full oracle rejected", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*epochFinalResponse)
	}{
		{"commit", func(v *epochFinalResponse) { v.Authority.PhysicalCommit = strings.Repeat("0", 40) }},
		{"tree", func(v *epochFinalResponse) { v.Authority.PhysicalTree = strings.Repeat("0", 40) }},
		{"not current", func(v *epochFinalResponse) { v.Authority.Current = false }},
		{"tail mismatch", func(v *epochFinalResponse) { v.Authority.CallerRootSHA256 = testDigest("other") }},
		{"projection", func(v *epochFinalResponse) { v.Projection.Catalog.Records++ }},
		{"extraction missing", func(v *epochFinalResponse) { v.ExtractionRoots = nil }},
		{"extraction result", func(v *epochFinalResponse) { v.ExtractionRoots[0].Totals.Rows++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var bad epochFinalResponse
			if json.Unmarshal(raw, &bad) != nil {
				t.Fatal("clone")
			}
			test.mutate(&bad)
			if _, _, err := reader.decodeFinal(epochTestJSON(t, bad, true)); err == nil {
				t.Fatal("wrong final authority accepted")
			}
		})
	}
	for _, bad := range [][]byte{append(raw, raw...), bytes.Replace(raw, []byte(`"current": true`), []byte(`"current": true, "current": true`), 1), bytes.Replace(raw, []byte(`"physical_commit":`), []byte(`"physical_revision": "a", "physical_commit":`), 1), bytes.Replace(raw, []byte(`"catalog_logical_sha256":`), []byte(`"phase": "cold", "catalog_logical_sha256":`), 1), bytes.Repeat([]byte{' '}, epochFinalResponseBytes+1)} {
		if _, _, err := reader.decodeFinal(bad); err == nil {
			t.Fatal("noncanonical or injected wire authority accepted")
		}
	}
}

func TestEpochInspectionProgressAcceptsActualHumaWire(t *testing.T) {
	_, api := humatest.New(t, huma.DefaultConfig("test", "test"))
	for _, status := range []int{200, 404, 409} {
		path := fmt.Sprintf("/progress-%d", status)
		type output struct {
			Body extractionpublication.Progress
		}
		huma.Register(api, huma.Operation{OperationID: fmt.Sprintf("progress-%d", status), Method: http.MethodGet, Path: path}, func(context.Context, *struct{}) (*output, error) {
			switch status {
			case 404:
				return nil, huma.Error404NotFound("extraction progress not found")
			case 409:
				return nil, huma.Error409Conflict("extraction progress changed; retry")
			}
			return &output{Body: extractionpublication.Progress{State: "unavailable"}}, nil
		})
		response := api.Get("http://127.0.0.1:12345" + path)
		value, err := decodeEpochProgress(response.Body.Bytes(), response.Code, "http://127.0.0.1:12345")
		if err != nil || status == 200 && (value == nil || value.State != "unavailable") || status != 200 && value != nil {
			t.Fatalf("actual Huma %d: %s; %+v; %v", status, response.Body.Bytes(), value, err)
		}
	}
}
