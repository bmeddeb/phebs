// Package servicequery compiles one authorized service scope into an exact
// repository, revision, and placement predicate for zoekt. It does not open a
// reader or expose a product surface; T34.3 owns the generation-bound reader.
package servicequery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/sourcegraph/zoekt/query"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	AuthoritySchema = "phebs-service-query-authority-v1"
	PredicatePolicy = "exact-service-placement-prefix-v1"

	// MaxExpressionBytes bounds parsing and the expression identity retained by
	// one compiled request. Transport body limits may be smaller.
	MaxExpressionBytes = 16 << 10
	MaxAuthorityBytes  = 16 << 10
	// regexp.QuoteMeta can at most double the admitted path bytes. Each exact
	// path atom adds the fixed ^(?:...)(?:/|$) envelope.
	MaxPredicateBytes = 2*servicecatalog.MaxServicePathBytes +
		servicecatalog.MaxServicePaths*len("^(?:)(?:/|$)")
)

var (
	ErrInvalidScope           = errors.New("invalid service query scope")
	ErrInvalidExpression      = errors.New("invalid service query expression")
	ErrUnsupportedComposition = errors.New("unsupported service query composition")
	ErrUnavailable            = errors.New("service query scope unavailable")
)

// Request carries the already authorized scopes to compile. Initial v2
// accepts exactly one repository-local scope; using a slice makes the future
// cross-repository boundary explicit rather than silently choosing one.
type Request struct {
	Expression       string
	RevisionSelector string
	Scopes           []PreparedScope
}

// Scope is the complete authority needed to derive a service's active
// placement set. CurrentCatalog and Summary fence the request/cursor snapshot;
// ActiveCatalog may be an older immutable generation for an explicitly stale
// service. Search is the strict-open T34.1 root the later reader must hold.
type Scope struct {
	CurrentCatalog servicecatalog.Publication
	ActiveCatalog  servicecatalog.Publication
	Summary        servicecatalog.RepositoryState
	State          servicecatalog.ServiceState
	Search         repositoryindex.SearchManifest
}

// V3Scope is the dark segmented-catalog equivalent of Scope. The caller
// supplies projections read through a verified root/member lease; no v2
// publication is consulted or accepted as fallback.
type V3Scope struct {
	CurrentRoot            servicecatalogv3.Root
	CurrentControlRevision uint64
	ActiveRoot             servicecatalogv3.Root
	Summary                servicecatalog.RepositoryState
	State                  servicecatalog.ServiceState
	DesiredProjection      servicecatalog.ServiceProjection
	ActiveProjection       servicecatalog.ServiceProjection
	Search                 repositoryindex.SearchManifest
}

// PreparedScope is an opaque, fully verified placement and generation
// authority. Callers may cache it only under its embedded state/search fences;
// compiling another expression or indexed selector then performs no catalog
// decode, source-member read, shard read, or placement reconstruction.
type PreparedScope struct {
	resolved resolvedScope
	valid    bool
}

// Compiled is one pre-ranking zoekt query plus its closed authority receipt.
// Query already contains repository, exact branch, and path predicates; a
// caller must not apply result-time service filtering.
type Compiled struct {
	Query     query.Q
	Authority Authority
}

// Prepare validates every catalog/state/search binding and derives the exact
// active placement set once. Current and active catalog generations are each
// decoded at most once; equal generations share the verified decode.
func Prepare(scope Scope) (PreparedScope, error) {
	resolved, err := validateScope(scope)
	if err != nil {
		return PreparedScope{}, err
	}
	return PreparedScope{resolved: resolved, valid: true}, nil
}

func PrepareV3(scope V3Scope) (PreparedScope, error) {
	resolved, err := validateV3Scope(scope)
	if err != nil {
		return PreparedScope{}, err
	}
	return PreparedScope{resolved: resolved, valid: true}, nil
}

// Compile parses one expression and allocates bounded predicates from an
// already prepared scope.
func Compile(request Request) (Compiled, error) {
	if len(request.Scopes) != 1 {
		if len(request.Scopes) > 1 {
			return Compiled{}, fmt.Errorf(
				"%w: cross-repository service queries are not admitted",
				ErrUnsupportedComposition,
			)
		}
		return Compiled{}, invalidf("exactly one repository scope is required")
	}
	if len(request.Expression) > MaxExpressionBytes ||
		!utf8.ValidString(request.Expression) {
		return Compiled{}, fmt.Errorf(
			"%w: expression must be valid UTF-8 of at most %d bytes",
			ErrInvalidExpression,
			MaxExpressionBytes,
		)
	}
	if !validText(request.RevisionSelector) {
		return Compiled{}, invalidf("revision selector is invalid")
	}

	if !request.Scopes[0].valid {
		return Compiled{}, invalidf("service scope was not prepared")
	}
	resolved := request.Scopes[0].resolved
	foundRevision := false
	for _, candidate := range resolved.revisions {
		if candidate.Selector == request.RevisionSelector {
			foundRevision = true
			resolved.revision = indexedRevision{
				Selector: candidate.Selector,
				Branch:   candidate.Branch,
				Commit:   candidate.Commit,
			}
			break
		}
	}
	if !foundRevision {
		return Compiled{}, unavailablef(
			"revision selector %q is not in the active search generation",
			request.RevisionSelector,
		)
	}
	parsed, err := query.Parse(request.Expression)
	if err != nil {
		return Compiled{}, fmt.Errorf("%w: parse expression: %v", ErrInvalidExpression, err)
	}
	pathQuery, predicateBytes, err := compilePaths(resolved.paths)
	if err != nil {
		return Compiled{}, err
	}
	compiled := query.Simplify(query.NewAnd(
		query.NewRepoSet(resolved.repository),
		&query.Branch{Pattern: resolved.revision.Branch, Exact: true},
		parsed,
		pathQuery,
	))

	authority, err := newAuthority(
		request.Expression, resolved, predicateBytes,
	)
	if err != nil {
		return Compiled{}, err
	}
	return Compiled{Query: compiled, Authority: authority}, nil
}

type resolvedScope struct {
	repository               string
	serviceKey               string
	status                   string
	incarnation              uint64
	revision                 indexedRevision
	currentCatalogGeneration string
	catalogControlRevision   uint64
	activeProjection         servicecatalog.ServiceProjection
	summary                  servicecatalog.RepositoryState
	state                    servicecatalog.ServiceState
	search                   repositoryindex.SearchManifest
	revisions                []store.IndexedRevision
	paths                    []string
	pathBytes                int
}

type indexedRevision struct {
	Selector string
	Branch   string
	Commit   string
}

func validateScope(scope Scope) (resolvedScope, error) {
	current, err := servicecatalog.VerifyPublication(scope.CurrentCatalog, true)
	if err != nil {
		return resolvedScope{}, invalidWrap("current catalog", err)
	}
	activePublication := scope.ActiveCatalog
	active := current
	if scope.ActiveCatalog.GenerationDigest == scope.CurrentCatalog.GenerationDigest {
		activePublication = scope.CurrentCatalog
	} else {
		activePersisted := scope.ActiveCatalog.ControlRevision != 0 ||
			!scope.ActiveCatalog.PublishedAt.IsZero()
		active, err = servicecatalog.VerifyPublication(
			scope.ActiveCatalog, activePersisted,
		)
		if err != nil {
			return resolvedScope{}, invalidWrap("active catalog", err)
		}
	}
	if err := servicecatalog.ValidateRepositoryState(scope.Summary, true); err != nil {
		return resolvedScope{}, invalidWrap("repository summary", err)
	}
	if err := servicecatalog.ValidateServiceState(scope.State, true); err != nil {
		return resolvedScope{}, invalidWrap("service state", err)
	}
	if err := repositoryindex.ValidateSearchManifest(scope.Search); err != nil {
		return resolvedScope{}, invalidWrap("repository search generation", err)
	}

	repository := scope.CurrentCatalog.Repository
	if scope.Summary.Repository != repository || scope.State.Repository != repository ||
		activePublication.Repository != repository || scope.Search.Repository != repository {
		return resolvedScope{}, invalidf("repository identities disagree")
	}
	if scope.Summary.CatalogGeneration != scope.CurrentCatalog.GenerationDigest ||
		scope.Summary.CatalogControlRevision != scope.CurrentCatalog.ControlRevision {
		return resolvedScope{}, invalidf("catalog and summary fences disagree")
	}

	desired, found, err := current.ProjectService(scope.State.ServiceKey)
	if err != nil {
		return resolvedScope{}, invalidWrap("desired service projection", err)
	}
	if !found || desired.Removed ||
		desired.Service.Disposition != servicecatalog.DispositionAccepted {
		return resolvedScope{}, unavailablef("service has no accepted desired projection")
	}
	if err := serviceStateMatchesProjection(scope.State, desired, false); err != nil {
		return resolvedScope{}, err
	}

	switch scope.State.Status {
	case servicecatalog.StatusCurrent, servicecatalog.StatusStale:
	default:
		return resolvedScope{}, unavailablef(
			"service status %q has no searchable active generation",
			scope.State.Status,
		)
	}
	activeProjection, found, err := active.ProjectService(scope.State.ServiceKey)
	if err != nil {
		return resolvedScope{}, invalidWrap("active service projection", err)
	}
	if !found || activeProjection.Removed ||
		activeProjection.Service.Disposition != servicecatalog.DispositionAccepted {
		return resolvedScope{}, invalidf("active catalog has no accepted service projection")
	}
	if err := serviceStateMatchesProjection(scope.State, activeProjection, true); err != nil {
		return resolvedScope{}, err
	}

	if scope.Search.TopologyPolicy != repositoryindex.DirectTopologyPolicy ||
		scope.Search.SourceGenerationDigest == "" {
		return resolvedScope{}, invalidf("search generation is not direct source authority")
	}
	if len(scope.Search.Revisions) == 0 ||
		scope.Search.Revisions[0].Commit != activePublication.SourceCommit {
		return resolvedScope{}, invalidf(
			"active catalog source commit is not the search generation HEAD",
		)
	}
	paths, pathBytes, err := resolvePaths(activeProjection)
	if err != nil {
		return resolvedScope{}, err
	}

	return resolvedScope{
		repository: repository, serviceKey: scope.State.ServiceKey,
		status: scope.State.Status, incarnation: scope.State.Incarnation,
		currentCatalogGeneration: scope.CurrentCatalog.GenerationDigest,
		catalogControlRevision:   scope.CurrentCatalog.ControlRevision,
		activeProjection:         activeProjection, summary: scope.Summary,
		state: scope.State, search: scope.Search,
		revisions: slices.Clone(scope.Search.Revisions),
		paths:     paths, pathBytes: pathBytes,
	}, nil
}

func validateV3Scope(scope V3Scope) (resolvedScope, error) {
	if err := servicecatalogv3.ValidateRoot(scope.CurrentRoot); err != nil {
		return resolvedScope{}, invalidWrap("current catalog", err)
	}
	activeRoot := scope.ActiveRoot
	if activeRoot.Digest == scope.CurrentRoot.Digest {
		activeRoot = scope.CurrentRoot
	} else if err := servicecatalogv3.ValidateRoot(activeRoot); err != nil {
		return resolvedScope{}, invalidWrap("active catalog", err)
	}
	if scope.CurrentControlRevision == 0 {
		return resolvedScope{}, invalidf("current catalog control revision is missing")
	}
	if err := servicecatalogv3.ValidateRepositoryState(scope.Summary, true); err != nil {
		return resolvedScope{}, invalidWrap("repository summary", err)
	}
	if err := servicecatalogv3.ValidateServiceState(scope.State, true); err != nil {
		return resolvedScope{}, invalidWrap("service state", err)
	}
	if err := repositoryindex.ValidateSearchManifest(scope.Search); err != nil {
		return resolvedScope{}, invalidWrap("repository search generation", err)
	}

	repository := scope.CurrentRoot.Binding.Repository
	if scope.Summary.Repository != repository || scope.State.Repository != repository ||
		activeRoot.Binding.Repository != repository || scope.Search.Repository != repository {
		return resolvedScope{}, invalidf("repository identities disagree")
	}
	if scope.Summary.CatalogGeneration != scope.CurrentRoot.Digest ||
		scope.Summary.CatalogControlRevision != scope.CurrentControlRevision {
		return resolvedScope{}, invalidf("catalog and summary fences disagree")
	}
	if scope.DesiredProjection.Removed ||
		scope.DesiredProjection.Service.Disposition != servicecatalog.DispositionAccepted {
		return resolvedScope{}, unavailablef("service has no accepted desired projection")
	}
	if err := servicecatalogv3.ValidateStateProjection(
		scope.State, scope.DesiredProjection, false,
	); err != nil {
		return resolvedScope{}, invalidWrap("desired service projection", err)
	}
	if scope.State.Status != servicecatalog.StatusCurrent &&
		scope.State.Status != servicecatalog.StatusStale {
		return resolvedScope{}, unavailablef(
			"service status %q has no searchable active generation", scope.State.Status,
		)
	}
	if scope.ActiveProjection.Removed ||
		scope.ActiveProjection.Service.Disposition != servicecatalog.DispositionAccepted {
		return resolvedScope{}, invalidf("active catalog has no accepted service projection")
	}
	if err := servicecatalogv3.ValidateStateProjection(
		scope.State, scope.ActiveProjection, true,
	); err != nil {
		return resolvedScope{}, invalidWrap("active service projection", err)
	}
	if scope.ActiveProjection.CatalogGeneration != activeRoot.Digest {
		return resolvedScope{}, invalidf("active projection and catalog disagree")
	}
	if scope.Search.TopologyPolicy != repositoryindex.DirectTopologyPolicy ||
		scope.Search.SourceGenerationDigest == "" {
		return resolvedScope{}, invalidf("search generation is not direct source authority")
	}
	if len(scope.Search.Revisions) == 0 ||
		scope.Search.Revisions[0].Commit != activeRoot.Binding.Source.Commit {
		return resolvedScope{}, invalidf(
			"active catalog source commit is not the search generation HEAD",
		)
	}
	paths, pathBytes, err := resolvePaths(scope.ActiveProjection)
	if err != nil {
		return resolvedScope{}, err
	}
	return resolvedScope{
		repository: repository, serviceKey: scope.State.ServiceKey,
		status: scope.State.Status, incarnation: scope.State.Incarnation,
		currentCatalogGeneration: scope.CurrentRoot.Digest,
		catalogControlRevision:   scope.CurrentControlRevision,
		activeProjection:         scope.ActiveProjection, summary: scope.Summary,
		state: scope.State, search: scope.Search,
		revisions: slices.Clone(scope.Search.Revisions),
		paths:     paths, pathBytes: pathBytes,
	}, nil
}

func resolvePaths(
	projection servicecatalog.ServiceProjection,
) ([]string, int, error) {
	paths := make([]string, 0, len(projection.Memberships))
	pathBytes := 0
	for _, membership := range projection.Memberships {
		if len(paths) > 0 && paths[len(paths)-1] == membership.Path {
			continue
		}
		if len(paths) >= servicecatalog.MaxServicePaths ||
			pathBytes > servicecatalog.MaxServicePathBytes-len(membership.Path) {
			return nil, 0, invalidf("active service placement bound exceeded")
		}
		paths = append(paths, membership.Path)
		pathBytes += len(membership.Path)
	}
	if len(paths) == 0 {
		return nil, 0, unavailablef("active service has no placement paths")
	}
	return paths, pathBytes, nil
}

func serviceStateMatchesProjection(
	state servicecatalog.ServiceState,
	projection servicecatalog.ServiceProjection,
	active bool,
) error {
	desired, err := servicecatalog.ServiceDesiredGeneration(
		projection, state.Incarnation,
	)
	if err != nil {
		return invalidWrap("service projection generation", err)
	}
	if active {
		if state.ActiveDesiredGeneration != desired ||
			state.ActiveSourceGeneration != projection.SourceGeneration ||
			state.ActiveCatalogGeneration != projection.CatalogGeneration {
			return invalidf("active service identities disagree with active catalog")
		}
		return nil
	}
	if state.DesiredGeneration != desired ||
		state.DesiredSourceGeneration != projection.SourceGeneration ||
		state.DesiredCatalogGeneration != projection.CatalogGeneration {
		return invalidf("desired service identities disagree with current catalog")
	}
	service := projection.Service
	if state.ServiceKey != service.Key || state.DisplayName != service.DisplayName ||
		state.Disposition != service.Disposition || state.Origin != service.Origin ||
		state.Reason != service.Reason || !slices.Equal(state.Successors, service.Successors) {
		return invalidf("service state record disagrees with current catalog")
	}
	return nil
}

func compilePaths(paths []string) (query.Q, int, error) {
	queries := make([]query.Q, 0, len(paths))
	predicateBytes := 0
	for _, path := range paths {
		pattern := "^(?:" + regexp.QuoteMeta(path) + ")(?:/|$)"
		if predicateBytes > MaxPredicateBytes-len(pattern) {
			return nil, 0, invalidf("compiled path predicates exceed %d bytes", MaxPredicateBytes)
		}
		predicateBytes += len(pattern)
		compiled, err := syntax.Parse(pattern, syntax.Perl)
		if err != nil {
			return nil, 0, invalidWrap("compile placement predicate", err)
		}
		queries = append(queries, &query.Regexp{
			Regexp: compiled, FileName: true, CaseSensitive: true,
		})
	}
	return query.NewOr(queries...), predicateBytes, nil
}

func pathDigest(paths []string) (string, error) {
	encoded, err := json.Marshal(paths)
	if err != nil {
		return "", err
	}
	return digest("phebs-service-query-paths-v1\x00", encoded), nil
}

func expressionDigest(expression string) string {
	return digest("phebs-service-query-expression-v1\x00", []byte(expression))
}

func digest(domain string, content []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(content)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validText(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, current := range value {
		if current < 0x20 || current == 0x7f {
			return false
		}
	}
	return true
}

func invalidWrap(label string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrInvalidScope, label, err)
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidScope, fmt.Sprintf(format, args...))
}

func unavailablef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnavailable, fmt.Sprintf(format, args...))
}
