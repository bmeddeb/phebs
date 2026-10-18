package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

const fieldAdmissionVersion = "1.4.0-released.1"

func fieldAdmissionFixture() (*proofAPIStore, api.Options, string) {
	lineage := "contract_scip_package_v1_" + strings.Repeat("b", 64)
	run := proofRun(atlasRepo, "scip-proto-field", "run-stable-field")
	run.Extractor = fieldAdmissionVersion
	assertion, resolution := proofAssertion(atlasRepo, run.ID, "stable-field", "REFERENCES_PROTO_FIELD", "shop.Cart#1", lineage, "field")
	resolution.Atom.ExtractorVersion = fieldAdmissionVersion
	st := &proofAPIStore{repos: []store.Repo{{Name: atlasRepo, IndexedCommitHash: run.Commit}}, runs: map[string]store.ExtractionRun{proofScope(atlasRepo, "scip-proto-field"): run}, assertions: map[string][]store.Assertion{atlasRepo: {assertion}}, resolutions: map[string]store.EvidenceResolution{proofEvidenceScope(atlasRepo, run.ID, assertion.Supporting[0]): resolution}, bundles: map[string]store.ProofBundleRecord{}}
	opts := api.Options{Store: st, Evidence: st, ProofBundles: st, ProtobufFieldReferencesReleased: true, EvidenceExtractorVersions: map[string]string{"scip-proto-field": fieldAdmissionVersion}, CatalogReleasedDomains: []string{"scip-proto-field"}, Principal: func(context.Context) string { return "user:t51" }}
	return st, opts, lineage
}

func TestReleasedFieldProofBoundaries(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(*proofAPIStore, *api.Options)
		wantError bool
		noRows    bool
	}{
		{name: "admitted legacy source"},
		{name: "admitted source beside explicit provisional surface", change: func(_ *proofAPIStore, opts *api.Options) {
			opts.ProtobufFieldReferencesReleased = false
		}},
		{name: "retained provisional version", wantError: true, noRows: true, change: func(st *proofAPIStore, _ *api.Options) {
			r := st.runs[proofScope(atlasRepo, "scip-proto-field")]
			r.Extractor = "1.4.0"
			st.runs[proofScope(atlasRepo, "scip-proto-field")] = r
		}},
		{name: "retained version beside explicit provisional surface", wantError: true, noRows: true, change: func(st *proofAPIStore, opts *api.Options) {
			opts.ProtobufFieldReferencesReleased = false
			r := st.runs[proofScope(atlasRepo, "scip-proto-field")]
			r.Extractor = "1.4.0"
			st.runs[proofScope(atlasRepo, "scip-proto-field")] = r
		}},
		{name: "foreign citation version", wantError: true, change: func(st *proofAPIStore, _ *api.Options) {
			for k, v := range st.resolutions {
				v.Atom.ExtractorVersion = "1.4.0"
				st.resolutions[k] = v
			}
		}},
		{name: "permission denied", noRows: true, change: func(_ *proofAPIStore, opts *api.Options) {
			opts.Visible = func(context.Context) func(store.Repo) bool { return func(store.Repo) bool { return false } }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			st, opts, lineage := fieldAdmissionFixture()
			if test.change != nil {
				test.change(st, &opts)
			}
			proof := api.NewProofService(opts)
			result, err := proof.FindProtoFieldReferences(t.Context(), lineage, "shop.Cart", 1)
			if (err != nil) != test.wantError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if test.noRows {
				for _, call := range st.calls {
					if strings.HasPrefix(call, "assertions:") || strings.HasPrefix(call, "resolve:") {
						t.Fatalf("unadmitted source read: %s", call)
					}
				}
			}
			if test.wantError && len(st.bundles) != 0 {
				t.Fatal("failed proof persisted")
			}
			if !test.wantError && !test.noRows && (len(result.Bundle.Assertions) != 1 || len(result.Bundle.Evidence) != 1) {
				t.Fatal("field evidence lost")
			}
		})
	}
	st, opts, lineage := fieldAdmissionFixture()
	proof := api.NewProofService(opts)
	for _, call := range []func() error{
		func() error { _, e := proof.FindOperationConsumers(t.Context(), "/shop.Cart/Get"); return e },
		func() error { _, e := proof.GetExtractionCoverage(t.Context(), nil); return e },
		func() error { _, e := proof.BuildFieldImpactReport(t.Context(), lineage, "shop.Cart", 1); return e },
		func() error {
			_, e := proof.FindProtoFieldReferences(t.Context(), "provisional_repo_path_v1_"+strings.Repeat("a", 64), "shop.Cart", 1)
			return e
		},
		func() error { _, e := proof.FindFieldReferences(t.Context(), lineage, "shop.Cart", 0); return e },
	} {
		if err := call(); err == nil {
			t.Fatal("unrelated query or lineage admitted")
		}
		if len(st.calls) != 0 {
			t.Fatalf("storage read before refusal: %v", st.calls)
		}
	}
	if _, err := proof.FindProtoFieldReferencesMCP(t.Context(), lineage, "shop.Cart", 1); err != nil {
		t.Fatal(err)
	}
}

func TestReleasedFieldMixedProvisionalThrift(t *testing.T) {
	for _, number := range []int{0, 1} {
		t.Run(strconv.Itoa(number), func(t *testing.T) {
			st, opts, _ := fieldAdmissionFixture()
			opts.ProtobufFieldReferencesReleased = false
			opts.CatalogReleasedDomains = nil
			// This old protobuf run must never be queried for a foreign-family
			// neutral request, even beside a selected stable field recipe.
			protoRun := st.runs[proofScope(atlasRepo, "scip-proto-field")]
			protoRun.Extractor = "1.4.0"
			st.runs[proofScope(atlasRepo, "scip-proto-field")] = protoRun
			const lineage = "retained_thrift_package_lineage"
			run := proofRun(atlasRepo, "scip-thrift-field", "run-provisional-thrift")
			assertion, resolution := proofAssertion(atlasRepo, run.ID, "thrift-field", "REFERENCES_THRIFT_FIELD", "shop.Cart#"+strconv.Itoa(number), lineage, "thrift-field")
			st.runs[proofScope(atlasRepo, "scip-thrift-field")] = run
			st.assertions[atlasRepo] = append(st.assertions[atlasRepo], assertion)
			st.resolutions[proofEvidenceScope(atlasRepo, run.ID, assertion.Supporting[0])] = resolution
			proof := api.NewProofService(opts)
			result, err := proof.FindFieldReferences(t.Context(), lineage, "shop.Cart", number)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(result.Bundle.Query.Domains, []string{"scip-thrift-field"}) || len(result.Bundle.Assertions) != 1 ||
				result.Bundle.Assertions[0].ID != assertion.ID || len(result.Bundle.Evidence) != 1 {
				t.Fatalf("independent Thrift proof changed: %+v", result.Bundle)
			}
			st.calls = nil
			if _, err := proof.FindProtoFieldReferences(t.Context(), lineage, "shop.Cart", 1); err == nil || len(st.calls) != 0 {
				t.Fatalf("foreign lineage crossed into stable protobuf: err=%v calls=%v", err, st.calls)
			}
		})
	}
}

func TestReleasedFieldRawAndRetainedBundleVersions(t *testing.T) {
	for _, version := range []string{fieldAdmissionVersion, "1.4.0"} {
		t.Run(version, func(t *testing.T) {
			st, opts, _ := fieldAdmissionFixture()
			run := st.runs[proofScope(atlasRepo, "scip-proto-field")]
			run.Extractor = version
			st.runs[proofScope(atlasRepo, "scip-proto-field")] = run
			rec := httptest.NewRecorder()
			api.New(opts).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=scip-proto-field", nil))
			want := http.StatusOK
			if version != fieldAdmissionVersion {
				want = http.StatusConflict
			}
			if rec.Code != want {
				t.Fatalf("raw=%d %s", rec.Code, rec.Body)
			}
			if version != fieldAdmissionVersion {
				for _, call := range st.calls {
					if strings.HasPrefix(call, "assertions:") {
						t.Fatal("unadmitted raw assertion read")
					}
				}
			}
		})
	}
	st, opts, lineage := fieldAdmissionFixture()
	legacy := opts
	legacy.ProtobufFieldReferencesReleased = false
	legacy.EvidenceExtractorVersions = nil
	run := st.runs[proofScope(atlasRepo, "scip-proto-field")]
	run.Extractor = "1.4.0"
	st.runs[proofScope(atlasRepo, "scip-proto-field")] = run
	bundle, err := api.NewProofService(legacy).FindProtoFieldReferences(t.Context(), lineage, "shop.Cart", 1)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	api.New(opts).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/proof_bundles/"+bundle.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("retained provisional proof admitted: %d %s", rec.Code, rec.Body)
	}
}

type fieldNativeStore struct {
	*partitionedKafkaStore
	version string
	seen    int
}

func (s *fieldNativeStore) ListPartitionedAssertions(ctx context.Context, q store.AssertionQuery, a store.PartitionedAssertionAuthority) ([]store.Assertion, error) {
	if a.ExpectedExtractor != fieldAdmissionVersion {
		return nil, store.ErrConflict
	}
	s.seen++
	if s.version != a.ExpectedExtractor {
		return nil, store.ErrNotFound
	}
	a.ExpectedExtractor = ""
	return s.partitionedKafkaStore.ListPartitionedAssertions(ctx, q, a)
}
func (s *fieldNativeStore) ResolvePartitionedEvidence(ctx context.Context, a store.PartitionedAssertionAuthority, atom string) (*store.EvidenceResolution, error) {
	if a.ExpectedExtractor != s.version {
		return nil, store.ErrNotFound
	}
	a.ExpectedExtractor = ""
	return s.partitionedKafkaStore.ResolvePartitionedEvidence(ctx, a, atom)
}

func TestReleasedFieldNativeVersionFence(t *testing.T) {
	for _, version := range []string{fieldAdmissionVersion, "1.4.0", "moves-after-persistence"} {
		t.Run(version, func(t *testing.T) {
			base, opts, lineage := fieldAdmissionFixture()
			run := base.runs[proofScope(atlasRepo, "scip-proto-field")]
			base.runs = map[string]store.ExtractionRun{}
			digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
			native := &fieldNativeStore{partitionedKafkaStore: &partitionedKafkaStore{proofAPIStore: base, current: true, root: store.PartitionedExtractionDomainReference{Schema: store.PartitionedExtractionDomainSchema, Repository: atlasRepo, Domain: "scip-proto-field", RunID: run.ID, PlanDigest: digest("1"), RootDigest: digest("2"), CandidateDigest: digest("3"), SourceDigest: digest("4"), ObservationDigest: digest("5")}, manifest: store.CandidateManifestPublication{Repository: atlasRepo, HeadCommit: run.Commit, ManifestDigest: digest("3"), PolicyDigest: digest("6")}}, version: version}
			opts.Store, opts.Evidence, opts.ProofBundles = native, native, native
			if version == "moves-after-persistence" {
				native.version = fieldAdmissionVersion
				base.onPut = func(store.ProofBundleRecord) { native.root.RootDigest = digest("9") }
			}
			result, err := api.NewProofService(opts).FindProtoFieldReferences(t.Context(), lineage, "shop.Cart", 1)
			if version == "moves-after-persistence" {
				if err == nil || result != nil {
					t.Fatal("moved native authority served after persistence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if version != fieldAdmissionVersion {
				want = 0
			}
			if len(result.Bundle.Assertions) != want || native.seen == 0 {
				t.Fatal("native fence bypassed")
			}
			if want == 0 {
				coverage := result.Bundle.Coverage
				if len(coverage.Repositories) != 1 || len(coverage.Repositories[0].Runs) != 1 {
					t.Fatalf("unavailable coverage lost: %+v", coverage)
				}
				run := coverage.Repositories[0].Runs[0]
				if run.Domain != "scip-proto-field" || run.Status != "unpublished" || run.RunID != "" || run.Fresh ||
					len(result.Bundle.ExactAuthorities) != 0 || len(result.Bundle.ExtractorVersions) != 0 || len(result.Bundle.Evidence) != 0 {
					t.Fatalf("unadmitted native version became exact-empty: run=%+v bundle=%+v", run, result.Bundle)
				}
			}
			if want == 1 && (len(result.Bundle.ExtractorVersions) != 1 || result.Bundle.ExtractorVersions[0].Extractor != fieldAdmissionVersion) {
				t.Fatal("native version witness absent")
			}
		})
	}
}
