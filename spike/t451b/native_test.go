package t451b

import (
	"context"
	"encoding/json"
	"flag"
	"testing"
)

var nativeConfig = flag.String("t451b-native-config", "", "explicit canonical config for the closed offline neutral compatibility gate")

func TestNativeCompatibility(t *testing.T) {
	if *nativeConfig == "" {
		t.Skip("native compatibility requires an explicit pinned config; never builds or discovers tools")
	}
	config, err := ReadConfig(*nativeConfig)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := RunConfig(context.Background(), config)
	payload := receipt.NeutralEvidence
	receipt.NeutralEvidence = nil
	encoded, _ := json.Marshal(receipt)
	t.Log(string(encoded))
	if err != nil || receipt.Outcome != "NEUTRAL_COMPATIBILITY_OBSERVED" || !receipt.Removed || !receipt.InputsRemoved || !receipt.Resources.LimitsVerified {
		t.Fatalf("neutral compatibility not established: %v", err)
	}
	if _, err = DecodeEvidence(payload); err != nil {
		t.Fatal(err)
	}
}
