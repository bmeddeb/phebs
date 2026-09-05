//go:build darwin

package t421

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

type corpusAuthorTestTransport struct {
	controller *dispatchadmission.Controller
	client     *dispatchadmission.Client
	done       <-chan error
	commands   []*exec.Cmd
}

func corpusAuthorTestNew(t *testing.T, git *ExecutionGitCustody, parent string, source *corpusAuthorSource) (*ExecutionCorpusAuthor, *corpusAuthorTestTransport) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	limits := dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1,
		ActivePerProducer: 1, Attempts: 10, WireBytes: 16 << 10, AckTimeout: 2 * time.Second}
	producer := dispatchadmission.Producer{ID: 1, Binding: [32]byte{31}, Sites: dispatchadmission.AuthorSites()}
	controller, err := dispatchadmission.New(ctx, dispatchadmission.Config{Limits: limits, Producers: []dispatchadmission.Producer{producer},
		Phases: []dispatchadmission.Phase{{ID: 1, Roles: []dispatchadmission.RoleBudget{{Role: dispatchadmission.RoleGit, Attempts: 10}}}}})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	server, child, err := dispatchadmission.NewPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	client, err := dispatchadmission.NewClient(ctx, child, producer, 1, limits)
	if err != nil {
		cancel()
		_ = server.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- controller.ServeChecked(ctx, 1, os.Getpid(), server, func(ctx context.Context, _ dispatchadmission.Site) error {
			_, _, err := git.Check(ctx)
			return err
		})
	}()
	transport := &corpusAuthorTestTransport{controller: controller, client: client, done: done}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("author test controller did not join")
		}
		for _, command := range transport.commands {
			if command.Process != nil && command.ProcessState == nil {
				t.Error("author retained an unjoined started command")
			}
		}
	})
	_, binary, err := git.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := func(ctx context.Context, command *exec.Cmd) (dispatchadmission.Handle, error) {
		transport.commands = append(transport.commands, command)
		environment, err := git.Environment(ctx, parent, parent)
		if err != nil || command.Path != binary {
			return dispatchadmission.Handle{}, ErrExecutionCorpusAuthor
		}
		command.Env = environment
		return client.Start(ctx, dispatchadmission.SiteCorpusAuthorGit, command)
	}
	author, err := newExecutionCorpusAuthor(ctx, source, parent, binary, start)
	if author != nil {
		t.Cleanup(func() { _ = author.Close() })
	}
	if err != nil {
		t.Fatal(err)
	}
	return author, transport
}

func TestExecutionCorpusAuthorRealCurrentRevisions(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
	source := corpusAuthorTestSource(t)
	author, transport := corpusAuthorTestNew(t, git, parent, source)
	var results []AuthoredExecutionRevision
	for index, physical := range source.revisions {
		result, err := author.AuthorNext(t.Context(), physical.Name)
		if err != nil {
			t.Fatalf("actual revision %s: %v", physical.Name, err)
		}
		results = append(results, result)
		if result.Commit != physical.ExpectedCommit || result.Tree != physical.ExpectedTree ||
			result.Manifest.TreeInventory != physical.ExpectedTreeInventory || result.Manifest.RegularFiles != 3 {
			t.Fatal("actual revision differs from independently derived tiny input recipe")
		}
		raw, err := MarshalCanonical(result)
		if err != nil || len(raw) > 4096 || strings.Contains(string(raw), author.Directory()) || strings.Contains(string(raw), "package neutral") {
			t.Fatal("authored manifest is not bounded source-free evidence")
		}
		wantAttempts := uint64(4 + index*3)
		snapshot, err := transport.controller.Snapshot()
		if err != nil || snapshot.Attempts != wantAttempts || snapshot.Producers[0].Active != 0 {
			t.Fatalf("actual admitted command prefix=%d want=%d: %v", snapshot.Attempts, wantAttempts, err)
		}
		if index+1 < len(source.revisions) {
			// This separate test-only observer is not an author command. It
			// proves the future commit object was not preauthored by the stream.
			command := gitCustodyTestCommand(t, git, t.Context(), parent, nil,
				"-C", author.Directory(), "cat-file", "-e", source.revisions[index+1].ExpectedCommit)
			if err := command.Run(); err == nil {
				t.Fatal("a future revision object already exists")
			}
		}
		if _, err := os.Stat(filepath.Join(author.Directory(), "packed-refs")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("exact command flow unexpectedly produced packed refs")
		}
	}
	if results[0].Tree != results[2].Tree || results[0].Tree == results[1].Tree ||
		results[1].ParentCommit != results[0].Commit || results[2].ParentCommit != results[1].Commit {
		t.Fatal("actual A-B-A parent/tree continuity failed")
	}
	if err := transport.controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := transport.client.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := transport.controller.Snapshot()
	if err != nil || !snapshot.Complete || snapshot.Attempts != 10 {
		t.Fatalf("actual author prefix did not close: %+v, %v", snapshot, err)
	}
	if err := author.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(author.Directory()); err != nil {
		t.Fatal("Close removed source custody")
	}
	if result, err := author.AuthorNext(t.Context(), "a"); err == nil || result != (AuthoredExecutionRevision{}) {
		t.Fatal("closed author issued a result")
	}
}

func TestExecutionCorpusAuthorRealRefusalAndCancellation(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
	for _, name := range []string{"out of order", "canceled before init", "stream failure", "stream cancellation", "ref drift", "config drift", "extra ref", "root replacement"} {
		t.Run(name, func(t *testing.T) {
			source := corpusAuthorTestSource(t)
			author, transport := corpusAuthorTestNew(t, git, parent, source)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			revision := "a"
			expectedAttempts := uint64(0)
			switch name {
			case "out of order":
				revision = "b"
			case "canceled before init":
				cancel()
			case "stream failure", "stream cancellation":
				expectedAttempts = 2
				source.walkBlobs = func(context.Context, string, func(string, []byte) error) error {
					if name == "stream cancellation" {
						cancel()
					}
					return errors.New("private source detail must not escape")
				}
			case "ref drift", "config drift", "extra ref":
				if _, err := author.AuthorNext(t.Context(), "a"); err != nil {
					t.Fatal(err)
				}
				expectedAttempts, revision = 4, "b"
				path, content := "refs/heads/main", strings.Repeat("a", 40)+"\n"
				switch name {
				case "config drift":
					path, content = "config", "[core]\n bare = true\n"
				case "extra ref":
					path = "refs/heads/future"
				}
				if err := os.WriteFile(filepath.Join(author.Directory(), path), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			case "root replacement":
				if err := os.Rename(author.Directory(), author.Directory()+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(author.Directory(), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			result, err := author.AuthorNext(ctx, revision)
			if !errors.Is(err, ErrExecutionCorpusAuthor) || result != (AuthoredExecutionRevision{}) || strings.Contains(err.Error(), "private source detail") {
				t.Fatalf("failed author returned evidence or raw cause: %+v, %v", result, err)
			}
			if _, err := author.AuthorNext(t.Context(), "a"); !errors.Is(err, ErrExecutionCorpusAuthor) {
				t.Fatal("author failure was not terminal")
			}
			snapshot, _ := transport.controller.Snapshot()
			if snapshot.Attempts != expectedAttempts || snapshot.Producers[0].Active != 0 {
				t.Fatalf("refused author prefix=%d active=%d want=%d", snapshot.Attempts, snapshot.Producers[0].Active, expectedAttempts)
			}
		})
	}
}

func TestExecutionCorpusAuthorRequiresActualBootstrap(t *testing.T) {
	parent, _ := inputCustodyTestFixture(t)
	before, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{nil, t.Context()} {
		if author, err := NewExecutionCorpusAuthor(ctx, nil, parent); err == nil || author != nil {
			t.Fatal("absent author admission created a source root")
		}
	}
	after, err := os.ReadDir(parent)
	if err != nil || len(before) != len(after) {
		t.Fatal("refused author bootstrap mutated parent")
	}
	var zero ExecutionCorpusAuthor
	if result, err := zero.AuthorNext(t.Context(), "a"); err == nil || result != (AuthoredExecutionRevision{}) {
		t.Fatal("zero fabricated author issued a result")
	}
}
