package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func TestArchiveTailPointRowsRejectsMalformedReply(t *testing.T) {
	for _, test := range []struct {
		name    string
		reply   *[]surrealdb.QueryResult[[]int]
		wantErr bool
	}{
		{"nil envelope", nil, true},
		{"no statement", &[]surrealdb.QueryResult[[]int]{}, true},
		{"statement error", &[]surrealdb.QueryResult[[]int]{{Status: "ERR"}}, true},
		{"unknown status with row", &[]surrealdb.QueryResult[[]int]{{Status: "UNKNOWN", Result: []int{1}}}, true},
		{"extra statement", &[]surrealdb.QueryResult[[]int]{{Status: "OK"}, {Status: "OK"}}, true},
		{"too many rows", &[]surrealdb.QueryResult[[]int]{{Status: "OK", Result: []int{1, 2}}}, true},
		{"valid absent", &[]surrealdb.QueryResult[[]int]{{Status: "OK"}}, false},
		{"valid present", &[]surrealdb.QueryResult[[]int]{{Status: "OK", Result: []int{1}}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := archiveTailPointRows(test.reply)
			if (err != nil) != test.wantErr {
				t.Fatalf("point reply error = %v", err)
			}
		})
	}
}

func TestArchiveTailGenerationScheduleCurrentPointIsStrict(t *testing.T) {
	state := newRunnerStore(t)
	fixtureContext := t.Context()
	const repository = "example.test/archive-tail-schedule"
	stage := ServiceRelationshipV3ScheduleStage
	base := generationSpec(repository, "sha256:"+strings.Repeat("1", 64))
	base.Stage = stage
	foreignRepository := generationSpec("example.test/archive-tail-foreign", "sha256:"+strings.Repeat("2", 64))
	foreignRepository.Stage = stage
	foreignStage := generationSpec(repository, "sha256:"+strings.Repeat("3", 64))
	foreignStage.Stage = "other-stage"

	read := func(t *testing.T, wantReads uint64, wantNotFound, wantSuccess bool) {
		t.Helper()
		ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{StoreReadAttempts: 2})
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := state.GetGenerationScheduleForArchiveTail(ctx, repository, stage)
		counts, finishErr := ledger.Finish()
		if finishErr != nil || counts != (readaccounting.Counts{StoreReadAttempts: wantReads}) ||
			(readErr == nil) != wantSuccess || errors.Is(readErr, ErrNotFound) != wantNotFound ||
			wantSuccess && (got == nil || got.Repository != repository || got.Stage != stage) {
			t.Fatalf("strict current schedule = %+v, reads=%+v, errors=%v/%v", got, counts, readErr, finishErr)
		}
	}

	read(t, 1, true, false) // A missing current pointer alone means idle.
	current, err := state.EnqueueGenerationSchedule(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	otherRepository, err := state.EnqueueGenerationSchedule(t.Context(), foreignRepository)
	if err != nil {
		t.Fatal(err)
	}
	otherStage, err := state.EnqueueGenerationSchedule(t.Context(), foreignStage)
	if err != nil {
		t.Fatal(err)
	}
	read(t, 2, false, true)

	pointerID := models.NewRecordID("generation_schedule_current",
		strings.TrimPrefix(generationCurrentID(repository, stage), "sha256:"))
	scheduleID := models.NewRecordID("generation_schedule", strings.TrimPrefix(current.Digest, "sha256:"))
	setPointer := func(t *testing.T, pointerRepository, pointerStage, digest, generation string) {
		t.Helper()
		requireCandidateRawQuery(t, fixtureContext, state, `UPDATE $rid SET repository = $repository,
			stage = $stage, schedule_digest = $digest, generation = $generation RETURN NONE`, map[string]any{
			"rid": pointerID, "repository": pointerRepository, "stage": pointerStage,
			"digest": digest, "generation": generation,
		})
	}
	setRow := func(t *testing.T, rowRepository, rowStage, digest string) {
		t.Helper()
		requireCandidateRawQuery(t, fixtureContext, state, `UPDATE $rid SET repository = $repository,
			stage = $stage, digest = $digest RETURN NONE`, map[string]any{
			"rid": scheduleID, "repository": rowRepository, "stage": rowStage, "digest": digest,
		})
	}
	for _, test := range []struct {
		name              string
		reads             uint64
		pointerRepo       string
		pointerStage      string
		pointerDigest     string
		pointerGeneration string
		rowRepo           string
		rowStage          string
		rowDigest         string
	}{
		{name: "dangling pointer", reads: 2, pointerDigest: "sha256:" + strings.Repeat("f", 64)},
		{name: "foreign repository schedule", reads: 2, pointerDigest: otherRepository.Digest, pointerGeneration: otherRepository.Generation},
		{name: "foreign stage schedule", reads: 2, pointerDigest: otherStage.Digest, pointerGeneration: otherStage.Generation},
		{name: "wrong pointer repository", reads: 1, pointerRepo: "example.test/archive-tail-impostor"},
		{name: "wrong pointer stage", reads: 1, pointerStage: "other-unused-stage"},
		{name: "invalid pointer digest", reads: 1, pointerDigest: "invalid"},
		{name: "wrong resolved repository", reads: 2, rowRepo: foreignRepository.Repository},
		{name: "wrong resolved stage", reads: 2, rowStage: foreignStage.Stage},
		{name: "wrong resolved digest", reads: 2, rowDigest: "sha256:" + strings.Repeat("e", 64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			pointerRepository, pointerStage := repository, stage
			pointerDigest, pointerGeneration := current.Digest, current.Generation
			rowRepository, rowStage, rowDigest := repository, stage, current.Digest
			if test.pointerRepo != "" {
				pointerRepository = test.pointerRepo
			}
			if test.pointerStage != "" {
				pointerStage = test.pointerStage
			}
			if test.pointerDigest != "" {
				pointerDigest = test.pointerDigest
			}
			if test.pointerGeneration != "" {
				pointerGeneration = test.pointerGeneration
			}
			if test.rowRepo != "" {
				rowRepository = test.rowRepo
			}
			if test.rowStage != "" {
				rowStage = test.rowStage
			}
			if test.rowDigest != "" {
				rowDigest = test.rowDigest
			}
			setPointer(t, pointerRepository, pointerStage, pointerDigest, pointerGeneration)
			setRow(t, rowRepository, rowStage, rowDigest)
			t.Cleanup(func() {
				setPointer(t, repository, stage, current.Digest, current.Generation)
				setRow(t, repository, stage, current.Digest)
			})
			read(t, test.reads, false, false)
		})
	}
	read(t, 2, false, true)
}
