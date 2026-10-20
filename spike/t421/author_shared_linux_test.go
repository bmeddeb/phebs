//go:build linux

package t421

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

// The normal transport has no alternate argv. Only this own test image selects
// one fixed test at its exact fd5 entry. Ordinary tests/r.a helpers are unaffected.
// This image proves mechanics only, never reference-issued author authority.
func init() {
	if len(os.Args) == 1 && os.Args[0] == "/proc/self/fd/5" && os.Getenv(dispatchadmission.ProductionEnvironment) == dispatchadmission.ProductionSelector {
		os.Args = append(os.Args, "-test.run=^TestLinuxAuthorSharedFixedTransportChild$")
	}
}

func linuxAuthorSharedConfig() dispatchadmission.Config {
	config := dispatchadmission.Config{Limits: dispatchadmission.Limits{Producers: 5, Sites: 5, Roles: 2, Phases: 3, ActivePerProducer: 4, Attempts: 100, WireBytes: 64 << 10, AckTimeout: 2 * time.Second}}
	config.Producers = append(config.Producers, dispatchadmission.Producer{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{ExecutionParentAuthorSite()}}, dispatchadmission.Producer{ID: 2, Binding: [32]byte{2}, Sites: []dispatchadmission.Site{{ID: 1, Role: dispatchadmission.RoleGit, Persistent: true}}})
	for index := range 3 {
		config.Producers = append(config.Producers, dispatchadmission.Producer{ID: uint32(7 + index), Binding: [32]byte{byte(7 + index)}, Sites: dispatchadmission.AuthorSites()})
		config.Phases = append(config.Phases, dispatchadmission.Phase{ID: uint32(2 + 2*index), Roles: []dispatchadmission.RoleBudget{{Role: executionRoleAuthor, Attempts: 1}, {Role: dispatchadmission.RoleGit, Attempts: 99}}})
	}
	return config
}

func linuxAuthorSharedFixture(t *testing.T) (*LinuxExecutionAuthorCustody, *corpusAuthorSource) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "parent")
	if os.Mkdir(parent, 0o700) != nil {
		t.Fatal("private parent")
	}
	owner := &LinuxExecutionAuthorCustody{}
	if owner.createRoots(t.Context(), parent) != nil {
		t.Fatal("native roots")
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	source := corpusAuthorTestSource(t)
	for index, physical := range source.revisions {
		prior := ""
		if index > 0 {
			prior = source.revisions[index-1].ExpectedCommit
		}
		raw, err := canonicalGitCommitBytesFor(physical.ExpectedTree, prior, physical.CommitMessage, source.recipe)
		if err != nil {
			t.Fatal(err)
		}
		owner.projection.expected[index] = AuthoredExecutionRevision{Name: physical.Name, Commit: physical.ExpectedCommit, Tree: physical.ExpectedTree, ParentCommit: prior, Manifest: source.manifest(physical, physical.ExpectedTreeInventory, SHA256(raw))}
		owner.projection.phases[index] = uint32(2 + 2*index)
		owner.projection.deadlines[index] = 10 * time.Second
	}
	return owner, source
}

func TestLinuxAuthorSharedTransportLeavesOtherProducerLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	controller, err := dispatchadmission.New(ctx, linuxAuthorSharedConfig())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controller.NewLocalProducer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(context.Background()) }()
	other, err := controller.NewLocalProducer(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close(context.Background()) }()
	held, err := other.Start(ctx, 1, exec.Command("/usr/bin/true"))
	if err != nil {
		t.Fatal(err)
	}
	heldWaited := false
	defer func() {
		if !heldWaited {
			_ = held.Wait()
		}
	}()
	owner, _ := linuxAuthorSharedFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan")
	planRaw := []byte("fixed tiny shared-author mechanism fixture\n")
	if os.WriteFile(planPath, planRaw, 0o600) != nil {
		t.Fatal("plan")
	}
	inputs := requireLinuxInputCustody(t, linuxInputSelection(t, executable, "image", true), linuxInputSelection(t, planPath, "plan", false))
	imageIdentity, err := inputs.Check(ctx, "image")
	if err != nil {
		t.Fatal(err)
	}
	// Fixed trusted-host Git is a mechanical fixture, not protected H2p authority.
	// The production path still requires genuine H2p and held-owner checks.
	coreRoot := t.TempDir()
	if os.Mkdir(filepath.Join(coreRoot, "core"), 0o700) != nil || os.Symlink("/usr/bin/git", filepath.Join(coreRoot, "core", "git")) != nil {
		t.Fatal("fixed Git fixture")
	}
	root, err := os.Open(coreRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for index, name := range []string{"a", "b", "a-return"} {
		err = inputs.WithInputs(context.Background(), []string{"image", "plan"}, func(files []*os.File) error {
			launch := linuxAuthorLaunch{controller: controller, parent: parent, producer: uint32(7 + index)}
			if owner.bindLaunchLocked(index, &launch) != nil {
				return errors.New("genuine shared launch refused")
			}
			request := linuxCorpusAuthorRequest{Schema: linuxCorpusAuthorRequestSchema, PlanSHA256: SHA256(planRaw), AuthorSHA256: imageIdentity.SHA256, SourcePath: owner.Directory(), SourceIdentity: owner.roots[1].identity, Controller: os.Getpid(), GitFD: int(root.Fd()), Revision: name, Previous: owner.previous}
			attempt := &linuxAuthorAttempt{result: ExecutionAuthorResult{Revision: name, ProducerID: uint32(7 + index)}}
			if err := owner.runTransport(ctx, attempt, index, files[0], files[1], request, launch, owner.checkRoots); err != nil {
				t.Logf("fixed transport revision=%s step=%s pid=%d joined=%t empty=%t stderr=%q", name, attempt.step, attempt.pid, attempt.result.RootJoined, attempt.result.SessionEmpty, attempt.stderr)
				return err
			}
			if !attempt.result.RootStarted || !attempt.result.RootJoined || !attempt.result.SessionEmpty || attempt.result.Accounting.Complete || !authorCustodyProducerComplete(attempt.result.Accounting, uint32(7+index), authorCustodyAttempts(index)) || attempt.result.Response == nil {
				return errors.New("shared completion lost its own terminal row")
			}
			count, err := parent.Count()
			if err != nil || count.Ordinal != uint64(index+1) || count.Active != 0 {
				return errors.New("parent actual root did not settle")
			}
			otherCount, err := other.Count()
			if err != nil || otherCount.Active != 1 {
				return errors.New("unrelated held producer was drained")
			}
			continued, err := other.Start(ctx, 1, exec.Command("/usr/bin/true"))
			if err != nil || continued.Wait() != nil {
				return errors.New("shared success fenced unrelated producer")
			}
			response := cloneCorpusAuthorResponse(*attempt.result.Response)
			owner.previous = &response
			owner.next++
			if index < 2 {
				if parent.Pause(ctx) != nil || other.Pause(ctx) != nil || controller.Fence() != nil || parent.Checkpoint(ctx) != nil || other.Checkpoint(ctx) != nil || controller.Advance() != nil || parent.Resume(uint32(4+2*index)) != nil || other.Resume(uint32(4+2*index)) != nil {
					return errors.New("explicit owning phase transition failed")
				}
			}
			t.Logf("revision=%s producer=%d actual_root=%d joined=%t empty_session=%t git=%d parent_ordinal=%d other_active=%d", name, attempt.result.ProducerID, attempt.pid, attempt.result.RootJoined, attempt.result.SessionEmpty, authorCustodyAttempts(index), count.Ordinal, otherCount.Active)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := held.Wait(); err != nil {
		t.Fatal(err)
	}
	heldWaited = true
	if other.Close(ctx) != nil || parent.Close(ctx) != nil || controller.Fence() != nil {
		t.Fatal("actual final cleanup")
	}
	final, err := controller.Snapshot()
	if err != nil || !final.Complete || final.Attempts != 17 {
		t.Fatal("shared committed accounting", final, err)
	} // 3 authors + 10 nested Git + 4 other roots.
}

func TestLinuxAuthorSharedFixedTransportChild(t *testing.T) {
	if len(os.Args) < 2 || os.Args[0] != "/proc/self/fd/5" || os.Getenv(dispatchadmission.ProductionEnvironment) != dispatchadmission.ProductionSelector {
		return
	}
	err := linuxAuthorSharedFixedChild(t)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "fixed shared-author mechanism refused")
		os.Exit(125)
	}
	os.Exit(0) // No testing footer may follow the one canonical protocol response.
}
func linuxAuthorSharedFixedChild(t *testing.T) (retErr error) {
	step := "capture"
	defer func() {
		if retErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "fixed shared-author stage=%s\n", step)
		}
	}()
	inputs, err := CaptureLinuxExecutionAuthorInputs()
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, inputs.Close()) }()
	unix.Umask(0o077)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	step = "bootstrap"
	lifetime, err := dispatchadmission.BootstrapAuthor(ctx)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, lifetime.Close(context.Background())) }()
	ctx = dispatchadmission.ProcessContext()
	step = "request-read"
	connection, err := net.FileConn(os.Stdin)
	if err != nil {
		return err
	}
	if connection.SetDeadline(time.Now().Add(10*time.Second)) != nil {
		_ = connection.Close()
		return ErrExecutionCorpusAuthor
	}
	raw, readErr := io.ReadAll(io.LimitReader(connection, MaxExecutionCorpusAuthorRequestBytes+1))
	closeErr := connection.Close()
	if readErr != nil || closeErr != nil {
		return ErrExecutionCorpusAuthor
	}
	step = "request-binding"
	expected, err := dispatchadmission.AuthorInputSHA256()
	if err != nil {
		return err
	}
	request, err := decodeLinuxCorpusAuthorRequest(raw, expected, os.Getppid())
	if err != nil {
		return err
	}
	step = "plan"
	plan, err := io.ReadAll(io.LimitReader(inputs.plan, MaxPlanBytes+1))
	if err != nil || !bytes.Equal(plan, []byte("fixed tiny shared-author mechanism fixture\n")) || SHA256(plan) != request.PlanSHA256 {
		return ErrExecutionCorpusAuthor
	}
	step = "image"
	digest, err := digestExecutionReferenceFD(ctx, inputs.image)
	if err != nil || digest != request.AuthorSHA256 {
		return ErrExecutionCorpusAuthor
	}
	step = "git-route"
	core := linuxAuthorGitCore(request.Controller, request.GitFD)
	if dispatchadmission.ProductionTool("git") != core+"/git" {
		return ErrExecutionCorpusAuthor
	}
	step = "source-root"
	author, err := openLinuxAuthorRoot(ctx, request.SourcePath, request.SourceIdentity)
	if err != nil {
		return err
	}
	author.platform.(*linuxCorpusAuthorState).inputs = inputs
	step = "resume"
	author, err = resumeCorpusAuthorState(ctx, author, corpusAuthorTestSource(t), request.Revision, request.Previous, true, core+"/git", dispatchadmission.StartAuthor)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, author.Close()) }()
	author.lifetime = ctx
	step = "author"
	response, err := author.AuthorRequested(ctx)
	if err != nil {
		return err
	}
	step = "response-write"
	output, err := net.FileConn(os.Stdout)
	if err != nil {
		return err
	}
	if output.SetDeadline(time.Now().Add(10*time.Second)) != nil {
		_ = output.Close()
		return ErrExecutionCorpusAuthor
	}
	count, writeErr := output.Write(response)
	closeErr = output.Close()
	if writeErr != nil || count != len(response) || closeErr != nil {
		return ErrExecutionCorpusAuthor
	}
	step = "terminal"
	return dispatchadmission.WaitAuthorCompletion(ctx)
}
