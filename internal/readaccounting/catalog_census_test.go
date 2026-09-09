package readaccounting

import (
	"context"
	"errors"
	"testing"
)

func TestCatalogCensusObserver(t *testing.T) {
	for _, mode := range []string{"ordinary", "missing", "begin", "batch", "end", "canceled_begin", "canceled_batch", "phase", "invalid", "sink", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			if mode != "ordinary" && mode != "missing" {
				var err error
				ctx, err = WithCatalogCensusObserver(ctx, func(CatalogCensusEvent, uint32, uint64) (uint32, error) {
					calls++
					if mode == "panic" {
						panic("private")
					}
					if mode == "sink" {
						return 0, ErrScope
					}
					if mode == "phase" {
						return 3, nil
					}
					return 2, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := WithCatalogCensusObserver(ctx, func(CatalogCensusEvent, uint32, uint64) (uint32, error) { return 2, nil }); err == nil {
					t.Fatal("nested observer")
				}
			}
			event, phase, records := CatalogCensusRecords, uint32(2), uint64(9)
			if mode == "begin" || mode == "canceled_begin" {
				event, phase, records = CatalogCensusBegin, 0, 0
			}
			if mode == "end" {
				event, records = CatalogCensusEnd, 0
			}
			if mode == "invalid" {
				records = 0
			}
			if mode == "canceled_begin" || mode == "canceled_batch" {
				cancel()
			}
			_, err := ObserveCatalogCensus(ctx, mode != "ordinary", event, phase, records)
			wantOK := mode == "ordinary" || mode == "begin" || mode == "batch" || mode == "end"
			wantCalls := 1
			if mode == "ordinary" || mode == "missing" || mode == "invalid" || mode == "canceled_begin" {
				wantCalls = 0
			}
			if (err == nil) != wantOK || calls != wantCalls {
				t.Fatal(calls, err)
			}
			if mode == "canceled_batch" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
