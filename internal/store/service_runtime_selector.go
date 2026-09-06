package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

const (
	ServiceRuntimeSelectorSchema = "phebs-service-runtime-selector-v1"
	ServiceRuntimeV2             = "v2"
	ServiceRuntimeV3             = "v3"

	MaxServiceRuntimeSelectors = MaxServiceCatalogV3LifecycleRoots

	serviceRuntimeSelectorSchemaMigrationVersion = "t41.9-service-runtime-selector-schema-v1"
	// This historical version was written into a marker understood by the full
	// supported predecessor set. Those binaries accept only the prior
	// candidate-control value and therefore refuse an activated selector before
	// any of them can serve.
	serviceRuntimeSelectorCompatibilityMigrationVersion = "t41.9-service-runtime-selector-compat-v1"
)

var ErrInvalidServiceRuntimeSelector = errors.New("invalid service runtime selector")

// ServiceRuntimeSelector is the one repository-local linearization point for
// every service-aware product consumer. Exactly one catalog identity is set:
// the legacy generation for v2 or the segmented root for v3.
type ServiceRuntimeSelector struct {
	Schema                       string
	Repository                   string
	Backend                      string
	CatalogGenerationDigest      string
	CatalogRootDigest            string
	CatalogControlRevision       uint64
	StateControlRevision         uint64
	StateSummaryDigest           string
	SearchGenerationDigest       string
	RelationshipGenerationDigest string
	RelationshipRootDigest       string
	ControlRevision              uint64
	Digest                       string
	ChangedAt                    time.Time
}

// ServiceRuntimeTarget is the exact already-built authority proposed for one
// selector CAS. Filesystem roots are validated by the caller while it holds
// the shared mutation/backup exclusion; the store transaction rechecks every
// database-owned component.
type ServiceRuntimeTarget struct {
	CatalogGenerationDigest      string
	CatalogRootDigest            string
	CatalogControlRevision       uint64
	StateControlRevision         uint64
	StateSummaryDigest           string
	SearchGenerationDigest       string
	RelationshipGenerationDigest string
	RelationshipRootDigest       string
}

type ServiceRuntimeSelectionRequest struct {
	Repository              string
	ExpectedControlRevision uint64
	ExpectedDigest          string
	Target                  ServiceRuntimeTarget
}

type ServiceRuntimeSelectorReader interface {
	GetServiceRuntimeSelector(context.Context, string) (ServiceRuntimeSelector, error)
	ConfirmServiceRuntimeSelector(context.Context, ServiceRuntimeSelector) error
}

// ConfirmServiceRuntimeSelectorAbsent is the final fence for compatibility-mode
// v2 reads. A first selector CAS must invalidate every request that began while
// the selector was absent.
func ConfirmServiceRuntimeSelectorAbsent(
	ctx context.Context,
	reader ServiceRuntimeSelectorReader,
	repository string,
) error {
	if reader == nil {
		return ErrInvalidServiceRuntimeSelector
	}
	_, err := reader.GetServiceRuntimeSelector(ctx, repository)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrConflict
}

type ServiceRuntimeSelectorWriter interface {
	SelectServiceRuntimeV2(
		context.Context,
		ServiceRuntimeSelectionRequest,
	) (ServiceRuntimeSelector, error)
	SelectServiceRuntimeV3(
		context.Context,
		ServiceRuntimeSelectionRequest,
	) (ServiceRuntimeSelector, error)
}

// ServiceRuntimeSelectorRetirer is the repository-deletion boundary. The
// deleting repository row, selector, and catalog reference are one database
// authority; callers remove filesystem bytes only after this method succeeds.
type ServiceRuntimeSelectorRetirer interface {
	RetireServiceRuntimeSelectorForRepositoryDeletion(context.Context, string) error
}

var _ ServiceRuntimeSelectorReader = (*Surreal)(nil)
var _ ServiceRuntimeSelectorWriter = (*Surreal)(nil)
var _ ServiceRuntimeSelectorRetirer = (*Surreal)(nil)

type serviceRuntimeSelectorRec struct {
	Schema                       string           `json:"schema"`
	Repository                   string           `json:"repository"`
	Backend                      string           `json:"backend"`
	CatalogGenerationDigest      string           `json:"catalog_generation_digest"`
	CatalogRootDigest            string           `json:"catalog_root_digest"`
	CatalogControlRevision       uint64           `json:"catalog_control_revision"`
	StateControlRevision         uint64           `json:"state_control_revision"`
	StateSummaryDigest           string           `json:"state_summary_digest"`
	SearchGenerationDigest       string           `json:"search_generation_digest"`
	RelationshipGenerationDigest string           `json:"relationship_generation_digest"`
	RelationshipRootDigest       string           `json:"relationship_root_digest"`
	ControlRevision              uint64           `json:"control_revision"`
	Digest                       string           `json:"digest"`
	ChangedAt                    time.Time        `json:"changed_at"`
	RecID                        *models.RecordID `json:"id"`
}

type serviceRuntimeMigrationRec struct {
	Version string `json:"version"`
}

func serviceRuntimeSelectorID(repository string) models.RecordID {
	return models.NewRecordID("service_runtime_selector", repository)
}

func serviceRuntimeSelectorSchemaMigrationID() models.RecordID {
	return models.NewRecordID("store_migration", "service_runtime_selector_schema")
}

func serviceRuntimeCurrentCatalogReferenceID(repository string) models.RecordID {
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-service-runtime-current-catalog-v1\x00"))
	_, _ = hash.Write([]byte(repository))
	return models.NewRecordID(
		"service_catalog_v3_state_reference",
		"runtime-current-"+hex.EncodeToString(hash.Sum(nil)),
	)
}

func serviceRuntimeSelectorFromRec(
	record serviceRuntimeSelectorRec,
) (ServiceRuntimeSelector, error) {
	selector := ServiceRuntimeSelector{
		Schema: record.Schema, Repository: record.Repository, Backend: record.Backend,
		CatalogGenerationDigest:      record.CatalogGenerationDigest,
		CatalogRootDigest:            record.CatalogRootDigest,
		CatalogControlRevision:       record.CatalogControlRevision,
		StateControlRevision:         record.StateControlRevision,
		StateSummaryDigest:           record.StateSummaryDigest,
		SearchGenerationDigest:       record.SearchGenerationDigest,
		RelationshipGenerationDigest: record.RelationshipGenerationDigest,
		RelationshipRootDigest:       record.RelationshipRootDigest,
		ControlRevision:              record.ControlRevision, Digest: record.Digest,
		ChangedAt: record.ChangedAt.UTC(),
	}
	if record.RecID == nil {
		return ServiceRuntimeSelector{}, ErrInvalidServiceRuntimeSelector
	}
	identifier, ok := record.RecID.ID.(string)
	if record.RecID.Table != "service_runtime_selector" ||
		!ok || identifier != selector.Repository ||
		validateServiceRuntimeSelector(selector) != nil {
		return ServiceRuntimeSelector{}, ErrInvalidServiceRuntimeSelector
	}
	return selector, nil
}

func serviceRuntimeSelectorDigest(selector ServiceRuntimeSelector) string {
	payload := struct {
		Schema                       string `json:"schema"`
		Repository                   string `json:"repository"`
		Backend                      string `json:"backend"`
		CatalogGenerationDigest      string `json:"catalog_generation_digest"`
		CatalogRootDigest            string `json:"catalog_root_digest"`
		CatalogControlRevision       uint64 `json:"catalog_control_revision"`
		StateControlRevision         uint64 `json:"state_control_revision"`
		StateSummaryDigest           string `json:"state_summary_digest"`
		SearchGenerationDigest       string `json:"search_generation_digest"`
		RelationshipGenerationDigest string `json:"relationship_generation_digest"`
		RelationshipRootDigest       string `json:"relationship_root_digest"`
		ControlRevision              uint64 `json:"control_revision"`
	}{
		Schema: selector.Schema, Repository: selector.Repository, Backend: selector.Backend,
		CatalogGenerationDigest:      selector.CatalogGenerationDigest,
		CatalogRootDigest:            selector.CatalogRootDigest,
		CatalogControlRevision:       selector.CatalogControlRevision,
		StateControlRevision:         selector.StateControlRevision,
		StateSummaryDigest:           selector.StateSummaryDigest,
		SearchGenerationDigest:       selector.SearchGenerationDigest,
		RelationshipGenerationDigest: selector.RelationshipGenerationDigest,
		RelationshipRootDigest:       selector.RelationshipRootDigest,
		ControlRevision:              selector.ControlRevision,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validateServiceRuntimeTarget(backend string, target ServiceRuntimeTarget) error {
	if target.CatalogControlRevision == 0 || target.StateControlRevision == 0 ||
		!validSHA256Digest(target.StateSummaryDigest) ||
		!validSHA256Digest(target.SearchGenerationDigest) ||
		!validSHA256Digest(target.RelationshipGenerationDigest) ||
		!validSHA256Digest(target.RelationshipRootDigest) {
		return ErrInvalidServiceRuntimeSelector
	}
	switch backend {
	case ServiceRuntimeV2:
		if !validSHA256Digest(target.CatalogGenerationDigest) ||
			target.CatalogRootDigest != "" {
			return ErrInvalidServiceRuntimeSelector
		}
	case ServiceRuntimeV3:
		if target.CatalogGenerationDigest != "" ||
			!validSHA256Digest(target.CatalogRootDigest) {
			return ErrInvalidServiceRuntimeSelector
		}
	default:
		return ErrInvalidServiceRuntimeSelector
	}
	return nil
}

func validateServiceRuntimeSelector(selector ServiceRuntimeSelector) error {
	if selector.Schema != ServiceRuntimeSelectorSchema ||
		validateCandidateRepository(selector.Repository) != nil ||
		selector.ControlRevision == 0 || selector.ControlRevision > math.MaxInt64 ||
		selector.ChangedAt.IsZero() || selector.ChangedAt.Location() != time.UTC {
		return ErrInvalidServiceRuntimeSelector
	}
	target := ServiceRuntimeTarget{
		CatalogGenerationDigest:      selector.CatalogGenerationDigest,
		CatalogRootDigest:            selector.CatalogRootDigest,
		CatalogControlRevision:       selector.CatalogControlRevision,
		StateControlRevision:         selector.StateControlRevision,
		StateSummaryDigest:           selector.StateSummaryDigest,
		SearchGenerationDigest:       selector.SearchGenerationDigest,
		RelationshipGenerationDigest: selector.RelationshipGenerationDigest,
		RelationshipRootDigest:       selector.RelationshipRootDigest,
	}
	if validateServiceRuntimeTarget(selector.Backend, target) != nil ||
		selector.Digest != serviceRuntimeSelectorDigest(selector) {
		return ErrInvalidServiceRuntimeSelector
	}
	return nil
}

func serviceRuntimeSelectorContent(selector ServiceRuntimeSelector) map[string]any {
	return map[string]any{
		"schema": selector.Schema, "repository": selector.Repository,
		"backend":                        selector.Backend,
		"catalog_generation_digest":      selector.CatalogGenerationDigest,
		"catalog_root_digest":            selector.CatalogRootDigest,
		"catalog_control_revision":       selector.CatalogControlRevision,
		"state_control_revision":         selector.StateControlRevision,
		"state_summary_digest":           selector.StateSummaryDigest,
		"search_generation_digest":       selector.SearchGenerationDigest,
		"relationship_generation_digest": selector.RelationshipGenerationDigest,
		"relationship_root_digest":       selector.RelationshipRootDigest,
		"control_revision":               selector.ControlRevision,
		"digest":                         selector.Digest, "changed_at": selector.ChangedAt,
	}
}

func (s *Surreal) GetServiceRuntimeSelector(
	ctx context.Context,
	repository string,
) (ServiceRuntimeSelector, error) {
	if err := validateCandidateRepository(repository); err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf(
			"get service runtime selector: repository: %w", err,
		)
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf("get service runtime selector: %w", err)
	}
	results, err := storeQuery[[]serviceRuntimeSelectorRec](
		ctx, s.accounting, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceRuntimeSelectorID(repository)}, storeRead(),
	)
	if err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf("get service runtime selector: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return ServiceRuntimeSelector{}, ErrNotFound
	}
	if len(rows) != 1 {
		return ServiceRuntimeSelector{}, ErrInvalidServiceRuntimeSelector
	}
	selector, err := serviceRuntimeSelectorFromRec(rows[0])
	if err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf("get service runtime selector: %w", err)
	}
	return selector, nil
}

func (s *Surreal) ConfirmServiceRuntimeSelector(
	ctx context.Context,
	expected ServiceRuntimeSelector,
) error {
	if validateServiceRuntimeSelector(expected) != nil {
		return fmt.Errorf("confirm service runtime selector: %w", ErrInvalidServiceRuntimeSelector)
	}
	current, err := s.GetServiceRuntimeSelector(ctx, expected.Repository)
	if err != nil {
		return fmt.Errorf("confirm service runtime selector: %w", err)
	}
	if current != expected {
		return fmt.Errorf("confirm service runtime selector: changed: %w", ErrConflict)
	}
	return nil
}

func (s *Surreal) ListServiceRuntimeSelectors(
	ctx context.Context,
) ([]ServiceRuntimeSelector, error) {
	results, err := storeQuery[[]serviceRuntimeSelectorRec](ctx, s.accounting, s.db, `
SELECT * FROM service_runtime_selector
	ORDER BY repository LIMIT $limit`, map[string]any{
		"limit": MaxServiceRuntimeSelectors + 1,
	}, storeRead())
	if err != nil {
		return nil, fmt.Errorf("list service runtime selectors: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) > MaxServiceRuntimeSelectors {
		return nil, fmt.Errorf("list service runtime selectors: %w", ErrInvalidServiceRuntimeSelector)
	}
	selectors := make([]ServiceRuntimeSelector, 0, len(rows))
	prior := ""
	for _, row := range rows {
		selector, decodeErr := serviceRuntimeSelectorFromRec(row)
		if decodeErr != nil || selector.Repository <= prior {
			return nil, fmt.Errorf("list service runtime selectors: %w", ErrInvalidServiceRuntimeSelector)
		}
		prior = selector.Repository
		selectors = append(selectors, selector)
	}
	return selectors, nil
}

// RetireServiceRuntimeSelectorForRepositoryDeletion atomically removes the
// selected runtime and its v3 catalog-lifecycle reference after repository
// authorization has entered the deleting state. The compatibility latch is
// deliberately irreversible.
func (s *Surreal) RetireServiceRuntimeSelectorForRepositoryDeletion(
	ctx context.Context,
	repository string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if validateCandidateRepository(repository) != nil {
		return fmt.Errorf(
			"retire service runtime selector: repository: %w",
			ErrInvalidServiceRuntimeSelector,
		)
	}
	for attempt := 0; ; attempt++ {
		results, err := storeQuery[[]bool](ctx, s.accounting, s.db, `
BEGIN;
LET $repository = (SELECT deleting FROM $repo_rid)[0];
LET $ready = $repository != NONE AND ($repository.deleting ?? false) = true;
IF $ready {
	DELETE $selector_rid RETURN NONE;
	DELETE $reference_rid RETURN NONE;
};
RETURN [$ready];
COMMIT;`, map[string]any{
			"repo_rid":      repoID(repository),
			"selector_rid":  serviceRuntimeSelectorID(repository),
			"reference_rid": serviceRuntimeCurrentCatalogReferenceID(repository),
		}, storeWrite(2))
		if err != nil && isRetryable(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
			continue
		}
		if err != nil {
			return fmt.Errorf("retire service runtime selector: %w", err)
		}
		for _, result := range *results {
			if len(result.Result) != 0 {
				if result.Result[0] {
					return nil
				}
				return fmt.Errorf(
					"retire service runtime selector: repository is not deleting: %w",
					ErrConflict,
				)
			}
		}
		return errors.New("retire service runtime selector: result is absent")
	}
}

func (s *Surreal) SelectServiceRuntimeV2(
	ctx context.Context,
	request ServiceRuntimeSelectionRequest,
) (ServiceRuntimeSelector, error) {
	return s.selectServiceRuntime(ctx, ServiceRuntimeV2, request)
}

func (s *Surreal) SelectServiceRuntimeV3(
	ctx context.Context,
	request ServiceRuntimeSelectionRequest,
) (ServiceRuntimeSelector, error) {
	return s.selectServiceRuntime(ctx, ServiceRuntimeV3, request)
}

// ValidateServiceRuntimeDatabaseTarget repeats the exact read-only database
// half of selector admission for startup and backup/restore validation.
func (s *Surreal) ValidateServiceRuntimeDatabaseTarget(
	ctx context.Context,
	repository, backend string,
	target ServiceRuntimeTarget,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if validateCandidateRepository(repository) != nil ||
		validateServiceRuntimeTarget(backend, target) != nil {
		return fmt.Errorf(
			"validate service runtime %s database target: %w",
			backend, ErrInvalidServiceRuntimeSelector,
		)
	}
	tx, err := storeBegin(ctx, s.accounting, s.db)
	if err != nil {
		return fmt.Errorf("validate service runtime %s database target: begin: %w", backend, err)
	}
	defer func() {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = storeCancel(cancelCtx, s.accounting, tx)
	}()
	if err := verifyServiceRuntimeRepository(ctx, s.accounting, tx, repository); err != nil {
		return fmt.Errorf(
			"validate service runtime %s database target: repository: %w",
			backend, err,
		)
	}
	if backend == ServiceRuntimeV2 {
		err = verifyServiceRuntimeV2Target(ctx, s.accounting, tx, repository, target)
	} else {
		err = verifyServiceRuntimeV3Target(ctx, s.accounting, tx, repository, target, false)
	}
	if err != nil {
		return fmt.Errorf("validate service runtime %s database target: %w", backend, err)
	}
	return nil
}

func (s *Surreal) selectServiceRuntime(
	ctx context.Context,
	backend string,
	request ServiceRuntimeSelectionRequest,
) (_ ServiceRuntimeSelector, retErr error) {
	if err := ctx.Err(); err != nil {
		return ServiceRuntimeSelector{}, err
	}
	if validateCandidateRepository(request.Repository) != nil ||
		validateServiceRuntimeTarget(backend, request.Target) != nil ||
		request.ExpectedControlRevision == 0 && request.ExpectedDigest != "" ||
		request.ExpectedControlRevision > 0 && !validSHA256Digest(request.ExpectedDigest) ||
		request.ExpectedControlRevision >= math.MaxInt64 {
		return ServiceRuntimeSelector{}, fmt.Errorf(
			"select service runtime %s: %w", backend, ErrInvalidServiceRuntimeSelector,
		)
	}
	tx, err := storeBegin(ctx, s.accounting, s.db)
	if err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: begin: %w", backend, err)
	}
	defer func() {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = storeCancel(cancelCtx, s.accounting, tx)
	}()

	currentResults, err := storeQuery[[]serviceRuntimeSelectorRec](
		ctx, s.accounting, tx, "SELECT * FROM $rid",
		map[string]any{"rid": serviceRuntimeSelectorID(request.Repository)}, storeRead(),
	)
	if err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: current: %w", backend, err)
	}
	currentRows := firstDomainRows(currentResults)
	if len(currentRows) > 1 {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: %w", backend, ErrConflict)
	}
	if request.ExpectedControlRevision == 0 {
		if len(currentRows) != 0 {
			return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: stale absent selector: %w", backend, ErrConflict)
		}
	} else {
		if len(currentRows) != 1 {
			return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: selector disappeared: %w", backend, ErrConflict)
		}
		current, currentErr := serviceRuntimeSelectorFromRec(currentRows[0])
		if currentErr != nil || current.ControlRevision != request.ExpectedControlRevision ||
			current.Digest != request.ExpectedDigest {
			return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: stale selector: %w", backend, ErrConflict)
		}
	}
	if err := verifyServiceRuntimeRepository(ctx, s.accounting, tx, request.Repository); err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf(
			"select service runtime %s: repository: %w", backend, err,
		)
	}

	compatibility, err := serviceRuntimeCompatibilityVersion(ctx, s.accounting, tx)
	if err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: compatibility marker: %w", backend, err)
	}
	if compatibility != candidateControlRevisionMigrationVersion &&
		compatibility != serviceRuntimeSelectorCompatibilityMigrationVersion &&
		compatibility != serviceStateV3SnapshotCompatibilityMigrationVersion &&
		compatibility != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: unsupported compatibility marker %q", backend, compatibility)
	}
	if backend == ServiceRuntimeV2 {
		err = verifyServiceRuntimeV2Target(ctx, s.accounting, tx, request.Repository, request.Target)
	} else {
		err = verifyServiceRuntimeV3Target(ctx, s.accounting, tx, request.Repository, request.Target, true)
	}
	if err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: target: %w", backend, err)
	}

	next := ServiceRuntimeSelector{
		Schema: ServiceRuntimeSelectorSchema, Repository: request.Repository,
		Backend:                      backend,
		CatalogGenerationDigest:      request.Target.CatalogGenerationDigest,
		CatalogRootDigest:            request.Target.CatalogRootDigest,
		CatalogControlRevision:       request.Target.CatalogControlRevision,
		StateControlRevision:         request.Target.StateControlRevision,
		StateSummaryDigest:           request.Target.StateSummaryDigest,
		SearchGenerationDigest:       request.Target.SearchGenerationDigest,
		RelationshipGenerationDigest: request.Target.RelationshipGenerationDigest,
		RelationshipRootDigest:       request.Target.RelationshipRootDigest,
		ControlRevision:              request.ExpectedControlRevision + 1,
		ChangedAt:                    storeTimestamp(time.Now()),
	}
	next.Digest = serviceRuntimeSelectorDigest(next)
	if validateServiceRuntimeSelector(next) != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: %w", backend, ErrInvalidServiceRuntimeSelector)
	}

	if compatibility != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
		updated, updateErr := storeQuery[any](ctx, s.accounting, tx, `
UPDATE $rid SET version = $version RETURN NONE`, map[string]any{
			"rid":     candidateControlRevisionMigrationID(),
			"version": serviceCatalogV3SourceGenerationCompatibilityMigrationVersion,
		}, storeWrite(1))
		if updateErr != nil {
			return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: latch compatibility: %w", backend, updateErr)
		}
		for _, result := range *updated {
			if result.Error != nil {
				return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: latch compatibility: %s", backend, result.Error.Message)
			}
		}
	}
	if err := updateServiceRuntimeCurrentCatalogReference(ctx, s.accounting, tx, next); err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: catalog pin: %w", backend, err)
	}
	written, err := storeQuery[[]serviceRuntimeSelectorRec](ctx, s.accounting, tx, `
UPSERT $rid CONTENT $content RETURN AFTER`, map[string]any{
		"rid":     serviceRuntimeSelectorID(next.Repository),
		"content": serviceRuntimeSelectorContent(next),
	}, storeWrite(1))
	if err != nil {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: write: %w", backend, err)
	}
	writtenRows := firstDomainRows(written)
	if len(writtenRows) != 1 {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: write: %w", backend, ErrConflict)
	}
	stored, err := serviceRuntimeSelectorFromRec(writtenRows[0])
	if err != nil || stored != next {
		return ServiceRuntimeSelector{}, fmt.Errorf("select service runtime %s: write validation: %w", backend, ErrConflict)
	}
	return s.reconcileServiceRuntimeSelection(
		ctx, backend, next, storeCommit(ctx, s.accounting, tx),
	)
}

func (s *Surreal) reconcileServiceRuntimeSelection(
	ctx context.Context,
	backend string,
	next ServiceRuntimeSelector,
	commitErr error,
) (ServiceRuntimeSelector, error) {
	confirmCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), generationReconcileTimeout,
	)
	defer cancel()
	confirmed, confirmErr := s.GetServiceRuntimeSelector(confirmCtx, next.Repository)
	if confirmErr == nil && confirmed == next {
		return confirmed, nil
	}
	if commitErr != nil {
		if isRetryable(commitErr) {
			return ServiceRuntimeSelector{}, fmt.Errorf(
				"select service runtime %s: commit: %v: %w",
				backend, commitErr, errors.Join(confirmErr, ErrConflict),
			)
		}
		return ServiceRuntimeSelector{}, fmt.Errorf(
			"select service runtime %s: commit: %w",
			backend, errors.Join(commitErr, confirmErr),
		)
	}
	return ServiceRuntimeSelector{}, fmt.Errorf(
		"select service runtime %s: confirmation: %w",
		backend, errors.Join(confirmErr, ErrConflict),
	)
}

func verifyServiceRuntimeRepository(
	ctx context.Context,
	owner *storeCallOwner,
	tx *surrealdb.Transaction,
	repository string,
) error {
	results, err := storeQuery[[]struct {
		Name     string `json:"name"`
		Deleting bool   `json:"deleting"`
	}](ctx, owner, tx, "SELECT name, deleting FROM $rid", map[string]any{
		"rid": repoID(repository),
	}, storeRead())
	if err != nil {
		return err
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].Name != repository || rows[0].Deleting {
		return ErrConflict
	}
	return nil
}

func serviceRuntimeCompatibilityVersion(
	ctx context.Context,
	owner *storeCallOwner,
	db *surrealdb.Transaction,
) (string, error) {
	results, err := storeQuery[[]serviceRuntimeMigrationRec](
		ctx, owner, db, "SELECT version FROM $rid",
		map[string]any{"rid": candidateControlRevisionMigrationID()}, storeRead(),
	)
	if err != nil {
		return "", err
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || strings.TrimSpace(rows[0].Version) != rows[0].Version ||
		rows[0].Version == "" {
		return "", ErrInvalidServiceRuntimeSelector
	}
	return rows[0].Version, nil
}

func verifyServiceRuntimeV2Target(
	ctx context.Context,
	owner *storeCallOwner,
	tx *surrealdb.Transaction,
	repository string,
	target ServiceRuntimeTarget,
) error {
	catalogResults, err := storeQuery[[]serviceCatalogCurrentRec](
		ctx, owner, tx, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogCurrentID(repository)}, storeRead(),
	)
	if err != nil {
		return err
	}
	catalogRows := firstDomainRows(catalogResults)
	if len(catalogRows) != 1 || catalogRows[0].Repository != repository ||
		catalogRows[0].GenerationDigest != target.CatalogGenerationDigest ||
		catalogRows[0].ControlRevision != target.CatalogControlRevision ||
		catalogRows[0].PublishedAt.IsZero() {
		return ErrConflict
	}
	summaryResults, err := storeQuery[[]serviceRepositoryStateRec](
		ctx, owner, tx, "SELECT * FROM $rid",
		map[string]any{"rid": serviceRepositoryStateID(repository)}, storeRead(),
	)
	if err != nil {
		return err
	}
	summaryRows := firstDomainRows(summaryResults)
	if len(summaryRows) != 1 {
		return ErrConflict
	}
	summary := repositoryStateFromRec(summaryRows[0])
	if servicecatalog.ValidateRepositoryState(summary, true) != nil ||
		summary.Repository != repository ||
		summary.CatalogGeneration != target.CatalogGenerationDigest ||
		summary.CatalogControlRevision != target.CatalogControlRevision ||
		summary.ControlRevision != target.StateControlRevision ||
		summary.SummaryDigest != target.StateSummaryDigest {
		return ErrConflict
	}
	return verifyServiceRuntimeActiveSearch(
		ctx, owner, tx, "service_state_current", repository, target.SearchGenerationDigest,
	)
}

func verifyServiceRuntimeV3Target(
	ctx context.Context,
	owner *storeCallOwner,
	tx *surrealdb.Transaction,
	repository string,
	target ServiceRuntimeTarget,
	requireCurrentCandidate bool,
) error {
	if requireCurrentCandidate {
		candidateResults, err := storeQuery[[]serviceCatalogV3CandidateRec](
			ctx, owner, tx, "SELECT * FROM $rid",
			map[string]any{"rid": serviceCatalogV3CandidateID(repository)}, storeRead(),
		)
		if err != nil {
			return err
		}
		candidates := firstDomainRows(candidateResults)
		if len(candidates) != 1 ||
			!validServiceCatalogV3CandidateRecord(candidates[0], repository) ||
			candidates[0].RootDigest != target.CatalogRootDigest ||
			candidates[0].ControlRevision != target.CatalogControlRevision {
			return ErrConflict
		}
	}
	summary, err := getServiceStateV3SummarySnapshotTx(
		ctx, owner,
		tx,
		repository,
		target.StateControlRevision,
		target.StateSummaryDigest,
	)
	if err != nil {
		return err
	}
	if summary.Repository != repository ||
		summary.CatalogGeneration != target.CatalogRootDigest ||
		summary.CatalogControlRevision != target.CatalogControlRevision ||
		summary.ControlRevision != target.StateControlRevision ||
		summary.SummaryDigest != target.StateSummaryDigest {
		return ErrConflict
	}
	// The activation plan is restartable control and restore deletes it. The
	// precious summary plus the accepted-row search fence below are the durable
	// activation proof.
	reference := ServiceCatalogV3RelationshipReference{
		Repository:                   repository,
		RelationshipGenerationDigest: target.RelationshipGenerationDigest,
		RelationshipRootDigest:       target.RelationshipRootDigest,
		CatalogRootDigest:            target.CatalogRootDigest,
		CatalogControlRevision:       target.CatalogControlRevision,
		StateControlRevision:         target.StateControlRevision,
		StateSummaryDigest:           target.StateSummaryDigest,
	}
	referenceResults, err := storeQuery[[]serviceCatalogV3RelationshipReferenceRec](
		ctx, owner, tx, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogV3RelationshipReferenceID(target.RelationshipGenerationDigest)}, storeRead(),
	)
	if err != nil {
		return err
	}
	references := firstDomainRows(referenceResults)
	if len(references) != 1 || !equalServiceCatalogV3RelationshipReference(references[0], reference) {
		return ErrConflict
	}
	if err := verifyServiceRuntimeV3ActiveSearchSnapshot(
		ctx, owner,
		tx,
		summary,
		target.SearchGenerationDigest,
	); err != nil {
		return err
	}
	lifecycleResults, err := storeQuery[[]serviceCatalogV3LifecycleRec](
		ctx, owner, tx, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogV3LifecycleID(target.CatalogRootDigest)}, storeRead(),
	)
	if err != nil {
		return err
	}
	lifecycleRows := firstDomainRows(lifecycleResults)
	if len(lifecycleRows) != 1 || lifecycleRows[0].Repository != repository ||
		lifecycleRows[0].RootDigest != target.CatalogRootDigest ||
		lifecycleRows[0].State != serviceCatalogV3Historical {
		return ErrConflict
	}
	return nil
}

func getServiceStateV3SummarySnapshotTx(
	ctx context.Context,
	owner *storeCallOwner,
	tx *surrealdb.Transaction,
	repository string,
	snapshotRevision uint64,
	snapshotDigest string,
) (servicecatalog.RepositoryState, error) {
	currentResults, err := storeQuery[[]serviceRepositoryStateRec](
		ctx, owner,
		tx,
		"SELECT * FROM $rid",
		map[string]any{"rid": serviceStateV3RepositoryID(repository)}, storeRead(),
	)
	if err != nil {
		return servicecatalog.RepositoryState{}, err
	}
	currentRows := firstDomainRows(currentResults)
	if len(currentRows) > 1 {
		return servicecatalog.RepositoryState{}, ErrConflict
	}
	if len(currentRows) == 1 &&
		currentRows[0].ControlRevision == snapshotRevision &&
		currentRows[0].SummaryDigest == snapshotDigest {
		summary, summaryErr := serviceStateV3RepositoryFromRec(currentRows[0])
		if summaryErr != nil || summary.Repository != repository ||
			!validServiceCatalogV3RecordID(
				currentRows[0].RecID,
				"service_state_v3_repository",
				repository,
			) {
			return servicecatalog.RepositoryState{}, ErrConflict
		}
		return *summary, nil
	}
	preimageResults, err := storeQuery[[]serviceRepositoryStateRec](ctx, owner, tx, `
SELECT * FROM service_state_v3_repository_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest LIMIT 2`, map[string]any{
		"repository": repository, "snapshot_revision": snapshotRevision,
		"snapshot_digest": snapshotDigest,
	}, storeRead())
	if err != nil {
		return servicecatalog.RepositoryState{}, err
	}
	preimages := firstDomainRows(preimageResults)
	if len(preimages) != 1 ||
		!validServiceStateV3PreimageRecord(
			preimages[0].RecID,
			"service_state_v3_repository_preimage",
		) || preimages[0].Repository != repository ||
		preimages[0].SnapshotRevision != snapshotRevision ||
		preimages[0].SnapshotDigest != snapshotDigest ||
		preimages[0].ControlRevision != snapshotRevision ||
		preimages[0].SummaryDigest != snapshotDigest {
		return servicecatalog.RepositoryState{}, ErrConflict
	}
	summary, err := serviceStateV3RepositoryFromRec(preimages[0])
	if err != nil {
		return servicecatalog.RepositoryState{}, ErrConflict
	}
	return *summary, nil
}

func verifyServiceRuntimeV3ActiveSearchSnapshot(
	ctx context.Context,
	owner *storeCallOwner,
	tx *surrealdb.Transaction,
	summary servicecatalog.RepositoryState,
	searchGeneration string,
) error {
	const maxSnapshotRows = servicecatalogv3.MaxTotalServices * 2
	currentResults, err := storeQuery[[]serviceStateRec](ctx, owner, tx, `
SELECT * FROM service_state_v3_current
	WHERE repository = $repository AND visible_from <= $snapshot_revision
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository":        summary.Repository,
		"snapshot_revision": summary.ControlRevision,
		"limit":             maxSnapshotRows + 1,
	}, storeRead())
	if err != nil {
		return err
	}
	currentRows := firstDomainRows(currentResults)
	if len(currentRows) > maxSnapshotRows {
		return ErrConflict
	}
	preimageResults, err := storeQuery[[]serviceStateRec](ctx, owner, tx, `
SELECT * FROM service_state_v3_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository":        summary.Repository,
		"snapshot_revision": summary.ControlRevision,
		"snapshot_digest":   summary.SummaryDigest,
		"limit":             maxSnapshotRows + 1,
	}, storeRead())
	if err != nil {
		return err
	}
	preimageRows := firstDomainRows(preimageResults)
	if len(preimageRows) > maxSnapshotRows {
		return ErrConflict
	}
	states := make(map[string]servicecatalog.ServiceState, len(currentRows)+len(preimageRows))
	for _, record := range currentRows {
		state, stateErr := serviceStateV3FromRec(record)
		expected := serviceStateV3ID(summary.Repository, record.ServiceKey)
		expectedID, _ := expected.ID.(string)
		if stateErr != nil || state.Repository != summary.Repository ||
			record.VisibleFrom == 0 || record.VisibleFrom > summary.ControlRevision ||
			!validServiceCatalogV3RecordID(
				record.RecID,
				"service_state_v3_current",
				expectedID,
			) || states[state.ServiceKey].Repository != "" {
			return ErrConflict
		}
		states[state.ServiceKey] = *state
	}
	for _, record := range preimageRows {
		state, stateErr := decodeServiceStateV3Preimage(
			summary.Repository,
			record.ServiceKey,
			summary.ControlRevision,
			summary.SummaryDigest,
			record,
		)
		if stateErr != nil || states[state.ServiceKey].Repository != "" {
			return ErrConflict
		}
		states[state.ServiceKey] = state
	}
	counts := serviceStateV3Counts{}
	for _, state := range states {
		if state.Removed {
			counts.Tombstones++
			continue
		}
		counts.Live++
		switch state.Status {
		case servicecatalog.StatusCurrent:
			counts.Current++
		case servicecatalog.StatusStale:
			counts.Stale++
		case servicecatalog.StatusUnavailable:
			counts.Unavailable++
		case servicecatalog.StatusConflict:
			counts.Conflict++
		default:
			return ErrConflict
		}
		if state.Disposition == servicecatalog.DispositionAccepted &&
			(state.Status != servicecatalog.StatusCurrent ||
				state.ActiveSearchGeneration != searchGeneration) {
			return ErrConflict
		}
	}
	if !counts.matchesSummary(summary) {
		return ErrConflict
	}
	return nil
}

func verifyServiceRuntimeActiveSearch(
	ctx context.Context,
	owner *storeCallOwner,
	tx *surrealdb.Transaction,
	table string,
	repository string,
	searchGeneration string,
) error {
	statement := fmt.Sprintf(`SELECT id FROM %s
	WHERE repository = $repository AND removed = false
		AND disposition = $accepted
		AND (status != $current OR (active_search_generation ?? '') != $search)
	LIMIT 1`, table)
	results, err := storeQuery[[]struct {
		RecID *models.RecordID `json:"id"`
	}](ctx, owner, tx, statement, map[string]any{
		"repository": repository, "accepted": servicecatalog.DispositionAccepted,
		"current": servicecatalog.StatusCurrent, "search": searchGeneration,
	}, storeRead())
	if err != nil {
		return err
	}
	if len(firstDomainRows(results)) != 0 {
		return ErrConflict
	}
	return nil
}

func updateServiceRuntimeCurrentCatalogReference(
	ctx context.Context,
	owner *storeCallOwner,
	tx *surrealdb.Transaction,
	selector ServiceRuntimeSelector,
) error {
	rid := serviceRuntimeCurrentCatalogReferenceID(selector.Repository)
	results, err := storeQuery[[]serviceCatalogV3StateReferenceRec](ctx, owner, tx, `
SELECT * FROM service_catalog_v3_state_reference
	WHERE repository = $repository AND kind = 'current' LIMIT 2`, map[string]any{
		"repository": selector.Repository,
	}, storeRead())
	if err != nil {
		return err
	}
	rows := firstDomainRows(results)
	if len(rows) > 1 {
		return ErrConflict
	}
	if len(rows) == 1 {
		if rows[0].RecID == nil {
			return ErrConflict
		}
		identifier, ok := rows[0].RecID.ID.(string)
		wantedIdentifier, _ := rid.ID.(string)
		if rows[0].RecID.Table != rid.Table ||
			!ok || identifier != wantedIdentifier || rows[0].Repository != selector.Repository ||
			rows[0].Kind != "current" || rows[0].ServiceKey != "" ||
			rows[0].StateRootDigest != "" || !validSHA256Digest(rows[0].RootDigest) ||
			rows[0].RecordedAt.IsZero() {
			return ErrConflict
		}
	}
	if selector.Backend == ServiceRuntimeV2 {
		deleted, deleteErr := storeQuery[any](
			ctx, owner, tx, "DELETE $rid RETURN NONE", map[string]any{"rid": rid}, storeWrite(1),
		)
		if deleteErr != nil {
			return deleteErr
		}
		for _, result := range *deleted {
			if result.Error != nil {
				return errors.New(result.Error.Message)
			}
		}
		return nil
	}
	created, err := storeQuery[any](ctx, owner, tx, `
UPSERT $rid CONTENT {
	repository: $repository,
	root_digest: $root_digest,
	kind: 'current',
	service_key: '',
	state_root_digest: '',
	recorded_at: $recorded_at
} RETURN NONE`, map[string]any{
		"rid": rid, "repository": selector.Repository,
		"root_digest": selector.CatalogRootDigest, "recorded_at": selector.ChangedAt,
	}, storeWrite(1))
	if err != nil {
		return err
	}
	for _, result := range *created {
		if result.Error != nil {
			return errors.New(result.Error.Message)
		}
	}
	return nil
}

const serviceRuntimeSelectorSchema = `
DEFINE TABLE OVERWRITE service_runtime_selector SCHEMAFULL;
DEFINE FIELD OVERWRITE schema ON service_runtime_selector TYPE string;
DEFINE FIELD OVERWRITE repository ON service_runtime_selector TYPE string;
DEFINE FIELD OVERWRITE backend ON service_runtime_selector TYPE string ASSERT $value INSIDE ['v2', 'v3'];
DEFINE FIELD OVERWRITE catalog_generation_digest ON service_runtime_selector TYPE string;
DEFINE FIELD OVERWRITE catalog_root_digest ON service_runtime_selector TYPE string;
DEFINE FIELD OVERWRITE catalog_control_revision ON service_runtime_selector TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE state_control_revision ON service_runtime_selector TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE state_summary_digest ON service_runtime_selector TYPE string;
DEFINE FIELD OVERWRITE search_generation_digest ON service_runtime_selector TYPE string;
DEFINE FIELD OVERWRITE relationship_generation_digest ON service_runtime_selector TYPE string;
DEFINE FIELD OVERWRITE relationship_root_digest ON service_runtime_selector TYPE string;
DEFINE FIELD OVERWRITE control_revision ON service_runtime_selector TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE digest ON service_runtime_selector TYPE string;
DEFINE FIELD OVERWRITE changed_at ON service_runtime_selector TYPE datetime;
DEFINE INDEX OVERWRITE service_runtime_selector_repository ON service_runtime_selector FIELDS repository UNIQUE;
`

func (s *Surreal) migrateServiceRuntimeSelectorSchema(ctx context.Context) error {
	marker := serviceRuntimeSelectorSchemaMigrationID()
	markerResults, err := storeQuery[[]serviceRuntimeMigrationRec](
		ctx, s.accounting, s.db, "SELECT version FROM $rid", map[string]any{"rid": marker}, storeRead(),
	)
	if err != nil {
		return fmt.Errorf("migrate service runtime selector schema: marker: %w", err)
	}
	markerRows := firstDomainRows(markerResults)
	if len(markerRows) == 1 {
		if markerRows[0].Version == serviceRuntimeSelectorSchemaMigrationVersion {
			return nil
		}
		return fmt.Errorf("migrate service runtime selector schema: unsupported marker %q", markerRows[0].Version)
	}
	if len(markerRows) > 1 {
		return errors.New("migrate service runtime selector schema: duplicate marker")
	}
	preflight, err := storeQuery[any](ctx, s.accounting, s.db, `
DEFINE TABLE IF NOT EXISTS service_runtime_selector SCHEMALESS;`, nil, storeWrite(1))
	if err != nil {
		return fmt.Errorf("migrate service runtime selector schema: preflight schema: %w", err)
	}
	for index, result := range *preflight {
		if result.Error != nil {
			return fmt.Errorf("migrate service runtime selector schema preflight statement %d: %s", index, result.Error.Message)
		}
	}
	probe, err := storeQuery[[]struct {
		Count int `json:"count"`
	}](ctx, s.accounting, s.db, `RETURN [{ count: array::len(
		SELECT id FROM service_runtime_selector LIMIT 1
	) }];`, nil, storeRead())
	if err != nil {
		return fmt.Errorf("migrate service runtime selector schema: preflight: %w", err)
	}
	probeRows := firstDomainRows(probe)
	if len(probeRows) != 1 || probeRows[0].Count != 0 {
		return errors.New("migrate service runtime selector schema: unowned pre-migration rows")
	}
	if err := s.applySchemaBatch(ctx, serviceRuntimeSelectorSchema, "migrate service runtime selector schema "); err != nil {
		return fmt.Errorf("migrate service runtime selector schema: define: %w", err)
	}
	written, err := storeQuery[any](ctx, s.accounting, s.db, `
BEGIN;
LET $current = (SELECT version FROM $rid LIMIT 1)[0].version;
IF $current != NONE AND $current != $version {
	THROW 'phebs-permanent: unsupported service runtime selector schema migration'
};
UPSERT $rid SET
	version = IF $current = NONE THEN $version ELSE $current END,
	completed_at = IF $current = NONE THEN time::now() ELSE completed_at END
	RETURN NONE;
COMMIT;`, map[string]any{
		"rid": marker, "version": serviceRuntimeSelectorSchemaMigrationVersion,
	}, storeWrite(1))
	if err != nil {
		return fmt.Errorf("migrate service runtime selector schema: marker write: %w", err)
	}
	for index, result := range *written {
		if result.Error != nil {
			return fmt.Errorf("migrate service runtime selector schema marker statement %d: %s", index, result.Error.Message)
		}
	}
	return nil
}

func (s *Surreal) validateServiceRuntimeSelectorStore(ctx context.Context) error {
	selectors, err := s.ListServiceRuntimeSelectors(ctx)
	if err != nil {
		return err
	}
	markerResults, err := storeQuery[[]serviceRuntimeMigrationRec](
		ctx, s.accounting, s.db, "SELECT version FROM $rid", map[string]any{
			"rid": candidateControlRevisionMigrationID(),
		}, storeRead(),
	)
	if err != nil {
		return fmt.Errorf("validate service runtime selector store: compatibility marker: %w", err)
	}
	markerRows := firstDomainRows(markerResults)
	if len(markerRows) != 1 {
		return fmt.Errorf("validate service runtime selector store: %w", ErrInvalidServiceRuntimeSelector)
	}
	if markerRows[0].Version != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
		return fmt.Errorf("validate service runtime selector store: unsupported compatibility latch: %w", ErrInvalidServiceRuntimeSelector)
	}
	referenceResults, err := storeQuery[[]serviceCatalogV3StateReferenceRec](ctx, s.accounting, s.db, `
SELECT * FROM service_catalog_v3_state_reference
	WHERE kind = 'current' ORDER BY repository LIMIT $limit`, map[string]any{
		"limit": MaxServiceRuntimeSelectors + 1,
	}, storeRead())
	if err != nil {
		return fmt.Errorf("validate service runtime selector store: current references: %w", err)
	}
	references := firstDomainRows(referenceResults)
	if len(references) > MaxServiceRuntimeSelectors {
		return fmt.Errorf("validate service runtime selector store: current reference bound: %w", ErrInvalidServiceRuntimeSelector)
	}
	byRepository := make(map[string]serviceCatalogV3StateReferenceRec, len(references))
	for _, reference := range references {
		if _, duplicate := byRepository[reference.Repository]; duplicate {
			return fmt.Errorf("validate service runtime selector store: duplicate current reference: %w", ErrInvalidServiceRuntimeSelector)
		}
		byRepository[reference.Repository] = reference
	}
	for _, selector := range selectors {
		reference, present := byRepository[selector.Repository]
		if selector.Backend == ServiceRuntimeV2 {
			if present {
				return fmt.Errorf("validate service runtime selector store: v2 current reference: %w", ErrInvalidServiceRuntimeSelector)
			}
			continue
		}
		wantedID := serviceRuntimeCurrentCatalogReferenceID(selector.Repository)
		if reference.RecID == nil {
			return fmt.Errorf("validate service runtime selector store: v3 current reference: %w", ErrInvalidServiceRuntimeSelector)
		}
		identifier, ok := reference.RecID.ID.(string)
		wantedIdentifier, _ := wantedID.ID.(string)
		if !present || reference.RecID.Table != wantedID.Table ||
			!ok || identifier != wantedIdentifier || reference.RootDigest != selector.CatalogRootDigest ||
			reference.Kind != "current" || reference.ServiceKey != "" ||
			reference.StateRootDigest != "" || reference.RecordedAt.IsZero() {
			return fmt.Errorf("validate service runtime selector store: v3 current reference: %w", ErrInvalidServiceRuntimeSelector)
		}
		delete(byRepository, selector.Repository)
	}
	if len(byRepository) != 0 {
		return fmt.Errorf("validate service runtime selector store: orphan current reference: %w", ErrInvalidServiceRuntimeSelector)
	}
	return nil
}
