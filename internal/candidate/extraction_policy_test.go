package candidate

import (
	"strings"
	"testing"
)

func TestExtractionPolicyDigest(t *testing.T) {
	legacy := "sha256:" + strings.Repeat("a", 64)
	// Pinned layer identities: the historical 169-fact layer must keep its
	// exact bytes, and each typed-scope composition must stay deterministic
	// and distinct at every boundary.
	const (
		chunkedLayer  = "sha256:0cb4049db49f428dfbcf98b841ea612e7c1962ef11ce8ce013684439fce2db3f"
		typedLayer    = "sha256:c5402045462ca98683f510b7bfe74119f36958b83de09fb5b54cb2327842aa1f"
		composedLayer = "sha256:b83303e5995a2c135015453b5b54b544875f46323274a4097f6f2924b77bc2b3"
	)
	ordinary, err := ExtractionPolicyDigest(legacy, false, false)
	if err != nil || ordinary != legacy {
		t.Fatalf("ordinary policy changed: %q, %v", ordinary, err)
	}
	selected, err := ExtractionPolicyDigest(legacy, true, false)
	if err != nil || selected != chunkedLayer {
		t.Fatalf("chunked layer identity changed: %q, %v", selected, err)
	}
	typed, err := ExtractionPolicyDigest(legacy, false, true)
	if err != nil || typed != typedLayer {
		t.Fatalf("typed-scope layer identity changed: %q, %v", typed, err)
	}
	both, err := ExtractionPolicyDigest(legacy, true, true)
	if err != nil || both != composedLayer {
		t.Fatalf("composed layer identity changed: %q, %v", both, err)
	}
	if len(map[string]struct{}{legacy: {}, selected: {}, typed: {}, both: {}}) != 4 {
		t.Fatal("policy layers are not mutually separated")
	}
	again, _ := ExtractionPolicyDigest(legacy, true, false)
	other, _ := ExtractionPolicyDigest("sha256:"+strings.Repeat("b", 64), true, true)
	if selected != again || other == both || 3*AccountedEvidenceChunkFacts+3 != 510 {
		t.Fatal("policy is not deterministic/candidate-bound or exceeds admission")
	}
	for _, mode := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		if _, err := ExtractionPolicyDigest("invalid", mode[0], mode[1]); err == nil {
			t.Fatalf("malformed candidate policy admitted under %v", mode)
		}
	}
}
