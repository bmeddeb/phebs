package typedsandbox

import (
	"runtime"
	"testing"
)

func TestInstallationPreflightCreatesNoContainer(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux native installation")
	}
	for _, fault := range []string{"", "missing seccomp", "seccomp only", "image environment", "image volume", "oversized info"} {
		t.Run(fault, func(t *testing.T) {
			d, options := fakeDaemon(t, fault)
			err := ValidateInstallation(t.Context(), options.Socket, options.ImageID)
			if (err == nil) != (fault == "") {
				t.Fatal("installation preflight", err)
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.created || d.started || d.removed {
				t.Fatal("preflight mutated daemon")
			}
		})
	}
}
