package typedsandbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
)

const MaxAllowanceBytes = 1024
const allowanceSchema = "phebs-typed-allowance-v1"

// Allowance is trusted controller authority shared by exactly two invocations.
// It does not prove readiness, a clock domain or completed output by itself.
type Allowance struct {
	Schema          string `json:"schema"`
	PlanningDigest  string `json:"planning_digest"`
	AttemptDigest   string `json:"attempt_digest"`
	BootID          string `json:"boot_id"`
	TimeDevice      uint64 `json:"time_device"`
	TimeInode       uint64 `json:"time_inode"`
	Start           int64  `json:"start_boottime_ns"`
	Deadline        int64  `json:"deadline_boottime_ns"`
	WorkerBytesUsed int64  `json:"worker_bytes_used"`
	WireBytesUsed   int64  `json:"wire_bytes_used"`
}

func (a Allowance) Validate() error {
	if a.Schema != allowanceSchema || !hostDigest(a.PlanningDigest) || !hostDigest(a.AttemptDigest) || !bootUUID(a.BootID) || a.TimeDevice == 0 || a.TimeInode == 0 || a.Start <= 0 || a.Start > math.MaxInt64-int64(WallLimit) || a.Deadline != a.Start+int64(WallLimit) || a.WorkerBytesUsed < 0 || a.WorkerBytesUsed > OutputBytes || a.WireBytesUsed < 0 || a.WireBytesUsed > maxWireBytes {
		return ErrRefused
	}
	return nil
}
func bootUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func EncodeAllowance(a Allowance) ([]byte, error) {
	if a.Validate() != nil {
		return nil, ErrRefused
	}
	raw, err := json.Marshal(a)
	if err != nil || len(raw) > MaxAllowanceBytes {
		return nil, ErrRefused
	}
	return raw, nil
}
func DecodeAllowance(raw []byte) (Allowance, error) {
	var a Allowance
	if len(raw) == 0 || len(raw) > MaxAllowanceBytes || json.Unmarshal(raw, &a) != nil {
		return a, ErrRefused
	}
	canonical, err := EncodeAllowance(a)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Allowance{}, ErrRefused
	}
	return a, nil
}
func (a Allowance) invocation(phase, request string) bool {
	if a.Validate() != nil || !hostDigest(request) {
		return false
	}
	return phase == ControlPlan && request == a.PlanningDigest && a.WorkerBytesUsed == 0 && a.WireBytesUsed == 0 || phase == ControlExecute && request != a.PlanningDigest
}
func (a Allowance) remaining(now int64) (int64, error) {
	if a.Validate() != nil || now < a.Start || now >= a.Deadline {
		return 0, ErrRefused
	}
	return a.Deadline - now, nil
}

// completion is never reconstructed from exported Result fields or receipts.
// Only a successful exact report followed by joined container removal mints it.
type completion struct {
	allowance    Allowance
	worker, wire int64
	control      ControlIdentity
	output       [32]byte
}

func AdvanceAllowance(original Allowance, result Result) (Allowance, error) {
	token := result.completed
	if token == nil || token.control.Phase != ControlPlan || VerifyCompletion(original, token.control, result) != nil {
		return Allowance{}, ErrRefused
	}
	if original.Validate() != nil || token == nil || token.allowance != original || original.WorkerBytesUsed != 0 || original.WireBytesUsed != 0 || token.worker < 0 || token.worker > OutputBytes || token.wire < 0 || token.wire > maxWireBytes {
		return Allowance{}, ErrRefused
	}
	original.WorkerBytesUsed = token.worker
	original.WireBytesUsed = token.wire
	return original, nil
}

func supervisorArgs(o Options) []string {
	raw, _ := EncodeAllowance(o.Allowance)
	return []string{SupervisorCommand, o.Control.Phase, o.Control.RequestDigest, string(raw), o.Control.SealDigest}
}
func parseSupervisorArgs(args []string) (Allowance, string, string, error) {
	if len(args) != 5 || args[0] != SupervisorCommand || !hostDigest(args[4]) {
		return Allowance{}, "", "", ErrRefused
	}
	a, err := DecodeAllowance([]byte(args[3]))
	if err != nil || !a.invocation(args[1], args[2]) {
		return Allowance{}, "", "", ErrRefused
	}
	return a, args[1], args[2], nil
}

// The workspace owns full canonical seal validation. This independent check
// binds the allowance bytes and phase/owner identity under its trusted seal hash.
func checkSealAllowance(raw []byte, a Allowance, phase, request string) error {
	var seal struct {
		Schema   string `json:"schema"`
		Identity struct {
			Allowance      json.RawMessage `json:"allowance"`
			Phase          string          `json:"phase"`
			PlanningDigest string          `json:"planning_digest"`
			AttemptDigest  string          `json:"attempt_digest"`
			RequestDigest  string          `json:"request_digest"`
		} `json:"identity"`
	}
	if len(raw) > MaxControlSealBytes || json.Unmarshal(raw, &seal) != nil || seal.Schema != "phebs-typed-worker-controls-v3" || !a.invocation(phase, request) {
		return ErrRefused
	}
	got, err := DecodeAllowance(seal.Identity.Allowance)
	if err != nil || got != a || seal.Identity.Phase != phase || seal.Identity.PlanningDigest != a.PlanningDigest || seal.Identity.AttemptDigest != a.AttemptDigest || seal.Identity.RequestDigest != request {
		return ErrRefused
	}
	return nil
}

func invocationDigest(a Allowance, phase, request, seal string) string {
	raw, _ := json.Marshal(supervisorArgs(Options{Allowance: a, Control: ControlIdentity{Phase: phase, RequestDigest: request, SealDigest: seal}}))
	return controlDigest(raw)
}

// VerifyCompletion authenticates successful, joined output from one exact
// invocation. Public Result fields and mutable output slices cannot mint it.
func VerifyCompletion(a Allowance, control ControlIdentity, result Result) error {
	token := result.completed
	if a.Validate() != nil || control.Validate() != nil || token == nil || token.allowance != a || token.control != control || !a.invocation(control.Phase, control.RequestDigest) || control.PlanningDigest != a.PlanningDigest || control.AttemptDigest != a.AttemptDigest || len(result.Stdout) > OutputBytes || len(result.Stderr) > OutputBytes-len(result.Stdout) || token.output != completionOutput(result.Stdout, result.Stderr) || result.ExitCode != 0 || !result.Removed || result.OOMKilled || result.StopReason != "" {
		return ErrRefused
	}
	return nil
}
func completionOutput(stdout, stderr []byte) [32]byte {
	h := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(stdout)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(stdout)
	binary.BigEndian.PutUint64(length[:], uint64(len(stderr)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(stderr)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
