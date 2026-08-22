package t4013

import (
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func TestPressureTargetBytesSelectsReportedPercentMidpoint(t *testing.T) {
	tests := []struct {
		name    string
		total   int64
		percent int
		want    int64
	}{
		{name: "exact", total: 100, percent: 82, want: 82},
		{name: "narrow interval", total: 101, percent: 82, want: 82},
		{name: "large", total: 460 << 30, percent: 82, want: 402_545_809_817},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := pressureTargetBytes(test.total, test.percent)
			if err != nil || got != test.want {
				t.Fatalf("target = %d, %v; want %d", got, err, test.want)
			}
			capacity := lifecycle.Capacity{TotalBytes: test.total, UsedBytes: got}
			capacity.AvailableBytes = capacity.TotalBytes - capacity.UsedBytes
			if reported := reportedCapacityPercent(capacity); reported != test.percent {
				t.Fatalf("reported percent = %d; want %d", reported, test.percent)
			}
		})
	}
}

func reportedCapacityPercent(capacity lifecycle.Capacity) int {
	if capacity.UsedBytes == 0 {
		return 0
	}
	return int((capacity.UsedBytes*100 + capacity.TotalBytes - 1) / capacity.TotalBytes)
}

func TestPressureHostPreflightRequiresReachableTarget(t *testing.T) {
	tests := []struct {
		name      string
		capacity  lifecycle.Capacity
		allocated int64
		wantErr   bool
	}{
		{name: "reachable", capacity: lifecycle.Capacity{
			TotalBytes: 460 << 30, UsedBytes: 300 << 30, AvailableBytes: 160 << 30,
			UsedPercent: 66, Pressure: lifecycle.PressureNormal,
		}},
		{name: "already pressured", capacity: lifecycle.Capacity{
			TotalBytes: 460 << 30, UsedBytes: 370 << 30, AvailableBytes: 90 << 30,
			UsedPercent: 81, Pressure: lifecycle.PressureCollect,
		}, wantErr: true},
		{name: "target exceeds custody ceiling", capacity: lifecycle.Capacity{
			TotalBytes: 1_000 << 30, UsedBytes: 500 << 30, AvailableBytes: 500 << 30,
			UsedPercent: 50, Pressure: lifecycle.PressureNormal,
		}, wantErr: true},
		{name: "prepared workspace consumes remaining allocation", capacity: lifecycle.Capacity{
			TotalBytes: 460 << 30, UsedBytes: 300 << 30, AvailableBytes: 160 << 30,
			UsedPercent: 66, Pressure: lifecycle.PressureNormal,
		}, allocated: 24 << 30, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePressureHostPreflightV23(test.capacity, test.allocated, frozenSafetyV10)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
	legacy := tests[len(tests)-1]
	if err := validatePressureHostPreflight(legacy.capacity, frozenSafetyV10); err != nil {
		t.Fatalf("historical preflight acquired V23 workspace charging: %v", err)
	}
}

func TestPressureTargetObservationV23AllowsOnePercentFilesystemDrift(t *testing.T) {
	for _, test := range []struct {
		name     string
		percent  int
		contract pressureBallastContract
		want     bool
	}{
		{name: "v22 exact", percent: 82, contract: pressureBallastV22, want: true},
		{name: "v22 rejects drift", percent: 81, contract: pressureBallastV22},
		{name: "v23 lower drift", percent: 81, contract: pressureBallastV23, want: true},
		{name: "v23 exact", percent: 82, contract: pressureBallastV23, want: true},
		{name: "v23 upper drift", percent: 83, contract: pressureBallastV23, want: true},
		{name: "v23 rejects low", percent: 80, contract: pressureBallastV23},
		{name: "v23 rejects high", percent: 84, contract: pressureBallastV23},
	} {
		t.Run(test.name, func(t *testing.T) {
			capacity := lifecycle.Capacity{
				Pressure: lifecycle.PressureCollect, UsedPercent: test.percent,
			}
			if got := pressureTargetObserved(capacity, 82, test.contract); got != test.want {
				t.Fatalf("observed = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRequiredPressureBallastHonorsBothFrozenAllocationBounds(t *testing.T) {
	base := lifecycle.Capacity{
		TotalBytes: 100 << 30, UsedBytes: 60 << 30, AvailableBytes: 40 << 30,
	}
	tests := []struct {
		name      string
		capacity  lifecycle.Capacity
		allocated int64
		safety    SafetyEnvelope
		want      int64
		wantGate  bool
	}{
		{name: "fits", capacity: base, allocated: 20 << 30, safety: frozenSafetyV10, want: (21 << 30) + (1 << 29)},
		{name: "already pressured", capacity: lifecycle.Capacity{
			TotalBytes: 100 << 30, UsedBytes: 82 << 30, AvailableBytes: 18 << 30,
		}, allocated: 20 << 30, safety: frozenSafetyV10, wantGate: true},
		{name: "ballast bound", capacity: lifecycle.Capacity{
			TotalBytes: 500 << 30, UsedBytes: 250 << 30, AvailableBytes: 250 << 30,
		}, allocated: 20 << 30, safety: frozenSafetyV10, wantGate: true},
		{name: "workspace bound", capacity: base, allocated: 80 << 30, safety: frozenSafetyV10, wantGate: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := requiredPressureBallast(test.capacity, test.allocated, test.safety)
			if test.wantGate {
				if !errors.Is(err, errProductionPressure) {
					t.Fatalf("error = %v; want production pressure gate", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("ballast = %d, %v; want %d", got, err, test.want)
			}
		})
	}
}
