// Package relationshippublication projects repository-shared relationship
// postings onto exact service-catalog placement authority and publishes one
// atomic repository root with independent service partitions.
package relationshippublication

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
)

const (
	RootSchema               = "phebs-relationship-root-v1"
	RootSchemaV2             = "phebs-relationship-root-v2"
	RepositoryMemberSchema   = "phebs-relationship-repository-member-v1"
	RepositoryMemberSchemaV2 = "phebs-relationship-repository-member-v2"
	ProjectionSchema         = "phebs-relationship-projection-v1"
	ServiceMemberSchema      = "phebs-service-relationship-member-v1"
	ServiceMemberSchemaV2    = "phebs-service-relationship-member-v2"
	ServiceReferenceSchema   = "phebs-service-relationship-reference-v1"
	PolicySchema             = "phebs-relationship-policy-v1"
	PointerSchema            = "phebs-relationship-pointer-v1"
	MarkerSchema             = "phebs-relationship-marker-v1"

	RepositoryBuckets               = 256
	MaxServices                     = 4_000
	MaxProjectionRecords            = 2_000_000
	MaxServiceReferences            = 1_000_000
	MaxTotalServiceReferences       = 20_000_000
	MaxClaimsPerPlacement           = 4_000
	MaxRolesPerClaim                = 5
	MaxTextBytes                    = 4_096
	MaxProjectionBytes              = 1 << 20
	MaxReferenceBytes               = 64 << 10
	MaxRepositoryMemberBytes        = 128 << 20
	MaxServiceMemberBytes           = 128 << 20
	MaxRootBytes                    = 8 << 20
	MaxGenerationBytes        int64 = 20 << 30
	// MaxResidentChargeBytes is a conservative in-process admission fence.
	// Builders charge canonical bytes plus fixed map/slice overhead before
	// retaining each repository projection or service reference. This keeps
	// the registered one-GiB scheduler class honest without pretending that
	// the encoded-generation ceiling is a resident-memory promise.
	MaxResidentChargeBytes int64 = 512 << 20
)

var (
	ErrInvalid    = errors.New("invalid relationship publication")
	ErrLimit      = errors.New("relationship publication bound exceeded")
	ErrNotFound   = errors.New("relationship publication not found")
	ErrPublishing = errors.New("relationship publication changed")
	ErrDenied     = errors.New("relationship publication not found")
)

type Policy struct {
	Schema                    string `json:"schema"`
	RepositoryBuckets         int    `json:"repository_buckets"`
	MaxServices               int    `json:"max_services"`
	MaxProjectionRecords      int    `json:"max_projection_records"`
	MaxServiceReferences      int    `json:"max_service_references"`
	MaxTotalServiceReferences int    `json:"max_total_service_references"`
	MaxClaimsPerPlacement     int    `json:"max_claims_per_placement"`
	MaxRolesPerClaim          int    `json:"max_roles_per_claim"`
	MaxTextBytes              int    `json:"max_text_bytes"`
	MaxProjectionBytes        int    `json:"max_projection_bytes"`
	MaxReferenceBytes         int    `json:"max_reference_bytes"`
	MaxRepositoryMemberBytes  int    `json:"max_repository_member_bytes"`
	MaxServiceMemberBytes     int    `json:"max_service_member_bytes"`
	MaxRootBytes              int    `json:"max_root_bytes"`
	MaxGenerationBytes        int64  `json:"max_generation_bytes"`
	MaxResidentChargeBytes    int64  `json:"max_resident_charge_bytes"`
	PlacementRule             string `json:"placement_rule"`
}

func FrozenPolicy() Policy {
	return Policy{
		Schema: PolicySchema, RepositoryBuckets: RepositoryBuckets,
		MaxServices: MaxServices, MaxProjectionRecords: MaxProjectionRecords,
		MaxServiceReferences:      MaxServiceReferences,
		MaxTotalServiceReferences: MaxTotalServiceReferences,
		MaxClaimsPerPlacement:     MaxClaimsPerPlacement, MaxRolesPerClaim: MaxRolesPerClaim,
		MaxTextBytes: MaxTextBytes, MaxProjectionBytes: MaxProjectionBytes,
		MaxReferenceBytes:        MaxReferenceBytes,
		MaxRepositoryMemberBytes: MaxRepositoryMemberBytes,
		MaxServiceMemberBytes:    MaxServiceMemberBytes, MaxRootBytes: MaxRootBytes,
		MaxGenerationBytes:     MaxGenerationBytes,
		MaxResidentChargeBytes: MaxResidentChargeBytes,
		PlacementRule:          "exact-prefix-membership-and-exact-unowned-v1",
	}
}

type Authority struct {
	Repository                  string                         `json:"repository"`
	CatalogGenerationDigest     string                         `json:"catalog_generation_digest"`
	CatalogDigest               string                         `json:"catalog_digest"`
	CatalogSourceGeneration     string                         `json:"catalog_source_generation"`
	ServiceStateSetDigest       string                         `json:"service_state_set_digest"`
	ServiceStateSummaryDigest   string                         `json:"service_state_summary_digest,omitempty"`
	ServiceStateControlRevision uint64                         `json:"service_state_control_revision,omitempty"`
	ObservationGenerationDigest string                         `json:"observation_generation_digest"`
	ObservationManifestDigest   string                         `json:"observation_manifest_digest"`
	ObservationSourceDigest     string                         `json:"observation_source_digest"`
	ResolverGenerationDigest    string                         `json:"resolver_generation_digest"`
	ResolverRootDigest          string                         `json:"resolver_root_digest"`
	RPCGenerationDigest         string                         `json:"rpc_generation_digest"`
	RPCRootDigest               string                         `json:"rpc_root_digest"`
	KafkaGenerationDigest       string                         `json:"kafka_generation_digest"`
	KafkaRootDigest             string                         `json:"kafka_root_digest"`
	PolicyDigest                string                         `json:"policy_digest"`
	Upstream                    *downstreamauthority.Authority `json:"upstream,omitempty"`
}

type RoleClaim struct {
	Role   string `json:"role"`
	Origin string `json:"origin"`
}

type ServiceClaim struct {
	ServiceKey  string      `json:"service_key"`
	Disposition string      `json:"disposition"`
	Roles       []RoleClaim `json:"roles"`
}

type Placement struct {
	Path    string         `json:"path"`
	Unowned bool           `json:"unowned"`
	Claims  []ServiceClaim `json:"claims"`
}

type Projection struct {
	Schema        string     `json:"schema"`
	Kind          string     `json:"kind"` // rpc | kafka
	PostingDigest string     `json:"posting_digest"`
	Class         string     `json:"class"`
	Plane         string     `json:"plane"`
	LookupKey     string     `json:"lookup_key,omitempty"`
	Source        Placement  `json:"source"`
	Target        *Placement `json:"target,omitempty"`
	Digest        string     `json:"digest"`
}

type RepositoryMember struct {
	Schema          string       `json:"schema"`
	AuthorityDigest string       `json:"authority_digest"`
	Bucket          int          `json:"bucket"`
	Projections     []Projection `json:"projections"`
	Digest          string       `json:"digest"`
}

type ServiceReference struct {
	Schema           string   `json:"schema"`
	ProjectionDigest string   `json:"projection_digest"`
	PostingDigest    string   `json:"posting_digest"`
	Kind             string   `json:"kind"`
	Plane            string   `json:"plane"`
	LookupKey        string   `json:"lookup_key,omitempty"`
	Participation    []string `json:"participation"` // source | target
	Digest           string   `json:"digest"`
}

type ServiceMember struct {
	Schema            string             `json:"schema"`
	AuthorityDigest   string             `json:"authority_digest"`
	ServiceKey        string             `json:"service_key"`
	Incarnation       uint64             `json:"incarnation"`
	ServiceGeneration string             `json:"service_generation"`
	References        []ServiceReference `json:"references"`
	Digest            string             `json:"digest"`
}

type MemberReceipt struct {
	Bucket        int    `json:"bucket"`
	Name          string `json:"name"`
	RecordCount   int    `json:"record_count"`
	ContentBytes  int64  `json:"content_bytes"`
	ContentDigest string `json:"content_digest"`
}

type ServiceReceipt struct {
	ServiceKey        string `json:"service_key"`
	Incarnation       uint64 `json:"incarnation"`
	ServiceGeneration string `json:"service_generation"`
	State             string `json:"state"` // complete | empty | failed
	Reason            string `json:"reason,omitempty"`
	Name              string `json:"name,omitempty"`
	ReferenceCount    int    `json:"reference_count"`
	ContentBytes      int64  `json:"content_bytes"`
	ContentDigest     string `json:"content_digest,omitempty"`
}

type serviceSetIdentity struct {
	ServiceKey        string `json:"service_key"`
	Incarnation       uint64 `json:"incarnation"`
	DesiredGeneration string `json:"desired_generation"`
}

func digestServiceSet(values []serviceSetIdentity) (string, error) {
	return digestValue(struct {
		Schema   string               `json:"schema"`
		Services []serviceSetIdentity `json:"services"`
	}{Schema: "phebs-relationship-service-state-set-v1", Services: values})
}

type Root struct {
	Schema                 string           `json:"schema"`
	Authority              Authority        `json:"authority"`
	AuthorityDigest        string           `json:"authority_digest"`
	Policy                 Policy           `json:"policy"`
	RepositoryComplete     bool             `json:"repository_complete"`
	AllServicesComplete    bool             `json:"all_services_complete"`
	RepositoryMembers      []MemberReceipt  `json:"repository_members"`
	Services               []ServiceReceipt `json:"services"`
	ProjectionCount        int              `json:"projection_count"`
	ServiceCount           int              `json:"service_count"`
	CompleteServiceCount   int              `json:"complete_service_count"`
	EmptyServiceCount      int              `json:"empty_service_count"`
	FailedServiceCount     int              `json:"failed_service_count"`
	ServiceReferenceCount  int              `json:"service_reference_count"`
	EncodedRepositoryBytes int64            `json:"encoded_repository_bytes"`
	EncodedServiceBytes    int64            `json:"encoded_service_bytes"`
	GenerationDigest       string           `json:"generation_digest"`
	Digest                 string           `json:"digest"`
}

type Pointer struct {
	Schema           string `json:"schema"`
	Repository       string `json:"repository"`
	GenerationDigest string `json:"generation_digest"`
	RootDigest       string `json:"root_digest"`
	RootName         string `json:"root_name"`
	Digest           string `json:"digest"`
}

type Marker struct {
	Schema  string  `json:"schema"`
	Pointer Pointer `json:"pointer"`
	Digest  string  `json:"digest"`
}

func validateAuthority(schema string, value Authority) error {
	if reponame.Validate(value.Repository) != nil ||
		!validDigest(value.CatalogGenerationDigest) || !validDigest(value.CatalogDigest) ||
		!validDigest(value.CatalogSourceGeneration) ||
		!validDigest(value.ServiceStateSetDigest) ||
		!validDigest(value.ObservationGenerationDigest) || !validDigest(value.ObservationManifestDigest) ||
		!validDigest(value.ObservationSourceDigest) || !validDigest(value.ResolverGenerationDigest) ||
		!validDigest(value.ResolverRootDigest) || !validDigest(value.RPCGenerationDigest) ||
		!validDigest(value.RPCRootDigest) || !validDigest(value.KafkaGenerationDigest) ||
		!validDigest(value.KafkaRootDigest) || !validDigest(value.PolicyDigest) {
		return fmt.Errorf("%w: authority", ErrInvalid)
	}
	if schema == RootSchema && (value.Upstream != nil || value.ServiceStateSummaryDigest != "" ||
		value.ServiceStateControlRevision != 0) {
		return fmt.Errorf("%w: v1 upstream authority", ErrInvalid)
	}
	if schema == RootSchemaV2 && (value.Upstream == nil ||
		downstreamauthority.RequireUsable(*value.Upstream) != nil || value.Upstream.Repository != value.Repository ||
		!validDigest(value.ServiceStateSummaryDigest) || value.ServiceStateControlRevision == 0) {
		return fmt.Errorf("%w: v2 upstream authority", ErrInvalid)
	}
	want, err := digestValue(FrozenPolicy())
	if err != nil || want != value.PolicyDigest {
		return fmt.Errorf("%w: policy authority", ErrInvalid)
	}
	return nil
}

func validatePlacement(value Placement) error {
	if repopath.Validate(value.Path) != nil || len(value.Claims) > MaxClaimsPerPlacement {
		return fmt.Errorf("%w: placement", ErrInvalid)
	}
	prior := ""
	for _, claim := range value.Claims {
		if !validText(claim.ServiceKey) || !validDisposition(claim.Disposition) ||
			claim.ServiceKey <= prior || len(claim.Roles) == 0 || len(claim.Roles) > MaxRolesPerClaim {
			return fmt.Errorf("%w: placement claim", ErrInvalid)
		}
		rolePrior := ""
		for _, role := range claim.Roles {
			key := role.Role + "\x00" + role.Origin
			if !validRole(role.Role) || !validOrigin(role.Origin) || key <= rolePrior {
				return fmt.Errorf("%w: role claim", ErrInvalid)
			}
			rolePrior = key
		}
		prior = claim.ServiceKey
	}
	if !value.Unowned && len(value.Claims) == 0 {
		return fmt.Errorf("%w: unclassified placement", ErrInvalid)
	}
	return nil
}

func validateProjection(value Projection) error {
	if value.Schema != ProjectionSchema || (value.Kind != "rpc" && value.Kind != "kafka") ||
		!validDigest(value.PostingDigest) || !validText(value.Class) || !validText(value.Plane) ||
		!optionalText(value.LookupKey) || validatePlacement(value.Source) != nil {
		return fmt.Errorf("%w: projection", ErrInvalid)
	}
	if value.Target != nil && validatePlacement(*value.Target) != nil {
		return fmt.Errorf("%w: target projection", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: projection digest", ErrInvalid)
	}
	return nil
}

func validateServiceReference(value ServiceReference) error {
	if value.Schema != ServiceReferenceSchema || !validDigest(value.ProjectionDigest) ||
		!validDigest(value.PostingDigest) || (value.Kind != "rpc" && value.Kind != "kafka") ||
		!validText(value.Plane) || !optionalText(value.LookupKey) || len(value.Participation) < 1 ||
		len(value.Participation) > 2 {
		return fmt.Errorf("%w: service reference", ErrInvalid)
	}
	for index, item := range value.Participation {
		if (item != "source" && item != "target") || index > 0 && value.Participation[index-1] >= item {
			return fmt.Errorf("%w: service participation", ErrInvalid)
		}
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: service reference digest", ErrInvalid)
	}
	return nil
}

func validDisposition(value string) bool {
	return value == "accepted" || value == "proposal" || value == "conflict" || value == "rejected"
}

func validRole(value string) bool {
	return value == "primary" || value == "supporting" || value == "typed" ||
		value == "shared" || value == "generated"
}

func validOrigin(value string) bool { return value == "base" || value == "override" }

func validText(value string) bool {
	return value != "" && len(value) <= MaxTextBytes && validUTF8(value)
}

func optionalText(value string) bool {
	return value == "" || len(value) <= MaxTextBytes && validUTF8(value)
}

func validUTF8(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func digestValue(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func cloneProjection(value Projection) Projection {
	value.Source = clonePlacement(value.Source)
	if value.Target != nil {
		target := clonePlacement(*value.Target)
		value.Target = &target
	}
	return value
}

func clonePlacement(value Placement) Placement {
	value.Claims = slices.Clone(value.Claims)
	for index := range value.Claims {
		value.Claims[index].Roles = slices.Clone(value.Claims[index].Roles)
	}
	return value
}
