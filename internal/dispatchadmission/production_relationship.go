package dispatchadmission

import (
	"context"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func ObserveProductionRelationship(ctx context.Context, event readaccounting.RelationshipEvent) error {
	selected := ProductionSemanticSelected()
	err := readaccounting.ObserveRelationship(ctx, selected, event)
	if err != nil && selected {
		if lifetime := productionRuntime.Load(); lifetime != nil && lifetime.client != nil {
			return lifetime.client.fail(ErrProtocol)
		}
		return ErrProductionBootstrap
	}
	return err
}
