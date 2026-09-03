package t421

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogingest"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t401"
)

type productionRelationshipComponents struct {
	upstream downstreamauthority.Authority
	resolver *resolvernamespace.Publication
	rpc      *rpccallerposting.Publication
	kafka    *kafkatopicposting.Publication
}

// productionLogicalAuthorities is a constructor witness, not ceremony evidence.
// It uses the exact authored source and native catalog census, all 10,000 live
// service-state rows, and the real component/relationship constructors. Search
// input leaves and receipt measurements remain explicitly modeled test inputs;
// this helper neither builds a real search index nor publishes a runtime selector.
func productionLogicalAuthorities(
	t *testing.T,
	plan Plan,
	physical map[string]productionIdentityState,
) map[string]AuthorityPhaseResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := store.OpenLocalMemory(ctx, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		if err := state.Close(closeCtx); err != nil {
			t.Errorf("close constructor-witness store: %v", err)
		}
	}()
	if err := state.UpsertRepo(ctx, store.Repo{
		Name: t401.RepositoryName, CloneURL: "https://" + t401.RepositoryName,
	}); err != nil {
		t.Fatal(err)
	}
	corpus, err := BuildCombinedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	components := make(map[string]productionRelationshipComponents, len(physical))
	reader, err := store.NewServiceStateV3Reader(state, servicecatalogv3.NewDefaultReadCache())
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]AuthorityPhaseResult, 5)
	var returnGeneration servicecatalogv3.Generation
	var returnSnapshot store.ServiceStateV3Snapshot
	for _, transition := range []struct{ group, physical, logical string }{
		{"a", "a", "a"},
		{"b-physical", "b", "a"},
		{"b-logical", "b", "b"},
		{"a-return", "a-return", "a-return"},
	} {
		input := physical[transition.physical]
		if err := state.SetRepoIndexed(ctx, t401.RepositoryName, input.Authority.PhysicalCommit, time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
		catalog := productionLogicalCatalog(t, corpus.Catalog, transition.logical)
		generation := productionIngestCatalog(ctx, t, state, input.DataDir, filepath.Join(root, "catalog.json"), catalog)
		if generation.Root.Binding.Source.FileCount != int(plan.Profile.Physical.CombinedRegularFiles) ||
			generation.Root.Binding.Source.AcceptedFileCount != int(plan.Profile.Logical.AcceptedPhysicalFiles) {
			t.Fatalf("%s native catalog census differs from the full frozen source: %+v", transition.group, generation.Root.Binding.Source)
		}
		reconcile, err := state.BeginServiceStateV3Reconcile(ctx, t401.RepositoryName)
		if err != nil {
			t.Fatal(err)
		}
		productionRunStatePlan(ctx, t, state, reconcile)
		activation, err := state.BeginServiceStateV3Activation(ctx, t401.RepositoryName, input.Authority.SearchGenerationSHA256)
		if err != nil {
			t.Fatal(err)
		}
		units := productionRunStatePlan(ctx, t, state, activation)
		snapshot, err := reader.AcceptedSnapshot(ctx, t401.RepositoryName)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.States) != int(plan.Profile.Logical.AcceptedServices) ||
			snapshot.Summary.CurrentCount != len(snapshot.States) ||
			snapshot.Summary.StaleCount != 0 || snapshot.Summary.UnavailableCount != 0 || snapshot.Summary.ConflictCount != 0 {
			t.Fatalf("%s native activation did not make every accepted service current: %+v", transition.group, snapshot.Summary)
		}
		component, present := components[transition.physical]
		if !present {
			component = productionBuildRelationshipComponents(ctx, t, root, input, input.NativeDomains)
			components[transition.physical] = component
		}
		relationship := productionBuildRelationship(ctx, t, root, plan, generation, snapshot, component)
		authority := input.Authority
		authority.LogicalRevision = transition.logical
		authority.CatalogRootSHA256 = generation.Root.Digest
		authority.CatalogActivationPlanSHA256 = activation.Plan.Digest
		authority.CatalogActivationScheduleSHA256 = activation.Schedule.Digest
		authority.CatalogActivationUnitSHA256 = units[9]
		if authority.CatalogActivationUnitSHA256 == "" {
			t.Fatal("native activation did not claim the exact selected ninth member")
		}
		authority.RelationshipGenerationSHA256 = relationship.GenerationDigest
		authority.RelationshipRootSHA256 = relationship.Digest
		authority.RelationshipProvenanceSHA256 = component.upstream.ProvenanceDigest
		result[transition.group] = AuthorityPhaseResult{
			AuthorityState: authority, ExtractionRoots: slices.Clone(input.ExtractionRoots),
		}
		if transition.group == "a-return" {
			returnGeneration, returnSnapshot = generation, snapshot
		}
	}
	// Full restore clears extraction_domain_root and reconstructible extraction
	// controls. Re-extraction therefore creates fresh run tokens even when every
	// semantic result is unchanged. Derive that changed input through the same
	// native run constructor; do not invent an arbitrary archive-phase digest.
	// This witnesses the provenance transition, not archive/restore execution.
	restoredDomains := productionModeledArchiveDomains(ctx, t, state, physical["a-return"])
	restoredComponents := productionBuildRelationshipComponents(ctx, t, root, physical["a-return"], restoredDomains)
	restoredRoot := productionBuildRelationship(ctx, t, root, plan, returnGeneration, returnSnapshot, restoredComponents)
	archive := result["a-return"]
	archive.RelationshipGenerationSHA256 = restoredRoot.GenerationDigest
	archive.RelationshipRootSHA256 = restoredRoot.Digest
	archive.RelationshipProvenanceSHA256 = restoredComponents.upstream.ProvenanceDigest
	if archive.RelationshipGenerationSHA256 == result["a-return"].RelationshipGenerationSHA256 ||
		archive.RelationshipRootSHA256 == result["a-return"].RelationshipRootSHA256 ||
		archive.RelationshipProvenanceSHA256 == result["a-return"].RelationshipProvenanceSHA256 {
		t.Fatal("native replacement extraction runs did not replace relationship provenance and identities")
	}
	result["archive"] = archive
	return result
}

func productionBuildRelationship(
	ctx context.Context,
	t *testing.T,
	root string,
	plan Plan,
	generation servicecatalogv3.Generation,
	snapshot store.ServiceStateV3Snapshot,
	components productionRelationshipComponents,
) relationshippublication.RootV3 {
	t.Helper()
	prepared, err := relationshippublication.BuildV3(ctx, relationshippublication.BuildRequestV3{
		Root: root, Catalog: generation,
		States: snapshot.States, ServiceSummary: snapshot.Summary,
		Resolver: components.resolver, RPC: components.rpc, Kafka: components.kafka, Upstream: components.upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The prepared root stays in this test's temporary directory. No pins,
	// selector, or live publication are impersonated to create a runtime pass.
	relationship := prepared.Root()
	want := plan.Oracle.ProductRelationships
	if relationship.ProjectionCount != int(want.TotalProjections) ||
		relationship.ServiceReferenceCount != int(want.ServiceReferences) ||
		relationship.ServiceCount != len(snapshot.States) || !relationship.AllServicesComplete {
		t.Fatalf("native relationship constructor did not preserve the full oracle: %+v", relationship)
	}
	return relationship
}

func productionLogicalCatalog(t *testing.T, base servicecatalog.Catalog, revision string) servicecatalog.Catalog {
	t.Helper()
	catalog := cloneCatalog(base)
	switch revision {
	case "a":
		catalog.Authority.Version = combinedAuthorityA
	case "b":
		catalog.Authority.Version = combinedAuthorityB
		catalog.Services[len(catalog.Services)/2].DisplayName += "-b"
	case "a-return":
		catalog.Authority.Version = combinedAuthorityAReturn
	default:
		t.Fatalf("unknown logical constructor-witness revision %q", revision)
	}
	return catalog
}

func productionIngestCatalog(
	ctx context.Context,
	t *testing.T,
	state *store.Surreal,
	dataDir, catalogPath string,
	catalog servicecatalog.Catalog,
) servicecatalogv3.Generation {
	t.Helper()
	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	reconciler := servicecatalogingest.V3Reconciler{
		DataDir: dataDir, Store: state,
		Selections: map[string]config.ServiceCatalog{t401.RepositoryName: {
			Kind: catalog.Authority.Kind, ID: catalog.Authority.ID, Version: catalog.Authority.Version,
			Path: catalogPath, Runtime: config.ServiceCatalogRuntimeV3,
		}},
	}
	outcome, err := reconciler.ReconcileRepository(ctx, t401.RepositoryName)
	if err != nil || outcome != servicecatalogingest.OutcomePublished {
		t.Fatalf("native catalog census/publication: outcome=%q, err=%v", outcome, err)
	}
	publication, err := state.GetServiceCatalogV3Candidate(ctx, t401.RepositoryName)
	if err != nil {
		t.Fatal(err)
	}
	return publication.Generation
}

func productionRunStatePlan(
	ctx context.Context,
	t *testing.T,
	state *store.Surreal,
	begin store.ServiceStateV3Begin,
) map[int64]string {
	t.Helper()
	if begin.Noop || begin.Plan == nil || begin.Schedule == nil {
		t.Fatal("constructor-witness transition lacks a native service-state plan")
	}
	schedule := begin.Schedule
	for attempts := 0; schedule.NextOffset < schedule.TotalItems; attempts++ {
		if attempts >= schedule.TotalChunks {
			t.Fatal("native service-state schedule expansion made insufficient progress")
		}
		var err error
		schedule, err = state.ExpandGenerationSchedule(ctx, schedule.Repository, schedule.Stage, schedule.Generation)
		if err != nil {
			t.Fatal(err)
		}
	}
	units := make(map[int64]string, schedule.TotalChunks)
	for range schedule.TotalChunks {
		chunk, err := state.ClaimGenerationChunk(ctx, store.GenerationResourceCPU, "t421-native-identity-witness")
		if err != nil {
			t.Fatal(err)
		}
		if chunk.ScheduleDigest != schedule.Digest {
			t.Fatal("native service-state worker claimed an unrelated test schedule")
		}
		if _, duplicate := units[chunk.Offset]; duplicate {
			t.Fatal("native service-state constructor witness repeated a chunk")
		}
		units[chunk.Offset] = chunk.Identity
		if _, err := state.ProcessServiceStateV3Chunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
		if err := state.CompleteGenerationChunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
	}
	settled, err := state.GetGenerationSchedule(ctx, schedule.Repository, schedule.Stage)
	if err != nil || settled.Status != store.GenerationScheduleSettled || settled.Succeeded != schedule.TotalChunks {
		t.Fatalf("native service-state schedule did not settle exactly: schedule=%+v, err=%v", settled, err)
	}
	return units
}

func productionModeledArchiveDomains(
	ctx context.Context,
	t *testing.T,
	state *store.Surreal,
	input productionIdentityState,
) []candidate.DownstreamDomainAuthority {
	t.Helper()
	domains := make([]candidate.DownstreamDomainAuthority, 0, len(input.Plans))
	for domain, plan := range input.Plans {
		// Archive is deliberately still a provenance constructor model. These
		// fresh invisible tokens are not restored or re-extracted store authority;
		// normal physical/logical components use the actual completed runs.
		run, err := state.BeginPartitionedExtractionRun(ctx, store.ExtractionScope{
			Repository: t401.RepositoryName, Commit: input.Authority.PhysicalCommit, Domain: domain,
		}, plan.ExtractorVersion, plan.Digest, plan.CandidateManifestDigest, plan.Schema,
			store.PartitionedExtractionRunLimits{
				Facts: plan.Reserved.Facts, Rows: plan.Reserved.Rows, References: plan.Reserved.References,
			})
		if err != nil {
			t.Fatal(err)
		}
		authority, err := candidate.NewDownstreamDomainAuthority(plan, input.Roots[domain], run.ID)
		if err != nil {
			t.Fatal(err)
		}
		domains = append(domains, authority)
	}
	return domains
}

func productionBuildRelationshipComponents(
	ctx context.Context,
	t *testing.T,
	root string,
	input productionIdentityState,
	domains []candidate.DownstreamDomainAuthority,
) productionRelationshipComponents {
	t.Helper()
	if len(domains) != len(input.Plans) {
		t.Fatal("relationship constructor lacks the complete native domain provenance inventory")
	}
	upstream, err := downstreamauthority.Build(input.ObservationSource.DownstreamAuthority(), domains)
	if err != nil {
		t.Fatal(err)
	}
	resolverStage, err := resolvernamespace.BuildV2(ctx, resolvernamespace.BuildRequestV2{
		BuildRequest: resolvernamespace.BuildRequest{
			Root: root, Repository: t401.RepositoryName,
			Commit: input.Authority.PhysicalCommit, ResolverGenerationDigest: input.Authority.ResolverCatalogGenerationSHA256,
			ResolverManifestDigest: input.Authority.ResolverCatalogRootSHA256, Descriptors: input.Descriptors,
			ResidentLimitBytes: relationshippublication.ResolverResidentLimit,
		}, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := resolverStage.Publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rpcStage, err := rpccallerposting.BuildV2(ctx, rpccallerposting.BuildRequestV2{
		Root: root, Observations: input.ObservationSource,
		Resolver: resolver, Upstream: upstream, ResidentLimitBytes: relationshippublication.RPCResidentLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	rpc, err := rpcStage.Publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kafkaStage, err := kafkatopicposting.BuildV2(ctx, kafkatopicposting.BuildRequestV2{
		Root: root, Observations: input.ObservationSource, Upstream: upstream,
		ResidentLimitBytes: relationshippublication.KafkaResidentLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	kafka, err := kafkaStage.Publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return productionRelationshipComponents{upstream: upstream, resolver: resolver, rpc: rpc, kafka: kafka}
}
