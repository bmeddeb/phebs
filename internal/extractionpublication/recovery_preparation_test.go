package extractionpublication

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

type recoveryPreparationSchedules struct {
	*testScheduleStore
	reads         atomic.Int64
	enqueues      atomic.Int64
	beforeEnqueue func() error
}

func (state *recoveryPreparationSchedules) GetGenerationSchedule(ctx context.Context, repository, stage string) (*store.GenerationSchedule, error) {
	state.reads.Add(1)
	return state.testScheduleStore.GetGenerationSchedule(ctx, repository, stage)
}

func (state *recoveryPreparationSchedules) EnqueueGenerationSchedule(ctx context.Context, spec store.GenerationScheduleSpec) (*store.GenerationSchedule, error) {
	state.enqueues.Add(1)
	if state.beforeEnqueue != nil {
		if err := state.beforeEnqueue(); err != nil {
			return nil, err
		}
	}
	value, err := state.testScheduleStore.EnqueueGenerationSchedule(ctx, spec)
	if err == nil {
		err = store.ValidateGenerationSchedule(*value)
	}
	return value, err
}

type recoveryPreparationEvidence struct {
	*testExtractionRunStore
	reads atomic.Int64
}

func (state *recoveryPreparationEvidence) GetPartitionedExtractionDomain(ctx context.Context, repository, domain string) (*store.PartitionedExtractionDomain, error) {
	state.reads.Add(1)
	value, err := state.testExtractionRunStore.GetPartitionedExtractionDomain(ctx, repository, domain)
	if err == nil && (value.Repository != repository || value.Domain != domain) {
		return nil, store.ErrNotFound
	}
	return value, err
}

type recoveryPreparationFixture struct {
	reconciler *Reconciler
	request    RecoveryPreparationRequest
	schedules  *recoveryPreparationSchedules
	evidence   *recoveryPreparationEvidence
	source     *testSource
	executor   *testExecutor
	publisher  *testPublisher
	domain     DomainPlan
	generation Generation
	root       candidate.DomainResultRoot
}

func newRecoveryPreparationFixture(t *testing.T, mode string) recoveryPreparationFixture {
	t.Helper()
	return newRecoveryPreparationFixtureForRepository(t, mode, "example.invalid/recovery-preparation")
}

func newRecoveryPreparationFixtureForRepository(t *testing.T, mode, repository string) recoveryPreparationFixture {
	t.Helper()
	candidateFixture := buildCandidateFixtureAt(t, repository, filepath.Join(t.TempDir(), "repository"), t.TempDir())
	const source = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	const observation = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	schedules := &recoveryPreparationSchedules{testScheduleStore: &testScheduleStore{}}
	evidence := &recoveryPreparationEvidence{testExtractionRunStore: &testExtractionRunStore{}}
	fixture := recoveryPreparationFixture{
		schedules: schedules, evidence: evidence, source: &testSource{}, executor: &testExecutor{}, publisher: &testPublisher{},
	}
	runtime := &Runtime{
		Root: t.TempDir(), Store: schedules, Source: fixture.source, Executor: fixture.executor,
		Publisher: fixture.publisher, Fence: &testFence{current: source},
	}
	fixture.reconciler = &Reconciler{
		Root: runtime.Root, CandidateRoot: candidateFixture.candidateDirectory, Runtime: runtime, Evidence: evidence,
		RecoveryPreparationEnabled: true,
		OpenCandidate: func(context.Context, string) (*candidate.Publication, error) {
			return candidateFixture.publication, nil
		},
		Authority: func(context.Context, string) (string, string, error) { return source, observation, nil },
		CandidateReference: func(context.Context, string) (candidate.State, error) {
			return candidateFixture.publication.State(), nil
		},
		AuthorityReference: func(context.Context, string) (string, string, error) { return source, observation, nil },
	}
	target, err := fixture.reconciler.Reconcile(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	fixture.generation, err = runtime.openGeneration(runtime.generationDirectory(repository, target), repository, target)
	if err != nil || len(fixture.generation.Domains) != 1 || fixture.generation.WorkItems == 0 {
		t.Fatalf("native fixture generation = %+v, %v", fixture.generation, err)
	}
	for ordinal := range fixture.generation.WorkItems {
		if err := runtime.Handle(t.Context(), currentChunk(t, schedules.testScheduleStore, repository, ordinal)); err != nil {
			t.Fatal(err)
		}
	}
	schedules.settle(repository, 0)
	prior, err := schedules.GetGenerationSchedule(t.Context(), repository, ScheduleStage)
	if err != nil || store.ValidateGenerationSchedule(*prior) != nil {
		t.Fatalf("native completed schedule is invalid: %+v, %v", prior, err)
	}
	fixture.domain, err = runtime.openDomainPlan(runtime.generationDirectory(repository, target), fixture.generation.Domains[0])
	if err != nil {
		t.Fatal(err)
	}
	fixture.root, err = runtime.Current(t.Context(), repository, fixture.domain.Plan.Domain)
	if err != nil {
		t.Fatal(err)
	}
	planRaw, err := canonical(fixture.domain.Plan)
	if err != nil {
		t.Fatal(err)
	}
	rootRaw, err := canonical(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	// The runtime fixture's publisher counts transitions. Its strict store
	// projection here preserves the native plan/root/run that actually finished.
	evidence.published = map[string]store.PartitionedExtractionDomain{
		fixture.domain.Plan.Domain: {
			Schema: store.PartitionedExtractionDomainSchema, Repository: repository, Domain: fixture.domain.Plan.Domain,
			RunID: fixture.domain.RunID, PlanDigest: fixture.domain.Plan.Digest, RootDigest: fixture.root.Digest,
			CandidateDigest: fixture.domain.Plan.CandidateManifestDigest, SourceDigest: source, ObservationDigest: observation,
			Facts: fixture.root.Totals.Facts, Rows: fixture.root.Totals.Rows, References: fixture.root.Totals.References,
			Plan: string(planRaw), Root: string(rootRaw),
		},
	}
	authority, err := authorityForPlans(repository, []DomainPlan{fixture.domain})
	if err != nil {
		t.Fatal(err)
	}
	fixture.request = RecoveryPreparationRequest{
		Schema: RecoveryPreparationSchema, Authority: authority, GenerationDigest: target, PriorScheduleDigest: prior.Digest,
		Roots: []RecoveryPreparationRoot{{Domain: fixture.domain.Plan.Domain, PlanDigest: fixture.domain.Plan.Digest, RootDigest: fixture.root.Digest}},
		Mode:  mode, TargetDomain: fixture.domain.Plan.Domain, TargetOrdinal: 0,
	}
	schedules.reads.Store(0)
	schedules.enqueues.Store(0)
	evidence.reads.Store(0)
	return fixture
}

type recoveryPreparationSnapshot struct {
	files        map[string][]byte
	modes        map[string]fs.FileMode
	schedules    map[string]store.GenerationSchedule
	publications map[string]store.PartitionedExtractionDomain
	enqueues     int64
	acquired     int
	released     int
	executions   int
	published    int
	begins       int
	aborts       int
}

func (fixture recoveryPreparationFixture) snapshot(t *testing.T) recoveryPreparationSnapshot {
	t.Helper()
	result := recoveryPreparationSnapshot{files: make(map[string][]byte), modes: make(map[string]fs.FileMode), schedules: make(map[string]store.GenerationSchedule), publications: make(map[string]store.PartitionedExtractionDomain)}
	err := filepath.WalkDir(fixture.reconciler.Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result.modes[path] = info.Mode()
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err == nil {
			result.files[path] = raw
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.schedules.mu.Lock()
	for key, value := range fixture.schedules.current {
		result.schedules[key] = value
	}
	fixture.schedules.mu.Unlock()
	fixture.evidence.mu.Lock()
	for key, value := range fixture.evidence.published {
		result.publications[key] = value
	}
	fixture.evidence.mu.Unlock()
	result.enqueues = fixture.schedules.enqueues.Load()
	result.acquired, result.released = fixture.source.counts()
	result.executions = fixture.executor.callCount()
	fixture.publisher.mu.Lock()
	result.published = fixture.publisher.calls
	fixture.publisher.mu.Unlock()
	result.begins, result.aborts = fixture.evidence.counts()
	return result
}

func (fixture recoveryPreparationFixture) resultDirectory() string {
	return filepath.Join(fixture.reconciler.Runtime.generationDirectory(fixture.request.Authority.Repository, fixture.request.GenerationDigest), domainKey(fixture.domain.Plan.Domain))
}

func TestRecoveryPreparationIsDefaultInactive(t *testing.T) {
	for _, reconciler := range []*Reconciler{nil, {}} {
		if _, err := reconciler.PrepareRecovery(t.Context(), RecoveryPreparationRequest{}); !errors.Is(err, ErrRecoveryPreparationDisabled) {
			t.Fatalf("default inactive hook = %v", err)
		}
	}
	fixture := newRecoveryPreparationFixture(t, RecoveryPreparationScheduleOnly)
	fixture.reconciler.RecoveryPreparationEnabled = false
	fixture.reconciler.CandidateReference = func(context.Context, string) (candidate.State, error) {
		t.Fatal("disabled preparation inspected candidate authority")
		return candidate.State{}, nil
	}
	before := fixture.snapshot(t)
	if _, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request); !errors.Is(err, ErrRecoveryPreparationDisabled) {
		t.Fatalf("disabled live hook = %v", err)
	}
	if fixture.schedules.reads.Load() != 0 || fixture.evidence.reads.Load() != 0 || !reflect.DeepEqual(before, fixture.snapshot(t)) {
		t.Fatal("disabled hook performed control reads or mutations")
	}
}

func TestRecoveryPreparationRefusesBeforeAnyMutation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*testing.T, *recoveryPreparationFixture)
	}{
		{"schema", func(_ *testing.T, fixture *recoveryPreparationFixture) { fixture.request.Schema = "unknown" }},
		{"mode", func(_ *testing.T, fixture *recoveryPreparationFixture) { fixture.request.Mode = "rewrite_all" }},
		{"candidate_authority", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			fixture.request.Authority.CandidateManifestDigest = "sha256:" + strings.Repeat("8", 64)
		}},
		{"observation_authority", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			fixture.request.Authority.ObservationGenerationDigest = "sha256:" + strings.Repeat("8", 64)
		}},
		{"authority_changed_during_validation", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			read := fixture.reconciler.CandidateReference
			calls := 0
			fixture.reconciler.CandidateReference = func(ctx context.Context, repository string) (candidate.State, error) {
				value, err := read(ctx, repository)
				calls++
				if calls > 1 {
					value.ManifestDigest = "sha256:" + strings.Repeat("8", 64)
				}
				return value, err
			}
		}},
		{"generation", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			fixture.request.GenerationDigest = "sha256:" + strings.Repeat("8", 64)
		}},
		{"predecessor", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			fixture.request.PriorScheduleDigest = "sha256:" + strings.Repeat("8", 64)
		}},
		{"root", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			fixture.request.Roots[0].RootDigest = "sha256:" + strings.Repeat("8", 64)
		}},
		{"plan", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			fixture.request.Roots[0].PlanDigest = "sha256:" + strings.Repeat("8", 64)
		}},
		{"missing_root", func(_ *testing.T, fixture *recoveryPreparationFixture) { fixture.request.Roots = nil }},
		{"duplicate_root", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			fixture.request.Roots = append(fixture.request.Roots, fixture.request.Roots[0])
		}},
		{"target_domain", func(_ *testing.T, fixture *recoveryPreparationFixture) { fixture.request.TargetDomain = "grpc-caller" }},
		{"target_ordinal", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			fixture.request.TargetOrdinal = len(fixture.domain.Plan.Expected)
		}},
		{"negative_ordinal", func(_ *testing.T, fixture *recoveryPreparationFixture) { fixture.request.TargetOrdinal = -1 }},
		{"schedule_only_missing_target", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			fixture.request.Mode = RecoveryPreparationScheduleOnly
			fixture.request.TargetDomain = ""
		}},
		{"store_root", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			value := fixture.evidence.published[fixture.domain.Plan.Domain]
			value.RootDigest = "sha256:" + strings.Repeat("8", 64)
			fixture.evidence.published[fixture.domain.Plan.Domain] = value
		}},
		{"store_run", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			value := fixture.evidence.published[fixture.domain.Plan.Domain]
			value.RunID = "other-staged-run"
			fixture.evidence.published[fixture.domain.Plan.Domain] = value
		}},
		{"active_schedule", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			key := scheduleKey(fixture.request.Authority.Repository, ScheduleStage)
			value := fixture.schedules.current[key]
			value.Status = store.GenerationScheduleActive
			fixture.schedules.current[key] = value
		}},
		{"failed_schedule", func(_ *testing.T, fixture *recoveryPreparationFixture) {
			fixture.schedules.settle(fixture.request.Authority.Repository, 1)
		}},
		{"incomplete_bitmap", func(t *testing.T, fixture *recoveryPreparationFixture) {
			if err := writeAtomicCanonical(filepath.Join(fixture.resultDirectory(), completionName()), newCompletionControl(fixture.domain.Plan)); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing_result", func(t *testing.T, fixture *recoveryPreparationFixture) {
			if err := os.Remove(filepath.Join(fixture.resultDirectory(), resultName(0))); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRecoveryPreparationFixture(t, RecoveryPreparationCheckpoint)
			test.edit(t, &fixture)
			before := fixture.snapshot(t)
			if _, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request); err == nil {
				t.Fatal("invalid completed-generation preparation was accepted")
			}
			if !reflect.DeepEqual(before, fixture.snapshot(t)) {
				t.Fatal("refused preparation mutated durable controls, evidence, schedules, or source work")
			}
		})
	}
}

func TestRecoveryPreparationPreservesNativeEvidenceAndBindsSuccessor(t *testing.T) {
	for _, mode := range []string{RecoveryPreparationScheduleOnly, RecoveryPreparationCheckpoint} {
		t.Run(mode, func(t *testing.T) {
			fixture := newRecoveryPreparationFixture(t, mode)
			before := fixture.snapshot(t)
			schedule, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request)
			if err != nil || schedule == nil || store.ValidateGenerationSchedule(*schedule) != nil {
				t.Fatalf("prepare recovery schedule = %+v, %v", schedule, err)
			}
			wantGeneration := recoveryGeneration(fixture.request.GenerationDigest, fixture.request.PriorScheduleDigest)
			if schedule.Generation != wantGeneration || schedule.Status != store.GenerationScheduleActive || schedule.Digest == fixture.request.PriorScheduleDigest {
				t.Fatalf("preparation did not create native predecessor-bound operational successor: %+v", schedule)
			}
			binding, err := fixture.reconciler.Runtime.readBinding(fixture.request.Authority.Repository, schedule.Generation)
			if err != nil || binding.TargetGeneration != fixture.request.GenerationDigest || binding.PriorSchedule != fixture.request.PriorScheduleDigest {
				t.Fatalf("successor binding = %+v, %v", binding, err)
			}
			after := fixture.snapshot(t)
			if !reflect.DeepEqual(before.publications, after.publications) || before.acquired != after.acquired || before.released != after.released ||
				before.executions != after.executions || before.published != after.published || before.begins != after.begins || before.aborts != after.aborts || after.enqueues != 1 {
				t.Fatal("preparation changed evidence authority, repeated source execution, or submitted more than one successor")
			}
			rootPath := filepath.Join(fixture.resultDirectory(), rootName())
			completionPath := filepath.Join(fixture.resultDirectory(), completionName())
			pointerPath := fixture.reconciler.Runtime.currentPath(fixture.request.Authority.Repository, fixture.domain.Plan.Domain)
			for path, raw := range before.files {
				if mode == RecoveryPreparationCheckpoint && (path == rootPath || path == completionPath || path == pointerPath) {
					continue
				}
				if !bytes.Equal(after.files[path], raw) {
					t.Fatalf("preparation changed preserved native bytes: %s", filepath.Base(path))
				}
			}
			if mode == RecoveryPreparationCheckpoint {
				if _, present := after.files[rootPath]; present {
					t.Fatal("checkpoint preparation retained assembled filesystem root")
				}
				if _, present := after.files[pointerPath]; present {
					t.Fatal("checkpoint preparation retained a dangling filesystem pointer")
				}
				completion, err := readCompletionControl(completionPath, fixture.domain.Plan)
				if err != nil || completion.Count != completion.Expected-1 || completion.Bits[0]&1 != 0 {
					t.Fatalf("checkpoint bitmap not reset: %+v, %v", completion, err)
				}
			}
			retryBefore := fixture.snapshot(t)
			if _, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request); err == nil || !reflect.DeepEqual(retryBefore, fixture.snapshot(t)) {
				t.Fatalf("old predecessor request was replayed or mutated controls: %v", err)
			}
			for ordinal := range fixture.generation.WorkItems {
				if err := fixture.reconciler.Runtime.Handle(t.Context(), currentChunk(t, fixture.schedules.testScheduleStore, fixture.request.Authority.Repository, ordinal)); err != nil {
					t.Fatal(err)
				}
			}
			fixture.schedules.settle(fixture.request.Authority.Repository, 0)
			restored, err := fixture.reconciler.Runtime.Current(t.Context(), fixture.request.Authority.Repository, fixture.domain.Plan.Domain)
			if err != nil || restored.Digest != fixture.root.Digest {
				t.Fatalf("ordinary recovery did not restore the same native root: %+v, %v", restored, err)
			}
			completed := fixture.snapshot(t)
			if completed.acquired != before.acquired || completed.executions != before.executions || !reflect.DeepEqual(completed.publications, before.publications) {
				t.Fatal("ordinary recovery failed to reuse preserved results and evidence authority")
			}
			for path, raw := range before.files {
				if !bytes.Equal(completed.files[path], raw) {
					t.Fatalf("ordinary recovery did not restore original native bytes: %s", filepath.Base(path))
				}
			}
			if _, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request); err == nil || !reflect.DeepEqual(completed, fixture.snapshot(t)) {
				t.Fatalf("old predecessor request was accepted after the successor settled: %v", err)
			}
		})
	}
}

func TestRecoveryPreparationSharesLiveReconcilerLock(t *testing.T) {
	fixture := newRecoveryPreparationFixture(t, RecoveryPreparationScheduleOnly)
	lock := &fixture.reconciler.mu[reconcileShard(fixture.request.Authority.Repository)]
	lock.Lock()
	locked := true
	defer func() {
		if locked {
			lock.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("preparation crossed the held live reconciler lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if fixture.schedules.reads.Load() != 0 || fixture.evidence.reads.Load() != 0 || fixture.schedules.enqueues.Load() != 0 {
		t.Fatal("preparation performed authority I/O before acquiring the shared reconciler lock")
	}
	lock.Unlock()
	locked = false
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preparation failed to resume after the live reconciler lock was released")
	}
}

func TestRecoveryPreparationCanceledLockWaitCannotMutateLater(t *testing.T) {
	fixture := newRecoveryPreparationFixture(t, RecoveryPreparationCheckpoint)
	lock := &fixture.reconciler.mu[reconcileShard(fixture.request.Authority.Repository)]
	lock.Lock()
	locked := true
	defer func() {
		if locked {
			lock.Unlock()
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	before := fixture.snapshot(t)
	done := make(chan error, 1)
	go func() {
		_, err := fixture.reconciler.PrepareRecovery(ctx, fixture.request)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled preparation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled preparation remained blocked on the reconciler mutex")
	}
	lock.Unlock()
	locked = false
	if !reflect.DeepEqual(before, fixture.snapshot(t)) || fixture.schedules.reads.Load() != 0 || fixture.evidence.reads.Load() != 0 {
		t.Fatal("canceled preparation retained authority I/O or a deferred mutation")
	}
}

// relabelGeneration deliberately changes every mutable name/binding together.
// This creates adversarial controls, not a production-derived generation.
func (fixture *recoveryPreparationFixture) relabelGeneration(t *testing.T, target string) {
	t.Helper()
	runtime := fixture.reconciler.Runtime
	repository := fixture.request.Authority.Repository
	from := runtime.generationDirectory(repository, fixture.request.GenerationDigest)
	to := runtime.generationDirectory(repository, target)
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	fixture.generation.Digest, fixture.request.GenerationDigest = target, target
	if err := writeAtomicCanonical(filepath.Join(to, generationName()), fixture.generation); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicCanonical(runtime.authorityBindingPath(fixture.request.Authority), authorityBinding{
		Schema: AuthorityBindingSchema, Authority: fixture.request.Authority, Target: target,
	}); err != nil {
		t.Fatal(err)
	}
	for _, root := range fixture.request.Roots {
		if err := writeAtomicCanonical(runtime.currentPath(repository, root.Domain), Pointer{
			Schema: PointerSchema, Repository: repository, Domain: root.Domain,
			GenerationDigest: target, PlanDigest: root.PlanDigest, RootDigest: root.RootDigest,
			RootName: filepath.Join(domainKey(root.Domain), rootName()),
		}); err != nil {
			t.Fatal(err)
		}
	}
	key := scheduleKey(repository, ScheduleStage)
	prior := fixture.schedules.current[key]
	prior.Generation = target
	var err error
	prior.Digest, err = store.GenerationScheduleDigest(store.GenerationScheduleSpec{
		Repository: prior.Repository, Stage: prior.Stage, Generation: prior.Generation,
		ResourceClass: prior.ResourceClass, TotalItems: prior.TotalItems, ChunkItems: prior.ChunkItems,
		MaxAttempts: prior.MaxAttempts, RepositoryTokens: prior.RepositoryTokens,
	})
	if err != nil || store.ValidateGenerationSchedule(prior) != nil {
		t.Fatalf("relabeled native schedule is invalid: %+v, %v", prior, err)
	}
	fixture.schedules.current[key] = prior
	fixture.request.PriorScheduleDigest = prior.Digest
	if err := runtime.writeBinding(scheduleBinding{
		Schema: BindingSchema, Repository: repository, ScheduleGeneration: target, TargetGeneration: target,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryPreparationRejectsCoherentlyRelabeledGenerationIdentity(t *testing.T) {
	fixture := newRecoveryPreparationFixture(t, RecoveryPreparationCheckpoint)
	derived := digestForPlanSet(fixture.request.Authority.Repository, fixture.domain.Plan.Digest)
	if derived != fixture.request.GenerationDigest {
		t.Fatal("baseline fixture does not have its production-derived semantic identity")
	}
	fake := "sha256:" + strings.Repeat("8", 64)
	fixture.relabelGeneration(t, fake)
	directory := fixture.reconciler.Runtime.generationDirectory(fixture.request.Authority.Repository, fake)
	// Both pre-existing fence checks accept these coherent mutable controls:
	// refusal must come from the added producer-identity derivation itself.
	if err := fixture.reconciler.confirmRecoveryAuthority(t.Context(), fixture.request, fixture.generation); err != nil {
		t.Fatalf("relabeled authority/schedule controls were not coherent: %v", err)
	}
	if err := fixture.reconciler.validateRecoveryDomain(t.Context(), directory, fixture.generation, fixture.domain, fixture.request.Roots[0]); err != nil {
		t.Fatalf("relabeled root/pointer/result controls were not coherent: %v", err)
	}
	fixture.schedules.reads.Store(0)
	fixture.evidence.reads.Store(0)
	fixture.reconciler.Runtime.Fence.(*testFence).before = func() { t.Error("invented generation reached the publication fence") }
	before := fixture.snapshot(t)
	if _, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request); !errors.Is(err, ErrStale) {
		t.Fatalf("coherently relabeled generation refusal = %v", err)
	}
	if !reflect.DeepEqual(before, fixture.snapshot(t)) || fixture.schedules.reads.Load() != 0 || fixture.evidence.reads.Load() != 0 {
		t.Fatal("invented semantic generation reached authority reads or mutations")
	}
}

func TestRecoveryPreparationRejectsCanonicalForeignRepositoryPlanBeforeFence(t *testing.T) {
	fixture := newRecoveryPreparationFixture(t, RecoveryPreparationCheckpoint)
	foreign := newRecoveryPreparationFixtureForRepository(t, RecoveryPreparationCheckpoint, "example.invalid/foreign-preparation")
	if candidate.ValidateDomainResultPlanControl(foreign.domain.Plan) != nil || candidate.ValidateDomainResultRoot(foreign.root, foreign.domain.Plan) != nil {
		t.Fatal("foreign fixture is not a canonical native plan/root")
	}
	directory := fixture.reconciler.Runtime.generationDirectory(fixture.request.Authority.Repository, fixture.request.GenerationDigest)
	descriptor := fixture.generation.Domains[0]
	descriptor.PlanDigest, descriptor.RunID = foreign.domain.Plan.Digest, foreign.domain.RunID
	fixture.generation.Domains[0] = descriptor
	if err := writeAtomicCanonical(filepath.Join(directory, generationName()), fixture.generation); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicCanonical(filepath.Join(directory, descriptor.PlanName), foreign.domain); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{completionName(), resultName(0), rootName()} {
		raw, err := os.ReadFile(filepath.Join(foreign.resultDirectory(), name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.resultDirectory(), name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Every supplied digest matches the transplanted native bytes. The only
	// invalid edge at this point is the requested repository owning a foreign
	// plan; reject that edge before the fence or foreign pointer/store reads.
	fixture.request.Roots[0].PlanDigest, fixture.request.Roots[0].RootDigest = foreign.domain.Plan.Digest, foreign.root.Digest
	var err error
	fixture.request.Authority, err = authorityForPlans(fixture.request.Authority.Repository, []DomainPlan{foreign.domain})
	if err != nil {
		t.Fatal(err)
	}
	// Keep the new producer-identity guard satisfied so this regression still
	// isolates the foreign plan's repository edge rather than an old digest.
	target := digestForPlanSet(fixture.request.Authority.Repository, foreign.domain.Plan.Digest)
	fixture.relabelGeneration(t, target)
	if fixture.generation.Digest != digestForPlanSet(fixture.generation.Repository, fixture.generation.Domains[0].PlanDigest) {
		t.Fatal("foreign plan fixture is not bound by the exact native generation recipe")
	}
	fence := fixture.reconciler.Runtime.Fence.(*testFence)
	fence.before = func() { t.Error("foreign repository plan reached the publication fence") }
	before, foreignBefore := fixture.snapshot(t), foreign.snapshot(t)
	if _, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request); err == nil {
		t.Fatal("canonical foreign repository plan was accepted")
	}
	if fixture.evidence.reads.Load() != 0 || fixture.schedules.reads.Load() != 0 ||
		!reflect.DeepEqual(before, fixture.snapshot(t)) || !reflect.DeepEqual(foreignBefore, foreign.snapshot(t)) {
		t.Fatal("foreign plan reached authority reads or mutated either repository")
	}
}

type cancelRecoveryCheckpointContext struct {
	context.Context
	cancel   context.CancelFunc
	check    int
	cancelAt int
}

func (ctx *cancelRecoveryCheckpointContext) Err() error {
	ctx.check++
	if ctx.check == ctx.cancelAt {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestRecoveryPreparationCheckpointCancellationKeepsSafePrefix(t *testing.T) {
	for _, test := range []struct {
		name     string
		cancelAt int
	}{
		{"before_bitmap", 1},
		{"after_bitmap", 2},
		{"after_pointer_removal", 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRecoveryPreparationFixture(t, RecoveryPreparationCheckpoint)
			before := fixture.snapshot(t)
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx := &cancelRecoveryCheckpointContext{Context: parent, cancel: cancel, cancelAt: test.cancelAt}
			directory := fixture.reconciler.Runtime.generationDirectory(fixture.request.Authority.Repository, fixture.request.GenerationDigest)
			err := fixture.reconciler.Runtime.prepareRecoveryCheckpoint(ctx, directory, fixture.domain, 0)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("checkpoint cancellation = %v", err)
			}
			after := fixture.snapshot(t)
			if test.cancelAt == 1 {
				if !reflect.DeepEqual(before, after) {
					t.Fatal("canceled-before-work checkpoint mutated state")
				}
				return
			}
			if !reflect.DeepEqual(before.publications, after.publications) || !reflect.DeepEqual(before.schedules, after.schedules) || after.enqueues != 0 {
				t.Fatal("partial checkpoint cancellation published, enqueued, or reset old jobs")
			}
			completionPath := filepath.Join(fixture.resultDirectory(), completionName())
			pointerPath := fixture.reconciler.Runtime.currentPath(fixture.request.Authority.Repository, fixture.domain.Plan.Domain)
			for path, raw := range before.files {
				if path == completionPath || test.cancelAt == 3 && path == pointerPath {
					continue
				}
				if !bytes.Equal(after.files[path], raw) {
					t.Fatalf("checkpoint cancellation damaged preserved bytes: %s", filepath.Base(path))
				}
			}
			if test.cancelAt == 3 {
				if _, present := after.files[pointerPath]; present {
					t.Fatal("pointer-removal cancellation restored a pointer")
				}
			}
			completion, err := readCompletionControl(completionPath, fixture.domain.Plan)
			if err != nil || completion.Count != completion.Expected-1 {
				t.Fatalf("partial checkpoint lost its recorded bitmap prefix: %+v, %v", completion, err)
			}
		})
	}
}

func TestRecoveryPreparationTerminalEnqueueAndLateAuthorityFailuresKeepCommittedPrefix(t *testing.T) {
	for _, test := range []struct {
		name string
		late bool
	}{
		{name: "enqueue_failed"},
		{name: "authority_changed_during_enqueue", late: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRecoveryPreparationFixture(t, RecoveryPreparationCheckpoint)
			before := fixture.snapshot(t)
			injected := errors.New("injected enqueue failure")
			fixture.schedules.beforeEnqueue = func() error {
				if !test.late {
					return injected
				}
				read := fixture.reconciler.CandidateReference
				fixture.reconciler.CandidateReference = func(ctx context.Context, repository string) (candidate.State, error) {
					value, err := read(ctx, repository)
					value.ManifestDigest = "sha256:" + strings.Repeat("8", 64)
					return value, err
				}
				return nil
			}
			_, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request)
			if test.late && !errors.Is(err, ErrStale) || !test.late && !errors.Is(err, injected) {
				t.Fatalf("terminal preparation failure = %v", err)
			}
			after := fixture.snapshot(t)
			if !reflect.DeepEqual(before.publications, after.publications) || before.acquired != after.acquired || before.executions != after.executions || after.enqueues != 1 {
				t.Fatal("terminal preparation failure changed precious authority or retried work")
			}
			rootPath := filepath.Join(fixture.resultDirectory(), rootName())
			pointerPath := fixture.reconciler.Runtime.currentPath(fixture.request.Authority.Repository, fixture.domain.Plan.Domain)
			if _, present := after.files[rootPath]; present {
				t.Fatal("terminal failure rolled back the checkpoint root removal")
			}
			if _, present := after.files[pointerPath]; present {
				t.Fatal("terminal failure rolled back the checkpoint pointer removal")
			}
			key := scheduleKey(fixture.request.Authority.Repository, ScheduleStage)
			current := after.schedules[key]
			if test.late {
				if current.Generation != recoveryGeneration(fixture.request.GenerationDigest, fixture.request.PriorScheduleDigest) || current.Status != store.GenerationScheduleActive {
					t.Fatal("late authority refusal discarded the committed native successor")
				}
			} else if !reflect.DeepEqual(before.schedules, after.schedules) {
				t.Fatal("failed enqueue reset the completed predecessor")
			}
			lock := &fixture.reconciler.mu[reconcileShard(fixture.request.Authority.Repository)]
			if !lock.TryLock() {
				t.Fatal("terminal preparation failure retained reconciler lock")
			}
			lock.Unlock()
			if fixture.reconciler.Runtime.Fence.(*testFence).active() {
				t.Fatal("terminal preparation failure retained publication fence")
			}
		})
	}
}
