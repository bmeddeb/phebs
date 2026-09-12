//go:build darwin || linux

package t421

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// This exercises the public operation, real inherited PC/DA/SA and the actual
// bounded HTTP/projector/receipt path. Prior phase acceptance, HTTP F/bytes and
// process census rows are SUPPLIED MODELS. The helper owns no engine, index,
// measured workspace or admitted configuration; this is not full native proof.
func TestEpochQueryRestoredModeledOperation(t *testing.T) {
	plan := accountingTestPlan(t)
	inputs, err := epochCatalogInputs(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	var catalog servicecatalog.Catalog
	if len(inputs) != 3 || json.Unmarshal(inputs[2].raw, &catalog) != nil {
		t.Fatal("actual generated a-return catalog fixture")
	}
	for _, mode := range []string{"success", "changed_F2", "missing_finish_sample", "final_fence_unavailable"} {
		t.Run(mode, func(t *testing.T) {
			// Catalog construction precedes this bounded helper lifetime. No sleep
			// or timer elision changes the public operation's original deadline.
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			run, join := epochQueryOperationTransport(t, ctx)
			joined := false
			t.Cleanup(func() {
				if !joined {
					join(false)
				}
			})
			_, value := epochTestFinal(t)
			projection, err := expectedStateProjectionForPhase(plan, "product_queries")
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(projection)
			if err != nil || json.Unmarshal(raw, &value.Projection) != nil {
				t.Fatal("projection", err)
			}
			value.Projection.Schema = "t421-final-state-projection-source-free-v1"
			value.QueryAuthority = &epochQueryAuthority{
				CatalogSourceGenerationSHA256:     testDigest("operation-catalog-source"),
				ResolverNamespaceGenerationSHA256: testDigest("operation-namespace-generation"),
				ResolverNamespaceRootSHA256:       testDigest("operation-namespace-root"),
			}
			bound, err := newEpochQueryProjectionContext(ctx, "github.com/t421/query", value, catalog)
			if err != nil {
				t.Fatal("catalog must match unchanged frozen projection", err)
			}
			queries, queryCount := epochProductQueryResponder(t, bound)
			var requests, samples, finals atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer private-key" ||
					r.Header.Get(dispatchadmission.ProductionRequestHeader) == "" ||
					r.Header.Get(dispatchadmission.ProductionRequestHeader) != run.control.RequestToken() {
					t.Error("operation lost real PC request fence/token")
					w.WriteHeader(http.StatusConflict)
					return
				}
				if r.URL.Path == "/api/t422/lifecycle/sample-workspace" {
					point := "start"
					if samples.Add(1) == 2 {
						point = "finish"
					}
					if r.Method != http.MethodPost || r.Header.Get("X-Phebs-T422-Workspace-Point") != point || r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "" {
						t.Error("workspace sample acquired an inspection ordinal")
					}
					if point == "finish" && mode == "missing_finish_sample" {
						w.WriteHeader(http.StatusConflict)
						return
					}
					_, _ = io.WriteString(w, `{"logical_bytes":10,"allocated_bytes":20}`)
					if point == "finish" && mode == "final_fence_unavailable" {
						// Lose only the final PC operation, after complete HTTP bytes.
						// This is a real unavailable fence, not a forged ACK.
						_ = run.control.Close()
					}
					return
				}
				w.Header().Set("Trailer", epochReadTrailer)
				switch r.URL.Path {
				case "/api/extraction-progress":
					n, d := plan.Profile.Physical.CombinedModeledPartitions, len(plan.Profile.Pipeline.ExtractionDomains)
					_, _ = fmt.Fprintf(w, `{"$schema":%q,"state":"current","total_partitions":%d,"materialized":%d,"pending":0,"running":0,"succeeded":%d,"failed":0,"domains":%d,"current_domains":%d}`+"\n",
						"http://"+r.Host+"/schemas/ExtractionProgress.json", n, n, n, d, d)
					pressureInspectionTrailer(t, w, r, epochInspectionReport{ControlFileReads: 2 + uint64(d), StoreReadAttempts: 4})
				case "/api/t421/tail-readiness":
					tail := epochTailReadiness{Schema: "t421-tail-readiness-source-free-v1", Status: "ready",
						SelectedRuntimeSHA256:        testDigest("operation-runtime"),
						RelationshipGenerationSHA256: value.Authority.RelationshipGenerationSHA256,
						RelationshipRootSHA256:       value.Authority.RelationshipRootSHA256,
						CallerGenerationSHA256:       value.Authority.CallerGenerationSHA256,
						CallerRootSHA256:             value.Authority.CallerRootSHA256}
					_, _ = w.Write(epochTestJSON(t, tail, false))
					pressureInspectionTrailer(t, w, r, epochInspectionReport{ControlFileReads: 4, StoreReadAttempts: 4})
				case "/api/t421/final-authority":
					if r.Header.Get("X-Phebs-T422-Query-Evidence") != "bound-v1" {
						t.Error("F omitted actual query-proof request")
					}
					response := value
					if finals.Add(1) == 2 && mode == "changed_F2" {
						proof := *value.QueryAuthority
						proof.ResolverNamespaceRootSHA256 = testDigest("changed-F2")
						response.QueryAuthority = &proof
					}
					_, _ = w.Write(epochTestJSON(t, response, true))
					pressureInspectionTrailer(t, w, r, epochInspectionReport{ControlFileReads: 100, StoreReadAttempts: 10, MemberVisits: 1000})
				default:
					queries.ServeHTTP(w, r)
				}
			}))
			t.Cleanup(server.Close)
			reader := &executionEpochInspection{run: run, plan: plan, next: 7, maximumReports: 8691, finalUsed: true, restoredStep: 3}
			reader.projection, err = expectedStateProjectionForPhase(plan, "lifecycle_collection")
			if err != nil {
				t.Fatal(err)
			}
			rows, _, err := correctedInspectionInventory(plan.Profile)
			if err != nil {
				t.Fatal(err)
			}
			reader.bounds = rows[12]
			raw, err = json.Marshal(value.Authority)
			if err != nil || json.Unmarshal(raw, &reader.collectionAuthority.AuthorityState) != nil {
				t.Fatal("supplied previous F", err)
			}
			reader.collectionAuthority.Phase, reader.collectionAuthority.Outcome = "lifecycle_collection", "passed"
			reader.collectionAuthority.PhysicalRevision, reader.collectionAuthority.LogicalRevision = "a-return", "a-return"
			reader.collectionAuthority.ExtractionRoots = cloneArchiveAuthority(AuthorityPhaseResult{ExtractionRoots: value.ExtractionRoots}).ExtractionRoots
			reader.restoredSamples.ArchiveComplete, reader.restoredSamples.CollectionComplete = true, true
			for i, name := range []string{"archive_restore", "lifecycle_collection"} {
				count := uint64(i + 1)
				reader.restoredSamples.Phases[i].Attempts, reader.restoredSamples.Phases[i].Completed = count, count
				reader.restoredSamples.Phases[i].Maximum = custodybytes.Sample{LogicalBytes: 10, AllocatedBytes: 20}
				reader.evidence.rows = append(reader.evidence.rows, ExecutionPhaseInspection{
					ServerEpoch: 5, Phase: name, SelectorAccepted: true, Final: &ExecutionInspectionFinal{},
				})
			}
			run.epoch = ExecutionEpochConfig{Epoch: 5, Listen: strings.TrimPrefix(server.URL, "http://"),
				APIKey: "private-key", Repository: bound.repository, CatalogSHA256: projection.CatalogSource.SHA256}
			run.inspection, run.flow.plan = reader, plan
			author := &ExecutionAuthorCustody{borrowedBy: run}
			run.flow.epochs = &ExecutionEpochConfigCustody{author: author, active: true, released: 5, queryCatalog: &catalog}
			run.flow.epochs.epochs[4] = run.epoch
			run.healthy, run.warm, run.archiveExecutionUsed, run.collectionExecutionUsed = true, true, true, true
			run.healthDone = make(chan struct{})
			close(run.healthDone)
			run.archiveInput, run.archivePrior = &epochArchiveInput{}, &AuthorityPhaseResult{}
			run.cancelRun = cancel
			run.lifetimeDeadline, _ = ctx.Deadline()
			run.setPhaseDeadlineLocked(run.lifetimeDeadline)
			t.Cleanup(func() {
				run.mu.Lock()
				if run.phaseTimer.Stop() {
					close(run.phaseDone)
				}
				run.mu.Unlock()
				<-run.phaseDone
			})
			beforeWire := run.control.ReservedWireBytes()
			if beforeWire != 4*2*dispatchadmission.FrameBytes {
				t.Fatal("fixture did not perform actual initial drain and phase13 handoff")
			}
			err = run.QueryRestored(ctx)
			wantSuccess := mode == "success"
			if (err == nil) != wantSuccess {
				t.Fatalf("public operation result: %v; read failure=%+v", err, reader.readFailure)
			}
			select {
			case <-run.restoredExecutionDone:
			default:
				t.Fatal("public operation returned before its cancellation/join boundary")
			}
			if run.phaseDeadline != run.lifetimeDeadline || queryCount.Load() != 38 || finals.Load() != 2 ||
				len(reader.productQueries) != 22 || reader.next != 49 {
				t.Fatal("deadline renewed or actual query/F prefix differs")
			}
			store, err := run.flow.store.Snapshot()
			if err != nil && mode != "final_fence_unavailable" || store.Store.Phase != 14 || run.processObservation.phase != 14 {
				t.Fatal("public operation did not advance real SA/process boundary", err)
			}
			parentCount, countErr := run.flow.parent.Count()
			if countErr != nil && mode != "final_fence_unavailable" || parentCount.Checkpoint != 13 || parentCount.Active != 1 {
				t.Fatal("real parent checkpoint/carry differs", parentCount, countErr)
			}
			wantSamples, completedSamples := int32(2), uint64(2)
			switch mode {
			case "changed_F2":
				wantSamples, completedSamples = 1, 1
			case "missing_finish_sample":
				completedSamples = 1
			}
			if samples.Load() != wantSamples || reader.restoredSamples.Phases[2].Completed != completedSamples ||
				reader.restoredSamples.Phases[2].Maximum != (custodybytes.Sample{LogicalBytes: 10, AllocatedBytes: 20}) {
				t.Fatal("completed supplied-byte prefix differs")
			}
			last := reader.evidence.rows[len(reader.evidence.rows)-1]
			if wantSuccess {
				if !last.SelectorAccepted || !reader.restoredSamples.ProductComplete || reader.productQueryEvidence == nil ||
					run.control.RequestToken() != "" || run.control.ReservedWireBytes()-beforeWire != 5*2*dispatchadmission.FrameBytes {
					t.Fatal("public success lacks actual final fence, selector or capture")
				}
				authorityHash, hashErr := authorityResultSHA256([]AuthorityPhaseResult{reader.productAuthority}, "product_queries")
				if hashErr != nil {
					t.Fatal(hashErr)
				}
				if err := validateQueryEvidence(*reader.productQueryEvidence, "passed", authorityHash,
					ReceiptMetrics{ControlReads: CountMetric(last.Reads.ControlFileReads + last.Reads.StoreReadAttempts),
						MemberReads: CountMetric(last.Reads.MemberVisits)}, plan); err != nil {
					t.Fatal("actual captured prefix failed unchanged receipt validator", err)
				}
			} else if reader.productQueryEvidence != nil || reader.restoredSamples.ProductComplete || last.SelectorAccepted {
				t.Fatal("failed public operation manufactured receipt acceptance")
			}
			calls, deadline := requests.Load(), run.phaseDeadline
			if run.QueryRestored(ctx) == nil || requests.Load() != calls || run.phaseDeadline != deadline {
				t.Fatal("one-shot public operation repeated work or renewed deadline")
			}
			join(mode == "final_fence_unavailable")
			joined = true
		})
	}
}

// Reuse the existing inherited helper body; it binds genuine semantic owners
// and an idle SDK owner but never executes the placeholder tool recipes.
func epochQueryOperationTransport(t *testing.T, ctx context.Context) (*ExecutionEpochOneRun, func(bool)) {
	t.Helper()
	site := dispatchadmission.Site{ID: executionSiteServe, Role: executionRolePhebs, Persistent: true}
	root := dispatchadmission.Producer{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{site}}
	child := dispatchadmission.Producer{ID: 6, Binding: [32]byte{6}, Sites: dispatchadmission.ProductionSites()}
	limits := dispatchadmission.Limits{Producers: 2, Sites: 33, Roles: 5, Phases: 3,
		ActivePerProducer: 1, Attempts: 1, WireBytes: 8192, AckTimeout: time.Second}
	var phases []dispatchadmission.Phase
	var storePhases []storeaccounting.Phase
	for _, phase := range []uint32{12, 13, 14} {
		roles := []dispatchadmission.RoleBudget{{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal},
			{Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility}, {Role: executionRolePhebs}}
		if phase == 12 {
			roles[len(roles)-1].Attempts = 1
		}
		phases = append(phases, dispatchadmission.Phase{ID: phase, Roles: roles})
		storePhases = append(storePhases, storeaccounting.Phase{ID: phase})
	}
	da, err := dispatchadmission.New(ctx, dispatchadmission.Config{Limits: limits, Producers: []dispatchadmission.Producer{root, child}, Phases: phases})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := da.NewLocalProducer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close(context.Background()) })
	sa, err := storeaccounting.New(ctx, storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: 6, Calls: 40, Transactions: 2}}, Phases: storePhases})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(ctx, sa, storeaccounting.WireConfig{
		Producers: []storeaccounting.WireProducer{{ID: 6, Binding: child.Binding, Phases: 14336}}, AckTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	childSA, saConfig, err := transport.Open(6)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = childSA.Close() })
	daParent, daChild, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daParent.Close(); _ = daChild.Close() })
	pcParent, pcChild, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pcParent.Close(); _ = pcChild.Close() })
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutionEpochLogicalTransportHelper$")
	command.Env = []string{"PHEBS_LOGICAL_TRANSPORT_HELPER=1", "GORACE=atexit_sleep_ms=0",
		dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionStoreSelector}
	command.ExtraFiles, command.Stderr, command.WaitDelay = []*os.File{daChild, pcChild, childSA}, os.Stderr, time.Second
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	handle, err := parent.StartInPhase(ctx, 12, site, command)
	if err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = command.Process.Kill()
			_ = handle.Wait()
			waited = true
		}
	})
	if daChild.Close() != nil || pcChild.Close() != nil || childSA.Close() != nil {
		t.Fatal("inherited endpoint close")
	}
	base := []string{"HOME=/tmp", "TMPDIR=/tmp", "TMP=/tmp", "TEMP=/tmp", "PATH=/tmp", "LANG=C", "LC_ALL=C", "TZ=UTC"}
	git := append(slices.Clone(base), "GIT_EXEC_PATH=/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ATTR_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0", "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR=/dev/null", "GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_KEY_1=core.untrackedCache", "GIT_CONFIG_KEY_2=core.hooksPath",
		"GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_VALUE_2=/dev/null")
	pc := dispatchadmission.PhaseControlConfig{OwnerControl: true, Phases: []uint32{12, 13, 14}, InitialPhase: 12,
		MaximumPhases: 3, MaximumWireBytes: 15 * 2 * dispatchadmission.FrameBytes, Timeout: time.Second}
	record := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramPhebs, SemanticMode: dispatchadmission.ProductionSemanticV3,
		InputSHA256: [32]byte{12}, Producer: child, Phase: 12, Limits: limits, Control: pc, Store: &saConfig,
		Tools: []dispatchadmission.ProductionToolBinding{{Role: "git", Path: "/bin/sh", Environment: git},
			{Role: "surreal", Path: "/bin/sh", Environment: base}, {Role: "zoekt-git-index", Path: "/bin/sh", Environment: git}}}
	if dispatchadmission.SendProductionBootstrap(ctx, daParent, pcParent, record) != nil {
		t.Fatal("inherited bootstrap")
	}
	control, err := dispatchadmission.NewPhaseControl(ctx, pcParent, child.Binding, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	served := make(chan error, 1)
	go func() { served <- da.Serve(ctx, 6, command.Process.Pid, daParent) }()
	t.Cleanup(func() { _ = daParent.Close(); <-served })
	epochHandoffRead(t, output, 'R')
	meter, err := newEpochProcessObservation(ctx, 41, 12, "phebs", map[string]string{"phebs": "phebs", "git": "git"},
		func() { t.Error("modeled process census failed") },
		func(context.Context, int) ([]t4013.NativeProcessRecord, error) { return epochProcessFixtureRows(), nil })
	if err != nil {
		t.Fatal(err)
	}
	pauseEpochProcessTicker(meter)
	t.Cleanup(func() { _, _ = meter.close() })
	run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{controller: da, parent: parent, store: transport, workspace: &productionRoot{}},
		control: control, processObservation: meter, stop: make(chan struct{}), done: make(chan struct{})}
	if control.DrainOwners(ctx) != nil || run.advanceReturnPhase(ctx, 13) != nil {
		t.Fatal("actual initial phase13 control handoff")
	}
	// The returned join owns the sole helper Wait. Even refused operation cases
	// close/join actual transport owners; a lost PC channel is not called clean.
	return run, func(brokenControl bool) {
		if waited {
			return
		}
		if !brokenControl {
			if control.RequestToken() != "" && control.FenceRequests(ctx) != nil {
				t.Error("cleanup request fence")
			}
			if control.Pause(ctx) != nil || parent.Pause(ctx) != nil || da.Fence() != nil || transport.Fence() != nil {
				t.Error("cleanup drained pause")
			}
		}
		meter.armStop()
		if _, err := meter.close(); err != nil {
			t.Error("modeled sampler join", err)
		}
		epochHandoffByte(t, input, 'C')
		waitErr := handle.Wait()
		waited = true
		serveErr := <-served
		// Keep one closed-channel receive available to unconditional cleanup.
		close(served)
		storeErr := transport.Wait(ctx, 6)
		controlErr := control.Close()
		if !brokenControl && (waitErr != nil || serveErr != nil || storeErr != nil || controlErr != nil) {
			t.Error("helper/DA/SA/PC clean join", waitErr, serveErr, storeErr, controlErr)
		}
		if brokenControl && waitErr == nil {
			t.Error("lost final fence concealed helper lifetime failure")
		}
		if command.ProcessState == nil || !command.ProcessState.Exited() {
			t.Error("helper sole Wait not complete")
		}
		if err := parent.Close(ctx); err != nil && !brokenControl {
			t.Error("parent receiver join", err)
		}
		if err := transport.Close(); err != nil && !brokenControl {
			t.Error("store receiver joins", err)
		}
		if !brokenControl {
			daPrefix, daErr := da.Snapshot()
			saPrefix, saErr := transport.Snapshot()
			if daErr != nil || saErr != nil || !daPrefix.Complete || !saPrefix.Complete ||
				saPrefix.Opened != 1 || saPrefix.TerminalEOF != 1 {
				t.Error("joined helper did not close actual accounting prefixes", daErr, saErr)
			}
		}
	}
}
