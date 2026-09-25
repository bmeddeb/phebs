package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/text/unicode/norm"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	phebsmcp "github.com/bmeddeb/phebs/internal/mcp"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/retentionstatus"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/ui"
)

// newServeVisibility builds the permission-aware repository visibility
// predicate (T10.3).
func newServeVisibility(d *serveDeps) {
	cfg := d.cfg
	st := d.st
	// T10.3: permission-aware visibility. Presence of the permissions block
	// enables enforcement; the closure resolves one request's predicate (nil
	// for administrators). Non-admins see public repos, repos their mapped
	// code-host identities grant, and always_visible matches — resolution
	// fails closed to public+always_visible on any error.
	var visibleFor func(ctx context.Context) func(store.Repo) bool
	if cfg.Permissions != nil {
		perms := cfg.Permissions
		idsByEmail := make(map[string][]string, len(perms.Users))
		for email, ids := range perms.Users {
			// same normalization pipeline as auth's NormalizedEmail, or a
			// non-NFC Unicode config key would silently never match
			key := strings.ToLower(norm.NFC.String(email))
			for _, id := range ids {
				idsByEmail[key] = append(idsByEmail[key], strings.ToLower(id))
			}
		}
		alwaysVisible := func(name string) bool {
			for _, pat := range perms.AlwaysVisible {
				if ok, _ := path.Match(pat, name); ok {
					return true
				}
			}
			return false
		}
		visibleFor = func(ctx context.Context) func(store.Repo) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			if ok && principal.IsAdmin {
				return nil // administrators see everything
			}
			granted := map[string]bool{}
			if ok && principal.User != nil {
				if ids := idsByEmail[principal.User.NormalizedEmail]; len(ids) > 0 {
					names, err := st.ListPermittedRepos(ctx, ids)
					if err != nil {
						log.Printf("resolve permitted repos: %v (failing closed)", err)
					}
					for _, name := range names {
						granted[name] = true
					}
				}
			}
			return func(r store.Repo) bool {
				return r.IsPublic || granted[r.Name] || alwaysVisible(r.Name)
			}
		}
	}
	d.visibleFor = visibleFor
}

// openServeSearcher opens the zoekt searcher with generation pins and wires
// usage reporting, visibility, and code navigation. The searcher close and
// the trailing stopBackground are deferred through d in their original order.
func openServeSearcher(d *serveDeps) error {
	cfg := d.cfg
	st := d.st

	dist, err := ui.FS()
	if err != nil {
		return err
	}
	d.dist = dist
	indexDir := filepath.Join(cfg.Server.DataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return fmt.Errorf("create index dir: %w", err)
	}
	d.indexDir = indexDir
	searcher, err := search.OpenWithGenerationPins(indexDir, st, d.searchGenerationPins)
	if err != nil {
		return err
	}
	d.searcher = searcher
	reportT4013Startup("searcher_ready")
	d.deferFunc(func(*error) { searcher.Close() })
	// Registered after Close so workers stop before the searcher's deferred
	// close; stopBackground is idempotent and its earlier defer still protects
	// startup failures before the searcher exists.
	d.deferFunc(func(*error) { d.stopBackground() })
	searcher.Contexts = cfg.Contexts // T8.1: context:<name> filters
	serviceCatalogV3Cache := servicecatalogv3.NewDefaultReadCache()
	serviceStateV3Reader, err := store.NewServiceStateV3Reader(st, serviceCatalogV3Cache)
	if err != nil {
		return fmt.Errorf("configure selected service state reader: %w", err)
	}
	d.serviceStateV3Reader = serviceStateV3Reader
	runtimeScopedSearch, err := search.NewRuntimeScopedSearcher(searcher, serviceStateV3Reader)
	if err != nil {
		return fmt.Errorf("configure selected service search: %w", err)
	}
	d.runtimeScopedSearch = runtimeScopedSearch
	// T10.2: one usage event per completed search (REST, SSE, and MCP all
	// funnel through the searcher). Local only — phebs never phones home.
	searcher.Usage = func(ctx context.Context, event store.UsageEvent) {
		if principal, ok := auth.PrincipalFromContext(ctx); ok {
			if principal.User != nil {
				event.ActorID = principal.User.ID
			}
			event.APIKeyID = principal.APIKeyID
		}
		if err := st.RecordUsageEvent(context.WithoutCancel(ctx), event); err != nil {
			log.Printf("usage event: %v", err)
		}
	}
	searcher.Visible = d.visibleFor // T10.3: the per-user RepoSet pre-pass
	codeNavigation := codenav.New(codenav.Options{
		DataDir: cfg.Server.DataDir,
		BindingResolver: codenav.TypedIndexResolveFunc(
			func(ctx context.Context, repository, revision string) (codenav.TypedIndexBinding, error) {
				repo, err := st.GetRepo(ctx, repository)
				if err != nil {
					return codenav.TypedIndexBinding{},
						codeNavigationRepositoryError(repository, err)
				}
				if repo == nil || repo.Deleting || repo.IndexedCommitHash == "" {
					return codenav.TypedIndexBinding{}, fmt.Errorf(
						"repository %q has no current indexed revision: %w",
						repository, codenav.ErrRevisionNotFound,
					)
				}
				if repo.IndexedAnalysisUnit != nil &&
					!codeNavigationRevisionAdmitted(*repo, revision) {
					return codenav.TypedIndexBinding{}, fmt.Errorf(
						"revision %q is outside the committed focused generation: %w",
						revision, codenav.ErrRevisionNotFound,
					)
				}
				return codenav.BindingFromAnalysisUnit(
					repository, revision, repo.IndexedAnalysisUnit,
				)
			},
		),
	})
	d.codeNavigation = codeNavigation
	return nil
}

// newServeAPIOptions assembles the API options: search/code-navigation
// services, fixture bindings, catalog/caller/relationship services, and the
// provisional/synthetic Workbench binding.
func newServeAPIOptions(d *serveDeps) (api.Options, error) {
	cfg := d.cfg
	st := d.st
	semanticLaunch := d.semanticLaunch

	// repository-membership webhook events re-sync every remote connection
	var resyncNames []string
	for _, c := range cfg.Connections {
		if phebssync.IsRemote(c) {
			resyncNames = append(resyncNames, c.Name)
		}
	}
	retentionStatus := retentionstatus.New(cfg.Server.DataDir, st)
	apiOpts := api.Options{
		Version: version,
		Store:   st, Search: d.searcher, ScopedSearch: d.runtimeScopedSearch,
		DataDir: cfg.Server.DataDir,
		CodeNav: d.codeNavigation,
		RetentionStatusSource: api.NewCompleteRetentionStatusSource(
			st, retentionStatus, nil,
		),
		LifecycleStatusSource: func(context.Context) lifecycle.Status {
			return d.lifecycleStatus.Snapshot()
		},
		SelectedLifecycleCleanup: semanticLaunch != nil && (semanticLaunch.request.ServerEpoch == 4 || semanticLaunch.request.ServerEpoch == 5),
		IsAdmin: func(ctx context.Context) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			return ok && principal.IsAdmin
		},
		AuditRecord: d.auditRecord, AuditLog: st, Analytics: st,
		Evidence: d.evidenceView, ProofBundles: d.proofBundles,
		CallerMapEnabled: cfg.Experimental.ProvisionalProtoExtraction ||
			cfg.Experimental.ProvisionalThriftExtraction,
		CallerReader:         d.callerReader,
		ProofBundleRetention: cfg.ProofBundles.RetentionFor(),
		Compatibility:        d.compatibility, Visible: d.visibleFor,
		Principal: func(ctx context.Context) string {
			principal, ok := auth.PrincipalFromContext(ctx)
			if !ok {
				return ""
			}
			if principal.User != nil {
				return "user:" + principal.User.ID
			}
			if principal.APIKeyID != "" {
				return "api-key:" + principal.APIKeyID
			}
			return "authenticated:" + principal.AuthMethod
		},
		InvestigationMutation: func(ctx context.Context) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			if !ok {
				return false
			}
			if principal.AuthMethod == "session" {
				return true
			}
			return principal.HasAPIKeyCapability(
				store.APIKeyCapabilityInvestigationWrite,
			)
		},
		AuthorizationProvider: func() string {
			if cfg.Permissions != nil {
				return "phebs-permissions-v1"
			}
			return "unfiltered-v1"
		}(),
		WebhookSecret: cfg.Webhook.Secret, ResyncConnections: resyncNames,
	}
	if fixtureDir := strings.TrimSpace(os.Getenv("PHEBS_INVESTIGATION_FIXTURES")); fixtureDir != "" {
		fixtureViews, err := api.NewInvestigationFixtureViews(fixtureDir)
		if err != nil {
			return api.Options{}, fmt.Errorf("load synthetic Investigation views: %w", err)
		}
		apiOpts.InvestigationViews = fixtureViews
		log.Printf("WARNING: synthetic Investigation fixture views enabled from %s; not production evidence", fixtureDir)
	}
	if fixturePath := strings.TrimSpace(os.Getenv("PHEBS_CONTRACT_ATLAS_FIXTURE")); fixturePath != "" {
		fixture, err := api.LoadContractCatalogFixture(fixturePath)
		if err != nil {
			return api.Options{}, fmt.Errorf("load synthetic Contract Atlas fixture: %w", err)
		}
		apiOpts.ContractCatalogFixture = fixture
		log.Printf("WARNING: synthetic Contract Atlas fixture enabled from %s; not production evidence", fixturePath)
	}
	apiOpts.ContractCatalog = api.NewContractCatalogService(apiOpts)
	apiOpts.CallerMap = api.NewCallerMapService(apiOpts)
	apiOpts.CallerComparison = api.NewCallerComparisonService(apiOpts)
	apiOpts.ServiceDirectory = api.NewRuntimeServiceDirectoryService(
		apiOpts, d.serviceStateV3Reader,
	)
	apiOpts.ObservationProgress = api.NewObservationProgressService(
		apiOpts,
		&observationpublication.ProgressReader{
			DataDir: cfg.Server.DataDir, Store: st, Cache: d.observationCache,
			InventoryV2: true,
		},
	)
	apiOpts.ExtractionProgress = api.NewExtractionProgressService(apiOpts, d.partitionRuntime)
	if d.relationshipRuntime != nil {
		apiOpts.Relationships = api.NewRuntimeRelationshipService(
			apiOpts, d.relationshipCache, d.relationshipV3Cache,
		)
		if apiOpts.Relationships == nil {
			return api.Options{}, errors.New("configure exact relationship readers")
		}
	}
	syntheticWorkbenchSetting := os.Getenv("PHEBS_SYNTHETIC_WORKBENCH")
	workbenchMode := ""
	if cfg.Experimental.ProvisionalWorkbench {
		var provisionalWorkbench store.InvestigationWorkbench
		if syntheticWorkbenchSetting == "" && apiOpts.ContractCatalogFixture == nil {
			if resolver := api.NewWorkbenchTargetResolver(apiOpts); resolver != nil {
				provisionalWorkbench = store.InvestigationWorkbenchService{
					Store: st, Resolver: resolver, Compatibility: d.compatibility,
				}
			}
		}
		if err := bindProvisionalWorkbench(
			&apiOpts,
			cfg.Experimental.ProvisionalProtoExtraction ||
				cfg.Experimental.ProvisionalThriftExtraction,
			syntheticWorkbenchSetting,
			provisionalWorkbench,
		); err != nil {
			return api.Options{}, err
		}
		workbenchMode = "provisional"
	} else {
		var syntheticWorkbench store.InvestigationWorkbench
		if syntheticWorkbenchSetting != "" {
			if resolver := api.NewWorkbenchTargetResolver(apiOpts); resolver != nil {
				syntheticWorkbench = store.InvestigationWorkbenchService{
					Store: st, Resolver: resolver, Compatibility: d.compatibility,
				}
			}
		}
		if err := bindSyntheticWorkbench(
			&apiOpts,
			syntheticWorkbenchSetting,
			syntheticWorkbench,
		); err != nil {
			return api.Options{}, err
		}
		if apiOpts.Workbench != nil {
			workbenchMode = "synthetic"
		}
	}
	if apiOpts.Workbench != nil {
		apiOpts.WorkbenchImpact = api.NewWorkbenchImpactService(apiOpts)
		apiOpts.WorkbenchImplementation =
			api.NewWorkbenchImplementationService(apiOpts)
		apiOpts.WorkbenchChecklist =
			api.NewWorkbenchChecklistService(apiOpts)
		if apiOpts.WorkbenchImpact == nil ||
			apiOpts.WorkbenchImplementation == nil ||
			apiOpts.WorkbenchChecklist == nil {
			return api.Options{}, fmt.Errorf(
				"%s Workbench evidence services are unavailable",
				workbenchMode,
			)
		}
		switch workbenchMode {
		case "provisional":
			log.Printf("WARNING: provisional Change Workbench enabled over store-derived Contract Atlas evidence; not a production or continuation surface")
		case "synthetic":
			log.Printf("WARNING: synthetic Change Workbench enabled for make dev; not a production or continuation surface")
		}
	}
	return apiOpts, nil
}

// newServeFinalAuthorityReads builds the T42.1 exact-mode final-authority and
// tail-readiness readers.
func newServeFinalAuthorityReads(d *serveDeps) (t421ExactFinalAuthorityRead, t421ExactFinalAuthorityRead, error) {
	var finalAuthority t421ExactFinalAuthorityRead
	var tailReadiness t421ExactFinalAuthorityRead
	if !d.exactReads {
		return finalAuthority, tailReadiness, nil
	}
	cfg := d.cfg
	st := d.st
	repository, err := t421FinalAuthorityRepository(cfg.ServiceCatalogs)
	if err != nil {
		return finalAuthority, tailReadiness, err
	}
	if _, focused := d.analysisUnits[repository]; focused {
		return finalAuthority, tailReadiness, errors.New("T42.1 exact mode requires whole-repository authority")
	}
	if d.manifestProvider == nil || d.openPartitionDomain == nil || d.callerReader == nil {
		return finalAuthority, tailReadiness, errors.New("T42.1 exact mode requires the complete extraction runtime")
	}
	policies, err := extract.CandidatePolicies(d.exs)
	if err != nil {
		return finalAuthority, tailReadiness, fmt.Errorf("configure T42.1 exact candidate policies: %w", err)
	}
	reader, err := newT421FinalAuthorityReader(
		repository, cfg.Server.DataDir, d.indexDir, st, d.searchGenerationPins,
		policies,
		func(readCtx context.Context) (candidate.State, error) {
			return d.manifestProvider.CurrentPublicationState(readCtx, repository, nil)
		},
		d.openPartitionDomain, d.relationshipV3Cache, d.callerReader, d.visibleFor,
	)
	if err != nil {
		return finalAuthority, tailReadiness, fmt.Errorf("configure T42.1 final authority reader: %w", err)
	}
	finalAuthority = t421ExactFinalAuthorityRead{
		Limits: t421FinalAuthorityReadLimits(), Read: reader.Read,
	}
	reader.stale = d.staleControl
	reader.checkpoint = d.checkpointControl
	reader.checkpointRecovery = d.checkpointRecovery
	reader.selectorCleanup = d.exact.state.selectorCleanup
	tailReadiness = t421ExactFinalAuthorityRead{
		Limits: t421TailReadinessLimits(), Read: reader.ReadTailReadiness,
	}
	return finalAuthority, tailReadiness, nil
}

// newServeHTTPHandlers assembles the API, MCP, and UI handlers into the final
// HTTP handler chain.
func newServeHTTPHandlers(d *serveDeps, apiOpts api.Options, finalAuthority, tailReadiness t421ExactFinalAuthorityRead) (http.Handler, error) {
	apiHandler := api.New(apiOpts)
	var mcpProofs phebsmcp.ProofQueries
	var mcpCompatibility phebsmcp.CompatibilityQueries
	if proofService := api.NewProofService(apiOpts); proofService != nil {
		mcpProofs = proofService
		if proofService.CompatibilityAvailable() {
			mcpCompatibility = proofService
		}
	}
	catalogQueries, callerMapQueries, comparisonQueries := mcpCallerMapServices(
		apiOpts.ContractCatalog, apiOpts.CallerMap, apiOpts.CallerComparison,
	)
	// T8.2/T9.1: MCP accepts the same DB-backed API keys as the HTTP API.
	// T21.13 builds two immutable registries over the same services. Stateless
	// request authentication selects the write registry only for a current
	// named key carrying investigation:write; handlers recheck that predicate
	// before every preview-bound or durable mutation call.
	mcpOpts := phebsmcp.Options{
		Version: version, Store: d.st, Search: d.searcher, ScopedSearch: d.runtimeScopedSearch,
		DataDir: d.cfg.Server.DataDir,
		CodeNav: d.codeNavigation, Visible: d.visibleFor, Proofs: mcpProofs,
		Compatibility:         mcpCompatibility,
		ContractCatalog:       catalogQueries,
		CallerMap:             callerMapQueries,
		CallerComparison:      comparisonQueries,
		ServiceDirectory:      apiOpts.ServiceDirectory,
		ObservationProgress:   apiOpts.ObservationProgress,
		Relationships:         apiOpts.Relationships,
		Workbench:             apiOpts.Workbench,
		WorkbenchImpact:       apiOpts.WorkbenchImpact,
		WorkbenchChecklist:    apiOpts.WorkbenchChecklist,
		Principal:             apiOpts.Principal,
		InvestigationMutation: mcpInvestigationMutation,
	}
	mcpReadServer := phebsmcp.NewServer(mcpOpts)
	mcpOpts.AdvertiseWorkbenchMutations = true
	mcpWriteServer := phebsmcp.NewServer(mcpOpts)
	// Stateless (T10.3): in stateful mode every tool call runs with the
	// session INITIATOR's context, so one user's session smears their
	// permissions onto whoever posts to it (the SDK's hijack guard is inert
	// without its own auth package). Stateless makes each POST carry its own
	// authenticated principal; phebs tools are plain request/response, so
	// nothing is lost.
	var mcpHandler http.Handler = mcpsdk.NewStreamableHTTPHandler(
		func(request *http.Request) *mcpsdk.Server {
			if mcpInvestigationMutation(request.Context()) {
				return mcpWriteServer
			}
			return mcpReadServer
		},
		&mcpsdk.StreamableHTTPOptions{Stateless: true},
	)
	if d.exact.state != nil {
		if d.semanticLaunch != nil && d.semanticLaunch.request.SearchWarm != "" {
			warm, warmErr := newT422SearchWarmControl(d.ctx, d.semanticLaunch, d.searcher)
			if warmErr != nil {
				return nil, warmErr
			}
			d.exact.state.searchWarm = warm
		}
		if err := d.exact.state.bindFinalReaders(finalAuthority, tailReadiness); err != nil {
			return nil, err
		}
		apiHandler, mcpHandler = d.exact.state.wrap(apiHandler), d.exact.state.wrap(mcpHandler)
	}

	handler := t422OwnerHTTPHandler(d.startup.Owners(), newHTTPHandler(d.authService, apiHandler, mcpHandler, promhttp.Handler(), http.FileServerFS(d.dist), d.cfg.Server), d.semanticLaunch)
	return handler, nil
}
