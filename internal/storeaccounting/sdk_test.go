//go:build darwin || linux

package storeaccounting

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	"github.com/surrealdb/surrealdb.go/pkg/constants"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

type storeSDKTestConnection struct {
	*gorillaws.Connection
	codec *surrealcbor.Codec
	call  func(context.Context, *connection.RPCRequest) (any, error)
	mu    sync.Mutex
	calls int
	next  byte
}

func (*storeSDKTestConnection) Connect(context.Context) error { return nil }
func (*storeSDKTestConnection) Close(context.Context) error   { return nil }
func (*storeSDKTestConnection) IsClosed() bool                { return false }
func (conn *storeSDKTestConnection) Send(ctx context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	return conn.Call(ctx, &connection.RPCRequest{Method: method, Params: params})
}
func (conn *storeSDKTestConnection) Call(ctx context.Context, request *connection.RPCRequest) (*connection.RPCResponse[cbor.RawMessage], error) {
	conn.mu.Lock()
	conn.calls++
	conn.next++
	next := conn.next
	callback := conn.call
	conn.mu.Unlock()
	var result any
	var err error
	if callback != nil {
		result, err = callback(ctx, request)
	} else {
		switch request.Method {
		case "begin":
			id := models.UUID{}
			id.UUID[0] = next
			result = id
		case "query":
			result = []surrealdb.QueryResult[any]{{Status: "OK", Result: []int{1}}}
		}
	}
	if err != nil {
		return nil, err
	}
	raw, err := conn.codec.Marshal(result)
	if err != nil {
		return nil, err
	}
	value := cbor.RawMessage(raw)
	return &connection.RPCResponse[cbor.RawMessage]{Result: &value}, nil
}

func storeAccountingFixture(t *testing.T, calls, transactions int) (context.Context, *SDKOwner, *Controller) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	controller, err := New(ctx, Config{
		Producers: []Producer{{ID: 2, Calls: calls, Transactions: transactions}},
		Phases:    []Phase{{ID: 1, Transactions: 100, Rows: 1000}, {ID: 2, Transactions: 100, Rows: 1000}},
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	transport, err := NewTransport(ctx, controller, WireConfig{
		Producers: []WireProducer{{ID: 2, Binding: [32]byte{1}, Phases: 3}}, AckTimeout: time.Second,
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
	client, err := NewClient(ctx, file, config)
	if err != nil {
		cancel()
		_ = transport.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = client.Close(context.Background()); _ = transport.Close() })
	owner, err := NewSDKOwner(client)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, owner, controller
}

func storeAccountingDB(t *testing.T, ctx context.Context, owner *SDKOwner) (*surrealdb.DB, *storeSDKTestConnection) {
	t.Helper()
	endpoint, err := url.Parse("ws://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	native := &storeSDKTestConnection{Connection: gorillaws.New(connection.NewConfig(endpoint)), codec: surrealcbor.New()}
	var conn connection.Connection = native
	if owner != nil {
		conn = &SDKConnection{sdkNative: native, owner: owner}
	}
	db, err := surrealdb.FromConnection(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db, native
}

func TestStoreAccountingLogicalCallsAndNoCarry(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 40, 2)
	db, _ := storeAccountingDB(t, ctx, owner)
	if _, err := SDKQuery[[]int](ctx, owner, db, "source read", nil, SDKRead()); err != nil {
		t.Fatal(err)
	}
	for _, rows := range []uint64{2, 0} {
		if _, err := SDKQuery[[]int](ctx, owner, db, "source write", nil, SDKWrite(rows)); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := SDKBegin(ctx, owner, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Checkpoint(ctx); !errors.Is(err, ErrBusy) {
		t.Fatalf("live UUID checkpoint: %v", err)
	}
	if _, err := SDKQuery[[]int](ctx, owner, tx, "transaction read", nil, SDKRead()); err != nil {
		t.Fatal(err)
	}
	if _, err := SDKQuery[[]int](ctx, owner, tx, "zero-row transaction control", nil, SDKWrite(0)); err != nil {
		t.Fatal(err)
	}
	for _, rows := range []uint64{3, 2} {
		if _, err := SDKQuery[[]int](ctx, owner, tx, "transaction append", nil, SDKWrite(rows)); err != nil {
			t.Fatal(err)
		}
	}
	if err := SDKCommit(ctx, owner, tx); err != nil {
		t.Fatal(err)
	}
	if err := SDKCancel(ctx, owner, tx); !errors.Is(err, constants.ErrTransactionClosed) {
		t.Fatalf("deferred cancel: %v", err)
	}
	snapshot, err := controller.Snapshot()
	if err != nil || snapshot.Transactions != 3 || snapshot.Rows != 7 || snapshot.MaximumRows != 5 {
		t.Fatalf("prefix=%+v error=%v", snapshot, err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Resume(2); err != nil {
		t.Fatal(err)
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
	snapshot, err = controller.Snapshot()
	if err != nil || !snapshot.Complete {
		t.Fatalf("closure=%+v %v", snapshot, err)
	}
}

func TestStoreAccountingImmutableVarsAndOrdinaryPassthrough(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "selected"}[selected], func(t *testing.T) {
			ctx := t.Context()
			var owner *SDKOwner
			if selected {
				ctx, owner, _ = storeAccountingFixture(t, 40, 2)
			}
			db, native := storeAccountingDB(t, ctx, owner)
			payload := []byte("original")
			vars := map[string]any{"payload": payload}
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if request.Params[0] != "exact source" {
					t.Error("SQL changed")
				}
				if selected {
					payload[0] = 'X'
					raw, ok := request.Params[1].(cbor.RawMessage)
					if !ok {
						t.Fatal("selected vars were not immutable native CBOR")
					}
					var observed map[string][]byte
					if err := native.codec.Unmarshal(raw, &observed); err != nil || string(observed["payload"]) != "original" {
						t.Fatalf("snapshot drift: %v %v", observed, err)
					}
				} else if request.Params[1].(map[string]any)["payload"].([]byte)[0] != 'o' {
					t.Fatal("ordinary vars changed")
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []int{1}}}, nil
			}
			if _, err := SDKQuery[[]int](ctx, owner, db, "exact source", vars, SDKRead()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStoreAccountingRefusalsRetainPrefix(t *testing.T) {
	for _, kind := range []string{"unsupported", "over_rows", "bare_query", "direct_use", "read_timeout", "write_timeout", "decode_error", "begin_error", "commit_error", "cancel_error"} {
		t.Run(kind, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			wantTransactions, wantRows := uint64(0), uint64(0)
			var tx *surrealdb.Transaction
			if kind == "commit_error" || kind == "cancel_error" {
				var err error
				tx, err = SDKBegin(ctx, owner, db)
				if err != nil {
					t.Fatal(err)
				}
				wantTransactions = 1
			}
			native.call = func(context.Context, *connection.RPCRequest) (any, error) { return nil, context.DeadlineExceeded }
			var err error
			switch kind {
			case "unsupported":
				_, err = SDKQuery[[]int](ctx, owner, db, "unknown", nil, SDKUnsupported())
			case "over_rows":
				_, err = SDKQuery[[]int](ctx, owner, db, "write", nil, SDKWrite(513))
			case "bare_query":
				_, err = surrealdb.Query[[]int](ctx, db, "bare", nil)
			case "direct_use":
				err = db.Use(ctx, "ns", "db")
			case "read_timeout":
				_, err = SDKQuery[[]int](ctx, owner, db, "read", nil, SDKRead())
			case "write_timeout":
				wantTransactions, wantRows = 1, 1
				_, err = SDKQuery[[]int](ctx, owner, db, "write", nil, SDKWrite(1))
			case "decode_error":
				wantTransactions, wantRows = 1, 1
				native.call = func(context.Context, *connection.RPCRequest) (any, error) {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: "not an integer array"}}, nil
				}
				_, err = SDKQuery[[]int](ctx, owner, db, "write", nil, SDKWrite(1))
			case "begin_error":
				wantTransactions = 1
				_, err = SDKBegin(ctx, owner, db)
			case "commit_error":
				err = SDKCommit(ctx, owner, tx)
			case "cancel_error":
				err = SDKCancel(ctx, owner, tx)
			}
			if err == nil {
				t.Fatal("unsafe forwarding succeeded")
			}
			// Parent failure delivery can race this read, but its accepted prefix
			// is already committed before the exact ACK admitted the native call.
			snapshot, _ := controller.Snapshot()
			if snapshot.Transactions != wantTransactions || snapshot.Rows != wantRows || snapshot.Complete {
				t.Fatalf("retained prefix=%+v", snapshot)
			}
			if kind == "begin_error" || kind == "commit_error" || kind == "cancel_error" {
				if !owner.transactions[0].used {
					t.Fatal("uncertain native UUID reservation disappeared")
				}
			}
			before := native.calls
			if _, err := SDKQuery[[]int](ctx, owner, db, "later", nil, SDKRead()); err == nil || native.calls != before {
				t.Fatal("sticky failure forwarded later work")
			}
		})
	}
}

type storeDecodeTestGate struct {
	*surrealcbor.Codec
	entered, release chan struct{}
	ctx              context.Context
}

func (gate *storeDecodeTestGate) Unmarshal(raw []byte, output any) error {
	if _, ok := output.(*[]int); ok {
		close(gate.entered)
		select {
		case <-gate.release:
		case <-gate.ctx.Done():
			return gate.ctx.Err()
		}
	}
	return gate.Codec.Unmarshal(raw, output)
}

func TestStoreAccountingReadSlotSpansTypedDecode(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 40, 2)
	db, native := storeAccountingDB(t, ctx, owner)
	gate := &storeDecodeTestGate{Codec: native.codec, entered: make(chan struct{}), release: make(chan struct{}), ctx: ctx}
	native.Unmarshaler = gate
	done := make(chan error, 1)
	go func() { _, err := SDKQuery[[]int](ctx, owner, db, "read", nil, SDKRead()); done <- err }()
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := owner.Checkpoint(ctx); !errors.Is(err, ErrBusy) {
		t.Errorf("decode handoff=%v", err)
	}
	snapshot, err := controller.Snapshot()
	if err != nil || snapshot.Transactions != 0 || snapshot.Rows != 0 {
		t.Errorf("read invented parent event: %+v %v", snapshot, err)
	}
	close(gate.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestStoreAccountingCompletedErrorsDoNotEraseAttempts(t *testing.T) {
	for _, kind := range []string{"query", "rpc"} {
		t.Run(kind, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			first := true
			native.call = func(context.Context, *connection.RPCRequest) (any, error) {
				if first {
					first = false
					if kind == "rpc" {
						// Decode the actual SDK wire error so its v3 ServerError
						// view proves a response, not a local transport failure.
						raw, err := native.codec.Marshal(map[string]any{"error": map[string]any{"code": -32000, "message": "known response"}})
						if err != nil {
							return nil, err
						}
						var response connection.RPCResponse[any]
						if err := native.codec.Unmarshal(raw, &response); err != nil {
							return nil, err
						}
						return nil, response.Error
					}
					return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "known aborted conflict"}}, nil
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []int{1}}}, nil
			}
			if _, err := SDKQuery[[]int](ctx, owner, db, "fixed write", nil, SDKWrite(2)); err == nil {
				t.Fatal("native error disappeared")
			}
			if _, err := SDKQuery[[]int](ctx, owner, db, "retry read", nil, SDKRead()); err != nil {
				t.Fatal(err)
			}
			snapshot, err := controller.Snapshot()
			if err != nil || snapshot.Transactions != 1 || snapshot.Rows != 2 || snapshot.Producers[0].Calls != 0 {
				t.Fatalf("completed failed attempt=%+v %v", snapshot, err)
			}
		})
	}
}

func TestStoreAccountingAllCallCapacityAcrossConnections(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 40, 2)
	first, firstNative := storeAccountingDB(t, ctx, owner)
	second, secondNative := storeAccountingDB(t, ctx, owner)
	entered, release := make(chan struct{}, 40), make(chan struct{})
	blocked := func(ctx context.Context, _ *connection.RPCRequest) (any, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []surrealdb.QueryResult[any]{{Status: "OK", Result: []int{1}}}, nil
	}
	firstNative.call, secondNative.call = blocked, blocked
	done := make(chan error, 40)
	for index := range 40 {
		db := first
		if index%2 != 0 {
			db = second
		}
		go func() { _, err := SDKQuery[[]int](ctx, owner, db, "read", nil, SDKRead()); done <- err }()
	}
	for range 40 {
		select {
		case <-entered:
		case <-ctx.Done():
			close(release)
			t.Fatal(ctx.Err())
		}
	}
	if _, err := SDKQuery[[]int](ctx, owner, first, "forty-first", nil, SDKRead()); !errors.Is(err, ErrLimit) {
		t.Errorf("ALL-call overflow=%v", err)
	}
	close(release)
	for range 40 {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	snapshot, _ := controller.Snapshot()
	if snapshot.Transactions != 0 || snapshot.Rows != 0 {
		t.Fatalf("read capacity manufactured writes: %+v", snapshot)
	}
	if firstNative.calls+secondNative.calls != 40 {
		t.Fatalf("forwarded calls=%d", firstNative.calls+secondNative.calls)
	}
}

func TestStoreAccountingUUIDCapacityAndOverlap(t *testing.T) {
	for _, kind := range []string{"third_uuid", "same_uuid_call", "same_uuid_terminal", "rows_overflow"} {
		t.Run(kind, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			tx, err := SDKBegin(ctx, owner, db)
			if err != nil {
				t.Fatal(err)
			}
			wantTransactions, wantRows := uint64(1), uint64(0)
			switch kind {
			case "third_uuid":
				if _, err := SDKBegin(ctx, owner, db); err != nil {
					t.Fatal(err)
				}
				wantTransactions = 2
				if _, err := SDKBegin(ctx, owner, db); !errors.Is(err, ErrLimit) {
					t.Fatalf("third UUID=%v", err)
				}
			case "rows_overflow":
				if _, err := SDKQuery[[]int](ctx, owner, tx, "append512", nil, SDKWrite(512)); err != nil {
					t.Fatal(err)
				}
				wantRows = 512
				before := native.calls
				if _, err := SDKQuery[[]int](ctx, owner, tx, "append1", nil, SDKWrite(1)); !errors.Is(err, ErrLimit) || native.calls != before {
					t.Fatalf("transaction row overflow=%v", err)
				}
			case "same_uuid_call", "same_uuid_terminal":
				entered, release := make(chan struct{}), make(chan struct{})
				native.call = func(ctx context.Context, _ *connection.RPCRequest) (any, error) {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []int{1}}}, nil
				}
				done := make(chan error, 1)
				go func() {
					if kind == "same_uuid_terminal" {
						done <- SDKCommit(ctx, owner, tx)
						return
					}
					_, err := SDKQuery[[]int](ctx, owner, tx, "pending read", nil, SDKRead())
					done <- err
				}()
				select {
				case <-entered:
				case <-ctx.Done():
					close(release)
					t.Fatal(ctx.Err())
				}
				var overlap error
				if kind == "same_uuid_terminal" {
					_, overlap = SDKQuery[[]int](ctx, owner, tx, "read overlaps terminal", nil, SDKRead())
				} else {
					overlap = SDKCommit(ctx, owner, tx)
				}
				if !errors.Is(overlap, ErrProtocol) {
					t.Errorf("same UUID overlap=%v", overlap)
				}
				select {
				case <-done:
				case <-ctx.Done():
					close(release)
					t.Fatal(ctx.Err())
				}
				close(release)
			}
			snapshot, _ := controller.Snapshot()
			if snapshot.Transactions != wantTransactions || snapshot.Rows != wantRows || !owner.transactions[0].used {
				t.Fatalf("UUID prefix=%+v", snapshot)
			}
		})
	}
}

func TestStoreAccountingConstructorAndWrongOwner(t *testing.T) {
	if _, err := NewSDKOwner(nil); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	if _, err := NewSDKOwner(&Client{}); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	ctx, owner, _ := storeAccountingFixture(t, 1, 1)
	if owner.callLimit != 1 || owner.txLimit != 1 {
		t.Fatal("CLI capacities changed")
	}
	if duplicate, err := NewSDKOwner(owner.client); !errors.Is(err, ErrConfig) || duplicate != nil {
		t.Fatal("one admitted client acquired a second ALL-call owner")
	}
	if _, err := NewSDKConnection(owner, nil); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	endpoint, err := url.Parse("ws://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	native := gorillaws.New(connection.NewConfig(endpoint))
	if _, err := NewSDKConnection(nil, native); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	if _, err := NewSDKConnection(&SDKOwner{}, native); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	wrapped, err := NewSDKConnection(owner, native)
	if err != nil || wrapped.owner != owner || wrapped.sdkNative != native {
		t.Fatalf("concrete constructor=%v %v", wrapped, err)
	}
	_, other, _ := storeAccountingFixture(t, 1, 1)
	db, source := storeAccountingDB(t, ctx, owner)
	if _, err := SDKQuery[[]int](ctx, owner, db, "original owner remains live", nil, SDKRead()); err != nil {
		t.Fatal(err)
	}
	before := source.calls
	if _, err := SDKQuery[[]int](ctx, other, db, "wrong owner", nil, SDKRead()); err == nil || source.calls != before {
		t.Fatal("cross-owner call reached SDK")
	}
}

func TestStoreAccountingSuccessfulTerminalNeedsParentSettlement(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 1, 1)
	db, native := storeAccountingDB(t, ctx, owner)
	tx, err := SDKBegin(ctx, owner, db)
	if err != nil {
		t.Fatal(err)
	}
	native.call = func(ctx context.Context, request *connection.RPCRequest) (any, error) {
		if request.Method != "commit" {
			t.Error("unexpected native submission")
		}
		_ = owner.client.Fail(ctx, ErrTransport)
		return nil, nil // A real successful SDK terminal reply, but no settle ACK.
	}
	if err := SDKCommit(ctx, owner, tx); err == nil || !tx.IsClosed() {
		t.Fatalf("terminal accounting=%v native closed=%v", err, tx.IsClosed())
	}
	if !owner.transactions[0].used || owner.calls[0] == nil {
		t.Fatal("uncertain parent settlement discarded the reservation")
	}
	if err := SDKCancel(ctx, owner, tx); err == nil {
		t.Fatal("deferred cancel cleared the sticky terminal failure")
	}
	snapshot, _ := controller.Snapshot()
	if snapshot.Transactions != 1 || snapshot.Rows != 0 || snapshot.Complete {
		t.Fatalf("terminal prefix=%+v", snapshot)
	}
}

func TestStoreAccountingTransactionObjectCannotAlias(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 40, 2)
	db, native := storeAccountingDB(t, ctx, owner)
	first, err := SDKBegin(ctx, owner, db)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SDKBegin(ctx, owner, db)
	if err != nil {
		t.Fatal(err)
	}
	*first.ID() = *second.ID() // Actual SDK exposes this mutable pointer.
	before := native.calls
	if _, err := SDKQuery[[]int](ctx, owner, first, "aliased transaction", nil, SDKWrite(1)); err == nil || native.calls != before {
		t.Fatal("one SDK object borrowed another live transaction's accounting")
	}
	snapshot, _ := controller.Snapshot()
	if snapshot.Transactions != 2 || snapshot.Rows != 0 || !owner.transactions[0].used || !owner.transactions[1].used {
		t.Fatalf("alias refusal lost original reservations: %+v", snapshot)
	}
}

func TestStoreAccountingForwardsCopiedNativeUUID(t *testing.T) {
	for _, method := range []string{"query", "commit", "cancel"} {
		t.Run(method, func(t *testing.T) {
			ctx, owner, _ := storeAccountingFixture(t, 1, 1)
			db, native := storeAccountingDB(t, ctx, owner)
			tx, err := SDKBegin(ctx, owner, db)
			if err != nil {
				t.Fatal(err)
			}
			original := *tx.ID()
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				tx.ID().UUID[0]++ // After our final gate, before native marshaling.
				expected := *request
				if method == "query" {
					expected.Txn = &original
				} else {
					expected.Params = []any{&original}
				}
				actualBytes, err := native.codec.Marshal(request)
				if err != nil {
					return nil, err
				}
				wantBytes, err := native.codec.Marshal(&expected)
				if err != nil || !bytes.Equal(actualBytes, wantBytes) {
					t.Errorf("native %s UUID changed after admission: %v", method, err)
				}
				if method == "query" {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []int{1}}}, nil
				}
				return nil, nil
			}
			switch method {
			case "query":
				_, err = SDKQuery[[]int](ctx, owner, tx, "read", nil, SDKRead())
			case "commit":
				err = SDKCommit(ctx, owner, tx)
			case "cancel":
				err = SDKCancel(ctx, owner, tx)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
