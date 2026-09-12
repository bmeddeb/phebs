//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const t422QueryEvidenceInheritedMode = "PHEBS_T422_QUERY_EVIDENCE_INHERITED"

func t422QueryEvidenceInheritedRecord(t *testing.T, mode string) (dispatchadmission.ProductionBootstrap, []byte) {
	t.Helper()
	record, raw := t422LifecycleBootstrapRecord(t)
	if mode != "wrong_epoch" {
		raw = bytes.Replace(raw, []byte(`"server_epoch":4`), []byte(`"server_epoch":5`), 1)
		record.Producer.ID, record.Phase, record.Limits.Phases = 6, 12, 3
		record.Control.Phases, record.Control.InitialPhase, record.Control.MaximumPhases = []uint32{12, 13, 14}, 12, 3
		record.InputSHA256 = sha256.Sum256(raw)
	}
	return record, raw
}

// Real inherited DA/PC, owner/request admission and auth are exercised here.
// Only the F body is supplied: this is not an engine/catalog or phase pass.
func TestT422QueryEvidenceInheritedRouting(t *testing.T) {
	for _, mode := range []string{"complete", "wrong_phase", "wrong_epoch"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			record, _ := t422QueryEvidenceInheritedRecord(t, mode)
			configuration := dispatchadmission.Config{Limits: record.Limits, Producers: []dispatchadmission.Producer{record.Producer}}
			for _, id := range record.Control.Phases {
				configuration.Phases = append(configuration.Phases, dispatchadmission.Phase{ID: id, Roles: []dispatchadmission.RoleBudget{
					{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal},
					{Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility},
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
			pcParent, pcChild, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = pcParent.Close(); _ = pcChild.Close() }()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422QueryEvidenceInheritedHelper$", "-test.count=1")
			command.Env = []string{t422QueryEvidenceInheritedMode + "=" + mode,
				dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionSelector, "GORACE=atexit_sleep_ms=0"}
			command.ExtraFiles = []*os.File{child, pcChild}
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
			diagnostic := &t422NativeQueryOutput{cancel: cancel}
			command.Stderr = diagnostic
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if command.ProcessState == nil {
					cancel()
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			_ = child.Close()
			_ = pcChild.Close()
			if err := dispatchadmission.SendProductionBootstrap(ctx, parent, pcParent, record); err != nil {
				t.Fatal(err)
			}
			served := make(chan error, 1)
			go func() { served <- controller.Serve(ctx, record.Producer.ID, command.Process.Pid, parent) }()
			joined := false
			defer func() {
				if !joined {
					cancel()
				}
				if err := <-served; joined && err != nil {
					t.Error("DA join", err)
				}
			}()
			control, err := dispatchadmission.NewPhaseControl(ctx, pcParent, record.Producer.Binding, record.Control)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = control.Close() }()
			scanner := bufio.NewScanner(output)
			if !scanner.Scan() || scanner.Text() != "ready" {
				t.Fatal("helper startup", scanner.Text(), scanner.Err())
			}
			if err := control.DrainOwners(ctx); err != nil {
				t.Fatal(err)
			}
			advances := 2
			if mode == "wrong_phase" {
				advances = 1
			}
			if mode == "wrong_epoch" {
				advances = 0
			}
			for range advances {
				for _, operation := range []func() error{
					func() error { return control.Pause(ctx) }, controller.Fence,
					func() error { return control.Checkpoint(ctx) }, controller.Advance,
					func() error { return control.Resume(ctx) },
				} {
					if err := operation(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := control.OpenRequests(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintln(input, control.RequestToken()); err != nil {
				t.Fatal(err)
			}
			if !scanner.Scan() || scanner.Text() != "checked" {
				t.Fatal("selected request", scanner.Text(), scanner.Err())
			}
			for _, operation := range []func() error{
				func() error { return control.FenceRequests(ctx) }, func() error { return control.Pause(ctx) }, controller.Fence,
				func() error { return control.Checkpoint(ctx) },
			} {
				if err := operation(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := input.Write([]byte{'c'}); err != nil {
				t.Fatal(err)
			}
			if !scanner.Scan() || scanner.Text() != "joined" {
				t.Fatal("helper close", scanner.Text(), scanner.Err())
			}
			// Drain the bounded test runner tail before sole Wait closes stdout.
			for lines := 0; scanner.Scan(); lines++ {
				if lines >= 10 {
					cancel()
					t.Fatal("unexpected helper tail")
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err != nil {
				t.Fatal("helper join", err, diagnostic.buffer.String())
			}
			joined = true
			if snapshot, err := controller.Snapshot(); err != nil || !snapshot.Complete || snapshot.Attempts != 0 {
				t.Fatal("actual empty dispatch closure", snapshot, err)
			}
		})
	}
}

func TestT422QueryEvidenceInheritedHelper(t *testing.T) {
	mode := os.Getenv(t422QueryEvidenceInheritedMode)
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal("bootstrap", err)
	}
	_, raw := t422QueryEvidenceInheritedRecord(t, mode)
	snapshot, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		t.Fatal(err)
	}
	launch, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal("owners", err)
	}
	failures, calls := 0, 0
	launch.fail = func(error) { failures++ }
	var report t421ExactReadReport
	state := t421NewExactReadAccountingState(func(raw []byte) error { return json.Unmarshal(raw, &report) }, launch.fail,
		t421ExactFinalAuthorityRead{Read: func(ctx context.Context) ([]byte, func() error, error) {
			calls++
			if selected, _ := ctx.Value(t422QueryEvidenceKey{}).(bool); !selected {
				t.Error("selected F extension context absent")
			}
			return []byte(`{"supplied_fixture":true}`), nil, nil
		}})
	state.semantic = launch
	authCtx, cancelAuth := context.WithCancel(ctx)
	defer cancelAuth()
	authService, err := auth.New(authCtx, auth.Options{Store: &t422LifecycleAuthFixture{}, Owners: owners, Config: config.Auth{APIKey: t421ExactReadTestCredential}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancelAuth(); authService.WaitCleanup() }()
	handler := t422OwnerHTTPHandler(owners, authService.Require(state.wrap(http.NotFoundHandler())), launch)
	fmt.Println("ready")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || scanner.Text() == "" {
		t.Fatal("request token missing", scanner.Err())
	}
	request := exactT421ReadRequest(http.MethodGet, t421ExactFinalAuthorityPath, 1).WithContext(ctx)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, scanner.Text())
	request.Header.Set(t422QueryEvidenceHeader, t422QueryEvidenceValue)
	response := serveT421ExactReadRequest(t, handler, request)
	if mode == "complete" {
		if response.status != http.StatusOK || calls != 1 || failures != 0 || report.Status != "complete" || report.VisibleRepositories != nil {
			t.Fatal("selected F failed", response.status, calls, failures, report)
		}
	} else if response.status != http.StatusConflict || calls != 0 || failures != 1 || report.Status != "admission_refused" {
		t.Fatal("wrong epoch/phase reached supplied reader", response.status, calls, failures, report)
	}
	fmt.Println("checked")
	var closeSignal [1]byte
	if _, err := io.ReadFull(os.Stdin, closeSignal[:]); err != nil || closeSignal[0] != 'c' {
		t.Fatal("close signal", err)
	}
	// Owners and request callbacks are joined before the real PC/DA EOF close.
	cancelAuth()
	authService.WaitCleanup()
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal("lifetime close", err)
	}
	fmt.Println("joined")
}
