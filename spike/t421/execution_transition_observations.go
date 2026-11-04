package t421

import (
	"slices"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/store"
)

type executionTransitionObservations struct {
	physical              epochRetentionObservation
	activationHit         epochActivationObservation
	activationRecovered   epochActivationObservation
	markerHit             epochMarkerObservation
	markerRecovered       epochMarkerObservation
	staleHit              extractionpublication.StaleLeaseTransition
	staleRecovered        extractionpublication.StaleLeaseTransition
	stalePreparation      epochStalePreparation
	checkpointPreparation epochStalePreparation
	checkpointHit         extractionpublication.CheckpointRestartTransition
	checkpointRecovered   extractionpublication.CheckpointRestartTransition
	checkpointSettlement  store.GenerationScheduleSettlement
	pressure              epochPressureObservations
	archiveManifest       *recovery.ArchiveTransitionManifest
	collectionCycle       lifecycle.CycleObservation
}

func cloneExecutionTransitionObservations(value executionTransitionObservations) executionTransitionObservations {
	if value.stalePreparation.Operations != nil {
		operation := *value.stalePreparation.Operations
		value.stalePreparation.Operations = &operation
	}
	if value.checkpointPreparation.Operations != nil {
		operation := *value.checkpointPreparation.Operations
		value.checkpointPreparation.Operations = &operation
	}
	if value.stalePreparation.Workspace != nil {
		workspace := *value.stalePreparation.Workspace
		value.stalePreparation.Workspace = &workspace
	}
	if value.checkpointPreparation.Workspace != nil {
		workspace := *value.checkpointPreparation.Workspace
		value.checkpointPreparation.Workspace = &workspace
	}
	value.pressure.normal.Owners = slices.Clone(value.pressure.normal.Owners)
	value.pressure.recovery.Owners = slices.Clone(value.pressure.recovery.Owners)
	value.collectionCycle.Owners = slices.Clone(value.collectionCycle.Owners)
	if value.archiveManifest != nil {
		manifest := *value.archiveManifest
		manifest.Components = slices.Clone(value.archiveManifest.Components)
		manifest.Reports = slices.Clone(value.archiveManifest.Reports)
		value.archiveManifest = &manifest
	}
	return value
}
