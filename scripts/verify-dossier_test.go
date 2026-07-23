package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dossier"
)

func TestOfflineDossierVerifierNeedsNoPhebsInstance(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed([]byte(
		"0123456789abcdef0123456789abcdef",
	))
	entries := make([]dossier.Entry, 4)
	var err error
	for i, name := range []string{
		"investigation",
		"revision",
		"run-artifact",
		"consumer-snapshot",
	} {
		entries[i], err = dossier.NewEntry(
			"objects/"+name+".json",
			"application/json",
			map[string]string{"id": name},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	visibleDigest, err := dossier.VisibleUnitSetDigest([]string{"repo:a"})
	if err != nil {
		t.Fatal(err)
	}
	snapshotManifest, err := dossier.DigestValue(
		"fixture-snapshot-manifest-v1",
		"snapshot",
	)
	if err != nil {
		t.Fatal(err)
	}
	inputManifest, err := dossier.DigestValue(
		"fixture-input-manifest-v1",
		"input",
	)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := dossier.Seal(dossier.Manifest{
		DossierID: "01OFFLINEVERIFY",
		Question:  "offline verification",
		Investigation: dossier.ObjectReference{
			Kind: "investigation", ID: "investigation",
			ContentDigest: entries[0].Digest, EntryPath: entries[0].Path,
		},
		Revision: dossier.ObjectReference{
			Kind: "revision", ID: "revision",
			ContentDigest: entries[1].Digest, EntryPath: entries[1].Path,
		},
		PrimaryArtifact: dossier.ObjectReference{
			Kind: "run_artifact", ID: "run-artifact",
			ContentDigest: entries[2].Digest, EntryPath: entries[2].Path,
		},
		ConsumerSnapshot: dossier.ObjectReference{
			Kind: "consumer_snapshot", ID: "consumer-snapshot",
			ContentDigest: entries[3].Digest, EntryPath: entries[3].Path,
		},
		SnapshotManifests:  []string{snapshotManifest},
		InputManifests:     []string{inputManifest},
		PackCardIdentities: []string{"pack:test"},
		Evidence:           []dossier.EvidenceReference{},
		Authorization: dossier.AuthorizationScope{
			Principal: "user:owner", VisibilityProjectionID: "visibility:1",
			VisibleUnitIDs: []string{"repo:a"}, VisibleUnitSetDigest: visibleDigest,
			RedactionScope:         "recipient-current-visible-universe",
			HandlingClassification: "internal",
		},
		Validity: dossier.Validity{
			EligibilityResult: "ineligible", SupportedClaims: []string{"test"},
			Blockers: []string{}, FreshnessState: "current",
			ValidationState: "test", ReviewDueRule: "on-change",
			Statement: dossier.ValidityStatement(),
		},
		IssuedAt: "2026-07-23T12:00:00Z",
	}, entries, "offline-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	content, err := dossier.CanonicalJSON(sealed)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "dossier.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{
		"-trusted-key-id", "offline-key",
		"-trusted-public-key", base64.StdEncoding.EncodeToString(
			privateKey.Public().(ed25519.PublicKey),
		),
		path,
	}, &stdout, &stderr)
	if exitCode != 0 || !strings.Contains(stdout.String(), `"status":"verified"`) ||
		!strings.Contains(stdout.String(), dossier.ValidityStatement()) {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}

	tampered := bytes.Replace(content, []byte("offline verification"), []byte("offline tampering"), 1)
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if exitCode := run([]string{path}, &stdout, &stderr); exitCode != 1 ||
		!strings.Contains(stderr.String(), "verification failed") {
		t.Fatalf("tampered exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}
