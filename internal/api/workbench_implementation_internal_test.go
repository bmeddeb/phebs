package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	implementationInvestigationID = "01J000000000000000000000218"
	implementationRevisionID      = "ivr_t218"
	implementationRepository      = "github.com/acme/cart"
	implementationCommit          = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	implementationOlderCommit     = "1111111111111111111111111111111111111111"
	implementationNewestCommit    = "2222222222222222222222222222222222222222"
	implementationParentCommit    = "3333333333333333333333333333333333333333"
)

type implementationRepoStore struct {
	store.Store
	repositories []store.Repo
}

func (fake *implementationRepoStore) ListRepos(
	context.Context,
) ([]store.Repo, error) {
	return slices.Clone(fake.repositories), nil
}

type implementationWorkbenchFake struct {
	view *store.WorkbenchView
}

func (fake *implementationWorkbenchFake) Read(
	_ context.Context,
	principal, investigationID string,
) (*store.WorkbenchView, error) {
	if fake.view == nil ||
		principal != "user:t218" ||
		investigationID != implementationInvestigationID {
		return nil, store.ErrNotFound
	}
	value := *fake.view
	return &value, nil
}

type implementationCatalogFake struct {
	detail *ContractCatalogOperation
	flip   bool
	calls  int
}

func (fake *implementationCatalogFake) OperationForProtocol(
	_ context.Context,
	protocol, repository, lineage, operation string,
) (*ContractCatalogOperation, error) {
	fake.calls++
	if fake.detail == nil ||
		fake.detail.Protocol != protocol ||
		fake.detail.Repository != repository ||
		fake.detail.DeclarationLineage != lineage ||
		fake.detail.Operation != operation {
		return nil, store.ErrNotFound
	}
	encoded, _ := json.Marshal(fake.detail)
	var detail ContractCatalogOperation
	_ = json.Unmarshal(encoded, &detail)
	detail.Protocol = fake.detail.Protocol
	if fake.flip {
		slices.Reverse(detail.Declaration.Sources)
		slices.Reverse(detail.Implementations)
		for index := range detail.Implementations {
			slices.Reverse(detail.Implementations[index].Claim.Sources)
		}
	}
	fake.flip = !fake.flip
	return &detail, nil
}

type implementationSourceCall struct {
	repository string
	commit     string
	path       string
}

type implementationSourceFake struct {
	files map[string][]byte
	calls []implementationSourceCall
}

func (fake *implementationSourceFake) ReadSource(
	_ context.Context,
	repository, commit, filePath string,
) ([]byte, error) {
	fake.calls = append(fake.calls, implementationSourceCall{
		repository: repository,
		commit:     commit,
		path:       filePath,
	})
	content, ok := fake.files[repository+"|"+commit+"|"+filePath]
	if !ok {
		return nil, store.ErrNotFound
	}
	return slices.Clone(content), nil
}

type implementationSearchFake struct {
	files []search.FileResult
	flip  bool
	calls []string
	opts  []search.Options
}

func (fake *implementationSearchFake) Search(
	_ context.Context,
	raw string,
	opts search.Options,
) (*search.Result, error) {
	fake.calls = append(fake.calls, raw)
	fake.opts = append(fake.opts, opts)
	files := slices.Clone(fake.files)
	if fake.flip {
		slices.Reverse(files)
	}
	fake.flip = !fake.flip
	return &search.Result{
		Files: files,
		Stats: search.Stats{
			MatchCount: len(files),
			FileCount:  len(files),
		},
	}, nil
}

type implementationNavFake struct {
	available  bool
	definition codenav.Location
	references []codenav.Location
	flip       bool
	queries    []codenav.Query
}

func (fake *implementationNavFake) Definition(
	_ context.Context,
	query codenav.Query,
) (codenav.DefinitionResult, error) {
	fake.queries = append(fake.queries, query)
	if !fake.available {
		return codenav.DefinitionResult{Available: false}, nil
	}
	location := fake.definition
	return codenav.DefinitionResult{
		Available: true,
		Symbol:    "scip-go gomod github.com/acme/cart Cart#Get().",
		Location:  &location,
	}, nil
}

func (fake *implementationNavFake) References(
	_ context.Context,
	query codenav.Query,
) (codenav.ReferencesResult, error) {
	fake.queries = append(fake.queries, query)
	if !fake.available {
		return codenav.ReferencesResult{
			Available: false,
			Locations: []codenav.Location{},
		}, nil
	}
	locations := slices.Clone(fake.references)
	if fake.flip {
		slices.Reverse(locations)
	}
	fake.flip = !fake.flip
	return codenav.ReferencesResult{
		Available: true,
		Symbol:    "scip-go gomod github.com/acme/cart Cart#Get().",
		Locations: locations,
	}, nil
}

type implementationHistoryFake struct {
	flip        bool
	commitCalls []phebssync.CommitListRequest
	diffCalls   []phebssync.DiffRequest
}

func (fake *implementationHistoryFake) Commits(
	_ context.Context,
	request phebssync.CommitListRequest,
) (phebssync.CommitListResult, error) {
	fake.commitCalls = append(fake.commitCalls, request)
	commits := []phebssync.GitCommit{
		{
			ID:        implementationNewestCommit,
			ParentIDs: []string{implementationParentCommit},
			Subject:   "new handler",
			Author: phebssync.GitIdentity{
				Name: "Ada",
				Time: time.Date(
					2026, time.July, 26, 10, 0, 0, 0, time.UTC,
				),
			},
		},
		{
			ID:        implementationOlderCommit,
			ParentIDs: []string{implementationParentCommit},
			Subject:   "old handler",
			Author: phebssync.GitIdentity{
				Name: "Ben",
				Time: time.Date(
					2026, time.July, 25, 10, 0, 0, 0, time.UTC,
				),
			},
		},
	}
	if fake.flip {
		slices.Reverse(commits)
	}
	fake.flip = !fake.flip
	return phebssync.CommitListResult{
		Revision: request.Ref,
		Commits:  commits,
	}, nil
}

func (fake *implementationHistoryFake) Diff(
	_ context.Context,
	request phebssync.DiffRequest,
) (phebssync.DiffResult, error) {
	fake.diffCalls = append(fake.diffCalls, request)
	return phebssync.DiffResult{
		Base:  implementationParentCommit,
		Head:  request.Head,
		Patch: "diff --git a/" + request.Path + " b/" + request.Path,
		Files: []phebssync.GitFileChange{{
			Status: "M",
			Path:   request.Path,
		}},
	}, nil
}

func TestWorkbenchImplementationServiceDeterministicCompositionAndPaging(
	t *testing.T,
) {
	service, source, searcher, nav, history := implementationService(t)
	request := WorkbenchImplementationRequest{
		InvestigationID: implementationInvestigationID,
		RevisionID:      implementationRevisionID,
		PageSize:        3,
	}
	first, err := service.Read(context.Background(), "user:t218", request)
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}
	second, err := service.Read(context.Background(), "user:t218", request)
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf(
			"equal semantic state changed response bytes\nfirst: %s\nsecond: %s",
			firstJSON,
			secondJSON,
		)
	}
	if first.Pagination.Complete ||
		first.Pagination.NextCursor == "" ||
		first.Pagination.TotalRows <= len(first.Rows) {
		t.Fatalf("unexpected first page pagination: %+v", first.Pagination)
	}
	request.Cursor = first.Pagination.NextCursor
	next, err := service.Read(context.Background(), "user:t218", request)
	if err != nil {
		t.Fatalf("next Read: %v", err)
	}
	if len(next.Rows) == 0 || next.Rows[0].ID == first.Rows[0].ID {
		t.Fatalf("cursor did not advance: first=%+v next=%+v", first.Rows, next.Rows)
	}

	all, err := service.Read(
		context.Background(),
		"user:t218",
		WorkbenchImplementationRequest{
			InvestigationID: implementationInvestigationID,
			RevisionID:      implementationRevisionID,
			PageSize:        workbenchImplementationMaxPage,
		},
	)
	if err != nil {
		t.Fatalf("all Read: %v", err)
	}
	roles := map[string]bool{}
	kinds := map[string]bool{}
	for _, row := range all.Rows {
		roles[row.CodeRole] = true
		kinds[row.Kind] = true
		if row.SelectionRule == "" || row.SelectionInput == "" {
			t.Fatalf("row lacks deterministic selection attribution: %+v", row)
		}
		switch row.ReviewState {
		case "selected":
			if row.Kind != "declaration" &&
				row.Kind != "implementation" &&
				row.Kind != "explicit_anchor" {
				t.Fatalf("non-seed row marked selected: %+v", row)
			}
		case "review_candidate":
		default:
			t.Fatalf("unexpected review state: %+v", row)
		}
	}
	for _, role := range []string{
		workbenchImplementationRoleProduction,
		workbenchImplementationRoleTest,
		workbenchImplementationRoleMock,
		workbenchImplementationRoleGenerated,
		workbenchImplementationRoleVendor,
		workbenchImplementationRoleDocumentation,
	} {
		if !roles[role] {
			t.Errorf("missing visible %s role", role)
		}
	}
	for _, kind := range []string{
		"declaration",
		"implementation",
		"search_candidate",
		"definition",
		"reference",
		"history_commit",
		"history_diff",
	} {
		if !kinds[kind] {
			t.Errorf("missing %s row", kind)
		}
	}
	generatedBoundary := false
	for _, row := range all.Rows {
		if row.CodeRole == workbenchImplementationRoleGenerated &&
			row.Boundary == "generated" {
			generatedBoundary = true
		}
	}
	if !generatedBoundary {
		t.Error("generated source did not retain its visible boundary")
	}
	for _, call := range source.calls {
		if call.commit != implementationCommit {
			t.Errorf("source read mutable or unexpected commit: %+v", call)
		}
	}
	for _, query := range nav.queries {
		if query.Revision != implementationCommit {
			t.Errorf("SCIP read mutable or unexpected revision: %+v", query)
		}
	}
	for _, call := range history.commitCalls {
		if call.Ref != implementationCommit {
			t.Errorf("history list used mutable or unexpected ref: %+v", call)
		}
	}
	for _, call := range history.diffCalls {
		if call.Head != implementationOlderCommit ||
			call.Base != "" {
			t.Errorf("history diff did not use selected immutable commit: %+v", call)
		}
	}
	for _, raw := range searcher.calls {
		if strings.Contains(raw, "HEAD") {
			t.Errorf("search query used mutable HEAD: %q", raw)
		}
	}
}

func TestWorkbenchImplementationServiceUnsupportedReadersAreGaps(
	t *testing.T,
) {
	service, _, _, _, _ := implementationService(t)
	service.search = nil
	service.codeNav = nil
	service.history = nil
	page, err := service.Read(
		context.Background(),
		"user:t218",
		WorkbenchImplementationRequest{
			InvestigationID: implementationInvestigationID,
			RevisionID:      implementationRevisionID,
			PageSize:        workbenchImplementationMaxPage,
		},
	)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, capability := range []string{"source_search", "scip", "history"} {
		found := false
		for _, gap := range page.Gaps {
			if gap.Capability == capability && gap.State == "unsupported" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing explicit %s unsupported gap: %+v", capability, page.Gaps)
		}
	}
	for _, row := range page.Rows {
		if row.Kind == "definition" ||
			row.Kind == "reference" ||
			row.Kind == "history_commit" ||
			row.Kind == "history_diff" {
			t.Fatalf("unsupported reader produced inferred row: %+v", row)
		}
	}
}

func TestWorkbenchImplementationServiceCursorRejectsSnapshotDrift(
	t *testing.T,
) {
	service, _, searcher, _, _ := implementationService(t)
	request := WorkbenchImplementationRequest{
		InvestigationID: implementationInvestigationID,
		RevisionID:      implementationRevisionID,
		PageSize:        1,
	}
	first, err := service.Read(context.Background(), "user:t218", request)
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}
	if first.Pagination.NextCursor == "" {
		t.Fatal("first page did not produce a cursor")
	}
	searcher.files[0].Path = "docs/cart-changed.md"
	request.Cursor = first.Pagination.NextCursor
	_, err = service.Read(context.Background(), "user:t218", request)
	assertWorkbenchImplementationStatus(t, err, http.StatusConflict)
}

func TestWorkbenchImplementationServiceRejectsMutableReaderResultsAndPins(
	t *testing.T,
) {
	service, _, searcher, _, _ := implementationService(t)
	searcher.files[0].Ref = "HEAD"
	_, err := service.Read(
		context.Background(),
		"user:t218",
		WorkbenchImplementationRequest{
			InvestigationID: implementationInvestigationID,
			RevisionID:      implementationRevisionID,
			PageSize:        workbenchImplementationMaxPage,
		},
	)
	assertWorkbenchImplementationStatus(t, err, http.StatusConflict)

	service, source, _, _, _ := implementationService(t)
	before := len(source.calls)
	_, err = service.Read(
		context.Background(),
		"user:t218",
		WorkbenchImplementationRequest{
			InvestigationID: implementationInvestigationID,
			RevisionID:      implementationRevisionID,
			Anchors: []WorkbenchImplementationAnchor{{
				Repository: implementationRepository,
				Commit:     strings.Repeat("b", 40),
				Path:       "internal/cart/handler.go",
				Encoding:   codenav.EncodingUTF8,
			}},
			PageSize: workbenchImplementationMaxPage,
		},
	)
	assertWorkbenchImplementationStatus(t, err, http.StatusConflict)
	if len(source.calls) != before {
		t.Fatalf("mismatched explicit pin reached source reader: %+v", source.calls[before:])
	}
}

func TestWorkbenchImplementationServiceHiddenSearchRowsDoNotAffectBytes(
	t *testing.T,
) {
	clean, _, cleanSearch, _, _ := implementationService(t)
	withHidden, _, hiddenSearch, _, _ := implementationService(t)
	hiddenRepository := "github.com/secret/cart"
	repositories := withHidden.opts.Store.(*implementationRepoStore).repositories
	withHidden.opts.Store.(*implementationRepoStore).repositories = append(
		repositories,
		store.Repo{
			Name:              hiddenRepository,
			IndexedCommitHash: strings.Repeat("9", 40),
		},
	)
	withHidden.opts.Visible = func(context.Context) func(store.Repo) bool {
		return func(repository store.Repo) bool {
			return repository.Name != hiddenRepository
		}
	}
	hiddenSearch.files = append(
		hiddenSearch.files,
		search.FileResult{
			Repo: hiddenRepository,
			Ref:  strings.Repeat("9", 40),
			Path: "secret/handler.go",
		},
	)
	request := WorkbenchImplementationRequest{
		InvestigationID: implementationInvestigationID,
		RevisionID:      implementationRevisionID,
		PageSize:        workbenchImplementationMaxPage,
	}
	cleanPage, err := clean.Read(context.Background(), "user:t218", request)
	if err != nil {
		t.Fatalf("clean Read: %v", err)
	}
	hiddenPage, err := withHidden.Read(context.Background(), "user:t218", request)
	if err != nil {
		t.Fatalf("hidden Read: %v", err)
	}
	cleanJSON, _ := json.Marshal(cleanPage)
	hiddenJSON, _ := json.Marshal(hiddenPage)
	if string(cleanJSON) != string(hiddenJSON) {
		t.Fatalf(
			"hidden search row changed response bytes\nclean: %s\nhidden: %s",
			cleanJSON,
			hiddenJSON,
		)
	}
	if len(cleanSearch.calls) != len(hiddenSearch.calls) {
		t.Fatalf(
			"hidden row changed bounded query count: clean=%d hidden=%d",
			len(cleanSearch.calls),
			len(hiddenSearch.calls),
		)
	}
}

func TestWorkbenchImplementationServiceHiddenSelectedTargetStopsBeforeReaders(
	t *testing.T,
) {
	service, source, _, _, _ := implementationService(t)
	catalog := service.catalog.(*implementationCatalogFake)
	service.opts.Visible = func(context.Context) func(store.Repo) bool {
		return func(store.Repo) bool { return false }
	}
	_, err := service.Read(
		context.Background(),
		"user:t218",
		WorkbenchImplementationRequest{
			InvestigationID: implementationInvestigationID,
			RevisionID:      implementationRevisionID,
			PageSize:        workbenchImplementationMaxPage,
		},
	)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden target error = %v, want not found", err)
	}
	if catalog.calls != 0 || len(source.calls) != 0 {
		t.Fatalf(
			"hidden selection reached catalog/source: catalog=%d source=%+v",
			catalog.calls,
			source.calls,
		)
	}
}

func TestWorkbenchImplementationServiceExplicitAnchorIsSelectedAndCited(
	t *testing.T,
) {
	service, _, _, _, _ := implementationService(t)
	page, err := service.Read(
		context.Background(),
		"user:t218",
		WorkbenchImplementationRequest{
			InvestigationID: implementationInvestigationID,
			RevisionID:      implementationRevisionID,
			Anchors: []WorkbenchImplementationAnchor{{
				Repository: implementationRepository,
				Commit:     implementationCommit,
				Path:       "docs/cart.md",
				Line:       2,
				Character:  3,
				Encoding:   codenav.EncodingUTF8,
			}},
			PageSize: workbenchImplementationMaxPage,
		},
	)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, row := range page.Rows {
		if row.Kind == "explicit_anchor" {
			if row.SelectionRule != "explicit_user_pin_v1" ||
				row.ReviewState != "selected" ||
				row.CodeRole != workbenchImplementationRoleDocumentation ||
				row.Source.StartLine != 3 ||
				row.Source.StartColumn != 4 ||
				row.Source.ContentDigest == "" {
				t.Fatalf("explicit anchor lost its pin or citation: %+v", row)
			}
			return
		}
	}
	t.Fatal("explicit anchor row missing")
}

func TestWorkbenchImplementationServiceSearchLimitIsVisible(
	t *testing.T,
) {
	service, _, searcher, _, _ := implementationService(t)
	searcher.files = make([]search.FileResult, 0, workbenchImplementationSearchLimit+1)
	for index := range workbenchImplementationSearchLimit + 1 {
		searcher.files = append(searcher.files, search.FileResult{
			Repo: implementationRepository,
			Ref:  implementationCommit,
			Path: fmt.Sprintf("internal/cart/candidate_%03d.go", index),
		})
	}
	page, err := service.Read(
		context.Background(),
		"user:t218",
		WorkbenchImplementationRequest{
			InvestigationID: implementationInvestigationID,
			RevisionID:      implementationRevisionID,
			PageSize:        workbenchImplementationMaxPage,
		},
	)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, opts := range searcher.opts {
		if opts.MaxMatches != workbenchImplementationSearchLimit+1 {
			t.Fatalf("search MaxMatches = %d, want sentinel limit %d",
				opts.MaxMatches, workbenchImplementationSearchLimit+1)
		}
	}
	truncated := false
	for _, gap := range page.Gaps {
		if gap.Capability == "source_search" &&
			gap.State == "truncated" &&
			gap.Code == "candidate_limit" {
			truncated = true
			break
		}
	}
	if !truncated {
		t.Fatalf("per-query search limit was silent: %+v", page.Gaps)
	}
	exactService, _, exactSearcher, _, _ := implementationService(t)
	exactSearcher.files = slices.Clone(
		searcher.files[:workbenchImplementationSearchLimit],
	)
	exactPage, err := exactService.Read(
		context.Background(),
		"user:t218",
		WorkbenchImplementationRequest{
			InvestigationID: implementationInvestigationID,
			RevisionID:      implementationRevisionID,
			PageSize:        workbenchImplementationMaxPage,
		},
	)
	if err != nil {
		t.Fatalf("exact-limit Read: %v", err)
	}
	for _, gap := range exactPage.Gaps {
		if gap.Capability == "source_search" &&
			gap.State == "truncated" &&
			gap.Code == "candidate_limit" {
			t.Fatalf("exactly %d candidates were falsely truncated: %+v",
				workbenchImplementationSearchLimit, exactPage.Gaps)
		}
	}
}

func TestWorkbenchImplementationExplicitPositionValidation(
	t *testing.T,
) {
	tests := []struct {
		name      string
		content   string
		line      int32
		character int32
		encoding  codenav.PositionEncoding
		want      bool
	}{
		{name: "utf8 boundary", content: "éx\n", character: 2, encoding: codenav.EncodingUTF8, want: true},
		{name: "utf8 split", content: "éx\n", character: 1, encoding: codenav.EncodingUTF8},
		{name: "utf16 boundary", content: "💡x\n", character: 2, encoding: codenav.EncodingUTF16, want: true},
		{name: "utf16 surrogate split", content: "💡x\n", character: 1, encoding: codenav.EncodingUTF16},
		{name: "utf32 boundary", content: "💡x\n", character: 1, encoding: codenav.EncodingUTF32, want: true},
		{name: "character beyond line", content: "abc\n", character: 4, encoding: codenav.EncodingUTF8},
		{name: "line beyond file", content: "abc\n", line: 2, encoding: codenav.EncodingUTF8},
		{name: "trailing empty line", content: "abc\n", line: 1, encoding: codenav.EncodingUTF8, want: true},
		{name: "invalid utf8", content: string([]byte{0xff}), encoding: codenav.EncodingUTF8},
		{name: "invalid encoding", content: "abc", encoding: codenav.PositionEncoding("bytes")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validWorkbenchImplementationPosition(
				[]byte(test.content),
				test.line,
				test.character,
				test.encoding,
			); got != test.want {
				t.Fatalf("valid position = %t, want %t", got, test.want)
			}
		})
	}
}

func TestWorkbenchImplementationRejectsInvalidAnchorWithoutSCIP(
	t *testing.T,
) {
	service, _, _, _, _ := implementationService(t)
	service.codeNav = nil
	_, err := service.Read(
		context.Background(),
		"user:t218",
		WorkbenchImplementationRequest{
			InvestigationID: implementationInvestigationID,
			RevisionID:      implementationRevisionID,
			Anchors: []WorkbenchImplementationAnchor{{
				Repository: implementationRepository,
				Commit:     implementationCommit,
				Path:       "docs/cart.md",
				Line:       2,
				Character:  99,
				Encoding:   codenav.EncodingUTF8,
			}},
			PageSize: workbenchImplementationMaxPage,
		},
	)
	assertWorkbenchImplementationStatus(t, err, http.StatusBadRequest)
}

func implementationService(
	t *testing.T,
) (
	*WorkbenchImplementationService,
	*implementationSourceFake,
	*implementationSearchFake,
	*implementationNavFake,
	*implementationHistoryFake,
) {
	t.Helper()
	selection := store.ChangeBriefContractSelection{
		Role:               store.ChangeBriefCurrent,
		Protocol:           "protobuf",
		Repository:         implementationRepository,
		DeclarationLineage: "proto/cart.proto:shop.Cart",
		CanonicalOperation: "/shop.Cart/Get",
	}
	view := &store.WorkbenchView{
		Investigation: store.Investigation{
			ID:                implementationInvestigationID,
			Owner:             "user:t218",
			CurrentRevisionID: implementationRevisionID,
		},
		Revision: store.Revision{
			ID:              implementationRevisionID,
			InvestigationID: implementationInvestigationID,
			ContentDigest:   "sha256:" + strings.Repeat("c", 64),
		},
		Brief: store.ChangeBrief{
			ID:              "icb_t218",
			InvestigationID: implementationInvestigationID,
			RevisionID:      implementationRevisionID,
			TicketKind:      store.ChangeBriefModify,
			What: store.ChangeBriefWhat{
				Selections: []store.ChangeBriefContractSelection{selection},
			},
			ContentDigest: "sha256:" + strings.Repeat("d", 64),
		},
	}
	sourceFor := func(filePath string, start, end int) ContractCatalogSource {
		return ContractCatalogSource{
			Repository: implementationRepository,
			Commit:     implementationCommit,
			Path:       filePath,
			StartByte:  start,
			EndByte:    end,
			StartLine:  1,
			EndLine:    1,
		}
	}
	detail := &ContractCatalogOperation{
		SchemaVersion:      contractCatalogSchemaVersion,
		Protocol:           selection.Protocol,
		Repository:         selection.Repository,
		DeclarationLineage: selection.DeclarationLineage,
		ServiceFQN:         "shop.Cart",
		Method:             "Get",
		Operation:          selection.CanonicalOperation,
		Declaration: ContractCatalogClaim{
			AssertionID: "declaration",
			CodeRole:    workbenchImplementationRoleGenerated,
			Sources: []ContractCatalogSource{
				sourceFor("generated/cart.pb.go", 0, 8),
			},
		},
		Implementations: []ContractCatalogRelationship{
			{
				Kind:           "implementation",
				Classification: "resolved_implementation",
				Claim: ContractCatalogClaim{
					AssertionID: "handler",
					CodeRole:    workbenchImplementationRoleProduction,
					Sources: []ContractCatalogSource{
						sourceFor("internal/cart/handler.go", 8, 16),
					},
				},
			},
			{
				Kind:           "implementation",
				Classification: "resolved_implementation",
				Claim: ContractCatalogClaim{
					AssertionID: "mock",
					CodeRole:    workbenchImplementationRoleMock,
					Sources: []ContractCatalogSource{
						sourceFor("internal/cart/mocks/cart.go", 0, 8),
					},
				},
			},
		},
		CoverageDigest: "sha256:" + strings.Repeat("e", 64),
	}
	files := map[string][]byte{}
	for filePath, content := range map[string]string{
		"generated/cart.pb.go":        "package generated\nfunc Get() {}\n",
		"internal/cart/handler.go":    "package cart\nfunc Get() {}\n",
		"internal/cart/mocks/cart.go": "package mocks\nfunc Get() {}\n",
		"docs/cart.md":                "# Cart\n\nGet docs\n",
	} {
		files[implementationRepository+"|"+implementationCommit+"|"+filePath] =
			[]byte(content)
	}
	source := &implementationSourceFake{files: files}
	searcher := &implementationSearchFake{
		files: []search.FileResult{
			{
				Repo: implementationRepository,
				Ref:  implementationCommit,
				Path: "docs/cart.md",
				Chunks: []search.Chunk{{
					Ranges: []search.Range{{
						StartLine: 3,
						StartCol:  1,
						EndLine:   3,
						EndCol:    4,
					}},
				}},
			},
			{
				Repo: implementationRepository,
				Ref:  implementationCommit,
				Path: "internal/cart/handler_test.go",
			},
			{
				Repo: implementationRepository,
				Ref:  implementationCommit,
				Path: "vendor/acme/cart.go",
			},
			{
				Repo: implementationRepository,
				Ref:  implementationCommit,
				Path: "generated/cart.pb.go",
			},
		},
	}
	location := func(filePath string, line int32) codenav.Location {
		return codenav.Location{
			Repo:     implementationRepository,
			Revision: implementationCommit,
			Path:     filePath,
			Range: codenav.CodeRange{
				Start: codenav.CodePosition{Line: line, Character: 0},
				End:   codenav.CodePosition{Line: line, Character: 3},
			},
			Encoding: codenav.EncodingUTF8,
		}
	}
	nav := &implementationNavFake{
		available:  true,
		definition: location("internal/cart/handler.go", 1),
		references: []codenav.Location{
			location("internal/cart/mocks/cart.go", 1),
			location("docs/cart.md", 2),
		},
	}
	history := &implementationHistoryFake{}
	repoStore := &implementationRepoStore{
		repositories: []store.Repo{{
			Name:              implementationRepository,
			IndexedCommitHash: implementationCommit,
		}},
	}
	opts := Options{
		Store: repoStore,
		Principal: func(context.Context) string {
			return "user:t218"
		},
	}
	return &WorkbenchImplementationService{
		opts:      opts,
		workbench: &implementationWorkbenchFake{view: view},
		catalog:   &implementationCatalogFake{detail: detail},
		source:    source,
		search:    searcher,
		codeNav:   nav,
		history:   history,
	}, source, searcher, nav, history
}

func assertWorkbenchImplementationStatus(
	t *testing.T,
	err error,
	want int,
) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected status %d error", want)
	}
	var statusError interface{ GetStatus() int }
	if !errors.As(err, &statusError) || statusError.GetStatus() != want {
		t.Fatalf("error = %v, want HTTP %d", err, want)
	}
}

// T21.8-review regression: the production constructor must preserve the
// nilness of the typed Search/CodeNav pointers. Boxing a nil pointer into
// the interface fields makes the unsupported-capability gates unreachable
// and the first seeded Read panics inside the lower reader.
func TestNewWorkbenchImplementationServicePreservesReaderNilness(
	t *testing.T,
) {
	searchReader, codeNavReader := workbenchImplementationReaders(nil, nil)
	if searchReader != nil || codeNavReader != nil {
		t.Fatal("typed-nil readers leaked into non-nil interfaces; the unsupported gates would never fire and seeded reads would panic")
	}
	enabledSearch, enabledNav := workbenchImplementationReaders(
		&search.Searcher{}, &codenav.Service{},
	)
	if enabledSearch == nil || enabledNav == nil {
		t.Fatal("enabled readers were dropped by the nil-preserving conversion")
	}
}
