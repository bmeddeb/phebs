//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutionSystemToolCustodyHoldsActualFixedImages(t *testing.T) {
	requireExternalToolFrozenHost(t)
	for _, role := range []string{"sh", "hdiutil", "ssh-keygen"} {
		t.Run(role, func(t *testing.T) {
			path := executionSystemToolPath(role)
			before := inputCustodyTestStat(t, path)
			tool, err := HoldExecutionSystemTool(t.Context(), role)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tool.Close() })
			identity, selected, err := tool.Check(t.Context(), role)
			if err != nil || selected != path || !inputCustodySame(before, inputCustodyTestStat(t, path)) {
				t.Fatal("fixed image changed or was not selected", err)
			}
			assertExternalToolIdentity(t, identity, role, selected, "bound executable")
			want := identity
			identity.Role = "changed returned value"
			if again, _, err := tool.Check(t.Context(), role); err != nil || again != want {
				t.Fatal("returned identity was not an independent value", err)
			}
			for range 2 {
				if tool.Close() != nil {
					t.Fatal("descriptor release failed")
				}
			}
			if _, err := tool.file.Stat(); err == nil || !inputCustodySame(before, inputCustodyTestStat(t, path)) {
				t.Fatal("Close retained the descriptor or changed the platform image")
			}
			if _, _, err := tool.Check(t.Context(), role); err == nil {
				t.Fatal("closed image custody accepted")
			}
		})
	}
}

func TestExecutionSystemToolCustodyRefusalsStick(t *testing.T) {
	requireExternalToolFrozenHost(t)
	for _, mode := range []string{"role", "context", "path", "volume", "descriptor"} {
		t.Run(mode, func(t *testing.T) {
			tool, err := HoldExecutionSystemTool(t.Context(), "sh")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tool.Close() })
			ctx, role := t.Context(), "sh"
			switch mode {
			case "role":
				role = "hdiutil"
			case "context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "path":
				tool.path = "/usr/bin/ssh-keygen"
			case "volume":
				tool.volume[0] ^= 1
			case "descriptor":
				_ = tool.file.Close()
			}
			identity, path, err := tool.Check(ctx, role)
			if err == nil || identity != (ExecutionToolIdentity{}) || path != "" {
				t.Fatal("invalid custody returned image authority")
			}
			if _, _, err := tool.Check(t.Context(), "sh"); err == nil {
				t.Fatal("refused custody recovered silently")
			}
		})
	}
	if tool, err := HoldExecutionSystemTool(t.Context(), "git"); err == nil || tool != nil {
		t.Fatal("non-platform role admitted")
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "owned-test-image"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := systemToolReadOnlyVolume(file, info); err == nil {
		t.Fatal("writable fixture accepted as a fixed read-only platform image")
	}
}
