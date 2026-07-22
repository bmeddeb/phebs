package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

type proofAPIStore struct {
	store.Store
	store.EvidenceStore
	store.ProofBundleStore

	repos       []store.Repo
	runs        map[string]store.ExtractionRun
	attempts    map[string]store.ExtractionAttempt
	assertions  map[string][]store.Assertion
	resolutions map[string]store.EvidenceResolution
	bundles     map[string]store.ProofBundleRecord
	calls       []string
}

func proofScope(repo, domain string) string { return repo + "\x00" + domain }
func proofEvidenceScope(repo, runID, atomID string) string {
	return repo + "\x00" + runID + "\x00" + atomID
}

func (s *proofAPIStore) ListRepos(context.Context) ([]store.Repo, error) {
	return append([]store.Repo(nil), s.repos...), nil
}

func (s *proofAPIStore) GetRepo(_ context.Context, name string) (*store.Repo, error) {
	s.calls = append(s.calls, "repo:"+name)
	for _, repo := range s.repos {
		if repo.Name == name {
			copyOfRepo := repo
			return &copyOfRepo, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *proofAPIStore) LatestPublishedRun(_ context.Context, repo, domain string) (*store.ExtractionRun, error) {
	s.calls = append(s.calls, "run:"+proofScope(repo, domain))
	run, ok := s.runs[proofScope(repo, domain)]
	if !ok {
		return nil, store.ErrNotFound
	}
	copyOfRun := run
	return &copyOfRun, nil
}

func (s *proofAPIStore) LatestExtractionAttempt(_ context.Context, repo, domain string) (*store.ExtractionAttempt, error) {
	s.calls = append(s.calls, "attempt:"+proofScope(repo, domain))
	if attempt, ok := s.attempts[proofScope(repo, domain)]; ok {
		copyOfAttempt := attempt
		return &copyOfAttempt, nil
	}
	run, ok := s.runs[proofScope(repo, domain)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &store.ExtractionAttempt{
		RunID: run.ID, Repo: run.Repo, Commit: run.Commit, Domain: run.Domain,
		Extractor: run.Extractor, Status: "published",
	}, nil
}

func (s *proofAPIStore) ListAssertions(_ context.Context, query store.AssertionQuery) ([]store.Assertion, error) {
	s.calls = append(s.calls, "assertions:"+proofScope(query.Repo, query.RunID))
	var result []store.Assertion
	for _, assertion := range s.assertions[query.Repo] {
		if query.RunID != "" && assertion.RunID != query.RunID ||
			query.Predicate != "" && assertion.Predicate != query.Predicate ||
			query.Object != "" && assertion.Object != query.Object ||
			query.Lineage != "" && assertion.Lineage != query.Lineage {
			continue
		}
		result = append(result, assertion)
	}
	return result, nil
}

func (s *proofAPIStore) ResolveEvidence(_ context.Context, repo, runID, atomID string) (*store.EvidenceResolution, error) {
	s.calls = append(s.calls, "resolve:"+proofEvidenceScope(repo, runID, atomID))
	resolution, ok := s.resolutions[proofEvidenceScope(repo, runID, atomID)]
	if !ok {
		return nil, store.ErrNotFound
	}
	copyOfResolution := resolution
	copyOfResolution.Occurrences = append([]store.SnapshotEvidence(nil), resolution.Occurrences...)
	return &copyOfResolution, nil
}

func (s *proofAPIStore) PutProofBundle(_ context.Context, bundle store.ProofBundleRecord) error {
	s.calls = append(s.calls, "put:"+bundle.ID)
	if s.bundles == nil {
		s.bundles = make(map[string]store.ProofBundleRecord)
	}
	if existing, ok := s.bundles[bundle.ID]; ok && existing.Content != bundle.Content {
		return store.ErrConflict
	}
	bundle.Repositories = append([]string(nil), bundle.Repositories...)
	bundle.RunIDs = append([]string(nil), bundle.RunIDs...)
	s.bundles[bundle.ID] = bundle
	return nil
}

func (s *proofAPIStore) GetProofBundle(_ context.Context, id string) (*store.ProofBundleRecord, error) {
	s.calls = append(s.calls, "bundle:"+id)
	bundle, ok := s.bundles[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	bundle.Repositories = append([]string(nil), bundle.Repositories...)
	bundle.RunIDs = append([]string(nil), bundle.RunIDs...)
	return &bundle, nil
}

func proofRun(repo, domain, runID string) store.ExtractionRun {
	return store.ExtractionRun{
		ID: runID, Repo: repo, Commit: strings.Repeat("a", 40), Domain: domain,
		Extractor: domain + "@1", Status: "published",
		Coverage: store.CoverageManifest{
			Protocols: []string{"grpc"}, CorpusFileCount: 1, CandidateFileCount: 1,
			ReadFileCount: 1, ReadBytes: 100,
			SourceScopeDigest: "sha256:" + strings.Repeat("b", 64),
			AssertionCount:    1, AtomCount: 1,
		},
	}
}

func proofAssertion(repo, runID, id, predicate, object, lineage, secret string) (store.Assertion, store.EvidenceResolution) {
	atomID := "ea_" + id
	assertion := store.Assertion{
		ID: id, Predicate: predicate, Subject: "consumer/" + id + ".go:10-20",
		Object: object, Lineage: lineage, Tier: store.TierExact, CodeRole: "production",
		Repo: repo, RunID: runID, Supporting: []string{atomID}, Detail: secret,
	}
	observed := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	resolution := store.EvidenceResolution{
		Atom: store.EvidenceAtom{ID: atomID, SchemaVersion: "t14-test", BlobDigest: "sha256:" + strings.Repeat("c", 64)},
		Occurrences: []store.SnapshotEvidence{{
			ID: "occ_" + id, AtomID: atomID, Repo: repo, Commit: strings.Repeat("a", 40),
			Path: "consumer/" + id + ".go", StartLine: 7, EndLine: 7,
			VisibilityScope: "repo:" + repo, RunID: runID, ObservedAt: observed,
		}},
	}
	return assertion, resolution
}

func proofHandler(st *proofAPIStore, principal string, visible *map[string]bool) http.Handler {
	return api.New(api.Options{
		Version: "test", Store: st, Evidence: st, ProofBundles: st,
		Visible: func(context.Context) func(store.Repo) bool {
			if visible == nil {
				return nil
			}
			return func(repo store.Repo) bool { return (*visible)[repo.Name] }
		},
		Principal:             func(context.Context) string { return principal },
		AuthorizationProvider: "test-permissions-v1",
	})
}

func getProof(t *testing.T, handler http.Handler, target string) (int, string, api.ProofBundleEnvelope) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	var envelope api.ProofBundleEnvelope
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
	}
	return recorder.Code, recorder.Body.String(), envelope
}

func TestProofBundleAdminMemberIsolationAndReadReauthorization(t *testing.T) {
	const (
		visibleRepo = "github.com/allowed/service"
		hiddenRepo  = "github.com/secret/hidden-service"
		operation   = "/shop.Cart/Get"
		domain      = "grpc-consumer"
	)
	visibleRun, hiddenRun := proofRun(visibleRepo, domain, "run-visible"), proofRun(hiddenRepo, domain, "run-hidden")
	visibleAssertion, visibleResolution := proofAssertion(visibleRepo, visibleRun.ID, "visible", "CALLS_OPERATION", operation, "", "visible detail")
	hiddenAssertion, hiddenResolution := proofAssertion(hiddenRepo, hiddenRun.ID, "hidden", "CALLS_OPERATION", operation, "", "top-secret-detail")
	st := &proofAPIStore{
		repos: []store.Repo{
			{Name: hiddenRepo, IndexedCommitHash: hiddenRun.Commit},
			{Name: visibleRepo, IndexedCommitHash: visibleRun.Commit},
		},
		runs: map[string]store.ExtractionRun{
			proofScope(visibleRepo, domain): visibleRun, proofScope(hiddenRepo, domain): hiddenRun,
		},
		assertions: map[string][]store.Assertion{
			visibleRepo: {visibleAssertion}, hiddenRepo: {hiddenAssertion},
		},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(visibleRepo, visibleRun.ID, visibleAssertion.Supporting[0]): visibleResolution,
			proofEvidenceScope(hiddenRepo, hiddenRun.ID, hiddenAssertion.Supporting[0]):    hiddenResolution,
		},
	}
	memberVisible := map[string]bool{visibleRepo: true}
	admin := proofHandler(st, "user:admin", nil)
	member := proofHandler(st, "user:member", &memberVisible)

	adminCode, _, adminBundle := getProof(t, admin, "/api/find_operation_consumers?operation=%2Fshop.Cart%2FGet")
	if adminCode != http.StatusOK {
		t.Fatalf("admin query = %d", adminCode)
	}
	st.calls = nil
	memberCode, memberBody, memberBundle := getProof(t, member, "/api/find_operation_consumers?operation=%2Fshop.Cart%2FGet")
	if memberCode != http.StatusOK {
		t.Fatalf("member query = %d %s", memberCode, memberBody)
	}
	if adminBundle.ID == memberBundle.ID {
		t.Fatal("admin and member received the same immutable bundle id")
	}
	if strings.Contains(memberBody, "hidden-service") || strings.Contains(memberBody, "top-secret") ||
		len(memberBundle.Bundle.Assertions) != 1 || memberBundle.Bundle.Assertions[0].Repo != visibleRepo ||
		memberBundle.Bundle.Coverage.RepositoryCount != 1 {
		t.Fatalf("member bundle leaked hidden scope: %s", memberBody)
	}
	if adminBundle.Bundle.Coverage.RepositoryCount != 2 || len(adminBundle.Bundle.Assertions) != 2 {
		t.Fatalf("admin bundle scope = %+v", adminBundle.Bundle)
	}
	if memberBundle.Bundle.VisibilityContext.Principal != "user:member" ||
		adminBundle.Bundle.VisibilityContext.Principal != "user:admin" {
		t.Fatalf("principals = %+v / %+v", memberBundle.Bundle.VisibilityContext, adminBundle.Bundle.VisibilityContext)
	}
	for _, call := range st.calls {
		if strings.Contains(call, hiddenRepo) || strings.Contains(call, "run-hidden") {
			t.Fatalf("member query touched hidden evidence: %v", st.calls)
		}
	}
	if len(memberBundle.Bundle.Evidence) != 1 || len(memberBundle.Bundle.ExtractorVersions) != 1 ||
		memberBundle.Bundle.ExtractorVersions[0].RunID != visibleRun.ID {
		t.Fatalf("member bundle is not self-contained: %+v", memberBundle.Bundle)
	}
	if repeatCode, repeatBody, repeat := getProof(t, member, "/api/find_operation_consumers?operation=%2Fshop.Cart%2FGet"); repeatCode != http.StatusOK || repeat.ID != memberBundle.ID || repeatBody != memberBody {
		t.Fatalf("identical query was not byte-identical: %d %s", repeatCode, repeatBody)
	}

	if code, _, got := getProof(t, member, "/api/proof_bundles/"+memberBundle.ID); code != http.StatusOK || got.ID != memberBundle.ID {
		t.Fatalf("member bundle read = %d %+v", code, got)
	}
	if code, body, _ := getProof(t, member, "/api/proof_bundles/"+adminBundle.ID); code != http.StatusNotFound || strings.Contains(body, "hidden") {
		t.Fatalf("member read of admin bundle = %d %s", code, body)
	}
	memberVisible = map[string]bool{}
	if code, body, _ := getProof(t, member, "/api/proof_bundles/"+memberBundle.ID); code != http.StatusNotFound || strings.Contains(body, visibleRepo) {
		t.Fatalf("revoked bundle read = %d %s", code, body)
	}
}

func TestProofFieldLineageCoverageAndValidation(t *testing.T) {
	const repo = "github.com/allowed/field"
	lineage := "contract_scip_package_v1_" + strings.Repeat("d", 64)
	domain, message, fieldNumber := "scip-proto-field", "shop.Cart", 1
	run := proofRun(repo, domain, "run-field")
	run.Coverage.AssertionCount = 2
	run.Coverage.AtomCount = 2
	matching, matchingResolution := proofAssertion(repo, run.ID, "matching", "REFERENCES_PROTO_FIELD", message+"#1", lineage, "match")
	other, otherResolution := proofAssertion(repo, run.ID, "other", "REFERENCES_PROTO_FIELD", message+"#1", "contract_other", "other")
	st := &proofAPIStore{
		repos:      []store.Repo{{Name: repo, IndexedCommitHash: run.Commit}},
		runs:       map[string]store.ExtractionRun{proofScope(repo, domain): run},
		assertions: map[string][]store.Assertion{repo: {matching, other}},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(repo, run.ID, matching.Supporting[0]): matchingResolution,
			proofEvidenceScope(repo, run.ID, other.Supporting[0]):    otherResolution,
		},
	}
	handler := proofHandler(st, "user:member", nil)
	target := "/api/find_proto_field_references?lineage=" + lineage + "&message=" + message + "&field_number=" + strconv.Itoa(fieldNumber)
	code, body, envelope := getProof(t, handler, target)
	if code != http.StatusOK || len(envelope.Bundle.Assertions) != 1 || envelope.Bundle.Assertions[0].ID != "matching" || strings.Contains(body, `"other"`) {
		t.Fatalf("field query = %d %s", code, body)
	}
	if envelope.Bundle.Query.FieldNumber != 1 || envelope.Bundle.Query.Lineage != lineage || len(envelope.Bundle.Evidence) != 1 {
		t.Fatalf("field bundle = %+v", envelope.Bundle)
	}

	code, _, coverage := getProof(t, handler, "/api/get_extraction_coverage?domains=scip-proto-field")
	if code != http.StatusOK || len(coverage.Bundle.Assertions) != 0 || coverage.Bundle.Coverage.RepositoryCount != 1 {
		t.Fatalf("coverage query = %d %+v", code, coverage)
	}
	if coverage.ID == envelope.ID {
		t.Fatal("different questions produced the same bundle id")
	}

	invalidTargets := []string{
		"/api/find_operation_consumers?operation=shop.Cart%2FGet",
		"/api/find_proto_field_references?lineage=" + lineage + "&message=" + message + "&field_number=19000",
		"/api/get_extraction_coverage?domains=scip-proto-field,scip-proto-field",
	}
	for _, invalid := range invalidTargets {
		if code, _, _ := getProof(t, handler, invalid); code != http.StatusBadRequest && code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid query %q = %d", invalid, code)
		}
	}
}

func TestProofAPIDarkWithoutBundleStore(t *testing.T) {
	st := &proofAPIStore{}
	handler := api.New(api.Options{Version: "test", Store: st, Evidence: st})
	paths := []string{
		"/api/find_operation_consumers?operation=%2Fshop.Cart%2FGet",
		"/api/find_proto_field_references?lineage=x&message=shop.Cart&field_number=1",
		"/api/get_extraction_coverage",
		"/api/proof_bundles/pb_" + strings.Repeat("0", 64),
	}
	sort.Strings(paths)
	for _, target := range paths {
		code, _, _ := getProof(t, handler, target)
		if code != http.StatusNotFound {
			t.Fatalf("dark proof route %q = %d", target, code)
		}
	}
}
