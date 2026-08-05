package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"os"
	"sync"
)

type Pressure string

const (
	PressureNormal      Pressure = "normal"
	PressureCollect     Pressure = "collect"
	PressureRefuse      Pressure = "refuse"
	PressureUnavailable Pressure = "unavailable"
)

type Capacity struct {
	TotalBytes     int64
	AvailableBytes int64
	UsedBytes      int64
	ProjectedBytes int64
	UsedPercent    int
	Pressure       Pressure
}

type CapacityProbe func(context.Context, string) (Capacity, error)

type Gate struct {
	dataDir string
	probe   CapacityProbe

	mu      sync.Mutex
	latched bool
}

func NewGate(dataDir string) *Gate {
	return &Gate{dataDir: dataDir, probe: ProbeCapacity}
}

func ProbeCapacity(ctx context.Context, dataDir string) (Capacity, error) {
	if err := ctx.Err(); err != nil {
		return Capacity{}, err
	}
	before, err := os.Lstat(dataDir)
	if err != nil {
		return Capacity{}, fmt.Errorf("inspect lifecycle data root: %w", err)
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return Capacity{}, errors.New("lifecycle data root is not a real directory")
	}
	root, err := os.Open(dataDir)
	if err != nil {
		return Capacity{}, fmt.Errorf("open lifecycle data root: %w", err)
	}
	defer func() { _ = root.Close() }()
	opened, err := root.Stat()
	if err != nil {
		return Capacity{}, fmt.Errorf("inspect open lifecycle data root: %w", err)
	}
	after, err := os.Lstat(dataDir)
	if err != nil || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		if err != nil {
			return Capacity{}, fmt.Errorf("reinspect lifecycle data root: %w", err)
		}
		return Capacity{}, errors.New("lifecycle data root changed while probing capacity")
	}
	total, available, err := platformCapacity(root)
	if err != nil {
		return Capacity{}, err
	}
	if total <= 0 || available < 0 || available > total {
		return Capacity{}, errors.New("filesystem returned invalid lifecycle capacity")
	}
	return Capacity{TotalBytes: total, AvailableBytes: available, UsedBytes: total - available}, nil
}

func (gate *Gate) Check(ctx context.Context, estimatedBytes int64) (Capacity, error) {
	if gate == nil || gate.probe == nil || gate.dataDir == "" {
		return Capacity{Pressure: PressureUnavailable}, ErrCapacityUnavailable
	}
	if estimatedBytes < 0 || estimatedBytes > MaxPressureDependentAdmissionBytes {
		return Capacity{}, fmt.Errorf(
			"lifecycle admission bytes must be from 0 through %d",
			MaxPressureDependentAdmissionBytes,
		)
	}
	capacity, err := gate.probe(ctx, gate.dataDir)
	if err != nil {
		if ctx.Err() != nil {
			return Capacity{}, ctx.Err()
		}
		return Capacity{Pressure: PressureUnavailable},
			fmt.Errorf("%w: %v", ErrCapacityUnavailable, err)
	}
	if capacity.UsedBytes > math.MaxInt64-estimatedBytes {
		return Capacity{}, errors.New("lifecycle projected capacity overflows int64")
	}
	capacity.ProjectedBytes = capacity.UsedBytes + estimatedBytes
	if capacity.ProjectedBytes > capacity.TotalBytes {
		capacity.ProjectedBytes = capacity.TotalBytes
	}
	capacity.UsedPercent = percentCeiling(capacity.ProjectedBytes, capacity.TotalBytes)

	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.latched {
		if capacity.UsedPercent >= ResumeWatermarkPercent {
			capacity.Pressure = PressureRefuse
			return capacity, ErrPressureRefusal
		}
		gate.latched = false
	}
	switch {
	case capacity.UsedPercent >= HardWatermarkPercent:
		gate.latched = true
		capacity.Pressure = PressureRefuse
		return capacity, ErrPressureRefusal
	case capacity.UsedPercent >= SoftWatermarkPercent:
		capacity.Pressure = PressureCollect
	default:
		capacity.Pressure = PressureNormal
	}
	return capacity, nil
}

func percentCeiling(part, total int64) int {
	if part <= 0 || total <= 0 {
		return 0
	}
	high, low := bits.Mul64(uint64(part), 100)
	percent, remainder := bits.Div64(high, low, uint64(total))
	if remainder > 0 {
		percent++
	}
	if percent > 100 {
		return 100
	}
	return int(percent)
}
