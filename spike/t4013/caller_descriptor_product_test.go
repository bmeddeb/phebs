package t4013

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	t40r1DescriptorProductSchemaV1 = "t4013-descriptor-present-product-parity-v1"
	t40r1DescriptorProductSchema   = "t4013-descriptor-present-product-parity-v2"
	t40r1DescriptorProductEnv      = "T40R1_DESCRIPTOR_PRODUCT_PARITY"
	t40r1DescriptorProductReceipt  = "descriptor-present-product-parity-v2.json"
)

type t40r1DescriptorProductAuthority struct {
	ObservationVersion           string `json:"observation_version"`
	ObservationCurrent           bool   `json:"observation_current"`
	ObservationRecords           int    `json:"observation_records"`
	ObservedRecords              int    `json:"observed_records"`
	RequiredDomains              int    `json:"required_domains"`
	Domain                       string `json:"domain"`
	Version                      string `json:"version"`
	CandidatePartitions          int    `json:"candidate_partitions"`
	DomainDisposition            string `json:"domain_disposition"`
	PlanRootCurrent              bool   `json:"plan_root_current"`
	UpstreamUsable               bool   `json:"upstream_usable"`
	CallerUpstreamDigestExact    bool   `json:"caller_upstream_digest_exact"`
	CallerUpstreamCanonicalExact bool   `json:"caller_upstream_canonical_exact"`
}

type t40r1DescriptorProductPipeline struct {
	WorkerTurns        int    `json:"worker_turns"`
	ExpectedPairs      int    `json:"expected_pairs"`
	SettledPairs       int    `json:"settled_pairs"`
	SucceededPairs     int    `json:"succeeded_pairs"`
	RefusedPairs       int    `json:"refused_pairs"`
	Disposition        string `json:"disposition"`
	AdmissionPairs     int    `json:"admission_pairs"`
	Artifacts          int    `json:"artifacts"`
	ResultRecords      int    `json:"result_records"`
	AbstentionRecords  int    `json:"abstention_records"`
	CoverageRecords    int    `json:"coverage_records"`
	CoveredCandidates  int    `json:"covered_candidates"`
	RefusalSummaries   int    `json:"refusal_summaries"`
	PublicationPresent bool   `json:"publication_present"`
	PublicationCurrent bool   `json:"publication_current"`
	PublicationParity  bool   `json:"publication_parity"`
}

type t40r1DescriptorProductRow struct {
	Classification string `json:"classification"`
	Resolution     string `json:"resolution"`
	Protocol       string `json:"protocol"`
	Operation      string `json:"operation"`
	Lineage        string `json:"lineage"`
	Path           string `json:"path"`
	ObjectPresent  bool   `json:"object_present"`
	BlobExact      bool   `json:"blob_exact"`
	Tier           string `json:"tier"`
	CodeRole       string `json:"code_role"`
}

type t40r1DescriptorProductProjection struct {
	ReaderAvailability string                    `json:"reader_availability"`
	ReaderOrigin       string                    `json:"reader_origin"`
	SchemaVersion      string                    `json:"schema_version"`
	MatchingRowsState  string                    `json:"matching_rows_state"`
	GenerationState    string                    `json:"generation_state"`
	DeclarationExact   bool                      `json:"declaration_exact"`
	Rows               int                       `json:"rows"`
	TotalMatchingRows  int                       `json:"total_matching_rows"`
	PairCount          int                       `json:"pair_count"`
	CandidateRecords   int                       `json:"candidate_records"`
	ResultRecords      int                       `json:"result_records"`
	AbstentionRecords  int                       `json:"abstention_records"`
	CoverageRecords    int                       `json:"coverage_records"`
	CoveredCandidates  int                       `json:"covered_candidates"`
	Row                t40r1DescriptorProductRow `json:"row"`
}

type t40r1DescriptorProductWitness struct {
	Schema               string                           `json:"schema"`
	Method               string                           `json:"method"`
	Profile              string                           `json:"profile"`
	Authority            t40r1DescriptorProductAuthority  `json:"authority"`
	Pipeline             t40r1DescriptorProductPipeline   `json:"pipeline"`
	Projection           t40r1DescriptorProductProjection `json:"projection"`
	Boundary             string                           `json:"boundary"`
	ClearsBoundary       bool                             `json:"clears_boundary"`
	EstablishesScalePass bool                             `json:"establishes_scale_pass"`
	AuthorizesCeremony   bool                             `json:"authorizes_ceremony"`
}

type t40r1DescriptorProductMeasurement struct {
	WorkerDuration  time.Duration
	ProductDuration time.Duration
	CallerDisk      int64
	ObservationDisk int64
}

func TestT40R1DescriptorPresentProductParity(t *testing.T) {
	if os.Getenv(t40r1DescriptorProductEnv) != "1" {
		t.Skip("set " + t40r1DescriptorProductEnv + "=1 to run the descriptor product-parity diagnostic")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary is not installed")
	}
	witness, measurement := runT40R1DescriptorProductParity(t)
	encoded, err := json.MarshalIndent(witness, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"worker=%s product=%s caller_disk=%d observation_disk=%d",
		measurement.WorkerDuration, measurement.ProductDuration,
		measurement.CallerDisk, measurement.ObservationDisk,
	)
	raw, err := os.ReadFile(t40r1DescriptorProductReceipt)
	if err != nil {
		t.Fatalf("retained descriptor product-parity witness unavailable: %v\n%s", err, encoded)
	}
	var retained t40r1DescriptorProductWitness
	if err := json.Unmarshal(raw, &retained); err != nil || !reflect.DeepEqual(retained, witness) {
		t.Fatalf("retained descriptor product-parity witness differs: %v\n%s", err, encoded)
	}
	t.Log(string(encoded))
}

func TestT40R1RetainedDescriptorPresentProductParityIsClosed(t *testing.T) {
	raw, err := os.ReadFile("descriptor-present-product-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var witness t40r1DescriptorProductWitness
	if err := json.Unmarshal(raw, &witness); err != nil {
		t.Fatal(err)
	}
	if witness.Schema != t40r1DescriptorProductSchemaV1 ||
		witness.Profile != "physical-grpc-descriptor-product-v1" ||
		witness.Authority.ObservationVersion != observationpublication.DownstreamAuthorityV2 ||
		!witness.Authority.ObservationCurrent || witness.Authority.ObservationRecords != 2 ||
		witness.Authority.ObservedRecords != 2 || witness.Authority.RequiredDomains != 1 ||
		witness.Authority.Domain != "grpc-caller" || witness.Authority.Version != "1.5.0" ||
		witness.Authority.CandidatePartitions != 0 ||
		witness.Authority.DomainDisposition != candidate.PartitionResultUnavailablePrerequisite ||
		!witness.Authority.PlanRootCurrent || !witness.Authority.UpstreamUsable ||
		!witness.Authority.CallerUpstreamDigestExact ||
		!witness.Authority.CallerUpstreamCanonicalExact ||
		witness.Pipeline.WorkerTurns != 6 || witness.Pipeline.ExpectedPairs != 4 ||
		witness.Pipeline.SettledPairs != 4 ||
		witness.Pipeline.SucceededPairs != 4 || witness.Pipeline.RefusedPairs != 0 ||
		witness.Pipeline.Disposition != string(store.CallerGenerationAdmitted) ||
		witness.Pipeline.AdmissionPairs != 4 || witness.Pipeline.Artifacts != 4 ||
		witness.Pipeline.ResultRecords != 1 || witness.Pipeline.AbstentionRecords != 5 ||
		witness.Pipeline.CoverageRecords != 0 || witness.Pipeline.CoveredCandidates != 0 ||
		witness.Pipeline.RefusalSummaries != 0 || !witness.Pipeline.PublicationPresent ||
		!witness.Pipeline.PublicationCurrent || !witness.Pipeline.PublicationParity ||
		witness.Projection.ReaderAvailability != string(callerexecute.PublicationCurrent) ||
		witness.Projection.ReaderOrigin != "current_publication" ||
		witness.Projection.SchemaVersion != "caller-map-v2" ||
		witness.Projection.MatchingRowsState != "exact" ||
		witness.Projection.GenerationState != "current" ||
		!witness.Projection.DeclarationExact || witness.Projection.Rows != 1 ||
		witness.Projection.TotalMatchingRows != 1 || witness.Projection.PairCount != 4 ||
		witness.Projection.CandidateRecords != 6 || witness.Projection.ResultRecords != 1 ||
		witness.Projection.AbstentionRecords != 5 || witness.Projection.CoverageRecords != 0 ||
		witness.Projection.CoveredCandidates != 0 ||
		witness.Projection.Row.Classification != "resolved_caller" ||
		witness.Projection.Row.Resolution != "syntax" ||
		witness.Projection.Row.Protocol != "protobuf" ||
		witness.Projection.Row.Operation != t40r1DescriptorOperation ||
		witness.Projection.Row.Lineage != t40r1DescriptorLineage ||
		witness.Projection.Row.Path != t40r1DescriptorGitBlobPath ||
		!witness.Projection.Row.ObjectPresent || !witness.Projection.Row.BlobExact ||
		witness.Projection.Row.Tier != "heuristic" ||
		witness.Projection.Row.CodeRole != "production" ||
		witness.Boundary != "descriptor_present_worker_product_parity" ||
		!witness.ClearsBoundary || witness.EstablishesScalePass || witness.AuthorizesCeremony {
		t.Fatalf("retained descriptor product-parity witness is not closed: %+v", witness)
	}
}

func TestT40R1RetainedDescriptorPresentProductParityV2IsClosed(t *testing.T) {
	raw, err := os.ReadFile(t40r1DescriptorProductReceipt)
	if err != nil {
		t.Skip("retained descriptor product-parity V2 witness is not authored yet")
	}
	var witness t40r1DescriptorProductWitness
	if err := json.Unmarshal(raw, &witness); err != nil {
		t.Fatal(err)
	}
	if witness.Schema != t40r1DescriptorProductSchema ||
		witness.Pipeline.WorkerTurns != 6 || witness.Pipeline.ExpectedPairs != 4 ||
		witness.Pipeline.SettledPairs != 4 || witness.Pipeline.SucceededPairs != 4 ||
		witness.Pipeline.RefusedPairs != 0 ||
		witness.Pipeline.Disposition != string(store.CallerGenerationAdmitted) ||
		witness.Pipeline.ResultRecords != 1 || witness.Pipeline.AbstentionRecords != 0 ||
		witness.Pipeline.CoverageRecords != 3 || witness.Pipeline.CoveredCandidates != 5 ||
		!witness.Pipeline.PublicationPresent || !witness.Pipeline.PublicationCurrent ||
		!witness.Pipeline.PublicationParity ||
		witness.Projection.ReaderAvailability != string(callerexecute.PublicationCurrent) ||
		witness.Projection.ReaderOrigin != "current_publication" ||
		witness.Projection.MatchingRowsState != "exact" ||
		witness.Projection.GenerationState != "current" ||
		witness.Projection.Rows != 1 || witness.Projection.TotalMatchingRows != 1 ||
		witness.Projection.ResultRecords != witness.Pipeline.ResultRecords ||
		witness.Projection.AbstentionRecords != witness.Pipeline.AbstentionRecords ||
		witness.Projection.CoverageRecords != witness.Pipeline.CoverageRecords ||
		witness.Projection.CoveredCandidates != witness.Pipeline.CoveredCandidates ||
		witness.Projection.Row.Operation != t40r1DescriptorOperation ||
		witness.Projection.Row.Lineage != t40r1DescriptorLineage ||
		witness.Projection.Row.Path != t40r1DescriptorGitBlobPath ||
		!witness.ClearsBoundary || witness.EstablishesScalePass || witness.AuthorizesCeremony {
		t.Fatalf("retained descriptor product-parity V2 witness is not closed: %+v", witness)
	}
}

func runT40R1DescriptorProductParity(
	t *testing.T,
) (t40r1DescriptorProductWitness, t40r1DescriptorProductMeasurement) {
	t.Helper()
	var witness t40r1DescriptorProductWitness
	var measurement t40r1DescriptorProductMeasurement
	runT40R1DescriptorGitBlobDiagnosticWithHook(t, func(runtime t40r1DescriptorGitBlobRuntime) {
		witness, measurement = executeT40R1DescriptorProductParity(t, runtime)
	})
	if witness.Schema == "" {
		t.Fatal("descriptor product-parity hook did not execute")
	}
	return witness, measurement
}

func executeT40R1DescriptorProductParity(
	t *testing.T,
	runtime t40r1DescriptorGitBlobRuntime,
) (t40r1DescriptorProductWitness, t40r1DescriptorProductMeasurement) {
	t.Helper()
	ctx := runtime.Context
	state := runtime.State
	observation := t40r1PublishDescriptorObservation(
		t, ctx, runtime.DataDir, runtime.RepositoryDir, runtime.Commit,
	)
	identities, err := candidate.PolicyIdentities(runtime.Policies)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.Open(runtime.CandidateRoot, candidate.Expected{
		Repository: t40r1DescriptorGitBlobRepo, Commit: runtime.Commit, Policies: identities,
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
		ctx, sparseDirectory, runtime.CandidateRoot,
		runtime.Manifest.State(), sparseRoot.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	adapters := runtime.CallerRegistry.Adapters()
	if len(adapters) != 1 || adapters[0].Domain != "grpc-caller" {
		t.Fatalf("descriptor caller adapters = %+v", adapters)
	}
	adapter := adapters[0]
	domain, err := sparse.OpenDomain(ctx, adapter.Domain, adapter.Version)
	if err != nil {
		t.Fatal(err)
	}
	partitions := domain.Partitions()
	plan, err := candidate.BuildDomainResultPlan(
		domain,
		candidate.DomainResultAuthority{
			SourceGenerationDigest:      observation.SourceGenerationDigest,
			ObservationGenerationDigest: observation.ObservationGenerationDigest,
			ExtractorVersion:            adapter.Version,
			ExtractionPolicyDigest: t40r1CallerUpstreamDigest(
				"descriptor-product-extraction-policy:" + adapter.Domain + ":" + adapter.Version,
			),
		},
		make([]candidate.DomainResultTotals, len(partitions)),
	)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]candidate.PartitionResult, len(partitions))
	for ordinal := range partitions {
		results[ordinal], err = candidate.BuildPartitionResult(
			plan, ordinal,
			candidate.PartitionResultSpec{Disposition: candidate.PartitionResultEmpty},
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
			Repository: t40r1DescriptorGitBlobRepo,
			Commit:     runtime.Commit, Domain: adapter.Domain,
		},
		"t40r1-descriptor-product-"+adapter.Domain,
		plan.Digest, runtime.Manifest.Digest, plan.Schema,
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
		ctx, state, t40r1DescriptorGitBlobRepo, adapter.Domain,
	)
	if err != nil {
		t.Fatal(err)
	}
	required := []downstreamauthority.DomainIdentity{{
		Domain: adapter.Domain, Version: adapter.Version,
	}}
	upstream, err := downstreamauthority.BuildRequired(
		observation, required, []candidate.DownstreamDomainAuthority{domainAuthority},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := downstreamauthority.RequireUsable(upstream); err != nil {
		t.Fatal(err)
	}

	mirrorDirectory, err := reposync.SafeRepoDir(runtime.DataDir, t40r1DescriptorGitBlobRepo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mirrorDirectory), 0o700); err != nil {
		t.Fatal(err)
	}
	t40r1CallerPartitionedGit(
		t, ctx, filepath.Dir(mirrorDirectory),
		"clone", "--bare", "--quiet", "--no-local", runtime.RepositoryDir, mirrorDirectory,
	)
	publications := callerpublication.NewRegistry(callerexecute.Root(runtime.DataDir))
	t.Cleanup(func() { _ = publications.Close() })
	worker, err := callerexecute.NewWorkerWithPublicationRegistry(
		runtime.DataDir, state, runtime.Provider, runtime.CallerRegistry, publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.CancelPendingJobs(
		ctx, store.JobCallerLeaf, t40r1DescriptorGitBlobRepo,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := state.EnqueuePending(
		ctx, store.JobCallerLeaf, t40r1DescriptorGitBlobRepo, false,
	); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	expectedPairs := len(runtime.Manifest.CallerLeaves) * len(adapters)
	turns := driveT40R1DescriptorProductWorker(t, ctx, state, worker, expectedPairs)
	measurement := t40r1DescriptorProductMeasurement{WorkerDuration: time.Since(started)}
	pointer, err := state.GetCallerGenerationPublication(ctx, t40r1DescriptorGitBlobRepo)
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
	upstreamCanonical, err := json.Marshal(upstream)
	if err != nil {
		t.Fatal(err)
	}

	started = time.Now()
	reader, err := callerexecute.NewPublicationReader(
		runtime.DataDir, state, runtime.CallerRegistry, publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	read, err := reader.Open(ctx, t40r1DescriptorGitBlobRepo)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Release() }()
	callerMap := api.NewCallerMapService(api.Options{
		Version: "t40r1-descriptor-product-parity", Store: state, Evidence: state,
		DataDir: runtime.DataDir, CallerMapEnabled: true, CallerReader: reader,
		Principal: func(context.Context) string { return "user:t40r1-descriptor-product" },
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repository store.Repo) bool {
				return repository.Name == t40r1DescriptorGitBlobRepo
			}
		},
		AuthorizationProvider: "t40r1-descriptor-product-visibility-v1",
	})
	if callerMap == nil {
		t.Fatal("exact Caller Map service was not constructed")
	}
	page, err := callerMap.List(ctx, api.CallerMapQuery{
		Endpoint: api.CallerMapEndpoint{
			Protocol: "protobuf", Repository: t40r1DescriptorGitBlobRepo,
			Lineage: t40r1DescriptorLineage, Operation: t40r1DescriptorOperation,
		},
	}, 10, "")
	measurement.ProductDuration = time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if page == nil || page.Generation == nil || page.Generation.RecordCounts == nil ||
		page.TotalMatchingRows == nil || len(page.Rows) != 1 {
		t.Fatalf("descriptor product projection is incomplete: %+v", page)
	}
	row := page.Rows[0]
	witness := t40r1DescriptorProductWitness{
		Schema:  t40r1DescriptorProductSchema,
		Method:  "publish current observation and partitioned gRPC extraction authority, execute all four descriptor-present physical caller pairs through the production V3 zero-fact coverage worker and real Git mirror, then require admitted outcome/publication parity with the shared reader and one exact authorized Caller Map row",
		Profile: "physical-grpc-descriptor-product-v1",
		Authority: t40r1DescriptorProductAuthority{
			ObservationVersion: observation.Version, ObservationCurrent: true,
			ObservationRecords: observation.RecordCount, ObservedRecords: observation.ObservedCount,
			RequiredDomains: len(required), Domain: adapter.Domain, Version: adapter.Version,
			CandidatePartitions: len(partitions), DomainDisposition: domainAuthority.Disposition,
			PlanRootCurrent: domainAuthority.PlanDigest == plan.Digest &&
				domainAuthority.RootDigest == root.Digest &&
				domainAuthority.CandidateManifestDigest == runtime.Manifest.Digest,
			UpstreamUsable:               true,
			CallerUpstreamDigestExact:    pointer.Generation.UpstreamDigest == upstream.Digest,
			CallerUpstreamCanonicalExact: pointer.Upstream == string(upstreamCanonical),
		},
		Pipeline: t40r1DescriptorProductPipeline{
			WorkerTurns: turns, ExpectedPairs: expectedPairs,
			SettledPairs: progress.SettledCount, SucceededPairs: progress.SucceededCount,
			RefusedPairs: progress.RefusedCount, Disposition: string(admission.Disposition),
			AdmissionPairs: admission.PairCount, Artifacts: admission.ArtifactCount,
			ResultRecords: admission.ResultCount, AbstentionRecords: admission.AbstentionCount,
			CoverageRecords:   admission.CoverageRecordCount,
			CoveredCandidates: admission.CoveredCandidateCount,
			RefusalSummaries:  len(admission.Refusals), PublicationPresent: true,
			PublicationCurrent: pointerCurrent,
			PublicationParity: pointer.PairCount == admission.PairCount &&
				pointer.ResultCount == admission.ResultCount &&
				pointer.AbstentionCount == admission.AbstentionCount &&
				pointer.CoverageRecordCount == admission.CoverageRecordCount &&
				pointer.CoveredCandidateCount == admission.CoveredCandidateCount,
		},
		Projection: t40r1DescriptorProductProjection{
			ReaderAvailability: string(read.Availability), ReaderOrigin: t40r1CallerUpstreamReadOrigin(read),
			SchemaVersion: page.SchemaVersion, MatchingRowsState: page.MatchingRowsState,
			GenerationState: page.Generation.State,
			DeclarationExact: page.Declaration != nil &&
				page.Declaration.Predicate == "DECLARES_OPERATION" &&
				page.Declaration.Object == strings.TrimPrefix(t40r1DescriptorOperation, "/"),
			Rows: len(page.Rows), TotalMatchingRows: *page.TotalMatchingRows,
			PairCount:         page.Generation.PairCount,
			CandidateRecords:  page.Generation.RecordCounts.CandidateRecords,
			ResultRecords:     page.Generation.ResultCount,
			AbstentionRecords: page.Generation.AbstentionCount,
			CoverageRecords:   page.Generation.CoverageRecordCount,
			CoveredCandidates: page.Generation.CoveredCandidateCount,
			Row: t40r1DescriptorProductRow{
				Classification: row.Classification, Resolution: row.Resolution,
				Protocol: row.Protocol, Operation: row.Operation,
				Lineage: row.DeclarationLineage, Path: row.Source.Path,
				ObjectPresent: row.Source.ObjectID != "",
				BlobExact: row.Source.BlobDigest == t40r1CallerUpstreamDigest(
					t40r1DescriptorGitBlobFiles[t40r1DescriptorGitBlobPath],
				),
				Tier: row.Tier, CodeRole: row.CodeRole,
			},
		},
		Boundary: "descriptor_present_worker_product_parity",
	}
	witness.ClearsBoundary =
		witness.Authority.ObservationVersion == observationpublication.DownstreamAuthorityV2 &&
			witness.Authority.ObservationCurrent && witness.Authority.ObservationRecords == 2 &&
			witness.Authority.ObservedRecords == 2 && witness.Authority.RequiredDomains == 1 &&
			witness.Authority.CandidatePartitions == 0 &&
			witness.Authority.DomainDisposition == candidate.PartitionResultUnavailablePrerequisite &&
			witness.Authority.PlanRootCurrent && witness.Authority.UpstreamUsable &&
			witness.Authority.CallerUpstreamDigestExact &&
			witness.Authority.CallerUpstreamCanonicalExact &&
			witness.Pipeline.WorkerTurns == 6 && witness.Pipeline.ExpectedPairs == 4 &&
			witness.Pipeline.SettledPairs == 4 &&
			witness.Pipeline.SucceededPairs == 4 && witness.Pipeline.RefusedPairs == 0 &&
			witness.Pipeline.Disposition == string(store.CallerGenerationAdmitted) &&
			witness.Pipeline.AdmissionPairs == 4 && witness.Pipeline.Artifacts == 4 &&
			witness.Pipeline.ResultRecords == 1 && witness.Pipeline.AbstentionRecords == 0 &&
			witness.Pipeline.CoverageRecords == 3 && witness.Pipeline.CoveredCandidates == 5 &&
			witness.Pipeline.RefusalSummaries == 0 && witness.Pipeline.PublicationPresent &&
			witness.Pipeline.PublicationCurrent && witness.Pipeline.PublicationParity &&
			witness.Projection.ReaderAvailability == string(callerexecute.PublicationCurrent) &&
			witness.Projection.ReaderOrigin == "current_publication" &&
			witness.Projection.SchemaVersion == "caller-map-v2" &&
			witness.Projection.MatchingRowsState == "exact" &&
			witness.Projection.GenerationState == "current" &&
			witness.Projection.DeclarationExact && witness.Projection.Rows == 1 &&
			witness.Projection.TotalMatchingRows == 1 && witness.Projection.PairCount == 4 &&
			witness.Projection.CandidateRecords == 6 && witness.Projection.ResultRecords == 1 &&
			witness.Projection.AbstentionRecords == 0 && witness.Projection.CoverageRecords == 3 &&
			witness.Projection.CoveredCandidates == 5 &&
			witness.Projection.Row.Classification == "resolved_caller" &&
			witness.Projection.Row.Resolution == "syntax" &&
			witness.Projection.Row.Protocol == "protobuf" &&
			witness.Projection.Row.Operation == t40r1DescriptorOperation &&
			witness.Projection.Row.Lineage == t40r1DescriptorLineage &&
			witness.Projection.Row.Path == t40r1DescriptorGitBlobPath &&
			witness.Projection.Row.ObjectPresent && witness.Projection.Row.BlobExact &&
			witness.Projection.Row.Tier == "heuristic" &&
			witness.Projection.Row.CodeRole == "production"
	if !witness.ClearsBoundary {
		t.Fatalf("descriptor product-parity diagnostic did not clear: %+v", witness)
	}
	measurement.CallerDisk = t40r1CallerProvider96DirectoryBytes(
		t, callerexecute.Root(runtime.DataDir),
	)
	measurement.ObservationDisk = t40r1CallerProvider96DirectoryBytes(
		t, filepath.Join(runtime.DataDir, "observations"),
	)
	return witness, measurement
}

func driveT40R1DescriptorProductWorker(
	t *testing.T,
	ctx context.Context,
	state *store.Surreal,
	worker *callerexecute.Worker,
	pairs int,
) int {
	t.Helper()
	publicationSeen := false
	for turn := 1; turn <= pairs+4; turn++ {
		job, err := state.ClaimJob(
			ctx, store.JobCallerLeaf, "t40r1-descriptor-product-parity",
		)
		if err != nil {
			t.Fatalf("claim descriptor product turn %d: %v", turn, err)
		}
		if err := state.SetJobStatus(ctx, *job, store.StatusRunning, ""); err != nil {
			t.Fatal(err)
		}
		job.Status = store.StatusRunning
		if err := worker.Handle(ctx, *job); err != nil {
			t.Fatalf("descriptor product worker turn %d failed: %v", turn, err)
		}
		if err := state.SetJobStatus(ctx, *job, store.StatusDone, ""); err != nil {
			t.Fatal(err)
		}
		pointer, pointerErr := state.GetCallerGenerationPublication(
			ctx, t40r1DescriptorGitBlobRepo,
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
	admissions, admissionErr := state.ListCallerGenerationAdmissions(
		ctx, t40r1DescriptorGitBlobRepo,
	)
	jobs, jobsErr := state.ListJobs(ctx, store.JobCallerLeaf, "")
	t.Fatalf(
		"descriptor product diagnostic did not reach a current publication and no-op: admissions=%+v admission_err=%v jobs=%+v jobs_err=%v",
		admissions, admissionErr, jobs, jobsErr,
	)
	return 0
}

func t40r1PublishDescriptorObservation(
	t *testing.T,
	ctx context.Context,
	dataDir,
	repositoryDirectory,
	commit string,
) observationpublication.DownstreamAuthority {
	return t40r1PublishObservationForRepository(
		t, ctx, dataDir, t40r1DescriptorGitBlobRepo, repositoryDirectory, commit,
	)
}

func t40r1PublishObservationForRepository(
	t *testing.T,
	ctx context.Context,
	dataDir,
	repository,
	repositoryDirectory,
	commit string,
) observationpublication.DownstreamAuthority {
	t.Helper()
	root := filepath.Join(dataDir, "observations")
	transition, err := observationpublication.BeginInventoryPublicationV2(
		root, repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		ctx, repositoryDirectory, sourceDirectory, repository,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot, err := sourcepartition.BuildSuperRoot(ctx, sourcepartition.BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: transition.SourceDirectory,
		Repository: repository, Source: source,
		Policy: sourcepartition.Policy{
			Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0",
			IncludeSuffixes: []string{".go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sourcePlan, err := sourcepartition.OpenSuperRoot(
		ctx, transition.SourceDirectory, sourceRoot,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observationpublication.BuildInventoryStageV2(
		ctx,
		observationpublication.InventoryBuildRequestV2{
			OutputDirectory:     transition.InventoryDirectory,
			RepositoryDirectory: repositoryDirectory, Plan: sourcePlan,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := observationpublication.CompleteInventoryPublicationV2(
		ctx, root, repository, transition.TransitionID, nil,
	); err != nil {
		t.Fatal(err)
	}
	authority, err := observationpublication.CurrentInventoryDownstreamAuthorityV2(
		ctx, root, repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	return authority
}
