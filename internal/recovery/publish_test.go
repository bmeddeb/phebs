package recovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishDirectoryNeverReplacesExistingTarget(t *testing.T) {
	parent := t.TempDir()
	stage := filepath.Join(parent, "stage")
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "new"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := publishDirectory(stage, target); err == nil {
		t.Fatal("publishDirectory replaced an existing target")
	}
	if _, err := os.Stat(filepath.Join(stage, "new")); err != nil {
		t.Fatalf("failed publication consumed staging directory: %v", err)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("existing empty target changed: %v, %v", entries, err)
	}
}
