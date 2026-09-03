package t421

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/scipfield"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftgo"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t401"
)

const t421ProductionReplayRepository = "example.invalid/t421-combined"

type t421ProductionReplayFile struct {
	path    string
	content []byte
}

type t421ProductionReplayCountingCorpus struct {
	sdk.Corpus
	reads int
}

func (corpus *t421ProductionReplayCountingCorpus) Read(
	ctx context.Context,
	path string,
) (sdk.Blob, error) {
	result, err := corpus.Corpus.Read(ctx, path)
	if err == nil {
		corpus.reads++
	}
	return result, err
}

func (corpus *t421ProductionReplayCountingCorpus) ReadSCIPIndex(
	ctx context.Context,
) (sdk.SCIPInput, error) {
	return corpus.Corpus.(sdk.SCIPCorpus).ReadSCIPIndex(ctx)
}

func (corpus *t421ProductionReplayCountingCorpus) SCIPTypedPartition() bool {
	typed, ok := corpus.Corpus.(sdk.SCIPTypedPartition)
	return ok && typed.SCIPTypedPartition()
}

type t421ProductionReplayPointerStore struct {
	store.CandidateManifestPublicationStore
	pointer store.CandidateManifestPublication
}

func (state *t421ProductionReplayPointerStore) GetCandidateManifestPublication(
	ctx context.Context,
	repository string,
) (*store.CandidateManifestPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if state == nil || repository != state.pointer.Repository {
		return nil, store.ErrNotFound
	}
	result := state.pointer
	return &result, nil
}

type t421ProductionReplayEvidence struct {
	store.EvidenceStore
	mu      sync.Mutex
	chunks  map[string]store.EvidenceChunkAccounting
	runs    map[string]*t421ProductionReplayEvidenceRun
	current map[string]store.PartitionedAssertionAuthority
}

type t421ProductionReplayEvidenceRun struct {
	associations map[string]struct{}
	assertions   map[string]store.Assertion
	status       string
	sealed       bool
	active       bool
	publishedKey bool
	quarantined  bool
	authority    store.PartitionedAssertionAuthority
}

func newT421ProductionReplayEvidence() *t421ProductionReplayEvidence {
	return &t421ProductionReplayEvidence{
		chunks:  make(map[string]store.EvidenceChunkAccounting),
		runs:    make(map[string]*t421ProductionReplayEvidenceRun),
		current: make(map[string]store.PartitionedAssertionAuthority),
	}
}

func (evidence *t421ProductionReplayEvidence) AddEvidenceChunk(
	ctx context.Context,
	runID, chunkID string,
	factCount int,
	_ []store.EvidenceAtom,
	associations []store.SnapshotEvidence,
	assertions []store.Assertion,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if evidence == nil || runID == "" || chunkID == "" || factCount <= 0 {
		return errors.New("T42.1 replay evidence chunk is invalid")
	}
	key := runID + "\x00" + chunkID
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	if _, exists := evidence.chunks[key]; exists {
		return nil
	}
	run := evidence.runs[runID]
	if run == nil {
		run = &t421ProductionReplayEvidenceRun{
			associations: make(map[string]struct{}),
			assertions:   make(map[string]store.Assertion),
			status:       "staged",
		}
		evidence.runs[runID] = run
	}
	rows := int64(0)
	for _, association := range associations {
		association.RunID = runID
		association.ID = store.ComputeSnapshotEvidenceID(association)
		if _, exists := run.associations[association.ID]; !exists {
			run.associations[association.ID] = struct{}{}
			rows++
		}
	}
	references := int64(0)
	for _, assertion := range assertions {
		assertion.RunID = runID
		assertion.ID = store.ComputeAssertionID(assertion)
		prior, exists := run.assertions[assertion.ID]
		if !exists {
			assertion.Supporting = t421ProductionReplayUnion(nil, assertion.Supporting)
			assertion.Contradicting = t421ProductionReplayUnion(nil, assertion.Contradicting)
			run.assertions[assertion.ID] = assertion
			rows++
			references += int64(len(assertion.Supporting) + len(assertion.Contradicting))
			continue
		}
		if prior.Predicate != assertion.Predicate || prior.Subject != assertion.Subject ||
			prior.Object != assertion.Object || prior.Lineage != assertion.Lineage ||
			prior.Tier != assertion.Tier || prior.CodeRole != assertion.CodeRole ||
			prior.Detail != assertion.Detail || prior.Repo != assertion.Repo {
			return errors.New("T42.1 replay assertion identity conflict")
		}
		before := len(prior.Supporting) + len(prior.Contradicting)
		prior.Supporting = t421ProductionReplayUnion(prior.Supporting, assertion.Supporting)
		prior.Contradicting = t421ProductionReplayUnion(prior.Contradicting, assertion.Contradicting)
		references += int64(len(prior.Supporting) + len(prior.Contradicting) - before)
		run.assertions[assertion.ID] = prior
	}
	evidence.chunks[key] = store.EvidenceChunkAccounting{
		RunID: runID, ChunkID: chunkID,
		ContentDigest:  SHA256([]byte(key)),
		FactCount:      int64(factCount),
		RowDelta:       rows,
		ReferenceDelta: references,
	}
	return nil
}

func (evidence *t421ProductionReplayEvidence) GetEvidenceChunkAccounting(
	ctx context.Context,
	runID, chunkID string,
) (store.EvidenceChunkAccounting, error) {
	if err := ctx.Err(); err != nil {
		return store.EvidenceChunkAccounting{}, err
	}
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	receipt, exists := evidence.chunks[runID+"\x00"+chunkID]
	if !exists {
		return store.EvidenceChunkAccounting{}, store.ErrNotFound
	}
	return receipt, nil
}

func (evidence *t421ProductionReplayEvidence) ListAssertions(
	ctx context.Context,
	query store.AssertionQuery,
) ([]store.Assertion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	run := evidence.runs[query.RunID]
	if run == nil || run.status != "published" || !run.publishedKey || run.quarantined {
		return nil, nil
	}
	return t421ProductionReplayAssertions(run, query)
}

func (evidence *t421ProductionReplayEvidence) ListPartitionedAssertions(
	ctx context.Context,
	query store.AssertionQuery,
	authority store.PartitionedAssertionAuthority,
) ([]store.Assertion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	run := evidence.runs[query.RunID]
	if run == nil || run.status != "staged" || !run.sealed || run.active || run.publishedKey || run.quarantined ||
		authority.Repository != query.Repo || authority.RunID != query.RunID ||
		authority.RootDigest == "" || authority.PlanDigest == "" ||
		authority.CandidateManifestDigest == "" || authority.CandidatePolicyDigest == "" ||
		run.authority != authority || evidence.current[authority.Domain] != authority {
		if query.After != nil {
			return nil, store.ErrConflict
		}
		return nil, store.ErrNotFound
	}
	if query.After != nil && query.After.RunID != query.RunID {
		return nil, store.ErrConflict
	}
	return t421ProductionReplayAssertions(run, query)
}

func t421ProductionReplayAssertions(
	run *t421ProductionReplayEvidenceRun,
	query store.AssertionQuery,
) ([]store.Assertion, error) {
	result := make([]store.Assertion, 0, len(run.assertions))
	for _, assertion := range run.assertions {
		if query.Repo != "" && assertion.Repo != query.Repo ||
			query.Predicate != "" && assertion.Predicate != query.Predicate ||
			query.Subject != "" && assertion.Subject != query.Subject ||
			query.Object != "" && assertion.Object != query.Object ||
			query.ObjectPrefix != "" && !strings.HasPrefix(assertion.Object, query.ObjectPrefix) ||
			query.Lineage != "" && assertion.Lineage != query.Lineage {
			continue
		}
		if query.After != nil && t421ProductionReplayCompareAssertionCursor(assertion, *query.After) <= 0 {
			continue
		}
		assertion.Supporting = slices.Clone(assertion.Supporting)
		assertion.Contradicting = slices.Clone(assertion.Contradicting)
		result = append(result, assertion)
	}
	sort.Slice(result, func(i, j int) bool {
		return t421ProductionReplayCompareAssertions(result[i], result[j]) < 0
	})
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(result) > limit {
		if !query.AllowTruncate {
			return nil, store.ErrResultLimit
		}
		result = result[:min(len(result), limit+1)]
	}
	return result, nil
}

func TestProductionReplayAssertionVisibilityIsNotMorePermissiveThanStore(t *testing.T) {
	evidence := newT421ProductionReplayEvidence()
	authority := store.PartitionedAssertionAuthority{
		Repository: t421ProductionReplayRepository, Domain: "proto-contract", RunID: "partitioned-run",
		Commit: strings.Repeat("a", 40), PlanDigest: SHA256([]byte("plan")), RootDigest: SHA256([]byte("root")),
		CandidateManifestDigest: SHA256([]byte("candidate")), CandidatePolicyDigest: SHA256([]byte("policy")),
	}
	row := store.Assertion{ID: "assertion", Repo: authority.Repository, RunID: authority.RunID,
		Predicate: "DECLARES_OPERATION", Subject: "api.proto", Object: "API.Call"}
	run := &t421ProductionReplayEvidenceRun{
		status: "staged", sealed: true, authority: authority,
		assertions: map[string]store.Assertion{row.ID: row},
	}
	evidence.runs[authority.RunID] = run
	evidence.current[authority.Domain] = authority
	query := store.AssertionQuery{Repo: authority.Repository, RunID: authority.RunID, Predicate: row.Predicate, Limit: 1}
	if rows, err := evidence.ListAssertions(t.Context(), query); err != nil || len(rows) != 0 {
		t.Fatalf("legacy mock exposed staged rows: %+v, %v", rows, err)
	}
	if rows, err := evidence.ListPartitionedAssertions(t.Context(), query, authority); err != nil || len(rows) != 1 {
		t.Fatalf("native mock hid exact current rows: %+v, %v", rows, err)
	}
	run.sealed = false
	if _, err := evidence.ListPartitionedAssertions(t.Context(), query, authority); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("native mock exposed unsealed run: %v", err)
	}
	run.sealed = true
	delete(evidence.current, authority.Domain)
	if _, err := evidence.ListPartitionedAssertions(t.Context(), query, authority); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("native mock exposed absent root: %v", err)
	}
	query.After = &store.AssertionCursor{RunID: authority.RunID, ID: row.ID}
	if _, err := evidence.ListPartitionedAssertions(t.Context(), query, authority); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("native mock accepted superseded continuation: %v", err)
	}
	run.status = "published"
	query.After = nil
	if rows, err := evidence.ListAssertions(t.Context(), query); err != nil || len(rows) != 0 {
		t.Fatalf("legacy mock exposed a run without a publication key: %+v, %v", rows, err)
	}
	run.publishedKey = true
	if rows, err := evidence.ListAssertions(t.Context(), query); err != nil || len(rows) != 1 {
		t.Fatalf("legacy mock rejected published rows: %+v, %v", rows, err)
	}
}

func t421ProductionReplayUnion(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		seen[value] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func t421ProductionReplayCompareAssertions(left, right store.Assertion) int {
	return strings.Compare(
		left.Predicate+"\x00"+left.Subject+"\x00"+left.Object+"\x00"+left.ID+"\x00"+left.RunID,
		right.Predicate+"\x00"+right.Subject+"\x00"+right.Object+"\x00"+right.ID+"\x00"+right.RunID,
	)
}

func t421ProductionReplayCompareAssertionCursor(
	left store.Assertion,
	right store.AssertionCursor,
) int {
	return strings.Compare(
		left.Predicate+"\x00"+left.Subject+"\x00"+left.Object+"\x00"+left.ID+"\x00"+left.RunID,
		right.Predicate+"\x00"+right.Subject+"\x00"+right.Object+"\x00"+right.ID+"\x00"+right.RunID,
	)
}

func TestFrozenProductionCandidateExtractionResolverAndCallerReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	combined, err := BuildCombinedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := BuildIndependentOracle()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	repositoryDir, commit := t421ProductionReplayRepositoryFixture(t, ctx, dataDir)
	extractors := t421ProductionReplayExtractors()
	policies, err := extract.CandidatePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}

	candidateRoot := candidatejob.CandidateRoot(dataDir)
	if err := os.MkdirAll(candidateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	candidateStage := filepath.Join(dataDir, "candidate-stage")
	if err := os.Mkdir(candidateStage, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := candidate.Build(ctx, candidate.Request{
		RepoDir: repositoryDir, OutputDir: candidateStage,
		Repository: t421ProductionReplayRepository, Commit: commit,
		Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	expectedCandidate := candidate.Expected{
		Repository: t421ProductionReplayRepository, Commit: commit,
		Policies: identities, PolicyDigest: manifest.PolicyDigest,
		GenerationDigest: manifest.GenerationDigest, ManifestDigest: manifest.Digest,
	}
	candidateState, err := candidate.PublishContext(
		ctx, candidateRoot, candidateStage, expectedCandidate,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.FinishPublication(candidateRoot, t421ProductionReplayRepository); err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.OpenContext(ctx, candidateRoot, expectedCandidate)
	if err != nil {
		t.Fatal(err)
	}
	t421ProductionReplayCheckCandidate(t, manifest, combined.Profile.Pipeline)

	sparseDirectory := filepath.Join(dataDir, "candidate-sparse")
	if err := os.Mkdir(sparseDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	sparseRoot, err := candidate.BuildSparseRoot(ctx, sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := candidate.OpenSparse(
		ctx, sparseDirectory, candidateRoot, candidateState, sparseRoot.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	evidence := newT421ProductionReplayEvidence()
	executor := &extract.EvidencePartitionExecutor{Evidence: evidence, Extractors: extractors}
	versions := make(map[string]string, len(identities))
	for _, identity := range identities {
		versions[identity.Domain] = identity.Version
	}
	roots := make(map[string]candidate.DomainResultRoot, len(combined.Profile.Pipeline.ExtractionDomains))
	plans := make(map[string]candidate.DomainResultPlan, len(combined.Profile.Pipeline.ExtractionDomains))
	for _, expectedDomain := range combined.Profile.Pipeline.ExtractionDomains {
		domain, openErr := sparse.OpenDomain(ctx, expectedDomain.Domain, versions[expectedDomain.Domain])
		if openErr != nil {
			t.Fatalf("open sparse domain %s: %v", expectedDomain.Domain, openErr)
		}
		plan, planErr := extractionpublication.BuildReservedPlan(domain, candidate.DomainResultAuthority{
			SourceGenerationDigest:      SHA256([]byte("t421-production-source")),
			ObservationGenerationDigest: SHA256([]byte("t421-production-observation")),
			ExtractorVersion:            versions[expectedDomain.Domain],
			ExtractionPolicyDigest:      SHA256([]byte("t421-production-extraction-policy")),
		})
		if planErr != nil {
			t.Fatalf("build reserved plan %s: %v", expectedDomain.Domain, planErr)
		}
		plans[expectedDomain.Domain] = plan
		partitions := domain.Partitions()
		t421ProductionReplayCheckPlan(t, plan, partitions, expectedDomain)
		source := extractionpublication.GitSparseSource{
			DataDir: dataDir,
			OpenDomain: func(
				context.Context,
				candidate.DomainResultPlan,
			) (*candidate.SparseDomain, error) {
				return domain, nil
			},
		}
		results := make([]candidate.PartitionResult, 0, len(plan.Expected))
		actualTotals := candidate.DomainResultTotals{}
		for ordinal := range plan.Expected {
			lease, acquireErr := source.AcquirePartition(ctx, plan, ordinal)
			if acquireErr != nil {
				t.Fatalf("acquire %s partition %d: %v", expectedDomain.Domain, ordinal, acquireErr)
			}
			spec, executeErr := executor.ExecutePartition(
				ctx, plan, ordinal, lease, "t421-production-"+expectedDomain.Domain,
			)
			lease.Release()
			if executeErr != nil {
				debugLease, debugAcquireErr := source.AcquirePartition(ctx, plan, ordinal)
				if debugAcquireErr != nil {
					t.Fatalf("execute %s partition %d: %v (debug acquire: %v)", expectedDomain.Domain, ordinal, executeErr, debugAcquireErr)
				}
				var debugErr error
				var debugCoverage sdk.Coverage
				debugCorpus := &t421ProductionReplayCountingCorpus{Corpus: debugLease.Corpus()}
				debugFacts := 0
				debugUnresolvedFacts := 0
				var nonGo []string
				_ = domain.ReadPartition(ctx, ordinal, func(record candidate.Record) error {
					if !strings.HasSuffix(record.Path, ".go") {
						nonGo = append(nonGo, record.Path)
					}
					return nil
				})
				for _, extractor := range extractors {
					if extractor.Domain() == expectedDomain.Domain && extractor.Version() == plan.ExtractorVersion {
						debugCoverage, debugErr = extractor.Extract(ctx, debugCorpus, func(fact sdk.Fact) error {
							debugFacts++
							if fact.Assertion.Tier == store.TierUnresolved {
								debugUnresolvedFacts++
							}
							return nil
						})
						break
					}
				}
				debugLease.Release()
				t.Fatalf("execute %s partition %d: %v (chain: %s; direct extractor: %v; coverage: %+v; reads: %d/%d; facts: %d unresolved: %d; non-Go candidates: %v)", expectedDomain.Domain, ordinal, executeErr, t421ProductionReplayErrorChain(executeErr), debugErr, debugCoverage, debugCorpus.reads, lease.CandidateRecords(), debugFacts, debugUnresolvedFacts, nonGo)
			}
			result, resultErr := candidate.BuildPartitionResult(plan, ordinal, spec)
			if resultErr != nil {
				t.Fatalf("close %s partition %d: %v", expectedDomain.Domain, ordinal, resultErr)
			}
			results = append(results, result)
			t421ProductionReplayAddTotals(&actualTotals, spec.Totals)
			if spec.Totals != t421ProductionReplayTotals(expectedDomain.Partitions[ordinal].Expected) {
				t.Fatalf("%s partition %d totals = %+v, want %+v", expectedDomain.Domain, ordinal, spec.Totals, expectedDomain.Partitions[ordinal].Expected)
			}
		}
		root, rootErr := candidate.BuildDomainResultRoot(plan, results)
		if rootErr != nil {
			t.Fatalf("close %s root: %v", expectedDomain.Domain, rootErr)
		}
		if actualTotals != t421ProductionReplayTotals(expectedDomain.Expected) || root.Totals != actualTotals {
			t.Fatalf("%s production extraction totals = %+v / %+v, want %+v", expectedDomain.Domain, actualTotals, root.Totals, expectedDomain.Expected)
		}
		roots[expectedDomain.Domain] = root
		// Model the writer's partitioned-native visibility only after every
		// partition has sealed and the production constructor accepts the root.
		runID := "t421-production-" + expectedDomain.Domain
		run := evidence.runs[runID]
		if run == nil {
			run = &t421ProductionReplayEvidenceRun{status: "staged"}
			evidence.runs[runID] = run
		}
		run.sealed = true
		run.authority = store.PartitionedAssertionAuthority{
			Repository: plan.Repository, Domain: plan.Domain, RunID: runID,
			PlanDigest: plan.Digest, RootDigest: root.Digest,
			Commit: candidateState.Commit, UnitDigest: candidateState.UnitDigest,
			CandidateManifestDigest: plan.CandidateManifestDigest,
			CandidatePolicyDigest:   plan.CandidatePolicyDigest,
		}
		evidence.current[plan.Domain] = run.authority
	}

	policySet, err := candidatejob.CompilePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	pointer := store.CandidateManifestPublication{
		Repository: candidateState.Repository, HeadCommit: candidateState.Commit,
		UnitDigest: candidateState.UnitDigest, PolicyDigest: candidateState.PolicyDigest,
		ManifestDigest: candidateState.ManifestDigest, GenerationDigest: candidateState.GenerationDigest,
		ManifestPath: candidateState.Manifest, ControlRevision: 1, PublishedAt: time.Now().UTC(),
	}
	provider, err := candidatejob.NewProvider(
		dataDir, &t421ProductionReplayPointerStore{pointer: pointer}, policySet,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverRegistry, err := resolvermaterialize.NewRegistry(extractors)
	if err != nil {
		t.Fatal(err)
	}
	callerRegistry, err := callerexecute.NewRegistry(extractors)
	if err != nil {
		t.Fatal(err)
	}
	manifestView, err := provider.OpenCandidateManifest(ctx, extract.CandidateManifestRequest{
		Repository: t421ProductionReplayRepository, Commit: commit,
		Domains: resolverRegistry.CandidateDomains(),
	})
	if err != nil {
		t.Fatal(err)
	}
	callerPlan, err := provider.OpenCandidateCallerPlan(ctx, extract.CandidateManifestRequest{
		Repository: t421ProductionReplayRepository, Commit: commit,
		Domains: callerRegistry.CandidateDomains(),
	})
	if err != nil {
		t.Fatal(err)
	}

	protoPlan, protoRoot := plans["proto-contract"], roots["proto-contract"]
	declaration := resolvercatalog.DeclarationPublication{
		Domain: "proto-contract", RunID: "t421-production-proto-contract",
		GenerationDigest: protoRoot.Digest,
		AuthoritySchema:  store.PartitionedExtractionDomainSchema,
		PlanDigest:       protoPlan.Digest, RootDigest: protoRoot.Digest,
	}
	resolverIdentity, err := resolvercatalog.NewIdentity(
		t421ProductionReplayRepository, commit, "", manifestView.Identity(),
		[]resolvercatalog.DeclarationPublication{declaration}, resolverRegistry.Packs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverRoot := filepath.Join(dataDir, "resolver-catalogs")
	if err := os.Mkdir(resolverRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	var resolverBlobReads, resolverBlobBytes uint64
	readResolverBlob := func(ctx context.Context, dir, oid string, limit int64) ([]byte, error) {
		content, err := gitobj.ReadBlob(ctx, dir, oid, limit)
		if err != nil {
			return nil, err
		}
		contentBytes := uint64(len(content))
		if resolverBlobReads == ^uint64(0) || contentBytes > ^uint64(0)-resolverBlobBytes {
			return nil, errors.New("production resolver blob accounting overflowed")
		}
		resolverBlobReads++
		resolverBlobBytes += contentBytes
		return content, nil
	}
	preparedResolver, err := resolvermaterialize.Build(ctx, resolvermaterialize.BuildRequest{
		Root: resolverRoot, RepositoryDir: repositoryDir,
		Identity: resolverIdentity, Registry: resolverRegistry, Manifest: manifestView,
		Declarations: []resolvermaterialize.DeclarationInput{{
			Protocol: resolvermaterialize.ProtocolGRPC,
			Domain:   declaration.Domain, RunID: declaration.RunID,
			GenerationDigest: declaration.GenerationDigest,
			AuthoritySchema:  declaration.AuthoritySchema,
			PlanDigest:       declaration.PlanDigest, RootDigest: declaration.RootDigest,
			CandidatePolicyDigest: protoPlan.CandidatePolicyDigest,
		}},
		Assertions: evidence, ReadBlob: readResolverBlob,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolverState, err := preparedResolver.Publish(
		ctx, func(context.Context, resolvercatalog.State) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	_, resolverViews, err := resolvermaterialize.OpenCallerResolvers(
		ctx, resolverRoot, resolverState,
		[]resolvermaterialize.Protocol{
			resolvermaterialize.ProtocolGRPC,
			resolvermaterialize.ProtocolThrift,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	grpcResolver := resolverViews[resolvermaterialize.ProtocolGRPC]
	if got, want := uint64(len(grpcResolver.Descriptors())), combined.Profile.Pipeline.GeneratedDescriptors; got != want {
		t.Fatalf("production resolver descriptors = %d, want %d", got, want)
	}
	if resolverBlobReads != combined.Profile.Pipeline.ResolverBlobReadsPerBuild ||
		resolverBlobBytes != combined.Profile.Pipeline.ResolverBlobBytesPerBuild {
		t.Fatalf("production resolver blobs = %d reads/%d bytes, want %d/%d", resolverBlobReads,
			resolverBlobBytes, combined.Profile.Pipeline.ResolverBlobReadsPerBuild,
			combined.Profile.Pipeline.ResolverBlobBytesPerBuild)
	}

	repository := &store.Repo{Name: t421ProductionReplayRepository, IndexedCommitHash: commit}
	resolverPointer := t421ProductionReplayResolverPointer(resolverState)
	generation, err := callerexecute.GenerationIdentity(callerexecute.GenerationAuthority{
		Repository: repository, Candidate: &pointer, Resolver: &resolverPointer,
	}, callerRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if generation.ResolverManifestDigest != resolverPointer.AuthorityDigest ||
		generation.ResolverManifestDigest == resolverState.ManifestDigest {
		t.Fatalf("production caller resolver authority = %q, pointer %q, storage manifest %q",
			generation.ResolverManifestDigest, resolverPointer.AuthorityDigest, resolverState.ManifestDigest)
	}
	pairs, _, err := callerexecute.ExpectedPairs(callerPlan, generation, callerRegistry)
	if err != nil {
		t.Fatal(err)
	}
	wantedLeaves := make(map[string]CallerLeafProfile, len(oracle.ProductRelationships.CallerLeaves))
	for _, leaf := range oracle.ProductRelationships.CallerLeaves {
		wantedLeaves[leaf.Prefix] = leaf
	}
	wantedPairs := make(map[string]struct{}, 2*len(wantedLeaves))
	for _, domain := range []string{"grpc-caller", "thrift-caller"} {
		for prefix := range wantedLeaves {
			wantedPairs[domain+"\x00"+prefix] = struct{}{}
		}
	}
	if len(pairs) != len(wantedPairs) {
		t.Fatalf("production caller pairs = %d, want %d", len(pairs), len(wantedPairs))
	}
	callerRoot := filepath.Join(dataDir, "caller-leaves")
	if err := os.Mkdir(callerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	pairReceipts := make([]callerpublication.PairReceipt, 0, len(pairs))
	leafPublications := make(map[string]*callerleaf.Publication, len(pairs))
	seenPairs := make(map[string]struct{}, len(pairs))
	actualLeaves := 0
	var resolved, abstentions, records, canonicalBytes, callerSourceReads uint64
	for _, pair := range pairs {
		protocol := resolvermaterialize.Protocol(pair.Adapter.Protocol)
		resolverView := resolverViews[protocol]
		if resolverView == nil {
			t.Fatalf("production caller pair %s has no %s resolver", pair.Identity.Digest, protocol)
		}
		stage, stageErr := callerleaf.NewStage(callerRoot, generation, pair.Identity)
		if stageErr != nil {
			t.Fatal(stageErr)
		}
		if executeErr := callerexecute.ExecutePair(ctx, callerexecute.ExecuteRequest{
			RepositoryDir: repositoryDir, Plan: callerPlan, Pair: pair,
			Protocol: string(protocol), ResolverCatalogDigest: generation.ResolverManifestDigest,
			Resolver: resolverView.Resolver(), Stage: stage,
		}); executeErr != nil {
			_ = stage.Discard()
			t.Fatalf("execute %s caller leaf %s: %v", protocol, pair.Candidate.Prefix, executeErr)
		}
		prepared, sealErr := stage.Seal()
		if sealErr != nil {
			t.Fatalf("seal %s caller leaf %s: %v", protocol, pair.Candidate.Prefix, sealErr)
		}
		leafPublication, installErr := prepared.Install(ctx)
		if installErr != nil {
			t.Fatalf("install %s caller leaf %s: %v", protocol, pair.Candidate.Prefix, installErr)
		}
		receipt := leafPublication.Receipt()
		pairReceipts = append(pairReceipts, callerpublication.PairReceipt{
			Pair: pair.Identity, Receipt: receipt,
		})
		leafPublications[pair.Identity.Digest] = leafPublication
		pairKey := pair.Adapter.Domain + "\x00" + pair.Candidate.Prefix
		if _, wanted := wantedPairs[pairKey]; !wanted {
			t.Fatalf("unexpected production caller pair %q", pairKey)
		}
		if _, duplicate := seenPairs[pairKey]; duplicate {
			t.Fatalf("duplicate production caller pair %q", pairKey)
		}
		seenPairs[pairKey] = struct{}{}
		if pair.Adapter.Domain != "grpc-caller" {
			wanted := wantedLeaves[pair.Candidate.Prefix]
			if uint64(pair.Candidate.RecordCount) != wanted.CandidateRecords ||
				receipt.ResultCount != 0 || receipt.AbstentionCount != 0 || receipt.RecordCount != 1 ||
				receipt.CoverageRecordCount != 1 || receipt.CoveredCandidateCount != pair.Candidate.RecordCount ||
				receipt.CoverageReason != callerleaf.CoverageReasonNoResolverDescriptors ||
				receipt.SourceBlobReads != 0 {
				t.Fatalf("thrift caller leaf %s = candidates %d receipt %+v", pair.Candidate.Prefix, pair.Candidate.RecordCount, receipt)
			}
			continue
		}
		wanted, exists := wantedLeaves[pair.Candidate.Prefix]
		if !exists {
			t.Fatalf("unexpected grpc caller leaf %q", pair.Candidate.Prefix)
		}
		if uint64(pair.Candidate.RecordCount) != wanted.CandidateRecords ||
			uint64(receipt.ResultCount) != wanted.ResolvedPostings ||
			uint64(receipt.AbstentionCount) != wanted.Abstentions ||
			uint64(receipt.RecordCount) != wanted.Records ||
			uint64(receipt.ContentBytes) != wanted.CanonicalBytes ||
			uint64(receipt.StagingBytes) != wanted.EncodedBytes {
			t.Fatalf("grpc caller leaf %s = candidates %d receipt %+v, want %+v", pair.Candidate.Prefix, pair.Candidate.RecordCount, receipt, wanted)
		}
		actualLeaves++
		callerSourceReads += uint64(receipt.SourceBlobReads)
		resolved += uint64(receipt.ResultCount)
		abstentions += uint64(receipt.AbstentionCount)
		records += uint64(receipt.RecordCount)
		canonicalBytes += uint64(receipt.ContentBytes)
	}
	if len(seenPairs) != len(wantedPairs) || actualLeaves != len(wantedLeaves) ||
		callerSourceReads != 11_601 || resolved != oracle.ProductRelationships.RPCProjections || abstentions != 11_603 ||
		records != 22_602 || canonicalBytes != 21_656_043 {
		t.Fatalf("production grpc caller totals = leaves %d source reads %d resolved %d abstentions %d records %d bytes %d", actualLeaves, callerSourceReads, resolved, abstentions, records, canonicalBytes)
	}
	completeManifest, err := callerpublication.BuildManifest(generation, pairReceipts)
	if err != nil {
		t.Fatal(err)
	}
	preparedPublication, err := callerpublication.PrepareWithValidated(
		ctx, callerRoot, completeManifest, leafPublications,
	)
	if err != nil {
		t.Fatal(err)
	}
	completePublication, err := preparedPublication.Publish(
		ctx, func(context.Context, callerpublication.State) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	completeState := completeManifest.State()
	reopened, err := callerpublication.Open(ctx, callerRoot, completeState)
	if err != nil {
		t.Fatal(err)
	}
	current, err := reopened.CurrentResult()
	if err != nil {
		t.Fatal(err)
	}
	reopenedManifest := reopened.Manifest()
	if !current || !completePublication.Current() ||
		reopened.State().ManifestDigest != completeState.ManifestDigest ||
		reopened.State().PairSetDigest != completeState.PairSetDigest ||
		reopenedManifest.Digest != completeManifest.Digest ||
		reopenedManifest.Aggregate != completeManifest.Aggregate ||
		len(reopenedManifest.Pairs) != len(pairs) ||
		reopenedManifest.Aggregate.PairCount != len(pairs) ||
		reopenedManifest.Aggregate.ArtifactCount != len(pairs) {
		t.Fatalf("complete production caller publication is not current and exact: state=%+v manifest=%+v", reopened.State(), reopenedManifest)
	}
	if err := callerpublication.ValidateManifest(reopenedManifest); err != nil {
		t.Fatal(err)
	}
}

func t421ProductionReplayErrorChain(err error) string {
	values := make([]string, 0, 4)
	for current := err; current != nil; current = errors.Unwrap(current) {
		values = append(values, fmt.Sprintf("%T: %v", current, current))
	}
	return strings.Join(values, " <- ")
}

func t421ProductionReplayExtractors() []extract.Extractor {
	return []extract.Extractor{
		gocaller.NewGRPC(), grpcgo.New(), kafkago.NewConsumer(), kafkago.NewProducer(),
		protodecl.New(), scipfield.New(), gocaller.NewThrift(), thriftgo.New(), thriftdecl.New(),
	}
}

func t421ProductionReplayCheckCandidate(
	t *testing.T,
	manifest candidate.Manifest,
	expected PipelineProfile,
) {
	t.Helper()
	if got := uint64(len(manifest.RepositoryMembers)); got != expected.CandidateRepositoryMembers {
		t.Fatalf("production candidate repository members = %d, want %d", got, expected.CandidateRepositoryMembers)
	}
	if got := uint64(len(manifest.CallerLeaves)); got != expected.CandidateCallerLeaves {
		t.Fatalf("production candidate caller leaves = %d, want %d", got, expected.CandidateCallerLeaves)
	}
	profiles := make(map[string]ExtractionDomainProfile, len(expected.ExtractionDomains))
	for _, domain := range expected.ExtractionDomains {
		profiles[domain.Domain] = domain
	}
	for _, domain := range manifest.Domains {
		profile, exists := profiles[domain.Domain]
		if !exists {
			t.Fatalf("unexpected production candidate domain %q", domain.Domain)
		}
		if uint64(domain.RepositoryCandidateCount) != profile.CandidateRecords {
			t.Fatalf("production candidate %s records = %d, want %d", domain.Domain, domain.RepositoryCandidateCount, profile.CandidateRecords)
		}
		delete(profiles, domain.Domain)
	}
	if len(profiles) != 0 {
		t.Fatalf("production candidate omitted domains: %v", profiles)
	}
}

func t421ProductionReplayCheckPlan(
	t *testing.T,
	plan candidate.DomainResultPlan,
	partitions []candidate.ExtractionPartition,
	expected ExtractionDomainProfile,
) {
	t.Helper()
	if plan.Schema != expected.ResultPlanSchema || plan.Availability != expected.Availability ||
		len(partitions) != len(expected.Partitions) || len(plan.Expected) != len(expected.Partitions) ||
		plan.Reserved != t421ProductionReplayTotals(expected.Reserved) {
		t.Fatalf("production %s plan = schema %q availability %q partitions %d reserved %+v, want %+v", expected.Domain, plan.Schema, plan.Availability, len(partitions), plan.Reserved, expected)
	}
	for index, partition := range partitions {
		want := expected.Partitions[index]
		memberOrdinal := int64(-1)
		if partition.Member != nil {
			memberOrdinal = int64(partition.Member.Ordinal)
		}
		if uint64(partition.Ordinal) != want.Ordinal || partition.Kind != want.Kind ||
			memberOrdinal != want.MemberOrdinal || partition.CallerPrefix != want.CallerPrefix ||
			uint64(partition.SourceStart) != want.SourceStart || uint64(partition.SourceEnd) != want.SourceEnd ||
			uint64(partition.MemberRecordStart) != want.MemberRecordStart || uint64(partition.MemberRecordEnd) != want.MemberRecordEnd ||
			uint64(partition.AdmittedRecords) != want.AdmittedRecords ||
			plan.Expected[index].Reservation != t421ProductionReplayTotals(want.Reservation) {
			t.Fatalf("production %s partition %d = %+v / %+v, want %+v", expected.Domain, index, partition, plan.Expected[index], want)
		}
	}
}

func t421ProductionReplayTotals(value ResultTotals) candidate.DomainResultTotals {
	return candidate.DomainResultTotals{
		Facts: value.Facts, Rows: value.Rows, References: value.References,
		CanonicalBytes: value.CanonicalBytes, EncodedBytes: value.EncodedBytes,
		MemberBytes: value.MemberBytes, Members: value.Members,
	}
}

func t421ProductionReplayAddTotals(
	total *candidate.DomainResultTotals,
	value candidate.DomainResultTotals,
) {
	total.Facts += value.Facts
	total.Rows += value.Rows
	total.References += value.References
	total.CanonicalBytes += value.CanonicalBytes
	total.EncodedBytes += value.EncodedBytes
	total.MemberBytes += value.MemberBytes
	total.Members += value.Members
}

func t421ProductionReplayResolverPointer(
	current resolvercatalog.State,
) store.ResolverCatalogPublication {
	declarations := make([]store.ResolverCatalogDeclarationPublication, len(current.Declarations))
	for index, declaration := range current.Declarations {
		declarations[index] = store.ResolverCatalogDeclarationPublication{
			Domain: declaration.Domain, RunID: declaration.RunID,
			GenerationDigest: declaration.GenerationDigest,
			AuthoritySchema:  declaration.AuthoritySchema,
			PlanDigest:       declaration.PlanDigest, RootDigest: declaration.RootDigest,
		}
	}
	packs := make([]store.ResolverCatalogPack, len(current.ResolverPacks))
	for index, pack := range current.ResolverPacks {
		packs[index] = store.ResolverCatalogPack{Name: pack.Name, Version: pack.Version}
	}
	return store.ResolverCatalogPublication{
		Repository: current.Repository, HeadCommit: current.Commit,
		UnitDigest: current.UnitDigest, Declarations: declarations,
		DeclarationSetDigest:    current.DeclarationSetDigest,
		CandidateManifestDigest: current.CandidateManifestDigest,
		SourceLanePolicy:        current.SourceLanePolicy, ResolverPacks: packs,
		ResolverPackSetDigest: current.ResolverPackSetDigest,
		CatalogPolicyDigest:   current.CatalogPolicyDigest,
		GenerationDigest:      current.GenerationDigest,
		ManifestDigest:        current.ManifestDigest, AuthorityDigest: current.AuthorityDigest,
		ManifestPath: current.Manifest,
	}
}

func t421ProductionReplayRepositoryFixture(
	t *testing.T,
	ctx context.Context,
	dataDir string,
) (string, string) {
	t.Helper()
	structural, err := frozenStructuralProfile()
	if err != nil {
		t.Fatal(err)
	}
	structuralPath, structuralContent, err := t401.FrozenStructuralGoFixture(structural, 0)
	if err != nil {
		t.Fatal(err)
	}
	goModPath, goModContent, err := t401.FrozenCallerControlFixture(structural)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]t421ProductionReplayFile, 0, 31_604)
	if err := WalkCombinedAdditions(func(path string, content []byte) error {
		files = append(files, t421ProductionReplayFile{path: path, content: slices.Clone(content)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	files = append(files,
		t421ProductionReplayFile{path: structuralPath, content: structuralContent},
		t421ProductionReplayFile{path: goModPath, content: goModContent},
	)
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	for index := 1; index < len(files); index++ {
		if files[index-1].path == files[index].path {
			t.Fatalf("T42.1 production replay fixture duplicates %q", files[index].path)
		}
	}
	repositoryDir := filepath.Join(dataDir, "repos", "example.invalid", "t421-combined.git")
	if err := os.MkdirAll(filepath.Dir(repositoryDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, commandErr := t421ProductionReplayGit(ctx, "init", "--bare", "--quiet", repositoryDir).CombinedOutput(); commandErr != nil {
		t.Fatalf("initialize production replay repository: %v: %s", commandErr, output)
	}
	command := t421ProductionReplayGit(ctx, "--git-dir", repositoryDir, "fast-import", "--quiet")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	writer := bufio.NewWriterSize(stdin, 1<<20)
	writeErr := error(nil)
	for index, file := range files {
		if _, err := fmt.Fprintf(writer, "blob\nmark :%d\ndata %d\n", index+1, len(file.content)); err != nil {
			writeErr = err
			break
		}
		if _, err := writer.Write(file.content); err != nil {
			writeErr = err
			break
		}
		if err := writer.WriteByte('\n'); err != nil {
			writeErr = err
			break
		}
	}
	if writeErr == nil {
		message := "T42.1 production candidate replay\n"
		_, writeErr = fmt.Fprintf(
			writer,
			"commit refs/heads/main\ncommitter T421 Replay <t421@example.invalid> 0 +0000\ndata %d\n%s",
			len(message), message,
		)
	}
	if writeErr == nil {
		for index, file := range files {
			if _, err := fmt.Fprintf(writer, "M 100644 :%d %q\n", index+1, file.path); err != nil {
				writeErr = err
				break
			}
		}
	}
	if writeErr == nil {
		_, writeErr = writer.WriteString("\ndone\n")
	}
	if flushErr := writer.Flush(); writeErr == nil {
		writeErr = flushErr
	}
	if closeErr := stdin.Close(); writeErr == nil {
		writeErr = closeErr
	}
	waitErr := command.Wait()
	if writeErr != nil || waitErr != nil {
		t.Fatalf("author production replay repository: write=%v wait=%v: %s", writeErr, waitErr, stderr.String())
	}
	output, err := t421ProductionReplayGit(
		ctx, "--git-dir", repositoryDir, "rev-parse", "refs/heads/main",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(string(output))
	if !gitobj.IsObjectID(commit) {
		t.Fatalf("production replay commit is invalid: %q", commit)
	}
	return repositoryDir, commit
}

func t421ProductionReplayGit(ctx context.Context, arguments ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "git", arguments...)
	environment := make([]string, 0, len(os.Environ())+3)
	for _, variable := range os.Environ() {
		if strings.HasPrefix(variable, "GIT_") || strings.HasPrefix(variable, "LC_ALL=") {
			continue
		}
		environment = append(environment, variable)
	}
	command.Env = append(environment,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C",
	)
	return command
}
