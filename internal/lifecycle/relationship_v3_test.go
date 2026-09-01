package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/relationshippublication"
)

func TestRelationshipGenerationOwnerV3IsDarkAndExactWhenEmpty(t *testing.T) {
	acquired := 0
	released := 0
	owner := RelationshipGenerationOwnerV3{
		DataDir: t.TempDir(),
		Pins:    &relationshippublication.CacheV3{},
		AcquireExclusive: func(context.Context) (func(), error) {
			acquired++
			return func() { released++ }, nil
		},
	}
	if owner.Name() != RelationshipV3Owner || owner.Name() != "relationship-v3-namespaces" {
		t.Fatalf("v3 owner name = %q", owner.Name())
	}
	result := owner.Sweep(t.Context(), time.Now().UTC(), "", DefaultLimits())
	if result.Err != nil || result.Completeness != Exact || result.More ||
		result.Scanned != 0 || result.Deleted != 0 || acquired != 1 || released != 1 {
		t.Fatalf(
			"empty dark v3 lifecycle = %+v, acquired %d released %d",
			result, acquired, released,
		)
	}
}

func TestRelationshipGenerationOwnerV3RefusesIncompleteInputs(t *testing.T) {
	result := (RelationshipGenerationOwnerV3{}).Sweep(
		t.Context(), time.Now().UTC(), "retained", DefaultLimits(),
	)
	if result.Err == nil || result.Completeness != Unavailable || result.Cursor != "retained" {
		t.Fatalf("incomplete v3 owner = %+v", result)
	}
}
