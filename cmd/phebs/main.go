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
	"path/filepath"
	"syscall"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/bmeddeb/phebs/internal/api"
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
		fmt.Fprintln(os.Stderr, "usage: phebs serve [-config phebs.yaml] [-addr :3070]")
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

	if cfg.Auth.APIKey == "" {
		log.Print("WARNING: auth.api_key is empty — the API is open")
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

	// repository-membership webhook events re-sync every remote connection
	var resyncNames []string
	for _, c := range cfg.Connections {
		if phebssync.IsRemote(c) {
			resyncNames = append(resyncNames, c.Name)
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", api.New(api.Options{
		Version: version, APIKey: cfg.Auth.APIKey,
		Store: st, Search: searcher, DataDir: cfg.Server.DataDir,
		WebhookSecret: cfg.Webhook.Secret, ResyncConnections: resyncNames,
	}))
	// T8.2: MCP over Streamable HTTP, same bearer as the API
	mcpServer := phebsmcp.NewServer(phebsmcp.Options{
		Version: version, Store: st, Search: searcher, DataDir: cfg.Server.DataDir,
	})
	mux.Handle("/api/mcp", api.RequireBearer(cfg.Auth.APIKey,
		mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return mcpServer }, nil)))
	mux.Handle("GET /metrics", promhttp.Handler()) // T3.3; unauthenticated like /api/health
	mux.Handle("/", http.FileServerFS(dist))

	srv := &http.Server{Addr: cfg.Server.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("phebs %s listening on %s (data: %s)", version, cfg.Server.Addr, cfg.Server.DataDir)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func loadConfig(path string) (*config.Config, error) {
	if path != "" {
		return config.Load(path)
	}
	log.Print("no -config given; using defaults")
	return config.Parse([]byte("{}"))
}
