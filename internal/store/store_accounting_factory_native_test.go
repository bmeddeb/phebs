//go:build darwin || linux

package store

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	"github.com/gorilla/websocket"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

// Diagnostic only: actual fixed native control reply shapes, not attempted
// transaction evidence. Authentication token bytes are never emitted.
func TestStoreAccountingNativeControlReplyShapes(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	runtime, stop, err := startEngine(ctx, "memory")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	dialer := websocket.Dialer{Subprotocols: []string{"cbor"}, HandshakeTimeout: 5 * time.Second}
	ws, response, err := dialer.DialContext(ctx, runtime.Endpoint+"/rpc", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	if err := ws.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := ws.SetWriteDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	ws.SetReadLimit(16 << 10)
	codec := surrealcbor.New()
	for i, step := range []struct {
		name, method string
		params       []any
	}{
		{"signin", "signin", []any{surrealdb.Auth{Username: "root", Password: "root"}}},
		{"namespace_definition", "query", []any{"DEFINE NAMESPACE IF NOT EXISTS phebs;", nil}},
		{"namespace_use", "use", []any{"phebs", nil}},
		{"database_definition", "query", []any{"DEFINE DATABASE IF NOT EXISTS phebs;", nil}},
		{"database_use", "use", []any{"phebs", "phebs"}},
	} {
		id := i + 1
		raw, err := codec.Marshal(map[string]any{"id": id, "method": step.method, "params": step.params})
		if err != nil {
			t.Fatal(err)
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, raw); err != nil {
			t.Fatal(err)
		}
		_, raw, err = ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			ID     int             `json:"id"`
			Result cbor.RawMessage `json:"result"`
			Error  cbor.RawMessage `json:"error"`
		}
		if err := codec.Unmarshal(raw, &envelope); err != nil || envelope.ID != id {
			t.Fatal("native envelope or identity invalid", err)
		}
		kind := func(raw cbor.RawMessage) string {
			if len(raw) == 0 {
				return "absent"
			}
			return []string{"uint", "negative", "bytes", "text", "array", "map", "tag", "simple"}[raw[0]>>5]
		}
		encoded := "withheld"
		if step.method == "use" && len(envelope.Result) <= 16 {
			encoded = hex.EncodeToString(envelope.Result)
		}
		t.Logf("step=%s result_kind=%s result_bytes=%d result_hex=%s error_kind=%s", step.name, kind(envelope.Result), len(envelope.Result), encoded, kind(envelope.Error))
		if step.method == "use" && kind(envelope.Result) == "map" {
			var scope map[string]cbor.RawMessage
			if err := codec.Unmarshal(envelope.Result, &scope); err != nil {
				t.Fatal(err)
			}
			_, namespace := scope["namespace"]
			_, database := scope["database"]
			t.Logf("step=%s fields=%d namespace_key=%t database_key=%t", step.name, len(scope), namespace, database)
			for _, key := range []string{"namespace", "database"} {
				value := scope[key]
				var text string
				textErr := codec.Unmarshal(value, &text)
				encoded := "withheld"
				if len(value) <= 3 || textErr == nil && text == "phebs" {
					encoded = hex.EncodeToString(value)
				}
				t.Logf("step=%s field=%s kind=%s bytes=%d equals_phebs=%t hex=%s", step.name, key, kind(value), len(value), textErr == nil && text == "phebs", encoded)
			}
		}
		if step.method == "query" && kind(envelope.Result) == "array" {
			var units []struct {
				Status string          `json:"status"`
				Result cbor.RawMessage `json:"result"`
			}
			if err := codec.Unmarshal(envelope.Result, &units); err != nil || len(units) > 3 {
				t.Fatal("fixed query reply shape invalid", err)
			}
			for j, unit := range units {
				if unit.Status != "OK" && unit.Status != "ERR" {
					t.Fatal("unknown fixed query status")
				}
				t.Logf("step=%s unit=%d status=%s result_kind=%s result_bytes=%d", step.name, j, unit.Status, kind(unit.Result), len(unit.Result))
				if unit.Status == "ERR" {
					// Only classify known fixed-control failure families, never
					// print arbitrary native diagnostics or authentication data.
					var message string
					if err := codec.Unmarshal(unit.Result, &message); err == nil {
						t.Logf("step=%s scope_error=%t permission_error=%t", step.name, strings.Contains(message, "namespace") || strings.Contains(message, "database"), strings.Contains(message, "permission"))
					}
				}
			}
		}
	}
}

func TestStoreAccountingNativeFullInitializationAndReopen(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	// Two serial connection epochs, not a frozen producer/config issuer. Each
	// retains the logical-B transaction and aggregate-row ceilings unchanged;
	// the reducer independently enforces the fixed 512-row transaction maximum.
	controller, err := storeaccounting.New(t.Context(), storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: 2, Calls: 40, Transactions: 2}},
		Phases: []storeaccounting.Phase{
			{ID: 1, Transactions: 170, Rows: 170 * 512},
			{ID: 2, Transactions: 170, Rows: 170 * 512},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(t.Context(), controller, storeaccounting.WireConfig{
		Producers:  []storeaccounting.WireProducer{{ID: 2, Binding: [32]byte{1}, Phases: 3}},
		AckTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Error(err)
		}
	})
	file, config, err := transport.Open(2)
	if err != nil {
		t.Fatal(err)
	}
	client, err := storeaccounting.NewClient(ctx, file, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = client.Close(context.Background()) })
	owner, err := newStoreCallOwner(client)
	if err != nil {
		t.Fatal(err)
	}
	runtime, stop, err := startEngine(ctx, "memory")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	var current *Surreal
	closeCurrent := func() {
		if current == nil {
			return
		}
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer closeCancel()
		if err := current.Close(closeCtx); err != nil {
			t.Error(err)
		}
		current = nil
	}
	t.Cleanup(closeCurrent)
	for phase := uint32(1); phase <= 2; phase++ {
		if phase == 1 {
			current, err = openLocalRootWithOwner(ctx, runtime.Endpoint, owner)
		} else {
			current, err = openWithOwner(ctx, runtime.Endpoint, "root", "root", "phebs", "phebs", owner)
		}
		if err != nil {
			prefix, snapshotErr := controller.Snapshot()
			t.Fatalf("selected initialization phase=%d error=%v prefix=%+v snapshot_error=%v", phase, err, prefix, snapshotErr)
		}
		if current.accounting != owner {
			t.Fatal("factory did not capture the same actual owner")
		}
		results, err := storeQuery[struct {
			Tables map[string]string `json:"tables"`
		}](ctx, owner, current.db, "INFO FOR DB;", nil, storeRead())
		if err != nil || results == nil || len(*results) != 1 || (*results)[0].Status != "OK" || len((*results)[0].Result.Tables) == 0 {
			t.Fatalf("actual initialized database census unavailable: %v", err)
		}
		beforeAuth, err := controller.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		initial := beforeAuth.Phases[phase-1]
		t.Logf("phase=%d pre_auth_transactions=%d pre_auth_rows=%d maximum_rows=%d tables=%d", phase,
			initial.Transactions, initial.Rows, initial.MaximumRows, len((*results)[0].Result.Tables))
		user, authErr := current.CreateFirstUser(ctx, User{
			ID: "selected-initial-admin", Email: "neutral@example.com", NormalizedEmail: "neutral@example.com",
			DisplayName: "Neutral", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		})
		if phase == 1 && (authErr != nil || user == nil || !user.IsAdmin) ||
			phase == 2 && !errors.Is(authErr, ErrConflict) {
			t.Fatalf("selected first-user phase=%d result_present=%t error=%v", phase, user != nil, authErr)
		}
		afterAuth, err := controller.Snapshot()
		if err != nil || afterAuth.Phases[phase-1].Transactions != initial.Transactions+1 ||
			afterAuth.Phases[phase-1].Rows != initial.Rows+2 || afterAuth.Producers[0].Calls != 0 {
			t.Fatalf("selected first-user prefix=%+v error=%v", afterAuth, err)
		}
		closeCurrent()
		if err := transport.Fence(); err != nil {
			t.Fatal(err)
		}
		if err := owner.Checkpoint(ctx); err != nil {
			t.Fatal(err)
		}
		prefix, err := controller.Snapshot()
		if err != nil || prefix.Phases[phase-1].Transactions == 0 || prefix.Phases[phase-1].MaximumRows != 488 {
			t.Fatalf("actual initialization prefix=%+v %v", prefix, err)
		}
		t.Logf("phase=%d transactions=%d rows=%d maximum_rows=%d tables=%d", phase,
			prefix.Phases[phase-1].Transactions, prefix.Phases[phase-1].Rows, prefix.Phases[phase-1].MaximumRows, len((*results)[0].Result.Tables))
		if phase == 1 {
			if err := transport.Advance(); err != nil {
				t.Fatal(err)
			}
			if err := owner.Resume(2); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := transport.Wait(ctx, 2); err != nil {
		t.Fatal(err)
	}
	prefix, err := controller.Snapshot()
	if err != nil || !prefix.Complete {
		t.Fatalf("final actual store prefix incomplete: %+v %v", prefix, err)
	}
}
