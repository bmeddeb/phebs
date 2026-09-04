package observationpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

// CurrentInventoryDownstreamAuthorityV2 reads one pointer-sized publication
// reference, the selected at-most-1-MiB inventory root, and the pointer again.
// It performs no segment/member/source/observation scan.
func CurrentInventoryDownstreamAuthorityV2(
	ctx context.Context, root, repository string,
) (DownstreamAuthority, error) {
	if err := ctx.Err(); err != nil {
		return DownstreamAuthority{}, err
	}
	selected, err := ReadInventoryPublicationRootV2Context(ctx, root, repository)
	if err != nil {
		return DownstreamAuthority{}, err
	}
	directory := inventoryGenerationDirectoryV2(root, repository, selected.Current.GenerationDigest)
	inventory, err := ReadInventoryRootV2Context(
		ctx,
		filepath.Join(directory, InventoryPublicationInventoryNameV2), repository,
	)
	if err != nil || inventory.GenerationDigest != selected.Current.GenerationDigest ||
		inventory.Digest != selected.Current.InventoryDigest ||
		inventory.SourcePartitionRootDigest != selected.Current.SourceRootDigest {
		return DownstreamAuthority{}, errors.Join(err, invalid("inventory v2 downstream authority"))
	}
	confirmed, err := ReadInventoryPublicationRootV2Context(ctx, root, repository)
	if err != nil || confirmed.Current != selected.Current {
		return DownstreamAuthority{}, errors.Join(err, invalid("inventory v2 pointer changed during downstream read"))
	}
	publication := &InventoryPublicationV2{
		directory: filepath.Join(directory, InventoryPublicationInventoryNameV2), root: inventory,
	}
	return publication.DownstreamAuthority(), nil
}

// AcquireCurrent pins and fully validates the exact V2 inventory selected by
// a shallow planning authority. A pointer reread prevents a worker from
// accepting a valid historical generation after current authority moves.
func (cache *InventoryCacheV2) AcquireCurrent(
	ctx context.Context, root, repository string, expected DownstreamAuthority,
) (*InventoryLeaseV2, error) {
	if expected.Version != DownstreamAuthorityV2 || expected.Repository != repository {
		return nil, invalid("inventory v2 current lease authority")
	}
	selected, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil {
		return nil, err
	}
	if selected.Current.GenerationDigest != expected.ObservationGenerationDigest ||
		selected.Current.InventoryDigest != expected.ObservationRootDigest ||
		selected.Current.SourceRootDigest != expected.SourceRootDigest {
		return nil, ErrStale
	}
	directory := filepath.Join(
		inventoryGenerationDirectoryV2(root, repository, selected.Current.GenerationDigest),
		InventoryPublicationInventoryNameV2,
	)
	inventory, err := ReadInventoryRootV2(directory, repository)
	if err != nil {
		return nil, err
	}
	lease, err := cache.Acquire(ctx, directory, inventory)
	if err != nil {
		return nil, err
	}
	if lease.Publication().DownstreamAuthority() != expected {
		lease.Release()
		return nil, ErrStale
	}
	confirmed, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil || confirmed.Current != selected.Current {
		lease.Release()
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrStale
		}
		return nil, errors.Join(err, ErrStale)
	}
	return lease, nil
}

const (
	DownstreamAuthorityV1 = "observation-v1"
	DownstreamAuthorityV2 = "observation-v2"
)

// DownstreamAuthority is the small, versioned observation identity used by
// derived generations. V1 fields retain the shipped manifest identity; V2
// names the independently published source super-root and inventory root.
// Consumers must bind the complete value rather than treating a generation
// digest as a sufficient substitute for its policy and root digests.
type DownstreamAuthority struct {
	Version                     string `json:"version"`
	Repository                  string `json:"repository"`
	SourceGenerationDigest      string `json:"source_generation_digest"`
	SourceRootDigest            string `json:"source_root_digest"`
	ObservationGenerationDigest string `json:"observation_generation_digest"`
	ObservationRootDigest       string `json:"observation_root_digest"`
	PartitionPolicyDigest       string `json:"partition_policy_digest"`
	ObservationPolicyDigest     string `json:"observation_policy_digest"`
	InventoryPolicyDigest       string `json:"inventory_policy_digest,omitempty"`
	RecordCount                 int    `json:"record_count"`
	ObservedCount               int    `json:"observed_count"`
}

// DownstreamSource is the version-neutral input surface for relationship
// component builders. WalkObserved is cold construction work; Authority is a
// bounded control read used by planning and no-op comparison.
type DownstreamSource interface {
	DownstreamAuthority() DownstreamAuthority
	WalkObserved(context.Context, func(Record, sourceobservation.Observation) error) error
}

func (publication *Publication) DownstreamAuthority() DownstreamAuthority {
	manifest := publication.Manifest()
	return DownstreamAuthority{
		Version: DownstreamAuthorityV1, Repository: manifest.Repository,
		SourceGenerationDigest:      manifest.SourceGenerationDigest,
		SourceRootDigest:            manifest.PartitionManifestDigest,
		ObservationGenerationDigest: manifest.GenerationDigest,
		ObservationRootDigest:       manifest.Digest,
		PartitionPolicyDigest:       manifest.PartitionPolicyDigest,
		ObservationPolicyDigest:     manifest.ObservationPolicyDigest,
		RecordCount:                 manifest.RecordCount, ObservedCount: manifest.ObservedCount,
	}
}

func (publication *InventoryPublicationV2) DownstreamAuthority() DownstreamAuthority {
	root := publication.Root()
	return DownstreamAuthority{
		Version: DownstreamAuthorityV2, Repository: root.Repository,
		SourceGenerationDigest:      root.SourceGenerationDigest,
		SourceRootDigest:            root.SourcePartitionRootDigest,
		ObservationGenerationDigest: root.GenerationDigest,
		ObservationRootDigest:       root.Digest,
		PartitionPolicyDigest:       root.PartitionPolicyDigest,
		ObservationPolicyDigest:     root.ObservationPolicyDigest,
		InventoryPolicyDigest:       root.InventoryPolicyDigest,
		RecordCount:                 root.RecordCount, ObservedCount: root.ObservedCount,
	}
}

// WalkObserved streams a validated V2 inventory in canonical segment/member
// order. It never opens the independently retained source super-root: every
// member is already bound to that root by the validated inventory controls.
func (publication *InventoryPublicationV2) WalkObserved(
	ctx context.Context,
	visit func(Record, sourceobservation.Observation) error,
) error {
	if publication == nil || visit == nil {
		return invalid("inventory v2 observation visitor")
	}
	for _, entry := range publication.root.Segments {
		if err := ctx.Err(); err != nil {
			return err
		}
		segment, err := readInventorySegmentV2(publication.directory, publication.root, entry)
		if err != nil {
			return err
		}
		directory := filepath.Join(publication.directory, entry.Directory)
		for ordinal, expected := range segment.Members {
			member, records, err := readMember(ctx, directory, ordinal, len(segment.Members))
			member.SourceMemberDigest = expected.SourceMemberDigest
			if err != nil || inventoryMemberFromV1(member, expected) != expected {
				return invalid("inventory v2 walk member mismatch")
			}
			for _, record := range records {
				if record.State != "observed" {
					continue
				}
				observation, err := readObservation(directory, record)
				if err != nil {
					return err
				}
				record.Placements = slices.Clone(record.Placements)
				for index := range record.Placements {
					record.Placements[index].Revisions = slices.Clone(record.Placements[index].Revisions)
				}
				if err := visit(record, observation); err != nil {
					return err
				}
			}
		}
	}
	return ctx.Err()
}

var _ DownstreamSource = (*Publication)(nil)
var _ DownstreamSource = (*InventoryPublicationV2)(nil)
