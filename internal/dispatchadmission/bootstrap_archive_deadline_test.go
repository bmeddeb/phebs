//go:build darwin || linux

package dispatchadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func TestProductionArchiveDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	for _, producer := range []uint32{10, 11} {
		for _, mode := range []string{"valid", "missing_deadline", "expired", "negative", "no_workspace", "foreign_producer", "semantic", "owner_control", "later_phase"} {
			t.Run(mode+"/"+strconv.Itoa(int(producer)), func(t *testing.T) {
				record := productionStoreTestRecord()
				record.Producer.ID, record.Store.Producer = producer, producer
				record.Control.MaximumPhases = 1
				record.Workspace = &ProductionWorkspaceBinding{Path: "/private/owned-workspace", Inode: 1, FSID: [2]int32{1, 2}}
				record.ArchiveDeadlineUnixNano = deadline.UnixNano()
				switch mode {
				case "missing_deadline":
					record.ArchiveDeadlineUnixNano = 0
				case "expired":
					record.ArchiveDeadlineUnixNano = time.Now().Add(-time.Second).UnixNano()
				case "negative":
					record.ArchiveDeadlineUnixNano = -1
				case "no_workspace":
					record.Workspace = nil
				case "foreign_producer":
					record.Producer.ID = 6
				case "semantic":
					record.SemanticMode = ProductionSemanticV3
				case "owner_control":
					record.Control.OwnerControl = true
				case "later_phase":
					record.Phase = 13
				}
				ctx, cancel, err := record.archiveContext(t.Context())
				if cancel != nil {
					defer cancel()
				}
				if mode != "valid" {
					if err == nil || ctx != nil {
						t.Fatal("invalid deadline/profile accepted", err)
					}
					return
				}
				if err != nil || record.validate() != nil {
					t.Fatal("closed archive profile refused", err)
				}
				got, ok := ctx.Deadline()
				if !ok || !got.Equal(deadline) {
					t.Fatal("parent deadline was restarted", got, deadline)
				}
				earlier := deadline.Add(-30 * time.Second)
				parent, stop := context.WithDeadline(t.Context(), earlier)
				defer stop()
				clipped, done, err := record.archiveContext(parent)
				if err != nil {
					t.Fatal(err)
				}
				defer done()
				got, ok = clipped.Deadline()
				if !ok || !got.Equal(earlier) {
					t.Fatal("shorter inherited deadline extended", got)
				}
				stop()
				if clipped.Err() != context.Canceled {
					t.Fatal("parent cancellation lost", clipped.Err())
				}
			})
		}
	}
	legacy := productionTestRecord()
	ctx, cancel, err := legacy.archiveContext(t.Context())
	if err != nil || cancel != nil || ctx != t.Context() {
		t.Fatal("legacy acquired deadline state", err)
	}
	raw, err := json.Marshal(legacy)
	if err != nil || bytes.Contains(raw, []byte("ArchiveDeadline")) {
		t.Fatal("legacy bootstrap bytes changed", err)
	}
}
