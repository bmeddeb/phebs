//go:build !linux

package typedworkspace

import "testing"

func TestUnsupportedPlatformRefusesPrivateCustody(t *testing.T) {
	if _, err := ObserveCapacity(t.Context(), t.TempDir()); err == nil {
		t.Fatal("unsupported platform observed private capacity")
	}
	// In particular, Darwin ACL grants are not described by POSIX mode bits.
	if file, err := openDirectory(t.TempDir(), true); err == nil || file != nil {
		t.Fatal("unsupported platform acquired private custody")
	}
}
