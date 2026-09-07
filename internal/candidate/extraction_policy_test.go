package candidate

import (
	"strings"
	"testing"
)

func TestExtractionPolicyDigest(t *testing.T) {
	legacy := "sha256:" + strings.Repeat("a", 64)
	ordinary, err := ExtractionPolicyDigest(legacy, false)
	if err != nil || ordinary != legacy {
		t.Fatalf("ordinary policy changed: %q, %v", ordinary, err)
	}
	selected, err := ExtractionPolicyDigest(legacy, true)
	if err != nil || !validDigest(selected) || selected == legacy {
		t.Fatalf("selected policy not separated: %q, %v", selected, err)
	}
	again, _ := ExtractionPolicyDigest(legacy, true)
	other, _ := ExtractionPolicyDigest("sha256:"+strings.Repeat("b", 64), true)
	if selected != again || selected == other || 3*AccountedEvidenceChunkFacts+3 != 510 {
		t.Fatal("selected policy is not deterministic/candidate-bound or exceeds admission")
	}
	for _, selected := range []bool{false, true} {
		if _, err := ExtractionPolicyDigest("invalid", selected); err == nil {
			t.Fatal("malformed candidate policy admitted")
		}
	}
}
