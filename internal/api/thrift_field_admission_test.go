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
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftfield"
	"github.com/bmeddeb/phebs/internal/store"
)

func thriftFieldAdmissionFixture(version string) (*proofAPIStore, api.Options, string) {
	st, opts, lineage := fieldAdmissionFixture()
	run := proofRun(atlasRepo, "scip-thrift-field", "run-stable-thrift-fields")
	run.Extractor = version
	assertion, resolution := proofAssertion(atlasRepo, run.ID, "stable-thrift-field", "REFERENCES_THRIFT_FIELD", "shop.Cart#0", lineage, "thrift-field")
	assertion.Detail = `{"schema":"thrift-field-reference-detail-v1","name":"success","generator":"thriftrw","source_binding":"module_digest","classification":"read"}`
	if version == thriftfield.CommittedApacheVersion {
		assertion.Tier = "derived"
		assertion.Detail = `{"schema":"thrift-field-reference-detail-v1","name":"success","generator":"apache","source_binding":"none","classification":"read"}`
	}
	resolution.Atom.ExtractorVersion = version
	st.runs = map[string]store.ExtractionRun{proofScope(atlasRepo, "scip-thrift-field"): run}
	st.assertions = map[string][]store.Assertion{atlasRepo: {assertion}}
	st.resolutions = map[string]store.EvidenceResolution{proofEvidenceScope(atlasRepo, run.ID, assertion.Supporting[0]): resolution}
	opts.ProtobufFieldReferencesReleased = false
	opts.ThriftFieldReferencesReleased = true
	opts.EvidenceExtractorVersions = map[string]string{"scip-thrift-field": version}
	opts.CatalogReleasedDomains = []string{"scip-thrift-field"}
	return st, opts, lineage
}

func TestReleasedThriftFieldProofVersions(t *testing.T) {
	for _, selected := range []string{thriftfield.CommittedApacheVersion, thriftfield.CommittedThriftRWVersion, thriftfield.CommittedApacheThriftRWVersion} {
		for _, test := range []struct{ name, actual, citation string }{
			{"current", selected, selected},
			{"provisional", "1.4.0", "1.4.0"},
			{"other selected set", "other-recipe", "other-recipe"},
			{"foreign citation", selected, "1.4.0"},
		} {
			t.Run(selected+"/"+test.name, func(t *testing.T) {
				st, opts, lineage := thriftFieldAdmissionFixture(selected)
				run := st.runs[proofScope(atlasRepo, "scip-thrift-field")]
				run.Extractor = test.actual
				st.runs[proofScope(atlasRepo, "scip-thrift-field")] = run
				for k, v := range st.resolutions {
					v.Atom.ExtractorVersion = test.citation
					st.resolutions[k] = v
				}
				result, err := api.NewProofService(opts).FindFieldReferences(t.Context(), lineage, "shop.Cart", 0)
				if test.name == "current" {
					if err != nil || result == nil || len(result.Bundle.Assertions) != 1 || len(result.Bundle.Evidence) != 1 || len(result.Bundle.ExtractorVersions) != 1 || result.Bundle.ExtractorVersions[0].Extractor != selected || !slices.Equal(result.Bundle.Query.Domains, []string{"scip-thrift-field"}) {
						t.Fatalf("result=%+v err=%v", result, err)
					}
					if _, err := api.NewProofService(opts).FindFieldReferencesMCP(t.Context(), lineage, "shop.Cart", 0); err != nil {
						t.Fatal(err)
					}
					rec := httptest.NewRecorder()
					api.New(opts).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/find_field_references?lineage="+lineage+"&message=shop.Cart&field_number=0", nil))
					if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"field_number":0`) || !strings.Contains(rec.Body.String(), "REFERENCES_THRIFT_FIELD") {
						t.Fatalf("field0 HTTP=%d %s", rec.Code, rec.Body)
					}
				} else {
					if err == nil || len(st.bundles) != 0 {
						t.Fatalf("unadmitted proof=%+v err=%v", result, err)
					}
					if test.actual != selected {
						for _, call := range st.calls {
							if strings.HasPrefix(call, "assertions:") || strings.HasPrefix(call, "resolve:") {
								t.Fatalf("old recipe rows read: %s", call)
							}
						}
					}
				}
				st.calls = nil
				rec := httptest.NewRecorder()
				api.New(opts).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=scip-thrift-field", nil))
				want := http.StatusOK
				if test.actual != selected {
					want = http.StatusConflict
				}
				if rec.Code != want {
					t.Fatalf("raw=%d %s", rec.Code, rec.Body)
				}
			})
		}
	}
}

func TestReleasedThriftFieldRefusalsBeforeReads(t *testing.T) {
	st, opts, lineage := thriftFieldAdmissionFixture(thriftfield.CommittedThriftRWVersion)
	proof := api.NewProofService(opts)
	for _, call := range []func() error{
		func() error {
			_, err := proof.FindProtoFieldReferences(t.Context(), lineage, "shop.Cart", 1)
			return err
		},
		func() error {
			_, err := proof.FindFieldReferences(t.Context(), lineage, "shop.Cart", 32768)
			return err
		},
		func() error {
			_, err := proof.FindFieldReferences(t.Context(), "provisional_repo_path_v1_"+strings.Repeat("a", 64), "shop.Cart", 0)
			return err
		},
		func() error { _, err := proof.GetExtractionCoverage(t.Context(), nil); return err },
		func() error { _, err := proof.BuildFieldImpactReport(t.Context(), lineage, "shop.Cart", 0); return err },
	} {
		if err := call(); err == nil || len(st.calls) != 0 {
			t.Fatalf("err=%v calls=%v", err, st.calls)
		}
	}
	handler := api.New(opts)
	for _, query := range []string{
		"/api/find_field_references?lineage=" + lineage + "&message=shop.Cart",
		"/api/find_field_references?lineage=" + lineage + "&message=shop.Cart&field_number=32768",
		"/api/find_proto_field_references?lineage=" + lineage + "&message=shop.Cart&field_number=1",
		"/api/evidence?domain=scip-proto-field",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, query, nil))
		if rec.Code < 400 || len(st.calls) != 0 {
			t.Fatalf("query=%s status=%d calls=%v", query, rec.Code, st.calls)
		}
	}
	for _, number := range []int{0, 19000, 32767} {
		result, err := proof.FindFieldReferences(t.Context(), lineage, "shop.Cart", number)
		if err != nil || !slices.Equal(result.Bundle.Query.Domains, []string{"scip-thrift-field"}) {
			t.Fatalf("number=%d result=%+v err=%v", number, result, err)
		}
	}
}

func TestReleasedThriftFieldMixedProvisionalProto(t *testing.T) {
	st, opts, _ := thriftFieldAdmissionFixture(thriftfield.CommittedApacheVersion)
	opts.ThriftFieldReferencesReleased = false
	opts.CatalogReleasedDomains = nil
	const lineage = "retained_proto_lineage"
	run := proofRun(atlasRepo, "scip-proto-field", "provisional-proto")
	assertion, resolution := proofAssertion(atlasRepo, run.ID, "provisional-field", "REFERENCES_PROTO_FIELD", "shop.Cart#1", lineage, "proto-field")
	st.runs[proofScope(atlasRepo, "scip-proto-field")] = run
	st.assertions[atlasRepo] = append(st.assertions[atlasRepo], assertion)
	st.resolutions[proofEvidenceScope(atlasRepo, run.ID, assertion.Supporting[0])] = resolution
	result, err := api.NewProofService(opts).FindFieldReferences(t.Context(), lineage, "shop.Cart", 1)
	if err != nil || len(result.Bundle.Assertions) != 1 || !slices.Equal(result.Bundle.Query.Domains, []string{"scip-proto-field"}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, call := range st.calls {
		if call == "run:"+proofScope(atlasRepo, "scip-thrift-field") {
			t.Fatal("foreign family queried stable Thrift")
		}
	}
	st.calls = nil
	if _, err := api.NewProofService(opts).FindFieldReferences(t.Context(), lineage, "shop.Cart", 0); err == nil || len(st.calls) != 0 {
		t.Fatalf("foreign field0 err=%v calls=%v", err, st.calls)
	}
}

func TestReleasedThriftFieldZeroDoesNotQueryIndependentProtobuf(t *testing.T) {
	st, opts, lineage := thriftFieldAdmissionFixture(thriftfield.CommittedThriftRWVersion)
	opts.ProtobufFieldReferencesReleased = true
	opts.EvidenceExtractorVersions["scip-proto-field"] = fieldAdmissionVersion
	proto := proofRun(atlasRepo, "scip-proto-field", "old-proto")
	proto.Extractor = "1.4.0"
	st.runs[proofScope(atlasRepo, "scip-proto-field")] = proto
	for _, number := range []int{0, 19000} {
		result, err := api.NewProofService(opts).FindFieldReferences(t.Context(), lineage, "shop.Cart", number)
		if err != nil || !slices.Equal(result.Bundle.Query.Domains, []string{"scip-thrift-field"}) {
			t.Fatalf("number=%d result=%+v err=%v", number, result, err)
		}
	}
	for _, call := range st.calls {
		if call == "run:"+proofScope(atlasRepo, "scip-proto-field") {
			t.Fatal("Thrift-only number queried protobuf")
		}
	}
}

func TestReleasedThriftFieldWithdrawalPreservesCustodyAndSurvivor(t *testing.T) {
	st, opts, lineage := thriftFieldAdmissionFixture(thriftfield.CommittedApacheThriftRWVersion)
	old, err := api.NewProofService(opts).FindFieldReferences(t.Context(), lineage, "shop.Cart", 0)
	if err != nil {
		t.Fatal(err)
	}
	content := st.bundles[old.ID].Content
	opts.EvidenceExtractorVersions["scip-thrift-field"] = thriftfield.CommittedThriftRWVersion
	handler := api.New(opts)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/proof_bundles/"+old.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("former selected-set bundle=%d", rec.Code)
	}
	if _, err := api.NewProofService(opts).FindFieldReferences(t.Context(), lineage, "shop.Cart", 0); err == nil {
		t.Fatal("warm both-generation reused for survivor")
	}
	run := proofRun(atlasRepo, "scip-thrift-field", "surviving-rw-generation")
	run.Extractor = thriftfield.CommittedThriftRWVersion
	assertion, resolution := proofAssertion(atlasRepo, run.ID, "survivor", "REFERENCES_THRIFT_FIELD", "shop.Cart#0", lineage, "thrift-field")
	resolution.Atom.ExtractorVersion = thriftfield.CommittedThriftRWVersion
	st.runs[proofScope(atlasRepo, "scip-thrift-field")] = run
	st.assertions[atlasRepo] = []store.Assertion{assertion}
	st.resolutions[proofEvidenceScope(atlasRepo, run.ID, assertion.Supporting[0])] = resolution
	current, err := api.NewProofService(opts).FindFieldReferences(t.Context(), lineage, "shop.Cart", 0)
	if err != nil || len(current.Bundle.Assertions) != 1 || current.Bundle.ExtractorVersions[0].Extractor != thriftfield.CommittedThriftRWVersion {
		t.Fatalf("survivor=%+v err=%v", current, err)
	}
	if st.bundles[old.ID].Content != content {
		t.Fatal("historical custody rewritten")
	}
}

type thriftFieldNativeStore struct {
	*partitionedKafkaStore
	expected, actual string
	checks           int
}

func (s *thriftFieldNativeStore) ListPartitionedAssertions(ctx context.Context, q store.AssertionQuery, a store.PartitionedAssertionAuthority) ([]store.Assertion, error) {
	if a.ExpectedExtractor != s.expected {
		return nil, store.ErrConflict
	}
	s.checks++
	if s.actual != a.ExpectedExtractor {
		return nil, store.ErrNotFound
	}
	a.ExpectedExtractor = ""
	return s.partitionedKafkaStore.ListPartitionedAssertions(ctx, q, a)
}

func (s *thriftFieldNativeStore) ResolvePartitionedEvidence(ctx context.Context, a store.PartitionedAssertionAuthority, atom string) (*store.EvidenceResolution, error) {
	if a.ExpectedExtractor != s.expected || a.ExpectedExtractor != s.actual {
		return nil, store.ErrNotFound
	}
	a.ExpectedExtractor = ""
	return s.partitionedKafkaStore.ResolvePartitionedEvidence(ctx, a, atom)
}

func TestReleasedThriftFieldNativeSelectedSetFence(t *testing.T) {
	for _, actual := range []string{thriftfield.CommittedApacheThriftRWVersion, thriftfield.CommittedApacheVersion, "1.4.0", ""} {
		t.Run(actual, func(t *testing.T) {
			base, opts, lineage := thriftFieldAdmissionFixture(thriftfield.CommittedApacheThriftRWVersion)
			run := base.runs[proofScope(atlasRepo, "scip-thrift-field")]
			base.runs = map[string]store.ExtractionRun{}
			digest := func(n int) string { return "sha256:" + strings.Repeat(strconv.Itoa(n), 64) }
			native := &thriftFieldNativeStore{partitionedKafkaStore: &partitionedKafkaStore{proofAPIStore: base, current: true, root: store.PartitionedExtractionDomainReference{Schema: store.PartitionedExtractionDomainSchema, Repository: atlasRepo, Domain: "scip-thrift-field", RunID: run.ID, PlanDigest: digest(1), RootDigest: digest(2), CandidateDigest: digest(3), SourceDigest: digest(4), ObservationDigest: digest(5)}, manifest: store.CandidateManifestPublication{Repository: atlasRepo, HeadCommit: run.Commit, ManifestDigest: digest(3), PolicyDigest: digest(6)}}, expected: thriftfield.CommittedApacheThriftRWVersion, actual: actual}
			opts.Store, opts.Evidence, opts.ProofBundles = native, native, native
			result, err := api.NewProofService(opts).FindFieldReferences(t.Context(), lineage, "shop.Cart", 0)
			if err != nil || native.checks == 0 {
				t.Fatalf("result=%+v checks=%d err=%v", result, native.checks, err)
			}
			if actual == native.expected {
				if len(result.Bundle.Assertions) != 1 || len(result.Bundle.ExtractorVersions) != 1 || result.Bundle.ExtractorVersions[0].Extractor != native.expected {
					t.Fatal("native version witness lost")
				}
			} else {
				if len(result.Bundle.Assertions) != 0 || len(result.Bundle.ExactAuthorities) != 0 || len(result.Bundle.ExtractorVersions) != 0 || len(result.Bundle.Coverage.Repositories) != 1 {
					t.Fatalf("wrong version became exact: %+v", result.Bundle)
				}
				coverage := result.Bundle.Coverage.Repositories[0].Runs
				if len(coverage) != 1 || coverage[0].Status != "unpublished" || coverage[0].RunID != "" || coverage[0].Fresh {
					t.Fatalf("unavailable coverage lost: %+v", coverage)
				}
			}
		})
	}
}
