package readaccounting

import (
	"context"
	"testing"
)

func TestSearchRepositoriesObservation(t *testing.T) {
	for _, name := range []string{"ordinary", "zero", "two", "missing", "duplicate", "canceled", "closed", "duplicate_selection"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx, ledger, err := Start(ctx, Counts{})
			if err != nil {
				t.Fatal(err)
			}
			if name != "ordinary" {
				ctx, err = WithSearchRepositories(ctx)
				if err != nil {
					t.Fatal(err)
				}
			}
			if name == "canceled" {
				cancel()
			}
			if name == "closed" {
				_, _ = ledger.Finish()
			}
			if name == "duplicate_selection" {
				_, _ = WithSearchRepositories(ctx)
			}
			count := uint64(2)
			if name == "zero" {
				count = 0
			}
			if name != "missing" {
				_ = ObserveSearchRepositories(ctx, count)
			}
			if name == "duplicate" {
				_ = ObserveSearchRepositories(ctx, 7)
			}
			counts, finishErr := ledger.Finish()
			wantFailure := name != "ordinary" && name != "zero" && name != "two"
			if (finishErr != nil) != wantFailure || counts != (Counts{}) {
				t.Fatalf("finish = %+v, %v", counts, finishErr)
			}
			value := ledger.SearchRepositories()
			wantPresent := name == "zero" || name == "two" || name == "duplicate"
			if (value != nil) != wantPresent {
				t.Fatalf("observation = %v", value)
			}
			if value != nil {
				if *value != count {
					t.Fatalf("count = %d", *value)
				}
				*value = 99
				if *ledger.SearchRepositories() != count {
					t.Fatal("getter exposed mutable observation")
				}
			}
		})
	}
	if _, err := WithSearchRepositories(context.Background()); err == nil {
		t.Fatal("accepted absent ledger")
	}
	if err := ObserveSearchRepositories(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
}
