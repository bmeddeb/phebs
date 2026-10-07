//go:build linux

package t4013

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLinuxProcessExecutablePathMatchesCurrentImage(t *testing.T) {
	want, err := os.Executable()
	if err == nil {
		want, err = filepath.EvalSymlinks(want)
	}
	got, gotErr := ObserveProcessExecutablePath(t.Context(), os.Getpid())
	if err != nil || gotErr != nil || got != want {
		t.Fatalf("executable=%q,%v; want=%q,%v", got, gotErr, want, err)
	}
	if _, err := ObserveProcessExecutablePath(t.Context(), 1<<30); err == nil {
		t.Fatal("missing process accepted")
	}
	for _, path := range []string{"", "relative", "/bin/../bin/test", "/test (deleted)", "/test\x00", "/\xff", "/" + strings.Repeat("x", maxLinuxProcessPathBytes)} {
		if _, err := validateLinuxProcessExecutablePath(path); err == nil {
			t.Fatalf("invalid path admitted: %.30q", path)
		}
	}
}

func TestLinuxDeletedExecutableRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sleep")
	raw, err := os.ReadFile("/bin/sleep")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, path, "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	if _, err := ObserveProcessExecutablePath(ctx, child.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveProcessExecutablePath(ctx, child.Process.Pid); err == nil {
		t.Fatal("deleted image accepted")
	}
}
