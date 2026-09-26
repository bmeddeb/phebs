package t451a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

type Options struct {
	Socket, Parent, Helper, BundleRoot string
	Manifest                           []byte
	Request                            Request
}

// Receipt excludes daemon messages, raw diagnostics and local host paths.
// A successful neutral plan is retained in full beside its exact output digest.
// Individual observations are not the independently reviewed T45.1a PASS gate.
type Receipt struct {
	Schema          string            `json:"schema"`
	RequestSHA256   string            `json:"request_sha256"`
	Request         *Request          `json:"request,omitempty"`
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

// Run has no target-repository argument. The private parent and copied inputs
// remain intact whenever the exact container's absence cannot be established.
func Run(ctx context.Context, options Options) (receipt Receipt, err error) {
	receipt = Receipt{Schema: "phebs-t451a-receipt-v1", Outcome: "STOP", Stage: "admission", ExitCode: -1}
	requestBytes, err := json.MarshalIndent(options.Request, "", "  ")
	if err != nil {
		return receipt, err
	}
	requestBytes = append(requestBytes, '\n')
	request, err := DecodeRequest(requestBytes)
	if err != nil {
		return receipt, err
	}
	receipt.RequestSHA256 = Digest(requestBytes)
	receipt.Request = &request
	helper, err := readBounded(options.Helper, MaxFileBytes)
	if err != nil || Digest(helper) != request.HelperSHA256 {
		return receipt, errors.New("owned helper identity refused")
	}
	parent, err := os.MkdirTemp(options.Parent, "t451a-run-")
	if err != nil {
		return receipt, fmt.Errorf("create private run: %w", err)
	}
	inputs := ""
	defer func() {
		if inputs != "" {
			if _, journalErr := os.Lstat(inputs + ".t451a-container.json"); !errors.Is(journalErr, os.ErrNotExist) {
				receipt.Outcome = "STOP"
				err = errors.Join(err, sandbox.ErrCustody)
				return
			}
		}
		cleanupErr := os.RemoveAll(parent)
		receipt.InputsRemoved = cleanupErr == nil
		err = errors.Join(err, cleanupErr)
		if err != nil {
			receipt.Outcome = "STOP"
		}
	}()
	if request.Mode == "plan" {
		bundle, err := DecodeBundle(options.Manifest)
		if err != nil {
			return receipt, err
		}
		if err := validateToolLayout(bundle); err != nil {
			return receipt, err
		}
		inputs, err = ImportBundle(ctx, options.BundleRoot, parent, options.Manifest, request.BundleSHA256)
		if err != nil {
			return receipt, err
		}
	} else {
		if len(options.Manifest) != 0 || request.BundleSHA256 != Digest(nil) {
			return receipt, errors.New("probes cannot import tools")
		}
		inputs = filepath.Join(parent, "inputs")
		if err := os.Mkdir(inputs, 0700); err != nil {
			return receipt, err
		}
	}
	if err := os.WriteFile(filepath.Join(inputs, "t451a"), helper, 0500); err != nil {
		return receipt, err
	}
	if err := os.WriteFile(filepath.Join(inputs, "request.json"), requestBytes, 0400); err != nil {
		return receipt, err
	}
	// The mount is read-only. Its namespace-visible modes permit the deliberately
	// unprivileged worker; the host's enclosing 0700 parent retains local custody.
	if err := filepath.WalkDir(inputs, func(name string, entry fs.DirEntry, walkErr error) error {
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
		return receipt, err
	}
	receipt.Stage = "sandbox"
	result, err := sandbox.Run(ctx, sandbox.Options{Socket: options.Socket, ImageID: request.ImageID, Inputs: inputs})
	receipt.ContainerID, receipt.Removed, receipt.ExitCode, receipt.Resources = result.ContainerID, result.Removed, result.ExitCode, result.Resources
	receipt.StopReason, receipt.OOMKilled = result.StopReason, result.OOMKilled
	receipt.OutputBytes, receipt.OutputSHA256 = len(result.Stdout), Digest(result.Stdout)
	if err != nil {
		if request.Mode == "plan" && len(result.Stderr) > 0 {
			err = fmt.Errorf("%w: %.8192s", err, result.Stderr)
		}
		return receipt, err
	}
	if result.ExitCode != 0 || !result.Removed || !result.Resources.LimitsVerified {
		return receipt, errors.New("execution evidence refused")
	}
	receipt.Stage = "complete"
	receipt.Outcome = "PROBE_OBSERVED"
	if request.Mode == "plan" {
		canonical, encodeErr := evidenceWire(result.Stdout)
		if encodeErr != nil || !bytes.Equal(canonical, result.Stdout) {
			return receipt, errors.New("neutral plan encoding refused")
		}
		receipt.NeutralEvidence = result.Stdout
		receipt.Outcome = "NEUTRAL_PLAN_OBSERVED"
	}
	return receipt, nil
}

// Receipt JSON may indent its embedded evidence. Compacting that RawMessage
// and restoring the worker encoder's terminal newline reconstructs the exact
// bytes bound by OutputSHA256 and OutputBytes.
func evidenceWire(data []byte) ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return nil, err
	}
	return append(compact.Bytes(), '\n'), nil
}

func readBounded(name string, limit int64) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("input file metadata refused")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("input file bytes refused")
	}
	return data, nil
}

func WorkerRequest() (Request, error) {
	if err := sandbox.ValidateWorker(); err != nil {
		return Request{}, err
	}
	data, err := readBounded("/inputs/request.json", 4096)
	if err != nil {
		return Request{}, err
	}
	return DecodeRequest(data)
}

func ReadManifest(name string) ([]byte, error) { return readBounded(name, MaxManifestBytes) }
