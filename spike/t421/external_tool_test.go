//go:build darwin || linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

func TestObserveExecutionExternalToolBindsRealGitAndGo(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	gitVersion := fixture.command(t, "--version")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goBinary, err = filepath.EvalSymlinks(goBinary)
	if err != nil {
		t.Fatal(err)
	}
	probeParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", probeParent)
	defer assertExternalProbeParentEmpty(t, probeParent)
	for name, value := range map[string]string{
		"GIT_DIR": "/private/not-admitted-git", "GIT_EXEC_PATH": "/private/not-admitted-git-core",
		"GOFLAGS": "-not-admitted", "GOENV": "/private/not-admitted-go-env", "GOWORK": "/private/not-admitted-work",
	} {
		t.Setenv(name, value)
	}
	for _, test := range []struct{ role, binary, version string }{
		{"git", fixture.git, gitVersion},
		{"go", goBinary, "go version " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH},
	} {
		t.Run(test.role, func(t *testing.T) {
			identity, err := ObserveExecutionExternalTool(t.Context(), test.role, test.binary)
			if err != nil {
				t.Fatal(err)
			}
			assertExternalToolIdentity(t, identity, test.role, test.binary, test.version)
		})
	}
}

func TestObserveExecutionExternalToolBindsFixedNativeImagesWithoutVersionProbe(t *testing.T) {
	requireExternalToolFrozenHost(t)
	for _, test := range []struct{ role, path string }{
		{"sh", "/bin/sh"}, {"hdiutil", "/usr/bin/hdiutil"}, {"ssh-keygen", "/usr/bin/ssh-keygen"},
	} {
		t.Run(test.role, func(t *testing.T) {
			identity, err := ObserveExecutionExternalTool(t.Context(), test.role, test.path)
			if err != nil {
				t.Fatal(err)
			}
			assertExternalToolIdentity(t, identity, test.role, test.path, "bound executable")
		})
	}
}

func TestObserveExecutionExternalToolRefusesUnadmittedRolePathAndContext(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "must-not-run")
	script := writeExternalToolScript(t, fmt.Sprintf("printf called > %q\nprintf '# changed\\n' >> \"$0\"\nprintf 'git version 2.50.1\\n'\n", marker))
	for _, test := range []struct{ name, role, binary string }{
		{"unknown role", "unknown", "/bin/sh"},
		{"repository role", "phebs", "/bin/sh"},
		{"missing executor", "t422-execute", "/bin/sh"},
		{"relative path", "git", "git"},
		{"padded path", "git", " /usr/bin/git"},
		{"missing path", "go", filepath.Join(root, "private-missing-image")},
		{"directory", "git", root},
		{"shell wrapper", "git", script},
		{"wrong fixed shell", "sh", script},
		{"wrong fixed hdiutil", "hdiutil", "/bin/sh"},
		{"wrong fixed ssh-keygen", "ssh-keygen", "/bin/sh"},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity, err := ObserveExecutionExternalTool(t.Context(), test.role, test.binary)
			assertExternalToolRefusal(t, identity, err, test.binary, marker)
		})
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("public observation executed a rejected shell wrapper: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	identity, err := ObserveExecutionExternalTool(ctx, "sh", "/bin/sh")
	assertExternalToolRefusal(t, identity, err, marker)
	//nolint:staticcheck // Deliberately exercise nil-context refusal at the public boundary.
	identity, err = ObserveExecutionExternalTool(nil, "sh", "/bin/sh")
	assertExternalToolRefusal(t, identity, err, marker)
}

func TestObserveExecutionExternalToolRefusesVersionOfWrongRole(t *testing.T) {
	requireExternalToolFrozenHost(t)
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ObserveExecutionExternalTool(t.Context(), "surreal", goBinary)
	assertExternalToolRefusal(t, identity, err, goBinary)
}

func TestObserveExecutionExternalToolRefusesDelegatingAppleGitShim(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	actual, err := executableidentity.Digest(fixture.git)
	if err != nil {
		t.Fatal(err)
	}
	shim, err := executableidentity.Digest("/usr/bin/git")
	if err != nil {
		t.Fatal(err)
	}
	if shim == actual {
		t.Skip("platform Git is already the actual core image")
	}
	probeParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", probeParent)
	defer assertExternalProbeParentEmpty(t, probeParent)
	identity, err := ObserveExecutionExternalTool(t.Context(), "git", "/usr/bin/git")
	assertExternalToolRefusal(t, identity, err, fixture.git)
}

func TestObserveExecutionExternalToolOptionalRealSurreal(t *testing.T) {
	requireExternalToolFrozenHost(t)
	binary := os.Getenv("PHEBS_T422_EXTERNAL_SURREAL")
	if binary == "" {
		t.Skip("set PHEBS_T422_EXTERNAL_SURREAL to an explicit supported native SurrealDB image")
	}
	identity, err := ObserveExecutionExternalTool(t.Context(), "surreal", binary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(identity.Version, "3.") {
		t.Fatalf("SurrealDB version is not supported: %q", identity.Version)
	}
	assertExternalToolIdentity(t, identity, "surreal", binary, identity.Version)
}

func TestRunExternalToolProbeClosesEnvironmentAndWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"PHEBS_EXTERNAL_PRIVATE_SENTINEL": "never-inherit-this", "GIT_DIR": "/private/not-admitted",
		"GOFLAGS": "-private-not-admitted", "GOENV": "/private/not-admitted", "GOWORK": "/private/not-admitted",
	} {
		t.Setenv(name, value)
	}
	body := fmt.Sprintf(`
[ "$1" = --version ] || exit 71
[ "$PWD" = %q ] || exit 72
[ "$HOME" = "$PWD" ] && [ "$PATH" = "$PWD" ] || exit 73
[ "$GOENV" = off ] && [ "$GOWORK" = off ] && [ "$GOTOOLCHAIN" = local ] || exit 74
[ -z "${PHEBS_EXTERNAL_PRIVATE_SENTINEL+x}" ] && [ -z "${GIT_DIR+x}" ] && [ -z "${GOFLAGS+x}" ] || exit 75
printf 'git version 2.50.1\n'
`, root)
	output, err := runExternalToolProbe(t.Context(), root, writeExternalToolScript(t, body), externalToolEnvironment(root), "--version")
	if err != nil || output != "git version 2.50.1" {
		t.Fatalf("closed probe environment or cwd differs: %q, %v", output, err)
	}
}

func TestRunExternalToolProbeBoundsOutputAndPreservesVersionValidation(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		wantError        bool
		publicInvalid    bool
	}{
		{name: "one newline", body: "printf 'git version 2.50.1\\n'", want: "git version 2.50.1"},
		{name: "extra newline remains invalid", body: "printf 'git version 2.50.1\\n\\n'", want: "git version 2.50.1\n", publicInvalid: true},
		{name: "multiple lines remain invalid", body: "printf 'git version 2.50.1\\nprivate-detail\\n'", want: "git version 2.50.1\nprivate-detail", publicInvalid: true},
		{name: "source path remains invalid", body: "printf 'git version 2.50.1 /private/not-admitted\\n'", want: "git version 2.50.1 /private/not-admitted", publicInvalid: true},
		{name: "stderr", body: "printf 'git version 2.50.1\\n'; printf 'private-diagnostic-sentinel' >&2", wantError: true},
		{name: "exit status", body: "printf 'private-diagnostic-sentinel'; exit 19", wantError: true},
		{name: "exact stdout bound", body: "printf '%s' '" + strings.Repeat("x", 4096) + "'", want: strings.Repeat("x", 4096), publicInvalid: true},
		{name: "stdout overflow", body: "printf '%s' '" + strings.Repeat("x", 4097) + "'", wantError: true},
		{name: "stderr overflow", body: "printf '%s' '" + strings.Repeat("x", 4097) + "' >&2", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			output, err := runExternalToolProbe(t.Context(), root, writeExternalToolScript(t, test.body), externalToolEnvironment(root))
			if test.wantError {
				if err == nil || output != "" || strings.Contains(err.Error(), "private-diagnostic-sentinel") || strings.Contains(err.Error(), root) {
					t.Fatalf("failed probe leaked output or admitted invalid state: bytes=%d error=%v", len(output), err)
				}
				return
			}
			if err != nil || output != test.want {
				t.Fatalf("bounded probe = %q, %v", output, err)
			}
			if test.publicInvalid && validPublicToolVersion(output) {
				t.Fatal("source-bearing or malformed probe output is a valid public version")
			}
		})
	}
}

func TestRunExternalToolProbeCancellationKillsOwnedDescendant(t *testing.T) {
	root := t.TempDir()
	pidPath := filepath.Join(root, "child-pid")
	body := fmt.Sprintf("/bin/sleep 30 & child=$!; printf '%%s\\n' \"$child\" > %q; wait\n", pidPath)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	start := time.Now()
	output, err := runExternalToolProbe(ctx, root, writeExternalToolScript(t, body), externalToolEnvironment(root))
	if err == nil || output != "" || time.Since(start) > 5*time.Second {
		t.Fatalf("external probe cancellation was not bounded: %v", err)
	}
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		t.Fatalf("owned external helper identity is invalid: %q", raw)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		} else if err != nil {
			t.Fatalf("owned external helper liveness is unavailable: %v", err)
		}
		select {
		case <-deadline.C:
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("owned helper survived canceled external probe")
		case <-tick.C:
		}
	}
}

func requireExternalToolFrozenHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("public external-tool observations require the frozen Darwin/arm64 host")
	}
}

func assertExternalToolIdentity(t *testing.T, got ExecutionToolIdentity, role, binary, version string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := executableidentity.Digest(resolved)
	if err != nil {
		t.Fatal(err)
	}
	want := ExecutionToolIdentity{Role: role, FileType: regularFileType, SHA256: digest, Version: version, Provenance: "external-executed-file-v1"}
	if got != want || !validPublicToolVersion(got.Version) || strings.Contains(got.Version, resolved) {
		t.Fatalf("external identity = %#v, want %#v", got, want)
	}
}

func assertExternalToolRefusal(t *testing.T, identity ExecutionToolIdentity, err error, private ...string) {
	t.Helper()
	if err == nil || identity != (ExecutionToolIdentity{}) {
		t.Fatalf("unadmitted external identity = %#v, %v", identity, err)
	}
	for _, value := range private {
		if filepath.IsAbs(value) && strings.Contains(err.Error(), value) {
			t.Fatalf("external refusal leaked private input: %v", err)
		}
	}
}

func assertExternalProbeParentEmpty(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("external probe retained private workspace: entries=%d error=%v", len(entries), err)
	}
}

func writeExternalToolScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "neutral-external-probe")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
