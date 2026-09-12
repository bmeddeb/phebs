package t421

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
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
	for _, mode := range []string{"archive", "start", "finish", "over_limit", "malformed", "wrong_point", "wrong_epoch", "missing_start", "canceled"} {
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
			valid := mode == "archive" || mode == "start" || mode == "finish"
			if (err == nil) != valid {
				t.Fatal("sample disposition", err)
			}
			row := reader.restoredSamples.Phases[index]
			if valid || mode == "over_limit" {
				completed := uint64(1)
				logical := uint64(10)
				if mode == "finish" {
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
			op, cancel, done, err := run.beginRestoredExecution(ctx, mode == "unaccepted_collection")
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
			err := run.startCollectionDeadline(ctx)
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
