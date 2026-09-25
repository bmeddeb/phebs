// Command phebs is the self-hosted code-search server: API, UI, sync, and
// indexing in one binary.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/scipfield"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftfield"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftgo"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	phebsmcp "github.com/bmeddeb/phebs/internal/mcp"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

var version = "0.2.1-dev" // ponytail: ldflags stamping when releases exist

const (
	evidenceSweepIdleInterval    = time.Hour
	evidenceSweepBacklogDelay    = 5 * time.Second
	evidenceSweepMaxStepsPerPass = 64
	evidenceStagedMaxAge         = 24 * time.Hour
	proofSweepMaxBundlesPerPass  = 8
	t335CatalogEncodedBytes      = 2801
	t335CatalogEncodedSHA256     = "sha256:7c495f76ed5660cc7f00d58a3089a77da2ebb860c7a22af6a76218a031f66ff0"
	t344CatalogEncodedBytes      = 3401
	t344CatalogEncodedSHA256     = "sha256:3308dd76d476a1dde641c3d5e794ba25288b450f81d0abcb6ea0cd1a64719e94"
	t4013ExactReportsEnvironment = "PHEBS_T4013_EXACT_REPORTS"
	t4013ExactReportsContract    = "source-free-v1"
)

var errPartitionAuthorityPending = errors.New("partitioned extraction authority pending")
var errServeFlags = errors.New("invalid serve flags")

func deferPendingPartitionAuthority(err error) (error, bool) {
	if errors.Is(err, errPartitionAuthorityPending) {
		return nil, true
	}
	return err, false
}

func partitionFenceAuthority(ctx context.Context, root, indexRoot string, state candidate.State) (string, string, error) {
	selected, err := observationpublication.ReadInventoryPublicationRootV2Context(ctx, root, state.Repository)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("%w: %v", errPartitionAuthorityPending, err)
		}
		return "", "", err
	}
	authority, err := observationpublication.ConfirmInventoryAuthorityReferenceV2(ctx, root, state.Repository, selected)
	if err != nil {
		return "", "", err
	}
	return partitionAuthorityForCandidate(ctx, indexRoot, state, authority)
}

// Bind both independently published inputs before planning, reuse or commit.
// The source manifest is bounded control metadata, never a member/corpus scan.
func partitionAuthorityForCandidate(ctx context.Context, indexRoot string, state candidate.State, authority observationpublication.InventoryAuthorityV2) (string, string, error) {
	source, err := repositoryindex.ReadSourceManifestContext(ctx, indexRoot, state.Repository)
	if err != nil {
		return "", "", err
	}
	if len(source.Revisions) == 0 || source.Revisions[0].Selector != "HEAD" ||
		source.Revisions[0].Commit != state.Commit || source.Digest != authority.SourceGenerationDigest {
		return "", "", errors.Join(errPartitionAuthorityPending, extractionpublication.ErrStale)
	}
	return authority.SourceGenerationDigest, authority.ObservationGenerationDigest, nil
}

// Reconcile an already authenticated current publication without creating another
// job. Missing/stale authority and callback failures retain ordinary queued retry.
func enqueueUnlessCurrent(
	ctx context.Context,
	kind store.JobKind,
	repository string,
	force bool,
	current func(context.Context, string) (bool, error),
	enqueue func(context.Context, store.JobKind, string, bool) (*store.Job, error),
) (*store.Job, error) {
	var currentErr error
	if !force && current != nil {
		var handled bool
		handled, currentErr = current(ctx, repository)
		if handled && currentErr == nil {
			return nil, nil
		}
	}
	job, enqueueErr := enqueue(ctx, kind, repository, force)
	return job, errors.Join(currentErr, enqueueErr)
}

func afterResolverPublication(
	ctx context.Context,
	repository string,
	reconcile func(context.Context, string) error,
	advance func(context.Context, string) error,
	enqueue func(context.Context, store.JobKind, string, bool) (*store.Job, error),
	callerEnabled bool,
) error {
	var reconcileErr error
	if reconcile != nil {
		reconcileErr = reconcile(ctx, repository)
	}
	var advanceErr error
	if advance != nil {
		advanceErr = advance(ctx, repository)
	}
	if !callerEnabled {
		return errors.Join(reconcileErr, advanceErr)
	}
	_, enqueueErr := enqueue(ctx, store.JobCallerLeaf, repository, false)
	return errors.Join(reconcileErr, advanceErr, enqueueErr)
}

func afterObservationPublication(
	ctx context.Context,
	repository string,
	reconcile func(context.Context, string) error,
	enqueue func(context.Context, store.JobKind, string, bool) (*store.Job, error),
) error {
	var reconcileErr error
	if reconcile != nil {
		reconcileErr = reconcile(ctx, repository)
	}
	_, enqueueErr := enqueue(ctx, store.JobExtract, repository, false)
	return errors.Join(reconcileErr, enqueueErr)
}

func afterServiceCatalogPublication(
	ctx context.Context,
	repository string,
	reconcile func(context.Context, string) error,
	enqueue func(context.Context, store.JobKind, string, bool) (*store.Job, error),
	candidateReady bool,
) error {
	var reconcileErr error
	if reconcile != nil {
		reconcileErr = reconcile(ctx, repository)
	}
	if !candidateReady {
		return reconcileErr
	}
	_, enqueueErr := enqueue(ctx, store.JobResolverCatalog, repository, false)
	return errors.Join(reconcileErr, enqueueErr)
}

func afterPartitionExtractionSettlement(
	ctx context.Context,
	repository string,
	reconcile func(context.Context, string) error,
	enqueue func(context.Context, store.JobKind, string, bool) (*store.Job, error),
	resolverReady bool,
	callerReady bool,
) error {
	var reconcileErr error
	if reconcile != nil {
		reconcileErr = reconcile(ctx, repository)
	}
	var resolverErr error
	if resolverReady {
		_, resolverErr = enqueue(
			ctx, store.JobResolverCatalog, repository, false,
		)
	}
	var callerErr error
	if callerReady {
		_, callerErr = enqueue(ctx, store.JobCallerLeaf, repository, false)
	}
	return errors.Join(reconcileErr, resolverErr, callerErr)
}

func allPartitionDomainsCurrent(
	ctx context.Context,
	domains []string,
	current func(context.Context, string) error,
) bool {
	if current == nil || len(domains) == 0 {
		return false
	}
	for _, domain := range domains {
		if domain == "" || current(ctx, domain) != nil {
			return false
		}
	}
	return true
}

func allPartitionDomainsMatch(
	ctx context.Context,
	domains []string,
	candidateManifest, sourceGeneration, observationGeneration, extractionPolicy string,
	current func(context.Context, string) (candidate.DownstreamDomainAuthority, error),
) bool {
	if current == nil || len(domains) == 0 || candidateManifest == "" ||
		sourceGeneration == "" || observationGeneration == "" || extractionPolicy == "" {
		return false
	}
	return allPartitionDomainsCurrent(ctx, domains, func(
		domainCtx context.Context, domain string,
	) error {
		authority, err := current(domainCtx, domain)
		if err != nil {
			return err
		}
		if authority.Domain != domain ||
			authority.CandidateManifestDigest != candidateManifest ||
			authority.ExtractionPolicyDigest != extractionPolicy ||
			authority.SourceGenerationDigest != sourceGeneration ||
			authority.ObservationGenerationDigest != observationGeneration {
			return store.ErrGenerationStale
		}
		return nil
	})
}

func legacyExtractionRequired(
	repository string,
	analysisUnits map[string]analysisunit.Scope,
) bool {
	_, configured := analysisUnits[repository]
	return configured
}

func main() {
	code, err := runPhebs(os.Args[1:])
	if err != nil {
		log.Print(err)
	}
	if code != 0 {
		os.Exit(code)
	}
}

// Work cancellation must not restore default signal handling while owned
// workers or the local database are still draining. Release the subscription
// only after command cleanup and the outer admitted lifetime have returned.
func commandSignalContext(parent context.Context) (context.Context, context.CancelFunc, context.CancelFunc) {
	signals, stopSignals := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(signals)
	return ctx, cancel, stopSignals
}

// runPhebs returns through owned exact-mode cleanup before main can exit.
// Ordinary invocations keep the absent-selector path with no producer state.
func runPhebs(args []string) (code int, retErr error) {
	lifetime, err := dispatchadmission.BootstrapProduction(context.Background())
	if err != nil {
		return 1, err
	}
	ctx := dispatchadmission.ProcessContext()
	if len(args) > 0 && (args[0] == "serve" || args[0] == "backup" || args[0] == "restore") {
		var cancel, stopSignals context.CancelFunc
		ctx, cancel, stopSignals = commandSignalContext(ctx)
		defer stopSignals()
		defer cancel()
	}
	if lifetime != nil {
		defer func() {
			retErr = errors.Join(retErr, lifetime.Close(context.Background()))
			if retErr != nil {
				code = 1
			}
		}()
		if _, err := lifetime.TakeStoreOwner(); err != nil {
			return 1, err
		}
	}
	if len(args) == 0 {
		if err := dispatchadmission.RequireProductionWorkCommand(""); err != nil {
			return 1, err
		}
		printUsage()
		return 2, nil
	}
	if err := dispatchadmission.RequireProductionWorkCommand(args[0]); err != nil {
		return 1, err
	}
	switch args[0] {
	case "serve":
		err = serve(ctx, args[1:])
		// The flag package already printed usage/diagnostics. Preserve its
		// ordinary exit codes, but return through any admitted lifetime close.
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		if errors.Is(err, errServeFlags) {
			return 2, nil
		}
	case "backup":
		err = backup(ctx, args[1:])
	case "restore":
		err = restore(ctx, args[1:])
	case "version":
		err = printVersion(args[1:], os.Stdout)
	case t422RuntimeFactsCommand:
		err = writeT422RuntimeFacts(context.Background(), args[1:], os.Stdout, lifetime)
	default:
		printUsage()
		return 2, nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
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

func backup(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	cfgPath := flags.String("config", "", "path to config file (defaults apply if omitted)")
	allowInsecurePerms := flags.Bool("allow-insecure-config-perms", false,
		"warn instead of refusing a config file readable by group or others")
	output := flags.String("output", "", "new backup directory (must not exist)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" {
		return errors.New("backup requires -output and accepts no positional arguments")
	}
	cfg, raw, err := loadRecoveryConfig(*cfgPath, *allowInsecurePerms)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx, err = bindT422ArchiveReports(ctx, cancel)
	if err != nil {
		return err
	}
	manifest, err := recovery.Create(ctx, recovery.BackupOptions{
		Options: recovery.Options{
			DataDir: cfg.Server.DataDir, Config: raw, PhebsVersion: version,
		},
		Output: *output,
	})
	if err != nil {
		return err
	}
	if dispatchadmission.ProductionWorkSelected() && ctx.Err() != nil {
		return ctx.Err()
	}
	fmt.Printf("backup published: %s (%s)\n", *output, manifest.ManifestSHA256)
	return nil
}

func restore(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	cfgPath := flags.String("config", "", "path to config file (defaults apply if omitted)")
	allowInsecurePerms := flags.Bool("allow-insecure-config-perms", false,
		"warn instead of refusing a config file readable by group or others")
	backupPath := flags.String("backup", "", "backup directory to verify and import")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *backupPath == "" {
		return errors.New("restore requires -backup and accepts no positional arguments")
	}
	cfg, raw, err := loadRecoveryConfig(*cfgPath, *allowInsecurePerms)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx, err = bindT422ArchiveReports(ctx, cancel)
	if err != nil {
		return err
	}
	manifest, err := recovery.Restore(ctx, recovery.RestoreOptions{
		Options: recovery.Options{
			DataDir: cfg.Server.DataDir, Config: raw, PhebsVersion: version,
		},
		Backup: *backupPath,
	})
	if err != nil {
		return err
	}
	if dispatchadmission.ProductionWorkSelected() && ctx.Err() != nil {
		return ctx.Err()
	}
	fmt.Printf("restore verified and imported: %s\n", manifest.ManifestSHA256)
	return nil
}

// Offline commands install only real context-based work observers. They do not
// create server runners, lifecycle owners, index sinks or semantic request state.
func bindT422ArchiveReports(ctx context.Context, cancel context.CancelFunc) (context.Context, error) {
	if !dispatchadmission.ProductionWorkSelected() {
		return ctx, nil
	}
	ctx, err := bindT422SourceReports(ctx, func(error) { cancel() })
	if err != nil {
		return nil, err
	}
	ctx, err = bindT422ObservationReports(ctx, func(error) { cancel() })
	if err != nil {
		return nil, err
	}
	ctx, err = bindT422ArchiveArtifactReports(ctx, cancel)
	if err != nil {
		return nil, err
	}
	return bindT422ArchiveWorkspace(ctx, cancel)
}

func reportT4013Startup(stage string) {
	if os.Getenv("PHEBS_T4013_STARTUP_DIAGNOSTICS") != "source-free-v1" {
		return
	}
	report, err := json.Marshal(struct {
		Schema string `json:"schema"`
		Stage  string `json:"stage"`
	}{Schema: "t4013-source-free-startup-v1", Stage: stage})
	if err == nil {
		log.Printf("T40.13 startup lifecycle: %s", report)
	}
}

func t4013ExactReportsEnabled() (bool, error) {
	value, present := os.LookupEnv(t4013ExactReportsEnvironment)
	if !present {
		return false, nil
	}
	if value != t4013ExactReportsContract {
		return false, errors.New("T40.13 exact-report contract is invalid")
	}
	return true, nil
}

func t4013ExactReportSink(prefix string) func([]byte) error {
	return func(report []byte) error {
		return log.Output(2, prefix+string(report))
	}
}

func bindT4013ExactReports(
	enabled bool,
	fail func(error),
	candidate *candidatejob.Worker,
	runners ...*store.Runner,
) {
	if !enabled {
		return
	}
	if fail == nil {
		panic("T40.13 exact reporting lacks its failure latch")
	}
	if candidate != nil {
		candidate.OperationReports = t4013ExactReportSink("candidate operation: ")
		candidate.OperationReportFailure = fail
	}
	for _, runner := range runners {
		if runner == nil {
			continue
		}
		runner.LifecycleReports = t4013ExactReportSink("job lifecycle: ")
		runner.LifecycleReportFailure = fail
	}
}

func t4013ExactReportTerminalError(failed <-chan struct{}) error {
	select {
	case <-failed:
		return errors.New("T40.13 exact reporting failed")
	default:
		return nil
	}
}

// T42 exact mode extends the existing process-lifetime failure latch to the
// three generation schedulers. Historical T40 reporting remains unchanged.
func bindT422ExactChunkReports(enabled bool, fail func(error), scheduler *generationscheduler.Scheduler) {
	if !enabled {
		return
	}
	if fail == nil || scheduler == nil {
		panic("T42.2 exact chunk reporting lacks its failure latch or scheduler")
	}
	scheduler.ChunkReports = t4013ExactReportSink("generation chunk lifecycle: ")
	scheduler.ChunkReportFailure = fail
}

func serverTerminalError(
	serveErr, shutdownErr error,
	exactReportFailed <-chan struct{},
	exactReadFailed <-chan error,
) error {
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	if exactReportFailed == nil && exactReadFailed == nil {
		shutdownErr = nil
	}
	return errors.Join(
		serveErr, shutdownErr,
		t4013ExactReportTerminalError(exactReportFailed),
		t421ExactReadTerminalError(exactReadFailed),
	)
}

// Only the decoded parent-bound V3 recipe selects unavailable compatibility.
// Its closed requests never ask for compatibility and its sandbox budget is
// zero. Keep the shared dispatch refusal as a terminal guard against any later
// accidental call. Ordinary and older exact launches still validate Buf.
func initializeCompatibilityForLaunch(ctx context.Context, semanticLaunch *t422SemanticLaunch) (compat.Service, error) {
	if semanticLaunch != nil {
		return nil, nil
	}
	bin, err := compat.FindBinary()
	if err != nil {
		return nil, fmt.Errorf("pinned buf not found — contract compatibility disabled (make build provides it; or set PHEBS_BUF): %w", err)
	}
	checker, err := compat.New(bin)
	if err != nil {
		//nolint:staticcheck // Buf is a proper name; preserve the ordinary startup warning.
		return nil, fmt.Errorf("Buf sandbox unavailable — contract compatibility disabled: %w", err)
	}
	if err := checker.Validate(ctx); err != nil {
		//nolint:staticcheck // Buf is a proper name; preserve the ordinary startup warning.
		return nil, fmt.Errorf("Buf validation failed — contract compatibility disabled: %w", err)
	}
	return checker, nil
}

func serve(ctx context.Context, args []string) (retErr error) {
	// Startup wiring is split into named phases (serve_*.go). Every teardown
	// action is deferred through d in the exact order the inline defers had,
	// draining LIFO when serve returns.
	d := &serveDeps{ctx: ctx}
	defer d.runDeferred(&retErr)

	var err error
	if d.flags, err = parseServeFlags(args); err != nil {
		return err
	}
	if d.exactReports, d.exactReads, err = serveExactMode(); err != nil {
		return err
	}
	if d.semanticLaunch, err = readServeSemanticLaunch(d.flags, d.exactReads, d.exactReports); err != nil {
		return err
	}
	reportT4013Startup("process_started")

	if d.cfg, d.rawConfig, err = loadServeConfig(d.semanticLaunch, d.flags); err != nil {
		return err
	}
	if err := newServeExtractionRegistries(d); err != nil {
		return err
	}
	if err := startServeOwners(d); err != nil {
		return err
	}
	if err := newServeExactScaffolding(d); err != nil {
		return err
	}
	if err := openServeStore(d); err != nil {
		return err
	}
	newServeBackground(d)
	if err := wireServeLifecycle(d); err != nil {
		return err
	}
	if err := recoverServePublications(d); err != nil {
		return err
	}
	if err := startServeLifecycleController(d); err != nil {
		return err
	}
	if err := newServeAuth(d); err != nil {
		return err
	}
	if err := reconcileServeState(d); err != nil {
		return err
	}
	if err := reconcileServeCatalogs(d); err != nil {
		return err
	}
	if err := startServeSyncRunners(d); err != nil {
		return err
	}
	if err := startServeObservationSchedulers(d); err != nil {
		return err
	}
	if err := startServeExtractionPipeline(d); err != nil {
		return err
	}
	startServeMaintenance(d)
	if err := startServeIndexPipeline(d); err != nil {
		return err
	}
	reportT4013Startup("scheduler_recovery_complete")

	newServeVisibility(d)
	if err := openServeSearcher(d); err != nil {
		return err
	}
	apiOpts, err := newServeAPIOptions(d)
	if err != nil {
		return err
	}
	finalAuthority, tailReadiness, err := newServeFinalAuthorityReads(d)
	if err != nil {
		return err
	}
	handler, err := newServeHTTPHandlers(d, apiOpts, finalAuthority, tailReadiness)
	if err != nil {
		return err
	}
	return runServeListener(d, handler)
}

func t421ExactReadServerBaseContext(
	ctx context.Context,
	enabled bool,
) func(net.Listener) context.Context {
	if !enabled {
		return nil
	}
	return func(net.Listener) context.Context { return ctx }
}

func legacyServiceCatalogSelections(
	selections map[string]config.ServiceCatalog,
) map[string]config.ServiceCatalog {
	legacy := make(map[string]config.ServiceCatalog, len(selections))
	for repository, selection := range selections {
		if selection.RuntimeVersion() != config.ServiceCatalogRuntimeV3 {
			legacy[repository] = selection
		}
	}
	return legacy
}

func legacyRelationshipReconcileFor(
	repository string,
	selections map[string]config.ServiceCatalog,
	reconcile func(context.Context, string) error,
) func(context.Context, string) error {
	selection, configured := selections[repository]
	if configured && selection.RuntimeVersion() == config.ServiceCatalogRuntimeV3 {
		return nil
	}
	return reconcile
}

func openStoreAfterRetentionWarning(
	warn func(string),
	open func() (*store.Surreal, error),
) (*store.Surreal, error) {
	warn(api.RetentionStatusWarningCode)
	return open()
}

func newHTTPHandler(authService *auth.Service, apiHandler, mcpHandler, metricsHandler, uiHandler http.Handler, serverCfg config.Server) http.Handler {
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
	// /metrics is operator telemetry: it sits behind auth like every other
	// protected route, not on the public surface.
	mux.Handle("GET /metrics", authService.Require(metricsHandler))
	mux.Handle("/", uiHandler)
	handler := api.WithRetentionStatusWarning(authService.LoadAndSave(mux))
	return securityHeadersMiddleware(serverCfg.SecurityHeadersEnabled())(handler)
}

func loadServerConfig(path string, allowInsecurePerms bool) (*config.Config, []byte, error) {
	if path != "" {
		if err := enforceConfigFilePermissions(path, allowInsecurePerms); err != nil {
			return nil, nil, err
		}
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

// enforceConfigFilePermissions refuses a config file that grants group or
// other access — fail-closed, since the config may hold API keys, OIDC client
// secrets, webhook secrets, and connection tokens. allowInsecurePerms
// (--allow-insecure-config-perms) downgrades the refusal to a loud warning
// for operators who know what they're doing.
func enforceConfigFilePermissions(path string, allowInsecurePerms bool) error {
	err := config.CheckFilePermissions(path)
	if err == nil {
		return nil
	}
	var insecure *config.InsecurePermissionsError
	if !errors.As(err, &insecure) {
		return err
	}
	if allowInsecurePerms {
		log.Printf("WARNING: %s", insecure.Error())
		return nil
	}
	return insecure
}

func loadRecoveryConfig(path string, allowInsecurePerms bool) (*config.Config, []byte, error) {
	if path != "" {
		if err := enforceConfigFilePermissions(path, allowInsecurePerms); err != nil {
			return nil, nil, err
		}
		return config.LoadForRecovery(path)
	}
	raw := []byte("{}")
	cfg, err := config.Parse(raw)
	return cfg, raw, err
}

// bindSyntheticThriftFieldDemo is the make-dev-only bridge from the committed
// cloneable fixture into the ordinary sync, index, and extraction pipeline.
// The environment value must name an explicit absolute bundle; production
// config and ordinary serve startup remain unchanged and default-dark.
func bindSyntheticThriftFieldDemo(cfg *config.Config, fixture string) error {
	if fixture == "" {
		return nil
	}
	if cfg == nil {
		return errors.New("synthetic Thrift field demo requires server configuration")
	}
	if strings.TrimSpace(fixture) != fixture ||
		!filepath.IsAbs(fixture) ||
		filepath.Clean(fixture) != fixture ||
		filepath.Base(fixture) != "t225-thrift-field-demo.bundle" {
		return errors.New(
			"synthetic Thrift field demo must name the absolute clean t225-thrift-field-demo.bundle path",
		)
	}
	info, err := os.Stat(fixture)
	if err != nil {
		return fmt.Errorf("inspect synthetic Thrift field demo: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("synthetic Thrift field demo must be a regular bundle file")
	}

	const connectionName = "t22-thrift-field-demo"
	alreadyConnected := false
	for _, connection := range cfg.Connections {
		if connection.Name == connectionName && connection.URL != fixture {
			return fmt.Errorf(
				"synthetic Thrift field demo connection %q already names another source",
				connectionName,
			)
		}
		if connection.URL == fixture {
			alreadyConnected = true
		}
	}
	if !alreadyConnected {
		cfg.Connections = append(cfg.Connections, config.Connection{
			Name: connectionName,
			Type: "git",
			URL:  fixture,
		})
	}
	cfg.Experimental.ProvisionalThriftFieldExtraction = true
	return nil
}

// bindT307NeutralServiceDemo is the make-dev-only bridge from one retained,
// cloneable neutral repository into the ordinary focused-index, extraction,
// resolver, and caller-overlay pipelines. The bridge is explicit so ordinary
// serve startup and production configuration remain unchanged.
func bindT307NeutralServiceDemo(cfg *config.Config, fixture string) error {
	if fixture == "" {
		return nil
	}
	if cfg == nil {
		return errors.New("T30.7 neutral service demo requires server configuration")
	}
	if strings.TrimSpace(fixture) != fixture ||
		!filepath.IsAbs(fixture) ||
		filepath.Clean(fixture) != fixture ||
		filepath.Base(fixture) != "t307-neutral-service.bundle" {
		return errors.New(
			"T30.7 neutral service demo must name the absolute clean t307-neutral-service.bundle path",
		)
	}
	info, err := os.Stat(fixture)
	if err != nil {
		return fmt.Errorf("inspect T30.7 neutral service demo: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("T30.7 neutral service demo must be a regular bundle file")
	}

	repository, err := phebssync.RepoName(fixture)
	if err != nil {
		return fmt.Errorf("derive T30.7 neutral service repository: %w", err)
	}
	desiredUnit := analysisunit.Config{
		Name:    "orders-service",
		Primary: []string{"service/orders"},
		Supporting: []string{
			"api/orders.proto",
			"gen/ordersv1/orders_grpc.pb.go",
			"generated-from-snapshot.json",
			"go.mod",
		},
	}
	desiredCanonical, err := desiredUnit.Scope(repository).Canonical()
	if err != nil {
		return fmt.Errorf("validate T30.7 neutral service scope: %w", err)
	}
	if existing, ok := cfg.AnalysisUnits[repository]; ok {
		existingCanonical, canonicalErr := existing.Scope(repository).Canonical()
		if canonicalErr != nil || string(existingCanonical) != string(desiredCanonical) ||
			existing.TypedIndex != nil {
			return fmt.Errorf(
				"T30.7 neutral service repository %q already has another analysis unit",
				repository,
			)
		}
	}

	const connectionName = "t30-service-scope-demo"
	alreadyConnected := false
	for _, connection := range cfg.Connections {
		if connection.Name == connectionName &&
			(connection.Type != "git" || connection.URL != fixture) {
			return fmt.Errorf(
				"T30.7 neutral service demo connection %q already names another source",
				connectionName,
			)
		}
		if connection.URL == fixture {
			if connection.Type != "git" {
				return errors.New(
					"T30.7 neutral service demo source is already bound to a non-git connection",
				)
			}
			alreadyConnected = true
		}
	}

	if !alreadyConnected {
		cfg.Connections = append(cfg.Connections, config.Connection{
			Name: connectionName,
			Type: "git",
			URL:  fixture,
		})
	}
	if cfg.AnalysisUnits == nil {
		cfg.AnalysisUnits = make(map[string]analysisunit.Config, 1)
	}
	cfg.AnalysisUnits[repository] = desiredUnit
	cfg.Experimental.ProvisionalProtoExtraction = true
	cfg.Experimental.ProvisionalKafkaExtraction = true
	return nil
}

// bindT335ServiceDirectoryDemo adds one reviewed operator catalog to the
// already-bound T30.7 neutral repository. It is a make-dev-only configuration
// bridge: catalog ingestion, lifecycle reconciliation, HTTP, MCP, and UI reads
// still use the ordinary production services.
func bindT335ServiceDirectoryDemo(
	cfg *config.Config,
	fixture, catalogPath string,
) error {
	if catalogPath == "" {
		return nil
	}
	if cfg == nil {
		return errors.New("T33.5 service directory demo requires server configuration")
	}
	if strings.TrimSpace(catalogPath) != catalogPath ||
		!filepath.IsAbs(catalogPath) ||
		filepath.Clean(catalogPath) != catalogPath ||
		filepath.Base(catalogPath) != "t335-service-catalog.json" {
		return errors.New(
			"T33.5 service directory demo must name the absolute clean t335-service-catalog.json path",
		)
	}
	info, err := os.Lstat(catalogPath)
	if err != nil {
		return fmt.Errorf("inspect T33.5 service directory catalog: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("T33.5 service directory catalog must be a regular file")
	}
	if strings.TrimSpace(fixture) != fixture ||
		!filepath.IsAbs(fixture) || filepath.Clean(fixture) != fixture ||
		filepath.Base(fixture) != "t307-neutral-service.bundle" {
		return errors.New("T33.5 service directory demo requires the exact T30.7 neutral bundle")
	}
	repository, err := phebssync.RepoName(fixture)
	if err != nil {
		return fmt.Errorf("derive T33.5 service directory repository: %w", err)
	}
	connected := false
	for _, connection := range cfg.Connections {
		if connection.Type == "git" && connection.URL == fixture {
			connected = true
			break
		}
	}
	unit, unitBound := cfg.AnalysisUnits[repository]
	if !connected || !unitBound || unit.Name != "orders-service" {
		return errors.New("T33.5 service directory demo requires the bound T30.7 neutral cohort")
	}
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		return fmt.Errorf("read T33.5 service directory catalog: %w", err)
	}
	encodedDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	if len(raw) != t335CatalogEncodedBytes ||
		encodedDigest != t335CatalogEncodedSHA256 {
		return fmt.Errorf(
			"T33.5 service directory catalog does not match the retained bytes: got %d bytes and %s",
			len(raw), encodedDigest,
		)
	}
	catalog, err := servicecatalog.Decode(raw)
	if err != nil {
		return fmt.Errorf("validate T33.5 service directory catalog: %w", err)
	}
	desired := config.ServiceCatalog{
		Kind: servicecatalog.AuthorityOperator, ID: "t335-demo",
		Version: "v1", Path: catalogPath,
	}
	if catalog.Authority.Kind != desired.Kind || catalog.Authority.ID != desired.ID ||
		catalog.Authority.Version != desired.Version {
		return errors.New("T33.5 service directory catalog authority does not match its selection")
	}
	if existing, ok := cfg.ServiceCatalogs[repository]; ok && existing != desired {
		return fmt.Errorf(
			"T33.5 service directory repository %q already has another service catalog",
			repository,
		)
	}
	if cfg.ServiceCatalogs == nil {
		cfg.ServiceCatalogs = make(map[string]config.ServiceCatalog, 1)
	}
	cfg.ServiceCatalogs[repository] = desired
	return nil
}

// bindT344ServiceSearchDemo adds the retained T32.3 neutral corpus as a
// separate whole-repository cohort. It deliberately does not attach an
// analysis unit or enable an evidence pack: ordinary whole indexing, catalog
// ingestion, activation, HTTP/MCP, and UI paths produce the demonstration.
func bindT344ServiceSearchDemo(
	cfg *config.Config,
	fixture, catalogPath string,
) error {
	if fixture == "" && catalogPath == "" {
		return nil
	}
	if cfg == nil || fixture == "" || catalogPath == "" {
		return errors.New("T34.4 service-search demo requires server, bundle, and catalog")
	}
	for path, base := range map[string]string{
		fixture:     "t323-neutral-corpus.bundle",
		catalogPath: "t344-service-catalog.json",
	} {
		if strings.TrimSpace(path) != path || !filepath.IsAbs(path) ||
			filepath.Clean(path) != path || filepath.Base(path) != base {
			return fmt.Errorf("T34.4 service-search demo requires the absolute clean %s path", base)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("T34.4 service-search demo %s is missing or special", base)
		}
	}
	repository, err := phebssync.RepoName(fixture)
	if err != nil {
		return fmt.Errorf("derive T34.4 service-search repository: %w", err)
	}
	if _, focused := cfg.AnalysisUnits[repository]; focused {
		return errors.New("T34.4 service-search demo repository must remain whole-repository")
	}
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		return fmt.Errorf("read T34.4 service-search catalog: %w", err)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	if len(raw) != t344CatalogEncodedBytes || digest != t344CatalogEncodedSHA256 {
		return fmt.Errorf(
			"T34.4 service-search catalog differs from retained bytes: got %d and %s",
			len(raw), digest,
		)
	}
	catalog, err := servicecatalog.Decode(raw)
	if err != nil {
		return fmt.Errorf("validate T34.4 service-search catalog: %w", err)
	}
	desired := config.ServiceCatalog{
		Kind: servicecatalog.AuthorityOperator, ID: "t344-demo",
		Version: "v1", Path: catalogPath,
	}
	if catalog.Authority.Kind != desired.Kind || catalog.Authority.ID != desired.ID ||
		catalog.Authority.Version != desired.Version {
		return errors.New("T34.4 service-search catalog authority differs from selection")
	}
	if existing, ok := cfg.ServiceCatalogs[repository]; ok && existing != desired {
		return fmt.Errorf("T34.4 service-search repository %q has another catalog", repository)
	}
	for _, connection := range cfg.Connections {
		if connection.URL == fixture && connection.Type != "git" {
			return errors.New("T34.4 service-search bundle is bound as a non-git source")
		}
		if connection.Name == "t34-service-search-demo" &&
			(connection.Type != "git" || connection.URL != fixture) {
			return errors.New("T34.4 service-search connection name is already in use")
		}
	}
	connected := false
	for _, connection := range cfg.Connections {
		connected = connected || connection.Type == "git" && connection.URL == fixture
	}
	if !connected {
		cfg.Connections = append(cfg.Connections, config.Connection{
			Name: "t34-service-search-demo", Type: "git", URL: fixture,
		})
	}
	if cfg.ServiceCatalogs == nil {
		cfg.ServiceCatalogs = make(map[string]config.ServiceCatalog, 1)
	}
	cfg.ServiceCatalogs[repository] = desired
	return nil
}

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

// evidenceExtractors is the validation-gated registry. The provisional
// declared-protobuf reader stays absent unless the operator explicitly opts
// in; T11.1/T12.3 do not support default production activation.
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

func enqueueCandidateAfterIndex(
	ctx context.Context,
	st store.Store,
	repo, commit string,
	diagnosticsEnabled bool,
) error {
	if err := store.EnqueueUnlessInFlight(ctx, st, store.JobCandidate, repo); err != nil {
		return store.WithClass(store.ClassExtract,
			fmt.Errorf("enqueue candidate planning for %s@%s: %w", repo, commit, err))
	}
	if diagnosticsEnabled {
		diagnostics.Logf(
			"candidate queued repository=%q commit=%s cause=indexed force=false",
			repo, commit,
		)
	}
	return nil
}

type postIndexCallback func(context.Context, string, string) error

type observationPlanningEnqueue func(
	context.Context,
	string,
) (observationpublication.PlanningEnqueue, error)

// chainObservationPlanningAfterIndex preserves the selected service-catalog
// and service-search transition as the first step at this seam. Planning is
// still attempted when that transition fails so both independently owned
// errors remain visible, while focused repositories retain their exact bypass.
func chainObservationPlanningAfterIndex(
	prior postIndexCallback,
	focused func(string) bool,
	enqueue observationPlanningEnqueue,
	report func(observationpublication.PlanningEnqueue),
) postIndexCallback {
	return func(ctx context.Context, repository, commit string) error {
		var priorErr error
		if prior != nil {
			priorErr = prior(ctx, repository, commit)
		}
		if focused != nil && focused(repository) {
			return priorErr
		}
		disposition, planningErr := enqueue(ctx, repository)
		if planningErr == nil && report != nil {
			report(disposition)
		}
		return errors.Join(priorErr, planningErr)
	}
}

type observationPlanningStartupSummary struct {
	Current     int
	Active      int
	Failed      int
	Enqueued    int
	Unavailable int
}

func (summary observationPlanningStartupSummary) total() int {
	return summary.Current + summary.Active + summary.Failed +
		summary.Enqueued + summary.Unavailable
}

// enqueueObservationPlanningStartup repairs the crash window between a
// committed whole-repository search generation and its durable planning
// ownership. The returned aggregate is deliberately source-free: callers log
// no repository identity or raw enqueue error.
func enqueueObservationPlanningStartup(
	ctx context.Context,
	repositories []store.Repo,
	enqueue observationPlanningEnqueue,
) (observationPlanningStartupSummary, error) {
	var summary observationPlanningStartupSummary
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		if repository.Deleting || repository.IndexedCommitHash == "" ||
			repository.IndexedAnalysisUnit != nil {
			continue
		}
		disposition, err := enqueue(ctx, repository.Name)
		if err != nil {
			if ctx.Err() != nil {
				return summary, ctx.Err()
			}
			summary.Unavailable++
			continue
		}
		switch disposition {
		case observationpublication.PlanningCurrent:
			summary.Current++
		case observationpublication.PlanningActive:
			summary.Active++
		case observationpublication.PlanningFailed:
			summary.Failed++
		case observationpublication.PlanningEnqueued:
			summary.Enqueued++
		default:
			summary.Unavailable++
		}
	}
	return summary, nil
}

func reconcileServiceSearchGeneration(
	ctx context.Context,
	st *store.Surreal,
	dataDir, repository string,
) (servicequery.ReconcileOutcome, error) {
	repo, err := st.GetRepo(ctx, repository)
	if errors.Is(err, store.ErrNotFound) ||
		err == nil && (repo.Deleting || repo.IndexedCommitHash == "" ||
			repo.IndexedAnalysisUnit != nil &&
				repo.IndexedAnalysisUnit.SearchIndexPosture ==
					analysisunit.SearchIndexFocused) {
		return servicequery.ReconcileOutcome{}, nil
	}
	if err != nil {
		return servicequery.ReconcileOutcome{}, err
	}
	return servicequery.ReconcileGeneration(
		ctx, filepath.Join(dataDir, "index"), st, repository,
	)
}

type analysisUnitPostureDiagnostic struct {
	Repository              string   `json:"repository,omitempty"`
	Configured              bool     `json:"configured"`
	UnitName                string   `json:"unit_name,omitempty"`
	UnitDigest              string   `json:"unit_digest,omitempty"`
	PrimaryPathCount        int      `json:"primary_path_count"`
	SupportingPathCount     int      `json:"supporting_path_count"`
	SearchPosture           string   `json:"search_posture"`
	TypedIndexPosture       string   `json:"typed_index_posture"`
	EnabledExtractorDomains []string `json:"enabled_extractor_domains"`
	Recommendation          string   `json:"recommendation"`
}

func logAnalysisUnitPosture(
	repository string,
	state *analysisunit.State,
	extractors []extract.Extractor,
) {
	report := analysisUnitPosture(repository, state, extractors)
	data, err := json.Marshal(report)
	if err != nil {
		diagnostics.Logf("encode analysis unit posture: %v", err)
		return
	}
	diagnostics.Logf("analysis unit posture: %s", data)
}

func analysisUnitPosture(
	repository string,
	state *analysisunit.State,
	extractors []extract.Extractor,
) analysisUnitPostureDiagnostic {
	domains := make([]string, 0, len(extractors))
	for _, extractor := range extractors {
		domains = append(domains, extractor.Domain())
	}
	sort.Strings(domains)
	report := analysisUnitPostureDiagnostic{
		Repository: repository, EnabledExtractorDomains: domains,
		SearchPosture:     analysisunit.SearchIndexWholeRepository,
		TypedIndexPosture: analysisunit.TypedIndexRepositoryRootUnbound,
		Recommendation:    "configure_analysis_unit_for_service_scope",
	}
	if state != nil {
		report.Configured = true
		report.UnitName = state.Name
		report.UnitDigest = state.Digest
		report.PrimaryPathCount = state.PrimaryPathCount
		report.SupportingPathCount = state.SupportingPathCount
		report.SearchPosture = state.SearchIndexPosture
		report.TypedIndexPosture = state.TypedIndexPosture
		report.Recommendation = "configuration_ready"
		if len(domains) == 0 {
			report.Recommendation = "enable_required_experimental_extractors"
		} else if state.TypedIndexPosture == analysisunit.TypedIndexRepositoryRootUnbound {
			for _, domain := range domains {
				if domain == "grpc-caller" || domain == "thrift-caller" ||
					domain == "scip-proto-field" || domain == "scip-thrift-field" {
					report.Recommendation = "configure_unit_bound_scip_for_typed_domains"
					break
				}
			}
		}
	}
	return report
}

// enqueueCandidateBackfill closes the upgrade and restart gap for repositories
// indexed before the current candidate-policy generation existed. The queue
// provides one pending slot per target, so restart and partial-progress
// retries are idempotent. Publication itself creates the extraction successor.
type candidateBackfillState interface {
	store.Store
	store.CandidateManifestPublicationStore
}

func enqueueCandidateBackfill(
	ctx context.Context,
	st candidateBackfillState,
	currentPolicyDigest string,
) error {
	return enqueueCandidateBackfillWithReadiness(
		ctx, st, currentPolicyDigest, func(string) bool { return true },
	)
}

func enqueueCandidateBackfillWithReadiness(
	ctx context.Context,
	st candidateBackfillState,
	currentPolicyDigest string,
	ready func(string) bool,
) error {
	if currentPolicyDigest == "" {
		return errors.New("backfill candidate jobs: current policy digest is required")
	}
	if ready == nil {
		return errors.New("backfill candidate jobs: mirror readiness is required")
	}
	repos, err := st.ListRepos(ctx)
	if err != nil {
		return fmt.Errorf("backfill candidate jobs: list repositories: %w", err)
	}
	for _, repo := range repos {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("backfill candidate jobs: %w", err)
		}
		if repo.IndexedCommitHash == "" || repo.Deleting {
			continue
		}
		if !ready(repo.Name) {
			continue
		}
		publication, publicationErr :=
			st.GetCandidateManifestPublication(ctx, repo.Name)
		retired := errors.Is(
			publicationErr, store.ErrInvalidCandidateManifestPublication,
		)
		if publicationErr == nil {
			if publication == nil {
				return fmt.Errorf(
					"backfill candidate job for %s: publication store returned nil",
					repo.Name,
				)
			}
			retired = publication.PolicyDigest != currentPolicyDigest
		} else if !errors.Is(publicationErr, store.ErrNotFound) && !retired {
			return fmt.Errorf(
				"backfill candidate job for %s: load publication: %w",
				repo.Name, publicationErr,
			)
		}
		if retired {
			// Candidate v3 cannot remain current under the v4 policy. Clear
			// the derived pointer before runners start and force one
			// replacement. Queue first so a crash cannot clear authority
			// without retaining the replacement request. A failure aborts
			// startup; restart repeats the idempotent reconciliation.
			if err := store.EnqueuePending(
				ctx, st, store.JobCandidate, repo.Name, true,
			); err != nil {
				return fmt.Errorf(
					"backfill candidate job for %s: force retired publication replacement: %w",
					repo.Name, err,
				)
			}
			if err := st.ClearCandidateManifestPublication(
				ctx, repo.Name,
			); err != nil {
				return fmt.Errorf(
					"backfill candidate job for %s: clear retired publication: %w",
					repo.Name, err,
				)
			}
			continue
		}
		if err := store.EnqueueUnlessInFlight(
			ctx, st, store.JobCandidate, repo.Name,
		); err != nil {
			return fmt.Errorf("backfill candidate job for %s: %w", repo.Name, err)
		}
	}
	return nil
}

func repositoryMirrorPresent(dataDir, repository string) bool {
	directory, err := phebssync.SafeRepoDir(dataDir, repository)
	if err != nil {
		return false
	}
	info, err := os.Stat(directory)
	return err == nil && info.IsDir()
}

func candidatePublicationPresent(dataDir, repository string) bool {
	return regularFilePresent(filepath.Join(
		candidatejob.CandidateRoot(dataDir), candidate.ManifestName(repository),
	))
}

func resolverPublicationPresent(dataDir, repository string) bool {
	return regularFilePresent(filepath.Join(
		dataDir, "resolver-catalogs", resolvercatalogid.ManifestName(repository),
	))
}

func regularFilePresent(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
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
		if releaser, ok := evidence.(interface {
			ReleaseOneUnrootedPartitionRun(context.Context) (bool, error)
		}); ok {
			if _, releaseErr := releaser.ReleaseOneUnrootedPartitionRun(ctx); releaseErr != nil {
				return progress, false, releaseErr
			}
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
	runProofBundleMaintenanceWithOwners(ctx, bundles, lifetime, idleInterval, backlogDelay, nil)
}

func runProofBundleMaintenanceWithOwners(
	ctx context.Context, bundles store.ProofBundleRetentionStore, lifetime, idleInterval, backlogDelay time.Duration,
	owners *dispatchadmission.Owners,
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
		turn, entryErr := owners.Enter(ctx)
		if entryErr != nil {
			return
		}
		deleted, backlogLikely, err := runProofBundleSweepPass(ctx, bundles, lifetime)
		if err != nil {
			if ctx.Err() != nil {
				turn.End()
				return
			}
			log.Printf("proof-bundle retention: %v", err)
			delay = idleInterval
			turn.End()
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
		turn.End()
	}
}

// runEvidenceMaintenance checks immediately at boot. Empty stores repeat the
// existing marker and candidate checks per idle interval; a likely backlog is
// processed in bounded bursts separated by a short yield. Pinned proof/checkpoint runs are excluded by the
// store, and each individual deletion transaction has its own fixed row cap.
func runEvidenceMaintenance(
	ctx context.Context, evidence store.EvidenceStore,
	idleInterval, backlogDelay, staleStagedAfter time.Duration,
) {
	runEvidenceMaintenanceWithOwners(ctx, evidence, idleInterval, backlogDelay, staleStagedAfter, nil)
}

func runEvidenceMaintenanceWithOwners(
	ctx context.Context, evidence store.EvidenceStore,
	idleInterval, backlogDelay, staleStagedAfter time.Duration,
	owners *dispatchadmission.Owners,
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
		turn, entryErr := owners.Enter(ctx)
		if entryErr != nil {
			return
		}
		progress, backlogLikely, err := runEvidenceSweepPass(ctx, evidence, staleStagedAfter)
		if err != nil {
			if ctx.Err() != nil {
				turn.End()
				return
			}
			log.Printf("evidence retention: %v", err)
			delay = idleInterval
			turn.End()
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
		turn.End()
	}
}

func codeNavigationRevisionAdmitted(repo store.Repo, revision string) bool {
	if repo.IndexedCommitHash == revision {
		return true
	}
	for _, indexed := range repo.IndexedRevisions {
		if indexed.Commit == revision {
			return true
		}
	}
	return false
}

func codeNavigationRepositoryError(repository string, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf(
			"repository %q is unavailable: %w",
			repository,
			codenav.ErrRevisionNotFound,
		)
	}
	return err
}
