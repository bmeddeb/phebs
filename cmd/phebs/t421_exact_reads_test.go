package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

const t421ExactReadTestCredential = "t421-exact-read-test-credential"

type t421ExactReadTestCapture struct {
	mu        sync.Mutex
	reports   [][]byte
	failures  []error
	sinkErr   error
	sinkPanic bool
}

func (capture *t421ExactReadTestCapture) report(payload []byte) error {
	capture.mu.Lock()
	capture.reports = append(capture.reports, append([]byte(nil), payload...))
	err := capture.sinkErr
	shouldPanic := capture.sinkPanic
	capture.mu.Unlock()
	if shouldPanic {
		panic("private sink panic")
	}
	return err
}

func (capture *t421ExactReadTestCapture) fail(err error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.failures = append(capture.failures, err)
}

func (capture *t421ExactReadTestCapture) snapshot() ([][]byte, []error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	reports := make([][]byte, len(capture.reports))
	for index := range capture.reports {
		reports[index] = append([]byte(nil), capture.reports[index]...)
	}
	return reports, append([]error(nil), capture.failures...)
}

func TestT421ExactReadsEnvironmentAndFrozenBounds(t *testing.T) {
	prior, present := os.LookupEnv(t421ExactReadsEnvironment)
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(t421ExactReadsEnvironment, prior)
		} else {
			_ = os.Unsetenv(t421ExactReadsEnvironment)
		}
	})
	if err := os.Unsetenv(t421ExactReadsEnvironment); err != nil {
		t.Fatal(err)
	}
	if enabled, err := t421ExactReadsEnabled(); err != nil || enabled {
		t.Fatalf("absent exact reads = %t, %v", enabled, err)
	}
	if err := os.Setenv(t421ExactReadsEnvironment, t421ExactReadsContract); err != nil {
		t.Fatal(err)
	}
	if enabled, err := t421ExactReadsEnabled(); err != nil || !enabled {
		t.Fatalf("exact reads = %t, %v", enabled, err)
	}
	if err := os.Setenv(t421ExactReadsEnvironment, "unknown"); err != nil {
		t.Fatal(err)
	}
	if _, err := t421ExactReadsEnabled(); err == nil {
		t.Fatal("unknown exact-read contract was accepted")
	}

	if t421ExactReadMaxOrdinal != math.MaxUint64-1 {
		t.Fatalf("maximum ordinal = %d", t421ExactReadMaxOrdinal)
	}
	if t421ExtractionProgressReadLimits() != (readaccounting.Counts{
		ControlFileReads: 66, StoreReadAttempts: 130,
	}) {
		t.Fatalf("extraction-progress limits = %+v", t421ExtractionProgressReadLimits())
	}
	lifecycle := httptest.NewRequest(http.MethodGet, "/api/lifecycle-status", nil)
	if limits, ok := t421ExactReadLimits(lifecycle); !ok || limits != (readaccounting.Counts{}) {
		t.Fatalf("lifecycle-status limits = %+v, %t", limits, ok)
	}
	if err := t421ExactReadTerminalError(nil); err != nil {
		t.Fatalf("absent exact reads acquired terminal error: %v", err)
	}
	failed := make(chan error, 1)
	failed <- errT421ExactReadAccounting
	if err := t421ExactReadTerminalError(failed); !errors.Is(err, errT421ExactReadAccounting) {
		t.Fatalf("terminal exact-read error = %v", err)
	}
}

func TestT421ExactReadHandlerReportsZeroReadLifecycleSnapshot(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	monitor, err := lifecycle.NewStatusMonitor(true, lifecycle.ClosedOwners())
	if err != nil {
		t.Fatal(err)
	}
	lifecycleHandler := api.New(api.Options{
		Version: "test",
		IsAdmin: func(ctx context.Context) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			return ok && principal.IsAdmin
		},
		LifecycleStatusSource: func(context.Context) lifecycle.Status {
			return monitor.Snapshot()
		},
	})
	capture := &t421ExactReadTestCapture{}
	handler := authService.Require(t421ExactReadHandler(
		true,
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/api/lifecycle-status":
				lifecycleHandler.ServeHTTP(writer, request)
			case "/api/extraction-progress":
				if err := readaccounting.Charge(request.Context(), readaccounting.ControlFileRead, 1); err != nil {
					t.Error(err)
				}
				writer.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("path = %q", request.URL.Path)
			}
		}),
		capture.report,
		capture.fail,
	))
	response := serveT421ExactReadRequest(
		t, handler, exactT421ReadRequest(http.MethodGet, "/api/lifecycle-status", 1),
	)
	want := `{"schema":"t421-source-free-read-accounting-v1","request_ordinal":1,"status":"complete","control_file_reads":0,"store_read_attempts":0,"member_visits":0,"store_write_attempts":0}`
	if response.status != http.StatusOK || response.report != want {
		t.Fatalf("lifecycle response = %d %q report=%q", response.status, response.body, response.report)
	}
	var status lifecycle.Status
	if err := json.Unmarshal(response.body, &status); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.ValidateStatus(status); err != nil {
		t.Fatal(err)
	}
	response = serveT421ExactReadRequest(
		t, handler, exactT421ReadRequest(http.MethodGet, "/api/extraction-progress?repository=private", 2),
	)
	wantSecond := `{"schema":"t421-source-free-read-accounting-v1","request_ordinal":2,"status":"complete","control_file_reads":1,"store_read_attempts":0,"member_visits":0,"store_write_attempts":0}`
	if response.status != http.StatusNoContent || response.report != wantSecond {
		t.Fatalf("shared ordinal response = %d report=%q", response.status, response.report)
	}
	reports, failures := capture.snapshot()
	if len(reports) != 2 || string(reports[0]) != want || string(reports[1]) != wantSecond || len(failures) != 0 {
		t.Fatalf("lifecycle reports = %q failures=%v", reports, failures)
	}
}

func TestT421ExactReadHandlerIsInactiveWithoutContract(t *testing.T) {
	nextFunc := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	var next http.Handler = &nextFunc
	if got := t421ExactReadHandler(false, next, nil, nil); got != next {
		t.Fatal("inactive exact reads wrapped the ordinary handler")
	}
	otherFunc := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	var other http.Handler = &otherFunc
	if got, gotOther := t421ExactReadHandlers(false, next, other, nil, nil); got != next || gotOther != other {
		t.Fatal("inactive shared exact reads wrapped an ordinary handler")
	}
	for _, principal := range []struct {
		name string
		in   auth.Principal
		want bool
	}{
		{"legacy", auth.Principal{AuthMethod: "api_key", APIKeyID: "legacy-config", IsAdmin: true}, true},
		{"named", auth.Principal{AuthMethod: "api_key", APIKeyID: "named", IsAdmin: true, User: &store.User{ID: "user"}}, false},
		{"session", auth.Principal{AuthMethod: "session", IsAdmin: true, User: &store.User{ID: "user"}}, false},
		{"missing id", auth.Principal{AuthMethod: "api_key", IsAdmin: true}, false},
		{"not admin", auth.Principal{AuthMethod: "api_key", APIKeyID: "legacy-config"}, false},
	} {
		t.Run(principal.name, func(t *testing.T) {
			if got := t421ExactReadLegacyPrincipal(principal.in); got != principal.want {
				t.Fatalf("legacy principal = %t; want %t", got, principal.want)
			}
		})
	}
}

func TestT421ExactReadHandlersShareAPIAndMCPState(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	capture := &t421ExactReadTestCapture{}
	var apiCalls, mcpCalls atomic.Uint64
	apiNext := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		apiCalls.Add(1)
		if _, ok := auth.PrincipalFromContext(request.Context()); !ok {
			t.Error("API exact ledger ran before authentication")
		}
		if err := readaccounting.Charge(request.Context(), readaccounting.ControlFileRead, 2); err != nil {
			t.Error(err)
		}
		if err := readaccounting.Charge(request.Context(), readaccounting.StoreReadAttempt, 2); err != nil {
			t.Error(err)
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	var gotMCPBody []byte
	mcpNext := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mcpCalls.Add(1)
		if _, ok := auth.PrincipalFromContext(request.Context()); !ok {
			t.Error("MCP exact ledger ran before authentication")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		gotMCPBody = append([]byte(nil), body...)
		if err := readaccounting.Charge(request.Context(), readaccounting.StoreReadAttempt, 7); err != nil {
			t.Error(err)
		}
		if err := readaccounting.Charge(request.Context(), readaccounting.MemberVisit, 2); err != nil {
			t.Error(err)
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	apiHandler, mcpHandler := t421ExactReadHandlers(
		true, apiNext, mcpNext, capture.report, capture.fail,
	)
	apiHandler, mcpHandler = authService.Require(apiHandler), authService.Require(mcpHandler)

	apiResponse := serveT421ExactReadRequest(t, apiHandler, exactT421ReadRequest(
		http.MethodGet, "/api/search?q=needle&scope=all_code&max_matches=1&context_lines=0", 1,
	))
	if apiResponse.status != http.StatusNoContent ||
		!strings.Contains(apiResponse.report, `"request_ordinal":1`) ||
		!strings.Contains(apiResponse.report, `"control_file_reads":2`) ||
		!strings.Contains(apiResponse.report, `"store_read_attempts":2`) {
		t.Fatalf("API exact response = %+v", apiResponse)
	}

	// Unmarked MCP setup traffic stays entirely outside the exact ledger and
	// does not consume the next shared ordinal.
	for _, method := range []string{"initialize", "tools/list"} {
		body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":{}}`
		request := httptest.NewRequest(http.MethodPost, t421ExactMCPPath, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
		response := serveT421ExactReadRequest(t, mcpHandler, request)
		if response.status != http.StatusNoContent || response.report != "" {
			t.Fatalf("unmarked MCP %q = %+v", method, response)
		}
	}

	mcpBody := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_service","arguments":{"repository":"example.com/repo","service_key":"orders"}}}`)
	mcpRequest := httptest.NewRequest(http.MethodPost, t421ExactMCPPath, bytes.NewReader(mcpBody))
	mcpRequest.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
	mcpRequest.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
	mcpRequest.Header.Set(t421ExactReadOrdinalHeader, "2")
	mcpResponse := serveT421ExactReadRequest(t, mcpHandler, mcpRequest)
	if mcpResponse.status != http.StatusNoContent || !bytes.Equal(gotMCPBody, mcpBody) ||
		!strings.Contains(mcpResponse.report, `"request_ordinal":2`) ||
		!strings.Contains(mcpResponse.report, `"store_read_attempts":7`) ||
		!strings.Contains(mcpResponse.report, `"member_visits":2`) {
		t.Fatalf("MCP exact response = %+v body=%q", mcpResponse, gotMCPBody)
	}

	unsupported := []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`)
	unsupportedRequest := httptest.NewRequest(http.MethodPost, t421ExactMCPPath, bytes.NewReader(unsupported))
	unsupportedRequest.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
	unsupportedRequest.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
	unsupportedRequest.Header.Set(t421ExactReadOrdinalHeader, "3")
	refused := serveT421ExactReadRequest(t, mcpHandler, unsupportedRequest)
	latched := serveT421ExactReadRequest(t, apiHandler, exactT421ReadRequest(
		http.MethodGet, "/api/search?q=needle&scope=all_code&max_matches=1&context_lines=0", 3,
	))
	reports, failures := capture.snapshot()
	if refused.status != http.StatusConflict || latched.status != http.StatusConflict ||
		apiCalls.Load() != 1 || mcpCalls.Load() != 3 || len(reports) != 3 || len(failures) != 1 ||
		!errors.Is(failures[0], errT421ExactReadAdmission) {
		t.Fatalf("shared refusal=%d/%d calls=%d/%d reports=%d failures=%v",
			refused.status, latched.status, apiCalls.Load(), mcpCalls.Load(), len(reports), failures)
	}
}

func TestT421ExactMCPAdmissionRejectsSetupAndMalformedCalls(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "search call",
			body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_code","arguments":{"query":"needle","max_matches":1,"context_lines":0,"scope":"all_code"}}}`,
			want: true,
		},
		{
			name: "relationship call",
			body: `{"jsonrpc":"2.0","id":"q","method":"tools/call","params":{"name":"list_service_relationships","arguments":{"repositories":["example.com/repo"],"service_key":"orders","view":"callers","kind":"rpc","plane":"grpc","lookup_key":"/svc/Call","page_size":1}}}`,
			want: true,
		},
		{name: "initialize", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`},
		{name: "list tools", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`},
		{name: "notification", body: `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"search_code","arguments":{"query":"needle","max_matches":1,"context_lines":0,"scope":"all_code"}}}`},
		{name: "null id", body: `{"jsonrpc":"2.0","id":null,"method":"tools/call","params":{"name":"get_service","arguments":{"repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "boolean id", body: `{"jsonrpc":"2.0","id":true,"method":"tools/call","params":{"name":"get_service","arguments":{"repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "object id", body: `{"jsonrpc":"2.0","id":{},"method":"tools/call","params":{"name":"get_service","arguments":{"repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "unsupported tool", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{}}}`},
		{name: "unknown argument", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_service","arguments":{"repository":"example.com/repo","service_key":"orders","extra":true}}}`},
		{name: "unknown top-level field", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","extra":true,"params":{"name":"get_service","arguments":{"repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "unknown params field", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_service","extra":true,"arguments":{"repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "duplicate top-level field", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","method":"tools/call","params":{"name":"get_service","arguments":{"repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "duplicate params field", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","name":"get_service","arguments":{"repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "duplicate argument field", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_service","arguments":{"repository":"wrong.example/repo","repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "case variant method", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","METHOD":"tools/call","params":{"name":"get_service","arguments":{"repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "case variant tool name", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","NAME":"get_service","arguments":{"repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "case variant argument", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_service","arguments":{"repository":"wrong.example/repo","Repository":"example.com/repo","service_key":"orders"}}}`},
		{name: "malformed", body: `{"jsonrpc":"2.0"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, t421ExactMCPPath, strings.NewReader(test.body))
			limits, ok := t421ExactReadLimits(request)
			if ok != test.want || ok && limits == (readaccounting.Counts{}) {
				t.Fatalf("admission = %t, limits %+v; want %t", ok, limits, test.want)
			}
		})
	}
}

func TestT421ExactMCPAdmissionBoundsJSONDepth(t *testing.T) {
	raw := strings.Repeat("[", t421ExactJSONMaxDepth+2) + "0" +
		strings.Repeat("]", t421ExactJSONMaxDepth+2)
	if t421ExactJSONKeysUnique([]byte(raw)) {
		t.Fatal("over-depth exact MCP JSON was accepted")
	}
}

func TestT421ExactReadFailureCancelsActiveFinalAuthority(t *testing.T) {
	root, cancel := context.WithCancel(t.Context())
	defer cancel()
	if t421ExactReadServerBaseContext(root, false) != nil {
		t.Fatal("ordinary server acquired an exact-read base context")
	}
	base := t421ExactReadServerBaseContext(root, true)
	if base == nil || base(nil) != root {
		t.Fatal("exact-read server did not retain its cancellation root")
	}

	authService := newT421ExactReadAuthService(t)
	capture := &t421ExactReadTestCapture{}
	started := make(chan struct{})
	canceled := make(chan error, 1)
	var committed atomic.Bool
	final := t421ExactFinalAuthorityRead{Read: func(ctx context.Context) ([]byte, func() error, error) {
		close(started)
		<-ctx.Done()
		canceled <- ctx.Err()
		return []byte(`{}`), func() error {
			committed.Store(true)
			return nil
		}, ctx.Err()
	}}
	handler := authService.Require(t421ExactReadHandler(
		true, http.NotFoundHandler(), capture.report,
		func(err error) {
			capture.fail(err)
			cancel()
		},
		final,
	))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, BaseContext: base}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		<-serveDone
	}()

	request := func(ordinal uint64) *http.Request {
		value, err := http.NewRequestWithContext(
			t.Context(), http.MethodGet,
			"http://"+listener.Addr().String()+t421ExactFinalAuthorityPath, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		value.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
		value.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
		value.Header.Set(t421ExactReadOrdinalHeader, strconv.FormatUint(ordinal, 10))
		return value
	}
	type result struct {
		status int
		err    error
	}
	firstRequest := request(1)
	firstDone := make(chan result, 1)
	go func() {
		response, err := http.DefaultClient.Do(firstRequest)
		if err != nil {
			firstDone <- result{err: err}
			return
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		firstDone <- result{status: response.StatusCode, err: errors.Join(readErr, response.Body.Close())}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("final-authority read did not start")
	}

	response, err := http.DefaultClient.Do(request(2))
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusConflict {
		t.Fatalf("overlap response = %d, read=%v close=%v", response.StatusCode, readErr, closeErr)
	}
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("active read cancellation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("failure latch did not cancel the active read")
	}
	select {
	case first := <-firstDone:
		if first.err != nil || first.status != http.StatusConflict {
			t.Fatalf("active response = %d, %v", first.status, first.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled active read did not return")
	}
	if committed.Load() {
		t.Fatal("canceled final-authority read committed its pending cache")
	}
}

func TestT421ExactReadHandlerReportsPostAuthCounts(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	var calls atomic.Uint64
	capture := &t421ExactReadTestCapture{}
	next := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		principal, ok := auth.PrincipalFromContext(request.Context())
		if !ok || !t421ExactReadLegacyPrincipal(principal) {
			t.Error("authenticated legacy principal was not preserved")
		}
		if err := readaccounting.Charge(request.Context(), readaccounting.ControlFileRead, 66); err != nil {
			t.Error(err)
		}
		if err := readaccounting.Charge(request.Context(), readaccounting.StoreReadAttempt, 130); err != nil {
			t.Error(err)
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := authService.Require(t421ExactReadHandler(
		true, next, capture.report, capture.fail,
	))

	for ordinal := uint64(1); ordinal <= 2; ordinal++ {
		response := serveT421ExactReadRequest(
			t, handler, exactT421ReadRequest(http.MethodGet, "/api/extraction-progress?repository=private-repository", ordinal),
		)
		if response.status != http.StatusNoContent || len(response.body) != 0 {
			t.Fatalf("request %d response = %d %q", ordinal, response.status, response.body)
		}
		want := `{"schema":"t421-source-free-read-accounting-v1","request_ordinal":` +
			strconv.FormatUint(ordinal, 10) +
			`,"status":"complete","control_file_reads":66,"store_read_attempts":130,"member_visits":0,"store_write_attempts":0}`
		if response.report != want {
			t.Fatalf("request %d report = %s; want %s", ordinal, response.report, want)
		}
		if strings.Contains(response.report, "private-repository") {
			t.Fatal("source-free report retained the request query")
		}
	}

	// Exact mode leaves unrelated, unmarked routes byte-for-byte on the
	// ordinary path. Marking an unsupported route is tested as admission below.
	ordinary := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	ordinary.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
	response := serveT421ExactReadRequest(t, handler, ordinary)
	if response.status != http.StatusNoContent || response.report != "" {
		t.Fatalf("ordinary route = %d, report %q", response.status, response.report)
	}
	reports, failures := capture.snapshot()
	if calls.Load() != 3 || len(reports) != 2 || len(failures) != 0 {
		t.Fatalf("calls = %d, reports = %d, failures = %v", calls.Load(), len(reports), failures)
	}
	for index := range reports {
		if string(reports[index]) != exactT421ReportForOrdinal(uint64(index+1)) {
			t.Fatalf("sink report %d = %s", index, reports[index])
		}
	}
}

func TestT421ExactReadHandlerUsesNetworkTrailerAndIgnoresAnonymousPublicHeaders(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	capture := &t421ExactReadTestCapture{}
	apiHandler := t421ExactReadHandler(true, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/health":
			writer.WriteHeader(http.StatusNoContent)
		case "/api/extraction-progress":
			if err := readaccounting.Charge(request.Context(), readaccounting.ControlFileRead, 1); err != nil {
				t.Error(err)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, "{}")
		default:
			http.NotFound(writer, request)
		}
	}), capture.report, capture.fail)
	server := httptest.NewServer(newHTTPHandler(
		authService, apiHandler, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(),
	))
	t.Cleanup(server.Close)

	public, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	public.Header.Set(t421ExactReadActivationHeader, "malformed")
	response, err := server.Client().Do(public)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusNoContent ||
		response.Trailer.Get(t421ExactReadTrailer) != "" {
		t.Fatalf("public response = %d trailer %q read=%v close=%v", response.StatusCode, response.Trailer.Get(t421ExactReadTrailer), readErr, closeErr)
	}
	unauthenticated, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet, server.URL+"/api/extraction-progress?repository=private-repository", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
	unauthenticated.Header.Set(t421ExactReadOrdinalHeader, "1")
	response, err = server.Client().Do(unauthenticated)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr = io.Copy(io.Discard, response.Body)
	closeErr = response.Body.Close()
	_, unauthenticatedFailures := capture.snapshot()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusUnauthorized ||
		response.Trailer.Get(t421ExactReadTrailer) != "" || len(unauthenticatedFailures) != 0 {
		t.Fatalf("unauthenticated response = %d trailer %q failures=%v read=%v close=%v", response.StatusCode, response.Trailer.Get(t421ExactReadTrailer), unauthenticatedFailures, readErr, closeErr)
	}

	exact, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet, server.URL+"/api/extraction-progress?repository=private-repository", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	exact.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
	exact.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
	exact.Header.Set(t421ExactReadOrdinalHeader, "1")
	response, err = server.Client().Do(exact)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr = response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || string(body) != "{}" {
		t.Fatalf("exact response = %d %q read=%v close=%v", response.StatusCode, body, readErr, closeErr)
	}
	encoded := response.Trailer.Get(t421ExactReadTrailer)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode network trailer %q: %v", encoded, err)
	}
	want := `{"schema":"t421-source-free-read-accounting-v1","request_ordinal":1,"status":"complete","control_file_reads":1,"store_read_attempts":0,"member_visits":0,"store_write_attempts":0}`
	reports, failures := capture.snapshot()
	if string(decoded) != want || len(reports) != 1 || string(reports[0]) != want || len(failures) != 0 {
		t.Fatalf("network report = %s sink=%q failures=%v", decoded, reports, failures)
	}
}

func TestT421ExactReadHandlerRefusesInvalidAndDuplicateAdmission(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	for _, test := range []struct {
		name    string
		request func() *http.Request
	}{
		{"missing activation", func() *http.Request {
			request := exactT421ReadRequest(http.MethodGet, "/api/extraction-progress?repository=secret", 1)
			request.Header.Del(t421ExactReadActivationHeader)
			return request
		}},
		{"duplicate activation", func() *http.Request {
			request := exactT421ReadRequest(http.MethodGet, "/api/extraction-progress?repository=secret", 1)
			request.Header.Add(t421ExactReadActivationHeader, t421ExactReadsContract)
			return request
		}},
		{"noncanonical ordinal", func() *http.Request {
			request := exactT421ReadRequest(http.MethodGet, "/api/extraction-progress?repository=secret", 1)
			request.Header.Set(t421ExactReadOrdinalHeader, "01")
			return request
		}},
		{"unknown ordinal", func() *http.Request {
			return exactT421ReadRequest(http.MethodGet, "/api/extraction-progress?repository=secret", 2)
		}},
		{"over cap", func() *http.Request {
			return exactT421ReadRequest(http.MethodGet, "/api/extraction-progress?repository=secret", t421ExactReadMaxOrdinal+1)
		}},
		{"wrong method", func() *http.Request {
			return exactT421ReadRequest(http.MethodPost, "/api/extraction-progress?repository=secret", 1)
		}},
		{"unsupported path", func() *http.Request {
			return exactT421ReadRequest(http.MethodGet, "/api/repo-status?repository=secret", 1)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Uint64
			capture := &t421ExactReadTestCapture{}
			handler := authService.Require(t421ExactReadHandler(
				true,
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }),
				capture.report,
				capture.fail,
			))
			response := serveT421ExactReadRequest(t, handler, test.request())
			if response.status != http.StatusConflict || calls.Load() != 0 {
				t.Fatalf("response = %d, downstream calls = %d", response.status, calls.Load())
			}
			want := `{"schema":"t421-source-free-read-accounting-v1","request_ordinal":0,"status":"admission_refused","control_file_reads":0,"store_read_attempts":0,"member_visits":0,"store_write_attempts":0}`
			if response.report != want || strings.Contains(string(response.body)+response.report, "secret") {
				t.Fatalf("refusal leaked or drifted: body %q report %q", response.body, response.report)
			}
			reports, failures := capture.snapshot()
			if len(reports) != 1 || string(reports[0]) != want ||
				len(failures) != 1 || !errors.Is(failures[0], errT421ExactReadAdmission) {
				t.Fatalf("reports = %q, failures = %v", reports, failures)
			}
		})
	}

	t.Run("duplicate accepted ordinal", func(t *testing.T) {
		var calls atomic.Uint64
		capture := &t421ExactReadTestCapture{}
		handler := authService.Require(t421ExactReadHandler(
			true,
			http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				writer.WriteHeader(http.StatusNoContent)
			}),
			capture.report,
			capture.fail,
		))
		first := serveT421ExactReadRequest(
			t, handler, exactT421ReadRequest(http.MethodGet, "/api/extraction-progress?repository=secret", 1),
		)
		second := serveT421ExactReadRequest(
			t, handler, exactT421ReadRequest(http.MethodGet, "/api/extraction-progress?repository=secret", 1),
		)
		third := serveT421ExactReadRequest(
			t, handler, exactT421ReadRequest(http.MethodGet, "/api/extraction-progress?repository=secret", 1),
		)
		if first.status != http.StatusNoContent || second.status != http.StatusConflict ||
			third.status != http.StatusConflict || third.report != "" || calls.Load() != 1 {
			t.Fatalf("statuses = %d/%d/%d, calls = %d, third report = %q", first.status, second.status, third.status, calls.Load(), third.report)
		}
		reports, failures := capture.snapshot()
		if len(reports) != 2 || len(failures) != 1 ||
			!errors.Is(failures[0], errT421ExactReadAdmission) {
			t.Fatalf("duplicate reports = %d, failures = %v", len(reports), failures)
		}
	})
}

func TestT421ExactReadHandlerLatchesAccountingReportAndPanicFailures(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	for _, test := range []struct {
		name       string
		next       http.Handler
		sinkErr    error
		sinkPanic  bool
		wantStatus string
		wantCount  uint64
		wantErr    error
		wantPanic  bool
	}{
		{
			name: "ignored accounting refusal",
			next: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_ = readaccounting.Charge(request.Context(), readaccounting.ControlFileRead, 67)
				writer.WriteHeader(http.StatusNoContent)
			}),
			wantStatus: "accounting_refused", wantCount: 67,
			wantErr: errT421ExactReadAccounting,
		},
		{
			name: "report refusal",
			next: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			}),
			sinkErr: errors.New("bounded sink refusal"), wantStatus: "complete",
			wantErr: errT421ExactReadReport,
		},
		{
			name: "report panic",
			next: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			}),
			sinkPanic: true, wantStatus: "complete",
			wantErr: errT421ExactReadReport,
		},
		{
			name: "handler panic",
			next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic("private panic cause")
			}),
			wantStatus: "handler_incomplete", wantErr: errT421ExactReadIncomplete,
			wantPanic: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &t421ExactReadTestCapture{
				sinkErr: test.sinkErr, sinkPanic: test.sinkPanic,
			}
			handler := authService.Require(t421ExactReadHandler(
				true, test.next, capture.report, capture.fail,
			))
			request := exactT421ReadRequest(
				http.MethodGet, "/api/extraction-progress?repository=private-repository", 1,
			)
			recorder := httptest.NewRecorder()
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				handler.ServeHTTP(recorder, request)
			}()
			if (recovered != nil) != test.wantPanic {
				t.Fatalf("recovered = %v; want panic %t", recovered, test.wantPanic)
			}
			reports, failures := capture.snapshot()
			if len(reports) != 1 || len(failures) != 1 || !errors.Is(failures[0], test.wantErr) {
				t.Fatalf("reports = %q, failures = %v", reports, failures)
			}
			report := string(reports[0])
			if !strings.Contains(report, `"status":"`+test.wantStatus+`"`) ||
				!strings.Contains(report, `"control_file_reads":`+strconv.FormatUint(test.wantCount, 10)) ||
				strings.Contains(report, "private-repository") || strings.Contains(report, "private panic cause") {
				t.Fatalf("failure report = %s", report)
			}
		})
	}
}

func TestT421ExactReadHandlerRefusesOverlappingRequest(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	capture := &t421ExactReadTestCapture{}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := authService.Require(t421ExactReadHandler(
		true,
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			close(entered)
			<-release
			writer.WriteHeader(http.StatusNoContent)
		}),
		capture.report,
		capture.fail,
	))
	first := httptest.NewRecorder()
	go func() {
		defer close(done)
		handler.ServeHTTP(first, exactT421ReadRequest(
			http.MethodGet, "/api/extraction-progress?repository=private-repository", 1,
		))
	}()
	<-entered
	second := serveT421ExactReadRequest(t, handler, exactT421ReadRequest(
		http.MethodGet, "/api/extraction-progress?repository=private-repository", 2,
	))
	close(release)
	<-done
	reports, failures := capture.snapshot()
	if first.Code != http.StatusNoContent || second.status != http.StatusConflict ||
		len(reports) != 2 || len(failures) != 1 || !errors.Is(failures[0], errT421ExactReadAdmission) {
		t.Fatalf("overlap responses=%d/%d reports=%q failures=%v", first.Code, second.status, reports, failures)
	}
}

type t421ExactReadEventWriter struct {
	*httptest.ResponseRecorder
	events *[]string
}

func (writer *t421ExactReadEventWriter) Write(payload []byte) (int, error) {
	*writer.events = append(*writer.events, "write")
	return writer.ResponseRecorder.Write(payload)
}

type t421ExactReadPartialWriter struct {
	header http.Header
	status int
}

func (writer *t421ExactReadPartialWriter) Header() http.Header {
	return writer.header
}

func (writer *t421ExactReadPartialWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
}

func (writer *t421ExactReadPartialWriter) Write(payload []byte) (int, error) {
	writer.WriteHeader(http.StatusOK)
	if len(payload) == 0 {
		return 0, nil
	}
	return len(payload) - 1, io.ErrShortWrite
}

func TestT421ExactFinalAuthorityRouteCommitsAfterBodyBeforeReport(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	capture := &t421ExactReadTestCapture{}
	events := make([]string, 0, 4)
	committed := false
	var downstreamCalls atomic.Uint64
	final := t421ExactFinalAuthorityRead{
		Limits: readaccounting.Counts{
			ControlFileReads: 2, StoreReadAttempts: 3, MemberVisits: 4,
		},
		Read: func(ctx context.Context) ([]byte, func() error, error) {
			events = append(events, "read")
			for _, charge := range []struct {
				kind  readaccounting.Kind
				count uint64
			}{
				{readaccounting.ControlFileRead, 2},
				{readaccounting.StoreReadAttempt, 3},
				{readaccounting.MemberVisit, 4},
			} {
				if err := readaccounting.Charge(ctx, charge.kind, charge.count); err != nil {
					t.Fatal(err)
				}
			}
			return []byte(`{"schema":"t421-final-authority-v1"}`), func() error {
				events = append(events, "commit")
				committed = true
				return nil
			}, nil
		},
	}
	handler := authService.Require(t421ExactReadHandler(
		true,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			downstreamCalls.Add(1)
		}),
		func(payload []byte) error {
			events = append(events, "report")
			return capture.report(payload)
		},
		capture.fail,
		final,
	))
	recorder := httptest.NewRecorder()
	writer := &t421ExactReadEventWriter{ResponseRecorder: recorder, events: &events}
	handler.ServeHTTP(writer, exactT421ReadRequest(
		http.MethodGet, t421ExactFinalAuthorityPath, 1,
	))

	wantReport := `{"schema":"t421-source-free-read-accounting-v1","request_ordinal":1,"status":"complete","control_file_reads":2,"store_read_attempts":3,"member_visits":4,"store_write_attempts":0}`
	reports, failures := capture.snapshot()
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"schema":"t421-final-authority-v1"}` ||
		recorder.Header().Get("Content-Type") != "application/json" || !committed ||
		downstreamCalls.Load() != 0 || len(reports) != 1 || string(reports[0]) != wantReport ||
		len(failures) != 0 || strings.Join(events, ",") != "read,write,commit,report" {
		t.Fatalf("response=%d %q content-type=%q committed=%t downstream=%d reports=%q failures=%v events=%v",
			recorder.Code, recorder.Body.String(), recorder.Header().Get("Content-Type"),
			committed, downstreamCalls.Load(), reports, failures, events)
	}
}

func TestT421ExactFinalAuthorityRouteRefusesBeforeCommit(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	for _, test := range []struct {
		name       string
		read       func(context.Context) ([]byte, func() error, error)
		sinkErr    error
		wantStatus string
		wantErr    error
		wantHTTP   int
		wantCommit bool
	}{
		{
			name: "callback error",
			read: func(context.Context) ([]byte, func() error, error) {
				return nil, nil, errors.New("private callback cause")
			},
			wantStatus: "final_authority_refused", wantErr: errT421ExactReadAuthority,
			wantHTTP: http.StatusConflict,
		},
		{
			name: "invalid body",
			read: func(context.Context) ([]byte, func() error, error) {
				return []byte(`private body`), nil, nil
			},
			wantStatus: "final_authority_refused", wantErr: errT421ExactReadAuthority,
			wantHTTP: http.StatusConflict,
		},
		{
			name: "accounting refusal",
			read: func(ctx context.Context) ([]byte, func() error, error) {
				_ = readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1)
				return []byte(`{}`), func() error {
					t.Fatal("commit ran after accounting refusal")
					return nil
				}, nil
			},
			wantStatus: "accounting_refused", wantErr: errT421ExactReadAccounting,
			wantHTTP: http.StatusOK,
		},
		{
			name: "commit error",
			read: func(context.Context) ([]byte, func() error, error) {
				return []byte(`{}`), func() error {
					return errors.New("private commit cause")
				}, nil
			},
			wantStatus: "commit_refused", wantErr: errT421ExactReadCommit,
			wantHTTP: http.StatusOK,
		},
		{
			name: "commit panic",
			read: func(context.Context) ([]byte, func() error, error) {
				return []byte(`{}`), func() error {
					panic("private commit panic")
				}, nil
			},
			wantStatus: "commit_refused", wantErr: errT421ExactReadCommit,
			wantHTTP: http.StatusOK,
		},
		{
			name: "sink refusal after commit",
			read: func(context.Context) ([]byte, func() error, error) {
				return []byte(`{}`), nil, nil
			},
			sinkErr: errors.New("private sink cause"), wantStatus: "complete",
			wantErr: errT421ExactReadReport, wantHTTP: http.StatusOK, wantCommit: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &t421ExactReadTestCapture{sinkErr: test.sinkErr}
			committed := false
			read := test.read
			if test.name == "sink refusal after commit" {
				read = func(context.Context) ([]byte, func() error, error) {
					return []byte(`{}`), func() error {
						committed = true
						return nil
					}, nil
				}
			}
			handler := authService.Require(t421ExactReadHandler(
				true,
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Fatal("final-authority request reached downstream handler")
				}),
				capture.report,
				capture.fail,
				t421ExactFinalAuthorityRead{Read: read},
			))
			response := serveT421ExactReadRequest(t, handler, exactT421ReadRequest(
				http.MethodGet, t421ExactFinalAuthorityPath, 1,
			))
			reports, failures := capture.snapshot()
			if response.status != test.wantHTTP || len(reports) != 1 ||
				!strings.Contains(string(reports[0]), `"status":"`+test.wantStatus+`"`) ||
				len(failures) != 1 || !errors.Is(failures[0], test.wantErr) ||
				committed != test.wantCommit ||
				strings.Contains(string(response.body)+string(reports[0]), "private") {
				t.Fatalf("response=%d %q reports=%q failures=%v committed=%t",
					response.status, response.body, reports, failures, committed)
			}
		})
	}
}

func TestT421ExactFinalAuthorityRouteRejectsPartialBody(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	capture := &t421ExactReadTestCapture{}
	committed := false
	handler := authService.Require(t421ExactReadHandler(
		true,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("final-authority request reached downstream handler")
		}),
		capture.report,
		capture.fail,
		t421ExactFinalAuthorityRead{Read: func(context.Context) ([]byte, func() error, error) {
			return []byte(`{}`), func() error {
				committed = true
				return nil
			}, nil
		}},
	))
	writer := &t421ExactReadPartialWriter{header: make(http.Header)}
	handler.ServeHTTP(writer, exactT421ReadRequest(
		http.MethodGet, t421ExactFinalAuthorityPath, 1,
	))
	reports, failures := capture.snapshot()
	if writer.status != http.StatusOK || committed || len(reports) != 1 ||
		!strings.Contains(string(reports[0]), `"status":"response_write_refused"`) ||
		len(failures) != 1 || !errors.Is(failures[0], errT421ExactReadResponse) {
		t.Fatalf("status=%d committed=%t reports=%q failures=%v",
			writer.status, committed, reports, failures)
	}
}

func TestT421ExactFinalAuthorityRouteIsFixedAndOptional(t *testing.T) {
	valid := t421ExactFinalAuthorityRead{
		Limits: readaccounting.Counts{ControlFileReads: 1},
		Read: func(context.Context) ([]byte, func() error, error) {
			return []byte(`{}`), nil, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, t421ExactFinalAuthorityPath, nil)
	if limits, ok := t421ExactReadLimits(request, valid); !ok || limits != valid.Limits {
		t.Fatalf("final-authority limits = %+v, %t", limits, ok)
	}
	for _, invalid := range []struct {
		name    string
		request *http.Request
		read    []t421ExactFinalAuthorityRead
	}{
		{"absent", httptest.NewRequest(http.MethodGet, t421ExactFinalAuthorityPath, nil), nil},
		{"zero value", httptest.NewRequest(http.MethodGet, t421ExactFinalAuthorityPath, nil), []t421ExactFinalAuthorityRead{{}}},
		{"unbounded", httptest.NewRequest(http.MethodGet, t421ExactFinalAuthorityPath, nil), []t421ExactFinalAuthorityRead{{
			Limits: readaccounting.Counts{ControlFileReads: math.MaxUint64}, Read: valid.Read,
		}}},
		{"query", httptest.NewRequest(http.MethodGet, t421ExactFinalAuthorityPath+"?repository=private", nil), []t421ExactFinalAuthorityRead{valid}},
		{"empty query", httptest.NewRequest(http.MethodGet, t421ExactFinalAuthorityPath+"?", nil), []t421ExactFinalAuthorityRead{valid}},
		{"method", httptest.NewRequest(http.MethodPost, t421ExactFinalAuthorityPath, nil), []t421ExactFinalAuthorityRead{valid}},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if _, ok := t421ExactReadLimits(invalid.request, invalid.read...); ok {
				t.Fatal("invalid final-authority target was admitted")
			}
		})
	}
}

func TestServerTerminalErrorRetainsShutdownAndExactFailures(t *testing.T) {
	if err := serverTerminalError(
		http.ErrServerClosed, context.DeadlineExceeded, nil, nil,
	); err != nil {
		t.Fatalf("ordinary shutdown behavior changed: %v", err)
	}
	exactReports := make(chan struct{})
	close(exactReports)
	exactReads := make(chan error, 1)
	exactReads <- errT421ExactReadAccounting
	err := serverTerminalError(
		http.ErrServerClosed, context.DeadlineExceeded, exactReports, exactReads,
	)
	if !errors.Is(err, context.DeadlineExceeded) ||
		!errors.Is(err, errT421ExactReadAccounting) ||
		!strings.Contains(err.Error(), "T40.13 exact reporting failed") {
		t.Fatalf("terminal server error = %v", err)
	}
}

type t421ExactReadTestResponse struct {
	status int
	body   []byte
	report string
}

func serveT421ExactReadRequest(
	t *testing.T,
	handler http.Handler,
	request *http.Request,
) t421ExactReadTestResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	trailer := response.Trailer.Get(t421ExactReadTrailer)
	if trailer == "" {
		return t421ExactReadTestResponse{status: response.StatusCode, body: body}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(trailer)
	if err != nil {
		t.Fatalf("decode exact-read trailer: %v", err)
	}
	return t421ExactReadTestResponse{
		status: response.StatusCode, body: body, report: string(decoded),
	}
}

func exactT421ReadRequest(method, target string, ordinal uint64) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
	request.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
	request.Header.Set(t421ExactReadOrdinalHeader, strconv.FormatUint(ordinal, 10))
	return request
}

func exactT421ReportForOrdinal(ordinal uint64) string {
	return `{"schema":"t421-source-free-read-accounting-v1","request_ordinal":` +
		strconv.FormatUint(ordinal, 10) +
		`,"status":"complete","control_file_reads":66,"store_read_attempts":130,"member_visits":0,"store_write_attempts":0}`
}

func newT421ExactReadAuthService(t *testing.T) *auth.Service {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	st, err := store.OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = st.Close(context.Background())
	})
	insecure := false
	service, err := auth.New(ctx, auth.Options{
		Store: st,
		Config: config.Auth{
			APIKey:       t421ExactReadTestCredential,
			CookieSecure: &insecure,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
