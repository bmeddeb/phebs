package api

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	resourcePlaneMaxRelationships = 200
	resourcePlaneMaxSources       = 16
	resourcePlaneMaxPathBytes     = 4 << 10
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
	visible func(string) bool,
) ([]ResourcePlaneSnapshot, error) {
	if registry == nil {
		return []ResourcePlaneSnapshot{}, nil
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
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				snapshot.State = ResourcePlaneFailed
				snapshot.Reason = "resource_plane_read_failed"
			} else if validResourcePlanePackState(value.State) {
				snapshot.State = value.State
				snapshot.Reason = value.Reason
				if value.State == ResourcePlaneEnabled {
					relationships, relationErr :=
						canonicalResourcePlaneRelationships(
							value.Relationships,
							visible,
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
	return result, nil
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
	visible func(string) bool,
) ([]ResourcePlaneRelationship, error) {
	if len(relationships) > resourcePlaneMaxRelationships {
		return nil, fmt.Errorf(
			"resource plane relationships exceed %d",
			resourcePlaneMaxRelationships,
		)
	}
	result := make([]ResourcePlaneRelationship, 0, len(relationships))
	for _, relationship := range relationships {
		if len(relationship.Sources) == 0 ||
			len(relationship.Sources) > resourcePlaneMaxSources {
			return nil, fmt.Errorf(
				"resource plane relationship sources must contain 1 through %d citations",
				resourcePlaneMaxSources,
			)
		}
		sources := make(
			[]ContractCatalogSource,
			0,
			len(relationship.Sources),
		)
		for _, source := range relationship.Sources {
			if visible != nil && !visible(source.Repository) {
				continue
			}
			if err := validateResourcePlaneSource(source); err != nil {
				return nil, err
			}
			sources = append(sources, source)
		}
		if len(relationship.Sources) > 0 && len(sources) == 0 {
			// A relationship supported only by repositories outside the
			// current principal's scope is structurally invisible.
			continue
		}
		for _, value := range []string{
			relationship.Kind,
			relationship.Subject,
			relationship.Object,
			relationship.Classification,
		} {
			if !validCatalogFilter(value) {
				return nil, errors.New(
					"resource plane relationship identity is invalid",
				)
			}
		}
		relationship.Sources = sources
		sort.Slice(relationship.Sources, func(left, right int) bool {
			return resourcePlaneSourceKey(
				relationship.Sources[left],
			) < resourcePlaneSourceKey(relationship.Sources[right])
		})
		for index := 1; index < len(relationship.Sources); index++ {
			if resourcePlaneSourceKey(relationship.Sources[index-1]) ==
				resourcePlaneSourceKey(relationship.Sources[index]) {
				return nil, errors.New(
					"resource plane relationship sources contain a duplicate",
				)
			}
		}
		result = append(result, relationship)
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

func validateResourcePlaneSource(source ContractCatalogSource) error {
	if err := validateCatalogRepository(source.Repository); err != nil {
		return errors.New("resource plane source repository is invalid")
	}
	if (len(source.Commit) != 40 && len(source.Commit) != 64) ||
		source.Commit != strings.ToLower(source.Commit) {
		return errors.New("resource plane source commit is invalid")
	}
	if _, err := hex.DecodeString(source.Commit); err != nil {
		return errors.New("resource plane source commit is invalid")
	}
	if source.Path == "" ||
		len(source.Path) > resourcePlaneMaxPathBytes ||
		!utf8.ValidString(source.Path) ||
		strings.HasPrefix(source.Path, "/") ||
		strings.HasPrefix(source.Path, "-") ||
		strings.Contains(source.Path, `\`) ||
		path.Clean(source.Path) != source.Path {
		return errors.New("resource plane source path is invalid")
	}
	for _, component := range strings.Split(source.Path, "/") {
		if component == "" || component == "." || component == ".." {
			return errors.New("resource plane source path is invalid")
		}
	}
	for _, character := range source.Path {
		if character < 0x20 || character == 0x7f {
			return errors.New("resource plane source path is invalid")
		}
	}
	if source.StartByte < 0 ||
		source.EndByte <= source.StartByte ||
		source.StartLine < 1 ||
		source.EndLine < source.StartLine ||
		!validQueryIdentity(source.AssertionID) ||
		!validQueryIdentity(source.RunID) ||
		!validQueryIdentity(source.AtomID) {
		return errors.New("resource plane source citation is invalid")
	}
	return nil
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
