package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

const submissionProbeScope = "phebs_submission_probe"
const submissionProbeControl = "phebs_submission_control"

// This opt-in, single-engine diagnostic attributes completed native writes
// only. Scrapes themselves add reads; retained read snapshots are unattributed,
// not zero. This is not submitted-row or attempted-prefix accounting.
func TestSubmissionNativeSignInUseProbe(t *testing.T) {
	if os.Getenv("PHEBS_TEST_SUBMISSION_NATIVE_PROBE") != "1" {
		t.Skip("set PHEBS_TEST_SUBMISSION_NATIVE_PROBE=1 for the serialized native diagnostic")
	}
	deadline := time.Now().Add(2 * time.Minute)
	// Reserve all three sequential SDK closes and the owned native stop.
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
		t.Fatalf("native start unavailable (error type %T)", err)
	}
	t.Cleanup(stop)
	if runtime.Surreal.Version != "3.2.0" {
		t.Fatalf("unexpected native version %q", runtime.Surreal.Version)
	}
	open := func() *surrealdb.DB {
		t.Helper()
		db, err := surrealdb.FromEndpointURLString(ctx, runtime.Endpoint)
		if err != nil {
			t.Fatalf("raw SDK connection: %v", err)
		}
		t.Cleanup(func() {
			cleanup, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancelCleanup()
			if err := db.Close(cleanup); err != nil {
				t.Errorf("close raw SDK: %v", err)
			}
		})
		return db
	}
	signIn := func(db *surrealdb.DB) error {
		_, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"})
		return err
	}
	control := open()
	// Retain this actual root token only in memory. Both prior BasicAuth and
	// bearer probes observed one native read per scrape before any operation.
	bearer, err := control.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"})
	if err != nil || bearer == "" {
		t.Fatalf("root control SignIn/token unavailable: %v", err)
	}
	// Initialize observable read/write series outside every measurement. This
	// creates no application schema and does not touch the target namespace.
	if _, err := surrealdb.Query[any](ctx, control,
		"DEFINE NAMESPACE "+submissionProbeControl+"; INFO FOR ROOT;", nil); err != nil {
		t.Fatalf("neutral counter initialization: %v", err)
	}
	root := submissionProbeInfo(ctx, t, control, "INFO FOR ROOT;")
	if _, exists := root.Namespaces[submissionProbeScope]; exists {
		t.Fatal("target namespace existed before fresh Use")
	}
	t.Log("fresh target namespace absent before measurement")
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := "http://" + strings.TrimPrefix(runtime.Endpoint, "ws://") + "/metrics"
	snapshot := func() idleClaimMetricSnapshot {
		t.Helper()
		value, err := readSubmissionProbeMetrics(ctx, client, endpoint, bearer)
		if err != nil {
			t.Fatalf("native metrics unavailable: %v\n%s", err, value.raw)
		}
		return value
	}
	writeControl := func(name string) (idleClaimMetricSnapshot, idleClaimMetricSnapshot) {
		t.Helper()
		before, after := snapshot(), snapshot()
		delta, err := submissionProbeWriteDelta(before, after)
		if err != nil || delta != 0 {
			t.Fatalf("%s scrape-only write control unavailable/changed: write=%d/%d; no operation attribution: %v",
				name, before.write, after.write, err)
		}
		t.Logf("%s scrape-only write control unchanged=%d; observed_read_snapshots=%d/%d read_series_present=%t/%t read_attribution=unavailable\n%s",
			name, after.write, before.read, after.read, before.readPresent, after.readPresent, after.raw)
		return before, after
	}
	_, _ = writeControl("initial")
	measure := func(name string, operation func() error) error {
		t.Helper()
		_, before := writeControl(name + "_before")
		operationErr := operation()
		after, _ := writeControl(name + "_after")
		delta, err := submissionProbeWriteDelta(before, after)
		if err != nil {
			t.Fatalf("%s write attribution unavailable: %v", name, err)
		}
		t.Logf("%s completed_delta_write=%d observed_read_snapshots=%d/%d read_series_present=%t/%t read_attribution=unavailable error_type=%T\n%s",
			name, delta, before.read, after.read, before.readPresent, after.readPresent, operationErr, after.raw)
		return operationErr
	}
	fresh := open()
	if err := measure("fresh_root_signin", func() error { return signIn(fresh) }); err != nil {
		t.Fatalf("fresh root SignIn: %v", err)
	}
	if err := measure("fresh_use", func() error { return fresh.Use(ctx, submissionProbeScope, submissionProbeScope) }); err != nil {
		t.Fatalf("fresh_use: %v", err)
	}
	// Inspect existence immediately after fresh Use, before any target-scoped
	// SQL query or repeat Use. The separate control connection is still unscoped;
	// its fixed ROOT read cannot create the target via a USE statement.
	root = submissionProbeInfo(ctx, t, control, "INFO FOR ROOT;")
	_, namespaceExists := root.Namespaces[submissionProbeScope]
	t.Logf("after_fresh_use target_namespace_exists=%t", namespaceExists)
	if err := measure("repeat_fresh_use", func() error { return fresh.Use(ctx, submissionProbeScope, submissionProbeScope) }); err != nil {
		t.Fatalf("repeat_fresh_use: %v", err)
	}
	for _, statement := range []string{"INFO FOR NS;", "INFO FOR DB;"} {
		_ = measure("fresh_"+statement, func() error {
			_, err := surrealdb.Query[any](ctx, fresh, statement, nil)
			return err
		})
	}
	// Establish existing resources explicitly, outside the next SignIn/Use
	// windows; this setup is not attributed to those operations.
	if _, err := surrealdb.Query[any](ctx, control,
		"DEFINE NAMESPACE IF NOT EXISTS "+submissionProbeScope+";", nil); err != nil {
		t.Fatal(err)
	}
	if err := control.Use(ctx, submissionProbeScope, submissionProbeScope); err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, control,
		"DEFINE DATABASE IF NOT EXISTS "+submissionProbeScope+";", nil); err != nil {
		t.Fatal(err)
	}
	root = submissionProbeInfo(ctx, t, control, "INFO FOR ROOT;")
	namespace := submissionProbeInfo(ctx, t, control, "INFO FOR NS;")
	_, namespaceExists = root.Namespaces[submissionProbeScope]
	_, databaseExists := namespace.Databases[submissionProbeScope]
	if !namespaceExists || !databaseExists {
		t.Fatal("explicit existing namespace/database setup did not persist")
	}
	existing := open()
	if err := measure("existing_root_signin", func() error { return signIn(existing) }); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"existing_use", "repeat_existing_use"} {
		if err := measure(name, func() error { return existing.Use(ctx, submissionProbeScope, submissionProbeScope) }); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, statement := range []string{"INFO FOR ROOT;", "INFO FOR NS;", "INFO FOR DB;"} {
		if err := measure("existing_"+statement, func() error {
			_, err := surrealdb.Query[any](ctx, existing, statement, nil)
			return err
		}); err != nil {
			t.Fatalf("existing metadata read: %v", err)
		}
	}
}

func submissionProbeWriteDelta(before, after idleClaimMetricSnapshot) (uint64, error) {
	if !before.writePresent || !after.writePresent || after.write < before.write {
		return 0, errors.New("native write counter unavailable/decreased; not zero")
	}
	return after.write - before.write, nil
}

type submissionProbeMetadata struct {
	Namespaces map[string]string `json:"namespaces"`
	Databases  map[string]string `json:"databases"`
}

func submissionProbeInfo(ctx context.Context, t *testing.T, db *surrealdb.DB, statement string) submissionProbeMetadata {
	t.Helper()
	rows, err := surrealdb.Query[submissionProbeMetadata](ctx, db, statement, nil)
	if err != nil || rows == nil || len(*rows) != 1 {
		t.Fatalf("fixed metadata existence query: %v", err)
	}
	return (*rows)[0].Result
}

// Unlike the scoped idle-claim reader, this test must include root/unscoped
// transactions. The fresh private engine has only these two neutral namespaces.
func readSubmissionProbeMetrics(ctx context.Context, client *http.Client, endpoint, bearer string) (idleClaimMetricSnapshot, error) {
	var out idleClaimMetricSnapshot
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return out, err
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := client.Do(request)
	if err != nil {
		return out, err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return out, err
	}
	if response.StatusCode != http.StatusOK || len(body) > 1<<20 {
		return out, fmt.Errorf("metrics status=%d bytes=%d", response.StatusCode, len(body))
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	const metricName = "surrealdb_transaction_total"
	for line := range strings.SplitSeq(string(body), "\n") {
		if strings.HasPrefix(line, metricName+"{") {
			if len(out.raw)+len(line)+1 > 8<<10 {
				return out, errors.New("transaction output exceeds 8 KiB")
			}
			out.raw += line + "\n"
		}
	}
	family := families[metricName]
	if family == nil {
		return out, nil
	}
	seen := make(map[string]bool)
	for _, metric := range family.GetMetric() {
		if len(metric.GetLabel()) > 16 {
			return out, errors.New("too many native labels")
		}
		labels := make(map[string]string, len(metric.GetLabel()))
		identity := make([]string, 0, len(metric.GetLabel()))
		for _, label := range metric.GetLabel() {
			name, value := label.GetName(), label.GetValue()
			if len(name) > 64 || len(value) > 256 {
				return out, errors.New("native label too large")
			}
			if _, duplicate := labels[name]; duplicate {
				return out, errors.New("duplicate native label")
			}
			labels[name] = value
			identity = append(identity, strconv.Quote(name)+"="+strconv.Quote(value))
		}
		for _, scope := range []string{labels["namespace"], labels["database"]} {
			if scope != "" && scope != "-" && scope != submissionProbeScope && scope != submissionProbeControl {
				return out, errors.New("nonneutral native scope")
			}
		}
		if (labels["user"] != "" && labels["user"] != "-" && labels["user"] != "root" && labels["user"] != "system_auth") || metric.Counter == nil ||
			(labels["write"] != "true" && labels["write"] != "false") ||
			(labels["outcome"] != "success" && labels["outcome"] != "error" && labels["outcome"] != "canceled") {
			return out, errors.New("unsupported native counter")
		}
		slices.Sort(identity)
		key := strings.Join(identity, "\n")
		if seen[key] {
			return out, errors.New("duplicate native series")
		}
		seen[key] = true
		value := metric.GetCounter().GetValue()
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= 1<<53 || math.Trunc(value) != value {
			return out, errors.New("invalid exact native counter")
		}
		counter := &out.read
		if labels["write"] == "true" {
			counter, out.writePresent = &out.write, true
		} else {
			out.readPresent = true
		}
		if ^uint64(0)-*counter < uint64(value) {
			return out, errors.New("native counter overflow")
		}
		*counter += uint64(value)
	}
	return out, nil
}

func TestSubmissionProbeMetricsReusesBearer(t *testing.T) {
	const bearer = "neutral-test-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+bearer {
			t.Error("metrics request did not preserve the supplied bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `# TYPE surrealdb_transaction_total counter
surrealdb_transaction_total{namespace="-",database="-",user="root",write="false",outcome="success"} 2
surrealdb_transaction_total{namespace="-",database="-",user="system_auth",write="true",outcome="success"} 3
`)
	}))
	defer server.Close()
	snapshot, err := readSubmissionProbeMetrics(t.Context(), server.Client(), server.URL, bearer)
	if err != nil || !snapshot.readPresent || !snapshot.writePresent || snapshot.read != 2 || snapshot.write != 3 {
		t.Fatalf("bearer parser/control fixture: %+v, %v", snapshot, err)
	}
}

func TestSubmissionProbeWriteOnlyDelta(t *testing.T) {
	for _, test := range []struct {
		name          string
		before, after idleClaimMetricSnapshot
		want          uint64
		invalid       bool
	}{
		{"scrape_reads_unattributed", idleClaimMetricSnapshot{read: 9, write: 14, readPresent: true, writePresent: true}, idleClaimMetricSnapshot{read: 10, write: 14, readPresent: true, writePresent: true}, 0, false},
		{"positive_write", idleClaimMetricSnapshot{read: 9, write: 14, readPresent: true, writePresent: true}, idleClaimMetricSnapshot{read: 12, write: 15, readPresent: true, writePresent: true}, 1, false},
		{"missing_before", idleClaimMetricSnapshot{}, idleClaimMetricSnapshot{write: 14, writePresent: true}, 0, true},
		{"missing_after", idleClaimMetricSnapshot{write: 14, writePresent: true}, idleClaimMetricSnapshot{}, 0, true},
		{"decreased", idleClaimMetricSnapshot{write: 14, writePresent: true}, idleClaimMetricSnapshot{write: 13, writePresent: true}, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := submissionProbeWriteDelta(test.before, test.after)
			if (err != nil) != test.invalid || got != test.want {
				t.Fatalf("write delta=%d, error=%v; want %d, invalid=%t", got, err, test.want, test.invalid)
			}
		})
	}
}
