package t4013

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionControlsIgnoreAmbientAndReopenExactCustody(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "custody")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	gitCore, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	host := hostToolchainBinding{gitCore: boundExecutable{path: gitCore}}
	controls, controlDigest, err := createExecutionControls(workspace, host)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"HOME=/ambient/home", "PATH=/ambient/path", "TMPDIR=/ambient/tmp",
		"GOMODCACHE=/ambient/mod", "GOCACHE=/ambient/build", "BASH_ENV=/ambient/bash",
		"ENV=/ambient/env", "SHELL=/ambient/shell", "XDG_CONFIG_HOME=/ambient/config",
	} {
		name, replacement, _ := strings.Cut(value, "=")
		t.Setenv(name, replacement)
	}

	environment := map[string]string{}
	for _, entry := range executionEnvironmentForControls(controls, false) {
		name, value, _ := strings.Cut(entry, "=")
		environment[name] = value
	}
	for name, want := range map[string]string{
		"HOME": controls.Home, "PATH": controls.GitExecPath, "TMPDIR": controls.Temp,
		"TEMP": controls.Temp, "TMP": controls.Temp, "GOTMPDIR": controls.Temp,
		"GOMODCACHE": controls.ModuleCache, "GOCACHE": controls.BuildCache,
		"XDG_CONFIG_HOME": controls.Home, "XDG_CACHE_HOME": controls.Temp,
		"GIT_EXEC_PATH": controls.GitExecPath,
	} {
		if got := environment[name]; got != want {
			t.Fatalf("closed control %s = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"BASH_ENV", "ENV", "SHELL"} {
		if _, ok := environment[name]; ok {
			t.Fatalf("ambient shell control survived: %s", name)
		}
	}
	if reopened, err := openExecutionControls(workspace, controlDigest, host, true); err != nil || reopened != controls {
		t.Fatalf("reopen exact execution controls: %v", err)
	}
}

func TestExecutionControlsRefuseReplacementAndBoundModuleSnapshot(t *testing.T) {
	newControls := func(t *testing.T) (string, executionControls, string, hostToolchainBinding) {
		t.Helper()
		workspace := filepath.Join(t.TempDir(), "custody")
		if err := os.Mkdir(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		gitCore, err := filepath.Abs(os.Args[0])
		if err != nil {
			t.Fatal(err)
		}
		host := hostToolchainBinding{gitCore: boundExecutable{path: gitCore}}
		controls, controlDigest, err := createExecutionControls(workspace, host)
		if err != nil {
			t.Fatal(err)
		}
		return workspace, controls, controlDigest, host
	}

	t.Run("manifest", func(t *testing.T) {
		workspace, _, controlDigest, host := newControls(t)
		path := filepath.Join(workspace, executionControlsFilename)
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := openExecutionControls(workspace, controlDigest, host, true); err == nil {
			t.Fatal("replaced execution control manifest passed")
		}
	})

	t.Run("temporary directory", func(t *testing.T) {
		workspace, controls, controlDigest, host := newControls(t)
		if err := os.Remove(controls.Temp); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), controls.Temp); err != nil {
			t.Fatal(err)
		}
		if _, err := openExecutionControls(workspace, controlDigest, host, true); err == nil {
			t.Fatal("replaced private temporary directory passed")
		}
	})

	t.Run("runtime cache", func(t *testing.T) {
		workspace, controls, controlDigest, host := newControls(t)
		if err := os.Mkdir(controls.ModuleCache, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := openExecutionControls(workspace, controlDigest, host, true); err == nil {
			t.Fatal("reappearing private module cache passed")
		}
	})

	t.Run("module snapshot", func(t *testing.T) {
		_, controls, _, _ := newControls(t)
		if err := os.Mkdir(controls.ModuleCache, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(controls.ModuleCache, "module")
		if err := os.WriteFile(path, []byte("reviewed"), 0o400); err != nil {
			t.Fatal(err)
		}
		before, err := privateCacheDigest(t.Context(), controls.ModuleCache)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("poisoned"), 0o600); err != nil {
			t.Fatal(err)
		}
		after, err := privateCacheDigest(t.Context(), controls.ModuleCache)
		if err != nil {
			t.Fatal(err)
		}
		if before == after {
			t.Fatal("changed private module snapshot retained its digest")
		}
		if _, err := os.Lstat(controls.BuildCache); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("private build cache is not absent: %v", err)
		}
	})

	t.Run("read-only module cache cleanup", func(t *testing.T) {
		_, controls, _, _ := newControls(t)
		nested := filepath.Join(controls.ModuleCache, "example@v1")
		if err := os.MkdirAll(nested, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nested, "module.go"), []byte("package example\n"), 0o400); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{nested, controls.ModuleCache} {
			if err := os.Chmod(path, 0o500); err != nil {
				t.Fatal(err)
			}
		}
		if err := removePrivateGoCache(controls.ModuleCache); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(controls.ModuleCache); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read-only private module cache survived: %v", err)
		}
	})
}
