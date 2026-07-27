package api_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestFieldReferenceReadIsPagedVisibilityBoundAndSideEffectFree(
	t *testing.T,
) {
	const (
		visibleRepo = "github.com/acme/visible"
		hiddenRepo  = "github.com/acme/hidden"
		domain      = "scip-proto-field"
		lineage     = "contract_scip_package_v1_shop_cart"
		message     = "shop.Cart"
	)
	visibleRun := proofRun(visibleRepo, domain, "run-visible-field")
	hiddenRun := proofRun(hiddenRepo, domain, "run-hidden-field")
	first, firstResolution := proofAssertion(
		visibleRepo,
		visibleRun.ID,
		"field-a",
		"REFERENCES_PROTO_FIELD",
		message+"#1",
		lineage,
		"visible-a",
	)
	second, secondResolution := proofAssertion(
		visibleRepo,
		visibleRun.ID,
		"field-b",
		"REFERENCES_PROTO_FIELD",
		message+"#1",
		lineage,
		"visible-b",
	)
	hidden, hiddenResolution := proofAssertion(
		hiddenRepo,
		hiddenRun.ID,
		"field-hidden",
		"REFERENCES_PROTO_FIELD",
		message+"#1",
		lineage,
		"secret",
	)
	newStore := func(withHidden bool) *proofAPIStore {
		st := &proofAPIStore{
			repos: []store.Repo{{
				Name: visibleRepo, IndexedCommitHash: visibleRun.Commit,
			}},
			runs: map[string]store.ExtractionRun{
				proofScope(visibleRepo, domain): visibleRun,
			},
			assertions: map[string][]store.Assertion{
				visibleRepo: {first, second},
			},
			resolutions: map[string]store.EvidenceResolution{
				proofEvidenceScope(
					visibleRepo,
					visibleRun.ID,
					first.Supporting[0],
				): firstResolution,
				proofEvidenceScope(
					visibleRepo,
					visibleRun.ID,
					second.Supporting[0],
				): secondResolution,
			},
			bundles: map[string]store.ProofBundleRecord{},
		}
		if withHidden {
			st.repos = append(st.repos, store.Repo{
				Name: hiddenRepo, IndexedCommitHash: hiddenRun.Commit,
			})
			st.runs[proofScope(hiddenRepo, domain)] = hiddenRun
			st.assertions[hiddenRepo] = []store.Assertion{hidden}
			st.resolutions[proofEvidenceScope(
				hiddenRepo,
				hiddenRun.ID,
				hidden.Supporting[0],
			)] = hiddenResolution
		}
		return st
	}
	read := func(st *proofAPIStore) (*api.FieldReferencePage, []byte) {
		t.Helper()
		service := api.NewFieldReferenceService(api.Options{
			Store: st, Evidence: st,
			Principal: func(context.Context) string {
				return "user:field-reader"
			},
			Visible: func(context.Context) func(store.Repo) bool {
				return func(repo store.Repo) bool {
					return repo.Name == visibleRepo
				}
			},
			AuthorizationProvider: "field-test-v1",
		})
		page, err := service.List(
			context.Background(),
			api.FieldReferenceQuery{
				Fields: []compat.FieldIdentity{{
					Lineage: lineage,
					Message: message,
					Number:  1,
				}},
			},
			1,
			"",
		)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		return page, encoded
	}

	withHidden := newStore(true)
	firstPage, hiddenBytes := read(withHidden)
	withoutHidden := newStore(false)
	_, baselineBytes := read(withoutHidden)
	if string(hiddenBytes) != string(baselineBytes) {
		t.Fatalf(
			"hidden repository changed field bytes:\nwith=%s\nwithout=%s",
			hiddenBytes,
			baselineBytes,
		)
	}
	if firstPage.TotalRows != 2 ||
		len(firstPage.Rows) != 1 ||
		firstPage.Pagination.Complete ||
		firstPage.Pagination.NextCursor == "" ||
		firstPage.Coverage.RepositoryCount != 1 {
		t.Fatalf("first field page = %+v", firstPage)
	}
	if len(withHidden.bundles) != 0 ||
		slices.ContainsFunc(withHidden.calls, func(call string) bool {
			return strings.HasPrefix(call, "put:")
		}) {
		t.Fatalf(
			"side-effect-free read minted a bundle: %+v calls=%v",
			withHidden.bundles,
			withHidden.calls,
		)
	}

	service := api.NewFieldReferenceService(api.Options{
		Store: withHidden, Evidence: withHidden,
		Principal: func(context.Context) string {
			return "user:field-reader"
		},
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repo store.Repo) bool {
				return repo.Name == visibleRepo
			}
		},
		AuthorizationProvider: "field-test-v1",
	})
	secondPage, err := service.List(
		context.Background(),
		firstPage.Query,
		1,
		firstPage.Pagination.NextCursor,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Rows) != 1 ||
		!secondPage.Pagination.Complete ||
		secondPage.Rows[0].Assertion.ID ==
			firstPage.Rows[0].Assertion.ID {
		t.Fatalf("second field page = %+v", secondPage)
	}
	if len(withHidden.bundles) != 0 {
		t.Fatalf("paging minted a proof bundle: %+v", withHidden.bundles)
	}
}

func TestProofFieldEndpointPersistsSharedReaderResult(t *testing.T) {
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
	page, err := service.List(
		context.Background(),
		api.FieldReferenceQuery{
			Fields: []compat.FieldIdentity{{
				Lineage: lineage, Message: message, Number: 1,
			}},
		},
		10,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.bundles) != 0 {
		t.Fatal("shared field reader persisted before proof request")
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
		envelope.Bundle.Assertions[0].ID !=
			page.Rows[0].Assertion.ID ||
		envelope.Bundle.Coverage.Digest != page.CoverageDigest {
		t.Fatalf(
			"proof/shared field mismatch: bundle=%+v page=%+v stored=%d",
			envelope,
			page,
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
