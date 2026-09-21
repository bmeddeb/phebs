//go:build darwin

package t421

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const (
	executionPreclaimFailureSchema   = "t422-source-free-preclaim-failure-v1"
	maxExecutionPreclaimFailureBytes = 256

	executionPreclaimStagePreflight           executionPreclaimStage = "preflight"
	executionPreclaimStageOperationalRoot     executionPreclaimStage = "operational_root"
	executionPreclaimStagePressureVolume      executionPreclaimStage = "pressure_volume"
	executionPreclaimStageWorkspace           executionPreclaimStage = "workspace"
	executionPreclaimStageSignerCustody       executionPreclaimStage = "signer_custody"
	executionPreclaimStageGitCustody          executionPreclaimStage = "git_custody"
	executionPreclaimStageGoBuildInputs       executionPreclaimStage = "go_build_inputs"
	executionPreclaimStageReferenceCandidates executionPreclaimStage = "reference_candidates"
	executionPreclaimStageReferenceTools      executionPreclaimStage = "reference_tools"
	executionPreclaimStageSurrealCustody      executionPreclaimStage = "surreal_custody"
	executionPreclaimStagePlanConstruction    executionPreclaimStage = "plan_construction"
	executionPreclaimStagePlanInputCustody    executionPreclaimStage = "plan_input_custody"
	executionPreclaimStageAuthorCustody       executionPreclaimStage = "author_custody"
	executionPreclaimStageEpochConfigs        executionPreclaimStage = "epoch_configs"
	executionPreclaimStageEpochOne            executionPreclaimStage = "epoch_one"
	executionPreclaimStageProfileTools        executionPreclaimStage = "profile_tools"
	executionPreclaimStageProfileSigner       executionPreclaimStage = "profile_signer"
	executionPreclaimStageProfileNamespace    executionPreclaimStage = "profile_signer_namespace"
	executionPreclaimStageProfileExecutor     executionPreclaimStage = "profile_executor"
	executionPreclaimStagePressureBallast     executionPreclaimStage = "pressure_ballast"
	executionPreclaimStagePressureSample      executionPreclaimStage = "pressure_sample"
	executionPreclaimStageRehearsalBinding    executionPreclaimStage = "rehearsal_binding"
	executionPreclaimStageProfileHost         executionPreclaimStage = "profile_host"
	executionPreclaimStageProfileEnvironment  executionPreclaimStage = "profile_environment"
	executionPreclaimStageProfileRuntime      executionPreclaimStage = "profile_runtime"
	executionPreclaimStageProfileIssue        executionPreclaimStage = "profile_issue"
	executionPreclaimStageParentImage         executionPreclaimStage = "parent_image"
	executionPreclaimStageHandoffProjection   executionPreclaimStage = "handoff_projection"
	executionPreclaimStageSignerNamespace     executionPreclaimStage = "signer_namespace"
	executionPreclaimStageCeremonyClaim       executionPreclaimStage = "ceremony_claim"
)

type executionPreclaimStage string

type executionPreclaimFailureV1 struct {
	Schema  string                 `json:"schema"`
	Stage   executionPreclaimStage `json:"stage"`
	Cleanup string                 `json:"cleanup"`
}

func validExecutionPreclaimStage(stage executionPreclaimStage) bool {
	switch stage {
	case executionPreclaimStagePreflight,
		executionPreclaimStageOperationalRoot,
		executionPreclaimStagePressureVolume,
		executionPreclaimStageWorkspace,
		executionPreclaimStageSignerCustody,
		executionPreclaimStageGitCustody,
		executionPreclaimStageGoBuildInputs,
		executionPreclaimStageReferenceCandidates,
		executionPreclaimStageReferenceTools,
		executionPreclaimStageSurrealCustody,
		executionPreclaimStagePlanConstruction,
		executionPreclaimStagePlanInputCustody,
		executionPreclaimStageAuthorCustody,
		executionPreclaimStageEpochConfigs,
		executionPreclaimStageEpochOne,
		executionPreclaimStageProfileTools,
		executionPreclaimStageProfileSigner,
		executionPreclaimStageProfileNamespace,
		executionPreclaimStageProfileExecutor,
		executionPreclaimStagePressureBallast,
		executionPreclaimStagePressureSample,
		executionPreclaimStageRehearsalBinding,
		executionPreclaimStageProfileHost,
		executionPreclaimStageProfileEnvironment,
		executionPreclaimStageProfileRuntime,
		executionPreclaimStageProfileIssue,
		executionPreclaimStageParentImage,
		executionPreclaimStageHandoffProjection,
		executionPreclaimStageSignerNamespace,
		executionPreclaimStageCeremonyClaim:
		return true
	default:
		return false
	}
}

func canonicalExecutionPreclaimFailure(value executionPreclaimFailureV1) ([]byte, error) {
	if value.Schema != executionPreclaimFailureSchema || !validExecutionPreclaimStage(value.Stage) ||
		(value.Cleanup != "clean" && value.Cleanup != "retained_or_unavailable") {
		return nil, errExecutionAuthorization
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw)+1 > maxExecutionPreclaimFailureBytes {
		return nil, errExecutionAuthorization
	}
	return append(raw, '\n'), nil
}

func decodeExecutionPreclaimFailure(raw []byte) (executionPreclaimFailureV1, error) {
	var value executionPreclaimFailureV1
	if len(raw) < 2 || len(raw) > maxExecutionPreclaimFailureBytes || raw[len(raw)-1] != '\n' || bytes.Count(raw, []byte{'\n'}) != 1 {
		return value, errExecutionAuthorization
	}
	decoder := json.NewDecoder(bytes.NewReader(raw[:len(raw)-1]))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || !executionJSONEOF(decoder) {
		return executionPreclaimFailureV1{}, errExecutionAuthorization
	}
	canonical, err := canonicalExecutionPreclaimFailure(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return executionPreclaimFailureV1{}, errExecutionAuthorization
	}
	return value, nil
}

type executionAuthorizationHandoffFrame struct {
	value    executionAuthorizationHandoffV1
	preclaim executionPreclaimFailureV1
	raw      []byte
	err      error
}

// Both message types share one buffered reader, preserving coalesced bytes.
func readExecutionAuthorizationHandoff(buffered *bufio.Reader) executionAuthorizationHandoffFrame {
	raw, err := buffered.ReadSlice('\n')
	if err != nil || len(raw) < 2 || len(raw) > maxExecutionAuthorizationHandoffFrameBytes {
		return executionAuthorizationHandoffFrame{err: errExecutionAuthorization}
	}
	raw = bytes.Clone(raw)
	value, err := decodeExecutionAuthorizationHandoff(raw)
	if err == nil {
		return executionAuthorizationHandoffFrame{value: value, raw: raw}
	}
	preclaim, err := decodeExecutionPreclaimFailure(raw)
	if err != nil {
		return executionAuthorizationHandoffFrame{err: errExecutionAuthorization}
	}
	return executionAuthorizationHandoffFrame{preclaim: preclaim, raw: raw}
}

type executionReturnedOutput struct {
	raw []byte
	err error
}

func captureExecutionReturnedOutput(reader io.Reader, frame chan<- executionAuthorizationHandoffFrame, returned chan<- executionReturnedOutput) {
	buffered := bufio.NewReaderSize(reader, maxExecutionAuthorizationHandoffFrameBytes)
	captured := readExecutionAuthorizationHandoff(buffered)
	frame <- captured
	if captured.err != nil {
		return
	}
	if captured.preclaim.Schema != "" {
		_, err := buffered.ReadByte()
		if errors.Is(err, io.EOF) {
			err = nil
		} else {
			err = errExecutionAuthorization
		}
		returned <- executionReturnedOutput{err: err}
		return
	}
	raw, err := captureExecutionReturnedPackage(buffered)
	returned <- executionReturnedOutput{raw: raw, err: err}
}

func emitExecutionPreclaimFailure(ctx context.Context, output *executionAuthorizationOutput, value executionPreclaimFailureV1) error {
	if ctx == nil || ctx.Err() != nil || output == nil || output.used {
		return errExecutionAuthorization
	}
	raw, err := canonicalExecutionPreclaimFailure(value)
	if err != nil {
		return errExecutionAuthorization
	}
	output.used = true
	return writeExecutionOutput(ctx, output, raw, output.deadline)
}

func forwardExecutionAuthorizationHandoff(ctx context.Context, output *executionAuthorizationOutput, raw []byte) (retErr error) {
	if ctx == nil || ctx.Err() != nil || output == nil || output.used || len(raw) < 2 || len(raw) > maxExecutionAuthorizationHandoffFrameBytes {
		return errExecutionAuthorization
	}
	output.used = true
	value, err := decodeExecutionAuthorizationHandoff(raw)
	if err != nil || output.check(ctx) != nil {
		return errExecutionAuthorization
	}
	deadline := time.Unix(0, value.FinalAdmissionDeadlineUnixNano)
	return writeExecutionOutput(ctx, output, raw, deadline)
}

// writeExecutionOutput preserves one cancellation/deadline corridor for both
// the early authorization handoff and the later authenticated package.
func writeExecutionOutput(ctx context.Context, output *executionAuthorizationOutput, raw []byte, deadline time.Time) (retErr error) {
	if ctx == nil || ctx.Err() != nil || output == nil || output.check(ctx) != nil {
		return errExecutionAuthorization
	}
	if output.deadline.Before(deadline) {
		deadline = output.deadline
	}
	if selected, ok := ctx.Deadline(); ok && selected.Before(deadline) {
		deadline = selected
	}
	if !time.Now().Before(deadline) || output.file.SetWriteDeadline(deadline) != nil || ctx.Err() != nil {
		return errExecutionAuthorization
	}
	joined := make(chan struct{})
	var cancellationErr error
	stop := context.AfterFunc(ctx, func() {
		cancellationErr = output.file.SetWriteDeadline(time.Now())
		close(joined)
	})
	defer func() {
		if !stop() {
			<-joined
		}
		if cancellationErr != nil || ctx.Err() != nil || !time.Now().Before(deadline) {
			retErr = errExecutionAuthorization
		}
	}()
	written, err := output.file.Write(raw)
	if err != nil || written != len(raw) || output.check(ctx) != nil {
		return errExecutionAuthorization
	}
	return nil
}
