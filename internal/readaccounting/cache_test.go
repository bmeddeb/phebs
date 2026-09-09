package readaccounting

import (
	"context"
	"errors"
	"testing"
)

func TestCacheObserverScopeAndReturnedCancellation(t *testing.T) {
	for _, mode := range []string{"hit", "load", "validation", "canceled-hit", "canceled-validation", "changed-phase", "invalid-kind", "invalid-phase", "sink", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			ctx, err := WithCacheObserver(ctx, func(CacheEvent, uint32) (uint32, error) {
				calls++
				if mode == "panic" {
					panic("observer")
				}
				if mode == "sink" {
					return 2, ErrEvent
				}
				if mode == "changed-phase" {
					return 3, nil
				}
				return 2, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, ledger, err := Start(ctx, Counts{})
			if err != nil {
				t.Fatal(err)
			}
			event, phase, wantCalls := CacheHit, uint32(0), 1
			switch mode {
			case "load":
				event = CacheRootLoad
			case "validation", "changed-phase":
				event, phase = CacheRootValidation, 2
			case "canceled-hit":
				cancel()
				wantCalls = 0
			case "canceled-validation":
				event, phase = CacheMemberValidation, 2
				cancel()
			case "invalid-kind":
				event, wantCalls = CacheEvent('x'), 0
			case "invalid-phase":
				event, wantCalls = CacheRootValidation, 0
			}
			observed, err := ObserveCache(ctx, true, event, phase)
			wantSuccess := mode == "hit" || mode == "load" || mode == "validation"
			if (err == nil) != wantSuccess || calls != wantCalls || wantSuccess && observed != 2 {
				t.Fatal(observed, calls, err)
			}
			if mode == "canceled-validation" && (!errors.Is(err, context.Canceled) || observed != 2) {
				t.Fatal("returned canceled result lost native phase", observed, err)
			}
			if counts, err := ledger.Finish(); err != nil || counts != (Counts{}) {
				t.Fatal("cache observer changed scoped read units", counts, err)
			}
			if _, err := WithCacheObserver(ctx, func(CacheEvent, uint32) (uint32, error) { return 2, nil }); err == nil {
				t.Fatal("nested cache observer admitted")
			}
		})
	}
	var absent context.Context // Explicit nil-context boundary, not caller usage.
	if _, err := WithCacheObserver(absent, func(CacheEvent, uint32) (uint32, error) { return 2, nil }); err == nil {
		t.Fatal("nil context admitted")
	}
	if _, err := WithCacheObserver(t.Context(), nil); err == nil {
		t.Fatal("nil sink admitted")
	}
	if phase, err := ObserveCache(absent, false, CacheHit, 0); phase != 0 || err != nil {
		t.Fatal("ordinary absent observer changed", phase, err)
	}
	if _, err := ObserveCache(t.Context(), true, CacheHit, 0); !errors.Is(err, ErrScope) {
		t.Fatal("missing selected observer admitted", err)
	}
}
