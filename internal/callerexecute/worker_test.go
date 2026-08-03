package callerexecute

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/store"
)

type workerTestPlan struct {
	digest  string
	control uint64
	domains []extract.CandidateManifestDomain
	leaves  []extract.CandidateCallerLeaf
	files   map[string][]extract.CandidateManifestFile
}

func (plan *workerTestPlan) Identity() string { return plan.digest }
func (plan *workerTestPlan) CandidateControlRevision() uint64 {
	return plan.control
}
func (plan *workerTestPlan) CallerDomains() []extract.CandidateManifestDomain {
	return slices.Clone(plan.domains)
}
func (plan *workerTestPlan) CallerLeaves() []extract.CandidateCallerLeaf {
	return slices.Clone(plan.leaves)
}
func (plan *workerTestPlan) ForEachCallerLeafFile(
	ctx context.Context,
	domain, version string,
	leaf extract.CandidateCallerLeaf,
	visit func(extract.CandidateManifestFile) error,
) error {
	if len(plan.domains) != 1 || plan.domains[0].Domain != domain ||
		plan.domains[0].Version != version || !slices.Contains(plan.leaves, leaf) {
		return errors.New("unexpected caller test pair")
	}
	for _, file := range plan.files[fmt.Sprintf("%s/%d", domain, leaf.Ordinal)] {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(file); err != nil {
			return err
		}
	}
	return nil
}

type workerTestProvider struct {
	plan  *workerTestPlan
	opens int
}

func (provider *workerTestProvider) OpenCandidateCallerPlan(
	context.Context,
	extract.CandidateManifestRequest,
) (extract.CandidateCallerPlan, error) {
	provider.opens++
	return provider.plan, nil
}

type workerTestStore struct {
	repo      store.Repo
	candidate store.CandidateManifestPublication
	resolver  store.ResolverCatalogPublication

	outcomes    []store.CallerLeafOutcome
	admission   *store.CallerGenerationAdmission
	publication *store.CallerGenerationPublication
	events      []string

	recordFailures        int
	admissionFailures     int
	publicationFailures   int
	publicationLostAck    bool
	summaryPayloadInvalid bool
	clearFailures         int
	ensureErr             error
}

func (state *workerTestStore) GetRepo(context.Context, string) (*store.Repo, error) {
	value := state.repo
	return &value, nil
}
func (state *workerTestStore) GetCandidateManifestPublication(
	context.Context,
	string,
) (*store.CandidateManifestPublication, error) {
	value := state.candidate
	return &value, nil
}
func (state *workerTestStore) GetResolverCatalogPublication(
	context.Context,
	string,
) (*store.ResolverCatalogPublication, error) {
	value := state.resolver
	return &value, nil
}
func (*workerTestStore) ResolverCatalogPublicationCurrent(
	context.Context,
	store.ResolverCatalogPublication,
) (bool, error) {
	return true, nil
}
func (state *workerTestStore) EnsureJobSuccessor(
	_ context.Context,
	job store.Job,
	force bool,
) (*store.Job, error) {
	if job.Kind != store.JobCallerLeaf || job.Target != state.repo.Name {
		return nil, errors.New("unexpected caller enqueue")
	}
	state.events = append(state.events, fmt.Sprintf("enqueue:%t", force))
	successor := &store.Job{Kind: job.Kind, Target: job.Target, Force: force}
	if state.ensureErr != nil {
		return successor, state.ensureErr
	}
	return successor, nil
}
func (state *workerTestStore) RecordCallerLeafOutcome(
	_ context.Context,
	_ store.Job,
	outcome store.CallerLeafOutcome,
) error {
	if state.recordFailures > 0 {
		state.recordFailures--
		state.events = append(state.events, "record-error")
		return errors.New("injected outcome write failure")
	}
	state.events = append(state.events, "record:"+string(outcome.Disposition))
	state.outcomes = append(state.outcomes, outcome)
	return nil
}
func (state *workerTestStore) GetCallerLeafOutcome(
	_ context.Context,
	generation store.CallerGenerationIdentity,
	pair store.CallerLeafPair,
) (*store.CallerLeafOutcome, error) {
	for index := range state.outcomes {
		if state.outcomes[index].Generation.Digest == generation.Digest &&
			state.outcomes[index].Pair.PairDigest == pair.PairDigest {
			value := state.outcomes[index]
			return &value, nil
		}
	}
	return nil, fmt.Errorf("caller outcome: %w", store.ErrNotFound)
}
func (state *workerTestStore) ListCallerLeafOutcomes(
	context.Context,
	store.CallerGenerationIdentity,
) ([]store.CallerLeafOutcome, error) {
	return slices.Clone(state.outcomes), nil
}
func (state *workerTestStore) RecordCallerGenerationAdmission(
	_ context.Context,
	_ store.Job,
	admission store.CallerGenerationAdmission,
	pairs []store.CallerLeafPair,
) error {
	if state.admissionFailures > 0 {
		state.admissionFailures--
		state.events = append(state.events, "admission-error")
		return errors.New("injected admission write failure")
	}
	admission.PairSetDigest, _ = store.ComputeCallerLeafPairSetDigest(admission.Generation, pairs)
	admission.PairCount = len(pairs)
	for _, outcome := range state.outcomes {
		if outcome.Disposition != store.CallerLeafSucceeded || outcome.Receipt == nil {
			continue
		}
		admission.ArtifactCount += outcome.Receipt.ArtifactCount
		admission.ResultCount += outcome.Receipt.ResultCount
		admission.AbstentionCount += outcome.Receipt.AbstentionCount
		admission.CanonicalBytes += outcome.Receipt.CanonicalBytes
		admission.StagingBytes += outcome.Receipt.StagingBytes
	}
	admission.WriterSchema = store.CallerLeafWriterSchema
	state.events = append(state.events, "admission:"+string(admission.Disposition))
	state.admission = &admission
	return nil
}
func (state *workerTestStore) GetCallerGenerationAdmission(
	context.Context,
	store.CallerGenerationIdentity,
) (*store.CallerGenerationAdmission, error) {
	if state.admission == nil {
		return nil, fmt.Errorf("caller admission: %w", store.ErrNotFound)
	}
	value := *state.admission
	return &value, nil
}
func (state *workerTestStore) ListCallerGenerationAdmissions(
	context.Context,
	string,
) ([]store.CallerGenerationAdmission, error) {
	if state.admission == nil {
		return []store.CallerGenerationAdmission{}, nil
	}
	return []store.CallerGenerationAdmission{*state.admission}, nil
}
func (state *workerTestStore) ClearCallerLeafOutcome(
	_ context.Context,
	_ store.CallerGenerationIdentity,
	pair store.CallerLeafPair,
) error {
	state.events = append(state.events, "clear-outcome")
	for index := range state.outcomes {
		if state.outcomes[index].Pair.PairDigest == pair.PairDigest {
			state.outcomes = append(state.outcomes[:index], state.outcomes[index+1:]...)
			break
		}
	}
	state.admission = nil
	return nil
}
func (state *workerTestStore) ClearCallerLeafGeneration(
	context.Context,
	store.CallerGenerationIdentity,
) error {
	if state.clearFailures > 0 {
		state.clearFailures--
		state.events = append(state.events, "clear-generation-error")
		return errors.New("injected caller generation clear failure")
	}
	state.events = append(state.events, "clear-generation")
	state.outcomes = nil
	state.admission = nil
	return nil
}
func (state *workerTestStore) ClearAllCallerLeafState(context.Context, string) error {
	state.outcomes = nil
	state.admission = nil
	return nil
}
func (state *workerTestStore) ClearAllCallerLeafStateForRestore(context.Context) error {
	state.outcomes = nil
	state.admission = nil
	return nil
}

func (state *workerTestStore) PublishCallerGeneration(
	_ context.Context,
	_ store.Job,
	publication store.CallerGenerationPublication,
) error {
	if state.publicationFailures > 0 {
		state.publicationFailures--
		state.events = append(state.events, "publish-error")
		return errors.New("injected caller publication write failure")
	}
	publication.PublicationRevision = 1
	if state.publication != nil {
		publication.PublicationRevision = state.publication.PublicationRevision
		if state.publication.ManifestDigest != publication.ManifestDigest {
			publication.PublicationRevision++
		}
	}
	publication.WriterSchema = store.CallerGenerationPublicationWriterSchema
	publication.PublishedAt = time.Now().UTC()
	state.events = append(state.events, "publish")
	state.publication = &publication
	if state.publicationLostAck {
		state.publicationLostAck = false
		state.events[len(state.events)-1] = "publish-lost-ack"
		return errors.New("injected lost publication acknowledgement")
	}
	return nil
}

func (state *workerTestStore) GetCallerGenerationPublication(
	context.Context,
	string,
) (*store.CallerGenerationPublication, error) {
	if state.publication == nil {
		return nil, fmt.Errorf("caller publication: %w", store.ErrNotFound)
	}
	publication := *state.publication
	publication.Pairs = slices.Clone(state.publication.Pairs)
	return &publication, nil
}

func (state *workerTestStore) GetCallerGenerationPublicationSummary(
	context.Context,
	string,
) (*store.CallerGenerationPublicationSummary, error) {
	if state.publication == nil {
		return nil, fmt.Errorf("caller publication summary: %w", store.ErrNotFound)
	}
	summary := callerPublicationSummary(*state.publication)
	return &summary, nil
}

func (state *workerTestStore) ListCallerGenerationPublicationSummaries(
	context.Context,
) ([]store.CallerGenerationPublicationSummary, error) {
	if state.publication == nil {
		return []store.CallerGenerationPublicationSummary{}, nil
	}
	return []store.CallerGenerationPublicationSummary{
		callerPublicationSummary(*state.publication),
	}, nil
}

func (state *workerTestStore) CallerGenerationPublicationCurrent(
	_ context.Context,
	publication store.CallerGenerationPublication,
) (bool, error) {
	return state.publication != nil &&
		state.publication.Generation == publication.Generation &&
		state.publication.ManifestDigest == publication.ManifestDigest &&
		state.publication.PublicationRevision == publication.PublicationRevision &&
		state.publication.PublicationIncarnation == publication.PublicationIncarnation &&
		publication.Generation.CandidateControlRevision == state.candidate.ControlRevision &&
		publication.Generation.ResolverControlRevision == state.resolver.ControlRevision, nil
}

func (state *workerTestStore) CallerGenerationPublicationSummaryCurrent(
	ctx context.Context,
	publication store.CallerGenerationPublicationSummary,
) (bool, error) {
	if state.summaryPayloadInvalid {
		return false, store.ErrInvalidCallerGenerationPublication
	}
	return state.CallerGenerationPublicationSummaryAuthorityCurrent(
		ctx, publication,
	)
}

func (state *workerTestStore) CallerGenerationPublicationSummaryAuthorityCurrent(
	_ context.Context,
	publication store.CallerGenerationPublicationSummary,
) (bool, error) {
	return state.publication != nil &&
		state.publication.Generation == publication.Generation &&
		state.publication.PairPayloadDigest == publication.PairPayloadDigest &&
		state.publication.ManifestDigest == publication.ManifestDigest &&
		state.publication.PublicationRevision == publication.PublicationRevision &&
		state.publication.PublicationIncarnation == publication.PublicationIncarnation &&
		publication.Generation.CandidateControlRevision == state.candidate.ControlRevision &&
		publication.Generation.ResolverControlRevision == state.resolver.ControlRevision, nil
}

func (state *workerTestStore) CallerGenerationPublicationSummariesAuthorityCurrent(
	ctx context.Context,
	publications []store.CallerGenerationPublicationSummary,
) (bool, error) {
	if len(publications) < 1 || len(publications) > 2 {
		return false, errors.New("caller publication joint fence requires one or two summaries")
	}
	for _, publication := range publications {
		current, err := state.CallerGenerationPublicationSummaryAuthorityCurrent(
			ctx, publication,
		)
		if err != nil || !current {
			return false, err
		}
	}
	return true, nil
}

func (state *workerTestStore) ClearCallerGenerationPublication(
	context.Context,
	string,
) error {
	state.events = append(state.events, "clear-publication")
	state.publication = nil
	return nil
}

func (state *workerTestStore) ClearAllCallerPublicationStateForRestore(
	context.Context,
) error {
	state.publication = nil
	state.outcomes = nil
	state.admission = nil
	return nil
}

type workerHarness struct {
	worker   *Worker
	state    *workerTestStore
	plan     *workerTestPlan
	provider *workerTestProvider
	job      store.Job
}

func newWorkerHarness(t *testing.T, leafCount int) workerHarness {
	t.Helper()
	const repository = "github.com/acme/caller-worker"
	digest := func(value byte) string {
		return "sha256:" + string(slices.Repeat([]byte{value}, 64))
	}
	leaves := make([]extract.CandidateCallerLeaf, leafCount)
	prefixes := []string{"00", "01", "10", "11"}
	for index := range leaves {
		leaves[index] = extract.CandidateCallerLeaf{
			Name: fmt.Sprintf("caller-%d.ndjson", index), Ordinal: index,
			Prefix: prefixes[index%len(prefixes)], PrefixBits: 2,
			RecordCount: 1, DeclaredBytes: 1, ContentBytes: 1,
			ContentDigest: digest(byte('a' + index)),
		}
	}
	plan := &workerTestPlan{
		digest: digest('b'), control: 1,
		domains: []extract.CandidateManifestDomain{{
			Domain: "grpc-caller", Version: "1.5.0",
		}},
		leaves: leaves, files: make(map[string][]extract.CandidateManifestFile),
	}
	state := &workerTestStore{
		repo: store.Repo{
			Name:              repository,
			IndexedCommitHash: "0123456789012345678901234567890123456789",
		},
		candidate: store.CandidateManifestPublication{
			Repository:   repository,
			HeadCommit:   "0123456789012345678901234567890123456789",
			PolicyDigest: digest('c'), ManifestDigest: plan.digest,
			GenerationDigest: digest('d'), ControlRevision: 1,
		},
		resolver: store.ResolverCatalogPublication{
			Repository:           repository,
			HeadCommit:           "0123456789012345678901234567890123456789",
			DeclarationSetDigest: digest('e'), CandidateManifestDigest: plan.digest,
			GenerationDigest: digest('f'), ManifestDigest: digest('1'),
			ControlRevision: 1, WriterSchema: resolvercatalogid.WriterSchema,
		},
	}
	provider := &workerTestProvider{plan: plan}
	registry := &Registry{
		adapters: []Adapter{{
			Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc",
		}},
		candidateDomains: slices.Clone(plan.domains),
	}
	worker, err := NewWorker(t.TempDir(), state, provider, registry)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := gocaller.NewDirectResolver(t.Context(), "grpc", nil)
	if err != nil {
		t.Fatal(err)
	}
	worker.resolve = func(context.Context, *authority, string) (*gocaller.DirectResolver, error) {
		return resolver, nil
	}
	worker.readBlob = func(context.Context, string, string, int64) ([]byte, error) {
		t.Fatal("empty worker test pair attempted a blob read")
		return nil, nil
	}
	return workerHarness{
		worker: worker, state: state, plan: plan, provider: provider,
		job: store.Job{Kind: store.JobCallerLeaf, Target: repository},
	}
}

// settle drives Handle the way the runner's drain loop does. The T30.6h
// schedule completes at most one replayed pair per turn, so a generation
// needs one turn per pending pair, one settling turn for admission and
// publication or repair, and a final turn that records no event to prove
// quiescence.
func (harness workerHarness) settle(t *testing.T) {
	t.Helper()
	for turn := 0; turn <= 2*len(harness.plan.leaves)+8; turn++ {
		before := len(harness.state.events)
		if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
			t.Fatal(err)
		}
		if len(harness.state.events) == before {
			return
		}
	}
	t.Fatal("caller generation did not settle within its turn budget")
}

func TestWorkerDurablySettlesPairThenAdmitsWarmGeneration(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	// Turn one replays the pair and returns; turn two admits and publishes.
	// The accumulated mutation order across both turns is unchanged.
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events, []string{
		"enqueue:false", "record:succeeded",
	}; !slices.Equal(got, want) {
		t.Fatalf("pair turn mutation order = %v, want %v", got, want)
	}
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events, []string{
		"enqueue:false", "record:succeeded", "enqueue:false",
		"admission:admitted", "enqueue:false", "publish",
	}; !slices.Equal(got, want) {
		t.Fatalf("pair mutation order = %v, want %v", got, want)
	}
	if len(harness.state.outcomes) != 1 || harness.state.outcomes[0].Receipt == nil {
		t.Fatalf("outcomes = %+v", harness.state.outcomes)
	}
	if harness.state.admission == nil ||
		harness.state.admission.Disposition != store.CallerGenerationAdmitted {
		t.Fatalf("admission = %+v", harness.state.admission)
	}
	opens := harness.provider.opens
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if harness.provider.opens != opens {
		t.Fatalf("warm admission reopened candidate content: %d -> %d", opens, harness.provider.opens)
	}

	// Control-only repairs retain the semantic generation and remain a warm
	// pointer-only no-op after the same process has validated it.
	harness.state.candidate.ControlRevision++
	harness.state.resolver.ControlRevision++
	harness.plan.control++
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if harness.provider.opens != opens {
		t.Fatalf("control-only reuse reopened candidate content: %d -> %d", opens, harness.provider.opens)
	}
}

func TestWorkerQueuesBeforeClearingInvalidPairPayload(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	harness.state.summaryPayloadInvalid = true
	before := len(harness.state.events)
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events[before:], []string{
		"enqueue:true", "clear-publication",
	}; !slices.Equal(got, want) {
		t.Fatalf("pair-payload repair order = %v, want %v", got, want)
	}
	if harness.state.publication != nil {
		t.Fatalf("invalid publication remains: %+v", harness.state.publication)
	}
}

func TestWorkerRecoversManifestBeforeStoreCommit(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.state.publicationFailures = 1
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if err := harness.worker.Handle(t.Context(), harness.job); err == nil ||
		!store.IsSuccessorRetry(err) {
		t.Fatalf("publication commit failure = %v, want successor retry", err)
	}
	if got, want := harness.state.events, []string{
		"enqueue:false", "record:succeeded", "enqueue:false",
		"admission:admitted", "enqueue:false", "publish-error",
	}; !slices.Equal(got, want) {
		t.Fatalf("failed publication order = %v, want %v", got, want)
	}
	marked, err := callerpublication.Publishing(
		harness.worker.root, harness.state.repo.Name,
	)
	if err != nil || !marked || harness.state.publication != nil {
		t.Fatalf("manifest-before-store boundary = marked %v pointer %+v err %v",
			marked, harness.state.publication, err)
	}
	harness.state.events = nil
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events, []string{
		"enqueue:false", "publish",
	}; !slices.Equal(got, want) {
		t.Fatalf("recovered publication order = %v, want %v", got, want)
	}
	marked, err = callerpublication.Publishing(
		harness.worker.root, harness.state.repo.Name,
	)
	if err != nil || marked || harness.state.publication == nil {
		t.Fatalf("recovered publication = marked %v pointer %+v err %v",
			marked, harness.state.publication, err)
	}
}

func TestWorkerRecoversCommittedMarkerAcrossRegistryGenerationConflict(
	t *testing.T,
) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	first := *harness.state.publication

	nextDigest := "sha256:" + strings.Repeat("9", 64)
	harness.plan.digest = nextDigest
	harness.state.candidate.ManifestDigest = nextDigest
	harness.state.candidate.GenerationDigest = "sha256:" + strings.Repeat("8", 64)
	harness.state.resolver.CandidateManifestDigest = nextDigest
	harness.state.outcomes = nil
	harness.state.admission = nil
	harness.state.publicationLostAck = true
	harness.state.events = nil
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if err := harness.worker.Handle(t.Context(), harness.job); err == nil ||
		!store.IsSuccessorRetry(err) {
		t.Fatalf("lost publication acknowledgement = %v, want successor retry", err)
	}
	if harness.state.publication == nil ||
		harness.state.publication.ManifestDigest == first.ManifestDigest {
		t.Fatalf("store did not commit replacement publication: %+v", harness.state.publication)
	}
	if marked, err := callerpublication.Publishing(
		harness.worker.root, harness.state.repo.Name,
	); err != nil || !marked {
		t.Fatalf("replacement marker after lost acknowledgement = %v, %v", marked, err)
	}

	// Registry still holds A while store and the exact surviving marker name B.
	// The warm successor must cross that conflict, clear only B's marker, and
	// make B process-current without another corpus plan open.
	opens := harness.provider.opens
	harness.state.events = nil
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if harness.provider.opens != opens {
		t.Fatalf("committed marker recovery reopened plan: %d -> %d", opens, harness.provider.opens)
	}
	if marked, err := callerpublication.Publishing(
		harness.worker.root, harness.state.repo.Name,
	); err != nil || marked {
		t.Fatalf("replacement marker remains after recovery = %v, %v", marked, err)
	}
}

func TestWorkerForceBypassesWarmValidationCache(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	receipt := artifactReceipt(*harness.state.outcomes[0].Receipt)
	path, err := callerleaf.ArtifactPath(
		harness.worker.root, harness.state.repo.Name, receipt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	opens := harness.provider.opens
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if harness.provider.opens != opens {
		t.Fatal("ordinary warm job bypassed its validated admission")
	}
	harness.state.events = nil
	forced := harness.job
	forced.Force = true
	if err := harness.worker.Handle(t.Context(), forced); err != nil {
		t.Fatal(err)
	}
	if harness.provider.opens != opens+1 {
		t.Fatal("forced job did not reopen the generation")
	}
	if got, want := harness.state.events, []string{"enqueue:true", "clear-outcome"}; !slices.Equal(got, want) {
		t.Fatalf("forced recovery order = %v, want %v", got, want)
	}
}

func TestWorkerRepairsStateWithoutFileQueueBeforeClear(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	receipt := artifactReceipt(*harness.state.outcomes[0].Receipt)
	path, err := callerleaf.ArtifactPath(
		harness.worker.root, harness.state.repo.Name, receipt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// A restarted worker has no process validation cache and must inspect the
	// durable success before trusting it.
	restarted, err := NewWorker(
		harness.worker.dataDir, harness.state, harness.provider, harness.worker.registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	restarted.resolve = harness.worker.resolve
	harness.state.events = nil
	if err := restarted.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events, []string{"enqueue:true", "clear-publication"}; !slices.Equal(got, want) {
		t.Fatalf("repair order = %v, want %v", got, want)
	}
	if len(harness.state.outcomes) != 1 {
		t.Fatalf("pointer retirement mutated leaf outcomes: %+v", harness.state.outcomes)
	}
	harness.state.events = nil
	if err := restarted.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events, []string{"enqueue:true", "clear-outcome"}; !slices.Equal(got, want) {
		t.Fatalf("leaf repair order = %v, want %v", got, want)
	}
	if len(harness.state.outcomes) != 0 {
		t.Fatalf("stale outcome survived leaf repair: %+v", harness.state.outcomes)
	}
}

func TestWorkerRepairsInvalidPairPayloadQueueBeforeClear(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	if harness.state.publication == nil {
		t.Fatal("worker did not publish the complete generation")
	}
	// The cheap pre-admission fence still matches: a same-length raw pair
	// mutation leaves every scalar and the stored commitment unchanged. The
	// final exact fence classifies the payload mismatch deterministically.
	harness.state.summaryPayloadInvalid = true
	harness.state.events = nil
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events, []string{
		"enqueue:true", "clear-publication",
	}; !slices.Equal(got, want) {
		t.Fatalf("pair-payload repair order = %v, want %v", got, want)
	}
	if harness.state.publication != nil {
		t.Fatalf("invalid pair payload remains: %+v", harness.state.publication)
	}
}

func TestWorkerRepairsSameSizeCorruptArtifactQueueBeforeClear(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.worker.execute = func(_ context.Context, request ExecuteRequest) error {
		return request.Stage.Add(callerleaf.Record{
			Schema: callerleaf.RecordSchema, Kind: callerleaf.RecordAbstention,
			Path:       "consumer/call.go",
			ObjectID:   "1111111111111111111111111111111111111111",
			SourceLane: candidate.SourceLaneBase, Reason: "no_direct_caller",
		})
	}
	harness.settle(t)
	receipt := artifactReceipt(*harness.state.outcomes[0].Receipt)
	path, err := callerleaf.ArtifactPath(
		harness.worker.root, harness.state.repo.Name, receipt,
	)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || len(content) == 0 || content[len(content)-1] != '\n' {
		t.Fatalf("read canonical artifact = %d bytes, %v", len(content), err)
	}
	content[len(content)-1] = ' '
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewWorker(
		harness.worker.dataDir, harness.state, harness.provider, harness.worker.registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	restarted.resolve = harness.worker.resolve
	harness.state.events = nil
	if err := restarted.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events, []string{"enqueue:true", "clear-publication"}; !slices.Equal(got, want) {
		t.Fatalf("corruption repair order = %v, want %v", got, want)
	}
	harness.state.events = nil
	if err := restarted.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events, []string{"enqueue:true", "clear-outcome"}; !slices.Equal(got, want) {
		t.Fatalf("corrupt leaf repair order = %v, want %v", got, want)
	}
	if len(harness.state.outcomes) != 0 {
		t.Fatalf("corrupt outcome survived repair: %+v", harness.state.outcomes)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt artifact survived repair: %v", err)
	}
}

func TestWorkerRecoversFileWithoutState(t *testing.T) {
	t.Run("exact orphan resumes", func(t *testing.T) {
		harness := newWorkerHarness(t, 1)
		harness.state.recordFailures = 1
		if err := harness.worker.Handle(t.Context(), harness.job); err == nil ||
			store.IsTerminal(err) || !store.IsSuccessorRetry(err) {
			t.Fatalf("outcome write failure = %v, want retryable error", err)
		}
		names, err := callerleaf.ArtifactNames(harness.worker.root, harness.state.repo.Name)
		if err != nil || len(names) != 1 {
			t.Fatalf("crash orphan = %v, err = %v", names, err)
		}
		harness.state.events = nil
		if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
			t.Fatal(err)
		}
		if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
			t.Fatal(err)
		}
		if got, want := harness.state.events, []string{
			"enqueue:false", "record:succeeded", "enqueue:false",
			"admission:admitted", "enqueue:false", "publish",
		}; !slices.Equal(got, want) {
			t.Fatalf("resume mutations = %v, want %v", got, want)
		}
		if len(harness.state.outcomes) != 1 || harness.state.outcomes[0].Disposition != store.CallerLeafSucceeded {
			t.Fatalf("resume outcome = %+v", harness.state.outcomes)
		}
	})

	t.Run("tampered orphan is removed before retry", func(t *testing.T) {
		harness := newWorkerHarness(t, 1)
		harness.worker.execute = func(_ context.Context, request ExecuteRequest) error {
			return request.Stage.Add(callerleaf.Record{
				Schema: callerleaf.RecordSchema, Kind: callerleaf.RecordAbstention,
				Path:       "consumer/call.go",
				ObjectID:   "1111111111111111111111111111111111111111",
				SourceLane: candidate.SourceLaneBase, Reason: "no_direct_caller",
			})
		}
		harness.state.recordFailures = 1
		if err := harness.worker.Handle(t.Context(), harness.job); err == nil ||
			store.IsTerminal(err) || !store.IsSuccessorRetry(err) {
			t.Fatalf("outcome write failure = %v, want retryable error", err)
		}
		names, err := callerleaf.ArtifactNames(harness.worker.root, harness.state.repo.Name)
		if err != nil || len(names) != 1 {
			t.Fatalf("crash orphan = %v, err = %v", names, err)
		}
		path := filepath.Join(
			harness.worker.root,
			callerleafid.RepositoryDirectory(harness.state.repo.Name),
			names[0],
		)
		content, err := os.ReadFile(path)
		if err != nil || len(content) == 0 || content[len(content)-1] != '\n' {
			t.Fatalf("read canonical orphan = %d bytes, %v", len(content), err)
		}
		content[len(content)-1] = ' '
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		harness.state.events = nil
		if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
			t.Fatal(err)
		}
		if got, want := harness.state.events, []string{"enqueue:false", "enqueue:true"}; !slices.Equal(got, want) {
			t.Fatalf("tamper repair mutations = %v, want %v", got, want)
		}
		if len(harness.state.outcomes) != 0 {
			t.Fatalf("tampered orphan became authority: %+v", harness.state.outcomes)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("tampered orphan was not removed: %v", err)
		}
		if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
			t.Fatal(err)
		}
		if len(harness.state.outcomes) != 1 || harness.state.outcomes[0].Disposition != store.CallerLeafSucceeded {
			t.Fatalf("post-repair outcome = %+v", harness.state.outcomes)
		}
	})

	t.Run("divergent orphan refuses pair without deleting evidence", func(t *testing.T) {
		harness := newWorkerHarness(t, 1)
		harness.state.recordFailures = 1
		if err := harness.worker.Handle(t.Context(), harness.job); err == nil ||
			store.IsTerminal(err) || !store.IsSuccessorRetry(err) {
			t.Fatalf("outcome write failure = %v, want retryable error", err)
		}
		before, err := callerleaf.ArtifactNames(harness.worker.root, harness.state.repo.Name)
		if err != nil || len(before) != 1 {
			t.Fatalf("first orphan = %v, err = %v", before, err)
		}
		harness.worker.execute = func(_ context.Context, request ExecuteRequest) error {
			return request.Stage.Add(callerleaf.Record{
				Schema: callerleaf.RecordSchema, Kind: callerleaf.RecordAbstention,
				Path:       "consumer/divergent.go",
				ObjectID:   "1111111111111111111111111111111111111111",
				SourceLane: candidate.SourceLaneBase, Reason: "no_direct_caller",
			})
		}
		harness.state.events = nil
		if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
			t.Fatal(err)
		}
		if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
			t.Fatal(err)
		}
		if got, want := harness.state.events, []string{
			"enqueue:false", "record:terminal_generation_refusal",
			"enqueue:false", "admission:terminal_generation_refusal",
		}; !slices.Equal(got, want) {
			t.Fatalf("divergence mutations = %v, want %v", got, want)
		}
		after, err := callerleaf.ArtifactNames(harness.worker.root, harness.state.repo.Name)
		if err != nil || !slices.Equal(after, before) {
			t.Fatalf("divergent evidence changed: before=%v after=%v err=%v", before, after, err)
		}
		if len(harness.state.outcomes) != 1 ||
			harness.state.outcomes[0].Disposition != store.CallerLeafTerminalGenerationRefusal {
			t.Fatalf("divergent outcome = %+v", harness.state.outcomes)
		}
	})

	t.Run("valid exact target plus divergent sibling preserves both", func(t *testing.T) {
		harness := newWorkerHarness(t, 1)
		harness.state.recordFailures = 1
		if err := harness.worker.Handle(t.Context(), harness.job); err == nil ||
			store.IsTerminal(err) || !store.IsSuccessorRetry(err) {
			t.Fatalf("outcome write failure = %v, want retryable error", err)
		}
		current, err := harness.worker.currentAuthority(t.Context(), harness.state.repo.Name)
		if err != nil {
			t.Fatal(err)
		}
		pairs, _, err := ExpectedPairs(harness.plan, current.semantic, harness.worker.registry)
		if err != nil {
			t.Fatal(err)
		}
		record := callerleaf.Record{
			Schema: callerleaf.RecordSchema, Kind: callerleaf.RecordAbstention,
			Path:       "consumer/divergent.go",
			ObjectID:   "1111111111111111111111111111111111111111",
			SourceLane: candidate.SourceLaneBase, Reason: "no_direct_caller",
		}
		isolatedRoot := filepath.Join(t.TempDir(), "isolated-caller-leaves")
		stage, err := callerleaf.NewStage(isolatedRoot, current.semantic, pairs[0].Identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.Add(record); err != nil {
			t.Fatal(err)
		}
		prepared, err := stage.Seal()
		if err != nil {
			t.Fatal(err)
		}
		publication, err := prepared.Install(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		receipt := publication.Receipt()
		isolatedPath, err := callerleaf.ArtifactPath(
			isolatedRoot, harness.state.repo.Name, receipt,
		)
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(isolatedPath)
		if err != nil {
			t.Fatal(err)
		}
		exactPath, err := callerleaf.ArtifactPath(
			harness.worker.root, harness.state.repo.Name, receipt,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exactPath, content, 0o600); err != nil {
			t.Fatal(err)
		}
		harness.worker.execute = func(_ context.Context, request ExecuteRequest) error {
			return request.Stage.Add(record)
		}
		harness.state.events = nil
		if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
			t.Fatal(err)
		}
		if len(harness.state.outcomes) != 1 ||
			harness.state.outcomes[0].Disposition != store.CallerLeafTerminalGenerationRefusal {
			t.Fatalf("divergent outcome = %+v", harness.state.outcomes)
		}
		names, err := callerleaf.ArtifactNames(harness.worker.root, harness.state.repo.Name)
		if err != nil || len(names) != 2 {
			t.Fatalf("divergent artifacts were not preserved: %v, %v", names, err)
		}
	})
}

func TestWorkerFailingPairOwnsItsOwnTurnBudget(t *testing.T) {
	harness := newWorkerHarness(t, 2)
	failSecond := true
	injected := errors.New("injected second-pair I/O failure")
	harness.worker.execute = func(_ context.Context, request ExecuteRequest) error {
		if failSecond && request.Pair.Identity.Leaf.Ordinal == 1 {
			return injected
		}
		return nil
	}
	// The first pair's turn completes successfully; the second pair fails in
	// its own turn without an ensured successor, so the runner charges the
	// failed attempt to that job alone rather than to the completed progress.
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if len(harness.state.outcomes) != 1 || harness.state.admission != nil {
		t.Fatalf("partial durable progress = outcomes %+v admission %+v", harness.state.outcomes, harness.state.admission)
	}
	if got, want := harness.state.events, []string{"enqueue:false", "record:succeeded"}; !slices.Equal(got, want) {
		t.Fatalf("partial mutation order = %v, want %v", got, want)
	}
	if err := harness.worker.Handle(t.Context(), harness.job); !errors.Is(err, injected) ||
		store.IsTerminal(err) || store.IsSuccessorRetry(err) {
		t.Fatalf("later operational failure = %v, want plain retryable injected error", err)
	}
	if len(harness.state.outcomes) != 1 || harness.state.admission != nil {
		t.Fatalf("failed turn persisted authority = outcomes %+v admission %+v", harness.state.outcomes, harness.state.admission)
	}
	failSecond = false
	harness.settle(t)
	if len(harness.state.outcomes) != 2 || harness.state.admission == nil ||
		harness.state.admission.Disposition != store.CallerGenerationAdmitted {
		t.Fatalf("successor completion = outcomes %+v admission %+v", harness.state.outcomes, harness.state.admission)
	}
}

func TestWorkerPropagatesInstallFailureAfterEnsuringSuccessor(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	injected := errors.New("injected caller artifact install failure")
	harness.worker.install = func(
		context.Context,
		*callerleaf.Prepared,
		*callerleaf.ArtifactInventory,
	) (*callerleaf.Publication, error) {
		return nil, injected
	}
	if err := harness.worker.Handle(t.Context(), harness.job); !errors.Is(err, injected) ||
		store.IsTerminal(err) || !store.IsSuccessorRetry(err) {
		t.Fatalf("install failure = %v, want retryable injected error", err)
	}
	if got, want := harness.state.events, []string{"enqueue:false"}; !slices.Equal(got, want) {
		t.Fatalf("install failure mutations = %v, want %v", got, want)
	}
	if len(harness.state.outcomes) != 0 || harness.state.admission != nil {
		t.Fatalf("install failure persisted authority: outcomes=%+v admission=%+v",
			harness.state.outcomes, harness.state.admission)
	}
}

func TestWorkerMarksAmbiguousSuccessorCommitFailure(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.job.Attempts = 2
	harness.state.ensureErr = errors.New("successor response unavailable")
	err := harness.worker.Handle(t.Context(), harness.job)
	if !errors.Is(err, harness.state.ensureErr) || store.IsTerminal(err) ||
		!store.IsSuccessorRetry(err) {
		t.Fatalf("ambiguous successor error = %v, want marked retry", err)
	}
	if got, want := harness.state.events, []string{"enqueue:false"}; !slices.Equal(got, want) {
		t.Fatalf("ambiguous successor mutations = %v, want %v", got, want)
	}
	if len(harness.state.outcomes) != 0 || harness.state.admission != nil {
		t.Fatalf("ambiguous successor persisted authority: outcomes=%+v admission=%+v",
			harness.state.outcomes, harness.state.admission)
	}
}

func TestWorkerPropagatesTerminalOutcomeWriteFailureAsRetryable(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.job.Attempts = 2
	harness.state.recordFailures = 1
	harness.worker.execute = func(context.Context, ExecuteRequest) error {
		return callerleaf.ErrLimit
	}
	if err := harness.worker.Handle(t.Context(), harness.job); err == nil ||
		store.IsTerminal(err) || !store.IsSuccessorRetry(err) {
		t.Fatalf("terminal outcome write failure = %v, want retryable store error", err)
	}
	if got, want := harness.state.events, []string{
		"enqueue:false", "record-error",
	}; !slices.Equal(got, want) {
		t.Fatalf("terminal outcome failure mutations = %v, want %v", got, want)
	}
}

func TestWorkerPropagatesGenerationClearFailureAfterForcedSuccessor(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.job.Attempts = 2
	current, err := harness.worker.currentAuthority(t.Context(), harness.state.repo.Name)
	if err != nil {
		t.Fatal(err)
	}
	harness.state.clearFailures = 1
	err = harness.worker.recoverGeneration(t.Context(), harness.job, current.stored)
	if err == nil || !store.IsSuccessorRetry(err) {
		t.Fatal("generation clear failure was swallowed")
	}
	if got, want := harness.state.events, []string{
		"enqueue:true", "clear-generation-error",
	}; !slices.Equal(got, want) {
		t.Fatalf("generation clear failure mutations = %v, want %v", got, want)
	}
}

func TestWorkerEnsuresSuccessorBeforeSettledAdmissionRetry(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.state.admissionFailures = 1
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if err := harness.worker.Handle(t.Context(), harness.job); err == nil ||
		store.IsTerminal(err) || !store.IsSuccessorRetry(err) {
		t.Fatalf("first admission write failure = %v, want retryable error", err)
	}
	if harness.state.admission != nil || len(harness.state.outcomes) != 1 {
		t.Fatalf("first admission boundary = outcomes %+v admission %+v", harness.state.outcomes, harness.state.admission)
	}
	harness.state.events = nil
	harness.state.admissionFailures = 1
	if err := harness.worker.Handle(t.Context(), harness.job); err == nil ||
		store.IsTerminal(err) || !store.IsSuccessorRetry(err) {
		t.Fatalf("settled admission write failure = %v, want retryable error", err)
	}
	if got, want := harness.state.events, []string{"enqueue:false", "admission-error"}; !slices.Equal(got, want) {
		t.Fatalf("settled admission recovery order = %v, want %v", got, want)
	}
	if harness.state.admission != nil {
		t.Fatalf("failed settled admission unexpectedly persisted: %+v", harness.state.admission)
	}
	harness.state.events = nil
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events, []string{
		"enqueue:false", "admission:admitted",
		"enqueue:false", "publish",
	}; !slices.Equal(got, want) {
		t.Fatalf("settled admission completion order = %v, want %v", got, want)
	}
}

func TestWorkerTerminalPairDoesNotSuppressSibling(t *testing.T) {
	harness := newWorkerHarness(t, 2)
	harness.worker.execute = func(_ context.Context, request ExecuteRequest) error {
		if request.Pair.Identity.Leaf.Ordinal == 0 {
			return callerleaf.ErrLimit
		}
		return request.Stage.Add(callerleaf.Record{
			Schema: callerleaf.RecordSchema, Kind: callerleaf.RecordAbstention,
			Path:       "consumer/call.go",
			ObjectID:   "1111111111111111111111111111111111111111",
			SourceLane: candidate.SourceLaneBase, Reason: "no_direct_caller",
		})
	}
	harness.settle(t)
	if len(harness.state.outcomes) != 2 ||
		harness.state.outcomes[0].Disposition != store.CallerLeafTerminalGenerationRefusal ||
		harness.state.outcomes[1].Disposition != store.CallerLeafSucceeded {
		t.Fatalf("sibling outcomes = %+v", harness.state.outcomes)
	}
	if harness.state.admission == nil ||
		harness.state.admission.Disposition != store.CallerGenerationTerminalGenerationRefusal {
		t.Fatalf("terminal admission = %+v", harness.state.admission)
	}
	// One replayed pair per turn: refusal turn, sibling turn, settling turn,
	// and settle's final no-event turn short-circuits before the plan opens.
	if harness.provider.opens != 3 {
		t.Fatalf("per-pair turns opened the candidate plan %d times, want 3", harness.provider.opens)
	}
}

func TestWorkerStopsContentWorkAfterFirstAggregateCrossing(t *testing.T) {
	harness := newWorkerHarness(t, 3)
	harness.worker.execute = func(context.Context, ExecuteRequest) error {
		return nil
	}
	// One replayed pair per turn: the first turn durably seeds pair zero and
	// completes without touching its siblings.
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if len(harness.state.outcomes) != 1 || harness.state.outcomes[0].Receipt == nil {
		t.Fatalf("aggregate seed outcomes = %+v", harness.state.outcomes)
	}
	// The pair is already process-validated, so this synthetic aggregate count
	// exercises only the drain budget without manufacturing 100,000 JSON rows.
	harness.state.outcomes[0].Receipt.ResultCount = callerleaf.MaxAggregateResultRecords
	executions := 0
	harness.worker.execute = func(_ context.Context, request ExecuteRequest) error {
		executions++
		digest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		return request.Stage.Add(callerleaf.Record{
			Schema: callerleaf.RecordSchema, Kind: callerleaf.RecordResult,
			Path:       "consumer/call.go",
			ObjectID:   "1111111111111111111111111111111111111111",
			SourceLane: candidate.SourceLaneBase,
			Fact: &sdk.Fact{
				Path: "consumer/call.go", StartLine: 1, EndLine: 1,
				Atom: sdk.AtomInput{
					SchemaVersion: "caller-v1", BlobDigest: digest,
					StartByte: 1, EndByte: 4, RuleID: "direct-v1",
					AdapterConfigDigest: digest, FactFingerprint: "f",
				},
				Assertion: sdk.AssertionInput{
					Predicate: "CALLS_OPERATION", Subject: "consumer/call.go:1-4",
					Object: "/demo.Service/Get", Lineage: "lineage", Tier: "derived",
				},
			},
		})
	}
	harness.settle(t)
	if executions != 1 {
		t.Fatalf("post-cap source executions = %d, want 1 crossing pair only", executions)
	}
	if len(harness.state.outcomes) != 3 ||
		harness.state.outcomes[1].Disposition != store.CallerLeafSucceeded ||
		harness.state.outcomes[2].Disposition != store.CallerLeafTerminalGenerationRefusal {
		t.Fatalf("bounded aggregate outcomes = %+v", harness.state.outcomes)
	}
	if harness.state.admission == nil ||
		harness.state.admission.Disposition != store.CallerGenerationTerminalGenerationRefusal {
		t.Fatalf("bounded aggregate admission = %+v", harness.state.admission)
	}
	names, err := callerleaf.ArtifactNames(harness.worker.root, harness.state.repo.Name)
	if err != nil || len(names) != 2 {
		t.Fatalf("aggregate crossing artifacts = %v, err = %v", names, err)
	}
}

func TestAdmissionDispositionAggregateCapAndCapPlusOne(t *testing.T) {
	makeOutcomes := func(counts ...int) []store.CallerLeafOutcome {
		result := make([]store.CallerLeafOutcome, len(counts))
		for index, count := range counts {
			result[index] = store.CallerLeafOutcome{
				Disposition: store.CallerLeafSucceeded,
				Receipt: &store.CallerLeafArtifactReceipt{
					ResultCount: count,
				},
			}
		}
		return result
	}
	atCap := makeOutcomes(12_500, 12_500, 12_500, 12_500, 12_500, 12_500, 12_500, 12_500)
	if got := admissionDisposition(atCap); got != store.CallerGenerationAdmitted {
		t.Fatalf("at-cap disposition = %q", got)
	}
	capPlusOne := append(slices.Clone(atCap), makeOutcomes(1)...)
	if got := admissionDisposition(capPlusOne); got != store.CallerGenerationTerminalGenerationRefusal {
		t.Fatalf("cap+1 disposition = %q", got)
	}
}

func TestExpectedPairsRefusesUnrepresentablePairSetCapPlusOne(t *testing.T) {
	generation := executeTestGeneration(t)
	domain := extract.CandidateManifestDomain{Domain: "grpc-caller", Version: "1.5.0"}
	plan := &workerTestPlan{
		digest:  generation.CandidateManifestDigest,
		domains: []extract.CandidateManifestDomain{domain},
		leaves:  make([]extract.CandidateCallerLeaf, callerleaf.MaxExpectedPairs+1),
	}
	_, _, err := ExpectedPairs(plan, generation, &Registry{
		adapters: []Adapter{{
			Domain: domain.Domain, Version: domain.Version, Protocol: "grpc",
		}},
		candidateDomains: []extract.CandidateManifestDomain{domain},
	})
	if !errors.Is(err, callerleaf.ErrLimit) {
		t.Fatalf("pair-set cap+1 = %v, want ErrLimit", err)
	}
}

func TestWorkerEndsTurnAfterOneReplayedPair(t *testing.T) {
	harness := newWorkerHarness(t, 2)
	executions := 0
	harness.worker.execute = func(context.Context, ExecuteRequest) error {
		executions++
		return nil
	}
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if executions != 1 || len(harness.state.outcomes) != 1 {
		t.Fatalf(
			"first turn = %d executions, %d outcomes, want exactly one replayed pair",
			executions, len(harness.state.outcomes),
		)
	}
	if harness.state.admission != nil || harness.state.publication != nil {
		t.Fatalf(
			"first turn settled early: admission=%+v publication=%+v",
			harness.state.admission, harness.state.publication,
		)
	}
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if executions != 2 || len(harness.state.outcomes) != 2 {
		t.Fatalf(
			"second turn = %d executions, %d outcomes, want the second pair alone",
			executions, len(harness.state.outcomes),
		)
	}
	harness.settle(t)
	if executions != 2 {
		t.Fatalf("settling turns replayed pairs: %d executions", executions)
	}
	if harness.state.admission == nil || harness.state.publication == nil {
		t.Fatal("settling turn did not admit and publish")
	}
}

func TestWorkerReplayDeadlineStaysRetryableWithoutSuccessorMark(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	fail := true
	harness.worker.execute = func(context.Context, ExecuteRequest) error {
		if fail {
			return fmt.Errorf(
				"read selected caller blob %q: %w",
				"consumer/call.go", context.DeadlineExceeded,
			)
		}
		return nil
	}
	err := harness.worker.Handle(t.Context(), harness.job)
	if !errors.Is(err, context.DeadlineExceeded) || store.IsTerminal(err) ||
		store.IsSuccessorRetry(err) {
		t.Fatalf("worker deadline = %v, want plain retryable deadline error", err)
	}
	if len(harness.state.outcomes) != 0 || len(harness.state.events) != 0 {
		t.Fatalf(
			"expired turn persisted state: outcomes=%+v events=%v",
			harness.state.outcomes, harness.state.events,
		)
	}
	// The runner requeues the plain retryable error against this job's own
	// attempt budget: a genuinely oversized pair exhausts attempts and fails,
	// while a transient expiry replays with a fresh worker deadline.
	fail = false
	harness.settle(t)
	if len(harness.state.outcomes) != 1 || harness.state.publication == nil {
		t.Fatalf("retried pair did not settle: %+v", harness.state.outcomes)
	}
}

func TestWorkerParentCancellationIsDistinctFromWorkerDeadline(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.worker.execute = func(ctx context.Context, _ ExecuteRequest) error {
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := harness.worker.Handle(ctx, harness.job)
	if !errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled parent = %v, want context.Canceled", err)
	}
	if len(harness.state.outcomes) != 0 {
		t.Fatalf("canceled turn persisted outcomes: %+v", harness.state.outcomes)
	}
}

func TestWorkerRestartReusesDurableOutcomesWithoutReplay(t *testing.T) {
	harness := newWorkerHarness(t, 2)
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
	if len(harness.state.outcomes) != 1 {
		t.Fatalf("first turn outcomes = %+v", harness.state.outcomes)
	}
	restarted, err := NewWorker(
		harness.worker.dataDir, harness.state, harness.provider, harness.worker.registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	restarted.resolve = harness.worker.resolve
	replayed := map[int]int{}
	restarted.execute = func(_ context.Context, request ExecuteRequest) error {
		replayed[request.Pair.Identity.Leaf.Ordinal]++
		return nil
	}
	restartedHarness := harness
	restartedHarness.worker = restarted
	restartedHarness.settle(t)
	if replayed[0] != 0 || replayed[1] != 1 {
		t.Fatalf("restart replay counts = %v, want only the pending pair once", replayed)
	}
	if harness.state.publication == nil || len(harness.state.outcomes) != 2 {
		t.Fatalf(
			"restarted worker did not complete: outcomes=%d publication=%v",
			len(harness.state.outcomes), harness.state.publication != nil,
		)
	}
}
