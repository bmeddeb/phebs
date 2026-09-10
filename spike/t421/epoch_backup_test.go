package t421

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestEpochBackupSharedOutput(t *testing.T) {
	const writers, repetitions = 2, 100
	output := &epochBackupOutput{remaining: writers * repetitions,
		server: &checkoutCommandOutput{remaining: writers * repetitions, cancel: func() {}},
		backup: &checkoutCommandOutput{remaining: writers * repetitions, cancel: func() {}}}
	var joined sync.WaitGroup
	for index := range writers {
		joined.Go(func() {
			for range repetitions {
				stream := output.server
				if index == 1 {
					stream = output.backup
				}
				if _, err := output.write(stream, []byte{'x'}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	joined.Wait()
	if output.remaining != 0 || !bytes.Equal(output.server.buffer.Bytes(), bytes.Repeat([]byte{'x'}, repetitions)) ||
		!bytes.Equal(output.backup.buffer.Bytes(), bytes.Repeat([]byte{'x'}, repetitions)) {
		t.Fatal("shared prefix changed")
	}
	if _, err := output.Write([]byte{'x'}); err == nil {
		t.Fatal("allocated a second output allowance")
	}
	if output.server.buffer.Len()+output.backup.buffer.Len() != writers*repetitions {
		t.Fatal("lost bounded prefix")
	}
}

func TestEpochBackupOutputPreservesSplitFrames(t *testing.T) {
	output := &epochBackupOutput{remaining: 100,
		server: &checkoutCommandOutput{remaining: 100, cancel: func() {}},
		backup: &checkoutCommandOutput{remaining: 100, cancel: func() {}}}
	backup := epochBackupCommandOutput{shared: output}
	for _, part := range []struct {
		server bool
		text   string
	}{
		{true, "RL1:B:"}, {false, "backup published: private"}, {true, "8\n"}, {false, " archive\n"},
	} {
		var err error
		if part.server {
			_, err = output.Write([]byte(part.text))
		} else {
			_, err = backup.Write([]byte(part.text))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if output.server.buffer.String() != "RL1:B:8\n" || output.backup.buffer.String() != "backup published: private archive\n" {
		t.Fatal("cross-process chunks corrupted the owning stream")
	}
}

func TestEpochBackupUnjoinedOutputIsUnread(t *testing.T) {
	// A nil flow would panic if the parser touched state after the join guard.
	run := &ExecutionEpochOneRun{backupStarted: true, output: &checkoutCommandOutput{}}
	result := ExecutionEpochOneResult{RootJoined: true}
	if run.finishAttemptObservation(t.Context(), &result, executionProcessDeath{}, nil) == nil {
		t.Fatal("unjoined backup output admitted")
	}
}

func TestEpochBackupLateFailureFencesAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	site := dispatchadmission.Site{ID: 1, Role: 1}
	controller, err := dispatchadmission.New(ctx, dispatchadmission.Config{
		Limits:    dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1, ActivePerProducer: 1, Attempts: 1, WireBytes: 4096, AckTimeout: time.Second},
		Producers: []dispatchadmission.Producer{{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{site}}},
		Phases:    []dispatchadmission.Phase{{ID: 12, Roles: []dispatchadmission.RoleBudget{{Role: 1, Attempts: 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controller.NewLocalProducer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(context.Background()) }()
	run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{parent: parent, controller: controller}, backupRetired: true, backupComplete: true}
	// The native backup already succeeded, but the later output validation
	// cannot complete. Exercise the actual failure fence used by finish.
	if run.finishAttemptObservation(ctx, &ExecutionEpochOneResult{RootJoined: true}, executionProcessDeath{}, nil) == nil {
		t.Fatal("missing output accepted")
	}
	run.fenceFailedBackup(ctx)
	attempt, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if _, err := parent.StartInPhase(attempt, 12, site, exec.Command("/usr/bin/true")); err == nil {
		t.Fatal("late failure left dispatch open")
	}
	snapshot, _ := controller.Snapshot()
	if snapshot.Attempts != 0 {
		t.Fatal("failed continuation charged new work")
	}
}

func TestEpochBackupClosedPrefix(t *testing.T) {
	makeResult := func() ExecutionEpochOneResult {
		result := ExecutionEpochOneResult{RootStarted: true, RootJoined: true, SessionEmpty: true}
		result.Store.Opened, result.Store.TerminalEOF, result.Store.Store.Phase = 5, 5, 12
		for _, id := range []uint32{1, 2, 3, 4, 5, 7, 8, 9, 10} {
			p := dispatchadmission.ProducerCount{Producer: id, Attached: true, Closed: true}
			if id == 1 {
				p.Closed, p.Ordinal = false, 8
			}
			if id == 5 {
				p.Checkpoint = 11
			}
			if id >= 7 && id <= 9 {
				p.Ordinal = authorCustodyAttempts(int(id - 7))
			}
			result.Accounting.Producers = append(result.Accounting.Producers, p)
		}
		for _, id := range []uint32{2, 3, 4, 5, 10} {
			p := storeaccounting.ProducerCount{Producer: id, Attached: true, Closed: true}
			if id == 4 {
				p.Closed, p.TerminalFencedEOF, p.TerminalPhase, p.Checkpoint = false, true, 8, 8
			}
			result.Store.Store.Producers = append(result.Store.Store.Producers, p)
		}
		return result
	}
	if !epochBackupClosedPrefix(t.Context(), makeResult()) {
		t.Fatal("complete joined prefix refused")
	}
	for _, mode := range []string{"unjoined", "session", "store_open", "backup_open", "phase", "root_closed", "store_reopened", "retirement"} {
		t.Run(mode, func(t *testing.T) {
			v := makeResult()
			switch mode {
			case "unjoined":
				v.RootJoined = false
			case "session":
				v.SessionEmpty = false
			case "store_open":
				v.Store.TerminalEOF--
			case "backup_open":
				v.Accounting.Producers[8].Closed = false
			case "phase":
				v.Store.Store.Phase = 11
			case "root_closed":
				v.Accounting.Producers[0].Closed = true
			case "store_reopened":
				v.Store.Store.Producers[3].Closed = false
			case "retirement":
				v.Accounting.Producers[4].Checkpoint = 12
			}
			if epochBackupClosedPrefix(t.Context(), v) {
				t.Fatal("unjoined/incorrect prefix admitted")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if epochBackupClosedPrefix(ctx, makeResult()) {
		t.Fatal("canceled prefix accepted")
	}
}
