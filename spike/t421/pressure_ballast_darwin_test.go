//go:build darwin

package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestExecutionPressureBallastSize(t *testing.T) {
	geometry, err := expectedExecutionPressureGeometry(Plan{SafetyEnvelope: frozenSafetyEnvelope()}, ExecutionHost{
		PressureTotalDiskBytes: 96 << 30, PressureAllocationUnitBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []uint64{8 << 30, 40 << 30, 68 << 30} {
		before := executionPressureBallastSample{Used: base, Available: 96<<30 - base}
		for _, target := range geometry.Targets {
			size, err := pressureBallastSize(before, target)
			if err != nil || size > 80<<30 || size%4096 != 0 ||
				!withinTolerance(base+size, target.TargetUsedBytes, 4096) ||
				usedPercentCeiling(base+size, 96<<30) != target.TargetUsedPercent {
				t.Fatalf("base %d target %d: size=%d error=%v", base, target.TargetUsedPercent, size, err)
			}
			before = executionPressureBallastSample{Used: base + size, Available: 96<<30 - base - size, Allocated: size}
		}
	}
	for _, test := range []struct {
		name   string
		before executionPressureBallastSample
		target PressureTargetGeometry
	}{
		{"overflow", executionPressureBallastSample{Used: ^uint64(0)}, geometry.Targets[0]},
		{"noncontiguous", executionPressureBallastSample{Used: 8 << 30}, geometry.Targets[0]},
		{"ballast_exceeds_used", executionPressureBallastSample{Used: 8 << 30, Available: 88 << 30, Allocated: 9 << 30}, geometry.Targets[0]},
		{"already_above_target", executionPressureBallastSample{Used: 90 << 30, Available: 6 << 30}, geometry.Targets[0]},
		{"unknown_action", executionPressureBallastSample{Used: 8 << 30, Available: 88 << 30}, PressureTargetGeometry{TargetUsedBytes: 80 << 30, Action: "other"}},
		{"remove_would_grow", executionPressureBallastSample{Used: 8 << 30, Available: 88 << 30}, geometry.Targets[2]},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pressureBallastSize(test.before, test.target); err == nil {
				t.Fatal("invalid size admitted")
			}
		})
	}
	before := executionPressureBallastSample{Used: 8 << 30, Allocated: 4096}
	for _, test := range []struct {
		name   string
		after  executionPressureBallastSample
		action string
		want   bool
	}{
		{"real_growth", executionPressureBallastSample{Used: before.Used + 8192, Allocated: 12288}, "add", true},
		{"one_unit_tolerance", executionPressureBallastSample{Used: before.Used + 12288, Allocated: 12288}, "add", true},
		{"excess_metadata", executionPressureBallastSample{Used: before.Used + 16384, Allocated: 12288}, "add", false},
		{"sparse_hole", executionPressureBallastSample{Used: before.Used + 8192, Allocated: 4096}, "add", false},
		{"real_shrink", executionPressureBallastSample{Used: before.Used - 4096}, "remove", true},
		{"unreleased_blocks", executionPressureBallastSample{Used: before.Used}, "remove", false},
		{"unknown", executionPressureBallastSample{Used: before.Used - 4096}, "other", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := pressureBallastDeltaMatches(test.action, before, test.after); got != test.want {
				t.Fatalf("delta matched=%t, want %t", got, test.want)
			}
		})
	}
}

func TestExecutionPressureBallastContinuation(t *testing.T) {
	prior := executionPressureBallastSample{Used: 90 << 30, Available: 6 << 30, Allocated: 40 << 30}
	drifted := executionPressureBallastSample{Used: prior.Used + 4096, Available: prior.Available - 4096, Allocated: prior.Allocated}
	if !pressureBallastAllocationUnchanged(prior, drifted) {
		t.Fatal("owned capacity drift was treated as ballast drift")
	}
	shrunk := executionPressureBallastSample{Used: prior.Used, Available: prior.Available, Allocated: prior.Allocated - 4096}
	if pressureBallastAllocationUnchanged(prior, shrunk) {
		t.Fatal("ballast allocation drift was treated as owned capacity drift")
	}
	grown := executionPressureBallastSample{Used: prior.Used, Available: prior.Available, Allocated: prior.Allocated + 4096}
	if pressureBallastAllocationUnchanged(prior, grown) {
		t.Fatal("ballast allocation growth was treated as owned capacity drift")
	}
}

func TestExecutionPressureBallastFirstTargetRejoinsQuietBaseline(t *testing.T) {
	quiet := executionPressureBallastSample{Used: 47662125056, Available: 96<<30 - 47662125056}
	spike := quiet
	spike.Used += 8192
	spike.Available -= 8192
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	calls := 0
	got, observations, err := settleExecutionPressureBallastBaseline(ctx, quiet, func(context.Context) (executionPressureBallastSample, error) {
		calls++
		if calls == 1 {
			return spike, nil
		}
		return quiet, nil
	})
	if err != nil || got != quiet || calls != 2 || observations.Samples != 2 || observations.First != spike || observations.Last != quiet {
		t.Fatalf("first target did not rejoin the accepted baseline: got=%+v observations=%+v calls=%d error=%v", got, observations, calls, err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	_, observations, err = settleExecutionPressureBallastBaseline(ctx, quiet, func(context.Context) (executionPressureBallastSample, error) {
		cancel()
		return spike, nil
	})
	if !errors.Is(err, errPressureVolume) || observations.Samples != 0 {
		t.Fatalf("canceled baseline settlement admitted a transient: observations=%+v error=%v", observations, err)
	}
}

func TestExecutionPressureBallastSettlement(t *testing.T) {
	if pressureBallastSettleCadence != 50*time.Millisecond || pressureBallastSettleLimit != 30*time.Second {
		t.Fatal("unexpected pressure ballast settlement bounds")
	}
	type observation struct {
		value   executionPressureBallastSample
		logical uint64
		err     error
	}
	exact := observation{value: executionPressureBallastSample{Used: 12 << 30, Available: 84 << 30, Allocated: 8 << 30}, logical: 8 << 30}
	priorAllocated := uint64(9 << 30)
	staleAllocation := exact
	staleAllocation.value.Allocated = priorAllocated
	staleCapacity := exact
	staleCapacity.value.Used += 4096
	staleCapacity.value.Available -= 4096
	invalidAllocation := exact
	invalidAllocation.value.Allocated -= 4096
	diagnosticOnly := exact
	diagnosticOnly.value.FreeBlocks = ^uint64(0)
	for _, test := range []struct {
		name             string
		observations     []observation
		cancelAfterFirst bool
		wantCalls        int
		wantError        bool
	}{
		{name: "immediate", observations: []observation{exact}, wantCalls: 1},
		{name: "bfree_is_diagnostic_only", observations: []observation{diagnosticOnly}, wantCalls: 1},
		{name: "allocation_settles", observations: []observation{staleAllocation, exact}, wantCalls: 2},
		{name: "capacity_settles", observations: []observation{staleCapacity, exact}, wantCalls: 2},
		{name: "logical_size_drift", observations: []observation{{value: exact.value, logical: exact.logical + 4096}}, wantCalls: 1, wantError: true},
		{name: "allocation_below_size", observations: []observation{invalidAllocation}, wantCalls: 1, wantError: true},
		{name: "custody_error", observations: []observation{{value: exact.value, logical: exact.logical, err: errors.New("custody")}}, wantCalls: 1, wantError: true},
		{name: "custody_error_after_valid_prefix", observations: []observation{staleCapacity, {err: errors.New("custody")}}, wantCalls: 2, wantError: true},
		{name: "canceled", observations: []observation{staleCapacity}, cancelAfterFirst: true, wantCalls: 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			calls := 0
			got, observations, err := settleExecutionPressureBallast(ctx, exact.logical, priorAllocated, func(context.Context) (executionPressureBallastSample, uint64, error) {
				index := min(calls, len(test.observations)-1)
				calls++
				if test.cancelAfterFirst && calls == 1 {
					cancel()
				}
				value := test.observations[index]
				return value.value, value.logical, value.err
			}, func(value executionPressureBallastSample) bool {
				return value.Used == exact.value.Used && value.Available == exact.value.Available && value.Allocated == exact.value.Allocated
			})
			if errors.Is(err, errPressureVolume) != test.wantError || calls != test.wantCalls {
				t.Fatalf("settlement calls=%d error=%v, want calls=%d error=%t", calls, err, test.wantCalls, test.wantError)
			}
			if !test.wantError && got != test.observations[len(test.observations)-1].value {
				t.Fatalf("settled sample lost observed fields: %+v", got)
			}
			wantSamples := uint64(test.wantCalls)
			if test.wantError && !test.cancelAfterFirst {
				wantSamples-- // Invalid final observations must not pollute extrema.
			}
			if observations.Samples != wantSamples || wantSamples > 0 &&
				(observations.First != test.observations[0].value || observations.Last != test.observations[wantSamples-1].value) {
				t.Fatalf("lost or invented settlement observations: %+v", observations)
			}
		})
	}
}

func TestExecutionPressureBallastAnchoredStability(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	started := time.Now()
	var calls uint64
	result, err := waitExecutionPressureBallastQuiet(ctx, 120*time.Millisecond, 4096, 8<<30, 68<<30,
		func(context.Context) (executionPressureBallastSample, uint64, error) {
			calls++
			used := uint64(20 << 30)
			if calls == 2 || calls == 3 {
				used -= 12 << 10
			}
			value := executionPressureBallastSample{Used: used, Available: 96<<30 - used, Allocated: 4 << 30}
			return value, value.Allocated, nil
		})
	if err != nil || calls < 4 || result.Samples != calls || result.UsedChanges != 2 || result.MaxUsedStep != 12<<10 || time.Since(started) < 120*time.Millisecond {
		t.Fatalf("anchored stability rejected a reversible excursion: calls=%d result=%+v elapsed=%s error=%v", calls, result, time.Since(started), err)
	}
}

func TestExecutionPressureBallastAnchoredStabilityRefusesPersistentShift(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var calls uint64
	result, err := waitExecutionPressureBallastQuiet(ctx, 120*time.Millisecond, 4096, 8<<30, 68<<30,
		func(context.Context) (executionPressureBallastSample, uint64, error) {
			calls++
			if calls == 5 {
				cancel()
			}
			used := uint64(20 << 30)
			if calls > 1 {
				used -= 8 << 10
			}
			value := executionPressureBallastSample{Used: used, Available: 96<<30 - used, Allocated: 4 << 30}
			return value, value.Allocated, nil
		})
	if err == nil || calls != 5 || result.Samples != calls-1 {
		t.Fatalf("persistent anchor shift passed: calls=%d result=%+v error=%v", calls, result, err)
	}
}

func TestExecutionPressureBallastAnchoredStabilityRefusesCanceledEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var calls uint64
	result, err := waitExecutionPressureBallastQuiet(ctx, 120*time.Millisecond, 4096, 8<<30, 68<<30,
		func(context.Context) (executionPressureBallastSample, uint64, error) {
			calls++
			if calls == 2 {
				time.Sleep(130 * time.Millisecond)
				cancel()
			}
			value := executionPressureBallastSample{Used: 20 << 30, Available: 76 << 30, Allocated: 4 << 30}
			return value, value.Allocated, nil
		})
	if err == nil || calls != 2 || result.Samples != 1 {
		t.Fatalf("canceled endpoint passed: calls=%d result=%+v error=%v", calls, result, err)
	}
}

func TestExecutionPressureBallastAnchoredStabilityRefusesAllocationDrift(t *testing.T) {
	var calls uint64
	_, err := waitExecutionPressureBallastQuiet(t.Context(), time.Second, 4096, 8<<30, 68<<30,
		func(context.Context) (executionPressureBallastSample, uint64, error) {
			calls++
			allocated := uint64(4 << 30)
			if calls > 1 {
				allocated += 4096
			}
			return executionPressureBallastSample{Used: 20 << 30, Available: 76 << 30, Allocated: allocated}, allocated, nil
		})
	if err == nil || calls != 2 {
		t.Fatalf("allocation drift was not refused: calls=%d error=%v", calls, err)
	}
}

func TestExecutionPressureBallastAnchoredStabilityRefusesEnvelopeExcursion(t *testing.T) {
	var calls uint64
	_, err := waitExecutionPressureBallastQuiet(t.Context(), time.Second, 4096, 8<<30, 68<<30,
		func(context.Context) (executionPressureBallastSample, uint64, error) {
			calls++
			used := uint64(20 << 30)
			if calls == 2 {
				used = 80 << 30
			}
			value := executionPressureBallastSample{Used: used, Available: 96<<30 - used, Allocated: 4 << 30}
			return value, value.Allocated, nil
		})
	if err == nil || calls != 2 {
		t.Fatalf("out-of-envelope excursion was not refused: calls=%d error=%v", calls, err)
	}
}

func TestExecutionPressureBallastSettlementSummary(t *testing.T) {
	for _, test := range []struct {
		name             string
		used, free       []uint64
		changes, maxStep uint64
		minFree, maxFree uint64
	}{
		{"observed_jump", []uint64{12288, 12288, 4096}, []uint64{10, 10, 12}, 1, 8192, 10, 12},
		{"observed_steps", []uint64{12288, 8192, 4096}, []uint64{10, 11, 12}, 2, 4096, 10, 12},
		{"bavail_moves_bfree_stays", []uint64{12288, 8192, 4096}, []uint64{10, 10, 10}, 2, 4096, 10, 10},
		{"nonmonotonic", []uint64{4096, 12288, 4096}, []uint64{12, 10, 12}, 2, 8192, 10, 12},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got executionPressureBallastSettlement
			var first, last executionPressureBallastSample
			for i, used := range test.used {
				last = executionPressureBallastSample{Used: used, Available: 96<<30 - used, Allocated: 4096, FreeBlocks: test.free[i]}
				if i == 0 {
					first = last
				}
				got.observe(last)
			}
			if got.Samples != uint64(len(test.used)) || got.UsedChanges != test.changes || got.MaxUsedStep != test.maxStep ||
				got.MinUsed != 4096 || got.MaxUsed != 12288 || got.MinFreeBlocks != test.minFree || got.MaxFreeBlocks != test.maxFree ||
				got.First != first || got.Last != last {
				t.Fatalf("wrong sampled summary: %+v", got)
			}
		})
	}
}

func TestExecutionPressureBallastRefusals(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled, t.Context()} {
		for _, v := range []*executionPressureVolume{nil, {}, {ready: true}, {ready: true, borrowed: true}} {
			if _, err := prepareExecutionPressureBallast(ctx, v); err == nil {
				t.Fatal("invalid volume issued ballast")
			}
		}
		for _, b := range []*executionPressureBallast{nil, {}, {volume: &executionPressureVolume{}}} {
			if _, err := b.nextTarget(ctx, nil, custodyByteSample{}, executionPressureBallastSample{}); err == nil {
				t.Fatal("invalid run issued mutation")
			}
			if _, err := b.remove(ctx, &ExecutionEpochOneRun{}); err == nil {
				t.Fatal("invalid run removed ballast")
			}
		}
	}
	v := &executionPressureVolume{ready: true, ballast: &executionPressureBallast{}}
	if v.removeEmpty(t.Context()) == nil || v.finishRehearsal(t.Context(), &ExecutionEpochOneRun{}) == nil {
		t.Fatal("outstanding ballast released volume")
	}
}

// A <=1-MiB native syscall fixture with a modeled run for early-refusal
// cleanup. It supplies no pressure/readiness evidence, never reaches nextTarget,
// and does not change the frozen geometry.
func TestExecutionPressureBallastOptionalNative(t *testing.T) {
	if os.Getenv("PHEBS_T422_BALLAST_NATIVE_REHEARSAL") != "1" {
		t.Skip("requires explicitly selected tiny APFS allocation gate")
	}
	requireExternalToolFrozenHost(t)
	parent, err := os.MkdirTemp("", "t422-ballast-syscall-")
	if err != nil {
		t.Fatal(err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retained on failure: %s", parent)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	v, err := prepareExecutionPressureVolume(ctx, parent)
	if v != nil {
		defer func() { _ = v.Close() }()
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(v.workspace.path, "pressure-ballast")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if file.Sync() != nil || v.workspace.file.Sync() != nil {
		t.Fatal("prepare native metadata baseline")
	}
	// Read the actual inode/capacity through the component sampler. This is
	// deliberately not a borrowed epoch or permission to use nextTarget.
	ballast := &executionPressureBallast{volume: v, file: file, info: info}
	before := uint64(0)
	var beforeRemoval executionPressureBallastSample
	for _, size := range []uint64{512 << 10, 1 << 20, 512 << 10, 0} {
		capacityBefore, err := ballast.sample()
		if err != nil {
			t.Fatal("native pre-resize capacity", err)
		}
		if size == 0 {
			beforeRemoval = capacityBefore
		}
		if err := resizeExecutionPressureBallast(ctx, file, before, size); err != nil {
			t.Fatalf("native resize %d to %d: %v", before, size, err)
		}
		var stat unix.Stat_t
		volume, err := inputCustodyVolume(file)
		if err != nil || volume != v.workspace.volume || unix.Fstat(int(file.Fd()), &stat) != nil ||
			stat.Size != int64(size) || stat.Blocks*512 != int64(size) {
			t.Fatalf("native allocation mismatch: size=%d stat=%+v error=%v", size, stat, err)
		}
		action := "add"
		if size < before {
			action = "remove"
		}
		var capacityAfter executionPressureBallastSample
		if action == "remove" {
			capacityAfter, _, err = settleExecutionPressureBallast(ctx, size, before, func(context.Context) (executionPressureBallastSample, uint64, error) {
				return ballast.observe()
			}, func(value executionPressureBallastSample) bool {
				return value.Allocated == size && pressureBallastDeltaMatches(action, capacityBefore, value)
			})
		} else {
			capacityAfter, err = ballast.sample()
		}
		t.Logf("native %s %d to %d: before=%+v after=%+v", action, before, size, capacityBefore, capacityAfter)
		if err != nil || !pressureBallastDeltaMatches(action, capacityBefore, capacityAfter) {
			t.Fatalf("native capacity delta exceeds unchanged 4096-byte tolerance: %v", err)
		}
		before = size
	}
	t.Run("modeled_phase_begin_refusal_closes_join", func(t *testing.T) {
		// Only volume/inode custody is native. The run prerequisites, fenced
		// control peer, and preflight recorder state are modeled; their mismatch
		// refuses pressure_80 before any pressure command or ballast mutation.
		admitted, _, err := newExecutionEventOrdinals().consumeFinalAdmission()
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		recorder, err := newExecutionPhaseEventRecorder(admitted, frozenPhaseOrder(), now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		flow := &ExecutionEpochOne{executionPhaseEvents: recorder}
		run := &ExecutionEpochOneRun{
			flow: flow, epoch: ExecutionEpochConfig{Epoch: 4}, pressureAllowed: true, healthy: true, warm: true,
			checkpointDone: make(chan struct{}), inspection: &executionEpochInspection{},
			control: modeledQueryReceiptControl(t, false), stop: make(chan struct{}),
			phaseDeadline: now.Add(time.Minute), lifetimeDeadline: now.Add(time.Minute),
		}
		close(run.checkpointDone)
		run.inspection.pressure.samples.Complete = true
		v.flow, v.borrowed, v.ballast = flow, true, ballast
		defer func() { v.flow, v.borrowed, v.ballast = nil, false, nil }()
		if err := run.Pressure(ctx, v); !errors.Is(err, ErrExecutionEpochOne) {
			t.Fatal("phase begin did not refuse", err)
		}
		if !recorder.failed || !run.pressureUsed || run.pressureCancel == nil || run.pressureDone == nil {
			t.Fatal("refusal did not reach the event guard after publishing the pressure join")
		}
		select {
		case <-run.pressureDone:
		default:
			t.Fatal("early refusal left finish waiting on the published pressure join")
		}
		select {
		case <-run.stop:
		default:
			t.Fatal("early refusal did not request stop")
		}
		if !errors.Is(run.err, ErrExecutionEpochOne) || run.inspection.pressure.samples.Complete ||
			!run.inspection.pressure.samples.Unavailable {
			t.Fatal("early refusal did not retain unavailable pressure state")
		}
		current, err := file.Stat()
		if err != nil || current.Size() != 0 || ballast.next != 0 || ballast.failed || ballast.removed {
			t.Fatal("early refusal changed the zero ballast", err)
		}
	})
	if file.Close() != nil {
		t.Fatal("close ballast")
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) || os.Remove(path) != nil || v.workspace.file.Sync() != nil {
		t.Fatal("exact ballast removal", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("ballast remains after unlink", err)
	}
	afterRemoval, err := ballast.capacity()
	t.Logf("native final removal: before=%+v after=%+v", beforeRemoval, afterRemoval)
	if err != nil || afterRemoval.Allocated != 0 || !pressureBallastDeltaMatches("remove", beforeRemoval, afterRemoval) {
		t.Fatal("post-unlink native capacity delta exceeds unchanged 4096-byte tolerance", err)
	}
	if err := v.removeEmpty(ctx); err != nil {
		t.Fatal("native empty detach", err)
	}
	if v.Close() != nil || os.Remove(filepath.Join(parent, ".t4013-operation.lock")) != nil || os.Remove(parent) != nil {
		t.Fatal("exact outer cleanup")
	}
}
