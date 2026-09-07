//go:build darwin || linux

package t421

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func authorSharedTestConfig(index int) dispatchadmission.Config {
	phase := uint32(2 + 2*index)
	return dispatchadmission.Config{
		Limits: dispatchadmission.Limits{Producers: 2, Sites: 2, Roles: 2, Phases: 1,
			ActivePerProducer: 4, Attempts: 100, WireBytes: 64 << 10, AckTimeout: time.Second},
		Producers: []dispatchadmission.Producer{
			{ID: executionRootProducer, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{ExecutionParentAuthorSite()}},
			{ID: uint32(7 + index), Binding: [32]byte{2}, Sites: dispatchadmission.AuthorSites()}},
		Phases: []dispatchadmission.Phase{{ID: phase, Roles: []dispatchadmission.RoleBudget{
			{Role: executionRoleAuthor, Attempts: 1}, {Role: dispatchadmission.RoleGit, Attempts: 99}}}},
	}
}

func TestExecutionAuthorLiveServerBorrowCannotBeBypassed(t *testing.T) {
	for _, mode := range []string{"borrowed", "author_active", "both", "wrong_run", "wrong_revision", "wrong_producer"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			controller, err := dispatchadmission.New(ctx, authorSharedTestConfig(1))
			if err != nil {
				t.Fatal(err)
			}
			parent, err := controller.NewLocalProducer(ctx, executionRootProducer)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Close(context.Background()) }()
			run := &ExecutionEpochOneRun{}
			custody := &ExecutionAuthorCustody{borrowedBy: run, next: 1,
				expected: [3]AuthoredExecutionRevision{{Name: "a"}, {Name: "b"}, {Name: "a-return"}}}
			borrower, producer := run, uint32(8)
			switch mode {
			case "borrowed":
				borrower = nil // Public AuthorNext/On has no borrowed-run authority.
			case "author_active":
				custody.borrowedBy, custody.active, borrower = nil, true, nil
			case "both":
				custody.active = true
			case "wrong_run":
				borrower = &ExecutionEpochOneRun{}
			case "wrong_revision":
				custody.next = 2
			case "wrong_producer":
				producer = 7
			}
			if custody.next >= len(custody.expected) {
				t.Fatal("guard fixture must not refuse on exhausted revision inventory")
			}
			wasActive := custody.active
			if custody.Close() == nil || custody.closed {
				t.Fatal("Close released live source custody")
			}
			// Refusal must occur before source/roots validation: this minimal
			// custody has none, and must not become poisoned by touching them.
			if _, err := custody.authorNext(ctx, controller, parent, producer, borrower); err == nil || custody.err != nil ||
				custody.active != wasActive || len(custody.results) != 0 {
				t.Fatal("borrow/author guard touched or admitted source", err)
			}
			count, err := parent.Count()
			if err != nil || count.Ordinal != 0 || count.Active != 0 || custody.borrowedBy != nil && custody.borrowedBy != run {
				t.Fatal("refusal launched or changed the live server borrow")
			}
			custody.borrowedBy, custody.active = nil, false
			if custody.Close() != nil || !custody.closed {
				t.Fatal("joined source custody cannot close")
			}
		})
	}
}

// The real scalar controller prefix, not a caller count or globally tiny Git
// budget, must refuse the next author attempt and any overlapping active one.
// Native true commands here are mechanical fixtures, not admitted author Git.
func TestExecutionAuthorSharedExactProducerBudget(t *testing.T) {
	for index, attempts := range []uint64{4, 3, 3} {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		config := authorSharedTestConfig(index)
		controller, err := dispatchadmission.New(ctx, config)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		serverFile, child, err := dispatchadmission.NewPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		producer := config.Producers[1]
		client, err := dispatchadmission.NewClient(ctx, child, producer, config.Phases[0].ID, config.Limits)
		if err != nil {
			_ = serverFile.Close()
			cancel()
			t.Fatal(err)
		}
		served := make(chan error, 1)
		go func() {
			served <- controller.ServeChecked(ctx, producer.ID, os.Getpid(), serverFile,
				func(context.Context, dispatchadmission.Site) error {
					if !authorCustodyDispatchAllowed(controller, producer.ID, index) {
						return ErrExecutionAuthorCustody
					}
					return nil
				})
		}()
		for range attempts {
			command := exec.CommandContext(ctx, "/usr/bin/true")
			handle, err := client.Start(ctx, dispatchadmission.SiteCorpusAuthorGit, command)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			if authorCustodyDispatchAllowed(controller, producer.ID, index) {
				cancel()
				_ = handle.Wait()
				t.Fatal("overlapping active author dispatch permitted")
			}
			if err := handle.Wait(); err != nil {
				cancel()
				t.Fatal(err)
			}
		}
		command := exec.CommandContext(ctx, "/usr/bin/true")
		if _, err := client.Start(ctx, dispatchadmission.SiteCorpusAuthorGit, command); err == nil || command.Process != nil {
			cancel()
			t.Fatal("extra actual author dispatch permitted")
		}
		if err := <-served; err == nil {
			cancel()
			t.Fatal("author bound did not fail closed")
		}
		snapshot, err := controller.Snapshot()
		cancel()
		if err == nil || snapshot.Attempts != attempts || snapshot.Producers[1].Ordinal != attempts || snapshot.Complete {
			t.Fatal("refused author prefix was changed or erased")
		}
	}
}

func TestExecutionAuthorSharedRefusalRetainsActualRootPrefix(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	controller, err := dispatchadmission.New(ctx, authorSharedTestConfig(0))
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controller.NewLocalProducer(ctx, executionRootProducer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(ctx) }()
	handle, err := parent.StartInPhase(ctx, 2, ExecutionParentAuthorSite(), exec.CommandContext(ctx, "/usr/bin/true"))
	if err != nil || handle.Wait() != nil {
		t.Fatal("root mechanical attempt failed", err)
	}
	// A caller-created zero custody cannot turn a genuine transport into input
	// authority, nor erase the real earlier direct-root prefix on refusal.
	result, err := (&ExecutionAuthorCustody{}).AuthorNextOn(ctx, controller, parent, 7)
	if err == nil || result.Completed || result.RootStarted || result.Accounting.Attempts != 1 || result.Accounting.Digest == ([32]byte{}) {
		t.Fatal("invalid custody admitted or discarded actual prefix", result, err)
	}
	if _, err := (&ExecutionAuthorCustody{}).AuthorNextOn(ctx, &dispatchadmission.Controller{}, parent, 7); err == nil {
		t.Fatal("different controller accepted genuine parent")
	}
}

func TestExecutionAuthorSharedCompletionUsesOwnActualRow(t *testing.T) {
	snapshot := dispatchadmission.Snapshot{Attempts: 19, Complete: false, Producers: []dispatchadmission.ProducerCount{
		{Producer: 1, Attached: true, Active: 1, Ordinal: 3},
		{Producer: 7, Attached: true, Closed: true, Ordinal: 4}}}
	if !authorCustodyProducerComplete(snapshot, 7, 4) {
		t.Fatal("live parent falsely blocks completed author row")
	}
	for _, mode := range []string{"absent", "wrong-id", "wrong-count", "active", "unattached", "unclosed"} {
		changed := snapshot
		changed.Producers = append([]dispatchadmission.ProducerCount(nil), snapshot.Producers...)
		switch mode {
		case "absent":
			changed.Producers = changed.Producers[:1]
		case "wrong-id":
			changed.Producers[1].Producer = 8
		case "wrong-count":
			changed.Producers[1].Ordinal = 3
		case "active":
			changed.Producers[1].Active = 1
		case "unattached":
			changed.Producers[1].Attached = false
		case "unclosed":
			changed.Producers[1].Closed = false
		}
		if authorCustodyProducerComplete(changed, 7, 4) {
			t.Fatal("incomplete own row accepted", mode)
		}
	}
}
