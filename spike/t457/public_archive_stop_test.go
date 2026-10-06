package t457

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451b"
)

func TestPublicArchiveSearchStaysStopped(t *testing.T) {
	if t451b.NativeProfile != "native-linux-arm64-rules-go-059-v2" || t451b.NativeProfileAmd64 != "native-linux-amd64-rules-go-059-v1" {
		t.Fatal("native profile identity changed")
	}
	if t451b.PublicArchiveDigest != "sha256:c9ecf680cd7bd0d88d8a6d1a0084a09c0a9dc45145fc28fbdcda888586d54bcc" {
		t.Fatal("frozen public archive digest changed")
	}
	worker, err := os.ReadFile("../t451a/launcher/native.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(worker, []byte(`runtime.GOARCH != "arm64" || os.Getuid() != 65534`)) || !bytes.Contains(worker, []byte("native compatibility driver requires admitted Linux arm64 worker")) {
		t.Fatal("arm64 worker gate changed")
	}
	if _, err = os.Stat("remote-apis-sdks.tar.gz"); err == nil {
		t.Fatal("public archive was committed")
	}
	raw, err := os.ReadFile("amd64_public_archive_stop_1.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Outcome   string `json:"outcome"`
		Bytes     int    `json:"archive_bytes"`
		Digest    string `json:"archive_sha256"`
		Found     bool   `json:"archive_found"`
		HostArch  string `json:"host_goarch"`
		HostUID   int    `json:"host_uid"`
		Cohort    string `json:"cohort_result"`
		Unchanged bool   `json:"arm64_profile_unchanged"`
		Next      string `json:"next_gate"`
		Rejected  struct {
			SHA256 string `json:"sha256"`
		} `json:"rejected_candidate"`
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "STOP" || receipt.Found || receipt.Bytes != 249496 || receipt.Digest != t451b.PublicArchiveDigest || receipt.HostArch != "amd64" || receipt.HostUID != 1000 || receipt.Cohort != "not_observed" || !receipt.Unchanged || receipt.Rejected.SHA256 == receipt.Digest || receipt.Rejected.SHA256 != "sha256:0712ceade13b0fb970245fec9885971ae0ca5759cc9770cdd002b07a2bfb418e" || receipt.Next == "" {
		t.Fatalf("archive stop receipt %+v", receipt)
	}
}
