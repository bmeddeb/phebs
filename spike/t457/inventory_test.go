package t457

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestAmd64ToolInventoryIsAdmitted(t *testing.T) {
	raw, err := json.Marshal(ToolInventory())
	if err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile("amd64_tool_inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, retained) {
		t.Fatalf("inventory bytes differ: %s", raw)
	}
	src, err := os.ReadFile("../../internal/typedbazel/provider/files.go")
	if err != nil || !bytes.Contains(src, []byte(compilerDigestAmd64)) {
		t.Fatal("sysroot digest diverges from the provider seal", err)
	}
	sum := sha256.Sum256(retained)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	inv, err := typedindex.DecodeInventory(context.Background(), retained, digest)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Bytes() != 65821854+5210483+18104856+72352400+14939693 {
		t.Fatalf("inventory bytes %d", inv.Bytes())
	}
	want := map[string]string{
		"tools/bin/bazel":            provider.BazelDigestAmd64,
		"tools/bin/gopackagesdriver": "sha256:" + launcher.NativeDriverSHA256Amd64,
		"tools/bin/scip-go":          typedindex.SCIPGoIndexerDigestAmd64,
		"tools/cc-sysroot.zip":       compilerDigestAmd64,
		"tools/go/bin/go":            provider.GoDigestAmd64,
	}
	if len(inv.Files()) != len(want) {
		t.Fatalf("files %d", len(inv.Files()))
	}
	for _, f := range inv.Files() {
		if f.Digest != want[f.Path] {
			t.Errorf("%s digest %s", f.Path, f.Digest)
		}
		if (f.Path != "tools/cc-sysroot.zip") != f.Executable {
			t.Errorf("%s executable %v", f.Path, f.Executable)
		}
	}
}

// compilerDigestAmd64 is the sealed sysroot digest from the provider package.
// It is repeated here so the inventory test fails if the two seals diverge.
const compilerDigestAmd64 = "sha256:2f2ec79d40bb602c2c957ffa2f4be62ec2709a53e28060c57e5d5664a7f8dcde"
