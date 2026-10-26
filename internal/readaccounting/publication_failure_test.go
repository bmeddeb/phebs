package readaccounting

import (
	"context"
	"errors"
	"testing"
)

func TestPublicationFailureObserverIsAdvisory(t *testing.T) {
	original := errors.New("private failure payload")
	for _, mode := range []string{"absent", "bound", "panic", "nested", "healthy"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			ctx := t.Context()
			if mode != "absent" {
				ctx = WithPublicationFailureObserver(ctx, func(stage PublicationFailureStage, err error, flags PublicationFailureFlags) {
					calls++
					if stage != PublicationFailureStoreQuery || err != original || flags != (PublicationFailureFlags{}) {
						t.Error("diagnostic input changed")
					}
					if mode == "panic" {
						panic("observer")
					}
				})
			}
			if mode == "nested" {
				ctx = WithPublicationFailureObserver(ctx, func(PublicationFailureStage, error, PublicationFailureFlags) {
					t.Fatal("nested observer replaced existing")
				})
			}
			accounted, ledger, err := Start(ctx, Counts{})
			if err != nil {
				t.Fatal(err)
			}
			failure := original
			if mode == "healthy" {
				failure = nil
			}
			NotePublicationFailure(accounted, PublicationFailureStoreQuery, failure, PublicationFailureFlags{})
			want := 1
			if mode == "absent" || mode == "healthy" {
				want = 0
			}
			if calls != want {
				t.Fatal("unexpected observation", calls)
			}
			counts, err := ledger.Finish()
			if err != nil || counts != (Counts{}) {
				t.Fatal("diagnostic changed ledger", counts, err)
			}
		})
	}
	var absent context.Context
	if WithPublicationFailureObserver(absent, func(PublicationFailureStage, error, PublicationFailureFlags) {}) != nil {
		t.Fatal("nil binding changed context")
	}
	ctx := t.Context()
	if WithPublicationFailureObserver(ctx, nil) != ctx {
		t.Fatal("nil observer changed context")
	}
	NotePublicationFailure(absent, PublicationFailureStoreQuery, original, PublicationFailureFlags{})
	if PublicationFailureStage(255).String() != "unknown" {
		t.Fatal("unbounded stage")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	bound := WithPublicationFailureObserver(canceled, func(PublicationFailureStage, error, PublicationFailureFlags) {})
	if bound.Err() != context.Canceled {
		t.Fatal("context contract changed")
	}
}
