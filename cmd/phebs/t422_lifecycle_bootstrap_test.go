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
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
)

const t422LifecycleBootstrapMode = "PHEBS_T422_LIFECYCLE_BOOTSTRAP_TEST"

func t422LifecycleBootstrapRecord(t *testing.T) (dispatchadmission.ProductionBootstrap, []byte) {
	t.Helper()
	raw, _ := t422SemanticTestRequest(t)
	raw = bytes.Replace(raw, []byte(`"server_epoch":1`), []byte(`"server_epoch":4`), 1)
	record := t422ServeFlagsRecord()
	record.SemanticMode, record.InputSHA256 = dispatchadmission.ProductionSemanticV3, sha256.Sum256(raw)
	record.Phase, record.Limits.Phases = 8, 2
	record.Control.OwnerControl = true
	record.Control.Phases, record.Control.InitialPhase, record.Control.MaximumPhases = []uint32{8, 9}, 8, 2
	return record, raw
}

// This composes the real inherited bootstrap, PC01, owner/request admission,
// normal authentication, concrete helper and exact-R completion spine. Native
// controller execution uses tiny test owners/cursors and supplied capacity;
// this issues no input custody, physical-pressure, store or freeze authority.
func TestT422LifecycleInheritedMechanics(t *testing.T) {
	for _, mode := range []string{"complete", "undrained", "sink-panic", "cancel-result"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			record, _ := t422LifecycleBootstrapRecord(t)
			configuration := dispatchadmission.Config{Limits: record.Limits, Producers: []dispatchadmission.Producer{record.Producer}}
			for _, phase := range record.Control.Phases {
				configuration.Phases = append(configuration.Phases, dispatchadmission.Phase{ID: phase, Roles: []dispatchadmission.RoleBudget{
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
			t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })
			controlParent, controlChild, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = controlParent.Close(); _ = controlChild.Close() })
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422LifecycleBootstrapHelper$")
			command.Env = []string{t422LifecycleBootstrapMode + "=" + mode,
				dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionSelector, "GORACE=atexit_sleep_ms=0"}
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
			serveClean := false
			defer func() {
				if !serveClean {
					cancel()
				}
				select {
				case err := <-served:
					if serveClean && err != nil {
						t.Error("admission receiver failed after clean child join", err)
					}
				case <-time.After(3 * time.Second):
					t.Error("inherited admission receiver did not join")
				}
			}()
			phase, err := dispatchadmission.NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = phase.Close() }()
			scanner := bufio.NewScanner(output)
			if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "http://127.0.0.1:") {
				lines := []string{scanner.Text()}
				for len(lines) < 10 && scanner.Scan() {
					lines = append(lines, scanner.Text())
				}
				t.Fatal("child did not expose its bounded test listener", lines, scanner.Err())
			}
			address := scanner.Text()
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
			defer client.CloseIdleConnections()
			request := func(path string, read bool) (int, error) {
				method := http.MethodPost
				if read {
					method = http.MethodGet
				}
				req, err := http.NewRequestWithContext(ctx, method, address+path, nil)
				if err != nil {
					return 0, err
				}
				token := phase.RequestToken()
				if token == "" {
					return 0, errors.New("private request window is unavailable")
				}
				req.Header.Set(dispatchadmission.ProductionRequestHeader, token)
				req.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
				if read {
					req.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
					req.Header.Set(t421ExactReadOrdinalHeader, "1")
				}
				response, err := client.Do(req)
				if err != nil {
					return 0, err
				}
				_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, t422LifecycleReadBytes+1))
				return response.StatusCode, errors.Join(readErr, response.Body.Close())
			}
			if status, err := request(t422LifecycleParkPath, false); err != nil || status != http.StatusOK {
				t.Fatal("admitted initial Park failed", status, err)
			}
			for _, operation := range []func() error{
				func() error { return phase.DrainOwners(ctx) }, func() error { return phase.Pause(ctx) }, controller.Fence,
				func() error { return phase.Checkpoint(ctx) }, controller.Advance, func() error { return phase.Resume(ctx) },
			} {
				if err := operation(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "undrained" {
				err = phase.ReopenOwners(ctx)
			} else {
				err = phase.OpenRequests(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			status, err := request(t422LifecycleNormalDrive, false)
			want := http.StatusConflict
			if mode == "complete" {
				want = http.StatusOK
			}
			if err != nil || status != want {
				t.Fatal("actual admitted drive result", mode, status, err)
			}
			if mode == "complete" {
				readDone := make(chan error, 1)
				go func() {
					status, err := request(t422LifecycleNormalRead, true)
					if err == nil && status != http.StatusOK {
						err = fmt.Errorf("read status %d", status)
					}
					readDone <- err
				}()
				if !scanner.Scan() || scanner.Text() != "report_pending" {
					t.Fatal("actual R report did not reach its tail", scanner.Text())
				}
				fenced := make(chan error, 1)
				go func() { fenced <- phase.FenceRequests(ctx) }()
				select {
				case err := <-fenced:
					t.Fatal("request fence skipped pending R report", err)
				case <-time.After(20 * time.Millisecond):
				}
				if _, err := input.Write([]byte{'r'}); err != nil {
					t.Fatal(err)
				}
				if err := <-readDone; err != nil {
					t.Fatal(err)
				}
				if err := <-fenced; err != nil {
					t.Fatal(err)
				}
			} else if mode == "undrained" {
				if err := phase.DrainOwners(ctx); err != nil {
					t.Fatal(err)
				}
			} else if err := phase.FenceRequests(ctx); err != nil {
				t.Fatal(err)
			}
			if err := phase.Pause(ctx); err != nil {
				t.Fatal(err)
			}
			if err := controller.Fence(); err != nil {
				t.Fatal(err)
			}
			if err := phase.Checkpoint(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := input.Write([]byte{'c'}); err != nil {
				t.Fatal(err)
			}
			if !scanner.Scan() || scanner.Text() != "mechanics_joined" {
				t.Fatal("child did not finish bounded mechanics", scanner.Text(), scanner.Err())
			}
			if err := command.Wait(); err != nil {
				t.Fatal("child did not join", err, diagnostic.String())
			}
			serveClean = true
			snapshot, err := controller.Snapshot()
			if err != nil || !snapshot.Complete || snapshot.Attempts != 0 {
				t.Fatal("helper did not close empty process-dispatch prefix", snapshot, err)
			}
		})
	}
}

type t422LifecycleCursorFixture map[string]struct {
	cursor   string
	revision uint64
}

func (values t422LifecycleCursorFixture) GetLifecycleCursor(_ context.Context, key string) (string, uint64, error) {
	value := values[key]
	return value.cursor, value.revision, nil
}

func (values t422LifecycleCursorFixture) CompareAndSwapLifecycleCursor(_ context.Context, key string, revision uint64, cursor string) error {
	if values[key].revision != revision {
		return lifecycle.ErrCursorConflict
	}
	values[key] = struct {
		cursor   string
		revision uint64
	}{cursor, revision + 1}
	return nil
}

type t422LifecycleOwnerFixture struct {
	name   string
	turns  *atomic.Uint64
	cancel context.CancelFunc
}

// Only the real legacy bearer authentication calls used by this test are
// supplied; any other store method hits the nil embedded interface and fails
// the test instead of silently manufacturing another backend capability.
type t422LifecycleAuthFixture struct {
	store.AuthStore
	key store.APIKey
}

func (*t422LifecycleAuthFixture) AuthStats(context.Context) (store.AuthStats, error) {
	return store.AuthStats{Users: 1, SetupComplete: true}, nil
}

func (fixture *t422LifecycleAuthFixture) SetLegacyAPIKey(_ context.Context, hash string, at time.Time) error {
	fixture.key = store.APIKey{ID: "legacy-config", Hash: hash, LastUsedAt: &at}
	return nil
}

func (fixture *t422LifecycleAuthFixture) GetAPIKey(_ context.Context, id string) (*store.APIKey, error) {
	if id != fixture.key.ID {
		return nil, store.ErrNotFound
	}
	key := fixture.key
	return &key, nil
}

func (*t422LifecycleAuthFixture) DeleteExpiredAuthSessions(context.Context, time.Time) (int, error) {
	return 0, nil
}

func (owner t422LifecycleOwnerFixture) Name() string { return owner.name }

func (owner t422LifecycleOwnerFixture) Sweep(_ context.Context, _ time.Time, _ string, _ lifecycle.Limits) lifecycle.OwnerResult {
	owner.turns.Add(1)
	if owner.cancel != nil {
		owner.cancel()
	}
	return lifecycle.OwnerResult{Completeness: lifecycle.Exact, Scanned: 1, Deleted: 1}
}

func TestT422LifecycleBootstrapHelper(t *testing.T) {
	mode := os.Getenv(t422LifecycleBootstrapMode)
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal("real inherited bootstrap failed", err)
	}
	_, raw := t422LifecycleBootstrapRecord(t)
	snapshot, err := dispatchadmission.ProductionSemanticState()
	if err != nil || snapshot.OrdinaryOwnersDrained {
		t.Fatal("initial owner fence was invented", err)
	}
	launch, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	runnerCtx, stopRunner := context.WithCancel(ctx)
	defer stopRunner()
	var failures atomic.Uint64
	launch.fail = func(error) { failures.Add(1); stopRunner() }
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal("owner binding failed", err)
	}
	var turns, probes atomic.Uint64
	var nativeOwners []lifecycle.Owner
	for index := 0; index < 16; index++ {
		owner := t422LifecycleOwnerFixture{name: "test-owner-" + strconv.Itoa(index), turns: &turns}
		if mode == "cancel-result" {
			owner.cancel = stopRunner
		}
		nativeOwners = append(nativeOwners, owner)
	}
	control, err := newT422LifecycleControl(runnerCtx, launch, nativeOwners)
	if err != nil {
		t.Fatal(err)
	}
	control.sink = func([]byte) error {
		if mode == "sink-panic" {
			panic("private fixture sink panic")
		}
		return nil
	}
	controller, err := lifecycle.NewController(t422LifecycleCursorFixture{}, nativeOwners...)
	if err != nil {
		t.Fatal(err)
	}
	gate := lifecycle.NewGateWithProbe("supplied-mechanics-capacity", func(context.Context, string) (lifecycle.Capacity, error) {
		probes.Add(1)
		return lifecycle.Capacity{TotalBytes: 100, AvailableBytes: 90, UsedBytes: 10}, nil
	})
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		lifecycle.RunWithControl(runnerCtx, controller, gate, time.Hour, time.Millisecond, control.ObserveOwner, nil, owners, control.runner)
	}()
	defer func() { stopRunner(); <-runnerDone }()
	state := t421NewExactReadAccountingState(func(raw []byte) error {
		var report t421ExactReadReport
		if json.Unmarshal(raw, &report) != nil || report.Schema != t421ExactReadReportSchema || report.Status != "complete" ||
			report.RequestOrdinal != 1 || report.ControlFileReads != 0 || report.StoreReadAttempts != 0 ||
			report.MemberVisits != 0 || report.StoreWriteAttempts != 0 {
			return errors.New("actual R accounting report changed its zero-native-read scope")
		}
		control.mu.Lock()
		pending := control.step == 2 && control.busy
		control.mu.Unlock()
		if !pending || turns.Load() != 16 || probes.Load() != 16 {
			return errors.New("R did not retain completed native cycle while pending")
		}
		fmt.Println("report_pending")
		var release [1]byte
		if _, err := io.ReadFull(os.Stdin, release[:]); err != nil || release[0] != 'r' {
			return errors.New("bounded report release failed")
		}
		return nil
	}, launch.fail)
	state.semantic, state.lifecycle = launch, control
	authService, err := auth.New(runnerCtx, auth.Options{Store: &t422LifecycleAuthFixture{}, Owners: owners,
		Config: config.Auth{APIKey: t421ExactReadTestCredential}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stopRunner(); authService.WaitCleanup() }()
	handler := t422OwnerHTTPHandler(owners, authService.Require(state.wrap(http.NotFoundHandler())), launch)
	server := httptest.NewUnstartedServer(handler)
	server.Config.BaseContext = func(net.Listener) context.Context { return runnerCtx }
	server.Start()
	defer server.Close()
	fmt.Println(server.URL)
	var closeSignal [1]byte
	// In the success path the R callback consumes the first byte. Do not race
	// it for stdin: wait for the helper's real post-report step first.
	if mode == "complete" {
		for {
			control.mu.Lock()
			completed := control.step == 3 && !control.busy
			control.mu.Unlock()
			if completed {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("post-report helper did not finish")
			case <-time.After(time.Millisecond):
			}
		}
	}
	if _, err := io.ReadFull(os.Stdin, closeSignal[:]); err != nil || closeSignal[0] != 'c' {
		t.Fatal("bounded terminal signal failed", err)
	}
	stopRunner()
	<-runnerDone
	control.mu.Lock()
	prefix, step := control.prefix[0], control.step
	control.mu.Unlock()
	switch mode {
	case "complete":
		if turns.Load() != 16 || probes.Load() != 16 || prefix.OwnerTurns != 16 || prefix.Deleted != 16 || failures.Load() != 0 || step != 3 {
			t.Fatal("completed helper invented, repeated or lost actual work", turns.Load(), probes.Load(), prefix, step)
		}
	case "undrained":
		if turns.Load() != 0 || probes.Load() != 0 || prefix.ReturnedTicks != 0 || failures.Load() == 0 || step != 1 {
			t.Fatal("undrained request crossed native boundary")
		}
	case "sink-panic", "cancel-result":
		if turns.Load() != 1 || probes.Load() != 0 || prefix.OwnerTurns != 1 || prefix.Deleted != 1 || failures.Load() == 0 || step != 1 {
			t.Fatal("returned positive prefix was lost or extra native work admitted", turns.Load(), probes.Load(), prefix, step)
		}
	default:
		t.Fatal("unknown mechanics mode")
	}
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal("producer/control closure failed", err)
	}
	fmt.Println("mechanics_joined")
}
