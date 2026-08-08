package extractionpublication

import (
	"context"
	"errors"

	"github.com/bmeddeb/phebs/internal/candidate"
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
	if publisher.Store == nil || runID == "" ||
		candidate.ValidateDomainResultPlanControl(plan) != nil ||
		candidate.ValidateDomainResultRoot(root, plan) != nil {
		return errors.New("partition store publisher received invalid authority")
	}
	planRaw, err := canonical(plan)
	if err != nil {
		return err
	}
	rootRaw, err := canonical(root)
	if err != nil {
		return err
	}
	return publisher.Store.PublishPartitionedExtractionDomain(ctx, store.PartitionedExtractionDomain{
		Schema:     store.PartitionedExtractionDomainSchema,
		Repository: plan.Repository, Domain: plan.Domain, RunID: runID,
		PlanDigest: plan.Digest, RootDigest: root.Digest,
		CandidateDigest:   plan.CandidateManifestDigest,
		SourceDigest:      plan.SourceGenerationDigest,
		ObservationDigest: plan.ObservationGenerationDigest,
		Facts:             root.Totals.Facts, Rows: root.Totals.Rows,
		References: root.Totals.References,
		Plan:       string(planRaw), Root: string(rootRaw),
	})
}

var _ Publisher = StorePublisher{}
