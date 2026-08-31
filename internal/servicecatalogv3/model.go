// Package servicecatalogv3 defines the runtime-dark segmented service-catalog
// contract selected by T41.2. Storage, source-census proof, and activation are
// deliberately owned by later tickets.
package servicecatalogv3

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

const (
	RootSchema            = "phebs-service-catalog-v3"
	ServiceMemberSchema   = "phebs-service-catalog-v3-service-member"
	PlacementMemberSchema = "phebs-service-catalog-v3-placement-member"

	MaxTotalServices  = 12_500
	MaxMemberships    = 75_000
	MaxDistinctPaths  = 40_000
	MaxSuccessorEdges = 12_500
	// V3 narrows v2's aggregate-only posture to one per-service bound.
	MaxServiceSuccessors  = 512
	MaxClaimsPerPlacement = 4_000
	MaxServicesPerMember  = 512
	MaxPathsPerMember     = 2_048
	MaxMembers            = 64
	MaxRootBytes          = 256 << 10
	MaxMemberBytes        = 2 << 20
	MaxLogicalBytes       = 16 << 20
	MaxPublicationBytes   = 32 << 20
	maxPreflightDepth     = 64
)

var (
	ErrInvalid         = errors.New("invalid segmented service catalog")
	ErrLimit           = errors.New("segmented service catalog limit exceeded")
	ErrNotV2Compatible = errors.New("segmented service catalog is not v2 compatible")
)

type Policy struct {
	Name                  string `json:"name"`
	MaxTotalServices      int    `json:"max_total_services"`
	MaxMemberships        int    `json:"max_memberships"`
	MaxDistinctPaths      int    `json:"max_distinct_paths"`
	MaxSuccessorEdges     int    `json:"max_successor_edges"`
	MaxServiceSuccessors  int    `json:"max_service_successors"`
	MaxClaimsPerPlacement int    `json:"max_claims_per_placement"`
	MaxServicesPerMember  int    `json:"max_services_per_member"`
	MaxPathsPerMember     int    `json:"max_paths_per_member"`
	MaxMembers            int    `json:"max_members"`
	MaxRootBytes          int    `json:"max_root_bytes"`
	MaxMemberBytes        int    `json:"max_member_bytes"`
	MaxLogicalBytes       int    `json:"max_logical_bytes"`
	MaxPublicationBytes   int    `json:"max_publication_bytes"`
	PlacementRouting      string `json:"placement_routing"`
}

func FrozenPolicy() Policy {
	return Policy{
		Name: "segmented-service-catalog-v3", MaxTotalServices: MaxTotalServices,
		MaxMemberships: MaxMemberships, MaxDistinctPaths: MaxDistinctPaths,
		MaxSuccessorEdges: MaxSuccessorEdges, MaxServiceSuccessors: MaxServiceSuccessors,
		MaxClaimsPerPlacement: MaxClaimsPerPlacement,
		MaxServicesPerMember:  MaxServicesPerMember, MaxPathsPerMember: MaxPathsPerMember,
		MaxMembers: MaxMembers, MaxRootBytes: MaxRootBytes, MaxMemberBytes: MaxMemberBytes,
		MaxLogicalBytes: MaxLogicalBytes, MaxPublicationBytes: MaxPublicationBytes,
		PlacementRouting: "greatest-first-path-with-exact-inherited-ancestors-v1",
	}
}

type Source struct {
	Kind              string `json:"kind"`
	Path              string `json:"path,omitempty"`
	Commit            string `json:"commit,omitempty"`
	CensusDigest      string `json:"census_digest"`
	LegacyDigest      string `json:"legacy_digest,omitempty"`
	FileCount         int    `json:"file_count"`
	AcceptedFileCount int    `json:"accepted_file_count"`
	UnownedFileCount  int    `json:"unowned_file_count"`
}

type Binding struct {
	Repository string                           `json:"repository"`
	Source     Source                           `json:"source"`
	Authority  servicecatalog.Authority         `json:"authority"`
	Override   *servicecatalog.OperatorOverride `json:"override,omitempty"`
}

type DispositionCounts struct {
	Accepted int `json:"accepted"`
	Proposal int `json:"proposal"`
	Conflict int `json:"conflict"`
	Rejected int `json:"rejected"`
}

type RoleCounts struct {
	Primary    int `json:"primary"`
	Supporting int `json:"supporting"`
	Shared     int `json:"shared"`
	Generated  int `json:"generated"`
	Typed      int `json:"typed"`
}

type MemberDescriptor struct {
	Kind          string `json:"kind"`
	Ordinal       int    `json:"ordinal"`
	Count         int    `json:"count"`
	First         string `json:"first"`
	Last          string `json:"last"`
	Records       int    `json:"records"`
	Memberships   int    `json:"memberships"`
	Claims        int    `json:"claims"`
	PreludeClaims int    `json:"prelude_claims,omitempty"`
	ContentBytes  int    `json:"content_bytes"`
	Digest        string `json:"digest"`
}

type Root struct {
	Schema             string             `json:"schema"`
	Binding            Binding            `json:"binding"`
	Policy             Policy             `json:"policy"`
	PolicyDigest       string             `json:"policy_digest"`
	LogicalDigest      string             `json:"logical_digest"`
	MappedV2Digest     string             `json:"mapped_v2_digest,omitempty"`
	Services           int                `json:"services"`
	Dispositions       DispositionCounts  `json:"dispositions"`
	Memberships        int                `json:"memberships"`
	Roles              RoleCounts         `json:"roles"`
	Paths              int                `json:"paths"`
	Unowned            int                `json:"unowned"`
	Successors         int                `json:"successors"`
	Claims             int                `json:"claims"`
	ServiceMembers     []MemberDescriptor `json:"service_members"`
	PlacementMembers   []MemberDescriptor `json:"placement_members"`
	EncodedMemberBytes int                `json:"encoded_member_bytes"`
	RootBytes          int                `json:"root_bytes"`
	EncodedBytes       int                `json:"encoded_bytes"`
	Digest             string             `json:"digest"`
}

type EncodedMember struct {
	Kind    string
	Ordinal int
	Content []byte
}

type Generation struct {
	Root    Root
	Members []EncodedMember
}

type ServiceMember struct {
	Schema        string                      `json:"schema"`
	PolicyDigest  string                      `json:"policy_digest"`
	LogicalDigest string                      `json:"logical_digest"`
	Ordinal       int                         `json:"ordinal"`
	Count         int                         `json:"count"`
	FirstKey      string                      `json:"first_key"`
	LastKey       string                      `json:"last_key"`
	Services      []servicecatalog.Service    `json:"services"`
	Memberships   []servicecatalog.Membership `json:"memberships"`
}

type ClaimRole struct {
	Role   string `json:"role"`
	Origin string `json:"origin"`
}

type Claim struct {
	ServiceKey  string      `json:"service_key"`
	Disposition string      `json:"disposition"`
	Roles       []ClaimRole `json:"roles"`
}

type Placement struct {
	Path    string                           `json:"path"`
	Claims  []Claim                          `json:"claims"`
	Unowned *servicecatalog.UnownedPlacement `json:"unowned,omitempty"`
}

type PlacementMember struct {
	Schema        string      `json:"schema"`
	PolicyDigest  string      `json:"policy_digest"`
	LogicalDigest string      `json:"logical_digest"`
	Ordinal       int         `json:"ordinal"`
	Count         int         `json:"count"`
	FirstPath     string      `json:"first_path"`
	LastPath      string      `json:"last_path"`
	Inherited     []Placement `json:"inherited"`
	Placements    []Placement `json:"placements"`
}

func PolicyDigest() string {
	return digest("phebs-service-catalog-v3-policy\x00", mustJSON(FrozenPolicy()))
}

func RootDigest(root Root) (string, error) {
	root.Digest = ""
	raw, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return digest("phebs-service-catalog-v3-root\x00", raw), nil
}

func digest(domain string, raw []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(raw)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func rawDigest(raw []byte) string { return digest("", raw) }

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	decoded, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && len(decoded) == sha256.Size
}

func canonical(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func decodeCanonical(raw []byte, value any) error {
	if err := preflightJSON(raw, MaxMemberBytes, memberCollectionLimit); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	want, err := canonical(value)
	if err != nil || !bytes.Equal(raw, want) {
		return ErrInvalid
	}
	return nil
}

func preflightCollections(raw []byte) error {
	return preflightJSON(raw, MaxMemberBytes, memberCollectionLimit)
}

type collectionLimit func(string) (int, bool)

func preflightJSON(raw []byte, maxBytes int, limit collectionLimit) error {
	if len(raw) > maxBytes {
		return limitf("encoded bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := preflightValue(decoder, "", 0, limit); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func preflightValue(
	decoder *json.Decoder,
	field string,
	depth int,
	limit collectionLimit,
) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= maxPreflightDepth {
		return limitf("nesting depth")
	}
	switch delimiter {
	case '{':
		for decoder.More() {
			name, ok := anyTokenString(decoder)
			if !ok {
				return ErrInvalid
			}
			if err := preflightValue(decoder, name, depth+1, limit); err != nil {
				return err
			}
		}
	case '[':
		maximum, bounded := limit(field)
		count := 0
		for decoder.More() {
			if bounded && count >= maximum {
				return limitf("collection %q", field)
			}
			if err := preflightValue(decoder, "", depth+1, limit); err != nil {
				return err
			}
			count++
		}
	default:
		return ErrInvalid
	}
	end, err := decoder.Token()
	if err != nil || end != matchingDelimiter(delimiter) {
		return ErrInvalid
	}
	return nil
}

func anyTokenString(decoder *json.Decoder) (string, bool) {
	token, err := decoder.Token()
	if err != nil {
		return "", false
	}
	value, ok := token.(string)
	return value, ok
}

func matchingDelimiter(delimiter json.Delim) json.Delim {
	if delimiter == '{' {
		return '}'
	}
	return ']'
}

func memberCollectionLimit(field string) (int, bool) {
	switch field {
	case "services":
		return MaxServicesPerMember, true
	case "memberships", "roles":
		return MaxMemberships, true
	case "successors":
		return MaxServiceSuccessors, true
	case "inherited":
		return MaxDistinctPaths, true
	case "placements":
		return MaxPathsPerMember, true
	case "claims":
		return MaxClaimsPerPlacement, true
	default:
		return 0, false
	}
}

func mustJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func limitf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrLimit, fmt.Sprintf(format, args...))
}
