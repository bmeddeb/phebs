//go:build darwin || linux

package dispatchadmission

import (
	"context"
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

// These are mechanical two-phase fixtures, not frozen producer/epoch issuers.
func storePhaseLifetime(t *testing.T, client *Client) (*ProductionLifetime, *storeaccounting.Controller, *storeaccounting.Transport) {
	t.Helper()
	parentCtx, cancelParent := context.WithCancel(t.Context())
	t.Cleanup(cancelParent)
	ctx, cancel := context.WithCancel(client.Context())
	controller, err := storeaccounting.New(parentCtx, storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: 2, Calls: 1, Transactions: 1}},
		Phases:    []storeaccounting.Phase{{ID: 1, Transactions: 10, Rows: 20}, {ID: 2, Transactions: 10, Rows: 20}},
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(parentCtx, controller, storeaccounting.WireConfig{
		Producers: []storeaccounting.WireProducer{{ID: 2, Binding: [32]byte{3}, Phases: 3}}, AckTimeout: time.Second,
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
	storeClient, err := storeaccounting.NewClient(ctx, file, config)
	if err != nil {
		cancel()
		_ = transport.Close()
		t.Fatal(err)
	}
	lifetime := &ProductionLifetime{client: client, storeClient: storeClient, cancelStore: cancel}
	client.mu.Lock()
	client.storeLifetime = lifetime
	client.mu.Unlock()
	t.Cleanup(func() { cancel(); _ = storeClient.Close(context.Background()); _ = transport.Close() })
	return lifetime, controller, transport
}

type phaseStoreDecodeGate struct {
	*surrealcbor.Codec
	ctx              context.Context
	entered, release chan struct{}
}

func (gate *phaseStoreDecodeGate) Unmarshal(raw []byte, value any) error {
	if _, ok := value.(*[]int); ok {
		close(gate.entered)
		select {
		case <-gate.release:
		case <-gate.ctx.Done():
			return gate.ctx.Err()
		}
	}
	return gate.Codec.Unmarshal(raw, value)
}

func phaseStoreDB(t *testing.T, ctx context.Context, owner *storeaccounting.SDKOwner, gate *phaseStoreDecodeGate, lostTerminal bool) (*surrealdb.DB, func(), <-chan struct{}) {
	t.Helper()
	joined := make(chan struct{})
	terminalSeen := make(chan struct{})
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
			_, raw, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var request struct {
				ID     any    `json:"id"`
				Method string `json:"method"`
			}
			if err := codec.Unmarshal(raw, &request); err != nil {
				t.Error(err)
				return
			}
			var result any
			switch request.Method {
			case "begin":
				id := models.UUID{}
				id.UUID[0] = 1
				result = id
			case "query":
				result = []surrealdb.QueryResult[any]{{Status: "OK", Result: []int{1}}}
			case "commit", "cancel":
				if lostTerminal {
					close(terminalSeen)
					return
				}
			default:
				t.Error("unexpected SDK method")
				return
			}
			raw, err = codec.Marshal(map[string]any{"id": request.ID, "result": result})
			if err != nil {
				t.Error(err)
				return
			}
			if err := ws.WriteMessage(websocket.BinaryMessage, raw); err != nil {
				return
			}
		}
	}))
	endpoint, err := url.Parse("ws" + strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	config := connection.NewConfig(endpoint)
	if gate != nil {
		config.Unmarshaler = gate
	}
	conn, err := storeaccounting.NewSDKConnection(owner, gorillaws.New(config))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	db, err := surrealdb.FromConnection(ctx, conn)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	closeDB := func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := db.Close(closeCtx); err != nil && !lostTerminal {
			t.Error(err)
		}
		server.Close()
		select {
		case <-joined:
		case <-closeCtx.Done():
			t.Error("SDK fixture socket did not join")
		}
	}
	t.Cleanup(closeDB)
	return db, closeDB, terminalSeen
}

func TestStorePhaseConcreteHandoffKeepsSemanticFences(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	config := testConfig()
	config.Producers[0].ID = 2
	dispatch, client, server := paired(t, config)
	lifetime, store, transport := storePhaseLifetime(t, client)
	owner, err := lifetime.TakeStoreOwner()
	if err != nil {
		t.Fatal(err)
	}
	control, done := phaseOwnersPair(t, client, time.Second)
	lifetime.controlDone = done
	owners, err := NewOwners(ctx, OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || client.bindOwners(owners) != nil {
		t.Fatal("semantic owner binding failed")
	}
	db, closeDB, _ := phaseStoreDB(t, ctx, owner, nil, false)
	if _, err := storeaccounting.SDKQuery[[]int](ctx, owner, db, "source write", nil, storeaccounting.SDKWrite(2)); err != nil {
		t.Fatal(err)
	}
	if control.DrainOwners(ctx) != nil || control.Pause(ctx) != nil || dispatch.Fence() != nil || transport.Fence() != nil || control.Checkpoint(ctx) != nil {
		t.Fatal("first concrete phase did not drain")
	}
	prefix, err := store.Snapshot()
	if err != nil || prefix.Producers[0].Checkpoint != 1 || prefix.Transactions != 1 || prefix.Rows != 2 {
		t.Fatalf("store checkpoint=%+v %v", prefix, err)
	}
	if dispatch.Advance() != nil || transport.Advance() != nil || control.Resume(ctx) != nil {
		t.Fatal("coordinated resume failed")
	}
	client.mu.Lock()
	requestsOpen := client.ownerRequestsOpen
	client.mu.Unlock()
	owners.mu.Lock()
	paused, requestsFenced := owners.paused, owners.requestsFenced
	owners.mu.Unlock()
	if requestsOpen || !paused || !requestsFenced || control.RequestToken() != "" {
		t.Fatal("resume reopened semantic work")
	}
	// Only the explicit observation window opens here; ordinary owners stay parked.
	if err := control.OpenRequests(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err := client.enterOwnerRequest(ctx, owners, control.RequestToken())
	if err != nil {
		t.Fatal(err)
	}
	_, err = storeaccounting.SDKQuery[[]int](ctx, owner, db, "prepared read", nil, storeaccounting.SDKRead())
	turn.End()
	if err != nil || control.FenceRequests(ctx) != nil || control.Pause(ctx) != nil || dispatch.Fence() != nil || transport.Fence() != nil || control.Checkpoint(ctx) != nil {
		t.Fatal("prepared phase did not drain", err)
	}
	closeDB()
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := phaseTestResult(t, server); err != nil {
		t.Fatal(err)
	}
	if err := transport.Wait(ctx, 2); err != nil {
		t.Fatal(err)
	}
	prefix, err = store.Snapshot()
	if err != nil || !prefix.Complete || prefix.Transactions != 1 || prefix.Rows != 2 {
		t.Fatalf("closed prefix=%+v %v", prefix, err)
	}
}

func TestStorePhaseRefusesUnclaimedReadTailUUIDAndUnknownTerminal(t *testing.T) {
	for _, mode := range []string{"unclaimed", "typed_read", "read_only_uuid", "unknown_terminal"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			config := testConfig()
			config.Producers[0].ID = 2
			dispatch, client, server := paired(t, config)
			lifetime, store, transport := storePhaseLifetime(t, client)
			control, done := phaseTestPair(t, client)
			lifetime.controlDone = done
			wantTransactions, wantRows := uint64(0), uint64(0)
			var queryDone chan error
			if mode != "unclaimed" {
				owner, err := lifetime.TakeStoreOwner()
				if err != nil {
					t.Fatal(err)
				}
				var gate *phaseStoreDecodeGate
				if mode == "typed_read" {
					gate = &phaseStoreDecodeGate{Codec: surrealcbor.New(), ctx: client.Context(), entered: make(chan struct{}), release: make(chan struct{})}
				}
				db, _, terminalSeen := phaseStoreDB(t, ctx, owner, gate, mode == "unknown_terminal")
				if mode == "typed_read" {
					queryDone = make(chan error, 1)
					go func() {
						_, err := storeaccounting.SDKQuery[[]int](ctx, owner, db, "read", nil, storeaccounting.SDKRead())
						queryDone <- err
					}()
					select {
					case <-gate.entered:
					case <-ctx.Done():
						t.Fatal("typed decode did not start")
					}
				} else {
					tx, err := storeaccounting.SDKBegin(ctx, owner, db)
					if err != nil {
						t.Fatal(err)
					}
					wantTransactions = 1
					if mode == "read_only_uuid" {
						if _, err := storeaccounting.SDKQuery[[]int](ctx, owner, tx, "transaction read", nil, storeaccounting.SDKRead()); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "unknown_terminal" {
						if _, err := storeaccounting.SDKQuery[[]int](ctx, owner, tx, "append", nil, storeaccounting.SDKWrite(2)); err != nil {
							t.Fatal(err)
						}
						wantRows = 2
						terminalCtx, terminalCancel := context.WithCancel(ctx)
						defer terminalCancel()
						terminalDone := make(chan error, 1)
						go func() { terminalDone <- storeaccounting.SDKCommit(terminalCtx, owner, tx) }()
						select {
						case <-terminalSeen:
						case <-ctx.Done():
							t.Fatal("terminal never reached native transport")
						}
						terminalCancel()
						if err := <-terminalDone; err == nil {
							t.Fatal("lost native terminal succeeded")
						}
					}
				}
			}
			if control.Pause(ctx) != nil || dispatch.Fence() != nil {
				t.Fatal("parent fences failed")
			}
			if err := transport.Fence(); err != nil && mode != "unknown_terminal" {
				t.Fatalf("store fence failure=%v", err)
			}
			if err := control.Checkpoint(ctx); err == nil {
				t.Fatal("unproven SDK drainage got a PC01 ACK")
			}
			if queryDone != nil {
				select {
				case err := <-queryDone:
					if err == nil {
						t.Fatal("failed phase read completed")
					}
				case <-ctx.Done():
					t.Fatal("typed read did not join")
				}
			}
			client.mu.Lock()
			acknowledged := client.controlCheckpointAcknowledged
			client.mu.Unlock()
			dispatchPrefix, _ := dispatch.Snapshot()
			if acknowledged || dispatchPrefix.Producers[0].Checkpoint != 0 {
				t.Fatal("failed SDK checkpoint advanced dispatch")
			}
			prefix, _ := store.Snapshot()
			if prefix.Transactions != wantTransactions || prefix.Rows != wantRows || prefix.Producers[0].Checkpoint != 0 || prefix.Complete {
				t.Fatalf("retained prefix=%+v", prefix)
			}
			if err := lifetime.Close(ctx); err == nil {
				t.Fatal("failed SDK owner closed successfully")
			}
			if err := phaseTestResult(t, server); err == nil {
				t.Fatal("dispatch failure vanished")
			}
		})
	}
}
