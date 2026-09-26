package t451a

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestReceiptEvidenceRoundTrip(t *testing.T) {
	original := []byte("{\"plan\":{\"label\":\"@@//lib:lib\",\"text\":\"\\u003cgo\\u003e\"},\"reconciled\":true}\n")
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
	}
}
