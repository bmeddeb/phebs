package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

// Both source writers include this guard inside their existing transaction and
// update the epoch in the existing repo write. No additional row is mutated.
const typedSourceEpochGuardSQL = `
IF $typed_source_changed AND ($before.typed_source_epoch ?? 0) = 9223372036854775807 {
 THROW 'phebs-permanent: typed source epoch exhausted';
};
LET $typed_source_epoch = IF $typed_source_changed
 THEN ($before.typed_source_epoch ?? 0) + 1
 ELSE $before.typed_source_epoch ?? 1 END;
`

type typedSourceRecord struct {
	Name           string `json:"name"`
	Commit         string `json:"indexed_commit_hash"`
	Incarnation    string `json:"typed_incarnation"`
	Epoch          int64  `json:"typed_source_epoch"`
	Deleting       bool   `json:"deleting"`
	HasIncarnation bool   `json:"has_incarnation"`
	HasEpoch       bool   `json:"has_epoch"`
}

const typedSourceProjection = "name, indexed_commit_hash, typed_incarnation, typed_source_epoch, deleting, typed_incarnation != NONE AS has_incarnation, typed_source_epoch != NONE AS has_epoch"

// GetTypedSource resolves the authoritative indexed HEAD. Legacy rows acquire
// their server-owned incarnation lazily with an exact CAS; missing or deleting
// repositories are never created or initialized. Steady-state reads one row.
func (s *Surreal) GetTypedSource(ctx context.Context, repository string) (typedindex.Source, error) {
	if reponame.Validate(repository) != nil || len(repository) > 512 {
		return typedindex.Source{}, typedindex.Invalid
	}
	for attempt := 0; attempt < maxQueueRetries; attempt++ {
		if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
			return typedindex.Source{}, err
		}
		results, err := storeQuery[[]typedSourceRecord](ctx, s.accounting, s.db,
			"SELECT "+typedSourceProjection+" FROM $rid LIMIT 1", map[string]any{"rid": repoID(repository)}, storeRead())
		if err != nil {
			return typedindex.Source{}, fmt.Errorf("read typed source: %w", err)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 || rows[0].Name != repository || rows[0].Deleting || !typedSourceHex(rows[0].Commit, 40) {
			return typedindex.Source{}, ErrNotFound
		}
		row := rows[0]
		if row.Incarnation != "" && row.Epoch > 0 {
			return typedSource(row)
		}
		if (row.HasIncarnation && !typedSourceHex(row.Incarnation, 32)) || (row.HasEpoch && row.Epoch < 1) {
			return typedindex.Source{}, typedindex.Invalid
		}
		fresh, err := newLeaseToken()
		if err != nil {
			return typedindex.Source{}, err
		}
		if err := readaccounting.Charge(ctx, readaccounting.StoreWriteAttempt, 1); err != nil {
			return typedindex.Source{}, err
		}
		_, err = storeQuery[[]typedSourceRecord](ctx, s.accounting, s.db, `
UPDATE $rid SET typed_incarnation = typed_incarnation ?? $fresh,
 typed_source_epoch = typed_source_epoch ?? 1
 WHERE deleting != true AND indexed_commit_hash = $commit
 AND (typed_incarnation ?? '') = $incarnation AND (typed_source_epoch ?? 0) = $epoch
 RETURN `+typedSourceProjection+`;`, map[string]any{
			"rid": repoID(repository), "fresh": fresh, "commit": row.Commit, "incarnation": row.Incarnation, "epoch": row.Epoch,
		}, storeWrite(1))
		if err != nil && !isRetryable(err) {
			return typedindex.Source{}, fmt.Errorf("initialize typed source: %w", err)
		}
	}
	return typedindex.Source{}, ErrConflict
}

func typedSource(row typedSourceRecord) (typedindex.Source, error) {
	if row.Deleting || reponame.Validate(row.Name) != nil || len(row.Name) > 512 || !typedSourceHex(row.Commit, 40) || !typedSourceHex(row.Incarnation, 32) || row.Epoch < 1 {
		return typedindex.Source{}, typedindex.Invalid
	}
	// Fixed canonical fields; no evidence-publication revision participates.
	raw, err := json.Marshal(struct {
		Schema      string `json:"schema"`
		Repository  string `json:"repository"`
		Incarnation string `json:"incarnation"`
		Commit      string `json:"commit"`
		Epoch       int64  `json:"epoch"`
	}{"phebs-typed-source-v1", row.Name, row.Incarnation, row.Commit, row.Epoch})
	if err != nil {
		return typedindex.Source{}, fmt.Errorf("encode typed source: %w", err)
	}
	hash := sha256.Sum256(raw)
	return typedindex.Source{Repository: row.Name, Incarnation: row.Incarnation, Commit: row.Commit, Generation: "sha256:" + hex.EncodeToString(hash[:])}, nil
}

func typedSourceHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
