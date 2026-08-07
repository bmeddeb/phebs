// Package generationscheduler executes durable T35.1 work one bounded chunk
// at a time. It owns process-local resource admission; the store owns
// repository tokens, generation coalescing, leases, and retry successors.
package generationscheduler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

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

type Class struct {
	Concurrency int
	Budget      Budget
	Handle      Handler
}

type Scheduler struct {
	Store          store.GenerationSchedulerStore
	Classes        map[store.GenerationResourceClass]Class
	MaxConcurrency int
	MaxMemoryBytes int64
	MaxDescriptors int
	PollEvery      time.Duration
	HeartbeatEvery time.Duration
	StaleAfter     time.Duration
	Backoff        func(attempt int) time.Duration
	WorkerPrefix   string
	Report         func(error)
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
		workers.Add(1)
		go func() {
			defer workers.Done()
			scheduler.planAndReap(ctx, class)
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
		scheduler.StaleAfter <= scheduler.HeartbeatEvery {
		return nil, errors.New("generation scheduler process bounds are invalid")
	}
	classes := make([]store.GenerationResourceClass, 0, len(scheduler.Classes))
	totalConcurrency := 0
	var totalMemory int64
	totalDescriptors := 0
	for class, configuration := range scheduler.Classes {
		if class != store.GenerationResourceCPU && class != store.GenerationResourceIO &&
			class != store.GenerationResourceMemory {
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

func (scheduler *Scheduler) planAndReap(ctx context.Context, class store.GenerationResourceClass) {
	ticker := time.NewTicker(scheduler.PollEvery)
	defer ticker.Stop()
	for {
		if _, err := scheduler.Store.ExpandNextGenerationSchedule(ctx, class); err != nil &&
			!errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			scheduler.report(fmt.Errorf("expand %s generation page: %w", class, err))
		}
		if _, err := scheduler.Store.ReapStaleGenerationChunks(ctx, class, scheduler.StaleAfter); err != nil &&
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
		chunk, err := scheduler.Store.ClaimGenerationChunk(ctx, class, worker)
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
	handleCtx, cancel := context.WithCancel(ctx)
	heartbeat := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(scheduler.HeartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-handleCtx.Done():
				heartbeat <- nil
				return
			case <-ticker.C:
				if err := scheduler.Store.HeartbeatGenerationChunk(handleCtx, chunk); err != nil {
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
	writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer writeCancel()
	if heartbeatErr != nil {
		if !errors.Is(heartbeatErr, store.ErrGenerationLeaseLost) {
			scheduler.report(fmt.Errorf("heartbeat generation chunk: %w", heartbeatErr))
		}
		return
	}
	if ctx.Err() != nil {
		if err := scheduler.Store.ReleaseGenerationChunk(writeCtx, chunk, ctx.Err().Error()); err != nil &&
			!errors.Is(err, store.ErrGenerationLeaseLost) && !errors.Is(err, store.ErrGenerationStale) {
			scheduler.report(fmt.Errorf("release generation chunk: %w", err))
		}
		return
	}
	if handleErr == nil {
		if err := scheduler.Store.CompleteGenerationChunk(writeCtx, chunk); err != nil &&
			!errors.Is(err, store.ErrGenerationLeaseLost) && !errors.Is(err, store.ErrGenerationStale) {
			scheduler.report(fmt.Errorf("complete generation chunk: %w", err))
		}
		return
	}
	notBefore := time.Now().UTC().Add(scheduler.Backoff(chunk.Attempt + 1))
	if _, err := scheduler.Store.RetryGenerationChunk(
		writeCtx, chunk, store.DurableErrorText(handleErr), notBefore,
	); err != nil &&
		!errors.Is(err, store.ErrGenerationExhausted) &&
		!errors.Is(err, store.ErrGenerationLeaseLost) &&
		!errors.Is(err, store.ErrGenerationStale) {
		scheduler.report(fmt.Errorf("retry generation chunk: %w", err))
	}
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
