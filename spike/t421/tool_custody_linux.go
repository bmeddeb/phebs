//go:build linux

package t421

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// LinuxExecutionToolCustody binds measured/verifier-produced identity to a
// sealed direct image. It admits no command, mutable build inputs, loader,
// libraries, helpers, signer, session or ceremony. No caller identity is trusted.
type LinuxExecutionToolCustody struct {
	mu       sync.Mutex // Probe cleanup only; image use is serialized by input.
	input    *LinuxExecutionInputCustody
	identity ExecutionToolIdentity
	probe    *linuxExecutionToolProbe
}

type linuxExecutionToolProbe struct {
	directory string
	pid       int
	joined    bool
}

// ProtectLinuxExecutionReferenceTool independently rebuilds the sealed supplied
// image using the existing exact source/module/SDK verifier. Only implemented Go
// roles/recipes qualify; source/build-input custody remains repeated observation
// on the trusted host, not immutability. Failure releases all anonymous custody.
func ProtectLinuxExecutionReferenceTool(ctx context.Context, request ReferenceToolRequest) (*LinuxExecutionToolCustody, error) {
	if _, _, _, _, _, err := referenceToolRoleForSchema(request.Role, request.PlanSchema, request.SourceCommit); err != nil {
		return nil, ErrExecutionToolCustody
	}
	tool, err := protectLinuxExecutionTool(ctx, request.Role, request.Binary)
	if err != nil {
		return nil, err
	}
	var identity ExecutionToolIdentity
	err = tool.input.WithInput(ctx, request.Role, func(file *os.File) error {
		var err error
		identity, err = verifyExecutionReferenceTool(ctx, request, file)
		return err
	})
	return finishLinuxExecutionTool(ctx, tool, request.Role, identity, err)
}

// ProtectLinuxExecutionExternalTool supports only the SurrealDB direct image.
// The closed version probe executes the sealed FD, with no ambient environment
// or PATH image selection. Version syntax is observation, not vendor attestation.
// Git/Go helper/SDK recipes and fixed-system/signer tools remain unimplemented.
// Uncertain probe drain/removal returns non-nil unusable cleanup custody;
// retain it and retry Close, never dispose scratch or the keeper prematurely.
func ProtectLinuxExecutionExternalTool(ctx context.Context, role, binary string) (*LinuxExecutionToolCustody, error) {
	if role != "surreal" {
		return nil, ErrExecutionToolCustody
	}
	tool, err := protectLinuxExecutionTool(ctx, role, binary)
	if err != nil {
		return nil, err
	}
	var version string
	err = tool.input.WithInput(ctx, role, func(file *os.File) error {
		var err error
		version, err = probeLinuxExecutionSurreal(ctx, tool, file)
		return err
	})
	input, checkErr := tool.input.Check(ctx, role)
	identity := ExecutionToolIdentity{Role: role, FileType: regularFileType, SHA256: input.SHA256,
		Version: version, Provenance: "external-sealed-file-linux-amd64-v1"}
	if checkErr != nil {
		err = ErrExecutionToolCustody
	}
	return finishLinuxExecutionTool(ctx, tool, role, identity, err)
}

func protectLinuxExecutionTool(ctx context.Context, role, binary string) (*LinuxExecutionToolCustody, error) {
	if runtime.GOARCH != "amd64" || ctx == nil || ctx.Err() != nil || len(binary) > maxInputCustodyPathBytes ||
		!filepath.IsAbs(binary) || filepath.Clean(binary) != binary || strings.TrimSpace(binary) != binary {
		return nil, ErrExecutionToolCustody
	}
	// Normalize only the explicitly selected direct image, never discover PATH.
	canonical, err := filepath.EvalSymlinks(binary)
	if err != nil || len(canonical) > maxInputCustodyPathBytes {
		return nil, ErrExecutionToolCustody
	}
	// Refuse the direct-copy ceiling before the existing bounded digest read.
	info, err := os.Lstat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxInputCustodyFileBytes {
		return nil, ErrExecutionToolCustody
	}
	digest, err := t4013.DigestHostExecutable(ctx, canonical)
	if err != nil {
		return nil, ErrExecutionToolCustody
	}
	input, err := ProtectLinuxExecutionInputs(ctx, []ExecutionInputCopy{{Name: role, Path: canonical, SHA256: digest, Executable: true}})
	if err != nil {
		return nil, ErrExecutionToolCustody
	}
	return &LinuxExecutionToolCustody{input: input}, nil
}

func finishLinuxExecutionTool(ctx context.Context, tool *LinuxExecutionToolCustody, role string, identity ExecutionToolIdentity, verifyErr error) (*LinuxExecutionToolCustody, error) {
	input, err := tool.input.Check(ctx, role)
	if verifyErr != nil || err != nil || identity.Role != role || identity.SHA256 != input.SHA256 || identity.FileType != regularFileType {
		_ = tool.refuse()
		_ = tool.Close()
		if tool.PrivateProbeDirectory() != "" {
			return tool, ErrExecutionToolCustody
		}
		return nil, ErrExecutionToolCustody
	}
	tool.identity = identity
	return tool, nil
}

func probeLinuxExecutionSurreal(ctx context.Context, tool *LinuxExecutionToolCustody, file *os.File) (_ string, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	workspace, err := os.MkdirTemp("", "phebs-linux-tool-probe-")
	if err != nil {
		return "", ErrExecutionToolCustody
	}
	tool.probe = &linuxExecutionToolProbe{directory: workspace, joined: true}
	defer func() {
		if tool.closeProbe() != nil {
			retErr = ErrExecutionToolCustody
		}
	}()
	canonical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", ErrExecutionToolCustody
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", ErrExecutionToolCustody
	}
	workspace, tool.probe.directory = canonical, canonical
	stdout := checkoutCommandOutput{remaining: 4 << 10, cancel: cancel}
	stderr := checkoutCommandOutput{remaining: 4 << 10, cancel: cancel}
	command := exec.CommandContext(ctx, "/proc/self/fd/3", "version")
	command.ExtraFiles = []*os.File{file}
	command.Dir, command.Env = workspace, externalToolEnvironment(workspace)
	command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
	runErr := runReferenceCommand(ctx, command)
	if command.Process != nil {
		tool.probe.pid, tool.probe.joined = command.Process.Pid, command.ProcessState != nil
	}
	if runErr != nil || ctx.Err() != nil || stdout.err != nil || stderr.err != nil || stderr.buffer.Len() != 0 {
		return "", ErrExecutionToolCustody
	}
	version := strings.TrimSuffix(stdout.buffer.String(), "\n")
	fields := strings.Fields(version)
	if !validPublicToolVersion(version) || len(fields) == 0 || store.ValidateSurrealVersionToken(fields[0]) != nil {
		return "", ErrExecutionToolCustody
	}
	return version, nil
}

// Check returns a detached identity without hash/build/probe work. Unknown roles,
// cancellation, closed custody and kernel drift permanently refuse this tool.
func (tool *LinuxExecutionToolCustody) Check(ctx context.Context, role string) (ExecutionToolIdentity, error) {
	if tool == nil || tool.input == nil || tool.identity.Role == "" || tool.identity.Role != role {
		return ExecutionToolIdentity{}, tool.refuse()
	}
	if _, err := tool.input.Check(ctx, role); err != nil {
		return ExecutionToolIdentity{}, ErrExecutionToolCustody
	}
	return tool.identity, nil
}

// WithImage lends the exact protected image for trusted, joined use. Callers
// obey LinuxExecutionInputCustody.WithInput's no-escape/no-reentry contract;
// they may match a running image using t4013.MatchLinuxProcessExecutableImage.
// This capability supplies no permission or complete dispatch recipe.
func (tool *LinuxExecutionToolCustody) WithImage(ctx context.Context, role string, use func(ExecutionToolIdentity, *os.File) error) error {
	if tool == nil || tool.input == nil || tool.identity.Role != role || tool.identity.Role == "" || use == nil {
		return tool.refuse()
	}
	err := tool.input.WithInput(ctx, role, func(file *os.File) error { return use(tool.identity, file) })
	if err != nil {
		return tool.refuse()
	}
	return nil
}

func (tool *LinuxExecutionToolCustody) refuse() error {
	if tool != nil && tool.input != nil {
		tool.input.mu.Lock()
		tool.input.err = ErrExecutionInputCustody
		tool.input.mu.Unlock()
	}
	return ErrExecutionToolCustody
}

// PrivateProbeDirectory identifies retained cleanup custody, never evidence.
// A failed probe may return non-nil unusable custody when drain/removal is
// uncertain. Close retries observation/removal, never signals a reused PID.
func (tool *LinuxExecutionToolCustody) PrivateProbeDirectory() string {
	if tool == nil {
		return ""
	}
	tool.mu.Lock()
	defer tool.mu.Unlock()
	if tool.probe == nil {
		return ""
	}
	return tool.probe.directory
}

func (tool *LinuxExecutionToolCustody) closeProbe() error {
	tool.mu.Lock()
	defer tool.mu.Unlock()
	if tool.probe == nil {
		return nil
	}
	if !tool.probe.joined {
		return ErrExecutionToolCustody
	}
	if tool.probe.pid != 0 {
		members, err := t4013.PrivateProcessSessionMembers(tool.probe.pid)
		if err != nil || members != 0 {
			return ErrExecutionToolCustody
		}
	}
	if os.RemoveAll(tool.probe.directory) != nil {
		return ErrExecutionToolCustody
	}
	tool.probe = nil
	return nil
}

// Close releases owned read FDs only; it cannot certify children or escaped FDs.
func (tool *LinuxExecutionToolCustody) Close() error {
	if tool != nil && tool.closeProbe() != nil {
		return tool.refuse()
	}
	if tool != nil && tool.input != nil && tool.input.Close() != nil {
		return ErrExecutionToolCustody
	}
	return nil
}
