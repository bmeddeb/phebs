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
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/text/unicode/norm"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
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
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/indexer"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	phebsmcp "github.com/bmeddeb/phebs/internal/mcp"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/retentionstatus"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogingest"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/ui"
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

func partitionFenceAuthority(ctx context.Context, root, repository string) (string, string, error) {
	if _, err := observationpublication.ReadInventoryPublicationRootV2Context(ctx, root, repository); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("%w: %v", errPartitionAuthorityPending, err)
		}
		return "", "", err
	}
	authority, err := observationpublication.CurrentInventoryAuthorityReferenceV2(ctx, root, repository)
	return authority.SourceGenerationDigest, authority.ObservationGenerationDigest, err
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
	candidateManifest, sourceGeneration, observationGeneration string,
	current func(context.Context, string) (candidate.DownstreamDomainAuthority, error),
) bool {
	if current == nil || len(domains) == 0 || candidateManifest == "" ||
		sourceGeneration == "" || observationGeneration == "" {
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

// runPhebs returns through owned exact-mode cleanup before main can exit.
// Ordinary invocations keep the absent-selector path with no producer state.
func runPhebs(args []string) (code int, retErr error) {
	lifetime, err := dispatchadmission.BootstrapProduction(context.Background())
	if err != nil {
		return 1, err
	}
	if lifetime != nil {
		defer func() {
			retErr = errors.Join(retErr, lifetime.Close(context.Background()))
			if retErr != nil {
				code = 1
			}
		}()
	}
	if len(args) == 0 {
		printUsage()
		return 2, nil
	}
	switch args[0] {
	case "serve":
		err = serve(args[1:])
		// The flag package already printed usage/diagnostics. Preserve its
		// ordinary exit codes, but return through any admitted lifetime close.
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		if errors.Is(err, errServeFlags) {
			return 2, nil
		}
	case "backup":
		err = backup(args[1:])
	case "restore":
		err = restore(args[1:])
	case "version":
		err = printVersion(args[1:], os.Stdout)
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
	ctx, cancel := signal.NotifyContext(dispatchadmission.ProcessContext(), os.Interrupt, syscall.SIGTERM)
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
	ctx, cancel := signal.NotifyContext(dispatchadmission.ProcessContext(), os.Interrupt, syscall.SIGTERM)
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

func serve(args []string) (retErr error) {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := flags.String("config", "", "path to config file (defaults apply if omitted)")
	addr := flags.String("addr", "", "listen address (overrides config)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %w", errServeFlags, err)
	}
	exactReports, err := t4013ExactReportsEnabled()
	if err != nil {
		return err
	}
	exactReads, err := t421ExactReadsEnabled()
	if err != nil {
		return err
	}
	semanticLaunch, err := readT422SemanticLaunch(dispatchadmission.ProcessContext(), os.Stdin)
	if err != nil {
		return err
	}
	if err := semanticLaunch.validateServeSelection(*addr, flags.Args(), exactReads, exactReports); err != nil {
		return err
	}
	reportT4013Startup("process_started")

	cfg, rawConfig, err := semanticLaunch.loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	reportT4013Startup("config_loaded")
	if fixture := os.Getenv("PHEBS_T307_NEUTRAL_SERVICE_REPO"); fixture != "" {
		if err := bindT307NeutralServiceDemo(cfg, fixture); err != nil {
			return err
		}
		log.Printf(
			"WARNING: neutral T30.7 focused-service demo enabled from %s; provisional evidence remains validation-gated",
			fixture,
		)
	}
	if catalog := os.Getenv("PHEBS_T335_SERVICE_CATALOG"); catalog != "" {
		fixture := os.Getenv("PHEBS_T307_NEUTRAL_SERVICE_REPO")
		if err := bindT335ServiceDirectoryDemo(cfg, fixture, catalog); err != nil {
			return err
		}
		log.Printf(
			"WARNING: neutral T33.5 multi-service directory enabled from %s; catalog metadata establishes no relationship or accuracy claim",
			catalog,
		)
	}
	if fixture, catalog := os.Getenv("PHEBS_T344_SERVICE_SEARCH_REPO"),
		os.Getenv("PHEBS_T344_SERVICE_SEARCH_CATALOG"); fixture != "" || catalog != "" {
		if err := bindT344ServiceSearchDemo(cfg, fixture, catalog); err != nil {
			return err
		}
		log.Printf(
			"WARNING: neutral T34.4 whole-repository service-search demo enabled; scope receipts establish no evidence, accuracy, or release claim",
		)
	}
	if fixture := os.Getenv("PHEBS_WORKBENCH_CLOSURE_REPO"); fixture != "" {
		if err := bindSyntheticWorkbenchClosureDemo(cfg, fixture); err != nil {
			return err
		}
		log.Printf(
			"WARNING: synthetic Workbench closure repository enabled from %s; not production evidence",
			fixture,
		)
	}
	if fixture := os.Getenv("PHEBS_THRIFT_FIELD_DEMO_REPO"); fixture != "" {
		if err := bindSyntheticThriftFieldDemo(cfg, fixture); err != nil {
			return err
		}
		log.Printf(
			"WARNING: synthetic Thrift field-zero repository enabled from %s; not production evidence",
			fixture,
		)
	}
	if *addr != "" {
		cfg.Server.Addr = *addr
	}
	exs := evidenceExtractors(
		cfg.Experimental.ProvisionalProtoExtraction,
		cfg.Experimental.ProvisionalThriftExtraction,
		cfg.Experimental.ProvisionalThriftFieldExtraction,
		cfg.Experimental.ProvisionalKafkaExtraction,
	)
	resolverRegistry, err := resolvermaterialize.NewRegistry(exs)
	if err != nil {
		return fmt.Errorf("configure resolver adapters: %w", err)
	}
	callerRegistry, err := callerexecute.NewRegistry(exs)
	if err != nil {
		return fmt.Errorf("configure caller-leaf adapters: %w", err)
	}
	callerPublications := callerpublication.NewRegistry(
		callerexecute.Root(cfg.Server.DataDir),
	)

	ctx, cancel := signal.NotifyContext(dispatchadmission.ProcessContext(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	owners, err := dispatchadmission.NewProductionOwners(ctx, t422ServerOwnerLimits())
	if err != nil {
		return err
	}
	var startupTurn dispatchadmission.OwnerTurn
	startupEnded := false
	if owners != nil {
		startupTurn, err = owners.Enter(ctx)
		if err != nil {
			return err
		}
		stopOwnerFailure := context.AfterFunc(owners.Context(), cancel)
		defer func() {
			stopOwnerFailure()
			if !startupEnded {
				startupTurn.End()
			}
			retErr = errors.Join(retErr, owners.Err())
		}()
	}
	if err := dispatchadmission.BindProductionOwners(owners); err != nil {
		return err
	}
	var exactReportFailed chan struct{}
	var failExactReport func(error)
	if exactReports {
		exactReportFailed = make(chan struct{})
		var exactReportFailureOnce sync.Once
		failExactReport = func(error) {
			exactReportFailureOnce.Do(func() { close(exactReportFailed) })
			cancel()
		}
	}
	var exactReadFailed chan error
	var failExactRead func(error)
	var exactReadState *t421ExactReadAccountingState
	if exactReads {
		exactReadFailed = make(chan error, 1)
		failExactRead = func(failure error) {
			if exactReadState != nil {
				exactReadState.markFailed()
			}
			select {
			case exactReadFailed <- failure:
				cancel()
			default:
			}
		}
		exactReadState = t421NewExactReadAccountingState(
			t4013ExactReportSink("exact read accounting: "), failExactRead,
		)
		exactReadState.semantic = semanticLaunch
		if semanticLaunch != nil {
			semanticLaunch.fail = failExactRead
		}
	}

	if err := os.MkdirAll(cfg.Server.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	reportT4013Startup("data_directory_ready")
	st, err := openStoreAfterRetentionWarning(
		func(code string) {
			log.Printf("WARNING: %s", code)
		},
		func() (*store.Surreal, error) {
			return store.OpenLocalWithConfig(
				ctx,
				cfg.Server.DataDir,
				recovery.ConfigDigest(rawConfig),
			)
		},
	)
	if err != nil {
		return err
	}
	reportT4013Startup("store_opened")
	defer func() { _ = st.Close(context.Background()) }()
	var callerReader *callerexecute.PublicationReader
	if callerRegistry.Enabled() {
		callerReader, err = callerexecute.NewPublicationReader(
			cfg.Server.DataDir, st, callerRegistry, callerPublications,
		)
		if err != nil {
			return fmt.Errorf("configure caller publication reader: %w", err)
		}
	}

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
			_ = callerPublications.Close()
		})
	}
	defer stopBackground()

	capacityGate := lifecycle.NewGate(cfg.Server.DataDir)
	partitionPublicationRoot := filepath.Join(cfg.Server.DataDir, "extraction-publications")
	acquireLifecycleMutation := func(lockCtx context.Context) (func(), error) {
		return focusedindex.AcquireMutationLock(
			lockCtx, filepath.Join(cfg.Server.DataDir, "index"),
		)
	}
	acquireObservationTransition := func(lockCtx context.Context) (func(), error) {
		return focusedindex.AcquireExclusiveMutationLock(
			lockCtx, filepath.Join(cfg.Server.DataDir, "index"),
		)
	}
	releaseCatalogRepair, err := acquireLifecycleMutation(ctx)
	if err != nil {
		return fmt.Errorf("acquire catalog v3 startup repair lock: %w", err)
	}
	catalogRepair, repairErr := st.RepairServiceCatalogV3Startup(ctx)
	releaseCatalogRepair()
	if repairErr != nil {
		return fmt.Errorf("repair catalog v3 startup state: %w", repairErr)
	}
	if catalogRepair.More {
		log.Printf(
			"catalog v3 startup orphan backlog: scanned=%d deleted=%d",
			catalogRepair.OrphansScanned, catalogRepair.OrphansDeleted,
		)
	}
	lifecycleOwners := []lifecycle.Owner{
		lifecycle.CatalogGenerationOwner{Store: st, Acquire: acquireLifecycleMutation},
		lifecycle.CatalogV3GenerationOwner{Store: st, Acquire: acquireLifecycleMutation},
		lifecycle.GenerationOwner{Store: st, Acquire: acquireLifecycleMutation},
		lifecycle.JobOwnerImpl{Store: st, Acquire: acquireLifecycleMutation},
	}
	searchGenerationPins := &focusedindex.SearchGenerationPins{}
	searchGenerationOwner := lifecycle.SearchGenerationOwnerImpl{
		IndexDir: filepath.Join(cfg.Server.DataDir, "index"),
		Pins:     searchGenerationPins, Acquire: acquireLifecycleMutation,
	}
	lifecycleOwners = append(lifecycleOwners, searchGenerationOwner)
	if semanticLaunch != nil && semanticLaunch.request.ServerEpoch == 1 {
		retention, retentionErr := newT422RetentionControl(ctx, semanticLaunch, searchGenerationOwner, searchGenerationPins)
		if retentionErr != nil {
			return retentionErr
		}
		exactReadState.retention = retention
		defer func() { stopBackground(); retention.Close() }()
	}
	observationCache := &observationpublication.Cache{}
	observationInventoryCache := &observationpublication.InventoryCacheV2{}
	relationshipCache := &relationshippublication.Cache{}
	relationshipV3Cache := &relationshippublication.CacheV3{}
	lifecycleOwners = append(lifecycleOwners, lifecycle.ObservationGenerationOwner{
		Root: filepath.Join(cfg.Server.DataDir, "observations"),
		Pins: observationCache, Acquire: acquireLifecycleMutation,
	})
	lifecycleOwners = append(lifecycleOwners, lifecycle.ObservationInventoryOwnerV2{
		Root:    filepath.Join(cfg.Server.DataDir, "observations"),
		Pins:    observationpublication.InventoryPinsV2{Cache: observationInventoryCache},
		Acquire: acquireLifecycleMutation,
	})
	lifecycleOwners = append(lifecycleOwners, lifecycle.RelationshipGenerationOwner{
		DataDir: cfg.Server.DataDir, Pins: relationshipCache,
		AcquireExclusive: acquireObservationTransition, Store: st,
	})
	lifecycleOwners = append(lifecycleOwners, lifecycle.RelationshipGenerationOwnerV3{
		DataDir: cfg.Server.DataDir, Pins: relationshipV3Cache,
		AcquireExclusive: acquireObservationTransition, Store: st,
	})
	lifecycleOwners = append(lifecycleOwners, lifecycle.ExtractionStageOwner{
		Root: partitionPublicationRoot, Acquire: acquireLifecycleMutation,
	})
	lifecycleOwners = append(lifecycleOwners, lifecycle.ClosedOwners()...)
	lifecycleStatus, lifecycleErr := lifecycle.NewStatusMonitor(
		cfg.Lifecycle.EnabledFor(), lifecycleOwners,
	)
	if lifecycleErr != nil {
		return fmt.Errorf("configure lifecycle status: %w", lifecycleErr)
	}
	observationRuntime := &observationpublication.Runtime{
		DataDir: cfg.Server.DataDir, Store: st, Cache: observationCache,
		AcquireTransition: acquireObservationTransition,
		InventoryV2:       true,
		Admit: func(admitCtx context.Context) error {
			capacity, admissionErr := capacityGate.Check(admitCtx, 0)
			lifecycleStatus.ObserveCapacity(capacity, admissionErr)
			return admissionErr
		},
	}
	var relationshipRuntime *relationshippublication.Runtime
	var reconcileRelationship func(context.Context, string) error
	if resolverRegistry.Enabled() {
		relationshipDomains := make([]downstreamauthority.DomainIdentity, 0, len(exs))
		for _, extractor := range exs {
			relationshipDomains = append(relationshipDomains, downstreamauthority.DomainIdentity{
				Domain: extractor.Domain(), Version: extractor.Version(),
			})
		}
		relationshipRuntime = &relationshippublication.Runtime{
			DataDir: cfg.Server.DataDir, Store: st, Cache: observationCache,
			InventoryCache: observationInventoryCache, Domains: relationshipDomains,
			Acquire: acquireLifecycleMutation,
			Admit: func(admitCtx context.Context) error {
				capacity, admissionErr := capacityGate.Check(admitCtx, 0)
				lifecycleStatus.ObserveCapacity(capacity, admissionErr)
				return admissionErr
			},
		}
		reconcileRelationship = func(reconcileCtx context.Context, repository string) error {
			err := relationshipRuntime.Reconcile(reconcileCtx, repository)
			if errors.Is(err, relationshippublication.ErrNotFound) {
				diagnostics.Logf(
					"relationship authority not ready: repository=%q error=%v",
					repository, err,
				)
				return nil
			}
			return err
		}
		observationRuntime.OnPublished = reconcileRelationship
	}
	if len(exs) > 0 {
		observationAfterPublication := observationRuntime.OnPublished
		observationRuntime.OnPublished = func(
			publishedCtx context.Context,
			repository string,
		) error {
			return afterObservationPublication(
				publishedCtx,
				repository,
				legacyRelationshipReconcileFor(
					repository, cfg.ServiceCatalogs, observationAfterPublication,
				),
				st.EnqueuePending,
			)
		}
	}
	releaseObservationRecovery, recoveryLockErr := acquireObservationTransition(ctx)
	if recoveryLockErr != nil {
		return fmt.Errorf("acquire observation v2 startup recovery lock: %w", recoveryLockErr)
	}
	observationV2Recovery, observationV2RecoveryErr :=
		observationpublication.RecoverInventoryPublicationsV2(
			ctx, filepath.Join(cfg.Server.DataDir, "observations"),
		)
	relationshipRecovery, relationshipRecoveryErr := relationshippublication.RecoverAll(
		ctx, cfg.Server.DataDir, st,
	)
	releaseObservationRecovery()
	if observationV2RecoveryErr != nil {
		return fmt.Errorf("recover observation v2 publications: %w", observationV2RecoveryErr)
	}
	if observationV2Recovery.Repositories > 0 {
		diagnostics.Logf(
			"observation v2 recovery: repositories=%d completed=%d incomplete=%d",
			observationV2Recovery.Repositories, observationV2Recovery.Completed,
			observationV2Recovery.Incomplete,
		)
	}
	if relationshipRecoveryErr != nil {
		return fmt.Errorf("recover relationship publications: %w", relationshipRecoveryErr)
	}
	if relationshipRecovery.Repositories > 0 {
		diagnostics.Logf(
			"relationship recovery: repositories=%d completed=%d unavailable=%d invalid=%d",
			relationshipRecovery.Repositories, relationshipRecovery.Completed,
			relationshipRecovery.Unavailable, relationshipRecovery.Invalid,
		)
	}
	releaseExtractionStageRecovery, extractionStageRecoveryLockErr :=
		acquireLifecycleMutation(ctx)
	if extractionStageRecoveryLockErr != nil {
		return fmt.Errorf("lock extraction publication stage recovery: %w", extractionStageRecoveryLockErr)
	}
	var extractionStageRecovery extractionpublication.StageRecoveryReport
	var extractionStageRecoveryErr error
	func() {
		defer releaseExtractionStageRecovery()
		extractionStageRecovery, extractionStageRecoveryErr =
			extractionpublication.RecoverStages(ctx, partitionPublicationRoot)
	}()
	if extractionStageRecoveryErr != nil {
		return fmt.Errorf("recover extraction publication stages: %w", extractionStageRecoveryErr)
	}
	if extractionStageRecovery.Repositories > 0 {
		diagnostics.Logf(
			"extraction stage recovery: repositories=%d retired=%d work=%d",
			extractionStageRecovery.Repositories, extractionStageRecovery.Retired,
			extractionStageRecovery.Work,
		)
	}
	reportT4013Startup("authority_recovery_complete")
	var lifecycleController *lifecycle.Controller
	var lifecycleControl *t422LifecycleControl
	var lifecycleRunnerControl *lifecycle.RunnerControl
	if cfg.Lifecycle.EnabledFor() {
		lifecycleController, lifecycleErr = lifecycle.NewController(
			st, lifecycleOwners...,
		)
		if lifecycleErr != nil {
			return fmt.Errorf("configure lifecycle maintenance: %w", lifecycleErr)
		}
	}
	if semanticLaunch != nil {
		if lifecycleController == nil {
			return errT422LifecycleControl
		}
		lifecycleControl, err = newT422LifecycleControl(ctx, semanticLaunch, lifecycleOwners)
		if err != nil {
			return err
		}
		lifecycleRunnerControl = lifecycleControl.runner
		exactReadState.lifecycle = lifecycleControl
	}

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

	authService, err := auth.New(ctx, auth.Options{Config: cfg.Auth, Store: st, Audit: auditRecord, Owners: owners})
	if err != nil {
		return err
	}
	if owners != nil {
		defer func() {
			cancel()
			authService.WaitCleanup()
		}()
	}
	// T10.1/T10.2 retention sweep: boot, then twice a day
	auditRetention, usageRetention := cfg.Audit.RetentionFor(), cfg.Analytics.RetentionFor()
	if auditRetention > 0 || usageRetention > 0 {
		runBackground(func() {
			ticker := time.NewTicker(12 * time.Hour)
			defer ticker.Stop()
			for {
				turn, err := owners.Enter(ctx)
				if err != nil {
					return
				}
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
				turn.End()
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
	// Stages have no committed owner after process restart. Clean them only at
	// this startup seam, before any candidate worker can be active; runtime
	// orphan reconciliation must never race an in-progress plan.
	if removed, err := candidate.CleanupStages(
		ctx, candidatejob.CandidateRoot(cfg.Server.DataDir),
	); err != nil {
		return fmt.Errorf("cleanup abandoned candidate stages: %w", err)
	} else if removed > 0 {
		log.Printf("candidate reconciliation: removed %d abandoned stage(s)", removed)
	}
	if removed, err := callerleaf.CleanupStages(
		ctx, callerexecute.Root(cfg.Server.DataDir),
	); err != nil {
		return fmt.Errorf("cleanup abandoned caller-leaf stages: %w", err)
	} else if removed > 0 {
		log.Printf("caller-leaf reconciliation: removed %d abandoned stage(s)", removed)
	}
	catalogReport, err := resolvercatalog.Reconcile(
		ctx, filepath.Join(cfg.Server.DataDir, "resolver-catalogs"), st,
		resolverRegistry.Packs(),
	)
	if err != nil {
		return fmt.Errorf("resolver catalog reconciliation: %w", err)
	}
	if catalogReport.StagesRemoved+catalogReport.MarkersRecovered+
		catalogReport.PublicationsCurrent+catalogReport.ReplacementsQueued+
		catalogReport.PointersCleared+catalogReport.OrphansObserved > 0 {
		log.Printf(
			"resolver catalog reconciliation: stages_removed=%d markers_recovered=%d current=%d replacements_queued=%d pointers_cleared=%d orphans=%d",
			catalogReport.StagesRemoved, catalogReport.MarkersRecovered,
			catalogReport.PublicationsCurrent, catalogReport.ReplacementsQueued,
			catalogReport.PointersCleared, catalogReport.OrphansObserved,
		)
	}
	if !callerRegistry.Enabled() {
		if err := callerpublication.ReconcileDeletionMarkers(
			ctx, callerexecute.Root(cfg.Server.DataDir),
			st.CallerPublicationRepositoryEligible,
		); err != nil {
			return fmt.Errorf("caller publication deletion reconciliation: %w", err)
		}
	}
	if callerRegistry.Enabled() {
		callerReport, err := callerexecute.ReconcilePublications(
			ctx, cfg.Server.DataDir, st, callerRegistry, callerPublications,
		)
		if err != nil {
			return fmt.Errorf("caller publication reconciliation: %w", err)
		}
		if callerReport.StagesRemoved+callerReport.MarkersRecovered+
			callerReport.PublicationsCurrent+callerReport.ReplacementsQueued+
			callerReport.PointersCleared+callerReport.OrphansObserved > 0 {
			log.Printf(
				"caller publication reconciliation: stages_removed=%d markers_recovered=%d current=%d replacements_queued=%d pointers_cleared=%d orphans=%d",
				callerReport.StagesRemoved, callerReport.MarkersRecovered,
				callerReport.PublicationsCurrent, callerReport.ReplacementsQueued,
				callerReport.PointersCleared, callerReport.OrphansObserved,
			)
		}
	}
	report, reconcileErr := phebssync.ReconcileArtifactsWithCallerLifecycle(
		ctx, st, cfg.Server.DataDir, cfg.Sync.CleanupOrphans,
		callerPublications,
	)
	if reconcileErr != nil {
		// Reconciliation establishes the artifact/search trust boundary. A
		// failed quarantine, revision clear, or credential scrub must not leave
		// the server running against state it could not prove safe.
		return fmt.Errorf("artifact reconciliation: %w", reconcileErr)
	}
	if report.OrphanRepos+report.UntrackedShards+report.UntrackedMirrors+
		report.UntrackedCandidates+report.CredentialsFixed+report.InvalidRepos+
		report.RevisionRepairs+report.LifecycleArtifacts > 0 {
		log.Printf("artifact reconciliation: orphans=%d shards=%d mirrors=%d candidates=%d credentials_scrubbed=%d invalid_repos=%d revision_repairs=%d lifecycle=%d deleted=%d",
			report.OrphanRepos, report.UntrackedShards, report.UntrackedMirrors,
			report.UntrackedCandidates, report.CredentialsFixed,
			report.InvalidRepos, report.RevisionRepairs, report.LifecycleArtifacts,
			report.Deleted)
	}
	reportT4013Startup("artifact_recovery_complete")
	analysisUnits := cfg.AnalysisUnitScopes()
	unitRepositories := make([]string, 0, len(analysisUnits))
	for repository := range analysisUnits {
		unitRepositories = append(unitRepositories, repository)
	}
	sort.Strings(unitRepositories)
	for _, repository := range unitRepositories {
		state, stateErr := analysisUnits[repository].State()
		if stateErr != nil {
			return fmt.Errorf("analysis unit %s: %w", repository, stateErr)
		}
		logAnalysisUnitPosture(repository, state, exs)
	}
	if len(unitRepositories) == 0 {
		logAnalysisUnitPosture("", nil, exs)
	}
	if queued, err := indexer.ReconcileAnalysisUnits(ctx, st, analysisUnits); err != nil {
		return fmt.Errorf("reconcile analysis units: %w", err)
	} else if queued > 0 {
		log.Printf("analysis unit reconciliation: queued %d index rebuild(s)", queued)
	}
	catalogReconciler := &servicecatalogingest.Reconciler{
		DataDir: cfg.Server.DataDir, Store: st,
		Selections: legacyServiceCatalogSelections(cfg.ServiceCatalogs),
	}
	v3CatalogReconciler := &servicecatalogingest.V3Reconciler{
		DataDir: cfg.Server.DataDir, Store: st,
		Selections: cfg.ServiceCatalogs,
	}
	serviceRuntime := newServiceRuntimeController(
		cfg.Server.DataDir, st, cfg.ServiceCatalogs, v3CatalogReconciler,
		relationshipRuntime, acquireLifecycleMutation, searchGenerationPins,
		relationshipCache, relationshipV3Cache,
	)
	var markerControl *t422MarkerControl
	if semanticLaunch != nil && semanticLaunch.request.ServerEpoch == 3 {
		markerControl, err = newT422MarkerControl(ctx, semanticLaunch, relationshipRuntime, serviceRuntime, acquireObservationTransition)
		if err != nil {
			return err
		}
		exactReadState.marker = markerControl
	}
	catalogReconciler.WithMutation = serviceRuntime.withV2Mutation
	defer func() {
		stopBackground()
		serviceRuntime.Close()
	}()
	catalogReconciler.OnPublished = func(
		publishedCtx context.Context,
		repository string,
	) error {
		var relationshipErr error
		if relationshipRuntime != nil {
			relationshipErr = afterServiceCatalogPublication(
				publishedCtx, repository, reconcileRelationship,
				st.EnqueuePending,
				candidatePublicationPresent(cfg.Server.DataDir, repository),
			)
		}
		return errors.Join(
			relationshipErr,
			serviceRuntime.Advance(publishedCtx, repository),
		)
	}
	serviceCatalogReport, err := catalogReconciler.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("reconcile service catalogs: %w", err)
	}
	if serviceCatalogReport.Current+serviceCatalogReport.Published+
		serviceCatalogReport.LegacyImported+serviceCatalogReport.NotReady+
		serviceCatalogReport.Unselected+len(serviceCatalogReport.Failures) > 0 {
		diagnostics.Logf(
			"service catalog reconciliation: current=%d published=%d legacy_imported=%d not_ready=%d unselected=%d failed=%d",
			serviceCatalogReport.Current, serviceCatalogReport.Published,
			serviceCatalogReport.LegacyImported, serviceCatalogReport.NotReady,
			serviceCatalogReport.Unselected, len(serviceCatalogReport.Failures),
		)
	}
	for _, failure := range serviceCatalogReport.Failures {
		diagnostics.Logf(
			"service catalog reconciliation failed: repository=%q error=%v",
			failure.Repository, failure.Err,
		)
	}
	serviceRepositories := make(map[string]struct{},
		len(cfg.ServiceCatalogs)+len(analysisUnits))
	for repository := range cfg.ServiceCatalogs {
		serviceRepositories[repository] = struct{}{}
	}
	for repository := range analysisUnits {
		serviceRepositories[repository] = struct{}{}
	}
	serviceNames := make([]string, 0, len(serviceRepositories))
	for repository := range serviceRepositories {
		serviceNames = append(serviceNames, repository)
	}
	sort.Strings(serviceNames)
	for _, repository := range serviceNames {
		if selection, configured := cfg.ServiceCatalogs[repository]; configured {
			// Reconciler.OnPublished already advanced the selected runtime under
			// its transition fence for v2. V3 deliberately bypasses the legacy
			// 4,000-service decoder and starts through its own reconciler.
			if selection.RuntimeVersion() == config.ServiceCatalogRuntimeV3 {
				if err := serviceRuntime.Advance(ctx, repository); err != nil {
					diagnostics.Logf(
						"service runtime v3 reconciliation unavailable: repository=%q error=%v",
						repository, err,
					)
				}
			}
			continue
		}
		outcome, reconcileErr := reconcileServiceSearchGeneration(
			ctx, st, cfg.Server.DataDir, repository,
		)
		if reconcileErr != nil {
			diagnostics.Logf(
				"service search reconciliation unavailable: repository=%q error=%v",
				repository, reconcileErr,
			)
			continue
		}
		if outcome.Activated > 0 {
			diagnostics.Logf(
				"service search reconciliation: repository=%q activated=%d search_generation=%s",
				repository, outcome.Activated, outcome.Search.Digest,
			)
		}
	}
	if err := serviceRuntime.PinSelections(ctx); err != nil {
		return fmt.Errorf("validate and pin selected service runtimes: %w", err)
	}
	if lifecycleController != nil {
		runBackground(func() {
			lifecycle.RunWithControl(
				ctx, lifecycleController, capacityGate,
				lifecycle.DefaultIdleInterval, lifecycle.DefaultBacklogDelay,
				func(result lifecycle.OwnerResult) {
					if lifecycleControl != nil {
						lifecycleControl.ObserveOwner(result)
					}
					lifecycleStatus.ObserveOwner(result)
					if result.Err != nil {
						diagnostics.Logf(
							"lifecycle owner=%q completeness=%s: %v",
							result.Owner, result.Completeness, result.Err,
						)
					} else if result.Deleted > 0 {
						diagnostics.Logf(
							"lifecycle owner=%q completeness=%s scanned=%d deleted=%d backlog=%t",
							result.Owner, result.Completeness, result.Scanned,
							result.Deleted, result.More,
						)
					}
				},
				lifecycleStatus.ObserveCapacity,
				owners,
				lifecycleRunnerControl,
			)
		})
	}
	if err := phebssync.EnqueueMissing(ctx, st, cfg); err != nil {
		return fmt.Errorf("enqueue sync jobs: %w", err)
	}
	runner := &store.Runner{Store: st, Kind: store.JobSync, Owners: owners,
		Handle: phebssync.HandlerWithLifecycles(
			cfg, st, callerPublications, serviceRuntime,
		),
		Interval: cfg.Sync.Interval(), Diagnostics: cfg.Diagnostics.Jobs}
	fetchRunner := &store.Runner{Store: st, Kind: store.JobFetch, Handle: phebssync.FetchHandler(cfg, st), Owners: owners,
		Interval: cfg.Sync.Interval(), Diagnostics: cfg.Diagnostics.Jobs}
	bindT4013ExactReports(exactReports, failExactReport, nil, runner, fetchRunner)
	runBackground(func() { runner.Run(ctx) })
	runBackground(func() { fetchRunner.Run(ctx) })
	if watched := phebssync.Watched(cfg); len(watched) > 0 {
		log.Printf("watch mode: polling %d local repo(s)", len(watched))
		runBackground(func() {
			(&phebssync.Watcher{Store: st, Conns: watched, Revisions: cfg.Revisions, Owners: owners}).Run(ctx)
		})
	}
	// T7.5: periodic freshness for remote connections
	if every := cfg.Sync.ResyncEvery(); every > 0 {
		runBackground(func() { phebssync.ResyncWithOwners(ctx, st, cfg, every, owners) })
	}

	// Candidate planning and extraction are independent queue consumers: they
	// must drain boot backfill even when this binary cannot start new zoekt
	// index children. Each runner processes repositories serially, bounding
	// Git/parser resource use at this integration seam.
	var onIndexed func(context.Context, string, string) error
	if len(cfg.ServiceCatalogs)+len(analysisUnits) > 0 {
		onIndexed = func(ctx context.Context, repository, _ string) error {
			selection, selected := cfg.ServiceCatalogs[repository]
			if !selected {
				if _, legacy := analysisUnits[repository]; !legacy {
					return nil
				}
			}
			if selected && selection.RuntimeVersion() == config.ServiceCatalogRuntimeV3 {
				return serviceRuntime.Advance(ctx, repository)
			}
			if _, err := catalogReconciler.ReconcileRepository(ctx, repository); err != nil {
				return err
			}
			if _, configured := cfg.ServiceCatalogs[repository]; configured {
				// The catalog callback already advanced the selected runtime.
				return nil
			}
			_, err := reconcileServiceSearchGeneration(
				ctx, st, cfg.Server.DataDir, repository,
			)
			return err
		}
	}
	catalogAfterIndex := onIndexed
	onIndexed = chainObservationPlanningAfterIndex(
		catalogAfterIndex,
		func(repository string) bool {
			_, focused := analysisUnits[repository]
			return focused
		},
		observationRuntime.EnqueuePlanning,
		func(disposition observationpublication.PlanningEnqueue) {
			diagnostics.Logf(
				"observation planning: disposition=%s", disposition,
			)
		},
	)
	if repositories, listErr := st.ListRepos(ctx); listErr != nil {
		return fmt.Errorf("list repositories for observation recovery: %w", listErr)
	} else {
		summary, recoveryErr := enqueueObservationPlanningStartup(
			ctx, repositories, observationRuntime.EnqueuePlanning,
		)
		if recoveryErr != nil {
			return recoveryErr
		}
		if summary.total() > 0 {
			diagnostics.Logf(
				"observation planning recovery: current=%d active=%d failed=%d enqueued=%d unavailable=%d",
				summary.Current, summary.Active, summary.Failed,
				summary.Enqueued, summary.Unavailable,
			)
		}
	}
	observationScheduler := &generationscheduler.Scheduler{
		Store: st, Owners: owners,
		Classes: map[store.GenerationResourceClass]generationscheduler.Class{
			store.GenerationResourceIO: {
				Concurrency: 1,
				Budget: generationscheduler.Budget{
					MaxMemoryBytes: 256 << 20, MaxDescriptors: 8,
				},
				Handle: func(workerCtx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
					switch chunk.Stage {
					case observationpublication.PlanningScheduleStage:
						return observationRuntime.HandlePlanning(workerCtx, chunk)
					case observationpublication.InventoryScheduleStageV2:
						return observationRuntime.HandleInventoryV2(workerCtx, chunk)
					default:
						return store.WithTerminal(errors.New("unknown observation IO stage"))
					}
				},
			},
			store.GenerationResourceCPU: {
				Concurrency: 2,
				Budget: generationscheduler.Budget{
					MaxMemoryBytes: 256 << 20, MaxDescriptors: 8,
				},
				Handle: func(workerCtx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
					switch chunk.Stage {
					case store.ServiceStateV3ReconcileStage,
						store.ServiceStateV3ActivateStage:
						_, err := serviceRuntime.ProcessServiceStateV3Chunk(workerCtx, chunk)
						return err
					default:
						return observationRuntime.Handle(workerCtx, chunk)
					}
				},
			},
		},
		PollEvery: time.Second, WorkerPrefix: "observation-worker",
		Report: func(err error) {
			diagnostics.Logf("observation scheduler unavailable: %v", err)
		},
	}
	bindT422ExactChunkReports(exactReads, failExactRead, observationScheduler)
	runBackground(func() {
		if err := observationScheduler.Run(ctx); err != nil && ctx.Err() == nil {
			diagnostics.Logf("observation scheduler stopped: %v", err)
		}
	})
	if relationshipRuntime != nil {
		relationshipScheduler := &generationscheduler.Scheduler{
			Store: st, Owners: owners,
			Classes: map[store.GenerationResourceClass]generationscheduler.Class{
				store.GenerationResourceMemory: {
					Concurrency: 1,
					Budget: generationscheduler.Budget{
						MaxMemoryBytes: 1 << 30, MaxDescriptors: 32,
					},
					Handle: func(workerCtx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
						var err error
						if chunk.Stage == relationshippublication.ScheduleStageV3 {
							if markerControl != nil {
								return markerControl.handleInEpoch(workerCtx, chunk)
							}
							err = relationshipRuntime.HandleV3(workerCtx, chunk)
						} else {
							err = relationshipRuntime.Handle(workerCtx, chunk)
						}
						if err == nil {
							err = serviceRuntime.Advance(workerCtx, chunk.Repository)
						}
						return err
					},
				},
			},
			PollEvery: time.Second, WorkerPrefix: "relationship-worker",
			Report: func(err error) {
				diagnostics.Logf("relationship scheduler unavailable: %v", err)
			},
		}
		bindT422ExactChunkReports(exactReads, failExactRead, relationshipScheduler)
		runBackground(func() {
			if err := relationshipScheduler.Run(ctx); err != nil && ctx.Err() == nil {
				diagnostics.Logf("relationship scheduler stopped: %v", err)
			}
		})
	}
	var evidenceView store.EvidenceStore
	var proofBundles store.ProofBundleStore
	var compatibility compat.Service
	var partitionRuntime *extractionpublication.Runtime
	var manifestProvider *candidatejob.Provider
	var openPartitionDomain func(
		context.Context, candidate.DomainResultPlan,
	) (*candidate.SparseDomain, error)
	if len(exs) > 0 {
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
		if cfg.Experimental.ProvisionalWorkbench {
			log.Print("WARNING: experimental provisional Change Workbench enabled; no runtime-use, completeness, migration-completion, decommission-safety, or extraction-accuracy claim is established")
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
		candidateWorker, provider, err := candidatejob.New(
			cfg.Server.DataDir, st, exs,
		)
		if err != nil {
			return fmt.Errorf("configure candidate planning: %w", err)
		}
		manifestProvider = provider
		worker := &extract.Worker{
			Repos: st, Evidence: st,
			NewCorpus:        extract.GitCorpus(cfg.Server.DataDir),
			Manifests:        manifestProvider,
			Extractors:       exs,
			Diagnostics:      cfg.Diagnostics.Extraction,
			ExtractorDetails: cfg.Diagnostics.ExtractorDetails,
		}
		openPartitionCandidate := func(
			openCtx context.Context,
			repository string,
		) (*candidate.Publication, error) {
			var unit *analysisunit.State
			if scope, configured := analysisUnits[repository]; configured {
				state, stateErr := scope.State()
				if stateErr != nil {
					return nil, stateErr
				}
				unit = state
			}
			return manifestProvider.OpenCurrentPublication(openCtx, repository, unit)
		}
		readPartitionCandidateReference := func(
			referenceCtx context.Context,
			repository string,
		) (candidate.State, error) {
			var unit *analysisunit.State
			if scope, configured := analysisUnits[repository]; configured {
				state, stateErr := scope.State()
				if stateErr != nil {
					return candidate.State{}, stateErr
				}
				unit = state
			}
			return manifestProvider.CurrentPublicationState(referenceCtx, repository, unit)
		}
		readPartitionAuthority := func(
			authorityCtx context.Context,
			repository string,
		) (string, string, error) {
			authority, authorityErr := observationpublication.CurrentInventoryAuthorityV2(
				authorityCtx, filepath.Join(cfg.Server.DataDir, "observations"), repository,
			)
			return authority.SourceGenerationDigest,
				authority.ObservationGenerationDigest, authorityErr
		}
		readPartitionFenceAuthority := func(
			authorityCtx context.Context,
			repository string,
		) (string, string, error) {
			return partitionFenceAuthority(
				authorityCtx, filepath.Join(cfg.Server.DataDir, "observations"), repository,
			)
		}
		partitionRuntime = &extractionpublication.Runtime{
			Root: partitionPublicationRoot, Store: st,
			Executor:    &extract.EvidencePartitionExecutor{Evidence: st, Extractors: exs},
			Publisher:   extractionpublication.StorePublisher{Store: st},
			Diagnostics: cfg.Diagnostics.Extraction,
		}
		partitionDomains := make([]string, len(exs))
		for index, extractor := range exs {
			partitionDomains[index] = extractor.Domain()
		}
		partitionPublicationsCurrent := func(
			currentCtx context.Context,
			repository string,
		) bool {
			candidateState, candidateErr := readPartitionCandidateReference(
				currentCtx, repository,
			)
			source, observation, authorityErr := readPartitionFenceAuthority(
				currentCtx, repository,
			)
			if candidateErr != nil || authorityErr != nil {
				return false
			}
			return allPartitionDomainsMatch(
				currentCtx, partitionDomains, candidateState.ManifestDigest,
				source, observation,
				func(
					domainCtx context.Context, domain string,
				) (candidate.DownstreamDomainAuthority, error) {
					// Store publication is the downstream authority for every
					// settled disposition, including explicit empty, terminal,
					// and retryable domain roots that intentionally have no
					// successful filesystem current pointer.
					return extractionpublication.CurrentDomainAuthority(
						domainCtx, st, repository, domain,
					)
				},
			)
		}
		partitionRuntime.OnSettled = func(
			settledCtx context.Context,
			repository string,
		) error {
			partitionsCurrent := partitionPublicationsCurrent(
				settledCtx, repository,
			)
			return afterPartitionExtractionSettlement(
				settledCtx, repository,
				legacyRelationshipReconcileFor(
					repository, cfg.ServiceCatalogs, reconcileRelationship,
				),
				st.EnqueuePending,
				resolverRegistry.Enabled() && partitionsCurrent,
				callerRegistry.Enabled() && partitionsCurrent &&
					resolverPublicationPresent(cfg.Server.DataDir, repository),
			)
		}
		partitionReconciler := &extractionpublication.Reconciler{
			Root: partitionPublicationRoot, CandidateRoot: candidatejob.CandidateRoot(cfg.Server.DataDir),
			Runtime: partitionRuntime, Evidence: st,
			OpenCandidate: openPartitionCandidate, Authority: readPartitionAuthority,
			CandidateReference: readPartitionCandidateReference,
			AuthorityReference: readPartitionFenceAuthority,
		}
		openPartitionDomain = partitionReconciler.OpenDomain
		partitionRuntime.Source = extractionpublication.GitSparseSource{
			DataDir: cfg.Server.DataDir, OpenDomain: partitionReconciler.OpenDomain,
		}
		partitionRuntime.Fence = extractionpublication.AuthorityFence{
			Store: st, Acquire: acquireObservationTransition,
			Current: func(fenceCtx context.Context, plan candidate.DomainResultPlan) error {
				publication, fenceErr := openPartitionCandidate(fenceCtx, plan.Repository)
				if fenceErr != nil {
					return fenceErr
				}
				state := publication.State()
				if state.ManifestDigest != plan.CandidateManifestDigest ||
					state.GenerationDigest != plan.CandidateGenerationDigest ||
					state.PolicyDigest != plan.CandidatePolicyDigest {
					return extractionpublication.ErrStale
				}
				source, observation, fenceErr := readPartitionFenceAuthority(fenceCtx, plan.Repository)
				if fenceErr != nil {
					return fenceErr
				}
				if source != plan.SourceGenerationDigest ||
					observation != plan.ObservationGenerationDigest {
					return extractionpublication.ErrStale
				}
				return nil
			},
		}
		candidateWorker.Diagnostics = cfg.Diagnostics.Candidates
		if err := enqueueCandidateBackfillWithReadiness(
			ctx, st, candidateWorker.PolicyDigest(), func(repository string) bool {
				return repositoryMirrorPresent(cfg.Server.DataDir, repository)
			},
		); err != nil {
			return err
		}
		var resolverRunner, callerRunner *store.Runner
		if resolverRegistry.Enabled() {
			resolverWorker, err := resolvermaterialize.NewWorker(
				cfg.Server.DataDir, st, manifestProvider, resolverRegistry,
			)
			if err != nil {
				return fmt.Errorf("configure resolver materialization: %w", err)
			}
			if relationshipRuntime != nil {
				resolverWorker.OnPublished = func(
					publishedCtx context.Context,
					repository string,
				) error {
					return afterResolverPublication(
						publishedCtx, repository,
						legacyRelationshipReconcileFor(
							repository, cfg.ServiceCatalogs, reconcileRelationship,
						),
						serviceRuntime.Advance,
						st.EnqueuePending, callerRegistry.Enabled(),
					)
				}
			}
			resolverRunner = &store.Runner{
				Store: st, Kind: store.JobResolverCatalog, Owners: owners,
				Handle: func(jobCtx context.Context, job store.Job) error {
					return resolverWorker.Handle(jobCtx, job)
				},
				Interval:    cfg.Sync.Interval(),
				Diagnostics: cfg.Diagnostics.Jobs,
			}
		}
		if callerRegistry.Enabled() {
			callerWorker, err := callerexecute.NewWorkerWithPublicationRegistry(
				cfg.Server.DataDir, st, manifestProvider, callerRegistry,
				callerPublications,
			)
			if err != nil {
				return fmt.Errorf("configure caller-leaf execution: %w", err)
			}
			if relationshipRuntime != nil {
				callerWorker.OnPublished = func(
					publishedCtx context.Context,
					repository string,
				) error {
					reconcile := legacyRelationshipReconcileFor(
						repository, cfg.ServiceCatalogs, reconcileRelationship,
					)
					if reconcile == nil {
						return nil
					}
					return reconcile(publishedCtx, repository)
				}
			}
			callerRunner = &store.Runner{
				Store: st, Kind: store.JobCallerLeaf, Owners: owners,
				Handle: func(jobCtx context.Context, job store.Job) error {
					return callerWorker.Handle(jobCtx, job)
				},
				Interval:    cfg.Sync.Interval(),
				Diagnostics: cfg.Diagnostics.Jobs,
			}
		}
		candidateRunner := &store.Runner{
			Store: st, Kind: store.JobCandidate, Handle: candidateWorker.Handle, Owners: owners,
			Interval: cfg.Sync.Interval(), Diagnostics: cfg.Diagnostics.Jobs,
		}
		exRunner := &store.Runner{Store: st, Kind: store.JobExtract, Owners: owners, Handle: func(
			jobCtx context.Context,
			job store.Job,
		) error {
			if !candidatePublicationPresent(cfg.Server.DataDir, job.Target) {
				diagnostics.Logf(
					"extraction deferred until candidate publication: repository=%q",
					job.Target,
				)
				return nil
			}
			// Focused analysis units still publish through the T30 legacy writer.
			// Whole-repository generations moved to partitioned authority in
			// T40.10-T40.12; running both writers would make the legacy worker hold
			// the repository lock across the entire corpus and starve the v2 plan.
			if legacyExtractionRequired(job.Target, analysisUnits) {
				return worker.Handle(jobCtx, job)
			}
			_, partitionErr := partitionReconciler.Reconcile(jobCtx, job.Target)
			var deferred bool
			partitionErr, deferred = deferPendingPartitionAuthority(partitionErr)
			if deferred {
				diagnostics.Logf(
					"partitioned extraction deferred until observation v2 authority: repository=%q",
					job.Target,
				)
			}
			if partitionErr != nil {
				diagnostics.Logf(
					"partitioned extraction reconcile failed: repository=%q error=%v",
					job.Target, partitionErr,
				)
			}
			return partitionErr
		},
			Interval: cfg.Sync.Interval(), Diagnostics: cfg.Diagnostics.Jobs}
		bindT4013ExactReports(
			exactReports, failExactReport, candidateWorker,
			candidateRunner, exRunner, resolverRunner, callerRunner,
		)
		runBackground(func() { candidateRunner.Run(ctx) })
		runBackground(func() { exRunner.Run(ctx) })
		partitionScheduler := &generationscheduler.Scheduler{
			Store:       st,
			Owners:      owners,
			Diagnostics: cfg.Diagnostics.Extraction,
			Classes: map[store.GenerationResourceClass]generationscheduler.Class{
				store.GenerationResourceExtraction: {
					Concurrency: extractionpublication.ScheduleClassConcurrency,
					Budget: generationscheduler.Budget{
						MaxMemoryBytes: 512 << 20, MaxDescriptors: 8,
					},
					Handle: func(
						workerCtx context.Context,
						chunk store.GenerationChunk,
						_ generationscheduler.Budget,
					) error {
						return partitionRuntime.Handle(workerCtx, chunk)
					},
					OnExhausted: partitionRuntime.OnExhausted,
				},
			},
			PollEvery: time.Second, WorkerPrefix: "extraction-partition-worker",
			Report: func(err error) {
				diagnostics.Logf("partitioned extraction scheduler unavailable: %v", err)
			},
		}
		bindT422ExactChunkReports(exactReads, failExactRead, partitionScheduler)
		runBackground(func() {
			if err := partitionScheduler.Run(ctx); err != nil && ctx.Err() == nil {
				diagnostics.Logf("partitioned extraction scheduler stopped: %v", err)
			}
		})
		if resolverRunner != nil {
			runBackground(func() { resolverRunner.Run(ctx) })
		}
		if callerRunner != nil {
			runBackground(func() { callerRunner.Run(ctx) })
		}
		catalogAfterIndex := onIndexed
		onIndexed = func(ctx context.Context, name, commit string) error {
			candidateErr := enqueueCandidateAfterIndex(
				ctx, st, name, commit, cfg.Diagnostics.Candidates,
			)
			var catalogErr error
			if catalogAfterIndex != nil {
				catalogErr = catalogAfterIndex(ctx, name, commit)
			}
			return errors.Join(candidateErr, catalogErr)
		}
	}
	if lifetime := cfg.ProofBundles.RetentionFor(); lifetime > 0 {
		runBackground(func() {
			runProofBundleMaintenanceWithOwners(
				ctx, st, lifetime, evidenceSweepIdleInterval, evidenceSweepBacklogDelay,
				owners,
			)
		})
	}
	runBackground(func() {
		runEvidenceMaintenanceWithOwners(
			ctx, st, evidenceSweepIdleInterval, evidenceSweepBacklogDelay, evidenceStagedMaxAge,
			owners,
		)
	})

	// index pipeline: same-SHA zoekt-git-index child consumes indexing_job
	if bin, err := indexer.FindBinary(); err != nil {
		diagnostics.Logf("WARNING: zoekt-git-index unavailable — indexing disabled (make build provides the exact linked module pin; or set PHEBS_ZOEKT_GIT_INDEX): %v", err)
	} else {
		focusedBin, focusedErr := focusedindex.FindBinary()
		if focusedErr != nil && len(analysisUnits) > 0 {
			log.Print("WARNING: phebs-focused-index not found — indexing disabled for configured analysis units (make build provides it; or set PHEBS_FOCUSED_INDEX)")
			focusedBin = ""
		}
		ix := &indexer.Indexer{
			DataDir:       cfg.Server.DataDir,
			Bin:           bin,
			FocusedBin:    focusedBin,
			Store:         st,
			Verbose:       cfg.Indexing.Verbose,
			Revisions:     cfg.Revisions,
			AnalysisUnits: analysisUnits,
			OnIndexed:     onIndexed,
			AdmitDerived: func(admitCtx context.Context, estimatedBytes int64) error {
				capacity, admissionErr := capacityGate.Check(admitCtx, estimatedBytes)
				lifecycleStatus.ObserveCapacity(capacity, admissionErr)
				if estimatedBytes == 0 && errors.Is(admissionErr, lifecycle.ErrCapacityUnavailable) {
					// T35 workloads fail closed when capacity is unavailable. The
					// pre-existing index pipeline retains its historical behavior,
					// while a measured hard/projected watermark still refuses it.
					diagnostics.Logf("lifecycle capacity unavailable for legacy index admission: %v", admissionErr)
					return nil
				}
				return admissionErr
			},
		}
		ixRunner := &store.Runner{Store: st, Kind: store.JobIndex, Handle: ix.Handle, Owners: owners,
			Interval: cfg.Sync.Interval(), Diagnostics: cfg.Diagnostics.Jobs}
		bindT4013ExactReports(exactReports, failExactReport, nil, ixRunner)
		runBackground(func() { ixRunner.Run(ctx) })
	}

	reportT4013Startup("scheduler_recovery_complete")

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
	searcher, err := search.OpenWithGenerationPins(indexDir, st, searchGenerationPins)
	if err != nil {
		return err
	}
	reportT4013Startup("searcher_ready")
	defer searcher.Close()
	// Registered after Close so workers stop before the searcher's deferred
	// close; stopBackground is idempotent and its earlier defer still protects
	// startup failures before the searcher exists.
	defer stopBackground()
	searcher.Contexts = cfg.Contexts // T8.1: context:<name> filters
	serviceCatalogV3Cache := servicecatalogv3.NewDefaultReadCache()
	serviceStateV3Reader, err := store.NewServiceStateV3Reader(st, serviceCatalogV3Cache)
	if err != nil {
		return fmt.Errorf("configure selected service state reader: %w", err)
	}
	runtimeScopedSearch, err := search.NewRuntimeScopedSearcher(searcher, serviceStateV3Reader)
	if err != nil {
		return fmt.Errorf("configure selected service search: %w", err)
	}
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
		Store:   st, Search: searcher, ScopedSearch: runtimeScopedSearch,
		DataDir: cfg.Server.DataDir,
		CodeNav: codeNavigation,
		RetentionStatusSource: api.NewCompleteRetentionStatusSource(
			st, retentionStatus, nil,
		),
		LifecycleStatusSource: func(context.Context) lifecycle.Status {
			return lifecycleStatus.Snapshot()
		},
		IsAdmin: func(ctx context.Context) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			return ok && principal.IsAdmin
		},
		AuditRecord: auditRecord, AuditLog: st, Analytics: st,
		Evidence: evidenceView, ProofBundles: proofBundles,
		CallerMapEnabled: cfg.Experimental.ProvisionalProtoExtraction ||
			cfg.Experimental.ProvisionalThriftExtraction,
		CallerReader:         callerReader,
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
	apiOpts.ServiceDirectory = api.NewRuntimeServiceDirectoryService(
		apiOpts, serviceStateV3Reader,
	)
	apiOpts.ObservationProgress = api.NewObservationProgressService(
		apiOpts,
		&observationpublication.ProgressReader{
			DataDir: cfg.Server.DataDir, Store: st, Cache: observationCache,
			InventoryV2: true,
		},
	)
	apiOpts.ExtractionProgress = api.NewExtractionProgressService(apiOpts, partitionRuntime)
	if relationshipRuntime != nil {
		apiOpts.Relationships = api.NewRuntimeRelationshipService(
			apiOpts, relationshipCache, relationshipV3Cache,
		)
		if apiOpts.Relationships == nil {
			return errors.New("configure exact relationship readers")
		}
	}
	syntheticWorkbenchSetting := os.Getenv("PHEBS_SYNTHETIC_WORKBENCH")
	workbenchMode := ""
	if cfg.Experimental.ProvisionalWorkbench {
		var provisionalWorkbench store.InvestigationWorkbench
		if syntheticWorkbenchSetting == "" && apiOpts.ContractCatalogFixture == nil {
			if resolver := api.NewWorkbenchTargetResolver(apiOpts); resolver != nil {
				provisionalWorkbench = store.InvestigationWorkbenchService{
					Store: st, Resolver: resolver, Compatibility: compatibility,
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
			return err
		}
		workbenchMode = "provisional"
	} else {
		var syntheticWorkbench store.InvestigationWorkbench
		if syntheticWorkbenchSetting != "" {
			if resolver := api.NewWorkbenchTargetResolver(apiOpts); resolver != nil {
				syntheticWorkbench = store.InvestigationWorkbenchService{
					Store: st, Resolver: resolver, Compatibility: compatibility,
				}
			}
		}
		if err := bindSyntheticWorkbench(
			&apiOpts,
			syntheticWorkbenchSetting,
			syntheticWorkbench,
		); err != nil {
			return err
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
			return fmt.Errorf(
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
	var finalAuthority t421ExactFinalAuthorityRead
	var tailReadiness t421ExactFinalAuthorityRead
	if exactReads {
		repository, err := t421FinalAuthorityRepository(cfg.ServiceCatalogs)
		if err != nil {
			return err
		}
		if _, focused := analysisUnits[repository]; focused {
			return errors.New("T42.1 exact mode requires whole-repository authority")
		}
		if manifestProvider == nil || openPartitionDomain == nil || callerReader == nil {
			return errors.New("T42.1 exact mode requires the complete extraction runtime")
		}
		policies, err := extract.CandidatePolicies(exs)
		if err != nil {
			return fmt.Errorf("configure T42.1 exact candidate policies: %w", err)
		}
		reader, err := newT421FinalAuthorityReader(
			repository, cfg.Server.DataDir, indexDir, st, searchGenerationPins,
			policies,
			func(readCtx context.Context) (candidate.State, error) {
				return manifestProvider.CurrentPublicationState(readCtx, repository, nil)
			},
			openPartitionDomain, relationshipV3Cache, callerReader, visibleFor,
		)
		if err != nil {
			return fmt.Errorf("configure T42.1 final authority reader: %w", err)
		}
		finalAuthority = t421ExactFinalAuthorityRead{
			Limits: t421FinalAuthorityReadLimits(), Read: reader.Read,
		}
		tailReadiness = t421ExactFinalAuthorityRead{
			Limits: t421TailReadinessLimits(), Read: reader.ReadTailReadiness,
		}
	}
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
		Version: version, Store: st, Search: searcher, ScopedSearch: runtimeScopedSearch,
		DataDir: cfg.Server.DataDir,
		CodeNav: codeNavigation, Visible: visibleFor, Proofs: mcpProofs,
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
	if exactReadState != nil {
		if err := exactReadState.bindFinalReaders(finalAuthority, tailReadiness); err != nil {
			return err
		}
		apiHandler, mcpHandler = exactReadState.wrap(apiHandler), exactReadState.wrap(mcpHandler)
	}
	handler := t422OwnerHTTPHandler(owners, newHTTPHandler(authService, apiHandler, mcpHandler, promhttp.Handler(), http.FileServerFS(dist)), semanticLaunch)

	srv := &http.Server{
		Addr: cfg.Server.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		BaseContext: t421ExactReadServerBaseContext(ctx, exactReads),
	}
	shutdownErr := make(chan error, 1)
	runBackground(func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr <- srv.Shutdown(shutdownCtx)
	})

	listener, err := net.Listen("tcp", cfg.Server.Addr)
	if err != nil {
		stopBackground()
		return serverTerminalError(
			err, <-shutdownErr, exactReportFailed, exactReadFailed,
		)
	}
	log.Printf("phebs %s listening on %s (data: %s)", version, cfg.Server.Addr, cfg.Server.DataDir)
	reportT4013Startup("http_ready")
	startupTurn.End()
	startupEnded = true
	serveErr := srv.Serve(listener)
	// Shutdown waits at most five seconds. Exact modes retain a timeout as a
	// terminal error; ordinary mode preserves its existing shutdown behavior.
	stopBackground()
	return serverTerminalError(
		serveErr, <-shutdownErr, exactReportFailed, exactReadFailed,
	)
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

func bindProvisionalWorkbench(
	opts *api.Options,
	hasProtocolEvidence bool,
	syntheticSetting string,
	workbench store.InvestigationWorkbench,
) error {
	if err := validateSyntheticWorkbenchSetting(syntheticSetting); err != nil {
		return err
	}
	if opts == nil {
		return errors.New("provisional Workbench options are required")
	}
	if syntheticSetting == "1" || opts.ContractCatalogFixture != nil {
		return fmt.Errorf(
			"%w: provisional Workbench cannot be combined with synthetic Workbench or Contract Atlas fixtures",
			errWorkbenchAuthorityConflict,
		)
	}
	if !hasProtocolEvidence || opts.Evidence == nil || opts.ContractCatalog == nil {
		return fmt.Errorf(
			"%w: provisional Workbench requires provisional protobuf or Thrift extraction",
			errWorkbenchEvidencePrerequisite,
		)
	}
	if workbench == nil {
		return fmt.Errorf(
			"%w: provisional Workbench service is unavailable",
			errWorkbenchEvidencePrerequisite,
		)
	}
	opts.Workbench = workbench
	return nil
}

var (
	errWorkbenchAuthorityConflict    = errors.New("workbench-authority-conflict")
	errWorkbenchEvidencePrerequisite = errors.New(
		"workbench-evidence-prerequisite",
	)
	errSyntheticWorkbenchSetting = errors.New(
		"PHEBS_SYNTHETIC_WORKBENCH must be empty or 1",
	)
)

func validateSyntheticWorkbenchSetting(setting string) error {
	switch setting {
	case "", "1":
		return nil
	default:
		return errSyntheticWorkbenchSetting
	}
}

// bindSyntheticWorkbench is the only pre-enablement registration path. It is
// deliberately coupled to both synthetic fixture providers and the exact flag
// set by make dev; ordinary serve startup leaves the Huma route and capability
// absent.
func bindSyntheticWorkbench(
	opts *api.Options,
	setting string,
	workbench store.InvestigationWorkbench,
) error {
	if err := validateSyntheticWorkbenchSetting(setting); err != nil {
		return err
	}
	if opts == nil {
		return errors.New("synthetic Workbench options are required")
	}
	switch setting {
	case "":
		return nil
	case "1":
	}
	if opts.InvestigationViews == nil || opts.ContractCatalogFixture == nil {
		return errors.New(
			"synthetic Workbench requires Investigation and Contract Atlas fixtures",
		)
	}
	if workbench == nil {
		return errors.New("synthetic Workbench service is unavailable")
	}
	opts.Workbench = workbench
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
	return api.WithRetentionStatusWarning(authService.LoadAndSave(mux))
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

// bindSyntheticWorkbenchClosureDemo is the make-dev-only bridge from the
// retained neutral monorepo into the ordinary sync, index, and extraction
// pipeline. The repository deliberately has no SCIP index, and only the
// reviewed provisional protobuf and Thrift packs are enabled. Resource and
// runtime planes remain unsupported.
func bindSyntheticWorkbenchClosureDemo(cfg *config.Config, fixture string) error {
	if fixture == "" {
		return nil
	}
	if cfg == nil {
		return errors.New("synthetic Workbench closure demo requires server configuration")
	}
	if strings.TrimSpace(fixture) != fixture ||
		!filepath.IsAbs(fixture) ||
		filepath.Clean(fixture) != fixture ||
		filepath.Base(fixture) != "t2114-workbench-closure.bundle" {
		return errors.New(
			"synthetic Workbench closure demo must name the absolute clean t2114-workbench-closure.bundle path",
		)
	}
	info, err := os.Stat(fixture)
	if err != nil {
		return fmt.Errorf("inspect synthetic Workbench closure demo: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("synthetic Workbench closure demo must be a regular bundle file")
	}

	const connectionName = "t21-workbench-closure"
	alreadyConnected := false
	for _, connection := range cfg.Connections {
		if connection.Name == connectionName && connection.URL != fixture {
			return fmt.Errorf(
				"synthetic Workbench closure demo connection %q already names another source",
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
	cfg.Experimental.ProvisionalProtoExtraction = true
	cfg.Experimental.ProvisionalThriftExtraction = true
	return nil
}

// bindT307NeutralServiceDemo is the make-dev-only bridge from one retained,
// cloneable neutral repository into the ordinary focused-index, extraction,
// resolver, caller-overlay, and store-derived Workbench pipelines. The bridge
// is explicit so ordinary serve startup and production configuration remain
// unchanged. Unlike the older demo bridges, it installs no projected API or
// Workbench result fixture.
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
	cfg.Experimental.ProvisionalWorkbench = true
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

// mcpInvestigationMutation is deliberately narrower than the browser/Huma
// mutation predicate: MCP writes require a named API key, never a browser
// session or the migration-only legacy key. Authenticate has already checked
// expiry, revocation, user existence, and disabled state before this context
// exists.
func mcpInvestigationMutation(ctx context.Context) bool {
	principal, ok := auth.PrincipalFromContext(ctx)
	return ok && principal.HasAPIKeyCapability(
		store.APIKeyCapabilityInvestigationWrite,
	)
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

// runEvidenceMaintenance checks immediately at boot. Empty stores cost one
// query per idle interval; a likely backlog is processed in bounded bursts
// separated by a short yield. Pinned proof/checkpoint runs are excluded by the
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
