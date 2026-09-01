package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func TestServiceStateMaximumCurrentPlanIsBounded(t *testing.T) {
	const repository = "example.com/acme/maximum-state"
	digest := "sha256:" + strings.Repeat("a", 64)
	projections := make([]servicecatalog.ServiceProjection, servicecatalog.MaxServices)
	for index := range projections {
		key := fmt.Sprintf("service-%04d", index)
		projections[index] = servicecatalog.ServiceProjection{
			Repository: repository,
			Service: servicecatalog.Service{
				Key: key, DisplayName: key,
				Disposition: servicecatalog.DispositionAccepted,
				Origin:      servicecatalog.OriginBase,
			},
			SourceGeneration: digest, CatalogGeneration: digest,
			GenerationDigest: digest,
		}
	}
	publication := servicecatalog.Publication{
		Repository: repository, GenerationDigest: digest, ControlRevision: 1,
	}
	now := time.Date(2026, 8, 5, 15, 0, 0, 0, time.UTC)
	updates, states, tombstones, err := planServiceStateTransition(
		publication, projections, map[string]servicecatalog.ServiceState{}, nil, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != servicecatalog.MaxServices ||
		len(states) != servicecatalog.MaxServices || tombstones != 0 {
		t.Fatalf(
			"maximum plan = updates %d, states %d, tombstones %d",
			len(updates), len(states), tombstones,
		)
	}
	summary, err := buildServiceStateSummary(
		publication, projections, states, tombstones, nil, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CatalogServiceCount != servicecatalog.MaxServices ||
		summary.LiveServiceCount != servicecatalog.MaxServices ||
		summary.UnavailableCount != servicecatalog.MaxServices ||
		summary.ControlRevision != 1 {
		t.Fatalf("maximum summary = %+v", summary)
	}
}

func TestGetServiceStateSummaryDistinguishesAbsentAndMalformedCatalogPointer(t *testing.T) {
	state, err := OpenLocalMemory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })

	const repository = "example.com/acme/catalog-pointer"
	if _, err := state.GetServiceStateSummary(t.Context(), repository); !errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrConflict) {
		t.Fatalf("absent catalog pointer = %v, want ErrNotFound", err)
	}

	results, err := surrealdb.Query[any](t.Context(), state.db, `CREATE $rid CONTENT {
		repository: $repository,
		generation_digest: 'malformed',
		control_revision: 1,
		published_at: time::now()
	}`, map[string]any{
		"rid": serviceCatalogCurrentID(repository), "repository": repository,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if result.Error != nil {
			t.Fatal(result.Error.Message)
		}
	}
	if _, err := state.GetServiceStateSummary(t.Context(), repository); !errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed catalog pointer = %v, want ErrConflict", err)
	}
}
