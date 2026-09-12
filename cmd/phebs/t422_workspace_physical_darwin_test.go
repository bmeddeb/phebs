//go:build darwin

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// The middle value is a real tiny native traversal. Earlier positions and the
// reopen outcome are modeled; no engine/author/selected HTTP proof is claimed.
func TestT422PhysicalWorkspaceCompletedPrefix(t *testing.T) {
	for _, mode := range []string{"ready", "reopen_failure", "cancel_after_walk", "ready_before_walk", "duplicate_ready", "finish_before_ready"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil || os.Chmod(root, 0700) != nil || os.WriteFile(filepath.Join(root, "actual"), []byte("retained actual bytes"), 0600) != nil {
				t.Fatal("tiny workspace")
			}
			held, err := os.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = held.Close() }()
			binding, err := dispatchadmission.DescribeProductionWorkspace(held, root)
			info, statErr := held.Stat()
			if err != nil || statErr != nil {
				t.Fatal("held workspace", err, statErr)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			observer := custodybytes.NewBorrowed(held, root, info, binding.FSID)
			var output bytes.Buffer
			state := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
			reports := &t422WorkspaceReports{writer: &output, initial: state}
			for _, phase := range []uint32{2, 3, 3, 4} {
				state.Phase = phase
				if reports.begin(state) != nil || reports.complete(custodybytes.Sample{}) != nil {
					t.Fatal("supplied earlier framing")
				}
			}
			if mode == "ready_before_walk" {
				if reports.physicalReopenReady() == nil {
					t.Fatal("ready before middle sample")
				}
				return
			}
			if reports.begin(state) != nil {
				t.Fatal("middle begin")
			}
			value, err := observer.Sample(ctx, 4)
			if err != nil || value.LogicalBytes == 0 || value.AllocatedBytes == 0 {
				t.Fatal("actual traversal", value, err)
			}
			if mode == "cancel_after_walk" {
				cancel()
			}
			if reports.complete(value) != nil {
				t.Fatal("committed sample lost after cancellation")
			}
			success := fmt.Sprintf("WB1:2:4S:0000000000000005:%016x:%016x\n", value.LogicalBytes, value.AllocatedBytes)
			if !strings.HasSuffix(output.String(), success) || output.Len() != 5*86 {
				t.Fatal("actual S not retained")
			}
			switch mode {
			case "reopen_failure", "cancel_after_walk":
				_ = observer.Fail()
				_ = reports.failed()
				if reports.physicalReopenReady() == nil || reports.begin(state) == nil || strings.Contains(output.String(), ":4R:") {
					t.Fatal("failure emitted ready/finish")
				}
				if got := observer.Snapshot(); !got.Unavailable || !got.Phases[3].Completed || got.Phases[3].Maximum != value {
					t.Fatal("failed continuation lost actual completed walk", got)
				}
			case "finish_before_ready":
				if reports.begin(state) == nil {
					t.Fatal("finish before ready")
				}
			default:
				if reports.physicalReopenReady() != nil || !strings.HasSuffix(output.String(), "WB1:2:4R:0000000000000005\n") || output.Len() != 5*86+26 {
					t.Fatal("fixed readiness record")
				}
				if mode == "duplicate_ready" {
					if reports.physicalReopenReady() == nil || output.Len() != 5*86+26 {
						t.Fatal("duplicate readiness")
					}
				}
			}
			if !strings.Contains(output.String(), success) {
				t.Fatal("failure erased S")
			}
		})
	}
}
