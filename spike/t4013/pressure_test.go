package t4013

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/spike/t401"
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

func TestV25PressurePreflightFundsGrowthAndBallast(t *testing.T) {
	tests := []struct {
		name      string
		used      int64
		allocated int64
		wantErr   bool
	}{
		{name: "reachable after growth", used: 285 << 30},
		{name: "growth reaches collect pressure", used: 300 << 30, wantErr: true},
		{name: "ballast exceeds remaining custody", used: 260 << 30, wantErr: true},
		{name: "prepared custody already exceeds bound", used: 285 << 30, allocated: 73 << 30, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capacity := lifecycle.Capacity{
				TotalBytes: 460 << 30, UsedBytes: test.used,
				AvailableBytes: 460<<30 - test.used,
				Pressure:       lifecycle.PressureNormal,
			}
			err := validatePressureHostPreflightV25(capacity, test.allocated, frozenSafetyV25)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, test.wantErr)
			}
		})
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

func TestPressureRehearsalRequiresBoundedNormalFilesystem(t *testing.T) {
	tests := []struct {
		name     string
		capacity lifecycle.Capacity
		wantErr  bool
	}{
		{name: "bounded normal", capacity: lifecycle.Capacity{
			TotalBytes: maximumPressureRehearsalFilesystemBytes,
			Pressure:   lifecycle.PressureNormal,
		}},
		{name: "host filesystem", capacity: lifecycle.Capacity{
			TotalBytes: maximumPressureRehearsalFilesystemBytes + 1,
			Pressure:   lifecycle.PressureNormal,
		}, wantErr: true},
		{name: "already pressured", capacity: lifecycle.Capacity{
			TotalBytes: maximumPressureRehearsalFilesystemBytes,
			Pressure:   lifecycle.PressureCollect,
		}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePressureRehearsalCapacity(test.capacity)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v; wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestFrozenStructuralPressureRetirementFitsRecoveryDeadline(t *testing.T) {
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	var structural t401.Profile
	for _, profile := range profiles {
		if profile.Name == t401.StructuralProfileName {
			structural = profile
			break
		}
	}
	reuseClasses := int(structural.Shape.GoBlobReuseClasses)
	if reuseClasses == 0 {
		t.Fatal("frozen structural profile is missing")
	}
	minimumPlacementsPerBlob := int(structural.Aggregate.EligibleGoFiles / structural.Shape.GoBlobReuseClasses)
	if 2*minimumPlacementsPerBlob <= sourcepartition.MaxPlacementsPerPartition {
		t.Fatal("frozen structural shape no longer proves one source member per reuse class")
	}

	root := t.TempDir()
	repository := strings.Repeat("a", 64)
	generation := strings.Repeat("b", 64)
	collecting := filepath.Join(root, repository, observationpublication.InventoryPublicationDirectoryV2, "collecting-"+generation)
	sourceSegment := filepath.Join(collecting, observationpublication.InventoryPublicationSourceNameV2, "segment-00000")
	inventorySegment := filepath.Join(collecting, observationpublication.InventoryPublicationInventoryNameV2, "segment-00000")
	objects := filepath.Join(inventorySegment, "objects")
	for _, directory := range []string{sourceSegment, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sourceDigest := strings.Repeat("c", 64)
	write(filepath.Join(filepath.Dir(sourceSegment), "phebs-source-partitions-"+sourceDigest+".root.json"))
	write(filepath.Join(sourceSegment, "phebs-source-partitions-"+sourceDigest+".manifest.json"))
	write(filepath.Join(filepath.Dir(inventorySegment), observationpublication.InventoryRootNameV2))
	write(filepath.Join(inventorySegment, "segment.json"))
	for ordinal := 0; ordinal < reuseClasses; ordinal++ {
		write(filepath.Join(sourceSegment, fmt.Sprintf("phebs-source-partitions-%016x-%016x.%05d.jsonl", 1, 2, ordinal)))
		write(filepath.Join(inventorySegment, fmt.Sprintf("member-%05d.jsonl", ordinal)))
	}
	// Replay the retained revision-B topology: it adds one distinct observation
	// while preserving 512 source members, making the larger retirement tree.
	for ordinal := 0; ordinal < reuseClasses+1; ordinal++ {
		write(filepath.Join(objects, fmt.Sprintf("%016x-%064x.json", ordinal, ordinal)))
	}

	cursor := ""
	turns := 0
	deleted := 0
	for {
		result, err := observationpublication.SweepInventoryLifecycleV2(
			t.Context(), root, time.Now(), cursor, observationpublication.InventoryPinsV2{},
			lifecycle.MaxCandidatesPerTick, lifecycle.MaxDeletesPerTick,
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.Deleted > lifecycle.MaxDeletesPerTick {
			t.Fatalf("turn deleted %d entries, limit %d", result.Deleted, lifecycle.MaxDeletesPerTick)
		}
		turns++
		deleted += result.Deleted
		cursor = result.Cursor
		if !result.More {
			break
		}
		if turns > 128 {
			t.Fatal("frozen structural retirement did not converge")
		}
	}

	const (
		recoveryDeadline       = 10 * time.Minute
		minimumExecutionMargin = 4 * time.Minute
	)
	wantDeleted := 10 + 2*reuseClasses + (reuseClasses + 1)
	wantTurns := (wantDeleted + lifecycle.MaxDeletesPerTick - 1) / lifecycle.MaxDeletesPerTick
	if deleted != wantDeleted || turns != wantTurns {
		t.Fatalf("retirement = %d entries in %d turns, want %d in %d", deleted, turns, wantDeleted, wantTurns)
	}
	// Allow worst owner alignment and two wholly fresh 14-owner cycles after
	// deletion. The remainder of the fixed deadline funds bounded sweep work and
	// the one-second status observer.
	scheduled := time.Duration((turns+3)*len(expectedCollectionOwners)) * lifecycle.DefaultPressureRecoveryDelay
	if scheduled > recoveryDeadline-minimumExecutionMargin {
		t.Fatalf("scheduled recovery = %v, want at least %v execution margin", scheduled, minimumExecutionMargin)
	}
}
