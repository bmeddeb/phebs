package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"reflect"
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

// selectionWithPath renders a release_selection block carrying one valid trust
// anchor beside the path under test, so each case isolates that field.
func selectionWithPath(t *testing.T, path string) string {
	t.Helper()
	return "release_selection:\n  path: " + path +
		"\n  keys:\n    - {id: key-1, public_key: '" + validPublicKey(t) + "'}\n"
}

// TestReleaseSelectionRejectsUnstablePath pins that the selection directory is a
// stable absolute path, so it never resolves against the process working
// directory, and that configuring one without a trust anchor is refused while
// parsing rather than only at startup, where every record would fail closed
// anyway but only after the directory had been read.
func TestReleaseSelectionRejectsUnstablePath(t *testing.T) {
	cases := map[string]string{
		"relative path":      selectionWithPath(t, "pack-releases"),
		"unclean path":       selectionWithPath(t, "/var/lib/phebs/../pack-releases"),
		"padded path":        selectionWithPath(t, `"/var/lib/phebs/pack-releases "`),
		"trailing separator": selectionWithPath(t, "/var/lib/phebs/pack-releases/"),
		// A configured directory is an operator intent to admit released packs,
		// so the key ring that makes any record verifiable must ship with it.
		"path without a key": "release_selection:\n  path: /var/lib/phebs/pack-releases\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(in))
			if err == nil || !strings.Contains(err.Error(), "release_selection: path") {
				t.Fatalf("Parse() err = %v, want a release_selection: path refusal", err)
			}
		})
	}
}

// TestReleaseSelectionRejectsMalformedRevocation pins the operator revocation
// list's grammar at parse time. Each entry must name something a signed
// record's release_id could carry, so a malformed entry is refused here rather
// than silently revoking nothing at startup, and a duplicate is refused rather
// than leaving the operator unsure which entry governs.
func TestReleaseSelectionRejectsMalformedRevocation(t *testing.T) {
	cases := map[string]string{
		"empty entry":           "release_selection:\n  revoked:\n    - ''\n",
		"whitespace entry":      "release_selection:\n  revoked:\n    - '   '\n",
		"entry outside grammar": "release_selection:\n  revoked:\n    - 'release signer'\n",
		"duplicate entry":       "release_selection:\n  revoked:\n    - rel-0001\n    - rel-0001\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(in))
			if err == nil || !strings.Contains(err.Error(), "release_selection.revoked") {
				t.Fatalf("Parse() err = %v, want a release_selection.revoked refusal", err)
			}
		})
	}
}

// TestReleaseSelectionAcceptsAStagedRevocation pins that the revocation list is
// inert while no selection directory is configured, so a withdrawal can be
// staged before the gate is switched on, and that a well-formed list beside a
// configured path and key validates and survives the round trip.
func TestReleaseSelectionAcceptsAStagedRevocation(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "staged without a path",
			in:   "release_selection:\n  revoked:\n    - rel-0001\n    - phebs.protobuf.contract/1.2.0\n",
			want: []string{"rel-0001", "phebs.protobuf.contract/1.2.0"},
		},
		{
			name: "configured beside a path and key",
			in: "release_selection:\n  path: /var/lib/phebs/pack-releases\n" +
				"  keys:\n    - {id: key-1, public_key: '" + validPublicKey(t) + "'}\n" +
				"  revoked:\n    - rel-0002\n",
			want: []string{"rel-0002"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := Parse([]byte(test.in))
			if err != nil {
				t.Fatalf("Parse() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(cfg.ReleaseSelection.Revoked, test.want) {
				t.Fatalf("Revoked = %#v, want %#v", cfg.ReleaseSelection.Revoked, test.want)
			}
		})
	}
}

// TestReleaseSelectionDocumentedExampleParses keeps the commented example in
// docs/config.example.yaml loadable once uncommented.
func TestReleaseSelectionDocumentedExampleParses(t *testing.T) {
	in := "release_selection:\n  path: /etc/phebs/pack-releases\n  keys:\n" +
		"    - id: release-signer\n      public_key: \"" + validPublicKey(t) + "\"\n" +
		"  revoked:\n    - \"phebs.protobuf.contract/1.2.0\"\n"
	if _, err := Parse([]byte(in)); err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
}
