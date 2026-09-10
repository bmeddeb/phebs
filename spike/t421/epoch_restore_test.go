package t421

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestEpochRestoreClosedPrefix(t *testing.T) {
	makeResult := func() ExecutionEpochOneResult {
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
