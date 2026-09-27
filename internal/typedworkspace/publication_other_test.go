//go:build !linux

package typedworkspace

import (
	"os"
	"testing"
)

func TestPublicationMutationRefusesUnsupportedPlatform(t *testing.T) {
	dir := t.TempDir()
	if release, err := AcquirePublicationMutation(t.Context(), dir); err == nil {
		release()
		t.Fatal("unsupported publication lock acquired")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("unsupported lock grew custody", entries, err)
	}
}
