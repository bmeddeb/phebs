package t421

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// These are ownership/sequence models with real bounded HTTP and PC transport.
// They do not substitute for a genuine protected epoch-five constructor or
// claim that supplied authority/cycle/byte values were measured natively.
func configureRestoredTestReader(t *testing.T, reader *executionEpochInspection) {
	t.Helper()
	configureArchiveTestReader(t, reader)
	reader.run.flow = &ExecutionEpochOne{plan: reader.plan, workspace: &productionRoot{}}
	reader.run.inspection = reader
	projection, err := expectedStateProjectionForPhase(reader.plan, "lifecycle_collection")
	if err != nil {
		t.Fatal(err)
	}
	reader.projection = projection
	reader.run.epoch.CatalogSHA256 = projection.CatalogSource.SHA256
	rows, _, _ := correctedInspectionInventory(reader.plan.Profile)
	reader.bounds = rows[12]
	reader.archiveAuthority.Phase = "archive_restore"
	reader.restoredSamples.ArchiveComplete = true
}

func TestEpochRestoredSampleWire(t *testing.T) {
	for _, mode := range []string{"archive", "start", "finish", "product_start", "product_finish", "product_no_queries", "over_limit", "malformed", "wrong_point", "wrong_epoch", "missing_start", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/t422/lifecycle/sample-workspace" || r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "" {
					t.Error("sample route/ordinal changed")
				}
				if mode == "malformed" {
					_, _ = w.Write([]byte(`{"logical_bytes":10}`))
					return
				}
				_, _ = w.Write([]byte(`{"logical_bytes":10,"allocated_bytes":20}`))
			}))
			configureRestoredTestReader(t, reader)
			reader.plan.WorkEnvelope.MaximumDataLogicalBytes, reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes = 10, 20
			reader.restoredStep = 1
			point, index := "start", 1
			if mode == "archive" {
				reader.projection.Phase, reader.finalUsed, point, index = "archive_restore", true, "archive_finish", 0
			}
			if mode == "finish" || mode == "missing_start" {
				reader.restoredStep, reader.finalUsed, point = 3, true, "finish"
				if mode == "finish" {
					reader.restoredSamples.Phases[1] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1, Maximum: custodybytes.Sample{LogicalBytes: 11, AllocatedBytes: 9}}
				}
			}
			if strings.HasPrefix(mode, "product_") {
				configureProductTestReader(t, reader)
				index = 2
				if mode != "product_start" {
					point, reader.finalUsed, reader.productFinalCalls = "finish", true, 2
					reader.productQueriesComplete = mode == "product_finish"
					reader.restoredSamples.Phases[2] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1, Maximum: custodybytes.Sample{LogicalBytes: 11, AllocatedBytes: 9}}
				}
			}
			if mode == "over_limit" {
				reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes = 19
			}
			if mode == "wrong_point" {
				point = "normalized"
			}
			if mode == "wrong_epoch" {
				reader.run.epoch.Epoch = 4
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			_, err := reader.restoredSample(ctx, point)
			valid := mode == "archive" || mode == "start" || mode == "finish" || mode == "product_start" || mode == "product_finish"
			if (err == nil) != valid {
				t.Fatal("sample disposition", err)
			}
			row := reader.restoredSamples.Phases[index]
			if valid || mode == "over_limit" {
				completed := uint64(1)
				logical := uint64(10)
				if mode == "finish" || mode == "product_finish" {
					completed, logical = 2, 11
				}
				if row.Completed != completed || row.Attempts != completed || row.Maximum != (custodybytes.Sample{LogicalBytes: logical, AllocatedBytes: 20}) {
					t.Fatal("actual completed prefix/maxima lost", row)
				}
			}
			if mode == "over_limit" && (!reader.restoredSamples.LimitExceeded || reader.restoredSamples.Unavailable) {
				t.Fatal("limit excess mislabeled unavailable")
			}
			before := calls.Load()
			if _, err := reader.restoredSample(t.Context(), point); err == nil || calls.Load() != before || reader.restoredSamples.Phases[index] != row {
				t.Fatal("retry changed retained sample", err)
			}
		})
	}
}

func TestEpochRestoredFreshCycleWire(t *testing.T) {
	for _, mode := range []string{"valid", "normal79", "normal80", "job_backlog", "job_error", "job_exact", "other_backlog", "nonzero_read", "missing_trailer", "wrong_step"} {
		t.Run(mode, func(t *testing.T) {
			cycle := pressureTestCycle()
			if mode == "normal79" || mode == "normal80" {
				percent := 79
				if mode == "normal80" {
					percent = 80
				}
				cycle.Capacity.UsedPercent = percent
				cycle.Capacity.UsedBytes = cycle.Capacity.TotalBytes * int64(percent) / 100
				cycle.Capacity.AvailableBytes = cycle.Capacity.TotalBytes - cycle.Capacity.UsedBytes
				cycle.Capacity.ProjectedBytes = cycle.Capacity.UsedBytes
			}
			for i := range cycle.Owners {
				if cycle.Owners[i].Name == lifecycle.JobOwner {
					switch mode {
					case "job_backlog":
						cycle.Owners[i].Backlog = true
					case "job_error":
						cycle.Owners[i].State = "error"
					case "job_exact":
						cycle.Owners[i].Completeness = lifecycle.Exact
					}
				} else if mode == "other_backlog" {
					cycle.Owners[i].Backlog = true
				}
			}
			var calls atomic.Int32
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/api/t422/lifecycle/fresh-cycle" || r.Method != http.MethodGet {
					t.Error("wrong fresh route")
				}
				if mode != "missing_trailer" {
					w.Header().Set("Trailer", epochReadTrailer)
				}
				raw := epochTestJSON(t, cycle, false)
				_, _ = w.Write(raw[:len(raw)-1])
				report := epochInspectionReport{}
				if mode == "nonzero_read" {
					report.StoreReadAttempts = 1
				}
				if mode != "missing_trailer" {
					pressureInspectionTrailer(t, w, r, report)
				}
			}))
			configureRestoredTestReader(t, reader)
			reader.restoredStep = 2
			if mode == "wrong_step" {
				reader.restoredStep = 1
			}
			err := reader.freshCycle(t.Context())
			valid := mode == "valid" || mode == "normal79" || mode == "job_backlog"
			if (err == nil) != valid || valid && (reader.restoredStep != 3 || !reflect.DeepEqual(reader.collectionCycle, cycle)) {
				t.Fatal("fresh cycle validation", err, reader.collectionCycle)
			}
			if mode == "wrong_step" && calls.Load() != 0 {
				t.Fatal("wrong step consumed a request")
			}
			before := calls.Load()
			if reader.freshCycle(t.Context()) == nil || calls.Load() != before {
				t.Fatal("fresh R was repeatable")
			}
		})
	}
}

func TestEpochRestoredLifecycleStatusBound(t *testing.T) {
	for _, mode := range []string{"first", "last", "exhausted", "no_fresh", "no_archive", "after_final"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			handler := api.New(api.Options{IsAdmin: func(context.Context) bool { return true }, SelectedLifecycleCleanup: true,
				LifecycleStatusSource: func(context.Context) lifecycle.Status { return pressureInspectionStatus() }})
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Trailer", epochReadTrailer)
				handler.ServeHTTP(w, r)
				pressureInspectionTrailer(t, w, r, epochInspectionReport{})
			}))
			configureRestoredTestReader(t, reader)
			reader.restoredStep = 3
			switch mode {
			case "last":
				reader.lifecycleCalls = 2880
			case "exhausted":
				reader.lifecycleCalls = 2881
			case "no_fresh":
				reader.restoredStep = 2
			case "no_archive":
				reader.restoredSamples.ArchiveComplete = false
			case "after_final":
				reader.finalUsed = true
			}
			value, _, err := reader.LifecycleStatus(t.Context())
			valid := mode == "first" || mode == "last"
			if (err == nil) != valid || valid && value.Owners[0].State != "not_run" || !valid && calls.Load() != 0 {
				t.Fatal("L limit or truthful pending status changed", err, calls.Load())
			}
		})
	}
}

func TestEpochRestoredOperationRefusalAndCancellation(t *testing.T) {
	for _, mode := range []string{"nil_context", "workspace", "unhealthy", "pending_health", "expired", "busy", "repeated", "unaccepted_collection", "canceled_http"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-r.Context().Done()
			}))
			configureRestoredTestReader(t, reader)
			run := reader.run
			run.inspection = nil
			run.done = make(chan struct{})
			run.healthy, run.healthDone = true, make(chan struct{})
			if mode != "pending_health" {
				close(run.healthDone)
			}
			run.phaseDeadline, run.lifetimeDeadline = time.Now().Add(time.Minute), time.Now().Add(time.Hour)
			run.archiveInput, run.archivePrior = &reader.archiveInput, &reader.archivePrior
			ctx := t.Context()
			switch mode {
			case "nil_context":
				ctx = nil
			case "workspace":
				run.flow.workspace = nil
			case "unhealthy":
				run.healthy = false
			case "expired":
				run.phaseDeadline = time.Now().Add(-time.Second)
			case "busy":
				run.restoredExecutionDone = make(chan struct{})
			case "repeated":
				run.archiveExecutionUsed = true
			}
			phase := uint32(12)
			if mode == "unaccepted_collection" {
				phase = 13
			}
			op, cancel, done, err := run.beginRestoredExecution(ctx, phase)
			if mode != "canceled_http" {
				if err == nil {
					cancel()
					t.Fatal("invalid operation admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			reader.projection.Phase, reader.restoredStep, reader.progressReady = "archive_restore", 0, true
			reader.tail.Status = "ready"
			manifest := archiveReaderManifest()
			reader.archiveManifest = &manifest
			go func() {
				err := reader.restoredCommand(op, "park")
				run.finishRestoredExecution(cancel, done, err)
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			// The same cancellation and done join used by finish: no handler
			// operation may continue into native/control shutdown.
			run.restoredExecutionCancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("canceled operation did not join")
			}
			if run.err == nil || reader.err == nil {
				t.Fatal("cancellation did not latch")
			}
		})
	}
}

func TestEpochRestoredCollectionAuthority(t *testing.T) {
	base, native := epochTestFinal(t)
	projection, err := expectedStateProjectionForPhase(base.plan, "lifecycle_collection")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(projection)
	if json.Unmarshal(raw, &native.Projection) != nil {
		t.Fatal("projection fixture")
	}
	native.Projection.Schema = "t421-final-state-projection-source-free-v1"
	var prior AuthorityPhaseResult
	raw, _ = json.Marshal(native.Authority)
	if json.Unmarshal(raw, &prior.AuthorityState) != nil {
		t.Fatal("authority fixture")
	}
	prior.Phase, prior.Outcome, prior.PhysicalRevision, prior.LogicalRevision = "archive_restore", "passed", "a-return", "a-return"
	prior.ExtractionRoots = native.ExtractionRoots
	for _, mode := range []string{"same", "relationship", "caller", "root_detail", "catalog", "no_archive"} {
		t.Run(mode, func(t *testing.T) {
			var value epochFinalResponse
			if decodeEpochJSON(epochTestJSON(t, native, true), &value, true) != nil {
				t.Fatal("clone fixture")
			}
			switch mode {
			case "relationship":
				value.Authority.RelationshipGenerationSHA256 = testDigest("changed")
			case "caller":
				value.Authority.CallerRootSHA256 = testDigest("changed")
			case "root_detail":
				value.ExtractionRoots[0].Totals.Rows++
			case "catalog":
				value.Projection.Catalog.Records++
			}
			reader := &executionEpochInspection{plan: base.plan, run: &ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Epoch: 5}},
				projection: projection, archiveAuthority: cloneArchiveAuthority(prior), restoredSamples: ExecutionRestoredSamples{ArchiveComplete: mode != "no_archive"}}
			reader.tail = epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: value.Authority.RelationshipGenerationSHA256,
				RelationshipRootSHA256: value.Authority.RelationshipRootSHA256, CallerGenerationSHA256: value.Authority.CallerGenerationSHA256, CallerRootSHA256: value.Authority.CallerRootSHA256}
			if _, _, err := reader.decodeFinal(epochTestJSON(t, value, true)); (err == nil) != (mode == "same") {
				t.Fatal(mode, err)
			}
		})
	}
}

func TestEpochRestoredCollectionBoundary(t *testing.T) {
	for _, mode := range []string{"valid", "unaccepted", "missing_sample", "wrong_prior", "repeated"} {
		t.Run(mode, func(t *testing.T) {
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("boundary issued HTTP") }))
			configureRestoredTestReader(t, reader)
			reader.projection.Phase, reader.finalUsed, reader.restoredStep = "archive_restore", true, 1
			reader.next, reader.reports = 9, 8
			reader.evidence.rows = []ExecutionPhaseInspection{{Phase: "archive_restore", ServerEpoch: 5, SelectorAccepted: true}}
			switch mode {
			case "unaccepted":
				reader.evidence.rows[0].SelectorAccepted = false
			case "missing_sample":
				reader.restoredSamples.ArchiveComplete = false
			case "wrong_prior":
				reader.archiveAuthority.Phase = "pressure_75"
			case "repeated":
				reader.projection.Phase = "lifecycle_collection"
			}
			err := reader.beginCollection()
			if (err == nil) != (mode == "valid") || reader.next != 9 || reader.reports != 8 {
				t.Fatal("boundary reset ordinal or accepted missing prior", err)
			}
			if mode == "valid" && (reader.bounds.LifecycleStatusCalls.Maximum != 2881 || reader.finalUsed || reader.bounds.ExtractionProgressCalls.Maximum != 1 || reader.bounds.TailReadinessCalls.Maximum != 1) {
				t.Fatal("frozen phase13 inventory changed", reader.bounds)
			}
		})
	}
}

func TestEpochRestoredCollectionClosedPrefix(t *testing.T) {
	for _, mode := range []string{"valid", "phase14", "phase15", "archive_only", "no_sample", "unavailable", "unaccepted", "wrong_epoch", "no_DA_handoff", "no_SA_handoff", "backup_stage", "restore_stage"} {
		t.Run(mode, func(t *testing.T) {
			v := epochRestorePrefixFixture()
			v.Store.Opened, v.Store.TerminalEOF, v.Store.Store.Phase = 7, 7, 13
			v.Accounting.Producers[0].Closed, v.Accounting.Producers[0].Ordinal = true, 10
			v.Accounting.Producers = append(v.Accounting.Producers, dispatchadmission.ProducerCount{Producer: 6, Attached: true, Closed: true, Checkpoint: 12})
			v.Store.Store.Producers = append(v.Store.Store.Producers, storeaccounting.ProducerCount{Producer: 6, Attached: true, Closed: true, Checkpoint: 12})
			v.RestoredSamples.ArchiveComplete, v.RestoredSamples.CollectionComplete = true, true
			for i, phase := range []string{"archive_restore", "lifecycle_collection"} {
				v.RestoredSamples.Phases[i] = ExecutionWorkspaceBytePhase{Attempts: uint64(i + 1), Completed: uint64(i + 1)}
				v.Inspection = append(v.Inspection, ExecutionPhaseInspection{ServerEpoch: 5, Phase: phase, SelectorAccepted: true, Final: &ExecutionInspectionFinal{}})
			}
			switch mode {
			case "phase14":
				v.Store.Store.Phase = 14
			case "phase15":
				v.Store.Store.Phase = 15
			case "archive_only":
				v.Inspection = v.Inspection[:1]
			case "no_sample":
				v.RestoredSamples.Phases[1].Completed--
			case "unavailable":
				v.RestoredSamples.Unavailable = true
			case "unaccepted":
				v.Inspection[1].SelectorAccepted = false
			case "wrong_epoch":
				v.Inspection[1].ServerEpoch = 4
			case "no_DA_handoff":
				v.Accounting.Producers[len(v.Accounting.Producers)-1].Checkpoint = 0
			case "no_SA_handoff":
				v.Store.Store.Producers[len(v.Store.Store.Producers)-1].Checkpoint = 0
			}
			stage := uint32(6)
			if mode == "backup_stage" {
				stage = 10
			}
			if mode == "restore_stage" {
				stage = 11
			}
			if epochArchiveClosedPrefix(t.Context(), v, stage) != (mode == "valid") {
				t.Fatal("unsupported phase or incomplete joined prefix admitted")
			}
		})
	}
}

func TestEpochRestoredCollectionDeadline(t *testing.T) {
	for _, mode := range []string{"four_hours", "lifetime_clip", "canceled", "altered_bound", "unaccepted"} {
		t.Run(mode, func(t *testing.T) {
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("clock boundary issued HTTP") }))
			configureRestoredTestReader(t, reader)
			reader.projection.Phase, reader.finalUsed, reader.restoredStep = "archive_restore", true, 1
			reader.evidence.rows = []ExecutionPhaseInspection{{Phase: "archive_restore", ServerEpoch: 5, SelectorAccepted: true}}
			run := reader.run
			run.lifetimeDeadline = time.Now().Add(5 * time.Hour)
			if mode == "lifetime_clip" {
				run.lifetimeDeadline = time.Now().Add(time.Hour)
			}
			if mode == "altered_bound" {
				run.flow.plan.PhaseDeadlines = append([]PhaseDeadline(nil), run.flow.plan.PhaseDeadlines...)
				run.flow.plan.PhaseDeadlines[12].DeadlineMS++
			}
			if mode == "unaccepted" {
				reader.evidence.rows[0].SelectorAccepted = false
			}
			run.setPhaseDeadlineLocked(time.Now().Add(time.Minute))
			defer run.stopPhaseDeadline()
			old, oldDone := run.phaseDeadline, run.phaseDone
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			before := time.Now()
			err := run.startRestoredPhaseDeadline(ctx, 13)
			valid := mode == "four_hours" || mode == "lifetime_clip"
			if (err == nil) != valid {
				t.Fatal("phase clock disposition", err)
			}
			if !valid {
				if run.phaseDeadline != old || run.phaseDone != oldDone {
					t.Fatal("refusal renewed the phase clock")
				}
				return
			}
			select {
			case <-oldDone:
			default:
				t.Fatal("prior phase timer did not join")
			}
			if mode == "lifetime_clip" && run.phaseDeadline != run.lifetimeDeadline || mode == "four_hours" &&
				(run.phaseDeadline.Before(before.Add(4*time.Hour)) || run.phaseDeadline.After(time.Now().Add(4*time.Hour))) {
				t.Fatal("phase clock moved outside its unchanged bound")
			}
		})
	}
}

func configureProductTestReader(t *testing.T, reader *executionEpochInspection) {
	t.Helper()
	configureRestoredTestReader(t, reader)
	projection, err := expectedStateProjectionForPhase(reader.plan, "product_queries")
	if err != nil {
		t.Fatal(err)
	}
	reader.projection = projection
	rows, _, _ := correctedInspectionInventory(reader.plan.Profile)
	reader.bounds = rows[13]
	reader.restoredStep = 3
	reader.restoredSamples.CollectionComplete = true
	reader.collectionAuthority.Phase = "lifecycle_collection"
}

func TestEpochRestoredQueryTransport(t *testing.T) {
	for _, mode := range []string{"post", "get", "positive_oversize", "missing_trailer", "extra_read", "duplicate_content_type", "empty_post", "too_large_post", "foreign_post", "before_F", "after_queries", "phase13", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_code","arguments":{}}}`)
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "1" || r.Header.Get(dispatchadmission.ProductionRequestHeader) == "" {
					t.Error("query lost shared ordinal/request token")
				}
				if mode == "get" {
					if r.Method != http.MethodGet || r.URL.Path != "/api/search" {
						t.Error("GET query changed")
					}
				} else {
					raw, err := io.ReadAll(r.Body)
					if err != nil || r.Method != http.MethodPost || r.URL.Path != "/api/mcp" || string(raw) != string(payload) ||
						r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json, text/event-stream" {
						t.Error("MCP POST body/header changed", err)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "duplicate_content_type" {
					w.Header().Add("Content-Type", "text/event-stream")
				}
				if mode != "missing_trailer" {
					w.Header().Set("Trailer", epochReadTrailer)
				}
				_, _ = w.Write([]byte(`{"v":1}`))
				report := epochInspectionReport{ControlFileReads: 1}
				if mode == "extra_read" {
					report.ControlFileReads++
				}
				if mode != "missing_trailer" {
					pressureInspectionTrailer(t, w, r, report)
				}
			}))
			configureProductTestReader(t, reader)
			reader.productFinalCalls = 1
			path, limit := "/api/mcp", int64(7)
			switch mode {
			case "get":
				path, payload = "/api/search?q=needle", nil
			case "positive_oversize":
				limit = 6
			case "empty_post":
				payload = []byte{}
			case "too_large_post":
				payload = []byte(strings.Repeat("x", 64<<10+1))
			case "foreign_post":
				path = "/api/search"
			case "before_F":
				reader.productFinalCalls = 0
			case "after_queries":
				reader.productQueriesComplete = true
			case "phase13":
				reader.projection.Phase = "lifecycle_collection"
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			raw, status, contentType, _, err := reader.readQueryRequest(ctx, path, payload, limit, epochInspectionReport{ControlFileReads: 1}, false)
			valid := mode == "post" || mode == "get"
			if (err == nil) != valid || valid && (status != http.StatusOK || contentType != "application/json" || string(raw) != `{"v":1}`) {
				t.Fatal("query transport disposition", err, status, contentType, string(raw))
			}
			if mode == "positive_oversize" || mode == "duplicate_content_type" {
				if reader.reports != 1 || reader.totals.ControlFileReads != 1 || reader.next != 2 {
					t.Fatal("valid accounting prefix lost on response refusal")
				}
			}
			noHTTP := mode == "empty_post" || mode == "too_large_post" || mode == "foreign_post" || mode == "before_F" || mode == "after_queries" || mode == "phase13" || mode == "canceled"
			if noHTTP && (calls.Load() != 0 || reader.next != 1) {
				t.Fatal("refusal dispatched a request")
			}
		})
	}
}

func TestEpochRestoredProductBoundary(t *testing.T) {
	for _, mode := range []string{"valid", "no_acceptance", "no_collection", "no_authority", "already_begun", "no_finish"} {
		t.Run(mode, func(t *testing.T) {
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("boundary issued HTTP") }))
			configureProductTestReader(t, reader)
			reader.projection.Phase, reader.finalUsed = "lifecycle_collection", true
			reader.evidence.rows = []ExecutionPhaseInspection{{Phase: "lifecycle_collection", ServerEpoch: 5, SelectorAccepted: true}}
			reader.next, reader.reports = 18, 17
			switch mode {
			case "no_acceptance":
				reader.evidence.rows[0].SelectorAccepted = false
			case "no_collection":
				reader.restoredSamples.CollectionComplete = false
			case "no_authority":
				reader.collectionAuthority.Phase = "archive_restore"
			case "already_begun":
				reader.productFinalCalls = 1
			case "no_finish":
				reader.finalUsed = false
			}
			err := reader.beginProductInspection()
			if (err == nil) != (mode == "valid") || reader.next != 18 || reader.reports != 17 {
				t.Fatal("product boundary/ordinal changed", err)
			}
			if mode == "valid" && (reader.bounds.FinalAuthorityPasses != exactInspectionCalls(2) || reader.bounds.ExtractionProgressCalls != exactInspectionCalls(1) || reader.bounds.TailReadinessCalls != exactInspectionCalls(1) || reader.finalUsed) {
				t.Fatal("product inventory changed")
			}
		})
	}
}

func TestEpochRestoredProductDeadline(t *testing.T) {
	reader := epochTestHTTPReader(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("deadline issued HTTP") }))
	configureProductTestReader(t, reader)
	reader.projection.Phase, reader.finalUsed = "lifecycle_collection", true
	reader.evidence.rows = []ExecutionPhaseInspection{{Phase: "lifecycle_collection", ServerEpoch: 5, SelectorAccepted: true}}
	run := reader.run
	run.lifetimeDeadline = time.Now().Add(time.Hour)
	run.setPhaseDeadlineLocked(time.Now().Add(time.Minute))
	defer run.stopPhaseDeadline()
	before := time.Now()
	if err := run.startRestoredPhaseDeadline(t.Context(), 14); err != nil ||
		run.phaseDeadline.Before(before.Add(20*time.Minute)) || run.phaseDeadline.After(time.Now().Add(20*time.Minute)) {
		t.Fatal("phase14 did not start the frozen20-minute clock", err)
	}
	deadline := run.phaseDeadline
	if err := run.startRestoredPhaseDeadline(t.Context(), 15); err == nil || run.phaseDeadline != deadline {
		t.Fatal("phase15 renewed a clock")
	}
}

func TestEpochRestoredProductFinalBracket(t *testing.T) {
	for _, mode := range []string{"valid", "relationship", "root_detail", "query_authority", "resolver_namespace_generation", "resolver_namespace_root", "missing_query_authority", "missing_queries", "missing_row", "before_gap", "after_gap", "returned_alias"} {
		t.Run(mode, func(t *testing.T) {
			_, value := epochTestFinal(t)
			value.QueryAuthority = &epochQueryAuthority{CatalogSourceGenerationSHA256: testDigest("catalog-source"),
				ResolverNamespaceGenerationSHA256: testDigest("namespace-generation"), ResolverNamespaceRootSHA256: testDigest("namespace-root")}
			var calls atomic.Int32
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("X-Phebs-T422-Query-Evidence") != "bound-v1" {
					t.Error("phase14 F omitted query authority request")
				}
				w.Header().Set("Trailer", epochReadTrailer)
				_, _ = w.Write(epochTestJSON(t, value, true))
				pressureInspectionTrailer(t, w, r, epochInspectionReport{})
			}))
			configureProductTestReader(t, reader)
			raw, err := json.Marshal(reader.projection)
			if err != nil || json.Unmarshal(raw, &value.Projection) != nil {
				t.Fatal("projection fixture", err)
			}
			value.Projection.Schema = "t421-final-state-projection-source-free-v1"
			raw, err = json.Marshal(value.Authority)
			if err != nil || json.Unmarshal(raw, &reader.collectionAuthority.AuthorityState) != nil {
				t.Fatal("authority fixture", err)
			}
			reader.collectionAuthority.Outcome = "passed"
			reader.collectionAuthority.PhysicalRevision, reader.collectionAuthority.LogicalRevision = "a-return", "a-return"
			reader.collectionAuthority.ExtractionRoots = cloneArchiveAuthority(AuthorityPhaseResult{ExtractionRoots: value.ExtractionRoots}).ExtractionRoots
			reader.tail = epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: value.Authority.RelationshipGenerationSHA256,
				RelationshipRootSHA256: value.Authority.RelationshipRootSHA256, CallerGenerationSHA256: value.Authority.CallerGenerationSHA256, CallerRootSHA256: value.Authority.CallerRootSHA256}
			reader.progressReady, reader.next = true, 9
			// Hash the supplied bytes actually served by this HTTP fixture. This
			// proves retained wire binding, not native authority construction.
			firstDigest := sha256.Sum256(epochTestJSON(t, value, true))
			authority, _, _, err := reader.Final(t.Context())
			if err != nil || reader.productFinalCalls != 1 || reader.productFirstFinalOrdinal != 9 {
				t.Fatal("first actual HTTP F refused", err)
			}
			if reader.productFinalDigests != ([2][sha256.Size]byte{firstDigest, {}}) {
				t.Fatal("first F did not retain only its actual served-byte digest")
			}
			// The corridor is explicitly modeled here. The separate query driver
			// must obtain these rows from actual typed HTTP/MCP responses.
			reader.productQueries, reader.productQueriesComplete, reader.next = epochProductTestRows(t), true, 48
			switch mode {
			case "relationship":
				value.Authority.RelationshipRootSHA256 = testDigest("different root")
			case "root_detail":
				value.ExtractionRoots[0].Totals.Rows++
			case "query_authority":
				value.QueryAuthority.CatalogSourceGenerationSHA256 = testDigest("changed-catalog-source")
			case "resolver_namespace_generation":
				value.QueryAuthority.ResolverNamespaceGenerationSHA256 = testDigest("changed-namespace-generation")
			case "resolver_namespace_root":
				value.QueryAuthority.ResolverNamespaceRootSHA256 = testDigest("changed-namespace-root")
			case "missing_query_authority":
				value.QueryAuthority = nil
			case "missing_queries":
				reader.productQueriesComplete = false
			case "missing_row":
				reader.productQueries = reader.productQueries[:21]
			case "before_gap":
				reader.productFirstFinalOrdinal--
			case "after_gap":
				reader.next++
			case "returned_alias":
				authority.ExtractionRoots[0].Totals.Rows++
			}
			_, _, _, err = reader.Final(t.Context())
			valid := mode == "valid" || mode == "returned_alias"
			if (err == nil) != valid || valid && (reader.productFinalCalls != 2 || reader.evidence.rows[len(reader.evidence.rows)-1].Final.Ordinal != 48) {
				t.Fatal("second F did not enforce actual authority/ordinal bracket", err)
			}
			wantDigests := [2][sha256.Size]byte{firstDigest, {}}
			if valid {
				wantDigests[1] = sha256.Sum256(epochTestJSON(t, value, true))
			}
			if reader.productFinalDigests != wantDigests {
				t.Fatal("failed F changed the retained prefix or successful F lost its wire digest")
			}
			before := calls.Load()
			if _, _, _, err := reader.Final(t.Context()); err == nil || calls.Load() != before {
				t.Fatal("third/retried F dispatched")
			}
			if (mode == "missing_queries" || mode == "missing_row" || mode == "before_gap" || mode == "after_gap") && before != 1 {
				t.Fatal("incomplete corridor dispatched second F")
			}
		})
	}
}

func TestEpochRestoredQueryAuthorityScope(t *testing.T) {
	for _, schema := range []string{PlanSchema, PlanV2Schema, PlanV3Schema} {
		t.Run(schema, func(t *testing.T) {
			reader, value := epochTestFinal(t)
			reader.plan.Schema = schema
			value.QueryAuthority = &epochQueryAuthority{CatalogSourceGenerationSHA256: testDigest("catalog-source"),
				ResolverNamespaceGenerationSHA256: testDigest("namespace-generation"), ResolverNamespaceRootSHA256: testDigest("namespace-root")}
			if _, _, err := reader.decodeFinal(epochTestJSON(t, value, true)); err == nil || reader.productQueryAuthority != (epochQueryAuthority{}) {
				t.Fatal("non-phase14 extension changed the baseline")
			}
		})
	}
}

func TestEpochRestoredProductClosedPrefix(t *testing.T) {
	for _, mode := range []string{"valid", "phase15", "no_queries", "one_final", "no_completion", "before_gap", "after_gap", "missing_sample", "unaccepted", "no_DA_handoff", "no_SA_handoff"} {
		t.Run(mode, func(t *testing.T) {
			v := epochRestorePrefixFixture()
			v.Store.Opened, v.Store.TerminalEOF, v.Store.Store.Phase = 7, 7, 14
			v.Accounting.Producers[0].Closed, v.Accounting.Producers[0].Ordinal = true, 10
			v.Accounting.Producers = append(v.Accounting.Producers, dispatchadmission.ProducerCount{Producer: 6, Attached: true, Closed: true, Checkpoint: 13})
			v.Store.Store.Producers = append(v.Store.Store.Producers, storeaccounting.ProducerCount{Producer: 6, Attached: true, Closed: true, Checkpoint: 13})
			v.RestoredSamples.ArchiveComplete, v.RestoredSamples.CollectionComplete, v.RestoredSamples.ProductComplete = true, true, true
			v.ProductQueries, v.ProductFinals, v.ProductFirstFinalOrdinal = epochProductTestRows(t), 2, 9
			for i, phase := range []string{"archive_restore", "lifecycle_collection", "product_queries"} {
				count := min(uint64(i+1), 2)
				v.RestoredSamples.Phases[i] = ExecutionWorkspaceBytePhase{Attempts: count, Completed: count}
				v.Inspection = append(v.Inspection, ExecutionPhaseInspection{ServerEpoch: 5, Phase: phase, SelectorAccepted: true, Final: &ExecutionInspectionFinal{Ordinal: 48}})
			}
			switch mode {
			case "phase15":
				v.Store.Store.Phase = 15
			case "no_queries":
				v.ProductQueries = nil
			case "one_final":
				v.ProductFinals = 1
			case "no_completion":
				v.RestoredSamples.ProductComplete = false
			case "before_gap":
				v.ProductFirstFinalOrdinal--
			case "after_gap":
				v.Inspection[2].Final.Ordinal++
			case "missing_sample":
				v.RestoredSamples.Phases[2].Completed--
			case "unaccepted":
				v.Inspection[2].SelectorAccepted = false
			case "no_DA_handoff":
				v.Accounting.Producers[len(v.Accounting.Producers)-1].Checkpoint--
			case "no_SA_handoff":
				v.Store.Store.Producers[len(v.Store.Store.Producers)-1].Checkpoint--
			}
			if epochArchiveClosedPrefix(t.Context(), v, 6) != (mode == "valid") {
				t.Fatal("incomplete product terminal prefix admitted")
			}
		})
	}
}
