package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

const epochNativeQuerySchema = "t422-query-native-helper-v1"
const epochNativeQueryInputMaximum = (17 << 20) + (64 << 10)

type epochNativeQueryInput struct {
	Schema      string          `json:"schema"`
	Listen      string          `json:"listen"`
	Repository  string          `json:"repository"`
	APIKey      string          `json:"api_key"`
	NextOrdinal uint64          `json:"next_ordinal"`
	Catalog     json.RawMessage `json:"catalog"`
	Final       json.RawMessage `json:"final"`
}

type epochNativeQueryOutput struct {
	Schema                  string                  `json:"schema"`
	NextOrdinal             uint64                  `json:"next_ordinal"`
	Rows                    []ExecutionProductQuery `json:"rows"`
	query, transport, stage string
	clause                  string
	page                    uint64
	bodyPrefix              []byte
	bodyBytes               int
	bodySHA256              string
}

// This opt-in test process has no execution-run or phase-control facsimile.
// Its parent owns the actual ordinary exact-control server and its lifetime.
// Only private FD3 receives source-free completed rows; errors never print
// input, credentials or URL-bearing transport errors. Only a failed relationship
// projection after bounded transport validation retains a private response prefix.
func TestEpochNativeQueryHelper(t *testing.T) {
	if os.Getenv("PHEBS_T422_NATIVE_QUERY_HELPER") != "1" {
		t.Skip("private native query helper")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	readClosed := make(chan struct{})
	stopRead := context.AfterFunc(ctx, func() {
		_ = os.Stdin.Close() // Unblock a stalled private input pipe at the same deadline.
		close(readClosed)
	})
	defer func() {
		if !stopRead() {
			<-readClosed
		}
	}()
	var descriptor syscall.Stat_t
	if syscall.Fstat(3, &descriptor) != nil || descriptor.Mode&syscall.S_IFMT != syscall.S_IFREG || descriptor.Mode&0777 != 0600 || descriptor.Size != 0 {
		t.Fatal("native query output descriptor refused")
	}
	output := os.NewFile(3, "native-query-result")
	if output == nil {
		t.Fatal("native query output unavailable")
	}
	info, err := output.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != 0 {
		t.Fatal("native query output refused")
	}
	defer func() {
		if output.Close() != nil {
			t.Error("native query output close refused")
		}
	}()
	input, queries, err := decodeEpochNativeQueryInput(ctx, os.Stdin)
	if err != nil {
		t.Fatal("native query input refused")
	}
	result, err := runEpochNativeQueries(ctx, input, queries)
	if err != nil {
		if result.bodyBytes != 0 {
			t.Logf("private relationship response: bytes=%d sha256=%s prefix_base64=%s", result.bodyBytes, result.bodySHA256, base64.StdEncoding.EncodeToString(result.bodyPrefix))
		}
		t.Fatalf("native query corridor refused: completed_rows=%d next_ordinal=%d query=%s transport=%s page=%d stage=%s clause=%s", len(result.Rows), result.NextOrdinal, result.query, result.transport, result.page, result.stage, result.clause)
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw)+1 > 64<<10 || ctx.Err() != nil {
		t.Fatal("native query result refused")
	}
	raw = append(raw, '\n')
	if n, err := output.Write(raw); err != nil || n != len(raw) || output.Sync() != nil || ctx.Err() != nil {
		t.Fatal("native query result write refused")
	}
}

func decodeEpochNativeQueryInput(ctx context.Context, source io.Reader) (epochNativeQueryInput, *epochQueryProjectionContext, error) {
	var input epochNativeQueryInput
	if ctx == nil || ctx.Err() != nil || source == nil {
		return input, nil, errEpochInspection
	}
	raw, err := io.ReadAll(io.LimitReader(source, epochNativeQueryInputMaximum+1))
	// Canonical JSON also refuses duplicate/aliased fields. The parent writes
	// one json.Marshal value (an optional final newline is accepted).
	if err != nil || len(raw) > epochNativeQueryInputMaximum || epochQueryWireJSON(bytes.TrimSuffix(raw, []byte{'\n'}), &input) != nil {
		return input, nil, errEpochInspection
	}
	host, port, listenErr := net.SplitHostPort(input.Listen)
	portNumber, portErr := strconv.ParseUint(port, 10, 16)
	if input.Schema != epochNativeQuerySchema || listenErr != nil || host != "127.0.0.1" || portErr != nil || portNumber == 0 || strconv.FormatUint(portNumber, 10) != port ||
		reponame.Validate(input.Repository) != nil || input.Repository == epochQueryHiddenRepository || input.APIKey != "t421-final-authority-regression-key" ||
		input.NextOrdinal == 0 || input.NextOrdinal > 11531-37 || len(input.Catalog) == 0 || len(input.Catalog) > 16<<20 || len(input.Final) == 0 || len(input.Final) > epochFinalResponseBytes || ctx.Err() != nil {
		return input, nil, errEpochInspection
	}
	var catalog servicecatalog.Catalog
	var final epochFinalResponse
	if decodeEpochQueryJSON(input.Catalog, &catalog) != nil || decodeEpochQueryJSON(input.Final, &final) != nil {
		return input, nil, errEpochInspection
	}
	queries, err := newEpochQueryProjectionContext(ctx, input.Repository, final, catalog)
	return input, queries, err
}

func runEpochNativeQueries(ctx context.Context, input epochNativeQueryInput, queries *epochQueryProjectionContext) (epochNativeQueryOutput, error) {
	result := epochNativeQueryOutput{Schema: epochNativeQuerySchema, NextOrdinal: input.NextOrdinal}
	for _, transport := range []string{"http", "mcp"} {
		for _, query := range correctedQueryCases() {
			result.query, result.transport, result.page, result.stage = query.Name, transport, 0, "project"
			projection, err := queries.queryProjection(query)
			maximum, maxErr := epochProductRemainingReads(result.Rows)
			if err != nil || maxErr != nil {
				return result, errEpochInspection
			}
			row := ExecutionProductQuery{Name: query.Name, Transport: transport, FirstOrdinal: result.NextOrdinal}
			cursor := ""
			for page := uint64(0); page < correctedProductQueryPages(query); page++ {
				result.page, result.stage = page+1, "request"
				ordinal := result.NextOrdinal
				result.NextOrdinal++ // Consumed requests are never retried.
				raw, status, contentType, report, err := readEpochNativeQuery(ctx, input, query, transport, cursor, ordinal, maximum)
				if err != nil {
					return result, errEpochInspection
				}
				projectionBody := raw
				if transport == "http" {
					result.stage = "project"
					row.Code = strconv.Itoa(status)
					cursor, err = projection.addHTTP(status, raw)
				} else {
					result.stage = "decode"
					var structured []byte
					if status != http.StatusOK {
						return result, errEpochInspection
					}
					structured, row.Code, err = decodeEpochQueryMCP(query, contentType, strconv.FormatUint(ordinal, 10), raw)
					if err == nil {
						result.stage = "project"
						projectionBody = structured
						cursor, err = projection.addMCP(row.Code, structured)
					}
				}
				if err != nil || (cursor == "") != (page+1 == correctedProductQueryPages(query)) {
					result.clause = projection.refusal
					if query.Surface == "service_relationships" && result.stage == "project" && status == http.StatusOK {
						result.retainRelationshipResponse(projectionBody)
					}
					return result, errEpochInspection
				}
				maximum.ControlFileReads -= report.ControlFileReads
				maximum.StoreReadAttempts -= report.StoreReadAttempts
				maximum.MemberVisits -= report.MemberVisits
				row.ControlFileReads += report.ControlFileReads
				row.StoreReadAttempts += report.StoreReadAttempts
				row.MemberVisits += report.MemberVisits
				if report.VisibleRepositories != nil {
					row.VisibleRepositories, row.VisibleRepositoriesObserved = *report.VisibleRepositories, true
				}
				row.LastOrdinal = ordinal
			}
			result.stage = "finish"
			actual, err := projection.finish()
			controls, stores, countErr := correctedProductQueryControlReads(query)
			transportIndex := 0
			if transport == "mcp" {
				transportIndex = 1
			} else if query.Name == "first_service" {
				stores += 4
			}
			if err != nil || countErr != nil || actual.SHA256 != query.ProjectionSHA256 || actual.Pages != correctedProductQueryPages(query) ||
				actual.Records != query.ExpectedRecords || actual.Paths != query.ExpectedPaths || row.ControlFileReads != controls || row.StoreReadAttempts != stores ||
				!validQueryMemberReads(PlanV3Schema, query, transportIndex, row.MemberVisits, row.MemberVisits) {
				return result, errEpochInspection
			}
			row.ProjectionSHA256, row.Pages, row.Records, row.Paths = actual.SHA256, actual.Pages, actual.Records, actual.Paths
			result.Rows = append(result.Rows, row)
		}
	}
	if !validExecutionProductQueries(result.Rows) || result.NextOrdinal-input.NextOrdinal != 38 || ctx.Err() != nil {
		return result, errEpochInspection
	}
	return result, nil
}

func (result *epochNativeQueryOutput) retainRelationshipResponse(body []byte) {
	result.bodyBytes, result.bodySHA256 = len(body), SHA256(body)
	result.bodyPrefix = bytes.Clone(body[:min(len(body), 64<<10)])
}

// Same bounded transport/trailer rules as readQueryRequest, without constructing
// an ExecutionRun or pretending this ordinary fixture owns selected DA/PC.
func readEpochNativeQuery(ctx context.Context, input epochNativeQueryInput, query QueryCase, transport, cursor string, ordinal uint64, maximum epochInspectionReport) ([]byte, int, string, epochInspectionReport, error) {
	id := strconv.FormatUint(ordinal, 10)
	method, path, payload, err := epochQueryRequest(query, transport, input.Repository, cursor, id)
	limit, limitErr := epochQueryBodyMaximum(query, transport, input.Listen, id)
	if ctx == nil || ctx.Err() != nil || err != nil || limitErr != nil {
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://"+input.Listen+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	request.Header.Set("Authorization", "Bearer "+input.APIKey)
	request.Header.Set("X-Phebs-T421-Exact-Reads", "source-free-v1")
	request.Header.Set("X-Phebs-T421-Exact-Read-Ordinal", id)
	if query.Name == "all_code_structural_marker" {
		request.Header.Set("X-Phebs-T422-Query-Evidence", "bound-v1")
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
	}
	wire := &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10}
	defer wire.CloseIdleConnections()
	client := &http.Client{Transport: wire, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, "", epochInspectionReport{}, errEpochInspection
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	closeErr := response.Body.Close()
	report, reportErr := decodeEpochReport(response.Trailer.Values(epochReadTrailer), ordinal, maximum)
	if readErr != nil || closeErr != nil || reportErr != nil || !validEpochQueryRepositories(report, query.Name == "all_code_structural_marker") || int64(len(raw)) > limit || ctx.Err() != nil ||
		len(response.Header.Values(epochReadTrailer)) != 0 || len(response.Trailer) != 1 || response.Uncompressed || response.Header.Get("Content-Encoding") != "" ||
		payload != nil && len(response.Header.Values("Content-Type")) != 1 {
		return nil, 0, "", report, errEpochInspection
	}
	return raw, response.StatusCode, response.Header.Get("Content-Type"), report, nil
}

func TestEpochNativeQueryInputRefusals(t *testing.T) {
	for _, raw := range []string{"", "{}", "null", "{}\n{}", `{"schema":"wrong"}`, `{"unknown":1}`, strings.Repeat(" ", epochNativeQueryInputMaximum+1)} {
		if _, _, err := decodeEpochNativeQueryInput(t.Context(), strings.NewReader(raw)); err == nil {
			t.Fatal("invalid private input accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := decodeEpochNativeQueryInput(ctx, strings.NewReader("{}")); err == nil {
		t.Fatal("canceled private input accepted")
	}
	bound, catalog := epochQueryProjectionFixture(t)
	valid := epochNativeQueryInput{Schema: epochNativeQuerySchema, Listen: "127.0.0.1:32123", Repository: bound.repository, APIKey: "t421-final-authority-regression-key", NextOrdinal: 10,
		Catalog: epochQueryMarshal(t, catalog), Final: epochQueryMarshal(t, bound.final)}
	for _, mode := range []string{"valid", "schema", "loopback", "port", "key", "ordinal_zero", "ordinal_overflow", "repository", "catalog", "final", "catalog_cap", "final_cap", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			input := valid
			switch mode {
			case "schema":
				input.Schema = "other"
			case "loopback":
				input.Listen = "localhost:32123"
			case "port":
				input.Listen = "127.0.0.1:032123"
			case "key":
				input.APIKey = strings.Repeat("0", 64)
			case "ordinal_zero":
				input.NextOrdinal = 0
			case "ordinal_overflow":
				input.NextOrdinal = ^uint64(0)
			case "repository":
				input.Repository = "../escape"
			case "catalog":
				input.Catalog = []byte("{}")
			case "final":
				input.Final = []byte("{}")
			case "catalog_cap":
				input.Catalog = epochQueryMarshal(t, strings.Repeat("x", 16<<20))
			case "final_cap":
				input.Final = epochQueryMarshal(t, strings.Repeat("x", 1<<20))
			}
			raw := epochQueryMarshal(t, input)
			if mode == "duplicate" {
				raw = append([]byte(`{"schema":"ignored",`), raw[1:]...)
			}
			_, queries, err := decodeEpochNativeQueryInput(t.Context(), bytes.NewReader(raw))
			if mode == "valid" {
				if err != nil || queries == nil {
					t.Fatal("valid supplied private input refused")
				}
			} else if err == nil {
				t.Fatal("altered private input accepted")
			}
		})
	}
}

func TestEpochNativeQueryFailureLocation(t *testing.T) {
	queries, _ := epochQueryProjectionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := runEpochNativeQueries(ctx, epochNativeQueryInput{Listen: "127.0.0.1:1", NextOrdinal: 2}, queries)
	if err != errEpochInspection || len(result.Rows) != 0 || result.NextOrdinal != 3 || result.query != "all_code_structural_marker" || result.transport != "http" || result.page != 1 || result.stage != "request" {
		t.Fatal("closed failure location or consumed ordinal lost")
	}
	result.clause = "root_authority"
	result.retainRelationshipResponse(bytes.Repeat([]byte("private-response"), 10_000))
	if len(result.bodyPrefix) != 64<<10 || result.bodyBytes != 160_000 || !validDigest(result.bodySHA256) {
		t.Fatal("private response diagnostic not bounded")
	}
	encoded := epochQueryMarshal(t, result)
	if bytes.Contains(encoded, []byte("request")) || bytes.Contains(encoded, []byte("all_code")) || bytes.Contains(encoded, []byte("root_authority")) || bytes.Contains(encoded, []byte("private-response")) || bytes.Contains(encoded, []byte(result.bodySHA256)) {
		t.Fatal("diagnostic leaked into result protocol")
	}
}
