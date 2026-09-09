//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestRelationshipProductionRefusalSticky(t *testing.T) {
	for _, mode := range []string{"missing", "sink", "panic", "canceled", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			_, client, server := paired(t, testConfig())
			setPipedTestRuntime(t, &ProductionLifetime{semanticMode: ProductionSemanticV3, client: client})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode != "missing" {
				var err error
				ctx, err = readaccounting.WithRelationshipObserver(ctx, func(readaccounting.RelationshipEvent) error {
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
			if mode == "invalid" {
				event = '?'
			}
			if err := ObserveProductionRelationship(ctx, event); err == nil {
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
