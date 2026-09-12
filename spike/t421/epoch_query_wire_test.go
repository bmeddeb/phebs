package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/search"
)

func TestEpochQueryWireRequests(t *testing.T) {
	pages := uint64(0)
	for _, query := range correctedQueryCases() {
		t.Run(query.Name, func(t *testing.T) {
			for _, transport := range []string{"http", "mcp"} {
				cursor := ""
				if query.PageSize > 0 {
					cursor = "opaque+/cursor=="
				}
				method, path, body, err := epochQueryRequest(query, transport, "example.com/actual/repository", cursor, "42")
				if err != nil || strings.Contains(path, "$") || len(body) > epochQueryRequestBytes {
					t.Fatal(transport, path, err)
				}
				if transport == "http" {
					parsed, err := url.Parse(path)
					if err != nil || method != http.MethodGet || body != nil || parsed.Query().Get("cursor") != cursor {
						t.Fatal("HTTP request", method, path, string(body), err)
					}
					continue
				}
				var request struct {
					JSONRPC string `json:"jsonrpc"`
					ID      string `json:"id"`
					Method  string `json:"method"`
					Params  struct {
						Name      string                     `json:"name"`
						Arguments map[string]json.RawMessage `json:"arguments"`
					} `json:"params"`
				}
				if json.Unmarshal(body, &request) != nil || method != http.MethodPost || path != "/api/mcp" || request.JSONRPC != "2.0" ||
					request.ID != "42" || request.Method != "tools/call" || request.Params.Name != query.MCPTool {
					t.Fatal("MCP request", method, path, string(body))
				}
				for _, key := range []string{"max_matches", "context_lines", "page_size"} {
					if raw, ok := request.Params.Arguments[key]; ok {
						var number int
						if json.Unmarshal(raw, &number) != nil {
							t.Fatal("numeric argument encoded as string", key)
						}
					}
				}
				for _, parameter := range query.Parameters {
					if parameter.Name == "query" {
						var actual string
						if json.Unmarshal(request.Params.Arguments["query"], &actual) != nil || actual != parameter.Value {
							t.Fatal("fixed query or regexp anchor changed", actual)
						}
					}
				}
				if raw, ok := request.Params.Arguments["repositories"]; ok && string(raw) != `["example.com/actual/repository"]` {
					t.Fatal("repository list", string(raw))
				}
				if raw, ok := request.Params.Arguments["service_key"]; ok {
					var key string
					_ = json.Unmarshal(raw, &key)
					if !strings.HasPrefix(key, "svc.load-") {
						t.Fatal("actual authored service key", key)
					}
				}
			}
		})
		count := uint64(1)
		if query.PageSize > 0 {
			count = (query.ExpectedRecords + query.PageSize - 1) / query.PageSize
		}
		pages += count
	}
	if pages != 19 {
		t.Fatal("fixed transport page inventory", pages)
	}
}

func TestEpochQueryWireRequestRefusals(t *testing.T) {
	base := correctedQueryCases()[0]
	for _, test := range []struct {
		name, transport, repository, cursor, id string
		change                                  func(*QueryCase)
	}{
		{name: "unknown_transport", transport: "setup", repository: "example.com/repo", id: "1"},
		{name: "empty_repository", transport: "http", id: "1"},
		{name: "hidden_as_authorized", transport: "http", repository: epochQueryHiddenRepository, id: "1"},
		{name: "unpaged_cursor", transport: "http", repository: "example.com/repo", cursor: "next", id: "1"},
		{name: "zero_id", transport: "mcp", repository: "example.com/repo", id: "0"},
		{name: "noncanonical_id", transport: "mcp", repository: "example.com/repo", id: "01"},
		{name: "overflow_id", transport: "mcp", repository: "example.com/repo", id: "18446744073709551616"},
		{name: "initialize", transport: "mcp", repository: "example.com/repo", id: "1", change: func(q *QueryCase) { q.MCPTool = "initialize" }},
		{name: "tools_list", transport: "mcp", repository: "example.com/repo", id: "1", change: func(q *QueryCase) { q.MCPTool = "tools/list" }},
		{name: "changed_page", transport: "http", repository: "example.com/repo", id: "1", change: func(q *QueryCase) { q.PageSize = 2 }},
		{name: "changed_path", transport: "http", repository: "example.com/repo", id: "1", change: func(q *QueryCase) { q.HTTP.Path = "https://example.com/" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := base
			if test.change != nil {
				test.change(&query)
			}
			method, path, body, err := epochQueryRequest(query, test.transport, test.repository, test.cursor, test.id)
			if !errors.Is(err, errEpochInspection) || method != "" || path != "" || body != nil {
				t.Fatal("request refusal", method, path, err)
			}
		})
	}
	query := correctedQueryCases()[5]
	if _, _, _, err := epochQueryRequest(query, "mcp", "example.com/repo", strings.Repeat("x", epochQueryCursorBytes+1), "1"); err == nil {
		t.Fatal("oversize cursor accepted")
	}
}

// Use the pinned SDK's actual content/result and JSON-RPC encoders, not just
// a parallel handwritten wire fixture. Payloads remain supplied parser data.
func epochQuerySDKMessage(t *testing.T, id string, structured []byte, refusal string) []byte {
	t.Helper()
	result := mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(structured)}}, StructuredContent: json.RawMessage(structured)}
	if refusal != "" {
		result = mcpsdk.CallToolResult{}
		result.SetError(errors.New(refusal))
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	requestID, err := jsonrpc.MakeID(id)
	if err != nil {
		t.Fatal(err)
	}
	message, err := jsonrpc.EncodeMessage(&jsonrpc.Response{ID: requestID, Result: raw})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestEpochQueryWireMCP(t *testing.T) {
	query := correctedQueryCases()[0]
	payload, _ := json.Marshal(struct {
		Value string `json:"value"`
	}{Value: "quotes\" slash\\ html<>& unicode\u2028"})
	message := epochQuerySDKMessage(t, "42", payload, "")
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		t.Run(contentType, func(t *testing.T) {
			raw := message
			if contentType == "text/event-stream" {
				raw = []byte(epochQuerySSEPrefix + string(message) + epochQuerySSESuffix)
			}
			original := bytes.Clone(raw)
			got, code, err := decodeEpochQueryMCP(query, contentType, "42", raw)
			if err != nil || code != "ok" || !bytes.Equal(got, payload) {
				t.Fatal(code, string(got), err)
			}
			got[0] = 'x'
			if !bytes.Equal(raw, original) {
				t.Fatal("returned alias changed supplied data")
			}
		})
	}
	denied := correctedQueryCases()[1]
	got, code, err := decodeEpochQueryMCP(denied, "application/json", "42", epochQuerySDKMessage(t, "42", nil, epochQueryNotFound))
	if err != nil || got != nil || code != "unknown_repository" {
		t.Fatal("fixed denial", code, err)
	}
}

// Actual pinned stateless SDK transport, but a supplied typed tool result:
// this proves no-initialize framing/serialization, not product query semantics.
func TestEpochQueryWireStatelessSDKTransport(t *testing.T) {
	type input struct {
		Query        string `json:"query"`
		Scope        string `json:"scope,omitempty"`
		Repository   string `json:"repository,omitempty"`
		ServiceKey   string `json:"service_key,omitempty"`
		MaxMatches   int    `json:"max_matches,omitempty"`
		ContextLines int    `json:"context_lines,omitempty"`
	}
	type output struct {
		Value string `json:"value"`
	}
	for _, refused := range []bool{false, true} {
		name := "success"
		if refused {
			name = "tool_error"
		}
		t.Run(name, func(t *testing.T) {
			query := correctedQueryCases()[0]
			if refused {
				query = correctedQueryCases()[1]
			}
			want := output{Value: "native SDK quotes\" slash\\ html<>& unicode\u2028"}
			var toolCalls, requests atomic.Int32
			server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "wire-test", Version: "1"}, nil)
			mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "search_code"}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in input) (*mcpsdk.CallToolResult, output, error) {
				toolCalls.Add(1)
				if in.Query != "T401Fixture" || in.MaxMatches != 1 || in.ContextLines != 0 ||
					!refused && in.Scope != "all_code" || refused && (in.Scope != "service" || in.Repository != epochQueryHiddenRepository || in.ServiceKey != "svc.load-00000") {
					t.Error("fixed typed arguments changed", in)
				}
				if refused {
					return nil, output{}, errors.New(epochQueryNotFound)
				}
				return nil, want, nil
			})
			// Exactly main's native options: no JSONResponse or EventStore and
			// no retained initialization/session request.
			handler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, &mcpsdk.StreamableHTTPOptions{Stateless: true})
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/mcp" {
					t.Error("unexpected transport request", r.Method, r.URL.Path)
				}
				handler.ServeHTTP(w, r)
			}))
			defer host.Close()
			method, path, payload, err := epochQueryRequest(query, "mcp", "example.com/actual/repository", "", "42")
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequestWithContext(t.Context(), method, host.URL+path, bytes.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			response, err := host.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			maximum, err := epochQueryBodyMaximum(query, "mcp", "", "42")
			if err != nil {
				_ = response.Body.Close()
				t.Fatal(err)
			}
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, maximum+1))
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" || requests.Load() != 1 || toolCalls.Load() != 1 {
				t.Fatal("native SDK exchange", response.StatusCode, response.Header, requests.Load(), toolCalls.Load(), readErr, closeErr)
			}
			actual, code, err := decodeEpochQueryMCP(query, response.Header.Get("Content-Type"), "42", raw)
			if err != nil || code != query.ExpectedMCPCode {
				t.Fatal("native SDK response refused", code, string(raw), err)
			}
			if refused {
				if actual != nil {
					t.Fatal("tool error invented structured output")
				}
			} else if expected, err := json.Marshal(want); err != nil || !bytes.Equal(actual, expected) {
				t.Fatal("native typed output changed", string(actual), err)
			}
		})
	}
}

func TestEpochQueryWireMCPRefusals(t *testing.T) {
	query := correctedQueryCases()[0]
	message := string(epochQuerySDKMessage(t, "42", []byte(`{"value":1}`), ""))
	for _, test := range []struct{ name, contentType, raw, id string }{
		{"wrong_id", "application/json", message, "43"},
		{"numeric_id", "application/json", strings.Replace(message, `"id":"42"`, `"id":42`, 1), "42"},
		{"duplicate_id", "application/json", strings.Replace(message, `"id":"42"`, `"id":"42","id":"42"`, 1), "42"},
		{"notification", "application/json", `{"jsonrpc":"2.0","method":"notifications/message","params":{}}`, "42"},
		{"rpc_error", "application/json", `{"jsonrpc":"2.0","id":"42","error":{"code":-1,"message":"failed"}}`, "42"},
		{"unknown_envelope", "application/json", strings.Replace(message, `"jsonrpc":"2.0"`, `"jsonrpc":"2.0","other":true`, 1), "42"},
		{"unknown_result", "application/json", strings.Replace(message, `"content":`, `"other":true,"content":`, 1), "42"},
		{"extra_text_field", "application/json", strings.Replace(message, `"type":"text"`, `"type":"text","annotations":{}`, 1), "42"},
		{"parity", "application/json", strings.Replace(message, `"structuredContent":{"value":1}`, `"structuredContent":{"value":2}`, 1), "42"},
		{"two_documents", "application/json", message + message, "42"},
		{"batch", "application/json", "[" + message + "]", "42"},
		{"sse_notification", "text/event-stream", epochQuerySSEPrefix + message + epochQuerySSESuffix + "event: message\ndata: {}\n\n", "42"},
		{"sse_comment", "text/event-stream", ": keepalive\n" + epochQuerySSEPrefix + message + epochQuerySSESuffix, "42"},
		{"sse_event_id", "text/event-stream", "id: injected\n" + epochQuerySSEPrefix + message + epochQuerySSESuffix, "42"},
		{"sse_missing_tail", "text/event-stream", epochQuerySSEPrefix + message, "42"},
		{"wrong_content_type", "text/plain", message, "42"},
		{"unexpected_error", "application/json", string(epochQuerySDKMessage(t, "42", nil, epochQueryNotFound)), "42"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, code, err := decodeEpochQueryMCP(query, test.contentType, test.id, []byte(test.raw))
			if !errors.Is(err, errEpochInspection) || got != nil || code != "" {
				t.Fatal("unrefused wire", string(got), code, err)
			}
		})
	}
	denied := correctedQueryCases()[1]
	for _, text := range []string{"not found", epochQueryNotFound + ": repository-secret"} {
		if _, _, err := decodeEpochQueryMCP(denied, "application/json", "42", epochQuerySDKMessage(t, "42", nil, text)); err == nil {
			t.Fatal("changed denial text accepted")
		}
	}
}

func TestEpochQueryWireBodyLimits(t *testing.T) {
	for _, query := range correctedQueryCases() {
		t.Run(query.Name, func(t *testing.T) {
			payload, err := epochQueryPayloadMaximum(query)
			if err != nil || payload <= 0 {
				t.Fatal(payload, err)
			}
			httpMaximum, err := epochQueryBodyMaximum(query, "http", "127.0.0.1:65535", "")
			if err != nil || httpMaximum <= payload {
				t.Fatal("Huma overhead missing", httpMaximum, payload, err)
			}
			mcpMaximum, err := epochQueryBodyMaximum(query, "mcp", "", strconv.FormatUint(math.MaxUint64, 10))
			if err != nil || mcpMaximum <= 3*payload {
				t.Fatal("MCP duplication/framing missing", mcpMaximum, payload, err)
			}
			if _, _, err := decodeEpochQueryMCP(query, "application/json", "1", bytes.Repeat([]byte{'x'}, int(mcpMaximum)+1)); err == nil {
				t.Fatal("oversize wire accepted")
			}
		})
	}
	query := correctedQueryCases()[5]
	for _, listen := range []string{"localhost:1", "127.0.0.1:0", "127.0.0.1:01", "127.0.0.1:65536", "[::1]:1"} {
		if _, err := epochQueryBodyMaximum(query, "http", listen, ""); err == nil {
			t.Fatal("unbound listener", listen)
		}
	}
	// A full admitted shared relationship payload can legitimately exceed the
	// former fixture's unrelated 4-MiB SSE scanner setting after MCP wrapping.
	payload := []byte(`{"value":"` + strings.Repeat("x", epochQueryRelationshipBytes-len(`{"value":""}`)) + `"}`)
	message := epochQuerySDKMessage(t, "42", payload, "")
	if len(message) <= 4<<20 {
		t.Fatal("test did not cross old fixture limit", len(message))
	}
	if got, _, err := decodeEpochQueryMCP(query, "application/json", "42", message); err != nil || !bytes.Equal(got, payload) {
		t.Fatal("valid payload boundary refused", err)
	}
	payload = append(payload[:len(payload)-2], []byte("x\"}")...)
	if _, _, err := decodeEpochQueryMCP(query, "application/json", "42", epochQuerySDKMessage(t, "42", payload, "")); err == nil {
		t.Fatal("payload cap+1 accepted")
	}
	// Source-derived search envelope accommodates an actual shaped one-file
	// response without interpreting a one-file page limit as a chunk-byte cap.
	searchPayload, _ := json.Marshal(search.Result{Files: []search.FileResult{{Repo: "example.com/repo", Path: "structural/fixture.go", Language: "Go", Chunks: []search.Chunk{{Content: strings.Repeat("x", 4608), StartLine: 1, Ranges: []search.Range{{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 12}}}}}}, Stats: search.Stats{MatchCount: 1, FileCount: 1}})
	maximum, err := epochQueryPayloadMaximum(correctedQueryCases()[0])
	if err != nil || int64(len(searchPayload)) > maximum {
		t.Fatal("frozen search shape did not fit", maximum, err)
	}
}
