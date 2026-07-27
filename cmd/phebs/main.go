// Command phebs is the self-hosted code-search server: API, UI, sync, and
// indexing in one binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/text/unicode/norm"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/scipfield"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftfield"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftgo"
	"github.com/bmeddeb/phebs/internal/indexer"
	phebsmcp "github.com/bmeddeb/phebs/internal/mcp"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/ui"
)

var version = "0.1.0-dev" // ponytail: ldflags stamping when releases exist

const (
	evidenceSweepIdleInterval    = time.Hour
	evidenceSweepBacklogDelay    = 5 * time.Second
	evidenceSweepMaxStepsPerPass = 64
	evidenceStagedMaxAge         = 24 * time.Hour
	proofSweepMaxBundlesPerPass  = 8
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "backup":
		err = backup(os.Args[2:])
	case "restore":
		err = restore(os.Args[2:])
	case "version":
		err = printVersion(os.Args[2:], os.Stdout)
	default:
		printUsage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  phebs serve [-config phebs.yaml] [-addr 127.0.0.1:3070]")
	fmt.Fprintln(os.Stderr, "  phebs backup [-config phebs.yaml] -output /path/to/backup")
	fmt.Fprintln(os.Stderr, "  phebs restore [-config phebs.yaml] -backup /path/to/backup")
	fmt.Fprintln(os.Stderr, "  phebs version")
}

func printVersion(args []string, output io.Writer) error {
	if len(args) != 0 {
		return errors.New("version accepts no arguments")
	}
	if _, err := fmt.Fprintln(output, version); err != nil {
		return fmt.Errorf("print version: %w", err)
	}
	return nil
}

func backup(args []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	cfgPath := flags.String("config", "", "path to config file (defaults apply if omitted)")
	output := flags.String("output", "", "new backup directory (must not exist)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" {
		return errors.New("backup requires -output and accepts no positional arguments")
	}
	cfg, raw, err := loadRecoveryConfig(*cfgPath)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	manifest, err := recovery.Create(ctx, recovery.BackupOptions{
		Options: recovery.Options{
			DataDir: cfg.Server.DataDir, Config: raw, PhebsVersion: version,
		},
		Output: *output,
	})
	if err != nil {
		return err
	}
	fmt.Printf("backup published: %s (%s)\n", *output, manifest.ManifestSHA256)
	return nil
}

func restore(args []string) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	cfgPath := flags.String("config", "", "path to config file (defaults apply if omitted)")
	backupPath := flags.String("backup", "", "backup directory to verify and import")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *backupPath == "" {
		return errors.New("restore requires -backup and accepts no positional arguments")
	}
	cfg, raw, err := loadRecoveryConfig(*cfgPath)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	manifest, err := recovery.Restore(ctx, recovery.RestoreOptions{
		Options: recovery.Options{
			DataDir: cfg.Server.DataDir, Config: raw, PhebsVersion: version,
		},
		Backup: *backupPath,
	})
	if err != nil {
		return err
	}
	fmt.Printf("restore verified and imported: %s\n", manifest.ManifestSHA256)
	return nil
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := flags.String("config", "", "path to config file (defaults apply if omitted)")
	addr := flags.String("addr", "", "listen address (overrides config)")
	_ = flags.Parse(args)

	cfg, rawConfig, err := loadServerConfig(*cfgPath)
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.Server.Addr = *addr
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := os.MkdirAll(cfg.Server.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	st, err := store.OpenLocalWithConfig(ctx, cfg.Server.DataDir, recovery.ConfigDigest(rawConfig))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(context.Background()) }()

	// Every service-owned goroutine is joined before the store is closed.
	// Calling cancel in this later-registered defer also covers startup and
	// ListenAndServe failures, not just signal-driven shutdown.
	var background sync.WaitGroup
	runBackground := func(run func()) {
		background.Add(1)
		go func() {
			defer background.Done()
			run()
		}()
	}
	var stopBackgroundOnce sync.Once
	stopBackground := func() {
		stopBackgroundOnce.Do(func() {
			cancel()
			background.Wait()
		})
	}
	defer stopBackground()

	// T10.1: one audit recorder feeds the auth surface and the huma middleware.
	// The actor comes from the request principal when the caller did not
	// already resolve it; recording failures never fail the request.
	auditRecord := func(ctx context.Context, event store.AuditEvent) {
		if principal, ok := auth.PrincipalFromContext(ctx); ok {
			if event.ActorID == "" && principal.User != nil {
				event.ActorID, event.ActorEmail = principal.User.ID, principal.User.Email
			}
			if event.APIKeyID == "" {
				event.APIKeyID = principal.APIKeyID
			}
			if event.AuthMethod == "" {
				event.AuthMethod = principal.AuthMethod
			}
		}
		// The action already completed; a client disconnect must not lose it.
		if err := st.AppendAuditEvent(context.WithoutCancel(ctx), event); err != nil {
			log.Printf("audit: %v", err)
		}
	}

	authService, err := auth.New(ctx, auth.Options{Config: cfg.Auth, Store: st, Audit: auditRecord})
	if err != nil {
		return err
	}
	// T10.1/T10.2 retention sweep: boot, then twice a day
	auditRetention, usageRetention := cfg.Audit.RetentionFor(), cfg.Analytics.RetentionFor()
	if auditRetention > 0 || usageRetention > 0 {
		runBackground(func() {
			ticker := time.NewTicker(12 * time.Hour)
			defer ticker.Stop()
			for {
				sweep := func(name string, keep time.Duration, prune func(context.Context, time.Time) (int, error)) {
					if keep <= 0 {
						return
					}
					if n, err := prune(ctx, time.Now().UTC().Add(-keep)); err != nil {
						log.Printf("%s retention: %v", name, err)
					} else if n > 0 {
						log.Printf("%s retention: pruned %d event(s)", name, n)
					}
				}
				sweep("audit", auditRetention, st.PruneAuditEvents)
				sweep("analytics", usageRetention, st.PruneUsageEvents)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		})
	}
	if setupToken := authService.SetupToken(); setupToken != "" {
		log.Printf("first-run setup token: %s", setupToken)
	}

	// sync pipeline: prune membership of dropped connections, enqueue boot
	// syncs, run one jittered poller
	names := make([]string, 0, len(cfg.Connections))
	for _, c := range cfg.Connections {
		names = append(names, c.Name)
	}
	if err := st.PruneConnections(ctx, names); err != nil {
		return fmt.Errorf("prune connections: %w", err)
	}
	report, reconcileErr := phebssync.ReconcileArtifacts(ctx, st, cfg.Server.DataDir, cfg.Sync.CleanupOrphans)
	if reconcileErr != nil {
		// Reconciliation establishes the artifact/search trust boundary. A
		// failed quarantine, revision clear, or credential scrub must not leave
		// the server running against state it could not prove safe.
		return fmt.Errorf("artifact reconciliation: %w", reconcileErr)
	}
	if report.OrphanRepos+report.UntrackedShards+report.UntrackedMirrors+report.CredentialsFixed+report.InvalidRepos+report.RevisionRepairs > 0 {
		log.Printf("artifact reconciliation: orphans=%d shards=%d mirrors=%d credentials_scrubbed=%d invalid_repos=%d revision_repairs=%d deleted=%d",
			report.OrphanRepos, report.UntrackedShards, report.UntrackedMirrors, report.CredentialsFixed,
			report.InvalidRepos, report.RevisionRepairs, report.Deleted)
	}
	if err := phebssync.EnqueueMissing(ctx, st, cfg); err != nil {
		return fmt.Errorf("enqueue sync jobs: %w", err)
	}
	runner := &store.Runner{Store: st, Kind: store.JobSync, Handle: phebssync.Handler(cfg, st),
		Interval: cfg.Sync.Interval()}
	runBackground(func() { runner.Run(ctx) })
	fetchRunner := &store.Runner{Store: st, Kind: store.JobFetch, Handle: phebssync.FetchHandler(cfg, st),
		Interval: cfg.Sync.Interval()}
	runBackground(func() { fetchRunner.Run(ctx) })
	if watched := phebssync.Watched(cfg); len(watched) > 0 {
		log.Printf("watch mode: polling %d local repo(s)", len(watched))
		runBackground(func() { (&phebssync.Watcher{Store: st, Conns: watched, Revisions: cfg.Revisions}).Run(ctx) })
	}
	// T7.5: periodic freshness for remote connections
	if every := cfg.Sync.ResyncEvery(); every > 0 {
		runBackground(func() { phebssync.Resync(ctx, st, cfg, every) })
	}

	// Extraction is an independent queue consumer: it must drain boot
	// backfill even when this binary cannot start new zoekt index children.
	// One runner processes repositories serially, bounding extraction
	// concurrency and its Git/parser resource use at this integration seam.
	var onIndexed func(context.Context, string, string) error
	var evidenceView store.EvidenceStore
	var proofBundles store.ProofBundleStore
	var compatibility compat.Service
	if exs := evidenceExtractors(
		cfg.Experimental.ProvisionalProtoExtraction,
		cfg.Experimental.ProvisionalThriftExtraction,
		cfg.Experimental.ProvisionalThriftFieldExtraction,
		cfg.Experimental.ProvisionalKafkaExtraction,
	); len(exs) > 0 {
		if cfg.Experimental.ProvisionalProtoExtraction {
			log.Print("WARNING: experimental provisional protobuf extraction enabled; T11.1/T12.3 validation is not established")
		}
		if cfg.Experimental.ProvisionalThriftExtraction {
			log.Print("WARNING: experimental provisional thrift extraction enabled; validation is the T19.1 rule-gate spike only")
		}
		if cfg.Experimental.ProvisionalThriftFieldExtraction {
			log.Print("WARNING: experimental provisional Thrift field extraction enabled; validation is the T22.1 rule-gate spike only")
		}
		if cfg.Experimental.ProvisionalKafkaExtraction {
			log.Print("WARNING: experimental provisional kafka extraction enabled; validation is the T23.1 rule-gate spike only and topic evidence is abstention-dominant by design")
		}
		evidenceView = st
		proofBundles = st
		if bin, err := compat.FindBinary(); err != nil {
			log.Printf("WARNING: pinned buf not found — contract compatibility disabled (make build provides it; or set PHEBS_BUF): %v", err)
		} else if checker, err := compat.New(bin); err != nil {
			log.Printf("WARNING: Buf sandbox unavailable — contract compatibility disabled: %v", err)
		} else if err := checker.Validate(ctx); err != nil {
			log.Printf("WARNING: Buf validation failed — contract compatibility disabled: %v", err)
		} else {
			compatibility = checker
		}
		worker := &extract.Worker{
			Repos: st, Evidence: st,
			NewCorpus:  extract.GitCorpus(cfg.Server.DataDir),
			Extractors: exs,
		}
		if err := enqueueExtractionBackfill(ctx, st); err != nil {
			return err
		}
		exRunner := &store.Runner{Store: st, Kind: store.JobExtract, Handle: worker.Handle,
			Interval: cfg.Sync.Interval()}
		runBackground(func() { exRunner.Run(ctx) })
		onIndexed = func(ctx context.Context, name, commit string) error {
			return enqueueExtractionAfterIndex(ctx, st, name, commit)
		}
	}
	if lifetime := cfg.ProofBundles.RetentionFor(); lifetime > 0 {
		runBackground(func() {
			runProofBundleMaintenance(
				ctx, st, lifetime, evidenceSweepIdleInterval, evidenceSweepBacklogDelay,
			)
		})
	}
	runBackground(func() {
		runEvidenceMaintenance(
			ctx, st, evidenceSweepIdleInterval, evidenceSweepBacklogDelay, evidenceStagedMaxAge,
		)
	})

	// index pipeline: same-SHA zoekt-git-index child consumes indexing_job
	if bin, err := indexer.FindBinary(); err != nil {
		log.Print("WARNING: zoekt-git-index not found — indexing disabled (make build provides it; or set PHEBS_ZOEKT_GIT_INDEX)")
	} else {
		ix := &indexer.Indexer{DataDir: cfg.Server.DataDir, Bin: bin, Store: st, Revisions: cfg.Revisions, OnIndexed: onIndexed}
		ixRunner := &store.Runner{Store: st, Kind: store.JobIndex, Handle: ix.Handle,
			Interval: cfg.Sync.Interval()}
		runBackground(func() { ixRunner.Run(ctx) })
	}

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

	dist, err := ui.FS()
	if err != nil {
		return err
	}
	indexDir := filepath.Join(cfg.Server.DataDir, "index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return fmt.Errorf("create index dir: %w", err)
	}
	searcher, err := search.Open(indexDir, st)
	if err != nil {
		return err
	}
	defer searcher.Close()
	// Registered after Close so workers stop before the searcher's deferred
	// close; stopBackground is idempotent and its earlier defer still protects
	// startup failures before the searcher exists.
	defer stopBackground()
	searcher.Contexts = cfg.Contexts // T8.1: context:<name> filters
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
	searcher.Visible = visibleFor // T10.3: the per-user RepoSet pre-pass
	codeNavigation := codenav.New(codenav.Options{DataDir: cfg.Server.DataDir})

	// repository-membership webhook events re-sync every remote connection
	var resyncNames []string
	for _, c := range cfg.Connections {
		if phebssync.IsRemote(c) {
			resyncNames = append(resyncNames, c.Name)
		}
	}
	apiOpts := api.Options{
		Version: version,
		Store:   st, Search: searcher, DataDir: cfg.Server.DataDir,
		CodeNav: codeNavigation,
		IsAdmin: func(ctx context.Context) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			return ok && principal.IsAdmin
		},
		AuditRecord: auditRecord, AuditLog: st, Analytics: st,
		Evidence: evidenceView, ProofBundles: proofBundles,
		CallerMapEnabled: cfg.Experimental.ProvisionalProtoExtraction ||
			cfg.Experimental.ProvisionalThriftExtraction,
		ProofBundleRetention: cfg.ProofBundles.RetentionFor(),
		Compatibility:        compatibility, Visible: visibleFor,
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
	if fixtureDir := strings.TrimSpace(os.Getenv("PHEBS_INVESTIGATION_FIXTURES")); fixtureDir != "" {
		fixtureViews, err := api.NewInvestigationFixtureViews(fixtureDir)
		if err != nil {
			return fmt.Errorf("load synthetic Investigation views: %w", err)
		}
		apiOpts.InvestigationViews = fixtureViews
		log.Printf("WARNING: synthetic Investigation fixture views enabled from %s; not production evidence", fixtureDir)
	}
	if fixturePath := strings.TrimSpace(os.Getenv("PHEBS_CONTRACT_ATLAS_FIXTURE")); fixturePath != "" {
		fixture, err := api.LoadContractCatalogFixture(fixturePath)
		if err != nil {
			return fmt.Errorf("load synthetic Contract Atlas fixture: %w", err)
		}
		apiOpts.ContractCatalogFixture = fixture
		log.Printf("WARNING: synthetic Contract Atlas fixture enabled from %s; not production evidence", fixturePath)
	}
	apiOpts.ContractCatalog = api.NewContractCatalogService(apiOpts)
	apiOpts.CallerMap = api.NewCallerMapService(apiOpts)
	apiOpts.CallerComparison = api.NewCallerComparisonService(apiOpts)
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
	mcpServer := phebsmcp.NewServer(phebsmcp.Options{
		Version: version, Store: st, Search: searcher, DataDir: cfg.Server.DataDir,
		CodeNav: codeNavigation, Visible: visibleFor, Proofs: mcpProofs,
		Compatibility:    mcpCompatibility,
		ContractCatalog:  catalogQueries,
		CallerMap:        callerMapQueries,
		CallerComparison: comparisonQueries,
	})
	// Stateless (T10.3): in stateful mode every tool call runs with the
	// session INITIATOR's context, so one user's session smears their
	// permissions onto whoever posts to it (the SDK's hijack guard is inert
	// without its own auth package). Stateless makes each POST carry its own
	// authenticated principal; phebs tools are plain request/response, so
	// nothing is lost.
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return mcpServer },
		&mcpsdk.StreamableHTTPOptions{Stateless: true})
	handler := newHTTPHandler(authService, apiHandler, mcpHandler, promhttp.Handler(), http.FileServerFS(dist))

	srv := &http.Server{Addr: cfg.Server.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	shutdownDone := make(chan struct{})
	runBackground(func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	})

	log.Printf("phebs %s listening on %s (data: %s)", version, cfg.Server.Addr, cfg.Server.DataDir)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// ListenAndServe returns as soon as Shutdown STARTS; wait for the drain so
	// in-flight handlers (and their audit/usage writes) finish before the
	// deferred store/searcher Closes run.
	<-shutdownDone
	return nil
}

func newHTTPHandler(authService *auth.Service, apiHandler, mcpHandler, metricsHandler, uiHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	protectedAPI := authService.Require(apiHandler)
	identifiedAPI := authService.Identify(apiHandler)
	mux.Handle("/api/auth/", authService.Handler())
	mux.Handle("/api/mcp", authService.Require(mcpHandler))
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if api.IsAuthenticationExempt(r.URL.Path) {
			if r.URL.Path == "/api/version" {
				identifiedAPI.ServeHTTP(w, r)
			} else {
				apiHandler.ServeHTTP(w, r)
			}
			return
		}
		protectedAPI.ServeHTTP(w, r)
	}))
	mux.Handle("GET /metrics", metricsHandler)
	mux.Handle("/", uiHandler)
	return authService.LoadAndSave(mux)
}

func loadServerConfig(path string) (*config.Config, []byte, error) {
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read config: %w", err)
		}
		cfg, err := config.Parse(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		return cfg, raw, nil
	}
	log.Print("no -config given; using defaults")
	raw := []byte("{}")
	cfg, err := config.Parse(raw)
	return cfg, raw, err
}

func loadRecoveryConfig(path string) (*config.Config, []byte, error) {
	if path != "" {
		return config.LoadForRecovery(path)
	}
	raw := []byte("{}")
	cfg, err := config.Parse(raw)
	return cfg, raw, err
}

// evidenceExtractors is the validation-gated registry. The provisional
// declared-protobuf reader stays absent unless the operator explicitly opts
// in; T11.1/T12.3 do not support default production activation.
// mcpCallerMapServices converts the typed Caller Map service pointers into
// the MCP option interfaces, preserving nilness. Assigning a nil typed
// pointer directly into an interface field produces a NON-nil interface, so
// the annex's all-or-none dark gate would never fire and every dark or
// partial deployment would advertise the Caller Map tools (T20.11-review
// blocker). Nil in, nil interface out — the same discipline as mcpProofs.
func mcpCallerMapServices(
	catalog *api.ContractCatalogService,
	callerMap *api.CallerMapService,
	comparison *api.CallerComparisonService,
) (phebsmcp.ContractCatalogQueries, phebsmcp.CallerMapQueries, phebsmcp.CallerComparisonQueries) {
	var catalogQueries phebsmcp.ContractCatalogQueries
	var callerMapQueries phebsmcp.CallerMapQueries
	var comparisonQueries phebsmcp.CallerComparisonQueries
	if catalog != nil {
		catalogQueries = catalog
	}
	if callerMap != nil {
		callerMapQueries = callerMap
	}
	if comparison != nil {
		comparisonQueries = comparison
	}
	return catalogQueries, callerMapQueries, comparisonQueries
}

func evidenceExtractors(
	provisionalProto, provisionalThrift, provisionalThriftField, provisionalKafka bool,
) []extract.Extractor {
	var extractors []extract.Extractor
	if provisionalProto {
		// T13.1/T13.2 and T20.8 ship behind the same experimental flag.
		// Legacy name-only consumers remain separate from the declaration-
		// proven typed caller domain.
		extractors = append(
			extractors,
			protodecl.New(), grpcgo.New(), scipfield.New(), gocaller.NewGRPC(),
		)
	}
	if provisionalThrift {
		// T19.2/T19.3 and T20.8: the Thrift packs ride their own dark flag;
		// name-only and declaration-proven caller domains stay separate.
		extractors = append(
			extractors, thriftdecl.New(), thriftgo.New(), gocaller.NewThrift(),
		)
	}
	if provisionalThriftField {
		// T22.2 is independently dark: it consumes a committed SCIP index and
		// T22.3 adds Apache tag-bound rows without changing that posture.
		extractors = append(extractors, thriftfield.New())
	}
	if provisionalKafka {
		// T23.2: both Kafka planes ride one dark flag; rule validation is
		// the T23.1 spike and the pack is abstention-dominant by design.
		extractors = append(extractors, kafkago.NewProducer(), kafkago.NewConsumer())
	}
	return extractors
}

func enqueueExtractionAfterIndex(ctx context.Context, st store.Store, repo, commit string) error {
	if err := store.EnqueueUnlessInFlight(ctx, st, store.JobExtract, repo); err != nil {
		return store.WithClass(store.ClassExtract,
			fmt.Errorf("enqueue extraction for %s@%s: %w", repo, commit, err))
	}
	return nil
}

// enqueueExtractionBackfill closes the upgrade gap for repositories indexed
// before extraction existed. The queue provides one pending slot per target,
// so restart and partial-progress retries are idempotent. Any store failure is
// fatal to startup rather than silently leaving a repository unextracted.
func enqueueExtractionBackfill(ctx context.Context, st store.Store) error {
	repos, err := st.ListRepos(ctx)
	if err != nil {
		return fmt.Errorf("backfill extraction jobs: list repositories: %w", err)
	}
	for _, repo := range repos {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("backfill extraction jobs: %w", err)
		}
		if repo.IndexedCommitHash == "" || repo.Deleting {
			continue
		}
		if err := store.EnqueueUnlessInFlight(ctx, st, store.JobExtract, repo.Name); err != nil {
			return fmt.Errorf("backfill extraction job for %s: %w", repo.Name, err)
		}
	}
	return nil
}

// runEvidenceSweepPass reclaims a bounded burst of fixed-size durable steps.
// Hitting the cap is only a backlog signal: the caller yields before starting
// another pass so retention cannot monopolize the database. Logical run
// deletion and physical proof-row deletion remain separately observable.
func runEvidenceSweepPass(
	ctx context.Context, evidence store.EvidenceStore, staleStagedAfter time.Duration,
) (progress store.EvidenceSweepProgress, backlogLikely bool, err error) {
	for range evidenceSweepMaxStepsPerPass {
		if err := ctx.Err(); err != nil {
			return progress, false, err
		}
		step, err := evidence.SweepEvidence(ctx, time.Now().UTC(), staleStagedAfter)
		if err != nil {
			return progress, false, err
		}
		if !step.DidWork() {
			return progress, false, nil
		}
		progress.RunsMarkedDeleting += step.RunsMarkedDeleting
		progress.RunsDeleted += step.RunsDeleted
		progress.AssociationRowsDeleted += step.AssociationRowsDeleted
		progress.AssertionRowsDeleted += step.AssertionRowsDeleted
		progress.AtomRowsDeleted += step.AtomRowsDeleted
		progress.RetentionPhasesAdvanced += step.RetentionPhasesAdvanced
	}
	return progress, true, nil
}

// runProofBundleSweepPass releases a bounded burst of bundle-owned pins. It
// never sweeps the now-unpinned extraction runs; the evidence maintenance path
// remains the sole owner of evidence reclamation.
func runProofBundleSweepPass(
	ctx context.Context, bundles store.ProofBundleRetentionStore, lifetime time.Duration,
) (deleted int, backlogLikely bool, err error) {
	if lifetime <= 0 {
		return 0, false, errors.New("proof-bundle retention lifetime must be positive")
	}
	for range proofSweepMaxBundlesPerPass {
		if err := ctx.Err(); err != nil {
			return deleted, false, err
		}
		n, err := bundles.SweepProofBundles(ctx, time.Now().UTC().Add(-lifetime))
		if err != nil {
			return deleted, false, err
		}
		if n == 0 {
			return deleted, false, nil
		}
		if n != 1 {
			return deleted, false, fmt.Errorf("proof-bundle retention: invalid sweep count %d", n)
		}
		deleted++
	}
	return deleted, true, nil
}

// runProofBundleMaintenance is started only for an explicitly configured
// lifetime. It checks at boot, drains bounded bursts with a yield, and then
// returns to the same low-frequency idle cadence as evidence retention.
func runProofBundleMaintenance(
	ctx context.Context, bundles store.ProofBundleRetentionStore, lifetime, idleInterval, backlogDelay time.Duration,
) {
	if lifetime <= 0 || idleInterval <= 0 || backlogDelay <= 0 {
		log.Printf("proof-bundle retention disabled: invalid lifetime/idle/backlog intervals %s/%s/%s", lifetime, idleInterval, backlogDelay)
		return
	}
	if ctx.Err() != nil {
		return
	}
	delay := time.Duration(0)
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		deleted, backlogLikely, err := runProofBundleSweepPass(ctx, bundles, lifetime)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("proof-bundle retention: %v", err)
			delay = idleInterval
			continue
		}
		if deleted > 0 {
			log.Printf("proof-bundle retention: swept %d bundle(s)", deleted)
		}
		if backlogLikely {
			delay = backlogDelay
		} else {
			delay = idleInterval
		}
	}
}

// runEvidenceMaintenance checks immediately at boot. Empty stores cost one
// query per idle interval; a likely backlog is processed in bounded bursts
// separated by a short yield. Pinned proof/checkpoint runs are excluded by the
// store, and each individual deletion transaction has its own fixed row cap.
func runEvidenceMaintenance(
	ctx context.Context, evidence store.EvidenceStore,
	idleInterval, backlogDelay, staleStagedAfter time.Duration,
) {
	if idleInterval <= 0 || backlogDelay <= 0 {
		log.Printf("evidence retention disabled: invalid idle/backlog intervals %s/%s", idleInterval, backlogDelay)
		return
	}
	if ctx.Err() != nil {
		return
	}
	delay := time.Duration(0)
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		progress, backlogLikely, err := runEvidenceSweepPass(ctx, evidence, staleStagedAfter)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("evidence retention: %v", err)
			delay = idleInterval
			continue
		}
		if progress.DidWork() {
			log.Printf(
				"evidence retention: completed %d run(s); deleted %d physical row(s) "+
					"(%d associations, %d assertions, %d atoms)",
				progress.RunsDeleted, progress.PhysicalRowsDeleted(),
				progress.AssociationRowsDeleted, progress.AssertionRowsDeleted,
				progress.AtomRowsDeleted,
			)
		}
		if backlogLikely {
			delay = backlogDelay
		} else {
			delay = idleInterval
		}
	}
}
