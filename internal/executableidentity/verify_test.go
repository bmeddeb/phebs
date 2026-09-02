package executableidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyRefusesExecutableReplacementAndSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tool")
	original := []byte("#!/bin/sh\nexit 0\n")
	sum := sha256.Sum256(original)
	expected := "sha256:" + hex.EncodeToString(sum[:])

	for _, test := range []struct {
		name   string
		mutate func(string) error
	}{
		{name: "content", mutate: func(path string) error {
			return os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o700)
		}},
		{name: "symlink", mutate: func(path string) error {
			replacement := path + ".replacement"
			if err := os.WriteFile(replacement, original, 0o700); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			return os.Symlink(replacement, path)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, original, 0o700); err != nil {
				t.Fatal(err)
			}
			if actual, err := Digest(path); err != nil || actual != expected {
				t.Fatalf("digest = %q, %v", actual, err)
			}
			if err := Verify(path, expected); err != nil {
				t.Fatal(err)
			}
			if err := test.mutate(path); err != nil {
				t.Fatal(err)
			}
			if err := Verify(path, expected); err == nil {
				t.Fatal("changed executable passed identity verification")
			}
			_ = os.Remove(path)
		})
	}
}
