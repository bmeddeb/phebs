package api

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/store"
)

type ResourcePlaneState string

const (
	ResourcePlaneEnabled       ResourcePlaneState = "enabled"
	ResourcePlaneUnsupported   ResourcePlaneState = "unsupported"
	ResourcePlaneFailed        ResourcePlaneState = "failed"
	ResourcePlaneStale         ResourcePlaneState = "stale"
	ResourcePlaneHumanAsserted ResourcePlaneState = "human_asserted"
)

type ResourcePlaneContext struct {
	InvestigationID string                               `json:"investigation_id"`
	RevisionID      string                               `json:"revision_id"`
	TicketKind      store.ChangeBriefTicketKind          `json:"ticket_kind"`
	Selections      []store.ChangeBriefContractSelection `json:"selections"`
}

type ResourcePlaneRelationship struct {
	Kind           string                  `json:"kind"`
	Subject        string                  `json:"subject"`
	Object         string                  `json:"object"`
	Classification string                  `json:"classification"`
	Sources        []ContractCatalogSource `json:"sources"`
}

type ResourcePlanePackSnapshot struct {
	State         ResourcePlaneState          `json:"state"`
	Reason        string                      `json:"reason,omitempty"`
	Relationships []ResourcePlaneRelationship `json:"relationships"`
}

// ResourcePlanePack is the explicit real-pack boundary. A registry entry
// without one may describe unsupported, failed, stale, or human-asserted
// state, but the registry strips every relationship from such entries.
type ResourcePlanePack interface {
	ReadResourcePlane(
		context.Context,
		ResourcePlaneContext,
	) (ResourcePlanePackSnapshot, error)
}

type ResourcePlaneRegistration struct {
	ID     string
	Label  string
	State  ResourcePlaneState
	Reason string
	Pack   ResourcePlanePack
}

type ResourcePlaneSnapshot struct {
	ID            string                      `json:"id"`
	Label         string                      `json:"label"`
	State         ResourcePlaneState          `json:"state"`
	Reason        string                      `json:"reason,omitempty"`
	Relationships []ResourcePlaneRelationship `json:"relationships"`
}

type ResourcePlaneRegistry struct {
	registrations []ResourcePlaneRegistration
}

func NewResourcePlaneRegistry(
	registrations []ResourcePlaneRegistration,
) (*ResourcePlaneRegistry, error) {
	registrations = slices.Clone(registrations)
	sort.Slice(registrations, func(left, right int) bool {
		return registrations[left].ID < registrations[right].ID
	})
	for index := range registrations {
		registration := &registrations[index]
		if strings.TrimSpace(registration.ID) != registration.ID ||
			registration.ID == "" ||
			strings.TrimSpace(registration.Label) != registration.Label ||
			registration.Label == "" ||
			!validResourcePlaneState(registration.State) {
			return nil, errors.New("resource plane registration is invalid")
		}
		if index > 0 &&
			registrations[index-1].ID == registration.ID {
			return nil, fmt.Errorf(
				"resource plane %q is registered more than once",
				registration.ID,
			)
		}
		if registration.Pack == nil &&
			registration.State == ResourcePlaneEnabled {
			return nil, fmt.Errorf(
				"enabled resource plane %q requires a real pack",
				registration.ID,
			)
		}
	}
	return &ResourcePlaneRegistry{registrations: registrations}, nil
}

func DefaultWorkbenchResourcePlanes() *ResourcePlaneRegistry {
	registry, _ := NewResourcePlaneRegistry(
		[]ResourcePlaneRegistration{
			{
				ID: "document-store", Label: "Document store",
				State:  ResourcePlaneUnsupported,
				Reason: "workbench_pack_not_enabled",
			},
			{
				ID: "kafka", Label: "Kafka",
				State:  ResourcePlaneUnsupported,
				Reason: "workbench_pack_not_enabled",
			},
			{
				ID: "redis", Label: "Redis",
				State:  ResourcePlaneUnsupported,
				Reason: "workbench_pack_not_available",
			},
			{
				ID: "runtime", Label: "Runtime",
				State:  ResourcePlaneUnsupported,
				Reason: "runtime_evidence_not_available",
			},
			{
				ID: "sql", Label: "SQL",
				State:  ResourcePlaneUnsupported,
				Reason: "workbench_pack_not_available",
			},
		},
	)
	return registry
}

func (registry *ResourcePlaneRegistry) Snapshot(
	ctx context.Context,
	input ResourcePlaneContext,
) []ResourcePlaneSnapshot {
	if registry == nil {
		return []ResourcePlaneSnapshot{}
	}
	result := make(
		[]ResourcePlaneSnapshot,
		0,
		len(registry.registrations),
	)
	for _, registration := range registry.registrations {
		snapshot := ResourcePlaneSnapshot{
			ID:            registration.ID,
			Label:         registration.Label,
			State:         registration.State,
			Reason:        registration.Reason,
			Relationships: []ResourcePlaneRelationship{},
		}
		if registration.Pack != nil {
			value, err := registration.Pack.ReadResourcePlane(ctx, input)
			if err != nil {
				snapshot.State = ResourcePlaneFailed
				snapshot.Reason = "resource_plane_read_failed"
			} else if validResourcePlanePackState(value.State) {
				snapshot.State = value.State
				snapshot.Reason = value.Reason
				if value.State == ResourcePlaneEnabled {
					relationships, relationErr :=
						canonicalResourcePlaneRelationships(
							value.Relationships,
						)
					if relationErr != nil {
						snapshot.State = ResourcePlaneFailed
						snapshot.Reason =
							"resource_plane_relationships_invalid"
					} else {
						snapshot.Relationships = relationships
					}
				}
			} else {
				snapshot.State = ResourcePlaneFailed
				snapshot.Reason = "resource_plane_state_invalid"
			}
		}
		result = append(result, snapshot)
	}
	return result
}

func validResourcePlaneState(state ResourcePlaneState) bool {
	return state == ResourcePlaneEnabled ||
		state == ResourcePlaneUnsupported ||
		state == ResourcePlaneFailed ||
		state == ResourcePlaneStale ||
		state == ResourcePlaneHumanAsserted
}

func validResourcePlanePackState(state ResourcePlaneState) bool {
	return state == ResourcePlaneEnabled ||
		state == ResourcePlaneFailed ||
		state == ResourcePlaneStale
}

func canonicalResourcePlaneRelationships(
	relationships []ResourcePlaneRelationship,
) ([]ResourcePlaneRelationship, error) {
	result := make(
		[]ResourcePlaneRelationship,
		len(relationships),
	)
	for index, relationship := range relationships {
		for _, value := range []string{
			relationship.Kind,
			relationship.Subject,
			relationship.Object,
			relationship.Classification,
		} {
			if value == "" || strings.TrimSpace(value) != value {
				return nil, errors.New(
					"resource plane relationship identity is invalid",
				)
			}
		}
		result[index] = relationship
		result[index].Sources = slices.Clone(relationship.Sources)
		sort.Slice(result[index].Sources, func(left, right int) bool {
			return resourcePlaneSourceKey(
				result[index].Sources[left],
			) < resourcePlaneSourceKey(result[index].Sources[right])
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return resourcePlaneRelationshipKey(result[left]) <
			resourcePlaneRelationshipKey(result[right])
	})
	for index := 1; index < len(result); index++ {
		if resourcePlaneRelationshipKey(result[index-1]) ==
			resourcePlaneRelationshipKey(result[index]) {
			return nil, errors.New(
				"resource plane relationships contain a duplicate",
			)
		}
	}
	return result, nil
}

func resourcePlaneRelationshipKey(
	relationship ResourcePlaneRelationship,
) string {
	return strings.Join([]string{
		relationship.Kind,
		relationship.Subject,
		relationship.Object,
		relationship.Classification,
		digestJSON(relationship.Sources),
	}, "\x00")
}

func resourcePlaneSourceKey(source ContractCatalogSource) string {
	return strings.Join([]string{
		source.Repository,
		source.Commit,
		source.Path,
		fmt.Sprintf("%010d", source.StartByte),
		fmt.Sprintf("%010d", source.EndByte),
		source.AtomID,
	}, "\x00")
}
