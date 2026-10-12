package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/packrelease"
)

// TestSignReleaseDerivesBindingsAndSelfVerifies pins release-sign: every
// binding comes from present fact (template digests are discarded), the record
// is admitted by the startup loader under the signing key alone, and a missing
// artifact or an existing output refuses.
func TestSignReleaseDerivesBindingsAndSelfVerifies(t *testing.T) {
	tmp := t.TempDir()
	executable := writeReleaseExecutable(t, tmp, "phebs-under-test", "binary-bytes", 0o700)
	artifacts := filepath.Join(tmp, "artifacts")
	writeCensusArtifacts(t, artifacts, censusContents)
	facts := releaseFacts{executable: executable, goVersion: "go1.26.1", sourceCommit: strings.Repeat("a", 40)}

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(tmp, "key.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	template := packrelease.PackRelease{
		ReleaseSchemaVersion: packrelease.ReleaseSchemaVersion, ReleaseID: "rel-wire-1", ReleaseVersion: "1.0.0",
		PackID: wireCompatibilityPackID, PackClaimVersion: "1.0.0",
		Card:       packrelease.ArtifactRef{ArtifactID: "card.json", Digest: testReleaseDigest("forged")},
		Manifest:   packrelease.ArtifactRef{ArtifactID: "manifest.json"},
		Validation: packrelease.Validation{ArtifactID: "validation.json", Applies: true, ExpiresAt: "2999-12-31T23:59:59Z"},
		DerivedStatus: packrelease.StatusReleased, ApprovedAt: "2026-10-12T00:00:00Z", ApprovalRecords: []string{"approval-test"},
	}
	write := func(release packrelease.PackRelease) string {
		raw, err := json.Marshal(release)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(tmp, "template-"+release.ReleaseID+".json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	out := filepath.Join(tmp, "selection", "release.json")
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		t.Fatal(err)
	}
	public, err := signRelease(t.Context(), facts, write(template), keyPath, "key-test", artifacts, out)
	if err != nil {
		t.Fatalf("signRelease: %v", err)
	}
	if !public.Equal(private.Public()) {
		t.Fatal("returned public key does not match the signing key")
	}
	var signed packrelease.PackRelease
	raw, err := os.ReadFile(out)
	if err != nil || json.Unmarshal(raw, &signed) != nil {
		t.Fatalf("read signed record: %v", err)
	}
	if signed.Card.Digest != testReleaseDigest("card-content") || signed.Validation.Digest != testReleaseDigest("validation-content") ||
		signed.ReferencedArtifactsRootDigest != censusRootDigest() ||
		signed.Implementation.PhebsBinaryDigest != testReleaseDigest("binary-bytes") ||
		signed.Implementation.PackImplementationDigest != packImplementationDigest(packRecipes, releasedClaims) {
		t.Fatalf("bindings were not derived from present fact: %+v", signed)
	}

	if _, err := signRelease(t.Context(), facts, write(template), keyPath, "key-test", artifacts, out); err == nil {
		t.Fatal("an existing record was overwritten")
	}
	missing := template
	missing.ReleaseID, missing.Manifest.ArtifactID = "rel-wire-2", "absent.json"
	if _, err := signRelease(t.Context(), facts, write(missing), keyPath, "key-test", artifacts, filepath.Join(tmp, "other.json")); err == nil {
		t.Fatal("a record naming an absent artifact was signed")
	}
}
