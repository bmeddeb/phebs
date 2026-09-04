package lifecycle

import (
	"context"
	"errors"
	"math"
	"slices"
	"sync"
	"time"
)

const (
	CycleObservationSchema      = "phebs-lifecycle-cycle-observation-v1"
	Pressure80ObservationSchema = "phebs-pressure-80-observation-v1"
	Pressure80ReportCalls       = uint64(2)
	MaxCycleObservationTurns    = CycleTurnLimit(4_096)
)

var ErrCycleObservationPending = errors.New("lifecycle cycle observation is pending")

type CycleTurnLimit uint64

type CycleOwnerObservation struct {
	Name         string       `json:"name"`
	State        string       `json:"state"`
	Completeness Completeness `json:"completeness"`
	Scanned      uint64       `json:"scanned"`
	Deleted      uint64       `json:"deleted"`
	LogicalBytes uint64       `json:"logical_bytes"`
	RootBytes    uint64       `json:"root_bytes"`
	MemberBytes  uint64       `json:"member_bytes"`
	Backlog      bool         `json:"backlog"`
	AttemptedAt  time.Time    `json:"attempted_at"`
}

type TransitionCapacityObservation struct {
	Completeness   Completeness `json:"completeness"`
	Pressure       Pressure     `json:"pressure"`
	TotalBytes     int64        `json:"total_bytes"`
	AvailableBytes int64        `json:"available_bytes"`
	UsedBytes      int64        `json:"used_bytes"`
	ProjectedBytes int64        `json:"projected_bytes"`
	UsedPercent    int          `json:"used_percent"`
	ObservedAt     time.Time    `json:"observed_at"`
}

type CycleObservation struct {
	Schema       string                        `json:"schema"`
	FenceAt      time.Time                     `json:"fence_at"`
	OwnerTurns   uint64                        `json:"owner_turns"`
	Scanned      uint64                        `json:"scanned"`
	Deleted      uint64                        `json:"deleted"`
	LogicalBytes uint64                        `json:"logical_bytes"`
	RootBytes    uint64                        `json:"root_bytes"`
	MemberBytes  uint64                        `json:"member_bytes"`
	Capacity     TransitionCapacityObservation `json:"capacity"`
	Owners       []CycleOwnerObservation       `json:"owners"`
}

type Pressure80Observation struct {
	Schema                  string                        `json:"schema"`
	BallastFenceAt          time.Time                     `json:"ballast_fence_at"`
	PriorCapacityObservedAt time.Time                     `json:"prior_capacity_observed_at"`
	Capacity                TransitionCapacityObservation `json:"capacity"`
}

type cycleObservationResult struct {
	value CycleObservation
	err   error
}

// CycleCollector is an exact-mode-only observer for lifecycle.Run's existing
// serial owner/capacity callbacks. It retains cumulative scalars and one final
// bounded owner cycle; it performs no lifecycle or status read of its own.
type CycleCollector struct {
	mu       sync.Mutex
	owners   []string
	maxTurns uint64
	now      func() time.Time

	started  bool
	finished bool
	fence    time.Time
	done     chan cycleObservationResult

	turns            uint64
	scanned          uint64
	deleted          uint64
	logical          uint64
	root             uint64
	member           uint64
	cycle            []CycleOwnerObservation
	cycleValid       bool
	cycleActive      bool
	readyCycle       []CycleOwnerObservation
	latest           time.Time
	observation      CycleObservation
	collectAttempted bool
	awaitingCapacity bool
}

func NewCycleCollector(owners []Owner, maxTurns CycleTurnLimit) (*CycleCollector, error) {
	registered := make([]registeredOwner, 0, len(owners))
	for _, owner := range owners {
		if owner == nil {
			return nil, errors.New("lifecycle cycle owner is nil")
		}
		registered = append(registered, registeredOwner{name: owner.Name(), owner: owner})
	}
	normalized, err := normalizeOwners(registered)
	if err != nil {
		return nil, err
	}
	if uint64(maxTurns) < uint64(len(normalized)) {
		return nil, errors.New("lifecycle cycle turn limit is smaller than its owner inventory")
	}
	if maxTurns > MaxCycleObservationTurns {
		return nil, errors.New("lifecycle cycle turn limit exceeds its fixed maximum")
	}
	names := make([]string, len(normalized))
	for index := range normalized {
		names[index] = normalized[index].name
	}
	return &CycleCollector{
		owners: names, maxTurns: uint64(maxTurns), now: time.Now,
		cycle: make([]CycleOwnerObservation, 0, len(names)),
	}, nil
}

// AwaitNormal arms one phase-local observation and waits for a complete clean
// sorted cycle followed immediately by lifecycle.Run's exact-normal capacity
// callback. A collector has one waiter and one result.
func (collector *CycleCollector) AwaitNormal(ctx context.Context) (CycleObservation, error) {
	if ctx == nil || collector == nil {
		return CycleObservation{}, errors.New("lifecycle cycle observation is incomplete")
	}
	collector.mu.Lock()
	if collector.started {
		collector.mu.Unlock()
		return CycleObservation{}, errors.New("lifecycle cycle observation was already started")
	}
	collector.started = true
	collector.fence = collector.now().UTC()
	collector.latest = collector.fence
	collector.done = make(chan cycleObservationResult, 1)
	done := collector.done
	collector.mu.Unlock()

	select {
	case <-ctx.Done():
		collector.cancel()
		return CycleObservation{}, ctx.Err()
	case result := <-done:
		if err := ctx.Err(); err != nil {
			collector.cancel()
			return CycleObservation{}, err
		}
		return cloneCycleObservation(result.value), result.err
	}
}

func (collector *CycleCollector) ObserveOwner(result OwnerResult) {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if !collector.started || collector.finished {
		return
	}
	if result.Owner == "" {
		collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle owner is absent"))
		return
	}
	if result.AttemptedAt.IsZero() {
		collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle owner timestamp is absent"))
		return
	}
	if result.AttemptedAt.Before(collector.fence) {
		if collector.cycleActive {
			collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle timestamp moved backwards"))
		}
		return
	}
	if collector.awaitingCapacity || result.AttemptedAt.Before(collector.latest) {
		collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle callback order is invalid"))
		return
	}
	if !slices.Contains(collector.owners, result.Owner) {
		collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle reported an unknown owner"))
		return
	}
	if result.Scanned < 0 || result.Scanned > MaxCandidatesPerTick ||
		result.Deleted < 0 || result.Deleted > MaxDeletesPerTick ||
		result.Deleted > result.Scanned || result.LogicalBytes < 0 ||
		result.RootBytes < 0 || result.MemberBytes < 0 {
		collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle owner result is invalid"))
		return
	}
	if collector.turns == collector.maxTurns ||
		!addCycleTotal(&collector.scanned, uint64(result.Scanned)) ||
		!addCycleTotal(&collector.deleted, uint64(result.Deleted)) ||
		!addCycleTotal(&collector.logical, uint64(result.LogicalBytes)) ||
		!addCycleTotal(&collector.root, uint64(result.RootBytes)) ||
		!addCycleTotal(&collector.member, uint64(result.MemberBytes)) {
		collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle observation exceeded its bound"))
		return
	}
	collector.turns++
	collector.latest = result.AttemptedAt
	collector.awaitingCapacity = true

	if result.CycleStart {
		if result.Owner != collector.owners[0] || collector.cycleActive {
			collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle start is invalid"))
			return
		}
		collector.cycle = collector.cycle[:0]
		collector.cycleValid = true
		collector.cycleActive = true
	}
	if !collector.cycleActive {
		return
	}
	index := len(collector.cycle)
	if index >= len(collector.owners) || result.Owner != collector.owners[index] ||
		result.CycleStart != (index == 0) ||
		result.CycleComplete != (index == len(collector.owners)-1) {
		collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle order is invalid"))
		return
	}
	state := "ok"
	if result.Err != nil {
		state = "error"
		collector.cycleValid = false
	}
	if result.Owner == JobOwner {
		collector.cycleValid = collector.cycleValid &&
			result.Completeness == LowerBound && !result.More
	} else {
		collector.cycleValid = collector.cycleValid &&
			result.Completeness == Exact && !result.More
	}
	collector.cycle = append(collector.cycle, CycleOwnerObservation{
		Name: result.Owner, State: state, Completeness: result.Completeness,
		Scanned: uint64(result.Scanned), Deleted: uint64(result.Deleted),
		LogicalBytes: uint64(result.LogicalBytes), RootBytes: uint64(result.RootBytes),
		MemberBytes: uint64(result.MemberBytes), Backlog: result.More,
		AttemptedAt: result.AttemptedAt.UTC(),
	})
	if result.CycleComplete {
		if collector.cycleValid {
			collector.readyCycle = append(collector.readyCycle[:0], collector.cycle...)
		}
		collector.cycleActive = false
		collector.cycle = collector.cycle[:0]
	}
}

func (collector *CycleCollector) ObserveCapacity(capacity Capacity, capacityErr error) {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if !collector.started || collector.finished {
		return
	}
	if !collector.awaitingCapacity {
		if collector.turns > 0 {
			collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle callback order is invalid"))
		}
		return
	}
	collector.awaitingCapacity = false
	if capacityErr != nil || capacity.Pressure != PressureNormal {
		collector.cycleValid = false
		collector.readyCycle = collector.readyCycle[:0]
		return
	}
	observedAt := collector.now().UTC()
	if observedAt.Before(collector.latest) {
		collector.finishLocked(CycleObservation{}, errors.New("lifecycle cycle callback order is invalid"))
		return
	}
	observed, err := transitionCapacityObservation(capacity, PressureNormal, observedAt)
	if err != nil {
		collector.finishLocked(CycleObservation{}, err)
		return
	}
	collector.latest = observedAt
	if len(collector.readyCycle) == 0 {
		return
	}
	value := CycleObservation{
		Schema: CycleObservationSchema, FenceAt: collector.fence,
		OwnerTurns: collector.turns, Scanned: collector.scanned, Deleted: collector.deleted,
		LogicalBytes: collector.logical, RootBytes: collector.root, MemberBytes: collector.member,
		Capacity: observed, Owners: append([]CycleOwnerObservation(nil), collector.readyCycle...),
	}
	collector.observation = cloneCycleObservation(value)
	collector.cycle = nil
	collector.readyCycle = nil
	collector.finishLocked(value, nil)
}

// ReadPressure80Collect performs the second zero-native-read R observation.
// Gate.Check(0)'s filesystem-capacity probe is metadata, not C/S/M/W work.
func (collector *CycleCollector) ReadPressure80Collect(
	ctx context.Context,
	gate *Gate,
	ballastFence time.Time,
) (Pressure80Observation, error) {
	if ctx == nil || collector == nil || gate == nil {
		return Pressure80Observation{}, errors.New("pressure 80 observation is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return Pressure80Observation{}, err
	}
	collector.mu.Lock()
	if collector.collectAttempted {
		collector.mu.Unlock()
		return Pressure80Observation{}, errors.New("pressure 80 observation was already attempted")
	}
	collector.collectAttempted = true
	normal := cloneCycleObservation(collector.observation)
	now := collector.now
	collector.mu.Unlock()
	if normal.Schema != CycleObservationSchema || ballastFence.IsZero() ||
		ballastFence.Before(normal.Capacity.ObservedAt) {
		return Pressure80Observation{}, ErrCycleObservationPending
	}
	capacity, err := gate.Check(ctx, 0)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Pressure80Observation{}, contextErr
		}
		return Pressure80Observation{}, errors.New("pressure 80 capacity observation failed")
	}
	if err := ctx.Err(); err != nil {
		return Pressure80Observation{}, err
	}
	observedAt := now().UTC()
	if observedAt.Before(ballastFence) {
		return Pressure80Observation{}, errors.New("pressure 80 capacity did not follow ballast")
	}
	observed, err := transitionCapacityObservation(capacity, PressureCollect, observedAt)
	if err != nil {
		return Pressure80Observation{}, err
	}
	if observed.UsedPercent != SoftWatermarkPercent ||
		observed.TotalBytes != normal.Capacity.TotalBytes ||
		observed.UsedBytes <= normal.Capacity.UsedBytes ||
		observed.AvailableBytes >= normal.Capacity.AvailableBytes {
		return Pressure80Observation{}, errors.New("pressure 80 capacity is not a contiguous collect transition")
	}
	return Pressure80Observation{
		Schema: Pressure80ObservationSchema, BallastFenceAt: ballastFence.UTC(),
		PriorCapacityObservedAt: normal.Capacity.ObservedAt, Capacity: observed,
	}, nil
}

func (collector *CycleCollector) cancel() {
	collector.mu.Lock()
	collector.finished = true
	collector.observation = CycleObservation{}
	collector.cycle = nil
	collector.readyCycle = nil
	collector.mu.Unlock()
}

func (collector *CycleCollector) finishLocked(value CycleObservation, err error) {
	if collector.finished {
		return
	}
	collector.finished = true
	collector.done <- cycleObservationResult{value: value, err: err}
}

func transitionCapacityObservation(
	capacity Capacity,
	want Pressure,
	observedAt time.Time,
) (TransitionCapacityObservation, error) {
	if observedAt.IsZero() || capacity.Pressure != want || capacity.TotalBytes <= 0 ||
		capacity.AvailableBytes < 0 || capacity.AvailableBytes > capacity.TotalBytes ||
		capacity.UsedBytes != capacity.TotalBytes-capacity.AvailableBytes ||
		capacity.ProjectedBytes != capacity.UsedBytes ||
		capacity.UsedPercent != percentCeiling(capacity.UsedBytes, capacity.TotalBytes) ||
		!pressureMatchesCapacity(want, capacity.UsedPercent) {
		return TransitionCapacityObservation{}, errors.New("lifecycle transition capacity is invalid")
	}
	return TransitionCapacityObservation{
		Completeness: Exact, Pressure: capacity.Pressure,
		TotalBytes: capacity.TotalBytes, AvailableBytes: capacity.AvailableBytes,
		UsedBytes: capacity.UsedBytes, ProjectedBytes: capacity.ProjectedBytes,
		UsedPercent: capacity.UsedPercent, ObservedAt: observedAt,
	}, nil
}

func pressureMatchesCapacity(pressure Pressure, usedPercent int) bool {
	switch pressure {
	case PressureNormal:
		return usedPercent < SoftWatermarkPercent
	case PressureCollect:
		return usedPercent >= SoftWatermarkPercent && usedPercent < HardWatermarkPercent
	case PressureRefuse:
		return usedPercent >= ResumeWatermarkPercent
	default:
		return false
	}
}

func addCycleTotal(total *uint64, value uint64) bool {
	if value > math.MaxUint64-*total {
		return false
	}
	*total += value
	return true
}

func cloneCycleObservation(value CycleObservation) CycleObservation {
	value.Owners = append([]CycleOwnerObservation(nil), value.Owners...)
	return value
}
