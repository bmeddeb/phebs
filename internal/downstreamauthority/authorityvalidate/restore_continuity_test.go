package authorityvalidate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestWithoutRunProvenanceIsExactAndDetached(t *testing.T) {
	value := sealedTestAuthority(Schema)
	raw, _ := json.Marshal(value)
	want := value
	want.ProvenanceDigest = ""
	want.Domains = append([]DomainAuthority(nil), value.Domains...)
	want.Domains[0].RunID = ""
	wantRaw, _ := json.Marshal(want)
	got, err := WithoutRunProvenance(raw)
	if err != nil || !bytes.Equal(got, wantRaw) {
		t.Fatalf("projection = %s, %v", got, err)
	}
	value.Domains[0].RunID = "restored-run"
	value.ProvenanceDigest, value.Digest = "", ""
	provenance, _ := json.Marshal(value)
	sum := sha256.Sum256(append([]byte(Schema+"-provenance\x00"), provenance...))
	value.ProvenanceDigest, value.Digest = "sha256:"+hex.EncodeToString(sum[:]), want.Digest
	changed, _ := json.Marshal(value)
	restored, err := WithoutRunProvenance(changed)
	if err != nil || !bytes.Equal(got, restored) {
		t.Fatalf("provenance-only rebuild differs: %s, %v", restored, err)
	}
	got[0] = '!'
	if raw[0] != '{' || changed[0] != '{' {
		t.Fatal("projection aliases input")
	}
}

func TestWithoutRunProvenanceRejectsUnvalidatedOrHistoricalInputs(t *testing.T) {
	for _, name := range []string{"historical", "unknown", "provenance", "semantic", "noncanonical", "missing", "oversized"} {
		t.Run(name, func(t *testing.T) {
			value := sealedTestAuthority(Schema)
			switch name {
			case "historical":
				value = sealedTestAuthority(SchemaV1)
			case "unknown":
				value.Schema += "-future"
			case "provenance":
				value.Domains[0].RunID = "different"
			case "semantic":
				value.Domains[0].RootDigest = testDigest("0")
			}
			raw, _ := json.Marshal(value)
			switch name {
			case "noncanonical":
				raw = append(raw, '\n')
			case "missing":
				raw = nil
			case "oversized":
				raw = make([]byte, MaxCanonicalBytes+1)
			}
			if _, err := WithoutRunProvenance(raw); err == nil {
				t.Fatal("invalid authority accepted")
			}
		})
	}
}
