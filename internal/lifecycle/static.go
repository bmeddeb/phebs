package lifecycle

import (
	"context"
	"errors"
	"time"
)

const (
	CatalogOwner       = "catalog-generations"
	SourceOwner        = "source-generations"
	SearchOwner        = "search-generations"
	ObservationOwner   = "observation-namespaces"
	ObservationV2Owner = "observation-v2-generations"
	ResolverOwner      = "resolver-namespaces"
	RelationshipOwner  = "relationship-namespaces"
	ProofOwner         = "proof-bundles"
	InvestigationOwner = "investigations"
	ReaderOwner        = "readers"
	JobOwner           = "durable-jobs"
	TombstoneOwner     = "service-tombstones"
	PartialStageOwner  = "partial-stages"
)

type StaticOwner struct {
	OwnerName    string
	Completeness Completeness
	Reason       string
}

func (owner StaticOwner) Name() string { return owner.OwnerName }

func (owner StaticOwner) Sweep(
	ctx context.Context, _ time.Time, cursor string, _ Limits,
) OwnerResult {
	if err := ctx.Err(); err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	result := OwnerResult{Cursor: "", Completeness: owner.Completeness}
	if owner.Completeness == Unavailable {
		if owner.Reason == "" {
			owner.Reason = "owner has no bounded collector"
		}
		result.Err = errors.New(owner.Reason)
	}
	return result
}

// ClosedOwners makes non-destructive owner state explicit until its physical
// representation exists or an owner-specific collector is migrated. Exact
// entries have no independent collectible history in the current runtime;
// unavailable entries deliberately remain unbounded rather than being swept
// through a neighboring owner's rules.
func ClosedOwners() []Owner {
	return []Owner{
		StaticOwner{OwnerName: SourceOwner, Completeness: Exact},
		StaticOwner{OwnerName: ResolverOwner, Completeness: Exact},
		StaticOwner{OwnerName: ProofOwner, Completeness: Exact},
		StaticOwner{OwnerName: InvestigationOwner, Completeness: Exact},
		StaticOwner{OwnerName: ReaderOwner, Completeness: Exact},
		StaticOwner{OwnerName: TombstoneOwner, Completeness: Exact},
	}
}
