package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"runtime"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

const executionRuntimeFactsCommand = "t422-runtime-facts"

type executionScheduleFacts struct {
	MaxAttempts      int `json:"max_attempts"`
	RepositoryTokens int `json:"repository_tokens"`
}

// Wire observations from the protected binary, not frozenExecutionRuntime.
// Native aggregate capacity and store retry capacity remain distinct from
// target partition totals and selected accepted-report limits.
type executionConfiguredRuntimeFacts struct {
	Schema                           string                 `json:"schema"`
	StoreRunnerDefaultMaxAttempts    int                    `json:"store_runner_default_max_attempts"`
	ObservationIOConcurrency         int                    `json:"observation_io_concurrency"`
	ObservationCPUConcurrency        int                    `json:"observation_cpu_concurrency"`
	RelationshipConcurrency          int                    `json:"relationship_concurrency"`
	ExtractionConcurrency            int                    `json:"extraction_concurrency"`
	ObservationPlanning              executionScheduleFacts `json:"observation_planning"`
	ObservationInventory             executionScheduleFacts `json:"observation_inventory"`
	ObservationExecution             executionScheduleFacts `json:"observation_execution"`
	Relationship                     executionScheduleFacts `json:"relationship"`
	Extraction                       executionScheduleFacts `json:"extraction"`
	NativeMaximumAggregatePartitions int                    `json:"native_maximum_aggregate_partitions"`
	StoreGenerationMaxAttempts       int                    `json:"store_generation_max_attempts"`
	SelectedJobAcceptedAttempts      int                    `json:"selected_job_accepted_attempts"`
	SelectedChunkAcceptedAttempts    int                    `json:"selected_chunk_accepted_attempts"`
	MaximumStoreRowsPerTransaction   int                    `json:"maximum_store_rows_per_transaction"`
}

func decodeExecutionRuntimeFacts(raw []byte) (executionConfiguredRuntimeFacts, error) {
	var facts executionConfiguredRuntimeFacts
	if len(raw) == 0 || len(raw) > 4<<10 || json.Unmarshal(raw, &facts) != nil {
		return facts, ErrExecutionEpochOne
	}
	canonical, err := json.Marshal(facts)
	// Exact re-encoding rejects duplicate/unknown/missing fields, reordered
	// keys, alternate numbers, trailing data and anything but one final LF.
	if err != nil || !bytes.Equal(raw, append(canonical, '\n')) || facts.Schema != "t422-runtime-facts-v1" {
		return facts, ErrExecutionEpochOne
	}
	for _, value := range []int{
		facts.StoreRunnerDefaultMaxAttempts, facts.ObservationIOConcurrency, facts.ObservationCPUConcurrency,
		facts.RelationshipConcurrency, facts.ExtractionConcurrency, facts.NativeMaximumAggregatePartitions,
		facts.StoreGenerationMaxAttempts, facts.SelectedJobAcceptedAttempts, facts.SelectedChunkAcceptedAttempts,
		facts.MaximumStoreRowsPerTransaction,
		facts.ObservationPlanning.MaxAttempts, facts.ObservationPlanning.RepositoryTokens,
		facts.ObservationInventory.MaxAttempts, facts.ObservationInventory.RepositoryTokens,
		facts.ObservationExecution.MaxAttempts, facts.ObservationExecution.RepositoryTokens,
		facts.Relationship.MaxAttempts, facts.Relationship.RepositoryTokens,
		facts.Extraction.MaxAttempts, facts.Extraction.RepositoryTokens,
	} {
		if value <= 0 {
			return facts, ErrExecutionEpochOne
		}
	}
	// No comparison against expected profile values: later admission owns it.
	return facts, nil
}

// Private owned prefix. Output pumps belong to the sole Wait; if it cannot
// join by Deadline, waited/stdout/stderr remain retained and must not be read.
// A later exit does not silently promote this failed attempt to completion.
type executionRuntimeObservation struct {
	Identity  ExecutionToolIdentity
	Path      string
	Directory string
	Deadline  time.Time

	CommandSHA256 string
	RawSHA256     string
	Facts         executionConfiguredRuntimeFacts
	Observed      bool
	Complete      bool

	PID          int
	RootStarted  bool
	RootJoined   bool
	SessionEmpty bool
	err          error
	waited       <-chan error
	stdout       *checkoutCommandOutput
	stderr       *checkoutCommandOutput
}

func (observed *executionRuntimeObservation) releasable() bool {
	return observed == nil || !observed.RootStarted || observed.RootJoined && observed.SessionEmpty
}

// run owns one native Start/Wait, but grants no image or profile authority.
// The only production caller below supplies the actual checked Phebs path.
// Cancellation signals the captured native session; it never creates a fresh
// grace deadline. Native enumeration/syscalls remain cooperatively bounded.
func (observed *executionRuntimeObservation) run(ctx context.Context, command *exec.Cmd) error {
	if observed == nil || ctx == nil || ctx.Err() != nil || command == nil || observed.RootStarted || observed.waited != nil || runtime.GOOS != "darwin" {
		return ErrExecutionEpochOne
	}
	deadline, ok := ctx.Deadline()
	if !ok || !time.Now().Before(deadline) {
		return ErrExecutionEpochOne
	}
	observed.Deadline = deadline
	commandContext, cancel := context.WithCancel(ctx) // Retains the ORIGINAL deadline.
	defer cancel()
	observed.stdout = &checkoutCommandOutput{remaining: 4 << 10, cancel: cancel}
	observed.stderr = &checkoutCommandOutput{remaining: 4 << 10, cancel: cancel}
	command.Stdout, command.Stderr = observed.stdout, observed.stderr
	// Existing bounded writer-pump allowance, clipped to the original time
	// remaining. The parent may return an unjoined prefix sooner on expiry.
	command.WaitDelay = min(time.Second, time.Until(deadline))
	prepareProductionSession(command)
	if err := command.Start(); err != nil {
		observed.err = errors.Join(ErrExecutionEpochOne, err)
		return observed.err
	}
	observed.RootStarted, observed.PID = true, command.Process.Pid
	waited := make(chan error, 1)
	observed.waited = waited
	go func() { waited <- command.Wait() }() // Sole Wait also owns both writer pumps.
	var waitErr error
	select {
	case waitErr = <-waited:
		observed.RootJoined = true
	case <-commandContext.Done():
		observed.err = errors.Join(ErrExecutionEpochOne, commandContext.Err(), t4013.KillPrivateProcessSession(observed.PID))
		waitErr, observed.RootJoined = waitExecutionProcessRoot(waited, deadline)
	}
	if !observed.RootJoined {
		// Wait and its bounded sinks stay reachable. No output inspection or
		// automatic retry; Close/outer release must retain borrowed inputs.
		observed.err = errors.Join(ErrExecutionEpochOne, observed.err, ctx.Err())
		return observed.err
	}
	members, sessionErr := t4013.PrivateProcessSessionMembers(observed.PID)
	observed.SessionEmpty = sessionErr == nil && members == 0
	if !observed.SessionEmpty {
		// This no-work root must leave no descendant. Forced cleanup remains
		// a failure even if native membership subsequently reaches zero.
		observed.err = errors.Join(ErrExecutionEpochOne, observed.err, sessionErr, t4013.KillPrivateProcessSession(observed.PID))
		sessionErr = t4013.WaitPrivateProcessSession(observed.PID, deadline)
		observed.SessionEmpty = sessionErr == nil
	}
	observed.err = errors.Join(observed.err, waitErr, sessionErr, commandContext.Err(), observed.stdout.err, observed.stderr.err)
	if observed.stderr.buffer.Len() != 0 || !time.Now().Before(deadline) {
		observed.err = errors.Join(ErrExecutionEpochOne, observed.err)
	}
	return observed.err
}

// prepareProfileRuntime is optional preparation evidence, never an admission
// issuer. The caller supplies the original deadline; the rehearsal's outer
// deadline does not establish the future launcher's bounded admission stage.
func (flow *ExecutionEpochOne) prepareProfileRuntime(ctx context.Context) error {
	if flow == nil || ctx == nil || ctx.Err() != nil || flow.epochs == nil || flow.epochs.author == nil {
		return ErrExecutionEpochOne
	}
	if deadline, ok := ctx.Deadline(); !ok || !time.Now().Before(deadline) {
		return ErrExecutionEpochOne
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if flow.plan.Schema != PlanV3Schema || flow.closed || flow.used || flow.authored || !flow.authorStarted.IsZero() ||
		flow.profileRuntime != nil || flow.profileEnvironment == nil || !flow.profileEnvironmentUsed ||
		author.closed || author.err != nil || author.active || author.borrowedBy != nil || author.next != 0 ||
		epochs.closed || epochs.err != nil || epochs.active || epochs.released != 0 || flow.phebs == nil ||
		author.request.Builds == nil || flow.phebs.referenceInputs != author.request.Builds {
		return ErrExecutionEpochOne
	}
	// ponytail: one preparation-only process holds the existing flow→author→
	// epochs locks through the caller's deadline; no child calls back here.
	// A separate reservation is needed only if concurrent preparation is added.
	observed := &executionRuntimeObservation{err: ErrExecutionEpochOne}
	flow.profileRuntime = observed // Consume once, including pre-Start refusal.
	if epochs.checkLocked(ctx, 1) != nil {
		return observed.err
	}
	identity, path, err := flow.phebs.Check(ctx, "phebs")
	if err != nil || identity.BuildVCSRevision != author.request.Builds.reference.source || identity.BuildVCSModified {
		return observed.err
	}
	observed.Identity, observed.Path, observed.Directory = identity, path, epochs.epochs[0].Temporary
	command := exec.Command(path, executionRuntimeFactsCommand)
	command.Dir, command.Env = observed.Directory, externalToolEnvironment(observed.Directory)
	// No operational selector, stdin, inherited descriptors or config. Bind
	// actual unnormalized argv/environment privately; not the three work rows.
	binding, err := json.Marshal(struct {
		Path      string   `json:"path"`
		Directory string   `json:"directory"`
		Args      []string `json:"args"`
		Env       []string `json:"environment"`
	}{Path: command.Path, Directory: command.Dir, Args: command.Args, Env: command.Env})
	if err != nil {
		return observed.err
	}
	observed.CommandSHA256 = SHA256(binding)
	observed.err = nil
	if err := observed.run(ctx, command); err != nil {
		observed.err = errors.Join(ErrExecutionEpochOne, observed.err, err)
		return observed.err
	}
	facts, err := decodeExecutionRuntimeFacts(observed.stdout.buffer.Bytes())
	if err != nil {
		observed.err = err
		return err
	}
	observed.Facts, observed.RawSHA256, observed.Observed = facts, SHA256(observed.stdout.buffer.Bytes()), true
	current, currentPath, err := flow.phebs.Check(ctx, "phebs")
	if err != nil || current != identity || currentPath != path || epochs.checkLocked(ctx, 1) != nil || ctx.Err() != nil {
		observed.err = ErrExecutionEpochOne
		return observed.err
	}
	observed.Complete = true
	return nil
}
