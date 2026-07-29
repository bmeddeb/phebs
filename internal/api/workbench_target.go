package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

type workbenchContractCatalog interface {
	OperationForProtocol(
		context.Context,
		string,
		string,
		string,
		string,
	) (*ContractCatalogOperation, error)
}

// WorkbenchTargetResolver composes the authorization-scoped repository list
// and exact Contract Atlas detail service into T21.3's narrow resolver.
// Missing and hidden inputs are omitted identically.
type WorkbenchTargetResolver struct {
	opts    Options
	catalog workbenchContractCatalog
}

func NewWorkbenchTargetResolver(opts Options) *WorkbenchTargetResolver {
	catalog := opts.ContractCatalog
	if catalog == nil {
		catalog = NewContractCatalogService(opts)
	}
	if catalog == nil {
		return nil
	}
	return &WorkbenchTargetResolver{opts: opts, catalog: catalog}
}

var _ store.WorkbenchResolver = (*WorkbenchTargetResolver)(nil)
var _ store.WorkbenchBaselineResolver = (*WorkbenchTargetResolver)(nil)

func (resolver *WorkbenchTargetResolver) ResolveWorkbench(
	ctx context.Context,
	principal string,
	request store.WorkbenchResolutionRequest,
) (store.WorkbenchResolution, error) {
	if resolver == nil || resolver.catalog == nil ||
		resolver.opts.Principal == nil ||
		strings.TrimSpace(resolver.opts.Principal(ctx)) != principal ||
		principal == "" {
		return store.WorkbenchResolution{}, store.ErrNotFound
	}
	visible, err := visibleRepositories(ctx, resolver.opts)
	if err != nil {
		return store.WorkbenchResolution{}, err
	}
	visibleByName := make(map[string]store.Repo, len(visible))
	for _, repository := range visible {
		visibleByName[repository.Name] = repository
	}
	result := store.WorkbenchResolution{
		AuthorizationDigest: digestJSON(
			catalogVisibilityContext(ctx, resolver.opts, visible),
		),
		Repositories: []store.WorkbenchRepositorySnapshot{},
		Endpoints:    []store.WorkbenchEndpointSnapshot{},
		Capabilities: []store.WorkbenchCapabilitySnapshot{},
	}
	resolvedRepositories := make(map[string]string)
	for _, name := range request.Repositories {
		repository, ok := visibleByName[name]
		if !ok || repository.IndexedCommitHash == "" {
			continue
		}
		resolvedRepositories[name] = repository.IndexedCommitHash
		result.Repositories = append(
			result.Repositories,
			store.WorkbenchRepositorySnapshot{
				Name: name, Commit: repository.IndexedCommitHash,
			},
		)
	}
	for _, selection := range request.Selections {
		commit, ok := resolvedRepositories[selection.Repository]
		if !ok {
			continue
		}
		detail, detailErr := resolver.catalog.OperationForProtocol(
			ctx,
			selection.Protocol,
			selection.Repository,
			selection.DeclarationLineage,
			selection.CanonicalOperation,
		)
		if detailErr != nil {
			var statusError huma.StatusError
			if errors.As(detailErr, &statusError) &&
				statusError.GetStatus() == http.StatusNotFound {
				continue
			}
			return store.WorkbenchResolution{}, fmt.Errorf(
				"resolve exact Workbench endpoint: %w",
				detailErr,
			)
		}
		if detail.Protocol != selection.Protocol ||
			detail.Repository != selection.Repository ||
			detail.DeclarationLineage != selection.DeclarationLineage ||
			detail.Operation != selection.CanonicalOperation {
			return store.WorkbenchResolution{}, errors.New(
				"resolve exact Workbench endpoint: Atlas identity mismatch",
			)
		}
		sources := make(
			[]store.WorkbenchDeclarationSource,
			len(detail.Declaration.Sources),
		)
		current := true
		for index, source := range detail.Declaration.Sources {
			if source.Repository != selection.Repository ||
				source.Commit != commit {
				current = false
				break
			}
			sources[index] = store.WorkbenchDeclarationSource{
				Repository:  source.Repository,
				Commit:      source.Commit,
				Path:        source.Path,
				StartByte:   source.StartByte,
				EndByte:     source.EndByte,
				StartLine:   source.StartLine,
				EndLine:     source.EndLine,
				AssertionID: source.AssertionID,
				RunID:       source.RunID,
				AtomID:      source.AtomID,
			}
		}
		if !current || len(sources) == 0 {
			continue
		}
		slices.SortFunc(
			sources,
			func(left, right store.WorkbenchDeclarationSource) int {
				for _, comparison := range []int{
					strings.Compare(left.Repository, right.Repository),
					strings.Compare(left.Commit, right.Commit),
					strings.Compare(left.Path, right.Path),
					left.StartByte - right.StartByte,
					left.EndByte - right.EndByte,
					strings.Compare(left.AtomID, right.AtomID),
				} {
					if comparison != 0 {
						return comparison
					}
				}
				return 0
			},
		)
		result.Endpoints = append(
			result.Endpoints,
			store.WorkbenchEndpointSnapshot{
				Selection:          selection,
				DeclarationCommit:  commit,
				DeclarationDigest:  digestJSON(detail.Declaration),
				DeclarationSources: sources,
				SourcesTruncated:   detail.Declaration.SourcesTruncated,
			},
		)
	}
	for _, id := range request.Capabilities {
		snapshot := store.WorkbenchCapabilitySnapshot{
			ID: id, Available: false,
		}
		switch id {
		case contractCatalogCapability:
			snapshot.Available = true
			snapshot.Version = contractCatalogSchemaVersion
			snapshot.ContentDigest = digestJSON(struct {
				Schema    string   `json:"schema"`
				Protocols []string `json:"protocols"`
			}{
				Schema:    contractCatalogSchemaVersion,
				Protocols: catalogProtocols(),
			})
		case "contract-compatibility":
			if resolver.opts.Compatibility != nil {
				snapshot.Available = true
				snapshot.Version = "buf-" + compat.Version + "-" + compat.Policy
				snapshot.ContentDigest = compat.WirePolicyDigest()
			}
		}
		result.Capabilities = append(result.Capabilities, snapshot)
	}
	return result, nil
}

func (resolver *WorkbenchTargetResolver) ResolveWorkbenchBaseline(
	ctx context.Context,
	principal string,
	request store.WorkbenchBaselineRequest,
) ([]store.WorkbenchSourceFile, error) {
	if resolver == nil || resolver.opts.Store == nil ||
		resolver.opts.Principal == nil ||
		strings.TrimSpace(resolver.opts.Principal(ctx)) != principal ||
		principal == "" ||
		!validCatalogFilter(request.Repository) ||
		(len(request.Commit) != 40 && len(request.Commit) != 64) ||
		len(request.Paths) == 0 ||
		len(request.Paths) > compat.WireLimits().MaxFilesPerSnapshot {
		return nil, store.ErrNotFound
	}
	visible, err := visibleRepositories(ctx, resolver.opts)
	if err != nil {
		return nil, err
	}
	authorized := false
	for _, repository := range visible {
		if repository.Name == request.Repository &&
			repository.IndexedCommitHash == request.Commit {
			authorized = true
			break
		}
	}
	if !authorized {
		return nil, store.ErrNotFound
	}
	result := make([]store.WorkbenchSourceFile, len(request.Paths))
	previous := ""
	for index, sourcePath := range request.Paths {
		if sourcePath <= previous ||
			!strings.HasSuffix(sourcePath, ".proto") {
			return nil, store.ErrNotFound
		}
		content, readErr := reposync.CatFile(
			ctx,
			resolver.opts.DataDir,
			request.Repository,
			request.Commit,
			sourcePath,
		)
		if readErr != nil || len(content) > compat.WireLimits().MaxFileBytes {
			return nil, store.ErrNotFound
		}
		result[index] = store.WorkbenchSourceFile{
			Path: sourcePath, Content: string(content),
		}
		previous = sourcePath
	}
	return result, nil
}
