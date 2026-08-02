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

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	checklistInvestigationID = "01J00000000000000000000219"
	checklistRevisionID      = "ivr_t219"
	checklistRepository      = "github.com/acme/cart"
	checklistCommit          = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestWorkbenchChecklistRawAccumulatorRetainsCanonicalCeiling(
	t *testing.T,
) {
	const candidateCount = workbenchChecklistMaxSuggestions + 250
	all := make([]workbenchChecklistRawSuggestion, candidateCount)
	allKeys := make([]string, candidateCount)
	for index := range all {
		all[index] = workbenchChecklistRawSuggestion{
			kind:          "review_candidate",
			summary:       fmt.Sprintf("Review candidate %04d.", index),
			selectionRule: "t30.6l:bounded_accumulator_v1",
		}
		allKeys[index] = workbenchChecklistRawKey(all[index])
	}
	slices.Sort(allKeys)
	accumulator := newWorkbenchChecklistRawAccumulator(nil, false)
	for index := len(all) - 1; index >= 0; index-- {
		accumulator.add(all[index], all[index])
	}
	retained := accumulator.retained()
	retainedKeys := make([]string, len(retained))
	for index := range retained {
		retainedKeys[index] = workbenchChecklistRawKey(retained[index])
	}
	slices.Sort(retainedKeys)
	if !accumulator.overflow ||
		len(retained) != workbenchChecklistMaxSuggestions ||
		!slices.Equal(
			retainedKeys,
			allKeys[:workbenchChecklistMaxSuggestions],
		) {
		t.Fatalf(
			"bounded accumulator = overflow:%t retained:%d canonical:%t",
			accumulator.overflow,
			len(retained),
			slices.Equal(
				retainedKeys,
				allKeys[:workbenchChecklistMaxSuggestions],
			),
		)
	}
}

func TestNormalizeWorkbenchChecklistEvidenceCanonicalizesImpactDefaults(
	t *testing.T,
) {
	implicit, err := normalizeWorkbenchChecklistEvidence(
		WorkbenchChecklistEvidenceInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := normalizeWorkbenchChecklistEvidence(
		WorkbenchChecklistEvidenceInput{
			ImpactFilters: WorkbenchImpactFilters{
				Freshness:  "any",
				Resolution: "any",
				Ordering:   "source",
				Level:      "occurrence",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(implicit, explicit) {
		t.Fatalf(
			"normalized checklist evidence differs: implicit=%+v explicit=%+v",
			implicit,
			explicit,
		)
	}
}

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

type checklistRotatingImpactFake struct {
	pages []WorkbenchImpactPage
	build int
}

func (fake *checklistRotatingImpactFake) Read(
	_ context.Context,
	_ string,
	request WorkbenchImpactRequest,
) (*WorkbenchImpactPage, error) {
	if request.InvestigationID != checklistInvestigationID ||
		request.RevisionID != checklistRevisionID || len(fake.pages) != 2 {
		return nil, store.ErrNotFound
	}
	pageIndex := 0
	if request.Cursor == "" {
		fake.build++
	} else if request.Cursor == fmt.Sprintf(
		"outer-%d-next",
		fake.build,
	) {
		pageIndex = 1
	} else {
		return nil, store.ErrNotFound
	}
	encoded, _ := json.Marshal(fake.pages[pageIndex])
	var page WorkbenchImpactPage
	_ = json.Unmarshal(encoded, &page)
	if pageIndex == 0 {
		page.Pagination = WorkbenchImpactPagination{
			NextCursor: fmt.Sprintf("outer-%d-next", fake.build),
		}
	} else {
		page.Pagination = WorkbenchImpactPagination{Complete: true}
	}
	for callerIndex := range page.Callers {
		caller := &page.Callers[callerIndex]
		if !caller.Pagination.Complete {
			caller.Pagination.NextCursor = fmt.Sprintf(
				"caller-%d-%d-next",
				fake.build,
				pageIndex,
			)
		}
		for rowIndex := range caller.ResolvedCallers {
			caller.ResolvedCallers[rowIndex].Source.Citation = fmt.Sprintf(
				"caller-%d-%d-%d",
				fake.build,
				pageIndex,
				rowIndex,
			)
		}
		for rowIndex := range caller.ExtractorAbstentions {
			caller.ExtractorAbstentions[rowIndex].Source.Citation = fmt.Sprintf(
				"abstention-%d-%d-%d",
				fake.build,
				pageIndex,
				rowIndex,
			)
		}
	}
	return &page, nil
}

type checklistEndlessImpactFake struct {
	page  WorkbenchImpactPage
	calls int
}

func (fake *checklistEndlessImpactFake) Read(
	_ context.Context,
	_ string,
	request WorkbenchImpactRequest,
) (*WorkbenchImpactPage, error) {
	wantCursor := ""
	if fake.calls > 0 {
		wantCursor = fmt.Sprintf("impact-page-%d", fake.calls)
	}
	if request.InvestigationID != checklistInvestigationID ||
		request.RevisionID != checklistRevisionID ||
		request.Cursor != wantCursor {
		return nil, store.ErrNotFound
	}
	fake.calls++
	encoded, _ := json.Marshal(fake.page)
	var page WorkbenchImpactPage
	_ = json.Unmarshal(encoded, &page)
	page.Pagination = WorkbenchImpactPagination{
		NextCursor: fmt.Sprintf("impact-page-%d", fake.calls),
	}
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

func TestWorkbenchChecklistIgnoresRotatedImpactCapabilities(
	t *testing.T,
) {
	service, impact, _, _ := checklistService(t, "user:t219")
	firstImpact := cloneChecklistImpactPage(t, impact.page)
	firstImpact.Callers[0].Pagination = CallerMapPagination{Complete: false}
	firstImpact.Callers[0].ResolvedCallers[0].Source.ObjectID =
		strings.Repeat("a", 40)
	firstImpact.Callers[0].ResolvedCallers[0].Source.BlobDigest =
		"sha256:" + strings.Repeat("b", 64)
	firstImpact.Callers[0].ResolvedCallers[0].Source.Plane =
		exactCallerMapPlane

	secondImpact := cloneChecklistImpactPage(t, impact.page)
	secondImpact.Callers[0].Pagination = CallerMapPagination{Complete: true}
	secondImpact.Callers[0].ResolvedCallers[0].Source.Path =
		"internal/cart/second_client.go"
	secondImpact.Callers[0].ResolvedCallers[0].Source.ObjectID =
		strings.Repeat("c", 40)
	secondImpact.Callers[0].ResolvedCallers[0].Source.BlobDigest =
		"sha256:" + strings.Repeat("d", 64)
	secondImpact.Callers[0].ResolvedCallers[0].Source.Plane =
		exactCallerMapPlane

	rotating := &checklistRotatingImpactFake{
		pages: []WorkbenchImpactPage{firstImpact, secondImpact},
	}
	service.impact = rotating

	first := checklistReadAll(t, service, "user:t219")
	second := checklistReadAll(t, service, "user:t219")
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf(
			"rotated Impact capabilities changed checklist identity\nfirst: %s\nsecond: %s",
			firstJSON,
			secondJSON,
		)
	}
	if rotating.build != 2 {
		t.Fatalf("Impact builds = %d, want 2", rotating.build)
	}

	var suggestion store.WorkbenchSuggestion
	for _, entry := range first.Entries {
		if entry.Suggestion.Kind == "review_resolved_caller" {
			suggestion = entry.Suggestion
			break
		}
	}
	if suggestion.ID == "" {
		t.Fatal("exact caller suggestion was not derived")
	}
	disposition, err := service.RecordDisposition(
		context.Background(),
		"user:t219",
		WorkbenchDispositionMutation{
			InvestigationID:    checklistInvestigationID,
			ExpectedRevisionID: checklistRevisionID,
			IdempotencyKey:     "accept-rotated-citation",
			Suggestion:         suggestion,
			Category:           "accepted",
		},
	)
	if err != nil {
		t.Fatalf("record disposition after capability rotation: %v", err)
	}
	current := checklistReadAll(t, service, "user:t219")
	entry := checklistEntryByID(t, current, suggestion.ID)
	if entry.EvidenceState != "current" || entry.State != "accepted" ||
		entry.Disposition == nil || entry.Disposition.ID != disposition.ID {
		t.Fatalf("rotated capability made disposition stale: %+v", entry)
	}
}

func TestWorkbenchChecklistKeepsFivePageImpactBound(t *testing.T) {
	service, impact, _, _ := checklistService(t, "user:t219")
	base := cloneChecklistImpactPage(t, impact.page)
	base.Callers[0].Pagination = CallerMapPagination{Complete: true}
	endless := &checklistEndlessImpactFake{page: base}
	service.impact = endless
	page := checklistReadAll(t, service, "user:t219")
	if endless.calls != workbenchChecklistEvidencePages {
		t.Fatalf(
			"Impact page reads = %d, want %d",
			endless.calls,
			workbenchChecklistEvidencePages,
		)
	}
	if !page.EvidenceSnapshot.ImpactTruncated {
		t.Fatal("five-page Impact limit was not retained in the snapshot")
	}
	for _, entry := range page.Entries {
		if entry.Suggestion.Kind == "review_remaining_evidence" {
			return
		}
	}
	t.Fatal("five-page Impact limit omitted its review suggestion")
}

func TestCanonicalWorkbenchImpactStripsOnlyTransportCapabilities(
	t *testing.T,
) {
	source := CallerMapSource{
		Repository: checklistRepository,
		Commit:     checklistCommit,
		Path:       "internal/cart/client.go",
		ObjectID:   strings.Repeat("a", 40),
		BlobDigest: "sha256:" + strings.Repeat("b", 64),
		Plane:      exactCallerMapPlane,
		StartByte:  10,
		EndByte:    20,
		StartLine:  4,
		EndLine:    4,
		Citation:   "caller-capability-one",
	}
	row := CallerMapRow{
		Classification: "resolved_caller",
		Resolution:     "syntax",
		Protocol:       "protobuf",
		Operation:      "/shop.Cart/Get",
		Tier:           "direct",
		Fresh:          true,
		Source:         source,
	}
	total := 1
	endpoint := CallerMapEndpoint{
		Protocol: "protobuf", Repository: checklistRepository,
		Lineage: "proto/cart.proto:shop.Cart", Operation: "/shop.Cart/Get",
	}
	replacementEndpoint := endpoint
	replacementEndpoint.Operation = "/shop.Cart/GetV2"
	generation := CallerMapGeneration{
		State: string(callerexecute.PublicationCurrent),
		Plane: exactCallerMapPlane, Repository: checklistRepository,
		Commit:           checklistCommit,
		GenerationDigest: "sha256:" + strings.Repeat("1", 64),
		ManifestDigest:   "sha256:" + strings.Repeat("2", 64),
		PairSetDigest:    "sha256:" + strings.Repeat("3", 64),
	}
	page := WorkbenchImpactPage{
		SchemaVersion:   workbenchImpactSchemaVersion,
		InvestigationID: checklistInvestigationID,
		RevisionID:      checklistRevisionID,
		TicketKind:      store.ChangeBriefMigrate,
		Callers: []WorkbenchCallerImpact{{
			Query:             CallerMapQuery{Endpoint: endpoint, Ordering: "source"},
			Generation:        generation,
			MatchingRowsState: "exact",
			ResolvedCallers:   []CallerMapRow{row},
			TotalMatchingRows: &total,
			Pagination: CallerMapPagination{
				NextCursor: "caller-cursor-one",
			},
		}},
		Comparison: &CallerComparisonExactPage{
			SchemaVersion: callerComparisonSchemaVersion,
			Query: CallerComparisonQuery{
				Old: endpoint, Replacement: replacementEndpoint,
				Ordering: "source", Level: "occurrence",
			},
			Old: CallerComparisonExactSnapshot{
				Endpoint: endpoint, Generation: generation,
				MatchingRowsState: "exact",
			},
			Replacement: CallerComparisonExactSnapshot{
				Endpoint: replacementEndpoint, Generation: generation,
				MatchingRowsState: "exact",
			},
			Rows: []CallerComparisonRow{{
				Level: "occurrence", Key: "caller-key",
				Classification: "both_evidence",
				Old: CallerComparisonSide{
					OccurrenceCount: 1,
					Rows:            []CallerMapRow{row},
				},
				Replacement: CallerComparisonSide{
					OccurrenceCount: 1,
					Rows: []CallerMapRow{{
						Classification: "resolved_caller",
						Source: CallerMapSource{
							Repository: checklistRepository,
							Commit:     checklistCommit,
							Path:       "internal/cart/replacement.go",
							ObjectID:   strings.Repeat("c", 40),
							BlobDigest: "sha256:" + strings.Repeat("d", 64),
							Plane:      exactCallerMapPlane,
							StartByte:  30,
							EndByte:    40,
							Citation:   "replacement-capability-one",
						},
					}},
				},
			}},
			TotalRows:         &total,
			MatchingRowsState: "exact",
			Pagination: CallerMapPagination{
				NextCursor: "comparison-cursor-one",
			},
		},
		FieldReferences: &FieldReferencePage{
			Pagination: CallerMapPagination{
				NextCursor: "field-cursor-one",
			},
		},
		Pagination: WorkbenchImpactPagination{
			NextCursor: "outer-cursor-one",
		},
	}
	rotated := cloneChecklistImpactPage(t, page)
	rotated.Pagination.NextCursor = "outer-cursor-two"
	rotated.Callers[0].Pagination.NextCursor = "caller-cursor-two"
	rotated.Callers[0].ResolvedCallers[0].Source.Citation =
		"caller-capability-two"
	rotated.Comparison.Pagination.NextCursor = "comparison-cursor-two"
	rotated.Comparison.Rows[0].Old.Rows[0].Source.Citation =
		"old-capability-two"
	rotated.Comparison.Rows[0].Replacement.Rows[0].Source.Citation =
		"replacement-capability-two"
	rotated.FieldReferences.Pagination.NextCursor = "field-cursor-two"

	canonical := canonicalWorkbenchChecklistImpactPage(page)
	rotatedCanonical := canonicalWorkbenchChecklistImpactPage(rotated)
	if digestJSON(canonical) != digestJSON(rotatedCanonical) {
		t.Fatal("rotated cursor or citation changed canonical Impact digest")
	}
	raw := rawSuggestionsFromImpact(page)
	rotatedRaw := rawSuggestionsFromImpact(rotated)
	if len(raw) != len(rotatedRaw) {
		t.Fatalf("rotated capability changed suggestion count: %d != %d", len(raw), len(rotatedRaw))
	}
	for index := range raw {
		if workbenchChecklistRawKey(raw[index]) !=
			workbenchChecklistRawKey(rotatedRaw[index]) {
			t.Fatalf("rotated capability changed suggestion %d identity", index)
		}
	}
	if page.Pagination.NextCursor == "" ||
		page.Callers[0].ResolvedCallers[0].Source.Citation == "" {
		t.Fatal("canonicalization mutated its input page")
	}
	if canonical.Pagination.NextCursor != "" ||
		canonical.Callers[0].Pagination.NextCursor != "" ||
		canonical.Callers[0].ResolvedCallers[0].Source.Citation != "" ||
		canonical.Comparison.Pagination.NextCursor != "" ||
		canonical.Comparison.Rows[0].Old.Rows[0].Source.Citation != "" ||
		canonical.Comparison.Rows[0].Replacement.Rows[0].Source.Citation != "" ||
		canonical.FieldReferences.Pagination.NextCursor != "" {
		t.Fatalf("transport capability survived canonicalization: %+v", canonical)
	}
	confirmed := canonical.Callers[0].ResolvedCallers[0].Source
	if confirmed.ObjectID != source.ObjectID ||
		confirmed.BlobDigest != source.BlobDigest ||
		confirmed.Path != source.Path || confirmed.StartByte != source.StartByte ||
		confirmed.EndByte != source.EndByte ||
		canonical.Callers[0].Generation.GenerationDigest !=
			generation.GenerationDigest {
		t.Fatalf("semantic source identity changed: %+v", confirmed)
	}
	semanticChange := cloneChecklistImpactPage(t, rotatedCanonical)
	semanticChange.Callers[0].ResolvedCallers[0].Source.ObjectID =
		strings.Repeat("e", 40)
	if digestJSON(canonical) == digestJSON(semanticChange) {
		t.Fatal("semantic source identity was omitted from canonical digest")
	}
	semanticChange = cloneChecklistImpactPage(t, rotatedCanonical)
	semanticChange.Callers[0].Generation.GenerationDigest =
		"sha256:" + strings.Repeat("f", 64)
	if digestJSON(canonical) == digestJSON(semanticChange) {
		t.Fatal("semantic generation identity was omitted from canonical digest")
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

func cloneChecklistImpactPage(
	t *testing.T,
	page WorkbenchImpactPage,
) WorkbenchImpactPage {
	t.Helper()
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var result WorkbenchImpactPage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
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
