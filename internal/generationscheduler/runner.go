// Package generationscheduler executes durable T35.1 work one bounded chunk
// at a time. It owns process-local resource admission; the store owns
// repository tokens, generation coalescing, leases, and retry successors.
package generationscheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	MaxProcessConcurrency       = 64
	MaxProcessMemoryBytes int64 = 8 << 30
	MaxProcessDescriptors       = 4_096
	MaxChunkMemoryBytes   int64 = 1 << 30
	MaxChunkDescriptors         = 256
)

type Budget struct {
	MaxMemoryBytes int64
	MaxDescriptors int
}

type Handler func(context.Context, store.GenerationChunk, Budget) error

// ExhaustedHandler records a source-free terminal retry result after the
// scheduler has atomically exhausted the final attempt. It runs only after the
// chunk is no longer leased, so implementations must re-fence the current
// schedule before installing any authority.
type ExhaustedHandler func(context.Context, store.GenerationChunk, error) error

type Class struct {
	Concurrency int
	Budget      Budget
	Handle      Handler
	OnExhausted ExhaustedHandler
}

type Scheduler struct {
	Store            store.GenerationSchedulerStore
	Classes          map[store.GenerationResourceClass]Class
	MaxConcurrency   int
	MaxMemoryBytes   int64
	MaxDescriptors   int
	PollEvery        time.Duration
	HeartbeatEvery   time.Duration
	StaleAfter       time.Duration
	StoreCallTimeout time.Duration
	Backoff          func(attempt int) time.Duration
	WorkerPrefix     string
	Report           func(error)
	Diagnostics      bool
	ChunkReports     func([]byte) error
}

const ChunkLifecycleSchema = "phebs-generation-chunk-lifecycle-v1"

type ChunkLifecycleReport struct {
	Schema     string `json:"schema"`
	Event      string `json:"event"`
	Identity   string `json:"identity"`
	Stage      string `json:"stage"`
	Generation string `json:"generation"`
	Attempt    int    `json:"attempt"`
	Outcome    string `json:"outcome"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

var processAdmission struct {
	sync.Mutex
	concurrency int
	memoryBytes int64
	descriptors int
}

func (scheduler *Scheduler) Run(ctx context.Context) error {
	classes, err := scheduler.validate()
	if err != nil {
		return err
	}
	releaseAdmission, err := scheduler.acquireProcessAdmission(classes)
	if err != nil {
		return err
	}
	defer releaseAdmission()
	var workers sync.WaitGroup
	for _, class := range classes {
		configuration := scheduler.Classes[class]
		workers.Add(2)
		go func() {
			defer workers.Done()
			scheduler.plan(ctx, class)
		}()
		go func() {
			defer workers.Done()
			scheduler.reap(ctx, class)
		}()
		for index := range configuration.Concurrency {
			workers.Add(1)
			go func(workerIndex int) {
				defer workers.Done()
				scheduler.work(ctx, class, configuration, workerIndex)
			}(index)
		}
	}
	<-ctx.Done()
	workers.Wait()
	return nil
}

func (scheduler *Scheduler) acquireProcessAdmission(
	classes []store.GenerationResourceClass,
) (func(), error) {
	concurrency := 0
	var memoryBytes int64
	descriptors := 0
	for _, class := range classes {
		configuration := scheduler.Classes[class]
		concurrency += configuration.Concurrency
		memoryBytes += int64(configuration.Concurrency) * configuration.Budget.MaxMemoryBytes
		descriptors += configuration.Concurrency * configuration.Budget.MaxDescriptors
	}
	processAdmission.Lock()
	defer processAdmission.Unlock()
	if processAdmission.concurrency+concurrency > MaxProcessConcurrency ||
		processAdmission.memoryBytes+memoryBytes > MaxProcessMemoryBytes ||
		processAdmission.descriptors+descriptors > MaxProcessDescriptors {
		return nil, errors.New("generation scheduler instances exceed process admission")
	}
	processAdmission.concurrency += concurrency
	processAdmission.memoryBytes += memoryBytes
	processAdmission.descriptors += descriptors
	var once sync.Once
	return func() {
		once.Do(func() {
			processAdmission.Lock()
			defer processAdmission.Unlock()
			processAdmission.concurrency -= concurrency
			processAdmission.memoryBytes -= memoryBytes
			processAdmission.descriptors -= descriptors
		})
	}, nil
}

func (scheduler *Scheduler) validate() ([]store.GenerationResourceClass, error) {
	if scheduler.Store == nil || len(scheduler.Classes) == 0 {
		return nil, errors.New("generation scheduler requires a store and classes")
	}
	if scheduler.MaxConcurrency == 0 {
		scheduler.MaxConcurrency = MaxProcessConcurrency
	}
	if scheduler.MaxMemoryBytes == 0 {
		scheduler.MaxMemoryBytes = MaxProcessMemoryBytes
	}
	if scheduler.MaxDescriptors == 0 {
		scheduler.MaxDescriptors = MaxProcessDescriptors
	}
	if scheduler.PollEvery == 0 {
		scheduler.PollEvery = 250 * time.Millisecond
	}
	if scheduler.HeartbeatEvery == 0 {
		scheduler.HeartbeatEvery = 5 * time.Second
	}
	if scheduler.StaleAfter == 0 {
		scheduler.StaleAfter = 4 * scheduler.HeartbeatEvery
	}
	if scheduler.StoreCallTimeout == 0 {
		scheduler.StoreCallTimeout = 5 * time.Second
	}
	if scheduler.Backoff == nil {
		scheduler.Backoff = deterministicBackoff
	}
	if scheduler.WorkerPrefix == "" {
		scheduler.WorkerPrefix = "generation-worker"
	}
	if scheduler.MaxConcurrency < 1 || scheduler.MaxConcurrency > MaxProcessConcurrency ||
		scheduler.MaxMemoryBytes < 1 || scheduler.MaxMemoryBytes > MaxProcessMemoryBytes ||
		scheduler.MaxDescriptors < 1 || scheduler.MaxDescriptors > MaxProcessDescriptors ||
		scheduler.PollEvery <= 0 || scheduler.HeartbeatEvery <= 0 ||
		scheduler.StaleAfter <= scheduler.HeartbeatEvery || scheduler.StoreCallTimeout <= 0 {
		return nil, errors.New("generation scheduler process bounds are invalid")
	}
	classes := make([]store.GenerationResourceClass, 0, len(scheduler.Classes))
	totalConcurrency := 0
	var totalMemory int64
	totalDescriptors := 0
	for class, configuration := range scheduler.Classes {
		if class != store.GenerationResourceCPU && class != store.GenerationResourceIO &&
			class != store.GenerationResourceMemory &&
			class != store.GenerationResourceExtraction {
			return nil, fmt.Errorf("generation scheduler resource class %q is invalid", class)
		}
		if configuration.Concurrency < 1 || configuration.Handle == nil ||
			configuration.Budget.MaxMemoryBytes < 1 ||
			configuration.Budget.MaxMemoryBytes > MaxChunkMemoryBytes ||
			configuration.Budget.MaxDescriptors < 1 ||
			configuration.Budget.MaxDescriptors > MaxChunkDescriptors {
			return nil, fmt.Errorf("generation scheduler class %q bounds are invalid", class)
		}
		totalConcurrency += configuration.Concurrency
		totalMemory += int64(configuration.Concurrency) * configuration.Budget.MaxMemoryBytes
		totalDescriptors += configuration.Concurrency * configuration.Budget.MaxDescriptors
		classes = append(classes, class)
	}
	if totalConcurrency > scheduler.MaxConcurrency || totalMemory > scheduler.MaxMemoryBytes ||
		totalDescriptors > scheduler.MaxDescriptors {
		return nil, errors.New("generation scheduler class budgets exceed process bounds")
	}
	slices.Sort(classes)
	return classes, nil
}

func deterministicBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := min(attempt-1, 6)
	return time.Second << shift
}

func (scheduler *Scheduler) storeCallTimeout() time.Duration {
	if scheduler.StoreCallTimeout > 0 {
		return scheduler.StoreCallTimeout
	}
	return 5 * time.Second
}

func (scheduler *Scheduler) pollEvery() time.Duration {
	if scheduler.PollEvery > 0 {
		return scheduler.PollEvery
	}
	return 250 * time.Millisecond
}

func (scheduler *Scheduler) deferralDelay() time.Duration {
	// The store call can consume its whole client timeout after computing the
	// persisted fence. Keep one complete poll interval after any successful
	// response so a different resource-class worker gets a deterministic
	// opportunity to claim the released repository token.
	return scheduler.storeCallTimeout() + scheduler.pollEvery()
}

func (scheduler *Scheduler) plan(ctx context.Context, class store.GenerationResourceClass) {
	ticker := time.NewTicker(scheduler.PollEvery)
	defer ticker.Stop()
	for {
		// Expansion is one atomic fanout transaction: a per-call deadline
		// would abort a slow-but-progressing page and restart the identical
		// work from the same offset every tick. It runs under the scheduler
		// lifetime; the independent reap loop keeps lease recovery alive
		// while an expansion call blocks.
		_, err := scheduler.Store.ExpandNextGenerationSchedule(ctx, class)
		if err != nil &&
			!errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			scheduler.report(fmt.Errorf("expand %s generation page: %w", class, err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (scheduler *Scheduler) reap(ctx context.Context, class store.GenerationResourceClass) {
	ticker := time.NewTicker(scheduler.PollEvery)
	defer ticker.Stop()
	for {
		callCtx, cancel := context.WithTimeout(ctx, scheduler.storeCallTimeout())
		_, err := scheduler.Store.ReapStaleGenerationChunks(callCtx, class, scheduler.StaleAfter)
		cancel()
		if err != nil &&
			ctx.Err() == nil {
			scheduler.report(fmt.Errorf("reap %s generation chunks: %w", class, err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (scheduler *Scheduler) work(
	ctx context.Context,
	class store.GenerationResourceClass,
	configuration Class,
	index int,
) {
	ticker := time.NewTicker(scheduler.PollEvery)
	defer ticker.Stop()
	worker := fmt.Sprintf("%s-%s-%d", scheduler.WorkerPrefix, class, index)
	for {
		callCtx, cancel := context.WithTimeout(ctx, scheduler.storeCallTimeout())
		chunk, err := scheduler.Store.ClaimGenerationChunk(callCtx, class, worker)
		cancel()
		if err == nil {
			scheduler.execute(ctx, configuration, *chunk)
			continue
		}
		if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			scheduler.report(fmt.Errorf("claim %s generation chunk: %w", class, err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (scheduler *Scheduler) execute(ctx context.Context, configuration Class, chunk store.GenerationChunk) {
	timingEnabled := scheduler != nil && (scheduler.Diagnostics || scheduler.ChunkReports != nil)
	var started time.Time
	if timingEnabled {
		started = time.Now()
	}
	scheduler.emitChunkLifecycle("started", chunk, "running")
	outcome := "handler_failed"
	defer func() {
		if timingEnabled {
			scheduler.emitChunkLifecycleDuration("settled", chunk, outcome, time.Since(started).Milliseconds())
		}
	}()
	handleCtx, cancel := context.WithCancel(ctx)
	heartbeat := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(scheduler.HeartbeatEvery)
		defer ticker.Stop()
		// The claimed row carries the store's durable heartbeat time. Keep a
		// conservative lower bound after each successful beat so a sequence of
		// client-side errors can never outlive the reaper's stale cutoff.
		lastConfirmed := time.Now()
		if chunk.HeartbeatAt != nil && chunk.HeartbeatAt.Before(lastConfirmed) {
			lastConfirmed = *chunk.HeartbeatAt
		}
		for {
			select {
			case <-handleCtx.Done():
				heartbeat <- nil
				return
			case <-ticker.C:
				beatStarted := time.Now()
				callCtx, callCancel := context.WithTimeout(
					handleCtx, scheduler.storeCallTimeout(),
				)
				err := scheduler.Store.HeartbeatGenerationChunk(callCtx, chunk)
				callCancel()
				if err == nil {
					// The durable write occurred no earlier than beatStarted.
					lastConfirmed = beatStarted
					continue
				}
				if handleCtx.Err() != nil {
					// The handler finished (or the scheduler stopped) while
					// this beat was in flight; not a heartbeat failure.
					heartbeat <- nil
					return
				}
				// A lease fence is definitive. A transient store error is
				// tolerated until the lease could actually have gone stale;
				// killing a healthy handler for one slow beat wastes the
				// whole chunk.
				if errors.Is(err, store.ErrGenerationLeaseLost) ||
					errors.Is(err, store.ErrGenerationStale) ||
					time.Since(lastConfirmed) >= scheduler.StaleAfter {
					cancel()
					heartbeat <- err
					return
				}
			}
		}
	}()
	handleErr := configuration.Handle(handleCtx, chunk, configuration.Budget)
	cancel()
	heartbeatErr := <-heartbeat
	writeCtx, writeCancel := context.WithTimeout(
		context.WithoutCancel(ctx), scheduler.storeCallTimeout(),
	)
	defer writeCancel()
	if heartbeatErr != nil {
		outcome = "heartbeat_failed"
		if errors.Is(heartbeatErr, store.ErrGenerationLeaseLost) || errors.Is(heartbeatErr, store.ErrGenerationStale) {
			outcome = "stale_fenced"
		}
		if !errors.Is(heartbeatErr, store.ErrGenerationLeaseLost) {
			scheduler.report(fmt.Errorf("heartbeat generation chunk: %w", heartbeatErr))
		}
		return
	}
	if ctx.Err() != nil {
		outcome = "released"
		if err := scheduler.Store.ReleaseGenerationChunk(writeCtx, chunk, ctx.Err().Error()); err != nil &&
			!errors.Is(err, store.ErrGenerationLeaseLost) && !errors.Is(err, store.ErrGenerationStale) {
			scheduler.report(fmt.Errorf("release generation chunk: %w", err))
		}
		return
	}
	if handleErr == nil {
		outcome = "completed"
		if err := scheduler.Store.CompleteGenerationChunk(writeCtx, chunk); err != nil &&
			!errors.Is(err, store.ErrGenerationLeaseLost) && !errors.Is(err, store.ErrGenerationStale) {
			outcome = "completion_failed"
			scheduler.report(fmt.Errorf("complete generation chunk: %w", err))
		} else if errors.Is(err, store.ErrGenerationLeaseLost) || errors.Is(err, store.ErrGenerationStale) {
			outcome = "stale_fenced"
		}
		return
	}
	if store.IsTerminal(handleErr) {
		outcome = "terminal"
		if err := scheduler.Store.FailGenerationChunk(
			writeCtx, chunk, store.DurableErrorText(handleErr),
		); err != nil &&
			!errors.Is(err, store.ErrGenerationLeaseLost) &&
			!errors.Is(err, store.ErrGenerationStale) {
			outcome = "terminal_record_failed"
			scheduler.report(fmt.Errorf("fail terminal generation chunk: %w", err))
		} else if errors.Is(err, store.ErrGenerationLeaseLost) || errors.Is(err, store.ErrGenerationStale) {
			outcome = "stale_fenced"
		}
		return
	}
	if store.IsDeferral(handleErr) {
		outcome = "deferred"
		if err := scheduler.Store.DeferGenerationChunk(
			writeCtx,
			chunk,
			store.DurableErrorText(handleErr),
			scheduler.deferralDelay(),
		); err != nil && !errors.Is(err, store.ErrGenerationLeaseLost) &&
			!errors.Is(err, store.ErrGenerationStale) {
			outcome = "deferral_failed"
			scheduler.report(fmt.Errorf("defer generation chunk: %w", err))
		} else if errors.Is(err, store.ErrGenerationLeaseLost) ||
			errors.Is(err, store.ErrGenerationStale) {
			outcome = "stale_fenced"
		}
		return
	}
	staleHandle := errors.Is(handleErr, store.ErrGenerationLeaseLost) || errors.Is(handleErr, store.ErrGenerationStale)
	if staleHandle {
		outcome = "stale_fenced"
	}
	notBefore := time.Now().UTC().Add(scheduler.Backoff(chunk.Attempt + 1))
	if _, err := scheduler.Store.RetryGenerationChunk(
		writeCtx, chunk, store.DurableErrorText(handleErr), notBefore,
	); errors.Is(err, store.ErrGenerationExhausted) {
		if !staleHandle {
			outcome = "exhausted"
		}
		if configuration.OnExhausted != nil {
			callbackCtx, callbackCancel := context.WithTimeout(
				context.WithoutCancel(ctx), scheduler.storeCallTimeout(),
			)
			defer callbackCancel()
			if callbackErr := configuration.OnExhausted(
				callbackCtx, chunk, handleErr,
			); callbackErr != nil && !errors.Is(callbackErr, store.ErrGenerationStale) {
				scheduler.report(fmt.Errorf("record exhausted generation chunk: %w", callbackErr))
			}
		}
	} else if err == nil {
		if !staleHandle {
			outcome = "retried"
		}
	} else if errors.Is(err, store.ErrGenerationLeaseLost) || errors.Is(err, store.ErrGenerationStale) {
		outcome = "stale_fenced"
	} else if err != nil &&
		!errors.Is(err, store.ErrGenerationLeaseLost) &&
		!errors.Is(err, store.ErrGenerationStale) {
		scheduler.report(fmt.Errorf("retry generation chunk: %w", err))
	}
}

func (scheduler *Scheduler) emitChunkLifecycle(event string, chunk store.GenerationChunk, outcome string) {
	scheduler.emitChunkLifecycleDuration(event, chunk, outcome, 0)
}

func (scheduler *Scheduler) emitChunkLifecycleDuration(
	event string,
	chunk store.GenerationChunk,
	outcome string,
	durationMS int64,
) {
	if scheduler == nil || (!scheduler.Diagnostics && scheduler.ChunkReports == nil) {
		return
	}
	report, err := json.Marshal(ChunkLifecycleReport{
		Schema: ChunkLifecycleSchema, Event: event, Identity: chunk.Identity,
		Stage: chunk.Stage, Generation: chunk.Generation, Attempt: chunk.Attempt, Outcome: outcome,
		DurationMS: durationMS,
	})
	if err != nil || len(report) > 4096 {
		return
	}
	sink := scheduler.ChunkReports
	if sink == nil {
		sink = func(value []byte) error {
			diagnostics.Logf("generation chunk lifecycle: %s", value)
			return nil
		}
	}
	func() {
		defer func() { _ = recover() }()
		_ = sink(report)
	}()
}

func (scheduler *Scheduler) report(err error) {
	if err == nil || scheduler.Report == nil {
		return
	}
	func() {
		defer func() { _ = recover() }()
		scheduler.Report(err)
	}()
}
