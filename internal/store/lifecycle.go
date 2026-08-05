package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const (
	MaxLifecycleCursorKeyBytes = 128
	MaxLifecycleCursorBytes    = 512
)

var ErrLifecycleCursorConflict = errors.New("lifecycle cursor changed")

type lifecycleCursorRecord struct {
	Key      string `json:"key"`
	Cursor   string `json:"cursor"`
	Revision uint64 `json:"revision"`
}

func (s *Surreal) GetLifecycleCursor(
	ctx context.Context, key string,
) (string, uint64, error) {
	if err := validateLifecycleCursor(key, ""); err != nil {
		return "", 0, err
	}
	results, err := surrealdb.Query[[]lifecycleCursorRecord](ctx, s.db,
		"RETURN SELECT key, cursor, revision FROM $rid LIMIT 1", map[string]any{
			"rid": lifecycleCursorRecordID(key),
		})
	if err != nil {
		return "", 0, fmt.Errorf("get lifecycle cursor: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) == 0 {
			continue
		}
		row := result.Result[0]
		if row.Key != key || row.Revision == 0 ||
			validateLifecycleCursor(row.Key, row.Cursor) != nil {
			return "", 0, errors.New("get lifecycle cursor: stored row is malformed")
		}
		return row.Cursor, row.Revision, nil
	}
	return "", 0, nil
}

func (s *Surreal) CompareAndSwapLifecycleCursor(
	ctx context.Context, key string, expected uint64, cursor string,
) error {
	if err := validateLifecycleCursor(key, cursor); err != nil {
		return err
	}
	results, err := surrealdb.Query[[]bool](ctx, s.db, `
BEGIN;
LET $existing = (SELECT revision FROM $rid LIMIT 1)[0].revision;
LET $matches = IF $expected = 0 THEN $existing = NONE ELSE $existing = $expected END;
IF $matches {
	UPSERT $rid SET key = $key, cursor = $cursor,
		revision = $expected + 1, updated_at = time::now() RETURN NONE;
};
RETURN [$matches];
COMMIT;`, map[string]any{
		"rid": lifecycleCursorRecordID(key), "key": key,
		"cursor": cursor, "expected": expected,
	})
	if err != nil {
		return fmt.Errorf("compare and swap lifecycle cursor: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) > 0 {
			if result.Result[0] {
				return nil
			}
			return ErrLifecycleCursorConflict
		}
	}
	return errors.New("compare and swap lifecycle cursor: result is absent")
}

func validateLifecycleCursor(key, cursor string) error {
	if key == "" || strings.TrimSpace(key) != key || !utf8.ValidString(key) ||
		len(key) > MaxLifecycleCursorKeyBytes || strings.ContainsAny(key, "\x00\r\n") {
		return errors.New("lifecycle cursor key is invalid")
	}
	if !utf8.ValidString(cursor) || len(cursor) > MaxLifecycleCursorBytes ||
		strings.ContainsAny(cursor, "\x00\r\n") {
		return errors.New("lifecycle cursor value is invalid")
	}
	return nil
}

func lifecycleCursorRecordID(key string) models.RecordID {
	digest := sha256.Sum256([]byte("phebs-lifecycle-cursor-v1\x00" + key))
	return models.NewRecordID("lifecycle_cursor", hex.EncodeToString(digest[:]))
}
