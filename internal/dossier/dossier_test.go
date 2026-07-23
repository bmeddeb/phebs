package dossier

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"
)

func sealedFixture(t *testing.T) (*Sealed, ed25519.PublicKey) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed([]byte(
		"0123456789abcdef0123456789abcdef",
	))
	investigation, err := NewEntry(
		"objects/investigation.json",
		"application/json",
		map[string]any{"investigation_id": "01SYNTHETIC"},
	)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := NewEntry(
		"objects/revision.json",
		"application/json",
		map[string]any{"revision_id": "ivr_fixture"},
	)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := NewEntry(
		"objects/artifact.json",
		"application/json",
		map[string]any{"artifact_id": "ira_fixture"},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewEntry(
		"objects/consumer-snapshot.json",
		"application/json",
		map[string]any{"snapshot_id": "ics_fixture"},
	)
	if err != nil {
		t.Fatal(err)
	}
	finding, err := NewEntry(
		"evidence/fact-one.json",
		"application/json",
		map[string]any{"fact_id": "fact:one"},
	)
	if err != nil {
		t.Fatal(err)
	}
	visibleDigest, err := VisibleUnitSetDigest([]string{"repo:a"})
	if err != nil {
		t.Fatal(err)
	}
	snapshotManifest, err := DigestValue(
		"fixture-snapshot-manifest-v1",
		"snapshot",
	)
	if err != nil {
		t.Fatal(err)
	}
	inputManifest, err := DigestValue("fixture-input-manifest-v1", "input")
	if err != nil {
		t.Fatal(err)
	}
	materialDigest, err := DigestValue("fixture-fact-reference-v1", "fact:one")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Seal(Manifest{
		DossierID: "01SYNTHETICDOSSIER",
		Question:  "Who consumes /shop.Cart/Get?",
		Investigation: ObjectReference{
			Kind: "investigation", ID: "01SYNTHETIC",
			ContentDigest: investigation.Digest, EntryPath: investigation.Path,
		},
		Revision: ObjectReference{
			Kind: "revision", ID: "ivr_fixture",
			ContentDigest: revision.Digest, EntryPath: revision.Path,
		},
		PrimaryArtifact: ObjectReference{
			Kind: "run_artifact", ID: "ira_fixture",
			ContentDigest: artifact.Digest, EntryPath: artifact.Path,
		},
		ConsumerSnapshot: ObjectReference{
			Kind: "consumer_snapshot", ID: "ics_fixture",
			ContentDigest: snapshot.Digest, EntryPath: snapshot.Path,
		},
		SnapshotManifests:  []string{snapshotManifest},
		InputManifests:     []string{inputManifest},
		PackCardIdentities: []string{"pack:grpc@1"},
		Evidence: []EvidenceReference{
			{
				FactID: "fact:one", FindingDigest: finding.Digest,
				FindingEntryPath: finding.Path, Mode: "authorized_locator",
				MaterialDigest:    materialDigest,
				AuthorizedLocator: "phebs://investigation/01SYNTHETIC/fact/fact:one",
			},
		},
		Authorization: AuthorizationScope{
			Principal: "user:owner", VisibilityProjectionID: "visibility:owner:1",
			VisibleUnitIDs: []string{"repo:a"}, VisibleUnitSetDigest: visibleDigest,
			RedactionScope:         "recipient-current-visible-universe",
			HandlingClassification: "internal",
		},
		Validity: Validity{
			EligibilityResult: "eligible:false:blocker",
			SupportedClaims:   []string{"consumer-census"}, Blockers: []string{"OWNER_UNKNOWN"},
			FreshnessState: "current", ValidationState: "released",
			ReviewDueRule: "on-material-change", Statement: ValidityStatement(),
		},
		IssuedAt: "2026-07-23T12:00:00Z",
	}, []Entry{snapshot, artifact, revision, investigation, finding}, "test-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return sealed, privateKey.Public().(ed25519.PublicKey)
}

func TestFindingReferenceBindsFactIdentity(t *testing.T) {
	sealed, _ := sealedFixture(t)
	sealed.Manifest.Evidence[0].FactID = "fact:other"
	if err := validateEntryReferences(sealed.Manifest, sealed.Entries); err == nil {
		t.Fatal("finding with a different fact identity unexpectedly accepted")
	}
}

func TestSealAndVerifyDeterministicDigestChain(t *testing.T) {
	first, publicKey := sealedFixture(t)
	second, _ := sealedFixture(t)
	firstBytes, err := CanonicalJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := CanonicalJSON(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatal("equal dossier inputs produced different canonical bytes")
	}
	verification, err := Verify(*first, VerifyOptions{
		TrustedKeyID: "test-key", TrustedPublic: publicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verification.RootDigest != first.RootDigest ||
		verification.EntryCount != len(first.Entries) {
		t.Fatalf("verification = %+v", verification)
	}
	decoded, err := Decode(firstBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(*decoded, VerifyOptions{
		TrustedKeyID: "test-key", TrustedPublic: publicKey,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsEveryTamperedLayer(t *testing.T) {
	sealed, publicKey := sealedFixture(t)
	tests := []struct {
		name   string
		mutate func(*Sealed)
	}{
		{
			name: "entry content",
			mutate: func(value *Sealed) {
				value.Entries[0].Content = json.RawMessage(`{"changed":true}`)
			},
		},
		{
			name: "entry digest",
			mutate: func(value *Sealed) {
				value.Entries[0].Digest = "sha256:changed"
			},
		},
		{
			name: "manifest",
			mutate: func(value *Sealed) {
				value.Manifest.Question = "changed"
			},
		},
		{
			name: "manifest digest",
			mutate: func(value *Sealed) {
				value.ManifestDigest = "sha256:changed"
			},
		},
		{
			name: "root",
			mutate: func(value *Sealed) {
				value.RootDigest = "sha256:changed"
			},
		},
		{
			name: "signature",
			mutate: func(value *Sealed) {
				value.Signature.Value = value.Signature.Value[:len(value.Signature.Value)-2] + "AA"
			},
		},
		{
			name: "key identity",
			mutate: func(value *Sealed) {
				value.Signature.KeyID = "other"
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			content, err := CanonicalJSON(sealed)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := Decode(content)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(candidate)
			if _, err := Verify(*candidate, VerifyOptions{
				TrustedKeyID: "test-key", TrustedPublic: publicKey,
			}); err == nil {
				t.Fatal("tampered dossier unexpectedly verified")
			}
		})
	}
}
