package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	catalogProtoDomain = "proto-contract"
	catalogGRPCDomain  = "grpc-consumer"
	catalogCommit      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func catalogOptions(
	st *proofAPIStore,
	principal string,
	visible *map[string]bool,
) api.Options {
	return api.Options{
		Version: "test", Store: st, Evidence: st,
		Principal: func(context.Context) string { return principal },
		Visible: func(context.Context) func(store.Repo) bool {
			if visible == nil {
				return nil
			}
			return func(repo store.Repo) bool { return (*visible)[repo.Name] }
		},
		AuthorizationProvider: "catalog-test-permissions-v1",
	}
}

func catalogRun(repo, domain, runID string, count int) store.ExtractionRun {
	run := proofRun(repo, domain, runID)
	run.Commit = catalogCommit
	run.Coverage.AssertionCount = count
	run.Coverage.AtomCount = count
	if domain == catalogProtoDomain {
		run.Coverage.Protocols = []string{"protobuf", "protobuf-shapes"}
	}
	return run
}

func catalogAssertion(
	repo, runID, id, predicate, path, object, lineage, detail string,
) (store.Assertion, store.EvidenceResolution) {
	atomID := "ea_" + id
	assertion := store.Assertion{
		ID: id, Predicate: predicate, Subject: path, Object: object,
		Lineage: lineage, Tier: store.TierExact, Repo: repo, RunID: runID,
		Supporting: []string{atomID}, Detail: detail,
	}
	resolution := store.EvidenceResolution{
		Atom: store.EvidenceAtom{
			ID: atomID, SchemaVersion: "t17-test",
			BlobDigest: "sha256:" + strings.Repeat("c", 64),
			StartByte:  11, EndByte: 29,
		},
		Occurrences: []store.SnapshotEvidence{{
			ID: "occ_" + id, AtomID: atomID, Repo: repo, Commit: catalogCommit,
			Path: path, StartLine: 3, EndLine: 4,
			VisibilityScope: "repo:" + repo, RunID: runID,
		}},
	}
	return assertion, resolution
}

func putCatalogAssertion(
	st *proofAPIStore,
	assertion store.Assertion,
	resolution store.EvidenceResolution,
) {
	st.assertions[assertion.Repo] = append(st.assertions[assertion.Repo], assertion)
	st.resolutions[proofEvidenceScope(
		assertion.Repo, assertion.RunID, assertion.Supporting[0],
	)] = resolution
}

func catalogHTTP(
	t *testing.T,
	handler http.Handler,
	target string,
	targetValue any,
) (int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	if recorder.Code == http.StatusOK && targetValue != nil {
		if err := json.Unmarshal(recorder.Body.Bytes(), targetValue); err != nil {
			t.Fatal(err)
		}
	}
	return recorder.Code, recorder.Body.String()
}

func requireCatalogStatus(t *testing.T, err error, want int) {
	t.Helper()
	statusErr, ok := err.(huma.StatusError)
	if !ok || statusErr.GetStatus() != want {
		t.Fatalf("error = %T %v, want HTTP %d", err, err, want)
	}
}

func TestContractCatalogDarkPostureAndScopeNonDisclosure(t *testing.T) {
	t.Run("dark and anonymous", func(t *testing.T) {
		st := &proofAPIStore{}
		dark := api.New(api.Options{
			Version: "test", Store: st,
			Principal: func(context.Context) string { return "user:member" },
		})
		for _, target := range []string{"/api/contract_atlas", "/api/openapi.json"} {
			code, body := catalogHTTP(t, dark, target, nil)
			if target == "/api/contract_atlas" && code != http.StatusNotFound {
				t.Fatalf("dark route = %d %s", code, body)
			}
			if strings.Contains(body, `"/api/contract_atlas"`) ||
				strings.Contains(body, `"contract-atlas"`) {
				t.Fatalf("dark API advertised the catalog: %s", body)
			}
		}

		anonymous := api.New(catalogOptions(st, "", nil))
		code, body := catalogHTTP(t, anonymous, "/api/contract_atlas", nil)
		if code != http.StatusNotFound || len(st.calls) != 0 ||
			strings.Contains(body, "repository") || strings.Contains(body, "coverage") {
			t.Fatalf("anonymous catalog = %d %s, calls=%v", code, body, st.calls)
		}
		_, version := catalogHTTP(t, anonymous, "/api/version", nil)
		if strings.Contains(version, "contract-atlas") {
			t.Fatalf("anonymous capabilities leaked catalog: %s", version)
		}

		enabled := api.New(catalogOptions(st, "user:member", nil))
		_, version = catalogHTTP(t, enabled, "/api/version", nil)
		if !strings.Contains(version, `"contract-atlas"`) {
			t.Fatalf("enabled capability absent: %s", version)
		}
		_, openAPI := catalogHTTP(t, enabled, "/api/openapi.json", nil)
		if !strings.Contains(openAPI, `"/api/contract_atlas"`) ||
			!strings.Contains(openAPI, `"/api/contract_atlas/operation"`) {
			t.Fatalf("enabled OpenAPI lacks catalog paths")
		}
	})

	t.Run("unknown hidden and deleting are indistinguishable", func(t *testing.T) {
		const (
			visibleRepo  = "github.com/visible/service"
			hiddenRepo   = "github.com/private/service"
			deletingRepo = "github.com/deleting/service"
			unknownRepo  = "github.com/unknown/service"
		)
		st := &proofAPIStore{repos: []store.Repo{
			{Name: visibleRepo, IndexedCommitHash: catalogCommit},
			{Name: hiddenRepo, IndexedCommitHash: catalogCommit},
			{Name: deletingRepo, IndexedCommitHash: catalogCommit, Deleting: true},
		}}
		visible := map[string]bool{
			visibleRepo: true, hiddenRepo: false, deletingRepo: true,
		}
		handler := api.New(catalogOptions(st, "user:member", &visible))
		for _, route := range []string{"list", "operation"} {
			var baselineCode int
			var baselineBody string
			for index, repository := range []string{unknownRepo, hiddenRepo, deletingRepo} {
				st.calls = nil
				target := "/api/contract_atlas?repository=" + url.QueryEscape(repository)
				if route == "operation" {
					target = "/api/contract_atlas/operation?repository=" +
						url.QueryEscape(repository) +
						"&lineage=contract_lineage&operation=%2Fdemo.Service%2FGet"
				}
				code, body := catalogHTTP(t, handler, target, nil)
				if code != http.StatusNotFound || len(st.calls) != 1 ||
					st.calls[0] != "list-repos" ||
					strings.Contains(body, repository) ||
					strings.Contains(body, "count") ||
					strings.Contains(body, "cursor") {
					t.Fatalf("%s %q = %d %s calls=%v", route, repository, code, body, st.calls)
				}
				if index == 0 {
					baselineCode, baselineBody = code, body
				} else if code != baselineCode || body != baselineBody {
					t.Fatalf("%s refusal differs: %d %q vs %d %q",
						route, baselineCode, baselineBody, code, body)
				}
			}
		}
	})
}

func TestContractCatalogDuplicateIdentityPaginationAndCursorBinding(t *testing.T) {
	const (
		repoA   = "github.com/acme/a"
		repoB   = "github.com/acme/b"
		service = "demo.Catalog"
	)
	runA := catalogRun(repoA, catalogProtoDomain, "run-proto-a", 2)
	runB := catalogRun(repoB, catalogProtoDomain, "run-proto-b", 1)
	st := &proofAPIStore{
		repos: []store.Repo{
			{Name: repoB, IndexedCommitHash: catalogCommit},
			{Name: repoA, IndexedCommitHash: catalogCommit},
		},
		runs: map[string]store.ExtractionRun{
			proofScope(repoA, catalogProtoDomain): runA,
			proofScope(repoB, catalogProtoDomain): runB,
		},
		assertions:  map[string][]store.Assertion{},
		resolutions: map[string]store.EvidenceResolution{},
	}
	for _, row := range []struct {
		repo, run, id, path, lineage string
	}{
		{repoA, runA.ID, "service-a-one", "proto/one.proto", "lineage-one"},
		{repoA, runA.ID, "service-a-two", "proto/two.proto", "lineage-two"},
		{repoB, runB.ID, "service-b-one", "proto/one.proto", "lineage-one"},
	} {
		assertion, resolution := catalogAssertion(
			row.repo, row.run, row.id, "DECLARES_SERVICE", row.path,
			service, row.lineage, `{"schema":"proto-service-detail-v1","name":"Catalog"}`,
		)
		putCatalogAssertion(st, assertion, resolution)
	}
	serviceAPI := api.NewContractCatalogService(catalogOptions(st, "user:member", nil))
	first, err := serviceAPI.List(context.Background(), api.ContractCatalogQuery{}, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	firstAgain, err := serviceAPI.List(context.Background(), api.ContractCatalogQuery{}, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := json.Marshal(first)
	repeatBytes, _ := json.Marshal(firstAgain)
	if string(firstBytes) != string(repeatBytes) || first.Pagination.NextCursor == "" ||
		first.Pagination.Complete || !first.Pagination.Truncated {
		t.Fatalf("unstable or unbounded first page: %s / %s", firstBytes, repeatBytes)
	}

	allItems := append([]api.ContractCatalogItem(nil), first.Items...)
	cursor := first.Pagination.NextCursor
	for cursor != "" {
		page, err := serviceAPI.List(context.Background(), api.ContractCatalogQuery{}, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		allItems = append(allItems, page.Items...)
		cursor = page.Pagination.NextCursor
	}
	if len(allItems) != 3 {
		t.Fatalf("catalog items = %+v", allItems)
	}
	identities := map[string]bool{}
	for _, item := range allItems {
		identity := strings.Join([]string{
			item.Repository, item.Lineage, item.ServiceFQN, item.Method,
		}, "\x00")
		if identities[identity] {
			t.Fatalf("duplicate paged identity %q", identity)
		}
		identities[identity] = true
		if item.ServiceFQN != service || item.Declaration.RunID == "" ||
			len(item.Declaration.Sources) != 1 ||
			item.Declaration.Sources[0].Commit != catalogCommit {
			t.Fatalf("incomplete catalog row: %+v", item)
		}
	}

	otherPrincipal := api.NewContractCatalogService(catalogOptions(st, "user:other", nil))
	_, err = otherPrincipal.List(
		context.Background(), api.ContractCatalogQuery{}, 2, first.Pagination.NextCursor,
	)
	requireCatalogStatus(t, err, http.StatusConflict)

	changedRun := runA
	changedRun.ID = "run-proto-a-replacement"
	changedRun.Commit = strings.Repeat("d", 40)
	st.runs[proofScope(repoA, catalogProtoDomain)] = changedRun
	_, err = serviceAPI.List(
		context.Background(), api.ContractCatalogQuery{}, 2, first.Pagination.NextCursor,
	)
	requireCatalogStatus(t, err, http.StatusConflict)
}

func TestContractCatalogOperationShapesRelationshipsAndNoPersistence(t *testing.T) {
	const (
		contractRepo = "github.com/acme/contracts"
		consumerRepo = "github.com/acme/consumer"
		lineage      = "contract_lineage_demo"
		service      = "demo.Catalog"
		method       = "Get"
	)
	protoRun := catalogRun(contractRepo, catalogProtoDomain, "run-proto", 4)
	grpcRun := catalogRun(consumerRepo, catalogGRPCDomain, "run-grpc", 3)
	st := &proofAPIStore{
		repos: []store.Repo{
			{Name: consumerRepo, IndexedCommitHash: catalogCommit},
			{Name: contractRepo, IndexedCommitHash: catalogCommit},
		},
		runs: map[string]store.ExtractionRun{
			proofScope(contractRepo, catalogProtoDomain): protoRun,
			proofScope(consumerRepo, catalogGRPCDomain):  grpcRun,
		},
		assertions:  map[string][]store.Assertion{},
		resolutions: map[string]store.EvidenceResolution{},
		bundles:     map[string]store.ProofBundleRecord{},
	}
	operationDetail := `{"schema":"proto-operation-detail-v1","request":{"raw":"Request","kind":"message","resolution":"same_file","declaration":"demo.Request"},"response":{"raw":"Response","kind":"message","resolution":"same_file","declaration":"demo.Response"},"client_streaming":false,"server_streaming":true}`
	messageDetail := `{"schema":"proto-message-detail-v1","name":"message"}`
	fieldDetail := `{"schema":"proto-field-detail-v2","name":"next","type":{"raw":"Request","kind":"message","resolution":"same_file","declaration":"demo.Request"},"cardinality":"singular"}`
	for _, row := range []struct {
		id, predicate, path, object, detail string
	}{
		{"operation", "DECLARES_OPERATION", "proto/catalog.proto", service + "/" + method, operationDetail},
		{"request", "DECLARES_MESSAGE", "proto/catalog.proto", "demo.Request", messageDetail},
		{"response", "DECLARES_MESSAGE", "proto/catalog.proto", "demo.Response", messageDetail},
		{"request-field", "DECLARES_FIELD", "proto/catalog.proto", "demo.Request#1", fieldDetail},
	} {
		assertion, resolution := catalogAssertion(
			contractRepo, protoRun.ID, row.id, row.predicate, row.path,
			row.object, lineage, row.detail,
		)
		putCatalogAssertion(st, assertion, resolution)
	}
	for _, row := range []struct {
		id, predicate, object, claimLineage, tier string
	}{
		{"registration", "REGISTERS_GRPC_SERVICE", service, lineage, store.TierDerived},
		{"caller", "CALLS_OPERATION", "/" + service + "/" + method, "provisional_other", store.TierHeuristic},
		{"ambiguous", "UNRESOLVED_GRPC_CALL", method, "", store.TierUnresolved},
	} {
		assertion, resolution := catalogAssertion(
			consumerRepo, grpcRun.ID, row.id, row.predicate,
			"go/"+row.id+".go", row.object, row.claimLineage, `{}`,
		)
		assertion.Tier = row.tier
		assertion.CodeRole = "production"
		putCatalogAssertion(st, assertion, resolution)
	}

	options := catalogOptions(st, "user:member", nil)
	options.ProofBundles = st
	serviceAPI := api.NewContractCatalogService(options)
	detail, err := serviceAPI.Operation(
		context.Background(), contractRepo, lineage, "/"+service+"/"+method,
	)
	if err != nil {
		t.Fatal(err)
	}
	if detail.SchemaVersion != "contract-atlas-v1" ||
		detail.Declaration.RunID != protoRun.ID ||
		detail.FactDetail.ServerStreaming != true ||
		detail.Request.State != "resolved" ||
		len(detail.Request.Fields) != 1 ||
		detail.Request.Fields[0].Nested == nil ||
		detail.Request.Fields[0].Nested.State != "cycle" ||
		detail.Response.State != "resolved" {
		t.Fatalf("operation shape = %+v", detail)
	}
	if len(detail.Implementations) != 1 ||
		detail.Implementations[0].Classification != "proven" ||
		len(detail.Callers) != 1 ||
		detail.Callers[0].Classification != "unresolved_name_match" ||
		len(detail.UnresolvedCandidates) != 1 ||
		detail.UnresolvedCandidates[0].Classification != "extractor_abstention" {
		t.Fatalf("relationships = implementations=%+v callers=%+v unresolved=%+v",
			detail.Implementations, detail.Callers, detail.UnresolvedCandidates)
	}
	for _, relationship := range append(
		append([]api.ContractCatalogRelationship{}, detail.Implementations...),
		append(detail.Callers, detail.UnresolvedCandidates...)...,
	) {
		if relationship.Claim.RunID != grpcRun.ID ||
			len(relationship.Claim.Sources) != 1 ||
			relationship.Claim.Sources[0].AssertionID != relationship.Claim.AssertionID {
			t.Fatalf("relationship lacks immutable locator: %+v", relationship)
		}
	}
	if detail.CoverageDigest == "" || detail.Coverage.Digest != detail.CoverageDigest ||
		detail.Caveat == "" || len(st.bundles) != 0 {
		t.Fatalf("coverage/caveat/persistence = %q / %q / %d",
			detail.CoverageDigest, detail.Caveat, len(st.bundles))
	}
	for _, call := range st.calls {
		if strings.HasPrefix(call, "put:") {
			t.Fatalf("read-only catalog persisted a proof bundle: %v", st.calls)
		}
	}
}

func TestContractCatalogOperationWithoutRelationshipsKeepsEmptySlices(t *testing.T) {
	const (
		contractRepo = "github.com/acme/contracts"
		lineage      = "contract_lineage_demo"
		operation    = "demo.Catalog/Get"
	)
	protoRun := catalogRun(contractRepo, catalogProtoDomain, "run-proto", 4)
	st := &proofAPIStore{
		repos: []store.Repo{{Name: contractRepo, IndexedCommitHash: catalogCommit}},
		runs: map[string]store.ExtractionRun{
			proofScope(contractRepo, catalogProtoDomain): protoRun,
		},
		assertions:  map[string][]store.Assertion{},
		resolutions: map[string]store.EvidenceResolution{},
		bundles:     map[string]store.ProofBundleRecord{},
	}
	operationDetail := `{"schema":"proto-operation-detail-v1","request":{"raw":"Request","resolution":"unresolved"},"response":{"raw":"Response","resolution":"unresolved"},"client_streaming":false,"server_streaming":false}`
	assertion, resolution := catalogAssertion(
		contractRepo, protoRun.ID, "operation", "DECLARES_OPERATION",
		"proto/catalog.proto", operation, lineage, operationDetail,
	)
	putCatalogAssertion(st, assertion, resolution)

	options := catalogOptions(st, "user:member", nil)
	options.ProofBundles = st
	serviceAPI := api.NewContractCatalogService(options)
	detail, err := serviceAPI.Operation(context.Background(), contractRepo, lineage, "/"+operation)
	if err != nil {
		t.Fatal(err)
	}
	// nil slices marshal as JSON null and crash the atlas UI, which spreads them.
	if detail.Implementations == nil || detail.Callers == nil || detail.UnresolvedCandidates == nil {
		t.Fatalf("relationship slices must be non-nil: implementations=%v callers=%v unresolved=%v",
			detail.Implementations == nil, detail.Callers == nil, detail.UnresolvedCandidates == nil)
	}
	if len(detail.Implementations)+len(detail.Callers)+len(detail.UnresolvedCandidates) != 0 {
		t.Fatalf("expected no relationships, got %+v", detail)
	}
}

func TestContractCatalogThriftPack(t *testing.T) {
	const (
		idlRepo      = "github.com/acme/thrift-idl"
		consumerRepo = "github.com/acme/thrift-consumer"
		lineage      = "thrift_lineage_demo"
		service      = "agent.Agent"
	)
	thriftRun := catalogRun(idlRepo, "thrift-contract", "run-thrift", 6)
	consumerRun := catalogRun(consumerRepo, "thrift-consumer", "run-thrift-go", 2)
	st := &proofAPIStore{
		repos: []store.Repo{
			{Name: consumerRepo, IndexedCommitHash: catalogCommit},
			{Name: idlRepo, IndexedCommitHash: catalogCommit},
		},
		runs: map[string]store.ExtractionRun{
			proofScope(idlRepo, "thrift-contract"):      thriftRun,
			proofScope(consumerRepo, "thrift-consumer"): consumerRun,
		},
		assertions:  map[string][]store.Assertion{},
		resolutions: map[string]store.EvidenceResolution{},
		bundles:     map[string]store.ProofBundleRecord{},
	}
	operationDetail := `{"schema":"thrift-operation-detail-v1","request":{"raw":"Agent.emitBatch_args","kind":"message","resolution":"same_file","declaration":"agent.Agent.emitBatch_args"},"response":{"raw":"Agent.emitBatch_result","kind":"message","resolution":"same_file","declaration":"agent.Agent.emitBatch_result"},"oneway":false}`
	messageDetail := `{"schema":"thrift-message-detail-v1","name":"emitBatch_args","kind":"struct","synthetic":true}`
	resultDetail := `{"schema":"thrift-message-detail-v1","name":"emitBatch_result","kind":"struct","synthetic":true}`
	successDetail := `{"schema":"thrift-field-detail-v1","name":"success","type":{"raw":"Batch","kind":"message","resolution":"unresolved","reason":"DECLARATION_NOT_FOUND"},"cardinality":"default"}`
	for _, row := range []struct {
		id, predicate, object, detail string
	}{
		{"svc", "DECLARES_SERVICE", service, `{"schema":"thrift-service-detail-v1","name":"Agent"}`},
		{"op", "DECLARES_OPERATION", service + "/emitBatch", operationDetail},
		{"args", "DECLARES_MESSAGE", "agent.Agent.emitBatch_args", messageDetail},
		{"result", "DECLARES_MESSAGE", "agent.Agent.emitBatch_result", resultDetail},
		// Field 0 is the wire success slot: the proto bounds would reject it.
		{"success", "DECLARES_FIELD", "agent.Agent.emitBatch_result#0", successDetail},
	} {
		assertion, resolution := catalogAssertion(
			idlRepo, thriftRun.ID, row.id, row.predicate, "idl/agent.thrift",
			row.object, lineage, row.detail,
		)
		putCatalogAssertion(st, assertion, resolution)
	}
	registration, registrationResolution := catalogAssertion(
		consumerRepo, consumerRun.ID, "reg", "REGISTERS_THRIFT_SERVICE",
		"go/server.go", service, "provisional_other", `{}`,
	)
	registration.Tier = store.TierDerived
	putCatalogAssertion(st, registration, registrationResolution)
	caller, callerResolution := catalogAssertion(
		consumerRepo, consumerRun.ID, "call", "CALLS_OPERATION",
		"go/client.go", "/"+service+"/emitBatch", "provisional_other", `{}`,
	)
	caller.Tier = store.TierHeuristic
	putCatalogAssertion(st, caller, callerResolution)
	// Thrift call abstentions carry the generated Go method name (EmitBatch),
	// not the wire name; the collector must derive that form to find them.
	ambiguous, ambiguousResolution := catalogAssertion(
		consumerRepo, consumerRun.ID, "amb", "UNRESOLVED_THRIFT_CALL",
		"go/other.go", "EmitBatch", "", `{}`,
	)
	ambiguous.Tier = store.TierUnresolved
	putCatalogAssertion(st, ambiguous, ambiguousResolution)

	options := catalogOptions(st, "user:member", nil)
	options.ProofBundles = st
	serviceAPI := api.NewContractCatalogService(options)

	list, err := serviceAPI.List(
		context.Background(), api.ContractCatalogQuery{Protocol: "thrift"}, 50, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || list.Items[0].Protocol != "thrift" ||
		list.Items[1].Protocol != "thrift" {
		t.Fatalf("thrift list = %+v", list.Items)
	}
	protoFiltered, err := serviceAPI.List(
		context.Background(), api.ContractCatalogQuery{Protocol: "protobuf"}, 50, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(protoFiltered.Items) != 0 {
		t.Fatalf("protobuf filter leaked thrift rows: %+v", protoFiltered.Items)
	}

	detail, err := serviceAPI.Operation(
		context.Background(), idlRepo, lineage, "/"+service+"/emitBatch",
	)
	if err != nil {
		t.Fatal(err)
	}
	if detail.FactDetail.Request.Declaration != "agent.Agent.emitBatch_args" ||
		detail.Request.State != "resolved" ||
		detail.Response.State != "resolved" ||
		len(detail.Response.Fields) != 1 ||
		detail.Response.Fields[0].FieldNumber != 0 ||
		detail.Response.Fields[0].Detail.Name != "success" {
		t.Fatalf("thrift operation shape = %+v", detail)
	}
	if len(detail.Implementations) != 1 ||
		detail.Implementations[0].Classification != "unresolved_name_match" ||
		len(detail.Callers) != 1 ||
		len(detail.UnresolvedCandidates) != 1 ||
		detail.UnresolvedCandidates[0].Classification != "extractor_abstention" {
		t.Fatalf("thrift relationships = %+v / %+v / %+v",
			detail.Implementations, detail.Callers, detail.UnresolvedCandidates)
	}
}

func TestContractCatalogProtocolMajorPagination(t *testing.T) {
	const (
		repo          = "github.com/acme/mixed"
		protoLineage  = "proto_lineage"
		thriftLineage = "thrift_lineage"
	)
	protoRun := catalogRun(repo, catalogProtoDomain, "run-proto", 1)
	thriftRun := catalogRun(repo, "thrift-contract", "run-thrift", 1)
	st := &proofAPIStore{
		repos: []store.Repo{{Name: repo, IndexedCommitHash: catalogCommit}},
		runs: map[string]store.ExtractionRun{
			proofScope(repo, catalogProtoDomain): protoRun,
			proofScope(repo, "thrift-contract"):  thriftRun,
		},
		assertions:  map[string][]store.Assertion{},
		resolutions: map[string]store.EvidenceResolution{},
		bundles:     map[string]store.ProofBundleRecord{},
	}
	protoService, protoResolution := catalogAssertion(
		repo, protoRun.ID, "psvc", "DECLARES_SERVICE", "a.proto", "demo.P", protoLineage,
		`{"schema":"proto-service-detail-v1","name":"P"}`,
	)
	putCatalogAssertion(st, protoService, protoResolution)
	thriftService, thriftResolution := catalogAssertion(
		repo, thriftRun.ID, "tsvc", "DECLARES_SERVICE", "a.thrift", "agent.T", thriftLineage,
		`{"schema":"thrift-service-detail-v1","name":"T"}`,
	)
	putCatalogAssertion(st, thriftService, thriftResolution)

	serviceAPI := api.NewContractCatalogService(catalogOptions(st, "user:member", nil))
	first, err := serviceAPI.List(context.Background(), api.ContractCatalogQuery{}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].Protocol != "protobuf" ||
		first.Pagination.Complete || first.Pagination.NextCursor == "" {
		t.Fatalf("first page = %+v %+v", first.Items, first.Pagination)
	}
	second, err := serviceAPI.List(
		context.Background(), api.ContractCatalogQuery{}, 1, first.Pagination.NextCursor,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Protocol != "thrift" {
		t.Fatalf("second page crossed the pack boundary wrong: %+v", second.Items)
	}
	if !second.Pagination.Complete && second.Pagination.NextCursor == "" {
		t.Fatalf("second pagination = %+v", second.Pagination)
	}
}

func TestContractCatalogBoundsAndPublicationRace(t *testing.T) {
	t.Run("input and field bounds", func(t *testing.T) {
		const (
			repo    = "github.com/acme/large-contract"
			lineage = "contract_large"
		)
		protoRun := catalogRun(repo, catalogProtoDomain, "run-large", 103)
		st := &proofAPIStore{
			repos: []store.Repo{{Name: repo, IndexedCommitHash: catalogCommit}},
			runs: map[string]store.ExtractionRun{
				proofScope(repo, catalogProtoDomain): protoRun,
			},
			assertions:  map[string][]store.Assertion{},
			resolutions: map[string]store.EvidenceResolution{},
		}
		operationDetail := `{"schema":"proto-operation-detail-v1","request":{"raw":"Request","kind":"message","resolution":"same_file","declaration":"demo.Request"},"response":{"raw":"string","kind":"scalar","resolution":"intrinsic"},"client_streaming":false,"server_streaming":false}`
		for _, row := range []struct {
			id, predicate, object, detail string
		}{
			{"op", "DECLARES_OPERATION", "demo.Large/Get", operationDetail},
			{"message", "DECLARES_MESSAGE", "demo.Request", `{"schema":"proto-message-detail-v1","name":"Request"}`},
		} {
			assertion, resolution := catalogAssertion(
				repo, protoRun.ID, row.id, row.predicate, "proto/large.proto",
				row.object, lineage, row.detail,
			)
			putCatalogAssertion(st, assertion, resolution)
		}
		for number := 1; number <= 101; number++ {
			id := fmt.Sprintf("field-%03d", number)
			detail := fmt.Sprintf(
				`{"schema":"proto-field-detail-v2","name":"f%d","type":{"raw":"string","kind":"scalar","resolution":"intrinsic"},"cardinality":"singular"}`,
				number,
			)
			assertion, resolution := catalogAssertion(
				repo, protoRun.ID, id, "DECLARES_FIELD", "proto/large.proto",
				fmt.Sprintf("demo.Request#%d", number), lineage, detail,
			)
			putCatalogAssertion(st, assertion, resolution)
		}
		serviceAPI := api.NewContractCatalogService(catalogOptions(st, "user:member", nil))
		_, err := serviceAPI.List(context.Background(), api.ContractCatalogQuery{}, 101, "")
		requireCatalogStatus(t, err, http.StatusBadRequest)
		// T19.4: thrift is a registered protocol; unregistered ones still
		// reject before any repository or evidence read.
		_, err = serviceAPI.List(
			context.Background(),
			api.ContractCatalogQuery{Protocol: "smoke-signals"},
			10,
			"",
		)
		requireCatalogStatus(t, err, http.StatusBadRequest)
		if len(st.calls) != 0 {
			t.Fatalf("invalid catalog input touched repository or evidence state: %v", st.calls)
		}
		detail, err := serviceAPI.Operation(
			context.Background(), repo, lineage, "/demo.Large/Get",
		)
		if err != nil {
			t.Fatal(err)
		}
		if !detail.ShapeTruncated || !detail.Request.Truncated ||
			len(detail.Request.Fields) != 100 ||
			!containsString(detail.Request.TruncationReasons, "fields_per_message_limit") {
			t.Fatalf("field bound was not explicit: %+v", detail.Request)
		}
	})

	t.Run("publication race retries without mixing runs", func(t *testing.T) {
		const repo = "github.com/acme/racing-contract"
		oldRun := catalogRun(repo, catalogProtoDomain, "run-old", 1)
		newRun := catalogRun(repo, catalogProtoDomain, "run-new", 1)
		st := &proofAPIStore{
			repos: []store.Repo{{Name: repo, IndexedCommitHash: catalogCommit}},
			runs: map[string]store.ExtractionRun{
				proofScope(repo, catalogProtoDomain): oldRun,
			},
			assertions:  map[string][]store.Assertion{},
			resolutions: map[string]store.EvidenceResolution{},
		}
		for _, run := range []store.ExtractionRun{oldRun, newRun} {
			assertion, resolution := catalogAssertion(
				repo, run.ID, "service-"+run.ID, "DECLARES_SERVICE",
				"proto/catalog.proto", "demo.Catalog", "lineage-"+run.ID,
				`{"schema":"proto-service-detail-v1","name":"Catalog"}`,
			)
			putCatalogAssertion(st, assertion, resolution)
		}
		swapped := false
		st.onAssertions = func(query store.AssertionQuery) {
			if !swapped && query.RunID == oldRun.ID {
				swapped = true
				st.runs[proofScope(repo, catalogProtoDomain)] = newRun
			}
		}
		serviceAPI := api.NewContractCatalogService(catalogOptions(st, "user:member", nil))
		result, err := serviceAPI.List(context.Background(), api.ContractCatalogQuery{}, 10, "")
		if err != nil {
			t.Fatal(err)
		}
		if !swapped || len(result.Items) != 1 ||
			result.Items[0].Declaration.RunID != newRun.ID ||
			result.CoverageDigest != result.Coverage.Digest {
			t.Fatalf("mixed or unretried projection: %+v", result)
		}
	})

	t.Run("authorization race fails closed", func(t *testing.T) {
		const repo = "github.com/acme/revoked-contract"
		protoRun := catalogRun(repo, catalogProtoDomain, "run-revoked", 1)
		st := &proofAPIStore{
			repos: []store.Repo{{Name: repo, IndexedCommitHash: catalogCommit}},
			runs: map[string]store.ExtractionRun{
				proofScope(repo, catalogProtoDomain): protoRun,
			},
			assertions:  map[string][]store.Assertion{},
			resolutions: map[string]store.EvidenceResolution{},
		}
		assertion, resolution := catalogAssertion(
			repo, protoRun.ID, "service", "DECLARES_SERVICE",
			"proto/catalog.proto", "demo.Catalog", "lineage",
			`{"schema":"proto-service-detail-v1","name":"Catalog"}`,
		)
		putCatalogAssertion(st, assertion, resolution)
		visible := map[string]bool{repo: true}
		st.onAssertions = func(store.AssertionQuery) {
			visible[repo] = false
		}
		serviceAPI := api.NewContractCatalogService(
			catalogOptions(st, "user:member", &visible),
		)
		_, err := serviceAPI.List(
			context.Background(), api.ContractCatalogQuery{}, 10, "",
		)
		requireCatalogStatus(t, err, http.StatusConflict)
	})

	t.Run("assertion scan continuation", func(t *testing.T) {
		const repo = "github.com/acme/wide-contract"
		protoRun := catalogRun(repo, catalogProtoDomain, "run-wide", 502)
		st := &proofAPIStore{
			repos: []store.Repo{{Name: repo, IndexedCommitHash: catalogCommit}},
			runs: map[string]store.ExtractionRun{
				proofScope(repo, catalogProtoDomain): protoRun,
			},
			assertions:  map[string][]store.Assertion{repo: {}},
			resolutions: map[string]store.EvidenceResolution{},
		}
		for index := 0; index < 501; index++ {
			st.assertions[repo] = append(st.assertions[repo], store.Assertion{
				ID:        fmt.Sprintf("noise-%03d", index),
				Predicate: "AAA_NOISE", Subject: "proto/noise.proto",
				Object: fmt.Sprintf("noise-%03d", index),
				Tier:   store.TierExact, Repo: repo, RunID: protoRun.ID,
			})
		}
		declaration, resolution := catalogAssertion(
			repo, protoRun.ID, "service", "DECLARES_SERVICE",
			"proto/catalog.proto", "demo.Catalog", "lineage",
			`{"schema":"proto-service-detail-v1","name":"Catalog"}`,
		)
		putCatalogAssertion(st, declaration, resolution)
		serviceAPI := api.NewContractCatalogService(catalogOptions(st, "user:member", nil))
		first, err := serviceAPI.List(
			context.Background(), api.ContractCatalogQuery{}, 10, "",
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Items) != 0 || first.Pagination.NextCursor == "" ||
			first.Pagination.Reason != "assertion_scan_limit" ||
			first.Pagination.Complete {
			t.Fatalf("scan bound was not explicit: %+v", first.Pagination)
		}
		second, err := serviceAPI.List(
			context.Background(), api.ContractCatalogQuery{}, 10,
			first.Pagination.NextCursor,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(second.Items) != 1 ||
			second.Items[0].Declaration.AssertionID != "service" ||
			!second.Pagination.Complete {
			t.Fatalf("scan continuation lost or duplicated rows: %+v", second)
		}
	})

	t.Run("relationship row bound", func(t *testing.T) {
		const (
			contractRepo = "github.com/acme/bounded-contract"
			consumerRepo = "github.com/acme/many-consumers"
			lineage      = "contract_bounded"
		)
		protoRun := catalogRun(contractRepo, catalogProtoDomain, "run-bounded-proto", 3)
		grpcRun := catalogRun(consumerRepo, catalogGRPCDomain, "run-many-grpc", 201)
		st := &proofAPIStore{
			repos: []store.Repo{
				{Name: contractRepo, IndexedCommitHash: catalogCommit},
				{Name: consumerRepo, IndexedCommitHash: catalogCommit},
			},
			runs: map[string]store.ExtractionRun{
				proofScope(contractRepo, catalogProtoDomain): protoRun,
				proofScope(consumerRepo, catalogGRPCDomain):  grpcRun,
			},
			assertions:  map[string][]store.Assertion{},
			resolutions: map[string]store.EvidenceResolution{},
		}
		for _, row := range []struct {
			id, predicate, object, detail string
		}{
			{
				"operation", "DECLARES_OPERATION", "demo.Bounded/Get",
				`{"schema":"proto-operation-detail-v1","request":{"raw":"string","kind":"scalar","resolution":"intrinsic"},"response":{"raw":"string","kind":"scalar","resolution":"intrinsic"},"client_streaming":false,"server_streaming":false}`,
			},
		} {
			assertion, resolution := catalogAssertion(
				contractRepo, protoRun.ID, row.id, row.predicate,
				"proto/bounded.proto", row.object, lineage, row.detail,
			)
			putCatalogAssertion(st, assertion, resolution)
		}
		for index := 0; index < 201; index++ {
			id := fmt.Sprintf("caller-%03d", index)
			assertion, resolution := catalogAssertion(
				consumerRepo, grpcRun.ID, id, "CALLS_OPERATION",
				"go/"+id+".go", "/demo.Bounded/Get", "grpc-lineage", `{}`,
			)
			assertion.Tier = store.TierHeuristic
			putCatalogAssertion(st, assertion, resolution)
		}
		serviceAPI := api.NewContractCatalogService(catalogOptions(st, "user:member", nil))
		detail, err := serviceAPI.Operation(
			context.Background(), contractRepo, lineage, "/demo.Bounded/Get",
		)
		if err != nil {
			t.Fatal(err)
		}
		if !detail.RelationshipsTruncated ||
			detail.RelationshipLimitReason != "relationship_row_limit" ||
			len(detail.Callers) != 200 {
			t.Fatalf("relationship bound was not explicit: truncated=%t reason=%q rows=%d",
				detail.RelationshipsTruncated,
				detail.RelationshipLimitReason,
				len(detail.Callers))
		}
	})

	t.Run("message depth and node bounds", func(t *testing.T) {
		const (
			repo    = "github.com/acme/recursive-contract"
			lineage = "contract_recursive"
		)
		protoRun := catalogRun(repo, catalogProtoDomain, "run-recursive", 1)
		st := &proofAPIStore{
			repos: []store.Repo{{Name: repo, IndexedCommitHash: catalogCommit}},
			runs: map[string]store.ExtractionRun{
				proofScope(repo, catalogProtoDomain): protoRun,
			},
			assertions:  map[string][]store.Assertion{},
			resolutions: map[string]store.EvidenceResolution{},
		}
		add := func(id, predicate, object, detail string) {
			assertion, resolution := catalogAssertion(
				repo, protoRun.ID, id, predicate, "proto/recursive.proto",
				object, lineage, detail,
			)
			putCatalogAssertion(st, assertion, resolution)
		}
		add(
			"operation-depth", "DECLARES_OPERATION", "demo.Deep/Get",
			`{"schema":"proto-operation-detail-v1","request":{"raw":"M0","kind":"message","resolution":"same_file","declaration":"demo.M0"},"response":{"raw":"string","kind":"scalar","resolution":"intrinsic"},"client_streaming":false,"server_streaming":false}`,
		)
		for index := 0; index < 8; index++ {
			message := fmt.Sprintf("demo.M%d", index)
			add(
				fmt.Sprintf("message-depth-%d", index),
				"DECLARES_MESSAGE", message,
				fmt.Sprintf(`{"schema":"proto-message-detail-v1","name":"M%d"}`, index),
			)
			if index < 7 {
				add(
					fmt.Sprintf("field-depth-%d", index),
					"DECLARES_FIELD", message+"#1",
					fmt.Sprintf(
						`{"schema":"proto-field-detail-v2","name":"next","type":{"raw":"M%d","kind":"message","resolution":"same_file","declaration":"demo.M%d"},"cardinality":"singular"}`,
						index+1, index+1,
					),
				)
			}
		}
		protoRun.Coverage.AssertionCount = len(st.assertions[repo])
		protoRun.Coverage.AtomCount = len(st.assertions[repo])
		st.runs[proofScope(repo, catalogProtoDomain)] = protoRun
		serviceAPI := api.NewContractCatalogService(catalogOptions(st, "user:member", nil))
		depth, err := serviceAPI.Operation(
			context.Background(), repo, lineage, "/demo.Deep/Get",
		)
		if err != nil {
			t.Fatal(err)
		}
		if !depth.ShapeTruncated ||
			!containsString(depth.Request.TruncationReasons, "message_depth_limit") {
			t.Fatalf("depth bound was not explicit: %+v", depth.Request)
		}

		nodeRun := catalogRun(repo, catalogProtoDomain, "run-node", 1)
		st.runs[proofScope(repo, catalogProtoDomain)] = nodeRun
		st.assertions[repo] = nil
		st.resolutions = map[string]store.EvidenceResolution{}
		add = func(id, predicate, object, detail string) {
			assertion, resolution := catalogAssertion(
				repo, nodeRun.ID, id, predicate, "proto/nodes.proto",
				object, lineage, detail,
			)
			putCatalogAssertion(st, assertion, resolution)
		}
		add(
			"operation-node", "DECLARES_OPERATION", "demo.Wide/Get",
			`{"schema":"proto-operation-detail-v1","request":{"raw":"Root","kind":"message","resolution":"same_file","declaration":"demo.Root"},"response":{"raw":"string","kind":"scalar","resolution":"intrinsic"},"client_streaming":false,"server_streaming":false}`,
		)
		add(
			"message-root", "DECLARES_MESSAGE", "demo.Root",
			`{"schema":"proto-message-detail-v1","name":"Root"}`,
		)
		for child := 1; child <= 100; child++ {
			childName := fmt.Sprintf("demo.Child%03d", child)
			add(
				fmt.Sprintf("root-field-%03d", child),
				"DECLARES_FIELD", fmt.Sprintf("demo.Root#%d", child),
				fmt.Sprintf(
					`{"schema":"proto-field-detail-v2","name":"child%d","type":{"raw":"Child%d","kind":"message","resolution":"same_file","declaration":"%s"},"cardinality":"singular"}`,
					child, child, childName,
				),
			)
			add(
				fmt.Sprintf("child-message-%03d", child),
				"DECLARES_MESSAGE", childName,
				fmt.Sprintf(`{"schema":"proto-message-detail-v1","name":"Child%d"}`, child),
			)
			for field := 1; field <= 2; field++ {
				add(
					fmt.Sprintf("child-%03d-field-%d", child, field),
					"DECLARES_FIELD", fmt.Sprintf("%s#%d", childName, field),
					fmt.Sprintf(
						`{"schema":"proto-field-detail-v2","name":"value%d","type":{"raw":"string","kind":"scalar","resolution":"intrinsic"},"cardinality":"singular"}`,
						field,
					),
				)
			}
		}
		nodeRun.Coverage.AssertionCount = len(st.assertions[repo])
		nodeRun.Coverage.AtomCount = len(st.assertions[repo])
		st.runs[proofScope(repo, catalogProtoDomain)] = nodeRun
		nodes, err := serviceAPI.Operation(
			context.Background(), repo, lineage, "/demo.Wide/Get",
		)
		if err != nil {
			t.Fatal(err)
		}
		if !nodes.ShapeTruncated ||
			!containsString(nodes.Request.TruncationReasons, "message_node_limit") {
			t.Fatalf("node bound was not explicit: %+v", nodes.Request.TruncationReasons)
		}
	})
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}
