package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

const t422CheckpointTestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// Supplied native-shaped identities only; these do not prove durable controls.
func t422CheckpointTestIdentities() (generationscheduler.TerminalClaim, store.GenerationStaleLeaseTransition, extractionpublication.RecoveryPreparationTarget) {
	target := extractionpublication.RecoveryPreparationTarget{Domain: "proto-contract", Ordinal: 2, Offset: 27,
		Schedule: store.GenerationSchedule{Repository: "test/repo", Generation: t422CheckpointTestDigest, Digest: t422CheckpointTestDigest}}
	event := store.GenerationStaleLeaseTransition{Point: store.GenerationStaleLeaseTransitionCheckpointHit,
		Repository: target.Schedule.Repository, Stage: extractionpublication.ScheduleStage, ResourceClass: store.GenerationResourceExtraction,
		Generation: target.Schedule.Generation, ScheduleDigest: target.Schedule.Digest, ChunkIdentity: t422CheckpointTestDigest,
		Offset: int64(target.Offset), Length: 1, Priority: store.GenerationPriorityNeverRun, ChunkStatus: store.GenerationChunkRunning,
		ScheduleStatus: store.GenerationScheduleActive, Leased: true, PrivateLeaseTokenDigest: store.GenerationLeaseTokenDigest("actual-fixture-claim")}
	claim := generationscheduler.TerminalClaim{Repository: event.Repository, Stage: event.Stage, ResourceClass: event.ResourceClass,
		Generation: event.Generation, ScheduleDigest: event.ScheduleDigest, ChunkIdentity: event.ChunkIdentity,
		Offset: event.Offset, Length: event.Length, Priority: event.Priority, LeaseTokenDigest: event.PrivateLeaseTokenDigest}
	return claim, event, target
}

func TestT422CheckpointClaimBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*generationscheduler.TerminalClaim, *store.GenerationStaleLeaseTransition, *extractionpublication.RecoveryPreparationTarget)
	}{
		{"exact", func(*generationscheduler.TerminalClaim, *store.GenerationStaleLeaseTransition, *extractionpublication.RecoveryPreparationTarget) {
		}},
		{"lease", func(c *generationscheduler.TerminalClaim, _ *store.GenerationStaleLeaseTransition, _ *extractionpublication.RecoveryPreparationTarget) {
			c.LeaseTokenDigest = t422CheckpointTestDigest
		}},
		{"claim", func(c *generationscheduler.TerminalClaim, _ *store.GenerationStaleLeaseTransition, _ *extractionpublication.RecoveryPreparationTarget) {
			c.ChunkIdentity = "other"
		}},
		{"point", func(_ *generationscheduler.TerminalClaim, e *store.GenerationStaleLeaseTransition, _ *extractionpublication.RecoveryPreparationTarget) {
			e.Point = store.GenerationStaleLeaseTransitionHit
		}},
		{"target", func(_ *generationscheduler.TerminalClaim, _ *store.GenerationStaleLeaseTransition, p *extractionpublication.RecoveryPreparationTarget) {
			p.Ordinal = 6
		}},
		{"global_offset", func(_ *generationscheduler.TerminalClaim, _ *store.GenerationStaleLeaseTransition, p *extractionpublication.RecoveryPreparationTarget) {
			p.Offset = 2
		}},
		{"schedule", func(_ *generationscheduler.TerminalClaim, _ *store.GenerationStaleLeaseTransition, p *extractionpublication.RecoveryPreparationTarget) {
			p.Schedule.Digest = "other"
		}},
		{"recovery", func(_ *generationscheduler.TerminalClaim, e *store.GenerationStaleLeaseTransition, _ *extractionpublication.RecoveryPreparationTarget) {
			e.Priority = store.GenerationPriorityStale
		}},
		{"attempt", func(c *generationscheduler.TerminalClaim, e *store.GenerationStaleLeaseTransition, _ *extractionpublication.RecoveryPreparationTarget) {
			c.Attempt, e.Attempt = 1, 1
		}},
		{"cutoff", func(_ *generationscheduler.TerminalClaim, e *store.GenerationStaleLeaseTransition, _ *extractionpublication.RecoveryPreparationTarget) {
			e.StaleBefore = time.Now()
		}},
		{"unleased", func(_ *generationscheduler.TerminalClaim, e *store.GenerationStaleLeaseTransition, _ *extractionpublication.RecoveryPreparationTarget) {
			e.Leased = false
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			claim, event, target := t422CheckpointTestIdentities()
			test.mutate(&claim, &event, &target)
			if got := t422CheckpointClaimMatches(claim, event, target); got != (test.name == "exact") {
				t.Fatal("claim binding", got)
			}
		})
	}
}

func TestT422CheckpointReportTailKeepsHookParked(t *testing.T) {
	for _, mode := range []string{"success", "sink", "panic", "body", "accounting", "observer_canceled", "not_parked", "wrong_lease"} {
		t.Run(mode, func(t *testing.T) {
			base, failures := t422StaleTestControl(t)
			_, event, _ := t422CheckpointTestIdentities()
			observer, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			control := &t422CheckpointControl{t422StaleControl: base, parked: mode != "not_parked"}
			control.hit.observer, control.hit.transition, control.hit.reading = observer, event, true
			value := extractionpublication.CheckpointRestartTransition{Point: event.Point, CheckpointStateDigest: t422CheckpointTestDigest, PrivateLeaseTokenDigest: event.PrivateLeaseTokenDigest}
			if mode == "wrong_lease" {
				value.PrivateLeaseTokenDigest = t422CheckpointTestDigest
			}
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4})
			if err != nil {
				t.Fatal(err)
			}
			controls := uint64(7)
			if mode == "accounting" {
				controls++
			}
			_ = readaccounting.Charge(ctx, readaccounting.ControlFileRead, controls)
			_ = readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 4)
			state := t421NewExactReadAccountingState(func([]byte) error {
				select {
				case <-control.hit.release:
					t.Error("report marked before sink joined")
				default:
				}
				if mode == "sink" {
					return errors.New("sink")
				}
				if mode == "panic" {
					panic("sink")
				}
				if mode == "observer_canceled" {
					cancel()
				}
				return nil
			}, func(error) {})
			state.active = true
			state.finishRead(httptest.NewRecorder(), ledger, 1, mode != "body", "complete", nil, nil, func(cause error) {
				if cause != nil {
					_ = control.stop(cause)
					return
				}
				_ = control.finishHit(value)
			})
			if control.hit.reported != (mode == "success") || control.terminal || control.parked != (mode != "not_parked") {
				t.Fatal("report moved held checkpoint")
			}
			select {
			case <-control.hit.release:
				if mode != "success" {
					t.Fatal("failed report admitted terminal callback")
				}
			default:
				if mode == "success" {
					t.Fatal("successful report unjoined")
				}
			}
			if mode != "success" && failures.Load() != 1 {
				t.Fatal("failure not sticky")
			}
			if mode == "success" && control.finishHit(value) == nil {
				t.Fatal("duplicate report accepted")
			}
		})
	}
}

func TestT422CheckpointClosedRoutesAndCosts(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		path, method := t422CheckpointHitPath, http.MethodGet
		if prepare {
			path, method = t422CheckpointPreparePath, http.MethodPost
		}
		for _, suffix := range []string{"", "?target=other", "?", "/extra"} {
			request := httptest.NewRequest(method, path+suffix, nil)
			if got := t422CheckpointRequest(request, prepare); got != (suffix == "") {
				t.Fatal("open route", request.URL, got)
			}
		}
	}
	counts := t422CheckpointPreparationLimits()
	if counts != (readaccounting.Counts{ControlFileReads: 119, StoreReadAttempts: 339, MemberVisits: 353296640, StoreWriteAttempts: 64}) {
		t.Fatal(counts)
	}
	if extractionpublication.CheckpointRestartTransitionControlFileReads != 7 || store.GenerationStaleLeaseTransitionStoreReadAttempts != 4 {
		t.Fatal("native R recipe changed")
	}
	base, failures := t422StaleTestControl(t)
	control := &t422CheckpointControl{t422StaleControl: base}
	// Absent selected bootstrap must fail before waiting for a report/capability.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if control.quiesce(ctx) == nil || failures.Load() != 1 || control.terminal {
		t.Fatal("unselected terminal accepted")
	}
}
