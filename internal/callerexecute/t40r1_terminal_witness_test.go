package callerexecute

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
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t40r1CallerTerminalWitnessSchema = "t4013-caller-terminal-witness-v1"
	t40r1CallerTerminalWitnessEnv    = "T40R1_CALLER_TERMINAL_WITNESS"
	t40r1CallerTerminalPairCount     = 192
	t40r1CallerTerminalLeafCount     = t40r1CallerTerminalPairCount / 2
	t40r1CallerTerminalCandidates    = 262_145
)

type t40r1CallerTerminalProgress struct {
	Settled   int `json:"settled"`
	Succeeded int `json:"succeeded"`
	Refused   int `json:"refused"`
}

type t40r1CallerTerminalAggregate struct {
	Disposition       string `json:"disposition"`
	PairCount         int    `json:"pair_count"`
	ArtifactCount     int    `json:"artifact_count"`
	ResultRecords     int    `json:"result_records"`
	AbstentionRecords int    `json:"abstention_records"`
	CoverageRecords   int    `json:"coverage_records"`
	CoveredCandidates int    `json:"covered_candidates"`
	CanonicalBytes    int64  `json:"canonical_bytes"`
	StagingBytes      int64  `json:"staging_bytes"`
	RefusalSummaries  int    `json:"refusal_summaries"`
}

type t40r1CallerTerminalPublication struct {
	Present           bool `json:"present"`
	AuthorityCurrent  bool `json:"authority_current"`
	PairCount         int  `json:"pair_count"`
	ResultRecords     int  `json:"result_records"`
	AbstentionRecords int  `json:"abstention_records"`
	CoverageRecords   int  `json:"coverage_records"`
	CoveredCandidates int  `json:"covered_candidates"`
}

type t40r1CallerTerminalReader struct {
	Availability string                      `json:"availability"`
	Origin       string                      `json:"origin"`
	Progress     t40r1CallerTerminalProgress `json:"progress"`
}

type t40r1CallerTerminalWitness struct {
	Schema               string                         `json:"schema"`
	Method               string                         `json:"method"`
	Profile              string                         `json:"profile"`
	LeafAdapter          string                         `json:"leaf_adapter"`
	Policy               string                         `json:"policy"`
	CandidateRecords     int                            `json:"candidate_records"`
	CandidateLeaves      int                            `json:"candidate_leaves"`
	Protocols            int                            `json:"protocols"`
	ExpectedPairs        int                            `json:"expected_pairs"`
	ResolverDescriptors  map[string]int                 `json:"resolver_descriptors"`
	WorkerTurns          int                            `json:"worker_turns"`
	CandidatePlanOpens   int                            `json:"candidate_plan_opens"`
	Progress             t40r1CallerTerminalProgress    `json:"progress"`
	Admission            t40r1CallerTerminalAggregate   `json:"admission"`
	Publication          t40r1CallerTerminalPublication `json:"publication"`
	Reader               t40r1CallerTerminalReader      `json:"reader"`
	Boundary             string                         `json:"boundary"`
	ClearsCallerBoundary bool                           `json:"clears_caller_boundary"`
	EstablishesScalePass bool                           `json:"establishes_scale_pass"`
	AuthorizesFreeze     bool                           `json:"authorizes_freeze"`
}

// t40r1CallerBoundaryStore deliberately exposes only the production caller
// store contract. The retained Take 20 sequence already proved current
// observation and extraction authority before entering caller execution; this
// focused witness starts at that exact boundary while keeping every caller
// outcome, admission, and publication write on production SurrealDB.
type t40r1CallerBoundaryStore struct {
	Store
}

type t40r1CallerTerminalPlan struct {
	digest  string
	control uint64
	domains []extract.CandidateManifestDomain
	leaves  []extract.CandidateCallerLeaf
	starts  []int
	counts  []int
}

func (plan *t40r1CallerTerminalPlan) Identity() string { return plan.digest }

func (plan *t40r1CallerTerminalPlan) CandidateControlRevision() uint64 {
	return plan.control
}

func (plan *t40r1CallerTerminalPlan) CallerDomains() []extract.CandidateManifestDomain {
	return slices.Clone(plan.domains)
}

func (plan *t40r1CallerTerminalPlan) CallerLeaves() []extract.CandidateCallerLeaf {
	return slices.Clone(plan.leaves)
}

func (plan *t40r1CallerTerminalPlan) ForEachCallerLeafFile(
	ctx context.Context,
	domain,
	version string,
	leaf extract.CandidateCallerLeaf,
	visit func(extract.CandidateManifestFile) error,
) error {
	domainFound := false
	for _, candidateDomain := range plan.domains {
		if candidateDomain.Domain == domain && candidateDomain.Version == version {
			domainFound = true
			break
		}
	}
	if !domainFound || leaf.Ordinal < 0 || leaf.Ordinal >= len(plan.leaves) ||
		plan.leaves[leaf.Ordinal] != leaf {
		return errors.New("T40.R1 caller terminal plan received an unexpected pair")
	}
	start, count := plan.starts[leaf.Ordinal], plan.counts[leaf.Ordinal]
	for offset := 0; offset < count; offset++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		ordinal := start + offset
		path := fmt.Sprintf("semantic/go/b%03d/f%06d.go", ordinal/1_024, ordinal)
		if ordinal == t40r1CallerTerminalCandidates-1 {
			path = "go.mod"
		}
		if err := visit(extract.CandidateManifestFile{
			Path: path, ObjectID: fmt.Sprintf("%040x", ordinal+1),
			DeclaredBytes: 512, SourceLane: candidate.SourceLaneBase,
		}); err != nil {
			return err
		}
	}
	return nil
}

type t40r1CallerTerminalProvider struct {
	plan  *t40r1CallerTerminalPlan
	opens int
}

func (provider *t40r1CallerTerminalProvider) OpenCandidateCallerPlan(
	context.Context,
	extract.CandidateManifestRequest,
) (extract.CandidateCallerPlan, error) {
	provider.opens++
	return provider.plan, nil
}

func TestT40R1CallerTerminalDiskWitness(t *testing.T) {
	if os.Getenv(t40r1CallerTerminalWitnessEnv) != "1" {
		t.Skip("set " + t40r1CallerTerminalWitnessEnv + "=1 to run the disk-backed caller witness")
	}
	if callerleaf.CurrentLeafAdapter != callerleaf.LeafAdapterV2 {
		t.Skip("the retained caller terminal witness is historical V2 evidence")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary is not installed")
	}
	witness := runT40R1CallerTerminalWitness(t)
	encoded, err := json.MarshalIndent(witness, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	retainedPath := filepath.Join(
		"..", "..", "spike", "t4013", "caller-terminal-witness.json",
	)
	raw, err := os.ReadFile(retainedPath)
	if err != nil {
		t.Fatalf("retained caller terminal witness unavailable: %v\n%s", err, encoded)
	}
	var retained t40r1CallerTerminalWitness
	if err := json.Unmarshal(raw, &retained); err != nil || !reflect.DeepEqual(retained, witness) {
		t.Fatalf("retained caller terminal witness differs: %v\n%s", err, encoded)
	}
	t.Log(string(encoded))
}

func TestT40R1RetainedCallerTerminalWitnessIsClosed(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "spike", "t4013", "caller-terminal-witness.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var witness t40r1CallerTerminalWitness
	if err := json.Unmarshal(raw, &witness); err != nil {
		t.Fatal(err)
	}
	if witness.Schema != t40r1CallerTerminalWitnessSchema ||
		witness.Profile != "semantic-262144-v1" ||
		witness.LeafAdapter != callerleaf.LeafAdapterV2 ||
		witness.Policy != callerleaf.PolicyNameV2 ||
		witness.CandidateRecords != t40r1CallerTerminalCandidates ||
		witness.CandidateLeaves != t40r1CallerTerminalLeafCount ||
		witness.Protocols != 2 || witness.ExpectedPairs != t40r1CallerTerminalPairCount ||
		witness.Boundary != "current_publication" || !witness.ClearsCallerBoundary ||
		witness.EstablishesScalePass || witness.AuthorizesFreeze {
		t.Fatalf("retained caller terminal witness is not closed: %+v", witness)
	}
	if witness.Progress != (t40r1CallerTerminalProgress{
		Settled:   t40r1CallerTerminalPairCount,
		Succeeded: t40r1CallerTerminalPairCount,
	}) || witness.Admission.Disposition != string(store.CallerGenerationAdmitted) ||
		witness.Admission.RefusalSummaries != 0 ||
		!witness.Publication.Present || !witness.Publication.AuthorityCurrent ||
		witness.Reader.Availability != string(PublicationCurrent) ||
		witness.Reader.Origin != "current_publication" {
		t.Fatalf("retained caller terminal authority is not current: %+v", witness)
	}
}

func runT40R1CallerTerminalWitness(t *testing.T) t40r1CallerTerminalWitness {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	dataDir := t.TempDir()
	state, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })

	registry, err := NewRegistry([]extract.Extractor{
		gocaller.NewGRPC(), gocaller.NewThrift(),
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := newT40R1CallerTerminalPlan(t, registry)
	provider := &t40r1CallerTerminalProvider{plan: plan}
	const repository = "github.com/phebs/t40r1-caller-terminal-witness"
	const commit = "0123456789012345678901234567890123456789"
	if err := state.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://github.com/phebs/t40r1-caller-terminal-witness.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	candidatePublication := store.CandidateManifestPublication{
		Repository: repository, HeadCommit: commit,
		PolicyDigest:     t40r1TerminalDigest("candidate-policy"),
		ManifestDigest:   plan.digest,
		GenerationDigest: t40r1TerminalDigest("candidate-generation"),
		ManifestPath:     candidateid.ManifestName(repository),
	}
	if err := state.PublishCandidateManifest(ctx, candidatePublication); err != nil {
		t.Fatal(err)
	}
	persistedCandidate, err := state.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	plan.control = persistedCandidate.ControlRevision
	resolverIdentity, err := resolvercatalog.NewIdentity(
		repository, commit, "", persistedCandidate.ManifestDigest, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverPublication := store.ResolverCatalogPublication{
		Repository: repository, HeadCommit: commit,
		Declarations:            []store.ResolverCatalogDeclarationPublication{},
		DeclarationSetDigest:    resolverIdentity.DeclarationSetDigest,
		CandidateManifestDigest: persistedCandidate.ManifestDigest,
		SourceLanePolicy:        resolvercatalog.SourceLanePolicy,
		ResolverPacks:           []store.ResolverCatalogPack{},
		ResolverPackSetDigest:   resolverIdentity.ResolverPackSetDigest,
		CatalogPolicyDigest:     resolverIdentity.CatalogPolicyDigest,
		GenerationDigest:        resolverIdentity.GenerationDigest,
		ManifestDigest:          t40r1TerminalDigest("resolver-manifest"),
		ManifestPath:            resolvercatalogid.ManifestName(repository),
	}
	if err := state.PublishResolverCatalog(ctx, resolverPublication); err != nil {
		t.Fatal(err)
	}

	boundaryStore := &t40r1CallerBoundaryStore{Store: state}
	worker, err := NewWorker(dataDir, boundaryStore, provider, registry)
	if err != nil {
		t.Fatal(err)
	}
	resolverDescriptors := map[string]int{"grpc": 0, "thrift": 0}
	resolvers := make(map[string]*gocaller.DirectResolver, 2)
	worker.resolve = func(
		resolveCtx context.Context,
		_ *authority,
		protocol string,
	) (*gocaller.DirectResolver, error) {
		if resolver := resolvers[protocol]; resolver != nil {
			return resolver, nil
		}
		resolver, resolverErr := gocaller.NewDirectResolver(resolveCtx, protocol, nil)
		if resolverErr == nil {
			resolverDescriptors[protocol] = resolver.DescriptorCount()
			resolvers[protocol] = resolver
		}
		return resolver, resolverErr
	}
	worker.readBlob = func(context.Context, string, string, int64) ([]byte, error) {
		return nil, errors.New("compact caller terminal witness attempted a source read")
	}

	current, err := currentAuthority(ctx, boundaryStore, registry, repository)
	if err != nil || current == nil {
		t.Fatalf("caller terminal witness authority = %+v, %v", current, err)
	}
	if _, err := state.CancelPendingJobs(ctx, store.JobCallerLeaf, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := state.EnqueuePending(ctx, store.JobCallerLeaf, repository, false); err != nil {
		t.Fatal(err)
	}
	turns := driveT40R1CallerTerminalWorker(t, ctx, state, worker, repository)

	progress, err := state.GetCallerLeafOutcomeProgress(ctx, current.stored)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := state.GetCallerGenerationAdmission(ctx, current.stored)
	if err != nil {
		t.Fatal(err)
	}
	pointer, err := state.GetCallerGenerationPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	pointerCurrent, err := state.CallerGenerationPublicationCurrent(ctx, *pointer)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewPublicationReader(
		dataDir, boundaryStore, registry, worker.publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	read, err := reader.Open(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Release() }()

	witness := t40r1CallerTerminalWitness{
		Schema:           t40r1CallerTerminalWitnessSchema,
		Method:           "the exact retained 96-leaf/192-pair semantic caller cardinality is replayed through production compact pair execution, caller artifact installation, a supervised disk-backed SurrealDB store, complete publication, and the shared product reader after upstream authority is prevalidated",
		Profile:          "semantic-262144-v1",
		LeafAdapter:      callerleaf.CurrentLeafAdapter,
		Policy:           callerleaf.CurrentPolicy().Name,
		CandidateRecords: t40r1CallerTerminalCandidates,
		CandidateLeaves:  t40r1CallerTerminalLeafCount,
		Protocols:        len(registry.Adapters()), ExpectedPairs: t40r1CallerTerminalPairCount,
		ResolverDescriptors: resolverDescriptors,
		WorkerTurns:         turns, CandidatePlanOpens: provider.opens,
		Progress: t40r1CallerTerminalProgress{
			Settled: progress.SettledCount, Succeeded: progress.SucceededCount,
			Refused: progress.RefusedCount,
		},
		Admission: t40r1CallerTerminalAggregate{
			Disposition: string(admission.Disposition), PairCount: admission.PairCount,
			ArtifactCount:     admission.ArtifactCount,
			ResultRecords:     admission.ResultCount,
			AbstentionRecords: admission.AbstentionCount,
			CoverageRecords:   admission.CoverageRecordCount,
			CoveredCandidates: admission.CoveredCandidateCount,
			CanonicalBytes:    admission.CanonicalBytes, StagingBytes: admission.StagingBytes,
			RefusalSummaries: len(admission.Refusals),
		},
		Publication: t40r1CallerTerminalPublication{
			Present: true, AuthorityCurrent: pointerCurrent,
			PairCount: pointer.PairCount, ResultRecords: pointer.ResultCount,
			AbstentionRecords: pointer.AbstentionCount,
			CoverageRecords:   pointer.CoverageRecordCount,
			CoveredCandidates: pointer.CoveredCandidateCount,
		},
		Reader: t40r1CallerTerminalReader{
			Availability: string(read.Availability), Origin: t40r1CallerReadOrigin(read),
			Progress: t40r1CallerTerminalProgress{
				Settled: progress.SettledCount, Succeeded: progress.SucceededCount,
				Refused: progress.RefusedCount,
			},
		},
		Boundary: "current_publication",
	}
	witness.ClearsCallerBoundary =
		witness.ResolverDescriptors["grpc"] == 0 &&
			witness.ResolverDescriptors["thrift"] == 0 &&
			witness.Progress == (t40r1CallerTerminalProgress{
				Settled:   t40r1CallerTerminalPairCount,
				Succeeded: t40r1CallerTerminalPairCount,
			}) &&
			witness.Admission.Disposition == string(store.CallerGenerationAdmitted) &&
			witness.Admission.PairCount == t40r1CallerTerminalPairCount &&
			witness.Admission.RefusalSummaries == 0 &&
			witness.Admission.ResultRecords == 0 && witness.Admission.AbstentionRecords == 0 &&
			witness.Admission.CoverageRecords == t40r1CallerTerminalPairCount &&
			witness.Admission.CoveredCandidates == 2*t40r1CallerTerminalCandidates &&
			witness.Publication.Present && witness.Publication.AuthorityCurrent &&
			read.Availability == PublicationCurrent &&
			witness.Reader.Origin == "current_publication"
	if !witness.ClearsCallerBoundary {
		t.Fatalf("caller terminal witness did not clear: %+v", witness)
	}
	return witness
}

func newT40R1CallerTerminalPlan(
	t *testing.T,
	registry *Registry,
) *t40r1CallerTerminalPlan {
	t.Helper()
	plan := &t40r1CallerTerminalPlan{
		digest:  t40r1TerminalDigest("candidate-manifest"),
		domains: registry.CandidateDomains(),
		leaves:  make([]extract.CandidateCallerLeaf, t40r1CallerTerminalLeafCount),
		starts:  make([]int, t40r1CallerTerminalLeafCount),
		counts:  make([]int, t40r1CallerTerminalLeafCount),
	}
	base := t40r1CallerTerminalCandidates / t40r1CallerTerminalLeafCount
	remainder := t40r1CallerTerminalCandidates % t40r1CallerTerminalLeafCount
	start := 0
	for ordinal := range plan.leaves {
		count := base
		if ordinal < remainder {
			count++
		}
		plan.starts[ordinal], plan.counts[ordinal] = start, count
		plan.leaves[ordinal] = extract.CandidateCallerLeaf{
			Name:    fmt.Sprintf("phebs-candidate-neutral-v4-caller-%06d.ndjson", ordinal),
			Ordinal: ordinal, Prefix: fmt.Sprintf("%08b", ordinal), PrefixBits: 8,
			RecordCount: count, DeclaredBytes: int64(count * 512),
			ContentBytes:  int64(count * 256),
			ContentDigest: t40r1TerminalDigest(fmt.Sprintf("candidate-leaf-%d", ordinal)),
		}
		start += count
	}
	if start != t40r1CallerTerminalCandidates {
		t.Fatalf("caller terminal candidate census = %d", start)
	}
	return plan
}

func driveT40R1CallerTerminalWorker(
	t *testing.T,
	ctx context.Context,
	state *store.Surreal,
	worker *Worker,
	repository string,
) int {
	t.Helper()
	publicationSeen := false
	for turn := 1; turn <= t40r1CallerTerminalPairCount+4; turn++ {
		job, err := state.ClaimJob(ctx, store.JobCallerLeaf, "t40r1-caller-terminal-witness")
		if err != nil {
			t.Fatalf("claim caller terminal turn %d: %v", turn, err)
		}
		if err := state.SetJobStatus(ctx, *job, store.StatusRunning, ""); err != nil {
			t.Fatal(err)
		}
		job.Status = store.StatusRunning
		if err := worker.Handle(ctx, *job); err != nil {
			t.Fatalf("caller terminal worker turn %d failed: %v", turn, err)
		}
		if err := state.SetJobStatus(ctx, *job, store.StatusDone, ""); err != nil {
			t.Fatal(err)
		}
		pointer, pointerErr := state.GetCallerGenerationPublication(ctx, repository)
		if pointerErr != nil && !errors.Is(pointerErr, store.ErrNotFound) {
			t.Fatal(pointerErr)
		}
		if pointerErr == nil {
			current, currentErr := state.CallerGenerationPublicationCurrent(ctx, *pointer)
			if currentErr != nil {
				t.Fatal(currentErr)
			}
			if current && publicationSeen {
				return turn
			}
			publicationSeen = publicationSeen || current
		}
	}
	t.Fatal("caller terminal witness did not reach a current publication and no-op")
	return 0
}

func t40r1CallerReadOrigin(read *PublicationRead) string {
	if read == nil {
		return "no_read"
	}
	if read.Availability == PublicationCurrent {
		return "current_publication"
	}
	if read.Availability == PublicationFailed && read.Admission != nil &&
		read.Admission.Disposition == store.CallerGenerationTerminalGenerationRefusal {
		return "terminal_admission"
	}
	if read.Availability == PublicationFailed {
		return "deterministic_publication_rejection"
	}
	return string(read.Availability)
}

func t40r1TerminalDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

var _ extract.CandidateCallerPlan = (*t40r1CallerTerminalPlan)(nil)
var _ extract.CandidateCallerPlanProvider = (*t40r1CallerTerminalProvider)(nil)
var _ Store = (*t40r1CallerBoundaryStore)(nil)
var _ PublicationReadStore = (*t40r1CallerBoundaryStore)(nil)
