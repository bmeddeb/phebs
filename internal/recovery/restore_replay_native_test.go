package recovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/store"
)

const restoreReplayProbeScope = "phebs_restore_replay_probe"

// This fixed neutral diagnostic isolates the fresh namespace/database from
// the earlier probe's SDK-created scope. Native error text is test-only and
// bounded; ordinary restore errors remain source-free.
func TestRestoreReplayNativeFreshScope(t *testing.T) {
	if os.Getenv("PHEBS_TEST_RESTORE_REPLAY_NATIVE") != "1" {
		t.Skip("fresh native import diagnostic not selected")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	runtime, stop, err := store.StartLocalImport(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if runtime.Surreal.Version != "3.2.0" {
		t.Fatal("unproven native engine")
	}
	transport := &http.Transport{MaxConnsPerHost: 1, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, definition := range []string{"NAMESPACE", "DATABASE"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, cliEndpoint(runtime.Endpoint)+"/sql",
			strings.NewReader("BEGIN; DEFINE "+definition+" IF NOT EXISTS phebs; COMMIT;"))
		if err != nil {
			t.Fatal(err)
		}
		request.SetBasicAuth("root", "root")
		request.Header.Set("Accept", "application/json")
		if definition == "DATABASE" {
			request.Header.Set("Surreal-NS", "phebs")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxCommandOutput+1))
		if err := errors.Join(readErr, response.Body.Close()); err != nil || len(raw) > maxCommandOutput {
			t.Fatalf("bounded bootstrap response unavailable: %v", err)
		}
		t.Logf("fixed %s bootstrap HTTP=%d response=%s", definition, response.StatusCode, raw)
		response.Body = io.NopCloser(bytes.NewReader(raw))
		if err := readRestoreReplayResponse(response, true, true); err != nil {
			t.Fatalf("bootstrap response refused: %v", err)
		}
	}
	const body = "OPTION IMPORT; BEGIN;\nDEFINE TABLE api_key TYPE ANY SCHEMALESS PERMISSIONS NONE;\n\nCOMMIT;"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, cliEndpoint(runtime.Endpoint)+"/import", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("root", "root")
	request.Header.Set("Surreal-NS", "phebs")
	request.Header.Set("Surreal-DB", "phebs")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxCommandOutput+1))
	if err := errors.Join(readErr, response.Body.Close()); err != nil || len(raw) > maxCommandOutput {
		t.Fatalf("bounded native response unavailable: %v", err)
	}
	t.Logf("fixed first DEFINE fresh-scope HTTP=%d response=%s", response.StatusCode, raw)
	response.Body = io.NopCloser(bytes.NewReader(raw))
	if err := readRestoreReplayImportResponse(response, true); err != nil {
		t.Fatal(err)
	}
}

// This test consumes the separately authored, retained neutral full-owned
// schema export through the actual production spool/HTTP executor. It does not
// claim a complete six-artifact Restore or durable phase-wide accounting.
func TestRestoreReplayNativeOwnedExport(t *testing.T) {
	path := os.Getenv("PHEBS_RESTORE_REPLAY_FIXTURE_INPUT")
	if os.Getenv("PHEBS_TEST_RESTORE_REPLAY_NATIVE") != "1" || path == "" {
		t.Skip("native full-owned replay not selected")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	artifact := Artifact{Path: DatabaseName, Size: 96931,
		SHA256: "sha256:93b6a3af8d37360ddc12b5df5f23d3530107c455591f203651d02e84da7de33c"}
	prepared, err := prepareRestoreReplay(ctx, path, artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.close() }()
	if prepared.census != (restoreReplayCensus{Units: 718, Definitions: 716, Records: 15}) {
		t.Fatalf("retained owned export census: %+v", prepared.census)
	}
	target := t.TempDir()
	runtime, stop, err := store.StartLocalImport(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stop()
		}
	}()
	if runtime.Surreal.Version != "3.2.0" {
		t.Fatal("unproven native replay engine")
	}
	if err := executeRestoreReplay(ctx, prepared, target, runtime.Endpoint, DatabaseIdentity{Namespace: "phebs", Database: "phebs"}); err != nil {
		t.Fatal(err)
	}
	db, err := surrealdb.FromEndpointURLString(ctx, runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Use(ctx, "phebs", "phebs"); err != nil {
		t.Fatal(err)
	}
	metadata, err := surrealdb.Query[struct {
		Tables map[string]string `json:"tables"`
	}](ctx, db, "INFO FOR DB;", nil)
	if err != nil || metadata == nil || len(*metadata) != 1 || len((*metadata)[0].Result.Tables) != 82 {
		t.Fatalf("imported native tables before any repair: %v %v", metadata, err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	stop()
	stopped = true
	state, err := store.OpenLocal(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close(context.Background()) }()
	repo, err := state.GetRepo(ctx, "example.com/neutral/replay")
	if err != nil || repo == nil || repo.CloneURL != "https://example.com/neutral/replay.git" {
		t.Fatalf("neutral original repo after reopen: %+v %v", repo, err)
	}
	if err := state.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Log("actual protected replay completed718 units/716 definitions/15 submitted literal records; native82 tables before repair and original neutral repo after reopen; no complete Restore or durable attempt-prefix claim")
}

// This opt-in probe establishes the native endpoint/transaction semantics only.
// It is not a production restore, submitted-row meter, or durable attempt record.
func TestRestoreReplayNativeImportTransactions(t *testing.T) {
	if os.Getenv("PHEBS_TEST_RESTORE_REPLAY_NATIVE") != "1" {
		t.Skip("native replay feasibility probe not selected")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	runtime, stop, err := store.StartLocalImport(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if runtime.Surreal.Version != "3.2.0" {
		t.Fatalf("unproven native version: %s", runtime.Surreal.Version)
	}
	db, err := surrealdb.FromEndpointURLString(ctx, runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Use(ctx, restoreReplayProbeScope, restoreReplayProbeScope); err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, db, `BEGIN;
DEFINE TABLE probe SCHEMALESS;
DEFINE FIELD OVERWRITE body ON probe TYPE string VALUE 'changed';
DEFINE EVENT reject ON probe WHEN $event = 'CREATE' THEN { THROW 'neutral-restore-guard' };
COMMIT;`, nil); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := cliEndpoint(runtime.Endpoint)
	if value, present := os.LookupEnv("SURREAL_HTTP_MAX_IMPORT_BODY_SIZE"); present {
		t.Logf("actual inherited import-body override present: %q", value)
	} else {
		t.Log("actual import-body override absent; official3.x default4GiB, no oversized request attempted")
	}
	post := func(body string, success, definition bool) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/import", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.SetBasicAuth("root", "root")
		request.Header.Set("Surreal-NS", restoreReplayProbeScope)
		request.Header.Set("Surreal-DB", restoreReplayProbeScope)
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		err = readRestoreReplayImportResponse(response, definition)
		t.Logf("native import HTTP_status=%d strict_result_error=%v", response.StatusCode, err)
		if success != (err == nil) {
			t.Fatalf("unexpected strict native import result: %v", err)
		}
	}
	post("OPTION IMPORT; BEGIN; DEFINE TABLE probe_extra TYPE ANY SCHEMALESS PERMISSIONS NONE; COMMIT;", true, true)
	// The complete native export contains unquoted SHA record IDs beginning
	// with digits. Preserve their string identity and value, not just parsing.
	const digitID = "1fe44fe9046fd23d55ff4dd34fd046827a8004d6c1ba5379fb9eedeb4f6838d9"
	digitLiteral := "OPTION IMPORT; INSERT [{id: probe_extra:" + digitID + ", body: 'original'}];"
	digitScanner := newRestoreReplayScanner(strings.NewReader(digitLiteral))
	digitUnit, err := digitScanner.next()
	if err != nil || digitUnit.Count != 1 || digitUnit.Definition {
		t.Fatalf("native digit-leading ID census: %+v %v", digitUnit, err)
	}
	post("OPTION IMPORT; BEGIN; INSERT ["+digitLiteral[digitUnit.Span.Start:digitUnit.Span.End]+"]; COMMIT;", true, false)
	digitRows, err := surrealdb.Query[[]struct {
		Body string `json:"body"`
	}](ctx, db, "SELECT body FROM $rid;", map[string]any{"rid": models.NewRecordID("probe_extra", digitID)})
	if err != nil || digitRows == nil || len(*digitRows) != 1 || len((*digitRows)[0].Result) != 1 ||
		(*digitRows)[0].Result[0].Body != "original" {
		t.Fatalf("native digit-leading string ID/value not preserved: %+v %v", digitRows, err)
	}
	before := restoreReplayProbeWrites(ctx, t, client, endpoint)
	for _, step := range []struct{ start, count int }{{0, 512}, {512, 1}} {
		parts := make([]string, step.count)
		for index := range parts {
			parts[index] = fmt.Sprintf("{id: probe:`%d`, body: 'original'}", step.start+index)
		}
		literal := "OPTION IMPORT; INSERT [" + strings.Join(parts, ", ") + "];"
		scanner := newRestoreReplayScanner(strings.NewReader(literal))
		unit, err := scanner.next()
		if err != nil || unit.Count != step.count || unit.Definition {
			t.Fatalf("actual literal row census: %+v %v", unit, err)
		}
		post("OPTION IMPORT; BEGIN; INSERT ["+literal[unit.Span.Start:unit.Span.End]+"]; COMMIT;", true, false)
	}
	after := restoreReplayProbeWrites(ctx, t, client, endpoint)
	if after < before || after-before != 2 {
		t.Fatalf("two native explicit write transactions: before=%d after=%d", before, after)
	}
	assertRows := func() {
		t.Helper()
		rows, err := surrealdb.Query[[]map[string]uint64](ctx, db,
			"SELECT count() AS total FROM probe WHERE body = 'original' GROUP ALL; SELECT count() AS total FROM probe GROUP ALL;", nil)
		if err != nil || rows == nil || len(*rows) != 2 {
			t.Fatalf("native row census: %v", err)
		}
		for _, result := range *rows {
			if len(result.Result) != 1 || result.Result[0]["total"] != 513 {
				t.Fatalf("native import values/count changed: %+v", rows)
			}
		}
	}
	assertRows()
	if _, err := surrealdb.Query[any](ctx, db, "INSERT {id: probe:ordinary, body: 'original'};", nil); err == nil || !strings.Contains(err.Error(), "neutral-restore-guard") {
		t.Fatalf("ordinary write did not retain native guard behavior: %v", err)
	}
	beforeFailure := restoreReplayProbeWrites(ctx, t, client, endpoint)
	parts := make([]string, 512)
	for index := range parts {
		parts[index] = fmt.Sprintf("{id: probe:`failed-%d`, body: 'original'}", index)
	}
	post("OPTION IMPORT; BEGIN; INSERT ["+strings.Join(parts, ", ")+"]; THROW 'neutral-restore-rollback'; COMMIT;", false, false)
	assertRows()
	afterFailure := restoreReplayProbeWrites(ctx, t, client, endpoint)
	if afterFailure < beforeFailure || afterFailure-beforeFailure != 1 {
		t.Fatalf("failed native explicit write transaction: before=%d after=%d", beforeFailure, afterFailure)
	}
	canceled, cancelRequest := context.WithCancel(ctx)
	cancelRequest()
	request, err := http.NewRequestWithContext(canceled, http.MethodPost, endpoint+"/import", strings.NewReader("OPTION IMPORT; BEGIN; INSERT [{id: probe:canceled, body:'original'}]; COMMIT;"))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("root", "root")
	request.Header.Set("Surreal-NS", restoreReplayProbeScope)
	request.Header.Set("Surreal-DB", restoreReplayProbeScope)
	if response, err := client.Do(request); !errors.Is(err, context.Canceled) {
		if response != nil {
			_ = response.Body.Close()
		}
		t.Fatalf("canceled import request = %v", err)
	}
	if afterCanceled := restoreReplayProbeWrites(ctx, t, client, endpoint); afterCanceled != afterFailure {
		t.Fatal("already-canceled import request added a native completed write")
	}
	t.Log("native completed write deltas=2 success,1 failed; recognized submitted data rows per unit=512/1/512; no native attempted-prefix or phase-wide max-row meter claimed")
}

func restoreReplayProbeWrites(ctx context.Context, t *testing.T, client *http.Client, endpoint string) uint64 {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("root", "root")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(raw) > 1<<20 {
		t.Fatal("native metric response unavailable")
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var rowNames []string
	for name := range families {
		if strings.Contains(name, "row") || strings.Contains(name, "record") {
			rowNames = append(rowNames, name)
		}
	}
	if rows := families["surrealdb_statement_rows_total"]; rows != nil {
		t.Logf("native statement-row metric help (not a submitted-row definition): %q", rows.GetHelp())
	}
	slices.Sort(rowNames)
	t.Logf("available native metric names containing row/record (not classified submitted rows): %q", rowNames)
	family := families["surrealdb_transaction_total"]
	if family == nil {
		t.Fatal("native completed transaction metric unavailable, not zero")
	}
	var total uint64
	found := false
	for _, metric := range family.GetMetric() {
		labels := make(map[string]string)
		for _, pair := range metric.GetLabel() {
			if _, duplicate := labels[pair.GetName()]; duplicate {
				t.Fatal("duplicate native transaction label")
			}
			labels[pair.GetName()] = pair.GetValue()
		}
		if labels["namespace"] != restoreReplayProbeScope || labels["database"] != restoreReplayProbeScope || labels["write"] != "true" {
			continue
		}
		value := metric.GetCounter().GetValue()
		if labels["user"] != "root" || metric.Counter == nil || value < 0 || value >= 1<<53 || math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
			t.Fatal("invalid native exact write counter")
		}
		total += uint64(value)
		found = true
	}
	if !found {
		t.Fatal("native scoped write counter unavailable, not zero")
	}
	return total
}
