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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/executableidentity"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/gofrs/flock"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
)

const (
	localRuntimeSchema = "phebs-surreal-runtime-v1"
	localRuntimeName   = ".surreal-runtime.json"
	maxRuntimeBytes    = 16 << 10
)

// localChildPassName is the persistent, mode-0600 record binding the
// supervised engine's root password to one database directory's lifetime.
// Unlike the live-backup rendezvous it is never removed on child stop.
const localChildPassName = ".surreal-child-pass"

const (
	localChildPassSchema = "phebs-surreal-child-pass-v1"
	maxChildPassBytes    = 1 << 10
)

// legacyRootPass is the root password of databases initialized before the
// supervised child bound its password to the database directory's lifetime
// (the old `--pass root` argv era). It is the only non-random password the
// child-pass file and the runtime descriptor ever carry.
const legacyRootPass = "root"

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
// Pass carries the database-bound root password: it is never placed on the
// child argv (world-readable via ps) and only reaches the child through the
// closed SURREAL_PASS environment channel.
type LocalRuntime struct {
	Schema       string          `json:"schema"`
	Token        string          `json:"token"`
	PID          int             `json:"pid"`
	Endpoint     string          `json:"endpoint"`
	Pass         string          `json:"pass"`
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
	output, err := dispatchadmission.OutputProduction(ctx, dispatchadmission.SiteSurrealVersion,
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
	runtime, owned, err := startOwnedEngine(ctx, engine)
	if err != nil {
		return LocalRuntime{}, nil, err
	}
	return runtime, owned.stop, nil
}

// newSurrealChildPass generates a fresh supervised-engine root password: 32
// bytes from crypto/rand, hex-encoded to 64 characters. The password never
// appears on the child argv.
func newSurrealChildPass() (string, error) {
	passBytes := make([]byte, 32)
	if _, err := rand.Read(passBytes); err != nil {
		return "", fmt.Errorf("create surreal child password: %w", err)
	}
	return hex.EncodeToString(passBytes), nil
}

// surrealChildArgs builds the supervised engine's argv. The root password is
// intentionally absent: it reaches the child only through the SURREAL_PASS
// environment entry, since the command line is world-readable through ps.
func surrealChildArgs(addr, engine string) []string {
	return []string{
		"start",
		"--bind", addr,
		"--user", "root",
		"--log", "warn",
		engine,
	}
}

// childPassFile is the persistent, mode-0600 record binding the supervised
// engine's root password to one database directory's lifetime. SurrealDB
// only initializes the root user when none exists and never rotates a stored
// root password, so every start of an existing database must sign in with the
// password used at initialization; the live-backup rendezvous cannot serve
// this because it is removed before the child stops.
type childPassFile struct {
	Schema string `json:"schema"`
	Pass   string `json:"pass"`
}

// validChildPass reports whether pass is a supervised-engine root password:
// 32 random bytes hex-encoded to 64 characters, or the literal legacy root
// password kept for databases initialized before the password was bound to
// the database directory. Anything else is refused fail-closed, like every
// other inconsistent credential field.
func validChildPass(pass string) bool {
	if pass == legacyRootPass {
		return true
	}
	if len(pass) != 64 {
		return false
	}
	for i := 0; i < len(pass); i++ {
		c := pass[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

// surrealKVDataDir returns the database directory for a surrealkv engine spec
// of the form "surrealkv:<dataDir>/db", or "" for engines with no persistent
// database (the memory test seam). Only surrealkv children bind their root
// password to a directory lifetime.
func surrealKVDataDir(engine string) string {
	rest, ok := strings.CutPrefix(engine, "surrealkv:")
	if !ok || rest == "" {
		return ""
	}
	return filepath.Dir(rest)
}

// dbDirHasNoEntries reports whether dataDir's database directory holds no
// entries at all. An empty directory is proven uninitialized, so startup
// may safely initialize it with a freshly published credential. Any entry
// may belong to a database initialized with an unknown password, so a
// non-empty directory never proves freshness.
func dbDirHasNoEntries(dataDir string) (bool, error) {
	path := filepath.Join(dataDir, "db")
	info, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("inspect database directory: %w", err)
	}
	if !info.IsDir() {
		return false, errors.New("database path is not a directory")
	}
	dir, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("inspect database directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	// Only emptiness matters; do not inventory and sort the entire directory
	// while holding the credential lock.
	_, err = dir.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect database directory entry: %w", err)
	}
	return false, nil
}

// childPassLockName is the stable per-data-directory lock serializing every
// child-password resolution (existing-file reads and first-start creation).
// The file is created once and never unlinked: unlinking and recreating it
// would let two processes hold locks on different inodes at the same time.
// Kernel lock release makes crashes safe. This reuses the repo's existing
// gofrs/flock dependency and the focusedindex lock pattern.
const childPassLockName = ".surreal-child-pass.lock"

// childPassTempPattern is the temp-file name pattern for atomic credential
// publication inside the data directory. Temp names are unique per attempt,
// so a stale temp from a crashed writer can never collide with a live
// publication.
const childPassTempPattern = ".surreal-child-pass.tmp.*"

// errChildPassLegacyVerify reports that dataDir holds a database but no child
// password file. The database may genuinely predate the password binding (the
// historical root/root era) or its credential may have been lost out of band;
// a missing password file alone proves neither, so the caller must verify by
// signing in before persisting or returning any password.
var errChildPassLegacyVerify = errors.New(
	"child password file is missing for an existing database: verify the legacy root password by sign-in",
)

// acquireChildPassLock takes the exclusive per-directory credential lock,
// waiting until ctx ends. The returned func releases it. The lock file is
// never unlinked or recreated while held.
func acquireChildPassLock(ctx context.Context, dataDir string) (func(), error) {
	lock := flock.New(filepath.Join(dataDir, childPassLockName), flock.SetPermissions(0o600))
	acquired, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("acquire child password lock: %w", err)
	}
	if !acquired {
		_ = lock.Close()
		return nil, errors.New("acquire child password lock: lock wait exceeded context")
	}
	return func() {
		_ = lock.Unlock()
		_ = lock.Close()
	}, nil
}

// childPassSyncSeam is the test seam for child-password durability.
// Production always uses real fsyncs and no pause; tests install hooks to
// freeze publication mid-flight, record fsync ordering, or inject sync
// failures. Hooks are process-wide: tests that install them must not run in
// parallel. A nil hook selects the production behavior.
type childPassSyncSeam struct {
	// pauseBeforeFileSync runs after a credential file is fully written and
	// before it is synced. Tests block here to freeze publication.
	pauseBeforeFileSync func()
	// syncFile fsyncs a fully-written credential file (temp or adopted).
	syncFile func(*os.File) error
	// syncDir fsyncs the data directory after the rename publishes the name.
	syncDir func(dataDir string) error
}

var childPassSeam = struct {
	sync.RWMutex
	seam childPassSyncSeam
}{}

func childPassSeamSnapshot() childPassSyncSeam {
	childPassSeam.RLock()
	defer childPassSeam.RUnlock()
	return childPassSeam.seam
}

// setChildPassSyncSeam installs seam for tests and returns a restore func
// that reinstalls the previous hooks.
func setChildPassSyncSeam(seam childPassSyncSeam) func() {
	childPassSeam.Lock()
	previous := childPassSeam.seam
	childPassSeam.seam = seam
	childPassSeam.Unlock()
	return func() {
		childPassSeam.Lock()
		childPassSeam.seam = previous
		childPassSeam.Unlock()
	}
}

// resolveChildPass returns the root password the supervised engine must use
// for dataDir's database. Every resolution — existing-file reads and
// first-start creation alike — runs under the stable per-directory
// cross-process lock, so no resolution can observe a half-published
// credential and no two first starts can publish competing ones. The first
// start for a directory persists its password in a mode-0600 file beside the
// database — a fresh random password, or the legacy root password once a
// running child proves the database predates this binding by signing in with
// it — and every later start reuses the persisted value, so sign-in keeps
// working across restarts, upgrades, and restore's stop-and-reopen
// validation. A precreated but empty database directory holds no initialized
// root user, so it is proven uninitialized and takes the fresh path: the
// published password is persisted before the child may start it, and the
// engine initializes its root with that password. SurrealDB only initializes
// the root user when none exists and never rotates a stored root password.
// An empty dataDir means a volatile engine with no database lifetime to bind
// to; those starts keep a fresh random password without taking the lock.
func resolveChildPass(ctx context.Context, dataDir string) (string, error) {
	if ctx == nil {
		return "", errors.New("resolve child password: context is nil")
	}
	if dataDir == "" {
		return newSurrealChildPass()
	}
	release, err := acquireChildPassLock(ctx, dataDir)
	if err != nil {
		return "", err
	}
	defer release()
	if file, record, err := openChildPassFile(dataDir); err == nil {
		return adoptChildPassFile(dataDir, file, record.Pass)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	empty, err := dbDirHasNoEntries(dataDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil && !empty {
		// Directory entries prove neither an initialized root nor legacy
		// credentials. The caller must probe without supplying credentials
		// that could initialize a new root.
		return "", errChildPassLegacyVerify
	}
	pass, err := newSurrealChildPass()
	if err != nil {
		return "", err
	}
	if err := publishChildPass(dataDir, pass); err != nil {
		return "", err
	}
	return pass, nil
}

// openChildPassFile opens and validates the persistent child password for
// dataDir, failing closed on any unsafe or inconsistent file. A missing file
// surfaces as os.ErrNotExist, which the caller treats as first start. The
// caller owns the returned file.
func openChildPassFile(dataDir string) (*os.File, childPassFile, error) {
	var record childPassFile
	path := filepath.Join(dataDir, localChildPassName)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, record, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maxChildPassBytes {
		return nil, record, errors.New("read child password: file is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, record, fmt.Errorf("open child password: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxChildPassBytes+1))
	if err != nil || len(data) > maxChildPassBytes {
		_ = file.Close()
		return nil, record, errors.New("read child password: file exceeds its limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		_ = file.Close()
		return nil, record, fmt.Errorf("decode child password: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		_ = file.Close()
		return nil, record, errors.New("decode child password: trailing content")
	}
	if record.Schema != localChildPassSchema || !validChildPass(record.Pass) {
		_ = file.Close()
		return nil, record, errors.New("child password file is inconsistent")
	}
	return file, record, nil
}

// readChildPassFile reads the persistent child password for dataDir, failing
// closed on any unsafe or inconsistent file. A missing file surfaces as
// os.ErrNotExist.
func readChildPassFile(dataDir string) (string, error) {
	file, record, err := openChildPassFile(dataDir)
	if err != nil {
		return "", err
	}
	_ = file.Close()
	return record.Pass, nil
}

// adoptChildPassFile durably adopts an already-open, validated credential
// file: the file and its directory are re-synced under the credential lock
// before the password may start a child, then the file is closed. This covers
// a previous writer that published complete bytes but died before finishing
// its syncs. A sync failure preserves the credential and refuses startup.
func adoptChildPassFile(dataDir string, file *os.File, pass string) (string, error) {
	seam := childPassSeamSnapshot()
	if seam.pauseBeforeFileSync != nil {
		seam.pauseBeforeFileSync()
	}
	syncFile := seam.syncFile
	if syncFile == nil {
		syncFile = (*os.File).Sync
	}
	syncErr := syncFile(file)
	closeErr := file.Close()
	if syncErr != nil {
		return "", fmt.Errorf("sync adopted child password: %w", syncErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close adopted child password: %w", closeErr)
	}
	if err := syncChildPassDir(dataDir, seam); err != nil {
		return "", err
	}
	return pass, nil
}

// publishChildPass durably publishes pass as dataDir's child password. The
// caller must hold the credential lock and must have established that no
// credential file exists yet: an existing credential is never replaced. The
// final name appears only through the rename of a fully-written, synced temp
// file, followed by a directory fsync, so a crash can never leave a torn
// final credential and only complete, durable credentials reach child
// startup. Any failure removes only the unpublished temp file and refuses
// startup; once renamed, the final credential is never deleted here — an
// uncertain directory sync preserves it and refuses, leaving durable adoption
// to the next resolution.
func publishChildPass(dataDir, pass string) error {
	if !validChildPass(pass) {
		return errors.New("publish child password: password is invalid")
	}
	encoded, err := json.Marshal(childPassFile{Schema: localChildPassSchema, Pass: pass})
	if err != nil {
		return fmt.Errorf("encode child password: %w", err)
	}
	encoded = append(encoded, '\n')
	seam := childPassSeamSnapshot()
	// os.CreateTemp creates the temp file mode 0600.
	tmp, err := os.CreateTemp(dataDir, childPassTempPattern)
	if err != nil {
		return fmt.Errorf("create child password temp file: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("write child password: %w", err)
	}
	if seam.pauseBeforeFileSync != nil {
		seam.pauseBeforeFileSync()
	}
	syncFile := seam.syncFile
	if syncFile == nil {
		syncFile = (*os.File).Sync
	}
	if err := syncFile(tmp); err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("sync child password: %w", err)
	}
	if err := tmp.Close(); err != nil {
		removeTemp()
		return fmt.Errorf("close child password: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dataDir, localChildPassName)); err != nil {
		removeTemp()
		return fmt.Errorf("publish child password: %w", err)
	}
	if err := syncChildPassDir(dataDir, seam); err != nil {
		return err
	}
	return nil
}

// syncChildPassDir fsyncs the data directory so a just-renamed credential
// name is durable. Fsyncing the file alone does not make the directory entry
// durable on Linux.
func syncChildPassDir(dataDir string, seam childPassSyncSeam) error {
	syncDir := seam.syncDir
	if syncDir == nil {
		syncDir = fsyncDir
	}
	if err := syncDir(dataDir); err != nil {
		return fmt.Errorf("sync child password directory: %w", err)
	}
	return nil
}

// fsyncDir fsyncs the directory itself.
func fsyncDir(dataDir string) error {
	dir, err := os.Open(dataDir)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}

// verifyAndAdoptLegacyChildPass proves a database without a password file is
// genuinely pre-binding: it signs in to the running child at addr with the
// historical root password. On success the legacy password is published — or
// a concurrently published credential is adopted — under the credential lock.
// On sign-in failure the database holds an unknown password, so startup is
// refused without persisting anything: an already-initialized random-password
// database is never locked out by a regenerated password.
func verifyAndAdoptLegacyChildPass(ctx context.Context, dataDir, addr string) (string, error) {
	if err := probeChildRootSignIn(ctx, addr, legacyRootPass); err != nil {
		return "", fmt.Errorf("refusing child startup: database has no password file and legacy root sign-in failed: %w", err)
	}
	release, err := acquireChildPassLock(ctx, dataDir)
	if err != nil {
		return "", err
	}
	defer release()
	if file, record, err := openChildPassFile(dataDir); err == nil {
		return adoptChildPassFile(dataDir, file, record.Pass)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := publishChildPass(dataDir, legacyRootPass); err != nil {
		return "", err
	}
	return legacyRootPass, nil
}

// probeChildRootSignIn dials the supervised child at addr and attempts one
// root sign-in with pass. It performs no scope initialization or schema work;
// the connection is closed before returning.
func probeChildRootSignIn(ctx context.Context, addr, pass string) error {
	if ctx == nil {
		return errors.New("probe child sign-in: context is nil")
	}
	u, err := url.ParseRequestURI("ws://" + addr)
	if err != nil {
		return fmt.Errorf("probe child sign-in: %w", err)
	}
	config := connection.NewConfig(u)
	if err := config.Validate(); err != nil {
		return fmt.Errorf("probe child sign-in: invalid connection config: %w", err)
	}
	conn := gorillaws.New(config)
	db, err := surrealdb.FromConnection(ctx, conn)
	if err != nil {
		return fmt.Errorf("probe child sign-in: connect: %w", err)
	}
	defer func() { _ = db.Close(ctx) }()
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: pass}); err != nil {
		return fmt.Errorf("probe child sign-in: %w", err)
	}
	return nil
}

// errLegacyVerifySelectedOwner reports that legacy child-password
// verification was refused because the process runs in selected-owner mode:
// every SDK call there goes through the authenticated process owner, and
// the legacy probe's raw owner-less connection would bypass the owner's
// final-send, strict-reply, failure-latching, and completion checks.
// Ordinary (non-selected) legacy migration is unaffected.
var errLegacyVerifySelectedOwner = errors.New(
	"refusing legacy child-password verification in selected-owner mode: legacy migration is unsupported with an authenticated process SDK owner",
)

// childProcessOwner reports the process's authenticated SDK owner for
// legacy-verification admission. Production always consults the dispatch
// admission state through processStoreCallOwner; tests install an override
// to prove selected-owner refusal. Overrides are process-wide: tests that
// install one must not run in parallel.
var childProcessOwner = processStoreCallOwner

// setChildProcessOwner installs lookup as the process-owner source for
// legacy-verification admission and returns a func restoring the previous
// source.
func setChildProcessOwner(lookup func() (*storeCallOwner, error)) func() {
	previous := childProcessOwner
	childProcessOwner = lookup
	return func() { childProcessOwner = previous }
}

// legacyChildCommand omits every credential-initialization input. With auth
// enabled, an existing root can sign in but a rootless database cannot acquire
// a throwaway root. Ignore ambient Surreal startup controls for this probe,
// especially USER/PASS, UNAUTHENTICATED, IMPORT_FILE and default namespace/db.
// Successful legacy verification retains this same ordinary child.
func legacyChildCommand(ctx context.Context, binary, addr, engine string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, "start", "--bind", addr,
		"--no-defaults", "--log", "warn", engine)
	environment := os.Environ()
	cmd.Env = make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "SURREAL_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	return cmd
}

func startOwnedEngine(ctx context.Context, engine string) (runtime LocalRuntime, owned *localEngine, err error) {
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
	//
	// The root password is bound to the database directory's lifetime and
	// travels only via the SURREAL_PASS environment entry, never on argv:
	// the child command line is world-readable through ps. The first start
	// for a directory persists its password in a mode-0600 file and every
	// later start reuses it, because SurrealDB only initializes the root
	// user when none exists and never rotates a stored password. Under the
	// production dispatch bootstrap the entry is admitted through the closed
	// extra-environment channel; without a bootstrap the command keeps its
	// ordinary environment.
	dataDir := surrealKVDataDir(engine)
	pass, err := resolveChildPass(ctx, dataDir)
	legacyVerify := errors.Is(err, errChildPassLegacyVerify)
	if err != nil && !legacyVerify {
		return LocalRuntime{}, nil, err
	}
	if legacyVerify {
		// Selected-owner mode authenticates the child through the process
		// SDK owner; the legacy probe needs a raw, owner-less connection,
		// so legacy verification is refused before the child starts rather
		// than bypassing the owner. Ordinary non-selected legacy migration
		// is unaffected.
		owner, ownerErr := childProcessOwner()
		if ownerErr != nil {
			return LocalRuntime{}, nil, ownerErr
		}
		if owner != nil {
			return LocalRuntime{}, nil, errLegacyVerifySelectedOwner
		}
	}
	cmdCtx := context.WithoutCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, identity.Path, surrealChildArgs(addr, engine)...)
	cmd.Env = append(os.Environ(), dispatchadmission.SurrealPassEnvKey+"="+pass)
	extraEnv := []string{dispatchadmission.SurrealPassEnvKey + "=" + pass}
	if legacyVerify {
		cmd = legacyChildCommand(cmdCtx, identity.Path, addr, engine)
		extraEnv = nil
		// Ordinary dispatch accepts the already-sanitized command. Every
		// installed exact runtime refuses a credential-free engine start,
		// even if it has no SDK owner; its admission contract is unchanged.
	}
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	handle, err := dispatchadmission.StartProductionWithEnv(
		ctx, dispatchadmission.SiteSurrealEngine, cmd,
		extraEnv,
	)
	if err != nil {
		return LocalRuntime{}, nil, fmt.Errorf("start surreal child: %w", err)
	}
	owned = &localEngine{process: cmd.Process, handle: handle}

	if err := waitHealthy(ctx, addr); err != nil {
		owned.stop()
		return LocalRuntime{}, nil, err
	}
	if legacyVerify {
		// Prove the database genuinely predates the password binding before
		// its password may be persisted or used. A failed probe refuses
		// startup without writing anything, so an already-initialized
		// random-password database is never locked out by a regenerated one.
		if pass, err = verifyAndAdoptLegacyChildPass(ctx, dataDir, addr); err != nil {
			owned.stop()
			return LocalRuntime{}, nil, err
		}
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		owned.stop()
		return LocalRuntime{}, nil, fmt.Errorf("create runtime token: %w", err)
	}
	return LocalRuntime{
		Schema: localRuntimeSchema, Token: hex.EncodeToString(tokenBytes), PID: cmd.Process.Pid,
		Endpoint: "ws://" + addr, Pass: pass, Surreal: identity,
	}, owned, nil
}

// StartLocalImport starts an isolated raw database child for restore. It does
// not apply phebs schema and deliberately publishes no live-backup descriptor.
func StartLocalImport(ctx context.Context, dataDir string) (LocalRuntime, func(), error) {
	return startLocal(ctx, dataDir)
}

// StartLocalImportWithMeasurement retains the raw import child's concrete
// ownership for synchronous measurements under the supplied SDK owner's idle
// lock. Like StartLocalImport, it applies no schema and publishes no runtime
// descriptor. The returned stop remains the sole process-wait owner.
//
// ctx must already have a deadline. Each guard call must have that deadline or
// an earlier one; it cannot renew the import's budget. The measurement callback
// has the same writer-exclusion, non-reentrancy and cooperative-cancellation
// requirements as WithQuiescentLocalEngine. The guard takes no PID authority.
func StartLocalImportWithMeasurement(ctx context.Context, dataDir string, owner *storeaccounting.SDKOwner) (LocalRuntime, func(), func(context.Context, func(context.Context) error) error, error) {
	if ctx == nil {
		return LocalRuntime{}, nil, nil, errLocalEngineQuiescence
	}
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return LocalRuntime{}, nil, nil, errLocalEngineQuiescence
	}
	if err := owner.Check(ctx); err != nil {
		return LocalRuntime{}, nil, nil, errors.Join(errLocalEngineQuiescence, err)
	}
	runtime, engine, err := startOwnedEngine(ctx, "surrealkv:"+filepath.Join(dataDir, "db"))
	if err != nil {
		return LocalRuntime{}, nil, nil, err
	}
	guard := func(operation context.Context, measure func(context.Context) error) error {
		if operation == nil {
			return errLocalEngineQuiescence
		}
		limit, bounded := operation.Deadline()
		if !bounded || limit.After(deadline) || ctx.Err() != nil {
			return errors.Join(errLocalEngineQuiescence, ctx.Err())
		}
		return engine.withQuiescent(operation, owner, measure)
	}
	return runtime, engine.stop, guard, nil
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
		!validChildPass(runtime.Pass) ||
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
