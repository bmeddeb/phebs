package servicecatalogv3

import (
	"context"
	"fmt"
	"slices"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

const (
	ServiceStateSchema       = "phebs-service-state-v3-shadow"
	RepositoryStateSchema    = "phebs-service-state-repository-v3-shadow"
	desiredServiceSchema     = "phebs-service-desired-v3-shadow"
	desiredGenerationSchema  = "phebs-service-desired-generation-v3-shadow"
	sourceGenerationSchema   = "phebs-service-source-generation-v3-shadow"
	sourceGenerationSchemaV2 = "phebs-service-source-generation-v3-shadow-v2"
	serviceStateDigestDomain = "phebs-service-state-v3-shadow\x00"
	repositoryDigestDomain   = "phebs-service-state-repository-v3-shadow\x00"
)

func StatePolicy() servicecatalog.StatePolicy {
	return servicecatalog.StatePolicy{
		ServiceSchema: ServiceStateSchema, RepositorySchema: RepositoryStateSchema,
		ServiceDigestDomain:    serviceStateDigestDomain,
		RepositoryDigestDomain: repositoryDigestDomain,
		MaxServices:            MaxTotalServices, MaxSuccessors: MaxServiceSuccessors,
	}
}

func ValidateServiceState(state servicecatalog.ServiceState, persisted bool) error {
	return servicecatalog.ValidateServiceStateWithPolicy(state, persisted, StatePolicy())
}

func SetServiceStateDigest(state *servicecatalog.ServiceState) error {
	return servicecatalog.SetServiceStateDigestWithPolicy(state, StatePolicy())
}

func ValidateRepositoryState(state servicecatalog.RepositoryState, persisted bool) error {
	return servicecatalog.ValidateRepositoryStateWithPolicy(state, persisted, StatePolicy())
}

func SetRepositoryStateDigest(state *servicecatalog.RepositoryState) error {
	return servicecatalog.SetRepositoryStateDigestWithPolicy(state, StatePolicy())
}

// SourceGenerationDigest binds state to the exact v3 source census without
// importing the segmented catalog's member layout into every service row.
func SourceGenerationDigest(root Root) (string, error) {
	if err := ValidateRoot(root); err != nil {
		return "", err
	}
	return sourceGenerationDigest(root)
}

func sourceGenerationDigest(root Root) (string, error) {
	if root.Schema == RootSchemaV2 {
		binding := struct {
			Schema             string `json:"schema"`
			Repository         string `json:"repository"`
			SourceCommit       string `json:"source_commit"`
			SourceCensusDigest string `json:"source_census_digest"`
			SourceFileCount    int    `json:"source_file_count"`
		}{
			Schema: sourceGenerationSchemaV2, Repository: root.Binding.Repository,
			SourceCommit:       root.Binding.Source.Commit,
			SourceCensusDigest: root.Binding.Source.CensusDigest,
			SourceFileCount:    root.Binding.Source.FileCount,
		}
		raw, err := canonical(binding)
		if err != nil {
			return "", err
		}
		return digest(sourceGenerationSchemaV2+"\x00", raw), nil
	}
	binding := struct {
		Schema     string `json:"schema"`
		Repository string `json:"repository"`
		Source     Source `json:"source"`
	}{
		Schema: sourceGenerationSchema, Repository: root.Binding.Repository,
		Source: root.Binding.Source,
	}
	raw, err := canonical(binding)
	if err != nil {
		return "", err
	}
	return digest(sourceGenerationSchema+"\x00", raw), nil
}

// ProjectServiceMember returns the independently digestible state inputs for
// one strict-opened at-most-512-service member.
func ProjectServiceMember(
	root Root,
	descriptor MemberDescriptor,
	raw []byte,
) ([]servicecatalog.ServiceProjection, error) {
	sourceGeneration, err := SourceGenerationDigest(root)
	if err != nil {
		return nil, err
	}
	return projectServiceMember(
		context.Background(), root, descriptor, raw, sourceGeneration,
	)
}

func projectServiceMember(
	ctx context.Context,
	root Root,
	descriptor MemberDescriptor,
	raw []byte,
	sourceGeneration string,
) ([]servicecatalog.ServiceProjection, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if descriptor.Kind != "service" || len(raw) != descriptor.ContentBytes ||
		rawDigest(raw) != descriptor.Digest {
		return nil, ErrInvalid
	}
	var member ServiceMember
	if err := decodeCanonicalMember(
		ctx, raw, &member,
		func() int { return len(member.Services) + len(member.Memberships) },
	); err != nil {
		return nil, err
	}
	if err := validateServiceMember(root, descriptor, member); err != nil {
		return nil, err
	}
	projections := make([]servicecatalog.ServiceProjection, 0, len(member.Services))
	membershipOffset := 0
	for _, service := range member.Services {
		start := membershipOffset
		for membershipOffset < len(member.Memberships) &&
			member.Memberships[membershipOffset].ServiceKey == service.Key {
			membershipOffset++
		}
		memberships := slices.Clone(member.Memberships[start:membershipOffset])
		binding := struct {
			Schema           string                      `json:"schema"`
			Repository       string                      `json:"repository"`
			SourceGeneration string                      `json:"source_generation"`
			Service          servicecatalog.Service      `json:"service"`
			Memberships      []servicecatalog.Membership `json:"memberships"`
		}{
			Schema: desiredServiceSchema, Repository: root.Binding.Repository,
			SourceGeneration: sourceGeneration,
			Service:          service, Memberships: memberships,
		}
		rawBinding, encodeErr := canonical(binding)
		if encodeErr != nil {
			return nil, encodeErr
		}
		projections = append(projections, servicecatalog.ServiceProjection{
			Repository: root.Binding.Repository, Service: service,
			Memberships: memberships, SourceGeneration: sourceGeneration,
			CatalogGeneration: root.Digest,
			GenerationDigest:  digest(desiredServiceSchema+"\x00", rawBinding),
			Removed:           service.Disposition == servicecatalog.DispositionRejected,
		})
	}
	if membershipOffset != len(member.Memberships) {
		return nil, ErrInvalid
	}
	return projections, nil
}

func ServiceDesiredGeneration(
	projection servicecatalog.ServiceProjection,
	incarnation uint64,
) (string, error) {
	if incarnation == 0 || !validDigest(projection.GenerationDigest) ||
		!validDigest(projection.SourceGeneration) ||
		!validDigest(projection.CatalogGeneration) || projection.Repository == "" ||
		projection.Service.Key == "" {
		return "", fmt.Errorf("%w: desired generation input", ErrInvalid)
	}
	binding := struct {
		Schema           string `json:"schema"`
		Repository       string `json:"repository"`
		ServiceKey       string `json:"service_key"`
		Incarnation      uint64 `json:"incarnation"`
		ProjectionDigest string `json:"projection_digest"`
	}{
		Schema: desiredGenerationSchema, Repository: projection.Repository,
		ServiceKey: projection.Service.Key, Incarnation: incarnation,
		ProjectionDigest: projection.GenerationDigest,
	}
	raw, err := canonical(binding)
	if err != nil {
		return "", err
	}
	return digest(desiredGenerationSchema+"\x00", raw), nil
}

// ValidateStateProjection proves that one persisted v3 state row still binds
// either its desired projection or its independently active projection.
func ValidateStateProjection(
	state servicecatalog.ServiceState,
	projection servicecatalog.ServiceProjection,
	active bool,
) error {
	if err := ValidateServiceState(state, true); err != nil {
		return err
	}
	desired, err := ServiceDesiredGeneration(projection, state.Incarnation)
	if err != nil {
		return err
	}
	if active {
		if state.ActiveDesiredGeneration != desired ||
			state.ActiveSourceGeneration != projection.SourceGeneration ||
			state.ActiveCatalogGeneration != projection.CatalogGeneration {
			return fmt.Errorf("%w: active projection identity", ErrInvalid)
		}
		return nil
	}
	service := projection.Service
	// DesiredCatalogGeneration records the immutable root where this
	// service-local desired identity last changed. A sibling-only catalog
	// successor therefore keeps the older provenance while the desired digest,
	// source generation, and complete service projection remain exact. Active
	// validation above stays root-exact because it opens that historical root.
	if state.DesiredGeneration != desired ||
		state.DesiredSourceGeneration != projection.SourceGeneration ||
		state.ServiceKey != service.Key || state.DisplayName != service.DisplayName ||
		state.Disposition != service.Disposition || state.Origin != service.Origin ||
		state.Reason != service.Reason ||
		!slices.Equal(state.Successors, service.Successors) ||
		state.Removed != projection.Removed {
		return fmt.Errorf("%w: desired projection identity", ErrInvalid)
	}
	return nil
}
