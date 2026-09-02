package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

type catalogV3StoreStub struct {
	sweep store.ServiceCatalogV3LifecycleSweep
	err   error
}

func (stub catalogV3StoreStub) SweepServiceCatalogV3Lifecycle(
	context.Context, string, int, int, int,
) (store.ServiceCatalogV3LifecycleSweep, error) {
	return stub.sweep, stub.err
}

type catalogV3SequenceStore struct {
	sweeps []store.ServiceCatalogV3LifecycleSweep
	calls  int
}

func (stub *catalogV3SequenceStore) SweepServiceCatalogV3Lifecycle(
	context.Context, string, int, int, int,
) (store.ServiceCatalogV3LifecycleSweep, error) {
	index := stub.calls
	stub.calls++
	if index >= len(stub.sweeps) {
		return store.ServiceCatalogV3LifecycleSweep{}, nil
	}
	return stub.sweeps[index], nil
}

func TestCatalogV3OwnerReportsBytesAndIsolatesMalformedCursor(t *testing.T) {
	digest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	owner := CatalogV3GenerationOwner{
		Store: catalogV3StoreStub{sweep: store.ServiceCatalogV3LifecycleSweep{
			Cursor: digest, Scanned: 1, Deleted: 1,
			RetiredLogicalBytes: 10, DeletedRootBytes: 20,
			DeletedMemberBytes: 30, More: true,
		}},
		Acquire: func(context.Context) (func(), error) { return func() {}, nil },
	}
	result := owner.Sweep(t.Context(), time.Time{}, "", DefaultLimits())
	if result.Err != nil || result.Cursor != digest || result.Deleted != 1 ||
		result.Completeness != LowerBound || result.LogicalBytes != 10 ||
		result.RootBytes != 20 || result.MemberBytes != 30 {
		t.Fatalf("owner result = %+v", result)
	}
}

func TestCatalogV3OwnerMalformedAdvanceAndStatusBytes(t *testing.T) {
	digest := "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	wantErr := errors.New("malformed")
	owner := CatalogV3GenerationOwner{
		Store: catalogV3StoreStub{
			sweep: store.ServiceCatalogV3LifecycleSweep{
				Cursor: digest, Scanned: 1, RetiredLogicalBytes: 10,
				DeletedRootBytes: 20, DeletedMemberBytes: 30,
			},
			err: wantErr,
		},
		Acquire: func(context.Context) (func(), error) { return func() {}, nil },
	}
	result := owner.Sweep(t.Context(), time.Time{}, "", DefaultLimits())
	if !errors.Is(result.Err, wantErr) || !result.AdvanceOnError ||
		result.Cursor != digest || result.LogicalBytes != 10 ||
		result.RootBytes != 20 || result.MemberBytes != 30 {
		t.Fatalf("malformed result = %+v", result)
	}
	monitor, err := NewStatusMonitor(true, []Owner{owner})
	if err != nil {
		t.Fatal(err)
	}
	result.Owner = owner.Name()
	monitor.ObserveOwner(result)
	status := monitor.Snapshot()
	if err := ValidateStatus(status); err != nil {
		t.Fatal(err)
	}
	got := status.Owners[0]
	if got.LogicalBytes != 10 || got.RootBytes != 20 || got.MemberBytes != 30 {
		t.Fatalf("owner status = %+v", got)
	}
}

func TestRunnerPressureRecoveryDrivesCatalogV3Owner(t *testing.T) {
	digest := "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	state := &catalogV3SequenceStore{sweeps: []store.ServiceCatalogV3LifecycleSweep{
		{Cursor: digest, Scanned: 1, Deleted: 1, More: true},
		{Scanned: 1, Deleted: 1},
	}}
	controller, err := NewController(newMemoryCursorStore(), CatalogV3GenerationOwner{
		Store: state,
		Acquire: func(context.Context) (func(), error) {
			return func() {}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	capacityChecks := 0
	gate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		capacityChecks++
		used := int64(700)
		if capacityChecks == 1 {
			used = 800
		}
		return Capacity{
			TotalBytes: 1_000, AvailableBytes: 1_000 - used, UsedBytes: used,
		}, nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	results := make(chan OwnerResult, 4)
	capacities := make(chan Capacity, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(
			ctx, controller, gate, time.Hour, time.Millisecond,
			func(result OwnerResult) { results <- result },
			func(capacity Capacity, err error) {
				if err != nil {
					capacities <- Capacity{Pressure: PressureUnavailable}
					return
				}
				capacities <- capacity
			},
		)
	}()

	gotResults := make([]OwnerResult, 0, 3)
	gotPressure := make([]Pressure, 0, 3)
	for range 3 {
		select {
		case result := <-results:
			gotResults = append(gotResults, result)
		case <-time.After(time.Second):
			cancel()
			t.Fatal("catalog v3 owner did not complete pressure recovery")
		}
		select {
		case capacity := <-capacities:
			gotPressure = append(gotPressure, capacity.Pressure)
		case <-time.After(time.Second):
			cancel()
			t.Fatal("catalog v3 pressure observation was not reported")
		}
	}
	select {
	case result := <-results:
		cancel()
		t.Fatalf("runner did not idle after catalog v3 pressure recovery: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("catalog v3 pressure runner did not stop")
	}
	if state.calls != 3 || !reflect.DeepEqual(
		gotPressure,
		[]Pressure{PressureCollect, PressureNormal, PressureNormal},
	) {
		t.Fatalf("catalog v3 pressure calls=%d pressure=%v", state.calls, gotPressure)
	}
	if gotResults[0].Completeness != LowerBound || !gotResults[0].More ||
		gotResults[0].Deleted != 1 || gotResults[1].Completeness != Exact ||
		gotResults[1].More || gotResults[1].Deleted != 1 ||
		gotResults[2].Completeness != Exact || gotResults[2].More ||
		gotResults[2].Deleted != 0 {
		t.Fatalf("catalog v3 pressure results = %+v", gotResults)
	}
}
