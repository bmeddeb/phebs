//go:build darwin

package t4013

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

type phase7ReplayDriverFixture struct {
	root     string
	parent   string
	lock     string
	bin      string
	ambient  string
	checkout string
	commit   string
	driver   string
}

func TestFullProfilePhase7ReplayDriverBoundary(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Phase 7 full-profile driver is Darwin-only")
	}

	t.Run("success commits a durable interlock before terminal PASS", func(t *testing.T) {
		fixture := newPhase7ReplayDriverFixture(t, "success")
		liveWrapperMarker := filepath.Join(fixture.bin, "live-wrapper-called")
		writePhase7ReplayDriverFile(t, fixture.driver,
			"#!/bin/bash\n: > \""+liveWrapperMarker+"\"\necho 'Phase 7 full-profile replay: PASS'\n", 0o700)
		phase7ReplayDriverGit(t, fixture.checkout, "update-index", "--assume-unchanged",
			"spike/t4013/run-phase7-full-profile-replay.sh")
		writePhase7ReplayDriverFile(t,
			filepath.Join(fixture.checkout, "spike", "t4013", "ignored_phase7_test.go"),
			"package t4013\nthis is not valid Go\n", 0o600)
		writePhase7ReplayDriverFile(t, filepath.Join(fixture.checkout, "tracked.txt"), "hidden\n", 0o600)
		phase7ReplayDriverGit(t, fixture.checkout, "update-index", "--assume-unchanged", "tracked.txt")
		writePhase7ReplayDriverFile(t, filepath.Join(fixture.checkout, "skipped.txt"), "hidden\n", 0o600)
		phase7ReplayDriverGit(t, fixture.checkout, "update-index", "--skip-worktree", "skipped.txt")
		fsmonitorMarker := filepath.Join(fixture.bin, "fsmonitor-called")
		fsmonitor := filepath.Join(fixture.bin, "fsmonitor")
		writePhase7ReplayDriverFile(t, fsmonitor, "#!/bin/bash\n: > \""+fsmonitorMarker+"\"\nexit 99\n", 0o700)
		phase7ReplayDriverGit(t, fixture.checkout, "config", "core.fsmonitor", fsmonitor)
		ambientMarker := filepath.Join(fixture.ambient, "ambient-command-called")
		output, err := fixture.command(fixture.commit).CombinedOutput()
		if err != nil {
			t.Fatalf("driver success: %v\n%s", err, output)
		}
		if _, err := os.Lstat(fsmonitorMarker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("closed Git invoked checkout fsmonitor: %v", err)
		}
		if _, err := os.Lstat(ambientMarker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("closed bootstrap used ambient shell or PATH state: %v", err)
		}
		if _, err := os.Lstat(liveWrapperMarker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("bootstrap executed the hidden live wrapper: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(fixture.ambient, "bash-env.function")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("closed Bash imported an ambient function: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		if len(lines) < 3 || lines[len(lines)-1] != "Phase 7 full-profile replay: PASS" {
			t.Fatalf("driver terminal output:\n%s", output)
		}
		if _, err := os.Lstat(fixture.lock); err != nil {
			t.Fatalf("successful driver did not retain fixed interlock: %v", err)
		}
		if controls := fixture.glob(t, "phebs-t4013-phase7-driver.*"); len(controls) != 0 {
			t.Fatalf("successful driver retained controls: %v", controls)
		}
		runs := fixture.glob(t, "phebs-t4013-phase7-full.*")
		if len(runs) != 1 {
			t.Fatalf("successful driver run roots = %v", runs)
		}
		canonicalRun, err := filepath.EvalSymlinks(runs[0])
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(runs[0], "phase7-replay.json"))
		if err != nil {
			t.Fatal(err)
		}
		wantDigest := fmt.Sprintf("result sha256: %x", sha256.Sum256(raw))
		if lines[len(lines)-2] != wantDigest {
			t.Fatalf("driver result digest = %q, want %q", lines[len(lines)-2], wantDigest)
		}
		wantCompletion := fmt.Sprintf("schema=t4013-full-profile-phase7-replay-completion-v1\nsource_commit=%s\nresult=%s\nresult_sha256=sha256:%x\n",
			fixture.commit, filepath.Join(canonicalRun, "phase7-replay.json"), sha256.Sum256(raw))
		completion, err := os.ReadFile(filepath.Join(fixture.lock, "completion"))
		if err != nil {
			t.Fatalf("read durable completion interlock: %v", err)
		}
		if string(completion) != wantCompletion {
			t.Fatalf("completion interlock = %q, want %q", completion, wantCompletion)
		}

		retryOutput, retryErr := fixture.command(fixture.commit).CombinedOutput()
		if phase7ReplayDriverExitCode(retryErr) != 1 ||
			strings.Contains(string(retryOutput), "Phase 7 full-profile replay: PASS") ||
			len(fixture.glob(t, "phebs-t4013-phase7-full.*")) != 1 {
			t.Fatalf("completed replay admitted a second attempt: %v\n%s", retryErr, retryOutput)
		}
	})

	for _, test := range []struct {
		name          string
		mode          string
		wrongHead     bool
		installDirty  bool
		installBadSHA bool
		parentInRepo  bool
		wantStatus    int
		wantState     bool
	}{
		{name: "wrong HEAD refuses before authoring", mode: "success", wrongHead: true, wantStatus: 1},
		{name: "dirty checkout refuses before authoring", mode: "success", installDirty: true, wantStatus: 1},
		{name: "ignored parent inside checkout refuses before authoring", mode: "success", parentInRepo: true, wantStatus: 1},
		{name: "child failure retains state", mode: "failure", wantStatus: 7, wantState: true},
		{name: "result digest failure retains state", mode: "success", installBadSHA: true, wantStatus: 1, wantState: true},
		{name: "surviving process group retains state", mode: "survivor", wantStatus: 1, wantState: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPhase7ReplayDriverFixture(t, test.mode)
			if test.installDirty {
				writePhase7ReplayDriverFile(t, filepath.Join(fixture.checkout, "dirty"), "dirty\n", 0o600)
			}
			if test.parentInRepo {
				fixture.parent = filepath.Join(fixture.checkout, "ignored-parent")
				if err := os.Mkdir(fixture.parent, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if test.installBadSHA {
				writePhase7ReplayDriverFile(t, filepath.Join(fixture.bin, "shasum"), `#!/bin/bash
for argument in "$@"; do
  [[ "$argument" == */phase7-replay.json ]] && exit 9
done
exec /usr/bin/shasum "$@"
`, 0o700)
			}
			expected := fixture.commit
			if test.wrongHead {
				expected = strings.Repeat("f", 40)
			}
			output, err := fixture.command(expected).CombinedOutput()
			if test.mode == "survivor" {
				fixture.waitForFile(t, filepath.Join(fixture.bin, "survivor.done"))
				fixture.releaseRetainedSentinels(t)
			}
			if phase7ReplayDriverExitCode(err) != test.wantStatus {
				t.Fatalf("driver status = %d, want %d: %v\n%s",
					phase7ReplayDriverExitCode(err), test.wantStatus, err, output)
			}
			if strings.Contains(string(output), "Phase 7 full-profile replay: PASS") {
				t.Fatalf("failed driver emitted PASS:\n%s", output)
			}
			_, lockErr := os.Lstat(fixture.lock)
			runs := fixture.glob(t, "phebs-t4013-phase7-full.*")
			controls := fixture.glob(t, "phebs-t4013-phase7-driver.*")
			if test.wantState {
				if lockErr != nil || len(runs) != 1 || len(controls) != 1 {
					t.Fatalf("failed driver state lock=%v runs=%v controls=%v", lockErr, runs, controls)
				}
			} else if !errors.Is(lockErr, os.ErrNotExist) || len(runs) != 0 || len(controls) != 0 {
				t.Fatalf("refused driver authored state lock=%v runs=%v controls=%v", lockErr, runs, controls)
			}
		})
	}

	for _, signalTest := range []struct {
		name   string
		signal syscall.Signal
		status int
	}{{"TERM", syscall.SIGTERM, 143}, {"INT", syscall.SIGINT, 130}} {
		t.Run(signalTest.name+" reaches the child group and retains state", func(t *testing.T) {
			fixture := newPhase7ReplayDriverFixture(t, "signal")
			command := fixture.command(fixture.commit)
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = command.Process.Kill()
				fixture.releaseRetainedSentinels(t)
			})
			fixture.waitForFile(t, filepath.Join(fixture.bin, "ready"))
			if err := command.Process.Signal(signalTest.signal); err != nil {
				t.Fatal(err)
			}
			err := command.Wait()
			if phase7ReplayDriverExitCode(err) != signalTest.status {
				t.Fatalf("signaled driver status = %d: %v\n%s", phase7ReplayDriverExitCode(err), err, output.Bytes())
			}
			if _, err := os.Lstat(filepath.Join(fixture.bin, "forwarded")); err != nil {
				t.Fatalf("%s was not forwarded: %v\n%s", signalTest.name, err, output.Bytes())
			}
			if _, err := os.Lstat(fixture.lock); err != nil ||
				len(fixture.glob(t, "phebs-t4013-phase7-full.*")) != 1 ||
				len(fixture.glob(t, "phebs-t4013-phase7-driver.*")) != 1 ||
				strings.Contains(output.String(), "Phase 7 full-profile replay: PASS") {
				t.Fatalf("signaled driver did not retain state:\n%s", output.Bytes())
			}
		})
	}

	for _, signalTest := range []struct {
		name   string
		mode   string
		signal syscall.Signal
		status int
	}{
		{name: "INT before ready", mode: "signal-before-ready", signal: syscall.SIGINT, status: 130},
		{name: "TERM after ready", mode: "signal-after-ready", signal: syscall.SIGTERM, status: 143},
	} {
		t.Run(signalTest.name+" cannot cross the workload launch boundary", func(t *testing.T) {
			fixture := newPhase7ReplayDriverFixture(t, signalTest.mode)
			command := fixture.command(fixture.commit)
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			continuePath := filepath.Join(fixture.bin, "launcher-continue")
			t.Cleanup(func() {
				_ = os.WriteFile(continuePath, []byte("continue\n"), 0o600)
				_ = command.Process.Kill()
				fixture.releaseRetainedSentinels(t)
			})
			fixture.waitForFile(t, filepath.Join(fixture.bin, "launcher-paused"))
			if err := command.Process.Signal(signalTest.signal); err != nil {
				t.Fatal(err)
			}
			if signalTest.mode == "signal-before-ready" {
				writePhase7ReplayDriverFile(t, continuePath, "continue\n", 0o600)
			}
			waited := make(chan error, 1)
			go func() { waited <- command.Wait() }()
			select {
			case err := <-waited:
				if phase7ReplayDriverExitCode(err) != signalTest.status {
					t.Fatalf("launch-boundary status = %d: %v\n%s",
						phase7ReplayDriverExitCode(err), err, output.Bytes())
				}
			case <-time.After(10 * time.Second):
				writePhase7ReplayDriverFile(t, continuePath, "continue\n", 0o600)
				_ = command.Process.Kill()
				fixture.releaseRetainedSentinels(t)
				<-waited
				t.Fatalf("launch-boundary signal did not terminate the wrapper:\n%s", output.Bytes())
			}
			if _, err := os.Lstat(filepath.Join(fixture.bin, "workload-started")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("signal crossed the workload launch boundary: %v\n%s", err, output.Bytes())
			}
		})
	}

	t.Run("reaped child identity is never signaled", func(t *testing.T) {
		driver, err := filepath.Abs("run-phase7-full-profile-replay.sh")
		if err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(t.TempDir(), "kill-called")
		command := exec.Command("/bin/bash", "-c", `source "$1"
set -m
/usr/bin/true &
active_child_pid=$!
wait "$active_child_pid"
kill() { : > "$MARKER"; }
retain_on_signal TERM 143
`, "phase7-reaped-child", driver)
		command.Env = append(os.Environ(), "MARKER="+marker)
		output, runErr := command.CombinedOutput()
		if phase7ReplayDriverExitCode(runErr) != 143 {
			t.Fatalf("reaped-child status = %d: %v\n%s", phase7ReplayDriverExitCode(runErr), runErr, output)
		}
		if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reaped child identity was signaled: %v", err)
		}
	})

	t.Run("notification wait refuses a dead sentinel", func(t *testing.T) {
		driver, err := filepath.Abs("run-phase7-full-profile-replay.sh")
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		command := exec.Command("/bin/bash", "-c", `source "$1"
/usr/bin/mkfifo "$2/notify"
exec 8<> "$2/notify"
set -m
(
  exec 8>&-
  /usr/bin/true
) &
active_child_pid=$!
exec 9< "$2/notify"
exec 8>&-
if read_active_child_notification ready; then
  exit 99
fi
exec 9<&-
`, "phase7-dead-sentinel", driver, root)
		started := time.Now()
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("dead-sentinel refusal: %v\n%s", runErr, output)
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Fatalf("dead-sentinel refusal took %s", elapsed)
		}
	})

	t.Run("post-done signal retires the pinned sentinel", func(t *testing.T) {
		driver, err := filepath.Abs("run-phase7-full-profile-replay.sh")
		if err != nil {
			t.Fatal(err)
		}
		root := newPhase7ReplayCustodyRoot(t, "sentinel")
		sentinelRoot := filepath.Join(root, "sentinel")
		command := exec.Command("/bin/bash", "-c", `source "$1"
active_child_root="$2/sentinel"
mkdir "$active_child_root"
/usr/bin/mkfifo "$active_child_root/release" "$active_child_root/notify"
exec 7<> "$active_child_root/release"
exec 8<> "$active_child_root/notify"
: > "$active_child_root/done"
set -m
(
  set +m
  trap ':' INT TERM HUP
  : > "$active_child_root/alive"
  trap 'rm -f -- "$active_child_root/alive"' EXIT
  exec </dev/null >/dev/null 2>&1
  token=
  while [[ "$token" != release ]]; do
    IFS= read -r token <&7 || :
  done
) &
active_child_pid=$!
while [[ ! -f "$active_child_root/alive" ]]; do /bin/sleep 0.01; done
retain_on_signal TERM 143
`, "phase7-post-done-signal", driver, root)
		output, runErr := command.CombinedOutput()
		if phase7ReplayDriverExitCode(runErr) != 143 {
			t.Fatalf("post-done signal status = %d: %v\n%s",
				phase7ReplayDriverExitCode(runErr), runErr, output)
		}
		if _, err := os.Lstat(filepath.Join(sentinelRoot, "alive")); err == nil {
			releasePhase7ReplaySentinel(t, sentinelRoot)
			t.Fatal("post-done signal left the sentinel alive")
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	})

	t.Run("parent release descriptor cannot block after sentinel death", func(t *testing.T) {
		driver, err := filepath.Abs("run-phase7-full-profile-replay.sh")
		if err != nil {
			t.Fatal(err)
		}
		root := newPhase7ReplayCustodyRoot(t, "sentinel")
		command := exec.Command("/bin/bash", "-c", `source "$1"
active_child_root="$2/sentinel"
mkdir "$active_child_root"
/usr/bin/mkfifo "$active_child_root/release" "$active_child_root/notify"
exec 7<> "$active_child_root/release"
exec 8<> "$active_child_root/notify"
set -m
(
  set +m
  : > "$active_child_root/alive"
  trap 'rm -f -- "$active_child_root/alive"' EXIT
  token=
  while [[ "$token" != release ]]; do
    IFS= read -r token <&7 || :
  done
) &
active_child_pid=$!
while [[ ! -f "$active_child_root/alive" ]]; do /bin/sleep 0.01; done
active_child_group_is_drained "$active_child_pid"
kill -KILL %+
if retire_active_child_sentinel; then
  exit 99
fi
if jobs -p %+ >/dev/null 2>&1; then
  exit 98
fi
/bin/rm -f -- "$2/sentinel/alive"
`, "phase7-dead-before-release", driver, root)
		started := time.Now()
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("dead sentinel release refusal: %v\n%s", runErr, output)
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Fatalf("dead sentinel release refusal took %s", elapsed)
		}
	})
}

func newPhase7ReplayDriverFixture(t *testing.T, mode string) phase7ReplayDriverFixture {
	t.Helper()
	driverSource := filepath.Join("run-phase7-full-profile-replay.sh")
	driverRaw, err := os.ReadFile(driverSource)
	if err != nil {
		t.Fatal(err)
	}
	root := newPhase7ReplayCustodyRoot(t, filepath.Join("phase7.lock", ".phase7-child.*"))
	fixture := phase7ReplayDriverFixture{
		root: root, parent: filepath.Join(root, "runs"), lock: filepath.Join(root, "phase7.lock"),
		bin: filepath.Join(root, "bin"), ambient: filepath.Join(root, "ambient"),
		checkout: filepath.Join(root, "checkout"),
	}
	for _, path := range []string{fixture.parent, fixture.bin, fixture.ambient, fixture.checkout} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fixture.driver = filepath.Join(fixture.checkout, "spike", "t4013", "run-phase7-full-profile-replay.sh")
	if err := os.MkdirAll(filepath.Dir(fixture.driver), 0o700); err != nil {
		t.Fatal(err)
	}
	driverText := strings.Replace(string(driverRaw),
		"lock=/private/tmp/phebs-t4013-phase7-full.lock", "lock="+fixture.lock, 1)
	if driverText == string(driverRaw) {
		t.Fatal("phase 7 replay lock fixture was not installed")
	}
	launcherPause := fmt.Sprintf(": > %q\n      while [[ ! -f %q ]]; do /bin/sleep 0.01; done\n      ",
		filepath.Join(fixture.bin, "launcher-paused"), filepath.Join(fixture.bin, "launcher-continue"))
	switch mode {
	case "signal-before-ready":
		needle := "    set +e\n    (\n      trap 'exit 130' INT"
		driverText = strings.Replace(driverText, needle,
			"    set +e\n    "+launcherPause+"(\n      trap 'exit 130' INT", 1)
		if !strings.Contains(driverText, launcherPause) {
			t.Fatal("pre-launch launcher pause fixture was not installed")
		}
	case "signal-after-ready":
		needle := "      printf 'ready\\n' >&8\n      exec 8>&-"
		driverText = strings.Replace(driverText, needle,
			"      printf 'ready\\n' >&8\n      "+launcherPause+"exec 8>&-", 1)
		if !strings.Contains(driverText, launcherPause) {
			t.Fatal("post-ready launcher pause fixture was not installed")
		}
	}
	writePhase7ReplayDriverFile(t, fixture.driver, driverText, 0o700)
	phase7ReplayDriverGit(t, fixture.checkout, "init", "--quiet")
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.checkout, ".gitignore"),
		"/spike/t4013/ignored_phase7_test.go\n/ignored-parent/\n", 0o600)
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.checkout, "tracked.txt"), "tracked\n", 0o600)
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.checkout, "skipped.txt"), "skipped\n", 0o600)
	phase7ReplayDriverGit(t, fixture.checkout, "add", ".gitignore", "tracked.txt", "skipped.txt",
		"spike/t4013/run-phase7-full-profile-replay.sh")
	phase7ReplayDriverGit(t, fixture.checkout,
		"-c", "user.name=phase7-driver-test", "-c", "user.email=phase7-driver-test@example.invalid",
		"commit", "--quiet", "-m", "fixture")
	fixture.commit = strings.TrimSpace(phase7ReplayDriverGit(t, fixture.checkout, "rev-parse", "HEAD"))
	if !hexIdentity(fixture.commit, 40) {
		t.Fatalf("fixture commit = %q", fixture.commit)
	}
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.bin, "commit"), fixture.commit+"\n", 0o600)
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.bin, "mode"), mode+"\n", 0o600)
	ambientMarker := filepath.Join(fixture.ambient, "ambient-command-called")
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.ambient, "bash-env"),
		": > \""+ambientMarker+"\"\n", 0o600)
	for _, name := range []string{"env", "mktemp", "chmod", "rm", "rmdir"} {
		writePhase7ReplayDriverFile(t, filepath.Join(fixture.ambient, name),
			"#!/bin/bash\n: > \""+ambientMarker+"\"\nexit 97\n", 0o700)
	}
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.bin, "go"), `#!/bin/bash
set -euo pipefail
fake_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
control_root=${GOCACHE%/go-build}
[[ "${PHEBS_T4013_AMBIENT_POISON:-}" == "" ]]
[[ "$CGO_ENABLED" == 0 && "$GOENV" == off && "$GOFLAGS" == -mod=readonly ]]
[[ "$GOTOOLCHAIN" == local && "$GOTELEMETRY" == off && "$GOWORK" == off && "$GOEXPERIMENT" == "" ]]
[[ "$HOME" == "$control_root/home" && "$GOMODCACHE" == "$control_root/go-mod" ]]
[[ "$GOTMPDIR" == "$control_root/tmp" && "$TMPDIR" == "$control_root/tmp" ]]
[[ "$GIT_CONFIG_NOSYSTEM" == 1 && "$GIT_CONFIG_GLOBAL" == /dev/null && "$GIT_CONFIG_SYSTEM" == /dev/null ]]
[[ "$GIT_ATTR_NOSYSTEM" == 1 && "$GIT_NO_LAZY_FETCH" == 1 && "$GIT_NO_REPLACE_OBJECTS" == 1 ]]
[[ "$GIT_OPTIONAL_LOCKS" == 0 && "$GIT_TERMINAL_PROMPT" == 0 && "$GIT_PAGER" == cat ]]
[[ "$PHEBS_T4013_PHASE7_REPLAY_COMMIT" == "$(cat "$fake_dir/commit")" ]]
[[ "$PWD" == "$PHEBS_T4013_PHASE7_REPLAY_SOURCE_ROOT" && "$PWD" == */phebs-t4013-phase7-driver.*/source ]]
[[ -z "$(/bin/ls -A "$PHEBS_T4013_PHASE7_REPLAY_ROOT")" ]]
[[ ! -e spike/t4013/ignored_phase7_test.go ]]
[[ "$(cat tracked.txt)" == tracked ]]
[[ "$(cat skipped.txt)" == skipped ]]
go_sha256="sha256:$(/usr/bin/shasum -a 256 "$0" | /usr/bin/awk 'NR == 1 { print $1 }')"
[[ "$PHEBS_T4013_PHASE7_REPLAY_GO_SHA256" == "$go_sha256" ]]
git_path=$(command -v git)
git_sha256="sha256:$(/usr/bin/shasum -a 256 "$git_path" | /usr/bin/awk 'NR == 1 { print $1 }')"
[[ "$PHEBS_T4013_PHASE7_REPLAY_GIT_SHA256" == "$git_sha256" ]]
[[ "$1" == test && "$2" == ./spike/t4013 && "$3" == -run ]]
case "$(cat "$fake_dir/mode")" in
  success)
    printf '{"schema":"fake"}\n' > "$PHEBS_T4013_PHASE7_REPLAY_ROOT/phase7-replay.json"
    ;;
  failure)
    exit 7
    ;;
  signal)
    trap 'printf "forwarded\n" > "$fake_dir/forwarded"; exit 143' INT TERM HUP
    : > "$fake_dir/ready"
    while :; do sleep 1; done
    ;;
  signal-before-ready|signal-after-ready)
    : > "$fake_dir/workload-started"
    /bin/sleep 2
    exit 98
    ;;
  survivor)
    (trap '' INT TERM HUP; sleep 2; : > "$fake_dir/survivor.done") </dev/null >/dev/null 2>&1 &
    survivor_pid=$!
    pgid=$(/bin/ps -o pgid= -p "$$" | /usr/bin/tr -d ' ')
    [[ "$survivor_pid" -gt 1 && "$pgid" =~ ^[0-9]+$ && "$pgid" -gt 1 ]]
    kill -0 "$survivor_pid"
    printf '{"schema":"fake"}\n' > "$PHEBS_T4013_PHASE7_REPLAY_ROOT/phase7-replay.json"
    exec /usr/bin/true
    ;;
  *) exit 65 ;;
esac
`, 0o700)
	return fixture
}

func newPhase7ReplayCustodyRoot(t *testing.T, sentinelPattern string) string {
	t.Helper()
	rawRoot, err := os.MkdirTemp("", "phebs-t4013-phase7-driver.")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(rawRoot)
	if err != nil {
		_ = os.RemoveAll(rawRoot)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sentinelRoots, err := filepath.Glob(filepath.Join(root, sentinelPattern))
		if err != nil {
			t.Errorf("phase 7 fixture retained at %s: inspect sentinels: %v", root, err)
			return
		}
		for _, sentinelRoot := range sentinelRoots {
			releasePhase7ReplaySentinel(t, sentinelRoot)
		}
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove phase 7 fixture %s: %v", root, err)
		}
	})
	return root
}

func (fixture phase7ReplayDriverFixture) command(expected string) *exec.Cmd {
	body := `set -euo pipefail
git_path=$1
checkout=$2
expected=$3
bootstrap_parent=$4
driver_path=$5
parent=$6
bash_env=$7
closed_git() {
  /usr/bin/env -i HOME=/dev/null PATH="$(dirname "$git_path"):/usr/bin:/bin" LC_ALL=C LANG=C TZ=UTC \
    GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null \
    GIT_ATTR_NOSYSTEM=1 GIT_NO_LAZY_FETCH=1 GIT_NO_REPLACE_OBJECTS=1 \
    GIT_OPTIONAL_LOCKS=0 GIT_TERMINAL_PROMPT=0 \
    "$git_path" -c core.hooksPath=/dev/null -c core.attributesFile=/dev/null \
      -c core.excludesFile=/dev/null -c core.fsmonitor=false "$@"
}
[[ "$expected" =~ ^[0-9a-f]{40}$ ]]
bootstrap=$(/usr/bin/mktemp -d "$bootstrap_parent/phase7-bootstrap.XXXXXX")
runner="$bootstrap/run-phase7-full-profile-replay.sh"
cleanup_bootstrap() {
  status=$?
  trap - EXIT
  /bin/rm -f -- "$runner" || status=1
  /bin/rmdir -- "$bootstrap" || status=1
  exit "$status"
}
trap cleanup_bootstrap EXIT
blob=$(closed_git -C "$checkout" rev-parse \
  "$expected:spike/t4013/run-phase7-full-profile-replay.sh") || exit 1
[[ "$blob" =~ ^[0-9a-f]{40}$ ]]
closed_git -C "$checkout" cat-file blob "$blob" > "$runner"
[[ "$(closed_git -C "$checkout" hash-object --no-filters "$runner")" == "$blob" ]]
/bin/chmod 700 "$runner"
export BASH_ENV="$bash_env"
phase7_ambient_function() { : > "${bash_env}.function"; }
export -f phase7_ambient_function
export PATH="$driver_path"
export PHEBS_T4013_PHASE7_REPLAY_CHECKOUT="$checkout"
export PHEBS_T4013_PHASE7_REPLAY_PARENT="$parent"
export PHEBS_T4013_HOST_STABILITY_ATTESTATION=dedicated-single-operator-host-with-tool-mutation-disabled
printf 'Phase 7 replay bootstrap: %s\n' "$bootstrap"
source "$runner"
main "$expected"
trap - EXIT
/bin/rm -f -- "$runner"
/bin/rmdir -- "$bootstrap"
`
	command := exec.Command("/usr/bin/env", "-i",
		"HOME=/dev/null", "PATH=/usr/bin:/bin", "LC_ALL=C", "LANG=C", "TZ=UTC",
		"/bin/bash", "--noprofile", "--norc", "-c", body, "phase7-driver-test",
		"/usr/bin/git", fixture.checkout, expected, fixture.root,
		fixture.bin+":"+os.Getenv("PATH"), fixture.parent, filepath.Join(fixture.ambient, "bash-env"))
	command.Env = phase7ReplayDriverEnvironment(fixture)
	return command
}

func phase7ReplayDriverEnvironment(fixture phase7ReplayDriverFixture) []string {
	environment := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "PATH", fullProfilePhase7HostAttestation, "PHEBS_T4013_PHASE7_REPLAY_PARENT",
			"PHEBS_T4013_PHASE7_REPLAY_CHECKOUT",
			"PHEBS_T4013_AMBIENT_POISON", "BASH_ENV", "ENV", runRootLockEnv:
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment,
		"PATH="+fixture.ambient,
		fullProfilePhase7HostAttestation+"="+fullProfilePhase7Attestation,
		"PHEBS_T4013_PHASE7_REPLAY_PARENT="+fixture.parent,
		"PHEBS_T4013_PHASE7_REPLAY_CHECKOUT="+fixture.checkout,
		"PHEBS_T4013_AMBIENT_POISON=must-not-reach-child",
		"BASH_ENV="+filepath.Join(fixture.ambient, "bash-env"),
		runRootLockEnv+"=",
	)
}

func (fixture phase7ReplayDriverFixture) glob(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixture.parent, pattern))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func (fixture phase7ReplayDriverFixture) waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Lstat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", filepath.Base(path))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (fixture phase7ReplayDriverFixture) releaseRetainedSentinels(t *testing.T) {
	t.Helper()
	roots, err := filepath.Glob(filepath.Join(fixture.lock, ".phase7-child.*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		releasePhase7ReplaySentinel(t, root)
	}
}

func releasePhase7ReplaySentinel(t *testing.T, root string) {
	t.Helper()
	alive := filepath.Join(root, "alive")
	if _, err := os.Lstat(alive); errors.Is(err, os.ErrNotExist) {
		return
	} else if err != nil {
		t.Fatal(err)
	}
	release, err := os.OpenFile(filepath.Join(root, "release"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := release.WriteString("release\n"); err != nil {
		_ = release.Close()
		t.Fatal(err)
	}
	if err := release.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Lstat(alive); errors.Is(err, os.ErrNotExist) {
			return
		} else if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("retained sentinel did not acknowledge release: %s", root)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func phase7ReplayDriverGit(t *testing.T, checkout string, args ...string) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(gitPath, append([]string{"-C", checkout}, args...)...)
	environment := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			environment = append(environment, entry)
		}
	}
	command.Env = append(environment,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func phase7ReplayDriverExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

func writePhase7ReplayDriverFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
