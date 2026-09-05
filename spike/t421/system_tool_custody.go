package t421

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// ExecutionSystemToolCustody holds one of the three fixed platform images on
// its observed read-only native volume. It never copies or changes that image.
// This trusted-host binding is not vendor attestation, a command permission,
// helper closure, or protection against a privileged host replacement.
type ExecutionSystemToolCustody struct {
	mu       sync.Mutex
	file     *os.File
	info     os.FileInfo
	path     string
	volume   [2]int32
	identity ExecutionToolIdentity
	closed   bool
	failed   bool
}

func executionSystemToolPath(role string) string {
	switch role {
	case "sh":
		return "/bin/sh"
	case "hdiutil", "ssh-keygen":
		return "/usr/bin/" + role
	default:
		return ""
	}
}

// HoldExecutionSystemTool selects the fixed path internally and reuses the
// real image observer. No version child runs for these roles. On refusal it
// closes its sole borrowed-image descriptor; there is no new disk custody.
func HoldExecutionSystemTool(ctx context.Context, role string) (_ *ExecutionSystemToolCustody, retErr error) {
	path := executionSystemToolPath(role)
	if ctx == nil || ctx.Err() != nil || path == "" || runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil, ErrExecutionToolCustody
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, ErrExecutionToolCustody
	}
	file, err := t4013.OpenHostImage(path)
	if err != nil {
		return nil, ErrExecutionToolCustody
	}
	defer func() {
		if retErr != nil {
			_ = file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, ErrExecutionToolCustody
	}
	volume, err := systemToolReadOnlyVolume(file, info)
	if err != nil {
		return nil, ErrExecutionToolCustody
	}
	identity, err := ObserveExecutionExternalTool(ctx, role, path)
	if err != nil {
		return nil, ErrExecutionToolCustody
	}
	tool := &ExecutionSystemToolCustody{file: file, info: info, path: path, volume: volume, identity: identity}
	if _, _, err := tool.Check(ctx, role); err != nil {
		return nil, ErrExecutionToolCustody
	}
	return tool, nil
}

// Check uses only fixed path/held-file metadata and one read-only-volume check,
// not an image hash or child. Any mismatch, canceled check or wrong role is
// sticky. The owner must retain this handle through its last joined command.
func (tool *ExecutionSystemToolCustody) Check(ctx context.Context, role string) (ExecutionToolIdentity, string, error) {
	if tool == nil {
		return ExecutionToolIdentity{}, "", ErrExecutionToolCustody
	}
	tool.mu.Lock()
	defer tool.mu.Unlock()
	refuse := func() (ExecutionToolIdentity, string, error) {
		tool.failed = true
		return ExecutionToolIdentity{}, "", ErrExecutionToolCustody
	}
	if ctx == nil || ctx.Err() != nil || tool.failed || tool.closed || tool.file == nil ||
		tool.identity.Role != role || tool.path == "" || tool.path != executionSystemToolPath(role) {
		return refuse()
	}
	held, err := tool.file.Stat()
	current, pathErr := os.Lstat(tool.path)
	resolved, resolveErr := filepath.EvalSymlinks(tool.path)
	if err != nil || pathErr != nil || resolveErr != nil || resolved != tool.path ||
		!inputCustodySame(tool.info, held) || !inputCustodySame(held, current) {
		return refuse()
	}
	volume, err := systemToolReadOnlyVolume(tool.file, held)
	if err != nil || volume != tool.volume || ctx.Err() != nil {
		return refuse()
	}
	return tool.identity, tool.path, nil
}

// Close only releases the read descriptor. It never mutates the system image
// or its volume; repeated Close is safe. Callers own command/session joins.
func (tool *ExecutionSystemToolCustody) Close() error {
	if tool == nil {
		return nil
	}
	tool.mu.Lock()
	defer tool.mu.Unlock()
	if tool.closed {
		return nil
	}
	tool.closed = true
	if tool.file != nil && tool.file.Close() != nil {
		tool.failed = true
		return ErrExecutionToolCustody
	}
	return nil
}
