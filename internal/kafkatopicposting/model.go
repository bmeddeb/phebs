// Package kafkatopicposting publishes source-spelled Kafka producer and
// consumer topic occurrences from validated Go observation publications.
// Topic spellings are source evidence, never broker or runtime identity.
package kafkatopicposting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

const (
	RootSchema    = "phebs-kafka-topic-posting-root-v1"
	RootSchemaV2  = "phebs-kafka-topic-posting-root-v2"
	MemberSchema  = "phebs-kafka-topic-posting-member-v1"
	PostingSchema = "phebs-kafka-topic-posting-v1"
	PolicySchema  = "phebs-kafka-topic-posting-policy-v1"

	PartitionBuckets        = 256
	MaxMembers              = 2 * PartitionBuckets
	MaxPostings             = 1_000_000
	MaxPostingsPerMember    = 65_536
	MaxTextBytes            = 4_096
	MaxTopicBytes           = 249
	MaxPostingBytes         = 1 << 20
	MaxMemberBytes          = 128 << 20
	MaxRootBytes            = 8 << 20
	MaxPostingIdentityBytes = int64(1 << 30)
	MaxGenerationBytes      = int64(20 << 30)
)

const historicalMaxPostingsPerMember = 50_000

var (
	ErrInvalid  = errors.New("invalid Kafka topic posting publication")
	ErrLimit    = errors.New("kafka topic posting bound exceeded")
	ErrNotFound = errors.New("kafka topic posting publication not found")
)

type Policy struct {
	Schema                  string `json:"schema"`
	PartitionBuckets        int    `json:"partition_buckets"`
	MaxMembers              int    `json:"max_members"`
	MaxPostings             int    `json:"max_postings"`
	MaxPostingsPerMember    int    `json:"max_postings_per_member"`
	MaxTextBytes            int    `json:"max_text_bytes"`
	MaxTopicBytes           int    `json:"max_topic_bytes"`
	MaxPostingBytes         int    `json:"max_posting_bytes"`
	MaxMemberBytes          int    `json:"max_member_bytes"`
	MaxRootBytes            int    `json:"max_root_bytes"`
	MaxPostingIdentityBytes int64  `json:"max_posting_identity_bytes"`
	MaxGenerationBytes      int64  `json:"max_generation_bytes"`
	SourceRoleRule          string `json:"source_role_rule"`
}

func FrozenPolicy() Policy {
	return Policy{
		Schema: PolicySchema, PartitionBuckets: PartitionBuckets,
		MaxMembers: MaxMembers, MaxPostings: MaxPostings,
		MaxPostingsPerMember: MaxPostingsPerMember, MaxTextBytes: MaxTextBytes,
		MaxTopicBytes: MaxTopicBytes, MaxPostingBytes: MaxPostingBytes,
		MaxMemberBytes: MaxMemberBytes, MaxRootBytes: MaxRootBytes,
		MaxPostingIdentityBytes: MaxPostingIdentityBytes,
		MaxGenerationBytes:      MaxGenerationBytes,
		SourceRoleRule:          "vendor-then-go-test-then-production-v1",
	}
}

func historicalPolicy() Policy {
	value := FrozenPolicy()
	value.MaxPostingsPerMember = historicalMaxPostingsPerMember
	return value
}

func admittedPolicy(value Policy) bool {
	return value == FrozenPolicy() || value == historicalPolicy()
}

// The admitted policies are process constants; their digests are computed once
// so per-request root validation performs no marshal/hash work.
var (
	frozenPolicyDigest     = sync.OnceValues(func() (string, error) { return digestValue(FrozenPolicy()) })
	historicalPolicyDigest = sync.OnceValues(func() (string, error) { return digestValue(historicalPolicy()) })
)

type Authority struct {
	Repository                  string                                      `json:"repository"`
	ObservationGenerationDigest string                                      `json:"observation_generation_digest"`
	ObservationManifestDigest   string                                      `json:"observation_manifest_digest"`
	ObservationSourceDigest     string                                      `json:"observation_source_digest"`
	PolicyDigest                string                                      `json:"policy_digest"`
	ObservationV2               *observationpublication.DownstreamAuthority `json:"observation_v2,omitempty"`
	Upstream                    *downstreamauthority.Authority              `json:"upstream,omitempty"`
}

// Posting retains source evidence only. TopicSpelling and GroupIDSpelling do
// not assert that a broker, deployment, or runtime uses the same identity.
type Posting struct {
	Schema          string                 `json:"schema"`
	Plane           string                 `json:"plane"` // producer | consumer
	Class           string                 `json:"class"` // literal | unresolved
	Reason          string                 `json:"reason,omitempty"`
	SourceReason    string                 `json:"source_reason,omitempty"`
	TopicSpelling   string                 `json:"topic_spelling,omitempty"`
	GroupIDSpelling string                 `json:"group_id_spelling,omitempty"`
	Library         string                 `json:"library"`
	ImportPath      string                 `json:"import_path,omitempty"`
	Shape           string                 `json:"shape"`
	Binding         string                 `json:"binding,omitempty"`
	ObjectID        string                 `json:"object_id"`
	ContentDigest   string                 `json:"content_digest"`
	Path            string                 `json:"path"`
	Mode            string                 `json:"mode"`
	Revisions       []int                  `json:"revisions"`
	Span            sourceobservation.Span `json:"span"`
	SourceRole      string                 `json:"source_role"`
	Digest          string                 `json:"digest"`
}

type Member struct {
	Schema   string    `json:"schema"`
	Plane    string    `json:"plane"`
	Bucket   int       `json:"bucket"`
	Postings []Posting `json:"postings"`
	Digest   string    `json:"digest"`
}

type MemberReceipt struct {
	Plane         string `json:"plane"`
	Bucket        int    `json:"bucket"`
	Name          string `json:"name"`
	PostingCount  int    `json:"posting_count"`
	ContentBytes  int64  `json:"content_bytes"`
	ContentDigest string `json:"content_digest"`
}

type Root struct {
	Schema             string          `json:"schema"`
	Authority          Authority       `json:"authority"`
	Policy             Policy          `json:"policy"`
	Members            []MemberReceipt `json:"members"`
	PostingCount       int             `json:"posting_count"`
	ProducerCount      int             `json:"producer_count"`
	ConsumerCount      int             `json:"consumer_count"`
	LiteralCount       int             `json:"literal_count"`
	UnresolvedCount    int             `json:"unresolved_count"`
	EncodedMemberBytes int64           `json:"encoded_member_bytes"`
	GenerationDigest   string          `json:"generation_digest"`
	Digest             string          `json:"digest"`
}

func setPostingDigest(value *Posting) error {
	value.Digest = ""
	digest, err := digestValue(*value)
	if err != nil {
		return err
	}
	value.Digest = digest
	return nil
}

func validatePosting(value Posting) error {
	if value.Schema != PostingSchema || !validPlane(value.Plane) ||
		!gitobj.IsObjectID(value.ObjectID) || !validDigest(value.ContentDigest) ||
		repopath.Validate(value.Path) != nil || (value.Mode != "100644" && value.Mode != "100755") ||
		len(value.Revisions) == 0 || !validSpan(value.Span) ||
		!validText(value.Library) || !optionalText(value.ImportPath) || !validText(value.Shape) ||
		!optionalText(value.GroupIDSpelling) ||
		(value.SourceRole != "production" && value.SourceRole != "test" && value.SourceRole != "vendor") {
		return fmt.Errorf("%w: posting shape", ErrInvalid)
	}
	for index, revision := range value.Revisions {
		if revision < 0 || index > 0 && value.Revisions[index-1] >= revision {
			return fmt.Errorf("%w: posting revisions", ErrInvalid)
		}
	}
	switch value.Class {
	case "literal":
		if !validTopic(value.TopicSpelling) || value.Reason != "" || value.SourceReason != "" ||
			(value.Binding != "literal" && value.Binding != "same-file-const") {
			return fmt.Errorf("%w: literal posting", ErrInvalid)
		}
	case "unresolved":
		if value.TopicSpelling != "" || value.Binding != "" || !validReason(value.Reason) ||
			!validText(value.SourceReason) {
			return fmt.Errorf("%w: unresolved posting", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: posting class", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: posting digest", ErrInvalid)
	}
	return nil
}

func validReason(value string) bool {
	return value == "dynamic_topic" || value == "unsupported_topic"
}

func validateMember(value Member) error {
	if value.Schema != MemberSchema || !validPlane(value.Plane) ||
		value.Bucket < 0 || value.Bucket >= PartitionBuckets ||
		len(value.Postings) < 1 || len(value.Postings) > MaxPostingsPerMember {
		return fmt.Errorf("%w: member shape", ErrInvalid)
	}
	prior := ""
	for _, posting := range value.Postings {
		if posting.Plane != value.Plane || validatePosting(posting) != nil {
			return fmt.Errorf("%w: member posting", ErrInvalid)
		}
		key := postingKey(posting)
		if prior != "" && prior >= key {
			return fmt.Errorf("%w: member ordering", ErrInvalid)
		}
		prior = key
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: member digest", ErrInvalid)
	}
	return nil
}

func validateRoot(value Root) error {
	if (value.Schema != RootSchema && value.Schema != RootSchemaV2) ||
		validateAuthority(value.Schema, value.Authority) != nil ||
		!admittedPolicy(value.Policy) || len(value.Members) > MaxMembers ||
		value.PostingCount < 0 || value.PostingCount > MaxPostings ||
		value.ProducerCount < 0 || value.ConsumerCount < 0 ||
		value.LiteralCount < 0 || value.UnresolvedCount < 0 ||
		value.PostingCount != value.ProducerCount+value.ConsumerCount ||
		value.PostingCount != value.LiteralCount+value.UnresolvedCount ||
		value.EncodedMemberBytes < 0 || value.EncodedMemberBytes > MaxGenerationBytes ||
		!validDigest(value.GenerationDigest) || !validDigest(value.Digest) {
		return fmt.Errorf("%w: root shape", ErrInvalid)
	}
	count := 0
	var encoded int64
	prior := ""
	for _, receipt := range value.Members {
		key := memberKey(receipt.Plane, receipt.Bucket)
		if !validPlane(receipt.Plane) || receipt.Bucket < 0 || receipt.Bucket >= PartitionBuckets ||
			prior >= key || receipt.Name != memberName(receipt.Plane, receipt.Bucket) ||
			receipt.PostingCount < 1 || receipt.PostingCount > value.Policy.MaxPostingsPerMember ||
			receipt.ContentBytes < 1 || receipt.ContentBytes > MaxMemberBytes ||
			!validDigest(receipt.ContentDigest) {
			return fmt.Errorf("%w: member receipt", ErrInvalid)
		}
		if receipt.PostingCount > MaxPostings-count || receipt.ContentBytes > MaxGenerationBytes-encoded {
			return ErrLimit
		}
		count += receipt.PostingCount
		encoded += receipt.ContentBytes
		prior = key
	}
	if count != value.PostingCount || encoded != value.EncodedMemberBytes {
		return fmt.Errorf("%w: root totals", ErrInvalid)
	}
	policyDigest, err := frozenPolicyDigest()
	if value.Policy == historicalPolicy() {
		policyDigest, err = historicalPolicyDigest()
	}
	if err != nil || value.Authority.PolicyDigest != policyDigest {
		return fmt.Errorf("%w: policy authority", ErrInvalid)
	}
	copyValue := value
	copyValue.GenerationDigest, copyValue.Digest = "", ""
	generation, err := digestValue(copyValue)
	if err != nil || generation != value.GenerationDigest {
		return fmt.Errorf("%w: generation digest", ErrInvalid)
	}
	copyValue = value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: root digest", ErrInvalid)
	}
	return nil
}

// ValidateRoot exposes the closed root-control validator to the later atomic
// relationship-root publisher without granting mutation or member access.
func ValidateRoot(value Root) error { return validateRoot(value) }

func validateAuthority(schema string, value Authority) error {
	if reponame.Validate(value.Repository) != nil ||
		!validDigest(value.ObservationGenerationDigest) ||
		!validDigest(value.ObservationManifestDigest) ||
		!validDigest(value.ObservationSourceDigest) || !validDigest(value.PolicyDigest) {
		return fmt.Errorf("%w: authority", ErrInvalid)
	}
	if schema == RootSchema && (value.ObservationV2 != nil || value.Upstream != nil) {
		return fmt.Errorf("%w: v1 observation authority", ErrInvalid)
	}
	if schema == RootSchemaV2 {
		observation := value.ObservationV2
		if observation == nil || value.Upstream == nil ||
			downstreamauthority.RequireUsable(*value.Upstream) != nil ||
			value.Upstream.Repository != value.Repository || value.Upstream.Observation != *observation ||
			observation.Version != observationpublication.DownstreamAuthorityV2 ||
			observation.Repository != value.Repository ||
			observation.ObservationGenerationDigest != value.ObservationGenerationDigest ||
			observation.ObservationRootDigest != value.ObservationManifestDigest ||
			observation.SourceGenerationDigest != value.ObservationSourceDigest ||
			!validDigest(observation.SourceRootDigest) || !validDigest(observation.PartitionPolicyDigest) ||
			!validDigest(observation.ObservationPolicyDigest) || !validDigest(observation.InventoryPolicyDigest) ||
			observation.RecordCount < 0 || observation.ObservedCount < 0 ||
			observation.ObservedCount > observation.RecordCount {
			return fmt.Errorf("%w: v2 observation authority", ErrInvalid)
		}
	}
	want, err := frozenPolicyDigest()
	historical, historicalErr := historicalPolicyDigest()
	if err != nil || historicalErr != nil ||
		value.PolicyDigest != want && value.PolicyDigest != historical {
		return fmt.Errorf("%w: policy authority", ErrInvalid)
	}
	return nil
}

func setRootDigests(value *Root) error {
	value.GenerationDigest, value.Digest = "", ""
	generation, err := digestValue(*value)
	if err != nil {
		return err
	}
	value.GenerationDigest = generation
	digest, err := digestValue(*value)
	if err != nil {
		return err
	}
	value.Digest = digest
	return validateRoot(*value)
}

func clonePostings(values []Posting) []Posting {
	result := slices.Clone(values)
	for index := range result {
		result[index].Revisions = slices.Clone(result[index].Revisions)
	}
	return result
}

func postingKey(value Posting) string {
	return strings.Join([]string{
		partitionIdentity(value), value.Path, value.Mode, revisionsKey(value.Revisions),
		value.ObjectID, fmt.Sprintf("%010d:%010d", value.Span.StartByte, value.Span.EndByte),
		value.Class, value.Library, value.Shape, value.Digest,
	}, "\x00")
}

func partitionIdentity(value Posting) string {
	if value.Class == "literal" {
		return value.TopicSpelling
	}
	return "__unresolved__"
}

func revisionsKey(values []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = fmt.Sprintf("%010d", value)
	}
	return strings.Join(parts, ",")
}

func partitionBucket(value string) int {
	sum := sha256.Sum256([]byte("phebs-kafka-topic-bucket-v1\x00" + value))
	return int(sum[0])
}

func memberKey(plane string, bucket int) string {
	return fmt.Sprintf("%s\x00%03d", plane, bucket)
}

func memberName(plane string, bucket int) string {
	return fmt.Sprintf("%s-%03d.json", plane, bucket)
}

func validPlane(value string) bool { return value == "producer" || value == "consumer" }

func validTopic(value string) bool {
	if value == "" || len(value) > MaxTopicBytes || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func validText(value string) bool {
	return value != "" && len(value) <= MaxTextBytes && validUTF8Text(value)
}

func optionalText(value string) bool {
	return value == "" || len(value) <= MaxTextBytes && validUTF8Text(value)
}

func validUTF8Text(value string) bool {
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

func validSpan(value sourceobservation.Span) bool {
	return value.StartByte >= 0 && value.EndByte > value.StartByte &&
		value.StartLine > 0 && value.EndLine >= value.StartLine
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
