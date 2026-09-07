package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/t421extractionprojection"
)

func t422StaleFixtureFinal() (candidate.State, t421FinalAuthorityResponse) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	state := candidate.State{Repository: "test/repo", Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ManifestDigest: digest,
		GenerationDigest: digest, PolicyDigest: digest}
	response := t421FinalAuthorityResponse{Schema: t421FinalAuthoritySchema, Authority: t421FinalAuthorityState{
		PhysicalCommit: state.Commit, CandidateGenerationSHA256: digest, SourceGenerationSHA256: digest,
		ObservationGenerationSHA256: digest, Current: true}}
	domains := []string{"grpc-caller", "grpc-consumer", "kafka-consumer", "kafka-producer", "proto-contract", "scip-proto-field", "thrift-caller", "thrift-consumer", "thrift-contract"}
	for i, domain := range domains {
		partitions := uint64(6)
		if i == 0 || i == 6 {
			partitions++
		}
		response.ExtractionRoots = append(response.ExtractionRoots, t421extractionprojection.RootResult{Domain: domain, Current: true,
			GenerationSHA256: digest, PlanSHA256: digest, RootSHA256: digest, CandidateGenerationSHA256: digest,
			SourceGenerationSHA256: digest, ObservationGenerationSHA256: digest, ApplicablePartitions: partitions})
	}
	return state, response
}

// This is a supplied bounded identity fixture, not a native nine-root F proof.
func TestT422StaleFinalSnapshot(t *testing.T) {
	state, response := t422StaleFixtureFinal()
	got, err := t422StaleFinalSnapshot(state, response)
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := candidate.ExtractionPolicyDigest(state.PolicyDigest, true)
	if got.authority.ExtractionPolicyDigest != policy || got.authority.CandidateManifestDigest != state.ManifestDigest ||
		got.final != response.Authority || got.roots[0].Domain != "grpc-caller" || got.generation != response.ExtractionRoots[0].GenerationSHA256 {
		t.Fatal("native scalar authority lost", got)
	}
	response.ExtractionRoots[0].RootSHA256 = "changed"
	if got.roots[0].RootDigest == "changed" {
		t.Fatal("captured slice aliases report")
	}
	for _, test := range []struct {
		name   string
		mutate func(*candidate.State, *t421FinalAuthorityResponse)
	}{
		{"manifest", func(s *candidate.State, _ *t421FinalAuthorityResponse) { s.ManifestDigest = "" }},
		{"candidate", func(s *candidate.State, _ *t421FinalAuthorityResponse) { s.GenerationDigest = "" }},
		{"commit", func(s *candidate.State, _ *t421FinalAuthorityResponse) { s.Commit = "other" }},
		{"not_current", func(_ *candidate.State, r *t421FinalAuthorityResponse) { r.Authority.Current = false }},
		{"missing_root", func(_ *candidate.State, r *t421FinalAuthorityResponse) { r.ExtractionRoots = r.ExtractionRoots[:8] }},
		{"unsorted", func(_ *candidate.State, r *t421FinalAuthorityResponse) { slices.Reverse(r.ExtractionRoots) }},
		{"duplicate", func(_ *candidate.State, r *t421FinalAuthorityResponse) { r.ExtractionRoots[1] = r.ExtractionRoots[0] }},
		{"operational_root", func(_ *candidate.State, r *t421FinalAuthorityResponse) {
			r.ExtractionRoots[0].ScheduleSHA256 = r.ExtractionRoots[0].PlanSHA256
		}},
		{"other_source", func(_ *candidate.State, r *t421FinalAuthorityResponse) {
			r.ExtractionRoots[0].SourceGenerationSHA256 = ""
		}},
		{"other_generation", func(_ *candidate.State, r *t421FinalAuthorityResponse) { r.ExtractionRoots[1].GenerationSHA256 = "" }},
		{"wrong_partition_count", func(_ *candidate.State, r *t421FinalAuthorityResponse) { r.ExtractionRoots[2].ApplicablePartitions++ }},
		{"no_target", func(_ *candidate.State, r *t421FinalAuthorityResponse) {
			r.ExtractionRoots[0].Domain = "arbitrary-caller"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, r := t422StaleFixtureFinal()
			test.mutate(&s, &r)
			if value, err := t422StaleFinalSnapshot(s, r); err == nil || value != (t422StaleFinal{}) {
				t.Fatal("malformed authority retained", value, err)
			}
		})
	}
}

// Private barrier mechanics only: no bootstrap or native result is fabricated.
func t422StaleTestControl(t *testing.T) (*t422StaleControl, *atomic.Int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	failures := new(atomic.Int32)
	control := &t422StaleControl{ctx: ctx, cancel: cancel, requeued: make(chan struct{}), launch: &t422SemanticLaunch{fail: func(error) { failures.Add(1) }}}
	for _, barrier := range []*t422StaleBarrier{&control.hit, &control.recovered} {
		barrier.ready, barrier.release = make(chan struct{}), make(chan struct{})
	}
	return control, failures
}

func TestT422StaleExactReportTail(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		for _, mode := range []string{"success", "sink", "sink_panic", "body", "accounting", "observer_canceled", "observer_expired"} {
			t.Run(mode+map[bool]string{false: "/hit", true: "/recovered"}[recovered], func(t *testing.T) {
				control, failures := t422StaleTestControl(t)
				barrier := &control.hit
				if recovered {
					barrier = &control.recovered
					control.hit.reported = true
				}
				observer, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				if mode == "observer_expired" {
					observer, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
					defer cancel()
				}
				barrier.observer, barrier.reading = observer, true
				ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{ControlFileReads: 4, StoreReadAttempts: 4})
				if err != nil {
					t.Fatal(err)
				}
				controls := uint64(4)
				if mode == "accounting" {
					controls++
				}
				_ = readaccounting.Charge(ctx, readaccounting.ControlFileRead, controls)
				_ = readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 4)
				state := t421NewExactReadAccountingState(func([]byte) error {
					select {
					case <-barrier.release:
						t.Error("released before joined report")
					default:
					}
					if mode == "sink" {
						return errors.New("sink")
					}
					if mode == "sink_panic" {
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
					_ = control.finishReport(barrier)
				})
				select {
				case <-barrier.release:
					if mode != "success" {
						t.Fatal("failed report released native observer")
					}
				default:
					if mode == "success" {
						t.Fatal("successful report not released")
					}
				}
				if mode != "success" && failures.Load() != 1 {
					t.Fatal("terminal failure not sticky", failures.Load())
				}
				if mode == "success" && control.finishReport(barrier) == nil {
					t.Fatal("report replay accepted")
				}
			})
		}
	}
}

func TestT422StaleDeadlineAndCancellationIntersection(t *testing.T) {
	for _, mode := range []string{"observer_deadline", "phase_deadline", "caller_deadline", "observer_cancel", "control_cancel", "already_canceled"} {
		t.Run(mode, func(t *testing.T) {
			control, _ := t422StaleTestControl(t)
			parent := t.Context()
			want := time.Now().Add(time.Second)
			observer, stopObserver := context.WithDeadline(parent, want)
			defer stopObserver()
			control.phaseEnd = want.Add(time.Hour)
			if mode == "phase_deadline" {
				want = want.Add(-500 * time.Millisecond)
				control.phaseEnd = want
			}
			if mode == "caller_deadline" {
				var cancel context.CancelFunc
				want = want.Add(-500 * time.Millisecond)
				parent, cancel = context.WithDeadline(parent, want)
				defer cancel()
			}
			ctx, ledger, err := readaccounting.Start(parent, readaccounting.Counts{ControlFileReads: 1})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "already_canceled" {
				stopObserver()
			}
			operation, finish := control.operationContext(ctx, observer)
			deadline, ok := operation.Deadline()
			if !ok || !deadline.Equal(want) {
				t.Fatal("deadline extended or restarted", deadline, want)
			}
			if err := readaccounting.Charge(operation, readaccounting.ControlFileRead, 1); err != nil {
				t.Fatal("request ledger lost", err)
			}
			if mode == "observer_cancel" {
				stopObserver()
			}
			if mode == "control_cancel" {
				control.cancel()
			}
			if mode == "observer_cancel" || mode == "control_cancel" || mode == "already_canceled" {
				select {
				case <-operation.Done():
				case <-time.After(time.Second):
					t.Fatal("cancellation bridge did not join")
				}
			}
			finish()
			finish()
			if counts, err := ledger.Finish(); err != nil || counts.ControlFileReads != 1 {
				t.Fatal("observed prefix lost", counts, err)
			}
		})
	}
}

func TestT422StaleClosedRoutesAndLimits(t *testing.T) {
	launch := &t422SemanticLaunch{}
	state := &t421ExactReadAccountingState{semantic: launch, stale: &t422StaleControl{launch: launch}}
	for _, path := range []string{t422StalePreparePath, t422StaleHitPath, t422StaleRecoveredPath} {
		method := http.MethodGet
		if path == t422StalePreparePath {
			method = http.MethodPost
		}
		request := httptest.NewRequest(method, path, nil)
		if method == http.MethodGet {
			request.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
			request.Header.Set(t421ExactReadOrdinalHeader, "1")
			if state.staleRead(request) == nil {
				t.Fatal("fixed R absent")
			}
		}
		if !t422SemanticRequestRoute(request) || !t422StaleRequest(request, path) {
			t.Fatal("fixed route absent", path)
		}
		for _, change := range []func(*http.Request){
			func(r *http.Request) { r.URL.RawQuery = "target=other" },
			func(r *http.Request) { r.URL.ForceQuery = true },
			func(r *http.Request) { r.ContentLength = 1 },
			func(r *http.Request) { r.TransferEncoding = []string{"chunked"} },
			func(r *http.Request) { r.Method = http.MethodPut },
		} {
			other := request.Clone(t.Context())
			change(other)
			if t422StaleRequest(other, path) || state.staleRead(other) != nil {
				t.Fatal("open target accepted")
			}
		}
	}
	if state.staleRead(httptest.NewRequest(http.MethodGet, t422StaleHitPath, bytes.NewBufferString("target"))) != nil {
		t.Fatal("body accepted")
	}
	state.semantic = nil
	if state.staleRead(httptest.NewRequest(http.MethodGet, t422StaleHitPath, nil)) != nil {
		t.Fatal("ordinary control installed")
	}
	want := readaccounting.Counts{ControlFileReads: 118, StoreReadAttempts: 339, MemberVisits: 353296640, StoreWriteAttempts: 64}
	if t422StalePreparationLimits() != want || extractionpublication.StaleLeaseTransitionControlFileReads != 4 || store.GenerationStaleLeaseTransitionStoreReadAttempts != 4 {
		t.Fatal("native recipe ceiling changed")
	}
}

func TestT422StaleConstructorRefusesUnbound(t *testing.T) {
	if control, err := newT422StaleControl(t.Context(), nil, nil); control != nil || err == nil {
		t.Fatal("unbound control accepted")
	}
}
