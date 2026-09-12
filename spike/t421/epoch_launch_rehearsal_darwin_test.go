//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// Default: startup, one authenticated health request and a joined stop only.
// The additional COLD selector opts into actual A convergence and phase 2->3;
// Neither mode signs evidence or admits a host/profile. The separate VOLUME
// selector places writable preparation/execution custody on a fresh owned APFS
// image. PRESSURE_SEQUENCE continues actual ballast/phases9–11 through archive,
// restored lifecycle/queries and admitted phase15 cleanup. Existing phase,
// author-lifetime and caller deadlines remain unchanged; no receipt is issued.
func TestExecutionEpochOneOptionalRealStartRehearsal(t *testing.T) {
	if os.Getenv("PHEBS_T422_EPOCH_ONE_REHEARSAL") != "1" {
		t.Skip("requires explicit serial protected epoch-one startup rehearsal")
	}
	cold := os.Getenv("PHEBS_T422_EPOCH_ONE_COLD_REHEARSAL") == "1"
	warm := os.Getenv("PHEBS_T422_EPOCH_ONE_WARM_REHEARSAL") == "1"
	physical := os.Getenv("PHEBS_T422_EPOCH_ONE_PHYSICAL_REHEARSAL") == "1"
	logical := os.Getenv("PHEBS_T422_LOGICAL_REHEARSAL") == "1"
	returnA := os.Getenv("PHEBS_T422_RETURN_A_REHEARSAL") == "1"
	stale := os.Getenv("PHEBS_T422_STALE_LEASE_REHEARSAL") == "1"
	checkpoint := os.Getenv("PHEBS_T422_CHECKPOINT_RESTART_REHEARSAL") == "1"
	onVolume := os.Getenv("PHEBS_T422_PRESSURE_VOLUME_REHEARSAL") == "1"
	pressure := os.Getenv("PHEBS_T422_PRESSURE_SEQUENCE_REHEARSAL") == "1"
	// Selector dependencies refuse before any host/tool/custody allocation.
	if pressure && (!checkpoint || !onVolume) {
		t.Fatal("pressure sequence requires checkpoint and mounted-volume selectors")
	}
	if checkpoint && !stale {
		t.Fatal("checkpoint selector requires explicit stale-lease selector")
	}
	if stale && !returnA {
		t.Fatal("stale-lease selector requires explicit return-A selector")
	}
	if returnA && !logical {
		t.Fatal("return-A selector requires explicit logical selector")
	}
	if logical && !physical {
		t.Fatal("logical selector requires explicit physical selector")
	}
	if physical && !warm {
		t.Fatal("physical selector requires explicit warm selector")
	}
	if warm && !cold {
		t.Fatal("warm selector requires explicit cold selector")
	}
	requireExternalToolFrozenHost(t)
	repository := os.Getenv("PHEBS_T422_PRODUCTION_REPOSITORY")
	commit := os.Getenv("PHEBS_T422_PRODUCTION_COMMIT")
	goRoot := os.Getenv("PHEBS_T422_PRODUCTION_GOROOT")
	moduleCache := os.Getenv("PHEBS_T422_PRODUCTION_MODULE_CACHE")
	gitBinary := os.Getenv("PHEBS_T422_PRODUCTION_GIT")
	if !validCommit(commit) || !executionGitAbsolutePath(repository) || !executionGitAbsolutePath(goRoot) ||
		!executionGitAbsolutePath(moduleCache) || !executionGitAbsolutePath(gitBinary) {
		t.Fatal("explicit exact source/native Git/SDK/offline cache selections required")
	}
	surrealBinary := toolCustodyExternalSurreal(t)
	directory, err := os.MkdirTemp("", "t422-epoch-one-rehearsal-")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("retained new rehearsal directory %s: %v", directory, err)
	}
	identity, err := os.Lstat(parent)
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	hostParent := parent
	var volume *executionPressureVolume
	var systemTools [2]*ExecutionSystemToolCustody
	var canRelease func() bool
	t.Cleanup(func() {
		if volume != nil {
			if !completed || t.Failed() || !volume.removed {
				preparation, phases := volume.byteSnapshot()
				t.Logf("retained actual workspace sample prefix: preparation=%+v phases=%+v; incomplete boundary coverage", preparation, phases)
				t.Logf("retained volume/image custody; no automatic retry or mounted cleanup: %s", hostParent)
				return
			}
			current, err := os.Lstat(hostParent)
			if err != nil || !os.SameFile(identity, current) || volume.Close() != nil {
				t.Error("volume outer cleanup identity/closure refused")
				return
			}
			if os.Remove(filepath.Join(hostParent, ".t4013-operation.lock")) != nil || os.Remove(hostParent) != nil {
				t.Error("volume outer cleanup retained nonempty or changed controls")
			}
			return
		}
		if !completed || t.Failed() {
			t.Logf("retained exact epoch-one custody; no automatic retry: %s", parent)
			return
		}
		current, err := os.Lstat(parent)
		if err != nil || !os.SameFile(identity, current) {
			t.Error("epoch-one cleanup parent changed; retaining custody")
			return
		}
		if err := os.RemoveAll(parent); err != nil {
			t.Error(err)
		}
	})
	// All flow/input/volume defers below run before this outer release. Its
	// errors precede the final directory cleanup above, preserving failed
	// custody. Future actual signing must join first; no signer executes here.
	t.Cleanup(func() {
		if canRelease != nil && !canRelease() {
			t.Error("fixed-host image handles retained beside unjoined rehearsal custody")
			return
		}
		for _, tool := range systemTools {
			if err := tool.Close(); err != nil {
				t.Error("outer fixed-host image close", err)
			}
		}
	})
	allowance := time.Hour
	if cold {
		allowance = 5 * time.Hour // Includes protected builds; phase bounds remain separate.
	}
	if physical {
		allowance = 9 * time.Hour // Protected builds plus unchanged cold/warm/B phase deadlines.
	}
	if logical {
		allowance += 4 * time.Hour // Separate unchanged phase-five deadline includes handoff.
	}
	if returnA {
		allowance += 4 * time.Hour // Phase six includes predecessor join and actual author nine.
	}
	if stale {
		allowance += 4 * time.Hour // Same server; phase seven starts before its handoff.
	}
	if checkpoint {
		allowance += 4 * time.Hour // Original phase-eight deadline spans owned death and epoch four.
	}
	if pressure {
		allowance += time.Hour
	} // Three unchanged twenty-minute phases.
	ctx, cancel := context.WithTimeout(t.Context(), allowance)
	defer cancel()
	if onVolume {
		volume, err = prepareExecutionPressureVolume(ctx, hostParent)
		if volume != nil {
			defer func() { _ = volume.Close() }()
		}
		if err != nil {
			t.Fatal("owned pressure-volume preparation", err)
		}
		ctx, parent, err = volume.borrowWorkspace(ctx)
		if err != nil {
			t.Fatal("owned mounted workspace borrow", err)
		}
		t.Logf("owned mounted preparation/execution workspace: %s; pressure_sequence=%t; no full launcher admission", parent, pressure)
	}
	var author *ExecutionAuthorCustody
	var epochs *ExecutionEpochConfigCustody
	var flow *ExecutionEpochOne
	var run *ExecutionEpochOneRun
	canRelease = func() bool {
		if flow != nil {
			flow.mu.Lock()
			defer flow.mu.Unlock()
			if !flow.profileRuntime.releasable() {
				return false
			}
		}
		if author != nil {
			author.mu.Lock()
			defer author.mu.Unlock()
			if author.active || author.borrowedBy != nil {
				return false
			}
		}
		if epochs != nil {
			epochs.mu.Lock()
			defer epochs.mu.Unlock()
			return !epochs.active
		}
		return true
	}
	for i, role := range []string{"sh", "ssh-keygen"} {
		systemTools[i], err = HoldExecutionSystemTool(ctx, role)
		if err != nil {
			t.Fatal("actual outer fixed-host image hold", role, err)
		}
	}
	git, err := ProtectExecutionGit(ctx, parent, gitBinary)
	if git != nil {
		defer func() {
			if canRelease() {
				_ = git.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	inputs, err := ProtectExecutionGoBuildInputs(ctx, parent, ExecutionGoBuildRequest{Git: git, RepositoryRoot: repository,
		PlanSourceCommit: commit, IntegratedMainCommit: commit, SourceCommit: commit, GoRoot: goRoot, ModuleCache: moduleCache})
	if inputs != nil {
		defer func() {
			if canRelease() {
				_ = inputs.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual source/SDK/module custody: %s; %+v", time.Since(started), inputs.Inventory())
	workspace := filepath.Join(parent, "supplied-builds")
	for _, path := range []string{workspace, filepath.Join(workspace, "home"), filepath.Join(workspace, "tmp"), filepath.Join(workspace, "cache")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var tools []*ExecutionToolCustody
	defer func() {
		if canRelease() {
			for _, tool := range tools {
				_ = tool.Close()
			}
		}
	}()
	for _, role := range []string{"t422-author", "phebs", "zoekt-git-index", "buf", "phebs-focused-index"} {
		selected := productionRehearsalBuildSchema(t, ctx, inputs, workspace, role, PlanV3Schema)
		started = time.Now()
		tool, err := inputs.ProtectReferenceToolV3(ctx, parent, role, selected)
		if tool != nil {
			tools = append(tools, tool)
		}
		if err != nil {
			t.Fatalf("actual protected %s reference admission: %v", role, err)
		}
		t.Logf("actual protected %s reference admission: %s", role, time.Since(started))
	}
	surreal, err := ProtectExecutionExternalTool(ctx, parent, "surreal", surrealBinary)
	if surreal != nil {
		tools = append(tools, surreal)
	}
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlanV3WithLogicalStoreWork(commit)
	if err != nil || ctx.Err() != nil {
		t.Fatal("private unsealed plan construction", err)
	}
	raw, err := MarshalCanonical(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(parent, "unsealed-plan-input.json")
	if err := os.WriteFile(planPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	planInput, err := ProtectExecutionInputs(ctx, parent, []ExecutionInputCopy{{Name: "plan", Path: planPath, SHA256: SHA256(raw)}})
	if planInput != nil {
		defer func() {
			if canRelease() {
				_ = planInput.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	author, err = PrepareExecutionAuthor(ctx, parent, ExecutionAuthorRequest{Git: git, Builds: inputs, Author: tools[0], Plan: planInput})
	if author != nil {
		defer func() {
			if canRelease() {
				_ = author.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	epochs, err = PrepareExecutionEpochConfigs(ctx, author)
	if epochs != nil {
		defer func() {
			if canRelease() {
				_ = epochs.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	flow, err = PrepareExecutionEpochOne(ctx, epochs, tools[1], tools[2], surreal)
	if flow != nil {
		defer func() { _ = flow.Close() }()
	}
	if err != nil {
		t.Fatal(err)
	}
	// Reuse the two required independent admissions above, not another build
	// or a fake complete profile. Every nonnil failed copy is already retained.
	if err := flow.bindProfileTools(ctx, tools[3], tools[4]); err != nil {
		t.Fatal("protected Buf/focused holder binding", err)
	}
	if err := flow.prepareProfileSystemTools(ctx, systemTools[0], systemTools[1]); err != nil {
		t.Fatal("actual outer fixed-host image observation", err)
	}
	if volume != nil {
		if pressure {
			if _, err := prepareExecutionPressureBallast(ctx, volume); err != nil {
				t.Fatal("preparation-owned zero ballast inode", err)
			}
		}
		if _, err := volume.samplePreparation(ctx); err != nil {
			t.Fatal("actual whole-workspace preparation sample", err)
		}
		if volume.bindRehearsal(ctx, flow) != nil {
			t.Fatal("exact flow could not bind its mounted custody")
		}
		if pressure {
			if err := volume.observeProfileHost(ctx, flow); err != nil {
				t.Fatal("actual pre-author host/root observation", err)
			}
			// Facts only: a below-floor available capacity remains observed.
			// Existing freeze admission must validate it separately before use.
		}
		// AuthorA has not anchored its cold deadline yet. This is preparation
		// evidence only, never an invented phase-one or timed cold-start sample.
		preparation, _ := volume.byteSnapshot()
		t.Logf("actual whole-workspace preparation maximum: %+v; no phase-one/cold-start coverage", preparation)
	}
	if err := flow.prepareProfileEnvironment(ctx); err != nil {
		t.Fatal("actual pre-author environment observation", err)
	}
	// Preparation-only: the original rehearsal deadline is not a separately
	// bounded launcher admission stage, and these facts issue no profile.
	if err := flow.prepareProfileRuntime(ctx); err != nil {
		if observed := flow.profileRuntime; observed != nil {
			t.Logf("runtime preparation prefix: pid=%d started=%t joined=%t session_empty=%t decoded=%t complete=%t deadline=%s",
				observed.PID, observed.RootStarted, observed.RootJoined, observed.SessionEmpty, observed.Observed, observed.Complete, observed.Deadline)
		}
		t.Fatal("actual protected runtime facts", err)
	}
	result, err := flow.AuthorA(ctx)
	if err != nil || !result.Completed || !result.RootJoined || !result.SessionEmpty || result.Revision != "a" {
		t.Fatalf("actual shared author A: %+v; %v", result, err)
	}
	// Bound AuthorA now owns both actual timed walks; do not repeat its
	// completed post-author boundary or relabel the preparation sample.
	started = time.Now()
	if physical {
		run, err = flow.StartPhysicalB(ctx)
	} else if warm {
		run, err = flow.StartColdWarm(ctx)
	} else if cold {
		run, err = flow.StartCold(ctx)
	} else {
		run, err = flow.Start(ctx)
	}
	if run != nil {
		defer func() {
			stopCtx, stop := context.WithTimeout(context.Background(), time.Minute)
			defer stop()
			result, err := run.Stop(stopCtx)
			if err != nil || !result.RootJoined || !result.SessionEmpty {
				t.Errorf("retained epoch-one stopped prefix: %+v; %v", result, err)
				run.mu.Lock()
				t.Logf("private native stop diagnostic: %v", run.nativeStopErr)
				t.Logf("private stop observations (Before precedes teardown; not global first-cause ordering): %+v", run.stopDiagnostic)
				t.Logf("private admission check: before_teardown=%+v after_join=%+v", run.stopDiagnostic.Before.Admission, run.stopDiagnostic.AdmissionAfterJoin)
				run.mu.Unlock()
				if run.processObservation != nil {
					t.Logf("private first server-process refusal: %q", run.processObservation.gauge.privateRefusal())
				}
			}
			if t.Failed() && result.RootJoined && run.output != nil {
				// Native Wait has also joined the combined-output copier. This
				// bounded private diagnostic is not returned public evidence.
				diagnosticParent := parent
				if volume != nil {
					volume.mu.Lock()
					if volume.teardownDetached {
						// A late cleanup refusal must not recreate unmounted custody.
						diagnosticParent = hostParent
					}
					volume.mu.Unlock()
				}
				diagnostics := map[string][]byte{"server.log": run.output.buffer.Bytes()}
				if reader := run.inspection; reader != nil {
					reader.mu.Lock()
					t.Logf("private first inspection refusal: %+v", reader.readFailure)
					t.Logf("private inspection prefix: X=%d T=%d F_used=%t accepted_reports=%d reads=%+v failed_HTTP=%d failed_body_bytes=%d",
						reader.progressCalls, reader.tailCalls, reader.finalUsed, reader.reports, reader.totals, reader.failureStatus, len(reader.failureBody))
					if reader.failureBody != nil {
						diagnostics["inspection-response.body"] = reader.failureBody
					}
					reader.mu.Unlock()
				}
				for name, raw := range diagnostics {
					file, err := os.OpenFile(filepath.Join(diagnosticParent, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
					if err != nil {
						t.Error("private joined-server diagnostic could not be retained", err)
					} else {
						_, writeErr := file.Write(raw)
						if closeErr := file.Close(); writeErr != nil || closeErr != nil {
							t.Error("private joined-server diagnostic was not completely retained")
						}
					}
				}
			}
		}()
	}
	if err != nil {
		t.Fatal("actual epoch-one Start", err)
	}
	if epochs.Close() == nil || author.Close() == nil {
		t.Fatal("active native server lost protected input/source custody")
	}
	if volume != nil && volume.Close() == nil {
		t.Fatal("active native server lost its volume borrow")
	}
	if err := run.Health(ctx); err != nil {
		t.Fatal("actual authenticated epoch-one health", err)
	}
	if cold {
		if err := run.ColdToWarm(ctx); err != nil {
			t.Fatal("actual epoch-one cold convergence/handoff", err)
		}
		t.Log("actual cold X/T/F and phase-three handoff returned; warm remains unobserved")
	}
	if warm {
		if err := run.ObserveWarm(ctx); err != nil {
			t.Fatal("actual epoch-one ordinary-owner warm observation", err)
		}
		t.Log("actual warm single X/T/F matched cold authority; request/report tail joined; no full warm work-metrics claim")
	}
	if physical {
		if err := run.PhysicalB(ctx); err != nil {
			t.Fatal("actual epoch-one physical B/current-prior observation", err)
		}
		t.Log("actual physical B X/T/F and current/prior retention read returned; no full phase metrics or later-epoch claim")
	}
	if logical {
		prior := run
		next, err := prior.StartLogicalB(ctx)
		if next != nil {
			run = next // Existing cleanup follows the actual successor, including failed bootstrap.
			// StartLogicalB already joined this run before starting next. Keep
			// its accepted per-epoch counters without retaining its output in
			// the successor or mislabeling this subset as full work metrics.
			prefix, priorErr := prior.Wait(context.Background())
			t.Logf("joined first-epoch retained-parent prefix before logical successor: %+v; %v; no full work-metrics claim", prefix, priorErr)
			assertRehearsalInspection(t, prior, prefix, []string{"cold", "warm_noop", "physical_delta_b"})
			if priorErr != nil {
				t.Fatal("successor lost joined first-epoch prefix", priorErr)
			}
		}
		if err != nil {
			t.Fatal("retained physical parent/logical successor", err)
		}
		if err := run.Health(ctx); err != nil {
			t.Fatal("actual logical health", err)
		}
		if err := run.LogicalB(ctx); err != nil {
			t.Fatal("actual logical hit/X/T/recovered/F", err)
		}
		t.Log("actual logical hit/recovery and physical-authority continuity returned; no full work metrics or freeze claim")
	}
	if returnA {
		prior := run
		startReturn := prior.StartReturnA
		if stale {
			startReturn = prior.StartReturnAStale
		}
		if checkpoint {
			startReturn = prior.StartReturnACheckpoint
		}
		next, err := startReturn(ctx)
		if next != nil {
			run = next // Cleanup always follows the actual successor first.
			prefix, priorErr := prior.Wait(context.Background())
			t.Logf("joined logical-epoch prefix before return-A successor: %+v; %v; no full work-metrics claim", prefix, priorErr)
			assertRehearsalInspection(t, prior, prefix, []string{"logical_delta_b"})
			if priorErr != nil {
				t.Fatal("successor lost joined logical prefix", priorErr)
			}
		}
		if err != nil {
			t.Fatal("retained logical parent/return-A author and successor", err)
		}
		if err := run.Health(ctx); err != nil {
			t.Fatal("actual return-A health", err)
		}
		if err := run.ReturnA(ctx); err != nil {
			t.Fatal("actual return-A marker hit/recovered/X/T/F", err)
		}
		t.Log("actual return-A marker continuation and authority observation returned; no full metrics, phases seven/eight, or freeze claim")
	}
	if stale {
		if err := run.StaleLease(ctx); err != nil {
			t.Fatal("actual stale preparation/hit/recovered/X/T/F", err)
		}
		t.Logf("actual stale-lease native preparation: %+v; R hit=%+v recovered=%+v; cumulative epoch-three exact-read totals (phases six/seven)=%+v; no full RecoveryPreparationResult, phase metrics or freeze claim",
			run.inspection.stalePreparation, run.inspection.staleHit, run.inspection.staleRecovered, run.inspection.totals)
	}
	if checkpoint {
		prior := run
		restart := prior.CheckpointRestart
		if pressure {
			restart = prior.CheckpointRestartPressure
		}
		next, err := restart(ctx)
		if next != nil {
			run = next // Even a refused bootstrap remains this cleanup's owner.
			prefix, priorErr := prior.Wait(context.Background())
			t.Logf("owned terminal predecessor: %+v; %v; metric prefixes deliberately incomplete", prefix, priorErr)
			assertRehearsalInspection(t, prior, prefix, []string{"return_a", "stale_lease"})
			if priorErr != nil {
				t.Fatal("checkpoint successor lost terminal predecessor", priorErr)
			}
		}
		if err != nil {
			t.Fatal("actual held checkpoint/owned kill/same-phase restart", err)
		}
		if err := run.Health(ctx); err != nil {
			t.Fatal("actual epoch-four health", err)
		}
		if err := run.RecoverCheckpoint(ctx); err != nil {
			t.Fatal("actual checkpoint recovered R/X/T/F", err)
		}
		t.Log("actual checkpoint restart and unchanged full native authority returned; no complete work-metrics, phase receipt, or freeze claim")
	}
	if pressure {
		if err := run.Pressure(ctx, volume); err != nil {
			t.Fatal("actual pressure sequence and workspace samples", err)
		}
		// Backup owns the real epoch-four shutdown; do not call the ordinary
		// rehearsal Stop first or renew the phase-twelve deadline.
		backedUp, err := run.BackupAndStop(ctx)
		t.Logf("actual backup/joined epoch-four prefix: %+v; %v", backedUp, err)
		if err != nil || !backedUp.RootJoined || !backedUp.SessionEmpty {
			t.Fatal("actual pressure-to-backup handoff", err)
		}
		assertRehearsalInspection(t, run, backedUp, []string{"process_restart", "pressure_80", "pressure_90", "pressure_75"})
		restored, err := run.RestoreBackup(ctx)
		t.Logf("actual joined restore prefix: %+v; %v", restored, err)
		if err != nil {
			t.Fatal("actual archive restore in original phase-twelve window", err)
		}
		next, err := run.StartRestored(ctx)
		if next != nil {
			run = next // The deferred Stop owns even a failed successor bootstrap.
		}
		if err != nil || next == nil {
			t.Fatal("actual restored successor launch", err)
		}
		if err := run.Health(ctx); err != nil {
			t.Fatal("actual restored authenticated health", err)
		}
		if err := run.CompleteArchive(ctx); err != nil {
			t.Fatal("actual restored archive R/X/T/F and finish sample", err)
		}
		if err := run.CollectRestored(ctx); err != nil {
			t.Fatal("actual restored fresh-owner collection", err)
		}
		if err := run.QueryRestored(ctx); err != nil {
			t.Fatal("actual restored HTTP/MCP query corridor", err)
		}
		// This starts the original clipped teardown clock before fencing and
		// shutdown, then owns input release and the one nonforced detach.
		teardown, err := volume.finishRestored(ctx, run)
		t.Logf("actual phase-fifteen component evidence (no global event ordinals): %+v; %v", teardown, err)
		if err != nil || !teardown.Joined || !teardown.CleanupClosed || !teardown.CustodyAbsent ||
			!teardown.Detached || !teardown.ImageRemoved || !teardown.RootRemoved ||
			teardown.ByteObservations != 2 || !teardown.Bytes.Completed || teardown.ByteUnavailable || teardown.ByteLimitExceeded ||
			teardown.AccountingError != nil || teardown.StoreError != nil || !teardown.Accounting.Complete ||
			teardown.Store.Opened != 7 || teardown.Store.TerminalEOF != 7 || !teardown.Store.PrefixesClosed || teardown.Store.Store.Phase != 15 {
			t.Fatal("actual restored teardown or retained positive evidence incomplete", err)
		}
		// Actual successful root launches: five servers, three authors, two
		// archive commands, create/attach, then the thirteenth root at detach.
		for index, census := range []SessionCensusEvidence{teardown.InitialJoined, teardown.BeforeDetach, teardown.AfterDetach, teardown.AfterCleanup, teardown.FinalClose} {
			want := uint64(12)
			if index >= 2 {
				want++
			}
			if census.RecordedSessions != want || census.CompletedCensuses != want || census.ObservedProcesses != 0 || census.Errors != 0 {
				t.Fatalf("actual teardown session census %d: %+v", index, census)
			}
		}
		stopped, err := run.Wait(ctx) // Cached joined result; no new read or stop allowance.
		if err != nil || !stopped.RootStarted || !stopped.RootJoined || !stopped.SessionEmpty ||
			stopped.QueryResults == nil || !validExecutionProductQueries(stopped.ProductQueries) {
			t.Fatal("actual restored joined query evidence missing", err)
		}
		assertRehearsalInspection(t, run, stopped, []string{"archive_restore", "lifecycle_collection", "product_queries"})
		t.Logf("actual pressure/archive/restored/query/teardown continuation returned after %s; complete all-phase metrics, output fit, signed receipt, launcher and freeze gates remain separate", time.Since(started))
		completed = true
		return // finishRestored already released inputs and removed custody; never sample an absent root.
	}
	stopCtx, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	stopped, err := run.Stop(stopCtx)
	if err != nil || !stopped.RootStarted || !stopped.RootJoined || !stopped.SessionEmpty {
		t.Fatalf("actual epoch-one owner-drained stop: %+v; %v", stopped, err)
	}
	var accepted []string
	switch {
	case checkpoint:
		accepted = []string{"process_restart"}
	case stale:
		accepted = []string{"return_a", "stale_lease"}
	case returnA:
		accepted = []string{"return_a"}
	case logical:
		accepted = []string{"logical_delta_b"}
	case physical:
		accepted = []string{"cold", "warm_noop", "physical_delta_b"}
	case warm:
		accepted = []string{"cold", "warm_noop"}
	case cold:
		accepted = []string{"cold"}
	}
	assertRehearsalInspection(t, run, stopped, accepted)
	if volume != nil {
		if _, err := volume.sampleJoined(ctx, run); err != nil {
			t.Fatal("actual joined whole-workspace sample", err)
		}
		preparation, phases := volume.byteSnapshot()
		t.Logf("actual non-atomic whole-workspace boundary samples: preparation=%+v phases=%+v; missing phase-start/mutation/lifecycle samples remain incomplete", preparation, phases)
	}
	t.Logf("epoch-one startup/health/stop: %s; cold_handoff_selector=%t warm_observation_selector=%t physical_b_selector=%t; %+v; no full warm/receipt/freeze claim", time.Since(started), cold, warm, physical, stopped)
	if !canRelease() || flow.Close() != nil || epochs.Close() != nil || author.Close() != nil || planInput.Close() != nil {
		t.Fatal("joined epoch-one owner/input closure failed; retaining custody")
	}
	for _, tool := range tools {
		if tool.Close() != nil {
			t.Fatal("joined protected tool closure failed")
		}
	}
	if inputs.Close() != nil || git.Close() != nil {
		t.Fatal("joined protected build/Git closure failed")
	}
	if volume != nil {
		// Detach has its own existing one-minute command allowance. The server
		// stop context may have expired during the required joined byte scan.
		if err := volume.finishRehearsal(ctx, run); err != nil {
			t.Fatal("populated volume retained after joined rehearsal", err)
		}
		t.Log("bound populated volume detached without force and removed without thawing source")
		completed = true
		return // No mounted input thaw/delete or recursive cleanup.
	}
	gitCustodyTestCleanup(t, git)
	goBuildTestCleanup(t, inputs)
	for index, role := range []string{"t422-author", "phebs", "zoekt-git-index", "surreal"} {
		inputCustodyTestCleanup(t, tools[index].input, []ExecutionInputCopy{{Name: role}})
	}
	inputCustodyTestCleanup(t, planInput, []ExecutionInputCopy{{Name: "plan"}})
	inputCustodyTestCleanup(t, epochs.catalogs, []ExecutionInputCopy{{Name: "catalog-a"}, {Name: "catalog-b"}, {Name: "catalog-a-return"}})
	inputCustodyTestCleanup(t, epochs.configs, []ExecutionInputCopy{{Name: "config-1"}, {Name: "config-2"}, {Name: "config-3"}, {Name: "config-4"}, {Name: "config-5"}})
	completed = true
}

// Compare only this joined epoch's exact-read prefix and accepted selectors.
// DA/SA stay cumulative; successor phase-eight process samples already include
// their predecessor. Neither stream is added into this inspection ledger.
func assertRehearsalInspection(t *testing.T, run *ExecutionEpochOneRun, result ExecutionEpochOneResult, want []string) {
	t.Helper()
	var accepted []string
	var counts readaccounting.Counts
	var reports uint64
	next := uint64(1)
	for _, row := range result.Inspection {
		if row.ServerEpoch != run.epoch.Epoch || row.FirstOrdinal != next || row.NextOrdinal <= row.FirstOrdinal || row.AcceptedReports > row.NextOrdinal-row.FirstOrdinal {
			t.Fatal("inspection epoch/ordinal prefix changed", row)
		}
		next = row.NextOrdinal
		reports += row.AcceptedReports
		counts.ControlFileReads += row.Reads.ControlFileReads
		counts.StoreReadAttempts += row.Reads.StoreReadAttempts
		counts.MemberVisits += row.Reads.MemberVisits
		counts.StoreWriteAttempts += row.Reads.StoreWriteAttempts
		if row.SelectorAccepted {
			if row.Final == nil || row.Final.Ordinal < row.FirstOrdinal || row.Final.Ordinal >= row.NextOrdinal || row.Final.Projection.Phase != row.Phase {
				t.Fatal("accepted selector has no actual phase-bound F", row)
			}
			accepted = append(accepted, row.Phase)
		}
	}
	if !slices.Equal(accepted, want) {
		t.Fatal("accepted native selectors differ", accepted, want)
	}
	if run.inspection != nil && (run.inspection.next != next || run.inspection.reports != reports || run.inspection.totals != counts) {
		t.Fatal("phase inspection deltas do not equal epoch prefix", counts, reports, next)
	}
}
