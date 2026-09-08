//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

const t422SelectorCleanupTestMode = "PHEBS_T422_SELECTOR_CLEANUP_TEST"

func t422SelectorCleanupTestRecord(t *testing.T) (dispatchadmission.ProductionBootstrap, []byte) {
	t.Helper()
	record, raw := t422StaleBootstrapRecord(t)
	var request t422SemanticLaunchRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	request.SelectorHandoffCleanup = t422SelectorCleanupSchema
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	record.InputSHA256 = sha256.Sum256(raw)
	return record, raw
}

// Real inherited DA/PC, drained owners and request-token admission surround
// the actual shared F handler/finishRead. F and its confirmed selector are
// supplied scalar fixtures: no native authority read or database is claimed.
func TestT422SelectorCleanupInheritedFinalTails(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	record, _ := t422SelectorCleanupTestRecord(t)
	configuration := dispatchadmission.Config{Limits: record.Limits, Producers: []dispatchadmission.Producer{record.Producer}}
	for _, phase := range record.Control.Phases {
		configuration.Phases = append(configuration.Phases, dispatchadmission.Phase{ID: phase, Roles: []dispatchadmission.RoleBudget{
			{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal}, {Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility},
		}})
	}
	controller, err := dispatchadmission.New(ctx, configuration)
	if err != nil {
		t.Fatal(err)
	}
	parent, child, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(); _ = child.Close() }()
	controlParent, controlChild, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controlParent.Close(); _ = controlChild.Close() }()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422SelectorCleanupInheritedHelper$")
	command.Env = []string{t422SelectorCleanupTestMode + "=1", dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionSelector, "GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles, command.WaitDelay = []*os.File{child, controlChild}, time.Second
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	_ = child.Close()
	_ = controlChild.Close()
	if err := dispatchadmission.SendProductionBootstrap(ctx, parent, controlParent, record); err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- controller.Serve(ctx, record.Producer.ID, command.Process.Pid, parent) }()
	clean := false
	defer func() {
		if !clean {
			cancel()
		}
		select {
		case err := <-served:
			if clean && err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("selector cleanup admission receiver did not join")
		}
	}()
	phase, err := dispatchadmission.NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = phase.Close() }()
	scanner := bufio.NewScanner(output)
	expect := func(want string) {
		t.Helper()
		if !scanner.Scan() || scanner.Text() != want {
			t.Fatalf("helper got %q, want %q: %v", scanner.Text(), want, scanner.Err())
		}
	}
	expect("ready")
	if err := phase.DrainOwners(ctx); err != nil {
		t.Fatal(err)
	}
	if err := phase.OpenRequests(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(input, phase.RequestToken()); err != nil {
		t.Fatal(err)
	}
	expect("tested")
	for _, operation := range []func() error{func() error { return phase.FenceRequests(ctx) }, func() error { return phase.Pause(ctx) }, controller.Fence, func() error { return phase.Checkpoint(ctx) }} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fmt.Fprintln(input, "close"); err != nil {
		t.Fatal(err)
	}
	expect("joined")
	if err := command.Wait(); err != nil {
		t.Fatal("selector cleanup helper failed", err, diagnostic.String())
	}
	clean = true
	snapshot, err := controller.Snapshot()
	if err != nil || !snapshot.Complete || snapshot.Attempts != 0 {
		t.Fatal("F-tail tests changed the empty native dispatch prefix", snapshot, err)
	}
}

func TestT422SelectorCleanupInheritedHelper(t *testing.T) {
	if os.Getenv(t422SelectorCleanupTestMode) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal("inherited bootstrap", err)
	}
	_, raw := t422SelectorCleanupTestRecord(t)
	snapshot, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		t.Fatal(err)
	}
	base, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal("owners", err)
	}
	authCtx, stopAuth := context.WithCancel(ctx)
	defer stopAuth()
	authService, err := auth.New(authCtx, auth.Options{Store: &t422LifecycleAuthFixture{}, Owners: owners, Config: config.Auth{APIKey: t421ExactReadTestCredential}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stopAuth(); authService.WaitCleanup() }()
	fmt.Println("ready")
	input := bufio.NewReader(os.Stdin)
	token, err := input.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	token = strings.TrimSuffix(token, "\n")
	for _, mode := range []string{"success", "body", "accounting", "cache", "cache_panic", "report", "report_panic", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			testT422SelectorCleanupFinalTail(ctx, t, *base, owners, authService, token, mode)
		})
	}
	for _, policy := range []string{"", "unknown-cleanup-policy"} {
		t.Run("unselected/"+policy, func(t *testing.T) {
			launch := *base
			launch.request.SelectorHandoffCleanup = policy
			var failures, downstream atomic.Uint64
			launch.fail = func(error) { failures.Add(1) }
			control, err := newT422SelectorCleanupControl(ctx, &launch, &store.Surreal{}, func(context.Context) (func(), error) {
				t.Error("unselected cleanup acquired mutation custody")
				return nil, errors.New("unselected")
			})
			if err == nil || control != nil {
				t.Fatal("unselected constructor admitted cleanup")
			}
			handler := t422OwnerHTTPHandler(owners, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				downstream.Add(1)
			}), &launch)
			request := httptest.NewRequest(http.MethodPost, t422SelectorCleanupPath, nil).WithContext(ctx)
			request.Header.Set(dispatchadmission.ProductionRequestHeader, token)
			writer := httptest.NewRecorder()
			handler.ServeHTTP(writer, request)
			if writer.Code != http.StatusServiceUnavailable || downstream.Load() != 0 || failures.Load() != 1 {
				t.Fatal("unselected policy reached the command route", writer.Code, downstream.Load(), failures.Load())
			}
		})
	}
	if t.Failed() {
		return
	}
	fmt.Println("tested")
	if line, err := input.ReadString('\n'); err != nil || line != "close\n" {
		t.Fatal("parent close", err)
	}
	stopAuth()
	authService.WaitCleanup()
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Println("joined")
}

func testT422SelectorCleanupFinalTail(ctx context.Context, t *testing.T, launch t422SemanticLaunch,
	owners *dispatchadmission.Owners, authService *auth.Service, token, mode string,
) {
	t.Helper()
	controlCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var failures atomic.Uint64
	launch.fail = func(error) { failures.Add(1); cancel() }
	acquire := func(context.Context) (func(), error) {
		t.Error("F tail attempted native cleanup")
		return nil, errors.New("no native cleanup in this test")
	}
	control, err := newT422SelectorCleanupControl(controlCtx, &launch, &store.Surreal{}, acquire)
	if err != nil {
		t.Fatal(err)
	}
	stale := &t422StaleControl{ctx: controlCtx, cancel: cancel, launch: &launch}
	candidateState, response := t422StaleFixtureFinal()
	candidateState.Repository = launch.request.Repository
	selected := store.ServiceRuntimeSelector{Repository: launch.request.Repository, Backend: store.ServiceRuntimeV3,
		ControlRevision: 3, Digest: t422CheckpointTestDigest, StateControlRevision: 4, StateSummaryDigest: t422CheckpointTestDigest}
	canonical, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	assertUnarmed := func() {
		t.Helper()
		if control.armed || stale.confirmed {
			t.Error("F continuation armed before its report completed")
		}
	}
	state := t421NewExactReadAccountingState(func([]byte) error {
		assertUnarmed()
		switch mode {
		case "report":
			return errors.New("report refused")
		case "report_panic":
			panic("report refused")
		case "canceled":
			cancel()
		}
		return nil
	}, launch.fail, t421ExactFinalAuthorityRead{Read: func(readCtx context.Context) ([]byte, func() error, error) {
		if err := stale.captureFinal(readCtx, candidateState, response); err != nil {
			return nil, nil, err
		}
		if err := control.captureFinal(readCtx, selected); err != nil {
			return nil, nil, err
		}
		if mode == "accounting" {
			_ = readaccounting.Charge(readCtx, readaccounting.StoreReadAttempt, 1)
		}
		return bytes.Clone(canonical), func() error {
			assertUnarmed()
			switch mode {
			case "cache":
				return errors.New("cache refused")
			case "cache_panic":
				panic("cache refused")
			}
			return nil
		}, nil
	}})
	state.semantic, state.stale, state.selectorCleanup = &launch, stale, control
	handler := t422OwnerHTTPHandler(owners, authService.Require(state.wrap(http.NotFoundHandler())), &launch)
	request := exactT421ReadRequest(http.MethodGet, t421ExactFinalAuthorityPath, 1).WithContext(ctx)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, token)
	var writer http.ResponseWriter = httptest.NewRecorder()
	if mode == "body" {
		writer = &t421ExactReadPartialWriter{header: make(http.Header)}
	}
	handler.ServeHTTP(writer, request)
	if !control.captured || control.selected != selected || state.active {
		t.Fatal("confirmed F scalar capture or shared request release missing")
	}
	if mode == "success" {
		if !control.armed || !stale.confirmed || control.failed || failures.Load() != 0 || state.failed {
			t.Fatal("successful composed F did not arm both continuations")
		}
	} else if control.armed || stale.confirmed || !control.failed || failures.Load() == 0 {
		t.Fatal("failed F left a usable cleanup or stale continuation")
	}
}

func TestT422SelectorCleanupPolicyAndClosedRoutes(t *testing.T) {
	for _, policy := range []string{"", t422SelectorCleanupSchema, "unknown-cleanup-policy"} {
		t.Run("policy/"+policy, func(t *testing.T) {
			raw, snapshot := t422SemanticTestRequest(t)
			var request t422SemanticLaunchRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			request.SelectorHandoffCleanup = policy
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, '\n')
			snapshot.InputSHA256 = sha256.Sum256(raw)
			launch, err := decodeT422SemanticLaunch(raw, snapshot)
			if (err == nil) != (policy != "unknown-cleanup-policy") {
				t.Fatal("closed policy admission differs", err)
			}
			if policy == "" && (launch.request.SelectorHandoffCleanup != "" || bytes.Contains(raw, []byte("selector_handoff_cleanup"))) {
				t.Fatal("omitted policy gained cleanup bytes")
			}
		})
	}
	for _, mode := range []string{"exact", "get", "query", "body", "marked", "alias"} {
		t.Run("route/"+mode, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, t422SelectorCleanupPath, nil)
			switch mode {
			case "get":
				request.Method = http.MethodGet
			case "query":
				request.URL.RawQuery = "repository=other"
			case "body":
				request.ContentLength = 1
			case "marked":
				request.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
			case "alias":
				request.URL.RawPath = "/api/t422/selector-handoff/%63leanup"
			}
			if t422SemanticRequestRoute(request) != (mode == "exact") {
				t.Fatal("closed cleanup recipe differs")
			}
		})
	}
	// With no selected cleanup control, the shared wrapper supplies no command.
	state := t421NewExactReadAccountingState(func([]byte) error { return nil }, func(error) {})
	writer := httptest.NewRecorder()
	state.wrap(http.NotFoundHandler()).ServeHTTP(writer, httptest.NewRequest(http.MethodPost, t422SelectorCleanupPath, nil))
	if writer.Code != http.StatusNotFound {
		t.Fatal("absent cleanup policy supplied a command", writer.Code)
	}
}
