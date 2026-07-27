package api

import (
	"context"
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/store"
)

type workbenchTargetCatalog struct{}

func (workbenchTargetCatalog) OperationForProtocol(
	_ context.Context,
	protocol, repository, lineage, operation string,
) (*ContractCatalogOperation, error) {
	commit := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	return &ContractCatalogOperation{
		SchemaVersion:      contractCatalogSchemaVersion,
		Protocol:           protocol,
		Repository:         repository,
		DeclarationLineage: lineage,
		Operation:          operation,
		Declaration: ContractCatalogClaim{
			AssertionID: "assertion-" + protocol,
			RunID:       "run-" + protocol,
			Predicate:   "DECLARES_OPERATION",
			Object:      operation[1:],
			Lineage:     lineage,
			Tier:        store.TierExact,
			Sources: []ContractCatalogSource{{
				Repository: repository,
				Commit:     commit,
				Path: "contracts/service." + map[string]string{
					"protobuf": "proto",
					"thrift":   "thrift",
				}[protocol],
				StartByte:   10,
				EndByte:     40,
				StartLine:   2,
				EndLine:     3,
				AssertionID: "assertion-" + protocol,
				RunID:       "run-" + protocol,
				AtomID:      "atom-" + protocol,
			}},
		},
	}, nil
}

type workbenchTargetCompatibility struct{}

func (workbenchTargetCompatibility) Check(
	context.Context,
	compat.Request,
) (*compat.CompatibilityResult, error) {
	return nil, errors.New("not called")
}

func TestWorkbenchTargetResolverKeepsExactProtocolRepositoryAndLineage(t *testing.T) {
	repositories := &contractFixtureRepoStore{repos: []store.Repo{
		{
			Name:              "example/contracts",
			IndexedCommitHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			Name:              "example/hidden",
			IndexedCommitHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	}}
	opts := Options{
		Store:         repositories,
		Compatibility: workbenchTargetCompatibility{},
		Principal: func(context.Context) string {
			return "user:workbench"
		},
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repository store.Repo) bool {
				return repository.Name != "example/hidden"
			}
		},
	}
	resolver := &WorkbenchTargetResolver{
		opts: opts, catalog: workbenchTargetCatalog{},
	}
	operation := "/shared.Service/Get"
	resolution, err := resolver.ResolveWorkbench(
		context.Background(),
		"user:workbench",
		store.WorkbenchResolutionRequest{
			Repositories: []string{
				"example/contracts",
				"example/hidden",
			},
			Selections: []store.ChangeBriefContractSelection{
				{
					Role:               store.ChangeBriefCurrent,
					Protocol:           "protobuf",
					Repository:         "example/contracts",
					DeclarationLineage: "proto-lineage",
					CanonicalOperation: operation,
				},
				{
					Role:               store.ChangeBriefReplacement,
					Protocol:           "thrift",
					Repository:         "example/contracts",
					DeclarationLineage: "thrift-lineage",
					CanonicalOperation: operation,
				},
				{
					Role:               store.ChangeBriefAnalogous,
					Protocol:           "protobuf",
					Repository:         "example/hidden",
					DeclarationLineage: "hidden-lineage",
					CanonicalOperation: operation,
				},
			},
			Capabilities: []string{
				"contract-atlas",
				"contract-compatibility",
				"unknown-pack",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Repositories) != 1 ||
		resolution.Repositories[0].Name != "example/contracts" ||
		len(resolution.Endpoints) != 2 ||
		resolution.Endpoints[0].Selection.Protocol != "protobuf" ||
		resolution.Endpoints[0].Selection.DeclarationLineage != "proto-lineage" ||
		resolution.Endpoints[1].Selection.Protocol != "thrift" ||
		resolution.Endpoints[1].Selection.DeclarationLineage != "thrift-lineage" ||
		resolution.Endpoints[0].DeclarationDigest ==
			resolution.Endpoints[1].DeclarationDigest {
		t.Fatalf("exact resolution = %+v", resolution)
	}
	for _, endpoint := range resolution.Endpoints {
		if len(endpoint.DeclarationSources) != 1 ||
			endpoint.DeclarationSources[0].Commit !=
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			t.Fatalf("declaration source = %+v", endpoint)
		}
	}
	if len(resolution.Capabilities) != 3 ||
		!resolution.Capabilities[0].Available ||
		!resolution.Capabilities[1].Available ||
		resolution.Capabilities[2].Available {
		t.Fatalf("capabilities = %+v", resolution.Capabilities)
	}
	if _, err := resolver.ResolveWorkbench(
		context.Background(),
		"user:other",
		store.WorkbenchResolutionRequest{},
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("principal mismatch = %v, want not found", err)
	}
}
