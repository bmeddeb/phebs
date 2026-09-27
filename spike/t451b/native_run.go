package t451b

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

type NativeConfig struct {
	Socket   string        `json:"socket"`
	Parent   string        `json:"parent"`
	Helper   string        `json:"helper"`
	Bundle   string        `json:"bundle"`
	Manifest string        `json:"manifest"`
	Receipt  string        `json:"receipt"`
	Request  NativeRequest `json:"request"`
}

type NativeReceipt struct {
	HostWallNanoseconds int64             `json:"host_wall_nanoseconds"`
	Schema              string            `json:"schema"`
	Request             NativeRequest     `json:"request"`
	RequestSHA256       string            `json:"request_sha256"`
	Outcome             string            `json:"outcome"`
	Stage               string            `json:"stage"`
	OutputSHA256        string            `json:"output_sha256"`
	OutputBytes         int               `json:"output_bytes"`
	StderrBytes         int               `json:"stderr_bytes"`
	StderrSHA256        string            `json:"stderr_sha256"`
	ExitCode            int               `json:"exit_code"`
	StopReason          string            `json:"stop_reason"`
	OOMKilled           bool              `json:"oom_killed"`
	ContainerID         string            `json:"container_id"`
	Removed             bool              `json:"removed"`
	InputsRemoved       bool              `json:"inputs_removed"`
	Resources           sandbox.Resources `json:"resources"`
	NativeEvidence      json.RawMessage   `json:"native_evidence,omitempty"`
}

func ReadNativeConfig(name string) (NativeConfig, error) {
	b, err := readBounded(name, 16384)
	if err != nil {
		return NativeConfig{}, err
	}
	c, err := decode[NativeConfig](b, 16384, true)
	if err != nil {
		return c, err
	}
	for _, p := range []string{c.Socket, c.Parent, c.Helper, c.Bundle, c.Manifest, c.Receipt} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
			return c, errors.New("native config requires exact absolute paths")
		}
	}
	_, err = DecodeNativeRequest(nativeRequestBytes(c.Request))
	return c, err
}

func RunNativeConfig(ctx context.Context, c NativeConfig) (NativeReceipt, error) {
	manifest, err := t451a.ReadManifest(c.Manifest)
	if err != nil {
		return NativeReceipt{}, err
	}
	f, err := os.OpenFile(c.Receipt, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return NativeReceipt{}, err
	}
	started := time.Now()
	r, runErr := runNative(ctx, c, manifest)
	r.HostWallNanoseconds = time.Since(started).Nanoseconds()
	return r, errors.Join(runErr, json.NewEncoder(f).Encode(r), f.Close())
}

func runNative(ctx context.Context, c NativeConfig, manifest []byte) (NativeReceipt, error) {
	r := NativeReceipt{Schema: "phebs-t451b-native-receipt-v1", Outcome: "STOP", Stage: "admission", ExitCode: -1}
	raw := nativeRequestBytes(c.Request)
	request, err := DecodeNativeRequest(raw)
	if err != nil {
		return r, err
	}
	r.Request = request
	r.RequestSHA256 = t451a.Digest(raw)
	helper, err := readBounded(c.Helper, t451a.MaxFileBytes)
	if err != nil || t451a.Digest(helper) != request.HelperSHA256 {
		return r, errors.New("native helper identity refused")
	}
	bundle, err := t451a.DecodeBundle(manifest)
	if err != nil {
		return r, err
	}
	if err = validateNativeLayout(bundle, request); err != nil {
		return r, err
	}
	result, removed, stage, runErr := runImported(ctx, c.Socket, c.Parent, c.Bundle, request.BundleSHA256, request.ImageID, manifest, raw, helper)
	r.Stage, r.InputsRemoved = stage, removed
	if stage != "sandbox" {
		return r, runErr
	}
	return finishNativeReceipt(r, result, runErr)
}

func finishNativeReceipt(r NativeReceipt, result sandbox.Result, runErr error) (NativeReceipt, error) {
	r.ContainerID, r.Removed, r.ExitCode, r.Resources = result.ContainerID, result.Removed, result.ExitCode, result.Resources
	r.StopReason, r.OOMKilled = result.StopReason, result.OOMKilled
	r.OutputBytes, r.OutputSHA256 = len(result.Stdout), t451a.Digest(result.Stdout)
	r.StderrBytes, r.StderrSHA256 = len(result.Stderr), t451a.Digest(result.Stderr)
	// Decode before examining execution failure: valid typed partial evidence
	// enriches only STOP. Sandbox refusal, cleanup or exit cannot become success.
	evidence, evidenceErr := DecodeNativeEvidence(result.Stdout)
	if evidenceErr == nil && evidence.Request == r.Request {
		r.NativeEvidence = result.Stdout
		r.Stage = evidence.Stage
	} else {
		evidenceErr = errors.Join(evidenceErr, errors.New("native returned request/evidence refused"))
	}
	if runErr != nil {
		return r, fmt.Errorf("native execution refused: %w: %.8192s", errors.Join(runErr, evidenceErr), result.Stderr)
	}
	if result.ExitCode != 0 || !result.Removed || !r.InputsRemoved || !result.Resources.LimitsVerified || evidenceErr != nil || evidence.Decision != "COHORT_OBSERVED" {
		return r, fmt.Errorf("native execution refused: %w: %.8192s", errors.Join(evidenceErr, errors.New("native execution incomplete")), result.Stderr)
	}
	r.Stage = "complete"
	r.Outcome = "NATIVE_COHORT_OBSERVED"
	return r, nil
}

// ReadWorkerProfile retains the original worker boundary. Native work requires
// its distinct entrypoint, inode boundary and request profile.
func ReadWorkerProfile() (Request, *NativeRequest, error) {
	r, err := WorkerRequest()
	return r, nil, err
}

func NativeWorkerRequest() (NativeRequest, error) {
	if err := sandbox.ValidateNativeT451bWorker(); err != nil {
		return NativeRequest{}, err
	}
	b, err := readBounded("/inputs/request.json", maxRequestBytes)
	if err != nil {
		return NativeRequest{}, err
	}
	return DecodeNativeRequest(b)
}
