//go:build linux

package t421

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestRenameExecutionSignerExclusiveMovesOnceAndRefusesReplacement pins the
// Linux promotion seam: the temporary name moves inside one held directory
// descriptor, and an existing destination is never replaced.
func TestRenameExecutionSignerExclusiveMovesOnceAndRefusesReplacement(t *testing.T) {
	root := t.TempDir()
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = directory.Close() })
	fd := int(directory.Fd())
	source := filepath.Join(root, "temporary-key")
	destination := filepath.Join(root, "private-key")
	want := []byte("exact retained signer material")
	if err := os.WriteFile(source, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameExecutionSignerExclusive(fd, fd, "temporary-key", "private-key"); err != nil {
		t.Fatal("exclusive promotion refused a fresh destination", err)
	}
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Fatal("promotion retained the temporary name", err)
	}
	if raw, err := os.ReadFile(destination); err != nil || !bytes.Equal(raw, want) {
		t.Fatal("promoted destination differs from the temporary bytes", err)
	}
	info, err := os.Lstat(destination)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatal("promoted destination identity changed", err)
	}
	if err := os.WriteFile(source, []byte("second attempt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameExecutionSignerExclusive(fd, fd, "temporary-key", "private-key"); err == nil {
		t.Fatal("exclusive promotion replaced an existing destination")
	}
	if raw, err := os.ReadFile(source); err != nil || string(raw) != "second attempt" {
		t.Fatal("refused promotion consumed the temporary name", err)
	}
	if raw, err := os.ReadFile(destination); err != nil || !bytes.Equal(raw, want) {
		t.Fatal("refused promotion changed the destination", err)
	}
}
