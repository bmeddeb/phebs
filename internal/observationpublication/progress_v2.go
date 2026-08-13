package observationpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

// readInventoryV2 projects the selected segmented observation route directly.
// It reads only the source manifest, one current pointer, one bounded inventory
// root, one one-item schedule, and (only after terminal settlement) one closed
// failed-attempt receipt. It never scans source or observation members.
func (reader *ProgressReader) readInventoryV2(
	ctx context.Context, repository string,
) (Progress, error) {
	if reader == nil || !filepath.IsAbs(reader.DataDir) || reader.Store == nil ||
		validateRepository(repository) != nil {
		return Progress{}, progressReadFailure(
			ProgressReadStageProjection, invalid("inventory v2 progress reader configuration"),
		)
	}
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(reader.DataDir, "index"), repository,
	)
	if err != nil {
		return Progress{}, progressReadFailure(ProgressReadStageControl, err)
	}
	root := filepath.Join(reader.DataDir, "observations")
	selected, selectedPresent, err := readOptionalInventoryPublicationRootV2(root, repository)
	if err != nil {
		return Progress{}, progressReadFailure(ProgressReadStageControl, err)
	}
	var publication *InventoryRootV2
	if selectedPresent {
		directory := inventoryGenerationDirectoryV2(
			root, repository, selected.Current.GenerationDigest,
		)
		value, readErr := ReadInventoryRootV2(
			filepath.Join(directory, InventoryPublicationInventoryNameV2), repository,
		)
		if readErr != nil || !inventoryProgressReferenceMatches(selected.Current, value) {
			return Progress{}, progressReadFailure(
				ProgressReadStagePublication,
				errors.Join(readErr, invalid("inventory v2 progress publication fence")),
			)
		}
		publication = &value
	}
	schedule, schedulePresent, err := reader.readSchedule(
		ctx, repository, InventoryScheduleStageV2,
	)
	if err != nil {
		return Progress{}, progressReadFailure(ProgressReadStagePlanning, err)
	}
	projectionSchedule := schedule
	var binding planningBinding
	if schedulePresent {
		runtime := Runtime{DataDir: reader.DataDir}
		binding, err = runtime.readPlanningBinding(repository, schedule.Generation)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) &&
				schedule.Status == store.GenerationScheduleSettled &&
				schedule.TotalChunks == 1 && schedule.Succeeded == 1 && schedule.Failed == 0 &&
				publication != nil && publication.SourceGenerationDigest == source.Digest {
				projectionSchedule = nil
			} else {
				return Progress{}, progressReadFailure(ProgressReadStagePlanning, err)
			}
		}
	}
	var failure *store.GenerationScheduleFailure
	if projectionSchedule != nil && projectionSchedule.Status == store.GenerationScheduleSettled &&
		projectionSchedule.Failed > 0 {
		failureStore, ok := reader.Store.(ProgressFailureStore)
		if !ok {
			return Progress{}, progressReadFailure(
				ProgressReadStageProjection,
				invalid("inventory v2 progress failure reader configuration"),
			)
		}
		failure, err = failureStore.GetGenerationScheduleFailure(
			ctx, repository, InventoryScheduleStageV2, projectionSchedule.Digest,
		)
		if err != nil || failure == nil ||
			failure.ScheduleDigest != projectionSchedule.Digest ||
			failure.Generation != projectionSchedule.Generation {
			return Progress{}, progressReadFailure(
				ProgressReadStagePlanning,
				errors.Join(err, invalid("inventory v2 progress terminal failure")),
			)
		}
	}
	result := projectInventoryProgressV2(
		repository, source.Digest, publication, selected, selectedPresent,
		projectionSchedule, binding, failure,
	)
	if err := ValidateProgress(result); err != nil {
		return Progress{}, progressReadFailure(ProgressReadStageProjection, err)
	}
	if err := reader.confirmInventoryProgressV2(
		ctx, repository, source.Digest, selected, selectedPresent,
		schedule, schedulePresent,
	); err != nil {
		return Progress{}, progressReadFailure(ProgressReadStageControl, err)
	}
	return result, nil
}

func projectInventoryProgressV2(
	repository, sourceDigest string,
	publication *InventoryRootV2,
	selected InventoryPublicationRootV2,
	selectedPresent bool,
	schedule *store.GenerationSchedule,
	binding planningBinding,
	failure *store.GenerationScheduleFailure,
) Progress {
	result := Progress{
		SchemaVersion: ProgressSchemaV2, SelectedVersion: "v2",
		Repository: repository, State: "unavailable",
		SourceGenerationDigest: sourceDigest,
	}
	publicationCurrent := false
	if publication != nil && selectedPresent {
		state := "stale"
		if publication.SourceGenerationDigest == sourceDigest {
			state = "current"
			publicationCurrent = true
		}
		receipt := publication.OperationReceipt
		receipt.UnsupportedReasons = slices.Clone(receipt.UnsupportedReasons)
		result.Publication = &PublicationProgress{
			State: state, GenerationDigest: publication.GenerationDigest,
			SourceGenerationDigest: publication.SourceGenerationDigest,
			RecordCount:            publication.RecordCount, ObservedCount: publication.ObservedCount,
			UnsupportedCount: publication.UnsupportedCount,
			ReceiptState:     "complete", Receipt: &receipt,
			SourceSegments:     selected.Current.SourceSegments,
			InventorySegments:  selected.Current.InventorySegments,
			EncodedMemberBytes: selected.Current.EncodedBytes,
			ObservationBytes:   selected.Current.ObservationBytes,
		}
	}
	if schedule != nil {
		state := string(schedule.Status)
		if schedule.Status == store.GenerationScheduleSettled && schedule.Failed > 0 {
			state = "failed"
		}
		result.Planning = &PlanningProgress{
			State: state, ScheduleGeneration: schedule.Generation,
			TargetGeneration:       binding.TargetGeneration,
			SourceGenerationDigest: binding.SourceGenerationDigest,
			Pending:                schedule.Pending, Running: schedule.Running,
			Succeeded: schedule.Succeeded, Failed: schedule.Failed,
		}
		if failure != nil && failure.Refusal != nil {
			refusal := *failure.Refusal
			result.Planning.Refusal = &refusal
		}
	}
	// A current immutable publication is the durable proof that its one-item
	// planning target settled successfully. Generation-schedule lifecycle may
	// collect the completed row, so retain that closed fact without inventing a
	// historical recovery-schedule identity that the publication does not bind.
	if publicationCurrent && result.Planning == nil {
		target, _, err := planningTarget(repository, sourceDigest)
		if err == nil {
			result.Planning = &PlanningProgress{
				State: "settled", TargetGeneration: target,
				SourceGenerationDigest: sourceDigest, Succeeded: 1,
			}
		}
	}
	switch {
	case publicationCurrent:
		result.State = "current"
	case result.Planning != nil &&
		result.Planning.SourceGenerationDigest == sourceDigest &&
		result.Planning.State == "active":
		result.State = "building"
	case result.Planning != nil &&
		result.Planning.SourceGenerationDigest == sourceDigest &&
		result.Planning.State == "failed":
		result.State = "failed"
	case result.Publication != nil || result.Planning != nil &&
		result.Planning.SourceGenerationDigest != sourceDigest:
		result.State = "stale"
	}
	return result
}

func (reader *ProgressReader) confirmInventoryProgressV2(
	ctx context.Context,
	repository, sourceDigest string,
	selected InventoryPublicationRootV2,
	selectedPresent bool,
	schedule *store.GenerationSchedule,
	schedulePresent bool,
) error {
	source, err := repositoryindex.ReadSourceManifest(
		filepath.Join(reader.DataDir, "index"), repository,
	)
	if err != nil || source.Digest != sourceDigest {
		return errors.Join(err, ErrStale)
	}
	confirmed, confirmedPresent, err := readOptionalInventoryPublicationRootV2(
		filepath.Join(reader.DataDir, "observations"), repository,
	)
	if err != nil || confirmedPresent != selectedPresent ||
		confirmedPresent && confirmed.Current != selected.Current {
		return errors.Join(err, ErrStale)
	}
	confirmedSchedule, confirmedSchedulePresent, err := reader.readSchedule(
		ctx, repository, InventoryScheduleStageV2,
	)
	if err != nil || confirmedSchedulePresent != schedulePresent ||
		confirmedSchedulePresent &&
			scheduleFenceOf(*confirmedSchedule) != scheduleFenceOf(*schedule) {
		return errors.Join(err, ErrStale)
	}
	return nil
}

func readOptionalInventoryPublicationRootV2(
	root, repository string,
) (InventoryPublicationRootV2, bool, error) {
	selected, err := ReadInventoryPublicationRootV2(root, repository)
	if errors.Is(err, os.ErrNotExist) {
		return InventoryPublicationRootV2{}, false, nil
	}
	return selected, err == nil, err
}

func inventoryProgressReferenceMatches(
	reference InventoryGenerationRefV2,
	root InventoryRootV2,
) bool {
	return reference.GenerationDigest == root.GenerationDigest &&
		reference.InventoryDigest == root.Digest &&
		reference.InventorySegments == len(root.Segments) &&
		reference.Records == root.RecordCount &&
		reference.EncodedBytes == root.EncodedMemberBytes &&
		reference.ObservationBytes == root.ObservationBytes
}
