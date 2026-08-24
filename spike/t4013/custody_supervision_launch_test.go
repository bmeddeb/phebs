//go:build darwin || linux

package t4013

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

const launcherDescendantModeEnv = "T4013_CUSTODY_LAUNCHER_MODE"

func bindLauncherToolchain(t *testing.T, toolchain privateToolchain) privateToolchain {
	t.Helper()
	toolchain.Schema = privateToolchainSchema
	root := t.TempDir()
	paths := []*string{&toolchain.Phebs, &toolchain.Zoekt, &toolchain.Focused, &toolchain.Buf}
	for index, path := range paths {
		if *path != "" {
			continue
		}
		*path = filepath.Join(root, privateToolchainInputs(toolchain)[index].name)
		if err := os.WriteFile(*path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if toolchain.TempDir == "" {
		toolchain.TempDir = filepath.Join(root, "tmp")
		if err := os.Mkdir(toolchain.TempDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	workspace := filepath.Dir(toolchain.TempDir)
	gitCore, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	toolchain.host.gitCore.path = gitCore
	toolchain.controls, toolchain.controlsDigest, err = createExecutionControls(workspace, toolchain.host)
	if err != nil {
		t.Fatal(err)
	}
	toolchain.TempDir = toolchain.controls.Temp
	for _, name := range []string{
		"T4013_CUSTODY_LEASE", "T4013_CUSTODY_LEASE_FD", "T4013_CUSTODY_LAUNCHER_MODE",
		"T4013_LAUNCHER_TEST_BINARY", "T4013_LAUNCHER_READY", "T4013_LAUNCHER_RELEASE",
		"T4013_LAUNCHER_DATA", "T4013_LAUNCHER_OUTPUT",
	} {
		if value, ok := os.LookupEnv(name); ok {
			toolchain.extraEnvironment = append(toolchain.extraEnvironment, name+"="+value)
		}
	}
	if _, err := bindPrivateToolchain(t.Context(), &toolchain); err != nil {
		t.Fatal(err)
	}
	return toolchain
}

func TestV25CustodyLeaseCoversRealGitArchive(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runLauncherGit(t, repository, "init", "--quiet")
	runLauncherGit(t, repository, "config", "user.name", "T40.13")
	runLauncherGit(t, repository, "config", "user.email", "t4013@example.invalid")
	if err := os.WriteFile(
		filepath.Join(repository, "payload"), bytes.Repeat([]byte("x"), 2<<20), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runLauncherGit(t, repository, "add", "payload")
	runLauncherGit(t, repository, "commit", "--quiet", "-m", "fixture")
	commit := strings.TrimSpace(runLauncherGit(t, repository, "rev-parse", "HEAD"))

	workspace := filepath.Join(root, "custody")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	supervision := beginLauncherCustody(t, workspace, "git-archive")

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	gitCore, err := resolveGitCoreExecutable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = exportReviewedSourceWith(
		ctx, repository, commit, filepath.Join(workspace, "source"), gitCore,
		func(command *exec.Cmd, _ string) error {
			stream, err := command.StdoutPipe()
			if err != nil {
				return err
			}
			command.Stderr = io.Discard
			if err := isolatePrivateServerSession(command); err != nil {
				return err
			}
			if err := command.Start(); err != nil {
				return err
			}
			cleanupLauncherCommand(t, command, "", supervision)
			var first [1]byte
			if _, err := io.ReadFull(stream, first[:]); err != nil {
				return err
			}
			if err := command.Process.Signal(syscall.SIGSTOP); err != nil {
				return err
			}
			if err := supervision.Drain(""); !errors.Is(err, errCustodyDescendantsLive) {
				_ = command.Process.Signal(syscall.SIGCONT)
				_ = command.Process.Kill()
				_ = command.Wait()
				return errors.Join(err, errors.New("real git archive did not retain the custody lease"))
			}
			if err := command.Process.Signal(syscall.SIGCONT); err != nil {
				return err
			}
			_, copyErr := io.Copy(io.Discard, stream)
			waitErr := command.Wait()
			return errors.Join(copyErr, waitErr, supervision.Drain(""))
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	finishLauncherCustody(t, supervision)
}

func TestV25CustodyLeaseCoversPhebsServeDescendantAfterRootExit(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "custody")
	profileRoot := filepath.Join(workspace, "profile")
	tempDir := filepath.Join(workspace, "tmp")
	for _, directory := range []string{workspace, profileRoot, tempDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(profileRoot, "phebs.yaml")
	if err := os.WriteFile(configPath, []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverPath := filepath.Join(root, "phebs")
	writeLauncherScript(t, serverPath, `
test "$1" = serve
T4013_CUSTODY_LAUNCHER_MODE=wait "$T4013_LAUNCHER_TEST_BINARY" -test.run='^TestV25CustodyLauncherDescendantHelper$' &
exit 0
`)
	ready := filepath.Join(root, "ready")
	release := filepath.Join(root, "release")
	t.Setenv("T4013_LAUNCHER_TEST_BINARY", os.Args[0])
	t.Setenv("T4013_LAUNCHER_READY", ready)
	t.Setenv("T4013_LAUNCHER_RELEASE", release)

	supervision := beginLauncherCustody(t, workspace, "phebs-serve")
	t.Setenv("T4013_CUSTODY_LEASE", filepath.Join(supervision.directory, custodyLeaseName))
	t.Setenv("T4013_CUSTODY_LEASE_FD", strconv.FormatUint(uint64(supervision.lease.Fd()), 10))
	toolchain := bindLauncherToolchain(t, privateToolchain{
		Schema: privateToolchainSchema, Phebs: serverPath,
		TempDir: tempDir, ClosedEnvironment: true,
	})
	server, err := launchPrivateServer(t.Context(), PreparedProfile{
		Config: configPath,
	}, toolchain, "custody")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.stop(5 * time.Second) })
	t.Cleanup(func() { _ = os.WriteFile(release, []byte("release\n"), 0o600) })
	if err := waitForCustodyFile(ready, 5*time.Second); err != nil {
		_ = server.stop(time.Second)
		t.Fatal(err)
	}
	if err := server.sampler.close(); err != nil {
		_ = server.stop(time.Second)
		t.Fatal(err)
	}
	rootErr := waitLauncherServerRoot(t, server, 5*time.Second)
	if rootErr != nil {
		_ = server.stop(time.Second)
		t.Fatal(rootErr)
	}
	if err := supervision.Drain(""); !errors.Is(err, errCustodyDescendantsLive) {
		_ = os.WriteFile(release, []byte("release\n"), 0o600)
		_ = server.stop(time.Second)
		t.Fatalf("serve descendant after root exit did not retain the custody lease: %v", err)
	}
	if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.stop(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	drainLauncherCustody(t, supervision, 10*time.Second)
	finishLauncherCustody(t, supervision)
}

func TestV25CustodyLeaseCoversPhebsRecoveryLaunchers(t *testing.T) {
	for _, mode := range []string{"backup", "restore"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			workspace := filepath.Join(root, "custody")
			profileRoot := filepath.Join(workspace, "profile")
			dataDir := filepath.Join(profileRoot, "data")
			tempDir := filepath.Join(workspace, "tmp")
			for _, directory := range []string{workspace, profileRoot, dataDir, tempDir} {
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			configPath := filepath.Join(profileRoot, "phebs.yaml")
			if err := os.WriteFile(configPath, []byte("test\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			toolPath := filepath.Join(root, "phebs")
			writeLauncherScript(t, toolPath, `
exec "$T4013_LAUNCHER_TEST_BINARY" -test.run='^TestV25CustodyLauncherDescendantHelper$'
`)
			ready := filepath.Join(root, "ready")
			release := filepath.Join(root, "release")
			t.Setenv("T4013_LAUNCHER_READY", ready)
			t.Setenv("T4013_LAUNCHER_RELEASE", release)
			t.Setenv("T4013_LAUNCHER_DATA", dataDir)
			t.Setenv("T4013_LAUNCHER_OUTPUT", filepath.Join(profileRoot, "backup-test"))
			t.Setenv("T4013_LAUNCHER_TEST_BINARY", os.Args[0])
			t.Setenv(launcherDescendantModeEnv, mode)
			t.Cleanup(func() { _ = os.WriteFile(release, []byte("release\n"), 0o600) })
			toolchain := privateToolchain{
				Phebs: toolPath, TempDir: tempDir, ClosedEnvironment: true,
			}
			toolchain = bindLauncherToolchain(t, toolchain)
			profile := PreparedProfile{Config: configPath, DataDir: dataDir}
			if mode == "restore" {
				if err := os.Mkdir(filepath.Join(profileRoot, "backup-test"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(profileRoot, "recovery-test.log"), []byte("backup\n"), 0o600,
				); err != nil {
					t.Fatal(err)
				}
			}

			supervision := beginLauncherCustody(t, workspace, "phebs-"+mode)
			t.Setenv("T4013_CUSTODY_LEASE", filepath.Join(supervision.directory, custodyLeaseName))
			t.Setenv("T4013_CUSTODY_LEASE_FD", strconv.FormatUint(uint64(supervision.lease.Fd()), 10))
			toolchain.extraEnvironment = append(toolchain.extraEnvironment,
				"T4013_CUSTODY_LEASE="+os.Getenv("T4013_CUSTODY_LEASE"),
				"T4013_CUSTODY_LEASE_FD="+os.Getenv("T4013_CUSTODY_LEASE_FD"),
			)
			launcherCtx, cancelLauncher := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancelLauncher()
			done := make(chan error, 1)
			finished := false
			t.Cleanup(func() {
				if finished {
					return
				}
				_ = os.WriteFile(release, []byte("release\n"), 0o600)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Errorf("%s launcher survived test cleanup", mode)
				}
			})
			go func() {
				if mode == "backup" {
					_, _, err := createLiveBackup(
						launcherCtx, toolchain, profile, workspace, "test",
					)
					done <- err
					return
				}
				_, err := restoreBackup(
					launcherCtx, toolchain, profile, workspace,
					privateRecoveryBackup{
						path:    filepath.Join(profileRoot, "backup-test"),
						logPath: filepath.Join(profileRoot, "recovery-test.log"),
					}, "test",
				)
				done <- err
			}()
			if err := waitForCustodyFile(ready, 5*time.Second); err != nil {
				t.Fatal(err)
			}
			if err := supervision.Drain(""); !errors.Is(err, errCustodyDescendantsLive) {
				t.Fatalf("%s root did not retain the custody lease: %v", mode, err)
			}
			if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				finished = true
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s launcher did not exit", mode)
			}
			if err := supervision.Drain(""); err != nil {
				t.Fatal(err)
			}
			finishLauncherCustody(t, supervision)
		})
	}
}

func TestV25CustodyLeaseCoversRealV25Toolchain(t *testing.T) {
	if os.Getenv("PHEBS_T4013_REAL_LAUNCHERS") != "1" {
		t.Skip("set PHEBS_T4013_REAL_LAUNCHERS=1 for the focused real-tool proof")
	}
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot, err = filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("go-run-compiler-linker", func(t *testing.T) {
		testRealGoLauncherInheritance(t, t.TempDir())
	})
	t.Run("zoekt-git-index", func(t *testing.T) {
		zoekt := filepath.Join(t.TempDir(), "zoekt-git-index")
		buildRealLauncherTool(t, moduleRoot, zoekt, "github.com/sourcegraph/zoekt/cmd/zoekt-git-index")
		testRealIndexerLauncherInheritance(t, t.TempDir(), zoekt)
	})
	t.Run("phebs-surreal-hard-death", func(t *testing.T) {
		requireRealSurrealLauncher(t)
		binDir := t.TempDir()
		phebs := filepath.Join(binDir, "phebs")
		zoekt := filepath.Join(binDir, "zoekt-git-index")
		buildRealLauncherTool(t, moduleRoot, phebs, "./cmd/phebs")
		buildRealLauncherTool(t, moduleRoot, zoekt, "github.com/sourcegraph/zoekt/cmd/zoekt-git-index")
		testRealPhebsSurrealInheritance(t, t.TempDir(), phebs, zoekt)
	})
	t.Run("phebs-backup-restore", func(t *testing.T) {
		requireRealSurrealLauncher(t)
		binDir := t.TempDir()
		phebs := filepath.Join(binDir, "phebs")
		zoekt := filepath.Join(binDir, "zoekt-git-index")
		buildRealLauncherTool(t, moduleRoot, phebs, "./cmd/phebs")
		buildRealLauncherTool(t, moduleRoot, zoekt, "github.com/sourcegraph/zoekt/cmd/zoekt-git-index")
		testRealPhebsRecoveryInheritance(t, t.TempDir(), phebs, zoekt)
	})
}

func testRealGoLauncherInheritance(t *testing.T, parent string) {
	t.Helper()
	root := filepath.Join(parent, "go-run")
	module := filepath.Join(root, "module")
	cache := filepath.Join(root, "cache")
	for _, directory := range []string{root, module, cache} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module custody.test/child\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	program := `package main

import (
	"os"
	"time"
)

func main() {
	if err := os.WriteFile(os.Getenv("T4013_GO_READY"), []byte("ready\n"), 0600); err != nil {
		panic(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("T4013_GO_RELEASE")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			os.Exit(2)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
`
	if err := os.WriteFile(filepath.Join(module, "main.go"), []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(root, "ready")
	release := filepath.Join(root, "release")
	workspace := filepath.Join(root, "custody")
	supervision := beginLauncherCustody(t, workspace, "real-go-run")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", ".")
	command.Dir = module
	command.Env = append(os.Environ(), "GOCACHE="+cache,
		"T4013_GO_READY="+ready, "T4013_GO_RELEASE="+release)
	if err := isolatePrivateServerSession(command); err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	cleanupLauncherCommand(t, command, release, supervision)
	if err := waitForCustodyFile(ready, 30*time.Second); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal(err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("go run root unexpectedly survived SIGKILL")
	}
	if err := supervision.Drain(""); !errors.Is(err, errCustodyDescendantsLive) {
		t.Fatalf("go run descendant did not retain the custody lease after its root died: %v", err)
	}
	if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	drainLauncherCustody(t, supervision, 10*time.Second)
	finishLauncherCustody(t, supervision)
}

func testRealIndexerLauncherInheritance(t *testing.T, parent, binary string) {
	t.Helper()
	root := filepath.Join(parent, "indexer")
	repository := filepath.Join(root, "repository")
	index := filepath.Join(root, "index")
	for _, directory := range []string{root, repository, index} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runLauncherGit(t, repository, "init", "--quiet")
	runLauncherGit(t, repository, "config", "user.name", "T40.13")
	runLauncherGit(t, repository, "config", "user.email", "t4013@example.invalid")
	content := bytes.Repeat([]byte("package fixture\n\nfunc CustodyNeedle() {}\n"), 200)
	for index := 0; index < 256; index++ {
		name := filepath.Join(repository, "fixture-"+threeDigits(index)+".go")
		if err := os.WriteFile(name, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runLauncherGit(t, repository, "add", ".")
	runLauncherGit(t, repository, "commit", "--quiet", "-m", "fixture")

	workspace := filepath.Join(root, "custody")
	supervision := beginLauncherCustody(t, workspace, "real-indexer")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary,
		"-index", index,
		"-incremental=false",
		"-submodules=false",
		"-file_limit=2097152",
		"-shard_limit=104857600",
		"-max_trigram_count=20000",
		repository,
	)
	command.Env = gitEnvironmentForContract(true)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := isolatePrivateServerSession(command); err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	cleanupLauncherCommand(t, command, "", supervision)
	if err := command.Process.Signal(syscall.SIGSTOP); err != nil {
		_ = command.Wait()
		t.Fatalf("stop exact indexer before proof: %v", err)
	}
	if err := supervision.Drain(""); !errors.Is(err, errCustodyDescendantsLive) {
		_ = command.Process.Signal(syscall.SIGCONT)
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("exact indexer did not retain the custody lease: %v", err)
	}
	if err := command.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := supervision.Drain(""); err != nil {
		t.Fatal(err)
	}
	finishLauncherCustody(t, supervision)
}

func testRealPhebsSurrealInheritance(t *testing.T, parent, phebs, zoekt string) {
	t.Helper()
	root := filepath.Join(parent, "phebs-surreal")
	profile, toolchain := realLauncherProfile(t, root, phebs, zoekt)
	workspace := filepath.Join(root, "custody")
	supervision := beginLauncherCustody(t, workspace, "real-phebs-surreal")
	server, err := launchPrivateServer(t.Context(), profile, toolchain, "hard-death")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.stop(5 * time.Second) })
	_ = waitForRealLauncherRuntime(t, profile.DataDir, 30*time.Second)
	if err := server.sampler.close(); err != nil {
		_ = server.stop(time.Second)
		t.Fatal(err)
	}
	if err := server.command.Process.Kill(); err != nil {
		_ = server.stop(time.Second)
		t.Fatal(err)
	}
	rootErr := waitLauncherServerRoot(t, server, 5*time.Second)
	if rootErr == nil {
		_ = server.stop(time.Second)
		t.Fatal("exact phebs root unexpectedly survived SIGKILL")
	}
	if err := supervision.Drain(""); !errors.Is(err, errCustodyDescendantsLive) {
		_ = server.stop(time.Second)
		t.Fatalf("phebs-launched Surreal did not retain the custody lease: %v", err)
	}
	// Stop the exact process session rather than signaling the persisted PID
	// again after the root has died; this avoids a PID-reuse cleanup window.
	_ = server.stop(5 * time.Second)
	drainLauncherCustody(t, supervision, 10*time.Second)
	finishLauncherCustody(t, supervision)
}

func testRealPhebsRecoveryInheritance(t *testing.T, parent, phebs, zoekt string) {
	t.Helper()
	root := filepath.Join(parent, "phebs-recovery")
	profile, toolchain := realLauncherProfile(t, root, phebs, zoekt)
	ctx := t.Context()
	server, err := launchPrivateServer(ctx, profile, toolchain, "recovery")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.stop(10 * time.Second) })
	startup, err := awaitPrivateServerHealth(
		ctx, server, profile, "recovery", 30*time.Second,
	)
	if err != nil {
		_ = server.stop(5 * time.Second)
		t.Fatalf("real recovery server startup stage=%s health=%s: %v",
			startup.LastStage, startup.LastHealthClass, err)
	}

	backup := filepath.Join(root, "backup")
	backupCtx, cancelBackup := context.WithTimeout(ctx, 60*time.Second)
	defer cancelBackup()
	runStoppedRealPhebsCommand(t, filepath.Join(root, "backup-custody"), "real-phebs-backup",
		exec.CommandContext(backupCtx, phebs,
			"backup", "-config", profile.Config, "-output", backup), toolchain)
	if err := server.stop(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(profile.DataDir, profile.DataDir+".prior"); err != nil {
		t.Fatal(err)
	}
	restoreCtx, cancelRestore := context.WithTimeout(ctx, 60*time.Second)
	defer cancelRestore()
	runStoppedRealPhebsCommand(t, filepath.Join(root, "restore-custody"), "real-phebs-restore",
		exec.CommandContext(restoreCtx, phebs,
			"restore", "-config", profile.Config, "-backup", backup), toolchain)
}

func runStoppedRealPhebsCommand(
	t *testing.T, workspace, label string, command *exec.Cmd, toolchain privateToolchain,
) {
	t.Helper()
	supervision := beginLauncherCustody(t, workspace, label)
	command.Env = executionEnvironmentForToolchain(toolchain)
	stderr := &boundedBuffer{remaining: maxHostVersionBytes}
	command.Stdout, command.Stderr = io.Discard, stderr
	if err := isolatePrivateServerSession(command); err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start %s: %v", label, err)
	}
	cleanupLauncherCommand(t, command, "", supervision)
	if err := command.Process.Signal(syscall.SIGSTOP); err != nil {
		_ = command.Wait()
		t.Fatalf("stop %s before proof: %v", label, err)
	}
	if err := supervision.Drain(""); !errors.Is(err, errCustodyDescendantsLive) {
		_ = command.Process.Signal(syscall.SIGCONT)
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("%s did not retain the custody lease: %v", label, err)
	}
	if err := command.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatalf("resume %s: %v", label, err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait for %s: %v: %s", label, err, strings.TrimSpace(stderr.String()))
	}
	if err := supervision.Drain(""); err != nil {
		t.Fatalf("drain %s: %v", label, err)
	}
	finishLauncherCustody(t, supervision)
}

func realLauncherProfile(
	t *testing.T, root, phebs, zoekt string,
) (PreparedProfile, privateToolchain) {
	t.Helper()
	profileRoot := filepath.Join(root, "profile")
	dataDir := filepath.Join(profileRoot, "data")
	tempDir := filepath.Join(root, "tmp")
	for _, directory := range []string{root, profileRoot, dataDir, tempDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	secure := false
	configuration, err := yaml.Marshal(config.Config{
		Server: config.Server{Addr: address, DataDir: dataDir},
		Auth: config.Auth{
			APIKey: "t4013-real-custody", CookieSecure: &secure,
		},
		Sync: config.Sync{PollInterval: "250ms", ResyncInterval: "0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(profileRoot, "phebs.yaml")
	if err := os.WriteFile(configPath, configuration, 0o600); err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(profileRoot, "api-key")
	if err := os.WriteFile(credentialPath, []byte("t4013-real-custody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := PreparedProfile{
		Config: configPath, Credential: credentialPath, DataDir: dataDir, Address: address,
	}
	surreal, err := store.FindSurrealBinary()
	if err != nil {
		t.Fatal(err)
	}
	toolchain := privateToolchain{
		Schema: privateToolchainSchema, Phebs: phebs, Zoekt: zoekt,
		TempDir: tempDir, ClosedEnvironment: true,
	}
	toolchain.host.surreal = boundExecutable{
		name: "surreal", path: surreal.Path, sha256: surreal.SHA256,
	}
	toolchain = bindLauncherToolchain(t, toolchain)
	return profile, toolchain
}

func waitForRealLauncherRuntime(t *testing.T, dataDir string, timeout time.Duration) store.LocalRuntime {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		runtime, err := store.ReadLocalRuntime(dataDir)
		if err == nil {
			return runtime
		}
		if time.Now().After(deadline) {
			t.Fatalf("wait for exact phebs Surreal runtime: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func buildRealLauncherTool(t *testing.T, moduleRoot, output, packagePath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", output, packagePath)
	command.Dir = moduleRoot
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := isolatePrivateServerSession(command); err != nil {
		t.Fatal(err)
	}
	runErr := command.Run()
	var sessionErr error
	if command.Process != nil {
		sessionErr = finishCustodyCommandSession(command.Process.Pid)
	}
	if runErr != nil || sessionErr != nil {
		t.Fatalf("build exact %s: %v", packagePath, errors.Join(runErr, sessionErr))
	}
}

func requireRealSurrealLauncher(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary is not installed")
	}
}

func cleanupLauncherCommand(
	t *testing.T, command *exec.Cmd, release string, supervision *custodySupervision,
) {
	t.Helper()
	t.Cleanup(func() {
		if release != "" {
			_ = os.WriteFile(release, []byte("release\n"), 0o600)
		}
		if command != nil && command.Process != nil {
			_ = command.Process.Signal(syscall.SIGCONT)
			if command.SysProcAttr != nil && command.SysProcAttr.Setsid {
				_ = killPrivateServerSession(command.Process.Pid)
			}
			_ = command.Process.Kill()
			waited := make(chan struct{})
			go func() {
				_ = command.Wait()
				close(waited)
			}()
			select {
			case <-waited:
			case <-time.After(5 * time.Second):
				t.Errorf("launcher root survived test cleanup")
			}
		}
		if supervision == nil || supervision.controller == nil ||
			supervision.state.Phase != custodyPhaseLive {
			return
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			err := supervision.Drain("")
			if err == nil {
				return
			}
			if !errors.Is(err, errCustodyDescendantsLive) || time.Now().After(deadline) {
				t.Errorf("cleanup launcher descendants: %v", err)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

func waitLauncherServerRoot(t *testing.T, server *privateServer, timeout time.Duration) error {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-server.done:
		server.done <- err
		return err
	case <-timer.C:
		t.Fatal("launcher server root did not exit before its deadline")
		return nil
	}
}

func drainLauncherCustody(t *testing.T, supervision *custodySupervision, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := supervision.Drain("")
		if err == nil {
			return
		}
		if !errors.Is(err, errCustodyDescendantsLive) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestV25CustodyLauncherDescendantHelper(t *testing.T) {
	mode := os.Getenv(launcherDescendantModeEnv)
	if mode == "" {
		return
	}
	leasePath := os.Getenv("T4013_CUSTODY_LEASE")
	var expected unix.Stat_t
	if leasePath == "" || unix.Stat(leasePath, &expected) != nil {
		t.Fatal("custody lease authority is unavailable")
	}
	descriptor, err := strconv.Atoi(os.Getenv("T4013_CUSTODY_LEASE_FD"))
	if err != nil || descriptor < custodyMinimumFD {
		t.Fatal("custody lease descriptor identity is invalid")
	}
	var observed unix.Stat_t
	if unix.Fstat(descriptor, &observed) != nil ||
		observed.Dev != expected.Dev || observed.Ino != expected.Ino {
		t.Fatal("custody lease was not inherited by the launcher descendant")
	}
	if err := os.WriteFile(os.Getenv("T4013_LAUNCHER_READY"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := waitForCustodyFile(os.Getenv("T4013_LAUNCHER_RELEASE"), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "wait":
	case "backup":
		if err := os.Mkdir(os.Getenv("T4013_LAUNCHER_OUTPUT"), 0o700); err != nil {
			t.Fatal(err)
		}
	case "restore":
		if err := os.Mkdir(os.Getenv("T4013_LAUNCHER_DATA"), 0o700); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("custody launcher mode is invalid")
	}
}

func threeDigits(value int) string {
	return string([]byte{'0' + byte(value/100), '0' + byte(value/10%10), '0' + byte(value%10)})
}

func beginLauncherCustody(t *testing.T, workspace, label string) *custodySupervision {
	t.Helper()
	supervision, err := beginPrepareCustody(
		workspace, digest([]byte(label)), mustCustodyToken(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = supervision.Close() })
	return supervision
}

func finishLauncherCustody(t *testing.T, supervision *custodySupervision) {
	t.Helper()
	if err := supervision.BeginFinalization(""); err != nil {
		t.Fatal(err)
	}
	if err := supervision.DrainTerminal(); err != nil {
		t.Fatal(err)
	}
	if err := supervision.Retire(); err != nil {
		t.Fatal(err)
	}
}

func runLauncherGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...)
	stdout := &boundedBuffer{remaining: maxHostVersionBytes}
	stderr := &boundedBuffer{remaining: maxHostVersionBytes}
	command.Stdout, command.Stderr = stdout, stderr
	command.Env = gitEnvironmentForContract(true)
	if err := isolatePrivateServerSession(command); err != nil {
		t.Fatal(err)
	}
	runErr := command.Run()
	var sessionErr error
	if command.Process != nil {
		sessionErr = finishCustodyCommandSession(command.Process.Pid)
	}
	if runErr != nil || sessionErr != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "),
			errors.Join(runErr, sessionErr), stderr.String())
	}
	return stdout.String()
}

func writeLauncherScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
}
