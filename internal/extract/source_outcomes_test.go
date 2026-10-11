package extract

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

func TestSourceOutcomeReceiptRequiresCompleteSuccessfulPartition(t *testing.T) {
	plan, _, commit := partitionExecutorPlan(t)
	for _, mode := range []string{"complete", "missing", "duplicate", "outside", "invalid", "reason", "digest", "cancelled", "aborted"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			corpus := newVerifiedCorpus(partitionExecutorCorpus{repository: plan.Repository, commit: commit})
			corpus.enumerated = map[string]struct{}{"zero.go": {}, "go.mod": {}}
			corpus.sources["zero.go"] = sourceRecord{digest: "sha256:" + strings.Repeat("a", 64)}
			capture, err := beginPartitionSourceOutcomes(root, corpus, plan, 0, "run")
			if err != nil {
				t.Fatal(err)
			}
			defer capture.close()
			outcome := sdk.SourceOutcome{Path: "zero.go", State: "analyzed", Reason: "source_scan_complete", BlobDigest: "sha256:" + strings.Repeat("a", 64)}
			if mode != "missing" && mode != "invalid" && mode != "reason" && mode != "digest" {
				if err := capture.append(outcome); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "duplicate":
				if err := capture.append(outcome); err == nil {
					t.Fatal("duplicate accepted")
				}
				return
			case "outside":
				outcome.Path = "other.go"
				if err := capture.append(outcome); err == nil {
					t.Fatal("outside source accepted")
				}
				return
			case "invalid", "reason", "digest":
				switch mode {
				case "invalid":
					outcome.State = "invented"
				case "reason":
					outcome.Reason = "read_complete"
				case "digest":
					outcome.BlobDigest = "sha256:" + strings.Repeat("b", 64)
				}
				if err := capture.append(outcome); err == nil {
					t.Fatal("invalid outcome accepted")
				}
				return
			case "aborted":
				capture.close()
				files, _ := os.ReadDir(root)
				if len(files) != 0 {
					t.Fatal("aborted partition left receipt")
				}
				return
			}
			ctx := t.Context()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			spec := candidate.PartitionResultSpec{Disposition: candidate.PartitionResultEmpty}
			err = capture.finish(ctx, plan, 0, spec)
			if mode != "complete" {
				if err == nil {
					t.Fatal("incomplete partition accepted")
				}
				files, _ := filepath.Glob(filepath.Join(root, "*.jsonl"))
				if len(files) != 0 {
					t.Fatal("failed partition installed receipt")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(root, "*.jsonl"))
			if err != nil || len(files) != 1 {
				t.Fatalf("files = %v, %v", files, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 3 {
				t.Fatalf("receipt lines = %d", len(lines))
			}
			var footer struct {
				Result candidate.PartitionResult `json:"result"`
			}
			if err := json.Unmarshal([]byte(lines[2]), &footer); err != nil {
				t.Fatal(err)
			}
			expected, err := candidate.BuildPartitionResult(plan, 0, spec)
			if err != nil {
				t.Fatal(err)
			}
			if footer.Result != expected {
				t.Fatal("completion does not bind exact partition result")
			}
			info, _ := os.Stat(files[0])
			if info.Mode().Perm() != 0o600 {
				t.Fatal("receipt is not private")
			}
		})
	}
}

func TestSourceOutcomeReceiptRetryCollisionAndByteBound(t *testing.T) {
	plan, _, commit := partitionExecutorPlan(t)
	root := t.TempDir()
	corpus := newVerifiedCorpus(partitionExecutorCorpus{repository: plan.Repository, commit: commit})
	corpus.enumerated = map[string]struct{}{"zero.go": {}}
	corpus.sources["zero.go"] = sourceRecord{digest: "sha256:" + strings.Repeat("a", 64)}
	finish := func(run string) error {
		capture, err := beginPartitionSourceOutcomes(root, corpus, plan, 0, run)
		if err != nil {
			return err
		}
		defer capture.close()
		if err := capture.append(sdk.SourceOutcome{Path: "zero.go", State: "analyzed", Reason: "source_scan_complete", BlobDigest: corpus.sources["zero.go"].digest}); err != nil {
			return err
		}
		return capture.finish(t.Context(), plan, 0, candidate.PartitionResultSpec{Disposition: candidate.PartitionResultEmpty})
	}
	if err := finish("run"); err != nil {
		t.Fatal(err)
	}
	if err := finish("run"); err != nil {
		t.Fatalf("exact retry refused: %v", err)
	}
	if err := finish("different-run"); err == nil {
		t.Fatal("different run replaced retained receipt")
	}
	link := filepath.Join(root, "receipt-link")
	files, err := filepath.Glob(filepath.Join(root, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("installed files = %v, %v", files, err)
	}
	if err := os.Symlink(files[0], link); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceOutcomeFileDigest(link); err == nil {
		t.Fatal("receipt collision followed symlink")
	}

	file, err := os.CreateTemp(t.TempDir(), "bounded")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	writer := sourceOutcomeWriter{file: file, remaining: 1}
	if _, err := writer.Write([]byte("xx")); err == nil {
		t.Fatal("byte bound not enforced")
	}
}
