package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const (
	GenerationScheduleSchema                     = "phebs-generation-schedule-v1"
	MaxGenerationStageBytes                      = 64
	MaxGenerationItems                     int64 = 80_000_000
	MaxGenerationChunkItems                      = 4_096
	MaxGenerationChunks                          = 1_000_000
	MaxGenerationFanoutPage                      = 64
	MaxGenerationClaimCandidates                 = 64
	MaxGenerationActiveStagesPerRepository       = 8
	maxGenerationScheduleClaimCandidates         = MaxGenerationClaimCandidates * MaxGenerationActiveStagesPerRepository
	MaxGenerationAttempts                        = 8
	ServiceRelationshipV3ScheduleStage           = "service-relationship-v3-shadow"
	MaxGenerationRepositoryTokens                = 8
	MaxGenerationWorkerBytes                     = 256
	MaxGenerationErrorBytes                      = 2_048
)

var (
	ErrGenerationStale     = errors.New("generation schedule is stale")
	ErrGenerationLeaseLost = errors.New("generation chunk lease lost")
	ErrGenerationExhausted = errors.New("generation chunk attempts exhausted")
)

const generationResourceClassMigrationVersion = "t40.10-generation-resource-class-v1"

type generationResourceClassMigrationState struct {
	Version string `json:"version"`
}

func generationResourceClassMigrationID() models.RecordID {
	return models.NewRecordID("store_migration", "generation_resource_class")
}

// migrateGenerationResourceClasses adds the extraction-only worker class to
// stores created before partition execution was wired into the production
// scheduler. The completion marker keeps steady-state startup to one point
// read and prevents an unknown future schema generation from being replaced.
func (s *Surreal) migrateGenerationResourceClasses(ctx context.Context) error {
	complete, err := s.generationResourceClassMigrationComplete(ctx)
	if err != nil {
		return fmt.Errorf("migrate generation resource classes: %w", err)
	}
	if complete {
		return nil
	}
	results, err := surrealdb.Query[any](ctx, s.db, `
DEFINE FIELD OVERWRITE resource_class ON generation_schedule TYPE string
	ASSERT $value INSIDE ['cpu', 'io', 'memory', 'extraction'];
DEFINE FIELD OVERWRITE resource_class ON generation_schedule_chunk TYPE string
	ASSERT $value INSIDE ['cpu', 'io', 'memory', 'extraction'];
UPSERT $marker SET version = $version, completed_at = time::now() RETURN NONE;`, map[string]any{
		"marker":  generationResourceClassMigrationID(),
		"version": generationResourceClassMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("migrate generation resource classes: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate generation resource classes statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	complete, err = s.generationResourceClassMigrationComplete(ctx)
	if err != nil {
		return fmt.Errorf("migrate generation resource classes: %w", err)
	}
	if !complete {
		return errors.New("migrate generation resource classes: completion marker missing")
	}
	return nil
}

func (s *Surreal) generationResourceClassMigrationComplete(ctx context.Context) (bool, error) {
	results, err := surrealdb.Query[[]generationResourceClassMigrationState](
		ctx, s.db, "SELECT version FROM $marker",
		map[string]any{"marker": generationResourceClassMigrationID()},
	)
	if err != nil {
		return false, err
	}
	for index, result := range *results {
		if result.Error != nil {
			return false, fmt.Errorf("read marker statement %d: %s", index, result.Error.Message)
		}
		for _, row := range result.Result {
			if row.Version == generationResourceClassMigrationVersion {
				return true, nil
			}
			if row.Version != "" {
				return false, fmt.Errorf("unsupported marker %q", row.Version)
			}
		}
	}
	return false, nil
}

// ClearAllGenerationScheduleStateForRestore discards imported restartable
// scheduler controls and extraction-domain roots without decoding them. Backup
// archives intentionally omit the filesystem generations and bindings those
// rows address, so retaining the rows would let an active schedule or current
// domain pointer permanently mask the required rebuild. Downstream job
// history remains precious, but its repo projections belong to this discarded
// control epoch and stay unavailable until an exact successor writer rebinds
// them. Immutable outcomes remain available for exact reuse by the successor
// schedule.
func (s *Surreal) ClearAllGenerationScheduleStateForRestore(ctx context.Context) error {
	results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
UPDATE repo UNSET
	latest_extraction_job, latest_extraction_job_created_at,
	latest_extraction_job_projection_version,
	latest_resolver_job, latest_resolver_job_created_at,
	latest_resolver_job_projection_version,
	latest_caller_job, latest_caller_job_created_at,
	latest_caller_job_projection_version
	RETURN NONE;
DELETE generation_schedule_chunk RETURN NONE;
DELETE generation_schedule_current RETURN NONE;
DELETE generation_schedule_repository RETURN NONE;
DELETE generation_schedule RETURN NONE;
DELETE service_state_v3_plan RETURN NONE;
DELETE extraction_domain_root RETURN NONE;
COMMIT;`, nil)
	if err != nil {
		return fmt.Errorf("clear generation schedules for restore: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"clear generation schedules for restore statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	return nil
}

type GenerationResourceClass string

const (
	GenerationResourceCPU    GenerationResourceClass = "cpu"
	GenerationResourceIO     GenerationResourceClass = "io"
	GenerationResourceMemory GenerationResourceClass = "memory"
	// Extraction is isolated from the generic CPU queue because handlers are
	// stage-specific; sharing CPU would let an observation worker claim and
	// terminally reject a partitioned-extraction chunk.
	GenerationResourceExtraction GenerationResourceClass = "extraction"
)

type GenerationScheduleStatus string

const (
	GenerationScheduleActive     GenerationScheduleStatus = "active"
	GenerationScheduleSuperseded GenerationScheduleStatus = "superseded"
	GenerationScheduleSettled    GenerationScheduleStatus = "settled"
)

type GenerationChunkStatus string

const (
	GenerationChunkPending  GenerationChunkStatus = "pending"
	GenerationChunkRunning  GenerationChunkStatus = "running"
	GenerationChunkDone     GenerationChunkStatus = "done"
	GenerationChunkFailed   GenerationChunkStatus = "failed"
	GenerationChunkCanceled GenerationChunkStatus = "canceled"
)

const (
	GenerationPriorityNeverRun = 0
	GenerationPriorityRetry    = 1
	GenerationPriorityStale    = 2
)

type GenerationScheduleSpec struct {
	Repository       string
	Stage            string
	Generation       string
	ResourceClass    GenerationResourceClass
	TotalItems       int64
	ChunkItems       int
	MaxAttempts      int
	RepositoryTokens int
}

type GenerationSchedule struct {
	Schema           string                   `json:"schema"`
	Digest           string                   `json:"digest"`
	Repository       string                   `json:"repository"`
	Stage            string                   `json:"stage"`
	Generation       string                   `json:"generation"`
	ResourceClass    GenerationResourceClass  `json:"resource_class"`
	TotalItems       int64                    `json:"total_items"`
	ChunkItems       int                      `json:"chunk_items"`
	TotalChunks      int                      `json:"total_chunks"`
	MaxAttempts      int                      `json:"max_attempts"`
	RepositoryTokens int                      `json:"repository_tokens"`
	NextOffset       int64                    `json:"next_offset"`
	Materialized     int                      `json:"materialized"`
	Pending          int                      `json:"pending"`
	Running          int                      `json:"running"`
	Succeeded        int                      `json:"succeeded"`
	Failed           int                      `json:"failed"`
	Status           GenerationScheduleStatus `json:"status"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
	LastClaimedAt    *time.Time               `json:"last_claimed_at,omitempty"`
}

type GenerationChunk struct {
	ID             string                  `json:"id"`
	Identity       string                  `json:"identity"`
	ScheduleDigest string                  `json:"schedule_digest"`
	Repository     string                  `json:"repository"`
	Stage          string                  `json:"stage"`
	Generation     string                  `json:"generation"`
	ResourceClass  GenerationResourceClass `json:"resource_class"`
	Offset         int64                   `json:"offset"`
	Length         int                     `json:"length"`
	Attempt        int                     `json:"attempt"`
	Priority       int                     `json:"priority"`
	Status         GenerationChunkStatus   `json:"status"`
	NotBefore      *time.Time              `json:"not_before,omitempty"`
	ClaimedBy      string                  `json:"claimed_by,omitempty"`
	ClaimedAt      *time.Time              `json:"claimed_at,omitempty"`
	HeartbeatAt    *time.Time              `json:"heartbeat_at,omitempty"`
	FinishedAt     *time.Time              `json:"finished_at,omitempty"`
	Error          string                  `json:"error,omitempty"`
	LeaseToken     string                  `json:"-" cbor:"lease_token,omitempty"`
}

// GenerationChunkLeaseState is the source-free authoritative projection used
// to prove one exact scheduler attempt's lease state. Priority plus the
// presence of a lease distinguish an untouched/reclaimed row from the stale
// pending shape written by release, deferral, or reaping. Lease tokens,
// worker identities, timestamps, and error details never leave the store.
type GenerationChunkLeaseState struct {
	Identity       string                `json:"identity"`
	ScheduleDigest string                `json:"schedule_digest"`
	Repository     string                `json:"repository"`
	Stage          string                `json:"stage"`
	Generation     string                `json:"generation"`
	Attempt        int                   `json:"attempt"`
	Priority       int                   `json:"priority"`
	Status         GenerationChunkStatus `json:"status"`
	Leased         bool                  `json:"leased"`
}

// GenerationScheduleRetentionState is the bounded source-free projection used
// to distinguish a collected historical schedule from a still-current one.
// A missing exact schedule is reported with Present false; Current is fenced
// by identical current-pointer reads around the exact-digest lookup.
type GenerationScheduleRetentionState struct {
	ScheduleDigest string                   `json:"schedule_digest"`
	Present        bool                     `json:"present"`
	CurrentPresent bool                     `json:"current_present"`
	Current        bool                     `json:"current"`
	Status         GenerationScheduleStatus `json:"status,omitempty"`
}

// GenerationScheduleFailure is the bounded source-free projection of the
// final failed attempt for one exact settled schedule. Raw durable error text,
// repository paths, worker identity, and lease state never leave the store.
type GenerationScheduleFailure struct {
	ScheduleDigest string                   `json:"schedule_digest"`
	Generation     string                   `json:"generation"`
	Attempt        int                      `json:"attempt"`
	Refusal        *pipelinerefusal.Receipt `json:"refusal,omitempty"`
}

const GenerationScheduleProgressSchema = "phebs-generation-schedule-progress-v1"

// GenerationScheduleProgress is the bounded, source-free current-schedule
// projection used by local ceremony diagnostics. It excludes repository,
// stage, timestamps, worker identity, lease state, and raw durable errors.
type GenerationScheduleProgress struct {
	Schema         string                     `json:"schema"`
	ScheduleDigest string                     `json:"schedule_digest"`
	Generation     string                     `json:"generation"`
	Status         GenerationScheduleStatus   `json:"status"`
	Total          int                        `json:"total"`
	Materialized   int                        `json:"materialized"`
	Pending        int                        `json:"pending"`
	Running        int                        `json:"running"`
	Succeeded      int                        `json:"succeeded"`
	Failed         int                        `json:"failed"`
	MaxAttempts    int                        `json:"max_attempts"`
	Failure        *GenerationScheduleFailure `json:"failure,omitempty"`
}

// LocalGenerationChunkReader connects to the already supervised local store
// without applying schema or migrations. It exposes only bounded source-free
// lease/schedule diagnostic projections and cannot mutate scheduler state.
type LocalGenerationChunkReader struct {
	store   *Surreal
	dataDir string
	token   string
}

type generationDiagnosticFenceRec struct {
	Fenced bool `json:"fenced"`
}

func OpenLocalGenerationChunkReader(
	ctx context.Context,
	dataDir string,
) (*LocalGenerationChunkReader, error) {
	if ctx == nil || !filepath.IsAbs(dataDir) {
		return nil, errors.New("open local generation chunk reader: data directory is invalid")
	}
	runtime, err := ReadLocalRuntime(dataDir)
	if err != nil || !processAlive(runtime.PID) {
		return nil, errors.Join(err, errors.New("open local generation chunk reader: runtime is unavailable"))
	}
	db, err := surrealdb.FromEndpointURLString(ctx, runtime.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("open local generation chunk reader: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = db.Close(context.Background())
		}
	}()
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"}); err != nil {
		return nil, fmt.Errorf("sign in local generation chunk reader: %w", err)
	}
	if err := db.Use(ctx, "phebs", "phebs"); err != nil {
		return nil, fmt.Errorf("select local generation chunk reader database: %w", err)
	}
	confirmed, err := ReadLocalRuntime(dataDir)
	if err != nil || confirmed.Token != runtime.Token || confirmed.PID != runtime.PID ||
		confirmed.Endpoint != runtime.Endpoint || !processAlive(confirmed.PID) {
		return nil, errors.Join(err, errors.New("open local generation chunk reader: runtime changed"))
	}
	failed = false
	return &LocalGenerationChunkReader{
		store: &Surreal{db: db}, dataDir: dataDir, token: runtime.Token,
	}, nil
}

func (reader *LocalGenerationChunkReader) GenerationChunkLeaseState(
	ctx context.Context,
	identity string,
) (GenerationChunkLeaseState, error) {
	if reader == nil || reader.store == nil || ctx == nil || !validSHA256(identity) {
		return GenerationChunkLeaseState{}, errors.New("read generation chunk lease state: request is invalid")
	}
	chunk, err := reader.store.generationChunkByIdentity(ctx, identity)
	if err != nil {
		return GenerationChunkLeaseState{}, err
	}
	if err := reader.confirmRuntime(); err != nil {
		return GenerationChunkLeaseState{}, errors.Join(
			err, errors.New("read generation chunk lease state: runtime changed"),
		)
	}
	return GenerationChunkLeaseState{
		Identity: chunk.Identity, ScheduleDigest: chunk.ScheduleDigest,
		Repository: chunk.Repository, Stage: chunk.Stage, Generation: chunk.Generation,
		Attempt: chunk.Attempt, Priority: chunk.Priority, Status: chunk.Status,
		Leased: chunk.LeaseToken != "",
	}, nil
}

// GenerationScheduleRetentionState reports whether one exact selected
// schedule is still retained and whether it remains current. The current
// pointer is read before and after the exact-digest lookup so a concurrent
// supersession or promotion cannot be mistaken for collection.
func (reader *LocalGenerationChunkReader) GenerationScheduleRetentionState(
	ctx context.Context,
	repository, stage, scheduleDigest string,
) (GenerationScheduleRetentionState, error) {
	if reader == nil || reader.store == nil || ctx == nil ||
		strings.TrimSpace(repository) != repository || repository == "" ||
		!validGenerationToken(stage) || stage == "" || !validSHA256(scheduleDigest) {
		return GenerationScheduleRetentionState{}, errors.New(
			"read generation schedule retention state: request is invalid",
		)
	}
	currentDigest, err := reader.currentGenerationScheduleDigest(ctx, repository, stage)
	if err != nil {
		return GenerationScheduleRetentionState{}, err
	}
	result := GenerationScheduleRetentionState{ScheduleDigest: scheduleDigest}
	schedule, scheduleErr := reader.store.generationScheduleByDigest(ctx, scheduleDigest)
	switch {
	case scheduleErr == nil:
		if err := ValidateGenerationSchedule(*schedule); err != nil ||
			schedule.Repository != repository || schedule.Stage != stage {
			return GenerationScheduleRetentionState{}, errors.Join(
				err, errors.New("read generation schedule retention state: projection is invalid"),
			)
		}
		result.Present = true
		result.Status = schedule.Status
	case errors.Is(scheduleErr, ErrNotFound):
	default:
		return GenerationScheduleRetentionState{}, scheduleErr
	}
	confirmedDigest, err := reader.currentGenerationScheduleDigest(ctx, repository, stage)
	if err != nil {
		return GenerationScheduleRetentionState{}, err
	}
	if confirmedDigest != currentDigest {
		return GenerationScheduleRetentionState{}, ErrGenerationStale
	}
	result.Current = currentDigest == scheduleDigest
	result.CurrentPresent = currentDigest != ""
	if err := reader.confirmRuntime(); err != nil {
		return GenerationScheduleRetentionState{}, errors.Join(
			err, errors.New("read generation schedule retention state: runtime changed"),
		)
	}
	return result, nil
}

func (reader *LocalGenerationChunkReader) currentGenerationScheduleDigest(
	ctx context.Context,
	repository, stage string,
) (string, error) {
	schedule, err := reader.store.GetGenerationSchedule(ctx, repository, stage)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if schedule == nil {
		return "", errors.New("read current generation schedule digest: nil schedule")
	}
	return schedule.Digest, nil
}

// FenceCurrentGenerationScheduleForDiagnostic removes the exact current
// pointer beneath one observed running chunk while leaving its lease intact.
// It exists only for the source-free stale-worker ceremony: the worker's
// ordinary completion transaction must subsequently observe the missing
// current pointer and settle the selected lease as stale. Repository recovery
// then installs the real successor through normal production reconciliation.
func (reader *LocalGenerationChunkReader) FenceCurrentGenerationScheduleForDiagnostic(
	ctx context.Context,
	selected GenerationChunkLeaseState,
) error {
	if reader == nil || reader.store == nil || ctx == nil ||
		!validSHA256(selected.Identity) || selected.Repository == "" ||
		!validGenerationToken(selected.Stage) || !validSHA256(selected.Generation) ||
		selected.Attempt < 0 || selected.Status != GenerationChunkRunning {
		return errors.New("fence current generation schedule for diagnostic: request is invalid")
	}
	results, err := queryGenerationSchedule[[]generationDiagnosticFenceRec](
		ctx, reader.store.db, "diagnostic_fence", `
BEGIN;
LET $chunk = (SELECT schedule_digest FROM generation_schedule_chunk
	WHERE identity = $identity AND repository = $repository AND stage = $stage
		AND generation = $generation AND attempt = $attempt AND status = 'running'
	LIMIT 1)[0];
LET $current_digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
LET $fenced = $chunk != NONE AND $chunk.schedule_digest = $current_digest;
IF $fenced {
	UPDATE generation_schedule SET status = 'superseded', updated_at = time::now()
		WHERE digest = $current_digest RETURN NONE;
	DELETE $current RETURN NONE;
};
RETURN [{ fenced: $fenced }];
COMMIT;`, map[string]any{
			"identity": selected.Identity, "repository": selected.Repository,
			"stage": selected.Stage, "generation": selected.Generation,
			"attempt": selected.Attempt,
			"current": models.NewRecordID(
				"generation_schedule_current",
				strings.TrimPrefix(generationCurrentID(selected.Repository, selected.Stage), "sha256:"),
			),
		})
	if err != nil {
		return fmt.Errorf("fence current generation schedule for diagnostic: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || !rows[0].Fenced {
		return ErrGenerationStale
	}
	if err := reader.confirmRuntime(); err != nil {
		return errors.Join(err, errors.New("fence current generation schedule for diagnostic: runtime changed"))
	}
	return nil
}

// CurrentGenerationRunningChunk returns one running chunk from the exact
// current repository/stage schedule. The result is re-read by identity and
// fenced against the current schedule before return; lease tokens, workers,
// timestamps, offsets, and errors never leave the store boundary.
func (reader *LocalGenerationChunkReader) CurrentGenerationRunningChunk(
	ctx context.Context,
	repository, stage string,
) (GenerationChunkLeaseState, error) {
	if reader == nil || reader.store == nil || ctx == nil ||
		strings.TrimSpace(repository) != repository || repository == "" ||
		!validGenerationToken(stage) || stage == "" {
		return GenerationChunkLeaseState{}, errors.New(
			"read current running generation chunk: request is invalid",
		)
	}
	results, err := queryGenerationSchedule[[]generationChunkRec](
		ctx, reader.store.db, "current_running_chunk", `
LET $digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
RETURN SELECT * FROM generation_schedule_chunk
	WHERE schedule_digest = $digest AND status = 'running'
	ORDER BY offset, attempt LIMIT 1;`, map[string]any{
			"current": models.NewRecordID(
				"generation_schedule_current",
				strings.TrimPrefix(generationCurrentID(repository, stage), "sha256:"),
			),
		},
	)
	if err != nil {
		return GenerationChunkLeaseState{}, fmt.Errorf(
			"read current running generation chunk: %w", err,
		)
	}
	rows := generationChunkRows(results)
	if len(rows) != 1 {
		return GenerationChunkLeaseState{}, fmt.Errorf(
			"read current running generation chunk: %w", ErrNotFound,
		)
	}
	chunk, err := rows[0].chunk()
	if err != nil || validGenerationChunkLease(chunk) != nil ||
		chunk.Repository != repository || chunk.Stage != stage {
		return GenerationChunkLeaseState{}, errors.Join(
			err, errors.New("read current running generation chunk: projection is invalid"),
		)
	}
	schedule, err := reader.store.GetGenerationSchedule(ctx, repository, stage)
	if err != nil || schedule == nil || schedule.Digest != chunk.ScheduleDigest ||
		schedule.Generation != chunk.Generation {
		return GenerationChunkLeaseState{}, errors.Join(err, ErrGenerationStale)
	}
	state, err := reader.GenerationChunkLeaseState(ctx, chunk.Identity)
	if err != nil || state.Status != GenerationChunkRunning ||
		state.ScheduleDigest != chunk.ScheduleDigest ||
		state.Repository != repository || state.Stage != stage ||
		state.Generation != chunk.Generation || state.Attempt != chunk.Attempt {
		return GenerationChunkLeaseState{}, errors.Join(err, ErrGenerationLeaseLost)
	}
	confirmed, err := reader.store.GetGenerationSchedule(ctx, repository, stage)
	if err != nil || confirmed == nil || confirmed.Digest != chunk.ScheduleDigest ||
		confirmed.Generation != chunk.Generation {
		return GenerationChunkLeaseState{}, errors.Join(err, ErrGenerationStale)
	}
	return state, nil
}

// GenerationScheduleProgress returns one current schedule snapshot. A failed
// settled schedule includes only its closed refusal, when one was durably
// recorded. Every settled result is re-read before return to fence a
// concurrent replacement before a ceremony treats it as terminal.
func (reader *LocalGenerationChunkReader) GenerationScheduleProgress(
	ctx context.Context,
	repository, stage string,
) (GenerationScheduleProgress, error) {
	if reader == nil || reader.store == nil || ctx == nil ||
		strings.TrimSpace(repository) != repository || repository == "" ||
		!validGenerationToken(stage) || stage == "" {
		return GenerationScheduleProgress{}, errors.New(
			"read generation schedule progress: request is invalid",
		)
	}
	schedule, err := reader.store.GetGenerationSchedule(ctx, repository, stage)
	if err != nil {
		return GenerationScheduleProgress{}, err
	}
	if schedule == nil {
		return GenerationScheduleProgress{}, errors.New(
			"read generation schedule progress: nil schedule",
		)
	}
	progress := scheduleProgress(schedule)
	if schedule.Status == GenerationScheduleSettled {
		if schedule.Failed > 0 {
			failure, failureErr := reader.store.GetGenerationScheduleFailure(
				ctx, repository, stage, schedule.Digest,
			)
			if failureErr != nil {
				return GenerationScheduleProgress{}, failureErr
			}
			progress.Failure = failure
		}
		confirmed, confirmErr := reader.store.GetGenerationSchedule(ctx, repository, stage)
		if confirmErr != nil || confirmed == nil ||
			scheduleProgress(confirmed) != scheduleProgress(schedule) {
			return GenerationScheduleProgress{}, errors.Join(
				confirmErr, ErrGenerationStale,
			)
		}
	}
	if err := reader.confirmRuntime(); err != nil {
		return GenerationScheduleProgress{}, errors.Join(
			err, errors.New("read generation schedule progress: runtime changed"),
		)
	}
	return progress, nil
}

// scheduleProgress is both the projection and the settled-recheck fence: the
// confirm read compares fresh projections, so a field added here participates
// in both automatically.
func scheduleProgress(schedule *GenerationSchedule) GenerationScheduleProgress {
	return GenerationScheduleProgress{
		Schema:         GenerationScheduleProgressSchema,
		ScheduleDigest: schedule.Digest, Generation: schedule.Generation,
		Status: schedule.Status, Total: schedule.TotalChunks,
		Materialized: schedule.Materialized, Pending: schedule.Pending,
		Running: schedule.Running, Succeeded: schedule.Succeeded,
		Failed: schedule.Failed, MaxAttempts: schedule.MaxAttempts,
	}
}

func (reader *LocalGenerationChunkReader) confirmRuntime() error {
	runtime, err := ReadLocalRuntime(reader.dataDir)
	if err != nil || runtime.Token != reader.token || !processAlive(runtime.PID) {
		return errors.Join(err, errors.New("local generation reader runtime changed"))
	}
	return nil
}

func (reader *LocalGenerationChunkReader) Close(ctx context.Context) error {
	if reader == nil || reader.store == nil {
		return nil
	}
	err := reader.store.Close(ctx)
	reader.store = nil
	return err
}

type GenerationSchedulerStore interface {
	EnqueueGenerationSchedule(context.Context, GenerationScheduleSpec) (*GenerationSchedule, error)
	ExpandGenerationSchedule(context.Context, string, string, string) (*GenerationSchedule, error)
	ExpandNextGenerationSchedule(context.Context, GenerationResourceClass) (*GenerationSchedule, error)
	ClaimGenerationChunk(context.Context, GenerationResourceClass, string) (*GenerationChunk, error)
	HeartbeatGenerationChunk(context.Context, GenerationChunk) error
	CompleteGenerationChunk(context.Context, GenerationChunk) error
	FailGenerationChunk(context.Context, GenerationChunk, string) error
	RetryGenerationChunk(context.Context, GenerationChunk, string, time.Time) (*GenerationChunk, error)
	DeferGenerationChunk(context.Context, GenerationChunk, string, time.Duration) error
	ReleaseGenerationChunk(context.Context, GenerationChunk, string) error
	ReapStaleGenerationChunks(context.Context, GenerationResourceClass, time.Duration) (int, error)
	GetGenerationSchedule(context.Context, string, string) (*GenerationSchedule, error)
}

var _ GenerationSchedulerStore = (*Surreal)(nil)

func normalizeGenerationScheduleSpec(spec GenerationScheduleSpec) (GenerationScheduleSpec, error) {
	if strings.TrimSpace(spec.Repository) != spec.Repository || spec.Repository == "" ||
		len(spec.Repository) > MaxJobHistoryTargetCharacters || !utf8.ValidString(spec.Repository) {
		return GenerationScheduleSpec{}, errors.New("generation schedule repository is invalid")
	}
	if strings.TrimSpace(spec.Stage) != spec.Stage || spec.Stage == "" ||
		len(spec.Stage) > MaxGenerationStageBytes || !validGenerationToken(spec.Stage) {
		return GenerationScheduleSpec{}, errors.New("generation schedule stage is invalid")
	}
	if !validSHA256(spec.Generation) {
		return GenerationScheduleSpec{}, errors.New("generation schedule identity is invalid")
	}
	if !validGenerationResourceClass(spec.ResourceClass) {
		return GenerationScheduleSpec{}, errors.New("generation schedule resource class is invalid")
	}
	if spec.TotalItems < 1 || spec.TotalItems > MaxGenerationItems {
		return GenerationScheduleSpec{}, fmt.Errorf("generation schedule items must be from 1 through %d", MaxGenerationItems)
	}
	if spec.ChunkItems < 1 || spec.ChunkItems > MaxGenerationChunkItems {
		return GenerationScheduleSpec{}, fmt.Errorf("generation chunk items must be from 1 through %d", MaxGenerationChunkItems)
	}
	if spec.MaxAttempts < 1 || spec.MaxAttempts > MaxGenerationAttempts {
		return GenerationScheduleSpec{}, fmt.Errorf("generation attempts must be from 1 through %d", MaxGenerationAttempts)
	}
	if spec.RepositoryTokens < 1 || spec.RepositoryTokens > MaxGenerationRepositoryTokens {
		return GenerationScheduleSpec{}, fmt.Errorf("generation repository tokens must be from 1 through %d", MaxGenerationRepositoryTokens)
	}
	chunks := (spec.TotalItems + int64(spec.ChunkItems) - 1) / int64(spec.ChunkItems)
	if chunks > MaxGenerationChunks {
		return GenerationScheduleSpec{}, fmt.Errorf("generation schedule has %d chunks, limit %d", chunks, MaxGenerationChunks)
	}
	return spec, nil
}

func validGenerationResourceClass(class GenerationResourceClass) bool {
	switch class {
	case GenerationResourceCPU, GenerationResourceIO, GenerationResourceMemory,
		GenerationResourceExtraction:
		return true
	default:
		return false
	}
}

func validGenerationToken(value string) bool {
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func generationScheduleDigest(spec GenerationScheduleSpec) string {
	return generationSchedulerDigest("phebs-generation-schedule-v1", strings.Join([]string{
		spec.Repository, spec.Stage, spec.Generation, string(spec.ResourceClass),
		fmt.Sprint(spec.TotalItems), fmt.Sprint(spec.ChunkItems),
		fmt.Sprint(spec.MaxAttempts), fmt.Sprint(spec.RepositoryTokens),
	}, "\x00"))
}

// GenerationScheduleDigest returns the immutable identity for one admitted
// schedule specification. Progress readers use it only to re-prove persisted
// rows; it does not create, reactivate, or mutate a schedule.
func GenerationScheduleDigest(spec GenerationScheduleSpec) (string, error) {
	normalized, err := normalizeGenerationScheduleSpec(spec)
	if err != nil {
		return "", err
	}
	return generationScheduleDigest(normalized), nil
}

// ValidateGenerationSchedule proves one bounded current-row projection before
// it is used as progress authority. Chunk errors and worker identities are
// deliberately not part of this source-free schedule summary.
func ValidateGenerationSchedule(schedule GenerationSchedule) error {
	spec, err := normalizeGenerationScheduleSpec(GenerationScheduleSpec{
		Repository: schedule.Repository, Stage: schedule.Stage,
		Generation: schedule.Generation, ResourceClass: schedule.ResourceClass,
		TotalItems: schedule.TotalItems, ChunkItems: schedule.ChunkItems,
		MaxAttempts: schedule.MaxAttempts, RepositoryTokens: schedule.RepositoryTokens,
	})
	if err != nil || schedule.Schema != GenerationScheduleSchema ||
		schedule.Digest != generationScheduleDigest(spec) ||
		schedule.TotalChunks != int((schedule.TotalItems+int64(schedule.ChunkItems)-1)/int64(schedule.ChunkItems)) ||
		schedule.NextOffset < 0 || schedule.NextOffset > schedule.TotalItems ||
		schedule.Materialized < 0 || schedule.Materialized > schedule.TotalChunks*schedule.MaxAttempts ||
		schedule.Pending < 0 || schedule.Running < 0 || schedule.Succeeded < 0 || schedule.Failed < 0 ||
		schedule.Pending+schedule.Running > schedule.Materialized ||
		schedule.Succeeded+schedule.Failed > schedule.TotalChunks || schedule.CreatedAt.IsZero() ||
		schedule.UpdatedAt.IsZero() || schedule.UpdatedAt.Before(schedule.CreatedAt) {
		return errors.New("generation schedule row is invalid")
	}
	switch schedule.Status {
	case GenerationScheduleActive, GenerationScheduleSuperseded:
	case GenerationScheduleSettled:
		if schedule.NextOffset != schedule.TotalItems || schedule.Pending != 0 || schedule.Running != 0 ||
			schedule.Succeeded+schedule.Failed != schedule.TotalChunks {
			return errors.New("settled generation schedule row is invalid")
		}
	default:
		return errors.New("generation schedule status is invalid")
	}
	return nil
}

func generationCurrentID(repository, stage string) string {
	return generationSchedulerDigest("phebs-generation-schedule-current-v1", repository+"\x00"+stage)
}

func generationRepositoryID(repository string) string {
	return generationSchedulerDigest("phebs-generation-repository-token-v1", repository)
}

func generationChunkID(schedule string, offset int64, attempt int) string {
	return generationSchedulerDigest("phebs-generation-chunk-v1", fmt.Sprintf("%s\x00%d\x00%d", schedule, offset, attempt))
}

func generationSchedulerDigest(domain, value string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(value))
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

type generationScheduleRec GenerationSchedule

func (record generationScheduleRec) schedule() GenerationSchedule {
	return GenerationSchedule(record)
}

type generationChunkRec struct {
	RecID *models.RecordID `json:"id"`
	GenerationChunk
}

func (record generationChunkRec) chunk() (GenerationChunk, error) {
	chunk := record.GenerationChunk
	if record.RecID == nil {
		return GenerationChunk{}, errors.New("generation chunk has no record id")
	}
	id, ok := record.RecID.ID.(string)
	if !ok || record.RecID.Table != "generation_schedule_chunk" {
		return GenerationChunk{}, errors.New("generation chunk record id is invalid")
	}
	chunk.ID = id
	return chunk, nil
}

func boundedGenerationError(message string) string {
	if !utf8.ValidString(message) {
		return "invalid UTF-8 worker error"
	}
	if len(message) <= MaxGenerationErrorBytes {
		return message
	}
	for length := MaxGenerationErrorBytes; length > 0; length-- {
		if utf8.ValidString(message[:length]) {
			return message[:length]
		}
	}
	return ""
}

func generationChunkSpecs(schedule GenerationSchedule) []map[string]any {
	remaining := schedule.TotalItems - schedule.NextOffset
	count := min(MaxGenerationFanoutPage, int((remaining+int64(schedule.ChunkItems)-1)/int64(schedule.ChunkItems)))
	chunks := make([]map[string]any, 0, count)
	for index := range count {
		offset := schedule.NextOffset + int64(index*schedule.ChunkItems)
		length := min(schedule.ChunkItems, int(schedule.TotalItems-offset))
		chunks = append(chunks, map[string]any{
			"identity":        generationChunkID(schedule.Digest, offset, 0),
			"schedule_digest": schedule.Digest, "repository": schedule.Repository,
			"stage": schedule.Stage, "generation": schedule.Generation,
			"resource_class": schedule.ResourceClass, "offset": offset,
			"length": length, "attempt": 0, "priority": GenerationPriorityNeverRun,
		})
	}
	return slices.Clip(chunks)
}

const enqueueGenerationScheduleSQL = `
BEGIN;
LET $old_digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
LET $existing = (SELECT digest FROM $schedule LIMIT 1)[0].digest;
LET $other_active_stages = array::len(SELECT id FROM generation_schedule
	WHERE repository = $repository AND status = 'active' AND stage != $stage);
IF $existing = NONE AND $other_active_stages >= $max_active_stages {
	THROW 'phebs-permanent: generation repository active-stage limit exceeded';
};

IF $existing = NONE OR $old_digest = $digest {
	IF $old_digest != NONE AND $old_digest != $digest {
		UPDATE generation_schedule SET status = 'superseded', updated_at = time::now()
			WHERE digest = $old_digest AND status = 'active' RETURN NONE;
	};
	IF $existing = NONE {
		CREATE $schedule CONTENT {
		schema: 'phebs-generation-schedule-v1', digest: $digest,
		repository: $repository, stage: $stage, generation: $generation,
		resource_class: $resource_class, total_items: $total_items,
		chunk_items: $chunk_items, total_chunks: $total_chunks,
		max_attempts: $max_attempts, repository_tokens: $repository_tokens,
		next_offset: 0, materialized: 0, pending: 0, running: 0,
		succeeded: 0, failed: 0, status: 'active', created_at: time::now(),
		updated_at: time::now(), last_claimed_at: NONE
		} RETURN NONE;
	};
	UPSERT $current SET repository = $repository, stage = $stage,
		schedule_digest = $digest, generation = $generation,
		updated_at = time::now() RETURN NONE;
	UPSERT $repository_state SET repository = $repository,
		running = IF running = NONE THEN 0 ELSE running END,
		last_claimed_at = IF last_claimed_at = NONE THEN NONE ELSE last_claimed_at END,
		updated_at = time::now() RETURN NONE;
};
RETURN SELECT * FROM $schedule LIMIT 1;
COMMIT;`

func (s *Surreal) EnqueueGenerationSchedule(
	ctx context.Context,
	spec GenerationScheduleSpec,
) (*GenerationSchedule, error) {
	spec, err := normalizeGenerationScheduleSpec(spec)
	if err != nil {
		return nil, err
	}
	digest := generationScheduleDigest(spec)
	totalChunks := int((spec.TotalItems + int64(spec.ChunkItems) - 1) / int64(spec.ChunkItems))
	variables := map[string]any{
		"schedule":         models.NewRecordID("generation_schedule", strings.TrimPrefix(digest, "sha256:")),
		"current":          models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(spec.Repository, spec.Stage), "sha256:")),
		"repository_state": models.NewRecordID("generation_schedule_repository", strings.TrimPrefix(generationRepositoryID(spec.Repository), "sha256:")),
		"digest":           digest, "repository": spec.Repository, "stage": spec.Stage,
		"generation": spec.Generation, "resource_class": spec.ResourceClass,
		"total_items": spec.TotalItems, "chunk_items": spec.ChunkItems,
		"total_chunks": totalChunks, "max_attempts": spec.MaxAttempts,
		"repository_tokens": spec.RepositoryTokens,
		"max_active_stages": MaxGenerationActiveStagesPerRepository,
	}
	for attempt := 0; ; attempt++ {
		if err := readaccounting.Charge(ctx, readaccounting.StoreWriteAttempt, 1); err != nil {
			return nil, fmt.Errorf("enqueue generation schedule: %w", err)
		}
		results, queryErr := surrealdb.Query[[]generationScheduleRec](ctx, s.db, enqueueGenerationScheduleSQL, variables)
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("enqueue generation schedule: %w", queryErr)
		}
		rows := generationScheduleRows(results)
		if len(rows) != 1 {
			return nil, errors.New("enqueue generation schedule: schedule row is absent")
		}
		schedule := rows[0].schedule()
		if schedule.Digest != digest || schedule.Repository != spec.Repository ||
			schedule.Stage != spec.Stage || schedule.Generation != spec.Generation ||
			schedule.ResourceClass != spec.ResourceClass || schedule.TotalItems != spec.TotalItems ||
			schedule.ChunkItems != spec.ChunkItems || schedule.MaxAttempts != spec.MaxAttempts ||
			schedule.RepositoryTokens != spec.RepositoryTokens {
			return nil, errors.New("enqueue generation schedule: immutable schedule collision")
		}
		if schedule.Status != GenerationScheduleActive {
			return nil, ErrGenerationStale
		}
		return &schedule, nil
	}
}

func generationScheduleRows(results *[]surrealdb.QueryResult[[]generationScheduleRec]) []generationScheduleRec {
	for _, result := range *results {
		if len(result.Result) > 0 {
			return result.Result
		}
	}
	return nil
}

// generationReconcileTimeout bounds the reconciliation reads that follow an
// ambiguous transaction response. Reconciliation must not inherit the caller
// context: the ambiguous class includes exactly the case where that context
// already expired while the server committed.
const generationReconcileTimeout = 5 * time.Second

// MaxGenerationScheduleReadAttempts is the retry ceiling for one
// GetGenerationSchedule call, including its first attempt.
const MaxGenerationScheduleReadAttempts = maxQueueRetries

// queryGenerationSchedule retries only SurrealDB's explicit transient
// conflict class. Generation transactions bind immutable digests, exact
// offsets, and exact worker leases, so replay after an aborted conflict cannot
// duplicate fanout or settle a lease twice.
func queryGenerationSchedule[T any](
	ctx context.Context,
	db *surrealdb.DB,
	operation string,
	statement string,
	variables map[string]any,
) (*[]surrealdb.QueryResult[T], error) {
	for attempt := 0; ; attempt++ {
		// This helper also executes writes. Only this preparation-reachable
		// read operation belongs to the narrowly scoped read-attempt ledger.
		if operation == "get_schedule" {
			if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
				return nil, err
			}
		}
		results, err := surrealdb.Query[T](ctx, db, statement, variables)
		if err == nil {
			return results, nil
		}
		if !isRetryable(err) || ctx.Err() != nil || attempt+1 >= maxQueueRetries {
			return nil, err
		}
		diagnostics.Logf(
			"generation schedule store retry: operation=%s attempt=%d",
			operation, attempt+1,
		)
	}
}

func (s *Surreal) GetGenerationSchedule(
	ctx context.Context,
	repository, stage string,
) (*GenerationSchedule, error) {
	if strings.TrimSpace(repository) != repository || repository == "" ||
		!validGenerationToken(stage) || stage == "" {
		return nil, errors.New("get generation schedule: invalid scope")
	}
	results, err := queryGenerationSchedule[[]generationScheduleRec](ctx, s.db, "get_schedule", `
LET $digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
RETURN SELECT * FROM generation_schedule WHERE digest = $digest LIMIT 1;`, map[string]any{
		"current": models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(repository, stage), "sha256:")),
	})
	if err != nil {
		return nil, fmt.Errorf("get generation schedule: %w", err)
	}
	rows := generationScheduleRows(results)
	if len(rows) != 1 {
		return nil, fmt.Errorf("get generation schedule: %w", ErrNotFound)
	}
	schedule := rows[0].schedule()
	if err := ValidateGenerationSchedule(schedule); err != nil {
		return nil, fmt.Errorf("get generation schedule: %w", err)
	}
	return &schedule, nil
}

type retireCurrentGenerationScheduleRec struct {
	Retired bool `json:"retired"`
}

// RetireCurrentGenerationSchedule removes one exact current projection without
// inventing work for a generation that has no scheduler items. Active workers
// are fenced by the missing current row and finish through the ordinary stale
// completion path; the immutable schedule and chunks remain lifecycle-owned.
func (s *Surreal) RetireCurrentGenerationSchedule(
	ctx context.Context,
	expected GenerationSchedule,
) error {
	if err := ValidateGenerationSchedule(expected); err != nil {
		return fmt.Errorf("retire current generation schedule: %w", err)
	}
	results, err := queryGenerationSchedule[[]retireCurrentGenerationScheduleRec](
		ctx, s.db, "retire_current", `
BEGIN;
LET $current_digest = (SELECT schedule_digest FROM $current
	WHERE repository = $repository AND stage = $stage AND generation = $generation
	LIMIT 1)[0].schedule_digest;
LET $schedule_digest = (SELECT digest FROM $schedule
	WHERE repository = $repository AND stage = $stage AND generation = $generation
		AND digest = $digest LIMIT 1)[0].digest;
LET $retired = $current_digest = $digest AND $schedule_digest = $digest;
IF $retired {
	UPDATE $schedule SET status = 'superseded', updated_at = time::now()
		WHERE status = 'active' RETURN NONE;
	DELETE $current WHERE schedule_digest = $digest AND repository = $repository
		AND stage = $stage AND generation = $generation RETURN NONE;
};
RETURN [{ retired: $retired }];
COMMIT;`, map[string]any{
			"current": models.NewRecordID(
				"generation_schedule_current",
				strings.TrimPrefix(generationCurrentID(expected.Repository, expected.Stage), "sha256:"),
			),
			"schedule": models.NewRecordID(
				"generation_schedule", strings.TrimPrefix(expected.Digest, "sha256:"),
			),
			"digest": expected.Digest, "repository": expected.Repository,
			"stage": expected.Stage, "generation": expected.Generation,
		},
	)
	if err != nil {
		return fmt.Errorf("retire current generation schedule: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 && result.Result[0].Retired {
			return nil
		}
	}
	return ErrGenerationStale
}

// GetGenerationScheduleFailure returns only the final failed attempt for an
// exact current settled schedule. The caller must still re-read the schedule
// after composing a larger projection to close the ordinary read fence.
func (s *Surreal) GetGenerationScheduleFailure(
	ctx context.Context,
	repository, stage, scheduleDigest string,
) (*GenerationScheduleFailure, error) {
	schedule, err := s.GetGenerationSchedule(ctx, repository, stage)
	if err != nil {
		return nil, err
	}
	if schedule.Digest != scheduleDigest || schedule.Status != GenerationScheduleSettled ||
		schedule.Failed < 1 {
		return nil, ErrGenerationStale
	}
	results, err := surrealdb.Query[[]generationChunkRec](ctx, s.db, `
SELECT * FROM generation_schedule_chunk
	WHERE schedule_digest = $digest AND status = 'failed'
	ORDER BY attempt DESC LIMIT 1`, map[string]any{"digest": scheduleDigest})
	if err != nil {
		return nil, fmt.Errorf("read generation schedule failure: %w", err)
	}
	rows := generationChunkRows(results)
	if len(rows) != 1 {
		return nil, fmt.Errorf("read generation schedule failure: %w", ErrNotFound)
	}
	chunk, err := rows[0].chunk()
	if err != nil {
		return nil, err
	}
	if chunk.ScheduleDigest != schedule.Digest || chunk.Repository != repository ||
		chunk.Stage != stage || chunk.Generation != schedule.Generation ||
		chunk.Status != GenerationChunkFailed || chunk.Attempt < 0 ||
		chunk.Attempt >= schedule.MaxAttempts {
		return nil, errors.New("read generation schedule failure: invalid failed chunk")
	}
	projection := &GenerationScheduleFailure{
		ScheduleDigest: chunk.ScheduleDigest,
		Generation:     chunk.Generation,
		Attempt:        chunk.Attempt,
	}
	if receipt, ok := pipelinerefusal.ParseDurableErrorText(chunk.Error); ok {
		projection.Refusal = &receipt
	}
	return projection, nil
}

const expandGenerationScheduleSQL = `
BEGIN;
LET $current_digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
LET $owned = (SELECT digest FROM $schedule WHERE status = 'active'
	AND digest = $digest AND next_offset = $next_offset LIMIT 1)[0].digest;
IF $current_digest = $digest AND $owned = $digest {
	FOR $chunk IN $chunks {
		CREATE generation_schedule_chunk CONTENT {
			identity: $chunk.identity, schedule_digest: $chunk.schedule_digest,
			repository: $chunk.repository, stage: $chunk.stage,
			generation: $chunk.generation, resource_class: $chunk.resource_class,
			offset: $chunk.offset, length: $chunk.length, attempt: $chunk.attempt,
			priority: $chunk.priority, status: 'pending', not_before: NONE,
			claimed_by: '', claimed_at: NONE, heartbeat_at: NONE,
			finished_at: NONE, error: '', lease_token: ''
		} RETURN NONE;
	};
	UPDATE $schedule SET next_offset = $new_offset,
		materialized += $chunk_count, pending += $chunk_count,
		updated_at = time::now() RETURN NONE;
};
RETURN SELECT * FROM $schedule LIMIT 1;
COMMIT;`

func (s *Surreal) ExpandGenerationSchedule(
	ctx context.Context,
	repository, stage, generation string,
) (*GenerationSchedule, error) {
	schedule, err := s.GetGenerationSchedule(ctx, repository, stage)
	if err != nil {
		return nil, err
	}
	if schedule.Generation != generation || schedule.Status != GenerationScheduleActive {
		return nil, ErrGenerationStale
	}
	if schedule.NextOffset == schedule.TotalItems {
		return schedule, nil
	}
	chunks := generationChunkSpecs(*schedule)
	newOffset := schedule.NextOffset
	for _, chunk := range chunks {
		newOffset += int64(chunk["length"].(int))
	}
	variables := map[string]any{
		"current":  models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(repository, stage), "sha256:")),
		"schedule": models.NewRecordID("generation_schedule", strings.TrimPrefix(schedule.Digest, "sha256:")),
		"digest":   schedule.Digest, "next_offset": schedule.NextOffset,
		"new_offset": newOffset, "chunk_count": len(chunks), "chunks": chunks,
	}
	results, err := queryGenerationSchedule[[]generationScheduleRec](
		ctx, s.db, "expand", expandGenerationScheduleSQL, variables,
	)
	if err != nil {
		return nil, fmt.Errorf("expand generation schedule: %w", err)
	}
	rows := generationScheduleRows(results)
	if len(rows) != 1 {
		return nil, errors.New("expand generation schedule: schedule row is absent")
	}
	expanded := rows[0].schedule()
	if expanded.NextOffset != newOffset {
		return nil, ErrGenerationStale
	}
	return &expanded, nil
}

func (s *Surreal) ExpandNextGenerationSchedule(
	ctx context.Context,
	class GenerationResourceClass,
) (*GenerationSchedule, error) {
	if !validGenerationResourceClass(class) {
		return nil, errors.New("expand next generation schedule: invalid resource class")
	}
	results, err := queryGenerationSchedule[[]generationScheduleRec](ctx, s.db, "select_expand", `
SELECT * FROM generation_schedule WHERE resource_class = $class
	AND status = 'active' AND next_offset < total_items
	ORDER BY updated_at, repository, stage LIMIT 1`, map[string]any{"class": class})
	if err != nil {
		return nil, fmt.Errorf("expand next generation schedule: %w", err)
	}
	rows := generationScheduleRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf("expand next generation schedule: %w", ErrNotFound)
	}
	schedule := rows[0].schedule()
	return s.ExpandGenerationSchedule(ctx, schedule.Repository, schedule.Stage, schedule.Generation)
}

const claimGenerationChunkSQL = `
BEGIN;
LET $current_digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
LET $schedule_ready = (SELECT digest FROM $schedule WHERE status = 'active'
	AND digest = $digest AND pending > 0 LIMIT 1)[0].digest;
LET $repository_running = (SELECT running FROM $repository_state LIMIT 1)[0].running;
LET $candidate = (SELECT id, priority, offset, attempt FROM generation_schedule_chunk
	WHERE schedule_digest = $digest AND status = 'pending'
		AND (not_before = NONE OR not_before <= time::now())
	ORDER BY priority, offset, attempt LIMIT 1)[0].id;
LET $claimed = IF $current_digest = $digest AND $schedule_ready = $digest
	AND $repository_running < $repository_tokens AND $candidate != NONE THEN
	(UPDATE $candidate SET status = 'running', claimed_by = $worker,
		lease_token = $lease, claimed_at = time::now(), heartbeat_at = time::now(),
		not_before = NONE WHERE status = 'pending' RETURN AFTER)
ELSE [] END;
IF array::len($claimed) = 1 {
	UPDATE $schedule SET pending -= 1, running += 1,
		last_claimed_at = time::now(), updated_at = time::now() RETURN NONE;
	UPDATE $repository_state SET running += 1, last_claimed_at = time::now(),
		updated_at = time::now() RETURN NONE;
};
RETURN $claimed;
COMMIT;`

func (s *Surreal) ClaimGenerationChunk(
	ctx context.Context,
	class GenerationResourceClass,
	worker string,
) (*GenerationChunk, error) {
	if !validGenerationResourceClass(class) || strings.TrimSpace(worker) != worker ||
		worker == "" || len(worker) > MaxGenerationWorkerBytes || !utf8.ValidString(worker) {
		return nil, errors.New("claim generation chunk: invalid class or worker")
	}
	results, err := surrealdb.Query[[]generationScheduleRec](ctx, s.db, `
SELECT * FROM generation_schedule WHERE resource_class = $class
	AND status = 'active' AND pending > 0
	ORDER BY last_claimed_at, created_at, repository, stage
	LIMIT $limit`, map[string]any{"class": class, "limit": maxGenerationScheduleClaimCandidates})
	if err != nil {
		return nil, fmt.Errorf("claim generation chunk candidates: %w", err)
	}
	candidates := generationScheduleRows(results)
	for _, record := range candidates {
		schedule := record.schedule()
		lease, leaseErr := newLeaseToken()
		if leaseErr != nil {
			return nil, leaseErr
		}
		variables := map[string]any{
			"current":          models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(schedule.Repository, schedule.Stage), "sha256:")),
			"schedule":         models.NewRecordID("generation_schedule", strings.TrimPrefix(schedule.Digest, "sha256:")),
			"repository_state": models.NewRecordID("generation_schedule_repository", strings.TrimPrefix(generationRepositoryID(schedule.Repository), "sha256:")),
			"digest":           schedule.Digest, "repository_tokens": schedule.RepositoryTokens,
			"worker": worker, "lease": lease,
		}
		for attempt := 0; ; attempt++ {
			claimedResults, claimErr := surrealdb.Query[[]generationChunkRec](ctx, s.db, claimGenerationChunkSQL, variables)
			if claimErr != nil {
				if isRetryable(claimErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
					continue
				}
				return nil, fmt.Errorf("claim generation chunk: %w", claimErr)
			}
			rows := generationChunkRows(claimedResults)
			if len(rows) == 0 {
				break
			}
			chunk, chunkErr := rows[0].chunk()
			if chunkErr != nil {
				return nil, chunkErr
			}
			return &chunk, nil
		}
	}
	return nil, fmt.Errorf("claim generation chunk: %w", ErrNotFound)
}

func generationChunkRows(results *[]surrealdb.QueryResult[[]generationChunkRec]) []generationChunkRec {
	for _, result := range *results {
		if len(result.Result) > 0 {
			return result.Result
		}
	}
	return nil
}

func validGenerationChunkLease(chunk GenerationChunk) error {
	if chunk.ID == "" || chunk.ScheduleDigest == "" || chunk.Repository == "" ||
		chunk.Stage == "" || chunk.LeaseToken == "" || chunk.ClaimedBy == "" ||
		chunk.Status != GenerationChunkRunning {
		return ErrGenerationLeaseLost
	}
	return nil
}

func generationChunkRecordID(chunk GenerationChunk) models.RecordID {
	return models.NewRecordID("generation_schedule_chunk", chunk.ID)
}

func (s *Surreal) HeartbeatGenerationChunk(ctx context.Context, chunk GenerationChunk) error {
	if err := validGenerationChunkLease(chunk); err != nil {
		return err
	}
	results, err := queryGenerationSchedule[[]generationChunkRec](ctx, s.db, "heartbeat", `
UPDATE $chunk SET heartbeat_at = time::now()
	WHERE status = 'running' AND lease_token = $lease AND claimed_by = $worker
	RETURN AFTER`, map[string]any{
		"chunk": generationChunkRecordID(chunk), "lease": chunk.LeaseToken,
		"worker": chunk.ClaimedBy,
	})
	if err != nil {
		return fmt.Errorf("heartbeat generation chunk: %w", err)
	}
	if len(generationChunkRows(results)) != 1 {
		return ErrGenerationLeaseLost
	}
	return nil
}

type generationTransitionRec struct {
	Owned     bool `json:"owned"`
	Current   bool `json:"current"`
	Exhausted bool `json:"exhausted"`
}

func generationTransitionRows(results *[]surrealdb.QueryResult[[]generationTransitionRec]) []generationTransitionRec {
	for _, result := range *results {
		if len(result.Result) > 0 {
			return result.Result
		}
	}
	return nil
}

const completeGenerationChunkSQL = `
BEGIN;
LET $owned = (SELECT id FROM $chunk WHERE status = 'running'
	AND lease_token = $lease AND claimed_by = $worker LIMIT 1)[0].id;
LET $current_digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
LET $is_current = $current_digest = $digest;
IF $owned != NONE {
	UPDATE $chunk SET status = IF $is_current THEN 'done' ELSE 'canceled' END,
		finished_at = time::now(), lease_token = '', heartbeat_at = NONE
		RETURN NONE;
	UPDATE $schedule SET running -= 1,
		succeeded += IF $is_current THEN 1 ELSE 0 END,
		status = IF $is_current AND next_offset = total_items AND pending = 0
			AND running = 1 AND succeeded + failed + 1 = total_chunks
			THEN 'settled' ELSE status END,
		updated_at = time::now() RETURN NONE;
	UPDATE $repository_state SET running -= 1, updated_at = time::now() RETURN NONE;
};
RETURN [{ owned: $owned != NONE, current: $is_current, exhausted: false }];
COMMIT;`

func (s *Surreal) CompleteGenerationChunk(ctx context.Context, chunk GenerationChunk) error {
	if err := validGenerationChunkLease(chunk); err != nil {
		return err
	}
	results, err := queryGenerationSchedule[[]generationTransitionRec](ctx, s.db, "complete", completeGenerationChunkSQL, map[string]any{
		"chunk":            generationChunkRecordID(chunk),
		"schedule":         models.NewRecordID("generation_schedule", strings.TrimPrefix(chunk.ScheduleDigest, "sha256:")),
		"current":          models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(chunk.Repository, chunk.Stage), "sha256:")),
		"repository_state": models.NewRecordID("generation_schedule_repository", strings.TrimPrefix(generationRepositoryID(chunk.Repository), "sha256:")),
		"digest":           chunk.ScheduleDigest, "lease": chunk.LeaseToken, "worker": chunk.ClaimedBy,
	})
	reconcile := func() (bool, error) {
		reconcileCtx, reconcileCancel := context.WithTimeout(
			context.WithoutCancel(ctx), generationReconcileTimeout,
		)
		defer reconcileCancel()
		return s.reconcileGenerationCompletion(reconcileCtx, chunk)
	}
	if err != nil {
		if applied, reconcileErr := reconcile(); applied {
			return nil
		} else if errors.Is(reconcileErr, ErrGenerationStale) ||
			errors.Is(reconcileErr, ErrGenerationLeaseLost) {
			return reconcileErr
		}
		return fmt.Errorf("complete generation chunk: %w", err)
	}
	rows := generationTransitionRows(results)
	if len(rows) != 1 || !rows[0].Owned {
		// A conflict replay of an invisibly committed completion also lands
		// here: the committed attempt already cleared the lease.
		if applied, reconcileErr := reconcile(); applied {
			return nil
		} else if errors.Is(reconcileErr, ErrGenerationStale) ||
			errors.Is(reconcileErr, ErrGenerationLeaseLost) {
			return reconcileErr
		}
		return ErrGenerationLeaseLost
	}
	if !rows[0].Current {
		return ErrGenerationStale
	}
	return nil
}

// reconcileGenerationCompletion closes an ambiguous client response without
// replaying accounting. A done chunk proves that its schedule and repository
// counters advanced in the same committed transaction; the current pointer is
// re-read so a superseded completion remains stale rather than successful.
func (s *Surreal) reconcileGenerationCompletion(
	ctx context.Context,
	claimed GenerationChunk,
) (bool, error) {
	current, err := s.generationChunkByIdentity(ctx, claimed.Identity)
	if err != nil {
		return false, err
	}
	if current.ID != claimed.ID || current.ScheduleDigest != claimed.ScheduleDigest ||
		current.Repository != claimed.Repository || current.Stage != claimed.Stage ||
		current.Generation != claimed.Generation || current.Offset != claimed.Offset ||
		current.Attempt != claimed.Attempt {
		return false, ErrGenerationLeaseLost
	}
	switch current.Status {
	case GenerationChunkDone:
		// Completion clears the lease token but preserves claimed_by, so the
		// claimant is the only surviving ownership evidence: a chunk reaped,
		// reclaimed, and completed by another worker must stay fenced.
		if current.ClaimedBy != claimed.ClaimedBy {
			return false, ErrGenerationLeaseLost
		}
		schedule, scheduleErr := s.GetGenerationSchedule(
			ctx, claimed.Repository, claimed.Stage,
		)
		if scheduleErr != nil {
			return false, scheduleErr
		}
		if schedule.Digest != claimed.ScheduleDigest ||
			schedule.Generation != claimed.Generation {
			return false, ErrGenerationStale
		}
		return true, nil
	case GenerationChunkCanceled:
		return false, ErrGenerationStale
	case GenerationChunkRunning:
		if current.LeaseToken != claimed.LeaseToken || current.ClaimedBy != claimed.ClaimedBy {
			return false, ErrGenerationLeaseLost
		}
		return false, nil
	default:
		return false, ErrGenerationLeaseLost
	}
}

const failGenerationChunkSQL = `
BEGIN;
LET $owned = (SELECT id FROM $chunk WHERE status = 'running'
	AND lease_token = $lease AND claimed_by = $worker LIMIT 1)[0].id;
LET $current_digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
LET $is_current = $current_digest = $digest;
IF $owned != NONE {
	UPDATE $chunk SET status = IF $is_current THEN 'failed' ELSE 'canceled' END,
		error = $error, finished_at = time::now(), lease_token = '', heartbeat_at = NONE
		RETURN NONE;
	UPDATE $schedule SET running -= 1,
		failed += IF $is_current THEN 1 ELSE 0 END,
		status = IF $is_current AND next_offset = total_items AND pending = 0
			AND running = 1 AND succeeded + failed + 1 = total_chunks
			THEN 'settled' ELSE status END,
		updated_at = time::now() RETURN NONE;
	UPDATE $repository_state SET running -= 1, updated_at = time::now() RETURN NONE;
};
RETURN [{ owned: $owned != NONE, current: $is_current, exhausted: true }];
COMMIT;`

// FailGenerationChunk settles one deterministic terminal result without
// materializing a retry successor. The exact worker lease and current schedule
// pointer are rechecked in the same transaction, so a superseded worker can
// only cancel its old chunk and cannot contribute failure to the new schedule.
func (s *Surreal) FailGenerationChunk(
	ctx context.Context,
	chunk GenerationChunk,
	errMessage string,
) error {
	if err := validGenerationChunkLease(chunk); err != nil {
		return err
	}
	results, err := queryGenerationSchedule[[]generationTransitionRec](
		ctx, s.db, "fail", failGenerationChunkSQL, map[string]any{
			"chunk": generationChunkRecordID(chunk),
			"schedule": models.NewRecordID(
				"generation_schedule",
				strings.TrimPrefix(chunk.ScheduleDigest, "sha256:"),
			),
			"current": models.NewRecordID(
				"generation_schedule_current",
				strings.TrimPrefix(
					generationCurrentID(chunk.Repository, chunk.Stage),
					"sha256:",
				),
			),
			"repository_state": models.NewRecordID(
				"generation_schedule_repository",
				strings.TrimPrefix(generationRepositoryID(chunk.Repository), "sha256:"),
			),
			"digest": chunk.ScheduleDigest, "lease": chunk.LeaseToken,
			"worker": chunk.ClaimedBy, "error": boundedGenerationError(errMessage),
		},
	)
	if err != nil {
		return fmt.Errorf("fail generation chunk: %w", err)
	}
	rows := generationTransitionRows(results)
	if len(rows) != 1 || !rows[0].Owned {
		return ErrGenerationLeaseLost
	}
	if !rows[0].Current {
		return ErrGenerationStale
	}
	return nil
}

const retryGenerationChunkSQL = `
BEGIN;
LET $owned = (SELECT id FROM $chunk WHERE status = 'running'
	AND lease_token = $lease AND claimed_by = $worker LIMIT 1)[0].id;
LET $current_digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
LET $is_current = $current_digest = $digest;
LET $exhausted = $attempt + 1 >= $max_attempts;
IF $owned != NONE {
	UPDATE $chunk SET status = IF $is_current THEN 'failed' ELSE 'canceled' END,
		error = $error, finished_at = time::now(), lease_token = '', heartbeat_at = NONE
		RETURN NONE;
	IF $is_current AND !$exhausted {
		CREATE generation_schedule_chunk CONTENT {
			identity: $successor_identity, schedule_digest: $digest,
			repository: $repository, stage: $stage, generation: $generation,
			resource_class: $resource_class, offset: $offset, length: $length,
			attempt: $attempt + 1, priority: 1, status: 'pending',
			not_before: $not_before, claimed_by: '', claimed_at: NONE,
			heartbeat_at: NONE, finished_at: NONE, error: '', lease_token: ''
		} RETURN NONE;
	};
	UPDATE $schedule SET running -= 1,
		materialized += IF $is_current AND !$exhausted THEN 1 ELSE 0 END,
		pending += IF $is_current AND !$exhausted THEN 1 ELSE 0 END,
		failed += IF $is_current AND $exhausted THEN 1 ELSE 0 END,
		status = IF $is_current AND $exhausted AND next_offset = total_items
			AND pending = 0 AND running = 1 AND succeeded + failed + 1 = total_chunks
			THEN 'settled' ELSE status END,
		updated_at = time::now() RETURN NONE;
	UPDATE $repository_state SET running -= 1, updated_at = time::now() RETURN NONE;
};
RETURN [{ owned: $owned != NONE, current: $is_current, exhausted: $exhausted }];
COMMIT;`

func (s *Surreal) RetryGenerationChunk(
	ctx context.Context,
	chunk GenerationChunk,
	errMessage string,
	notBefore time.Time,
) (*GenerationChunk, error) {
	if err := validGenerationChunkLease(chunk); err != nil {
		return nil, err
	}
	if notBefore.IsZero() {
		return nil, errors.New("retry generation chunk: not-before is required")
	}
	schedule, err := s.generationScheduleByDigest(ctx, chunk.ScheduleDigest)
	if err != nil {
		return nil, err
	}
	successorIdentity := generationChunkID(chunk.ScheduleDigest, chunk.Offset, chunk.Attempt+1)
	variables := map[string]any{
		"chunk":            generationChunkRecordID(chunk),
		"schedule":         models.NewRecordID("generation_schedule", strings.TrimPrefix(chunk.ScheduleDigest, "sha256:")),
		"current":          models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(chunk.Repository, chunk.Stage), "sha256:")),
		"repository_state": models.NewRecordID("generation_schedule_repository", strings.TrimPrefix(generationRepositoryID(chunk.Repository), "sha256:")),
		"digest":           chunk.ScheduleDigest, "lease": chunk.LeaseToken, "worker": chunk.ClaimedBy,
		"attempt": chunk.Attempt, "max_attempts": schedule.MaxAttempts,
		"error": boundedGenerationError(errMessage), "not_before": notBefore.UTC(),
		"successor_identity": successorIdentity, "repository": chunk.Repository,
		"stage": chunk.Stage, "generation": chunk.Generation,
		"resource_class": chunk.ResourceClass, "offset": chunk.Offset, "length": chunk.Length,
	}
	results, err := queryGenerationSchedule[[]generationTransitionRec](
		ctx, s.db, "retry", retryGenerationChunkSQL, variables,
	)
	if err != nil {
		if s.reconcileGenerationExhaustion(ctx, chunk, schedule.MaxAttempts, errMessage) {
			return nil, ErrGenerationExhausted
		}
		return nil, fmt.Errorf("retry generation chunk: %w", err)
	}
	rows := generationTransitionRows(results)
	if len(rows) != 1 || !rows[0].Owned {
		if s.reconcileGenerationExhaustion(ctx, chunk, schedule.MaxAttempts, errMessage) {
			return nil, ErrGenerationExhausted
		}
		return nil, ErrGenerationLeaseLost
	}
	if !rows[0].Current {
		return nil, ErrGenerationStale
	}
	if rows[0].Exhausted {
		return nil, ErrGenerationExhausted
	}
	return s.generationChunkByIdentity(ctx, successorIdentity)
}

// reconcileGenerationExhaustion detects a final-attempt retry whose
// transaction committed while the client response was lost: the chunk row is
// durably failed at the caller's exact attempt with the caller's error text.
// Retry clears the lease but preserves claimed_by, so exact claimant matching
// both recovers the caller's committed result and fences a reaped worker from
// adopting another claimant's exhaustion.
func (s *Surreal) reconcileGenerationExhaustion(
	ctx context.Context,
	claimed GenerationChunk,
	maxAttempts int,
	errMessage string,
) bool {
	if claimed.Attempt+1 < maxAttempts {
		return false
	}
	reconcileCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), generationReconcileTimeout,
	)
	defer cancel()
	current, err := s.generationChunkByIdentity(reconcileCtx, claimed.Identity)
	return err == nil && current.ID == claimed.ID &&
		current.ScheduleDigest == claimed.ScheduleDigest &&
		current.Offset == claimed.Offset && current.Attempt == claimed.Attempt &&
		current.ClaimedBy == claimed.ClaimedBy &&
		current.Status == GenerationChunkFailed &&
		current.Error == boundedGenerationError(errMessage)
}

const releaseGenerationChunkSQL = `
BEGIN;
LET $owned = (SELECT id FROM $chunk WHERE status = 'running'
	AND lease_token = $lease AND claimed_by = $worker LIMIT 1)[0].id;
LET $current_digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
LET $is_current = $current_digest = $digest;
IF $owned != NONE {
	UPDATE $chunk SET status = IF $is_current THEN 'pending' ELSE 'canceled' END,
		priority = IF $is_current THEN 2 ELSE priority END,
		not_before = IF $is_current THEN time::now() ELSE NONE END,
		error = $error, claimed_by = '', claimed_at = NONE,
		heartbeat_at = NONE, lease_token = '' RETURN NONE;
	UPDATE $schedule SET running -= 1,
		pending += IF $is_current THEN 1 ELSE 0 END,
		updated_at = time::now() RETURN NONE;
	UPDATE $repository_state SET running -= 1, updated_at = time::now() RETURN NONE;
};
RETURN [{ owned: $owned != NONE, current: $is_current, exhausted: false }];
COMMIT;`

const deferGenerationChunkSQL = `
BEGIN;
LET $owned = (SELECT id FROM $chunk WHERE status = 'running'
	AND lease_token = $lease AND claimed_by = $worker LIMIT 1)[0].id;
LET $current_digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
LET $is_current = $current_digest = $digest;
IF $owned != NONE {
	UPDATE $chunk SET status = IF $is_current THEN 'pending' ELSE 'canceled' END,
		priority = IF $is_current THEN 2 ELSE priority END,
		not_before = IF $is_current THEN $not_before ELSE NONE END,
		error = $error, claimed_by = '', claimed_at = NONE,
		heartbeat_at = NONE, lease_token = '' RETURN NONE;
	UPDATE $schedule SET running -= 1,
		pending += IF $is_current THEN 1 ELSE 0 END,
		updated_at = time::now() RETURN NONE;
	UPDATE $repository_state SET running -= 1, updated_at = time::now() RETURN NONE;
};
RETURN [{ owned: $owned != NONE, current: $is_current, exhausted: false }];
COMMIT;`

// DeferGenerationChunk returns the same exact leased chunk to delayed pending
// state without consuming an attempt or materializing a successor. The delay
// is recomputed for each store-level conflict retry so the committed fence
// never inherits elapsed time from an earlier conflicted transaction attempt.
func (s *Surreal) DeferGenerationChunk(
	ctx context.Context,
	chunk GenerationChunk,
	reason string,
	delay time.Duration,
) error {
	if err := validGenerationChunkLease(chunk); err != nil {
		return err
	}
	if delay <= 0 {
		return errors.New("defer generation chunk: delay is required")
	}
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]generationTransitionRec](
			ctx,
			s.db,
			deferGenerationChunkSQL,
			map[string]any{
				"chunk": generationChunkRecordID(chunk),
				"schedule": models.NewRecordID(
					"generation_schedule",
					strings.TrimPrefix(chunk.ScheduleDigest, "sha256:"),
				),
				"current": models.NewRecordID(
					"generation_schedule_current",
					strings.TrimPrefix(
						generationCurrentID(chunk.Repository, chunk.Stage),
						"sha256:",
					),
				),
				"repository_state": models.NewRecordID(
					"generation_schedule_repository",
					strings.TrimPrefix(generationRepositoryID(chunk.Repository), "sha256:"),
				),
				"digest": chunk.ScheduleDigest, "lease": chunk.LeaseToken,
				"worker": chunk.ClaimedBy, "error": boundedGenerationError(reason),
				"not_before": time.Now().UTC().Add(delay),
			},
		)
		if err != nil {
			if isRetryable(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				diagnostics.Logf(
					"generation schedule store retry: operation=defer attempt=%d",
					attempt+1,
				)
				continue
			}
			return fmt.Errorf("defer generation chunk: %w", err)
		}
		rows := generationTransitionRows(results)
		if len(rows) != 1 || !rows[0].Owned {
			return ErrGenerationLeaseLost
		}
		if !rows[0].Current {
			return ErrGenerationStale
		}
		return nil
	}
}

func (s *Surreal) ReleaseGenerationChunk(ctx context.Context, chunk GenerationChunk, reason string) error {
	if err := validGenerationChunkLease(chunk); err != nil {
		return err
	}
	results, err := queryGenerationSchedule[[]generationTransitionRec](ctx, s.db, "release", releaseGenerationChunkSQL, map[string]any{
		"chunk":            generationChunkRecordID(chunk),
		"schedule":         models.NewRecordID("generation_schedule", strings.TrimPrefix(chunk.ScheduleDigest, "sha256:")),
		"current":          models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(chunk.Repository, chunk.Stage), "sha256:")),
		"repository_state": models.NewRecordID("generation_schedule_repository", strings.TrimPrefix(generationRepositoryID(chunk.Repository), "sha256:")),
		"digest":           chunk.ScheduleDigest, "lease": chunk.LeaseToken,
		"worker": chunk.ClaimedBy, "error": boundedGenerationError(reason),
	})
	if err != nil {
		return fmt.Errorf("release generation chunk: %w", err)
	}
	rows := generationTransitionRows(results)
	if len(rows) != 1 || !rows[0].Owned {
		return ErrGenerationLeaseLost
	}
	if !rows[0].Current {
		return ErrGenerationStale
	}
	return nil
}

const reapGenerationChunkSQL = `
BEGIN;
LET $owned = (SELECT id FROM $chunk WHERE status = 'running'
	AND lease_token = $lease AND claimed_by = $worker
	AND heartbeat_at = $heartbeat AND heartbeat_at < $cutoff LIMIT 1)[0].id;
LET $current_digest = (SELECT schedule_digest FROM $current LIMIT 1)[0].schedule_digest;
LET $is_current = $current_digest = $digest;
IF $owned != NONE {
	UPDATE $chunk SET status = IF $is_current THEN 'pending' ELSE 'canceled' END,
		priority = IF $is_current THEN 2 ELSE priority END,
		not_before = IF $is_current THEN time::now() ELSE NONE END,
		error = $error, claimed_by = '', claimed_at = NONE,
		heartbeat_at = NONE, lease_token = '' RETURN NONE;
	UPDATE $schedule SET running -= 1,
		pending += IF $is_current THEN 1 ELSE 0 END,
		updated_at = time::now() RETURN NONE;
	UPDATE $repository_state SET running -= 1, updated_at = time::now() RETURN NONE;
};
RETURN [{ owned: $owned != NONE, current: $is_current, exhausted: false }];
COMMIT;`

// releaseStaleGenerationChunk binds the destructive release to the exact
// heartbeat observed by the reaper's candidate read. A worker heartbeat that
// lands after selection therefore wins, and the reaper cannot revoke the
// renewed lease using stale evidence.
func (s *Surreal) releaseStaleGenerationChunk(
	ctx context.Context,
	chunk GenerationChunk,
	cutoff time.Time,
) error {
	if err := validGenerationChunkLease(chunk); err != nil {
		return err
	}
	if chunk.HeartbeatAt == nil || !chunk.HeartbeatAt.Before(cutoff) {
		return ErrGenerationLeaseLost
	}
	results, err := queryGenerationSchedule[[]generationTransitionRec](
		ctx, s.db, "reap", reapGenerationChunkSQL, map[string]any{
			"chunk": generationChunkRecordID(chunk),
			"schedule": models.NewRecordID(
				"generation_schedule",
				strings.TrimPrefix(chunk.ScheduleDigest, "sha256:"),
			),
			"current": models.NewRecordID(
				"generation_schedule_current",
				strings.TrimPrefix(
					generationCurrentID(chunk.Repository, chunk.Stage),
					"sha256:",
				),
			),
			"repository_state": models.NewRecordID(
				"generation_schedule_repository",
				strings.TrimPrefix(generationRepositoryID(chunk.Repository), "sha256:"),
			),
			"digest": chunk.ScheduleDigest, "lease": chunk.LeaseToken,
			"worker": chunk.ClaimedBy, "heartbeat": chunk.HeartbeatAt.UTC(),
			"cutoff": cutoff.UTC(), "error": "stale worker lease reaped",
		},
	)
	if err != nil {
		return fmt.Errorf("reap generation chunk: %w", err)
	}
	rows := generationTransitionRows(results)
	if len(rows) != 1 || !rows[0].Owned {
		return ErrGenerationLeaseLost
	}
	if !rows[0].Current {
		return ErrGenerationStale
	}
	return nil
}

func (s *Surreal) ReapStaleGenerationChunks(
	ctx context.Context,
	class GenerationResourceClass,
	staleAfter time.Duration,
) (int, error) {
	if !validGenerationResourceClass(class) || staleAfter <= 0 {
		return 0, errors.New("reap generation chunks: invalid class or cutoff")
	}
	cutoff := time.Now().UTC().Add(-staleAfter)
	results, err := queryGenerationSchedule[[]generationChunkRec](ctx, s.db, "select_reap", `
SELECT * FROM generation_schedule_chunk WITH INDEX generation_schedule_chunk_stale
	WHERE resource_class = $class AND status = 'running'
	AND heartbeat_at != NONE AND heartbeat_at < $cutoff
	ORDER BY heartbeat_at, repository, stage, offset LIMIT $limit`, map[string]any{
		"class": class, "cutoff": cutoff, "limit": MaxGenerationClaimCandidates,
	})
	if err != nil {
		return 0, fmt.Errorf("reap generation chunk candidates: %w", err)
	}
	reaped := 0
	for _, row := range generationChunkRows(results) {
		chunk, chunkErr := row.chunk()
		if chunkErr != nil {
			return reaped, chunkErr
		}
		if releaseErr := s.releaseStaleGenerationChunk(ctx, chunk, cutoff); releaseErr != nil {
			if errors.Is(releaseErr, ErrGenerationStale) {
				reaped++
				continue
			}
			if errors.Is(releaseErr, ErrGenerationLeaseLost) {
				continue
			}
			return reaped, releaseErr
		}
		reaped++
	}
	return reaped, nil
}

func (s *Surreal) generationScheduleByDigest(ctx context.Context, digest string) (*GenerationSchedule, error) {
	if !validSHA256(digest) {
		return nil, errors.New("generation schedule digest is invalid")
	}
	results, err := surrealdb.Query[[]generationScheduleRec](ctx, s.db,
		"SELECT * FROM $schedule LIMIT 1", map[string]any{
			"schedule": models.NewRecordID("generation_schedule", strings.TrimPrefix(digest, "sha256:")),
		})
	if err != nil {
		return nil, fmt.Errorf("read generation schedule: %w", err)
	}
	rows := generationScheduleRows(results)
	if len(rows) != 1 {
		return nil, fmt.Errorf("read generation schedule: %w", ErrNotFound)
	}
	schedule := rows[0].schedule()
	return &schedule, nil
}

func (s *Surreal) generationChunkByIdentity(ctx context.Context, identity string) (*GenerationChunk, error) {
	results, err := queryGenerationSchedule[[]generationChunkRec](ctx, s.db, "chunk_by_identity",
		"SELECT * FROM generation_schedule_chunk WHERE identity = $identity LIMIT 1",
		map[string]any{"identity": identity})
	if err != nil {
		return nil, fmt.Errorf("read generation chunk: %w", err)
	}
	rows := generationChunkRows(results)
	if len(rows) != 1 {
		return nil, fmt.Errorf("read generation chunk: %w", ErrNotFound)
	}
	chunk, err := rows[0].chunk()
	if err != nil {
		return nil, err
	}
	return &chunk, nil
}
