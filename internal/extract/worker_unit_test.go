package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t201"
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
	latestByScope map[string]*store.ExtractionRun
	outcomes      map[string]*store.ExtractionDomainOutcome
	latestErr     error
	latestAttempt *store.ExtractionAttempt
	batches       []evidenceBatch
	// assertionAttributes mirrors the store's per-semantic-ID attribute
	// consistency guard so worker tests fail where SurrealDB would THROW.
	assertionAttributes map[string][3]string
	published           bool
	publishedWith       store.CoverageManifest
	aborted             bool
	abortCanceled       bool
	abortDeadline       time.Duration
	retainBatches       bool
	batchCount          int
	stagedFacts         int
	publishHook         func() error
	addHook             func() error
}

func newMemoryEvidence() *memoryEvidence {
	return &memoryEvidence{
		runs:          make(map[string]*store.ExtractionRun),
		latestByScope: make(map[string]*store.ExtractionRun),
		outcomes:      make(map[string]*store.ExtractionDomainOutcome),
		retainBatches: true,
	}
}

func (m *memoryEvidence) BeginExtractionRun(
	_ context.Context,
	scope store.ExtractionScope,
	extractor string,
) (*store.ExtractionRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextRun++
	run := &store.ExtractionRun{
		ID:   fmt.Sprintf("run-%d", m.nextRun),
		Repo: scope.Repository, Commit: scope.Commit,
		UnitDigest: scope.UnitDigest, Domain: scope.Domain,
		Extractor: extractor, Status: "staged",
	}
	m.runs[run.ID] = run
	m.latestAttempt = &store.ExtractionAttempt{
		RunID: run.ID, Repo: scope.Repository, Commit: scope.Commit,
		UnitDigest: scope.UnitDigest, Domain: scope.Domain,
		Extractor: extractor, Status: "staged",
	}
	copyOfRun := *run
	return &copyOfRun, nil
}

func (m *memoryEvidence) AddEvidence(_ context.Context, _ string, atoms []store.EvidenceAtom, assocs []store.SnapshotEvidence, asserts []store.Assertion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.addHook != nil {
		if err := m.addHook(); err != nil {
			return err
		}
	}
	// Mirror the real store's attribute-consistency contract (T23.R): a
	// duplicate semantic assertion ID whose (tier, code_role, detail)
	// differs is a permanent store rejection, and the in-memory harness
	// must not let extractors pass what SurrealDB would refuse.
	if m.assertionAttributes == nil {
		m.assertionAttributes = make(map[string][3]string)
	}
	for _, assertion := range asserts {
		id := store.ComputeAssertionID(assertion)
		attributes := [3]string{assertion.Tier, assertion.CodeRole, assertion.Detail}
		if previous, ok := m.assertionAttributes[id]; ok && previous != attributes {
			return fmt.Errorf("duplicate semantic assertion has conflicting attributes: %s", id)
		}
		m.assertionAttributes[id] = attributes
	}
	m.batchCount++
	m.stagedFacts += len(atoms)
	if m.retainBatches {
		m.batches = append(m.batches, evidenceBatch{
			atoms:   append([]store.EvidenceAtom(nil), atoms...),
			assocs:  append([]store.SnapshotEvidence(nil), assocs...),
			asserts: append([]store.Assertion(nil), asserts...),
		})
	}
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
		run.Coverage = coverage
		m.latest = run
		m.latestByScope[memoryScopeKey(store.ExtractionScope{
			Repository: run.Repo, Commit: run.Commit,
			UnitDigest: run.UnitDigest, Domain: run.Domain,
		})] = run
		if m.latestAttempt != nil && m.latestAttempt.RunID == runID {
			m.latestAttempt.Status = "published"
		}
	}
	return nil
}

func (m *memoryEvidence) PublishExtractionRunWithOutcome(
	ctx context.Context,
	runID string,
	coverage store.CoverageManifest,
	outcome store.ExtractionDomainOutcome,
) error {
	if err := m.PublishExtractionRun(ctx, runID, coverage); err != nil {
		return err
	}
	return m.RecordExtractionDomainOutcome(ctx, outcome)
}

func (m *memoryEvidence) RecordExtractionDomainOutcome(
	_ context.Context,
	outcome store.ExtractionDomainOutcome,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copyOfOutcome := outcome
	copyOfOutcome.Generation.Digest =
		store.ComputeExtractionGenerationDigest(copyOfOutcome.Generation)
	m.outcomes[memoryOutcomeKey(outcome.Scope)] = &copyOfOutcome
	return nil
}

func (m *memoryEvidence) LatestExtractionDomainOutcome(
	_ context.Context,
	scope store.ExtractionScope,
) (*store.ExtractionDomainOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	outcome := m.outcomes[memoryOutcomeKey(scope)]
	if outcome == nil || outcome.Scope != scope {
		return nil, store.ErrNotFound
	}
	copyOfOutcome := *outcome
	return &copyOfOutcome, nil
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
		if m.latestAttempt != nil && m.latestAttempt.RunID == runID {
			m.latestAttempt.Status = "aborted"
		}
	}
	return nil
}

func (m *memoryEvidence) LatestPublishedRun(
	_ context.Context,
	scope store.ExtractionScope,
) (*store.ExtractionRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latestErr != nil {
		return nil, m.latestErr
	}
	run := m.latestByScope[memoryScopeKey(scope)]
	if run == nil {
		return nil, store.ErrNotFound
	}
	copyOfRun := *run
	return &copyOfRun, nil
}

func (m *memoryEvidence) LatestExtractionAttempt(
	context.Context,
	store.ExtractionScope,
) (*store.ExtractionAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latestAttempt == nil {
		return nil, store.ErrNotFound
	}
	copyOfAttempt := *m.latestAttempt
	return &copyOfAttempt, nil
}

func memoryScopeKey(scope store.ExtractionScope) string {
	return scope.Repository + "\x00" + scope.Commit + "\x00" +
		scope.UnitDigest + "\x00" + scope.Domain
}

func memoryOutcomeKey(scope store.ExtractionScope) string {
	return scope.Repository + "\x00" + scope.Domain
}

func (*memoryEvidence) ListAssertions(context.Context, store.AssertionQuery) ([]store.Assertion, error) {
	return nil, nil
}
func (*memoryEvidence) ListReverseAssertions(
	context.Context, store.ReverseAssertionQuery,
) (*store.ReverseAssertionPage, error) {
	return &store.ReverseAssertionPage{}, nil
}
func (*memoryEvidence) ResolveEvidence(context.Context, string, string, string) (*store.EvidenceResolution, error) {
	return nil, store.ErrNotFound
}
func (*memoryEvidence) PinRun(context.Context, string, string) error { return nil }
func (*memoryEvidence) SweepEvidence(
	context.Context, time.Time, time.Duration,
) (store.EvidenceSweepProgress, error) {
	return store.EvidenceSweepProgress{}, nil
}

type unitCorpusFactory struct {
	lock func(context.Context, string) (func(), error)
}

func (f unitCorpusFactory) Lock(ctx context.Context, repo string) (func(), error) {
	if f.lock == nil {
		return func() {}, nil
	}
	return f.lock(ctx, repo)
}

func (unitCorpusFactory) New(repo, commit string) sdk.Corpus {
	return unitCorpus{repo: repo, commit: commit}
}

func unitFactory(lock func(context.Context, string) (func(), error)) CorpusFactory {
	return unitCorpusFactory{lock: lock}
}

type t202ProfileCorpus struct {
	repo, commit string
	files        map[string][]byte
}

func (c t202ProfileCorpus) RepoName() string { return c.repo }
func (c t202ProfileCorpus) Commit() string   { return c.commit }
func (c t202ProfileCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	paths := make([]string, 0, len(c.files))
	for filePath := range c.files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
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
func (c t202ProfileCorpus) Read(_ context.Context, filePath string) (sdk.Blob, error) {
	content, ok := c.files[filePath]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("profile path %q is absent", filePath)
	}
	digest := sha256.Sum256(content)
	return sdk.Blob{
		Content: string(content), Digest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

type t202ProfileFactory struct {
	files map[string][]byte
}

func (t202ProfileFactory) Lock(context.Context, string) (func(), error) {
	return func() {}, nil
}
func (f t202ProfileFactory) New(repo, commit string) sdk.Corpus {
	return t202ProfileCorpus{repo: repo, commit: commit, files: f.files}
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
		if len(batch.atoms) > evidenceChunkSize || len(batch.assocs) != len(batch.atoms) ||
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

func stageDeterministicUnitChunks(t *testing.T, count int) (*runSink, *memoryEvidence, store.CoverageManifest) {
	t.Helper()
	corpus := newVerifiedCorpus(unitCorpus{repo: "host/repo", commit: unitCommit})
	if err := corpus.Inventory(context.Background(), func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if _, err := corpus.Read(context.Background(), "same.proto"); err != nil {
		t.Fatal(err)
	}
	evidence := newMemoryEvidence()
	sink := newRunSink(context.Background(), evidence, "run-1",
		"host/repo", unitCommit, "1", corpus, nil)
	for i := range count {
		fact := unitFact("same.proto", fmt.Sprintf("object-%04d", i))
		if err := sink.Emit(fact); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Finish(); err != nil {
		t.Fatal(err)
	}
	stats, err := corpus.Stats()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := coverageManifest(sdk.Coverage{Protocols: []string{"protobuf"}},
		sink.atomCount, sink.assertionCount, sink.unresolvedCount, stats, emptyGitlinkInventory())
	if err != nil {
		t.Fatal(err)
	}
	return sink, evidence, manifest
}

func TestFactChunksAreDeterministicBoundedAndContentAddressed(t *testing.T) {
	firstSink, firstEvidence, firstCoverage := stageDeterministicUnitChunks(t, 600)
	secondSink, secondEvidence, secondCoverage := stageDeterministicUnitChunks(t, 600)

	if !reflect.DeepEqual(firstSink.stagedChunkIDs, secondSink.stagedChunkIDs) {
		t.Fatalf("chunk ids differ:\n%v\n%v", firstSink.stagedChunkIDs, secondSink.stagedChunkIDs)
	}
	if !reflect.DeepEqual(firstEvidence.batches, secondEvidence.batches) {
		t.Fatal("equal fact streams produced different staged rows")
	}
	if !reflect.DeepEqual(firstCoverage, secondCoverage) {
		t.Fatalf("equal fact streams produced different coverage:\n%+v\n%+v",
			firstCoverage, secondCoverage)
	}
	if firstSink.atomCount != secondSink.atomCount ||
		firstSink.assertionCount != secondSink.assertionCount ||
		firstSink.unresolvedCount != secondSink.unresolvedCount {
		t.Fatalf("coverage counters differ: %d/%d/%d vs %d/%d/%d",
			firstSink.atomCount, firstSink.assertionCount, firstSink.unresolvedCount,
			secondSink.atomCount, secondSink.assertionCount, secondSink.unresolvedCount)
	}
	if len(firstSink.stagedChunkIDs) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(firstSink.stagedChunkIDs))
	}
	seen := map[string]bool{}
	for _, id := range firstSink.stagedChunkIDs {
		if !validSHA256(id) || seen[id] {
			t.Fatalf("invalid or duplicate chunk id %q", id)
		}
		seen[id] = true
	}

	reordered := []sdk.Fact{
		unitFact("same.proto", "object-0001"),
		unitFact("same.proto", "object-0000"),
	}
	if buildFactChunk(0, reordered).ID ==
		buildFactChunk(0, []sdk.Fact{reordered[1], reordered[0]}).ID {
		t.Fatal("reordered facts produced the same chunk identity")
	}
}

func TestFactChunkReplayChangesNoRowsOrCounters(t *testing.T) {
	corpus := newVerifiedCorpus(unitCorpus{repo: "host/repo", commit: unitCommit})
	if err := corpus.Inventory(context.Background(), func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	evidence := newMemoryEvidence()
	sink := newRunSink(context.Background(), evidence, "run-1",
		"host/repo", unitCommit, "1", corpus, nil)
	chunk := buildFactChunk(0, []sdk.Fact{unitFact("same.proto", "object")})

	if err := sink.stageChunkLocked(chunk); err != nil {
		t.Fatal(err)
	}
	if err := sink.stageChunkLocked(chunk); err != nil {
		t.Fatal(err)
	}
	if evidence.batchCount != 1 || evidence.stagedFacts != 1 ||
		sink.atomCount != 1 || sink.assertionCount != 1 ||
		sink.chunkSequence != 1 || len(sink.stagedChunkIDs) != 1 {
		t.Fatalf("replay state: batches=%d facts=%d atoms=%d assertions=%d sequence=%d chunks=%v",
			evidence.batchCount, evidence.stagedFacts, sink.atomCount, sink.assertionCount,
			sink.chunkSequence, sink.stagedChunkIDs)
	}
}

func TestFactChunkMalformedShapesFailBeforeStaging(t *testing.T) {
	valid := buildFactChunk(0, []sdk.Fact{unitFact("same.proto", "object")})
	tests := []struct {
		name   string
		mutate func(*sdk.FactChunk)
	}{
		{"schema", func(chunk *sdk.FactChunk) { chunk.Schema = "future" }},
		{"empty", func(chunk *sdk.FactChunk) {
			chunk.Facts = nil
			chunk.ID = computeFactChunkID(*chunk)
		}},
		{"oversized", func(chunk *sdk.FactChunk) {
			chunk.Facts = make([]sdk.Fact, evidenceChunkSize+1)
			for i := range chunk.Facts {
				chunk.Facts[i] = unitFact("same.proto", fmt.Sprintf("object-%d", i))
			}
			chunk.ID = computeFactChunkID(*chunk)
		}},
		{"identity", func(chunk *sdk.FactChunk) {
			chunk.ID = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}},
		{"sequence", func(chunk *sdk.FactChunk) {
			chunk.Sequence = 1
			chunk.ID = computeFactChunkID(*chunk)
		}},
		{"fact", func(chunk *sdk.FactChunk) {
			chunk.Facts[0].Assertion.Predicate = ""
			chunk.ID = computeFactChunkID(*chunk)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chunk := valid
			chunk.Facts = append([]sdk.Fact(nil), valid.Facts...)
			test.mutate(&chunk)
			evidence := newMemoryEvidence()
			sink := newRunSink(context.Background(), evidence, "run-1",
				"host/repo", unitCommit, "1", nil, nil)
			if err := sink.stageChunkLocked(chunk); err == nil {
				t.Fatal("malformed chunk staged")
			}
			if evidence.batchCount != 0 || sink.atomCount != 0 ||
				sink.assertionCount != 0 || sink.chunkSequence != 0 {
				t.Fatalf("malformed chunk changed state: evidence=%+v sink=%+v", evidence, sink)
			}
		})
	}
}

func TestWorkerRejectsFactBeyondChunkedRunLimit(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	evidence.retainBatches = false
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
	if err != nil {
		t.Fatalf("terminal limit should settle the job: %v", err)
	}
	if evidence.published || !evidence.aborted {
		t.Fatalf("over-limit state: published=%v aborted=%v", evidence.published, evidence.aborted)
	}
	scope := store.ExtractionScope{
		Repository: repo.Name, Commit: unitCommit, Domain: "unit",
	}
	outcome, err := evidence.LatestExtractionDomainOutcome(
		context.Background(), scope,
	)
	if err != nil ||
		outcome.Disposition != store.DomainOutcomeTerminalGenerationRefusal ||
		!strings.Contains(outcome.Receipt, `"reason":"limit_refusal"`) {
		t.Fatalf("limit outcome = %+v, %v", outcome, err)
	}
	if err := worker.Handle(
		context.Background(),
		store.Job{Target: repo.Name, Force: true},
	); err != nil {
		t.Fatalf("forced terminal no-op: %v", err)
	}
	if evidence.nextRun != 1 {
		t.Fatalf("forced terminal generation created %d runs", evidence.nextRun)
	}
}

func TestWorkerStagesFrozenLargeProfileWithinMemoryCeiling(t *testing.T) {
	const (
		heapCeiling = uint64(256 << 20)
	)
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	evidence.retainBatches = false
	profile, err := t201.GenerateProfile(t201.ScaleProfileName)
	if err != nil {
		t.Fatal(err)
	}
	candidatePaths := make(map[string]bool)
	unresolved := 0
	for _, call := range profile.Oracle.Calls {
		candidatePaths[call.Path] = true
		if call.Resolution == t201.ResolutionUnresolved {
			unresolved++
		}
	}

	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	peak := baseline.HeapAlloc
	extractor := unitExtractor{domain: "unit", version: "1",
		candidate: func(filePath string) bool { return candidatePaths[filePath] },
		extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
			currentPath := ""
			for i, call := range profile.Oracle.Calls {
				if call.Path != currentPath {
					if _, err := corpus.Read(ctx, call.Path); err != nil {
						return sdk.Coverage{}, err
					}
					currentPath = call.Path
				}
				content := profile.Files[call.Path]
				digest := sha256.Sum256(content)
				object := call.CanonicalOperation
				tier := store.TierExact
				if call.Resolution == t201.ResolutionUnresolved {
					object = "unresolved:" + call.ID
					tier = store.TierUnresolved
				}
				fact := sdk.Fact{
					Atom: sdk.AtomInput{
						SchemaVersion: "t20-v1",
						BlobDigest:    "sha256:" + hex.EncodeToString(digest[:]),
						StartByte:     call.StartByte, EndByte: call.EndByte,
						RuleID: "t202-scale-worker",
						AdapterConfigDigest: "sha256:" +
							"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
						FactFingerprint: "CALLS_OPERATION|" + call.ID,
					},
					Assertion: sdk.AssertionInput{
						Predicate: "CALLS_OPERATION", Subject: call.ID, Object: object,
						Tier: tier, CodeRole: call.CodeRole,
					},
					Path: call.Path, StartLine: call.StartLine, EndLine: call.EndLine,
				}
				if err := emit(fact); err != nil {
					return sdk.Coverage{}, err
				}
				if i%evidenceChunkSize == 0 {
					var current runtime.MemStats
					runtime.ReadMemStats(&current)
					if current.HeapAlloc > peak {
						peak = current.HeapAlloc
					}
				}
			}
			return sdk.Coverage{
				Protocols:       []string{"grpc", "thrift", "unknown"},
				UnresolvedCount: unresolved,
			}, nil
		}}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: t202ProfileFactory{files: profile.Files}, Extractors: []Extractor{extractor}}
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatal(err)
	}
	var final runtime.MemStats
	runtime.ReadMemStats(&final)
	if final.HeapAlloc > peak {
		peak = final.HeapAlloc
	}
	incremental := uint64(0)
	if peak > baseline.HeapAlloc {
		incremental = peak - baseline.HeapAlloc
	}
	if incremental > heapCeiling {
		t.Fatalf("incremental heap = %d bytes, ceiling %d", incremental, heapCeiling)
	}
	targetFacts := len(profile.Oracle.Calls)
	wantChunks := (targetFacts + evidenceChunkSize - 1) / evidenceChunkSize
	if !evidence.published || evidence.batchCount != wantChunks ||
		evidence.stagedFacts != targetFacts ||
		evidence.publishedWith.AtomCount != targetFacts ||
		evidence.publishedWith.AssertionCount != targetFacts ||
		evidence.publishedWith.UnresolvedCount != unresolved {
		t.Fatalf("large staging: published=%v chunks=%d facts=%d coverage=%+v",
			evidence.published, evidence.batchCount, evidence.stagedFacts, evidence.publishedWith)
	}
	t.Logf("T20.2 frozen profile: %d facts, %d chunks, %d incremental heap bytes",
		targetFacts, wantChunks, incremental)
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
		for i := range evidenceChunkSize + 20 {
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

func TestWorkerStagingConflictLeavesRunInvisible(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	addCalls := 0
	evidence.addHook = func() error {
		addCalls++
		if addCalls == 1 {
			return store.ErrConflict
		}
		return nil
	}
	extractor := unitExtractor{domain: "unit", version: "1",
		extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
			for i := range evidenceChunkSize {
				if err := emitUnit(ctx, corpus, emit,
					unitFact(fmt.Sprintf("f%03d.proto", i), fmt.Sprintf("object-%03d", i))); err != nil {
					return sdk.Coverage{}, err
				}
			}
			return sdk.Coverage{}, nil
		}}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}
	err := worker.Handle(context.Background(), store.Job{Target: repo.Name})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("Handle error = %v", err)
	}
	if evidence.published || !evidence.aborted || evidence.batchCount != 0 {
		t.Fatalf("staging conflict state: published=%v aborted=%v batches=%d",
			evidence.published, evidence.aborted, evidence.batchCount)
	}
	scope := store.ExtractionScope{
		Repository: repo.Name, Commit: unitCommit, Domain: "unit",
	}
	outcome, outcomeErr := evidence.LatestExtractionDomainOutcome(
		context.Background(), scope,
	)
	if outcomeErr != nil ||
		outcome.Disposition != store.DomainOutcomeRetryableFailure {
		t.Fatalf("staging retry outcome = %+v, %v", outcome, outcomeErr)
	}
	if err := worker.Handle(
		context.Background(), store.Job{Target: repo.Name},
	); err != nil {
		t.Fatalf("retry after temporary staging conflict: %v", err)
	}
	outcome, outcomeErr = evidence.LatestExtractionDomainOutcome(
		context.Background(), scope,
	)
	if outcomeErr != nil ||
		outcome.Disposition != store.DomainOutcomePublished ||
		!evidence.published || evidence.nextRun != 2 {
		t.Fatalf(
			"retried publication = %+v, %v, published=%v runs=%d",
			outcome, outcomeErr, evidence.published, evidence.nextRun,
		)
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

// Waiting behind a long index or fetch of the same mirror must not consume
// the extraction budget: the lock waits on the job context and the timeout
// starts once the mirror is fenced.
func TestExtractionBudgetStartsAfterMirrorLock(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	lock := func(ctx context.Context, _ string) (func(), error) {
		if _, hasDeadline := ctx.Deadline(); hasDeadline {
			t.Fatal("lock wait consumes the extraction budget")
		}
		return func() {}, nil
	}
	extractor := unitExtractor{domain: "unit", version: "1",
		extract: func(ctx context.Context, _ sdk.Corpus, _ sdk.Emit) (sdk.Coverage, error) {
			if _, hasDeadline := ctx.Deadline(); !hasDeadline {
				t.Fatal("extractor runs without the extraction budget")
			}
			return sdk.Coverage{}, nil
		}}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: newMemoryEvidence(),
		NewCorpus: unitFactory(lock), Extractors: []Extractor{extractor}}
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatal(err)
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

func TestWorkerRecoversCandidatePanicBeforeBeginningRun(t *testing.T) {
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
	if evidence.published || evidence.aborted || evidence.nextRun != 0 {
		t.Fatalf(
			"candidate panic state: published=%v aborted=%v begun=%d",
			evidence.published, evidence.aborted, evidence.nextRun)
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

// A pre-outcome published run has no durable generation authority, so the
// worker replaces it even when commit and extractor version match. Once an
// outcome exists, that exact generation short-circuits.
func TestWorkerReplacesLegacyInventoryRuns(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	extractions := 0
	extractor := unitExtractor{domain: "unit", version: "1",
		extract: func(context.Context, sdk.Corpus, sdk.Emit) (sdk.Coverage, error) {
			extractions++
			return sdk.Coverage{Protocols: []string{"protobuf"}}, nil
		}}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{extractor}}

	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatal(err)
	}
	if extractions != 1 || evidence.publishedWith.InventoryPolicy != corpusInventoryPolicy ||
		!strings.HasPrefix(evidence.publishedWith.GitlinkDigest, "sha256:") {
		t.Fatalf("first publish = %d extractions, manifest %+v", extractions, evidence.publishedWith)
	}

	// Policy-current run at the same commit and version short-circuits.
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatal(err)
	}
	if extractions != 1 {
		t.Fatalf("policy-current run was re-extracted %d times", extractions)
	}

	// The prior nonempty generation must be replaced because it predates the
	// extractor-gated symlink contract and has no durable outcome.
	evidence.mu.Lock()
	evidence.latest.Coverage.InventoryPolicy = "gitlink-boundary-v1"
	clear(evidence.outcomes)
	evidence.mu.Unlock()
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatal(err)
	}
	if extractions != 2 || evidence.publishedWith.InventoryPolicy != corpusInventoryPolicy {
		t.Fatalf("prior policy run not replaced: %d extractions, manifest %+v", extractions, evidence.publishedWith)
	}

	// A markerless legacy manifest must also be replaced.
	evidence.mu.Lock()
	evidence.latest.Coverage.InventoryPolicy = ""
	evidence.latest.Coverage.GitlinkDigest = ""
	clear(evidence.outcomes)
	evidence.mu.Unlock()
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatal(err)
	}
	if extractions != 3 || evidence.publishedWith.InventoryPolicy != corpusInventoryPolicy {
		t.Fatalf("legacy run not replaced: %d extractions, manifest %+v", extractions, evidence.publishedWith)
	}
}

// T19.8: one domain's failure must not starve the remaining domains. Ordinary
// per-domain errors are joined into the job error so the runner retries;
// published domains short-circuit on that retry while the failed domain runs
// again. Cancellation stops new attempts.
func TestWorkerIsolatesPerDomainFailures(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	attempts := map[string]int{}
	extractorFor := func(domain string, fail bool) unitExtractor {
		return unitExtractor{domain: domain, version: "1",
			extract: func(context.Context, sdk.Corpus, sdk.Emit) (sdk.Coverage, error) {
				attempts[domain]++
				if fail {
					return sdk.Coverage{}, errors.New(domain + " exploded")
				}
				return sdk.Coverage{Protocols: []string{"protobuf"}}, nil
			}}
	}
	worker := Worker{Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil), Extractors: []Extractor{
			extractorFor("d1", true), extractorFor("d2", false), extractorFor("d3", false),
		}}

	err := worker.Handle(context.Background(), store.Job{Target: repo.Name})
	if err == nil || !strings.Contains(err.Error(), "d1 exploded") {
		t.Fatalf("aggregate error = %v", err)
	}
	if attempts["d1"] != 1 || attempts["d2"] != 1 || attempts["d3"] != 1 {
		t.Fatalf("first pass attempts = %v", attempts)
	}
	statuses := map[string]string{}
	evidence.mu.Lock()
	for _, run := range evidence.runs {
		statuses[run.Domain] = run.Status
	}
	evidence.mu.Unlock()
	if statuses["d1"] != "aborted" || statuses["d2"] != "published" || statuses["d3"] != "published" {
		t.Fatalf("run statuses = %v", statuses)
	}

	// Retry: published domains short-circuit, the aborted domain runs again.
	err = worker.Handle(context.Background(), store.Job{Target: repo.Name})
	if err == nil || !strings.Contains(err.Error(), "d1 exploded") {
		t.Fatalf("retry error = %v", err)
	}
	if attempts["d1"] != 2 || attempts["d2"] != 1 || attempts["d3"] != 1 {
		t.Fatalf("retry attempts = %v", attempts)
	}

	// A canceled context stops new domain attempts.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	err = worker.Handle(canceled, store.Job{Target: repo.Name})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	if attempts["d1"] != 2 || attempts["d2"] != 1 || attempts["d3"] != 1 {
		t.Fatalf("canceled attempts = %v", attempts)
	}
}
