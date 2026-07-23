package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const reviewCursorVersion = "investigation-review-cursor-v1"

type ReviewQueue string

const (
	ReviewQueueNewConsumers          ReviewQueue = "new_consumers"
	ReviewQueueCoverageRegression    ReviewQueue = "coverage_regression"
	ReviewQueueUnresolvedAttribution ReviewQueue = "unresolved_attribution"
)

// ReviewProjectionRule is the versioned pack rule that converts a published
// snapshot into ReviewItems. ExpiresAfter is applied to the immutable
// snapshot publication time, not the wall clock at projection time.
type ReviewProjectionRule struct {
	ID           string        `json:"id"`
	Version      string        `json:"version"`
	ExpiresAfter time.Duration `json:"expires_after"`
}

// ReviewAttributionCondition is a typed unresolved hop reported by the
// analysis that published the source fact. The store verifies that FactID is
// present in the authorized source snapshot before it can become a ReviewItem.
type ReviewAttributionCondition struct {
	FactID      string `json:"fact_id"`
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	Hop         string `json:"hop"`
	ReasonCode  string `json:"reason_code"`
}

// ReviewProjectionRequest contains only rule inputs that cannot be recovered
// from the immutable source snapshot. It has no ReviewItem field and therefore
// cannot be used as a hand-creation API.
type ReviewProjectionRequest struct {
	Principal              string                       `json:"principal"`
	SourceSnapshotID       string                       `json:"source_snapshot_id"`
	Rule                   ReviewProjectionRule         `json:"rule"`
	HumanRecordStateDigest string                       `json:"human_record_state_digest"`
	UnresolvedAttribution  []ReviewAttributionCondition `json:"unresolved_attribution"`
}

// ReviewItem is a deterministic projection, never an authored record.
// SupersededBy/SupersededAt are projection lifecycle fields and do not
// participate in the immutable identity digest.
type ReviewItem struct {
	ID                     string      `json:"review_item_id"`
	ProjectionKey          string      `json:"projection_key"`
	Principal              string      `json:"principal"`
	InvestigationID        string      `json:"investigation_id"`
	SourceSnapshotID       string      `json:"source_snapshot_id"`
	ComparisonID           string      `json:"comparison_id"`
	ProjectionID           string      `json:"projection_id"`
	ProjectionVersion      string      `json:"projection_version"`
	Queue                  ReviewQueue `json:"queue"`
	SubjectKind            string      `json:"subject_kind"`
	SubjectID              string      `json:"subject_id"`
	Delta                  string      `json:"delta"`
	Cause                  string      `json:"cause"`
	EvidenceReference      string      `json:"evidence_reference,omitempty"`
	HumanRecordStateDigest string      `json:"human_record_state_digest"`
	EvaluatedAt            time.Time   `json:"evaluated_at"`
	ExpiresAt              time.Time   `json:"expires_at"`
	SupersededBy           string      `json:"superseded_by,omitempty"`
	SupersededAt           *time.Time  `json:"superseded_at,omitempty"`
	ContentDigest          string      `json:"content_digest"`
}

type ReviewItemView struct {
	ReviewItem
	Lifecycle    string `json:"lifecycle"`
	Acknowledged bool   `json:"acknowledged"`
}

// ReviewCursor is the canonical payload stored inside the authorization-
// epoch-bound InvestigationCursor. Acknowledgement never mutates ReviewItems.
type ReviewCursor struct {
	Version                string   `json:"version"`
	AcknowledgedItemIDs    []string `json:"acknowledged_item_ids"`
	LastViewedComparisonID string   `json:"last_viewed_comparison_id"`
}

type InvestigationReviewStore interface {
	MaterializeReviewProjection(context.Context, ReviewProjectionRequest) ([]ReviewItem, error)
	ListReviewItemsAs(context.Context, string, string, time.Time) ([]ReviewItemView, error)
	PutReviewCursor(context.Context, string, string, ReviewCursor) (*ReviewCursor, error)
	GetReviewCursor(context.Context, string, string) (*ReviewCursor, error)
}

var _ InvestigationReviewStore = (*Surreal)(nil)

type reviewProjectionInput struct {
	Principal              string
	InvestigationID        string
	SourceSnapshotID       string
	Comparison             ConsumerComparison
	Coverage               InvestigationCoverageLedger
	Facts                  []ConsumerEdgeFact
	Rule                   ReviewProjectionRule
	HumanRecordStateDigest string
	UnresolvedAttribution  []ReviewAttributionCondition
	EvaluatedAt            time.Time
}

type reviewCandidate struct {
	Queue             ReviewQueue
	SubjectKind       string
	SubjectID         string
	Delta             string
	Cause             string
	EvidenceReference string
}

func validReviewQueue(queue ReviewQueue) bool {
	return queue == ReviewQueueNewConsumers ||
		queue == ReviewQueueCoverageRegression ||
		queue == ReviewQueueUnresolvedAttribution
}

func normalizeReviewProjectionRule(rule ReviewProjectionRule) (ReviewProjectionRule, error) {
	if err := validateDomainString("review projection id", rule.ID, true); err != nil {
		return ReviewProjectionRule{}, err
	}
	if err := validateDomainString("review projection version", rule.Version, true); err != nil {
		return ReviewProjectionRule{}, err
	}
	if rule.ExpiresAfter <= 0 || rule.ExpiresAfter > 365*24*time.Hour {
		return ReviewProjectionRule{}, errors.New("review expiry must be within (0, 365d]")
	}
	return rule, nil
}

func normalizeReviewAttribution(
	values []ReviewAttributionCondition,
	facts map[string]ConsumerEdgeFact,
) ([]ReviewAttributionCondition, error) {
	if len(values) > 5000 {
		return nil, errors.New("unresolved attribution exceeds 5000 rows")
	}
	values = slices.Clone(values)
	for _, value := range values {
		for name, field := range map[string]string{
			"attribution fact":         value.FactID,
			"attribution subject kind": value.SubjectKind,
			"attribution subject":      value.SubjectID,
			"attribution hop":          value.Hop,
			"attribution reason":       value.ReasonCode,
		} {
			if err := validateDomainString(name, field, true); err != nil {
				return nil, err
			}
		}
		if value.Hop != "service" && value.Hop != "owner" &&
			value.Hop != "build_target" && value.Hop != "deployable" {
			return nil, fmt.Errorf("unsupported attribution hop %q", value.Hop)
		}
		if _, ok := facts[value.FactID]; !ok {
			return nil, fmt.Errorf(
				"unresolved attribution fact %q is absent from the source snapshot",
				value.FactID,
			)
		}
	}
	slices.SortFunc(values, func(left, right ReviewAttributionCondition) int {
		return strings.Compare(
			left.FactID+"\x00"+left.Hop+"\x00"+left.SubjectKind+"\x00"+left.SubjectID+"\x00"+left.ReasonCode,
			right.FactID+"\x00"+right.Hop+"\x00"+right.SubjectKind+"\x00"+right.SubjectID+"\x00"+right.ReasonCode,
		)
	})
	for i := 1; i < len(values); i++ {
		if values[i] == values[i-1] {
			return nil, errors.New("duplicate unresolved attribution condition")
		}
	}
	return values, nil
}

func reviewItemCore(item ReviewItem) any {
	return struct {
		ID, ProjectionKey, Principal, InvestigationID, SourceSnapshotID string
		ComparisonID, ProjectionID, ProjectionVersion                   string
		Queue                                                           ReviewQueue
		SubjectKind, SubjectID, Delta, Cause, EvidenceReference         string
		HumanRecordStateDigest                                          string
		EvaluatedAt, ExpiresAt                                          time.Time
	}{
		item.ID,
		item.ProjectionKey,
		item.Principal,
		item.InvestigationID,
		item.SourceSnapshotID,
		item.ComparisonID,
		item.ProjectionID,
		item.ProjectionVersion,
		item.Queue,
		item.SubjectKind,
		item.SubjectID,
		item.Delta,
		item.Cause,
		item.EvidenceReference,
		item.HumanRecordStateDigest,
		item.EvaluatedAt,
		item.ExpiresAt,
	}
}

func normalizeReviewItem(item ReviewItem) (ReviewItem, error) {
	if !strings.HasPrefix(item.ID, "iri_") ||
		!strings.HasPrefix(item.ProjectionKey, "irk_") ||
		!validInvestigationPrincipal(item.Principal) ||
		!validULID(item.InvestigationID) ||
		!strings.HasPrefix(item.SourceSnapshotID, "ics_") ||
		!validReviewQueue(item.Queue) {
		return ReviewItem{}, errors.New("review item identity is inconsistent")
	}
	for name, value := range map[string]string{
		"comparison id":             item.ComparisonID,
		"review projection id":      item.ProjectionID,
		"review projection version": item.ProjectionVersion,
		"review subject kind":       item.SubjectKind,
		"review subject":            item.SubjectID,
		"review delta":              item.Delta,
		"review cause":              item.Cause,
		"human record state digest": item.HumanRecordStateDigest,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return ReviewItem{}, err
		}
	}
	if err := validateDomainString(
		"review evidence reference",
		item.EvidenceReference,
		false,
	); err != nil {
		return ReviewItem{}, err
	}
	switch item.Queue {
	case ReviewQueueNewConsumers:
		if (item.Delta != "added" && item.Delta != "reintroduced") ||
			item.Cause != "relationship_added_traced" ||
			item.EvidenceReference == "" {
			return ReviewItem{}, errors.New("new-consumer review item is not positively traced")
		}
	case ReviewQueueCoverageRegression:
		if item.Delta != "regressed" {
			return ReviewItem{}, errors.New("coverage review item is not a regression")
		}
	case ReviewQueueUnresolvedAttribution:
		if item.Delta != "unresolved" || item.EvidenceReference == "" ||
			!strings.Contains(item.Cause, ":") {
			return ReviewItem{}, errors.New("attribution review item is not unresolved")
		}
	}
	wantProjectionKey := hashIdentity(
		"irk_",
		item.Principal,
		item.InvestigationID,
		item.ProjectionID,
		item.ProjectionVersion,
		string(item.Queue),
		item.SubjectKind,
		item.SubjectID,
	)
	wantID := hashIdentity(
		"iri_",
		item.Principal,
		item.InvestigationID,
		item.SourceSnapshotID,
		item.ComparisonID,
		item.ProjectionID,
		item.ProjectionVersion,
		string(item.Queue),
		item.SubjectKind,
		item.SubjectID,
		item.Delta,
		item.Cause,
		item.EvidenceReference,
		item.HumanRecordStateDigest,
	)
	if item.ProjectionKey != wantProjectionKey || item.ID != wantID {
		return ReviewItem{}, errors.New("review item derived identity is inconsistent")
	}
	item.EvaluatedAt = storeTimestamp(item.EvaluatedAt)
	item.ExpiresAt = storeTimestamp(item.ExpiresAt)
	if item.EvaluatedAt.IsZero() || !item.ExpiresAt.After(item.EvaluatedAt) {
		return ReviewItem{}, errors.New("review item has invalid lifecycle times")
	}
	if item.SupersededAt != nil {
		value := storeTimestamp(*item.SupersededAt)
		item.SupersededAt = &value
		if item.SupersededBy == "" || value.Before(item.EvaluatedAt) {
			return ReviewItem{}, errors.New("review item supersession is incomplete")
		}
	} else if item.SupersededBy != "" {
		return ReviewItem{}, errors.New("review item supersession is incomplete")
	}
	digest, err := contentDigest(reviewItemCore(item))
	if err != nil {
		return ReviewItem{}, err
	}
	if item.ContentDigest != "" && item.ContentDigest != digest {
		return ReviewItem{}, errors.New("review item digest mismatch")
	}
	item.ContentDigest = digest
	return item, nil
}

// LifecycleAt derives the built-in lifecycle without mutating the ReviewItem.
// When both terminal rules apply, whichever occurred first wins.
func (item ReviewItem) LifecycleAt(at time.Time) string {
	at = storeTimestamp(at)
	expired := !at.Before(item.ExpiresAt)
	superseded := item.SupersededAt != nil && !at.Before(*item.SupersededAt)
	switch {
	case expired && superseded && item.ExpiresAt.Before(*item.SupersededAt):
		return "expired"
	case superseded:
		return "superseded"
	case expired:
		return "expired"
	default:
		return "open"
	}
}

func newReviewItem(input reviewProjectionInput, candidate reviewCandidate) (ReviewItem, error) {
	projectionKey := hashIdentity(
		"irk_",
		input.Principal,
		input.InvestigationID,
		input.Rule.ID,
		input.Rule.Version,
		string(candidate.Queue),
		candidate.SubjectKind,
		candidate.SubjectID,
	)
	id := hashIdentity(
		"iri_",
		input.Principal,
		input.InvestigationID,
		input.SourceSnapshotID,
		input.Comparison.ComparisonID,
		input.Rule.ID,
		input.Rule.Version,
		string(candidate.Queue),
		candidate.SubjectKind,
		candidate.SubjectID,
		candidate.Delta,
		candidate.Cause,
		candidate.EvidenceReference,
		input.HumanRecordStateDigest,
	)
	return normalizeReviewItem(ReviewItem{
		ID: id, ProjectionKey: projectionKey,
		Principal: input.Principal, InvestigationID: input.InvestigationID,
		SourceSnapshotID:  input.SourceSnapshotID,
		ComparisonID:      input.Comparison.ComparisonID,
		ProjectionID:      input.Rule.ID,
		ProjectionVersion: input.Rule.Version,
		Queue:             candidate.Queue, SubjectKind: candidate.SubjectKind,
		SubjectID: candidate.SubjectID, Delta: candidate.Delta,
		Cause: candidate.Cause, EvidenceReference: candidate.EvidenceReference,
		HumanRecordStateDigest: input.HumanRecordStateDigest,
		EvaluatedAt:            input.EvaluatedAt,
		ExpiresAt:              input.EvaluatedAt.Add(input.Rule.ExpiresAfter),
	})
}

func deriveReviewItems(input reviewProjectionInput) ([]ReviewItem, error) {
	if !validInvestigationPrincipal(input.Principal) ||
		!validULID(input.InvestigationID) ||
		!strings.HasPrefix(input.SourceSnapshotID, "ics_") {
		return nil, errors.New("review projection identity is invalid")
	}
	var err error
	input.Rule, err = normalizeReviewProjectionRule(input.Rule)
	if err != nil {
		return nil, err
	}
	if err := validateDomainString(
		"human record state digest",
		input.HumanRecordStateDigest,
		true,
	); err != nil {
		return nil, err
	}
	if err := validateConsumerComparison(input.Comparison); err != nil {
		return nil, err
	}
	input.EvaluatedAt = storeTimestamp(input.EvaluatedAt)
	if input.EvaluatedAt.IsZero() {
		return nil, errors.New("review evaluation time is required")
	}
	facts := make(map[string]ConsumerEdgeFact, len(input.Facts))
	for _, fact := range input.Facts {
		facts[fact.FactID] = fact
	}
	input.UnresolvedAttribution, err = normalizeReviewAttribution(
		input.UnresolvedAttribution,
		facts,
	)
	if err != nil {
		return nil, err
	}

	candidates := make([]reviewCandidate, 0, len(input.Comparison.Deltas)+
		len(input.Comparison.NonComparabilityCauses)+len(input.Coverage.Failures)+
		len(input.UnresolvedAttribution)+1)
	for _, delta := range input.Comparison.Deltas {
		if delta.Change != "added" && delta.Change != "reintroduced" {
			continue
		}
		candidates = append(candidates, reviewCandidate{
			Queue: ReviewQueueNewConsumers, SubjectKind: "consumer_relationship",
			SubjectID: delta.LogicalRelationshipID, Delta: delta.Change,
			Cause: delta.Cause, EvidenceReference: delta.After.FactID,
		})
	}
	for _, cause := range input.Comparison.NonComparabilityCauses {
		if cause == "no_prior_snapshot" {
			continue
		}
		candidates = append(candidates, reviewCandidate{
			Queue: ReviewQueueCoverageRegression, SubjectKind: "comparison",
			SubjectID: input.Comparison.ComparisonID, Delta: "regressed",
			Cause: cause,
		})
	}
	for _, failure := range input.Coverage.Failures {
		candidates = append(candidates, reviewCandidate{
			Queue: ReviewQueueCoverageRegression, SubjectKind: "coverage_unit",
			SubjectID: failure.Unit, Delta: "regressed", Cause: failure.Code,
		})
	}
	if input.Coverage.Partial+input.Coverage.Failed > 0 &&
		len(input.Coverage.Failures) == 0 {
		candidates = append(candidates, reviewCandidate{
			Queue: ReviewQueueCoverageRegression, SubjectKind: "run_artifact",
			SubjectID: input.Comparison.RightArtifactID,
			Delta:     "regressed", Cause: "processing_incomplete",
		})
	}
	for _, unresolved := range input.UnresolvedAttribution {
		candidates = append(candidates, reviewCandidate{
			Queue:       ReviewQueueUnresolvedAttribution,
			SubjectKind: unresolved.SubjectKind, SubjectID: unresolved.SubjectID,
			Delta: "unresolved", Cause: unresolved.Hop + ":" + unresolved.ReasonCode,
			EvidenceReference: unresolved.FactID,
		})
	}
	slices.SortFunc(candidates, func(left, right reviewCandidate) int {
		return strings.Compare(
			string(left.Queue)+"\x00"+left.SubjectKind+"\x00"+left.SubjectID+
				"\x00"+left.Delta+"\x00"+left.Cause+"\x00"+left.EvidenceReference,
			string(right.Queue)+"\x00"+right.SubjectKind+"\x00"+right.SubjectID+
				"\x00"+right.Delta+"\x00"+right.Cause+"\x00"+right.EvidenceReference,
		)
	})
	items := make([]ReviewItem, 0, len(candidates))
	for _, candidate := range candidates {
		item, err := newReviewItem(input, candidate)
		if err != nil {
			return nil, err
		}
		if len(items) > 0 && items[len(items)-1].ID == item.ID {
			continue
		}
		items = append(items, item)
	}
	sortReviewItemsByID(items)
	return items, nil
}

func sortReviewItemsByID(items []ReviewItem) {
	slices.SortFunc(items, func(left, right ReviewItem) int {
		return strings.Compare(left.ID, right.ID)
	})
}

func normalizeReviewCursor(cursor ReviewCursor) (ReviewCursor, string, error) {
	if cursor.Version == "" {
		cursor.Version = reviewCursorVersion
	}
	if cursor.Version != reviewCursorVersion {
		return ReviewCursor{}, "", errors.New("unsupported review cursor version")
	}
	var err error
	cursor.AcknowledgedItemIDs, err = canonicalStrings(
		"acknowledged review item",
		cursor.AcknowledgedItemIDs,
	)
	if err != nil {
		return ReviewCursor{}, "", err
	}
	for _, id := range cursor.AcknowledgedItemIDs {
		if !strings.HasPrefix(id, "iri_") {
			return ReviewCursor{}, "", errors.New("invalid acknowledged review item")
		}
	}
	if err := validateDomainString(
		"last viewed comparison",
		cursor.LastViewedComparisonID,
		false,
	); err != nil {
		return ReviewCursor{}, "", err
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return ReviewCursor{}, "", err
	}
	return cursor, string(encoded), nil
}

func ParseReviewCursor(value string) (*ReviewCursor, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var cursor ReviewCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, fmt.Errorf("decode review cursor: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode review cursor: trailing JSON value")
	}
	normalized, _, err := normalizeReviewCursor(cursor)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func reviewItemRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_review_item", id)
}

func reviewProjectionRecordID(
	principal, sourceSnapshotID, projectionID, projectionVersion string,
) models.RecordID {
	return models.NewRecordID(
		"investigation_review_projection",
		hashIdentity(
			"irp_",
			principal,
			sourceSnapshotID,
			projectionID,
			projectionVersion,
		),
	)
}

type reviewProjectionRecord struct {
	ID                string    `json:"projection_record_id"`
	Principal         string    `json:"principal"`
	InvestigationID   string    `json:"investigation_id"`
	SourceSnapshotID  string    `json:"source_snapshot_id"`
	SourceSequence    int       `json:"source_sequence"`
	ProjectionID      string    `json:"projection_id"`
	ProjectionVersion string    `json:"projection_version"`
	InputDigest       string    `json:"input_digest"`
	EvaluatedAt       time.Time `json:"evaluated_at"`
}

func (s *Surreal) existingReviewProjection(
	ctx context.Context,
	request ReviewProjectionRequest,
) (*reviewProjectionRecord, error) {
	results, err := surrealdb.Query[[]reviewProjectionRecord](
		ctx,
		s.db,
		`SELECT projection_record_id, principal, investigation_id,
			source_snapshot_id, source_sequence, projection_id, projection_version,
			input_digest, evaluated_at FROM $rid LIMIT 1`,
		map[string]any{
			"rid": reviewProjectionRecordID(
				request.Principal,
				request.SourceSnapshotID,
				request.Rule.ID,
				request.Rule.Version,
			),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("read review projection: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func (s *Surreal) latestReviewProjection(
	ctx context.Context,
	principal, investigationID, projectionID, projectionVersion string,
) (*reviewProjectionRecord, error) {
	results, err := surrealdb.Query[[]reviewProjectionRecord](
		ctx,
		s.db,
		`SELECT projection_record_id, principal, investigation_id,
			source_snapshot_id, source_sequence, projection_id, projection_version,
			input_digest, evaluated_at FROM investigation_review_projection
			WHERE principal = $principal AND investigation_id = $investigation_id
			  AND projection_id = $projection_id
			  AND projection_version = $projection_version
			ORDER BY source_sequence DESC LIMIT 1`,
		map[string]any{
			"principal": principal, "investigation_id": investigationID,
			"projection_id": projectionID, "projection_version": projectionVersion,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("read latest review projection: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func (s *Surreal) investigationReviewRevision(
	ctx context.Context,
	investigationID string,
) (int, error) {
	type revisionRow struct {
		Revision int `json:"revision"`
	}
	results, err := surrealdb.Query[[]revisionRow](
		ctx,
		s.db,
		`SELECT (review_revision ?? 0) AS revision FROM $rid
			WHERE investigation_id = $investigation_id LIMIT 1`,
		map[string]any{
			"rid":              investigationRecordID(investigationID),
			"investigation_id": investigationID,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("read investigation review revision: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return 0, ErrNotFound
	}
	return rows[0].Revision, nil
}

func (s *Surreal) listReviewItemsRaw(
	ctx context.Context,
	principal, investigationID, projectionID, projectionVersion string,
) ([]ReviewItem, error) {
	results, err := surrealdb.Query[[]ReviewItem](
		ctx,
		s.db,
		`SELECT review_item_id, projection_key, principal, investigation_id,
			source_snapshot_id, comparison_id, projection_id, projection_version,
			queue, subject_kind, subject_id, delta, cause, evidence_reference,
			human_record_state_digest, evaluated_at, expires_at,
			superseded_by, superseded_at, content_digest
			FROM investigation_review_item
			WHERE principal = $principal AND investigation_id = $investigation_id
			  AND ($projection_id = '' OR projection_id = $projection_id)
			  AND ($projection_version = '' OR projection_version = $projection_version)
			ORDER BY evaluated_at, review_item_id`,
		map[string]any{
			"principal": principal, "investigation_id": investigationID,
			"projection_id": projectionID, "projection_version": projectionVersion,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list review items: %w", err)
	}
	rows := firstDomainRows(results)
	for i := range rows {
		rows[i], err = normalizeReviewItem(rows[i])
		if err != nil {
			return nil, fmt.Errorf("list review items: row %d: %w", i, err)
		}
	}
	return rows, nil
}

const materializeReviewProjectionSQL = `
BEGIN;
LET $locked = UPDATE $investigation_rid SET review_revision = $next_revision
	WHERE investigation_id = $investigation_id
	  AND (review_revision ?? 0) = $expected_revision
	RETURN VALUE investigation_id;
IF array::len($locked) = 1 {
	FOR $row IN $superseded_rows {
		UPDATE $row.rid SET superseded_by = $projection_record_id,
			superseded_at = $evaluated_at RETURN NONE
	};
	FOR $row IN $review_items {
		CREATE $row.rid CONTENT $row.content RETURN NONE
	};
	CREATE $projection_rid CONTENT $projection_content RETURN NONE
};
RETURN $locked;
COMMIT;`

func reviewProjectionInputDigest(request ReviewProjectionRequest) (string, error) {
	normalized := request
	var err error
	normalized.Rule, err = normalizeReviewProjectionRule(normalized.Rule)
	if err != nil {
		return "", err
	}
	normalized.UnresolvedAttribution = slices.Clone(normalized.UnresolvedAttribution)
	slices.SortFunc(
		normalized.UnresolvedAttribution,
		func(left, right ReviewAttributionCondition) int {
			return strings.Compare(
				left.FactID+"\x00"+left.Hop+"\x00"+left.SubjectKind+"\x00"+left.SubjectID+"\x00"+left.ReasonCode,
				right.FactID+"\x00"+right.Hop+"\x00"+right.SubjectKind+"\x00"+right.SubjectID+"\x00"+right.ReasonCode,
			)
		},
	)
	return contentDigest(normalized)
}

// MaterializeReviewProjection derives the three built-in queues from an
// authorized immutable snapshot. It deliberately exposes no Put/Create method
// for ReviewItem.
func (s *Surreal) MaterializeReviewProjection(
	ctx context.Context,
	request ReviewProjectionRequest,
) ([]ReviewItem, error) {
	if !validInvestigationPrincipal(request.Principal) ||
		!strings.HasPrefix(request.SourceSnapshotID, "ics_") {
		return nil, ErrNotFound
	}
	var err error
	request.Rule, err = normalizeReviewProjectionRule(request.Rule)
	if err != nil {
		return nil, fmt.Errorf("materialize review projection: %w", err)
	}
	if err := validateDomainString(
		"human record state digest",
		request.HumanRecordStateDigest,
		true,
	); err != nil {
		return nil, fmt.Errorf("materialize review projection: %w", err)
	}
	snapshot, err := s.GetConsumerSnapshotAs(
		ctx,
		request.Principal,
		request.SourceSnapshotID,
	)
	if err != nil {
		return nil, err
	}
	projection, err := s.readInvestigationProjection(
		ctx,
		request.Principal,
		snapshot.Snapshot.InvestigationID,
	)
	if err != nil {
		return nil, ErrNotFound
	}
	artifact, err := s.GetRunArtifactAs(
		ctx,
		request.Principal,
		snapshot.Snapshot.ArtifactID,
	)
	if err != nil {
		return nil, err
	}
	coverage, _, err := ParseInvestigationCoverageLedger(artifact.CoverageLedger)
	if err != nil {
		return nil, fmt.Errorf("materialize review projection: coverage: %w", err)
	}
	factSet := make(map[string]ConsumerEdgeFact, len(snapshot.Snapshot.Facts))
	for _, fact := range snapshot.Snapshot.Facts {
		factSet[fact.FactID] = fact
	}
	request.UnresolvedAttribution, err = normalizeReviewAttribution(
		request.UnresolvedAttribution,
		factSet,
	)
	if err != nil {
		return nil, fmt.Errorf("materialize review projection: %w", err)
	}
	input := reviewProjectionInput{
		Principal:              request.Principal,
		InvestigationID:        snapshot.Snapshot.InvestigationID,
		SourceSnapshotID:       request.SourceSnapshotID,
		Comparison:             snapshot.Comparison,
		Coverage:               *coverage,
		Facts:                  snapshot.Snapshot.Facts,
		Rule:                   request.Rule,
		HumanRecordStateDigest: request.HumanRecordStateDigest,
		UnresolvedAttribution:  request.UnresolvedAttribution,
		EvaluatedAt:            snapshot.CreatedAt,
	}
	items, err := deriveReviewItems(input)
	if err != nil {
		return nil, fmt.Errorf("materialize review projection: %w", err)
	}
	inputDigest, err := reviewProjectionInputDigest(request)
	if err != nil {
		return nil, fmt.Errorf("materialize review projection: %w", err)
	}
	if existing, existingErr := s.existingReviewProjection(ctx, request); existingErr == nil {
		if existing.InputDigest != inputDigest ||
			existing.InvestigationID != snapshot.Snapshot.InvestigationID {
			return nil, fmt.Errorf(
				"materialize review projection: immutable input conflicts: %w",
				ErrConflict,
			)
		}
		return s.listReviewItemsForSnapshotAuthorized(
			ctx,
			request.Principal,
			request.SourceSnapshotID,
			request.Rule.ID,
			request.Rule.Version,
			projection,
		)
	} else if !errors.Is(existingErr, ErrNotFound) {
		return nil, existingErr
	}

	projectionRecordID := hashIdentity(
		"irp_",
		request.Principal,
		request.SourceSnapshotID,
		request.Rule.ID,
		request.Rule.Version,
	)
	for attempt := 0; attempt < maxQueueRetries; attempt++ {
		if attempt > 0 {
			existing, existingErr := s.existingReviewProjection(ctx, request)
			if existingErr == nil {
				if existing.InputDigest != inputDigest ||
					existing.InvestigationID != snapshot.Snapshot.InvestigationID {
					return nil, fmt.Errorf(
						"materialize review projection: immutable input conflicts: %w",
						ErrConflict,
					)
				}
				return s.listReviewItemsForSnapshotAuthorized(
					ctx,
					request.Principal,
					request.SourceSnapshotID,
					request.Rule.ID,
					request.Rule.Version,
					projection,
				)
			}
			if !errors.Is(existingErr, ErrNotFound) {
				return nil, existingErr
			}
		}
		revision, err := s.investigationReviewRevision(
			ctx,
			snapshot.Snapshot.InvestigationID,
		)
		if err != nil {
			return nil, err
		}
		latest, latestErr := s.latestReviewProjection(
			ctx,
			request.Principal,
			snapshot.Snapshot.InvestigationID,
			request.Rule.ID,
			request.Rule.Version,
		)
		if latestErr == nil {
			if latest.SourceSequence > snapshot.Sequence ||
				latest.SourceSequence == snapshot.Sequence &&
					latest.SourceSnapshotID != request.SourceSnapshotID ||
				input.EvaluatedAt.Before(latest.EvaluatedAt) {
				return nil, fmt.Errorf(
					"materialize review projection: source is older than the current projection: %w",
					ErrConflict,
				)
			}
		}
		if latestErr != nil && !errors.Is(latestErr, ErrNotFound) {
			return nil, latestErr
		}
		existing, err := s.listReviewItemsRaw(
			ctx,
			request.Principal,
			snapshot.Snapshot.InvestigationID,
			request.Rule.ID,
			request.Rule.Version,
		)
		if err != nil {
			return nil, err
		}
		supersededRows := make([]map[string]any, 0, len(existing))
		for _, item := range existing {
			if item.SupersededAt == nil && item.SourceSnapshotID != request.SourceSnapshotID {
				supersededRows = append(supersededRows, map[string]any{
					"rid": reviewItemRecordID(item.ID),
				})
			}
		}
		reviewItems := make([]map[string]any, len(items))
		for i, item := range items {
			reviewItems[i] = map[string]any{
				"rid": reviewItemRecordID(item.ID), "content": item,
			}
		}
		results, queryErr := surrealdb.Query[[]string](
			ctx,
			s.db,
			materializeReviewProjectionSQL,
			map[string]any{
				"investigation_rid": investigationRecordID(snapshot.Snapshot.InvestigationID),
				"investigation_id":  snapshot.Snapshot.InvestigationID,
				"expected_revision": revision, "next_revision": revision + 1,
				"superseded_rows":      supersededRows,
				"projection_record_id": projectionRecordID,
				"evaluated_at":         input.EvaluatedAt,
				"review_items":         reviewItems,
				"projection_rid": reviewProjectionRecordID(
					request.Principal,
					request.SourceSnapshotID,
					request.Rule.ID,
					request.Rule.Version,
				),
				"projection_content": reviewProjectionRecord{
					ID:                projectionRecordID,
					Principal:         request.Principal,
					InvestigationID:   snapshot.Snapshot.InvestigationID,
					SourceSnapshotID:  request.SourceSnapshotID,
					SourceSequence:    snapshot.Sequence,
					ProjectionID:      request.Rule.ID,
					ProjectionVersion: request.Rule.Version,
					InputDigest:       inputDigest,
					EvaluatedAt:       input.EvaluatedAt,
				},
			},
		)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil {
				continue
			}
			return nil, fmt.Errorf("materialize review projection: %w", queryErr)
		}
		if len(firstDomainRows(results)) != 1 {
			continue
		}
		if err := s.confirmInvestigationProjection(
			ctx,
			request.Principal,
			projection,
		); err != nil {
			return nil, err
		}
		return items, nil
	}
	return nil, fmt.Errorf(
		"materialize review projection: concurrent update: %w",
		ErrConflict,
	)
}

func (s *Surreal) listReviewItemsForSnapshot(
	ctx context.Context,
	principal, snapshotID, projectionID, projectionVersion string,
) ([]ReviewItem, error) {
	results, err := surrealdb.Query[[]ReviewItem](
		ctx,
		s.db,
		`SELECT review_item_id, projection_key, principal, investigation_id,
			source_snapshot_id, comparison_id, projection_id, projection_version,
			queue, subject_kind, subject_id, delta, cause, evidence_reference,
			human_record_state_digest, evaluated_at, expires_at,
			superseded_by, superseded_at, content_digest
			FROM investigation_review_item
			WHERE principal = $principal AND source_snapshot_id = $snapshot_id
			  AND projection_id = $projection_id
			  AND projection_version = $projection_version
			ORDER BY review_item_id`,
		map[string]any{
			"principal": principal, "snapshot_id": snapshotID,
			"projection_id": projectionID, "projection_version": projectionVersion,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list snapshot review items: %w", err)
	}
	rows := firstDomainRows(results)
	for i := range rows {
		rows[i], err = normalizeReviewItem(rows[i])
		if err != nil {
			return nil, fmt.Errorf("list snapshot review items: row %d: %w", i, err)
		}
	}
	// Keep an explicit in-process order as the contract boundary. The query's
	// ORDER BY is useful for the database plan, but callers must not depend on
	// a backend preserving that order across an idempotent materialization.
	sortReviewItemsByID(rows)
	return rows, nil
}

func (s *Surreal) listReviewItemsForSnapshotAuthorized(
	ctx context.Context,
	principal, snapshotID, projectionID, projectionVersion string,
	projection *investigationAuthz,
) ([]ReviewItem, error) {
	items, readErr := s.listReviewItemsForSnapshot(
		ctx,
		principal,
		snapshotID,
		projectionID,
		projectionVersion,
	)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	return items, readErr
}

func (s *Surreal) ListReviewItemsAs(
	ctx context.Context,
	principal, investigationID string,
	at time.Time,
) ([]ReviewItemView, error) {
	projection, err := s.readInvestigationProjection(ctx, principal, investigationID)
	if err != nil {
		return nil, ErrNotFound
	}
	if at.IsZero() {
		return nil, errors.New("review lifecycle evaluation time is required")
	}
	items, readErr := s.listReviewItemsRaw(ctx, principal, investigationID, "", "")
	cursor, cursorErr := s.GetReviewCursor(ctx, principal, investigationID)
	if cursorErr != nil && !errors.Is(cursorErr, ErrNotFound) {
		return nil, cursorErr
	}
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	acknowledged := map[string]struct{}{}
	if cursor != nil {
		for _, id := range cursor.AcknowledgedItemIDs {
			acknowledged[id] = struct{}{}
		}
	}
	views := make([]ReviewItemView, len(items))
	for i, item := range items {
		_, acknowledgedItem := acknowledged[item.ID]
		views[i] = ReviewItemView{
			ReviewItem:   item,
			Lifecycle:    item.LifecycleAt(at),
			Acknowledged: acknowledgedItem,
		}
	}
	return views, nil
}

func (s *Surreal) PutReviewCursor(
	ctx context.Context,
	principal, investigationID string,
	cursor ReviewCursor,
) (*ReviewCursor, error) {
	normalized, encoded, err := normalizeReviewCursor(cursor)
	if err != nil {
		return nil, fmt.Errorf("put review cursor: %w", err)
	}
	if _, err := s.PutInvestigationCursor(
		ctx,
		principal,
		investigationID,
		encoded,
	); err != nil {
		return nil, err
	}
	return &normalized, nil
}

func (s *Surreal) GetReviewCursor(
	ctx context.Context,
	principal, investigationID string,
) (*ReviewCursor, error) {
	cursor, err := s.GetInvestigationCursor(ctx, principal, investigationID)
	if err != nil {
		return nil, err
	}
	return ParseReviewCursor(cursor.Cursor)
}
