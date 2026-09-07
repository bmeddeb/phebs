package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
)

func compactTestJob(event string, attempt int) store.JobLifecycleReport {
	return store.JobLifecycleReport{Schema: store.JobLifecycleSchema, Event: event, JobID: "job:neutral", Kind: store.JobCandidate,
		Target: "example.test/neutral", Attempt: attempt, Outcome: executionJobOutcome(event)}
}
func compactTestChunk(event, outcome string, attempt int) generationscheduler.ChunkLifecycleReport {
	return generationscheduler.ChunkLifecycleReport{Schema: generationscheduler.ChunkLifecycleSchema, Event: event,
		Identity: "sha256:" + strings.Repeat("1", 64), Generation: "sha256:" + strings.Repeat("2", 64),
		Stage: extractionpublication.ScheduleStage, Attempt: attempt, Outcome: outcome}
}
func TestT422AttemptCompactNativeVocabulary(t *testing.T) {
	state := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	var reports []struct {
		report any
		want   string
	}
	for _, event := range []string{"claimed", "started", "done", "released", "deferred", "yielded", "requeued"} {
		for attempt := 1; attempt <= 3; attempt++ {
			if event == "requeued" && attempt == 3 {
				continue
			}
			want := ""
			if event == "started" {
				want = "A2j" + string(byte('0'+attempt)) + "\n"
			}
			if event == "requeued" {
				want = "A2r" + string(byte('0'+attempt)) + "\n"
			}
			reports = append(reports, struct {
				report any
				want   string
			}{compactTestJob(event, attempt), want})
		}
	}
	for _, outcome := range []string{"terminal", "attempts_exhausted"} {
		job := compactTestJob("failed", 3)
		job.Outcome = outcome
		reports = append(reports, struct {
			report any
			want   string
		}{job, ""})
	}
	for _, kind := range []store.JobKind{store.JobSync, store.JobIndex, store.JobFetch, store.JobCandidate, store.JobExtract, store.JobResolverCatalog, store.JobCallerLeaf} {
		job := compactTestJob("started", 1)
		job.Kind = kind
		reports = append(reports, struct {
			report any
			want   string
		}{job, "A2j1\n"})
	}
	for _, stage := range []string{observationpublication.PlanningScheduleStage, observationpublication.InventoryScheduleStageV2,
		observationpublication.ScheduleStage, extractionpublication.ScheduleStage, relationshippublication.ScheduleStage,
		relationshippublication.ScheduleStageV3, store.ServiceStateV3ReconcileStage, store.ServiceStateV3ActivateStage} {
		for attempt := 0; attempt < 5; attempt++ {
			chunk := compactTestChunk("started", "running", attempt)
			chunk.Stage = stage
			reports = append(reports, struct {
				report any
				want   string
			}{chunk, "A2c" + string(byte('0'+attempt)) + "\n"})
		}
	}
	for _, outcome := range []string{"handler_failed", "heartbeat_failed", "stale_fenced", "released", "release_failed", "pre_heartbeat_failed",
		"completed", "completion_failed", "terminal", "terminal_record_failed", "deferred", "deferral_failed", "retried", "exhausted"} {
		for attempt := 0; attempt < 5; attempt++ {
			if outcome == "retried" && attempt == 4 {
				continue
			}
			want := ""
			if outcome == "retried" {
				want = "A2t" + string(byte('1'+attempt)) + "\n"
			}
			reports = append(reports, struct {
				report any
				want   string
			}{compactTestChunk("settled", outcome, attempt), want})
		}
	}
	for _, test := range reports {
		raw, err := json.Marshal(test.report)
		if err != nil {
			t.Fatal(err)
		}
		kind := "job"
		if _, ok := test.report.(generationscheduler.ChunkLifecycleReport); ok {
			kind = "chunk"
		}
		got, err := t422AttemptRecord(state, state, kind, raw)
		if test.want == "" {
			if err != nil || got != ([5]byte{}) {
				t.Fatalf("%+v: %q %v", test.report, got, err)
			}
		} else if err != nil || string(got[:]) != test.want {
			t.Fatalf("%+v: %q %v", test.report, got, err)
		}
	}
}

func TestT422AttemptCompactRefusals(t *testing.T) {
	state := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	for _, name := range []string{"job zero", "job max", "job retry max", "job event", "job outcome", "job kind", "job identity", "job target", "job schema", "job wait", "job duration",
		"chunk negative", "chunk max", "chunk retry max", "chunk event", "chunk outcome", "chunk stage", "chunk identity", "chunk generation", "chunk schema", "chunk duration", "chunk started duration",
		"unknown kind", "unknown field", "duplicate field", "omitted field", "trailing", "partial", "oversized", "ordinary", "phase", "producer", "input", "zero delta invalid phase", "zero delta unknown field"} {
		t.Run(name, func(t *testing.T) {
			current := state
			kind := "job"
			job := compactTestJob("started", 1)
			chunk := compactTestChunk("started", "running", 0)
			switch name {
			case "job zero":
				job.Attempt = 0
			case "job max":
				job.Attempt = 4
			case "job retry max":
				job = compactTestJob("requeued", 3)
			case "job event":
				job.Event = "unknown"
			case "job outcome":
				job.Outcome = "unknown"
			case "job kind":
				job.Kind = "unknown"
			case "job identity":
				job.JobID = ""
			case "job target":
				job.Target = ""
			case "job schema":
				job.Schema = "unknown"
			case "job wait":
				job.QueueWaitMS = -1
			case "job duration":
				job.HandleMS = -1
			case "chunk negative":
				chunk.Attempt = -1
			case "chunk max":
				chunk.Attempt = 5
			case "chunk retry max":
				chunk = compactTestChunk("settled", "retried", 4)
			case "chunk event":
				chunk.Event = "unknown"
			case "chunk outcome":
				chunk.Outcome = "unknown"
			case "chunk stage":
				chunk.Stage = "service-relationship-v3-shadow-extra"
			case "chunk identity":
				chunk.Identity = "invalid"
			case "chunk generation":
				chunk.Generation = "invalid"
			case "chunk schema":
				chunk.Schema = "unknown"
			case "chunk duration":
				chunk.DurationMS = -1
			case "chunk started duration":
				chunk.DurationMS = 1
			case "ordinary":
				current.Mode = ""
			case "phase":
				current.Phase = 5
			case "producer":
				current.ProducerID = 3
			case "input":
				current.InputSHA256 = [32]byte{2}
			case "zero delta invalid phase":
				job = compactTestJob("claimed", 1)
				current.Phase = 5
			case "zero delta unknown field":
				job = compactTestJob("claimed", 1)
			}
			var value any = job
			if strings.HasPrefix(name, "chunk") {
				kind = "chunk"
				value = chunk
			}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "unknown kind":
				kind = "other"
			case "unknown field", "zero delta unknown field":
				raw = append([]byte(`{"extra":0,`), raw[1:]...)
			case "duplicate field":
				raw = append([]byte(`{"schema":"phebs-job-lifecycle-v1",`), raw[1:]...)
			case "omitted field":
				raw = []byte(strings.Replace(string(raw), `"handle_ms":0,`, "", 1))
			case "trailing":
				raw = append(raw, []byte("{}")...)
			case "partial":
				raw = raw[:len(raw)-1]
			case "oversized":
				raw = make([]byte, store.MaxJobLifecycleReportSize+1)
			}
			if got, err := t422AttemptRecord(current, state, kind, raw); err == nil || got != ([5]byte{}) {
				t.Fatalf("refusal=%q %v", got, err)
			}
		})
	}
}
