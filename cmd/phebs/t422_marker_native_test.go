//go:build darwin

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogingest"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
	t421fixture "github.com/bmeddeb/phebs/spike/t421"
)

// Actual reduced source/search, 56 extraction results, resolver, two-service
// prior and changed target publications; real marker scheduler/heartbeat,
// HIT HTTP tail, guarded FD6 S/R, RecoverSelected, service Advance and one
// target Complete. Source/search indexing is test setup before bootstrap.
// Candidate/extraction/state/prior setup uses real manual claims, not ordinary
// runners. Auth backend and sixteen unused lifecycle callbacks remain supplied.
// No author ReturnA, 10k catalog, selected Zoekt, full F or full phase claim.
func TestT422WorkspaceMarkerNativeComposition(t *testing.T) {
	if os.Getenv("PHEBS_T422_NATIVE_MARKER") != "1" {
		t.Skip("opt-in real reduced marker publication composition")
	}
	testT422ArchiveRetiredNativeEndpointFailure(t, false, true, false, "workspace-marker")
}

func t422MarkerNativeCatalog(t *testing.T) servicecatalog.Catalog {
	t.Helper()
	corpus, err := t421fixture.BuildCombinedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	catalog := servicecatalog.Catalog{Schema: corpus.Catalog.Schema, Override: corpus.Catalog.Override,
		Authority: servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: "native-marker", Version: "prior"}}
	wanted := make(map[string]bool, 2)
	for _, service := range corpus.Catalog.Services {
		if service.Disposition == servicecatalog.DispositionAccepted && len(catalog.Services) < 2 {
			catalog.Services = append(catalog.Services, service)
			wanted[service.Key] = true
		}
	}
	if len(catalog.Services) != 2 {
		t.Fatal("two real accepted input services")
	}
	accepted := make(map[string]bool)
	unowned := make(map[string]string)
	for _, membership := range corpus.Catalog.Memberships {
		if wanted[membership.ServiceKey] {
			catalog.Memberships = append(catalog.Memberships, membership)
			accepted[membership.Path] = true
		} else {
			unowned[membership.Path] = membership.Origin
		}
	}
	for _, placement := range corpus.Catalog.Unowned {
		unowned[placement.Path] = placement.Origin
	}
	for path, origin := range unowned {
		if !accepted[path] {
			catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{Path: path, Origin: origin})
		}
	}
	slices.SortFunc(catalog.Unowned, func(a, b servicecatalog.UnownedPlacement) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})
	if err := servicecatalogv3.ValidateCatalog(catalog); err != nil {
		t.Fatal("actual reduced catalog input", err)
	}
	return catalog
}

// Existing manual setup pattern, now with exact actual schedule identity and
// first-attempt checks. This is not a heartbeat/reaper claim.
func t422MarkerNativeState(t *testing.T, ctx context.Context, st *store.Surreal, begin store.ServiceStateV3Begin) {
	t.Helper()
	if begin.Noop {
		return
	}
	if begin.Schedule == nil {
		t.Fatal("actual state schedule absent")
	}
	schedule := begin.Schedule
	var err error
	for schedule.NextOffset < schedule.TotalItems {
		schedule, err = st.ExpandGenerationSchedule(ctx, schedule.Repository, schedule.Stage, schedule.Generation)
		if err != nil {
			t.Fatal(err)
		}
	}
	for completed := 0; completed < schedule.TotalChunks; completed++ {
		chunk, err := st.ClaimGenerationChunk(ctx, store.GenerationResourceCPU, "native-marker-state-setup")
		if err != nil || chunk == nil || chunk.ScheduleDigest != schedule.Digest || chunk.Attempt != 0 {
			t.Fatal("actual state claim", chunk, err)
		}
		if _, err := st.ProcessServiceStateV3Chunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
		if err := st.CompleteGenerationChunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := st.GetGenerationSchedule(ctx, schedule.Repository, schedule.Stage)
	if err != nil || actual == nil || actual.Digest != schedule.Digest || actual.Status != store.GenerationScheduleSettled ||
		actual.Pending != 0 || actual.Running != 0 || actual.Failed != 0 || actual.Succeeded != schedule.TotalChunks {
		t.Fatal("actual settled state setup", actual, err)
	}
}

func runT422MarkerNative(t *testing.T, ctx context.Context, root string, st *store.Surreal, launch *t422SemanticLaunch,
	workspace *t422LifecycleControl, owners *dispatchadmission.Owners, authService *auth.Service) {
	t.Helper()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	var err error
	work, err = bindT422SourceReports(work, launch.fail)
	if err != nil {
		t.Fatal(err)
	}
	work, err = bindT422ObservationReports(work, launch.fail)
	if err != nil {
		t.Fatal(err)
	}
	data, repository := filepath.Join(root, "data"), launch.request.Repository
	_, _, _ = seedT422PreparationNative(t, work, data, st, launch, true)
	extractors := evidenceExtractors(true, true, false, true)
	policies, err := candidatejob.CompilePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := candidatejob.NewProvider(data, st, policies)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := resolvermaterialize.NewRegistry(extractors)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := resolvermaterialize.NewWorker(data, st, provider, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueuePending(work, store.JobResolverCatalog, repository, false); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimJob(work, store.JobResolverCatalog, "native-marker-resolver-setup")
	if err != nil || job == nil || job.Target != repository {
		t.Fatal("actual resolver claim", job, err)
	}
	if err := resolver.Handle(work, *job); err != nil {
		t.Fatal("actual resolver materialization", err)
	}
	if err := st.SetJobStatus(work, *job, store.StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	// The resolver's genuine no-op successor remains queued; no claim that
	// all durable jobs drain or that this is the ordinary complete pipeline.
	indexRoot := filepath.Join(data, "index")
	acquire := func(ctx context.Context) (func(), error) { return focusedindex.AcquireMutationLock(ctx, indexRoot) }
	exclusive := func(ctx context.Context) (func(), error) {
		return focusedindex.AcquireExclusiveMutationLock(ctx, indexRoot)
	}
	relationship := &relationshippublication.Runtime{DataDir: data, Store: st,
		Cache: &observationpublication.Cache{}, InventoryCache: &observationpublication.InventoryCacheV2{}, Acquire: acquire}
	for _, extractor := range extractors {
		relationship.Domains = append(relationship.Domains, downstreamauthority.DomainIdentity{Domain: extractor.Domain(), Version: extractor.Version()})
	}
	catalog := t422MarkerNativeCatalog(t)
	catalogPath := filepath.Join(root, "marker-catalog.json")
	selections := map[string]config.ServiceCatalog{}
	reconciler := &servicecatalogingest.V3Reconciler{DataDir: data, Store: st, Selections: selections,
		RequiredIndexedCommit: launch.request.ReturnSourceCommit}
	services := newServiceRuntimeController(data, st, selections, reconciler, relationship, acquire,
		&focusedindex.SearchGenerationPins{}, &relationshippublication.Cache{}, &relationshippublication.CacheV3{})
	defer services.Close()
	revisions := []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: launch.request.ReturnSourceCommit}}
	search, _, err := focusedindex.ReadRepositorySearchGenerationContext(work, indexRoot, repository, revisions)
	if err != nil {
		t.Fatal(err)
	}
	publishCatalog := func() {
		t.Helper()
		raw, err := json.Marshal(catalog)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(catalogPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		selections[repository] = config.ServiceCatalog{Kind: catalog.Authority.Kind, ID: catalog.Authority.ID,
			Version: catalog.Authority.Version, Path: catalogPath, Runtime: config.ServiceCatalogRuntimeV3}
		outcome, err := reconciler.ReconcileRepository(work, repository)
		if err != nil || outcome != servicecatalogingest.OutcomePublished {
			t.Fatal("actual catalog census/publication", outcome, err)
		}
		begin, err := st.BeginServiceStateV3Reconcile(work, repository)
		if err != nil {
			t.Fatal(err)
		}
		t422MarkerNativeState(t, work, st, begin)
		activation, err := st.BeginServiceStateV3Activation(work, repository, search.Digest)
		if err != nil {
			t.Fatal(err)
		}
		t422MarkerNativeState(t, work, st, activation)
	}
	publishCatalog()
	if current, err := relationship.ReconcileV3(work, repository); err != nil || current {
		t.Fatal("actual prior schedule", current, err)
	}
	priorSchedule, err := st.GetGenerationSchedule(work, repository, relationshippublication.ScheduleStageV3)
	if err != nil || priorSchedule == nil || priorSchedule.TotalItems != 1 {
		t.Fatal("actual one-chunk prior", priorSchedule, err)
	}
	if _, err := st.ExpandGenerationSchedule(work, repository, priorSchedule.Stage, priorSchedule.Generation); err != nil {
		t.Fatal(err)
	}
	priorChunk, err := st.ClaimGenerationChunk(work, store.GenerationResourceMemory, "native-marker-prior-setup")
	if err != nil || priorChunk == nil || priorChunk.ScheduleDigest != priorSchedule.Digest || priorChunk.Attempt != 0 {
		t.Fatal("actual prior claim", priorChunk, err)
	}
	if err := relationship.HandleV3(work, *priorChunk); err != nil {
		t.Fatal("actual prior HandleV3", err)
	}
	if err := services.Advance(work, repository); err != nil {
		t.Fatal("actual prior selector", err)
	}
	if err := st.CompleteGenerationChunk(work, *priorChunk); err != nil {
		t.Fatal(err)
	}
	prior, err := relationshippublication.OpenCurrentV3(work, filepath.Join(data, "relationships"), repository)
	if err != nil {
		t.Fatal("strict-open actual prior", err)
	}
	priorRoot := prior.Root()
	catalog.Authority.Version = "target"
	catalog.Services[0].DisplayName += " target"
	publishCatalog()
	if current, err := relationship.ReconcileV3(work, repository); err != nil || current {
		t.Fatal("actual changed target schedule", current, err)
	}
	target, err := st.GetGenerationSchedule(work, repository, relationshippublication.ScheduleStageV3)
	if err != nil || target == nil || target.TotalItems != 1 || target.Digest == priorSchedule.Digest {
		t.Fatal("actual target schedule", target, err)
	}
	marker, err := newT422MarkerControl(work, launch, relationship, services, exclusive)
	if err != nil || workspace.markerWorkspace == nil {
		t.Fatal("actual marker/engine binding", err)
	}
	defer marker.cancel()
	marker.workspace = workspace.markerWorkspace
	var readReports atomic.Uint64
	state := t421NewExactReadAccountingState(func(raw []byte) error {
		var report t421ExactReadReport
		if json.Unmarshal(raw, &report) != nil || report.Schema != t421ExactReadReportSchema ||
			report.Status != "complete" || report.RequestOrdinal != readReports.Load()+1 ||
			report.RequestOrdinal > 2 || report.ControlFileReads != relationshippublication.PublicationTransitionControlFileReadsV3 ||
			report.StoreReadAttempts != 0 || report.MemberVisits != 0 || report.StoreWriteAttempts != 0 {
			return errT422MarkerControl
		}
		readReports.Add(1)
		return nil
	}, launch.fail)
	state.semantic, state.lifecycle, state.marker = launch, workspace, marker
	server := httptest.NewUnstartedServer(t422OwnerHTTPHandler(owners, authService.Require(state.wrap(http.NotFoundHandler())), launch))
	server.Config.BaseContext = func(net.Listener) context.Context { return work }
	server.Start()
	defer server.Close()
	fmt.Println("endpoint=" + server.URL)
	input := bufio.NewScanner(os.Stdin)
	if !input.Scan() || input.Text() != "start" {
		t.Fatal("actual target scheduler start", input.Err())
	}
	admitted, err := dispatchadmission.ProductionSemanticState()
	if err != nil || !launch.matches(admitted) || admitted.Phase != 6 || admitted.OrdinaryOwnersDrained {
		t.Fatal("actual open marker owner state", err)
	}
	schedulerContext, stopScheduler := context.WithCancel(work)
	defer stopScheduler()
	completed := make(chan generationscheduler.ChunkLifecycleReport, 1)
	failure := make(chan error, 1)
	fail := func(err error) {
		select {
		case failure <- err:
		default:
		}
		stopScheduler()
		cancel()
		launch.fail(err)
	}
	scheduler := &generationscheduler.Scheduler{Store: st, Owners: owners,
		Classes: map[store.GenerationResourceClass]generationscheduler.Class{
			store.GenerationResourceMemory: {Concurrency: 1,
				Budget:            generationscheduler.Budget{MaxMemoryBytes: 1 << 30, MaxDescriptors: 32},
				MarkerMeasurement: marker.measurementSelected,
				Handle: func(ctx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
					return marker.handleInEpoch(ctx, chunk)
				}},
		}, PollEvery: time.Second, WorkerPrefix: "native-marker-target", Report: fail}
	bindT422ExactChunkReports(true, fail, scheduler)
	exactSink := scheduler.ChunkReports
	var started atomic.Uint64
	scheduler.ChunkReports = func(raw []byte) error {
		if err := exactSink(raw); err != nil {
			return err
		}
		var report generationscheduler.ChunkLifecycleReport
		if json.Unmarshal(raw, &report) != nil || report.Schema != generationscheduler.ChunkLifecycleSchema ||
			report.Stage != target.Stage || report.Generation != target.Generation || report.Attempt != 0 {
			return errT422MarkerControl
		}
		switch {
		case report.Event == "started" && report.Outcome == "running":
			if started.Add(1) != 1 {
				return errT422MarkerControl
			}
		case report.Event == "settled" && report.Outcome == "completed":
			if started.Load() != 1 {
				return errT422MarkerControl
			}
			select {
			case completed <- report:
			default:
				return errT422MarkerControl
			}
			stopScheduler() // Actual Complete already succeeded before this report.
		default:
			return errT422MarkerControl
		}
		return nil
	}
	joined := make(chan error, 1)
	go func() { joined <- scheduler.Run(schedulerContext) }()
	defer func() {
		stopScheduler()
		if joined != nil {
			if err := <-joined; err != nil {
				t.Error(err)
			}
		}
	}()
	fmt.Println("marker_running")
	var report generationscheduler.ChunkLifecycleReport
	select {
	case <-work.Done():
		t.Fatal("actual marker target canceled", work.Err())
	case err := <-failure:
		t.Fatal("actual marker scheduler refused", err)
	case report = <-completed:
	}
	if err := <-joined; err != nil {
		t.Fatal(err)
	}
	joined = nil
	select {
	case err := <-failure:
		t.Fatal("actual scheduler terminal failure", err)
	default:
	}
	if work.Err() != nil {
		t.Fatal(work.Err())
	}
	settled, err := st.GetGenerationSchedule(work, repository, target.Stage)
	if err != nil || settled == nil || settled.Digest != target.Digest || settled.Status != store.GenerationScheduleSettled ||
		settled.Succeeded != 1 || settled.Pending != 0 || settled.Running != 0 || settled.Failed != 0 {
		t.Fatal("actual one Complete", settled, err)
	}
	marker.mu.Lock()
	stage, committed, targetProof, chunk := marker.stage, marker.recoveryCommitted, marker.target, marker.chunk
	markerErr := marker.err
	marker.mu.Unlock()
	if stage != t422MarkerComplete || !committed || markerErr != nil || readReports.Load() != 2 ||
		report.Identity != chunk.Identity || targetProof.ScheduleDigest != target.Digest ||
		targetProof.Request.PriorGenerationDigest != priorRoot.GenerationDigest || targetProof.Request.PriorRootDigest != priorRoot.Digest {
		t.Fatal("actual marker retained prior/claim/continuation", stage, markerErr)
	}
	current, err := relationshippublication.OpenCurrentV3(work, filepath.Join(data, "relationships"), repository)
	if err != nil {
		t.Fatal(err)
	}
	actual := current.Root()
	selector, err := st.GetServiceRuntimeSelector(work, repository)
	if err != nil || selector.Backend != store.ServiceRuntimeV3 ||
		selector.RelationshipGenerationDigest != actual.GenerationDigest || selector.RelationshipRootDigest != actual.Digest ||
		actual.GenerationDigest != targetProof.Request.TargetGenerationDigest || actual.Digest != targetProof.Request.TargetRootDigest {
		t.Fatal("actual Advance selector/target", selector, err)
	}
	if _, err := st.ListRepos(work); err != nil {
		t.Fatal("actual SDK resume", err)
	}
	fmt.Println("marker_complete")
	if !input.Scan() || input.Text() != "close" {
		t.Fatal("actual marker joined close", input.Err())
	}
	if work.Err() != nil {
		t.Fatal(errors.Join(errT422MarkerControl, work.Err()))
	}
}
