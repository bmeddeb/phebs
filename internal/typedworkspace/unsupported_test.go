//go:build !linux

package typedworkspace

import "testing"

func TestUnsupportedPlatformRefusesPrivateCustody(t *testing.T) {
	// In particular, Darwin ACL grants are not described by POSIX mode bits.
	if file, err := openDirectory(t.TempDir(), true); err == nil || file != nil {
		t.Fatal("unsupported platform acquired private custody")
	}
}
