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
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/store"
)

type proofAPIStore struct {
	store.Store
	store.EvidenceStore
	store.ProofBundleStore

	repos        []store.Repo
	runs         map[string]store.ExtractionRun
	attempts     map[string]store.ExtractionAttempt
	assertions   map[string][]store.Assertion
	assertionErr error
	resolutions  map[string]store.EvidenceResolution
	bundles      map[string]store.ProofBundleRecord
	calls        []string
	onAssertions func(store.AssertionQuery)
}

func proofScope(repo, domain string) string { return repo + "\x00" + domain }
func proofEvidenceScope(repo, runID, atomID string) string {
	return repo + "\x00" + runID + "\x00" + atomID
}

func (s *proofAPIStore) ListRepos(context.Context) ([]store.Repo, error) {
	s.calls = append(s.calls, "list-repos")
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
	if s.onAssertions != nil {
		s.onAssertions(query)
	}
	if s.assertionErr != nil {
		return nil, s.assertionErr
	}
	var result []store.Assertion
	for _, assertion := range s.assertions[query.Repo] {
		if query.RunID != "" && assertion.RunID != query.RunID ||
			query.Predicate != "" && assertion.Predicate != query.Predicate ||
			query.Subject != "" && assertion.Subject != query.Subject ||
			query.Object != "" && assertion.Object != query.Object ||
			query.ObjectPrefix != "" && !strings.HasPrefix(assertion.Object, query.ObjectPrefix) ||
			query.Lineage != "" && assertion.Lineage != query.Lineage {
			continue
		}
		result = append(result, assertion)
	}
	sort.Slice(result, func(i, j int) bool {
		return compareAssertionTuple(result[i], result[j]) < 0
	})
	if query.After != nil {
		after := store.Assertion{
			Predicate: query.After.Predicate,
			Subject:   query.After.Subject,
			Object:    query.After.Object,
			ID:        query.After.ID,
			RunID:     query.After.RunID,
		}
		filtered := result[:0]
		for _, assertion := range result {
			if compareAssertionTuple(assertion, after) > 0 {
				filtered = append(filtered, assertion)
			}
		}
		result = filtered
	}
	if query.Limit > 0 && len(result) > query.Limit {
		if query.AllowTruncate {
			return append([]store.Assertion(nil), result[:query.Limit+1]...), nil
		}
		return nil, store.ErrResultLimit
	}
	return result, nil
}

func compareAssertionTuple(left, right store.Assertion) int {
	for _, pair := range [][2]string{
		{left.Predicate, right.Predicate},
		{left.Subject, right.Subject},
		{left.Object, right.Object},
		{left.ID, right.ID},
		{left.RunID, right.RunID},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

func (s *proofAPIStore) ListReverseAssertions(
	_ context.Context,
	query store.ReverseAssertionQuery,
) (*store.ReverseAssertionPage, error) {
	s.calls = append(s.calls, "reverse:"+proofScope(query.Repo, query.RunID))
	var rows []store.Assertion
	for _, assertion := range s.assertions[query.Repo] {
		if assertion.Repo != query.Repo || assertion.RunID != query.RunID ||
			assertion.Predicate != query.Predicate ||
			assertion.Object != query.Object ||
			query.Lineage != "" && assertion.Lineage != query.Lineage {
			continue
		}
		if query.After != nil && compareReverseAssertion(
			assertion, store.Assertion{
				Lineage: query.After.RowLineage,
				Subject: query.After.Subject, ID: query.After.AssertionID,
			},
		) <= 0 {
			continue
		}
		rows = append(rows, assertion)
	}
	sort.Slice(rows, func(i, j int) bool {
		return compareReverseAssertion(rows[i], rows[j]) < 0
	})
	limit := query.Limit
	if limit == 0 {
		limit = 50
	}
	page := &store.ReverseAssertionPage{Assertions: rows}
	if len(rows) <= limit {
		return page, nil
	}
	page.Assertions = rows[:limit]
	last := page.Assertions[len(page.Assertions)-1]
	page.Next = &store.ReverseAssertionCursor{
		Repo: query.Repo, RunID: query.RunID, Predicate: query.Predicate,
		Object: query.Object, QueryLineage: query.Lineage,
		RowLineage: last.Lineage, Subject: last.Subject, AssertionID: last.ID,
	}
	return page, nil
}

func compareReverseAssertion(left, right store.Assertion) int {
	for _, pair := range [][2]string{
		{left.Lineage, right.Lineage},
		{left.Subject, right.Subject},
		{left.ID, right.ID},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
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

func (s *proofAPIStore) GetProofBundle(_ context.Context, id string, activeAfter *time.Time) (*store.ProofBundleRecord, error) {
	s.calls = append(s.calls, "bundle:"+id)
	bundle, ok := s.bundles[id]
	if !ok || activeAfter != nil && !bundle.RetainedAt.After(*activeAfter) {
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
		Atom: store.EvidenceAtom{
			ID: atomID, SchemaVersion: "t14-test", BlobDigest: "sha256:" + strings.Repeat("c", 64),
			StartByte: 0, EndByte: 4,
		},
		Occurrences: []store.SnapshotEvidence{{
			ID: "occ_" + id, AtomID: atomID, Repo: repo, Commit: strings.Repeat("a", 40),
			Path: "consumer/" + id + ".go", StartLine: 7, EndLine: 7,
			VisibilityScope: "repo:" + repo, RunID: runID, ObservedAt: observed,
		}},
	}
	return assertion, resolution
}

func proofHandler(st *proofAPIStore, principal string, visible *map[string]bool, compatibility ...compat.Service) http.Handler {
	return proofHandlerWithRetention(st, principal, visible, 0, compatibility...)
}

func proofHandlerWithRetention(st *proofAPIStore, principal string, visible *map[string]bool, retention time.Duration, compatibility ...compat.Service) http.Handler {
	var checker compat.Service
	if len(compatibility) > 0 {
		checker = compatibility[0]
	}
	return api.New(api.Options{
		Version: "test", Store: st, Evidence: st, ProofBundles: st,
		ProofBundleRetention: retention, Compatibility: checker,
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

type fixedCompatibility struct {
	result  compat.CompatibilityResult
	err     error
	request compat.Request
}

type preflightCompatibility struct{}

func (preflightCompatibility) Check(
	ctx context.Context,
	request compat.Request,
) (*compat.CompatibilityResult, error) {
	_, err := compat.Prepare(ctx, request)
	return nil, err
}

func (c *fixedCompatibility) Check(_ context.Context, request compat.Request) (*compat.CompatibilityResult, error) {
	c.request = request
	if c.err != nil {
		return nil, c.err
	}
	result := c.result
	result.Violations = append([]compat.Violation(nil), c.result.Violations...)
	result.AffectedFields = append([]compat.FieldIdentity(nil), c.result.AffectedFields...)
	return &result, nil
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

func postCompatibility(t *testing.T, handler http.Handler, request compat.Request) (int, string, api.ProofBundleEnvelope) {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/check_contract_compatibility", strings.NewReader(string(encoded)))
	httpRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, httpRequest)
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

	st.calls = nil
	if code, _, got := getProof(t, member, "/api/proof_bundles/"+memberBundle.ID); code != http.StatusOK || got.ID != memberBundle.ID {
		t.Fatalf("member bundle read = %d %+v", code, got)
	}
	if len(st.calls) != 2 || st.calls[0] != "bundle:"+memberBundle.ID || st.calls[1] != "list-repos" {
		t.Fatalf("bundle read did not use one repository snapshot: %v", st.calls)
	}
	if code, body, _ := getProof(t, member, "/api/proof_bundles/"+adminBundle.ID); code != http.StatusNotFound || strings.Contains(body, "hidden") {
		t.Fatalf("member read of admin bundle = %d %s", code, body)
	}
	expiredRecord := st.bundles[memberBundle.ID]
	expiredRecord.RetainedAt = time.Now().UTC().Add(-2 * time.Hour)
	st.bundles[memberBundle.ID] = expiredRecord
	retainedMember := proofHandlerWithRetention(st, "user:member", &memberVisible, time.Hour)
	expiredCode, expiredBody, _ := getProof(t, retainedMember, "/api/proof_bundles/"+memberBundle.ID)
	missingCode, missingBody, _ := getProof(t, retainedMember, "/api/proof_bundles/pb_"+strings.Repeat("0", 64))
	deniedCode, deniedBody, _ := getProof(t, retainedMember, "/api/proof_bundles/"+adminBundle.ID)
	if expiredCode != http.StatusNotFound || missingCode != http.StatusNotFound || deniedCode != http.StatusNotFound ||
		expiredBody != missingBody || expiredBody != deniedBody {
		t.Fatalf("expired/missing/unauthorized responses differ: expired=(%d %s) missing=(%d %s) denied=(%d %s)",
			expiredCode, expiredBody, missingCode, missingBody, deniedCode, deniedBody)
	}
	memberVisible = map[string]bool{}
	if code, body, _ := getProof(t, member, "/api/proof_bundles/"+memberBundle.ID); code != http.StatusNotFound || strings.Contains(body, visibleRepo) {
		t.Fatalf("revoked bundle read = %d %s", code, body)
	}
}

func TestContractCompatibilityJoinsOnlyVisibleAffectedConsumers(t *testing.T) {
	const (
		visibleRepo = "github.com/allowed/cart-client"
		hiddenRepo  = "github.com/secret/cart-client"
		domain      = "scip-proto-field"
		lineage     = "contract_scip_package_v1_cart"
		message     = "shop.Cart"
	)
	visibleRun, hiddenRun := proofRun(visibleRepo, domain, "run-visible-field"), proofRun(hiddenRepo, domain, "run-hidden-field")
	visibleAssertion, visibleResolution := proofAssertion(visibleRepo, visibleRun.ID, "visible-field", "REFERENCES_PROTO_FIELD", message+"#1", lineage, "read")
	hiddenAssertion, hiddenResolution := proofAssertion(hiddenRepo, hiddenRun.ID, "hidden-field", "REFERENCES_PROTO_FIELD", message+"#1", lineage, "secret read")
	st := &proofAPIStore{
		repos: []store.Repo{
			{Name: hiddenRepo, IndexedCommitHash: hiddenRun.Commit},
			{Name: visibleRepo, IndexedCommitHash: visibleRun.Commit},
		},
		runs: map[string]store.ExtractionRun{
			proofScope(visibleRepo, domain): visibleRun,
			proofScope(hiddenRepo, domain):  hiddenRun,
		},
		assertions: map[string][]store.Assertion{
			visibleRepo: {visibleAssertion}, hiddenRepo: {hiddenAssertion},
		},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(visibleRepo, visibleRun.ID, visibleAssertion.Supporting[0]): visibleResolution,
			proofEvidenceScope(hiddenRepo, hiddenRun.ID, hiddenAssertion.Supporting[0]):    hiddenResolution,
		},
	}
	field := compat.FieldIdentity{Lineage: lineage, Message: message, Number: 1}
	checker := &fixedCompatibility{result: compat.CompatibilityResult{
		Compatible: false,
		Before:     compat.InputSnapshot{Digest: "sha256:" + strings.Repeat("1", 64), Files: []compat.InputFile{{Path: "shop/cart.proto", Digest: "sha256:" + strings.Repeat("2", 64)}}},
		After:      compat.InputSnapshot{Digest: "sha256:" + strings.Repeat("3", 64), Files: []compat.InputFile{{Path: "shop/cart.proto", Digest: "sha256:" + strings.Repeat("4", 64)}}},
		Violations: []compat.Violation{{
			Snapshot: "after", Path: "shop/cart.proto", StartLine: 3, StartColumn: 16,
			EndLine: 3, EndColumn: 22, Rule: "FIELD_WIRE_COMPATIBLE_TYPE",
			Message: "field changed wire type", Field: &field,
		}},
		AffectedFields: []compat.FieldIdentity{field},
		Run: compat.Run{
			Engine: "buf", Version: compat.Version, Policy: compat.Policy,
			Arguments: []string{"breaking", "../after", "--against", "../before"},
			ExitCode:  100, Result: "breaking",
		},
	}}
	visible := map[string]bool{visibleRepo: true}
	handler := proofHandler(st, "user:member", &visible, checker)
	request := compat.Request{
		Lineage: lineage,
		Before:  []compat.File{{Path: "shop/cart.proto", Content: "syntax = \"proto3\"; message Cart { int32 count = 1; }"}},
		After:   []compat.File{{Path: "shop/cart.proto", Content: "syntax = \"proto3\"; message Cart { string count = 1; }"}},
	}
	code, body, envelope := postCompatibility(t, handler, request)
	if code != http.StatusOK {
		t.Fatalf("compatibility = %d %s", code, body)
	}
	if checker.request.Lineage != lineage || envelope.Bundle.Query.Kind != "check_contract_compatibility" ||
		envelope.Bundle.Query.BeforeDigest != checker.result.Before.Digest ||
		envelope.Bundle.Query.AfterDigest != checker.result.After.Digest {
		t.Fatalf("request/query = %+v / %+v", checker.request, envelope.Bundle.Query)
	}
	if envelope.Bundle.Compatibility == nil || envelope.Bundle.Compatibility.Compatible ||
		len(envelope.Bundle.Compatibility.Violations) != 1 ||
		envelope.Bundle.Compatibility.Violations[0].Rule != "FIELD_WIRE_COMPATIBLE_TYPE" ||
		envelope.Bundle.Compatibility.Run.Version != compat.Version {
		t.Fatalf("compatibility verdict = %+v", envelope.Bundle.Compatibility)
	}
	if len(envelope.Bundle.Assertions) != 1 || envelope.Bundle.Assertions[0].Repo != visibleRepo ||
		len(envelope.Bundle.Evidence) != 1 || len(envelope.Bundle.Evidence[0].Occurrences) != 1 ||
		envelope.Bundle.Evidence[0].Occurrences[0].Path != "consumer/visible-field.go" ||
		envelope.Bundle.Evidence[0].Occurrences[0].StartLine != 7 {
		t.Fatalf("consumer citations = %+v / %+v", envelope.Bundle.Assertions, envelope.Bundle.Evidence)
	}
	if strings.Contains(body, hiddenRepo) || strings.Contains(body, "secret") ||
		strings.Contains(body, "int32 count") || strings.Contains(body, "string count") {
		t.Fatalf("bundle leaked hidden evidence or source input: %s", body)
	}
	for _, call := range st.calls {
		if strings.Contains(call, hiddenRepo) || strings.Contains(call, hiddenRun.ID) {
			t.Fatalf("compatibility query touched hidden evidence: %v", st.calls)
		}
	}
	if envelope.Bundle.Caveat == "" || !strings.Contains(envelope.Bundle.Caveat, "WIRE verdict") || len(st.bundles) != 1 {
		t.Fatalf("bundle caveat/persistence = %q / %d", envelope.Bundle.Caveat, len(st.bundles))
	}
	retryCode, retryBody, retry := postCompatibility(t, handler, request)
	if retryCode != http.StatusOK ||
		retry.ID != envelope.ID ||
		retryBody != body ||
		len(st.bundles) != 1 {
		t.Fatalf(
			"compatibility exact retry changed wire bytes/id: %d %s / %s bundles=%d",
			retryCode,
			retry.ID,
			envelope.ID,
			len(st.bundles),
		)
	}

	// The endpoint is dark when a real checker is absent; the shared proof
	// service still exposes the three evidence-only T14.2 operations.
	dark := proofHandler(st, "user:member", &visible)
	if darkCode, darkBody, _ := postCompatibility(t, dark, request); darkCode != http.StatusNotFound {
		t.Fatalf("dark compatibility endpoint = %d %s", darkCode, darkBody)
	}
}

func TestContractCompatibilityClassifiesBoundedCheckerRefusalsBeforeEvidence(t *testing.T) {
	request := compat.Request{
		Lineage: "contract_lineage",
		Before:  []compat.File{{Path: "x.proto", Content: "syntax = \"proto3\";"}},
		After:   []compat.File{{Path: "x.proto", Content: "syntax = \"proto3\";"}},
	}
	for _, test := range []struct {
		name string
		err  error
		code int
	}{
		{name: "invalid input", err: compat.ErrInvalidInput, code: http.StatusBadRequest},
		{name: "resource limit", err: compat.ErrLimit, code: http.StatusUnprocessableEntity},
		{name: "sandbox unavailable", err: compat.ErrUnavailable, code: http.StatusServiceUnavailable},
		{name: "engine refusal", err: compat.ErrCheckFailed, code: http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := &proofAPIStore{}
			code, body, _ := postCompatibility(t,
				proofHandler(st, "user:member", nil, &fixedCompatibility{err: test.err}), request)
			if code != test.code || body == "" {
				t.Fatalf("response = %d %s, want %d", code, body, test.code)
			}
			if len(st.calls) != 0 || len(st.bundles) != 0 {
				t.Fatalf("failed checker touched evidence or persisted: calls=%v bundles=%v", st.calls, st.bundles)
			}
		})
	}
}

func TestContractCompatibilityHTTPKeepsFrozenTokenRefusalBytes(t *testing.T) {
	st := &proofAPIStore{}
	request := compat.Request{
		Lineage: "contract_lineage",
		Before: []compat.File{{
			Path:    "x.proto",
			Content: strings.Repeat("x ", 500_001),
		}},
		After: []compat.File{{
			Path:    "x.proto",
			Content: "syntax = \"proto3\";",
		}},
	}
	code, body, _ := postCompatibility(
		t,
		proofHandler(
			st,
			"user:member",
			nil,
			preflightCompatibility{},
		),
		request,
	)
	const want = "{\"$schema\":\"https://example.com/schemas/ErrorModel.json\"," +
		"\"title\":\"Bad Request\",\"status\":400," +
		"\"detail\":\"invalid compatibility input: parse field identities: " +
		"before/x.proto: source exceeds 500000-token parser limit\"}\n"
	if code != http.StatusBadRequest || body != want {
		t.Fatalf("compatibility rejection bytes = %d %s", code, body)
	}
	if len(st.calls) != 0 || len(st.bundles) != 0 {
		t.Fatalf(
			"preflight rejection touched evidence: calls=%v bundles=%v",
			st.calls,
			st.bundles,
		)
	}
}

func TestProofQueryLimitsReturnUnprocessableEntity(t *testing.T) {
	const (
		repo      = "github.com/allowed/limits"
		domain    = "grpc-consumer"
		operation = "/shop.Cart/Get"
	)
	run := proofRun(repo, domain, "run-limits")
	makeStore := func(assertions []store.Assertion) *proofAPIStore {
		return &proofAPIStore{
			repos:      []store.Repo{{Name: repo, IndexedCommitHash: run.Commit}},
			runs:       map[string]store.ExtractionRun{proofScope(repo, domain): run},
			assertions: map[string][]store.Assertion{repo: assertions},
		}
	}
	rows := make([]store.Assertion, 5001)
	for i := range rows {
		rows[i] = store.Assertion{
			ID: "assertion-" + strconv.Itoa(i), Predicate: "CALLS_OPERATION", Object: operation,
			Repo: repo, RunID: run.ID,
		}
	}
	evidenceHeavy := store.Assertion{
		ID: "evidence-heavy", Predicate: "CALLS_OPERATION", Object: operation,
		Repo: repo, RunID: run.ID, Supporting: make([]string, 20_001),
	}
	for i := range evidenceHeavy.Supporting {
		evidenceHeavy.Supporting[i] = "atom-" + strconv.Itoa(i)
	}
	for _, test := range []struct {
		name       string
		assertions []store.Assertion
	}{
		{name: "assertions", assertions: rows},
		{name: "evidence references", assertions: []store.Assertion{evidenceHeavy}},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := makeStore(test.assertions)
			code, body, _ := getProof(t, proofHandler(st, "user:member", nil),
				"/api/find_operation_consumers?operation=%2Fshop.Cart%2FGet")
			if code != http.StatusUnprocessableEntity || !strings.Contains(body, "narrow the query") || len(st.bundles) != 0 {
				t.Fatalf("limit response = %d %s, bundles=%d", code, body, len(st.bundles))
			}
		})
	}
	t.Run("store result limit", func(t *testing.T) {
		st := makeStore(nil)
		st.assertionErr = store.ErrResultLimit
		code, body, _ := getProof(t, proofHandler(st, "user:member", nil),
			"/api/find_operation_consumers?operation=%2Fshop.Cart%2FGet")
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "narrow the query") || len(st.bundles) != 0 {
			t.Fatalf("store limit response = %d %s, bundles=%d", code, body, len(st.bundles))
		}
	})
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
	code, _, defaultCoverage := getProof(
		t, handler, "/api/get_extraction_coverage",
	)
	wantDefaultDomains := strings.Join([]string{
		"grpc-caller", "grpc-consumer", "kafka-consumer", "kafka-producer",
		"proto-contract", "scip-proto-field", "thrift-caller",
		"thrift-consumer", "thrift-contract",
	}, "\x00")
	if code != http.StatusOK ||
		strings.Join(defaultCoverage.Bundle.Query.Domains, "\x00") != wantDefaultDomains {
		t.Fatalf("default coverage domains = %d %+v",
			code, defaultCoverage.Bundle.Query.Domains)
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

func TestFindKafkaTopicUsage(t *testing.T) {
	const repo = "github.com/allowed/kafka"
	producerRun := proofRun(repo, "kafka-producer", "run-kafka-producer")
	consumerRun := proofRun(repo, "kafka-consumer", "run-kafka-consumer")
	produces, producesResolution := proofAssertion(repo, producerRun.ID, "produces", "PRODUCES_TO_TOPIC", "topic:orders-v1", "provisional_repo_path_v1_"+strings.Repeat("a", 64), "orders")
	otherTopic, otherResolution := proofAssertion(repo, producerRun.ID, "other-topic", "PRODUCES_TO_TOPIC", "topic:other", "provisional_repo_path_v1_"+strings.Repeat("b", 64), "other")
	consumes, consumesResolution := proofAssertion(repo, consumerRun.ID, "consumes", "CONSUMES_FROM_TOPIC", "topic:orders-v1", "provisional_repo_path_v1_"+strings.Repeat("c", 64), "reader")
	unresolvedRow, unresolvedResolution := proofAssertion(repo, producerRun.ID, "abstains", "UNRESOLVED_KAFKA_PRODUCER", "unresolved:call-expr", "", "env")
	// One unresolved assertion can merge several exact source sites. The
	// census counts supporting sites, not assertion/file-shape groups.
	unresolvedRow.Supporting = append(unresolvedRow.Supporting, "atom-second-site")
	var assertionQueries []store.AssertionQuery
	st := &proofAPIStore{
		repos: []store.Repo{{Name: repo, IndexedCommitHash: producerRun.Commit}},
		runs: map[string]store.ExtractionRun{
			proofScope(repo, "kafka-producer"): producerRun,
			proofScope(repo, "kafka-consumer"): consumerRun,
		},
		assertions: map[string][]store.Assertion{repo: {produces, otherTopic, consumes, unresolvedRow}},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(repo, producerRun.ID, produces.Supporting[0]):      producesResolution,
			proofEvidenceScope(repo, producerRun.ID, otherTopic.Supporting[0]):    otherResolution,
			proofEvidenceScope(repo, consumerRun.ID, consumes.Supporting[0]):      consumesResolution,
			proofEvidenceScope(repo, producerRun.ID, unresolvedRow.Supporting[0]): unresolvedResolution,
		},
		onAssertions: func(query store.AssertionQuery) {
			assertionQueries = append(assertionQueries, query)
		},
	}
	handler := proofHandler(st, "user:member", nil)
	code, body, envelope := getProof(t, handler, "/api/find_kafka_topic_usage?topic=orders-v1")
	if code != http.StatusOK || len(envelope.Bundle.Assertions) != 2 || strings.Contains(body, `"other-topic"`) {
		t.Fatalf("topic query = %d %s", code, body)
	}
	if envelope.Bundle.Query.Kind != "find_kafka_topic_usage" || envelope.Bundle.Query.Topic != "orders-v1" {
		t.Fatalf("topic bundle query = %+v", envelope.Bundle.Query)
	}
	census := envelope.Bundle.UnresolvedCensus
	if census == nil || census.SchemaVersion != "kafka-topic-census-v1" {
		t.Fatalf("census = %+v", census)
	}
	// Both kafka runs are published, no class hit the bounded row limit —
	// the census must say so explicitly so "nothing ran" and "nothing was
	// unresolved" can never be conflated.
	if census.PublishedRuns != 2 || census.ProducerPublishedRuns != 1 ||
		census.ConsumerPublishedRuns != 1 || len(census.Truncated) != 0 {
		t.Fatalf("census run/truncation state = %+v", census)
	}
	// The topic query performs two exact evidence reads plus one prefix-batched
	// census read per published plane/run — never six class reads per plane.
	if len(assertionQueries) != 4 {
		t.Fatalf("topic assertion query count = %d, want 4: %+v", len(assertionQueries), assertionQueries)
	}
	censusQueries := 0
	for _, query := range assertionQueries {
		if query.ObjectPrefix == "unresolved:" {
			censusQueries++
			if query.Object != "" || !query.AllowTruncate {
				t.Fatalf("unbounded census query = %+v", query)
			}
		}
	}
	if censusQueries != 2 {
		t.Fatalf("census query count = %d, want 2", censusQueries)
	}
	// Every frozen shape class is present in both planes even at zero; the
	// one abstention row is counted; the abstention rows never appear as
	// bundle assertions.
	if len(census.Producer) != 6 || len(census.Consumer) != 6 {
		t.Fatalf("census classes = %+v", census)
	}
	for class, count := range census.Producer {
		want := 0
		if class == "call-expr" {
			want = 2
		}
		if count != want {
			t.Fatalf("producer census[%s] = %d, want %d", class, count, want)
		}
	}
	for class, count := range census.Consumer {
		if count != 0 {
			t.Fatalf("consumer census[%s] = %d, want 0", class, count)
		}
	}
	for _, assertion := range envelope.Bundle.Assertions {
		if strings.HasPrefix(assertion.Object, "unresolved:") {
			t.Fatalf("census row leaked into bundle assertions: %+v", assertion)
		}
	}

	// A second identical question rebuilds an identical census.
	code, _, second := getProof(t, handler, "/api/find_kafka_topic_usage?topic=orders-v1")
	if code != http.StatusOK || second.Bundle.UnresolvedCensus == nil ||
		second.Bundle.UnresolvedCensus.Producer["call-expr"] != 2 {
		t.Fatalf("second topic query = %d %+v", code, second.Bundle.UnresolvedCensus)
	}

	// Per-domain publication is intentionally isolated. A producer-only
	// replacement must not make consumer zeros look measured.
	delete(st.runs, proofScope(repo, "kafka-consumer"))
	code, _, producerOnly := getProof(t, handler, "/api/find_kafka_topic_usage?topic=orders-v1")
	partial := producerOnly.Bundle.UnresolvedCensus
	if code != http.StatusOK || partial == nil || partial.PublishedRuns != 1 ||
		partial.ProducerPublishedRuns != 1 || partial.ConsumerPublishedRuns != 0 {
		t.Fatalf("producer-only census = %d %+v", code, partial)
	}

	// KD2 identity bounds refuse illegal spellings; existing surfaces stay
	// untouched by the census (their bundles omit the field entirely).
	invalidTargets := []string{
		"/api/find_kafka_topic_usage?topic=bad%20topic%21",
		"/api/find_kafka_topic_usage?topic=.",
		"/api/find_kafka_topic_usage?topic=" + strings.Repeat("x", 250),
	}
	for _, invalid := range invalidTargets {
		if code, _, _ := getProof(t, handler, invalid); code != http.StatusBadRequest && code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid topic query %q = %d", invalid, code)
		}
	}
	code, body, _ = getProof(t, handler, "/api/get_extraction_coverage?domains=kafka-producer,kafka-consumer")
	if code != http.StatusOK || strings.Contains(body, "unresolved_census") {
		t.Fatalf("coverage query carries a census: %d %s", code, body)
	}
}

func TestProofAPIDarkWithoutBundleStore(t *testing.T) {
	st := &proofAPIStore{}
	handler := api.New(api.Options{Version: "test", Store: st, Evidence: st})
	paths := []string{
		"/api/find_operation_consumers?operation=%2Fshop.Cart%2FGet",
		"/api/find_proto_field_references?lineage=x&message=shop.Cart&field_number=1",
		"/api/find_kafka_topic_usage?topic=orders-v1",
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
