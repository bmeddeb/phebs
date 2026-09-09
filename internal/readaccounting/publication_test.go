package readaccounting

import (
	"context"
	"errors"
	"testing"
)

func TestPublicationObserverScope(t *testing.T) {
	calls := 0
	ctx, err := WithPublicationObserver(t.Context(), func() error { calls++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, ledger, err := Start(ctx, Counts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ObservePublication(ctx, true); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	if counts, err := ledger.Finish(); err != nil || counts != (Counts{}) {
		t.Fatal(counts, err)
	}
	var absent context.Context
	if _, err := WithPublicationObserver(ctx, func() error { return nil }); err == nil {
		t.Fatal("nested observer accepted")
	}
	if _, err := WithPublicationObserver(absent, func() error { return nil }); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := WithPublicationObserver(t.Context(), nil); err == nil {
		t.Fatal("nil observer accepted")
	}
	if err := ObservePublication(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := ObservePublication(t.Context(), true); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	for _, mode := range []string{"before", "during", "sink", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			ctx, err := WithPublicationObserver(ctx, func() error {
				calls++
				switch mode {
				case "during":
					cancel()
				case "sink":
					return ErrEvent
				case "panic":
					panic("sink")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			wantCalls, wantErr := 1, error(ErrEvent)
			if mode == "before" || mode == "during" {
				wantErr = context.Canceled
			}
			if mode == "before" {
				cancel()
			}
			if err := ObservePublication(ctx, true); !errors.Is(err, wantErr) || calls != wantCalls {
				t.Fatal(calls, err)
			}
		})
	}
}
