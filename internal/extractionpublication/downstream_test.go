package extractionpublication

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

type downstreamSnapshotStore struct {
	publication *store.PartitionedExtractionDomain
	calls       int
	repository  string
	domain      string
}

func (state *downstreamSnapshotStore) GetPartitionedExtractionDomain(
	_ context.Context,
	repository, domain string,
) (*store.PartitionedExtractionDomain, error) {
	state.calls++
	state.repository = repository
	state.domain = domain
	if state.publication == nil {
		return nil, nil
	}
	publication := *state.publication
	return &publication, nil
}

func TestCurrentSnapshotReturnsExactPlanRootAndAuthority(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("1", 64), false)
	root, err := candidate.BuildDomainResultRoot(plan, []candidate.PartitionResult{})
	if err != nil {
		t.Fatal(err)
	}
	planRaw, err := canonical(plan)
	if err != nil {
		t.Fatal(err)
	}
	rootRaw, err := canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	publication := &store.PartitionedExtractionDomain{
		Schema:     store.PartitionedExtractionDomainSchema,
		Repository: plan.Repository, Domain: plan.Domain, RunID: "run-current",
		PlanDigest: plan.Digest, RootDigest: root.Digest,
		CandidateDigest:   plan.CandidateManifestDigest,
		SourceDigest:      plan.SourceGenerationDigest,
		ObservationDigest: plan.ObservationGenerationDigest,
		Facts:             root.Totals.Facts, Rows: root.Totals.Rows,
		References: root.Totals.References,
		Plan:       string(planRaw), Root: string(rootRaw),
	}
	state := &downstreamSnapshotStore{publication: publication}

	snapshot, err := CurrentSnapshot(t.Context(), state, plan.Repository, plan.Domain)
	if err != nil {
		t.Fatal(err)
	}
	wantAuthority, err := candidate.NewDownstreamDomainAuthority(plan, root, publication.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Plan, plan) || !reflect.DeepEqual(snapshot.Root, root) ||
		snapshot.Authority != wantAuthority {
		t.Fatalf("current snapshot = %+v", snapshot)
	}
	if state.calls != 1 || state.repository != plan.Repository || state.domain != plan.Domain {
		t.Fatalf("store reads = %d %q %q", state.calls, state.repository, state.domain)
	}

	authority, err := CurrentDomainAuthority(t.Context(), state, plan.Repository, plan.Domain)
	if err != nil || authority != wantAuthority || state.calls != 2 {
		t.Fatalf("delegated authority = %+v, reads=%d, error=%v", authority, state.calls, err)
	}
}

func TestCurrentSnapshotRefusesMissingOrMismatchedPublication(t *testing.T) {
	if _, err := CurrentSnapshot(t.Context(), &downstreamSnapshotStore{}, "repo", "domain"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing publication error = %v", err)
	}

	plan := buildTestPlan(t, "sha256:"+strings.Repeat("2", 64), false)
	root, err := candidate.BuildDomainResultRoot(plan, []candidate.PartitionResult{})
	if err != nil {
		t.Fatal(err)
	}
	planRaw, err := canonical(plan)
	if err != nil {
		t.Fatal(err)
	}
	rootRaw, err := canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	state := &downstreamSnapshotStore{publication: &store.PartitionedExtractionDomain{
		Schema:     store.PartitionedExtractionDomainSchema,
		Repository: plan.Repository, Domain: plan.Domain, RunID: "run-current",
		PlanDigest: plan.Digest, RootDigest: root.Digest,
		CandidateDigest:   "sha256:" + strings.Repeat("f", 64),
		SourceDigest:      plan.SourceGenerationDigest,
		ObservationDigest: plan.ObservationGenerationDigest,
		Facts:             root.Totals.Facts, Rows: root.Totals.Rows,
		References: root.Totals.References,
		Plan:       string(planRaw), Root: string(rootRaw),
	}}
	if _, err := CurrentSnapshot(t.Context(), state, plan.Repository, plan.Domain); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched publication error = %v", err)
	}
}
