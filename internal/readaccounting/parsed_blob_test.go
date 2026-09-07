package readaccounting

import (
	"context"
	"errors"
	"testing"
)

func TestParsedBlobObserverScope(t *testing.T) {
	calls := 0
	ctx, err := WithParsedBlobObserver(t.Context(), func() error { calls++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, ledger, err := Start(ctx, Counts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ObserveParsedBlob(ctx, true); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	if counts, err := ledger.Finish(); err != nil || counts != (Counts{}) {
		t.Fatal(counts, err)
	}
	if _, err := WithParsedBlobObserver(ctx, func() error { return nil }); err == nil {
		t.Fatal("nested observer accepted")
	}
	var absent context.Context // Exercise the explicit nil-context refusal.
	if _, err := WithParsedBlobObserver(absent, func() error { return nil }); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := WithParsedBlobObserver(t.Context(), nil); err == nil {
		t.Fatal("nil observer accepted")
	}
	if err := ObserveParsedBlob(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := ObserveParsedBlob(t.Context(), true); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	for _, mode := range []string{"canceled", "panic", "error", "during"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			ctx, err := WithParsedBlobObserver(ctx, func() error {
				calls++
				switch mode {
				case "panic":
					panic("observer")
				case "error":
					return ErrEvent
				case "during":
					cancel()
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			wantCalls, wantError := 1, ErrEvent
			if mode == "canceled" {
				cancel()
				wantCalls = 0
			}
			if mode == "canceled" || mode == "during" {
				wantError = context.Canceled
			}
			if err := ObserveParsedBlob(ctx, true); !errors.Is(err, wantError) || calls != wantCalls {
				t.Fatal(calls, err)
			}
		})
	}
}
