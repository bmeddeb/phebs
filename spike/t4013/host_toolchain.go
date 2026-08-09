package t4013

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	maxHostExecutableBytes = 256 << 20
	maxHostVersionBytes    = 4 << 10
)

// ObserveHostToolchain returns the exact, source-free identities of every host
// executable used directly or indirectly to build and operate the ceremony.
func ObserveHostToolchain(ctx context.Context) ([]HostToolObservation, error) {
	if ctx == nil {
		return nil, errors.New("T40.13 host toolchain context is nil")
	}
	goPath, err := resolveExecutable("go")
	if err != nil {
		return nil, err
	}
	goDigestBefore, err := executableDigest(goPath)
	if err != nil {
		return nil, fmt.Errorf("digest Go driver before observation: %w", err)
	}
	goVersion, err := boundedCommand(ctx, goPath, "version")
	if err != nil {
		return nil, fmt.Errorf("observe Go driver version: %w", err)
	}
	toolDirectory, err := boundedCommand(ctx, goPath, "env", "GOTOOLDIR")
	if err != nil {
		return nil, fmt.Errorf("observe Go tool directory: %w", err)
	}
	if !filepath.IsAbs(toolDirectory) {
		return nil, errors.New("T40.13 Go tool directory is not absolute")
	}
	goDigestAfter, err := executableDigest(goPath)
	if err != nil || goDigestAfter != goDigestBefore {
		return nil, errors.New("T40.13 Go driver changed while it was observed")
	}
	compilePath, err := resolveExactExecutable(filepath.Join(toolDirectory, "compile"))
	if err != nil {
		return nil, err
	}
	linkPath, err := resolveExactExecutable(filepath.Join(toolDirectory, "link"))
	if err != nil {
		return nil, err
	}
	compileIdentity, err := observeExecutable(ctx, "go-compile", compilePath, "-V=full")
	if err != nil {
		return nil, fmt.Errorf("observe Go compiler version: %w", err)
	}
	linkIdentity, err := observeExecutable(ctx, "go-link", linkPath, "-V=full")
	if err != nil {
		return nil, fmt.Errorf("observe Go linker version: %w", err)
	}
	gitPath, err := resolveExecutable("git")
	if err != nil {
		return nil, err
	}
	gitIdentity, err := observeExecutable(ctx, "git", gitPath, "--version")
	if err != nil {
		return nil, fmt.Errorf("observe Git version: %w", err)
	}
	surreal, err := store.FindSurrealBinary()
	if err != nil {
		return nil, fmt.Errorf("observe SurrealDB executable: %w", err)
	}
	surrealDigestAfter, err := executableDigest(surreal.Path)
	if err != nil || surrealDigestAfter != surreal.SHA256 {
		return nil, errors.New("T40.13 SurrealDB executable changed while it was observed")
	}
	values := []HostToolObservation{
		{Name: "go", Version: goVersion, SHA256: goDigestBefore},
		compileIdentity,
		linkIdentity,
		gitIdentity,
		{Name: "surreal", Version: surreal.Version, SHA256: surreal.SHA256},
	}
	if err := validateHostToolchain(values); err != nil {
		return nil, err
	}
	return values, nil
}

func observeExecutable(ctx context.Context, name, path string, arguments ...string) (HostToolObservation, error) {
	before, err := executableDigest(path)
	if err != nil {
		return HostToolObservation{}, err
	}
	version, err := boundedCommand(ctx, path, arguments...)
	if err != nil {
		return HostToolObservation{}, err
	}
	after, err := executableDigest(path)
	if err != nil || after != before {
		return HostToolObservation{}, errors.New("host executable changed while it was observed")
	}
	return HostToolObservation{Name: name, Version: version, SHA256: before}, nil
}

// VerifyHostToolchain re-observes the host tools and compares every ordered
// version and executable digest with the independently reviewed v2 plan.
func VerifyHostToolchain(ctx context.Context, expected []HostToolObservation) error {
	if err := validateHostToolchain(expected); err != nil {
		return err
	}
	observed, err := ObserveHostToolchain(ctx)
	if err != nil {
		return err
	}
	if !slices.Equal(observed, expected) {
		return errors.New("T40.13 host toolchain differs from the frozen plan")
	}
	return nil
}

func resolveExecutable(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("resolve host tool %s: %w", name, err)
	}
	return resolveExactExecutable(path)
}

func resolveExactExecutable(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve host executable: %w", err)
	}
	if !filepath.IsAbs(resolved) {
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("make host executable absolute: %w", err)
		}
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect host executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Size() <= 0 || info.Size() > maxHostExecutableBytes {
		return "", errors.New("T40.13 host tool is not a bounded executable regular file")
	}
	return resolved, nil
}

func executableDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxHostExecutableBytes {
		return "", errors.New("host executable changed or exceeded its byte bound")
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxHostExecutableBytes+1))
	if err != nil {
		return "", err
	}
	if written != info.Size() || written > maxHostExecutableBytes {
		return "", errors.New("host executable changed while hashing")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func boundedCommand(ctx context.Context, path string, arguments ...string) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stdout := &boundedBuffer{remaining: maxHostVersionBytes}
	stderr := &boundedBuffer{remaining: maxHostVersionBytes}
	command := exec.CommandContext(commandContext, path, arguments...)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return "", err
	}
	value := strings.TrimSpace(stdout.String())
	if value == "" {
		value = strings.TrimSpace(stderr.String())
	}
	if !boundedVersion(value) {
		return "", errors.New("host tool returned an invalid bounded version")
	}
	return value, nil
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	remaining int
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	if len(value) > buffer.remaining {
		written := 0
		if buffer.remaining > 0 {
			written, _ = buffer.buffer.Write(value[:buffer.remaining])
			buffer.remaining = 0
		}
		return written, errors.New("host tool version exceeds its byte bound")
	}
	written, err := buffer.buffer.Write(value)
	buffer.remaining -= written
	return written, err
}

func (buffer *boundedBuffer) String() string { return buffer.buffer.String() }
