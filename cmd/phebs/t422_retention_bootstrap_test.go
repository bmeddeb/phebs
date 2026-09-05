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
	"io"
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
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

const t422RetentionBootstrapMode = "PHEBS_T422_RETENTION_BOOTSTRAP_TEST"

func t422RetentionBootstrapRecord(t *testing.T) (dispatchadmission.ProductionBootstrap, []byte) {
	t.Helper()
	raw, _ := t422SemanticTestRequest(t)
	record := t422ServeFlagsRecord()
	record.SemanticMode, record.InputSHA256 = dispatchadmission.ProductionSemanticV3, sha256.Sum256(raw)
	record.Phase, record.Limits.Phases = 2, 3
	record.Control.OwnerControl = true
	record.Control.Phases, record.Control.InitialPhase, record.Control.MaximumPhases = []uint32{2, 3, 4}, 2, 3
	return record, raw
}

// This proves actual inherited admission, the real outer request owner/auth
// stack, successful-F-tail capture and phase-four pin/terminal ordering. The F
// payload is explicitly a tiny supplied fixture, not the full native authority
// reader. Native C41 evidence is covered separately by two actual generations.
func TestT422RetentionInheritedTails(t *testing.T) {
	for _, mode := range []string{"pin-close", "f-report", "f-panic", "r-report", "r-panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			record, _ := t422RetentionBootstrapRecord(t)
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
			t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })
			controlParent, controlChild, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = controlParent.Close(); _ = controlChild.Close() })
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422RetentionBootstrapHelper$")
			command.Env = []string{t422RetentionBootstrapMode + "=" + mode, dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionSelector, "GORACE=atexit_sleep_ms=0"}
			command.ExtraFiles = []*os.File{child, controlChild}
			command.WaitDelay = time.Second
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
			t.Cleanup(func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			})
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
					t.Error("receiver not joined")
				}
			}()
			phase, err := dispatchadmission.NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = phase.Close() }()
			scanner := bufio.NewScanner(output)
			if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "http://127.0.0.1:") {
				t.Fatal("helper listener absent", scanner.Text())
			}
			address := scanner.Text()
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
			defer client.CloseIdleConnections()
			call := func(path string, read bool) {
				t.Helper()
				method := http.MethodPost
				if read {
					method = http.MethodGet
				}
				request, err := http.NewRequestWithContext(ctx, method, address+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set(dispatchadmission.ProductionRequestHeader, phase.RequestToken())
				request.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
				if read {
					request.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
					request.Header.Set(t421ExactReadOrdinalHeader, "1")
				}
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				raw, readErr := io.ReadAll(io.LimitReader(response.Body, t422RetentionBodyBytes+1))
				closeErr := response.Body.Close()
				if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || len(raw) > t422RetentionBodyBytes {
					t.Fatal("owned request failed", response.StatusCode, string(raw), readErr, closeErr)
				}
			}
			advance := func() {
				t.Helper()
				for _, operation := range []func() error{func() error { return phase.Pause(ctx) }, controller.Fence, func() error { return phase.Checkpoint(ctx) }, controller.Advance, func() error { return phase.Resume(ctx) }, func() error { return phase.OpenRequests(ctx) }} {
					if err := operation(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := phase.DrainOwners(ctx); err != nil {
				t.Fatal(err)
			}
			advance()
			call(t421ExactFinalAuthorityPath, true)
			if err := phase.FenceRequests(ctx); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(mode, "f-") {
				advance()
				call(t422RetentionPinPath, false)
				if err := phase.FenceRequests(ctx); err != nil {
					t.Fatal(err)
				}
			}
			for _, operation := range []func() error{func() error { return phase.Pause(ctx) }, controller.Fence, func() error { return phase.Checkpoint(ctx) }} {
				if err := operation(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := input.Write([]byte{'c'}); err != nil {
				t.Fatal(err)
			}
			if !scanner.Scan() || scanner.Text() != "retention_joined" {
				t.Fatal("retention helper did not join", scanner.Text())
			}
			if err := command.Wait(); err != nil {
				t.Fatal("child failed", err, diagnostic.String())
			}
			clean = true
			snapshot, err := controller.Snapshot()
			if err != nil || !snapshot.Complete || snapshot.Attempts != 0 {
				t.Fatal("empty dispatch prefix did not close", snapshot, err)
			}
		})
	}
}

func TestT422RetentionBootstrapHelper(t *testing.T) {
	mode := os.Getenv(t422RetentionBootstrapMode)
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal("inherited bootstrap", err)
	}
	_, raw := t422RetentionBootstrapRecord(t)
	snapshot, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		t.Fatal(err)
	}
	launch, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var failures atomic.Uint64
	launch.fail = func(error) { failures.Add(1) }
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal("owners", err)
	}
	pins := &focusedindex.SearchGenerationPins{}
	owner := lifecycle.SearchGenerationOwnerImpl{IndexDir: t.TempDir(), Pins: pins, Acquire: func(context.Context) (func(), error) { return func() {}, nil }}
	control, err := newT422RetentionControl(ctx, launch, owner, pins)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	canonical, err := json.Marshal(t421FinalAuthorityResponse{Schema: t421FinalAuthoritySchema, Authority: t421FinalAuthorityState{
		SearchGenerationSHA256: digest, SearchInventory: t421FinalSetIdentity{Records: 2}, Current: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	state := t421NewExactReadAccountingState(func([]byte) error {
		control.mu.Lock()
		before := control.step == 0 && control.busy && control.old == "" && control.pin == nil
		control.mu.Unlock()
		if !before {
			return errors.New("F identity committed before its complete report")
		}
		if mode == "f-report" {
			return errors.New("private F sink")
		}
		if mode == "f-panic" {
			panic("private F sink")
		}
		return nil
	}, launch.fail, t421ExactFinalAuthorityRead{Read: func(context.Context) ([]byte, func() error, error) { return bytes.Clone(canonical), nil, nil }})
	state.semantic, state.retention = launch, control
	authCtx, stopAuth := context.WithCancel(ctx)
	defer stopAuth()
	authService, err := auth.New(authCtx, auth.Options{Store: &t422LifecycleAuthFixture{}, Owners: owners, Config: config.Auth{APIKey: t421ExactReadTestCredential}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stopAuth(); authService.WaitCleanup() }()
	server := httptest.NewServer(t422OwnerHTTPHandler(owners, authService.Require(state.wrap(http.NotFoundHandler())), launch))
	defer server.Close()
	fmt.Println(server.URL)
	var terminal [1]byte
	if _, err := io.ReadFull(os.Stdin, terminal[:]); err != nil || terminal[0] != 'c' {
		t.Fatal("terminal ownership", err)
	}
	if strings.HasPrefix(mode, "f-") {
		if failures.Load() == 0 || control.old != "" || control.step != 0 || pins.Pinned(launch.request.Repository, digest) {
			t.Fatal("failed F tail captured identity")
		}
	} else {
		if failures.Load() != 0 || control.old != digest || control.step != 2 || !pins.Pinned(launch.request.Repository, digest) {
			t.Fatal("warm identity/phase-four pin missing")
		}
		if strings.HasPrefix(mode, "r-") {
			// Only the completion/error boundary is exercised here, not native R
			// success. The actual C41 method has its own real generation fixture.
			if mode == "r-panic" {
				launch.fail = func(error) { failures.Add(1); panic("private terminal callback") }
			}
			_, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{})
			if err != nil {
				t.Fatal(err)
			}
			terminalState := t421NewExactReadAccountingState(func([]byte) error { return nil }, func(error) { failures.Add(1) })
			terminalState.active = true
			terminalState.finishRead(httptest.NewRecorder(), ledger, 2, false, "native-failure", errT422RetentionControl, nil,
				func(err error) { control.complete(ctx, 2, err) })
			if failures.Load() == 0 || pins.Pinned(launch.request.Repository, digest) || !terminalState.failed {
				t.Fatal("terminal R callback retained pin or hid failure")
			}
		}
	}
	server.Close()
	stopAuth()
	authService.WaitCleanup()
	control.Close()
	control.Close()
	if pins.Pinned(launch.request.Repository, digest) {
		t.Fatal("Close retained pin")
	}
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Println("retention_joined")
}
