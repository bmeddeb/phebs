package provider

import (
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
)

const preparationFailurePrefix = 16 << 10
const preparationFailureBytes = 32 << 10

// The raw prefix stays in private returned evidence. Logs expose no tool text.
func preparationCommandFailure(index int, stderr []byte, err error) []byte {
	exit := -1
	var ended *exec.ExitError
	if errors.As(err, &ended) {
		exit = ended.ExitCode()
	}
	prefix := stderr
	if len(prefix) > preparationFailurePrefix {
		prefix = prefix[:preparationFailurePrefix]
	}
	raw, _ := json.Marshal(struct {
		Command      int    `json:"command"`
		Exit         int    `json:"exit"`
		StderrBytes  int    `json:"stderr_bytes"`
		StderrSHA    string `json:"stderr_sha256"`
		StderrPrefix []byte `json:"stderr_prefix"`
	}{index, exit, len(stderr), preparationDigest(stderr), prefix})
	return append(raw, '\n')
}

func TestPreparationCommandFailure(t *testing.T) {
	for _, size := range []int{0, 12, preparationFailurePrefix, preparationFailurePrefix + 1} {
		stderr := make([]byte, size)
		raw := preparationCommandFailure(1, stderr, errors.New("private error"))
		if len(raw) >= preparationFailureBytes {
			t.Fatal("failure frame overflow")
		}
		var got struct {
			Command      int
			Exit         int
			StderrBytes  int    `json:"stderr_bytes"`
			StderrSHA    string `json:"stderr_sha256"`
			StderrPrefix []byte `json:"stderr_prefix"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Command != 1 || got.Exit != -1 || got.StderrBytes != size || got.StderrSHA != preparationDigest(stderr) || len(got.StderrPrefix) != min(size, preparationFailurePrefix) {
			t.Fatal("private failure facts")
		}
	}
}
