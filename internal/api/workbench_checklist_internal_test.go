package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	checklistInvestigationID = "01J00000000000000000000219"
	checklistRevisionID      = "ivr_t219"
	checklistRepository      = "github.com/acme/cart"
	checklistCommit          = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type checklistWorkbenchFake struct {
	view *store.WorkbenchView
}

func (fake *checklistWorkbenchFake) Read(
	_ context.Context,
	_, investigationID string,
) (*store.WorkbenchView, error) {
	if fake.view == nil ||
		investigationID != fake.view.Investigation.ID {
		return nil, store.ErrNotFound
	}
	value := *fake.view
	return &value, nil
}

type checklistImpactFake struct {
	page  WorkbenchImpactPage
	calls int
}

func (fake *checklistImpactFake) Read(
	_ context.Context,
	_ string,
	request WorkbenchImpactRequest,
) (*WorkbenchImpactPage, error) {
	fake.calls++
	if request.InvestigationID != fake.page.InvestigationID ||
		request.RevisionID != fake.page.RevisionID ||
		request.Cursor != "" {
		return nil, store.ErrNotFound
	}
	encoded, _ := json.Marshal(fake.page)
	var page WorkbenchImpactPage
	_ = json.Unmarshal(encoded, &page)
	return &page, nil
}

type checklistImplementationFake struct {
	page  WorkbenchImplementationPage
	calls int
}

func (fake *checklistImplementationFake) Read(
	_ context.Context,
	_ string,
	request WorkbenchImplementationRequest,
) (*WorkbenchImplementationPage, error) {
	fake.calls++
	if request.InvestigationID != fake.page.InvestigationID ||
		request.RevisionID != fake.page.RevisionID ||
		request.Cursor != "" {
		return nil, store.ErrNotFound
	}
	encoded, _ := json.Marshal(fake.page)
	var page WorkbenchImplementationPage
	_ = json.Unmarshal(encoded, &page)
	return &page, nil
}

type checklistDispositionReceipt struct {
	request store.WorkbenchDispositionRequest
	value   store.WorkbenchDisposition
}

type checklistDispositionFake struct {
	owner    string
	records  []store.WorkbenchDisposition
	receipts map[string]checklistDispositionReceipt
}

func (fake *checklistDispositionFake) ListWorkbenchDispositionsAs(
	_ context.Context,
	_, _ string,
) ([]store.WorkbenchDisposition, error) {
	return slices.Clone(fake.records), nil
}

func (fake *checklistDispositionFake) GetWorkbenchDispositionMutationAs(
	_ context.Context,
	principal,
	investigationID,
	expectedRevisionID,
	idempotencyKey string,
) (*store.WorkbenchDisposition, error) {
	if principal != fake.owner {
		return nil, store.ErrNotFound
	}
	receipt, ok := fake.receipts[principal+"|"+idempotencyKey]
	if !ok ||
		receipt.value.InvestigationID != investigationID ||
		receipt.value.RevisionID != expectedRevisionID {
		return nil, store.ErrNotFound
	}
	value := receipt.value
	return &value, nil
}

func (fake *checklistDispositionFake) AppendWorkbenchDisposition(
	_ context.Context,
	principal string,
	request store.WorkbenchDispositionRequest,
) (*store.WorkbenchDisposition, error) {
	if principal != fake.owner {
		return nil, store.ErrNotFound
	}
	suggestion, err := store.CanonicalWorkbenchSuggestion(
		request.Suggestion,
	)
	if err != nil {
		return nil, err
	}
	request.Suggestion = suggestion
	key := principal + "|" + request.IdempotencyKey
	if receipt, ok := fake.receipts[key]; ok {
		if digestJSON(receipt.request) != digestJSON(request) {
			return nil, store.ErrConflict
		}
		value := receipt.value
		return &value, nil
	}
	switch request.Category {
	case "accepted", "rejected", "completed", "reopened", "waived":
	default:
		return nil, store.ErrInvalidWorkbenchDisposition
	}
	if (request.Category == "rejected" ||
		request.Category == "reopened" ||
		request.Category == "waived") &&
		strings.TrimSpace(request.Rationale) == "" {
		return nil, store.ErrInvalidWorkbenchDisposition
	}
	sequence := 1
	if request.Supersedes == "" {
		for _, existing := range fake.records {
			if existing.Suggestion.ID == suggestion.ID &&
				!checklistDispositionSuperseded(
					fake.records,
					existing.ID,
				) {
				return nil, store.ErrConflict
			}
		}
	} else {
		found := false
		for _, existing := range fake.records {
			if existing.ID == request.Supersedes &&
				existing.Suggestion.ID == suggestion.ID &&
				!checklistDispositionSuperseded(
					fake.records,
					existing.ID,
				) {
				sequence = existing.Sequence + 1
				found = true
				break
			}
		}
		if !found {
			return nil, store.ErrConflict
		}
	}
	id := "wbd_" + strings.TrimPrefix(
		digestJSON(struct {
			Principal string `json:"principal"`
			Key       string `json:"key"`
		}{Principal: principal, Key: request.IdempotencyKey}),
		"sha256:",
	)
	value := store.WorkbenchDisposition{
		SchemaVersion:   store.WorkbenchDispositionSchemaVersion,
		ID:              id,
		InvestigationID: suggestion.InvestigationID,
		RevisionID:      suggestion.RevisionID,
		Suggestion:      suggestion,
		Category:        request.Category,
		Actor:           principal,
		Authority:       "investigation_owner",
		Rationale:       strings.TrimSpace(request.Rationale),
		Sequence:        sequence,
		Supersedes:      request.Supersedes,
		CreatedAt: time.Date(
			2026,
			time.July,
			27,
			12,
			0,
			len(fake.records),
			0,
			time.UTC,
		),
	}
	value.ContentDigest = digestJSON(struct {
		ID         string `json:"id"`
		Suggestion string `json:"suggestion"`
		Category   string `json:"category"`
		Sequence   int    `json:"sequence"`
		Supersedes string `json:"supersedes"`
	}{
		ID:         value.ID,
		Suggestion: value.Suggestion.ContentDigest,
		Category:   value.Category,
		Sequence:   value.Sequence,
		Supersedes: value.Supersedes,
	})
	fake.records = append(fake.records, value)
	fake.receipts[key] = checklistDispositionReceipt{
		request: request,
		value:   value,
	}
	return &value, nil
}

func checklistDispositionSuperseded(
	records []store.WorkbenchDisposition,
	id string,
) bool {
	for _, record := range records {
		if record.Supersedes == id {
			return true
		}
	}
	return false
}

func TestWorkbenchChecklistDeterministicUnacceptedProjectionAndCursor(
	t *testing.T,
) {
	service, _, _, _ := checklistService(t, "user:t219")
	request := WorkbenchChecklistRequest{
		InvestigationID: checklistInvestigationID,
		RevisionID:      checklistRevisionID,
		PageSize:        2,
	}
	first, err := service.Read(context.Background(), "user:t219", request)
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}
	second, err := service.Read(context.Background(), "user:t219", request)
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf(
			"equal state changed checklist bytes\nfirst: %s\nsecond: %s",
			firstJSON,
			secondJSON,
		)
	}
	if first.Pagination.Complete ||
		first.Pagination.NextCursor == "" ||
		first.Pagination.TotalEntries <= len(first.Entries) {
		t.Fatalf("unexpected pagination: %+v", first.Pagination)
	}
	for _, entry := range first.Entries {
		if entry.State != "unaccepted" ||
			entry.EvidenceState != "current" ||
			entry.Disposition != nil ||
			len(entry.Suggestion.Evidence) == 0 ||
			entry.Suggestion.RevisionID != checklistRevisionID {
			t.Fatalf("suggestion was pre-accepted or unbound: %+v", entry)
		}
	}
	request.Cursor = first.Pagination.NextCursor
	next, err := service.Read(context.Background(), "user:t219", request)
	if err != nil {
		t.Fatalf("next Read: %v", err)
	}
	if len(next.Entries) == 0 ||
		next.Entries[0].Suggestion.ID ==
			first.Entries[0].Suggestion.ID {
		t.Fatalf("cursor did not advance: first=%+v next=%+v", first, next)
	}
}

func TestWorkbenchChecklistDispositionSupersessionAndStaleEvidence(
	t *testing.T,
) {
	service, _, implementation, dispositions :=
		checklistService(t, "user:t219")
	page := checklistReadAll(t, service, "user:t219")
	suggestion := page.Entries[0].Suggestion
	accepted, err := service.RecordDisposition(
		context.Background(),
		"user:t219",
		WorkbenchDispositionMutation{
			InvestigationID:    checklistInvestigationID,
			ExpectedRevisionID: checklistRevisionID,
			IdempotencyKey:     "accept-one",
			Suggestion:         suggestion,
			Category:           "accepted",
		},
	)
	if err != nil {
		t.Fatalf("accept suggestion: %v", err)
	}
	retry, err := service.RecordDisposition(
		context.Background(),
		"user:t219",
		WorkbenchDispositionMutation{
			InvestigationID:    checklistInvestigationID,
			ExpectedRevisionID: checklistRevisionID,
			IdempotencyKey:     "accept-one",
			Suggestion:         suggestion,
			Category:           "accepted",
		},
	)
	if err != nil || retry.ID != accepted.ID {
		t.Fatalf("idempotent retry = %+v, %v", retry, err)
	}
	_, err = service.RecordDisposition(
		context.Background(),
		"user:t219",
		WorkbenchDispositionMutation{
			InvestigationID:    checklistInvestigationID,
			ExpectedRevisionID: checklistRevisionID,
			IdempotencyKey:     "duplicate-root",
			Suggestion:         suggestion,
			Category:           "completed",
		},
	)
	assertWorkbenchChecklistStatus(t, err, http.StatusConflict)

	reopened, err := service.RecordDisposition(
		context.Background(),
		"user:t219",
		WorkbenchDispositionMutation{
			InvestigationID:    checklistInvestigationID,
			ExpectedRevisionID: checklistRevisionID,
			IdempotencyKey:     "reopen-one",
			Suggestion:         suggestion,
			Category:           "reopened",
			Rationale:          "The cited implementation needs another pass.",
			Supersedes:         accepted.ID,
		},
	)
	if err != nil || reopened.Sequence != 2 {
		t.Fatalf("reopen suggestion = %+v, %v", reopened, err)
	}
	current := checklistReadAll(t, service, "user:t219")
	entry := checklistEntryByID(t, current, suggestion.ID)
	if entry.State != "reopened" ||
		len(entry.DispositionHistory) != 2 ||
		entry.Disposition.ID != reopened.ID {
		t.Fatalf("supersession projection = %+v", entry)
	}

	implementation.page.Rows[0].ID = "wri_changed"
	implementation.page.Rows[0].Source.Path =
		"internal/cart/changed_handler_test.go"
	implementation.page.SnapshotDigest = "sha256:" +
		strings.Repeat("f", 64)
	refreshed := checklistReadAll(t, service, "user:t219")
	stale := checklistEntryByID(t, refreshed, suggestion.ID)
	if stale.EvidenceState != "stale" ||
		stale.State != "reopened" ||
		stale.Suggestion.ContentDigest != suggestion.ContentDigest {
		t.Fatalf("stale evidence silently retargeted: %+v", stale)
	}
	currentCount := 0
	for _, candidate := range refreshed.Entries {
		if candidate.EvidenceState == "current" &&
			candidate.State == "unaccepted" {
			currentCount++
		}
	}
	if currentCount == 0 {
		t.Fatal("refreshed evidence did not produce new unaccepted suggestions")
	}
	_, err = service.RecordDisposition(
		context.Background(),
		"user:t219",
		WorkbenchDispositionMutation{
			InvestigationID:    checklistInvestigationID,
			ExpectedRevisionID: checklistRevisionID,
			IdempotencyKey:     "stale-new-root",
			Suggestion:         suggestion,
			Category:           "completed",
		},
	)
	assertWorkbenchChecklistStatus(t, err, http.StatusConflict)
	completed, err := service.RecordDisposition(
		context.Background(),
		"user:t219",
		WorkbenchDispositionMutation{
			InvestigationID:    checklistInvestigationID,
			ExpectedRevisionID: checklistRevisionID,
			IdempotencyKey:     "complete-stale",
			Suggestion:         suggestion,
			Category:           "completed",
			Supersedes:         reopened.ID,
		},
	)
	if err != nil || completed.Sequence != 3 {
		t.Fatalf("correct stale disposition = %+v, %v", completed, err)
	}
	if len(dispositions.records) != 3 {
		t.Fatalf("disposition append count = %d, want 3", len(dispositions.records))
	}
}

func TestWorkbenchChecklistReaderCannotMutate(t *testing.T) {
	service, _, _, _ := checklistService(t, "user:reader")
	page := checklistReadAll(t, service, "user:reader")
	_, err := service.RecordDisposition(
		context.Background(),
		"user:reader",
		WorkbenchDispositionMutation{
			InvestigationID:    checklistInvestigationID,
			ExpectedRevisionID: checklistRevisionID,
			IdempotencyKey:     "reader-write",
			Suggestion:         page.Entries[0].Suggestion,
			Category:           "accepted",
		},
	)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reader mutation = %v, want not found", err)
	}
}

func TestWorkbenchChecklistCompletionHasNoTaskOrDecisionSurface(
	t *testing.T,
) {
	serviceType := reflect.TypeOf(WorkbenchChecklistService{})
	for index := 0; index < serviceType.NumField(); index++ {
		name := strings.ToLower(serviceType.Field(index).Name)
		for _, forbidden := range []string{
			"decision",
			"reviewitem",
			"task",
			"checklistitem",
		} {
			if strings.Contains(name, forbidden) {
				t.Fatalf(
					"checklist service field %q introduces %s state",
					serviceType.Field(index).Name,
					forbidden,
				)
			}
		}
	}
	pointerType := reflect.TypeOf(&WorkbenchChecklistService{})
	for _, forbidden := range []string{
		"CreateReviewItem",
		"PutReviewItem",
		"MutateReviewItem",
		"CreateTask",
		"PutTask",
		"CreateDecision",
	} {
		if _, exists := pointerType.MethodByName(forbidden); exists {
			t.Fatalf("forbidden checklist method %q exists", forbidden)
		}
	}

	service, _, _, _ := checklistService(t, "user:t219")
	page := checklistReadAll(t, service, "user:t219")
	for index, entry := range page.Entries {
		if _, err := service.RecordDisposition(
			context.Background(),
			"user:t219",
			WorkbenchDispositionMutation{
				InvestigationID:    checklistInvestigationID,
				ExpectedRevisionID: checklistRevisionID,
				IdempotencyKey: fmt.Sprintf(
					"complete-all-%d",
					index,
				),
				Suggestion: entry.Suggestion,
				Category:   "completed",
			},
		); err != nil {
			t.Fatalf("complete entry %d: %v", index, err)
		}
	}
	completed := checklistReadAll(t, service, "user:t219")
	for _, entry := range completed.Entries {
		if entry.EvidenceState == "current" &&
			entry.State != "completed" {
			t.Fatalf("entry was not completed: %+v", entry)
		}
	}
	if !strings.Contains(
		completed.Caveat,
		"creates no Investigation Decision",
	) {
		t.Fatalf("completion caveat = %q", completed.Caveat)
	}
}

func TestWorkbenchDispositionMutationRejectsTaskFieldsAndCustomState(
	t *testing.T,
) {
	for _, field := range []string{
		"comment",
		"assignment",
		"assignee",
		"due_date",
		"priority",
		"state",
	} {
		raw := []byte(`{"` + field + `":"forbidden"}`)
		var mutation WorkbenchDispositionMutation
		if json.Unmarshal(raw, &mutation) == nil {
			t.Errorf("field %q was accepted", field)
		}
	}
	service, _, _, _ := checklistService(t, "user:t219")
	page := checklistReadAll(t, service, "user:t219")
	_, err := service.RecordDisposition(
		context.Background(),
		"user:t219",
		WorkbenchDispositionMutation{
			InvestigationID:    checklistInvestigationID,
			ExpectedRevisionID: checklistRevisionID,
			IdempotencyKey:     "custom-state",
			Suggestion:         page.Entries[0].Suggestion,
			Category:           "assigned",
		},
	)
	assertWorkbenchChecklistStatus(t, err, http.StatusBadRequest)
}

func TestWorkbenchChecklistCursorRejectsEvidenceRefresh(t *testing.T) {
	service, _, implementation, _ := checklistService(t, "user:t219")
	request := WorkbenchChecklistRequest{
		InvestigationID: checklistInvestigationID,
		RevisionID:      checklistRevisionID,
		PageSize:        1,
	}
	first, err := service.Read(context.Background(), "user:t219", request)
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}
	if first.Pagination.NextCursor == "" {
		t.Fatal("first page omitted cursor")
	}
	implementation.page.Rows[0].ID = "wri_cursor_changed"
	implementation.page.SnapshotDigest = "sha256:" +
		strings.Repeat("e", 64)
	request.Cursor = first.Pagination.NextCursor
	_, err = service.Read(context.Background(), "user:t219", request)
	assertWorkbenchChecklistStatus(t, err, http.StatusConflict)
}

func checklistService(
	t *testing.T,
	principal string,
) (
	*WorkbenchChecklistService,
	*checklistImpactFake,
	*checklistImplementationFake,
	*checklistDispositionFake,
) {
	t.Helper()
	view := &store.WorkbenchView{
		Investigation: store.Investigation{
			ID:                checklistInvestigationID,
			Owner:             "user:t219",
			CurrentRevisionID: checklistRevisionID,
		},
		Revision: store.Revision{
			ID:              checklistRevisionID,
			InvestigationID: checklistInvestigationID,
			ContentDigest:   "sha256:" + strings.Repeat("1", 64),
		},
		Brief: store.ChangeBrief{
			ID:              "icb_t219",
			InvestigationID: checklistInvestigationID,
			RevisionID:      checklistRevisionID,
			TicketKind:      store.ChangeBriefModify,
			ContentDigest:   "sha256:" + strings.Repeat("2", 64),
		},
	}
	caller := CallerMapRow{
		Classification:     "resolved_caller",
		Resolution:         "scip",
		Protocol:           "protobuf",
		Operation:          "/shop.Cart/Get",
		DeclarationLineage: "proto/cart.proto:shop.Cart",
		Tier:               "derived",
		CodeRole:           "production",
		Fresh:              true,
		Source: CallerMapSource{
			Repository: checklistRepository,
			Commit:     checklistCommit,
			Path:       "internal/cart/client.go",
			StartByte:  10,
			EndByte:    20,
			StartLine:  4,
			EndLine:    4,
		},
	}
	impact := &checklistImpactFake{
		page: WorkbenchImpactPage{
			SchemaVersion:   workbenchImpactSchemaVersion,
			InvestigationID: checklistInvestigationID,
			RevisionID:      checklistRevisionID,
			TicketKind:      store.ChangeBriefModify,
			Atlas:           []WorkbenchAtlasImpact{},
			Callers: []WorkbenchCallerImpact{{
				ResolvedCallers:      []CallerMapRow{caller},
				ExtractorAbstentions: []CallerMapRow{},
			}},
			ResourcePlanes: []ResourcePlaneSnapshot{{
				ID:            "runtime",
				Label:         "Runtime",
				State:         ResourcePlaneUnsupported,
				Relationships: []ResourcePlaneRelationship{},
			}},
			AnalysisScope: WorkbenchAnalysisScope{
				Coverage:     []WorkbenchImpactCoverage{},
				Capabilities: []WorkbenchImpactCapability{},
				Gaps: []WorkbenchImpactGap{{
					Capability: "history",
					Target:     checklistRepository,
					State:      "unsupported",
					Code:       "history_unavailable",
				}},
			},
			Pagination: WorkbenchImpactPagination{Complete: true},
		},
	}
	implementation := &checklistImplementationFake{
		page: WorkbenchImplementationPage{
			SchemaVersion:   workbenchImplementationSchemaVersion,
			InvestigationID: checklistInvestigationID,
			RevisionID:      checklistRevisionID,
			TicketKind:      store.ChangeBriefModify,
			Rows: []WorkbenchImplementationRow{{
				ID:             "wri_t219",
				Kind:           "search_candidate",
				CodeRole:       "test",
				ReviewState:    "review_candidate",
				SelectionRule:  "operation_identifier_search_v1",
				SelectionInput: checklistRepository + "|Get",
				Source: WorkbenchImplementationSourceCitation{
					Repository: checklistRepository,
					Commit:     checklistCommit,
					Path:       "internal/cart/handler_test.go",
					StartLine:  8,
					EndLine:    8,
				},
			}},
			Capabilities: []WorkbenchImplementationCapability{},
			Gaps:         []WorkbenchImplementationGap{},
			Pagination: WorkbenchImplementationPagination{
				TotalRows: 1,
				Complete:  true,
			},
			SnapshotDigest: "sha256:" + strings.Repeat("3", 64),
		},
	}
	dispositions := &checklistDispositionFake{
		owner:    "user:t219",
		receipts: make(map[string]checklistDispositionReceipt),
	}
	return &WorkbenchChecklistService{
		opts: Options{
			Principal: func(context.Context) string {
				return principal
			},
		},
		workbench:      &checklistWorkbenchFake{view: view},
		impact:         impact,
		implementation: implementation,
		dispositions:   dispositions,
	}, impact, implementation, dispositions
}

func checklistReadAll(
	t *testing.T,
	service *WorkbenchChecklistService,
	principal string,
) *WorkbenchChecklistPage {
	t.Helper()
	page, err := service.Read(
		context.Background(),
		principal,
		WorkbenchChecklistRequest{
			InvestigationID: checklistInvestigationID,
			RevisionID:      checklistRevisionID,
			PageSize:        workbenchChecklistMaxPage,
		},
	)
	if err != nil {
		t.Fatalf("Read all: %v", err)
	}
	if !page.Pagination.Complete {
		t.Fatalf("fixture exceeded one page: %+v", page.Pagination)
	}
	return page
}

func checklistEntryByID(
	t *testing.T,
	page *WorkbenchChecklistPage,
	id string,
) WorkbenchChecklistEntry {
	t.Helper()
	for _, entry := range page.Entries {
		if entry.Suggestion.ID == id {
			return entry
		}
	}
	t.Fatalf("checklist entry %q not found", id)
	return WorkbenchChecklistEntry{}
}

func assertWorkbenchChecklistStatus(
	t *testing.T,
	err error,
	want int,
) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected HTTP %d error", want)
	}
	var statusError interface{ GetStatus() int }
	if !errors.As(err, &statusError) ||
		statusError.GetStatus() != want {
		t.Fatalf("error = %v, want HTTP %d", err, want)
	}
}
