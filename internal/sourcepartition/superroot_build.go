package sourcepartition

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
)

// BuildSuperRoot plans one v2 super-root stage from one identity-stable
// streamed repository-source pass. Members are canonically hashed before an
// exact validated prior member is hard-linked or new bytes are written; the
// final fence re-reads only the small root and segment controls. Until the
// caller installs the completed stage, everything lives under the caller-owned
// empty output directory, so no reader can observe a corrupt prefix.
func BuildSuperRoot(ctx context.Context, request BuildRequest) (SuperRoot, error) {
	if request.Metrics != nil {
		*request.Metrics = BuildMetrics{}
	}
	metrics := request.Metrics
	if metrics == nil {
		metrics = &BuildMetrics{}
	}
	if request.SuperRootMetrics != nil {
		*request.SuperRootMetrics = SuperRootBuildMetrics{}
	}
	superMetrics := request.SuperRootMetrics
	if superMetrics == nil {
		superMetrics = &SuperRootBuildMetrics{}
	}
	if !filepath.IsAbs(request.SourceDirectory) ||
		!filepath.IsAbs(request.OutputDirectory) {
		return SuperRoot{}, invalidf("build directories must be absolute")
	}
	if err := ValidatePolicy(request.Policy); err != nil {
		return SuperRoot{}, err
	}
	if err := repositoryindex.ValidateSourceManifest(request.Source); err != nil ||
		request.Source.Repository != request.Repository {
		return SuperRoot{}, invalidf("source generation identity is invalid")
	}
	opened, err := repositoryindex.ReadSourceManifest(
		request.SourceDirectory, request.Repository,
	)
	if err != nil || opened.Digest != request.Source.Digest {
		return SuperRoot{}, invalidf("source generation changed before planning")
	}
	if err := requireEmptyDirectory(request.OutputDirectory); err != nil {
		return SuperRoot{}, err
	}
	policyDigest, err := PolicyDigest(request.Policy)
	if err != nil {
		return SuperRoot{}, err
	}
	generationDigest, err := GenerationDigest(
		request.Repository, request.Source.Digest, policyDigest,
	)
	if err != nil {
		return SuperRoot{}, err
	}
	reusable, err := reusableSuperRootMembers(
		ctx, request.PriorSuperRootDirectory, request.Repository, policyDigest,
	)
	if err != nil {
		return SuperRoot{}, err
	}
	spoolDirectory, err := os.MkdirTemp(request.OutputDirectory, ".source-partition-spool-")
	if err != nil {
		return SuperRoot{}, err
	}
	defer func() { _ = os.RemoveAll(spoolDirectory) }()
	leaves, err := planLeaves(ctx, request, metrics, spoolDirectory)
	if err != nil {
		return SuperRoot{}, err
	}

	memberPool, err := os.MkdirTemp(request.OutputDirectory, ".source-partition-members-")
	if err != nil {
		return SuperRoot{}, err
	}
	defer func() { _ = os.RemoveAll(memberPool) }()
	summaries := make([]Member, 0, len(leaves))
	var aggregateDeclared, aggregateEncoded int64
	for index, leaf := range leaves {
		memberPath := filepath.Join(memberPool, fmt.Sprintf("member-%05d.jsonl", index))
		member, err := summarizeMember(ctx, leaf)
		if err != nil {
			return SuperRoot{}, err
		}
		if prior, ok := reusable[member.Prefix]; ok && reusableMemberEqual(member, prior.member) {
			if err := os.Link(prior.path, memberPath); err != nil {
				return SuperRoot{}, fmt.Errorf("reuse validated partition member: %w", err)
			}
			superMetrics.ReusedMembers++
			superMetrics.ReusedBytes += member.ContentBytes
		} else {
			member, err = writeMemberAt(ctx, memberPath, leaf)
			if err != nil {
				return SuperRoot{}, err
			}
			superMetrics.WrittenMembers++
			superMetrics.WrittenBytes += member.ContentBytes
		}
		if member.DeclaredBytes > MaxAggregateDeclaredBytes-aggregateDeclared {
			return SuperRoot{}, checkPlanLimit(
				pipelinerefusal.DimensionGenerationBlobBytes,
				aggregateDeclared+member.DeclaredBytes, MaxAggregateDeclaredBytes,
				"aggregate declared bytes exceed their bound",
			)
		}
		if member.ContentBytes > MaxAggregateEncodedBytes-aggregateEncoded {
			return SuperRoot{}, checkPlanLimit(
				pipelinerefusal.DimensionGenerationEncodedBytes,
				aggregateEncoded+member.ContentBytes, MaxAggregateEncodedBytes,
				"aggregate encoded bytes exceed their bound",
			)
		}
		aggregateDeclared += member.DeclaredBytes
		aggregateEncoded += member.ContentBytes
		metrics.UniqueBlobs += member.BlobCount
		metrics.UniqueBlobBytes += member.DeclaredBytes
		summaries = append(summaries, member)
		if err := removeSpool(leaf); err != nil {
			return SuperRoot{}, err
		}
	}

	segments, err := packSegments(summaries)
	if err != nil {
		return SuperRoot{}, err
	}
	root := SuperRoot{
		Schema: SuperRootSchema, Repository: request.Repository,
		SourceGenerationDigest: request.Source.Digest,
		RevisionCount:          len(request.Source.Revisions),
		Policy:                 request.Policy, PolicyDigest: policyDigest,
		PartitionPolicy: frozenPolicy(), AggregatePolicy: frozenAggregatePolicy(),
		GenerationDigest: generationDigest,
		Segments:         []SegmentEntry{},
	}
	memberIndex := 0
	for ordinal, count := range segments {
		segmentDirectory := filepath.Join(
			request.OutputDirectory, segmentDirectoryName(ordinal),
		)
		if err := os.Mkdir(segmentDirectory, 0o700); err != nil {
			return SuperRoot{}, err
		}
		manifest := Manifest{
			Schema: ManifestSchema, Repository: request.Repository,
			SourceGenerationDigest: request.Source.Digest,
			RevisionCount:          len(request.Source.Revisions),
			Policy:                 request.Policy, PolicyDigest: policyDigest,
			PartitionPolicy: frozenPolicy(), GenerationDigest: generationDigest,
			Members: make([]Member, 0, count),
		}
		for local := 0; local < count; local++ {
			if err := ctx.Err(); err != nil {
				return SuperRoot{}, err
			}
			member := summaries[memberIndex]
			member.Ordinal, member.Count = local, count
			member.Name = memberName(request.Repository, generationDigest, local)
			if err := os.Rename(
				filepath.Join(memberPool, fmt.Sprintf("member-%05d.jsonl", memberIndex)),
				filepath.Join(segmentDirectory, member.Name),
			); err != nil {
				return SuperRoot{}, err
			}
			manifest.Members = append(manifest.Members, member)
			manifest.BlobCount += member.BlobCount
			manifest.PlacementCount += member.PlacementCount
			manifest.DeclaredBytes += member.DeclaredBytes
			manifest.EncodedMemberBytes += member.ContentBytes
			memberIndex++
		}
		manifest.Digest, err = ManifestDigest(manifest)
		if err != nil {
			return SuperRoot{}, err
		}
		if err := writeCanonicalJSON(
			filepath.Join(segmentDirectory, ManifestName(request.Repository)),
			manifest, MaxManifestBytes,
		); err != nil {
			return SuperRoot{}, err
		}
		if err := syncDirectory(segmentDirectory); err != nil {
			return SuperRoot{}, err
		}
		root.Segments = append(root.Segments, SegmentEntry{
			Ordinal: ordinal, Count: len(segments),
			Directory:          segmentDirectoryName(ordinal),
			ManifestDigest:     manifest.Digest,
			MemberCount:        count,
			FirstPrefix:        manifest.Members[0].Prefix,
			LastPrefix:         manifest.Members[count-1].Prefix,
			BlobCount:          manifest.BlobCount,
			PlacementCount:     manifest.PlacementCount,
			DeclaredBytes:      manifest.DeclaredBytes,
			EncodedMemberBytes: manifest.EncodedMemberBytes,
		})
		root.MemberCount += count
		root.BlobCount += manifest.BlobCount
		root.PlacementCount += manifest.PlacementCount
		root.DeclaredBytes += manifest.DeclaredBytes
		root.EncodedMemberBytes += manifest.EncodedMemberBytes
	}
	root.Digest, err = SuperRootDigest(root)
	if err != nil {
		return SuperRoot{}, err
	}
	if err := writeCanonicalJSON(
		filepath.Join(request.OutputDirectory, SuperRootName(request.Repository)),
		root, MaxManifestBytes,
	); err != nil {
		return SuperRoot{}, err
	}
	if err := syncDirectory(request.OutputDirectory); err != nil {
		return SuperRoot{}, err
	}
	if err := os.RemoveAll(spoolDirectory); err != nil {
		return SuperRoot{}, err
	}
	if err := os.RemoveAll(memberPool); err != nil {
		return SuperRoot{}, err
	}
	if err := validateSuperRootControls(ctx, request.OutputDirectory, root); err != nil {
		return SuperRoot{}, err
	}
	return root, nil
}

type reusableSuperRootMember struct {
	member Member
	path   string
}

func reusableSuperRootMembers(
	ctx context.Context, directory, repository, policyDigest string,
) (map[string]reusableSuperRootMember, error) {
	result := make(map[string]reusableSuperRootMember)
	if directory == "" {
		return result, nil
	}
	if !filepath.IsAbs(directory) {
		return nil, invalidf("prior super-root directory must be absolute")
	}
	root, err := ReadSuperRoot(directory, repository)
	if err != nil {
		return nil, fmt.Errorf("read prior super-root: %w", err)
	}
	if root.PolicyDigest != policyDigest {
		return nil, invalidf("prior super-root policy differs")
	}
	// Reuse authority is earned by validating the complete prior stage, never
	// by trusting a digest-bearing control alone.
	if err := ValidateSuperRootStage(ctx, directory, root); err != nil {
		return nil, fmt.Errorf("validate prior super-root: %w", err)
	}
	for _, segment := range root.Segments {
		segmentDirectory := filepath.Join(directory, segment.Directory)
		manifest, err := ReadManifest(segmentDirectory, repository)
		if err != nil {
			return nil, err
		}
		for _, member := range manifest.Members {
			if _, exists := result[member.Prefix]; exists {
				return nil, invalidf("prior super-root repeats a member prefix")
			}
			result[member.Prefix] = reusableSuperRootMember{
				member: member, path: filepath.Join(segmentDirectory, member.Name),
			}
		}
	}
	return result, nil
}

func reusableMemberEqual(left, right Member) bool {
	return left.Prefix == right.Prefix && left.PrefixBits == right.PrefixBits &&
		left.BlobCount == right.BlobCount &&
		left.PlacementCount == right.PlacementCount &&
		left.DeclaredBytes == right.DeclaredBytes &&
		left.ContentBytes == right.ContentBytes &&
		left.FirstObjectID == right.FirstObjectID &&
		left.LastObjectID == right.LastObjectID && left.Digest == right.Digest
}

// packSegments assigns ordered member summaries to the fewest ordered
// segments whose totals stay within the retained v1 generation bounds. Every
// check happens before growth: a member that would overflow the open segment
// closes it, and a segment that would exceed the aggregate segment bound
// refuses before any assignment.
func packSegments(members []Member) ([]int, error) {
	if len(members) == 0 {
		return nil, nil
	}
	if len(members) > MaxAggregateMembers {
		return nil, checkPlanLimit(
			pipelinerefusal.DimensionPartitionCount,
			int64(len(members)), MaxAggregateMembers,
			"aggregate member count exceeds its bound",
		)
	}
	segments := []int{0}
	var segmentDeclared, segmentEncoded int64
	segmentMembers := 0
	for _, member := range members {
		fits := segmentMembers+1 <= MaxPartitions &&
			member.DeclaredBytes <= MaxGenerationBlobBytes-segmentDeclared &&
			member.ContentBytes <= MaxGenerationContentBytes-segmentEncoded
		if !fits {
			if len(segments) >= MaxSegments {
				return nil, checkPlanLimit(
					pipelinerefusal.DimensionAggregateSegments,
					int64(len(segments)+1), MaxSegments,
					"aggregate segment count exceeds its bound",
				)
			}
			segments = append(segments, 0)
			segmentDeclared, segmentEncoded, segmentMembers = 0, 0, 0
		}
		segments[len(segments)-1]++
		segmentMembers++
		segmentDeclared += member.DeclaredBytes
		segmentEncoded += member.ContentBytes
	}
	return segments, nil
}

// validateSuperRootControls is the final re-fence of a fresh build: it
// re-reads only the bounded controls (root plus one manifest per segment) and
// stats member files without reopening their content. Byte integrity is
// already proven by hash-on-write; corpus-sized members are read exactly once
// per build.
func validateSuperRootControls(ctx context.Context, directory string, expected SuperRoot) error {
	root, err := ReadSuperRoot(directory, expected.Repository)
	if err != nil {
		return err
	}
	if root.Digest != expected.Digest {
		return invalidf("super-root changed before its final fence")
	}
	wantNames := map[string]bool{SuperRootName(expected.Repository): true}
	for _, entry := range root.Segments {
		if err := ctx.Err(); err != nil {
			return err
		}
		segmentDirectory := filepath.Join(directory, entry.Directory)
		manifest, err := ReadManifest(segmentDirectory, root.Repository)
		if err != nil {
			return err
		}
		if err := checkSegmentBinding(root, entry, manifest); err != nil {
			return err
		}
		for _, member := range manifest.Members {
			info, statErr := os.Lstat(filepath.Join(segmentDirectory, member.Name))
			if statErr != nil || !info.Mode().IsRegular() ||
				info.Size() != member.ContentBytes {
				return invalidf("segment member is missing, special, or changed")
			}
		}
		wantNames[entry.Directory] = true
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) != len(wantNames) {
		return invalidf("super-root stage contains an incomplete or extra artifact")
	}
	for _, entry := range entries {
		if !wantNames[entry.Name()] || entry.Type()&os.ModeSymlink != 0 {
			return invalidf("super-root stage contains an unknown or special artifact")
		}
	}
	return nil
}
