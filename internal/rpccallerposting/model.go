// Package rpccallerposting joins repository-shared Go observations to exact
// resolver namespace authority and publishes deterministic RPC call postings.
// Service ownership is projected only by later Epic 37 tickets.
package rpccallerposting

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

const (
	RootSchema    = "phebs-rpc-caller-posting-root-v1"
	MemberSchema  = "phebs-rpc-caller-posting-member-v1"
	PostingSchema = "phebs-rpc-caller-posting-v1"
	PolicySchema  = "phebs-rpc-caller-posting-policy-v1"

	PartitionBuckets         = 256
	MaxMembers               = 2 * PartitionBuckets
	MaxPostings              = 1_000_000
	MaxPostingsPerMember     = 50_000
	MaxCandidateOperations   = 32
	MaxResolverRecordDigests = 64
	MaxNamespaceReads        = 16_384
	MaxTextBytes             = 4_096
	MaxPostingBytes          = 1 << 20
	MaxMemberBytes           = 128 << 20
	MaxRootBytes             = 8 << 20
	MaxPostingIdentityBytes  = int64(1 << 30)
	MaxGenerationBytes       = int64(20 << 30)
)

var (
	ErrInvalid  = errors.New("invalid RPC caller posting publication")
	ErrLimit    = errors.New("RPC caller posting bound exceeded")
	ErrNotFound = errors.New("RPC caller posting publication not found")
)

type Policy struct {
	Schema                   string `json:"schema"`
	PartitionBuckets         int    `json:"partition_buckets"`
	MaxMembers               int    `json:"max_members"`
	MaxPostings              int    `json:"max_postings"`
	MaxPostingsPerMember     int    `json:"max_postings_per_member"`
	MaxCandidateOperations   int    `json:"max_candidate_operations"`
	MaxResolverRecordDigests int    `json:"max_resolver_record_digests"`
	MaxNamespaceReads        int    `json:"max_namespace_reads"`
	MaxTextBytes             int    `json:"max_text_bytes"`
	MaxPostingBytes          int    `json:"max_posting_bytes"`
	MaxMemberBytes           int    `json:"max_member_bytes"`
	MaxRootBytes             int    `json:"max_root_bytes"`
	MaxPostingIdentityBytes  int64  `json:"max_posting_identity_bytes"`
	MaxGenerationBytes       int64  `json:"max_generation_bytes"`
	SourceRoleRule           string `json:"source_role_rule"`
}

func FrozenPolicy() Policy {
	return Policy{
		Schema: PolicySchema, PartitionBuckets: PartitionBuckets,
		MaxMembers: MaxMembers, MaxPostings: MaxPostings,
		MaxPostingsPerMember:     MaxPostingsPerMember,
		MaxCandidateOperations:   MaxCandidateOperations,
		MaxResolverRecordDigests: MaxResolverRecordDigests,
		MaxNamespaceReads:        MaxNamespaceReads, MaxTextBytes: MaxTextBytes,
		MaxPostingBytes: MaxPostingBytes, MaxMemberBytes: MaxMemberBytes,
		MaxRootBytes: MaxRootBytes, MaxPostingIdentityBytes: MaxPostingIdentityBytes,
		MaxGenerationBytes: MaxGenerationBytes,
		SourceRoleRule:     "resolver-generated-then-vendor-then-go-test-then-production-v1",
	}
}

type Authority struct {
	Repository                  string `json:"repository"`
	ObservationGenerationDigest string `json:"observation_generation_digest"`
	ObservationManifestDigest   string `json:"observation_manifest_digest"`
	ObservationSourceDigest     string `json:"observation_source_digest"`
	ResolverCommit              string `json:"resolver_commit"`
	ResolverGenerationDigest    string `json:"resolver_generation_digest"`
	ResolverRootDigest          string `json:"resolver_root_digest"`
	PolicyDigest                string `json:"policy_digest"`
}

type Posting struct {
	Schema                string                 `json:"schema"`
	Protocol              string                 `json:"protocol"`
	Class                 string                 `json:"class"` // resolved | name_match | unresolved
	Reason                string                 `json:"reason,omitempty"`
	LookupOperation       string                 `json:"lookup_operation,omitempty"`
	Operation             string                 `json:"operation,omitempty"`
	CandidateOperations   []string               `json:"candidate_operations"`
	ObjectID              string                 `json:"object_id"`
	ContentDigest         string                 `json:"content_digest"`
	Path                  string                 `json:"path"`
	Mode                  string                 `json:"mode"`
	Revisions             []int                  `json:"revisions"`
	Span                  sourceobservation.Span `json:"span"`
	SourceRole            string                 `json:"source_role"`
	DeclarationPath       string                 `json:"declaration_path,omitempty"`
	DeclarationLineage    string                 `json:"declaration_lineage,omitempty"`
	ResolverRecordDigests []string               `json:"resolver_record_digests"`
	Digest                string                 `json:"digest"`
}

type Member struct {
	Schema   string    `json:"schema"`
	Protocol string    `json:"protocol"`
	Bucket   int       `json:"bucket"`
	Postings []Posting `json:"postings"`
	Digest   string    `json:"digest"`
}

type MemberReceipt struct {
	Protocol      string `json:"protocol"`
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
	ResolvedCount      int             `json:"resolved_count"`
	NameMatchCount     int             `json:"name_match_count"`
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
	if value.Schema != PostingSchema || (value.Protocol != "grpc" && value.Protocol != "thrift") ||
		!gitobj.IsObjectID(value.ObjectID) || !validDigest(value.ContentDigest) ||
		repopath.Validate(value.Path) != nil || (value.Mode != "100644" && value.Mode != "100755") ||
		len(value.Revisions) == 0 || !validSpan(value.Span) ||
		len(value.CandidateOperations) > MaxCandidateOperations ||
		len(value.ResolverRecordDigests) > MaxResolverRecordDigests ||
		(value.SourceRole != "production" && value.SourceRole != "test" &&
			value.SourceRole != "vendor" && value.SourceRole != "generated") {
		return fmt.Errorf("%w: posting shape", ErrInvalid)
	}
	for index, revision := range value.Revisions {
		if revision < 0 || index > 0 && value.Revisions[index-1] >= revision {
			return fmt.Errorf("%w: posting revisions", ErrInvalid)
		}
	}
	if !canonicalTextSet(value.CandidateOperations, false) ||
		!canonicalDigestSet(value.ResolverRecordDigests) {
		return fmt.Errorf("%w: posting sets", ErrInvalid)
	}
	switch value.Class {
	case "resolved":
		if value.Reason != "" || !validText(value.LookupOperation) ||
			value.Operation != value.LookupOperation || !validText(value.Operation) ||
			len(value.CandidateOperations) != 0 || repopath.Validate(value.DeclarationPath) != nil ||
			!validText(value.DeclarationLineage) || len(value.ResolverRecordDigests) < 1 {
			return fmt.Errorf("%w: resolved posting", ErrInvalid)
		}
	case "name_match":
		if value.Reason != "name_match_only" || len(value.CandidateOperations) != 1 ||
			value.LookupOperation != value.CandidateOperations[0] || value.Operation != "" ||
			value.DeclarationPath != "" || value.DeclarationLineage != "" ||
			len(value.ResolverRecordDigests) < 1 {
			return fmt.Errorf("%w: name-match posting", ErrInvalid)
		}
	case "unresolved":
		if !validReason(value.Reason) || value.LookupOperation != "" || value.Operation != "" ||
			value.DeclarationPath != "" || value.DeclarationLineage != "" {
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
	switch value {
	case "ambiguous_receiver_provenance", "ambiguous_method_candidates",
		"dynamic_receiver", "unsupported_receiver", "resolver_conflict",
		"resolver_ambiguous", "resolver_unsupported", "resolver_unavailable",
		"resolver_generated_input":
		return true
	default:
		return false
	}
}

func validateMember(value Member) error {
	if value.Schema != MemberSchema || (value.Protocol != "grpc" && value.Protocol != "thrift") ||
		value.Bucket < 0 || value.Bucket >= PartitionBuckets ||
		len(value.Postings) < 1 || len(value.Postings) > MaxPostingsPerMember {
		return fmt.Errorf("%w: member shape", ErrInvalid)
	}
	prior := ""
	for _, posting := range value.Postings {
		if posting.Protocol != value.Protocol || validatePosting(posting) != nil {
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
	if value.Schema != RootSchema || validateAuthority(value.Authority) != nil ||
		value.Policy != FrozenPolicy() || len(value.Members) > MaxMembers ||
		value.PostingCount < 0 || value.PostingCount > MaxPostings ||
		value.ResolvedCount < 0 || value.NameMatchCount < 0 || value.UnresolvedCount < 0 ||
		value.PostingCount != value.ResolvedCount+value.NameMatchCount+value.UnresolvedCount ||
		value.EncodedMemberBytes < 0 || value.EncodedMemberBytes > MaxGenerationBytes ||
		!validDigest(value.GenerationDigest) || !validDigest(value.Digest) {
		return fmt.Errorf("%w: root shape", ErrInvalid)
	}
	count := 0
	var encoded int64
	prior := ""
	for _, receipt := range value.Members {
		if (receipt.Protocol != "grpc" && receipt.Protocol != "thrift") ||
			receipt.Bucket < 0 || receipt.Bucket >= PartitionBuckets ||
			receipt.Name != memberName(receipt.Protocol, receipt.Bucket) ||
			receipt.PostingCount < 1 || receipt.PostingCount > MaxPostingsPerMember ||
			receipt.ContentBytes < 1 || receipt.ContentBytes > MaxMemberBytes ||
			!validDigest(receipt.ContentDigest) {
			return fmt.Errorf("%w: member receipt", ErrInvalid)
		}
		key := memberKey(receipt.Protocol, receipt.Bucket)
		if prior != "" && prior >= key {
			return fmt.Errorf("%w: root member ordering", ErrInvalid)
		}
		prior = key
		count += receipt.PostingCount
		if receipt.ContentBytes > MaxGenerationBytes-encoded {
			return ErrLimit
		}
		encoded += receipt.ContentBytes
	}
	if count != value.PostingCount || encoded != value.EncodedMemberBytes {
		return fmt.Errorf("%w: root totals", ErrInvalid)
	}
	generation := value
	generation.GenerationDigest, generation.Digest = "", ""
	generationDigest, err := digestValue(generation)
	if err != nil || generationDigest != value.GenerationDigest {
		return fmt.Errorf("%w: generation digest", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: root digest", ErrInvalid)
	}
	return nil
}

func validateAuthority(value Authority) error {
	if reponame.Validate(value.Repository) != nil || !validDigest(value.ObservationGenerationDigest) ||
		!validDigest(value.ObservationManifestDigest) || !validDigest(value.ObservationSourceDigest) ||
		!validText(value.ResolverCommit) || !validDigest(value.ResolverGenerationDigest) ||
		!validDigest(value.ResolverRootDigest) || !validDigest(value.PolicyDigest) {
		return fmt.Errorf("%w: authority", ErrInvalid)
	}
	want, err := digestValue(FrozenPolicy())
	if err != nil || want != value.PolicyDigest {
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
	return nil
}

func partitionOperation(value Posting) string {
	if value.Class == "unresolved" {
		return "__unresolved__"
	}
	return value.LookupOperation
}

func partitionBucket(operation string) int {
	sum := sha256.Sum256([]byte("phebs-rpc-caller-operation-v1\x00" + operation))
	return int(sum[0])
}

func memberKey(protocol string, bucket int) string {
	return fmt.Sprintf("%s\x00%03d", protocol, bucket)
}

func memberName(protocol string, bucket int) string {
	return fmt.Sprintf("%s-%02x.json", protocol, bucket)
}

func postingKey(value Posting) string {
	return strings.Join([]string{
		partitionOperation(value), value.Path, value.ObjectID,
		fmt.Sprintf("%010d:%010d", value.Span.StartByte, value.Span.EndByte),
		value.Class, value.Digest,
	}, "\x00")
}

func validSpan(value sourceobservation.Span) bool {
	return value.StartByte >= 0 && value.EndByte > value.StartByte &&
		value.StartLine > 0 && value.EndLine >= value.StartLine
}

func canonicalTextSet(values []string, allowEmpty bool) bool {
	if values == nil {
		return false
	}
	for index, value := range values {
		valid := validText(value)
		if allowEmpty && value == "" {
			valid = true
		}
		if !valid || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func canonicalDigestSet(values []string) bool {
	if values == nil {
		return false
	}
	for index, value := range values {
		if !validDigest(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func validText(value string) bool {
	if value == "" || len(value) > MaxTextBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
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

func decodeExact(raw []byte, limit int, destination any) error {
	if len(raw) > limit {
		return ErrLimit
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func clonePostings(values []Posting) []Posting {
	result := slices.Clone(values)
	for index := range result {
		result[index].CandidateOperations = slices.Clone(result[index].CandidateOperations)
		result[index].Revisions = slices.Clone(result[index].Revisions)
		result[index].ResolverRecordDigests = slices.Clone(result[index].ResolverRecordDigests)
	}
	return result
}
