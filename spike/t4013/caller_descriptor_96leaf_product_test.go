package t4013

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	t40r1Descriptor96Schema       = "t4013-descriptor-present-96leaf-product-v1"
	t40r1Descriptor96Env          = "T40R1_DESCRIPTOR_96LEAF_PRODUCT"
	t40r1Descriptor96Repo         = "github.com/phebs/t40r1-descriptor-96leaf-product"
	t40r1Descriptor96Files        = t40r1CallerProvider96Files + 1
	t40r1Descriptor96Leaves       = t40r1CallerProvider96Leaves
	t40r1Descriptor96ConsumerPath = "consumer/early000111.go"
	t40r1Descriptor96ExtraPath    = "semantic/go/extra000015.go"
	t40r1Descriptor96Profile      = "physical-261770-96leaf-grpc-product-v1"
	t40r1Descriptor96BlobGroup    = 4_096
)

type t40r1Descriptor96Physical struct {
	RegularFiles          int                           `json:"regular_files"`
	UniqueGitBlobs        int                           `json:"unique_git_blobs"`
	CandidateRecords      int                           `json:"candidate_records"`
	CallerLeaves          int                           `json:"caller_leaves"`
	LeafSetDigest         string                        `json:"leaf_set_digest"`
	LeafContentBytes      int64                         `json:"leaf_content_bytes"`
	MinLeafRecords        int                           `json:"min_leaf_records"`
	MaxLeafRecords        int                           `json:"max_leaf_records"`
	PrefixDistribution    []t40r1CallerProvider96Prefix `json:"prefix_distribution"`
	PeakSpoolBytes        int64                         `json:"peak_spool_bytes"`
	ManifestDigestBound   bool                          `json:"manifest_digest_bound"`
	ControlRevisionBound  bool                          `json:"control_revision_bound"`
	ProviderLeavesExact   bool                          `json:"provider_leaves_exact"`
	ConsumerPath          string                        `json:"consumer_path"`
	ConsumerLeafOrdinal   int                           `json:"consumer_leaf_ordinal"`
	ConsumerLeafPrefix    string                        `json:"consumer_leaf_prefix"`
	ConsumerObjectExact   bool                          `json:"consumer_object_exact"`
	ConsumerVisitedBefore int                           `json:"consumer_visited_before"`
}

type t40r1Descriptor96Resolver struct {
	PublicationCurrent       bool  `json:"publication_current"`
	ManifestMembers          int   `json:"manifest_members"`
	MaterializationBlobReads int   `json:"materialization_blob_reads"`
	MaterializationBlobBytes int64 `json:"materialization_blob_bytes"`
	Descriptors              int   `json:"descriptors"`
	OperationExact           bool  `json:"operation_exact"`
	LineageExact             bool  `json:"lineage_exact"`
	GeneratedObjectExact     bool  `json:"generated_object_exact"`
}

type t40r1Descriptor96Authority struct {
	ObservationVersion         string `json:"observation_version"`
	ObservationRecords         int    `json:"observation_records"`
	ObservedRecords            int    `json:"observed_records"`
	CandidatePartitions        int    `json:"candidate_partitions"`
	DomainDisposition          string `json:"domain_disposition"`
	PlanRootCurrent            bool   `json:"plan_root_current"`
	UpstreamUsable             bool   `json:"upstream_usable"`
	WorkerUpstreamDigestExact  bool   `json:"worker_upstream_digest_exact"`
	ProductUpstreamDigestExact bool   `json:"product_upstream_digest_exact"`
}

type t40r1Descriptor96Refusal struct {
	Stage          string `json:"stage"`
	GenerationKind string `json:"generation_kind"`
	Classification string `json:"classification"`
	Dimension      string `json:"dimension"`
	Observed       int64  `json:"observed"`
	Limit          int64  `json:"limit"`
	OutcomeCount   int    `json:"outcome_count"`
}

type t40r1Descriptor96Pipeline struct {
	WorkerTurns       int                        `json:"worker_turns"`
	ExpectedPairs     int                        `json:"expected_pairs"`
	SettledPairs      int                        `json:"settled_pairs"`
	SucceededPairs    int                        `json:"succeeded_pairs"`
	RefusedPairs      int                        `json:"refused_pairs"`
	Disposition       string                     `json:"disposition"`
	Artifacts         int                        `json:"artifacts"`
	ResultRecords     int                        `json:"result_records"`
	AbstentionRecords int                        `json:"abstention_records"`
	SourceBlobReads   int                        `json:"source_blob_reads"`
	SourceBlobBytes   int64                      `json:"source_blob_bytes"`
	OutOfLeafReads    int                        `json:"out_of_leaf_reads"`
	Refusals          []t40r1Descriptor96Refusal `json:"refusals"`
	PublicationAbsent bool                       `json:"publication_absent"`
}

type t40r1Descriptor96Product struct {
	ReaderAvailability    string                     `json:"reader_availability"`
	ReaderOrigin          string                     `json:"reader_origin"`
	SchemaVersion         string                     `json:"schema_version"`
	MatchingRowsState     string                     `json:"matching_rows_state"`
	GenerationState       string                     `json:"generation_state"`
	Rows                  int                        `json:"rows"`
	TotalsUnavailable     bool                       `json:"totals_unavailable"`
	ProgressState         string                     `json:"progress_state"`
	SettledPairs          int                        `json:"settled_pairs"`
	SucceededPairs        int                        `json:"succeeded_pairs"`
	RefusedPairs          int                        `json:"refused_pairs"`
	TotalPairs            int                        `json:"total_pairs"`
	Refusals              []t40r1Descriptor96Refusal `json:"refusals"`
	ProgressRefusalParity bool                       `json:"progress_refusal_parity"`
}

type t40r1Descriptor96Witness struct {
	Schema               string                     `json:"schema"`
	Method               string                     `json:"method"`
	Profile              string                     `json:"profile"`
	Physical             t40r1Descriptor96Physical  `json:"physical"`
	Resolver             t40r1Descriptor96Resolver  `json:"resolver"`
	Authority            t40r1Descriptor96Authority `json:"authority"`
	Pipeline             t40r1Descriptor96Pipeline  `json:"pipeline"`
	Product              t40r1Descriptor96Product   `json:"product"`
	Boundary             string                     `json:"boundary"`
	ReproducesRefusal    bool                       `json:"reproduces_refusal"`
	EstablishesScalePass bool                       `json:"establishes_scale_pass"`
	AuthorizesCeremony   bool                       `json:"authorizes_ceremony"`
}

type t40r1Descriptor96Measurement struct {
	CandidateBuild     time.Duration
	ResolverBuild      time.Duration
	ObservationBuild   time.Duration
	WorkerRun          time.Duration
	ProductRead        time.Duration
	BuildAllocated     uint64
	WorkerAllocated    uint64
	CandidateDiskBytes int64
	ObservationDisk    int64
	CallerDisk         int64
}

func TestT40R1DescriptorPresent96LeafPhysicalWorkerProductDiagnostic(t *testing.T) {
	if os.Getenv(t40r1Descriptor96Env) != "1" {
		t.Skip("set " + t40r1Descriptor96Env + "=1 to run the descriptor-present 96-leaf product diagnostic")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary is not installed")
	}
	witness, measurement := runT40R1Descriptor96Diagnostic(t)
	encoded, err := json.MarshalIndent(witness, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"candidate=%s resolver=%s observation=%s worker=%s product=%s build_alloc=%d worker_alloc=%d candidate_disk=%d observation_disk=%d caller_disk=%d",
		measurement.CandidateBuild, measurement.ResolverBuild,
		measurement.ObservationBuild, measurement.WorkerRun, measurement.ProductRead,
		measurement.BuildAllocated, measurement.WorkerAllocated,
		measurement.CandidateDiskBytes, measurement.ObservationDisk, measurement.CallerDisk,
	)
	raw, err := os.ReadFile("descriptor-present-96leaf-product.json")
	if err != nil {
		t.Fatalf("retained descriptor-present 96-leaf witness unavailable: %v\n%s", err, encoded)
	}
	var retained t40r1Descriptor96Witness
	if err := json.Unmarshal(raw, &retained); err != nil || !reflect.DeepEqual(retained, witness) {
		t.Fatalf("retained descriptor-present 96-leaf witness differs: %v\n%s", err, encoded)
	}
	t.Log(string(encoded))
}

func TestT40R1RetainedDescriptorPresent96LeafPhysicalWorkerProductIsClosed(t *testing.T) {
	raw, err := os.ReadFile("descriptor-present-96leaf-product.json")
	if err != nil {
		t.Skip("retained descriptor-present 96-leaf witness is not authored yet")
	}
	var witness t40r1Descriptor96Witness
	if err := json.Unmarshal(raw, &witness); err != nil {
		t.Fatal(err)
	}
	if witness.Schema != t40r1Descriptor96Schema ||
		witness.Profile != t40r1Descriptor96Profile ||
		witness.Physical.RegularFiles != t40r1Descriptor96Files ||
		witness.Physical.UniqueGitBlobs != 72 ||
		witness.Physical.CandidateRecords != 261_769 ||
		witness.Physical.CallerLeaves != t40r1Descriptor96Leaves ||
		witness.Physical.LeafSetDigest !=
			"sha256:d8854d25ad69f3ad5510f2400813ebbb2f62c19fd4c8803924b503c0a669c37b" ||
		witness.Physical.LeafContentBytes != 85_598_421 ||
		witness.Physical.MinLeafRecords != 1_953 || witness.Physical.MaxLeafRecords != 4_096 ||
		!reflect.DeepEqual(witness.Physical.PrefixDistribution, []t40r1CallerProvider96Prefix{
			{Bits: 6, Leaves: 32, Records: 129_114, MinRecords: 3_946, MaxRecords: 4_096},
			{Bits: 7, Leaves: 64, Records: 132_655, MinRecords: 1_953, MaxRecords: 2_166},
		}) || witness.Physical.PeakSpoolBytes != 107_066_282 ||
		!witness.Physical.ManifestDigestBound || !witness.Physical.ControlRevisionBound ||
		!witness.Physical.ProviderLeavesExact ||
		witness.Physical.ConsumerPath != t40r1Descriptor96ConsumerPath ||
		witness.Physical.ConsumerLeafOrdinal != 0 ||
		witness.Physical.ConsumerLeafPrefix != "000000" ||
		!witness.Physical.ConsumerObjectExact ||
		witness.Physical.ConsumerVisitedBefore != 1_522 ||
		!witness.Resolver.PublicationCurrent || witness.Resolver.ManifestMembers != 2 ||
		witness.Resolver.MaterializationBlobReads != 4 ||
		witness.Resolver.MaterializationBlobBytes != 713 || witness.Resolver.Descriptors != 1 ||
		!witness.Resolver.OperationExact || !witness.Resolver.LineageExact ||
		!witness.Resolver.GeneratedObjectExact ||
		witness.Authority.ObservationVersion != observationpublication.DownstreamAuthorityV2 ||
		witness.Authority.ObservationRecords != 67 || witness.Authority.ObservedRecords != 67 ||
		witness.Authority.CandidatePartitions != 0 ||
		witness.Authority.DomainDisposition != candidate.PartitionResultUnavailablePrerequisite ||
		!witness.Authority.PlanRootCurrent || !witness.Authority.UpstreamUsable ||
		!witness.Authority.WorkerUpstreamDigestExact ||
		!witness.Authority.ProductUpstreamDigestExact ||
		witness.Pipeline.WorkerTurns != 40 ||
		witness.Pipeline.ExpectedPairs != t40r1Descriptor96Leaves ||
		witness.Pipeline.SettledPairs != t40r1Descriptor96Leaves ||
		witness.Pipeline.SucceededPairs != 38 || witness.Pipeline.RefusedPairs != 58 ||
		witness.Pipeline.Disposition != string(store.CallerGenerationTerminalGenerationRefusal) ||
		witness.Pipeline.Artifacts != 38 || witness.Pipeline.ResultRecords != 1 ||
		witness.Pipeline.AbstentionRecords != 100_245 ||
		witness.Pipeline.SourceBlobReads != 100_243 ||
		witness.Pipeline.SourceBlobBytes != 9_222_381 ||
		witness.Pipeline.OutOfLeafReads != 0 || len(witness.Pipeline.Refusals) != 1 ||
		witness.Pipeline.Refusals[0].Stage != string(pipelinerefusal.StageCallerGenerationAdmission) ||
		witness.Pipeline.Refusals[0].GenerationKind != string(pipelinerefusal.GenerationCaller) ||
		witness.Pipeline.Refusals[0].Classification != string(pipelinerefusal.ClassificationLimit) ||
		witness.Pipeline.Refusals[0].Dimension != string(pipelinerefusal.DimensionCallerGenerationAbstentions) ||
		witness.Pipeline.Refusals[0].Observed != 100_245 ||
		witness.Pipeline.Refusals[0].Limit != 100_000 ||
		witness.Pipeline.Refusals[0].OutcomeCount != 58 ||
		!witness.Pipeline.PublicationAbsent ||
		witness.Product.ReaderAvailability != string(callerexecute.PublicationFailed) ||
		witness.Product.ReaderOrigin != "terminal_admission" ||
		witness.Product.SchemaVersion != "caller-map-v2" ||
		witness.Product.MatchingRowsState != "unavailable" ||
		witness.Product.GenerationState != "failed" || witness.Product.Rows != 0 ||
		!witness.Product.TotalsUnavailable || witness.Product.ProgressState != "complete" ||
		witness.Product.SettledPairs != witness.Pipeline.SettledPairs ||
		witness.Product.SucceededPairs != witness.Pipeline.SucceededPairs ||
		witness.Product.RefusedPairs != witness.Pipeline.RefusedPairs ||
		witness.Product.TotalPairs != witness.Pipeline.ExpectedPairs ||
		!witness.Product.ProgressRefusalParity ||
		witness.Boundary != "descriptor_present_96_leaf_worker_product_refusal" ||
		!witness.ReproducesRefusal || witness.EstablishesScalePass || witness.AuthorizesCeremony {
		t.Fatalf("retained descriptor-present 96-leaf witness is not closed: %+v", witness)
	}
}

func runT40R1Descriptor96Diagnostic(
	t *testing.T,
) (t40r1Descriptor96Witness, t40r1Descriptor96Measurement) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Minute)
	defer cancel()
	dataDir := t.TempDir()
	state, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	repositoryDirectory, commit, objects := t40r1Descriptor96Repository(t, ctx)
	measurement := t40r1Descriptor96Measurement{}

	extractors := []extract.Extractor{gocaller.NewGRPC(), protodecl.New()}
	policies, err := extract.CandidatePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	policySet, err := candidatejob.CompilePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	callerRegistry, err := callerexecute.NewRegistry(extractors)
	if err != nil {
		t.Fatal(err)
	}
	resolverRegistry, err := resolvermaterialize.NewRegistry(extractors)
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot := candidatejob.CandidateRoot(dataDir)
	if err := os.MkdirAll(candidateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	var buildMetrics candidate.BuildMetrics
	buildBefore := t40r1CallerProvider96MemStats()
	started := time.Now()
	manifest, err := candidate.Build(ctx, candidate.Request{
		RepoDir: repositoryDirectory, OutputDir: candidateRoot,
		Repository: t40r1Descriptor96Repo, Commit: commit,
		Policies: policies, Metrics: &buildMetrics,
	})
	measurement.CandidateBuild = time.Since(started)
	measurement.BuildAllocated = t40r1CallerProvider96AllocatedSince(buildBefore)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.UpsertRepo(ctx, store.Repo{
		Name:     t40r1Descriptor96Repo,
		CloneURL: "https://github.com/phebs/t40r1-descriptor-96leaf.invalid.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetRepoIndexed(ctx, t40r1Descriptor96Repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := state.PublishCandidateManifest(ctx, store.CandidateManifestPublication{
		Repository: t40r1Descriptor96Repo, HeadCommit: commit,
		PolicyDigest: manifest.PolicyDigest, ManifestDigest: manifest.Digest,
		GenerationDigest: manifest.GenerationDigest,
		ManifestPath:     candidate.ManifestName(t40r1Descriptor96Repo),
	}); err != nil {
		t.Fatal(err)
	}
	candidatePointer, err := state.GetCandidateManifestPublication(ctx, t40r1Descriptor96Repo)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := candidatejob.NewProvider(dataDir, state, policySet)
	if err != nil {
		t.Fatal(err)
	}
	manifestView, err := provider.OpenCandidateManifest(ctx, extract.CandidateManifestRequest{
		Repository: t40r1Descriptor96Repo, Commit: commit,
		Domains: resolverRegistry.CandidateDomains(),
	})
	if err != nil {
		t.Fatal(err)
	}
	callerPlan, err := provider.OpenCandidateCallerPlan(ctx, extract.CandidateManifestRequest{
		Repository: t40r1Descriptor96Repo, Commit: commit,
		Domains: callerRegistry.CandidateDomains(),
	})
	if err != nil {
		t.Fatal(err)
	}
	leafDigest, prefixDistribution, leafContentBytes, _, minLeaf, maxLeaf :=
		t40r1CallerLeafSummaryForSchema(t, t40r1Descriptor96Schema, manifest.CallerLeaves)
	consumerLeaf, consumerBefore := t40r1Descriptor96ConsumerLeaf(
		t, ctx, callerPlan, objects[t40r1Descriptor96ConsumerPath],
	)

	declarationRun, declarationGeneration := t40r1PublishDescriptor96Declaration(
		t, ctx, state, *candidatePointer, commit,
	)
	resolverIdentity, err := resolvercatalog.NewIdentity(
		t40r1Descriptor96Repo, commit, "", candidatePointer.ManifestDigest,
		[]resolvercatalog.DeclarationPublication{{
			Domain: "proto-contract", RunID: declarationRun.ID,
			GenerationDigest: declarationGeneration,
		}},
		resolverRegistry.Packs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverRoot := filepath.Join(dataDir, "resolver-catalogs")
	if err := os.MkdirAll(resolverRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	resolverBlobReads := 0
	resolverBlobBytes := int64(0)
	started = time.Now()
	preparedResolver, err := resolvermaterialize.Build(ctx, resolvermaterialize.BuildRequest{
		Root: resolverRoot, RepositoryDir: repositoryDirectory,
		Identity: resolverIdentity, Registry: resolverRegistry, Manifest: manifestView,
		Declarations: []resolvermaterialize.DeclarationInput{{
			Protocol: resolvermaterialize.ProtocolGRPC,
			Domain:   "proto-contract", RunID: declarationRun.ID,
			GenerationDigest: declarationGeneration,
		}},
		Assertions: state,
		ReadBlob: func(readCtx context.Context, directory, oid string, limit int64) ([]byte, error) {
			content, readErr := gitobj.ReadBlob(readCtx, directory, oid, limit)
			if readErr == nil {
				resolverBlobReads++
				resolverBlobBytes += int64(len(content))
			}
			return content, readErr
		},
	})
	measurement.ResolverBuild = time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = preparedResolver.Discard() })
	resolverState, err := preparedResolver.Publish(
		ctx, func(commitCtx context.Context, current resolvercatalog.State) error {
			return state.PublishResolverCatalog(commitCtx, t40r1CallerUpstreamResolverPointer(current))
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverPointer, err := state.GetResolverCatalogPublication(ctx, t40r1Descriptor96Repo)
	if err != nil {
		t.Fatal(err)
	}
	resolverCurrent, err := state.ResolverCatalogPublicationCurrent(ctx, *resolverPointer)
	if err != nil {
		t.Fatal(err)
	}
	resolverPublication, resolverView, err := resolvermaterialize.OpenCallerResolver(
		ctx, resolverRoot, resolverState, resolvermaterialize.ProtocolGRPC,
	)
	if err != nil {
		t.Fatal(err)
	}
	descriptors := resolverView.Descriptors()
	if len(descriptors) != 1 {
		t.Fatalf("descriptor-present 96-leaf resolver count = %d", len(descriptors))
	}
	descriptor := descriptors[0]
	generatedExact, err := resolverView.Resolver().GeneratedSource(
		t40r1DescriptorGeneratedPath, objects[t40r1DescriptorGeneratedPath],
	)
	if err != nil {
		t.Fatal(err)
	}

	started = time.Now()
	observation := t40r1PublishObservationForRepository(
		t, ctx, dataDir, t40r1Descriptor96Repo, repositoryDirectory, commit,
	)
	measurement.ObservationBuild = time.Since(started)
	upstream, partitions, domainAuthority := t40r1Descriptor96PartitionedAuthority(
		t, ctx, state, candidateRoot, manifest, policies, observation,
	)
	mirrorDirectory, err := reposync.SafeRepoDir(dataDir, t40r1Descriptor96Repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mirrorDirectory), 0o700); err != nil {
		t.Fatal(err)
	}
	t40r1CallerPartitionedGit(
		t, ctx, filepath.Dir(mirrorDirectory),
		"clone", "--bare", "--quiet", "--no-local", repositoryDirectory, mirrorDirectory,
	)
	publications := callerpublication.NewRegistry(callerexecute.Root(dataDir))
	t.Cleanup(func() { _ = publications.Close() })
	worker, err := callerexecute.NewWorkerWithPublicationRegistry(
		dataDir, state, provider, callerRegistry, publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.CancelPendingJobs(ctx, store.JobCallerLeaf, t40r1Descriptor96Repo); err != nil {
		t.Fatal(err)
	}
	if _, err := state.EnqueuePending(ctx, store.JobCallerLeaf, t40r1Descriptor96Repo, false); err != nil {
		t.Fatal(err)
	}
	workerBefore := t40r1CallerProvider96MemStats()
	started = time.Now()
	turns, admission := driveT40R1Descriptor96Worker(t, ctx, state, worker)
	measurement.WorkerRun = time.Since(started)
	measurement.WorkerAllocated = t40r1CallerProvider96AllocatedSince(workerBefore)
	progress, err := state.GetCallerLeafOutcomeProgress(ctx, admission.Generation)
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := state.ListCallerLeafOutcomes(ctx, admission.Generation)
	if err != nil {
		t.Fatal(err)
	}
	sourceReads, sourceBytes, outOfLeafReads := t40r1Descriptor96SourceTotals(outcomes)
	refusals := t40r1Descriptor96Refusals(admission.Refusals)
	_, publicationErr := state.GetCallerGenerationPublication(ctx, t40r1Descriptor96Repo)

	started = time.Now()
	reader, err := callerexecute.NewPublicationReader(dataDir, state, callerRegistry, publications)
	if err != nil {
		t.Fatal(err)
	}
	read, err := reader.Open(ctx, t40r1Descriptor96Repo)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Release() }()
	callerMap := api.NewCallerMapService(api.Options{
		Version: "t40r1-descriptor-96leaf-product", Store: state, Evidence: state,
		DataDir: dataDir, CallerMapEnabled: true, CallerReader: reader,
		Principal: func(context.Context) string { return "user:t40r1-descriptor-96leaf" },
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repository store.Repo) bool { return repository.Name == t40r1Descriptor96Repo }
		},
		AuthorizationProvider: "t40r1-descriptor-96leaf-visibility-v1",
	})
	if callerMap == nil {
		t.Fatal("exact Caller Map service was not constructed")
	}
	page, err := callerMap.List(ctx, api.CallerMapQuery{
		Endpoint: api.CallerMapEndpoint{
			Protocol: "protobuf", Repository: t40r1Descriptor96Repo,
			Lineage: t40r1DescriptorLineage, Operation: t40r1DescriptorOperation,
		},
	}, 10, "")
	measurement.ProductRead = time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if page == nil || page.Generation == nil || page.Generation.PartitionProgress == nil ||
		page.Generation.PartitionProgress.TotalPairCount == nil {
		t.Fatalf("descriptor-present 96-leaf product gap is incomplete: %+v", page)
	}
	productProgress := page.Generation.PartitionProgress
	productRefusals := t40r1Descriptor96APIRefusals(productProgress.Refusals)

	witness := t40r1Descriptor96Witness{
		Schema:  t40r1Descriptor96Schema,
		Method:  "author an exact 96-leaf physical caller generation with one descriptor-backed consumer in the first hash leaf, publish current observation/extraction and resolver authority, execute the production worker until terminal admission/no-op, and require the shared reader and exact Caller Map to project the same closed refusal",
		Profile: t40r1Descriptor96Profile,
		Physical: t40r1Descriptor96Physical{
			RegularFiles: manifest.Corpus.RegularCount, UniqueGitBlobs: len(objects),
			CandidateRecords: t40r1Descriptor96CallerRecords(manifest.CallerLeaves),
			CallerLeaves:     len(manifest.CallerLeaves), LeafSetDigest: leafDigest,
			LeafContentBytes: leafContentBytes, MinLeafRecords: minLeaf, MaxLeafRecords: maxLeaf,
			PrefixDistribution: prefixDistribution, PeakSpoolBytes: buildMetrics.PeakSpoolBytes,
			ManifestDigestBound:  callerPlan.Identity() == candidatePointer.ManifestDigest,
			ControlRevisionBound: callerPlan.CandidateControlRevision() == candidatePointer.ControlRevision,
			ProviderLeavesExact: t40r1CallerProvider96LeavesExact(
				manifest.CallerLeaves, callerPlan.CallerLeaves(),
			),
			ConsumerPath:        t40r1Descriptor96ConsumerPath,
			ConsumerLeafOrdinal: consumerLeaf.Ordinal, ConsumerLeafPrefix: consumerLeaf.Prefix,
			ConsumerObjectExact: true, ConsumerVisitedBefore: consumerBefore,
		},
		Resolver: t40r1Descriptor96Resolver{
			PublicationCurrent:       resolverCurrent,
			ManifestMembers:          len(resolverPublication.Manifest().Members),
			MaterializationBlobReads: resolverBlobReads,
			MaterializationBlobBytes: resolverBlobBytes,
			Descriptors:              len(descriptors),
			OperationExact:           descriptor.Operation == t40r1DescriptorOperation,
			LineageExact:             descriptor.DeclarationLineage == t40r1DescriptorLineage,
			GeneratedObjectExact: generatedExact &&
				descriptor.GeneratedObjectID == objects[t40r1DescriptorGeneratedPath],
		},
		Authority: t40r1Descriptor96Authority{
			ObservationVersion:  observation.Version,
			ObservationRecords:  observation.RecordCount,
			ObservedRecords:     observation.ObservedCount,
			CandidatePartitions: partitions,
			DomainDisposition:   domainAuthority.Disposition,
			PlanRootCurrent: domainAuthority.CandidateManifestDigest == manifest.Digest &&
				domainAuthority.SourceGenerationDigest == observation.SourceGenerationDigest &&
				domainAuthority.ObservationGenerationDigest == observation.ObservationGenerationDigest,
			UpstreamUsable:            true,
			WorkerUpstreamDigestExact: admission.Generation.UpstreamDigest == upstream.Digest,
			ProductUpstreamDigestExact: admission.Generation.UpstreamDigest != "" &&
				read.ExpectedGeneration.UpstreamDigest == upstream.Digest && read.Admission != nil,
		},
		Pipeline: t40r1Descriptor96Pipeline{
			WorkerTurns: turns, ExpectedPairs: len(manifest.CallerLeaves),
			SettledPairs: progress.SettledCount, SucceededPairs: progress.SucceededCount,
			RefusedPairs: progress.RefusedCount, Disposition: string(admission.Disposition),
			Artifacts: admission.ArtifactCount, ResultRecords: admission.ResultCount,
			AbstentionRecords: admission.AbstentionCount,
			SourceBlobReads:   sourceReads, SourceBlobBytes: sourceBytes,
			OutOfLeafReads: outOfLeafReads, Refusals: refusals,
			PublicationAbsent: errors.Is(publicationErr, store.ErrNotFound),
		},
		Product: t40r1Descriptor96Product{
			ReaderAvailability: string(read.Availability),
			ReaderOrigin:       t40r1CallerUpstreamReadOrigin(read),
			SchemaVersion:      page.SchemaVersion, MatchingRowsState: page.MatchingRowsState,
			GenerationState: page.Generation.State, Rows: len(page.Rows),
			TotalsUnavailable:     page.TotalMatchingRows == nil,
			ProgressState:         productProgress.State,
			SettledPairs:          productProgress.SettledPairCount,
			SucceededPairs:        productProgress.SucceededPairCount,
			RefusedPairs:          productProgress.RefusedPairCount,
			TotalPairs:            *productProgress.TotalPairCount,
			Refusals:              productRefusals,
			ProgressRefusalParity: reflect.DeepEqual(productRefusals, refusals),
		},
		Boundary: "descriptor_present_96_leaf_worker_product_refusal",
	}
	witness.ReproducesRefusal =
		witness.Physical.RegularFiles == t40r1Descriptor96Files &&
			witness.Physical.CallerLeaves == t40r1Descriptor96Leaves &&
			witness.Physical.ConsumerLeafOrdinal == 0 &&
			witness.Physical.ConsumerLeafPrefix == "000000" &&
			witness.Physical.ConsumerObjectExact && witness.Resolver.PublicationCurrent &&
			witness.Resolver.Descriptors == 1 && witness.Resolver.OperationExact &&
			witness.Resolver.LineageExact && witness.Resolver.GeneratedObjectExact &&
			witness.Authority.UpstreamUsable && witness.Authority.WorkerUpstreamDigestExact &&
			witness.Authority.ProductUpstreamDigestExact &&
			witness.Pipeline.ExpectedPairs == t40r1Descriptor96Leaves &&
			witness.Pipeline.SettledPairs == t40r1Descriptor96Leaves &&
			witness.Pipeline.Disposition == string(store.CallerGenerationTerminalGenerationRefusal) &&
			witness.Pipeline.ResultRecords == 1 && len(witness.Pipeline.Refusals) == 1 &&
			witness.Pipeline.Refusals[0].Stage == string(pipelinerefusal.StageCallerGenerationAdmission) &&
			witness.Pipeline.Refusals[0].Classification == string(pipelinerefusal.ClassificationLimit) &&
			witness.Pipeline.Refusals[0].Dimension == string(pipelinerefusal.DimensionCallerGenerationAbstentions) &&
			witness.Pipeline.Refusals[0].Observed > witness.Pipeline.Refusals[0].Limit &&
			witness.Pipeline.PublicationAbsent &&
			witness.Product.ReaderAvailability == string(callerexecute.PublicationFailed) &&
			witness.Product.ReaderOrigin == "terminal_admission" &&
			witness.Product.SchemaVersion == "caller-map-v2" &&
			witness.Product.MatchingRowsState == "unavailable" &&
			witness.Product.GenerationState == "failed" && witness.Product.Rows == 0 &&
			witness.Product.TotalsUnavailable && witness.Product.ProgressState == "complete" &&
			witness.Product.ProgressRefusalParity
	if !witness.ReproducesRefusal {
		t.Fatalf("descriptor-present 96-leaf diagnostic did not reproduce the closed refusal: %+v", witness)
	}
	measurement.CandidateDiskBytes = t40r1CallerProvider96DirectoryBytes(t, candidateRoot)
	measurement.ObservationDisk = t40r1CallerProvider96DirectoryBytes(
		t, filepath.Join(dataDir, "observations"),
	)
	measurement.CallerDisk = t40r1CallerProvider96DirectoryBytes(t, callerexecute.Root(dataDir))
	return witness, measurement
}

func t40r1Descriptor96Repository(
	t *testing.T,
	ctx context.Context,
) (string, string, map[string]string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "repository.git")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t40r1CallerPartitionedGit(t, ctx, directory, "init", "--bare", "-q")
	objects := map[string]string{}
	special := make(map[string]string, len(t40r1DescriptorGitBlobFiles))
	for path, content := range t40r1DescriptorGitBlobFiles {
		if path == t40r1DescriptorGitBlobPath {
			path = t40r1Descriptor96ConsumerPath
		}
		special[path] = content
		objects[path] = t40r1Descriptor96HashBlob(t, ctx, directory, []byte(content))
	}
	neutralFiles := t40r1CallerProvider96Files - len(special)
	neutralBlobs := (neutralFiles + t40r1Descriptor96BlobGroup - 1) / t40r1Descriptor96BlobGroup
	neutralOIDs := make([]string, neutralBlobs)
	for group := range neutralOIDs {
		key := fmt.Sprintf("neutral-blob-%03d", group)
		neutralOIDs[group] = t40r1Descriptor96HashBlob(
			t, ctx, directory, t40r1Descriptor96NeutralContent(group),
		)
		objects[key] = neutralOIDs[group]
	}
	extraOID := t40r1Descriptor96HashBlob(t, ctx, directory, t40r1Descriptor96ExtraContent())
	objects["extra-caller-blob"] = extraOID
	command := exec.CommandContext(ctx, "git", "--git-dir", directory, "fast-import", "--quiet")
	command.Env = append(slices.DeleteFunc(os.Environ(), func(value string) bool {
		return strings.HasPrefix(value, "GIT_")
	}), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	writer := bufio.NewWriterSize(stdin, 1<<20)
	message := "descriptor-present 96-leaf physical product diagnostic"
	_, writeErr := fmt.Fprintf(writer,
		"commit refs/heads/main\nmark :1\nauthor T40R1 Descriptor 96 <t40r1-descriptor-96@example.invalid> 946684800 +0000\ncommitter T40R1 Descriptor 96 <t40r1-descriptor-96@example.invalid> 946684800 +0000\ndata %d\n%s\n",
		len(message), message,
	)
	for ordinal := 0; writeErr == nil && ordinal < neutralFiles; ordinal++ {
		_, writeErr = fmt.Fprintf(
			writer, "M 100644 %s semantic/go/f%06d.go\n",
			neutralOIDs[ordinal/t40r1Descriptor96BlobGroup], ordinal,
		)
	}
	if writeErr == nil {
		_, writeErr = fmt.Fprintf(
			writer, "M 100644 %s %s\n", extraOID, t40r1Descriptor96ExtraPath,
		)
	}
	paths := make([]string, 0, len(special))
	for path := range special {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if writeErr == nil {
			_, writeErr = fmt.Fprintf(writer, "M 100644 %s %s\n", objects[path], path)
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
		t.Fatalf("author descriptor-present 96-leaf Git tree: write=%v wait=%v", writeErr, waitErr)
	}
	commit := strings.TrimSpace(t40r1CallerPartitionedGit(
		t, ctx, directory, "rev-parse", "refs/heads/main",
	))
	return directory, commit, objects
}

func t40r1Descriptor96HashBlob(
	t *testing.T,
	ctx context.Context,
	repository string,
	content []byte,
) string {
	t.Helper()
	command := exec.CommandContext(ctx, "git", "--git-dir", repository, "hash-object", "-w", "--stdin")
	command.Stdin = strings.NewReader(string(content))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("hash descriptor-present 96-leaf blob: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func t40r1Descriptor96ConsumerLeaf(
	t *testing.T,
	ctx context.Context,
	plan extract.CandidateCallerPlan,
	objectID string,
) (extract.CandidateCallerLeaf, int) {
	t.Helper()
	visited := 0
	for _, leaf := range plan.CallerLeaves() {
		found := false
		if err := plan.ForEachCallerLeafFile(
			ctx, "grpc-caller", "1.5.0", leaf,
			func(file extract.CandidateManifestFile) error {
				if file.Path == t40r1Descriptor96ConsumerPath {
					if file.ObjectID != objectID {
						return errors.New("descriptor consumer object differs")
					}
					found = true
				}
				if !found {
					visited++
				}
				return nil
			},
		); err != nil {
			t.Fatal(err)
		}
		if found {
			return leaf, visited
		}
	}
	t.Fatal("descriptor consumer is absent from the physical caller plan")
	return extract.CandidateCallerLeaf{}, 0
}

func t40r1PublishDescriptor96Declaration(
	t *testing.T,
	ctx context.Context,
	state *store.Surreal,
	candidatePointer store.CandidateManifestPublication,
	commit string,
) (*store.ExtractionRun, string) {
	t.Helper()
	run, err := state.BeginExtractionRun(ctx, store.ExtractionScope{
		Repository: t40r1Descriptor96Repo, Commit: commit, Domain: "proto-contract",
	}, "t40r1-descriptor-96leaf-proto-v1")
	if err != nil {
		t.Fatal(err)
	}
	declarationBytes := len(t40r1DescriptorGitBlobFiles[t40r1DescriptorDeclaration])
	operation := strings.TrimPrefix(t40r1DescriptorOperation, "/")
	atom := store.EvidenceAtom{
		SchemaVersion: "t40r1-descriptor-96leaf-v1",
		BlobDigest:    t40r1CallerUpstreamDigest("descriptor-96leaf-declaration-source"),
		StartByte:     0, EndByte: declarationBytes, RuleID: "t40r1-descriptor-96leaf-declaration-v1",
		ExtractorVersion: "1.0.0", AdapterConfigDigest: "none", FactFingerprint: operation,
	}
	atom.ID = store.ComputeAtomID(atom)
	association := store.SnapshotEvidence{
		AtomID: atom.ID, Repo: t40r1Descriptor96Repo, Commit: commit,
		Path: t40r1DescriptorDeclaration, StartLine: 1, EndLine: 4,
		VisibilityScope: "repo:" + t40r1Descriptor96Repo,
	}
	assertion := store.Assertion{
		Predicate: "DECLARES_OPERATION", Subject: t40r1DescriptorDeclaration,
		Object: operation, Lineage: t40r1DescriptorLineage,
		Tier: store.TierExact, CodeRole: "production", Repo: t40r1Descriptor96Repo,
		Supporting: []string{atom.ID}, Detail: `{"schema":"proto-operation-detail-v1"}`,
	}
	if err := state.AddEvidence(
		ctx, run.ID, []store.EvidenceAtom{atom},
		[]store.SnapshotEvidence{association}, []store.Assertion{assertion},
	); err != nil {
		t.Fatal(err)
	}
	generation := store.ExtractionGenerationIdentity{
		CandidateManifestDigest:  candidatePointer.ManifestDigest,
		CandidatePolicyDigest:    candidatePointer.PolicyDigest,
		CandidateControlRevision: candidatePointer.ControlRevision,
		Extractor:                run.Extractor, InventoryPolicy: "candidate-manifest-v4-descriptor-96leaf",
		DependencyDigest: t40r1CallerUpstreamDigest("descriptor-96leaf-declaration-dependencies"),
	}
	generation.Digest = store.ComputeExtractionGenerationDigest(generation)
	coverage := store.CoverageManifest{
		Protocols: []string{"protobuf"}, CorpusFileCount: t40r1Descriptor96Files,
		CandidateFileCount: 1, ReadFileCount: 1, ReadBytes: int64(declarationBytes),
		SourceScopeDigest: t40r1CallerUpstreamDigest("descriptor-96leaf-declaration-scope"),
		AssertionCount:    1, AtomCount: 1, CandidateManifestDigest: candidatePointer.ManifestDigest,
		ScopePosture: "whole-repository", CandidatePlane: "repository",
		ScopeCorpusFileCount:     t40r1Descriptor96Files,
		ScopeCorpusDeclaredBytes: t40r1Descriptor96CorpusBytes(),
		ScopeCorpusDigest:        t40r1CallerUpstreamDigest("descriptor-96leaf-declaration-corpus"),
		PlannedFileCount:         1, PlannedRequiredFileCount: 1,
		PlannedDeclaredBytes: int64(declarationBytes),
		PlannedScopeDigest:   t40r1CallerUpstreamDigest("descriptor-96leaf-declaration-plan"),
	}
	outcome := store.ExtractionDomainOutcome{
		Scope: store.ExtractionScope{
			Repository: t40r1Descriptor96Repo, Commit: commit, Domain: "proto-contract",
		},
		Disposition: store.DomainOutcomePublished, Generation: generation,
		RunID: run.ID, ReceiptSchema: store.ExtractionOutcomeReceiptSchema,
		Receipt: `{"schema":"` + store.ExtractionOutcomeReceiptSchema + `"}`,
	}
	if err := state.PublishExtractionRunWithOutcome(ctx, run.ID, coverage, outcome); err != nil {
		t.Fatal(err)
	}
	return run, generation.Digest
}

func t40r1Descriptor96PartitionedAuthority(
	t *testing.T,
	ctx context.Context,
	state *store.Surreal,
	candidateRoot string,
	manifest candidate.Manifest,
	policies []candidate.Policy,
	observation observationpublication.DownstreamAuthority,
) (downstreamauthority.Authority, int, candidate.DownstreamDomainAuthority) {
	t.Helper()
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.Open(candidateRoot, candidate.Expected{
		Repository: t40r1Descriptor96Repo, Commit: manifest.Commit, Policies: identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	sparseDirectory := t.TempDir()
	sparseRoot, err := candidate.BuildSparseRoot(ctx, sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := candidate.OpenSparse(
		ctx, sparseDirectory, candidateRoot, manifest.State(), sparseRoot.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(ctx, "grpc-caller", "1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	partitions := domain.Partitions()
	plan, err := candidate.BuildDomainResultPlan(
		domain,
		candidate.DomainResultAuthority{
			SourceGenerationDigest:      observation.SourceGenerationDigest,
			ObservationGenerationDigest: observation.ObservationGenerationDigest,
			ExtractorVersion:            "1.5.0",
			ExtractionPolicyDigest:      t40r1CallerUpstreamDigest("descriptor-96leaf-extraction-policy"),
		},
		make([]candidate.DomainResultTotals, len(partitions)),
	)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]candidate.PartitionResult, len(partitions))
	for ordinal := range partitions {
		results[ordinal], err = candidate.BuildPartitionResult(
			plan, ordinal, candidate.PartitionResultSpec{Disposition: candidate.PartitionResultEmpty},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	root, err := candidate.BuildDomainResultRoot(plan, results)
	if err != nil {
		t.Fatal(err)
	}
	extractionRun, err := state.BeginPartitionedExtractionRun(
		ctx,
		store.ExtractionScope{
			Repository: t40r1Descriptor96Repo, Commit: manifest.Commit, Domain: "grpc-caller",
		},
		"t40r1-descriptor-96leaf-grpc", plan.Digest, manifest.Digest, plan.Schema,
		store.PartitionedExtractionRunLimits{},
	)
	if err != nil {
		t.Fatal(err)
	}
	publisher := extractionpublication.StorePublisher{Store: state}
	if err := publisher.PublishDomain(ctx, plan, root, extractionRun.ID); err != nil {
		t.Fatal(err)
	}
	domainAuthority, err := extractionpublication.CurrentDomainAuthority(
		ctx, state, t40r1Descriptor96Repo, "grpc-caller",
	)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := downstreamauthority.BuildRequired(
		observation,
		[]downstreamauthority.DomainIdentity{{Domain: "grpc-caller", Version: "1.5.0"}},
		[]candidate.DownstreamDomainAuthority{domainAuthority},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := downstreamauthority.RequireUsable(upstream); err != nil {
		t.Fatal(err)
	}
	return upstream, len(partitions), domainAuthority
}

func driveT40R1Descriptor96Worker(
	t *testing.T,
	ctx context.Context,
	state *store.Surreal,
	worker *callerexecute.Worker,
) (int, *store.CallerGenerationAdmission) {
	t.Helper()
	terminalSeen := false
	for turn := 1; turn <= t40r1Descriptor96Leaves+4; turn++ {
		job, err := state.ClaimJob(ctx, store.JobCallerLeaf, "t40r1-descriptor-96leaf")
		if err != nil {
			t.Fatalf("claim descriptor-present 96-leaf turn %d: %v", turn, err)
		}
		if err := state.SetJobStatus(ctx, *job, store.StatusRunning, ""); err != nil {
			t.Fatal(err)
		}
		job.Status = store.StatusRunning
		if err := worker.Handle(ctx, *job); err != nil {
			t.Fatalf("descriptor-present 96-leaf worker turn %d: %v", turn, err)
		}
		if err := state.SetJobStatus(ctx, *job, store.StatusDone, ""); err != nil {
			t.Fatal(err)
		}
		admissions, err := state.ListCallerGenerationAdmissions(ctx, t40r1Descriptor96Repo)
		if err != nil {
			t.Fatal(err)
		}
		if len(admissions) > 1 {
			t.Fatalf("descriptor-present 96-leaf admissions = %d", len(admissions))
		}
		if len(admissions) == 1 {
			admission := admissions[0]
			if admission.Disposition != store.CallerGenerationTerminalGenerationRefusal {
				t.Fatalf("descriptor-present 96-leaf admission = %s", admission.Disposition)
			}
			if terminalSeen {
				return turn, &admission
			}
			terminalSeen = true
		}
	}
	t.Fatal("descriptor-present 96-leaf worker did not reach terminal admission and no-op")
	return 0, nil
}

func t40r1Descriptor96SourceTotals(outcomes []store.CallerLeafOutcome) (int, int64, int) {
	reads := 0
	bytes := int64(0)
	outOfLeaf := 0
	for _, outcome := range outcomes {
		if outcome.Receipt == nil {
			continue
		}
		reads += outcome.Receipt.SourceBlobReads
		bytes += outcome.Receipt.SourceBlobBytes
		outOfLeaf += outcome.Receipt.OutOfLeafReads
	}
	return reads, bytes, outOfLeaf
}

func t40r1Descriptor96Refusals(
	values []store.CallerGenerationRefusalSummary,
) []t40r1Descriptor96Refusal {
	result := make([]t40r1Descriptor96Refusal, len(values))
	for index, value := range values {
		result[index] = t40r1Descriptor96Refusal{
			Stage: string(value.Refusal.Stage), GenerationKind: string(value.Refusal.GenerationKind),
			Classification: string(value.Refusal.Classification), Dimension: string(value.Refusal.Dimension),
			Observed: value.Refusal.Observed, Limit: value.Refusal.Limit,
			OutcomeCount: value.OutcomeCount,
		}
	}
	return result
}

func t40r1Descriptor96APIRefusals(
	values []api.CallerMapRefusalSummary,
) []t40r1Descriptor96Refusal {
	result := make([]t40r1Descriptor96Refusal, len(values))
	for index, value := range values {
		result[index] = t40r1Descriptor96Refusal{
			Stage: string(value.Stage), GenerationKind: string(value.GenerationKind),
			Classification: string(value.Classification), Dimension: string(value.Dimension),
			Observed: value.Observed, Limit: value.Limit, OutcomeCount: value.OutcomeCount,
		}
	}
	return result
}

func t40r1Descriptor96CallerRecords(leaves []candidate.CallerLeaf) int {
	total := 0
	for _, leaf := range leaves {
		total += leaf.RecordCount
	}
	return total
}

func t40r1Descriptor96CorpusBytes() int64 {
	neutralFiles := t40r1CallerProvider96Files - len(t40r1DescriptorGitBlobFiles)
	total := int64(0)
	for ordinal := 0; ordinal < neutralFiles; {
		group := ordinal / t40r1Descriptor96BlobGroup
		count := min(t40r1Descriptor96BlobGroup, neutralFiles-ordinal)
		total += int64(count * len(t40r1Descriptor96NeutralContent(group)))
		ordinal += count
	}
	for _, content := range t40r1DescriptorGitBlobFiles {
		total += int64(len(content))
	}
	total += int64(len(t40r1Descriptor96ExtraContent()))
	return total
}

func t40r1Descriptor96NeutralContent(group int) []byte {
	return []byte(fmt.Sprintf(
		"package neutral\n\n// PhysicalGroup%03d is a deterministic shared-blob control.\nfunc Call() {}\n",
		group,
	))
}

func t40r1Descriptor96ExtraContent() []byte {
	return []byte("package neutral\n\n// SplitControl restores the exact physical leaf count.\nfunc Call() {}\n")
}
