package store

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/gorilla/websocket"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

func TestLocalScopeInitializationWireAndFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		remote  bool
		failAt  int
		failure string
		stage   string
	}{
		{"signin", false, 0, "rpc", "signin:"},
		{"namespace_rpc", false, 1, "rpc", "define local namespace:"},
		{"namespace_statement", false, 1, "statement", "define local namespace:"},
		{"namespace_empty", false, 1, "empty", "define local namespace:"},
		{"namespace_extra", false, 1, "extra", "define local namespace:"},
		{"namespace_status", false, 1, "status", "define local namespace:"},
		{"namespace_use", false, 2, "rpc", "select local namespace:"},
		{"database_rpc", false, 3, "rpc", "define local database:"},
		{"database_statement", false, 3, "statement", "define local database:"},
		{"database_empty", false, 3, "empty", "define local database:"},
		{"database_extra", false, 3, "extra", "define local database:"},
		{"database_status", false, 3, "status", "define local database:"},
		{"database_use", false, 4, "rpc", "use phebs/phebs:"},
		{"schema", false, 5, "rpc", "apply schema:"},
		{"generic_remote_unchanged", true, 2, "rpc", "apply schema:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			codec := surrealcbor.New()
			var calls []localScopeWireRequest
			joined := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(joined)
				if r.URL.Path != "/rpc" {
					t.Errorf("unexpected request path %q", r.URL.Path)
					return
				}
				upgrader := websocket.Upgrader{Subprotocols: []string{"cbor"}}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer func() { _ = conn.Close() }()
				deadline, _ := ctx.Deadline()
				if err := conn.SetReadDeadline(deadline); err != nil {
					t.Error(err)
					return
				}
				if err := conn.SetWriteDeadline(deadline); err != nil {
					t.Error(err)
					return
				}
				conn.SetReadLimit(1 << 20)
				for {
					kind, body, err := conn.ReadMessage()
					if err != nil {
						return
					}
					if kind != websocket.BinaryMessage || len(calls) > test.failAt {
						t.Error("unexpected non-CBOR call or call after failed initialization")
						return
					}
					var request localScopeWireRequest
					if err := codec.Unmarshal(body, &request); err != nil {
						t.Error(err)
						return
					}
					position := len(calls)
					calls = append(calls, request)
					var result any
					switch request.Method {
					case "signin":
						result = "neutral-test-token"
					case "query":
						result = []surrealdb.QueryResult[any]{{Status: "OK"}}
					case "use":
					default:
						t.Errorf("unexpected method %q", request.Method)
						return
					}
					response := map[string]any{"id": request.ID, "result": &result}
					if position == test.failAt {
						switch test.failure {
						case "rpc":
							delete(response, "result")
							response["error"] = map[string]any{"code": -32603, "message": "neutral initialization refusal"}
						case "statement":
							result = []surrealdb.QueryResult[any]{{Status: "ERR", Result: "neutral statement refusal"}}
						case "empty":
							result = []surrealdb.QueryResult[any]{}
						case "extra":
							result = []surrealdb.QueryResult[any]{{Status: "OK"}, {Status: "OK"}}
						case "status":
							result = []surrealdb.QueryResult[any]{{Status: "unknown"}}
						}
					}
					body, err = codec.Marshal(response)
					if err != nil {
						t.Error(err)
						return
					}
					if err := conn.WriteMessage(websocket.BinaryMessage, body); err != nil {
						t.Error(err)
						return
					}
				}
			}))
			defer server.Close()
			endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
			var state *Surreal
			var err error
			if test.remote {
				state, err = Open(ctx, endpoint, "remote-user", "remote-pass", "remote-ns", "remote-db")
			} else {
				state, err = openLocalRoot(ctx, endpoint)
			}
			if err == nil || state != nil || !strings.Contains(err.Error(), test.stage) {
				t.Fatalf("initialization=%v state=%v; want %s", err, state, test.stage)
			}
			select {
			case <-joined:
			case <-ctx.Done():
				t.Fatal("failed initialization did not close its SDK socket")
			}
			if len(calls) != test.failAt+1 {
				t.Fatalf("calls=%d want %d", len(calls), test.failAt+1)
			}
			for i, request := range calls {
				if err := checkLocalScopeWireRequest(request, i, test.remote); err != nil {
					t.Fatalf("request %d: %v", i, err)
				}
			}
		})
	}
}

type localScopeWireRequest struct {
	ID      any               `json:"id"`
	Method  string            `json:"method"`
	Params  []cbor.RawMessage `json:"params"`
	Txn     cbor.RawMessage   `json:"txn"`
	Session cbor.RawMessage   `json:"session"`
}

func checkLocalScopeWireRequest(request localScopeWireRequest, position int, remote bool) error {
	codec := surrealcbor.New()
	if request.ID == nil || len(request.Txn) != 0 || len(request.Session) != 0 {
		return errors.New("missing request identity or unexpected transaction/session")
	}
	user, pass, namespace, database := "root", "root", "phebs", "phebs"
	if remote {
		user, pass, namespace, database = "remote-user", "remote-pass", "remote-ns", "remote-db"
	}
	if position == 0 {
		if request.Method != "signin" || len(request.Params) != 1 {
			return errors.New("missing initial signin")
		}
		var auth map[string]string
		if err := codec.Unmarshal(request.Params[0], &auth); err != nil {
			return err
		}
		if len(auth) != 2 || auth["user"] != user || auth["pass"] != pass {
			return errors.New("signin did not preserve exact system credentials")
		}
		return nil
	}
	if len(request.Params) != 2 {
		return errors.New("query/use did not have exactly two parameters")
	}
	var first string
	if err := codec.Unmarshal(request.Params[0], &first); err != nil {
		return err
	}
	if (!remote && position == 2) || (!remote && position == 4) || (remote && position == 1) {
		if request.Method != "use" || first != namespace {
			return errors.New("unexpected use method/namespace")
		}
		if !remote && position == 2 {
			if len(request.Params[1]) != 1 || request.Params[1][0] != 0xf6 {
				return errors.New("namespace-only use did not send CBOR null")
			}
			return nil
		}
		var actualDatabase string
		if err := codec.Unmarshal(request.Params[1], &actualDatabase); err != nil {
			return err
		}
		if actualDatabase != database {
			return errors.New("unexpected database selection")
		}
		return nil
	}
	want := "BEGIN;\n" + schema + "\nCOMMIT;"
	if !remote && position == 1 {
		want = "DEFINE NAMESPACE IF NOT EXISTS phebs;"
	} else if !remote && position == 3 {
		want = "DEFINE DATABASE IF NOT EXISTS phebs;"
	}
	if request.Method != "query" || first != want {
		return errors.New("unexpected definition/schema query")
	}
	return nil
}

func TestLocalScopeInitializationCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := initializeLocalScope(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled before any submission: %v", err)
	}
	if _, err := openLocalRoot(ctx, "http://127.0.0.1:1"); err == nil {
		t.Fatal("local root constructor accepted a non-WebSocket endpoint")
	}
}

func TestLocalScopeNativeInitialization(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	state, err := OpenLocalMemory(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if err := state.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	runtime, err := ReadLocalRuntime(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	root := submissionProbeInfo(ctx, t, state.db, "INFO FOR ROOT;")
	namespace := submissionProbeInfo(ctx, t, state.db, "INFO FOR NS;")
	if _, exists := root.Namespaces["phebs"]; !exists {
		t.Fatal("local namespace missing")
	}
	if _, exists := namespace.Databases["phebs"]; !exists {
		t.Fatal("local database missing")
	}
	if err := state.UpsertRepo(ctx, Repo{Name: "neutral/local-scope", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	repeated, err := openLocalRoot(ctx, runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := repeated.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	repository, err := repeated.GetRepo(ctx, "neutral/local-scope")
	if err != nil || repository == nil || repository.DefaultBranch != "main" {
		t.Fatalf("repeated explicit initialization changed existing data: %+v %v", repository, err)
	}
	tx, err := repeated.db.Begin(ctx)
	if err != nil {
		t.Fatalf("retained concrete SDK lost WebSocket transaction support: %v", err)
	}
	if _, err := surrealdb.Query[any](ctx, tx, "SELECT * FROM repo LIMIT 1;", nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
}
