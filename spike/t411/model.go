// Package t411 freezes production-aligned logical-service profiles and the
// source-free capacity decision owned by T41.1. Production packages must not
// import spike packages.
package t411

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
	EnvelopeSchema   = "t411-service-load-envelope-v1"
	ProfileSchema    = "t411-service-load-profile-v1"
	TransitionSchema = "t411-service-transition-profile-v1"
	ReceiptSchema    = "t411-service-load-receipt-v1"

	AcceptedServiceFloor  = 8_000
	AcceptedServiceTarget = 10_000
	MaxTotalServices      = 12_500
	MaxMemberships        = 75_000
	MaxDistinctPaths      = 40_000
	MaxSuccessorEdges     = 12_500
	MaxServiceSuccessors  = 512
	MaxLogicalBytes       = 16 << 20
	MaxPublicationBytes   = 32 << 20
	MaxClaimsPerPlacement = 4_000
	MaxClaimsPerBucket    = 512

	MaxServicesPerMember  = 512
	MaxPathsPerMember     = 2_048
	MaxCatalogMembers     = 64
	MaxCatalogRootBytes   = 256 << 10
	MaxCatalogMemberBytes = 2 << 20
)

type Envelope struct {
	Schema     string            `json:"schema"`
	Profiles   []Profile         `json:"profiles"`
	Transition TransitionProfile `json:"transition_profile"`
	Boundary   BoundaryProfile   `json:"boundary_profile"`
	Claims     Claims            `json:"claims"`
}

type Profile struct {
	Schema                     string                 `json:"schema"`
	Name                       string                 `json:"name"`
	Seed                       string                 `json:"seed"`
	Authority                  AuthorityRule          `json:"authority"`
	AcceptedServices           int                    `json:"accepted_services"`
	TotalServiceRecords        int                    `json:"total_service_records"`
	Memberships                int                    `json:"memberships"`
	DistinctPaths              int                    `json:"distinct_paths"`
	UnownedPaths               int                    `json:"unowned_paths"`
	RoleMemberships            []Count                `json:"role_memberships"`
	MaxAcceptedPathFanout      int                    `json:"max_accepted_path_fanout"`
	MaxTotalClaimsPerPlacement int                    `json:"max_total_claims_per_placement"`
	LogicalCatalog             ArtifactIdentity       `json:"logical_catalog"`
	Fixture                    FixtureIdentity        `json:"fixture"`
	Publication                PublicationProjection  `json:"publication_projection"`
	Relationships              []RelationshipIdentity `json:"relationship_distributions"`
	Claims                     Claims                 `json:"claims"`
}

type AuthorityRule struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Version  string `json:"version"`
	Explicit bool   `json:"explicit"`
	Inferred bool   `json:"inferred"`
}

type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type ArtifactIdentity struct {
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type FixtureIdentity struct {
	Algorithm        string `json:"algorithm"`
	RegularFiles     int    `json:"regular_files"`
	DistinctContents int    `json:"distinct_contents"`
	ContentBytes     int64  `json:"content_bytes"`
	SHA256           string `json:"sha256"`
}

type PublicationProjection struct {
	PolicyDigest            string           `json:"policy_digest"`
	Root                    ArtifactIdentity `json:"root"`
	ServiceMembers          int              `json:"service_members"`
	ServiceMemberBytes      int              `json:"service_member_bytes"`
	MaxServiceMemberBytes   int              `json:"max_service_member_bytes"`
	PlacementMembers        int              `json:"placement_members"`
	PlacementMemberBytes    int              `json:"placement_member_bytes"`
	MaxPlacementMemberBytes int              `json:"max_placement_member_bytes"`
	TotalMembers            int              `json:"total_members"`
	EncodedBytes            int              `json:"encoded_bytes"`
	MemberSetSHA256         string           `json:"member_set_sha256"`
}

type RelationshipIdentity struct {
	Name   string `json:"name"`
	Edges  int    `json:"edges"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type TransitionProfile struct {
	Schema    string               `json:"schema"`
	Name      string               `json:"name"`
	Revisions []TransitionRevision `json:"revisions"`
	Cases     []TransitionCase     `json:"cases"`
	Claims    Claims               `json:"claims"`
}

type TransitionRevision struct {
	Name    string                 `json:"name"`
	Catalog servicecatalog.Catalog `json:"catalog"`
}

type TransitionCase struct {
	Name                string   `json:"name"`
	ServiceKey          string   `json:"service_key"`
	From                string   `json:"from"`
	To                  string   `json:"to"`
	Successors          []string `json:"successors,omitempty"`
	ExpectedIncarnation uint64   `json:"expected_incarnation,omitempty"`
}

type BoundaryProfile struct {
	MaxService   MaxServiceBoundary   `json:"max_service"`
	MaxPlacement MaxPlacementBoundary `json:"max_placement"`
}

type MaxServiceBoundary struct {
	ServiceKeyBytes  int              `json:"service_key_bytes"`
	DisplayNameBytes int              `json:"display_name_bytes"`
	ReasonBytes      int              `json:"reason_bytes"`
	DistinctPaths    int              `json:"distinct_paths"`
	PathBytes        int              `json:"path_bytes"`
	Successors       int              `json:"successors"`
	Member           ArtifactIdentity `json:"member"`
}

type MaxPlacementBoundary struct {
	PathBytes                   int              `json:"path_bytes"`
	Claims                      int              `json:"claims"`
	RolesPerClaim               int              `json:"roles_per_claim"`
	CatalogMember               ArtifactIdentity `json:"catalog_member"`
	UnbucketedRelationshipBytes int              `json:"unbucketed_relationship_bytes"`
	ClaimsPerRelationshipBucket int              `json:"claims_per_relationship_bucket"`
	RelationshipBuckets         int              `json:"relationship_buckets"`
	MaxRelationshipBucketBytes  int              `json:"max_relationship_bucket_bytes"`
}

type Receipt struct {
	Schema      string               `json:"schema"`
	MeasuredOn  string               `json:"measured_on"`
	Inputs      InputIdentity        `json:"inputs"`
	Environment Environment          `json:"environment"`
	Envelope    ArtifactIdentity     `json:"envelope"`
	Profiles    []ProfileMeasurement `json:"profiles"`
	Boundary    BoundaryProfile      `json:"boundary_profile"`
	Decision    CapDecision          `json:"decision"`
	Claims      Claims               `json:"claims"`
}

type InputIdentity struct {
	T323ReceiptSHA256 string `json:"t323_receipt_sha256"`
	T323BundleSHA256  string `json:"t323_bundle_sha256"`
}

type Environment struct {
	GOOS                string `json:"goos"`
	GOARCH              string `json:"goarch"`
	GoVersion           string `json:"go_version"`
	LogicalCPUs         int    `json:"logical_cpus"`
	ProcessPeakRSSBytes int64  `json:"process_peak_rss_bytes"`
	RSSMethod           string `json:"rss_method"`
}

type ProfileMeasurement struct {
	Name             string                `json:"name"`
	ProfileDigest    string                `json:"profile_digest"`
	WallMicros       int64                 `json:"wall_micros"`
	GoAllocatedBytes uint64                `json:"go_allocated_bytes"`
	Serialization    []ByteMetric          `json:"serialization"`
	Projection       PublicationProjection `json:"projection"`
	StoreTransaction StoreEstimate         `json:"store_transaction_estimate"`
	Filesystem       FilesystemEstimate    `json:"filesystem_estimate"`
	Lifecycle        LifecycleEstimate     `json:"lifecycle_estimate"`
}

type ByteMetric struct {
	Kind        string `json:"kind"`
	Measurement string `json:"measurement"`
	Bytes       int64  `json:"bytes"`
}

type StoreEstimate struct {
	ImmutableRows    int `json:"immutable_rows"`
	LargestRowBytes  int `json:"largest_row_bytes"`
	PointerSwapRows  int `json:"pointer_swap_rows"`
	PointerSwapBytes int `json:"pointer_swap_bytes"`
}

type FilesystemEstimate struct {
	RegularFiles int   `json:"regular_files"`
	LogicalBytes int64 `json:"logical_bytes"`
}

type LifecycleEstimate struct {
	RootRows     int `json:"root_rows"`
	MemberRows   int `json:"member_rows"`
	CollectRows  int `json:"collect_rows"`
	CollectBytes int `json:"collect_bytes"`
}

type CapDecision struct {
	AcceptedFloor              int    `json:"accepted_floor"`
	AcceptedTarget             int    `json:"accepted_target"`
	MaxTotalServiceRecords     int    `json:"max_total_service_records"`
	MaxMemberships             int    `json:"max_memberships"`
	MaxDistinctPaths           int    `json:"max_distinct_paths"`
	MaxSuccessorEdges          int    `json:"max_successor_edges"`
	MaxServiceSuccessors       int    `json:"max_service_successors"`
	MaxLogicalBytes            int    `json:"max_logical_bytes"`
	MaxPublicationBytes        int    `json:"max_publication_bytes"`
	MaxClaimsPerPlacement      int    `json:"max_claims_per_placement"`
	MaxClaimsPerBucket         int    `json:"max_claims_per_bucket"`
	RelationshipRepresentation string `json:"relationship_representation"`
	HardPreGrowthRefusal       bool   `json:"hard_pre_growth_refusal"`
	ChangesProductionCaps      bool   `json:"changes_production_caps"`
}

type Claims struct {
	Synthetic                 bool `json:"synthetic"`
	SourceFree                bool `json:"source_free"`
	ExplicitAuthority         bool `json:"explicit_authority"`
	EstablishesTargetSLO      bool `json:"establishes_target_slo"`
	EstablishesAccuracy       bool `json:"establishes_accuracy"`
	QualifiesSupportedScale   bool `json:"qualifies_supported_scale"`
	ChangesProductionBehavior bool `json:"changes_production_behavior"`
	AuthorizesRelease         bool `json:"authorizes_release"`
}

func MarshalCanonical(value any) ([]byte, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func DecodeStrict[T any](data []byte) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return value, err
	}
	return value, nil
}

func SHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

func CheckLimit(name string, observed int) error {
	limits := map[string]int{
		"total_service_records": MaxTotalServices,
		"memberships":           MaxMemberships,
		"distinct_paths":        MaxDistinctPaths,
		"successor_edges":       MaxSuccessorEdges,
		"service_successors":    MaxServiceSuccessors,
		"logical_bytes":         MaxLogicalBytes,
		"publication_bytes":     MaxPublicationBytes,
		"claims_per_placement":  MaxClaimsPerPlacement,
		"claims_per_bucket":     MaxClaimsPerBucket,
	}
	limit, ok := limits[name]
	if !ok {
		return fmt.Errorf("unknown T41.1 limit %q", name)
	}
	if observed < 0 || observed > limit {
		return fmt.Errorf("T41.1 %s observed=%d limit=%d", name, observed, limit)
	}
	return nil
}
