//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestPublicationProductionRefusalSticky(t *testing.T) {
	for _, mode := range []string{"missing", "sink", "panic", "canceled", "during"} {
		t.Run(mode, func(t *testing.T) {
			_, client, server := paired(t, testConfig())
			setPipedTestRuntime(t, &ProductionLifetime{semanticMode: ProductionSemanticV3, client: client})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode != "missing" {
				var err error
				ctx, err = readaccounting.WithPublicationObserver(ctx, func() error {
					switch mode {
					case "panic":
						panic("observer")
					case "during":
						cancel()
						return nil
					}
					return errors.New("observer")
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "canceled" {
				cancel()
			}
			if err := ObserveProductionPublication(ctx); err == nil {
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
