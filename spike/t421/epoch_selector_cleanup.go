package t421

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// SelectorCleanupEvidence contains actual native command counters, separate
// from F's read-only ledger and the independently acknowledged SA stream.
type SelectorCleanupEvidence struct {
	Schema                string `json:"schema"`
	Phase                 uint32 `json:"phase"`
	InputSHA256           string `json:"input_sha256"`
	SelectedRuntimeSHA256 string `json:"selected_runtime_sha256"`
	Turns                 uint64 `json:"turns"`
	Deleted               uint64 `json:"deleted"`
	MaxDeleted            uint64 `json:"max_deleted"`
	StoreReadAttempts     uint64 `json:"store_read_attempts"`
	StoreWriteAttempts    uint64 `json:"store_write_attempts"`
	Done                  bool   `json:"done"`
	Failed                bool   `json:"failed"`
}

type epochSelectorCleanupObservation = SelectorCleanupEvidence

// The caller owns the existing phase deadline and drained/open request window.
// There is one POST, no retry and no phase advance before its final fence joins
// the server report tail. An omitted policy preserves the former choreography.
func (reader *executionEpochInspection) cleanupSelectorHandoff(ctx context.Context) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() {
		if retErr != nil && reader.readFailure.Stage == "" {
			reader.readFailure = epochReadFailure{Stage: "selector_handoff_cleanup", Cause: retErr}
		}
		reader.fail(retErr)
	}()
	if reader.plan.SelectorHandoffCleanup == nil {
		return nil
	}
	bound, ok := SelectorHandoffCleanupForPhase(reader.plan, reader.projection.Phase)
	if !ok || ctx == nil || ctx.Err() != nil || reader.err != nil || !reader.finalUsed ||
		reader.selectorCleanupPhase == reader.projection.Phase || reader.run == nil || reader.run.control == nil ||
		reader.run.epoch.Epoch != bound.ServerEpoch {
		return errEpochInspection
	}
	reader.selectorCleanupPhase = reader.projection.Phase
	var evidence *ExecutionPhaseInspection
	if reader.plan.Schema == PlanV5Schema {
		if len(reader.evidence.rows) == 0 {
			return errEpochInspection
		}
		evidence = &reader.evidence.rows[len(reader.evidence.rows)-1]
		if evidence.Phase != reader.projection.Phase || evidence.ServerEpoch != bound.ServerEpoch ||
			evidence.Final == nil || evidence.SelectorAccepted || evidence.SelectorCleanup != nil {
			return errEpochInspection
		}
	}
	run := reader.run
	run.mu.Lock()
	valid := !run.stopping && run.err == nil && run.attemptInput != ([32]byte{})
	input := run.attemptInput
	run.mu.Unlock()
	token := run.control.RequestToken()
	if !valid || token == "" {
		return errEpochInspection
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+run.epoch.Listen+"/api/t422/selector-handoff/cleanup", nil)
	if err != nil {
		return errEpochInspection
	}
	request.Header.Set("Authorization", "Bearer "+run.epoch.APIKey)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, token)
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errEpochInspection
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	closeErr := response.Body.Close()
	// Private bounded failure custody only; it is not an F ordinal/report.
	reader.failureStatus, reader.failureBody = response.StatusCode, append([]byte(nil), raw...)
	var value epochSelectorCleanupObservation
	if readErr != nil || closeErr != nil || len(raw) > 4096 || response.StatusCode != http.StatusOK ||
		response.Uncompressed || response.Header.Get("Content-Encoding") != "" || len(response.Trailer) != 0 ||
		len(response.Header.Values(epochReadTrailer)) != 0 || decodeEpochJSON(raw, &value, false) != nil ||
		ctx.Err() != nil || reader.validateSelectorCleanup(value, bound, "sha256:"+hex.EncodeToString(input[:])) != nil {
		return errEpochInspection
	}
	reader.selectorCleanup = value
	if evidence != nil {
		evidence.SelectorCleanup = &value
	}
	reader.failureStatus, reader.failureBody = 0, nil
	return nil
}

func (reader *executionEpochInspection) validateSelectorCleanup(value epochSelectorCleanupObservation, bound SelectorHandoffCleanupPhase, input string) error {
	return validateSelectorCleanupObservation(reader.plan, value, bound, input, reader.tail.SelectedRuntimeSHA256)
}

func validateSelectorCleanupObservation(plan Plan, value SelectorCleanupEvidence, bound SelectorHandoffCleanupPhase, input, selected string) error {
	var phase uint32
	for index, name := range plan.PhaseOrder {
		if name == bound.Phase {
			phase = uint32(index + 1)
		}
	}
	maximumDeleted, boundErr := checkedInspectionReadSum(bound.PreimageRowsMaximum, bound.SummaryPreimagesMaximum)
	if boundErr != nil || phase == 0 || plan.SelectorHandoffCleanup == nil || plan.SelectorHandoffCleanup.MaximumDeletesPerTurn != 16 ||
		value.Schema != SelectorHandoffCleanupSchema || value.Phase != phase || value.InputSHA256 != input ||
		value.SelectedRuntimeSHA256 != selected || !validDigest(value.SelectedRuntimeSHA256) ||
		!value.Done || value.Failed || value.Turns < bound.OwnerTurns.Minimum || value.Turns > bound.OwnerTurns.Maximum ||
		value.Deleted > maximumDeleted ||
		value.MaxDeleted > plan.SelectorHandoffCleanup.MaximumDeletesPerTurn || value.MaxDeleted > value.Deleted ||
		(value.Deleted == 0) != (value.MaxDeleted == 0) ||
		value.StoreReadAttempts < bound.StoreReadAttempts.Minimum || value.StoreReadAttempts > bound.StoreReadAttempts.Maximum ||
		value.StoreWriteAttempts < bound.StoreWriteAttempts.Minimum || value.StoreWriteAttempts > bound.StoreWriteAttempts.Maximum {
		return errEpochInspection
	}
	// Pin the successful native turn composition used to derive the tighter
	// read ceilings. This checks reported facts; it does not mint SA counters.
	turns, reads, writes, maxDeleted := uint64(1), uint64(3), uint64(0), value.Deleted
	if value.Deleted != 0 {
		rows := value.Deleted - 1 // The final committed deletion owns the summary.
		full := rows / plan.SelectorHandoffCleanup.MaximumDeletesPerTurn
		turns, writes = full+1, full+1
		var err error
		reads, err = checkedMultiply(full, 7)
		if err != nil {
			return errEpochInspection
		}
		reads, err = checkedInspectionReadSum(reads, 9)
		if err != nil {
			return errEpochInspection
		}
		if rows%plan.SelectorHandoffCleanup.MaximumDeletesPerTurn != 0 {
			writes++
		}
		if turns > 1 {
			maxDeleted = plan.SelectorHandoffCleanup.MaximumDeletesPerTurn
		}
	}
	if value.Turns != turns || value.StoreReadAttempts != reads || value.StoreWriteAttempts != writes || value.MaxDeleted != maxDeleted {
		return errEpochInspection
	}
	return nil
}
