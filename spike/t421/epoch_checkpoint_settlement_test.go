package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

// TestExecutionEpochCheckpointV7RecoveredPrecedesSettlement pins the V23 order:
// R arrives while deferred higher-offset siblings keep the schedule active.
// V7 accepts it for the selected chunk only; retained V6 stays exact.
func TestExecutionEpochCheckpointV7RecoveredPrecedesSettlement(t *testing.T) {
	reader, _, hit := epochCheckpointTestPreparation(t)
	hit.ObservedScheduleChunks = 56
	reader.checkpointHit = hit
	settled := hit
	settled.Point, settled.Priority, settled.ChunkStatus, settled.Leased = store.GenerationStaleLeaseTransitionRecovered, store.GenerationPriorityStale, store.GenerationChunkDone, false
	settled.ScheduleStatus, settled.ObservedScheduleSuccesses, settled.PrivateLeaseChanged = store.GenerationScheduleSettled, 56, true
	settled.CompletionBitSet, settled.RootExists, settled.Current = true, true, true
	for _, root := range reader.staleAuthority.ExtractionRoots {
		if root.Domain == hit.Domain {
			settled.RootDigest = root.RootSHA256
		}
	}
	active := settled
	active.ScheduleStatus, active.ObservedScheduleSuccesses = store.GenerationScheduleActive, 53
	unpublished := active
	unpublished.RootExists, unpublished.Current, unpublished.RootDigest = false, false, ""

	retained := reader.plan.Schema
	for name, test := range map[string]struct {
		value extractionpublication.CheckpointRestartTransition
		v6    bool
		v7    bool
	}{
		"settled all succeeded":              {value: settled, v6: true, v7: true},
		"active deferred siblings":           {value: active, v7: true},
		"active selected domain unpublished": {value: unpublished, v7: true},
		"active but all succeeded": {value: func() extractionpublication.CheckpointRestartTransition {
			v := active
			v.ObservedScheduleSuccesses = 56
			return v
		}()},
		"settled with missing successes": {value: func() extractionpublication.CheckpointRestartTransition {
			v := settled
			v.ObservedScheduleSuccesses = 53
			return v
		}()},
		"root without current": {value: func() extractionpublication.CheckpointRestartTransition {
			v := unpublished
			v.RootExists = true
			return v
		}()},
		"current without root digest": {value: func() extractionpublication.CheckpointRestartTransition {
			v := unpublished
			v.Current = true
			return v
		}()},
		"superseded schedule": {value: func() extractionpublication.CheckpointRestartTransition {
			v := active
			v.ScheduleStatus = store.GenerationScheduleSuperseded
			return v
		}()},
	} {
		t.Run(name, func(t *testing.T) {
			for _, schema := range []string{retained, PlanV7Schema} {
				reader.plan.Schema = schema
				want := test.v6
				if schema == PlanV7Schema {
					want = test.v7
				}
				if got := reader.validateCheckpoint(test.value, true) == nil; got != want {
					t.Fatalf("%s accepted=%t, want %t", schema, got, want)
				}
			}
		})
	}
}

func epochSettlementTestReader(t *testing.T, respond func(call int32) (int, store.GenerationScheduleSettlement)) (*executionEpochInspection, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	var digest string
	reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if r.URL.Path != recoverySettlementPath || r.URL.RawQuery != "" {
			t.Error("settlement request binding differs", r.URL.String())
		}
		status, value := respond(call)
		value.ScheduleDigest = digest
		ordinal := uint64(call)
		report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", Status: "complete", RequestOrdinal: ordinal, StoreReadAttempts: 1}
		w.Header().Add("Trailer", epochReadTrailer)
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write(epochTestJSON(t, value, false))
		} else {
			_, _ = w.Write([]byte("exact read request refused\n"))
		}
		w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
	}))
	digest = testDigest("recovery-schedule")
	reader.plan.Schema = PlanV7Schema
	reader.run.epoch.Epoch = 4
	reader.projection.Phase = "process_restart"
	reader.checkpointRecovered.Point = store.GenerationStaleLeaseTransitionRecovered
	reader.checkpointPreparation.RecoverySchedule = digest
	reader.bounds.ScheduleSettlementCalls = CounterBound{Minimum: 1, Maximum: 3}
	return reader, &calls
}

func settlementObservation(pending, succeeded, failed int, status store.GenerationScheduleStatus) store.GenerationScheduleSettlement {
	return store.GenerationScheduleSettlement{Status: status, TotalChunks: pending + succeeded + failed, Pending: pending, Succeeded: succeeded, Failed: failed}
}

func TestExecutionEpochCheckpointScheduleSettlementBarrier(t *testing.T) {
	t.Run("deferred siblings finish after R", func(t *testing.T) {
		reader, calls := epochSettlementTestReader(t, func(call int32) (int, store.GenerationScheduleSettlement) {
			if call == 1 {
				return http.StatusOK, settlementObservation(3, 53, 0, store.GenerationScheduleActive)
			}
			return http.StatusOK, settlementObservation(0, 56, 0, store.GenerationScheduleSettled)
		})
		if settled, err := reader.scheduleSettlement(t.Context()); err != nil || settled || reader.checkpointSettlement.Complete() {
			t.Fatal("pending observation", settled, err)
		}
		if reader.checkpointFinalMatches(reader.staleAuthority) {
			t.Fatal("F accepted before Z")
		}
		if settled, err := reader.scheduleSettlement(t.Context()); err != nil || !settled || !reader.checkpointSettlement.Complete() || calls.Load() != 2 {
			t.Fatal("settled observation", settled, err, calls.Load())
		}
		if _, err := reader.scheduleSettlement(t.Context()); err == nil {
			t.Fatal("Z repeated after proof")
		}
	})
	t.Run("phase deadline", func(t *testing.T) {
		reader, calls := epochSettlementTestReader(t, func(int32) (int, store.GenerationScheduleSettlement) {
			return http.StatusOK, settlementObservation(3, 53, 0, store.GenerationScheduleActive)
		})
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		if err := reader.awaitScheduleSettlement(ctx); err == nil || reader.err == nil || calls.Load() != 1 || reader.checkpointSettlement.Complete() {
			t.Fatal("deadline did not refuse", err, calls.Load())
		}
	})
	t.Run("call bound", func(t *testing.T) {
		reader, calls := epochSettlementTestReader(t, func(int32) (int, store.GenerationScheduleSettlement) {
			return http.StatusOK, settlementObservation(3, 53, 0, store.GenerationScheduleActive)
		})
		for range 3 {
			if _, err := reader.scheduleSettlement(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := reader.scheduleSettlement(t.Context()); err == nil || calls.Load() != 3 {
			t.Fatal("Z exceeded its inventory bound", calls.Load())
		}
	})
	for name, respond := range map[string]func(int32) (int, store.GenerationScheduleSettlement){
		"server refuses failure or replacement": func(int32) (int, store.GenerationScheduleSettlement) {
			return http.StatusConflict, store.GenerationScheduleSettlement{}
		},
		"settled with failure": func(int32) (int, store.GenerationScheduleSettlement) {
			return http.StatusOK, settlementObservation(0, 55, 1, store.GenerationScheduleSettled)
		},
		"superseded": func(int32) (int, store.GenerationScheduleSettlement) {
			return http.StatusOK, settlementObservation(3, 53, 0, store.GenerationScheduleSuperseded)
		},
		"inconsistent counters": func(int32) (int, store.GenerationScheduleSettlement) {
			value := settlementObservation(3, 53, 0, store.GenerationScheduleActive)
			value.TotalChunks++
			return http.StatusOK, value
		},
	} {
		t.Run(name, func(t *testing.T) {
			reader, _ := epochSettlementTestReader(t, respond)
			if settled, err := reader.scheduleSettlement(t.Context()); err == nil || settled || reader.err == nil {
				t.Fatal("refusal accepted", settled, err)
			}
		})
	}
	t.Run("retained V6 has no Z", func(t *testing.T) {
		reader, calls := epochSettlementTestReader(t, func(int32) (int, store.GenerationScheduleSettlement) {
			return http.StatusOK, settlementObservation(0, 56, 0, store.GenerationScheduleSettled)
		})
		reader.plan.Schema = PlanV6Schema
		if _, err := reader.scheduleSettlement(t.Context()); err == nil || calls.Load() != 0 {
			t.Fatal("V6 issued a settlement call")
		}
	})
}
