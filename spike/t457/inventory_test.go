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
	assertMED505Supersession(t)
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
		"tools/go/bin/go":            goDigestAmd64,
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

// compilerDigestAmd64 and goDigestAmd64 are the sysroot and Go 1.25.0 SDK
// sealed when this inventory was measured. MED-505 superseded both in the
// provider; the retained inventory keeps its original bytes.
const (
	compilerDigestAmd64 = "sha256:2f2ec79d40bb602c2c957ffa2f4be62ec2709a53e28060c57e5d5664a7f8dcde"
	goDigestAmd64       = "sha256:b93cdfdbc72f1afc3f21498c80bf3d155a44a9b95e2d690c940511051574bc25"
)

// assertMED505Supersession requires the provider to seal exactly the sysroot
// and Go SDK recorded by the retained MED-505 admission, not this inventory's.
func assertMED505Supersession(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile("med505_amd64_admission_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var admission struct {
		GoSDK struct {
			GoSHA256 string `json:"go_sha256"`
		} `json:"go_sdk"`
		Sysroot struct {
			SHA256 string `json:"sha256"`
		} `json:"sysroot"`
	}
	if err := json.Unmarshal(raw, &admission); err != nil {
		t.Fatal(err)
	}
	sysroot, sdk := "sha256:"+admission.Sysroot.SHA256, "sha256:"+admission.GoSDK.GoSHA256
	src, err := os.ReadFile("../../internal/typedbazel/provider/files.go")
	if err != nil || !bytes.Contains(src, []byte(sysroot)) || sysroot == compilerDigestAmd64 {
		t.Fatal("sysroot digest diverges from the MED-505 admission", err)
	}
	if provider.GoDigestAmd64 != sdk || sdk == goDigestAmd64 {
		t.Fatal("Go SDK digest diverges from the MED-505 admission")
	}
}
