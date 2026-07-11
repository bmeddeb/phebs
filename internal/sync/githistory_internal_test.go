package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Overflow must stop the producer, not merely discard everything after the
// retained prefix. The alias writes a large stream and only creates the marker
// after a delay if it reaches normal completion; set -e makes SIGPIPE/kill
// terminate the alias before that side effect.
func TestRunGitLimitedStopsProducerOnOverflow(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "producer-finished")
	t.Setenv("PHEBS_OVERFLOW_MARKER", marker)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, truncated, err := runGitLimited(ctx, t.TempDir(), 1024,
		"-c", `alias.phebs-overflow=!set -e; dd if=/dev/zero bs=1048576 count=256 2>/dev/null; sleep 1; touch "$PHEBS_OVERFLOW_MARKER"`,
		"phebs-overflow")
	if err != nil {
		t.Fatalf("runGitLimited: %v", err)
	}
	if !truncated || len(out) != 1024 {
		t.Fatalf("output len=%d truncated=%v, want 1024/true", len(out), truncated)
	}
	// Wait past the alias's post-output delay so an accidentally orphaned shell
	// cannot create the marker after this assertion.
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("producer continued after overflow: marker err=%v", err)
	}
}

// Each 128-ID batch fits independently, but the combined response must share
// one budget. This regresses the old N*limit allocation behavior in blame.
func TestLoadCommitsUsesAggregateOutputBudget(t *testing.T) {
	dir := t.TempDir()
	gitc(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, dir, "add", "file.txt")
	gitc(t, dir, "commit", "-m", "metadata budget fixture")
	oid := strings.TrimSpace(gitc(t, dir, "rev-parse", "HEAD"))

	batchIDs := make([]string, 128)
	for i := range batchIDs {
		batchIDs[i] = oid
	}
	args := []string{"show", "-s", "--no-show-signature", "-z", "--format=" + commitFormat}
	args = append(args, batchIDs...)
	oneBatch, truncated, err := runGitLimited(context.Background(), dir, 1<<20, args...)
	if err != nil || truncated || len(oneBatch) == 0 {
		t.Fatalf("measure one metadata batch: len=%d truncated=%v err=%v", len(oneBatch), truncated, err)
	}
	budget := int64(len(oneBatch) + len(oneBatch)/2)
	if _, err := loadCommits(context.Background(), dir, batchIDs, budget); err != nil {
		t.Fatalf("one batch should fit aggregate budget %d: %v", budget, err)
	}

	allIDs := append(append([]string(nil), batchIDs...), batchIDs...)
	if _, err := loadCommits(context.Background(), dir, allIDs, budget); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("two batches with aggregate budget %d: err=%v, want ErrTooLarge", budget, err)
	}
}
