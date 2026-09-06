//go:build darwin || linux

package store

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	"github.com/gorilla/websocket"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

func storeLocalTestReply(method string, namespaceOnly bool) any {
	if method == "signin" {
		return "neutral-test-token"
	}
	if method == "query" {
		return []surrealdb.QueryResult[any]{{Status: "OK"}}
	}
	var database any = "phebs"
	if namespaceOnly {
		database = cbor.RawMessage{0xc6, 0xf6}
	}
	return map[string]any{"namespace": "phebs", "database": database}
}

func TestStoreAccountingLocalFactoryActualSDKWire(t *testing.T) {
	for _, test := range []struct {
		name     string
		failStep uint8
		reply    string
		existing bool
	}{
		{"first_schema_native_error", 0, "", false},
		{"namespace_missing", 3, "missing", false},
		{"namespace_undefined", 3, "undefined", false},
		{"namespace_nonnull", 3, "nonnull", false},
		{"namespace_error", 3, "error", false},
		{"database_missing", 5, "missing", false},
		{"database_undefined", 5, "undefined", false},
		{"database_nonnull", 5, "nonnull", false},
		{"database_error", 5, "error", false},
		{"existing_first_schema_native_error", 0, "", true},
		{"existing_database_missing", 2, "missing", true},
		{"existing_database_undefined", 2, "undefined", true},
		{"existing_database_nonnull", 2, "nonnull", true},
		{"existing_database_error", 2, "error", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			joined := make(chan struct{})
			var calls []localScopeWireRequest
			controls := 5
			if test.existing {
				controls = 2
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(joined)
				upgrader := websocket.Upgrader{Subprotocols: []string{"cbor"}}
				ws, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = ws.Close() }()
				deadline, _ := ctx.Deadline()
				if err := ws.SetReadDeadline(deadline); err != nil {
					t.Error(err)
					return
				}
				if err := ws.SetWriteDeadline(deadline); err != nil {
					t.Error(err)
					return
				}
				ws.SetReadLimit(1 << 20)
				codec := surrealcbor.New()
				for {
					kind, body, err := ws.ReadMessage()
					if err != nil {
						return
					}
					if kind != websocket.BinaryMessage || len(calls) > controls {
						t.Error("unexpected native call")
						return
					}
					var request localScopeWireRequest
					if err := codec.Unmarshal(body, &request); err != nil {
						t.Error(err)
						return
					}
					position := len(calls)
					if test.existing && position > 0 {
						position += 3 // existing Use/schema equal the final local Use/schema
					}
					if err := checkLocalScopeWireRequest(request, position, false); err != nil {
						t.Error(err)
						return
					}
					calls = append(calls, request)
					envelope := map[string]any{"id": request.ID, "result": storeLocalTestReply(request.Method, position == 2)}
					if len(calls) == controls+1 {
						envelope["result"] = []surrealdb.QueryResult[any]{{Status: "ERR", Result: "neutral schema refusal"}}
					}
					if uint8(len(calls)) == test.failStep {
						switch test.reply {
						case "missing":
							delete(envelope, "result")
						case "undefined":
							envelope["result"] = cbor.RawMessage{0xf7}
						case "nonnull":
							envelope["result"] = true
						case "error":
							envelope["error"] = map[string]any{"code": -32603, "message": "neutral refusal"}
						}
					}
					body, err = codec.Marshal(envelope)
					if err != nil {
						t.Error(err)
						return
					}
					if err := ws.WriteMessage(websocket.BinaryMessage, body); err != nil {
						t.Error(err)
						return
					}
				}
			}))
			defer server.Close()
			endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
			var state *Surreal
			var err error
			if test.existing {
				state, err = openWithOwner(ctx, endpoint, "root", "root", "phebs", "phebs", owner)
			} else {
				state, err = openLocalRootWithOwner(ctx, endpoint, owner)
			}
			var queryError *surrealdb.QueryError
			if state != nil || err == nil || test.failStep == 0 && !errors.As(err, &queryError) {
				t.Fatalf("factory=%v error=%v", state, err)
			}
			select {
			case <-joined:
			case <-ctx.Done():
				t.Fatal("SDK socket did not join")
			}
			wantCalls, wantWrites, wantRows, wantMaximum := controls+1, uint64(3), uint64(490), uint64(488)
			if test.existing {
				wantWrites, wantRows = 1, 488
			}
			if test.failStep != 0 {
				wantCalls = int(test.failStep)
				wantWrites, wantMaximum = 2, 1
				if test.existing {
					wantWrites, wantMaximum = 0, 0
				} else if test.failStep == 3 {
					wantWrites = 1
				}
				wantRows = wantWrites
			}
			if len(calls) != wantCalls {
				t.Fatalf("actual calls=%d", len(calls))
			}
			snapshot, _ := controller.Snapshot()
			if snapshot.Transactions != wantWrites || snapshot.Rows != wantRows || snapshot.MaximumRows != wantMaximum || snapshot.Producers[0].Calls != 0 {
				t.Fatalf("prefix=%+v", snapshot)
			}
		})
	}
}

func TestStoreAccountingExistingLocalFactoryRejectsUnboundConfig(t *testing.T) {
	for _, test := range []struct {
		name, endpoint, user, pass, namespace, database string
	}{
		{"scheme", "http://127.0.0.1:1", "root", "root", "phebs", "phebs"},
		{"hostname", "ws://localhost:1", "root", "root", "phebs", "phebs"},
		{"remote", "ws://192.0.2.1:1", "root", "root", "phebs", "phebs"},
		{"ipv6", "ws://[::1]:1", "root", "root", "phebs", "phebs"},
		{"missing_port", "ws://127.0.0.1", "root", "root", "phebs", "phebs"},
		{"zero_port", "ws://127.0.0.1:0", "root", "root", "phebs", "phebs"},
		{"padded_port", "ws://127.0.0.1:01", "root", "root", "phebs", "phebs"},
		{"large_port", "ws://127.0.0.1:65536", "root", "root", "phebs", "phebs"},
		{"userinfo", "ws://root:root@127.0.0.1:1", "root", "root", "phebs", "phebs"},
		{"path", "ws://127.0.0.1:1/rpc", "root", "root", "phebs", "phebs"},
		{"query", "ws://127.0.0.1:1?", "root", "root", "phebs", "phebs"},
		{"fragment", "ws://127.0.0.1:1#fragment", "root", "root", "phebs", "phebs"},
		{"user", "ws://127.0.0.1:1", "other", "root", "phebs", "phebs"},
		{"password", "ws://127.0.0.1:1", "root", "other", "phebs", "phebs"},
		{"namespace", "ws://127.0.0.1:1", "root", "root", "other", "phebs"},
		{"database", "ws://127.0.0.1:1", "root", "root", "phebs", "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 1, 1)
			state, err := openWithOwner(ctx, test.endpoint, test.user, test.pass, test.namespace, test.database, owner)
			if state != nil || !errors.Is(err, storeaccounting.ErrConfig) {
				t.Fatalf("unbound factory=%v error=%v", state, err)
			}
			if err := owner.Check(ctx); err != nil {
				t.Fatalf("pre-connect configuration refusal poisoned owner: %v", err)
			}
			snapshot, _ := controller.Snapshot()
			if snapshot.Transactions != 0 || snapshot.Rows != 0 {
				t.Fatalf("pre-connect refusal invented writes: %+v", snapshot)
			}
		})
	}
}
