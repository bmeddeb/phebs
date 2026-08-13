package observationpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	ProgressSchema   = "phebs-observation-progress-v1"
	ProgressSchemaV2 = "phebs-observation-progress-v2"
)

type ProgressStore interface {
	GetGenerationSchedule(context.Context, string, string) (*store.GenerationSchedule, error)
}

type ProgressFailureStore interface {
	GetGenerationScheduleFailure(
		context.Context, string, string, string,
	) (*store.GenerationScheduleFailure, error)
}

type ProgressReadStage string

const (
	ProgressReadStageControl     ProgressReadStage = "control"
	ProgressReadStagePublication ProgressReadStage = "publication"
	ProgressReadStagePlanning    ProgressReadStage = "planning"
	ProgressReadStageSchedule    ProgressReadStage = "schedule"
	ProgressReadStageProjection  ProgressReadStage = "projection"
)

// ProgressReadError retains one closed source-free failure stage while
// preserving the typed cause for boundary classification. It never replaces
// an ErrStale, context, store, or immutable-corruption cause.
type ProgressReadError struct {
	Stage ProgressReadStage
	Err   error
}

func (failure *ProgressReadError) Error() string {
	if failure == nil {
		return "read observation progress"
	}
	return fmt.Sprintf("read observation progress %s: %v", failure.Stage, failure.Err)
}

func (failure *ProgressReadError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

func progressReadFailure(stage ProgressReadStage, err error) error {
	if err == nil {
		return nil
	}
	return &ProgressReadError{Stage: stage, Err: err}
}

// ProgressReader composes one fully validated cached publication with bounded
// source, marker, plan, and schedule controls. It never reads source blobs or
// observation members on a warm cache hit.
type ProgressReader struct {
	DataDir     string
	Store       ProgressStore
	Cache       *Cache
	InventoryV2 bool
}

type Progress struct {
	SchemaVersion          string               `json:"schema"`
	SelectedVersion        string               `json:"selected_version,omitempty"`
	Repository             string               `json:"repository"`
	State                  string               `json:"state"` // current | building | failed | stale | unavailable
	SourceGenerationDigest string               `json:"source_generation_digest"`
	Publication            *PublicationProgress `json:"publication,omitempty"`
	Planning               *PlanningProgress    `json:"planning,omitempty"`
	Schedule               *ScheduleProgress    `json:"schedule,omitempty"`
}

type PlanningProgress struct {
	State                  string                   `json:"state"` // active | settled | failed
	ScheduleGeneration     string                   `json:"schedule_generation"`
	TargetGeneration       string                   `json:"target_generation"`
	SourceGenerationDigest string                   `json:"source_generation_digest"`
	Pending                int                      `json:"pending"`
	Running                int                      `json:"running"`
	Succeeded              int                      `json:"succeeded"`
	Failed                 int                      `json:"failed"`
	Refusal                *pipelinerefusal.Receipt `json:"refusal,omitempty"`
}

type PublicationProgress struct {
	State                  string            `json:"state"` // current | stale
	GenerationDigest       string            `json:"generation_digest"`
	SourceGenerationDigest string            `json:"source_generation_digest"`
	RecordCount            int               `json:"record_count"`
	ObservedCount          int               `json:"observed_count"`
	UnsupportedCount       int               `json:"unsupported_count"`
	ReceiptState           string            `json:"receipt_state"` // complete | legacy_unavailable
	Receipt                *OperationReceipt `json:"receipt,omitempty"`
	SourceSegments         int               `json:"source_segments,omitempty"`
	InventorySegments      int               `json:"inventory_segments,omitempty"`
	EncodedMemberBytes     int64             `json:"encoded_member_bytes,omitempty"`
	ObservationBytes       int64             `json:"observation_bytes,omitempty"`
}

type ScheduleProgress struct {
	State                 string `json:"state"` // active | settled | failed
	ScheduleGeneration    string `json:"schedule_generation"`
	PublicationGeneration string `json:"publication_generation"`
	TotalPartitions       int    `json:"total_partitions"`
	Materialized          int    `json:"materialized"`
	Pending               int    `json:"pending"`
	Running               int    `json:"running"`
	Succeeded             int    `json:"succeeded"`
	Failed                int    `json:"failed"`
}

func (reader *ProgressReader) Read(ctx context.Context, repository string) (Progress, error) {
	if reader != nil && reader.InventoryV2 {
		return reader.readInventoryV2(ctx, repository)
	}
	if reader == nil || !filepath.IsAbs(reader.DataDir) || reader.Store == nil ||
		reader.Cache == nil || validateRepository(repository) != nil {
		return Progress{}, progressReadFailure(
			ProgressReadStageProjection, invalid("progress reader configuration"),
		)
	}
	source, err := repositoryindex.ReadSourceManifest(filepath.Join(reader.DataDir, "index"), repository)
	if err != nil {
		return Progress{}, progressReadFailure(ProgressReadStageControl, err)
	}
	root := filepath.Join(reader.DataDir, "observations")
	pointer, pointerPresent, err := readOptionalPointer(root, repository)
	if err != nil {
		return Progress{}, progressReadFailure(ProgressReadStageControl, err)
	}
	var publicationManifest *Manifest
	if pointerPresent {
		lease, acquireErr := reader.Cache.Acquire(ctx, root, repository)
		if acquireErr != nil {
			return Progress{}, progressReadFailure(ProgressReadStagePublication, acquireErr)
		}
		defer lease.Release()
		manifest := lease.Publication().Manifest()
		if manifest.GenerationDigest != pointer.GenerationDigest || manifest.Digest != pointer.ManifestDigest {
			return Progress{}, progressReadFailure(
				ProgressReadStagePublication,
				errors.Join(ErrStale, invalid("progress publication fence")),
			)
		}
		publicationManifest = &manifest
	}
	marker, markerPresent, err := readMarker(root, repository)
	if err != nil {
		return Progress{}, progressReadFailure(ProgressReadStageControl, err)
	}
	planning, planningPresent, err := reader.readSchedule(ctx, repository, PlanningScheduleStage)
	if err != nil {
		return Progress{}, progressReadFailure(ProgressReadStagePlanning, err)
	}
	planningProjection := planning
	var planningBindingValue planningBinding
	if planningPresent {
		runtime := Runtime{DataDir: reader.DataDir}
		planningBindingValue, err = runtime.readPlanningBinding(repository, planning.Generation)
		if err != nil {
			// Backup retains the durable settled schedule and current publication,
			// while observation-plans is an explicitly excluded rebuildable
			// namespace. Omit only that completed historical planning projection;
			// active, failed, corrupt, or publication-less states still fail closed.
			if errors.Is(err, os.ErrNotExist) &&
				planning.Status == store.GenerationScheduleSettled &&
				planning.TotalChunks == 1 && planning.Succeeded == 1 &&
				planning.Failed == 0 && publicationManifest != nil &&
				publicationManifest.SourceGenerationDigest == source.Digest {
				planningProjection = nil
			} else {
				return Progress{}, progressReadFailure(ProgressReadStagePlanning, err)
			}
		}
	}
	schedule, schedulePresent, err := reader.readSchedule(ctx, repository, ScheduleStage)
	if err != nil {
		return Progress{}, progressReadFailure(ProgressReadStageSchedule, err)
	}
	var targetGeneration, targetSource string
	if schedulePresent {
		runtime := Runtime{DataDir: reader.DataDir}
		scheduleTarget, targetErr := runtime.scheduleTarget(repository, schedule.Generation)
		switch {
		case markerPresent:
			if targetErr != nil || scheduleTarget != marker.GenerationDigest {
				return Progress{}, progressReadFailure(
					ProgressReadStageSchedule,
					errors.Join(targetErr, invalid("progress schedule target")),
				)
			}
			targetGeneration = marker.GenerationDigest
		case schedule.Status == store.GenerationScheduleActive &&
			(publicationManifest == nil || publicationManifest.SourceGenerationDigest != source.Digest):
			return Progress{}, progressReadFailure(
				ProgressReadStageSchedule,
				invalid("active progress schedule has no publication marker"),
			)
		case targetErr == nil && schedule.Status == store.GenerationScheduleSettled &&
			publicationManifest != nil && scheduleTarget == publicationManifest.GenerationDigest:
			targetGeneration = scheduleTarget
			targetSource = publicationManifest.SourceGenerationDigest
		}
	}
	if markerPresent {
		targetGeneration = marker.GenerationDigest
		plan, planErr := reader.readPlan(repository, targetGeneration)
		if planErr != nil {
			return Progress{}, progressReadFailure(ProgressReadStageControl, planErr)
		}
		targetSource = plan.SourceGenerationDigest
	}

	result := projectProgress(
		repository, source.Digest, publicationManifest,
		planningProjection, planningBindingValue, schedule, targetGeneration, targetSource,
	)
	if err := ValidateProgress(result); err != nil {
		return Progress{}, progressReadFailure(ProgressReadStageProjection, err)
	}
	if err := reader.confirm(
		ctx, repository, source.Digest, pointer, pointerPresent, marker, markerPresent,
		planning, planningPresent, schedule, schedulePresent,
	); err != nil {
		return Progress{}, progressReadFailure(ProgressReadStageControl, err)
	}
	return result, nil
}

func (reader *ProgressReader) readPlan(repository, generation string) (sourcepartition.Manifest, error) {
	directory := filepath.Join(
		reader.DataDir, "observation-plans", repositoryHash(repository),
		stringsTrimDigest(generation),
	)
	manifest, err := sourcepartition.ReadManifest(directory, repository)
	if err != nil {
		return sourcepartition.Manifest{}, err
	}
	want, err := GenerationDigest(manifest)
	if err != nil || want != generation {
		return sourcepartition.Manifest{}, errors.Join(err, invalid("progress plan generation"))
	}
	return manifest, nil
}

func (reader *ProgressReader) readSchedule(
	ctx context.Context, repository, stage string,
) (*store.GenerationSchedule, bool, error) {
	schedule, err := reader.Store.GetGenerationSchedule(ctx, repository, stage)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if schedule == nil || store.ValidateGenerationSchedule(*schedule) != nil {
		return nil, false, invalid("progress schedule")
	}
	value := *schedule
	return &value, true, nil
}

func (reader *ProgressReader) confirm(
	ctx context.Context,
	repository, sourceDigest string,
	pointer Pointer,
	pointerPresent bool,
	marker Marker,
	markerPresent bool,
	planning *store.GenerationSchedule,
	planningPresent bool,
	schedule *store.GenerationSchedule,
	schedulePresent bool,
) error {
	source, err := repositoryindex.ReadSourceManifest(filepath.Join(reader.DataDir, "index"), repository)
	if err != nil || source.Digest != sourceDigest {
		return errors.Join(err, ErrStale)
	}
	root := filepath.Join(reader.DataDir, "observations")
	confirmedPointer, confirmedPointerPresent, err := readOptionalPointer(root, repository)
	if err != nil || confirmedPointerPresent != pointerPresent || confirmedPointerPresent && confirmedPointer != pointer {
		return errors.Join(err, ErrStale)
	}
	confirmedMarker, confirmedMarkerPresent, err := readMarker(root, repository)
	if err != nil || confirmedMarkerPresent != markerPresent || confirmedMarkerPresent && confirmedMarker != marker {
		return errors.Join(err, ErrStale)
	}
	confirmedPlanning, confirmedPlanningPresent, err := reader.readSchedule(
		ctx, repository, PlanningScheduleStage,
	)
	if err != nil || confirmedPlanningPresent != planningPresent ||
		confirmedPlanningPresent && scheduleFenceOf(*confirmedPlanning) != scheduleFenceOf(*planning) {
		return errors.Join(err, ErrStale)
	}
	confirmedSchedule, confirmedSchedulePresent, err := reader.readSchedule(
		ctx, repository, ScheduleStage,
	)
	if err != nil || confirmedSchedulePresent != schedulePresent ||
		confirmedSchedulePresent && scheduleFenceOf(*confirmedSchedule) != scheduleFenceOf(*schedule) {
		return errors.Join(err, ErrStale)
	}
	return nil
}

func projectProgress(
	repository, sourceDigest string,
	manifest *Manifest,
	planning *store.GenerationSchedule,
	planningBindingValue planningBinding,
	schedule *store.GenerationSchedule,
	targetGeneration, targetSource string,
) Progress {
	result := Progress{
		SchemaVersion: ProgressSchema, Repository: repository, State: "unavailable",
		SourceGenerationDigest: sourceDigest,
	}
	if planning != nil {
		state := string(planning.Status)
		if planning.Status == store.GenerationScheduleSettled && planning.Failed > 0 {
			state = "failed"
		}
		result.Planning = &PlanningProgress{
			State: state, ScheduleGeneration: planning.Generation,
			TargetGeneration:       planningBindingValue.TargetGeneration,
			SourceGenerationDigest: planningBindingValue.SourceGenerationDigest,
			Pending:                planning.Pending, Running: planning.Running,
			Succeeded: planning.Succeeded, Failed: planning.Failed,
		}
	}
	publicationCurrent := false
	if manifest != nil {
		state := "stale"
		if manifest.SourceGenerationDigest == sourceDigest {
			state = "current"
			publicationCurrent = true
		}
		receiptState := "legacy_unavailable"
		var receipt *OperationReceipt
		if manifest.OperationReceipt != nil {
			receiptState = "complete"
			value := *manifest.OperationReceipt
			value.UnsupportedReasons = slices.Clone(value.UnsupportedReasons)
			receipt = &value
		}
		result.Publication = &PublicationProgress{
			State: state, GenerationDigest: manifest.GenerationDigest,
			SourceGenerationDigest: manifest.SourceGenerationDigest,
			RecordCount:            manifest.RecordCount, ObservedCount: manifest.ObservedCount,
			UnsupportedCount: manifest.UnsupportedCount,
			ReceiptState:     receiptState, Receipt: receipt,
		}
	}
	if schedule != nil && targetGeneration != "" {
		state := string(schedule.Status)
		if schedule.Status == store.GenerationScheduleSettled && schedule.Failed > 0 {
			state = "failed"
		}
		result.Schedule = &ScheduleProgress{
			State: state, ScheduleGeneration: schedule.Generation,
			PublicationGeneration: targetGeneration,
			TotalPartitions:       schedule.TotalChunks, Materialized: schedule.Materialized,
			Pending: schedule.Pending, Running: schedule.Running,
			Succeeded: schedule.Succeeded, Failed: schedule.Failed,
		}
	}
	switch {
	case publicationCurrent:
		result.State = "current"
	case result.Planning != nil && result.Planning.SourceGenerationDigest == sourceDigest && result.Planning.State == "active":
		result.State = "building"
	case result.Planning != nil && result.Planning.SourceGenerationDigest == sourceDigest && result.Planning.State == "failed":
		result.State = "failed"
	case result.Schedule != nil && targetSource == sourceDigest && result.Schedule.State == "active":
		result.State = "building"
	case result.Schedule != nil && targetSource == sourceDigest && result.Schedule.State == "failed":
		result.State = "failed"
	case result.Publication != nil ||
		result.Planning != nil && result.Planning.SourceGenerationDigest != sourceDigest ||
		targetSource != "" && targetSource != sourceDigest:
		result.State = "stale"
	}
	return result
}

func ValidateProgress(progress Progress) error {
	if (progress.SchemaVersion != ProgressSchema && progress.SchemaVersion != ProgressSchemaV2) ||
		validateRepository(progress.Repository) != nil ||
		!validDigest(progress.SourceGenerationDigest) {
		return invalid("progress identity")
	}
	selectedV2 := progress.SchemaVersion == ProgressSchemaV2
	if selectedV2 && progress.SelectedVersion != "v2" ||
		!selectedV2 && progress.SelectedVersion != "" {
		return invalid("progress selected version")
	}
	switch progress.State {
	case "current", "building", "failed", "stale", "unavailable":
	default:
		return invalid("progress state")
	}
	if progress.Publication != nil {
		publication := progress.Publication
		maxRecords := MaxGenerationRecords
		if selectedV2 {
			maxRecords = MaxInventoryRecordsV2
		}
		if publication.State != "current" && publication.State != "stale" ||
			!validDigest(publication.GenerationDigest) || !validDigest(publication.SourceGenerationDigest) ||
			publication.RecordCount < 0 || publication.RecordCount > maxRecords ||
			publication.ObservedCount < 0 || publication.UnsupportedCount < 0 ||
			publication.RecordCount != publication.ObservedCount+publication.UnsupportedCount {
			return invalid("progress publication")
		}
		if selectedV2 {
			if publication.SourceSegments < 0 || publication.SourceSegments > sourcepartition.MaxSegments ||
				publication.InventorySegments < 0 || publication.InventorySegments > MaxInventorySegmentsV2 ||
				publication.EncodedMemberBytes < 0 || publication.EncodedMemberBytes > MaxGenerationBytes ||
				publication.ObservationBytes < 0 || publication.ObservationBytes > MaxGenerationBytes {
				return invalid("progress v2 publication")
			}
		} else if publication.SourceSegments != 0 || publication.InventorySegments != 0 ||
			publication.EncodedMemberBytes != 0 || publication.ObservationBytes != 0 {
			return invalid("legacy progress publication")
		}
		switch publication.ReceiptState {
		case "complete":
			if publication.Receipt == nil || validateProgressReceipt(*publication.Receipt, *publication) != nil {
				return invalid("progress receipt")
			}
		case "legacy_unavailable":
			if publication.Receipt != nil {
				return invalid("legacy progress receipt")
			}
		default:
			return invalid("progress receipt state")
		}
		if publication.State == "current" && publication.SourceGenerationDigest != progress.SourceGenerationDigest ||
			publication.State == "stale" && publication.SourceGenerationDigest == progress.SourceGenerationDigest {
			return invalid("progress publication currency")
		}
	}
	if progress.Planning != nil {
		planning := progress.Planning
		target, _, targetErr := planningTarget(
			progress.Repository, planning.SourceGenerationDigest,
		)
		retainedCurrentPlanning := selectedV2 && planning.State == "settled" &&
			planning.ScheduleGeneration == "" && progress.State == "current" &&
			progress.Publication != nil && progress.Publication.State == "current" &&
			progress.Publication.SourceGenerationDigest == planning.SourceGenerationDigest
		if planning.State != "active" && planning.State != "settled" && planning.State != "failed" ||
			(!validDigest(planning.ScheduleGeneration) && !retainedCurrentPlanning) ||
			!validDigest(planning.TargetGeneration) ||
			!validDigest(planning.SourceGenerationDigest) ||
			targetErr != nil || planning.TargetGeneration != target ||
			planning.Pending < 0 || planning.Pending > 1 || planning.Running < 0 || planning.Running > 1 ||
			planning.Succeeded < 0 || planning.Succeeded > 1 || planning.Failed < 0 || planning.Failed > 1 ||
			planning.Pending+planning.Running+planning.Succeeded+planning.Failed > 1 {
			return invalid("progress planning projection")
		}
		if planning.State == "active" && (planning.Succeeded != 0 || planning.Failed != 0) ||
			planning.State == "settled" && (planning.Pending != 0 || planning.Running != 0 ||
				planning.Succeeded != 1 || planning.Failed != 0) ||
			planning.State == "failed" && (planning.Pending != 0 || planning.Running != 0 ||
				planning.Succeeded != 0 || planning.Failed != 1) {
			return invalid("progress planning state")
		}
		if planning.Refusal != nil {
			if !selectedV2 || planning.State != "failed" ||
				pipelinerefusal.Validate(*planning.Refusal) != nil ||
				planning.Refusal.Stage != pipelinerefusal.StageObservationPublication ||
				planning.Refusal.GenerationKind != pipelinerefusal.GenerationObservation {
				return invalid("progress planning refusal")
			}
		}
	}
	if selectedV2 && progress.Schedule != nil {
		return invalid("progress v2 legacy schedule")
	}
	if progress.Schedule != nil {
		schedule := progress.Schedule
		if schedule.State != "active" && schedule.State != "settled" && schedule.State != "failed" ||
			!validDigest(schedule.ScheduleGeneration) || !validDigest(schedule.PublicationGeneration) ||
			schedule.TotalPartitions < 1 || schedule.TotalPartitions > store.MaxGenerationChunks ||
			schedule.Materialized < 0 || schedule.Pending < 0 || schedule.Running < 0 ||
			schedule.Succeeded < 0 || schedule.Failed < 0 ||
			schedule.Succeeded+schedule.Failed > schedule.TotalPartitions {
			return invalid("progress schedule projection")
		}
		if schedule.State == "failed" && schedule.Failed == 0 ||
			schedule.State == "settled" && schedule.Failed != 0 {
			return invalid("progress schedule state")
		}
	}
	switch progress.State {
	case "current":
		if progress.Publication == nil || progress.Publication.State != "current" {
			return invalid("current progress authority")
		}
	case "building":
		if (progress.Planning == nil || progress.Planning.State != "active" ||
			progress.Planning.SourceGenerationDigest != progress.SourceGenerationDigest) &&
			(progress.Schedule == nil || progress.Schedule.State != "active") {
			return invalid("building progress authority")
		}
	case "failed":
		if (progress.Planning == nil || progress.Planning.State != "failed" ||
			progress.Planning.SourceGenerationDigest != progress.SourceGenerationDigest) &&
			(progress.Schedule == nil || progress.Schedule.State != "failed") {
			return invalid("failed progress authority")
		}
	case "unavailable":
		if progress.Publication != nil && progress.Publication.State == "current" ||
			progress.Planning != nil && (progress.Planning.State == "active" || progress.Planning.State == "failed") ||
			progress.Schedule != nil && (progress.Schedule.State == "active" || progress.Schedule.State == "failed") {
			return invalid("unavailable progress authority")
		}
	}
	return nil
}

func validateProgressReceipt(receipt OperationReceipt, publication PublicationProgress) error {
	manifest := Manifest{
		RecordCount: publication.RecordCount, ObservedCount: publication.ObservedCount,
		UnsupportedCount: publication.UnsupportedCount,
	}
	return validateOperationReceipt(receipt, manifest)
}

func readOptionalPointer(root, repository string) (Pointer, bool, error) {
	pointer, err := readPointer(root, repository)
	if errors.Is(err, os.ErrNotExist) {
		return Pointer{}, false, nil
	}
	return pointer, err == nil, err
}

type scheduleFence struct {
	Digest       string
	Generation   string
	Status       store.GenerationScheduleStatus
	NextOffset   int64
	Materialized int
	Pending      int
	Running      int
	Succeeded    int
	Failed       int
	UpdatedAt    time.Time
}

func scheduleFenceOf(schedule store.GenerationSchedule) scheduleFence {
	return scheduleFence{
		Digest: schedule.Digest, Generation: schedule.Generation, Status: schedule.Status,
		NextOffset: schedule.NextOffset, Materialized: schedule.Materialized,
		Pending: schedule.Pending, Running: schedule.Running,
		Succeeded: schedule.Succeeded, Failed: schedule.Failed, UpdatedAt: schedule.UpdatedAt,
	}
}

func stringsTrimDigest(value string) string {
	if len(value) == len("sha256:")+64 && value[:len("sha256:")] == "sha256:" {
		return value[len("sha256:"):]
	}
	return value
}
