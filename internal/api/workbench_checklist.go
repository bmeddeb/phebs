package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	workbenchChecklistSchemaVersion  = "workbench-checklist-v1"
	workbenchChecklistCursorVersion  = "workbench-checklist-cursor-v1"
	workbenchChecklistCursorLimit    = 64 << 10
	workbenchChecklistDefaultPage    = 50
	workbenchChecklistMaxPage        = 100
	workbenchChecklistEvidencePages  = 5
	workbenchChecklistMaxSuggestions = 1_000
	workbenchChecklistMaxEvidence    = 32
	workbenchChecklistMutationBytes  = 512 << 10
	workbenchChecklistCaveat         = "Checklist entries are deterministic unaccepted suggestions plus immutable human Dispositions. A displayed or fully dispositioned checklist does not establish completeness, correctness, migration completion, runtime use, or retirement safety and creates no Investigation Decision."
)

type workbenchChecklistReader interface {
	Read(context.Context, string, string) (*store.WorkbenchView, error)
}

type workbenchChecklistImpact interface {
	Read(
		context.Context,
		string,
		WorkbenchImpactRequest,
	) (*WorkbenchImpactPage, error)
}

type workbenchChecklistImplementation interface {
	Read(
		context.Context,
		string,
		WorkbenchImplementationRequest,
	) (*WorkbenchImplementationPage, error)
}

type workbenchChecklistDispositionStore interface {
	ListWorkbenchDispositionsAs(
		context.Context,
		string,
		string,
	) ([]store.WorkbenchDisposition, error)
	GetWorkbenchDispositionMutationAs(
		context.Context,
		string,
		string,
		string,
		string,
	) (*store.WorkbenchDisposition, error)
	AppendWorkbenchDisposition(
		context.Context,
		string,
		store.WorkbenchDispositionRequest,
	) (*store.WorkbenchDisposition, error)
}

// WorkbenchChecklistService composes T21.7/T21.8 readers and the narrow
// Investigation-owned Disposition store. It has no ReviewItem, Decision,
// evidence-store, task, or generic Disposition mutation field.
type WorkbenchChecklistService struct {
	opts           Options
	workbench      workbenchChecklistReader
	impact         workbenchChecklistImpact
	implementation workbenchChecklistImplementation
	dispositions   workbenchChecklistDispositionStore
}

func NewWorkbenchChecklistService(opts Options) *WorkbenchChecklistService {
	if opts.Workbench == nil || opts.Principal == nil || opts.Store == nil {
		return nil
	}
	dispositions, ok := opts.Store.(workbenchChecklistDispositionStore)
	if !ok {
		return nil
	}
	impact := NewWorkbenchImpactService(opts)
	implementation := NewWorkbenchImplementationService(opts)
	if impact == nil || implementation == nil {
		return nil
	}
	return &WorkbenchChecklistService{
		opts:           opts,
		workbench:      opts.Workbench,
		impact:         impact,
		implementation: implementation,
		dispositions:   dispositions,
	}
}

type WorkbenchChecklistEvidenceInput struct {
	CompatibilityRun string                          `json:"compatibility_run_id,omitempty"`
	ImpactFilters    WorkbenchImpactFilters          `json:"impact_filters"`
	Anchors          []WorkbenchImplementationAnchor `json:"anchors"`
}

type WorkbenchChecklistRequest struct {
	InvestigationID string                          `json:"investigation_id"`
	RevisionID      string                          `json:"revision_id"`
	Evidence        WorkbenchChecklistEvidenceInput `json:"evidence"`
	PageSize        int                             `json:"page_size,omitempty"`
	Cursor          string                          `json:"cursor,omitempty"`
}

type WorkbenchChecklistEvidenceSnapshot struct {
	ImpactDigest            string `json:"impact_digest"`
	ImplementationDigest    string `json:"implementation_digest"`
	CombinedDigest          string `json:"combined_digest"`
	ImpactTruncated         bool   `json:"impact_truncated"`
	ImplementationTruncated bool   `json:"implementation_truncated"`
}

type WorkbenchChecklistEntry struct {
	Suggestion         store.WorkbenchSuggestion    `json:"suggestion"`
	EvidenceState      string                       `json:"evidence_state"` // current | stale
	State              string                       `json:"state"`          // unaccepted | fixed category
	Disposition        *store.WorkbenchDisposition  `json:"disposition,omitempty"`
	DispositionHistory []store.WorkbenchDisposition `json:"disposition_history"`
}

type WorkbenchChecklistPagination struct {
	TotalEntries int    `json:"total_entries"`
	Complete     bool   `json:"complete"`
	NextCursor   string `json:"next_cursor,omitempty"`
}

type WorkbenchChecklistPage struct {
	SchemaVersion    string                             `json:"schema_version"`
	InvestigationID  string                             `json:"investigation_id"`
	RevisionID       string                             `json:"revision_id"`
	TicketKind       store.ChangeBriefTicketKind        `json:"ticket_kind"`
	EvidenceSnapshot WorkbenchChecklistEvidenceSnapshot `json:"evidence_snapshot"`
	Entries          []WorkbenchChecklistEntry          `json:"entries"`
	Pagination       WorkbenchChecklistPagination       `json:"pagination"`
	SnapshotDigest   string                             `json:"snapshot_digest"`
	Caveat           string                             `json:"caveat"`
}

// WorkbenchDispositionMutation accepts only the fixed checklist vocabulary.
// Actor is deliberately absent and is derived from the authenticated
// principal. Its custom decoder rejects task-like and custom state fields.
type WorkbenchDispositionMutation struct {
	InvestigationID    string                          `json:"investigation_id"`
	ExpectedRevisionID string                          `json:"expected_revision_id"`
	IdempotencyKey     string                          `json:"idempotency_key"`
	Evidence           WorkbenchChecklistEvidenceInput `json:"evidence"`
	Suggestion         store.WorkbenchSuggestion       `json:"suggestion"`
	Category           string                          `json:"category"`
	Rationale          string                          `json:"rationale,omitempty"`
	Supersedes         string                          `json:"supersedes,omitempty"`
}

func (mutation *WorkbenchDispositionMutation) UnmarshalJSON(
	data []byte,
) error {
	if len(data) > workbenchChecklistMutationBytes {
		return fmt.Errorf(
			"workbench disposition mutation exceeds %d bytes",
			workbenchChecklistMutationBytes,
		)
	}
	type wire WorkbenchDispositionMutation
	var decoded wire
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New(
				"workbench disposition mutation has trailing JSON",
			)
		}
		return err
	}
	*mutation = WorkbenchDispositionMutation(decoded)
	return nil
}

type workbenchChecklistCursor struct {
	Schema         string `json:"schema"`
	QueryDigest    string `json:"query_digest"`
	Principal      string `json:"principal"`
	RevisionDigest string `json:"revision_digest"`
	SnapshotDigest string `json:"snapshot_digest"`
	Offset         int    `json:"offset"`
	Checksum       string `json:"checksum"`
}

type workbenchChecklistRawSuggestion struct {
	kind          string
	summary       string
	selectionRule string
	evidence      []store.WorkbenchSuggestionEvidence
}

type workbenchChecklistBuild struct {
	view             *store.WorkbenchView
	evidenceSnapshot WorkbenchChecklistEvidenceSnapshot
	suggestions      []store.WorkbenchSuggestion
	dispositions     []store.WorkbenchDisposition
	entries          []WorkbenchChecklistEntry
	revisionDigest   string
	snapshotDigest   string
}

func (service *WorkbenchChecklistService) Read(
	ctx context.Context,
	principal string,
	request WorkbenchChecklistRequest,
) (*WorkbenchChecklistPage, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable(
			"workbench checklist unavailable",
		)
	}
	if err := service.validatePrincipal(ctx, principal); err != nil {
		return nil, err
	}
	request, err := normalizeWorkbenchChecklistRequest(request)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	build, err := service.build(
		ctx,
		principal,
		request.InvestigationID,
		request.RevisionID,
		request.Evidence,
	)
	if err != nil {
		return nil, err
	}
	queryDigest := digestJSON(struct {
		InvestigationID string                          `json:"investigation_id"`
		RevisionID      string                          `json:"revision_id"`
		Evidence        WorkbenchChecklistEvidenceInput `json:"evidence"`
		PageSize        int                             `json:"page_size"`
	}{
		InvestigationID: request.InvestigationID,
		RevisionID:      request.RevisionID,
		Evidence:        request.Evidence,
		PageSize:        request.PageSize,
	})
	cursor, err := decodeWorkbenchChecklistCursor(
		request.Cursor,
		queryDigest,
		principal,
		build.revisionDigest,
		build.snapshotDigest,
	)
	if err != nil {
		return nil, err
	}
	offset := 0
	if cursor != nil {
		offset = cursor.Offset
	}
	if offset < 0 || offset > len(build.entries) {
		return nil, huma.Error409Conflict(
			"workbench checklist cursor no longer addresses this snapshot",
		)
	}
	end := min(offset+request.PageSize, len(build.entries))
	page := &WorkbenchChecklistPage{
		SchemaVersion:    workbenchChecklistSchemaVersion,
		InvestigationID:  request.InvestigationID,
		RevisionID:       request.RevisionID,
		TicketKind:       build.view.Brief.TicketKind,
		EvidenceSnapshot: build.evidenceSnapshot,
		Entries:          slices.Clone(build.entries[offset:end]),
		Pagination: WorkbenchChecklistPagination{
			TotalEntries: len(build.entries),
			Complete:     end == len(build.entries),
		},
		SnapshotDigest: build.snapshotDigest,
		Caveat:         workbenchChecklistCaveat,
	}
	if !page.Pagination.Complete {
		page.Pagination.NextCursor, err = encodeWorkbenchChecklistCursor(
			workbenchChecklistCursor{
				Schema:         workbenchChecklistCursorVersion,
				QueryDigest:    queryDigest,
				Principal:      principal,
				RevisionDigest: build.revisionDigest,
				SnapshotDigest: build.snapshotDigest,
				Offset:         end,
			},
		)
		if err != nil {
			return nil, err
		}
	}
	return page, nil
}

func (service *WorkbenchChecklistService) RecordDisposition(
	ctx context.Context,
	principal string,
	mutation WorkbenchDispositionMutation,
) (*store.WorkbenchDisposition, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable(
			"workbench checklist unavailable",
		)
	}
	if err := service.validatePrincipal(ctx, principal); err != nil {
		return nil, err
	}
	normalizedEvidence, err := normalizeWorkbenchChecklistEvidence(
		mutation.Evidence,
	)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	mutation.Evidence = normalizedEvidence
	if err := validateWorkbenchDispositionMutation(mutation); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	suggestion, err := store.CanonicalWorkbenchSuggestion(
		mutation.Suggestion,
	)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	mutation.Suggestion = suggestion
	storeRequest := store.WorkbenchDispositionRequest{
		ExpectedRevisionID: mutation.ExpectedRevisionID,
		IdempotencyKey:     mutation.IdempotencyKey,
		Suggestion:         suggestion,
		Category:           mutation.Category,
		Rationale:          mutation.Rationale,
		Supersedes:         mutation.Supersedes,
	}
	if _, receiptErr :=
		service.dispositions.GetWorkbenchDispositionMutationAs(
			ctx,
			principal,
			mutation.InvestigationID,
			mutation.ExpectedRevisionID,
			mutation.IdempotencyKey,
		); receiptErr == nil {
		// The store compares the complete immutable request digest. This path
		// makes exact retries survive evidence refresh without letting a new
		// fabricated suggestion bypass current-projection validation.
		return service.dispositions.AppendWorkbenchDisposition(
			ctx,
			principal,
			storeRequest,
		)
	} else if !errors.Is(receiptErr, store.ErrNotFound) {
		return nil, checklistDispositionError(receiptErr)
	}

	build, err := service.build(
		ctx,
		principal,
		mutation.InvestigationID,
		mutation.ExpectedRevisionID,
		mutation.Evidence,
	)
	if err != nil {
		return nil, err
	}
	if mutation.Supersedes == "" {
		current := false
		for _, entry := range build.entries {
			projected := entry.Suggestion
			if entry.EvidenceState == "current" &&
				projected.ID == suggestion.ID &&
				projected.ContentDigest == suggestion.ContentDigest &&
				projected.EvidenceSnapshotDigest ==
					suggestion.EvidenceSnapshotDigest {
				if entry.Disposition != nil {
					return nil, huma.Error409Conflict(
						"workbench suggestion already has an active disposition; corrections must supersede it",
					)
				}
				current = true
				break
			}
		}
		if !current {
			return nil, huma.Error409Conflict(
				"workbench suggestion is stale or is not in the current deterministic projection",
			)
		}
	} else {
		active := false
		for _, entry := range build.entries {
			if entry.Disposition != nil &&
				entry.Disposition.ID == mutation.Supersedes &&
				entry.Suggestion.ID == suggestion.ID &&
				entry.Suggestion.ContentDigest ==
					suggestion.ContentDigest {
				active = true
				break
			}
		}
		if !active {
			return nil, huma.Error409Conflict(
				"superseded workbench disposition is stale, already superseded, or out of scope",
			)
		}
	}
	value, err := service.dispositions.AppendWorkbenchDisposition(
		ctx,
		principal,
		storeRequest,
	)
	if err != nil {
		return nil, checklistDispositionError(err)
	}
	return value, nil
}

func (service *WorkbenchChecklistService) validatePrincipal(
	ctx context.Context,
	principal string,
) error {
	if strings.TrimSpace(principal) == "" ||
		service.opts.Principal == nil ||
		strings.TrimSpace(service.opts.Principal(ctx)) != principal ||
		service.workbench == nil ||
		service.impact == nil ||
		service.implementation == nil ||
		service.dispositions == nil {
		return store.ErrNotFound
	}
	return nil
}

func checklistDispositionError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.ErrNotFound
	case errors.Is(err, store.ErrInvalidWorkbenchDisposition):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, store.ErrConflict):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, store.ErrResultLimit):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		return huma.Error500InternalServerError(
			"record workbench disposition",
			err,
		)
	}
}

func normalizeWorkbenchChecklistRequest(
	request WorkbenchChecklistRequest,
) (WorkbenchChecklistRequest, error) {
	if strings.TrimSpace(request.InvestigationID) !=
		request.InvestigationID ||
		request.InvestigationID == "" ||
		strings.TrimSpace(request.RevisionID) != request.RevisionID ||
		request.RevisionID == "" {
		return request, errors.New(
			"investigation_id and revision_id are required",
		)
	}
	if request.PageSize == 0 {
		request.PageSize = workbenchChecklistDefaultPage
	}
	if request.PageSize < 1 ||
		request.PageSize > workbenchChecklistMaxPage {
		return request, fmt.Errorf(
			"page_size must be from 1 through %d",
			workbenchChecklistMaxPage,
		)
	}
	evidence, err := normalizeWorkbenchChecklistEvidence(request.Evidence)
	if err != nil {
		return request, err
	}
	request.Evidence = evidence
	return request, nil
}

func normalizeWorkbenchChecklistEvidence(
	evidence WorkbenchChecklistEvidenceInput,
) (WorkbenchChecklistEvidenceInput, error) {
	anchors, err := canonicalWorkbenchImplementationAnchors(
		evidence.Anchors,
	)
	if err != nil {
		return evidence, err
	}
	evidence.Anchors = anchors
	if strings.TrimSpace(evidence.CompatibilityRun) !=
		evidence.CompatibilityRun {
		return evidence, errors.New(
			"compatibility_run_id must be canonical",
		)
	}
	return evidence, nil
}

func validateWorkbenchDispositionMutation(
	mutation WorkbenchDispositionMutation,
) error {
	if strings.TrimSpace(mutation.InvestigationID) !=
		mutation.InvestigationID ||
		mutation.InvestigationID == "" ||
		strings.TrimSpace(mutation.ExpectedRevisionID) !=
			mutation.ExpectedRevisionID ||
		mutation.ExpectedRevisionID == "" ||
		mutation.Suggestion.InvestigationID !=
			mutation.InvestigationID ||
		mutation.Suggestion.RevisionID !=
			mutation.ExpectedRevisionID {
		return errors.New(
			"investigation, expected revision, and suggestion binding are required",
		)
	}
	if strings.TrimSpace(mutation.IdempotencyKey) !=
		mutation.IdempotencyKey ||
		mutation.IdempotencyKey == "" ||
		len(mutation.IdempotencyKey) > 256 ||
		!utf8.ValidString(mutation.IdempotencyKey) {
		return errors.New(
			"idempotency_key must be bounded canonical UTF-8",
		)
	}
	return nil
}

func validWorkbenchChecklistView(
	view *store.WorkbenchView,
	investigationID, revisionID string,
) bool {
	return view != nil &&
		view.Investigation.ID == investigationID &&
		view.Investigation.CurrentRevisionID == revisionID &&
		view.Revision.ID == revisionID &&
		view.Revision.InvestigationID == investigationID &&
		view.Brief.RevisionID == revisionID &&
		view.Brief.InvestigationID == investigationID
}

func (service *WorkbenchChecklistService) build(
	ctx context.Context,
	principal,
	investigationID,
	revisionID string,
	evidenceInput WorkbenchChecklistEvidenceInput,
) (*workbenchChecklistBuild, error) {
	view, err := service.workbench.Read(
		ctx,
		principal,
		investigationID,
	)
	if err != nil {
		return nil, err
	}
	if !validWorkbenchChecklistView(view, investigationID, revisionID) {
		return nil, store.ErrNotFound
	}
	revisionDigest := digestJSON(struct {
		Revision store.Revision    `json:"revision"`
		Brief    store.ChangeBrief `json:"brief"`
	}{Revision: view.Revision, Brief: view.Brief})
	impactPages, impactTruncated, err := service.collectImpact(
		ctx,
		principal,
		investigationID,
		revisionID,
		evidenceInput,
	)
	if err != nil {
		return nil, err
	}
	implementationPages, implementationTruncated, err :=
		service.collectImplementation(
			ctx,
			principal,
			investigationID,
			revisionID,
			evidenceInput,
		)
	if err != nil {
		return nil, err
	}
	impactDigest := digestJSON(impactPages)
	implementationDigest := digestJSON(implementationPages)
	combinedDigest := digestJSON(struct {
		Input                   WorkbenchChecklistEvidenceInput `json:"input"`
		ImpactDigest            string                          `json:"impact_digest"`
		ImplementationDigest    string                          `json:"implementation_digest"`
		ImpactTruncated         bool                            `json:"impact_truncated"`
		ImplementationTruncated bool                            `json:"implementation_truncated"`
	}{
		Input:                   evidenceInput,
		ImpactDigest:            impactDigest,
		ImplementationDigest:    implementationDigest,
		ImpactTruncated:         impactTruncated,
		ImplementationTruncated: implementationTruncated,
	})
	evidenceSnapshot := WorkbenchChecklistEvidenceSnapshot{
		ImpactDigest:            impactDigest,
		ImplementationDigest:    implementationDigest,
		CombinedDigest:          combinedDigest,
		ImpactTruncated:         impactTruncated,
		ImplementationTruncated: implementationTruncated,
	}
	raw := make([]workbenchChecklistRawSuggestion, 0)
	for _, page := range impactPages {
		raw = append(raw, rawSuggestionsFromImpact(page)...)
	}
	for _, page := range implementationPages {
		raw = append(raw, rawSuggestionsFromImplementation(page)...)
	}
	if impactTruncated {
		raw = append(raw, workbenchChecklistTruncationSuggestion(
			"impact",
			impactDigest,
		))
	}
	if implementationTruncated {
		raw = append(raw, workbenchChecklistTruncationSuggestion(
			"implementation",
			implementationDigest,
		))
	}
	suggestions, err := canonicalWorkbenchChecklistSuggestions(
		investigationID,
		revisionID,
		combinedDigest,
		raw,
	)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"derive workbench checklist suggestions",
			err,
		)
	}
	dispositions, err := service.dispositions.ListWorkbenchDispositionsAs(
		ctx,
		principal,
		investigationID,
	)
	if err != nil {
		return nil, checklistDispositionError(err)
	}
	entries, err := projectWorkbenchChecklistEntries(
		suggestions,
		dispositions,
	)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"project workbench checklist",
			err,
		)
	}
	confirmedView, err := service.workbench.Read(
		ctx,
		principal,
		investigationID,
	)
	if err != nil {
		return nil, err
	}
	if !validWorkbenchChecklistView(
		confirmedView,
		investigationID,
		revisionID,
	) {
		return nil, store.ErrNotFound
	}
	if digestJSON(struct {
		Revision store.Revision    `json:"revision"`
		Brief    store.ChangeBrief `json:"brief"`
	}{Revision: confirmedView.Revision, Brief: confirmedView.Brief}) !=
		revisionDigest {
		return nil, huma.Error409Conflict(
			"workbench revision changed while building the checklist; retry",
		)
	}
	confirmedDispositions, err :=
		service.dispositions.ListWorkbenchDispositionsAs(
			ctx,
			principal,
			investigationID,
		)
	if err != nil {
		return nil, checklistDispositionError(err)
	}
	if digestJSON(dispositions) != digestJSON(confirmedDispositions) {
		return nil, huma.Error409Conflict(
			"workbench dispositions changed while building the checklist; retry",
		)
	}
	snapshotDigest := digestJSON(struct {
		RevisionDigest   string                             `json:"revision_digest"`
		EvidenceSnapshot WorkbenchChecklistEvidenceSnapshot `json:"evidence_snapshot"`
		Entries          []WorkbenchChecklistEntry          `json:"entries"`
	}{
		RevisionDigest:   revisionDigest,
		EvidenceSnapshot: evidenceSnapshot,
		Entries:          entries,
	})
	return &workbenchChecklistBuild{
		view:             view,
		evidenceSnapshot: evidenceSnapshot,
		suggestions:      suggestions,
		dispositions:     dispositions,
		entries:          entries,
		revisionDigest:   revisionDigest,
		snapshotDigest:   snapshotDigest,
	}, nil
}

func (service *WorkbenchChecklistService) collectImpact(
	ctx context.Context,
	principal,
	investigationID,
	revisionID string,
	evidence WorkbenchChecklistEvidenceInput,
) ([]WorkbenchImpactPage, bool, error) {
	pages := make([]WorkbenchImpactPage, 0, workbenchChecklistEvidencePages)
	cursor := ""
	for pageIndex := 0; pageIndex < workbenchChecklistEvidencePages; pageIndex++ {
		page, err := service.impact.Read(
			ctx,
			principal,
			WorkbenchImpactRequest{
				InvestigationID:  investigationID,
				RevisionID:       revisionID,
				CompatibilityRun: evidence.CompatibilityRun,
				Filters:          evidence.ImpactFilters,
				PageSize:         callerMapMaxPage,
				Cursor:           cursor,
			},
		)
		if err != nil {
			return nil, false, err
		}
		if page == nil ||
			page.InvestigationID != investigationID ||
			page.RevisionID != revisionID ||
			page.SchemaVersion != workbenchImpactSchemaVersion {
			return nil, false, huma.Error409Conflict(
				"impact inventory returned an inconsistent Workbench snapshot",
			)
		}
		pages = append(pages, *page)
		if page.Pagination.Complete {
			return pages, false, nil
		}
		if page.Pagination.NextCursor == "" {
			return nil, false, huma.Error500InternalServerError(
				"impact inventory omitted its continuation cursor",
			)
		}
		cursor = page.Pagination.NextCursor
	}
	return pages, true, nil
}

func (service *WorkbenchChecklistService) collectImplementation(
	ctx context.Context,
	principal,
	investigationID,
	revisionID string,
	evidence WorkbenchChecklistEvidenceInput,
) ([]WorkbenchImplementationPage, bool, error) {
	pages := make(
		[]WorkbenchImplementationPage,
		0,
		workbenchChecklistEvidencePages,
	)
	cursor := ""
	for pageIndex := 0; pageIndex < workbenchChecklistEvidencePages; pageIndex++ {
		page, err := service.implementation.Read(
			ctx,
			principal,
			WorkbenchImplementationRequest{
				InvestigationID: investigationID,
				RevisionID:      revisionID,
				Anchors:         evidence.Anchors,
				PageSize:        workbenchImplementationMaxPage,
				Cursor:          cursor,
			},
		)
		if err != nil {
			return nil, false, err
		}
		if page == nil ||
			page.InvestigationID != investigationID ||
			page.RevisionID != revisionID ||
			page.SchemaVersion !=
				workbenchImplementationSchemaVersion {
			return nil, false, huma.Error409Conflict(
				"implementation evidence returned an inconsistent Workbench snapshot",
			)
		}
		pages = append(pages, *page)
		if page.Pagination.Complete {
			return pages, false, nil
		}
		if page.Pagination.NextCursor == "" {
			return nil, false, huma.Error500InternalServerError(
				"implementation evidence omitted its continuation cursor",
			)
		}
		cursor = page.Pagination.NextCursor
	}
	return pages, true, nil
}

func checklistEvidenceID(prefix string, value any) string {
	return prefix + "_" +
		strings.TrimPrefix(digestJSON(value), "sha256:")[:24]
}

func checklistGenericEvidence(
	plane, kind, id string,
	value any,
) store.WorkbenchSuggestionEvidence {
	return store.WorkbenchSuggestionEvidence{
		Plane:  plane,
		Kind:   kind,
		ID:     id,
		Digest: digestJSON(value),
	}
}

func checklistImplementationEvidence(
	row WorkbenchImplementationRow,
) store.WorkbenchSuggestionEvidence {
	return store.WorkbenchSuggestionEvidence{
		Plane:      "implementation",
		Kind:       row.Kind,
		ID:         row.ID,
		Digest:     digestJSON(row),
		Repository: row.Source.Repository,
		Commit:     row.Source.Commit,
		Path:       row.Source.Path,
		StartByte:  row.Source.StartByte,
		EndByte:    row.Source.EndByte,
		StartLine:  row.Source.StartLine,
		EndLine:    row.Source.EndLine,
	}
}

func checklistCallerEvidence(
	kind string,
	row CallerMapRow,
) store.WorkbenchSuggestionEvidence {
	id := checklistEvidenceID(
		"caller",
		struct {
			Kind string       `json:"kind"`
			Row  CallerMapRow `json:"row"`
		}{Kind: kind, Row: row},
	)
	return store.WorkbenchSuggestionEvidence{
		Plane:      "impact",
		Kind:       kind,
		ID:         id,
		Digest:     digestJSON(row),
		Repository: row.Source.Repository,
		Commit:     row.Source.Commit,
		Path:       row.Source.Path,
		StartByte:  row.Source.StartByte,
		EndByte:    row.Source.EndByte,
		StartLine:  row.Source.StartLine,
		EndLine:    row.Source.EndLine,
	}
}

func checklistCatalogEvidence(
	kind string,
	source ContractCatalogSource,
	value any,
) store.WorkbenchSuggestionEvidence {
	return store.WorkbenchSuggestionEvidence{
		Plane:      "impact",
		Kind:       kind,
		ID:         checklistEvidenceID(kind, source),
		Digest:     digestJSON(value),
		Repository: source.Repository,
		Commit:     source.Commit,
		Path:       source.Path,
		StartByte:  source.StartByte,
		EndByte:    source.EndByte,
		StartLine:  source.StartLine,
		EndLine:    source.EndLine,
	}
}

func boundedWorkbenchChecklistSummary(value string) string {
	value = strings.TrimSpace(value)
	const limit = 4 << 10
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value)
}

func rawSuggestionsFromImplementation(
	page WorkbenchImplementationPage,
) []workbenchChecklistRawSuggestion {
	raw := make([]workbenchChecklistRawSuggestion, 0)
	for _, row := range page.Rows {
		if row.Kind == "declaration" || row.Kind == "history_commit" {
			continue
		}
		kind := "review_related_source"
		if row.Kind == "history_diff" {
			kind = "review_historical_change"
		}
		raw = append(raw, workbenchChecklistRawSuggestion{
			kind: kind,
			summary: boundedWorkbenchChecklistSummary(fmt.Sprintf(
				"Review %s %s at %s:%s.",
				row.CodeRole,
				row.Kind,
				row.Source.Repository,
				row.Source.Path,
			)),
			selectionRule: "t21.9:implementation:" +
				row.SelectionRule,
			evidence: []store.WorkbenchSuggestionEvidence{
				checklistImplementationEvidence(row),
			},
		})
	}
	for _, gap := range page.Gaps {
		raw = append(raw, workbenchChecklistRawSuggestion{
			kind: "resolve_analysis_gap",
			summary: boundedWorkbenchChecklistSummary(fmt.Sprintf(
				"Review implementation evidence gap %s/%s for %s.",
				gap.Capability,
				gap.Code,
				gap.Target,
			)),
			selectionRule: "t21.9:implementation_gap_v1",
			evidence: []store.WorkbenchSuggestionEvidence{
				checklistGenericEvidence(
					"implementation",
					"gap",
					checklistEvidenceID("implementation_gap", gap),
					gap,
				),
			},
		})
	}
	return raw
}

func rawSuggestionsFromImpact(
	page WorkbenchImpactPage,
) []workbenchChecklistRawSuggestion {
	raw := make([]workbenchChecklistRawSuggestion, 0)
	for _, caller := range page.Callers {
		for _, row := range caller.ResolvedCallers {
			raw = append(raw, workbenchChecklistRawSuggestion{
				kind: "review_resolved_caller",
				summary: boundedWorkbenchChecklistSummary(fmt.Sprintf(
					"Review resolved caller at %s:%s for %s.",
					row.Source.Repository,
					row.Source.Path,
					row.Operation,
				)),
				selectionRule: "t21.9:resolved_caller_v1",
				evidence: []store.WorkbenchSuggestionEvidence{
					checklistCallerEvidence("resolved_caller", row),
				},
			})
		}
		for _, row := range caller.ExtractorAbstentions {
			raw = append(raw, workbenchChecklistRawSuggestion{
				kind: "resolve_caller_abstention",
				summary: boundedWorkbenchChecklistSummary(fmt.Sprintf(
					"Review unresolved caller evidence at %s:%s.",
					row.Source.Repository,
					row.Source.Path,
				)),
				selectionRule: "t21.9:caller_abstention_v1",
				evidence: []store.WorkbenchSuggestionEvidence{
					checklistCallerEvidence(
						"extractor_abstention",
						row,
					),
				},
			})
		}
	}
	for _, atlas := range page.Atlas {
		for _, relationship := range atlas.NameMatches {
			evidence := make(
				[]store.WorkbenchSuggestionEvidence,
				0,
				len(relationship.Claim.Sources),
			)
			for _, source := range relationship.Claim.Sources {
				evidence = append(
					evidence,
					checklistCatalogEvidence(
						"name_match",
						source,
						relationship,
					),
				)
			}
			if len(evidence) == 0 {
				evidence = append(evidence, checklistGenericEvidence(
					"impact",
					"name_match",
					checklistEvidenceID("name_match", relationship),
					relationship,
				))
			}
			raw = append(raw, workbenchChecklistRawSuggestion{
				kind: "review_name_match",
				summary: boundedWorkbenchChecklistSummary(fmt.Sprintf(
					"Review name match for %s.",
					atlas.Selection.CanonicalOperation,
				)),
				selectionRule: "t21.9:atlas_name_match_v1",
				evidence:      evidence,
			})
		}
		for _, relationship := range atlas.ExtractorAbstentions {
			evidence := make(
				[]store.WorkbenchSuggestionEvidence,
				0,
				len(relationship.Claim.Sources),
			)
			for _, source := range relationship.Claim.Sources {
				evidence = append(
					evidence,
					checklistCatalogEvidence(
						"extractor_abstention",
						source,
						relationship,
					),
				)
			}
			if len(evidence) == 0 {
				evidence = append(evidence, checklistGenericEvidence(
					"impact",
					"extractor_abstention",
					checklistEvidenceID(
						"extractor_abstention",
						relationship,
					),
					relationship,
				))
			}
			raw = append(raw, workbenchChecklistRawSuggestion{
				kind: "resolve_caller_abstention",
				summary: boundedWorkbenchChecklistSummary(fmt.Sprintf(
					"Review extractor abstention for %s.",
					atlas.Selection.CanonicalOperation,
				)),
				selectionRule: "t21.9:atlas_abstention_v1",
				evidence:      evidence,
			})
		}
	}
	if page.Comparison != nil {
		for _, row := range page.Comparison.Rows {
			evidence := []store.WorkbenchSuggestionEvidence{
				checklistGenericEvidence(
					"impact",
					"caller_comparison",
					checklistEvidenceID("comparison", row),
					row,
				),
			}
			for _, side := range [][]CallerMapRow{
				row.Old.Rows,
				row.Replacement.Rows,
			} {
				for _, caller := range side {
					if len(evidence) ==
						workbenchChecklistMaxEvidence {
						break
					}
					evidence = append(
						evidence,
						checklistCallerEvidence(
							"caller_comparison_source",
							caller,
						),
					)
				}
			}
			raw = append(raw, workbenchChecklistRawSuggestion{
				kind: "review_migration_evidence",
				summary: boundedWorkbenchChecklistSummary(fmt.Sprintf(
					"Review %s migration evidence for %s.",
					row.Classification,
					row.Key,
				)),
				selectionRule: "t21.9:caller_comparison_v1",
				evidence:      evidence,
			})
		}
	}
	if page.Compatibility != nil {
		for _, violation := range page.Compatibility.Violations {
			raw = append(raw, workbenchChecklistRawSuggestion{
				kind: "review_compatibility_finding",
				summary: boundedWorkbenchChecklistSummary(fmt.Sprintf(
					"Review compatibility rule %s at %s:%d.",
					violation.Rule,
					violation.Path,
					violation.StartLine,
				)),
				selectionRule: "t21.9:compatibility_violation_v1",
				evidence: []store.WorkbenchSuggestionEvidence{
					checklistGenericEvidence(
						"impact",
						"compatibility_violation",
						checklistEvidenceID(
							"compatibility",
							violation,
						),
						violation,
					),
				},
			})
		}
	}
	if page.FieldReferences != nil {
		for _, row := range page.FieldReferences.Rows {
			fieldID := strings.Join([]string{
				row.Field.Lineage,
				row.Field.Message,
				strconv.Itoa(row.Field.Number),
			}, ":")
			raw = append(raw, workbenchChecklistRawSuggestion{
				kind: "review_field_reference",
				summary: boundedWorkbenchChecklistSummary(
					"Review references to stable field " +
						fieldID + ".",
				),
				selectionRule: "t21.9:stable_field_reference_v1",
				evidence: []store.WorkbenchSuggestionEvidence{
					checklistGenericEvidence(
						"impact",
						"field_reference",
						checklistEvidenceID(
							"field_reference",
							row,
						),
						row,
					),
				},
			})
		}
	}
	for _, gap := range page.AnalysisScope.Gaps {
		raw = append(raw, workbenchChecklistRawSuggestion{
			kind: "resolve_analysis_gap",
			summary: boundedWorkbenchChecklistSummary(fmt.Sprintf(
				"Review impact gap %s/%s for %s.",
				gap.Capability,
				gap.Code,
				gap.Target,
			)),
			selectionRule: "t21.9:impact_gap_v1",
			evidence: []store.WorkbenchSuggestionEvidence{
				checklistGenericEvidence(
					"impact",
					"gap",
					checklistEvidenceID("impact_gap", gap),
					gap,
				),
			},
		})
	}
	for _, plane := range page.ResourcePlanes {
		if plane.State == ResourcePlaneEnabled {
			continue
		}
		raw = append(raw, workbenchChecklistRawSuggestion{
			kind: "resolve_resource_plane_gap",
			summary: boundedWorkbenchChecklistSummary(fmt.Sprintf(
				"Review %s resource plane state %s.",
				plane.Label,
				plane.State,
			)),
			selectionRule: "t21.9:resource_plane_gap_v1",
			evidence: []store.WorkbenchSuggestionEvidence{
				checklistGenericEvidence(
					"impact",
					"resource_plane",
					"resource_plane_"+plane.ID,
					plane,
				),
			},
		})
	}
	return raw
}

func workbenchChecklistTruncationSuggestion(
	plane, digest string,
) workbenchChecklistRawSuggestion {
	return workbenchChecklistRawSuggestion{
		kind: "review_remaining_evidence",
		summary: "Review remaining bounded " + plane +
			" evidence pages.",
		selectionRule: "t21.9:evidence_page_limit_v1",
		evidence: []store.WorkbenchSuggestionEvidence{{
			Plane:  plane,
			Kind:   "page_limit",
			ID:     plane + "_page_limit",
			Digest: digest,
		}},
	}
}

func workbenchChecklistRawKey(
	raw workbenchChecklistRawSuggestion,
) string {
	evidence := slices.Clone(raw.evidence)
	sort.Slice(evidence, func(left, right int) bool {
		return digestJSON(evidence[left]) < digestJSON(evidence[right])
	})
	return raw.kind + "|" +
		raw.selectionRule + "|" +
		raw.summary + "|" +
		digestJSON(evidence)
}

func canonicalWorkbenchChecklistSuggestions(
	investigationID,
	revisionID,
	snapshotDigest string,
	raw []workbenchChecklistRawSuggestion,
) ([]store.WorkbenchSuggestion, error) {
	raw = slices.Clone(raw)
	sort.Slice(raw, func(left, right int) bool {
		return workbenchChecklistRawKey(raw[left]) <
			workbenchChecklistRawKey(raw[right])
	})
	raw = slices.CompactFunc(
		raw,
		func(
			left,
			right workbenchChecklistRawSuggestion,
		) bool {
			return workbenchChecklistRawKey(left) ==
				workbenchChecklistRawKey(right)
		},
	)
	if len(raw) > workbenchChecklistMaxSuggestions {
		raw = raw[:workbenchChecklistMaxSuggestions-1]
		raw = append(raw, workbenchChecklistRawSuggestion{
			kind: "review_suggestion_limit",
			summary: "Review additional evidence omitted by the " +
				"bounded checklist suggestion limit.",
			selectionRule: "t21.9:suggestion_limit_v1",
			evidence: []store.WorkbenchSuggestionEvidence{{
				Plane:  "impact",
				Kind:   "suggestion_limit",
				ID:     "checklist_suggestion_limit",
				Digest: snapshotDigest,
			}},
		})
	}
	suggestions := make(
		[]store.WorkbenchSuggestion,
		0,
		len(raw),
	)
	for _, candidate := range raw {
		suggestion, err := store.CanonicalWorkbenchSuggestion(
			store.WorkbenchSuggestion{
				SchemaVersion:          store.WorkbenchSuggestionSchemaVersion,
				InvestigationID:        investigationID,
				RevisionID:             revisionID,
				Kind:                   candidate.kind,
				Summary:                candidate.summary,
				SelectionRule:          candidate.selectionRule,
				EvidenceSnapshotDigest: snapshotDigest,
				Evidence:               candidate.evidence,
			},
		)
		if err != nil {
			return nil, err
		}
		suggestions = append(suggestions, suggestion)
	}
	sort.Slice(suggestions, func(left, right int) bool {
		return suggestions[left].ID < suggestions[right].ID
	})
	return suggestions, nil
}

func projectWorkbenchChecklistEntries(
	suggestions []store.WorkbenchSuggestion,
	dispositions []store.WorkbenchDisposition,
) ([]WorkbenchChecklistEntry, error) {
	current := make(map[string]store.WorkbenchSuggestion, len(suggestions))
	for _, suggestion := range suggestions {
		if _, exists := current[suggestion.ID]; exists {
			return nil, errors.New(
				"duplicate current workbench suggestion",
			)
		}
		current[suggestion.ID] = suggestion
	}
	history := make(map[string][]store.WorkbenchDisposition)
	for _, disposition := range dispositions {
		suggestion, err := store.CanonicalWorkbenchSuggestion(
			disposition.Suggestion,
		)
		if err != nil ||
			suggestion.ID != disposition.Suggestion.ID {
			return nil, errors.New(
				"workbench disposition contains an invalid suggestion",
			)
		}
		history[suggestion.ID] = append(
			history[suggestion.ID],
			disposition,
		)
	}
	entries := make([]WorkbenchChecklistEntry, 0, len(current)+len(history))
	seen := make(map[string]struct{}, len(current)+len(history))
	for suggestionID, suggestion := range current {
		entry, err := projectWorkbenchChecklistEntry(
			suggestion,
			"current",
			history[suggestionID],
		)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
		seen[suggestionID] = struct{}{}
	}
	for suggestionID, dispositionHistory := range history {
		if _, exists := seen[suggestionID]; exists {
			continue
		}
		entry, err := projectWorkbenchChecklistEntry(
			dispositionHistory[0].Suggestion,
			"stale",
			dispositionHistory,
		)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].EvidenceState != entries[right].EvidenceState {
			return entries[left].EvidenceState == "current"
		}
		return entries[left].Suggestion.ID <
			entries[right].Suggestion.ID
	})
	return entries, nil
}

func projectWorkbenchChecklistEntry(
	suggestion store.WorkbenchSuggestion,
	evidenceState string,
	history []store.WorkbenchDisposition,
) (WorkbenchChecklistEntry, error) {
	history = slices.Clone(history)
	sort.Slice(history, func(left, right int) bool {
		if history[left].Sequence != history[right].Sequence {
			return history[left].Sequence < history[right].Sequence
		}
		return history[left].ID < history[right].ID
	})
	for index, disposition := range history {
		if disposition.Suggestion.ID != suggestion.ID ||
			disposition.Sequence != index+1 ||
			index == 0 && disposition.Supersedes != "" ||
			index > 0 &&
				disposition.Supersedes != history[index-1].ID {
			return WorkbenchChecklistEntry{}, errors.New(
				"workbench disposition chain is inconsistent",
			)
		}
	}
	entry := WorkbenchChecklistEntry{
		Suggestion:         suggestion,
		EvidenceState:      evidenceState,
		State:              "unaccepted",
		DispositionHistory: history,
	}
	if len(history) > 0 {
		latest := history[len(history)-1]
		entry.State = latest.Category
		entry.Disposition = &latest
	}
	return entry, nil
}

func decodeWorkbenchChecklistCursor(
	raw,
	queryDigest,
	principal,
	revisionDigest,
	snapshotDigest string,
) (*workbenchChecklistCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > workbenchChecklistCursorLimit {
		return nil, huma.Error400BadRequest(
			"workbench checklist cursor is too large",
		)
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) > workbenchChecklistCursorLimit {
		return nil, huma.Error400BadRequest(
			"invalid workbench checklist cursor",
		)
	}
	var cursor workbenchChecklistCursor
	if json.Unmarshal(data, &cursor) != nil {
		return nil, huma.Error400BadRequest(
			"invalid workbench checklist cursor",
		)
	}
	checksum := cursor.Checksum
	cursor.Checksum = ""
	if checksum == "" || checksum != digestJSON(cursor) {
		return nil, huma.Error400BadRequest(
			"invalid workbench checklist cursor checksum",
		)
	}
	cursor.Checksum = checksum
	if cursor.Schema != workbenchChecklistCursorVersion ||
		cursor.QueryDigest != queryDigest ||
		cursor.Principal != principal ||
		cursor.RevisionDigest != revisionDigest {
		return nil, huma.Error400BadRequest(
			"workbench checklist cursor does not match this request",
		)
	}
	if cursor.SnapshotDigest != snapshotDigest {
		return nil, huma.Error409Conflict(
			"workbench checklist changed since the prior page; restart pagination",
		)
	}
	return &cursor, nil
}

func encodeWorkbenchChecklistCursor(
	cursor workbenchChecklistCursor,
) (string, error) {
	cursor.Checksum = ""
	cursor.Checksum = digestJSON(cursor)
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", huma.Error500InternalServerError(
			"encode workbench checklist cursor",
			err,
		)
	}
	if len(data) > workbenchChecklistCursorLimit {
		return "", huma.Error500InternalServerError(
			"workbench checklist cursor exceeded its limit",
		)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
