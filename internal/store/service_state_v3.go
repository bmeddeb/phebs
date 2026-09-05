package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

const (
	MaxServiceStateV3ChunkRows = 512
	// ServiceStateV3ActivationTransitionTargetOffset is the frozen T42 logical
	// transition unit. The exact transition reader performs five store reads:
	// selector, plan, schedule, this unit, and the selector confirmation.
	ServiceStateV3ActivationTransitionTargetOffset      int64  = 9
	ServiceStateV3ActivationTransitionStoreReadAttempts uint64 = 5

	ServiceStateV3ReconcileStage = "service-state-v3-reconcile"
	ServiceStateV3ActivateStage  = "service-state-v3-activate"

	serviceStateV3PlanSchema = "phebs-service-state-v3-plan"
	serviceStateV3Reconcile  = "reconcile"
	serviceStateV3Activate   = "activate"
	serviceStateV3Running    = "running"
	serviceStateV3Reconciled = "reconciled"
	serviceStateV3Activated  = "activated"
	serviceStateV3Failed     = "failed"
	serviceStateV3Superseded = "superseded"

	serviceStateV3PreimageBacklogMarker = "phebs-deferred: service state v3 preimage backlog"
)

var ErrInvalidServiceStateV3 = errors.New("invalid service state v3")

type ServiceStateV3Plan struct {
	Schema                 string
	Digest                 string
	Repository             string
	Phase                  string
	CatalogRoot            string
	CatalogControlRevision uint64
	SearchGeneration       string
	ScheduleDigest         string
	Repair                 int
	State                  string
	BaseChunk              int
	NextChunk              int
	TotalChunks            int
	ServiceMemberChunks    int
	RemovalChunks          int
	RemovalCursor          string
	CatalogServiceCount    int
	LiveServiceCount       int
	CurrentCount           int
	StaleCount             int
	UnavailableCount       int
	ConflictCount          int
	TombstoneCount         int
	SummaryControlRevision uint64
	SummaryDigest          string
	RowsRead               int64
	RowsWritten            int64
	BytesWritten           int64
	MaxChunkRows           int
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type ServiceStateV3Begin struct {
	Plan     *ServiceStateV3Plan
	Schedule *GenerationSchedule
	Noop     bool
}

type ServiceStateV3ChunkResult struct {
	Applied int
	Read    int
	// Settled means the durable plan is terminal. The generation scheduler
	// still owns this chunk lease and completes it after downstream handoff.
	Settled bool
}

// ServiceStateV3ActivationAuthority is the source-free identity of the
// selected T42 activation plan and its frozen ninth service-member unit.
type ServiceStateV3ActivationAuthority struct {
	PlanDigest     string
	ScheduleDigest string
	UnitDigest     string
}

type ServiceStateV3ActivationTransitionPoint string

const (
	ServiceStateV3ActivationTransitionHit       ServiceStateV3ActivationTransitionPoint = "hit"
	ServiceStateV3ActivationTransitionRecovered ServiceStateV3ActivationTransitionPoint = "recovered"
)

// ServiceStateV3ActivationTransitionRequest names one exact logical-transition
// snapshot. ExpectedSelector is the already-observed prior or final product
// authority; the reader refuses if current selection changed.
type ServiceStateV3ActivationTransitionRequest struct {
	Point            ServiceStateV3ActivationTransitionPoint
	ExpectedSelector ServiceRuntimeSelector
	PlanDigest       string
	ScheduleDigest   string
	UnitDigest       string
}

// ServiceStateV3ActivationTransition is the bounded source-free identity of a
// validated hit or recovered snapshot. Worker identity, lease token, raw rows,
// and timestamps stay inside the store.
type ServiceStateV3ActivationTransition struct {
	Point                  ServiceStateV3ActivationTransitionPoint
	SelectorDigest         string
	CatalogRootDigest      string
	SearchGenerationDigest string
	PlanDigest             string
	ScheduleDigest         string
	UnitDigest             string
}

type serviceStateV3PlanRec struct {
	Schema                 string           `json:"schema"`
	Digest                 string           `json:"digest"`
	Repository             string           `json:"repository"`
	Phase                  string           `json:"phase"`
	CatalogRoot            string           `json:"catalog_root"`
	CatalogControlRevision uint64           `json:"catalog_control_revision"`
	SearchGeneration       string           `json:"search_generation"`
	ScheduleDigest         string           `json:"schedule_digest"`
	Repair                 int              `json:"repair"`
	State                  string           `json:"state"`
	BaseChunk              int              `json:"base_chunk"`
	NextChunk              int              `json:"next_chunk"`
	TotalChunks            int              `json:"total_chunks"`
	ServiceMemberChunks    int              `json:"service_member_chunks"`
	RemovalChunks          int              `json:"removal_chunks"`
	RemovalCursor          string           `json:"removal_cursor"`
	CatalogServiceCount    int              `json:"catalog_service_count"`
	LiveServiceCount       int              `json:"live_service_count"`
	CurrentCount           int              `json:"current_count"`
	StaleCount             int              `json:"stale_count"`
	UnavailableCount       int              `json:"unavailable_count"`
	ConflictCount          int              `json:"conflict_count"`
	TombstoneCount         int              `json:"tombstone_count"`
	SummaryControlRevision uint64           `json:"summary_control_revision"`
	SummaryDigest          string           `json:"summary_digest"`
	RowsRead               int64            `json:"rows_read"`
	RowsWritten            int64            `json:"rows_written"`
	BytesWritten           int64            `json:"bytes_written"`
	MaxChunkRows           int              `json:"max_chunk_rows"`
	CreatedAt              time.Time        `json:"created_at"`
	UpdatedAt              time.Time        `json:"updated_at"`
	RecID                  *models.RecordID `json:"id"`
}

func (record serviceStateV3PlanRec) plan() ServiceStateV3Plan {
	return ServiceStateV3Plan{
		Schema: record.Schema, Digest: record.Digest, Repository: record.Repository,
		Phase: record.Phase, CatalogRoot: record.CatalogRoot,
		CatalogControlRevision: record.CatalogControlRevision,
		SearchGeneration:       record.SearchGeneration, ScheduleDigest: record.ScheduleDigest,
		Repair: record.Repair, State: record.State, BaseChunk: record.BaseChunk,
		NextChunk: record.NextChunk, TotalChunks: record.TotalChunks,
		ServiceMemberChunks: record.ServiceMemberChunks, RemovalChunks: record.RemovalChunks,
		RemovalCursor: record.RemovalCursor, CatalogServiceCount: record.CatalogServiceCount,
		LiveServiceCount: record.LiveServiceCount, CurrentCount: record.CurrentCount,
		StaleCount: record.StaleCount, UnavailableCount: record.UnavailableCount,
		ConflictCount: record.ConflictCount, TombstoneCount: record.TombstoneCount,
		SummaryControlRevision: record.SummaryControlRevision,
		SummaryDigest:          record.SummaryDigest, RowsRead: record.RowsRead,
		RowsWritten: record.RowsWritten, BytesWritten: record.BytesWritten,
		MaxChunkRows: record.MaxChunkRows, CreatedAt: record.CreatedAt.UTC(),
		UpdatedAt: record.UpdatedAt.UTC(),
	}
}

func serviceStateV3ID(repository, serviceKey string) models.RecordID {
	return models.NewRecordID(
		"service_state_v3_current",
		strings.TrimPrefix(generationSchedulerDigest(
			"phebs-service-state-v3-id", repository+"\x00"+serviceKey,
		), "sha256:"),
	)
}

func serviceStateV3RepositoryID(repository string) models.RecordID {
	return models.NewRecordID("service_state_v3_repository", repository)
}

func serviceStateV3PlanID(digest string) models.RecordID {
	return models.NewRecordID("service_state_v3_plan", strings.TrimPrefix(digest, "sha256:"))
}

func serviceStateV3PlanDigest(
	repository, phase, catalogRoot string,
	catalogRevision uint64,
	searchGeneration string,
	repair int,
) string {
	return generationSchedulerDigest(
		"phebs-service-state-v3-plan-v1",
		fmt.Sprintf(
			"%s\x00%s\x00%s\x00%d\x00%s\x00%d",
			repository, phase, catalogRoot, catalogRevision, searchGeneration, repair,
		),
	)
}

func serviceStateV3Stage(phase string) string {
	if phase == serviceStateV3Reconcile {
		return ServiceStateV3ReconcileStage
	}
	return ServiceStateV3ActivateStage
}

func validateServiceStateV3Plan(plan ServiceStateV3Plan) error {
	if plan.Schema != serviceStateV3PlanSchema ||
		validateCandidateRepository(plan.Repository) != nil ||
		!validSHA256Digest(plan.CatalogRoot) || plan.CatalogControlRevision == 0 ||
		!validSHA256Digest(plan.ScheduleDigest) || plan.Repair < 0 ||
		plan.BaseChunk < 0 || plan.NextChunk < plan.BaseChunk ||
		plan.TotalChunks < 1 || plan.NextChunk > plan.TotalChunks ||
		plan.ServiceMemberChunks < 0 ||
		plan.ServiceMemberChunks > servicecatalogv3.MaxMembers ||
		plan.RemovalChunks < 0 || plan.TotalChunks !=
		plan.ServiceMemberChunks+plan.RemovalChunks+1 ||
		plan.CatalogServiceCount < 0 ||
		plan.CatalogServiceCount > servicecatalogv3.MaxTotalServices ||
		plan.LiveServiceCount < 0 ||
		plan.LiveServiceCount > servicecatalogv3.MaxTotalServices*2 ||
		plan.CurrentCount < 0 || plan.StaleCount < 0 ||
		plan.UnavailableCount < 0 || plan.ConflictCount < 0 ||
		plan.LiveServiceCount != plan.CurrentCount+plan.StaleCount+
			plan.UnavailableCount+plan.ConflictCount ||
		plan.TombstoneCount < 0 || plan.SummaryControlRevision > 0 &&
		!validSHA256Digest(plan.SummaryDigest) ||
		plan.SummaryControlRevision == 0 && plan.SummaryDigest != "" ||
		plan.RowsRead < 0 || plan.RowsWritten < 0 || plan.BytesWritten < 0 ||
		plan.MaxChunkRows < 0 || plan.MaxChunkRows > MaxServiceStateV3ChunkRows ||
		plan.CreatedAt.IsZero() || plan.UpdatedAt.Before(plan.CreatedAt) {
		return ErrInvalidServiceStateV3
	}
	if plan.Digest != serviceStateV3PlanDigest(
		plan.Repository, plan.Phase, plan.CatalogRoot,
		plan.CatalogControlRevision, plan.SearchGeneration, plan.Repair,
	) {
		return ErrInvalidServiceStateV3
	}
	switch plan.Phase {
	case serviceStateV3Reconcile:
		if plan.SearchGeneration != "" {
			return ErrInvalidServiceStateV3
		}
	case serviceStateV3Activate:
		if !validSHA256Digest(plan.SearchGeneration) || plan.RemovalChunks != 0 {
			return ErrInvalidServiceStateV3
		}
	default:
		return ErrInvalidServiceStateV3
	}
	switch plan.State {
	case serviceStateV3Running:
		if plan.NextChunk >= plan.TotalChunks {
			return ErrInvalidServiceStateV3
		}
	case serviceStateV3Reconciled:
		if plan.Phase != serviceStateV3Reconcile || plan.NextChunk != plan.TotalChunks {
			return ErrInvalidServiceStateV3
		}
		if plan.LiveServiceCount > servicecatalogv3.MaxTotalServices {
			return ErrInvalidServiceStateV3
		}
	case serviceStateV3Activated:
		if plan.Phase != serviceStateV3Activate || plan.NextChunk != plan.TotalChunks {
			return ErrInvalidServiceStateV3
		}
	case serviceStateV3Failed, serviceStateV3Superseded:
	default:
		return ErrInvalidServiceStateV3
	}
	return nil
}

func serviceStateV3FromRec(row serviceStateRec) (*servicecatalog.ServiceState, error) {
	state := &servicecatalog.ServiceState{
		Schema: row.Schema, Repository: row.Repository, ServiceKey: row.ServiceKey,
		DisplayName: row.DisplayName, Disposition: row.Disposition, Origin: row.Origin,
		Reason: row.Reason, Successors: slices.Clone(row.Successors),
		Incarnation: row.Incarnation, DesiredGeneration: row.DesiredGeneration,
		DesiredSourceGeneration:  row.DesiredSourceGeneration,
		DesiredCatalogGeneration: row.DesiredCatalogGeneration,
		ActiveDesiredGeneration:  row.ActiveDesiredGeneration,
		ActiveSourceGeneration:   row.ActiveSourceGeneration,
		ActiveCatalogGeneration:  row.ActiveCatalogGeneration,
		ActiveSearchGeneration:   row.ActiveSearchGeneration,
		Status:                   row.Status, Removed: row.Removed, StateDigest: row.StateDigest,
		ControlRevision: row.ControlRevision, ChangedAt: row.ChangedAt.UTC(),
	}
	if err := servicecatalogv3.ValidateServiceState(*state, true); err != nil {
		return nil, err
	}
	return state, nil
}

func serviceStateV3RepositoryFromRec(
	row serviceRepositoryStateRec,
) (*servicecatalog.RepositoryState, error) {
	state := repositoryStateFromRec(row)
	if err := servicecatalogv3.ValidateRepositoryState(state, true); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *Surreal) getServiceStateV3Plan(
	ctx context.Context,
	digest string,
) (*ServiceStateV3Plan, error) {
	if !validSHA256Digest(digest) {
		return nil, ErrInvalidServiceStateV3
	}
	results, err := surrealdb.Query[[]serviceStateV3PlanRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceStateV3PlanID(digest)},
	)
	if err != nil {
		return nil, err
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) != 1 {
		return nil, ErrInvalidServiceStateV3
	}
	plan := rows[0].plan()
	identifier, _ := rows[0].RecID.ID.(string)
	if !validServiceCatalogV3RecordID(
		rows[0].RecID, "service_state_v3_plan", strings.TrimPrefix(digest, "sha256:"),
	) || identifier == "" || validateServiceStateV3Plan(plan) != nil {
		return nil, ErrInvalidServiceStateV3
	}
	return &plan, nil
}

// ReadServiceStateV3ActivationAuthority resolves the immutable activation
// proof behind one selected v3 runtime. The three bounded queries are charged
// separately; raw plan, schedule, chunk, worker, and timestamp fields do not
// cross this store boundary.
func (s *Surreal) ReadServiceStateV3ActivationAuthority(
	ctx context.Context,
	selector ServiceRuntimeSelector,
) (ServiceStateV3ActivationAuthority, error) {
	if ctx == nil || validateServiceRuntimeSelector(selector) != nil ||
		selector.Backend != ServiceRuntimeV3 {
		return ServiceStateV3ActivationAuthority{}, ErrInvalidServiceStateV3
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return ServiceStateV3ActivationAuthority{}, fmt.Errorf(
			"read service state v3 activation plan: %w", err,
		)
	}
	planResults, err := surrealdb.Query[[]serviceStateV3PlanRec](ctx, s.db, `
SELECT * FROM service_state_v3_plan
	WHERE repository = $repository AND phase = 'activate' AND state = 'activated'
		AND catalog_root = $catalog_root
		AND catalog_control_revision = $catalog_revision
		AND search_generation = $search
		AND summary_control_revision = $summary_revision
		AND summary_digest = $summary_digest
	ORDER BY repair LIMIT 2`, map[string]any{
		"repository": selector.Repository, "catalog_root": selector.CatalogRootDigest,
		"catalog_revision": selector.CatalogControlRevision,
		"search":           selector.SearchGenerationDigest,
		"summary_revision": selector.StateControlRevision,
		"summary_digest":   selector.StateSummaryDigest,
	})
	if err != nil {
		return ServiceStateV3ActivationAuthority{}, fmt.Errorf(
			"read service state v3 activation plan: %w", err,
		)
	}
	plans := firstDomainRows(planResults)
	if len(plans) != 1 {
		return ServiceStateV3ActivationAuthority{}, ErrInvalidServiceStateV3
	}
	plan := plans[0].plan()
	if validateServiceStateV3Plan(plan) != nil ||
		!validServiceCatalogV3RecordID(
			plans[0].RecID, "service_state_v3_plan", strings.TrimPrefix(plan.Digest, "sha256:"),
		) || plan.Repository != selector.Repository || plan.Phase != serviceStateV3Activate ||
		plan.State != serviceStateV3Activated || plan.CatalogRoot != selector.CatalogRootDigest ||
		plan.CatalogControlRevision != selector.CatalogControlRevision ||
		plan.SearchGeneration != selector.SearchGenerationDigest ||
		plan.SummaryControlRevision != selector.StateControlRevision ||
		plan.SummaryDigest != selector.StateSummaryDigest || plan.Repair != 0 ||
		plan.BaseChunk != 0 ||
		plan.ServiceMemberChunks <= int(ServiceStateV3ActivationTransitionTargetOffset) ||
		plan.NextChunk != plan.TotalChunks {
		return ServiceStateV3ActivationAuthority{}, ErrInvalidServiceStateV3
	}

	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return ServiceStateV3ActivationAuthority{}, fmt.Errorf(
			"read service state v3 activation schedule: %w", err,
		)
	}
	schedule, err := s.generationScheduleByDigest(ctx, plan.ScheduleDigest)
	if err != nil || schedule == nil || ValidateGenerationSchedule(*schedule) != nil ||
		schedule.Digest != plan.ScheduleDigest || schedule.Repository != selector.Repository ||
		schedule.Stage != ServiceStateV3ActivateStage || schedule.Generation != plan.Digest ||
		schedule.ResourceClass != GenerationResourceCPU || schedule.TotalItems != int64(plan.TotalChunks) ||
		schedule.ChunkItems != 1 || schedule.TotalChunks != plan.TotalChunks ||
		schedule.Status != GenerationScheduleSettled || schedule.Succeeded != schedule.TotalChunks ||
		schedule.Failed != 0 {
		return ServiceStateV3ActivationAuthority{}, errors.Join(err, ErrInvalidServiceStateV3)
	}

	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return ServiceStateV3ActivationAuthority{}, fmt.Errorf(
			"read service state v3 activation unit: %w", err,
		)
	}
	unitResults, err := surrealdb.Query[[]generationChunkRec](ctx, s.db, `
SELECT * FROM generation_schedule_chunk
	WHERE schedule_digest = $schedule AND repository = $repository
		AND stage = $stage AND generation = $generation AND offset = $offset
	ORDER BY attempt LIMIT 2`, map[string]any{
		"schedule": schedule.Digest, "repository": selector.Repository,
		"stage": ServiceStateV3ActivateStage, "generation": plan.Digest,
		"offset": ServiceStateV3ActivationTransitionTargetOffset,
	})
	if err != nil {
		return ServiceStateV3ActivationAuthority{}, fmt.Errorf(
			"read service state v3 activation unit: %w", err,
		)
	}
	units := generationChunkRows(unitResults)
	if len(units) != 1 {
		return ServiceStateV3ActivationAuthority{}, ErrInvalidServiceStateV3
	}
	unit, err := units[0].chunk()
	_, cleanCompletion, recoveredCompletion := serviceStateV3ActivationUnitShape(unit)
	if err != nil || unit.Identity != generationChunkID(
		schedule.Digest, ServiceStateV3ActivationTransitionTargetOffset, 0,
	) ||
		unit.ScheduleDigest != schedule.Digest || unit.Repository != selector.Repository ||
		unit.Stage != ServiceStateV3ActivateStage || unit.Generation != plan.Digest ||
		unit.ResourceClass != GenerationResourceCPU ||
		unit.Offset != ServiceStateV3ActivationTransitionTargetOffset || unit.Length != 1 ||
		unit.Attempt != 0 || (!cleanCompletion && !recoveredCompletion) {
		return ServiceStateV3ActivationAuthority{}, errors.Join(err, ErrInvalidServiceStateV3)
	}
	return ServiceStateV3ActivationAuthority{
		PlanDigest: plan.Digest, ScheduleDigest: schedule.Digest, UnitDigest: unit.Identity,
	}, nil
}

// ReadServiceStateV3ActivationTransition validates one exact snapshot at the
// committed ninth activation unit or after its schedule recovers. The five
// bounded queries are charged separately and never mutate scheduler state.
func (s *Surreal) ReadServiceStateV3ActivationTransition(
	ctx context.Context,
	request ServiceStateV3ActivationTransitionRequest,
) (ServiceStateV3ActivationTransition, error) {
	if ctx == nil || validateServiceRuntimeSelector(request.ExpectedSelector) != nil ||
		request.ExpectedSelector.Backend != ServiceRuntimeV3 ||
		!validSHA256Digest(request.PlanDigest) ||
		!validSHA256Digest(request.ScheduleDigest) ||
		!validSHA256Digest(request.UnitDigest) ||
		(request.Point != ServiceStateV3ActivationTransitionHit &&
			request.Point != ServiceStateV3ActivationTransitionRecovered) {
		return ServiceStateV3ActivationTransition{}, ErrInvalidServiceStateV3
	}
	selector, err := s.GetServiceRuntimeSelector(ctx, request.ExpectedSelector.Repository)
	if err != nil {
		return ServiceStateV3ActivationTransition{}, fmt.Errorf(
			"read service state v3 activation transition selector: %w", err,
		)
	}
	if selector != request.ExpectedSelector {
		return ServiceStateV3ActivationTransition{}, ErrInvalidServiceStateV3
	}

	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return ServiceStateV3ActivationTransition{}, fmt.Errorf(
			"read service state v3 activation transition plan: %w", err,
		)
	}
	plan, err := s.getServiceStateV3Plan(ctx, request.PlanDigest)
	if err != nil {
		return ServiceStateV3ActivationTransition{}, fmt.Errorf(
			"read service state v3 activation transition plan: %w", err,
		)
	}
	if plan.Digest != request.PlanDigest || plan.Repository != selector.Repository ||
		plan.Phase != serviceStateV3Activate || plan.Repair != 0 || plan.BaseChunk != 0 ||
		plan.ScheduleDigest != request.ScheduleDigest ||
		plan.SearchGeneration != selector.SearchGenerationDigest ||
		plan.ServiceMemberChunks <= int(ServiceStateV3ActivationTransitionTargetOffset) ||
		plan.TotalChunks <= int(ServiceStateV3ActivationTransitionTargetOffset)+1 {
		return ServiceStateV3ActivationTransition{}, ErrInvalidServiceStateV3
	}

	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return ServiceStateV3ActivationTransition{}, fmt.Errorf(
			"read service state v3 activation transition schedule: %w", err,
		)
	}
	schedule, err := s.generationScheduleByDigest(ctx, request.ScheduleDigest)
	if err != nil {
		return ServiceStateV3ActivationTransition{}, fmt.Errorf(
			"read service state v3 activation transition schedule: %w", err,
		)
	}
	if ValidateGenerationSchedule(*schedule) != nil || schedule.Digest != request.ScheduleDigest ||
		schedule.Repository != plan.Repository || schedule.Stage != ServiceStateV3ActivateStage ||
		schedule.Generation != plan.Digest || schedule.ResourceClass != GenerationResourceCPU ||
		schedule.TotalItems != int64(plan.TotalChunks) || schedule.ChunkItems != 1 ||
		schedule.TotalChunks != plan.TotalChunks || schedule.MaxAttempts != MaxGenerationAttempts ||
		schedule.RepositoryTokens != 1 || schedule.NextOffset != schedule.TotalItems ||
		schedule.Materialized != schedule.TotalChunks {
		return ServiceStateV3ActivationTransition{}, ErrInvalidServiceStateV3
	}

	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return ServiceStateV3ActivationTransition{}, fmt.Errorf(
			"read service state v3 activation transition unit: %w", err,
		)
	}
	unitResults, err := surrealdb.Query[[]generationChunkRec](ctx, s.db, `
SELECT * FROM generation_schedule_chunk
	WHERE schedule_digest = $schedule AND repository = $repository
		AND stage = $stage AND generation = $generation AND offset = $offset
	ORDER BY attempt LIMIT 2`, map[string]any{
		"schedule": schedule.Digest, "repository": plan.Repository,
		"stage": ServiceStateV3ActivateStage, "generation": plan.Digest,
		"offset": ServiceStateV3ActivationTransitionTargetOffset,
	})
	if err != nil {
		return ServiceStateV3ActivationTransition{}, fmt.Errorf(
			"read service state v3 activation transition unit: %w", err,
		)
	}
	units := generationChunkRows(unitResults)
	if len(units) != 1 {
		return ServiceStateV3ActivationTransition{}, ErrInvalidServiceStateV3
	}
	unit, err := units[0].chunk()
	if err != nil {
		return ServiceStateV3ActivationTransition{}, errors.Join(err, ErrInvalidServiceStateV3)
	}
	if unit.Identity != request.UnitDigest ||
		unit.Identity != generationChunkID(
			schedule.Digest, ServiceStateV3ActivationTransitionTargetOffset, 0,
		) || unit.ScheduleDigest != schedule.Digest || unit.Repository != plan.Repository ||
		unit.Stage != ServiceStateV3ActivateStage || unit.Generation != plan.Digest ||
		unit.ResourceClass != GenerationResourceCPU ||
		unit.Offset != ServiceStateV3ActivationTransitionTargetOffset || unit.Length != 1 ||
		unit.Attempt != 0 {
		return ServiceStateV3ActivationTransition{}, ErrInvalidServiceStateV3
	}

	targetNext := int(ServiceStateV3ActivationTransitionTargetOffset) + 1
	runningUnit, _, recoveredUnit := serviceStateV3ActivationUnitShape(unit)
	switch request.Point {
	case ServiceStateV3ActivationTransitionHit:
		if plan.State != serviceStateV3Running || plan.NextChunk != targetNext ||
			schedule.Status != GenerationScheduleActive || schedule.Pending != schedule.TotalChunks-targetNext ||
			schedule.Running != 1 || schedule.Succeeded != targetNext-1 || schedule.Failed != 0 ||
			!runningUnit {
			return ServiceStateV3ActivationTransition{}, ErrInvalidServiceStateV3
		}
	case ServiceStateV3ActivationTransitionRecovered:
		if plan.State != serviceStateV3Activated || plan.NextChunk != plan.TotalChunks ||
			plan.CatalogRoot != selector.CatalogRootDigest ||
			plan.CatalogControlRevision != selector.CatalogControlRevision ||
			plan.SummaryControlRevision != selector.StateControlRevision ||
			plan.SummaryDigest != selector.StateSummaryDigest ||
			schedule.Status != GenerationScheduleSettled || schedule.Pending != 0 ||
			schedule.Running != 0 || schedule.Succeeded != schedule.TotalChunks ||
			schedule.Failed != 0 || !recoveredUnit {
			return ServiceStateV3ActivationTransition{}, ErrInvalidServiceStateV3
		}
	}
	if err := s.ConfirmServiceRuntimeSelector(ctx, request.ExpectedSelector); err != nil {
		return ServiceStateV3ActivationTransition{}, fmt.Errorf(
			"confirm service state v3 activation transition selector: %w", err,
		)
	}
	return ServiceStateV3ActivationTransition{
		Point: request.Point, SelectorDigest: selector.Digest,
		CatalogRootDigest: plan.CatalogRoot, SearchGenerationDigest: plan.SearchGeneration,
		PlanDigest: plan.Digest, ScheduleDigest: schedule.Digest, UnitDigest: unit.Identity,
	}, nil
}

// serviceStateV3ActivationUnitShape accepts only row shapes written by claim,
// completion, and the one release/reclaim path used by the exact transition.
func serviceStateV3ActivationUnitShape(unit GenerationChunk) (running, clean, recovered bool) {
	validWorker := unit.ClaimedBy != "" && strings.TrimSpace(unit.ClaimedBy) == unit.ClaimedBy &&
		len(unit.ClaimedBy) <= MaxGenerationWorkerBytes && utf8.ValidString(unit.ClaimedBy)
	if !validWorker || unit.NotBefore != nil || unit.ClaimedAt == nil || unit.ClaimedAt.IsZero() {
		return false, false, false
	}
	if unit.Status == GenerationChunkRunning && validGenerationChunkLease(unit) == nil &&
		validServiceStateV3LeaseToken(unit.LeaseToken) && unit.Priority == GenerationPriorityNeverRun &&
		unit.HeartbeatAt != nil && !unit.HeartbeatAt.IsZero() &&
		!unit.HeartbeatAt.Before(*unit.ClaimedAt) && unit.FinishedAt == nil && unit.Error == "" {
		running = true
	}
	if unit.Status != GenerationChunkDone || unit.LeaseToken != "" || unit.HeartbeatAt != nil ||
		unit.FinishedAt == nil || unit.FinishedAt.IsZero() || unit.FinishedAt.Before(*unit.ClaimedAt) {
		return running, false, false
	}
	clean = unit.Priority == GenerationPriorityNeverRun && unit.Error == ""
	recovered = unit.Priority == GenerationPriorityStale && unit.Error != "" &&
		boundedGenerationError(unit.Error) == unit.Error
	return running, clean, recovered
}

func validServiceStateV3LeaseToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(token)
	return err == nil && hex.EncodeToString(decoded) == token
}

func (s *Surreal) getRawServiceStateV3Summary(
	ctx context.Context,
	repository string,
) (*servicecatalog.RepositoryState, error) {
	results, err := surrealdb.Query[[]serviceRepositoryStateRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceStateV3RepositoryID(repository)},
	)
	if err != nil {
		return nil, err
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) != 1 {
		return nil, ErrInvalidServiceStateV3
	}
	return serviceStateV3RepositoryFromRec(rows[0])
}

// GetServiceStateV3Summary is a dark store read. Product readers remain on
// v1/v2 until T41.6/T41.7 and cannot reach this namespace.
func (s *Surreal) GetServiceStateV3Summary(
	ctx context.Context,
	repository string,
) (*servicecatalog.RepositoryState, error) {
	root, err := s.GetServiceCatalogV3CandidateRoot(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("get service state v3 summary: catalog: %w", err)
	}
	summary, err := s.getRawServiceStateV3Summary(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("get service state v3 summary: %w", err)
	}
	if summary.CatalogGeneration != root.Root.Digest ||
		summary.CatalogControlRevision != root.ControlRevision {
		return nil, fmt.Errorf("get service state v3 summary: unreconciled: %w", ErrConflict)
	}
	return summary, nil
}

func (s *Surreal) currentServiceStateV3Plan(
	ctx context.Context,
	repository, phase string,
) (*ServiceStateV3Plan, *GenerationSchedule, error) {
	schedule, err := s.GetGenerationSchedule(ctx, repository, serviceStateV3Stage(phase))
	if errors.Is(err, ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	plan, err := s.getServiceStateV3Plan(ctx, schedule.Generation)
	if err != nil {
		return nil, nil, err
	}
	if plan.Repository != repository || plan.Phase != phase ||
		plan.ScheduleDigest != schedule.Digest {
		return nil, nil, ErrInvalidServiceStateV3
	}
	return plan, schedule, nil
}

func (s *Surreal) BeginServiceStateV3Reconcile(
	ctx context.Context,
	repository string,
) (ServiceStateV3Begin, error) {
	candidate, err := s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil {
		return ServiceStateV3Begin{}, fmt.Errorf("begin service state v3 reconcile: %w", err)
	}
	summary, summaryErr := s.getRawServiceStateV3Summary(ctx, repository)
	if summaryErr != nil && !errors.Is(summaryErr, ErrNotFound) {
		return ServiceStateV3Begin{}, fmt.Errorf("begin service state v3 reconcile: summary: %w", summaryErr)
	}
	if summary != nil && summary.CatalogGeneration == candidate.Generation.Root.Digest &&
		summary.CatalogControlRevision == candidate.ControlRevision {
		if err := s.retireTerminalServiceStateV3Schedule(
			ctx, repository, serviceStateV3Reconcile,
		); err != nil {
			return ServiceStateV3Begin{}, err
		}
		return ServiceStateV3Begin{Noop: true}, nil
	}
	if summary == nil {
		counts, countErr := s.serviceStateV3CountsForRepository(ctx, repository)
		if countErr != nil {
			return ServiceStateV3Begin{}, countErr
		}
		summary = &servicecatalog.RepositoryState{
			LiveServiceCount: counts.Live, CurrentCount: counts.Current,
			StaleCount: counts.Stale, UnavailableCount: counts.Unavailable,
			ConflictCount: counts.Conflict, TombstoneCount: counts.Tombstones,
		}
	}
	currentPlan, currentSchedule, err := s.currentServiceStateV3Plan(
		ctx, repository, serviceStateV3Reconcile,
	)
	if err != nil {
		return ServiceStateV3Begin{}, err
	}
	if currentPlan != nil && currentPlan.CatalogRoot != candidate.Generation.Root.Digest &&
		currentPlan.State == serviceStateV3Running {
		return ServiceStateV3Begin{}, fmt.Errorf(
			"begin service state v3 reconcile: unsettled successor fence: %w", ErrConflict,
		)
	}
	if currentPlan != nil && currentPlan.State == serviceStateV3Running &&
		currentSchedule.Status == GenerationScheduleActive {
		return ServiceStateV3Begin{Plan: currentPlan, Schedule: currentSchedule}, nil
	}
	return s.beginServiceStateV3Plan(
		ctx, candidate, summary, serviceStateV3Reconcile, "", currentPlan, currentSchedule,
	)
}

func (s *Surreal) BeginServiceStateV3Activation(
	ctx context.Context,
	repository, searchGeneration string,
) (ServiceStateV3Begin, error) {
	if !validSHA256Digest(searchGeneration) {
		return ServiceStateV3Begin{}, errors.New("begin service state v3 activation: search generation is invalid")
	}
	candidate, err := s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil {
		return ServiceStateV3Begin{}, fmt.Errorf("begin service state v3 activation: %w", err)
	}
	summary, err := s.getRawServiceStateV3Summary(ctx, repository)
	if err != nil || summary.CatalogGeneration != candidate.Generation.Root.Digest ||
		summary.CatalogControlRevision != candidate.ControlRevision {
		return ServiceStateV3Begin{}, fmt.Errorf("begin service state v3 activation: unreconciled: %w", ErrConflict)
	}
	completed, err := s.completedServiceStateV3Activation(
		ctx, repository, candidate.Generation.Root.Digest,
		candidate.ControlRevision, searchGeneration, *summary,
	)
	if err != nil {
		return ServiceStateV3Begin{}, err
	}
	if completed {
		if err := s.retireTerminalServiceStateV3Schedule(
			ctx, repository, serviceStateV3Activate,
		); err != nil {
			return ServiceStateV3Begin{}, err
		}
		return ServiceStateV3Begin{Noop: true}, nil
	}
	currentPlan, currentSchedule, err := s.currentServiceStateV3Plan(
		ctx, repository, serviceStateV3Activate,
	)
	if err != nil {
		return ServiceStateV3Begin{}, err
	}
	if currentPlan != nil && currentPlan.CatalogRoot == candidate.Generation.Root.Digest &&
		currentPlan.CatalogControlRevision == candidate.ControlRevision &&
		currentPlan.SearchGeneration == searchGeneration &&
		currentPlan.State == serviceStateV3Running &&
		currentSchedule.Status == GenerationScheduleActive {
		return ServiceStateV3Begin{Plan: currentPlan, Schedule: currentSchedule}, nil
	}
	return s.beginServiceStateV3Plan(
		ctx, candidate, summary, serviceStateV3Activate, searchGeneration,
		currentPlan, currentSchedule,
	)
}

func (s *Surreal) completedServiceStateV3Activation(
	ctx context.Context,
	repository, root string,
	revision uint64,
	searchGeneration string,
	summary servicecatalog.RepositoryState,
) (bool, error) {
	results, err := surrealdb.Query[[]serviceStateV3PlanRec](ctx, s.db, `
SELECT * FROM service_state_v3_plan
	WHERE repository = $repository AND phase = 'activate'
		AND catalog_root = $root AND catalog_control_revision = $revision
		AND search_generation = $search AND state = 'activated'
	ORDER BY updated_at DESC LIMIT 1`, map[string]any{
		"repository": repository, "root": root, "revision": revision,
		"search": searchGeneration,
	})
	if err != nil {
		return false, err
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return false, nil
	}
	plan := rows[0].plan()
	if len(rows) != 1 || validateServiceStateV3Plan(plan) != nil {
		return false, ErrInvalidServiceStateV3
	}
	return plan.SummaryControlRevision == summary.ControlRevision &&
		plan.SummaryDigest == summary.SummaryDigest, nil
}

func (s *Surreal) beginServiceStateV3Plan(
	ctx context.Context,
	candidate *ServiceCatalogV3Candidate,
	summary *servicecatalog.RepositoryState,
	phase, searchGeneration string,
	prior *ServiceStateV3Plan,
	priorSchedule *GenerationSchedule,
) (ServiceStateV3Begin, error) {
	root := candidate.Generation.Root
	plan := ServiceStateV3Plan{
		Schema: serviceStateV3PlanSchema, Repository: root.Binding.Repository,
		Phase: phase, CatalogRoot: root.Digest,
		CatalogControlRevision: candidate.ControlRevision,
		SearchGeneration:       searchGeneration,
		ServiceMemberChunks:    len(root.ServiceMembers),
		CatalogServiceCount:    root.Services, State: serviceStateV3Running,
	}
	if summary != nil {
		plan.LiveServiceCount = summary.LiveServiceCount
		plan.CurrentCount = summary.CurrentCount
		plan.StaleCount = summary.StaleCount
		plan.UnavailableCount = summary.UnavailableCount
		plan.ConflictCount = summary.ConflictCount
		plan.TombstoneCount = summary.TombstoneCount
		plan.SummaryControlRevision = summary.ControlRevision
		plan.SummaryDigest = summary.SummaryDigest
	}
	if phase == serviceStateV3Reconcile {
		priorLive := plan.LiveServiceCount
		plan.RemovalChunks = (priorLive + root.Services + MaxServiceStateV3ChunkRows - 1) /
			MaxServiceStateV3ChunkRows
	}
	plan.TotalChunks = plan.ServiceMemberChunks + plan.RemovalChunks + 1
	if prior != nil && prior.CatalogRoot == plan.CatalogRoot &&
		prior.CatalogControlRevision == plan.CatalogControlRevision &&
		prior.SearchGeneration == plan.SearchGeneration && prior.State == serviceStateV3Running &&
		priorSchedule != nil && priorSchedule.Status == GenerationScheduleSettled &&
		priorSchedule.Failed > 0 {
		if prior.Repair >= MaxGenerationAttempts {
			return ServiceStateV3Begin{}, ErrGenerationExhausted
		}
		plan = *prior
		plan.Repair++
		plan.BaseChunk = prior.NextChunk
		plan.ScheduleDigest = ""
		plan.CreatedAt = time.Time{}
		plan.UpdatedAt = time.Time{}
	}
	var removalProof *serviceStateV3RemovalProof
	if phase == serviceStateV3Reconcile && plan.Repair == 0 {
		var proofErr error
		removalProof, proofErr = s.proveServiceStateV3NoRemovals(ctx, candidate, summary)
		if proofErr != nil {
			return ServiceStateV3Begin{}, proofErr
		}
		if removalProof != nil {
			plan.RemovalChunks = 0
			plan.TotalChunks = plan.ServiceMemberChunks + 1
		}
	}
	remaining := plan.TotalChunks - plan.BaseChunk
	if remaining < 1 {
		return ServiceStateV3Begin{}, ErrInvalidServiceStateV3
	}
	var (
		schedule *GenerationSchedule
		err      error
	)
	for {
		plan.Digest = serviceStateV3PlanDigest(
			plan.Repository, plan.Phase, plan.CatalogRoot,
			plan.CatalogControlRevision, plan.SearchGeneration, plan.Repair,
		)
		schedule, err = s.EnqueueGenerationSchedule(ctx, GenerationScheduleSpec{
			Repository: plan.Repository, Stage: serviceStateV3Stage(plan.Phase),
			Generation: plan.Digest, ResourceClass: GenerationResourceCPU,
			TotalItems: int64(remaining), ChunkItems: 1,
			MaxAttempts: MaxGenerationAttempts, RepositoryTokens: 1,
		})
		if !errors.Is(err, ErrGenerationStale) {
			break
		}
		if plan.Repair >= MaxGenerationAttempts {
			return ServiceStateV3Begin{}, ErrGenerationExhausted
		}
		plan.Repair++
	}
	if err != nil {
		return ServiceStateV3Begin{}, fmt.Errorf("begin service state v3 plan: enqueue: %w", err)
	}
	plan.ScheduleDigest = schedule.Digest
	now := storeTimestamp(time.Now())
	plan.CreatedAt, plan.UpdatedAt = now, now
	if err := s.createServiceStateV3Plan(ctx, plan, prior, removalProof); err != nil {
		existing, readErr := s.getServiceStateV3Plan(ctx, plan.Digest)
		if readErr == nil && existing.ScheduleDigest == schedule.Digest &&
			existing.Repository == plan.Repository && existing.Phase == plan.Phase &&
			existing.CatalogRoot == plan.CatalogRoot &&
			existing.CatalogControlRevision == plan.CatalogControlRevision &&
			existing.SearchGeneration == plan.SearchGeneration &&
			existing.ServiceMemberChunks == plan.ServiceMemberChunks &&
			existing.RemovalChunks == plan.RemovalChunks &&
			existing.TotalChunks == plan.TotalChunks && existing.BaseChunk == plan.BaseChunk &&
			existing.Repair == plan.Repair &&
			existing.State == serviceStateV3Running {
			return ServiceStateV3Begin{Plan: existing, Schedule: schedule}, nil
		}
		_ = s.RetireCurrentGenerationSchedule(ctx, *schedule)
		return ServiceStateV3Begin{}, fmt.Errorf("begin service state v3 plan: %w", err)
	}
	return ServiceStateV3Begin{Plan: &plan, Schedule: schedule}, nil
}

func (s *Surreal) retireTerminalServiceStateV3Schedule(
	ctx context.Context,
	repository, phase string,
) error {
	plan, schedule, err := s.currentServiceStateV3Plan(ctx, repository, phase)
	if err != nil || plan == nil {
		return err
	}
	terminal := plan.State == serviceStateV3Reconciled ||
		plan.State == serviceStateV3Activated || plan.State == serviceStateV3Superseded
	if !terminal || schedule.Status != GenerationScheduleSettled {
		return nil
	}
	if err := s.RetireCurrentGenerationSchedule(ctx, *schedule); err != nil &&
		!errors.Is(err, ErrGenerationStale) {
		return err
	}
	return nil
}

// This is an ephemeral proof for a new schedule, never a persisted plan mode.
// Repairs keep their already-admitted removal layout and cumulative counters.
type serviceStateV3RemovalProof struct {
	repository      string
	catalogRoot     string
	catalogRevision uint64
	summary         servicecatalog.RepositoryState
	liveKeys        []string
}

type serviceStateV3LiveRec struct {
	RecID           *models.RecordID `json:"id"`
	ServiceKey      string           `json:"service_key"`
	Status          string           `json:"status"`
	Removed         bool             `json:"removed"`
	ControlRevision uint64           `json:"control_revision"`
	VisibleFrom     uint64           `json:"visible_from"`
}

type serviceStateV3CatalogKeys struct {
	all  []string
	live []string
}

func (keys *serviceStateV3CatalogKeys) appendMember(
	ctx context.Context,
	root servicecatalogv3.Root,
	descriptor servicecatalogv3.MemberDescriptor,
	raw []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	projections, err := servicecatalogv3.ProjectServiceMember(root, descriptor, raw)
	if err != nil {
		return err
	}
	for _, projection := range projections {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := projection.Service.Key
		if len(keys.all) >= servicecatalogv3.MaxTotalServices ||
			len(keys.all) != 0 && key <= keys.all[len(keys.all)-1] {
			return ErrInvalidServiceStateV3
		}
		keys.all = append(keys.all, key)
		if !projection.Removed {
			keys.live = append(keys.live, key)
		}
	}
	return nil
}

func serviceStateV3CandidateKeys(
	ctx context.Context,
	generation servicecatalogv3.Generation,
) (serviceStateV3CatalogKeys, error) {
	keys := serviceStateV3CatalogKeys{all: []string{}, live: []string{}}
	if ctx == nil {
		return keys, ErrInvalidServiceStateV3
	}
	if err := ctx.Err(); err != nil {
		return keys, err
	}
	if err := servicecatalogv3.ValidateRoot(generation.Root); err != nil {
		return keys, err
	}
	ordinal := 0
	for _, member := range generation.Members {
		if err := ctx.Err(); err != nil {
			return keys, err
		}
		if member.Kind != "service" {
			continue
		}
		if ordinal >= len(generation.Root.ServiceMembers) || member.Ordinal != ordinal {
			return keys, ErrInvalidServiceStateV3
		}
		if err := keys.appendMember(ctx, generation.Root,
			generation.Root.ServiceMembers[ordinal], member.Content); err != nil {
			return keys, err
		}
		ordinal++
	}
	if ordinal != len(generation.Root.ServiceMembers) || len(keys.all) != generation.Root.Services {
		return keys, ErrInvalidServiceStateV3
	}
	return keys, nil
}

func (s *Surreal) proveServiceStateV3NoRemovals(
	ctx context.Context,
	candidate *ServiceCatalogV3Candidate,
	summary *servicecatalog.RepositoryState,
) (*serviceStateV3RemovalProof, error) {
	keys, err := serviceStateV3CandidateKeys(ctx, candidate.Generation)
	if err != nil {
		return nil, err
	}
	// ponytail: the existing (repository, service_key) index can traverse all
	// retained tombstones. The limit bounds results, not physical scan work;
	// review a live-key index if profiling warrants a history-independent scan.
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return nil, err
	}
	results, err := surrealdb.Query[[]serviceStateV3LiveRec](ctx, s.db, `
SELECT id, service_key, status, removed, control_revision, visible_from
	FROM service_state_v3_current WHERE repository = $repository AND removed = false
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository": candidate.Generation.Root.Binding.Repository,
		"limit":      servicecatalogv3.MaxTotalServices*2 + 1,
	})
	if err != nil {
		return nil, err
	}
	liveKeys, counts, err := serviceStateV3LiveCensus(
		ctx, candidate.Generation.Root.Binding.Repository, firstDomainRows(results),
	)
	if err != nil {
		return nil, err
	}
	if summary == nil || counts.Live != summary.LiveServiceCount ||
		counts.Current != summary.CurrentCount || counts.Stale != summary.StaleCount ||
		counts.Unavailable != summary.UnavailableCount || counts.Conflict != summary.ConflictCount {
		return nil, ErrConflict
	}
	for _, key := range liveKeys {
		if _, present := slices.BinarySearch(keys.all, key); !present {
			return nil, nil // A real removal needs the existing conservative scan.
		}
	}
	return &serviceStateV3RemovalProof{
		repository:  candidate.Generation.Root.Binding.Repository,
		catalogRoot: candidate.Generation.Root.Digest, catalogRevision: candidate.ControlRevision,
		// Tombstones are inherited under the exact summary CAS, not recounted.
		summary: *summary, liveKeys: liveKeys,
	}, nil
}

// Only the closed projection is validated here. Normal member processing still
// validates full state rows and their digests; this does not fetch successors.
func serviceStateV3LiveCensus(
	ctx context.Context,
	repository string,
	rows []serviceStateV3LiveRec,
) ([]string, serviceStateV3Counts, error) {
	counts := serviceStateV3Counts{}
	if ctx == nil || len(rows) > servicecatalogv3.MaxTotalServices*2 {
		return nil, counts, ErrInvalidServiceStateV3
	}
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, counts, err
		}
		key := row.ServiceKey
		if len(key) == 0 || len(key) > servicecatalog.MaxServiceKeyBytes ||
			len(keys) != 0 && key <= keys[len(keys)-1] || row.Removed ||
			row.ControlRevision == 0 || row.VisibleFrom == 0 {
			return nil, counts, ErrInvalidServiceStateV3
		}
		for index, value := range []byte(key) {
			if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
				value >= '0' && value <= '9' || index != 0 && (value == '.' || value == '_' || value == '-') {
				continue
			}
			return nil, counts, ErrInvalidServiceStateV3
		}
		expected := serviceStateV3ID(repository, key)
		identifier, _ := expected.ID.(string)
		if !validServiceCatalogV3RecordID(row.RecID, "service_state_v3_current", identifier) {
			return nil, counts, ErrInvalidServiceStateV3
		}
		counts.Live++
		switch row.Status {
		case servicecatalog.StatusCurrent:
			counts.Current++
		case servicecatalog.StatusStale:
			counts.Stale++
		case servicecatalog.StatusUnavailable:
			counts.Unavailable++
		case servicecatalog.StatusConflict:
			counts.Conflict++
		default:
			return nil, counts, ErrInvalidServiceStateV3
		}
		keys = append(keys, key)
	}
	return keys, counts, ctx.Err()
}

func (s *Surreal) serviceStateV3RemovalDrainKeys(
	ctx context.Context,
	plan ServiceStateV3Plan,
) ([]string, error) {
	root, err := s.ReadServiceCatalogV3Root(ctx, plan.Repository, plan.CatalogRoot)
	if err != nil {
		return nil, err
	}
	if root.Services != plan.CatalogServiceCount || len(root.ServiceMembers) != plan.ServiceMemberChunks {
		return nil, ErrInvalidServiceStateV3
	}
	keys := serviceStateV3CatalogKeys{all: []string{}, live: []string{}}
	for _, descriptor := range root.ServiceMembers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := s.serviceCatalogV3MemberContent(ctx, descriptor)
		if err != nil {
			return nil, err
		}
		if err := keys.appendMember(ctx, root, descriptor, raw); err != nil {
			return nil, err
		}
	}
	if len(keys.all) != root.Services {
		return nil, ErrInvalidServiceStateV3
	}
	return keys.live, ctx.Err()
}

func (s *Surreal) createServiceStateV3Plan(
	ctx context.Context,
	plan ServiceStateV3Plan,
	prior *ServiceStateV3Plan,
	removalProof *serviceStateV3RemovalProof,
) error {
	if validateServiceStateV3Plan(plan) != nil {
		return ErrInvalidServiceStateV3
	}
	priorRID := models.NewRecordID("service_state_v3_plan", "absent")
	priorDigest := ""
	var priorContent map[string]any
	if prior != nil {
		priorRID = serviceStateV3PlanID(prior.Digest)
		priorDigest = prior.Digest
		priorContent = serviceStateV3PlanContent(*prior)
	}
	var summaryContent map[string]any
	liveKeys := []string{}
	if removalProof != nil {
		if plan.Phase != serviceStateV3Reconcile || plan.RemovalChunks != 0 ||
			removalProof.repository != plan.Repository || removalProof.catalogRoot != plan.CatalogRoot ||
			removalProof.catalogRevision != plan.CatalogControlRevision || removalProof.liveKeys == nil {
			return ErrInvalidServiceStateV3
		}
		liveKeys = removalProof.liveKeys
		if removalProof.summary.ControlRevision != 0 {
			summaryContent = serviceRepositoryStateContent(removalProof.summary)
		}
	}
	results, err := surrealdb.Query[[]serviceStateV3PlanRec](ctx, s.db, `
BEGIN;
LET $candidate = (SELECT root_digest, control_revision FROM $candidate_rid LIMIT 1)[0];
LET $current = (SELECT schedule_digest FROM $schedule_current LIMIT 1)[0].schedule_digest;
LET $schedule = (SELECT digest, generation, status FROM $schedule_rid LIMIT 1)[0];
LET $existing = (SELECT id FROM $plan_rid LIMIT 1)[0].id;
IF $check_removal_proof {
	LET $prior = (SELECT * OMIT id FROM $prior_rid LIMIT 1)[0];
	LET $summary = (SELECT * OMIT id FROM $summary_rid LIMIT 1)[0];
	LET $prior_ok = IF $prior_present THEN $prior = $expected_prior ELSE $prior = NONE END;
	LET $summary_ok = IF $summary_present THEN $summary = $expected_summary ELSE $summary = NONE END;
	LET $live_keys = SELECT VALUE service_key FROM service_state_v3_current
		WHERE repository = $repository AND removed = false ORDER BY service_key LIMIT $live_limit;
	IF !$prior_ok OR !$summary_ok OR $live_keys != $expected_live_keys {
		THROW 'phebs-permanent: service state v3 removal proof changed';
	};
};
IF $candidate = NONE OR $candidate.root_digest != $catalog_root OR
	$candidate.control_revision != $catalog_revision OR $current != $schedule_digest OR
	$schedule = NONE OR $schedule.digest != $schedule_digest OR
	$schedule.generation != $digest OR $schedule.status != 'active' OR $existing != NONE {
	THROW 'phebs-permanent: service state v3 plan fence changed';
};
IF $prior_digest != '' {
	UPDATE $prior_rid SET state = 'superseded', updated_at = time::now()
		WHERE digest = $prior_digest AND state = 'running' RETURN NONE;
};
CREATE $plan_rid CONTENT $content RETURN AFTER;
COMMIT;`, map[string]any{
		"candidate_rid": serviceCatalogV3CandidateID(plan.Repository),
		"schedule_current": models.NewRecordID(
			"generation_schedule_current",
			strings.TrimPrefix(generationCurrentID(plan.Repository, serviceStateV3Stage(plan.Phase)), "sha256:"),
		),
		"schedule_rid": models.NewRecordID(
			"generation_schedule", strings.TrimPrefix(plan.ScheduleDigest, "sha256:"),
		),
		"plan_rid":  serviceStateV3PlanID(plan.Digest),
		"prior_rid": priorRID, "prior_digest": priorDigest,
		"prior_present": prior != nil, "expected_prior": priorContent,
		"check_removal_proof": removalProof != nil,
		"summary_rid":         serviceStateV3RepositoryID(plan.Repository),
		"summary_present":     summaryContent != nil, "expected_summary": summaryContent,
		"repository": plan.Repository, "expected_live_keys": liveKeys,
		"live_limit":       servicecatalogv3.MaxTotalServices*2 + 1,
		"catalog_root":     plan.CatalogRoot,
		"catalog_revision": plan.CatalogControlRevision,
		"schedule_digest":  plan.ScheduleDigest, "digest": plan.Digest,
		"content": serviceStateV3PlanContent(plan),
	})
	if err != nil {
		return err
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return ErrConflict
	}
	created := rows[0].plan()
	if validateServiceStateV3Plan(created) != nil || created.Digest != plan.Digest {
		return ErrInvalidServiceStateV3
	}
	return nil
}

func serviceStateV3PlanContent(plan ServiceStateV3Plan) map[string]any {
	return map[string]any{
		"schema": plan.Schema, "digest": plan.Digest,
		"repository": plan.Repository, "phase": plan.Phase,
		"catalog_root":             plan.CatalogRoot,
		"catalog_control_revision": plan.CatalogControlRevision,
		"search_generation":        plan.SearchGeneration,
		"schedule_digest":          plan.ScheduleDigest, "repair": plan.Repair,
		"state": plan.State, "base_chunk": plan.BaseChunk,
		"next_chunk": plan.NextChunk, "total_chunks": plan.TotalChunks,
		"service_member_chunks": plan.ServiceMemberChunks,
		"removal_chunks":        plan.RemovalChunks, "removal_cursor": plan.RemovalCursor,
		"catalog_service_count": plan.CatalogServiceCount,
		"live_service_count":    plan.LiveServiceCount,
		"current_count":         plan.CurrentCount, "stale_count": plan.StaleCount,
		"unavailable_count": plan.UnavailableCount,
		"conflict_count":    plan.ConflictCount, "tombstone_count": plan.TombstoneCount,
		"summary_control_revision": plan.SummaryControlRevision,
		"summary_digest":           plan.SummaryDigest, "rows_read": plan.RowsRead,
		"rows_written": plan.RowsWritten, "bytes_written": plan.BytesWritten,
		"max_chunk_rows": plan.MaxChunkRows,
		"created_at":     plan.CreatedAt, "updated_at": plan.UpdatedAt,
	}
}

// ProcessServiceStateV3Chunk applies one exact leased scheduler chunk. The
// scheduler owns completion so any post-apply runtime handoff failure can
// retry the same idempotent plan chunk before settling its lease.
func (s *Surreal) ProcessServiceStateV3Chunk(
	ctx context.Context,
	chunk GenerationChunk,
) (ServiceStateV3ChunkResult, error) {
	if err := validGenerationChunkLease(chunk); err != nil ||
		chunk.ResourceClass != GenerationResourceCPU || chunk.Length != 1 ||
		(chunk.Stage != ServiceStateV3ReconcileStage &&
			chunk.Stage != ServiceStateV3ActivateStage) {
		return ServiceStateV3ChunkResult{}, fmt.Errorf(
			"process service state v3 chunk: invalid lease: %w", ErrGenerationLeaseLost,
		)
	}
	plan, err := s.getServiceStateV3Plan(ctx, chunk.Generation)
	if err != nil {
		return ServiceStateV3ChunkResult{}, fmt.Errorf("process service state v3 chunk: plan: %w", err)
	}
	logicalChunk := plan.BaseChunk + int(chunk.Offset)
	if chunk.ScheduleDigest != plan.ScheduleDigest || chunk.Repository != plan.Repository ||
		chunk.Stage != serviceStateV3Stage(plan.Phase) || logicalChunk >= plan.TotalChunks {
		return ServiceStateV3ChunkResult{}, fmt.Errorf("process service state v3 chunk: plan fence: %w", ErrGenerationStale)
	}
	result := ServiceStateV3ChunkResult{}
	if logicalChunk == plan.NextChunk {
		root, rootErr := s.GetServiceCatalogV3CandidateRoot(ctx, plan.Repository)
		if rootErr != nil || root.Root.Digest != plan.CatalogRoot ||
			root.ControlRevision != plan.CatalogControlRevision {
			return result, fmt.Errorf("process service state v3 chunk: catalog fence: %w", ErrConflict)
		}
		if plan.Phase == serviceStateV3Reconcile {
			result, err = s.processServiceStateV3ReconcileChunk(
				ctx, chunk, *plan, *root, logicalChunk,
			)
		} else {
			result, err = s.processServiceStateV3ActivationChunk(
				ctx, chunk, *plan, *root, logicalChunk,
			)
		}
		if err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
	} else if logicalChunk > plan.NextChunk {
		return result, fmt.Errorf("process service state v3 chunk: out of order: %w", ErrConflict)
	} else if plan.NextChunk == plan.TotalChunks &&
		(plan.State == serviceStateV3Reconciled || plan.State == serviceStateV3Activated) {
		result.Settled = true
	}
	return result, nil
}

func (s *Surreal) processServiceStateV3ReconcileChunk(
	ctx context.Context,
	chunk GenerationChunk,
	plan ServiceStateV3Plan,
	opened ServiceCatalogV3CandidateRoot,
	logicalChunk int,
) (ServiceStateV3ChunkResult, error) {
	if logicalChunk < plan.ServiceMemberChunks {
		descriptor := opened.Root.ServiceMembers[logicalChunk]
		raw, err := s.serviceCatalogV3MemberContent(ctx, descriptor)
		if err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		projections, err := servicecatalogv3.ProjectServiceMember(
			opened.Root, descriptor, raw,
		)
		if err != nil || len(projections) > MaxServiceStateV3ChunkRows {
			return ServiceStateV3ChunkResult{}, ErrInvalidServiceStateV3
		}
		existing, err := s.serviceStateV3RowsForProjections(
			ctx, plan.Repository, projections,
		)
		if err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		changes := make([]serviceStateV3Change, 0, len(projections))
		projectedPlan := plan
		now := storeTimestamp(time.Now())
		for _, projection := range projections {
			prior, exists := existing[projection.Service.Key]
			next, changed, projectErr := projectServiceStateV3(
				projection, prior, exists, now,
			)
			if projectErr != nil {
				return ServiceStateV3ChunkResult{}, projectErr
			}
			if err := applyServiceStateV3CountDelta(&projectedPlan, prior, exists, next); err != nil {
				return ServiceStateV3ChunkResult{}, err
			}
			if changed {
				update := serviceStateUpdate{State: next}
				if exists {
					update.ExpectedRevision = prior.ControlRevision
					update.ExpectedDigest = prior.StateDigest
				}
				changes = append(changes, serviceStateV3Change{
					update: update, prior: prior, existed: exists,
				})
			}
		}
		if err := s.commitServiceStateV3Changes(
			ctx, chunk, plan, changes, len(projections), plan.RemovalCursor, nil,
		); err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		return ServiceStateV3ChunkResult{
			Applied: len(changes), Read: len(projections),
		}, nil
	}
	removalEnd := plan.ServiceMemberChunks + plan.RemovalChunks
	if logicalChunk < removalEnd {
		rows, err := s.nextServiceStateV3RemovalRows(ctx, plan)
		if err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		changes := make([]serviceStateV3Change, 0, len(rows))
		removalCursor := plan.RemovalCursor
		now := storeTimestamp(time.Now())
		present, err := s.serviceStateV3PresentKeys(ctx, opened.Root, rows)
		if err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		for _, prior := range rows {
			removalCursor = prior.ServiceKey
			if present[prior.ServiceKey] {
				continue
			}
			next, removalErr := implicitRemovedServiceStateV3(prior, now)
			if removalErr != nil {
				return ServiceStateV3ChunkResult{}, removalErr
			}
			changes = append(changes, serviceStateV3Change{
				update: serviceStateUpdate{
					State: next, ExpectedRevision: prior.ControlRevision,
					ExpectedDigest: prior.StateDigest,
				},
				prior: prior, existed: true,
			})
		}
		if err := s.commitServiceStateV3Changes(
			ctx, chunk, plan, changes, len(rows), removalCursor, nil,
		); err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		return ServiceStateV3ChunkResult{Applied: len(changes), Read: len(rows)}, nil
	}
	if logicalChunk != plan.TotalChunks-1 {
		return ServiceStateV3ChunkResult{}, ErrInvalidServiceStateV3
	}
	now := storeTimestamp(time.Now())
	nextSummary := servicecatalog.RepositoryState{
		Schema:     servicecatalogv3.RepositoryStateSchema,
		Repository: plan.Repository, CatalogGeneration: plan.CatalogRoot,
		CatalogControlRevision: plan.CatalogControlRevision,
		CatalogServiceCount:    opened.Root.Services,
		LiveServiceCount:       plan.LiveServiceCount, CurrentCount: plan.CurrentCount,
		StaleCount: plan.StaleCount, UnavailableCount: plan.UnavailableCount,
		ConflictCount: plan.ConflictCount, TombstoneCount: plan.TombstoneCount,
		ControlRevision: plan.SummaryControlRevision + 1, UpdatedAt: now,
	}
	if nextSummary.ControlRevision == 0 {
		nextSummary.ControlRevision = 1
	}
	if err := servicecatalogv3.SetRepositoryStateDigest(&nextSummary); err != nil {
		return ServiceStateV3ChunkResult{}, err
	}
	if err := servicecatalogv3.ValidateRepositoryState(nextSummary, true); err != nil {
		return ServiceStateV3ChunkResult{}, err
	}
	nextPlan := plan
	nextPlan.NextChunk++
	nextPlan.State = serviceStateV3Reconciled
	nextPlan.SummaryControlRevision = nextSummary.ControlRevision
	nextPlan.SummaryDigest = nextSummary.SummaryDigest
	nextPlan.UpdatedAt = now
	if err := s.commitServiceStateV3Chunk(
		ctx, chunk, plan, nextPlan, nil, nil, &nextSummary, true,
	); err != nil {
		return ServiceStateV3ChunkResult{}, err
	}
	return ServiceStateV3ChunkResult{Settled: true}, nil
}

func (s *Surreal) processServiceStateV3ActivationChunk(
	ctx context.Context,
	chunk GenerationChunk,
	plan ServiceStateV3Plan,
	opened ServiceCatalogV3CandidateRoot,
	logicalChunk int,
) (ServiceStateV3ChunkResult, error) {
	if logicalChunk == plan.TotalChunks-1 {
		summary, err := s.getRawServiceStateV3Summary(ctx, plan.Repository)
		if err != nil || summary.ControlRevision != plan.SummaryControlRevision ||
			summary.SummaryDigest != plan.SummaryDigest {
			return ServiceStateV3ChunkResult{}, fmt.Errorf("activate service state v3 final summary: %w", ErrConflict)
		}
		nextPlan := plan
		nextPlan.NextChunk++
		nextPlan.State = serviceStateV3Activated
		nextPlan.UpdatedAt = storeTimestamp(time.Now())
		if err := s.commitServiceStateV3Chunk(
			ctx, chunk, plan, nextPlan, nil, summary, nil, false,
		); err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		return ServiceStateV3ChunkResult{Settled: true}, nil
	}
	if logicalChunk >= plan.ServiceMemberChunks {
		return ServiceStateV3ChunkResult{}, ErrInvalidServiceStateV3
	}
	descriptor := opened.Root.ServiceMembers[logicalChunk]
	raw, err := s.serviceCatalogV3MemberContent(ctx, descriptor)
	if err != nil {
		return ServiceStateV3ChunkResult{}, err
	}
	projections, err := servicecatalogv3.ProjectServiceMember(opened.Root, descriptor, raw)
	if err != nil || len(projections) > MaxServiceStateV3ChunkRows {
		return ServiceStateV3ChunkResult{}, ErrInvalidServiceStateV3
	}
	existing, err := s.serviceStateV3RowsForProjections(ctx, plan.Repository, projections)
	if err != nil {
		return ServiceStateV3ChunkResult{}, err
	}
	summary, err := s.getRawServiceStateV3Summary(ctx, plan.Repository)
	if err != nil || summary.CatalogGeneration != plan.CatalogRoot ||
		summary.CatalogControlRevision != plan.CatalogControlRevision ||
		summary.ControlRevision != plan.SummaryControlRevision ||
		summary.SummaryDigest != plan.SummaryDigest {
		return ServiceStateV3ChunkResult{}, fmt.Errorf("activate service state v3 summary: %w", ErrConflict)
	}
	changes := make([]serviceStateV3Change, 0, len(projections))
	now := storeTimestamp(time.Now())
	for _, projection := range projections {
		state, found := existing[projection.Service.Key]
		if !found {
			return ServiceStateV3ChunkResult{}, ErrInvalidServiceStateV3
		}
		if err := verifyServiceStateV3Projection(state, projection); err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		if projection.Removed ||
			projection.Service.Disposition != servicecatalog.DispositionAccepted {
			continue
		}
		if state.Status == servicecatalog.StatusCurrent &&
			state.ActiveDesiredGeneration == state.DesiredGeneration &&
			state.ActiveSourceGeneration == state.DesiredSourceGeneration &&
			state.ActiveCatalogGeneration == state.DesiredCatalogGeneration &&
			state.ActiveSearchGeneration == plan.SearchGeneration {
			continue
		}
		if state.Status != servicecatalog.StatusCurrent &&
			state.Status != servicecatalog.StatusStale &&
			state.Status != servicecatalog.StatusUnavailable {
			return ServiceStateV3ChunkResult{}, ErrInvalidServiceStateV3
		}
		next := state
		next.Successors = slices.Clone(state.Successors)
		next.ActiveDesiredGeneration = state.DesiredGeneration
		next.ActiveSourceGeneration = state.DesiredSourceGeneration
		next.ActiveCatalogGeneration = state.DesiredCatalogGeneration
		next.ActiveSearchGeneration = plan.SearchGeneration
		next.Status = servicecatalog.StatusCurrent
		next.ControlRevision++
		next.ChangedAt = now
		if err := servicecatalogv3.SetServiceStateDigest(&next); err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		changes = append(changes, serviceStateV3Change{
			update: serviceStateUpdate{
				State: next, ExpectedRevision: state.ControlRevision,
				ExpectedDigest: state.StateDigest,
			},
			prior: state, existed: true,
		})
	}
	if err := s.commitServiceStateV3Changes(
		ctx, chunk, plan, changes, len(projections), plan.RemovalCursor, summary,
	); err != nil {
		return ServiceStateV3ChunkResult{}, err
	}
	return ServiceStateV3ChunkResult{Applied: len(changes), Read: len(projections)}, nil
}

func (s *Surreal) serviceCatalogV3MemberContent(
	ctx context.Context,
	descriptor servicecatalogv3.MemberDescriptor,
) ([]byte, error) {
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return nil, fmt.Errorf("service catalog v3 member content: %w", err)
	}
	results, err := surrealdb.Query[[]serviceCatalogV3MemberRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogV3MemberID(descriptor.Digest)},
	)
	if err != nil {
		return nil, err
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].MemberDigest != descriptor.Digest ||
		rows[0].Kind != descriptor.Kind || rows[0].Ordinal != descriptor.Ordinal ||
		rows[0].ContentBytes != descriptor.ContentBytes ||
		len(rows[0].Content) != descriptor.ContentBytes {
		return nil, ErrInvalidServiceCatalogV3Candidate
	}
	return []byte(rows[0].Content), nil
}

func (s *Surreal) serviceStateV3RowsForProjections(
	ctx context.Context,
	repository string,
	projections []servicecatalog.ServiceProjection,
) (map[string]servicecatalog.ServiceState, error) {
	if len(projections) > MaxServiceStateV3ChunkRows {
		return nil, ErrInvalidServiceStateV3
	}
	rids := make([]models.RecordID, len(projections))
	wanted := make(map[string]struct{}, len(projections))
	for index, projection := range projections {
		if projection.Repository != repository {
			return nil, ErrInvalidServiceStateV3
		}
		rids[index] = serviceStateV3ID(repository, projection.Service.Key)
		wanted[projection.Service.Key] = struct{}{}
	}
	if len(rids) == 0 {
		return map[string]servicecatalog.ServiceState{}, nil
	}
	results, err := surrealdb.Query[[]serviceStateRec](
		ctx, s.db, "SELECT * FROM $rids", map[string]any{"rids": rids},
	)
	if err != nil {
		return nil, err
	}
	rows := firstDomainRows(results)
	if len(rows) > len(projections) {
		return nil, ErrInvalidServiceStateV3
	}
	states := make(map[string]servicecatalog.ServiceState, len(rows))
	for _, row := range rows {
		state, stateErr := serviceStateV3FromRec(row)
		if stateErr != nil || state.Repository != repository {
			return nil, ErrInvalidServiceStateV3
		}
		if _, ok := wanted[state.ServiceKey]; !ok {
			return nil, ErrInvalidServiceStateV3
		}
		if _, duplicate := states[state.ServiceKey]; duplicate {
			return nil, ErrInvalidServiceStateV3
		}
		states[state.ServiceKey] = *state
	}
	return states, nil
}

func (s *Surreal) nextServiceStateV3RemovalRows(
	ctx context.Context,
	plan ServiceStateV3Plan,
) ([]servicecatalog.ServiceState, error) {
	results, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_current
	WHERE repository = $repository AND removed = false AND service_key > $after
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository": plan.Repository, "after": plan.RemovalCursor,
		"limit": MaxServiceStateV3ChunkRows + 1,
	})
	if err != nil {
		return nil, err
	}
	rows := firstDomainRows(results)
	if len(rows) > MaxServiceStateV3ChunkRows {
		rows = rows[:MaxServiceStateV3ChunkRows]
	}
	states := make([]servicecatalog.ServiceState, 0, len(rows))
	priorKey := plan.RemovalCursor
	for _, row := range rows {
		state, stateErr := serviceStateV3FromRec(row)
		if stateErr != nil || state.Repository != plan.Repository || state.Removed ||
			state.ServiceKey <= priorKey {
			return nil, ErrInvalidServiceStateV3
		}
		priorKey = state.ServiceKey
		states = append(states, *state)
	}
	return states, nil
}

func (s *Surreal) serviceStateV3PresentKeys(
	ctx context.Context,
	root servicecatalogv3.Root,
	rows []servicecatalog.ServiceState,
) (map[string]bool, error) {
	memberIndexes := make(map[int]struct{})
	for _, state := range rows {
		index := sort.Search(len(root.ServiceMembers), func(index int) bool {
			return root.ServiceMembers[index].Last >= state.ServiceKey
		})
		if index < len(root.ServiceMembers) &&
			root.ServiceMembers[index].First <= state.ServiceKey {
			memberIndexes[index] = struct{}{}
		}
	}
	present := make(map[string]bool, len(rows))
	for index := range memberIndexes {
		descriptor := root.ServiceMembers[index]
		raw, err := s.serviceCatalogV3MemberContent(ctx, descriptor)
		if err != nil {
			return nil, err
		}
		projections, err := servicecatalogv3.ProjectServiceMember(root, descriptor, raw)
		if err != nil {
			return nil, err
		}
		for _, projection := range projections {
			present[projection.Service.Key] = true
		}
	}
	return present, nil
}

func projectServiceStateV3(
	projection servicecatalog.ServiceProjection,
	prior servicecatalog.ServiceState,
	exists bool,
	now time.Time,
) (servicecatalog.ServiceState, bool, error) {
	incarnation := uint64(1)
	if exists {
		incarnation = prior.Incarnation
		if prior.Removed && !projection.Removed {
			incarnation++
			if incarnation == 0 {
				return servicecatalog.ServiceState{}, false, ErrInvalidServiceStateV3
			}
		}
	}
	desired, err := servicecatalogv3.ServiceDesiredGeneration(projection, incarnation)
	if err != nil {
		return servicecatalog.ServiceState{}, false, err
	}
	if exists && prior.DesiredGeneration == desired &&
		prior.Removed == projection.Removed {
		return prior, false, nil
	}
	next := servicecatalog.ServiceState{
		Schema:     servicecatalogv3.ServiceStateSchema,
		Repository: projection.Repository, ServiceKey: projection.Service.Key,
		DisplayName: projection.Service.DisplayName,
		Disposition: projection.Service.Disposition, Origin: projection.Service.Origin,
		Reason:      projection.Service.Reason,
		Successors:  slices.Clone(projection.Service.Successors),
		Incarnation: incarnation, DesiredGeneration: desired,
		DesiredSourceGeneration:  projection.SourceGeneration,
		DesiredCatalogGeneration: projection.CatalogGeneration,
		Removed:                  projection.Removed, ControlRevision: 1, ChangedAt: now,
	}
	if exists {
		next.ControlRevision = prior.ControlRevision + 1
		if !prior.Removed || projection.Removed {
			next.ActiveDesiredGeneration = prior.ActiveDesiredGeneration
			next.ActiveSourceGeneration = prior.ActiveSourceGeneration
			next.ActiveCatalogGeneration = prior.ActiveCatalogGeneration
			next.ActiveSearchGeneration = prior.ActiveSearchGeneration
		}
	}
	switch projection.Service.Disposition {
	case servicecatalog.DispositionRejected:
		next.Status = servicecatalog.StatusRemoved
	case servicecatalog.DispositionConflict:
		next.Status = servicecatalog.StatusConflict
	case servicecatalog.DispositionProposal:
		next.Status = servicecatalog.StatusUnavailable
	case servicecatalog.DispositionAccepted:
		switch {
		case next.ActiveDesiredGeneration == next.DesiredGeneration &&
			next.ActiveSourceGeneration == next.DesiredSourceGeneration:
			next.Status = servicecatalog.StatusCurrent
		case next.ActiveDesiredGeneration != "":
			next.Status = servicecatalog.StatusStale
		default:
			next.Status = servicecatalog.StatusUnavailable
		}
	default:
		return servicecatalog.ServiceState{}, false, ErrInvalidServiceStateV3
	}
	if err := servicecatalogv3.SetServiceStateDigest(&next); err != nil {
		return servicecatalog.ServiceState{}, false, err
	}
	if err := servicecatalogv3.ValidateServiceState(next, true); err != nil {
		return servicecatalog.ServiceState{}, false, err
	}
	return next, true, nil
}

func implicitRemovedServiceStateV3(
	prior servicecatalog.ServiceState,
	now time.Time,
) (servicecatalog.ServiceState, error) {
	next := prior
	next.Successors = []string{}
	next.Disposition = servicecatalog.DispositionRejected
	next.Reason = servicecatalog.ImplicitRemovalReason
	next.DesiredGeneration = ""
	next.DesiredSourceGeneration = ""
	next.DesiredCatalogGeneration = ""
	next.Status = servicecatalog.StatusRemoved
	next.Removed = true
	next.ControlRevision++
	next.ChangedAt = now
	if err := servicecatalogv3.SetServiceStateDigest(&next); err != nil {
		return servicecatalog.ServiceState{}, err
	}
	if err := servicecatalogv3.ValidateServiceState(next, true); err != nil {
		return servicecatalog.ServiceState{}, err
	}
	return next, nil
}

func verifyServiceStateV3Projection(
	state servicecatalog.ServiceState,
	projection servicecatalog.ServiceProjection,
) error {
	desired, err := servicecatalogv3.ServiceDesiredGeneration(projection, state.Incarnation)
	if err != nil || state.Repository != projection.Repository ||
		state.ServiceKey != projection.Service.Key || state.DesiredGeneration != desired ||
		state.DesiredSourceGeneration != projection.SourceGeneration ||
		state.Removed != projection.Removed {
		return ErrInvalidServiceStateV3
	}
	return nil
}

func applyServiceStateV3CountDelta(
	plan *ServiceStateV3Plan,
	prior servicecatalog.ServiceState,
	exists bool,
	next servicecatalog.ServiceState,
) error {
	if exists && !prior.Removed {
		if err := addServiceStateV3Status(plan, prior.Status, -1); err != nil {
			return err
		}
		plan.LiveServiceCount--
	}
	if !next.Removed {
		if err := addServiceStateV3Status(plan, next.Status, 1); err != nil {
			return err
		}
		plan.LiveServiceCount++
	}
	plan.TombstoneCount += tombstoneDelta(prior, exists, next)
	if plan.LiveServiceCount < 0 || plan.TombstoneCount < 0 {
		return ErrInvalidServiceStateV3
	}
	return nil
}

func addServiceStateV3Status(
	plan *ServiceStateV3Plan,
	status string,
	delta int,
) error {
	switch status {
	case servicecatalog.StatusCurrent:
		plan.CurrentCount += delta
	case servicecatalog.StatusStale:
		plan.StaleCount += delta
	case servicecatalog.StatusUnavailable:
		plan.UnavailableCount += delta
	case servicecatalog.StatusConflict:
		plan.ConflictCount += delta
	default:
		return ErrInvalidServiceStateV3
	}
	if plan.CurrentCount < 0 || plan.StaleCount < 0 ||
		plan.UnavailableCount < 0 || plan.ConflictCount < 0 {
		return ErrInvalidServiceStateV3
	}
	return nil
}

func advanceServiceStateV3Metrics(
	plan *ServiceStateV3Plan,
	read int,
	updates []serviceStateUpdate,
) {
	plan.RowsRead += int64(read)
	plan.RowsWritten += int64(len(updates))
	plan.MaxChunkRows = max(plan.MaxChunkRows, read)
	for _, update := range updates {
		raw, _ := json.Marshal(update.State)
		plan.BytesWritten += int64(len(raw))
	}
	plan.UpdatedAt = storeTimestamp(time.Now())
}

type serviceStateV3Change struct {
	update  serviceStateUpdate
	prior   servicecatalog.ServiceState
	existed bool
}

type serviceStateV3ChunkWrite struct {
	plan    ServiceStateV3Plan
	updates []serviceStateUpdate
	summary *servicecatalog.RepositoryState
}

// nextServiceStateV3ChunkWrite reserves payload records for the plan and, during
// activation, its matching summary. Member packing and the durable ordinal stay
// unchanged: only the final prefix completes the member. Already-written states
// are skipped by the ordinary projection/current-state checks after a retry.
func nextServiceStateV3ChunkWrite(
	plan ServiceStateV3Plan,
	changes []serviceStateV3Change,
	read int,
	removalCursor string,
	summary *servicecatalog.RepositoryState,
) (serviceStateV3ChunkWrite, error) {
	if validateServiceStateV3Plan(plan) != nil || len(changes) > read ||
		read < 0 || read > MaxServiceStateV3ChunkRows ||
		plan.SummaryControlRevision >= math.MaxInt64 ||
		(plan.Phase == serviceStateV3Activate) != (summary != nil) {
		return serviceStateV3ChunkWrite{}, ErrInvalidServiceStateV3
	}
	limit := MaxServiceStateV3ChunkRows - 1
	if summary != nil {
		if summary.Repository != plan.Repository ||
			summary.ControlRevision != plan.SummaryControlRevision ||
			summary.SummaryDigest != plan.SummaryDigest {
			return serviceStateV3ChunkWrite{}, ErrInvalidServiceStateV3
		}
		limit--
	}
	count := min(len(changes), limit)
	write := serviceStateV3ChunkWrite{
		plan: plan, updates: make([]serviceStateUpdate, 0, count),
	}
	for _, change := range changes[:count] {
		if change.update.State.Repository != plan.Repository ||
			servicecatalogv3.ValidateServiceState(change.update.State, true) != nil {
			return serviceStateV3ChunkWrite{}, ErrInvalidServiceStateV3
		}
		if err := applyServiceStateV3CountDelta(
			&write.plan, change.prior, change.existed, change.update.State,
		); err != nil {
			return serviceStateV3ChunkWrite{}, err
		}
		write.updates = append(write.updates, change.update)
	}
	completedRead := 0
	if count == len(changes) {
		write.plan.NextChunk++
		write.plan.RemovalCursor = removalCursor
		completedRead = read
	}
	// These durable summaries describe committed writes and completed-member
	// reads, not failed/retried read attempts or phase-wide work accounting.
	advanceServiceStateV3Metrics(&write.plan, completedRead, write.updates)
	if summary != nil && count != 0 {
		nextSummary := *summary
		nextSummary.LiveServiceCount = write.plan.LiveServiceCount
		nextSummary.CurrentCount = write.plan.CurrentCount
		nextSummary.StaleCount = write.plan.StaleCount
		nextSummary.UnavailableCount = write.plan.UnavailableCount
		nextSummary.ConflictCount = write.plan.ConflictCount
		nextSummary.TombstoneCount = write.plan.TombstoneCount
		nextSummary.ControlRevision++
		nextSummary.UpdatedAt = write.plan.UpdatedAt
		if err := servicecatalogv3.SetRepositoryStateDigest(&nextSummary); err != nil {
			return serviceStateV3ChunkWrite{}, err
		}
		if err := servicecatalogv3.ValidateRepositoryState(nextSummary, true); err != nil {
			return serviceStateV3ChunkWrite{}, err
		}
		write.plan.SummaryControlRevision = nextSummary.ControlRevision
		write.plan.SummaryDigest = nextSummary.SummaryDigest
		write.summary = &nextSummary
	}
	if err := validateServiceStateV3Plan(write.plan); err != nil {
		return serviceStateV3ChunkWrite{}, err
	}
	return write, nil
}

func (s *Surreal) commitServiceStateV3Changes(
	ctx context.Context,
	chunk GenerationChunk,
	plan ServiceStateV3Plan,
	changes []serviceStateV3Change,
	read int,
	removalCursor string,
	summary *servicecatalog.RepositoryState,
) error {
	// Validate every prefix before submitting the first one, just as the old
	// single transaction validated every state and final summary before I/O.
	// Store/lease/cancellation failure can still leave a valid committed prefix.
	var writes [2]serviceStateV3ChunkWrite
	writeCount := 0
	projectedPlan, projectedSummary := plan, summary
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if writeCount == len(writes) {
			return ErrInvalidServiceStateV3
		}
		write, err := nextServiceStateV3ChunkWrite(
			projectedPlan, changes, read, removalCursor, projectedSummary,
		)
		if err != nil {
			return err
		}
		writes[writeCount] = write
		writeCount++
		if len(write.updates) == len(changes) {
			break
		}
		changes = changes[len(write.updates):]
		projectedPlan = write.plan
		if write.summary != nil {
			projectedSummary = write.summary
		}
	}
	for _, write := range writes[:writeCount] {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.commitServiceStateV3Chunk(
			ctx, chunk, plan, write.plan, write.updates, summary, write.summary, false,
		); err != nil {
			return err
		}
		plan = write.plan
		if write.summary != nil {
			summary = write.summary
		}
	}
	return nil
}

func (s *Surreal) commitServiceStateV3Chunk(
	ctx context.Context,
	chunk GenerationChunk,
	priorPlan, nextPlan ServiceStateV3Plan,
	updates []serviceStateUpdate,
	expectedSummary *servicecatalog.RepositoryState,
	nextSummary *servicecatalog.RepositoryState,
	requireRemovalDrain bool,
) error {
	payloadRecords := len(updates) + 1 // The plan is submitted even for a no-op.
	if nextSummary != nil {
		payloadRecords++
	}
	if validateServiceStateV3Plan(priorPlan) != nil ||
		validateServiceStateV3Plan(nextPlan) != nil ||
		priorPlan.Digest != nextPlan.Digest ||
		priorPlan.ScheduleDigest != nextPlan.ScheduleDigest ||
		payloadRecords > MaxServiceStateV3ChunkRows {
		return ErrInvalidServiceStateV3
	}
	if expectedSummary != nil &&
		(expectedSummary.ControlRevision != priorPlan.SummaryControlRevision ||
			expectedSummary.SummaryDigest != priorPlan.SummaryDigest) {
		return ErrInvalidServiceStateV3
	}
	checkLiveKeys := requireRemovalDrain && priorPlan.RemovalChunks == 0
	drainKeys := []string{}
	if checkLiveKeys {
		if priorPlan.Phase != serviceStateV3Reconcile {
			return ErrInvalidServiceStateV3
		}
		var err error
		drainKeys, err = s.serviceStateV3RemovalDrainKeys(ctx, priorPlan)
		if err != nil {
			return err
		}
	}
	if priorPlan.SummaryControlRevision >= math.MaxInt64 {
		return ErrInvalidServiceStateV3
	}
	selector, selectorErr := s.GetServiceRuntimeSelector(ctx, priorPlan.Repository)
	selectorPresent := true
	if errors.Is(selectorErr, ErrNotFound) {
		selectorPresent = false
		selectorErr = nil
	}
	if selectorErr != nil {
		return fmt.Errorf("commit service state v3 chunk: selector preflight: %w", selectorErr)
	}
	selectorContent := map[string]any{}
	if selectorPresent {
		selectorContent = serviceRuntimeSelectorContent(selector)
	}
	nextVisibleFrom := priorPlan.SummaryControlRevision + 1
	encodedUpdates := make([]map[string]any, 0, len(updates))
	for _, update := range updates {
		if update.State.Repository != priorPlan.Repository ||
			servicecatalogv3.ValidateServiceState(update.State, true) != nil {
			return ErrInvalidServiceStateV3
		}
		content := serviceStateContent(update.State)
		content["visible_from"] = nextVisibleFrom
		encodedUpdates = append(encodedUpdates, map[string]any{
			"rid":               serviceStateV3ID(update.State.Repository, update.State.ServiceKey),
			"expected_revision": update.ExpectedRevision,
			"expected_digest":   update.ExpectedDigest,
			"content":           content,
		})
	}
	writeSummary := nextSummary != nil
	var summaryContent map[string]any
	if writeSummary {
		if servicecatalogv3.ValidateRepositoryState(*nextSummary, true) != nil ||
			nextSummary.Repository != priorPlan.Repository ||
			nextSummary.ControlRevision != nextPlan.SummaryControlRevision ||
			nextSummary.SummaryDigest != nextPlan.SummaryDigest {
			return ErrInvalidServiceStateV3
		}
		summaryContent = serviceRepositoryStateContent(*nextSummary)
	}
	results, err := surrealdb.Query[[]serviceStateV3PlanRec](ctx, s.db, `
BEGIN;
LET $repository_state = (SELECT deleting FROM $repository_rid LIMIT 1)[0];
LET $candidate = (SELECT root_digest, control_revision FROM $candidate_rid LIMIT 1)[0];
LET $current = (SELECT schedule_digest FROM $schedule_current LIMIT 1)[0].schedule_digest;
LET $schedule = (SELECT digest, generation, status FROM $schedule_rid LIMIT 1)[0];
LET $chunk = (SELECT identity, schedule_digest, repository, stage, generation,
	status, attempt, lease_token, claimed_by FROM $chunk_rid LIMIT 1)[0];
LET $plan = (SELECT * FROM $plan_rid LIMIT 1)[0];
LET $selector = (SELECT schema, repository, backend,
	catalog_generation_digest, catalog_root_digest, catalog_control_revision,
	state_control_revision, state_summary_digest, search_generation_digest,
	relationship_generation_digest, relationship_root_digest,
	control_revision, digest, changed_at FROM $selector_rid LIMIT 1)[0];
LET $selector_ok = IF $selector_present THEN $selector != NONE
	AND $selector.schema = $expected_selector.schema
	AND $selector.repository = $expected_selector.repository
	AND $selector.backend = $expected_selector.backend
	AND $selector.catalog_generation_digest = $expected_selector.catalog_generation_digest
	AND $selector.catalog_root_digest = $expected_selector.catalog_root_digest
	AND $selector.catalog_control_revision = $expected_selector.catalog_control_revision
	AND $selector.state_control_revision = $expected_selector.state_control_revision
	AND $selector.state_summary_digest = $expected_selector.state_summary_digest
	AND $selector.search_generation_digest = $expected_selector.search_generation_digest
	AND $selector.relationship_generation_digest = $expected_selector.relationship_generation_digest
	AND $selector.relationship_root_digest = $expected_selector.relationship_root_digest
	AND $selector.control_revision = $expected_selector.control_revision
	AND $selector.digest = $expected_selector.digest
	AND $selector.changed_at = $expected_selector.changed_at
	ELSE $selector = NONE END;
IF !$selector_ok {
	THROW 'phebs-permanent: service state v3 selector fence changed';
};
LET $preimage_summaries = SELECT snapshot_revision, snapshot_digest
	FROM service_state_v3_repository_preimage
	WHERE repository = $repository ORDER BY snapshot_revision LIMIT 2;
LET $preimage_row_snapshots = SELECT snapshot_revision, snapshot_digest
	FROM service_state_v3_preimage WHERE repository = $repository
	GROUP BY snapshot_revision, snapshot_digest
	ORDER BY snapshot_revision LIMIT 2;
IF array::len($preimage_summaries) > 1 OR
	array::len($preimage_row_snapshots) > 1 OR
	(array::len($preimage_summaries) = 0 AND
		array::len($preimage_row_snapshots) != 0) OR
	(array::len($preimage_row_snapshots) = 1 AND
		($preimage_row_snapshots[0].snapshot_revision !=
			$preimage_summaries[0].snapshot_revision OR
		$preimage_row_snapshots[0].snapshot_digest !=
			$preimage_summaries[0].snapshot_digest)) {
	THROW 'phebs-permanent: invalid service state v3 preimage ownership';
};
LET $preimage_snapshot = $preimage_summaries[0];
IF $preimage_snapshot != NONE AND ($selector = NONE OR
	$selector.backend != 'v3' OR
	$preimage_snapshot.snapshot_revision != $selector.state_control_revision OR
	$preimage_snapshot.snapshot_digest != $selector.state_summary_digest) {
	THROW 'phebs-deferred: service state v3 preimage backlog';
};
LET $summary = (SELECT * FROM $summary_rid LIMIT 1)[0];
LET $summary_ok = IF $expected_summary_revision = 0 THEN $summary = NONE
	ELSE $summary != NONE AND $summary.control_revision = $expected_summary_revision
		AND $summary.summary_digest = $expected_summary_digest END;
LET $plan_ok = $plan != NONE AND $plan.digest = $plan_digest
	AND $plan.schedule_digest = $schedule_digest AND $plan.state = 'running'
	AND $plan.next_chunk = $expected_next_chunk
	AND $plan.removal_cursor = $expected_removal_cursor
	AND $plan.catalog_service_count = $expected_catalog_count
	AND $plan.live_service_count = $expected_live_count
	AND $plan.current_count = $expected_current_count
	AND $plan.stale_count = $expected_stale_count
	AND $plan.unavailable_count = $expected_unavailable_count
	AND $plan.conflict_count = $expected_conflict_count
	AND $plan.tombstone_count = $expected_tombstones
	AND $plan.rows_read = $expected_rows_read
	AND $plan.rows_written = $expected_rows_written
	AND $plan.bytes_written = $expected_bytes_written
	AND $plan.max_chunk_rows = $expected_max_chunk_rows
	AND $plan.summary_control_revision = $expected_summary_revision
	AND $plan.summary_digest = $expected_summary_digest;
LET $lease_ok = $chunk != NONE AND $chunk.identity = $chunk_identity
	AND $chunk.schedule_digest = $schedule_digest AND $chunk.repository = $repository
	AND $chunk.stage = $stage AND $chunk.generation = $plan_digest
	AND $chunk.status = 'running' AND $chunk.attempt = $attempt
	AND $chunk.lease_token = $lease AND $chunk.claimed_by = $worker;
LET $drained = IF !$require_removal_drain THEN true ELSE
	IF $check_live_keys THEN (
		SELECT VALUE service_key FROM service_state_v3_current
			WHERE repository = $repository AND removed = false
			ORDER BY service_key LIMIT $final_live_limit
	) = $expected_final_live_keys ELSE array::len(
		SELECT id FROM service_state_v3_current
			WHERE repository = $repository AND removed = false
				AND service_key > $removal_cursor LIMIT 1
	) = 0 END;
IF $repository_state = NONE OR $repository_state.deleting = true OR
	$candidate = NONE OR $candidate.root_digest != $catalog_root OR
	$candidate.control_revision != $catalog_revision OR $current != $schedule_digest OR
	$schedule = NONE OR $schedule.digest != $schedule_digest OR
	$schedule.generation != $plan_digest OR $schedule.status != 'active' OR
	!$selector_ok OR !$plan_ok OR !$lease_ok OR !$summary_ok OR !$drained {
	THROW 'phebs-permanent: service state v3 chunk fence changed';
};
FOR $update IN $updates {
	LET $existing = (SELECT * FROM $update.rid LIMIT 1)[0];
	LET $revision = IF $existing = NONE THEN 0 ELSE $existing.control_revision END;
	LET $digest = IF $existing = NONE THEN '' ELSE $existing.state_digest END;
	IF $revision != $update.expected_revision OR $digest != $update.expected_digest {
		THROW 'phebs-permanent: service state v3 row compare-and-swap conflict';
	};
	LET $preserve = $existing != NONE AND $selector != NONE
		AND $selector.backend = 'v3'
		AND ($existing.visible_from ?? 1) <= $selector.state_control_revision;
	IF $preserve {
		LET $prior_rows = SELECT state_digest, control_revision, snapshot_digest
			FROM service_state_v3_preimage
			WHERE repository = $repository
				AND snapshot_revision = $selector.state_control_revision
				AND service_key = $update.content.service_key LIMIT 2;
		IF array::len($prior_rows) > 1 {
			THROW 'phebs-permanent: duplicate service state v3 preimage';
		};
		LET $prior = $prior_rows[0];
		IF $prior = NONE {
			CREATE service_state_v3_preimage CONTENT {
				schema: $existing.schema, repository: $existing.repository,
				service_key: $existing.service_key,
				display_name: $existing.display_name,
				disposition: $existing.disposition, origin: $existing.origin,
				reason: $existing.reason, successors: $existing.successors,
				incarnation: $existing.incarnation,
				desired_generation: $existing.desired_generation,
				desired_source_generation: $existing.desired_source_generation,
				desired_catalog_generation: $existing.desired_catalog_generation,
				active_desired_generation: $existing.active_desired_generation,
				active_source_generation: $existing.active_source_generation,
				active_catalog_generation: $existing.active_catalog_generation,
				active_search_generation: $existing.active_search_generation,
				status: $existing.status, removed: $existing.removed,
				state_digest: $existing.state_digest,
				control_revision: $existing.control_revision,
				changed_at: $existing.changed_at,
				visible_from: $existing.visible_from ?? 1,
				snapshot_revision: $selector.state_control_revision,
				snapshot_digest: $selector.state_summary_digest
			} RETURN NONE;
		} ELSE IF $prior.state_digest != $existing.state_digest
			OR $prior.control_revision != $existing.control_revision
			OR $prior.snapshot_digest != $selector.state_summary_digest {
			THROW 'phebs-permanent: service state v3 preimage conflict';
		};
	};
	UPSERT $update.rid CONTENT $update.content RETURN NONE;
};
LET $preserved_rows = IF $selector = NONE THEN 0 ELSE array::len(
	SELECT id FROM service_state_v3_preimage
		WHERE repository = $repository
			AND snapshot_revision = $selector.state_control_revision
			AND snapshot_digest = $selector.state_summary_digest LIMIT 1
) END;
LET $preserve_summary = $summary != NONE AND $selector != NONE
	AND $selector.backend = 'v3' AND ($preserved_rows = 1 OR $write_summary)
	AND $summary.control_revision = $selector.state_control_revision
	AND $summary.summary_digest = $selector.state_summary_digest;
IF $preserve_summary {
	LET $prior_summary_rows = SELECT summary_digest, snapshot_digest
		FROM service_state_v3_repository_preimage
		WHERE repository = $repository
			AND snapshot_revision = $selector.state_control_revision LIMIT 2;
	IF array::len($prior_summary_rows) > 1 {
		THROW 'phebs-permanent: duplicate service state v3 summary preimage';
	};
	LET $prior_summary = $prior_summary_rows[0];
	IF $prior_summary = NONE {
		CREATE service_state_v3_repository_preimage CONTENT {
			schema: $summary.schema, repository: $summary.repository,
			catalog_generation: $summary.catalog_generation,
			catalog_control_revision: $summary.catalog_control_revision,
			catalog_service_count: $summary.catalog_service_count,
			live_service_count: $summary.live_service_count,
			current_count: $summary.current_count,
			stale_count: $summary.stale_count,
			unavailable_count: $summary.unavailable_count,
			conflict_count: $summary.conflict_count,
			tombstone_count: $summary.tombstone_count,
			summary_digest: $summary.summary_digest,
			control_revision: $summary.control_revision,
			updated_at: $summary.updated_at,
			snapshot_revision: $selector.state_control_revision,
			snapshot_digest: $selector.state_summary_digest
		} RETURN NONE;
	} ELSE IF $prior_summary.summary_digest != $summary.summary_digest
		OR $prior_summary.snapshot_digest != $selector.state_summary_digest {
		THROW 'phebs-permanent: service state v3 summary preimage conflict';
	};
};
IF $write_summary {
	UPSERT $summary_rid CONTENT $summary_content RETURN NONE;
};
UPDATE $plan_rid CONTENT $plan_content RETURN AFTER;
COMMIT;`, map[string]any{
		"candidate_rid":  serviceCatalogV3CandidateID(priorPlan.Repository),
		"repository_rid": repoID(priorPlan.Repository),
		"schedule_current": models.NewRecordID(
			"generation_schedule_current",
			strings.TrimPrefix(generationCurrentID(priorPlan.Repository, chunk.Stage), "sha256:"),
		),
		"schedule_rid": models.NewRecordID(
			"generation_schedule", strings.TrimPrefix(chunk.ScheduleDigest, "sha256:"),
		),
		"chunk_rid":         models.NewRecordID("generation_schedule_chunk", chunk.ID),
		"plan_rid":          serviceStateV3PlanID(priorPlan.Digest),
		"summary_rid":       serviceStateV3RepositoryID(priorPlan.Repository),
		"selector_rid":      serviceRuntimeSelectorID(priorPlan.Repository),
		"selector_present":  selectorPresent,
		"expected_selector": selectorContent,
		"catalog_root":      priorPlan.CatalogRoot,
		"catalog_revision":  priorPlan.CatalogControlRevision,
		"schedule_digest":   priorPlan.ScheduleDigest, "plan_digest": priorPlan.Digest,
		"repository": priorPlan.Repository, "stage": chunk.Stage,
		"chunk_identity": chunk.Identity, "attempt": chunk.Attempt,
		"lease": chunk.LeaseToken, "worker": chunk.ClaimedBy,
		"expected_next_chunk":        priorPlan.NextChunk,
		"expected_removal_cursor":    priorPlan.RemovalCursor,
		"expected_catalog_count":     priorPlan.CatalogServiceCount,
		"expected_live_count":        priorPlan.LiveServiceCount,
		"expected_current_count":     priorPlan.CurrentCount,
		"expected_stale_count":       priorPlan.StaleCount,
		"expected_unavailable_count": priorPlan.UnavailableCount,
		"expected_conflict_count":    priorPlan.ConflictCount,
		"expected_tombstones":        priorPlan.TombstoneCount,
		"expected_rows_read":         priorPlan.RowsRead,
		"expected_rows_written":      priorPlan.RowsWritten,
		"expected_bytes_written":     priorPlan.BytesWritten,
		"expected_max_chunk_rows":    priorPlan.MaxChunkRows,
		"expected_summary_revision":  priorPlan.SummaryControlRevision,
		"expected_summary_digest":    priorPlan.SummaryDigest,
		"require_removal_drain":      requireRemovalDrain,
		"check_live_keys":            checkLiveKeys,
		"expected_final_live_keys":   drainKeys,
		"final_live_limit":           servicecatalogv3.MaxTotalServices + 1,
		"removal_cursor":             priorPlan.RemovalCursor,
		"updates":                    encodedUpdates, "write_summary": writeSummary,
		"summary_content": summaryContent,
		"plan_content":    serviceStateV3PlanContent(nextPlan),
	})
	if err != nil {
		if strings.Contains(err.Error(), serviceStateV3PreimageBacklogMarker) {
			return WithDeferral(fmt.Errorf(
				"commit service state v3 chunk: preimage lifecycle backlog: %w",
				ErrConflict,
			))
		}
		return fmt.Errorf("commit service state v3 chunk: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return fmt.Errorf("commit service state v3 chunk: %w", ErrConflict)
	}
	stored := rows[0].plan()
	if validateServiceStateV3Plan(stored) != nil ||
		stored.Digest != nextPlan.Digest || stored.NextChunk != nextPlan.NextChunk ||
		stored.State != nextPlan.State {
		return ErrInvalidServiceStateV3
	}
	return nil
}

const serviceStateV3SchemaMigrationVersion = "t41.5-service-state-v3-schema-v1"

func serviceStateV3SchemaMigrationID() models.RecordID {
	return models.NewRecordID("store_migration", "service_state_v3_schema")
}

const serviceStateV3Schema = `
DEFINE TABLE OVERWRITE service_state_v3_current SCHEMAFULL;
DEFINE FIELD OVERWRITE schema ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE repository ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE service_key ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE display_name ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE disposition ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE origin ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE reason ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE successors ON service_state_v3_current TYPE array<string>;
DEFINE FIELD OVERWRITE incarnation ON service_state_v3_current TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE desired_generation ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE desired_source_generation ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE desired_catalog_generation ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE active_desired_generation ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE active_source_generation ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE active_catalog_generation ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE active_search_generation ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE status ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE removed ON service_state_v3_current TYPE bool;
DEFINE FIELD OVERWRITE state_digest ON service_state_v3_current TYPE string;
DEFINE FIELD OVERWRITE control_revision ON service_state_v3_current TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE changed_at ON service_state_v3_current TYPE datetime;
DEFINE FIELD OVERWRITE visible_from ON service_state_v3_current TYPE int ASSERT $value >= 1;
DEFINE INDEX OVERWRITE service_state_v3_repository_key ON service_state_v3_current FIELDS repository, service_key UNIQUE;
DEFINE INDEX OVERWRITE service_state_v3_desired_root ON service_state_v3_current FIELDS desired_catalog_generation;
DEFINE INDEX OVERWRITE service_state_v3_active_root ON service_state_v3_current FIELDS active_catalog_generation;

DEFINE TABLE OVERWRITE service_state_v3_repository SCHEMAFULL;
DEFINE FIELD OVERWRITE schema ON service_state_v3_repository TYPE string;
DEFINE FIELD OVERWRITE repository ON service_state_v3_repository TYPE string;
DEFINE FIELD OVERWRITE catalog_generation ON service_state_v3_repository TYPE string;
DEFINE FIELD OVERWRITE catalog_control_revision ON service_state_v3_repository TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE catalog_service_count ON service_state_v3_repository TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE live_service_count ON service_state_v3_repository TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE current_count ON service_state_v3_repository TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE stale_count ON service_state_v3_repository TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE unavailable_count ON service_state_v3_repository TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE conflict_count ON service_state_v3_repository TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE tombstone_count ON service_state_v3_repository TYPE int ASSERT $value >= 0;
DEFINE FIELD OVERWRITE summary_digest ON service_state_v3_repository TYPE string;
DEFINE FIELD OVERWRITE control_revision ON service_state_v3_repository TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE updated_at ON service_state_v3_repository TYPE datetime;

DEFINE TABLE OVERWRITE service_state_v3_plan SCHEMAFULL;
DEFINE FIELD OVERWRITE schema ON service_state_v3_plan TYPE string;
DEFINE FIELD OVERWRITE digest ON service_state_v3_plan TYPE string;
DEFINE FIELD OVERWRITE repository ON service_state_v3_plan TYPE string;
DEFINE FIELD OVERWRITE phase ON service_state_v3_plan TYPE string ASSERT $value INSIDE ['reconcile', 'activate'];
DEFINE FIELD OVERWRITE catalog_root ON service_state_v3_plan TYPE string;
DEFINE FIELD OVERWRITE catalog_control_revision ON service_state_v3_plan TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE search_generation ON service_state_v3_plan TYPE string;
DEFINE FIELD OVERWRITE schedule_digest ON service_state_v3_plan TYPE string;
DEFINE FIELD OVERWRITE repair ON service_state_v3_plan TYPE int ASSERT $value >= 0 AND $value <= 8;
DEFINE FIELD OVERWRITE state ON service_state_v3_plan TYPE string ASSERT $value INSIDE ['running', 'reconciled', 'activated', 'failed', 'superseded'];
DEFINE FIELD OVERWRITE base_chunk ON service_state_v3_plan TYPE int ASSERT $value >= 0;
DEFINE FIELD OVERWRITE next_chunk ON service_state_v3_plan TYPE int ASSERT $value >= 0;
DEFINE FIELD OVERWRITE total_chunks ON service_state_v3_plan TYPE int ASSERT $value >= 1 AND $value <= 129;
DEFINE FIELD OVERWRITE service_member_chunks ON service_state_v3_plan TYPE int ASSERT $value >= 0 AND $value <= 64;
DEFINE FIELD OVERWRITE removal_chunks ON service_state_v3_plan TYPE int ASSERT $value >= 0 AND $value <= 98;
DEFINE FIELD OVERWRITE removal_cursor ON service_state_v3_plan TYPE string;
DEFINE FIELD OVERWRITE catalog_service_count ON service_state_v3_plan TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE live_service_count ON service_state_v3_plan TYPE int ASSERT $value >= 0 AND $value <= 25000;
DEFINE FIELD OVERWRITE current_count ON service_state_v3_plan TYPE int ASSERT $value >= 0 AND $value <= 25000;
DEFINE FIELD OVERWRITE stale_count ON service_state_v3_plan TYPE int ASSERT $value >= 0 AND $value <= 25000;
DEFINE FIELD OVERWRITE unavailable_count ON service_state_v3_plan TYPE int ASSERT $value >= 0 AND $value <= 25000;
DEFINE FIELD OVERWRITE conflict_count ON service_state_v3_plan TYPE int ASSERT $value >= 0 AND $value <= 25000;
DEFINE FIELD OVERWRITE tombstone_count ON service_state_v3_plan TYPE int ASSERT $value >= 0;
DEFINE FIELD OVERWRITE summary_control_revision ON service_state_v3_plan TYPE int ASSERT $value >= 0;
DEFINE FIELD OVERWRITE summary_digest ON service_state_v3_plan TYPE string;
DEFINE FIELD OVERWRITE rows_read ON service_state_v3_plan TYPE int ASSERT $value >= 0;
DEFINE FIELD OVERWRITE rows_written ON service_state_v3_plan TYPE int ASSERT $value >= 0;
DEFINE FIELD OVERWRITE bytes_written ON service_state_v3_plan TYPE int ASSERT $value >= 0;
DEFINE FIELD OVERWRITE max_chunk_rows ON service_state_v3_plan TYPE int ASSERT $value >= 0 AND $value <= 512;
DEFINE FIELD OVERWRITE created_at ON service_state_v3_plan TYPE datetime;
DEFINE FIELD OVERWRITE updated_at ON service_state_v3_plan TYPE datetime;
DEFINE INDEX OVERWRITE service_state_v3_plan_target ON service_state_v3_plan FIELDS repository, phase, catalog_root, catalog_control_revision, search_generation, state, updated_at;
`

func (s *Surreal) migrateServiceStateV3Schema(ctx context.Context) error {
	markerResults, err := surrealdb.Query[[]struct {
		Version string `json:"version"`
	}](ctx, s.db, "SELECT version FROM $rid", map[string]any{
		"rid": serviceStateV3SchemaMigrationID(),
	})
	if err != nil {
		return fmt.Errorf("migrate service state v3 schema: marker: %w", err)
	}
	markerRows := firstDomainRows(markerResults)
	if len(markerRows) == 1 {
		if markerRows[0].Version == serviceStateV3SchemaMigrationVersion {
			return nil
		}
		return fmt.Errorf("migrate service state v3 schema: unsupported marker %q", markerRows[0].Version)
	}
	if len(markerRows) > 1 {
		return errors.New("migrate service state v3 schema: duplicate marker")
	}
	preflight, err := surrealdb.Query[any](ctx, s.db, `
DEFINE TABLE IF NOT EXISTS service_state_v3_current SCHEMALESS;
DEFINE TABLE IF NOT EXISTS service_state_v3_repository SCHEMALESS;
DEFINE TABLE IF NOT EXISTS service_state_v3_plan SCHEMALESS;`, nil)
	if err != nil {
		return fmt.Errorf("migrate service state v3 schema: preflight schema: %w", err)
	}
	for index, result := range *preflight {
		if result.Error != nil {
			return fmt.Errorf("migrate service state v3 preflight statement %d: %s", index, result.Error.Message)
		}
	}
	probe, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](ctx, s.db, `RETURN [{ count:
		array::len(SELECT id FROM service_state_v3_current LIMIT 1) +
		array::len(SELECT id FROM service_state_v3_repository LIMIT 1) +
		array::len(SELECT id FROM service_state_v3_plan LIMIT 1)
	}];`, nil)
	if err != nil {
		return fmt.Errorf("migrate service state v3 schema: preflight: %w", err)
	}
	probeRows := firstDomainRows(probe)
	if len(probeRows) != 1 || probeRows[0].Count != 0 {
		return errors.New("migrate service state v3 schema: unowned pre-migration rows")
	}
	results, err := surrealdb.Query[any](ctx, s.db, serviceStateV3Schema, nil)
	if err != nil {
		return fmt.Errorf("migrate service state v3 schema: define: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf("migrate service state v3 schema statement %d: %s", index, result.Error.Message)
		}
	}
	marker, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $current = (SELECT version FROM $rid LIMIT 1)[0].version;
IF $current != NONE AND $current != $version {
	THROW 'phebs-permanent: unsupported service state v3 schema migration';
};
UPSERT $rid SET version = IF $current = NONE THEN $version ELSE $current END,
	completed_at = IF $current = NONE THEN time::now() ELSE completed_at END RETURN NONE;
COMMIT;`, map[string]any{
		"rid": serviceStateV3SchemaMigrationID(), "version": serviceStateV3SchemaMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("migrate service state v3 schema: marker write: %w", err)
	}
	for index, result := range *marker {
		if result.Error != nil {
			return fmt.Errorf("migrate service state v3 marker statement %d: %s", index, result.Error.Message)
		}
	}
	return nil
}

const (
	serviceStateV3SnapshotSchemaMigrationVersion                  = "t41.10-service-state-v3-snapshot-v1"
	serviceStateV3SnapshotCompatibilityMigrationVersion           = "t41.10-service-state-v3-snapshot-compat-v1"
	serviceCatalogV3SourceGenerationCompatibilityMigrationVersion = "t41.10-service-catalog-v3-source-generation-compat-v1"
)

func serviceStateV3SnapshotSchemaMigrationID() models.RecordID {
	return models.NewRecordID("store_migration", "service_state_v3_snapshot_schema")
}

const serviceStateV3SnapshotSchema = `
DEFINE FIELD OVERWRITE visible_from ON service_state_v3_current TYPE option<int>;
UPDATE service_state_v3_current SET visible_from = 1 WHERE visible_from = NONE;
DEFINE FIELD OVERWRITE visible_from ON service_state_v3_current TYPE int ASSERT $value >= 1;

DEFINE TABLE OVERWRITE service_state_v3_preimage SCHEMAFULL;
DEFINE FIELD OVERWRITE schema ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE repository ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE service_key ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE display_name ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE disposition ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE origin ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE reason ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE successors ON service_state_v3_preimage TYPE array<string>;
DEFINE FIELD OVERWRITE incarnation ON service_state_v3_preimage TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE desired_generation ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE desired_source_generation ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE desired_catalog_generation ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE active_desired_generation ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE active_source_generation ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE active_catalog_generation ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE active_search_generation ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE status ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE removed ON service_state_v3_preimage TYPE bool;
DEFINE FIELD OVERWRITE state_digest ON service_state_v3_preimage TYPE string;
DEFINE FIELD OVERWRITE control_revision ON service_state_v3_preimage TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE changed_at ON service_state_v3_preimage TYPE datetime;
DEFINE FIELD OVERWRITE visible_from ON service_state_v3_preimage TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE snapshot_revision ON service_state_v3_preimage TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE snapshot_digest ON service_state_v3_preimage TYPE string;
DEFINE INDEX OVERWRITE service_state_v3_preimage_snapshot ON service_state_v3_preimage FIELDS repository, snapshot_revision, service_key UNIQUE;
DEFINE INDEX OVERWRITE service_state_v3_preimage_desired_root ON service_state_v3_preimage FIELDS desired_catalog_generation;
DEFINE INDEX OVERWRITE service_state_v3_preimage_active_root ON service_state_v3_preimage FIELDS active_catalog_generation;

DEFINE TABLE OVERWRITE service_state_v3_repository_preimage SCHEMAFULL;
DEFINE FIELD OVERWRITE schema ON service_state_v3_repository_preimage TYPE string;
DEFINE FIELD OVERWRITE repository ON service_state_v3_repository_preimage TYPE string;
DEFINE FIELD OVERWRITE catalog_generation ON service_state_v3_repository_preimage TYPE string;
DEFINE FIELD OVERWRITE catalog_control_revision ON service_state_v3_repository_preimage TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE catalog_service_count ON service_state_v3_repository_preimage TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE live_service_count ON service_state_v3_repository_preimage TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE current_count ON service_state_v3_repository_preimage TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE stale_count ON service_state_v3_repository_preimage TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE unavailable_count ON service_state_v3_repository_preimage TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE conflict_count ON service_state_v3_repository_preimage TYPE int ASSERT $value >= 0 AND $value <= 12500;
DEFINE FIELD OVERWRITE tombstone_count ON service_state_v3_repository_preimage TYPE int ASSERT $value >= 0;
DEFINE FIELD OVERWRITE summary_digest ON service_state_v3_repository_preimage TYPE string;
DEFINE FIELD OVERWRITE control_revision ON service_state_v3_repository_preimage TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE updated_at ON service_state_v3_repository_preimage TYPE datetime;
DEFINE FIELD OVERWRITE snapshot_revision ON service_state_v3_repository_preimage TYPE int ASSERT $value >= 1;
DEFINE FIELD OVERWRITE snapshot_digest ON service_state_v3_repository_preimage TYPE string;
DEFINE INDEX OVERWRITE service_state_v3_repository_preimage_snapshot ON service_state_v3_repository_preimage FIELDS repository, snapshot_revision UNIQUE;
DEFINE INDEX OVERWRITE service_state_v3_repository_preimage_catalog ON service_state_v3_repository_preimage FIELDS repository, catalog_generation, snapshot_revision;

`

func (s *Surreal) migrateServiceStateV3SnapshotSchema(ctx context.Context) error {
	return s.migrateServiceStateV3SnapshotSchemaWithDefinition(
		ctx,
		serviceStateV3SnapshotSchema,
	)
}

func (s *Surreal) migrateServiceStateV3SnapshotSchemaWithDefinition(
	ctx context.Context,
	definition string,
) error {
	marker := serviceStateV3SnapshotSchemaMigrationID()
	results, err := surrealdb.Query[[]struct {
		Version string `json:"version"`
	}](ctx, s.db, "SELECT version FROM $rid", map[string]any{"rid": marker})
	if err != nil {
		return fmt.Errorf("migrate service state v3 snapshot schema: marker: %w", err)
	}
	rows := firstDomainRows(results)
	current := false
	if len(rows) == 1 {
		if rows[0].Version == serviceStateV3SnapshotSchemaMigrationVersion {
			current = true
		} else {
			return fmt.Errorf("migrate service state v3 snapshot schema: unsupported marker %q", rows[0].Version)
		}
	}
	if len(rows) > 1 {
		return errors.New("migrate service state v3 snapshot schema: duplicate marker")
	}
	latched, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $versions = SELECT version FROM $rid LIMIT 2;
IF array::len($versions) != 1 OR
	($versions[0].version != $candidate AND
	 $versions[0].version != $selector AND
	 $versions[0].version != $snapshot AND
	 $versions[0].version != $source_generation) {
	THROW 'phebs-permanent: unsupported service state v3 snapshot compatibility marker';
};
UPDATE $rid SET version = IF $versions[0].version = $source_generation
	THEN $source_generation ELSE $snapshot END RETURN NONE;
COMMIT;`, map[string]any{
		"rid":               candidateControlRevisionMigrationID(),
		"candidate":         candidateControlRevisionMigrationVersion,
		"selector":          serviceRuntimeSelectorCompatibilityMigrationVersion,
		"snapshot":          serviceStateV3SnapshotCompatibilityMigrationVersion,
		"source_generation": serviceCatalogV3SourceGenerationCompatibilityMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("migrate service state v3 snapshot schema: compatibility latch: %w", err)
	}
	for index, result := range *latched {
		if result.Error != nil {
			return fmt.Errorf("migrate service state v3 snapshot compatibility statement %d: %s", index, result.Error.Message)
		}
	}
	if current {
		return nil
	}
	defined, err := surrealdb.Query[any](ctx, s.db, definition, nil)
	if err != nil {
		return fmt.Errorf("migrate service state v3 snapshot schema: define: %w", err)
	}
	for index, result := range *defined {
		if result.Error != nil {
			return fmt.Errorf("migrate service state v3 snapshot schema statement %d: %s", index, result.Error.Message)
		}
	}
	written, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $current = (SELECT version FROM $rid LIMIT 1)[0].version;
IF $current != NONE AND $current != $version {
	THROW 'phebs-permanent: unsupported service state v3 snapshot schema migration';
};
UPSERT $rid SET version = IF $current = NONE THEN $version ELSE $current END,
	completed_at = IF $current = NONE THEN time::now() ELSE completed_at END RETURN NONE;
COMMIT;`, map[string]any{
		"rid": marker, "version": serviceStateV3SnapshotSchemaMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("migrate service state v3 snapshot schema: marker write: %w", err)
	}
	for index, result := range *written {
		if result.Error != nil {
			return fmt.Errorf("migrate service state v3 snapshot marker statement %d: %s", index, result.Error.Message)
		}
	}
	return nil
}

// migrateServiceSourceGenerationCompatibility irreversibly raises the common
// service compatibility marker for catalog-v3 source-generation semantics
// after the snapshot schema is available. It changes no schema or catalog row.
func (s *Surreal) migrateServiceSourceGenerationCompatibility(
	ctx context.Context,
) error {
	results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $versions = SELECT version FROM $rid LIMIT 2;
IF array::len($versions) != 1 OR
	($versions[0].version != $snapshot AND
	 $versions[0].version != $source_generation) {
	THROW 'phebs-permanent: unsupported service catalog v3 source generation compatibility marker';
};
UPDATE $rid SET version = $source_generation RETURN NONE;
COMMIT;`, map[string]any{
		"rid":               candidateControlRevisionMigrationID(),
		"snapshot":          serviceStateV3SnapshotCompatibilityMigrationVersion,
		"source_generation": serviceCatalogV3SourceGenerationCompatibilityMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("migrate service catalog v3 source generation compatibility: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate service catalog v3 source generation compatibility statement %d: %s",
				index,
				result.Error.Message,
			)
		}
	}
	return nil
}

type serviceStateV3SchedulePointerRec struct {
	ScheduleDigest string `json:"schedule_digest"`
}

func fenceServiceStateV3CandidateAdvance(
	ctx context.Context,
	tx *surrealdb.Transaction,
	repository, priorRoot, nextRoot string,
) error {
	if priorRoot == "" || priorRoot == nextRoot {
		return nil
	}
	reconcilePlan, reconcileSchedule, err := serviceStateV3PlanInTransaction(
		ctx, tx, repository, serviceStateV3Reconcile,
	)
	if err != nil {
		return err
	}
	if reconcilePlan != nil && reconcilePlan.State == serviceStateV3Running {
		return fmt.Errorf("service catalog v3 successor refused while state reconcile is unsettled: %w", ErrConflict)
	}
	if reconcileSchedule != nil && reconcilePlan == nil {
		return ErrInvalidServiceStateV3
	}
	activationPlan, activationSchedule, err := serviceStateV3PlanInTransaction(
		ctx, tx, repository, serviceStateV3Activate,
	)
	if err != nil {
		return err
	}
	if activationSchedule == nil {
		return nil
	}
	if activationPlan == nil {
		return ErrInvalidServiceStateV3
	}
	results, err := surrealdb.Query[any](ctx, tx, `
UPDATE $plan_rid SET state = IF state = 'running' THEN 'superseded' ELSE state END,
	updated_at = time::now() WHERE digest = $plan_digest RETURN NONE;
UPDATE $schedule_rid SET status = IF status = 'active' THEN 'superseded' ELSE status END,
	updated_at = time::now() WHERE digest = $schedule_digest RETURN NONE;
DELETE $current_rid WHERE schedule_digest = $schedule_digest RETURN NONE;`, map[string]any{
		"plan_rid":    serviceStateV3PlanID(activationPlan.Digest),
		"plan_digest": activationPlan.Digest,
		"schedule_rid": models.NewRecordID(
			"generation_schedule", strings.TrimPrefix(activationSchedule.Digest, "sha256:"),
		),
		"schedule_digest": activationSchedule.Digest,
		"current_rid": models.NewRecordID(
			"generation_schedule_current",
			strings.TrimPrefix(generationCurrentID(repository, ServiceStateV3ActivateStage), "sha256:"),
		),
	})
	if err != nil {
		return err
	}
	for _, result := range *results {
		if result.Error != nil {
			return errors.New(result.Error.Message)
		}
	}
	return nil
}

func serviceStateV3PlanInTransaction(
	ctx context.Context,
	tx *surrealdb.Transaction,
	repository, phase string,
) (*ServiceStateV3Plan, *GenerationSchedule, error) {
	pointerResults, err := surrealdb.Query[[]serviceStateV3SchedulePointerRec](
		ctx, tx, "SELECT schedule_digest FROM $rid",
		map[string]any{"rid": models.NewRecordID(
			"generation_schedule_current",
			strings.TrimPrefix(generationCurrentID(repository, serviceStateV3Stage(phase)), "sha256:"),
		)},
	)
	if err != nil {
		return nil, nil, err
	}
	pointers := firstDomainRows(pointerResults)
	if len(pointers) == 0 {
		return nil, nil, nil
	}
	if len(pointers) != 1 || !validSHA256Digest(pointers[0].ScheduleDigest) {
		return nil, nil, ErrInvalidServiceStateV3
	}
	scheduleResults, err := surrealdb.Query[[]generationScheduleRec](
		ctx, tx, "SELECT * FROM $rid",
		map[string]any{"rid": models.NewRecordID(
			"generation_schedule", strings.TrimPrefix(pointers[0].ScheduleDigest, "sha256:"),
		)},
	)
	if err != nil {
		return nil, nil, err
	}
	scheduleRows := firstDomainRows(scheduleResults)
	if len(scheduleRows) != 1 {
		return nil, nil, ErrInvalidServiceStateV3
	}
	schedule := scheduleRows[0].schedule()
	if ValidateGenerationSchedule(schedule) != nil || schedule.Repository != repository ||
		schedule.Stage != serviceStateV3Stage(phase) ||
		schedule.Digest != pointers[0].ScheduleDigest {
		return nil, nil, ErrInvalidServiceStateV3
	}
	planResults, err := surrealdb.Query[[]serviceStateV3PlanRec](
		ctx, tx, "SELECT * FROM $rid",
		map[string]any{"rid": serviceStateV3PlanID(schedule.Generation)},
	)
	if err != nil {
		return nil, nil, err
	}
	planRows := firstDomainRows(planResults)
	if len(planRows) != 1 {
		return nil, &schedule, nil
	}
	plan := planRows[0].plan()
	if validateServiceStateV3Plan(plan) != nil || plan.ScheduleDigest != schedule.Digest ||
		plan.Repository != repository || plan.Phase != phase {
		return nil, nil, ErrInvalidServiceStateV3
	}
	return &plan, &schedule, nil
}

type serviceStateV3Counts struct {
	Live        int
	Current     int
	Stale       int
	Unavailable int
	Conflict    int
	Tombstones  int
}

func (s *Surreal) serviceStateV3CountsForRepository(
	ctx context.Context,
	repository string,
) (serviceStateV3Counts, error) {
	results, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_current
	WHERE repository = $repository ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository": repository, "limit": servicecatalogv3.MaxTotalServices*2 + 1,
	})
	if err != nil {
		return serviceStateV3Counts{}, err
	}
	rows := firstDomainRows(results)
	if len(rows) > servicecatalogv3.MaxTotalServices*2 {
		return serviceStateV3Counts{}, ErrInvalidServiceStateV3
	}
	counts := serviceStateV3Counts{}
	priorKey := ""
	for _, record := range rows {
		state, stateErr := serviceStateV3FromRec(record)
		if stateErr != nil || state.Repository != repository || state.ServiceKey <= priorKey {
			return serviceStateV3Counts{}, ErrInvalidServiceStateV3
		}
		priorKey = state.ServiceKey
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
			return serviceStateV3Counts{}, ErrInvalidServiceStateV3
		}
	}
	return counts, nil
}

func (counts serviceStateV3Counts) matchesPlan(plan ServiceStateV3Plan) bool {
	return counts.Live == plan.LiveServiceCount && counts.Current == plan.CurrentCount &&
		counts.Stale == plan.StaleCount && counts.Unavailable == plan.UnavailableCount &&
		counts.Conflict == plan.ConflictCount && counts.Tombstones == plan.TombstoneCount
}

func (counts serviceStateV3Counts) matchesSummary(
	summary servicecatalog.RepositoryState,
) bool {
	return counts.Live == summary.LiveServiceCount && counts.Current == summary.CurrentCount &&
		counts.Stale == summary.StaleCount && counts.Unavailable == summary.UnavailableCount &&
		counts.Conflict == summary.ConflictCount && counts.Tombstones == summary.TombstoneCount
}

type serviceStateV3CurrentScheduleRec struct {
	Repository     string `json:"repository"`
	Stage          string `json:"stage"`
	ScheduleDigest string `json:"schedule_digest"`
}

func (s *Surreal) validateServiceStateV3Precious(
	ctx context.Context,
	roots map[string]string,
	rootRepositories map[string]string,
	candidateRoots map[string]string,
) (int, int, int, error) {
	const maxStateRows = servicecatalogv3.MaxTotalServices * 2
	rowResults, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_current ORDER BY id LIMIT $limit`, map[string]any{
		"limit": maxStateRows + 1,
	})
	if err != nil {
		return 0, 0, 0, err
	}
	rows := firstDomainRows(rowResults)
	if len(rows) > maxStateRows {
		return 0, 0, 0, ErrInvalidServiceStateV3
	}
	counts := make(map[string]serviceStateV3Counts)
	currentStateKeys := make(map[string]struct{}, len(rows))
	for _, record := range rows {
		state, stateErr := serviceStateV3FromRec(record)
		if stateErr != nil {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		stateKey := state.Repository + "\x00" + state.ServiceKey
		if _, duplicate := currentStateKeys[stateKey]; duplicate {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		currentStateKeys[stateKey] = struct{}{}
		expected := serviceStateV3ID(state.Repository, state.ServiceKey)
		expectedID, _ := expected.ID.(string)
		if !validServiceCatalogV3RecordID(
			record.RecID, "service_state_v3_current", expectedID,
		) || record.VisibleFrom == 0 || state.DesiredCatalogGeneration != "" &&
			(roots[state.DesiredCatalogGeneration] != serviceCatalogV3Historical ||
				rootRepositories[state.DesiredCatalogGeneration] != state.Repository) ||
			state.ActiveCatalogGeneration != "" &&
				(roots[state.ActiveCatalogGeneration] != serviceCatalogV3Historical ||
					rootRepositories[state.ActiveCatalogGeneration] != state.Repository) {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		current := counts[state.Repository]
		if state.Removed {
			current.Tombstones++
		} else {
			current.Live++
			switch state.Status {
			case servicecatalog.StatusCurrent:
				current.Current++
			case servicecatalog.StatusStale:
				current.Stale++
			case servicecatalog.StatusUnavailable:
				current.Unavailable++
			case servicecatalog.StatusConflict:
				current.Conflict++
			default:
				return 0, 0, 0, ErrInvalidServiceStateV3
			}
		}
		counts[state.Repository] = current
	}
	summaryResults, err := surrealdb.Query[[]serviceRepositoryStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_repository ORDER BY repository LIMIT $limit`, map[string]any{
		"limit": MaxServiceCatalogV3LifecycleRoots + 1,
	})
	if err != nil {
		return 0, 0, 0, err
	}
	summaryRows := firstDomainRows(summaryResults)
	if len(summaryRows) > MaxServiceCatalogV3LifecycleRoots {
		return 0, 0, 0, ErrInvalidServiceStateV3
	}
	summaries := make(map[string]servicecatalog.RepositoryState, len(summaryRows))
	for _, record := range summaryRows {
		summary, summaryErr := serviceStateV3RepositoryFromRec(record)
		if summaryErr != nil ||
			roots[summary.CatalogGeneration] != serviceCatalogV3Historical ||
			rootRepositories[summary.CatalogGeneration] != summary.Repository ||
			!validServiceCatalogV3RecordID(
				record.RecID, "service_state_v3_repository", summary.Repository,
			) {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		if _, duplicate := summaries[summary.Repository]; duplicate {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		summaries[summary.Repository] = *summary
	}
	const maxPreimageSummaries = MaxServiceCatalogV3LifecycleRoots
	preimageSummaries := make(
		map[string]servicecatalog.RepositoryState,
		len(summaryRows),
	)
	for _, summary := range summaries {
		preimageSummaries[serviceStateV3SnapshotKey(
			summary.Repository,
			summary.ControlRevision,
			summary.SummaryDigest,
		)] = summary
	}
	preimageSummaryResults, err := surrealdb.Query[[]serviceRepositoryStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_repository_preimage
	ORDER BY repository, snapshot_revision LIMIT $limit`, map[string]any{
		"limit": maxPreimageSummaries + 1,
	})
	if err != nil {
		return 0, 0, 0, err
	}
	preimageSummaryRows := firstDomainRows(preimageSummaryResults)
	if len(preimageSummaryRows) > maxPreimageSummaries {
		return 0, 0, 0, ErrInvalidServiceStateV3
	}
	preimageSummaryRepositories := make(map[string]struct{}, len(preimageSummaryRows))
	preimageSummaryOwners := make(map[string]struct{}, len(preimageSummaryRows))
	for _, record := range preimageSummaryRows {
		summary, summaryErr := serviceStateV3RepositoryFromRec(record)
		key := serviceStateV3SnapshotKey(
			record.Repository,
			record.SnapshotRevision,
			record.SnapshotDigest,
		)
		existing := preimageSummaries[key]
		if summaryErr != nil || record.SnapshotRevision != summary.ControlRevision ||
			record.SnapshotDigest != summary.SummaryDigest ||
			roots[summary.CatalogGeneration] != serviceCatalogV3Historical ||
			rootRepositories[summary.CatalogGeneration] != summary.Repository ||
			!validServiceStateV3PreimageRecord(
				record.RecID,
				"service_state_v3_repository_preimage",
			) || existing.Repository != "" &&
			!sameServiceStateV3Summary(existing, *summary) {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		if _, duplicate := preimageSummaryRepositories[record.Repository]; duplicate {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		preimageSummaryRepositories[record.Repository] = struct{}{}
		preimageSummaryOwners[key] = struct{}{}
		preimageSummaries[key] = *summary
	}
	preimageRowResults, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_preimage
	ORDER BY repository, snapshot_revision, service_key LIMIT $limit`, map[string]any{
		"limit": maxStateRows + 1,
	})
	if err != nil {
		return 0, 0, 0, err
	}
	preimageRows := firstDomainRows(preimageRowResults)
	if len(preimageRows) > maxStateRows || len(preimageRows) > len(rows) {
		return 0, 0, 0, ErrInvalidServiceStateV3
	}
	priorPreimageKey := ""
	for _, record := range preimageRows {
		state, stateErr := serviceStateV3FromRec(record)
		snapshotKey := serviceStateV3SnapshotKey(
			record.Repository,
			record.SnapshotRevision,
			record.SnapshotDigest,
		)
		rowKey := snapshotKey + "\x00" + record.ServiceKey
		_, hasCurrent := currentStateKeys[record.Repository+"\x00"+record.ServiceKey]
		_, hasPreimageSummary := preimageSummaryOwners[snapshotKey]
		if stateErr != nil || !hasCurrent || !hasPreimageSummary ||
			record.VisibleFrom == 0 ||
			record.VisibleFrom > record.SnapshotRevision ||
			preimageSummaries[snapshotKey].Repository != record.Repository ||
			!validServiceStateV3PreimageRecord(
				record.RecID,
				"service_state_v3_preimage",
			) || rowKey <= priorPreimageKey ||
			state.DesiredCatalogGeneration != "" &&
				(roots[state.DesiredCatalogGeneration] != serviceCatalogV3Historical ||
					rootRepositories[state.DesiredCatalogGeneration] != state.Repository) ||
			state.ActiveCatalogGeneration != "" &&
				(roots[state.ActiveCatalogGeneration] != serviceCatalogV3Historical ||
					rootRepositories[state.ActiveCatalogGeneration] != state.Repository) {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		priorPreimageKey = rowKey
	}
	selectors, err := s.ListServiceRuntimeSelectors(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	selectedByRepository := make(map[string]ServiceRuntimeSelector, len(selectors))
	selectedStates := make(map[string]map[string]servicecatalog.ServiceState, len(selectors))
	for _, selector := range selectors {
		if selector.Backend != ServiceRuntimeV3 {
			continue
		}
		selectedByRepository[selector.Repository] = selector
		selectedStates[selector.Repository] = make(map[string]servicecatalog.ServiceState)
	}
	for _, record := range rows {
		selector, selected := selectedByRepository[record.Repository]
		if !selected || record.VisibleFrom > selector.StateControlRevision {
			continue
		}
		state, stateErr := serviceStateV3FromRec(record)
		if stateErr != nil || selectedStates[record.Repository][state.ServiceKey].Repository != "" {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		selectedStates[record.Repository][state.ServiceKey] = *state
	}
	for _, record := range preimageRows {
		selector, selected := selectedByRepository[record.Repository]
		if !selected || record.SnapshotRevision != selector.StateControlRevision ||
			record.SnapshotDigest != selector.StateSummaryDigest {
			continue
		}
		state, stateErr := serviceStateV3FromRec(record)
		if stateErr != nil || selectedStates[record.Repository][state.ServiceKey].Repository != "" {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		selectedStates[record.Repository][state.ServiceKey] = *state
	}
	for repository, selector := range selectedByRepository {
		summary := preimageSummaries[serviceStateV3SnapshotKey(
			repository,
			selector.StateControlRevision,
			selector.StateSummaryDigest,
		)]
		if summary.Repository != repository {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		var snapshotCounts serviceStateV3Counts
		for _, state := range selectedStates[repository] {
			if state.Removed {
				snapshotCounts.Tombstones++
				continue
			}
			snapshotCounts.Live++
			switch state.Status {
			case servicecatalog.StatusCurrent:
				snapshotCounts.Current++
			case servicecatalog.StatusStale:
				snapshotCounts.Stale++
			case servicecatalog.StatusUnavailable:
				snapshotCounts.Unavailable++
			case servicecatalog.StatusConflict:
				snapshotCounts.Conflict++
			default:
				return 0, 0, 0, ErrInvalidServiceStateV3
			}
		}
		if !snapshotCounts.matchesSummary(summary) {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
	}
	const maxPlans = MaxServiceCatalogV3LifecycleRoots * 6
	planResults, err := surrealdb.Query[[]serviceStateV3PlanRec](ctx, s.db, `
SELECT * FROM service_state_v3_plan ORDER BY digest LIMIT $limit`, map[string]any{
		"limit": maxPlans + 1,
	})
	if err != nil {
		return 0, 0, 0, err
	}
	planRows := firstDomainRows(planResults)
	if len(planRows) > maxPlans {
		return 0, 0, 0, ErrInvalidServiceStateV3
	}
	scheduleResults, err := surrealdb.Query[[]generationScheduleRec](ctx, s.db, `
SELECT * FROM generation_schedule
	WHERE stage = $reconcile OR stage = $activate ORDER BY digest LIMIT $limit`, map[string]any{
		"reconcile": ServiceStateV3ReconcileStage,
		"activate":  ServiceStateV3ActivateStage, "limit": maxPlans + 1,
	})
	if err != nil {
		return 0, 0, 0, err
	}
	scheduleRows := firstDomainRows(scheduleResults)
	if len(scheduleRows) > maxPlans {
		return 0, 0, 0, ErrInvalidServiceStateV3
	}
	schedules := make(map[string]GenerationSchedule, len(scheduleRows))
	for _, record := range scheduleRows {
		schedule := record.schedule()
		if ValidateGenerationSchedule(schedule) != nil {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		schedules[schedule.Digest] = schedule
	}
	currentResults, err := surrealdb.Query[[]serviceStateV3CurrentScheduleRec](ctx, s.db, `
SELECT repository, stage, schedule_digest FROM generation_schedule_current
	WHERE stage = $reconcile OR stage = $activate ORDER BY repository, stage LIMIT $limit`, map[string]any{
		"reconcile": ServiceStateV3ReconcileStage,
		"activate":  ServiceStateV3ActivateStage,
		"limit":     MaxServiceCatalogV3LifecycleRoots*2 + 1,
	})
	if err != nil {
		return 0, 0, 0, err
	}
	currentRows := firstDomainRows(currentResults)
	if len(currentRows) > MaxServiceCatalogV3LifecycleRoots*2 {
		return 0, 0, 0, ErrInvalidServiceStateV3
	}
	currents := make(map[string]string, len(currentRows))
	for _, current := range currentRows {
		key := current.Repository + "\x00" + current.Stage
		if !validSHA256Digest(current.ScheduleDigest) || currents[key] != "" {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		currents[key] = current.ScheduleDigest
	}
	runningReconcile := make(map[string]bool)
	for _, record := range planRows {
		plan := record.plan()
		expectedID := strings.TrimPrefix(plan.Digest, "sha256:")
		schedule, ok := schedules[plan.ScheduleDigest]
		if validateServiceStateV3Plan(plan) != nil ||
			!validServiceCatalogV3RecordID(record.RecID, "service_state_v3_plan", expectedID) ||
			roots[plan.CatalogRoot] != serviceCatalogV3Historical ||
			rootRepositories[plan.CatalogRoot] != plan.Repository || !ok ||
			schedule.Generation != plan.Digest || schedule.Repository != plan.Repository ||
			schedule.Stage != serviceStateV3Stage(plan.Phase) {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		if plan.State == serviceStateV3Running {
			key := plan.Repository + "\x00" + schedule.Stage
			if schedule.Status != GenerationScheduleActive ||
				currents[key] != schedule.Digest || !counts[plan.Repository].matchesPlan(plan) {
				return 0, 0, 0, ErrInvalidServiceStateV3
			}
			if plan.Phase == serviceStateV3Reconcile {
				runningReconcile[plan.Repository] = true
			}
		}
	}
	for _, record := range rows {
		summary, present := summaries[record.Repository]
		if present && record.VisibleFrom > summary.ControlRevision &&
			!runningReconcile[record.Repository] {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
	}
	for repository, summary := range summaries {
		strictCurrent := summary.CatalogGeneration == candidateRoots[repository]
		if strictCurrent && !runningReconcile[repository] &&
			!counts[repository].matchesSummary(summary) {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
	}
	for repository, count := range counts {
		if _, ok := summaries[repository]; !ok && candidateRoots[repository] == "" &&
			!runningReconcile[repository] &&
			(count.Live != 0 || count.Tombstones != 0) {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
	}
	return len(rows) + len(preimageRows),
		len(summaryRows) + len(preimageSummaryRows), len(planRows), nil
}

func serviceStateV3SnapshotKey(
	repository string,
	snapshotRevision uint64,
	snapshotDigest string,
) string {
	return fmt.Sprintf("%s\x00%020d\x00%s", repository, snapshotRevision, snapshotDigest)
}
