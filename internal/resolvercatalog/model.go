// Package resolvercatalog owns immutable, commit-bound resolver catalog
// publications. Resolver adapters and their semantic records are intentionally
// outside this package; T30.6f freezes only the lifecycle and generic canonical
// JSON-record envelope consumed by later materializers.
package resolvercatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
)

const (
	ManifestSchema = "phebs-resolver-catalog-manifest-v1"
	RecordSchema   = "phebs-resolver-catalog-record-v1"
	StateSchema    = "phebs-resolver-catalog-state-v1"

	SourceLanePolicy  = resolvercatalogid.SourceLanePolicy
	CatalogPolicyName = "phebs-resolver-catalog-policy-v1"

	MaxDeclarationPublications = 16
	MaxResolverPacks           = 16
	MaxMembers                 = 256
	MaxRecordsPerMember        = 100_000
	MaxRecords                 = 1_000_000
	MaxRecordBytes             = 1 << 20
	MaxMetadataBytes           = 64 << 10
	MaxManifestBytes           = 1 << 20
	MaxMemberContentBytes      = int64(64 << 20)
	MaxCatalogContentBytes     = int64(512 << 20)
	// PublicationMemoryDesignBytes is the modeled per-publication design
	// budget, not a Go heap meter. The enforceable inputs beneath it retain
	// their individual byte/count caps.
	PublicationMemoryDesignBytes = int64(64 << 20)
	MaxDirectoryEntries          = 32_768
	MaxStagingDiskBytes          = int64(520 << 20)
	// MaxPublicationDiskBytes models one clean old live catalog, one full
	// serialized stage, and both manifests during a replacement transition.
	// Prior-process stages and undeclared residue are outside this model and
	// are bounded/reclaimed through the directory lifecycle.
	MaxPublicationDiskBytes = int64(1034 << 20)
	// MaxOpenFiles is structural: lifecycle code opens at most an archive plus
	// one artifact, or one member plus no other lifecycle-owned descriptor.
	MaxOpenFiles = 2

	maxManifestBytes = MaxManifestBytes
)

var (
	ErrInvalidIdentity = errors.New("invalid resolver catalog identity")
	ErrInvalidManifest = errors.New("invalid resolver catalog manifest")
	ErrPublishing      = errors.New("resolver catalog publication is incomplete")
	ErrLimit           = errors.New("resolver catalog bound exceeded")
)

// DeclarationPublication identifies one exact declaration plane consumed by
// every resolver pack. Entries are ordered by Domain.
type DeclarationPublication struct {
	Domain           string `json:"domain"`
	RunID            string `json:"run_id"`
	GenerationDigest string `json:"generation_digest"`
}

// ResolverPack identifies one deterministic adapter generation. The empty set
// is the neutral T30.6f fixture and does not authorize any resolver semantics.
type ResolverPack struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Policy freezes lifecycle resource bounds as digest-bearing behavior.
type Policy struct {
	Name                         string `json:"name"`
	RecordSchema                 string `json:"record_schema"`
	RecordOrdering               string `json:"record_ordering"`
	MaxDeclarationPublications   int    `json:"max_declaration_publications"`
	MaxResolverPacks             int    `json:"max_resolver_packs"`
	MaxMembers                   int    `json:"max_members"`
	MaxRecordsPerMember          int    `json:"max_records_per_member"`
	MaxRecords                   int    `json:"max_records"`
	MaxRecordBytes               int    `json:"max_record_bytes"`
	MaxMetadataBytes             int    `json:"max_metadata_bytes"`
	MaxManifestBytes             int    `json:"max_manifest_bytes"`
	MaxMemberContentBytes        int64  `json:"max_member_content_bytes"`
	MaxCatalogContentBytes       int64  `json:"max_catalog_content_bytes"`
	PublicationMemoryDesignBytes int64  `json:"max_memory_bytes"`
	MaxDirectoryEntries          int    `json:"max_directory_entries"`
	MaxStagingDiskBytes          int64  `json:"max_staging_disk_bytes"`
	MaxPublicationDiskBytes      int64  `json:"max_publication_disk_bytes"`
	MaxOpenFiles                 int    `json:"max_open_files"`
}

func FrozenPolicy() Policy {
	return Policy{
		Name:                         CatalogPolicyName,
		RecordSchema:                 RecordSchema,
		RecordOrdering:               "member-name-then-emission-v1",
		MaxDeclarationPublications:   MaxDeclarationPublications,
		MaxResolverPacks:             MaxResolverPacks,
		MaxMembers:                   MaxMembers,
		MaxRecordsPerMember:          MaxRecordsPerMember,
		MaxRecords:                   MaxRecords,
		MaxRecordBytes:               MaxRecordBytes,
		MaxMetadataBytes:             MaxMetadataBytes,
		MaxManifestBytes:             MaxManifestBytes,
		MaxMemberContentBytes:        MaxMemberContentBytes,
		MaxCatalogContentBytes:       MaxCatalogContentBytes,
		PublicationMemoryDesignBytes: PublicationMemoryDesignBytes,
		MaxDirectoryEntries:          MaxDirectoryEntries,
		MaxStagingDiskBytes:          MaxStagingDiskBytes,
		MaxPublicationDiskBytes:      MaxPublicationDiskBytes,
		MaxOpenFiles:                 MaxOpenFiles,
	}
}

// Identity binds every immutable input independently of member bytes.
type Identity struct {
	Repository              string                   `json:"repository"`
	Commit                  string                   `json:"commit"`
	UnitDigest              string                   `json:"unit_digest"`
	Declarations            []DeclarationPublication `json:"declarations"`
	DeclarationSetDigest    string                   `json:"declaration_set_digest"`
	CandidateManifestDigest string                   `json:"candidate_manifest_digest"`
	SourceLanePolicy        string                   `json:"source_lane_policy"`
	ResolverPacks           []ResolverPack           `json:"resolver_packs"`
	ResolverPackSetDigest   string                   `json:"resolver_pack_set_digest"`
	CatalogPolicy           Policy                   `json:"catalog_policy"`
	CatalogPolicyDigest     string                   `json:"catalog_policy_digest"`
	GenerationDigest        string                   `json:"generation_digest"`
}

// MemberReceipt binds one canonical NDJSON member and its canonical metadata.
type MemberReceipt struct {
	Name           string          `json:"name"`
	Metadata       json.RawMessage `json:"metadata"`
	RecordCount    int             `json:"record_count"`
	ContentBytes   int64           `json:"content_bytes"`
	ContentDigest  string          `json:"content_digest"`
	MetadataDigest string          `json:"metadata_digest"`
}

// Manifest is the sole filesystem visibility authority.
type Manifest struct {
	Schema   string          `json:"schema"`
	Identity Identity        `json:"identity"`
	Members  []MemberReceipt `json:"members"`
	Digest   string          `json:"digest"`
}

// State is the primitive database publication pointer.
type State struct {
	Schema                  string                   `json:"schema"`
	Repository              string                   `json:"repository"`
	Commit                  string                   `json:"commit"`
	UnitDigest              string                   `json:"unit_digest"`
	Declarations            []DeclarationPublication `json:"declarations"`
	DeclarationSetDigest    string                   `json:"declaration_set_digest"`
	CandidateManifestDigest string                   `json:"candidate_manifest_digest"`
	SourceLanePolicy        string                   `json:"source_lane_policy"`
	ResolverPacks           []ResolverPack           `json:"resolver_packs"`
	ResolverPackSetDigest   string                   `json:"resolver_pack_set_digest"`
	CatalogPolicyDigest     string                   `json:"catalog_policy_digest"`
	GenerationDigest        string                   `json:"generation_digest"`
	ManifestDigest          string                   `json:"manifest_digest"`
	Manifest                string                   `json:"manifest"`
}

func (manifest Manifest) State() State {
	return State{
		Schema:                  StateSchema,
		Repository:              manifest.Identity.Repository,
		Commit:                  manifest.Identity.Commit,
		UnitDigest:              manifest.Identity.UnitDigest,
		Declarations:            append([]DeclarationPublication{}, manifest.Identity.Declarations...),
		DeclarationSetDigest:    manifest.Identity.DeclarationSetDigest,
		CandidateManifestDigest: manifest.Identity.CandidateManifestDigest,
		SourceLanePolicy:        manifest.Identity.SourceLanePolicy,
		ResolverPacks:           append([]ResolverPack{}, manifest.Identity.ResolverPacks...),
		ResolverPackSetDigest:   manifest.Identity.ResolverPackSetDigest,
		CatalogPolicyDigest:     manifest.Identity.CatalogPolicyDigest,
		GenerationDigest:        manifest.Identity.GenerationDigest,
		ManifestDigest:          manifest.Digest,
		Manifest:                resolvercatalogid.ManifestName(manifest.Identity.Repository),
	}
}

func statesEqual(left, right State) bool {
	if !slices.Equal(left.Declarations, right.Declarations) {
		return false
	}
	if !slices.Equal(left.ResolverPacks, right.ResolverPacks) {
		return false
	}
	left.Declarations = nil
	right.Declarations = nil
	left.ResolverPacks = nil
	right.ResolverPacks = nil
	return reflect.DeepEqual(left, right)
}

// NewIdentity validates primitive inputs and derives every set/policy digest.
// Callers cannot supply a digest that disagrees with the ordered values.
func NewIdentity(
	repository, commit, unitDigest, candidateManifestDigest string,
	declarations []DeclarationPublication,
	packs []ResolverPack,
) (Identity, error) {
	identity := Identity{
		Repository: repository, Commit: commit, UnitDigest: unitDigest,
		Declarations:            append([]DeclarationPublication{}, declarations...),
		CandidateManifestDigest: candidateManifestDigest,
		SourceLanePolicy:        SourceLanePolicy,
		ResolverPacks:           append([]ResolverPack{}, packs...),
		CatalogPolicy:           FrozenPolicy(),
	}
	var err error
	identity.DeclarationSetDigest, err = digestCanonical(identity.Declarations)
	if err != nil {
		return Identity{}, err
	}
	identity.ResolverPackSetDigest, err = digestCanonical(identity.ResolverPacks)
	if err != nil {
		return Identity{}, err
	}
	identity.CatalogPolicyDigest, err = digestCanonical(identity.CatalogPolicy)
	if err != nil {
		return Identity{}, err
	}
	generation := identity
	generation.GenerationDigest = ""
	identity.GenerationDigest, err = digestCanonical(generation)
	if err != nil {
		return Identity{}, err
	}
	if err := validateIdentity(identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func validateIdentity(identity Identity) error {
	if err := reponame.Validate(identity.Repository); err != nil {
		return fmt.Errorf("%w: repository: %v", ErrInvalidIdentity, err)
	}
	if !validObjectID(identity.Commit) {
		return fmt.Errorf("%w: commit is not a canonical object id", ErrInvalidIdentity)
	}
	if identity.UnitDigest != "" && !validDigest(identity.UnitDigest) {
		return fmt.Errorf("%w: unit digest", ErrInvalidIdentity)
	}
	if !validDigest(identity.CandidateManifestDigest) {
		return fmt.Errorf("%w: candidate manifest digest", ErrInvalidIdentity)
	}
	if identity.SourceLanePolicy != SourceLanePolicy {
		return fmt.Errorf("%w: source-lane policy", ErrInvalidIdentity)
	}
	if len(identity.Declarations) > MaxDeclarationPublications {
		return fmt.Errorf("%w: declaration publications: %w", ErrInvalidIdentity, ErrLimit)
	}
	if identity.Declarations == nil {
		return fmt.Errorf("%w: declarations must be an explicit set", ErrInvalidIdentity)
	}
	for index, declaration := range identity.Declarations {
		if declaration.Domain == "" || !validToken(declaration.Domain, 128) ||
			declaration.RunID == "" || !validToken(declaration.RunID, 256) ||
			!validExtractionGenerationDigest(declaration.GenerationDigest) {
			return fmt.Errorf("%w: declaration %d", ErrInvalidIdentity, index)
		}
		if index > 0 && identity.Declarations[index-1].Domain >= declaration.Domain {
			return fmt.Errorf("%w: declarations are not uniquely ordered", ErrInvalidIdentity)
		}
	}
	if len(identity.ResolverPacks) > MaxResolverPacks {
		return fmt.Errorf("%w: resolver packs: %w", ErrInvalidIdentity, ErrLimit)
	}
	if identity.ResolverPacks == nil {
		return fmt.Errorf("%w: resolver packs must be an explicit set", ErrInvalidIdentity)
	}
	for index, pack := range identity.ResolverPacks {
		if !validToken(pack.Name, 128) || !validToken(pack.Version, 64) {
			return fmt.Errorf("%w: resolver pack %d", ErrInvalidIdentity, index)
		}
		if index > 0 && identity.ResolverPacks[index-1].Name >= pack.Name {
			return fmt.Errorf("%w: resolver packs are not uniquely ordered", ErrInvalidIdentity)
		}
	}
	if identity.CatalogPolicy != FrozenPolicy() {
		return fmt.Errorf("%w: catalog policy", ErrInvalidIdentity)
	}
	declarationDigest, err := digestCanonical(identity.Declarations)
	if err != nil || declarationDigest != identity.DeclarationSetDigest {
		return fmt.Errorf("%w: declaration-set digest", ErrInvalidIdentity)
	}
	packDigest, err := digestCanonical(identity.ResolverPacks)
	if err != nil || packDigest != identity.ResolverPackSetDigest {
		return fmt.Errorf("%w: resolver-pack-set digest", ErrInvalidIdentity)
	}
	policyDigest, err := digestCanonical(identity.CatalogPolicy)
	if err != nil || policyDigest != identity.CatalogPolicyDigest {
		return fmt.Errorf("%w: catalog-policy digest", ErrInvalidIdentity)
	}
	generation := identity
	generation.GenerationDigest = ""
	generationDigest, err := digestCanonical(generation)
	if err != nil || generationDigest != identity.GenerationDigest {
		return fmt.Errorf("%w: generation digest", ErrInvalidIdentity)
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Schema != ManifestSchema {
		return fmt.Errorf("%w: schema", ErrInvalidManifest)
	}
	if err := validateIdentity(manifest.Identity); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if len(manifest.Members) > MaxMembers {
		return fmt.Errorf("%w: members: %w", ErrInvalidManifest, ErrLimit)
	}
	if manifest.Members == nil {
		return fmt.Errorf("%w: members must be an explicit set", ErrInvalidManifest)
	}
	var records int
	var contentBytes int64
	memberKeys := make(map[string]struct{}, len(manifest.Members))
	for index, member := range manifest.Members {
		if !validMemberName(member.Name) || len(member.Metadata) == 0 ||
			len(member.Metadata) > MaxMetadataBytes ||
			member.RecordCount < 0 ||
			member.RecordCount > MaxRecordsPerMember ||
			member.ContentBytes < 0 ||
			member.ContentBytes > MaxMemberContentBytes ||
			!validDigest(member.ContentDigest) ||
			!validDigest(member.MetadataDigest) {
			return fmt.Errorf("%w: member %d", ErrInvalidManifest, index)
		}
		metadata, err := canonicalRaw(member.Metadata, MaxMetadataBytes)
		if err != nil || !bytes.Equal(metadata, member.Metadata) {
			return fmt.Errorf("%w: member %d metadata", ErrInvalidManifest, index)
		}
		metadataDigest, err := digestCanonical(json.RawMessage(metadata))
		if err != nil || metadataDigest != member.MetadataDigest {
			return fmt.Errorf("%w: member %d metadata digest", ErrInvalidManifest, index)
		}
		if index > 0 && manifest.Members[index-1].Name >= member.Name {
			return fmt.Errorf("%w: members are not uniquely ordered", ErrInvalidManifest)
		}
		key := portableMemberNameKey(member.Name)
		if _, duplicate := memberKeys[key]; duplicate {
			return fmt.Errorf("%w: members collide on a portable filesystem", ErrInvalidManifest)
		}
		memberKeys[key] = struct{}{}
		records += member.RecordCount
		contentBytes += member.ContentBytes
		if records > MaxRecords || contentBytes > MaxCatalogContentBytes {
			return fmt.Errorf("%w: aggregate member bounds: %w", ErrInvalidManifest, ErrLimit)
		}
	}
	unsigned := manifest
	unsigned.Digest = ""
	digest, err := digestCanonical(unsigned)
	if err != nil || digest != manifest.Digest {
		return fmt.Errorf("%w: self digest", ErrInvalidManifest)
	}
	return nil
}

func validMemberName(value string) bool {
	return validToken(value, 128) && path.Base(value) == value &&
		!strings.HasPrefix(value, ".") && strings.HasSuffix(value, ".ndjson")
}

// portableMemberNameKey matches the case-folded, NFC-normalized identity used
// by repository locks. APFS commonly aliases these spellings even when their
// UTF-8 byte ordering is distinct.
func portableMemberNameKey(value string) string {
	return repowork.CanonicalKey(value)
}

func validToken(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && hex.EncodeToString(decoded) == strings.TrimPrefix(value, "sha256:")
}

func validExtractionGenerationDigest(value string) bool {
	const prefix = "extraction_generation_v1_"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil &&
		hex.EncodeToString(decoded) == strings.TrimPrefix(value, prefix)
}

func digestCanonical(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalRaw(raw json.RawMessage, max int) ([]byte, error) {
	if len(raw) > max {
		return nil, ErrLimit
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing JSON value")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(canonical) > max {
		return nil, ErrLimit
	}
	return canonical, nil
}

func validateRecordEnvelope(raw []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope == nil {
		return errors.New("resolver catalog record must be an object")
	}
	var schema string
	if err := json.Unmarshal(envelope["schema"], &schema); err != nil ||
		schema != RecordSchema {
		return errors.New("resolver catalog record schema is missing or unsupported")
	}
	return nil
}
