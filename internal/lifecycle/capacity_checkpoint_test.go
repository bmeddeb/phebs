package lifecycle

import (
	"context"
	"errors"
	"testing"
)

func TestCapacityCheckpointFailureCannotBecomePressureRefusal(t *testing.T) {
	for _, used := range []int64{700, 800, 900} {
		t.Run(string(rune(used)), func(t *testing.T) {
			collector := testControlCollector(t, []Owner{controlledTestOwner{name: "a"}}, 1)
			gate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
				return Capacity{TotalBytes: 1000, UsedBytes: used, AvailableBytes: 1000 - used}, nil
			})
			if err := collector.SetCapacityCheckpoint(func(context.Context) error { return errors.Join(ErrPressureRefusal, context.Canceled) }); err != nil {
				t.Fatal(err)
			}
			capacity, err := collector.checkCapacity(t.Context(), gate)
			if err == nil || errors.Is(err, ErrPressureRefusal) || capacity.Pressure != PressureUnavailable {
				t.Fatal(capacity, err)
			}
			if collector.SetCapacityCheckpoint(func(context.Context) error { return nil }) == nil {
				t.Fatal("checkpoint replaced")
			}
		})
	}
}
