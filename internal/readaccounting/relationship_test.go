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
			ctx, err := WithRelationshipObserver(ctx, func(event RelationshipEvent, quantity uint64) error {
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
			err = ObserveRelationship(ctx, true, event, 1)
			if mode == "both" {
				if err != nil || ObserveRelationship(ctx, true, RelationshipProjection, 1) != nil || string(events) != "BP" {
					t.Fatal(events, err)
				}
				if counts, err := ledger.Finish(); err != nil || counts != (Counts{}) {
					t.Fatal(counts, err)
				}
			} else if err == nil || mode == "invalid" && len(events) != 0 || mode != "invalid" && len(events) != 1 {
				t.Fatal(events, err)
			}
			if _, err := WithRelationshipObserver(ctx, func(RelationshipEvent, uint64) error { return nil }); err == nil {
				t.Fatal("nested observer accepted")
			}
		})
	}
	var absent context.Context
	if _, err := WithRelationshipObserver(absent, func(RelationshipEvent, uint64) error { return nil }); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := WithRelationshipObserver(t.Context(), nil); err == nil {
		t.Fatal("nil observer accepted")
	}
	if err := ObserveRelationship(t.Context(), false, RelationshipBuild, 1); err != nil {
		t.Fatal(err)
	}
	if err := ObserveRelationship(t.Context(), true, RelationshipBuild, 1); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
}

func TestRelationshipObserverQuantities(t *testing.T) {
	for _, test := range []struct {
		event    RelationshipEvent
		quantity uint64
		valid    bool
	}{
		{RelationshipBuild, 0, false}, {RelationshipBuild, 1, true}, {RelationshipBuild, 2, false},
		{RelationshipProjection, 0, false}, {RelationshipProjection, 1, true}, {RelationshipProjection, 2, false},
		{RelationshipReferences, 0, true}, {RelationshipReferences, 7, true}, {RelationshipReferences, ^uint64(0), true},
		{'?', 0, false},
	} {
		calls := 0
		ctx, err := WithRelationshipObserver(t.Context(), func(event RelationshipEvent, quantity uint64) error {
			calls++
			if event != test.event || quantity != test.quantity {
				t.Fatal(event, quantity)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		err = ObserveRelationship(ctx, true, test.event, test.quantity)
		if (err == nil) != test.valid || (calls == 1) != test.valid {
			t.Fatal(test, calls, err)
		}
	}
	if err := ObserveRelationship(t.Context(), true, RelationshipReferences, 0); !errors.Is(err, ErrScope) {
		t.Fatal("zero references bypassed selected coverage", err)
	}
}
