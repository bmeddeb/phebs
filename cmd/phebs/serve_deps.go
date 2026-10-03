package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"sync"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalogingest"
	"github.com/bmeddeb/phebs/internal/store"
)

// serveDeps carries the state threaded through serve's startup phases. Phases
// read what earlier phases produced and fill in the next fields; serve itself
// stays a short ordered sequence of phase calls.
type serveDeps struct {
	ctx    context.Context
	cancel context.CancelFunc
	// deferred holds every deferred shutdown action in registration order.
	// runDeferred drains it LIFO when serve returns, exactly matching the
	// ordering the inline defers had.
	deferred []func(retErr *error)

	flags          *serveFlags
	exactReports   bool
	exactReads     bool
	semanticLaunch *t422SemanticLaunch
	cfg            *config.Config
	rawConfig      []byte

	exs                []extract.Extractor
	resolverRegistry   *resolvermaterialize.Registry
	callerRegistry     *callerexecute.Registry
	callerPublications *callerpublication.Registry

	startup *serveOwners
	exact   *serveExact

	st           *store.Surreal
	callerReader *callerexecute.PublicationReader

	runBackground  func(func())
	stopBackground func()

	capacityGate                 *lifecycle.Gate
	partitionPublicationRoot     string
	acquireLifecycleMutation     func(context.Context) (func(), error)
	acquireObservationTransition func(context.Context) (func(), error)

	lifecycleOwners           []lifecycle.Owner
	searchGenerationPins      *focusedindex.SearchGenerationPins
	observationCache          *observationpublication.Cache
	observationInventoryCache *observationpublication.InventoryCacheV2
	relationshipCache         *relationshippublication.Cache
	relationshipV3Cache       *relationshippublication.CacheV3
	lifecycleStatus           *lifecycle.StatusMonitor
	observationRuntime        *observationpublication.Runtime
	relationshipRuntime       *relationshippublication.Runtime
	reconcileRelationship     func(context.Context, string) error

	lifecycleController    *lifecycle.Controller
	lifecycleControl       *t422LifecycleControl
	lifecycleRunnerControl *lifecycle.RunnerControl
	retention              *t422RetentionControl

	authService   *auth.Service
	auditRecord   func(context.Context, store.AuditEvent)
	analysisUnits map[string]analysisunit.Scope

	catalogReconciler   *servicecatalogingest.Reconciler
	v3CatalogReconciler *servicecatalogingest.V3Reconciler
	serviceRuntime      *serviceRuntimeController
	activationControl   *t422ActivationControl
	markerControl       *t422MarkerControl
	staleControl        *t422StaleControl
	checkpointControl   *t422CheckpointControl
	checkpointRecovery  *t422CheckpointRecoveryControl

	onIndexed func(context.Context, string, string) error

	evidenceView        store.EvidenceStore
	proofBundles        store.ProofBundleStore
	compatibility       compat.Service
	partitionRuntime    *extractionpublication.Runtime
	manifestProvider    *candidatejob.Provider
	openPartitionDomain func(context.Context, candidate.DomainResultPlan) (*candidate.SparseDomain, error)

	visibleFor           func(ctx context.Context) func(store.Repo) bool
	searcher             *search.Searcher
	runtimeScopedSearch  search.ScopedSearcher
	serviceStateV3Reader *store.ServiceStateV3Reader
	codeNavigation       *codenav.Service
	// Kept nil by ordinary serve until Bazel's registration gates pass.
	typedInstallation *typedServeInstallation
	typedRuntime      *typedServeRuntime
	dist              fs.FS
	indexDir          string
}

// deferFunc appends a deferred action run LIFO by runDeferred when serve
// returns. It preserves the exact ordering the inline defers had.
func (d *serveDeps) deferFunc(fn func(retErr *error)) {
	d.deferred = append(d.deferred, fn)
}

func (d *serveDeps) runDeferred(retErr *error) {
	for _, fn := range d.deferred {
		defer fn(retErr)
	}
}

// serveFlags carries the parsed `phebs serve` flag values.
type serveFlags struct {
	configPath         string
	allowInsecurePerms bool
	addr               string
	positional         []string
}

func parseServeFlags(args []string) (*serveFlags, error) {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := flags.String("config", "", "path to config file (defaults apply if omitted)")
	allowInsecurePerms := flags.Bool("allow-insecure-config-perms", false,
		"warn instead of refusing a config file readable by group or others")
	addr := flags.String("addr", "", "listen address (overrides config)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", errServeFlags, err)
	}
	return &serveFlags{
		configPath: *cfgPath, allowInsecurePerms: *allowInsecurePerms,
		addr: *addr, positional: flags.Args(),
	}, nil
}

// serveOwners holds the admitted production-owner lifetime for serve. finish
// mirrors the deferred closure previously inline in serve; complete marks the
// startup turn ended once the HTTP server is listening.
type serveOwners struct {
	owners             *dispatchadmission.Owners
	turn               dispatchadmission.OwnerTurn
	entered            bool
	ended              bool
	stopOnOwnerFailure func() bool
}

func (s *serveOwners) Owners() *dispatchadmission.Owners {
	if s == nil {
		return nil
	}
	return s.owners
}

func (s *serveOwners) finish(retErr *error) {
	if s == nil || !s.entered {
		return
	}
	s.stopOnOwnerFailure()
	if !s.ended {
		s.turn.End()
	}
	*retErr = errors.Join(*retErr, s.owners.Err())
}

func (s *serveOwners) complete() {
	if s == nil || !s.entered {
		return
	}
	s.turn.End()
	s.ended = true
}

// startServeOwners enters the admitted production-owner lifetime. The cancel
// and finish actions are deferred through d so they run on every return path,
// exactly as the inline defers did.
func startServeOwners(d *serveDeps) error {
	ctx, cancel := context.WithCancel(d.ctx)
	d.ctx, d.cancel = ctx, cancel
	// Registered first so it runs last: cancel covers startup and
	// ListenAndServe failures, not just signal-driven shutdown.
	d.deferFunc(func(*error) { cancel() })
	owners, err := dispatchadmission.NewProductionOwners(ctx, t422ServerOwnerLimits())
	if err != nil {
		return err
	}
	d.startup = &serveOwners{}
	if owners != nil {
		turn, err := owners.Enter(ctx)
		if err != nil {
			return err
		}
		d.startup.owners = owners
		d.startup.turn = turn
		d.startup.entered = true
		d.startup.stopOnOwnerFailure = context.AfterFunc(owners.Context(), cancel)
		d.deferFunc(d.startup.finish)
	}
	return dispatchadmission.BindProductionOwners(owners)
}

// serveExact carries the exact-report/exact-read scaffolding shared across
// phases: failure latches, report sinks, and the accounting state.
type serveExact struct {
	reportFailed chan struct{}
	failReport   func(error)
	readFailed   chan error
	failRead     func(error)
	state        *t421ExactReadAccountingState
	reuse        *t422ReuseControl
	attempts     *t422AttemptSinks
}

// closeArchive mirrors the archive-close defer previously inline in serve.
func (e *serveExact) closeArchive(retErr *error) {
	if e == nil || e.state == nil || e.state.archive == nil {
		return
	}
	if closeErr := e.state.archive.close(); closeErr != nil {
		e.failRead(closeErr)
		*retErr = errors.Join(*retErr, closeErr)
	}
}

// newServeBackground wires the background goroutine supervisor. Every
// service-owned goroutine is joined before the store is closed.
func newServeBackground(d *serveDeps) {
	var background sync.WaitGroup
	d.runBackground = func(run func()) {
		background.Add(1)
		go func() {
			defer background.Done()
			run()
		}()
	}
	var stopBackgroundOnce sync.Once
	d.stopBackground = func() {
		stopBackgroundOnce.Do(func() {
			d.cancel()
			background.Wait()
			_ = d.callerPublications.Close()
		})
	}
	d.deferFunc(func(*error) { d.stopBackground() })
}
