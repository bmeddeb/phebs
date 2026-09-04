package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestCallerAuthorityStoreReadAccounting(t *testing.T) {
	state := newTestStore(t)
	fixture := newCallerPublicationFixture(
		t, state, "github.com/acme/caller-read-accounting",
	)
	if err := state.PublishCallerGeneration(
		t.Context(), *fixture.job, fixture.publication,
	); err != nil {
		t.Fatal(err)
	}
	resolver, err := state.GetResolverCatalogPublication(
		t.Context(), fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := state.GetCallerGenerationPublication(
		t.Context(), fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := state.GetCallerGenerationPublicationSummary(
		t.Context(), fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("successful shared queries", func(t *testing.T) {
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{StoreReadAttempts: 6},
		)
		if err != nil {
			t.Fatal(err)
		}
		gotResolver, err := state.GetResolverCatalogPublication(ctx, fixture.repository)
		if err != nil || gotResolver.GenerationDigest != resolver.GenerationDigest {
			t.Fatalf("resolver pointer = %+v, %v", gotResolver, err)
		}
		if current, err := state.ResolverCatalogPublicationCurrent(ctx, *resolver); err != nil || !current {
			t.Fatalf("resolver current = %t, %v", current, err)
		}
		gotSummary, err := state.GetCallerGenerationPublicationSummary(ctx, fixture.repository)
		if err != nil || gotSummary.Generation != summary.Generation {
			t.Fatalf("caller summary = %+v, %v", gotSummary, err)
		}
		gotPublication, err := state.GetCallerGenerationPublication(ctx, fixture.repository)
		if err != nil || gotPublication.Generation != publication.Generation {
			t.Fatalf("caller pointer = %+v, %v", gotPublication, err)
		}
		if current, err := state.CallerGenerationPublicationSummaryCurrent(ctx, *summary); err != nil || !current {
			t.Fatalf("caller exact current = %t, %v", current, err)
		}
		if current, err := state.CallerGenerationPublicationSummaryAuthorityCurrent(ctx, *summary); err != nil || !current {
			t.Fatalf("caller authority current = %t, %v", current, err)
		}
		want := readaccounting.Counts{StoreReadAttempts: 6}
		if counts, err := ledger.Finish(); err != nil || counts != want {
			t.Fatalf("caller authority attempts = %+v, %v", counts, err)
		}
	})

	for _, test := range []struct {
		name string
		call func(context.Context) error
	}{
		{
			name: "resolver pointer",
			call: func(ctx context.Context) error {
				_, err := state.GetResolverCatalogPublication(ctx, fixture.repository)
				return err
			},
		},
		{
			name: "resolver current",
			call: func(ctx context.Context) error {
				_, err := state.ResolverCatalogPublicationCurrent(ctx, *resolver)
				return err
			},
		},
		{
			name: "caller summary",
			call: func(ctx context.Context) error {
				_, err := state.GetCallerGenerationPublicationSummary(ctx, fixture.repository)
				return err
			},
		},
		{
			name: "caller pointer",
			call: func(ctx context.Context) error {
				_, err := state.GetCallerGenerationPublication(ctx, fixture.repository)
				return err
			},
		},
		{
			name: "caller exact current",
			call: func(ctx context.Context) error {
				_, err := state.CallerGenerationPublicationSummaryCurrent(ctx, *summary)
				return err
			},
		},
		{
			name: "caller authority current",
			call: func(ctx context.Context) error {
				_, err := state.CallerGenerationPublicationSummaryAuthorityCurrent(ctx, *summary)
				return err
			},
		},
	} {
		t.Run("refusal before "+test.name, func(t *testing.T) {
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
			if err != nil {
				t.Fatal(err)
			}
			if err := test.call(ctx); !errors.Is(err, readaccounting.ErrLimit) {
				t.Fatalf("zero read-attempt budget = %v", err)
			}
			want := readaccounting.Counts{StoreReadAttempts: 1}
			if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != want {
				t.Fatalf("denied attempt = %+v, %v", counts, err)
			}
		})
	}
}
