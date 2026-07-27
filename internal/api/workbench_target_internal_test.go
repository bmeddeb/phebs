package api

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
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

func TestWorkbenchTargetResolverReadsBaselineAtAuthorizedExactCommit(t *testing.T) {
	origin := t.TempDir()
	run := func(args ...string) string {
		full := append(
			[]string{
				"-c", "user.name=Workbench Test",
				"-c", "user.email=workbench@example.invalid",
				"-C", origin,
			},
			args...,
		)
		output, err := exec.Command("git", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run("init", "-b", "main")
	const source = "syntax = \"proto3\";\nservice Checkout {}\n"
	if err := os.MkdirAll(filepath.Join(origin, "proto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(origin, "proto", "shop.proto"),
		[]byte(source),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "baseline")
	commit := run("rev-parse", "HEAD")
	dataDir := t.TempDir()
	const repository = "example/contracts"
	if err := phebssync.Mirror(
		context.Background(),
		"file://"+origin,
		phebssync.RepoDir(dataDir, repository),
	); err != nil {
		t.Fatal(err)
	}
	repositories := &contractFixtureRepoStore{repos: []store.Repo{{
		Name: repository, IndexedCommitHash: commit,
	}}}
	resolver := &WorkbenchTargetResolver{opts: Options{
		Store: repositories, DataDir: dataDir,
		Principal: func(context.Context) string {
			return "user:workbench"
		},
	}}
	files, err := resolver.ResolveWorkbenchBaseline(
		context.Background(),
		"user:workbench",
		store.WorkbenchBaselineRequest{
			Repository: repository,
			Commit:     commit,
			Paths:      []string{"proto/shop.proto"},
		},
	)
	if err != nil || len(files) != 1 ||
		files[0].Path != "proto/shop.proto" ||
		files[0].Content != source {
		t.Fatalf("exact baseline = %+v, %v", files, err)
	}
	for _, request := range []store.WorkbenchBaselineRequest{
		{
			Repository: repository,
			Commit:     strings.Repeat("b", 40),
			Paths:      []string{"proto/shop.proto"},
		},
		{
			Repository: repository,
			Commit:     commit,
			Paths:      []string{"proto/missing.proto"},
		},
	} {
		if _, err := resolver.ResolveWorkbenchBaseline(
			context.Background(),
			"user:workbench",
			request,
		); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("unbound baseline request = %v, want not found", err)
		}
	}
	if _, err := resolver.ResolveWorkbenchBaseline(
		context.Background(),
		"user:other",
		store.WorkbenchBaselineRequest{
			Repository: repository,
			Commit:     commit,
			Paths:      []string{"proto/shop.proto"},
		},
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unauthorized baseline = %v, want not found", err)
	}
}
