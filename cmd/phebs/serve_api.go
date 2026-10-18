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
	"sort"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/text/unicode/norm"

	documentation "github.com/bmeddeb/phebs"
	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/config"
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
	codeNavigationOptions := codenav.Options{
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
	}
	if d.typedRuntime != nil {
		codeNavigationOptions.RoutedResolver = d.typedRuntime.resolver
	}
	codeNavigation := codenav.New(codeNavigationOptions)
	d.codeNavigation = codeNavigation
	return nil
}

// experimentalProofBundles returns the proof-bundle store only while a
// provisional extraction switch is on. The proof service behind it serves the
// experimental evidence products (proof bundles, Contract Impact, Thrift-field
// references, Kafka topic usage and compatibility) on HTTP and MCP, and those
// switches are their only admission. A released recipe admits its own product
// and never these, so a released Caller Map alone leaves them dark. Without a
// released pack this is exactly the store the extraction pipeline installed.
func experimentalProofBundles(cfg *config.Config, proofBundles store.ProofBundleStore) store.ProofBundleStore {
	if provisionalExtractionOn(cfg) {
		return proofBundles
	}
	return nil
}

// admittedProofBundles is the proof-bundle store for the experimental proof
// service, or for released Kafka planes, which serve only topic usage through
// it (T55.2/T56.2).
func admittedProofBundles(cfg *config.Config, released map[string]bool, proofBundles store.ProofBundleStore) store.ProofBundleStore {
	if bundles := experimentalProofBundles(cfg, proofBundles); bundles != nil || len(releasedKafkaPlanes(cfg, released)) == 0 && !released["scip-proto-field"] && !released["scip-thrift-field"] {
		return bundles
	}
	return proofBundles
}

// releasedKafkaPlanes names the Kafka planes a proof service is admitted for
// solely through released recipes; the provisional switches keep the full
// experimental service, so they return none.
func releasedKafkaPlanes(cfg *config.Config, released map[string]bool) []string {
	if provisionalExtractionOn(cfg) {
		return nil
	}
	var planes []string
	for _, plane := range []string{"consumer", "producer"} {
		if released["kafka-"+plane] {
			planes = append(planes, plane)
		}
	}
	return planes
}

// releasedCatalogDomains scopes a release-only Contract Atlas to the domains
// verified records admit (T49.3), so an unreleased consumer or caller domain
// is named unavailable rather than reported unpublished. The provisional
// switches keep the whole experimental catalog.
func releasedCatalogDomains(cfg *config.Config, released map[string]bool) []string {
	if provisionalExtractionOn(cfg) || len(released) == 0 {
		return nil
	}
	domains := make([]string, 0, len(released))
	for domain := range released {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains
}

// releasedKafkaTopics admits the composed inventory beside the released planes
// only when a verified phebs.kafka.topics record selects it and both planes are
// themselves released: the combined claim measured nothing else. Revoking
// either plane withdraws Topics too, and that withdrawal is logged once.
func releasedKafkaTopics(claims map[string]bool, planes []string) bool {
	if !claims[kafkaTopicsPackID] {
		return false
	}
	if len(planes) != 2 {
		log.Printf("release selection: %s withdrawn: its combined claim needs both Kafka planes released, have %v",
			kafkaTopicsPackID, planes)
		return false
	}
	return true
}

func releasedCallerComparison(claims, domains map[string]bool) bool {
	if !claims[callerComparisonPackID] {
		return false
	}
	if !domains["proto-contract"] || !domains["grpc-caller"] {
		log.Printf("release selection: %s withdrawn: its comparison needs the released protobuf Caller Map recipe", callerComparisonPackID)
		return false
	}
	return true
}

func releasedOperationImpact(d *serveDeps) bool {
	if !d.releasedClaims[operationImpactPackID] {
		return false
	}
	if d.releasedDomains["proto-contract"] && d.releasedDomains["grpc-caller"] {
		declaration, caller := false, false
		for _, ex := range d.exs {
			declaration = declaration || ex.Domain() == "proto-contract" && ex.Version() == "4.0.0"
			caller = caller || ex.Domain() == "grpc-caller" && ex.Version() == "1.6.0-released.1"
		}
		if declaration && caller {
			return true
		}
	}
	log.Printf("release selection: %s withdrawn: operation Impact needs the current released protobuf Caller Map recipe", operationImpactPackID)
	return false
}

// admittedWireCompatibility admits the standalone Buf WIRE verdict (T53.2)
// under the same dark switches as the proof-enriched check, or ordinarily when
// a verified released record selects its claim (T53.3). A released claim never
// admits the proof service, Impact or field evidence.
func admittedWireCompatibility(cfg *config.Config, released map[string]bool, checker compat.Service) compat.Service {
	if provisionalExtractionOn(cfg) || released[wireCompatibilityPackID] {
		return checker
	}
	return nil
}

func provisionalExtractionOn(cfg *config.Config) bool {
	experimental := cfg.Experimental
	return experimental.ProvisionalProtoExtraction || experimental.ProvisionalThriftExtraction ||
		experimental.ProvisionalThriftFieldExtraction || experimental.ProvisionalKafkaExtraction
}

// newServeAPIOptions assembles the API options: search/code-navigation
// services, fixture bindings, and catalog/caller/relationship services.
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
	var typedAdmitted func(store.TypedIndexOperator) bool
	var typedProvider func(string) bool
	if d.typedRuntime != nil && d.typedRuntime.registry != nil {
		typedAdmitted = d.typedRuntime.registry.admits
		typedProvider = d.typedRuntime.registry.provider
	}
	comparisonReleased := releasedCallerComparison(d.releasedClaims, d.releasedDomains)
	apiOpts := api.Options{
		Version:                     version,
		TypedIndexAvailable:         d.typedRuntime != nil,
		TypedIndexAdmitted:          typedAdmitted,
		TypedIndexProviderAvailable: typedProvider,
		Store:                       st, Search: d.searcher, ScopedSearch: d.runtimeScopedSearch,
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
		Evidence: d.evidenceView, ProofBundles: admittedProofBundles(cfg, d.releasedDomains, d.proofBundles),
		KafkaReleasedPlanes:             releasedKafkaPlanes(cfg, d.releasedDomains),
		ProtobufFieldReferencesReleased: !provisionalExtractionOn(cfg) && d.releasedDomains["scip-proto-field"],
		ThriftFieldReferencesReleased:   !provisionalExtractionOn(cfg) && d.releasedDomains["scip-thrift-field"],
		EvidenceExtractorVersions:       releasedEvidenceVersions(d),
		KafkaTopicInventoryReleased: releasedKafkaTopics(
			d.releasedClaims, releasedKafkaPlanes(cfg, d.releasedDomains)),
		CatalogReleasedDomains: releasedCatalogDomains(cfg, d.releasedDomains),
		ContractCatalogUnadmitted: !provisionalExtractionOn(cfg) &&
			!d.releasedDomains["proto-contract"] && !d.releasedDomains["thrift-contract"],
		CallerMapEnabled: d.callerRegistry.Enabled(),
		CallerComparisonEnabled: comparisonReleased || cfg.Experimental.ProvisionalProtoExtraction ||
			cfg.Experimental.ProvisionalThriftExtraction,
		CallerComparisonReleased: comparisonReleased,
		OperationImpactEnabled:   releasedOperationImpact(d),
		CallerReader:             d.callerReader,
		ProofBundleRetention:     cfg.ProofBundles.RetentionFor(),
		Compatibility:            d.compatibility, Visible: d.visibleFor,
		WireCompatibility: admittedWireCompatibility(cfg, d.releasedClaims, d.compatibility),
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
		AuthorizationProvider: func() string {
			if cfg.Permissions != nil {
				return "phebs-permissions-v1"
			}
			return "unfiltered-v1"
		}(),
		WebhookSecret: cfg.Webhook.Secret, ResyncConnections: resyncNames,
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
	apiOpts.OperationImpact = api.NewOperationImpactService(apiOpts)
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

func releasedEvidenceVersions(d *serveDeps) map[string]string {
	var versions map[string]string
	for _, extractor := range d.exs {
		domain := extractor.Domain()
		if (domain == "scip-proto-field" || domain == "scip-thrift-field") && d.releasedDomains[domain] {
			if versions == nil {
				versions = map[string]string{}
			}
			versions[domain] = extractor.Version()
		}
	}
	return versions
}

// newServeMCPServer assembles the read-only MCP tool registry from the API
// options, so MCP advertises exactly the proof, compatibility, Contract Atlas,
// Caller Map and comparison services the API constructed.
func newServeMCPServer(d *serveDeps, apiOpts api.Options) *mcpsdk.Server {
	var operationImpact phebsmcp.OperationImpactQueries
	if apiOpts.OperationImpact != nil {
		operationImpact = apiOpts.OperationImpact
	}
	var mcpProofs phebsmcp.ProofQueries
	var mcpCompatibility phebsmcp.CompatibilityQueries
	var mcpTopicInventory phebsmcp.TopicInventoryQueries
	if proofService := api.NewProofService(apiOpts); proofService != nil {
		mcpProofs = proofService
		if proofService.CompatibilityAvailable() && !apiOpts.ProtobufFieldReferencesReleased && !apiOpts.ThriftFieldReferencesReleased && len(apiOpts.KafkaReleasedPlanes) == 0 {
			mcpCompatibility = proofService
		}
		// The composed Topics inventory is admitted on exactly the boundary its
		// HTTP routes use: the experimental proof surface, or released planes
		// beside the released T57.3 Topics claim. Released planes alone keep
		// listing only find_kafka_topic_usage.
		if !apiOpts.ProtobufFieldReferencesReleased && !apiOpts.ThriftFieldReferencesReleased && len(apiOpts.KafkaReleasedPlanes) == 0 || apiOpts.KafkaTopicInventoryReleased {
			mcpTopicInventory = proofService
		}
	}
	catalogQueries, callerMapQueries, comparisonQueries := mcpCallerMapServices(
		apiOpts.ContractCatalog, apiOpts.CallerMap, apiOpts.CallerComparison,
	)
	// T8.2/T9.1: MCP accepts the same DB-backed API keys as the HTTP API.
	// It advertises one read-only tool registry for every authenticated caller.
	mcpOpts := phebsmcp.Options{
		Version: version, Store: d.st, Search: d.searcher, ScopedSearch: d.runtimeScopedSearch,
		DataDir: d.cfg.Server.DataDir,
		CodeNav: d.codeNavigation, Visible: d.visibleFor, Proofs: mcpProofs,
		Compatibility:               mcpCompatibility,
		KafkaTopicUsageOnly:         len(apiOpts.KafkaReleasedPlanes) > 0,
		ProtobufFieldReferencesOnly: apiOpts.ProtobufFieldReferencesReleased,
		ThriftFieldReferencesOnly:   apiOpts.ThriftFieldReferencesReleased,
		KafkaTopicInventoryReleased: apiOpts.KafkaTopicInventoryReleased,
		WireCompatibility:           apiOpts.WireCompatibility,
		ContractCatalog:             catalogQueries,
		CallerMap:                   callerMapQueries,
		CallerComparison:            comparisonQueries,
		OperationImpact:             operationImpact,
		ServiceDirectory:            apiOpts.ServiceDirectory,
		ObservationProgress:         apiOpts.ObservationProgress,
		Relationships:               apiOpts.Relationships,
		TopicInventory:              mcpTopicInventory,
	}
	return phebsmcp.NewServer(mcpOpts)
}

// newServeHTTPHandlers assembles the API, MCP, and UI handlers into the final
// HTTP handler chain.
func newServeHTTPHandlers(d *serveDeps, apiOpts api.Options, finalAuthority, tailReadiness t421ExactFinalAuthorityRead) (http.Handler, error) {
	apiHandler := api.New(apiOpts)
	mcpServer := newServeMCPServer(d, apiOpts)
	// Stateless (T10.3): in stateful mode every tool call runs with the
	// session INITIATOR's context, so one user's session smears their
	// permissions onto whoever posts to it (the SDK's hijack guard is inert
	// without its own auth package). Stateless makes each POST carry its own
	// authenticated principal; phebs tools are plain request/response, so
	// nothing is lost.
	var mcpHandler http.Handler = mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return mcpServer },
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

	docsHandler, err := documentation.NewHandler()
	if err != nil {
		return nil, err
	}
	uiHandler := documentationAndUI(d.authService, docsHandler, http.FileServerFS(d.dist))
	handler := t422OwnerHTTPHandler(d.startup.Owners(), newHTTPHandler(d.authService, apiHandler, mcpHandler, promhttp.Handler(), uiHandler, d.cfg.Server), d.semanticLaunch)
	return handler, nil
}
