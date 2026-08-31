package lifecycle

import (
	"context"
	"errors"
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
