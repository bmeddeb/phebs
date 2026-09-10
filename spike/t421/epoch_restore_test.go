package t421

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestEpochRestoreClosedPrefix(t *testing.T) {
	makeResult := epochRestorePrefixFixture
	if !epochRestoreClosedPrefix(t.Context(), makeResult()) {
		t.Fatal("joined restore prefix refused")
	}
	for _, mode := range []string{"restore_open", "restore_store_open", "restore_active", "backup_open", "server_unjoined", "session", "missing_restore", "missing_eof", "root_ordinal", "whole_complete"} {
		t.Run(mode, func(t *testing.T) {
			v := makeResult()
			switch mode {
			case "restore_open":
				v.Accounting.Producers[9].Closed = false
			case "restore_store_open":
				v.Store.Store.Producers[5].Closed = false
			case "restore_active":
				v.Accounting.Producers[9].Active = 1
			case "backup_open":
				v.Accounting.Producers[8].Closed = false
			case "server_unjoined":
				v.RootJoined = false
			case "session":
				v.SessionEmpty = false
			case "missing_restore":
				v.Accounting.Producers = v.Accounting.Producers[:9]
			case "missing_eof":
				v.Store.TerminalEOF--
			case "root_ordinal":
				v.Accounting.Producers[0].Ordinal--
			case "whole_complete":
				v.Store.Complete = true
			}
			if epochRestoreClosedPrefix(t.Context(), v) {
				t.Fatal("incomplete restore admitted")
			}
		})
	}
}

// This models complete prior history; the inherited test separately observes
// a real producer-six close without pretending it executed that history.
func epochRestorePrefixFixture() ExecutionEpochOneResult {
	v := ExecutionEpochOneResult{RootStarted: true, RootJoined: true, SessionEmpty: true}
	v.Store.Opened, v.Store.TerminalEOF, v.Store.Store.Phase = 6, 6, 12
	for _, id := range []uint32{1, 2, 3, 4, 5, 7, 8, 9, 10, 11} {
		p := dispatchadmission.ProducerCount{Producer: id, Attached: true, Closed: true}
		if id == 1 {
			p.Closed, p.Ordinal = false, 9
		}
		if id == 5 {
			p.Checkpoint = 11
		}
		if id >= 7 && id <= 9 {
			p.Ordinal = authorCustodyAttempts(int(id - 7))
		}
		v.Accounting.Producers = append(v.Accounting.Producers, p)
	}
	for _, id := range []uint32{2, 3, 4, 5, 10, 11} {
		p := storeaccounting.ProducerCount{Producer: id, Attached: true, Closed: true}
		if id == 4 {
			p.Closed, p.TerminalFencedEOF, p.TerminalPhase, p.Checkpoint = false, true, 8, 8
		}
		v.Store.Store.Producers = append(v.Store.Store.Producers, p)
	}
	return v
}

func TestEpochRestoredClosedPrefix(t *testing.T) {
	for _, mode := range []string{"valid", "root_open", "root_ordinal", "server_open", "store_open", "missing_eof", "prior_restore_open", "prior_terminal_lost", "unknown_stage"} {
		t.Run(mode, func(t *testing.T) {
			v := epochRestorePrefixFixture()
			v.Accounting.Producers[0].Closed, v.Accounting.Producers[0].Ordinal = true, 10
			v.Accounting.Producers = append(v.Accounting.Producers, dispatchadmission.ProducerCount{Producer: 6, Attached: true, Closed: true})
			v.Store.Store.Producers = append(v.Store.Store.Producers, storeaccounting.ProducerCount{Producer: 6, Attached: true, Closed: true})
			v.Store.Opened, v.Store.TerminalEOF = 7, 7
			switch mode {
			case "prior_restore_open":
				v.Accounting.Producers[9].Closed = false
			case "prior_terminal_lost":
				v.Store.Store.Producers[2].TerminalFencedEOF = false
			case "server_open":
				v.Accounting.Producers[10].Closed = false
			case "store_open":
				v.Store.Store.Producers[6].Closed = false
			case "missing_eof":
				v.Store.TerminalEOF--
			case "root_open":
				v.Accounting.Producers[0].Closed = false
			case "root_ordinal":
				v.Accounting.Producers[0].Ordinal--
			}
			stage := uint32(6)
			if mode == "unknown_stage" {
				stage = 7
			}
			if epochArchiveClosedPrefix(t.Context(), v, stage) != (mode == "valid") {
				t.Fatal("restored prefix classification", mode)
			}
		})
	}
}

func TestEpochRestoredStartupBounds(t *testing.T) {
	for _, mode := range []string{"valid", "v2", "deadline", "health", "short"} {
		p := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
		switch mode {
		case "v2":
			p.Schema = PlanV2Schema
		case "deadline":
			p.PhaseDeadlines[11].DeadlineMS++
		case "health":
			p.SafetyEnvelope.ServerHealthDeadlineMS++
		case "short":
			p.PhaseDeadlines = p.PhaseDeadlines[:11]
		}
		b, err := restoredStartupBounds(p)
		if mode == "valid" {
			if err != nil || b.lifetime != 4*time.Hour || b.health != 15*time.Minute || b.controlPairs != 3 || b.outputBytes != 64<<20 {
				t.Fatal(b, err)
			}
		} else if err == nil {
			t.Fatal("changed startup bound admitted", mode)
		}
	}
	for _, run := range []*ExecutionEpochOneRun{nil, {}, {done: make(chan struct{})}} {
		if _, err := run.StartRestored(t.Context()); err == nil {
			t.Fatal("unjoined restored start")
		}
	}
	if got := (&ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Epoch: 5}}).producer(); got != 6 {
		t.Fatal(got)
	}
}

func TestEpochRestoredStartPreflight(t *testing.T) {
	// Invalid eligibility must refuse before touching these deliberately
	// unusable dependencies. This is not successful native admission evidence.
	for _, mode := range []string{"backup_join", "backup_session", "backup_complete", "backup_retired", "restore_join", "restore_session", "restore_complete", "restore_used", "root_join", "root_session", "manifest", "backup_work", "restore_work", "duplicate", "in_progress", "expired", "renewed", "wrong_retained", "wrong_epoch", "closed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			deadline := time.Now().Add(time.Minute)
			flow := &ExecutionEpochOne{plan: Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()},
				controller: &dispatchadmission.Controller{}, parent: &dispatchadmission.LocalProducer{}, store: &storeaccounting.Transport{},
				epochs: &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{}}}
			run := &ExecutionEpochOneRun{flow: flow, done: make(chan struct{}), epoch: ExecutionEpochConfig{Epoch: 4},
				phaseDeadline: deadline, lifetimeDeadline: deadline, backupStarted: true, backupJoined: true, backupSessionEmpty: true,
				backupComplete: true, backupRetired: true, restoreStarted: true, restoreJoined: true, restoreSessionEmpty: true,
				restoreComplete: true, restoreUsed: true, backupManifestSHA256: testDigest("archive"), restoreManifestSHA256: testDigest("archive"),
				result: ExecutionEpochOneResult{RootJoined: true, SessionEmpty: true, BackupWork: ExecutionAttemptObservation{Complete: true}, RestoreWork: ExecutionAttemptObservation{Complete: true}}}
			close(run.done)
			flow.retained = run
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "backup_join":
				run.backupJoined = false
			case "backup_session":
				run.backupSessionEmpty = false
			case "backup_complete":
				run.backupComplete = false
			case "backup_retired":
				run.backupRetired = false
			case "restore_join":
				run.restoreJoined = false
			case "restore_session":
				run.restoreSessionEmpty = false
			case "restore_complete":
				run.restoreComplete = false
			case "restore_used":
				run.restoreUsed = false
			case "root_join":
				run.result.RootJoined = false
			case "root_session":
				run.result.SessionEmpty = false
			case "manifest":
				run.restoreManifestSHA256 = testDigest("other")
			case "backup_work":
				run.result.BackupWork.Complete = false
			case "restore_work":
				run.result.RestoreWork.Complete = false
			case "duplicate":
				run.restoredStartUsed = true
			case "in_progress":
				run.returnStarting = true
			case "expired":
				run.phaseDeadline = time.Now().Add(-time.Second)
			case "renewed":
				run.phaseDeadline = deadline.Add(time.Second)
			case "wrong_retained":
				flow.retained = nil
			case "wrong_epoch":
				run.epoch.Epoch = 3
			case "closed":
				flow.closed = true
			case "canceled":
				cancel()
			}
			used, starting := run.restoredStartUsed, run.returnStarting
			if next, err := run.StartRestored(ctx); err == nil || next != nil {
				t.Fatal("invalid start admitted")
			}
			if run.restoredStartUsed != used || run.returnStarting != starting || run.returnStartDone != nil || run.returnStartCancel != nil || run.err != nil {
				t.Fatal("refusal consumed operation or touched its lifetime")
			}
		})
	}
}

func TestEpochRestoreHeldRootSafety(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native held-root custody is Darwin-only")
	}
	for _, mode := range []string{"empty", "nested", "canceled", "nil_context", "replaced", "symlink_root", "mode", "closed", "volume", "broad_path"} {
		t.Run(mode, func(t *testing.T) {
			parent, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(parent, "data")
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			held, err := openProductionRoot(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = held.file.Close() }()
			outside := filepath.Join(parent, "outside")
			if err = os.WriteFile(outside, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			if mode != "empty" {
				if err = os.Mkdir(filepath.Join(path, "nested"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(path, "nested", "data"), []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(outside, filepath.Join(path, "outside-link")); err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			switch mode {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil_context":
				ctx = nil
			case "replaced", "symlink_root":
				if err = os.Rename(path, path+"-prior"); err != nil {
					t.Fatal(err)
				}
				if mode == "replaced" {
					err = os.Mkdir(path, 0o700)
				} else {
					err = os.Symlink(path+"-prior", path)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err = os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "closed":
				if err = held.file.Close(); err != nil {
					t.Fatal(err)
				}
			case "volume":
				held.volume[0]++
			case "broad_path":
				held.path = string(filepath.Separator)
			}
			err = emptyRetiredDataRoot(ctx, held)
			if (err == nil) != (mode == "empty" || mode == "nested") {
				t.Fatal(mode, err)
			}
			raw, err := os.ReadFile(outside)
			if err != nil || string(raw) != "preserve" {
				t.Fatal("outside target changed", err)
			}
			if mode == "empty" || mode == "nested" {
				info, e := os.Stat(path)
				if e != nil || !os.SameFile(info, held.info) {
					t.Fatal("data-root inode replaced", e)
				}
				entries, e := os.ReadDir(path)
				if e != nil || len(entries) != 0 {
					t.Fatal("root not empty", entries, e)
				}
			} else {
				prior := path
				if mode == "replaced" || mode == "symlink_root" {
					prior = path + "-prior"
				}
				if raw, e := os.ReadFile(filepath.Join(prior, "nested", "data")); e != nil || string(raw) != "old" {
					t.Fatal("refusal deleted prior installation", e)
				}
			}
		})
	}
}

func TestEpochArchiveActualCommandDigest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, restore := range []bool{false, true} {
		line := "backup published: /private/archive (" + digest + ")\n"
		if restore {
			line = "restore verified and imported: " + digest + "\n"
		}
		for _, mode := range []string{"valid", "empty", "partial", "duplicate", "wrong_path", "bad_digest", "uppercase"} {
			raw := line
			switch mode {
			case "valid":
				raw = "ordinary diagnostic\n" + line + "PASS\n"
			case "empty":
				raw = ""
			case "partial":
				raw = strings.TrimSuffix(line, "\n")
			case "duplicate":
				raw = line + line
			case "wrong_path":
				if restore {
					raw = strings.Replace(line, "imported: ", "imported:", 1)
				} else {
					raw = strings.Replace(line, "/private/archive", "/other", 1)
				}
			case "bad_digest":
				raw = strings.Replace(line, digest, "sha256:bad", 1)
			case "uppercase":
				raw = strings.Replace(line, digest, strings.ToUpper(digest), 1)
			}
			got, err := epochArchiveCommandDigest([]byte(raw), "/private/archive", restore)
			if mode == "valid" {
				if err != nil || got != digest {
					t.Fatal(mode, got, err)
				}
			} else if err == nil {
				t.Fatal("invalid native output accepted", mode, restore)
			}
		}
	}
}

func TestEpochRestoreRefusesUnjoinedRun(t *testing.T) {
	for _, run := range []*ExecutionEpochOneRun{nil, {}, {done: make(chan struct{})}} {
		if _, err := run.RestoreBackup(t.Context()); err == nil {
			t.Fatal("unjoined run admitted")
		}
	}
}

func TestEpochRestoreOutputSharesRemainingBudget(t *testing.T) {
	canceled := false
	output := &epochBackupOutput{remaining: 2, server: &checkoutCommandOutput{remaining: 2, cancel: func() {}}, backup: &checkoutCommandOutput{remaining: 2, cancel: func() {}}, restore: &checkoutCommandOutput{remaining: 2, cancel: func() { canceled = true }}}
	if _, err := output.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	writer := epochBackupCommandOutput{shared: output, restore: true}
	if _, err := writer.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("c")); err == nil || !canceled {
		t.Fatal("new restore work ignored aggregate bound")
	}
	if output.server.buffer.String() != "a" || output.restore.buffer.String() != "b" || output.backup.buffer.Len() != 0 {
		t.Fatal("archive streams mixed")
	}
}
