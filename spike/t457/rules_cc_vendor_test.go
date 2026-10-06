package t457

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451b"
)

func TestRulesCCVendorStaysStopped(t *testing.T) {
	if t451b.NativeProfile != "native-linux-arm64-rules-go-059-v2" || t451b.NativeProfileAmd64 != "native-linux-amd64-rules-go-059-v1" {
		t.Fatal("native profile identity changed")
	}
	if launcher.NativeDownloaderConfig != "block bcr.bazel.build\n" {
		t.Fatal("cohort downloader block changed")
	}
	raw, err := os.ReadFile("amd64_rules_cc_vendor_1.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Outcome      string `json:"outcome"`
		RepoCacheB   int64  `json:"resolved_repo_cache_bytes"`
		ProtobufTree int64  `json:"protobuf_content_tree_bytes"`
		Protobuf27B  int64  `json:"protobuf_27_0_zip_bytes"`
		Modules      int    `json:"resolved_source_json_count"`
		CohortResult string `json:"cohort_result"`
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "STOP" || receipt.CohortResult != "not_observed" || receipt.Protobuf27B != 7991003 || receipt.Modules != 27 || receipt.RepoCacheB != 112257936 || receipt.ProtobufTree != 45188616 {
		t.Fatalf("vendor receipt %+v", receipt)
	}
	if _, err = os.Stat("vendor"); err == nil {
		t.Fatal("rules_cc vendor tree was committed")
	}
}
