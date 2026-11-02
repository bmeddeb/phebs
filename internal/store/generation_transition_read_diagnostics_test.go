//go:build darwin || linux

package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func TestGenerationTransitionDiagnosticQueryAndDecodedID(t *testing.T) {
	// Pin the exact original statement, including whitespace. The selected
	// sibling changes only its RETURN projection, not a predicate or operation.
	const original = `
BEGIN;
LET $chunk = (SELECT * FROM generation_schedule_chunk
	WHERE identity = $identity LIMIT 1)[0];
LET $current_digest = (SELECT VALUE schedule_digest FROM $current LIMIT 1)[0];
RETURN [{ chunk: $chunk, current_schedule_digest: $current_digest }];
COMMIT;`
	if generationTransitionReadQuery(t.Context()) != original {
		t.Fatal("ordinary SQL changed")
	}
	prefix, suffix, ok := strings.Cut(original, "RETURN ")
	if !ok {
		t.Fatal("missing return")
	}
	_, tail, ok := strings.Cut(suffix, ";\n")
	if !ok {
		t.Fatal("missing return terminator")
	}
	observed := readaccounting.WithTransitionReadFailureObserver(t.Context(), func(readaccounting.TransitionReadSide, readaccounting.TransitionReadStage, error, readaccounting.TransitionReadFailureFlags) {
	})
	selected := generationTransitionReadQuery(observed)
	if !strings.HasPrefix(selected, prefix+"RETURN ") || !strings.HasSuffix(selected, ";\n"+tail) || strings.Count(selected, "SELECT ") != strings.Count(original, "SELECT ") || !strings.Contains(selected, "type::is_record($chunk.id)") {
		t.Fatal("diagnostic query changed more than projection")
	}
	for _, test := range []struct {
		record generationChunkRec
		want   readaccounting.TransitionReadIDKind
	}{
		{generationChunkRec{}, readaccounting.TransitionReadIDMissing},
		{generationChunkRec{RecID: &models.RecordID{Table: "other", ID: "one"}}, readaccounting.TransitionReadIDWrongTable},
		{generationChunkRec{RecID: &models.RecordID{Table: "generation_schedule_chunk", ID: int64(1)}}, readaccounting.TransitionReadIDWrongType},
		{generationChunkRec{RecID: &models.RecordID{Table: "generation_schedule_chunk", ID: "one"}}, readaccounting.TransitionReadIDValid},
	} {
		if generationTransitionDecodedID(test.record) != test.want {
			t.Fatal("record ID kind differs")
		}
	}
}

func TestGenerationTransitionReadFailureRealStoreParity(t *testing.T) {
	state := newRunnerStore(t)
	ctx := t.Context()
	spec := generationSpec("example.invalid/reader-diagnostics", "sha256:"+strings.Repeat("3", 64))
	spec.Stage, spec.ResourceClass, spec.TotalItems, spec.ChunkItems = "extraction-partitions", GenerationResourceExtraction, 1, 1
	schedule, err := state.EnqueueGenerationSchedule(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = state.ExpandGenerationSchedule(ctx, spec.Repository, spec.Stage, spec.Generation); err != nil {
		t.Fatal(err)
	}
	chunk, err := state.ClaimGenerationChunk(ctx, spec.ResourceClass, "reader-diag")
	if err != nil || chunk == nil {
		t.Fatal("claim refused", err)
	}
	request := GenerationStaleLeaseTransitionRequest{Point: GenerationStaleLeaseTransitionCheckpointHit, Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation, ResourceClass: spec.ResourceClass, ScheduleDigest: schedule.Digest, ChunkIdentity: chunk.Identity, Length: 1, Attempt: 0}
	type saved struct {
		side  readaccounting.TransitionReadSide
		stage readaccounting.TransitionReadStage
		flags readaccounting.TransitionReadFailureFlags
	}
	read := func(target GenerationStaleLeaseTransitionRequest, diagnostic bool) (GenerationStaleLeaseTransition, []saved, readaccounting.Counts, error) {
		t.Helper()
		observed := ctx
		records := []saved{}
		if diagnostic {
			observed = readaccounting.WithTransitionReadFailureObserver(observed, func(side readaccounting.TransitionReadSide, stage readaccounting.TransitionReadStage, err error, flags readaccounting.TransitionReadFailureFlags) {
				records = append(records, saved{side, stage, flags})
			})
			observed = readaccounting.WithTransitionReadSide(observed, readaccounting.TransitionReadBefore)
		}
		observed, ledger, err := readaccounting.Start(observed, readaccounting.Counts{StoreReadAttempts: 2})
		if err != nil {
			t.Fatal(err)
		}
		value, failure := state.ReadGenerationStaleLeaseTransition(observed, target)
		counts, finish := ledger.Finish()
		if finish != nil {
			t.Fatal(finish)
		}
		return value, records, counts, failure
	}
	baseline, records, counts, err := read(request, false)
	if err != nil || len(records) != 0 || counts.StoreReadAttempts != 2 {
		t.Fatal("ordinary healthy read", err, counts)
	}
	value, records, observedCounts, err := read(request, true)
	if err != nil || value != baseline || len(records) != 0 || observedCounts != counts {
		t.Fatal("diagnostic healthy parity", err)
	}
	// A tuple corruption still fails at the original tuple predicate. Diagnostic
	// fields are advisory and report record-ID presence independently.
	mutate := func(sql string, vars map[string]any) {
		t.Helper()
		if _, err := surrealdb.Query[any](ctx, state.db, sql, vars); err != nil {
			t.Fatal(err)
		}
	}
	vars := map[string]any{"rid": generationChunkRecordID(*chunk)}
	mutate("UPDATE $rid SET offset = 1 RETURN NONE", vars)
	_, _, ordinaryCounts, ordinary := read(request, false)
	value, records, observedCounts, failure := read(request, true)
	if !errors.Is(ordinary, ErrGenerationLeaseLost) || failure.Error() != ordinary.Error() || value != (GenerationStaleLeaseTransition{}) || observedCounts != ordinaryCounts || len(records) != 1 || records[0].stage != readaccounting.TransitionReadTuple || !records[0].flags.SQLAvailable || !records[0].flags.SQLChunkPresent || !records[0].flags.SQLRecordIDPresent || !records[0].flags.SQLRecordIDIsRecord || records[0].flags.DecodedID != readaccounting.TransitionReadIDValid {
		t.Fatal("tuple refusal parity")
	}
	mutate("UPDATE $rid SET offset = 0 RETURN NONE", vars)
	// Missing row reproduces V20's *signature*, not its unknown native cause.
	mutate("DELETE $rid RETURN NONE", vars)
	_, _, ordinaryCounts, ordinary = read(request, false)
	value, records, observedCounts, failure = read(request, true)
	if !errors.Is(ordinary, ErrGenerationLeaseLost) || !strings.Contains(ordinary.Error(), "generation chunk has no record id") || failure.Error() != ordinary.Error() || value != (GenerationStaleLeaseTransition{}) || observedCounts != ordinaryCounts || len(records) != 1 {
		t.Fatal("missing row refusal parity")
	}
	f := records[0]
	if f.side != readaccounting.TransitionReadBefore || f.stage != readaccounting.TransitionReadRecordID || !f.flags.SQLAvailable || f.flags.SQLChunkPresent || f.flags.SQLRecordIDPresent || f.flags.SQLRecordIDIsRecord || f.flags.DecodedID != readaccounting.TransitionReadIDMissing || !f.flags.CurrentChecked || !f.flags.CurrentMatches || f.flags.Fence != readaccounting.TransitionReadCountOne {
		t.Fatalf("missing row diagnostic differs: %+v", f.flags)
	}
	// Ordinary-query counterfactual: removing only diagnostic projection must
	// retain the exact refusal, but the new SQL presence evidence is unavailable.
	savedSQL := generationTransitionDiagnosticSQL
	generationTransitionDiagnosticSQL = generationTransitionReadSQL
	t.Cleanup(func() { generationTransitionDiagnosticSQL = savedSQL })
	_, records, _, failure = read(request, true)
	if failure.Error() != ordinary.Error() || len(records) != 1 || records[0].flags.SQLAvailable {
		t.Fatal("original-query counterfactual differs")
	}
}
