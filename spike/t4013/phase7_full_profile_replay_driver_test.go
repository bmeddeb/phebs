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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const phase7ReplayDriverTestCommit = "0123456789abcdef0123456789abcdef01234567"

type phase7ReplayDriverFixture struct {
	root   string
	parent string
	lock   string
	bin    string
	driver string
}

func TestFullProfilePhase7ReplayDriverBoundary(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Phase 7 full-profile driver is Darwin-only")
	}

	t.Run("success commits a durable interlock before terminal PASS", func(t *testing.T) {
		fixture := newPhase7ReplayDriverFixture(t, "success")
		output, err := fixture.command(phase7ReplayDriverTestCommit).CombinedOutput()
		if err != nil {
			t.Fatalf("driver success: %v\n%s", err, output)
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
			phase7ReplayDriverTestCommit, filepath.Join(canonicalRun, "phase7-replay.json"), sha256.Sum256(raw))
		completion, err := os.ReadFile(filepath.Join(fixture.lock, "completion"))
		if err != nil {
			t.Fatalf("read durable completion interlock: %v", err)
		}
		if string(completion) != wantCompletion {
			t.Fatalf("completion interlock = %q, want %q", completion, wantCompletion)
		}

		retryOutput, retryErr := fixture.command(phase7ReplayDriverTestCommit).CombinedOutput()
		if phase7ReplayDriverExitCode(retryErr) != 1 ||
			strings.Contains(string(retryOutput), "Phase 7 full-profile replay: PASS") ||
			len(fixture.glob(t, "phebs-t4013-phase7-full.*")) != 1 {
			t.Fatalf("completed replay admitted a second attempt: %v\n%s", retryErr, retryOutput)
		}
	})

	for _, test := range []struct {
		name          string
		mode          string
		expected      string
		installDirty  bool
		installBadSHA bool
		wantStatus    int
		wantState     bool
	}{
		{name: "wrong HEAD refuses before authoring", mode: "success", expected: strings.Repeat("f", 40), wantStatus: 1},
		{name: "dirty checkout refuses before authoring", mode: "success", expected: phase7ReplayDriverTestCommit, installDirty: true, wantStatus: 1},
		{name: "child failure retains state", mode: "failure", expected: phase7ReplayDriverTestCommit, wantStatus: 7, wantState: true},
		{name: "result digest failure retains state", mode: "success", expected: phase7ReplayDriverTestCommit, installBadSHA: true, wantStatus: 1, wantState: true},
		{name: "surviving process group retains state", mode: "survivor", expected: phase7ReplayDriverTestCommit, wantStatus: 1, wantState: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPhase7ReplayDriverFixture(t, test.mode)
			if test.mode == "survivor" {
				t.Cleanup(func() { fixture.stopSurvivor(t) })
			}
			if test.installDirty {
				writePhase7ReplayDriverFile(t, filepath.Join(fixture.bin, "dirty"), "dirty\n", 0o600)
			}
			if test.installBadSHA {
				writePhase7ReplayDriverFile(t, filepath.Join(fixture.bin, "shasum"), `#!/bin/bash
for argument in "$@"; do
  [[ "$argument" == */phase7-replay.json ]] && exit 9
done
exec /usr/bin/shasum "$@"
`, 0o700)
			}
			output, err := fixture.command(test.expected).CombinedOutput()
			if test.mode == "survivor" {
				fixture.stopSurvivor(t)
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

	t.Run("TERM reaches the child group and retains state", func(t *testing.T) {
		fixture := newPhase7ReplayDriverFixture(t, "signal")
		command := fixture.command(phase7ReplayDriverTestCommit)
		var output bytes.Buffer
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = command.Process.Kill() })
		fixture.waitForFile(t, filepath.Join(fixture.bin, "ready"))
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		err := command.Wait()
		if phase7ReplayDriverExitCode(err) != 143 {
			t.Fatalf("signaled driver status = %d: %v\n%s", phase7ReplayDriverExitCode(err), err, output.Bytes())
		}
		if _, err := os.Lstat(filepath.Join(fixture.bin, "forwarded")); err != nil {
			t.Fatalf("TERM was not forwarded: %v\n%s", err, output.Bytes())
		}
		if _, err := os.Lstat(fixture.lock); err != nil ||
			len(fixture.glob(t, "phebs-t4013-phase7-full.*")) != 1 ||
			len(fixture.glob(t, "phebs-t4013-phase7-driver.*")) != 1 ||
			strings.Contains(output.String(), "Phase 7 full-profile replay: PASS") {
			t.Fatalf("signaled driver did not retain state:\n%s", output.Bytes())
		}
	})
}

func newPhase7ReplayDriverFixture(t *testing.T, mode string) phase7ReplayDriverFixture {
	t.Helper()
	root := t.TempDir()
	fixture := phase7ReplayDriverFixture{
		root: root, parent: filepath.Join(root, "runs"), lock: filepath.Join(root, "phase7.lock"),
		bin: filepath.Join(root, "bin"), driver: filepath.Join("run-phase7-full-profile-replay.sh"),
	}
	for _, path := range []string{fixture.parent, fixture.bin} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.bin, "commit"), phase7ReplayDriverTestCommit+"\n", 0o600)
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.bin, "mode"), mode+"\n", 0o600)
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.bin, "git"), `#!/bin/bash
set -euo pipefail
fake_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
case "$*" in
  "rev-parse HEAD") cat "$fake_dir/commit" ;;
  "status --porcelain=v1 --untracked-files=all") [[ ! -e "$fake_dir/dirty" ]] || printf '?? dirty\n' ;;
  *) exit 64 ;;
esac
`, 0o700)
	writePhase7ReplayDriverFile(t, filepath.Join(fixture.bin, "go"), `#!/bin/bash
set -euo pipefail
fake_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
control_root=${GOCACHE%/go-build}
[[ "${PHEBS_T4013_AMBIENT_POISON:-}" == "" ]]
[[ "$CGO_ENABLED" == 0 && "$GOENV" == off && "$GOFLAGS" == -mod=readonly ]]
[[ "$GOTOOLCHAIN" == local && "$GOTELEMETRY" == off && "$GOWORK" == off && "$GOEXPERIMENT" == "" ]]
[[ "$HOME" == "$control_root/home" && "$GOMODCACHE" == "$control_root/go-mod" ]]
[[ "$GOTMPDIR" == "$control_root/tmp" && "$TMPDIR" == "$control_root/tmp" ]]
[[ "$PHEBS_T4013_PHASE7_REPLAY_COMMIT" == "$(cat "$fake_dir/commit")" ]]
go_sha256="sha256:$(/usr/bin/shasum -a 256 "$0" | /usr/bin/awk 'NR == 1 { print $1 }')"
[[ "$PHEBS_T4013_PHASE7_REPLAY_GO_SHA256" == "$go_sha256" ]]
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
  survivor)
    (trap '' INT TERM HUP; sleep 60) </dev/null >/dev/null 2>&1 &
    pgid=$(/bin/ps -o pgid= -p "$$" | /usr/bin/tr -d ' ')
    [[ "$pgid" =~ ^[0-9]+$ && "$pgid" -gt 1 ]]
    printf '%s\n' "$pgid" > "$fake_dir/survivor.pgid"
    printf '{"schema":"fake"}\n' > "$PHEBS_T4013_PHASE7_REPLAY_ROOT/phase7-replay.json"
    exec /usr/bin/true
    ;;
  *) exit 65 ;;
esac
`, 0o700)
	return fixture
}

func (fixture phase7ReplayDriverFixture) command(expected string) *exec.Cmd {
	command := exec.Command("/bin/bash", "-c", `source "$1"
lock="$2"
main "$3"
`, "phase7-driver-test", fixture.driver, fixture.lock, expected)
	command.Env = phase7ReplayDriverEnvironment(fixture)
	return command
}

func phase7ReplayDriverEnvironment(fixture phase7ReplayDriverFixture) []string {
	environment := make([]string, 0, len(os.Environ())+5)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "PATH", fullProfilePhase7HostAttestation, "PHEBS_T4013_PHASE7_REPLAY_PARENT",
			"PHEBS_T4013_AMBIENT_POISON", runRootLockEnv:
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment,
		"PATH="+fixture.bin+":"+os.Getenv("PATH"),
		fullProfilePhase7HostAttestation+"="+fullProfilePhase7Attestation,
		"PHEBS_T4013_PHASE7_REPLAY_PARENT="+fixture.parent,
		"PHEBS_T4013_AMBIENT_POISON=must-not-reach-child",
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

func (fixture phase7ReplayDriverFixture) stopSurvivor(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixture.bin, "survivor.pgid"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pgid <= 0 {
		t.Fatalf("invalid survivor process group %q: %v", raw, err)
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("kill fake survivor process group: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(-pgid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("inspect fake survivor process group: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake survivor process group %d was not retired", pgid)
		}
		time.Sleep(10 * time.Millisecond)
	}
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
