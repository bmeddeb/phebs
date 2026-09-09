package readaccounting

import (
	"context"
	"errors"
	"testing"
)

func TestRelationshipObserver(t *testing.T) {
	for _, mode := range []string{"both", "invalid", "canceled", "during", "sink", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var events []RelationshipEvent
			ctx, err := WithRelationshipObserver(ctx, func(event RelationshipEvent) error {
				events = append(events, event)
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
			ctx, ledger, err := Start(ctx, Counts{})
			if err != nil {
				t.Fatal(err)
			}
			event := RelationshipBuild
			if mode == "invalid" {
				event = '?'
			}
			if mode == "canceled" {
				cancel()
			}
			err = ObserveRelationship(ctx, true, event)
			if mode == "both" {
				if err != nil || ObserveRelationship(ctx, true, RelationshipProjection) != nil || string(events) != "BP" {
					t.Fatal(events, err)
				}
				if counts, err := ledger.Finish(); err != nil || counts != (Counts{}) {
					t.Fatal(counts, err)
				}
			} else if err == nil || mode == "invalid" && len(events) != 0 || mode != "invalid" && len(events) != 1 {
				t.Fatal(events, err)
			}
			if _, err := WithRelationshipObserver(ctx, func(RelationshipEvent) error { return nil }); err == nil {
				t.Fatal("nested observer accepted")
			}
		})
	}
	var absent context.Context
	if _, err := WithRelationshipObserver(absent, func(RelationshipEvent) error { return nil }); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := WithRelationshipObserver(t.Context(), nil); err == nil {
		t.Fatal("nil observer accepted")
	}
	if err := ObserveRelationship(t.Context(), false, RelationshipBuild); err != nil {
		t.Fatal(err)
	}
	if err := ObserveRelationship(t.Context(), true, RelationshipBuild); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
}
