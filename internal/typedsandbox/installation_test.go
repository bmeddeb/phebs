package typedsandbox

import (
	"flag"
	"runtime"
	"testing"
)

var nativePreflightImage = flag.String("typed-installation-preflight-image", "", "explicit local native image for read-only installation preflight")

func TestNativeInstallationPreflight(t *testing.T) {
	if *nativePreflightImage == "" {
		t.Skip("explicit native image required")
	}
	if err := ValidateInstallation(t.Context(), "/var/run/docker.sock", *nativePreflightImage); err != nil {
		t.Fatal(err)
	}
}

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
