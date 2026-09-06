//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// The source tests inspect actual operands at the native marshaler, then send
// replies through a real SDK WebSocket. No production gate accepts a fake
// connection or caller-supplied accounting proof.
type storeSDKTestConnection struct {
	codec   *surrealcbor.Codec
	call    func(context.Context, *connection.RPCRequest) (any, error)
	ctx     context.Context
	replies chan []byte
	calls   int
	next    byte
}

func (native *storeSDKTestConnection) Marshal(value any) ([]byte, error) {
	request, ok := value.(*connection.RPCRequest)
	if !ok {
		return native.codec.Marshal(value)
	}
	native.calls++
	native.next++
	var result any
	var err error
	if native.call != nil {
		result, err = native.call(native.ctx, request)
	} else {
		switch request.Method {
		case "begin":
			id := models.UUID{}
			id.UUID[0] = native.next
			result = id
		case "query":
			result = []surrealdb.QueryResult[any]{{Status: "OK", Result: []int{1}}}
		}
	}
	reply := map[string]any{"id": request.ID, "result": result}
	var rpc *connection.ServerError
	var query *surrealdb.QueryError
	switch {
	case errors.As(err, &rpc):
		delete(reply, "result")
		reply["error"] = map[string]any{"code": rpc.Code, "message": rpc.Message}
	case errors.As(err, &query):
		reply["result"] = []surrealdb.QueryResult[any]{{Status: "ERR", Result: query.Message}}
	case err != nil:
		// A native transport/pre-serialization failure has no known reply.
		return nil, err
	}
	encoded, err := native.codec.Marshal(request)
	if err != nil {
		return nil, err
	}
	response, err := native.codec.Marshal(reply)
	if err != nil {
		return nil, err
	}
	select {
	case native.replies <- response:
		return encoded, nil
	case <-native.ctx.Done():
		return nil, native.ctx.Err()
	}
}

func storeAccountingFixture(t *testing.T, calls, transactions int) (context.Context, *storeCallOwner, *storeaccounting.Controller) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	controller, err := storeaccounting.New(ctx, storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: 2, Calls: calls, Transactions: transactions}},
		Phases:    []storeaccounting.Phase{{ID: 1, Transactions: 100, Rows: 1000}, {ID: 2, Transactions: 100, Rows: 1000}},
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(ctx, controller, storeaccounting.WireConfig{
		Producers: []storeaccounting.WireProducer{{ID: 2, Binding: [32]byte{1}, Phases: 3}}, AckTimeout: time.Second,
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	file, config, err := transport.Open(2)
	if err != nil {
		cancel()
		_ = transport.Close()
		t.Fatal(err)
	}
	client, err := storeaccounting.NewClient(ctx, file, config)
	if err != nil {
		cancel()
		_ = transport.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = client.Close(context.Background()); _ = transport.Close() })
	owner, err := newStoreCallOwner(client)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, owner, controller
}

func storeAccountingDB(t *testing.T, ctx context.Context, owner *storeCallOwner) (*surrealdb.DB, *storeSDKTestConnection) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	native := &storeSDKTestConnection{codec: surrealcbor.New(), ctx: ctx, replies: make(chan []byte, 1)}
	joined := make(chan struct{})
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
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
			select {
			case response := <-native.replies:
				if err := ws.WriteMessage(websocket.BinaryMessage, response); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}))
	endpoint, err := url.Parse("ws" + strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		cancel()
		server.Close()
		t.Fatal(err)
	}
	config := connection.NewConfig(endpoint)
	config.Marshaler = native
	actual := gorillaws.New(config)
	var conn connection.Connection = actual
	if owner != nil {
		conn, err = storeaccounting.NewSDKConnection(owner.SDKOwner, actual)
		if err != nil {
			cancel()
			server.Close()
			t.Fatal(err)
		}
	}
	db, err := surrealdb.FromConnection(ctx, conn)
	if err != nil {
		cancel()
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer closeCancel()
		if err := db.Close(closeCtx); err != nil {
			t.Error(err)
		}
		cancel()
		server.Close()
		select {
		case <-joined:
		case <-closeCtx.Done():
			t.Error("source SDK fixture did not join")
		}
	})
	return db, native
}

func TestStoreAccountingFixtureReplyClasses(t *testing.T) {
	for _, test := range []struct {
		name   string
		result any
		err    error
		known  bool
	}{
		{"rpc", nil, &connection.ServerError{Code: -32603, Message: "neutral refusal"}, true},
		{"query", nil, &surrealdb.QueryError{Message: "neutral query refusal"}, true},
		{"transport", nil, context.DeadlineExceeded, false},
		{"decode", []surrealdb.QueryResult[any]{{Status: "OK", Result: "not an integer array"}}, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 1, 1)
			db, native := storeAccountingDB(t, ctx, owner)
			native.call = func(context.Context, *connection.RPCRequest) (any, error) { return test.result, test.err }
			_, err := storeQuery[[]int](ctx, owner, db, "source write", nil, storeWrite(1))
			if err == nil {
				t.Fatal("failed native reply succeeded")
			}
			var rpc *connection.ServerError
			var query *surrealdb.QueryError
			if test.name == "rpc" && !errors.As(err, &rpc) || test.name == "query" && !errors.As(err, &query) {
				t.Fatalf("native reply class changed: %v", err)
			}
			prefix, _ := controller.Snapshot()
			if prefix.Transactions != 1 || prefix.Rows != 1 || prefix.MaximumRows != 1 || native.calls != 1 {
				t.Fatalf("attempt changed: %+v calls=%d", prefix, native.calls)
			}
			if test.known {
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
			}
			if err := owner.checkpoint(ctx); (err == nil) != test.known {
				t.Fatalf("reply completion changed: %v", err)
			}
		})
	}
}
