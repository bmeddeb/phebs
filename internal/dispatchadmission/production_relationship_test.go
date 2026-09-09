//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestRelationshipProductionRefusalSticky(t *testing.T) {
	for _, mode := range []string{"missing", "sink", "panic", "canceled", "invalid", "quantity", "zero_missing"} {
		t.Run(mode, func(t *testing.T) {
			_, client, server := paired(t, testConfig())
			setPipedTestRuntime(t, &ProductionLifetime{semanticMode: ProductionSemanticV3, client: client})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode != "missing" && mode != "zero_missing" {
				var err error
				ctx, err = readaccounting.WithRelationshipObserver(ctx, func(readaccounting.RelationshipEvent, uint64) error {
					if mode == "panic" {
						panic("observer")
					}
					if mode == "sink" {
						return readaccounting.ErrEvent
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "canceled" {
				cancel()
			}
			event := readaccounting.RelationshipBuild
			quantity := uint64(1)
			if mode == "invalid" {
				event = '?'
			}
			if mode == "quantity" {
				quantity = 2
			}
			if mode == "zero_missing" {
				event, quantity = readaccounting.RelationshipReferences, 0
			}
			if err := ObserveProductionRelationship(ctx, event, quantity); err == nil {
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
