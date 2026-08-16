package t4013

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

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t40r1CallerUpstreamSchema     = "t4013-caller-terminal-upstream-witness-v1"
	t40r1CallerUpstreamEnv        = "T40R1_CALLER_UPSTREAM_WITNESS"
	t40r1CallerUpstreamCandidates = 262_145
	t40r1CallerUpstreamLeaves     = 96
	t40r1CallerUpstreamPairs      = 192
	t40r1CallerUpstreamRepository = "github.com/phebs/t40r1-caller-upstream-witness"
	t40r1CallerUpstreamCommit     = "0123456789012345678901234567890123456789"
	t40r1CallerUpstreamLineage    = "provisional_repo_path_v1_" +
		"abababababababababababababababababababababababababababababababab"
	t40r1CallerUpstreamOperation = "phebs.orders.v1.Orders/Get"
)

type t40r1CallerUpstreamProgress struct {
	Settled   int `json:"settled"`
	Succeeded int `json:"succeeded"`
	Refused   int `json:"refused"`
}

type t40r1CallerUpstreamResolver struct {
	PublicationCurrent bool           `json:"publication_current"`
	ManifestMembers    int            `json:"manifest_members"`
	Descriptors        map[string]int `json:"descriptors"`
}

type t40r1CallerUpstreamAdmission struct {
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

type t40r1CallerUpstreamPublication struct {
	Present           bool `json:"present"`
	AuthorityCurrent  bool `json:"authority_current"`
	PairCount         int  `json:"pair_count"`
	ResultRecords     int  `json:"result_records"`
	AbstentionRecords int  `json:"abstention_records"`
	CoverageRecords   int  `json:"coverage_records"`
	CoveredCandidates int  `json:"covered_candidates"`
}

type t40r1CallerUpstreamReader struct {
	Availability string `json:"availability"`
	Origin       string `json:"origin"`
}

type t40r1CallerUpstreamCallerMap struct {
	SchemaVersion     string `json:"schema_version"`
	MatchingRowsState string `json:"matching_rows_state"`
	GenerationState   string `json:"generation_state"`
	DeclarationExact  bool   `json:"declaration_exact"`
	Rows              int    `json:"rows"`
	TotalMatchingRows int    `json:"total_matching_rows"`
	PairCount         int    `json:"pair_count"`
	CandidateRecords  int    `json:"candidate_records"`
	CoverageRecords   int    `json:"coverage_records"`
	CoveredCandidates int    `json:"covered_candidates"`
}

type t40r1CallerUpstreamWitness struct {
	Schema                 string                         `json:"schema"`
	Method                 string                         `json:"method"`
	Profile                string                         `json:"profile"`
	LeafAdapter            string                         `json:"leaf_adapter"`
	Policy                 string                         `json:"policy"`
	CandidateRecords       int                            `json:"candidate_records"`
	CandidateLeaves        int                            `json:"candidate_leaves"`
	Protocols              int                            `json:"protocols"`
	ExpectedPairs          int                            `json:"expected_pairs"`
	Resolver               t40r1CallerUpstreamResolver    `json:"resolver"`
	WorkerTurns            int                            `json:"worker_turns"`
	CandidatePlanOpens     int                            `json:"candidate_plan_opens"`
	Progress               t40r1CallerUpstreamProgress    `json:"progress"`
	Admission              t40r1CallerUpstreamAdmission   `json:"admission"`
	Publication            t40r1CallerUpstreamPublication `json:"publication"`
	Reader                 t40r1CallerUpstreamReader      `json:"reader"`
	CallerMap              t40r1CallerUpstreamCallerMap   `json:"caller_map"`
	Boundary               string                         `json:"boundary"`
	ClearsUpstreamBoundary bool                           `json:"clears_upstream_boundary"`
	EstablishesScalePass   bool                           `json:"establishes_scale_pass"`
	AuthorizesFreeze       bool                           `json:"authorizes_freeze"`
}

// t40r1CallerUpstreamStore keeps the focused witness on the pre-partitioned
// caller identity used by Take 20 while exposing the real candidate, resolver,
// outcome, admission, and publication store methods. Moving the partitioned
// observation/extraction authority boundary is a separate diagnostic step.
type t40r1CallerUpstreamStore struct {
	callerexecute.Store
}

type t40r1CallerUpstreamPlan struct {
	digest  string
	control uint64
	domains []extract.CandidateManifestDomain
	leaves  []extract.CandidateCallerLeaf
	starts  []int
	counts  []int
}

func (plan *t40r1CallerUpstreamPlan) Identity() string { return plan.digest }

func (plan *t40r1CallerUpstreamPlan) CandidateControlRevision() uint64 {
	return plan.control
}

func (plan *t40r1CallerUpstreamPlan) CorpusFileCount() int {
	return t40r1CallerUpstreamCandidates
}

func (*t40r1CallerUpstreamPlan) GitlinkBoundaries() extract.CandidateManifestGitlinks {
	return extract.CandidateManifestGitlinks{}
}

func (plan *t40r1CallerUpstreamPlan) DomainScope(
	_, _ string,
) (extract.CandidateManifestScope, error) {
	return extract.CandidateManifestScope{
		ManifestDigest: plan.digest, Plane: candidate.PlaneCaller,
		CorpusFileCount:    t40r1CallerUpstreamCandidates,
		CandidateFileCount: t40r1CallerUpstreamCandidates,
		RequiredFileCount:  t40r1CallerUpstreamCandidates,
	}, nil
}

func (*t40r1CallerUpstreamPlan) TypedInput(
	_, _, _ string,
) (extract.CandidateManifestTypedInput, error) {
	return extract.CandidateManifestTypedInput{}, nil
}

func (*t40r1CallerUpstreamPlan) PathInScope(path string) bool {
	return path != ""
}

func (plan *t40r1CallerUpstreamPlan) CallerDomains() []extract.CandidateManifestDomain {
	return slices.Clone(plan.domains)
}

func (plan *t40r1CallerUpstreamPlan) CallerLeaves() []extract.CandidateCallerLeaf {
	return slices.Clone(plan.leaves)
}

func (plan *t40r1CallerUpstreamPlan) ForEachRepositoryFile(
	ctx context.Context,
	_, _ string,
	visit func(extract.CandidateManifestFile) error,
) error {
	return plan.forEachFile(ctx, 0, t40r1CallerUpstreamCandidates, visit)
}

func (plan *t40r1CallerUpstreamPlan) ForEachCallerLeafFile(
	ctx context.Context,
	domain,
	version string,
	leaf extract.CandidateCallerLeaf,
	visit func(extract.CandidateManifestFile) error,
) error {
	domainFound := false
	for _, current := range plan.domains {
		if current.Domain == domain && current.Version == version {
			domainFound = true
			break
		}
	}
	if !domainFound || leaf.Ordinal < 0 || leaf.Ordinal >= len(plan.leaves) ||
		plan.leaves[leaf.Ordinal] != leaf {
		return errors.New("T40.R1 caller upstream plan received an unexpected pair")
	}
	return plan.forEachFile(
		ctx, plan.starts[leaf.Ordinal], plan.counts[leaf.Ordinal], visit,
	)
}

func (*t40r1CallerUpstreamPlan) forEachFile(
	ctx context.Context,
	start,
	count int,
	visit func(extract.CandidateManifestFile) error,
) error {
	for offset := 0; offset < count; offset++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		ordinal := start + offset
		if err := visit(extract.CandidateManifestFile{
			Path:     fmt.Sprintf("semantic/go/b%03d/f%06d.go", ordinal/1_024, ordinal),
			ObjectID: fmt.Sprintf("%040x", ordinal+1), Mode: "100644",
			DeclaredBytes: 512, SourceLane: candidate.SourceLaneBase,
			Required: true,
		}); err != nil {
			return err
		}
	}
	return nil
}

type t40r1CallerUpstreamProvider struct {
	plan  *t40r1CallerUpstreamPlan
	opens int
}

type t40r1CallerPlanProvider interface {
	extract.CandidateCallerPlanProvider
	OpenCount() int
}

func (provider *t40r1CallerUpstreamProvider) OpenCandidateCallerPlan(
	context.Context,
	extract.CandidateManifestRequest,
) (extract.CandidateCallerPlan, error) {
	provider.opens++
	return provider.plan, nil
}

func (provider *t40r1CallerUpstreamProvider) OpenCount() int { return provider.opens }

func TestT40R1CallerTerminalUpstreamWitness(t *testing.T) {
	if os.Getenv(t40r1CallerUpstreamEnv) != "1" {
		t.Skip("set " + t40r1CallerUpstreamEnv + "=1 to run the disk-backed upstream witness")
	}
	if callerleaf.CurrentLeafAdapter != callerleaf.LeafAdapterV2 {
		t.Skip("the retained caller upstream witness is historical V2 evidence")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary is not installed")
	}
	witness := runT40R1CallerTerminalUpstreamWitness(t)
	encoded, err := json.MarshalIndent(witness, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	retainedPath := filepath.Join("caller-terminal-upstream-witness.json")
	raw, err := os.ReadFile(retainedPath)
	if err != nil {
		t.Fatalf("retained caller upstream witness unavailable: %v\n%s", err, encoded)
	}
	var retained t40r1CallerUpstreamWitness
	if err := json.Unmarshal(raw, &retained); err != nil || !reflect.DeepEqual(retained, witness) {
		t.Fatalf("retained caller upstream witness differs: %v\n%s", err, encoded)
	}
	t.Log(string(encoded))
}

func TestT40R1RetainedCallerTerminalUpstreamWitnessIsClosed(t *testing.T) {
	raw, err := os.ReadFile("caller-terminal-upstream-witness.json")
	if err != nil {
		t.Fatal(err)
	}
	var witness t40r1CallerUpstreamWitness
	if err := json.Unmarshal(raw, &witness); err != nil {
		t.Fatal(err)
	}
	if witness.Schema != t40r1CallerUpstreamSchema ||
		witness.Profile != "semantic-262144-v1" ||
		witness.LeafAdapter != callerleaf.LeafAdapterV2 ||
		witness.Policy != callerleaf.PolicyNameV2 ||
		witness.CandidateRecords != t40r1CallerUpstreamCandidates ||
		witness.CandidateLeaves != t40r1CallerUpstreamLeaves ||
		witness.Protocols != 2 || witness.ExpectedPairs != t40r1CallerUpstreamPairs ||
		witness.Boundary != "exact_product_projection" ||
		!witness.ClearsUpstreamBoundary || witness.EstablishesScalePass ||
		witness.AuthorizesFreeze {
		t.Fatalf("retained caller upstream witness is not closed: %+v", witness)
	}
	if !witness.Resolver.PublicationCurrent ||
		witness.Resolver.ManifestMembers != 3 ||
		witness.Resolver.Descriptors["grpc"] != 0 ||
		witness.Resolver.Descriptors["thrift"] != 0 ||
		witness.Progress != (t40r1CallerUpstreamProgress{
			Settled: t40r1CallerUpstreamPairs, Succeeded: t40r1CallerUpstreamPairs,
		}) || witness.Admission.Disposition != string(store.CallerGenerationAdmitted) ||
		witness.Admission.RefusalSummaries != 0 || !witness.Publication.Present ||
		!witness.Publication.AuthorityCurrent ||
		witness.Reader.Availability != string(callerexecute.PublicationCurrent) ||
		witness.Reader.Origin != "current_publication" ||
		witness.CallerMap.SchemaVersion != "caller-map-v2" ||
		witness.CallerMap.MatchingRowsState != "exact" ||
		witness.CallerMap.GenerationState != "current" ||
		!witness.CallerMap.DeclarationExact || witness.CallerMap.Rows != 0 ||
		witness.CallerMap.TotalMatchingRows != 0 {
		t.Fatalf("retained caller upstream authority is not exact: %+v", witness)
	}
}

func runT40R1CallerTerminalUpstreamWitness(
	t *testing.T,
) t40r1CallerUpstreamWitness {
	witness, _ := runT40R1CallerTerminalUpstreamWitnessWithOptions(
		t, t40r1CallerUpstreamRunOptions{},
	)
	return witness
}

type t40r1CallerUpstreamRunOptions struct {
	partitionedAuthority bool
	physicalCallerPlan   bool
}

func runT40R1CallerTerminalUpstreamWitnessWithOptions(
	t *testing.T,
	options t40r1CallerUpstreamRunOptions,
) (t40r1CallerUpstreamWitness, *t40r1CallerPartitionedRun) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	dataDir := t.TempDir()
	state, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })

	if err := state.UpsertRepo(ctx, store.Repo{
		Name:     t40r1CallerUpstreamRepository,
		CloneURL: "https://github.com/phebs/t40r1-caller-upstream-witness.git",
	}); err != nil {
		t.Fatal(err)
	}
	callerRegistry, err := callerexecute.NewRegistry([]extract.Extractor{
		gocaller.NewGRPC(), gocaller.NewThrift(),
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := newT40R1CallerUpstreamPlan(t, callerRegistry)
	var provider t40r1CallerPlanProvider = &t40r1CallerUpstreamProvider{plan: plan}
	headCommit := t40r1CallerUpstreamCommit
	candidatePointer := store.CandidateManifestPublication{
		Repository:       t40r1CallerUpstreamRepository,
		HeadCommit:       headCommit,
		PolicyDigest:     t40r1CallerUpstreamDigest("candidate-policy"),
		ManifestDigest:   plan.digest,
		GenerationDigest: t40r1CallerUpstreamDigest("candidate-generation"),
		ManifestPath:     candidateid.ManifestName(t40r1CallerUpstreamRepository),
	}
	var partitioned *t40r1CallerPartitionedRun
	if options.partitionedAuthority {
		partitioned = prepareT40R1CallerPartitionedRun(
			t, ctx, dataDir, state, callerRegistry, options.physicalCallerPlan,
		)
		headCommit = partitioned.commit
		plan.digest = partitioned.manifest.Digest
		candidatePointer = store.CandidateManifestPublication{
			Repository:       t40r1CallerUpstreamRepository,
			HeadCommit:       headCommit,
			PolicyDigest:     partitioned.manifest.PolicyDigest,
			ManifestDigest:   partitioned.manifest.Digest,
			GenerationDigest: partitioned.manifest.GenerationDigest,
			ManifestPath:     candidateid.ManifestName(t40r1CallerUpstreamRepository),
		}
	}
	if err := state.SetRepoIndexed(
		ctx, t40r1CallerUpstreamRepository, headCommit, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.PublishCandidateManifest(ctx, candidatePointer); err != nil {
		t.Fatal(err)
	}
	persistedCandidate, err := state.GetCandidateManifestPublication(
		ctx, t40r1CallerUpstreamRepository,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan.control = persistedCandidate.ControlRevision
	if partitioned != nil {
		partitioned.publish(t, ctx, state)
	}
	if options.physicalCallerPlan {
		physicalProvider := newT40R1CallerPhysicalProvider(
			t, dataDir, state, partitioned, callerRegistry,
		)
		provider = physicalProvider
		partitioned.physicalProvider = physicalProvider
		if _, err := os.Stat(candidatejob.CandidateRoot(dataDir)); err != nil {
			t.Fatalf("physical candidate root before resolver build: %v", err)
		}
	}

	declarationRun, declarationGeneration :=
		publishT40R1CallerUpstreamDeclaration(t, ctx, state, headCommit)
	resolverExtractors := []extract.Extractor{
		gocaller.NewGRPC(), gocaller.NewThrift(),
		protodecl.New(), thriftdecl.New(),
	}
	resolverRegistry, err := resolvermaterialize.NewRegistry(resolverExtractors)
	if err != nil {
		t.Fatal(err)
	}
	identityDeclarations := []resolvercatalog.DeclarationPublication{{
		Domain: "proto-contract", RunID: declarationRun.ID,
		GenerationDigest: declarationGeneration,
	}}
	resolverIdentity, err := resolvercatalog.NewIdentity(
		t40r1CallerUpstreamRepository, headCommit, "",
		persistedCandidate.ManifestDigest, identityDeclarations,
		resolverRegistry.Packs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverRoot := filepath.Join(dataDir, "resolver-catalogs")
	if err := os.MkdirAll(resolverRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	preparedResolver, err := resolvermaterialize.Build(ctx, resolvermaterialize.BuildRequest{
		Root: resolverRoot, RepositoryDir: dataDir, Identity: resolverIdentity,
		Registry: resolverRegistry, Manifest: plan,
		Declarations: []resolvermaterialize.DeclarationInput{{
			Protocol: resolvermaterialize.ProtocolGRPC,
			Domain:   "proto-contract", RunID: declarationRun.ID,
			GenerationDigest: declarationGeneration,
		}},
		Assertions: state,
		ReadBlob: func(context.Context, string, string, int64) ([]byte, error) {
			return nil, errors.New("empty resolver materialization attempted a source read")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = preparedResolver.Discard() })
	resolverState, err := preparedResolver.Publish(
		ctx, func(commitCtx context.Context, current resolvercatalog.State) error {
			return state.PublishResolverCatalog(
				commitCtx, t40r1CallerUpstreamResolverPointer(current),
			)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	persistedResolver, err := state.GetResolverCatalogPublication(
		ctx, t40r1CallerUpstreamRepository,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverCurrent, err := state.ResolverCatalogPublicationCurrent(ctx, *persistedResolver)
	if err != nil {
		t.Fatal(err)
	}

	publications := callerpublication.NewRegistry(callerexecute.Root(dataDir))
	t.Cleanup(func() { _ = publications.Close() })
	var boundaryStore callerexecute.Store = &t40r1CallerUpstreamStore{Store: state}
	if partitioned != nil {
		boundaryStore = state
	}
	worker, err := callerexecute.NewWorkerWithPublicationRegistry(
		dataDir, boundaryStore, provider, callerRegistry, publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.CancelPendingJobs(
		ctx, store.JobCallerLeaf, t40r1CallerUpstreamRepository,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := state.EnqueuePending(
		ctx, store.JobCallerLeaf, t40r1CallerUpstreamRepository, false,
	); err != nil {
		t.Fatal(err)
	}
	turns := driveT40R1CallerUpstreamWorker(t, ctx, state, worker)

	pointer, err := state.GetCallerGenerationPublication(
		ctx, t40r1CallerUpstreamRepository,
	)
	if err != nil {
		t.Fatal(err)
	}
	pointerCurrent, err := state.CallerGenerationPublicationCurrent(ctx, *pointer)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := state.GetCallerLeafOutcomeProgress(ctx, pointer.Generation)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := state.GetCallerGenerationAdmission(ctx, pointer.Generation)
	if err != nil {
		t.Fatal(err)
	}

	resolverPublication, resolverViews, err := resolvermaterialize.OpenCallerResolvers(
		ctx, resolverRoot, resolverState,
		[]resolvermaterialize.Protocol{
			resolvermaterialize.ProtocolGRPC, resolvermaterialize.ProtocolThrift,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverDescriptors := map[string]int{
		"grpc":   len(resolverViews[resolvermaterialize.ProtocolGRPC].Descriptors()),
		"thrift": len(resolverViews[resolvermaterialize.ProtocolThrift].Descriptors()),
	}

	reader, err := callerexecute.NewPublicationReader(
		dataDir, boundaryStore, callerRegistry, publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	read, err := reader.Open(ctx, t40r1CallerUpstreamRepository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Release() }()

	callerMap := api.NewCallerMapService(api.Options{
		Version: "t40r1-caller-upstream-witness", Store: state, Evidence: state,
		DataDir: dataDir, CallerMapEnabled: true, CallerReader: reader,
		Principal: func(context.Context) string { return "user:t40r1-witness" },
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repository store.Repo) bool {
				return repository.Name == t40r1CallerUpstreamRepository
			}
		},
		AuthorizationProvider: "t40r1-witness-visibility-v1",
	})
	if callerMap == nil {
		t.Fatal("exact Caller Map service was not constructed")
	}
	page, err := callerMap.List(ctx, api.CallerMapQuery{
		Endpoint: api.CallerMapEndpoint{
			Protocol: "protobuf", Repository: t40r1CallerUpstreamRepository,
			Lineage:   t40r1CallerUpstreamLineage,
			Operation: "/" + t40r1CallerUpstreamOperation,
		},
	}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if page == nil || page.Generation == nil || page.Generation.RecordCounts == nil ||
		page.TotalMatchingRows == nil {
		t.Fatalf("exact Caller Map projection is incomplete: %+v", page)
	}

	witness := t40r1CallerUpstreamWitness{
		Schema:  t40r1CallerUpstreamSchema,
		Method:  "the retained semantic caller cardinality is replayed through a production-built and filesystem-published resolver catalog, the production resolver cache and caller worker, disk-backed outcomes/admission/publication, the shared exact reader, and an authorized exact Caller Map zero-row projection",
		Profile: "semantic-262144-v1", LeafAdapter: callerleaf.CurrentLeafAdapter,
		Policy:           callerleaf.CurrentPolicy().Name,
		CandidateRecords: t40r1CallerUpstreamCandidates,
		CandidateLeaves:  t40r1CallerUpstreamLeaves,
		Protocols:        len(callerRegistry.Adapters()), ExpectedPairs: t40r1CallerUpstreamPairs,
		Resolver: t40r1CallerUpstreamResolver{
			PublicationCurrent: resolverCurrent,
			ManifestMembers:    len(resolverPublication.Manifest().Members),
			Descriptors:        resolverDescriptors,
		},
		WorkerTurns: turns, CandidatePlanOpens: provider.OpenCount(),
		Progress: t40r1CallerUpstreamProgress{
			Settled: progress.SettledCount, Succeeded: progress.SucceededCount,
			Refused: progress.RefusedCount,
		},
		Admission: t40r1CallerUpstreamAdmission{
			Disposition: string(admission.Disposition), PairCount: admission.PairCount,
			ArtifactCount:     admission.ArtifactCount,
			ResultRecords:     admission.ResultCount,
			AbstentionRecords: admission.AbstentionCount,
			CoverageRecords:   admission.CoverageRecordCount,
			CoveredCandidates: admission.CoveredCandidateCount,
			CanonicalBytes:    admission.CanonicalBytes,
			StagingBytes:      admission.StagingBytes,
			RefusalSummaries:  len(admission.Refusals),
		},
		Publication: t40r1CallerUpstreamPublication{
			Present: true, AuthorityCurrent: pointerCurrent,
			PairCount: pointer.PairCount, ResultRecords: pointer.ResultCount,
			AbstentionRecords: pointer.AbstentionCount,
			CoverageRecords:   pointer.CoverageRecordCount,
			CoveredCandidates: pointer.CoveredCandidateCount,
		},
		Reader: t40r1CallerUpstreamReader{
			Availability: string(read.Availability),
			Origin:       t40r1CallerUpstreamReadOrigin(read),
		},
		CallerMap: t40r1CallerUpstreamCallerMap{
			SchemaVersion:     page.SchemaVersion,
			MatchingRowsState: page.MatchingRowsState,
			GenerationState:   page.Generation.State,
			DeclarationExact: page.Declaration != nil &&
				page.Declaration.Predicate == "DECLARES_OPERATION" &&
				page.Declaration.Object == t40r1CallerUpstreamOperation,
			Rows: len(page.Rows), TotalMatchingRows: *page.TotalMatchingRows,
			PairCount:         page.Generation.PairCount,
			CandidateRecords:  page.Generation.RecordCounts.CandidateRecords,
			CoverageRecords:   page.Generation.CoverageRecordCount,
			CoveredCandidates: page.Generation.CoveredCandidateCount,
		},
		Boundary: "exact_product_projection",
	}
	if options.physicalCallerPlan {
		witness.Method = "the exact physical candidate caller plan is opened through the production store-bound provider and its immutable leaf members are replayed through the partitioned authority, resolver, caller worker, durable publication, shared reader, and exact Caller Map"
		witness.Profile = "physical-provider-control-v1"
		witness.CandidateRecords = partitioned.callerRecordCount()
		witness.CandidateLeaves = len(partitioned.manifest.CallerLeaves)
		witness.ExpectedPairs = len(callerRegistry.Adapters()) * witness.CandidateLeaves
		witness.Boundary = "physical_candidate_plan_provider_membership"
	}
	witness.ClearsUpstreamBoundary =
		witness.Resolver.PublicationCurrent && witness.Resolver.ManifestMembers == 3 &&
			witness.Resolver.Descriptors["grpc"] == 0 &&
			witness.Resolver.Descriptors["thrift"] == 0 &&
			witness.Progress == (t40r1CallerUpstreamProgress{
				Settled: witness.ExpectedPairs, Succeeded: witness.ExpectedPairs,
			}) && witness.Admission.Disposition == string(store.CallerGenerationAdmitted) &&
			witness.Admission.PairCount == witness.ExpectedPairs &&
			witness.Admission.RefusalSummaries == 0 &&
			witness.Admission.ResultRecords == 0 && witness.Admission.AbstentionRecords == 0 &&
			witness.Admission.CoverageRecords == witness.ExpectedPairs &&
			witness.Admission.CoveredCandidates ==
				witness.Protocols*witness.CandidateRecords &&
			witness.Publication.Present && witness.Publication.AuthorityCurrent &&
			read.Availability == callerexecute.PublicationCurrent &&
			witness.Reader.Origin == "current_publication" &&
			witness.CallerMap.SchemaVersion == "caller-map-v2" &&
			witness.CallerMap.MatchingRowsState == "exact" &&
			witness.CallerMap.GenerationState == "current" &&
			witness.CallerMap.DeclarationExact && witness.CallerMap.Rows == 0 &&
			witness.CallerMap.TotalMatchingRows == 0 &&
			witness.CallerMap.PairCount == witness.Publication.PairCount &&
			witness.CallerMap.CandidateRecords == witness.CandidateRecords &&
			witness.CallerMap.CoverageRecords == witness.Publication.CoverageRecords &&
			witness.CallerMap.CoveredCandidates == witness.Publication.CoveredCandidates
	if !witness.ClearsUpstreamBoundary {
		t.Fatalf("caller upstream witness did not clear: %+v", witness)
	}
	if partitioned != nil {
		partitioned.close(t, pointer)
	}
	return witness, partitioned
}

func publishT40R1CallerUpstreamDeclaration(
	t *testing.T,
	ctx context.Context,
	state *store.Surreal,
	commit string,
) (*store.ExtractionRun, string) {
	t.Helper()
	run, err := state.BeginExtractionRun(ctx, store.ExtractionScope{
		Repository: t40r1CallerUpstreamRepository,
		Commit:     commit,
		Domain:     "proto-contract",
	}, "t40r1-witness-proto-declaration-v1")
	if err != nil {
		t.Fatal(err)
	}
	atom := store.EvidenceAtom{
		SchemaVersion: "t40r1-witness-v1",
		BlobDigest:    t40r1CallerUpstreamDigest("declaration-source"),
		StartByte:     0, EndByte: 21, RuleID: "t40r1-declaration-v1",
		ExtractorVersion: "1.0.0", AdapterConfigDigest: "none",
		FactFingerprint: t40r1CallerUpstreamOperation,
	}
	atom.ID = store.ComputeAtomID(atom)
	association := store.SnapshotEvidence{
		AtomID: atom.ID, Repo: t40r1CallerUpstreamRepository,
		Commit: commit, Path: "idl/orders.proto",
		StartLine: 1, EndLine: 1,
		VisibilityScope: "repo:" + t40r1CallerUpstreamRepository,
	}
	assertion := store.Assertion{
		Predicate: "DECLARES_OPERATION", Subject: "idl/orders.proto",
		Object: t40r1CallerUpstreamOperation, Lineage: t40r1CallerUpstreamLineage,
		Tier: store.TierExact, CodeRole: "production",
		Repo: t40r1CallerUpstreamRepository, Supporting: []string{atom.ID},
		Detail: `{"schema":"proto-operation-detail-v1"}`,
	}
	if err := state.AddEvidence(
		ctx, run.ID, []store.EvidenceAtom{atom},
		[]store.SnapshotEvidence{association}, []store.Assertion{assertion},
	); err != nil {
		t.Fatal(err)
	}
	candidatePointer, err := state.GetCandidateManifestPublication(
		ctx, t40r1CallerUpstreamRepository,
	)
	if err != nil {
		t.Fatal(err)
	}
	generation := store.ExtractionGenerationIdentity{
		CandidateManifestDigest:  candidatePointer.ManifestDigest,
		CandidatePolicyDigest:    candidatePointer.PolicyDigest,
		CandidateControlRevision: candidatePointer.ControlRevision,
		Extractor:                run.Extractor,
		InventoryPolicy:          "candidate-manifest-v4-caller-upstream-witness",
		DependencyDigest:         t40r1CallerUpstreamDigest("declaration-dependencies"),
	}
	generation.Digest = store.ComputeExtractionGenerationDigest(generation)
	coverage := store.CoverageManifest{
		Protocols: []string{"protobuf"}, CorpusFileCount: 1,
		CandidateFileCount: 1, ReadFileCount: 1, ReadBytes: 21,
		SourceScopeDigest: t40r1CallerUpstreamDigest("declaration-scope"),
		AssertionCount:    1, AtomCount: 1,
		CandidateManifestDigest:  candidatePointer.ManifestDigest,
		ScopePosture:             "whole-repository",
		CandidatePlane:           "repository",
		ScopeCorpusFileCount:     1,
		ScopeCorpusDeclaredBytes: 21,
		ScopeCorpusDigest:        t40r1CallerUpstreamDigest("declaration-corpus"),
		PlannedFileCount:         1,
		PlannedRequiredFileCount: 1,
		PlannedDeclaredBytes:     21,
		PlannedScopeDigest:       t40r1CallerUpstreamDigest("declaration-plan"),
	}
	outcome := store.ExtractionDomainOutcome{
		Scope: store.ExtractionScope{
			Repository: t40r1CallerUpstreamRepository,
			Commit:     commit,
			Domain:     "proto-contract",
		},
		Disposition:   store.DomainOutcomePublished,
		Generation:    generation,
		RunID:         run.ID,
		ReceiptSchema: store.ExtractionOutcomeReceiptSchema,
		Receipt:       `{"schema":"` + store.ExtractionOutcomeReceiptSchema + `"}`,
	}
	if err := state.PublishExtractionRunWithOutcome(
		ctx, run.ID, coverage, outcome,
	); err != nil {
		t.Fatal(err)
	}
	return run, generation.Digest
}

func newT40R1CallerUpstreamPlan(
	t *testing.T,
	registry *callerexecute.Registry,
) *t40r1CallerUpstreamPlan {
	t.Helper()
	plan := &t40r1CallerUpstreamPlan{
		digest:  t40r1CallerUpstreamDigest("candidate-manifest"),
		domains: registry.CandidateDomains(),
		leaves:  make([]extract.CandidateCallerLeaf, t40r1CallerUpstreamLeaves),
		starts:  make([]int, t40r1CallerUpstreamLeaves),
		counts:  make([]int, t40r1CallerUpstreamLeaves),
	}
	base := t40r1CallerUpstreamCandidates / t40r1CallerUpstreamLeaves
	remainder := t40r1CallerUpstreamCandidates % t40r1CallerUpstreamLeaves
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
			ContentDigest: t40r1CallerUpstreamDigest(fmt.Sprintf("candidate-leaf-%d", ordinal)),
		}
		start += count
	}
	if start != t40r1CallerUpstreamCandidates {
		t.Fatalf("caller upstream candidate census = %d", start)
	}
	return plan
}

func driveT40R1CallerUpstreamWorker(
	t *testing.T,
	ctx context.Context,
	state *store.Surreal,
	worker *callerexecute.Worker,
) int {
	t.Helper()
	publicationSeen := false
	for turn := 1; turn <= t40r1CallerUpstreamPairs+4; turn++ {
		job, err := state.ClaimJob(
			ctx, store.JobCallerLeaf, "t40r1-caller-upstream-witness",
		)
		if err != nil {
			t.Fatalf("claim caller upstream turn %d: %v", turn, err)
		}
		if err := state.SetJobStatus(ctx, *job, store.StatusRunning, ""); err != nil {
			t.Fatal(err)
		}
		job.Status = store.StatusRunning
		if err := worker.Handle(ctx, *job); err != nil {
			t.Fatalf("caller upstream worker turn %d failed: %v", turn, err)
		}
		if err := state.SetJobStatus(ctx, *job, store.StatusDone, ""); err != nil {
			t.Fatal(err)
		}
		pointer, pointerErr := state.GetCallerGenerationPublication(
			ctx, t40r1CallerUpstreamRepository,
		)
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
	t.Fatal("caller upstream witness did not reach a current publication and no-op")
	return 0
}

func t40r1CallerUpstreamResolverPointer(
	current resolvercatalog.State,
) store.ResolverCatalogPublication {
	declarations := make(
		[]store.ResolverCatalogDeclarationPublication, len(current.Declarations),
	)
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
		ManifestDigest:        current.ManifestDigest, ManifestPath: current.Manifest,
	}
}

func t40r1CallerUpstreamReadOrigin(read *callerexecute.PublicationRead) string {
	if read == nil {
		return "no_read"
	}
	if read.Availability == callerexecute.PublicationCurrent {
		return "current_publication"
	}
	if read.Availability == callerexecute.PublicationFailed && read.Admission != nil &&
		read.Admission.Disposition == store.CallerGenerationTerminalGenerationRefusal {
		return "terminal_admission"
	}
	if read.Availability == callerexecute.PublicationFailed {
		return "deterministic_publication_rejection"
	}
	return string(read.Availability)
}

func t40r1CallerUpstreamDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

var _ extract.CandidateManifest = (*t40r1CallerUpstreamPlan)(nil)
var _ extract.CandidateCallerPlan = (*t40r1CallerUpstreamPlan)(nil)
var _ extract.CandidateCallerPlanProvider = (*t40r1CallerUpstreamProvider)(nil)
var _ callerexecute.Store = (*t40r1CallerUpstreamStore)(nil)
