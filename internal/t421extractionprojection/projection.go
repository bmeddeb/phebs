// Package t421extractionprojection derives the source-free T42.1 extraction
// projection from exact current production controls.
package t421extractionprojection

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/t421sourceprojection"
)

var ErrInvalid = errors.New("invalid T42.1 extraction projection")

// SetIdentity is the scalar identity of one domain-framed set.
type SetIdentity struct {
	Records     uint64 `json:"records"`
	FramedBytes uint64 `json:"framed_bytes"`
	SHA256      string `json:"sha256"`
}

type ResultTotals = candidate.DomainResultTotals

// PartitionProfile is the exact phase-state partition shape.
type PartitionProfile struct {
	Ordinal           uint64       `json:"ordinal"`
	Kind              string       `json:"kind"`
	MemberOrdinal     int64        `json:"member_ordinal"`
	CallerPrefix      string       `json:"caller_prefix,omitempty"`
	SourceStart       uint64       `json:"source_start"`
	SourceEnd         uint64       `json:"source_end"`
	MemberRecordStart uint64       `json:"member_record_start"`
	MemberRecordEnd   uint64       `json:"member_record_end"`
	AdmittedRecords   uint64       `json:"admitted_records"`
	Reservation       ResultTotals `json:"reservation"`
	Expected          ResultTotals `json:"expected"`
}

// PhaseProjection is the exact extraction slice embedded in a T42.1 phase
// projection.
type PhaseProjection struct {
	Domain                  string       `json:"domain"`
	Availability            string       `json:"availability"`
	ApplicablePartitions    uint64       `json:"applicable_partitions"`
	MemberPartitions        uint64       `json:"member_partitions"`
	TypedPartitions         uint64       `json:"typed_partitions"`
	TypedScopeRecords       uint64       `json:"typed_scope_records"`
	TypedScopePathBytes     uint64       `json:"typed_scope_path_bytes"`
	TypedScopeEncodedBytes  uint64       `json:"typed_scope_encoded_bytes"`
	TypedScopeSHA256        string       `json:"typed_scope_sha256,omitempty"`
	TypedScopeContentSHA256 string       `json:"typed_scope_descriptor_content_sha256,omitempty"`
	Candidates              SetIdentity  `json:"candidates"`
	Reserved                ResultTotals `json:"reserved"`
	Expected                ResultTotals `json:"expected"`
	PartitionShape          SetIdentity  `json:"partition_shape"`
}

// PartitionResult is the exact source-free result row used by the receipt.
type PartitionResult struct {
	Ordinal              uint64       `json:"ordinal"`
	Kind                 string       `json:"kind"`
	MemberOrdinal        int64        `json:"member_ordinal"`
	CallerPrefix         string       `json:"caller_prefix,omitempty"`
	SourceStart          uint64       `json:"source_start"`
	SourceEnd            uint64       `json:"source_end"`
	MemberRecordStart    uint64       `json:"member_record_start"`
	MemberRecordEnd      uint64       `json:"member_record_end"`
	AdmittedRecords      uint64       `json:"admitted_records"`
	Reservation          ResultTotals `json:"reservation"`
	Disposition          string       `json:"disposition"`
	Totals               ResultTotals `json:"totals"`
	PartitionSHA256      string       `json:"partition_sha256"`
	ExpectationSHA256    string       `json:"expectation_sha256"`
	ResultDigestSHA256   string       `json:"result_digest_sha256"`
	ResultIdentitySHA256 string       `json:"result_identity_sha256"`
}

// RootResult is the detailed current-domain evidence. The caller adds the
// aggregate generation digest; V2 deliberately leaves operational schedule
// identity to transition evidence.
type RootResult struct {
	Domain                      string            `json:"domain"`
	Current                     bool              `json:"current"`
	GenerationSHA256            string            `json:"generation_sha256"`
	RootSHA256                  string            `json:"root_sha256"`
	CandidateGenerationSHA256   string            `json:"candidate_generation_sha256"`
	SourceGenerationSHA256      string            `json:"source_generation_sha256"`
	ObservationGenerationSHA256 string            `json:"observation_generation_sha256"`
	PlanSHA256                  string            `json:"plan_sha256"`
	ScheduleSHA256              string            `json:"schedule_sha256"`
	ApplicablePartitions        uint64            `json:"applicable_partitions"`
	MemberPartitions            uint64            `json:"member_partitions"`
	TypedPartitions             uint64            `json:"typed_partitions"`
	TypedScopeRecords           uint64            `json:"typed_scope_records"`
	TypedScopePathBytes         uint64            `json:"typed_scope_path_bytes"`
	TypedScopeEncodedBytes      uint64            `json:"typed_scope_encoded_bytes"`
	TypedScopeSHA256            string            `json:"typed_scope_sha256,omitempty"`
	TypedScopeContentSHA256     string            `json:"typed_scope_descriptor_content_sha256,omitempty"`
	Candidates                  SetIdentity       `json:"candidates"`
	Members                     SetIdentity       `json:"members"`
	Reserved                    ResultTotals      `json:"reserved"`
	Totals                      ResultTotals      `json:"totals"`
	PartitionResultsSHA256      string            `json:"partition_results_sha256"`
	PartitionResults            []PartitionResult `json:"partition_results"`
}

// Result contains both projections derived by one exact domain read.
type Result struct {
	Projection PhaseProjection `json:"projection"`
	Root       RootResult      `json:"root"`
}

// Derive closes the stored plan/root against the exact sparse domain and
// derives only source-free scalar and digest evidence.
func Derive(
	ctx context.Context,
	snapshot extractionpublication.DomainSnapshot,
	domain *candidate.SparseDomain,
	candidates SetIdentity,
	candidateProof SetIdentity,
) (Result, error) {
	if ctx == nil || domain == nil {
		return Result{}, invalid("input")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := candidate.ValidateDomainResultPlan(snapshot.Plan, domain); err != nil {
		return Result{}, errors.Join(ErrInvalid, err)
	}
	if err := candidate.ValidateDomainResultRoot(snapshot.Root, snapshot.Plan); err != nil {
		return Result{}, errors.Join(ErrInvalid, err)
	}
	authority, err := candidate.NewDownstreamDomainAuthority(snapshot.Plan, snapshot.Root, snapshot.Authority.RunID)
	if err != nil || authority != snapshot.Authority {
		return Result{}, invalid("authority")
	}
	if !validSetIdentity(candidates) || !validSetIdentity(candidateProof) {
		return Result{}, invalid("candidate identity")
	}

	partitions := domain.Partitions()
	if len(partitions) != len(snapshot.Plan.Expected) || len(partitions) != len(snapshot.Root.Results) {
		return Result{}, invalid("partition inventory")
	}
	profiles := make([]PartitionProfile, len(partitions))
	results := make([]PartitionResult, len(partitions))
	shape := newIdentityBuilder("t421-extraction-partition-shape-v1/" + snapshot.Plan.Domain)
	members := newIdentityBuilder("t421-extraction-result-members-v1")
	actualCandidateProof := t421sourceprojection.NewCandidateProof(snapshot.Plan.Domain)
	var memberPartitions, typedPartitions, admitted uint64
	typedOrdinal := -1
	var typedDescriptor *candidate.SparseTypedScopeDescriptor
	for index, partition := range partitions {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if partition.Ordinal != index || partition.SourceStart < 0 || partition.SourceEnd < partition.SourceStart ||
			partition.MemberRecordStart < 0 || partition.MemberRecordEnd < partition.MemberRecordStart ||
			partition.AdmittedRecords < 0 {
			return Result{}, invalid("partition scalar")
		}
		memberOrdinal := int64(-1)
		switch partition.Kind {
		case candidate.PartitionKindCandidateMember:
			if partition.Member == nil || partition.Member.Ordinal < 0 || partition.TypedScope != nil {
				return Result{}, invalid("candidate-member partition")
			}
			memberOrdinal = int64(partition.Member.Ordinal)
			memberPartitions++
			if uint64(partition.AdmittedRecords) > ^uint64(0)-admitted {
				return Result{}, invalid("candidate record overflow")
			}
			admitted += uint64(partition.AdmittedRecords)
			if err := domain.ReadPartition(ctx, index, func(record candidate.Record) error {
				return actualCandidateProof.Add(
					record.Path, record.OID, record.DeclaredBytes, record.Required,
				)
			}); err != nil {
				return Result{}, err
			}
		case candidate.PartitionKindTypedInput:
			if partition.Member != nil || partition.TypedScope == nil {
				return Result{}, invalid("typed partition")
			}
			typedPartitions++
			if typedOrdinal < 0 {
				typedOrdinal = index
				descriptor := *partition.TypedScope
				typedDescriptor = &descriptor
			} else if *partition.TypedScope != *typedDescriptor {
				return Result{}, invalid("typed scope mismatch")
			}
		default:
			return Result{}, invalid("partition kind")
		}

		expectation := snapshot.Plan.Expected[index]
		settled := snapshot.Root.Results[index]
		profile := PartitionProfile{
			Ordinal: uint64(index), Kind: partition.Kind, MemberOrdinal: memberOrdinal,
			CallerPrefix: partition.CallerPrefix, SourceStart: uint64(partition.SourceStart),
			SourceEnd: uint64(partition.SourceEnd), MemberRecordStart: uint64(partition.MemberRecordStart),
			MemberRecordEnd: uint64(partition.MemberRecordEnd), AdmittedRecords: uint64(partition.AdmittedRecords),
			Reservation: expectation.Reservation, Expected: settled.Totals,
		}
		if err := shape.add(profile); err != nil {
			return Result{}, err
		}
		if err := members.add(settled.Identity); err != nil {
			return Result{}, err
		}
		profiles[index] = profile
		results[index] = PartitionResult{
			Ordinal: profile.Ordinal, Kind: profile.Kind, MemberOrdinal: profile.MemberOrdinal,
			CallerPrefix: profile.CallerPrefix, SourceStart: profile.SourceStart, SourceEnd: profile.SourceEnd,
			MemberRecordStart: profile.MemberRecordStart, MemberRecordEnd: profile.MemberRecordEnd,
			AdmittedRecords: profile.AdmittedRecords, Reservation: profile.Reservation,
			Disposition: settled.Disposition, Totals: settled.Totals,
			PartitionSHA256: partition.Digest, ExpectationSHA256: expectation.Digest,
			ResultDigestSHA256: settled.Digest, ResultIdentitySHA256: settled.Identity,
		}
	}
	if candidates.Records != admitted {
		return Result{}, invalid("candidate cardinality")
	}
	actualProof, err := actualCandidateProof.Finish()
	if err != nil || SetIdentity(actualProof) != candidateProof {
		return Result{}, errors.Join(err, invalid("candidate content"))
	}

	projection := PhaseProjection{
		Domain: snapshot.Plan.Domain, Availability: snapshot.Plan.Availability,
		ApplicablePartitions: uint64(len(partitions)), MemberPartitions: memberPartitions,
		TypedPartitions: typedPartitions, Candidates: candidates,
		Reserved: snapshot.Plan.Reserved, Expected: snapshot.Root.Totals,
		PartitionShape: shape.finish(),
	}
	if typedOrdinal >= 0 {
		scope, err := domain.TypedSourceScope(ctx, typedOrdinal)
		if err != nil {
			return Result{}, err
		}
		summary, err := scope.Summary(ctx)
		if err != nil || typedDescriptor == nil || summary.Records != typedDescriptor.Records ||
			summary.EncodedBytes != typedDescriptor.ContentBytes ||
			summary.ContentDigest != typedDescriptor.ContentDigest || summary.PathBytes < 0 {
			return Result{}, invalid("typed scope summary")
		}
		projection.TypedScopeRecords = uint64(summary.Records)
		projection.TypedScopePathBytes = uint64(summary.PathBytes)
		projection.TypedScopeEncodedBytes = uint64(summary.EncodedBytes)
		projection.TypedScopeSHA256 = summary.ContentDigest
		projection.TypedScopeContentSHA256 = summary.ContentDigest
	}

	partitionResultsSHA256, err := receiptSHA256(results)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Projection: projection,
		Root: RootResult{
			Domain: projection.Domain, Current: true, RootSHA256: snapshot.Root.Digest,
			CandidateGenerationSHA256:   snapshot.Plan.CandidateGenerationDigest,
			SourceGenerationSHA256:      snapshot.Plan.SourceGenerationDigest,
			ObservationGenerationSHA256: snapshot.Plan.ObservationGenerationDigest,
			PlanSHA256:                  snapshot.Plan.Digest,
			ApplicablePartitions:        projection.ApplicablePartitions,
			MemberPartitions:            memberPartitions, TypedPartitions: typedPartitions,
			TypedScopeRecords: projection.TypedScopeRecords, TypedScopePathBytes: projection.TypedScopePathBytes,
			TypedScopeEncodedBytes:  projection.TypedScopeEncodedBytes,
			TypedScopeSHA256:        projection.TypedScopeSHA256,
			TypedScopeContentSHA256: projection.TypedScopeContentSHA256,
			Candidates:              candidates, Members: members.finish(), Reserved: snapshot.Plan.Reserved,
			Totals: snapshot.Root.Totals, PartitionResultsSHA256: partitionResultsSHA256,
			PartitionResults: results,
		},
	}, nil
}

type identityBuilder struct {
	hash    hash.Hash
	bytes   uint64
	records uint64
}

func newIdentityBuilder(domain string) *identityBuilder {
	builder := &identityBuilder{hash: sha256.New()}
	builder.writeFrame([]byte(domain))
	return builder
}

func (builder *identityBuilder) add(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	builder.writeFrame(raw)
	builder.records++
	return nil
}

func (builder *identityBuilder) writeFrame(raw []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(raw)))
	_, _ = builder.hash.Write(length[:])
	_, _ = builder.hash.Write(raw)
	builder.bytes += uint64(len(length) + len(raw))
}

func (builder *identityBuilder) finish() SetIdentity {
	return SetIdentity{
		Records: builder.records, FramedBytes: builder.bytes,
		SHA256: "sha256:" + hex.EncodeToString(builder.hash.Sum(nil)),
	}
}

func validSetIdentity(value SetIdentity) bool {
	return value.FramedBytes > 0 && validDigest(value.SHA256)
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	decoded, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && hex.EncodeToString(decoded) == value[len("sha256:"):]
}

func receiptSHA256(value any) (string, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func invalid(name string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, name)
}
