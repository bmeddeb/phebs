package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

func TestSelectedIndexRoutePreservesOrdinaryResolutionAndDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "indexer")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := executablePath(link)
	if err != nil || resolved != path {
		t.Fatal("ordinary resolution changed", resolved, err)
	}
	digest, err := executableidentity.Digest(path)
	if err != nil || verifyIndexExecutable(resolved, digest) != nil {
		t.Fatal("ordinary digest refused", err)
	}
	if verifyIndexExecutable(link, digest) == nil {
		t.Fatal("unselected symlink bypassed the strict verifier")
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if verifyIndexExecutable(resolved, digest) == nil {
		t.Fatal("ordinary replacement accepted")
	}
}
