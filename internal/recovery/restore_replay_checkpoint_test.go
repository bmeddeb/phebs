package recovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
)

func TestRestoreReplayCustodyCheckpoints(t *testing.T) {
	for failAt := 0; failAt <= 6; failAt++ {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT; INSERT [{id: repo:one}];")
			prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = prepared.close() }()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if r.URL.Path == "/sql" {
					_, _ = io.WriteString(w, `[{"result":null,"status":"OK","time":"0ns","type":null},{"result":null,"status":"OK","time":"0ns","type":null},{"result":null,"status":"OK","time":"0ns","type":null}]`)
					return
				}
				_, _ = io.WriteString(w, `[{"result":[],"status":"OK","time":"0ns","type":null},{"result":null,"status":"OK","time":"0ns","type":null}]`)
			}))
			defer server.Close()
			target := t.TempDir()
			calls := 0
			refused := errors.New("private checkpoint refused")
			ctx := custodybytes.WithCheckpoint(t.Context(), func(context.Context) error {
				calls++
				if calls == failAt {
					return refused
				}
				return nil
			})
			err = executeRestoreReplay(ctx, prepared, target, strings.Replace(server.URL, "http://", "ws://", 1), DatabaseIdentity{Namespace: "phebs", Database: "phebs"}, nil)
			if failAt == 0 {
				if err != nil || calls != 6 {
					t.Fatalf("success calls=%d error=%v", calls, err)
				}
			} else if !errors.Is(err, refused) || prepared.terminal == nil {
				t.Fatalf("failure at %d was not terminal: calls=%d error=%v terminal=%v", failAt, calls, err, prepared.terminal)
			}
			if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
				t.Fatalf("spool custody remains: %v %v", entries, err)
			}
		})
	}
}

func TestRestoreReplaySpoolCheckpointSeesNamedConsumedBytes(t *testing.T) {
	path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT; INSERT [{id: repo:one}];")
	prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.close() }()
	directory := t.TempDir()
	calls := 0
	ctx := custodybytes.WithCheckpoint(t.Context(), func(context.Context) error {
		calls++
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 1 {
			t.Fatalf("missing live spool: %v %v", entries, err)
		}
		info, err := entries[0].Info()
		if err != nil || info.Size() == 0 || info.Mode().Perm() != 0o400 {
			t.Fatalf("spool is not the protected consumed-byte file: %v %v", info, err)
		}
		return nil
	})
	file, _, err := prepared.spoolNext(ctx, directory)
	if err != nil || calls != 1 {
		t.Fatalf("spool checkpoint calls=%d error=%v", calls, err)
	}
	_ = file.Close()
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("successful spool remained linked: %v %v", entries, err)
	}
}
