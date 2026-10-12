package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestCanonicalProofIdentityAndBoundary(t *testing.T) {
	input := `{"schema_version":"proof-bundle-v1","coverage":{"schema_version":"coverage-certificate-v3","domains":[],"repository_count":0,"repositories":[],"digest":""}}`
	var output bytes.Buffer
	if err := canonicalize(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	var envelope api.ProofBundleEnvelope
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(envelope.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.ID != store.ComputeProofBundleID(string(canonical)) {
		t.Fatal("native identity mismatch")
	}
	for _, bad := range []string{input + input, strings.Replace(input, "proof-bundle-v1", "invented-v9", 1), strings.Replace(input, `"schema_version":"proof-bundle-v1"`, `"extra":true,"schema_version":"proof-bundle-v1"`, 1)} {
		if err := canonicalize(strings.NewReader(bad), &bytes.Buffer{}); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
