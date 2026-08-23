package t4013

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCeremonyDriverCleanupTrapSurvivesFunctionScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(root, "run-large-mac-ceremony.sh")
	marker := filepath.Join(t.TempDir(), "cleanup marker")
	plan := filepath.Join(t.TempDir(), "plan with spaces.json")
	prepared := filepath.Join(t.TempDir(), "prepared 'quoted'.json")
	workspace := filepath.Join(t.TempDir(), "custody")
	script := `
source "$1"
cleanup_prepared() {
  printf '%s\n%s\n' "$1" "$2" > "$MARKER"
}
exercise_scope() {
  EXIT_PREPARED_PLAN="$PLAN_PATH"
  EXIT_PREPARED_MANIFEST="$PREPARED_PATH"
  EXIT_PREPARED_WORKSPACE="$WORKSPACE_PATH"
  trap cleanup_on_exit EXIT
  false
}
exercise_scope
`
	command := exec.Command("bash", "-c", script, "take11-cleanup-test", driver)
	command.Env = append(os.Environ(),
		"MARKER="+marker,
		"PLAN_PATH="+plan,
		"PREPARED_PATH="+prepared,
		"WORKSPACE_PATH="+workspace,
	)
	output, runErr := command.CombinedOutput()
	if runErr == nil {
		t.Fatal("synthetic preparation failure unexpectedly succeeded")
	}
	if bytes.Contains(output, []byte("unbound variable")) {
		t.Fatalf("cleanup trap lost its scoped state: %s", output)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("cleanup trap did not run: %v; output=%s", err, output)
	}
	want := plan + "\n" + prepared + "\n"
	if string(raw) != want {
		t.Fatalf("cleanup arguments = %q, want %q", raw, want)
	}
}

func TestCeremonyDriverCleanupTrapRetainsExecutedCustody(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "custody")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, executedMarkerName), []byte("executed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanupMarker := filepath.Join(root, "cleanup-ran")
	script := `
source "$1"
cleanup_prepared() { : > "$CLEANUP_MARKER"; }
EXIT_PREPARED_PLAN="$PLAN_PATH"
EXIT_PREPARED_MANIFEST="$PREPARED_PATH"
EXIT_PREPARED_WORKSPACE="$WORKSPACE_PATH"
trap cleanup_on_exit EXIT
false
`
	command := exec.Command("bash", "-c", script, "executed-cleanup-test", driver)
	command.Env = append(os.Environ(),
		"CLEANUP_MARKER="+cleanupMarker,
		"PLAN_PATH="+filepath.Join(root, "plan.json"),
		"PREPARED_PATH="+filepath.Join(root, "prepared.json"),
		"WORKSPACE_PATH="+workspace,
	)
	if err := command.Run(); err == nil {
		t.Fatal("synthetic executed-custody failure unexpectedly succeeded")
	}
	if _, err := os.Lstat(cleanupMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("executed custody cleanup ran: %v", err)
	}
}

func TestCeremonyDriverRetiresEveryConsumedNeutralID(t *testing.T) {
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := `source "$1"; reject_review_stopped_id "$2"`
	for _, value := range []string{
		"t40r1-neutral-01", "t40r1-neutral-16", "t40r1-neutral-17", "t40r1-neutral-34",
	} {
		if err := exec.Command("bash", "-c", script, "retired-id-test", driver, value).Run(); err == nil {
			t.Fatalf("consumed ceremony id passed: %s", value)
		}
	}
	if output, err := exec.Command(
		"bash", "-c", script, "fresh-id-test", driver, "t40r1-neutral-35",
	).CombinedOutput(); err != nil {
		t.Fatalf("fresh ceremony id was rejected: %v: %s", err, output)
	}
}

func TestCeremonyDriverClosesAmbientGoAndRehearsalEnvironment(t *testing.T) {
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := `
source "$1"
trap cleanup_on_exit EXIT
initialize_closed_go_cache
export GOFLAGS=-tags=ambient GOWORK=/private/ambient.work PHEBS_T4013_REPOSITORY_SCALE_TIMING=1
closed_go env
`
	output, err := exec.Command("bash", "-c", script, "closed-go-test", driver).CombinedOutput()
	if err != nil {
		t.Fatalf("closed Go environment failed: %v: %s", err, output)
	}
	values := make(map[string]string)
	for _, entry := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[name] = value
		}
	}
	for name, want := range map[string]string{
		"CGO_ENABLED": "0", "GOENV": "off", "GOFLAGS": "",
		"GOTOOLCHAIN": "local", "GOWORK": "off",
	} {
		if got := values[name]; got != want {
			t.Fatalf("closed Go environment %s = %q, want %q", name, got, want)
		}
	}
	if _, found := values["PHEBS_T4013_REPOSITORY_SCALE_TIMING"]; found {
		t.Fatal("closed Go environment retained an ambient rehearsal trigger")
	}
}

func TestCeremonyDriverChangesExecutionEnvironmentOnlyAtV25(t *testing.T) {
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := `source "$1"; trap cleanup_on_exit EXIT; initialize_closed_go_cache; cd "$3"; export GOEXPERIMENT=historical-ambient; plan_go "$2" env`
	for _, test := range []struct {
		schema string
		want   string
	}{
		{schema: PlanSchemaV24, want: "historical-ambient"},
		{schema: PlanSchemaV25, want: ""},
	} {
		plan := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(plan, []byte(`{"schema":"`+test.schema+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		module := t.TempDir()
		if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module example.invalid/driver-test\n\ngo 1.26\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("bash", "-c", script, "plan-go-test", driver, plan, module).CombinedOutput()
		if err != nil {
			t.Fatalf("plan environment %s: %v: %s", test.schema, err, output)
		}
		got := ""
		for _, entry := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			if name, value, found := strings.Cut(entry, "="); found && name == "GOEXPERIMENT" {
				got = value
			}
		}
		if got != test.want {
			t.Fatalf("plan environment %s = %q, want %q", test.schema, got, test.want)
		}
	}
}

func TestCeremonyDriverKeepsHistoricalCommandsForeground(t *testing.T) {
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(plan, []byte(`{"schema":"`+PlanSchemaV24+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `source "$1"; run_active_child() { return 99; }; plan_go_active "$2" printf 'historical-foreground\n'`
	output, err := exec.Command("bash", "-c", script, "historical-plan-go-test", driver, plan).CombinedOutput()
	if err != nil || string(output) != "historical-foreground\n" {
		t.Fatalf("historical command dispatch = %v: %q", err, output)
	}
}

func TestCeremonyDriverStopsWhenV25ModuleVerificationFails(t *testing.T) {
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(plan, []byte(`{"schema":"`+PlanSchemaV25+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "requested-command-ran")
	script := `
source "$1"
closed_go() {
  if [[ "$*" == *"go mod verify"* ]]; then return 9; fi
  : > "$MARKER"
}
if plan_go "$2" ignored-command; then exit 0; fi
[[ ! -e "$MARKER" ]]
`
	command := exec.Command("bash", "-c", script, "plan-go-verification-test", driver, plan)
	command.Env = append(os.Environ(), "MARKER="+marker)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("failed verification did not stop plan command: %v: %s", err, output)
	}
}

func TestCeremonyDriverSelectsPreflightByPlanSchema(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := `
source "$1"
preflight() { printf 'v25-execute\n'; }
historical_preflight() { printf 'historical-execute\n'; }
verification_preflight() { printf 'v25-seal\n'; }
historical_verification_preflight() { printf 'historical-seal\n'; }
preflight_for_plan "$2"
verification_preflight_for_plan "$2"
`
	for _, test := range []struct {
		schema string
		want   string
	}{
		{schema: PlanSchemaV24, want: "historical-execute\nhistorical-seal\n"},
		{schema: PlanSchemaV25, want: "v25-execute\nv25-seal\n"},
	} {
		plan := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(plan, []byte(`{"schema":"`+test.schema+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("bash", "-c", script, "preflight-dispatch-test", driver, plan).CombinedOutput()
		if err != nil {
			t.Fatalf("preflight dispatch %s: %v: %s", test.schema, err, output)
		}
		if string(output) != test.want {
			t.Fatalf("preflight dispatch %s = %q, want %q", test.schema, output, test.want)
		}
	}

	raw, err := os.ReadFile(driver)
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	seal := source[strings.Index(source, "seal_run() {"):strings.Index(source, "execute_ceremony() {")]
	execute := source[strings.Index(source, "execute_ceremony() {"):strings.Index(source, "verify_run() {")]
	if !strings.Contains(seal, `verification_preflight_for_plan "$plan_path"`) {
		t.Fatal("standalone seal bypasses plan-aware verification preflight")
	}
	if !strings.Contains(execute, `preflight_for_plan "$plan_path"`) {
		t.Fatal("execute bypasses plan-aware preflight")
	}
}

func TestCeremonyDriverExposesResumableSealCommand(t *testing.T) {
	raw, err := os.ReadFile("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		`$SCRIPT_NAME seal <ceremony-id>`,
		`seal_run "$2"`,
		`if (( seal_count == 3 )); then`,
	} {
		if !strings.Contains(string(raw), marker) {
			t.Fatalf("resumable seal marker is absent: %s", marker)
		}
	}
}

func TestCeremonyDriverSerializesRunOperations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	runRoot := t.TempDir()
	ready := filepath.Join(t.TempDir(), "ready")
	release := filepath.Join(t.TempDir(), "release")
	holderScript := `
source "$1"
trap cleanup_on_exit EXIT
acquire_run_lock "$2"
: > "$3"
while [[ ! -e "$4" ]]; do sleep 0.01; done
`
	holder := exec.Command("bash", "-c", holderScript, "lock-holder", driver, runRoot, ready, release)
	var holderOutput bytes.Buffer
	holder.Stdout = &holderOutput
	holder.Stderr = &holderOutput
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.WriteFile(release, []byte("release\n"), 0o600)
		_ = holder.Process.Kill()
		_ = holder.Wait()
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Lstat(ready); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock holder did not become ready: %s", holderOutput.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	contenderScript := `source "$1"; trap cleanup_on_exit EXIT; acquire_run_lock "$2"`
	output, err := exec.Command(
		"bash", "-c", contenderScript, "lock-contender", driver, runRoot,
	).CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("operation lock is retained")) {
		t.Fatalf("concurrent operation did not fail closed: err=%v output=%s", err, output)
	}
	if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err != nil {
		t.Fatalf("lock holder failed: %v: %s", err, holderOutput.String())
	}
	if _, err := os.Lstat(filepath.Join(runRoot, ".t4013-operation.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("normally released operation lock remains: %v", err)
	}

	raw, err := os.ReadFile(driver)
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	seal := source[strings.Index(source, "seal_run() {"):strings.Index(source, "execute_ceremony() {")]
	execute := source[strings.Index(source, "execute_ceremony() {"):strings.Index(source, "verify_run() {")]
	for name, section := range map[string]string{"seal": seal, "execute": execute} {
		if !strings.Contains(section, `acquire_run_lock "$run_root"`) {
			t.Fatalf("%s does not acquire the per-run operation lock", name)
		}
	}
}

func TestCeremonyDriverRetainsLockWhenOwnershipChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	runRoot := t.TempDir()
	script := `
source "$1"
trap cleanup_on_exit EXIT
acquire_run_lock "$2"
rm -- "${RUN_LOCK_DIRECTORY}/owner"
printf 'foreign\n' > "${RUN_LOCK_DIRECTORY}/owner"
`
	output, err := exec.Command("bash", "-c", script, "changed-lock-owner", driver, runRoot).CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("lock owner changed")) {
		t.Fatalf("changed lock owner was not retained: err=%v output=%s", err, output)
	}
	owner := filepath.Join(runRoot, ".t4013-operation.lock", "owner")
	raw, err := os.ReadFile(owner)
	if err != nil {
		t.Fatalf("unprovable operation lock was removed: %v", err)
	}
	if string(raw) != "foreign\n" {
		t.Fatalf("retained operation lock owner = %q", raw)
	}
}

func TestCeremonyDriverSignalRetainsUnprovenChildState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	runRoot := filepath.Join(root, "run")
	cache := filepath.Join(root, "go-cache")
	workspace := filepath.Join(root, "custody")
	cleanupMarker := filepath.Join(root, "cleanup-ran")
	for _, path := range []string{runRoot, cache, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script := `
source "$1"
cleanup_prepared() { : > "$CLEANUP_MARKER"; }
CLOSED_GO_CACHE="$CACHE_PATH"
EXIT_PREPARED_PLAN="$ROOT_PATH/plan.json"
EXIT_PREPARED_MANIFEST="$ROOT_PATH/prepared.json"
EXIT_PREPARED_WORKSPACE="$WORKSPACE_PATH"
trap cleanup_on_exit EXIT
acquire_run_lock "$RUN_ROOT"
retain_on_signal TERM 143
`
	command := exec.Command("bash", "-c", script, "signal-retention-test", driver)
	command.Env = append(os.Environ(),
		"ROOT_PATH="+root,
		"RUN_ROOT="+runRoot,
		"CACHE_PATH="+cache,
		"WORKSPACE_PATH="+workspace,
		"CLEANUP_MARKER="+cleanupMarker,
	)
	output, runErr := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 143 ||
		!bytes.Contains(output, []byte("child exit is unproven")) {
		t.Fatalf("signal retention = %v: %s", runErr, output)
	}
	for _, path := range []string{
		filepath.Join(runRoot, ".t4013-operation.lock"), cache, workspace,
	} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("signal removed retained state %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(cleanupMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("signal ran destructive prepared cleanup: %v", err)
	}
}

func TestCeremonyDriverForwardsSignalsAndRetainsOperationState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		signal syscall.Signal
		status int
	}{
		{name: "INT", signal: syscall.SIGINT, status: 130},
		{name: "TERM", signal: syscall.SIGTERM, status: 143},
		{name: "HUP", signal: syscall.SIGHUP, status: 129},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runRoot := filepath.Join(root, "run")
			cache := filepath.Join(root, "go-cache")
			workspace := filepath.Join(root, "custody")
			prepared := filepath.Join(root, "prepared.json")
			ready := filepath.Join(root, "ready")
			forwarded := filepath.Join(root, "forwarded")
			cleanupMarker := filepath.Join(root, "cleanup-ran")
			for _, path := range []string{runRoot, cache, workspace} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{prepared + ".preparing", filepath.Join(workspace, "server.log")} {
				if err := os.WriteFile(path, []byte("retain\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			script := `
source "$1"
cleanup_prepared() { : > "$CLEANUP_MARKER"; }
CLOSED_GO_CACHE="$CACHE_PATH"
EXIT_PREPARED_PLAN="$ROOT_PATH/plan.json"
EXIT_PREPARED_MANIFEST="$PREPARED_PATH"
EXIT_PREPARED_WORKSPACE="$WORKSPACE_PATH"
trap cleanup_on_exit EXIT
trap 'retain_on_signal INT 130' INT
trap 'retain_on_signal TERM 143' TERM
trap 'retain_on_signal HUP 129' HUP
acquire_run_lock "$RUN_ROOT"
run_active_child bash -c '
  trap '\''printf "forwarded\n" > "$FORWARDED_PATH"; exit 0'\'' INT TERM HUP
  : > "$READY_PATH"
  for _ in 1 2 3 4 5; do sleep 1; done
'
`
			command := exec.Command("bash", "-c", script, "signal-forwarding-test", driver)
			command.Env = append(os.Environ(),
				"ROOT_PATH="+root,
				"RUN_ROOT="+runRoot,
				"CACHE_PATH="+cache,
				"WORKSPACE_PATH="+workspace,
				"PREPARED_PATH="+prepared,
				"READY_PATH="+ready,
				"FORWARDED_PATH="+forwarded,
				"CLEANUP_MARKER="+cleanupMarker,
			)
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Lstat(ready); err == nil {
					break
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
				if time.Now().After(deadline) {
					_ = command.Process.Kill()
					_ = command.Wait()
					t.Fatalf("active child did not become ready: %s", output.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := command.Process.Signal(test.signal); err != nil {
				_ = command.Process.Kill()
				_ = command.Wait()
				t.Fatal(err)
			}
			runErr := command.Wait()
			var exitErr *exec.ExitError
			if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != test.status ||
				!bytes.Contains(output.Bytes(), []byte("child exit is unproven")) {
				t.Fatalf("signal forwarding = %v: %s", runErr, output.String())
			}
			for _, path := range []string{
				forwarded,
				filepath.Join(runRoot, ".t4013-operation.lock"),
				cache,
				workspace,
				prepared + ".preparing",
				filepath.Join(workspace, "server.log"),
			} {
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("signal removed retained state %s: %v", path, err)
				}
			}
			if _, err := os.Lstat(cleanupMarker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("signal ran destructive cleanup: %v", err)
			}
		})
	}
}

func TestCeremonyDriverFailurePolicyIsV25OnlyAndStopsBeforeSeal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		schema      string
		phase       string
		wantCleanup bool
		wantReceipt bool
		wantRetain  bool
	}{
		{name: "V25 Prepare", schema: PlanSchemaV25, phase: "prepare", wantRetain: true},
		{name: "V25 Execute", schema: PlanSchemaV25, phase: "execute", wantRetain: true},
		{name: "historical Prepare", schema: PlanSchemaV24, phase: "prepare", wantCleanup: true},
		{name: "historical Execute", schema: PlanSchemaV24, phase: "execute", wantCleanup: true, wantReceipt: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runRoot := filepath.Join(root, "run")
			evidence := filepath.Join(runRoot, "evidence")
			private := filepath.Join(runRoot, "private")
			repository := filepath.Join(root, "repository")
			cache := filepath.Join(root, "go-cache")
			for _, path := range []string{evidence, private, repository, cache} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			plan := filepath.Join(evidence, "plan.json")
			if err := os.WriteFile(plan, []byte(`{"schema":"`+test.schema+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			cleanupMarker := filepath.Join(root, "cleanup")
			receiptMarker := filepath.Join(root, "receipt")
			sealMarker := filepath.Join(root, "seal")
			script := `
source "$1"
REPO_REAL="$REPOSITORY_PATH"
CEREMONY_REAL="$ROOT_PATH"
CLOSED_GO_CACHE="$CACHE_PATH"
SIGNING_KEY="$ROOT_PATH/signing-key"
initialize_repository() { :; }
initialize_ceremony_root() { :; }
initialize_closed_go_cache() { :; }
select_signing_key() { :; }
ensure_signing_key() { :; }
run_root_for() { printf '%s\n' "$RUN_ROOT"; }
plan_digest_for() { printf 'sha256:test\n'; }
require_exact_inventory() { :; }
cmp() { :; }
verify_frozen_identity() { :; }
preflight_for_plan() { :; }
require_clean_checkout() { :; }
cleanup_prepared() {
  : > "$CLEANUP_MARKER"
  [[ ! -d "$RUN_ROOT/custody" ]] || rmdir "$RUN_ROOT/custody"
  [[ ! -e "$2" ]] || unlink "$2"
  [[ ! -e "$2.preparing" ]] || unlink "$2.preparing"
}
plan_go() { : > "$RECEIPT_MARKER"; }
seal_evidence() { : > "$SEAL_MARKER"; }
plan_go_in_repo_active() {
  if [[ "$*" == *t4013-prepare* ]]; then
    mkdir "$RUN_ROOT/custody"
    if [[ "$FAIL_PHASE" == prepare ]]; then
      : > "$RUN_ROOT/private/prepared.json.preparing"
      return 9
    fi
    : > "$RUN_ROOT/private/prepared.json"
    return 0
  fi
  rmdir "$RUN_ROOT/custody"
  return 9
}
trap cleanup_on_exit EXIT
execute_ceremony fresh-policy-test sha256:test "$EXECUTE_APPROVAL"
`
			command := exec.Command("bash", "-c", script, "failure-policy-test", driver)
			command.Env = append(os.Environ(),
				"ROOT_PATH="+root,
				"RUN_ROOT="+runRoot,
				"REPOSITORY_PATH="+repository,
				"CACHE_PATH="+cache,
				"FAIL_PHASE="+test.phase,
				"CLEANUP_MARKER="+cleanupMarker,
				"RECEIPT_MARKER="+receiptMarker,
				"SEAL_MARKER="+sealMarker,
			)
			output, runErr := command.CombinedOutput()
			if runErr == nil {
				t.Fatalf("failed ceremony returned success: %s", output)
			}
			for path, want := range map[string]bool{
				cleanupMarker: test.wantCleanup,
				receiptMarker: test.wantReceipt,
				sealMarker:    false,
				filepath.Join(runRoot, ".t4013-operation.lock"): test.wantRetain,
				cache: test.wantRetain,
			} {
				_, err := os.Lstat(path)
				if (err == nil) != want || err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("path %s present=%t, want=%t, err=%v; output=%s", path, err == nil, want, err, output)
				}
			}
			if got := bytes.Contains(output, []byte("child exit is unproven")); got != test.wantRetain {
				t.Fatalf("unproven retention output=%t, want=%t: %s", got, test.wantRetain, output)
			}
		})
	}
}

func TestCeremonyDriverCleanupRefusalRetainsOperationState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	runRoot := filepath.Join(root, "run")
	cache := filepath.Join(root, "go-cache")
	workspace := filepath.Join(root, "custody")
	for _, path := range []string{runRoot, cache, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script := `
source "$1"
cleanup_prepared() { return 9; }
CLOSED_GO_CACHE="$CACHE_PATH"
EXIT_PREPARED_PLAN="$ROOT_PATH/plan.json"
EXIT_PREPARED_MANIFEST="$ROOT_PATH/prepared.json"
EXIT_PREPARED_WORKSPACE="$WORKSPACE_PATH"
trap cleanup_on_exit EXIT
acquire_run_lock "$RUN_ROOT"
false
`
	command := exec.Command("bash", "-c", script, "cleanup-refusal-test", driver)
	command.Env = append(os.Environ(),
		"ROOT_PATH="+root,
		"RUN_ROOT="+runRoot,
		"CACHE_PATH="+cache,
		"WORKSPACE_PATH="+workspace,
	)
	output, runErr := command.CombinedOutput()
	if runErr == nil || !bytes.Contains(output, []byte("prepared cleanup refused")) {
		t.Fatalf("cleanup refusal = %v: %s", runErr, output)
	}
	for _, path := range []string{
		filepath.Join(runRoot, ".t4013-operation.lock"), cache, workspace,
	} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("cleanup refusal removed retained state %s: %v", path, err)
		}
	}
}

func TestCeremonyDriverPreservesHistoricalSealReceiptAndResumesV25(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	driver, err := filepath.Abs("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		schema      string
		existing    bool
		wantCommand bool
	}{
		{name: "historical-existing", schema: PlanSchemaV24, existing: true},
		{name: "historical-missing", schema: PlanSchemaV24, wantCommand: true},
		{name: "v25-existing", schema: PlanSchemaV25, existing: true, wantCommand: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			plan := filepath.Join(root, "plan.json")
			observation := filepath.Join(root, "observation.json")
			results := filepath.Join(root, "results.json")
			marker := filepath.Join(root, "receipt-command-ran")
			if err := os.WriteFile(plan, []byte(`{"schema":"`+test.schema+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(observation, []byte("observation\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.existing {
				if err := os.WriteFile(results, []byte("historical receipt\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			script := `
source "$1"
REPO_REAL="$2"
plan_go() {
  : > "$MARKER"
  [[ -e "$RESULTS_PATH" ]] || printf 'resumed receipt\n' > "$RESULTS_PATH"
}
prepare_receipt_for_seal "$3" sha256:test "$4" "$5"
`
			command := exec.Command("bash", "-c", script, "seal-receipt-test", driver, root, plan, observation, results)
			command.Env = append(os.Environ(), "MARKER="+marker, "RESULTS_PATH="+results)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("prepare seal receipt: %v: %s", err, output)
			}
			_, markerErr := os.Lstat(marker)
			if got := markerErr == nil; got != test.wantCommand {
				t.Fatalf("receipt command ran = %t, want %t", got, test.wantCommand)
			}
			raw, err := os.ReadFile(results)
			if err != nil {
				t.Fatal(err)
			}
			want := "historical receipt\n"
			if !test.existing {
				want = "resumed receipt\n"
			}
			if string(raw) != want {
				t.Fatalf("receipt = %q, want %q", raw, want)
			}
		})
	}
}

func TestCeremonyDriverRetainsSurvivingCustodyAndPublishesSealFilesAtomically(t *testing.T) {
	raw, err := os.ReadFile("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	driver := string(raw)
	for _, marker := range []string{
		`if [[ -e "$custody_path" || -L "$custody_path" ]]; then`,
		`! -e "${EXIT_PREPARED_WORKSPACE}/.t4013-executed"`,
		`"${observation_path}.tmp"`,
		`"${observation_path}.teardown"`,
		`"${observation_path}.teardown.tmp"`,
		`cleanup_prepared "${evidence_root}/plan.json" "$prepared_path"`,
		`verification_preflight_for_plan "$plan_path"`,
		`manifest_tmp="${evidence_root}/manifest.json.tmp"`,
		`-data-parent "$CEREMONY_REAL"`,
		`durable_promote "$manifest_tmp" "${evidence_root}/manifest.json" "$evidence_root"`,
		`durable_promote "$checksums_tmp" "${evidence_root}/SHA256SUMS" "$evidence_root"`,
		`durable_promote "$signature_tmp" "${evidence_root}/SHA256SUMS.sig" "$evidence_root"`,
		`go run ./spike/t4013/cmd/t4013-promote`,
	} {
		if !strings.Contains(driver, marker) {
			t.Fatalf("custody-safe atomic seal marker is absent: %s", marker)
		}
	}
	if strings.Contains(driver, `(( execute_status != 0 )) && [[ ! -e "$observation_path" ]]`) {
		t.Fatal("driver still authorizes fallback cleanup from observation existence")
	}
	execute := driver[strings.Index(driver, "execute_ceremony() {"):strings.Index(driver, "verify_run() {")]
	cheap := strings.Index(execute, `[[ "$approved_digest" == "$actual_digest" ]]`)
	expensive := strings.Index(execute, `preflight_for_plan "$plan_path"`)
	if cheap < 0 || expensive < 0 || cheap > expensive {
		t.Fatal("execute runs costly preflight before cheap frozen-plan validation")
	}
	bound := strings.Index(driver, `package_bytes="$(wc -c < "$package_tmp"`)
	promote := strings.Index(driver, `durable_promote "$package_tmp" "$package" "$run_root"`)
	if bound < 0 || promote < 0 || bound > promote {
		t.Fatal("package is promoted before its transfer bound is checked")
	}
}
