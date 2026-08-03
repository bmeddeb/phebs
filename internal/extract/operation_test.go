package extract

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	candidatepkg "github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/store"
)

type operationClock struct {
	mu   sync.Mutex
	next time.Time
	step time.Duration
}

func TestCandidatePointerDiagnosticResult(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "current", want: "current"},
		{name: "missing", err: store.ErrNotFound, want: "missing"},
		{name: "publication in progress", err: candidatepkg.ErrPublishing, want: "stale"},
		{name: "generation mismatch", err: ErrCandidateManifestStale, want: "stale"},
		{name: "integrity error", err: errors.New("invalid pointer"), want: "error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := candidatePointerDiagnosticResult(test.err); got != test.want {
				t.Fatalf("result = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSchedulerUnavailableTypedInputKeepsExplicitFalse(t *testing.T) {
	report := schedulerDiagnostic{Domains: []schedulerDomainDiagnostic{{
		Domain: "grpc-caller", State: "unavailable_prerequisite",
		TypedInputPresent: diagnosticBool(true, false),
	}}}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"typed_input_present":false`) {
		t.Fatalf("scheduler diagnostic omitted false posture: %s", raw)
	}
}

func (clock *operationClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	current := clock.next
	clock.next = clock.next.Add(clock.step)
	return current
}

type operationCorpusCounters struct {
	mu    sync.Mutex
	locks int
	walks int
	reads int
}

type operationCorpusFactory struct {
	counters *operationCorpusCounters
}

func (factory operationCorpusFactory) Lock(
	context.Context,
	string,
) (func(), error) {
	factory.counters.mu.Lock()
	factory.counters.locks++
	factory.counters.mu.Unlock()
	return func() {}, nil
}

func (factory operationCorpusFactory) New(
	repository, commit string,
) sdk.Corpus {
	return operationCorpus{
		repository: repository, commit: commit, counters: factory.counters,
	}
}

type operationCorpus struct {
	repository string
	commit     string
	counters   *operationCorpusCounters
}

func (corpus operationCorpus) RepoName() string { return corpus.repository }
func (corpus operationCorpus) Commit() string   { return corpus.commit }
func (corpus operationCorpus) WalkFiles(
	ctx context.Context,
	visit func(string) error,
) error {
	corpus.counters.mu.Lock()
	corpus.counters.walks++
	corpus.counters.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return visit("same.proto")
}

func (corpus operationCorpus) Read(
	context.Context,
	string,
) (sdk.Blob, error) {
	corpus.counters.mu.Lock()
	corpus.counters.reads++
	corpus.counters.mu.Unlock()
	content := "same blob"
	digest := sha256.Sum256([]byte(content))
	return sdk.Blob{
		Content: content,
		Digest:  "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func captureOperationReport(
	t *testing.T,
) (ExtractionOperationSink, func() (ExtractionOperationReport, []byte)) {
	t.Helper()
	var mu sync.Mutex
	var reports [][]byte
	sink := func(input []byte) error {
		mu.Lock()
		reports = append(reports, bytes.Clone(input))
		mu.Unlock()
		return nil
	}
	read := func() (ExtractionOperationReport, []byte) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if len(reports) != 1 {
			t.Fatalf("operation report count = %d, want 1", len(reports))
		}
		var report ExtractionOperationReport
		if err := json.Unmarshal(reports[0], &report); err != nil {
			t.Fatalf("decode operation report: %v", err)
		}
		return report, bytes.Clone(reports[0])
	}
	return sink, read
}

func TestExtractionOperationPublishedAccountingAndFakeClock(t *testing.T) {
	run := func() (ExtractionOperationReport, []byte, *operationCorpusCounters) {
		t.Helper()
		base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
		clock := &operationClock{next: base, step: 10 * time.Millisecond}
		counters := &operationCorpusCounters{}
		evidence := newMemoryEvidence()
		repository := &store.Repo{
			Name: "example.invalid/mono", IndexedCommitHash: unitCommit,
		}
		extractor := unitExtractor{
			domain: "proto-contract", version: "v1",
			candidate: func(filePath string) bool {
				return filePath == "same.proto"
			},
			extract: func(
				ctx context.Context,
				corpus sdk.Corpus,
				emit sdk.Emit,
			) (sdk.Coverage, error) {
				if !sdk.DiagnosticCountersEnabled(ctx) {
					return sdk.Coverage{}, errors.New("diagnostic counter request missing")
				}
				if err := emitUnit(
					ctx, corpus, emit, unitFact("same.proto", "service"),
				); err != nil {
					return sdk.Coverage{}, err
				}
				return sdk.Coverage{
					Protocols: []string{"protobuf"},
					Diagnostics: []sdk.DiagnosticCounter{{
						Name: "generated_stubs_recognized", Value: 1,
					}},
				}, nil
			},
		}
		sink, read := captureOperationReport(t)
		createdAt := base.Add(-2 * time.Second)
		claimedAt := base.Add(-time.Second)
		worker := Worker{
			Repos: readyRepoGetter(repository), Evidence: evidence,
			NewCorpus:  operationCorpusFactory{counters: counters},
			Extractors: []Extractor{extractor},
			Now:        clock.Now, OperationReports: sink, ExtractorDetails: true,
		}
		err := worker.Handle(context.Background(), store.Job{
			ID: "extraction_job:operation", Kind: store.JobExtract,
			Target: repository.Name, CreatedAt: createdAt,
			ClaimedAt: &claimedAt, Attempts: 2,
		})
		if err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if !evidence.published {
			t.Fatal("operation reporting changed successful publication")
		}
		report, raw := read()
		return report, raw, counters
	}

	first, firstRaw, counters := run()
	_, secondRaw, _ := run()
	if !bytes.Equal(firstRaw, secondRaw) {
		t.Fatalf("fake-clock reports differ:\n%s\n%s", firstRaw, secondRaw)
	}
	if first.Schema != ExtractionOperationSchema ||
		first.Repository != "example.invalid/mono" ||
		first.IndexedHead != unitCommit ||
		first.Attempt.JobID != "extraction_job:operation" ||
		first.Attempt.Number != 3 ||
		first.QueueWaitMS != 1000 ||
		first.PointerWorkMS != 10 ||
		first.MirrorLockWaitMS != 10 ||
		first.StrictOpenMS != 0 ||
		first.TotalMS <= 0 {
		t.Fatalf("job envelope = %+v", first)
	}
	if len(first.Domains) != 1 {
		t.Fatalf("domain count = %d, want 1", len(first.Domains))
	}
	domain := first.Domains[0]
	if domain.Domain != "proto-contract" ||
		domain.ExtractorVersion != "v1" ||
		domain.Reason != OperationReasonPublishedNonempty {
		t.Fatalf("domain identity/reason = %+v", domain)
	}
	if len(domain.ExtractorDiagnostics) != 1 ||
		domain.ExtractorDiagnostics[0].Name != "generated_stubs_recognized" ||
		domain.ExtractorDiagnostics[0].Value != 1 {
		t.Fatalf("extractor diagnostics = %+v", domain.ExtractorDiagnostics)
	}
	if domain.InventoryMS <= 0 || domain.OpenedSourceMS <= 0 ||
		domain.ExtractorMS <= 0 || domain.StagingMS <= 0 ||
		domain.PublicationMS <= 0 || domain.CleanupMS <= 0 ||
		domain.AbortMS != 0 {
		t.Fatalf("domain timings = %+v", domain)
	}
	if domain.Counts != (ExtractionOperationDomainCounts{
		CorpusFiles: 1, CandidateFiles: 1, OpenedSourceAttempts: 1,
		OpenedSourceFiles: 1, Facts: 1, Atoms: 1, Assertions: 1,
		StagedChunks: 1, StagedRows: 2,
	}) {
		t.Fatalf("domain counts = %+v", domain.Counts)
	}
	if domain.Bytes.OpenedSource != int64(len("same blob")) ||
		domain.Limits.CorpusFiles != maxCorpusFiles ||
		domain.Limits.OpenedSourceAttempts != maxCorpusFiles*4 ||
		domain.Limits.Facts != maxFactsPerRun ||
		domain.Limits.OpenedSourceBytes != maxCorpusRunBytes ||
		domain.Limits.AggregateWallMS != 15*60*1000 ||
		domain.Limits.MirrorLockMS != 14*60*1000+50*1000 ||
		domain.Limits.DomainWallMS != 5*60*1000 ||
		domain.Limits.MaxSerialDomains != 16 ||
		domain.Limits.SchedulerIdentityBytes != 64<<10 ||
		domain.Limits.AggregateStagedRows != 100_000 ||
		domain.Limits.DomainStagedRows != 25_000 {
		t.Fatalf("domain bytes/limits = %+v / %+v", domain.Bytes, domain.Limits)
	}
	counters.mu.Lock()
	gotLocks, gotWalks, gotReads := counters.locks, counters.walks, counters.reads
	counters.mu.Unlock()
	if gotLocks != 1 || gotWalks != 1 || gotReads != 1 {
		t.Fatalf(
			"instrumented opens/reads = locks:%d walks:%d reads:%d, want 1/1/1",
			gotLocks, gotWalks, gotReads,
		)
	}
	for _, forbidden := range []string{"same.proto", "same blob", "service"} {
		if bytes.Contains(firstRaw, []byte(forbidden)) {
			t.Fatalf("operation report exposed source material %q: %s", forbidden, firstRaw)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(firstRaw, &decoded); err != nil {
		t.Fatal(err)
	}
	domains := decoded["domains"].([]any)
	domainObject := domains[0].(map[string]any)
	for _, jobOnly := range []string{
		"queue_wait_ms", "mirror_lock_wait_ms", "pointer_work_ms",
		"strict_open_ms",
	} {
		if _, duplicated := domainObject[jobOnly]; duplicated {
			t.Fatalf("domain duplicated job work %q", jobOnly)
		}
	}
}

func TestExtractionOperationQueueWaitStartsAtAttemptEligibility(t *testing.T) {
	claimedAt := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	notBefore := claimedAt.Add(-time.Minute)
	operation := newOperationRecorder(store.Job{
		Target:    "example.invalid/retry",
		CreatedAt: claimedAt.Add(-16 * time.Minute),
		NotBefore: &notBefore,
		ClaimedAt: &claimedAt,
	}, func() time.Time {
		return claimedAt
	})
	if operation.report.QueueWaitMS != int64(time.Minute/time.Millisecond) {
		t.Fatalf(
			"retry queue wait = %dms, want %dms",
			operation.report.QueueWaitMS,
			time.Minute/time.Millisecond,
		)
	}
}

func TestExtractionOperationDurationBucketsCoverExtractionBudget(t *testing.T) {
	if len(extractionOperationDurationBuckets) == 0 {
		t.Fatal("extraction operation duration has no finite buckets")
	}
	last := extractionOperationDurationBuckets[len(extractionOperationDurationBuckets)-1]
	if last < extractionTimeout.Seconds() {
		t.Fatalf(
			"last extraction duration bucket = %.1fs, want at least %.1fs",
			last, extractionTimeout.Seconds(),
		)
	}
}

func TestExtractionOperationReportsFocusedExclusions(t *testing.T) {
	domain := &domainOperationRecorder{
		now: time.Now,
		report: ExtractionOperationDomain{
			Domain:           "scip-proto-field",
			ExtractorVersion: "1.4.0",
			Reason:           OperationReasonPublishedEmpty,
		},
	}
	corpus := &verifiedCorpus{
		corpusFileCount: 12,
		candidates:      map[string]struct{}{"consumer/use.go": {}},
		domainScope: CandidateManifestScope{
			CandidateDeclaredBytes: 4096,
		},
		excludedFiles: 3,
		excludedBytes: 2048,
	}
	coverage := sdk.Coverage{
		ExcludedSCIPDocuments:   2,
		ExcludedSCIPDefinitions: 4,
		ExcludedSCIPOccurrences: 9,
	}
	domain.capture(corpus, nil)
	domain.captureCoverage(coverage, false)

	report := domain.snapshot()
	if report.Counts.ExcludedSourceFiles != 3 ||
		report.Bytes.ExcludedSourceDeclared != 2048 ||
		report.Counts.ExcludedSCIPDocuments != 2 ||
		report.Counts.ExcludedSCIPDefinitions != 4 ||
		report.Counts.ExcludedSCIPOccurrences != 9 {
		t.Fatalf("operation exclusions = %+v / %+v", report.Counts, report.Bytes)
	}
	var receipt ExtractionDomainOutcomeReceipt
	raw := encodeExtractionDomainOutcomeReceipt(
		domain, store.DomainOutcomePublished,
	)
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Counts != report.Counts || receipt.Bytes != report.Bytes {
		t.Fatalf(
			"durable receipt exclusions = %+v / %+v, want %+v / %+v",
			receipt.Counts, receipt.Bytes, report.Counts, report.Bytes,
		)
	}
}

type operationCurrentProvider struct {
	identity      string
	policyDigest  string
	identityCalls int
	openCalls     int
}

func (provider *operationCurrentProvider) PolicyDigest() string {
	return provider.policyDigest
}

func (provider *operationCurrentProvider) CandidateManifestIdentity(
	context.Context,
	CandidateManifestRequest,
) (string, error) {
	provider.identityCalls++
	return provider.identity, nil
}

func (provider *operationCurrentProvider) OpenCandidateManifest(
	context.Context,
	CandidateManifestRequest,
) (CandidateManifest, error) {
	provider.openCalls++
	return nil, errors.New("unexpected strict open")
}

func TestExtractionOperationAlreadyCurrentIsPointerOnly(t *testing.T) {
	repository := &store.Repo{
		Name: "example.invalid/current", IndexedCommitHash: unitCommit,
	}
	extractor := unitExtractor{
		domain: "proto-contract", version: "v1",
		candidate: func(string) bool { return true },
		extract: func(
			context.Context, sdk.Corpus, sdk.Emit,
		) (sdk.Coverage, error) {
			t.Fatal("already-current job ran extractor")
			return sdk.Coverage{}, nil
		},
	}
	identity := "sha256:" + strings.Repeat("a", 64)
	policy, err := candidateManifestInventoryPolicy(identity)
	if err != nil {
		t.Fatal(err)
	}
	evidence := newMemoryEvidence()
	scope := store.ExtractionScope{
		Repository: repository.Name, Commit: unitCommit,
		Domain: extractor.domain,
	}
	evidence.latestByScope[memoryScopeKey(scope)] = &store.ExtractionRun{
		ID: "run-current", Repo: repository.Name, Commit: unitCommit,
		Domain: extractor.domain, Extractor: extractor.version,
		Coverage: store.CoverageManifest{
			InventoryPolicy: policy, CandidateManifestDigest: identity,
		},
	}
	provider := &operationCurrentProvider{
		identity:     identity,
		policyDigest: "sha256:" + strings.Repeat("b", 64),
	}
	counters := operationCorpusCounters{}
	sink, read := captureOperationReport(t)
	clock := &operationClock{
		next: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
		step: 5 * time.Millisecond,
	}
	worker := Worker{
		Repos: readyRepoGetter(repository), Evidence: evidence,
		NewCorpus: operationCorpusFactory{counters: &counters},
		Manifests: provider, Extractors: []Extractor{extractor},
		Now: clock.Now, OperationReports: sink,
	}
	if err := worker.Handle(
		context.Background(),
		store.Job{ID: "extraction_job:current", Target: repository.Name},
	); err != nil {
		t.Fatal(err)
	}
	report, _ := read()
	if provider.identityCalls != 1 || provider.openCalls != 0 {
		t.Fatalf(
			"candidate work = identity:%d open:%d, want 1/0",
			provider.identityCalls, provider.openCalls,
		)
	}
	counters.mu.Lock()
	gotLocks, gotWalks, gotReads := counters.locks, counters.walks, counters.reads
	counters.mu.Unlock()
	if gotLocks != 0 || gotWalks != 0 || gotReads != 0 {
		t.Fatalf(
			"no-op corpus work = locks:%d walks:%d reads:%d",
			gotLocks, gotWalks, gotReads,
		)
	}
	if report.CandidateManifestDigest != identity ||
		report.PolicyDigest != provider.policyDigest ||
		report.PointerWorkMS != 5 ||
		report.MirrorLockWaitMS != 0 ||
		len(report.Domains) != 1 ||
		report.Domains[0].Reason != OperationReasonAlreadyCurrent {
		t.Fatalf("already-current report = %+v", report)
	}
}

func TestExtractionOperationNilManifestIsClassifiedWithoutPanic(t *testing.T) {
	repository := &store.Repo{
		Name: "example.invalid/nil-manifest", IndexedCommitHash: unitCommit,
	}
	extractor := unitExtractor{
		domain: "proto-contract", version: "v1",
		candidate: func(string) bool { return true },
		extract: func(
			context.Context, sdk.Corpus, sdk.Emit,
		) (sdk.Coverage, error) {
			t.Fatal("nil manifest reached extractor")
			return sdk.Coverage{}, nil
		},
	}
	sink, read := captureOperationReport(t)
	worker := Worker{
		Repos: readyRepoGetter(repository), Evidence: newMemoryEvidence(),
		NewCorpus: unitFactory(nil),
		Manifests: manifestProviderFunc(func(
			context.Context,
			CandidateManifestRequest,
		) (CandidateManifest, error) {
			return nil, nil
		}),
		Extractors:       []Extractor{extractor},
		OperationReports: sink,
	}
	err := worker.Handle(
		context.Background(),
		store.Job{Target: repository.Name},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "candidate manifest provider returned nil") {
		t.Fatalf("Handle error = %v", err)
	}
	report, _ := read()
	if len(report.Domains) != 1 ||
		report.Domains[0].Reason != OperationReasonFailed {
		t.Fatalf("nil-manifest report = %+v", report)
	}
}

func TestExtractionOperationNeverAttemptedDomainsFollowCancellation(
	t *testing.T,
) {
	repository := &store.Repo{
		Name:              "example.invalid/cancel-after-limit",
		IndexedCommitHash: unitCommit,
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	extractors := []Extractor{
		unitExtractor{
			domain: "limit", version: "v1",
			candidate: func(string) bool { return false },
			extract: func(
				context.Context, sdk.Corpus, sdk.Emit,
			) (sdk.Coverage, error) {
				cancel()
				return sdk.Coverage{}, operationLimitError("read-byte cap")
			},
		},
	}
	for _, domain := range []string{"canceled", "never-c", "never-d"} {
		domain := domain
		extractors = append(extractors, unitExtractor{
			domain: domain, version: "v1",
			candidate: func(string) bool { return false },
			extract: func(
				context.Context, sdk.Corpus, sdk.Emit,
			) (sdk.Coverage, error) {
				t.Fatalf("domain %q unexpectedly executed", domain)
				return sdk.Coverage{}, nil
			},
		})
	}
	sink, read := captureOperationReport(t)
	worker := Worker{
		Repos: readyRepoGetter(repository), Evidence: newMemoryEvidence(),
		NewCorpus: unitFactory(nil), Extractors: extractors,
		OperationReports: sink,
	}
	err := worker.Handle(ctx, store.Job{Target: repository.Name})
	if !errors.Is(err, context.Canceled) ||
		errors.Is(err, errOperationLimitRefusal) {
		t.Fatalf("Handle error = %v, want only cancellation; the durable limit outcome is settled", err)
	}
	report, _ := read()
	if len(report.Domains) != 4 {
		t.Fatalf("domain count = %d, want 4", len(report.Domains))
	}
	wantReasons := []string{
		OperationReasonLimitRefusal,
		OperationReasonCanceled,
		OperationReasonCanceled,
		OperationReasonCanceled,
	}
	for index, domain := range report.Domains {
		if domain.Reason != wantReasons[index] {
			t.Errorf(
				"domain %q reason = %q, want %q",
				domain.Domain, domain.Reason, wantReasons[index],
			)
		}
		if index > 0 && domain.Counts != (ExtractionOperationDomainCounts{}) {
			t.Errorf("unattempted domain %q has counts %+v", domain.Domain, domain.Counts)
		}
	}
}

func TestExtractionOperationCancellationAndSinkFailureDoNotChangeDisposition(
	t *testing.T,
) {
	t.Run("cancellation", func(t *testing.T) {
		repository := &store.Repo{
			Name: "example.invalid/canceled", IndexedCommitHash: unitCommit,
		}
		extractor := unitExtractor{
			domain: "proto-contract", version: "v1",
			extract: func(
				context.Context, sdk.Corpus, sdk.Emit,
			) (sdk.Coverage, error) {
				return sdk.Coverage{}, nil
			},
		}
		sink, read := captureOperationReport(t)
		worker := Worker{
			Repos: readyRepoGetter(repository), Evidence: newMemoryEvidence(),
			NewCorpus: unitFactory(func(
				ctx context.Context,
				_ string,
			) (func(), error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}),
			Extractors: []Extractor{extractor}, OperationReports: sink,
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := worker.Handle(ctx, store.Job{Target: repository.Name})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle = %v, want context canceled", err)
		}
		report, _ := read()
		if len(report.Domains) != 1 ||
			report.Domains[0].Reason != OperationReasonCanceled {
			t.Fatalf("canceled report = %+v", report)
		}
	})

	t.Run("sink failure", func(t *testing.T) {
		repository := &store.Repo{
			Name: "example.invalid/sink", IndexedCommitHash: unitCommit,
		}
		evidence := newMemoryEvidence()
		extractor := unitExtractor{
			domain: "proto-contract", version: "v1",
			candidate: func(string) bool { return false },
			extract: func(
				context.Context, sdk.Corpus, sdk.Emit,
			) (sdk.Coverage, error) {
				return sdk.Coverage{Protocols: []string{"protobuf"}}, nil
			},
		}
		worker := Worker{
			Repos: readyRepoGetter(repository), Evidence: evidence,
			NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor},
			OperationReports: func([]byte) error {
				return errors.New("report sink unavailable")
			},
		}
		if err := worker.Handle(
			context.Background(),
			store.Job{Target: repository.Name},
		); err != nil {
			t.Fatalf("Handle changed by report sink: %v", err)
		}
		if !evidence.published {
			t.Fatal("report sink failure prevented publication")
		}
	})

	t.Run("sink panic", func(t *testing.T) {
		repository := &store.Repo{
			Name: "example.invalid/sink-panic", IndexedCommitHash: unitCommit,
		}
		evidence := newMemoryEvidence()
		extractor := unitExtractor{
			domain: "proto-contract", version: "v1",
			candidate: func(string) bool { return false },
			extract: func(
				context.Context, sdk.Corpus, sdk.Emit,
			) (sdk.Coverage, error) {
				return sdk.Coverage{Protocols: []string{"protobuf"}}, nil
			},
		}
		worker := Worker{
			Repos: readyRepoGetter(repository), Evidence: evidence,
			NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor},
			OperationReports: func([]byte) error {
				panic("report sink panic")
			},
		}
		if err := worker.Handle(
			context.Background(),
			store.Job{Target: repository.Name},
		); err != nil {
			t.Fatalf("Handle changed by report sink panic: %v", err)
		}
		if !evidence.published {
			t.Fatal("report sink panic prevented publication")
		}
	})
}

func TestExtractionOperationGenericReasonsAndDiagnosticRedaction(t *testing.T) {
	for _, reason := range []string{
		OperationReasonAlreadyCurrent, OperationReasonNotReady,
		OperationReasonStale, OperationReasonNoCandidates,
		OperationReasonTypedInputAbsent, OperationReasonLimitRefusal,
		OperationReasonPublishedEmpty, OperationReasonPublishedNonempty,
		OperationReasonAggregateBudget, OperationReasonDomainBudget,
		OperationReasonCanceled, OperationReasonFailed,
	} {
		if !validOperationReason(reason) {
			t.Errorf("reason %q is not frozen", reason)
		}
	}
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"not ready", candidatepkg.ErrPublishing, OperationReasonNotReady},
		{"stale", errStaleRun, OperationReasonStale},
		{"limit", operationLimitError("cap exceeded"), OperationReasonLimitRefusal},
		{"aggregate budget", errExtractionAggregateBudget, OperationReasonAggregateBudget},
		{"domain budget", errExtractionDomainBudget, OperationReasonDomainBudget},
		{"git object limit", gitobj.ErrTooLarge, OperationReasonLimitRefusal},
		{"semantic limit", codenav.ErrSemanticLimit, OperationReasonLimitRefusal},
		{"hover limit", codenav.ErrHoverTooLarge, OperationReasonLimitRefusal},
		{"candidate corpus limit", candidatepkg.ErrCorpusTooLarge, OperationReasonLimitRefusal},
		{"candidate byte limit", candidatepkg.ErrCandidateTooLarge, OperationReasonLimitRefusal},
		{"compat limit", compat.ErrLimit, OperationReasonLimitRefusal},
		{
			"integrity prose is not a limit",
			errors.New("candidate manifest gitlink sample exceeds its bounds"),
			OperationReasonFailed,
		},
		{"canceled", context.Canceled, OperationReasonCanceled},
		{"failed", errors.New("raw diagnostic"), OperationReasonFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := operationReason(test.err); got != test.want {
				t.Fatalf("reason = %q, want %q", got, test.want)
			}
		})
	}
	limitErr := operationLimitError("run exceeds fact limit")
	if !errors.Is(limitErr, errOperationLimitRefusal) ||
		errors.Unwrap(limitErr) != errOperationLimitRefusal ||
		strings.Contains(limitErr.Error(), "\n") {
		t.Fatalf("limit error wrapping = %q / %v", limitErr, errors.Unwrap(limitErr))
	}
	for _, test := range []struct {
		name  string
		stats corpusStats
		facts int
		want  string
	}{
		{
			name: "no candidates",
			want: OperationReasonNoCandidates,
		},
		{
			name: "typed input absent",
			stats: corpusStats{
				typedInputKind: "scip", candidateFileCount: 1,
			},
			want: OperationReasonTypedInputAbsent,
		},
		{
			name: "published empty",
			stats: corpusStats{
				candidateFileCount: 1,
			},
			want: OperationReasonPublishedEmpty,
		},
		{
			name:  "published nonempty",
			facts: 1,
			want:  OperationReasonPublishedNonempty,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := successfulOperationReason(test.stats, test.facts); got != test.want {
				t.Fatalf("reason = %q, want %q", got, test.want)
			}
		})
	}

	repository := &store.Repo{
		Name: "example.invalid/failure", IndexedCommitHash: unitCommit,
	}
	evidence := newMemoryEvidence()
	extractor := unitExtractor{
		domain: "proto-contract", version: "v1",
		candidate: func(filePath string) bool {
			return filePath == "same.proto"
		},
		extract: func(
			context.Context, sdk.Corpus, sdk.Emit,
		) (sdk.Coverage, error) {
			return sdk.Coverage{}, errors.New(
				"secret extractor diagnostic at private/source.proto",
			)
		},
	}
	sink, read := captureOperationReport(t)
	worker := Worker{
		Repos: readyRepoGetter(repository), Evidence: evidence,
		NewCorpus:  operationCorpusFactory{counters: &operationCorpusCounters{}},
		Extractors: []Extractor{extractor}, OperationReports: sink,
	}
	err := worker.Handle(context.Background(), store.Job{Target: repository.Name})
	if err == nil {
		t.Fatal("failing extractor unexpectedly succeeded")
	}
	report, raw := read()
	if len(report.Domains) != 1 ||
		report.Domains[0].Reason != OperationReasonFailed ||
		report.Domains[0].AbortMS < 0 {
		t.Fatalf("failure report = %+v", report)
	}
	for _, forbidden := range []string{"secret extractor", "private/source.proto"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("report leaked raw diagnostic %q: %s", forbidden, raw)
		}
	}
}

func TestExtractorDiagnosticCountersAreBoundedAndSourceFree(t *testing.T) {
	domain := &domainOperationRecorder{report: ExtractionOperationDomain{}}
	domain.captureCoverage(sdk.Coverage{Diagnostics: []sdk.DiagnosticCounter{
		{Name: "generated_stubs_recognized", Value: 2},
		{Name: "call_sites_found", Value: 0},
	}}, true)
	report := domain.snapshot()
	if len(report.ExtractorDiagnostics) != 2 ||
		report.ExtractorDiagnostics[0].Name != "generated_stubs_recognized" {
		t.Fatalf("diagnostics = %+v", report.ExtractorDiagnostics)
	}

	domain.captureCoverage(sdk.Coverage{Diagnostics: []sdk.DiagnosticCounter{
		{Name: "private/source.go", Value: 1},
	}}, true)
	if got := domain.snapshot().ExtractorDiagnostics; len(got) != 0 {
		t.Fatalf("unsafe diagnostics retained: %+v", got)
	}
}

func TestExtractionOperationCapAndMinimalOverflow(t *testing.T) {
	report := ExtractionOperationReport{
		Schema: ExtractionOperationSchema, Repository: "example.invalid/repo",
		Attempt: ExtractionOperationAttempt{
			JobID: "extraction_job:cap", Number: 1,
		},
		Domains: []ExtractionOperationDomain{{
			ExtractorVersion: "v1", Reason: OperationReasonFailed,
		}},
	}
	base, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	padding := MaxExtractionOperationSize - len(base)
	if padding <= 0 {
		t.Fatalf("base report size = %d", len(base))
	}
	report.Domains[0].Domain = strings.Repeat("x", padding)
	atCap, err := encodeExtractionOperation(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(atCap) != MaxExtractionOperationSize {
		t.Fatalf("at-cap size = %d, want %d", len(atCap), MaxExtractionOperationSize)
	}
	var full ExtractionOperationReport
	if err := json.Unmarshal(atCap, &full); err != nil {
		t.Fatal(err)
	}
	if full.Truncated || len(full.Domains) != 1 {
		t.Fatalf("at-cap report was truncated: %+v", full)
	}

	report.Domains[0].Domain += "x"
	overflow, err := encodeExtractionOperation(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(overflow) > MaxExtractionOperationSize {
		t.Fatalf("overflow size = %d", len(overflow))
	}
	var minimal extractionOperationMinimal
	if err := json.Unmarshal(overflow, &minimal); err != nil {
		t.Fatal(err)
	}
	if !minimal.Truncated || minimal.DomainCount != 1 ||
		minimal.Repository != report.Repository ||
		minimal.Attempt != report.Attempt {
		t.Fatalf("minimal overflow = %+v", minimal)
	}
	again, err := encodeExtractionOperation(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(overflow, again) {
		t.Fatal("minimal overflow encoding is not deterministic")
	}
	if bytes.Contains(overflow, []byte(`"domains"`)) {
		t.Fatalf("minimal overflow retained domains: %s", overflow)
	}
}

func TestClassifyDomainOutcomeUsesTypedMarkers(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		disposition store.DomainOutcomeDisposition
		control     bool
	}{
		{
			name:        "untyped integrity lookalike stays retryable",
			err:         errors.New(candidatepkg.ErrInvalidManifest.Error()),
			disposition: store.DomainOutcomeRetryableFailure,
		},
		{
			name: "typed candidate integrity is terminal control",
			err: errors.Join(
				errors.New("strict open"),
				candidatepkg.ErrInvalidManifest,
			),
			disposition: store.DomainOutcomeTerminalGenerationRefusal,
			control:     true,
		},
		{
			name:        "untyped limit prose stays retryable",
			err:         errors.New("run exceeds 12500-fact limit"),
			disposition: store.DomainOutcomeRetryableFailure,
		},
		{
			name:        "typed limit is terminal",
			err:         operationLimitError("run exceeds fact limit"),
			disposition: store.DomainOutcomeTerminalGenerationRefusal,
		},
		{
			name: "explicit terminal marker is terminal",
			err: store.WithTerminal(
				errors.New("deterministic refusal"),
			),
			disposition: store.DomainOutcomeTerminalGenerationRefusal,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			disposition, control := classifyDomainOutcome(test.err)
			if disposition != test.disposition || control != test.control {
				t.Fatalf(
					"classification = %q/%t, want %q/%t",
					disposition, control,
					test.disposition, test.control,
				)
			}
		})
	}
}
