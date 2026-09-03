package t421

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
)

// TestFrozenOverlayNativeRelationshipComponents is a component regression, not
// a full constructor, ordinary-server, recovery, or ceremony pass. It retains
// every frozen overlay file and the existing replay's representative structural
// Go/control inputs, rather than the two-million-file structural inventory.
// Source, observations, candidates, declaration facts/results, resolver symbols,
// and postings are produced by their real constructors. Only the declaration
// store publication/current selector and run ID use the strict standalone replay
// model; the component upstream contains that one genuinely executed domain.
// It does not execute the other eight domains, caller leaves, service projection,
// durable scheduling, publication fences, archive, or server authorization.
func TestFrozenOverlayNativeRelationshipComponents(t *testing.T) {
	// Test allowance: twice the initial run rounded to five minutes. This is
	// host-variance headroom, not a production or ceremony deadline change.
	ctx, cancel := context.WithTimeout(t.Context(), 2*5*time.Minute)
	defer cancel()
	combined, err := BuildCombinedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	profile := combined.Profile.Pipeline
	dataDir := t.TempDir()
	repositoryDir, commit := t421ProductionReplayRepositoryFixture(t, ctx, dataDir)
	observations := frozenOverlayComponentObservations(ctx, t, dataDir, repositoryDir, commit)
	if got := observations.DownstreamAuthority().ObservedCount; uint64(got) != profile.SupportedGoFiles {
		t.Fatalf("observed full-overlay Go records = %d, want %d", got, profile.SupportedGoFiles)
	}
	state, descriptors, domain := frozenOverlayComponentResolver(ctx, t, dataDir, repositoryDir, commit, observations)
	if uint64(len(descriptors)) != profile.GeneratedDescriptors {
		t.Fatalf("production resolver descriptors = %d, want %d", len(descriptors), profile.GeneratedDescriptors)
	}
	upstream, err := downstreamauthority.Build(observations.DownstreamAuthority(), []candidate.DownstreamDomainAuthority{domain})
	if err != nil {
		t.Fatal(err)
	}
	resolverStage, err := resolvernamespace.BuildV2(ctx, resolvernamespace.BuildRequestV2{
		BuildRequest: resolvernamespace.BuildRequest{
			Root: dataDir, Repository: state.Repository, Commit: state.Commit,
			ResolverGenerationDigest: state.GenerationDigest, ResolverManifestDigest: state.AuthorityDigest,
			Descriptors: descriptors, ResidentLimitBytes: relationshippublication.ResolverResidentLimit,
		}, Upstream: upstream,
	})
	if err != nil {
		t.Fatalf("build full-overlay resolver namespaces: %v", err)
	}
	defer func() {
		if err := resolverStage.Discard(); err != nil {
			t.Error(err)
		}
	}()
	resolver, err := resolverStage.Publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolver.Root().NamespaceCount; uint64(got) != profile.GeneratedMappings {
		t.Fatalf("resolver namespaces = %d, want %d", got, profile.GeneratedMappings)
	}
	frozenOverlayComponentLookupDemand(ctx, t, observations, resolver)

	rpcStage, err := rpccallerposting.BuildV2(ctx, rpccallerposting.BuildRequestV2{
		Root: dataDir, Observations: observations, Resolver: resolver, Upstream: upstream,
		ResidentLimitBytes: relationshippublication.RPCResidentLimit,
	})
	if err != nil {
		t.Fatalf("build full-overlay RPC postings: %v", err)
	}
	defer func() {
		if err := rpcStage.Discard(); err != nil {
			t.Error(err)
		}
	}()
	rpc, err := rpcStage.Publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rpcRows := 0
	if err := rpc.WalkPostings(ctx, func(posting rpccallerposting.Posting) error {
		if posting.Protocol != "grpc" || posting.Class != "resolved" || posting.SourceRole != "production" {
			return fmt.Errorf("unexpected full-overlay RPC posting: protocol=%s class=%s role=%s", posting.Protocol, posting.Class, posting.SourceRole)
		}
		rpcRows++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rpcRoot := rpc.Root()
	if uint64(rpcRows) != profile.RPCCallPostings || rpcRoot.PostingCount != rpcRows ||
		rpcRoot.ResolvedCount != rpcRows || rpcRoot.NameMatchCount != 0 || rpcRoot.UnresolvedCount != 0 {
		t.Fatalf("full-overlay RPC totals = rows %d, root %+v", rpcRows, rpcRoot)
	}

	kafkaStage, err := kafkatopicposting.BuildV2(ctx, kafkatopicposting.BuildRequestV2{
		Root: dataDir, Observations: observations, Upstream: upstream,
		ResidentLimitBytes: relationshippublication.KafkaResidentLimit,
	})
	if err != nil {
		t.Fatalf("build full-overlay Kafka postings: %v", err)
	}
	defer func() {
		if err := kafkaStage.Discard(); err != nil {
			t.Error(err)
		}
	}()
	kafka, err := kafkaStage.Publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kafkaRows := 0
	if err := kafka.WalkPostings(ctx, func(kafkatopicposting.Posting) error {
		kafkaRows++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	kafkaRoot := kafka.Root()
	if uint64(kafkaRoot.ProducerCount) != profile.KafkaProducerPostings ||
		uint64(kafkaRoot.ConsumerCount) != profile.KafkaConsumerPostings ||
		kafkaRows != kafkaRoot.PostingCount || kafkaRows != kafkaRoot.ProducerCount+kafkaRoot.ConsumerCount ||
		kafkaRoot.LiteralCount != kafkaRows || kafkaRoot.UnresolvedCount != 0 {
		t.Fatalf("full-overlay Kafka totals = rows %d, root %+v", kafkaRows, kafkaRoot)
	}
	t.Logf("component-only PASS: namespaces=%d RPC resolved=%d Kafka producer rows=%d consumer rows=%d; resident limits=%d/%d/%d bytes; no service relationship-pair or ordinary-server claim",
		resolver.Root().NamespaceCount, rpcRows, kafkaRoot.ProducerCount, kafkaRoot.ConsumerCount,
		relationshippublication.ResolverResidentLimit, relationshippublication.RPCResidentLimit, relationshippublication.KafkaResidentLimit)
}

func frozenOverlayComponentObservations(
	ctx context.Context, t *testing.T, dataDir, repositoryDir, commit string,
) *observationpublication.InventoryPublicationV2 {
	t.Helper()
	sourceDir := filepath.Join(dataDir, "source")
	source, err := repositoryindex.BuildSourceGeneration(ctx, repositoryDir, sourceDir, t421ProductionReplayRepository,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}})
	if err != nil {
		t.Fatal(err)
	}
	partitionDir := filepath.Join(dataDir, "source-partitions")
	if err := os.Mkdir(partitionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sourceRoot, err := sourcepartition.BuildSuperRoot(ctx, sourcepartition.BuildRequest{
		SourceDirectory: sourceDir, OutputDirectory: partitionDir, Repository: t421ProductionReplayRepository,
		Source: source, Policy: sourcepartition.Policy{
			Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sourcepartition.OpenSuperRoot(ctx, partitionDir, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(dataDir, "observations")
	root, err := observationpublication.BuildInventoryStageV2(ctx, observationpublication.InventoryBuildRequestV2{
		OutputDirectory: directory, RepositoryDirectory: repositoryDir, Plan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := observationpublication.OpenInventoryV2(ctx, directory, root)
	if err != nil {
		t.Fatal(err)
	}
	return publication
}

func frozenOverlayComponentResolver(
	ctx context.Context, t *testing.T, dataDir, repositoryDir, commit string,
	observations observationpublication.DownstreamSource,
) (resolvercatalog.State, []gocaller.DirectDescriptor, candidate.DownstreamDomainAuthority) {
	t.Helper()
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
	stageDir := filepath.Join(dataDir, "candidate-stage")
	for _, directory := range []string{candidateRoot, stageDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := candidate.Build(ctx, candidate.Request{
		RepoDir: repositoryDir, OutputDir: stageDir, Repository: t421ProductionReplayRepository, Commit: commit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := candidate.Expected{
		Repository: t421ProductionReplayRepository, Commit: commit, Policies: identities,
		PolicyDigest: manifest.PolicyDigest, GenerationDigest: manifest.GenerationDigest, ManifestDigest: manifest.Digest,
	}
	state, err := candidate.PublishContext(ctx, candidateRoot, stageDir, expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.FinishPublication(candidateRoot, expected.Repository); err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.OpenContext(ctx, candidateRoot, expected)
	if err != nil {
		t.Fatal(err)
	}
	sparseDir := filepath.Join(dataDir, "candidate-sparse")
	if err := os.Mkdir(sparseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sparseRoot, err := candidate.BuildSparseRoot(ctx, sparseDir, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := candidate.OpenSparse(ctx, sparseDir, candidateRoot, state, sparseRoot.Digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	proto := protodecl.New()
	domain, err := sparse.OpenDomain(ctx, proto.Domain(), proto.Version())
	if err != nil {
		t.Fatal(err)
	}
	observed := observations.DownstreamAuthority()
	plan, err := extractionpublication.BuildReservedPlan(domain, candidate.DomainResultAuthority{
		SourceGenerationDigest: observed.SourceGenerationDigest, ObservationGenerationDigest: observed.ObservationGenerationDigest,
		ExtractorVersion: proto.Version(), ExtractionPolicyDigest: sparseRoot.PolicyDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	const runID = "t421-component-modeled-proto-run"
	evidence := newT421ProductionReplayEvidence()
	executor := extract.EvidencePartitionExecutor{Evidence: evidence, Extractors: extractors}
	source := extractionpublication.GitSparseSource{
		DataDir:    dataDir,
		OpenDomain: func(context.Context, candidate.DomainResultPlan) (*candidate.SparseDomain, error) { return domain, nil },
	}
	results := make([]candidate.PartitionResult, 0, len(plan.Expected))
	for ordinal := range plan.Expected {
		lease, err := source.AcquirePartition(ctx, plan, ordinal)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := executor.ExecutePartition(ctx, plan, ordinal, lease, runID)
		lease.Release()
		if err != nil {
			t.Fatalf("execute full-overlay proto partition %d: %v", ordinal, err)
		}
		result, err := candidate.BuildPartitionResult(plan, ordinal, spec)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, result)
	}
	root, err := candidate.BuildDomainResultRoot(plan, results)
	if err != nil {
		t.Fatal(err)
	}
	// This is the same strict, explicitly modeled publication boundary used by
	// the standalone replay, not a store write or an invented semantic result.
	run := evidence.runs[runID]
	if run == nil || len(results) == 0 {
		t.Fatal("full-overlay proto execution produced no evidence")
	}
	run.sealed = true
	run.authority = store.PartitionedAssertionAuthority{
		Repository: plan.Repository, Domain: plan.Domain, RunID: runID,
		PlanDigest: plan.Digest, RootDigest: root.Digest, Commit: commit,
		CandidateManifestDigest: plan.CandidateManifestDigest, CandidatePolicyDigest: plan.CandidatePolicyDigest,
	}
	evidence.current[plan.Domain] = run.authority
	upstream, err := candidate.NewDownstreamDomainAuthority(plan, root, runID)
	if err != nil {
		t.Fatal(err)
	}
	policySet, err := candidatejob.CompilePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := candidatejob.NewProvider(dataDir, &t421ProductionReplayPointerStore{pointer: store.CandidateManifestPublication{
		Repository: state.Repository, HeadCommit: state.Commit, UnitDigest: state.UnitDigest,
		PolicyDigest: state.PolicyDigest, ManifestDigest: state.ManifestDigest, GenerationDigest: state.GenerationDigest,
		ManifestPath: state.Manifest, ControlRevision: 1, PublishedAt: time.Now().UTC(),
	}}, policySet)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := resolvermaterialize.NewRegistry(extractors)
	if err != nil {
		t.Fatal(err)
	}
	view, err := provider.OpenCandidateManifest(ctx, extract.CandidateManifestRequest{
		Repository: state.Repository, Commit: commit, Domains: registry.CandidateDomains(),
	})
	if err != nil {
		t.Fatal(err)
	}
	declaration := resolvercatalog.DeclarationPublication{
		Domain: plan.Domain, RunID: runID, GenerationDigest: root.Digest,
		AuthoritySchema: store.PartitionedExtractionDomainSchema, PlanDigest: plan.Digest, RootDigest: root.Digest,
	}
	identity, err := resolvercatalog.NewIdentity(state.Repository, commit, state.UnitDigest, view.Identity(),
		[]resolvercatalog.DeclarationPublication{declaration}, registry.Packs())
	if err != nil {
		t.Fatal(err)
	}
	resolverDir := filepath.Join(dataDir, "resolver-catalogs")
	if err := os.Mkdir(resolverDir, 0o700); err != nil {
		t.Fatal(err)
	}
	prepared, err := resolvermaterialize.Build(ctx, resolvermaterialize.BuildRequest{
		Root: resolverDir, RepositoryDir: repositoryDir, Identity: identity, Registry: registry, Manifest: view, Assertions: evidence,
		Declarations: []resolvermaterialize.DeclarationInput{{
			Protocol: resolvermaterialize.ProtocolGRPC, Domain: plan.Domain, RunID: runID, GenerationDigest: root.Digest,
			AuthoritySchema: declaration.AuthoritySchema, PlanDigest: plan.Digest, RootDigest: root.Digest,
			CandidatePolicyDigest: plan.CandidatePolicyDigest,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolverState, err := prepared.Publish(ctx, func(context.Context, resolvercatalog.State) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	_, views, err := resolvermaterialize.OpenCallerResolvers(ctx, resolverDir, resolverState,
		[]resolvermaterialize.Protocol{resolvermaterialize.ProtocolGRPC, resolvermaterialize.ProtocolThrift})
	if err != nil {
		t.Fatal(err)
	}
	descriptors := append(views[resolvermaterialize.ProtocolGRPC].Descriptors(), views[resolvermaterialize.ProtocolThrift].Descriptors()...)
	return resolverState, descriptors, upstream
}

func frozenOverlayComponentLookupDemand(
	ctx context.Context, t *testing.T, observations observationpublication.DownstreamSource, resolver *resolvernamespace.Publication,
) {
	t.Helper()
	keys := make(map[string]struct{})
	walked := 0
	if err := observations.WalkObserved(ctx, func(_ observationpublication.Record, observation sourceobservation.Observation) error {
		walked++
		for _, function := range observation.Functions {
			for _, call := range function.Calls {
				for _, protocol := range []string{"grpc", "thrift"} {
					for _, imported := range observation.Imports {
						if imported.Kind != "blank" {
							keys[protocol+"\x00"+imported.Path] = struct{}{}
						}
					}
					for _, binding := range call.Bindings {
						if binding.ImportPath != "" {
							keys[protocol+"\x00"+binding.ImportPath] = struct{}{}
						}
					}
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	present := 0
	for _, namespace := range resolver.Root().Namespaces {
		if _, ok := keys[namespace.Protocol+"\x00"+namespace.Namespace]; ok {
			present++
		}
	}
	if walked != observations.DownstreamAuthority().ObservedCount || len(keys) != 20_002 || present != 10_000 ||
		len(keys)-present != 10_002 || len(keys) <= rpccallerposting.MaxNamespaceReads {
		t.Fatalf("full-overlay lookup demand: observed=%d keys=%d present=%d absent=%d member-read limit=%d",
			walked, len(keys), present, len(keys)-present, rpccallerposting.MaxNamespaceReads)
	}
	t.Logf("full-overlay lookup demand: observed=%d keys=%d present=%d absent=%d; no observation or namespace filtered",
		walked, len(keys), present, len(keys)-present)
}
