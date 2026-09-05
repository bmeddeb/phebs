//go:build darwin

package t421

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestExecutionAuthorCustodyNativeOwnership(t *testing.T) {
	for _, mode := range []string{"intact", "source-replaced", "home-symlink", "lease-replaced"} {
		t.Run(mode, func(t *testing.T) {
			parent, _ := inputCustodyTestFixture(t)
			custody := &ExecutionAuthorCustody{parent: parent}
			t.Cleanup(func() { custody.active = false; _ = custody.Close() })
			if err := custody.createRoots(t.Context()); err != nil || custody.checkRoots(t.Context()) != nil {
				t.Fatalf("native parent roots: %v", err)
			}
			if len(custody.PrivateDirectories()) != 3 || custody.Directory() != custody.roots[1].path || custody.identity.Inode == 0 {
				t.Fatal("private source/home/tmp custody is incomplete")
			}
			if err := custody.checkSource(t.Context(), nil); err != nil {
				t.Fatal("fresh source is not empty", err)
			}
			observed, err := ObserveExecutionCorpusSource(t.Context(), custody.Directory())
			if err != nil || observed.Identity != custody.identity || observed.ConfigSHA256 != "" {
				t.Fatal("held source does not bind actual native identity")
			}
			if other, err := acquireProductionSourceLease(parent); other != nil || err == nil {
				if other != nil {
					_ = other.Close()
				}
				t.Fatal("second source owner acquired a held lease")
			}
			switch mode {
			case "source-replaced":
				path := custody.Directory()
				if os.Rename(path, path+"-retained") != nil || os.Mkdir(path, 0o700) != nil {
					t.Fatal("replace exact empty test root")
				}
			case "home-symlink":
				path := custody.roots[2].path
				if os.Rename(path, path+"-retained") != nil || os.Symlink(path+"-retained", path) != nil {
					t.Fatal("alias exact empty home root")
				}
			case "lease-replaced":
				path := filepath.Join(parent, productionSourceLeaseName)
				if os.Rename(path, path+"-retained") != nil || os.WriteFile(path, nil, 0o600) != nil {
					t.Fatal("replace exact empty test lease")
				}
			}
			if err := custody.checkRoots(t.Context()); (err == nil) != (mode == "intact") {
				t.Fatalf("native continuity outcome: %v", err)
			}
			custody.active = true
			if custody.Close() == nil {
				t.Fatal("active child custody was released")
			}
			for _, root := range custody.roots {
				if _, err := root.file.Stat(); err != nil {
					t.Fatal("active refusal closed a root")
				}
			}
			custody.active = false
			if err := custody.Close(); err != nil {
				t.Fatal(err)
			}
			for _, root := range custody.roots {
				if _, err := root.file.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatal("joined Close left an owned descriptor open")
				}
				if _, err := os.Lstat(root.path); err != nil {
					t.Fatal("Close deleted private custody")
				}
			}
			if _, err := custody.lease.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("joined Close kept its lease descriptor")
			}
		})
	}
}

func TestExecutionAuthorCustodyProtectedPlanRefusals(t *testing.T) {
	for _, mode := range []string{"bounded-input", "executable", "empty", "oversized", "closed"} {
		t.Run(mode, func(t *testing.T) {
			parent, selected := inputCustodyTestFixture(t)
			selected.Name = "plan"
			raw := []byte("neutral protected input fixture, not a canonical frozen plan\n")
			switch mode {
			case "empty":
				raw = nil
			case "oversized":
				raw = bytes.Repeat([]byte{'x'}, MaxPlanBytes+1)
			}
			if err := os.WriteFile(selected.Path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			selected.SHA256, selected.Executable = SHA256(raw), mode == "executable"
			if selected.Executable {
				if err := os.Chmod(selected.Path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			inputs, err := inputCustodyTestProtect(t, t.Context(), parent, []ExecutionInputCopy{selected})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "closed" {
				_ = inputs.Close()
			}
			path, observed, err := readAuthorCustodyPlan(t.Context(), inputs)
			if (err == nil) != (mode == "bounded-input") {
				t.Fatalf("protected input read: %v", err)
			}
			if err == nil {
				if !bytes.Equal(observed, raw) || path != filepath.Join(inputs.Directory(), "plan") {
					t.Fatal("protected bytes changed")
				}
				custody := &ExecutionAuthorCustody{request: ExecutionAuthorRequest{Plan: inputs}}
				if custody.Close() != nil {
					t.Fatal("empty owner close")
				}
				if _, err := inputs.Check(t.Context(), "plan"); err != nil {
					t.Fatal("author closed a borrowed protected input")
				}
			}
		})
	}
}

func TestExecutionAuthorCustodyPlanSourceObservation(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	commits, err := InspectExecutionCheckout(t.Context(), fixture.root, fixture.git, fixture.plan, fixture.integration, fixture.source)
	if err != nil || fixture.plan == fixture.source || fixture.integration == fixture.source {
		t.Fatal("actual ordered checkout fixture", err)
	}
	// This private unit fixture exercises the binding projection using actual
	// inspected identities. It is not a protected SDK or reference-tool issuer.
	builds := &ExecutionGoBuildCustody{planSource: fixture.plan, commits: commits, reference: executionReferenceSource{source: fixture.source}}
	if !authorCustodyBuildBinding(builds, fixture.plan) || authorCustodyBuildBinding(builds, fixture.source) {
		t.Fatal("ordered plan/integration/execution commits collapsed to equality")
	}
	builds.planSource = ""
	if authorCustodyBuildBinding(builds, fixture.plan) {
		t.Fatal("missing actual plan-source observation admitted")
	}
}

func TestExecutionAuthorCustodyNoCallerAuthority(t *testing.T) {
	parent, _ := inputCustodyTestFixture(t)
	for _, request := range []ExecutionAuthorRequest{{}, {Git: &ExecutionGitCustody{}},
		{Git: &ExecutionGitCustody{}, Builds: &ExecutionGoBuildCustody{}, Author: &ExecutionToolCustody{}, Plan: &ExecutionInputCustody{}}} {
		custody, err := PrepareExecutionAuthor(t.Context(), parent, request)
		if custody != nil || !errors.Is(err, ErrExecutionAuthorCustody) {
			t.Fatal("caller-created zero/ordinary handles issued author custody")
		}
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("refused authority created mutable custody")
	}
}

func TestExecutionAuthorCustodyExactGitBudgets(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
	_, path, err := git.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	environment, err := git.Environment(t.Context(), parent, parent)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []uint64{4, 3, 3} {
		config, control := authorCustodyConfig(index, [32]byte{19})
		if config.Limits.Attempts != want || config.Limits.ActivePerProducer != 1 || config.Limits.WireBytes != (2*want+3)*128 || control.MaximumWireBytes != 384 {
			t.Fatal("author flow budget differs from exact recipe")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		controller, err := dispatchadmission.New(ctx, config)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		parentPipe, childPipe, err := dispatchadmission.NewPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		client, err := dispatchadmission.NewClient(ctx, childPipe, config.Producers[0], 1, config.Limits)
		if err != nil {
			_ = parentPipe.Close()
			cancel()
			t.Fatal(err)
		}
		served := make(chan error, 1)
		go func() {
			served <- controller.ServeChecked(ctx, 1, os.Getpid(), parentPipe, func(ctx context.Context, _ dispatchadmission.Site) error {
				_, _, err := git.Check(ctx)
				return err
			})
		}()
		// Real protected Git version commands test only the local controller
		// recipe's bounds, not author work or full-population execution.
		for range want {
			command := exec.CommandContext(ctx, path, "--version")
			command.Env, command.Stdout, command.Stderr = environment, io.Discard, io.Discard
			if err := client.Run(ctx, dispatchadmission.SiteCorpusAuthorGit, command); err != nil {
				cancel()
				t.Fatal(err)
			}
		}
		if client.Pause(ctx) != nil || controller.Fence() != nil || client.Checkpoint(ctx) != nil || client.Close(ctx) != nil {
			cancel()
			t.Fatal("bounded controller recipe did not checkpoint/close")
		}
		if err := <-served; err != nil {
			cancel()
			t.Fatal(err)
		}
		snapshot, err := controller.Snapshot()
		cancel()
		if err != nil || !snapshot.Complete || snapshot.Attempts != want || snapshot.Producers[0].Active != 0 {
			t.Fatal("committed author budget prefix differs")
		}
	}
}

func TestExecutionAuthorCustodyActualWrongProgramJoined(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
	_, path, err := git.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Intentionally bypass the public constructor only to run its real owned
	// transport's negative path. Protected Git is NOT a verified author image.
	// No successful author admission or CLI claim follows from this test.
	custody := &ExecutionAuthorCustody{parent: parent, authorPath: path}
	if err := custody.createRoots(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = custody.Close() })
	environment, err := git.Environment(t.Context(), custody.roots[2].path, custody.roots[3].path)
	if err != nil {
		t.Fatal(err)
	}
	custody.environment = append(environment, dispatchadmission.ProductionEnvironment+"="+dispatchadmission.ProductionSelector)
	custody.tools = []dispatchadmission.ProductionToolBinding{{Role: "git", Path: path, Environment: environment}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := custody.runAuthor(ctx, cancel, 0, []byte("not an author request\n"), ExecutionAuthorResult{Revision: "a"})
	if err == nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || result.Completed || result.Response != nil || result.Accounting.Attempts != 0 {
		t.Fatalf("actual wrong-program cleanup facts: %+v, %v", result, err)
	}
	if _, err := os.Stat(custody.Directory()); err != nil {
		t.Fatal("failed native start path discarded source custody")
	}
}

func TestExecutionAuthorCustodySocketAdoption(t *testing.T) {
	for _, mode := range []string{"socket", "anonymous-pipe", "regular", "closed"} {
		t.Run(mode, func(t *testing.T) {
			var file, peer *os.File
			var err error
			switch mode {
			case "socket", "closed":
				file, peer, err = dispatchadmission.NewPipe()
			case "anonymous-pipe":
				file, peer, err = os.Pipe()
			case "regular":
				file, err = os.CreateTemp(t.TempDir(), "not-a-socket")
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = file.Close()
				if peer != nil {
					_ = peer.Close()
				}
			})
			if mode == "closed" {
				_ = file.Close()
			}
			connection, err := adoptAuthorCustodySocket(file)
			if (err == nil) != (mode == "socket") {
				t.Fatalf("adoption %s: %v", mode, err)
			}
			if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("adoption retained original descriptor", err)
			}
			if connection == nil {
				return
			}
			defer func() { _ = connection.Close() }()
			peerSocket, err := adoptAuthorCustodySocket(peer)
			if err != nil {
				t.Fatal("adopt test peer", err)
			}
			defer func() { _ = peerSocket.Close() }()
			if connection.SetReadDeadline(time.Now().Add(time.Second)) != nil ||
				connection.SetWriteDeadline(time.Now().Add(time.Second)) != nil {
				t.Fatal("adopted socket is not pollable")
			}
			if err := writeAuthorCustodyRequest(t.Context(), connection, []byte("bounded request")); err != nil ||
				peerSocket.SetReadDeadline(time.Now().Add(time.Second)) != nil {
				t.Fatal("send actual parent request", err)
			}
			if raw, err := io.ReadAll(peerSocket); err != nil || string(raw) != "bounded request" {
				t.Fatalf("parent request EOF: %q / %v", raw, err)
			}
			if _, err := peerSocket.Write([]byte("exact input")); err != nil || peerSocket.CloseWrite() != nil {
				t.Fatal("write actual socket peer", err)
			}
			if raw, err := io.ReadAll(connection); err != nil || string(raw) != "exact input" {
				t.Fatalf("bounded socket EOF: %q / %v", raw, err)
			}
			if err := readAuthorCustodyEOF(t.Context(), connection, bufio.NewReader(connection)); err != nil {
				t.Fatal("actual parent terminal EOF", err)
			}
		})
	}
}

func TestExecutionAuthorCustodyCanceledDeadlineResetDoesNotAdmitIO(t *testing.T) {
	for _, operation := range []string{"request-write", "terminal-read"} {
		t.Run(operation, func(t *testing.T) {
			parent, child, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			connection, err := adoptAuthorCustodySocket(parent)
			if err != nil {
				_ = child.Close()
				t.Fatal(err)
			}
			defer func() { _ = connection.Close(); _ = child.Close() }()
			peerSocket, err := adoptAuthorCustodySocket(child)
			if err != nil {
				t.Fatal("adopt test peer", err)
			}
			defer func() { _ = peerSocket.Close() }()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reader := bufio.NewReader(connection)
			if operation == "terminal-read" {
				if _, err := peerSocket.Write([]byte{'x'}); err != nil {
					t.Fatal(err)
				}
				if _, err := reader.Peek(1); err != nil {
					t.Fatal(err)
				}
			}
			// Deterministically place cancellation before the production future
			// deadline setter, as when its AfterFunc already unblocked the socket.
			cancel()
			if connection.SetDeadline(time.Now()) != nil {
				t.Fatal("set canceled deadline")
			}
			if operation == "request-write" {
				if !errors.Is(writeAuthorCustodyRequest(ctx, connection, []byte("must not be sent")), ErrExecutionAuthorCustody) {
					t.Fatal("canceled request write passed")
				}
				if peerSocket.SetReadDeadline(time.Now().Add(time.Second)) != nil {
					t.Fatal("peer deadline")
				}
				if raw, err := io.ReadAll(peerSocket); err != nil || len(raw) != 0 {
					t.Fatalf("canceled request sent bytes before EOF: %q / %v", raw, err)
				}
			} else {
				if !errors.Is(readAuthorCustodyEOF(ctx, connection, reader), ErrExecutionAuthorCustody) || reader.Buffered() != 1 {
					t.Fatal("canceled EOF operation consumed input after resetting the deadline")
				}
			}
		})
	}
}

func TestExecutionAuthorCustodyRetainedPrefixAndResponse(t *testing.T) {
	result := ExecutionAuthorResult{Revision: "b", RootStarted: true, Accounting: dispatchadmission.Snapshot{
		Attempts: 2, Phases: []dispatchadmission.PhaseCount{{Phase: 1, Attempts: 2, Roles: []dispatchadmission.RoleCount{{Role: 1, Attempts: 2}}}},
		Producers: []dispatchadmission.ProducerCount{{Producer: 1, Active: 1}}}}
	custody := &ExecutionAuthorCustody{err: ErrExecutionAuthorCustody, results: []ExecutionAuthorResult{result}}
	copy := custody.Results()
	copy[0].Accounting.Phases[0].Roles[0].Attempts = 99
	copy[0].Accounting.Producers[0].Active = 0
	if got := custody.Results()[0]; got.Accounting.Attempts != 2 || got.Accounting.Phases[0].Roles[0].Attempts != 2 || got.Accounting.Producers[0].Active != 1 {
		t.Fatal("caller mutation rewrote stopped prefix")
	}
	if _, err := custody.AuthorNext(t.Context()); err == nil || len(custody.Results()) != 1 {
		t.Fatal("failed author retried or discarded its prefix")
	}
	expected := AuthoredExecutionRevision{Name: "a", Commit: strings.Repeat("a", 40)}
	response := ExecutionCorpusAuthorResponse{Result: expected, ConfigSHA256: SHA256([]byte("config"))}
	raw, err := corpusAuthorCanonical(response, MaxExecutionCorpusAuthorResponseBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := authorCustodyCanonicalResponse(raw, expected); err != nil || got != response {
		t.Fatal("actual canonical response projection changed")
	}
	for _, malformed := range [][]byte{append(bytes.Clone(raw), '\n'), bytes.Replace(raw, []byte(`"name":"a"`), []byte(`"name":"b"`), 1), []byte("{}\n")} {
		if _, err := authorCustodyCanonicalResponse(malformed, expected); err == nil {
			t.Fatal("changed/noncanonical author response admitted")
		}
	}
}
