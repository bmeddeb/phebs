//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

const t422CheckpointBridgeEnvironment = "PHEBS_TEST_CHECKPOINT_BRIDGE_INPUT"

type t422CheckpointBridge struct {
	Input           t422CheckpointRecoveryInput `json:"input"`
	RuntimeRoot     string                      `json:"runtime_root"`
	Schedule        store.GenerationSchedule    `json:"schedule"`
	DataDir         string                      `json:"data_dir,omitempty"`
	DeferredSibling bool                        `json:"deferred_sibling,omitempty"`
}

func loadT422CheckpointBridge(t *testing.T, path string) t422CheckpointBridge {
	t.Helper()
	raw, err := os.ReadFile(path)
	var value t422CheckpointBridge
	if err != nil || len(raw) > 32<<10 || json.Unmarshal(raw, &value) != nil || !validT422CheckpointRecoveryInput(value.Input) || !filepath.IsAbs(value.RuntimeRoot) {
		t.Fatal("invalid test bridge input", err)
	}
	return value
}

// Test-only producer/recovery/HTTP bridge. No source exports, production hook,
// privileged driver, existing credential or retained native fixture is used.
func TestT422CheckpointRecoveryHTTPBridge(t *testing.T) {
	runT422CheckpointHTTPBridgeCases(t, false)
}

func runT422CheckpointHTTPBridgeCases(t *testing.T, realStore bool) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	exporter := filepath.Join(work, "exporter.test")
	build := exec.CommandContext(t.Context(), "go", "test", "-c", "-race", "-o", exporter, "./internal/extractionpublication")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal("build test-only fixture producer", err, string(output))
	}
	exported := filepath.Join(work, "exported")
	produce := exec.CommandContext(t.Context(), exporter, "-test.run=^TestCheckpointBridgeExportHelper$", "-test.timeout=1m")
	produce.Env = append(os.Environ(), "PHEBS_TEST_CHECKPOINT_BRIDGE_EXPORT="+exported, "GORACE=atexit_sleep_ms=0")
	if output, err := produce.CombinedOutput(); err != nil {
		t.Fatal("generate test-only checkpoint controls", err, string(output))
	}
	var metadata struct {
		Repository string                                            `json:"repository"`
		Hit        extractionpublication.CheckpointRestartTransition `json:"hit"`
		Root       extractionpublication.RecoveryPreparationRoot     `json:"root"`
		Schedule   store.GenerationSchedule                          `json:"schedule"`
	}
	raw, err := os.ReadFile(filepath.Join(exported, "metadata.json"))
	if err != nil || json.Unmarshal(raw, &metadata) != nil {
		t.Fatal("read generated fixture", err)
	}
	modes := []string{"http_complete", "http_recovered_first", "http_wrong_chunk", "http_same_lease", "http_report_failure", "http_wrong_trailer", "http_wrong_plan", "http_wrong_offset"}
	if realStore {
		modes = append(modes[:5], "http_deferred_sibling")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			private := t.TempDir()
			copyRoot := filepath.Join(private, "runtime")
			if err := filepath.WalkDir(filepath.Join(exported, "runtime"), func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(filepath.Join(exported, "runtime"), path)
				if err != nil {
					return err
				}
				dest := filepath.Join(copyRoot, rel)
				if entry.IsDir() {
					return os.Mkdir(dest, 0o700)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				return os.WriteFile(dest, data, 0o600)
			}); err != nil {
				t.Fatal(err)
			}
			input := t422CheckpointRecoveryFixture()
			input.Offset, input.Hit, input.Roots[4] = 2, metadata.Hit, metadata.Root
			input.Prior.CandidateGenerationSHA256 = metadata.Hit.CandidateGenerationDigest
			input.Prior.SourceGenerationSHA256, input.Prior.ObservationGenerationSHA256 = metadata.Hit.SourceGenerationDigest, metadata.Hit.ObservationGenerationDigest
			if mode == "http_wrong_plan" {
				input.Hit.PlanDigest, input.Roots[4].PlanDigest = t422CheckpointTestDigest, t422CheckpointTestDigest
			}
			bridge := t422CheckpointBridge{Input: input, RuntimeRoot: copyRoot, Schedule: metadata.Schedule, DeferredSibling: mode == "http_deferred_sibling"}
			if realStore {
				prepareT422RealStoreBridge(t, &bridge, private)
			}
			raw, err := json.Marshal(bridge)
			path := filepath.Join(private, "input.json")
			if err != nil || os.WriteFile(path, raw, 0o600) != nil {
				t.Fatal("write bridge input", err)
			}
			runT422CheckpointRecoveryInherited(t, mode, path)
		})
	}
}

// Bounded supplied store rows. Real runtime Handle/Read validate generated
// immutable files, and real ledger/report code charges their actual reads.
type t422BridgeStore struct {
	*t422RecoverySchedulerStore
	store.AuthStore // Unexpected auth methods fail instead of faking success.
	mu              sync.Mutex
	schedule        store.GenerationSchedule
	key             store.APIKey
}

func (state *t422BridgeStore) GetGenerationSchedule(_ context.Context, repo, stage string) (*store.GenerationSchedule, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if repo != state.schedule.Repository || stage != state.schedule.Stage {
		return nil, store.ErrGenerationStale
	}
	value := state.schedule
	return &value, nil
}

func (state *t422BridgeStore) RetireCurrentGenerationSchedule(context.Context, store.GenerationSchedule) error {
	panic("unexpected fixture retirement")
}

func (state *t422BridgeStore) CompleteGenerationChunk(ctx context.Context, chunk store.GenerationChunk) error {
	state.mu.Lock()
	state.schedule.Status, state.schedule.Pending, state.schedule.Running, state.schedule.Succeeded = store.GenerationScheduleSettled, 0, 0, 3
	state.mu.Unlock()
	return state.t422RecoverySchedulerStore.CompleteGenerationChunk(ctx, chunk)
}

func (state *t422BridgeStore) ReadGenerationStaleLeaseTransition(ctx context.Context, request store.GenerationStaleLeaseTransitionRequest) (store.GenerationStaleLeaseTransition, error) {
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 2); err != nil {
		return store.GenerationStaleLeaseTransition{}, err
	}
	event := state.hit
	if request.Point != store.GenerationStaleLeaseTransitionRecovered || request.Repository != event.Repository || request.Stage != event.Stage || request.Generation != event.Generation ||
		request.ResourceClass != event.ResourceClass || request.ScheduleDigest != event.ScheduleDigest || request.ChunkIdentity != event.ChunkIdentity || request.Offset != event.Offset || request.Length != 1 || request.Attempt != 0 || !request.StaleBefore.IsZero() {
		return store.GenerationStaleLeaseTransition{}, store.ErrGenerationStale
	}
	state.mu.Lock()
	settled := state.schedule.Status == store.GenerationScheduleSettled
	state.mu.Unlock()
	if !settled || !state.completed.Load() {
		return store.GenerationStaleLeaseTransition{}, store.ErrGenerationStale
	}
	event.Point, event.Priority, event.ChunkStatus, event.Leased = store.GenerationStaleLeaseTransitionRecovered, store.GenerationPriorityStale, store.GenerationChunkDone, false
	event.ScheduleStatus, event.StaleBefore = store.GenerationScheduleSettled, time.Time{}
	event.PrivateLeaseTokenDigest, event.ChunkStateDigest, event.CheckpointStateDigest = "", t422CheckpointTestDigest, t422CheckpointTestDigest
	readaccounting.ObserveRecoverySchedule(ctx, event.ScheduleDigest, 3, 3)
	return event, nil
}

func (state *t422BridgeStore) SetLegacyAPIKey(_ context.Context, hash string, now time.Time) error {
	state.key = store.APIKey{ID: "legacy-config", Hash: hash, LastUsedAt: &now}
	return nil
}
func (*t422BridgeStore) AuthStats(context.Context) (store.AuthStats, error) {
	return store.AuthStats{Users: 1, SetupComplete: true}, nil
}
func (*t422BridgeStore) DeleteExpiredAuthSessions(context.Context, time.Time) (int, error) {
	return 0, nil
}
func (state *t422BridgeStore) GetAPIKey(_ context.Context, id string) (*store.APIKey, error) {
	if id != state.key.ID {
		return nil, store.ErrNotFound
	}
	value := state.key
	return &value, nil
}
func (*t422BridgeStore) TouchAPIKey(context.Context, string, time.Time) error { return nil }

type t422BridgeUnused struct {
	extractionpublication.Source
	extractionpublication.Executor
}
type t422BridgePublisher struct{}

func (t422BridgePublisher) PublishDomain(_ context.Context, plan candidate.DomainResultPlan, root candidate.DomainResultRoot, _ string) error {
	return candidate.ValidateDomainResultRoot(root, plan)
}
func (t422BridgePublisher) PublishRefusal(context.Context, candidate.DomainResultPlan, candidate.DomainResultRoot, string) error {
	panic("unexpected fixture publication refusal")
}

type t422BridgeFence struct{ repository, source string }

func (fence t422BridgeFence) FenceDomain(_ context.Context, request extractionpublication.FenceRequest) (func(), error) {
	if request.Plan.Repository != fence.repository || request.Plan.SourceGenerationDigest != fence.source || candidate.ValidateDomainResultPlanControl(request.Plan) != nil {
		return nil, extractionpublication.ErrStale
	}
	return func() {}, nil
}

func testT422CheckpointHTTPBridge(t *testing.T, ctx context.Context, mode string, launch *t422SemanticLaunch, owners *dispatchadmission.Owners, lifetime *dispatchadmission.ProductionLifetime, bridge t422CheckpointBridge, failures *atomic.Int32) {
	t.Helper()
	seq := &t422RecoverySchedulerStore{available: make(chan struct{}), recoveredEntered: make(chan struct{}), reaperContext: make(chan context.Context, 1), beforeRequeue: mode == "http_recovered_first", deferredSibling: bridge.DeferredSibling}
	var state t422HTTPBridgeStore = &t422BridgeStore{t422RecoverySchedulerStore: seq, schedule: bridge.Schedule}
	if bridge.DataDir != "" {
		state = openT422RealStoreBridge(t, ctx, bridge.DataDir, seq)
	}
	runtime := &extractionpublication.Runtime{Root: bridge.RuntimeRoot, Store: state, Source: t422BridgeUnused{}, Executor: t422BridgeUnused{}, Publisher: t422BridgePublisher{}, Fence: t422BridgeFence{launch.request.Repository, bridge.Input.Hit.SourceGenerationDigest}}
	control, err := newT422CheckpointRecoveryControl(ctx, launch, &extractionpublication.Reconciler{StoreAccounting: true, Runtime: runtime, Root: bridge.RuntimeRoot})
	if err != nil {
		t.Fatal(err)
	}
	defer control.cancel()
	seq.control = control
	hit := bridge.Input.Hit
	seq.hit = store.GenerationStaleLeaseTransition{Point: store.GenerationStaleLeaseTransitionHit, Repository: launch.request.Repository, Stage: extractionpublication.ScheduleStage,
		ResourceClass: store.GenerationResourceExtraction, Generation: hit.ScheduleGeneration, ScheduleDigest: hit.ScheduleDigest, ChunkIdentity: hit.ChunkIdentity,
		Offset: 2, Length: 1, ScheduleStatus: store.GenerationScheduleActive, ChunkStatus: store.GenerationChunkRunning, Leased: true,
		StaleBefore: time.Now().Add(-time.Minute), ChunkStateDigest: t422CheckpointTestDigest, CheckpointStateDigest: t422CheckpointTestDigest,
		PrivateLeaseTokenDigest: store.GenerationLeaseTokenDigest("killed-native-fixture-lease")}
	seq.requeued = seq.hit
	seq.requeued.Point, seq.requeued.Priority, seq.requeued.ChunkStatus, seq.requeued.Leased, seq.requeued.StaleBefore = store.GenerationStaleLeaseTransitionRequeued, store.GenerationPriorityStale, store.GenerationChunkPending, false, time.Time{}
	var siblingCallbacks atomic.Int32
	finished, joined := make(chan error, 1), make(chan error, 1)
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	scheduler := &generationscheduler.Scheduler{Store: state, Owners: owners, PollEvery: time.Hour, HeartbeatEvery: time.Hour, StaleAfter: 2 * time.Hour,
		Classes: map[store.GenerationResourceClass]generationscheduler.Class{store.GenerationResourceExtraction: {Concurrency: 1, Budget: generationscheduler.Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
			Handle: func(ctx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
				return runtime.Handle(ctx, chunk)
			},
			OnStaleLeaseTransition: func(ctx context.Context, event store.GenerationStaleLeaseTransition) error {
				selected := event.ChunkIdentity == hit.ChunkIdentity || event.Offset == int64(bridge.Input.Offset)
				if event.Point == store.GenerationStaleLeaseTransitionRecovered && selected {
					close(seq.recoveredEntered)
					if mode == "http_wrong_chunk" {
						event.ChunkIdentity = t422CheckpointTestDigest
					}
					if mode == "http_wrong_offset" {
						event.Offset++
					}
					if mode == "http_same_lease" {
						event.PrivateLeaseTokenDigest = seq.hit.PrivateLeaseTokenDigest
					}
				}
				err := control.observeTransition(ctx, event)
				if event.Point == store.GenerationStaleLeaseTransitionRecovered {
					if selected || err != nil {
						finished <- err
					} else {
						control.mu.Lock()
						credited := control.failure != nil || control.recovered.observer != nil || control.recovered.reported
						control.mu.Unlock()
						if credited {
							t.Error("unrelated completion supplied selected recovery credit")
						}
						siblingCallbacks.Add(1)
					}
				}
				return err
			}}}}
	if bridge.DataDir != "" {
		scheduler.PollEvery, scheduler.HeartbeatEvery, scheduler.StaleAfter = time.Second, 5*time.Second, 20*time.Second
	}
	go func() {
		if err := control.waitForReader(runCtx); err != nil {
			joined <- err
			return
		}
		joined <- scheduler.Run(runCtx)
	}()
	authCtx, stopAuth := context.WithCancel(ctx)
	service, err := auth.New(authCtx, auth.Options{Store: state, Owners: owners, Config: config.Auth{APIKey: t421ExactReadTestCredential}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stopAuth(); service.WaitCleanup() }()
	var sinkFailed atomic.Bool
	var sinkCalls atomic.Int32
	accounting := t421NewExactReadAccountingState(func([]byte) error {
		sinkCalls.Add(1)
		if mode == "http_report_failure" {
			sinkFailed.Store(true)
			return errors.New("supplied report sink failure")
		}
		return nil
	}, func(error) {})
	accounting.semantic, accounting.checkpointRecovery = launch, control
	inner := t422OwnerHTTPHandler(owners, service.Require(accounting.wrap(http.NotFoundHandler())), launch)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r)
		if mode == "http_wrong_trailer" {
			w.Header().Set(t421ExactReadTrailer, base64.RawURLEncoding.EncodeToString([]byte(`{"schema":"t421-source-free-read-accounting-v1","request_ordinal":2,"status":"complete","control_file_reads":7,"store_read_attempts":4,"member_visits":0,"store_write_attempts":0}`)))
		}
	}))
	defer server.Close()
	stdin := bufio.NewReader(os.Stdin)
	token, err := stdin.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+t422CheckpointRecoveredPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header = exactT421ReadRequest(http.MethodGet, t422CheckpointRecoveredPath, 1).Header
	request.Header.Set(dispatchadmission.ProductionRequestHeader, strings.TrimSuffix(token, "\n"))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<10))
	if closeErr := response.Body.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusConflict {
		t.Fatal("unexpected HTTP admission/auth status", response.StatusCode, string(body))
	}
	var callbackErr error
	select {
	case callbackErr = <-finished:
	case <-ctx.Done():
		t.Fatal("callback did not join", ctx.Err(), response.StatusCode, string(body), control.failure)
	}
	stop()
	if err := <-joined; err != nil {
		t.Fatal(err)
	}
	stopAuth()
	service.WaitCleanup()
	server.Close()
	encoded := response.Trailer.Values(t421ExactReadTrailer)
	var report t421ExactReadReport
	if len(encoded) != 1 {
		t.Fatal("missing exact trailer", response.StatusCode, string(body))
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded[0])
	if err != nil || json.Unmarshal(raw, &report) != nil {
		t.Fatal("invalid trailer", err)
	}
	var value extractionpublication.CheckpointRestartTransition
	expected := bridge.Input.Hit
	expected.Point, expected.Priority, expected.ChunkStatus, expected.Leased = store.GenerationStaleLeaseTransitionRecovered, store.GenerationPriorityStale, store.GenerationChunkDone, false
	expected.ScheduleStatus, expected.RootDigest = store.GenerationScheduleSettled, bridge.Input.Roots[4].RootDigest
	expected.CompletionBitSet, expected.RootExists, expected.Current, expected.PrivateLeaseChanged = true, true, true, true
	expected.ObservedScheduleChunks, expected.ObservedScheduleSuccesses = 3, 3
	validBody := json.Unmarshal(body, &value) == nil && value == expected
	accepted := response.StatusCode == http.StatusOK && report.Schema == t421ExactReadReportSchema && report.RequestOrdinal == 1 && report.Status == "complete" && report.ControlFileReads == 7 && report.StoreReadAttempts == 4 && report.MemberVisits == 0 && report.StoreWriteAttempts == 0 && validBody && callbackErr == nil && !sinkFailed.Load() && control.recovered.reported
	want := mode == "http_complete" || mode == "http_recovered_first" || mode == "http_deferred_sibling"
	if accepted != want || control.finalSeen || sinkCalls.Load() != 1 {
		t.Fatal("bridge acceptance differs", mode, response.StatusCode, report, callbackErr, control.failure, string(body))
	}
	if bridge.DeferredSibling && siblingCallbacks.Load() != 1 {
		t.Fatal("actual deferred sibling did not complete before selected recovery", siblingCallbacks.Load())
	}
	if want && (failures.Load() != 0 || control.failure != nil) {
		t.Fatal("valid bridge refused", control.failure)
	}
	if mode == "http_wrong_chunk" || mode == "http_wrong_offset" || mode == "http_same_lease" {
		if response.StatusCode != 409 || string(body) != "exact read request refused\n" || report.ControlFileReads != 0 || report.StoreReadAttempts != 0 || control.failure == nil {
			t.Fatal("refusal envelope differs", report, control.failure)
		}
	}
	if mode == "http_wrong_chunk" && (control.failure.Stage != t422RecoveryTransitionTarget || !control.failure.Event.Identity) ||
		mode == "http_same_lease" && (control.failure.Stage != t422RecoveryTransitionState || !control.failure.Event.Token) {
		t.Fatal("callback refusal not disambiguated", control.failure)
	}
	if mode == "http_wrong_offset" && (control.failure.Stage != t422RecoveryTransitionTarget || !control.failure.Event.Offset) {
		t.Fatal("selected offset mismatch bypassed strict control", control.failure)
	}
	if mode == "http_report_failure" && (!validBody || report.Status != "complete" || report.ControlFileReads != 7 || report.StoreReadAttempts != 4 || callbackErr == nil || control.recovered.reported || control.failure == nil || control.failure.Stage != t422RecoveryReadReport) {
		t.Fatal("report-sink failure gained acceptance", report, control.failure)
	}
	if mode == "http_wrong_trailer" && (!validBody || report.RequestOrdinal != 2 || callbackErr != nil || !control.recovered.reported || control.failure != nil) {
		t.Fatal("wire corruption did not remain separate from internal report release", report, control.failure)
	}
	if mode == "http_wrong_plan" && (response.StatusCode != 409 || control.failure == nil || control.failure.Stage != t422RecoveryRuntimeRead || control.failure.Cause != "runtime_stale") {
		t.Fatal("runtime binding refusal not disambiguated", report, control.failure)
	}
	fmt.Printf("bridge_observation mode=%s http=%d ordinal=%d status=%s controls=%d store=%d reported=%t accepted=%t first=%v\n", mode, response.StatusCode, report.RequestOrdinal, report.Status, report.ControlFileReads, report.StoreReadAttempts, control.recovered.reported, accepted, control.failure)
	fmt.Println("callback_joined")
	var signal [1]byte
	if _, err := io.ReadFull(stdin, signal[:]); err != nil || signal[0] != 'f' {
		t.Fatal(err)
	}
	fmt.Println("bridge_checked")
	if _, err := io.ReadFull(stdin, signal[:]); err != nil || signal[0] != 'c' {
		t.Fatal(err)
	}
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Println("closed")
}
