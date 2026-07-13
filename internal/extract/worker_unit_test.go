package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

const unitCommit = "cccccccccccccccccccccccccccccccccccccccc"

type unitCorpus struct{ repo, commit string }

func (c unitCorpus) RepoName() string { return c.repo }
func (c unitCorpus) Commit() string   { return c.commit }
func (c unitCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	paths := []string{"a.proto", "invented.proto", "read.proto", "same.proto"}
	for i := range 600 {
		paths = append(paths, fmt.Sprintf("f%03d.proto", i))
	}
	for _, filePath := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(filePath); err != nil {
			return err
		}
	}
	return nil
}
func (c unitCorpus) Read(context.Context, string) (sdk.Blob, error) {
	digest := sha256.Sum256([]byte("same blob"))
	return sdk.Blob{
		Content: "same blob", Digest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

type repoGetterFunc func(context.Context, string) (*store.Repo, error)

func (f repoGetterFunc) GetRepo(ctx context.Context, name string) (*store.Repo, error) {
	return f(ctx, name)
}

type unitExtractor struct {
	domain, version string
	candidate       func(string) bool
	extract         func(context.Context, sdk.Corpus, sdk.Emit) (sdk.Coverage, error)
}

func (e unitExtractor) Domain() string  { return e.domain }
func (e unitExtractor) Version() string { return e.version }
func (e unitExtractor) Candidate(filePath string) bool {
	if e.candidate == nil {
		return false
	}
	return e.candidate(filePath)
}
func (e unitExtractor) Extract(ctx context.Context, c sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
	return e.extract(ctx, c, emit)
}

type evidenceBatch struct {
	atoms   []store.EvidenceAtom
	assocs  []store.SnapshotEvidence
	asserts []store.Assertion
}

type memoryEvidence struct {
	mu sync.Mutex

	nextRun       int
	runs          map[string]*store.ExtractionRun
	latest        *store.ExtractionRun
	batches       []evidenceBatch
	published     bool
	publishedWith store.CoverageManifest
	aborted       bool
	abortCanceled bool
	abortDeadline time.Duration
	publishHook   func() error
}

func newMemoryEvidence() *memoryEvidence {
	return &memoryEvidence{runs: make(map[string]*store.ExtractionRun)}
}

func (m *memoryEvidence) BeginExtractionRun(_ context.Context, repo, commit, domain, extractor string) (*store.ExtractionRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextRun++
	run := &store.ExtractionRun{
		ID: fmt.Sprintf("run-%d", m.nextRun), Repo: repo, Commit: commit,
		Domain: domain, Extractor: extractor, Status: "staged",
	}
	m.runs[run.ID] = run
	copyOfRun := *run
	return &copyOfRun, nil
}

func (m *memoryEvidence) AddEvidence(_ context.Context, _ string, atoms []store.EvidenceAtom, assocs []store.SnapshotEvidence, asserts []store.Assertion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batches = append(m.batches, evidenceBatch{
		atoms:   append([]store.EvidenceAtom(nil), atoms...),
		assocs:  append([]store.SnapshotEvidence(nil), assocs...),
		asserts: append([]store.Assertion(nil), asserts...),
	})
	return nil
}

func (m *memoryEvidence) PublishExtractionRun(_ context.Context, runID string, coverage store.CoverageManifest) error {
	m.mu.Lock()
	hook := m.publishHook
	m.mu.Unlock()
	if hook != nil {
		if err := hook(); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published = true
	m.publishedWith = coverage
	if run := m.runs[runID]; run != nil {
		run.Status = "published"
		m.latest = run
	}
	return nil
}

func (m *memoryEvidence) AbortExtractionRun(ctx context.Context, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aborted = true
	m.abortCanceled = ctx.Err() != nil
	if deadline, ok := ctx.Deadline(); ok {
		m.abortDeadline = time.Until(deadline)
	}
	if run := m.runs[runID]; run != nil && run.Status == "staged" {
		run.Status = "aborted"
	}
	return nil
}

func (m *memoryEvidence) LatestPublishedRun(context.Context, string, string) (*store.ExtractionRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latest == nil {
		return nil, store.ErrNotFound
	}
	copyOfRun := *m.latest
	return &copyOfRun, nil
}

func (*memoryEvidence) ListAssertions(context.Context, store.AssertionQuery) ([]store.Assertion, error) {
	return nil, nil
}
func (*memoryEvidence) ResolveEvidence(context.Context, string, string, string) (*store.EvidenceResolution, error) {
	return nil, store.ErrNotFound
}
func (*memoryEvidence) PinRun(context.Context, string, string) error { return nil }
func (*memoryEvidence) SweepEvidence(context.Context, time.Time, time.Duration) (int, error) {
	return 0, nil
}

func unitFactory(lock func(context.Context, string) (func(), error)) CorpusFactory {
	return CorpusFactoryFuncs{
		LockFunc: lock,
		NewFunc:  func(repo, commit string) sdk.Corpus { return unitCorpus{repo: repo, commit: commit} },
	}
}

func unitFact(filePath, object string) sdk.Fact {
	digest := sha256.Sum256([]byte("same blob"))
	return sdk.Fact{
		Atom: sdk.AtomInput{
			SchemaVersion: "t12-v2", BlobDigest: "sha256:" + hex.EncodeToString(digest[:]),
			StartByte: 0, EndByte: 4, RuleID: "unit-rule",
			AdapterConfigDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			FactFingerprint:     "P|same",
		},
		Assertion: sdk.AssertionInput{
			Predicate: "P", Subject: filePath, Object: object,
			Tier: store.TierExact, CodeRole: "production",
		},
		Path: filePath, StartLine: 1, EndLine: 1,
	}
}

func TestValidateFactAcceptsPlannedCodeRoles(t *testing.T) {
	for _, role := range []string{"", "production", "test", "mock", "generated", "vendor"} {
		fact := unitFact("a.proto", "object")
		fact.Assertion.CodeRole = role
		if err := validateFact(fact); err != nil {
			t.Errorf("validateFact(code role %q): %v", role, err)
		}
	}
	fact := unitFact("a.proto", "object")
	fact.Assertion.CodeRole = "benchmark"
	if err := validateFact(fact); err == nil {
		t.Fatal("validateFact accepted an unknown code role")
	}
}

func emitUnit(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit, fact sdk.Fact) error {
	if _, err := corpus.Read(ctx, fact.Path); err != nil {
		return err
	}
	return emit(fact)
}

func readyRepoGetter(repo *store.Repo) RepoGetter {
	return repoGetterFunc(func(_ context.Context, name string) (*store.Repo, error) {
		if repo == nil || name != repo.Name {
			return nil, store.ErrNotFound
		}
		copyOfRepo := *repo
		return &copyOfRepo, nil
	})
}

func TestWorkerStreamsBoundedBatchesAndCountsDistinctRows(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	extractor := unitExtractor{domain: "unit", version: "1",
		candidate: func(filePath string) bool { return strings.HasPrefix(filePath, "f") },
		extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
			for i := range 600 {
				if err := emitUnit(ctx, corpus, emit, unitFact(fmt.Sprintf("f%03d.proto", i), fmt.Sprintf("object-%03d", i))); err != nil {
					return sdk.Coverage{}, err
				}
			}
			return sdk.Coverage{Protocols: []string{"protobuf"}}, nil
		}}
	worker := Worker{
		Repos: readyRepoGetter(repo), Evidence: evidence, NewCorpus: unitFactory(nil),
		Extractors: []Extractor{extractor},
	}
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatal(err)
	}
	if got := len(evidence.batches); got != 3 {
		t.Fatalf("batch count = %d, want 3", got)
	}
	for i, batch := range evidence.batches {
		if len(batch.atoms) > evidenceBatchSize || len(batch.assocs) != len(batch.atoms) ||
			len(batch.asserts) != len(batch.atoms) {
			t.Fatalf("batch %d is not self-contained/bounded: %d/%d/%d", i,
				len(batch.atoms), len(batch.assocs), len(batch.asserts))
		}
	}
	if evidence.publishedWith.AtomCount != 1 || evidence.publishedWith.AssertionCount != 600 {
		t.Fatalf("coverage counts = %+v, want 1 distinct atom / 600 assertions", evidence.publishedWith)
	}
	coverage := evidence.publishedWith
	if coverage.CorpusFileCount != 604 || coverage.CandidateFileCount != 600 ||
		coverage.ReadFileCount != 600 || coverage.ReadBytes != 600*int64(len("same blob")) ||
		!validSHA256(coverage.SourceScopeDigest) {
		t.Fatalf("source coverage = %+v", coverage)
	}
}

func TestWorkerRejectsFactBeyondRunLimit(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	extractor := unitExtractor{domain: "unit", version: "1",
		candidate: func(filePath string) bool { return filePath == "same.proto" },
		extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
			fact := unitFact("same.proto", "object")
			for range maxFactsPerRun + 1 {
				if err := emitUnit(ctx, corpus, emit, fact); err != nil {
					return sdk.Coverage{}, err
				}
			}
			return sdk.Coverage{}, nil
		}}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
	err := worker.Handle(context.Background(), store.Job{Target: repo.Name})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exceeds %d-fact limit", maxFactsPerRun)) {
		t.Fatalf("Handle error = %v", err)
	}
	if evidence.published || !evidence.aborted {
		t.Fatalf("over-limit state: published=%v aborted=%v", evidence.published, evidence.aborted)
	}
}

func TestWorkerCountsSameRunDuplicateFactOnce(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	extractor := unitExtractor{domain: "unit", version: "1", extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
		fact := unitFact("same.proto", "object")
		if err := emitUnit(ctx, corpus, emit, fact); err != nil {
			return sdk.Coverage{}, err
		}
		return sdk.Coverage{}, emitUnit(ctx, corpus, emit, fact)
	}}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatal(err)
	}
	if evidence.publishedWith.AtomCount != 1 || evidence.publishedWith.AssertionCount != 1 {
		t.Fatalf("duplicate coverage counts = %+v", evidence.publishedWith)
	}
}

func TestWorkerFailureAfterFlushAbortsWithoutPublish(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	want := errors.New("late parse failure")
	extractor := unitExtractor{domain: "unit", version: "1", extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
		for i := range evidenceBatchSize + 20 {
			if err := emitUnit(ctx, corpus, emit, unitFact(fmt.Sprintf("f%03d.proto", i), fmt.Sprintf("object-%03d", i))); err != nil {
				return sdk.Coverage{}, err
			}
		}
		return sdk.Coverage{}, want
	}}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
	err := worker.Handle(context.Background(), store.Job{Target: repo.Name})
	if !errors.Is(err, want) || store.Classify(err) != store.ClassExtract {
		t.Fatalf("Handle error = %v", err)
	}
	if len(evidence.batches) != 1 || evidence.published || !evidence.aborted {
		t.Fatalf("failed run state: batches=%d published=%v aborted=%v",
			len(evidence.batches), evidence.published, evidence.aborted)
	}
}

func TestWorkerRejectsCoverageFailureAndUnresolvedMismatch(t *testing.T) {
	tests := []sdk.Coverage{
		{Failures: []string{"partial read"}},
		{UnresolvedCount: 1},
	}
	for _, coverage := range tests {
		repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
		evidence := newMemoryEvidence()
		extractor := unitExtractor{domain: "unit", version: "1", extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
			if err := emitUnit(ctx, corpus, emit, unitFact("a.proto", "object")); err != nil {
				return sdk.Coverage{}, err
			}
			return coverage, nil
		}}
		worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
			NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
		if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err == nil {
			t.Fatalf("coverage %+v published", coverage)
		}
		if evidence.published || !evidence.aborted {
			t.Fatalf("coverage failure state: published=%v aborted=%v", evidence.published, evidence.aborted)
		}
	}
}

func TestWorkerCancellationUsesBoundedDetachedAbort(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	ctx, cancel := context.WithCancel(context.Background())
	extractor := unitExtractor{domain: "unit", version: "1", extract: func(_ context.Context, _ sdk.Corpus, _ sdk.Emit) (sdk.Coverage, error) {
		cancel()
		return sdk.Coverage{}, nil
	}}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
	err := worker.Handle(ctx, store.Job{Target: repo.Name})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Handle error = %v", err)
	}
	if !evidence.aborted || evidence.abortCanceled || evidence.abortDeadline <= 0 ||
		evidence.abortDeadline > abortTimeout {
		t.Fatalf("abort context: aborted=%v canceled=%v deadline=%v",
			evidence.aborted, evidence.abortCanceled, evidence.abortDeadline)
	}
}

func TestWorkerTreatsGuardedStaleConflictAsSuccess(t *testing.T) {
	for _, mutate := range []func(*store.Repo){
		func(repo *store.Repo) { repo.Deleting = true },
		func(repo *store.Repo) { repo.IndexedCommitHash = "dddddddddddddddddddddddddddddddddddddddd" },
	} {
		repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
		var repoMu sync.Mutex
		getter := repoGetterFunc(func(_ context.Context, _ string) (*store.Repo, error) {
			repoMu.Lock()
			defer repoMu.Unlock()
			copyOfRepo := *repo
			return &copyOfRepo, nil
		})
		evidence := newMemoryEvidence()
		evidence.publishHook = func() error {
			repoMu.Lock()
			mutate(repo)
			repoMu.Unlock()
			return store.ErrConflict
		}
		extractor := unitExtractor{domain: "unit", version: "1", extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
			return sdk.Coverage{}, emitUnit(ctx, corpus, emit, unitFact("a.proto", "object"))
		}}
		worker := Worker{Repos: getter, Evidence: evidence,
			NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
		if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
			t.Fatalf("stale conflict should complete quietly: %v", err)
		}
		if evidence.published || !evidence.aborted {
			t.Fatalf("stale conflict state: published=%v aborted=%v", evidence.published, evidence.aborted)
		}
	}
}

func TestWorkerPreservesUnexplainedPublishConflict(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	evidence.publishHook = func() error { return store.ErrConflict }
	extractor := unitExtractor{domain: "unit", version: "1", extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
		return sdk.Coverage{}, emitUnit(ctx, corpus, emit, unitFact("a.proto", "object"))
	}}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
	err := worker.Handle(context.Background(), store.Job{Target: repo.Name})
	if !errors.Is(err, store.ErrConflict) || store.Classify(err) != store.ClassExtract {
		t.Fatalf("Handle error = %v", err)
	}
	if !evidence.aborted {
		t.Fatal("unexplained conflict did not abort staged run")
	}
}

func TestWorkerLocksBeforeRepositoryReloadAndCorpusUse(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	locked := false
	lock := func(context.Context, string) (func(), error) {
		if locked {
			t.Fatal("double lock")
		}
		locked = true
		return func() { locked = false }, nil
	}
	getter := repoGetterFunc(func(context.Context, string) (*store.Repo, error) {
		if !locked {
			t.Fatal("repository loaded outside mirror lock")
		}
		copyOfRepo := *repo
		return &copyOfRepo, nil
	})
	evidence := newMemoryEvidence()
	extractor := unitExtractor{domain: "unit", version: "1", extract: func(_ context.Context, _ sdk.Corpus, _ sdk.Emit) (sdk.Coverage, error) {
		if !locked {
			t.Fatal("extractor used corpus outside mirror lock")
		}
		return sdk.Coverage{}, nil
	}}
	worker := Worker{Repos: getter, Evidence: evidence,
		NewCorpus: unitFactory(lock), Extractors: []Extractor{extractor}}
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatal(err)
	}
	if locked {
		t.Fatal("mirror lock not released")
	}
}

func TestWorkerRecoversExtractorPanicAndAborts(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	extractor := unitExtractor{domain: "unit", version: "1", extract: func(context.Context, sdk.Corpus, sdk.Emit) (sdk.Coverage, error) {
		panic(panickingStringer{})
	}}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
	err := worker.Handle(context.Background(), store.Job{Target: repo.Name})
	if err == nil || !strings.Contains(err.Error(), "extractor panic") ||
		store.Classify(err) != store.ClassExtract {
		t.Fatalf("Handle error = %v", err)
	}
	if evidence.published || !evidence.aborted {
		t.Fatalf("panic state: published=%v aborted=%v", evidence.published, evidence.aborted)
	}
}

func TestWorkerRecoversCandidatePanicAndAborts(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	extractor := unitExtractor{
		domain: "unit", version: "1",
		candidate: func(string) bool { panic(panickingStringer{}) },
		extract: func(context.Context, sdk.Corpus, sdk.Emit) (sdk.Coverage, error) {
			return sdk.Coverage{}, nil
		},
	}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
	err := worker.Handle(context.Background(), store.Job{Target: repo.Name})
	if err == nil || !strings.Contains(err.Error(), "candidate predicate panic") ||
		store.Classify(err) != store.ClassExtract {
		t.Fatalf("Handle error = %v", err)
	}
	if evidence.published || !evidence.aborted {
		t.Fatalf("candidate panic state: published=%v aborted=%v", evidence.published, evidence.aborted)
	}
}

func TestWorkerRefusesUnreadCandidateWhenWalkErrorIgnored(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	wantWalkError := errors.New("selector stopped")
	extractor := unitExtractor{
		domain: "unit", version: "1", candidate: func(filePath string) bool { return filePath == "a.proto" },
		extract: func(ctx context.Context, corpus sdk.Corpus, _ sdk.Emit) (sdk.Coverage, error) {
			// A buggy extractor ignores the visitor error and returns success. The
			// trusted candidate ledger still prevents partial publication.
			_ = corpus.WalkFiles(ctx, func(string) error { return wantWalkError })
			return sdk.Coverage{}, nil
		},
	}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
	err := worker.Handle(context.Background(), store.Job{Target: repo.Name})
	if err == nil || !strings.Contains(err.Error(), "candidate \"a.proto\" was not read") {
		t.Fatalf("Handle error = %v", err)
	}
	if evidence.published || !evidence.aborted {
		t.Fatalf("unread candidate state: published=%v aborted=%v", evidence.published, evidence.aborted)
	}
}

type panickingStringer struct{}

func (panickingStringer) String() string { panic("formatter panic") }

func TestWorkerRejectsUnboundOrForgedSourceProvenance(t *testing.T) {
	tests := []struct {
		name    string
		extract func(context.Context, sdk.Corpus, sdk.Emit) (sdk.Coverage, error)
	}{
		{
			name: "path was not read",
			extract: func(_ context.Context, _ sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
				return sdk.Coverage{}, emit(unitFact("invented.proto", "object"))
			},
		},
		{
			name: "digest mismatch",
			extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
				fact := unitFact("read.proto", "object")
				if _, err := corpus.Read(ctx, fact.Path); err != nil {
					return sdk.Coverage{}, err
				}
				fact.Atom.BlobDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
				return sdk.Coverage{}, emit(fact)
			},
		},
		{
			name: "span beyond blob",
			extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
				fact := unitFact("read.proto", "object")
				if _, err := corpus.Read(ctx, fact.Path); err != nil {
					return sdk.Coverage{}, err
				}
				fact.Atom.EndByte = len("same blob") + 1
				return sdk.Coverage{}, emit(fact)
			},
		},
		{
			name: "line span mismatch",
			extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
				fact := unitFact("read.proto", "object")
				if _, err := corpus.Read(ctx, fact.Path); err != nil {
					return sdk.Coverage{}, err
				}
				fact.StartLine = 2
				fact.EndLine = 2
				return sdk.Coverage{}, emit(fact)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
			evidence := newMemoryEvidence()
			extractor := unitExtractor{domain: "unit", version: "1", extract: test.extract}
			worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
				NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
			if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err == nil {
				t.Fatal("forged provenance published")
			}
			if evidence.published || !evidence.aborted {
				t.Fatalf("forged provenance state: published=%v aborted=%v", evidence.published, evidence.aborted)
			}
		})
	}
}
