package observationpublication

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

func TestParsedBlobNativeBoundary(t *testing.T) {
	for _, mode := range []string{"parsed", "nil_metrics", "cached", "parse_error", "invalid_utf8", "sink_error"} {
		t.Run(mode, func(t *testing.T) {
			stage := &Stage{directory: t.TempDir(), priorRead: true}
			content := []byte("package demo\nconst A = 1\n")
			switch mode {
			case "parse_error":
				content = []byte("package demo\nfunc broken(\n")
			case "invalid_utf8":
				content = []byte{0xff}
			}
			blob := sourcepartition.BlobRecord{ObjectID: strings.Repeat("1", 40), DeclaredBytes: int64(len(content))}
			if mode == "cached" {
				if _, _, err := stage.observe(t.Context(), blob, content, nil); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			failure := errors.New("sink failure")
			ctx, err := readaccounting.WithParsedBlobObserver(t.Context(), func() error {
				calls++
				if mode == "sink_error" {
					return failure
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			metrics := &Metrics{}
			if mode == "nil_metrics" {
				metrics = nil
			}
			record, _, err := stage.observe(ctx, blob, content, metrics)
			if mode == "sink_error" {
				if !errors.Is(err, failure) {
					t.Fatal("sink failure lost", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "cached" || mode == "parse_error" || mode == "invalid_utf8" {
				want = 0
			}
			if calls != want || (metrics != nil && metrics.ParsedBlobs != calls) {
				t.Fatal("event changed native counter", calls, metrics)
			}
			if (mode == "parse_error" || mode == "invalid_utf8") && (record.State != "unsupported" || record.Reason != mode) {
				t.Fatal("unsupported parse reclassified", record)
			}
		})
	}
}

func TestParsedBlobInventoryReuseAndRetry(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"), "copy/a.go": []byte("package demo\nconst A = 1\n"),
		"b.go": []byte("package demo\nconst B = 2\n"), "bad.go": []byte("package demo\nfunc broken(\n"),
	})
	_, plan, _ := buildInventorySuperPlanV2(t, repository, commit, "example/parsed-events")
	calls := 0
	ctx, err := readaccounting.WithParsedBlobObserver(t.Context(), func() error { calls++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	var metrics InventoryMetricsV2
	request := InventoryBuildRequestV2{OutputDirectory: output, RepositoryDirectory: repository, Plan: plan, Metrics: &metrics}
	root, err := BuildInventoryStageV2(ctx, request)
	if err != nil || calls != 2 || metrics.ParsedBlobs != 2 || root.UnsupportedCount != 1 {
		t.Fatal("cold event parity", calls, metrics, err)
	}
	for _, mode := range []string{"root_noop", "member_reuse", "completed_segment", "discarded_segment"} {
		t.Run(mode, func(t *testing.T) {
			before := calls
			next := request
			want := 0
			switch mode {
			case "member_reuse":
				next.OutputDirectory, next.PriorDirectory = t.TempDir(), output
			case "completed_segment", "discarded_segment":
				if err := os.Remove(filepath.Join(output, InventoryRootNameV2)); err != nil {
					t.Fatal(err)
				}
				if mode == "discarded_segment" {
					member := filepath.Join(output, root.Segments[0].Directory, inventoryMemberNameV2(0))
					if err := os.WriteFile(member, []byte("broken\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					want = 2
				}
			}
			rebuilt, err := BuildInventoryStageV2(ctx, next)
			if err != nil || rebuilt.Digest != root.Digest || calls-before != want || metrics.ParsedBlobs != want {
				t.Fatal("replay native event parity", calls-before, metrics, err)
			}
			if mode == "member_reuse" && (metrics.ReadBlobs != 0 || metrics.ReusedMembers != root.MemberCount) {
				t.Fatal("unchanged members reparsed", metrics)
			}
		})
	}
}

func TestParsedBlobInventoryFailedPrefix(t *testing.T) {
	repository, commit := observationFixture(t, map[string][]byte{
		"a.go": []byte("package demo\nconst A = 1\n"), "b.go": []byte("package demo\nconst B = 2\n"),
	})
	_, plan, _ := buildInventorySuperPlanV2(t, repository, commit, "example/parsed-prefix")
	output := t.TempDir()
	calls := 0
	ctx, err := readaccounting.WithParsedBlobObserver(t.Context(), func() error {
		calls++
		if calls == 1 {
			// Force a later segment-control collision, after successful object
			// events. No publication/root cardinality can recover this prefix.
			return os.WriteFile(filepath.Join(output, inventorySegmentDirectoryV2(0), "segment.json"), []byte("broken\n"), 0o600)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var metrics InventoryMetricsV2
	_, err = BuildInventoryStageV2(ctx, InventoryBuildRequestV2{
		OutputDirectory: output, RepositoryDirectory: repository, Plan: plan, Metrics: &metrics,
	})
	if err == nil || calls != 2 || metrics.ParsedBlobs != 2 {
		t.Fatal("failed publication erased native prefix", calls, metrics, err)
	}
	if _, err := os.Stat(filepath.Join(output, InventoryRootNameV2)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed prefix became a root", err)
	}
}
