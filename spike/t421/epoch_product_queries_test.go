package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
)

// Modeled receipt-shaped rows only: this never claims native query execution.
func epochProductTestRows(t *testing.T) []ExecutionProductQuery {
	t.Helper()
	var rows []ExecutionProductQuery
	ordinal := uint64(10)
	for _, transport := range []string{"http", "mcp"} {
		for _, query := range correctedQueryCases() {
			controls, stores, err := correctedProductQueryControlReads(query)
			if err != nil {
				t.Fatal(err)
			}
			code := strconv.Itoa(int(query.ExpectedStatus))
			if transport == "mcp" {
				code = query.ExpectedMCPCode
			} else if query.Name == "first_service" {
				stores += 4
			}
			members := uint64(0)
			if query.Surface == "service_relationships" || transport == "http" && query.Name == "first_service" {
				members = 1
			}
			pages := correctedProductQueryPages(query)
			rows = append(rows, ExecutionProductQuery{Name: query.Name, Transport: transport, Code: code,
				ProjectionSHA256: query.ProjectionSHA256, Pages: pages, Records: query.ExpectedRecords, Paths: query.ExpectedPaths,
				FirstOrdinal: ordinal, LastOrdinal: ordinal + pages - 1, ControlFileReads: controls, StoreReadAttempts: stores, MemberVisits: members})
			ordinal += pages
		}
	}
	return rows
}

func TestEpochProductQueryPrefix(t *testing.T) {
	rows := epochProductTestRows(t)
	if !validExecutionProductQueries(rows) || len(rows) != 22 || rows[len(rows)-1].LastOrdinal-rows[0].FirstOrdinal != 37 {
		t.Fatal("closed modeled inventory refused")
	}
	for _, mode := range []string{"missing", "reordered", "duplicate", "hash", "pages", "ordinal", "overflow", "controls", "stores", "members", "hidden_reads"} {
		t.Run(mode, func(t *testing.T) {
			bad := slices.Clone(rows)
			switch mode {
			case "missing":
				bad = bad[:len(bad)-1]
			case "reordered":
				bad[0], bad[1] = bad[1], bad[0]
			case "duplicate":
				bad[1] = bad[0]
			case "hash":
				bad[0].ProjectionSHA256 = ""
			case "pages":
				bad[0].Pages++
			case "ordinal":
				bad[1].FirstOrdinal++
			case "overflow":
				bad[0].MemberVisits = ^uint64(0)
			case "controls":
				bad[0].ControlFileReads++
			case "stores":
				bad[0].StoreReadAttempts++
			case "members":
				bad[2].MemberVisits = 0
			case "hidden_reads":
				bad[1].StoreReadAttempts++
				bad[2].StoreReadAttempts-- // Preserve total: per-transport check still refuses.
			}
			if validExecutionProductQueries(bad) {
				t.Fatal("accepted altered execution prefix")
			}
		})
	}
}

func TestEpochProductQueryActualHTTPRefusal(t *testing.T) {
	for _, mode := range []string{"valid", "bad_body", "bad_report", "wrong_order", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/search" || r.URL.Query().Get("repository") != epochQueryHiddenRepository ||
					r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "11" {
					t.Error("unexpected actual request")
				}
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", RequestOrdinal: 11, Status: "complete", StoreReadAttempts: 1}
				if mode == "bad_report" {
					report.MemberVisits = 1
				}
				w.Header().Set("Trailer", epochReadTrailer)
				w.WriteHeader(http.StatusNotFound)
				body := []byte(`{"title":"Not Found","status":404,"detail":"search scope not found"}`)
				if mode == "bad_body" {
					body = []byte(`{"status":200}`)
				}
				_, _ = w.Write(body)
				w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(epochTestJSON(t, report, false), []byte{'\n'})))
			}))
			reader.run.epoch.Epoch, reader.run.epoch.Repository = 5, "example.com/mono"
			reader.projection.Phase, reader.productFinalCalls, reader.maximumReports = "product_queries", 1, 8691
			reader.next = 11
			reader.productQueries = epochProductTestRows(t)[:1]
			query := correctedQueryCases()[1]
			if mode == "wrong_order" {
				query = correctedQueryCases()[0]
			}
			ctx := t.Context()
			if mode == "canceled" {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			err := reader.productQuery(ctx, &epochQueryProjectionContext{repository: reader.run.epoch.Repository, services: []string{serviceKey(0)}}, query, "http")
			if (err == nil) != (mode == "valid") {
				t.Fatalf("actual transport disposition: %v; calls=%d refusal=%+v status=%d body=%q", err, calls.Load(), reader.readFailure, reader.failureStatus, reader.failureBody)
			}
			if mode == "valid" {
				got := reader.productQueries[1]
				if got != epochProductTestRows(t)[1] || reader.reports != 1 || reader.next != 12 {
					t.Fatal("actual response/report prefix lost", got)
				}
			} else {
				before := calls.Load()
				if reader.productQuery(t.Context(), &epochQueryProjectionContext{}, query, "http") == nil || calls.Load() != before || len(reader.productQueries) != 1 {
					t.Fatal("refusal retried or invented completed transport")
				}
				if mode == "bad_body" || mode == "bad_report" {
					if reader.reports != 1 || reader.totals.StoreReadAttempts != 1 {
						t.Fatal("semantic refusal erased accepted native report")
					}
				}
			}
		})
	}
}

// Real HTTP/POST, ordinals, trailers, typed decoders and the complete driver
// order; product authority and payloads remain explicitly supplied fixtures.
func TestEpochProductQueryCorridor(t *testing.T) {
	bound, _ := epochQueryProjectionFixture(t)
	type page struct {
		query     QueryCase
		transport string
		cursor    string
		body      []byte
		status    int
		report    epochInspectionReport
	}
	var pages []page
	for _, row := range epochProductTestRows(t) {
		var query QueryCase
		for _, candidate := range correctedQueryCases() {
			if candidate.Name == row.Name {
				query = candidate
			}
		}
		var bodies [][]byte
		switch query.Surface {
		case "all_code_search", "service_search":
			if query.ExpectedStatus == http.StatusNotFound {
				bodies = [][]byte{[]byte(`{"title":"Not Found","status":404,"detail":"search scope not found"}`)}
			} else {
				bodies = [][]byte{epochQueryMarshal(t, epochQuerySearchFixture(t, bound, query, 0))}
			}
		case "service_detail":
			bodies = [][]byte{epochQueryMarshal(t, epochQueryServiceFixture(bound))}
		case "service_relationships":
			for _, value := range epochQueryRelationshipFixture(t, bound, query) {
				bodies = append(bodies, epochQueryMarshal(t, value))
			}
		}
		cursor := ""
		for i, body := range bodies {
			requestCursor := cursor
			if query.PageSize > 0 {
				var value struct {
					Pagination struct {
						NextCursor string `json:"next_cursor"`
					} `json:"pagination"`
				}
				if err := json.Unmarshal(body, &value); err != nil {
					t.Fatal(err)
				}
				cursor = value.Pagination.NextCursor
			}
			ordinal := uint64(10 + len(pages))
			report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", RequestOrdinal: ordinal, Status: "complete",
				ControlFileReads: row.ControlFileReads, StoreReadAttempts: row.StoreReadAttempts, MemberVisits: row.MemberVisits}
			if len(bodies) > 1 {
				report.ControlFileReads, report.StoreReadAttempts = 2, 3
				if i == 0 {
					report.ControlFileReads, report.StoreReadAttempts = 5, 4
				}
			}
			status := int(query.ExpectedStatus)
			if row.Transport == "mcp" {
				result := epochQueryMCPResult{Content: []epochQueryMCPText{{Type: "text", Text: string(body)}}, Structured: json.RawMessage(body)}
				if status != http.StatusOK {
					result.Content[0].Text, result.Structured, result.IsError = epochQueryNotFound, nil, true
				}
				body = epochQueryMarshal(t, epochQueryMCPResponse{JSONRPC: "2.0", ID: strconv.FormatUint(ordinal, 10), Result: epochQueryMarshal(t, result)})
				status = http.StatusOK
			}
			pages = append(pages, page{query: query, transport: row.Transport, cursor: requestCursor, body: body, status: status, report: report})
		}
	}
	var count atomic.Int32
	reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(count.Add(1)) - 1
		if i >= len(pages) {
			t.Error("extra query request")
			w.WriteHeader(http.StatusConflict)
			return
		}
		page := pages[i]
		method, path, payload, err := epochQueryRequest(page.query, page.transport, bound.repository, page.cursor, strconv.FormatUint(page.report.RequestOrdinal, 10))
		if err != nil || r.Method != method || r.URL.RequestURI() != path ||
			r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != strconv.FormatUint(page.report.RequestOrdinal, 10) {
			t.Error("query request order or ordinal changed")
		}
		if page.transport == "mcp" {
			raw, err := io.ReadAll(io.LimitReader(r.Body, epochQueryRequestBytes+1))
			if err != nil || !bytes.Equal(raw, payload) {
				t.Error("not a bounded direct tool call")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Trailer", epochReadTrailer)
		w.WriteHeader(page.status)
		_, _ = w.Write(page.body)
		w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(epochQueryMarshal(t, page.report)))
	}))
	reader.run.epoch.Epoch, reader.run.epoch.Repository = 5, bound.repository
	reader.projection.Phase, reader.productFinalCalls, reader.maximumReports, reader.next = "product_queries", 1, 8691, 10
	for _, transport := range []string{"http", "mcp"} {
		for _, query := range correctedQueryCases() {
			if err := reader.productQuery(t.Context(), bound, query, transport); err != nil {
				t.Fatalf("%s/%s: %v; refusal=%+v", transport, query.Name, err, reader.readFailure)
			}
		}
	}
	if count.Load() != 38 || reader.reports != 38 || reader.next != 48 || !validExecutionProductQueries(reader.productQueries) ||
		reader.totals.ControlFileReads != 160 || reader.totals.StoreReadAttempts != 164 || reader.productQueriesComplete {
		t.Fatal("corridor result or actual report totals differ")
	}
}
