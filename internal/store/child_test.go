package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSurrealVersionTokenAcceptsStable3xAndBuildMetadata(t *testing.T) {
	for _, token := range []string{
		"3.0",
		"3.0.0",
		"3.2.3",
		"3.2.3+20260721.40522d1",
		"3.2+local-build",
	} {
		t.Run(token, func(t *testing.T) {
			if err := validateSurrealVersionToken(token); err != nil {
				t.Fatalf("validateSurrealVersionToken(%q): %v", token, err)
			}
		})
	}
}

func TestValidateSurrealVersionTokenRejectsUnsupportedOrInvalid(t *testing.T) {
	for _, token := range []string{
		"",
		" 3.2.3",
		"3",
		"2.9.9",
		"4.0.0",
		"3.2.beta",
		"3.2.3.4",
		"3.2.3-alpha.1",
		"3.2.3+",
		"3.2.3+bad_meta",
	} {
		t.Run(token, func(t *testing.T) {
			if err := validateSurrealVersionToken(token); err == nil {
				t.Fatalf("validateSurrealVersionToken(%q) succeeded", token)
			}
		})
	}
}

func TestInspectSurrealBinaryAcceptsBuildMetadataVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "surreal")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '3.2.3+20260721.40522d1 for macos on aarch64'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	identity, err := InspectSurrealBinary(path)
	if err != nil {
		t.Fatalf("InspectSurrealBinary: %v", err)
	}
	if identity.Version != "3.2.3+20260721.40522d1" {
		t.Fatalf("version = %q", identity.Version)
	}
	if identity.Path == "" || !validSHA256(identity.SHA256) {
		t.Fatalf("identity = %+v", identity)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := InspectSurrealBinaryContext(canceled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled identity inspection = %v", err)
	}
}

func TestInspectSurrealBinaryUsesStdoutVersion(t *testing.T) {
	for _, tc := range []struct {
		name, script, version string
	}{
		{"warning", "printf 'onnxruntime warning\\n' >&2\nprintf '3.2.0 for linux on aarch64\\n'\n", "3.2.0"},
		{"stderr only", "printf '3.2.0\\n' >&2\n", ""},
		{"invalid stdout", "printf '3.2.0\\n' >&2\nprintf 'invalid\\n'\n", ""},
		{"nonzero exit", "printf '3.2.0\\n'\nexit 7\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "surreal")
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"+tc.script), 0o755); err != nil {
				t.Fatal(err)
			}
			identity, err := InspectSurrealBinaryContext(t.Context(), path)
			if tc.version == "" {
				if err == nil {
					t.Fatalf("invalid version probe accepted: %+v", identity)
				}
				return
			}
			if err != nil || identity.Version != tc.version || !validSHA256(identity.SHA256) {
				t.Fatalf("identity = %+v, %v", identity, err)
			}
		})
	}
}

func TestExpectedSurrealDigestBypassesIdentityCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "surreal")
	marker := filepath.Join(t.TempDir(), "replacement-ran")
	firstText := "#!/bin/sh\nprintf '3.0.0\\n'\n"
	secondText := "#!/bin/sh\n: > \"$T4013_REPLACEMENT_MARKER\"\nprintf '3.1.0\\n'\n"
	first := []byte(firstText + strings.Repeat(" ", len(secondText)-len(firstText)))
	second := []byte(secondText)
	if err := os.WriteFile(path, first, 0o755); err != nil {
		t.Fatal(err)
	}
	identity, err := InspectSurrealBinary(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHEBS_SURREAL", path)
	t.Setenv("PHEBS_SURREAL_SHA256", identity.SHA256)
	t.Setenv("T4013_REPLACEMENT_MARKER", marker)
	if _, err := findSurrealBinary(true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, second, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := findSurrealBinary(true); err == nil || !strings.Contains(err.Error(), "digest differs") {
		t.Fatalf("same-metadata replacement error = %v", err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement SurrealDB executable ran before refusal: %v", err)
	}
}

func TestStartEngineRevalidatesExpectedSurrealDigestAfterVersionProbe(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "surreal")
	replacement := filepath.Join(root, "replacement")
	marker := filepath.Join(root, "replacement-ran")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then mv \"$T4013_REPLACEMENT\" \"$0\"; printf '3.0.0\\n'; exit 0; fi\n: > \"$T4013_REPLACEMENT_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("#!/bin/sh\n: > \"$T4013_REPLACEMENT_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	expected, err := fileSHA256Context(t.Context(), path, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHEBS_SURREAL", path)
	t.Setenv("PHEBS_SURREAL_SHA256", expected)
	t.Setenv("T4013_REPLACEMENT", replacement)
	t.Setenv("T4013_REPLACEMENT_MARKER", marker)
	if _, stop, err := startEngine(t.Context(), "memory"); err == nil {
		stop()
		t.Fatal("replacement SurrealDB executable started")
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement SurrealDB executable ran: %v", err)
	}
}

func TestLocalRuntimeDescriptorOwnershipAndCleanup(t *testing.T) {
	dataDir := t.TempDir()
	runtime := LocalRuntime{
		Schema: localRuntimeSchema, Token: strings.Repeat("a", 32), PID: os.Getpid(),
		Endpoint: "ws://127.0.0.1:32123", Pass: strings.Repeat("d", 64), ConfigSHA256: "sha256:" + strings.Repeat("c", 64),
		Surreal: SurrealIdentity{
			Path: "/private/test/surreal", Version: "3.2.0", SHA256: "sha256:" + strings.Repeat("b", 64),
		},
	}
	cleanup, err := PublishLocalRuntime(dataDir, runtime)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dataDir, localRuntimeName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime descriptor mode = %v, %v", info, err)
	}
	got, err := ReadLocalRuntime(dataDir)
	if err != nil || got != runtime {
		t.Fatalf("ReadLocalRuntime = %+v, %v", got, err)
	}
	if _, err := PublishLocalRuntime(dataDir, runtime); err == nil || !strings.Contains(err.Error(), "another live server") {
		t.Fatalf("second publisher error = %v", err)
	}
	cleanup()
	if _, err := os.Stat(filepath.Join(dataDir, localRuntimeName)); !os.IsNotExist(err) {
		t.Fatalf("runtime descriptor survived cleanup: %v", err)
	}
}

func TestLocalRuntimeDescriptorRefusesCorruptPredecessor(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, localRuntimeName)
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := LocalRuntime{
		Schema: localRuntimeSchema, Token: strings.Repeat("a", 32), PID: os.Getpid(),
		Endpoint: "ws://127.0.0.1:32123", Pass: strings.Repeat("d", 64), ConfigSHA256: "sha256:" + strings.Repeat("c", 64),
		Surreal: SurrealIdentity{
			Path: "/private/test/surreal", Version: "3.2.0", SHA256: "sha256:" + strings.Repeat("b", 64),
		},
	}
	if _, err := PublishLocalRuntime(dataDir, runtime); err == nil || !strings.Contains(err.Error(), "cannot be trusted") {
		t.Fatalf("PublishLocalRuntime error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{}\n" {
		t.Fatalf("corrupt predecessor was overwritten: %q, %v", data, err)
	}
}

func TestOpenLocalRefusesCorruptDescriptorBeforeStartingChild(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, localRuntimeName), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The refusal happens before any child starts, so the error path owns
	// nothing; still own the store on the unexpected-success path, where
	// the assertion below fatals before any explicit close could run.
	unexpected, err := OpenLocal(context.Background(), dataDir)
	ownTestStore(t, unexpected)
	if err == nil || !strings.Contains(err.Error(), "cannot be trusted") {
		t.Fatalf("OpenLocal error = %v", err)
	}
}

func TestOpenLocalWithConfigRefusesInvalidDigestBeforeStartingChild(t *testing.T) {
	unexpected, err := OpenLocalWithConfig(context.Background(), t.TempDir(), "bad")
	ownTestStore(t, unexpected)
	if err == nil || !strings.Contains(err.Error(), "config digest") {
		t.Fatalf("OpenLocalWithConfig error = %v", err)
	}
}

func TestNewSurrealChildPassIsRandom(t *testing.T) {
	t.Parallel()
	first, err := newSurrealChildPass()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newSurrealChildPass()
	if err != nil {
		t.Fatal(err)
	}
	for _, pass := range []string{first, second} {
		if len(pass) != 64 {
			t.Fatalf("child password length = %d, want 64", len(pass))
		}
		raw, err := hex.DecodeString(pass)
		if err != nil || len(raw) != 32 {
			t.Fatalf("child password decodes to %d bytes: %v", len(raw), err)
		}
	}
	if first == second {
		t.Fatal("two generated child passwords are identical")
	}
}

// TestResolveChildPassStableAcrossStarts is the regression for the P1 review
// finding: the child password is bound to the database directory's lifetime,
// not to each start. Reopening the same directory must reuse the persisted
// password, while different directories get distinct passwords.
func TestResolveChildPassStableAcrossStarts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	first, err := resolveChildPass(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !validChildPass(first) || first == legacyRootPass {
		t.Fatalf("fresh directory resolved to %q, want a fresh random password", first)
	}
	second, err := resolveChildPass(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("reopening the same database directory changed the child password")
	}
	info, err := os.Stat(filepath.Join(dataDir, localChildPassName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("child password file mode = %v, %v", info, err)
	}
	other, err := resolveChildPass(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Fatal("two database directories share a child password")
	}
}

// TestResolveChildPassLegacyRootDatabase covers the upgrade path: a database
// directory that already holds content without a persisted password may have
// been initialized with the historical root/root credential, which SurrealDB
// never rotates — but a missing password file alone never proves that, so
// resolution now refuses to guess and reports errChildPassLegacyVerify. (An
// empty database directory is proven uninitialized and takes the fresh path
// instead.) The caller (startOwnedEngine) verifies by signing in with the
// legacy password against the running child before the legacy choice is
// persisted; a database whose credential was lost out of band fails that
// probe and refuses startup instead of being locked out by a regenerated
// password.
func TestResolveChildPassLegacyRootDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	// Simulate a genuinely initialized database: any directory entry may
	// belong to a database initialized with an unknown password, so the
	// empty-directory fresh path does not apply.
	if err := os.WriteFile(filepath.Join(dbPath, "MANIFEST-000001"), []byte("manifest"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveChildPass(ctx, dataDir); !errors.Is(err, errChildPassLegacyVerify) {
		t.Fatalf("resolveChildPass with missing file on existing database = %v, want errChildPassLegacyVerify", err)
	}
	// Once the legacy password is verified and published, later starts reuse
	// it like any other persisted credential.
	if err := publishChildPass(dataDir, legacyRootPass); err != nil {
		t.Fatalf("publish verified legacy password: %v", err)
	}
	second, err := resolveChildPass(ctx, dataDir)
	if err != nil || second != legacyRootPass {
		t.Fatalf("legacy database second resolve = %q, %v; want the persisted legacy root password", second, err)
	}
	// The runtime descriptor must accept the persisted legacy password too.
	runtime := LocalRuntime{
		Schema: localRuntimeSchema, Token: strings.Repeat("a", 32), PID: os.Getpid(),
		Endpoint: "ws://127.0.0.1:32123", Pass: legacyRootPass, ConfigSHA256: "sha256:" + strings.Repeat("c", 64),
		Surreal: SurrealIdentity{
			Path: "/private/test/surreal", Version: "3.2.0", SHA256: "sha256:" + strings.Repeat("b", 64),
		},
	}
	if _, err := PublishLocalRuntime(t.TempDir(), runtime); err != nil {
		t.Fatalf("publish descriptor with legacy root password: %v", err)
	}
}

func TestResolveChildPassRefusesCorruptFile(t *testing.T) {
	t.Parallel()
	write := func(t *testing.T, dataDir string, mode os.FileMode, contents string) {
		t.Helper()
		path := filepath.Join(dataDir, localChildPassName)
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	valid := `{"schema":"phebs-surreal-child-pass-v1","pass":"` + strings.Repeat("d", 64) + `"}` + "\n"
	for name, setup := range map[string]func(t *testing.T, dataDir string){
		"group-readable": func(t *testing.T, dataDir string) { write(t, dataDir, 0o640, valid) },
		"bad json":       func(t *testing.T, dataDir string) { write(t, dataDir, 0o600, "{}\n") },
		"wrong schema":   func(t *testing.T, dataDir string) { write(t, dataDir, 0o600, `{"schema":"other","pass":"root"}`+"\n") },
		"bad password": func(t *testing.T, dataDir string) {
			write(t, dataDir, 0o600, `{"schema":"phebs-surreal-child-pass-v1","pass":"short"}`+"\n")
		},
		"unknown field": func(t *testing.T, dataDir string) {
			write(t, dataDir, 0o600, `{"schema":"phebs-surreal-child-pass-v1","pass":"root","extra":1}`+"\n")
		},
		"trailing data": func(t *testing.T, dataDir string) { write(t, dataDir, 0o600, valid+"{}\n") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			setup(t, dataDir)
			if _, err := resolveChildPass(context.Background(), dataDir); err == nil {
				t.Fatalf("resolved child password from %s file", name)
			}
		})
	}
	// A directory in place of the file fails closed as well.
	dataDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dataDir, localChildPassName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveChildPass(context.Background(), dataDir); err == nil {
		t.Fatal("resolved child password from a directory")
	}
}

func TestSurrealKVDataDir(t *testing.T) {
	t.Parallel()
	if got := surrealKVDataDir("memory"); got != "" {
		t.Fatalf("surrealKVDataDir(memory) = %q, want empty", got)
	}
	if got := surrealKVDataDir("surrealkv:/data/db"); got != "/data" {
		t.Fatalf("surrealKVDataDir(surrealkv:/data/db) = %q, want /data", got)
	}
	if got := surrealKVDataDir("surrealkv:"); got != "" {
		t.Fatalf("surrealKVDataDir(surrealkv:) = %q, want empty", got)
	}
}

func TestSurrealChildArgsCarryNoPassword(t *testing.T) {
	t.Parallel()
	pass, err := newSurrealChildPass()
	if err != nil {
		t.Fatal(err)
	}
	args := surrealChildArgs("127.0.0.1:0", "memory")
	seenUser := false
	for index, arg := range args {
		if arg == "--pass" {
			t.Fatal("child argv carries --pass")
		}
		if arg == pass {
			t.Fatal("child argv carries the generated password")
		}
		if arg == "--user" && index+1 < len(args) && args[index+1] == "root" {
			seenUser = true
		}
	}
	if !seenUser {
		t.Fatalf("child argv missing --user root: %q", args)
	}
}

func TestLocalRuntimeDescriptorRefusesBadPassword(t *testing.T) {
	t.Parallel()
	base := LocalRuntime{
		Schema: localRuntimeSchema, Token: strings.Repeat("a", 32), PID: os.Getpid(),
		Endpoint: "ws://127.0.0.1:32123", Pass: strings.Repeat("d", 64), ConfigSHA256: "sha256:" + strings.Repeat("c", 64),
		Surreal: SurrealIdentity{
			Path: "/private/test/surreal", Version: "3.2.0", SHA256: "sha256:" + strings.Repeat("b", 64),
		},
	}
	for name, pass := range map[string]string{
		"empty":   "",
		"short":   "abc123",
		"long":    strings.Repeat("d", 65),
		"non-hex": strings.Repeat("z", 64),
	} {
		bad := base
		bad.Pass = pass
		if _, err := PublishLocalRuntime(t.TempDir(), bad); err == nil {
			t.Fatalf("published descriptor with %s password", name)
		}
	}
	// A descriptor already on disk with a malformed password is refused on read.
	dataDir := t.TempDir()
	bad := base
	bad.Pass = "short"
	encoded, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, localRuntimeName), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLocalRuntime(dataDir); err == nil {
		t.Fatal("read descriptor with malformed password")
	}
	// A valid descriptor still cannot be published over the untrusted
	// predecessor: the malformed file on disk blocks the next publisher.
	if _, err := PublishLocalRuntime(dataDir, base); err == nil {
		t.Fatal("published over an untrusted predecessor")
	}
}
