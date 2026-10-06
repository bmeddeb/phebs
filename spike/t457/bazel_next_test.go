package t457

import (
	"bytes"
	"os"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451b"
)

func TestAmd64BazelNextGateStaysStopped(t *testing.T) {
	if t451b.NativeProfile != "native-linux-arm64-rules-go-059-v2" || t451b.NativeProfileAmd64 != "native-linux-amd64-rules-go-059-v1" {
		t.Fatal("native profile identity changed")
	}
	if launcher.NativeDownloaderConfig != "block bcr.bazel.build\n" {
		t.Fatal("cohort downloader block changed")
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
}
