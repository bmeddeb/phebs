// Package dossier defines the self-contained, offline-verifiable
// Investigation dossier format. Integrity and signature verification do not
// imply current authorization, freshness, or continuing policy validity.
package dossier

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

const (
	FormatVersion  = "phebs-investigation-dossier-v1"
	HashAlgorithm  = "sha256"
	MaxSealedBytes = 64 << 20

	validityStatement = "Offline verification proves this dossier's byte integrity and signer only; it does not prove current authorization, freshness, evidence availability, or continuing validity."
)

type ObjectReference struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	ContentDigest string `json:"content_digest"`
	EntryPath     string `json:"entry_path"`
}

type EvidenceReference struct {
	FactID            string `json:"fact_id"`
	FindingDigest     string `json:"finding_digest"`
	FindingEntryPath  string `json:"finding_entry_path"`
	Mode              string `json:"mode"`
	MaterialDigest    string `json:"material_digest"`
	AuthorizedLocator string `json:"authorized_locator,omitempty"`
	EntryPath         string `json:"entry_path,omitempty"`
}

type AuthorizationScope struct {
	Principal              string   `json:"principal"`
	VisibilityProjectionID string   `json:"visibility_projection_id"`
	VisibleUnitIDs         []string `json:"visible_unit_ids"`
	VisibleUnitSetDigest   string   `json:"visible_unit_set_digest"`
	RedactionScope         string   `json:"redaction_scope"`
	HandlingClassification string   `json:"handling_classification"`
}

type Validity struct {
	EligibilityResult string   `json:"eligibility_result"`
	SupportedClaims   []string `json:"supported_claims"`
	Blockers          []string `json:"blockers"`
	FreshnessState    string   `json:"freshness_state"`
	ValidationState   string   `json:"validation_state"`
	ReviewDueRule     string   `json:"review_due_rule"`
	ExpiresAt         string   `json:"expires_at,omitempty"`
	Statement         string   `json:"statement"`
}

type Manifest struct {
	FormatVersion      string              `json:"format_version"`
	DossierID          string              `json:"dossier_id"`
	Question           string              `json:"question"`
	Investigation      ObjectReference     `json:"investigation"`
	Revision           ObjectReference     `json:"revision"`
	PrimaryArtifact    ObjectReference     `json:"primary_artifact"`
	ConsumerSnapshot   ObjectReference     `json:"consumer_snapshot"`
	ChangeBrief        *ObjectReference    `json:"change_brief,omitempty"`
	Baseline           *ObjectReference    `json:"baseline,omitempty"`
	Decision           *ObjectReference    `json:"decision,omitempty"`
	SnapshotManifests  []string            `json:"snapshot_manifests"`
	InputManifests     []string            `json:"input_manifests"`
	PackCardIdentities []string            `json:"pack_card_identities"`
	Evidence           []EvidenceReference `json:"evidence"`
	Authorization      AuthorizationScope  `json:"authorization"`
	Validity           Validity            `json:"validity"`
	IssuedAt           string              `json:"issued_at"`
	PredecessorID      string              `json:"predecessor_id,omitempty"`
	VerificationKeyID  string              `json:"verification_key_id"`
}

type Entry struct {
	Path      string          `json:"path"`
	MediaType string          `json:"media_type"`
	Content   json.RawMessage `json:"content"`
	Digest    string          `json:"digest"`
}

type Signature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
	Value     string `json:"value"`
}

type Sealed struct {
	FormatVersion  string    `json:"format_version"`
	HashAlgorithm  string    `json:"hash_algorithm"`
	DossierID      string    `json:"dossier_id"`
	Manifest       Manifest  `json:"manifest"`
	ManifestDigest string    `json:"manifest_digest"`
	Entries        []Entry   `json:"entries"`
	RootDigest     string    `json:"root_digest"`
	Signature      Signature `json:"signature"`
}

type VerifyOptions struct {
	TrustedKeyID  string
	TrustedPublic ed25519.PublicKey
}

type Verification struct {
	DossierID       string
	RootDigest      string
	KeyID           string
	PublicKeySHA256 string
	EntryCount      int
}

func ValidityStatement() string {
	return validityStatement
}

func CanonicalJSON(value any) ([]byte, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}
	return content, nil
}

func digestDomain(domain string, fields ...[]byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	for _, field := range fields {
		_, _ = fmt.Fprintf(hash, "%d:", len(field))
		_, _ = hash.Write(field)
		_, _ = hash.Write([]byte{';'})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func DigestValue(domain string, value any) (string, error) {
	content, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	return digestDomain(domain, content), nil
}

func visibleUnitSetDigest(values []string) (string, error) {
	content, err := CanonicalJSON(values)
	if err != nil {
		return "", err
	}
	return digestDomain("phebs-dossier-visible-units-v1", content), nil
}

func normalizeStrings(name string, values []string) ([]string, error) {
	values = slices.Clone(values)
	for _, value := range values {
		if strings.TrimSpace(value) == "" || len(value) > 1<<16 {
			return nil, fmt.Errorf("%s contains an invalid value", name)
		}
	}
	slices.Sort(values)
	values = slices.Compact(values)
	if values == nil {
		return []string{}, nil
	}
	return values, nil
}

func validSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 ||
		!strings.HasPrefix(value, "sha256:") {
		return false
	}
	encoded := strings.TrimPrefix(value, "sha256:")
	if encoded != strings.ToLower(encoded) {
		return false
	}
	decoded, err := hex.DecodeString(encoded)
	return err == nil && len(decoded) == sha256.Size
}

func normalizeManifest(manifest Manifest) (Manifest, error) {
	if manifest.FormatVersion == "" {
		manifest.FormatVersion = FormatVersion
	}
	if manifest.FormatVersion != FormatVersion || manifest.DossierID == "" ||
		manifest.Question == "" || manifest.IssuedAt == "" ||
		manifest.VerificationKeyID == "" {
		return Manifest{}, errors.New("dossier manifest identity is incomplete")
	}
	issuedAt, parseErr := time.Parse(time.RFC3339, manifest.IssuedAt)
	if parseErr != nil {
		return Manifest{}, errors.New("dossier issued_at is not RFC3339")
	}
	for name, reference := range map[string]ObjectReference{
		"investigation":     manifest.Investigation,
		"revision":          manifest.Revision,
		"primary artifact":  manifest.PrimaryArtifact,
		"consumer snapshot": manifest.ConsumerSnapshot,
	} {
		if reference.Kind == "" || reference.ID == "" ||
			reference.ContentDigest == "" || reference.EntryPath == "" {
			return Manifest{}, fmt.Errorf("%s reference is incomplete", name)
		}
	}
	for name, reference := range map[string]*ObjectReference{
		"change brief": manifest.ChangeBrief,
		"baseline":     manifest.Baseline,
		"decision":     manifest.Decision,
	} {
		if reference != nil && (reference.Kind == "" || reference.ID == "" ||
			reference.ContentDigest == "" || reference.EntryPath == "") {
			return Manifest{}, fmt.Errorf("%s reference is incomplete", name)
		}
	}
	var err error
	manifest.SnapshotManifests, err = normalizeStrings(
		"snapshot manifests",
		manifest.SnapshotManifests,
	)
	if err != nil {
		return Manifest{}, err
	}
	manifest.InputManifests, err = normalizeStrings("input manifests", manifest.InputManifests)
	if err != nil {
		return Manifest{}, err
	}
	if len(manifest.SnapshotManifests) == 0 || len(manifest.InputManifests) == 0 {
		return Manifest{}, errors.New(
			"dossier snapshot and input manifests are required",
		)
	}
	for _, digest := range append(
		slices.Clone(manifest.SnapshotManifests),
		manifest.InputManifests...,
	) {
		if !validSHA256Digest(digest) {
			return Manifest{}, errors.New(
				"dossier snapshot or input manifest digest is invalid",
			)
		}
	}
	manifest.PackCardIdentities, err = normalizeStrings(
		"pack-card identities",
		manifest.PackCardIdentities,
	)
	if err != nil {
		return Manifest{}, err
	}
	if len(manifest.PackCardIdentities) == 0 {
		return Manifest{}, errors.New("dossier pack-card identity is required")
	}
	manifest.Authorization.VisibleUnitIDs, err = normalizeStrings(
		"visible unit ids",
		manifest.Authorization.VisibleUnitIDs,
	)
	if err != nil {
		return Manifest{}, err
	}
	wantVisibleDigest, err := visibleUnitSetDigest(manifest.Authorization.VisibleUnitIDs)
	if err != nil {
		return Manifest{}, err
	}
	if len(manifest.Validity.SupportedClaims) == 0 {
		return Manifest{}, errors.New("dossier supported claim is required")
	}
	if manifest.Authorization.Principal == "" ||
		manifest.Authorization.VisibilityProjectionID == "" ||
		manifest.Authorization.RedactionScope == "" ||
		manifest.Authorization.HandlingClassification == "" ||
		manifest.Authorization.VisibleUnitSetDigest != wantVisibleDigest {
		return Manifest{}, errors.New("dossier authorization scope is incomplete")
	}
	manifest.Validity.SupportedClaims, err = normalizeStrings(
		"supported claims",
		manifest.Validity.SupportedClaims,
	)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Validity.Blockers, err = normalizeStrings("blockers", manifest.Validity.Blockers)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Validity.EligibilityResult == "" ||
		manifest.Validity.FreshnessState == "" ||
		manifest.Validity.ValidationState == "" ||
		manifest.Validity.ReviewDueRule == "" ||
		manifest.Validity.Statement != validityStatement {
		return Manifest{}, errors.New("dossier validity statement is incomplete")
	}
	if manifest.Validity.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, manifest.Validity.ExpiresAt)
		if err != nil || !expiresAt.After(issuedAt) {
			return Manifest{}, errors.New(
				"dossier expires_at must be RFC3339 and after issued_at",
			)
		}
	}
	if manifest.Evidence == nil {
		manifest.Evidence = []EvidenceReference{}
	}
	manifest.Evidence = slices.Clone(manifest.Evidence)
	slices.SortFunc(manifest.Evidence, func(left, right EvidenceReference) int {
		return strings.Compare(left.FactID, right.FactID)
	})
	for i, evidence := range manifest.Evidence {
		if evidence.FactID == "" || evidence.FindingDigest == "" ||
			evidence.FindingEntryPath == "" || evidence.MaterialDigest == "" ||
			(evidence.Mode != "embedded" && evidence.Mode != "authorized_locator") {
			return Manifest{}, errors.New("dossier evidence reference is incomplete")
		}
		if !validSHA256Digest(evidence.MaterialDigest) {
			return Manifest{}, errors.New("dossier evidence material digest is invalid")
		}
		if evidence.Mode == "embedded" &&
			(evidence.EntryPath == "" || evidence.AuthorizedLocator != "") {
			return Manifest{}, errors.New("embedded evidence reference is inconsistent")
		}
		if evidence.Mode == "authorized_locator" &&
			(evidence.AuthorizedLocator == "" || evidence.EntryPath != "") {
			return Manifest{}, errors.New("locator evidence reference is inconsistent")
		}
		if i > 0 && manifest.Evidence[i-1].FactID == evidence.FactID {
			return Manifest{}, errors.New("duplicate dossier evidence fact")
		}
	}
	return manifest, nil
}

func normalizeEntries(entries []Entry) ([]Entry, error) {
	if len(entries) == 0 || len(entries) > 20_000 {
		return nil, errors.New("dossier entries must contain between 1 and 20000 rows")
	}
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(left, right Entry) int {
		return strings.Compare(left.Path, right.Path)
	})
	for i := range entries {
		if entries[i].Path == "" || strings.HasPrefix(entries[i].Path, "/") ||
			strings.Contains(entries[i].Path, "..") ||
			entries[i].MediaType == "" ||
			len(entries[i].Content) == 0 {
			return nil, errors.New("dossier entry is invalid")
		}
		var canonical any
		decoder := json.NewDecoder(bytes.NewReader(entries[i].Content))
		decoder.UseNumber()
		if err := decoder.Decode(&canonical); err != nil {
			return nil, fmt.Errorf("decode dossier entry %q: %w", entries[i].Path, err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("dossier entry %q has trailing JSON", entries[i].Path)
		}
		content, err := CanonicalJSON(canonical)
		if err != nil {
			return nil, err
		}
		entries[i].Content = content
		wantDigest := digestDomain(
			"phebs-dossier-entry-v1",
			[]byte(entries[i].Path),
			content,
		)
		if entries[i].Digest != "" && entries[i].Digest != wantDigest {
			return nil, fmt.Errorf("dossier entry %q digest mismatch", entries[i].Path)
		}
		entries[i].Digest = wantDigest
		if i > 0 && entries[i-1].Path == entries[i].Path {
			return nil, errors.New("duplicate dossier entry path")
		}
	}
	return entries, nil
}

func rootDigest(manifestDigest string, entries []Entry) (string, error) {
	type digestRow struct {
		Path   string `json:"path"`
		Digest string `json:"digest"`
	}
	rows := make([]digestRow, len(entries))
	for i, entry := range entries {
		rows[i] = digestRow{Path: entry.Path, Digest: entry.Digest}
	}
	content, err := CanonicalJSON(struct {
		ManifestDigest string      `json:"manifest_digest"`
		Entries        []digestRow `json:"entries"`
	}{manifestDigest, rows})
	if err != nil {
		return "", err
	}
	return digestDomain("phebs-dossier-root-v1", content), nil
}

func validateEntryReferences(manifest Manifest, entries []Entry) error {
	entryByPath := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		entryByPath[entry.Path] = entry
	}
	for _, reference := range []ObjectReference{
		manifest.Investigation,
		manifest.Revision,
		manifest.PrimaryArtifact,
		manifest.ConsumerSnapshot,
	} {
		if entryByPath[reference.EntryPath].Digest != reference.ContentDigest {
			return fmt.Errorf("referenced entry %q digest is inconsistent", reference.EntryPath)
		}
	}
	for _, reference := range []*ObjectReference{
		manifest.ChangeBrief,
		manifest.Baseline,
		manifest.Decision,
	} {
		if reference != nil &&
			entryByPath[reference.EntryPath].Digest != reference.ContentDigest {
			return fmt.Errorf("referenced entry %q digest is inconsistent", reference.EntryPath)
		}
	}
	for _, reference := range manifest.Evidence {
		finding := entryByPath[reference.FindingEntryPath]
		if finding.Digest != reference.FindingDigest {
			return fmt.Errorf(
				"finding entry %q digest is inconsistent",
				reference.FindingEntryPath,
			)
		}
		var identity struct {
			FactID string `json:"fact_id"`
		}
		if err := json.Unmarshal(finding.Content, &identity); err != nil ||
			identity.FactID != reference.FactID {
			return fmt.Errorf(
				"finding entry %q fact identity is inconsistent",
				reference.FindingEntryPath,
			)
		}
		if reference.Mode == "embedded" &&
			entryByPath[reference.EntryPath].Digest != reference.MaterialDigest {
			return fmt.Errorf("embedded evidence entry %q digest is inconsistent", reference.EntryPath)
		}
	}
	return nil
}

func NewEntry(path, mediaType string, value any) (Entry, error) {
	content, err := CanonicalJSON(value)
	if err != nil {
		return Entry{}, err
	}
	entries, err := normalizeEntries([]Entry{
		{Path: path, MediaType: mediaType, Content: content},
	})
	if err != nil {
		return Entry{}, err
	}
	return entries[0], nil
}

func VisibleUnitSetDigest(values []string) (string, error) {
	values, err := normalizeStrings("visible unit ids", values)
	if err != nil {
		return "", err
	}
	return visibleUnitSetDigest(values)
}

func Seal(
	manifest Manifest,
	entries []Entry,
	keyID string,
	privateKey ed25519.PrivateKey,
) (*Sealed, error) {
	if len(privateKey) != ed25519.PrivateKeySize || keyID == "" {
		return nil, errors.New("Ed25519 signing key and key id are required")
	}
	manifest.VerificationKeyID = keyID
	var err error
	manifest, err = normalizeManifest(manifest)
	if err != nil {
		return nil, err
	}
	entries, err = normalizeEntries(entries)
	if err != nil {
		return nil, err
	}
	if err := validateEntryReferences(manifest, entries); err != nil {
		return nil, err
	}
	manifestContent, err := CanonicalJSON(manifest)
	if err != nil {
		return nil, err
	}
	manifestDigest := digestDomain("phebs-dossier-manifest-v1", manifestContent)
	root, err := rootDigest(manifestDigest, entries)
	if err != nil {
		return nil, err
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	signature := ed25519.Sign(
		privateKey,
		[]byte("phebs-dossier-signature-v1\x00"+root),
	)
	return &Sealed{
		FormatVersion: FormatVersion, HashAlgorithm: HashAlgorithm,
		DossierID: manifest.DossierID, Manifest: manifest,
		ManifestDigest: manifestDigest, Entries: entries, RootDigest: root,
		Signature: Signature{
			Algorithm: "ed25519", KeyID: keyID,
			PublicKey: base64.StdEncoding.EncodeToString(publicKey),
			Value:     base64.StdEncoding.EncodeToString(signature),
		},
	}, nil
}

func Verify(sealed Sealed, options VerifyOptions) (*Verification, error) {
	if sealed.FormatVersion != FormatVersion ||
		sealed.HashAlgorithm != HashAlgorithm ||
		sealed.DossierID == "" ||
		sealed.DossierID != sealed.Manifest.DossierID {
		return nil, errors.New("dossier envelope identity is inconsistent")
	}
	manifest, err := normalizeManifest(sealed.Manifest)
	if err != nil {
		return nil, err
	}
	if !reflectManifestEqual(manifest, sealed.Manifest) {
		return nil, errors.New("dossier manifest is not canonical")
	}
	entries, err := normalizeEntries(sealed.Entries)
	if err != nil {
		return nil, err
	}
	if !reflectEntriesEqual(entries, sealed.Entries) {
		return nil, errors.New("dossier entries are not canonical")
	}
	if err := validateEntryReferences(manifest, entries); err != nil {
		return nil, err
	}
	manifestContent, err := CanonicalJSON(manifest)
	if err != nil {
		return nil, err
	}
	wantManifestDigest := digestDomain("phebs-dossier-manifest-v1", manifestContent)
	if sealed.ManifestDigest != wantManifestDigest {
		return nil, errors.New("dossier manifest digest mismatch")
	}
	wantRoot, err := rootDigest(wantManifestDigest, entries)
	if err != nil {
		return nil, err
	}
	if sealed.RootDigest != wantRoot {
		return nil, errors.New("dossier root digest mismatch")
	}
	if sealed.Signature.Algorithm != "ed25519" ||
		sealed.Signature.KeyID == "" ||
		sealed.Signature.KeyID != manifest.VerificationKeyID {
		return nil, errors.New("dossier signature identity is inconsistent")
	}
	publicKey, err := base64.StdEncoding.DecodeString(sealed.Signature.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("dossier public key is invalid")
	}
	signature, err := base64.StdEncoding.DecodeString(sealed.Signature.Value)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, errors.New("dossier signature is invalid")
	}
	if options.TrustedKeyID != "" && sealed.Signature.KeyID != options.TrustedKeyID {
		return nil, errors.New("dossier signer key id is not trusted")
	}
	if len(options.TrustedPublic) != 0 &&
		!bytes.Equal(publicKey, options.TrustedPublic) {
		return nil, errors.New("dossier signer public key is not trusted")
	}
	if !ed25519.Verify(
		ed25519.PublicKey(publicKey),
		[]byte("phebs-dossier-signature-v1\x00"+wantRoot),
		signature,
	) {
		return nil, errors.New("dossier signature verification failed")
	}
	fingerprint := sha256.Sum256(publicKey)
	return &Verification{
		DossierID: sealed.DossierID, RootDigest: sealed.RootDigest,
		KeyID:           sealed.Signature.KeyID,
		PublicKeySHA256: "sha256:" + hex.EncodeToString(fingerprint[:]),
		EntryCount:      len(entries),
	}, nil
}

func reflectManifestEqual(left, right Manifest) bool {
	leftBytes, leftErr := CanonicalJSON(left)
	rightBytes, rightErr := CanonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func reflectEntriesEqual(left, right []Entry) bool {
	leftBytes, leftErr := CanonicalJSON(left)
	rightBytes, rightErr := CanonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func Decode(content []byte) (*Sealed, error) {
	if len(content) == 0 || len(content) > MaxSealedBytes {
		return nil, errors.New("decode dossier: content size is outside the allowed range")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var sealed Sealed
	if err := decoder.Decode(&sealed); err != nil {
		return nil, fmt.Errorf("decode dossier: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode dossier: trailing JSON value")
	}
	canonical, err := CanonicalJSON(sealed)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(content, canonical) {
		return nil, errors.New("decode dossier: envelope is not canonical JSON")
	}
	return &sealed, nil
}
