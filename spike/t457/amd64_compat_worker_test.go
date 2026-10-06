package t457

import (
	"encoding/json"
	"os"
	"runtime"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451b"
)

func TestAmd64CompatWorkerStopsOnSandboxUID(t *testing.T) {
	if t451b.NativeProfile != "native-linux-arm64-rules-go-059-v2" {
		t.Fatal("arm64 profile changed")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("receipt host is linux/amd64")
	}
	if os.Getuid() == 65534 || launcher.NativeCompatWorkerAdmitted() {
		t.Fatal("this process matches the sandbox worker; do not claim a compatibility run from the receipt test")
	}
	if launcher.NativeCompatDriverDigest(runtime.GOARCH) != launcher.NativeDriverSHA256Amd64 {
		t.Fatal("x86 host did not select the amd64 driver pin")
	}
	raw, err := os.ReadFile("amd64_native_compat_arch_1.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Outcome   string `json:"outcome"`
		Arch      string `json:"admitted_arch"`
		HostArch  string `json:"host_goarch"`
		HostUID   int    `json:"host_uid"`
		Invoked   bool   `json:"worker_invoked"`
		Cohort    string `json:"cohort_result"`
		Next      string `json:"next_gate"`
		Arm64Pin  string `json:"arm64_driver_sha256"`
		Amd64Pin  string `json:"amd64_driver_sha256"`
		Unchanged bool   `json:"arm64_profile_unchanged"`
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "STOP" || receipt.Arch != "amd64" || receipt.HostArch != "amd64" || receipt.HostUID != 1000 || receipt.Invoked || receipt.Cohort != "not_observed" || !receipt.Unchanged || receipt.Next == "" || receipt.Arm64Pin != "sha256:"+launcher.NativeDriverSHA256 || receipt.Amd64Pin != "sha256:"+launcher.NativeDriverSHA256Amd64 {
		t.Fatalf("compat arch receipt %+v", receipt)
	}
}
