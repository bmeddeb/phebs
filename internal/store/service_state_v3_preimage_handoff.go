package store

import (
	"context"
	"fmt"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// These are statement attempts, not SA01 submitted rows. No catalog member
// payload is decoded by this operation, so MemberVisits remains zero.
const (
	ServiceStateV3PreimageHandoffDeleteLimit       = MaxServiceCatalogV3OrphanDelete
	ServiceStateV3PreimageHandoffStoreReadMaximum  = 9
	ServiceStateV3PreimageHandoffStoreWriteMaximum = 2
)

type ServiceStateV3PreimageDrain struct {
	// Deleted counts committed preimage rows and the repository summary, at
	// most MaxServiceCatalogV3OrphanDelete. Error results prove no delete count.
	Deleted int
	// Done proves the entire repository preimage inventory is empty. Protected
	// accompanies a refusal to delete the currently selected snapshot.
	Done      bool
	Protected bool
}

// DrainServiceStateV3PreimageHandoff performs one repository-local cleanup
// turn. The caller must hold EXCLUSIVE lifecycle mutation custody and join old snapshot
// readers/owners before entering; this method does not establish that join.
// The exact selector is checked in the same transaction as every delete. The
// exclusive lock, not a no-op UPDATE or an MVCC read, excludes selector CAS.
// No catalog root, member, current state, or retained plan is deleted. There
// is no retry: even an empty successful turn begins and commits its read check.
func (s *Surreal) DrainServiceStateV3PreimageHandoff(
	ctx context.Context,
	selected ServiceRuntimeSelector,
) (ServiceStateV3PreimageDrain, error) {
	if ctx == nil || validateServiceRuntimeSelector(selected) != nil || selected.Backend != ServiceRuntimeV3 {
		return ServiceStateV3PreimageDrain{}, ErrInvalidServiceRuntimeSelector
	}
	if err := ctx.Err(); err != nil {
		return ServiceStateV3PreimageDrain{}, err
	}
	tx, err := storeBegin(ctx, s.accounting, s.db)
	if err != nil {
		return ServiceStateV3PreimageDrain{}, fmt.Errorf("preimage handoff: begin: %w", err)
	}
	defer func() {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = storeCancel(cancelCtx, s.accounting, tx)
	}()
	if err := s.fenceServiceStateV3PreimageHandoff(ctx, tx, selected); err != nil {
		return ServiceStateV3PreimageDrain{}, err
	}
	prior, err := s.serviceStateV3HandoffInventory(ctx, tx, selected.Repository)
	if err != nil {
		return ServiceStateV3PreimageDrain{}, err
	}
	result := ServiceStateV3PreimageDrain{Done: prior == nil}
	if prior != nil {
		result.Protected = prior.SnapshotRevision == selected.StateControlRevision &&
			prior.SnapshotDigest == selected.StateSummaryDigest
		if result.Protected {
			if prior.CatalogGeneration != selected.CatalogRootDigest {
				return ServiceStateV3PreimageDrain{}, ErrInvalidServiceStateV3
			}
			return ServiceStateV3PreimageDrain{Protected: true}, ErrConflict
		} else {
			if err := chargeServiceStateV3HandoffRead(ctx, true); err != nil {
				return ServiceStateV3PreimageDrain{}, err
			}
			owners, err := storeQuery[[]serviceCatalogV3LifecycleRec](ctx, s.accounting, tx,
				"SELECT * FROM $rid LIMIT 1", map[string]any{
					"rid": serviceCatalogV3LifecycleID(prior.CatalogGeneration),
				}, storeRead())
			if err != nil {
				return ServiceStateV3PreimageDrain{}, fmt.Errorf("preimage handoff: historical owner: %w", err)
			}
			ownerRows := firstDomainRows(owners)
			if len(ownerRows) != 1 || ownerRows[0].Repository != selected.Repository ||
				ownerRows[0].RootDigest != prior.CatalogGeneration {
				return ServiceStateV3PreimageDrain{}, ErrInvalidServiceCatalogV3Lifecycle
			}
			result.Deleted, err = s.drainServiceStateV3PreimagesTx(
				ctx, tx, ownerRows[0], ServiceStateV3PreimageHandoffDeleteLimit, true,
			)
			if err != nil {
				return ServiceStateV3PreimageDrain{}, err
			}
			remaining, err := s.serviceStateV3HandoffInventory(ctx, tx, selected.Repository)
			if err != nil {
				return ServiceStateV3PreimageDrain{}, err
			}
			result.Done = remaining == nil
			if result.Deleted == 0 && !result.Done {
				return ServiceStateV3PreimageDrain{}, ErrInvalidServiceStateV3
			}
		}
	}
	if err := storeCommit(ctx, s.accounting, tx); err != nil {
		return ServiceStateV3PreimageDrain{}, fmt.Errorf("preimage handoff: commit: %w", err)
	}
	return result, nil
}

func (s *Surreal) fenceServiceStateV3PreimageHandoff(
	ctx context.Context,
	tx *surrealdb.Transaction,
	selected ServiceRuntimeSelector,
) error {
	if err := chargeServiceStateV3HandoffRead(ctx, true); err != nil {
		return err
	}
	fenced, err := storeQuery[[]serviceRuntimeSelectorRec](ctx, s.accounting, tx,
		"SELECT * FROM $rid LIMIT 1", map[string]any{
			"rid": serviceRuntimeSelectorID(selected.Repository),
		}, storeRead())
	if err != nil {
		return fmt.Errorf("preimage handoff: selector fence: %w", err)
	}
	rows := firstDomainRows(fenced)
	if len(rows) != 1 {
		return ErrConflict
	}
	actual, err := serviceRuntimeSelectorFromRec(rows[0])
	if err != nil || actual != selected {
		return ErrConflict
	}
	return nil
}

// The summary is the snapshot owner; row desired/active catalog identities
// cannot substitute for it. When no summary remains, a bounded sentinel
// refuses orphan rows. It never scans every live row to exclude one snapshot.
func (s *Surreal) serviceStateV3HandoffInventory(
	ctx context.Context,
	tx *surrealdb.Transaction,
	repository string,
) (*serviceRepositoryStateRec, error) {
	if err := chargeServiceStateV3HandoffRead(ctx, true); err != nil {
		return nil, err
	}
	results, err := storeQuery[[]serviceRepositoryStateRec](ctx, s.accounting, tx, `
SELECT * FROM service_state_v3_repository_preimage
	WHERE repository = $repository LIMIT 2`, map[string]any{"repository": repository}, storeRead())
	if err != nil {
		return nil, fmt.Errorf("preimage handoff: summary inventory: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) > 1 {
		return nil, ErrInvalidServiceStateV3
	}
	var prior *serviceRepositoryStateRec
	if len(rows) == 1 {
		prior = &rows[0]
		summary, err := serviceStateV3RepositoryFromRec(*prior)
		if err != nil || summary.Repository != repository ||
			!validSHA256Digest(summary.CatalogGeneration) ||
			prior.SnapshotRevision != summary.ControlRevision ||
			prior.SnapshotDigest != summary.SummaryDigest ||
			!validServiceStateV3PreimageRecord(prior.RecID, "service_state_v3_repository_preimage") {
			return nil, ErrInvalidServiceStateV3
		}
	}
	if prior != nil {
		return prior, nil
	}
	if err := chargeServiceStateV3HandoffRead(ctx, true); err != nil {
		return nil, err
	}
	orphans, err := storeQuery[[]struct {
		ID *models.RecordID `json:"id"`
	}](ctx, s.accounting, tx, `
SELECT id FROM service_state_v3_preimage WHERE repository = $repository LIMIT 1`, map[string]any{
		"repository": repository,
	}, storeRead())
	if err != nil {
		return nil, fmt.Errorf("preimage handoff: orphan inventory: %w", err)
	}
	if len(firstDomainRows(orphans)) != 0 {
		return nil, ErrInvalidServiceStateV3
	}
	return prior, nil
}

func chargeServiceStateV3HandoffRead(ctx context.Context, handoff bool) error {
	if !handoff {
		return nil
	}
	return readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1)
}

func chargeServiceStateV3HandoffWrite(ctx context.Context, handoff bool) error {
	if !handoff {
		return nil
	}
	return readaccounting.Charge(ctx, readaccounting.StoreWriteAttempt, 1)
}
