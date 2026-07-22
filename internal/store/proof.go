package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const (
	maxProofBundleContentBytes = 64 << 20
	maxProofBundleScopes       = 10_000
)

// ComputeProofBundleID returns the immutable identity of canonical bundle
// content. Content deliberately excludes the ID itself.
func ComputeProofBundleID(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "pb_" + hex.EncodeToString(sum[:])
}

func proofBundleID(id string) models.RecordID { return models.NewRecordID("proof_bundle", id) }

type proofBundleRec struct {
	RecID        *models.RecordID `json:"id"`
	BundleID     string           `json:"bundle_id"`
	Content      string           `json:"content"`
	Repositories []string         `json:"repositories"`
	RunIDs       []string         `json:"run_ids"`
	CreatedAt    *time.Time       `json:"created_at"`
	RetainedAt   *time.Time       `json:"retained_at"`
}

func (r proofBundleRec) bundle() ProofBundleRecord {
	retainedAt := r.RetainedAt
	if retainedAt == nil {
		retainedAt = r.CreatedAt
	}
	var retained time.Time
	if retainedAt != nil {
		retained = retainedAt.UTC()
	}
	return ProofBundleRecord{
		ID: r.BundleID, Content: r.Content,
		Repositories: r.Repositories, RunIDs: r.RunIDs, RetainedAt: retained,
	}
}

func firstProofBundleRows(results *[]surrealdb.QueryResult[[]proofBundleRec]) []proofBundleRec {
	for _, result := range *results {
		if len(result.Result) > 0 {
			return result.Result
		}
	}
	return nil
}

func validateProofBundle(bundle ProofBundleRecord) error {
	if bundle.ID == "" || bundle.ID != ComputeProofBundleID(bundle.Content) {
		return errors.New("proof bundle id does not match canonical content")
	}
	if len(bundle.Content) == 0 || len(bundle.Content) > maxProofBundleContentBytes ||
		!utf8.ValidString(bundle.Content) || !json.Valid([]byte(bundle.Content)) {
		return errors.New("proof bundle content is not bounded valid JSON")
	}
	if err := validateProofScopes("repositories", bundle.Repositories); err != nil {
		return err
	}
	if err := validateProofScopes("run ids", bundle.RunIDs); err != nil {
		return err
	}
	if bundle.RetainedAt.IsZero() {
		return errors.New("proof bundle retained-at time is required")
	}
	return nil
}

func validateProofScopes(name string, values []string) error {
	if len(values) > maxProofBundleScopes {
		return fmt.Errorf("proof bundle %s exceed %d entries", name, maxProofBundleScopes)
	}
	if !sort.StringsAreSorted(values) {
		return fmt.Errorf("proof bundle %s are not sorted", name)
	}
	for i, value := range values {
		if strings.TrimSpace(value) == "" || !utf8.ValidString(value) || len(value) > maxEvidenceIdentityBytes ||
			i > 0 && values[i-1] == value {
			return fmt.Errorf("proof bundle %s contain an invalid or duplicate identity", name)
		}
	}
	return nil
}

const putProofBundleSQL = `
BEGIN;
LET $eligible = SELECT VALUE run_id FROM extraction_run
	WHERE run_id IN $run_ids
	  AND evidence_format_version = $evidence_format_version
	  AND retention_quarantined = false
	  AND run_id = record::id(id)
	  AND ` + evidenceRunHasNoAmbiguousClaimantSQL + `
	  AND ((status = 'published' AND published_key != NONE)
	    OR (status = 'superseded' AND published_key = NONE));
LET $existing = SELECT bundle_id, content, repositories, run_ids FROM $rid LIMIT 1;
LET $immutable = array::len($existing) = 0 OR
	($existing[0].bundle_id = $bundle_id AND $existing[0].content = $content
	 AND $existing[0].repositories = $repositories AND $existing[0].run_ids = $run_ids);
LET $ready = array::len($eligible) = array::len($run_ids) AND $immutable;
LET $saved = IF $ready THEN
	(UPSERT $rid SET bundle_id = $bundle_id, content = $content,
		repositories = $repositories, run_ids = $run_ids,
		created_at = IF created_at = NONE THEN $retained_at ELSE created_at END,
		retained_at = $retained_at RETURN AFTER)
	ELSE [] END;
FOR $pin IN $pins {
	IF $ready {
		UPSERT $pin.rid SET pin_key = $pin.pin_key, run_id = $pin.run_id,
			kind = $kind, created_at = IF created_at = NONE THEN time::now() ELSE created_at END RETURN NONE
	}
};
RETURN $saved;
COMMIT;`

// PutProofBundle atomically persists immutable canonical content and pins all
// referenced compatible runs. Exact retries are idempotent.
func (s *Surreal) PutProofBundle(ctx context.Context, bundle ProofBundleRecord) error {
	if err := validateProofBundle(bundle); err != nil {
		return fmt.Errorf("put proof bundle: %w", err)
	}
	kind := "proof-bundle:" + bundle.ID
	pins := make([]map[string]any, len(bundle.RunIDs))
	for i, runID := range bundle.RunIDs {
		pins[i] = map[string]any{
			"rid":     evidencePinRecordID(runID, kind),
			"pin_key": hashIdentity("pin_", runID, kind),
			"run_id":  runID,
		}
	}
	vars := map[string]any{
		"rid": proofBundleID(bundle.ID), "bundle_id": bundle.ID,
		"content": bundle.Content, "repositories": bundle.Repositories,
		"run_ids": bundle.RunIDs, "pins": pins, "kind": kind,
		"retained_at":                 bundle.RetainedAt.UTC(),
		"evidence_format_version":     evidenceFormatVersion,
		"max_evidence_identity_bytes": maxEvidenceIdentityBytes,
	}
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]proofBundleRec](ctx, s.db, putProofBundleSQL, vars)
		if err != nil {
			if isRetryable(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("put proof bundle: %w", err)
		}
		if len(firstProofBundleRows(results)) == 1 {
			return nil
		}
		return fmt.Errorf("put proof bundle: referenced run is unavailable or immutable content conflicts: %w", ErrConflict)
	}
}

// GetProofBundle returns opaque content without authorizing its repository
// scope. The API must reauthorize Repositories before returning Content.
func (s *Surreal) GetProofBundle(ctx context.Context, id string, activeAfter *time.Time) (*ProofBundleRecord, error) {
	if len(id) != len("pb_")+sha256.Size*2 || !strings.HasPrefix(id, "pb_") {
		return nil, fmt.Errorf("get proof bundle: invalid id: %w", ErrNotFound)
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(id, "pb_")); err != nil {
		return nil, fmt.Errorf("get proof bundle: invalid id: %w", ErrNotFound)
	}
	results, err := surrealdb.Query[[]proofBundleRec](ctx, s.db,
		`SELECT bundle_id, content, repositories, run_ids, created_at, retained_at
			FROM $rid
			WHERE $active_after = NONE OR (retained_at ?? created_at) > $active_after
			LIMIT 1`,
		map[string]any{"rid": proofBundleID(id), "active_after": activeAfter})
	if err != nil {
		return nil, fmt.Errorf("get proof bundle: %w", err)
	}
	rows := firstProofBundleRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	bundle := rows[0].bundle()
	if bundle.ID != id {
		return nil, errors.New("get proof bundle: stored identity is inconsistent")
	}
	if err := validateProofBundle(bundle); err != nil {
		return nil, fmt.Errorf("get proof bundle: stored bundle is inconsistent: %w", err)
	}
	return &bundle, nil
}

const sweepProofBundleSQL = `
BEGIN;
LET $locked = UPDATE $rid SET retention_revision = (retention_revision ?? 0) + 1
	WHERE bundle_id = $bundle_id
	  AND (retained_at ?? created_at) <= $active_after RETURN AFTER;
IF array::len($locked) = 1 {
	DELETE evidence_pin WHERE kind = $kind RETURN NONE
};
LET $deleted = IF array::len($locked) = 1 THEN (DELETE $rid RETURN BEFORE) ELSE [] END;
RETURN [{ deleted: array::len($deleted) }];
COMMIT;`

type proofBundleSweepResultRec struct {
	Deleted int `json:"deleted"`
}

// SweepProofBundles releases exactly one expired bundle and its own pins in
// one transaction. It never deletes extraction evidence; SweepEvidence makes
// the independent decision whether a newly unpinned run is reclaimable.
func (s *Surreal) SweepProofBundles(ctx context.Context, activeAfter time.Time) (int, error) {
	if activeAfter.IsZero() {
		return 0, errors.New("sweep proof bundles: active-after cutoff is required")
	}
	var rows []proofBundleRec
	for _, candidateSQL := range []string{
		`SELECT id, bundle_id, created_at, retained_at FROM proof_bundle
			WHERE retained_at != NONE AND retained_at <= $active_after
			ORDER BY retained_at, bundle_id LIMIT 1`,
		`SELECT id, bundle_id, created_at, retained_at FROM proof_bundle
			WHERE retained_at = NONE AND created_at <= $active_after
			ORDER BY created_at, bundle_id LIMIT 1`,
	} {
		candidates, err := surrealdb.Query[[]proofBundleRec](ctx, s.db, candidateSQL,
			map[string]any{"active_after": activeAfter.UTC()})
		if err != nil {
			return 0, fmt.Errorf("sweep proof bundles: candidate: %w", err)
		}
		rows = firstProofBundleRows(candidates)
		if len(rows) > 0 {
			break
		}
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if rows[0].RecID == nil || rows[0].BundleID == "" {
		return 0, errors.New("sweep proof bundles: candidate identity is inconsistent")
	}
	candidate := rows[0]
	vars := map[string]any{
		"rid": *candidate.RecID, "bundle_id": candidate.BundleID,
		"kind": "proof-bundle:" + candidate.BundleID, "active_after": activeAfter.UTC(),
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]proofBundleSweepResultRec](ctx, s.db, sweepProofBundleSQL, vars)
		if queryErr != nil {
			if isRetryable(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return 0, fmt.Errorf("sweep proof bundles: bundle %s: %w", candidate.BundleID, queryErr)
		}
		deleted := 0
		for _, result := range *results {
			for _, row := range result.Result {
				deleted += row.Deleted
			}
		}
		if deleted < 0 || deleted > 1 {
			return 0, fmt.Errorf("sweep proof bundles: invalid delete count %d", deleted)
		}
		return deleted, nil
	}
}
