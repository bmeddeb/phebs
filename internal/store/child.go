package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/executableidentity"
)

const (
	localRuntimeSchema = "phebs-surreal-runtime-v1"
	localRuntimeName   = ".surreal-runtime.json"
	maxRuntimeBytes    = 16 << 10
)

// SurrealIdentity binds operational commands to the exact executable used by
// the supervised child. Path is kept only in the private runtime descriptor;
// backup manifests record the version and digest without leaking host layout.
type SurrealIdentity struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// LocalRuntime is the lifecycle-bound rendezvous for `phebs backup`. The file
// is mode 0600 under the data directory and is removed before the child stops.
// Token prevents an old process cleanup from deleting a successor descriptor.
type LocalRuntime struct {
	Schema       string          `json:"schema"`
	Token        string          `json:"token"`
	PID          int             `json:"pid"`
	Endpoint     string          `json:"endpoint"`
	ConfigSHA256 string          `json:"config_sha256,omitempty"`
	Surreal      SurrealIdentity `json:"surreal"`
}

// CurrentStoreIdentity is the writer/read contract a restore must validate
// before importing any bytes into a new data directory.
type StoreIdentity struct {
	StoreSchema                string `json:"store_schema_version"`
	EvidenceFormat             string `json:"evidence_format_version"`
	EvidenceMigration          string `json:"evidence_migration_version"`
	CallerPublicationWriter    string `json:"caller_publication_writer_schema"`
	CallerPublicationMigration string `json:"caller_publication_migration_version"`
}

type surrealIdentityCacheKey struct {
	path    string
	size    int64
	modTime int64
}

var surrealIdentityCache = struct {
	sync.Mutex
	values map[surrealIdentityCacheKey]SurrealIdentity
}{values: make(map[surrealIdentityCacheKey]SurrealIdentity)}

func CurrentStoreIdentity() StoreIdentity {
	return StoreIdentity{
		StoreSchema: evidenceStoreSchemaVersion, EvidenceFormat: evidenceFormatVersion,
		EvidenceMigration:          evidenceMigrationVersion,
		CallerPublicationWriter:    CallerGenerationPublicationWriterSchema,
		CallerPublicationMigration: callerGenerationPublicationMigrationVersion,
	}
}

// FindSurrealBinary resolves and validates the supported SurrealDB 3.x child.
// PHEBS_SURREAL is an explicit operator/test override; otherwise PATH wins.
func FindSurrealBinary() (SurrealIdentity, error) {
	return findSurrealBinary(false)
}

func findSurrealBinary(useCache bool) (SurrealIdentity, error) {
	candidate := strings.TrimSpace(os.Getenv("PHEBS_SURREAL"))
	if candidate == "" {
		found, err := exec.LookPath("surreal")
		if err != nil {
			return SurrealIdentity{}, fmt.Errorf("find surreal binary: %w", err)
		}
		candidate = found
	}
	expected := os.Getenv("PHEBS_SURREAL_SHA256")
	if expected != "" && (strings.TrimSpace(expected) != expected || !validSHA256(expected)) {
		return SurrealIdentity{}, errors.New("find surreal binary: expected digest is invalid")
	}
	identity, err := inspectSurrealBinary(
		context.Background(), candidate, useCache && expected == "", expected,
	)
	if err != nil {
		return SurrealIdentity{}, err
	}
	if expected != "" && identity.SHA256 != expected {
		return SurrealIdentity{}, errors.New("find surreal binary: executable digest differs")
	}
	return identity, nil
}

// InspectSurrealBinary validates one exact executable and returns its stable
// identity. Restore uses this on the manifest-bound binary before import.
func InspectSurrealBinary(candidate string) (SurrealIdentity, error) {
	return inspectSurrealBinary(context.Background(), candidate, false, "")
}

// InspectSurrealBinaryContext is InspectSurrealBinary with caller cancellation
// applied to hashing and version inspection.
func InspectSurrealBinaryContext(ctx context.Context, candidate string) (SurrealIdentity, error) {
	return inspectSurrealBinary(ctx, candidate, false, "")
}

func inspectSurrealBinary(
	ctx context.Context, candidate string, useCache bool, expectedSHA256 string,
) (SurrealIdentity, error) {
	if ctx == nil {
		return SurrealIdentity{}, errors.New("inspect surreal binary: context is nil")
	}
	if strings.TrimSpace(candidate) != candidate || candidate == "" {
		return SurrealIdentity{}, errors.New("inspect surreal binary: path is empty or padded")
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return SurrealIdentity{}, fmt.Errorf("inspect surreal binary path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return SurrealIdentity{}, fmt.Errorf("resolve surreal binary: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return SurrealIdentity{}, fmt.Errorf("stat surreal binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return SurrealIdentity{}, errors.New("inspect surreal binary: not an executable regular file")
	}
	cacheKey := surrealIdentityCacheKey{path: resolved, size: info.Size(), modTime: info.ModTime().UnixNano()}
	if useCache {
		surrealIdentityCache.Lock()
		identity, ok := surrealIdentityCache.values[cacheKey]
		surrealIdentityCache.Unlock()
		if ok {
			return identity, nil
		}
	}
	digest, err := fileSHA256Context(ctx, resolved, 1<<30)
	if err != nil {
		return SurrealIdentity{}, fmt.Errorf("digest surreal binary: %w", err)
	}
	if expectedSHA256 != "" && digest != expectedSHA256 {
		return SurrealIdentity{}, errors.New("find surreal binary: executable digest differs")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := dispatchadmission.CombinedOutputProduction(ctx, dispatchadmission.SiteSurrealVersion,
		exec.CommandContext(ctx, resolved, "version"))
	if err != nil {
		return SurrealIdentity{}, fmt.Errorf("run surreal version: %w", err)
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return SurrealIdentity{}, errors.New("run surreal version: empty output")
	}
	if err := validateSurrealVersionToken(fields[0]); err != nil {
		return SurrealIdentity{}, err
	}
	identity := SurrealIdentity{Path: resolved, Version: fields[0], SHA256: digest}
	if useCache {
		surrealIdentityCache.Lock()
		surrealIdentityCache.values[cacheKey] = identity
		surrealIdentityCache.Unlock()
	}
	return identity, nil
}

// ValidateSurrealVersionToken applies the same supported-version check used by
// the child supervisor without discovering or executing a binary.
func ValidateSurrealVersionToken(token string) error {
	return validateSurrealVersionToken(token)
}

func validateSurrealVersionToken(token string) error {
	if strings.TrimSpace(token) != token || token == "" {
		return fmt.Errorf("invalid surreal version %q", token)
	}
	core, build, hasBuild := strings.Cut(token, "+")
	if strings.Contains(core, "-") || (hasBuild && !validSemverBuildMetadata(build)) {
		return fmt.Errorf("invalid surreal version %q", token)
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "3" {
		return fmt.Errorf("unsupported surreal version %q: require 3.x", token)
	}
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("invalid surreal version %q", token)
		}
		if _, err := strconv.Atoi(part); err != nil {
			return fmt.Errorf("invalid surreal version %q", token)
		}
	}
	return nil
}

func validSemverBuildMetadata(value string) bool {
	if value == "" {
		return false
	}
	for _, segment := range strings.Split(value, ".") {
		if segment == "" {
			return false
		}
		for _, char := range segment {
			if (char >= '0' && char <= '9') ||
				(char >= 'A' && char <= 'Z') ||
				(char >= 'a' && char <= 'z') ||
				char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

// startLocal launches a supervised `surreal` child with surrealkv storage
// under dataDir, waits until it is healthy, and returns its runtime plus a
// stop func. This replaces in-process embedding — see the 2026-07-09 ADR.
func startLocal(ctx context.Context, dataDir string) (LocalRuntime, func(), error) {
	return startEngine(ctx, "surrealkv:"+filepath.Join(dataDir, "db"))
}

// startEngine is startLocal with an explicit storage engine. The only
// non-surrealkv caller is the OpenLocalMemory test seam; production servers
// always run surrealkv under the data directory.
func startEngine(ctx context.Context, engine string) (runtime LocalRuntime, stop func(), err error) {
	identity, err := findSurrealBinary(true)
	if err != nil {
		return LocalRuntime{}, nil, err
	}
	if err := executableidentity.Verify(identity.Path, os.Getenv("PHEBS_SURREAL_SHA256")); err != nil {
		return LocalRuntime{}, nil, fmt.Errorf("verify surreal identity before start: %w", err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return LocalRuntime{}, nil, fmt.Errorf("pick port: %w", err)
	}
	addr := l.Addr().String()
	// ponytail: close-then-reuse leaves a tiny port race; fine for a local
	// child. Revisit only if flaky in CI.
	_ = l.Close()

	// Detach the child command from the caller's (signal) context: on Ctrl-C
	// the DB must outlive the HTTP drain and in-flight terminal job writes.
	cmdCtx := context.WithoutCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, identity.Path, "start",
		"--bind", addr,
		"--user", "root", "--pass", "root",
		"--log", "warn",
		engine,
	)
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	handle, err := dispatchadmission.StartProduction(ctx, dispatchadmission.SiteSurrealEngine, cmd)
	if err != nil {
		return LocalRuntime{}, nil, fmt.Errorf("start surreal child: %w", err)
	}
	stop = func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = handle.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}

	if err := waitHealthy(ctx, addr); err != nil {
		stop()
		return LocalRuntime{}, nil, err
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		stop()
		return LocalRuntime{}, nil, fmt.Errorf("create runtime token: %w", err)
	}
	return LocalRuntime{
		Schema: localRuntimeSchema, Token: hex.EncodeToString(tokenBytes), PID: cmd.Process.Pid,
		Endpoint: "ws://" + addr, Surreal: identity,
	}, stop, nil
}

// StartLocalImport starts an isolated raw database child for restore. It does
// not apply phebs schema and deliberately publishes no live-backup descriptor.
func StartLocalImport(ctx context.Context, dataDir string) (LocalRuntime, func(), error) {
	return startLocal(ctx, dataDir)
}

// PublishLocalRuntime makes a healthy, schema-ready child discoverable to a
// concurrent backup command and returns an ownership-safe cleanup function.
func PublishLocalRuntime(dataDir string, runtime LocalRuntime) (func(), error) {
	if err := validateRuntime(runtime); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(runtime)
	if err != nil {
		return nil, fmt.Errorf("encode local runtime: %w", err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join(dataDir, localRuntimeName)
	if existing, err := os.Lstat(path); err == nil {
		if !existing.Mode().IsRegular() {
			return nil, errors.New("publish local runtime: existing descriptor is not a regular file")
		}
		current, readErr := ReadLocalRuntime(dataDir)
		if readErr != nil {
			return nil, fmt.Errorf("publish local runtime: existing descriptor cannot be trusted: %w", readErr)
		}
		if processAlive(current.PID) {
			return nil, errors.New("publish local runtime: another live server owns the data directory")
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale local runtime: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect local runtime: %w", err)
	}
	// O_EXCL is the data-directory ownership claim. A partial descriptor after
	// a crash intentionally blocks the next server until an operator inspects
	// it; backup also fails closed while publication is incomplete.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("claim local runtime: %w", err)
	}
	removeOnError := func() { _ = os.Remove(path) }
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		removeOnError()
		return nil, fmt.Errorf("write local runtime: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		removeOnError()
		return nil, fmt.Errorf("sync local runtime: %w", err)
	}
	if err := file.Close(); err != nil {
		removeOnError()
		return nil, fmt.Errorf("close local runtime: %w", err)
	}
	return func() {
		current, err := ReadLocalRuntime(dataDir)
		if err == nil && current.Token == runtime.Token {
			_ = os.Remove(path)
		}
	}, nil
}

// checkLocalRuntimeAvailable refuses a known live or untrustworthy owner
// before a second database child is started. PublishLocalRuntime repeats the
// check after startup to close ordinary check/use races; SurrealKV's own file
// locking remains the final fence for simultaneous first starts.
func checkLocalRuntimeAvailable(dataDir string) error {
	path := filepath.Join(dataDir, localRuntimeName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect local runtime: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("existing local runtime descriptor is not a regular file")
	}
	runtime, err := ReadLocalRuntime(dataDir)
	if err != nil {
		return fmt.Errorf("existing local runtime descriptor cannot be trusted: %w", err)
	}
	if processAlive(runtime.PID) {
		return errors.New("another live server owns the data directory")
	}
	return nil
}

// ReadLocalRuntime reads and validates the private live-backup rendezvous.
func ReadLocalRuntime(dataDir string) (LocalRuntime, error) {
	path := filepath.Join(dataDir, localRuntimeName)
	info, err := os.Lstat(path)
	if err != nil {
		return LocalRuntime{}, fmt.Errorf("read local runtime: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maxRuntimeBytes {
		return LocalRuntime{}, errors.New("read local runtime: descriptor is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return LocalRuntime{}, fmt.Errorf("open local runtime: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxRuntimeBytes+1))
	if err != nil || len(data) > maxRuntimeBytes {
		return LocalRuntime{}, errors.New("read local runtime: descriptor exceeds its limit")
	}
	var runtime LocalRuntime
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&runtime); err != nil {
		return LocalRuntime{}, fmt.Errorf("decode local runtime: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return LocalRuntime{}, errors.New("decode local runtime: trailing content")
	}
	if err := validateRuntime(runtime); err != nil {
		return LocalRuntime{}, err
	}
	return runtime, nil
}

func validateRuntime(runtime LocalRuntime) error {
	if runtime.Schema != localRuntimeSchema || len(runtime.Token) != 32 || runtime.PID <= 0 ||
		runtime.Surreal.Path == "" ||
		runtime.Surreal.Version == "" || !validSHA256(runtime.Surreal.SHA256) {
		return errors.New("local runtime descriptor is inconsistent")
	}
	if runtime.ConfigSHA256 != "" && !validSHA256(runtime.ConfigSHA256) {
		return errors.New("local runtime descriptor config digest is invalid")
	}
	host, port, err := net.SplitHostPort(strings.TrimPrefix(runtime.Endpoint, "ws://"))
	if !strings.HasPrefix(runtime.Endpoint, "ws://") || host != "127.0.0.1" || err != nil {
		return errors.New("local runtime descriptor endpoint is not loopback")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("local runtime descriptor port is invalid")
	}
	if _, err := hex.DecodeString(runtime.Token); err != nil {
		return errors.New("local runtime descriptor token is invalid")
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func processAlive(pid int) bool {
	return processAlivePlatform(pid)
}

func fileSHA256Context(ctx context.Context, path string, maxBytes int64) (string, error) {
	if ctx == nil {
		return "", errors.New("file digest context is nil")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(contextBoundReader{ctx, file}, maxBytes+1))
	if err != nil {
		return "", err
	}
	if written > maxBytes {
		return "", fmt.Errorf("file exceeds %d-byte limit", maxBytes)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

type contextBoundReader struct {
	context.Context
	io.Reader
}

func (reader contextBoundReader) Read(value []byte) (int, error) {
	if err := reader.Err(); err != nil {
		return 0, err
	}
	return reader.Reader.Read(value)
}

func waitHealthy(ctx context.Context, addr string) error {
	deadline := time.Now().Add(15 * time.Second)
	url := "http://" + addr + "/health"
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		resp, err := http.Get(url) //nolint:gosec // loopback child, constructed addr
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("surreal child on %s not healthy after 15s", addr)
}
