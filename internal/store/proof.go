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
	BundleID     string   `json:"bundle_id"`
	Content      string   `json:"content"`
	Repositories []string `json:"repositories"`
	RunIDs       []string `json:"run_ids"`
}

func (r proofBundleRec) bundle() ProofBundleRecord {
	return ProofBundleRecord{
		ID: r.BundleID, Content: r.Content,
		Repositories: r.Repositories, RunIDs: r.RunIDs,
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
		created_at = IF created_at = NONE THEN time::now() ELSE created_at END RETURN AFTER)
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
func (s *Surreal) GetProofBundle(ctx context.Context, id string) (*ProofBundleRecord, error) {
	if len(id) != len("pb_")+sha256.Size*2 || !strings.HasPrefix(id, "pb_") {
		return nil, fmt.Errorf("get proof bundle: invalid id: %w", ErrNotFound)
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(id, "pb_")); err != nil {
		return nil, fmt.Errorf("get proof bundle: invalid id: %w", ErrNotFound)
	}
	results, err := surrealdb.Query[[]proofBundleRec](ctx, s.db,
		"SELECT bundle_id, content, repositories, run_ids FROM $rid LIMIT 1",
		map[string]any{"rid": proofBundleID(id)})
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
