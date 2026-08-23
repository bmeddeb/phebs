package t4013

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const privateServerShutdownHelperEnvironment = "PHEBS_T4013_SHUTDOWN_HELPER"

func TestPrivateToolReplacementRefusesEveryHarnessLaunchBeforeMutation(t *testing.T) {
	boundaries := []struct {
		name string
		run  func(context.Context, privateToolchain, PreparedProfile, string) error
	}{
		{name: "serve", run: func(ctx context.Context, toolchain privateToolchain, profile PreparedProfile, workspace string) error {
			server, err := launchPrivateServer(ctx, profile, toolchain, "boundary")
			if server != nil {
				_ = server.stop(time.Second)
			}
			return err
		}},
		{name: "backup", run: func(ctx context.Context, toolchain privateToolchain, profile PreparedProfile, workspace string) error {
			_, _, err := createLiveBackup(ctx, toolchain, profile, workspace, "boundary")
			return err
		}},
		{name: "restore", run: func(ctx context.Context, toolchain privateToolchain, profile PreparedProfile, workspace string) error {
			base := filepath.Dir(profile.Config)
			_, err := restoreBackup(ctx, toolchain, profile, workspace, privateRecoveryBackup{
				path: filepath.Join(base, "backup-boundary"), logPath: filepath.Join(base, "recovery-boundary.log"),
			}, "boundary")
			return err
		}},
	}

	for _, boundary := range boundaries {
		for replaced := 0; replaced < 4; replaced++ {
			name := privateToolchainInputs(privateToolchain{})[replaced].name
			t.Run(boundary.name+"/"+name, func(t *testing.T) {
				workspace := t.TempDir()
				toolRoot := filepath.Join(workspace, "toolchain")
				profileRoot := filepath.Join(workspace, "profile")
				tempRoot := filepath.Join(workspace, "tmp")
				for _, path := range []string{toolRoot, profileRoot, tempRoot, filepath.Join(profileRoot, "data")} {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				toolchain := privateToolchain{
					Schema: privateToolchainSchema, ClosedEnvironment: true, TempDir: tempRoot,
					Phebs: filepath.Join(toolRoot, "phebs"), Zoekt: filepath.Join(toolRoot, "zoekt-git-index"),
					Focused: filepath.Join(toolRoot, "phebs-focused-index"), Buf: filepath.Join(toolRoot, "buf"),
				}
				for index, input := range privateToolchainInputs(toolchain) {
					if err := os.WriteFile(input.path, []byte{byte(index + 1)}, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := bindPrivateToolchain(t.Context(), &toolchain); err != nil {
					t.Fatal(err)
				}
				input := privateToolchainInputs(toolchain)[replaced]
				if err := os.WriteFile(input.path, []byte{byte(replaced + 10)}, 0o700); err != nil {
					t.Fatal(err)
				}
				profile := PreparedProfile{
					Config: filepath.Join(profileRoot, "phebs.yaml"), DataDir: filepath.Join(profileRoot, "data"),
				}
				err := boundary.run(t.Context(), toolchain, profile, workspace)
				if err == nil || !strings.Contains(err.Error(), input.name) {
					t.Fatalf("replacement error = %v", err)
				}
				for _, path := range []string{
					filepath.Join(profileRoot, "server-boundary.log"),
					filepath.Join(profileRoot, "recovery-boundary.log"),
					profile.DataDir + ".prior-boundary",
				} {
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("launch mutation exists after refusal: %s: %v", path, err)
					}
				}
			})
		}
	}
}

func TestPrivateServerStopTerminatesProcessSession(t *testing.T) {
	if helperPath := os.Getenv(privateServerShutdownHelperEnvironment); helperPath != "" {
		runPrivateServerShutdownHelper(helperPath)
		return
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("private server process sessions require Linux or macOS")
	}
	tests := []struct {
		name           string
		cancelContext  bool
		wantForcedKill bool
	}{
		{name: "explicit timeout", wantForcedKill: true},
		{name: "context cancellation", cancelContext: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			childPIDPath := filepath.Join(t.TempDir(), "child.pid")
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPrivateServerStopTerminatesProcessSession$")
			command.Env = append(os.Environ(), privateServerShutdownHelperEnvironment+"="+childPIDPath)
			if err := isolatePrivateServerSession(command); err != nil {
				t.Fatal(err)
			}
			logFile, err := os.OpenFile(filepath.Join(t.TempDir(), "server.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			command.Stdout, command.Stderr = logFile, logFile
			if err := command.Start(); err != nil {
				_ = logFile.Close()
				t.Fatal(err)
			}
			server := &privateServer{
				command: command, sessionIsolated: true,
				done: make(chan error, 1), log: logFile,
			}
			go func() { server.done <- command.Wait() }()
			defer func() { _ = killPrivateServerSession(command.Process.Pid) }()

			childPID := awaitPrivateServerShutdownHelper(t, childPIDPath)
			if alive, err := privateServerSessionAlive(command.Process.Pid); err != nil || !alive {
				t.Fatalf("live server session was not observed: alive=%v err=%v", alive, err)
			}
			if err := syscall.Kill(childPID, 0); err != nil {
				t.Fatalf("live helper child was not independently observed: %v", err)
			}
			if test.cancelContext {
				cancel()
			}
			stopErr := server.stop(100 * time.Millisecond)
			if stopErr == nil {
				t.Fatal("forced or canceled process-group shutdown passed silently")
			}
			if test.wantForcedKill && !strings.Contains(stopErr.Error(), "required forced process-session kill") {
				t.Fatalf("stop error = %v", stopErr)
			}
			if !errors.Is(stopErr, errPrivateServerShutdownUnproven) {
				t.Fatalf("forced shutdown lost its retained uncertainty: %v", stopErr)
			}
			if alive, err := privateServerSessionAlive(command.Process.Pid); err != nil || alive {
				t.Fatalf("server process session survived: alive=%v err=%v", alive, err)
			}
			awaitProcessGone(t, childPID)
		})
	}
}

func TestReviewedSourceExportRetainsControlOnlyForUnprovenShutdown(t *testing.T) {
	repository := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(t.Context(), "git", args...)
		command.Dir = repository
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	runGit("init")
	runGit("config", "user.email", "t4013@example.invalid")
	runGit("config", "user.name", "T40.13")
	if err := os.WriteFile(filepath.Join(repository, "source.go"), []byte("package frozen\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "source.go")
	runGit("commit", "-m", "freeze")
	commit := runGit("rev-parse", "HEAD")
	gitCore, err := resolveGitCoreExecutable(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ordinary := errors.New("ordinary export failure")
	for _, test := range []struct {
		name         string
		cause        error
		contextCause error
		wantRetain   bool
	}{
		{name: "ordinary failure", cause: ordinary},
		{name: "cancellation", cause: context.Canceled, wantRetain: true},
		{name: "external deadline", cause: ordinary, contextCause: context.DeadlineExceeded, wantRetain: true},
		{name: "shutdown uncertainty", cause: errPrivateServerShutdownUnproven, wantRetain: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			output := filepath.Join(parent, "source")
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			err := exportReviewedSourceWith(
				ctx, repository, commit, output, gitCore,
				func(*exec.Cmd, string) error {
					if test.contextCause != nil {
						cancel(test.contextCause)
					}
					return test.cause
				},
			)
			if !errors.Is(err, test.cause) {
				t.Fatalf("export error = %v, want %v", err, test.cause)
			}
			controls, err := filepath.Glob(filepath.Join(parent, ".t4013-git-export-*"))
			if err != nil || (len(controls) > 0) != test.wantRetain {
				t.Fatalf("retained export controls = %v, %v", controls, err)
			}
		})
	}
}

func TestMeasuredSourceExportRetainsSignaledShutdownUncertainty(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("strict process sessions require Linux or macOS")
	}
	root := t.TempDir()
	output := filepath.Join(root, "source")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := extractFrozenSourceCommandMeasured(
		exec.CommandContext(t.Context(), "/bin/sh", "-c", "kill -KILL $$"), output, root,
	)
	if !errors.Is(err, errPrivateServerShutdownUnproven) {
		t.Fatalf("signaled measured source export = %v", err)
	}
}

func TestHistoricalPrivateServerStopRetainsRootProcessContract(t *testing.T) {
	if helperPath := os.Getenv(privateServerShutdownHelperEnvironment); helperPath != "" {
		runPrivateServerShutdownHelper(helperPath)
		return
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("historical process test requires Unix signals")
	}
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	command := exec.Command(os.Args[0], "-test.run=^TestHistoricalPrivateServerStopRetainsRootProcessContract$")
	command.Env = append(os.Environ(), privateServerShutdownHelperEnvironment+"="+childPIDPath)
	logFile, err := os.OpenFile(filepath.Join(t.TempDir(), "server.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	server := &privateServer{command: command, done: make(chan error, 1), log: logFile}
	go func() { server.done <- command.Wait() }()
	childPID := awaitPrivateServerShutdownHelper(t, childPIDPath)
	childRunning := true
	defer func() {
		if childRunning {
			_ = syscall.Kill(-childPID, syscall.SIGKILL)
		}
	}()
	if err := server.stop(100 * time.Millisecond); err != nil {
		t.Fatalf("historical root-process stop = %v", err)
	}
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("historical stop unexpectedly gained descendant-session ownership: %v", err)
	}
	if err := syscall.Kill(-childPID, syscall.SIGKILL); err != nil {
		t.Fatalf("clean historical descendant: %v", err)
	}
	awaitProcessGone(t, childPID)
	childRunning = false
}

func TestRSSSamplerReportsProbeErrorsOnlyForV25(t *testing.T) {
	probeErr := errors.New("probe failed")
	for _, test := range []struct {
		name   string
		strict bool
		want   bool
	}{
		{name: "historical", strict: false, want: false},
		{name: "V25", strict: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			sampler := newRSSSampler(1, test.strict)
			sampler.recordFailure(probeErr)
			_, _, _, _, err := sampler.metrics()
			if (err != nil) != test.want {
				t.Fatalf("strict=%v sampler error = %v", test.strict, err)
			}
		})
	}
}

func TestMeasuredShortCommandDoesNotInventSamplerFailure(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("strict process sampling requires Linux or macOS")
	}
	for _, test := range []struct {
		name    string
		status  string
		wantErr bool
	}{
		{name: "clean", status: "0"},
		{name: "nonzero", status: "7", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			metrics, err := runMeasuredCommand(
				exec.CommandContext(t.Context(), "/bin/sh", "-c", "exit "+test.status), t.TempDir(), true,
			)
			if errors.Is(err, errProcessSamplingFailed) || (err != nil) != test.wantErr || metrics.OtherChildren != 1 {
				t.Fatalf("short command metrics=%+v err=%v", metrics, err)
			}
		})
	}
}

func TestStrictRSSSamplerStopsBeforeAnotherProbe(t *testing.T) {
	sampler := newSyntheticRSSSampler(10)
	close(sampler.stop)
	sampler.run()
	if sampler.samples != 0 || sampler.failedSamples != 0 {
		t.Fatalf("stopped sampler ran another probe: samples=%d failures=%d",
			sampler.samples, sampler.failedSamples)
	}
}

func TestCustodyCommandCancellationTerminatesProcessSession(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("custody process sessions require Linux or macOS")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPrivateServerStopTerminatesProcessSession$")
	command.Env = append(os.Environ(), privateServerShutdownHelperEnvironment+"="+childPIDPath)
	done := make(chan error, 1)
	go func() {
		_, err := runCustodyCombinedOutput(command)
		done <- err
	}()
	childPID := awaitPrivateServerShutdownHelper(t, childPIDPath)
	sessionID, err := unix.Getsid(childPID)
	if err != nil {
		t.Fatal(err)
	}
	if alive, err := privateServerSessionAlive(sessionID); err != nil || !alive {
		t.Fatalf("live custody session was not observed: alive=%v err=%v", alive, err)
	}
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("live custody child was not independently observed: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, errPrivateServerShutdownUnproven) {
		t.Fatalf("canceled custody command lost shutdown uncertainty: %v", err)
	}
	if alive, err := privateServerSessionAlive(sessionID); err != nil || alive {
		t.Fatalf("custody process session survived: alive=%v err=%v", alive, err)
	}
	awaitProcessGone(t, childPID)
}

func awaitProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		} else if err != nil {
			t.Fatalf("inspect helper child %d: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper child %d survived process-session cleanup", pid)
}

func runPrivateServerShutdownHelper(childPIDPath string) {
	signal.Ignore(os.Interrupt)
	readyPath := childPIDPath + ".ready"
	child := exec.Command("/bin/sh", "-c", `trap '' INT; : > "$1"; while :; do sleep 30; done`, "helper", readyPath)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		panic(err)
	}
	if err := os.WriteFile(childPIDPath, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		panic(err)
	}
	if err := child.Wait(); err != nil {
		panic(err)
	}
}

func awaitPrivateServerShutdownHelper(t *testing.T, childPIDPath string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(childPIDPath)
		_, readyErr := os.Stat(childPIDPath + ".ready")
		if err == nil && readyErr == nil {
			pid, parseErr := strconv.Atoi(string(raw))
			if parseErr != nil || pid <= 0 {
				t.Fatalf("helper child PID = %q, %v", raw, parseErr)
			}
			return pid
		}
		if (err != nil && !errors.Is(err, os.ErrNotExist)) || (readyErr != nil && !errors.Is(readyErr, os.ErrNotExist)) {
			t.Fatalf("read helper readiness: %v, %v", err, readyErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("private server shutdown helper did not become ready")
	return 0
}

func TestExtractFrozenSourceAcceptsCanonicalGitDirectoryHeaders(t *testing.T) {
	archive := frozenSourceArchive(t,
		archiveEntry{name: ".claude/", kind: tar.TypeDir, mode: 0o775},
		archiveEntry{name: ".claude/launch.json", kind: tar.TypeReg, mode: 0o664, content: "frozen\n"},
		archiveEntry{name: "script.sh", kind: tar.TypeReg, mode: 0o775, content: "#!/bin/sh\n"},
	)
	output := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractFrozenSource(bytes.NewReader(archive), output); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(output, ".claude", "launch.json"))
	if err != nil || string(content) != "frozen\n" {
		t.Fatalf("extracted content = %q, %v", content, err)
	}
	info, err := os.Stat(filepath.Join(output, "script.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("extracted executable mode = %v", info.Mode().Perm())
	}
}

func TestInspectStartupLogRetainsOnlyClosedStageAndDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	raw := []byte("private startup detail stays in custody\n" +
		"2026/08/08 00:00:00 T40.13 startup lifecycle: {\"schema\":\"t4013-source-free-startup-v1\",\"stage\":\"store_opened\"}\n" +
		"more private detail stays in custody\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	bytes, digest, stage, err := inspectStartupLog(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if bytes != int64(len(raw)) || digest != "sha256:"+hex.EncodeToString(sum[:]) || stage != "store_opened" {
		t.Fatalf("startup log = %d, %q, %q", bytes, digest, stage)
	}
	if digest == string(raw) {
		t.Fatal("startup diagnostic retained raw log content")
	}
}

func TestInspectStartupLogRejectsUnknownStage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, []byte(
		"T40.13 startup lifecycle: {\"schema\":\"t4013-source-free-startup-v1\",\"stage\":\"private_path\"}\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := inspectStartupLog(path); err == nil {
		t.Fatal("unknown startup stage passed")
	}
}

func TestExtractFrozenSourceRejectsNoncanonicalAndEscapingEntries(t *testing.T) {
	tests := []struct {
		name  string
		entry archiveEntry
	}{
		{name: "absolute", entry: archiveEntry{name: "/escape", kind: tar.TypeReg}},
		{name: "parent", entry: archiveEntry{name: "../escape", kind: tar.TypeReg}},
		{name: "embedded parent", entry: archiveEntry{name: "inside/../../escape", kind: tar.TypeReg}},
		{name: "repeated separator", entry: archiveEntry{name: "inside//file", kind: tar.TypeReg}},
		{name: "backslash", entry: archiveEntry{name: `inside\file`, kind: tar.TypeReg}},
		{name: "root directory", entry: archiveEntry{name: "./", kind: tar.TypeDir}},
		{name: "symlink", entry: archiveEntry{name: "link", kind: tar.TypeSymlink}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := frozenSourceArchive(t, test.entry)
			output := filepath.Join(t.TempDir(), "source")
			if err := os.Mkdir(output, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := extractFrozenSource(bytes.NewReader(archive), output); err == nil {
				t.Fatal("invalid frozen source entry passed")
			}
			entries, err := os.ReadDir(output)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected archive wrote entries: %v, %v", entries, err)
			}
		})
	}
	if _, err := frozenArchiveName(&tar.Header{Name: "file/", Typeflag: tar.TypeReg}); err == nil {
		t.Fatal("non-directory trailing slash passed archive-name validation")
	}
}

func TestExecutionEnvironmentClosesAmbientGoControls(t *testing.T) {
	for _, value := range []string{
		"GOFLAGS=-tags=ambient", "GOWORK=/private/ambient.work", "GOEXPERIMENT=ambient",
		"GOTOOLCHAIN=auto", "GOOS=plan9", "GOARCH=386", "GOMEMLIMIT=1MiB",
		"CGO_ENABLED=1", "GIT_CONFIG_GLOBAL=/private/ambient.gitconfig",
		"PHEBS_PRIVATE=ambient", "SURREAL_LOG=trace", "ZOEKT_DISABLE_CATFILE_BATCH=1",
		"TMPDIR=/private/ambient-tmp", "GOTMPDIR=/private/ambient-gotmp", "DYLD_INSERT_LIBRARIES=ambient",
	} {
		name, replacement, _ := strings.Cut(value, "=")
		t.Setenv(name, replacement)
	}

	values := make(map[string][]string)
	for _, entry := range scrubExecutionEnvironment() {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = append(values[name], value)
	}
	want := map[string]string{
		"CGO_ENABLED": "0", "GOARCH": runtime.GOARCH, "GOENV": "off",
		"GOEXPERIMENT": "", "GOFLAGS": "-mod=readonly", "GOFIPS140": "off", "GOOS": runtime.GOOS,
		"GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
		"GIT_ATTR_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_NO_LAZY_FETCH": "1", "GIT_OPTIONAL_LOCKS": "0", "GIT_TERMINAL_PROMPT": "0",
	}
	for name, value := range want {
		if got := values[name]; len(got) != 1 || got[0] != value {
			t.Fatalf("closed environment %s = %v, want %q", name, got, value)
		}
	}
	for _, name := range []string{
		"GOMEMLIMIT", "PHEBS_PRIVATE", "SURREAL_LOG", "ZOEKT_DISABLE_CATFILE_BATCH",
		"TMPDIR", "GOTMPDIR", "DYLD_INSERT_LIBRARIES",
	} {
		if got := values[name]; len(got) != 0 {
			t.Fatalf("ambient environment %s survived: %v", name, got)
		}
	}
}

func TestProcessSnapshotFindsBoundedDescendantsAndRSS(t *testing.T) {
	pids, processes, err := parseProcessSnapshot([]byte(
		processSnapshotRow(10, 1, 4, "Fri Aug 22 12:00:00 2026", "/private/phebs")+
			processSnapshotRow(11, 10, 8, "Fri Aug 22 12:00:01 2026", "/usr/bin/git")+
			processSnapshotRow(12, 10, 16, "Fri Aug 22 12:00:02 2026", "/private/phebs-focused-index"),
	), 10)
	if err != nil || !slices.Equal(pids, []int{10, 11, 12}) ||
		processes[11].rssBytes != 8*1024 ||
		processes[12].name != "/private/phebs-focused-index" {
		t.Fatalf("process snapshot = %v, %+v, %v", pids, processes, err)
	}
}

func TestProcessStartIdentityIsStableForCurrentProcess(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("exact process identity is supported on Linux and macOS")
	}
	first, err := processStartIdentity(os.Getpid(), processSnapshot{})
	if err != nil || first.token == "" || first.parent < 0 || first.name == "" {
		t.Fatalf("first process identity = %+v, %v", first, err)
	}
	second, err := processStartIdentity(os.Getpid(), processSnapshot{})
	if err != nil || second != first {
		t.Fatalf("second process identity = %+v, %v; want %+v", second, err, first)
	}
}

func TestRSSSamplerContainsInvalidStrictSamples(t *testing.T) {
	root := processSnapshotRow(10, 1, 4, "Fri Aug 22 12:00:00 2026", "/private/phebs")
	child := processSnapshotRow(11, 10, 8, "Fri Aug 22 12:00:01 2026", "/usr/bin/git")
	tests := []struct {
		name     string
		output   string
		probeErr error
	}{
		{name: "timeout", probeErr: context.DeadlineExceeded},
		{name: "byte limit", probeErr: errors.New("T40.13 command output exceeds its bound")},
		{name: "malformed row", output: "10 1 bad /private/phebs\n"},
		{name: "duplicate PID", output: root + root},
		{name: "parent cycle", output: processSnapshotRow(10, 11, 4, "Fri Aug 22 12:00:00 2026", "/private/phebs") + child},
		{name: "nil traversal"},
		{name: "missing root", output: processSnapshotRow(99, 1, 4, "Fri Aug 22 12:00:00 2026", "/private/other")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sampler := newSyntheticRSSSampler(10)
			sampler.recordSnapshot([]byte(test.output), test.probeErr)
			peak, gitChildren, indexChildren, otherChildren, err := sampler.metrics()
			if !errors.Is(err, errProcessSamplingFailed) || peak != 0 || gitChildren != 0 ||
				indexChildren != 0 || otherChildren != 0 || sampler.failedSamples != 1 ||
				len(sampler.activeChildren) != 0 {
				t.Fatalf("invalid sample metrics = %d %d %d %d, failures=%d active=%d err=%v",
					peak, gitChildren, indexChildren, otherChildren, sampler.failedSamples,
					len(sampler.activeChildren), err)
			}
		})
	}

	sampler := newSyntheticRSSSampler(10)
	sampler.recordSnapshot([]byte(root), nil)
	peak, gitChildren, indexChildren, otherChildren, err := sampler.metrics()
	if err != nil || peak != 4*1024 || gitChildren != 0 || indexChildren != 0 || otherChildren != 0 {
		t.Fatalf("root-only sample metrics = %d %d %d %d, err=%v",
			peak, gitChildren, indexChildren, otherChildren, err)
	}

	mismatch := newSyntheticRSSSampler(10)
	mismatch.identityProbe = func(pid int, candidate processSnapshot) (processIdentityObservation, error) {
		if pid == 11 {
			candidate.parent++
		}
		return processIdentityObservation{
			token: strconv.Itoa(pid), parent: candidate.parent, name: candidate.name,
		}, nil
	}
	mismatch.recordSnapshot([]byte(root+child), nil)
	if peak, gitChildren, _, _, err := mismatch.metrics(); !errors.Is(err, errProcessSamplingFailed) || peak != 0 || gitChildren != 0 {
		t.Fatalf("kernel/table mismatch = peak:%d git:%d err:%v", peak, gitChildren, err)
	}
}

func TestProcessClassificationAcceptsOwnedKernelNameLimits(t *testing.T) {
	for _, test := range []struct {
		name string
		want processClass
	}{
		{name: "zoekt-git-index", want: processClassIndex},
		{name: "phebs-focused-i", want: processClassIndex},
		{name: "phebs-focused-in", want: processClassIndex},
		{name: "(phebs-focused-i)", want: processClassIndex},
		{name: "(git)", want: processClassGit},
	} {
		if got := classifyProcess(test.name); got != test.want {
			t.Fatalf("kernel command %q class = %d, want %d", test.name, got, test.want)
		}
	}
	for _, name := range []string{"phebs-focused-i", "phebs-focused-in"} {
		_, class, err := validateProcessIdentityObservation(11, processSnapshot{
			parent: 10, name: "/private/phebs-focused-index",
		}, processIdentityObservation{token: "child", parent: 10, name: name})
		if err != nil || class != processClassIndex {
			t.Fatalf("kernel command %q did not match its owned binary: class=%d err=%v", name, class, err)
		}
	}
}

func TestRSSSamplerRetainsBoundedFirstFailure(t *testing.T) {
	first := errors.New("first process probe failure")
	later := errors.New("later process probe failure")
	sampler := newSyntheticRSSSampler(10)
	sampler.recordSnapshot(nil, first)
	for range 10_000 {
		sampler.recordSnapshot(nil, later)
	}
	_, _, _, _, err := sampler.metrics()
	if !errors.Is(err, errProcessSamplingFailed) || !errors.Is(err, first) || errors.Is(err, later) ||
		sampler.firstErr != first || sampler.failedSamples != 10_001 ||
		strings.Count(err.Error(), first.Error()) != 1 || strings.Contains(err.Error(), later.Error()) {
		t.Fatalf("bounded sampler failure = first:%v count:%d err:%v",
			sampler.firstErr, sampler.failedSamples, err)
	}
	sampler.resetWindow()
	_, _, _, _, err = sampler.metrics()
	if !errors.Is(err, first) || sampler.failedSamples != 10_001 {
		t.Fatalf("reset erased sampler failure: count=%d err=%v", sampler.failedSamples, err)
	}
	sampler.failedSamples = ^uint64(0)
	sampler.recordSnapshot(nil, later)
	if sampler.failedSamples != ^uint64(0) {
		t.Fatalf("failed sample count wrapped to %d", sampler.failedSamples)
	}
}

func TestRSSSamplerCountsChildLifetimesInConstantSpace(t *testing.T) {
	const rootStart = "Fri Aug 22 12:00:00 2026"
	root := processSnapshotRow(10, 1, 4, rootStart, "/private/phebs")
	gitA := processSnapshotRow(11, 10, 8, "Fri Aug 22 12:00:01 2026", "/usr/bin/git")
	index := processSnapshotRow(12, 10, 16, "Fri Aug 22 12:00:02 2026", "/private/phebs-focused-index")
	sampler := newSyntheticRSSSampler(10)
	for range 2 {
		sampler.recordSnapshot([]byte(root+gitA+index), nil)
	}
	_, gitChildren, indexChildren, otherChildren, err := sampler.metrics()
	if err != nil || gitChildren != 1 || indexChildren != 1 || otherChildren != 0 || len(sampler.activeChildren) != 2 {
		t.Fatalf("repeated child sample = %d %d %d active=%d err=%v",
			gitChildren, indexChildren, otherChildren, len(sampler.activeChildren), err)
	}
	sampler.recordSnapshot([]byte(root), nil)
	sampler.recordSnapshot([]byte(root+gitA), nil)
	gitB := processSnapshotRow(11, 10, 8, "Fri Aug 22 12:00:03 2026", "/usr/bin/git")
	sampler.recordSnapshot([]byte(root+gitB), nil)
	_, gitChildren, indexChildren, otherChildren, err = sampler.metrics()
	if err != nil || gitChildren != 2 || indexChildren != 1 || otherChildren != 0 || len(sampler.activeChildren) != 1 {
		t.Fatalf("reused child sample = %d %d %d active=%d err=%v",
			gitChildren, indexChildren, otherChildren, len(sampler.activeChildren), err)
	}
	strong := newSyntheticRSSSampler(10)
	identities := map[int]string{10: strong.rootIdentity.token, 11: "child-instance-a"}
	strong.identityProbe = func(pid int, candidate processSnapshot) (processIdentityObservation, error) {
		return processIdentityObservation{
			token: identities[pid], parent: candidate.parent, name: candidate.name,
		}, nil
	}
	strong.recordSnapshot([]byte(root+gitA), nil)
	identities[11] = "child-instance-b"
	strong.recordSnapshot([]byte(root+gitA), nil)
	_, gitChildren, _, _, err = strong.metrics()
	if err != nil || gitChildren != 2 {
		t.Fatalf("same-second PID reuse = %d, err=%v", gitChildren, err)
	}

	drift := newSyntheticRSSSampler(10)
	drift.recordSnapshot([]byte(root+gitA), nil)
	drift.recordSnapshot([]byte(root+processSnapshotRow(
		11, 10, 8, "Fri Aug 22 12:00:01 2026", "/private/phebs-focused-index")), nil)
	_, _, _, _, err = drift.metrics()
	if !errors.Is(err, errProcessSamplingFailed) {
		t.Fatalf("child class drift passed: %v", err)
	}

	unavailable := newSyntheticRSSSampler(10)
	unavailable.recordSnapshot([]byte(root+gitA), nil)
	unavailable.identityProbe = func(pid int, candidate processSnapshot) (processIdentityObservation, error) {
		if pid == 11 {
			return processIdentityObservation{}, errors.New("identity disappeared")
		}
		return processIdentityObservation{
			token: unavailable.rootIdentity.token, parent: candidate.parent, name: candidate.name,
		}, nil
	}
	unavailable.recordSnapshot([]byte(root+gitA), nil)
	if _, _, _, _, err = unavailable.metrics(); !errors.Is(err, errProcessSamplingFailed) {
		t.Fatalf("ambiguous child identity passed: %v", err)
	}
	firstUnavailable := newSyntheticRSSSampler(10)
	firstUnavailable.identityProbe = unavailable.identityProbe
	for range 2 {
		firstUnavailable.recordSnapshot([]byte(root+gitA), nil)
	}
	_, gitChildren, _, _, err = firstUnavailable.metrics()
	if !errors.Is(err, errProcessSamplingFailed) || gitChildren != 0 || len(firstUnavailable.activeChildren) != 0 {
		t.Fatalf("repeated first-seen identity failure = %d active=%d err=%v",
			gitChildren, len(firstUnavailable.activeChildren), err)
	}

	bounded := newSyntheticRSSSampler(10)
	bounded.strictGitChildren = maxProcessChildLifetimes - 1
	bounded.recordSnapshot([]byte(root+gitA+index), nil)
	_, gitChildren, indexChildren, _, err = bounded.metrics()
	if !errors.Is(err, errProcessSamplingFailed) || gitChildren != maxProcessChildLifetimes-1 || indexChildren != 0 ||
		len(bounded.activeChildren) != 0 {
		t.Fatalf("child ceiling = git:%d index:%d active:%d err=%v",
			gitChildren, indexChildren, len(bounded.activeChildren), err)
	}
}

func TestRSSSamplerAllowsZeroSampleAfterObservedRootExit(t *testing.T) {
	live := newSyntheticRSSSampler(10)
	live.recordSnapshot(nil, nil)
	if _, _, _, _, err := live.metrics(); !errors.Is(err, errProcessSamplingFailed) {
		t.Fatalf("missing live root passed: %v", err)
	}

	exited := newSyntheticRSSSampler(10)
	exited.observeRootExit()
	exited.recordSnapshot(nil, nil)
	if peak, gitChildren, indexChildren, otherChildren, err := exited.metrics(); err != nil || peak != 0 || gitChildren != 0 || indexChildren != 0 || otherChildren != 0 {
		t.Fatalf("observed root exit = %d %d %d %d, err=%v",
			peak, gitChildren, indexChildren, otherChildren, err)
	}
	raced := newSyntheticRSSSampler(10)
	raced.expectConcurrentRootWait()
	recorded := make(chan struct{})
	go func() {
		raced.recordSnapshot(nil, nil)
		close(recorded)
	}()
	select {
	case <-recorded:
		t.Fatal("missing-root sample did not wait for its concurrent Wait owner")
	case <-time.After(10 * time.Millisecond):
	}
	raced.observeRootExit()
	<-recorded
	if _, _, _, _, err := raced.metrics(); err != nil {
		t.Fatalf("reap-before-marker race failed: %v", err)
	}

	survivor := newSyntheticRSSSampler(10)
	survivor.observeRootExit()
	survivor.recordSnapshot([]byte(processSnapshotRow(
		11, 10, 8, "Fri Aug 22 12:00:01 2026", "/usr/bin/git")), nil)
	if _, _, _, _, err := survivor.metrics(); !errors.Is(err, errProcessSamplingFailed) {
		t.Fatalf("missing root with surviving child passed: %v", err)
	}

	reused := newSyntheticRSSSampler(10)
	reused.observeRootExit()
	reused.recordSnapshot([]byte(processSnapshotRow(
		10, 1, 4, "Fri Aug 22 12:00:01 2026", "/private/reused")), nil)
	if peak, _, _, _, err := reused.metrics(); err != nil || peak != 0 {
		t.Fatalf("reused root after observed exit = peak:%d err:%v", peak, err)
	}
}

func TestRSSSamplerResetWaitsForInFlightSnapshot(t *testing.T) {
	root := []byte(processSnapshotRow(10, 1, 4, "Fri Aug 22 12:00:00 2026", "/private/phebs"))
	sampler := newSyntheticRSSSampler(10)
	sampler.sampleMu.Lock()
	sampler.recordSnapshotLocked(root, nil)
	sampler.mu.Lock()
	resetDone := make(chan struct{})
	go func() {
		sampler.resetWindow()
		close(resetDone)
	}()
	sampler.sampleMu.Unlock()
	deadline := time.Now().Add(time.Second)
	for sampler.sampleMu.TryLock() {
		sampler.sampleMu.Unlock()
		if time.Now().After(deadline) {
			sampler.mu.Unlock()
			t.Fatal("reset did not acquire the sample barrier")
		}
		runtime.Gosched()
	}
	sampler.mu.Unlock()
	<-resetDone
	peak, _, _, _, err := sampler.metrics()
	if err != nil || peak != 0 || sampler.samples != 0 {
		t.Fatalf("reset retained prior-window sample: peak=%d samples=%d err=%v", peak, sampler.samples, err)
	}
}

func TestProcessSnapshotDescendantBoundary(t *testing.T) {
	root := processSnapshotRow(10, 1, 4, "Fri Aug 22 12:00:00 2026", "/private/phebs")
	var children strings.Builder
	for index := 0; index < maxProcessDescendants+1; index++ {
		children.WriteString(processSnapshotRow(
			11+index, 10, 1, "Fri Aug 22 12:00:01 2026", "/private/child"))
	}
	output := children.String()
	last := strings.LastIndex(output, "\n")
	previous := strings.LastIndex(output[:last], "\n")
	accepted := root + output[:previous+1]
	if pids, _, err := parseProcessSnapshot([]byte(accepted), 10); err != nil || len(pids) != maxProcessDescendants+1 {
		t.Fatalf("maximum descendant snapshot = %d, err=%v", len(pids), err)
	}
	if _, _, err := parseProcessSnapshot([]byte(root+output), 10); err == nil {
		t.Fatal("over-bound descendant snapshot passed")
	}
}

func processSnapshotRow(pid, parent, rss int, _ string, name string) string {
	return strconv.Itoa(pid) + " " + strconv.Itoa(parent) + " " + strconv.Itoa(rss) + " " + name + "\n"
}

func newSyntheticRSSSampler(pid int) *rssSampler {
	sampler := newRSSSampler(pid, true)
	sampler.identityProbe = func(observedPID int, candidate processSnapshot) (processIdentityObservation, error) {
		token := strconv.Itoa(observedPID)
		return processIdentityObservation{token: token, parent: candidate.parent, name: candidate.name}, nil
	}
	sampler.captureRootIdentity()
	return sampler
}

func TestExecutionEnvironmentChangesOnlyAtV25(t *testing.T) {
	t.Setenv("GOEXPERIMENT", "historical-ambient")
	t.Setenv("SURREAL_LOG", "historical-ambient")
	for _, test := range []struct {
		schema string
		want   string
	}{
		{PlanSchemaV20, "historical-ambient"},
		{PlanSchemaV21, "historical-ambient"},
		{PlanSchemaV23, "historical-ambient"},
		{PlanSchemaV24, "historical-ambient"},
		{PlanSchemaV25, ""},
	} {
		got := ""
		surreal := ""
		for _, entry := range executionEnvironmentForPlan(Plan{Schema: test.schema}) {
			if name, value, found := strings.Cut(entry, "="); found && name == "GOEXPERIMENT" {
				got = value
			}
			if name, value, found := strings.Cut(entry, "="); found && name == "SURREAL_LOG" {
				surreal = value
			}
		}
		if got != test.want {
			t.Fatalf("execution environment for %s = %q, want %q", test.schema, got, test.want)
		}
		if surreal != test.want {
			t.Fatalf("Surreal environment for %s = %q, want %q", test.schema, surreal, test.want)
		}
	}
}

func TestFrozenSourceExportContractChangesOnlyAtV25(t *testing.T) {
	for _, test := range []struct {
		schema string
		want   frozenSourceExportContract
	}{
		{PlanSchemaV20, frozenSourceExportLegacy},
		{PlanSchemaV21, frozenSourceExportLegacy},
		{PlanSchemaV23, frozenSourceExportLegacy},
		{PlanSchemaV24, frozenSourceExportLegacy},
		{PlanSchemaV25, frozenSourceExportV25},
	} {
		if got := frozenSourceExportContractForPlan(test.schema); got != test.want {
			t.Fatalf("source export contract for %s = %v, want %v", test.schema, got, test.want)
		}
	}
}

type archiveEntry struct {
	name    string
	kind    byte
	mode    int64
	content string
}

func frozenSourceArchive(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for _, entry := range entries {
		mode := entry.mode
		if mode == 0 {
			mode = 0o600
		}
		header := &tar.Header{
			Name: entry.name, Typeflag: entry.kind, Mode: mode,
			Size: int64(len(entry.content)),
		}
		if entry.kind == tar.TypeDir {
			header.Size = 0
		}
		if entry.kind == tar.TypeSymlink {
			header.Linkname = "target"
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := writer.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
