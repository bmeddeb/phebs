package t4013

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckedArithmeticBoundaries(t *testing.T) {
	addCases := []struct {
		left, right int64
		want        int64
		overflow    bool
	}{
		{0, 0, 0, false}, {maxInt64Value, 0, maxInt64Value, false},
		{maxInt64Value, 1, 0, true}, {-maxInt64Value - 1, -1, 0, true},
		{-1, 1, 0, false},
	}
	for _, test := range addCases {
		got, err := checkedAddInt64(test.left, test.right)
		if (err != nil) != test.overflow || !test.overflow && got != test.want {
			t.Fatalf("checkedAddInt64(%d, %d) = %d, %v", test.left, test.right, got, err)
		}
	}
	mulCases := []struct {
		left, right int64
		want        int64
		overflow    bool
	}{
		{maxInt64Value, 1, maxInt64Value, false}, {-maxInt64Value - 1, 1, -maxInt64Value - 1, false},
		{maxInt64Value, 2, 0, true}, {-maxInt64Value - 1, 2, 0, true},
		{-maxInt64Value - 1, -1, 0, true}, {-maxInt64Value, -1, maxInt64Value, false},
	}
	for _, test := range mulCases {
		got, err := checkedMulInt64(test.left, test.right)
		if (err != nil) != test.overflow || !test.overflow && got != test.want {
			t.Fatalf("checkedMulInt64(%d, %d) = %d, %v", test.left, test.right, got, err)
		}
	}
}

func TestMetricAggregationRefusesOverflow(t *testing.T) {
	if _, err := mergeMetrics(PhaseMetrics{WallMS: maxInt64Value}, PhaseMetrics{WallMS: 1}); err == nil {
		t.Fatal("wall aggregation accepted signed overflow")
	}
	if _, err := mergeMetrics(PhaseMetrics{PublicationWrites: maxInt64Value}, PhaseMetrics{PublicationWrites: 1}); err == nil {
		t.Fatal("scalar aggregation accepted signed overflow")
	}
	if _, err := mergeMetrics(PhaseMetrics{GitToIndexTransitions: maxInt64Value}, PhaseMetrics{GitToIndexTransitions: 1}); err == nil {
		t.Fatal("process-class transition aggregation accepted signed overflow")
	}
	if _, err := mergeConcurrentMetrics(PhaseMetrics{PeakRSSBytes: maxInt64Value}, PhaseMetrics{PeakRSSBytes: 1}); err == nil {
		t.Fatal("concurrent RSS aggregation accepted signed overflow")
	}
}

func TestMetricAggregationPreservesSimultaneousOperationFailure(t *testing.T) {
	operationErr := errors.New("operation failed")
	metrics, err := mergeMetricsPreservingError(
		operationErr,
		PhaseMetrics{WallMS: maxInt64Value, ControlReads: 7},
		PhaseMetrics{WallMS: 1},
	)
	if !errors.Is(err, operationErr) || !strings.Contains(err.Error(), "aggregation overflowed") ||
		metrics.ControlReads != 7 {
		t.Fatalf("simultaneous failures were not preserved: %v", err)
	}
}
