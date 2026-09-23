package t421

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// Model the already-admitted F and owned catalog at the product-query handoff;
// this does not claim that a native server or the predecessor phases ran.
func TestEpochProductQueryContextFinalBinding(t *testing.T) {
	bound, catalog := epochQueryProjectionFixture(t)
	for _, mode := range []string{"complete_v4", "catalog_population", "caller_publication", "resolver_catalog_counts", "rpc_postings", "missing_observation", "query_authority", "missing_baseline", "changed_baseline", "wrong_borrower", "wrong_repository", "wrong_catalog"} {
		t.Run(mode, func(t *testing.T) {
			wire := bound.final
			wire.Authority.CallerGenerationSHA256, wire.Authority.CallerRootSHA256 = testDigest("caller-generation"), testDigest("caller-root")
			wire.CatalogPopulation = &ExecutionCatalogPopulation{AcceptedServices: uint64(len(catalog.Services))}
			wire.CallerPublication = &ExecutionCallerPublicationObservation{
				RelationshipRootReads: 1, RelationshipGenerationReads: 1,
				GenerationSHA256: wire.Authority.CallerGenerationSHA256, ManifestSHA256: wire.Authority.CallerRootSHA256,
				Leaves: []callerpublication.LeafObservation{}, RPCProjection: SetIdentity{Records: 1, FramedBytes: 1, SHA256: testDigest("rpc")},
			}
			wire.ResolverCatalogCounts = &readaccounting.ResolverCatalogCounts{
				GenerationSHA256: wire.Authority.ResolverCatalogGenerationSHA256, ManifestSHA256: wire.Authority.ResolverCatalogRootSHA256,
				DeclarationRecords: 5, GeneratedDescriptors: 7,
			}
			wire.RPCPostings = &ExecutionRPCPostingObservation{Resolved: 2, NameMatch: 1, Unresolved: 3}
			// Hash the supplied native-shaped response independently of the
			// handoff's epochFinalBody reconstruction.
			digest := sha256.Sum256(epochTestJSON(t, wire, true))
			authority := AuthorityPhaseResult{Phase: "product_queries", ExtractionRoots: wire.ExtractionRoots}
			projection := PhaseStateProjection{Phase: "product_queries"}
			if err := json.Unmarshal(epochQueryMarshal(t, wire.Authority), &authority.AuthorityState); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(epochQueryMarshal(t, wire.Projection), &projection); err != nil {
				t.Fatal(err)
			}
			reader := &executionEpochInspection{
				productBaseline: &digest, productQueryAuthority: *wire.QueryAuthority,
				finalCatalogPopulation: wire.CatalogPopulation, finalCallerPublication: wire.CallerPublication,
				finalResolverCatalogCounts: wire.ResolverCatalogCounts, finalRPCPostings: wire.RPCPostings,
			}
			run := &ExecutionEpochOneRun{inspection: reader,
				epoch: ExecutionEpochConfig{Epoch: 5, Repository: bound.repository, CatalogSHA256: testDigest("catalog-bytes")}}
			author := &ExecutionAuthorCustody{borrowedBy: run}
			epochs := &ExecutionEpochConfigCustody{author: author, active: true, released: 5, queryCatalog: &catalog}
			epochs.epochs[4] = run.epoch
			run.flow = &ExecutionEpochOne{epochs: epochs}
			switch mode {
			case "catalog_population":
				reader.finalCatalogPopulation.AcceptedServices++
			case "caller_publication":
				reader.finalCallerPublication.RelationshipRootReads++
			case "resolver_catalog_counts":
				reader.finalResolverCatalogCounts.DeclarationRecords++
			case "rpc_postings":
				reader.finalRPCPostings.Unresolved++
			case "missing_observation":
				reader.finalCallerPublication = nil
			case "query_authority":
				reader.productQueryAuthority.ResolverNamespaceRootSHA256 = testDigest("changed")
			case "missing_baseline":
				reader.productBaseline = nil
			case "changed_baseline":
				reader.productBaseline[0] ^= 1
			case "wrong_borrower":
				author.borrowedBy = &ExecutionEpochOneRun{}
			case "wrong_repository":
				epochs.epochs[4].Repository = "github.com/t421/other"
			case "wrong_catalog":
				epochs.epochs[4].CatalogSHA256 = testDigest("changed")
			}
			got, err := run.productQueryContext(t.Context(), authority, projection)
			if mode != "complete_v4" {
				if !errors.Is(err, ErrExecutionEpochOne) || got != nil {
					t.Fatalf("mutated F or custody accepted: context=%v, error=%v", got, err)
				}
				return
			}
			if err != nil || got == nil {
				t.Fatalf("complete V4 handoff refused: %v", err)
			}
			if got.repository != bound.repository || !reflect.DeepEqual(got.final, wire) || !reflect.DeepEqual(got.services, bound.services) || !reflect.DeepEqual(got.placements, bound.placements) {
				t.Fatal("query context changed the admitted F or catalog projection")
			}
			if got.final.CatalogPopulation == reader.finalCatalogPopulation || got.final.CallerPublication == reader.finalCallerPublication ||
				got.final.ResolverCatalogCounts == reader.finalResolverCatalogCounts || got.final.RPCPostings == reader.finalRPCPostings {
				t.Fatal("query context retained mutable inspection observations")
			}
		})
	}
}
