package t421

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// These are actual native command counters, separate from F's read-only
// ledger and from the independently acknowledged SA transaction/row stream.
type epochSelectorCleanupObservation struct {
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
	reader.failureStatus, reader.failureBody = 0, nil
	return nil
}

func (reader *executionEpochInspection) validateSelectorCleanup(value epochSelectorCleanupObservation, bound SelectorHandoffCleanupPhase, input string) error {
	var phase uint32
	for index, name := range reader.plan.PhaseOrder {
		if name == bound.Phase {
			phase = uint32(index + 1)
		}
	}
	if phase == 0 || reader.plan.SelectorHandoffCleanup == nil || reader.plan.SelectorHandoffCleanup.MaximumDeletesPerTurn != 16 ||
		value.Schema != SelectorHandoffCleanupSchema || value.Phase != phase || value.InputSHA256 != input ||
		value.SelectedRuntimeSHA256 != reader.tail.SelectedRuntimeSHA256 || !validDigest(value.SelectedRuntimeSHA256) ||
		!value.Done || value.Failed || value.Turns < bound.OwnerTurns.Minimum || value.Turns > bound.OwnerTurns.Maximum ||
		value.Deleted > bound.PreimageRowsMaximum+bound.SummaryPreimagesMaximum ||
		value.MaxDeleted > reader.plan.SelectorHandoffCleanup.MaximumDeletesPerTurn || value.MaxDeleted > value.Deleted ||
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
		full := rows / reader.plan.SelectorHandoffCleanup.MaximumDeletesPerTurn
		turns, reads, writes = full+1, 7*full+9, full+1
		if rows%reader.plan.SelectorHandoffCleanup.MaximumDeletesPerTurn != 0 {
			writes++
		}
		if turns > 1 {
			maxDeleted = reader.plan.SelectorHandoffCleanup.MaximumDeletesPerTurn
		}
	}
	if value.Turns != turns || value.StoreReadAttempts != reads || value.StoreWriteAttempts != writes || value.MaxDeleted != maxDeleted {
		return errEpochInspection
	}
	return nil
}
