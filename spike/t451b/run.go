package t451b

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

type Options struct {
	Socket, Parent, Helper, BundleRoot string
	Manifest                           []byte
	Request                            Request
}
type Receipt struct {
	Schema          string            `json:"schema"`
	Request         Request           `json:"request"`
	RequestSHA256   string            `json:"request_sha256"`
	Outcome         string            `json:"outcome"`
	Stage           string            `json:"stage"`
	OutputSHA256    string            `json:"output_sha256"`
	OutputBytes     int               `json:"output_bytes"`
	ExitCode        int               `json:"exit_code"`
	StopReason      string            `json:"stop_reason"`
	OOMKilled       bool              `json:"oom_killed"`
	ContainerID     string            `json:"container_id"`
	Removed         bool              `json:"removed"`
	InputsRemoved   bool              `json:"inputs_removed"`
	Resources       sandbox.Resources `json:"resources"`
	NeutralEvidence json.RawMessage   `json:"neutral_evidence,omitempty"`
}

func Run(ctx context.Context, o Options) (receipt Receipt, err error) {
	receipt = Receipt{Schema: "phebs-t451b-receipt-v1", Outcome: "STOP", Stage: "admission", ExitCode: -1}
	raw, err := json.MarshalIndent(o.Request, "", "  ")
	if err != nil {
		return receipt, err
	}
	raw = append(raw, '\n')
	r, err := DecodeRequest(raw)
	if err != nil {
		return receipt, err
	}
	receipt.Request = r
	receipt.RequestSHA256 = t451a.Digest(raw)
	helper, err := readBounded(o.Helper, t451a.MaxFileBytes)
	if err != nil || t451a.Digest(helper) != r.HelperSHA256 {
		return receipt, errors.New("helper identity refused")
	}
	bundle, err := t451a.DecodeBundle(o.Manifest)
	if err != nil {
		return receipt, err
	}
	if err = validateLayout(bundle, r); err != nil {
		return receipt, err
	}
	result, removed, stage, err := runImported(ctx, o.Socket, o.Parent, o.BundleRoot, r.BundleSHA256, r.ImageID, o.Manifest, raw, helper)
	receipt.InputsRemoved, receipt.Stage = removed, stage
	if stage != "sandbox" {
		return receipt, err
	}
	receipt.ContainerID, receipt.Removed, receipt.ExitCode, receipt.Resources = result.ContainerID, result.Removed, result.ExitCode, result.Resources
	receipt.StopReason, receipt.OOMKilled = result.StopReason, result.OOMKilled
	receipt.OutputBytes, receipt.OutputSHA256 = len(result.Stdout), t451a.Digest(result.Stdout)
	if err != nil {
		return receipt, fmt.Errorf("compatibility execution refused: %w: %.8192s", err, result.Stderr)
	}
	if result.ExitCode != 0 || !result.Removed || !result.Resources.LimitsVerified {
		return receipt, errors.New("compatibility execution evidence refused")
	}
	evidence, err := DecodeEvidence(result.Stdout)
	if err != nil {
		return receipt, err
	}
	if evidence.Request != r {
		return receipt, errors.New("neutral evidence request binding mismatch")
	}
	var compact bytes.Buffer
	if err = json.Compact(&compact, result.Stdout); err != nil || !bytes.Equal(append(compact.Bytes(), '\n'), result.Stdout) {
		return receipt, errors.New("compatibility evidence encoding refused")
	}
	receipt.NeutralEvidence = result.Stdout
	receipt.Stage = "complete"
	receipt.Outcome = "NEUTRAL_COMPATIBILITY_OBSERVED"
	return receipt, nil
}

func WorkerRequest() (Request, error) {
	if err := sandbox.ValidateWorker(); err != nil {
		return Request{}, err
	}
	data, err := readBounded("/inputs/request.json", maxRequestBytes)
	if err != nil {
		return Request{}, err
	}
	return DecodeRequest(data)
}

// runImported is the shared custody boundary for both closed request profiles.
// Its caller validates the request and complete input layout before entry.
func runImported(ctx context.Context, socket, parentPath, bundleRoot, bundleDigest, imageID string, manifest, raw, helper []byte) (result sandbox.Result, removed bool, stage string, err error) {
	stage = "admission"
	parent, err := os.MkdirTemp(parentPath, "t451b-run-")
	if err != nil {
		return result, removed, stage, err
	}
	inputs := ""
	defer func() {
		if inputs != "" {
			if _, journalErr := os.Lstat(inputs + ".t451a-container.json"); !errors.Is(journalErr, os.ErrNotExist) {
				err = errors.Join(err, sandbox.ErrCustody)
				return
			}
		}
		cleanupErr := os.RemoveAll(parent)
		removed = cleanupErr == nil
		err = errors.Join(err, cleanupErr)
	}()
	inputs, err = t451a.ImportBundle(ctx, bundleRoot, parent, manifest, bundleDigest)
	if err != nil {
		return result, removed, stage, err
	}
	if err = os.WriteFile(filepath.Join(inputs, "t451a"), helper, 0500); err != nil {
		return result, removed, stage, err
	}
	if err = os.WriteFile(filepath.Join(inputs, "request.json"), raw, 0400); err != nil {
		return result, removed, stage, err
	}
	if err = filepath.WalkDir(inputs, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := fs.FileMode(0444)
		if info.Mode().Perm()&0100 != 0 {
			mode = 0555
		}
		if entry.IsDir() {
			mode = 0755
		}
		return os.Chmod(name, mode)
	}); err != nil {
		return result, removed, stage, err
	}
	stage = "sandbox"
	result, err = sandbox.Run(ctx, sandbox.Options{Socket: socket, ImageID: imageID, Inputs: inputs})
	return result, removed, stage, err
}
