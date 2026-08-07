package generationscheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/store"
)

type schedulerStore struct {
	mu          sync.Mutex
	chunks      []store.GenerationChunk
	completed   int
	retried     int
	retryErrors []string
	released    int
	done        chan struct{}
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
	return nil
}
func (scheduler *schedulerStore) CompleteGenerationChunk(_ context.Context, _ store.GenerationChunk) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.completed++
	if scheduler.done != nil && scheduler.completed+scheduler.retried == cap(scheduler.done) {
		close(scheduler.done)
	}
	return nil
}
func (scheduler *schedulerStore) RetryGenerationChunk(_ context.Context, _ store.GenerationChunk, message string, _ time.Time) (*store.GenerationChunk, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.retried++
	scheduler.retryErrors = append(scheduler.retryErrors, message)
	if scheduler.done != nil && scheduler.completed+scheduler.retried == cap(scheduler.done) {
		close(scheduler.done)
	}
	return &store.GenerationChunk{}, nil
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

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "inner refusal", err: wrappedRefusal, want: refusal.Error()},
		{name: "ordinary error", err: ordinary, want: ordinary.Error()},
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
			if fake.retried != 1 || len(fake.retryErrors) != 1 ||
				fake.retryErrors[0] != test.want {
				t.Fatalf("retry/errors = %d/%q, want %q", fake.retried, fake.retryErrors, test.want)
			}
		})
	}
}
func (scheduler *schedulerStore) ReleaseGenerationChunk(context.Context, store.GenerationChunk, string) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.released++
	return nil
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
