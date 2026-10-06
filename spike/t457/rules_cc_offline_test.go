package t457

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451b"
)

func TestAmd64OfflineCCPinIsSingular(t *testing.T) {
	if t451b.NativeProfile != "native-linux-arm64-rules-go-059-v2" {
		t.Fatal("arm64 profile changed")
	}
	if t451b.NativeProfileAmd64 != "native-linux-amd64-rules-go-059-v1" || t451b.Amd64RulesCCVersion != "0.2.14" || t451b.Amd64ProtobufVersion != "33.4" {
		t.Fatal("amd64 offline CC pin is ambiguous")
	}
	raw, err := os.ReadFile("amd64_rules_cc_offline_1.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Outcome   string `json:"outcome"`
		RulesCC   string `json:"rules_cc"`
		Protobuf  string `json:"protobuf"`
		Archive   string `json:"archive_sha256"`
		ArchiveB  int64  `json:"archive_bytes"`
		Cohort    string `json:"cohort_result"`
		NotSealed string `json:"not_sealed"`
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "PASS" || receipt.RulesCC != "0.2.14" || receipt.Protobuf != "33.4" || receipt.Cohort != "not_observed" || receipt.NotSealed != "rules_cc 0.1.1 + protobuf 27.0" || receipt.ArchiveB != 85483520 || receipt.Archive != "sha256:97e24d24608a993d6f57a8c11f6e4e8c26f8431cd5cd303373412fd476e31161" {
		t.Fatalf("offline receipt %+v", receipt)
	}
}
