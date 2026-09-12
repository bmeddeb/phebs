//go:build darwin

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/store"
)

// Actual inherited FD6, selected command, real engine quiescence and whole-root
// byte walks. The sixteen sweep callbacks and bearer auth backend are modeled;
// the cursor store, filesystem capacity and SDK reads are real. This proves no
// corpus deletion, pressure transition, frozen-volume capacity or whole phase.
func TestT422WorkspaceNativeComposition(t *testing.T) {
	testT422ArchiveRetiredNativeEndpoint(t, false, true, false)
}

// Actual selected epoch-one FD6, engine/SDK quiescence, drained authenticated
// HTTP finish command and source-bound WB prefix. Semantic input, auth backend
// and sixteen unused owners are supplied. This starts no author or corpus
// pipeline, proves only phase-two finish, and makes no warm/whole-phase claim.
func TestT422WorkspaceEarlyNativeComposition(t *testing.T) {
	testT422ArchiveRetiredNativeEndpointFailure(t, false, true, false, "workspace-early")
}

// Actual Resume-three callback plus independent cold/warm HTTP finish walks.
// Semantic input, auth backend and sixteen unused owner callbacks are supplied;
// native FD6, PC/DA/SA transitions, engine/SDK guards and joins are real.
// No author/corpus, capacity-normal cycle or full ColdToWarm admission is claimed.
func TestT422WorkspaceWarmNativeComposition(t *testing.T) {
	testT422ArchiveRetiredNativeEndpointFailure(t, false, true, false, "workspace-warm")
}

// Native phase-four callback/reopen composition under the original three-minute
// fixture deadline and full-profile 21 PC pairs. Compared with warm-only: three
// more guarded walks, two HTTP calls and 284 WB bytes (621 total). No author B,
// pin/authority/physical pipeline or representative workspace is executed;
// auth/semantic inputs and sixteen unused owners remain supplied.
func TestT422WorkspacePhysicalNativeComposition(t *testing.T) {
	testT422ArchiveRetiredNativeEndpointFailure(t, false, true, false, "workspace-physical")
}

// Only the actual epoch-four/phase-eight finish sample, with real FD6,
// engine/SDK guard, auth/PC and joins. Recovery/F/owner states are supplied;
// no hard death, corpus recovery, normal-capacity cycle or full phase is proven.
func TestT422WorkspaceRecoveryNativeComposition(t *testing.T) {
	testT422ArchiveRetiredNativeEndpointFailure(t, false, true, false, "workspace-recovery")
}

func TestT422WorkspaceNativeHelper(t *testing.T) {
	if os.Getenv(t422BackupFixture) == "" {
		return
	}
	markerWorkspace := os.Getenv(t422BackupFixture) == "workspace-marker"
	preparationWorkspace := os.Getenv(t422BackupFixture) == "workspace-preparation" || markerWorkspace
	fixtureLimit := 3 * time.Minute
	allOwners := os.Getenv(t422BackupFixture) == "workspace-all-owners"
	if os.Getenv(t422BackupFixture) == "workspace-cleanup" || allOwners {
		fixtureLimit = 10 * time.Minute
	}
	if preparationWorkspace {
		fixtureLimit = t422PreparationFixtureLimit
	}
	fixtureContext := t.Context()
	if preparationWorkspace {
		deadline, err := strconv.ParseInt(os.Getenv("PHEBS_T422_FIXTURE_DEADLINE"), 10, 64)
		if err != nil || deadline <= 0 {
			t.Fatal("original preparation fixture deadline", err)
		}
		var finish context.CancelFunc
		fixtureContext, finish = context.WithDeadline(fixtureContext, time.Unix(0, deadline))
		defer finish()
	}
	ctx, cancel := context.WithTimeout(fixtureContext, fixtureLimit)
	defer cancel()
	cleanupContext := context.Background()
	if preparationWorkspace {
		cleanupContext = ctx
	}
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal(err)
	}
	defer func() {
		if e := lifetime.Close(cleanupContext); e != nil {
			t.Error(e)
		}
	}()
	if _, err = lifetime.TakeStoreOwner(); err != nil {
		t.Fatal(err)
	}
	ownerLimits := dispatchadmission.OwnerLimits{Owners: 1, Requests: 1}
	if markerWorkspace {
		ownerLimits = t422ServerOwnerLimits() // Real scheduler expansion/reap/claim plus auth and lifecycle owners.
	}
	owners, err := dispatchadmission.NewProductionOwners(ctx, ownerLimits)
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal(err)
	}
	root := os.Getenv("PHEBS_T422_BACKUP_FIXTURE_ROOT")
	raw, err := os.ReadFile(filepath.Join(root, "phebs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenLocalWithConfig(ctx, filepath.Join(root, "data"), recovery.ConfigDigest(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := st.Close(cleanupContext); e != nil {
			t.Error(e)
		}
	}()
	// A real sibling contributes to the whole-workspace lower bound. This
	// positive assertion alone does not isolate its contribution from the DB;
	// the authenticated FD6 and real walker establish the measured root.
	sibling := make([]byte, 1<<20)
	if err = os.WriteFile(filepath.Join(root, "outside-data"), sibling, 0o600); err != nil {
		t.Fatal(err)
	}
	_, semanticRaw := t422LifecycleBootstrapRecord(t)
	physicalWorkspace := os.Getenv(t422BackupFixture) == "workspace-physical"
	warmWorkspace := os.Getenv(t422BackupFixture) == "workspace-warm" || physicalWorkspace
	earlyWorkspace := os.Getenv(t422BackupFixture) == "workspace-early" || warmWorkspace
	if preparationWorkspace {
		semanticRaw, err = os.ReadFile(filepath.Join(root, "preparation-semantic.json"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if earlyWorkspace {
		semanticRaw, _ = t422SemanticTestRequest(t)
	}
	snapshot, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		t.Fatal(err)
	}
	launch, err := decodeT422SemanticLaunch(semanticRaw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	runnerCtx, stopRunner := context.WithCancel(ctx)
	defer stopRunner()
	var failures, turns atomic.Uint64
	launch.fail = func(error) { failures.Add(1); stopRunner() }
	var modeledOwners []lifecycle.Owner
	for index := 0; index < 16; index++ {
		modeledOwners = append(modeledOwners, t422LifecycleOwnerFixture{name: fmt.Sprintf("test-owner-%02d", index), turns: &turns})
	}
	wantTurns, wantDeleted := uint64(16), uint64(16)
	verifyCleanup := func() {}
	if os.Getenv(t422BackupFixture) == "workspace-cleanup" {
		modeledOwners, verifyCleanup = seedT422CleanupNativeOwners(t, root, &turns)
		wantTurns, wantDeleted = t422CleanupExpectedTurns, t422CleanupExpectedDeleted
	} else if allOwners {
		modeledOwners, verifyCleanup = seedT422AllNativeOwners(t, st, root, &turns)
	}
	control, err := newT422LifecycleControl(runnerCtx, launch, modeledOwners)
	if err != nil || control.bindWorkspaceBytes(st) != nil {
		t.Fatal("actual workspace callback binding", err)
	}
	if control.workspaceByteSnapshot().Phases[8].Completed {
		t.Fatal("binding invented a byte observation")
	}
	controller, err := lifecycle.NewController(st, modeledOwners...)
	if err != nil {
		t.Fatal(err)
	}
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		lifecycle.RunWithControl(runnerCtx, controller, lifecycle.NewGate(filepath.Join(root, "data")),
			lifecycle.DefaultIdleInterval, lifecycle.DefaultBacklogDelay, control.ObserveOwner, nil, owners, control.runner)
	}()
	defer func() { stopRunner(); <-runnerDone }()
	authService, err := auth.New(runnerCtx, auth.Options{Store: &t422LifecycleAuthFixture{}, Owners: owners,
		Config: config.Auth{APIKey: t421ExactReadTestCredential}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stopRunner(); authService.WaitCleanup() }()
	if markerWorkspace {
		runT422MarkerNative(t, ctx, root, st, launch, control, owners, authService)
		return
	}
	if preparationWorkspace {
		runT422PreparationNative(t, ctx, root, st, launch, control, owners, authService)
		return
	}
	var readReports atomic.Uint64
	readState := t421NewExactReadAccountingState(func(raw []byte) error {
		var report t421ExactReadReport
		if json.Unmarshal(raw, &report) != nil || report.Schema != t421ExactReadReportSchema || report.Status != "complete" ||
			report.RequestOrdinal != 1 || report.ControlFileReads != 0 || report.StoreReadAttempts != 0 || report.MemberVisits != 0 || report.StoreWriteAttempts != 0 {
			return errT422LifecycleControl
		}
		readReports.Add(1)
		return nil
	}, launch.fail)
	readState.semantic, readState.lifecycle = launch, control
	handler := t422OwnerHTTPHandler(owners, authService.Require(readState.wrap(http.NotFoundHandler())), launch)
	runtime, err := store.ReadLocalRuntime(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("endpoint=" + runtime.Endpoint)
	input := bufio.NewScanner(os.Stdin)
	if earlyWorkspace {
		if control.collector != nil || control.workspaceByteSnapshot().Phases[1].Completed {
			t.Fatal("early binding invented collector or sample")
		}
		server := httptest.NewServer(handler)
		defer server.Close()
		phases := []uint32{2}
		if warmWorkspace {
			phases = append(phases, 3)
		}
		if physicalWorkspace {
			phases = append(phases, 4, 4)
		}
		for pointIndex, phase := range phases {
			point := "finish"
			if pointIndex == 2 {
				point = "start"
			}
			if !input.Scan() {
				t.Fatal("actual early request token", input.Err())
			}
			var priorSample t422WorkspaceSampleResponse
			if phase == 3 || phase == 4 && point == "finish" {
				before := control.workspaceByteSnapshot()
				if before.Unavailable || !before.Phases[phase-1].Completed || turns.Load() != 0 || failures.Load() != 0 {
					t.Fatal("callback did not complete before actual reopened request", before)
				}
				priorSample = t422WorkspaceSampleResponse{
					LogicalBytes:   before.Phases[phase-1].Maximum.LogicalBytes,
					AllocatedBytes: before.Phases[phase-1].Maximum.AllocatedBytes,
				}
			}
			request, err := http.NewRequestWithContext(runnerCtx, http.MethodPost, server.URL+t422WorkspaceSamplePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
			request.Header.Set(dispatchadmission.ProductionRequestHeader, input.Text())
			request.Header.Set(t422WorkspacePointHeader, point)
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 257))
			closeErr := response.Body.Close()
			var sample t422WorkspaceSampleResponse
			if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || len(body) > 256 ||
				json.Unmarshal(body, &sample) != nil || sample.LogicalBytes < uint64(len(sibling)) || sample.AllocatedBytes == 0 {
				t.Fatal("actual early workspace response", response.StatusCode, readErr, closeErr, string(body))
			}
			observed := control.workspaceByteSnapshot()
			wantLogical, wantAllocated := sample.LogicalBytes, sample.AllocatedBytes
			if phase == 3 || phase == 4 && point == "finish" {
				wantLogical = max(wantLogical, priorSample.LogicalBytes)
				wantAllocated = max(wantAllocated, priorSample.AllocatedBytes)
			}
			if observed.Unavailable || !observed.Phases[phase-1].Completed ||
				observed.Phases[phase-1].Maximum.LogicalBytes != wantLogical ||
				observed.Phases[phase-1].Maximum.AllocatedBytes != wantAllocated ||
				phase == 2 && observed.Phases[2].Completed ||
				turns.Load() != 0 || readReports.Load() != 0 || failures.Load() != 0 {
				t.Fatal("actual early positive prefix", observed, turns.Load(), readReports.Load(), failures.Load())
			}
			if _, err = st.ListRepos(ctx); err != nil {
				t.Fatal("real early SDK did not resume after native quiescence", err)
			}
			switch phase {
			case 2:
				fmt.Println("early_measured_and_resumed")
			case 3:
				fmt.Printf("warm_measured_and_resumed:%016x:%016x\n", sample.LogicalBytes, sample.AllocatedBytes)
			default:
				fmt.Printf("physical_%s_measured:%016x:%016x\n", point, sample.LogicalBytes, sample.AllocatedBytes)
				if point == "start" {
					if !input.Scan() || input.Text() != "probe_reopened" {
						t.Fatal("parent did not observe physical R", input.Err())
					}
					state, err := dispatchadmission.ProductionSemanticState()
					if err != nil || state.ProducerID != 2 || state.Phase != 4 || state.OrdinaryOwnersDrained {
						t.Fatal("actual physical reopened state", state, err)
					}
					turn, err := owners.Enter(ctx)
					if err != nil {
						t.Fatal("actual physical ordinary admission", err)
					}
					turn.End()
					request, err := owners.EnterRequest(ctx)
					if err != nil {
						t.Fatal("actual physical request admission", err)
					}
					request.End()
					if _, err := st.ListRepos(ctx); err != nil {
						t.Fatal("actual physical SDK resumption", err)
					}
					observed := control.workspaceByteSnapshot()
					if observed.Unavailable || !observed.Phases[3].Completed || turns.Load() != 0 || failures.Load() != 0 {
						t.Fatal("actual physical callback prefix", observed)
					}
					fmt.Println("physical_owners_sdk_reopened")
				}
			}
		}
		if !input.Scan() || input.Text() != "close" {
			t.Fatal("joined early close request", input.Text(), input.Err())
		}
		return
	}
	for index, path := range []string{t422LifecycleParkPath, t422LifecycleNormalDrive} {
		if !input.Scan() {
			t.Fatal("actual request token", input.Err())
		}
		if index == 1 {
			request := httptest.NewRequest(http.MethodPost, t422WorkspaceSamplePath, nil).WithContext(runnerCtx)
			request.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
			request.Header.Set(dispatchadmission.ProductionRequestHeader, input.Text())
			request.Header.Set(t422WorkspacePointHeader, "start")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var sample t422WorkspaceSampleResponse
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &sample) != nil ||
				sample.LogicalBytes < uint64(len(sibling)) || sample.AllocatedBytes == 0 || turns.Load() != 0 {
				t.Fatal("actual standalone workspace sample", response.Code, response.Body.String(), turns.Load())
			}
		}
		request := httptest.NewRequest(http.MethodPost, path, nil).WithContext(runnerCtx)
		request.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
		request.Header.Set(dispatchadmission.ProductionRequestHeader, input.Text())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != `{"status":"complete"}` {
			t.Fatal("actual lifecycle command", path, response.Code, response.Body.String())
		}
		if index == 0 {
			fmt.Println("parked")
			if !input.Scan() {
				t.Fatal("recovered sample token", input.Err())
			}
			request := httptest.NewRequest(http.MethodPost, t422WorkspaceSamplePath, nil).WithContext(runnerCtx)
			request.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
			request.Header.Set(dispatchadmission.ProductionRequestHeader, input.Text())
			request.Header.Set(t422WorkspacePointHeader, "finish")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var sample t422WorkspaceSampleResponse
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &sample) != nil ||
				sample.LogicalBytes < uint64(len(sibling)) || sample.AllocatedBytes == 0 || turns.Load() != 0 || failures.Load() != 0 {
				t.Fatal("actual recovered sample", response.Code, response.Body.String())
			}
			observed := control.workspaceByteSnapshot()
			if observed.Unavailable || !observed.Phases[7].Completed || observed.Phases[7].Maximum.LogicalBytes != sample.LogicalBytes ||
				observed.Phases[7].Maximum.AllocatedBytes != sample.AllocatedBytes {
				t.Fatal("actual recovered byte prefix", observed)
			}
			if _, err := st.ListRepos(ctx); err != nil {
				t.Fatal("actual recovered SDK resumption", err)
			}
			fmt.Println("recovered_workspace_measured")
			if os.Getenv(t422BackupFixture) == "workspace-recovery" {
				if !input.Scan() || input.Text() != "close" {
					t.Fatal("recovered close", input.Err())
				}
				return
			}
		}
	}
	// Close the real zero-read R report before requesting the normalized native
	// sample; the child must not bypass the lifecycle step owned by that tail.
	request := httptest.NewRequest(http.MethodGet, t422LifecycleNormalRead, nil).WithContext(runnerCtx)
	request.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, input.Text())
	request.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
	request.Header.Set(t421ExactReadOrdinalHeader, "1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var cycle lifecycle.CycleObservation
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &cycle) != nil || readReports.Load() != 1 {
		t.Fatal("actual normal-cycle R", response.Code, response.Body.String(), readReports.Load())
	}
	if allOwners {
		if cycle.OwnerTurns < t422CleanupExpectedTurns || cycle.OwnerTurns > uint64(lifecycle.MaxCycleObservationTurns) || cycle.Deleted != t422AllOwnersMinimumDeleted {
			t.Fatal("real-owner cycle did not drain its expected fixture", cycle)
		}
		jobBacklog := false
		for _, owner := range cycle.Owners {
			if owner.Name == lifecycle.JobOwner {
				jobBacklog = owner.State == "ok" && owner.Completeness == lifecycle.LowerBound && owner.Backlog
			} else if owner.State != "ok" || owner.Completeness != lifecycle.Exact || owner.Backlog {
				t.Fatal("non-job owner did not drain", owner)
			}
		}
		if !jobBacklog {
			t.Fatal("resumed job census did not retain truthful backlog", cycle)
		}
		wantTurns = cycle.OwnerTurns
	} else if cycle.OwnerTurns != wantTurns || cycle.Deleted != wantDeleted {
		t.Fatal("actual normal-cycle counts", cycle)
	}
	request = httptest.NewRequest(http.MethodPost, t422WorkspaceSamplePath, nil).WithContext(runnerCtx)
	request.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, input.Text())
	request.Header.Set(t422WorkspacePointHeader, "normalized")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var normalized t422WorkspaceSampleResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &normalized) != nil || normalized.LogicalBytes < uint64(len(sibling)) || normalized.AllocatedBytes == 0 || turns.Load() != wantTurns {
		t.Fatal("actual normalized workspace sample", response.Code, response.Body.String(), turns.Load())
	}
	bytes := control.workspaceByteSnapshot()
	if failures.Load() != 0 || turns.Load() != wantTurns || bytes.Unavailable || !bytes.Phases[8].Completed ||
		bytes.Phases[8].Maximum.LogicalBytes < uint64(len(sibling)) || bytes.Phases[8].Maximum.AllocatedBytes == 0 {
		t.Fatal("actual positive workspace checkpoint", failures.Load(), turns.Load(), bytes)
	}
	verifyCleanup()
	if _, err = st.ListRepos(ctx); err != nil {
		t.Fatal("real SDK did not resume after native quiescence", err)
	}
	fmt.Println("measured_and_resumed")
	if !input.Scan() || input.Text() != "close" {
		t.Fatal("joined close request", input.Text(), input.Err())
	}
}
