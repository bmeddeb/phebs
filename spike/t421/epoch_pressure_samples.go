package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Actual synchronous boundary observations, separate from the joined lifecycle
// stream. Failed suffixes retain completed maxima, never partial walk totals.
type ExecutionPressureSamples struct {
	Phases                               [3]ExecutionWorkspaceBytePhase
	Complete, Unavailable, LimitExceeded bool
}

// Reuse the exact checkpoint predecessor/closed-producer rules while checking
// the genuine phase-eleven endpoint. Never relabel a result as phase eight.
func epochPressureClosedPrefix(ctx context.Context, result ExecutionEpochOneResult) bool {
	if !result.PressureSamples.Complete || result.PressureSamples.Unavailable || result.PressureSamples.LimitExceeded || len(result.Inspection) != 4 {
		return false
	}
	for i, count := range []uint64{4, 3, 4} {
		row := result.PressureSamples.Phases[i]
		if row.Attempts != count || row.Completed != count {
			return false
		}
	}
	for i, phase := range []string{"process_restart", "pressure_80", "pressure_90", "pressure_75"} {
		row := result.Inspection[i]
		if row.ServerEpoch != 4 || row.Phase != phase || row.Final == nil || !row.SelectorAccepted {
			return false
		}
	}
	return epochCheckpointClosedPrefixAt(ctx, result, false, 11)
}

func pressureSampleExpected(phase string, step, ordinal uint8, point string) bool {
	sequence := [...]struct {
		phase, point string
		step         uint8
	}{
		{"pressure_80", "start", 1}, {"pressure_80", "normalized", 3},
		{"pressure_80", "ballast", 3}, {"pressure_80", "finish", 4},
		{"pressure_90", "start", 4}, {"pressure_90", "ballast", 4}, {"pressure_90", "finish", 5},
		{"pressure_75", "start", 5}, {"pressure_75", "ballast", 5},
		{"pressure_75", "removed", 6}, {"pressure_75", "finish", 9},
	}
	return int(ordinal) < len(sequence) && sequence[ordinal].phase == phase && sequence[ordinal].point == point && sequence[ordinal].step == step
}

func (reader *executionEpochInspection) pressureSample(ctx context.Context, point string) (value custodybytes.Sample, retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() {
		reader.fail(retErr)
		if retErr != nil {
			reader.pressure.samples.Complete = false
			if !reader.pressure.samples.LimitExceeded {
				reader.pressure.samples.Unavailable = true
			}
		}
	}()
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.run == nil || !reader.run.pressureAllowed || reader.run.epoch.Epoch != 4 ||
		reader.run.flow == nil || reader.run.flow.workspace == nil || reader.run.control == nil ||
		!pressureSampleExpected(reader.projection.Phase, reader.pressure.step, reader.pressure.sampleOrdinal, point) ||
		(point == "finish") != reader.finalUsed {
		return value, errEpochInspection
	}
	token := reader.run.control.RequestToken()
	if token == "" {
		return value, errEpochInspection
	}
	phase := 0
	if reader.projection.Phase == "pressure_90" {
		phase = 1
	}
	if reader.projection.Phase == "pressure_75" {
		phase = 2
	}
	row := &reader.pressure.samples.Phases[phase]
	row.Attempts++
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+reader.run.epoch.Listen+"/api/t422/lifecycle/sample-workspace", nil)
	if err != nil {
		return value, errEpochInspection
	}
	request.Header.Set("Authorization", "Bearer "+reader.run.epoch.APIKey)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, token)
	request.Header.Set("X-Phebs-T422-Workspace-Point", point)
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return value, errEpochInspection
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 257))
	reader.failureStatus, reader.failureBody, reader.failureOrdinal = response.StatusCode, raw, 0
	closeErr := response.Body.Close()
	var wire struct {
		LogicalBytes   uint64 `json:"logical_bytes"`
		AllocatedBytes uint64 `json:"allocated_bytes"`
	}
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || response.Uncompressed || response.Header.Get("Content-Encoding") != "" ||
		len(response.Trailer) != 0 || len(response.Header.Values(epochReadTrailer)) != 0 || decodeEpochJSON(append(raw, '\n'), &wire, false) != nil {
		return value, errEpochInspection
	}
	canonical, err := json.Marshal(wire)
	if err != nil || !bytes.Equal(raw, canonical) {
		return value, errEpochInspection
	}
	value = custodybytes.Sample{LogicalBytes: wire.LogicalBytes, AllocatedBytes: wire.AllocatedBytes}
	row.Completed++
	row.Maximum.LogicalBytes = max(row.Maximum.LogicalBytes, value.LogicalBytes)
	row.Maximum.AllocatedBytes = max(row.Maximum.AllocatedBytes, value.AllocatedBytes)
	if value.LogicalBytes > reader.plan.WorkEnvelope.MaximumDataLogicalBytes || value.AllocatedBytes > reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes {
		reader.pressure.samples.LimitExceeded = true
		return value, errEpochInspection
	}
	if ctx.Err() != nil {
		return value, errEpochInspection
	}
	reader.pressure.sampleOrdinal++
	return value, nil
}
