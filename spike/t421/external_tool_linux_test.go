//go:build linux

package t421

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLinuxExternalToolRefusesChildProbeRolesWithoutExecution(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-run")
	script := writeExternalToolScript(t, fmt.Sprintf("printf called > %q\n", marker))
	assertRefusal := func(t *testing.T, role, binary string) {
		t.Helper()
		identity, err := ObserveExecutionExternalTool(t.Context(), role, binary)
		assertExternalToolRefusal(t, identity, err, binary)
		if err == nil || err.Error() != "external tool version probes are not admitted on Linux" {
			t.Fatalf("child-probe role %q did not hit the Linux probe refusal: %v", role, err)
		}
	}
	for _, role := range []string{"git", "go", "surreal"} {
		t.Run(role, func(t *testing.T) {
			assertRefusal(t, role, script)
			if binary, err := exec.LookPath(role); err == nil {
				assertRefusal(t, role, binary)
			}
		})
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("Linux child-probe refusal executed a rejected image: %v", err)
	}
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
