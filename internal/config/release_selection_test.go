package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

func validPublicKey(t *testing.T) string {
	t.Helper()
	public := ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))
	for i := range public {
		public[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(public)
}

func TestReleaseSelectionValidKeysPass(t *testing.T) {
	in := "release_selection:\n  path: /var/lib/phebs/releases\n  keys:\n    - {id: key-1, public_key: '" + validPublicKey(t) + "'}\n"
	if _, err := Parse([]byte(in)); err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
}

func TestReleaseSelectionEmptyIsAdmitted(t *testing.T) {
	// The dark default: no release_selection block at all validates clean.
	if _, err := Parse([]byte("server:\n  data_dir: ./data\n")); err != nil {
		t.Fatalf("Parse() unexpected error on empty release selection: %v", err)
	}
}

func TestReleaseSelectionRejectsMalformedKey(t *testing.T) {
	cases := map[string]string{
		"missing id":             "release_selection:\n  keys:\n    - {id: '', public_key: '" + validPublicKey(t) + "'}\n",
		"bad base64":             "release_selection:\n  keys:\n    - {id: key-1, public_key: 'not-base64!'}\n",
		"id outside key grammar": "release_selection:\n  keys:\n    - {id: 'release signer', public_key: '" + validPublicKey(t) + "'}\n",
		"wrong key size":         "release_selection:\n  keys:\n    - {id: key-1, public_key: '" + base64.StdEncoding.EncodeToString([]byte("short")) + "'}\n",
		"duplicate id": "release_selection:\n  keys:\n    - {id: key-1, public_key: '" + validPublicKey(t) + "'}\n" +
			"    - {id: key-1, public_key: '" + validPublicKey(t) + "'}\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(in))
			if err == nil || !strings.Contains(err.Error(), "release_selection.keys") {
				t.Fatalf("Parse() err = %v, want a release_selection.keys refusal", err)
			}
		})
	}
}

// TestReleaseSelectionDocumentedExampleParses keeps the commented example in
// docs/config.example.yaml loadable once uncommented.
func TestReleaseSelectionDocumentedExampleParses(t *testing.T) {
	in := "release_selection:\n  path: /etc/phebs/pack-releases\n  keys:\n" +
		"    - id: release-signer\n      public_key: \"" + validPublicKey(t) + "\"\n"
	if _, err := Parse([]byte(in)); err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
}
