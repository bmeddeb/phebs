package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestCycleCollectorUsesSerialCallbacksForZeroReadPressure80Evidence(t *testing.T) {
	owners := testCycleObservationOwners()
	cursorStore := newMemoryCursorStore()
	cursorStore.values[rotationCursorKey] = RelationshipOwner
	controller, err := NewController(cursorStore, owners...)
	if err != nil {
		t.Fatal(err)
	}
	collector, err := NewCycleCollector(owners, 4_096)
	if err != nil {
		t.Fatal(err)
	}

	base := time.UnixMilli(1_700_000_000_000).UTC()
	var clockMu sync.Mutex
	var tick time.Duration
	armed := make(chan struct{})
	var armOnce sync.Once
	clock := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		tick++
		armOnce.Do(func() { close(armed) })
		return base.Add(tick)
	}
	controller.now = clock
	collector.now = clock

	var capacityMu sync.Mutex
	used := int64(700)
	capacityChecks := 0
	gate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		capacityMu.Lock()
		defer capacityMu.Unlock()
		capacityChecks++
		observedUsed := used
		if capacityChecks == 7 {
			observedUsed = 800
		}
		return Capacity{
			TotalBytes: 1_000, AvailableBytes: 1_000 - observedUsed, UsedBytes: observedUsed,
		}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	readCtx, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	type observed struct {
		value CycleObservation
		err   error
	}
	result := make(chan observed, 1)
	go func() {
		value, observeErr := collector.AwaitNormal(readCtx)
		result <- observed{value: value, err: observeErr}
	}()
	<-armed
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		Run(
			ctx, controller, gate, time.Hour, time.Millisecond,
			collector.ObserveOwner, collector.ObserveCapacity,
		)
	}()

	var normal CycleObservation
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		normal = got.value
	case <-time.After(time.Second):
		t.Fatal("fresh lifecycle cycle was not observed")
	}
	if normal.Schema != CycleObservationSchema || normal.OwnerTurns != 37 ||
		len(normal.Owners) != len(owners) || normal.Capacity.Pressure != PressureNormal ||
		normal.Capacity.UsedPercent != 70 {
		t.Fatalf("normal cycle = %+v", normal)
	}
	for index, owner := range normal.Owners {
		if owner.Name != owners[index].Name() || owner.State != "ok" ||
			owner.AttemptedAt.UnixMilli() != normal.FenceAt.UnixMilli() {
			t.Fatalf("owner %d = %+v", index, owner)
		}
	}

	// The returned slice and capacity are caller-owned; the second report uses
	// the collector's retained copy.
	normal.Owners[0].Name = "changed"
	normal.Capacity.TotalBytes = 1
	ballastFence := clock()
	capacityMu.Lock()
	used = 800
	capacityMu.Unlock()
	collect, err := collector.ReadPressure80Collect(readCtx, gate, ballastFence)
	if err != nil {
		t.Fatal(err)
	}
	if collect.Schema != Pressure80ObservationSchema ||
		collect.Capacity.Pressure != PressureCollect || collect.Capacity.UsedPercent != 80 ||
		collect.Capacity.TotalBytes != 1_000 ||
		!collect.PriorCapacityObservedAt.Before(collect.BallastFenceAt) ||
		!collect.BallastFenceAt.Before(collect.Capacity.ObservedAt) {
		t.Fatalf("pressure 80 observation = %+v", collect)
	}
	if _, secondErr := collector.ReadPressure80Collect(readCtx, gate, clock()); secondErr == nil {
		t.Fatal("pressure 80 observation was repeated")
	}
	if counts, accountingErr := ledger.Finish(); accountingErr != nil || counts != (readaccounting.Counts{}) {
		t.Fatalf("pressure observations charged native reads: %+v, %v", counts, accountingErr)
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("lifecycle runner did not stop")
	}
}

func TestCycleCollectorFailsClosedAtItsTurnLimit(t *testing.T) {
	owners := []Owner{
		StaticOwner{OwnerName: "alpha", Completeness: Exact},
		StaticOwner{OwnerName: JobOwner, Completeness: LowerBound},
	}
	if _, err := NewCycleCollector(owners, MaxCycleObservationTurns+1); err == nil {
		t.Fatal("collector accepted a turn limit above its fixed maximum")
	}
	collector, err := NewCycleCollector(owners, 2)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	collector.now = func() time.Time { return base }
	result := make(chan error, 1)
	go func() {
		_, observeErr := collector.AwaitNormal(t.Context())
		result <- observeErr
	}()
	for !collectorStarted(collector) {
		time.Sleep(time.Millisecond)
	}
	collector.ObserveOwner(OwnerResult{
		Owner: "alpha", AttemptedAt: base,
		CycleStart: true, Completeness: Exact,
	})
	collector.ObserveCapacity(Capacity{
		TotalBytes: 1_000, AvailableBytes: 500, UsedBytes: 500,
		ProjectedBytes: 500, UsedPercent: 50, Pressure: PressureNormal,
	}, nil)
	collector.ObserveOwner(OwnerResult{
		Owner: JobOwner, AttemptedAt: base,
		CycleComplete: true, Completeness: LowerBound, More: true,
	})
	collector.ObserveCapacity(Capacity{
		TotalBytes: 1_000, AvailableBytes: 500, UsedBytes: 500,
		ProjectedBytes: 500, UsedPercent: 50, Pressure: PressureNormal,
	}, nil)
	select {
	case got := <-result:
		t.Fatalf("durable-job backlog completed the observation: %v", got)
	case <-time.After(10 * time.Millisecond):
	}
	collector.ObserveOwner(OwnerResult{
		Owner: "alpha", AttemptedAt: base,
		CycleStart: true, Completeness: Exact,
	})
	select {
	case observeErr := <-result:
		if observeErr == nil {
			t.Fatalf("turn limit error = %v", observeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("turn limit did not fail the observation")
	}
}

func TestCycleCollectorCancellationWinsBufferedCompletion(t *testing.T) {
	owners := []Owner{StaticOwner{OwnerName: "alpha", Completeness: Exact}}
	collector, err := NewCycleCollector(owners, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, observeErr := collector.AwaitNormal(ctx)
		result <- observeErr
	}()
	for !collectorStarted(collector) {
		time.Sleep(time.Millisecond)
	}
	collector.mu.Lock()
	collector.observation = CycleObservation{Schema: CycleObservationSchema}
	cancel()
	collector.finishLocked(collector.observation, nil)
	collector.mu.Unlock()
	if observeErr := <-result; !errors.Is(observeErr, context.Canceled) {
		t.Fatalf("observation error = %v, want context cancellation", observeErr)
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if collector.observation.Schema != "" {
		t.Fatal("canceled observation remained available")
	}
}

func TestCycleCollectorRefusesOwnerlessCallback(t *testing.T) {
	collector, err := NewCycleCollector(
		[]Owner{StaticOwner{OwnerName: "alpha", Completeness: Exact}}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, observeErr := collector.AwaitNormal(t.Context())
		result <- observeErr
	}()
	for !collectorStarted(collector) {
		time.Sleep(time.Millisecond)
	}
	collector.ObserveOwner(OwnerResult{Err: errors.New("private rotation failure")})
	if observeErr := <-result; observeErr == nil || observeErr.Error() != "lifecycle cycle owner is absent" {
		t.Fatalf("ownerless callback error = %v", observeErr)
	}
}

func TestCycleCollectorRefusesUnpairedCapacityCallback(t *testing.T) {
	collector, err := NewCycleCollector(
		[]Owner{StaticOwner{OwnerName: "alpha", Completeness: Exact}}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	collector.now = func() time.Time { return base }
	result := make(chan error, 1)
	go func() {
		_, observeErr := collector.AwaitNormal(t.Context())
		result <- observeErr
	}()
	for !collectorStarted(collector) {
		time.Sleep(time.Millisecond)
	}
	collector.ObserveOwner(OwnerResult{
		Owner: "alpha", AttemptedAt: base, CycleStart: true, CycleComplete: true,
		Completeness: Exact, More: true,
	})
	capacity := Capacity{
		TotalBytes: 1_000, AvailableBytes: 500, UsedBytes: 500,
		ProjectedBytes: 500, UsedPercent: 50, Pressure: PressureNormal,
	}
	collector.ObserveCapacity(capacity, nil)
	collector.ObserveCapacity(capacity, nil)
	if observeErr := <-result; observeErr == nil ||
		observeErr.Error() != "lifecycle cycle callback order is invalid" {
		t.Fatalf("unpaired capacity error = %v", observeErr)
	}
}

func TestPressure80ObservationRefusesCancellationDuringCapacityProbe(t *testing.T) {
	base := time.Now().UTC()
	collector := &CycleCollector{
		now: func() time.Time { return base.Add(2 * time.Nanosecond) },
		observation: CycleObservation{
			Schema: CycleObservationSchema,
			Capacity: TransitionCapacityObservation{
				Completeness: Exact, Pressure: PressureNormal,
				TotalBytes: 1_000, AvailableBytes: 300, UsedBytes: 700,
				ProjectedBytes: 700, UsedPercent: 70, ObservedAt: base,
			},
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	gate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		cancel()
		return Capacity{TotalBytes: 1_000, AvailableBytes: 200, UsedBytes: 800}, nil
	})
	if _, err := collector.ReadPressure80Collect(
		ctx, gate, base.Add(time.Nanosecond),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("pressure-80 observation error = %v, want context cancellation", err)
	}
}

func TestTransitionCapacityObservationRejectsMislabeledPressure(t *testing.T) {
	_, err := transitionCapacityObservation(Capacity{
		TotalBytes: 1_000, AvailableBytes: 200, UsedBytes: 800,
		ProjectedBytes: 800, UsedPercent: 80, Pressure: PressureNormal,
	}, PressureNormal, time.Now())
	if err == nil {
		t.Fatal("normal capacity accepted the collect watermark")
	}
}

func collectorStarted(collector *CycleCollector) bool {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return collector.started
}

func testCycleObservationOwners() []Owner {
	names := []string{
		CatalogOwner, CatalogV3Owner, JobOwner, GenerationScheduleOwner,
		InvestigationOwner, ObservationOwner, ObservationV2Owner, PartialStageOwner,
		ProofOwner, ReaderOwner, RelationshipOwner, RelationshipV3Owner,
		ResolverOwner, SearchOwner, TombstoneOwner, SourceOwner,
	}
	owners := make([]Owner, 0, len(names))
	for _, name := range names {
		completeness := Exact
		if name == JobOwner {
			completeness = LowerBound
		}
		owners = append(owners, StaticOwner{OwnerName: name, Completeness: completeness})
	}
	return owners
}
