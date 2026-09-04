package observationpublication

import (
	"context"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

// InventoryAuthorityV2 is the small control-only authority T40.10 binds into
// each extraction plan. It contains no observation or source member bytes.
type InventoryAuthorityV2 struct {
	SourceGenerationDigest      string
	ObservationGenerationDigest string
	SourceRootDigest            string
	InventoryRootDigest         string
}

// CurrentInventoryAuthorityReferenceV2 performs the final publication-fence
// read: one latest pointer plus its selected small source super-root, followed
// by a pointer reread. The generation was fully validated when installed and
// by CurrentInventoryAuthorityV2 during planning, so the final fence does not
// rescan every source/inventory segment for every completed domain.
func CurrentInventoryAuthorityReferenceV2(
	ctx context.Context,
	root,
	repository string,
) (InventoryAuthorityV2, error) {
	if err := ctx.Err(); err != nil {
		return InventoryAuthorityV2{}, err
	}
	selected, err := ReadInventoryPublicationRootV2Context(ctx, root, repository)
	if err != nil {
		return InventoryAuthorityV2{}, err
	}
	directory := inventoryGenerationDirectoryV2(root, repository, selected.Current.GenerationDigest)
	sourceRoot, err := sourcepartition.ReadSuperRootContext(
		ctx, filepath.Join(directory, InventoryPublicationSourceNameV2), repository,
	)
	if err != nil {
		return InventoryAuthorityV2{}, err
	}
	if sourceRoot.Digest != selected.Current.SourceRootDigest {
		return InventoryAuthorityV2{}, invalid("inventory v2 selected source authority changed")
	}
	confirmed, err := ReadInventoryPublicationRootV2Context(ctx, root, repository)
	if err != nil || confirmed.Current != selected.Current {
		return InventoryAuthorityV2{}, invalid("inventory v2 pointer changed during authority read")
	}
	return InventoryAuthorityV2{
		SourceGenerationDigest:      sourceRoot.SourceGenerationDigest,
		ObservationGenerationDigest: selected.Current.GenerationDigest,
		SourceRootDigest:            sourceRoot.Digest,
		InventoryRootDigest:         selected.Current.InventoryDigest,
	}, nil
}

// CurrentInventoryAuthorityV2 validates the selected source/inventory roots
// and rereads the latest pointer before returning. Callers performing a final
// publication fence hold the observation transition lock around this call.
func CurrentInventoryAuthorityV2(
	ctx context.Context,
	root,
	repository string,
) (InventoryAuthorityV2, error) {
	selected, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil {
		return InventoryAuthorityV2{}, err
	}
	directory := inventoryGenerationDirectoryV2(root, repository, selected.Current.GenerationDigest)
	sourceDirectory := filepath.Join(directory, InventoryPublicationSourceNameV2)
	inventoryDirectory := filepath.Join(directory, InventoryPublicationInventoryNameV2)
	sourceRoot, err := sourcepartition.ReadSuperRoot(sourceDirectory, repository)
	if err != nil {
		return InventoryAuthorityV2{}, err
	}
	plan, err := sourcepartition.OpenSuperRoot(ctx, sourceDirectory, sourceRoot)
	if err != nil {
		return InventoryAuthorityV2{}, err
	}
	inventoryRoot, err := ReadInventoryRootV2(inventoryDirectory, repository)
	if err != nil {
		return InventoryAuthorityV2{}, err
	}
	if err := ValidateInventoryStageV2(ctx, inventoryDirectory, inventoryRoot); err != nil {
		return InventoryAuthorityV2{}, err
	}
	if err := ValidateInventorySourceV2(inventoryDirectory, inventoryRoot, plan); err != nil {
		return InventoryAuthorityV2{}, err
	}
	if selected.Current.SourceRootDigest != sourceRoot.Digest ||
		selected.Current.InventoryDigest != inventoryRoot.Digest ||
		selected.Current.GenerationDigest != inventoryRoot.GenerationDigest {
		return InventoryAuthorityV2{}, invalid("inventory v2 selected authority changed")
	}
	confirmed, err := ReadInventoryPublicationRootV2(root, repository)
	if err != nil || confirmed.Current != selected.Current {
		return InventoryAuthorityV2{}, invalid("inventory v2 pointer changed during authority read")
	}
	return InventoryAuthorityV2{
		SourceGenerationDigest:      sourceRoot.SourceGenerationDigest,
		ObservationGenerationDigest: inventoryRoot.GenerationDigest,
		SourceRootDigest:            sourceRoot.Digest, InventoryRootDigest: inventoryRoot.Digest,
	}, nil
}
