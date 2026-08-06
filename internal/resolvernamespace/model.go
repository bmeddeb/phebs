// Package resolvernamespace owns the repository-shared, namespace-sharded
// declaration/resolver catalog consumed by relationship postings. Catalog
// identity is deliberately independent of service or analysis-unit ownership.
package resolvernamespace

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
)

const (
	RootSchema    = "phebs-resolver-namespace-root-v1"
	MemberSchema  = "phebs-resolver-namespace-member-v1"
	RecordSchema  = "phebs-resolver-namespace-record-v1"
	PointerSchema = "phebs-resolver-namespace-pointer-v1"
	MarkerSchema  = "phebs-resolver-namespace-marker-v1"
	PolicySchema  = "phebs-resolver-namespace-policy-v1"
	LanguageGo    = "go"

	MaxNamespaces          = 16_384
	MaxRecordsPerNamespace = 8_192
	MaxRecords             = 100_000
	MaxConstructors        = 64
	MaxConflictCandidates  = 32
	MaxTextBytes           = 4_096
	MaxRecordBytes         = 1 << 20
	MaxRecordIdentityBytes = int64(512 << 20)
	MaxRootBytes           = 8 << 20
	MaxMemberBytes         = 64 << 20
	MaxGenerationBytes     = int64(10 << 30)
)

var (
	ErrInvalid    = errors.New("invalid resolver namespace catalog")
	ErrLimit      = errors.New("resolver namespace catalog bound exceeded")
	ErrNotFound   = errors.New("resolver namespace not found")
	ErrPublishing = errors.New("resolver namespace publication is incomplete")
)

type Policy struct {
	Schema                 string `json:"schema"`
	MaxNamespaces          int    `json:"max_namespaces"`
	MaxRecordsPerNamespace int    `json:"max_records_per_namespace"`
	MaxRecords             int    `json:"max_records"`
	MaxConstructors        int    `json:"max_constructors"`
	MaxConflictCandidates  int    `json:"max_conflict_candidates"`
	MaxTextBytes           int    `json:"max_text_bytes"`
	MaxRecordBytes         int    `json:"max_record_bytes"`
	MaxRecordIdentityBytes int64  `json:"max_record_identity_bytes"`
	MaxRootBytes           int    `json:"max_root_bytes"`
	MaxMemberBytes         int    `json:"max_member_bytes"`
	MaxGenerationBytes     int64  `json:"max_generation_bytes"`
}

func FrozenPolicy() Policy {
	return Policy{
		Schema: PolicySchema, MaxNamespaces: MaxNamespaces,
		MaxRecordsPerNamespace: MaxRecordsPerNamespace,
		MaxRecords:             MaxRecords, MaxConstructors: MaxConstructors,
		MaxConflictCandidates: MaxConflictCandidates, MaxTextBytes: MaxTextBytes,
		MaxRecordBytes: MaxRecordBytes, MaxRecordIdentityBytes: MaxRecordIdentityBytes,
		MaxRootBytes: MaxRootBytes, MaxMemberBytes: MaxMemberBytes,
		MaxGenerationBytes: MaxGenerationBytes,
	}
}

type Authority struct {
	Repository               string `json:"repository"`
	Commit                   string `json:"commit"`
	ResolverGenerationDigest string `json:"resolver_generation_digest"`
	ResolverManifestDigest   string `json:"resolver_manifest_digest"`
	PolicyDigest             string `json:"policy_digest"`
}

// Candidate is one exact target retained by a conflict record. It never
// chooses among conflicting declaration authority.
type Candidate struct {
	Package            string `json:"package"`
	Operation          string `json:"operation"`
	DeclarationPath    string `json:"declaration_path"`
	DeclarationLineage string `json:"declaration_lineage"`
}

// Record is one exact generated-symbol resolver identity or an explicit
// conflict marker. Ambiguous, unavailable, and unsupported inputs stay in the
// catalog as records rather than disappearing from resolver authority.
type Record struct {
	Schema                string      `json:"schema"`
	Kind                  string      `json:"kind"` // symbol | conflict
	State                 string      `json:"state"`
	Reason                string      `json:"reason,omitempty"`
	Language              string      `json:"language"`
	Protocol              string      `json:"protocol"`
	Namespace             string      `json:"namespace"`
	Package               string      `json:"package"`
	ClientType            string      `json:"client_type"`
	Method                string      `json:"method"`
	Operation             string      `json:"operation,omitempty"`
	Constructors          []string    `json:"constructors"`
	GeneratedPath         string      `json:"generated_path,omitempty"`
	GeneratedObjectID     string      `json:"generated_object_id,omitempty"`
	GeneratedDigest       string      `json:"generated_digest,omitempty"`
	GeneratorRelativePath string      `json:"generator_relative_path,omitempty"`
	DeclarationPath       string      `json:"declaration_path,omitempty"`
	DeclarationLineage    string      `json:"declaration_lineage,omitempty"`
	Candidates            []Candidate `json:"candidates"`
	Digest                string      `json:"digest"`
}

type Member struct {
	Schema    string   `json:"schema"`
	Language  string   `json:"language"`
	Protocol  string   `json:"protocol"`
	Namespace string   `json:"namespace"`
	Records   []Record `json:"records"`
	Digest    string   `json:"digest"`
}

type NamespaceReceipt struct {
	Language      string `json:"language"`
	Protocol      string `json:"protocol"`
	Namespace     string `json:"namespace"`
	Member        string `json:"member"`
	RecordCount   int    `json:"record_count"`
	ContentBytes  int64  `json:"content_bytes"`
	ContentDigest string `json:"content_digest"`
}

type Root struct {
	Schema           string             `json:"schema"`
	Authority        Authority          `json:"authority"`
	Policy           Policy             `json:"policy"`
	Namespaces       []NamespaceReceipt `json:"namespaces"`
	NamespaceCount   int                `json:"namespace_count"`
	RecordCount      int                `json:"record_count"`
	ContentBytes     int64              `json:"content_bytes"`
	GenerationDigest string             `json:"generation_digest"`
	Digest           string             `json:"digest"`
}

type Pointer struct {
	Schema           string `json:"schema"`
	Repository       string `json:"repository"`
	GenerationDigest string `json:"generation_digest"`
	RootDigest       string `json:"root_digest"`
	RootFile         string `json:"root_file"`
	Digest           string `json:"digest"`
}

type marker struct {
	Schema  string  `json:"schema"`
	Pointer Pointer `json:"pointer"`
	Digest  string  `json:"digest"`
}

func newAuthority(repository, commit, generation, manifest string) (Authority, error) {
	policyDigest, err := digestValue(FrozenPolicy())
	if err != nil {
		return Authority{}, err
	}
	value := Authority{
		Repository: repository, Commit: commit,
		ResolverGenerationDigest: generation,
		ResolverManifestDigest:   manifest, PolicyDigest: policyDigest,
	}
	if err := validateAuthority(value); err != nil {
		return Authority{}, err
	}
	return value, nil
}

func validateAuthority(value Authority) error {
	if reponame.Validate(value.Repository) != nil || !validToken(value.Commit, 256) ||
		!validDigest(value.ResolverGenerationDigest) ||
		!validDigest(value.ResolverManifestDigest) || !validDigest(value.PolicyDigest) {
		return fmt.Errorf("%w: authority", ErrInvalid)
	}
	want, err := digestValue(FrozenPolicy())
	if err != nil || value.PolicyDigest != want {
		return fmt.Errorf("%w: policy authority", ErrInvalid)
	}
	return nil
}

func setRecordDigest(value *Record) error {
	value.Digest = ""
	digest, err := digestValue(*value)
	if err != nil {
		return err
	}
	value.Digest = digest
	return nil
}

func validateRecord(value Record) error {
	if value.Schema != RecordSchema || value.Language != LanguageGo ||
		(value.Protocol != "grpc" && value.Protocol != "thrift") ||
		!validText(value.Namespace) || !validText(value.Package) ||
		!validText(value.ClientType) || !validText(value.Method) ||
		len(value.Constructors) > MaxConstructors || len(value.Candidates) > MaxConflictCandidates {
		return fmt.Errorf("%w: record shape", ErrInvalid)
	}
	for index, constructor := range value.Constructors {
		if !validText(constructor) || index > 0 && value.Constructors[index-1] >= constructor {
			return fmt.Errorf("%w: constructors", ErrInvalid)
		}
	}
	switch value.Kind {
	case "symbol":
		if len(value.Candidates) != 0 || !validText(value.Operation) ||
			!validText(value.GeneratedPath) || repopath.Validate(value.GeneratedPath) != nil ||
			!gitobj.IsObjectID(value.GeneratedObjectID) || !validDigest(value.GeneratedDigest) {
			return fmt.Errorf("%w: symbol identity", ErrInvalid)
		}
		if value.Protocol == "grpc" &&
			(repopath.Validate(value.GeneratorRelativePath) != nil ||
				!strings.HasSuffix(value.GeneratorRelativePath, ".proto")) ||
			value.Protocol == "thrift" && value.GeneratorRelativePath != "" {
			return fmt.Errorf("%w: generated selector", ErrInvalid)
		}
		switch value.State {
		case "resolved":
			if value.Reason != "" || repopath.Validate(value.DeclarationPath) != nil ||
				!validText(value.DeclarationLineage) {
				return fmt.Errorf("%w: resolved symbol authority", ErrInvalid)
			}
		case "ambiguous", "unsupported", "unavailable":
			if !validText(value.Reason) || value.DeclarationPath != "" ||
				value.DeclarationLineage != "" {
				return fmt.Errorf("%w: abstention", ErrInvalid)
			}
		default:
			return fmt.Errorf("%w: symbol state", ErrInvalid)
		}
	case "conflict":
		if value.State != "conflict" || value.Reason != "multiple_resolver_targets" ||
			value.Operation != "" || len(value.Constructors) != 0 ||
			value.GeneratedPath != "" || value.GeneratedObjectID != "" ||
			value.GeneratedDigest != "" || value.GeneratorRelativePath != "" ||
			value.DeclarationPath != "" || value.DeclarationLineage != "" ||
			len(value.Candidates) < 2 {
			return fmt.Errorf("%w: conflict record", ErrInvalid)
		}
		for index, candidate := range value.Candidates {
			if err := validateCandidate(candidate); err != nil ||
				index > 0 && candidateKey(value.Candidates[index-1]) >= candidateKey(candidate) {
				return fmt.Errorf("%w: conflict candidates", ErrInvalid)
			}
		}
	default:
		return fmt.Errorf("%w: record kind", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || value.Digest != digest {
		return fmt.Errorf("%w: record digest", ErrInvalid)
	}
	return nil
}

func validateCandidate(value Candidate) error {
	if !validText(value.Package) || !validText(value.Operation) ||
		repopath.Validate(value.DeclarationPath) != nil ||
		!validText(value.DeclarationLineage) {
		return fmt.Errorf("%w: conflict candidate", ErrInvalid)
	}
	return nil
}

func validateMember(value Member) error {
	if value.Schema != MemberSchema || value.Language != LanguageGo ||
		(value.Protocol != "grpc" && value.Protocol != "thrift") ||
		!validText(value.Namespace) || len(value.Records) > MaxRecordsPerNamespace {
		return fmt.Errorf("%w: member", ErrInvalid)
	}
	prior := ""
	for _, record := range value.Records {
		if record.Language != value.Language || record.Protocol != value.Protocol ||
			record.Namespace != value.Namespace ||
			validateRecord(record) != nil {
			return fmt.Errorf("%w: member record", ErrInvalid)
		}
		key := recordKey(record)
		if prior != "" && prior >= key {
			return fmt.Errorf("%w: member ordering", ErrInvalid)
		}
		prior = key
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || value.Digest != digest {
		return fmt.Errorf("%w: member digest", ErrInvalid)
	}
	return nil
}

func validateRoot(value Root) error {
	if value.Schema != RootSchema || validateAuthority(value.Authority) != nil ||
		value.Policy != FrozenPolicy() || len(value.Namespaces) > MaxNamespaces ||
		value.NamespaceCount != len(value.Namespaces) || value.RecordCount < 0 ||
		value.RecordCount > MaxRecords || value.ContentBytes < 0 ||
		value.ContentBytes > MaxGenerationBytes || !validDigest(value.GenerationDigest) {
		return fmt.Errorf("%w: root", ErrInvalid)
	}
	count := 0
	var content int64
	prior := ""
	for _, receipt := range value.Namespaces {
		if receipt.Language != LanguageGo ||
			(receipt.Protocol != "grpc" && receipt.Protocol != "thrift") ||
			!validText(receipt.Namespace) || !validMemberName(receipt.Member) ||
			receipt.RecordCount < 0 || receipt.RecordCount > MaxRecordsPerNamespace ||
			receipt.ContentBytes < 0 || receipt.ContentBytes > MaxMemberBytes ||
			!validDigest(receipt.ContentDigest) {
			return fmt.Errorf("%w: namespace receipt", ErrInvalid)
		}
		key := namespaceKey(receipt.Language, receipt.Protocol, receipt.Namespace)
		if prior != "" && prior >= key {
			return fmt.Errorf("%w: namespace ordering", ErrInvalid)
		}
		prior = key
		count += receipt.RecordCount
		if receipt.ContentBytes > MaxGenerationBytes-content {
			return ErrLimit
		}
		content += receipt.ContentBytes
	}
	if count != value.RecordCount || content != value.ContentBytes {
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

func setPointerDigest(value *Pointer) error {
	value.Digest = ""
	digest, err := digestValue(*value)
	if err != nil {
		return err
	}
	value.Digest = digest
	return nil
}

func validatePointer(value Pointer) error {
	if value.Schema != PointerSchema || reponame.Validate(value.Repository) != nil ||
		!validDigest(value.GenerationDigest) || !validDigest(value.RootDigest) ||
		value.RootFile != "root.json" {
		return fmt.Errorf("%w: pointer", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: pointer digest", ErrInvalid)
	}
	return nil
}

func namespaceKey(language, protocol, namespace string) string {
	return strings.Join([]string{language, protocol, namespace}, "\x00")
}

func recordLookupKey(value Record) string {
	return strings.Join([]string{value.Language, value.Protocol, value.Namespace, value.ClientType, value.Method}, "\x00")
}

func recordKey(value Record) string {
	return strings.Join([]string{recordLookupKey(value), value.Kind, value.State, value.Digest}, "\x00")
}

func candidateKey(value Candidate) string {
	return strings.Join([]string{
		value.Package, value.Operation, value.DeclarationPath, value.DeclarationLineage,
	}, "\x00")
}

func memberName(language, protocol, namespace string) string {
	sum := sha256.Sum256([]byte(namespaceKey(language, protocol, namespace)))
	return "namespace-" + hex.EncodeToString(sum[:]) + ".json"
}

func validMemberName(value string) bool {
	if len(value) != len("namespace-")+64+len(".json") ||
		!strings.HasPrefix(value, "namespace-") || !strings.HasSuffix(value, ".json") {
		return false
	}
	_, err := hex.DecodeString(value[len("namespace-") : len(value)-len(".json")])
	return err == nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

func validToken(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func validText(value string) bool {
	if !validToken(value, MaxTextBytes) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
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

func cloneRecords(values []Record) []Record {
	result := slices.Clone(values)
	for index := range result {
		result[index].Constructors = slices.Clone(result[index].Constructors)
		result[index].Candidates = slices.Clone(result[index].Candidates)
	}
	return result
}
