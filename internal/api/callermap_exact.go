package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	exactCallerMapSchemaVersion = "caller-map-v2"
	exactCallerMapCursorVersion = "caller-map-cursor-v2"
	exactCallerCitationVersion  = "caller-map-citation-v1"
	exactCallerAuthorityVersion = "caller-map-authority-v1"
	exactCallerMapPlane         = "repository-overlay"

	exactCallerMapMaxRecords = callerleaf.MaxAggregateResultRecords +
		callerleaf.MaxAggregateAbstentionRecords
	exactCallerMapMaxIdentityBytes = 128 << 20
	exactCallerMapMaxIndexes       = 8
	exactCallerMapMaxBindings      = 8
	exactCallerMapBindingRefs      = exactCallerMapMaxRecords
	exactCallerMapBindingLifetime  = 5 * time.Minute
	exactCallerReadLimit           = 8
	exactCallerCitationReadLimit   = 2
	exactCallerUnitCandidateLimit  = 25_000
	exactCallerUnitValueLimit      = 64
	exactCallerUnitValueBytes      = 4 << 10
	// These deterministic charges cover the retained Go candidate struct and
	// each retained string header in addition to separately counted payload.
	exactCallerUnitCandidateStructuralBytes = 112
	exactCallerUnitValueStructuralBytes     = 16
)

var errExactCallerProjectionInvalid = errors.New(
	"caller publication record is semantically invalid",
)

type exactCallerMapService struct {
	opts Options

	mu           sync.Mutex
	indexBuild   chan struct{}
	exactRead    chan struct{}
	citationRead chan struct{}
	secret       [sha256.Size]byte
	indexes      map[string]*exactCallerIndex
	indexOrder   []string
	bindings     map[string]*exactCallerBinding
	bindingCount int
	bindingRefs  int
}

type exactCallerIndex struct {
	key           string
	records       []exactCallerRecord
	endpoints     map[exactCallerEndpointKey]exactCallerEndpointIndex
	identityBytes int
	failure       exactCallerIndexFailure
	bindings      int
	uses          int
}

type exactCallerIndexFailure uint8

const (
	exactCallerIndexUsable exactCallerIndexFailure = iota
	exactCallerIndexSemanticFailure
	exactCallerIndexLimitFailure
)

type exactCallerEndpointIndex struct {
	source []int
	unit   []int
}

// exactCallerEndpointKey copies only two string headers into the map and
// reuses the already counted immutable record payload. Concatenating a string
// key would retain a second operation copy per endpoint outside the index's
// identity budget.
type exactCallerEndpointKey struct {
	protocol  string
	operation string
}

type exactCallerRecord struct {
	reference callerpublication.RecordReference
	recordID  string
	protocol  string
	operation string
	lineage   string

	classification   string
	resolution       string
	tier             string
	codeRole         string
	path             string
	objectID         string
	blobDigest       string
	startByte        int
	endByte          int
	startLine        int
	endLine          int
	unitGroup        string
	unitState        string
	unitReason       string
	unitCandidates   []CallerMapUnitCandidate
	unresolvedReason string
}

type exactCallerBinding struct {
	id             string
	createdAt      time.Time
	queryDigest    string
	visibility     VisibilityContext
	indexKey       string
	records        []int
	publication    callerexecute.PublicationBinding
	declaration    ContractCatalogClaim
	generation     CallerMapGeneration
	scope          AnalysisScopeProjection
	excludedGoTest int
	comparison     *exactCallerComparisonBinding
	uses           int
	retired        bool
	finalized      bool
}

// exactCallerComparisonBinding retains only fixed-size union metadata and a
// bounded sample of index positions. Exact caller records and their strings
// remain owned by the at-most-two pinned reverse indexes.
type exactCallerComparisonBinding struct {
	old         exactCallerComparisonSource
	replacement exactCallerComparisonSource
	snapshot    string
	entries     []exactCallerComparisonEntry
}

type exactCallerComparisonSource struct {
	visibility  VisibilityContext
	indexKey    string
	publication callerexecute.PublicationBinding
	declaration ContractCatalogClaim
	generation  CallerMapGeneration
}

type exactCallerComparisonEntry struct {
	kind              uint8
	unresolved        bool
	representativeOld bool
	representative    int
	oldCount          int
	oldStart          int
	oldEnd            int
	replacementCount  int
	replacementStart  int
	replacementEnd    int
}

type exactCallerCursor struct {
	Schema      string            `json:"schema"`
	Binding     string            `json:"binding"`
	QueryDigest string            `json:"query_digest"`
	Visibility  VisibilityContext `json:"visibility"`
	Generation  string            `json:"generation_digest"`
	Manifest    string            `json:"manifest_digest"`
	PairSet     string            `json:"pair_set_digest"`
	Revision    uint64            `json:"publication_revision"`
	Offset      int               `json:"offset"`
}

type exactCallerAuthority struct {
	Schema             string                               `json:"schema"`
	QueryDigest        string                               `json:"query_digest"`
	Repository         string                               `json:"repository"`
	Visibility         VisibilityContext                    `json:"visibility"`
	RepositoryRevision uint64                               `json:"repository_revision"`
	Snapshot           string                               `json:"snapshot"`
	MatchingRowsState  string                               `json:"matching_rows_state"`
	Generation         CallerMapGeneration                  `json:"generation"`
	ScopeDigest        string                               `json:"scope_digest"`
	ProjectionFailed   bool                                 `json:"projection_failed,omitempty"`
	Publication        *callerexecute.PublicationDescriptor `json:"publication,omitempty"`
}

type exactCallerCitation struct {
	Schema     string `json:"schema"`
	Repository string `json:"repository"`
	Binding    string `json:"binding"`
	Position   int    `json:"position"`
	RecordID   string `json:"record_id"`
	Side       string `json:"side,omitempty"`
}

// CallerMapCitation is the only source content returned by a repository-
// overlay citation. Content is exactly the cited byte range, not the file.
type CallerMapCitation struct {
	SchemaVersion string              `json:"schema_version"`
	Generation    CallerMapGeneration `json:"generation"`
	Source        CallerMapSource     `json:"source"`
	Content       string              `json:"content"`
}

func newExactCallerMapService(opts Options) *exactCallerMapService {
	service := &exactCallerMapService{
		opts: opts, indexes: make(map[string]*exactCallerIndex),
		bindings:     make(map[string]*exactCallerBinding),
		indexBuild:   make(chan struct{}, 1),
		exactRead:    make(chan struct{}, exactCallerReadLimit),
		citationRead: make(chan struct{}, exactCallerCitationReadLimit),
	}
	if _, err := rand.Read(service.secret[:]); err != nil {
		return nil
	}
	return service
}

func (service *exactCallerMapService) list(
	ctx context.Context,
	query CallerMapQuery,
	pageSize int,
	cursor string,
) (*CallerMapPage, error) {
	if service == nil || service.opts.CallerReader == nil {
		return nil, huma.Error503ServiceUnavailable("caller map unavailable")
	}
	if !catalogAuthenticated(ctx, service.opts) {
		return nil, huma.Error404NotFound("caller map not found")
	}
	pack, err := validateCallerMapQuery(query)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if pageSize == 0 {
		pageSize = callerMapDefaultPage
	}
	if pageSize < 1 || pageSize > callerMapMaxPage {
		return nil, huma.Error400BadRequest(
			fmt.Sprintf("page_size must be from 1 through %d", callerMapMaxPage),
		)
	}
	query = normalizeCallerMapQuery(query)
	queryDigest := digestJSON(struct {
		Query CallerMapQuery `json:"query"`
		Size  int            `json:"page_size"`
	}{query, pageSize})

	repository, visibility, err := service.authorize(ctx, query.Endpoint.Repository)
	if err != nil {
		return nil, err
	}
	repositoryRevision, err := exactCallerRepositoryRevision(repository)
	if err != nil {
		return nil, err
	}
	finishExactRead, err := service.beginExactRead(ctx)
	if err != nil {
		return nil, err
	}
	defer finishExactRead()
	if cursor != "" {
		return service.continuePage(
			ctx, query, pageSize, cursor, queryDigest, visibility,
		)
	}

	read, err := service.opts.CallerReader.Open(ctx, repository.Name)
	if err != nil {
		return nil, exactCallerReaderError("open caller generation", err)
	}
	if read == nil {
		return nil, huma.Error500InternalServerError(
			"open caller generation", errors.New("caller reader returned no state"),
		)
	}
	defer func() { _ = read.Release() }()
	if read.Availability != callerexecute.PublicationCurrent {
		return service.gapPage(
			ctx, query, queryDigest, read, visibility, repositoryRevision,
		)
	}
	declaration, err := exactCallerDeclaration(
		ctx, service.opts.Evidence, read, pack, query,
	)
	if err != nil {
		return nil, err
	}
	index, err := service.index(ctx, read)
	if err != nil {
		if errors.Is(err, errExactCallerProjectionInvalid) {
			return service.projectionFailurePage(
				ctx, query, queryDigest, read, visibility,
				repositoryRevision,
			)
		}
		return nil, err
	}
	defer service.releaseIndex(index)
	ordered := index.endpoints[exactEndpointKey(protocolExtractorName(pack.protocol), query.Endpoint.Operation)]
	positions := ordered.source
	if query.Ordering == "unit" {
		positions = ordered.unit
	}
	filtered := make([]int, 0, len(positions))
	for _, position := range positions {
		if exactCallerRecordMatches(index.records[position], query) {
			filtered = append(filtered, position)
		}
	}
	if len(filtered) > exactCallerMapBindingRefs {
		return nil, huma.Error422UnprocessableEntity("caller map result exceeds its bounded generation")
	}

	publicationBinding, err := read.Binding()
	if err != nil {
		return nil, huma.Error500InternalServerError("bind caller generation", err)
	}
	binding := &exactCallerBinding{
		createdAt: time.Now(), queryDigest: queryDigest, visibility: visibility,
		indexKey: index.key, records: filtered,
		publication: publicationBinding, declaration: declaration,
		generation: service.generation(read), scope: callerAnalysisScope(repository),
	}
	if read.Publication == nil {
		return nil, huma.Error500InternalServerError(
			"project caller generation counts",
			errors.New("caller publication pair payload is unavailable"),
		)
	}
	candidateRecords, excludedGoTest, err := exactCallerRecordCounts(read.Publication)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"project caller generation counts", err,
		)
	}
	binding.excludedGoTest = excludedGoTest
	binding.generation.ExcludedGoTestRecords = excludedGoTest
	binding.generation.RecordCounts = &CallerMapRecordCounts{
		CandidateRecords:      candidateRecords,
		BaseRecords:           candidateRecords - excludedGoTest,
		ExcludedGoTestRecords: excludedGoTest,
	}
	// Every serialized row carries a citation. Retain the same bounded request
	// binding for citations even when the first page is also the final page;
	// the compact token never embeds the potentially maximum-shaped generation
	// and publication state.
	if len(filtered) > 0 {
		if err := service.retainActiveBinding(binding); err != nil {
			return nil, err
		}
		defer service.releaseBinding(binding)
	}
	page, err := service.page(ctx, read, index, binding, query, pageSize, 0)
	if err != nil {
		if binding.id != "" {
			service.dropBinding(binding.id)
		}
		return nil, err
	}
	confirmedRepository, err := service.confirmWithRepository(
		ctx, read, query.Endpoint.Repository, visibility,
	)
	if err != nil || analysisScopeDigest(callerAnalysisScope(confirmedRepository)) !=
		analysisScopeDigest(binding.scope) {
		if binding.id != "" {
			service.dropBinding(binding.id)
		}
		if err != nil {
			return nil, err
		}
		return nil, exactCallerAuthorityConflict()
	}
	return page, nil
}

func (service *exactCallerMapService) generationProgress(
	ctx context.Context,
	repositoryName string,
) (*CallerGenerationProgress, error) {
	if service == nil || service.opts.CallerReader == nil {
		return nil, huma.Error503ServiceUnavailable("caller generation progress unavailable")
	}
	if !catalogAuthenticated(ctx, service.opts) {
		return nil, huma.Error404NotFound("caller generation progress not found")
	}
	repository, visibility, err := service.authorize(ctx, repositoryName)
	if err != nil {
		return nil, err
	}
	revision, err := exactCallerRepositoryRevision(repository)
	if err != nil {
		return nil, err
	}
	finishExactRead, err := service.beginExactRead(ctx)
	if err != nil {
		return nil, err
	}
	defer finishExactRead()

	read, err := service.opts.CallerReader.Open(ctx, repository.Name)
	if err != nil {
		return nil, exactCallerReaderError("open caller generation progress", err)
	}
	if read == nil {
		return nil, huma.Error500InternalServerError(
			"open caller generation progress",
			errors.New("caller reader returned no state"),
		)
	}
	defer func() { _ = read.Release() }()

	generation := service.generation(read)
	if read.Availability == callerexecute.PublicationCurrent {
		confirmed, confirmErr := service.confirmWithRepository(
			ctx, read, repository.Name, visibility,
		)
		if confirmErr != nil {
			return nil, confirmErr
		}
		confirmedRevision, revisionErr := exactCallerRepositoryRevision(confirmed)
		if revisionErr != nil {
			return nil, revisionErr
		}
		if confirmedRevision != revision ||
			analysisScopeDigest(callerAnalysisScope(confirmed)) !=
				analysisScopeDigest(callerAnalysisScope(repository)) {
			return nil, exactCallerAuthorityConflict()
		}
	} else {
		generation = service.unavailableGeneration(read)
		current, currentErr := service.opts.CallerReader.UnavailableCurrent(ctx, read)
		if currentErr != nil {
			return nil, huma.Error500InternalServerError(
				"confirm caller generation progress", currentErr,
			)
		}
		if !current {
			return nil, huma.Error409Conflict(
				"caller generation changed while building the response",
			)
		}
		confirmed, confirmErr := service.confirmAuthorization(
			ctx, repository.Name, visibility,
		)
		if confirmErr != nil {
			return nil, confirmErr
		}
		confirmedRevision, revisionErr := exactCallerRepositoryRevision(confirmed)
		if revisionErr != nil {
			return nil, revisionErr
		}
		if confirmedRevision != revision ||
			analysisScopeDigest(callerAnalysisScope(confirmed)) !=
				analysisScopeDigest(callerAnalysisScope(repository)) {
			return nil, exactCallerAuthorityConflict()
		}
	}
	projectionStore, ok := service.opts.Store.(store.CallerJobProjectionStore)
	if !ok {
		return nil, huma.Error503ServiceUnavailable(
			"caller job projection unavailable",
		)
	}
	callerJobState, callerJob, err := projectionStore.GetCallerJobProjection(
		ctx, repository.Name,
	)
	if errors.Is(err, store.ErrNotFound) {
		return nil, exactCallerAuthorityConflict()
	}
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"read caller job projection", err,
		)
	}
	if !validCallerJobProjection(callerJobState, callerJob) {
		return nil, huma.Error500InternalServerError(
			"project caller generation progress",
			errors.New("caller job projection is invalid"),
		)
	}

	progress := &CallerGenerationProgress{
		SchemaVersion:  callerProgressSchema,
		Generation:     generation,
		Scope:          callerGenerationProgressScope(repository),
		CallerJobState: callerJobState,
		CallerJob:      callerJob,
	}
	encoded, err := json.Marshal(progress)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"project caller generation progress", err,
		)
	}
	if len(encoded) > callerProgressLimit {
		return nil, huma.Error500InternalServerError(
			"project caller generation progress",
			errors.New("caller generation progress exceeds its response bound"),
		)
	}
	if !validCallerPartitionProgress(progress.Generation.PartitionProgress) {
		return nil, huma.Error500InternalServerError(
			"project caller generation progress",
			errors.New("caller generation progress is invalid"),
		)
	}
	return progress, nil
}

func validCallerJobProjection(
	state store.JobProjectionState,
	job *store.CallerJobProjection,
) bool {
	switch state {
	case store.JobProjectionUnavailable:
		return job == nil
	case store.JobProjectionExact:
		return job != nil && slices.Contains([]store.JobStatus{
			store.StatusPending, store.StatusClaimed, store.StatusRunning,
			store.StatusDone, store.StatusFailed, store.StatusCanceled,
		}, job.Status) && job.Attempts >= 0 && job.Attempts <= 1_000_000
	default:
		return false
	}
}

func callerGenerationProgressScope(
	repository store.Repo,
) CallerGenerationProgressScope {
	result := CallerGenerationProgressScope{
		Repository: repository.Name, Commit: repository.IndexedCommitHash,
		ScopePosture: analysisunit.SearchIndexWholeRepository,
	}
	if repository.IndexedAnalysisUnit == nil {
		return result
	}
	result.ScopePosture = repository.IndexedAnalysisUnit.SearchIndexPosture
	result.AnalysisUnitDigest = repository.IndexedAnalysisUnit.Digest
	result.PrimaryPathCount = repository.IndexedAnalysisUnit.PrimaryPathCount
	result.SupportingPathCount = repository.IndexedAnalysisUnit.SupportingPathCount
	return result
}

func (service *exactCallerMapService) continuePage(
	ctx context.Context,
	query CallerMapQuery,
	pageSize int,
	encoded, queryDigest string,
	visibility VisibilityContext,
) (*CallerMapPage, error) {
	cursor, err := service.decodeCursor(encoded)
	if err != nil {
		return nil, err
	}
	if cursor.QueryDigest != queryDigest {
		return nil, huma.Error400BadRequest("caller map cursor does not match the query")
	}
	if cursor.Visibility != visibility {
		return nil, huma.Error409Conflict("caller map cursor is no longer valid")
	}
	binding := service.acquireBinding(cursor.Binding)
	if binding == nil {
		return nil, huma.Error409Conflict("caller map cursor is no longer valid")
	}
	defer service.releaseBinding(binding)
	if binding.queryDigest != queryDigest ||
		binding.visibility != visibility || cursor.Offset < 1 ||
		cursor.Offset >= len(binding.records) ||
		cursor.Generation != binding.generation.GenerationDigest ||
		cursor.Manifest != binding.generation.ManifestDigest ||
		cursor.PairSet != binding.generation.PairSetDigest ||
		cursor.Revision != binding.generation.PublicationRevision {
		return nil, huma.Error409Conflict("caller map cursor is no longer valid")
	}
	read, err := service.opts.CallerReader.Reopen(ctx, binding.publication)
	if err != nil {
		return nil, exactCallerReaderError("reopen caller generation", err)
	}
	if read == nil {
		return nil, huma.Error500InternalServerError(
			"reopen caller generation", errors.New("caller reader returned no state"),
		)
	}
	defer func() { _ = read.Release() }()
	if read.Availability != callerexecute.PublicationCurrent {
		service.dropBinding(binding.id)
		return nil, huma.Error409Conflict("caller map cursor is no longer valid")
	}
	index := service.acquireIndex(binding.indexKey)
	if index == nil {
		service.dropBinding(binding.id)
		return nil, huma.Error409Conflict("caller map cursor snapshot expired")
	}
	defer service.releaseIndex(index)
	page, err := service.page(
		ctx, read, index, binding, query, pageSize, cursor.Offset,
	)
	if err != nil {
		return nil, err
	}
	confirmedRepository, err := service.confirmWithRepository(
		ctx, read, query.Endpoint.Repository, visibility,
	)
	if err != nil || analysisScopeDigest(callerAnalysisScope(confirmedRepository)) !=
		analysisScopeDigest(binding.scope) {
		service.dropBinding(binding.id)
		if err != nil {
			return nil, err
		}
		return nil, exactCallerAuthorityConflict()
	}
	return page, nil
}

func (service *exactCallerMapService) page(
	ctx context.Context,
	read *callerexecute.PublicationRead,
	index *exactCallerIndex,
	binding *exactCallerBinding,
	query CallerMapQuery,
	pageSize, offset int,
) (*CallerMapPage, error) {
	end := min(offset+pageSize, len(binding.records))
	rows := make([]CallerMapRow, 0, end-offset)
	for _, position := range binding.records[offset:end] {
		indexed := index.records[position]
		pair, record, err := read.Lease().ReadRecord(ctx, indexed.reference)
		if err != nil {
			if errors.Is(err, callerleaf.ErrInvalidArtifact) {
				return nil, huma.Error409Conflict("caller generation changed while reading")
			}
			return nil, huma.Error500InternalServerError("read caller record", err)
		}
		confirmed, err := projectExactCallerRecord(
			callerReadGeneration(read), pair, indexed.reference, record,
		)
		if err != nil || !sameExactCallerRecord(indexed, confirmed) {
			return nil, huma.Error409Conflict("caller generation changed while reading")
		}
		row := exactCallerRow(read, indexed)
		citation, err := service.encodeCitation(binding, position, indexed)
		if err != nil {
			return nil, huma.Error500InternalServerError("encode caller citation", err)
		}
		row.Source.Citation = citation
		rows = append(rows, row)
	}
	pagination := CallerMapPagination{Complete: end == len(binding.records)}
	if !pagination.Complete {
		cursor := exactCallerCursor{
			Schema: exactCallerMapCursorVersion, Binding: binding.id,
			QueryDigest: binding.queryDigest, Visibility: binding.visibility,
			Generation: binding.generation.GenerationDigest,
			Manifest:   binding.generation.ManifestDigest,
			PairSet:    binding.generation.PairSetDigest,
			Revision:   binding.generation.PublicationRevision, Offset: end,
		}
		pagination.NextCursor = service.encodeSigned(cursor)
	}
	declaration := binding.declaration
	snapshot := exactCallerPageSnapshot(binding)
	repositoryRevision := binding.publication.Summary.PublicationRevision
	publication, err := exactCallerRequiredPublicationDescriptor(read)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"describe caller map publication", err,
		)
	}
	authority, err := service.encodeExactAuthority(exactCallerAuthority{
		Schema: exactCallerAuthorityVersion, QueryDigest: binding.queryDigest,
		Repository: binding.generation.Repository, Visibility: binding.visibility,
		RepositoryRevision: repositoryRevision, Snapshot: snapshot,
		MatchingRowsState: "exact", Generation: binding.generation,
		ScopeDigest: analysisScopeDigest(binding.scope),
		Publication: publication,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"encode caller map authority", err,
		)
	}
	return &CallerMapPage{
		SchemaVersion: exactCallerMapSchemaVersion, Query: query,
		Declaration: &declaration, Rows: rows,
		Groups:            callerMapGroups(rows, query.Ordering),
		TotalMatchingRows: callerMapTotal(len(binding.records)), Pagination: pagination,
		Generation: &binding.generation, MatchingRowsState: "exact",
		Scope: func() *AnalysisScopeProjection {
			scope := cloneAnalysisScope(binding.scope)
			return &scope
		}(),
		Caveat:         "Repository-overlay static source evidence only; a complete generation is not runtime use, completeness, migration completion, or decommissioning safety.",
		exactSnapshot:  snapshot,
		exactAuthority: authority,
	}, nil
}

func exactCallerPageSnapshot(binding *exactCallerBinding) string {
	if binding == nil {
		return ""
	}
	return digestJSON(struct {
		Visibility  VisibilityContext                `json:"visibility"`
		Publication callerexecute.PublicationBinding `json:"publication"`
		Declaration ContractCatalogClaim             `json:"declaration"`
		Generation  CallerMapGeneration              `json:"generation"`
		Scope       AnalysisScopeProjection          `json:"scope"`
	}{
		Visibility: binding.visibility, Publication: binding.publication,
		Declaration: binding.declaration, Generation: binding.generation,
		Scope: binding.scope,
	})
}

func (service *exactCallerMapService) gapPage(
	ctx context.Context,
	query CallerMapQuery,
	queryDigest string,
	read *callerexecute.PublicationRead,
	visibility VisibilityContext,
	authorizedRevision uint64,
) (*CallerMapPage, error) {
	generation := service.unavailableGeneration(read)
	publication, err := exactCallerPublicationDescriptor(read)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"describe caller generation gap", err,
		)
	}
	current, err := service.opts.CallerReader.UnavailableCurrent(ctx, read)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"confirm caller generation gap", err,
		)
	}
	if !current {
		return nil, huma.Error409Conflict(
			"caller generation changed while building the response",
		)
	}
	repository, err := service.confirmAuthorization(
		ctx, query.Endpoint.Repository, visibility,
	)
	if err != nil {
		return nil, err
	}
	revision, err := exactCallerRepositoryRevision(repository)
	if err != nil {
		return nil, err
	}
	if revision != authorizedRevision {
		return nil, exactCallerAuthorityConflict()
	}
	return service.exactCallerGapPage(
		query, queryDigest, visibility, revision, generation,
		callerAnalysisScope(repository), false, publication,
	)
}

func (service *exactCallerMapService) projectionFailurePage(
	ctx context.Context,
	query CallerMapQuery,
	queryDigest string,
	read *callerexecute.PublicationRead,
	visibility VisibilityContext,
	authorizedRevision uint64,
) (*CallerMapPage, error) {
	repository, err := service.confirmWithRepository(
		ctx, read, query.Endpoint.Repository, visibility,
	)
	if err != nil {
		return nil, err
	}
	revision, err := exactCallerRepositoryRevision(repository)
	if err != nil {
		return nil, err
	}
	if revision != authorizedRevision {
		return nil, exactCallerAuthorityConflict()
	}
	publication, err := exactCallerRequiredPublicationDescriptor(read)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"describe failed caller generation", err,
		)
	}
	generation := service.generation(read)
	candidateRecords, excludedGoTest, err := exactCallerRecordCounts(read.Publication)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"project failed caller generation counts", err,
		)
	}
	generation.ExcludedGoTestRecords = excludedGoTest
	generation.RecordCounts = &CallerMapRecordCounts{
		CandidateRecords:      candidateRecords,
		BaseRecords:           candidateRecords - excludedGoTest,
		ExcludedGoTestRecords: excludedGoTest,
	}
	generation.State = string(callerexecute.PublicationFailed)
	generation.Reason = "complete caller generation failed semantic projection validation"
	return service.exactCallerGapPage(
		query, queryDigest, visibility, revision, generation,
		callerAnalysisScope(repository), true, publication,
	)
}

func (service *exactCallerMapService) exactCallerGapPage(
	query CallerMapQuery,
	queryDigest string,
	visibility VisibilityContext,
	repositoryRevision uint64,
	generation CallerMapGeneration,
	scope AnalysisScopeProjection,
	projectionFailed bool,
	publication *callerexecute.PublicationDescriptor,
) (*CallerMapPage, error) {
	snapshot := exactCallerGapSnapshot(
		visibility, repositoryRevision, generation, scope, publication,
	)
	authority, err := service.encodeExactAuthority(exactCallerAuthority{
		Schema: exactCallerAuthorityVersion, QueryDigest: queryDigest,
		Repository: generation.Repository, Visibility: visibility,
		RepositoryRevision: repositoryRevision, Snapshot: snapshot,
		MatchingRowsState: "unavailable", Generation: generation,
		ScopeDigest:      analysisScopeDigest(scope),
		ProjectionFailed: projectionFailed, Publication: publication,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"encode caller map gap authority", err,
		)
	}
	return &CallerMapPage{
		SchemaVersion: exactCallerMapSchemaVersion, Query: query,
		Rows: []CallerMapRow{}, Pagination: CallerMapPagination{Complete: true},
		Generation: &generation, MatchingRowsState: "unavailable",
		Scope: func() *AnalysisScopeProjection {
			cloned := cloneAnalysisScope(scope)
			return &cloned
		}(),
		Caveat:         "Caller totals and absence are unavailable until one exact complete repository-overlay generation is current.",
		exactSnapshot:  snapshot,
		exactAuthority: authority,
	}, nil
}

func exactCallerGapSnapshot(
	visibility VisibilityContext,
	repositoryRevision uint64,
	generation CallerMapGeneration,
	scope AnalysisScopeProjection,
	publication *callerexecute.PublicationDescriptor,
) string {
	return digestJSON(struct {
		Visibility         VisibilityContext                    `json:"visibility"`
		RepositoryRevision uint64                               `json:"repository_revision"`
		Generation         CallerMapGeneration                  `json:"generation"`
		Scope              AnalysisScopeProjection              `json:"scope"`
		Publication        *callerexecute.PublicationDescriptor `json:"publication,omitempty"`
	}{visibility, repositoryRevision, generation, scope, publication})
}

func exactCallerPublicationDescriptor(
	read *callerexecute.PublicationRead,
) (*callerexecute.PublicationDescriptor, error) {
	if read == nil || read.Summary == nil {
		return nil, nil
	}
	descriptor, err := read.Descriptor()
	if err != nil {
		return nil, err
	}
	return &descriptor, nil
}

func exactCallerRequiredPublicationDescriptor(
	read *callerexecute.PublicationRead,
) (*callerexecute.PublicationDescriptor, error) {
	descriptor, err := exactCallerPublicationDescriptor(read)
	if err != nil {
		return nil, err
	}
	if descriptor == nil {
		return nil, errors.New("caller publication read has no descriptor")
	}
	return descriptor, nil
}

func (service *CallerMapService) confirmWorkbenchCallerSnapshot(
	ctx context.Context,
	query CallerMapQuery,
	pageSize int,
	encoded string,
) (exactCallerSnapshotConfirmation, error) {
	if service == nil || service.exact == nil {
		return exactCallerSnapshotConfirmation{},
			huma.Error503ServiceUnavailable("caller map unavailable")
	}
	return service.exact.confirmWorkbenchCallerSnapshot(
		ctx, query, pageSize, encoded,
	)
}

func (service *exactCallerMapService) confirmWorkbenchCallerSnapshot(
	ctx context.Context,
	query CallerMapQuery,
	pageSize int,
	encoded string,
) (exactCallerSnapshotConfirmation, error) {
	if service == nil || service.opts.CallerReader == nil {
		return exactCallerSnapshotConfirmation{},
			huma.Error503ServiceUnavailable("caller map unavailable")
	}
	if !catalogAuthenticated(ctx, service.opts) {
		return exactCallerSnapshotConfirmation{},
			huma.Error404NotFound("caller map not found")
	}
	if _, err := validateCallerMapQuery(query); err != nil {
		return exactCallerSnapshotConfirmation{},
			huma.Error400BadRequest(err.Error())
	}
	if pageSize == 0 {
		pageSize = callerMapDefaultPage
	}
	if pageSize < 1 || pageSize > callerMapMaxPage {
		return exactCallerSnapshotConfirmation{},
			huma.Error400BadRequest(
				fmt.Sprintf(
					"page_size must be from 1 through %d", callerMapMaxPage,
				),
			)
	}
	query = normalizeCallerMapQuery(query)
	queryDigest := digestJSON(struct {
		Query CallerMapQuery `json:"query"`
		Size  int            `json:"page_size"`
	}{query, pageSize})

	repository, visibility, err := service.authorize(
		ctx, query.Endpoint.Repository,
	)
	if err != nil {
		return exactCallerSnapshotConfirmation{}, err
	}
	repositoryRevision, err := exactCallerRepositoryRevision(repository)
	if err != nil {
		return exactCallerSnapshotConfirmation{}, err
	}
	var authority exactCallerAuthority
	if len(encoded) > callerMapCursorLimit ||
		service.decodeSigned(encoded, &authority) != nil ||
		!validExactCallerAuthority(authority) ||
		authority.QueryDigest != queryDigest ||
		authority.Repository != query.Endpoint.Repository ||
		authority.Visibility != visibility ||
		authority.RepositoryRevision != repositoryRevision {
		return exactCallerSnapshotConfirmation{},
			exactCallerAuthorityConflict()
	}
	finishExactRead, err := service.beginExactRead(ctx)
	if err != nil {
		return exactCallerSnapshotConfirmation{}, err
	}
	defer finishExactRead()

	if authority.MatchingRowsState == "exact" {
		return service.confirmWorkbenchCurrentCallerSnapshot(
			ctx, query, authority,
		)
	}
	return service.confirmWorkbenchCallerGapSnapshot(
		ctx, query, authority,
	)
}

func validExactCallerAuthority(authority exactCallerAuthority) bool {
	if authority.Schema != exactCallerAuthorityVersion ||
		authority.QueryDigest == "" || authority.Repository == "" ||
		authority.Snapshot == "" ||
		authority.Generation.Repository != authority.Repository ||
		authority.Generation.Plane != exactCallerMapPlane ||
		!validAnalysisScopeDigest(authority.ScopeDigest) ||
		!validCallerPartitionProgress(authority.Generation.PartitionProgress) ||
		!validCallerRecordCounts(authority.Generation) {
		return false
	}
	switch authority.MatchingRowsState {
	case "exact":
		return !authority.ProjectionFailed && authority.Publication != nil &&
			authority.Generation.RecordCounts != nil &&
			authority.Generation.State == string(callerexecute.PublicationCurrent) &&
			validExactCallerPublicationDescriptor(
				authority.Publication, authority.Repository,
				authority.Generation,
			) &&
			authority.Publication.PublicationRevision ==
				authority.RepositoryRevision &&
			authority.Generation.PublicationRevision ==
				authority.RepositoryRevision
	case "unavailable":
		if authority.ProjectionFailed {
			return authority.Publication != nil &&
				authority.Generation.RecordCounts != nil &&
				authority.Generation.State == string(callerexecute.PublicationFailed) &&
				validExactCallerPublicationDescriptor(
					authority.Publication, authority.Repository,
					authority.Generation,
				) &&
				authority.Publication.PublicationRevision ==
					authority.RepositoryRevision &&
				authority.Generation.PublicationRevision ==
					authority.RepositoryRevision
		}
		return stringIn(
			authority.Generation.State,
			string(callerexecute.PublicationMissing),
			string(callerexecute.PublicationStale),
			string(callerexecute.PublicationFailed),
		) && validExactCallerPublicationDescriptor(
			authority.Publication, authority.Repository,
			authority.Generation,
		)
	default:
		return false
	}
}

func validExactCallerPublicationDescriptor(
	descriptor *callerexecute.PublicationDescriptor,
	repository string,
	generation CallerMapGeneration,
) bool {
	if descriptor == nil {
		return generation.PublicationRevision == 0 &&
			generation.ManifestDigest == "" && generation.PairSetDigest == ""
	}
	return descriptor.Validate() == nil &&
		descriptor.Repository == repository &&
		descriptor.GenerationDigest == generation.GenerationDigest &&
		descriptor.ManifestDigest == generation.ManifestDigest &&
		descriptor.PairSetDigest == generation.PairSetDigest &&
		descriptor.PublicationRevision == generation.PublicationRevision
}

func exactCallerAuthorityConflict() error {
	return huma.Error409Conflict(
		"caller map authority is no longer valid",
	)
}

func (service *exactCallerMapService) confirmWorkbenchCurrentCallerSnapshot(
	ctx context.Context,
	query CallerMapQuery,
	authority exactCallerAuthority,
) (exactCallerSnapshotConfirmation, error) {
	read, err := service.opts.CallerReader.ReopenDescriptor(
		ctx, *authority.Publication,
	)
	if err != nil {
		return exactCallerSnapshotConfirmation{},
			exactCallerReaderError("reopen caller generation authority", err)
	}
	if read == nil {
		return exactCallerSnapshotConfirmation{},
			huma.Error500InternalServerError(
				"reopen caller generation authority",
				errors.New("caller reader returned no state"),
			)
	}
	defer func() { _ = read.Release() }()
	if read.Availability != callerexecute.PublicationCurrent {
		return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
	}
	publication, descriptorErr := exactCallerPublicationDescriptor(read)
	if descriptorErr != nil ||
		!reflect.DeepEqual(publication, authority.Publication) {
		return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
	}
	generation := service.generation(read)
	generation.RecordCounts = cloneCallerMapRecordCounts(
		authority.Generation.RecordCounts,
	)
	generation.ExcludedGoTestRecords = authority.Generation.ExcludedGoTestRecords
	if !reflect.DeepEqual(generation, authority.Generation) {
		return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
	}
	repository, err := service.confirmWithRepository(
		ctx, read, query.Endpoint.Repository, authority.Visibility,
	)
	if err != nil {
		return exactCallerSnapshotConfirmation{}, err
	}
	revision, err := exactCallerRepositoryRevision(repository)
	if err != nil {
		return exactCallerSnapshotConfirmation{}, err
	}
	if revision != authority.RepositoryRevision {
		return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
	}
	scope := callerAnalysisScope(repository)
	if analysisScopeDigest(scope) != authority.ScopeDigest {
		return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
	}
	return exactCallerSnapshotConfirmation{
		Snapshot: authority.Snapshot, MatchingRowsState: "exact",
		Generation: generation, Scope: cloneAnalysisScope(scope),
	}, nil
}

func (service *exactCallerMapService) confirmWorkbenchCallerGapSnapshot(
	ctx context.Context,
	query CallerMapQuery,
	authority exactCallerAuthority,
) (exactCallerSnapshotConfirmation, error) {
	var read *callerexecute.PublicationRead
	var err error
	if authority.ProjectionFailed {
		read, err = service.opts.CallerReader.ReopenDescriptor(
			ctx, *authority.Publication,
		)
	} else {
		read, err = service.opts.CallerReader.Open(ctx, authority.Repository)
	}
	if err != nil {
		return exactCallerSnapshotConfirmation{},
			exactCallerReaderError("reopen caller generation gap authority", err)
	}
	if read == nil {
		return exactCallerSnapshotConfirmation{},
			huma.Error500InternalServerError(
				"reopen caller generation gap authority",
				errors.New("caller reader returned no state"),
			)
	}
	defer func() { _ = read.Release() }()
	publication, descriptorErr := exactCallerPublicationDescriptor(read)
	if descriptorErr != nil ||
		!reflect.DeepEqual(publication, authority.Publication) {
		return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
	}

	var generation CallerMapGeneration
	if authority.ProjectionFailed {
		if read.Availability != callerexecute.PublicationCurrent {
			return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
		}
		generation = service.generation(read)
		generation.RecordCounts = cloneCallerMapRecordCounts(
			authority.Generation.RecordCounts,
		)
		generation.ExcludedGoTestRecords =
			authority.Generation.ExcludedGoTestRecords
		generation.State = string(callerexecute.PublicationFailed)
		generation.Reason =
			"complete caller generation failed semantic projection validation"
	} else {
		if read.Availability == callerexecute.PublicationCurrent {
			return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
		}
		generation = service.unavailableGeneration(read)
	}
	if !reflect.DeepEqual(generation, authority.Generation) {
		return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
	}

	var current bool
	if authority.ProjectionFailed {
		current, err = service.opts.CallerReader.Current(ctx, read)
	} else {
		current, err = service.opts.CallerReader.UnavailableCurrent(ctx, read)
	}
	if err != nil {
		return exactCallerSnapshotConfirmation{},
			huma.Error500InternalServerError(
				"confirm caller generation gap authority", err,
			)
	}
	if !current {
		return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
	}
	repository, err := service.confirmAuthorization(
		ctx, query.Endpoint.Repository, authority.Visibility,
	)
	if err != nil {
		return exactCallerSnapshotConfirmation{}, err
	}
	revision, err := exactCallerRepositoryRevision(repository)
	if err != nil {
		return exactCallerSnapshotConfirmation{}, err
	}
	scope := callerAnalysisScope(repository)
	if revision != authority.RepositoryRevision ||
		analysisScopeDigest(scope) != authority.ScopeDigest ||
		exactCallerGapSnapshot(
			authority.Visibility, revision, generation, scope,
			authority.Publication,
		) != authority.Snapshot {
		return exactCallerSnapshotConfirmation{}, exactCallerAuthorityConflict()
	}
	return exactCallerSnapshotConfirmation{
		Snapshot: authority.Snapshot, MatchingRowsState: "unavailable",
		Generation: generation, Scope: cloneAnalysisScope(scope),
	}, nil
}

func (service *exactCallerMapService) index(
	ctx context.Context,
	read *callerexecute.PublicationRead,
) (*exactCallerIndex, error) {
	key := exactIndexKey(read)
	if cached := service.acquireIndex(key); cached != nil {
		return service.indexResult(cached)
	}
	// A cold scan materializes the generation's bounded reverse index once.
	// Serialize construction across repositories so concurrent cold requests
	// cannot multiply the 128 MiB accounted identity budget. Waiting honors the
	// request context; the cache is rechecked after admission to the build slot.
	select {
	case service.indexBuild <- struct{}{}:
		defer func() { <-service.indexBuild }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if cached := service.acquireIndex(key); cached != nil {
		return service.indexResult(cached)
	}
	index := &exactCallerIndex{
		key: key, endpoints: make(map[exactCallerEndpointKey]exactCallerEndpointIndex),
	}
	var projectionErr error
	var limitErr error
	err := read.Lease().ScanRecords(ctx, func(
		pair callerpublication.PairReceipt,
		reference callerpublication.RecordReference,
		record callerleaf.Record,
	) error {
		// Once one semantic defect is known, finish the generic descriptor and
		// receipt validation without retaining more API projection state. A
		// physical mutation or I/O failure discovered later takes precedence.
		if projectionErr != nil || limitErr != nil {
			return nil
		}
		projected, err := projectExactCallerRecord(
			callerReadGeneration(read), pair, reference, record,
		)
		if err != nil {
			projectionErr = err
			return nil
		}
		if projected.operation == "" {
			return nil
		}
		recordIdentityBytes := exactCallerRecordIdentityBytes(projected)
		if len(index.records) >= exactCallerMapMaxRecords ||
			!exactCallerIdentityAdmits(index.identityBytes, recordIdentityBytes) {
			limitErr = callerleaf.ErrLimit
			return nil
		}
		index.identityBytes += recordIdentityBytes
		index.records = append(index.records, projected)
		return nil
	})
	err = exactCallerIndexScanError(err, projectionErr, limitErr)
	if err != nil {
		switch {
		case errors.Is(err, callerleaf.ErrLimit):
			index.failure = exactCallerIndexLimitFailure
		case errors.Is(err, errExactCallerProjectionInvalid):
			index.failure = exactCallerIndexSemanticFailure
		case errors.Is(err, callerleaf.ErrInvalidArtifact):
			return nil, huma.Error409Conflict("caller generation changed while indexing")
		default:
			return nil, huma.Error500InternalServerError("index caller generation", err)
		}
		// Stable, query-independent refusals are tiny negative entries under the
		// same exact key and eight-slot eviction bound as usable indexes. Retry
		// and no-op requests therefore never rehash 128-512 MiB indefinitely.
		index.records = nil
		index.endpoints = nil
		index.identityBytes = 0
		if retainErr := service.retainIndex(index); retainErr != nil {
			return nil, retainErr
		}
		service.releaseIndex(index)
		return nil, exactCallerIndexFailureError(index.failure)
	}
	for position := range index.records {
		record := index.records[position]
		key := exactEndpointKey(record.protocol, record.operation)
		endpoint := index.endpoints[key]
		endpoint.source = append(endpoint.source, position)
		endpoint.unit = append(endpoint.unit, position)
		index.endpoints[key] = endpoint
	}
	for key, endpoint := range index.endpoints {
		sort.Slice(endpoint.source, func(i, j int) bool {
			return exactCallerRecordLess(index.records[endpoint.source[i]], index.records[endpoint.source[j]], false)
		})
		sort.Slice(endpoint.unit, func(i, j int) bool {
			return exactCallerRecordLess(index.records[endpoint.unit[i]], index.records[endpoint.unit[j]], true)
		})
		index.endpoints[key] = endpoint
	}
	if err := service.retainIndex(index); err != nil {
		return nil, err
	}
	return index, nil
}

func exactCallerIndexScanError(scanErr, projectionErr, limitErr error) error {
	if scanErr != nil {
		return scanErr
	}
	if projectionErr != nil {
		return projectionErr
	}
	return limitErr
}

func (service *exactCallerMapService) indexResult(
	index *exactCallerIndex,
) (*exactCallerIndex, error) {
	if index == nil || index.failure == exactCallerIndexUsable {
		return index, nil
	}
	service.releaseIndex(index)
	return nil, exactCallerIndexFailureError(index.failure)
}

func exactCallerIndexFailureError(failure exactCallerIndexFailure) error {
	switch failure {
	case exactCallerIndexSemanticFailure:
		return errExactCallerProjectionInvalid
	case exactCallerIndexLimitFailure:
		return huma.Error422UnprocessableEntity(
			"caller map index exceeds its bounded identity budget",
		)
	default:
		return nil
	}
}

func projectExactCallerRecord(
	generation store.CallerGenerationIdentity,
	pair callerpublication.PairReceipt,
	reference callerpublication.RecordReference,
	record callerleaf.Record,
) (exactCallerRecord, error) {
	if record.Fact == nil {
		return exactCallerRecord{}, nil
	}
	fact := record.Fact
	if fact.Path != record.Path || fact.Atom.StartByte < 0 ||
		fact.Atom.EndByte <= fact.Atom.StartByte || fact.StartLine < 1 ||
		fact.EndLine < fact.StartLine ||
		fact.Atom.EndByte > int(callerleaf.MaxDirectSourceBytes) ||
		fact.EndLine > int(callerleaf.MaxDirectSourceBytes)+1 ||
		fact.Atom.BlobDigest == "" {
		return exactCallerRecord{}, exactCallerProjectionError(
			"caller record source identity is invalid", nil,
		)
	}
	path, start, end, err := parseCallerSubject(fact.Assertion.Subject)
	if err != nil || path != record.Path || start != fact.Atom.StartByte ||
		end != fact.Atom.EndByte {
		return exactCallerRecord{}, exactCallerProjectionError(
			"caller record subject is inconsistent", err,
		)
	}
	var detail callerMapDetail
	if err := decodeCatalogDetail(fact.Assertion.Detail, "go-caller-detail-v1", &detail); err != nil {
		return exactCallerRecord{}, exactCallerProjectionError(
			"caller record detail is invalid", err,
		)
	}
	if err := validateExactCallerUnitCandidates(detail.UnitCandidates); err != nil {
		return exactCallerRecord{}, exactCallerProjectionError(
			"caller record unit candidates are invalid", err,
		)
	}
	if err := validateExactCallerContract(record, detail); err != nil {
		return exactCallerRecord{}, exactCallerProjectionError(
			"caller record semantic contract is invalid", err,
		)
	}
	protocol := detail.Protocol
	if protocol != "grpc" && protocol != "thrift" ||
		detail.Resolution != "direct-syntax" ||
		detail.AttributionDigest != "" {
		return exactCallerRecord{}, exactCallerProjectionError(
			"caller record resolver identity is inconsistent", nil,
		)
	}
	wantedDomain := map[string]string{"grpc": "grpc-caller", "thrift": "thrift-caller"}[protocol]
	if pair.Pair.Domain != wantedDomain || pair.Pair.Digest != reference.PairDigest ||
		pair.Receipt.Name != reference.ArtifactName {
		return exactCallerRecord{}, exactCallerProjectionError(
			"caller record pair identity is inconsistent", nil,
		)
	}
	var directDetail struct {
		ResolverCatalogDigest string `json:"resolver_catalog_digest"`
	}
	if err := json.Unmarshal([]byte(fact.Assertion.Detail), &directDetail); err != nil ||
		directDetail.ResolverCatalogDigest != generation.ResolverManifestDigest {
		return exactCallerRecord{}, exactCallerProjectionError(
			"caller record resolver manifest is inconsistent", err,
		)
	}
	classification := "resolved_caller"
	lineage := fact.Assertion.Lineage
	resolution := "syntax"
	if fact.Assertion.Predicate == "UNRESOLVED_CALLER" {
		classification, lineage, resolution = "extractor_abstention", "", "unresolved"
	}
	recordID := digestJSON(struct {
		Generation string                            `json:"generation"`
		Reference  callerpublication.RecordReference `json:"reference"`
	}{generation.Digest, reference})
	return exactCallerRecord{
		reference: reference, recordID: recordID,
		protocol: protocol, operation: fact.Assertion.Object, lineage: lineage,
		classification: classification, resolution: resolution,
		tier: fact.Assertion.Tier, codeRole: fact.Assertion.CodeRole,
		path: record.Path, objectID: record.ObjectID,
		blobDigest: fact.Atom.BlobDigest,
		startByte:  start, endByte: end,
		startLine: fact.StartLine, endLine: fact.EndLine,
		unitGroup: callerUnitGroup(detail), unitState: detail.UnitState,
		unitReason:       detail.UnitReason,
		unitCandidates:   cloneExactCallerUnitCandidates(detail.UnitCandidates),
		unresolvedReason: detail.UnresolvedReason,
	}, nil
}

func validateExactCallerContract(
	record callerleaf.Record,
	detail callerMapDetail,
) error {
	if record.Fact == nil {
		return errors.New("caller record fact is missing")
	}
	assertion := record.Fact.Assertion
	if err := validateOperation(assertion.Object); err != nil {
		return err
	}
	if !stringIn(assertion.Tier,
		store.TierExact, store.TierDerived, store.TierHeuristic, store.TierUnresolved) {
		return errors.New("caller record confidence tier is invalid")
	}
	if !stringIn(assertion.CodeRole,
		"production", "test", "mock", "generated", "vendor") {
		return errors.New("caller record code role is invalid")
	}
	if !stringIn(detail.UnitState, "resolved", "ambiguous", "unavailable") {
		return errors.New("caller record unit state is invalid")
	}
	for _, value := range []string{detail.UnitReason, detail.UnresolvedReason} {
		if value != "" && !validCatalogFilter(value) {
			return errors.New("caller record reason is invalid")
		}
	}
	switch assertion.Predicate {
	case "CALLS_OPERATION":
		if record.Kind != callerleaf.RecordResult || record.Reason != "" ||
			!validQueryIdentity(assertion.Lineage) ||
			assertion.Tier != store.TierHeuristic || detail.UnresolvedReason != "" {
			return errors.New("caller result record is inconsistent")
		}
	case "UNRESOLVED_CALLER":
		if record.Kind != callerleaf.RecordAbstention ||
			record.Reason != "unresolved_caller" || assertion.Lineage != "" ||
			assertion.Tier != store.TierUnresolved || detail.UnresolvedReason == "" {
			return errors.New("caller abstention record is inconsistent")
		}
	default:
		return errors.New("caller record predicate is invalid")
	}
	return nil
}

func exactCallerProjectionError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", errExactCallerProjectionInvalid, message)
	}
	return fmt.Errorf(
		"%w: %s: %v", errExactCallerProjectionInvalid, message, cause,
	)
}

func exactCallerRecordMatches(record exactCallerRecord, query CallerMapQuery) bool {
	if record.classification == "resolved_caller" && record.lineage != query.Endpoint.Lineage ||
		query.PathPrefix != "" && !strings.HasPrefix(record.path, query.PathPrefix) ||
		query.CodeRole != "" && record.codeRole != query.CodeRole ||
		query.Tier != "" && record.tier != query.Tier ||
		query.Freshness == "stale" ||
		query.Resolution != "any" && query.Resolution != record.resolution {
		return false
	}
	if query.Unit == "" && query.Owner == "" {
		return true
	}
	for _, candidate := range record.unitCandidates {
		if query.Unit != "" && !callerUnitMatches(candidate, query.Unit) ||
			query.Owner != "" && !stringSliceContains(candidate.Owners, query.Owner) {
			continue
		}
		return true
	}
	return false
}

func exactCallerRecordLess(left, right exactCallerRecord, unit bool) bool {
	if unit && left.unitGroup != right.unitGroup {
		return left.unitGroup < right.unitGroup
	}
	if left.path != right.path {
		return left.path < right.path
	}
	if left.startByte != right.startByte {
		return left.startByte < right.startByte
	}
	return left.recordID < right.recordID
}

func exactCallerRow(
	read *callerexecute.PublicationRead,
	record exactCallerRecord,
) CallerMapRow {
	return CallerMapRow{
		Classification: record.classification, Resolution: record.resolution,
		Protocol:  map[string]string{"grpc": "protobuf", "thrift": "thrift"}[record.protocol],
		Operation: record.operation, DeclarationLineage: record.lineage,
		Tier: record.tier, CodeRole: record.codeRole, Fresh: true,
		UnitGroup: record.unitGroup,
		Unit: boundedUnitAttribution(
			record.unitState, record.unitReason, record.unitCandidates,
		),
		Source: CallerMapSource{
			Repository: callerReadGeneration(read).Repository,
			Commit:     callerReadGeneration(read).HeadCommit,
			Path:       record.path, ObjectID: record.objectID,
			BlobDigest: record.blobDigest, Plane: exactCallerMapPlane,
			StartByte: record.startByte, EndByte: record.endByte,
			StartLine: record.startLine, EndLine: record.endLine,
			AssertionID: record.recordID, RunID: callerReadGeneration(read).Digest,
			AtomID: record.blobDigest,
		},
		UnresolvedReason: record.unresolvedReason,
	}
}

func exactCallerDeclaration(
	ctx context.Context,
	source store.EvidenceStore,
	read *callerexecute.PublicationRead,
	pack protocolPack,
	query CallerMapQuery,
) (ContractCatalogClaim, error) {
	if source == nil || read == nil || read.Resolver == nil || read.Summary == nil {
		return ContractCatalogClaim{}, huma.Error409Conflict("caller declaration generation is unavailable")
	}
	runID := ""
	for _, declaration := range read.Resolver.Declarations {
		if declaration.Domain == pack.declarationDomain {
			runID = declaration.RunID
			break
		}
	}
	if runID == "" {
		return ContractCatalogClaim{}, huma.Error404NotFound("caller map endpoint not found")
	}
	rows, err := source.ListAssertions(ctx, store.AssertionQuery{
		Repo: query.Endpoint.Repository, RunID: runID,
		Predicate: "DECLARES_OPERATION",
		Object:    strings.TrimPrefix(query.Endpoint.Operation, "/"),
		Lineage:   query.Endpoint.Lineage, Limit: 1,
	})
	if err != nil {
		return ContractCatalogClaim{}, huma.Error500InternalServerError("read caller declaration", err)
	}
	if len(rows) != 1 {
		return ContractCatalogClaim{}, huma.Error404NotFound("caller map endpoint not found")
	}
	claim, _, err := resolveCatalogClaim(
		ctx, source, rows[0], callerReadGeneration(read).HeadCommit,
		catalogLocatorsPerClaimLimit,
	)
	if err != nil {
		return ContractCatalogClaim{}, huma.Error500InternalServerError("resolve caller declaration", err)
	}
	return claim, nil
}

func (service *exactCallerMapService) authorize(
	ctx context.Context,
	repository string,
) (store.Repo, VisibilityContext, error) {
	allow := repoFilter(ctx, service.opts)
	repo, err := service.opts.Store.GetRepo(ctx, repository)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Repo{}, VisibilityContext{},
			huma.Error500InternalServerError("authorize caller map repository", err)
	}
	if err == nil && repo == nil {
		return store.Repo{}, VisibilityContext{},
			huma.Error500InternalServerError(
				"authorize caller map repository",
				errors.New("repository store returned no record"),
			)
	}
	if err != nil || repo.Deleting ||
		allow != nil && !allow(*repo) {
		return store.Repo{}, VisibilityContext{}, huma.Error404NotFound("caller map endpoint not found")
	}
	return *repo, catalogVisibilityContext(ctx, service.opts, []store.Repo{*repo}), nil
}

func (service *exactCallerMapService) confirm(
	ctx context.Context,
	read *callerexecute.PublicationRead,
	repository string,
	visibility VisibilityContext,
) error {
	_, err := service.confirmWithRepository(
		ctx, read, repository, visibility,
	)
	return err
}

func (service *exactCallerMapService) confirmWithRepository(
	ctx context.Context,
	read *callerexecute.PublicationRead,
	repository string,
	visibility VisibilityContext,
) (store.Repo, error) {
	current, err := service.opts.CallerReader.Current(ctx, read)
	if err != nil {
		return store.Repo{}, huma.Error500InternalServerError(
			"confirm caller generation", err,
		)
	}
	if !current {
		return store.Repo{}, huma.Error409Conflict(
			"caller generation changed while building the response",
		)
	}
	return service.confirmAuthorization(ctx, repository, visibility)
}

func (service *exactCallerMapService) confirmAuthorization(
	ctx context.Context,
	repository string,
	visibility VisibilityContext,
) (store.Repo, error) {
	repo, confirmed, err := service.authorize(ctx, repository)
	if err != nil {
		return store.Repo{}, err
	}
	if confirmed != visibility {
		return store.Repo{}, huma.Error409Conflict("caller map authorization changed while building the response; retry")
	}
	return repo, nil
}

func exactCallerRepositoryRevision(repository store.Repo) (uint64, error) {
	if repository.CallerPublicationRevision < 0 {
		return 0, huma.Error500InternalServerError(
			"confirm caller publication revision",
			errors.New("repository caller publication revision is negative"),
		)
	}
	return uint64(repository.CallerPublicationRevision), nil
}

func (service *exactCallerMapService) generation(
	read *callerexecute.PublicationRead,
) CallerMapGeneration {
	generation := callerReadGeneration(read)
	result := CallerMapGeneration{
		State: string(read.Availability), Plane: exactCallerMapPlane,
		Repository: generation.Repository, Commit: generation.HeadCommit,
		UnitDigest:           generation.UnitDigest,
		GenerationDigest:     generation.Digest,
		DeclarationSetDigest: generation.DeclarationSetDigest,
		CandidateManifest:    generation.CandidateManifestDigest,
		ResolverManifest:     generation.ResolverManifestDigest,
	}
	if read.Summary == nil {
		return result
	}
	result.PairSetDigest = read.Summary.PairSetDigest
	result.ManifestDigest = read.Summary.ManifestDigest
	result.PublicationRevision = read.Summary.PublicationRevision
	result.PairCount = read.Summary.PairCount
	result.ResultCount = read.Summary.ResultCount
	result.AbstentionCount = read.Summary.AbstentionCount
	result.CoverageRecordCount = read.Summary.CoverageRecordCount
	result.CoveredCandidateCount = read.Summary.CoveredCandidateCount
	result.CanonicalBytes = read.Summary.CanonicalBytes
	total := read.Summary.PairCount
	result.PartitionProgress = &CallerMapPartitionProgress{
		State: "complete", SettledPairCount: total,
		SucceededPairCount: total, TotalPairCount: &total,
	}
	return result
}

func exactCallerRecordCounts(
	publication *store.CallerGenerationPublication,
) (int, int, error) {
	if publication == nil {
		return 0, 0, errors.New("caller publication is absent")
	}
	maximum := int(^uint(0) >> 1)
	candidateRecords := 0
	excludedGoTest := 0
	// Candidate-manifest validation assigns exactly one immutable leaf
	// descriptor to each ordinal. Expected caller pairs repeat that descriptor
	// once per enabled domain, so the ordinal is the cross-domain census key.
	leaves := make(map[int]exactCallerLeafCensus)
	for _, pair := range publication.Pairs {
		candidate := pair.Pair.CandidateRecordCount
		excluded := pair.Receipt.ExcludedGoTestRecords
		if candidate < 0 || excluded < 0 || excluded > candidate {
			return 0, 0, errors.New(
				"caller publication candidate counts are inconsistent",
			)
		}
		leaf, found := leaves[pair.Pair.LeafOrdinal]
		if found {
			if !sameExactCallerLeaf(leaf.pair, pair.Pair) ||
				leaf.candidateRecords != candidate ||
				leaf.excludedGoTestRecords != excluded {
				return 0, 0, errors.New(
					"caller publication domain copies disagree on candidate leaf census",
				)
			}
			continue
		}
		if candidate > maximum-candidateRecords ||
			excluded > maximum-excludedGoTest {
			return 0, 0, errors.New(
				"caller publication candidate counts are inconsistent",
			)
		}
		leaves[pair.Pair.LeafOrdinal] = exactCallerLeafCensus{
			pair:                  pair.Pair,
			candidateRecords:      candidate,
			excludedGoTestRecords: excluded,
		}
		candidateRecords += candidate
		excludedGoTest += excluded
	}
	return candidateRecords, excludedGoTest, nil
}

type exactCallerLeafCensus struct {
	pair                  store.CallerLeafPair
	candidateRecords      int
	excludedGoTestRecords int
}

// sameExactCallerLeaf compares the immutable candidate-manifest leaf
// descriptor repeated once for every enabled caller domain. Domain-specific
// adapter identity and pair digest are deliberately excluded.
func sameExactCallerLeaf(left, right store.CallerLeafPair) bool {
	return left.LeafOrdinal == right.LeafOrdinal &&
		left.LeafPrefix == right.LeafPrefix &&
		left.LeafPrefixBits == right.LeafPrefixBits &&
		left.CandidateMemberName == right.CandidateMemberName &&
		left.CandidateDeclaredBytes == right.CandidateDeclaredBytes &&
		left.CandidateContentBytes == right.CandidateContentBytes &&
		left.CandidateContentDigest == right.CandidateContentDigest &&
		left.LeafAdapterVersion == right.LeafAdapterVersion
}

func (service *exactCallerMapService) unavailableGeneration(
	read *callerexecute.PublicationRead,
) CallerMapGeneration {
	generation := service.generation(read)
	if generation.State == "" && read != nil {
		generation.State = string(read.Availability)
	}
	if read != nil {
		generation.Reason = "complete caller generation " +
			string(read.Availability)
	}
	generation.PartitionProgress = unavailableCallerPartitionProgress(read)
	return generation
}

func unavailableCallerPartitionProgress(
	read *callerexecute.PublicationRead,
) *CallerMapPartitionProgress {
	result := &CallerMapPartitionProgress{State: "unavailable"}
	if read == nil || read.Progress == nil {
		return result
	}
	progress := *read.Progress
	if progress.SettledCount < 0 || progress.SucceededCount < 0 ||
		progress.RefusedCount < 0 ||
		progress.SucceededCount+progress.RefusedCount != progress.SettledCount {
		return result
	}
	result.SettledPairCount = progress.SettledCount
	result.SucceededPairCount = progress.SucceededCount
	result.RefusedPairCount = progress.RefusedCount
	if read.Admission != nil && read.Admission.PairCount >= progress.SettledCount {
		result.Refusals = make([]CallerMapRefusalSummary, len(read.Admission.Refusals))
		for index, summary := range read.Admission.Refusals {
			result.Refusals[index] = CallerMapRefusalSummary{
				Stage:          summary.Refusal.Stage,
				GenerationKind: summary.Refusal.GenerationKind,
				Classification: summary.Refusal.Classification,
				Dimension:      summary.Refusal.Dimension,
				Observed:       summary.Refusal.Observed,
				Limit:          summary.Refusal.Limit,
				OutcomeCount:   summary.OutcomeCount,
			}
		}
		total := read.Admission.PairCount
		result.TotalPairCount = &total
		if progress.SettledCount == total {
			result.State = "complete"
		} else {
			result.State = "partial"
		}
	} else if progress.SettledCount > 0 {
		result.State = "partial"
	}
	return result
}

func exactIndexKey(read *callerexecute.PublicationRead) string {
	return digestJSON(struct {
		Generation  string `json:"generation"`
		Manifest    string `json:"manifest"`
		PairSet     string `json:"pair_set"`
		Revision    uint64 `json:"revision"`
		Incarnation string `json:"incarnation"`
	}{
		read.Summary.Generation.Digest, read.Summary.ManifestDigest,
		read.Summary.PairSetDigest, read.Summary.PublicationRevision,
		read.Summary.PublicationIncarnation,
	})
}

func exactEndpointKey(protocol, operation string) exactCallerEndpointKey {
	return exactCallerEndpointKey{protocol: protocol, operation: operation}
}

func exactCallerRecordIdentityBytes(record exactCallerRecord) int {
	result := len(record.reference.PairDigest) +
		len(record.reference.ArtifactName) +
		len(record.reference.Record.Digest) +
		len(record.recordID) + len(record.protocol) + len(record.operation) +
		len(record.lineage) + len(record.classification) + len(record.resolution) +
		len(record.tier) + len(record.codeRole) + len(record.path) +
		len(record.objectID) + len(record.blobDigest) + len(record.unitGroup) +
		len(record.unitState) + len(record.unitReason) + len(record.unresolvedReason)
	result += len(record.unitCandidates) * exactCallerUnitCandidateStructuralBytes
	for _, candidate := range record.unitCandidates {
		result += len(candidate.ID)
		for _, values := range [][]string{
			candidate.BuildTargets, candidate.Deployables,
			candidate.LogicalServices, candidate.Owners,
		} {
			for _, value := range values {
				result += len(value) + exactCallerUnitValueStructuralBytes
			}
		}
	}
	return result
}

func exactCallerIdentityAdmits(current, addition int) bool {
	return current >= 0 && addition >= 0 && current <= exactCallerMapMaxIdentityBytes &&
		addition <= exactCallerMapMaxIdentityBytes-current
}

func validateExactCallerUnitCandidates(candidates []CallerMapUnitCandidate) error {
	if len(candidates) > exactCallerUnitCandidateLimit {
		return fmt.Errorf("more than %d unit candidates", exactCallerUnitCandidateLimit)
	}
	previousID := ""
	for index, candidate := range candidates {
		if len(candidate.ID) != len("au_")+64 ||
			!strings.HasPrefix(candidate.ID, "au_") ||
			!exactCallerLowerHex(candidate.ID[len("au_"):]) {
			return fmt.Errorf("candidate %d has a noncanonical id", index)
		}
		if previousID != "" && previousID >= candidate.ID {
			return fmt.Errorf("candidate %d is unordered or duplicated", index)
		}
		previousID = candidate.ID
		for _, values := range [][]string{
			candidate.BuildTargets, candidate.Deployables,
			candidate.LogicalServices, candidate.Owners,
		} {
			if len(values) > exactCallerUnitValueLimit {
				return fmt.Errorf("candidate %d has too many attribution values", index)
			}
			previous := ""
			for valueIndex, value := range values {
				if value == "" || len(value) > exactCallerUnitValueBytes ||
					!utf8.ValidString(value) {
					return fmt.Errorf("candidate %d has an invalid attribution value", index)
				}
				for _, current := range value {
					if current < 0x20 || current == 0x7f {
						return fmt.Errorf("candidate %d attribution value contains a control character", index)
					}
				}
				if valueIndex > 0 && previous >= value {
					return fmt.Errorf("candidate %d attribution values are unordered or duplicated", index)
				}
				previous = value
			}
		}
	}
	return nil
}

func cloneExactCallerUnitCandidates(
	candidates []CallerMapUnitCandidate,
) []CallerMapUnitCandidate {
	cloned := make([]CallerMapUnitCandidate, len(candidates))
	for index, candidate := range candidates {
		cloned[index] = candidate
		cloned[index].BuildTargets = cloneExactCallerStrings(candidate.BuildTargets)
		cloned[index].Deployables = cloneExactCallerStrings(candidate.Deployables)
		cloned[index].LogicalServices = cloneExactCallerStrings(candidate.LogicalServices)
		cloned[index].Owners = cloneExactCallerStrings(candidate.Owners)
	}
	return cloned
}

func cloneExactCallerStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func exactCallerLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, current := range value {
		if current < '0' || current > '9' && current < 'a' || current > 'f' {
			return false
		}
	}
	return true
}

func sameExactCallerRecord(left, right exactCallerRecord) bool {
	left.reference = callerpublication.RecordReference{}
	right.reference = callerpublication.RecordReference{}
	return reflect.DeepEqual(left, right)
}

func exactCallerBindingIndexKeys(binding *exactCallerBinding) []string {
	if binding == nil {
		return nil
	}
	if binding.comparison == nil {
		if binding.indexKey == "" {
			return nil
		}
		return []string{binding.indexKey}
	}
	oldKey := binding.comparison.old.indexKey
	replacementKey := binding.comparison.replacement.indexKey
	switch {
	case oldKey == "" && replacementKey == "":
		return nil
	case oldKey == replacementKey || replacementKey == "":
		return []string{oldKey}
	case oldKey == "":
		return []string{replacementKey}
	default:
		return []string{oldKey, replacementKey}
	}
}

func exactCallerBindingPinsIndex(binding *exactCallerBinding, key string) bool {
	for _, candidate := range exactCallerBindingIndexKeys(binding) {
		if candidate == key {
			return true
		}
	}
	return false
}

func (service *exactCallerMapService) acquireIndex(key string) *exactCallerIndex {
	service.mu.Lock()
	defer service.mu.Unlock()
	index := service.indexes[key]
	if index != nil {
		index.uses++
	}
	return index
}

func (service *exactCallerMapService) releaseIndex(index *exactCallerIndex) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if index != nil && index.uses > 0 {
		index.uses--
	}
}

func (service *exactCallerMapService) retainIndex(index *exactCallerIndex) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	// Binding expiry must release index pins even when the next request is a
	// fresh first page rather than a continuation or another binding. Without
	// this sweep, eight abandoned cursors could keep cache admission wedged
	// after their documented lifetime.
	service.expireBindingsLocked(time.Now())
	if prior := service.indexes[index.key]; prior != nil {
		return nil
	}
	for len(service.indexOrder) >= exactCallerMapMaxIndexes {
		victimPosition, bindingVictims := service.indexAdmissionVictimLocked()
		if victimPosition < 0 {
			return huma.Error503ServiceUnavailable(
				"caller map index capacity is busy; retry",
			)
		}
		for _, binding := range bindingVictims {
			service.retireBindingLocked(binding)
		}
		victim := service.indexOrder[victimPosition]
		service.indexOrder = append(
			service.indexOrder[:victimPosition],
			service.indexOrder[victimPosition+1:]...,
		)
		delete(service.indexes, victim)
	}
	service.indexes[index.key] = index
	index.uses = 1
	service.indexOrder = append(service.indexOrder, index.key)
	return nil
}

func (service *exactCallerMapService) indexAdmissionVictimLocked() (
	int,
	[]*exactCallerBinding,
) {
	// Prefer an already unbound index so pressure never invalidates a token
	// needlessly.
	for position, candidate := range service.indexOrder {
		if retained := service.indexes[candidate]; retained != nil &&
			retained.bindings == 0 && retained.uses == 0 {
			return position, nil
		}
	}
	victimPosition := -1
	var victimBindings []*exactCallerBinding
	for position, candidate := range service.indexOrder {
		retained := service.indexes[candidate]
		if retained == nil || retained.uses != 0 || retained.bindings == 0 {
			continue
		}
		bindings := make([]*exactCallerBinding, 0, retained.bindings)
		for _, binding := range service.bindings {
			if binding != nil && exactCallerBindingPinsIndex(binding, candidate) &&
				binding.uses == 0 && !binding.retired && !binding.finalized {
				bindings = append(bindings, binding)
			}
		}
		// A missing pin is retired in flight; a visible non-idle pin is active.
		// Neither is reclaimable until its request releases it.
		if len(bindings) != retained.bindings {
			continue
		}
		if victimPosition < 0 || len(bindings) < len(victimBindings) {
			victimPosition, victimBindings = position, bindings
		}
	}
	return victimPosition, victimBindings
}

func (service *exactCallerMapService) retainBinding(binding *exactCallerBinding) error {
	return service.retainBindingWithUse(binding, false)
}

func (service *exactCallerMapService) retainActiveBinding(
	binding *exactCallerBinding,
) error {
	return service.retainBindingWithUse(binding, true)
}

func (service *exactCallerMapService) retainBindingWithUse(
	binding *exactCallerBinding,
	active bool,
) error {
	// Own an exact-capacity position slice before retaining the binding. A
	// selective query may have been filtered through a much larger endpoint
	// slice; retaining that backing allocation would exceed the documented
	// aggregate-position bound even though len(records) remained within it.
	positions := make([]int, len(binding.records))
	copy(positions, binding.records)
	binding.records = positions

	service.mu.Lock()
	defer service.mu.Unlock()
	service.expireBindingsLocked(time.Now())
	indexKeys := exactCallerBindingIndexKeys(binding)
	if len(indexKeys) == 0 {
		return huma.Error409Conflict("caller map index snapshot expired")
	}
	for _, indexKey := range indexKeys {
		if service.indexes[indexKey] == nil {
			return huma.Error409Conflict("caller map index snapshot expired")
		}
	}
	victims, ok := service.bindingAdmissionVictimsLocked(len(binding.records))
	if !ok {
		return huma.Error503ServiceUnavailable(
			"caller map request binding capacity is busy; retry",
		)
	}
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return huma.Error500InternalServerError("allocate caller map cursor", err)
	}
	binding.id = base64.RawURLEncoding.EncodeToString(raw)
	// Complete the admission only after both capacity and ID allocation are
	// known to succeed. Pressure may retire the oldest idle tokens, but never an
	// active request or a retired binding whose positions remain in use.
	for _, victim := range victims {
		service.retireBindingLocked(victim)
	}
	service.bindings[binding.id] = binding
	service.bindingCount++
	service.bindingRefs += len(binding.records)
	for _, indexKey := range indexKeys {
		service.indexes[indexKey].bindings++
	}
	if active {
		binding.uses++
	}
	return nil
}

func (service *exactCallerMapService) bindingAdmissionVictimsLocked(
	incomingRefs int,
) ([]*exactCallerBinding, bool) {
	if incomingRefs > exactCallerMapBindingRefs {
		return nil, false
	}
	candidates := make([]*exactCallerBinding, 0, len(service.bindings))
	for _, candidate := range service.bindings {
		if candidate != nil && candidate.uses == 0 &&
			!candidate.retired && !candidate.finalized {
			candidates = append(candidates, candidate)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].createdAt.Equal(candidates[j].createdAt) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].createdAt.Before(candidates[j].createdAt)
	})
	retainedCount, retainedRefs := service.bindingCount, service.bindingRefs
	victims := make([]*exactCallerBinding, 0, len(candidates))
	for retainedCount >= exactCallerMapMaxBindings ||
		incomingRefs > exactCallerMapBindingRefs-retainedRefs {
		if len(victims) == len(candidates) {
			return nil, false
		}
		victim := candidates[len(victims)]
		victims = append(victims, victim)
		retainedCount--
		retainedRefs -= len(victim.records)
	}
	return victims, true
}

func (service *exactCallerMapService) acquireBinding(id string) *exactCallerBinding {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.expireBindingsLocked(time.Now())
	binding := service.bindings[id]
	if binding == nil || binding.retired || binding.finalized {
		return nil
	}
	// A live user keeps the position slice accounted and the reverse index
	// pinned even when wall-clock expiry or an error retires the map entry.
	binding.uses++
	return binding
}

func (service *exactCallerMapService) releaseBinding(binding *exactCallerBinding) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if binding == nil || binding.finalized || binding.uses < 1 {
		return
	}
	binding.uses--
	if binding.retired && binding.uses == 0 {
		service.finalizeBindingLocked(binding)
	}
}

func (service *exactCallerMapService) dropBinding(id string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if binding := service.bindings[id]; binding != nil {
		service.retireBindingLocked(binding)
	}
}

func (service *exactCallerMapService) expireBindingsLocked(now time.Time) {
	for _, binding := range service.bindings {
		if now.Sub(binding.createdAt) > exactCallerMapBindingLifetime {
			service.retireBindingLocked(binding)
		}
	}
}

func (service *exactCallerMapService) retireBindingLocked(
	binding *exactCallerBinding,
) {
	if binding == nil || binding.retired || binding.finalized {
		return
	}
	binding.retired = true
	if service.bindings[binding.id] == binding {
		delete(service.bindings, binding.id)
	}
	if binding.uses == 0 {
		service.finalizeBindingLocked(binding)
	}
}

func (service *exactCallerMapService) finalizeBindingLocked(
	binding *exactCallerBinding,
) {
	if binding == nil || binding.finalized {
		return
	}
	binding.finalized = true
	service.bindingRefs -= len(binding.records)
	if service.bindingCount > 0 {
		service.bindingCount--
	}
	for _, indexKey := range exactCallerBindingIndexKeys(binding) {
		if index := service.indexes[indexKey]; index != nil && index.bindings > 0 {
			index.bindings--
		}
	}
}

func (service *exactCallerMapService) encodeSigned(value any) string {
	raw, _ := json.Marshal(value)
	signature := hmac.New(sha256.New, service.secret[:])
	_, _ = signature.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." +
		base64.RawURLEncoding.EncodeToString(signature.Sum(nil))
}

func (service *exactCallerMapService) encodeExactAuthority(
	value any,
) (string, error) {
	encoded := service.encodeSigned(value)
	if encoded == "" || len(encoded) > callerMapCursorLimit {
		return "", errors.New("exact caller authority exceeds its bound")
	}
	return encoded, nil
}

func (service *exactCallerMapService) decodeCursor(encoded string) (exactCallerCursor, error) {
	var cursor exactCallerCursor
	if len(encoded) > callerMapCursorLimit {
		return cursor, huma.Error400BadRequest("caller map cursor is invalid")
	}
	if err := service.decodeSigned(encoded, &cursor); err != nil ||
		cursor.Schema != exactCallerMapCursorVersion {
		return exactCallerCursor{}, huma.Error400BadRequest("caller map cursor is invalid")
	}
	return cursor, nil
}

func (service *exactCallerMapService) decodeSigned(encoded string, destination any) error {
	payload, signature, ok := strings.Cut(encoded, ".")
	if !ok {
		return errors.New("signed value has no authentication")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != payload ||
		len(raw) > callerMapCursorLimit || !json.Valid(raw) {
		return errors.New("signed value is invalid")
	}
	want, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || base64.RawURLEncoding.EncodeToString(want) != signature {
		return errors.New("signed value authentication is invalid")
	}
	mac := hmac.New(sha256.New, service.secret[:])
	_, _ = mac.Write(raw)
	if !hmac.Equal(want, mac.Sum(nil)) {
		return errors.New("signed value authentication differs")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return nil
}

func (service *exactCallerMapService) encodeCitation(
	binding *exactCallerBinding,
	position int,
	record exactCallerRecord,
) (string, error) {
	return service.encodeCitationFor(binding, "", position, record)
}

func (service *exactCallerMapService) encodeComparisonCitation(
	binding *exactCallerBinding,
	side string,
	position int,
	record exactCallerRecord,
) (string, error) {
	if side != "old" && side != "replacement" {
		return "", errors.New("caller comparison citation side is invalid")
	}
	return service.encodeCitationFor(binding, side, position, record)
}

func (service *exactCallerMapService) encodeCitationFor(
	binding *exactCallerBinding,
	side string,
	position int,
	record exactCallerRecord,
) (string, error) {
	if binding == nil || binding.id == "" || position < 0 || record.recordID == "" {
		return "", errors.New("caller citation binding is invalid")
	}
	repository := binding.generation.Repository
	if binding.comparison != nil {
		source, ok := exactCallerComparisonSourceFor(binding, side)
		if !ok {
			return "", errors.New("caller comparison citation binding is invalid")
		}
		repository = source.generation.Repository
	} else if side != "" {
		return "", errors.New("caller citation binding side is invalid")
	}
	citation := exactCallerCitation{
		Schema:     exactCallerCitationVersion,
		Repository: repository, Binding: binding.id,
		Position: position, RecordID: record.recordID, Side: side,
	}
	encoded := service.encodeSigned(citation)
	if len(encoded) > callerMapCursorLimit {
		return "", errors.New("caller citation exceeds its bounded envelope")
	}
	return encoded, nil
}

func exactCallerComparisonSourceFor(
	binding *exactCallerBinding,
	side string,
) (exactCallerComparisonSource, bool) {
	if binding == nil || binding.comparison == nil {
		return exactCallerComparisonSource{}, false
	}
	switch side {
	case "old":
		return binding.comparison.old, true
	case "replacement":
		return binding.comparison.replacement, true
	default:
		return exactCallerComparisonSource{}, false
	}
}

func exactCallerCitationSource(
	binding *exactCallerBinding,
	side string,
) (
	VisibilityContext,
	string,
	callerexecute.PublicationBinding,
	CallerMapGeneration,
	bool,
) {
	if binding == nil {
		return VisibilityContext{}, "", callerexecute.PublicationBinding{},
			CallerMapGeneration{}, false
	}
	if binding.comparison == nil {
		if side != "" {
			return VisibilityContext{}, "", callerexecute.PublicationBinding{},
				CallerMapGeneration{}, false
		}
		return binding.visibility, binding.indexKey, binding.publication,
			binding.generation, true
	}
	source, ok := exactCallerComparisonSourceFor(binding, side)
	if !ok {
		return VisibilityContext{}, "", callerexecute.PublicationBinding{},
			CallerMapGeneration{}, false
	}
	return source.visibility, source.indexKey, source.publication,
		source.generation, true
}

func (service *exactCallerMapService) readCitation(
	ctx context.Context,
	encoded string,
) (*CallerMapCitation, error) {
	if service == nil || !catalogAuthenticated(ctx, service.opts) {
		return nil, huma.Error404NotFound("caller citation not found")
	}
	if len(encoded) > callerMapCursorLimit {
		return nil, huma.Error400BadRequest("caller citation is invalid")
	}
	var citation exactCallerCitation
	if err := service.decodeSigned(encoded, &citation); err != nil ||
		citation.Schema != exactCallerCitationVersion || citation.Repository == "" ||
		citation.Binding == "" ||
		citation.Position < 0 || citation.RecordID == "" {
		return nil, huma.Error400BadRequest("caller citation is invalid")
	}
	_, visibility, err := service.authorize(
		ctx, citation.Repository,
	)
	if err != nil {
		return nil, err
	}
	finishExactRead, err := service.beginExactRead(ctx)
	if err != nil {
		return nil, err
	}
	defer finishExactRead()
	binding := service.acquireBinding(citation.Binding)
	if binding == nil {
		return nil, huma.Error409Conflict("caller citation snapshot expired")
	}
	defer service.releaseBinding(binding)
	boundVisibility, indexKey, publication, generation, ok :=
		exactCallerCitationSource(binding, citation.Side)
	if !ok || generation.Repository != citation.Repository {
		return nil, huma.Error400BadRequest("caller citation is invalid")
	}
	if visibility != boundVisibility {
		return nil, huma.Error404NotFound("caller citation not found")
	}
	index := service.acquireIndex(indexKey)
	if index == nil {
		service.dropBinding(binding.id)
		return nil, huma.Error409Conflict("caller citation snapshot expired")
	}
	defer service.releaseIndex(index)
	if citation.Position >= len(index.records) {
		return nil, huma.Error400BadRequest("caller citation is invalid")
	}
	indexed := index.records[citation.Position]
	if indexed.recordID != citation.RecordID {
		return nil, huma.Error400BadRequest("caller citation is invalid")
	}
	finishCitationRead, err := service.beginCitationRead(ctx)
	if err != nil {
		return nil, err
	}
	defer finishCitationRead()
	read, err := service.opts.CallerReader.Reopen(ctx, publication)
	if err != nil {
		return nil, exactCallerReaderError("open caller citation", err)
	}
	if read == nil {
		return nil, huma.Error500InternalServerError(
			"open caller citation", errors.New("caller reader returned no state"),
		)
	}
	defer func() { _ = read.Release() }()
	if read.Availability != callerexecute.PublicationCurrent {
		return nil, huma.Error409Conflict("caller citation generation is no longer current")
	}
	currentGeneration := service.generation(read)
	if currentGeneration.Repository != generation.Repository ||
		currentGeneration.Commit != generation.Commit ||
		currentGeneration.GenerationDigest != generation.GenerationDigest ||
		currentGeneration.ManifestDigest != generation.ManifestDigest ||
		currentGeneration.PairSetDigest != generation.PairSetDigest ||
		currentGeneration.PublicationRevision != generation.PublicationRevision {
		return nil, huma.Error409Conflict("caller citation generation is no longer current")
	}
	pair, record, err := read.Lease().ReadRecord(ctx, indexed.reference)
	if err != nil {
		if errors.Is(err, callerleaf.ErrInvalidArtifact) {
			return nil, huma.Error409Conflict("caller citation record is no longer current")
		}
		return nil, huma.Error500InternalServerError("read caller citation record", err)
	}
	projected, err := projectExactCallerRecord(
		callerReadGeneration(read), pair, indexed.reference, record,
	)
	if err != nil || !sameExactCallerRecord(indexed, projected) {
		return nil, huma.Error400BadRequest("caller citation is invalid")
	}
	repositoryDir, err := exactCallerCitationRepositoryDir(
		service.opts.DataDir, generation.Repository,
	)
	if err != nil {
		return nil, err
	}
	resolved, blobBytes, err := gitobj.ResolveBlob(
		ctx, repositoryDir, generation.Commit+":"+indexed.path,
	)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, huma.Error409Conflict("caller citation immutable object differs")
		}
		return nil, huma.Error500InternalServerError("resolve caller citation object", err)
	}
	if resolved != indexed.objectID {
		return nil, huma.Error409Conflict("caller citation immutable object differs")
	}
	if blobBytes > callerleaf.MaxDirectSourceBytes {
		return nil, huma.Error409Conflict("caller citation immutable content differs")
	}
	blob, err := gitobj.ReadBlob(
		ctx, repositoryDir, indexed.objectID, callerleaf.MaxDirectSourceBytes,
	)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, gitobj.ErrTooLarge) {
			return nil, huma.Error409Conflict("caller citation immutable content differs")
		}
		return nil, huma.Error500InternalServerError("read caller citation object", err)
	}
	digest := sha256.Sum256(blob)
	if "sha256:"+hex.EncodeToString(digest[:]) != indexed.blobDigest ||
		indexed.startByte < 0 || indexed.endByte <= indexed.startByte ||
		indexed.endByte > len(blob) {
		return nil, huma.Error409Conflict("caller citation immutable content differs")
	}
	if err := service.confirm(
		ctx, read, generation.Repository, visibility,
	); err != nil {
		return nil, err
	}
	return &CallerMapCitation{
		SchemaVersion: exactCallerCitationVersion, Generation: generation,
		Source: CallerMapSource{
			Repository: generation.Repository, Commit: generation.Commit,
			Path: indexed.path, ObjectID: indexed.objectID,
			BlobDigest: indexed.blobDigest, Plane: exactCallerMapPlane,
			StartByte: indexed.startByte, EndByte: indexed.endByte,
			StartLine: indexed.startLine, EndLine: indexed.endLine,
			AssertionID: indexed.recordID, RunID: generation.GenerationDigest,
			AtomID: indexed.blobDigest,
		},
		Content: string(blob[indexed.startByte:indexed.endByte]),
	}, nil
}

func (service *exactCallerMapService) beginCitationRead(
	ctx context.Context,
) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case service.citationRead <- struct{}{}:
		return func() { <-service.citationRead }, nil
	default:
		return nil, huma.Error503ServiceUnavailable(
			"caller citation read capacity is busy; retry",
		)
	}
}

func (service *exactCallerMapService) beginExactRead(
	ctx context.Context,
) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case service.exactRead <- struct{}{}:
		return func() { <-service.exactRead }, nil
	default:
		return nil, huma.Error503ServiceUnavailable(
			"caller map exact read capacity is busy; retry",
		)
	}
}

func exactCallerCitationRepositoryDir(dataDir, repository string) (string, error) {
	directory, err := phebssync.SafeRepoDir(dataDir, repository)
	if err == nil {
		return directory, nil
	}
	if errors.Is(err, phebssync.ErrBadInput) {
		return "", huma.Error404NotFound("caller citation not found")
	}
	return "", huma.Error500InternalServerError(
		"resolve caller citation repository", err,
	)
}

func callerReadGeneration(read *callerexecute.PublicationRead) store.CallerGenerationIdentity {
	if read == nil {
		return store.CallerGenerationIdentity{}
	}
	if read.Summary != nil {
		return read.Summary.Generation
	}
	if read.Publication != nil {
		return read.Publication.Generation
	}
	return read.ExpectedGeneration
}

func exactCallerReaderError(action string, err error) error {
	if errors.Is(err, callerleaf.ErrCapacity) {
		return huma.Error503ServiceUnavailable(
			"caller publication capacity is busy; retry",
		)
	}
	return huma.Error500InternalServerError(action, err)
}
