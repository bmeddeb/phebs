package store

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLegacyChildCommandCannotInheritInitialization(t *testing.T) {
	for _, key := range []string{"SURREAL_USER", "SURREAL_PASS", "SURREAL_UNAUTHENTICATED", "SURREAL_IMPORT_FILE", "SURREAL_DEFAULT_NAMESPACE", "SURREAL_DEFAULT_DATABASE"} {
		t.Setenv(key, "must-not-reach-probe")
	}
	t.Setenv("PHEBS_PROBE_ENV_TEST", "keep")
	cmd := legacyChildCommand(t.Context(), "/test/surreal", "127.0.0.1:1234", "surrealkv:/test/db")
	want := []string{"/test/surreal", "start", "--bind", "127.0.0.1:1234", "--no-defaults", "--log", "warn", "surrealkv:/test/db"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("probe args = %q, want %q", cmd.Args, want)
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "SURREAL_") {
			t.Fatal("probe inherited an ambient SurrealDB control")
		}
	}
	if !slices.Contains(cmd.Env, "PHEBS_PROBE_ENV_TEST=keep") {
		t.Fatal("probe removed unrelated environment")
	}
}

func TestResolveChildPassRefusesNonDirectoryDatabase(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "db"), []byte("not a database directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveChildPass(t.Context(), dataDir); err == nil {
		t.Fatal("non-directory database accepted")
	}
	if _, err := os.Lstat(filepath.Join(dataDir, localChildPassName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid database path minted a password")
	}
}

func TestLegacyProbeDoesNotInitializeUnknownRoot(t *testing.T) {
	binary := surrealTestBinary(t)
	t.Setenv("PHEBS_SURREAL", binary)
	// The proof must hold even when inherited initialization/authentication
	// options would create a root, import a script or disable authentication.
	t.Setenv("SURREAL_USER", "root")
	t.Setenv("SURREAL_PASS", "ambient-unknown-password")
	t.Setenv("SURREAL_UNAUTHENTICATED", "true")
	t.Setenv("SURREAL_DEFAULT_NAMESPACE", "ambient")
	t.Setenv("SURREAL_DEFAULT_DATABASE", "ambient")
	importFile := filepath.Join(t.TempDir(), "must-not-import.surql")
	if err := os.WriteFile(importFile, []byte("DEFINE USER root ON ROOT PASSWORD 'ambient-import-password' ROLES OWNER;"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SURREAL_IMPORT_FILE", importFile)
	for _, name := range []string{"unrelated entry", "rootless engine"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), childPassTestTimeout)
			defer cancel()
			dataDir := t.TempDir()
			dbPath := filepath.Join(dataDir, "db")
			if err := os.Mkdir(dbPath, 0o700); err != nil {
				t.Fatal(err)
			}
			if name == "unrelated entry" {
				if err := os.WriteFile(filepath.Join(dbPath, ".DS_Store"), []byte("unrelated file"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				_, child := startRawSurrealOnLoopbackForTest(t, ctx, time.Second, func(addr string) *exec.Cmd {
					return legacyChildCommand(ctx, binary, addr, "surrealkv:"+dbPath)
				})
				child.stop()
			}
			for range 2 {
				_, engine, err := startOwnedEngine(ctx, "surrealkv:"+dbPath)
				if engine != nil {
					t.Cleanup(engine.stop)
				}
				if err == nil || !strings.Contains(err.Error(), "legacy root sign-in failed") {
					t.Fatalf("probe must refuse rootless database: %v", err)
				}
				if _, err := os.Lstat(filepath.Join(dataDir, localChildPassName)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed legacy probe published a credential")
				}
			}
			// Explicit initialization in this disposable fixture proves that
			// neither failed probe created an inaccessible root. Production
			// never guesses a replacement credential for ambiguous state.
			pass, err := newSurrealChildPass()
			if err != nil {
				t.Fatal(err)
			}
			release, err := acquireChildPassLock(ctx, dataDir)
			if err != nil {
				t.Fatal(err)
			}
			err = publishChildPass(dataDir, pass)
			release()
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"SURREAL_USER", "SURREAL_PASS", "SURREAL_UNAUTHENTICATED", "SURREAL_IMPORT_FILE", "SURREAL_DEFAULT_NAMESPACE", "SURREAL_DEFAULT_DATABASE"} {
				t.Setenv(key, "")
				if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			runtime, engine, err := startOwnedEngine(ctx, "surrealkv:"+dbPath)
			if engine != nil {
				t.Cleanup(engine.stop)
			}
			if err != nil {
				t.Fatal(err)
			}
			if runtime.Pass != pass {
				t.Fatal("explicit initialization did not retain its durable credential")
			}
			if err := probeChildRootSignIn(ctx, strings.TrimPrefix(runtime.Endpoint, "ws://"), pass); err != nil {
				t.Fatalf("probe created an inaccessible root: %v", err)
			}
		})
	}
}

func TestOwnedEngineCleanupPrecedesFixtureRemoval(t *testing.T) {
	t.Setenv("PHEBS_SURREAL", surrealTestBinary(t))
	var pid int
	var dataDir string
	t.Run("early return", func(t *testing.T) {
		dataDir = t.TempDir()
		ctx, cancel := context.WithTimeout(t.Context(), childPassTestTimeout)
		defer cancel()
		runtime, engine, err := startOwnedEngine(ctx, "surrealkv:"+filepath.Join(dataDir, "db"))
		if engine != nil {
			t.Cleanup(engine.stop)
		}
		if err != nil {
			t.Fatal(err)
		}
		pid = runtime.PID
		// No explicit stop: cleanup owns all early returns, including Fatal.
	})
	if pid != 0 && processAlive(pid) {
		t.Fatal("child survived the fixture's cleanup-only path")
	}
	if _, err := os.Lstat(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("subtest fixture was not removed after child cleanup")
	}
}
