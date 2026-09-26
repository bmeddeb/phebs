package t451a

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestReceiptEvidenceRoundTrip(t *testing.T) {
	// Formatting, member order, and literal HTML characters must survive the
	// outer receipt's JSON encoding; RawMessage would normalize these bytes.
	response := []byte(" {\"Packages\": [],\n\"Roots\": [\"<go>\"] }\n")
	original, err := json.Marshal(neutralEvidence{ResponseWire: response, ResponseSHA256: Digest(response), ResponseBytes: len(response)})
	if err != nil {
		t.Fatal(err)
	}
	original = append(original, '\n')
	receipt := Receipt{NeutralEvidence: original, OutputSHA256: Digest(original), OutputBytes: len(original)}
	for _, indent := range []string{"", "  "} {
		var wire bytes.Buffer
		encoder := json.NewEncoder(&wire)
		encoder.SetIndent("", indent)
		if err := encoder.Encode(receipt); err != nil {
			t.Fatal(err)
		}
		var decoded Receipt
		if err := json.Unmarshal(wire.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		reconstructed, err := evidenceWire(decoded.NeutralEvidence)
		if err != nil || !bytes.Equal(reconstructed, original) || Digest(reconstructed) != decoded.OutputSHA256 || len(reconstructed) != decoded.OutputBytes {
			t.Fatalf("receipt evidence changed: %v", err)
		}
		var evidence neutralEvidence
		if err := json.Unmarshal(decoded.NeutralEvidence, &evidence); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(evidence.ResponseWire, response) || Digest(evidence.ResponseWire) != evidence.ResponseSHA256 || len(evidence.ResponseWire) != evidence.ResponseBytes {
			t.Fatal("retained driver response bytes changed")
		}
	}
}
