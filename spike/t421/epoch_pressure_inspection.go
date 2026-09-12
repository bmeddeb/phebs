package t421

import (
	"context"
	"net/http"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func pressureInspectionPhase(phase string) bool {
	return phase == "pressure_80" || phase == "pressure_90" || phase == "pressure_75"
}

func (reader *executionEpochInspection) pressureFinalReady() bool {
	if reader.run == nil || !reader.run.pressureAllowed || reader.run.epoch.Epoch != 4 || reader.pressureBaseline == nil ||
		reader.lifecycleCalls < reader.bounds.LifecycleStatusCalls.Minimum || reader.lifecycleCalls > reader.bounds.LifecycleStatusCalls.Maximum {
		return false
	}
	switch reader.projection.Phase {
	case "pressure_80":
		return reader.pressure.step == 4
	case "pressure_90":
		return reader.pressure.step == 5
	case "pressure_75":
		return reader.pressure.step == 9
	}
	return false
}

// L is the ordinary bounded in-memory status snapshot, not the transition R
// evidence or a claim of a successful/drained cycle. The existing zero-unit
// accounting trailer and shared ordinal remain mandatory even for zero reads.
func (reader *executionEpochInspection) LifecycleStatus(ctx context.Context) (result lifecycle.Status, report epochInspectionReport, retErr error) {
	if reader == nil {
		return result, report, errEpochInspection
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	allowed := reader.run != nil && (reader.run.pressureAllowed && reader.run.epoch.Epoch == 4 &&
		(reader.projection.Phase == "pressure_80" || reader.projection.Phase == "pressure_75") ||
		reader.run.epoch.Epoch == 5 && reader.projection.Phase == "lifecycle_collection" && reader.restoredStep == 3 &&
			reader.restoredSamples.ArchiveComplete && reader.archiveAuthority.Phase == "archive_restore")
	if reader.err != nil || !allowed || reader.finalUsed ||
		reader.lifecycleCalls >= reader.bounds.LifecycleStatusCalls.Maximum {
		return result, report, errEpochInspection
	}
	reader.lifecycleCalls++
	raw, status, report, err := reader.read(ctx, api.LifecycleStatusPath, api.LifecycleStatusResponseLimit, epochInspectionReport{})
	if err != nil || status != http.StatusOK {
		return result, report, errEpochInspection
	}
	value := struct {
		Schema string `json:"$schema"`
		lifecycle.Status
	}{}
	if decodeEpochJSON(raw, &value, false) != nil || value.Schema != "http://"+reader.run.epoch.Listen+"/schemas/Status.json" ||
		lifecycle.ValidateSelectedCleanupStatus(value.Status) != nil || !value.Policy.Enabled {
		return result, report, errEpochInspection
	}
	names := correctedLifecycleOwners()
	if len(value.Owners) != len(names) {
		return result, report, errEpochInspection
	}
	for i, name := range names {
		if value.Owners[i].Name != name {
			return result, report, errEpochInspection
		}
	}
	return value.Status, report, nil
}
