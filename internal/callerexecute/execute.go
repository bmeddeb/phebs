package callerexecute

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/gitobj"
)

var ErrImmutableLeafInput = errors.New("caller leaf immutable input mismatch")

type BlobReader func(context.Context, string, string, int64) ([]byte, error)

type ExecuteRequest struct {
	RepositoryDir         string
	Plan                  extract.CandidateCallerPlan
	Pair                  ExpectedPair
	Protocol              string
	ResolverCatalogDigest string
	Resolver              *gocaller.DirectResolver
	ReadBlob              BlobReader
	Stage                 *callerleaf.Stage
}

// ExecutePair streams one exact domain/leaf into its caller-owned stage. Its
// only source-read capability is an OID supplied by the selected leaf replay.
func ExecutePair(ctx context.Context, request ExecuteRequest) error {
	if request.RepositoryDir == "" || request.Plan == nil || request.Stage == nil ||
		request.Pair.Adapter.Protocol != request.Protocol ||
		request.Pair.Adapter.Domain == "" || request.ResolverCatalogDigest == "" {
		return errors.New("caller pair execution request is incomplete")
	}
	if request.ReadBlob == nil {
		request.ReadBlob = gitobj.ReadBlob
	}
	if request.Resolver == nil {
		return errors.New("caller pair execution has no compiled resolver")
	}
	if (request.Pair.Identity.LeafAdapterVersion == callerleaf.LeafAdapterV2 ||
		request.Pair.Identity.LeafAdapterVersion == callerleaf.LeafAdapterV3) &&
		request.Resolver.DescriptorCount() == 0 {
		return executeNoResolverCoverage(ctx, request)
	}
	seenCandidates := 0
	err := request.Plan.ForEachCallerLeafFile(
		ctx, request.Pair.Adapter.Domain, request.Pair.Adapter.Version,
		request.Pair.Candidate,
		func(file extract.CandidateManifestFile) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			seenCandidates++
			switch file.SourceLane {
			case candidate.SourceLaneGoTest:
				request.Stage.ObserveExcludedGoTest()
				return nil
			case candidate.SourceLaneBase:
			default:
				return fmt.Errorf("%w: unsupported source lane", ErrImmutableLeafInput)
			}
			generated, generatedErr := request.Resolver.GeneratedSource(
				file.Path, file.ObjectID,
			)
			if generatedErr != nil {
				return fmt.Errorf("%w: %v", ErrImmutableLeafInput, generatedErr)
			}
			if generated {
				return addInputAbstention(request.Stage, file, "resolver_generated_input")
			}
			if !strings.HasSuffix(file.Path, ".go") {
				return addInputAbstention(request.Stage, file, "catalog_owned_input")
			}
			if file.DeclaredBytes > callerleaf.MaxDirectSourceBytes {
				return addInputAbstention(request.Stage, file, "source_too_large")
			}
			if err := request.Stage.ObserveSourceBlob(file.DeclaredBytes); err != nil {
				return err
			}
			content, err := request.ReadBlob(
				ctx, request.RepositoryDir, file.ObjectID,
				file.DeclaredBytes,
			)
			if err != nil {
				return fmt.Errorf("read selected caller blob %q: %w", file.Path, err)
			}
			if int64(len(content)) != file.DeclaredBytes {
				return fmt.Errorf(
					"%w: %q declared %d bytes, read %d",
					ErrImmutableLeafInput, file.Path, file.DeclaredBytes, len(content),
				)
			}
			if !utf8.Valid(content) {
				return addInputAbstention(request.Stage, file, "invalid_utf8")
			}
			sum := sha256.Sum256(content)
			blob := sdk.Blob{
				Content: string(content),
				Digest:  "sha256:" + hex.EncodeToString(sum[:]),
			}
			emitted := 0
			_, err = gocaller.ScanDirectSyntaxFile(
				ctx,
				gocaller.DirectScanRequest{
					Protocol: request.Protocol, Path: file.Path, Blob: blob,
					Resolver:              request.Resolver,
					ResolverCatalogDigest: request.ResolverCatalogDigest,
				},
				func(fact sdk.Fact) error {
					if fact.Path != file.Path || fact.Atom.BlobDigest != blob.Digest {
						return fmt.Errorf("%w: direct scanner escaped selected blob", ErrImmutableLeafInput)
					}
					kind, reason := callerleaf.RecordResult, ""
					if fact.Assertion.Predicate == "UNRESOLVED_CALLER" {
						kind, reason = callerleaf.RecordAbstention, "unresolved_caller"
					}
					emitted++
					return request.Stage.Add(callerleaf.Record{
						Schema: callerleaf.RecordSchema, Kind: kind,
						Path: file.Path, ObjectID: file.ObjectID,
						SourceLane: candidate.SourceLaneBase,
						Fact:       &fact, Reason: reason,
					})
				},
			)
			if err != nil {
				return err
			}
			if emitted == 0 {
				return addInputAbstention(request.Stage, file, "no_direct_caller")
			}
			return nil
		},
	)
	if err != nil {
		return err
	}
	if request.Pair.Identity.LeafAdapterVersion == callerleaf.LeafAdapterV3 {
		_, err = request.Stage.CompactZeroFactCoverage(seenCandidates)
	}
	return err
}

func executeNoResolverCoverage(ctx context.Context, request ExecuteRequest) error {
	seen := 0
	noDirect := 0
	gapCounts := map[string]int{}
	err := request.Plan.ForEachCallerLeafFile(
		ctx, request.Pair.Adapter.Domain, request.Pair.Adapter.Version,
		request.Pair.Candidate,
		func(file extract.CandidateManifestFile) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			seen++
			switch file.SourceLane {
			case candidate.SourceLaneGoTest:
				gapCounts[callerleaf.CoverageGapExcludedGoTest]++
				return nil
			case candidate.SourceLaneBase:
			default:
				return fmt.Errorf("%w: unsupported source lane", ErrImmutableLeafInput)
			}
			generated, generatedErr := request.Resolver.GeneratedSource(
				file.Path, file.ObjectID,
			)
			if generatedErr != nil {
				return fmt.Errorf("%w: %v", ErrImmutableLeafInput, generatedErr)
			}
			if generated {
				gapCounts[callerleaf.CoverageGapResolverGeneratedInput]++
				return nil
			}
			if !strings.HasSuffix(file.Path, ".go") {
				gapCounts[callerleaf.CoverageGapCatalogOwnedInput]++
				return nil
			}
			noDirect++
			return nil
		},
	)
	if err != nil {
		return err
	}
	if seen > request.Pair.Identity.Leaf.RecordCount {
		return fmt.Errorf("%w: compact coverage exceeded the pair member", ErrImmutableLeafInput)
	}
	if unselected := request.Pair.Identity.Leaf.RecordCount - seen; unselected > 0 {
		gapCounts[callerleaf.CoverageGapDomainUnselected] = unselected
	}
	gaps := make([]callerleaf.CoverageGap, 0, len(gapCounts))
	for _, reason := range []string{
		callerleaf.CoverageGapCatalogOwnedInput,
		callerleaf.CoverageGapDomainUnselected,
		callerleaf.CoverageGapExcludedGoTest,
		callerleaf.CoverageGapResolverGeneratedInput,
	} {
		if count := gapCounts[reason]; count > 0 {
			gaps = append(gaps, callerleaf.CoverageGap{Reason: reason, Count: count})
		}
	}
	return request.Stage.AddNoResolverCoverage(noDirect, gaps)
}

func addInputAbstention(
	stage *callerleaf.Stage,
	file extract.CandidateManifestFile,
	reason string,
) error {
	return stage.Add(callerleaf.Record{
		Schema: callerleaf.RecordSchema, Kind: callerleaf.RecordAbstention,
		Path: file.Path, ObjectID: file.ObjectID,
		SourceLane: candidate.SourceLaneBase, Reason: reason,
	})
}
