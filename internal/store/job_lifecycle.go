package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const MaxJobLifecycleScanRows = 64

type JobLifecycleRow struct {
	ID         string
	Status     JobStatus
	FinishedAt *time.Time
}

type JobLifecyclePage struct {
	Rows []JobLifecycleRow
	Next string
}

type jobLifecycleRec struct {
	Status     JobStatus        `json:"status"`
	FinishedAt *time.Time       `json:"finished_at"`
	RecID      *models.RecordID `json:"id"`
}

type jobLifecycleDelete struct {
	Deleted int `json:"deleted"`
}

func (s *Surreal) ScanJobLifecycle(
	ctx context.Context, kind JobKind, after string, limit int,
) (JobLifecyclePage, error) {
	if !validJobKind(kind) || limit < 1 || limit > MaxJobLifecycleScanRows ||
		(after != "" && !validJobCursorID(after)) {
		return JobLifecyclePage{}, errors.New("scan job lifecycle: scope is invalid")
	}
	variables := map[string]any{"table": string(kind), "limit": limit + 1}
	statement := `RETURN SELECT id, status, finished_at FROM type::table($table)
		ORDER BY id LIMIT $limit;`
	if after != "" {
		cursor := models.NewRecordID(string(kind), after)
		statement = `RETURN SELECT id, status, finished_at FROM ` + cursor.String() + `>..
			ORDER BY id LIMIT $limit;`
	}
	results, err := surrealdb.Query[[]jobLifecycleRec](ctx, s.db, statement, variables)
	if err != nil {
		return JobLifecyclePage{}, fmt.Errorf("scan job lifecycle: %w", err)
	}
	var rows []jobLifecycleRec
	for _, result := range *results {
		rows = append(rows, result.Result...)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	page := JobLifecyclePage{Rows: make([]JobLifecycleRow, 0, len(rows))}
	for _, row := range rows {
		id, idErr := jobLifecycleRecordID(kind, row.RecID)
		if idErr != nil || !validJobStatus(row.Status, false) ||
			(row.Status == StatusDone || row.Status == StatusFailed ||
				row.Status == StatusCanceled) && row.FinishedAt == nil {
			return JobLifecyclePage{}, errors.New("scan job lifecycle: stored row is malformed")
		}
		page.Rows = append(page.Rows, JobLifecycleRow{
			ID: id, Status: row.Status, FinishedAt: row.FinishedAt,
		})
	}
	if more && len(page.Rows) > 0 {
		page.Next = page.Rows[len(page.Rows)-1].ID
	}
	return page, nil
}

func (s *Surreal) DeleteOldestTerminalJobs(
	ctx context.Context, kind JobKind, before *time.Time, limit int,
) (int, error) {
	if !validJobKind(kind) || limit < 1 || limit > 16 {
		return 0, errors.New("delete terminal jobs: scope is invalid")
	}
	results, err := surrealdb.Query[[]jobLifecycleDelete](ctx, s.db, `
BEGIN;
LET $ids = SELECT VALUE id FROM type::table($table)
	WHERE status IN ['done', 'failed', 'canceled'] AND finished_at != NONE
		AND ($before = NONE OR finished_at < $before)
	ORDER BY finished_at, id LIMIT $limit;
LET $deleted = DELETE $ids WHERE status IN ['done', 'failed', 'canceled']
	AND finished_at != NONE AND ($before = NONE OR finished_at < $before)
	RETURN BEFORE;
RETURN [{ deleted: array::len($deleted) }];
COMMIT;`, map[string]any{
		"table": string(kind), "before": before, "limit": limit,
	})
	if err != nil {
		return 0, fmt.Errorf("delete terminal jobs: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) > 0 {
			deleted := result.Result[0].Deleted
			if deleted < 0 || deleted > limit {
				return 0, errors.New("delete terminal jobs: invalid deletion count")
			}
			return deleted, nil
		}
	}
	return 0, errors.New("delete terminal jobs: result is absent")
}

func jobLifecycleRecordID(kind JobKind, record *models.RecordID) (string, error) {
	if record == nil || record.Table != string(kind) {
		return "", errors.New("job lifecycle record id is absent")
	}
	id, ok := record.ID.(string)
	if !ok || !validJobCursorID(id) {
		return "", errors.New("job lifecycle record id is invalid")
	}
	return id, nil
}
