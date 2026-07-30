package extract

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

func testDomainSchedulingLimits() domainSchedulingLimits {
	return domainSchedulingLimits{
		AggregateTimeout:    time.Second,
		MirrorTimeout:       900 * time.Millisecond,
		DomainTimeout:       200 * time.Millisecond,
		AbortTimeout:        20 * time.Millisecond,
		OutcomeTimeout:      20 * time.Millisecond,
		MinimumStartBudget:  2 * time.Millisecond,
		MaxSerialDomains:    maxSerialExtractionDomains,
		MaxSchedulerBytes:   maxExtractionSchedulerBytes,
		MaxStagedRows:       maxExtractionStagedRows,
		MaxDomainStagedRows: maxDomainExtractionStagedRows,
	}
}

func schedulerTestWorker(
	evidence *memoryEvidence,
	extractors []Extractor,
	limits domainSchedulingLimits,
) Worker {
	return Worker{
		Repos: readyRepoGetter(&store.Repo{
			Name: "host/scheduler", IndexedCommitHash: unitCommit,
		}),
		Evidence: evidence, NewCorpus: unitFactory(nil),
		Extractors: extractors, schedulingLimits: &limits,
		OperationReports: func([]byte) error {
			return nil
		},
	}
}

func TestDomainSchedulingLimitsAreFrozenAndConsistent(t *testing.T) {
	limits, err := effectiveDomainSchedulingLimits(nil)
	if err != nil {
		t.Fatal(err)
	}
	if limits.AggregateTimeout != 15*time.Minute ||
		limits.MirrorTimeout != 14*time.Minute+50*time.Second ||
		limits.DomainTimeout != 5*time.Minute ||
		limits.DomainTimeout >= limits.AggregateTimeout ||
		limits.MirrorTimeout > limits.AggregateTimeout ||
		limits.MaxSerialDomains != 16 ||
		limits.MaxSchedulerBytes != 64<<10 ||
		limits.MaxStagedRows != 100_000 ||
		limits.MaxDomainStagedRows != 25_000 {
		t.Fatalf("production scheduler limits = %+v", limits)
	}

	invalid := testDomainSchedulingLimits()
	invalid.DomainTimeout = invalid.AggregateTimeout
	if _, err := effectiveDomainSchedulingLimits(&invalid); err == nil {
		t.Fatal("accepted a per-domain cap equal to the aggregate cap")
	}
	invalid = testDomainSchedulingLimits()
	invalid.AggregateTimeout = productionDomainSchedulingLimits.AggregateTimeout +
		time.Second
	if _, err := effectiveDomainSchedulingLimits(&invalid); err == nil {
		t.Fatal("accepted an expanded aggregate cap")
	}
}

func TestScheduledDomainIdentityBoundIncludesRetainedOverhead(t *testing.T) {
	domain := scheduledDomain{
		extractor: registeredExtractor{domain: "domain", version: "v1"},
		scope: store.ExtractionScope{
			Repository: "host/repo",
			Commit:     unitCommit,
			Domain:     "domain",
		},
		generation: store.ExtractionGenerationIdentity{
			Extractor:        "v1",
			DependencyDigest: "sha256:" + string(make([]byte, 64)),
		},
	}
	got := scheduledDomainIdentityBytes(domain)
	raw := len(domain.extractor.domain) +
		len(domain.extractor.version) +
		len(domain.scope.Repository) +
		len(domain.scope.Commit) +
		len(domain.scope.Domain) +
		len(domain.generation.Extractor) +
		len(domain.generation.DependencyDigest)
	if got != raw+256 {
		t.Fatalf("scheduled identity bytes = %d, want %d retained bytes + 256",
			got, raw)
	}
	limits := testDomainSchedulingLimits()
	limits.MaxSchedulerBytes = got
	if err := validateScheduledDomains(
		[]scheduledDomain{domain}, limits,
	); err != nil {
		t.Fatalf("exact scheduler identity bound: %v", err)
	}
	limits.MaxSchedulerBytes--
	if err := validateScheduledDomains(
		[]scheduledDomain{domain}, limits,
	); !errors.Is(err, errExtractionAggregateBudget) {
		t.Fatalf("scheduler identity overflow = %v", err)
	}
}

func TestSchedulerIdentityRefusalDefersAllWithoutStarting(t *testing.T) {
	evidence := newMemoryEvidence()
	started := 0
	limits := testDomainSchedulingLimits()
	limits.MaxSchedulerBytes = 1
	worker := schedulerTestWorker(evidence, []Extractor{unitExtractor{
		domain: "too-large", version: "v1",
		extract: func(
			context.Context, sdk.Corpus, sdk.Emit,
		) (sdk.Coverage, error) {
			started++
			return sdk.Coverage{}, nil
		},
	}}, limits)
	err := worker.Handle(
		context.Background(), store.Job{Target: "host/scheduler"},
	)
	if !errors.Is(err, errExtractionAggregateBudget) ||
		store.IsYield(err) || started != 0 || evidence.nextRun != 0 {
		t.Fatalf("identity refusal err=%v yield=%v started=%d runs=%d",
			err, store.IsYield(err), started, evidence.nextRun)
	}
	outcome, outcomeErr := evidence.LatestExtractionDomainOutcome(
		context.Background(),
		store.ExtractionScope{
			Repository: "host/scheduler",
			Commit:     unitCommit,
			Domain:     "too-large",
		},
	)
	if outcomeErr != nil ||
		outcome.Disposition != store.DomainOutcomeRetryableFailure ||
		outcome.RunID != "" {
		t.Fatalf("identity refusal outcome = %+v, %v", outcome, outcomeErr)
	}
}

func TestSchedulerNoProgressDeferralConsumesOrdinaryAttempt(t *testing.T) {
	evidence := newMemoryEvidence()
	started := 0
	limits := testDomainSchedulingLimits()
	limits.AggregateTimeout = 120 * time.Millisecond
	limits.MirrorTimeout = 90 * time.Millisecond
	limits.DomainTimeout = 40 * time.Millisecond
	limits.AbortTimeout = 10 * time.Millisecond
	limits.OutcomeTimeout = 10 * time.Millisecond
	limits.MinimumStartBudget = 5 * time.Millisecond
	worker := schedulerTestWorker(evidence, []Extractor{unitExtractor{
		domain: "not-started", version: "v1",
		extract: func(
			context.Context, sdk.Corpus, sdk.Emit,
		) (sdk.Coverage, error) {
			started++
			return sdk.Coverage{}, nil
		},
	}}, limits)
	worker.Repos = repoGetterFunc(func(
		ctx context.Context,
		_ string,
	) (*store.Repo, error) {
		timer := time.NewTimer(75 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return &store.Repo{
				Name: "host/scheduler", IndexedCommitHash: unitCommit,
			}, nil
		}
	})
	err := worker.Handle(
		context.Background(), store.Job{Target: "host/scheduler"},
	)
	if !errors.Is(err, errExtractionAggregateBudget) ||
		store.IsYield(err) || started != 0 {
		t.Fatalf("zero-progress deferral err=%v yield=%v started=%d",
			err, store.IsYield(err), started)
	}
}

func TestSchedulerPreRunFailureDoesNotYieldWithoutDurableProgress(t *testing.T) {
	evidence := newMemoryEvidence()
	manifest := validMemoryCandidateManifest()
	manifest.walkErr = errors.New("temporary inventory read failure")
	extractors := []Extractor{
		unitExtractor{domain: "first", version: "v1"},
		unitExtractor{domain: "second", version: "v1"},
	}
	limits := testDomainSchedulingLimits()
	limits.MaxSerialDomains = 1
	worker := schedulerTestWorker(evidence, extractors, limits)
	worker.Manifests = splitManifestProvider{
		identity: func(
			context.Context,
			CandidateManifestRequest,
		) (string, error) {
			return manifest.Identity(), nil
		},
		open: func(
			context.Context,
			CandidateManifestRequest,
		) (CandidateManifest, error) {
			return manifest, nil
		},
	}
	err := worker.Handle(
		context.Background(), store.Job{Target: "host/scheduler"},
	)
	if !errors.Is(err, manifest.walkErr) || store.IsYield(err) ||
		evidence.nextRun != 0 {
		t.Fatalf("pre-run failure err=%v yield=%v runs=%d",
			err, store.IsYield(err), evidence.nextRun)
	}
}

func TestSchedulerEarlyTerminalFailureDoesNotStarvePeers(t *testing.T) {
	evidence := newMemoryEvidence()
	started := make([]string, 0, 3)
	extractor := func(domain string, terminal bool) Extractor {
		return unitExtractor{
			domain: domain, version: "v1",
			extract: func(
				context.Context, sdk.Corpus, sdk.Emit,
			) (sdk.Coverage, error) {
				started = append(started, domain)
				if terminal {
					return sdk.Coverage{}, operationLimitError("fixture refusal")
				}
				return sdk.Coverage{}, nil
			},
		}
	}
	worker := schedulerTestWorker(evidence, []Extractor{
		extractor("first", true),
		extractor("second", false),
		extractor("third", false),
	}, testDomainSchedulingLimits())

	if err := worker.Handle(
		context.Background(), store.Job{Target: "host/scheduler"},
	); !store.IsTerminal(err) {
		t.Fatalf("settled terminal peer error = %v, want terminal marker", err)
	}
	if want := []string{"first", "second", "third"}; !equalStrings(started, want) {
		t.Fatalf("starts = %v, want %v", started, want)
	}
	first, err := evidence.LatestExtractionDomainOutcome(
		context.Background(),
		store.ExtractionScope{
			Repository: "host/scheduler", Commit: unitCommit, Domain: "first",
		},
	)
	if err != nil ||
		first.Disposition != store.DomainOutcomeTerminalGenerationRefusal {
		t.Fatalf("first outcome = %+v, %v", first, err)
	}
}

func TestSchedulerRestartGivesEveryNeverAttemptedDomainAStart(t *testing.T) {
	evidence := newMemoryEvidence()
	started := make([]string, 0, 4)
	extractors := make([]Extractor, 0, 3)
	for _, domain := range []string{"a", "b", "c"} {
		domain := domain
		extractors = append(extractors, unitExtractor{
			domain: domain, version: "v1",
			extract: func(
				context.Context, sdk.Corpus, sdk.Emit,
			) (sdk.Coverage, error) {
				started = append(started, domain)
				return sdk.Coverage{}, errors.New("retry fixture")
			},
		})
	}
	limits := testDomainSchedulingLimits()
	limits.MaxSerialDomains = 1
	worker := schedulerTestWorker(evidence, extractors, limits)

	for attempt := range 4 {
		err := worker.Handle(
			context.Background(), store.Job{Target: "host/scheduler"},
		)
		if err == nil {
			t.Fatal("retryable fixture unexpectedly completed")
		}
		if got, want := store.IsYield(err), attempt < 2; got != want {
			t.Fatalf("attempt %d yield = %v, want %v: %v",
				attempt+1, got, want, err)
		}
	}
	if want := []string{"a", "b", "c", "a"}; !equalStrings(started, want) {
		t.Fatalf("restart starts = %v, want %v", started, want)
	}

	// Simulate a crash after B's newer BeginExtractionRun marker but before
	// its retryable outcome write. The newest durable attempt, not the older
	// outcome's run id, must move B behind C and A and survive deferral.
	bScope := store.ExtractionScope{
		Repository: "host/scheduler", Commit: unitCommit, Domain: "b",
	}
	evidence.mu.Lock()
	bRecordedAt := evidence.outcomes[memoryOutcomeKey(bScope)].RecordedAt
	evidence.attempts[memoryScopeKey(bScope)] = &store.ExtractionAttempt{
		RunID: "crashed-b", Repo: bScope.Repository, Commit: bScope.Commit,
		Domain: bScope.Domain, Extractor: "v1", Status: "staged",
		StartedAt: bRecordedAt.Add(time.Second),
	}
	evidence.mu.Unlock()
	if err := worker.Handle(
		context.Background(), store.Job{Target: "host/scheduler"},
	); err == nil {
		t.Fatal("post-crash retry fixture unexpectedly completed")
	}
	if want := []string{"a", "b", "c", "a", "c"}; !equalStrings(started, want) {
		t.Fatalf("post-crash starts = %v, want %v", started, want)
	}
	bOutcome, err := evidence.LatestExtractionDomainOutcome(
		context.Background(), bScope,
	)
	if err != nil || bOutcome.RunID != "crashed-b" {
		t.Fatalf("deferred crashed attempt = %+v, %v", bOutcome, err)
	}

	for _, domain := range []string{"a", "b", "c"} {
		scope := store.ExtractionScope{
			Repository: "host/scheduler", Commit: unitCommit, Domain: domain,
		}
		outcome, err := evidence.LatestExtractionDomainOutcome(
			context.Background(), scope,
		)
		if err != nil || outcome.RunID == "" ||
			outcome.Disposition != store.DomainOutcomeRetryableFailure {
			t.Fatalf("%s retry outcome = %+v, %v", domain, outcome, err)
		}
		if attempt, err := evidence.LatestExtractionAttempt(
			context.Background(), scope,
		); err != nil || attempt.RunID != outcome.RunID {
			t.Fatalf("%s attempt = %+v, %v; outcome %+v",
				domain, attempt, err, outcome)
		}
	}
}

func TestSchedulerSlowDomainCapAllowsLaterPeer(t *testing.T) {
	evidence := newMemoryEvidence()
	var mu sync.Mutex
	started := make([]string, 0, 2)
	blocked := unitExtractor{
		domain: "slow", version: "v1",
		extract: func(
			ctx context.Context, _ sdk.Corpus, _ sdk.Emit,
		) (sdk.Coverage, error) {
			mu.Lock()
			started = append(started, "slow")
			mu.Unlock()
			<-ctx.Done()
			return sdk.Coverage{}, ctx.Err()
		},
	}
	fast := unitExtractor{
		domain: "later", version: "v1",
		extract: func(
			context.Context, sdk.Corpus, sdk.Emit,
		) (sdk.Coverage, error) {
			mu.Lock()
			started = append(started, "later")
			mu.Unlock()
			return sdk.Coverage{}, nil
		},
	}
	limits := testDomainSchedulingLimits()
	limits.AggregateTimeout = 300 * time.Millisecond
	limits.MirrorTimeout = 250 * time.Millisecond
	limits.DomainTimeout = 40 * time.Millisecond
	limits.AbortTimeout = 10 * time.Millisecond
	limits.OutcomeTimeout = 10 * time.Millisecond
	startedAt := time.Now()
	worker := schedulerTestWorker(evidence, []Extractor{blocked, fast}, limits)
	err := worker.Handle(
		context.Background(), store.Job{Target: "host/scheduler"},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow Handle = %v, want deadline", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("per-domain cap took %v", elapsed)
	}
	mu.Lock()
	got := append([]string(nil), started...)
	mu.Unlock()
	if want := []string{"slow", "later"}; !equalStrings(got, want) {
		t.Fatalf("starts = %v, want %v", got, want)
	}
	outcome, err := evidence.LatestExtractionDomainOutcome(
		context.Background(),
		store.ExtractionScope{
			Repository: "host/scheduler", Commit: unitCommit, Domain: "slow",
		},
	)
	if err != nil || outcome.Disposition != store.DomainOutcomeRetryableFailure ||
		outcome.RunID == "" {
		t.Fatalf("slow outcome = %+v, %v", outcome, err)
	}
}

func TestSchedulerAggregateAndMirrorCapsDoNotMultiply(t *testing.T) {
	evidence := newMemoryEvidence()
	var lockStarted, lockReleased time.Time
	factory := unitCorpusFactory{lock: func(
		context.Context, string,
	) (func(), error) {
		lockStarted = time.Now()
		return func() { lockReleased = time.Now() }, nil
	}}
	started := 0
	extractors := make([]Extractor, 0, 3)
	for _, domain := range []string{"a", "b", "c"} {
		domain := domain
		extractors = append(extractors, unitExtractor{
			domain: domain, version: "v1",
			extract: func(
				ctx context.Context, _ sdk.Corpus, _ sdk.Emit,
			) (sdk.Coverage, error) {
				started++
				<-ctx.Done()
				return sdk.Coverage{}, ctx.Err()
			},
		})
	}
	limits := testDomainSchedulingLimits()
	limits.AggregateTimeout = 300 * time.Millisecond
	limits.MirrorTimeout = 180 * time.Millisecond
	limits.DomainTimeout = 110 * time.Millisecond
	limits.AbortTimeout = 10 * time.Millisecond
	limits.OutcomeTimeout = 10 * time.Millisecond
	limits.MinimumStartBudget = 5 * time.Millisecond
	worker := schedulerTestWorker(evidence, extractors, limits)
	worker.NewCorpus = factory

	wallStarted := time.Now()
	if err := worker.Handle(
		context.Background(), store.Job{Target: "host/scheduler"},
	); err == nil {
		t.Fatal("aggregate-limited job unexpectedly completed")
	}
	wall := time.Since(wallStarted)
	lockHold := lockReleased.Sub(lockStarted)
	if started != 2 {
		t.Fatalf("started domains = %d, want 2", started)
	}
	if lockStarted.IsZero() || lockReleased.IsZero() ||
		lockHold > 500*time.Millisecond {
		t.Fatalf("mirror lock hold = %v (%v to %v)",
			lockHold, lockStarted, lockReleased)
	}
	if wall > 700*time.Millisecond {
		t.Fatalf("aggregate wall = %v", wall)
	}
	outcome, err := evidence.LatestExtractionDomainOutcome(
		context.Background(),
		store.ExtractionScope{
			Repository: "host/scheduler", Commit: unitCommit, Domain: "c",
		},
	)
	if err != nil || outcome.Disposition != store.DomainOutcomeRetryableFailure ||
		outcome.RunID != "" {
		t.Fatalf("deferred outcome = %+v, %v", outcome, err)
	}
}

func TestSchedulerStagedRowCapDefersThenRetries(t *testing.T) {
	evidence := newMemoryEvidence()
	started := make([]string, 0, 3)
	extractors := make([]Extractor, 0, 3)
	for _, domain := range []string{"a", "b", "c"} {
		domain := domain
		extractors = append(extractors, unitExtractor{
			domain: domain, version: "v1",
			candidate: func(path string) bool {
				return path == "a.proto"
			},
			extract: func(
				ctx context.Context, corpus sdk.Corpus, emit sdk.Emit,
			) (sdk.Coverage, error) {
				started = append(started, domain)
				return sdk.Coverage{}, emitUnit(
					ctx, corpus, emit, unitFact("a.proto", domain),
				)
			},
		})
	}
	limits := testDomainSchedulingLimits()
	limits.MaxStagedRows = 4
	limits.MaxDomainStagedRows = 2
	worker := schedulerTestWorker(evidence, extractors, limits)

	if err := worker.Handle(
		context.Background(), store.Job{Target: "host/scheduler"},
	); !errors.Is(err, errExtractionAggregateBudget) {
		t.Fatalf("first staged-row job = %v", err)
	}
	if want := []string{"a", "b"}; !equalStrings(started, want) {
		t.Fatalf("first starts = %v, want %v", started, want)
	}
	if err := worker.Handle(
		context.Background(), store.Job{Target: "host/scheduler"},
	); err != nil {
		t.Fatalf("deferred retry = %v", err)
	}
	if want := []string{"a", "b", "c"}; !equalStrings(started, want) {
		t.Fatalf("retry starts = %v, want %v", started, want)
	}
}

func TestSchedulerDomainStageCapAbortsWithoutPartialRows(t *testing.T) {
	evidence := newMemoryEvidence()
	extractor := unitExtractor{
		domain: "bounded-stage", version: "v1",
		candidate: func(path string) bool {
			return path == "a.proto"
		},
		extract: func(
			ctx context.Context, corpus sdk.Corpus, emit sdk.Emit,
		) (sdk.Coverage, error) {
			if err := emitUnit(
				ctx, corpus, emit, unitFact("a.proto", "one"),
			); err != nil {
				return sdk.Coverage{}, err
			}
			return sdk.Coverage{}, emit(
				unitFact("a.proto", "two"),
			)
		},
	}
	limits := testDomainSchedulingLimits()
	limits.MaxStagedRows = 2
	limits.MaxDomainStagedRows = 2
	worker := schedulerTestWorker(evidence, []Extractor{extractor}, limits)

	if err := worker.Handle(
		context.Background(), store.Job{Target: "host/scheduler"},
	); !errors.Is(err, errExtractionDomainBudget) {
		t.Fatalf("stage-cap error = %v", err)
	}
	if !evidence.aborted || evidence.batchCount != 0 ||
		evidence.stagedFacts != 0 {
		t.Fatalf("stage cleanup: aborted=%v batches=%d facts=%d",
			evidence.aborted, evidence.batchCount, evidence.stagedFacts)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
