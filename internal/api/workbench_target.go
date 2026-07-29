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
	_, _, visibilityDigest, err := workbenchImplementationVisibility(visible)
	if err != nil {
		return store.WorkbenchResolution{}, huma.Error409Conflict(
			"repository analysis-unit state changed while resolving the Workbench target",
			err,
		)
	}
	visibleByName := make(map[string]store.Repo, len(visible))
	visibleScopeByName := make(
		map[string]workbenchRepositoryScope,
		len(visible),
	)
	visibleScopes := make([]store.WorkbenchRepositorySnapshot, 0, len(visible))
	for _, repository := range visible {
		visibleByName[repository.Name] = repository
		scope, scopeErr := workbenchScopeForRepository(repository)
		if scopeErr != nil {
			return store.WorkbenchResolution{}, huma.Error409Conflict(
				"repository analysis-unit state changed while resolving the Workbench target",
				scopeErr,
			)
		}
		visibleScopeByName[repository.Name] = scope
		visibleScopes = append(
			visibleScopes,
			scope.snapshot(repository.Name, repository.IndexedCommitHash),
		)
	}
	slices.SortFunc(
		visibleScopes,
		func(left, right store.WorkbenchRepositorySnapshot) int {
			return strings.Compare(left.Name, right.Name)
		},
	)
	result := store.WorkbenchResolution{
		AuthorizationDigest: digestJSON(
			struct {
				Visibility   VisibilityContext                   `json:"visibility"`
				Repositories []store.WorkbenchRepositorySnapshot `json:"repositories"`
			}{
				Visibility: catalogVisibilityContext(
					ctx,
					resolver.opts,
					visible,
				),
				Repositories: visibleScopes,
			},
		),
		Repositories: []store.WorkbenchRepositorySnapshot{},
		Endpoints:    []store.WorkbenchEndpointSnapshot{},
		Capabilities: []store.WorkbenchCapabilitySnapshot{},
	}
	type resolvedRepository struct {
		commit string
		scope  workbenchRepositoryScope
	}
	resolvedRepositories := make(map[string]resolvedRepository)
	for _, name := range request.Repositories {
		repository, ok := visibleByName[name]
		if !ok || repository.IndexedCommitHash == "" {
			continue
		}
		scope := visibleScopeByName[name]
		resolvedRepositories[name] = resolvedRepository{
			commit: repository.IndexedCommitHash,
			scope:  scope,
		}
		result.Repositories = append(
			result.Repositories,
			scope.snapshot(name, repository.IndexedCommitHash),
		)
	}
	for _, selection := range request.Selections {
		resolved, ok := resolvedRepositories[selection.Repository]
		if !ok {
			continue
		}
		commit := resolved.commit
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
				source.Commit != commit ||
				!resolved.scope.admits(source.Path) {
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
				ScopePosture:       resolved.scope.posture,
				UnitDigest:         resolved.scope.unitDigest,
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
	if err := resolver.confirmWorkbenchVisibility(ctx, visibilityDigest); err != nil {
		return store.WorkbenchResolution{}, err
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
	_, _, visibilityDigest, err := workbenchImplementationVisibility(visible)
	if err != nil {
		return nil, huma.Error409Conflict(
			"repository analysis-unit state changed while resolving the Workbench baseline",
			err,
		)
	}
	authorized := false
	var authorizedScope workbenchRepositoryScope
	for _, repository := range visible {
		if repository.Name == request.Repository &&
			repository.IndexedCommitHash == request.Commit {
			scope, scopeErr := workbenchScopeForRepository(repository)
			if scopeErr != nil {
				return nil, huma.Error409Conflict(
					"repository analysis-unit state changed while resolving the Workbench baseline",
					scopeErr,
				)
			}
			if !scope.matchesSnapshot(store.WorkbenchRepositorySnapshot{
				Name:         request.Repository,
				Commit:       request.Commit,
				ScopePosture: request.ScopePosture,
				UnitDigest:   request.UnitDigest,
			}) {
				continue
			}
			authorized = true
			authorizedScope = scope
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
			!strings.HasSuffix(sourcePath, ".proto") ||
			!authorizedScope.admits(sourcePath) {
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
	if err := resolver.confirmWorkbenchVisibility(ctx, visibilityDigest); err != nil {
		return nil, err
	}
	return result, nil
}

func (resolver *WorkbenchTargetResolver) confirmWorkbenchVisibility(
	ctx context.Context,
	expectedDigest string,
) error {
	visible, err := visibleRepositories(ctx, resolver.opts)
	if err != nil {
		return err
	}
	_, _, currentDigest, err := workbenchImplementationVisibility(visible)
	if err != nil {
		return huma.Error409Conflict(
			"repository analysis-unit state changed while resolving the Workbench result",
			err,
		)
	}
	if currentDigest != expectedDigest {
		return huma.Error409Conflict(
			"repository authorization, indexed commit, or analysis unit changed while resolving the Workbench result; retry",
		)
	}
	return nil
}
