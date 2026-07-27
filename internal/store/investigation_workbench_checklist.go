package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const (
	WorkbenchSuggestionSchemaVersion  = "workbench-suggestion-v1"
	WorkbenchDispositionSchemaVersion = "workbench-disposition-v1"

	maxWorkbenchSuggestionEvidence     = 32
	maxWorkbenchDispositionRecords     = 1_000
	maxWorkbenchDispositionSequence    = 64
	maxWorkbenchSuggestionSummaryBytes = 4 << 10
	maxWorkbenchSuggestionRuleBytes    = 4 << 10
	maxWorkbenchDispositionTextBytes   = 16 << 10
)

var ErrInvalidWorkbenchDisposition = errors.New(
	"invalid workbench disposition",
)

var workbenchSuggestionKinds = map[string]struct{}{
	"review_related_source":        {},
	"review_historical_change":     {},
	"review_resolved_caller":       {},
	"resolve_caller_abstention":    {},
	"review_name_match":            {},
	"review_migration_evidence":    {},
	"review_compatibility_finding": {},
	"review_field_reference":       {},
	"resolve_analysis_gap":         {},
	"resolve_resource_plane_gap":   {},
	"review_remaining_evidence":    {},
	"review_suggestion_limit":      {},
}

var workbenchDispositionCategories = map[string]struct{}{
	"accepted":  {},
	"rejected":  {},
	"completed": {},
	"reopened":  {},
	"waived":    {},
}

// WorkbenchSuggestionEvidence is one immutable pointer back to the exact
// Workbench evidence that caused a deterministic suggestion. Source fields
// are optional for non-source findings, but ID and Digest are always present.
type WorkbenchSuggestionEvidence struct {
	Plane      string `json:"plane"`
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Digest     string `json:"digest"`
	Repository string `json:"repository,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Path       string `json:"path,omitempty"`
	StartByte  int    `json:"start_byte,omitempty"`
	EndByte    int    `json:"end_byte,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
}

// WorkbenchSuggestion is a machine projection, not an authored task. Its ID
// includes the originating Workbench revision, the complete evidence snapshot
// digest, the deterministic rule, and canonical evidence references.
type WorkbenchSuggestion struct {
	SchemaVersion          string                        `json:"schema_version"`
	ID                     string                        `json:"suggestion_id"`
	InvestigationID        string                        `json:"investigation_id"`
	RevisionID             string                        `json:"revision_id"`
	Kind                   string                        `json:"kind"`
	Summary                string                        `json:"summary"`
	SelectionRule          string                        `json:"selection_rule"`
	EvidenceSnapshotDigest string                        `json:"evidence_snapshot_digest"`
	Evidence               []WorkbenchSuggestionEvidence `json:"evidence"`
	ContentDigest          string                        `json:"content_digest"`
}

// WorkbenchDisposition is the only human-authored checklist state. Records
// are immutable; corrections and reopenings append one successor.
type WorkbenchDisposition struct {
	SchemaVersion   string              `json:"schema_version"`
	ID              string              `json:"disposition_id"`
	InvestigationID string              `json:"investigation_id"`
	RevisionID      string              `json:"revision_id"`
	Suggestion      WorkbenchSuggestion `json:"suggestion"`
	Category        string              `json:"category"`
	Actor           string              `json:"actor"`
	Authority       string              `json:"authority"`
	Rationale       string              `json:"rationale,omitempty"`
	Sequence        int                 `json:"sequence"`
	Supersedes      string              `json:"supersedes,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	ContentDigest   string              `json:"content_digest"`
}

// WorkbenchDispositionRequest is an internal persistence command. Transport
// adapters must call the shared checklist service, which proves that
// Suggestion belongs to the current deterministic projection before this
// owner/CAS boundary is reached.
type WorkbenchDispositionRequest struct {
	ExpectedRevisionID string
	IdempotencyKey     string
	Suggestion         WorkbenchSuggestion
	Category           string
	Rationale          string
	Supersedes         string
}

type WorkbenchDispositionStore interface {
	ListWorkbenchDispositionsAs(
		context.Context,
		string,
		string,
	) ([]WorkbenchDisposition, error)
	GetWorkbenchDispositionMutationAs(
		context.Context,
		string,
		string,
		string,
		string,
	) (*WorkbenchDisposition, error)
	AppendWorkbenchDisposition(
		context.Context,
		string,
		WorkbenchDispositionRequest,
	) (*WorkbenchDisposition, error)
}

var _ WorkbenchDispositionStore = (*Surreal)(nil)

type workbenchSuggestionCore struct {
	SchemaVersion          string                        `json:"schema_version"`
	InvestigationID        string                        `json:"investigation_id"`
	RevisionID             string                        `json:"revision_id"`
	Kind                   string                        `json:"kind"`
	Summary                string                        `json:"summary"`
	SelectionRule          string                        `json:"selection_rule"`
	EvidenceSnapshotDigest string                        `json:"evidence_snapshot_digest"`
	Evidence               []WorkbenchSuggestionEvidence `json:"evidence"`
}

func workbenchSuggestionEvidenceKey(
	evidence WorkbenchSuggestionEvidence,
) string {
	return strings.Join([]string{
		evidence.Plane,
		evidence.Kind,
		evidence.ID,
		evidence.Digest,
		evidence.Repository,
		evidence.Commit,
		evidence.Path,
		fmt.Sprintf(
			"%010d|%010d|%010d|%010d",
			evidence.StartByte,
			evidence.EndByte,
			evidence.StartLine,
			evidence.EndLine,
		),
	}, "|")
}

func workbenchSuggestionEvidenceIdentity(
	evidence WorkbenchSuggestionEvidence,
) string {
	return evidence.Plane + "|" + evidence.Kind + "|" + evidence.ID
}

func validWorkbenchObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func validateWorkbenchSuggestionEvidence(
	evidence WorkbenchSuggestionEvidence,
) error {
	if evidence.Plane != "impact" &&
		evidence.Plane != "implementation" {
		return errors.New("suggestion evidence plane is invalid")
	}
	for name, value := range map[string]string{
		"evidence kind": evidence.Kind,
		"evidence id":   evidence.ID,
	} {
		if strings.TrimSpace(value) != value {
			return fmt.Errorf("%s is not canonical", name)
		}
		if err := validateDomainString(name, value, true); err != nil {
			return err
		}
	}
	if !validSHA256Digest(evidence.Digest) {
		return errors.New("suggestion evidence digest is invalid")
	}
	if evidence.Repository == "" {
		if evidence.Commit != "" ||
			evidence.Path != "" ||
			evidence.StartByte != 0 ||
			evidence.EndByte != 0 ||
			evidence.StartLine != 0 ||
			evidence.EndLine != 0 {
			return errors.New(
				"suggestion evidence source fields are incomplete",
			)
		}
		return nil
	}
	if strings.TrimSpace(evidence.Repository) != evidence.Repository ||
		strings.TrimSpace(evidence.Path) != evidence.Path ||
		!validWorkbenchObjectID(evidence.Commit) ||
		evidence.Path == "" ||
		len(evidence.Path) > 4096 ||
		evidence.StartByte < 0 ||
		(evidence.EndByte != 0 &&
			evidence.EndByte < evidence.StartByte) ||
		evidence.StartLine < 0 ||
		(evidence.EndLine != 0 &&
			evidence.EndLine < evidence.StartLine) {
		return errors.New("suggestion evidence source is invalid")
	}
	for _, character := range evidence.Path {
		if character < 0x20 || character == 0x7f {
			return errors.New("suggestion evidence path is invalid")
		}
	}
	return nil
}

// CanonicalWorkbenchSuggestion validates, sorts, and content-addresses one
// deterministic suggestion. Callers may provide an empty ID/digest when
// constructing it; a non-empty value must match the canonical result.
func CanonicalWorkbenchSuggestion(
	suggestion WorkbenchSuggestion,
) (WorkbenchSuggestion, error) {
	if suggestion.SchemaVersion == "" {
		suggestion.SchemaVersion = WorkbenchSuggestionSchemaVersion
	}
	if suggestion.SchemaVersion != WorkbenchSuggestionSchemaVersion ||
		!validULID(suggestion.InvestigationID) ||
		!strings.HasPrefix(suggestion.RevisionID, "ivr_") {
		return WorkbenchSuggestion{}, fmt.Errorf(
			"suggestion identity is invalid: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	if _, ok := workbenchSuggestionKinds[suggestion.Kind]; !ok {
		return WorkbenchSuggestion{}, fmt.Errorf(
			"suggestion kind is invalid: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	if strings.TrimSpace(suggestion.Summary) != suggestion.Summary ||
		suggestion.Summary == "" ||
		!utf8.ValidString(suggestion.Summary) ||
		len(suggestion.Summary) > maxWorkbenchSuggestionSummaryBytes ||
		strings.TrimSpace(suggestion.SelectionRule) !=
			suggestion.SelectionRule ||
		suggestion.SelectionRule == "" ||
		!utf8.ValidString(suggestion.SelectionRule) ||
		len(suggestion.SelectionRule) >
			maxWorkbenchSuggestionRuleBytes ||
		!validSHA256Digest(suggestion.EvidenceSnapshotDigest) {
		return WorkbenchSuggestion{}, fmt.Errorf(
			"suggestion content is invalid: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	if len(suggestion.Evidence) == 0 ||
		len(suggestion.Evidence) > maxWorkbenchSuggestionEvidence {
		return WorkbenchSuggestion{}, fmt.Errorf(
			"suggestion evidence must contain 1..%d entries: %w",
			maxWorkbenchSuggestionEvidence,
			ErrInvalidWorkbenchDisposition,
		)
	}
	evidence := slices.Clone(suggestion.Evidence)
	for _, reference := range evidence {
		if err := validateWorkbenchSuggestionEvidence(reference); err != nil {
			return WorkbenchSuggestion{}, fmt.Errorf(
				"suggestion evidence: %w: %w",
				err,
				ErrInvalidWorkbenchDisposition,
			)
		}
	}
	sort.Slice(evidence, func(left, right int) bool {
		return workbenchSuggestionEvidenceKey(evidence[left]) <
			workbenchSuggestionEvidenceKey(evidence[right])
	})
	canonical := evidence[:0]
	for _, reference := range evidence {
		if len(canonical) > 0 &&
			workbenchSuggestionEvidenceIdentity(
				canonical[len(canonical)-1],
			) == workbenchSuggestionEvidenceIdentity(reference) {
			if workbenchSuggestionEvidenceKey(
				canonical[len(canonical)-1],
			) != workbenchSuggestionEvidenceKey(reference) {
				return WorkbenchSuggestion{}, fmt.Errorf(
					"suggestion evidence identity conflicts: %w",
					ErrInvalidWorkbenchDisposition,
				)
			}
			continue
		}
		canonical = append(canonical, reference)
	}
	suggestion.Evidence = canonical
	core := workbenchSuggestionCore{
		SchemaVersion:          suggestion.SchemaVersion,
		InvestigationID:        suggestion.InvestigationID,
		RevisionID:             suggestion.RevisionID,
		Kind:                   suggestion.Kind,
		Summary:                suggestion.Summary,
		SelectionRule:          suggestion.SelectionRule,
		EvidenceSnapshotDigest: suggestion.EvidenceSnapshotDigest,
		Evidence:               suggestion.Evidence,
	}
	digest, err := contentDigest(core)
	if err != nil {
		return WorkbenchSuggestion{}, err
	}
	id := "wbs_" + strings.TrimPrefix(digest, "sha256:")
	if suggestion.ID != "" && suggestion.ID != id ||
		suggestion.ContentDigest != "" &&
			suggestion.ContentDigest != digest {
		return WorkbenchSuggestion{}, fmt.Errorf(
			"suggestion digest or id is inconsistent: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	suggestion.ID = id
	suggestion.ContentDigest = digest
	return suggestion, nil
}

func normalizeWorkbenchDispositionText(
	value string,
) (string, error) {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) ||
		len(value) > maxWorkbenchDispositionTextBytes {
		return "", fmt.Errorf(
			"rationale is not bounded valid UTF-8: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	for _, character := range value {
		if character < 0x20 &&
			character != '\n' &&
			character != '\t' {
			return "", fmt.Errorf(
				"rationale contains control text: %w",
				ErrInvalidWorkbenchDisposition,
			)
		}
	}
	return value, nil
}

func workbenchDispositionRationaleRequired(category string) bool {
	switch category {
	case "rejected", "reopened", "waived":
		return true
	default:
		return false
	}
}

type workbenchDispositionCore struct {
	SchemaVersion   string              `json:"schema_version"`
	ID              string              `json:"disposition_id"`
	InvestigationID string              `json:"investigation_id"`
	RevisionID      string              `json:"revision_id"`
	Suggestion      WorkbenchSuggestion `json:"suggestion"`
	Category        string              `json:"category"`
	Actor           string              `json:"actor"`
	Authority       string              `json:"authority"`
	Rationale       string              `json:"rationale"`
	Sequence        int                 `json:"sequence"`
	Supersedes      string              `json:"supersedes"`
}

func normalizeWorkbenchDisposition(
	disposition WorkbenchDisposition,
) (WorkbenchDisposition, error) {
	if disposition.SchemaVersion == "" {
		disposition.SchemaVersion = WorkbenchDispositionSchemaVersion
	}
	suggestion, err := CanonicalWorkbenchSuggestion(
		disposition.Suggestion,
	)
	if err != nil {
		return WorkbenchDisposition{}, err
	}
	disposition.Suggestion = suggestion
	rationale, err := normalizeWorkbenchDispositionText(
		disposition.Rationale,
	)
	if err != nil {
		return WorkbenchDisposition{}, err
	}
	disposition.Rationale = rationale
	if disposition.SchemaVersion != WorkbenchDispositionSchemaVersion ||
		!strings.HasPrefix(disposition.ID, "wbd_") ||
		len(disposition.ID) != len("wbd_")+64 ||
		disposition.InvestigationID != suggestion.InvestigationID ||
		disposition.RevisionID != suggestion.RevisionID ||
		!validInvestigationPrincipal(disposition.Actor) ||
		disposition.Authority != "investigation_owner" ||
		disposition.Sequence < 1 ||
		disposition.Sequence > maxWorkbenchDispositionSequence ||
		disposition.CreatedAt.IsZero() {
		return WorkbenchDisposition{}, fmt.Errorf(
			"workbench disposition identity is invalid: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	if _, ok := workbenchDispositionCategories[disposition.Category]; !ok {
		return WorkbenchDisposition{}, fmt.Errorf(
			"workbench disposition category is invalid: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	if workbenchDispositionRationaleRequired(disposition.Category) &&
		disposition.Rationale == "" {
		return WorkbenchDisposition{}, fmt.Errorf(
			"rationale is required for %s: %w",
			disposition.Category,
			ErrInvalidWorkbenchDisposition,
		)
	}
	if disposition.Category == "reopened" &&
		disposition.Supersedes == "" {
		return WorkbenchDisposition{}, fmt.Errorf(
			"reopened must supersede a prior disposition: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	if disposition.Sequence == 1 && disposition.Supersedes != "" ||
		disposition.Sequence > 1 &&
			(!strings.HasPrefix(disposition.Supersedes, "wbd_") ||
				len(disposition.Supersedes) != len("wbd_")+64) {
		return WorkbenchDisposition{}, fmt.Errorf(
			"workbench disposition supersession is invalid: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	disposition.CreatedAt = storeTimestamp(disposition.CreatedAt)
	digest, err := contentDigest(workbenchDispositionCore{
		SchemaVersion:   disposition.SchemaVersion,
		ID:              disposition.ID,
		InvestigationID: disposition.InvestigationID,
		RevisionID:      disposition.RevisionID,
		Suggestion:      disposition.Suggestion,
		Category:        disposition.Category,
		Actor:           disposition.Actor,
		Authority:       disposition.Authority,
		Rationale:       disposition.Rationale,
		Sequence:        disposition.Sequence,
		Supersedes:      disposition.Supersedes,
	})
	if err != nil {
		return WorkbenchDisposition{}, err
	}
	if disposition.ContentDigest != "" &&
		disposition.ContentDigest != digest {
		return WorkbenchDisposition{}, fmt.Errorf(
			"workbench disposition digest is inconsistent: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	disposition.ContentDigest = digest
	return disposition, nil
}

type workbenchDispositionRecord struct {
	SchemaVersion    string    `json:"schema_version"`
	DispositionID    string    `json:"disposition_id"`
	Principal        string    `json:"principal"`
	IdempotencyKey   string    `json:"idempotency_key"`
	RequestDigest    string    `json:"request_digest"`
	InvestigationID  string    `json:"investigation_id"`
	RevisionID       string    `json:"revision_id"`
	SuggestionID     string    `json:"suggestion_id"`
	SuggestionDigest string    `json:"suggestion_digest"`
	SuggestionJSON   string    `json:"suggestion_json"`
	Category         string    `json:"category"`
	Actor            string    `json:"actor"`
	Authority        string    `json:"authority"`
	Rationale        string    `json:"rationale"`
	Sequence         int       `json:"sequence"`
	Supersedes       string    `json:"supersedes,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	ContentDigest    string    `json:"content_digest"`
}

func workbenchDispositionRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_workbench_disposition", id)
}

func workbenchDispositionMutationID(
	principal, idempotencyKey string,
) string {
	return hashIdentity("wbd_", principal, idempotencyKey)
}

func decodeWorkbenchDispositionRecord(
	record workbenchDispositionRecord,
) (WorkbenchDisposition, error) {
	var suggestion WorkbenchSuggestion
	decoder := json.NewDecoder(strings.NewReader(record.SuggestionJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suggestion); err != nil {
		return WorkbenchDisposition{}, fmt.Errorf(
			"decode stored workbench suggestion: %w",
			err,
		)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return WorkbenchDisposition{}, errors.New(
				"decode stored workbench suggestion: trailing data",
			)
		}
		return WorkbenchDisposition{}, fmt.Errorf(
			"decode stored workbench suggestion trailing data: %w",
			err,
		)
	}
	disposition, err := normalizeWorkbenchDisposition(
		WorkbenchDisposition{
			SchemaVersion:   record.SchemaVersion,
			ID:              record.DispositionID,
			InvestigationID: record.InvestigationID,
			RevisionID:      record.RevisionID,
			Suggestion:      suggestion,
			Category:        record.Category,
			Actor:           record.Actor,
			Authority:       record.Authority,
			Rationale:       record.Rationale,
			Sequence:        record.Sequence,
			Supersedes:      record.Supersedes,
			CreatedAt:       record.CreatedAt,
			ContentDigest:   record.ContentDigest,
		},
	)
	if err != nil {
		return WorkbenchDisposition{}, err
	}
	if record.SuggestionID != disposition.Suggestion.ID ||
		record.SuggestionDigest != disposition.Suggestion.ContentDigest ||
		record.Principal != disposition.Actor ||
		record.DispositionID != workbenchDispositionMutationID(
			record.Principal,
			record.IdempotencyKey,
		) {
		return WorkbenchDisposition{}, errors.New(
			"stored workbench disposition binding is inconsistent",
		)
	}
	requestDigest, err := contentDigest(workbenchDispositionRequestCore{
		Principal:          record.Principal,
		ExpectedRevisionID: record.RevisionID,
		IdempotencyKey:     record.IdempotencyKey,
		Suggestion:         disposition.Suggestion,
		Category:           disposition.Category,
		Rationale:          disposition.Rationale,
		Supersedes:         disposition.Supersedes,
	})
	if err != nil || requestDigest != record.RequestDigest {
		return WorkbenchDisposition{}, errors.New(
			"stored workbench disposition request digest is inconsistent",
		)
	}
	return disposition, nil
}

func (s *Surreal) getWorkbenchDispositionRecord(
	ctx context.Context,
	id string,
) (*workbenchDispositionRecord, error) {
	if !strings.HasPrefix(id, "wbd_") ||
		len(id) != len("wbd_")+64 {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]workbenchDispositionRecord](
		ctx,
		s.db,
		`SELECT schema_version, disposition_id, principal, idempotency_key,
			request_digest, investigation_id, revision_id, suggestion_id,
			suggestion_digest, suggestion_json, category, actor, authority,
			rationale, sequence, supersedes, created_at, content_digest
			FROM $rid LIMIT 1`,
		map[string]any{"rid": workbenchDispositionRecordID(id)},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get workbench disposition record: %w",
			err,
		)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func (s *Surreal) authorizeWorkbenchDispositionOwner(
	ctx context.Context,
	principal, investigationID, expectedRevisionID string,
) (*Investigation, error) {
	investigation, err := s.GetInvestigationAs(
		ctx,
		principal,
		investigationID,
	)
	if err != nil || investigation.Owner != principal {
		return nil, ErrNotFound
	}
	if investigation.Lifecycle == "archived" ||
		investigation.CurrentRevisionID != expectedRevisionID {
		return nil, fmt.Errorf(
			"workbench revision is not current: %w",
			ErrConflict,
		)
	}
	revision, err := s.GetRevisionAs(
		ctx,
		principal,
		expectedRevisionID,
	)
	if err != nil ||
		revision.InvestigationID != investigationID {
		return nil, fmt.Errorf(
			"workbench revision is unavailable: %w",
			ErrConflict,
		)
	}
	return investigation, nil
}

func validWorkbenchDispositionIdempotencyKey(value string) bool {
	return value != "" &&
		strings.TrimSpace(value) == value &&
		utf8.ValidString(value) &&
		len(value) <= maxWorkbenchIDBytes
}

// GetWorkbenchDispositionMutationAs resolves an idempotency receipt only
// after owner authority and the expected current revision are rechecked.
func (s *Surreal) GetWorkbenchDispositionMutationAs(
	ctx context.Context,
	principal,
	investigationID,
	expectedRevisionID,
	idempotencyKey string,
) (*WorkbenchDisposition, error) {
	if !validWorkbenchDispositionIdempotencyKey(idempotencyKey) {
		return nil, ErrNotFound
	}
	if _, err := s.authorizeWorkbenchDispositionOwner(
		ctx,
		principal,
		investigationID,
		expectedRevisionID,
	); err != nil {
		return nil, err
	}
	record, err := s.getWorkbenchDispositionRecord(
		ctx,
		workbenchDispositionMutationID(principal, idempotencyKey),
	)
	if err != nil {
		return nil, err
	}
	if record.Principal != principal ||
		record.IdempotencyKey != idempotencyKey ||
		record.InvestigationID != investigationID ||
		record.RevisionID != expectedRevisionID {
		return nil, errors.New(
			"stored workbench disposition receipt is inconsistent",
		)
	}
	disposition, err := decodeWorkbenchDispositionRecord(*record)
	if err != nil {
		return nil, err
	}
	if _, err := s.authorizeWorkbenchDispositionOwner(
		ctx,
		principal,
		investigationID,
		expectedRevisionID,
	); err != nil {
		return nil, err
	}
	return &disposition, nil
}

type workbenchDispositionRequestCore struct {
	Principal          string              `json:"principal"`
	ExpectedRevisionID string              `json:"expected_revision_id"`
	IdempotencyKey     string              `json:"idempotency_key"`
	Suggestion         WorkbenchSuggestion `json:"suggestion"`
	Category           string              `json:"category"`
	Rationale          string              `json:"rationale"`
	Supersedes         string              `json:"supersedes"`
}

func normalizeWorkbenchDispositionRequest(
	principal string,
	request WorkbenchDispositionRequest,
) (WorkbenchDispositionRequest, string, error) {
	if !validInvestigationPrincipal(principal) ||
		!validWorkbenchDispositionIdempotencyKey(
			request.IdempotencyKey,
		) {
		return request, "", fmt.Errorf(
			"principal or idempotency key is invalid: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	suggestion, err := CanonicalWorkbenchSuggestion(request.Suggestion)
	if err != nil {
		return request, "", err
	}
	request.Suggestion = suggestion
	if request.ExpectedRevisionID != suggestion.RevisionID {
		return request, "", fmt.Errorf(
			"expected revision does not match suggestion: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	if _, ok := workbenchDispositionCategories[request.Category]; !ok {
		return request, "", fmt.Errorf(
			"category is invalid: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	request.Rationale, err = normalizeWorkbenchDispositionText(
		request.Rationale,
	)
	if err != nil {
		return request, "", err
	}
	if workbenchDispositionRationaleRequired(request.Category) &&
		request.Rationale == "" {
		return request, "", fmt.Errorf(
			"rationale is required for %s: %w",
			request.Category,
			ErrInvalidWorkbenchDisposition,
		)
	}
	if request.Category == "reopened" &&
		request.Supersedes == "" {
		return request, "", fmt.Errorf(
			"reopened must supersede a prior disposition: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	if request.Supersedes != "" &&
		(!strings.HasPrefix(request.Supersedes, "wbd_") ||
			len(request.Supersedes) != len("wbd_")+64) {
		return request, "", fmt.Errorf(
			"supersedes is invalid: %w",
			ErrInvalidWorkbenchDisposition,
		)
	}
	digest, err := contentDigest(workbenchDispositionRequestCore{
		Principal:          principal,
		ExpectedRevisionID: request.ExpectedRevisionID,
		IdempotencyKey:     request.IdempotencyKey,
		Suggestion:         suggestion,
		Category:           request.Category,
		Rationale:          request.Rationale,
		Supersedes:         request.Supersedes,
	})
	return request, digest, err
}

const appendWorkbenchDispositionSQL = `
BEGIN;
LET $authorized = SELECT VALUE investigation_id FROM $investigation_rid
	WHERE investigation_id = $investigation_id AND owner = $principal
	  AND lifecycle != 'archived'
	  AND (current_revision_id ?? '') = $expected_revision_id
	  AND array::len(SELECT VALUE revision_id FROM $revision_rid
		WHERE revision_id = $expected_revision_id
		  AND investigation_id = $investigation_id LIMIT 1) = 1
	LIMIT 1;
LET $existing = SELECT schema_version, disposition_id, principal,
	idempotency_key, request_digest, investigation_id, revision_id,
	suggestion_id, suggestion_digest, suggestion_json, category, actor,
	authority, rationale, sequence, supersedes, created_at, content_digest
	FROM $disposition_rid LIMIT 1;
LET $prior_valid = IF $supersedes = '' THEN
	array::len(SELECT VALUE disposition_id
		FROM investigation_workbench_disposition
		WHERE investigation_id = $investigation_id
		  AND revision_id = $expected_revision_id
		  AND suggestion_id = $suggestion_id LIMIT 1) = 0
	ELSE
	array::len(SELECT VALUE disposition_id FROM $supersedes_rid
		WHERE disposition_id = $supersedes
		  AND investigation_id = $investigation_id
		  AND revision_id = $expected_revision_id
		  AND suggestion_id = $suggestion_id
		  AND suggestion_digest = $suggestion_digest
		  AND sequence = $prior_sequence
		  AND array::len(SELECT VALUE disposition_id
			FROM investigation_workbench_disposition
			WHERE supersedes = $supersedes LIMIT 1) = 0
		LIMIT 1) = 1 END;
LET $created = IF array::len($authorized) = 1
	AND array::len($existing) = 0 AND $prior_valid THEN
	(CREATE $disposition_rid SET schema_version = $schema_version,
		disposition_id = $disposition_id, principal = $principal,
		idempotency_key = $idempotency_key, request_digest = $request_digest,
		investigation_id = $investigation_id, revision_id = $expected_revision_id,
		suggestion_id = $suggestion_id, suggestion_digest = $suggestion_digest,
		suggestion_json = $suggestion_json, category = $category,
		actor = $principal, authority = 'investigation_owner',
		rationale = $rationale, sequence = $sequence,
		supersedes = IF $supersedes = '' THEN NONE ELSE $supersedes END,
		created_at = $now, content_digest = $content_digest RETURN AFTER)
	ELSE [] END;
LET $saved = IF array::len($authorized) != 1 THEN []
	ELSE IF array::len($existing) = 1 THEN $existing ELSE $created END;
IF array::len($created) = 1 {
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 201, created_at = $now RETURN NONE;
};
RETURN $saved;
COMMIT;`

func (s *Surreal) AppendWorkbenchDisposition(
	ctx context.Context,
	principal string,
	request WorkbenchDispositionRequest,
) (*WorkbenchDisposition, error) {
	request, requestDigest, err :=
		normalizeWorkbenchDispositionRequest(principal, request)
	if err != nil {
		return nil, err
	}
	if _, err := s.authorizeWorkbenchDispositionOwner(
		ctx,
		principal,
		request.Suggestion.InvestigationID,
		request.ExpectedRevisionID,
	); err != nil {
		return nil, err
	}
	dispositionID := workbenchDispositionMutationID(
		principal,
		request.IdempotencyKey,
	)
	if existing, getErr := s.getWorkbenchDispositionRecord(
		ctx,
		dispositionID,
	); getErr == nil {
		if existing.RequestDigest != requestDigest {
			return nil, fmt.Errorf(
				"workbench disposition idempotency key reused: %w",
				ErrConflict,
			)
		}
		value, decodeErr := decodeWorkbenchDispositionRecord(*existing)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if _, err := s.authorizeWorkbenchDispositionOwner(
			ctx,
			principal,
			request.Suggestion.InvestigationID,
			request.ExpectedRevisionID,
		); err != nil {
			return nil, err
		}
		return &value, nil
	} else if !errors.Is(getErr, ErrNotFound) {
		return nil, getErr
	}

	sequence := 1
	if request.Supersedes != "" {
		previous, getErr := s.getWorkbenchDispositionRecord(
			ctx,
			request.Supersedes,
		)
		if getErr != nil {
			return nil, fmt.Errorf(
				"superseded disposition is unavailable: %w",
				ErrConflict,
			)
		}
		previousValue, decodeErr :=
			decodeWorkbenchDispositionRecord(*previous)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if previousValue.InvestigationID !=
			request.Suggestion.InvestigationID ||
			previousValue.RevisionID != request.ExpectedRevisionID ||
			previousValue.Suggestion.ID != request.Suggestion.ID ||
			previousValue.Suggestion.ContentDigest !=
				request.Suggestion.ContentDigest {
			return nil, fmt.Errorf(
				"superseded disposition is out of scope: %w",
				ErrConflict,
			)
		}
		sequence = previousValue.Sequence + 1
	}
	if sequence > maxWorkbenchDispositionSequence {
		return nil, fmt.Errorf(
			"workbench disposition supersession limit exceeded: %w",
			ErrResultLimit,
		)
	}
	suggestionJSON, err := json.Marshal(request.Suggestion)
	if err != nil {
		return nil, err
	}
	now := storeTimestamp(time.Now())
	disposition, err := normalizeWorkbenchDisposition(
		WorkbenchDisposition{
			SchemaVersion:   WorkbenchDispositionSchemaVersion,
			ID:              dispositionID,
			InvestigationID: request.Suggestion.InvestigationID,
			RevisionID:      request.ExpectedRevisionID,
			Suggestion:      request.Suggestion,
			Category:        request.Category,
			Actor:           principal,
			Authority:       "investigation_owner",
			Rationale:       request.Rationale,
			Sequence:        sequence,
			Supersedes:      request.Supersedes,
			CreatedAt:       now,
		},
	)
	if err != nil {
		return nil, err
	}
	supersedesRecordID := disposition.Supersedes
	if supersedesRecordID == "" {
		supersedesRecordID = disposition.ID
	}
	vars := map[string]any{
		"investigation_rid": investigationRecordID(
			disposition.InvestigationID,
		),
		"investigation_id":     disposition.InvestigationID,
		"revision_rid":         revisionRecordID(disposition.RevisionID),
		"expected_revision_id": disposition.RevisionID,
		"principal":            principal,
		"disposition_rid": workbenchDispositionRecordID(
			disposition.ID,
		),
		"schema_version":    disposition.SchemaVersion,
		"disposition_id":    disposition.ID,
		"idempotency_key":   request.IdempotencyKey,
		"request_digest":    requestDigest,
		"suggestion_id":     disposition.Suggestion.ID,
		"suggestion_digest": disposition.Suggestion.ContentDigest,
		"suggestion_json":   string(suggestionJSON),
		"category":          disposition.Category,
		"rationale":         disposition.Rationale,
		"sequence":          disposition.Sequence,
		"supersedes":        disposition.Supersedes,
		"supersedes_rid": workbenchDispositionRecordID(
			supersedesRecordID,
		),
		"prior_sequence": disposition.Sequence - 1,
		"content_digest": disposition.ContentDigest,
	}
	if err := addInvestigationAuditVars(
		vars,
		"investigation.workbench.disposition.append",
		disposition.Suggestion.ID,
		principal,
		now,
	); err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]workbenchDispositionRecord](
			ctx,
			s.db,
			appendWorkbenchDispositionSQL,
			vars,
		)
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) &&
				ctx.Err() == nil &&
				attempt+1 < maxQueueRetries {
				continue
			}
			recoveryContext, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				workbenchReceiptRecovery,
			)
			existing, getErr := s.getWorkbenchDispositionRecord(
				recoveryContext,
				disposition.ID,
			)
			cancel()
			if getErr == nil {
				if existing.RequestDigest != requestDigest {
					return nil, fmt.Errorf(
						"workbench disposition idempotency key reused: %w",
						ErrConflict,
					)
				}
				value, decodeErr :=
					decodeWorkbenchDispositionRecord(*existing)
				return &value, decodeErr
			}
			return nil, fmt.Errorf(
				"append workbench disposition: %w",
				queryErr,
			)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			if _, err := s.authorizeWorkbenchDispositionOwner(
				ctx,
				principal,
				disposition.InvestigationID,
				disposition.RevisionID,
			); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf(
				"workbench disposition supersession changed: %w",
				ErrConflict,
			)
		}
		if rows[0].RequestDigest != requestDigest {
			return nil, fmt.Errorf(
				"workbench disposition idempotency key reused: %w",
				ErrConflict,
			)
		}
		value, err := decodeWorkbenchDispositionRecord(rows[0])
		if err != nil {
			return nil, err
		}
		return &value, nil
	}
}

// ListWorkbenchDispositionsAs returns the immutable history visible through
// the Investigation's current authorization projection. Reader grants may
// inspect it but cannot call the owner-only append boundary.
func (s *Surreal) ListWorkbenchDispositionsAs(
	ctx context.Context,
	principal, investigationID string,
) ([]WorkbenchDisposition, error) {
	projection, err := s.readInvestigationProjection(
		ctx,
		principal,
		investigationID,
	)
	if err != nil {
		return nil, err
	}
	results, queryErr := surrealdb.Query[[]workbenchDispositionRecord](
		ctx,
		s.db,
		`SELECT schema_version, disposition_id, principal, idempotency_key,
			request_digest, investigation_id, revision_id, suggestion_id,
			suggestion_digest, suggestion_json, category, actor, authority,
			rationale, sequence, supersedes, created_at, content_digest
			FROM investigation_workbench_disposition
			WHERE investigation_id = $investigation_id
			ORDER BY created_at ASC, disposition_id ASC LIMIT $limit`,
		map[string]any{
			"investigation_id": investigationID,
			"limit":            maxWorkbenchDispositionRecords + 1,
		},
	)
	if err := s.confirmInvestigationProjection(
		ctx,
		principal,
		projection,
	); err != nil {
		return nil, err
	}
	if queryErr != nil {
		return nil, fmt.Errorf(
			"list workbench dispositions: %w",
			queryErr,
		)
	}
	rows := firstDomainRows(results)
	if len(rows) > maxWorkbenchDispositionRecords {
		return nil, fmt.Errorf(
			"workbench disposition history exceeded %d records: %w",
			maxWorkbenchDispositionRecords,
			ErrResultLimit,
		)
	}
	values := make([]WorkbenchDisposition, 0, len(rows))
	for _, row := range rows {
		value, err := decodeWorkbenchDispositionRecord(row)
		if err != nil {
			return nil, fmt.Errorf(
				"list workbench dispositions: %w",
				err,
			)
		}
		values = append(values, value)
	}
	sort.Slice(values, func(left, right int) bool {
		if values[left].CreatedAt != values[right].CreatedAt {
			return values[left].CreatedAt.Before(
				values[right].CreatedAt,
			)
		}
		return values[left].ID < values[right].ID
	})
	return values, nil
}
