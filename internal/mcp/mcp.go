// Package mcp exposes phebs to agents over the Model Context Protocol
// (T8.2, PLAN P4 — the MCP-first product layer). Official go-sdk; tools map
// 1:1 onto the existing search/read/store internals.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/investigation"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

// ProofQueries is the T14.1 query core projected through MCP. The interface
// keeps the transport adapter unable to recreate evidence or permission
// semantics independently of the HTTP API.
type ProofQueries interface {
	FindOperationConsumersMCP(ctx context.Context, operation string) (*investigation.Envelope, error)
	FindProtoFieldReferencesMCP(ctx context.Context, lineage, message string, fieldNumber int) (*investigation.Envelope, error)
	FindFieldReferencesMCP(ctx context.Context, lineage, message string, fieldNumber int) (*investigation.Envelope, error)
	FindKafkaTopicUsageMCP(ctx context.Context, topic string) (*investigation.Envelope, error)
	GetExtractionCoverageMCP(ctx context.Context, domains []string) (*investigation.Envelope, error)
}

// CompatibilityQueries is separate from the three always-present proof
// queries so a missing Buf sandbox cannot advertise a placeholder tool.
type CompatibilityQueries interface {
	CheckContractCompatibilityMCP(ctx context.Context, request compat.Request) (*investigation.Envelope, error)
}

type Options struct {
	Version string
	Store   store.Store
	Search  *search.Searcher          // nil = search_code reports unavailable
	DataDir string                    // bare mirrors for read_file
	CodeNav *codenav.Service          // nil = SCIP tools report unavailable
	History *phebssync.HistoryService // nil = construct from DataDir
	// Proofs enables the proof-backed evidence tools. Nil leaves them
	// unregistered so the provisional extraction feature stays dark.
	Proofs ProofQueries
	// Compatibility enables T14.3 only after the pinned Buf checker and its
	// host sandbox have initialized successfully.
	Compatibility CompatibilityQueries
	// ContractCatalog and CallerMap enable T20.11's three read-only discovery
	// tools only as one complete annex. CallerComparison adds T20.13's fourth
	// workflow tool only when the same annex and shared comparison service are
	// present. These are the same instances used by Huma.
	ContractCatalog  ContractCatalogQueries
	CallerMap        CallerMapQueries
	CallerComparison CallerComparisonQueries
	// ServiceDirectory is the same T33.4 store-backed service supplied to
	// Huma. Nil keeps the tools undiscoverable.
	ServiceDirectory ServiceDirectoryQueries
	// ObservationProgress is the same T36.4 authorization-first service used
	// by Huma. Nil keeps the tool undiscoverable.
	ObservationProgress ObservationProgressQueries
	// Relationships is the same bounded T37.5 service supplied to Huma.
	Relationships RelationshipQueries
	// Workbench and WorkbenchChecklist enable T21.13's synthetic/dark MCP
	// annex only when both real shared services are present. WorkbenchImpact
	// adds T38.4's exact read-only impact projection when the same service is
	// available to Huma. Principal is the same authenticated identity
	// projection used by Huma.
	Workbench          store.InvestigationWorkbench
	WorkbenchImpact    WorkbenchImpactQueries
	WorkbenchChecklist WorkbenchChecklistQueries
	Principal          func(context.Context) string
	// InvestigationMutation rechecks that the current MCP request carries a
	// valid named API key with investigation:write. It never replaces the
	// shared services' owner, revision, preview, snapshot, or idempotency
	// checks. AdvertiseWorkbenchMutations is selected per authenticated
	// stateless HTTP request so credentials that cannot invoke durable writes
	// do not discover them.
	InvestigationMutation       func(context.Context) bool
	AdvertiseWorkbenchMutations bool
	// Visible resolves the caller's repo visibility (T10.3); nil disables
	// permission filtering. search_code is covered inside the searcher; this
	// hook gates the tools that bypass it (read_file, list_repos, SCIP,
	// history). Requires per-request principals — serve runs the streamable
	// handler stateless so tool contexts carry the current caller.
	Visible func(ctx context.Context) func(store.Repo) bool
}

// maxFileBytes caps read_file output: a whole-file dump larger than this
// wastes an agent's context window; ranged reads cover the rest.
const maxFileBytes = 200_000

// NewServer builds the phebs MCP server over search, immutable source reads,
// SCIP navigation, and bounded Git history plumbing.
func NewServer(opts Options) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "phebs", Version: opts.Version}, nil)
	history := opts.History
	if history == nil {
		history = phebssync.NewHistoryService(opts.DataDir)
	}

	type searchIn struct {
		Query        string `json:"query" jsonschema:"zoekt query syntax: plain terms AND together and patterns are regex; filters include repo: file: lang: sym: case:yes content: plus phebs' archived:/fork:/public:yes|no and context:<name> (named repo set); prefix any atom with - to negate"`
		MaxMatches   int    `json:"max_matches,omitempty" jsonschema:"maximum files returned; default 50, cap 500"`
		ContextLines int    `json:"context_lines,omitempty" jsonschema:"lines of context around each match; default 0, cap 10"`
		Scope        string `json:"scope,omitempty" jsonschema:"all_code (default) or service"`
		Repository   string `json:"repository,omitempty" jsonschema:"exact repository required for service scope"`
		ServiceKey   string `json:"service_key,omitempty" jsonschema:"repository-local key required for service scope"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "search_code",
		Description: "Search All code or one exact service scope. Returns matched files, line-numbered chunks, and a digest-bound scope receipt; service scope includes accepted shared paths and excludes unowned paths.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in searchIn) (*sdk.CallToolResult, search.Result, error) {
		if opts.Search == nil {
			return nil, search.Result{}, errors.New("search unavailable: no index open")
		}
		res, err := opts.Search.SearchScoped(ctx, search.ScopeSelector{
			Kind: in.Scope, Repository: in.Repository, ServiceKey: in.ServiceKey,
		}, in.Query,
			search.Options{MaxMatches: in.MaxMatches, ContextLines: in.ContextLines})
		if err != nil {
			return nil, search.Result{}, err
		}
		return nil, *res, nil
	})

	type readIn struct {
		Repo      string `json:"repo" jsonschema:"full repo name as returned by list_repos or search_code, e.g. github.com/acme/api"`
		Path      string `json:"path" jsonschema:"file path within the repo"`
		Ref       string `json:"ref,omitempty" jsonschema:"commit-ish; defaults to the repository's indexed commit"`
		StartLine int    `json:"start_line,omitempty" jsonschema:"1-based first line to return; default 1"`
		EndLine   int    `json:"end_line,omitempty" jsonschema:"1-based last line to return, inclusive; default end of file"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "read_file",
		Description: "Read a file (or a line range of it) from an indexed repository at its indexed revision.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in readIn) (*sdk.CallToolResult, readOut, error) {
		ref, err := indexedRevision(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, readOut{}, err
		}
		content, err := phebssync.CatFile(ctx, opts.DataDir, in.Repo, ref, in.Path)
		if err != nil {
			return nil, readOut{}, err
		}
		if !utf8.Valid(content) {
			return nil, readOut{}, fmt.Errorf("%s is a binary file (%d bytes)", in.Path, len(content))
		}
		return nil, sliceLines(string(content), in.StartLine, in.EndLine), nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "list_repos",
		Description: "List every indexed repository with its metadata and active analysis-unit scope (name, exact primary/supporting paths, search posture, and typed-index posture when configured).",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, reposOut, error) {
		repos, err := opts.Store.ListRepos(ctx)
		if err != nil {
			return nil, reposOut{}, err
		}
		allow := repoFilter(ctx, opts)
		out := reposOut{Repos: make([]repoInfo, 0, len(repos))}
		for _, r := range repos {
			if r.Deleting || r.IndexedCommitHash == "" {
				continue
			}
			if allow != nil && !allow(r) {
				continue
			}
			out.Repos = append(out.Repos, repoInfo{
				Name: r.Name, DefaultBranch: r.DefaultBranch, WebURL: r.WebURL,
				IsPublic: r.IsPublic, IsFork: r.IsFork, IsArchived: r.IsArchived,
				IndexedAt:    r.IndexedAt,
				AnalysisUnit: analysisunit.CloneState(r.IndexedAnalysisUnit),
			})
		}
		return nil, out, nil
	})

	registerCodeNavigationTools(s, opts)
	registerHistoryTools(s, opts, history)
	registerProofTools(s, opts)
	registerCallerMapTools(s, opts)
	registerCallerComparisonTool(s, opts)
	registerServiceDirectoryTools(s, opts)
	registerObservationProgressTool(s, opts)
	registerRelationshipTools(s, opts)
	registerWorkbenchTools(s, opts)

	return s
}

func registerProofTools(s *sdk.Server, opts Options) {
	if opts.Proofs == nil {
		return
	}

	type operationIn struct {
		Operation string `json:"operation" jsonschema:"canonical fully-qualified RPC operation /scope.Service/method (gRPC or Thrift)"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:         "find_operation_consumers",
		Description:  "Find matching static call evidence for one bare canonical RPC operation across the registered consumer domains (gRPC and Thrift). This operation-name query does not establish declaration identity or a known-caller roster. Returns envelope v1.0 with permission-scoped facts, extractor abstentions, exact proof references, separate coverage dimensions, and server-rendered qualifications; it cannot establish absence or safety.",
		OutputSchema: proofEnvelopeSchema("mcp-payload-contract-edges-v1.0.json"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in operationIn) (*sdk.CallToolResult, investigation.Envelope, error) {
		result, err := opts.Proofs.FindOperationConsumersMCP(ctx, in.Operation)
		if err != nil {
			return nil, investigation.Envelope{}, err
		}
		return nil, *result, nil
	})

	type fieldIn struct {
		Lineage     string `json:"lineage" jsonschema:"canonical contract lineage id from extracted protobuf/SCIP evidence"`
		Message     string `json:"message" jsonschema:"fully-qualified protobuf message name, e.g. shop.Cart"`
		FieldNumber int    `json:"field_number" jsonschema:"protobuf wire field number, 1 through 536870911 excluding 19000 through 19999"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:         "find_proto_field_references",
		Description:  "Find source references to one protobuf field identity (lineage, message, field number). Returns envelope v1.0 with permission-scoped facts and separate semantic, attribution, and processing states; field names are versioned attributes, not identity.",
		OutputSchema: proofEnvelopeSchema("mcp-payload-contract-edges-v1.0.json"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in fieldIn) (*sdk.CallToolResult, investigation.Envelope, error) {
		result, err := opts.Proofs.FindProtoFieldReferencesMCP(ctx, in.Lineage, in.Message, in.FieldNumber)
		if err != nil {
			return nil, investigation.Envelope{}, err
		}
		return nil, *result, nil
	})

	type neutralFieldIn struct {
		Lineage     string `json:"lineage" jsonschema:"canonical contract SCIP package lineage id from extracted protobuf or Thrift field evidence"`
		Message     string `json:"message" jsonschema:"protocol-qualified message name, e.g. shop.Cart"`
		FieldNumber *int   `json:"field_number" jsonschema:"wire field number admitted by at least one registered field-reference pack; Thrift field 0 is valid"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:         "find_field_references",
		Description:  "Find protocol-qualified source references to one neutral field identity (lineage, message, field number) across every registered field-reference pack whose number bounds admit it. Returns envelope v1.0 with exact citations and separate semantic, attribution, and processing states; it makes no completeness or accuracy claim.",
		OutputSchema: proofEnvelopeSchema("mcp-payload-contract-edges-v1.0.json"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in neutralFieldIn) (*sdk.CallToolResult, investigation.Envelope, error) {
		if in.FieldNumber == nil {
			return nil, investigation.Envelope{}, errors.New(
				"field_number is required",
			)
		}
		result, err := opts.Proofs.FindFieldReferencesMCP(
			ctx,
			in.Lineage,
			in.Message,
			*in.FieldNumber,
		)
		if err != nil {
			return nil, investigation.Envelope{}, err
		}
		return nil, *result, nil
	})

	type topicIn struct {
		Topic string `json:"topic" jsonschema:"one Kafka topic spelling, 1 through 249 characters of [a-zA-Z0-9._-]"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:         "find_kafka_topic_usage",
		Description:  "Find evidence-derived producers and consumers of one Kafka topic spelling. Returns envelope v1.0 with permission-scoped facts and exact proof references. The topic is a source spelling with no cluster or environment identity; production Kafka topics are overwhelmingly configuration-driven, so unresolved sites dominate by design — they are counted per shape class in the persisted bundle's census, and this list is never a completeness claim.",
		OutputSchema: proofEnvelopeSchema("mcp-payload-contract-edges-v1.0.json"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in topicIn) (*sdk.CallToolResult, investigation.Envelope, error) {
		result, err := opts.Proofs.FindKafkaTopicUsageMCP(ctx, in.Topic)
		if err != nil {
			return nil, investigation.Envelope{}, err
		}
		return nil, *result, nil
	})

	type coverageIn struct {
		Domains []string `json:"domains,omitempty" jsonschema:"extractor domains; omit for grpc-caller, grpc-consumer, kafka-consumer, kafka-producer, proto-contract, scip-proto-field, scip-thrift-field, thrift-caller, thrift-consumer, and thrift-contract"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:         "get_extraction_coverage",
		Description:  "Return envelope v1.0 containing the immutable extraction coverage certificate for the caller's complete visible repository universe. Pack-defined eligible-unit counts remain withheld when they were not computed.",
		OutputSchema: proofEnvelopeSchema("mcp-payload-coverage-v1.0.json"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in coverageIn) (*sdk.CallToolResult, investigation.Envelope, error) {
		result, err := opts.Proofs.GetExtractionCoverageMCP(ctx, in.Domains)
		if err != nil {
			return nil, investigation.Envelope{}, err
		}
		return nil, *result, nil
	})

	if opts.Compatibility == nil {
		return
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:         "check_contract_compatibility",
		Description:  "Run the pinned Buf WIRE policy over bounded before/after protobuf source sets, then return envelope v1.0 with the derived conclusion, affected field-reference evidence, exact proof references, coverage, and provenance.",
		OutputSchema: proofEnvelopeSchema("mcp-payload-contract-edges-v1.0.json"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in compat.Request) (*sdk.CallToolResult, investigation.Envelope, error) {
		result, err := opts.Compatibility.CheckContractCompatibilityMCP(ctx, in)
		if err != nil {
			return nil, investigation.Envelope{}, err
		}
		return nil, *result, nil
	})
}

func proofEnvelopeSchema(payloadFilename string) map[string]any {
	schema, err := investigation.EnvelopeOutputSchema(payloadFilename)
	if err != nil {
		panic(err)
	}
	return schema
}

type positionIn struct {
	Repo      string `json:"repo" jsonschema:"full repository name as returned by list_repos"`
	Path      string `json:"path" jsonschema:"source file path within the repository"`
	Ref       string `json:"ref,omitempty" jsonschema:"full indexed commit object ID; defaults to the repository's indexed commit"`
	Line      int32  `json:"line" jsonschema:"zero-based source line"`
	Character int32  `json:"character" jsonschema:"zero-based UTF-16 code-unit offset from line start"`
}

func registerCodeNavigationTools(s *sdk.Server, opts Options) {
	query := func(ctx context.Context, in positionIn) (codenav.Query, error) {
		if opts.CodeNav == nil {
			return codenav.Query{}, errors.New("code navigation unavailable: no SCIP service configured")
		}
		ref, err := indexedRevision(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return codenav.Query{}, err
		}
		return codenav.Query{
			Repo: in.Repo, Revision: ref, Path: in.Path,
			Line: in.Line, Character: in.Character, Encoding: codenav.EncodingUTF16,
		}, nil
	}

	sdk.AddTool(s, &sdk.Tool{
		Name:        "find_definitions",
		Description: "Find the precise SCIP definition for the symbol at a zero-based UTF-16 source position. Returns available=false when the indexed revision has no committed SCIP index.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in positionIn) (*sdk.CallToolResult, codenav.DefinitionResult, error) {
		q, err := query(ctx, in)
		if err != nil {
			return nil, codenav.DefinitionResult{}, err
		}
		result, err := opts.CodeNav.Definition(ctx, q)
		return nil, result, err
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "find_references",
		Description: "Find precise SCIP references for the symbol at a zero-based UTF-16 source position. Returned ranges use zero-based UTF-16 positions.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in positionIn) (*sdk.CallToolResult, codenav.ReferencesResult, error) {
		q, err := query(ctx, in)
		if err != nil {
			return nil, codenav.ReferencesResult{}, err
		}
		result, err := opts.CodeNav.References(ctx, q)
		return nil, result, err
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "hover",
		Description: "Return SCIP signature, documentation, and symbol metadata at a zero-based UTF-16 source position.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in positionIn) (*sdk.CallToolResult, codenav.HoverResult, error) {
		q, err := query(ctx, in)
		if err != nil {
			return nil, codenav.HoverResult{}, err
		}
		result, err := opts.CodeNav.Hover(ctx, q)
		return nil, result, err
	})
}

func registerHistoryTools(s *sdk.Server, opts Options, history *phebssync.HistoryService) {
	type blameIn struct {
		Repo string `json:"repo" jsonschema:"full repository name as returned by list_repos"`
		Path string `json:"path" jsonschema:"file path within the repository"`
		Ref  string `json:"ref,omitempty" jsonschema:"commit-ish; defaults to the repository's indexed commit"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "blame",
		Description: "Attribute each source line to a commit at an immutable revision, following moves and renames. Large results report truncated=true.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in blameIn) (*sdk.CallToolResult, phebssync.BlameResult, error) {
		ref, err := indexedRevision(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, phebssync.BlameResult{}, err
		}
		result, err := history.Blame(ctx, phebssync.BlameRequest{Repo: in.Repo, Ref: ref, Path: in.Path})
		return nil, result, err
	})

	type commitsIn struct {
		Repo   string `json:"repo" jsonschema:"full repository name as returned by list_repos"`
		Ref    string `json:"ref,omitempty" jsonschema:"commit-ish; defaults to the repository's indexed commit"`
		Path   string `json:"path,omitempty" jsonschema:"optional file path; history follows renames"`
		Limit  int    `json:"limit,omitempty" jsonschema:"commits per page; default 50, cap 200"`
		Offset int    `json:"offset,omitempty" jsonschema:"zero-based commit offset, cap 10000"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "list_commits",
		Description: "List commits reachable from an immutable revision, optionally scoped to a file and followed across renames.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in commitsIn) (*sdk.CallToolResult, phebssync.CommitListResult, error) {
		ref, err := indexedRevision(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, phebssync.CommitListResult{}, err
		}
		result, err := history.Commits(ctx, phebssync.CommitListRequest{
			Repo: in.Repo, Ref: ref, Path: in.Path, Limit: in.Limit, Offset: in.Offset,
		})
		return nil, result, err
	})

	type commitIn struct {
		Repo string `json:"repo" jsonschema:"full repository name as returned by list_repos"`
		Ref  string `json:"ref,omitempty" jsonschema:"commit-ish; defaults to the repository's indexed commit"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "get_commit",
		Description: "Get commit metadata, parents, and first-parent file changes for one immutable revision.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in commitIn) (*sdk.CallToolResult, phebssync.CommitResult, error) {
		ref, err := indexedRevision(ctx, opts, in.Repo, in.Ref)
		if err != nil {
			return nil, phebssync.CommitResult{}, err
		}
		result, err := history.Commit(ctx, phebssync.CommitRequest{Repo: in.Repo, Ref: ref})
		return nil, result, err
	})

	type diffIn struct {
		Repo         string `json:"repo" jsonschema:"full repository name as returned by list_repos"`
		Head         string `json:"head,omitempty" jsonschema:"head commit-ish; defaults to the repository's indexed commit"`
		Base         string `json:"base,omitempty" jsonschema:"base commit-ish; defaults to head's first parent"`
		Path         string `json:"path,omitempty" jsonschema:"optional exact path filter"`
		ContextLines *int   `json:"context_lines,omitempty" jsonschema:"unified context lines; default 3, maximum 20"`
	}
	sdk.AddTool(s, &sdk.Tool{
		Name:        "diff",
		Description: "Return a bounded unified diff and structured file statistics. Binary files are summarized and large patches report truncated=true.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in diffIn) (*sdk.CallToolResult, phebssync.DiffResult, error) {
		head, err := indexedRevision(ctx, opts, in.Repo, in.Head)
		if err != nil {
			return nil, phebssync.DiffResult{}, err
		}
		request := phebssync.DiffRequest{Repo: in.Repo, Base: in.Base, Head: head, Path: in.Path}
		if in.ContextLines != nil {
			request.ContextLines = *in.ContextLines
			request.ContextLinesSet = true
		}
		result, err := history.Diff(ctx, request)
		return nil, result, err
	})
}

func indexedRevision(ctx context.Context, opts Options, repoName, requested string) (string, error) {
	if opts.Store == nil {
		return "", errors.New("repository store unavailable")
	}
	repo, err := opts.Store.GetRepo(ctx, repoName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", fmt.Errorf("unknown repo %q (use list_repos for names)", repoName)
		}
		return "", err
	}
	// T10.3: a permission denial reads exactly like a missing repo —
	// disclosing existence would itself be a leak.
	if allow := repoFilter(ctx, opts); allow != nil && !allow(*repo) {
		return "", fmt.Errorf("unknown repo %q (use list_repos for names)", repoName)
	}
	if repo.Deleting {
		return "", fmt.Errorf("repo %q is being deleted", repoName)
	}
	if repo.IndexedCommitHash == "" {
		return "", fmt.Errorf("repo %q has no indexed revision yet", repoName)
	}
	if requested != "" {
		return requested, nil
	}
	return repo.IndexedCommitHash, nil
}

// repoFilter resolves the request's visibility predicate; nil = everything.
func repoFilter(ctx context.Context, opts Options) func(store.Repo) bool {
	if opts.Visible == nil {
		return nil
	}
	return opts.Visible(ctx)
}

// reposOut is list_repos' result shape.
type reposOut struct {
	Repos []repoInfo `json:"repos"`
}

type repoInfo struct {
	Name          string              `json:"name"`
	DefaultBranch string              `json:"default_branch,omitempty"`
	WebURL        string              `json:"web_url,omitempty"`
	IsPublic      bool                `json:"is_public"`
	IsFork        bool                `json:"is_fork"`
	IsArchived    bool                `json:"is_archived"`
	IndexedAt     *time.Time          `json:"indexed_at,omitempty"`
	AnalysisUnit  *analysisunit.State `json:"analysis_unit,omitempty"`
}

// readOut is read_file's result shape.
type readOut struct {
	Content   string `json:"content"`
	StartLine int    `json:"start_line"`          // first line of Content, 1-based
	EndLine   int    `json:"end_line"`            // last line of Content, inclusive
	TotalLine int    `json:"total_lines"`         // lines in the whole file
	Truncated bool   `json:"truncated,omitempty"` // cut at the size cap; re-request with start_line/end_line
}

// sliceLines applies an optional 1-based inclusive line range, then a size
// cap (T8.3: a multi-MB dump only wastes agent context — the Truncated flag
// points at ranged re-reads). Whole lines are kept while they fit; a single
// line that alone exceeds the cap is byte-truncated (UTF-8-safe) so progress
// is always made — the pre-fix code let the first line through uncapped.
func sliceLines(content string, start, end int) readOut {
	if content == "" {
		return readOut{TotalLine: 0} // empty file: zero lines, not one
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	total := len(lines)
	if start < 1 {
		start = 1
	}
	if start > total {
		start = total
	}
	if end < start || end > total {
		end = total
	}

	var b strings.Builder
	last := start - 1 // index one past the final included line
	truncated := false
	for i := start - 1; i < end; i++ {
		line := lines[i]
		sep := 0
		if b.Len() > 0 {
			sep = 1 // the '\n' joining this line to the previous
		}
		if b.Len()+sep+len(line) > maxFileBytes {
			if b.Len() == 0 { // first line alone overflows: deliver a safe prefix
				b.WriteString(truncateUTF8(line, maxFileBytes))
				last = i + 1
			}
			truncated = true
			break
		}
		if sep == 1 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		last = i + 1
	}
	return readOut{
		Content:   b.String(),
		StartLine: start,
		EndLine:   last,
		TotalLine: total,
		Truncated: truncated,
	}
}

// truncateUTF8 cuts s to at most max bytes without splitting a rune.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
