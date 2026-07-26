package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const (
	ChangeBriefSchemaVersion = "change-brief-v1"

	maxChangeBriefProblemBytes       = 16 << 10
	maxChangeBriefOutcomeBytes       = 16 << 10
	maxChangeBriefListItemBytes      = 4 << 10
	maxChangeBriefListItems          = 32
	maxChangeBriefAggregateTextBytes = 128 << 10
	maxChangeBriefExternalRefBytes   = 2 << 10
	maxChangeBriefSelections         = 3
	maxChangeBriefProposalFiles      = 256
	maxChangeBriefProposalFileBytes  = 4 << 20
	maxChangeBriefProposalBytes      = 32 << 20
)

type ChangeBriefTicketKind string

const (
	ChangeBriefAdd     ChangeBriefTicketKind = "add"
	ChangeBriefModify  ChangeBriefTicketKind = "modify"
	ChangeBriefMigrate ChangeBriefTicketKind = "migrate"
	ChangeBriefRetire  ChangeBriefTicketKind = "retire"
)

type ChangeBriefSelectionRole string

const (
	ChangeBriefCurrent     ChangeBriefSelectionRole = "current"
	ChangeBriefReplacement ChangeBriefSelectionRole = "replacement"
	ChangeBriefAnalogous   ChangeBriefSelectionRole = "analogous"
)

// ChangeBriefContractSelection is one exact Atlas identity. Snapshot and
// authorization commitments are deliberately added by the T21.3 preview;
// this record carries no mutable repository HEAD.
type ChangeBriefContractSelection struct {
	Role               ChangeBriefSelectionRole `json:"role"`
	Protocol           string                   `json:"protocol"`
	Repository         string                   `json:"repository"`
	DeclarationLineage string                   `json:"declaration_lineage"`
	CanonicalOperation string                   `json:"canonical_operation"`
}

// ChangeBriefProposalFile commits to caller-owned IDL bytes without storing
// those bytes as published repository evidence.
type ChangeBriefProposalFile struct {
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
	SizeBytes   int    `json:"size_bytes"`
}

type ChangeBriefProposal struct {
	Protocol        string                    `json:"protocol"`
	Files           []ChangeBriefProposalFile `json:"files"`
	SourceSetDigest string                    `json:"source_set_digest"`
}

type changeBriefProposalSourceSet struct {
	Protocol string                    `json:"protocol"`
	Files    []ChangeBriefProposalFile `json:"files"`
}

type ChangeBriefWhat struct {
	Selections []ChangeBriefContractSelection `json:"selections"`
	Proposal   *ChangeBriefProposal           `json:"proposal,omitempty"`
}

// ChangeBrief is immutable canonical content bound one-to-one to an
// Investigation Revision. CreatedAt is store metadata and does not participate
// in content identity.
type ChangeBrief struct {
	ID                string                `json:"brief_id"`
	SchemaVersion     string                `json:"schema_version"`
	InvestigationID   string                `json:"investigation_id"`
	RevisionID        string                `json:"revision_id"`
	TicketKind        ChangeBriefTicketKind `json:"ticket_kind"`
	Problem           string                `json:"problem"`
	DesiredOutcome    string                `json:"desired_outcome"`
	SuccessCriteria   []string              `json:"success_criteria"`
	NonGoals          []string              `json:"non_goals"`
	Assumptions       []string              `json:"assumptions"`
	OpenQuestions     []string              `json:"open_questions"`
	ExternalReference string                `json:"external_reference,omitempty"`
	What              ChangeBriefWhat       `json:"what"`
	Creator           string                `json:"creator"`
	CreatedAt         time.Time             `json:"created_at"`
	ContentDigest     string                `json:"content_digest"`
}

type AppendChangeBriefRevisionRequest struct {
	Principal          string      `json:"principal"`
	ExpectedRevisionID string      `json:"expected_revision_id,omitempty"`
	Revision           Revision    `json:"revision"`
	Brief              ChangeBrief `json:"brief"`
}

type ChangeBriefRevision struct {
	Revision Revision    `json:"revision"`
	Brief    ChangeBrief `json:"brief"`
}

// InvestigationChangeBriefStore is the T21.2 persistence boundary. There is
// intentionally no PutChangeBrief method: every brief mutation appends a new
// parent Revision through one CAS transaction.
type InvestigationChangeBriefStore interface {
	AppendChangeBriefRevision(context.Context, AppendChangeBriefRevisionRequest) (*ChangeBriefRevision, error)
	GetChangeBriefAs(context.Context, string, string) (*ChangeBrief, error)
	GetChangeBriefForRevisionAs(context.Context, string, string) (*ChangeBrief, error)
}

var _ InvestigationChangeBriefStore = (*Surreal)(nil)

type changeBriefContent struct {
	SchemaVersion     string                `json:"schema_version"`
	InvestigationID   string                `json:"investigation_id"`
	RevisionID        string                `json:"revision_id"`
	TicketKind        ChangeBriefTicketKind `json:"ticket_kind"`
	Problem           string                `json:"problem"`
	DesiredOutcome    string                `json:"desired_outcome"`
	SuccessCriteria   []string              `json:"success_criteria"`
	NonGoals          []string              `json:"non_goals"`
	Assumptions       []string              `json:"assumptions"`
	OpenQuestions     []string              `json:"open_questions"`
	ExternalReference string                `json:"external_reference,omitempty"`
	What              ChangeBriefWhat       `json:"what"`
	Creator           string                `json:"creator"`
}

type changeBriefRecord struct {
	ID              string    `json:"brief_id"`
	InvestigationID string    `json:"investigation_id"`
	RevisionID      string    `json:"revision_id"`
	SchemaVersion   string    `json:"schema_version"`
	Content         string    `json:"content"`
	ContentDigest   string    `json:"content_digest"`
	CreatedAt       time.Time `json:"created_at"`
}

func changeBriefRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_change_brief", id)
}

func validChangeBriefTicketKind(kind ChangeBriefTicketKind) bool {
	return kind == ChangeBriefAdd || kind == ChangeBriefModify ||
		kind == ChangeBriefMigrate || kind == ChangeBriefRetire
}

func validChangeBriefSelectionRole(role ChangeBriefSelectionRole) bool {
	return role == ChangeBriefCurrent || role == ChangeBriefReplacement ||
		role == ChangeBriefAnalogous
}

func normalizeChangeBriefText(name, value string, required bool, limit int) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s is not valid UTF-8", name)
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	if len(value) > limit {
		return "", fmt.Errorf("%s exceeds %d bytes", name, limit)
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return "", fmt.Errorf("%s contains an unsafe control character", name)
		}
	}
	return value, nil
}

func normalizeChangeBriefTextList(name string, values []string, required bool) ([]string, error) {
	if len(values) > maxChangeBriefListItems {
		return nil, fmt.Errorf("%s exceeds %d entries", name, maxChangeBriefListItems)
	}
	result := make([]string, len(values))
	seen := make(map[string]bool, len(values))
	for index, value := range values {
		normalized, err := normalizeChangeBriefText(
			fmt.Sprintf("%s[%d]", name, index),
			value,
			true,
			maxChangeBriefListItemBytes,
		)
		if err != nil {
			return nil, err
		}
		if seen[normalized] {
			return nil, fmt.Errorf("%s contains duplicate entry %q", name, normalized)
		}
		seen[normalized] = true
		result[index] = normalized
	}
	if required && len(result) == 0 {
		return nil, fmt.Errorf("%s requires at least one entry", name)
	}
	return result, nil
}

func normalizeChangeBriefProtocol(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "protobuf" && value != "thrift" {
		return "", fmt.Errorf("unsupported contract protocol %q", value)
	}
	return value, nil
}

func normalizeChangeBriefSelection(
	selection ChangeBriefContractSelection,
) (ChangeBriefContractSelection, error) {
	if !validChangeBriefSelectionRole(selection.Role) {
		return ChangeBriefContractSelection{}, fmt.Errorf("invalid contract selection role %q", selection.Role)
	}
	var err error
	selection.Protocol, err = normalizeChangeBriefProtocol(selection.Protocol)
	if err != nil {
		return ChangeBriefContractSelection{}, err
	}
	for name, target := range map[string]*string{
		"contract repository":          &selection.Repository,
		"contract declaration lineage": &selection.DeclarationLineage,
		"canonical operation":          &selection.CanonicalOperation,
	} {
		*target, err = normalizeChangeBriefText(name, *target, true, 4<<10)
		if err != nil {
			return ChangeBriefContractSelection{}, err
		}
	}
	if !strings.HasPrefix(selection.CanonicalOperation, "/") {
		return ChangeBriefContractSelection{}, errors.New("canonical operation must begin with /")
	}
	return selection, nil
}

func normalizeChangeBriefProposal(
	proposal *ChangeBriefProposal,
) (*ChangeBriefProposal, error) {
	if proposal == nil {
		return nil, nil
	}
	normalized := *proposal
	var err error
	normalized.Protocol, err = normalizeChangeBriefProtocol(normalized.Protocol)
	if err != nil {
		return nil, err
	}
	if len(normalized.Files) == 0 || len(normalized.Files) > maxChangeBriefProposalFiles {
		return nil, fmt.Errorf("proposal files must contain 1..%d entries", maxChangeBriefProposalFiles)
	}
	normalized.Files = slices.Clone(normalized.Files)
	totalBytes := 0
	for index := range normalized.Files {
		file := &normalized.Files[index]
		file.Path = strings.TrimSpace(file.Path)
		if len(file.Path) == 0 || len(file.Path) > 4096 || !utf8.ValidString(file.Path) ||
			file.Path != path.Clean(file.Path) || strings.HasPrefix(file.Path, "/") ||
			file.Path == "." || file.Path == ".." || strings.HasPrefix(file.Path, "../") ||
			strings.Contains(file.Path, "\\") {
			return nil, fmt.Errorf("proposal file %d has an invalid relative path", index)
		}
		if !validSHA256Digest(file.ContentHash) {
			return nil, fmt.Errorf("proposal file %q has an invalid content hash", file.Path)
		}
		if file.SizeBytes < 1 || file.SizeBytes > maxChangeBriefProposalFileBytes {
			return nil, fmt.Errorf("proposal file %q size is outside 1..%d", file.Path, maxChangeBriefProposalFileBytes)
		}
		totalBytes += file.SizeBytes
		if totalBytes > maxChangeBriefProposalBytes {
			return nil, fmt.Errorf("proposal source set exceeds %d bytes", maxChangeBriefProposalBytes)
		}
	}
	slices.SortFunc(normalized.Files, func(left, right ChangeBriefProposalFile) int {
		return strings.Compare(left.Path, right.Path)
	})
	for index := 1; index < len(normalized.Files); index++ {
		if normalized.Files[index].Path == normalized.Files[index-1].Path {
			return nil, fmt.Errorf("proposal repeats path %q", normalized.Files[index].Path)
		}
	}
	digest, err := contentDigest(changeBriefProposalSourceSet{
		Protocol: normalized.Protocol,
		Files:    normalized.Files,
	})
	if err != nil {
		return nil, err
	}
	if normalized.SourceSetDigest != "" && normalized.SourceSetDigest != digest {
		return nil, errors.New("proposal source-set digest does not match canonical files")
	}
	normalized.SourceSetDigest = digest
	return &normalized, nil
}

func normalizeChangeBriefWhat(value ChangeBriefWhat) (ChangeBriefWhat, error) {
	if len(value.Selections) > maxChangeBriefSelections {
		return ChangeBriefWhat{}, fmt.Errorf("contract selections exceed %d entries", maxChangeBriefSelections)
	}
	value.Selections = slices.Clone(value.Selections)
	seenRoles := make(map[ChangeBriefSelectionRole]bool, len(value.Selections))
	for index := range value.Selections {
		normalized, err := normalizeChangeBriefSelection(value.Selections[index])
		if err != nil {
			return ChangeBriefWhat{}, fmt.Errorf("selection %d: %w", index, err)
		}
		if seenRoles[normalized.Role] {
			return ChangeBriefWhat{}, fmt.Errorf("contract selection repeats role %q", normalized.Role)
		}
		seenRoles[normalized.Role] = true
		value.Selections[index] = normalized
	}
	slices.SortFunc(value.Selections, func(left, right ChangeBriefContractSelection) int {
		return strings.Compare(string(left.Role), string(right.Role))
	})
	proposal, err := normalizeChangeBriefProposal(value.Proposal)
	if err != nil {
		return ChangeBriefWhat{}, err
	}
	value.Proposal = proposal
	if len(value.Selections) == 0 && value.Proposal == nil {
		return ChangeBriefWhat{}, errors.New("what requires a contract selection or proposal commitment")
	}
	return value, nil
}

func changeBriefContentFromBrief(brief ChangeBrief) changeBriefContent {
	return changeBriefContent{
		SchemaVersion: brief.SchemaVersion, InvestigationID: brief.InvestigationID,
		RevisionID: brief.RevisionID, TicketKind: brief.TicketKind,
		Problem: brief.Problem, DesiredOutcome: brief.DesiredOutcome,
		SuccessCriteria: brief.SuccessCriteria, NonGoals: brief.NonGoals,
		Assumptions: brief.Assumptions, OpenQuestions: brief.OpenQuestions,
		ExternalReference: brief.ExternalReference, What: brief.What, Creator: brief.Creator,
	}
}

func normalizeChangeBrief(brief ChangeBrief) (ChangeBrief, []byte, error) {
	if brief.SchemaVersion == "" {
		brief.SchemaVersion = ChangeBriefSchemaVersion
	}
	if brief.SchemaVersion != ChangeBriefSchemaVersion {
		return ChangeBrief{}, nil, fmt.Errorf("unsupported change brief schema %q", brief.SchemaVersion)
	}
	if !validULID(brief.InvestigationID) || !strings.HasPrefix(brief.RevisionID, "ivr_") {
		return ChangeBrief{}, nil, errors.New("change brief requires an Investigation and Revision identity")
	}
	if !validChangeBriefTicketKind(brief.TicketKind) {
		return ChangeBrief{}, nil, fmt.Errorf("invalid change brief ticket kind %q", brief.TicketKind)
	}
	var err error
	brief.Problem, err = normalizeChangeBriefText(
		"change brief problem",
		brief.Problem,
		true,
		maxChangeBriefProblemBytes,
	)
	if err != nil {
		return ChangeBrief{}, nil, err
	}
	brief.DesiredOutcome, err = normalizeChangeBriefText(
		"change brief desired outcome",
		brief.DesiredOutcome,
		true,
		maxChangeBriefOutcomeBytes,
	)
	if err != nil {
		return ChangeBrief{}, nil, err
	}
	for name, target := range map[string]*[]string{
		"success criteria": &brief.SuccessCriteria,
		"non-goals":        &brief.NonGoals,
		"assumptions":      &brief.Assumptions,
		"open questions":   &brief.OpenQuestions,
	} {
		*target, err = normalizeChangeBriefTextList(name, *target, name == "success criteria")
		if err != nil {
			return ChangeBrief{}, nil, err
		}
	}
	brief.ExternalReference, err = normalizeChangeBriefText(
		"external reference",
		brief.ExternalReference,
		false,
		maxChangeBriefExternalRefBytes,
	)
	if err != nil {
		return ChangeBrief{}, nil, err
	}
	brief.What, err = normalizeChangeBriefWhat(brief.What)
	if err != nil {
		return ChangeBrief{}, nil, err
	}
	if !validInvestigationPrincipal(brief.Creator) {
		return ChangeBrief{}, nil, errors.New("change brief creator is invalid")
	}
	totalText := len(brief.Problem) + len(brief.DesiredOutcome) + len(brief.ExternalReference)
	for _, values := range [][]string{
		brief.SuccessCriteria, brief.NonGoals, brief.Assumptions, brief.OpenQuestions,
	} {
		for _, value := range values {
			totalText += len(value)
		}
	}
	if totalText > maxChangeBriefAggregateTextBytes {
		return ChangeBrief{}, nil, fmt.Errorf(
			"change brief text exceeds %d aggregate bytes",
			maxChangeBriefAggregateTextBytes,
		)
	}
	content, err := json.Marshal(changeBriefContentFromBrief(brief))
	if err != nil {
		return ChangeBrief{}, nil, fmt.Errorf("encode canonical change brief: %w", err)
	}
	hash := sha256.Sum256(content)
	id := "icb_" + hex.EncodeToString(hash[:])
	digest := "sha256:" + hex.EncodeToString(hash[:])
	if brief.ID != "" && brief.ID != id {
		return ChangeBrief{}, nil, errors.New("change brief id does not match canonical content")
	}
	if brief.ContentDigest != "" && brief.ContentDigest != digest {
		return ChangeBrief{}, nil, errors.New("change brief digest does not match canonical content")
	}
	brief.ID = id
	brief.ContentDigest = digest
	return brief, content, nil
}

// CanonicalChangeBrief returns the normalized structured value and exact
// canonical bytes used for its content id.
func CanonicalChangeBrief(brief ChangeBrief) (ChangeBrief, []byte, error) {
	brief.CreatedAt = time.Time{}
	return normalizeChangeBrief(brief)
}

func changeBriefFromRecord(record changeBriefRecord) (*ChangeBrief, error) {
	if len(record.Content) == 0 || len(record.Content) > maxChangeBriefAggregateTextBytes+maxChangeBriefProposalBytes ||
		!utf8.ValidString(record.Content) {
		return nil, errors.New("get change brief: stored content is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(record.Content))
	decoder.DisallowUnknownFields()
	var content changeBriefContent
	if err := decoder.Decode(&content); err != nil {
		return nil, fmt.Errorf("get change brief: decode stored content: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("get change brief: stored content has a trailing value")
	}
	brief := ChangeBrief{
		ID: record.ID, SchemaVersion: content.SchemaVersion,
		InvestigationID: content.InvestigationID, RevisionID: content.RevisionID,
		TicketKind: content.TicketKind, Problem: content.Problem,
		DesiredOutcome: content.DesiredOutcome, SuccessCriteria: content.SuccessCriteria,
		NonGoals: content.NonGoals, Assumptions: content.Assumptions,
		OpenQuestions: content.OpenQuestions, ExternalReference: content.ExternalReference,
		What: content.What, Creator: content.Creator, CreatedAt: record.CreatedAt,
		ContentDigest: record.ContentDigest,
	}
	normalized, canonical, err := normalizeChangeBrief(brief)
	if err != nil {
		return nil, fmt.Errorf("get change brief: stored immutable content is inconsistent: %w", err)
	}
	if !bytes.Equal(canonical, []byte(record.Content)) ||
		normalized.ID != record.ID ||
		normalized.ContentDigest != record.ContentDigest ||
		normalized.SchemaVersion != record.SchemaVersion ||
		normalized.InvestigationID != record.InvestigationID ||
		normalized.RevisionID != record.RevisionID ||
		record.CreatedAt.IsZero() {
		return nil, errors.New("get change brief: stored immutable content is inconsistent")
	}
	return &normalized, nil
}

func (s *Surreal) GetChangeBrief(ctx context.Context, id string) (*ChangeBrief, error) {
	if !strings.HasPrefix(id, "icb_") {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]changeBriefRecord](ctx, s.db,
		`SELECT brief_id, investigation_id, revision_id, schema_version,
			content, content_digest, created_at FROM $rid LIMIT 1`,
		map[string]any{"rid": changeBriefRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get change brief: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	return changeBriefFromRecord(rows[0])
}

func (s *Surreal) changeBriefIDForRevision(ctx context.Context, revisionID string) (string, error) {
	if !strings.HasPrefix(revisionID, "ivr_") {
		return "", ErrNotFound
	}
	type briefID struct {
		ID string `json:"brief_id"`
	}
	results, err := surrealdb.Query[[]briefID](ctx, s.db,
		"SELECT brief_id FROM investigation_change_brief WHERE revision_id = $revision_id LIMIT 1",
		map[string]any{"revision_id": revisionID})
	if err != nil {
		return "", fmt.Errorf("resolve revision change brief: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || !strings.HasPrefix(rows[0].ID, "icb_") {
		return "", ErrNotFound
	}
	return rows[0].ID, nil
}

func (s *Surreal) investigationIDForChangeBrief(ctx context.Context, id string) (string, error) {
	if !strings.HasPrefix(id, "icb_") {
		return "", ErrNotFound
	}
	revisionID, err := s.lookupRecordString(
		ctx,
		"SELECT revision_id AS value FROM $rid LIMIT 1",
		changeBriefRecordID(id),
	)
	if err != nil {
		return "", err
	}
	return s.investigationIDForRevision(ctx, revisionID)
}

func (s *Surreal) GetChangeBriefAs(
	ctx context.Context,
	principal, id string,
) (*ChangeBrief, error) {
	investigationID, resolutionErr := s.investigationIDForChangeBrief(ctx, id)
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	value, readErr := s.GetChangeBrief(ctx, id)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return value, readErr
}

func (s *Surreal) GetChangeBriefForRevisionAs(
	ctx context.Context,
	principal, revisionID string,
) (*ChangeBrief, error) {
	investigationID, resolutionErr := s.investigationIDForRevision(ctx, revisionID)
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	id, readErr := s.changeBriefIDForRevision(ctx, revisionID)
	if readErr == nil {
		var value *ChangeBrief
		value, readErr = s.GetChangeBrief(ctx, id)
		if readErr == nil && value.RevisionID != revisionID {
			readErr = errors.New("get revision change brief: stored binding is inconsistent")
		}
		if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
			return nil, err
		}
		return value, readErr
	}
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return nil, readErr
}

const appendChangeBriefRevisionSQL = `
BEGIN;
LET $locked = UPDATE $investigation_rid SET current_revision_id = $revision_id,
	updated_at = $now
	WHERE investigation_id = $investigation_id AND owner = $principal
	  AND lifecycle != 'archived'
	  AND (current_revision_id ?? '') = $expected_revision_id
	  AND (($seq = 1 AND $expected_revision_id = '')
	    OR ($seq > 1 AND array::len(SELECT VALUE revision_id FROM $expected_revision_rid
			WHERE revision_id = $expected_revision_id
			  AND investigation_id = $investigation_id AND seq = $previous_seq LIMIT 1) = 1))
	  AND array::len(SELECT VALUE revision_id FROM $revision_rid LIMIT 1) = 0
	  AND array::len(SELECT VALUE brief_id FROM $brief_rid LIMIT 1) = 0
	RETURN AFTER;
LET $saved_revision = IF array::len($locked) = 1 THEN
	(CREATE $revision_rid SET revision_id = $revision_id,
		investigation_id = $investigation_id, seq = $seq,
		normalized_question = $normalized_question, decision_sought = $decision_sought,
		declared_universe = $declared_universe, snapshot_policy = $snapshot_policy,
		build_configuration = $build_configuration, pack_selection = $pack_selection,
		enumeration_method = $enumeration_method, creator = $principal,
		created_at = $now, content_digest = $revision_digest RETURN AFTER)
	ELSE [] END;
LET $saved = IF array::len($saved_revision) = 1 THEN
	(CREATE $brief_rid SET brief_id = $brief_id,
		investigation_id = $investigation_id, revision_id = $revision_id,
		schema_version = $brief_schema_version, content = $brief_content,
		content_digest = $brief_digest, created_at = $now RETURN AFTER)
	ELSE [] END;
IF array::len($saved) = 1 {
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 200, created_at = $now RETURN NONE
};
RETURN $saved;
COMMIT;`

func (s *Surreal) AppendChangeBriefRevision(
	ctx context.Context,
	request AppendChangeBriefRevisionRequest,
) (*ChangeBriefRevision, error) {
	if !validInvestigationPrincipal(request.Principal) {
		return nil, ErrNotFound
	}
	if request.Revision.Creator == "" {
		request.Revision.Creator = request.Principal
	}
	if request.Revision.Creator != request.Principal {
		return nil, errors.New("append change brief revision: creator must be the principal")
	}
	if err := validateRevision(&request.Revision); err != nil {
		return nil, fmt.Errorf("append change brief revision: revision: %w", err)
	}
	wantExpected := ""
	if request.Revision.Seq > 1 {
		wantExpected = revisionIdentity(request.Revision.InvestigationID, request.Revision.Seq-1)
	}
	if request.ExpectedRevisionID != wantExpected {
		return nil, fmt.Errorf("append change brief revision: expected revision does not precede the new sequence: %w", ErrConflict)
	}
	if request.Brief.InvestigationID == "" {
		request.Brief.InvestigationID = request.Revision.InvestigationID
	}
	if request.Brief.RevisionID == "" {
		request.Brief.RevisionID = request.Revision.ID
	}
	if request.Brief.Creator == "" {
		request.Brief.Creator = request.Principal
	}
	if request.Brief.InvestigationID != request.Revision.InvestigationID ||
		request.Brief.RevisionID != request.Revision.ID ||
		request.Brief.Creator != request.Principal {
		return nil, errors.New("append change brief revision: brief binding does not match the new revision")
	}
	var content []byte
	var err error
	request.Brief, content, err = normalizeChangeBrief(request.Brief)
	if err != nil {
		return nil, fmt.Errorf("append change brief revision: brief: %w", err)
	}
	parent, err := s.GetInvestigationAs(
		ctx,
		request.Principal,
		request.Revision.InvestigationID,
	)
	if err != nil || parent.Owner != request.Principal {
		return nil, ErrNotFound
	}
	now := storeTimestamp(time.Now())
	expectedRID := revisionRecordID(request.Revision.ID)
	if request.ExpectedRevisionID != "" {
		expectedRID = revisionRecordID(request.ExpectedRevisionID)
	}
	vars := map[string]any{
		"investigation_rid":     investigationRecordID(request.Revision.InvestigationID),
		"investigation_id":      request.Revision.InvestigationID,
		"principal":             request.Principal,
		"expected_revision_id":  request.ExpectedRevisionID,
		"expected_revision_rid": expectedRID,
		"previous_seq":          request.Revision.Seq - 1,
		"revision_rid":          revisionRecordID(request.Revision.ID),
		"revision_id":           request.Revision.ID,
		"seq":                   request.Revision.Seq,
		"normalized_question":   request.Revision.NormalizedQuestion,
		"decision_sought":       request.Revision.DecisionSought,
		"declared_universe":     request.Revision.DeclaredUniverse,
		"snapshot_policy":       request.Revision.SnapshotPolicy,
		"build_configuration":   request.Revision.BuildConfiguration,
		"pack_selection":        request.Revision.PackSelection,
		"enumeration_method":    request.Revision.EnumerationMethod,
		"revision_digest":       request.Revision.ContentDigest,
		"brief_rid":             changeBriefRecordID(request.Brief.ID),
		"brief_id":              request.Brief.ID,
		"brief_schema_version":  request.Brief.SchemaVersion,
		"brief_content":         string(content),
		"brief_digest":          request.Brief.ContentDigest,
		"now":                   now,
	}
	if err := addInvestigationAuditVars(
		vars,
		"investigation.change_brief.append",
		request.Brief.ID,
		request.Principal,
		now,
	); err != nil {
		return nil, fmt.Errorf("append change brief revision: audit identity: %w", err)
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]changeBriefRecord](
			ctx,
			s.db,
			appendChangeBriefRevisionSQL,
			vars,
		)
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("append change brief revision: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			current, readErr := s.GetInvestigationAs(
				ctx,
				request.Principal,
				request.Revision.InvestigationID,
			)
			if readErr != nil || current.Owner != request.Principal {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("append change brief revision: stale or unavailable parent revision: %w", ErrConflict)
		}
		brief, readErr := changeBriefFromRecord(rows[0])
		if readErr != nil {
			return nil, readErr
		}
		revision, readErr := s.GetRevision(ctx, request.Revision.ID)
		if readErr != nil || revision.InvestigationID != brief.InvestigationID {
			return nil, errors.New("append change brief revision: stored object graph is inconsistent")
		}
		return &ChangeBriefRevision{Revision: *revision, Brief: *brief}, nil
	}
}
