package relationshippublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const CitationContentLimit = sourceobservation.MaxSourceBytes

// Evidence is the common exact source record behind one relationship
// projection. Protocol-specific classifications remain explicit; no RPC
// declaration or Kafka broker/runtime identity is inferred by this adapter.
type Evidence struct {
	Kind                  string                 `json:"kind"`
	Plane                 string                 `json:"plane"`
	Class                 string                 `json:"class"`
	Reason                string                 `json:"reason,omitempty"`
	Path                  string                 `json:"path"`
	ObjectID              string                 `json:"object_id"`
	ContentDigest         string                 `json:"content_digest"`
	Span                  sourceobservation.Span `json:"span"`
	SourceRole            string                 `json:"source_role"`
	Operation             string                 `json:"operation,omitempty"`
	CandidateOperations   []string               `json:"candidate_operations,omitempty"`
	DeclarationPath       string                 `json:"declaration_path,omitempty"`
	DeclarationLineage    string                 `json:"declaration_lineage,omitempty"`
	ResolverRecordDigests []string               `json:"resolver_record_digests,omitempty"`
	TopicSpelling         string                 `json:"topic_spelling,omitempty"`
	GroupIDSpelling       string                 `json:"group_id_spelling,omitempty"`
	Library               string                 `json:"library,omitempty"`
	Shape                 string                 `json:"shape,omitempty"`
	Binding               string                 `json:"binding,omitempty"`
	PostingDigest         string                 `json:"posting_digest"`
}

type EvidenceReader struct {
	rpc   *rpccallerposting.Publication
	kafka *kafkatopicposting.Publication
}

// OpenEvidenceReader validates each exact component root once for a bounded
// response binding. Page reads still validate selected members.
func (publication *Publication) OpenEvidenceReader(
	ctx context.Context,
	dataDir string,
) (*EvidenceReader, error) {
	if publication == nil || !filepath.IsAbs(dataDir) {
		return nil, fmt.Errorf("%w: evidence reader", ErrInvalid)
	}
	authority := publication.rootValue.Authority
	rpc, err := rpccallerposting.OpenGeneration(
		ctx, filepath.Join(dataDir, "relationship-rpc-postings"),
		authority.Repository, authority.RPCGenerationDigest, authority.RPCRootDigest,
	)
	if err != nil {
		return nil, fmt.Errorf("open relationship RPC evidence: %w", err)
	}
	kafka, err := kafkatopicposting.OpenGeneration(
		ctx, filepath.Join(dataDir, "relationship-kafka-postings"),
		authority.Repository, authority.KafkaGenerationDigest, authority.KafkaRootDigest,
	)
	if err != nil {
		return nil, fmt.Errorf("open relationship Kafka evidence: %w", err)
	}
	return &EvidenceReader{rpc: rpc, kafka: kafka}, nil
}

// ReadEvidence resolves a projection through the exact component generation
// bound into its relationship root. The lookup opens one component root and
// one keyed member; it never walks a repository corpus or sibling member.
func (publication *Publication) ReadEvidence(
	ctx context.Context,
	dataDir string,
	projection Projection,
) (Evidence, error) {
	if publication == nil || validateProjection(projection) != nil ||
		projection.Kind == "" || !filepath.IsAbs(dataDir) {
		return Evidence{}, fmt.Errorf("%w: evidence lookup", ErrInvalid)
	}
	authority := publication.rootValue.Authority
	switch projection.Kind {
	case "rpc":
		component, err := rpccallerposting.OpenGeneration(
			ctx, filepath.Join(dataDir, "relationship-rpc-postings"),
			authority.Repository, authority.RPCGenerationDigest, authority.RPCRootDigest,
		)
		if err != nil {
			return Evidence{}, fmt.Errorf("open relationship RPC evidence: %w", err)
		}
		posting, err := component.ReadDigest(
			ctx, projection.Plane, projection.LookupKey, projection.PostingDigest,
		)
		if err != nil {
			return Evidence{}, fmt.Errorf("read relationship RPC evidence: %w", err)
		}
		return Evidence{
			Kind: "rpc", Plane: posting.Protocol, Class: posting.Class, Reason: posting.Reason,
			Path: posting.Path, ObjectID: posting.ObjectID, ContentDigest: posting.ContentDigest,
			Span: posting.Span, SourceRole: posting.SourceRole, Operation: posting.Operation,
			CandidateOperations: slices.Clone(posting.CandidateOperations),
			DeclarationPath:     posting.DeclarationPath, DeclarationLineage: posting.DeclarationLineage,
			ResolverRecordDigests: slices.Clone(posting.ResolverRecordDigests),
			PostingDigest:         posting.Digest,
		}, nil
	case "kafka":
		component, err := kafkatopicposting.OpenGeneration(
			ctx, filepath.Join(dataDir, "relationship-kafka-postings"),
			authority.Repository, authority.KafkaGenerationDigest, authority.KafkaRootDigest,
		)
		if err != nil {
			return Evidence{}, fmt.Errorf("open relationship Kafka evidence: %w", err)
		}
		posting, err := component.ReadDigest(
			ctx, projection.Plane, projection.LookupKey, projection.PostingDigest,
		)
		if err != nil {
			return Evidence{}, fmt.Errorf("read relationship Kafka evidence: %w", err)
		}
		return Evidence{
			Kind: "kafka", Plane: posting.Plane, Class: posting.Class, Reason: posting.Reason,
			Path: posting.Path, ObjectID: posting.ObjectID, ContentDigest: posting.ContentDigest,
			Span: posting.Span, SourceRole: posting.SourceRole, TopicSpelling: posting.TopicSpelling,
			GroupIDSpelling: posting.GroupIDSpelling, Library: posting.Library,
			Shape: posting.Shape, Binding: posting.Binding, PostingDigest: posting.Digest,
		}, nil
	default:
		return Evidence{}, fmt.Errorf("%w: evidence kind", ErrInvalid)
	}
}

// ReadPage groups projection evidence by protocol/plane and lookup key. Each
// selected component member is read once even when many page rows share it.
func (reader *EvidenceReader) ReadPage(
	ctx context.Context,
	projections []Projection,
) (map[string]Evidence, error) {
	if reader == nil || reader.rpc == nil || reader.kafka == nil ||
		len(projections) == 0 || len(projections) > 100 {
		return nil, fmt.Errorf("%w: evidence page", ErrInvalid)
	}
	type group struct {
		kind, plane, key string
	}
	wanted := make(map[group]map[string]Projection)
	for _, projection := range projections {
		if validateProjection(projection) != nil {
			return nil, fmt.Errorf("%w: evidence page projection", ErrInvalid)
		}
		groupKey := group{projection.Kind, projection.Plane, projection.LookupKey}
		if wanted[groupKey] == nil {
			wanted[groupKey] = make(map[string]Projection)
		}
		wanted[groupKey][projection.PostingDigest] = projection
	}
	result := make(map[string]Evidence, len(projections))
	for groupKey, groupWanted := range wanted {
		switch groupKey.kind {
		case "rpc":
			var values []rpccallerposting.Posting
			var err error
			if groupKey.key == "" {
				values, err = reader.rpc.ReadUnresolved(ctx, groupKey.plane)
			} else {
				values, err = reader.rpc.ReadOperation(ctx, groupKey.plane, groupKey.key)
			}
			if err != nil {
				return nil, err
			}
			for _, posting := range values {
				projection, present := groupWanted[posting.Digest]
				if !present {
					continue
				}
				result[projection.Digest] = rpcEvidence(posting)
			}
		case "kafka":
			var values []kafkatopicposting.Posting
			var err error
			if groupKey.key == "" {
				values, err = reader.kafka.ReadUnresolved(ctx, groupKey.plane)
			} else {
				values, err = reader.kafka.ReadTopic(ctx, groupKey.plane, groupKey.key)
			}
			if err != nil {
				return nil, err
			}
			for _, posting := range values {
				projection, present := groupWanted[posting.Digest]
				if !present {
					continue
				}
				result[projection.Digest] = kafkaEvidence(posting)
			}
		default:
			return nil, fmt.Errorf("%w: evidence page kind", ErrInvalid)
		}
	}
	if len(result) != len(projections) {
		return nil, ErrNotFound
	}
	return result, nil
}

func rpcEvidence(posting rpccallerposting.Posting) Evidence {
	return Evidence{
		Kind: "rpc", Plane: posting.Protocol, Class: posting.Class, Reason: posting.Reason,
		Path: posting.Path, ObjectID: posting.ObjectID, ContentDigest: posting.ContentDigest,
		Span: posting.Span, SourceRole: posting.SourceRole, Operation: posting.Operation,
		CandidateOperations: slices.Clone(posting.CandidateOperations),
		DeclarationPath:     posting.DeclarationPath, DeclarationLineage: posting.DeclarationLineage,
		ResolverRecordDigests: slices.Clone(posting.ResolverRecordDigests),
		PostingDigest:         posting.Digest,
	}
}

func kafkaEvidence(posting kafkatopicposting.Posting) Evidence {
	return Evidence{
		Kind: "kafka", Plane: posting.Plane, Class: posting.Class, Reason: posting.Reason,
		Path: posting.Path, ObjectID: posting.ObjectID, ContentDigest: posting.ContentDigest,
		Span: posting.Span, SourceRole: posting.SourceRole, TopicSpelling: posting.TopicSpelling,
		GroupIDSpelling: posting.GroupIDSpelling, Library: posting.Library,
		Shape: posting.Shape, Binding: posting.Binding, PostingDigest: posting.Digest,
	}
}

// ReadEvidenceContent returns only the exact cited byte span from the
// immutable blob object. It refuses external alternates and verifies the
// complete source digest before slicing, so a path or mutable branch is never
// trusted as citation authority.
func ReadEvidenceContent(
	ctx context.Context,
	dataDir, repository string,
	evidence Evidence,
) ([]byte, error) {
	if !filepath.IsAbs(dataDir) || evidence.Path == "" ||
		!gitobj.IsObjectID(evidence.ObjectID) || !validDigest(evidence.ContentDigest) ||
		evidence.Span.StartByte < 0 || evidence.Span.EndByte <= evidence.Span.StartByte ||
		evidence.Span.EndByte > CitationContentLimit {
		return nil, fmt.Errorf("%w: citation", ErrInvalid)
	}
	repositoryDirectory, err := phebssync.SafeRepoDir(dataDir, repository)
	if err != nil {
		return nil, err
	}
	if err := gitobj.RejectAlternates(repositoryDirectory); err != nil {
		return nil, err
	}
	content, err := gitobj.ReadBlob(
		ctx, repositoryDirectory, evidence.ObjectID, CitationContentLimit,
	)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(content)
	if "sha256:"+hex.EncodeToString(sum[:]) != evidence.ContentDigest ||
		evidence.Span.EndByte > len(content) {
		return nil, errors.New("relationship citation content differs from publication authority")
	}
	return slices.Clone(content[evidence.Span.StartByte:evidence.Span.EndByte]), nil
}
