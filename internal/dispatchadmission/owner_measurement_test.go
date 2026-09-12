package dispatchadmission

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestOwnerMeasurementRetainsAndDrains(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		owners, err := NewOwners(ctx, OwnerLimits{Owners: 3, Requests: 2})
		if err != nil {
			t.Fatal(err)
		}
		target, _ := owners.Enter(ctx)
		peer, _ := owners.Enter(ctx)
		request, _ := owners.EnterRequest(ctx)
		var measurement *OwnerMeasurement
		joined := make(chan struct{})
		go func() { defer close(joined); measurement, err = target.FenceMeasurement(ctx) }()
		synctest.Wait()
		peer.End()
		synctest.Wait()
		select {
		case <-joined:
			t.Fatal("ignored the actual request tail")
		default:
		}
		request.End()
		<-joined
		if err != nil || !measurement.Quiescent(ctx) {
			t.Fatal(err)
		}
		owners.mu.Lock()
		if owners.active == 0 || owners.pausedReady || owners.requestsReady || owners.terminal != 0 {
			t.Error("manufactured ordinary or terminal drainage")
		}
		owners.mu.Unlock()
		if measurement.Resume(ctx) != nil {
			t.Fatal("resume")
		}
		probe, err := owners.EnterRequest(ctx)
		if err != nil {
			t.Fatal(err)
		}
		probe.End()
		target.End()
		if owners.Pause(ctx) != nil {
			t.Fatal("target was not ended normally")
		}
	})
}

func TestOwnerMeasurementRefusals(t *testing.T) {
	for _, mode := range []string{"ordinary_reopen", "ordinary_pause", "copied_end", "replay", "request", "canceled", "deadline", "renewed_deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			owners, err := NewOwners(ctx, OwnerLimits{Owners: 2, Requests: 1})
			if err != nil {
				t.Fatal(err)
			}
			target, _ := owners.Enter(ctx)
			switch mode {
			case "request":
				request, _ := owners.EnterRequest(ctx)
				_, err = request.FenceMeasurement(ctx)
			case "canceled":
				cancel()
				_, err = target.FenceMeasurement(ctx)
			case "deadline":
				_, err = target.FenceMeasurement(t.Context())
			default:
				measurement, acquireErr := target.FenceMeasurement(ctx)
				if acquireErr != nil {
					t.Fatal(acquireErr)
				}
				switch mode {
				case "ordinary_reopen":
					err = owners.Resume()
				case "ordinary_pause":
					err = owners.Pause(ctx)
				case "renewed_deadline":
					err = measurement.Resume(context.Background())
				case "copied_end":
					copy := target
					copy.End()
					err = owners.Err()
				case "replay":
					if measurement.Resume(ctx) != nil {
						t.Fatal("first resume")
					}
					err = measurement.Resume(ctx)
				}
			}
			if err == nil || owners.Err() == nil {
				t.Fatal("refusal did not latch")
			}
		})
	}
}
