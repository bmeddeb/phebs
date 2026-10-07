//go:build linux

package t421

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func TestLinuxToolCustodyRealSurreal(t *testing.T) {
	// Test selection uses the prepared host PATH; the production API receives
	// the explicit selected path and performs no discovery. Missing tools fail.
	binary, err := exec.LookPath("surreal")
	if err != nil {
		t.Fatal("required prepared-host SurrealDB is missing", err)
	}
	for key, value := range map[string]string{"HOME": filepath.Join(t.TempDir(), "missing-home"), "GOFLAGS": "-invalid-ambient", "GIT_CONFIG_COUNT": "invalid"} {
		t.Setenv(key, value)
	}
	before := linuxInputFDCount(t)
	tool, err := ProtectLinuxExecutionExternalTool(t.Context(), "surreal", binary)
	if err != nil {
		t.Fatal("real sealed SurrealDB probe must pass without skip", err)
	}
	t.Cleanup(func() { _ = tool.Close() })
	identity, err := tool.Check(t.Context(), "surreal")
	if err != nil || identity.Role != "surreal" || identity.FileType != regularFileType || !validExecutionSHA256(identity.SHA256) || !strings.HasPrefix(identity.Version, "3.") || identity.Provenance != "external-sealed-file-linux-amd64-v1" {
		t.Fatal("observed identity", identity, err)
	}
	identity.Version = "caller-mutated"
	again, err := tool.Check(t.Context(), "surreal")
	if err != nil || again.Version == identity.Version || linuxInputFDCount(t) != before+1 {
		t.Fatal("identity/owned keeper drift", err)
	}
	if err := tool.Close(); err != nil || linuxInputFDCount(t) != before {
		t.Fatal("real tool cleanup", err)
	}
	if _, err := tool.Check(t.Context(), "surreal"); err == nil {
		t.Fatal("closed tool accepted")
	}
}

func TestLinuxToolCustodyRetainsProbeUntilJoinedAndDrained(t *testing.T) {
	for _, joined := range []bool{false, true} {
		t.Run(map[bool]string{false: "unjoined", true: "joined-with-descendant"}[joined], func(t *testing.T) {
			before := linuxInputFDCount(t)
			tool, err := protectLinuxExecutionTool(t.Context(), "phebs", "/usr/bin/sleep")
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(t.TempDir(), "probe")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			tool.probe = &linuxExecutionToolProbe{directory: directory, joined: joined}
			if joined {
				command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestExecutionProcessSessionHelper$")
				command.Env = []string{"PHEBS_EXECUTION_SESSION_HELPER=exit-root", "GORACE=atexit_sleep_ms=0"}
				command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				command.Stdin = bytes.NewReader([]byte{1})
				var output bytes.Buffer
				command.Stdout, command.Stderr, command.WaitDelay = &output, os.Stderr, time.Second
				if err := command.Run(); err != nil {
					t.Fatal(err)
				}
				if output.Len() != 8 || command.ProcessState == nil {
					t.Fatal("descendant/root-join fixture missing")
				}
				tool.probe.pid = command.Process.Pid
				t.Cleanup(func() {
					_ = t4013.KillPrivateProcessSession(command.Process.Pid)
					_ = t4013.WaitPrivateProcessSession(command.Process.Pid, time.Now().Add(6*time.Second))
				})
			}
			retained, err := finishLinuxExecutionTool(t.Context(), tool, "phebs", ExecutionToolIdentity{}, ErrExecutionToolCustody)
			if !errors.Is(err, ErrExecutionToolCustody) || retained != tool || tool.PrivateProbeDirectory() != directory || linuxInputFDCount(t) != before+1 {
				t.Fatal("uncertain probe released custody", err)
			}
			if _, err := os.Stat(directory); err != nil {
				t.Fatal("uncertain probe directory removed", err)
			}
			if joined {
				if err := t4013.KillPrivateProcessSession(tool.probe.pid); err != nil {
					t.Fatal(err)
				}
				if err := t4013.WaitPrivateProcessSession(tool.probe.pid, time.Now().Add(6*time.Second)); err != nil {
					t.Fatal(err)
				}
			} else {
				tool.probe.joined = true
			}
			_ = tool.Close() // Sticky refusal remains even after successful cleanup.
			if tool.PrivateProbeDirectory() != "" || linuxInputFDCount(t) != before {
				t.Fatal("drained probe failed to release custody")
			}
			if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("drained probe directory survived", err)
			}
		})
	}
}

func TestLinuxToolCustodyExternalRefusalsAndCleanup(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name, role, binary string
		ctx                context.Context
	}{
		{"wrong-version", "surreal", goBinary, t.Context()},
		{"git", "git", goBinary, t.Context()}, {"go", "go", goBinary, t.Context()}, {"system", "ssh-keygen", goBinary, t.Context()},
		{"missing", "surreal", filepath.Join(t.TempDir(), "missing"), t.Context()}, {"relative", "surreal", "surreal", t.Context()},
		{"nil-context", "surreal", goBinary, nil}, {"canceled", "surreal", goBinary, ctx},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := linuxInputFDCount(t)
			tool, err := ProtectLinuxExecutionExternalTool(test.ctx, test.role, test.binary)
			if !errors.Is(err, ErrExecutionToolCustody) || tool != nil || linuxInputFDCount(t) != before {
				t.Fatal("external refusal or cleanup", tool, err)
			}
		})
	}
	if tool, err := ProtectLinuxExecutionReferenceTool(t.Context(), ReferenceToolRequest{Role: "git"}); tool != nil || err == nil {
		t.Fatal("unsupported reference role accepted")
	}
}

func TestLinuxToolCustodyClosedProbeRefusals(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), goBinary, "env", "GOROOT")
	command.Env = []string{"GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "PATH=/usr/bin:/bin", "LC_ALL=C"}
	rawRoot, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	goRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(rawRoot)))
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module neutral.invalid/probe\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := `package main
import("fmt";"os";"strings";"time")
var mode, marker string
func main(){
 if len(os.Args)!=2 || os.Args[1]!="version" { os.Exit(7) }
 if err:=os.WriteFile(marker,[]byte{1},0600);err!=nil { os.Exit(8) }
 switch mode {
 case "version": fmt.Println("2.0.0")
 case "source": fmt.Println("3.2.0 /private/source")
 case "stderr": fmt.Fprintln(os.Stderr,"diagnostic");fmt.Println("3.2.0")
 case "overflow": fmt.Print(strings.Repeat("x",5000))
 case "canceled": fmt.Println("3.2.0");for { time.Sleep(time.Second) }
 }
}`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "tmp", "cache", "bin"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	request := ReferenceToolRequest{GoRoot: goRoot, ModuleCache: t.TempDir()}
	for _, mode := range []string{"version", "source", "stderr", "overflow", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			binary, marker := filepath.Join(root, "image-"+mode), filepath.Join(root, "marker-"+mode)
			if _, err := runReferenceGo(t.Context(), root, filepath.Join(goRoot, "bin", "go"), referenceBuildEnvironment(request, root), 64<<10,
				"build", "-trimpath", "-buildvcs=false", "-p=1", "-ldflags=-X main.mode="+mode+" -X main.marker="+marker, "-o", binary, "."); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if mode == "canceled" {
				timed, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				ctx = timed
			}
			before := linuxInputFDCount(t)
			tool, err := ProtectLinuxExecutionExternalTool(ctx, "surreal", binary)
			if tool != nil || !errors.Is(err, ErrExecutionToolCustody) || linuxInputFDCount(t) != before {
				t.Fatal("closed probe refusal/cleanup", tool, err)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("native version fixture was not executed", err)
			}
			if mode == "canceled" && ctx.Err() != context.DeadlineExceeded {
				t.Fatal("cancellation fixture missed deadline")
			}
		})
	}
}

func TestLinuxToolCustodyReferenceSealedExactBuild(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	fixture.write(t, "go.mod", "module github.com/bmeddeb/phebs\n\ngo 1.26\n")
	fixture.write(t, "go.sum", "")
	fixture.write(t, ".gitignore", "/ignored/\n/cmd/phebs-focused-index/injected.go\n")
	fixture.write(t, "cmd/phebs-focused-index/main.go", "package main\nvar message=\"exact\"\nfunc main(){println(message)}\n")
	fixture.command(t, "add", "go.mod", "go.sum", ".gitignore", "cmd/phebs-focused-index/main.go")
	fixture.source = fixture.commit(t, "neutral sealed reference command")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goCommand := exec.CommandContext(t.Context(), goBinary, "env", "GOROOT")
	goCommand.Dir = t.TempDir()
	goCommand.Env = []string{"GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "PATH=/usr/bin:/bin", "LC_ALL=C"}
	rawRoot, err := goCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	goRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(rawRoot)))
	if err != nil {
		t.Fatal(err)
	}
	moduleCache, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := ReferenceToolRequest{RepositoryRoot: fixture.root, GitBinary: fixture.git, GoRoot: goRoot, ModuleCache: moduleCache,
		PlanSourceCommit: fixture.plan, IntegratedMainCommit: fixture.integration, SourceCommit: fixture.source, Role: "phebs-focused-index"}
	workspace := newReferenceToolBuildWorkspace(t, request)
	request.Binary = filepath.Join(workspace, "supplied")
	buildReferenceToolFixture(t, request, workspace)
	before := linuxInputFDCount(t)
	tool, err := ProtectLinuxExecutionReferenceTool(t.Context(), request)
	if err != nil {
		t.Fatal("sealed exact independent reference build refused", err)
	}
	t.Cleanup(func() { _ = tool.Close() })
	identity, err := tool.Check(t.Context(), request.Role)
	if err != nil || identity.BuildVCSRevision != fixture.source || identity.Provenance != "go-build-info-vcs-v1" {
		t.Fatal("reference identity", identity, err)
	}
	// The supplied pathname is no longer authority after the sealed copy exists.
	if err := os.Remove(request.Binary); err != nil {
		t.Fatal(err)
	}
	if err := tool.WithImage(t.Context(), request.Role, func(identity ExecutionToolIdentity, file *os.File) error {
		digest, err := digestExecutionReferenceFD(t.Context(), file)
		if err != nil || digest != identity.SHA256 {
			t.Fatal("held bytes changed after source removal", err)
		}
		command := exec.CommandContext(t.Context(), "/proc/self/fd/3")
		command.ExtraFiles = []*os.File{file}
		command.Dir, command.Env = workspace, externalToolEnvironment(workspace)
		return runReferenceCommand(t.Context(), command)
	}); err != nil {
		t.Fatal(err)
	}
	if err := tool.Close(); err != nil || linuxInputFDCount(t) != before {
		t.Fatal("reference cleanup", err)
	}
	// Matching Go build metadata cannot replace exact independent byte equality.
	fixture.write(t, "cmd/phebs-focused-index/injected.go", "package main\nfunc init(){message=\"injected\"}\n")
	buildReferenceToolFixture(t, request, workspace)
	if err := os.Remove(filepath.Join(fixture.root, "cmd/phebs-focused-index/injected.go")); err != nil {
		t.Fatal(err)
	}
	if rejected, err := ProtectLinuxExecutionReferenceTool(t.Context(), request); rejected != nil || !errors.Is(err, ErrExecutionToolCustody) || linuxInputFDCount(t) != before {
		t.Fatal("metadata-identical lie or cleanup", rejected, err)
	}
}

func TestLinuxToolCustodyScopedMatchAndStickyRefusal(t *testing.T) {
	// This fixture installs no provenance. Only the private constructor is used
	// to exercise FD dispatch/match; public APIs never accept this test identity.
	tool, err := protectLinuxExecutionTool(t.Context(), "phebs", "/usr/bin/sleep")
	if err != nil {
		t.Fatal(err)
	}
	tool.identity = ExecutionToolIdentity{Role: "phebs", FileType: regularFileType, SHA256: tool.input.inputs["phebs"].identity.SHA256}
	t.Cleanup(func() { _ = tool.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := tool.WithImage(ctx, "phebs", func(_ ExecutionToolIdentity, image *os.File) error {
		child := exec.CommandContext(ctx, "/proc/self/fd/3", "30")
		child.ExtraFiles = []*os.File{image}
		if err := child.Start(); err != nil {
			return err
		}
		defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
		if err := t4013.MatchLinuxProcessExecutableImage(ctx, child.Process.Pid, image); err != nil {
			return err
		}
		if _, err := t4013.ObserveProcessExecutablePath(ctx, child.Process.Pid); err == nil {
			t.Fatal("deleted path observer relaxed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Check(t.Context(), "wrong-role"); !errors.Is(err, ErrExecutionToolCustody) {
		t.Fatal("wrong role accepted")
	}
	if _, err := tool.Check(t.Context(), "phebs"); err == nil {
		t.Fatal("refusal did not latch")
	}
}
