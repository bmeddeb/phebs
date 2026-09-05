package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t422MarkerHitPath       = "/api/t422/return-a-marker/hit"
	t422MarkerRecoveredPath = "/api/t422/return-a-marker/recovered"
)

var errT422ReadCompletion = errors.New("T42.2 native read completion failed")

// Main binds only the concrete native control before exposing either shared
// exact transport. Requests can select neither a target nor an arbitrary read.
func (state *t421ExactReadAccountingState) markerRead(request *http.Request) func(context.Context) ([]byte, func(error), error) {
	if state.marker == nil || state.semantic == nil || state.marker.launch != state.semantic ||
		request == nil || request.URL == nil {
		return nil
	}
	if request.URL.Path != t422MarkerHitPath && request.URL.Path != t422MarkerRecoveredPath ||
		request.Method != http.MethodGet ||
		request.URL.Path != request.URL.EscapedPath() || request.URL.RawQuery != "" || request.URL.ForceQuery ||
		request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		return nil
	}
	if request.URL.Path == t422MarkerHitPath {
		return state.marker.ReadHit
	}
	return state.marker.ReadRecovered
}

func t422CompleteReadReport(finish func(error), terminalErr error) (err error) {
	defer func() {
		if recover() != nil {
			err = errT422ReadCompletion
		}
	}()
	finish(terminalErr)
	return nil
}

// Only phase six owns the one-shot marker continuation. Later phases retain
// ordinary HandleV3/Advance; the fixed installed hook still refuses any new
// marker, because stale/restart must preserve the established authority.
func (control *t422MarkerControl) handleInEpoch(ctx context.Context, chunk store.GenerationChunk) error {
	if ctx == nil || ctx.Err() != nil || chunk.Repository != control.launch.request.Repository ||
		chunk.Stage != relationshippublication.ScheduleStageV3 {
		return control.stop(errT422MarkerControl)
	}
	current, err := dispatchadmission.ProductionSemanticState()
	selected, selectionErr := t422MarkerPhaseRoute(control.launch, current)
	if err != nil || selectionErr != nil {
		return control.stop(errT422MarkerControl)
	}
	if selected {
		return control.HandleV3(ctx, chunk)
	}
	if err := control.runtime.HandleV3(ctx, chunk); err != nil {
		return err
	}
	return control.services.Advance(ctx, chunk.Repository)
}

func t422MarkerPhaseRoute(launch *t422SemanticLaunch, current dispatchadmission.ProductionSemanticSnapshot) (bool, error) {
	if launch == nil || launch.request.ServerEpoch != 3 || !launch.matches(current) {
		return false, errT422MarkerControl
	}
	switch current.Phase {
	case 6:
		return true, nil
	case 7, 8:
		return false, nil
	default:
		return false, errT422MarkerControl
	}
}
