package readaccounting

import (
	"context"
	"errors"
	"testing"
)

func TestSourceObserverScope(t *testing.T) {
	calls := 0
	ctx, err := WithSourceObserver(t.Context(), func() error { calls++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, ledger, err := Start(ctx, Counts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ObserveSource(ctx, true); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	if counts, err := ledger.Finish(); err != nil || counts != (Counts{}) {
		t.Fatal(counts, err)
	}
	if _, err := WithSourceObserver(ctx, func() error { return nil }); err == nil {
		t.Fatal("nested observer accepted")
	}
	if err := ObserveSource(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := ObserveSource(t.Context(), true); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := ObserveSource(canceled, true); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(calls, err)
	}
	panicCtx, _ := WithSourceObserver(t.Context(), func() error { panic("test") })
	if err := ObserveSource(panicCtx, true); !errors.Is(err, ErrEvent) {
		t.Fatal(err)
	}
	during, stop := context.WithCancel(t.Context())
	defer stop()
	during, err = WithSourceObserver(during, func() error { stop(); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := ObserveSource(during, true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation during synchronous report was lost", err)
	}
}
