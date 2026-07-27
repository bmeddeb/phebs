package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	workbenchImplementationSchemaVersion = "workbench-related-implementation-v1"
	workbenchImplementationCursorVersion = "workbench-related-implementation-cursor-v1"
	workbenchImplementationCursorLimit   = 64 << 10
	workbenchImplementationDefaultPage   = 50
	workbenchImplementationMaxPage       = 100
	workbenchImplementationMaxAnchors    = 32
	workbenchImplementationMaxSeeds      = 64
	workbenchImplementationMaxNavSeeds   = 32
	workbenchImplementationMaxHistory    = 16
	workbenchImplementationHistoryLimit  = 2
	workbenchImplementationMaxQueries    = 32
	workbenchImplementationSearchLimit   = 50
	workbenchImplementationMaxSearchRows = 500
	workbenchImplementationMaxRows       = 2_000
	workbenchImplementationMaxSource     = 32 << 20
	workbenchImplementationDiffExcerpt   = 16 << 10
	workbenchImplementationMaxDiffFiles  = 50
	workbenchImplementationCaveat        = "Related files and history are cited candidates selected by the displayed deterministic rule or explicit user pin. They are not a recommended edit, runtime-use claim, completeness claim, correctness claim, or authorization to mutate source."
)

const (
	workbenchImplementationRoleProduction    = "production"
	workbenchImplementationRoleTest          = "test"
	workbenchImplementationRoleMock          = "mock"
	workbenchImplementationRoleGenerated     = "generated"
	workbenchImplementationRoleVendor        = "vendor"
	workbenchImplementationRoleDocumentation = "documentation"
)

type workbenchImplementationReader interface {
	Read(context.Context, string, string) (*store.WorkbenchView, error)
}

type workbenchImplementationCatalog interface {
	OperationForProtocol(
		context.Context,
		string,
		string,
		string,
		string,
	) (*ContractCatalogOperation, error)
}

type workbenchImplementationSource interface {
	ReadSource(context.Context, string, string, string) ([]byte, error)
}

type workbenchImplementationSearch interface {
	Search(context.Context, string, search.Options) (*search.Result, error)
}

type workbenchImplementationCodeNav interface {
	Definition(context.Context, codenav.Query) (codenav.DefinitionResult, error)
	References(context.Context, codenav.Query) (codenav.ReferencesResult, error)
}

type workbenchImplementationHistory interface {
	Commits(context.Context, phebssync.CommitListRequest) (phebssync.CommitListResult, error)
	Diff(context.Context, phebssync.DiffRequest) (phebssync.DiffResult, error)
}

type workbenchGitSource struct {
	dataDir string
}

func (source workbenchGitSource) ReadSource(
	ctx context.Context,
	repository, commit, filePath string,
) ([]byte, error) {
	if !gitobj.IsObjectID(commit) {
		return nil, fmt.Errorf("source commit must be an immutable object id")
	}
	return phebssync.CatFile(ctx, source.dataDir, repository, commit, filePath)
}

// WorkbenchImplementationService composes the already-authorized Atlas,
// indexed search, SCIP, source, and history readers. It has no mutation or
// evidence-store field and never resolves a mutable repository ref.
type WorkbenchImplementationService struct {
	opts      Options
	workbench workbenchImplementationReader
	catalog   workbenchImplementationCatalog
	source    workbenchImplementationSource
	search    workbenchImplementationSearch
	codeNav   workbenchImplementationCodeNav
	history   workbenchImplementationHistory
}

func NewWorkbenchImplementationService(opts Options) *WorkbenchImplementationService {
	if opts.Store == nil ||
		opts.Workbench == nil ||
		opts.Principal == nil ||
		opts.DataDir == "" {
		return nil
	}
	catalog := opts.ContractCatalog
	if catalog == nil {
		catalog = NewContractCatalogService(opts)
	}
	if catalog == nil {
		return nil
	}
	return &WorkbenchImplementationService{
		opts:      opts,
		workbench: opts.Workbench,
		catalog:   catalog,
		source:    workbenchGitSource{dataDir: opts.DataDir},
		search:    opts.Search,
		codeNav:   opts.CodeNav,
		history:   phebssync.NewHistoryService(opts.DataDir),
	}
}

type WorkbenchImplementationAnchor struct {
	Repository string                   `json:"repository"`
	Commit     string                   `json:"commit"`
	Path       string                   `json:"path"`
	Line       int32                    `json:"line"`
	Character  int32                    `json:"character"`
	Encoding   codenav.PositionEncoding `json:"encoding,omitempty"`
}

type WorkbenchImplementationRequest struct {
	InvestigationID string                          `json:"investigation_id"`
	RevisionID      string                          `json:"revision_id"`
	Anchors         []WorkbenchImplementationAnchor `json:"anchors"`
	PageSize        int                             `json:"page_size,omitempty"`
	Cursor          string                          `json:"cursor,omitempty"`
}

type WorkbenchImplementationSourceCitation struct {
	Repository    string `json:"repository"`
	Commit        string `json:"commit"`
	Path          string `json:"path"`
	StartByte     int    `json:"start_byte,omitempty"`
	EndByte       int    `json:"end_byte,omitempty"`
	StartLine     int    `json:"start_line,omitempty"`
	StartColumn   int    `json:"start_column,omitempty"`
	EndLine       int    `json:"end_line,omitempty"`
	EndColumn     int    `json:"end_column,omitempty"`
	ContentDigest string `json:"content_digest,omitempty"`
	SizeBytes     int    `json:"size_bytes,omitempty"`
}

type WorkbenchImplementationCommit struct {
	ID               string   `json:"id"`
	ParentIDs        []string `json:"parent_ids"`
	Subject          string   `json:"subject"`
	SubjectTruncated bool     `json:"subject_truncated,omitempty"`
	AuthorName       string   `json:"author_name,omitempty"`
	AuthorTime       string   `json:"author_time,omitempty"`
}

type WorkbenchImplementationDiff struct {
	Base           string                    `json:"base,omitempty"`
	Head           string                    `json:"head"`
	Digest         string                    `json:"digest"`
	PatchExcerpt   string                    `json:"patch_excerpt"`
	PatchTruncated bool                      `json:"patch_truncated"`
	Files          []phebssync.GitFileChange `json:"files"`
	FilesTruncated bool                      `json:"files_truncated,omitempty"`
}

type WorkbenchImplementationRow struct {
	ID             string                                `json:"id"`
	Kind           string                                `json:"kind"`
	CodeRole       string                                `json:"code_role"`
	Boundary       string                                `json:"boundary,omitempty"`
	ReviewState    string                                `json:"review_state"`
	SelectionRule  string                                `json:"selection_rule"`
	SelectionInput string                                `json:"selection_input"`
	Symbol         string                                `json:"symbol,omitempty"`
	Source         WorkbenchImplementationSourceCitation `json:"source"`
	Commit         *WorkbenchImplementationCommit        `json:"commit,omitempty"`
	Diff           *WorkbenchImplementationDiff          `json:"diff,omitempty"`
}

type WorkbenchImplementationCapability struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type WorkbenchImplementationGap struct {
	Capability string `json:"capability"`
	Target     string `json:"target,omitempty"`
	State      string `json:"state"`
	Code       string `json:"code"`
}

type WorkbenchImplementationPagination struct {
	TotalRows  int    `json:"total_rows"`
	Complete   bool   `json:"complete"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type WorkbenchImplementationPage struct {
	SchemaVersion   string                              `json:"schema_version"`
	InvestigationID string                              `json:"investigation_id"`
	RevisionID      string                              `json:"revision_id"`
	TicketKind      store.ChangeBriefTicketKind         `json:"ticket_kind"`
	Rows            []WorkbenchImplementationRow        `json:"rows"`
	Capabilities    []WorkbenchImplementationCapability `json:"capabilities"`
	Gaps            []WorkbenchImplementationGap        `json:"gaps"`
	Pagination      WorkbenchImplementationPagination   `json:"pagination"`
	SnapshotDigest  string                              `json:"snapshot_digest"`
	Caveat          string                              `json:"caveat"`
}

type workbenchImplementationCursor struct {
	Schema           string `json:"schema"`
	QueryDigest      string `json:"query_digest"`
	Principal        string `json:"principal"`
	RevisionDigest   string `json:"revision_digest"`
	VisibilityDigest string `json:"visibility_digest"`
	SnapshotDigest   string `json:"snapshot_digest"`
	Offset           int    `json:"offset"`
	Checksum         string `json:"checksum"`
}

type workbenchImplementationSeed struct {
	kind           string
	codeRole       string
	selectionRule  string
	selectionInput string
	source         WorkbenchImplementationSourceCitation
	query          codenav.Query
}

type workbenchImplementationBuild struct {
	service      *WorkbenchImplementationService
	ctx          context.Context
	visible      map[string]store.Repo
	rows         []WorkbenchImplementationRow
	gaps         []WorkbenchImplementationGap
	capabilities map[string]WorkbenchImplementationCapability
	sourceCache  map[string][]byte
	sourceBytes  int
	seenRows     map[string]struct{}
	seedFiles    map[string]workbenchImplementationSeed
}

func (service *WorkbenchImplementationService) Read(
	ctx context.Context,
	principal string,
	request WorkbenchImplementationRequest,
) (*WorkbenchImplementationPage, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable(
			"related implementation evidence unavailable",
		)
	}
	if service.opts.Store == nil ||
		service.workbench == nil ||
		service.catalog == nil ||
		service.source == nil {
		return nil, huma.Error503ServiceUnavailable(
			"related implementation evidence dependencies unavailable",
		)
	}
	if strings.TrimSpace(principal) == "" ||
		service.opts.Principal == nil ||
		strings.TrimSpace(service.opts.Principal(ctx)) != principal {
		return nil, store.ErrNotFound
	}
	if strings.TrimSpace(request.InvestigationID) != request.InvestigationID ||
		request.InvestigationID == "" ||
		strings.TrimSpace(request.RevisionID) != request.RevisionID ||
		request.RevisionID == "" {
		return nil, huma.Error400BadRequest(
			"investigation_id and revision_id are required",
		)
	}
	if request.PageSize == 0 {
		request.PageSize = workbenchImplementationDefaultPage
	}
	if request.PageSize < 1 || request.PageSize > workbenchImplementationMaxPage {
		return nil, huma.Error400BadRequest(fmt.Sprintf(
			"page_size must be from 1 through %d",
			workbenchImplementationMaxPage,
		))
	}
	anchors, err := canonicalWorkbenchImplementationAnchors(request.Anchors)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	request.Anchors = anchors

	view, err := service.workbench.Read(
		ctx,
		principal,
		request.InvestigationID,
	)
	if err != nil {
		return nil, err
	}
	if !validWorkbenchImplementationView(
		view,
		request.InvestigationID,
		request.RevisionID,
	) {
		return nil, store.ErrNotFound
	}

	visibleRepositoriesAtStart, err := visibleRepositories(ctx, service.opts)
	if err != nil {
		return nil, err
	}
	visible, visibilityDigest, err :=
		workbenchImplementationVisibility(visibleRepositoriesAtStart)
	if err != nil {
		return nil, err
	}
	queryDigest := digestJSON(struct {
		InvestigationID string                          `json:"investigation_id"`
		RevisionID      string                          `json:"revision_id"`
		Anchors         []WorkbenchImplementationAnchor `json:"anchors"`
		PageSize        int                             `json:"page_size"`
	}{
		InvestigationID: request.InvestigationID,
		RevisionID:      request.RevisionID,
		Anchors:         request.Anchors,
		PageSize:        request.PageSize,
	})
	revisionDigest := digestJSON(struct {
		Revision store.Revision    `json:"revision"`
		Brief    store.ChangeBrief `json:"brief"`
	}{Revision: view.Revision, Brief: view.Brief})

	build := &workbenchImplementationBuild{
		service:      service,
		ctx:          ctx,
		visible:      visible,
		rows:         []WorkbenchImplementationRow{},
		gaps:         []WorkbenchImplementationGap{},
		capabilities: make(map[string]WorkbenchImplementationCapability),
		sourceCache:  make(map[string][]byte),
		seenRows:     make(map[string]struct{}),
		seedFiles:    make(map[string]workbenchImplementationSeed),
	}
	build.setCapability("source", "enabled", "")
	catalogDigests, seeds, searchInputs, err :=
		build.catalogSeeds(view.Brief.What.Selections)
	if err != nil {
		return nil, err
	}
	explicitSeeds, err := build.explicitSeeds(request.Anchors)
	if err != nil {
		return nil, err
	}
	seeds = append(seeds, explicitSeeds...)
	seeds = canonicalWorkbenchImplementationSeeds(seeds)
	if len(seeds) > workbenchImplementationMaxSeeds {
		seeds = seeds[:workbenchImplementationMaxSeeds]
		build.addGap(
			"source",
			"",
			"truncated",
			"seed_limit",
		)
	}
	if len(seeds) == 0 {
		build.addGap(
			"source",
			"",
			"unsupported",
			"no_selected_contract_or_anchor",
		)
	}
	preparedSeeds := make([]workbenchImplementationSeed, 0, len(seeds))
	for _, seed := range seeds {
		prepared, err := build.addSeed(seed)
		if err != nil {
			return nil, err
		}
		preparedSeeds = append(preparedSeeds, prepared)
	}
	if err := build.searchRelated(searchInputs, preparedSeeds); err != nil {
		return nil, err
	}
	if err := build.navigate(preparedSeeds); err != nil {
		return nil, err
	}
	if err := build.readHistory(preparedSeeds); err != nil {
		return nil, err
	}

	confirmedView, err := service.workbench.Read(
		ctx,
		principal,
		request.InvestigationID,
	)
	if err != nil {
		return nil, err
	}
	if !validWorkbenchImplementationView(
		confirmedView,
		request.InvestigationID,
		request.RevisionID,
	) {
		return nil, store.ErrNotFound
	}
	confirmedRevisionDigest := digestJSON(struct {
		Revision store.Revision    `json:"revision"`
		Brief    store.ChangeBrief `json:"brief"`
	}{Revision: confirmedView.Revision, Brief: confirmedView.Brief})
	if confirmedRevisionDigest != revisionDigest {
		return nil, huma.Error409Conflict(
			"workbench revision changed while reading related implementation evidence; retry",
		)
	}
	visibleRepositoriesAtEnd, err := visibleRepositories(ctx, service.opts)
	if err != nil {
		return nil, err
	}
	_, confirmedVisibilityDigest, err :=
		workbenchImplementationVisibility(visibleRepositoriesAtEnd)
	if err != nil {
		return nil, err
	}
	if confirmedVisibilityDigest != visibilityDigest {
		return nil, huma.Error409Conflict(
			"repository authorization or indexed commits changed while reading related implementation evidence; retry",
		)
	}

	build.finalize()
	if len(build.rows) > workbenchImplementationMaxRows {
		build.rows = build.rows[:workbenchImplementationMaxRows]
		build.addGap(
			"composition",
			"",
			"truncated",
			"row_limit",
		)
		build.finalize()
	}
	capabilities := make([]WorkbenchImplementationCapability, 0, len(build.capabilities))
	for _, capability := range build.capabilities {
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(i, j int) bool {
		return capabilities[i].ID < capabilities[j].ID
	})
	snapshotDigest := digestJSON(struct {
		CatalogDigests   []string                            `json:"catalog_digests"`
		Rows             []WorkbenchImplementationRow        `json:"rows"`
		Capabilities     []WorkbenchImplementationCapability `json:"capabilities"`
		Gaps             []WorkbenchImplementationGap        `json:"gaps"`
		VisibilityDigest string                              `json:"visibility_digest"`
	}{
		CatalogDigests:   catalogDigests,
		Rows:             build.rows,
		Capabilities:     capabilities,
		Gaps:             build.gaps,
		VisibilityDigest: visibilityDigest,
	})
	cursor, err := decodeWorkbenchImplementationCursor(
		request.Cursor,
		queryDigest,
		principal,
		revisionDigest,
		visibilityDigest,
		snapshotDigest,
	)
	if err != nil {
		return nil, err
	}
	offset := 0
	if cursor != nil {
		offset = cursor.Offset
	}
	if offset < 0 || offset > len(build.rows) {
		return nil, huma.Error409Conflict(
			"related implementation evidence cursor no longer addresses this snapshot",
		)
	}
	end := min(offset+request.PageSize, len(build.rows))
	pageRows := slices.Clone(build.rows[offset:end])
	page := &WorkbenchImplementationPage{
		SchemaVersion:   workbenchImplementationSchemaVersion,
		InvestigationID: request.InvestigationID,
		RevisionID:      request.RevisionID,
		TicketKind:      view.Brief.TicketKind,
		Rows:            pageRows,
		Capabilities:    capabilities,
		Gaps:            slices.Clone(build.gaps),
		Pagination: WorkbenchImplementationPagination{
			TotalRows: len(build.rows),
			Complete:  end == len(build.rows),
		},
		SnapshotDigest: snapshotDigest,
		Caveat:         workbenchImplementationCaveat,
	}
	if !page.Pagination.Complete {
		page.Pagination.NextCursor, err = encodeWorkbenchImplementationCursor(
			workbenchImplementationCursor{
				Schema:           workbenchImplementationCursorVersion,
				QueryDigest:      queryDigest,
				Principal:        principal,
				RevisionDigest:   revisionDigest,
				VisibilityDigest: visibilityDigest,
				SnapshotDigest:   snapshotDigest,
				Offset:           end,
			},
		)
		if err != nil {
			return nil, err
		}
	}
	return page, nil
}

func validWorkbenchImplementationView(
	view *store.WorkbenchView,
	investigationID, revisionID string,
) bool {
	return view != nil &&
		view.Investigation.ID == investigationID &&
		view.Investigation.CurrentRevisionID == revisionID &&
		view.Revision.ID == revisionID &&
		view.Revision.InvestigationID == investigationID &&
		view.Brief.RevisionID == revisionID &&
		view.Brief.InvestigationID == investigationID
}

func (build *workbenchImplementationBuild) catalogSeeds(
	selections []store.ChangeBriefContractSelection,
) ([]string, []workbenchImplementationSeed, []string, error) {
	selections = slices.Clone(selections)
	sort.Slice(selections, func(i, j int) bool {
		return workbenchImplementationSelectionKey(selections[i]) <
			workbenchImplementationSelectionKey(selections[j])
	})
	build.setCapability("contract_atlas", "enabled", "")
	digests := make([]string, 0, len(selections))
	seeds := make([]workbenchImplementationSeed, 0)
	searchInputs := make([]string, 0, len(selections)*2)
	for _, selection := range selections {
		repository, ok := build.visible[selection.Repository]
		if !ok {
			return nil, nil, nil, store.ErrNotFound
		}
		if !gitobj.IsObjectID(repository.IndexedCommitHash) {
			return nil, nil, nil, huma.Error409Conflict(
				"selected repository has no immutable indexed commit",
			)
		}
		detail, err := build.service.catalog.OperationForProtocol(
			build.ctx,
			selection.Protocol,
			selection.Repository,
			selection.DeclarationLineage,
			selection.CanonicalOperation,
		)
		if err != nil {
			return nil, nil, nil, err
		}
		if detail == nil ||
			detail.Protocol != selection.Protocol ||
			detail.Repository != selection.Repository ||
			detail.DeclarationLineage != selection.DeclarationLineage ||
			detail.Operation != selection.CanonicalOperation {
			return nil, nil, nil, huma.Error409Conflict(
				"contract Atlas returned an inconsistent selected operation",
			)
		}
		digests = append(
			digests,
			workbenchImplementationCatalogDigest(selection, detail),
		)
		input := workbenchImplementationSelectionKey(selection)
		seeds = append(
			seeds,
			build.claimSeeds(
				"declaration",
				detail.Declaration,
				"selected_contract_declaration_v1",
				input,
			)...,
		)
		relationships := slices.Clone(detail.Implementations)
		sort.Slice(relationships, func(i, j int) bool {
			return workbenchImplementationRelationshipKey(relationships[i]) <
				workbenchImplementationRelationshipKey(relationships[j])
		})
		for _, relationship := range relationships {
			if relationship.Kind != "implementation" {
				continue
			}
			seeds = append(
				seeds,
				build.claimSeeds(
					"implementation",
					relationship.Claim,
					"atlas_implementation_relationship_v1",
					input+"|"+relationship.Classification,
				)...,
			)
		}
		if detail.RelationshipsTruncated {
			build.addGap(
				"contract_atlas",
				input,
				"truncated",
				"relationship_limit",
			)
		}
		searchInputs = append(
			searchInputs,
			workbenchImplementationOperationTerms(detail.ServiceFQN, detail.Method)...,
		)
	}
	slices.Sort(digests)
	searchInputs = canonicalWorkbenchImplementationStrings(searchInputs)
	return digests, seeds, searchInputs, nil
}

func (build *workbenchImplementationBuild) claimSeeds(
	kind string,
	claim ContractCatalogClaim,
	rule, input string,
) []workbenchImplementationSeed {
	sources := slices.Clone(claim.Sources)
	sort.Slice(sources, func(i, j int) bool {
		return workbenchImplementationCatalogSourceKey(sources[i]) <
			workbenchImplementationCatalogSourceKey(sources[j])
	})
	seeds := make([]workbenchImplementationSeed, 0, len(sources))
	for _, source := range sources {
		repository, ok := build.visible[source.Repository]
		if !ok || source.Commit != repository.IndexedCommitHash {
			continue
		}
		if !gitobj.IsObjectID(source.Commit) ||
			!validWorkbenchImplementationPath(source.Path) {
			continue
		}
		seeds = append(seeds, workbenchImplementationSeed{
			kind:           kind,
			codeRole:       workbenchImplementationCodeRole(source.Path, claim.CodeRole),
			selectionRule:  rule,
			selectionInput: input,
			source: WorkbenchImplementationSourceCitation{
				Repository: source.Repository,
				Commit:     source.Commit,
				Path:       source.Path,
				StartByte:  source.StartByte,
				EndByte:    source.EndByte,
				StartLine:  source.StartLine,
				EndLine:    source.EndLine,
			},
		})
	}
	if claim.SourcesTruncated {
		build.addGap(
			"contract_atlas",
			input,
			"truncated",
			"claim_source_limit",
		)
	}
	return seeds
}

func (build *workbenchImplementationBuild) explicitSeeds(
	anchors []WorkbenchImplementationAnchor,
) ([]workbenchImplementationSeed, error) {
	seeds := make([]workbenchImplementationSeed, 0, len(anchors))
	for _, anchor := range anchors {
		repository, ok := build.visible[anchor.Repository]
		if !ok {
			return nil, store.ErrNotFound
		}
		if anchor.Commit != repository.IndexedCommitHash {
			return nil, huma.Error409Conflict(
				"explicit anchors must use the repository's immutable indexed commit",
			)
		}
		seeds = append(seeds, workbenchImplementationSeed{
			kind:           "explicit_anchor",
			codeRole:       workbenchImplementationCodeRole(anchor.Path, ""),
			selectionRule:  "explicit_user_pin_v1",
			selectionInput: workbenchImplementationAnchorKey(anchor),
			source: WorkbenchImplementationSourceCitation{
				Repository:  anchor.Repository,
				Commit:      anchor.Commit,
				Path:        anchor.Path,
				StartLine:   int(anchor.Line) + 1,
				StartColumn: int(anchor.Character) + 1,
			},
			query: codenav.Query{
				Repo:      anchor.Repository,
				Revision:  anchor.Commit,
				Path:      anchor.Path,
				Line:      anchor.Line,
				Character: anchor.Character,
				Encoding:  anchor.Encoding,
			},
		})
	}
	return seeds, nil
}

func (build *workbenchImplementationBuild) addSeed(
	seed workbenchImplementationSeed,
) (workbenchImplementationSeed, error) {
	content, err := build.readSource(seed.source)
	if err != nil {
		return seed, err
	}
	seed.source.ContentDigest = workbenchImplementationBytesDigest(content)
	seed.source.SizeBytes = len(content)
	if seed.query.Repo == "" {
		line, character, ok := workbenchImplementationPosition(
			content,
			seed.source.StartByte,
		)
		if ok {
			seed.query = codenav.Query{
				Repo:      seed.source.Repository,
				Revision:  seed.source.Commit,
				Path:      seed.source.Path,
				Line:      line,
				Character: character,
				Encoding:  codenav.EncodingUTF8,
			}
		}
	}
	row := WorkbenchImplementationRow{
		Kind:           seed.kind,
		CodeRole:       seed.codeRole,
		Boundary:       workbenchImplementationBoundary(seed.codeRole),
		ReviewState:    "selected",
		SelectionRule:  seed.selectionRule,
		SelectionInput: seed.selectionInput,
		Source:         seed.source,
	}
	build.addRow(row)
	key := workbenchImplementationFileKey(seed.source)
	if _, exists := build.seedFiles[key]; !exists {
		build.seedFiles[key] = seed
	}
	return seed, nil
}

func (build *workbenchImplementationBuild) readSource(
	source WorkbenchImplementationSourceCitation,
) ([]byte, error) {
	if build.service.source == nil {
		return nil, huma.Error503ServiceUnavailable(
			"immutable source reader unavailable",
		)
	}
	key := workbenchImplementationFileKey(source)
	if content, ok := build.sourceCache[key]; ok {
		return content, nil
	}
	content, err := build.service.source.ReadSource(
		build.ctx,
		source.Repository,
		source.Commit,
		source.Path,
	)
	if err != nil {
		return nil, huma.Error409Conflict(
			"an immutable cited source file is unavailable",
			err,
		)
	}
	if build.sourceBytes+len(content) > workbenchImplementationMaxSource {
		return nil, huma.Error422UnprocessableEntity(
			"related source files exceed the bounded source-read budget",
		)
	}
	build.sourceBytes += len(content)
	build.sourceCache[key] = slices.Clone(content)
	return content, nil
}

func (build *workbenchImplementationBuild) searchRelated(
	terms []string,
	seeds []workbenchImplementationSeed,
) error {
	if build.service.search == nil {
		build.setCapability("source_search", "unsupported", "search reader unavailable")
		build.addGap("source_search", "", "unsupported", "search_unavailable")
		return nil
	}
	build.setCapability("source_search", "enabled", "")
	repositories := make([]string, 0)
	seenRepositories := make(map[string]struct{})
	for _, seed := range seeds {
		if _, ok := seenRepositories[seed.source.Repository]; ok {
			continue
		}
		seenRepositories[seed.source.Repository] = struct{}{}
		repositories = append(repositories, seed.source.Repository)
	}
	slices.Sort(repositories)
	if len(terms) == 0 || len(repositories) == 0 {
		build.addGap(
			"source_search",
			"",
			"unsupported",
			"no_bounded_search_input",
		)
		return nil
	}
	queryCount := 0
	rowCount := 0
	for _, repositoryName := range repositories {
		for _, term := range terms {
			if queryCount == workbenchImplementationMaxQueries ||
				rowCount == workbenchImplementationMaxSearchRows {
				build.addGap(
					"source_search",
					"",
					"truncated",
					"candidate_limit",
				)
				return nil
			}
			queryCount++
			raw := regexp.QuoteMeta(term) +
				" repo:^" + regexp.QuoteMeta(repositoryName) + "$"
			result, err := build.service.search.Search(
				build.ctx,
				raw,
				search.Options{
					MaxMatches:   workbenchImplementationSearchLimit,
					ContextLines: 0,
				},
			)
			if err != nil {
				if ctxErr := build.ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				build.setCapability("source_search", "failed", "bounded search failed")
				build.addGap(
					"source_search",
					repositoryName+"|"+term,
					"failed",
					"search_failed",
				)
				continue
			}
			if result == nil {
				build.setCapability(
					"source_search",
					"failed",
					"bounded search returned no result",
				)
				build.addGap(
					"source_search",
					repositoryName+"|"+term,
					"failed",
					"search_failed",
				)
				continue
			}
			files := slices.Clone(result.Files)
			sort.Slice(files, func(i, j int) bool {
				return workbenchImplementationSearchFileKey(files[i]) <
					workbenchImplementationSearchFileKey(files[j])
			})
			for _, file := range files {
				repository, visible := build.visible[file.Repo]
				if !visible {
					continue
				}
				if file.Ref != repository.IndexedCommitHash {
					return huma.Error409Conflict(
						"indexed search revision changed while reading related implementation evidence; retry",
					)
				}
				if file.Repo != repositoryName ||
					!gitobj.IsObjectID(file.Ref) ||
					!validWorkbenchImplementationPath(file.Path) {
					continue
				}
				source := WorkbenchImplementationSourceCitation{
					Repository: file.Repo,
					Commit:     file.Ref,
					Path:       file.Path,
				}
				if matched, ok := workbenchImplementationSearchRange(file); ok {
					source.StartLine = matched.StartLine
					source.StartColumn = matched.StartCol
					source.EndLine = matched.EndLine
					source.EndColumn = matched.EndCol
				}
				role := workbenchImplementationCodeRole(file.Path, "")
				build.addRow(WorkbenchImplementationRow{
					Kind:           "search_candidate",
					CodeRole:       role,
					Boundary:       workbenchImplementationBoundary(role),
					ReviewState:    "review_candidate",
					SelectionRule:  "operation_identifier_search_v1",
					SelectionInput: repositoryName + "|" + term,
					Source:         source,
				})
				rowCount++
				if rowCount == workbenchImplementationMaxSearchRows {
					break
				}
			}
		}
	}
	return nil
}

func (build *workbenchImplementationBuild) navigate(
	seeds []workbenchImplementationSeed,
) error {
	if build.service.codeNav == nil {
		build.setCapability("scip", "unsupported", "SCIP reader unavailable")
		build.addGap("scip", "", "unsupported", "scip_unavailable")
		return nil
	}
	build.setCapability("scip", "enabled", "")
	if len(seeds) > workbenchImplementationMaxNavSeeds {
		seeds = seeds[:workbenchImplementationMaxNavSeeds]
		build.addGap("scip", "", "truncated", "anchor_limit")
	}
	for _, seed := range seeds {
		if seed.query.Repo == "" {
			build.addGap(
				"scip",
				workbenchImplementationFileKey(seed.source),
				"unsupported",
				"source_position_unavailable",
			)
			continue
		}
		definition, err := build.service.codeNav.Definition(build.ctx, seed.query)
		if err != nil {
			if ctxErr := build.ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			build.setCapability("scip", "failed", "SCIP lookup failed")
			build.addGap(
				"scip",
				workbenchImplementationFileKey(seed.source),
				"failed",
				"definition_failed",
			)
		} else if !definition.Available {
			build.addGap(
				"scip",
				workbenchImplementationFileKey(seed.source),
				"unsupported",
				"index_unavailable",
			)
		} else if definition.Location == nil {
			build.addGap(
				"scip",
				workbenchImplementationFileKey(seed.source),
				"unsupported",
				"definition_not_resolved",
			)
		} else if location, ok := build.validLocation(*definition.Location); ok {
			role := workbenchImplementationCodeRole(location.Path, "")
			build.addRow(WorkbenchImplementationRow{
				Kind:           "definition",
				CodeRole:       role,
				Boundary:       workbenchImplementationBoundary(role),
				ReviewState:    "review_candidate",
				SelectionRule:  "scip_definition_from_cited_anchor_v1",
				SelectionInput: workbenchImplementationFileKey(seed.source),
				Symbol:         definition.Symbol,
				Source:         location,
			})
		}

		references, err := build.service.codeNav.References(build.ctx, seed.query)
		if err != nil {
			if ctxErr := build.ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			build.setCapability("scip", "failed", "SCIP lookup failed")
			build.addGap(
				"scip",
				workbenchImplementationFileKey(seed.source),
				"failed",
				"references_failed",
			)
			continue
		}
		if !references.Available {
			continue
		}
		locations := slices.Clone(references.Locations)
		sort.Slice(locations, func(i, j int) bool {
			return workbenchImplementationLocationKey(locations[i]) <
				workbenchImplementationLocationKey(locations[j])
		})
		if len(locations) > codenav.MaxReferenceLocations {
			locations = locations[:codenav.MaxReferenceLocations]
			references.Truncated = true
		}
		for _, rawLocation := range locations {
			location, ok := build.validLocation(rawLocation)
			if !ok {
				continue
			}
			role := workbenchImplementationCodeRole(location.Path, "")
			build.addRow(WorkbenchImplementationRow{
				Kind:           "reference",
				CodeRole:       role,
				Boundary:       workbenchImplementationBoundary(role),
				ReviewState:    "review_candidate",
				SelectionRule:  "scip_reference_from_cited_anchor_v1",
				SelectionInput: workbenchImplementationFileKey(seed.source),
				Symbol:         references.Symbol,
				Source:         location,
			})
		}
		if references.Truncated {
			build.addGap(
				"scip",
				workbenchImplementationFileKey(seed.source),
				"truncated",
				"reference_limit",
			)
		}
	}
	return nil
}

func (build *workbenchImplementationBuild) validLocation(
	location codenav.Location,
) (WorkbenchImplementationSourceCitation, bool) {
	repository, ok := build.visible[location.Repo]
	if !ok ||
		location.Revision != repository.IndexedCommitHash ||
		!gitobj.IsObjectID(location.Revision) ||
		!validWorkbenchImplementationPath(location.Path) {
		return WorkbenchImplementationSourceCitation{}, false
	}
	return WorkbenchImplementationSourceCitation{
		Repository:  location.Repo,
		Commit:      location.Revision,
		Path:        location.Path,
		StartLine:   int(location.Range.Start.Line) + 1,
		StartColumn: int(location.Range.Start.Character) + 1,
		EndLine:     int(location.Range.End.Line) + 1,
		EndColumn:   int(location.Range.End.Character) + 1,
	}, true
}

func (build *workbenchImplementationBuild) readHistory(
	seeds []workbenchImplementationSeed,
) error {
	if build.service.history == nil {
		build.setCapability("history", "unsupported", "history reader unavailable")
		build.addGap("history", "", "unsupported", "history_unavailable")
		return nil
	}
	build.setCapability("history", "enabled", "")
	files := make([]workbenchImplementationSeed, 0, len(build.seedFiles))
	for _, seed := range build.seedFiles {
		files = append(files, seed)
	}
	files = canonicalWorkbenchImplementationSeeds(files)
	if len(files) > workbenchImplementationMaxHistory {
		files = files[:workbenchImplementationMaxHistory]
		build.addGap("history", "", "truncated", "file_limit")
	}
	for _, seed := range files {
		target := workbenchImplementationFileKey(seed.source)
		commits, err := build.service.history.Commits(
			build.ctx,
			phebssync.CommitListRequest{
				Repo:  seed.source.Repository,
				Ref:   seed.source.Commit,
				Path:  seed.source.Path,
				Limit: workbenchImplementationHistoryLimit,
			},
		)
		if err != nil {
			if ctxErr := build.ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			build.setCapability("history", "failed", "history lookup failed")
			build.addGap("history", target, "failed", "commit_list_failed")
			continue
		}
		if commits.Revision != seed.source.Commit {
			return huma.Error409Conflict(
				"history revision changed while reading related implementation evidence; retry",
			)
		}
		historyCommits := slices.Clone(commits.Commits)
		sort.Slice(historyCommits, func(i, j int) bool {
			return historyCommits[i].ID < historyCommits[j].ID
		})
		if len(historyCommits) > workbenchImplementationHistoryLimit {
			historyCommits = historyCommits[:workbenchImplementationHistoryLimit]
			commits.HasMore = true
		}
		for _, commit := range historyCommits {
			if !gitobj.IsObjectID(commit.ID) {
				return huma.Error409Conflict(
					"history returned a mutable or invalid commit identity",
				)
			}
			converted := workbenchImplementationCommit(commit)
			build.addRow(WorkbenchImplementationRow{
				Kind:           "history_commit",
				CodeRole:       seed.codeRole,
				Boundary:       workbenchImplementationBoundary(seed.codeRole),
				ReviewState:    "review_candidate",
				SelectionRule:  "path_history_from_cited_file_v1",
				SelectionInput: target,
				Source: WorkbenchImplementationSourceCitation{
					Repository: seed.source.Repository,
					Commit:     seed.source.Commit,
					Path:       seed.source.Path,
				},
				Commit: &converted,
			})
		}
		if len(historyCommits) == 0 {
			build.addGap("history", target, "unsupported", "no_file_history")
			continue
		}
		selectedCommit := historyCommits[0]
		diff, err := build.service.history.Diff(
			build.ctx,
			phebssync.DiffRequest{
				Repo:            seed.source.Repository,
				Head:            selectedCommit.ID,
				Path:            seed.source.Path,
				ContextLines:    3,
				ContextLinesSet: true,
			},
		)
		if err != nil {
			if ctxErr := build.ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			build.setCapability("history", "failed", "history diff failed")
			build.addGap("history", target, "failed", "diff_failed")
			continue
		}
		if diff.Head != selectedCommit.ID ||
			diff.Base != "" && !gitobj.IsObjectID(diff.Base) {
			return huma.Error409Conflict(
				"history diff returned an inconsistent immutable revision",
			)
		}
		convertedDiff := workbenchImplementationDiff(diff)
		build.addRow(WorkbenchImplementationRow{
			Kind:           "history_diff",
			CodeRole:       seed.codeRole,
			Boundary:       workbenchImplementationBoundary(seed.codeRole),
			ReviewState:    "review_candidate",
			SelectionRule:  "lexicographically_first_path_commit_diff_v1",
			SelectionInput: target,
			Source: WorkbenchImplementationSourceCitation{
				Repository: seed.source.Repository,
				Commit:     seed.source.Commit,
				Path:       seed.source.Path,
			},
			Diff: &convertedDiff,
		})
		if commits.HasMore {
			build.addGap("history", target, "truncated", "commit_limit")
		}
	}
	return nil
}

func (build *workbenchImplementationBuild) addRow(
	row WorkbenchImplementationRow,
) {
	row.ID = ""
	key := digestJSON(row)
	if _, exists := build.seenRows[key]; exists {
		return
	}
	build.seenRows[key] = struct{}{}
	row.ID = "wri_" + strings.TrimPrefix(key, "sha256:")[:24]
	build.rows = append(build.rows, row)
}

func (build *workbenchImplementationBuild) addGap(
	capability, target, state, code string,
) {
	build.gaps = append(build.gaps, WorkbenchImplementationGap{
		Capability: capability,
		Target:     target,
		State:      state,
		Code:       code,
	})
}

func (build *workbenchImplementationBuild) setCapability(
	id, state, reason string,
) {
	current, ok := build.capabilities[id]
	if ok && workbenchImplementationCapabilityRank(current.State) >
		workbenchImplementationCapabilityRank(state) {
		return
	}
	build.capabilities[id] = WorkbenchImplementationCapability{
		ID: id, State: state, Reason: reason,
	}
}

func (build *workbenchImplementationBuild) finalize() {
	sort.Slice(build.rows, func(i, j int) bool {
		return workbenchImplementationRowKey(build.rows[i]) <
			workbenchImplementationRowKey(build.rows[j])
	})
	sort.Slice(build.gaps, func(i, j int) bool {
		left, right := build.gaps[i], build.gaps[j]
		return left.Capability+"|"+left.Target+"|"+left.State+"|"+left.Code <
			right.Capability+"|"+right.Target+"|"+right.State+"|"+right.Code
	})
	build.gaps = slices.Compact(build.gaps)
}

func canonicalWorkbenchImplementationAnchors(
	anchors []WorkbenchImplementationAnchor,
) ([]WorkbenchImplementationAnchor, error) {
	if len(anchors) > workbenchImplementationMaxAnchors {
		return nil, fmt.Errorf(
			"anchors must contain at most %d entries",
			workbenchImplementationMaxAnchors,
		)
	}
	anchors = slices.Clone(anchors)
	for index := range anchors {
		anchor := &anchors[index]
		if strings.TrimSpace(anchor.Repository) != anchor.Repository ||
			anchor.Repository == "" ||
			!gitobj.IsObjectID(anchor.Commit) ||
			!validWorkbenchImplementationPath(anchor.Path) ||
			anchor.Line < 0 ||
			anchor.Character < 0 {
			return nil, fmt.Errorf(
				"anchors must use a repository, immutable commit, safe path, and non-negative position",
			)
		}
		if anchor.Encoding == "" {
			anchor.Encoding = codenav.EncodingUTF8
		}
		switch anchor.Encoding {
		case codenav.EncodingUTF8, codenav.EncodingUTF16, codenav.EncodingUTF32:
		default:
			return nil, fmt.Errorf("anchors contain an unsupported position encoding")
		}
	}
	sort.Slice(anchors, func(i, j int) bool {
		return workbenchImplementationAnchorKey(anchors[i]) <
			workbenchImplementationAnchorKey(anchors[j])
	})
	for index := 1; index < len(anchors); index++ {
		if workbenchImplementationAnchorKey(anchors[index-1]) ==
			workbenchImplementationAnchorKey(anchors[index]) {
			return nil, fmt.Errorf("anchors contain a duplicate explicit pin")
		}
	}
	return anchors, nil
}

func canonicalWorkbenchImplementationSeeds(
	seeds []workbenchImplementationSeed,
) []workbenchImplementationSeed {
	seeds = slices.Clone(seeds)
	sort.Slice(seeds, func(i, j int) bool {
		return workbenchImplementationSeedKey(seeds[i]) <
			workbenchImplementationSeedKey(seeds[j])
	})
	return slices.CompactFunc(seeds, func(a, b workbenchImplementationSeed) bool {
		return workbenchImplementationSeedKey(a) ==
			workbenchImplementationSeedKey(b)
	})
}

func workbenchImplementationVisibility(
	repositories []store.Repo,
) (map[string]store.Repo, string, error) {
	visible := make(map[string]store.Repo, len(repositories))
	type identity struct {
		Repository string `json:"repository"`
		Commit     string `json:"commit"`
	}
	identities := make([]identity, 0, len(repositories))
	for _, repository := range repositories {
		if repository.Name == "" {
			return nil, "", huma.Error500InternalServerError(
				"repository visibility set is inconsistent",
			)
		}
		if _, exists := visible[repository.Name]; exists {
			return nil, "", huma.Error500InternalServerError(
				"repository visibility set is inconsistent",
			)
		}
		visible[repository.Name] = repository
		identities = append(identities, identity{
			Repository: repository.Name,
			Commit:     repository.IndexedCommitHash,
		})
	}
	sort.Slice(identities, func(i, j int) bool {
		return identities[i].Repository < identities[j].Repository
	})
	return visible, digestJSON(identities), nil
}

func workbenchImplementationCodeRole(filePath, declared string) string {
	switch declared {
	case workbenchImplementationRoleProduction,
		workbenchImplementationRoleTest,
		workbenchImplementationRoleMock,
		workbenchImplementationRoleGenerated,
		workbenchImplementationRoleVendor,
		workbenchImplementationRoleDocumentation:
		return declared
	}
	lower := strings.ToLower(filePath)
	segments := strings.Split(lower, "/")
	for _, segment := range segments {
		if segment == "vendor" || segment == "third_party" {
			return workbenchImplementationRoleVendor
		}
	}
	base := path.Base(lower)
	for _, segment := range segments {
		if segment == "mock" || segment == "mocks" ||
			strings.HasPrefix(segment, "mock_") {
			return workbenchImplementationRoleMock
		}
	}
	if strings.HasSuffix(base, "_mock.go") {
		return workbenchImplementationRoleMock
	}
	for _, segment := range segments {
		if segment == "generated" || segment == "gen" {
			return workbenchImplementationRoleGenerated
		}
	}
	if strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, "_grpc.pb.go") ||
		strings.Contains(base, ".generated.") {
		return workbenchImplementationRoleGenerated
	}
	for _, segment := range segments {
		if segment == "test" || segment == "tests" || segment == "testdata" {
			return workbenchImplementationRoleTest
		}
	}
	if strings.HasSuffix(base, "_test.go") ||
		strings.HasSuffix(base, ".spec.ts") ||
		strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".spec.tsx") ||
		strings.HasSuffix(base, ".test.tsx") {
		return workbenchImplementationRoleTest
	}
	extension := path.Ext(base)
	for _, segment := range segments {
		if segment == "doc" || segment == "docs" {
			return workbenchImplementationRoleDocumentation
		}
	}
	switch extension {
	case ".md", ".rst", ".adoc":
		return workbenchImplementationRoleDocumentation
	default:
		return workbenchImplementationRoleProduction
	}
}

func workbenchImplementationBoundary(role string) string {
	switch role {
	case workbenchImplementationRoleGenerated:
		return "generated"
	case workbenchImplementationRoleVendor:
		return "vendor"
	default:
		return ""
	}
}

func workbenchImplementationOperationTerms(serviceFQN, method string) []string {
	terms := []string{}
	if validWorkbenchImplementationTerm(method) {
		terms = append(terms, method)
	}
	parts := strings.FieldsFunc(serviceFQN, func(r rune) bool {
		return r == '.' || r == '/' || r == ':' || r == '#'
	})
	if len(parts) > 0 && validWorkbenchImplementationTerm(parts[len(parts)-1]) {
		terms = append(terms, parts[len(parts)-1])
	}
	return canonicalWorkbenchImplementationStrings(terms)
}

func validWorkbenchImplementationTerm(value string) bool {
	if len(value) < 2 || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		switch {
		case r == '_':
		case 'a' <= r && r <= 'z':
		case 'A' <= r && r <= 'Z':
		case '0' <= r && r <= '9':
		default:
			return false
		}
	}
	return true
}

func validWorkbenchImplementationPath(value string) bool {
	if value == "" || len(value) > 4096 ||
		strings.HasPrefix(value, "-") ||
		strings.HasPrefix(value, "/") ||
		strings.Contains(value, "..") ||
		!utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func workbenchImplementationPosition(
	content []byte,
	offset int,
) (int32, int32, bool) {
	if offset < 0 || offset > len(content) {
		return 0, 0, false
	}
	var line int32
	var character int32
	for _, value := range content[:offset] {
		if value == '\n' {
			line++
			character = 0
		} else {
			character++
		}
	}
	return line, character, true
}

func workbenchImplementationCommit(
	commit phebssync.GitCommit,
) WorkbenchImplementationCommit {
	subject := commit.Subject
	truncated := false
	if len(subject) > 4096 {
		subject = subject[:4096]
		truncated = true
	}
	parents := slices.Clone(commit.ParentIDs)
	slices.Sort(parents)
	if len(parents) > 16 {
		parents = parents[:16]
	}
	return WorkbenchImplementationCommit{
		ID:               commit.ID,
		ParentIDs:        parents,
		Subject:          subject,
		SubjectTruncated: truncated,
		AuthorName:       workbenchImplementationBoundedString(commit.Author.Name, 1024),
		AuthorTime:       commit.Author.Time.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func workbenchImplementationDiff(
	diff phebssync.DiffResult,
) WorkbenchImplementationDiff {
	patch := diff.Patch
	truncated := diff.Truncated
	if len(patch) > workbenchImplementationDiffExcerpt {
		patch = patch[:workbenchImplementationDiffExcerpt]
		truncated = true
	}
	files := slices.Clone(diff.Files)
	sort.Slice(files, func(i, j int) bool {
		left, right := files[i], files[j]
		return left.Path+"|"+left.OldPath+"|"+left.Status <
			right.Path+"|"+right.OldPath+"|"+right.Status
	})
	filesTruncated := len(files) > workbenchImplementationMaxDiffFiles
	if filesTruncated {
		files = files[:workbenchImplementationMaxDiffFiles]
	}
	return WorkbenchImplementationDiff{
		Base:           diff.Base,
		Head:           diff.Head,
		Digest:         workbenchImplementationBytesDigest([]byte(diff.Patch)),
		PatchExcerpt:   patch,
		PatchTruncated: truncated,
		Files:          files,
		FilesTruncated: filesTruncated,
	}
}

func workbenchImplementationBytesDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func workbenchImplementationSelectionKey(
	selection store.ChangeBriefContractSelection,
) string {
	return string(selection.Role) + "|" +
		selection.Protocol + "|" +
		selection.Repository + "|" +
		selection.DeclarationLineage + "|" +
		selection.CanonicalOperation
}

func workbenchImplementationRelationshipKey(
	relationship ContractCatalogRelationship,
) string {
	return relationship.Kind + "|" +
		relationship.Classification + "|" +
		relationship.Reason + "|" +
		relationship.Claim.AssertionID
}

func workbenchImplementationCatalogDigest(
	selection store.ChangeBriefContractSelection,
	detail *ContractCatalogOperation,
) string {
	type claim struct {
		AssertionID      string                  `json:"assertion_id"`
		RunID            string                  `json:"run_id"`
		Predicate        string                  `json:"predicate"`
		Object           string                  `json:"object"`
		Lineage          string                  `json:"lineage"`
		Tier             string                  `json:"tier"`
		CodeRole         string                  `json:"code_role"`
		Sources          []ContractCatalogSource `json:"sources"`
		SourcesTruncated bool                    `json:"sources_truncated"`
	}
	normalizeClaim := func(value ContractCatalogClaim) claim {
		sources := slices.Clone(value.Sources)
		sort.Slice(sources, func(i, j int) bool {
			return workbenchImplementationCatalogSourceKey(sources[i]) <
				workbenchImplementationCatalogSourceKey(sources[j])
		})
		return claim{
			AssertionID:      value.AssertionID,
			RunID:            value.RunID,
			Predicate:        value.Predicate,
			Object:           value.Object,
			Lineage:          value.Lineage,
			Tier:             value.Tier,
			CodeRole:         value.CodeRole,
			Sources:          sources,
			SourcesTruncated: value.SourcesTruncated,
		}
	}
	type implementation struct {
		Classification string `json:"classification"`
		Reason         string `json:"reason"`
		Claim          claim  `json:"claim"`
	}
	implementations := make([]implementation, 0, len(detail.Implementations))
	for _, relationship := range detail.Implementations {
		if relationship.Kind != "implementation" {
			continue
		}
		implementations = append(implementations, implementation{
			Classification: relationship.Classification,
			Reason:         relationship.Reason,
			Claim:          normalizeClaim(relationship.Claim),
		})
	}
	sort.Slice(implementations, func(i, j int) bool {
		return digestJSON(implementations[i]) < digestJSON(implementations[j])
	})
	return digestJSON(struct {
		Selection              store.ChangeBriefContractSelection `json:"selection"`
		ServiceFQN             string                             `json:"service_fqn"`
		Method                 string                             `json:"method"`
		Declaration            claim                              `json:"declaration"`
		Implementations        []implementation                   `json:"implementations"`
		RelationshipsTruncated bool                               `json:"relationships_truncated"`
		CoverageDigest         string                             `json:"coverage_digest"`
	}{
		Selection:              selection,
		ServiceFQN:             detail.ServiceFQN,
		Method:                 detail.Method,
		Declaration:            normalizeClaim(detail.Declaration),
		Implementations:        implementations,
		RelationshipsTruncated: detail.RelationshipsTruncated,
		CoverageDigest:         detail.CoverageDigest,
	})
}

func workbenchImplementationCatalogSourceKey(
	source ContractCatalogSource,
) string {
	return fmt.Sprintf(
		"%s|%s|%s|%010d|%010d|%s",
		source.Repository,
		source.Commit,
		source.Path,
		source.StartByte,
		source.EndByte,
		source.AssertionID,
	)
}

func workbenchImplementationAnchorKey(
	anchor WorkbenchImplementationAnchor,
) string {
	return fmt.Sprintf(
		"%s|%s|%s|%010d|%010d|%s",
		anchor.Repository,
		anchor.Commit,
		anchor.Path,
		anchor.Line,
		anchor.Character,
		anchor.Encoding,
	)
}

func workbenchImplementationSeedKey(seed workbenchImplementationSeed) string {
	return seed.kind + "|" +
		seed.selectionRule + "|" +
		seed.selectionInput + "|" +
		workbenchImplementationFileKey(seed.source) + "|" +
		fmt.Sprintf(
			"%010d|%010d|%010d|%010d",
			seed.source.StartByte,
			seed.source.EndByte,
			seed.source.StartLine,
			seed.source.EndLine,
		)
}

func workbenchImplementationFileKey(
	source WorkbenchImplementationSourceCitation,
) string {
	return source.Repository + "|" + source.Commit + "|" + source.Path
}

func workbenchImplementationSearchFileKey(file search.FileResult) string {
	return file.Repo + "|" + file.Ref + "|" + file.Path
}

func workbenchImplementationSearchRange(
	file search.FileResult,
) (search.Range, bool) {
	ranges := make([]search.Range, 0)
	for _, chunk := range file.Chunks {
		ranges = append(ranges, chunk.Ranges...)
	}
	if len(ranges) == 0 {
		return search.Range{}, false
	}
	sort.Slice(ranges, func(i, j int) bool {
		left, right := ranges[i], ranges[j]
		if left.StartLine != right.StartLine {
			return left.StartLine < right.StartLine
		}
		if left.StartCol != right.StartCol {
			return left.StartCol < right.StartCol
		}
		if left.EndLine != right.EndLine {
			return left.EndLine < right.EndLine
		}
		return left.EndCol < right.EndCol
	})
	return ranges[0], true
}

func workbenchImplementationLocationKey(location codenav.Location) string {
	return fmt.Sprintf(
		"%s|%s|%s|%010d|%010d|%010d|%010d",
		location.Repo,
		location.Revision,
		location.Path,
		location.Range.Start.Line,
		location.Range.Start.Character,
		location.Range.End.Line,
		location.Range.End.Character,
	)
}

func workbenchImplementationRowKey(row WorkbenchImplementationRow) string {
	return row.Kind + "|" +
		row.CodeRole + "|" +
		workbenchImplementationFileKey(row.Source) + "|" +
		fmt.Sprintf(
			"%010d|%010d|%010d|%010d|",
			row.Source.StartLine,
			row.Source.StartColumn,
			row.Source.EndLine,
			row.Source.EndColumn,
		) +
		row.SelectionRule + "|" +
		row.SelectionInput + "|" +
		row.Symbol + "|" +
		row.ID
}

func canonicalWorkbenchImplementationStrings(values []string) []string {
	values = slices.Clone(values)
	slices.Sort(values)
	return slices.Compact(values)
}

func workbenchImplementationBoundedString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func workbenchImplementationCapabilityRank(state string) int {
	switch state {
	case "enabled":
		return 0
	case "unsupported":
		return 1
	case "failed":
		return 2
	default:
		return 3
	}
}

func decodeWorkbenchImplementationCursor(
	raw,
	queryDigest,
	principal,
	revisionDigest,
	visibilityDigest,
	snapshotDigest string,
) (*workbenchImplementationCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > workbenchImplementationCursorLimit {
		return nil, huma.Error400BadRequest(
			"related implementation evidence cursor is too large",
		)
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, huma.Error400BadRequest(
			"invalid related implementation evidence cursor",
			err,
		)
	}
	var cursor workbenchImplementationCursor
	if len(data) > workbenchImplementationCursorLimit ||
		json.Unmarshal(data, &cursor) != nil {
		return nil, huma.Error400BadRequest(
			"invalid related implementation evidence cursor",
		)
	}
	checksum := cursor.Checksum
	cursor.Checksum = ""
	if checksum == "" || checksum != digestJSON(cursor) {
		return nil, huma.Error400BadRequest(
			"invalid related implementation evidence cursor checksum",
		)
	}
	cursor.Checksum = checksum
	if cursor.Schema != workbenchImplementationCursorVersion ||
		cursor.QueryDigest != queryDigest ||
		cursor.Principal != principal ||
		cursor.RevisionDigest != revisionDigest {
		return nil, huma.Error400BadRequest(
			"related implementation evidence cursor does not match this request",
		)
	}
	if cursor.VisibilityDigest != visibilityDigest ||
		cursor.SnapshotDigest != snapshotDigest {
		return nil, huma.Error409Conflict(
			"related implementation evidence changed since the prior page; restart pagination",
		)
	}
	return &cursor, nil
}

func encodeWorkbenchImplementationCursor(
	cursor workbenchImplementationCursor,
) (string, error) {
	cursor.Checksum = ""
	cursor.Checksum = digestJSON(cursor)
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", huma.Error500InternalServerError(
			"encode related implementation evidence cursor",
			err,
		)
	}
	if len(data) > workbenchImplementationCursorLimit {
		return "", huma.Error500InternalServerError(
			"related implementation evidence cursor exceeded its limit",
		)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
