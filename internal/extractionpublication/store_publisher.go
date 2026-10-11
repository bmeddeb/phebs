package extractionpublication

import (
	"context"
	"errors"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

// StorePublisher installs the store half of a T40.10 domain publication. The
// store seals the invisible T40.7 run, independently recounts its chunk and
// evidence populations, and atomically replaces the repository/domain root.
// Runtime writes its filesystem pointer only after this transition succeeds.
type StorePublisher struct {
	Store store.PartitionedEvidenceStore
}

func (publisher StorePublisher) PublishDomain(
	ctx context.Context,
	plan candidate.DomainResultPlan,
	root candidate.DomainResultRoot,
	runID string,
) error {
	// Count this invocation, including exact-current recounts and later
	// validation/native failures, not only a changed authority or success.
	if err := dispatchadmission.ObserveProductionPublication(ctx); err != nil {
		return err
	}
	if publisher.Store == nil || runID == "" ||
		candidate.ValidateDomainResultPlanControl(plan) != nil ||
		candidate.ValidateDomainResultRoot(root, plan) != nil {
		return errors.New("partition store publisher received invalid authority")
	}
	publication, err := partitionPublication(plan, root, runID)
	if err != nil {
		return err
	}
	return publisher.Store.PublishPartitionedExtractionDomain(ctx, publication)
}

// PublishRefusal settles one terminally refused domain under the same exact
// authority binding as a success, with staged-evidence accounting instead of
// the success equality. The refused run is sealed and rooted by the store,
// which retains it as the attributable record of the refusal.
func (publisher StorePublisher) PublishRefusal(
	ctx context.Context,
	plan candidate.DomainResultPlan,
	root candidate.DomainResultRoot,
	runID string,
) error {
	if err := dispatchadmission.ObserveProductionPublication(ctx); err != nil {
		return err
	}
	if publisher.Store == nil || runID == "" ||
		candidate.ValidateDomainResultPlanControl(plan) != nil ||
		candidate.ValidateDomainResultRoot(root, plan) != nil ||
		root.Disposition != candidate.PartitionResultTerminalRefusal {
		return errors.New("partition store publisher received invalid refusal authority")
	}
	publication, err := partitionPublication(plan, root, runID)
	if err != nil {
		return err
	}
	return publisher.Store.PublishPartitionedExtractionDomainRefusal(ctx, publication)
}

func partitionPublication(
	plan candidate.DomainResultPlan,
	root candidate.DomainResultRoot,
	runID string,
) (store.PartitionedExtractionDomain, error) {
	planRaw, err := canonical(plan)
	if err != nil {
		return store.PartitionedExtractionDomain{}, err
	}
	rootRaw, err := canonical(root)
	if err != nil {
		return store.PartitionedExtractionDomain{}, err
	}
	return store.PartitionedExtractionDomain{
		Schema:     store.PartitionedExtractionDomainSchema,
		Repository: plan.Repository, Domain: plan.Domain, RunID: runID,
		PlanDigest: plan.Digest, RootDigest: root.Digest,
		CandidateDigest:   plan.CandidateManifestDigest,
		SourceDigest:      plan.SourceGenerationDigest,
		ObservationDigest: plan.ObservationGenerationDigest,
		Facts:             root.Totals.Facts, Rows: root.Totals.Rows,
		References: root.Totals.References,
		Plan:       string(planRaw), Root: string(rootRaw),
	}, nil
}

var _ Publisher = StorePublisher{}
