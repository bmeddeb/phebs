//go:build linux

package t421

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLinuxExternalToolAdmitsRealGitAndGoChildProbes pins the admitted Linux
// child-probe recipe against the real prepared-host images. Both oracles stay
// structural rather than nominal: Git must resolve an exec-path whose core image
// hashes equal to the selected image, so a delegating shim cannot bless an
// unrelated helper, and Go must equal the verifier toolchain exactly. The closed
// probe environment must also override hostile ambient Git/Go variables, and no
// private probe scratch may survive the observation.
func TestLinuxExternalToolAdmitsRealGitAndGoChildProbes(t *testing.T) {
	gitBinary := requireLinuxExternalTool(t, "git")
	goBinary := requireLinuxExternalTool(t, "go")
	// Independent oracle: the ambient environment and an unbounded reader, so the
	// expectation is not produced by the same probe path under test.
	gitVersion := linuxHostToolVersion(t, gitBinary, "--version")
	probeParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", probeParent)
	defer assertExternalProbeParentEmpty(t, probeParent)
	for name, value := range map[string]string{
		"GIT_DIR": "/not-admitted-git", "GIT_EXEC_PATH": "/not-admitted-git-core",
		"GOFLAGS": "-not-admitted", "GOENV": "/not-admitted-go-env", "GOWORK": "/not-admitted-work",
	} {
		t.Setenv(name, value)
	}
	for _, test := range []struct{ role, binary, version string }{
		{"git", gitBinary, gitVersion},
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

// TestLinuxExternalToolRefusesSurrealWithoutExecution pins Linux's single
// admitted SurrealDB route, the sealed custody in
// ProtectLinuxExecutionExternalTool. The public-path observer refuses the role
// before hashing or launching anything, so it cannot issue a second, weaker
// identity for the same bytes, and a rejected script never runs.
func TestLinuxExternalToolRefusesSurrealWithoutExecution(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-run")
	script := writeExternalToolScript(t, fmt.Sprintf("printf called > %q\nprintf '3.2.0 for linux on x86_64\\n'\n", marker))
	for _, binary := range []string{script, requireLinuxExternalTool(t, "surreal")} {
		identity, err := ObserveExecutionExternalTool(t.Context(), "surreal", binary)
		assertExternalToolRefusal(t, identity, err, binary, marker)
		if err.Error() != "external SurrealDB observation requires its sealed Linux custody" {
			t.Fatalf("Linux SurrealDB observation did not name its sealed custody route: %v", err)
		}
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("Linux SurrealDB refusal executed a rejected image: %v", err)
	}
}

// TestLinuxExternalToolRefusesWrongRoleNativeImages proves each admitted role
// stays bound to its own structural oracle. Every image here is a real native
// ELF64 executable that passes the bounded header screen, so only the role
// oracle refuses it, and each refusal names that oracle instead of leaking the
// selected path. This is the Linux analogue of the Darwin delegating-shim case,
// which additionally needs a second Git build this host does not carry.
func TestLinuxExternalToolRefusesWrongRoleNativeImages(t *testing.T) {
	gitBinary := requireLinuxExternalTool(t, "git")
	surrealBinary := requireLinuxExternalTool(t, "surreal")
	for _, test := range []struct{ name, role, binary, want string }{
		{"go role rejects the Git image", "go", gitBinary,
			"external Go version differs from the verifier toolchain"},
		{"git role rejects a foreign native image", "git", executionSystemToolPath("ssh-keygen"),
			"external tool version probe failed or was not source-free"},
		{"git role rejects a valid non-Git version", "git", surrealBinary,
			"external Git version is invalid"},
		{"go role rejects a valid non-Go version", "go", surrealBinary,
			"external Go version differs from the verifier toolchain"},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity, err := ObserveExecutionExternalTool(t.Context(), test.role, test.binary)
			assertExternalToolRefusal(t, identity, err, test.binary)
			if err.Error() != test.want {
				t.Fatalf("wrong-role refusal = %v, want %q", err, test.want)
			}
		})
	}
}

func requireLinuxExternalTool(t *testing.T, role string) string {
	t.Helper()
	// Test selection uses the prepared host PATH; the production API receives the
	// explicit selected path and performs no discovery. Missing tools fail.
	binary, err := exec.LookPath(role)
	if err != nil {
		t.Fatalf("required prepared-host %s image is missing: %v", role, err)
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func linuxHostToolVersion(t *testing.T, binary string, arguments ...string) string {
	t.Helper()
	var stdout, stderr strings.Builder
	command := exec.CommandContext(t.Context(), binary, arguments...)
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("independent host version probe failed: %v", err)
	}
	return strings.TrimSuffix(stdout.String(), "\n")
}

func TestLinuxExternalToolFixedSystemRolesRequireResolvedFixedImages(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-run")
	script := writeExternalToolScript(t, fmt.Sprintf("printf called > %q\nprintf 'bound executable\\n'\n", marker))
	for _, test := range []struct{ name, role, binary string }{
		{"shell wrapper", "sh", script},
		{"missing hdiutil", "hdiutil", executionSystemToolPath("hdiutil")},
		{"wrong fixed image", "ssh-keygen", "/bin/sh"},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity, err := ObserveExecutionExternalTool(t.Context(), test.role, test.binary)
			assertExternalToolRefusal(t, identity, err, test.binary, marker)
		})
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("rejected fixed-system wrapper was executed: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	identity, err := ObserveExecutionExternalTool(ctx, "ssh-keygen", executionSystemToolPath("ssh-keygen"))
	assertExternalToolRefusal(t, identity, err, marker)
	//nolint:staticcheck // Deliberately exercise nil-context refusal at the public boundary.
	identity, err = ObserveExecutionExternalTool(nil, "sh", "/bin/sh")
	assertExternalToolRefusal(t, identity, err, marker)
}

// TestLinuxExternalToolAdmitsResolvedFixedShImage pins the resolved-equality
// semantic for fixed-system roles: the packaged /bin/sh symlink observes its
// resolved shell image. HoldExecutionSystemTool keeps the separate literal-path
// rule and refuses the same symlink (pinned in the system-tool custody tests).
func TestLinuxExternalToolAdmitsResolvedFixedShImage(t *testing.T) {
	resolved, err := filepath.EvalSymlinks("/bin/sh")
	if err != nil || resolved == "/bin/sh" {
		t.Fatalf("frozen host /bin/sh is not the expected packaged symlink: %q, %v", resolved, err)
	}
	for _, binary := range []string{resolved, "/bin/sh"} {
		identity, err := ObserveExecutionExternalTool(t.Context(), "sh", binary)
		if err != nil {
			t.Fatal("resolved fixed shell image was not admitted", binary, err)
		}
		assertExternalToolIdentity(t, identity, "sh", binary, "bound executable")
	}
}

func TestLinuxExternalToolNativeImageScreen(t *testing.T) {
	image := executionSystemToolPath("ssh-keygen")
	if err := validateExternalToolNativeImage(image); err != nil {
		t.Fatal("literal fixed ssh-keygen image was not admitted by the native-image screen", err)
	}
	full, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	write := func(name string, raw []byte, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, raw, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if err := validateExternalToolNativeImage(write("copy", full, 0o700)); err != nil {
		t.Fatal("valid copied native image was refused", err)
	}
	elf32 := append([]byte(nil), full[:256]...)
	elf32[4] = 1
	for name, path := range map[string]string{
		"non-executable": write("non-executable", full, 0o600),
		"empty":          write("empty", nil, 0o700),
		"short":          write("short", full[:8], 0o700),
		"elf32":          write("elf32", elf32, 0o700),
		"script":         writeExternalToolScript(t, "printf 'not an image\\n'"),
		"directory":      root,
		"missing":        filepath.Join(root, "missing-image"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateExternalToolNativeImage(path); err == nil {
				t.Fatal("unadmitted image passed the native-image screen")
			}
		})
	}
	link := filepath.Join(root, "linked-image")
	if err := os.Symlink(image, link); err != nil {
		t.Fatal(err)
	}
	if err := validateExternalToolNativeImage(link); err == nil {
		t.Fatal("symlinked image passed the native-image screen")
	}
}
