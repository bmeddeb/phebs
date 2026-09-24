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
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

const t422QueryEvidenceInheritedMode = "PHEBS_T422_QUERY_EVIDENCE_INHERITED"

func t422QueryEvidenceInheritedRecord(t *testing.T, mode string) (dispatchadmission.ProductionBootstrap, []byte) {
	t.Helper()
	if mode == "reuse_prior" {
		raw, _ := t422SemanticTestRequest(t)
		record := t422ServeFlagsRecord()
		record.Producer.ID, record.Phase, record.Limits.Phases = 2, 2, 3
		record.SemanticMode, record.InputSHA256 = dispatchadmission.ProductionSemanticV3, sha256.Sum256(raw)
		record.Control.OwnerControl = true
		record.Control.Phases, record.Control.InitialPhase, record.Control.MaximumPhases = []uint32{2, 3, 4}, 2, 3
		return record, raw
	}
	record, raw := t422LifecycleBootstrapRecord(t)
	if mode != "wrong_epoch" && mode != "archive_tail_wrong_epoch" {
		raw = bytes.Replace(raw, []byte(`"server_epoch":4`), []byte(`"server_epoch":5`), 1)
		record.Producer.ID, record.Phase, record.Limits.Phases = 6, 12, 3
		record.Control.Phases, record.Control.InitialPhase, record.Control.MaximumPhases = []uint32{12, 13, 14}, 12, 3
		record.InputSHA256 = sha256.Sum256(raw)
	}
	return record, raw
}

// Real inherited DA/PC, owner/request admission and auth are exercised here.
// The F and archive-tail bodies are supplied: this is not an engine/catalog or phase pass.
func TestT422QueryEvidenceInheritedRouting(t *testing.T) {
	for _, mode := range []string{"complete", "wrong_phase", "wrong_epoch", "reuse_complete", "reuse_cancel", "reuse_write", "reuse_phase", "reuse_request", "reuse_prior", "continuity_complete", "continuity_duplicate", "continuity_no_semantic", "archive_tail_complete", "archive_tail_wrong_phase", "archive_tail_wrong_epoch"} {
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
			if mode == "wrong_phase" || mode == "reuse_phase" || mode == "reuse_prior" || mode == "archive_tail_wrong_phase" {
				advances = 1
			}
			if mode == "wrong_epoch" || mode == "archive_tail_complete" || mode == "archive_tail_wrong_epoch" {
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
				t.Fatal("selected request", scanner.Text(), scanner.Err(), diagnostic.buffer.String())
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

type t422ReuseOrderWriter struct {
	output *bytes.Buffer
	events *[]string
	prior  func() bool
}

func (writer t422ReuseOrderWriter) Write(raw []byte) (int, error) {
	event := "reuse"
	if writer.prior != nil {
		event = "reuse_after_prior"
		if !writer.prior() {
			event = "reuse_before_prior"
		}
	}
	*writer.events = append(*writer.events, event)
	return writer.output.Write(raw)
}

func TestT422QueryEvidenceInheritedHelper(t *testing.T) {
	mode := os.Getenv(t422QueryEvidenceInheritedMode)
	if mode == "" {
		return
	}
	continuity := strings.HasPrefix(mode, "continuity_")
	archiveTail := strings.HasPrefix(mode, "archive_tail_")
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
	failures, calls, tailCalls := 0, 0, 0
	launch.fail = func(error) { failures++ }
	var report t421ExactReadReport
	var reports int
	var cancelTerminal context.CancelFunc
	events := []string{}
	reportSink := func(raw []byte) error {
		reports++
		events = append(events, "report")
		if mode == "reuse_cancel" && reports == 2 && cancelTerminal != nil {
			cancelTerminal()
		}
		return json.Unmarshal(raw, &report)
	}
	finalBody := []byte(`{"supplied_fixture":true}`)
	if mode == "reuse_prior" {
		finalBody = []byte(`{"schema":"t421-final-authority-source-free-v1","authority":{"search_generation_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","search_inventory":{"records":1},"current":true}}`)
	}
	state := t421NewExactReadAccountingState(reportSink, launch.fail,
		t421ExactFinalAuthorityRead{Read: func(ctx context.Context) ([]byte, func() error, error) {
			calls++
			if selected, _ := ctx.Value(t422QueryEvidenceKey{}).(bool); !selected && mode != "reuse_prior" && !continuity {
				t.Error("selected F extension context absent")
			}
			if selected, _ := ctx.Value(t422CallerContinuityKey{}).(bool); selected != continuity {
				t.Error("caller continuity opt-in context differs")
			}
			return finalBody, nil, nil
		}},
		t421ExactFinalAuthorityRead{Limits: t421TailReadinessLimits(), Read: func(ctx context.Context) ([]byte, func() error, error) {
			tailCalls++
			if selected, _ := ctx.Value(t422ArchiveTailKey{}).(bool); !selected {
				t.Error("archive tail opt-in context absent")
			}
			if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 7); err != nil {
				return nil, nil, err
			}
			if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 23); err != nil {
				return nil, nil, err
			}
			return []byte(`{"schema":"t421-tail-readiness-source-free-v1","status":"ready"}` + "\n"), nil, nil
		}})
	state.semantic = launch
	if mode == "continuity_no_semantic" {
		state.semantic = nil
	}
	var reuseOutput bytes.Buffer
	var reuse *t422ReuseControl
	if mode == "reuse_complete" || mode == "reuse_cancel" || mode == "reuse_write" || mode == "reuse_phase" || mode == "reuse_request" || mode == "reuse_prior" {
		reuse, err = newT422ReuseControl(launch, launch.fail)
		if err != nil {
			t.Fatal("reuse control", err)
		}
		reuse.writer = t422ReuseOrderWriter{output: &reuseOutput, events: &events}
		state.reuse = reuse
	}
	if mode == "reuse_prior" {
		retention := &t422RetentionControl{ctx: ctx, launch: launch}
		state.retention = retention
		reuse.writer = t422ReuseOrderWriter{output: &reuseOutput, events: &events, prior: func() bool {
			retention.mu.Lock()
			defer retention.mu.Unlock()
			return retention.step == 1 && !retention.busy
		}}
	}
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
	token := scanner.Text()
	path := t421ExactFinalAuthorityPath
	if archiveTail {
		path = t421ExactTailReadinessPath
	}
	request := exactT421ReadRequest(http.MethodGet, path, 1).WithContext(ctx)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, token)
	if mode != "reuse_prior" && !continuity && !archiveTail {
		request.Header.Set(t422QueryEvidenceHeader, t422QueryEvidenceValue)
	}
	if archiveTail {
		request.Header.Set(t422ArchiveTailHeader, t422ArchiveTailValue)
	}
	if continuity {
		request.Header.Set(t422CallerContinuityHeader, t422CallerContinuityValue)
		if mode == "continuity_duplicate" {
			request.Header.Add(t422CallerContinuityHeader, t422CallerContinuityValue)
		}
	}
	if mode == "reuse_phase" {
		request.Header.Set(t422QueryTerminalHeader, t422QueryTerminalValue)
	}
	if mode == "reuse_request" {
		request.Header.Add(t422QueryTerminalHeader, t422QueryTerminalValue)
		request.Header.Add(t422QueryTerminalHeader, t422QueryTerminalValue)
	}
	response := serveT421ExactReadRequest(t, handler, request)
	if mode == "reuse_complete" || mode == "reuse_cancel" || mode == "reuse_write" {
		if response.status != http.StatusOK || calls != 1 || failures != 0 || reports != 1 || reuseOutput.Len() != 0 || len(events) != 1 || events[0] != "report" {
			t.Fatal("first F completed reuse", response.status, calls, failures, reports, reuseOutput.String(), events)
		}
		requestCtx, cancelRequest := context.WithCancel(ctx)
		cancelTerminal = cancelRequest
		defer cancelRequest()
		if mode == "reuse_write" {
			reuse.writer = t422ReuseFailWriter{}
		}
		terminal := exactT421ReadRequest(http.MethodGet, t421ExactFinalAuthorityPath, 2).WithContext(requestCtx)
		terminal.Header.Set(dispatchadmission.ProductionRequestHeader, token)
		terminal.Header.Set(t422QueryEvidenceHeader, t422QueryEvidenceValue)
		terminal.Header.Set(t422QueryTerminalHeader, t422QueryTerminalValue)
		response = serveT421ExactReadRequest(t, handler, terminal)
		wantFailure := 0
		if mode != "reuse_complete" {
			wantFailure = 1
		}
		if response.status != http.StatusOK || calls != 2 || reports != 2 || failures != wantFailure {
			t.Fatal("terminal F outcome", response.status, calls, reports, failures, report)
		}
		if mode == "reuse_complete" {
			if reuseOutput.String() != "RU1:6:E:00000\n" || len(events) != 3 || events[0] != "report" || events[1] != "report" || events[2] != "reuse" {
				t.Fatal("reuse did not follow the exact report", reuseOutput.String(), events)
			}
		} else {
			if reuseOutput.Len() != 0 || len(events) != 2 || events[0] != "report" || events[1] != "report" {
				t.Fatal("failed terminal emitted reuse", reuseOutput.String(), events)
			}
		}
	} else if mode == "reuse_prior" {
		if response.status != http.StatusOK || calls != 1 || failures != 0 || reports != 1 ||
			reuseOutput.String() != "RU1:2:3:00000\n" || len(events) != 2 || events[0] != "report" || events[1] != "reuse_after_prior" {
			t.Fatal("reuse did not follow prior/report", response.status, calls, failures, reports, reuseOutput.String(), events)
		}
	} else if mode == "archive_tail_complete" {
		if response.status != http.StatusOK || calls != 0 || tailCalls != 1 || failures != 0 || reports != 1 ||
			report.Status != "complete" || report.ControlFileReads != 7 || report.StoreReadAttempts != 23 {
			t.Fatal("selected archive tail failed", response.status, calls, tailCalls, failures, reports, report)
		}
	} else if mode == "complete" || mode == "continuity_complete" {
		if response.status != http.StatusOK || calls != 1 || failures != 0 || report.Status != "complete" || report.VisibleRepositories != nil {
			t.Fatal("selected F failed", response.status, calls, failures, report)
		}
	} else if mode == "reuse_phase" || mode == "reuse_request" {
		if response.status != http.StatusConflict || calls != 0 || failures != 1 || reports != 1 || report.Status != "admission_refused" || reuseOutput.Len() != 0 {
			t.Fatal("mismatched terminal reached reuse", response.status, calls, failures, reports, report, reuseOutput.String())
		}
	} else if response.status != http.StatusConflict || calls != 0 || tailCalls != 0 || failures != 1 || report.Status != "admission_refused" {
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
