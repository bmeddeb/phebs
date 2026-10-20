package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

func TestSelectedSurrealRoutePreservesOrdinaryDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "surreal")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if info, selected, err := selectedSurrealBinaryInfo(path); info != nil || selected || err != nil {
		t.Fatal("ordinary path acquired selected authority", selected, err)
	}
	digest, err := executableidentity.Digest(path)
	if err != nil || verifySurrealExecutable(path, digest) != nil {
		t.Fatal("ordinary digest refused", err)
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if verifySurrealExecutable(link, digest) == nil {
		t.Fatal("unselected symlink bypassed the strict verifier")
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if verifySurrealExecutable(path, digest) == nil {
		t.Fatal("ordinary replacement accepted")
	}
}
