//go:build darwin

package main

import (
	"bufio"
	"context"
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
	testT422ArchiveRetiredNativeEndpoint(t, false, true)
}

func TestT422WorkspaceNativeHelper(t *testing.T) {
	if os.Getenv(t422BackupFixture) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
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
	handler := t422OwnerHTTPHandler(owners, authService.Require(http.HandlerFunc(control.command)), launch)
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
	bytes := control.workspaceByteSnapshot()
	if failures.Load() != 0 || turns.Load() != 16 || bytes.Unavailable || !bytes.Phases[8].Completed ||
		bytes.Phases[8].Maximum.LogicalBytes < uint64(len(sibling)) || bytes.Phases[8].Maximum.AllocatedBytes == 0 {
		t.Fatal("actual positive workspace checkpoint", failures.Load(), turns.Load(), bytes)
	}
	if _, err = st.ListRepos(ctx); err != nil {
		t.Fatal("real SDK did not resume after native quiescence", err)
	}
	fmt.Println("measured_and_resumed")
	if !input.Scan() || input.Text() != "close" {
		t.Fatal("joined close request", input.Text(), input.Err())
	}
}
