package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// InvestigationLedgerStore retains principal-scoped relationship history
// independently of sweepable RunArtifacts. Reads reauthorize the Investigation
// and never expose another principal's projection or counts.
type InvestigationLedgerStore interface {
	RecordConsumerSnapshot(context.Context, ConsumerSnapshot) (*ConsumerSnapshotRecord, error)
	GetConsumerSnapshotAs(context.Context, string, string) (*ConsumerSnapshotRecord, error)
	ListConsumerLedgerAs(context.Context, string, string) ([]ConsumerLedgerRow, error)
}

var _ InvestigationLedgerStore = (*Surreal)(nil)

type ConsumerSnapshotRecord struct {
	ID            string             `json:"snapshot_id"`
	Sequence      int                `json:"sequence"`
	Snapshot      ConsumerSnapshot   `json:"snapshot"`
	Continuity    string             `json:"continuity_namespace"`
	Comparison    ConsumerComparison `json:"comparison"`
	CreatedAt     time.Time          `json:"created_at"`
	ContentDigest string             `json:"content_digest"`
}

// ConsumerLedgerRow is a derived first/last-seen projection. A positively
// traced removal leaves an inactive tombstone so a later compatible positive
// observation can be classified as reintroduced.
type ConsumerLedgerRow struct {
	ID                    string           `json:"ledger_id"`
	Principal             string           `json:"principal"`
	InvestigationID       string           `json:"investigation_id"`
	Continuity            string           `json:"continuity_namespace"`
	LogicalRelationshipID string           `json:"logical_relationship_id"`
	CurrentFact           ConsumerEdgeFact `json:"current_fact"`
	FirstSeenSnapshotID   string           `json:"first_seen_snapshot_id"`
	FirstSeenArtifactID   string           `json:"first_seen_artifact_id"`
	LastSeenSnapshotID    string           `json:"last_seen_snapshot_id"`
	LastSeenArtifactID    string           `json:"last_seen_artifact_id"`
	LastRemovedSnapshotID string           `json:"last_removed_snapshot_id,omitempty"`
	LastRemovedArtifactID string           `json:"last_removed_artifact_id,omitempty"`
	ObservationCount      int              `json:"observation_count"`
	Active                bool             `json:"active"`
	UpdatedAt             time.Time        `json:"updated_at"`
	ContentDigest         string           `json:"content_digest"`
}

func consumerSnapshotRecordID(principal, artifactID string) models.RecordID {
	return models.NewRecordID(
		"investigation_consumer_snapshot",
		hashIdentity("ics_", principal, artifactID),
	)
}

func consumerLedgerRecordID(
	principal, investigationID, continuity, logicalID string,
) models.RecordID {
	return models.NewRecordID(
		"investigation_consumer_edge_ledger",
		hashIdentity("icel_", principal, investigationID, continuity, logicalID),
	)
}

func consumerSnapshotCore(record ConsumerSnapshotRecord) any {
	return struct {
		ID, Continuity string
		Sequence       int
		Snapshot       ConsumerSnapshot
		Comparison     ConsumerComparison
		CreatedAt      time.Time
	}{
		record.ID,
		record.Continuity,
		record.Sequence,
		record.Snapshot,
		record.Comparison,
		record.CreatedAt,
	}
}

func normalizeConsumerSnapshotRecord(
	record ConsumerSnapshotRecord,
) (ConsumerSnapshotRecord, error) {
	var err error
	record.Snapshot, err = normalizeConsumerSnapshot(record.Snapshot)
	if err != nil {
		return ConsumerSnapshotRecord{}, err
	}
	wantID := hashIdentity("ics_", record.Snapshot.Principal, record.Snapshot.ArtifactID)
	if record.ID != wantID || record.Sequence < 1 ||
		record.Continuity != consumerContinuityNamespace(record.Snapshot) {
		return ConsumerSnapshotRecord{}, errors.New("consumer snapshot record identity is inconsistent")
	}
	if err := validateConsumerComparison(record.Comparison); err != nil {
		return ConsumerSnapshotRecord{}, err
	}
	record.CreatedAt = storeTimestamp(record.CreatedAt)
	digest, err := contentDigest(consumerSnapshotCore(record))
	if err != nil {
		return ConsumerSnapshotRecord{}, err
	}
	if record.ContentDigest != "" && record.ContentDigest != digest {
		return ConsumerSnapshotRecord{}, errors.New("consumer snapshot record digest mismatch")
	}
	record.ContentDigest = digest
	return record, nil
}

func validateConsumerComparison(comparison ConsumerComparison) error {
	for name, value := range map[string]string{
		"comparison id":           comparison.ComparisonID,
		"right artifact":          comparison.RightArtifactID,
		"comparison rule":         comparison.RuleID,
		"comparison rule version": comparison.RuleVersion,
		"comparison rule digest":  comparison.RuleDigest,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return err
		}
	}
	if comparison.NonComparabilityCauses == nil || comparison.Deltas == nil {
		return errors.New("comparison causes and deltas must be arrays")
	}
	if comparison.Comparable {
		if comparison.PerSideCoverageOnly || len(comparison.NonComparabilityCauses) != 0 {
			return errors.New("comparable result carries comparison-report limitations")
		}
	} else if !comparison.PerSideCoverageOnly ||
		len(comparison.NonComparabilityCauses) == 0 ||
		len(comparison.Deltas) != 0 {
		return errors.New("non-comparable result presents an ordinary delta")
	}
	for _, delta := range comparison.Deltas {
		if delta.LogicalRelationshipID == "" ||
			!slices.Contains([]string{"added", "removed", "reintroduced", "modified"}, delta.Change) {
			return errors.New("comparison contains an invalid delta")
		}
		switch delta.Change {
		case "removed":
			if delta.Cause != "relationship_removed_traced" ||
				delta.Before == nil || delta.After != nil {
				return errors.New("removal is not positively traced")
			}
		case "added", "reintroduced":
			if delta.Cause != "relationship_added_traced" ||
				delta.Before != nil || delta.After == nil {
				return errors.New("addition is not positively traced")
			}
		case "modified":
			if delta.Cause != "source_modified" ||
				delta.Before == nil || delta.After == nil {
				return errors.New("modification is not positively traced")
			}
		}
	}
	return nil
}

func consumerLedgerCore(row ConsumerLedgerRow) any {
	return struct {
		ID, Principal, InvestigationID, Continuity, LogicalRelationshipID string
		CurrentFact                                                       ConsumerEdgeFact
		FirstSeenSnapshotID, FirstSeenArtifactID                          string
		LastSeenSnapshotID, LastSeenArtifactID                            string
		LastRemovedSnapshotID, LastRemovedArtifactID                      string
		ObservationCount                                                  int
		Active                                                            bool
		UpdatedAt                                                         time.Time
	}{
		row.ID, row.Principal, row.InvestigationID, row.Continuity,
		row.LogicalRelationshipID, row.CurrentFact,
		row.FirstSeenSnapshotID, row.FirstSeenArtifactID,
		row.LastSeenSnapshotID, row.LastSeenArtifactID,
		row.LastRemovedSnapshotID, row.LastRemovedArtifactID,
		row.ObservationCount, row.Active, row.UpdatedAt,
	}
}

func normalizeConsumerLedgerRow(row ConsumerLedgerRow) (ConsumerLedgerRow, error) {
	wantID := hashIdentity(
		"icel_",
		row.Principal,
		row.InvestigationID,
		row.Continuity,
		row.LogicalRelationshipID,
	)
	if row.ID != wantID || !validInvestigationPrincipal(row.Principal) ||
		!validULID(row.InvestigationID) || row.ObservationCount < 1 ||
		row.CurrentFact.LogicalRelationshipID != row.LogicalRelationshipID {
		return ConsumerLedgerRow{}, errors.New("consumer ledger row identity is inconsistent")
	}
	for name, value := range map[string]string{
		"continuity namespace":    row.Continuity,
		"logical relationship id": row.LogicalRelationshipID,
		"first seen snapshot":     row.FirstSeenSnapshotID,
		"first seen artifact":     row.FirstSeenArtifactID,
		"last seen snapshot":      row.LastSeenSnapshotID,
		"last seen artifact":      row.LastSeenArtifactID,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return ConsumerLedgerRow{}, err
		}
	}
	// A reintroduction keeps the historical tombstone. Active rows may
	// therefore carry both removal fields, but they must remain paired.
	if (row.LastRemovedSnapshotID == "") != (row.LastRemovedArtifactID == "") {
		return ConsumerLedgerRow{}, errors.New("consumer ledger removal tombstone is incomplete")
	}
	row.UpdatedAt = storeTimestamp(row.UpdatedAt)
	digest, err := contentDigest(consumerLedgerCore(row))
	if err != nil {
		return ConsumerLedgerRow{}, err
	}
	if row.ContentDigest != "" && row.ContentDigest != digest {
		return ConsumerLedgerRow{}, errors.New("consumer ledger row digest mismatch")
	}
	row.ContentDigest = digest
	return row, nil
}

func firstConsumerSnapshotRows(
	results *[]surrealdb.QueryResult[[]ConsumerSnapshotRecord],
) []ConsumerSnapshotRecord {
	return firstDomainRows(results)
}

func (s *Surreal) existingConsumerSnapshot(
	ctx context.Context,
	principal, artifactID string,
) (*ConsumerSnapshotRecord, error) {
	results, err := surrealdb.Query[[]ConsumerSnapshotRecord](
		ctx,
		s.db,
		`SELECT snapshot_id, sequence, snapshot, continuity_namespace, comparison,
			created_at, content_digest FROM $rid LIMIT 1`,
		map[string]any{"rid": consumerSnapshotRecordID(principal, artifactID)},
	)
	if err != nil {
		return nil, fmt.Errorf("read consumer snapshot: %w", err)
	}
	rows := firstConsumerSnapshotRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	row, err := normalizeConsumerSnapshotRecord(rows[0])
	if err != nil {
		return nil, fmt.Errorf("read consumer snapshot: %w", err)
	}
	return &row, nil
}

func (s *Surreal) latestConsumerSnapshot(
	ctx context.Context,
	principal, investigationID string,
) (*ConsumerSnapshotRecord, error) {
	results, err := surrealdb.Query[[]ConsumerSnapshotRecord](
		ctx,
		s.db,
		`SELECT snapshot_id, sequence, snapshot, continuity_namespace, comparison,
			created_at, content_digest FROM investigation_consumer_snapshot
			WHERE principal = $principal AND investigation_id = $investigation_id
			ORDER BY sequence DESC LIMIT 1`,
		map[string]any{"principal": principal, "investigation_id": investigationID},
	)
	if err != nil {
		return nil, fmt.Errorf("read latest consumer snapshot: %w", err)
	}
	rows := firstConsumerSnapshotRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	row, err := normalizeConsumerSnapshotRecord(rows[0])
	if err != nil {
		return nil, fmt.Errorf("read latest consumer snapshot: %w", err)
	}
	return &row, nil
}

func firstComparison(snapshot ConsumerSnapshot) ConsumerComparison {
	ruleDigest, _ := contentDigest(struct {
		ID, Version string
	}{consumerComparisonRuleID, consumerComparisonRuleVersion})
	return ConsumerComparison{
		ComparisonID: hashIdentity(
			"iccmp_",
			consumerComparisonRuleID,
			consumerComparisonRuleVersion,
			"",
			snapshot.ArtifactID,
			snapshot.Semantics.VisibilityProjection,
		),
		RightArtifactID: snapshot.ArtifactID,
		Comparable:      false,
		RuleID:          consumerComparisonRuleID,
		RuleVersion:     consumerComparisonRuleVersion,
		RuleDigest:      ruleDigest,
		NonComparabilityCauses: []string{
			"no_prior_snapshot",
		},
		PerSideCoverageOnly: true,
		Deltas:              []ConsumerDelta{},
	}
}

func (s *Surreal) consumerLedgerRows(
	ctx context.Context,
	principal, investigationID, continuity string,
) ([]ConsumerLedgerRow, error) {
	results, err := surrealdb.Query[[]ConsumerLedgerRow](
		ctx,
		s.db,
		`SELECT ledger_id, principal, investigation_id, continuity_namespace,
			logical_relationship_id, current_fact, first_seen_snapshot_id,
			first_seen_artifact_id, last_seen_snapshot_id, last_seen_artifact_id,
			last_removed_snapshot_id, last_removed_artifact_id, observation_count,
			active, updated_at, content_digest
			FROM investigation_consumer_edge_ledger
			WHERE principal = $principal AND investigation_id = $investigation_id
			  AND continuity_namespace = $continuity
			ORDER BY logical_relationship_id`,
		map[string]any{
			"principal": principal, "investigation_id": investigationID,
			"continuity": continuity,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("read consumer ledger: %w", err)
	}
	rows := firstDomainRows(results)
	for i := range rows {
		rows[i], err = normalizeConsumerLedgerRow(rows[i])
		if err != nil {
			return nil, fmt.Errorf("read consumer ledger: row %d: %w", i, err)
		}
	}
	return rows, nil
}

func buildConsumerLedgerRows(
	snapshotID string,
	snapshot ConsumerSnapshot,
	comparison *ConsumerComparison,
	existing []ConsumerLedgerRow,
	now time.Time,
) ([]ConsumerLedgerRow, error) {
	rows := make(map[string]ConsumerLedgerRow, len(existing)+len(snapshot.Facts))
	for _, row := range existing {
		rows[row.LogicalRelationshipID] = row
	}
	for _, fact := range snapshot.Facts {
		row, ok := rows[fact.LogicalRelationshipID]
		if !ok {
			row = ConsumerLedgerRow{
				ID: hashIdentity(
					"icel_",
					snapshot.Principal,
					snapshot.InvestigationID,
					consumerContinuityNamespace(snapshot),
					fact.LogicalRelationshipID,
				),
				Principal:             snapshot.Principal,
				InvestigationID:       snapshot.InvestigationID,
				Continuity:            consumerContinuityNamespace(snapshot),
				LogicalRelationshipID: fact.LogicalRelationshipID,
				FirstSeenSnapshotID:   snapshotID,
				FirstSeenArtifactID:   snapshot.ArtifactID,
			}
		} else if !row.Active && comparison.Comparable {
			for i := range comparison.Deltas {
				if comparison.Deltas[i].LogicalRelationshipID == fact.LogicalRelationshipID &&
					comparison.Deltas[i].Change == "added" {
					comparison.Deltas[i].Change = "reintroduced"
				}
			}
		}
		row.CurrentFact = fact
		row.LastSeenSnapshotID = snapshotID
		row.LastSeenArtifactID = snapshot.ArtifactID
		row.ObservationCount++
		row.Active = true
		row.UpdatedAt = now
		row.ContentDigest = ""
		normalized, err := normalizeConsumerLedgerRow(row)
		if err != nil {
			return nil, err
		}
		rows[fact.LogicalRelationshipID] = normalized
	}
	if comparison.Comparable {
		present := make(map[string]struct{}, len(snapshot.Facts))
		for _, fact := range snapshot.Facts {
			present[fact.LogicalRelationshipID] = struct{}{}
		}
		for logicalID, row := range rows {
			if !row.Active {
				continue
			}
			if _, ok := present[logicalID]; ok {
				continue
			}
			row.Active = false
			row.LastRemovedSnapshotID = snapshotID
			row.LastRemovedArtifactID = snapshot.ArtifactID
			row.UpdatedAt = now
			row.ContentDigest = ""
			normalized, err := normalizeConsumerLedgerRow(row)
			if err != nil {
				return nil, err
			}
			rows[logicalID] = normalized
		}
	}
	result := make([]ConsumerLedgerRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, row)
	}
	slices.SortFunc(result, func(a, b ConsumerLedgerRow) int {
		return strings.Compare(a.LogicalRelationshipID, b.LogicalRelationshipID)
	})
	return result, nil
}

const recordConsumerSnapshotSQL = `
BEGIN;
LET $locked = UPDATE $investigation_rid SET ledger_revision = $next_revision
	WHERE investigation_id = $investigation_id
	  AND (ledger_revision ?? 0) = $expected_revision RETURN AFTER;
LET $saved = IF array::len($locked) = 1 THEN
	(CREATE $snapshot_rid CONTENT $snapshot_content RETURN AFTER)
	ELSE [] END;
FOR $row IN $ledger_rows {
	IF array::len($saved) = 1 {
		UPSERT $row.rid CONTENT $row.content RETURN NONE
	}
};
RETURN $saved;
COMMIT;`

func (s *Surreal) investigationLedgerRevision(
	ctx context.Context,
	investigationID string,
) (int, error) {
	type revisionRow struct {
		Revision int `json:"revision"`
	}
	results, err := surrealdb.Query[[]revisionRow](
		ctx,
		s.db,
		`SELECT (ledger_revision ?? 0) AS revision FROM $rid
			WHERE investigation_id = $investigation_id LIMIT 1`,
		map[string]any{
			"rid":              investigationRecordID(investigationID),
			"investigation_id": investigationID,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("read investigation ledger revision: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return 0, ErrNotFound
	}
	return rows[0].Revision, nil
}

// RecordConsumerSnapshot appends one immutable principal-scoped census and
// updates only its compact derived ledger rows. It never interprets a missing
// fact as removal unless CompareConsumerSnapshots returned comparable.
func (s *Surreal) RecordConsumerSnapshot(
	ctx context.Context,
	in ConsumerSnapshot,
) (*ConsumerSnapshotRecord, error) {
	in, err := normalizeConsumerSnapshot(in)
	if err != nil {
		return nil, fmt.Errorf("record consumer snapshot: %w", err)
	}
	investigationID, resolutionErr := s.investigationIDForRunArtifact(ctx, in.ArtifactID)
	projection, err := s.readAuthorizedProjection(ctx, in.Principal, investigationID, resolutionErr)
	if err != nil || investigationID != in.InvestigationID {
		return nil, ErrNotFound
	}
	artifact, err := s.GetRunArtifactAs(ctx, in.Principal, in.ArtifactID)
	if err != nil {
		return nil, err
	}
	if artifact.TerminalStatus != RunPublished {
		return nil, errors.New("record consumer snapshot: artifact is not published")
	}
	run, err := s.GetRunAs(ctx, in.Principal, artifact.RunID)
	if err != nil {
		return nil, err
	}
	if run.RevisionID != in.RevisionID {
		return nil, errors.New("record consumer snapshot: revision does not match artifact")
	}
	factReferences := make(map[string]struct{}, len(artifact.FactReferences))
	for _, factID := range artifact.FactReferences {
		factReferences[factID] = struct{}{}
	}
	for _, fact := range in.Facts {
		if _, ok := factReferences[fact.FactID]; !ok {
			return nil, fmt.Errorf(
				"record consumer snapshot: fact %q is absent from artifact",
				fact.FactID,
			)
		}
	}
	for attempt := 0; attempt < maxQueueRetries; attempt++ {
		if existing, getErr := s.existingConsumerSnapshot(ctx, in.Principal, in.ArtifactID); getErr == nil {
			if existing.Snapshot.Principal != in.Principal ||
				existing.Snapshot.ArtifactID != in.ArtifactID ||
				!consumerSnapshotsEqual(existing.Snapshot, in) {
				return nil, fmt.Errorf("record consumer snapshot: immutable content conflicts: %w", ErrConflict)
			}
			if err := s.confirmInvestigationProjection(ctx, in.Principal, projection); err != nil {
				return nil, err
			}
			return existing, nil
		} else if !errors.Is(getErr, ErrNotFound) {
			return nil, getErr
		}
		revision, err := s.investigationLedgerRevision(ctx, in.InvestigationID)
		if err != nil {
			return nil, err
		}
		latest, latestErr := s.latestConsumerSnapshot(ctx, in.Principal, in.InvestigationID)
		var comparison ConsumerComparison
		sequence := 1
		if errors.Is(latestErr, ErrNotFound) {
			comparison = firstComparison(in)
		} else if latestErr != nil {
			return nil, latestErr
		} else {
			sequence = latest.Sequence + 1
			comparison, err = CompareConsumerSnapshots(latest.Snapshot, in)
			if err != nil {
				return nil, fmt.Errorf("record consumer snapshot: compare: %w", err)
			}
		}
		continuity := consumerContinuityNamespace(in)
		ledgerRows, err := s.consumerLedgerRows(
			ctx,
			in.Principal,
			in.InvestigationID,
			continuity,
		)
		if err != nil {
			return nil, err
		}
		now := storeTimestamp(time.Now())
		record := ConsumerSnapshotRecord{
			ID:       hashIdentity("ics_", in.Principal, in.ArtifactID),
			Sequence: sequence, Snapshot: in, Continuity: continuity,
			Comparison: comparison, CreatedAt: now,
		}
		updatedRows, err := buildConsumerLedgerRows(
			record.ID,
			in,
			&record.Comparison,
			ledgerRows,
			now,
		)
		if err != nil {
			return nil, fmt.Errorf("record consumer snapshot: ledger: %w", err)
		}
		record, err = normalizeConsumerSnapshotRecord(record)
		if err != nil {
			return nil, fmt.Errorf("record consumer snapshot: %w", err)
		}
		rows := make([]map[string]any, len(updatedRows))
		for i, row := range updatedRows {
			rows[i] = map[string]any{
				"rid": consumerLedgerRecordID(
					row.Principal,
					row.InvestigationID,
					row.Continuity,
					row.LogicalRelationshipID,
				),
				"content": row,
			}
		}
		results, queryErr := surrealdb.Query[[]ConsumerSnapshotRecord](
			ctx,
			s.db,
			recordConsumerSnapshotSQL,
			map[string]any{
				"investigation_rid": investigationRecordID(in.InvestigationID),
				"investigation_id":  in.InvestigationID,
				"expected_revision": revision,
				"next_revision":     revision + 1,
				"snapshot_rid":      consumerSnapshotRecordID(in.Principal, in.ArtifactID),
				"snapshot_content": map[string]any{
					"snapshot_id":          record.ID,
					"sequence":             record.Sequence,
					"principal":            in.Principal,
					"investigation_id":     in.InvestigationID,
					"artifact_id":          in.ArtifactID,
					"snapshot":             record.Snapshot,
					"continuity_namespace": record.Continuity,
					"comparison":           record.Comparison,
					"created_at":           record.CreatedAt,
					"content_digest":       record.ContentDigest,
				},
				"ledger_rows": rows,
			},
		)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil {
				continue
			}
			return nil, fmt.Errorf("record consumer snapshot: %w", queryErr)
		}
		saved := firstConsumerSnapshotRows(results)
		if len(saved) != 1 {
			continue
		}
		saved[0], err = normalizeConsumerSnapshotRecord(saved[0])
		if err != nil {
			return nil, fmt.Errorf("record consumer snapshot: saved row: %w", err)
		}
		if err := s.confirmInvestigationProjection(ctx, in.Principal, projection); err != nil {
			return nil, err
		}
		return &saved[0], nil
	}
	return nil, fmt.Errorf("record consumer snapshot: concurrent update: %w", ErrConflict)
}

func consumerSnapshotsEqual(left, right ConsumerSnapshot) bool {
	leftDigest, leftErr := contentDigest(left)
	rightDigest, rightErr := contentDigest(right)
	return leftErr == nil && rightErr == nil && leftDigest == rightDigest
}

func (s *Surreal) GetConsumerSnapshotAs(
	ctx context.Context,
	principal, id string,
) (*ConsumerSnapshotRecord, error) {
	if !strings.HasPrefix(id, "ics_") {
		return nil, ErrNotFound
	}
	investigationID, resolutionErr := s.lookupRecordString(
		ctx,
		"SELECT investigation_id AS value FROM $rid LIMIT 1",
		models.NewRecordID("investigation_consumer_snapshot", id),
	)
	projection, err := s.readAuthorizedProjection(ctx, principal, investigationID, resolutionErr)
	if err != nil {
		return nil, err
	}
	results, readErr := surrealdb.Query[[]ConsumerSnapshotRecord](
		ctx,
		s.db,
		`SELECT snapshot_id, sequence, snapshot, continuity_namespace, comparison,
			created_at, content_digest FROM $rid
			WHERE principal = $principal LIMIT 1`,
		map[string]any{
			"rid":       models.NewRecordID("investigation_consumer_snapshot", id),
			"principal": principal,
		},
	)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, fmt.Errorf("get consumer snapshot: %w", readErr)
	}
	rows := firstConsumerSnapshotRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	row, err := normalizeConsumerSnapshotRecord(rows[0])
	if err != nil {
		return nil, fmt.Errorf("get consumer snapshot: %w", err)
	}
	return &row, nil
}

func (s *Surreal) ListConsumerLedgerAs(
	ctx context.Context,
	principal, investigationID string,
) ([]ConsumerLedgerRow, error) {
	projection, err := s.readInvestigationProjection(ctx, principal, investigationID)
	if err != nil {
		return nil, ErrNotFound
	}
	results, readErr := surrealdb.Query[[]ConsumerLedgerRow](
		ctx,
		s.db,
		`SELECT ledger_id, principal, investigation_id, continuity_namespace,
			logical_relationship_id, current_fact, first_seen_snapshot_id,
			first_seen_artifact_id, last_seen_snapshot_id, last_seen_artifact_id,
			last_removed_snapshot_id, last_removed_artifact_id, observation_count,
			active, updated_at, content_digest
			FROM investigation_consumer_edge_ledger
			WHERE principal = $principal AND investigation_id = $investigation_id
			ORDER BY continuity_namespace, logical_relationship_id`,
		map[string]any{
			"principal":        principal,
			"investigation_id": investigationID,
		},
	)
	if err := s.confirmInvestigationProjection(ctx, principal, projection); err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, fmt.Errorf("list consumer ledger: %w", readErr)
	}
	rows := firstDomainRows(results)
	for i := range rows {
		rows[i], err = normalizeConsumerLedgerRow(rows[i])
		if err != nil {
			return nil, fmt.Errorf("list consumer ledger: row %d: %w", i, err)
		}
	}
	return rows, nil
}
