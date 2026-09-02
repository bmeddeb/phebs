package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

const (
	MaxServiceStateV3ChunkRows = 512

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
	if err := s.createServiceStateV3Plan(ctx, plan, prior); err != nil {
		existing, readErr := s.getServiceStateV3Plan(ctx, plan.Digest)
		if readErr == nil && existing.ScheduleDigest == schedule.Digest &&
			existing.Repository == plan.Repository && existing.Phase == plan.Phase &&
			existing.CatalogRoot == plan.CatalogRoot &&
			existing.CatalogControlRevision == plan.CatalogControlRevision &&
			existing.SearchGeneration == plan.SearchGeneration &&
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

func (s *Surreal) createServiceStateV3Plan(
	ctx context.Context,
	plan ServiceStateV3Plan,
	prior *ServiceStateV3Plan,
) error {
	if validateServiceStateV3Plan(plan) != nil {
		return ErrInvalidServiceStateV3
	}
	priorRID := models.NewRecordID("service_state_v3_plan", "absent")
	priorDigest := ""
	if prior != nil {
		priorRID = serviceStateV3PlanID(prior.Digest)
		priorDigest = prior.Digest
	}
	results, err := surrealdb.Query[[]serviceStateV3PlanRec](ctx, s.db, `
BEGIN;
LET $candidate = (SELECT root_digest, control_revision FROM $candidate_rid LIMIT 1)[0];
LET $current = (SELECT schedule_digest FROM $schedule_current LIMIT 1)[0].schedule_digest;
LET $schedule = (SELECT digest, generation, status FROM $schedule_rid LIMIT 1)[0];
LET $existing = (SELECT id FROM $plan_rid LIMIT 1)[0].id;
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
		updates := make([]serviceStateUpdate, 0, len(projections))
		nextPlan := plan
		now := storeTimestamp(time.Now())
		for _, projection := range projections {
			prior, exists := existing[projection.Service.Key]
			next, changed, projectErr := projectServiceStateV3(
				projection, prior, exists, now,
			)
			if projectErr != nil {
				return ServiceStateV3ChunkResult{}, projectErr
			}
			if err := applyServiceStateV3CountDelta(&nextPlan, prior, exists, next); err != nil {
				return ServiceStateV3ChunkResult{}, err
			}
			if changed {
				update := serviceStateUpdate{State: next}
				if exists {
					update.ExpectedRevision = prior.ControlRevision
					update.ExpectedDigest = prior.StateDigest
				}
				updates = append(updates, update)
			}
		}
		nextPlan.NextChunk++
		advanceServiceStateV3Metrics(&nextPlan, len(projections), updates)
		if err := s.commitServiceStateV3Chunk(
			ctx, chunk, plan, nextPlan, updates, nil, nil, false,
		); err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		return ServiceStateV3ChunkResult{
			Applied: len(updates), Read: len(projections),
		}, nil
	}
	removalEnd := plan.ServiceMemberChunks + plan.RemovalChunks
	if logicalChunk < removalEnd {
		rows, err := s.nextServiceStateV3RemovalRows(ctx, plan)
		if err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		updates := make([]serviceStateUpdate, 0, len(rows))
		nextPlan := plan
		now := storeTimestamp(time.Now())
		present, err := s.serviceStateV3PresentKeys(ctx, opened.Root, rows)
		if err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		for _, prior := range rows {
			nextPlan.RemovalCursor = prior.ServiceKey
			if present[prior.ServiceKey] {
				continue
			}
			next, removalErr := implicitRemovedServiceStateV3(prior, now)
			if removalErr != nil {
				return ServiceStateV3ChunkResult{}, removalErr
			}
			if err := applyServiceStateV3CountDelta(&nextPlan, prior, true, next); err != nil {
				return ServiceStateV3ChunkResult{}, err
			}
			updates = append(updates, serviceStateUpdate{
				State: next, ExpectedRevision: prior.ControlRevision,
				ExpectedDigest: prior.StateDigest,
			})
		}
		nextPlan.NextChunk++
		advanceServiceStateV3Metrics(&nextPlan, len(rows), updates)
		if err := s.commitServiceStateV3Chunk(
			ctx, chunk, plan, nextPlan, updates, nil, nil, false,
		); err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		return ServiceStateV3ChunkResult{Applied: len(updates), Read: len(rows)}, nil
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
	updates := make([]serviceStateUpdate, 0, len(projections))
	nextPlan := plan
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
		if err := applyServiceStateV3CountDelta(&nextPlan, state, true, next); err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		updates = append(updates, serviceStateUpdate{
			State: next, ExpectedRevision: state.ControlRevision,
			ExpectedDigest: state.StateDigest,
		})
	}
	nextPlan.NextChunk++
	advanceServiceStateV3Metrics(&nextPlan, len(projections), updates)
	var nextSummary *servicecatalog.RepositoryState
	if len(updates) != 0 {
		candidateSummary := *summary
		candidateSummary.LiveServiceCount = nextPlan.LiveServiceCount
		candidateSummary.CurrentCount = nextPlan.CurrentCount
		candidateSummary.StaleCount = nextPlan.StaleCount
		candidateSummary.UnavailableCount = nextPlan.UnavailableCount
		candidateSummary.ConflictCount = nextPlan.ConflictCount
		candidateSummary.TombstoneCount = nextPlan.TombstoneCount
		candidateSummary.ControlRevision++
		candidateSummary.UpdatedAt = now
		if err := servicecatalogv3.SetRepositoryStateDigest(&candidateSummary); err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		if err := servicecatalogv3.ValidateRepositoryState(candidateSummary, true); err != nil {
			return ServiceStateV3ChunkResult{}, err
		}
		nextPlan.SummaryControlRevision = candidateSummary.ControlRevision
		nextPlan.SummaryDigest = candidateSummary.SummaryDigest
		nextSummary = &candidateSummary
	}
	if err := s.commitServiceStateV3Chunk(
		ctx, chunk, plan, nextPlan, updates, summary, nextSummary, false,
	); err != nil {
		return ServiceStateV3ChunkResult{}, err
	}
	return ServiceStateV3ChunkResult{Applied: len(updates), Read: len(projections)}, nil
}

func (s *Surreal) serviceCatalogV3MemberContent(
	ctx context.Context,
	descriptor servicecatalogv3.MemberDescriptor,
) ([]byte, error) {
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

func (s *Surreal) commitServiceStateV3Chunk(
	ctx context.Context,
	chunk GenerationChunk,
	priorPlan, nextPlan ServiceStateV3Plan,
	updates []serviceStateUpdate,
	expectedSummary *servicecatalog.RepositoryState,
	nextSummary *servicecatalog.RepositoryState,
	requireRemovalDrain bool,
) error {
	if validateServiceStateV3Plan(priorPlan) != nil ||
		validateServiceStateV3Plan(nextPlan) != nil ||
		priorPlan.Digest != nextPlan.Digest ||
		priorPlan.ScheduleDigest != nextPlan.ScheduleDigest ||
		len(updates) > MaxServiceStateV3ChunkRows {
		return ErrInvalidServiceStateV3
	}
	if expectedSummary != nil &&
		(expectedSummary.ControlRevision != priorPlan.SummaryControlRevision ||
			expectedSummary.SummaryDigest != priorPlan.SummaryDigest) {
		return ErrInvalidServiceStateV3
	}
	encodedUpdates := make([]map[string]any, 0, len(updates))
	for _, update := range updates {
		if update.State.Repository != priorPlan.Repository ||
			servicecatalogv3.ValidateServiceState(update.State, true) != nil {
			return ErrInvalidServiceStateV3
		}
		encodedUpdates = append(encodedUpdates, map[string]any{
			"rid":               serviceStateV3ID(update.State.Repository, update.State.ServiceKey),
			"expected_revision": update.ExpectedRevision,
			"expected_digest":   update.ExpectedDigest,
			"content":           serviceStateContent(update.State),
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
LET $summary = (SELECT summary_digest, control_revision FROM $summary_rid LIMIT 1)[0];
LET $summary_ok = IF $expected_summary_revision = 0 THEN $summary = NONE
	ELSE $summary != NONE AND $summary.control_revision = $expected_summary_revision
		AND $summary.summary_digest = $expected_summary_digest END;
LET $plan_ok = $plan != NONE AND $plan.digest = $plan_digest
	AND $plan.schedule_digest = $schedule_digest AND $plan.state = 'running'
	AND $plan.next_chunk = $expected_next_chunk
	AND $plan.tombstone_count = $expected_tombstones
	AND $plan.summary_control_revision = $expected_summary_revision
	AND $plan.summary_digest = $expected_summary_digest;
LET $lease_ok = $chunk != NONE AND $chunk.identity = $chunk_identity
	AND $chunk.schedule_digest = $schedule_digest AND $chunk.repository = $repository
	AND $chunk.stage = $stage AND $chunk.generation = $plan_digest
	AND $chunk.status = 'running' AND $chunk.attempt = $attempt
	AND $chunk.lease_token = $lease AND $chunk.claimed_by = $worker;
LET $drained = !$require_removal_drain OR array::len(
	SELECT id FROM service_state_v3_current
		WHERE repository = $repository AND removed = false
			AND service_key > $removal_cursor LIMIT 1
) = 0;
IF $repository_state = NONE OR $repository_state.deleting = true OR
	$candidate = NONE OR $candidate.root_digest != $catalog_root OR
	$candidate.control_revision != $catalog_revision OR $current != $schedule_digest OR
	$schedule = NONE OR $schedule.digest != $schedule_digest OR
	$schedule.generation != $plan_digest OR $schedule.status != 'active' OR
	!$plan_ok OR !$lease_ok OR !$summary_ok OR !$drained {
	THROW 'phebs-permanent: service state v3 chunk fence changed';
};
FOR $update IN $updates {
	LET $existing = (SELECT state_digest, control_revision FROM $update.rid LIMIT 1)[0];
	LET $revision = IF $existing = NONE THEN 0 ELSE $existing.control_revision END;
	LET $digest = IF $existing = NONE THEN '' ELSE $existing.state_digest END;
	IF $revision != $update.expected_revision OR $digest != $update.expected_digest {
		THROW 'phebs-permanent: service state v3 row compare-and-swap conflict';
	};
	UPSERT $update.rid CONTENT $update.content RETURN NONE;
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
		"chunk_rid":        models.NewRecordID("generation_schedule_chunk", chunk.ID),
		"plan_rid":         serviceStateV3PlanID(priorPlan.Digest),
		"summary_rid":      serviceStateV3RepositoryID(priorPlan.Repository),
		"catalog_root":     priorPlan.CatalogRoot,
		"catalog_revision": priorPlan.CatalogControlRevision,
		"schedule_digest":  priorPlan.ScheduleDigest, "plan_digest": priorPlan.Digest,
		"repository": priorPlan.Repository, "stage": chunk.Stage,
		"chunk_identity": chunk.Identity, "attempt": chunk.Attempt,
		"lease": chunk.LeaseToken, "worker": chunk.ClaimedBy,
		"expected_next_chunk":       priorPlan.NextChunk,
		"expected_tombstones":       priorPlan.TombstoneCount,
		"expected_summary_revision": priorPlan.SummaryControlRevision,
		"expected_summary_digest":   priorPlan.SummaryDigest,
		"require_removal_drain":     requireRemovalDrain,
		"removal_cursor":            priorPlan.RemovalCursor,
		"updates":                   encodedUpdates, "write_summary": writeSummary,
		"summary_content": summaryContent,
		"plan_content":    serviceStateV3PlanContent(nextPlan),
	})
	if err != nil {
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
	for _, record := range rows {
		state, stateErr := serviceStateV3FromRec(record)
		if stateErr != nil {
			return 0, 0, 0, ErrInvalidServiceStateV3
		}
		expected := serviceStateV3ID(state.Repository, state.ServiceKey)
		expectedID, _ := expected.ID.(string)
		if !validServiceCatalogV3RecordID(
			record.RecID, "service_state_v3_current", expectedID,
		) || state.DesiredCatalogGeneration != "" &&
			roots[state.DesiredCatalogGeneration] != serviceCatalogV3Historical ||
			state.ActiveCatalogGeneration != "" &&
				roots[state.ActiveCatalogGeneration] != serviceCatalogV3Historical {
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
			roots[plan.CatalogRoot] != serviceCatalogV3Historical || !ok ||
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
	return len(rows), len(summaryRows), len(planRows), nil
}
