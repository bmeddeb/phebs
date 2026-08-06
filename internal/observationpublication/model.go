// Package observationpublication publishes immutable, content-addressed
// source observations under one atomic repository current pointer.
package observationpublication

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

const (
	ManifestSchema = "phebs-observation-publication-v1"
	MemberSchema   = "phebs-observation-member-v1"
	RecordSchema   = "phebs-observation-record-v1"
	PointerSchema  = "phebs-observation-pointer-v1"
	MarkerSchema   = "phebs-observation-marker-v1"
	ReceiptSchema  = "phebs-observation-operation-receipt-v1"
	BindingSchema  = "phebs-observation-schedule-binding-v1"

	LanguagePack               = "go"
	MaxManifestBytes           = 8 << 20
	MaxMemberBytes       int64 = 128 << 20
	MaxObservationBytes        = 64 << 20
	MaxGenerationBytes   int64 = 20 << 30
	MaxGenerationRecords       = 250_000
	MaxArtifactNameBytes       = 255
)

var (
	ErrInvalid  = errors.New("invalid observation publication")
	ErrLimit    = errors.New("observation publication limit exceeded")
	ErrStale    = errors.New("observation publication is stale")
	ErrPinned   = errors.New("observation publication is pinned")
	ErrComplete = errors.New("observation publication is already complete")
)

type Record struct {
	Schema            string                      `json:"schema"`
	ObjectID          string                      `json:"object_id"`
	DeclaredBytes     int64                       `json:"declared_bytes"`
	Placements        []sourcepartition.Placement `json:"placements"`
	State             string                      `json:"state"` // observed | unsupported
	Reason            string                      `json:"reason,omitempty"`
	ContentDigest     string                      `json:"content_digest"`
	ObservationDigest string                      `json:"observation_digest,omitempty"`
	ObservationName   string                      `json:"observation_name,omitempty"`
	ObservationOrigin string                      `json:"observation_origin,omitempty"` // parsed | reused
}

// ReasonCount is one closed unsupported-source classification. It contains no
// path, object identity, source sample, or parser error text.
type ReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// OperationReceipt is the source-free, digest-bound accounting for one
// complete publication. SourceBlobReads counts inputs incorporated into
// successfully published members; failed or interrupted attempts are outside
// this immutable publication receipt and remain scheduler diagnostics.
type OperationReceipt struct {
	Schema             string        `json:"schema"`
	InputBlobs         int           `json:"input_blobs"`
	SourceBlobReads    int           `json:"source_blob_reads"`
	ParsedObservations int           `json:"parsed_observations"`
	ReusedObservations int           `json:"reused_observations"`
	ObservedBlobs      int           `json:"observed_blobs"`
	UnsupportedBlobs   int           `json:"unsupported_blobs"`
	UnsupportedReasons []ReasonCount `json:"unsupported_reasons"`
}

type Member struct {
	Ordinal            int    `json:"ordinal"`
	Count              int    `json:"count"`
	Name               string `json:"name"`
	SourceMemberDigest string `json:"source_member_digest"`
	RecordCount        int    `json:"record_count"`
	ObservedCount      int    `json:"observed_count"`
	UnsupportedCount   int    `json:"unsupported_count"`
	ContentBytes       int64  `json:"content_bytes"`
	Digest             string `json:"digest"`
}

type Manifest struct {
	Schema                    string            `json:"schema"`
	Repository                string            `json:"repository"`
	Language                  string            `json:"language"`
	SourceGenerationDigest    string            `json:"source_generation_digest"`
	PartitionGenerationDigest string            `json:"partition_generation_digest"`
	PartitionManifestDigest   string            `json:"partition_manifest_digest"`
	PartitionPolicyDigest     string            `json:"partition_policy_digest"`
	ObservationPolicyDigest   string            `json:"observation_policy_digest"`
	GenerationDigest          string            `json:"generation_digest"`
	Members                   []Member          `json:"members"`
	RecordCount               int               `json:"record_count"`
	ObservedCount             int               `json:"observed_count"`
	UnsupportedCount          int               `json:"unsupported_count"`
	EncodedMemberBytes        int64             `json:"encoded_member_bytes"`
	ObservationBytes          int64             `json:"observation_bytes"`
	OperationReceipt          *OperationReceipt `json:"operation_receipt,omitempty"`
	Digest                    string            `json:"digest"`
}

type Pointer struct {
	Schema           string `json:"schema"`
	Repository       string `json:"repository"`
	GenerationDigest string `json:"generation_digest"`
	ManifestDigest   string `json:"manifest_digest"`
	ManifestName     string `json:"manifest_name"`
}

type Marker struct {
	Schema           string `json:"schema"`
	Repository       string `json:"repository"`
	GenerationDigest string `json:"generation_digest"`
	ManifestDigest   string `json:"manifest_digest,omitempty"`
}

type Metrics struct {
	ReadBlobs          int
	ParsedBlobs        int
	ReusedObservations int
	UnsupportedBlobs   int
	WrittenBytes       int64
}

type scheduleBinding struct {
	Schema                  string `json:"schema"`
	Repository              string `json:"repository"`
	ScheduleGeneration      string `json:"schedule_generation"`
	PublicationGeneration   string `json:"publication_generation"`
	PriorScheduleDigest     string `json:"prior_schedule_digest,omitempty"`
	SourceGenerationDigest  string `json:"source_generation_digest"`
	PartitionManifestDigest string `json:"partition_manifest_digest"`
}

func GenerationDigest(partition sourcepartition.Manifest) (string, error) {
	if err := sourcepartition.ValidateManifest(partition); err != nil {
		return "", err
	}
	return digest("phebs-observation-generation-v1", strings.Join([]string{
		partition.Repository, partition.SourceGenerationDigest,
		partition.GenerationDigest, partition.Digest, partition.PolicyDigest,
		sourceobservation.PolicyDigest(), LanguagePack,
	}, "\x00")), nil
}

func ManifestDigest(value Manifest) (string, error) {
	value.Digest = ""
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest("phebs-observation-manifest-v1", string(raw)), nil
}

// ValidateManifest exposes the closed publication-control validator to later
// immutable consumers without granting filesystem or mutation capabilities.
func ValidateManifest(value Manifest) error { return validateManifest(value) }

func repositoryDirectory(root, repository string) string {
	return filepath.Join(root, repositoryHash(repository))
}

func generationDirectory(root, repository, generation string) string {
	return filepath.Join(repositoryDirectory(root, repository), strings.TrimPrefix(generation, "sha256:"))
}

func pointerPath(root, repository string) string {
	return filepath.Join(repositoryDirectory(root, repository), "current.json")
}

func markerPath(root, repository string) string {
	return filepath.Join(repositoryDirectory(root, repository), "publishing.json")
}

func manifestName() string { return "manifest.json" }

func memberName(ordinal int) string { return fmt.Sprintf("member-%05d.jsonl", ordinal) }

func observationName(policyDigest, contentDigest string) string {
	return filepath.Join("objects",
		strings.TrimPrefix(policyDigest, "sha256:")[:16]+"-"+
			strings.TrimPrefix(contentDigest, "sha256:")+".json")
}

func repositoryHash(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return hex.EncodeToString(sum[:])
}

func digest(domain, value string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(value))
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalid, message) }

func validateRepository(repository string) error {
	if err := reponame.Validate(repository); err != nil {
		return invalid("repository identity")
	}
	return nil
}
