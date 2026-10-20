package t421

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func TestEpochProcessPrivateThresholdPreservesHistoricalEnvelope(t *testing.T) {
	if frozenSafetyEnvelope().MaximumPeakRSSBytes != 20<<30 {
		t.Fatal("historical envelope changed")
	}
	for _, test := range []struct {
		name      string
		limit     uint64
		wantError bool
	}{
		{"exact", 12288, false},
		{"one-byte-over", 12287, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var failures atomic.Int64
			meter, err := newExecutionProcessPhaseObservationWithRSSLimit(t.Context(), 41, 2, "phebs", map[string]string{"phebs": "phebs", "git": "git"}, func() { failures.Add(1) },
				func(context.Context, int) ([]t4013.NativeProcessRecord, error) { return epochProcessFixtureRows(), nil }, test.limit)
			if meter == nil || errors.Is(err, errEpochProcessRSS) != test.wantError {
				t.Fatal("private threshold not applied", err)
			}
			result, err := meter.close()
			if errors.Is(err, errEpochProcessRSS) != test.wantError || result.RSSLimitExceeded != test.wantError || !result.Joined ||
				len(result.Phases) != 1 || !result.Phases[0].Observation.Available || result.Phases[0].Observation.ObservedRSSBytes != 12288 {
				t.Fatal("actual overshoot measurement or joined prefix lost", result, err)
			}
			wantFailures := int64(0)
			if test.wantError {
				wantFailures = 1
			}
			if failures.Load() != wantFailures {
				t.Fatal("unexpected cancellation count", failures.Load())
			}
		})
	}
	if meter, err := newExecutionProcessPhaseObservationWithRSSLimit(t.Context(), 41, 2, "phebs", map[string]string{"phebs": "phebs"}, func() {},
		func(context.Context, int) ([]t4013.NativeProcessRecord, error) { return epochProcessFixtureRows(), nil }, 0); err == nil || meter != nil {
		t.Fatal("zero threshold issued an unbounded sampler")
	}
}

func TestEpochProcessPreparedGaugePreservesRootBirth(t *testing.T) {
	gauge, err := NewProcessObservationGauge(41, "different-already-bound-birth", map[string]string{"phebs": "phebs", "git": "git"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	meter, err := newExecutionProcessObservationWithGauge(t.Context(), 41, 2, "phebs", func() {},
		func(context.Context, int) ([]t4013.NativeProcessRecord, error) {
			calls++
			return epochProcessFixtureRows(), nil
		}, 6<<30, gauge)
	if err == nil || meter == nil || gauge.expectedRootStartIdentity != "different-already-bound-birth" || calls != 1 {
		t.Fatal("native scope binding replaced by initial census", err, calls)
	}
	value, err := meter.close()
	if err == nil || !value.Joined || value.Phases[0].Observation.Available || calls != 1 {
		t.Fatal("refused birth retried or manufactured a measurement", value, err, calls)
	}
}

func TestEpochProcessKnownClassifierCancellationNeverCommitsCensus(t *testing.T) {
	for _, startup := range []bool{true, false} {
		name := "regular-with-positive-prefix"
		if startup {
			name = "startup"
		}
		t.Run(name, func(t *testing.T) {
			rows := epochProcessFixtureRows()
			gauge, err := NewProcessObservationGauge(41, rows[0].StartIdentity, map[string]string{"phebs": "phebs", "git": "git"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if !startup {
				if _, err := gauge.acceptRows(rows); err != nil {
					t.Fatal(err)
				}
			}
			gauge.classify = func(_ context.Context, row t4013.NativeProcessRecord) (int, bool) {
				if row.PID == rows[len(rows)-1].PID {
					cancel()
				}
				class, known := gauge.nameClasses[row.ObservedName]
				return class, known
			}
			probes := 0
			probe := func(context.Context, int) ([]t4013.NativeProcessRecord, error) {
				probes++
				return rows, nil
			}
			var observation ProcessObservation
			if startup {
				meter, err := newExecutionProcessObservationWithGauge(ctx, 41, 2, "phebs", func() {}, probe, 6<<30, gauge)
				if err == nil || meter == nil {
					t.Fatal("late startup classification admitted", err)
				}
				result, err := meter.close()
				if err == nil || !result.Joined || len(result.Phases) != 1 {
					t.Fatal("refused startup did not retain its joined unavailable prefix", result, err)
				}
				observation = result.Phases[0].Observation
			} else {
				gauge.probe = probe
				observation, err = gauge.Sample(ctx)
				if err == nil {
					t.Fatal("late regular classification admitted")
				}
			}
			want := uint64(1)
			if startup {
				want = 0
			}
			if observation.Available || observation.FailureClass != "measurement_unavailable" || observation.CompletedCensuses != want || probes != 1 {
				t.Fatal("canceled classifier committed or retried a census", observation, probes)
			}
		})
	}
}
