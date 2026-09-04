package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestCycleCollectorUsesSerialCallbacksForZeroReadPressureEvidence(t *testing.T) {
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
	runnerCtx, cancelRunner := context.WithCancel(readCtx)
	defer cancelRunner()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		Run(
			runnerCtx, controller, gate, time.Hour, time.Millisecond,
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
	pressure90Fence := clock()
	capacityMu.Lock()
	used = 900
	capacityMu.Unlock()
	refuse, err := collector.ReadPressure90Refusal(readCtx, gate, pressure90Fence)
	if err != nil {
		t.Fatal(err)
	}
	if refuse.Schema != Pressure90ObservationSchema ||
		refuse.Capacity.Pressure != PressureRefuse || refuse.Capacity.UsedPercent != 90 ||
		refuse.Capacity.TotalBytes != 1_000 ||
		refuse.PriorCapacityObservedAt != collect.Capacity.ObservedAt ||
		refuse.Capacity.ObservedAt.Before(refuse.BallastFenceAt) {
		t.Fatalf("pressure 90 observation = %+v", refuse)
	}
	if _, secondErr := collector.ReadPressure90Refusal(readCtx, gate, clock()); secondErr == nil {
		t.Fatal("pressure 90 observation was repeated")
	}
	pressure75Fence := clock()
	capacityMu.Lock()
	used = 750
	capacityMu.Unlock()
	target75, err := collector.ReadPressure75Refusal(readCtx, gate, pressure75Fence)
	if err != nil {
		t.Fatal(err)
	}
	if target75.Schema != Pressure75ObservationSchema ||
		target75.Capacity.Pressure != PressureRefuse || target75.Capacity.UsedPercent != 75 ||
		target75.Capacity.TotalBytes != 1_000 ||
		target75.PriorCapacityObservedAt != refuse.Capacity.ObservedAt ||
		target75.Capacity.ObservedAt.Before(target75.BallastFenceAt) {
		t.Fatalf("pressure 75 observation = %+v", target75)
	}
	if _, secondErr := collector.ReadPressure75Refusal(readCtx, gate, clock()); secondErr == nil {
		t.Fatal("pressure 75 observation was repeated")
	}

	cancelRunner()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("lifecycle runner did not stop")
	}
	capacityMu.Lock()
	used = 700
	capacityMu.Unlock()
	recoveryFence := clock()
	recoveryResult := make(chan observed, 1)
	go func() {
		value, observeErr := collector.AwaitPressure75Recovery(readCtx, gate, recoveryFence)
		recoveryResult <- observed{value: value, err: observeErr}
	}()
	for !collectorRecoveryStarted(collector) {
		time.Sleep(time.Millisecond)
	}
	for cycle := range 2 {
		for index, owner := range owners {
			completeness := Exact
			if owner.Name() == JobOwner {
				completeness = LowerBound
			}
			collector.ObserveOwner(OwnerResult{
				Owner: owner.Name(), AttemptedAt: clock(), Completeness: completeness,
				CycleStart: index == 0, CycleComplete: index == len(owners)-1,
				More: cycle == 1 && owner.Name() == JobOwner,
			})
			capacity, capacityErr := gate.Check(readCtx, 0)
			collector.ObserveCapacity(capacity, capacityErr)
		}
	}
	var recovery CycleObservation
	select {
	case got := <-recoveryResult:
		if got.err != nil {
			t.Fatal(got.err)
		}
		recovery = got.value
	case <-time.After(time.Second):
		t.Fatal("pressure 75 recovery cycle was not observed")
	}
	job := -1
	for index := range recovery.Owners {
		if recovery.Owners[index].Name == JobOwner {
			job = index
			break
		}
	}
	if recovery.Schema != CycleObservationSchema || recovery.OwnerTurns != 2*uint64(len(owners)) ||
		recovery.Capacity.Pressure != PressureNormal || recovery.Capacity.UsedPercent != 70 ||
		job < 0 || !recovery.Owners[job].Backlog {
		t.Fatalf("pressure 75 recovery cycle = %+v", recovery)
	}
	final75, err := collector.ReadPressure75Normal(readCtx, gate)
	if err != nil {
		t.Fatal(err)
	}
	if final75.Schema != Pressure75RecoveryObservationSchema ||
		final75.Capacity.Pressure != PressureNormal || final75.Capacity.UsedPercent != 70 ||
		final75.Capacity.TotalBytes != 1_000 ||
		final75.PriorCapacityObservedAt != recovery.Capacity.ObservedAt {
		t.Fatalf("pressure 75 final observation = %+v", final75)
	}
	if _, secondErr := collector.ReadPressure75Normal(readCtx, gate); secondErr == nil {
		t.Fatal("pressure 75 final observation was repeated")
	}
	if counts, accountingErr := ledger.Finish(); accountingErr != nil || counts != (readaccounting.Counts{}) {
		t.Fatalf("pressure observations charged native reads: %+v, %v", counts, accountingErr)
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

func TestCycleCollectorAwaitFreshAllowsDurableJobBacklog(t *testing.T) {
	owners := testCycleObservationOwners()
	collector, err := NewCycleCollector(owners, MaxCycleObservationTurns)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	var tick time.Duration
	collector.now = func() time.Time {
		tick++
		return base.Add(tick)
	}
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	type observed struct {
		value CycleObservation
		err   error
	}
	result := make(chan observed, 1)
	go func() {
		value, observeErr := collector.AwaitFresh(ctx)
		result <- observed{value: value, err: observeErr}
	}()
	for !collectorStarted(collector) {
		time.Sleep(time.Millisecond)
	}
	for index, owner := range owners {
		completeness := Exact
		more := false
		if owner.Name() == JobOwner {
			completeness, more = LowerBound, true
		}
		collector.ObserveOwner(OwnerResult{
			Owner: owner.Name(), AttemptedAt: collector.now(), Completeness: completeness, More: more,
			CycleStart: index == 0, CycleComplete: index == len(owners)-1,
		})
		collector.ObserveCapacity(Capacity{
			TotalBytes: 1_000, AvailableBytes: 300, UsedBytes: 700,
			ProjectedBytes: 700, UsedPercent: 70, Pressure: PressureNormal,
		}, nil)
	}
	got := <-result
	if got.err != nil {
		t.Fatal(got.err)
	}
	job := -1
	for index := range got.value.Owners {
		if got.value.Owners[index].Name == JobOwner {
			job = index
			break
		}
	}
	if FreshCycleReportCalls != 1 || got.value.Schema != CycleObservationSchema ||
		got.value.OwnerTurns != uint64(len(owners)) || job < 0 || !got.value.Owners[job].Backlog {
		t.Fatalf("fresh lifecycle cycle = %+v", got.value)
	}
	if _, repeatErr := collector.AwaitFresh(ctx); repeatErr == nil {
		t.Fatal("fresh lifecycle observation was repeated")
	}
	if counts, accountingErr := ledger.Finish(); accountingErr != nil || counts != (readaccounting.Counts{}) {
		t.Fatalf("fresh lifecycle observation charged native reads: %+v, %v", counts, accountingErr)
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

func TestPressure90ObservationRequiresSameGateAndRedactsProbeFailure(t *testing.T) {
	base := time.Now().UTC()
	priorGate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		return Capacity{}, errors.New("unused")
	})
	collector := &CycleCollector{
		now: func() time.Time { return base.Add(2 * time.Nanosecond) },
		pressure80: Pressure80Observation{
			Schema: Pressure80ObservationSchema,
			Capacity: TransitionCapacityObservation{
				Completeness: Exact, Pressure: PressureCollect,
				TotalBytes: 1_000, AvailableBytes: 200, UsedBytes: 800,
				ProjectedBytes: 800, UsedPercent: 80, ObservedAt: base,
			},
		},
		pressureGate: priorGate,
	}
	probes := 0
	otherGate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		probes++
		return Capacity{}, errors.New("private probe failure")
	})
	if _, err := collector.ReadPressure90Refusal(
		t.Context(), otherGate, base.Add(time.Nanosecond),
	); err == nil || err.Error() != "pressure 90 observation does not follow pressure 80" || probes != 0 {
		t.Fatalf("different-gate result = %v, probes = %d", err, probes)
	}

	collector.refuseAttempted = false
	collector.pressureGate = otherGate
	if _, err := collector.ReadPressure90Refusal(
		t.Context(), otherGate, base.Add(time.Nanosecond),
	); err == nil || err.Error() != "pressure 90 capacity observation failed" || probes != 1 {
		t.Fatalf("probe-failure result = %v, probes = %d", err, probes)
	}
}

func TestPressure90ObservationRefusesCancellationDuringCapacityProbe(t *testing.T) {
	base := time.Now().UTC()
	ctx, cancel := context.WithCancel(t.Context())
	gate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		cancel()
		return Capacity{TotalBytes: 1_000, AvailableBytes: 100, UsedBytes: 900}, nil
	})
	collector := &CycleCollector{
		now: func() time.Time { return base.Add(2 * time.Nanosecond) },
		pressure80: Pressure80Observation{
			Schema: Pressure80ObservationSchema,
			Capacity: TransitionCapacityObservation{
				Completeness: Exact, Pressure: PressureCollect,
				TotalBytes: 1_000, AvailableBytes: 200, UsedBytes: 800,
				ProjectedBytes: 800, UsedPercent: 80, ObservedAt: base,
			},
		},
		pressureGate: gate,
	}
	if _, err := collector.ReadPressure90Refusal(
		ctx, gate, base.Add(time.Nanosecond),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("pressure-90 observation error = %v, want context cancellation", err)
	}
}

func TestPressure75NormalRequiresCompletedRecoveryCycle(t *testing.T) {
	base := time.Now().UTC()
	probes := 0
	gate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		probes++
		return Capacity{TotalBytes: 1_000, AvailableBytes: 300, UsedBytes: 700}, nil
	})
	collector := &CycleCollector{
		now: func() time.Time { return base.Add(time.Nanosecond) },
		observation: CycleObservation{
			Schema: CycleObservationSchema,
			Capacity: TransitionCapacityObservation{
				Completeness: Exact, Pressure: PressureNormal,
				TotalBytes: 1_000, AvailableBytes: 300, UsedBytes: 700,
				ProjectedBytes: 700, UsedPercent: 70, ObservedAt: base,
			},
		},
		pressureGate: gate,
	}
	if _, err := collector.ReadPressure75Normal(t.Context(), gate); err == nil ||
		err.Error() != "pressure 75 normal observation does not follow recovery" || probes != 0 {
		t.Fatalf("pre-recovery normal result = %v, probes = %d", err, probes)
	}
}

func TestPressure75NormalRefusesRecoveryCanceledDuringProbe(t *testing.T) {
	base := time.Now().UTC()
	started := make(chan struct{})
	release := make(chan struct{})
	gate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		close(started)
		<-release
		return Capacity{TotalBytes: 1_000, AvailableBytes: 300, UsedBytes: 700}, nil
	})
	collector := &CycleCollector{
		now: func() time.Time { return base.Add(time.Nanosecond) },
		observation: CycleObservation{
			Schema: CycleObservationSchema,
			Capacity: TransitionCapacityObservation{
				Completeness: Exact, Pressure: PressureNormal,
				TotalBytes: 1_000, AvailableBytes: 300, UsedBytes: 700,
				ProjectedBytes: 700, UsedPercent: 70, ObservedAt: base,
			},
		},
		pressureGate: gate, recoveryComplete: true,
	}
	result := make(chan error, 1)
	go func() {
		_, err := collector.ReadPressure75Normal(t.Context(), gate)
		result <- err
	}()
	<-started
	collector.cancel()
	close(release)
	if err := <-result; err == nil ||
		err.Error() != "pressure 75 normal observation does not follow recovery" {
		t.Fatalf("canceled recovery normal result = %v", err)
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

func collectorRecoveryStarted(collector *CycleCollector) bool {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return collector.recoveryMode && !collector.finished
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
