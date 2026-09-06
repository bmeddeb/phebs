//go:build darwin || linux

package storeaccounting

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/gorilla/websocket"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

func TestStoreAccountingLocalSDKNullRepresentation(t *testing.T) {
	for _, test := range []struct {
		name   string
		result cbor.RawMessage
	}{
		{"null", cbor.RawMessage{0xf6}},
		{"undefined", cbor.RawMessage{0xf7}},
		{"missing", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			codec := surrealcbor.New()
			envelope := map[string]any{"id": "neutral-id"}
			if test.result != nil {
				envelope["result"] = test.result
			}
			body, err := codec.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			var response connection.RPCResponse[cbor.RawMessage]
			if err := codec.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			if response.Result != nil {
				t.Fatalf("pinned SDK no longer collapses %s; revisit selected raw-result preservation", test.name)
			}
			decoder := storeLocalReplyDecoder{Codec: codec}
			if err := decoder.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			if test.result == nil {
				if response.Result != nil {
					t.Fatal("selected decoder invented an absent result")
				}
			} else if response.Result == nil || !bytes.Equal(*response.Result, test.result) {
				t.Fatal("selected decoder lost the actual result bytes")
			}
		})
	}
}

func TestStoreAccountingLocalReplyDecoderCompatibility(t *testing.T) {
	codec := surrealcbor.New()
	decoder := storeLocalReplyDecoder{Codec: codec}
	id := models.UUID{}
	id.UUID[0] = 1
	for _, test := range []struct {
		name   string
		result any
	}{
		{"token", "neutral-token"},
		{"uuid", id},
		{"query", []surrealdb.QueryResult[[]int]{{Status: "OK", Result: []int{1}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := codec.Marshal(test.result)
			if err != nil {
				t.Fatal(err)
			}
			body, err := codec.Marshal(map[string]any{"id": "neutral-id", "result": test.result})
			if err != nil {
				t.Fatal(err)
			}
			var response connection.RPCResponse[cbor.RawMessage]
			if err := decoder.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			if response.ID != "neutral-id" || response.Error != nil || response.Result == nil || !bytes.Equal(*response.Result, raw) {
				t.Fatal("selected decoder changed the native envelope")
			}
			clear(body)
			if !bytes.Equal(*response.Result, raw) {
				t.Fatal("retained result aliases the native response buffer")
			}
			selected := reflect.New(reflect.TypeOf(test.result)).Interface()
			ordinary := reflect.New(reflect.TypeOf(test.result)).Interface()
			if err := decoder.Unmarshal(*response.Result, selected); err != nil {
				t.Fatal(err)
			}
			if err := codec.Unmarshal(raw, ordinary); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(selected, ordinary) {
				t.Fatal("typed result did not use the default codec semantics")
			}
		})
	}
	t.Run("native_error", func(t *testing.T) {
		body, err := codec.Marshal(map[string]any{
			"id": "neutral-id", "error": map[string]any{"code": -32603, "message": "neutral refusal"},
		})
		if err != nil {
			t.Fatal(err)
		}
		var response connection.RPCResponse[cbor.RawMessage]
		if err := decoder.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		var native *connection.ServerError
		if response.Result != nil || response.Error == nil || !errors.As(response.Error, &native) || native.Code != -32603 || native.Message != "neutral refusal" {
			t.Fatal("native error classification changed")
		}
	})
	t.Run("malformed_envelope", func(t *testing.T) {
		var response connection.RPCResponse[cbor.RawMessage]
		if err := decoder.Unmarshal([]byte{0xa1}, &response); err == nil {
			t.Fatal("malformed native envelope decoded")
		}
	})
	t.Run("nil_destination", func(t *testing.T) {
		if err := decoder.Unmarshal([]byte{0xa0}, (*connection.RPCResponse[cbor.RawMessage])(nil)); err == nil {
			t.Fatal("nil response destination decoded")
		}
	})
}

func storeLocalAccountingFixture(t *testing.T) (context.Context, *SDKOwner, *Controller, *surrealdb.DB, *SDKConnection, *storeSDKTestConnection) {
	t.Helper()
	ctx, owner, controller := storeAccountingFixture(t, 40, 2)
	native := &storeSDKTestConnection{Connection: gorillaws.New(connection.NewConfig(&url.URL{Scheme: "ws", Host: "127.0.0.1:1"})), codec: surrealcbor.New()}
	conn := &SDKConnection{sdkNative: native, owner: owner, localStep: storeLocalSignIn}
	db, err := surrealdb.FromConnection(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) }) // scripted close has no I/O
	return ctx, owner, controller, db, conn, native
}

func storeLocalTestReply(request *connection.RPCRequest) any {
	if request.Method == "signin" {
		return "neutral-test-token"
	}
	if request.Method == "query" {
		return []surrealdb.QueryResult[any]{{Status: "OK"}}
	}
	database := request.Params[1]
	if database == nil {
		database = cbor.RawMessage{0xc6, 0xf6}
	}
	return map[string]any{"namespace": "phebs", "database": database}
}

func TestStoreAccountingLocalInitializationFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		step    storeLocalStep
		result  any
		rpc     bool
		pending bool
	}{
		{"signin_rpc", storeLocalSignIn, nil, true, false},
		{"signin_null", storeLocalSignIn, nil, false, true},
		{"signin_empty", storeLocalSignIn, "", false, true},
		{"signin_type", storeLocalSignIn, 12, false, true},
		{"namespace_rpc", storeLocalNamespaceDefinition, nil, true, false},
		{"namespace_null", storeLocalNamespaceDefinition, nil, false, true},
		{"namespace_empty", storeLocalNamespaceDefinition, []any{}, false, true},
		{"namespace_extra", storeLocalNamespaceDefinition, []surrealdb.QueryResult[any]{{Status: "OK"}, {Status: "OK"}}, false, true},
		{"namespace_status", storeLocalNamespaceDefinition, []surrealdb.QueryResult[any]{{Status: "unknown"}}, false, true},
		{"namespace_statement_error", storeLocalNamespaceDefinition, []surrealdb.QueryResult[any]{{Status: "ERR", Result: "neutral refusal"}}, false, false},
		{"namespace_use_rpc", storeLocalNamespaceUse, nil, true, false},
		{"namespace_use_nonnull", storeLocalNamespaceUse, true, false, true},
		{"namespace_use_undefined", storeLocalNamespaceUse, cbor.RawMessage{0xf7}, false, true},
		{"namespace_use_malformed", storeLocalNamespaceUse, cbor.RawMessage{0x81}, false, true},
		{"database_empty", storeLocalDatabaseDefinition, []any{}, false, true},
		{"database_use_rpc", storeLocalDatabaseUse, nil, true, false},
		{"database_use_nonnull", storeLocalDatabaseUse, true, false, true},
		{"database_use_undefined", storeLocalDatabaseUse, cbor.RawMessage{0xf7}, false, true},
		{"database_use_malformed", storeLocalDatabaseUse, cbor.RawMessage{0x81}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller, db, conn, native := storeLocalAccountingFixture(t)
			position := storeLocalStep(0)
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				position++
				if position == test.step {
					if test.rpc {
						return nil, &connection.ServerError{Code: -32603, Message: "neutral refusal"}
					}
					return test.result, nil
				}
				return storeLocalTestReply(request), nil
			}
			if err := InitializeLocalScope(ctx, db, conn); err == nil {
				t.Fatal("failed initialization succeeded")
			}
			if position != test.step || conn.localStep != test.step || owner.err == nil {
				t.Fatalf("position=%d step=%d failure=%v", position, conn.localStep, owner.err)
			}
			snapshot, _ := controller.Snapshot()
			wantWrites := uint64(0)
			if test.step >= storeLocalNamespaceDefinition {
				wantWrites++
			}
			if test.step >= storeLocalDatabaseDefinition {
				wantWrites++
			}
			if snapshot.Transactions != wantWrites || snapshot.Rows != wantWrites || snapshot.Complete {
				t.Fatalf("retained prefix=%+v", snapshot)
			}
			pending := 0
			for _, call := range owner.calls {
				if call != nil {
					pending++
				}
			}
			if (pending == 1) != test.pending {
				t.Fatalf("retained calls=%d want pending=%v", pending, test.pending)
			}
			if test.pending && (test.step == storeLocalNamespaceDefinition || test.step == storeLocalDatabaseDefinition) && snapshot.Producers[0].Calls != 1 {
				t.Fatalf("malformed definition was settled: %+v", snapshot)
			}
			if err := InitializeLocalScope(ctx, db, conn); err == nil || position != test.step {
				t.Fatal("failed initializer was restarted")
			}
		})
	}
}

func TestStoreAccountingLocalControlsAreClosed(t *testing.T) {
	for _, name := range []string{"direct_signin", "direct_use", "wrong_auth", "wrong_use", "wrong_sql", "wrong_vars", "wrong_rows", "wrong_step", "premature_query", "repeated_initialization"} {
		t.Run(name, func(t *testing.T) {
			ctx, owner, controller, db, conn, native := storeLocalAccountingFixture(t)
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				return storeLocalTestReply(request), nil
			}
			var err error
			wantWrites, wantCalls := uint64(0), 0
			switch name {
			case "direct_signin":
				_, err = db.SignIn(ctx, storeLocalAuth())
			case "direct_use":
				err = db.Use(ctx, "phebs", "phebs")
			case "wrong_auth", "wrong_use":
				call, acquireErr := owner.acquire(ctx, 0, nil)
				if acquireErr != nil {
					t.Fatal(acquireErr)
				}
				call.local, call.localStep = conn, storeLocalSignIn
				callCtx := context.WithValue(ctx, storeCallContextKey{}, call)
				if name == "wrong_auth" {
					_, err = db.SignIn(callCtx, surrealdb.Auth{Username: "root", Password: "root", Namespace: "phebs"})
				} else {
					conn.localStep, call.localStep = storeLocalNamespaceUse, storeLocalNamespaceUse
					err = db.Use(callCtx, "phebs", "") // null is not empty string
				}
			case "wrong_sql", "wrong_vars", "wrong_rows", "wrong_step":
				conn.localStep = storeLocalNamespaceDefinition
				sql, vars := storeLocalNamespaceSQL, map[string]any(nil)
				recipe := SDKQueryRecipe{supported: true, rows: 1, local: conn, localStep: storeLocalNamespaceDefinition}
				switch name {
				case "wrong_sql":
					sql += " DEFINE DATABASE injected;"
				case "wrong_vars":
					vars = map[string]any{"unused": true}
				case "wrong_rows":
					recipe.rows = 0
				case "wrong_step":
					recipe.localStep = storeLocalDatabaseDefinition
				}
				_, err = SDKQuery[any](ctx, owner, db, sql, vars, recipe)
			case "premature_query":
				_, err = SDKQuery[any](ctx, owner, db, "SELECT 1", nil, SDKRead())
			case "repeated_initialization":
				if err := InitializeLocalScope(ctx, db, conn); err != nil {
					t.Fatal(err)
				}
				wantWrites, wantCalls = 2, 5
				err = InitializeLocalScope(ctx, db, conn)
			}
			if err == nil || native.calls != wantCalls {
				t.Fatalf("error=%v native calls=%d", err, native.calls)
			}
			snapshot, _ := controller.Snapshot()
			if snapshot.Transactions != wantWrites || snapshot.Rows != wantWrites {
				t.Fatalf("refusal changed prefix: %+v", snapshot)
			}
		})
	}
}

func TestStoreAccountingLocalInitializationCancellation(t *testing.T) {
	for _, step := range []storeLocalStep{storeLocalSignIn, storeLocalNamespaceDefinition, storeLocalNamespaceUse, storeLocalDatabaseUse, storeExistingLocalSignIn, storeExistingLocalUse} {
		t.Run(map[storeLocalStep]string{storeLocalSignIn: "signin", storeLocalNamespaceDefinition: "definition", storeLocalNamespaceUse: "namespace_use", storeLocalDatabaseUse: "database_use", storeExistingLocalSignIn: "existing_signin", storeExistingLocalUse: "existing_use"}[step], func(t *testing.T) {
			ctx, owner, controller, db, conn, native := storeLocalAccountingFixture(t)
			entered := make(chan struct{})
			position := storeLocalStep(0)
			initializer := InitializeLocalScope
			if step >= storeExistingLocalSignIn {
				conn.localStep, position = storeExistingLocalSignIn, storeLocalReady
				initializer = InitializeExistingLocalScope
			}
			native.call = func(ctx context.Context, request *connection.RPCRequest) (any, error) {
				position++
				if position == step {
					close(entered)
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return storeLocalTestReply(request), nil
			}
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			joined := make(chan error, 1)
			go func() { joined <- initializer(callCtx, db, conn) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("initialization never entered native call")
			}
			if err := owner.Checkpoint(ctx); !errors.Is(err, ErrBusy) {
				t.Fatalf("active initialization checkpoint: %v", err)
			}
			cancel()
			select {
			case err := <-joined:
				if err == nil {
					t.Fatal("canceled native initialization succeeded")
				}
			case <-ctx.Done():
				t.Fatal("initialization did not join")
			}
			if conn.localStep != step {
				t.Fatal("uncertain native reply advanced initialization")
			}
			snapshot, _ := controller.Snapshot()
			want := uint64(0)
			if step >= storeLocalNamespaceDefinition {
				want = 1
			}
			if step >= storeLocalDatabaseDefinition {
				want++
			}
			if step >= storeExistingLocalSignIn {
				want = 0
			}
			if snapshot.Transactions != want || snapshot.Rows != want {
				t.Fatalf("prefix=%+v", snapshot)
			}
			pending := 0
			for _, call := range owner.calls {
				if call != nil {
					pending++
				}
			}
			if pending != 1 {
				t.Fatalf("uncertain local call released: %d", pending)
			}
		})
	}
}

func TestStoreAccountingLocalFactoryActualSDKWire(t *testing.T) {
	for _, test := range []struct {
		name     string
		failStep storeLocalStep
		reply    string
		existing bool
	}{
		{"five_step_prefix", 0, "", false},
		{"namespace_missing", storeLocalNamespaceUse, "missing", false},
		{"namespace_undefined", storeLocalNamespaceUse, "undefined", false},
		{"namespace_nonnull", storeLocalNamespaceUse, "nonnull", false},
		{"namespace_error", storeLocalNamespaceUse, "error", false},
		{"database_missing", storeLocalDatabaseUse, "missing", false},
		{"database_undefined", storeLocalDatabaseUse, "undefined", false},
		{"database_nonnull", storeLocalDatabaseUse, "nonnull", false},
		{"database_error", storeLocalDatabaseUse, "error", false},
		{"database_null", storeLocalDatabaseUse, "null", false},
		{"database_wrong_scope", storeLocalDatabaseUse, "wrong_scope", false},
		{"database_missing_key", storeLocalDatabaseUse, "missing_key", false},
		{"database_extra_key", storeLocalDatabaseUse, "extra_key", false},
		{"database_duplicate_key", storeLocalDatabaseUse, "duplicate_key", false},
		{"two_step_prefix", 0, "", true},
		{"existing_missing", storeExistingLocalUse, "missing", true},
		{"existing_undefined", storeExistingLocalUse, "undefined", true},
		{"existing_nonnull", storeExistingLocalUse, "nonnull", true},
		{"existing_error", storeExistingLocalUse, "error", true},
		{"existing_null", storeExistingLocalUse, "null", true},
		{"existing_wrong_scope", storeExistingLocalUse, "wrong_scope", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			joined := make(chan struct{})
			var calls []sdkLocalWireRequest
			controls, ready := 5, storeLocalReady
			if test.existing {
				controls, ready = 2, storeExistingLocalReady
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
				ws.SetReadLimit(4096)
				codec := surrealcbor.New()
				for {
					kind, body, err := ws.ReadMessage()
					if err != nil {
						return
					}
					if kind != websocket.BinaryMessage || len(calls) >= controls {
						t.Error("unexpected native call")
						return
					}
					var request sdkLocalWireRequest
					if err := codec.Unmarshal(body, &request); err != nil {
						t.Error(err)
						return
					}
					position := len(calls)
					if test.existing && position == 1 {
						position = 4
					}
					if err := checkSDKLocalWireRequest(request, position); err != nil {
						t.Error(err)
						return
					}
					calls = append(calls, request)
					var database any = "phebs"
					if position == 2 {
						database = nil
					}
					reply := storeLocalTestReply(&connection.RPCRequest{Method: request.Method, Params: []any{"phebs", database}})
					envelope := map[string]any{"id": request.ID, "result": reply}
					step := storeLocalStep(len(calls))
					if test.existing {
						step += storeLocalReady
					}
					if step == test.failStep {
						switch test.reply {
						case "missing":
							delete(envelope, "result")
						case "undefined":
							envelope["result"] = cbor.RawMessage{0xf7}
						case "nonnull":
							envelope["result"] = true
						case "null":
							envelope["result"] = nil
						case "wrong_scope":
							envelope["result"] = map[string]any{"namespace": "other", "database": "phebs"}
						case "missing_key":
							envelope["result"] = map[string]any{"namespace": "phebs"}
						case "extra_key":
							envelope["result"] = map[string]any{"namespace": "phebs", "database": "phebs", "extra": nil}
						case "duplicate_key":
							envelope["result"] = cbor.RawMessage("\xa3\x69namespace\x65phebs\x68database\x65phebs\x68database\x65phebs")
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
			u, err := url.Parse(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			constructor := NewLocalSDKConnection
			initializer := InitializeLocalScope
			if test.existing {
				constructor, initializer = NewExistingLocalSDKConnection, InitializeExistingLocalScope
			}
			conn, err := constructor(ctx, owner, connection.NewConfig(u))
			if err != nil {
				t.Fatal(err)
			}
			db, err := surrealdb.FromConnection(ctx, conn)
			if err != nil {
				t.Fatal(err)
			}
			closeDB := func() {
				closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := db.Close(closeCtx); err != nil {
					t.Error(err)
				}
			}
			defer closeDB()
			err = initializer(ctx, db, conn)
			if (err != nil) != (test.failStep != 0) {
				t.Fatalf("initialization error=%v", err)
			}
			closeDB()
			if test.failStep == 0 {
				if conn.localStep != ready {
					t.Fatal("initializer did not reach ready")
				}
				if err := owner.Close(ctx); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-joined:
			case <-ctx.Done():
				t.Fatal("SDK socket did not join")
			}
			wantCalls, wantWrites, wantMaximum := controls, uint64(2), uint64(1)
			if test.existing {
				wantWrites, wantMaximum = 0, 0
			}
			if test.failStep != 0 {
				wantCalls = int(test.failStep)
				if test.existing {
					wantCalls -= int(storeLocalReady)
				}
				if test.failStep == storeLocalNamespaceUse {
					wantWrites = 1
				}
				pending := 0
				for _, call := range owner.calls {
					if call != nil {
						pending++
						if call.local.localStep != test.failStep {
							t.Fatal("malformed reply advanced initialization")
						}
					}
				}
				if (pending == 1) != (test.reply != "error") || owner.err == nil {
					t.Fatalf("failed reply retained calls=%d failure=%v", pending, owner.err)
				}
			}
			if len(calls) != wantCalls {
				t.Fatalf("actual calls=%d", len(calls))
			}
			snapshot, _ := controller.Snapshot()
			if snapshot.Transactions != wantWrites || snapshot.Rows != wantWrites || snapshot.MaximumRows != wantMaximum || snapshot.Producers[0].Calls != 0 {
				t.Fatalf("prefix=%+v", snapshot)
			}
		})
	}
}

type sdkLocalWireRequest struct {
	ID      any               `json:"id"`
	Method  string            `json:"method"`
	Params  []cbor.RawMessage `json:"params"`
	Txn     cbor.RawMessage   `json:"txn"`
	Session cbor.RawMessage   `json:"session"`
}

func checkSDKLocalWireRequest(request sdkLocalWireRequest, position int) error {
	methods := [...]string{"signin", "query", "use", "query", "use"}
	params := [...][]any{{storeLocalAuth()}, {storeLocalNamespaceSQL, nil}, {"phebs", nil}, {storeLocalDatabaseSQL, nil}, {"phebs", "phebs"}}
	if position < 0 || position >= len(methods) || request.ID == nil || request.Method != methods[position] || len(request.Params) != len(params[position]) || len(request.Txn) != 0 || len(request.Session) != 0 {
		return errors.New("local source method or native identity changed")
	}
	codec := surrealcbor.New()
	for i, value := range params[position] {
		want, err := codec.Marshal(value)
		if err != nil || !bytes.Equal(want, request.Params[i]) {
			return errors.New("local source operand changed")
		}
	}
	return nil
}

func TestSDKLocalConstructorRefusesAbsentOwnerAndConfig(t *testing.T) {
	ctx, owner, _ := storeAccountingFixture(t, 1, 1)
	for _, constructor := range []func(context.Context, *SDKOwner, *connection.Config) (*SDKConnection, error){NewLocalSDKConnection, NewExistingLocalSDKConnection} {
		for _, candidate := range []*SDKOwner{nil, {}} {
			if conn, err := constructor(ctx, candidate, nil); conn != nil || !errors.Is(err, ErrConfig) {
				t.Fatalf("invalid owner=%v %v", conn, err)
			}
		}
		if conn, err := constructor(ctx, owner, nil); conn != nil || !errors.Is(err, ErrConfig) {
			t.Fatalf("invalid config=%v %v", conn, err)
		}
	}
}

func TestSDKExistingLocalSequenceRefusesBypass(t *testing.T) {
	for _, mode := range []string{"five_step", "premature_query", "wrong_auth", "wrong_database", "repeat"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller, db, conn, native := storeLocalAccountingFixture(t)
			conn.localStep = storeExistingLocalSignIn
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				return storeLocalTestReply(request), nil
			}
			var err error
			wantCalls := 0
			switch mode {
			case "five_step":
				err = InitializeLocalScope(ctx, db, conn)
			case "premature_query":
				_, err = SDKQuery[any](ctx, owner, db, "SELECT 1", nil, SDKRead())
			case "repeat":
				if err := InitializeExistingLocalScope(ctx, db, conn); err != nil {
					t.Fatal(err)
				}
				wantCalls = 2
				err = InitializeExistingLocalScope(ctx, db, conn)
			default:
				call, acquireErr := owner.acquire(ctx, 0, nil)
				if acquireErr != nil {
					t.Fatal(acquireErr)
				}
				call.local, call.localStep = conn, storeExistingLocalSignIn
				callCtx := context.WithValue(ctx, storeCallContextKey{}, call)
				if mode == "wrong_auth" {
					_, err = db.SignIn(callCtx, surrealdb.Auth{Username: "root", Password: "other"})
				} else {
					conn.localStep, call.localStep = storeExistingLocalUse, storeExistingLocalUse
					err = db.Use(callCtx, "phebs", "other")
				}
			}
			if err == nil || native.calls != wantCalls || owner.err == nil {
				t.Fatalf("refusal=%v native=%d sticky=%v", err, native.calls, owner.err)
			}
			snapshot, _ := controller.Snapshot()
			if snapshot.Transactions != 0 || snapshot.Rows != 0 || snapshot.MaximumRows != 0 {
				t.Fatalf("control refusal invented write submissions: %+v", snapshot)
			}
		})
	}
}

func TestSDKLocalUseReplyRequiresExactNativeScope(t *testing.T) {
	const namespaceFirst = "\xa2\x69namespace\x65phebs\x68database"
	const namespaceOnly = namespaceFirst + "\xc6\xf6"
	const databaseSelected = namespaceFirst + "\x65phebs"
	for _, test := range []struct {
		name, raw string
		database  any
		accepted  bool
	}{
		{"namespace_first", namespaceOnly, nil, true},
		{"namespace_reverse", "\xa2\x68database\xc6\xf6\x69namespace\x65phebs", nil, true},
		{"database_first", databaseSelected, "phebs", true},
		{"database_reverse", "\xa2\x68database\x65phebs\x69namespace\x65phebs", "phebs", true},
		{"wrong_step_namespace", namespaceOnly, "phebs", false},
		{"wrong_step_database", databaseSelected, nil, false},
		{"caller_database", databaseSelected, "other", false},
		{"missing", "", nil, false},
		{"null", "\xf6", nil, false},
		{"undefined", "\xf7", nil, false},
		{"none", "\xc6\xf6", nil, false},
		{"array", "\x82\x65phebs\xc6\xf6", nil, false},
		{"missing_key", "\xa1\x69namespace\x65phebs", nil, false},
		{"extra_key", "\xa3\x69namespace\x65phebs\x68database\xc6\xf6\x61x\xf6", nil, false},
		{"duplicate_key", "\xa3\x69namespace\x65phebs\x68database\xc6\xf6\x68database\xc6\xf6", nil, false},
		{"duplicate_replaces_key", "\xa2\x69namespace\x65phebs\x69namespace\x65phebs", nil, false},
		{"wrong_namespace", "\xa2\x69namespace\x65other\x68database\x65phebs", "phebs", false},
		{"wrong_database", namespaceFirst + "\x65other", "phebs", false},
		{"null_not_none", namespaceFirst + "\xf6", nil, false},
		{"undefined_not_none", namespaceFirst + "\xf7", nil, false},
		{"trailing", databaseSelected + "\xf6", "phebs", false},
		{"indefinite", "\xbf\x69namespace\x65phebs\x68database\x65phebs\xff", "phebs", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := storeLocalScopeReply([]byte(test.raw), test.database); got != test.accepted {
				t.Fatalf("scope reply accepted=%t, want=%t", got, test.accepted)
			}
		})
	}
}
