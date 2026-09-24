package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

type archiveTailScheduleCurrentRec struct {
	Repository     string `json:"repository"`
	Stage          string `json:"stage"`
	ScheduleDigest string `json:"schedule_digest"`
	Generation     string `json:"generation"`
}

func archiveTailPointRows[T any](results *[]surrealdb.QueryResult[[]T]) ([]T, error) {
	if results == nil || len(*results) != 1 || (*results)[0].Status != "OK" ||
		(*results)[0].Error != nil || len((*results)[0].Result) > 1 {
		return nil, ErrConflict
	}
	return (*results)[0].Result, nil
}

// GetGenerationScheduleForArchiveTail is the V5-only strict current point read.
// ErrNotFound means the current pointer itself is absent. A dangling or
// mismatched pointer is an integrity error and must never look like idle work.
// A present schedule costs two SDK read attempts, each with the generation
// schedule's existing 64-attempt transient-conflict retry ceiling.
func (s *Surreal) GetGenerationScheduleForArchiveTail(
	ctx context.Context, repository, stage string,
) (*GenerationSchedule, error) {
	if strings.TrimSpace(repository) != repository || repository == "" ||
		!validGenerationToken(stage) || stage == "" {
		return nil, errors.New("get archive tail generation schedule: invalid scope")
	}
	currentResults, err := queryGenerationSchedule[[]archiveTailScheduleCurrentRec](
		ctx, s.accounting, s.db, "get_schedule",
		"SELECT repository, stage, schedule_digest, generation FROM $rid LIMIT 1",
		map[string]any{"rid": models.NewRecordID(
			"generation_schedule_current",
			strings.TrimPrefix(generationCurrentID(repository, stage), "sha256:"),
		)}, storeRead(),
	)
	if err != nil {
		return nil, fmt.Errorf("get archive tail generation schedule pointer: %w", err)
	}
	currentRows, err := archiveTailPointRows(currentResults)
	if err != nil {
		return nil, fmt.Errorf("get archive tail generation schedule pointer: %w", err)
	}
	if len(currentRows) == 0 {
		return nil, fmt.Errorf("get archive tail generation schedule pointer: %w", ErrNotFound)
	}
	if len(currentRows) != 1 || currentRows[0].Repository != repository ||
		currentRows[0].Stage != stage || !validSHA256(currentRows[0].ScheduleDigest) ||
		!validSHA256(currentRows[0].Generation) {
		return nil, fmt.Errorf("get archive tail generation schedule pointer: %w", ErrConflict)
	}
	current := currentRows[0]
	scheduleResults, err := queryGenerationSchedule[[]generationScheduleRec](
		ctx, s.accounting, s.db, "get_schedule", "SELECT * FROM $rid LIMIT 1",
		map[string]any{"rid": models.NewRecordID(
			"generation_schedule", strings.TrimPrefix(current.ScheduleDigest, "sha256:"),
		)}, storeRead(),
	)
	if err != nil {
		return nil, fmt.Errorf("get archive tail generation schedule row: %w", err)
	}
	rows, err := archiveTailPointRows(scheduleResults)
	if err != nil {
		return nil, fmt.Errorf("get archive tail generation schedule row: %w", err)
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("get archive tail generation schedule row: %w", ErrConflict)
	}
	schedule := rows[0].schedule()
	if schedule.Repository != repository || schedule.Stage != stage ||
		schedule.Digest != current.ScheduleDigest || schedule.Generation != current.Generation ||
		ValidateGenerationSchedule(schedule) != nil {
		return nil, fmt.Errorf("get archive tail generation schedule row: %w", ErrConflict)
	}
	return &schedule, nil
}
