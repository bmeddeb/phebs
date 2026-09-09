//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Default: startup, one authenticated health request and a joined stop only.
// The additional COLD selector opts into actual A convergence and phase 2->3;
// Neither mode signs evidence or admits a host/profile. The separate VOLUME
// selector places writable preparation/execution custody on a fresh owned APFS
// image; it performs no ballast mutation or pressure phase.
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
	// Selector dependencies refuse before any host/tool/custody allocation.
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
	t.Cleanup(func() {
		if volume != nil {
			if !completed || t.Failed() || !volume.removed {
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
		t.Logf("owned mounted preparation/execution workspace: %s; no ballast or full launcher admission", parent)
	}
	var author *ExecutionAuthorCustody
	var epochs *ExecutionEpochConfigCustody
	var flow *ExecutionEpochOne
	var run *ExecutionEpochOneRun
	canRelease := func() bool {
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
	for _, role := range []string{"t422-author", "phebs", "zoekt-git-index"} {
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
		defer func() { _ = epochs.Close() }()
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
	if volume != nil && volume.bindRehearsal(ctx, flow) != nil {
		t.Fatal("exact flow could not bind its mounted custody")
	}
	result, err := flow.AuthorA(ctx)
	if err != nil || !result.Completed || !result.RootJoined || !result.SessionEmpty || result.Revision != "a" {
		t.Fatalf("actual shared author A: %+v; %v", result, err)
	}
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
			}
			if t.Failed() && result.RootJoined && run.output != nil {
				// Native Wait has also joined the combined-output copier. This
				// bounded private diagnostic is not returned public evidence.
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
					file, err := os.OpenFile(filepath.Join(parent, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
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
		next, err := prior.CheckpointRestart(ctx)
		if next != nil {
			run = next // Even a refused bootstrap remains this cleanup's owner.
			prefix, priorErr := prior.Wait(context.Background())
			t.Logf("owned terminal predecessor: %+v; %v; metric prefixes deliberately incomplete", prefix, priorErr)
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
	stopCtx, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	stopped, err := run.Stop(stopCtx)
	if err != nil || !stopped.RootStarted || !stopped.RootJoined || !stopped.SessionEmpty {
		t.Fatalf("actual epoch-one owner-drained stop: %+v; %v", stopped, err)
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
		if err := volume.finishRehearsal(stopCtx, run); err != nil {
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
