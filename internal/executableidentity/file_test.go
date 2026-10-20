package executableidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestDescriptorDigestPreservesOffsetAndStrictPathVerifier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image")
	raw := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(path, raw, 0o500); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(7, 0); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	expected := "sha256:" + hex.EncodeToString(sum[:])
	if digest, err := DigestFile(file); err != nil || digest != expected || VerifyFile(file, expected) != nil {
		t.Fatal("whole descriptor identity", digest, err)
	}
	if offset, err := file.Seek(0, 1); err != nil || offset != 7 {
		t.Fatal("descriptor digest changed the borrower's offset", offset, err)
	}
	for _, pin := range []string{"", "sha256:bad", "sha256:" + string(make([]byte, 64)), "sha256:0000000000000000000000000000000000000000000000000000000000000000"} {
		if VerifyFile(file, pin) == nil {
			t.Fatal("missing/malformed/mismatched identity admitted", pin)
		}
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if Verify(link, expected) == nil {
		t.Fatal("descriptor support relaxed ordinary final-symlink refusal")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if VerifyFile(file, expected) != nil || Verify(path, expected) == nil {
		t.Fatal("descriptor identity confused with a live canonical path")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := DigestFile(file); err == nil {
		t.Fatal("closed descriptor accepted")
	}
}
