package main

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestT422CheckpointReaderDiagnosticFirstFailure(t *testing.T) {
	base, failures := t422StaleTestControl(t)
	control := &t422CheckpointRecoveryControl{t422StaleControl: base, input: t422CheckpointRecoveryFixture()}
	ctx := readaccounting.WithTransitionReadSide(control.observeReaderFailure(t.Context()), readaccounting.TransitionReadBefore)
	flags := readaccounting.TransitionReadFailureFlags{Fence: readaccounting.TransitionReadCountOne, CurrentChecked: true, CurrentMatches: true, SQLAvailable: true, DecodedID: readaccounting.TransitionReadIDMissing}
	private := strings.Repeat("private-token-row-secret", 100)
	original := errors.Join(store.ErrGenerationLeaseLost, errors.New(private))
	readaccounting.NoteTransitionReadFailure(ctx, readaccounting.TransitionReadRecordID, original, flags)
	first := *control.readerFailure
	// A later sink cannot replace the first private failure or issue credit.
	readaccounting.NoteTransitionReadFailure(ctx, readaccounting.TransitionReadTuple, store.ErrGenerationStale, readaccounting.TransitionReadFailureFlags{})
	if *control.readerFailure != first || control.err != nil || failures.Load() != 0 {
		t.Fatal("advisory recording changed authority")
	}
	var output bytes.Buffer
	old := log.Writer()
	defer log.SetOutput(old)
	log.SetOutput(&output)
	_ = control.refuse(t.Context(), t422RecoveryRuntimeRead, original, nil)
	if control.failure.Reader != first || !first.Present || first.Side != readaccounting.TransitionReadBefore || first.Stage != readaccounting.TransitionReadRecordID || first.Cause != "store_lease" || control.ctx.Err() == nil || failures.Load() != 1 {
		t.Fatal("first refusal lost reader snapshot or ordering")
	}
	if strings.Contains(output.String(), private) || output.Len() > 2048 || !strings.Contains(output.String(), "reader_side=before reader_stage=record_id reader_cause=store_lease") {
		t.Fatal("reader log disclosed data or lost labels")
	}
	readaccounting.NoteTransitionReadFailure(ctx, readaccounting.TransitionReadShape, store.ErrGenerationStale, flags)
	if control.failure.Reader != first {
		t.Fatal("late reader changed first refusal")
	}
}
