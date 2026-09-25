package api_test

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestProofFieldEndpointPersistsFieldResult(t *testing.T) {
	const (
		repo    = "github.com/acme/field-proof"
		domain  = "scip-proto-field"
		lineage = "contract_scip_package_v1_shop_cart"
		message = "shop.Cart"
	)
	run := proofRun(repo, domain, "run-field-proof")
	assertion, resolution := proofAssertion(
		repo,
		run.ID,
		"field-proof",
		"REFERENCES_PROTO_FIELD",
		message+"#1",
		lineage,
		"shared",
	)
	st := &proofAPIStore{
		repos: []store.Repo{{
			Name: repo, IndexedCommitHash: run.Commit,
		}},
		runs: map[string]store.ExtractionRun{
			proofScope(repo, domain): run,
		},
		assertions: map[string][]store.Assertion{
			repo: {assertion},
		},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(
				repo,
				run.ID,
				assertion.Supporting[0],
			): resolution,
		},
		bundles: map[string]store.ProofBundleRecord{},
	}
	service := api.NewFieldReferenceService(api.Options{
		Store: st, Evidence: st,
		Principal: func(context.Context) string {
			return "user:field-proof"
		},
		AuthorizationProvider: "field-proof-v1",
	})
	if len(st.bundles) != 0 {
		t.Fatal("proof bundle persisted before proof request")
	}

	proof := api.NewProofService(api.Options{
		Store: st, Evidence: st, ProofBundles: st,
		FieldReferences: service,
		Principal: func(context.Context) string {
			return "user:field-proof"
		},
		AuthorizationProvider: "field-proof-v1",
	})
	envelope, err := proof.FindProtoFieldReferences(
		context.Background(),
		lineage,
		message,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.bundles) != 1 ||
		envelope.Bundle.Query.Kind != "find_proto_field_references" ||
		len(envelope.Bundle.Assertions) != 1 ||
		envelope.Bundle.Assertions[0].ID != assertion.ID {
		t.Fatalf(
			"proof field mismatch: bundle=%+v stored=%d",
			envelope,
			len(st.bundles),
		)
	}
}

func TestProofFieldEndpointRebuildsAfterPersistenceConflict(t *testing.T) {
	const (
		repo    = "github.com/acme/field-proof-race"
		domain  = "scip-proto-field"
		lineage = "contract_scip_package_v1_shop_cart"
		message = "shop.Cart"
	)
	firstRun := proofRun(repo, domain, "run-field-proof-first")
	firstAssertion, firstResolution := proofAssertion(
		repo,
		firstRun.ID,
		"field-proof-first",
		"REFERENCES_PROTO_FIELD",
		message+"#1",
		lineage,
		"first",
	)
	replacementRun := proofRun(
		repo,
		domain,
		"run-field-proof-replacement",
	)
	replacementAssertion, replacementResolution := proofAssertion(
		repo,
		replacementRun.ID,
		"field-proof-replacement",
		"REFERENCES_PROTO_FIELD",
		message+"#1",
		lineage,
		"replacement",
	)
	st := &proofAPIStore{
		repos: []store.Repo{{
			Name: repo, IndexedCommitHash: firstRun.Commit,
		}},
		runs: map[string]store.ExtractionRun{
			proofScope(repo, domain): firstRun,
		},
		assertions: map[string][]store.Assertion{
			repo: {firstAssertion},
		},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(
				repo,
				firstRun.ID,
				firstAssertion.Supporting[0],
			): firstResolution,
			proofEvidenceScope(
				repo,
				replacementRun.ID,
				replacementAssertion.Supporting[0],
			): replacementResolution,
		},
		bundles:      map[string]store.ProofBundleRecord{},
		putConflicts: 1,
	}
	st.onPutConflict = func() {
		st.runs[proofScope(repo, domain)] = replacementRun
		st.assertions[repo] = []store.Assertion{replacementAssertion}
	}
	proof := api.NewProofService(api.Options{
		Store: st, Evidence: st, ProofBundles: st,
		Principal: func(context.Context) string {
			return "user:field-proof-race"
		},
		AuthorizationProvider: "field-proof-race-v1",
	})
	envelope, err := proof.FindProtoFieldReferences(
		context.Background(),
		lineage,
		message,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if st.putConflicts != 0 ||
		len(st.bundles) != 1 ||
		len(envelope.Bundle.Assertions) != 1 ||
		envelope.Bundle.Assertions[0].ID != replacementAssertion.ID ||
		len(envelope.Bundle.Coverage.Repositories) != 1 ||
		len(envelope.Bundle.Coverage.Repositories[0].Runs) != 1 ||
		envelope.Bundle.Coverage.Repositories[0].Runs[0].RunID !=
			replacementRun.ID {
		t.Fatalf(
			"proof did not rebuild after publication race: envelope=%+v bundles=%d",
			envelope,
			len(st.bundles),
		)
	}
}
