//go:build linux && phebs_t42_native

package t421

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// Required full composition: no reduced corpus, fake issuer, skipped tool,
// substituted server, old memory ceiling or observation-only admission.
func TestLinuxEpochRequiredNativeLiveBorrow(t *testing.T) {
	if os.Getuid() == 0 || os.Geteuid() == 0 || runtime.GOARCH != "amd64" || runtime.Version() != "go1.26.5" {
		t.Fatal("required unprivileged Linux amd64 Go1.26.5; no skip")
	}
	helperRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_GO_REFERENCE_REQUEST")
	treeRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_INPUT_TREE_REQUEST")
	candidates := [3]ReferenceToolRequest{readNativeGoReferenceRequest(t, "PHEBS_T42_REFERENCE_CANDIDATE_REQUEST"), readNativeGoReferenceRequest(t, "PHEBS_T42_PHEBS_CANDIDATE_REQUEST"), readNativeGoReferenceRequest(t, "PHEBS_T42_ZOEKT_CANDIDATE_REQUEST")}
	roles := [3]string{"t422-author", "phebs", "zoekt-git-index"}
	source := candidates[0]
	for i, request := range candidates {
		if request.Role != roles[i] || i < 2 && request.PlanSchema != "" || i == 2 && request.PlanSchema != PlanV3Schema || request.SourceCommit != source.SourceCommit || request.RepositoryRoot != source.RepositoryRoot || request.PlanSourceCommit != source.PlanSourceCommit || request.IntegratedMainCommit != source.IntegratedMainCommit || request.GoRoot != source.GoRoot || request.ModuleCache != source.ModuleCache || request.GitBinary != source.GitBinary {
			t.Fatal("exact same-source candidate lineage required", roles[i])
		}
	}
	if helperRequest.Role != linuxGoReferenceRole || helperRequest.PlanSchema != linuxGoReferenceRecipe || helperRequest.SourceCommit != source.SourceCommit || helperRequest.RepositoryRoot != source.RepositoryRoot || helperRequest.GoRoot != source.GoRoot || helperRequest.ModuleCache != source.ModuleCache || helperRequest.GitBinary != source.GitBinary || helperRequest.PlanSourceCommit != source.PlanSourceCommit || helperRequest.IntegratedMainCommit != source.IntegratedMainCommit {
		t.Fatal("exact source-bound helper required")
	}
	originals, err := snapshotGoReferenceOriginals(linuxGoReferencePlan{OriginalSDK: source.GoRoot, OriginalModules: source.ModuleCache, OriginalSource: source.RepositoryRoot})
	if err != nil {
		t.Fatal("original snapshot")
	}
	propagation, err := goBuildImportHostRoot()
	if err != nil {
		t.Fatal("host propagation")
	}
	t.Cleanup(func() {
		after, err := goBuildImportHostRoot()
		if err != nil || after != propagation {
			t.Error("host propagation changed")
		}
		for _, original := range originals {
			var current unix.Stat_t
			if unix.Lstat(original.path, &current) != nil || !sameLinuxInputStat(original.stat, current) {
				t.Error("original source/SDK/modules changed")
			}
		}
	})
	base, err := os.MkdirTemp("", "phebs-linux-live-epochs-")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := os.Lstat(base)
	if err != nil {
		t.Fatal("fixture identity", base)
	}
	parent, epochParent := filepath.Join(base, "inputs"), filepath.Join(base, "epochs")
	for _, directory := range []string{parent, epochParent} {
		if os.Mkdir(directory, 0o700) != nil {
			t.Fatal("private parent", base)
		}
	}
	var helper *LinuxGoReferenceHelper
	var treeHelper *LinuxInputTreeHelper
	var git *LinuxExecutionGitCustody
	var builds *LinuxExecutionGoBuildCustody
	var tools [4]*LinuxExecutionToolCustody
	var planInput *LinuxExecutionInputCustody
	var author *LinuxExecutionAuthorCustody
	var epochs *LinuxExecutionEpochConfigCustody
	var flow *LinuxExecutionEpochFlow
	successful := false
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if flow != nil {
			closeErr := flow.Close(ctx)
			select {
			case <-flow.done:
				if t.Failed() {
					logLinuxEpochNativeFailure(t, flow, base)
				}
				if closeErr != nil {
					t.Error("flow failed or sticky cleanup refusal", closeErr)
				}
			default:
				t.Error("unknown flow retains all owners and exact fixture", base)
				return
			}
		}
		if epochs != nil {
			if epochs.Close(ctx) != nil {
				t.Error("epoch cleanup refused", base)
			}
			if !epochs.closed {
				t.Error("epoch owner retained", base)
				return
			}
		}
		if author != nil {
			if author.Close(ctx) != nil {
				t.Error("author cleanup refused", base)
			}
			if !author.released {
				t.Error("author retained", base)
				return
			}
		}
		for _, tool := range tools {
			if tool != nil {
				if tool.Close() != nil {
					t.Error("tool cleanup refused", base)
				}
				if tool.probe != nil || tool.input == nil || !tool.input.closed {
					t.Error("tool owner retained", base)
					return
				}
			}
		}
		if planInput != nil && planInput.Close() != nil {
			t.Error("plan cleanup refused", base)
			if !planInput.closed {
				t.Error("plan owner retained", base)
				return
			}
		}
		if builds != nil {
			if builds.Close(ctx) != nil {
				t.Error("Build cleanup refused", base)
			}
			if !builds.released {
				t.Error("Build owner retained", base)
				return
			}
		}
		if git != nil {
			if git.Close(ctx) != nil {
				t.Error("Git cleanup refused", base)
			}
			if !git.released {
				t.Error("Git owner retained", base)
				return
			}
		}
		if helper != nil && helper.Close() != nil {
			t.Error("reference helper retained", base)
			return
		}
		if treeHelper != nil && treeHelper.Close() != nil {
			t.Error("tree helper retained", base)
			return
		}
		if !successful || t.Failed() {
			t.Log("failed exact fixture retained", base)
			return
		}
		current, err := os.Lstat(base)
		if err != nil || !os.SameFile(baseline, current) || os.RemoveAll(base) != nil {
			t.Error("exact joined fixture cleanup refused", base)
		}
	})
	helper, err = ProtectLinuxGoReferenceHelper(t.Context(), helperRequest)
	if err != nil {
		t.Fatal("reference helper", base)
	}
	treeHelper, err = ProtectLinuxInputTreeHelper(t.Context(), treeRequest)
	if err != nil {
		t.Fatal("H2k helper", base)
	}
	git, err = ProtectLinuxExecutionGit(t.Context(), parent, source.GitBinary, treeHelper)
	if err != nil {
		t.Fatal("protected Git", base)
	}
	builds, err = ProtectLinuxExecutionGoBuildInputs(t.Context(), parent, LinuxExecutionGoBuildRequest{Git: git, RepositoryRoot: source.RepositoryRoot, PlanSourceCommit: source.PlanSourceCommit, IntegratedMainCommit: source.IntegratedMainCommit, SourceCommit: source.SourceCommit, GoRoot: source.GoRoot, ModuleCache: source.ModuleCache}, treeHelper)
	if err != nil {
		if builds != nil {
			t.Log("Go preparation stage", builds.preparationStep, builds.Inventory())
		}
		t.Fatal("protected builds", base)
	}
	inventory := builds.Inventory()
	for i, request := range candidates {
		if i == 2 {
			tools[i], err = builds.ProtectReferenceToolV3(t.Context(), parent, roles[i], request.Binary, helper)
		} else {
			tools[i], err = builds.ProtectReferenceTool(t.Context(), parent, roles[i], request.Binary, helper)
		}
		if err != nil {
			logNativeGoReferenceFailure(t, builds, base)
			t.Fatal("actual protected reference build", roles[i], base)
		}
		identity, e := tools[i].Check(t.Context(), roles[i])
		if e != nil || i < 2 && identity.BuildVCSRevision != source.SourceCommit || tools[i].referenceInputs != builds || !builds.last.joined || !builds.last.empty || builds.Inventory() != inventory {
			t.Fatal("reference lineage/inventory", roles[i])
		}
		t.Logf("role=%s sha256=%s source=%s actual_reference_pid=%d joined=%t empty=%t", roles[i], identity.SHA256, identity.BuildVCSRevision, builds.last.pid, builds.last.joined, builds.last.empty)
	}
	surrealPath := os.Getenv("PHEBS_T42_SURREAL")
	canonical, err := filepath.EvalSymlinks(surrealPath)
	if err != nil || canonical != surrealPath || !filepath.IsAbs(canonical) {
		t.Fatal("explicit canonical Surreal image required")
	}
	tools[3], err = ProtectLinuxExecutionExternalTool(t.Context(), "surreal", surrealPath)
	if err != nil {
		t.Fatal("actual sealed Surreal", base)
	}
	plan, err := BuildPlanV3(source.PlanSourceCommit)
	if err != nil || plan.Profile.Physical.CombinedRegularFiles != 2_031_604 {
		t.Fatal("full frozen physical plan")
	}
	raw, err := MarshalCanonical(plan)
	if err != nil || len(raw) > MaxPlanBytes {
		t.Fatal("canonical plan")
	}
	planPath := filepath.Join(base, "selected-plan")
	if os.WriteFile(planPath, raw, 0o600) != nil {
		t.Fatal("plan staging")
	}
	planInput, err = ProtectLinuxExecutionInputs(t.Context(), []ExecutionInputCopy{{Name: "plan", Path: planPath, SHA256: SHA256(raw)}})
	if err != nil || os.Remove(planPath) != nil {
		t.Fatal("sealed plan")
	}
	author, err = PrepareLinuxExecutionAuthor(t.Context(), parent, LinuxExecutionAuthorRequest{Git: git, Builds: builds, Author: tools[0], Plan: planInput})
	if err != nil {
		t.Fatal("genuine source author", base)
	}
	epochs, err = PrepareLinuxExecutionEpochConfigs(t.Context(), epochParent, author, treeHelper)
	if err != nil {
		t.Fatal("genuine eight-input epochs", base)
	}
	if builds.Inventory() != inventory {
		t.Fatal("epoch constructor reinventoried SDK")
	}
	if _, err := os.Lstat(filepath.Join(builds.PrivateDirectory(), "payload")); !os.IsNotExist(err) {
		t.Fatal("mutable SDK payload retained")
	}
	// The operational resource scope forbids all preparation helper memfds and
	// failed prefixes. Actual joined constructors have already retired staging.
	if helper.Close() != nil || treeHelper.Close() != nil {
		t.Fatal("preparation helper retirement refused", base)
	}
	for _, tree := range []*LinuxExecutionInputTree{builds.tree, git.tree, epochs.catalogs, epochs.configs} {
		members, e := t4013.PrivateProcessSessionMembers(tree.pid)
		if e != nil || members != 0 || !tree.joined || !tree.empty {
			t.Fatal("constructor actual join/session")
		}
	}
	flow, err = PrepareLinuxExecutionEpochFlow(t.Context(), LinuxExecutionEpochRequest{Epochs: epochs, Phebs: tools[1], Zoekt: tools[2], Surreal: tools[3]})
	if err != nil {
		t.Fatal("source-owned Linux resource/held-flow admission refused", base)
	}
	resources := flow.held.resources
	resources.mu.Lock()
	t.Logf("resource_policy=%s whole_rss_limit=%d physical_bytes=%d available_bytes=%d effective_capacity_bytes=%d payload_bytes=%d required_bytes=%d", resources.policySHA, resources.RSSLimit(), resources.initial.PhysicalBytes, resources.initial.AvailableBytes, resources.calculation.EffectiveCapacityBytes, resources.footprint.PayloadBytes, resources.calculation.RequiredBytes)
	resources.mu.Unlock()
	if author.Close(t.Context()) == nil || epochs.Close(t.Context()) == nil {
		t.Fatal("whole-flow borrower lost owner barriers")
	}
	if _, e := author.AuthorNext(t.Context()); e == nil {
		t.Fatal("public author reentry admitted")
	}
	result, err := flow.AuthorA(t.Context())
	if err != nil || !result.Completed || !result.RootJoined || !result.SessionEmpty {
		logLinuxEpochNativeFailure(t, flow, base)
		t.Fatal("actual A", base)
	}
	run, err := flow.StartPhysicalB(t.Context())
	if err != nil {
		logLinuxEpochNativeFailure(t, flow, base)
		t.Fatal("actual first server", base)
	}
	for _, step := range []struct {
		name string
		run  func(context.Context) error
	}{{"health-a", run.Health}, {"cold", run.ColdToWarm}, {"warm", run.ObserveWarm}, {"physical-b", run.PhysicalB}} {
		if err := step.run(t.Context()); err != nil {
			logLinuxEpochNativeFailure(t, flow, base)
			t.Fatal("actual phase", step.name, base)
		}
	}
	logical, err := run.StartLogicalB(t.Context())
	if err != nil {
		logLinuxEpochNativeFailure(t, flow, base)
		t.Fatal("joined logical handoff", base)
	}
	if !run.run.joinedEmpty() {
		t.Fatal("source handed off before predecessor join")
	}
	if logical.Health(t.Context()) != nil || logical.LogicalB(t.Context()) != nil {
		logLinuxEpochNativeFailure(t, flow, base)
		t.Fatal("actual canonical logical catalog", base)
	}
	returned, err := logical.StartReturnA(t.Context())
	if err != nil {
		logLinuxEpochNativeFailure(t, flow, base)
		t.Fatal("joined return author/server handoff", base)
	}
	if !logical.run.joinedEmpty() {
		t.Fatal("return author preceded actual logical join")
	}
	if returned.Health(t.Context()) != nil || returned.ReturnA(t.Context()) != nil {
		logLinuxEpochNativeFailure(t, flow, base)
		t.Fatal("actual return authority", base)
	}
	final, err := returned.Stop(t.Context())
	if err != nil || !final.RootJoined || !final.SessionEmpty {
		logLinuxEpochNativeFailure(t, flow, base)
		t.Fatal("terminal actual server/receiver/session join", base)
	}
	results := author.Results()
	if len(results) != 3 {
		t.Fatal("three actual source lifetimes")
	}
	for i, result := range results {
		changed := uint64(1)
		if i == 0 {
			changed = 2_031_604
		}
		if !result.Completed || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || result.Response == nil || result.Response.Result != author.projection.expected[i] || result.Response.ChangedPhysicalFiles == nil || !result.Response.ChangedPhysicalFiles.Complete || result.Response.ChangedPhysicalFiles.Count != changed || !authorCustodyProducerComplete(result.Accounting, uint32(7+i), authorCustodyAttempts(i)) {
			t.Fatal("canonical actual terminal author 4/3/3", i)
		}
		t.Logf("revision=%s producer=%d actual_git=%d joined=%t empty=%t commit=%s", result.Revision, result.ProducerID, authorCustodyAttempts(i), result.RootJoined, result.SessionEmpty, result.Response.Result.Commit)
	}
	if len(final.Authorities) != 5 {
		t.Fatal("full cold/warm/physical/logical/return authority prefix", len(final.Authorities))
	}
	for i, name := range []string{"cold", "warm_noop", "physical_delta_b", "logical_delta_b", "return_a"} {
		if final.Authorities[i].Phase != name {
			t.Fatal("ordered actual authority prefix", i)
		}
	}
	t.Logf("actual_full_population=%d constructor_entries=%d constructor_files=%d constructor_bytes=%d final_authorities=%d root_joined=%t session_empty=%t", plan.Profile.Physical.CombinedRegularFiles, inventory.Entries, inventory.Files, inventory.Bytes, len(final.Authorities), final.RootJoined, final.SessionEmpty)
	if err := flow.Close(t.Context()); err != nil {
		logLinuxEpochNativeFailure(t, flow, base)
		t.Fatal("whole tracked flow join", base)
	}
	resources.mu.Lock()
	resourceChecks, resourceClosed, resourceFailed, resourceCloseFailed := resources.checks, resources.closed, resources.err != nil, resources.closeErr != nil
	resources.mu.Unlock()
	if resourceChecks == 0 || !resourceClosed || resourceFailed || resourceCloseFailed {
		t.Fatal("resource observation/keeper settlement")
	}
	t.Logf("resource_checks=%d keeper_closed=%t operation_refused=%t keeper_close_failed=%t", resourceChecks, resourceClosed, resourceFailed, resourceCloseFailed)
	for i, pid := range flow.core.serverSessions[:3] {
		members, e := t4013.PrivateProcessSessionMembers(pid)
		if pid <= 0 || e != nil || members != 0 {
			t.Fatal("actual server SID proof", i, pid, e)
		}
		t.Logf("server_epoch=%d actual_pid=%d actual_wait=true session_empty=true", i+1, pid)
	}
	for i, pid := range flow.core.epochs.author.sessions {
		members, e := t4013.PrivateProcessSessionMembers(pid)
		if pid <= 0 || e != nil || members != 0 {
			t.Fatal("actual author SID proof", i, pid, e)
		}
		t.Logf("author_revision=%d actual_pid=%d actual_wait=true session_empty=true", i, pid)
	}
	if author.borrowedBy != nil || epochs.borrowedBy != nil || epochs.active {
		t.Fatal("joined borrower barrier retained")
	}
	successful = true
}

func logLinuxEpochNativeFailure(t *testing.T, flow *LinuxExecutionEpochFlow, base string) {
	if flow != nil {
		select {
		case <-flow.done:
		default:
			t.Log("private flow done=false; mutable diagnostics unavailable; retained fixture", base)
			return
		}
	}
	t.Helper()
	if flow == nil {
		return
	}
	flow.mu.Lock()
	core := flow.core
	flow.mu.Unlock()
	if core == nil {
		t.Log("flow refusal before coordinator", base)
		return
	}
	core.mu.Lock()
	sessions := core.serverSessions
	core.mu.Unlock()
	t.Logf("private flow fixture=%s fixed_failure_step=%s actual_server_sessions=%v", base, flow.failureStep, sessions)
	if flow.held != nil && flow.held.resources != nil {
		a := flow.held.resources
		t.Logf("private resource policy=%s initial=%+v calculation=%+v payload_bytes=%d checks=%d", a.policySHA, a.initial, a.calculation, a.footprint.PayloadBytes, a.checks)
		t.Logf("private first resource refusal=%q", a.privateRefusal())
	}
	if flow.held != nil && flow.held.whole != nil {
		t.Logf("whole private process refusal=%q", flow.held.whole.gauge.privateRefusal())
		prefix, err := flow.held.whole.snapshot() // Joined saved state only; no new sample.
		t.Logf("whole private joined=%t RSS_limit_exceeded=%t saved_refusal=%t", prefix.Joined, prefix.RSSLimitExceeded, err != nil)
	}
	author := flow.request.Epochs.author
	author.mu.Lock()
	attempt := author.active
	if attempt == nil {
		attempt = author.last
	}
	if attempt != nil {
		select {
		case <-attempt.done:
			t.Logf("private author stage=%s pid=%d started=%t joined=%t empty=%t stderr=%q", attempt.step, attempt.pid, attempt.result.RootStarted, attempt.result.RootJoined, attempt.result.SessionEmpty, attempt.stderr)
			t.Logf("private author first_refusal=%s actual_wait_observed=%t actual_wait_failed=%t status_observed=%t exit_code=%d signal=%d", attempt.privateFailure, attempt.waitObserved, attempt.waitFailed, attempt.waitStatusObserved, attempt.waitExitCode, attempt.waitSignal)
		default:
			t.Log("private author done=false; mutable diagnostic fields unavailable")
		}
	}
	author.mu.Unlock()
	if held := flow.held; held != nil {
		for run := range held.servers {
			select {
			case <-run.done:
				run.mu.Lock()
				t.Logf("private epoch=%d failure=%v native_stop=%v admission=%+v", run.epoch.Epoch, run.err, run.nativeStopErr, run.stopDiagnostic)
				run.mu.Unlock()
				if run.output != nil {
					raw := run.output.buffer.Bytes()
					path := filepath.Join(base, fmt.Sprintf("joined-server-%d.log", run.epoch.Epoch))
					if os.WriteFile(path, raw, 0o600) != nil {
						t.Log("private joined output retention refused")
					}
				}
			default:
				t.Log("private server done=false; output unavailable")
			}
		}
	}
}
