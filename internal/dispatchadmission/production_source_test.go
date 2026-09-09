//go:build darwin || linux

package dispatchadmission

import (
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestSourceProductionRefusalSticky(t *testing.T) {
	for _, mode := range []string{"missing", "sink", "panic"} {
		t.Run(mode, func(t *testing.T) {
			_, client, server := paired(t, testConfig())
			setPipedTestRuntime(t, &ProductionLifetime{semanticMode: ProductionSemanticV3, client: client})
			ctx := t.Context()
			if mode != "missing" {
				var err error
				ctx, err = readaccounting.WithSourceObserver(ctx, func() error {
					if mode == "panic" {
						panic("observer")
					}
					return errors.New("observer")
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := ObserveProductionSourceRead(ctx); err == nil {
				t.Fatal("selected observer refusal accepted")
			}
			if client.Context().Err() == nil {
				t.Fatal("selected refusal was not sticky")
			}
			if err := <-server; err == nil {
				t.Fatal("selected refusal did not close transport")
			}
		})
	}
}

func TestCacheProductionRefusalSticky(t *testing.T) {
	for _, mode := range []string{"missing", "sink", "panic", "changed-phase"} {
		t.Run(mode, func(t *testing.T) {
			_, client, server := paired(t, testConfig())
			setPipedTestRuntime(t, &ProductionLifetime{semanticMode: ProductionSemanticV3, client: client})
			ctx := t.Context()
			if mode != "missing" {
				var err error
				ctx, err = readaccounting.WithCacheObserver(ctx, func(readaccounting.CacheEvent, uint32) (uint32, error) {
					if mode == "panic" {
						panic("observer")
					}
					if mode == "changed-phase" {
						return 3, nil
					}
					return 2, errors.New("observer")
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ObserveProductionCache(ctx, readaccounting.CacheRootValidation, 2); err == nil {
				t.Fatal("selected observer refusal accepted")
			}
			if client.Context().Err() == nil || !ProductionSemanticSelected() {
				t.Fatal("selected refusal was not sticky")
			}
			if err := <-server; err == nil {
				t.Fatal("selected refusal did not close transport")
			}
		})
	}
}

func TestSourceCensusProductionRefusalSticky(t *testing.T) {
	for _, mode := range []string{"missing", "sink", "panic", "changed-phase", "invalid-prefix"} {
		t.Run(mode, func(t *testing.T) {
			_, client, server := paired(t, testConfig())
			setPipedTestRuntime(t, &ProductionLifetime{semanticMode: ProductionSemanticV3, client: client})
			ctx := t.Context()
			calls := 0
			if mode != "missing" {
				var err error
				ctx, err = readaccounting.WithSourceCensusObserver(ctx, func(readaccounting.SourceCensusEvent, uint32, uint64, uint64) (uint32, error) {
					calls++
					if mode == "panic" {
						panic("observer")
					}
					if mode == "changed-phase" {
						return 3, nil
					}
					if mode == "invalid-prefix" {
						return 2, nil
					}
					return 0, readaccounting.ErrScope
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			event := readaccounting.SourceCensusBatch
			if mode == "invalid-prefix" {
				if _, err := ObserveProductionSourceCensus(ctx, readaccounting.SourceCensusBegin, 0, 0, 0); err != nil {
					t.Fatal(err)
				}
				if _, err := ObserveProductionSourceCensus(ctx, readaccounting.SourceCensusBatch, 2, 4, 3); err != nil {
					t.Fatal(err)
				}
				event = 0
			}
			if _, err := ObserveProductionSourceCensus(ctx, event, 2, 1, 1); err == nil {
				t.Fatal("selected refusal accepted")
			}
			if mode == "invalid-prefix" && calls != 2 {
				t.Fatal("invalid meter emitted a wire event")
			}
			if client.Context().Err() == nil || !ProductionSemanticSelected() {
				t.Fatal("refusal not sticky")
			}
			if err := <-server; err == nil {
				t.Fatal("transport not closed")
			}
		})
	}
}
