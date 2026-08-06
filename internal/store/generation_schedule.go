package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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
	MaxGenerationRepositoryTokens                = 8
	MaxGenerationWorkerBytes                     = 256
	MaxGenerationErrorBytes                      = 2_048
)

var (
	ErrGenerationStale     = errors.New("generation schedule is stale")
	ErrGenerationLeaseLost = errors.New("generation chunk lease lost")
	ErrGenerationExhausted = errors.New("generation chunk attempts exhausted")
)

type GenerationResourceClass string

const (
	GenerationResourceCPU    GenerationResourceClass = "cpu"
	GenerationResourceIO     GenerationResourceClass = "io"
	GenerationResourceMemory GenerationResourceClass = "memory"
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

type GenerationSchedulerStore interface {
	EnqueueGenerationSchedule(context.Context, GenerationScheduleSpec) (*GenerationSchedule, error)
	ExpandGenerationSchedule(context.Context, string, string, string) (*GenerationSchedule, error)
	ExpandNextGenerationSchedule(context.Context, GenerationResourceClass) (*GenerationSchedule, error)
	ClaimGenerationChunk(context.Context, GenerationResourceClass, string) (*GenerationChunk, error)
	HeartbeatGenerationChunk(context.Context, GenerationChunk) error
	CompleteGenerationChunk(context.Context, GenerationChunk) error
	RetryGenerationChunk(context.Context, GenerationChunk, string, time.Time) (*GenerationChunk, error)
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
	case GenerationResourceCPU, GenerationResourceIO, GenerationResourceMemory:
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

func (s *Surreal) GetGenerationSchedule(
	ctx context.Context,
	repository, stage string,
) (*GenerationSchedule, error) {
	if strings.TrimSpace(repository) != repository || repository == "" ||
		!validGenerationToken(stage) || stage == "" {
		return nil, errors.New("get generation schedule: invalid scope")
	}
	results, err := surrealdb.Query[[]generationScheduleRec](ctx, s.db, `
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
	results, err := surrealdb.Query[[]generationScheduleRec](ctx, s.db, expandGenerationScheduleSQL, variables)
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
	results, err := surrealdb.Query[[]generationScheduleRec](ctx, s.db, `
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
	results, err := surrealdb.Query[[]generationChunkRec](ctx, s.db, `
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
	results, err := surrealdb.Query[[]generationTransitionRec](ctx, s.db, completeGenerationChunkSQL, map[string]any{
		"chunk":            generationChunkRecordID(chunk),
		"schedule":         models.NewRecordID("generation_schedule", strings.TrimPrefix(chunk.ScheduleDigest, "sha256:")),
		"current":          models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(chunk.Repository, chunk.Stage), "sha256:")),
		"repository_state": models.NewRecordID("generation_schedule_repository", strings.TrimPrefix(generationRepositoryID(chunk.Repository), "sha256:")),
		"digest":           chunk.ScheduleDigest, "lease": chunk.LeaseToken, "worker": chunk.ClaimedBy,
	})
	if err != nil {
		return fmt.Errorf("complete generation chunk: %w", err)
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
	results, err := surrealdb.Query[[]generationTransitionRec](ctx, s.db, retryGenerationChunkSQL, variables)
	if err != nil {
		return nil, fmt.Errorf("retry generation chunk: %w", err)
	}
	rows := generationTransitionRows(results)
	if len(rows) != 1 || !rows[0].Owned {
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

func (s *Surreal) ReleaseGenerationChunk(ctx context.Context, chunk GenerationChunk, reason string) error {
	if err := validGenerationChunkLease(chunk); err != nil {
		return err
	}
	results, err := surrealdb.Query[[]generationTransitionRec](ctx, s.db, releaseGenerationChunkSQL, map[string]any{
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

func (s *Surreal) ReapStaleGenerationChunks(
	ctx context.Context,
	class GenerationResourceClass,
	staleAfter time.Duration,
) (int, error) {
	if !validGenerationResourceClass(class) || staleAfter <= 0 {
		return 0, errors.New("reap generation chunks: invalid class or cutoff")
	}
	cutoff := time.Now().UTC().Add(-staleAfter)
	results, err := surrealdb.Query[[]generationChunkRec](ctx, s.db, `
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
		if releaseErr := s.ReleaseGenerationChunk(ctx, chunk, "stale worker lease reaped"); releaseErr != nil {
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
	results, err := surrealdb.Query[[]generationChunkRec](ctx, s.db,
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
