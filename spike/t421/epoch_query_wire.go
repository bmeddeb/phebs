package t421

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicequery"
)

const (
	// Existing native exact-request and shared product response limits, not
	// new query/output allowances. HTTP and MCP add their own framing below.
	epochQueryRequestBytes      = 64 << 10
	epochQueryDetailBytes       = 1 << 20
	epochQueryRelationshipBytes = 2 << 20
	epochQueryCursorBytes       = 16 << 10
	epochQuerySSEPrefix         = "event: message\ndata: "
	epochQuerySSESuffix         = "\n\n"
	epochQueryHiddenRepository  = "github.com/t421/hidden"
	// The fixed absent repository takes GetRepo's wrapped not-found path;
	// searchService adds its repository wrapper, SearchScoped adds the scope
	// marker, and the real MCP tool preserves the complete error chain.
	epochQueryNotFound = `search scope not found: service search: repository: repo "github.com/t421/hidden": not found`
)

func epochQueryKnown(query QueryCase) bool {
	for _, expected := range correctedQueryCases() {
		if expected.Name == query.Name {
			return reflect.DeepEqual(query, expected)
		}
	}
	return false
}

func epochQueryID(id string) bool {
	value, err := strconv.ParseUint(id, 10, 64)
	return err == nil && value != 0 && strconv.FormatUint(value, 10) == id
}

// Only the frozen target's keys are expanded: t411's author emits
// svc.load-%05d, and transformCatalog changes paths, not service identities.
func epochQueryExpand(value, repository string) string {
	value = strings.ReplaceAll(value, "$authorized_repository", repository)
	value = strings.ReplaceAll(value, "$hidden_repository", epochQueryHiddenRepository)
	for _, index := range []int{0, 1, 2, 100} {
		placeholder := "$accepted_service_" + fmtQueryIndex(index)
		value = strings.ReplaceAll(value, placeholder, serviceKey(index))
	}
	return value
}

func fmtQueryIndex(index int) string {
	value := strconv.Itoa(index)
	return strings.Repeat("0", 5-len(value)) + value
}

// No setup traffic or caller-selected method/tool is admitted. The selected
// server's stateless MCP transport supplies its own initialized session state.
func epochQueryRequest(query QueryCase, transport, repository, cursor, id string) (method, path string, payload []byte, err error) {
	if !epochQueryKnown(query) || reponame.Validate(repository) != nil || repository == epochQueryHiddenRepository ||
		len(cursor) > epochQueryCursorBytes || cursor != "" && query.PageSize == 0 ||
		transport != "http" && transport != "mcp" {
		return "", "", nil, errEpochInspection
	}
	if transport == "http" {
		path = epochQueryExpand(query.HTTP.Path, url.QueryEscape(repository))
		// Service keys and the fixed hidden repository must also be URL-escaped.
		path = strings.ReplaceAll(path, epochQueryHiddenRepository, url.QueryEscape(epochQueryHiddenRepository))
		parsed, parseErr := url.Parse(path)
		if parseErr != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" || strings.Contains(path, "$") {
			return "", "", nil, errEpochInspection
		}
		if cursor != "" {
			values := parsed.Query()
			values.Set("cursor", cursor)
			parsed.RawQuery = values.Encode()
		}
		return http.MethodGet, parsed.String(), nil, nil
	}
	if !epochQueryID(id) {
		return "", "", nil, errEpochInspection
	}
	arguments := make(map[string]any, len(query.Parameters)+1)
	for _, parameter := range query.Parameters {
		value := epochQueryExpand(parameter.Value, repository)
		// The closed search expressions retain their literal regexp '$'
		// anchors; exact QueryCase validation already excludes new placeholders.
		switch parameter.Name {
		case "max_matches", "context_lines", "page_size":
			number, parseErr := strconv.Atoi(value)
			if parseErr != nil || strconv.Itoa(number) != value {
				return "", "", nil, errEpochInspection
			}
			arguments[parameter.Name] = number
		case "repositories":
			// The closed case has exactly the one actual authorized repository;
			// construct its JSON value instead of interpolating inside JSON text.
			arguments[parameter.Name] = []string{repository}
		default:
			arguments[parameter.Name] = value
		}
	}
	if cursor != "" {
		arguments["cursor"] = cursor
	}
	payload, err = json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}{Name: query.MCPTool, Arguments: arguments}})
	if err != nil || len(payload) > epochQueryRequestBytes {
		return "", "", nil, errEpochInspection
	}
	return http.MethodPost, "/api/mcp", payload, nil
}

// This is a maximum-sized serialization model, never returned query evidence.
// The accepted fixed searches return at most one file/chunk/match, not merely
// max_matches=1 files with arbitrarily many chunks. The structural author pins
// 4,608-byte inputs and one marker; the shared-file generator supplies its exact
// byte length. Generic search has no equivalent encoded-response guarantee.
func epochQuerySearchMaximum(query QueryCase) (int64, error) {
	profile, err := frozenStructuralProfile()
	if err != nil {
		return 0, errEpochInspection
	}
	contentBytes := int(profile.Shape.GoFileBytes)
	if query.Name == "shared_placement_service_scope" {
		_, content, _, err := combinedOverlayFile("shared/group-0000/library.go", nil)
		if err != nil {
			return 0, errEpochInspection
		}
		contentBytes = len(content)
	}
	digest := "sha256:" + strings.Repeat("f", 64)
	repository := strings.Repeat("\x00", reponame.MaxBytes)
	result := search.Result{Files: []search.FileResult{}, Stats: search.Stats{DurationMS: math.MaxInt64},
		Scope: &search.ScopeReceipt{Schema: search.ScopeReceiptSchema, Kind: search.ScopeAllCode,
			MembershipPolicy: "visible-indexed-repositories-v1", ExpressionDigest: digest,
			ResultSetDigest: digest, Digest: digest, Revisions: []search.ScopeRevision{}}}
	if query.ExpectedRecords == 1 {
		// The frozen marker's .go/.txt and shared .go inputs classify as Go or
		// Text. All other string fields use their admitted maximum byte lengths.
		result.Files = []search.FileResult{{Repo: repository, Path: strings.Repeat("\x00", repopath.MaxBytes),
			Ref: strings.Repeat("f", 40), Language: "Text", Chunks: []search.Chunk{{
				Content: strings.Repeat("\x00", contentBytes), StartLine: math.MaxInt,
				Ranges: []search.Range{{StartLine: math.MaxInt, StartCol: math.MaxInt, EndLine: math.MaxInt, EndCol: math.MaxInt}},
			}}}}
		result.Stats.MatchCount, result.Stats.FileCount = 1, 1
		result.Scope.ResultFiles, result.Scope.ResultMatches = 1, 1
		result.Scope.Revisions = []search.ScopeRevision{{Repository: repository, Commit: strings.Repeat("f", 40)}}
	}
	var authorityBytes int64
	if query.Surface == "service_search" {
		result.Scope.Kind, result.Scope.Repository, result.Scope.ServiceStatus = search.ScopeService, repository, "current"
		result.Scope.ServiceKey = strings.Repeat("\x00", servicecatalog.MaxServiceKeyBytes)
		result.Scope.MembershipPolicy = "accepted-roles-union-shared-included-unowned-excluded-v1"
		authorityBytes = int64(len(`,"service_authority":`)) + servicequery.MaxAuthorityBytes
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return 0, errEpochInspection
	}
	return int64(len(raw)) + authorityBytes, nil
}

func epochQueryPayloadMaximum(query QueryCase) (int64, error) {
	if !epochQueryKnown(query) {
		return 0, errEpochInspection
	}
	switch query.Surface {
	case "service_detail":
		return epochQueryDetailBytes, nil
	case "service_relationships":
		return epochQueryRelationshipBytes, nil
	case "all_code_search", "service_search":
		return epochQuerySearchMaximum(query)
	default:
		return 0, errEpochInspection
	}
}

type epochQueryMCPText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type epochQueryMCPResult struct {
	Content    []epochQueryMCPText `json:"content"`
	Structured json.RawMessage     `json:"structuredContent,omitempty"`
	IsError    bool                `json:"isError,omitempty"`
}

type epochQueryMCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result"`
}

func epochQueryBodyMaximum(query QueryCase, transport, listen, id string) (int64, error) {
	payload, err := epochQueryPayloadMaximum(query)
	if err != nil {
		return 0, err
	}
	if transport == "mcp" {
		if !epochQueryID(id) {
			return 0, errEpochInspection
		}
		result, _ := json.Marshal(epochQueryMCPResult{Content: []epochQueryMCPText{{Type: "text"}}, Structured: json.RawMessage(`{}`)})
		envelope, _ := json.Marshal(epochQueryMCPResponse{JSONRPC: "2.0", ID: id, Result: result})
		// Pinned SDK v1.6.1 repeats JSON as raw structuredContent and quoted
		// text. Already serialized JSON has no raw controls: quoting adds at
		// most another copy. Reserve the exact empty wrapper and fixed SSE.
		return 3*payload + int64(len(envelope)+len(epochQuerySSEPrefix)+len(epochQuerySSESuffix)), nil
	}
	if transport != "http" {
		return 0, errEpochInspection
	}
	host, port, err := net.SplitHostPort(listen)
	number, numberErr := strconv.ParseUint(port, 10, 16)
	if err != nil || host != "127.0.0.1" || numberErr != nil || number == 0 || strconv.FormatUint(number, 10) != port {
		return 0, errEpochInspection
	}
	schema := "Result"
	if query.ExpectedStatus != http.StatusOK {
		schema = "ErrorModel"
	} else if query.Surface == "service_detail" {
		schema = "ServiceDetail"
	} else if query.Surface == "service_relationships" {
		schema = "RelationshipPage"
	}
	extra, _ := json.Marshal(struct {
		Schema string `json:"$schema"`
	}{Schema: "http://" + listen + "/schemas/" + schema + ".json"})
	// Inserting this field adds len(extra)-1 bytes; the JSON encoder adds LF.
	return payload + int64(len(extra)), nil
}

// Canonical round-trip also rejects duplicate envelope fields and omitted
// required fields. Payload schema/semantics are the projection reader's job.
func epochQueryWireJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return errEpochInspection
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errEpochInspection
	}
	return nil
}

func decodeEpochQueryMCP(query QueryCase, contentType, id string, raw []byte) ([]byte, string, error) {
	maximum, err := epochQueryBodyMaximum(query, "mcp", "", id)
	if err != nil || int64(len(raw)) > maximum {
		return nil, "", errEpochInspection
	}
	switch contentType {
	case "application/json":
	case "text/event-stream":
		if !bytes.HasPrefix(raw, []byte(epochQuerySSEPrefix)) || !bytes.HasSuffix(raw, []byte(epochQuerySSESuffix)) {
			return nil, "", errEpochInspection
		}
		raw = raw[len(epochQuerySSEPrefix) : len(raw)-len(epochQuerySSESuffix)]
	default:
		return nil, "", errEpochInspection
	}
	var response epochQueryMCPResponse
	if epochQueryWireJSON(raw, &response) != nil || response.JSONRPC != "2.0" || response.ID != id {
		return nil, "", errEpochInspection
	}
	var result epochQueryMCPResult
	if epochQueryWireJSON(response.Result, &result) != nil || len(result.Content) != 1 || result.Content[0].Type != "text" {
		return nil, "", errEpochInspection
	}
	if query.ExpectedMCPCode == "unknown_repository" {
		if !result.IsError || len(result.Structured) != 0 || result.Content[0].Text != epochQueryNotFound {
			return nil, "", errEpochInspection
		}
		return nil, "unknown_repository", nil
	}
	payloadMaximum, err := epochQueryPayloadMaximum(query)
	if err != nil || result.IsError || int64(len(result.Structured)) > payloadMaximum || len(result.Structured) < 2 ||
		result.Structured[0] != '{' || result.Structured[len(result.Structured)-1] != '}' || !bytes.Equal([]byte(result.Content[0].Text), result.Structured) {
		return nil, "", errEpochInspection
	}
	return bytes.Clone(result.Structured), "ok", nil
}
