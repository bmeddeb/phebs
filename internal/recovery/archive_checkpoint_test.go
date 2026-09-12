package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
)

// These are real empty archive filesystem operations, not a selected engine or
// whole Restore fixture. They exercise each materializing helper's own scope.
func TestArchiveCustodyCheckpoints(t *testing.T) {
	tests := []struct {
		name    string
		create  func(context.Context, string, string) error
		verify  func(context.Context, string) error
		restore func(context.Context, string, string) error
		checks  int
		creates int
	}{
		{"focused", func(ctx context.Context, root, archive string) error {
			_, err := focusedindex.CreateArchiveWithReportContext(ctx, root, archive)
			return err
		}, func(ctx context.Context, archive string) error {
			_, err := focusedindex.VerifyArchiveWithReportContext(ctx, archive)
			return err
		}, focusedindex.RestoreArchiveContext, 4, 1},
		{"resolver", func(ctx context.Context, root, archive string) error {
			_, err := resolvercatalog.CreateArchiveWithReportContext(ctx, root, archive)
			return err
		}, func(ctx context.Context, archive string) error {
			_, err := resolvercatalog.VerifyArchiveWithReportContext(ctx, archive)
			return err
		}, resolvercatalog.RestoreArchiveContext, 4, 1},
		{"caller", func(ctx context.Context, root, archive string) error {
			_, err := callerpublication.CreateArchiveWithReportContext(ctx, root, archive)
			return err
		}, func(ctx context.Context, archive string) error {
			_, err := callerpublication.VerifyArchiveWithReportContext(ctx, archive)
			return err
		}, callerpublication.RestoreArchiveContext, 0, 1},
		{"observation", func(ctx context.Context, root, archive string) error {
			_, err := observationpublication.CreateArchive(ctx, root, archive)
			return err
		}, func(ctx context.Context, archive string) error {
			_, err := observationpublication.VerifyArchive(ctx, archive)
			return err
		}, observationpublication.RestoreArchive, 4, 5},
		{"relationship", func(ctx context.Context, root, archive string) error {
			_, err := relationshippublication.CreateArchive(ctx, root, archive)
			return err
		}, func(ctx context.Context, archive string) error {
			_, err := relationshippublication.VerifyArchive(ctx, archive)
			return err
		}, relationshippublication.RestoreArchive, 2, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("TMPDIR", root)
			archive := filepath.Join(root, "archive.tar")
			calls := 0
			ctx := custodybytes.WithCheckpoint(t.Context(), func(context.Context) error {
				calls++
				if info, err := os.Stat(archive); err != nil || info.Size() == 0 {
					t.Fatalf("archive not live at checkpoint: %v", err)
				}
				return nil
			})
			if err := tt.create(ctx, filepath.Join(root, "absent"), archive); err != nil || calls != tt.creates {
				t.Fatalf("create checks=%d error=%v", calls, err)
			}
			calls = 0
			if err := tt.verify(ctx, archive); err != nil || calls != tt.checks {
				t.Fatalf("verify checks=%d want=%d error=%v", calls, tt.checks, err)
			}
			calls = 0
			if err := tt.restore(ctx, archive, filepath.Join(root, "installed")); err != nil || calls != 2 {
				t.Fatalf("restore checks=%d error=%v", calls, err)
			}
			for failAt := 1; failAt <= tt.checks; failAt++ {
				calls = 0
				refused := errors.New("private checkpoint refused")
				failed := custodybytes.WithCheckpoint(t.Context(), func(context.Context) error {
					calls++
					if calls == failAt {
						return refused
					}
					return nil
				})
				if err := tt.verify(failed, archive); !errors.Is(err, refused) || calls < failAt || calls > tt.checks {
					t.Fatalf("verify failure at %d: checks=%d error=%v", failAt, calls, err)
				}
			}
			for failAt := 1; failAt <= tt.creates; failAt++ {
				calls = 0
				refused := errors.New("private create checkpoint refused")
				failed := custodybytes.WithCheckpoint(t.Context(), func(context.Context) error {
					calls++
					if calls == failAt {
						return refused
					}
					return nil
				})
				output := filepath.Join(t.TempDir(), "failed.tar")
				if err := tt.create(failed, filepath.Join(root, "absent"), output); !errors.Is(err, refused) || calls > tt.creates+1 {
					t.Fatalf("create failure at %d: checks=%d error=%v", failAt, calls, err)
				}
			}
			if tt.checks != 0 {
				t.Run("actual_cleanup_error", func(t *testing.T) {
					parent := t.TempDir()
					work, saved := filepath.Join(parent, "work"), filepath.Join(parent, "saved")
					if err := os.Mkdir(work, 0o700); err != nil {
						t.Fatal(err)
					}
					t.Setenv("TMPDIR", work)
					archive := filepath.Join(work, "archive.tar")
					if err := tt.create(t.Context(), filepath.Join(work, "absent"), archive); err != nil {
						t.Fatal(err)
					}
					displaced := false
					restore := func() {
						if !displaced {
							return
						}
						if err := os.Remove(work); err != nil && !errors.Is(err, os.ErrNotExist) {
							t.Error(err)
						}
						if err := os.Rename(saved, work); err != nil {
							t.Error(err)
						}
						displaced = false
					}
					t.Cleanup(restore)
					calls := 0
					ctx := custodybytes.WithCheckpoint(t.Context(), func(context.Context) error {
						calls++
						if calls == tt.checks-1 {
							// Only this fixture's parent is displaced. The temporary
							// self-link produces deterministic ELOOP, not a privilege-
							// dependent permission denial or an escaped cleanup root.
							if err := os.Rename(work, saved); err != nil {
								t.Fatal(err)
							}
							displaced = true
							if err := os.Symlink("work", work); err != nil {
								t.Fatal(err)
							}
						}
						if calls == tt.checks {
							restore()
						}
						return nil
					})
					if err := tt.verify(ctx, archive); !errors.Is(err, syscall.ELOOP) || calls != tt.checks {
						t.Fatalf("cleanup failure became a successful boundary: checks=%d error=%v", calls, err)
					}
					entries, err := os.ReadDir(work)
					if err != nil || len(entries) < 2 {
						t.Fatalf("failed temporary population was not retained: %v %v", entries, err)
					}
				})
			}
		})
	}
}

func TestArchiveCheckpointSelectedRestorePreservesOldStage(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	stage := target + ".observation-restore"
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(stage, "entry.restore-part")
	const retained = "retained partial source"
	if err := os.WriteFile(partial, []byte(retained), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ctx := custodybytes.WithCheckpoint(t.Context(), func(context.Context) error { calls++; return nil })
	_, err := Restore(ctx, RestoreOptions{Options: Options{DataDir: target}, Backup: filepath.Join(root, "absent-backup")})
	if err == nil || !strings.Contains(err.Error(), "selected observation restore stage") || calls != 0 {
		t.Fatalf("selected Restore did not refuse before backup/replay: %v checks=%d", err, calls)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected refusal created target: %v", err)
	}
	// The actual helper also rechecks at use, independently of the top-level
	// preflight, before opening an archive or removing any old partial file.
	if err := observationpublication.RestoreArchiveWithStage(ctx, filepath.Join(root, "absent.tar"), target, stage); err == nil || !strings.Contains(err.Error(), "absent observation stage") {
		t.Fatalf("actual-use stage guard: %v", err)
	}
	if raw, err := os.ReadFile(partial); err != nil || string(raw) != retained {
		t.Fatalf("retained partial altered: %q %v", raw, err)
	}
}
