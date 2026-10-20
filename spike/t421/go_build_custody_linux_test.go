//go:build linux

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newLinuxGoOwnerCleanupFixture(t *testing.T) *LinuxExecutionGoBuildCustody {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "owner")
	if os.Mkdir(directory, 0o700) != nil {
		t.Fatal("owner")
	}
	stage, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stage.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	return &LinuxExecutionGoBuildCustody{directory: directory, stage: stage, stageInfo: info, children: make(map[string]os.FileInfo)}
}

func TestLinuxGoBuildOwnerCloseRetainsPreparation(t *testing.T) {
	custody := newLinuxGoOwnerCleanupFixture(t)
	attempt := &linuxCheckoutGitAttempt{done: make(chan struct{}), pid: 42}
	custody.transport = &linuxCheckoutGit{active: attempt}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if custody.Close(ctx) != ErrExecutionGoBuildCustody || custody.released || custody.transport.active != attempt {
		t.Fatal("unjoined preparation released owner")
	}
	if _, err := os.Lstat(custody.directory); err != nil {
		t.Fatal("unjoined preparation lost prefix")
	}
	// Modeled join only; the native acceptance must observe actual Wait/drain.
	attempt.joined, attempt.empty = true, true
	close(attempt.done)
	if custody.Close(t.Context()) != ErrExecutionGoBuildCustody || !custody.released {
		t.Fatal("sticky refusal concealed joined release")
	}
}

func TestLinuxGoBuildOwnerExactClose(t *testing.T) {
	for _, test := range []string{"valid", "unknown", "replaced-root", "replaced-child"} {
		t.Run(test, func(t *testing.T) {
			custody := newLinuxGoOwnerCleanupFixture(t)
			if custody.stage.Mkdir("home", 0o700) != nil {
				t.Fatal("child")
			}
			var err error
			custody.children["home"], err = custody.stage.Stat("home")
			if err != nil {
				t.Fatal(err)
			}
			switch test {
			case "unknown":
				if custody.stage.WriteFile("unknown", []byte("retain"), 0o600) != nil {
					t.Fatal("unknown")
				}
			case "replaced-root":
				if os.Rename(custody.directory, custody.directory+"-retained") != nil || os.Mkdir(custody.directory, 0o700) != nil {
					t.Fatal("replacement root")
				}
			case "replaced-child":
				if custody.stage.Rename("home", "retained-home") != nil || custody.stage.Mkdir("home", 0o700) != nil {
					t.Fatal("replacement child")
				}
			}
			err = custody.Close(t.Context())
			if test == "valid" {
				if err != nil || !custody.released {
					t.Fatal("exact cleanup failed", err)
				}
			} else if err != ErrExecutionGoBuildCustody || custody.released {
				t.Fatal("unknown/replaced custody released", err)
			} else if _, err := os.Lstat(custody.directory); err != nil {
				t.Fatal("cleanup removed unknown/replacement root", err)
			}
		})
	}
}

func TestLinuxGoBuildOwnerRefusesIncompleteRequests(t *testing.T) {
	parent := t.TempDir()
	if os.Chmod(parent, 0o700) != nil {
		t.Fatal("parent")
	}
	if custody, err := ProtectLinuxExecutionGoBuildInputs(t.Context(), parent, LinuxExecutionGoBuildRequest{}, &LinuxInputTreeHelper{}); err != ErrExecutionGoBuildCustody || custody != nil {
		t.Fatal("incomplete resources admitted")
	}
	if (&LinuxExecutionGoBuildCustody{}).Check(t.Context()) != ErrExecutionGoBuildCustody {
		t.Fatal("zero owner admitted")
	}
}

func TestLinuxGoBuildOwnerRetainsMovedPayloadAfterParentReplacement(t *testing.T) {
	custody := newLinuxGoOwnerCleanupFixture(t)
	payload := filepath.Join(custody.directory, "payload")
	if custody.stage.Mkdir("payload", 0o700) != nil {
		t.Fatal("payload")
	}
	var err error
	custody.children["payload"], err = custody.stage.Stat("payload")
	if err != nil {
		t.Fatal(err)
	}
	custody.staging, err = newLinuxGoBuildStaging(payload)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = custody.staging.tree.Close() })
	if custody.staging.writeControl(t.Context(), "source/go.mod", []byte("retain source")) != nil {
		t.Fatal("populated staging")
	}
	retained := custody.directory + "-retained"
	if os.Rename(custody.directory, retained) != nil || os.Mkdir(custody.directory, 0o700) != nil ||
		os.Rename(filepath.Join(retained, "payload"), payload) != nil {
		t.Fatal("replace owner while preserving payload inode")
	}
	if custody.Close(t.Context()) != ErrExecutionGoBuildCustody || custody.released {
		t.Fatal("changed outer owner released")
	}
	if raw, err := custody.staging.tree.ReadFile("source/go.mod"); err != nil || string(raw) != "retain source" {
		t.Fatal("changed owner deleted still-matching moved source", err)
	}
}
