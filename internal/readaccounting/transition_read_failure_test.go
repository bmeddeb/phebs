package readaccounting

import (
	"context"
	"errors"
	"testing"
)

func TestTransitionReadFailureObserverIsAdvisory(t *testing.T) {
	original := errors.New("private payload must not become a diagnostic field")
	for _, mode := range []string{"absent", "bound", "panic", "nested", "success", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if WithTransitionReadSide(ctx, TransitionReadBefore) != ctx {
				t.Fatal("ordinary side binding allocated a context")
			}
			if mode != "absent" {
				ctx = WithTransitionReadFailureObserver(ctx, func(side TransitionReadSide, stage TransitionReadStage, err error, flags TransitionReadFailureFlags) {
					calls++
					if side != TransitionReadAfter || stage != TransitionReadRecordID || err != original || flags.DecodedID != TransitionReadIDMissing {
						t.Error("diagnostic input changed")
					}
					if mode == "panic" {
						panic("sink")
					}
				})
			}
			if mode == "nested" {
				ctx = WithTransitionReadFailureObserver(ctx, func(TransitionReadSide, TransitionReadStage, error, TransitionReadFailureFlags) {
					t.Fatal("nested sink replaced first")
				})
			}
			if mode == "canceled" {
				cancel()
			}
			ctx = WithTransitionReadSide(ctx, TransitionReadAfter)
			failure := original
			if mode == "success" {
				failure = nil
			}
			NoteTransitionReadFailure(ctx, TransitionReadRecordID, failure, TransitionReadFailureFlags{DecodedID: TransitionReadIDMissing})
			want := 1
			if mode == "absent" || mode == "success" {
				want = 0
			}
			if calls != want {
				t.Fatal("unexpected sink calls", calls)
			}
		})
	}
	ctx, ledger, err := Start(t.Context(), Counts{})
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithTransitionReadFailureObserver(ctx, func(TransitionReadSide, TransitionReadStage, error, TransitionReadFailureFlags) {})
	NoteTransitionReadFailure(ctx, TransitionReadRecordID, original, TransitionReadFailureFlags{})
	if counts, err := ledger.Finish(); err != nil || counts != (Counts{}) {
		t.Fatal("observer gained accounting", counts, err)
	}
	var absent context.Context
	if WithTransitionReadFailureObserver(absent, func(TransitionReadSide, TransitionReadStage, error, TransitionReadFailureFlags) {}) != nil || WithTransitionReadSide(absent, TransitionReadBefore) != nil || HasTransitionReadFailureObserver(absent) {
		t.Fatal("nil context gained binding")
	}
	NoteTransitionReadFailure(absent, TransitionReadInput, original, TransitionReadFailureFlags{})
	if WithTransitionReadFailureObserver(ctx, nil) != ctx {
		t.Fatal("nil observer replaced context")
	}
	for _, value := range []string{TransitionReadSide(255).String(), TransitionReadStage(255).String(), TransitionReadCardinality(255).String(), TransitionReadIDKind(255).String()} {
		if value != "unknown" {
			t.Fatal("open diagnostic label")
		}
	}
}
