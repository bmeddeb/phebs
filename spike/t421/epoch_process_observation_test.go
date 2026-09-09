package t421

import (
	"context"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
)

func epochProcessFixtureRows() []t4013.NativeProcessRecord {
	return []t4013.NativeProcessRecord{
		{PID: 41, ParentPID: 1, RSSBytes: 4096, StartIdentity: "actual-test-birth", ObservedName: "phebs"},
		{PID: 42, ParentPID: 41, RSSBytes: 8192, StartIdentity: "actual-child-birth", ObservedName: "git"},
	}
}

func pauseEpochProcessTicker(meter *epochProcessObservation) {
	meter.stopOnce.Do(func() { close(meter.stop) })
	<-meter.done
}

func TestEpochProcessNames(t *testing.T) {
	tools := []dispatchadmission.ProductionToolBinding{{Role: "git", Path: "/private/tools/git"},
		{Role: "surreal", Path: "/private/tools/surreal"}, {Role: "zoekt-git-index", Path: "/private/tools/zoekt-git-index"}}
	names, err := epochProcessNames("/private/tools/phebs", tools)
	if err != nil || len(names) != 11 || names["git-unpack-objects"] != "" || names["git-unpack-objec"] != "git" || names["sh"] != "sh" {
		t.Fatal("native custody alias classification", names, err)
	}
	for _, test := range []struct {
		name, root string
		tools      []dispatchadmission.ProductionToolBinding
	}{
		{"renamed-root", "/private/tools/other", tools}, {"relative-root", "phebs", tools},
		{"missing-tool", "/private/tools/phebs", tools[:2]},
		{"wrong-image-name", "/private/tools/phebs", []dispatchadmission.ProductionToolBinding{{Role: "git", Path: "/private/sh"}, tools[1], tools[2]}},
		{"unknown-role", "/private/tools/phebs", []dispatchadmission.ProductionToolBinding{{Role: "go", Path: "/private/go"}, tools[1], tools[2]}},
		{"duplicate", "/private/tools/phebs", []dispatchadmission.ProductionToolBinding{tools[0], tools[0], tools[2]}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := epochProcessNames(test.root, test.tools); err == nil {
				t.Fatal("unbound table accepted")
			}
		})
	}
}

func TestEpochProcessInitialFailureRetainedWithoutRetry(t *testing.T) {
	for _, kind := range []string{"probe", "empty", "identity", "unknown", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if kind == "canceled" {
				cancel()
			}
			calls := 0
			meter, err := newEpochProcessObservation(ctx, 41, 2, "phebs", map[string]string{"phebs": "phebs", "git": "git"}, func() {},
				func(context.Context, int) ([]t4013.NativeProcessRecord, error) {
					calls++
					rows := epochProcessFixtureRows()
					switch kind {
					case "probe":
						return nil, errors.New("native denial")
					case "empty":
						return nil, nil
					case "identity":
						rows[0].StartIdentity = ""
					case "unknown":
						rows[1].ObservedName = "unknown"
					}
					return rows, nil
				})
			if err == nil || meter == nil {
				t.Fatal("initial refusal lost its owned prefix", err)
			}
			result, err := meter.close()
			wantCalls := 1
			if kind == "canceled" {
				wantCalls = 0
			}
			if err == nil || !result.Joined || len(result.Phases) != 1 || result.Phases[0].Observation.Available ||
				result.Phases[0].Observation.CompletedCensuses != 0 || calls != wantCalls {
				t.Fatal("initial refusal retried or manufactured a census", result, calls, err)
			}
			if err := validateNativeObservation(result.Phases[0].Observation); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEpochProcessRSSRefusalEverySampleBoundary(t *testing.T) {
	limit := frozenSafetyEnvelope().MaximumPeakRSSBytes
	if limit != 20<<30 {
		t.Fatal("source-frozen RSS threshold changed", limit)
	}
	for _, stage := range []string{"initial", "ticker", "ending-boundary", "starting-boundary", "final"} {
		t.Run(stage, func(t *testing.T) {
			overAt := int64(2)
			if stage == "initial" {
				overAt = 1
			}
			if stage == "starting-boundary" {
				overAt = 3
			}
			var calls, failures atomic.Int64
			stopped := make(chan struct{})
			meter, err := newEpochProcessObservation(t.Context(), 41, 2, "phebs", map[string]string{"phebs": "phebs", "git": "git"},
				func() {
					if failures.Add(1) == 1 {
						close(stopped)
					}
				},
				func(context.Context, int) ([]t4013.NativeProcessRecord, error) {
					rows := epochProcessFixtureRows()
					if calls.Add(1) == overAt {
						rows[1].RSSBytes = int64(limit+1) - rows[0].RSSBytes
					}
					return rows, nil
				})
			if meter == nil {
				t.Fatal("threshold refusal lost owned cleanup handle")
			}
			if stage == "initial" {
				if !errors.Is(err, errEpochProcessRSS) || !meter.closed {
					t.Fatal("initial limit did not retain joined refused sampler", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if stage == "ticker" {
				select {
				case <-stopped:
				case <-time.After(3 * time.Second):
					t.Fatal("live ticker did not stop at RSS overshoot")
				}
			} else {
				pauseEpochProcessTicker(meter)
			}
			if strings.HasSuffix(stage, "boundary") {
				advanced := false
				if meter.advance(t.Context(), 3, func() error { advanced = true; return nil }) == nil {
					t.Fatal("over-limit phase transition succeeded")
				}
				if advanced != (stage == "starting-boundary") {
					t.Fatal("overshoot crossed wrong native boundary", advanced)
				}
			}
			result, err := meter.close()
			if !errors.Is(err, errEpochProcessRSS) || !result.Joined || !result.RSSLimitExceeded || calls.Load() != overAt || failures.Load() != 1 {
				t.Fatal("RSS refusal lost stop, join or exact probe prefix", result, err, calls.Load(), failures.Load())
			}
			value := result.Phases[len(result.Phases)-1].Observation
			if !value.Available || value.FailureClass != "" || value.ObservedRSSBytes != limit+1 || value.ObservedRSSHighWaterBytes != limit+1 || validateNativeObservation(value) != nil {
				t.Fatal("positive valid overshoot was clipped or relabeled unavailable", value)
			}
			meter.exited() // Even a later root exit cannot overwrite the refused valid endpoint.
			meter.armStop()
			if meter.advance(t.Context(), meter.phase+1, func() error { t.Fatal("limit latch reset at phase transition"); return nil }) == nil {
				t.Fatal("limit latch advanced")
			}
			if _, err := meter.close(); !errors.Is(err, errEpochProcessRSS) {
				t.Fatal("cleanup hid sticky limit", err)
			}
			again, err := meter.snapshot()
			if !errors.Is(err, errEpochProcessRSS) || !reflect.DeepEqual(result, again) || calls.Load() != overAt || failures.Load() != 1 {
				t.Fatal("cleanup retried or erased over-limit observations", again, err, calls.Load(), failures.Load())
			}
		})
	}
}

func TestEpochProcessRSSAtLimitAdmitted(t *testing.T) {
	limit := frozenSafetyEnvelope().MaximumPeakRSSBytes
	var calls atomic.Int64
	meter, err := newEpochProcessObservation(t.Context(), 41, 2, "phebs", map[string]string{"phebs": "phebs", "git": "git"},
		func() { t.Error("exact threshold triggered stop") }, func(context.Context, int) ([]t4013.NativeProcessRecord, error) {
			calls.Add(1)
			rows := epochProcessFixtureRows()
			rows[1].RSSBytes = int64(limit) - rows[0].RSSBytes
			return rows, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	pauseEpochProcessTicker(meter)
	result, err := meter.close()
	if err != nil || result.RSSLimitExceeded || !result.Phases[0].Observation.Available || result.Phases[0].Observation.ObservedRSSBytes != limit || calls.Load() != 2 {
		t.Fatal("exact threshold rejected or changed", result, err, calls.Load())
	}
}

func TestEpochProcessPhaseBoundaryAndDetachedJoin(t *testing.T) {
	rows := epochProcessFixtureRows()
	calls := 0
	meter, err := newEpochProcessObservation(t.Context(), 41, 2, "phebs", map[string]string{"phebs": "phebs", "git": "git"}, func() {},
		func(context.Context, int) ([]t4013.NativeProcessRecord, error) { calls++; return rows, nil })
	if err != nil {
		t.Fatal(err)
	}
	pauseEpochProcessTicker(meter)
	if err := meter.advance(t.Context(), 3, func() error {
		if calls != 2 || meter.phase != 2 {
			t.Fatal("advance preceded ending-phase native sample")
		}
		rows[1].RSSBytes = 1 << 30
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("new phase reused a census instead of sampling", calls)
	}
	result, err := meter.close()
	if err != nil || !result.Joined || len(result.Phases) != 2 || calls != 4 {
		t.Fatal(result, calls, err)
	}
	for _, phase := range result.Phases {
		if phase.Observation.CompletedCensuses != 2 || validateNativeObservation(phase.Observation) != nil {
			t.Fatal(phase)
		}
	}
	if result.Phases[0].Observation.ObservedRSSHighWaterBytes != 12288 || result.Phases[1].Observation.ObservedRSSHighWaterBytes != 1<<30+4096 {
		t.Fatal("boundary mixed positive samples", result)
	}
	result.Phases[0].Observation.Classes[0].Class = "mutable"
	again, err := meter.close()
	if err != nil || again.Phases[0].Observation.Classes[0].Class == "mutable" || calls != 4 {
		t.Fatal("repeat close changed evidence", again, calls, err)
	}
	meter.armStop()
	meter.exited()
	if _, err := meter.snapshot(); err != nil {
		t.Fatal("intentional post-meter death changed observation", err)
	}
}

func TestEpochProcessUnexpectedExitAndRequiredFailureKeepPositivePrefix(t *testing.T) {
	for _, kind := range []string{"exit", "exit-after-close", "probe", "identity", "refusal-then-exit"} {
		t.Run(kind, func(t *testing.T) {
			rows := epochProcessFixtureRows()
			calls, failures := 0, 0
			meter, err := newEpochProcessObservation(t.Context(), 41, 8, "phebs", map[string]string{"phebs": "phebs", "git": "git"}, func() { failures++ },
				func(context.Context, int) ([]t4013.NativeProcessRecord, error) {
					calls++
					if kind == "probe" && calls > 1 {
						return nil, errors.New("denied")
					}
					return rows, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			pauseEpochProcessTicker(meter)
			if kind == "exit-after-close" {
				if _, err := meter.close(); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "exit", "exit-after-close":
				meter.exited()
				meter.armStop()
			case "identity":
				rows[0].StartIdentity = "reused-pid"
			case "refusal-then-exit":
				rows[1].ObservedName = "unknown"
				if meter.sampleLocked(t.Context()) == nil {
					t.Fatal("unknown classification accepted")
				}
				meter.exited()
			}
			result, err := meter.close()
			value := result.Phases[0].Observation
			if err == nil || value.Available || value.CompletedCensuses == 0 || value.ObservedRSSHighWaterBytes != 12288 || validateNativeObservation(value) != nil {
				t.Fatal("lost positive failure prefix", value, err)
			}
			if kind == "refusal-then-exit" && value.FailureClass != "unknown_classification" {
				t.Fatal("native Wait overwrote first refusal", value)
			}
			priorCalls := calls
			if meter.advance(t.Context(), 9, func() error { t.Fatal("failed observation advanced"); return nil }) == nil {
				t.Fatal("failed advance succeeded")
			}
			if _, err := meter.close(); err == nil || calls != priorCalls {
				t.Fatal("sticky failure retried", calls, priorCalls)
			}
			if strings.HasPrefix(kind, "exit") && failures != 1 {
				t.Fatal("unexpected wait did not stop run", failures)
			}
		})
	}
}

func TestEpochProcessTickerAndCloseJoinBlockedProbe(t *testing.T) {
	var calls atomic.Int64
	entered, release, failed := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	meter, err := newEpochProcessObservation(t.Context(), 41, 2, "phebs", map[string]string{"phebs": "phebs", "git": "git"}, func() { failed <- struct{}{} },
		func(ctx context.Context, _ int) ([]t4013.NativeProcessRecord, error) {
			if calls.Add(1) == 2 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return epochProcessFixtureRows(), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("fixed native ticker did not start")
	}
	closed := make(chan error, 1)
	go func() { _, err := meter.close(); closed <- err }()
	select {
	case <-closed:
		t.Fatal("close abandoned issued native sample")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("close did not perform exactly one final live sample", calls.Load())
	}
	select {
	case <-failed:
		t.Fatal("intentional ticker stop failed sample")
	default:
	}
}

func TestEpochProcessCheckpointMerge(t *testing.T) {
	gauge, rows := observationFixture(t)
	gauge.probe = func(context.Context, int) ([]t4013.NativeProcessRecord, error) { return rows, nil }
	prior, err := gauge.Sample(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next := prior
	next.Classes = append([]ProcessObservationClass(nil), prior.Classes...)
	next.ObservedRSSBytes, next.ObservedRSSHighWaterBytes = 4096, 4096
	got := mergeEpochProcessObservation(prior, next)
	if got.CompletedCensuses != 2 || got.ObservedRSSHighWaterBytes != prior.ObservedRSSHighWaterBytes || got.ObservedRSSBytes != next.ObservedRSSBytes || validateNativeObservation(got) != nil {
		t.Fatal(got)
	}
	next.Available, next.FailureClass = false, "measurement_unavailable"
	next.CompletedCensuses, next.ObservedDescendants, next.ObservedDescendantsHighWater, next.ObservedRSSBytes, next.ObservedRSSHighWaterBytes = 0, 0, 0, 0, 0
	for index := range next.Classes {
		next.Classes[index].ObservedRows, next.Classes[index].ObservedHighWater = 0, 0
	}
	got = mergeEpochProcessObservation(prior, next)
	if got.Available || got.CompletedCensuses != 1 || got.ObservedRSSBytes != prior.ObservedRSSBytes || validateNativeObservation(got) != nil {
		t.Fatal("new root refusal erased prior root", got)
	}
	prior.CompletedCensuses, next.CompletedCensuses = math.MaxUint64, 1
	if got := mergeEpochProcessObservation(prior, next); got.Available || got.FailureClass != "counter_overflow" {
		t.Fatal(got)
	}
}

func TestEpochProcessProductionWiring(t *testing.T) {
	raw, err := os.ReadFile("epoch_launch.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	ordered := []string{"handle, err := flow.parent.StartInPhase", "run.processObservation, err = startEpochProcessObservation", "waitErr := handle.Wait()", "run.processObservation.exited()", "go run.finish"}
	previous := -1
	for _, needle := range ordered {
		index := strings.Index(text, needle)
		if index <= previous {
			t.Fatal("native startup/Wait ordering changed", needle)
		}
		previous = index
	}
	closing, signal := strings.Index(text, "run.processObservation.close()"), strings.Index(text, "signalProductionStop(run.command.Process)")
	if closing < 0 || closing >= signal {
		t.Fatal("native sampler closes after intentional signal")
	}
	if !reflect.DeepEqual([]time.Duration{epochProcessCadence, epochProcessProbeTimeout}, []time.Duration{250 * time.Millisecond, 2 * time.Second}) {
		t.Fatal("server-only fixed sampling recipe changed")
	}
}
