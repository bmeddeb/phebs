//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package recovery

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRecoveryRegularOpenRefusesFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte("neutral"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatal(err)
	}
	// Substitute after an otherwise successful caller-side regular check.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if file, err := openRecoveryRegular(path); err == nil {
		_ = file.Close()
		t.Fatal("FIFO descriptor accepted")
	}
	if _, err := digestFile(t.Context(), path, 1<<20); err == nil {
		t.Fatal("FIFO digest accepted")
	}
	if _, err := readManifest(path); err == nil {
		t.Fatal("FIFO manifest accepted")
	}
}

func TestRecoveryRegularOpenPreservesRegularSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(path, []byte("neutral"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, path+"-link"); err != nil {
		t.Fatal(err)
	}
	digest, err := digestFile(t.Context(), path+"-link", 1<<20)
	if err != nil || digest != digestBytes([]byte("neutral")) {
		t.Fatalf("regular symlink digest changed: %q %v", digest, err)
	}
}
