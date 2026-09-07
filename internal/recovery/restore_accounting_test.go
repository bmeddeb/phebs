//go:build darwin || linux

package recovery

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func restoreAccountingFixture(t *testing.T) (*storeaccounting.SDKOwner, *storeaccounting.Controller) {
	t.Helper()
	controller, err := storeaccounting.New(t.Context(), storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: 11, Calls: 1, Transactions: 1}},
		Phases:    []storeaccounting.Phase{{ID: 12, Transactions: 20, Rows: 2000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(t.Context(), controller, storeaccounting.WireConfig{
		Producers: []storeaccounting.WireProducer{{ID: 11, Binding: [32]byte{1}, Phases: 2048}}, AckTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	file, config, err := transport.Open(11)
	if err != nil {
		t.Fatal(err)
	}
	client, err := storeaccounting.NewClient(t.Context(), file, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	owner, err := storeaccounting.NewSDKOwner(client)
	if err != nil {
		t.Fatal(err)
	}
	return owner, controller
}

func TestRestoreAccountingHTTPPrefix(t *testing.T) {
	const ok = `{"result":null,"status":"OK","time":"0ns","type":null}`
	for _, failAt := range []int32{0, 1, 2, 3, 4, 5} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			owner, controller := restoreAccountingFixture(t)
			rows := strings.TrimSuffix(strings.Repeat("{id:repo:one},", 513), ",")
			path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT;\nDEFINE TABLE repo TYPE ANY SCHEMALESS PERMISSIONS NONE;\nINSERT ["+rows+"];\n")
			prepared, err := prepareRestoreReplay(t.Context(), path, artifact)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = prepared.close() }()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ordinal := calls.Add(1)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
				}
				prefix, err := controller.Snapshot()
				wantRows := [...]uint64{0, 1, 2, 3, 515, 516}[ordinal]
				if err != nil || prefix.Transactions != uint64(ordinal) || prefix.Rows != wantRows || prefix.Producers[0].Calls != 1 {
					t.Errorf("HTTP preceded accounting: %+v %v", prefix, err)
				}
				if ordinal == failAt {
					// HTTP 200 with a failed COMMIT is never a settled success.
					_, _ = io.WriteString(w, "["+ok+`,{"status":"ERR","result":"failed","time":"0ns","type":null}]`)
					return
				}
				first := ok
				if ordinal >= 4 {
					first = strings.Replace(ok, `"result":null`, `"result":[]`, 1)
				}
				if ordinal <= 2 {
					first += "," + ok
				}
				_, _ = io.WriteString(w, "["+first+","+ok+"]")
			}))
			defer server.Close()
			err = executeRestoreReplay(t.Context(), prepared, t.TempDir(), strings.Replace(server.URL, "http://", "ws://", 1), DatabaseIdentity{Namespace: "phebs", Database: "phebs"}, owner)
			wantCalls := failAt
			if failAt == 0 {
				wantCalls = 5
			}
			if (err == nil) != (failAt == 0) || calls.Load() != wantCalls {
				t.Fatalf("error=%v calls=%d want=%d", err, calls.Load(), wantCalls)
			}
			prefix, _ := controller.Snapshot()
			if prefix.Transactions != uint64(wantCalls) || prefix.Rows != [...]uint64{0, 1, 2, 3, 515, 516}[wantCalls] {
				t.Fatalf("attempted prefix lost: %+v", prefix)
			}
			if failAt != 0 {
				if prefix.Producers[0].Calls != 1 || owner.Checkpoint(t.Context()) == nil || owner.Close(t.Context()) == nil {
					t.Fatal("uncertain native result settled")
				}
				return
			}
			if prefix.MaximumRows != 512 || prefix.Producers[0].Calls != 0 {
				t.Fatalf("bounded replay did not settle: %+v", prefix)
			}
			if err := controller.Fence(); err != nil {
				t.Fatal(err)
			}
			if err := owner.Checkpoint(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := owner.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRestoreAccountingPreflightFallback(t *testing.T) {
	for _, mode := range []string{"supported", "unsupported", "version", "missing artifact"} {
		for _, selected := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/selected=%t", mode, selected), func(t *testing.T) {
				owner, controller := restoreAccountingFixture(t)
				if !selected {
					owner = nil
				}
				raw := "OPTION IMPORT; INSERT [{id:repo:one}];"
				if mode == "unsupported" {
					raw = "OPTION IMPORT; DEFINE FUNCTION fn::custom() {};"
				}
				path, artifact := restoreReplayTestArtifact(t, raw)
				manifest := Manifest{Surreal: ToolIdentity{Version: "3.2.0"}, Inventory: []Artifact{artifact}}
				if mode == "version" {
					manifest.Surreal.Version = "3.1.0"
				}
				if mode == "missing artifact" {
					manifest.Inventory = nil
				}
				prepared, err := prepareRestoreReplayForManifest(t.Context(), filepath.Dir(path), manifest, owner)
				if prepared != nil {
					defer func() { _ = prepared.close() }()
				}
				if (err != nil) != (selected && mode != "supported") || (prepared != nil) != (mode == "supported") {
					t.Fatalf("prepared=%t error=%v", prepared != nil, err)
				}
				prefix, _ := controller.Snapshot()
				if prefix.Transactions != 0 || prefix.Rows != 0 {
					t.Fatalf("preflight invented native work: %+v", prefix)
				}
			})
		}
	}
}

type restoreAccountingTransport func(*http.Request) (*http.Response, error)

func (transport restoreAccountingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type restoreAccountingResponseBody struct {
	io.Reader
	close func() error
}

func (body restoreAccountingResponseBody) Close() error { return body.close() }

func TestRestoreAccountingResponseClosure(t *testing.T) {
	for _, mode := range []string{"success", "close error", "late cancellation"} {
		t.Run(mode, func(t *testing.T) {
			owner, controller := restoreAccountingFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			closed := false
			transport := restoreAccountingTransport(func(request *http.Request) (*http.Response, error) {
				if _, err := io.Copy(io.Discard, request.Body); err != nil {
					return nil, err
				}
				if err := request.Body.Close(); err != nil {
					return nil, err
				}
				const raw = `[{"result":[],"status":"OK","time":"0ns","type":null},{"result":null,"status":"OK","time":"0ns","type":null}]`
				body := restoreAccountingResponseBody{Reader: strings.NewReader(raw), close: func() error {
					closed = true
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Producers[0].Calls != 1 {
						t.Errorf("call settled before response close: %+v %v", prefix, err)
					}
					if mode == "close error" {
						return io.ErrClosedPipe
					}
					if mode == "late cancellation" {
						cancel()
					}
					return nil
				}}
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})
			err := submitRestoreReplayRequest(ctx, &http.Client{Transport: transport}, "http://127.0.0.1:1/import",
				DatabaseIdentity{Namespace: "phebs", Database: "phebs"}, strings.NewReader("closed native transaction"), 25, false, false, 1, owner)
			prefix, _ := controller.Snapshot()
			wantCalls := 1
			if mode == "success" {
				wantCalls = 0
			}
			if !closed || (err == nil) != (mode == "success") || prefix.Transactions != 1 || prefix.Rows != 1 || prefix.Producers[0].Calls != wantCalls {
				t.Fatalf("closed=%t error=%v prefix=%+v", closed, err, prefix)
			}
		})
	}
}

// This opt-in test composes actual bounded native replay with the mechanical
// acknowledged owner. It is not a protected production-image launch, complete
// Restore, phase-wide admission, or native hard-death/cleanup rehearsal.
func TestRestoreAccountingNativeReplay(t *testing.T) {
	if os.Getenv("PHEBS_TEST_RESTORE_REPLAY_NATIVE") != "1" {
		t.Skip("native acknowledged replay not selected")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	owner, controller := restoreAccountingFixture(t)
	rows := make([]string, 513)
	for index := range rows {
		rows[index] = fmt.Sprintf("{id: repo:neutral%d, value: %d}", index, index)
	}
	path, artifact := restoreReplayTestArtifact(t, "OPTION IMPORT;\nDEFINE TABLE repo TYPE ANY SCHEMALESS PERMISSIONS NONE;\nINSERT ["+strings.Join(rows, ", ")+"];\n")
	prepared, err := prepareRestoreReplay(ctx, path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.close() }()
	if prepared.census != (restoreReplayCensus{Units: 3, Definitions: 1, Records: 513}) {
		t.Fatalf("neutral input census differs: %+v", prepared.census)
	}
	target := t.TempDir()
	runtime, stop, err := store.StartLocalImport(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if runtime.Surreal.Version != "3.2.0" {
		t.Fatal("unproven native replay engine")
	}
	if err := executeRestoreReplay(ctx, prepared, target, runtime.Endpoint, DatabaseIdentity{Namespace: "phebs", Database: "phebs"}, owner); err != nil {
		t.Fatal(err)
	}
	prefix, err := controller.Snapshot()
	if err != nil || prefix.Transactions != 5 || prefix.Rows != 516 || prefix.MaximumRows != 512 || prefix.Producers[0].Calls != 0 {
		t.Fatalf("actual replay prefix differs: %+v %v", prefix, err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// Independent read-only test verification deliberately uses an ordinary SDK
	// connection after accounting closes; it cannot issue production evidence.
	db, err := surrealdb.FromEndpointURLString(ctx, runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Use(ctx, "phebs", "phebs"); err != nil {
		t.Fatal(err)
	}
	count, err := surrealdb.Query[[]uint64](ctx, db, "SELECT VALUE count() FROM repo GROUP ALL;", nil)
	if err != nil || count == nil || len(*count) != 1 || len((*count)[0].Result) != 1 || (*count)[0].Result[0] != 513 {
		t.Fatalf("neutral native row count differs: %v %v", count, err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Log("actual native bounded replay and SA01 composition: 5 attempted transactions/516 submitted rows/max512, drained owner, independent native513-row read; no full Restore or producer-admission claim")
}
