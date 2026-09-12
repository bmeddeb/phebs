package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestLocalImportMeasurementRefusesBeforeLaunch(t *testing.T) {
	ctx, owner, _ := storeAccountingFixture(t, 2, 1)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, test := range []struct {
		name  string
		ctx   context.Context
		owner *storeaccounting.SDKOwner
		cause error
	}{
		{"missing_context", nil, owner.SDKOwner, errLocalEngineQuiescence},
		{"unbounded_context", context.Background(), owner.SDKOwner, errLocalEngineQuiescence},
		{"missing_owner", ctx, nil, storeaccounting.ErrConfig},
		{"invalid_owner", ctx, &storeaccounting.SDKOwner{}, storeaccounting.ErrConfig},
		{"canceled_context", canceled, owner.SDKOwner, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "must-not-create")
			runtime, stop, guard, err := StartLocalImportWithMeasurement(test.ctx, path, test.owner)
			if !errors.Is(err, test.cause) || runtime != (LocalRuntime{}) || stop != nil || guard != nil {
				t.Fatal("invalid import was admitted", runtime, err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refused import created custody", err)
			}
		})
	}
}
