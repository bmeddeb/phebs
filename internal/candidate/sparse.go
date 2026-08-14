package candidate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/bmeddeb/phebs/internal/gitobj"
)

const (
	SparseRootSchema          = "phebs-candidate-partition-root-v1"
	SparseDomainSchema        = "phebs-candidate-domain-index-v1"
	ExtractionPartitionSchema = "phebs-extraction-partition-v1"
	SchedulePageSchema        = "phebs-extraction-partition-schedule-page-v1"
	DeadlineClosureSchema     = "phebs-extraction-partition-deadline-v1"

	SparseRootFileName           = "candidate-partition-root.json"
	MaxSparseDomains             = MaxPolicies
	MaxDomainPartitions          = 16_384
	MaxSparseAggregatePartitions = 131_072
	MaxSchedulePagePartitions    = 256
	MaxSparseRootBytes           = 1 << 20
	MaxSparseDomainBytes         = 16 << 20
	MaxSparseAggregateIndexBytes = 128 << 20
	MaxSparseTypedScopeBytes     = 48 << 20
	MaxSparseAggregateScopeBytes = 256 << 20
	MaxSchedulePageBytes         = 64 << 10

	PartitionKindCandidateMember = "candidate_member"
	PartitionKindTypedInput      = "typed_input"

	PartitionMaxCandidateRecords       = MaxRecordsPerArtifact
	PartitionMaxCandidateDeclaredBytes = MaxDeclaredBytesPerArtifact
	PartitionMaxCandidateContentBytes  = int64(maxArtifactBytes)
	PartitionMaxTypedInputBytes        = MaxDeclaredBytesPerArtifact
	PartitionMaxTypedScopeRecords      = 262_144
	PartitionMaxTypedScopePathBytes    = 16 << 20
	PartitionMaxOpenedSourceBytes      = int64(512 << 20)
	PartitionMaxRetainedSourceBytes    = int64(512 << 20)
	PartitionMaxFacts                  = 12_500
	PartitionMaxRows                   = 25_000
	PartitionMaxReferences             = 20_000
	PartitionDeadline                  = 5 * time.Minute

	sparseTypedScopeMagic     = "phebs-typed-source-scope-v1\n"
	sparseTypedScopeEntryBase = sha256.Size + 2 + 1 + 64 + 8
)

var (
	ErrInvalidSparseRoot = errors.New("invalid sparse candidate root")
	ErrSparseStale       = errors.New("sparse candidate root is stale")
	ErrSparseUnavailable = errors.New("sparse candidate domain is unavailable")
	ErrSparseTypedInput  = errors.New("sparse partition is a typed input")
)

type ExtractionPartitionQuotas struct {
	CandidateRecords       int   `json:"candidate_records"`
	CandidateDeclaredBytes int64 `json:"candidate_declared_bytes"`
	CandidateContentBytes  int64 `json:"candidate_content_bytes"`
	TypedInputBytes        int64 `json:"typed_input_bytes"`
	OpenedSourceBytes      int64 `json:"opened_source_bytes"`
	RetainedSourceBytes    int64 `json:"retained_source_bytes"`
	Facts                  int   `json:"facts"`
	Rows                   int   `json:"rows"`
	References             int   `json:"references"`
	DeadlineMilliseconds   int64 `json:"deadline_milliseconds"`
}

func FrozenExtractionPartitionQuotas() ExtractionPartitionQuotas {
	return ExtractionPartitionQuotas{
		CandidateRecords:       PartitionMaxCandidateRecords,
		CandidateDeclaredBytes: PartitionMaxCandidateDeclaredBytes,
		CandidateContentBytes:  PartitionMaxCandidateContentBytes,
		TypedInputBytes:        PartitionMaxTypedInputBytes,
		OpenedSourceBytes:      PartitionMaxOpenedSourceBytes,
		RetainedSourceBytes:    PartitionMaxRetainedSourceBytes,
		Facts:                  PartitionMaxFacts,
		Rows:                   PartitionMaxRows,
		References:             PartitionMaxReferences,
		DeadlineMilliseconds:   PartitionDeadline.Milliseconds(),
	}
}

type SparseTypedInput struct {
	Kind          string `json:"kind"`
	Path          string `json:"path,omitempty"`
	ObjectID      string `json:"object_id,omitempty"`
	DeclaredBytes int64  `json:"declared_bytes"`
	Present       bool   `json:"present"`
}

type SparseTypedScopeDescriptor struct {
	Name          string `json:"name"`
	Records       int    `json:"records"`
	ContentBytes  int64  `json:"content_bytes"`
	ContentDigest string `json:"content_digest"`
}

type SparseDomainDescriptor struct {
	Domain                   string                      `json:"domain"`
	Version                  string                      `json:"version"`
	PolicyOrdinal            int                         `json:"policy_ordinal"`
	Plane                    Plane                       `json:"plane"`
	Availability             string                      `json:"availability"`
	UnavailableReason        string                      `json:"unavailable_reason,omitempty"`
	TypedInputs              []SparseTypedInput          `json:"typed_inputs"`
	PartitionCount           int                         `json:"partition_count"`
	AdmittedRecords          int                         `json:"admitted_records"`
	AdmittedDeclaredBytes    int64                       `json:"admitted_declared_bytes"`
	CandidateMemberReadBytes int64                       `json:"candidate_member_read_bytes"`
	TypedScope               *SparseTypedScopeDescriptor `json:"typed_scope,omitempty"`
	IndexName                string                      `json:"index_name"`
	IndexBytes               int64                       `json:"index_bytes"`
	IndexDigest              string                      `json:"index_digest"`
	IndexContentDigest       string                      `json:"index_content_digest"`
	DomainScheduleDigest     string                      `json:"domain_schedule_digest"`
}

type SparseRoot struct {
	Schema                    string                    `json:"schema"`
	CandidateSchema           string                    `json:"candidate_schema"`
	Repository                string                    `json:"repository"`
	Commit                    string                    `json:"commit"`
	UnitDigest                string                    `json:"unit_digest"`
	PolicyDigest              string                    `json:"policy_digest"`
	CandidateGenerationDigest string                    `json:"candidate_generation_digest"`
	CandidateManifestDigest   string                    `json:"candidate_manifest_digest"`
	Quotas                    ExtractionPartitionQuotas `json:"quotas"`
	Domains                   []SparseDomainDescriptor  `json:"domains"`
	ScheduleDigest            string                    `json:"schedule_digest"`
	Digest                    string                    `json:"digest"`
}

type ExtractionPartition struct {
	Schema                    string                      `json:"schema"`
	Repository                string                      `json:"repository"`
	CandidateManifestDigest   string                      `json:"candidate_manifest_digest"`
	CandidateGenerationDigest string                      `json:"candidate_generation_digest"`
	Domain                    string                      `json:"domain"`
	Version                   string                      `json:"version"`
	Plane                     Plane                       `json:"plane"`
	Kind                      string                      `json:"kind"`
	Ordinal                   int                         `json:"ordinal"`
	SourceStart               int                         `json:"source_start"`
	SourceEnd                 int                         `json:"source_end"`
	ByteKind                  string                      `json:"byte_kind"`
	Member                    *Artifact                   `json:"member,omitempty"`
	TypedInput                *SparseTypedInput           `json:"typed_input,omitempty"`
	TypedScope                *SparseTypedScopeDescriptor `json:"typed_scope,omitempty"`
	CallerPrefix              string                      `json:"caller_prefix,omitempty"`
	CallerPrefixBits          int                         `json:"caller_prefix_bits,omitempty"`
	AdmittedRecords           int                         `json:"admitted_records"`
	AdmittedDeclaredBytes     int64                       `json:"admitted_declared_bytes"`
	Quotas                    ExtractionPartitionQuotas   `json:"quotas"`
	Digest                    string                      `json:"digest"`
}

type SparseDomainIndex struct {
	Schema                    string                    `json:"schema"`
	Repository                string                    `json:"repository"`
	CandidateManifestDigest   string                    `json:"candidate_manifest_digest"`
	CandidateGenerationDigest string                    `json:"candidate_generation_digest"`
	Domain                    string                    `json:"domain"`
	Version                   string                    `json:"version"`
	PolicyOrdinal             int                       `json:"policy_ordinal"`
	Plane                     Plane                     `json:"plane"`
	Quotas                    ExtractionPartitionQuotas `json:"quotas"`
	Partitions                []ExtractionPartition     `json:"partitions"`
	ScheduleDigest            string                    `json:"schedule_digest"`
	Digest                    string                    `json:"digest"`
}

type ScheduledPartition struct {
	Ordinal         int    `json:"ordinal"`
	Kind            string `json:"kind"`
	PartitionDigest string `json:"partition_digest"`
}

type CoalescedSchedulePage struct {
	Schema               string               `json:"schema"`
	RootScheduleDigest   string               `json:"root_schedule_digest"`
	DomainScheduleDigest string               `json:"domain_schedule_digest"`
	Domain               string               `json:"domain"`
	Version              string               `json:"version"`
	StartOrdinal         int                  `json:"start_ordinal"`
	EndOrdinal           int                  `json:"end_ordinal"`
	Partitions           []ScheduledPartition `json:"partitions"`
	Complete             bool                 `json:"complete"`
	Digest               string               `json:"digest"`
}

type DeadlineClosure struct {
	Schema               string `json:"schema"`
	PartitionDigest      string `json:"partition_digest"`
	Disposition          string `json:"disposition"`
	DeadlineMilliseconds int64  `json:"deadline_milliseconds"`
	Digest               string `json:"digest"`
}

type SparseBuildMetrics struct {
	CandidateMembersRead int
	CandidateBytesRead   int64
	DomainIndexesWritten int
	PeakIndexBytes       int64
	AggregateIndexBytes  int64
	AggregatePartitions  int
	TypedScopesWritten   int
	AggregateScopeBytes  int64
}

type SparseReadMetrics struct {
	CandidateManifestReads int
	RootReads              int
	DomainIndexReads       int
	CandidateMemberReads   int
	TypedScopeReads        int
	ControlBytesRead       int64
	CandidateBytesRead     int64
}

type SparsePublication struct {
	candidateRoot string
	sparseRoot    string
	manifest      Manifest
	state         State
	root          SparseRoot
	metrics       *SparseReadMetrics
}

type SparseDomain struct {
	publication *SparsePublication
	descriptor  SparseDomainDescriptor
	index       SparseDomainIndex
}

// Commit returns the immutable Git revision transitively bound by this
// domain's validated candidate publication. It lets T40.10 source leases read
// selected object IDs without re-resolving a mutable repository ref.
func (domain *SparseDomain) Commit() string {
	if domain == nil || domain.publication == nil {
		return ""
	}
	return domain.publication.manifest.Commit
}

// Domain returns the validated extraction-domain identity bound by this sparse
// authority. It is used to select domain-specific versioned result contracts
// without inferring identity from a possibly empty partition set.
func (domain *SparseDomain) Domain() string {
	if domain == nil {
		return ""
	}
	return domain.index.Domain
}

type sparseSourceMember struct {
	artifact   Artifact
	prefix     string
	prefixBits int
}

type sparseDomainBuilder struct {
	identity      PolicyIdentity
	index         SparseDomainIndex
	descriptor    SparseDomainDescriptor
	typedScope    []sparseTypedSource
	typedScopeRaw []byte
}

type sparseTypedSource struct {
	pathIdentity  [sha256.Size]byte
	path          string
	objectID      string
	declaredBytes int64
}

// SparseTypedSourceScope is one validated, immutable typed-partition source
// scope. Its compact bytes remain private so callers cannot bypass validation.
type SparseTypedSourceScope struct {
	raw     []byte
	records int
}

func sparseDomainName(ordinal int) string {
	return fmt.Sprintf("candidate-partition-domain-%03d.json", ordinal)
}

func sparseTypedScopeName(ordinal int) string {
	return fmt.Sprintf("candidate-partition-typed-scope-%03d.bin", ordinal)
}

func sparseAggregateWithinLimits(partitions int, indexBytes int64) bool {
	return partitions >= 0 && partitions <= MaxSparseAggregatePartitions &&
		indexBytes >= 0 && indexBytes <= MaxSparseAggregateIndexBytes
}

func BuildSparseRoot(
	ctx context.Context,
	outputDirectory string,
	publication *Publication,
	metrics *SparseBuildMetrics,
) (SparseRoot, error) {
	if ctx == nil {
		return SparseRoot{}, errors.New("sparse candidate build requires a context")
	}
	if metrics != nil {
		*metrics = SparseBuildMetrics{}
	}
	if publication == nil || publication.root == "" {
		return SparseRoot{}, errors.New("sparse candidate build requires a validated publication")
	}
	if !filepath.IsAbs(outputDirectory) || outputDirectory == string(filepath.Separator) {
		return SparseRoot{}, errors.New("sparse candidate output must be an absolute non-root directory")
	}
	if err := requireEmptyDirectory(outputDirectory); err != nil {
		return SparseRoot{}, err
	}
	manifest := publication.manifest
	state := publication.state
	if err := state.Validate(); err != nil {
		return SparseRoot{}, err
	}
	if err := validateManifestIdentity(manifest, Expected{}, &state); err != nil {
		return SparseRoot{}, err
	}
	if IsPublishing(publication.root, manifest.Repository) {
		return SparseRoot{}, ErrPublishing
	}
	if len(manifest.Policies) > MaxSparseDomains {
		return SparseRoot{}, sparseInvalid("domain count exceeds %d", MaxSparseDomains)
	}

	root := SparseRoot{
		Schema: SparseRootSchema, CandidateSchema: manifest.Schema,
		Repository: manifest.Repository, Commit: manifest.Commit,
		UnitDigest: manifest.UnitDigest, PolicyDigest: manifest.PolicyDigest,
		CandidateGenerationDigest: manifest.GenerationDigest,
		CandidateManifestDigest:   manifest.Digest,
		Quotas:                    FrozenExtractionPartitionQuotas(),
		Domains:                   make([]SparseDomainDescriptor, 0, len(manifest.Policies)),
	}
	builders := make([]sparseDomainBuilder, len(manifest.Policies))
	for policyOrdinal, identity := range manifest.Policies {
		builders[policyOrdinal] = newSparseDomainBuilder(manifest, policyOrdinal, identity)
	}
	aggregatePartitions := 0
	if err := scanSparseMembers(ctx, publication, builders, metrics, &aggregatePartitions); err != nil {
		return SparseRoot{}, err
	}
	aggregateScopeBytes := int64(0)
	for index := range builders {
		if err := addSparseTypedInputPartitions(
			manifest, &builders[index], &aggregatePartitions, &aggregateScopeBytes,
		); err != nil {
			return SparseRoot{}, err
		}
	}
	aggregateIndexBytes := int64(0)
	for policyOrdinal := range builders {
		if err := ctx.Err(); err != nil {
			return SparseRoot{}, err
		}
		builder := &builders[policyOrdinal]
		finishSparseDomain(builder)
		if err := validateSparseDomain(builder.index, builder.descriptor, manifest, true); err != nil {
			return SparseRoot{}, err
		}
		index, descriptor := builder.index, builder.descriptor
		indexName := sparseDomainName(policyOrdinal)
		indexBytes, err := sparseCanonical(index)
		if err != nil {
			return SparseRoot{}, err
		}
		if len(indexBytes) > MaxSparseDomainBytes {
			return SparseRoot{}, sparseInvalid("domain index exceeds %d bytes", MaxSparseDomainBytes)
		}
		if !sparseAggregateWithinLimits(
			aggregatePartitions, aggregateIndexBytes+int64(len(indexBytes)),
		) {
			return SparseRoot{}, sparseInvalid(
				"aggregate domain indexes exceed %d bytes", MaxSparseAggregateIndexBytes,
			)
		}
		aggregateIndexBytes += int64(len(indexBytes))
		descriptor.IndexName = indexName
		descriptor.IndexBytes = int64(len(indexBytes))
		descriptor.IndexDigest = index.Digest
		descriptor.IndexContentDigest = sparseContentDigest(indexBytes)
		if descriptor.TypedScope != nil {
			if err := writeSparseControl(
				filepath.Join(outputDirectory, descriptor.TypedScope.Name), builder.typedScopeRaw,
			); err != nil {
				return SparseRoot{}, err
			}
			if metrics != nil {
				metrics.TypedScopesWritten++
				metrics.AggregateScopeBytes = aggregateScopeBytes
			}
		}
		if err := writeSparseControl(filepath.Join(outputDirectory, indexName), indexBytes); err != nil {
			return SparseRoot{}, err
		}
		root.Domains = append(root.Domains, descriptor)
		if metrics != nil {
			metrics.DomainIndexesWritten++
			metrics.PeakIndexBytes = max(metrics.PeakIndexBytes, int64(len(indexBytes)))
			metrics.AggregateIndexBytes = aggregateIndexBytes
			metrics.AggregatePartitions = aggregatePartitions
		}
	}
	root.ScheduleDigest = sparseScheduleDigest(root)
	root.Digest = sparseRootDigest(root)
	rootBytes, err := sparseCanonical(root)
	if err != nil {
		return SparseRoot{}, err
	}
	if len(rootBytes) > MaxSparseRootBytes {
		return SparseRoot{}, sparseInvalid("root exceeds %d bytes", MaxSparseRootBytes)
	}
	if err := writeSparseControl(filepath.Join(outputDirectory, SparseRootFileName), rootBytes); err != nil {
		return SparseRoot{}, err
	}
	if err := syncDirectory(outputDirectory); err != nil {
		return SparseRoot{}, err
	}
	if err := ValidateSparseStage(ctx, outputDirectory, manifest, root.Digest); err != nil {
		return SparseRoot{}, err
	}
	if IsPublishing(publication.root, manifest.Repository) {
		return SparseRoot{}, ErrPublishing
	}
	return cloneSparseRoot(root), nil
}

func newSparseDomainBuilder(
	manifest Manifest,
	policyOrdinal int,
	identity PolicyIdentity,
) sparseDomainBuilder {
	return sparseDomainBuilder{
		identity: identity,
		index: SparseDomainIndex{
			Schema: SparseDomainSchema, Repository: manifest.Repository,
			CandidateManifestDigest:   manifest.Digest,
			CandidateGenerationDigest: manifest.GenerationDigest,
			Domain:                    identity.Domain, Version: identity.Version,
			PolicyOrdinal: policyOrdinal, Plane: identity.Plane,
			Quotas: FrozenExtractionPartitionQuotas(), Partitions: []ExtractionPartition{},
		},
		descriptor: SparseDomainDescriptor{
			Domain: identity.Domain, Version: identity.Version,
			PolicyOrdinal: policyOrdinal, Plane: identity.Plane,
			TypedInputs: sparseTypedInputs(manifest, identity),
		},
	}
}

func scanSparseMembers(
	ctx context.Context,
	publication *Publication,
	builders []sparseDomainBuilder,
	metrics *SparseBuildMetrics,
	aggregatePartitions *int,
) error {
	manifest := publication.manifest
	repositoryTargets := make([]*sparseDomainBuilder, 0, len(builders))
	callerTargets := make([]*sparseDomainBuilder, 0, len(builders))
	for index := range builders {
		builder := &builders[index]
		if sparseTypedInputsUnavailable(builder.descriptor.TypedInputs) {
			continue
		}
		switch {
		case builder.identity.Plane == PlaneCaller:
			callerTargets = append(callerTargets, builder)
		case builder.identity.Plane == PlaneRepository || manifest.UnitDigest == "":
			repositoryTargets = append(repositoryTargets, builder)
		}
	}
	for _, member := range manifest.RepositoryMembers {
		if err := scanSparseMember(
			ctx, publication, sparseSourceMember{artifact: member}, repositoryTargets, metrics,
			aggregatePartitions,
		); err != nil {
			return err
		}
	}
	for _, leaf := range manifest.CallerLeaves {
		if err := scanSparseMember(ctx, publication, sparseSourceMember{
			artifact: leaf.Artifact, prefix: leaf.Prefix, prefixBits: leaf.PrefixBits,
		}, callerTargets, metrics, aggregatePartitions); err != nil {
			return err
		}
	}
	seenFocused := make([]bool, len(builders))
	for _, projection := range manifest.LocalProjections {
		if projection.PolicyOrdinal < 0 || projection.PolicyOrdinal >= len(builders) {
			return sparseInvalid("local projection policy ordinal is invalid")
		}
		builder := &builders[projection.PolicyOrdinal]
		if builder.identity.Plane != PlaneLocal || manifest.UnitDigest == "" ||
			projection.Domain != builder.identity.Domain || projection.Version != builder.identity.Version ||
			seenFocused[projection.PolicyOrdinal] {
			return sparseInvalid("local projection identity is invalid")
		}
		seenFocused[projection.PolicyOrdinal] = true
		if sparseTypedInputsUnavailable(builder.descriptor.TypedInputs) {
			continue
		}
		for _, member := range projection.Members {
			if err := scanSparseMember(
				ctx, publication, sparseSourceMember{artifact: member},
				[]*sparseDomainBuilder{builder}, metrics, aggregatePartitions,
			); err != nil {
				return err
			}
		}
	}
	if manifest.UnitDigest != "" {
		for index := range builders {
			if builders[index].identity.Plane == PlaneLocal && !seenFocused[index] {
				return sparseInvalid(
					"local projection %s/%s is missing",
					builders[index].identity.Domain, builders[index].identity.Version,
				)
			}
		}
	}
	return nil
}

func scanSparseMember(
	ctx context.Context,
	publication *Publication,
	source sparseSourceMember,
	targets []*sparseDomainBuilder,
	metrics *SparseBuildMetrics,
	aggregatePartitions *int,
) error {
	if len(targets) == 0 {
		return nil
	}
	counts := make([]int, len(targets))
	declared := make([]int64, len(targets))
	targetsByDomain := make(map[string][]int, len(targets))
	for index, target := range targets {
		targetsByDomain[target.identity.Domain] = append(targetsByDomain[target.identity.Domain], index)
	}
	err := validateArtifactFile(
		ctx, filepath.Join(publication.root, source.artifact.Name), source.artifact,
		func(record Record) error {
			for _, recordDomain := range record.Domains {
				for _, index := range targetsByDomain[recordDomain] {
					target := targets[index]
					if target.identity.Plane == PlaneLocal && publication.manifest.UnitDigest != "" && !record.InUnit {
						return sparseInvalid("local partition contains an out-of-unit record")
					}
					if target.identity.Plane == PlaneCaller && !hashHasPrefix(record.Hash, source.prefix) {
						return sparseInvalid("caller partition contains an out-of-prefix record")
					}
					counts[index]++
					if record.DeclaredBytes > math.MaxInt64-declared[index] {
						return sparseInvalid("admitted byte count overflows")
					}
					declared[index] += record.DeclaredBytes
					if sparseTypedInputPresent(target.descriptor.TypedInputs) {
						if len(target.typedScope) >= PartitionMaxTypedScopeRecords {
							return sparseInvalid(
								"typed document scope exceeds %d records",
								PartitionMaxTypedScopeRecords,
							)
						}
						target.typedScope = append(target.typedScope, sparseTypedSource{
							pathIdentity: sha256.Sum256([]byte(record.Path)),
							path:         record.Path,
							objectID:     record.OID, declaredBytes: record.DeclaredBytes,
						})
					}
				}
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("index sparse candidate member %q: %w", source.artifact.Name, err)
	}
	if metrics != nil {
		metrics.CandidateMembersRead++
		metrics.CandidateBytesRead += source.artifact.ContentBytes
	}
	for index, target := range targets {
		if counts[index] == 0 {
			continue
		}
		if len(target.index.Partitions) >= MaxDomainPartitions ||
			aggregatePartitions == nil || *aggregatePartitions >= MaxSparseAggregatePartitions {
			return sparseInvalid(
				"domain or generation partition count exceeds %d/%d",
				MaxDomainPartitions, MaxSparseAggregatePartitions,
			)
		}
		start := target.descriptor.AdmittedRecords
		member := source.artifact
		partition := ExtractionPartition{
			Schema:                    ExtractionPartitionSchema,
			Repository:                publication.manifest.Repository,
			CandidateManifestDigest:   publication.manifest.Digest,
			CandidateGenerationDigest: publication.manifest.GenerationDigest,
			Domain:                    target.identity.Domain, Version: target.identity.Version, Plane: target.identity.Plane,
			Kind:    PartitionKindCandidateMember,
			Ordinal: len(target.index.Partitions), SourceStart: start,
			SourceEnd: start + counts[index], ByteKind: sparseByteKind(target.identity.Plane),
			Member: &member, CallerPrefix: source.prefix,
			CallerPrefixBits:      source.prefixBits,
			AdmittedRecords:       counts[index],
			AdmittedDeclaredBytes: declared[index],
			Quotas:                FrozenExtractionPartitionQuotas(),
		}
		partition.Digest = sparsePartitionDigest(partition)
		target.index.Partitions = append(target.index.Partitions, partition)
		*aggregatePartitions = *aggregatePartitions + 1
		target.descriptor.AdmittedRecords += counts[index]
		if declared[index] > math.MaxInt64-target.descriptor.AdmittedDeclaredBytes ||
			source.artifact.ContentBytes > math.MaxInt64-target.descriptor.CandidateMemberReadBytes {
			return sparseInvalid("domain totals overflow")
		}
		target.descriptor.AdmittedDeclaredBytes += declared[index]
		target.descriptor.CandidateMemberReadBytes += source.artifact.ContentBytes
	}
	return nil
}

func addSparseTypedInputPartitions(
	manifest Manifest,
	builder *sparseDomainBuilder,
	aggregatePartitions *int,
	aggregateScopeBytes *int64,
) error {
	if builder == nil || sparseTypedInputsUnavailable(builder.descriptor.TypedInputs) {
		return nil
	}
	if sparseTypedInputPresent(builder.descriptor.TypedInputs) {
		typedScope, err := canonicalTypedScope(builder.typedScope)
		if err != nil {
			return err
		}
		if len(typedScope) > MaxSparseTypedScopeBytes || aggregateScopeBytes == nil ||
			int64(len(typedScope)) > MaxSparseAggregateScopeBytes-*aggregateScopeBytes {
			return sparseInvalid("typed source scopes exceed %d/%d bytes", MaxSparseTypedScopeBytes, MaxSparseAggregateScopeBytes)
		}
		builder.typedScopeRaw = typedScope
		builder.descriptor.TypedScope = &SparseTypedScopeDescriptor{
			Name:    sparseTypedScopeName(builder.descriptor.PolicyOrdinal),
			Records: len(builder.typedScope), ContentBytes: int64(len(typedScope)),
			ContentDigest: sparseContentDigest(typedScope),
		}
		*aggregateScopeBytes += int64(len(typedScope))
	}
	for _, input := range builder.descriptor.TypedInputs {
		if !input.Present {
			continue
		}
		if len(builder.index.Partitions) >= MaxDomainPartitions ||
			aggregatePartitions == nil || *aggregatePartitions >= MaxSparseAggregatePartitions {
			return sparseInvalid(
				"domain or generation partition count exceeds %d/%d",
				MaxDomainPartitions, MaxSparseAggregatePartitions,
			)
		}
		typedInput := input
		start := builder.descriptor.AdmittedRecords
		typedScope := *builder.descriptor.TypedScope
		partition := ExtractionPartition{
			Schema:                    ExtractionPartitionSchema,
			Repository:                manifest.Repository,
			CandidateManifestDigest:   manifest.Digest,
			CandidateGenerationDigest: manifest.GenerationDigest,
			Domain:                    builder.identity.Domain, Version: builder.identity.Version,
			Plane: builder.identity.Plane, Kind: PartitionKindTypedInput,
			Ordinal: len(builder.index.Partitions), SourceStart: start, SourceEnd: start,
			ByteKind: "typed_input_declared", TypedInput: &typedInput,
			TypedScope:            &typedScope,
			AdmittedDeclaredBytes: input.DeclaredBytes,
			Quotas:                FrozenExtractionPartitionQuotas(),
		}
		partition.Digest = sparsePartitionDigest(partition)
		builder.index.Partitions = append(builder.index.Partitions, partition)
		*aggregatePartitions = *aggregatePartitions + 1
		if input.DeclaredBytes > math.MaxInt64-builder.descriptor.AdmittedDeclaredBytes {
			return sparseInvalid("domain totals overflow")
		}
		builder.descriptor.AdmittedDeclaredBytes += input.DeclaredBytes
	}
	return nil
}

func sparseTypedInputPresent(inputs []SparseTypedInput) bool {
	for _, input := range inputs {
		if input.Present {
			return true
		}
	}
	return false
}

func canonicalTypedScope(values []sparseTypedSource) ([]byte, error) {
	if len(values) > PartitionMaxTypedScopeRecords {
		return nil, sparseInvalid("typed document scope exceeds %d records", PartitionMaxTypedScopeRecords)
	}
	ordered := slices.Clone(values)
	slices.SortFunc(ordered, func(left, right sparseTypedSource) int {
		switch {
		case left.path < right.path:
			return -1
		case left.path > right.path:
			return 1
		default:
			return 0
		}
	})
	headerBytes := len(sparseTypedScopeMagic) + 4 + len(ordered)*4
	pathBytes := 0
	for _, value := range ordered {
		if !safePath(value.path) || len(value.path) > math.MaxUint16 ||
			value.pathIdentity != sha256.Sum256([]byte(value.path)) ||
			len(value.path) > PartitionMaxTypedScopePathBytes-pathBytes {
			return nil, sparseInvalid("typed document scope exceeds its path envelope")
		}
		pathBytes += len(value.path)
	}
	result := make([]byte, 0, headerBytes+len(ordered)*sparseTypedScopeEntryBase+pathBytes)
	result = append(result, sparseTypedScopeMagic...)
	result = binary.BigEndian.AppendUint32(result, uint32(len(ordered)))
	result = append(result, make([]byte, len(ordered)*4)...)
	for index, value := range ordered {
		if index > 0 && value.path == ordered[index-1].path {
			return nil, sparseInvalid("typed document scope repeats a path identity")
		}
		if !gitobj.IsObjectID(value.objectID) || value.declaredBytes < 0 ||
			value.declaredBytes > PartitionMaxCandidateDeclaredBytes {
			return nil, sparseInvalid("typed document scope has an invalid source identity")
		}
		binary.BigEndian.PutUint32(
			result[len(sparseTypedScopeMagic)+4+index*4:], uint32(len(result)),
		)
		result = append(result, value.pathIdentity[:]...)
		result = binary.BigEndian.AppendUint16(result, uint16(len(value.path)))
		result = append(result, value.path...)
		result = append(result, byte(len(value.objectID)))
		result = append(result, value.objectID...)
		result = append(result, make([]byte, 64-len(value.objectID))...)
		result = binary.BigEndian.AppendUint64(result, uint64(value.declaredBytes))
	}
	return result, nil
}

func finishSparseDomain(builder *sparseDomainBuilder) {
	builder.index.ScheduleDigest = sparseDomainScheduleDigest(builder.index)
	builder.index.Digest = sparseDomainDigest(builder.index)
	builder.descriptor.PartitionCount = len(builder.index.Partitions)
	builder.descriptor.DomainScheduleDigest = builder.index.ScheduleDigest
	builder.descriptor.Availability, builder.descriptor.UnavailableReason = sparseAvailability(
		builder.descriptor.TypedInputs, len(builder.index.Partitions),
	)
}

func sparseTypedInputs(manifest Manifest, identity PolicyIdentity) []SparseTypedInput {
	result := make([]SparseTypedInput, 0, len(identity.TypedInputs))
	for _, kind := range identity.TypedInputs {
		input := SparseTypedInput{Kind: kind}
		for _, candidateInput := range manifest.TypedInputs {
			if candidateInput.Kind == kind && slices.Contains(candidateInput.Domains, identity.Domain) {
				input.Path = candidateInput.Path
				input.ObjectID = candidateInput.OID
				input.DeclaredBytes = candidateInput.DeclaredBytes
				input.Present = candidateInput.Present
				break
			}
		}
		result = append(result, input)
	}
	return result
}

func sparseTypedInputsUnavailable(inputs []SparseTypedInput) bool {
	for _, input := range inputs {
		if !input.Present {
			return true
		}
	}
	return false
}

func sparseByteKind(plane Plane) string {
	switch plane {
	case PlaneCaller:
		return "caller_declared"
	case PlaneLocal:
		return "local_declared"
	default:
		return "repository_declared"
	}
}

func ValidateSparseStage(
	ctx context.Context,
	directory string,
	manifest Manifest,
	trustedRootDigest string,
) error {
	if ctx == nil {
		return errors.New("sparse stage validation requires a context")
	}
	if !validDigest(trustedRootDigest) {
		return errors.New("trusted sparse root digest is required")
	}
	root, _, err := readSparseRoot(filepath.Join(directory, SparseRootFileName))
	if err != nil {
		return err
	}
	if err := validateSparseRoot(root, manifest); err != nil {
		return err
	}
	if root.Digest != trustedRootDigest {
		return fmt.Errorf("%w: root digest differs from trusted authority", ErrSparseStale)
	}
	allowed := map[string]struct{}{SparseRootFileName: {}}
	for _, descriptor := range root.Domains {
		if err := ctx.Err(); err != nil {
			return err
		}
		index, raw, err := readSparseDomain(filepath.Join(directory, descriptor.IndexName))
		if err != nil {
			return err
		}
		if int64(len(raw)) != descriptor.IndexBytes ||
			sparseContentDigest(raw) != descriptor.IndexContentDigest ||
			index.Digest != descriptor.IndexDigest {
			return sparseInvalid("domain index %q differs from its root descriptor", descriptor.IndexName)
		}
		if err := validateSparseDomain(index, descriptor, manifest, true); err != nil {
			return err
		}
		allowed[descriptor.IndexName] = struct{}{}
		if descriptor.TypedScope != nil {
			if _, err := readSparseTypedScope(
				filepath.Join(directory, descriptor.TypedScope.Name), *descriptor.TypedScope,
			); err != nil {
				return err
			}
			allowed[descriptor.TypedScope.Name] = struct{}{}
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) != len(allowed) {
		return sparseInvalid("sparse stage has undeclared entries")
	}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return sparseInvalid("sparse stage entry %q is undeclared or special", entry.Name())
		}
	}
	return nil
}

func OpenSparse(
	ctx context.Context,
	sparseDirectory, candidateRoot string,
	state State,
	expectedRootDigest string,
	metrics *SparseReadMetrics,
) (*SparsePublication, error) {
	if ctx == nil {
		return nil, errors.New("sparse candidate open requires a context")
	}
	if metrics != nil {
		*metrics = SparseReadMetrics{}
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}
	if !validDigest(expectedRootDigest) {
		return nil, errors.New("sparse candidate root digest is required")
	}
	if IsPublishing(candidateRoot, state.Repository) {
		return nil, ErrPublishing
	}
	manifest, err := readManifest(filepath.Join(candidateRoot, state.Manifest))
	if err != nil {
		return nil, err
	}
	if metrics != nil {
		metrics.CandidateManifestReads++
		if raw, marshalErr := sparseCanonical(manifest); marshalErr == nil {
			metrics.ControlBytesRead += int64(len(raw))
		}
	}
	if err := validateManifestIdentity(manifest, Expected{}, &state); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSparseStale, err)
	}
	root, raw, err := readSparseRoot(filepath.Join(sparseDirectory, SparseRootFileName))
	if err != nil {
		return nil, err
	}
	if metrics != nil {
		metrics.RootReads++
		metrics.ControlBytesRead += int64(len(raw))
	}
	if err := validateSparseRoot(root, manifest); err != nil {
		return nil, err
	}
	if root.Digest != expectedRootDigest {
		return nil, fmt.Errorf("%w: root digest differs from expected authority", ErrSparseStale)
	}
	if IsPublishing(candidateRoot, state.Repository) {
		return nil, ErrPublishing
	}
	return &SparsePublication{
		candidateRoot: candidateRoot, sparseRoot: sparseDirectory,
		manifest: manifest, state: state, root: root, metrics: metrics,
	}, nil
}

func (publication *SparsePublication) Root() SparseRoot {
	if publication == nil {
		return SparseRoot{}
	}
	return cloneSparseRoot(publication.root)
}

func (publication *SparsePublication) DomainControl(
	domain, version string,
) (SparseDomainDescriptor, error) {
	if publication == nil {
		return SparseDomainDescriptor{}, errors.New("sparse publication is nil")
	}
	for _, descriptor := range publication.root.Domains {
		if descriptor.Domain == domain && descriptor.Version == version {
			return cloneSparseDescriptor(descriptor), nil
		}
	}
	return SparseDomainDescriptor{}, os.ErrNotExist
}

func (publication *SparsePublication) OpenDomain(
	ctx context.Context,
	domain, version string,
) (*SparseDomain, error) {
	if ctx == nil {
		return nil, errors.New("sparse domain open requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	descriptor, err := publication.DomainControl(domain, version)
	if err != nil {
		return nil, err
	}
	index, raw, err := readSparseDomain(filepath.Join(publication.sparseRoot, descriptor.IndexName))
	if err != nil {
		return nil, err
	}
	if publication.metrics != nil {
		publication.metrics.DomainIndexReads++
		publication.metrics.ControlBytesRead += int64(len(raw))
	}
	if int64(len(raw)) != descriptor.IndexBytes ||
		sparseContentDigest(raw) != descriptor.IndexContentDigest ||
		index.Digest != descriptor.IndexDigest {
		return nil, sparseInvalid("domain index differs from its root descriptor")
	}
	if err := validateSparseDomain(index, descriptor, publication.manifest, false); err != nil {
		return nil, err
	}
	return &SparseDomain{publication: publication, descriptor: descriptor, index: index}, nil
}

func (domain *SparseDomain) Partitions() []ExtractionPartition {
	if domain == nil {
		return nil
	}
	return cloneSparsePartitions(domain.index.Partitions)
}

func (domain *SparseDomain) SchedulePage(
	startOrdinal, maxPartitions int,
) (CoalescedSchedulePage, error) {
	if domain == nil || domain.publication == nil {
		return CoalescedSchedulePage{}, errors.New("sparse domain is nil")
	}
	if domain.descriptor.Availability == "unavailable" {
		return CoalescedSchedulePage{}, ErrSparseUnavailable
	}
	if startOrdinal < 0 || startOrdinal > len(domain.index.Partitions) ||
		maxPartitions <= 0 || maxPartitions > MaxSchedulePagePartitions {
		return CoalescedSchedulePage{}, errors.New("invalid sparse schedule page request")
	}
	end := min(len(domain.index.Partitions), startOrdinal+maxPartitions)
	page := CoalescedSchedulePage{
		Schema:               SchedulePageSchema,
		RootScheduleDigest:   domain.publication.root.ScheduleDigest,
		DomainScheduleDigest: domain.index.ScheduleDigest,
		Domain:               domain.index.Domain, Version: domain.index.Version,
		StartOrdinal: startOrdinal, EndOrdinal: end,
		Partitions: make([]ScheduledPartition, 0, end-startOrdinal),
		Complete:   end == len(domain.index.Partitions),
	}
	for _, partition := range domain.index.Partitions[startOrdinal:end] {
		page.Partitions = append(page.Partitions, ScheduledPartition{
			Ordinal: partition.Ordinal, Kind: partition.Kind, PartitionDigest: partition.Digest,
		})
	}
	page.Digest = sparseSchedulePageDigest(page)
	raw, err := sparseCanonical(page)
	if err != nil {
		return CoalescedSchedulePage{}, err
	}
	if len(raw) > MaxSchedulePageBytes {
		return CoalescedSchedulePage{}, sparseInvalid("schedule page exceeds %d bytes", MaxSchedulePageBytes)
	}
	return page, nil
}

func (domain *SparseDomain) ReadPartition(
	ctx context.Context,
	ordinal int,
	visit func(Record) error,
) error {
	if domain == nil || domain.publication == nil || visit == nil ||
		ordinal < 0 || ordinal >= len(domain.index.Partitions) {
		return errors.New("invalid sparse partition read")
	}
	if ctx == nil {
		return errors.New("sparse partition read requires a context")
	}
	if domain.descriptor.Availability == "unavailable" {
		return ErrSparseUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	publication := domain.publication
	if IsPublishing(publication.candidateRoot, publication.state.Repository) {
		return ErrPublishing
	}
	partition := domain.index.Partitions[ordinal]
	if partition.Kind == PartitionKindTypedInput {
		return ErrSparseTypedInput
	}
	if partition.Member == nil {
		return sparseInvalid("candidate partition has no member")
	}
	count, declared := 0, int64(0)
	err := validateArtifactFile(
		ctx, filepath.Join(publication.candidateRoot, partition.Member.Name), *partition.Member,
		func(record Record) error {
			if !slices.Contains(record.Domains, partition.Domain) {
				return nil
			}
			if partition.Plane == PlaneLocal && publication.manifest.UnitDigest != "" && !record.InUnit {
				return sparseInvalid("local sparse read found an out-of-unit record")
			}
			if partition.Plane == PlaneCaller && !hashHasPrefix(record.Hash, partition.CallerPrefix) {
				return sparseInvalid("caller sparse read found an out-of-prefix record")
			}
			count++
			if record.DeclaredBytes > math.MaxInt64-declared {
				return sparseInvalid("partition declared bytes overflow")
			}
			declared += record.DeclaredBytes
			record.Required = slices.Contains(record.RequiredDomains, partition.Domain)
			return visit(record)
		},
	)
	if err != nil {
		return fmt.Errorf("read sparse candidate member %q: %w", partition.Member.Name, err)
	}
	if count != partition.AdmittedRecords || declared != partition.AdmittedDeclaredBytes {
		return sparseInvalid("selected member no longer matches admitted partition totals")
	}
	if publication.metrics != nil {
		publication.metrics.CandidateMemberReads++
		publication.metrics.CandidateBytesRead += partition.Member.ContentBytes
	}
	if IsPublishing(publication.candidateRoot, publication.state.Repository) {
		return ErrPublishing
	}
	return nil
}

func (domain *SparseDomain) TypedInputPartition(ordinal int) (SparseTypedInput, error) {
	if domain == nil || domain.publication == nil || ordinal < 0 || ordinal >= len(domain.index.Partitions) {
		return SparseTypedInput{}, errors.New("invalid sparse typed-input partition read")
	}
	if domain.descriptor.Availability == "unavailable" {
		return SparseTypedInput{}, ErrSparseUnavailable
	}
	partition := domain.index.Partitions[ordinal]
	if partition.Kind != PartitionKindTypedInput || partition.TypedInput == nil {
		return SparseTypedInput{}, errors.New("sparse partition is not a typed input")
	}
	return *partition.TypedInput, nil
}

// TypedSourceScope reads the one digest-bound source-scope control used by a
// typed partition. The returned lookup supports exact document admission and
// immutable Git object acquisition without reopening candidate members.
func (domain *SparseDomain) TypedSourceScope(
	ctx context.Context,
	ordinal int,
) (SparseTypedSourceScope, error) {
	if ctx == nil {
		return SparseTypedSourceScope{}, errors.New("typed source scope requires a context")
	}
	if domain == nil || domain.publication == nil || ordinal < 0 || ordinal >= len(domain.index.Partitions) {
		return SparseTypedSourceScope{}, errors.New("invalid sparse typed source scope read")
	}
	partition := domain.index.Partitions[ordinal]
	if partition.Kind != PartitionKindTypedInput || partition.TypedScope == nil ||
		domain.descriptor.TypedScope == nil || *partition.TypedScope != *domain.descriptor.TypedScope {
		return SparseTypedSourceScope{}, errors.New("sparse partition has no typed source scope")
	}
	if domain.descriptor.Availability == "unavailable" {
		return SparseTypedSourceScope{}, ErrSparseUnavailable
	}
	if err := ctx.Err(); err != nil {
		return SparseTypedSourceScope{}, err
	}
	publication := domain.publication
	if IsPublishing(publication.candidateRoot, publication.state.Repository) {
		return SparseTypedSourceScope{}, ErrPublishing
	}
	scope, err := readSparseTypedScope(
		filepath.Join(publication.sparseRoot, partition.TypedScope.Name), *partition.TypedScope,
	)
	if err != nil {
		return SparseTypedSourceScope{}, err
	}
	if publication.metrics != nil {
		publication.metrics.TypedScopeReads++
		publication.metrics.ControlBytesRead += int64(len(scope.raw))
	}
	if IsPublishing(publication.candidateRoot, publication.state.Repository) {
		return SparseTypedSourceScope{}, ErrPublishing
	}
	return scope, nil
}

// Contains reports whether path is in the exact admitted typed source scope.
func (scope SparseTypedSourceScope) Contains(path string) bool {
	_, _, ok := scope.source(path)
	return ok
}

// Source resolves an admitted path to its immutable object identity and size.
func (scope SparseTypedSourceScope) Source(path string) (string, int64, bool) {
	return scope.source(path)
}

func PartitionResultIdentity(partition ExtractionPartition, resultDigest string) (string, error) {
	if err := validateSparsePartition(partition, nil, partition.SourceStart); err != nil || !validDigest(resultDigest) {
		return "", errors.New("partition result identity is invalid")
	}
	return partitionResultIdentityFromDigests(partition.Digest, resultDigest), nil
}

// partitionResultIdentityFromDigests is the established T40.8 nonproduct
// identity. T40.9 results deliberately retain this v1 envelope: their result
// digest now binds the stronger plan and authority fields without creating a
// competing scheduler identity.
func partitionResultIdentityFromDigests(partitionDigest, resultDigest string) string {
	payload, _ := json.Marshal(struct {
		Schema          string `json:"schema"`
		PartitionDigest string `json:"partition_digest"`
		ResultDigest    string `json:"result_digest"`
	}{Schema: "phebs-extraction-partition-result-identity-v1", PartitionDigest: partitionDigest, ResultDigest: resultDigest})
	return digest("phebs-extraction-partition-result-identity-v1\x00", payload)
}

func ClosePartitionDeadline(partition ExtractionPartition) (DeadlineClosure, error) {
	if err := validateSparsePartition(partition, nil, partition.SourceStart); err != nil {
		return DeadlineClosure{}, err
	}
	closure := DeadlineClosure{
		Schema: DeadlineClosureSchema, PartitionDigest: partition.Digest,
		Disposition:          "deadline_exhausted",
		DeadlineMilliseconds: FrozenExtractionPartitionQuotas().DeadlineMilliseconds,
	}
	closure.Digest = sparseDeadlineDigest(closure)
	return closure, nil
}

func validateSparseRoot(root SparseRoot, manifest Manifest) error {
	if root.Schema != SparseRootSchema || root.CandidateSchema != ManifestSchema ||
		root.Repository != manifest.Repository || root.Commit != manifest.Commit ||
		root.UnitDigest != manifest.UnitDigest || root.PolicyDigest != manifest.PolicyDigest ||
		root.CandidateGenerationDigest != manifest.GenerationDigest ||
		root.CandidateManifestDigest != manifest.Digest ||
		root.Quotas != FrozenExtractionPartitionQuotas() ||
		len(root.Domains) != len(manifest.Policies) || len(root.Domains) > MaxSparseDomains ||
		root.ScheduleDigest != sparseScheduleDigest(root) || root.Digest != sparseRootDigest(root) {
		return sparseInvalid("root envelope or identity is invalid")
	}
	seenNames := make(map[string]struct{}, len(root.Domains))
	aggregatePartitions := 0
	aggregateIndexBytes := int64(0)
	aggregateScopeBytes := int64(0)
	for ordinal, descriptor := range root.Domains {
		identity := manifest.Policies[ordinal]
		if descriptor.Domain != identity.Domain || descriptor.Version != identity.Version ||
			descriptor.PolicyOrdinal != ordinal || descriptor.Plane != identity.Plane ||
			descriptor.IndexName != sparseDomainName(ordinal) ||
			descriptor.PartitionCount < 0 || descriptor.PartitionCount > MaxDomainPartitions ||
			descriptor.AdmittedRecords < 0 || descriptor.AdmittedDeclaredBytes < 0 ||
			descriptor.CandidateMemberReadBytes < 0 || descriptor.IndexBytes <= 0 ||
			descriptor.IndexBytes > MaxSparseDomainBytes || !validDigest(descriptor.IndexDigest) ||
			!validDigest(descriptor.IndexContentDigest) || !validDigest(descriptor.DomainScheduleDigest) ||
			!validSparseAvailability(descriptor.Availability, descriptor.UnavailableReason) ||
			!equalSparseTypedInputs(descriptor.TypedInputs, sparseTypedInputs(manifest, identity)) {
			return sparseInvalid("domain descriptor %d is invalid", ordinal)
		}
		wantAvailability, wantReason := sparseAvailability(descriptor.TypedInputs, descriptor.PartitionCount)
		if descriptor.Availability != wantAvailability || descriptor.UnavailableReason != wantReason {
			return sparseInvalid("domain descriptor %d availability is invalid", ordinal)
		}
		for _, input := range descriptor.TypedInputs {
			if !validSparseTypedInput(input, input.Present) {
				return sparseInvalid("domain descriptor %d typed input is invalid", ordinal)
			}
		}
		hasTypedInput := sparseTypedInputPresent(descriptor.TypedInputs)
		if hasTypedInput != (descriptor.TypedScope != nil) ||
			(descriptor.TypedScope != nil && !validSparseTypedScopeDescriptor(
				*descriptor.TypedScope, ordinal, descriptor.AdmittedRecords,
			)) {
			return sparseInvalid("domain descriptor %d typed scope is invalid", ordinal)
		}
		if descriptor.PartitionCount > MaxSparseAggregatePartitions-aggregatePartitions ||
			descriptor.IndexBytes > MaxSparseAggregateIndexBytes-aggregateIndexBytes {
			return sparseInvalid("aggregate sparse controls exceed their partition or byte limit")
		}
		aggregatePartitions += descriptor.PartitionCount
		aggregateIndexBytes += descriptor.IndexBytes
		if descriptor.TypedScope != nil {
			if descriptor.TypedScope.ContentBytes > MaxSparseAggregateScopeBytes-aggregateScopeBytes {
				return sparseInvalid("aggregate typed source scopes exceed their byte limit")
			}
			aggregateScopeBytes += descriptor.TypedScope.ContentBytes
		}
		if !sparseAggregateWithinLimits(aggregatePartitions, aggregateIndexBytes) {
			return sparseInvalid("aggregate sparse controls exceed their partition or byte limit")
		}
		if descriptor.Availability == "unavailable" &&
			(descriptor.PartitionCount != 0 || descriptor.AdmittedRecords != 0 ||
				descriptor.AdmittedDeclaredBytes != 0 || descriptor.CandidateMemberReadBytes != 0) {
			return sparseInvalid("unavailable domain descriptor %d carries admitted work", ordinal)
		}
		if _, duplicate := seenNames[descriptor.IndexName]; duplicate {
			return sparseInvalid("duplicate domain index name")
		}
		seenNames[descriptor.IndexName] = struct{}{}
		if descriptor.TypedScope != nil {
			if _, duplicate := seenNames[descriptor.TypedScope.Name]; duplicate {
				return sparseInvalid("duplicate typed source scope name")
			}
			seenNames[descriptor.TypedScope.Name] = struct{}{}
		}
	}
	return nil
}

func validateSparseDomain(
	index SparseDomainIndex,
	descriptor SparseDomainDescriptor,
	manifest Manifest,
	validateMembership bool,
) error {
	if index.Schema != SparseDomainSchema || index.Repository != manifest.Repository ||
		index.CandidateManifestDigest != manifest.Digest ||
		index.CandidateGenerationDigest != manifest.GenerationDigest ||
		index.Domain != descriptor.Domain || index.Version != descriptor.Version ||
		index.PolicyOrdinal != descriptor.PolicyOrdinal || index.Plane != descriptor.Plane ||
		index.Quotas != FrozenExtractionPartitionQuotas() || index.Partitions == nil ||
		len(index.Partitions) != descriptor.PartitionCount || len(index.Partitions) > MaxDomainPartitions ||
		index.ScheduleDigest != descriptor.DomainScheduleDigest ||
		index.ScheduleDigest != sparseDomainScheduleDigest(index) ||
		(index.Digest != descriptor.IndexDigest && descriptor.IndexDigest != "") ||
		index.Digest != sparseDomainDigest(index) {
		return sparseInvalid("domain index envelope is invalid")
	}
	records, declared, memberBytes := 0, int64(0), int64(0)
	seenMembers := make(map[string]struct{}, len(index.Partitions))
	var sources []sparseSourceMember
	var sourceOrdinals map[string]int
	if validateMembership {
		var err error
		sources, err = sparseManifestSources(manifest, descriptor)
		if err != nil {
			return err
		}
		sourceOrdinals = make(map[string]int, len(sources))
		for ordinal, source := range sources {
			sourceOrdinals[source.artifact.Name] = ordinal
		}
	}
	lastSourceOrdinal := -1
	typedOrdinal := 0
	typedPhase := false
	for ordinal, partition := range index.Partitions {
		if err := validateSparsePartition(partition, &index, records); err != nil {
			return err
		}
		if partition.Ordinal != ordinal {
			return sparseInvalid("partition ordering is invalid")
		}
		switch partition.Kind {
		case PartitionKindCandidateMember:
			if typedPhase || partition.Member == nil {
				return sparseInvalid("candidate partition follows typed work or has no member")
			}
			if _, duplicate := seenMembers[partition.Member.Name]; duplicate {
				return sparseInvalid("partition member is duplicated")
			}
			seenMembers[partition.Member.Name] = struct{}{}
			if validateMembership {
				sourceOrdinal, ok := sourceOrdinals[partition.Member.Name]
				if !ok || sourceOrdinal <= lastSourceOrdinal ||
					*partition.Member != sources[sourceOrdinal].artifact ||
					partition.CallerPrefix != sources[sourceOrdinal].prefix ||
					partition.CallerPrefixBits != sources[sourceOrdinal].prefixBits {
					return sparseInvalid("partition member is absent, forged, or out of order")
				}
				lastSourceOrdinal = sourceOrdinal
			}
			records = partition.SourceEnd
			if partition.Member.ContentBytes > math.MaxInt64-memberBytes {
				return sparseInvalid("domain totals overflow")
			}
			memberBytes += partition.Member.ContentBytes
		case PartitionKindTypedInput:
			typedPhase = true
			for typedOrdinal < len(descriptor.TypedInputs) &&
				!descriptor.TypedInputs[typedOrdinal].Present {
				typedOrdinal++
			}
			if typedOrdinal >= len(descriptor.TypedInputs) || partition.TypedInput == nil ||
				*partition.TypedInput != descriptor.TypedInputs[typedOrdinal] ||
				partition.TypedScope == nil || descriptor.TypedScope == nil ||
				*partition.TypedScope != *descriptor.TypedScope {
				return sparseInvalid("typed partition is missing, forged, or out of order")
			}
			typedOrdinal++
		default:
			return sparseInvalid("partition kind is invalid")
		}
		if partition.AdmittedDeclaredBytes > math.MaxInt64-declared {
			return sparseInvalid("domain totals overflow")
		}
		declared += partition.AdmittedDeclaredBytes
	}
	for typedOrdinal < len(descriptor.TypedInputs) && !descriptor.TypedInputs[typedOrdinal].Present {
		typedOrdinal++
	}
	if typedOrdinal != len(descriptor.TypedInputs) {
		return sparseInvalid("present typed input has no partition")
	}
	if records != descriptor.AdmittedRecords || declared != descriptor.AdmittedDeclaredBytes ||
		memberBytes != descriptor.CandidateMemberReadBytes {
		return sparseInvalid("domain descriptor totals disagree with its index")
	}
	return nil
}

func sparseManifestSources(
	manifest Manifest,
	descriptor SparseDomainDescriptor,
) ([]sparseSourceMember, error) {
	switch {
	case descriptor.Plane == PlaneCaller:
		result := make([]sparseSourceMember, 0, len(manifest.CallerLeaves))
		for _, leaf := range manifest.CallerLeaves {
			result = append(result, sparseSourceMember{
				artifact: leaf.Artifact, prefix: leaf.Prefix, prefixBits: leaf.PrefixBits,
			})
		}
		return result, nil
	case descriptor.Plane == PlaneLocal && manifest.UnitDigest != "":
		for _, projection := range manifest.LocalProjections {
			if projection.Domain == descriptor.Domain && projection.Version == descriptor.Version &&
				projection.PolicyOrdinal == descriptor.PolicyOrdinal {
				result := make([]sparseSourceMember, 0, len(projection.Members))
				for _, member := range projection.Members {
					result = append(result, sparseSourceMember{artifact: member})
				}
				return result, nil
			}
		}
		return nil, sparseInvalid("local projection is absent from the candidate manifest")
	default:
		result := make([]sparseSourceMember, 0, len(manifest.RepositoryMembers))
		for _, member := range manifest.RepositoryMembers {
			result = append(result, sparseSourceMember{artifact: member})
		}
		return result, nil
	}
}

func validateSparsePartition(
	partition ExtractionPartition,
	index *SparseDomainIndex,
	wantStart int,
) error {
	quotas := FrozenExtractionPartitionQuotas()
	if partition.Schema != ExtractionPartitionSchema || !safeRepository(partition.Repository) ||
		!validDigest(partition.CandidateManifestDigest) ||
		!validDigest(partition.CandidateGenerationDigest) ||
		partition.Domain == "" || partition.Version == "" ||
		(partition.Plane != PlaneRepository && partition.Plane != PlaneLocal && partition.Plane != PlaneCaller) ||
		partition.Ordinal < 0 || partition.SourceStart != wantStart || partition.Quotas != quotas ||
		partition.Digest != sparsePartitionDigest(partition) {
		return sparseInvalid("partition %d is invalid", partition.Ordinal)
	}
	if index != nil && (partition.Repository != index.Repository ||
		partition.CandidateManifestDigest != index.CandidateManifestDigest ||
		partition.CandidateGenerationDigest != index.CandidateGenerationDigest ||
		partition.Domain != index.Domain || partition.Version != index.Version ||
		partition.Plane != index.Plane) {
		return sparseInvalid("partition %d crosses its domain identity", partition.Ordinal)
	}
	switch partition.Kind {
	case PartitionKindCandidateMember:
		if partition.Member == nil || partition.TypedInput != nil ||
			partition.TypedScope != nil ||
			partition.SourceEnd <= partition.SourceStart ||
			partition.SourceEnd-partition.SourceStart != partition.AdmittedRecords ||
			partition.AdmittedRecords <= 0 || partition.AdmittedRecords > partition.Member.RecordCount ||
			partition.AdmittedRecords > quotas.CandidateRecords ||
			partition.AdmittedDeclaredBytes < 0 ||
			partition.AdmittedDeclaredBytes > partition.Member.DeclaredBytes ||
			partition.Member.RecordCount <= 0 || partition.Member.RecordCount > quotas.CandidateRecords ||
			partition.Member.Ordinal < 0 || filepath.Base(partition.Member.Name) != partition.Member.Name ||
			partition.Member.DeclaredBytes < 0 ||
			partition.Member.DeclaredBytes > quotas.CandidateDeclaredBytes ||
			partition.Member.ContentBytes <= 0 ||
			partition.Member.ContentBytes > quotas.CandidateContentBytes ||
			!validDigest(partition.Member.ContentDigest) ||
			partition.ByteKind != sparseByteKind(partition.Plane) {
			return sparseInvalid("candidate partition %d is invalid", partition.Ordinal)
		}
		if partition.Plane == PlaneCaller {
			if partition.CallerPrefixBits <= 0 || partition.CallerPrefixBits > sha256.Size*8 ||
				len(partition.CallerPrefix) != partition.CallerPrefixBits ||
				!sparsePrefixIsCanonical(partition.CallerPrefix) {
				return sparseInvalid("caller partition prefix is invalid")
			}
		} else if partition.CallerPrefix != "" || partition.CallerPrefixBits != 0 {
			return sparseInvalid("non-caller partition carries a caller range")
		}
	case PartitionKindTypedInput:
		if partition.Member != nil || partition.TypedInput == nil ||
			partition.TypedScope == nil ||
			partition.SourceEnd != partition.SourceStart || partition.AdmittedRecords != 0 ||
			partition.ByteKind != "typed_input_declared" ||
			partition.CallerPrefix != "" || partition.CallerPrefixBits != 0 ||
			!validSparseTypedInput(*partition.TypedInput, true) ||
			!validSparseTypedScopeDescriptor(*partition.TypedScope, -1, -1) ||
			partition.AdmittedDeclaredBytes != partition.TypedInput.DeclaredBytes ||
			partition.AdmittedDeclaredBytes > quotas.TypedInputBytes {
			return sparseInvalid("typed-input partition %d is invalid", partition.Ordinal)
		}
	default:
		return sparseInvalid("partition kind is invalid")
	}
	return nil
}

func validSparseTypedScopeDescriptor(
	descriptor SparseTypedScopeDescriptor,
	ordinal int,
	records int,
) bool {
	if ordinal >= 0 && descriptor.Name != sparseTypedScopeName(ordinal) {
		return false
	}
	if filepath.Base(descriptor.Name) != descriptor.Name ||
		descriptor.Records < 0 || descriptor.Records > PartitionMaxTypedScopeRecords ||
		descriptor.ContentBytes < int64(len(sparseTypedScopeMagic)+4+descriptor.Records*(4+sparseTypedScopeEntryBase+1)) ||
		descriptor.ContentBytes <= 0 || descriptor.ContentBytes > MaxSparseTypedScopeBytes ||
		!validDigest(descriptor.ContentDigest) {
		return false
	}
	if descriptor.Records == 0 && descriptor.ContentBytes != int64(len(sparseTypedScopeMagic)+4) {
		return false
	}
	return records < 0 || descriptor.Records == records
}

func validSparseTypedInput(input SparseTypedInput, requirePresent bool) bool {
	if input.Kind == "" || input.DeclaredBytes < 0 || input.DeclaredBytes > PartitionMaxTypedInputBytes {
		return false
	}
	if requirePresent || input.Present {
		return input.Present && safePath(input.Path) && gitobj.IsObjectID(input.ObjectID)
	}
	return (input.Path == "" || safePath(input.Path)) &&
		input.ObjectID == "" && input.DeclaredBytes == 0
}

func sparsePrefixIsCanonical(prefix string) bool {
	for _, current := range prefix {
		if current != '0' && current != '1' {
			return false
		}
	}
	return true
}

func validSparseAvailability(state, reason string) bool {
	switch state {
	case "admitted", "empty":
		return reason == ""
	case "unavailable":
		return reason == "typed_input_absent"
	default:
		return false
	}
}

func sparseAvailability(inputs []SparseTypedInput, partitionCount int) (string, string) {
	for _, input := range inputs {
		if !input.Present {
			return "unavailable", "typed_input_absent"
		}
	}
	if partitionCount == 0 {
		if len(inputs) > 0 {
			return "invalid", ""
		}
		return "empty", ""
	}
	return "admitted", ""
}

func equalSparseTypedInputs(left, right []SparseTypedInput) bool {
	return slices.Equal(left, right)
}

func readSparseRoot(filePath string) (SparseRoot, []byte, error) {
	var root SparseRoot
	raw, err := readSparseJSON(filePath, MaxSparseRootBytes, &root)
	return root, raw, err
}

func writeSparseControl(filePath string, payload []byte) (resultErr error) {
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return err
	}
	return file.Sync()
}

func readSparseDomain(filePath string) (SparseDomainIndex, []byte, error) {
	var index SparseDomainIndex
	raw, err := readSparseJSON(filePath, MaxSparseDomainBytes, &index)
	return index, raw, err
}

func readSparseTypedScope(
	filePath string,
	descriptor SparseTypedScopeDescriptor,
) (SparseTypedSourceScope, error) {
	if !validSparseTypedScopeDescriptor(descriptor, -1, -1) {
		return SparseTypedSourceScope{}, sparseInvalid("typed source scope descriptor is invalid")
	}
	raw, err := readSparseBytes(filePath, MaxSparseTypedScopeBytes)
	if err != nil {
		return SparseTypedSourceScope{}, err
	}
	if int64(len(raw)) != descriptor.ContentBytes ||
		sparseContentDigest(raw) != descriptor.ContentDigest ||
		!validSparseTypedScope(raw, descriptor.Records) {
		return SparseTypedSourceScope{}, sparseInvalid("typed source scope differs from its descriptor")
	}
	return SparseTypedSourceScope{raw: raw, records: descriptor.Records}, nil
}

func validSparseTypedScope(raw []byte, records int) bool {
	headerBytes := len(sparseTypedScopeMagic) + 4 + records*4
	if records < 0 || records > PartitionMaxTypedScopeRecords ||
		len(raw) < headerBytes+records*sparseTypedScopeEntryBase ||
		!bytes.Equal(raw[:len(sparseTypedScopeMagic)], []byte(sparseTypedScopeMagic)) ||
		binary.BigEndian.Uint32(raw[len(sparseTypedScopeMagic):len(sparseTypedScopeMagic)+4]) != uint32(records) {
		return false
	}
	previousPath := ""
	pathBytes := 0
	cursor := headerBytes
	for ordinal := 0; ordinal < records; ordinal++ {
		offset := int(binary.BigEndian.Uint32(
			raw[len(sparseTypedScopeMagic)+4+ordinal*4:],
		))
		if offset != cursor || offset+sparseTypedScopeEntryBase > len(raw) {
			return false
		}
		identity := raw[offset : offset+sha256.Size]
		pathLength := int(binary.BigEndian.Uint16(raw[offset+sha256.Size:]))
		pathStart := offset + sha256.Size + 2
		pathEnd := pathStart + pathLength
		if pathLength == 0 || pathEnd+1+64+8 > len(raw) ||
			pathLength > PartitionMaxTypedScopePathBytes-pathBytes {
			return false
		}
		path := string(raw[pathStart:pathEnd])
		pathIdentity := sha256.Sum256([]byte(path))
		if !safePath(path) || !bytes.Equal(identity, pathIdentity[:]) ||
			(ordinal > 0 && previousPath >= path) {
			return false
		}
		previousPath = path
		pathBytes += pathLength
		oidLength := int(raw[pathEnd])
		oidStart := pathEnd + 1
		if oidLength != 40 && oidLength != 64 ||
			!gitobj.IsObjectID(string(raw[oidStart:oidStart+oidLength])) {
			return false
		}
		for _, padding := range raw[oidStart+oidLength : oidStart+64] {
			if padding != 0 {
				return false
			}
		}
		declared := binary.BigEndian.Uint64(raw[oidStart+64 : oidStart+64+8])
		if declared > uint64(PartitionMaxCandidateDeclaredBytes) {
			return false
		}
		cursor = oidStart + 64 + 8
	}
	return cursor == len(raw)
}

func (scope SparseTypedSourceScope) source(path string) (string, int64, bool) {
	headerBytes := len(sparseTypedScopeMagic) + 4 + scope.records*4
	if !safePath(path) || scope.records < 0 || scope.records > PartitionMaxTypedScopeRecords ||
		len(scope.raw) < headerBytes+scope.records*sparseTypedScopeEntryBase {
		return "", 0, false
	}
	low, high := 0, scope.records
	for low < high {
		middle := low + (high-low)/2
		offset := scope.offset(middle)
		pathLength := int(binary.BigEndian.Uint16(scope.raw[offset+sha256.Size:]))
		pathStart := offset + sha256.Size + 2
		current := string(scope.raw[pathStart : pathStart+pathLength])
		if current < path {
			low = middle + 1
		} else {
			high = middle
		}
	}
	if low >= scope.records {
		return "", 0, false
	}
	offset := scope.offset(low)
	pathLength := int(binary.BigEndian.Uint16(scope.raw[offset+sha256.Size:]))
	pathStart := offset + sha256.Size + 2
	if string(scope.raw[pathStart:pathStart+pathLength]) != path {
		return "", 0, false
	}
	oidLengthOffset := pathStart + pathLength
	oidLength := int(scope.raw[oidLengthOffset])
	oidStart := oidLengthOffset + 1
	declared := binary.BigEndian.Uint64(scope.raw[oidStart+64 : oidStart+64+8])
	return string(scope.raw[oidStart : oidStart+oidLength]), int64(declared), true
}

func (scope SparseTypedSourceScope) offset(ordinal int) int {
	return int(binary.BigEndian.Uint32(
		scope.raw[len(sparseTypedScopeMagic)+4+ordinal*4:],
	))
}

// Records returns the exact number of admitted typed source identities.
func (scope SparseTypedSourceScope) Records() int { return scope.records }

// Walk visits every canonical admitted path in path-identity order.
func (scope SparseTypedSourceScope) Walk(
	ctx context.Context,
	visit func(string) error,
) error {
	if ctx == nil || visit == nil {
		return errors.New("typed source scope walk is invalid")
	}
	for ordinal := 0; ordinal < scope.records; ordinal++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		offset := scope.offset(ordinal)
		pathLength := int(binary.BigEndian.Uint16(scope.raw[offset+sha256.Size:]))
		pathStart := offset + sha256.Size + 2
		if err := visit(string(scope.raw[pathStart : pathStart+pathLength])); err != nil {
			return err
		}
	}
	return nil
}

func readSparseBytes(filePath string, limit int64) (_ []byte, resultErr error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > limit {
		return nil, sparseInvalid("control is special, empty, or oversized")
	}
	source, err := openStableRegularFile(filePath, info)
	if err != nil {
		return nil, sparseInvalid("open control: %v", err)
	}
	defer func() {
		if closeErr := source.file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(source.file, limit+1))
	if err != nil {
		return nil, err
	}
	if err := source.verifyAfterRead(int64(len(raw))); err != nil {
		return nil, sparseInvalid("control changed during read: %v", err)
	}
	if int64(len(raw)) > limit {
		return nil, sparseInvalid("control exceeds %d bytes", limit)
	}
	return raw, nil
}

func readSparseJSON(filePath string, limit int64, target any) (_ []byte, resultErr error) {
	raw, err := readSparseBytes(filePath, limit)
	if err != nil {
		return nil, err
	}
	if err := strictDecode(bytes.NewReader(raw), limit, target); err != nil {
		return nil, sparseInvalid("decode control: %v", err)
	}
	canonical, err := sparseCanonical(target)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, sparseInvalid("control is not canonical")
	}
	return raw, nil
}

func sparseCanonical(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func sparseContentDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sparsePartitionDigest(partition ExtractionPartition) string {
	copy := partition
	copy.Digest = ""
	raw, _ := json.Marshal(copy)
	return digest("phebs-extraction-partition-v1\x00", raw)
}

func sparseDomainScheduleDigest(index SparseDomainIndex) string {
	payload := struct {
		Schema     string   `json:"schema"`
		Repository string   `json:"repository"`
		Manifest   string   `json:"candidate_manifest_digest"`
		Domain     string   `json:"domain"`
		Version    string   `json:"version"`
		Partitions []string `json:"partitions"`
	}{
		Schema: "phebs-extraction-domain-schedule-v1", Repository: index.Repository,
		Manifest: index.CandidateManifestDigest, Domain: index.Domain, Version: index.Version,
		Partitions: make([]string, 0, len(index.Partitions)),
	}
	for _, partition := range index.Partitions {
		payload.Partitions = append(payload.Partitions, partition.Digest)
	}
	raw, _ := json.Marshal(payload)
	return digest("phebs-extraction-domain-schedule-v1\x00", raw)
}

func sparseDomainDigest(index SparseDomainIndex) string {
	copy := index
	copy.Digest = ""
	raw, _ := json.Marshal(copy)
	return digest("phebs-candidate-domain-index-v1\x00", raw)
}

func sparseScheduleDigest(root SparseRoot) string {
	payload := struct {
		Schema   string                    `json:"schema"`
		Manifest string                    `json:"candidate_manifest_digest"`
		Quotas   ExtractionPartitionQuotas `json:"quotas"`
		Domains  []string                  `json:"domains"`
	}{
		Schema:   "phebs-extraction-generation-schedule-v1",
		Manifest: root.CandidateManifestDigest, Quotas: root.Quotas,
		Domains: make([]string, 0, len(root.Domains)),
	}
	for _, domain := range root.Domains {
		payload.Domains = append(payload.Domains, domain.DomainScheduleDigest)
	}
	raw, _ := json.Marshal(payload)
	return digest("phebs-extraction-generation-schedule-v1\x00", raw)
}

func sparseRootDigest(root SparseRoot) string {
	copy := root
	copy.Digest = ""
	raw, _ := json.Marshal(copy)
	return digest("phebs-candidate-partition-root-v1\x00", raw)
}

func sparseSchedulePageDigest(page CoalescedSchedulePage) string {
	copy := page
	copy.Digest = ""
	raw, _ := json.Marshal(copy)
	return digest("phebs-extraction-partition-schedule-page-v1\x00", raw)
}

func sparseDeadlineDigest(closure DeadlineClosure) string {
	copy := closure
	copy.Digest = ""
	raw, _ := json.Marshal(copy)
	return digest("phebs-extraction-partition-deadline-v1\x00", raw)
}

func sparseInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSparseRoot, fmt.Sprintf(format, args...))
}

func cloneSparseRoot(root SparseRoot) SparseRoot {
	result := root
	result.Domains = make([]SparseDomainDescriptor, len(root.Domains))
	for index, descriptor := range root.Domains {
		result.Domains[index] = cloneSparseDescriptor(descriptor)
	}
	return result
}

func cloneSparseDescriptor(descriptor SparseDomainDescriptor) SparseDomainDescriptor {
	result := descriptor
	result.TypedInputs = slices.Clone(descriptor.TypedInputs)
	if descriptor.TypedScope != nil {
		typedScope := *descriptor.TypedScope
		result.TypedScope = &typedScope
	}
	return result
}

func cloneSparsePartitions(partitions []ExtractionPartition) []ExtractionPartition {
	result := slices.Clone(partitions)
	for index := range result {
		if result[index].Member != nil {
			member := *result[index].Member
			result[index].Member = &member
		}
		if result[index].TypedInput != nil {
			input := *result[index].TypedInput
			result[index].TypedInput = &input
		}
		if result[index].TypedScope != nil {
			typedScope := *result[index].TypedScope
			result[index].TypedScope = &typedScope
		}
	}
	return result
}
