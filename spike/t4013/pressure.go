package t4013

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

const pressureBallastName = "pressure-ballast.bin"

type pressureBallast struct {
	path string
}

type pressureBallastContract uint8

const (
	pressureBallastV22 pressureBallastContract = iota
	pressureBallastV23
	pressureBallastV25
)

func pressureBallastContractForPlan(plan Plan) pressureBallastContract {
	if planSchemaVersion(plan.Schema) >= 25 {
		return pressureBallastV25
	}
	if planSchemaVersion(plan.Schema) >= 23 {
		return pressureBallastV23
	}
	return pressureBallastV22
}

func validatePressureHostPreflightV25(
	capacity lifecycle.Capacity,
	workspaceAllocated int64,
	safety SafetyEnvelope,
) error {
	if workspaceAllocated < 0 || workspaceAllocated > safety.MaximumPrePressureBytes {
		return errors.New("T40.13 prepared custody exceeds the frozen pre-pressure allocation ceiling")
	}
	growth := safety.MaximumPrePressureBytes - workspaceAllocated
	if growth > capacity.AvailableBytes || capacity.UsedBytes > capacity.TotalBytes-growth {
		return errors.New("T40.13 host cannot retain normal pressure through the pre-pressure phases")
	}
	projected := capacity
	var err error
	projected.UsedBytes, err = checkedAddInt64(projected.UsedBytes, growth)
	if err != nil {
		return err
	}
	projected.AvailableBytes, err = checkedAddInt64(projected.AvailableBytes, -growth)
	if err != nil {
		return err
	}
	watermark, err := percentFloor(projected.TotalBytes, lifecycle.SoftWatermarkPercent-1)
	if err != nil {
		return err
	}
	if projected.UsedBytes > watermark {
		return errors.New("T40.13 host cannot retain normal pressure through the pre-pressure phases")
	}
	projected.Pressure = lifecycle.PressureNormal
	_, err = requiredPressureBallast(projected, safety.MaximumPrePressureBytes, safety)
	if err != nil {
		return errors.Join(err, errors.New("T40.13 host cannot reach the frozen pressure target after pre-pressure growth"))
	}
	return nil
}

func validatePressureHostPreflight(capacity lifecycle.Capacity, safety SafetyEnvelope) error {
	target, err := pressureTargetBytes(capacity.TotalBytes, safety.PressureTargetUsedPercent)
	if err != nil || capacity.UsedBytes < 0 || capacity.UsedBytes > capacity.TotalBytes ||
		capacity.AvailableBytes < 0 || capacity.AvailableBytes > capacity.TotalBytes ||
		capacity.Pressure != lifecycle.PressureNormal || target <= capacity.UsedBytes ||
		target-capacity.UsedBytes > safety.MaximumDataAllocatedBytes {
		return errors.Join(err, errors.New("T40.13 host cannot reach the frozen pressure target inside its custody ceiling"))
	}
	return nil
}

func validatePressureHostPreflightV23(
	capacity lifecycle.Capacity,
	workspaceAllocated int64,
	safety SafetyEnvelope,
) error {
	_, err := requiredPressureBallast(capacity, workspaceAllocated, safety)
	if err != nil || capacity.Pressure != lifecycle.PressureNormal {
		return errors.Join(err, errors.New("T40.13 host cannot reach the frozen pressure target inside its custody ceiling"))
	}
	return nil
}

func pressureTargetBytes(total int64, percent int) (int64, error) {
	if total <= 0 || percent < lifecycle.SoftWatermarkPercent || percent >= lifecycle.HardWatermarkPercent {
		return 0, errors.New("T40.13 pressure target is invalid")
	}
	// Capacity reports ceil(used * 100 / total). Select the midpoint of the
	// complete byte interval that reports the frozen percentage. This leaves
	// headroom for filesystem metadata while remaining deterministic.
	lowerFloor, err := percentFloor(total, percent-1)
	if err != nil {
		return 0, err
	}
	lower, err := checkedAddInt64(lowerFloor, 1)
	if err != nil {
		return 0, err
	}
	upper, err := percentFloor(total, percent)
	if err != nil {
		return 0, err
	}
	if lower > upper {
		return 0, errors.New("T40.13 pressure target cannot be represented")
	}
	span, err := checkedAddInt64(upper, -lower)
	if err != nil {
		return 0, err
	}
	middle, err := checkedAddInt64(lower, span/2)
	if err != nil {
		return 0, err
	}
	return middle, nil
}

func percentFloor(total int64, percent int) (int64, error) {
	whole, err := checkedMulInt64(total/100, int64(percent))
	if err != nil {
		return 0, err
	}
	remainder, err := checkedMulInt64(total%100, int64(percent))
	if err != nil {
		return 0, err
	}
	return checkedAddInt64(whole, remainder/100)
}

func requiredPressureBallast(
	capacity lifecycle.Capacity,
	workspaceAllocated int64,
	safety SafetyEnvelope,
) (int64, error) {
	target, err := pressureTargetBytes(capacity.TotalBytes, safety.PressureTargetUsedPercent)
	if err != nil || capacity.UsedBytes < 0 || capacity.UsedBytes > capacity.TotalBytes ||
		workspaceAllocated < 0 || workspaceAllocated > safety.MaximumDataAllocatedBytes {
		return 0, errors.Join(err, errors.New("T40.13 pressure exercise inputs are invalid"))
	}
	if capacity.UsedBytes >= target {
		return 0, fmt.Errorf("T40.13 host is already at the frozen pressure target: %w", errProductionPressure)
	}
	ballastBytes := target - capacity.UsedBytes
	if ballastBytes > safety.MaximumPressureBallastBytes ||
		ballastBytes > safety.MaximumDataAllocatedBytes-workspaceAllocated ||
		ballastBytes > capacity.AvailableBytes {
		return 0, fmt.Errorf("T40.13 frozen pressure ballast cannot fit its envelope: %w", errProductionPressure)
	}
	return ballastBytes, nil
}

func createPressureBallast(
	ctx context.Context,
	workspace string,
	plan Plan,
) (*pressureBallast, int64, int64, error) {
	if ctx == nil || !filepath.IsAbs(workspace) {
		return nil, 0, 0, errors.New("T40.13 pressure ballast scope is invalid")
	}
	safety := plan.Safety
	contract := pressureBallastContractForPlan(plan)
	info, err := os.Lstat(workspace)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, 0, errors.Join(err, errors.New("T40.13 pressure ballast root is invalid"))
	}
	gate := lifecycle.NewGate(workspace)
	before, gateErr := gate.Check(ctx, 0)
	if gateErr != nil || before.Pressure != lifecycle.PressureNormal {
		return nil, 0, 0, errors.Join(gateErr,
			fmt.Errorf("T40.13 pressure ballast requires normal starting capacity: %w", errProductionPressure))
	}
	_, allocatedBefore, err := measureDataBytesForPlanContext(ctx, plan, workspace)
	if err != nil {
		return nil, 0, 0, err
	}
	if contract >= pressureBallastV25 && allocatedBefore > safety.MaximumPrePressureBytes {
		return nil, 0, 0, errReviewCeiling
	}
	ballastBytes, err := requiredPressureBallast(before, allocatedBefore, safety)
	if err != nil {
		return nil, 0, 0, err
	}
	path := filepath.Join(workspace, pressureBallastName)
	if !isWithin(path, workspace) {
		return nil, 0, 0, errors.New("T40.13 pressure ballast escaped custody")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, 0, 0, err
	}
	allocateErr := allocatePressureFile(file, ballastBytes)
	closeErr := file.Close()
	if allocateErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return nil, 0, 0, errors.Join(allocateErr, closeErr)
	}
	logical, allocated, err := measureDataBytesForPlanContext(ctx, plan, workspace)
	if err != nil || allocated > safety.MaximumDataAllocatedBytes {
		_ = os.Remove(path)
		if err == nil {
			err = errReviewCeiling
		}
		return nil, 0, 0, err
	}
	after, gateErr := lifecycle.NewGate(workspace).Check(ctx, 0)
	if gateErr != nil || !pressureTargetObserved(after, safety.PressureTargetUsedPercent, contract) {
		_ = os.Remove(path)
		return nil, 0, 0, errors.Join(gateErr, errors.New("T40.13 pressure ballast missed its frozen collect target"))
	}
	return &pressureBallast{path: path}, logical, allocated, nil
}

func pressureTargetObserved(
	capacity lifecycle.Capacity,
	target int,
	contract pressureBallastContract,
) bool {
	if capacity.Pressure != lifecycle.PressureCollect {
		return false
	}
	if contract >= pressureBallastV23 {
		return capacity.UsedPercent >= target-1 && capacity.UsedPercent <= target+1
	}
	return capacity.UsedPercent == target
}

func (ballast *pressureBallast) remove(workspace string) error {
	if ballast == nil || !filepath.IsAbs(workspace) || !isWithin(ballast.path, workspace) ||
		filepath.Base(ballast.path) != pressureBallastName {
		return errors.New("T40.13 pressure ballast cleanup scope is invalid")
	}
	info, err := os.Lstat(ballast.path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(err, errors.New("T40.13 pressure ballast cleanup target is invalid"))
	}
	if err := os.Remove(ballast.path); err != nil {
		return err
	}
	if _, err := os.Lstat(ballast.path); !os.IsNotExist(err) {
		return errors.New("T40.13 pressure ballast cleanup left its target behind")
	}
	return nil
}
