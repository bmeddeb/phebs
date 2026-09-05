package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// ErrExecutionToolCustody exposes no input, build or child diagnostic.
var ErrExecutionToolCustody = errors.New("execution tool copy verification unavailable or changed")

// ExecutionToolCustody binds an independently verified identity to one protected
// private direct-image copy. It is not a command permission, a complete toolchain
// or a CheckoutAdmissionBinding. Native helpers, SDKs, mutable inputs and command
// recipes retain their separate admission requirements and trusted-host boundary.
type ExecutionToolCustody struct {
	input           *ExecutionInputCustody
	identity        ExecutionToolIdentity
	referenceInputs *ExecutionGoBuildCustody
}

// ProtectExecutionReferenceTool verifies the protected copy with the existing
// exact reference build, never with a caller-supplied identity or verified flag.
// Only implemented Go roles are supported. Reference source/SDK/module custody
// remains the existing verifier's bounded repeated observations, not immutability.
// After copy creation, any failure returns closed, retained cleanup custody but
// no usable identity. The caller owns its eventual detach-before-removal flow.
func ProtectExecutionReferenceTool(ctx context.Context, parent string, request ReferenceToolRequest) (*ExecutionToolCustody, error) {
	if _, _, _, _, _, err := referenceToolRole(request.Role); err != nil {
		return nil, ErrExecutionToolCustody
	}
	tool, selection, err := protectExecutionToolCopy(ctx, parent, request.Role, request.Binary)
	if err != nil {
		return tool, err
	}
	request.Binary = selection.Path
	identity, err := VerifyExecutionReferenceTool(ctx, request)
	return finishExecutionToolCopy(ctx, tool, selection, identity, err)
}

// ProtectExecutionExternalTool currently observes only a copied SurrealDB direct
// image. Git/Go copies require separately admitted helper/SDK location recipes;
// fixed-system-image roles must retain their platform paths. Those roles all
// refuse before copying, rather than relaxing the existing observer's checks.
func ProtectExecutionExternalTool(ctx context.Context, parent, role, binary string) (*ExecutionToolCustody, error) {
	if role != "surreal" {
		return nil, ErrExecutionToolCustody
	}
	tool, selection, err := protectExecutionToolCopy(ctx, parent, role, binary)
	if err != nil {
		return tool, err
	}
	identity, err := ObserveExecutionExternalTool(ctx, role, selection.Path)
	return finishExecutionToolCopy(ctx, tool, selection, identity, err)
}

func protectExecutionToolCopy(ctx context.Context, parent, role, binary string) (*ExecutionToolCustody, ExecutionInputCopy, error) {
	if ctx == nil || ctx.Err() != nil || runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" ||
		len(parent) > maxInputCustodyPathBytes || !filepath.IsAbs(parent) || filepath.Clean(parent) != parent || parent == string(filepath.Separator) ||
		len(binary) > maxInputCustodyPathBytes || !filepath.IsAbs(binary) || filepath.Clean(binary) != binary {
		return nil, ExecutionInputCopy{}, ErrExecutionToolCustody
	}
	canonicalParent, err := filepath.EvalSymlinks(parent)
	parentInfo, statErr := os.Lstat(parent)
	if err != nil || canonicalParent != parent || statErr != nil || !parentInfo.IsDir() ||
		parentInfo.Mode().Perm() != 0o700 || !inputCustodyOwned(parentInfo) {
		return nil, ExecutionInputCopy{}, ErrExecutionToolCustody
	}
	// Normalize only the explicitly selected image; never discover one via PATH.
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil || len(binary) > maxInputCustodyPathBytes {
		return nil, ExecutionInputCopy{}, ErrExecutionToolCustody
	}
	digest, err := t4013.DigestHostExecutable(ctx, binary)
	if err != nil {
		return nil, ExecutionInputCopy{}, ErrExecutionToolCustody
	}
	selection := ExecutionInputCopy{Name: role, Path: binary, SHA256: digest, Executable: true}
	input, err := ProtectExecutionInputs(ctx, parent, []ExecutionInputCopy{selection})
	if input == nil {
		return nil, ExecutionInputCopy{}, ErrExecutionToolCustody
	}
	tool := &ExecutionToolCustody{input: input}
	if err == nil {
		selection.Path, err = input.Check(ctx, role)
	}
	if err != nil {
		_ = tool.refuse()
		_ = tool.Close()
		return tool, ExecutionInputCopy{}, ErrExecutionToolCustody
	}
	return tool, selection, nil
}

func finishExecutionToolCopy(ctx context.Context, tool *ExecutionToolCustody, selection ExecutionInputCopy, identity ExecutionToolIdentity, verifyErr error) (*ExecutionToolCustody, error) {
	path, custodyErr := tool.input.Check(ctx, selection.Name)
	if verifyErr != nil || custodyErr != nil || path != selection.Path || identity.Role != selection.Name ||
		identity.SHA256 != selection.SHA256 || identity.FileType != regularFileType {
		_ = tool.refuse()
		_ = tool.Close()
		return tool, ErrExecutionToolCustody
	}
	tool.identity = identity
	return tool, nil
}

// Check returns a source-free identity by value and its private path only for
// the verified role while custody is intact. No hashing/build/probe runs here.
// A wrong role, cancellation, drift or closed custody refuses permanently.
func (tool *ExecutionToolCustody) Check(ctx context.Context, role string) (ExecutionToolIdentity, string, error) {
	if tool == nil || tool.input == nil || tool.identity.Role == "" || tool.identity.Role != role {
		return ExecutionToolIdentity{}, "", tool.refuse()
	}
	path, err := tool.input.Check(ctx, role)
	if err != nil {
		return ExecutionToolIdentity{}, "", ErrExecutionToolCustody
	}
	return tool.identity, path, nil
}

func (tool *ExecutionToolCustody) refuse() error {
	if tool != nil && tool.input != nil {
		tool.input.mu.Lock()
		tool.input.err = ErrExecutionInputCustody
		tool.input.mu.Unlock()
	}
	return ErrExecutionToolCustody
}

// Directory returns private cleanup custody even after failed verification.
func (tool *ExecutionToolCustody) Directory() string {
	if tool == nil || tool.input == nil {
		return ""
	}
	return tool.input.Directory()
}

// Close releases read descriptors only; it neither thaws nor removes any copy.
func (tool *ExecutionToolCustody) Close() error {
	if tool != nil && tool.input != nil && tool.input.Close() != nil {
		return ErrExecutionToolCustody
	}
	return nil
}
