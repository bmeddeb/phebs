//go:build darwin

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestT422WorkspaceNativeHelper(t *testing.T) {
	if os.Getenv(t422BackupFixture) == "" {
		return
	}
	fixtureLimit := 3 * time.Minute
	allOwners := os.Getenv(t422BackupFixture) == "workspace-all-owners"
	if os.Getenv(t422BackupFixture) == "workspace-cleanup" || allOwners {
		fixtureLimit = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), fixtureLimit)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal(err)
	}
	defer func() {
		if e := lifetime.Close(context.Background()); e != nil {
			t.Error(e)
		}
	}()
	if _, err = lifetime.TakeStoreOwner(); err != nil {
		t.Fatal(err)
	}
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
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
		if e := st.Close(context.Background()); e != nil {
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
