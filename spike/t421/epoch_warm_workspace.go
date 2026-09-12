package t421

import (
	"context"
	"encoding/hex"
	"sync"
)

// Tee only the actual epoch-one combined stdout/stderr writer. The same
// pointer is installed for both streams, preserving exec's single copier.
// Never read checkoutCommandOutput's bytes.Buffer before the native join.
type epochWarmWorkspaceOutput struct {
	mu                                               sync.Mutex
	output                                           *checkoutCommandOutput
	plan                                             Plan
	input                                            string
	line                                             [79]byte // Long unrelated logs are discarded here, not in retained output.
	length                                           int
	long, reserved                                   bool
	tail                                             [3]byte
	observation                                      ExecutionWorkspaceByteObservation
	sample                                           ExecutionWorkspaceBytePhase
	armed, signaled                                  bool
	startInvalid                                     bool
	ready                                            chan struct{}
	physicalReady                                    chan struct{}
	physicalSample                                   ExecutionWorkspaceBytePhase
	physicalArmed, physicalSignaled, physicalInvalid bool
	err                                              error
}

func newEpochWarmWorkspaceOutput(output *checkoutCommandOutput, plan Plan, input [32]byte) *epochWarmWorkspaceOutput {
	return &epochWarmWorkspaceOutput{output: output, plan: plan, input: "sha256:" + hex.EncodeToString(input[:]), ready: make(chan struct{}), physicalReady: make(chan struct{})}
}

func (out *epochWarmWorkspaceOutput) signalLocked() {
	if !out.signaled {
		out.signaled = true
		close(out.ready)
	}
}

func (out *epochWarmWorkspaceOutput) Write(raw []byte) (int, error) {
	// Charge/store the real prefix first, even if its semantic event refuses.
	n, writeErr := out.output.Write(raw)
	out.mu.Lock()
	for _, b := range raw[:n] {
		out.reserved = out.reserved || out.tail[1] == 'W' && out.tail[2] == 'B' && b == 'B' ||
			out.tail[0] == 'W' && out.tail[1] == 'B' && b == ':'
		out.tail[0], out.tail[1], out.tail[2] = out.tail[1], out.tail[2], b
		if out.length < len(out.line) {
			out.line[out.length] = b
			out.length++
		} else {
			out.long = true
		}
		if b != '\n' {
			continue
		}
		if out.reserved && out.err == nil {
			if out.long {
				out.err = errExecutionAttempts
			} else {
				_, out.err = observeWorkspaceByteEvent(out.line[:out.length], out.plan, 2, out.input, &out.observation)
				row := out.observation.Phases[2]
				if row.Attempts != 0 && !out.armed {
					out.startInvalid = true
					out.err = errExecutionAttempts
				}
				physical := out.observation.Phases[3]
				if physical.Attempts >= 2 && !out.physicalArmed {
					out.physicalInvalid = true
					out.err = errExecutionAttempts
				}
				if physical.Completed == 2 && out.physicalSample.Completed == 0 {
					out.physicalSample = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1, Maximum: out.observation.physicalPostAuthor}
				}
				if out.observation.physicalReady {
					out.signalPhysicalLocked()
				}
				if row.Completed == 1 && out.sample.Completed == 0 {
					out.sample = row // Retain positive excess even on this refusal.
					out.signalLocked()
				}
			}
		}
		out.length, out.long, out.reserved, out.tail = 0, false, false, [3]byte{}
	}
	if writeErr != nil && out.err == nil {
		out.err = writeErr
	}
	err := out.err
	if err != nil {
		out.signalLocked()
		out.signalPhysicalLocked()
	}
	out.mu.Unlock()
	if err != nil {
		out.output.cancel()
		return n, err
	}
	return n, writeErr
}

func (out *epochWarmWorkspaceOutput) arm() error {
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.armed || out.err != nil || out.observation.Phases[2].Attempts != 0 {
		return ErrExecutionEpochOne
	}
	out.armed = true
	return nil
}

func (out *epochWarmWorkspaceOutput) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ErrExecutionEpochOne
	case <-out.ready:
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	if ctx.Err() != nil || out.err != nil || !out.armed || out.observation.Phases[1].Completed != 1 || out.sample.Attempts != 1 || out.sample.Completed != 1 {
		return ErrExecutionEpochOne
	}
	return nil
}

func (out *epochWarmWorkspaceOutput) snapshot() (ExecutionWorkspaceBytePhase, bool, bool) {
	out.mu.Lock()
	defer out.mu.Unlock()
	exceeded := out.sample.Completed == 1 && (out.sample.Maximum.LogicalBytes > out.plan.WorkEnvelope.MaximumDataLogicalBytes ||
		out.sample.Maximum.AllocatedBytes > out.plan.SafetyEnvelope.MaximumDataAllocatedBytes)
	return out.sample, out.startInvalid || out.sample.Completed != 1 && (out.err != nil || out.armed), exceeded
}

// This is called with the original caller context, never the retiring cold
// deadline context. The warm timer is armed BEFORE the Resume request. Its
// cancellation is owned by the existing cold operation until the callback ACK
// stream is observed; Stop cannot race this gap with another control exchange.
func (run *ExecutionEpochOneRun) resumeMeasuredWarm(caller context.Context) error {
	if run.completeColdAtBoundary(caller, true) != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	deadline := run.phaseDeadline
	run.mu.Unlock()
	ctx, cancel := context.WithDeadline(caller, deadline)
	defer cancel()
	if run.warmWorkspace.arm() != nil || run.control.Resume(ctx) != nil || run.warmWorkspace.wait(ctx) != nil {
		return ErrExecutionEpochOne
	}
	return ctx.Err()
}

func (out *epochWarmWorkspaceOutput) signalPhysicalLocked() {
	if !out.physicalSignaled {
		out.physicalSignaled = true
		close(out.physicalReady)
	}
}

func (out *epochWarmWorkspaceOutput) armPhysical() error {
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.physicalArmed || out.err != nil || out.observation.Phases[2].Completed != 2 ||
		out.observation.Phases[3].Attempts != 1 || out.observation.Phases[3].Completed != 1 {
		return ErrExecutionEpochOne
	}
	out.physicalArmed = true
	return nil
}

func (out *epochWarmWorkspaceOutput) waitPhysical(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ErrExecutionEpochOne
	case <-out.physicalReady:
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	if ctx.Err() != nil || out.err != nil || !out.physicalArmed ||
		out.physicalSample.Attempts != 1 || out.physicalSample.Completed != 1 || !out.observation.physicalReady {
		return ErrExecutionEpochOne
	}
	return nil
}

func (out *epochWarmWorkspaceOutput) physicalSnapshot() (ExecutionWorkspaceBytePhase, bool, bool) {
	out.mu.Lock()
	defer out.mu.Unlock()
	excess := out.physicalSample.Completed != 0 &&
		(out.physicalSample.Maximum.LogicalBytes > out.plan.WorkEnvelope.MaximumDataLogicalBytes ||
			out.physicalSample.Maximum.AllocatedBytes > out.plan.SafetyEnvelope.MaximumDataAllocatedBytes)
	return out.physicalSample, out.physicalInvalid || out.physicalSample.Completed != 1 &&
		(out.err != nil || out.physicalArmed), excess
}

// Called only after authorPhysical and beginPhysical succeed, before any
// progress query. Reopen's selected ACK is not completion: S retains the
// middle walk, and only R after real owner/request reopening releases wait.
func (run *ExecutionEpochOneRun) reopenMeasuredPhysical(ctx context.Context) error {
	if run.warmWorkspace == nil {
		return run.control.ReopenOwners(ctx)
	}
	if run.warmWorkspace.armPhysical() != nil || run.control.ReopenOwners(ctx) != nil ||
		run.warmWorkspace.waitPhysical(ctx) != nil {
		return ErrExecutionEpochOne
	}
	run.inspection.mu.Lock()
	row, unavailable, excess := run.warmWorkspace.physicalSnapshot()
	run.inspection.midphaseSamples.PostAuthor = row
	run.inspection.midphaseSamples.Unavailable = run.inspection.midphaseSamples.Unavailable || unavailable
	run.inspection.midphaseSamples.LimitExceeded = run.inspection.midphaseSamples.LimitExceeded || excess
	run.inspection.mu.Unlock()
	return ctx.Err()
}
