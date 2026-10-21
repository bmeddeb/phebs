//go:build linux

package t421

import (
	"context"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestLinuxEpochEngineMeasurementAbsentActorRefuses(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	called := false
	if err := (&linuxEpochTraceRoot{}).withEngineMeasurement(ctx, func(context.Context, dispatchadmission.EngineMeasurementIdentity) error { called = true; return nil }); err == nil || called {
		t.Fatal("missing owned actor accepted")
	}
}
