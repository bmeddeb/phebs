package t421

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

const executionAttemptPrefix = "exact attempt: "
const maxExecutionAttemptLine = store.MaxJobLifecycleReportSize + 1024

var errExecutionAttempts = errors.New("execution attempt observation incomplete")

type ExecutionAttemptCount struct {
	JobAttempts, Retries, MaxRetriesUnit uint64
	SourceBlobAttempts                   uint64
	ObservationParses                    uint64
}

// Complete refers only to this post-join report subset. It proves no live
// ceiling enforcement, handler invocation, complete phase work, or admission.
type ExecutionAttemptObservation struct {
	Phases           [15]ExecutionAttemptCount
	Lifecycle        ExecutionLifecycleObservation
	Cache            ExecutionCacheObservation
	Complete         bool
	SourceBound      bool
	AttemptBound     bool
	ObservationBound bool
}

// The genuine caller supplies only output whose native Wait joined the copy
// goroutines and its independently retained bootstrap input identity. Never
// inspect a live checkoutCommandOutput or infer pipe EOF from a PC01 ACK.
func observeExecutionAttempts(raw []byte, plan Plan, producer uint32, input [32]byte, joined bool) (out ExecutionAttemptObservation, err error) {
	if !joined || plan.Schema != PlanV3Schema || len(plan.PhaseOrder) != len(out.Phases) || len(plan.WorkEnvelope.Phases) != len(out.Phases) ||
		producer < 2 || producer > 6 || input == ([32]byte{}) || len(raw) > 64<<20 || plan.ProcessAccounting == nil ||
		len(plan.ProcessAccounting.DispatchBudgets) != len(out.Phases) || !slices.Equal(plan.PhaseOrder, frozenPhaseOrder()) {
		return out, errExecutionAttempts
	}
	for index, name := range plan.PhaseOrder {
		if plan.WorkEnvelope.Phases[index].Phase != name || plan.ProcessAccounting.DispatchBudgets[index].Phase != name {
			return out, errExecutionAttempts
		}
	}
	wantInput := "sha256:" + hex.EncodeToString(input[:])
	reader := bufio.NewReaderSize(bytes.NewReader(raw), maxExecutionAttemptLine)
	consumed := 0
	for {
		start := consumed
		line, readErr := reader.ReadSlice('\n')
		consumed += len(line)
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			if !out.SourceBound || !out.AttemptBound || !out.ObservationBound {
				return out, errExecutionAttempts
			}
			for _, phase := range out.Phases {
				if phase.Retries > phase.JobAttempts {
					return out, errExecutionAttempts
				}
			}
			out.Complete = true
			out.Lifecycle.Complete = out.Lifecycle.Bound
			out.Cache.Complete = out.Cache.complete()
			if out.Cache.Bound && !out.Cache.Complete {
				out.Complete, out.Lifecycle.Complete = false, false
				return out, errExecutionAttempts
			}
			return out, nil
		}
		if readErr == nil && executionSetupTokenDiagnostic(line) {
			continue
		}
		if observed, err := observeCacheEvent(line, plan, producer, wantInput, &out.Cache); observed {
			if err != nil || readErr != nil {
				return out, errExecutionAttempts
			}
			continue
		}
		if observed, err := observeLifecycleEvent(line, plan, producer, wantInput, &out.Lifecycle); observed {
			if err != nil || readErr != nil {
				return out, errExecutionAttempts
			}
			continue
		}
		if blob, err := observeBlobEvent(line, plan, producer, wantInput, &out); blob {
			if err != nil || readErr != nil {
				return out, errExecutionAttempts
			}
			continue
		}
		if attempt, err := observeCompactAttempt(line, plan, producer, wantInput, &out); attempt {
			if err != nil || readErr != nil {
				return out, errExecutionAttempts
			}
			continue
		}
		// Unrelated long lines do not acquire a new log admission limit or a
		// proportional allocation. A partial final line is always incomplete.
		long := errors.Is(readErr, bufio.ErrBufferFull)
		for errors.Is(readErr, bufio.ErrBufferFull) {
			line, readErr = reader.ReadSlice('\n')
			consumed += len(line)
		}
		// Scan the original immutable line without copying: markers split
		// across reader fragments must not turn into unrelated output.
		if long && (reservedBlobEvent(raw[start:consumed], "SR") || reservedBlobEvent(raw[start:consumed], "OP") || reservedCompactAttempt(raw[start:consumed]) || reservedLifecycleEvent(raw[start:consumed]) || reservedCacheEvent(raw[start:consumed])) {
			return out, errExecutionAttempts
		}
		if readErr != nil {
			return out, errExecutionAttempts
		}
	}
}

// Called once by each native epoch's finish, after Wait joined the output pump.
// A stable buffer still need not be lossless: overflow, sink refusal, native
// failure and protocol failure all preserve counts but prevent completeness.
func (run *ExecutionEpochOneRun) finishAttemptObservation(ctx context.Context, result *ExecutionEpochOneResult, death executionProcessDeath, failure error) error {
	if !result.RootJoined || run.output == nil {
		return ErrExecutionEpochOne // Do not inspect a possibly live buffer.
	}
	var err error
	result.Attempts, err = observeExecutionAttempts(run.output.buffer.Bytes(), run.flow.plan, run.producer(), run.attemptInput, true)
	var indexErr error
	result.IndexOffers, indexErr = observeExecutionIndexOffers(run.output.buffer.Bytes(), run.flow.plan, run.producer(), run.attemptInput, true, err == nil && failure == nil)
	footer, footerErr := executionTerminalFooter(run.output.buffer.Bytes(), run.attemptInput)
	if run.terminalEntered {
		// Only finish owns these actual native facts and the preceding PC/SDK,
		// DA/SA joins. A footer or a boolean assertion cannot replace them.
		if !footer || !run.terminalRequested || !run.checkpointAllowed || run.epoch.Epoch != 3 || run.producer() != 4 ||
			run.flow.controller == nil || run.flow.controller.Context().Err() != nil || run.terminalContext == nil || run.terminalContext.Err() != nil ||
			!epochCheckpointClosedPrefix(ctx, *result, true) || !death.RootJoined || !death.SessionEmpty ||
			run.command == nil || run.command.Process == nil || run.command.ProcessState != death.ProcessState ||
			!executionProcessSIGKILL(death.WaitErr, death.ProcessState, run.command.Process.Pid) {
			footerErr = errExecutionAttempts
		}
	} else if footer {
		footerErr = errExecutionAttempts // No footer is valid in an ordinary close.
	}
	if err != nil || !result.Attempts.Lifecycle.Complete || !result.Attempts.Cache.Complete || indexErr != nil || failure != nil || footerErr != nil || run.output.err != nil || ctx == nil || ctx.Err() != nil {
		result.Attempts.Complete = false
		result.Attempts.Lifecycle.Complete = false
		result.Attempts.Cache.Complete = false
		result.IndexOffers.Complete = false
		return ErrExecutionEpochOne
	}
	return nil
}

// One bounded line pass after Wait, in addition to the existing two metric
// parsers. Complete ordinary diagnostics may follow the native fence;
// partial final lines retain the existing refusal, even for ordinary output.
// Future compact metric families must extend this reserved-family check too.
func executionTerminalFooter(raw []byte, input [32]byte) (seen bool, err error) {
	if len(raw) > 64<<20 || input == ([32]byte{}) {
		return false, errExecutionAttempts
	}
	var want [81]byte
	copy(want[:], "TFE1:4:8:sha256:")
	hex.Encode(want[16:80], input[:])
	want[80] = '\n'
	for len(raw) != 0 {
		end := bytes.IndexByte(raw, '\n')
		if end < 0 {
			return seen, errExecutionAttempts
		}
		line := raw[:end+1]
		raw = raw[end+1:]
		if executionSetupTokenDiagnostic(line) {
			continue
		}
		if bytes.Contains(line, []byte("TFE")) {
			if seen || !bytes.Equal(line, want[:]) {
				return seen, errExecutionAttempts
			}
			seen = true
			continue
		}
		index := reservedTerminalIndex(line)
		if seen && (reservedBlobEvent(line, "SR") || reservedCompactAttempt(line) || index ||
			bytes.Contains(line, []byte("OPB")) || reservedBlobEvent(line, "OP") || reservedLifecycleEvent(line) || reservedCacheEvent(line)) ||
			index && line[0] != 'I' && !bytes.HasPrefix(line, []byte("ZI")) {
			return seen, errExecutionAttempts
		}
	}
	return seen, nil
}

// The standard logger's exact setup-token line is ordinary payload, even when
// its opaque base64url text contains a reserved marker or ends in I4/Ib4.
// Recognize the whole native envelope, never a prefix or arbitrary log text;
// an appended/split telemetry record must still reach the strict matchers.
func executionSetupTokenDiagnostic(line []byte) bool {
	const timestamp = "2006/01/02 15:04:05 "
	const label = "first-run setup token: "
	const tokenStart = len(timestamp) + len(label)
	if len(line) != tokenStart+43+1 || line[len(line)-1] != '\n' || !bytes.Equal(line[len(timestamp):tokenStart], []byte(label)) {
		return false
	}
	stamp, err := time.Parse(timestamp, string(line[:len(timestamp)]))
	if err != nil || stamp.Format(timestamp) != string(line[:len(timestamp)]) {
		return false
	}
	var token [32]byte
	n, err := base64.RawURLEncoding.Strict().Decode(token[:], line[tokenStart:len(line)-1])
	return err == nil && n == len(token)
}

// Match the existing index parser's reserved lines, plus an embedded binding
// or compact suffix hidden behind an ordinary/fragmented diagnostic line.
func reservedTerminalIndex(line []byte) bool {
	if bytes.HasPrefix(line, []byte("I")) || bytes.HasPrefix(line, []byte("ZI")) || bytes.Contains(line, []byte("IXB")) ||
		bytes.Contains(line, []byte("ZIB")) || bytes.Contains(line, []byte("ZIE")) {
		return true
	}
	index := bytes.LastIndexByte(line, 'I')
	if index < 0 {
		return false
	}
	suffix := line[index:]
	if len(suffix) == 3 && bytes.IndexByte([]byte("0123456789ABCDEF"), suffix[1]) >= 0 {
		return true
	}
	return len(suffix) >= 4 && bytes.IndexByte([]byte("bef"), suffix[1]) >= 0 && bytes.IndexByte([]byte("0123456789ABCDEF"), suffix[2]) >= 0
}
