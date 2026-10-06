package t457

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451b"
)

func TestAmd64CompatUIDAttemptStaysStopped(t *testing.T) {
	if t451b.NativeProfile != "native-linux-arm64-rules-go-059-v2" {
		t.Fatal("arm64 profile changed")
	}
	if launcher.NativeDriverSHA256 != "f49a0ff4339e32cc699c6fbb5a80b9d8f936b3bfe6b08a924fa19495e35b2bfd" || launcher.NativeDriverSHA256Amd64 != "2b58a9c9a294fc8d9c899bd66f881f7236ed4422a998a4cebab07662ec373bb8" {
		t.Fatal("driver pins changed")
	}
	raw, err := os.ReadFile("amd64_native_compat_uid_1.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Outcome   string `json:"outcome"`
		Invoked   bool   `json:"worker_invoked"`
		UID       int    `json:"host_uid"`
		Arch      string `json:"host_goarch"`
		Driver    string `json:"amd64_driver_sha256"`
		Error     string `json:"error"`
		Child     bool   `json:"driver_child_started"`
		Cohort    string `json:"cohort_result"`
		Unchanged bool   `json:"arm64_profile_unchanged"`
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "STOP" || !receipt.Invoked || receipt.UID != 65534 || receipt.Arch != "amd64" || receipt.Driver != "sha256:"+launcher.NativeDriverSHA256Amd64 || receipt.Error != "open /scratch/workspace/lib/lib.go: no such file or directory" || receipt.Child || receipt.Cohort != "not_observed" || !receipt.Unchanged {
		t.Fatalf("uid attempt receipt %+v", receipt)
	}
}
