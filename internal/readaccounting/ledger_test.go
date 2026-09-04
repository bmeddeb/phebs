package readaccounting

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
)

func TestLedgerScopeAndRefusal(t *testing.T) {
	for _, test := range []struct {
		name  string
		kind  Kind
		count uint64
		want  Counts
		err   error
	}{
		{"file", ControlFileRead, 2, Counts{ControlFileReads: 2}, nil},
		{"query", StoreReadAttempt, 2, Counts{StoreReadAttempts: 2}, nil},
		{"member", MemberVisit, 2, Counts{MemberVisits: 2}, nil},
		{"write", StoreWriteAttempt, 2, Counts{StoreWriteAttempts: 2}, nil},
		{"limit", ControlFileRead, 3, Counts{ControlFileReads: 3}, ErrLimit},
		{"overflow", MemberVisit, math.MaxUint64, Counts{MemberVisits: 3}, ErrLimit},
		{"unknown", Kind(255), 1, Counts{}, ErrEvent},
		{"zero", MemberVisit, 0, Counts{}, ErrEvent},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, ledger, err := Start(t.Context(), Counts{2, 2, 2, 2})
			if err != nil {
				t.Fatal(err)
			}
			if err := Charge(ctx, test.kind, test.count); !errors.Is(err, test.err) {
				t.Fatalf("charge = %v, want %v", err, test.err)
			}
			got, err := ledger.Finish()
			if got != test.want || !errors.Is(err, test.err) {
				t.Fatalf("finish = %+v, %v; want %+v, %v", got, err, test.want, test.err)
			}
			got.ControlFileReads = 99
			wantErr := test.err
			if wantErr == nil {
				wantErr = ErrClosed
			}
			if err := Charge(ctx, ControlFileRead, 1); !errors.Is(err, wantErr) {
				t.Fatalf("late event = %v, want sticky %v", err, wantErr)
			}
			got, err = ledger.Finish()
			if got != test.want || !errors.Is(err, wantErr) {
				t.Fatalf("late finish = %+v, %v", got, err)
			}
		})
	}
	ctx, ledger, err := Start(t.Context(), Counts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Charge(ctx, ControlFileRead, 1); !errors.Is(err, ErrLimit) {
		t.Fatal("zero limit permitted work")
	}
	got, err := ledger.Finish()
	if got.ControlFileReads != 1 || !errors.Is(err, ErrLimit) {
		t.Fatalf("zero-limit sentinel = %+v, %v", got, err)
	}
	if _, _, err := Start(nil, Counts{}); !errors.Is(err, ErrScope) { //nolint:staticcheck // Exercise the rejected nil-parent boundary.
		t.Fatal("nil parent did not refuse")
	}
	if _, _, err := Start(t.Context(), Counts{MemberVisits: math.MaxUint64}); !errors.Is(err, ErrScope) {
		t.Fatal("unrepresentable sentinel did not refuse")
	}
	nested, parent, err := Start(t.Context(), Counts{MemberVisits: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Start(nested, Counts{}); !errors.Is(err, ErrScope) {
		t.Fatal("nested scope did not refuse")
	}
	if got, err := parent.Finish(); got != (Counts{}) || !errors.Is(err, ErrScope) {
		t.Fatalf("ignored nested-scope failure escaped parent: %+v, %v", got, err)
	}
	if !IsError(fmt.Errorf("wrapped: %w", ErrClosed)) || IsError(errors.New("other")) {
		t.Fatal("accounting error classification is not exact")
	}
}

func TestLedgerIndependentConcurrentScopesAndInactiveCost(t *testing.T) {
	parent := t.Context()
	const operations = 64
	left, leftLedger, err := Start(parent, Counts{MemberVisits: operations})
	if err != nil {
		t.Fatal(err)
	}
	right, rightLedger, err := Start(parent, Counts{StoreReadAttempts: operations})
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range operations {
		workers.Go(func() {
			if err := Charge(left, MemberVisit, 1); err != nil {
				t.Error(err)
			}
			if err := Charge(right, StoreReadAttempt, 1); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	for _, test := range []struct {
		ledger *Ledger
		want   Counts
	}{
		{leftLedger, Counts{MemberVisits: operations}},
		{rightLedger, Counts{StoreReadAttempts: operations}},
	} {
		if got, err := test.ledger.Finish(); got != test.want || err != nil {
			t.Fatalf("independent scope = %+v, %v; want %+v", got, err, test.want)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if err := Charge(context.Background(), MemberVisit, 1); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("inactive charge allocates: %g", allocations)
	}
}
