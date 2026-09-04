package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

func TestFinalAuthorityStoreReadAccountingCountsNativeQueries(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	selected, err := fixture.store.SelectServiceRuntimeV3(
		t.Context(),
		ServiceRuntimeSelectionRequest{
			Repository: fixture.repository,
			Target:     fixture.v3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	root, err := fixture.store.ReadServiceCatalogV3Root(
		t.Context(), fixture.repository, selected.CatalogRootDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	descriptors := append(
		append([]servicecatalogv3.MemberDescriptor{}, root.ServiceMembers...),
		root.PlacementMembers...,
	)
	if len(descriptors) == 0 {
		t.Fatal("catalog fixture has no member descriptor")
	}
	wantMember, err := fixture.store.ReadServiceCatalogV3Member(
		t.Context(), descriptors[0],
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("selector get and confirm", func(t *testing.T) {
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{StoreReadAttempts: 2},
		)
		if err != nil {
			t.Fatal(err)
		}
		got, err := fixture.store.GetServiceRuntimeSelector(ctx, fixture.repository)
		if err != nil || got != selected {
			t.Fatalf("selector = %+v, %v", got, err)
		}
		if err := fixture.store.ConfirmServiceRuntimeSelector(ctx, selected); err != nil {
			t.Fatal(err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{StoreReadAttempts: 2}) {
			t.Fatalf("selector read attempts = %+v, %v", counts, err)
		}
	})

	t.Run("selected summary current branch", func(t *testing.T) {
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{StoreReadAttempts: 1},
		)
		if err != nil {
			t.Fatal(err)
		}
		got, err := fixture.store.GetServiceStateV3SummarySnapshot(
			ctx,
			fixture.repository,
			selected.StateControlRevision,
			selected.StateSummaryDigest,
		)
		if err != nil || got.ControlRevision != selected.StateControlRevision ||
			got.SummaryDigest != selected.StateSummaryDigest {
			t.Fatalf("selected summary = %+v, %v", got, err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{StoreReadAttempts: 1}) {
			t.Fatalf("current summary read attempts = %+v, %v", counts, err)
		}
	})

	t.Run("selected point current branch", func(t *testing.T) {
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{StoreReadAttempts: 1},
		)
		if err != nil {
			t.Fatal(err)
		}
		got, err := fixture.store.GetServiceStateV3PointSnapshot(
			ctx, fixture.repository, "orders",
			selected.StateControlRevision, selected.StateSummaryDigest,
		)
		if err != nil || got.ServiceKey != "orders" {
			t.Fatalf("selected point = %+v, %v", got, err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{StoreReadAttempts: 1}) {
			t.Fatalf("current point read attempts = %+v, %v", counts, err)
		}
	})

	t.Run("catalog root and member", func(t *testing.T) {
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{StoreReadAttempts: 4},
		)
		if err != nil {
			t.Fatal(err)
		}
		gotRoot, err := fixture.store.ReadServiceCatalogV3Root(
			ctx, fixture.repository, selected.CatalogRootDigest,
		)
		if err != nil || gotRoot.Digest != root.Digest {
			t.Fatalf("catalog root = %+v, %v", gotRoot, err)
		}
		gotMember, err := fixture.store.ReadServiceCatalogV3Member(ctx, descriptors[0])
		if err != nil || string(gotMember) != string(wantMember) {
			t.Fatalf("catalog member = %q, %v", gotMember, err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{StoreReadAttempts: 4}) {
			t.Fatalf("catalog read attempts = %+v, %v", counts, err)
		}
	})

	t.Run("catalog root charges before every query", func(t *testing.T) {
		for limit := uint64(0); limit < 3; limit++ {
			t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
				ctx, ledger, err := readaccounting.Start(
					t.Context(), readaccounting.Counts{StoreReadAttempts: limit},
				)
				if err != nil {
					t.Fatal(err)
				}
				_, readErr := fixture.store.ReadServiceCatalogV3Root(
					ctx, fixture.repository, selected.CatalogRootDigest,
				)
				if !errors.Is(readErr, readaccounting.ErrLimit) {
					t.Fatalf("limit %d root read = %v", limit, readErr)
				}
				want := readaccounting.Counts{StoreReadAttempts: limit + 1}
				if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != want {
					t.Fatalf("limit %d root attempts = %+v, %v", limit, counts, err)
				}
			})
		}
	})

	oldRevision := selected.StateControlRevision
	oldDigest := selected.StateSummaryDigest
	successor := serviceStateV3Generation(
		t,
		fixture.repository,
		strings.Repeat("7", 40),
		"read-accounting-successor",
		[]servicecatalog.Service{{
			Key:         "orders",
			DisplayName: "Orders read-accounting successor",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
	)
	if err := fixture.store.PublishServiceCatalogV3Candidate(t.Context(), successor); err != nil {
		t.Fatal(err)
	}
	reconcile, err := fixture.store.BeginServiceStateV3Reconcile(
		t.Context(), fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, fixture.store, reconcile)
	current, err := fixture.store.GetServiceStateV3SummaryPoint(t.Context(), fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	if current.ControlRevision == oldRevision || current.SummaryDigest == oldDigest {
		t.Fatalf("successor did not move current summary: %+v", current)
	}

	t.Run("selected summary preimage branch", func(t *testing.T) {
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{StoreReadAttempts: 2},
		)
		if err != nil {
			t.Fatal(err)
		}
		got, err := fixture.store.GetServiceStateV3SummarySnapshot(
			ctx, fixture.repository, oldRevision, oldDigest,
		)
		if err != nil || got.ControlRevision != oldRevision || got.SummaryDigest != oldDigest {
			t.Fatalf("selected preimage summary = %+v, %v", got, err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{StoreReadAttempts: 2}) {
			t.Fatalf("preimage summary read attempts = %+v, %v", counts, err)
		}
	})

	t.Run("selected point preimage branch", func(t *testing.T) {
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{StoreReadAttempts: 2},
		)
		if err != nil {
			t.Fatal(err)
		}
		got, err := fixture.store.GetServiceStateV3PointSnapshot(
			ctx, fixture.repository, "orders", oldRevision, oldDigest,
		)
		if err != nil || got.ServiceKey != "orders" {
			t.Fatalf("selected preimage point = %+v, %v", got, err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{StoreReadAttempts: 2}) {
			t.Fatalf("preimage point read attempts = %+v, %v", counts, err)
		}
	})

	t.Run("selected summary refuses before fallback query", func(t *testing.T) {
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{StoreReadAttempts: 1},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := fixture.store.GetServiceStateV3SummarySnapshot(
			ctx, fixture.repository, oldRevision, oldDigest,
		)
		if !errors.Is(readErr, readaccounting.ErrLimit) {
			t.Fatalf("preimage fallback = %v", readErr)
		}
		want := readaccounting.Counts{StoreReadAttempts: 2}
		if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != want {
			t.Fatalf("preimage fallback attempts = %+v, %v", counts, err)
		}
	})

	t.Run("selected point refuses before fallback query", func(t *testing.T) {
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{StoreReadAttempts: 1},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := fixture.store.GetServiceStateV3PointSnapshot(
			ctx, fixture.repository, "orders", oldRevision, oldDigest,
		)
		if !errors.Is(readErr, readaccounting.ErrLimit) {
			t.Fatalf("point preimage fallback = %v", readErr)
		}
		want := readaccounting.Counts{StoreReadAttempts: 2}
		if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != want {
			t.Fatalf("point preimage fallback attempts = %+v, %v", counts, err)
		}
	})
}

func TestFinalAuthorityStoreReadAccountingRefusesBeforeSDKCall(t *testing.T) {
	const repository = "example.invalid/final-authority-read-accounting"
	digest := "sha256:" + strings.Repeat("a", 64)
	state := &Surreal{}
	for _, test := range []struct {
		name string
		call func(context.Context) error
	}{
		{
			name: "runtime selector",
			call: func(ctx context.Context) error {
				_, err := state.GetServiceRuntimeSelector(ctx, repository)
				return err
			},
		},
		{
			name: "selected summary",
			call: func(ctx context.Context) error {
				_, err := state.GetServiceStateV3SummarySnapshot(ctx, repository, 1, digest)
				return err
			},
		},
		{
			name: "selected point",
			call: func(ctx context.Context) error {
				_, err := state.GetServiceStateV3PointSnapshot(
					ctx, repository, "orders", 1, digest,
				)
				return err
			},
		},
		{
			name: "catalog root",
			call: func(ctx context.Context) error {
				_, err := state.ReadServiceCatalogV3Root(ctx, repository, digest)
				return err
			},
		},
		{
			name: "catalog member",
			call: func(ctx context.Context) error {
				_, err := state.ReadServiceCatalogV3Member(ctx, servicecatalogv3.MemberDescriptor{
					Kind: "service", Ordinal: 0, Digest: digest, ContentBytes: 1,
				})
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
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
