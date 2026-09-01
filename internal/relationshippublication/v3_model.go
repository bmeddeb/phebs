package relationshippublication

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

const (
	RootSchemaV3             = "phebs-relationship-root-v3-shadow"
	RepositoryMemberSchemaV3 = "phebs-relationship-repository-member-v3-shadow"
	ProjectionBucketSchemaV3 = "phebs-relationship-projection-bucket-v3-shadow"
	ServiceMemberSchemaV3    = "phebs-service-relationship-range-v3-shadow"
	ServiceRecordSchemaV3    = "phebs-service-relationship-record-v3-shadow"
	PolicySchemaV3           = "phebs-relationship-policy-v3-shadow"
	PointerSchemaV3          = "phebs-relationship-pointer-v3-shadow"
	MarkerSchemaV3           = "phebs-relationship-marker-v3-shadow"

	MaxServicesV3                    = servicecatalogv3.MaxTotalServices
	MaxClaimsPerProjectionBucketV3   = 512
	MaxProjectionBucketsV3           = 8
	MaxServicesPerServiceMemberV3    = 512
	MaxServiceMembersV3              = 512
	MaxProjectionBucketBytesV3       = MaxProjectionBytes
	MaxRootBytesV3                   = 256 << 10
	MaxGenerationFilesV3             = 1 + RepositoryBuckets + MaxServiceMembersV3
	RelationshipPublicationsV3Shadow = "relationship-publications-v3-shadow"
)

// PolicyV3 freezes every admission and wire bound used by the dark v3 layout.
type PolicyV3 struct {
	Schema                       string `json:"schema"`
	RepositoryBuckets            int    `json:"repository_buckets"`
	MaxServices                  int    `json:"max_services"`
	MaxProjectionRecords         int    `json:"max_projection_records"`
	MaxServiceReferences         int    `json:"max_service_references"`
	MaxTotalServiceReferences    int    `json:"max_total_service_references"`
	MaxClaimsPerPlacement        int    `json:"max_claims_per_placement"`
	MaxClaimsPerProjectionBucket int    `json:"max_claims_per_projection_bucket"`
	MaxProjectionBuckets         int    `json:"max_projection_buckets"`
	MaxServicesPerServiceMember  int    `json:"max_services_per_service_member"`
	MaxServiceMembers            int    `json:"max_service_members"`
	MaxRolesPerClaim             int    `json:"max_roles_per_claim"`
	MaxTextBytes                 int    `json:"max_text_bytes"`
	MaxProjectionBucketBytes     int    `json:"max_projection_bucket_bytes"`
	MaxReferenceBytes            int    `json:"max_reference_bytes"`
	MaxRepositoryMemberBytes     int    `json:"max_repository_member_bytes"`
	MaxServiceMemberBytes        int    `json:"max_service_member_bytes"`
	MaxRootBytes                 int    `json:"max_root_bytes"`
	MaxGenerationBytes           int64  `json:"max_generation_bytes"`
	MaxResidentChargeBytes       int64  `json:"max_resident_charge_bytes"`
	MaxGenerationFiles           int    `json:"max_generation_files"`
	PlacementRule                string `json:"placement_rule"`
}

func FrozenPolicyV3() PolicyV3 {
	return PolicyV3{
		Schema: PolicySchemaV3, RepositoryBuckets: RepositoryBuckets,
		MaxServices: MaxServicesV3, MaxProjectionRecords: MaxProjectionRecords,
		MaxServiceReferences:         MaxServiceReferences,
		MaxTotalServiceReferences:    MaxTotalServiceReferences,
		MaxClaimsPerPlacement:        MaxClaimsPerPlacement,
		MaxClaimsPerProjectionBucket: MaxClaimsPerProjectionBucketV3,
		MaxProjectionBuckets:         MaxProjectionBucketsV3,
		MaxServicesPerServiceMember:  MaxServicesPerServiceMemberV3,
		MaxServiceMembers:            MaxServiceMembersV3, MaxRolesPerClaim: MaxRolesPerClaim,
		MaxTextBytes: MaxTextBytes, MaxProjectionBucketBytes: MaxProjectionBucketBytesV3,
		MaxReferenceBytes:        MaxReferenceBytes,
		MaxRepositoryMemberBytes: MaxRepositoryMemberBytes,
		MaxServiceMemberBytes:    MaxServiceMemberBytes, MaxRootBytes: MaxRootBytesV3,
		MaxGenerationBytes: MaxGenerationBytes, MaxResidentChargeBytes: MaxResidentChargeBytes,
		MaxGenerationFiles: MaxGenerationFilesV3,
		PlacementRule:      "exact-prefix-membership-and-aligned-512-claim-buckets-v1",
	}
}

// AuthorityV3 binds the complete catalog/state identity and embeds the full
// validated upstream authority so recovery can reconstruct every exact run pin.
type AuthorityV3 struct {
	Repository                  string                        `json:"repository"`
	CatalogRootDigest           string                        `json:"catalog_root_digest"`
	CatalogLogicalDigest        string                        `json:"catalog_logical_digest"`
	CatalogSourceGeneration     string                        `json:"catalog_source_generation"`
	CatalogControlRevision      uint64                        `json:"catalog_control_revision"`
	ServiceStateSetDigest       string                        `json:"service_state_set_digest"`
	ServiceStateSummaryDigest   string                        `json:"service_state_summary_digest"`
	ServiceStateControlRevision uint64                        `json:"service_state_control_revision"`
	ObservationGenerationDigest string                        `json:"observation_generation_digest"`
	ObservationManifestDigest   string                        `json:"observation_manifest_digest"`
	ObservationSourceDigest     string                        `json:"observation_source_digest"`
	ResolverGenerationDigest    string                        `json:"resolver_generation_digest"`
	ResolverRootDigest          string                        `json:"resolver_root_digest"`
	RPCGenerationDigest         string                        `json:"rpc_generation_digest"`
	RPCRootDigest               string                        `json:"rpc_root_digest"`
	KafkaGenerationDigest       string                        `json:"kafka_generation_digest"`
	KafkaRootDigest             string                        `json:"kafka_root_digest"`
	Upstream                    downstreamauthority.Authority `json:"upstream"`
	UpstreamDigest              string                        `json:"upstream_digest"`
	PolicyDigest                string                        `json:"policy_digest"`
}

// ProjectionBucketV3 is one aligned claim fragment. ProjectionDigest is the
// digest of the reconstructed v1 semantic Projection, not merely this bucket.
type ProjectionBucketV3 struct {
	Schema           string     `json:"schema"`
	ProjectionDigest string     `json:"projection_digest"`
	Kind             string     `json:"kind"`
	PostingDigest    string     `json:"posting_digest"`
	Class            string     `json:"class"`
	Plane            string     `json:"plane"`
	LookupKey        string     `json:"lookup_key,omitempty"`
	Ordinal          int        `json:"ordinal"`
	Count            int        `json:"count"`
	Source           Placement  `json:"source"`
	Target           *Placement `json:"target,omitempty"`
	Digest           string     `json:"digest"`
}

type RepositoryMemberV3 struct {
	Schema    string               `json:"schema"`
	Bucket    int                  `json:"bucket"`
	Fragments []ProjectionBucketV3 `json:"fragments"`
	Digest    string               `json:"digest"`
}

type ServiceRecordV3 struct {
	Schema            string             `json:"schema"`
	ServiceKey        string             `json:"service_key"`
	Incarnation       uint64             `json:"incarnation"`
	ServiceGeneration string             `json:"service_generation"`
	State             string             `json:"state"`
	Reason            string             `json:"reason,omitempty"`
	References        []ServiceReference `json:"references"`
	Digest            string             `json:"digest"`
}

type ServiceMemberV3 struct {
	Schema   string            `json:"schema"`
	Ordinal  int               `json:"ordinal"`
	Count    int               `json:"count"`
	FirstKey string            `json:"first_key"`
	LastKey  string            `json:"last_key"`
	Services []ServiceRecordV3 `json:"services"`
	Digest   string            `json:"digest"`
}

type RepositoryReceiptV3 struct {
	Bucket          int    `json:"bucket"`
	Name            string `json:"name"`
	ProjectionCount int    `json:"projection_count"`
	FragmentCount   int    `json:"fragment_count"`
	ContentBytes    int64  `json:"content_bytes"`
	ContentDigest   string `json:"content_digest"`
}

type ServiceRangeReceiptV3 struct {
	Ordinal        int    `json:"ordinal"`
	Count          int    `json:"count"`
	FirstKey       string `json:"first_key"`
	LastKey        string `json:"last_key"`
	ServiceCount   int    `json:"service_count"`
	CompleteCount  int    `json:"complete_count"`
	EmptyCount     int    `json:"empty_count"`
	FailedCount    int    `json:"failed_count"`
	ReferenceCount int    `json:"reference_count"`
	Name           string `json:"name"`
	ContentBytes   int64  `json:"content_bytes"`
	ContentDigest  string `json:"content_digest"`
}

type RootV3 struct {
	Schema                  string                  `json:"schema"`
	Authority               AuthorityV3             `json:"authority"`
	AuthorityDigest         string                  `json:"authority_digest"`
	Policy                  PolicyV3                `json:"policy"`
	RepositoryComplete      bool                    `json:"repository_complete"`
	AllServicesComplete     bool                    `json:"all_services_complete"`
	RepositoryMembers       []RepositoryReceiptV3   `json:"repository_members"`
	ServiceMembers          []ServiceRangeReceiptV3 `json:"service_members"`
	ProjectionCount         int                     `json:"projection_count"`
	ProjectionFragmentCount int                     `json:"projection_fragment_count"`
	ServiceCount            int                     `json:"service_count"`
	CompleteServiceCount    int                     `json:"complete_service_count"`
	EmptyServiceCount       int                     `json:"empty_service_count"`
	FailedServiceCount      int                     `json:"failed_service_count"`
	ServiceReferenceCount   int                     `json:"service_reference_count"`
	EncodedRepositoryBytes  int64                   `json:"encoded_repository_bytes"`
	EncodedServiceBytes     int64                   `json:"encoded_service_bytes"`
	GenerationDigest        string                  `json:"generation_digest"`
	Digest                  string                  `json:"digest"`
}

type PointerV3 struct {
	Schema           string `json:"schema"`
	Repository       string `json:"repository"`
	GenerationDigest string `json:"generation_digest"`
	RootDigest       string `json:"root_digest"`
	RootName         string `json:"root_name"`
	Digest           string `json:"digest"`
}

type MarkerV3 struct {
	Schema    string    `json:"schema"`
	Pointer   PointerV3 `json:"pointer"`
	StageName string    `json:"stage_name,omitempty"`
	Digest    string    `json:"digest"`
}

type serviceSetIdentityV3 struct {
	ServiceKey        string `json:"service_key"`
	Incarnation       uint64 `json:"incarnation"`
	DesiredGeneration string `json:"desired_generation"`
}

func validateAuthorityV3(value AuthorityV3) error {
	if reponame.Validate(value.Repository) != nil || value.CatalogControlRevision == 0 ||
		value.ServiceStateControlRevision == 0 {
		return fmt.Errorf("%w: v3 authority", ErrInvalid)
	}
	digests := [...]string{
		value.CatalogRootDigest, value.CatalogLogicalDigest, value.CatalogSourceGeneration,
		value.ServiceStateSetDigest, value.ServiceStateSummaryDigest,
		value.ObservationGenerationDigest, value.ObservationManifestDigest,
		value.ObservationSourceDigest, value.ResolverGenerationDigest, value.ResolverRootDigest,
		value.RPCGenerationDigest, value.RPCRootDigest, value.KafkaGenerationDigest,
		value.KafkaRootDigest, value.UpstreamDigest, value.PolicyDigest,
	}
	if downstreamauthority.RequireUsable(value.Upstream) != nil ||
		value.Upstream.Repository != value.Repository || value.Upstream.Digest != value.UpstreamDigest {
		return fmt.Errorf("%w: v3 upstream authority", ErrInvalid)
	}
	for _, digest := range digests {
		if !validDigest(digest) {
			return fmt.Errorf("%w: v3 authority digest", ErrInvalid)
		}
	}
	want, err := digestValue(FrozenPolicyV3())
	if err != nil || want != value.PolicyDigest {
		return fmt.Errorf("%w: v3 policy authority", ErrInvalid)
	}
	return nil
}

func validateProjectionBucketV3(value ProjectionBucketV3) error {
	if value.Schema != ProjectionBucketSchemaV3 || !validDigest(value.ProjectionDigest) ||
		(value.Kind != "rpc" && value.Kind != "kafka") || !validDigest(value.PostingDigest) ||
		!validText(value.Class) || !validText(value.Plane) || !optionalText(value.LookupKey) ||
		value.Ordinal < 0 || value.Count < 1 || value.Count > MaxProjectionBucketsV3 ||
		value.Ordinal >= value.Count || validatePlacementBucketV3(value.Source) != nil {
		return fmt.Errorf("%w: projection bucket", ErrInvalid)
	}
	if value.Target != nil && validatePlacementBucketV3(*value.Target) != nil {
		return fmt.Errorf("%w: projection target bucket", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	want, err := digestValue(copyValue)
	if err != nil || want != value.Digest {
		return fmt.Errorf("%w: projection bucket digest", ErrInvalid)
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxProjectionBucketBytesV3 {
		return fmt.Errorf("%w: projection bucket bytes", ErrInvalid)
	}
	return nil
}

func validatePlacementBucketV3(value Placement) error {
	if repopath.Validate(value.Path) != nil || len(value.Claims) > MaxClaimsPerProjectionBucketV3 {
		return fmt.Errorf("%w: placement bucket", ErrInvalid)
	}
	prior := ""
	for _, claim := range value.Claims {
		if !validText(claim.ServiceKey) || !validDisposition(claim.Disposition) ||
			claim.ServiceKey <= prior || len(claim.Roles) < 1 || len(claim.Roles) > MaxRolesPerClaim {
			return fmt.Errorf("%w: placement bucket claim", ErrInvalid)
		}
		rolePrior := ""
		for _, role := range claim.Roles {
			key := role.Role + "\x00" + role.Origin
			if !validRole(role.Role) || !validOrigin(role.Origin) || key <= rolePrior {
				return fmt.Errorf("%w: placement bucket role", ErrInvalid)
			}
			rolePrior = key
		}
		prior = claim.ServiceKey
	}
	return nil
}

func validateRepositoryMemberV3(value RepositoryMemberV3) error {
	if value.Schema != RepositoryMemberSchemaV3 || value.Bucket < 0 ||
		value.Bucket >= RepositoryBuckets || len(value.Fragments) < 1 {
		return fmt.Errorf("%w: v3 repository member", ErrInvalid)
	}
	for index := 0; index < len(value.Fragments); {
		fragment := value.Fragments[index]
		if validateProjectionBucketV3(fragment) != nil ||
			projectionBucket(fragment.ProjectionDigest) != value.Bucket || fragment.Ordinal != 0 {
			return fmt.Errorf("%w: v3 repository fragment", ErrInvalid)
		}
		end := index + fragment.Count
		if end > len(value.Fragments) {
			return fmt.Errorf("%w: v3 repository fragment count", ErrInvalid)
		}
		group := value.Fragments[index:end]
		for ordinal, item := range group {
			if validateProjectionBucketV3(item) != nil || item.ProjectionDigest != fragment.ProjectionDigest ||
				item.Ordinal != ordinal || !equalProjectionBucketHeaderV3(fragment, item) {
				return fmt.Errorf("%w: v3 projection fragment sequence", ErrInvalid)
			}
		}
		if index > 0 && value.Fragments[index-1].ProjectionDigest >= fragment.ProjectionDigest {
			return fmt.Errorf("%w: v3 projection order", ErrInvalid)
		}
		projection, err := flattenProjectionBucketsV3(group)
		if err != nil || projection.Digest != fragment.ProjectionDigest ||
			projectionBucket(projection.Digest) != value.Bucket {
			return fmt.Errorf("%w: v3 projection reconstruction", ErrInvalid)
		}
		index = end
	}
	copyValue := value
	copyValue.Digest = ""
	want, err := digestValue(copyValue)
	if err != nil || want != value.Digest {
		return fmt.Errorf("%w: v3 repository member digest", ErrInvalid)
	}
	return nil
}

func equalProjectionBucketHeaderV3(left, right ProjectionBucketV3) bool {
	return left.Schema == right.Schema && left.ProjectionDigest == right.ProjectionDigest &&
		left.Kind == right.Kind && left.PostingDigest == right.PostingDigest &&
		left.Class == right.Class && left.Plane == right.Plane && left.LookupKey == right.LookupKey &&
		left.Count == right.Count && left.Source.Path == right.Source.Path &&
		left.Source.Unowned == right.Source.Unowned && (left.Target == nil) == (right.Target == nil) &&
		(left.Target == nil || left.Target.Path == right.Target.Path && left.Target.Unowned == right.Target.Unowned)
}

func validateServiceRecordV3(value ServiceRecordV3) error {
	if value.Schema != ServiceRecordSchemaV3 || !validText(value.ServiceKey) || value.Incarnation == 0 ||
		!validDigest(value.ServiceGeneration) || len(value.References) > MaxServiceReferences {
		return fmt.Errorf("%w: v3 service record", ErrInvalid)
	}
	switch value.State {
	case "complete":
		if value.Reason != "" || len(value.References) == 0 {
			return fmt.Errorf("%w: v3 complete service", ErrInvalid)
		}
	case "empty":
		if value.Reason != "" || len(value.References) != 0 {
			return fmt.Errorf("%w: v3 empty service", ErrInvalid)
		}
	case "failed":
		if (value.Reason != "reference_limit" && value.Reason != "resident_limit" &&
			value.Reason != "encoded_limit") || len(value.References) != 0 {
			return fmt.Errorf("%w: v3 failed service", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: v3 service state", ErrInvalid)
	}
	prior := ""
	for _, reference := range value.References {
		if validateServiceReference(reference) != nil || reference.Digest <= prior {
			return fmt.Errorf("%w: v3 service reference", ErrInvalid)
		}
		prior = reference.Digest
	}
	copyValue := value
	copyValue.Digest = ""
	want, err := digestValue(copyValue)
	if err != nil || want != value.Digest {
		return fmt.Errorf("%w: v3 service record digest", ErrInvalid)
	}
	return nil
}

func validateServiceMemberV3(value ServiceMemberV3) error {
	if value.Schema != ServiceMemberSchemaV3 || value.Ordinal < 0 || value.Count < 1 ||
		value.Count > MaxServiceMembersV3 || value.Ordinal >= value.Count ||
		len(value.Services) < 1 || len(value.Services) > MaxServicesPerServiceMemberV3 ||
		value.FirstKey != value.Services[0].ServiceKey ||
		value.LastKey != value.Services[len(value.Services)-1].ServiceKey {
		return fmt.Errorf("%w: v3 service member", ErrInvalid)
	}
	prior := ""
	for _, record := range value.Services {
		if validateServiceRecordV3(record) != nil || record.ServiceKey <= prior {
			return fmt.Errorf("%w: v3 service member record", ErrInvalid)
		}
		prior = record.ServiceKey
	}
	copyValue := value
	copyValue.Digest = ""
	want, err := digestValue(copyValue)
	if err != nil || want != value.Digest {
		return fmt.Errorf("%w: v3 service member digest", ErrInvalid)
	}
	return nil
}

func ValidateRootV3(value RootV3) error {
	if value.Schema != RootSchemaV3 || validateAuthorityV3(value.Authority) != nil ||
		value.Policy != FrozenPolicyV3() || !value.RepositoryComplete || !validDigest(value.AuthorityDigest) ||
		len(value.RepositoryMembers) > RepositoryBuckets || len(value.ServiceMembers) > MaxServiceMembersV3 ||
		value.ServiceCount < 0 || value.ServiceCount > MaxServicesV3 ||
		value.CompleteServiceCount < 0 || value.CompleteServiceCount > value.ServiceCount ||
		value.EmptyServiceCount < 0 || value.EmptyServiceCount > value.ServiceCount ||
		value.FailedServiceCount < 0 || value.FailedServiceCount > value.ServiceCount ||
		value.ProjectionCount < 0 || value.ProjectionCount > MaxProjectionRecords ||
		value.ProjectionFragmentCount < value.ProjectionCount ||
		value.ProjectionFragmentCount > value.ProjectionCount*MaxProjectionBucketsV3 ||
		value.ServiceReferenceCount < 0 || value.ServiceReferenceCount > MaxTotalServiceReferences ||
		value.EncodedRepositoryBytes < 0 || value.EncodedServiceBytes < 0 ||
		value.EncodedRepositoryBytes > MaxGenerationBytes ||
		value.EncodedServiceBytes > MaxGenerationBytes-value.EncodedRepositoryBytes ||
		!validDigest(value.GenerationDigest) || !validDigest(value.Digest) {
		return fmt.Errorf("%w: v3 root", ErrInvalid)
	}
	wantAuthority, _ := digestValue(value.Authority)
	if wantAuthority != value.AuthorityDigest ||
		value.CompleteServiceCount+value.EmptyServiceCount+value.FailedServiceCount != value.ServiceCount ||
		value.AllServicesComplete != (value.FailedServiceCount == 0) {
		return fmt.Errorf("%w: v3 root authority totals", ErrInvalid)
	}
	var projections, fragments int
	var repositoryBytes int64
	priorBucket := -1
	for _, receipt := range value.RepositoryMembers {
		if receipt.Bucket <= priorBucket || receipt.Bucket >= RepositoryBuckets ||
			receipt.Name != repositoryMemberName(receipt.Bucket) || receipt.ProjectionCount < 1 ||
			receipt.ProjectionCount > MaxProjectionRecords ||
			receipt.FragmentCount < receipt.ProjectionCount ||
			receipt.FragmentCount > MaxProjectionRecords*MaxProjectionBucketsV3 ||
			receipt.FragmentCount > receipt.ProjectionCount*MaxProjectionBucketsV3 ||
			receipt.ContentBytes < 1 || receipt.ContentBytes > MaxRepositoryMemberBytes ||
			!validDigest(receipt.ContentDigest) {
			return fmt.Errorf("%w: v3 repository receipt", ErrInvalid)
		}
		if receipt.ProjectionCount > value.ProjectionCount-projections ||
			receipt.FragmentCount > value.ProjectionFragmentCount-fragments ||
			receipt.ContentBytes > value.EncodedRepositoryBytes-repositoryBytes {
			return fmt.Errorf("%w: v3 repository receipt totals", ErrInvalid)
		}
		projections += receipt.ProjectionCount
		fragments += receipt.FragmentCount
		repositoryBytes += receipt.ContentBytes
		priorBucket = receipt.Bucket
	}
	if projections != value.ProjectionCount || fragments != value.ProjectionFragmentCount ||
		repositoryBytes != value.EncodedRepositoryBytes {
		return fmt.Errorf("%w: v3 repository totals", ErrInvalid)
	}
	var services, complete, empty, failed, references int
	var serviceBytes int64
	priorKey := ""
	for ordinal, receipt := range value.ServiceMembers {
		if receipt.Ordinal != ordinal || receipt.Count != len(value.ServiceMembers) ||
			receipt.ServiceCount < 1 || receipt.ServiceCount > MaxServicesPerServiceMemberV3 ||
			!validText(receipt.FirstKey) || !validText(receipt.LastKey) ||
			receipt.LastKey < receipt.FirstKey ||
			ordinal > 0 && receipt.FirstKey <= priorKey ||
			receipt.CompleteCount < 0 || receipt.CompleteCount > receipt.ServiceCount ||
			receipt.EmptyCount < 0 || receipt.EmptyCount > receipt.ServiceCount ||
			receipt.FailedCount < 0 || receipt.FailedCount > receipt.ServiceCount ||
			receipt.CompleteCount+receipt.EmptyCount+receipt.FailedCount != receipt.ServiceCount ||
			receipt.ReferenceCount < 0 || receipt.ReferenceCount > MaxTotalServiceReferences ||
			receipt.Name != serviceRangeMemberNameV3(ordinal) || receipt.ContentBytes < 1 ||
			receipt.ContentBytes > MaxServiceMemberBytes || !validDigest(receipt.ContentDigest) {
			return fmt.Errorf("%w: v3 service receipt", ErrInvalid)
		}
		if receipt.ServiceCount > value.ServiceCount-services ||
			receipt.CompleteCount > value.CompleteServiceCount-complete ||
			receipt.EmptyCount > value.EmptyServiceCount-empty ||
			receipt.FailedCount > value.FailedServiceCount-failed ||
			receipt.ReferenceCount > value.ServiceReferenceCount-references ||
			receipt.ContentBytes > value.EncodedServiceBytes-serviceBytes {
			return fmt.Errorf("%w: v3 service receipt totals", ErrInvalid)
		}
		services += receipt.ServiceCount
		complete += receipt.CompleteCount
		empty += receipt.EmptyCount
		failed += receipt.FailedCount
		references += receipt.ReferenceCount
		serviceBytes += receipt.ContentBytes
		priorKey = receipt.LastKey
	}
	if services != value.ServiceCount || complete != value.CompleteServiceCount ||
		empty != value.EmptyServiceCount || failed != value.FailedServiceCount ||
		references != value.ServiceReferenceCount || serviceBytes != value.EncodedServiceBytes {
		return fmt.Errorf("%w: v3 service totals", ErrInvalid)
	}
	wantGeneration, _ := generationDigestV3(value)
	wantRoot, _ := rootDigestV3(value)
	if wantGeneration != value.GenerationDigest || wantRoot != value.Digest {
		return fmt.Errorf("%w: v3 root digest", ErrInvalid)
	}
	return nil
}

func digestServiceSetV3(values []serviceSetIdentityV3) (string, error) {
	return digestValue(struct {
		Schema   string                 `json:"schema"`
		Services []serviceSetIdentityV3 `json:"services"`
	}{Schema: "phebs-relationship-service-state-set-v3-shadow", Services: values})
}

func generationDigestV3(value RootV3) (string, error) {
	value.GenerationDigest = ""
	value.Digest = ""
	return digestValue(value)
}

func rootDigestV3(value RootV3) (string, error) {
	value.Digest = ""
	return digestValue(value)
}

func cloneRootV3(value RootV3) RootV3 {
	value.RepositoryMembers = slices.Clone(value.RepositoryMembers)
	value.ServiceMembers = slices.Clone(value.ServiceMembers)
	value.Authority.Upstream.Required = slices.Clone(value.Authority.Upstream.Required)
	value.Authority.Upstream.Domains = slices.Clone(value.Authority.Upstream.Domains)
	return value
}

func serviceRangeMemberNameV3(ordinal int) string {
	return fmt.Sprintf("service-range-%03d.json", ordinal)
}

func projectionBucketsV3(value Projection) ([]ProjectionBucketV3, int, error) {
	if validateProjection(value) != nil {
		return nil, 0, fmt.Errorf("%w: v3 projection", ErrInvalid)
	}
	targetClaims := 0
	if value.Target != nil {
		targetClaims = len(value.Target.Claims)
	}
	claimCount := max(len(value.Source.Claims), targetClaims)
	count := max(1, (claimCount+MaxClaimsPerProjectionBucketV3-1)/MaxClaimsPerProjectionBucketV3)
	if count > MaxProjectionBucketsV3 {
		return nil, 0, ErrLimit
	}
	result := make([]ProjectionBucketV3, 0, count)
	encodedBytes := 0
	for ordinal := range count {
		fragment := ProjectionBucketV3{
			Schema: ProjectionBucketSchemaV3, ProjectionDigest: value.Digest,
			Kind: value.Kind, PostingDigest: value.PostingDigest, Class: value.Class,
			Plane: value.Plane, LookupKey: value.LookupKey, Ordinal: ordinal, Count: count,
			Source: fragmentPlacementV3(value.Source, ordinal),
		}
		if value.Target != nil {
			target := fragmentPlacementV3(*value.Target, ordinal)
			fragment.Target = &target
		}
		fragment.Digest, _ = digestValue(fragment)
		raw, err := json.Marshal(fragment)
		if err != nil {
			return nil, 0, err
		}
		if len(raw) > MaxProjectionBucketBytesV3 || len(raw) > MaxRepositoryMemberBytes-encodedBytes ||
			validateProjectionBucketV3(fragment) != nil {
			return nil, 0, ErrLimit
		}
		encodedBytes += len(raw)
		result = append(result, fragment)
	}
	return result, encodedBytes, nil
}

func fragmentPlacementV3(value Placement, ordinal int) Placement {
	start := min(ordinal*MaxClaimsPerProjectionBucketV3, len(value.Claims))
	end := min(start+MaxClaimsPerProjectionBucketV3, len(value.Claims))
	result := Placement{Path: value.Path, Unowned: value.Unowned}
	result.Claims = make([]ServiceClaim, end-start)
	for index, claim := range value.Claims[start:end] {
		claim.Roles = slices.Clone(claim.Roles)
		result.Claims[index] = claim
	}
	return result
}

func flattenProjectionBucketsV3(values []ProjectionBucketV3) (Projection, error) {
	if len(values) < 1 || len(values) > MaxProjectionBucketsV3 || values[0].Count != len(values) {
		return Projection{}, fmt.Errorf("%w: v3 projection fragment set", ErrInvalid)
	}
	first := values[0]
	projection := Projection{
		Schema: ProjectionSchema, Kind: first.Kind, PostingDigest: first.PostingDigest,
		Class: first.Class, Plane: first.Plane, LookupKey: first.LookupKey,
		Source: Placement{Path: first.Source.Path, Unowned: first.Source.Unowned},
		Digest: first.ProjectionDigest,
	}
	if first.Target != nil {
		projection.Target = &Placement{Path: first.Target.Path, Unowned: first.Target.Unowned}
	}
	for ordinal, fragment := range values {
		if validateProjectionBucketV3(fragment) != nil || fragment.Ordinal != ordinal ||
			!equalProjectionBucketHeaderV3(first, fragment) {
			return Projection{}, fmt.Errorf("%w: v3 projection fragment", ErrInvalid)
		}
		projection.Source.Claims = append(projection.Source.Claims, clonePlacement(fragment.Source).Claims...)
		if projection.Target != nil {
			projection.Target.Claims = append(
				projection.Target.Claims, clonePlacement(*fragment.Target).Claims...,
			)
		}
	}
	if validateProjection(projection) != nil || projection.Digest != first.ProjectionDigest {
		return Projection{}, fmt.Errorf("%w: v3 semantic projection", ErrInvalid)
	}
	return projection, nil
}
