package t421

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"
)

const (
	executionLinuxPreworkOuterMode      = "run-t422-linux-prework-outer-v1"
	executionLinuxPreworkInnerMode      = "run-t422-linux-prework-inner-v1"
	executionLinuxPreworkSchema         = "t422-linux-executor-prework-v1"
	executionLinuxPreworkMaximum        = 60 * time.Minute
	executionLinuxPreworkComplete       = "t422-linux-executor-prework-joined-v1\n"
	executionLinuxPreworkRetiredFailure = "t422-linux-executor-prework-retired-failure-v1\n"
)

// These selections are untrusted input. Only independent protected reference
// comparison in the actual inner process can issue its private live binding.
type linuxExecutorPreworkSelection struct {
	Schema                string                    `json:"schema"`
	PreparationRoot       string                    `json:"preparation_root"`
	RepositoryRoot        string                    `json:"repository_root"`
	PlanSourceCommit      string                    `json:"plan_source_commit"`
	IntegratedMainCommit  string                    `json:"integrated_main_commit"`
	SourceCommit          string                    `json:"source_commit"`
	GoRoot                string                    `json:"go_root"`
	ModuleCache           string                    `json:"module_cache"`
	GitBinary             string                    `json:"git_binary"`
	InputHelperBinary     string                    `json:"input_helper_binary"`
	ReferenceHelperBinary string                    `json:"reference_helper_binary"`
	DeadlineUnixNano      int64                     `json:"deadline_unix_nano"`
	ParentPID             int                       `json:"parent_pid"`
	ParentStart           string                    `json:"parent_start"`
	ReadDevice            uint64                    `json:"read_device"`
	ReadInode             uint64                    `json:"read_inode"`
	Attempt               string                    `json:"attempt"`
	ExpectedInput         linuxExecutorPreworkInput `json:"expected_input"`
	ExpectedGoSHA256      string                    `json:"expected_go_sha256"`
}

// Comparison data only: the inner must independently issue all these facts.
type linuxExecutorPreworkInput struct {
	ManifestSHA256 string `json:"manifest_sha256"`
	HelperSHA256   string `json:"helper_sha256"`
	Entries        uint64 `json:"entries"`
	Files          uint64 `json:"files"`
	Bytes          uint64 `json:"bytes"`
	PathBytes      uint64 `json:"path_bytes"`
}

func validLinuxExecutorAttempt(attempt string) bool {
	value, err := hex.DecodeString(attempt)
	return err == nil && len(value) == 16 && hex.EncodeToString(value) == attempt
}

func linuxExecutorPreworkReceipt(attempt string, failed bool) string {
	prefix := executionLinuxPreworkComplete
	if failed {
		prefix = executionLinuxPreworkRetiredFailure
	}
	return prefix[:len(prefix)-1] + ":" + attempt + "\n"
}

func linuxExecutorPreworkRetired(receipt, attempt string) bool {
	return validLinuxExecutorAttempt(attempt) && (receipt == linuxExecutorPreworkReceipt(attempt, false) || receipt == linuxExecutorPreworkReceipt(attempt, true))
}

func encodeLinuxExecutorPrework(value linuxExecutorPreworkSelection) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxExecutionSelectionBytes {
		return "", ErrExecutionLauncher
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeLinuxExecutorPrework(encoded string) (linuxExecutorPreworkSelection, error) {
	var value linuxExecutorPreworkSelection
	if len(encoded) == 0 || len(encoded) > maxExecutionSelectionCharacters {
		return value, ErrExecutionLauncher
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) > maxExecutionSelectionBytes || base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return value, ErrExecutionLauncher
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return value, ErrExecutionLauncher
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) || value.Schema != executionLinuxPreworkSchema {
		return value, ErrExecutionLauncher
	}
	return value, nil
}
