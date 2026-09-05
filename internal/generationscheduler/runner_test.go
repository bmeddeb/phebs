package generationscheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestChunkLifecycleReportBindsStartedLeaseIdentity(t *testing.T) {
	var reports [][]byte
	scheduler := &Scheduler{ChunkReports: func(value []byte) error {
		reports = append(reports, append([]byte(nil), value...))
		return nil
	}}
	chunk := store.GenerationChunk{
		Identity: "sha256:" + strings.Repeat("1", 64),
		Stage:    "extraction-partitions", Generation: "sha256:" + strings.Repeat("2", 64), Attempt: 3,
	}
	if err := scheduler.emitChunkLifecycle("started", chunk, "running"); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %d", len(reports))
	}
	var report ChunkLifecycleReport
	if err := json.Unmarshal(reports[0], &report); err != nil {
		t.Fatal(err)
	}
	if report.Schema != ChunkLifecycleSchema || report.Event != "started" ||
		report.Identity != chunk.Identity || report.Generation != chunk.Generation ||
		report.Attempt != chunk.Attempt || report.Outcome != "running" {
		t.Fatalf("report = %+v", report)
	}
}

func TestChunkLifecycleFailuresAreExactOnly(t *testing.T) {
	for _, test := range []struct {
		name      string
		sink      func([]byte) error
		oversized bool
	}{
		{"sink error", func([]byte) error { return errors.New("private sink cause") }, false},
		{"sink panic", func([]byte) error { panic("private sink panic") }, false},
		{"size limit", func([]byte) error { t.Fatal("oversized report reached sink"); return nil }, true},
	} {
		for _, exact := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/exact=%t", test.name, exact), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				fake := &schedulerStore{}
				failures, handled, gated := 0, 0, 0
				scheduler := &Scheduler{
					Store: fake, HeartbeatEvery: time.Hour, ChunkReports: test.sink,
				}
				if exact {
					scheduler.ChunkReportFailure = func(err error) {
						if err == nil || strings.Contains(err.Error(), "private") {
							t.Fatalf("failure contains private cause: %v", err)
						}
						failures++
						cancel()
					}
				}
				chunk := store.GenerationChunk{Identity: "chunk"}
				if test.oversized {
					chunk.Identity = strings.Repeat("x", MaxChunkLifecycleReportSize)
				}
				scheduler.execute(ctx, Class{
					BeforeLeaseHeartbeat: func(context.Context, store.GenerationChunk) error {
						gated++
						return nil
					},
					Handle: func(context.Context, store.GenerationChunk, Budget) error {
						handled++
						return nil
					},
				}, chunk)
				if exact {
					if failures != 1 || ctx.Err() == nil || handled != 0 || gated != 0 ||
						fake.completed+fake.retried+fake.failed+fake.released+fake.deferred != 0 {
						t.Fatalf("exact failure advanced work: failures=%d gated=%d handled=%d state=%+v",
							failures, gated, handled, fake)
					}
				} else if failures != 0 || ctx.Err() != nil || handled != 1 || gated != 1 || fake.completed != 1 {
					t.Fatalf("advisory failure changed work: failures=%d gated=%d handled=%d completed=%d",
						failures, gated, handled, fake.completed)
				}
			})
		}
	}
}

func TestExactChunkReportFailureStopsFurtherClaims(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fake := &schedulerStore{chunks: []store.GenerationChunk{
		{Identity: "first", ResourceClass: store.GenerationResourceCPU},
		{Identity: "second", ResourceClass: store.GenerationResourceCPU},
	}}
	scheduler := &Scheduler{
		Store: fake, PollEvery: time.Nanosecond,
		ChunkReports:       func([]byte) error { return errors.New("private sink cause") },
		ChunkReportFailure: func(error) { cancel() },
	}
	scheduler.work(ctx, store.GenerationResourceCPU, Class{
		Handle: func(context.Context, store.GenerationChunk, Budget) error {
			t.Fatal("handler ran after start report failed")
			return nil
		},
	}, 0)
	if len(fake.chunks) != 1 || fake.chunks[0].Identity != "second" {
		t.Fatalf("claims after report cancellation: %+v", fake.chunks)
	}
}

func TestExactChunkReportingCanceledAdmission(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*Scheduler, context.Context)
	}{
		{"plan", func(s *Scheduler, ctx context.Context) { s.plan(ctx, store.GenerationResourceCPU) }},
		{"reap", func(s *Scheduler, ctx context.Context) { s.reap(ctx, store.GenerationResourceCPU, nil) }},
		{"work", func(s *Scheduler, ctx context.Context) { s.work(ctx, store.GenerationResourceCPU, Class{}, 0) }},
		{"execute", func(s *Scheduler, ctx context.Context) { s.execute(ctx, Class{}, store.GenerationChunk{}) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			// No store or handler: any canceled native admission would panic.
			test.run(&Scheduler{PollEvery: time.Nanosecond, ChunkReportFailure: func(error) {}}, ctx)
		})
	}
}

func TestExactChunkSettledFailureFollowsDurableCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fake := &schedulerStore{}
	reports, failures := 0, 0
	scheduler := &Scheduler{
		Store: fake, HeartbeatEvery: time.Hour,
		ChunkReports: func([]byte) error {
			reports++
			if reports == 2 {
				return errors.New("private settled sink failure")
			}
			return nil
		},
		ChunkReportFailure: func(error) {
			if fake.completed != 1 {
				t.Fatal("settled failure preceded durable completion")
			}
			failures++
			cancel()
		},
	}
	scheduler.execute(ctx, Class{
		Handle: func(context.Context, store.GenerationChunk, Budget) error { return nil },
	}, store.GenerationChunk{})
	if reports != 2 || failures != 1 || ctx.Err() == nil || fake.completed != 1 {
		t.Fatalf("settled reports=%d failures=%d completed=%d canceled=%v", reports, failures, fake.completed, ctx.Err())
	}
}

func TestExactChunkReportFailureCallbackPanicStillRefuses(t *testing.T) {
	scheduler := &Scheduler{
		ChunkReports:       func([]byte) error { return errors.New("private failure") },
		ChunkReportFailure: func(error) { panic("private callback panic") },
	}
	if err := scheduler.emitChunkLifecycle("started", store.GenerationChunk{}, "running"); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("callback panic did not return bounded failure: %v", err)
	}
	// Neither native store access nor the handler may follow the failed report,
	// even when its faulty callback failed to cancel the context.
	scheduler.execute(t.Context(), Class{}, store.GenerationChunk{})
}

func TestExactChunkReportCancellationDuringGateDoesNotStartHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reports := 0
	scheduler := &Scheduler{
		ChunkReports:       func([]byte) error { reports++; return nil },
		ChunkReportFailure: func(error) { cancel() },
	}
	scheduler.execute(ctx, Class{
		BeforeLeaseHeartbeat: func(context.Context, store.GenerationChunk) error {
			cancel()
			return nil
		},
		// Nil handler/store prove neither starts after the gate cancels.
	}, store.GenerationChunk{})
	if reports != 1 {
		t.Fatalf("canceled gate invented a settled report: %d reports", reports)
	}
}

func TestExactChunkReportingRequiresSink(t *testing.T) {
	failures := 0
	scheduler := &Scheduler{
		Store:              &schedulerStore{},
		Classes:            map[store.GenerationResourceClass]Class{store.GenerationResourceCPU: {}},
		ChunkReportFailure: func(error) { failures++ },
	}
	if _, err := scheduler.validate(); err == nil || !strings.Contains(err.Error(), "requires a sink") {
		t.Fatalf("exact reporting without sink was admitted: %v", err)
	}
	if err := scheduler.emitChunkLifecycle("started", store.GenerationChunk{}, "running"); err == nil || failures != 1 {
		t.Fatalf("exact report fell back to advisory logging: err=%v failures=%d", err, failures)
	}
}

func TestCanceledHandlerReleaseReportsExactDurableOutcome(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		outcome string
	}{
		{"success", nil, "released"},
		{"failure", errors.New("private release failure"), "release_failed"},
		{"lost lease", fmt.Errorf("private lease: %w", store.ErrGenerationLeaseLost), "stale_fenced"},
		{"stale schedule", fmt.Errorf("private schedule: %w", store.ErrGenerationStale), "stale_fenced"},
	} {
		for _, exact := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/exact=%t", test.name, exact), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				fake := &schedulerStore{releaseErr: test.err}
				var reports []ChunkLifecycleReport
				scheduler := &Scheduler{
					Store: fake, HeartbeatEvery: time.Hour,
					ChunkReports: func(raw []byte) error {
						var report ChunkLifecycleReport
						if err := json.Unmarshal(raw, &report); err != nil {
							return err
						}
						reports = append(reports, report)
						return nil
					},
				}
				if exact {
					scheduler.ChunkReportFailure = func(err error) {
						t.Fatalf("reporting failed: %v", err)
					}
				}
				scheduler.execute(ctx, Class{
					Handle: func(context.Context, store.GenerationChunk, Budget) error {
						cancel()
						return ctx.Err()
					},
				}, store.GenerationChunk{})
				want := "released"
				if exact {
					want = test.outcome
				}
				if len(reports) != 2 || reports[1].Event != "settled" || reports[1].Outcome != want ||
					fake.released != 1 || fake.completed+fake.failed+fake.retried+fake.deferred != 0 {
					t.Fatalf("release outcome = %+v, want %q; release attempts=%d", reports, want, fake.released)
				}
			})
		}
	}
}

type schedulerStore struct {
	mu           sync.Mutex
	chunks       []store.GenerationChunk
	completed    int
	retried      int
	retryErrors  []string
	failed       int
	failErrors   []string
	released     int
	deferred     int
	deferDelay   time.Duration
	deferErrors  []string
	done         chan struct{}
	retryErr     error
	releaseErr   error
	heartbeatErr error
}

type observedSchedulerStore struct {
	*schedulerStore
	chunk         store.GenerationChunk
	observed      bool
	observedReaps int
	ordinaryReaps int
	heartbeats    int
}

func (state *observedSchedulerStore) HeartbeatGenerationChunk(
	context.Context,
	store.GenerationChunk,
) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.heartbeats++
	return nil
}

func (state *observedSchedulerStore) ReapStaleGenerationChunks(
	context.Context,
	store.GenerationResourceClass,
	time.Duration,
) (int, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.ordinaryReaps++
	return 0, nil
}

func (state *observedSchedulerStore) ReapStaleGenerationChunksObserved(
	ctx context.Context,
	_ store.GenerationResourceClass,
	_ time.Duration,
	observer store.GenerationStaleLeaseTransitionObserver,
) (int, error) {
	state.mu.Lock()
	if state.observed {
		state.mu.Unlock()
		return 0, nil
	}
	state.observed = true
	state.observedReaps++
	chunk := state.chunk
	state.mu.Unlock()
	hit := store.GenerationStaleLeaseTransition{
		Point:      store.GenerationStaleLeaseTransitionHit,
		Repository: chunk.Repository, Stage: chunk.Stage, Generation: chunk.Generation,
		ResourceClass: chunk.ResourceClass, ScheduleDigest: chunk.ScheduleDigest,
		ScheduleStatus: store.GenerationScheduleActive, ChunkIdentity: chunk.Identity,
		Offset: chunk.Offset, Length: chunk.Length, Attempt: chunk.Attempt,
		Priority: chunk.Priority, ChunkStatus: store.GenerationChunkRunning,
		Leased: true, ChunkStateDigest: "sha256:" + strings.Repeat("f", 64),
	}
	if err := observer(ctx, hit); err != nil {
		return 0, err
	}
	requeued := hit
	requeued.Point = store.GenerationStaleLeaseTransitionRequeued
	requeued.Priority = store.GenerationPriorityStale
	requeued.ChunkStatus = store.GenerationChunkPending
	requeued.Leased = false
	requeued.ChunkStateDigest = ""
	if err := observer(ctx, requeued); err != nil {
		return 1, err
	}
	return 1, nil
}

type blockedExpansionRecoveryStore struct {
	mu               sync.Mutex
	chunk            store.GenerationChunk
	available        bool
	claimed          bool
	completionFailed chan struct{}
	done             chan struct{}
	reaped           int
	completed        int
}

func newBlockedExpansionRecoveryStore() *blockedExpansionRecoveryStore {
	return &blockedExpansionRecoveryStore{
		chunk: store.GenerationChunk{
			ID: "wedged", Identity: "sha256:" + strings.Repeat("1", 64),
			ScheduleDigest: "sha256:" + strings.Repeat("2", 64),
			Repository:     "example.invalid/wedged", Stage: "extraction-partitions",
			Generation:    "sha256:" + strings.Repeat("3", 64),
			ResourceClass: store.GenerationResourceExtraction,
			Offset:        0, Length: 1, Status: store.GenerationChunkRunning,
			LeaseToken: "first-lease",
		},
		available: true, completionFailed: make(chan struct{}), done: make(chan struct{}),
	}
}

func (*blockedExpansionRecoveryStore) EnqueueGenerationSchedule(
	context.Context, store.GenerationScheduleSpec,
) (*store.GenerationSchedule, error) {
	return nil, errors.New("unused")
}

func (*blockedExpansionRecoveryStore) ExpandGenerationSchedule(
	context.Context, string, string, string,
) (*store.GenerationSchedule, error) {
	return nil, errors.New("unused")
}

func (*blockedExpansionRecoveryStore) ExpandNextGenerationSchedule(
	ctx context.Context, _ store.GenerationResourceClass,
) (*store.GenerationSchedule, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (state *blockedExpansionRecoveryStore) ClaimGenerationChunk(
	_ context.Context, _ store.GenerationResourceClass, worker string,
) (*store.GenerationChunk, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.available || state.claimed {
		return nil, store.ErrNotFound
	}
	state.available = false
	state.claimed = true
	chunk := state.chunk
	chunk.ClaimedBy = worker
	if state.reaped > 0 {
		chunk.LeaseToken = "recovered-lease"
	}
	state.chunk = chunk
	return &chunk, nil
}

func (*blockedExpansionRecoveryStore) HeartbeatGenerationChunk(
	context.Context, store.GenerationChunk,
) error {
	return nil
}

func (state *blockedExpansionRecoveryStore) CompleteGenerationChunk(
	_ context.Context, chunk store.GenerationChunk,
) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.completed == 0 {
		state.completed++
		close(state.completionFailed)
		return errors.New("transaction conflict: completion can be retried")
	}
	if chunk.LeaseToken != "recovered-lease" {
		return store.ErrGenerationLeaseLost
	}
	state.completed++
	close(state.done)
	return nil
}

func (*blockedExpansionRecoveryStore) FailGenerationChunk(
	context.Context, store.GenerationChunk, string,
) error {
	return errors.New("unused")
}

func (*blockedExpansionRecoveryStore) RetryGenerationChunk(
	context.Context, store.GenerationChunk, string, time.Time,
) (*store.GenerationChunk, error) {
	return nil, errors.New("unused")
}

func (*blockedExpansionRecoveryStore) DeferGenerationChunk(
	context.Context, store.GenerationChunk, string, time.Duration,
) error {
	return errors.New("unused")
}

func (*blockedExpansionRecoveryStore) ReleaseGenerationChunk(
	context.Context, store.GenerationChunk, string,
) error {
	return errors.New("unused")
}

func (state *blockedExpansionRecoveryStore) ReapStaleGenerationChunks(
	ctx context.Context, _ store.GenerationResourceClass, _ time.Duration,
) (int, error) {
	select {
	case <-state.completionFailed:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.reaped > 0 {
		return 0, nil
	}
	state.reaped++
	state.available = true
	state.claimed = false
	return 1, nil
}

func (*blockedExpansionRecoveryStore) GetGenerationSchedule(
	context.Context, string, string,
) (*store.GenerationSchedule, error) {
	return nil, store.ErrNotFound
}

func TestSchedulerReaperProgressIsIndependentOfBlockedExpansion(t *testing.T) {
	state := newBlockedExpansionRecoveryStore()
	ctx, cancel := context.WithCancel(t.Context())
	scheduler := &Scheduler{
		Store: state,
		Classes: map[store.GenerationResourceClass]Class{
			store.GenerationResourceExtraction: {
				Concurrency: 1,
				Budget:      Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
				Handle: func(context.Context, store.GenerationChunk, Budget) error {
					return nil
				},
			},
		},
		PollEvery: time.Millisecond, HeartbeatEvery: time.Second,
		StaleAfter: 5 * time.Second,
	}
	runDone := make(chan error, 1)
	go func() { runDone <- scheduler.Run(ctx) }()
	select {
	case <-state.done:
		cancel()
	case <-time.After(2 * time.Second):
		cancel()
		<-runDone
		t.Fatal("blocked expansion starved stale-lease recovery")
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.reaped != 1 || state.completed != 2 {
		t.Fatalf("reaped/completion attempts = %d/%d, want 1/2", state.reaped, state.completed)
	}
}

func TestSchedulerObservedStaleLeaseGateAndRecoveredCompletion(t *testing.T) {
	first := store.GenerationChunk{
		ID: "observed-stale", Identity: "sha256:" + strings.Repeat("1", 64),
		ScheduleDigest: "sha256:" + strings.Repeat("2", 64),
		Repository:     "example.invalid/observed-stale", Stage: "extraction-partitions",
		Generation:    "sha256:" + strings.Repeat("3", 64),
		ResourceClass: store.GenerationResourceExtraction,
		Offset:        6, Length: 1, Attempt: 0, Priority: store.GenerationPriorityNeverRun,
		Status: store.GenerationChunkRunning, LeaseToken: "first-lease",
	}
	state := &observedSchedulerStore{schedulerStore: &schedulerStore{}, chunk: first}
	gateEntered := make(chan struct{})
	requeued := make(chan struct{})
	var requeuedOnce sync.Once
	var pointsMu sync.Mutex
	var points []store.GenerationStaleLeaseTransitionPoint
	var recoveredLeaseDigest string
	recoveredObserverFailure := errors.New("recovered transition report failed")
	observer := func(_ context.Context, transition store.GenerationStaleLeaseTransition) error {
		pointsMu.Lock()
		points = append(points, transition.Point)
		pointsMu.Unlock()
		if transition.Point == store.GenerationStaleLeaseTransitionRequeued {
			requeuedOnce.Do(func() { close(requeued) })
		}
		if transition.Point == store.GenerationStaleLeaseTransitionRecovered {
			recoveredLeaseDigest = transition.PrivateLeaseTokenDigest
			return recoveredObserverFailure
		}
		return nil
	}
	var handled atomic.Int64
	configuration := Class{
		Concurrency: 1, Budget: Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
		Handle: func(context.Context, store.GenerationChunk, Budget) error {
			handled.Add(1)
			return nil
		},
		BeforeLeaseHeartbeat: func(ctx context.Context, chunk store.GenerationChunk) error {
			if chunk.LeaseToken != first.LeaseToken {
				return nil
			}
			close(gateEntered)
			select {
			case <-requeued:
				return store.ErrGenerationLeaseLost
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		OnStaleLeaseTransition: observer,
	}
	reports := make(chan error, 1)
	scheduler := &Scheduler{
		Store: state,
		Classes: map[store.GenerationResourceClass]Class{
			store.GenerationResourceExtraction: configuration,
		},
		PollEvery: time.Hour, HeartbeatEvery: time.Hour, StaleAfter: 2 * time.Hour,
		Report: func(err error) { reports <- err },
	}
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		scheduler.execute(t.Context(), configuration, first)
	}()
	select {
	case <-gateEntered:
	case <-time.After(time.Second):
		t.Fatal("first lease did not stop before its heartbeat")
	}
	reapCtx, cancelReap := context.WithCancel(t.Context())
	reapDone := make(chan struct{})
	go func() {
		defer close(reapDone)
		scheduler.reap(reapCtx, store.GenerationResourceExtraction, observer)
	}()
	select {
	case <-requeued:
		cancelReap()
	case <-time.After(time.Second):
		cancelReap()
		t.Fatal("observed reaper did not signal durable requeue")
	}
	<-reapDone
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("old lease did not leave the pre-heartbeat gate")
	}
	if handled.Load() != 0 {
		t.Fatalf("old lease reached handler %d times", handled.Load())
	}

	recovered := first
	recovered.Priority = store.GenerationPriorityStale
	recovered.LeaseToken = "recovered-lease"
	scheduler.execute(t.Context(), configuration, recovered)
	if handled.Load() != 1 {
		t.Fatalf("recovered lease handler calls = %d, want 1", handled.Load())
	}
	select {
	case report := <-reports:
		if !errors.Is(report, recoveredObserverFailure) {
			t.Fatalf("recovered observer report = %v", report)
		}
	default:
		t.Fatal("recovered observer failure was not reported")
	}
	state.mu.Lock()
	completed, heartbeats := state.completed, state.heartbeats
	observedReaps, ordinaryReaps := state.observedReaps, state.ordinaryReaps
	state.mu.Unlock()
	if completed != 1 || heartbeats != 0 || observedReaps != 1 || ordinaryReaps != 0 {
		t.Fatalf(
			"completed/heartbeats/observed/ordinary = %d/%d/%d/%d",
			completed, heartbeats, observedReaps, ordinaryReaps,
		)
	}
	pointsMu.Lock()
	defer pointsMu.Unlock()
	wantPoints := []store.GenerationStaleLeaseTransitionPoint{
		store.GenerationStaleLeaseTransitionHit,
		store.GenerationStaleLeaseTransitionRequeued,
		store.GenerationStaleLeaseTransitionRecovered,
	}
	if !slices.Equal(points, wantPoints) {
		t.Fatalf("transition points = %v, want %v", points, wantPoints)
	}
	if recoveredLeaseDigest != store.GenerationLeaseTokenDigest("recovered-lease") ||
		recoveredLeaseDigest == store.GenerationLeaseTokenDigest(first.LeaseToken) {
		t.Fatalf("recovered private lease digest = %q", recoveredLeaseDigest)
	}
}

func TestSchedulerAcceptsExtractionResourceClass(t *testing.T) {
	scheduler := &Scheduler{
		Store: &schedulerStore{},
		Classes: map[store.GenerationResourceClass]Class{
			store.GenerationResourceExtraction: {
				Concurrency: 1,
				Budget:      Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
				Handle:      func(context.Context, store.GenerationChunk, Budget) error { return nil },
			},
		},
	}
	classes, err := scheduler.validate()
	if err != nil || len(classes) != 1 || classes[0] != store.GenerationResourceExtraction {
		t.Fatalf("extraction scheduler validation = %v, %v", classes, err)
	}
}

func (scheduler *schedulerStore) EnqueueGenerationSchedule(context.Context, store.GenerationScheduleSpec) (*store.GenerationSchedule, error) {
	return nil, errors.New("unused")
}
func (scheduler *schedulerStore) ExpandGenerationSchedule(context.Context, string, string, string) (*store.GenerationSchedule, error) {
	return nil, errors.New("unused")
}
func (scheduler *schedulerStore) ExpandNextGenerationSchedule(context.Context, store.GenerationResourceClass) (*store.GenerationSchedule, error) {
	return nil, store.ErrNotFound
}
func (scheduler *schedulerStore) ClaimGenerationChunk(_ context.Context, class store.GenerationResourceClass, worker string) (*store.GenerationChunk, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	for index, chunk := range scheduler.chunks {
		if chunk.ResourceClass != class {
			continue
		}
		scheduler.chunks = append(scheduler.chunks[:index], scheduler.chunks[index+1:]...)
		chunk.ClaimedBy = worker
		return &chunk, nil
	}
	return nil, store.ErrNotFound
}
func (scheduler *schedulerStore) HeartbeatGenerationChunk(context.Context, store.GenerationChunk) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.heartbeatErr
}
func (scheduler *schedulerStore) CompleteGenerationChunk(_ context.Context, _ store.GenerationChunk) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.completed++
	if scheduler.done != nil && scheduler.completed+scheduler.retried+scheduler.failed == cap(scheduler.done) {
		close(scheduler.done)
	}
	return nil
}
func (scheduler *schedulerStore) FailGenerationChunk(_ context.Context, _ store.GenerationChunk, message string) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.failed++
	scheduler.failErrors = append(scheduler.failErrors, message)
	if scheduler.done != nil && scheduler.completed+scheduler.retried+scheduler.failed == cap(scheduler.done) {
		close(scheduler.done)
	}
	return nil
}
func (scheduler *schedulerStore) RetryGenerationChunk(_ context.Context, _ store.GenerationChunk, message string, _ time.Time) (*store.GenerationChunk, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.retried++
	scheduler.retryErrors = append(scheduler.retryErrors, message)
	if scheduler.done != nil && scheduler.completed+scheduler.retried+scheduler.failed == cap(scheduler.done) {
		close(scheduler.done)
	}
	if scheduler.retryErr != nil {
		return nil, scheduler.retryErr
	}
	return &store.GenerationChunk{}, nil
}

func (scheduler *schedulerStore) DeferGenerationChunk(
	_ context.Context,
	_ store.GenerationChunk,
	message string,
	delay time.Duration,
) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.deferred++
	scheduler.deferDelay = delay
	scheduler.deferErrors = append(scheduler.deferErrors, message)
	return nil
}

func TestSchedulerReportsExhaustionAfterRetryTransition(t *testing.T) {
	fake := &schedulerStore{retryErr: store.ErrGenerationExhausted}
	exhausted := make(chan error, 1)
	scheduler := &Scheduler{
		Store: fake, HeartbeatEvery: time.Hour,
		Backoff: func(int) time.Duration { return time.Millisecond },
	}
	configuration := Class{
		Handle: func(context.Context, store.GenerationChunk, Budget) error {
			return errors.New("temporary")
		},
		OnExhausted: func(_ context.Context, _ store.GenerationChunk, cause error) error {
			exhausted <- cause
			return nil
		},
	}
	scheduler.execute(t.Context(), configuration, store.GenerationChunk{
		ID: "exhausted", ResourceClass: store.GenerationResourceCPU,
		Status: store.GenerationChunkRunning, LeaseToken: "lease",
	})
	select {
	case cause := <-exhausted:
		if cause == nil || cause.Error() != "temporary" {
			t.Fatalf("exhausted cause = %v", cause)
		}
	default:
		t.Fatal("exhaustion callback was not called")
	}
}

func TestSchedulerDefersWithoutConsumingAttempt(t *testing.T) {
	tests := []struct {
		name  string
		cause error
	}{
		{name: "typed contention", cause: store.WithDeferral(errors.New("mutation lock busy"))},
		{name: "stale authority", cause: fmt.Errorf("authority changed: %w", store.ErrGenerationStale)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &schedulerStore{}
			scheduler := &Scheduler{
				Store: fake, PollEvery: 37 * time.Millisecond, HeartbeatEvery: time.Hour,
				Backoff: func(int) time.Duration { return time.Millisecond },
			}
			scheduler.execute(t.Context(), Class{
				Budget: Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
				Handle: func(context.Context, store.GenerationChunk, Budget) error {
					return test.cause
				},
			}, store.GenerationChunk{
				ID: "chunk", ResourceClass: store.GenerationResourceMemory,
				Status: store.GenerationChunkRunning, LeaseToken: "lease",
			})

			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.deferred != 1 || fake.retried != 0 || fake.failed != 0 ||
				fake.deferDelay != scheduler.storeCallTimeout()+scheduler.PollEvery ||
				len(fake.deferErrors) != 1 ||
				!strings.Contains(fake.deferErrors[0], test.cause.Error()) {
				t.Fatalf(
					"deferred/retried/failed/delay/errors = %d/%d/%d/%s/%q",
					fake.deferred, fake.retried, fake.failed,
					fake.deferDelay, fake.deferErrors,
				)
			}
		})
	}
}

func TestSchedulerHeartbeatFailureUsesDurableLeaseAge(t *testing.T) {
	fake := &schedulerStore{heartbeatErr: errors.New("transient heartbeat failure")}
	scheduler := &Scheduler{
		Store: fake, HeartbeatEvery: 5 * time.Millisecond,
		StaleAfter: 500 * time.Millisecond,
	}
	heartbeatAt := time.Now().Add(-time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		scheduler.execute(t.Context(), Class{
			Handle: func(ctx context.Context, _ store.GenerationChunk, _ Budget) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}, store.GenerationChunk{
			ID: "stale-heartbeat", ResourceClass: store.GenerationResourceCPU,
			Status: store.GenerationChunkRunning, LeaseToken: "lease",
			HeartbeatAt: &heartbeatAt,
		})
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("transient heartbeat outlived the durable lease stale cutoff")
	}
}

func TestSchedulerPersistsStructuralDurableErrorText(t *testing.T) {
	privateCause := errors.New("private /repo/path object deadbeef")
	refusal := pipelinerefusal.Unknown(
		privateCause,
		pipelinerefusal.StageDomainInventory,
		pipelinerefusal.GenerationExtractionDomain,
	)
	wrappedRefusal := fmt.Errorf("outer private token: %w", refusal)
	if !errors.Is(wrappedRefusal, privateCause) {
		t.Fatal("outer wrapper lost the refusal cause")
	}
	ordinary := fmt.Errorf("ordinary wrapper: %w", errors.New("ordinary cause"))
	terminalOrdinary := store.WithTerminal(ordinary)

	tests := []struct {
		name        string
		err         error
		want        string
		wantRetried int
		wantFailed  int
	}{
		{
			name: "retryable inner refusal", err: wrappedRefusal,
			want: refusal.Error(), wantRetried: 1,
		},
		{
			name: "retryable ordinary error", err: ordinary,
			want: ordinary.Error(), wantRetried: 1,
		},
		{
			name: "terminal inner refusal", err: store.WithTerminal(wrappedRefusal),
			want: refusal.Error(), wantFailed: 1,
		},
		{
			name: "terminal ordinary error", err: terminalOrdinary,
			want: terminalOrdinary.Error(), wantFailed: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &schedulerStore{}
			scheduler := &Scheduler{
				Store: fake, HeartbeatEvery: time.Hour,
				Backoff: func(int) time.Duration { return time.Millisecond },
			}
			scheduler.execute(t.Context(), Class{
				Budget: Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
				Handle: func(context.Context, store.GenerationChunk, Budget) error {
					return test.err
				},
			}, store.GenerationChunk{
				ID: "chunk", ResourceClass: store.GenerationResourceIO,
				Status: store.GenerationChunkRunning, LeaseToken: "lease",
			})

			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.retried != test.wantRetried || fake.failed != test.wantFailed {
				t.Fatalf(
					"retried/failed = %d/%d, want %d/%d",
					fake.retried, fake.failed, test.wantRetried, test.wantFailed,
				)
			}
			persisted := fake.retryErrors
			if test.wantFailed == 1 {
				persisted = fake.failErrors
			}
			if len(persisted) != 1 || persisted[0] != test.want {
				t.Fatalf("persisted errors = %q, want %q", persisted, test.want)
			}
		})
	}
}
func (scheduler *schedulerStore) ReleaseGenerationChunk(context.Context, store.GenerationChunk, string) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.released++
	return scheduler.releaseErr
}
func (scheduler *schedulerStore) ReapStaleGenerationChunks(context.Context, store.GenerationResourceClass, time.Duration) (int, error) {
	return 0, nil
}
func (scheduler *schedulerStore) GetGenerationSchedule(context.Context, string, string) (*store.GenerationSchedule, error) {
	return nil, store.ErrNotFound
}

func TestSchedulerEnforcesProcessBudgetsBeforeWorkersStart(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Scheduler)
	}{
		{name: "concurrency", configure: func(s *Scheduler) { s.MaxConcurrency = 1 }},
		{name: "memory", configure: func(s *Scheduler) { s.MaxMemoryBytes = 1 }},
		{name: "descriptors", configure: func(s *Scheduler) { s.MaxDescriptors = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheduler := &Scheduler{
				Store: &schedulerStore{},
				Classes: map[store.GenerationResourceClass]Class{
					store.GenerationResourceCPU: {
						Concurrency: 2, Budget: Budget{MaxMemoryBytes: 64, MaxDescriptors: 2},
						Handle: func(context.Context, store.GenerationChunk, Budget) error { return nil },
					},
				},
			}
			test.configure(scheduler)
			if err := scheduler.Run(t.Context()); err == nil {
				t.Fatal("invalid process budget started workers")
			}
		})
	}
}

func TestSchedulerAdmissionIsProcessWideAcrossInstances(t *testing.T) {
	newScheduler := func(concurrency int) *Scheduler {
		return &Scheduler{
			Store: &schedulerStore{},
			Classes: map[store.GenerationResourceClass]Class{
				store.GenerationResourceCPU: {
					Concurrency: concurrency,
					Budget:      Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
					Handle:      func(context.Context, store.GenerationChunk, Budget) error { return nil },
				},
			},
			PollEvery: time.Second, HeartbeatEvery: time.Second, StaleAfter: 5 * time.Second,
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	firstDone := make(chan error, 1)
	go func() { firstDone <- newScheduler(40).Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		processAdmission.Lock()
		admitted := processAdmission.concurrency
		processAdmission.Unlock()
		if admitted == 40 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first scheduler did not acquire process admission")
		}
		time.Sleep(time.Millisecond)
	}
	if err := newScheduler(25).Run(t.Context()); err == nil {
		t.Fatal("second scheduler exceeded the shared process ceiling")
	}
	cancel()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	processAdmission.Lock()
	defer processAdmission.Unlock()
	if processAdmission.concurrency != 0 || processAdmission.memoryBytes != 0 ||
		processAdmission.descriptors != 0 {
		t.Fatalf(
			"process admission leaked: concurrency=%d memory=%d descriptors=%d",
			processAdmission.concurrency, processAdmission.memoryBytes,
			processAdmission.descriptors,
		)
	}
}

func TestSchedulerBoundsConcurrencyAndPreservesChunkBudgets(t *testing.T) {
	const chunkCount = 6
	done := make(chan struct{}, chunkCount)
	fake := &schedulerStore{done: done}
	for index := range chunkCount {
		fake.chunks = append(fake.chunks, store.GenerationChunk{
			ID: fmt.Sprintf("chunk-%d", index), Identity: fmt.Sprintf("identity-%d", index),
			ScheduleDigest: "sha256:" + fmt.Sprintf("%064d", 1),
			Repository:     "example.invalid/repo", Stage: "stage",
			Generation:    "sha256:" + fmt.Sprintf("%064d", 2),
			ResourceClass: store.GenerationResourceCPU, Offset: int64(index), Length: 1,
			Status: store.GenerationChunkRunning, LeaseToken: fmt.Sprintf("lease-%d", index),
		})
	}
	var active atomic.Int32
	var maximum atomic.Int32
	handler := func(_ context.Context, _ store.GenerationChunk, budget Budget) error {
		if budget != (Budget{MaxMemoryBytes: 1 << 20, MaxDescriptors: 4}) {
			t.Errorf("handler budget = %+v", budget)
		}
		current := active.Add(1)
		for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
		}
		time.Sleep(10 * time.Millisecond)
		active.Add(-1)
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	scheduler := &Scheduler{
		Store: fake,
		Classes: map[store.GenerationResourceClass]Class{
			store.GenerationResourceCPU: {
				Concurrency: 2, Budget: Budget{MaxMemoryBytes: 1 << 20, MaxDescriptors: 4},
				Handle: handler,
			},
		},
		MaxConcurrency: 2, MaxMemoryBytes: 2 << 20, MaxDescriptors: 8,
		PollEvery: time.Millisecond, HeartbeatEvery: time.Second, StaleAfter: 5 * time.Second,
	}
	runDone := make(chan error, 1)
	go func() { runDone <- scheduler.Run(ctx) }()
	select {
	case <-done:
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not settle bounded chunks")
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 2 || fake.completed != chunkCount || fake.retried != 0 {
		t.Fatalf("execution maximum/completed/retried = %d/%d/%d", maximum.Load(), fake.completed, fake.retried)
	}
}

func TestSchedulerCreatesRetryTransitionWithoutDisturbingSuccess(t *testing.T) {
	done := make(chan struct{}, 2)
	fake := &schedulerStore{done: done, chunks: []store.GenerationChunk{
		{ID: "success", ResourceClass: store.GenerationResourceIO, Status: store.GenerationChunkRunning, LeaseToken: "a"},
		{ID: "retry", ResourceClass: store.GenerationResourceIO, Status: store.GenerationChunkRunning, LeaseToken: "b"},
	}}
	ctx, cancel := context.WithCancel(t.Context())
	scheduler := &Scheduler{
		Store: fake,
		Classes: map[store.GenerationResourceClass]Class{
			store.GenerationResourceIO: {
				Concurrency: 1, Budget: Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
				Handle: func(_ context.Context, chunk store.GenerationChunk, _ Budget) error {
					if chunk.ID == "retry" {
						return errors.New("retry")
					}
					return nil
				},
			},
		},
		PollEvery: time.Millisecond, HeartbeatEvery: time.Second, StaleAfter: 5 * time.Second,
		Backoff: func(int) time.Duration { return time.Millisecond },
	}
	runDone := make(chan error, 1)
	go func() { runDone <- scheduler.Run(ctx) }()
	select {
	case <-done:
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not record success and retry")
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if fake.completed != 1 || fake.retried != 1 {
		t.Fatalf("completed/retried = %d/%d", fake.completed, fake.retried)
	}
}
