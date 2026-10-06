package t457

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451b"
)

func TestPublicArchiveFoundAndWorkerStaysStopped(t *testing.T) {
	if t451b.NativeProfile != "native-linux-arm64-rules-go-059-v2" || t451b.NativeProfileAmd64 != "native-linux-amd64-rules-go-059-v1" {
		t.Fatal("native profile identity changed")
	}
	archive, err := os.ReadFile("../../tools/corpus/remote-apis-sdks.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if len(archive) != 249496 || t451a.Digest(archive) != t451b.PublicArchiveDigest {
		t.Fatalf("placed archive bytes %d digest %s", len(archive), t451a.Digest(archive))
	}
	foundRaw, err := os.ReadFile("amd64_public_archive_found_1.json")
	if err != nil {
		t.Fatal(err)
	}
	var found struct {
		Found         bool   `json:"archive_found"`
		Path          string `json:"archive_path"`
		Bytes         int    `json:"archive_bytes"`
		Digest        string `json:"archive_sha256"`
		Supplied      string `json:"supplied_as"`
		Reconstructed bool   `json:"reconstructed"`
		Cohort        string `json:"cohort_result"`
	}
	if err = json.Unmarshal(foundRaw, &found); err != nil {
		t.Fatal(err)
	}
	if !found.Found || found.Reconstructed || found.Path != "tools/corpus/remote-apis-sdks.tar.gz" || found.Bytes != 249496 || found.Digest != t451b.PublicArchiveDigest || found.Supplied != "external host attachment" || found.Cohort != "not_observed" {
		t.Fatalf("found receipt %+v", found)
	}
	stopRaw, err := os.ReadFile("amd64_native_compat_worker_stop_1.json")
	if err != nil {
		t.Fatal(err)
	}
	var stop struct {
		Outcome  string `json:"outcome"`
		Invoked  bool   `json:"worker_invoked"`
		HostArch string `json:"host_goarch"`
		HostUID  int    `json:"host_uid"`
		Cohort   string `json:"cohort_result"`
		Next     string `json:"next_gate"`
	}
	if err = json.Unmarshal(stopRaw, &stop); err != nil {
		t.Fatal(err)
	}
	if stop.Outcome != "STOP" || stop.Invoked || stop.HostArch != "amd64" || stop.HostUID != 1000 || stop.Cohort != "not_observed" || stop.Next == "" {
		t.Fatalf("worker stop receipt %+v", stop)
	}
}
