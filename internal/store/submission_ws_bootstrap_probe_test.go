package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/gorilla/websocket"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

// This diagnostic uses the public Gorilla handshake and pinned SDK codec,
// not a replacement production SDK connection. Native completed KV counters
// remain diagnostics: they are not submitted rows or durable attempted prefixes.
func TestSubmissionNativeWebSocketBootstrapProbe(t *testing.T) {
	if os.Getenv("PHEBS_TEST_SUBMISSION_WS_PROBE") != "1" {
		t.Skip("set PHEBS_TEST_SUBMISSION_WS_PROBE=1 for the serialized native diagnostic")
	}
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	runtime, stop, err := startEngine(ctx, "memory")
	if err != nil {
		t.Fatalf("native start unavailable (error type %T)", err)
	}
	t.Cleanup(stop)
	if runtime.Surreal.Version != "3.2.0" {
		t.Fatalf("unexpected native version %q", runtime.Surreal.Version)
	}
	control, err := surrealdb.FromEndpointURLString(ctx, runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := control.Close(cleanup); err != nil {
			t.Errorf("close SDK control: %v", err)
		}
	})
	bearer, err := control.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"})
	if err != nil || bearer == "" {
		t.Fatalf("control SignIn/token: %v", err)
	}
	if _, err := surrealdb.Query[any](ctx, control,
		"DEFINE NAMESPACE "+submissionProbeControl+"; INFO FOR ROOT;", nil); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	metricsURL := "http://" + strings.TrimPrefix(runtime.Endpoint, "ws://") + "/metrics"
	snapshot := func() idleClaimMetricSnapshot {
		t.Helper()
		value, err := readSubmissionProbeMetrics(ctx, client, metricsURL, bearer)
		if err != nil {
			t.Fatalf("metrics unavailable: %v", err)
		}
		return value
	}
	measure := func(name string, operation func() error) error {
		t.Helper()
		first, before := snapshot(), snapshot()
		if delta, err := submissionProbeWriteDelta(first, before); err != nil || delta != 0 {
			t.Fatalf("%s initial scrape-only write control changed/unavailable: %d %v", name, delta, err)
		}
		operationErr := operation()
		after, last := snapshot(), snapshot()
		if delta, err := submissionProbeWriteDelta(after, last); err != nil || delta != 0 {
			t.Fatalf("%s final scrape-only write control changed/unavailable: %d %v", name, delta, err)
		}
		delta, err := submissionProbeWriteDelta(before, after)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s completed_kv_write_delta=%d read_attribution=unavailable error_type=%T\n%s",
			name, delta, operationErr, after.raw)
		return operationErr
	}
	headers := http.Header{
		"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte("root:root"))},
		"Surreal-NS":    {submissionProbeScope},
		"Surreal-DB":    {submissionProbeScope},
	}
	dial := func(header http.Header) (*websocket.Conn, int, error) {
		dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second, Subprotocols: []string{"cbor"}}
		conn, response, err := dialer.DialContext(ctx, runtime.Endpoint+"/rpc", header)
		status := 0
		if response != nil {
			status = response.StatusCode
			_ = response.Body.Close()
		}
		if err != nil {
			return nil, status, err
		}
		t.Cleanup(func() { _ = conn.Close() })
		if conn.Subprotocol() != "cbor" {
			return nil, status, fmt.Errorf("unexpected protocol %q", conn.Subprotocol())
		}
		conn.SetReadLimit(1 << 20)
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, status, err
		}
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return nil, status, err
		}
		return conn, status, nil
	}
	codec := surrealcbor.New()
	ordinal := 0
	call := func(conn *websocket.Conn, request connection.RPCRequest, result any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		ordinal++
		request.ID = fmt.Sprintf("probe-%d", ordinal)
		body, err := codec.Marshal(request)
		if err != nil {
			return err
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, body); err != nil {
			return err
		}
		kind, body, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if kind != websocket.BinaryMessage {
			return fmt.Errorf("unexpected message kind %d", kind)
		}
		var response connection.RPCResponse[cbor.RawMessage]
		if err := codec.Unmarshal(body, &response); err != nil {
			return err
		}
		if response.ID != request.ID {
			return fmt.Errorf("unexpected response identity")
		}
		if response.Error != nil {
			return response.Error
		}
		if result != nil {
			if response.Result == nil {
				return fmt.Errorf("missing RPC result")
			}
			return codec.Unmarshal(*response.Result, result)
		}
		return nil
	}
	query := func(conn *websocket.Conn, sql string, transaction *models.UUID) error {
		var rows []surrealdb.QueryResult[any]
		request := connection.RPCRequest{Method: "query", Params: []any{sql, map[string]any{}}}
		if transaction != nil {
			request.Txn = transaction
		}
		if err := call(conn, request, &rows); err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].Status != "OK" {
			return fmt.Errorf("native query result shape/status refused: %+v", rows)
		}
		return nil
	}
	root := submissionProbeInfo(ctx, t, control, "INFO FOR ROOT;")
	if _, exists := root.Namespaces[submissionProbeScope]; exists {
		t.Fatal("fresh target namespace already exists")
	}
	var fresh *websocket.Conn
	if err := measure("fresh_scoped_handshake", func() error {
		fresh, _, err = dial(headers)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Inspect from the independent control socket before any candidate RPC.
	root = submissionProbeInfo(ctx, t, control, "INFO FOR ROOT;")
	_, namespaceExists := root.Namespaces[submissionProbeScope]
	t.Logf("fresh_handshake_before_first_rpc namespace_exists=%t", namespaceExists)
	if namespaceExists {
		t.Fatal("fresh handshake unexpectedly created its target namespace")
	}
	// An absent containing namespace also establishes the target DB is absent;
	// no control USE can create it before this observation.
	// A RETURN verifies actual inherited selection without a USE request.
	var selection []surrealdb.QueryResult[[]string]
	if err := call(fresh, connection.RPCRequest{Method: "query", Params: []any{
		"RETURN [session::ns(), session::db()];", map[string]any{},
	}}, &selection); err != nil || len(selection) != 1 || selection[0].Status != "OK" ||
		len(selection[0].Result) != 2 || selection[0].Result[0] != submissionProbeScope || selection[0].Result[1] != submissionProbeScope {
		t.Fatalf("handshake did not inherit exact scope: %+v %v", selection, err)
	}
	if err := query(fresh, "INFO FOR ROOT;", nil); err != nil {
		t.Fatalf("handshake root authority: %v", err)
	}
	for _, name := range []string{"absent_auth", "wrong_auth"} {
		header := headers.Clone()
		header.Del("Authorization")
		if name == "wrong_auth" {
			header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("root:wrong")))
		}
		conn, status, err := dial(header)
		if err == nil {
			err = query(conn, "INFO FOR ROOT;", nil)
			_ = conn.Close()
			var serverError *connection.ServerError
			if !errors.As(err, &serverError) || serverError.Kind != "NotAllowed" {
				t.Fatalf("%s expected native NotAllowed, got %v", name, err)
			}
		} else if !errors.Is(err, websocket.ErrBadHandshake) || (status != http.StatusUnauthorized && status != http.StatusForbidden) {
			t.Fatalf("%s expected native HTTP auth refusal, status=%d error=%v", name, status, err)
		}
		t.Logf("%s root-authority refused HTTP_status=%d error_type=%T", name, status, err)
	}
	// Explicit neutral creation is setup, not attributed to a handshake.
	if _, err := surrealdb.Query[any](ctx, control, "DEFINE NAMESPACE IF NOT EXISTS "+submissionProbeScope+";", nil); err != nil {
		t.Fatal(err)
	}
	if err := control.Use(ctx, submissionProbeScope, submissionProbeScope); err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, control, "DEFINE DATABASE IF NOT EXISTS "+submissionProbeScope+"; CREATE probe:one SET value = 1;", nil); err != nil {
		t.Fatal(err)
	}
	var existing *websocket.Conn
	if err := measure("existing_scoped_handshake", func() error {
		existing, _, err = dial(headers)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var transaction models.UUID
	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"handshake_begin", func() error { return call(existing, connection.RPCRequest{Method: "begin"}, &transaction) }},
		{"handshake_transaction_write", func() error { return query(existing, "UPDATE probe:one SET value = 2;", &transaction) }},
		{"handshake_commit", func() error {
			return call(existing, connection.RPCRequest{Method: "commit", Params: []any{&transaction}}, nil)
		}},
	} {
		if err := measure(step.name, step.run); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
	}
	values, err := surrealdb.Query[[]struct {
		Value int `json:"value"`
	}](ctx, control, "SELECT * FROM probe:one;", nil)
	if err != nil || values == nil || len(*values) != 1 || len((*values)[0].Result) != 1 || (*values)[0].Result[0].Value != 2 {
		t.Fatalf("handshake transaction did not persist exact value: %+v %v", values, err)
	}
	for _, terminal := range []string{"cancel", "commit"} {
		if err := measure("sdk_implicit_select_"+terminal, func() error {
			_, err := surrealdb.Query[any](ctx, control, "SELECT * FROM probe:one;", nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		var tx *surrealdb.Transaction
		if err := measure("sdk_read_only_begin_"+terminal, func() error {
			tx, err = control.Begin(ctx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := measure("sdk_transaction_select_"+terminal, func() error {
			_, err := surrealdb.Query[any](ctx, tx, "SELECT * FROM probe:one;", nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := measure("sdk_read_only_"+terminal, func() error {
			if terminal == "cancel" {
				return tx.Cancel(ctx)
			}
			return tx.Commit(ctx)
		}); err != nil {
			t.Fatal(err)
		}
	}
}
