// Command phebs is the self-hosted code-search server: API, UI, sync, and
// indexing in one binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/text/unicode/norm"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/indexer"
	phebsmcp "github.com/bmeddeb/phebs/internal/mcp"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/ui"
)

var version = "0.1.0-dev" // ponytail: ldflags stamping when releases exist

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: phebs serve [-config phebs.yaml] [-addr 127.0.0.1:3070]")
		os.Exit(2)
	}
	if err := serve(os.Args[2:]); err != nil {
		log.Fatal(err)
	}
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := flags.String("config", "", "path to config file (defaults apply if omitted)")
	addr := flags.String("addr", "", "listen address (overrides config)")
	_ = flags.Parse(args)

	cfg, err := loadConfig(*cfgPath)
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
	st, err := store.OpenLocal(ctx, cfg.Server.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(context.Background()) }()

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
		go func() {
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
		}()
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
	go runner.Run(ctx)
	fetchRunner := &store.Runner{Store: st, Kind: store.JobFetch, Handle: phebssync.FetchHandler(cfg, st),
		Interval: cfg.Sync.Interval()}
	go fetchRunner.Run(ctx)
	if watched := phebssync.Watched(cfg); len(watched) > 0 {
		log.Printf("watch mode: polling %d local repo(s)", len(watched))
		go (&phebssync.Watcher{Store: st, Conns: watched}).Run(ctx)
	}
	// T7.5: periodic freshness for remote connections
	if every := cfg.Sync.ResyncEvery(); every > 0 {
		go phebssync.Resync(ctx, st, cfg, every)
	}

	// index pipeline: same-SHA zoekt-git-index child consumes indexing_job
	if bin, err := indexer.FindBinary(); err != nil {
		log.Print("WARNING: zoekt-git-index not found — indexing disabled (make build provides it; or set PHEBS_ZOEKT_GIT_INDEX)")
	} else {
		ix := &indexer.Indexer{DataDir: cfg.Server.DataDir, Bin: bin, Store: st}
		ixRunner := &store.Runner{Store: st, Kind: store.JobIndex, Handle: ix.Handle,
			Interval: cfg.Sync.Interval()}
		go ixRunner.Run(ctx)
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
	apiHandler := api.New(api.Options{
		Version: version,
		Store:   st, Search: searcher, DataDir: cfg.Server.DataDir,
		CodeNav: codeNavigation,
		IsAdmin: func(ctx context.Context) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			return ok && principal.IsAdmin
		},
		AuditRecord: auditRecord, AuditLog: st, Analytics: st, Visible: visibleFor,
		WebhookSecret: cfg.Webhook.Secret, ResyncConnections: resyncNames,
	})
	// T8.2/T9.1: MCP accepts the same DB-backed API keys as the HTTP API.
	mcpServer := phebsmcp.NewServer(phebsmcp.Options{
		Version: version, Store: st, Search: searcher, DataDir: cfg.Server.DataDir,
		CodeNav: codeNavigation, Visible: visibleFor,
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
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

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
	mux.Handle("/api/auth/", authService.Handler())
	mux.Handle("/api/mcp", authService.Require(mcpHandler))
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if api.IsAuthenticationExempt(r.URL.Path) {
			apiHandler.ServeHTTP(w, r)
			return
		}
		protectedAPI.ServeHTTP(w, r)
	}))
	mux.Handle("GET /metrics", metricsHandler)
	mux.Handle("/", uiHandler)
	return authService.LoadAndSave(mux)
}

func loadConfig(path string) (*config.Config, error) {
	if path != "" {
		return config.Load(path)
	}
	log.Print("no -config given; using defaults")
	return config.Parse([]byte("{}"))
}
