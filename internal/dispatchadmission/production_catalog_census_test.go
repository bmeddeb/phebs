package dispatchadmission

import (
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"testing"
)

func TestCatalogCensusProductionRefusalSticky(t *testing.T) {
	for _, mode := range []string{"missing", "sink", "panic", "changed-phase", "invalid-prefix"} {
		t.Run(mode, func(t *testing.T) {
			_, client, server := paired(t, testConfig())
			setPipedTestRuntime(t, &ProductionLifetime{semanticMode: ProductionSemanticV3, client: client})
			ctx := t.Context()
			calls := 0
			if mode != "missing" {
				var err error
				ctx, err = readaccounting.WithCatalogCensusObserver(ctx, func(readaccounting.CatalogCensusEvent, uint32, uint64) (uint32, error) {
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
			event := readaccounting.CatalogCensusRecords
			if mode == "invalid-prefix" {
				if _, err := ObserveProductionCatalogCensus(ctx, readaccounting.CatalogCensusBegin, 0, 0); err != nil {
					t.Fatal(err)
				}
				if _, err := ObserveProductionCatalogCensus(ctx, readaccounting.CatalogCensusRecords, 2, 4); err != nil {
					t.Fatal(err)
				}
				event = 0
			}
			if _, err := ObserveProductionCatalogCensus(ctx, event, 2, 1); err == nil {
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
