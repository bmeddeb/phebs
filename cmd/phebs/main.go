// Command phebs is the self-hosted code-search server: API, UI, sync, and
// indexing in one binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/indexer"
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
	runner := &store.Runner{Store: st, Kind: store.JobSync, Handle: phebssync.Handler(cfg, st)}
	go runner.Run(ctx)

	// index pipeline: same-SHA zoekt-git-index child consumes indexing_job
	if bin, err := indexer.FindBinary(); err != nil {
		log.Print("WARNING: zoekt-git-index not found — indexing disabled (make dev builds it)")
	} else {
		ix := &indexer.Indexer{DataDir: cfg.Server.DataDir, Bin: bin, Store: st}
		ixRunner := &store.Runner{Store: st, Kind: store.JobIndex, Handle: ix.Handle}
		go ixRunner.Run(ctx)
	}

	dist, err := fs.Sub(ui.Dist, "dist")
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

	mux := http.NewServeMux()
	mux.Handle("/api/", api.New(api.Options{
		Version: version, APIKey: cfg.Auth.APIKey,
		Store: st, Search: searcher, DataDir: cfg.Server.DataDir,
	}))
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
