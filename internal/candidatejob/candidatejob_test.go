package candidatejob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

type policyExtractor struct {
	domain, version, requiredSuffix string
}

func (extractor policyExtractor) Domain() string  { return extractor.domain }
func (extractor policyExtractor) Version() string { return extractor.version }
func (extractor policyExtractor) Candidate(filePath string) bool {
	return strings.HasSuffix(filePath, extractor.requiredSuffix)
}
func (extractor policyExtractor) Extract(
	context.Context,
	sdk.Corpus,
	sdk.Emit,
) (sdk.Coverage, error) {
	return sdk.Coverage{}, nil
}

type replayAuditExtractor struct {
	domain, version, suffix string
	paths                   []string
}

func (extractor *replayAuditExtractor) Domain() string  { return extractor.domain }
func (extractor *replayAuditExtractor) Version() string { return extractor.version }
func (extractor *replayAuditExtractor) Candidate(filePath string) bool {
	return strings.HasSuffix(filePath, extractor.suffix)
}
func (extractor *replayAuditExtractor) Extract(
	ctx context.Context,
	corpus sdk.Corpus,
	_ sdk.Emit,
) (sdk.Coverage, error) {
	var paths []string
	if err := corpus.WalkFiles(ctx, func(filePath string) error {
		if _, err := corpus.Read(ctx, filePath); err != nil {
			return err
		}
		paths = append(paths, filePath)
		return nil
	}); err != nil {
		return sdk.Coverage{}, err
	}
	extractor.paths = paths
	return sdk.Coverage{}, nil
}

type replayAuditEvidence struct {
	mu        sync.Mutex
	next      int
	staged    map[string]*store.ExtractionRun
	current   map[string]*store.ExtractionRun
	outcomes  map[string]*store.ExtractionDomainOutcome
	onOutcome func(store.ExtractionDomainOutcome)
}

func newReplayAuditEvidence() *replayAuditEvidence {
	return &replayAuditEvidence{
		staged:   make(map[string]*store.ExtractionRun),
		current:  make(map[string]*store.ExtractionRun),
		outcomes: make(map[string]*store.ExtractionDomainOutcome),
	}
}

func (evidence *replayAuditEvidence) BeginExtractionRun(
	_ context.Context,
	scope store.ExtractionScope,
	extractor string,
) (*store.ExtractionRun, error) {
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	evidence.next++
	run := &store.ExtractionRun{
		ID:   fmt.Sprintf("replay-run-%d", evidence.next),
		Repo: scope.Repository, Commit: scope.Commit,
		UnitDigest: scope.UnitDigest, Domain: scope.Domain,
		Extractor: extractor, Status: "staged",
	}
	evidence.staged[run.ID] = run
	cloned := *run
	return &cloned, nil
}

func (*replayAuditEvidence) AddEvidence(
	context.Context,
	string,
	[]store.EvidenceAtom,
	[]store.SnapshotEvidence,
	[]store.Assertion,
) error {
	return nil
}

func (evidence *replayAuditEvidence) PublishExtractionRun(
	_ context.Context,
	runID string,
	coverage store.CoverageManifest,
) error {
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	run := evidence.staged[runID]
	if run == nil {
		return store.ErrNotFound
	}
	run.Status = "published"
	run.Coverage = coverage
	evidence.current[run.Domain] = run
	return nil
}

func (evidence *replayAuditEvidence) PublishExtractionRunWithOutcome(
	ctx context.Context,
	runID string,
	coverage store.CoverageManifest,
	outcome store.ExtractionDomainOutcome,
) error {
	if err := evidence.PublishExtractionRun(ctx, runID, coverage); err != nil {
		return err
	}
	return evidence.RecordExtractionDomainOutcome(ctx, outcome)
}

func (evidence *replayAuditEvidence) RecordExtractionDomainOutcome(
	_ context.Context,
	outcome store.ExtractionDomainOutcome,
) error {
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	copyOfOutcome := outcome
	copyOfOutcome.Generation.Digest =
		store.ComputeExtractionGenerationDigest(copyOfOutcome.Generation)
	evidence.outcomes[outcome.Scope.Repository+"\x00"+outcome.Scope.Domain] =
		&copyOfOutcome
	hook := evidence.onOutcome
	if hook != nil {
		hook(copyOfOutcome)
	}
	return nil
}

func (evidence *replayAuditEvidence) LatestExtractionDomainOutcome(
	_ context.Context,
	scope store.ExtractionScope,
) (*store.ExtractionDomainOutcome, error) {
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	outcome := evidence.outcomes[scope.Repository+"\x00"+scope.Domain]
	if outcome == nil || outcome.Scope != scope {
		return nil, store.ErrNotFound
	}
	copyOfOutcome := *outcome
	return &copyOfOutcome, nil
}

func (evidence *replayAuditEvidence) AbortExtractionRun(
	_ context.Context,
	runID string,
) error {
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	delete(evidence.staged, runID)
	return nil
}

func (evidence *replayAuditEvidence) LatestPublishedRun(
	_ context.Context,
	scope store.ExtractionScope,
) (*store.ExtractionRun, error) {
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	run := evidence.current[scope.Domain]
	if run == nil ||
		run.Repo != scope.Repository ||
		run.Commit != scope.Commit ||
		run.UnitDigest != scope.UnitDigest {
		return nil, store.ErrNotFound
	}
	cloned := *run
	return &cloned, nil
}

func (*replayAuditEvidence) LatestExtractionAttempt(
	context.Context,
	store.ExtractionScope,
) (*store.ExtractionAttempt, error) {
	return nil, store.ErrNotFound
}

func (*replayAuditEvidence) ListAssertions(
	context.Context,
	store.AssertionQuery,
) ([]store.Assertion, error) {
	return nil, nil
}

func (*replayAuditEvidence) ListReverseAssertions(
	context.Context,
	store.ReverseAssertionQuery,
) (*store.ReverseAssertionPage, error) {
	return &store.ReverseAssertionPage{}, nil
}

func (*replayAuditEvidence) ResolveEvidence(
	context.Context,
	string,
	string,
	string,
) (*store.EvidenceResolution, error) {
	return nil, store.ErrNotFound
}

func (*replayAuditEvidence) PinRun(
	context.Context,
	string,
	string,
) error {
	return nil
}

func (*replayAuditEvidence) SweepEvidence(
	context.Context,
	time.Time,
	time.Duration,
) (store.EvidenceSweepProgress, error) {
	return store.EvidenceSweepProgress{}, nil
}

type legacyV3PartitionPolicy struct {
	EnumerationPolicy              string `json:"enumeration_policy"`
	CallerHashPolicy               string `json:"caller_hash_policy"`
	LocalProjectionPolicy          string `json:"local_projection_policy"`
	InitialPrefixBits              int    `json:"initial_prefix_bits"`
	MaxRecords                     int    `json:"max_records"`
	MaxDeclaredBytes               int64  `json:"max_declared_bytes"`
	MaxLocalProjectionArtifacts    int    `json:"max_local_projection_artifacts"`
	MaxLocalProjectionContentBytes int64  `json:"max_local_projection_content_bytes"`
	RecordOrdering                 string `json:"record_ordering"`
	SplitRule                      string `json:"split_rule"`
}

func legacyV3FrozenPartitionPolicy() legacyV3PartitionPolicy {
	return legacyV3PartitionPolicy{
		EnumerationPolicy:              "phebs-candidate-enumeration-v2",
		CallerHashPolicy:               "phebs-caller-path-v1",
		LocalProjectionPolicy:          "focused-domain-repository-order-v1",
		InitialPrefixBits:              2,
		MaxRecords:                     4096,
		MaxDeclaredBytes:               64 << 20,
		MaxLocalProjectionArtifacts:    16_384,
		MaxLocalProjectionContentBytes: 4 << 30,
		RecordOrdering:                 "hash-path-oid-v1",
		SplitRule:                      "next-hash-bit-v1",
	}
}

type legacyV3Manifest struct {
	Schema            string                         `json:"schema"`
	Repository        string                         `json:"repository"`
	Commit            string                         `json:"commit"`
	UnitDigest        string                         `json:"unit_digest"`
	PolicyDigest      string                         `json:"policy_digest"`
	GenerationDigest  string                         `json:"generation_digest"`
	TypedIndex        *candidate.TypedIndexSelection `json:"typed_index,omitempty"`
	PartitionPolicy   legacyV3PartitionPolicy        `json:"partition_policy"`
	Policies          []candidate.PolicyIdentity     `json:"policies"`
	Corpus            candidate.CorpusSummary        `json:"corpus"`
	UnitCorpus        candidate.CorpusSummary        `json:"unit_corpus"`
	Domains           []candidate.DomainSummary      `json:"domains"`
	TypedInputs       []candidate.TypedInput         `json:"typed_inputs"`
	RepositoryMembers []candidate.Artifact           `json:"repository_members"`
	LocalProjections  []candidate.LocalProjection    `json:"local_projections"`
	CallerLeaves      []candidate.CallerLeaf         `json:"caller_leaves"`
	Digest            string                         `json:"digest"`
}

func legacyV3Digest(domain string, payload []byte) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(domain))
	_, _ = hasher.Write(payload)
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

type manifestStore struct {
	mu sync.Mutex

	repository *store.Repo
	pointer    *store.CandidateManifestPublication

	publishFailure  error
	pointerFailure  error
	clearFailure    error
	beforePublish   func(*manifestStore)
	publishCalls    int
	fanoutCount     int
	getPointerCalls int
	clearCalls      int
	repairRevision  uint64
}

func (state *manifestStore) CandidateControlRepairNeeded(
	_ context.Context,
	publication store.CandidateManifestPublication,
) (bool, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.repairRevision == publication.ControlRevision, nil
}

func (state *manifestStore) GetRepo(
	_ context.Context,
	name string,
) (*store.Repo, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.repository == nil || state.repository.Name != name {
		return nil, store.ErrNotFound
	}
	return cloneRepository(state.repository), nil
}

func (state *manifestStore) GetCandidateManifestPublication(
	_ context.Context,
	repository string,
) (*store.CandidateManifestPublication, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.getPointerCalls++
	if state.pointerFailure != nil {
		return nil, state.pointerFailure
	}
	if state.pointer == nil || state.pointer.Repository != repository {
		return nil, store.ErrNotFound
	}
	result := *state.pointer
	return &result, nil
}

func (state *manifestStore) PublishCandidateManifest(
	_ context.Context,
	publication store.CandidateManifestPublication,
) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.publishCalls++
	if state.beforePublish != nil {
		hook := state.beforePublish
		state.beforePublish = nil
		hook(state)
	}
	if state.publishFailure != nil {
		err := state.publishFailure
		state.publishFailure = nil
		return err
	}
	if state.repository == nil ||
		state.repository.Name != publication.Repository ||
		state.repository.Deleting ||
		state.repository.IndexedCommitHash != publication.HeadCommit ||
		unitDigest(state.repository.IndexedAnalysisUnit) != publication.UnitDigest {
		return store.ErrConflict
	}
	same := state.pointer != nil &&
		samePublication(*state.pointer, publication)
	if state.pointer != nil && samePublicationScope(*state.pointer, publication) &&
		!same {
		return store.ErrConflict
	}
	switch {
	case state.pointer == nil:
		publication.ControlRevision = 1
	case same && publication.ControlRevision == 0:
		publication.ControlRevision = state.pointer.ControlRevision
	case same &&
		publication.ControlRevision == state.pointer.ControlRevision+1:
	case !same && publication.ControlRevision == 0:
		publication.ControlRevision = state.pointer.ControlRevision + 1
	default:
		return store.ErrConflict
	}
	if state.pointer == nil || !same ||
		publication.ControlRevision != state.pointer.ControlRevision {
		publication.PublishedAt = time.Now().UTC()
		state.pointer = &publication
	} else {
		publication.PublishedAt = state.pointer.PublishedAt
		state.pointer = &publication
	}
	if state.repairRevision != 0 &&
		publication.ControlRevision > state.repairRevision {
		state.repairRevision = 0
	}
	if state.fanoutCount == 0 {
		state.fanoutCount++
	}
	return nil
}

func (state *manifestStore) ClearCandidateManifestPublication(
	_ context.Context,
	repository string,
) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.clearCalls++
	if state.clearFailure != nil {
		return state.clearFailure
	}
	if state.pointer != nil && state.pointer.Repository == repository {
		state.pointer = nil
	}
	state.pointerFailure = nil
	return nil
}

func (state *manifestStore) ListCandidateManifestPublications(
	context.Context,
) ([]store.CandidateManifestPublication, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.pointer == nil {
		return nil, nil
	}
	return []store.CandidateManifestPublication{*state.pointer}, nil
}

func cloneRepository(input *store.Repo) *store.Repo {
	if input == nil {
		return nil
	}
	result := *input
	result.IndexedAnalysisUnit = analysisunit.CloneState(input.IndexedAnalysisUnit)
	result.IndexedRevisions = slices.Clone(input.IndexedRevisions)
	return &result
}

func unitDigest(unit *analysisunit.State) string {
	if unit == nil {
		return ""
	}
	return unit.Digest
}

func samePublicationScope(
	left, right store.CandidateManifestPublication,
) bool {
	return left.Repository == right.Repository &&
		left.HeadCommit == right.HeadCommit &&
		left.UnitDigest == right.UnitDigest &&
		left.PolicyDigest == right.PolicyDigest
}

func samePublication(left, right store.CandidateManifestPublication) bool {
	return samePublicationScope(left, right) &&
		left.ManifestDigest == right.ManifestDigest &&
		left.GenerationDigest == right.GenerationDigest &&
		left.ManifestPath == right.ManifestPath
}

func TestWorkerRetryRepairsPublicationAndProviderStreamsBothPlanes(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	dataDir, repository, commit := candidateGitFixture(t)
	unit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service"},
		Supporting: []string{},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	state := &manifestStore{
		repository: &store.Repo{
			Name: repository, IndexedCommitHash: commit,
			IndexedAnalysisUnit: unit,
		},
		publishFailure: errors.New("injected pointer failure"),
	}
	extractors := []extract.Extractor{
		policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		},
		policyExtractor{
			domain: "grpc-caller", version: "caller-v1",
			requiredSuffix: ".go",
		},
	}
	worker, provider, err := New(dataDir, state, extractors)
	if err != nil {
		t.Fatal(err)
	}
	strictOpens := 0
	recoverPublication := worker.recover
	worker.recover = func(
		ctx context.Context,
		root string,
		expected candidate.Expected,
	) (*candidate.Publication, error) {
		strictOpens++
		if !candidate.IsPublishing(root, repository) {
			t.Fatal("recovery removed the marker before strict validation")
		}
		publication, recoverErr := recoverPublication(ctx, root, expected)
		if !candidate.IsPublishing(root, repository) {
			t.Fatal("strict recovery removed the marker before state commit")
		}
		return publication, recoverErr
	}
	if worker.policies != provider.policies ||
		worker.policies.Digest() != provider.PolicyDigest() {
		t.Fatal("planner and provider did not share one frozen policy generation")
	}

	job := store.Job{Kind: store.JobCandidate, Target: repository}
	firstErr := worker.Handle(ctx, job)
	if firstErr == nil ||
		store.Classify(firstErr) != store.ClassExtract ||
		!strings.Contains(firstErr.Error(), "injected pointer failure") {
		t.Fatalf("first Handle error = %v", firstErr)
	}
	root := CandidateRoot(dataDir)
	if !candidate.IsPublishing(root, repository) {
		t.Fatal("failed database transition did not leave the fail-closed marker")
	}
	manifestPath := filepath.Join(root, candidate.ManifestName(repository))
	before, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := worker.Handle(ctx, job); err != nil {
		t.Fatalf("retry Handle: %v", err)
	}
	if candidate.IsPublishing(root, repository) {
		t.Fatal("successful retry left the publication marker")
	}
	after, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("retry rebuilt a strictly reusable filesystem publication")
	}
	if state.publishCalls != 2 || state.fanoutCount != 1 || state.pointer == nil {
		t.Fatalf(
			"publication calls/fanout/pointer = %d/%d/%+v",
			state.publishCalls, state.fanoutCount, state.pointer,
		)
	}
	if strictOpens != 1 {
		t.Fatalf("marker recovery strict opens = %d, want 1", strictOpens)
	}

	request := extract.CandidateManifestRequest{
		Repository: repository, Commit: commit,
		AnalysisUnit: analysisunit.CloneState(unit),
		Domains:      slices.Clone(provider.domains),
	}
	providerStrictOpens := 0
	openProviderPublication := provider.open
	provider.open = func(
		ctx context.Context,
		root string,
		expected candidate.Expected,
	) (*candidate.Publication, error) {
		providerStrictOpens++
		return openProviderPublication(ctx, root, expected)
	}
	identity, err := provider.CandidateManifestIdentity(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if identity != state.pointer.ManifestDigest || providerStrictOpens != 0 {
		t.Fatalf(
			"pointer identity/strict opens = %q/%d",
			identity, providerStrictOpens,
		)
	}
	opened, err := provider.OpenCandidateManifest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if providerStrictOpens != 1 {
		t.Fatalf("provider strict opens = %d, want 1", providerStrictOpens)
	}
	if opened.Identity() != state.pointer.ManifestDigest ||
		opened.CorpusFileCount() != 3 {
		t.Fatalf(
			"manifest identity/count = %q/%d",
			opened.Identity(), opened.CorpusFileCount(),
		)
	}
	if boundaries := opened.GitlinkBoundaries(); boundaries.Count != 0 ||
		boundaries.Digest == "" || boundaries.SampleTruncated {
		t.Fatalf("gitlink boundaries = %+v", boundaries)
	}
	callerPlanOpens := 0
	openCallerPlan := provider.openCaller
	provider.openCaller = func(
		ctx context.Context,
		root string,
		expected candidate.Expected,
	) (*candidate.CallerPlan, error) {
		callerPlanOpens++
		return openCallerPlan(ctx, root, expected)
	}
	callerPlan, err := provider.OpenCandidateCallerPlan(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if callerPlanOpens != 1 || providerStrictOpens != 1 ||
		callerPlan.Identity() != state.pointer.ManifestDigest ||
		callerPlan.CandidateControlRevision() != state.pointer.ControlRevision {
		t.Fatalf(
			"caller/strict opens, identity, control = %d/%d/%q/%d",
			callerPlanOpens, providerStrictOpens, callerPlan.Identity(),
			callerPlan.CandidateControlRevision(),
		)
	}
	if domains := callerPlan.CallerDomains(); !slices.Equal(domains, []extract.CandidateManifestDomain{{
		Domain: "grpc-caller", Version: "caller-v1",
	}}) {
		t.Fatalf("caller-plan domains = %+v", domains)
	}
	leaves := callerPlan.CallerLeaves()
	if len(leaves) == 0 {
		t.Fatal("caller plan has no leaf descriptors")
	}
	var leafPaths []string
	for _, leaf := range leaves {
		if err := callerPlan.ForEachCallerLeafFile(
			ctx, "grpc-caller", "caller-v1", leaf,
			func(file extract.CandidateManifestFile) error {
				leafPaths = append(leafPaths, file.Path)
				return nil
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(leafPaths, []string{"client/use.go"}) {
		t.Fatalf("caller leaf replay = %v", leafPaths)
	}
	forgedLeaf := leaves[0]
	forgedLeaf.ContentBytes++
	if err := callerPlan.ForEachCallerLeafFile(
		ctx, "grpc-caller", "caller-v1", forgedLeaf,
		func(extract.CandidateManifestFile) error { return nil },
	); !errors.Is(err, candidate.ErrInvalidManifest) {
		t.Fatalf("forged caller leaf error = %v", err)
	}

	type seenFile struct {
		path     string
		required bool
		inUnit   bool
	}
	var protoFiles, callerFiles []seenFile
	if err := opened.ForEachRepositoryFile(
		ctx, "proto-contract", "proto-v1",
		func(file extract.CandidateManifestFile) error {
			protoFiles = append(protoFiles, seenFile{
				path: file.Path, required: file.Required, inUnit: file.InUnit,
			})
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := opened.ForEachRepositoryFile(
		ctx, "grpc-caller", "caller-v1",
		func(file extract.CandidateManifestFile) error {
			callerFiles = append(callerFiles, seenFile{
				path: file.Path, required: file.Required, inUnit: file.InUnit,
			})
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(protoFiles, []seenFile{{
		path: "service/api.proto", required: true, inUnit: true,
	}}) {
		t.Fatalf("proto files = %+v", protoFiles)
	}
	if !slices.Equal(callerFiles, []seenFile{{
		path: "client/use.go", required: true, inUnit: false,
	}}) {
		t.Fatalf("caller files = %+v", callerFiles)
	}
}

func TestCandidateOperationReportsRebuildAndPointerOnlyWarmNoop(t *testing.T) {
	t.Parallel()
	dataDir, repository, commit := candidateGitFixture(t)
	state := &manifestStore{repository: &store.Repo{
		Name: repository, IndexedCommitHash: commit,
	}}
	worker, _, err := New(dataDir, state, []extract.Extractor{
		policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		},
		policyExtractor{
			domain: "grpc-caller", version: "caller-v1",
			requiredSuffix: ".go",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var reports [][]byte
	worker.OperationReports = func(report []byte) error {
		reports = append(reports, slices.Clone(report))
		return nil
	}
	created := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	claimed := created.Add(9 * time.Second)
	job := store.Job{
		ID: "candidate_manifest_job:receipt", Kind: store.JobCandidate,
		Target: repository, CreatedAt: created, ClaimedAt: &claimed,
	}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(reports))
	}
	var rebuild, warm CandidateOperationReport
	if err := json.Unmarshal(reports[0], &rebuild); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reports[1], &warm); err != nil {
		t.Fatal(err)
	}
	if rebuild.Decision != "rebuild" || rebuild.Outcome != "done" ||
		!rebuild.ManifestSummaryPresent || rebuild.QueueWaitMS != 9000 ||
		rebuild.DeclaredSourceBytes <= 0 ||
		rebuild.Planes.Repository.Records == 0 ||
		rebuild.Planes.Caller.Records == 0 || rebuild.PeakSpoolBytes <= 0 ||
		rebuild.ControlRevision == 0 {
		t.Fatalf("rebuild receipt = %+v", rebuild)
	}
	if warm.Decision != "warm_noop" || warm.Outcome != "done" ||
		warm.ManifestSummaryPresent || warm.ControlRevision == 0 {
		t.Fatalf("warm receipt = %+v", warm)
	}
	for _, raw := range reports {
		for _, forbidden := range []string{
			"service/api.proto", "client/use.go", "notes.txt",
		} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("candidate receipt leaked path %q: %s", forbidden, raw)
			}
		}
	}
}

func TestExtractionProviderReplaysTwoFocusedDomainsWithoutRepositoryMembers(
	t *testing.T,
) {
	dataDir, repository, commit := candidateMultiLocalGitFixture(t)
	unit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	state := &manifestStore{repository: &store.Repo{
		Name: repository, IndexedCommitHash: commit,
		IndexedAnalysisUnit: unit,
	}}
	proto := &replayAuditExtractor{
		domain: "proto-contract", version: "proto-v1", suffix: ".proto",
	}
	goConsumer := &replayAuditExtractor{
		domain: "grpc-consumer", version: "grpc-v1", suffix: ".go",
	}
	extractors := []extract.Extractor{proto, goConsumer}
	planner, provider, err := New(dataDir, state, extractors)
	if err != nil {
		t.Fatal(err)
	}
	if err := planner.Handle(t.Context(), store.Job{
		Kind: store.JobCandidate, Target: repository,
	}); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(
		CandidateRoot(dataDir), candidate.ManifestName(repository),
	))
	if err != nil {
		t.Fatal(err)
	}
	var manifest candidate.Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Digest != state.pointer.ManifestDigest ||
		len(manifest.RepositoryMembers) == 0 ||
		len(manifest.LocalProjections) != 2 {
		t.Fatalf(
			"focused publication members/projections = %d/%d",
			len(manifest.RepositoryMembers), len(manifest.LocalProjections),
		)
	}
	for _, projection := range manifest.LocalProjections {
		if len(projection.Members) == 0 {
			t.Fatalf("local projection %q is unexpectedly empty", projection.Domain)
		}
	}

	strictOpens := 0
	removedRepositoryMembers := false
	openPublication := provider.open
	provider.open = func(
		ctx context.Context,
		root string,
		expected candidate.Expected,
	) (*candidate.Publication, error) {
		strictOpens++
		publication, openErr := openPublication(ctx, root, expected)
		if openErr != nil {
			return nil, openErr
		}
		if removedRepositoryMembers {
			return nil, errors.New("provider opened the publication more than once")
		}
		for _, member := range manifest.RepositoryMembers {
			source := filepath.Join(root, member.Name)
			blocked := source + ".replay-blocked"
			if err := os.Rename(source, blocked); err != nil {
				return nil, err
			}
			t.Cleanup(func() {
				if _, err := os.Lstat(blocked); err == nil {
					_ = os.Rename(blocked, source)
				}
			})
		}
		removedRepositoryMembers = true
		return publication, nil
	}

	evidence := newReplayAuditEvidence()
	reportClock := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)
	now := func() time.Time {
		current := reportClock
		reportClock = reportClock.Add(5 * time.Millisecond)
		return current
	}
	var operationRaw []byte
	extraction := &extract.Worker{
		Repos: state, Evidence: evidence,
		NewCorpus: extract.GitCorpus(dataDir),
		Manifests: provider, Extractors: extractors,
		Now: now,
		OperationReports: func(report []byte) error {
			operationRaw = slices.Clone(report)
			return nil
		},
	}
	if err := extraction.Handle(t.Context(), store.Job{
		Kind: store.JobExtract, Target: repository,
	}); err != nil {
		t.Fatal(err)
	}
	if strictOpens != 1 || !removedRepositoryMembers {
		t.Fatalf(
			"strict opens/repository removal = %d/%t, want 1/true",
			strictOpens, removedRepositoryMembers,
		)
	}
	var operation extract.ExtractionOperationReport
	if err := json.Unmarshal(operationRaw, &operation); err != nil {
		t.Fatalf("decode extraction operation: %v", err)
	}
	if operation.CandidateManifestDigest != state.pointer.ManifestDigest ||
		operation.PolicyDigest != provider.PolicyDigest() ||
		operation.StrictOpenMS != 5 ||
		operation.MirrorLockWaitMS != 5 ||
		len(operation.Domains) != 2 {
		t.Fatalf("extraction operation envelope = %+v", operation)
	}
	for _, domain := range operation.Domains {
		if domain.Reason != extract.OperationReasonPublishedEmpty ||
			domain.Counts.CandidateFiles != 1 ||
			domain.Counts.OpenedSourceFiles != 1 ||
			domain.Counts.OpenedSourceAttempts != 1 {
			t.Fatalf("extraction operation domain = %+v", domain)
		}
	}
	for _, forbidden := range []string{
		"service/api.proto", "service/consumer.go", "client/use.go",
	} {
		if strings.Contains(string(operationRaw), forbidden) {
			t.Fatalf(
				"extraction operation exposed source path %q: %s",
				forbidden, operationRaw,
			)
		}
	}
	if !slices.Equal(proto.paths, []string{"service/api.proto"}) {
		t.Fatalf("proto focused replay = %v", proto.paths)
	}
	if !slices.Equal(goConsumer.paths, []string{"service/consumer.go"}) {
		t.Fatalf("Go focused replay = %v", goConsumer.paths)
	}
	for _, domain := range []string{"proto-contract", "grpc-consumer"} {
		run := evidence.current[domain]
		if run == nil ||
			run.Coverage.CandidateManifestDigest != state.pointer.ManifestDigest ||
			run.Coverage.ScopePosture != "focused-local" {
			t.Fatalf("published %s coverage = %+v", domain, run)
		}
	}
}

func TestTamperTerminalOutcomeSameDigestStrictRepairRunsExtractionOnce(
	t *testing.T,
) {
	dataDir, repository, commit := candidateGitFixture(t)
	unit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	state := &manifestStore{repository: &store.Repo{
		Name: repository, IndexedCommitHash: commit,
		IndexedAnalysisUnit: unit,
	}}
	proto := &replayAuditExtractor{
		domain: "proto-contract", version: "proto-v1", suffix: ".proto",
	}
	planner, provider, err := New(
		dataDir, state, []extract.Extractor{proto},
	)
	if err != nil {
		t.Fatal(err)
	}
	candidateJob := store.Job{
		Kind: store.JobCandidate, Target: repository,
	}
	if err := planner.Handle(t.Context(), candidateJob); err != nil {
		t.Fatal(err)
	}
	initial := *state.pointer
	if initial.ControlRevision != 1 {
		t.Fatalf("initial control revision = %d", initial.ControlRevision)
	}
	manifestPath := filepath.Join(
		CandidateRoot(dataDir), candidate.ManifestName(repository),
	)
	file, err := os.OpenFile(manifestPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(" "); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	evidence := newReplayAuditEvidence()
	evidence.onOutcome = func(outcome store.ExtractionDomainOutcome) {
		if !outcome.CandidateControlFailure {
			return
		}
		state.mu.Lock()
		state.repairRevision =
			outcome.Generation.CandidateControlRevision
		state.mu.Unlock()
	}
	extraction := &extract.Worker{
		Repos: state, Evidence: evidence,
		NewCorpus:  extract.GitCorpus(dataDir),
		Manifests:  provider,
		Extractors: []extract.Extractor{proto},
	}
	extractionJob := store.Job{
		Kind: store.JobExtract, Target: repository,
	}
	if err := extraction.Handle(t.Context(), extractionJob); !store.IsTerminal(err) {
		t.Fatalf("tampered extraction error = %v, want terminal marker", err)
	}
	scope := store.ExtractionScope{
		Repository: repository, Commit: commit,
		UnitDigest: unit.Digest, Domain: proto.domain,
	}
	refusal, err := evidence.LatestExtractionDomainOutcome(
		t.Context(), scope,
	)
	if err != nil ||
		refusal.Disposition !=
			store.DomainOutcomeTerminalGenerationRefusal ||
		!refusal.CandidateControlFailure ||
		len(proto.paths) != 0 {
		t.Fatalf(
			"tamper refusal=%+v err=%v extractor paths=%v",
			refusal, err, proto.paths,
		)
	}
	if err := extraction.Handle(t.Context(), store.Job{
		Kind: store.JobExtract, Target: repository, Force: true,
	}); err != nil {
		t.Fatalf("forced terminal no-op: %v", err)
	}
	if len(proto.paths) != 0 {
		t.Fatalf("forced terminal outcome reran extractor: %v", proto.paths)
	}

	if err := planner.Handle(t.Context(), store.Job{
		Kind: store.JobCandidate, Target: repository, Force: true,
	}); err != nil {
		t.Fatalf("strict repair: %v", err)
	}
	repaired := *state.pointer
	if repaired.ManifestDigest != initial.ManifestDigest ||
		repaired.ControlRevision != initial.ControlRevision+1 {
		t.Fatalf(
			"same-digest repair pointer = %+v, want digest %s revision %d",
			repaired, initial.ManifestDigest, initial.ControlRevision+1,
		)
	}
	if err := extraction.Handle(t.Context(), extractionJob); err != nil {
		t.Fatalf("post-repair extraction: %v", err)
	}
	if !slices.Equal(proto.paths, []string{"service/api.proto"}) {
		t.Fatalf("post-repair extractor paths = %v", proto.paths)
	}
	published, err := evidence.LatestExtractionDomainOutcome(
		t.Context(), scope,
	)
	if err != nil ||
		published.Disposition != store.DomainOutcomePublished ||
		published.Generation.CandidateControlRevision !=
			repaired.ControlRevision {
		t.Fatalf("post-repair outcome = %+v, %v", published, err)
	}
}

func TestWorkerUpgradesLegacyV3PublicationAndCleansArtifacts(t *testing.T) {
	dataDir, repository, commit := candidateGitFixture(t)
	unit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	state := &manifestStore{repository: &store.Repo{
		Name: repository, IndexedCommitHash: commit,
		IndexedAnalysisUnit: unit,
	}}
	worker, _, err := New(dataDir, state, []extract.Extractor{
		policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		},
		policyExtractor{
			domain: "grpc-caller", version: "caller-v1",
			requiredSuffix: ".go",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{Kind: store.JobCandidate, Target: repository}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	initialState, err := pointerState(*state.pointer)
	if err != nil {
		t.Fatal(err)
	}
	initialPublication, err := candidate.OpenStateContext(
		t.Context(), CandidateRoot(dataDir), initialState,
	)
	if err != nil {
		t.Fatal(err)
	}
	initialManifest := initialPublication.Manifest()
	if initialManifest.Schema != candidate.ManifestSchema ||
		len(initialManifest.LocalProjections) != 1 {
		t.Fatalf("initial v4 manifest = %+v", initialManifest)
	}

	legacyPointer, legacyNames := installLegacyV3Publication(
		t, CandidateRoot(dataDir), initialManifest,
	)
	if len(legacyNames) < 2 ||
		legacyPointer.PolicyDigest == worker.policies.digest ||
		legacyPointer.GenerationDigest == initialState.GenerationDigest {
		t.Fatalf(
			"legacy pointer/names = %+v / %v",
			legacyPointer, legacyNames,
		)
	}
	legacyRaw, err := os.ReadFile(filepath.Join(
		CandidateRoot(dataDir), candidate.ManifestName(repository),
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		string(legacyRaw), `"schema":"phebs-candidate-manifest-v3"`,
	) || !strings.Contains(string(legacyRaw), `"local_projections"`) {
		t.Fatalf("legacy manifest is not v3-shaped: %s", legacyRaw)
	}
	state.mu.Lock()
	state.pointer = &legacyPointer
	state.mu.Unlock()

	strictUpgradeOpens := 0
	openPublication := worker.open
	worker.open = func(
		ctx context.Context,
		root string,
		expected candidate.Expected,
	) (*candidate.Publication, error) {
		strictUpgradeOpens++
		return openPublication(ctx, root, expected)
	}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if strictUpgradeOpens != 1 || state.publishCalls != 2 {
		t.Fatalf(
			"v3 upgrade strict opens/publishes = %d/%d, want 1/2",
			strictUpgradeOpens, state.publishCalls,
		)
	}
	state.mu.Lock()
	upgradedPointer := *state.pointer
	state.mu.Unlock()
	if upgradedPointer.PolicyDigest != worker.policies.digest ||
		upgradedPointer.GenerationDigest == legacyPointer.GenerationDigest ||
		upgradedPointer.ManifestDigest == legacyPointer.ManifestDigest {
		t.Fatalf(
			"v3 pointer was not replaced by v4: old=%+v new=%+v",
			legacyPointer, upgradedPointer,
		)
	}
	upgradedState, err := pointerState(upgradedPointer)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := candidate.OpenStateContext(
		t.Context(), CandidateRoot(dataDir), upgradedState,
	)
	if err != nil {
		t.Fatal(err)
	}
	upgradedManifest := upgraded.Manifest()
	if upgradedManifest.Schema != candidate.ManifestSchema ||
		len(upgradedManifest.LocalProjections) != 1 {
		t.Fatalf("upgraded manifest = %+v", upgradedManifest)
	}
	for _, name := range legacyNames {
		if _, err := os.Lstat(
			filepath.Join(CandidateRoot(dataDir), name),
		); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy v3 artifact %q remains: %v", name, err)
		}
	}
}

func TestWorkerSameHEADTypedIndexChangeRecoversNewPublication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir, repository, commit := candidateTypedGitFixture(t)
	scope := analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service/api.proto"},
		Supporting: []string{
			"service/a.scip",
			"service/b.scip",
		},
		TypedIndex: &analysisunit.TypedIndex{
			Kind: analysisunit.TypedIndexKindSCIP,
			Path: "service/a.scip",
		},
	}
	firstUnit, err := scope.State()
	if err != nil {
		t.Fatal(err)
	}
	scope.TypedIndex.Path = "service/b.scip"
	secondUnit, err := scope.State()
	if err != nil {
		t.Fatal(err)
	}
	if firstUnit.Digest != secondUnit.Digest ||
		analysisunit.EqualState(firstUnit, secondUnit) {
		t.Fatal("typed designation did not preserve semantic identity and change committed state")
	}

	state := &manifestStore{repository: &store.Repo{
		Name: repository, IndexedCommitHash: commit,
		IndexedAnalysisUnit: firstUnit,
	}}
	worker, provider, err := New(
		dataDir, state, []extract.Extractor{policyExtractor{
			domain: "scip-proto-field", version: "scip-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{Kind: store.JobCandidate, Target: repository}
	if err := worker.Handle(ctx, job); err != nil {
		t.Fatal(err)
	}
	firstPointer := *state.pointer
	firstRequest := extract.CandidateManifestRequest{
		Repository: repository, Commit: commit,
		AnalysisUnit: analysisunit.CloneState(firstUnit),
		Domains:      slices.Clone(provider.domains),
	}
	firstManifest, err := provider.OpenCandidateManifest(ctx, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	firstTyped, err := firstManifest.TypedInput(
		"scip-proto-field", "scip-v1", analysisunit.TypedIndexKindSCIP,
	)
	if err != nil || !firstTyped.Present ||
		firstTyped.Path != "service/a.scip" {
		t.Fatalf("first typed input = %+v, %v", firstTyped, err)
	}

	// SetRepoIndexedState clears the candidate pointer for this full-state
	// change even though the semantic unit digest and HEAD are unchanged.
	state.mu.Lock()
	state.repository.IndexedAnalysisUnit = analysisunit.CloneState(secondUnit)
	state.pointer = nil
	state.publishFailure = errors.New("injected typed-pointer failure")
	state.mu.Unlock()

	err = worker.Handle(ctx, job)
	if err == nil ||
		!strings.Contains(err.Error(), "injected typed-pointer failure") {
		t.Fatalf("first typed replacement = %v", err)
	}
	root := CandidateRoot(dataDir)
	if !candidate.IsPublishing(root, repository) {
		t.Fatal("failed typed replacement did not retain its publication marker")
	}
	if err := worker.Handle(ctx, job); err != nil {
		t.Fatalf("recover typed replacement: %v", err)
	}
	if candidate.IsPublishing(root, repository) {
		t.Fatal("recovered typed replacement left its publication marker")
	}

	state.mu.Lock()
	if state.pointer == nil {
		state.mu.Unlock()
		t.Fatal("typed replacement did not commit a pointer")
	}
	secondPointer := *state.pointer
	state.mu.Unlock()
	if secondPointer.HeadCommit != firstPointer.HeadCommit ||
		secondPointer.UnitDigest != firstPointer.UnitDigest ||
		secondPointer.GenerationDigest == firstPointer.GenerationDigest ||
		secondPointer.ManifestDigest == firstPointer.ManifestDigest {
		t.Fatalf(
			"typed publication identities = first %+v / second %+v",
			firstPointer, secondPointer,
		)
	}

	secondRequest := firstRequest
	secondRequest.AnalysisUnit = analysisunit.CloneState(secondUnit)
	secondManifest, err := provider.OpenCandidateManifest(ctx, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondTyped, err := secondManifest.TypedInput(
		"scip-proto-field", "scip-v1", analysisunit.TypedIndexKindSCIP,
	)
	if err != nil || !secondTyped.Present ||
		secondTyped.Path != "service/b.scip" ||
		secondTyped.ObjectID == firstTyped.ObjectID {
		t.Fatalf("second typed input = %+v, %v; first = %+v", secondTyped, err, firstTyped)
	}
	if _, err := provider.CandidateManifestIdentity(
		ctx, firstRequest,
	); err == nil {
		t.Fatal("old typed designation still satisfied the current pointer")
	}
}

func TestWorkerExactPointerReuseRepairsFanoutWithoutStrictOpen(
	t *testing.T,
) {
	t.Parallel()
	dataDir, repository, commit := candidateGitFixture(t)
	state := &manifestStore{
		repository: &store.Repo{
			Name: repository, IndexedCommitHash: commit,
		},
	}
	worker, _, err := New(
		dataDir, state, []extract.Extractor{policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{Kind: store.JobCandidate, Target: repository}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.fanoutCount = 0
	state.mu.Unlock()
	strictOpens := 0
	worker.open = func(
		context.Context,
		string,
		candidate.Expected,
	) (*candidate.Publication, error) {
		strictOpens++
		return nil, errors.New("unexpected strict publication open")
	}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if strictOpens != 0 || state.publishCalls != 2 ||
		state.fanoutCount != 1 {
		t.Fatalf(
			"steady-state strict opens/publishes/fanout = %d/%d/%d",
			strictOpens, state.publishCalls, state.fanoutCount,
		)
	}

	coldWorker, _, err := New(
		dataDir, state, []extract.Extractor{policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	coldStrictOpens := 0
	coldWorker.open = func(
		context.Context,
		string,
		candidate.Expected,
	) (*candidate.Publication, error) {
		coldStrictOpens++
		return nil, errors.New("unexpected cold strict publication open")
	}
	if err := coldWorker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if coldStrictOpens != 0 || state.publishCalls != 3 {
		t.Fatalf(
			"cold identity capture strict opens/publishes = %d/%d",
			coldStrictOpens, state.publishCalls,
		)
	}
}

func TestWorkerRepairsMemberChangedAfterControlFingerprint(
	t *testing.T,
) {
	t.Parallel()
	dataDir, repository, commit := candidateGitFixture(t)
	state := &manifestStore{
		repository: &store.Repo{
			Name: repository, IndexedCommitHash: commit,
		},
	}
	worker, _, err := New(
		dataDir, state, []extract.Extractor{policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{Kind: store.JobCandidate, Target: repository}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	persisted, err := pointerState(*state.pointer)
	if err != nil {
		t.Fatal(err)
	}
	root := CandidateRoot(dataDir)
	opened, err := candidate.OpenStateContext(
		t.Context(), root, persisted,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest := opened.Manifest()
	if len(manifest.RepositoryMembers) == 0 {
		t.Fatal("fixture has no repository member")
	}
	memberPath := filepath.Join(root, manifest.RepositoryMembers[0].Name)
	info, err := os.Lstat(memberPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(memberPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("fixture member is empty")
	}
	raw[len(raw)/2] ^= 0x01
	if err := os.WriteFile(memberPath, raw, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	changedTime := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(memberPath, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}

	strictOpens := 0
	openPublication := worker.open
	worker.open = func(
		ctx context.Context,
		root string,
		expected candidate.Expected,
	) (*candidate.Publication, error) {
		strictOpens++
		return openPublication(ctx, root, expected)
	}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if strictOpens != 1 || state.publishCalls != 2 {
		t.Fatalf(
			"repair strict opens/publishes = %d/%d, want 1/2",
			strictOpens, state.publishCalls,
		)
	}
	if _, err := candidate.OpenStateContext(
		t.Context(), root, persisted,
	); err != nil {
		t.Fatalf("rebuilt publication remains invalid: %v", err)
	}

	// The rebuild refreshes the process-local identities. The next retry is
	// again metadata-only and does not strictly consume member bytes.
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if strictOpens != 1 || state.publishCalls != 3 {
		t.Fatalf(
			"warm retry strict opens/publishes = %d/%d, want 1/3",
			strictOpens, state.publishCalls,
		)
	}
}

func TestWorkerMissingPointerControlRebuildsWithoutStrictReuseOpen(
	t *testing.T,
) {
	t.Parallel()
	dataDir, repository, commit := candidateGitFixture(t)
	state := &manifestStore{
		repository: &store.Repo{
			Name: repository, IndexedCommitHash: commit,
		},
	}
	worker, _, err := New(
		dataDir, state, []extract.Extractor{policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{Kind: store.JobCandidate, Target: repository}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(
		CandidateRoot(dataDir), candidate.ManifestName(repository),
	)
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	strictOpens := 0
	worker.open = func(
		context.Context,
		string,
		candidate.Expected,
	) (*candidate.Publication, error) {
		strictOpens++
		return nil, errors.New("unexpected strict reuse open")
	}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if strictOpens != 0 || state.publishCalls != 2 {
		t.Fatalf(
			"missing-control strict opens/publishes = %d/%d",
			strictOpens, state.publishCalls,
		)
	}
	if info, err := os.Lstat(manifestPath); err != nil ||
		!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("rebuilt manifest control = %+v, %v", info, err)
	}
}

func TestWorkerDoesNotAdoptUnmarkedFilesystemBytesWithoutValidPointer(
	t *testing.T,
) {
	t.Parallel()
	for _, testCase := range []struct {
		name           string
		invalidPointer bool
	}{
		{name: "missing pointer"},
		{name: "invalid pointer", invalidPointer: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dataDir, repository, commit := candidateGitFixture(t)
			state := &manifestStore{
				repository: &store.Repo{
					Name: repository, IndexedCommitHash: commit,
				},
			}
			if testCase.invalidPointer {
				state.pointer = &store.CandidateManifestPublication{
					Repository: repository,
				}
				state.pointerFailure = fmt.Errorf(
					"tampered pointer: %w",
					store.ErrInvalidCandidateManifestPublication,
				)
			}
			worker, provider, err := New(
				dataDir, state, []extract.Extractor{policyExtractor{
					domain: "proto-contract", version: "proto-v1",
					requiredSuffix: ".proto",
				}},
			)
			if err != nil {
				t.Fatal(err)
			}

			root := CandidateRoot(dataDir)
			if err := ensureCandidateRoot(root, true); err != nil {
				t.Fatal(err)
			}
			stage, err := candidate.NewStage(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(stage) }()
			forgedPolicies := slices.Clone(worker.policies.policies)
			for index := range forgedPolicies {
				forgedPolicies[index].Enumerate = func(string) bool {
					return false
				}
				forgedPolicies[index].Required = nil
			}
			forged, err := candidate.Build(t.Context(), candidate.Request{
				RepoDir:   reposync.RepoDir(dataDir, repository),
				OutputDir: stage, Repository: repository, Commit: commit,
				Policies: forgedPolicies,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(forged.Domains) != 1 ||
				forged.Domains[0].RepositoryCandidateCount != 0 {
				t.Fatalf("forged fixture retained candidates: %+v", forged.Domains)
			}
			expected, err := worker.expected(state.repository)
			if err != nil {
				t.Fatal(err)
			}
			expected.ManifestDigest = forged.Digest
			if _, err := candidate.PublishContext(
				t.Context(), root, stage, expected,
			); err != nil {
				t.Fatal(err)
			}
			if err := candidate.FinishPublication(root, repository); err != nil {
				t.Fatal(err)
			}
			if candidate.IsPublishing(root, repository) {
				t.Fatal("forged fixture unexpectedly retained a crash marker")
			}

			if err := worker.Handle(
				t.Context(),
				store.Job{Kind: store.JobCandidate, Target: repository},
			); err != nil {
				t.Fatal(err)
			}
			if state.pointer == nil ||
				state.pointer.ManifestDigest == forged.Digest {
				t.Fatalf(
					"unmarked filesystem generation was adopted: %+v",
					state.pointer,
				)
			}
			if testCase.invalidPointer && state.clearCalls != 1 {
				t.Fatalf("invalid pointer clear calls = %d", state.clearCalls)
			}

			opened, err := provider.OpenCandidateManifest(
				t.Context(), extract.CandidateManifestRequest{
					Repository: repository, Commit: commit,
					Domains: slices.Clone(provider.domains),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			files := 0
			if err := opened.ForEachRepositoryFile(
				t.Context(), "proto-contract", "proto-v1",
				func(file extract.CandidateManifestFile) error {
					files++
					if file.Path != "service/api.proto" {
						t.Fatalf("rebuilt candidate = %+v", file)
					}
					return nil
				},
			); err != nil {
				t.Fatal(err)
			}
			if files != 1 {
				t.Fatalf("rebuilt candidate count = %d", files)
			}
		})
	}
}

func TestWorkerRepairsOnlyTypedInvalidPublicationPointers(t *testing.T) {
	t.Parallel()
	extractor := policyExtractor{
		domain: "proto-contract", version: "proto-v1",
		requiredSuffix: ".proto",
	}

	t.Run("invalid derived pointer is cleared and rebuilt", func(t *testing.T) {
		dataDir, repository, commit := candidateGitFixture(t)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
			pointer: &store.CandidateManifestPublication{
				Repository: repository,
			},
			pointerFailure: fmt.Errorf(
				"tampered pointer: %w",
				store.ErrInvalidCandidateManifestPublication,
			),
		}
		worker, provider, err := New(
			dataDir, state, []extract.Extractor{extractor},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.Handle(
			t.Context(),
			store.Job{Kind: store.JobCandidate, Target: repository},
		); err != nil {
			t.Fatal(err)
		}
		if state.clearCalls != 1 || state.pointerFailure != nil ||
			state.pointer == nil || state.fanoutCount != 1 {
			t.Fatalf(
				"repair clear/error/pointer/fanout = %d/%v/%+v/%d",
				state.clearCalls, state.pointerFailure, state.pointer,
				state.fanoutCount,
			)
		}
		if _, err := provider.OpenCandidateManifest(
			t.Context(),
			extract.CandidateManifestRequest{
				Repository: repository, Commit: commit,
				Domains: slices.Clone(provider.domains),
			},
		); err != nil {
			t.Fatalf("open rebuilt publication: %v", err)
		}
	})

	t.Run("pointer store failure remains fail closed", func(t *testing.T) {
		dataDir, repository, commit := candidateGitFixture(t)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
			pointerFailure: errors.New("database unavailable"),
		}
		worker, _, err := New(
			dataDir, state, []extract.Extractor{extractor},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = worker.Handle(
			t.Context(),
			store.Job{Kind: store.JobCandidate, Target: repository},
		)
		if err == nil || !strings.Contains(err.Error(), "database unavailable") {
			t.Fatalf("pointer store failure = %v", err)
		}
		if state.clearCalls != 0 || state.publishCalls != 0 {
			t.Fatalf(
				"store failure clear/publish calls = %d/%d",
				state.clearCalls, state.publishCalls,
			)
		}
	})

	t.Run("invalid pointer clear failure remains fail closed", func(t *testing.T) {
		dataDir, repository, commit := candidateGitFixture(t)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
			pointerFailure: fmt.Errorf(
				"tampered pointer: %w",
				store.ErrInvalidCandidateManifestPublication,
			),
			clearFailure: errors.New("database unavailable"),
		}
		worker, _, err := New(
			dataDir, state, []extract.Extractor{extractor},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = worker.Handle(
			t.Context(),
			store.Job{Kind: store.JobCandidate, Target: repository},
		)
		if err == nil ||
			!strings.Contains(err.Error(), "clear invalid publication pointer") {
			t.Fatalf("invalid pointer clear failure = %v", err)
		}
		if state.clearCalls != 1 || state.publishCalls != 0 {
			t.Fatalf(
				"clear failure clear/publish calls = %d/%d",
				state.clearCalls, state.publishCalls,
			)
		}
	})
}

func TestWorkerPreservesFixedRootAndRequiredSymlinkPosture(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name, alias, target, domain, requiredSuffix, want string
	}{
		{
			name: "SCIP fixed root", alias: "index.scip",
			target: "target.go", domain: "scip-proto-field",
			requiredSuffix: "index.scip", want: "typed index",
		},
		{
			name: "attribution fixed root", alias: "layout-snapshot.json",
			target: "target.go", domain: "grpc-caller",
			requiredSuffix: ".go", want: "is forbidden",
		},
		{
			name: "required alias target", alias: "api.proto",
			target: "target.proto", domain: "proto-contract",
			requiredSuffix: "api.proto", want: "does not preserve required domain",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dataDir, repository, commit := candidateSymlinkGitFixture(
				t, testCase.alias, testCase.target,
			)
			state := &manifestStore{
				repository: &store.Repo{
					Name: repository, IndexedCommitHash: commit,
				},
			}
			worker, _, err := New(
				dataDir,
				state,
				[]extract.Extractor{policyExtractor{
					domain: testCase.domain, version: "test-v1",
					requiredSuffix: testCase.requiredSuffix,
				}},
			)
			if err != nil {
				t.Fatal(err)
			}
			err = worker.Handle(
				t.Context(),
				store.Job{Kind: store.JobCandidate, Target: repository},
			)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("symlink admission error = %v, want %q", err, testCase.want)
			}
			if state.publishCalls != 0 || state.pointer != nil ||
				state.fanoutCount != 0 {
				t.Fatalf(
					"refused symlink publish/pointer/fanout = %d/%+v/%d",
					state.publishCalls, state.pointer, state.fanoutCount,
				)
			}
		})
	}

	t.Run("enumeration-only broken symlink is skipped", func(t *testing.T) {
		dataDir, repository, commit := candidateSymlinkGitFixtureWithTarget(
			t, "broken.go", "missing.go", false,
		)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
		}
		worker, _, err := New(
			dataDir,
			state,
			[]extract.Extractor{policyExtractor{
				domain: "scip-proto-field", version: "test-v1",
				requiredSuffix: "index.scip",
			}},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.Handle(
			t.Context(),
			store.Job{Kind: store.JobCandidate, Target: repository},
		); err != nil {
			t.Fatalf("enumeration-only symlink: %v", err)
		}
		if state.publishCalls != 1 || state.pointer == nil ||
			state.fanoutCount != 1 {
			t.Fatalf(
				"enumeration-only publish/pointer/fanout = %d/%+v/%d",
				state.publishCalls, state.pointer, state.fanoutCount,
			)
		}
	})

	t.Run("one required alias blocks the shared generation", func(t *testing.T) {
		dataDir, repository, commit := candidateSymlinkGitFixture(
			t, "api.proto", "target.proto",
		)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
		}
		worker, _, err := New(
			dataDir,
			state,
			[]extract.Extractor{
				policyExtractor{
					domain: "proto-contract", version: "test-v1",
					requiredSuffix: "api.proto",
				},
				policyExtractor{
					domain: "kafka-producer", version: "test-v1",
					requiredSuffix: ".go",
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = worker.Handle(
			t.Context(),
			store.Job{Kind: store.JobCandidate, Target: repository},
		)
		if err == nil ||
			!strings.Contains(err.Error(), "does not preserve required domain") {
			t.Fatalf("shared-generation symlink error = %v", err)
		}
		if state.publishCalls != 0 || state.pointer != nil ||
			state.fanoutCount != 0 {
			t.Fatalf(
				"blocked shared generation publish/pointer/fanout = %d/%+v/%d",
				state.publishCalls, state.pointer, state.fanoutCount,
			)
		}
	})
}

func TestProviderRefusesPartialStaleMalformedAndTamperedPublications(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	dataDir, repository, commit := candidateGitFixture(t)
	state := &manifestStore{
		repository: &store.Repo{
			Name: repository, IndexedCommitHash: commit,
		},
	}
	worker, provider, err := New(
		dataDir,
		state,
		[]extract.Extractor{policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Handle(
		ctx, store.Job{Kind: store.JobCandidate, Target: repository},
	); err != nil {
		t.Fatal(err)
	}
	request := extract.CandidateManifestRequest{
		Repository: repository, Commit: commit,
		Domains: slices.Clone(provider.domains),
	}

	t.Run("partial domain request", func(t *testing.T) {
		before := state.getPointerCalls
		partial := request
		partial.Domains = nil
		if _, err := provider.OpenCandidateManifest(ctx, partial); err == nil {
			t.Fatal("partial domain request succeeded")
		}
		if state.getPointerCalls != before {
			t.Fatal("partial domain request reached the pointer store")
		}
	})

	t.Run("stale commit", func(t *testing.T) {
		stale := request
		stale.Commit = strings.Repeat("a", len(commit))
		if _, err := provider.OpenCandidateManifest(ctx, stale); err == nil ||
			!errors.Is(err, extract.ErrCandidateManifestStale) ||
			!strings.Contains(err.Error(), "does not match") {
			t.Fatalf("stale commit error = %v", err)
		}
	})

	t.Run("malformed pointer path", func(t *testing.T) {
		state.mu.Lock()
		original := *state.pointer
		state.pointer.ManifestPath = "nested/manifest.json"
		state.mu.Unlock()
		defer func() {
			state.mu.Lock()
			state.pointer = &original
			state.mu.Unlock()
		}()
		if _, err := provider.OpenCandidateManifest(ctx, request); err == nil ||
			!strings.Contains(err.Error(), "publication pointer") {
			t.Fatalf("malformed pointer error = %v", err)
		}
	})

	t.Run("stable publication marker", func(t *testing.T) {
		markerPath := filepath.Join(
			CandidateRoot(dataDir), candidate.PublishingName(repository),
		)
		if err := os.WriteFile(markerPath, []byte("publishing\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Remove(markerPath); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				t.Errorf("remove marker: %v", err)
			}
		}()
		before := state.getPointerCalls
		if _, err := provider.CandidateManifestIdentity(
			ctx, request,
		); !errors.Is(err, candidate.ErrPublishing) {
			t.Fatalf("marker identity error = %v", err)
		}
		if state.getPointerCalls != before {
			t.Fatal("marker-covered identity reached the pointer store")
		}
	})

	t.Run("tampered manifest bytes", func(t *testing.T) {
		manifestPath := filepath.Join(
			CandidateRoot(dataDir), candidate.ManifestName(repository),
		)
		file, err := os.OpenFile(manifestPath, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(" "); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := provider.OpenCandidateManifest(ctx, request); err == nil ||
			!errors.Is(err, candidate.ErrInvalidManifest) {
			t.Fatalf("tampered manifest error = %v", err)
		}
	})
}

func TestWorkerDistinguishesStaleAndDeterministicPublicationConflicts(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	extractor := policyExtractor{
		domain: "proto-contract", version: "proto-v1",
		requiredSuffix: ".proto",
	}

	t.Run("stale indexed generation is a no-op", func(t *testing.T) {
		dataDir, repository, commit := candidateGitFixture(t)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
		}
		state.beforePublish = func(current *manifestStore) {
			current.repository.IndexedCommitHash = strings.Repeat("b", len(commit))
		}
		worker, _, err := New(
			dataDir, state, []extract.Extractor{extractor},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.Handle(
			ctx, store.Job{Kind: store.JobCandidate, Target: repository},
		); err != nil {
			t.Fatalf("stale candidate job = %v", err)
		}
		if !candidate.IsPublishing(CandidateRoot(dataDir), repository) {
			t.Fatal("stale transition did not remain fail-closed for its successor")
		}
	})

	t.Run("same generation conflict fails loudly", func(t *testing.T) {
		dataDir, repository, commit := candidateGitFixture(t)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
		}
		worker, _, err := New(
			dataDir, state, []extract.Extractor{extractor},
		)
		if err != nil {
			t.Fatal(err)
		}
		job := store.Job{Kind: store.JobCandidate, Target: repository}
		if err := worker.Handle(ctx, job); err != nil {
			t.Fatal(err)
		}
		state.mu.Lock()
		state.pointer.ManifestDigest = "sha256:" + strings.Repeat("c", 64)
		state.mu.Unlock()

		job.Force = true
		err = worker.Handle(ctx, job)
		if err == nil || !errors.Is(err, store.ErrConflict) ||
			store.Classify(err) != store.ClassExtract ||
			!strings.Contains(err.Error(), "deterministic publication conflict") {
			t.Fatalf("deterministic conflict error = %v", err)
		}
	})
}

func TestWorkerNoOpsForMissingDeletingAndUnindexedRepositories(
	t *testing.T,
) {
	t.Parallel()
	dataDir := t.TempDir()
	repository := "example.com/acme/service"
	state := &manifestStore{}
	worker, _, err := New(
		dataDir,
		state,
		[]extract.Extractor{policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{Kind: store.JobCandidate, Target: repository}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatalf("missing repository: %v", err)
	}
	state.repository = &store.Repo{Name: repository, Deleting: true}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatalf("deleting repository: %v", err)
	}
	state.repository = &store.Repo{Name: repository}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatalf("unindexed repository: %v", err)
	}
	if _, err := os.Lstat(CandidateRoot(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no-op jobs created candidate root: %v", err)
	}
}

func candidateGitFixture(t *testing.T) (dataDir, repository, commit string) {
	t.Helper()
	dataDir = t.TempDir()
	repository = "example.com/acme/service"
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "init", work)
	runGit(t, work, "config", "user.email", "candidate@example.invalid")
	runGit(t, work, "config", "user.name", "Candidate Test")
	writeFixtureFile(t, work, "service/api.proto", "syntax = \"proto3\";\n")
	writeFixtureFile(t, work, "client/use.go", "package client\n")
	writeFixtureFile(t, work, "notes.txt", "not a candidate\n")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "fixture")
	commit = strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	repoDir := reposync.RepoDir(dataDir, repository)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "--bare", work, repoDir)
	return dataDir, repository, commit
}

func candidateMultiLocalGitFixture(
	t *testing.T,
) (dataDir, repository, commit string) {
	t.Helper()
	dataDir = t.TempDir()
	repository = "example.com/acme/multi-local"
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "init", work)
	runGit(t, work, "config", "user.email", "candidate@example.invalid")
	runGit(t, work, "config", "user.name", "Candidate Test")
	writeFixtureFile(t, work, "service/api.proto", "syntax = \"proto3\";\n")
	writeFixtureFile(t, work, "service/consumer.go", "package service\n")
	writeFixtureFile(t, work, "outside/api.proto", "syntax = \"proto3\";\n")
	writeFixtureFile(t, work, "outside/consumer.go", "package outside\n")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "multi-local fixture")
	commit = strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	repoDir := reposync.RepoDir(dataDir, repository)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "--bare", work, repoDir)
	return dataDir, repository, commit
}

func installLegacyV3Publication(
	t *testing.T,
	root string,
	current candidate.Manifest,
) (store.CandidateManifestPublication, []string) {
	t.Helper()
	partition := legacyV3FrozenPartitionPolicy()
	policyPayload, err := json.Marshal(struct {
		Schema    string                     `json:"schema"`
		Partition legacyV3PartitionPolicy    `json:"partition_policy"`
		Policies  []candidate.PolicyIdentity `json:"policies"`
	}{
		Schema:    "phebs-candidate-manifest-v3",
		Partition: partition,
		Policies:  current.Policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	policyDigest := legacyV3Digest(
		"phebs-candidate-policy-v1\x00", policyPayload,
	)
	generationPayload, err := json.Marshal(struct {
		Schema       string                         `json:"schema"`
		Repository   string                         `json:"repository"`
		Commit       string                         `json:"commit"`
		UnitDigest   string                         `json:"unit_digest"`
		PolicyDigest string                         `json:"policy_digest"`
		TypedIndex   *candidate.TypedIndexSelection `json:"typed_index,omitempty"`
	}{
		Schema:       "phebs-candidate-manifest-v3",
		Repository:   current.Repository,
		Commit:       current.Commit,
		UnitDigest:   current.UnitDigest,
		PolicyDigest: policyDigest,
		TypedIndex:   current.TypedIndex,
	})
	if err != nil {
		t.Fatal(err)
	}
	generationDigest := legacyV3Digest(
		"phebs-candidate-generation-v1\x00", generationPayload,
	)
	legacy := legacyV3Manifest{
		Schema:            "phebs-candidate-manifest-v3",
		Repository:        current.Repository,
		Commit:            current.Commit,
		UnitDigest:        current.UnitDigest,
		PolicyDigest:      policyDigest,
		GenerationDigest:  generationDigest,
		TypedIndex:        current.TypedIndex,
		PartitionPolicy:   partition,
		Policies:          slices.Clone(current.Policies),
		Corpus:            current.Corpus,
		UnitCorpus:        current.UnitCorpus,
		Domains:           slices.Clone(current.Domains),
		TypedInputs:       slices.Clone(current.TypedInputs),
		RepositoryMembers: slices.Clone(current.RepositoryMembers),
		LocalProjections:  slices.Clone(current.LocalProjections),
		CallerLeaves:      slices.Clone(current.CallerLeaves),
	}
	prefix := candidate.ArtifactBase(current.Repository) + "-" +
		strings.TrimPrefix(generationDigest, "sha256:") + "-"
	oldNames := make([]string, 0,
		len(legacy.RepositoryMembers)+len(legacy.CallerLeaves)+
			len(legacy.LocalProjections))
	for ordinal := range legacy.RepositoryMembers {
		member := &legacy.RepositoryMembers[ordinal]
		legacyName := prefix + fmt.Sprintf(
			"repository-%06d.ndjson", ordinal,
		)
		if err := os.Rename(
			filepath.Join(root, member.Name),
			filepath.Join(root, legacyName),
		); err != nil {
			t.Fatal(err)
		}
		member.Name = legacyName
		oldNames = append(oldNames, legacyName)
	}
	for projectionIndex := range legacy.LocalProjections {
		projection := &legacy.LocalProjections[projectionIndex]
		for ordinal := range projection.Members {
			member := &projection.Members[ordinal]
			legacyName := prefix + fmt.Sprintf(
				"local-%03d-%06d.ndjson",
				projection.PolicyOrdinal, ordinal,
			)
			if err := os.Rename(
				filepath.Join(root, member.Name),
				filepath.Join(root, legacyName),
			); err != nil {
				t.Fatal(err)
			}
			member.Name = legacyName
			oldNames = append(oldNames, legacyName)
		}
	}
	for ordinal := range legacy.CallerLeaves {
		leaf := &legacy.CallerLeaves[ordinal]
		legacyName := prefix + fmt.Sprintf(
			"caller-%06d.ndjson", ordinal,
		)
		if err := os.Rename(
			filepath.Join(root, leaf.Name),
			filepath.Join(root, legacyName),
		); err != nil {
			t.Fatal(err)
		}
		leaf.Name = legacyName
		oldNames = append(oldNames, legacyName)
	}
	manifestPayload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Digest = legacyV3Digest(
		"phebs-candidate-manifest-v1\x00", manifestPayload,
	)
	manifestPayload, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	manifestPayload = append(manifestPayload, '\n')
	if err := os.WriteFile(
		filepath.Join(root, candidate.ManifestName(current.Repository)),
		manifestPayload, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	return store.CandidateManifestPublication{
		Repository:       current.Repository,
		HeadCommit:       current.Commit,
		UnitDigest:       current.UnitDigest,
		PolicyDigest:     policyDigest,
		ManifestDigest:   legacy.Digest,
		GenerationDigest: generationDigest,
		ManifestPath:     candidate.ManifestName(current.Repository),
		PublishedAt:      time.Now().UTC(),
	}, oldNames
}

func candidateTypedGitFixture(t *testing.T) (dataDir, repository, commit string) {
	t.Helper()
	dataDir = t.TempDir()
	repository = "example.com/acme/typed-service"
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "init", work)
	runGit(t, work, "config", "user.email", "candidate@example.invalid")
	runGit(t, work, "config", "user.name", "Candidate Test")
	writeFixtureFile(t, work, "service/api.proto", "syntax = \"proto3\";\n")
	writeFixtureFile(t, work, "service/a.scip", "first typed input\n")
	writeFixtureFile(t, work, "service/b.scip", "second typed input\n")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "typed fixture")
	commit = strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	repoDir := reposync.RepoDir(dataDir, repository)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "--bare", work, repoDir)
	return dataDir, repository, commit
}

func candidateSymlinkGitFixture(
	t *testing.T,
	alias, target string,
) (dataDir, repository, commit string) {
	return candidateSymlinkGitFixtureWithTarget(
		t, alias, target, true,
	)
}

func candidateSymlinkGitFixtureWithTarget(
	t *testing.T,
	alias, target string,
	writeTarget bool,
) (dataDir, repository, commit string) {
	t.Helper()
	dataDir = t.TempDir()
	repository = "example.com/acme/symlink-service"
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "init", work)
	runGit(t, work, "config", "user.email", "candidate@example.invalid")
	runGit(t, work, "config", "user.name", "Candidate Test")
	if writeTarget {
		writeFixtureFile(t, work, target, "package target\n")
	}
	aliasPath := filepath.Join(work, filepath.FromSlash(alias))
	if err := os.MkdirAll(filepath.Dir(aliasPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, aliasPath); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "symlink fixture")
	commit = strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	repoDir := reposync.RepoDir(dataDir, repository)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "--bare", work, repoDir)
	return dataDir, repository, commit
}

func writeFixtureFile(t *testing.T, root, relative, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	if directory != "" {
		command.Dir = directory
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
