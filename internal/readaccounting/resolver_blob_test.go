package readaccounting

import (
	"context"
	"errors"
	"testing"
)

func TestResolverBlobObserver(t *testing.T) {
	var counts, bytes uint64
	ctx, err := WithResolverBlobObserver(t.Context(), func(value uint64) error { counts++; bytes += value; return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, ledger, err := Start(ctx, Counts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []uint64{0, 23} {
		if err := ObserveResolverBlob(ctx, true, value); err != nil {
			t.Fatal(err)
		}
	}
	if value, err := ledger.Finish(); err != nil || value != (Counts{}) || counts != 2 || bytes != 23 {
		t.Fatal(value, counts, bytes, err)
	}
	var absent context.Context
	if _, err := WithResolverBlobObserver(ctx, func(uint64) error { return nil }); err == nil {
		t.Fatal("nested observer accepted")
	}
	if _, err := WithResolverBlobObserver(absent, func(uint64) error { return nil }); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := WithResolverBlobObserver(t.Context(), nil); err == nil {
		t.Fatal("nil observer accepted")
	}
	if err := ObserveResolverBlob(t.Context(), false, 1); err != nil {
		t.Fatal(err)
	}
	if err := ObserveResolverBlob(t.Context(), true, 1); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	for _, mode := range []string{"before", "during", "sink", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			ctx, err := WithResolverBlobObserver(ctx, func(value uint64) error {
				calls++
				if value != 17 {
					t.Fatal("actual returned bytes changed")
				}
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
			want := error(ErrEvent)
			if mode == "before" || mode == "during" {
				want = context.Canceled
			}
			if mode == "before" {
				cancel()
			}
			if err := ObserveResolverBlob(ctx, true, 17); !errors.Is(err, want) || calls != 1 {
				t.Fatal(calls, err)
			}
		})
	}
}
