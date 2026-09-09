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
