//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

func linuxAuthorTestClient(ctx context.Context, attempts uint64) (*dispatchadmission.Client, *dispatchadmission.Controller, <-chan error, error) {
	limits := dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1, ActivePerProducer: 1, Attempts: attempts, WireBytes: 16 << 10, AckTimeout: 2 * time.Second}
	producer := dispatchadmission.Producer{ID: 1, Binding: [32]byte{31}, Sites: dispatchadmission.AuthorSites()}
	controller, err := dispatchadmission.New(ctx, dispatchadmission.Config{Limits: limits, Producers: []dispatchadmission.Producer{producer}, Phases: []dispatchadmission.Phase{{ID: 1, Roles: []dispatchadmission.RoleBudget{{Role: dispatchadmission.RoleGit, Attempts: attempts}}}}})
	if err != nil {
		return nil, nil, nil, err
	}
	server, child, err := dispatchadmission.NewPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	client, err := dispatchadmission.NewClient(ctx, child, producer, 1, limits)
	if err != nil {
		_ = server.Close()
		return nil, nil, nil, err
	}
	served := make(chan error, 1)
	go func() {
		served <- controller.ServeChecked(ctx, 1, os.Getpid(), server, func(context.Context, dispatchadmission.Site) error { return nil })
	}()
	return client, controller, served, nil
}

// Tiny in-process fixtures exercise the shared engine and real metered Git,
// not the public constructor/CLI or the full frozen population qualification.
func TestLinuxAuthorThreeEngineLifetimesExactGit(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	if os.Mkdir(parent, 0o700) != nil {
		t.Fatal("private parent fixture")
	}
	owner := &LinuxExecutionAuthorCustody{}
	if owner.createRoots(t.Context(), parent) != nil {
		t.Fatal("native fixture roots")
	}
	defer func() { _ = owner.Close(context.Background()) }()
	source := corpusAuthorTestSource(t)
	var previous *ExecutionCorpusAuthorResponse
	for index, name := range []string{"a", "b", "a-return"} {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		client, controller, served, err := linuxAuthorTestClient(ctx, authorCustodyAttempts(index))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		author, err := openLinuxAuthorRoot(ctx, owner.Directory(), owner.roots[1].identity)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		start := func(ctx context.Context, cmd *exec.Cmd) (dispatchadmission.Handle, error) {
			cmd.Env = executionGitEnvironment("/usr/bin", owner.roots[2].path, owner.roots[3].path)
			return client.Start(ctx, dispatchadmission.SiteCorpusAuthorGit, cmd)
		}
		author, err = resumeCorpusAuthorState(ctx, author, source, name, previous, true, "/usr/bin/git", start)
		if err != nil {
			cancel()
			t.Fatal("resume current revision", name, err)
		}
		raw, err := author.AuthorRequested(ctx)
		if closeErr := author.Close(); err == nil {
			err = closeErr
		}
		closeErr := client.Close(ctx)
		fenceErr := controller.Fence()
		serveErr := <-served
		snapshot, snapshotErr := controller.Snapshot()
		cancel()
		if err != nil || closeErr != nil || fenceErr != nil || serveErr != nil || snapshotErr != nil || !snapshot.Complete || !authorCustodyProducerComplete(snapshot, 1, authorCustodyAttempts(index)) {
			t.Fatal("actual Git progression/accounting", name, err, closeErr, fenceErr, serveErr, snapshotErr)
		}
		physical := source.revisions[index]
		parentCommit := ""
		if index > 0 {
			parentCommit = source.revisions[index-1].ExpectedCommit
		}
		commitRaw, e := canonicalGitCommitBytesFor(physical.ExpectedTree, parentCommit, physical.CommitMessage, source.recipe)
		if e != nil {
			t.Fatal(e)
		}
		expected := AuthoredExecutionRevision{Name: name, Commit: physical.ExpectedCommit, Tree: physical.ExpectedTree, ParentCommit: parentCommit, Manifest: source.manifest(physical, physical.ExpectedTreeInventory, SHA256(commitRaw))}
		response, e := authorCustodyCanonicalResponseFor(raw, expected, true)
		if e != nil {
			t.Fatal("canonical actual response", name, e)
		}
		previous = &response
	}
	if owner.checkSource(t.Context(), previous) != nil {
		t.Fatal("final native continuity")
	}
}

func TestLinuxAuthorGitCancellationBeforePin(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	lifetime, release := context.WithTimeout(t.Context(), 10*time.Second)
	defer release()
	client, controller, served, err := linuxAuthorTestClient(lifetime, 1)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxAuthorGitWaitingChild$")
	command.Env = []string{"PHEBS_T42_AUTHOR_WAITING_CHILD=1", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	state := &linuxCorpusAuthorState{}
	if state.prepare(ctx, command) != nil {
		t.Fatal("prepare")
	}
	handle, err := state.start(ctx, command, func(ctx context.Context, command *exec.Cmd) (dispatchadmission.Handle, error) {
		handle, err := client.Start(ctx, dispatchadmission.SiteCorpusAuthorGit, command)
		if err == nil {
			cancel()
		} // Native Start happened; no pidfd publication yet.
		return handle, err
	})
	if err != nil {
		t.Fatal("actual metered Start", err)
	}
	waitErr := handle.Wait()
	if command.ProcessState == nil || waitErr == nil || command.Cancel != nil || handle.(*linuxCorpusAuthorWait).command.fd != -1 {
		t.Fatal("canceled root lost actual Wait/callback join")
	}
	status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("owned pidfd cancellation did not deliver actual SIGKILL")
	}
	if client.Close(lifetime) != nil || controller.Fence() != nil || <-served != nil {
		t.Fatal("canceled root lost genuine settlement")
	}
	snapshot, err := controller.Snapshot()
	if err != nil || !snapshot.Complete || !authorCustodyProducerComplete(snapshot, 1, 1) {
		t.Fatal("cancellation lost finite accounting")
	}
}

func TestLinuxAuthorGitPinFailureChild(t *testing.T) {
	if os.Getenv("PHEBS_T42_AUTHOR_PIN_FAILURE_CHILD") != "1" {
		return
	}
	if err := linuxAuthorGitPinFailureControl(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	_, _ = fmt.Fprintln(os.Stdout, "EMFILE retained genuine Handle.Wait and settlement")
	os.Exit(0)
}
func linuxAuthorGitPinFailureControl() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, controller, served, err := linuxAuthorTestClient(ctx, 1)
	if err != nil {
		return err
	}
	command := rootWaitFixedCommand(ctx, "exit")
	// The production Linux recipe uses exec.Command, not CommandContext.
	command = exec.Command(command.Path, command.Args[1:]...)
	command.Env = []string{rootWaitChildEnvironment + "=exit", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	state := &linuxCorpusAuthorState{}
	if err := state.prepare(ctx, command); err != nil {
		return err
	}
	var previous unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &previous); err != nil {
		return err
	}
	limited := previous
	limited.Cur = 0
	limitedSet := false
	var actual dispatchadmission.Handle
	started, waited, transportDone := false, false, false
	defer func() {
		if started && !waited {
			_ = actual.Wait()
		}
		if !transportDone {
			_ = client.Close(ctx)
			<-served
		}
	}()
	handle, err := state.start(ctx, command, func(ctx context.Context, command *exec.Cmd) (dispatchadmission.Handle, error) {
		handle, err := client.Start(ctx, dispatchadmission.SiteCorpusAuthorGit, command)
		if err == nil {
			actual = handle
			started = true
		}
		if err == nil {
			err = rootWaitObserveUnreaped(command)
		}
		if err == nil {
			err = unix.Setrlimit(unix.RLIMIT_NOFILE, &limited)
			limitedSet = err == nil
		}
		return handle, err
	})
	restoreErr := unix.Setrlimit(unix.RLIMIT_NOFILE, &previous)
	if err != nil {

		return err
	}
	wait := handle.(*linuxCorpusAuthorWait)
	pinErr := wait.command.pinErr
	fd := wait.command.fd
	waitErr := handle.Wait()
	waited = true
	if !limitedSet || restoreErr != nil || fd != -1 || !errors.Is(pinErr, unix.EMFILE) || !errors.Is(waitErr, ErrExecutionCorpusAuthor) || command.ProcessState == nil || command.ProcessState.ExitCode() != 17 {
		return errors.New("EMFILE lost actual exit17 or pin failure")
	}
	closeErr := client.Close(ctx)
	fenceErr := controller.Fence()
	serveErr := <-served
	transportDone = true
	if closeErr != nil || fenceErr != nil || serveErr != nil {
		return errors.New("EMFILE lost genuine DA settlement")
	}
	snapshot, err := controller.Snapshot()
	if err != nil || !snapshot.Complete || !authorCustodyProducerComplete(snapshot, 1, 1) {
		return errors.New("EMFILE lost final accounting")
	}
	return nil
}
func TestLinuxAuthorGitPinFailureStillSettles(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxAuthorGitPinFailureChild$")
	command.Env = []string{"PHEBS_T42_AUTHOR_PIN_FAILURE_CHILD=1", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "EMFILE retained genuine Handle.Wait and settlement\n" {
		t.Fatal("isolated child-only EMFILE control", err, string(output))
	}
}

func TestLinuxAuthorRootRejectsForeignOwnershipControls(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "control")
	if os.WriteFile(file, []byte("fixed\n"), 0o600) != nil {
		t.Fatal("fixture")
	}
	state := &linuxCorpusAuthorState{}
	for _, mode := range []os.FileMode{0o600, 0o622} {
		if os.Chmod(file, mode) != nil {
			t.Fatal("mode")
		}
		info, err := os.Lstat(file)
		if err != nil || state.owned(info) != (mode == 0o600) {
			t.Fatal("owned-file permission boundary", mode, err)
		}
	}
	if os.Chmod(file, 0o600) != nil || os.Link(file, file+"-alias") != nil {
		t.Fatal("hardlink")
	}
	info, err := os.Lstat(file)
	if err != nil || state.owned(info) {
		t.Fatal("linked mutable control admitted")
	}
}

func TestLinuxAuthorGitWaitingChild(t *testing.T) {
	if os.Getenv("PHEBS_T42_AUTHOR_WAITING_CHILD") != "1" {
		return
	}
	// A fixed live child cannot satisfy the cancellation oracle by natural exit.
	for {
		time.Sleep(time.Hour)
	}
}
