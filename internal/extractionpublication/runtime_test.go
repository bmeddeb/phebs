package extractionpublication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestPartitionTimingReportIsBoundedAndSourceFree(t *testing.T) {
	var raw []byte
	runtime := &Runtime{TimingReports: func(value []byte) error {
		raw = append([]byte(nil), value...)
		return nil
	}}
	report := PartitionTimingReport{
		Schema:     PartitionTimingSchema,
		Identity:   "sha256:" + strings.Repeat("a", 64),
		Generation: "sha256:" + strings.Repeat("b", 64),
		Domain:     "kafka-producer",
		Attempt:    2, Outcome: "completed",
		SourceAcquireMS: 10, ExecutorMS: 20, ResultMS: 5, AssemblyMS: 3, TotalMS: 40,
	}
	runtime.emitPartitionTiming(report)
	var decoded PartitionTimingReport
	if len(raw) == 0 || len(raw) > 4096 || json.Unmarshal(raw, &decoded) != nil || decoded != report {
		t.Fatalf("timing report = %s", raw)
	}
	if strings.Contains(string(raw), "repository") || strings.Contains(string(raw), "path") ||
		strings.Contains(string(raw), "error") {
		t.Fatalf("timing report exposed source-bearing fields: %s", raw)
	}
	invalid := report
	invalid.TotalMS = 1
	if ValidatePartitionTimingReport(invalid) == nil {
		t.Fatal("phase durations larger than total were accepted")
	}
}

func TestPartitionTimingReportV1CompatibilityAndV2TerminalReceipt(t *testing.T) {
	base := PartitionTimingReport{
		Schema:     PartitionTimingSchemaV1,
		Identity:   "sha256:" + strings.Repeat("a", 64),
		Generation: "sha256:" + strings.Repeat("b", 64),
		Outcome:    "terminal_refusal", TotalMS: 1,
	}
	if err := ValidatePartitionTimingReport(base); err != nil {
		t.Fatalf("historical v1 report = %v", err)
	}
	base.Schema = PartitionTimingSchemaV2
	if ValidatePartitionTimingReport(base) == nil {
		t.Fatal("v2 terminal report without a refusal was accepted")
	}
	base.RefusalStage = pipelinerefusal.StageEvidenceStaging
	base.RefusalGenerationKind = pipelinerefusal.GenerationExtractionDomain
	base.RefusalClassification = pipelinerefusal.ClassificationLimit
	base.RefusalDimension = pipelinerefusal.DimensionFacts
	base.RefusalObserved, base.RefusalLimit = 2, 1
	if err := ValidatePartitionTimingReport(base); err != nil {
		t.Fatalf("v2 terminal report = %v", err)
	}
	base.Outcome = "failed"
	if ValidatePartitionTimingReport(base) == nil {
		t.Fatal("non-terminal report with refusal fields was accepted")
	}

	v3 := PartitionTimingReport{
		Schema: PartitionTimingSchemaV3, Identity: base.Identity, Generation: base.Generation,
		Domain: "grpc-caller", Outcome: "failed", FailureClass: PartitionFailureDeadline,
		TotalMS: 300_000, ExecutorMS: 300_000,
	}
	if err := ValidatePartitionTimingReport(v3); err != nil {
		t.Fatalf("v3 deadline report = %v", err)
	}
	v3.FailureClass = "private-error"
	if ValidatePartitionTimingReport(v3) == nil {
		t.Fatal("v3 arbitrary failure class was accepted")
	}
	v3.FailureClass = PartitionFailureOther
	v3.Domain = "private/path"
	if ValidatePartitionTimingReport(v3) == nil {
		t.Fatal("v3 source-shaped domain was accepted")
	}
}

func TestPartitionTimingFailureClassificationIsClosed(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, PartitionFailureDeadline},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), PartitionFailureDeadline},
		{context.Canceled, PartitionFailureCanceled},
		{errors.New("private detail"), PartitionFailureOther},
	} {
		if got := classifyPartitionTimingFailure(test.err); got != test.want {
			t.Fatalf("failure class = %q, want %q", got, test.want)
		}
	}
}

type testLease struct{ source *testSource }

func (lease testLease) Release() {
	lease.source.mu.Lock()
	lease.source.releases++
	lease.source.mu.Unlock()
}
func (testLease) Corpus() sdk.Corpus                      { return nil }
func (testLease) CandidateRecords() int                   { return 0 }
func (testLease) TypedSourceRecords() int                 { return 0 }
func (testLease) TypedInput() *candidate.SparseTypedInput { return nil }
func (testLease) MemberBytes() int64                      { return 20 }
func (testLease) Members() int64                          { return 1 }

type testSource struct {
	mu       sync.Mutex
	acquires int
	releases int
}

type deadlineSource struct {
	testSource
	hadDeadline bool
}

func (source *deadlineSource) AcquirePartition(
	ctx context.Context,
	_ candidate.DomainResultPlan,
	_ int,
) (PartitionLease, error) {
	_, source.hadDeadline = ctx.Deadline()
	source.mu.Lock()
	source.acquires++
	source.mu.Unlock()
	return testLease{source: &source.testSource}, nil
}

type deadlineExecutor struct {
	deadline time.Time
	present  bool
}

type terminalExecutor struct{}

func (terminalExecutor) ExecutePartition(
	context.Context,
	candidate.DomainResultPlan,
	int,
	PartitionLease,
	string,
) (candidate.PartitionResultSpec, error) {
	return candidate.PartitionResultSpec{
		Disposition: candidate.PartitionResultTerminalRefusal,
		Reason:      "deterministic_refusal",
	}, store.WithTerminal(errors.New("private deterministic detail"))
}

func (executor *deadlineExecutor) ExecutePartition(
	ctx context.Context,
	plan candidate.DomainResultPlan,
	ordinal int,
	_ PartitionLease,
	_ string,
) (candidate.PartitionResultSpec, error) {
	executor.deadline, executor.present = ctx.Deadline()
	reservation := plan.Expected[ordinal].Reservation
	return candidate.PartitionResultSpec{
		Disposition: candidate.PartitionResultSuccess,
		Totals: candidate.DomainResultTotals{
			Facts: min(1, reservation.Facts), Rows: min(2, reservation.Rows),
			References: min(1, reservation.References),
		},
		ContentDigest: "sha256:" + strings.Repeat("f", 64),
	}, nil
}

func (source *testSource) AcquirePartition(
	context.Context, candidate.DomainResultPlan, int,
) (PartitionLease, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.acquires++
	return testLease{source: source}, nil
}

func (source *testSource) counts() (int, int) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.acquires, source.releases
}

type testExecutor struct {
	mu       sync.Mutex
	calls    int
	failures int
	wait     bool
}

func (executor *testExecutor) ExecutePartition(
	ctx context.Context,
	plan candidate.DomainResultPlan,
	ordinal int,
	_ PartitionLease,
	_ string,
) (candidate.PartitionResultSpec, error) {
	executor.mu.Lock()
	executor.calls++
	if executor.failures > 0 {
		executor.failures--
		executor.mu.Unlock()
		return candidate.PartitionResultSpec{}, errors.New("transient")
	}
	wait := executor.wait
	executor.mu.Unlock()
	if wait {
		<-ctx.Done()
		return candidate.PartitionResultSpec{}, ctx.Err()
	}
	reservation := plan.Expected[ordinal].Reservation
	return candidate.PartitionResultSpec{
		Disposition: candidate.PartitionResultSuccess,
		Totals: candidate.DomainResultTotals{
			Facts: min(1, reservation.Facts), Rows: min(2, reservation.Rows),
			References:     min(1, reservation.References),
			CanonicalBytes: min(16, reservation.CanonicalBytes),
			EncodedBytes:   min(20, reservation.EncodedBytes),
			MemberBytes:    min(20, reservation.MemberBytes),
			Members:        min(1, reservation.Members),
		},
		ContentDigest: "sha256:" + strings.Repeat(fmt.Sprintf("%x", ordinal+1), 64),
	}, nil
}

func (executor *testExecutor) callCount() int {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.calls
}

type testPublisher struct {
	mu        sync.Mutex
	calls     int
	failures  int
	published map[string]candidate.DomainResultRoot
	before    func(candidate.DomainResultRoot)
}

func (publisher *testPublisher) PublishDomain(
	_ context.Context,
	plan candidate.DomainResultPlan,
	root candidate.DomainResultRoot,
	_ string,
) error {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	publisher.calls++
	if publisher.before != nil {
		publisher.before(root)
	}
	if publisher.failures > 0 {
		publisher.failures--
		return errors.New("publish unavailable")
	}
	if publisher.published == nil {
		publisher.published = map[string]candidate.DomainResultRoot{}
	}
	publisher.published[plan.Domain] = root
	return nil
}

type testFence struct {
	mu      sync.Mutex
	calls   int
	stale   bool
	current string
	held    bool
	before  func()
}

func (fence *testFence) FenceDomain(_ context.Context, request FenceRequest) (func(), error) {
	fence.mu.Lock()
	defer fence.mu.Unlock()
	fence.calls++
	if fence.before != nil {
		fence.before()
	}
	if fence.stale || fence.current != "" && fence.current != request.Plan.SourceGenerationDigest {
		return nil, ErrStale
	}
	fence.held = true
	return func() {
		fence.mu.Lock()
		fence.held = false
		fence.mu.Unlock()
	}, nil
}

func (fence *testFence) active() bool {
	fence.mu.Lock()
	defer fence.mu.Unlock()
	return fence.held
}

type testScheduleStore struct {
	mu      sync.Mutex
	current map[string]store.GenerationSchedule
}

func scheduleKey(repository, stage string) string { return repository + "\x00" + stage }

func (state *testScheduleStore) EnqueueGenerationSchedule(
	_ context.Context, spec store.GenerationScheduleSpec,
) (*store.GenerationSchedule, error) {
	digest, err := store.GenerationScheduleDigest(spec)
	if err != nil {
		return nil, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.current == nil {
		state.current = map[string]store.GenerationSchedule{}
	}
	key := scheduleKey(spec.Repository, spec.Stage)
	if current, ok := state.current[key]; ok && current.Generation == spec.Generation {
		if current.Status != store.GenerationScheduleActive {
			return nil, store.ErrGenerationStale
		}
		copy := current
		return &copy, nil
	}
	now := time.Now().UTC()
	schedule := store.GenerationSchedule{
		Schema: store.GenerationScheduleSchema, Digest: digest,
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation,
		ResourceClass: spec.ResourceClass, TotalItems: spec.TotalItems,
		ChunkItems:  spec.ChunkItems,
		TotalChunks: int((spec.TotalItems + int64(spec.ChunkItems) - 1) / int64(spec.ChunkItems)),
		MaxAttempts: spec.MaxAttempts, RepositoryTokens: spec.RepositoryTokens,
		Status: store.GenerationScheduleActive, CreatedAt: now, UpdatedAt: now,
	}
	state.current[key] = schedule
	copy := schedule
	return &copy, nil
}

func (state *testScheduleStore) GetGenerationSchedule(
	_ context.Context, repository, stage string,
) (*store.GenerationSchedule, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	schedule, ok := state.current[scheduleKey(repository, stage)]
	if !ok {
		return nil, store.ErrNotFound
	}
	copy := schedule
	return &copy, nil
}

func (state *testScheduleStore) RetireCurrentGenerationSchedule(
	_ context.Context, expected store.GenerationSchedule,
) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	key := scheduleKey(expected.Repository, expected.Stage)
	current, ok := state.current[key]
	if !ok || current.Digest != expected.Digest || current.Generation != expected.Generation {
		return store.ErrGenerationStale
	}
	delete(state.current, key)
	return nil
}

func (state *testScheduleStore) HeartbeatGenerationChunk(_ context.Context, chunk store.GenerationChunk) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	current, ok := state.current[scheduleKey(chunk.Repository, chunk.Stage)]
	if !ok || current.Generation != chunk.Generation || current.Digest != chunk.ScheduleDigest {
		return store.ErrGenerationStale
	}
	if chunk.LeaseToken == "lost" {
		return store.ErrGenerationLeaseLost
	}
	return nil
}

func (state *testScheduleStore) settle(repository string, failed int) {
	state.mu.Lock()
	defer state.mu.Unlock()
	key := scheduleKey(repository, ScheduleStage)
	schedule := state.current[key]
	schedule.Status = store.GenerationScheduleSettled
	schedule.NextOffset = schedule.TotalItems
	schedule.Materialized = schedule.TotalChunks
	schedule.Succeeded = schedule.TotalChunks - failed
	schedule.Failed = failed
	state.current[key] = schedule
}

func (*testScheduleStore) ExpandGenerationSchedule(context.Context, string, string, string) (*store.GenerationSchedule, error) {
	return nil, store.ErrNotFound
}
func (*testScheduleStore) ExpandNextGenerationSchedule(context.Context, store.GenerationResourceClass) (*store.GenerationSchedule, error) {
	return nil, store.ErrNotFound
}
func (*testScheduleStore) ClaimGenerationChunk(context.Context, store.GenerationResourceClass, string) (*store.GenerationChunk, error) {
	return nil, store.ErrNotFound
}
func (*testScheduleStore) CompleteGenerationChunk(context.Context, store.GenerationChunk) error {
	return nil
}
func (*testScheduleStore) FailGenerationChunk(context.Context, store.GenerationChunk, string) error {
	return nil
}
func (*testScheduleStore) RetryGenerationChunk(context.Context, store.GenerationChunk, string, time.Time) (*store.GenerationChunk, error) {
	return nil, store.ErrGenerationExhausted
}
func (*testScheduleStore) DeferGenerationChunk(
	context.Context, store.GenerationChunk, string, time.Duration,
) error {
	return nil
}
func (*testScheduleStore) ReleaseGenerationChunk(context.Context, store.GenerationChunk, string) error {
	return nil
}
func (*testScheduleStore) ReapStaleGenerationChunks(context.Context, store.GenerationResourceClass, time.Duration) (int, error) {
	return 0, nil
}

func newRuntimeFixture(t *testing.T, plan candidate.DomainResultPlan) (*Runtime, *testScheduleStore, *testSource, *testExecutor, *testPublisher, *testFence, DomainPlan) {
	t.Helper()
	state := &testScheduleStore{}
	source := &testSource{}
	executor := &testExecutor{}
	publisher := &testPublisher{}
	fence := &testFence{current: plan.SourceGenerationDigest}
	runtime := &Runtime{
		Root: t.TempDir(), Store: state, Source: source, Executor: executor,
		Publisher: publisher, Fence: fence,
	}
	domain := DomainPlan{Schema: DomainSchema, RunID: "run-a", Plan: plan}
	return runtime, state, source, executor, publisher, fence, domain
}

func TestRuntimeProgressReadsOnlyBoundedScheduleAndCurrentPointers(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("1", 64), true)
	runtime, state, _, _, _, _, domain := newRuntimeFixture(t, plan)
	if _, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain}); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	key := scheduleKey(plan.Repository, ScheduleStage)
	schedule := state.current[key]
	schedule.NextOffset = schedule.TotalItems
	schedule.Materialized = schedule.TotalChunks
	schedule.Pending = schedule.TotalChunks - 1
	schedule.Running = 1
	state.current[key] = schedule
	state.mu.Unlock()

	progress, err := runtime.Progress(t.Context(), plan.Repository)
	if err != nil || progress.State != "active" || progress.Total != len(plan.Expected) ||
		progress.Materialized != len(plan.Expected) || progress.Pending != len(plan.Expected)-1 ||
		progress.Running != 1 || progress.Domains != 1 || progress.CurrentDomains != 0 {
		t.Fatalf("progress = %+v, error=%v", progress, err)
	}
}

func TestValidateProgressAllowsMaterializedRetryAttempts(t *testing.T) {
	tests := []struct {
		name     string
		progress Progress
		wantErr  bool
	}{
		{
			name: "active retry",
			progress: Progress{
				State: "active", Total: 2, Materialized: 3,
				Pending: 1, Succeeded: 1, Domains: 1,
			},
		},
		{
			name: "current retry",
			progress: Progress{
				State: "current", Total: 2, Materialized: 3,
				Succeeded: 2, Domains: 1, CurrentDomains: 1,
			},
		},
		{
			name: "current below total",
			progress: Progress{
				State: "current", Total: 2, Materialized: 1,
				Succeeded: 2, Domains: 1, CurrentDomains: 1,
			},
			wantErr: true,
		},
		{
			name: "above frozen allocation",
			progress: Progress{
				State: "active", Total: 2, Materialized: 2*ScheduleMaxAttempts + 1,
				Pending: 1, Succeeded: 1, Domains: 1,
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateProgress(test.progress)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateProgress(%+v) error = %v, want error %t", test.progress, err, test.wantErr)
			}
		})
	}
}

func currentChunk(t *testing.T, state *testScheduleStore, repository string, offset int) store.GenerationChunk {
	t.Helper()
	schedule, err := state.GetGenerationSchedule(t.Context(), repository, ScheduleStage)
	if err != nil {
		t.Fatal(err)
	}
	return store.GenerationChunk{
		ID: "chunk", Identity: "chunk", ScheduleDigest: schedule.Digest,
		Repository: repository, Stage: ScheduleStage, Generation: schedule.Generation,
		ResourceClass: store.GenerationResourceExtraction, Offset: int64(offset), Length: 1,
		Status: store.GenerationChunkRunning, ClaimedBy: "worker", LeaseToken: "lease",
	}
}

func TestRuntimePointerLastRestartAndSettledRetryReuse(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("1", 64), true)
	runtime, state, source, executor, publisher, fence, domain := newRuntimeFixture(t, plan)
	runtime.OnSettled = func(context.Context, string) error {
		if fence.active() {
			return errors.New("downstream callback retained exclusive publication fence")
		}
		return nil
	}
	publisher.failures = 1
	publisher.before = func(candidate.DomainResultRoot) {
		if publisher.calls > 2 {
			return
		}
		if _, err := os.Lstat(runtime.currentPath(plan.Repository, plan.Domain)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pointer existed before authoritative publish: %v", err)
		}
	}
	generation, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain})
	if err != nil {
		t.Fatal(err)
	}
	existing, present, err := runtime.ExistingPlans(
		t.Context(), plan.Repository, []candidate.DomainResultPlan{plan},
	)
	if err != nil || !present || len(existing) != 1 || existing[0].RunID != domain.RunID {
		t.Fatalf("existing run bindings = %+v, %t, %v", existing, present, err)
	}
	chunk := currentChunk(t, state, plan.Repository, 0)
	if err := runtime.Handle(t.Context(), chunk); err == nil || !strings.Contains(err.Error(), "publish unavailable") {
		t.Fatalf("first publish error = %v", err)
	}
	if acquired, released := source.counts(); acquired != 1 || released != 1 {
		t.Fatalf("source counts after failure = %d/%d", acquired, released)
	}
	if err := runtime.Handle(t.Context(), chunk); err != nil {
		t.Fatal(err)
	}
	if acquired, _ := source.counts(); acquired != 1 || executor.callCount() != 1 {
		t.Fatalf("settled retry repeated work: source=%d executor=%d", acquired, executor.callCount())
	}
	root, err := runtime.Current(t.Context(), plan.Repository, plan.Domain)
	if err != nil || root.PlanDigest != plan.Digest || root.Disposition != candidate.PartitionResultSuccess {
		t.Fatalf("current root = %+v, %v", root, err)
	}

	restarted := &Runtime{Root: runtime.Root, Store: state, Source: source, Executor: executor, Publisher: publisher, Fence: runtime.Fence}
	domain.RunID = "new-run-must-not-replace-historical"
	again, err := restarted.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain})
	if err != nil || again != generation {
		t.Fatalf("restart reconcile = %q, %v, want %q", again, err, generation)
	}
	if acquired, _ := source.counts(); acquired != 1 {
		t.Fatalf("restart reopened source %d times", acquired)
	}
}

func TestRuntimeReleasesSourceLeaseBeforePublicationFence(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("f", 64), true)
	runtime, state, source, _, _, fence, domain := newRuntimeFixture(t, plan)
	fence.before = func() {
		acquired, released := source.counts()
		if acquired != 1 || released != 1 {
			t.Fatalf("source lease at publication fence = %d/%d, want released", acquired, released)
		}
	}
	if _, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, state, plan.Repository, 0)); err != nil {
		t.Fatal(err)
	}
	if fence.calls != 1 {
		t.Fatalf("publication fence calls = %d, want 1", fence.calls)
	}
}

func TestRuntimeZeroWorkDomainPublishesWithoutSchedulerLease(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("0", 64), false)
	runtime, _, source, executor, _, fence, domain := newRuntimeFixture(t, plan)
	runtime.OnSettled = func(context.Context, string) error {
		if fence.active() {
			return errors.New("downstream callback retained exclusive publication fence")
		}
		return nil
	}

	generation, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain})
	if err != nil {
		t.Fatal(err)
	}
	root, err := runtime.Current(t.Context(), plan.Repository, plan.Domain)
	if err != nil || root.PlanDigest != plan.Digest ||
		root.Disposition != candidate.PartitionResultEmpty {
		t.Fatalf("zero-work root = %+v, %v", root, err)
	}
	if generation == "" || fence.calls != 1 {
		t.Fatalf("zero-work generation/fence = %q/%d", generation, fence.calls)
	}
	if acquired, released := source.counts(); acquired != 0 || released != 0 ||
		executor.callCount() != 0 {
		t.Fatalf("zero-work content work = source %d/%d executor %d", acquired, released, executor.callCount())
	}
}

func TestRuntimeCancellationAndLostLeaseInstallNoResult(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("2", 64), true)
	runtime, state, source, executor, _, _, domain := newRuntimeFixture(t, plan)
	var timings []PartitionTimingReport
	runtime.TimingReports = func(raw []byte) error {
		var timing PartitionTimingReport
		if err := json.Unmarshal(raw, &timing); err != nil {
			return err
		}
		timings = append(timings, timing)
		return nil
	}
	_, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain})
	if err != nil {
		t.Fatal(err)
	}
	executor.wait = true
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	canceledChunk := currentChunk(t, state, plan.Repository, 0)
	canceledChunk.Identity = "sha256:" + strings.Repeat("c", 64)
	if err := runtime.Handle(ctx, canceledChunk); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled handle error = %v", err)
	}
	status, err := runtime.Status(t.Context(), plan.Repository, digestForPlanSet(plan.Repository, plan.Digest))
	if err != nil || status.Domains[0].Settled != 0 {
		t.Fatalf("canceled status = %+v, %v", status, err)
	}
	executor.wait = false
	chunk := currentChunk(t, state, plan.Repository, 0)
	chunk.Identity = "sha256:" + strings.Repeat("d", 64)
	chunk.LeaseToken = "lost"
	if err := runtime.Handle(t.Context(), chunk); !errors.Is(err, store.ErrGenerationLeaseLost) {
		t.Fatalf("lost lease error = %v", err)
	}
	if acquired, released := source.counts(); acquired != 1 || released != 1 {
		t.Fatalf("lease cleanup = %d/%d", acquired, released)
	}
	if len(timings) != 2 || timings[0].Domain != plan.Domain ||
		timings[0].FailureClass != PartitionFailureCanceled ||
		timings[1].FailureClass != PartitionFailureOther {
		t.Fatalf("closed failure timings = %+v", timings)
	}
}

func TestRuntimeInstallsTerminalExecutorResultBeforeTerminalSettlement(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("8", 64), true)
	runtime, state, _, _, publisher, _, domain := newRuntimeFixture(t, plan)
	var timing PartitionTimingReport
	runtime.TimingReports = func(raw []byte) error { return json.Unmarshal(raw, &timing) }
	runtime.Executor = terminalExecutor{}
	generation, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain})
	if err != nil {
		t.Fatal(err)
	}
	chunk := currentChunk(t, state, plan.Repository, 0)
	chunk.Identity = "sha256:" + strings.Repeat("c", 64)
	err = runtime.Handle(t.Context(), chunk)
	if !store.IsTerminal(err) {
		t.Fatalf("terminal handle = %v", err)
	}
	status, statusErr := runtime.Status(t.Context(), plan.Repository, generation)
	if statusErr != nil || len(status.Domains) != 1 || status.Domains[0].Settled != 1 ||
		status.Domains[0].Failed != 1 ||
		status.Domains[0].Disposition != candidate.PartitionResultTerminalRefusal {
		t.Fatalf("terminal status = %+v, %v", status, statusErr)
	}
	if publisher.calls != 0 {
		t.Fatalf("terminal root publication calls = %d", publisher.calls)
	}
	for range 50 {
		if timing.Outcome != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if timing.Outcome != "terminal_refusal" || timing.Schema != PartitionTimingSchemaV3 ||
		timing.Domain != plan.Domain ||
		timing.RefusalStage != pipelinerefusal.StageUnknown ||
		timing.RefusalGenerationKind != pipelinerefusal.GenerationUnknown ||
		timing.RefusalClassification != pipelinerefusal.ClassificationUnknown ||
		timing.RefusalDimension != pipelinerefusal.DimensionUnknown {
		t.Fatalf("terminal timing = %+v", timing)
	}
}

func TestSchedulePlanningAuthorityUsesProductionControlValidation(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("7", 64), true)
	runtime, _, _, _, _, _, domain := newRuntimeFixture(t, plan)
	generation, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain})
	if err != nil {
		t.Fatal(err)
	}
	authority, err := runtime.SchedulePlanningAuthority(plan.Repository, generation)
	if err != nil || authority.SourceGenerationDigest != plan.SourceGenerationDigest ||
		authority.ObservationGenerationDigest != plan.ObservationGenerationDigest ||
		authority.CandidateGenerationDigest != plan.CandidateGenerationDigest {
		t.Fatalf("planning authority = %+v, %v", authority, err)
	}
	invalid := scheduleBinding{
		Schema: BindingSchema, Repository: plan.Repository,
		ScheduleGeneration: generation, TargetGeneration: generation,
		PriorSchedule: "sha256:" + strings.Repeat("9", 64),
	}
	if err := writeAtomicCanonical(runtime.bindingPath(plan.Repository, generation), invalid); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.SchedulePlanningAuthority(plan.Repository, generation); err == nil {
		t.Fatal("production-invalid schedule binding passed diagnostic authority validation")
	}
}

func TestRuntimeStartsPartitionDeadlineAfterSourceLeaseAcquisition(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("a", 64), true)
	schedules := &testScheduleStore{}
	source := &deadlineSource{}
	executor := &deadlineExecutor{}
	publisher := &testPublisher{}
	runtime := &Runtime{
		Root: t.TempDir(), Store: schedules, Source: source, Executor: executor,
		Publisher: publisher, Fence: &testFence{current: plan.SourceGenerationDigest},
	}
	if _, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{{
		Schema: DomainSchema, RunID: "deadline-run", Plan: plan,
	}}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := runtime.Handle(t.Context(), currentChunk(t, schedules, plan.Repository, 0)); err != nil {
		t.Fatal(err)
	}
	if source.hadDeadline {
		t.Fatal("partition deadline began before repository/source lease acquisition")
	}
	want := time.Duration(plan.Expected[0].Quotas.DeadlineMilliseconds) * time.Millisecond
	remaining := executor.deadline.Sub(started)
	if !executor.present || remaining < want-time.Second || remaining > want+time.Second {
		t.Fatalf("executor deadline remaining = %v, want %v", remaining, want)
	}
}

func TestRuntimeExhaustionPreservesPriorPointerAndReportsFailure(t *testing.T) {
	planA := buildTestPlan(t, "sha256:"+strings.Repeat("3", 64), true)
	runtime, state, _, _, _, fence, domainA := newRuntimeFixture(t, planA)
	generationA, err := runtime.Reconcile(t.Context(), planA.Repository, []DomainPlan{domainA})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, state, planA.Repository, 0)); err != nil {
		t.Fatal(err)
	}
	rootA, err := runtime.Current(t.Context(), planA.Repository, planA.Domain)
	if err != nil {
		t.Fatal(err)
	}

	planB := buildTestPlan(t, "sha256:"+strings.Repeat("4", 64), true)
	domainB := DomainPlan{Schema: DomainSchema, RunID: "run-b", Plan: planB}
	fence.current = planB.SourceGenerationDigest
	generationB, err := runtime.Reconcile(t.Context(), planB.Repository, []DomainPlan{domainB})
	if err != nil || generationB == generationA {
		t.Fatalf("B reconcile = %q, %v", generationB, err)
	}
	chunkB := currentChunk(t, state, planB.Repository, 0)
	state.settle(planB.Repository, 1)
	if err := runtime.OnExhausted(t.Context(), chunkB, errors.New("transient")); err != nil {
		t.Fatal(err)
	}
	current, err := runtime.Current(t.Context(), planA.Repository, planA.Domain)
	if err != nil || current.Digest != rootA.Digest {
		t.Fatalf("prior pointer moved: %+v, %v", current, err)
	}
	status, err := runtime.Status(t.Context(), planB.Repository, generationB)
	if err != nil || status.Domains[0].RetryExhausted != 1 || status.Domains[0].Current {
		t.Fatalf("exhausted status = %+v, %v", status, err)
	}
	again, err := runtime.Reconcile(t.Context(), planB.Repository, []DomainPlan{domainB})
	if err != nil || again != generationB {
		t.Fatalf("settled failure reconcile = %q, %v", again, err)
	}
}

func TestRuntimeSupersededLeaseStopsBeforeSourceAcquisition(t *testing.T) {
	planA := buildTestPlan(t, "sha256:"+strings.Repeat("d", 64), true)
	runtime, state, source, _, _, fence, domainA := newRuntimeFixture(t, planA)
	if _, err := runtime.Reconcile(t.Context(), planA.Repository, []DomainPlan{domainA}); err != nil {
		t.Fatal(err)
	}
	staleChunk := currentChunk(t, state, planA.Repository, 0)
	planB := buildTestPlan(t, "sha256:"+strings.Repeat("e", 64), true)
	fence.current = planB.SourceGenerationDigest
	if _, err := runtime.Reconcile(t.Context(), planB.Repository, []DomainPlan{{Schema: DomainSchema, RunID: "run-b", Plan: planB}}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Handle(t.Context(), staleChunk); !errors.Is(err, store.ErrGenerationStale) {
		t.Fatalf("superseded handle error = %v", err)
	}
	if acquired, _ := source.counts(); acquired != 0 {
		t.Fatalf("superseded chunk acquired %d source leases", acquired)
	}
}

func TestAuthorityFenceErrorDefersStaleAuthorityButNotCancellationOrOperationalFailure(t *testing.T) {
	tests := []struct {
		name         string
		ctx          func(*testing.T) context.Context
		cause        error
		wantDeferred bool
	}{
		{
			name: "stale authority", ctx: func(t *testing.T) context.Context { return t.Context() },
			cause: ErrStale, wantDeferred: true,
		},
		{
			name: "store stale", ctx: func(t *testing.T) context.Context { return t.Context() },
			cause: store.ErrGenerationStale, wantDeferred: true,
		},
		{
			name: "typed contention", ctx: func(t *testing.T) context.Context { return t.Context() },
			cause: store.WithDeferral(errors.New("busy")), wantDeferred: true,
		},
		{
			name: "operational", ctx: func(t *testing.T) context.Context { return t.Context() },
			cause: errors.New("read failed"),
		},
		{
			name: "caller cancellation",
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			cause: context.Canceled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := classifyAuthorityFenceError(test.ctx(t), test.cause)
			if store.IsDeferral(err) != test.wantDeferred || !errors.Is(err, ErrStale) {
				t.Fatalf("classified error = %v, deferred=%t", err, store.IsDeferral(err))
			}
		})
	}
}

func TestRuntimeStalePublicationFenceDefersWithoutRepeatingContent(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("7", 64), true)
	runtime, state, source, executor, _, fence, domain := newRuntimeFixture(t, plan)
	if _, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain}); err != nil {
		t.Fatal(err)
	}
	fence.stale = true
	chunk := currentChunk(t, state, plan.Repository, 0)
	if err := runtime.Handle(t.Context(), chunk); !store.IsDeferral(err) || !errors.Is(err, ErrStale) {
		t.Fatalf("stale publication error = %v", err)
	}
	fence.stale = false
	if err := runtime.Handle(t.Context(), chunk); err != nil {
		t.Fatal(err)
	}
	if acquired, released := source.counts(); acquired != 1 || released != 1 || executor.callCount() != 1 {
		t.Fatalf("stale publication repeated content = source %d/%d executor %d",
			acquired, released, executor.callCount())
	}
}

func TestRuntimeSmallDeltaAndAToBToAReactivation(t *testing.T) {
	planA := buildTestPlan(t, "sha256:"+strings.Repeat("5", 64), true)
	runtime, state, source, executor, _, fence, domainA := newRuntimeFixture(t, planA)
	generationA, err := runtime.Reconcile(t.Context(), planA.Repository, []DomainPlan{domainA})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, state, planA.Repository, 0)); err != nil {
		t.Fatal(err)
	}
	planB := buildTestPlan(t, "sha256:"+strings.Repeat("6", 64), true)
	fence.current = planB.SourceGenerationDigest
	generationB, err := runtime.Reconcile(t.Context(), planB.Repository, []DomainPlan{{Schema: DomainSchema, RunID: "run-b", Plan: planB}})
	if err != nil || generationB == generationA {
		t.Fatalf("B reconcile = %q, %v", generationB, err)
	}
	bSchedule, err := state.GetGenerationSchedule(t.Context(), planB.Repository, ScheduleStage)
	if err != nil || bSchedule.Generation == generationB {
		t.Fatalf("B transition reused content authority as schedule identity: %+v, %v", bSchedule, err)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, state, planB.Repository, 0)); err != nil {
		t.Fatal(err)
	}
	if acquired, _ := source.counts(); acquired != 2 || executor.callCount() != 2 {
		t.Fatalf("A/B work = source %d executor %d", acquired, executor.callCount())
	}
	fence.current = planA.SourceGenerationDigest
	reactivated, err := runtime.Reconcile(t.Context(), planA.Repository, []DomainPlan{{Schema: DomainSchema, RunID: "ignored-new-run", Plan: planA}})
	if err != nil || reactivated != generationA {
		t.Fatalf("A reactivation = %q, %v", reactivated, err)
	}
	if acquired, _ := source.counts(); acquired != 2 || executor.callCount() != 2 {
		t.Fatalf("A reactivation repeated content: source %d executor %d", acquired, executor.callCount())
	}
	current, err := runtime.Current(t.Context(), planA.Repository, planA.Domain)
	if err != nil || current.PlanDigest != planA.Digest {
		t.Fatalf("reactivated current = %+v, %v", current, err)
	}
	schedule, err := state.GetGenerationSchedule(t.Context(), planA.Repository, ScheduleStage)
	if err != nil || schedule.Generation == generationA {
		t.Fatalf("reactivated schedule = %+v, %v", schedule, err)
	}
	binding, err := runtime.readBinding(planA.Repository, schedule.Generation)
	if err != nil || binding.TargetGeneration != generationA {
		t.Fatalf("reactivated schedule target = %+v, %v", binding, err)
	}
}

func TestRuntimeReuseAuthorityRepairsSettledSuccessorSchedule(t *testing.T) {
	planA := buildTestPlan(t, "sha256:"+strings.Repeat("5", 64), true)
	runtime, state, source, executor, _, fence, domainA := newRuntimeFixture(t, planA)
	generationA, err := runtime.Reconcile(t.Context(), planA.Repository, []DomainPlan{domainA})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, state, planA.Repository, 0)); err != nil {
		t.Fatal(err)
	}
	state.settle(planA.Repository, 0)
	planB := buildTestPlan(t, "sha256:"+strings.Repeat("6", 64), true)
	fence.current = planB.SourceGenerationDigest
	generationB, err := runtime.Reconcile(t.Context(), planB.Repository, []DomainPlan{{
		Schema: DomainSchema, RunID: "run-b", Plan: planB,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, state, planB.Repository, 0)); err != nil {
		t.Fatal(err)
	}
	state.settle(planB.Repository, 0)
	fence.current = planA.SourceGenerationDigest
	generation, err := runtime.openGeneration(
		runtime.generationDirectory(planA.Repository, generationA), planA.Repository, generationA,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.reactivateComplete(
		t.Context(), generation, runtime.generationDirectory(planA.Repository, generationA),
	); err != nil {
		t.Fatal(err)
	}
	authority, err := authorityForPlans(planA.Repository, []DomainPlan{domainA})
	if err != nil {
		t.Fatal(err)
	}
	target, reused, err := runtime.ReuseAuthority(t.Context(), authority)
	if err != nil || !reused || target != generationA {
		t.Fatalf("reuse settled B to A = %q/%t, %v", target, reused, err)
	}
	schedule, err := state.GetGenerationSchedule(t.Context(), planA.Repository, ScheduleStage)
	if err != nil || schedule.Generation == generationA || schedule.Generation == generationB {
		t.Fatalf("settled B to A transition schedule = %+v, %v", schedule, err)
	}
	binding, err := runtime.readBinding(planA.Repository, schedule.Generation)
	if err != nil || binding.TargetGeneration != generationA || binding.PriorSchedule == "" {
		t.Fatalf("settled B to A transition binding = %+v, %v", binding, err)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, state, planA.Repository, 0)); err != nil {
		t.Fatal(err)
	}
	state.settle(planA.Repository, 0)
	progress, err := runtime.Progress(t.Context(), planA.Repository)
	if err != nil || progress.State != "current" || progress.CurrentDomains != progress.Domains {
		t.Fatalf("reactivated A progress = %+v, %v", progress, err)
	}
	if acquired, _ := source.counts(); acquired != 2 || executor.callCount() != 2 {
		t.Fatalf("settled A/B/A repeated content: source %d executor %d", acquired, executor.callCount())
	}
}

func TestRuntimeZeroWorkReuseRetiresMismatchedSchedule(t *testing.T) {
	for _, settled := range []bool{false, true} {
		name := "active"
		if settled {
			name = "settled"
		}
		t.Run(name, func(t *testing.T) {
			planA := buildTestPlan(t, "sha256:"+strings.Repeat("a", 64), false)
			runtime, state, source, executor, publisher, fence, domainA := newRuntimeFixture(t, planA)
			generationA, err := runtime.Reconcile(t.Context(), planA.Repository, []DomainPlan{domainA})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := state.GetGenerationSchedule(
				t.Context(), planA.Repository, ScheduleStage,
			); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("initial zero-work schedule = %v", err)
			}

			planB := buildTestPlan(t, "sha256:"+strings.Repeat("b", 64), true)
			fence.current = planB.SourceGenerationDigest
			_, err = runtime.Reconcile(t.Context(), planB.Repository, []DomainPlan{{
				Schema: DomainSchema, RunID: "run-b", Plan: planB,
			}})
			if err != nil {
				t.Fatal(err)
			}
			chunkB := currentChunk(t, state, planB.Repository, 0)
			scheduleB, err := state.GetGenerationSchedule(t.Context(), planB.Repository, ScheduleStage)
			if err != nil {
				t.Fatal(err)
			}
			if settled {
				if err := runtime.Handle(t.Context(), chunkB); err != nil {
					t.Fatal(err)
				}
				state.settle(planB.Repository, 0)
				fence.current = planA.SourceGenerationDigest
				generation, err := runtime.openGeneration(
					runtime.generationDirectory(planA.Repository, generationA),
					planA.Repository, generationA,
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := runtime.reactivateComplete(
					t.Context(), generation,
					runtime.generationDirectory(planA.Repository, generationA),
				); err != nil {
					t.Fatal(err)
				}
			}

			authority, err := authorityForPlans(planA.Repository, []DomainPlan{domainA})
			if err != nil {
				t.Fatal(err)
			}
			acquiredBefore, releasedBefore := source.counts()
			executedBefore, publishedBefore, fencedBefore := executor.callCount(), publisher.calls, fence.calls
			target, reused, err := runtime.ReuseAuthority(t.Context(), authority)
			if err != nil || !reused || target != generationA {
				t.Fatalf("reuse zero-work A after B = %q/%t, %v", target, reused, err)
			}
			if _, err := state.GetGenerationSchedule(
				t.Context(), planA.Repository, ScheduleStage,
			); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("retired schedule = %v", err)
			}
			if _, err := os.Stat(runtime.bindingPath(
				planA.Repository, recoveryGeneration(generationA, scheduleB.Digest),
			)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("zero-work transition binding = %v", err)
			}
			root, err := runtime.Current(t.Context(), planA.Repository, planA.Domain)
			if err != nil || root.PlanDigest != planA.Digest {
				t.Fatalf("zero-work current root = %+v, %v", root, err)
			}
			progress, err := runtime.Progress(t.Context(), planA.Repository)
			if err != nil || progress != (Progress{State: "unavailable"}) {
				t.Fatalf("zero-work progress = %+v, %v", progress, err)
			}
			acquiredAfter, releasedAfter := source.counts()
			if acquiredAfter != acquiredBefore || releasedAfter != releasedBefore ||
				executor.callCount() != executedBefore || publisher.calls != publishedBefore ||
				fence.calls != fencedBefore {
				t.Fatalf(
					"zero-work reuse performed work: source %d/%d -> %d/%d executor %d -> %d publisher %d -> %d fence %d -> %d",
					acquiredBefore, releasedBefore, acquiredAfter, releasedAfter,
					executedBefore, executor.callCount(), publishedBefore, publisher.calls,
					fencedBefore, fence.calls,
				)
			}
		})
	}
}

func TestRuntimeNewZeroWorkGenerationRetiresActivePredecessor(t *testing.T) {
	planB := buildTestPlan(t, "sha256:"+strings.Repeat("c", 64), true)
	runtime, state, source, executor, _, fence, domainB := newRuntimeFixture(t, planB)
	if _, err := runtime.Reconcile(t.Context(), planB.Repository, []DomainPlan{domainB}); err != nil {
		t.Fatal(err)
	}
	scheduleB, err := state.GetGenerationSchedule(t.Context(), planB.Repository, ScheduleStage)
	if err != nil {
		t.Fatal(err)
	}
	planA := buildTestPlan(t, "sha256:"+strings.Repeat("d", 64), false)
	fence.current = planA.SourceGenerationDigest
	generationA, err := runtime.Reconcile(t.Context(), planA.Repository, []DomainPlan{{
		Schema: DomainSchema, RunID: "run-zero", Plan: planA,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.GetGenerationSchedule(
		t.Context(), planA.Repository, ScheduleStage,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("retired schedule = %v", err)
	}
	if _, err := os.Stat(runtime.bindingPath(
		planA.Repository, recoveryGeneration(generationA, scheduleB.Digest),
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new zero-work transition binding = %v", err)
	}
	root, err := runtime.Current(t.Context(), planA.Repository, planA.Domain)
	if err != nil || root.PlanDigest != planA.Digest {
		t.Fatalf("new zero-work current root = %+v, %v", root, err)
	}
	if acquired, released := source.counts(); acquired != 0 || released != 0 || executor.callCount() != 0 {
		t.Fatalf("new zero-work content work = source %d/%d executor %d", acquired, released, executor.callCount())
	}
}

func TestRuntimeZeroApplicablePublishesHonestEmptyWithoutSchedule(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("7", 64), false)
	runtime, state, source, executor, _, _, domain := newRuntimeFixture(t, plan)
	generation, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.GetGenerationSchedule(t.Context(), plan.Repository, ScheduleStage); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("zero plan schedule error = %v", err)
	}
	root, err := runtime.Current(t.Context(), plan.Repository, plan.Domain)
	if err != nil || root.Disposition != candidate.PartitionResultEmpty || root.ResultCount != 0 {
		t.Fatalf("empty root = %+v, %v", root, err)
	}
	status, err := runtime.Status(t.Context(), plan.Repository, generation)
	if err != nil || !status.Domains[0].Current || status.Domains[0].Expected != 0 {
		t.Fatalf("empty status = %+v, %v", status, err)
	}
	if acquired, _ := source.counts(); acquired != 0 || executor.callCount() != 0 {
		t.Fatalf("empty plan performed work: source %d executor %d", acquired, executor.callCount())
	}
}

func TestRuntimeRefusesMalformedReservationBeforeContentLease(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("8", 64), true)
	runtime, _, source, _, _, _, domain := newRuntimeFixture(t, plan)
	domain.Plan.Expected[0].Reservation.Facts = int64(domain.Plan.Expected[0].Quotas.Facts) + 1
	if _, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed reservation error = %v", err)
	}
	if acquired, _ := source.counts(); acquired != 0 {
		t.Fatalf("invalid plan acquired %d content leases", acquired)
	}
}

func TestRuntimeConcurrentDomainsStayIndependentAndAssembleOnce(t *testing.T) {
	sourceDigest := "sha256:" + strings.Repeat("9", 64)
	proto := buildTestPlanForDomain(t, sourceDigest, true, "proto-contract", ".proto")
	thrift := buildTestPlanForDomain(t, sourceDigest, true, "thrift-contract", ".thrift")
	runtime, state, source, executor, publisher, _, _ := newRuntimeFixture(t, proto)
	_, err := runtime.Reconcile(t.Context(), proto.Repository, []DomainPlan{
		{Schema: DomainSchema, RunID: "run-proto", Plan: proto},
		{Schema: DomainSchema, RunID: "run-thrift", Plan: thrift},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsByOffset := make([]error, 2)
	for offset := range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsByOffset[offset] = runtime.Handle(t.Context(), currentChunk(t, state, proto.Repository, offset))
		}()
	}
	wait.Wait()
	if err := errors.Join(errorsByOffset...); err != nil {
		t.Fatal(err)
	}
	if acquired, released := source.counts(); acquired != 2 || released != 2 || executor.callCount() != 2 {
		t.Fatalf("concurrent work = source %d/%d executor %d", acquired, released, executor.callCount())
	}
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if len(publisher.published) != 2 || publisher.calls != 2 {
		t.Fatalf("published domains/calls = %d/%d", len(publisher.published), publisher.calls)
	}
}

func TestRuntimeCompletionControlDefersSingleLinearResultPass(t *testing.T) {
	plan := buildTestPlanForDomainAndTyped(
		t, "sha256:"+strings.Repeat("b", 64), true,
		"proto-contract", ".proto", true,
	)
	if len(plan.Expected) != 2 {
		t.Fatalf("two-partition plan has %d expectations", len(plan.Expected))
	}
	runtime, schedules, _, _, _, _, domain := newRuntimeFixture(t, plan)
	generation, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, schedules, plan.Repository, 0)); err != nil {
		t.Fatal(err)
	}
	resultDirectory := filepath.Join(
		runtime.generationDirectory(plan.Repository, generation), domainKey(plan.Domain),
	)
	completion, err := readCompletionControl(filepath.Join(resultDirectory, completionName()), plan)
	if err != nil || completion.Count != 1 {
		t.Fatalf("partial completion = %+v, %v", completion, err)
	}
	if _, err := os.Lstat(filepath.Join(resultDirectory, rootName())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial completion root = %v", err)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, schedules, plan.Repository, 1)); err != nil {
		t.Fatal(err)
	}
	completion, err = readCompletionControl(filepath.Join(resultDirectory, completionName()), plan)
	if err != nil || completion.Count != 2 {
		t.Fatalf("complete progress = %+v, %v", completion, err)
	}
	if _, err := runtime.Current(t.Context(), plan.Repository, plan.Domain); err != nil {
		t.Fatal(err)
	}
}

func digestForPlanSet(repository string, plans ...string) string {
	semantic := struct {
		Schema     string   `json:"schema"`
		Repository string   `json:"repository"`
		Plans      []string `json:"plans"`
	}{Schema: GenerationSchema, Repository: repository, Plans: plans}
	raw, _ := canonical(semantic)
	return digest("phebs-extraction-partition-generation-v1", raw)
}

func buildTestPlan(t *testing.T, sourceDigest string, admitted bool) candidate.DomainResultPlan {
	return buildTestPlanForDomain(t, sourceDigest, admitted, "proto-contract", ".proto")
}

func TestReservedPlanSelectsV3OnlyForKafkaProducer(t *testing.T) {
	sourceDigest := "sha256:" + strings.Repeat("9", 64)
	kafka := buildTestPlanForDomain(
		t, sourceDigest, true, candidate.DomainResultPlanV3Domain, ".go",
	)
	if kafka.Schema != candidate.DomainResultPlanSchemaV3 ||
		kafka.Limits != candidate.FrozenDomainResultLimitsV3() {
		t.Fatalf("kafka result contract = %s %+v", kafka.Schema, kafka.Limits)
	}
	proto := buildTestPlan(t, sourceDigest, true)
	if proto.Schema != candidate.DomainResultPlanSchemaV2 ||
		proto.Limits != candidate.FrozenDomainResultLimitsV2() {
		t.Fatalf("historical result contract = %s %+v", proto.Schema, proto.Limits)
	}
}

func buildTestPlanForDomain(
	t *testing.T,
	sourceDigest string,
	admitted bool,
	domainName,
	suffix string,
) candidate.DomainResultPlan {
	return buildTestPlanForDomainAndTyped(t, sourceDigest, admitted, domainName, suffix, false)
}

func buildTestPlanForDomainAndTyped(
	t *testing.T,
	sourceDigest string,
	admitted bool,
	domainName,
	suffix string,
	typed bool,
) candidate.DomainResultPlan {
	t.Helper()
	repository := "example.invalid/partitioned"
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "partition@example.invalid")
	runGit(t, repo, "config", "user.name", "Partition Test")
	name := "README.md"
	if admitted {
		name = "schema" + suffix
	}
	if err := os.WriteFile(filepath.Join(repo, name), []byte("syntax = \"proto3\";\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := []string{name}
	if typed {
		if err := os.WriteFile(filepath.Join(repo, "index.scip"), []byte("typed-index"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, "index.scip")
	}
	runGit(t, repo, append([]string{"add", "--"}, paths...)...)
	runGit(t, repo, "commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	policy := candidate.Policy{
		Domain: domainName, Version: "3.0.0",
		EnumerationPolicy: domainName + "-paths-v1", Plane: candidate.PlaneRepository,
		Enumerate: func(path string) bool { return strings.HasSuffix(path, suffix) },
		Required:  func(path string) bool { return strings.HasSuffix(path, suffix) },
	}
	if typed {
		policy.TypedInputs = []string{"scip"}
	}
	policies := []candidate.Policy{policy}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := candidate.PolicyDigest(identities)
	if err != nil {
		t.Fatal(err)
	}
	candidateDirectory := t.TempDir()
	manifest, err := candidate.Build(t.Context(), candidate.Request{
		RepoDir: repo, OutputDir: candidateDirectory, Repository: repository,
		Commit: commit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.Open(candidateDirectory, candidate.Expected{
		Repository: repository, Commit: commit, Policies: identities,
		PolicyDigest: policyDigest, GenerationDigest: manifest.GenerationDigest,
		ManifestDigest: manifest.Digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	sparseDirectory := t.TempDir()
	sparseRoot, err := candidate.BuildSparseRoot(t.Context(), sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := candidate.OpenSparse(
		t.Context(), sparseDirectory, candidateDirectory, manifest.State(), sparseRoot.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), domainName, "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildReservedPlan(domain, candidate.DomainResultAuthority{
		SourceGenerationDigest:      sourceDigest,
		ObservationGenerationDigest: "sha256:" + strings.Repeat("a", 64),
		ExtractorVersion:            "3.0.0",
		ExtractionPolicyDigest:      "sha256:" + strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
