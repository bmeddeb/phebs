package servicequery

import (
	"context"
	"errors"
	"slices"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

// RuntimeStore is the narrow precious-state boundary required to open and
// final-fence one service-scoped search. Authorization remains the caller's
// responsibility and must happen before invoking OpenRuntimeScope.
type RuntimeStore interface {
	RuntimeRepositoryStore
	GetServiceStateRead(context.Context, string, string) (*store.ServiceStateRead, error)
	GetServiceCatalogGeneration(context.Context, string, string) (*servicecatalog.Publication, error)
	ConfirmServiceStateSnapshot(context.Context, string, servicecatalog.RepositoryState) error
}

type RuntimeRepositoryStore interface {
	GetRepo(context.Context, string) (*store.Repo, error)
}

type RuntimeSelectorStore interface {
	RuntimeStore
	store.ServiceRuntimeSelectorReader
}

// RuntimeScope is an opaque strict-open store/filesystem snapshot. Its
// prepared compiler input and search root remain immutable copies; callers
// must final-fence the scope before any result escapes.
type RuntimeScope struct {
	prepared  PreparedScope
	search    repositoryindex.SearchManifest
	source    repositoryindex.SourceManifest
	searchDir string
	repo      store.Repo
	summary   servicecatalog.RepositoryState
	v3Read    *store.ServiceStateV3Read
	selector  store.ServiceRuntimeSelector
	selected  bool
	valid     bool
}

func (scope RuntimeScope) Prepared() (PreparedScope, bool) {
	return scope.prepared, scope.valid
}

func (scope RuntimeScope) Search() (repositoryindex.SearchManifest, bool) {
	if !scope.valid {
		return repositoryindex.SearchManifest{}, false
	}
	return cloneSearchManifest(scope.search), true
}

// ExactSearchDirectory returns the immutable generation root for an explicit
// runtime selector. Implicit-v2 and fixed-backend test seams remain bound to
// the mutable flat publication and return false.
func (scope RuntimeScope) ExactSearchDirectory() (string, bool) {
	if !scope.valid || scope.selector.Backend == "" || scope.searchDir == "" {
		return "", false
	}
	return scope.searchDir, true
}

func (scope *RuntimeScope) Close() {
	if scope != nil && scope.v3Read != nil {
		scope.v3Read.Close()
		scope.valid = false
	}
}

// OpenRuntimeScope strict-opens current catalog/state, the exact historical
// active catalog when stale, and the current direct v2 search controls. Full
// member hashing belongs to the generation cache fill; this hot boundary reads
// only bounded control files and precious point rows.
func OpenRuntimeScope(
	ctx context.Context,
	indexDir string,
	st RuntimeStore,
	repository, serviceKey string,
) (RuntimeScope, error) {
	repo, err := st.GetRepo(ctx, repository)
	if err != nil {
		return RuntimeScope{}, unavailablef("repository state: %v", err)
	}
	revisions, err := runtimeRevisions(*repo)
	if err != nil {
		return RuntimeScope{}, err
	}
	read, err := st.GetServiceStateRead(ctx, repository, serviceKey)
	if err != nil {
		return RuntimeScope{}, unavailablef("service state: %v", err)
	}
	active := read.Publication
	if read.Entry.State.ActiveCatalogGeneration == "" {
		return RuntimeScope{}, unavailablef("service has no active catalog generation")
	}
	if read.Entry.State.ActiveCatalogGeneration != read.Publication.GenerationDigest {
		active, err = dereferenceCatalog(
			ctx, st, repository, read.Entry.State.ActiveCatalogGeneration,
		)
		if err != nil {
			return RuntimeScope{}, unavailablef("active catalog: %v", err)
		}
	}
	search, source, err := focusedindex.ReadRepositorySearchGeneration(
		indexDir, repository, revisions,
	)
	if err != nil {
		return RuntimeScope{}, unavailablef("repository search generation: %v", err)
	}
	if read.Entry.State.ActiveSearchGeneration != search.Digest {
		return RuntimeScope{}, unavailablef("service search generation is not active")
	}
	prepared, err := Prepare(Scope{
		CurrentCatalog: read.Publication,
		ActiveCatalog:  active,
		Summary:        read.Summary,
		State:          read.Entry.State,
		Search:         search,
	})
	if err != nil {
		return RuntimeScope{}, err
	}
	opened := RuntimeScope{
		prepared: prepared, search: cloneSearchManifest(search),
		source: cloneSourceManifest(source), searchDir: indexDir,
		repo:    cloneRuntimeRepo(*repo),
		summary: read.Summary, valid: true,
	}
	if err := ConfirmRuntimeScope(ctx, indexDir, st, opened); err != nil {
		return RuntimeScope{}, err
	}
	return opened, nil
}

// OpenRuntimeScopeV3 is the fixed-backend segmented-catalog seam retained for
// tests and embeddings. Production uses OpenSelectedRuntimeScope.
func OpenRuntimeScopeV3(
	ctx context.Context,
	indexDir string,
	st RuntimeRepositoryStore,
	reader *store.ServiceStateV3Reader,
	repository, serviceKey string,
) (_ RuntimeScope, retErr error) {
	return openRuntimeScopeV3(ctx, indexDir, st, reader, nil, repository, serviceKey)
}

func openRuntimeScopeV3(
	ctx context.Context,
	indexDir string,
	st RuntimeRepositoryStore,
	reader *store.ServiceStateV3Reader,
	selector *store.ServiceRuntimeSelector,
	repository, serviceKey string,
) (_ RuntimeScope, retErr error) {
	repo, err := st.GetRepo(ctx, repository)
	if err != nil {
		return RuntimeScope{}, unavailablef("repository state: %v", err)
	}
	revisions, err := runtimeRevisions(*repo)
	if err != nil {
		return RuntimeScope{}, err
	}
	var read *store.ServiceStateV3Read
	if selector == nil {
		read, err = reader.OpenService(ctx, repository, serviceKey)
	} else {
		read, err = reader.OpenServiceSelected(ctx, *selector, serviceKey)
	}
	if err != nil {
		return RuntimeScope{}, unavailablef("service state v3: %v", err)
	}
	opened := RuntimeScope{v3Read: read}
	defer func() {
		if retErr != nil {
			opened.Close()
		}
	}()
	if read.Entry.Projection == nil || read.ActiveProjection == nil ||
		read.Entry.State.ActiveCatalogGeneration == "" {
		return RuntimeScope{}, unavailablef("service has no searchable catalog projection")
	}
	search, source, err := focusedindex.ReadRepositorySearchGeneration(
		indexDir, repository, revisions,
	)
	if err != nil {
		return RuntimeScope{}, unavailablef("repository search generation: %v", err)
	}
	if read.Entry.State.ActiveSearchGeneration != search.Digest {
		return RuntimeScope{}, unavailablef("service search generation is not active")
	}
	prepared, err := PrepareV3(V3Scope{
		CurrentRoot: read.Root, CurrentControlRevision: read.Pointer.ControlRevision,
		ActiveRoot: read.ActiveRoot, Summary: read.Summary, State: read.Entry.State,
		DesiredProjection: *read.Entry.Projection,
		ActiveProjection:  *read.ActiveProjection,
		Search:            search,
	})
	if err != nil {
		return RuntimeScope{}, err
	}
	opened.prepared = prepared
	opened.search = cloneSearchManifest(search)
	opened.source = cloneSourceManifest(source)
	opened.searchDir = indexDir
	opened.repo = cloneRuntimeRepo(*repo)
	opened.summary = read.Summary
	opened.valid = true
	if err := ConfirmRuntimeScopeV3(ctx, indexDir, st, reader, opened); err != nil {
		return RuntimeScope{}, err
	}
	return opened, nil
}

func openSelectedRuntimeScopeV2(
	ctx context.Context,
	indexDir string,
	st RuntimeSelectorStore,
	reader *store.ServiceStateV3Reader,
	selector store.ServiceRuntimeSelector,
	controls focusedindex.SearchGenerationControls,
	serviceKey string,
) (RuntimeScope, error) {
	read, err := st.GetServiceStateRead(ctx, selector.Repository, serviceKey)
	if err != nil {
		return RuntimeScope{}, unavailablef("service state: %v", err)
	}
	active := read.Publication
	if read.Entry.State.ActiveCatalogGeneration == "" {
		return RuntimeScope{}, unavailablef("service has no active catalog generation")
	}
	if read.Entry.State.ActiveCatalogGeneration != read.Publication.GenerationDigest {
		active, err = dereferenceCatalog(
			ctx, st, selector.Repository, read.Entry.State.ActiveCatalogGeneration,
		)
		if err != nil {
			return RuntimeScope{}, unavailablef("active catalog: %v", err)
		}
	}
	if read.Entry.State.ActiveSearchGeneration != controls.Search.Digest {
		return RuntimeScope{}, unavailablef("service search generation is not active")
	}
	prepared, err := Prepare(Scope{
		CurrentCatalog: read.Publication, ActiveCatalog: active,
		Summary: read.Summary, State: read.Entry.State, Search: controls.Search,
	})
	if err != nil {
		return RuntimeScope{}, err
	}
	opened := RuntimeScope{
		prepared: prepared, search: cloneSearchManifest(controls.Search),
		source: cloneSourceManifest(controls.Source), searchDir: controls.Directory,
		repo: store.Repo{Name: selector.Repository}, summary: read.Summary,
		selector: selector, selected: true, valid: true,
	}
	if !runtimeScopeMatchesSelector(opened, selector) {
		return RuntimeScope{}, unavailablef("runtime selector authority does not match")
	}
	if err := ConfirmSelectedRuntimeScope(
		ctx, indexDir, st, reader, opened,
	); err != nil {
		return RuntimeScope{}, err
	}
	return opened, nil
}

func openSelectedRuntimeScopeV3(
	ctx context.Context,
	indexDir string,
	st RuntimeSelectorStore,
	reader *store.ServiceStateV3Reader,
	selector store.ServiceRuntimeSelector,
	controls focusedindex.SearchGenerationControls,
	serviceKey string,
) (_ RuntimeScope, retErr error) {
	read, err := reader.OpenServiceSelected(ctx, selector, serviceKey)
	if err != nil {
		return RuntimeScope{}, unavailablef("service state v3: %v", err)
	}
	opened := RuntimeScope{v3Read: read}
	defer func() {
		if retErr != nil {
			opened.Close()
		}
	}()
	if read.Entry.Projection == nil || read.ActiveProjection == nil ||
		read.Entry.State.ActiveCatalogGeneration == "" {
		return RuntimeScope{}, unavailablef("service has no searchable catalog projection")
	}
	if read.Entry.State.ActiveSearchGeneration != controls.Search.Digest {
		return RuntimeScope{}, unavailablef("service search generation is not active")
	}
	prepared, err := PrepareV3(V3Scope{
		CurrentRoot: read.Root, CurrentControlRevision: read.Pointer.ControlRevision,
		ActiveRoot: read.ActiveRoot, Summary: read.Summary, State: read.Entry.State,
		DesiredProjection: *read.Entry.Projection,
		ActiveProjection:  *read.ActiveProjection,
		Search:            controls.Search,
	})
	if err != nil {
		return RuntimeScope{}, err
	}
	opened.prepared = prepared
	opened.search = cloneSearchManifest(controls.Search)
	opened.source = cloneSourceManifest(controls.Source)
	opened.searchDir = controls.Directory
	opened.repo = store.Repo{Name: selector.Repository}
	opened.summary = read.Summary
	opened.selector = selector
	opened.selected = true
	opened.valid = true
	if !runtimeScopeMatchesSelector(opened, selector) {
		return RuntimeScope{}, unavailablef("runtime selector authority does not match")
	}
	if err := ConfirmSelectedRuntimeScope(
		ctx, indexDir, st, reader, opened,
	); err != nil {
		return RuntimeScope{}, err
	}
	return opened, nil
}

// OpenSelectedRuntimeScope chooses exactly one service runtime after the
// caller has authorized the repository. An absent selector preserves the
// upgrade-compatible v2 runtime; an explicit selector is captured into the
// opaque scope and the nonselected backend is never consulted.
func OpenSelectedRuntimeScope(
	ctx context.Context,
	indexDir string,
	st RuntimeSelectorStore,
	reader *store.ServiceStateV3Reader,
	repository, serviceKey string,
) (_ RuntimeScope, retErr error) {
	selector, err := st.GetServiceRuntimeSelector(ctx, repository)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			opened, openErr := OpenRuntimeScope(
				ctx, indexDir, st, repository, serviceKey,
			)
			if openErr != nil {
				return RuntimeScope{}, openErr
			}
			opened.selected = true
			return opened, nil
		}
		return RuntimeScope{}, unavailablef("runtime selector: %v", err)
	}
	if selector.Repository != repository {
		return RuntimeScope{}, unavailablef("runtime selector repository does not match")
	}
	controls, err := focusedindex.ReadSearchGenerationControls(
		ctx, indexDir, repository, selector.SearchGenerationDigest,
	)
	if err != nil {
		return RuntimeScope{}, unavailablef("selected search generation: %v", err)
	}
	switch selector.Backend {
	case store.ServiceRuntimeV2:
		return openSelectedRuntimeScopeV2(
			ctx, indexDir, st, reader, selector, controls, serviceKey,
		)
	case store.ServiceRuntimeV3:
		return openSelectedRuntimeScopeV3(
			ctx, indexDir, st, reader, selector, controls, serviceKey,
		)
	default:
		return RuntimeScope{}, unavailablef("runtime selector backend is invalid")
	}
}

func dereferenceCatalog(
	ctx context.Context,
	st RuntimeStore,
	repository, generation string,
) (servicecatalog.Publication, error) {
	publication, err := st.GetServiceCatalogGeneration(ctx, repository, generation)
	if err != nil {
		return servicecatalog.Publication{}, err
	}
	return *publication, nil
}

// ConfirmRuntimeScope is the pre-emission linearization point. It rereads only
// the repository row, catalog/summary points, and bounded v2 control files; a
// publication, rollback, restore, removal, or state transition discards the
// complete result.
func ConfirmRuntimeScope(
	ctx context.Context,
	indexDir string,
	st RuntimeStore,
	scope RuntimeScope,
) error {
	if !scope.valid {
		return invalidf("runtime scope was not opened")
	}
	if err := st.ConfirmServiceStateSnapshot(
		ctx, scope.repo.Name, scope.summary,
	); err != nil {
		return unavailablef("service state changed: %v", err)
	}
	return confirmRuntimeRepository(ctx, indexDir, st, scope)
}

func ConfirmRuntimeScopeV3(
	ctx context.Context,
	indexDir string,
	st RuntimeRepositoryStore,
	reader *store.ServiceStateV3Reader,
	scope RuntimeScope,
) error {
	if !scope.valid || scope.v3Read == nil {
		return invalidf("runtime v3 scope was not opened")
	}
	if err := confirmRuntimeRepository(ctx, indexDir, st, scope); err != nil {
		return err
	}
	if err := reader.Confirm(ctx, scope.v3Read); err != nil {
		return unavailablef("service state changed: %v", err)
	}
	return nil
}

// ConfirmSelectedRuntimeScope preserves the chosen backend's complete fence
// and makes selector confirmation the final mutable-authority read.
func ConfirmSelectedRuntimeScope(
	ctx context.Context,
	indexDir string,
	st RuntimeSelectorStore,
	reader *store.ServiceStateV3Reader,
	scope RuntimeScope,
) error {
	if !scope.valid || !scope.selected {
		return invalidf("selected runtime scope was not opened")
	}
	if scope.selector.Backend == "" {
		if err := ConfirmRuntimeScope(ctx, indexDir, st, scope); err != nil {
			return err
		}
		if err := store.ConfirmServiceRuntimeSelectorAbsent(
			ctx, st, scope.repo.Name,
		); err != nil {
			return unavailablef("runtime selector changed: %v", err)
		}
		return nil
	}
	if !runtimeScopeMatchesSelector(scope, scope.selector) {
		return invalidf("selected runtime scope was not opened")
	}
	switch scope.selector.Backend {
	case store.ServiceRuntimeV2:
		if err := st.ConfirmServiceStateSnapshot(
			ctx, scope.repo.Name, scope.summary,
		); err != nil {
			return unavailablef("service state changed: %v", err)
		}
		if err := confirmSelectedSearchGeneration(ctx, indexDir, scope); err != nil {
			return err
		}
		if err := st.ConfirmServiceRuntimeSelector(ctx, scope.selector); err != nil {
			return unavailablef("runtime selector changed: %v", err)
		}
		return nil
	case store.ServiceRuntimeV3:
		if err := confirmSelectedSearchGeneration(ctx, indexDir, scope); err != nil {
			return err
		}
		// The selected v3 reader confirms its state summary and then this exact
		// selector after the immutable-search fence above.
		if err := reader.Confirm(ctx, scope.v3Read); err != nil {
			return unavailablef("service state changed: %v", err)
		}
		return nil
	default:
		return invalidf("selected runtime backend is invalid")
	}
}

func confirmSelectedSearchGeneration(
	ctx context.Context,
	indexDir string,
	scope RuntimeScope,
) error {
	controls, err := focusedindex.ReadSearchGenerationControls(
		ctx, indexDir, scope.repo.Name, scope.selector.SearchGenerationDigest,
	)
	if err != nil || controls.Directory != scope.searchDir ||
		controls.Search.Digest != scope.search.Digest ||
		controls.Source.Digest != scope.source.Digest ||
		!slices.Equal(controls.Search.Revisions, scope.search.Revisions) ||
		!slices.Equal(controls.Source.Revisions, scope.source.Revisions) {
		return unavailablef("selected search generation changed")
	}
	return nil
}

func runtimeScopeMatchesSelector(
	scope RuntimeScope,
	selector store.ServiceRuntimeSelector,
) bool {
	if !scope.valid || scope.repo.Name != selector.Repository ||
		scope.summary.CatalogControlRevision != selector.CatalogControlRevision ||
		scope.summary.ControlRevision != selector.StateControlRevision ||
		scope.summary.SummaryDigest != selector.StateSummaryDigest ||
		scope.search.Digest != selector.SearchGenerationDigest {
		return false
	}
	wantCatalog := selector.CatalogGenerationDigest
	if selector.Backend == store.ServiceRuntimeV3 {
		wantCatalog = selector.CatalogRootDigest
	}
	return wantCatalog != "" &&
		scope.summary.CatalogGeneration == wantCatalog &&
		scope.prepared.resolved.currentCatalogGeneration == wantCatalog &&
		scope.prepared.resolved.catalogControlRevision == selector.CatalogControlRevision
}

func confirmRuntimeRepository(
	ctx context.Context,
	indexDir string,
	st RuntimeRepositoryStore,
	scope RuntimeScope,
) error {
	repo, err := st.GetRepo(ctx, scope.repo.Name)
	if err != nil || !sameRuntimeRepo(scope.repo, repo) {
		return unavailablef("repository generation changed")
	}
	revisions, err := runtimeRevisions(*repo)
	if err != nil {
		return err
	}
	search, source, err := focusedindex.ReadRepositorySearchGeneration(
		indexDir, repo.Name, revisions,
	)
	if err != nil || search.Digest != scope.search.Digest ||
		source.Digest != scope.source.Digest {
		return unavailablef("repository search generation changed")
	}
	return nil
}

func runtimeRevisions(repo store.Repo) ([]store.IndexedRevision, error) {
	if repo.Deleting || repo.IndexedCommitHash == "" ||
		(repo.IndexedAnalysisUnit != nil &&
			repo.IndexedAnalysisUnit.SearchIndexPosture == analysisunit.SearchIndexFocused) {
		return nil, unavailablef("repository has no direct whole-search generation")
	}
	if len(repo.IndexedRevisions) == 0 {
		return []store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD", Commit: repo.IndexedCommitHash,
		}}, nil
	}
	return slices.Clone(repo.IndexedRevisions), nil
}

func sameRuntimeRepo(expected store.Repo, current *store.Repo) bool {
	if current == nil || current.Name != expected.Name || current.Deleting ||
		current.IndexedCommitHash != expected.IndexedCommitHash ||
		!slices.Equal(current.IndexedRevisions, expected.IndexedRevisions) {
		return false
	}
	if expected.IndexedAnalysisUnit == nil || current.IndexedAnalysisUnit == nil {
		return expected.IndexedAnalysisUnit == nil && current.IndexedAnalysisUnit == nil
	}
	return expected.IndexedAnalysisUnit.Digest == current.IndexedAnalysisUnit.Digest &&
		expected.IndexedAnalysisUnit.SearchIndexPosture ==
			current.IndexedAnalysisUnit.SearchIndexPosture
}

func cloneRuntimeRepo(repo store.Repo) store.Repo {
	repo.IndexedRevisions = slices.Clone(repo.IndexedRevisions)
	if repo.IndexedAnalysisUnit != nil {
		unit := *repo.IndexedAnalysisUnit
		repo.IndexedAnalysisUnit = &unit
	}
	return repo
}

func cloneSearchManifest(manifest repositoryindex.SearchManifest) repositoryindex.SearchManifest {
	manifest.Revisions = slices.Clone(manifest.Revisions)
	manifest.PhysicalRoot.Members = slices.Clone(manifest.PhysicalRoot.Members)
	return manifest
}

func cloneSourceManifest(manifest repositoryindex.SourceManifest) repositoryindex.SourceManifest {
	manifest.Revisions = slices.Clone(manifest.Revisions)
	manifest.RevisionMembers = slices.Clone(manifest.RevisionMembers)
	manifest.Members = slices.Clone(manifest.Members)
	return manifest
}
