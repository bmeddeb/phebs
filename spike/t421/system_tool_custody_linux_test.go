//go:build linux

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The frozen Linux rehearsal host provides a literal /usr/bin/ssh-keygen image
// and a packaged /bin/sh symlink; hdiutil is Apple-only and absent. These tests
// pin that inventory; a changed host must re-baseline rather than pass silently.

func linuxSystemToolStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestLinuxSystemToolCustodyHoldsFixedSshKeygen(t *testing.T) {
	path := executionSystemToolPath("ssh-keygen")
	before := linuxSystemToolStat(t, path)
	tool, err := HoldExecutionSystemTool(t.Context(), "ssh-keygen")
	if err != nil {
		t.Fatal("literal fixed ssh-keygen image was not admitted", err)
	}
	t.Cleanup(func() { _ = tool.Close() })
	identity, selected, err := tool.Check(t.Context(), "ssh-keygen")
	if err != nil || selected != path || !inputCustodySame(before, linuxSystemToolStat(t, path)) {
		t.Fatal("fixed image changed or was not selected", err)
	}
	assertExternalToolIdentity(t, identity, "ssh-keygen", selected, "bound executable")
	want := identity
	identity.Role = "changed returned value"
	if again, _, err := tool.Check(t.Context(), "ssh-keygen"); err != nil || again != want {
		t.Fatal("returned identity was not an independent value", err)
	}
	for range 2 {
		if tool.Close() != nil {
			t.Fatal("descriptor release failed")
		}
	}
	if _, err := tool.file.Stat(); err == nil || !inputCustodySame(before, linuxSystemToolStat(t, path)) {
		t.Fatal("Close retained the descriptor or changed the platform image")
	}
	if _, _, err := tool.Check(t.Context(), "ssh-keygen"); err == nil {
		t.Fatal("closed image custody accepted")
	}
}

func TestLinuxSystemToolCustodyRefusesUnadmittedFixedImages(t *testing.T) {
	if resolved, err := filepath.EvalSymlinks(executionSystemToolPath("sh")); err != nil || resolved == executionSystemToolPath("sh") {
		t.Fatalf("frozen host /bin/sh is not the expected packaged symlink: %q, %v", resolved, err)
	}
	if tool, err := HoldExecutionSystemTool(t.Context(), "sh"); err == nil || tool != nil {
		t.Fatal("symlinked fixed /bin/sh was admitted by the literal-path hold")
	}
	if _, err := os.Lstat(executionSystemToolPath("hdiutil")); !os.IsNotExist(err) {
		t.Fatalf("frozen Linux host unexpectedly provides hdiutil: %v", err)
	}
	if tool, err := HoldExecutionSystemTool(t.Context(), "hdiutil"); err == nil || tool != nil {
		t.Fatal("absent fixed hdiutil image was admitted")
	}
	if tool, err := HoldExecutionSystemTool(t.Context(), "git"); err == nil || tool != nil {
		t.Fatal("non-platform role admitted")
	}
	if tool, err := HoldExecutionSystemTool(t.Context(), "unknown-role"); err == nil || tool != nil {
		t.Fatal("unknown role admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if tool, err := HoldExecutionSystemTool(ctx, "ssh-keygen"); err == nil || tool != nil {
		t.Fatal("canceled hold admitted")
	}
	//nolint:staticcheck // Deliberately exercise nil-context refusal at the public boundary.
	if tool, err := HoldExecutionSystemTool(nil, "ssh-keygen"); err == nil || tool != nil {
		t.Fatal("nil-context hold admitted")
	}
}

func TestLinuxSystemToolCustodyRefusalsStick(t *testing.T) {
	for _, mode := range []string{"role", "context", "path", "volume", "descriptor"} {
		t.Run(mode, func(t *testing.T) {
			tool, err := HoldExecutionSystemTool(t.Context(), "ssh-keygen")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tool.Close() })
			ctx, role := t.Context(), "ssh-keygen"
			switch mode {
			case "role":
				role = "hdiutil"
			case "context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "path":
				tool.path = "/usr/bin/git"
			case "volume":
				tool.volume[0] ^= 1
			case "descriptor":
				_ = tool.file.Close()
			}
			identity, path, err := tool.Check(ctx, role)
			if err == nil || identity != (ExecutionToolIdentity{}) || path != "" {
				t.Fatal("invalid custody returned image authority")
			}
			if _, _, err := tool.Check(t.Context(), "ssh-keygen"); err == nil {
				t.Fatal("refused custody recovered silently")
			}
		})
	}
}

func TestLinuxSystemToolVolumeScreensSystemImages(t *testing.T) {
	writable, err := os.Create(filepath.Join(t.TempDir(), "writable-image"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writable.Close() }()
	if held, err := writable.Stat(); err != nil {
		t.Fatal(err)
	} else if _, err := systemToolReadOnlyVolume(writable, held); err == nil {
		t.Fatal("writable fixture accepted as a fixed-system image")
	}
	volume := func(path string) ([2]int32, error) {
		t.Helper()
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		held, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return systemToolReadOnlyVolume(file, held)
	}
	for _, path := range []string{"/usr/lib/os-release", "/etc/hostname"} {
		if _, err := volume(path); err == nil {
			t.Fatal("root-owned non-executable image accepted", path)
		}
	}
	if info, err := os.Lstat("/usr/bin/passwd"); err == nil && info.Mode()&os.ModeSetuid != 0 {
		if _, err := volume("/usr/bin/passwd"); err == nil {
			t.Fatal("setuid image accepted as a fixed-system image")
		}
	}
	first, err := volume("/usr/bin/gnutrue")
	if err != nil || first == ([2]int32{}) {
		t.Fatal("root-owned executable image refused", err)
	}
	if second, err := volume("/usr/bin/gnutrue"); err != nil || second != first {
		t.Fatal("fixed-system volume tuple was not stable", err)
	}
}
