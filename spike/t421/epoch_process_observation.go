package t421

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
)

const (
	epochProcessCadence      = 250 * time.Millisecond
	epochProcessProbeTimeout = 2 * time.Second
)

var errEpochProcessRSS = errors.New("server sampled RSS limit exceeded")

// ExecutionServerProcessObservation covers only the actual server root and
// descendants seen in its completed sequential censuses. It is not whole-work
// process evidence: author/controller, offline work and cleanup remain outside
// this scope. Joined means the sampler, not the process session, has joined.
// Epoch four's phase-eight row already carries the prior root's phase-eight
// prefix: a later whole-work collector must replace that row, not add it again.
type ExecutionServerProcessObservation struct {
	Phases           []ExecutionServerProcessPhase
	Joined           bool
	RSSLimitExceeded bool // A successful sample crossed the unchanged frozen threshold; measurement stays available.
}

type ExecutionServerProcessPhase struct {
	Phase       uint32
	Observation ProcessObservation
}

// mu serializes the sole ticker, checkpoint transitions and the final live
// sample. No run mutex is held across a probe. State is bounded by fifteen
// phases, 129 native rows per census and the existing private name bound; no
// per-PID history survives a sample. A ticker drops missed ticks, never queues
// overlapping probes or catch-up work. The probe context is cooperative.
type epochProcessObservation struct {
	mu           sync.Mutex
	gauge        *ProcessObservationGauge
	rssLimit     uint64 // Source-frozen once at startup; not a caller-controlled policy.
	phase        uint32
	result       ExecutionServerProcessObservation
	prefix       *ProcessObservation // The earlier root's actual phase-eight prefix.
	stop, done   chan struct{}
	stopOnce     sync.Once
	closed       bool
	expectedExit bool
	fail         func()
}

// Names derive only from the already checked tool paths and closed native Git
// aliases. Darwin PROC_PIDTASKALLINFO exposes a sixteen-byte command field;
// aliases are truncated to that ABI width, never learned from sampled rows.
// Classification does not attest the sampled row's executable image.
func epochProcessNames(root string, tools []dispatchadmission.ProductionToolBinding) (map[string]string, error) {
	names := make(map[string]string)
	add := func(name, role string) error {
		if len(name) > 16 {
			name = name[:16]
		}
		if !validObservedProcessName(name) || !validObservedProcessClass(role) {
			return ErrExecutionEpochOne
		}
		if prior, exists := names[name]; exists && prior != role {
			return ErrExecutionEpochOne
		}
		names[name] = role
		return nil
	}
	if !filepath.IsAbs(root) || filepath.Base(root) != "phebs" || add(filepath.Base(root), "phebs") != nil || len(tools) != 3 {
		return nil, ErrExecutionEpochOne
	}
	seen := make(map[string]bool)
	for _, tool := range tools {
		if seen[tool.Role] || !slices.Contains([]string{"git", "surreal", "zoekt-git-index"}, tool.Role) ||
			!filepath.IsAbs(tool.Path) || filepath.Base(tool.Path) != tool.Role || add(filepath.Base(tool.Path), tool.Role) != nil {
			return nil, ErrExecutionEpochOne
		}
		seen[tool.Role] = true
	}
	for _, alias := range executionGitImageNames() {
		if add(alias, "git") != nil {
			return nil, ErrExecutionEpochOne
		}
	}
	// /bin/sh is an existing separately admitted trusted-system Git resource.
	if add("sh", "sh") != nil || len(names) > MaxProcessObservationNames {
		return nil, ErrExecutionEpochOne
	}
	return names, nil
}

// Called immediately after owned Start and before its sole Wait is started.
// Start-to-first-census work is not observed, and child histories are never
// complete. Keeping the child unreaped binds this initial PID to that birth.
func startEpochProcessObservation(ctx context.Context, pid int, phase uint32, rootName string, names map[string]string, fail func()) (*epochProcessObservation, error) {
	return newEpochProcessObservation(ctx, pid, phase, rootName, names, fail, t4013.ObserveProcessTreeRecords)
}

func newEpochProcessObservation(ctx context.Context, pid int, phase uint32, rootName string, names map[string]string, fail func(), probe func(context.Context, int) ([]t4013.NativeProcessRecord, error)) (*epochProcessObservation, error) {
	if ctx == nil || pid <= 0 || phase < 2 || phase > 14 || !validObservedProcessName(rootName) || names[rootName] == "" || fail == nil || probe == nil {
		return nil, ErrExecutionEpochOne
	}
	gauge, err := newProcessObservationGauge(pid, names)
	if err != nil {
		return nil, err
	}
	gauge.rootObservedName = rootName // Known checked root basename, not learned from the first census.
	meter := &epochProcessObservation{gauge: gauge, rssLimit: frozenSafetyEnvelope().MaximumPeakRSSBytes, phase: phase,
		stop: make(chan struct{}), done: make(chan struct{}), fail: fail}
	initialCtx, cancel := context.WithTimeout(ctx, epochProcessProbeTimeout)
	var rows []t4013.NativeProcessRecord
	probeErr := initialCtx.Err()
	if probeErr == nil {
		rows, probeErr = probe(initialCtx, pid)
	}
	if probeErr != nil || initialCtx.Err() != nil {
		if probeErr == nil {
			probeErr = initialCtx.Err()
		}
		_, err = gauge.failProbe(probeErr)
	} else if len(rows) == 0 || rows[0].PID != pid || rows[0].StartIdentity == "" {
		_, err = gauge.fail("invalid_census")
	} else {
		gauge.expectedRootStartIdentity = rows[0].StartIdentity
		_, err = gauge.acceptRows(rows)
	}
	cancel()
	gauge.probe = probe
	if err == nil {
		err = meter.checkRSSLocked(gauge.Observation())
	}
	if err != nil {
		meter.closed, meter.result.Joined = true, true
		_ = meter.saveLocked()
		close(meter.done)
		return meter, err
	}
	go meter.run()
	return meter, nil
}

func (meter *epochProcessObservation) run() {
	defer close(meter.done)
	ticker := time.NewTicker(epochProcessCadence)
	defer ticker.Stop()
	for {
		select {
		case <-meter.stop:
			return
		case <-ticker.C:
		}
		meter.mu.Lock()
		select {
		case <-meter.stop:
			meter.mu.Unlock()
			return
		default:
		}
		err := meter.sampleLocked(context.Background())
		notify := err != nil && !meter.result.RSSLimitExceeded
		meter.mu.Unlock()
		if err != nil {
			if notify {
				meter.fail()
			}
			return
		}
	}
}

func (meter *epochProcessObservation) sampleLocked(ctx context.Context) error {
	if meter.result.RSSLimitExceeded {
		return errEpochProcessRSS
	}
	probeCtx, cancel := context.WithTimeout(ctx, epochProcessProbeTimeout)
	defer cancel()
	observation, err := meter.gauge.Sample(probeCtx)
	if err != nil {
		return err
	}
	return meter.checkRSSLocked(observation)
}

// A genuine completed census above the existing frozen 20-GiB threshold is
// a safety refusal, not unavailable measurement. Preserve the whole overshoot,
// stop immediately and never probe or reset it away during later cleanup.
// The owned callback only requests stop; it does not re-enter this meter.
func (meter *epochProcessObservation) checkRSSLocked(observation ProcessObservation) error {
	if meter.result.RSSLimitExceeded {
		return errEpochProcessRSS
	}
	if observation.Available && observation.ObservedRSSHighWaterBytes > meter.rssLimit {
		meter.result.RSSLimitExceeded = true
		meter.fail()
		return errEpochProcessRSS
	}
	return nil
}

// Advance executes only the already-quiescent accounting transition. No next
// phase permission is resumed until both real boundary samples have returned.
func (meter *epochProcessObservation) advance(ctx context.Context, phase uint32, advance func() error) error {
	if meter == nil || ctx == nil || advance == nil {
		return ErrExecutionEpochOne
	}
	meter.mu.Lock()
	defer meter.mu.Unlock()
	if meter.closed || phase != meter.phase+1 || phase > 14 || meter.sampleLocked(ctx) != nil {
		return ErrExecutionEpochOne
	}
	if meter.saveLocked() != nil {
		return ErrExecutionEpochOne
	}
	if advance() != nil {
		return ErrExecutionEpochOne
	}
	meter.phase = phase
	meter.prefix = nil
	meter.gauge.mu.Lock()
	// Identity and sticky failure never reset; only completed phase counters do.
	observation := meter.gauge.observation
	meter.gauge.observation = ProcessObservation{MeasurementKind: observation.MeasurementKind, NativeHistory: observation.NativeHistory,
		SimultaneousBounds: observation.SimultaneousBounds, Classes: make([]ProcessObservationClass, len(observation.Classes))}
	for index, class := range observation.Classes {
		meter.gauge.observation.Classes[index].Class = class.Class
	}
	meter.gauge.mu.Unlock()
	return meter.sampleLocked(ctx)
}

// Called by the sole owned Wait. An observed exit before the logical stop arm
// invalidates its last phase even when a tick has not noticed a missing root.
func (meter *epochProcessObservation) exited() {
	if meter == nil {
		return
	}
	meter.mu.Lock()
	unexpected := !meter.expectedExit && !meter.result.RSSLimitExceeded
	if unexpected {
		meter.gauge.mu.Lock()
		_, _ = meter.gauge.fail("measurement_unavailable")
		meter.gauge.mu.Unlock()
		if meter.closed {
			_ = meter.saveLocked()
		}
	}
	meter.mu.Unlock()
	if unexpected {
		meter.fail()
	}
}

// Sampling closes before the caller requests intentional native death. An
// already observed unexpected exit remains sticky; arming cannot clear it.
// A natural exit between the last live census and the actual signal may be
// unobserved. This is a logical endpoint, not physical first-cause ordering.
func (meter *epochProcessObservation) armStop() {
	if meter == nil {
		return
	}
	meter.mu.Lock()
	meter.expectedExit = true
	meter.mu.Unlock()
}

func (meter *epochProcessObservation) snapshot() (ExecutionServerProcessObservation, error) {
	if meter == nil {
		return ExecutionServerProcessObservation{}, ErrExecutionEpochOne
	}
	meter.mu.Lock()
	defer meter.mu.Unlock()
	err := meter.saveLocked()
	if !meter.closed {
		err = ErrExecutionEpochOne
	}
	return cloneServerProcessObservation(meter.result), err
}

// Close joins the ticker and its in-flight sample, then takes one final live
// sample before the caller signals the owned root. Failure never retries an
// earlier refused probe. Calling this after an unexpected Wait cannot succeed.
func (meter *epochProcessObservation) close() (ExecutionServerProcessObservation, error) {
	if meter == nil {
		return ExecutionServerProcessObservation{}, ErrExecutionEpochOne
	}
	meter.stopOnce.Do(func() { close(meter.stop) })
	<-meter.done
	meter.mu.Lock()
	defer meter.mu.Unlock()
	if !meter.closed {
		_ = meter.sampleLocked(context.Background())
		meter.closed = true
	}
	meter.result.Joined = true
	err := meter.saveLocked()
	return cloneServerProcessObservation(meter.result), err
}

func (meter *epochProcessObservation) saveLocked() error {
	observation := meter.gauge.Observation()
	if meter.prefix != nil {
		observation = mergeEpochProcessObservation(*meter.prefix, observation)
	}
	index := slices.IndexFunc(meter.result.Phases, func(value ExecutionServerProcessPhase) bool { return value.Phase == meter.phase })
	value := ExecutionServerProcessPhase{Phase: meter.phase, Observation: observation}
	if index < 0 {
		meter.result.Phases = append(meter.result.Phases, value)
	} else {
		meter.result.Phases[index] = value
	}
	if !observation.Available {
		return ErrExecutionEpochOne
	}
	if meter.result.RSSLimitExceeded {
		return errEpochProcessRSS
	}
	return nil
}

func cloneServerProcessObservation(value ExecutionServerProcessObservation) ExecutionServerProcessObservation {
	value.Phases = slices.Clone(value.Phases)
	for index := range value.Phases {
		value.Phases[index].Observation.Classes = slices.Clone(value.Phases[index].Observation.Classes)
	}
	return value
}

// Two disjoint root lifetimes in checkpoint phase eight contribute actual
// census counts and maxima, never a sum presented as simultaneous RSS. A new
// root with no accepted census cannot erase the earlier positive current row.
func mergeEpochProcessObservation(prior, next ProcessObservation) ProcessObservation {
	result := next
	result.Classes = slices.Clone(next.Classes)
	if next.CompletedCensuses == 0 {
		result = prior
		result.Classes = slices.Clone(prior.Classes)
	}
	if next.CompletedCensuses > math.MaxUint64-prior.CompletedCensuses {
		// Like a refused gauge sample, an unrepresentable successor retains
		// the coherent earlier prefix instead of wrapping or clipping it.
		result = prior
		result.Classes = slices.Clone(prior.Classes)
		result.Available, result.FailureClass = false, "counter_overflow"
		return result
	}
	if len(next.Classes) != len(prior.Classes) {
		result = prior
		result.Classes = slices.Clone(prior.Classes)
		result.Available, result.FailureClass = false, "unknown_classification"
		return result
	}
	result.CompletedCensuses = prior.CompletedCensuses + next.CompletedCensuses
	result.ObservedDescendantsHighWater = max(prior.ObservedDescendantsHighWater, next.ObservedDescendantsHighWater)
	result.ObservedRSSHighWaterBytes = max(prior.ObservedRSSHighWaterBytes, next.ObservedRSSHighWaterBytes)
	for index, class := range prior.Classes {
		if class.Class != next.Classes[index].Class {
			result.Available, result.FailureClass = false, "unknown_classification"
			return result
		}
		result.Classes[index].ObservedHighWater = max(class.ObservedHighWater, next.Classes[index].ObservedHighWater)
	}
	result.Available = prior.Available && next.Available
	if !prior.Available {
		result.FailureClass = prior.FailureClass
	} else if !next.Available {
		result.FailureClass = next.FailureClass
	}
	return result
}

func (run *ExecutionEpochOneRun) processPhaseAdvance(ctx context.Context, phase uint32) error {
	return run.processObservation.advance(ctx, phase, func() error {
		if run.flow.controller.Advance() != nil || run.flow.store.Advance() != nil {
			return ErrExecutionEpochOne
		}
		return nil
	})
}
