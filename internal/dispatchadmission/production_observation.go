package dispatchadmission

import (
	"context"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// ObserveProductionParsedBlob binds the existing native successful ParsedBlobs
// event. Missing or failed selected coverage is sticky even when a worker
// otherwise handles its returned error as an ordinary retry.
func ObserveProductionParsedBlob(ctx context.Context) error {
	selected := ProductionSemanticSelected()
	err := readaccounting.ObserveParsedBlob(ctx, selected)
	if err != nil && selected {
		if lifetime := productionRuntime.Load(); lifetime != nil && lifetime.client != nil {
			return lifetime.client.fail(ErrProtocol)
		}
		return ErrProductionBootstrap
	}
	return err
}
