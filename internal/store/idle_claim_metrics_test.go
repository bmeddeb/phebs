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

const idleClaimMetricsScope = "phebs_idle_claim_diagnostic"

// This opt-in diagnostic observes the pinned engine's completed transaction
// counters. It is not an attempted-prefix meter or a whole-phase budget proof.
func TestIdleClaimNativeTransactionMetrics(t *testing.T) {
	if os.Getenv("PHEBS_TEST_IDLE_CLAIM_METRICS") != "1" {
		t.Skip("set PHEBS_TEST_IDLE_CLAIM_METRICS=1 for the serialized native diagnostic")
	}
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-30*time.Second).Before(deadline) {
		deadline = outer.Add(-30 * time.Second)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	runtime, stop, err := startEngine(ctx, "memory")
	if err != nil {
		t.Fatalf("native start unavailable (error type %T)", err)
	}
	defer stop()
	if runtime.Surreal.Version != "3.2.0" {
		t.Fatalf("unexpected native version %q", runtime.Surreal.Version)
	}
	s, err := Open(ctx, runtime.Endpoint, "root", "root", idleClaimMetricsScope, idleClaimMetricsScope)
	if err != nil {
		t.Fatalf("open neutral store: %v", err)
	}
	defer func() {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		if err := s.Close(cleanup); err != nil {
			t.Errorf("close neutral SDK: %v", err)
		}
	}()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := "http://" + strings.TrimPrefix(runtime.Endpoint, "ws://") + "/metrics"
	snapshot := func(stage string) idleClaimMetricSnapshot {
		t.Helper()
		value, err := readIdleClaimMetrics(ctx, client, endpoint)
		if err != nil {
			t.Fatalf("%s metrics unavailable: %v\n%s", stage, err, value.raw)
		}
		if !value.readPresent || !value.writePresent {
			t.Skipf("%s metrics unavailable: scoped read/write counter presence=%t/%t; not zero", stage, value.readPresent, value.writePresent)
		}
		t.Logf("%s read=%d write=%d\n%s", stage, value.read, value.write, value.raw)
		return value
	}
	before := snapshot("baseline")
	job, err := s.ClaimJob(ctx, JobSync, "neutral-idle-claim")
	t.Logf("empty ClaimJob result_nil=%t error=%v", job == nil, err)
	if !errors.Is(err, ErrNotFound) || job != nil {
		t.Fatalf("unexpected empty claim: job_present=%t error=%v", job != nil, err)
	}
	afterEmpty := snapshot("after_empty_claim")
	if _, err := surrealdb.Query[any](ctx, s.db,
		"SELECT id FROM type::table($table) LIMIT 1", map[string]any{"table": string(JobSync)}); err != nil {
		t.Fatalf("SELECT-only control: %v", err)
	}
	afterSelect := snapshot("after_select_only")
	created, err := s.CreateJob(ctx, JobSync, "neutral-target")
	if err != nil || created == nil {
		t.Fatalf("create control job: result_nil=%t error=%v", created == nil, err)
	}
	afterCreate := snapshot("after_create_setup")
	claimed, err := s.ClaimJob(ctx, JobSync, "neutral-populated-claim")
	t.Logf("populated ClaimJob result_present=%t error=%v", claimed != nil, err)
	if err != nil || claimed == nil || claimed.ID != created.ID || claimed.Status != StatusClaimed {
		t.Fatalf("unexpected populated claim: result_present=%t error=%v", claimed != nil, err)
	}
	afterPopulated := snapshot("after_populated_claim")
	for _, step := range []struct {
		name          string
		before, after idleClaimMetricSnapshot
		reads, writes uint64
	}{
		{"empty_claim", before, afterEmpty, 1, 0},
		{"select_only", afterEmpty, afterSelect, 1, 0},
		{"create_setup", afterSelect, afterCreate, 0, 1},
		{"populated_claim", afterCreate, afterPopulated, 1, 1},
	} {
		if step.after.read < step.before.read || step.after.write < step.before.write {
			t.Fatalf("%s counter decreased; measurement unavailable", step.name)
		}
		t.Logf("%s delta_read=%d delta_write=%d", step.name,
			step.after.read-step.before.read, step.after.write-step.before.write)
		if step.after.read-step.before.read != step.reads || step.after.write-step.before.write != step.writes {
			t.Errorf("%s native transaction deltas differ from read=%d write=%d", step.name, step.reads, step.writes)
		}
	}
}

type idleClaimMetricSnapshot struct {
	read, write               uint64
	readPresent, writePresent bool
	raw                       string
}

func readIdleClaimMetrics(ctx context.Context, client *http.Client, endpoint string) (idleClaimMetricSnapshot, error) {
	var out idleClaimMetricSnapshot
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return out, err
	}
	request.SetBasicAuth("root", "root")
	response, err := client.Do(request)
	if err != nil {
		return out, err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return out, err
	}
	if response.StatusCode != http.StatusOK || len(body) > 1<<20 {
		return out, fmt.Errorf("HTTP status=%d bytes=%d", response.StatusCode, len(body))
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return out, fmt.Errorf("parse metrics: %w", err)
	}
	const name = "surrealdb_transaction_total"
	for line := range strings.SplitSeq(string(body), "\n") {
		if strings.HasPrefix(line, name+"{") &&
			strings.Contains(line, `namespace="`+idleClaimMetricsScope+`"`) &&
			strings.Contains(line, `database="`+idleClaimMetricsScope+`"`) {
			if len(out.raw)+len(line)+1 > 8<<10 {
				return out, errors.New("scoped transaction output exceeds 8 KiB")
			}
			out.raw += line + "\n"
		}
	}
	family := families[name]
	if family == nil {
		return out, nil
	}
	seen := make(map[string]bool)
	for _, metric := range family.GetMetric() {
		if len(metric.GetLabel()) > 16 {
			return out, errors.New("transaction label count exceeds diagnostic cap")
		}
		labels := make(map[string]string, len(metric.GetLabel()))
		identity := make([]string, 0, len(metric.GetLabel()))
		for _, label := range metric.GetLabel() {
			if len(label.GetName()) > 64 || len(label.GetValue()) > 256 {
				return out, errors.New("transaction label exceeds diagnostic byte cap")
			}
			if _, duplicate := labels[label.GetName()]; duplicate {
				return out, errors.New("duplicate transaction label")
			}
			labels[label.GetName()] = label.GetValue()
			identity = append(identity, strconv.Quote(label.GetName())+"="+strconv.Quote(label.GetValue()))
		}
		if labels["namespace"] != idleClaimMetricsScope || labels["database"] != idleClaimMetricsScope {
			continue
		}
		if labels["user"] != "root" || metric.Counter == nil ||
			(labels["write"] != "true" && labels["write"] != "false") ||
			(labels["outcome"] != "success" && labels["outcome"] != "error" && labels["outcome"] != "canceled") {
			return out, errors.New("unsupported scoped transaction metric")
		}
		// Native instrumentation metadata is diagnostic data, not a frozen
		// closed label schema. Preserve every pair in the series identity.
		slices.Sort(identity)
		key := strings.Join(identity, "\n")
		if seen[key] {
			return out, errors.New("duplicate scoped transaction series")
		}
		seen[key] = true
		value := metric.GetCounter().GetValue()
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= 1<<53 || math.Trunc(value) != value {
			return out, errors.New("invalid exact transaction counter")
		}
		if labels["write"] == "true" {
			if ^uint64(0)-out.write < uint64(value) {
				return out, errors.New("write counter overflow")
			}
			out.write += uint64(value)
			out.writePresent = true
		} else {
			if ^uint64(0)-out.read < uint64(value) {
				return out, errors.New("read counter overflow")
			}
			out.read += uint64(value)
			out.readPresent = true
		}
	}
	return out, nil
}

func TestIdleClaimMetricsScopeAndUnavailable(t *testing.T) {
	const prefix = "# TYPE surrealdb_transaction_total counter\n"
	row := func(scope, write, value string) string {
		return `surrealdb_transaction_total{namespace="` + scope + `",database="` + idleClaimMetricsScope +
			`",user="root",write="` + write + `",outcome="success"} ` + value + "\n"
	}
	for _, test := range []struct {
		name, body                string
		read, write               uint64
		readPresent, writePresent bool
		wantError                 bool
	}{
		{"scoped", prefix + row(idleClaimMetricsScope, "false", "2") + row(idleClaimMetricsScope, "true", "3") + row("unrelated", "true", "100"), 2, 3, true, true, false},
		{"absent", "# no registered transaction family\n", 0, 0, false, false, false},
		{"duplicate", prefix + strings.Repeat(row(idleClaimMetricsScope, "true", "1"), 2), 0, 0, false, false, true},
		{"extra metadata", prefix + strings.Replace(row(idleClaimMetricsScope, "true", "4"), `outcome="success"`, `outcome="success",otel_scope_name="neutral_scope"`, 1), 0, 4, false, true, false},
		{"reordered duplicate", prefix + row(idleClaimMetricsScope, "true", "1") + strings.Replace(row(idleClaimMetricsScope, "true", "1"), `user="root",write="true"`, `write="true",user="root"`, 1), 0, 0, false, false, true},
		{"fraction", prefix + row(idleClaimMetricsScope, "true", "0.5"), 0, 0, false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, pass, ok := r.BasicAuth()
				if !ok || user != "root" || pass != "root" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			got, err := readIdleClaimMetrics(t.Context(), server.Client(), server.URL)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v", err)
			}
			if err == nil && (got.read != test.read || got.write != test.write ||
				got.readPresent != test.readPresent || got.writePresent != test.writePresent || strings.Contains(got.raw, "unrelated")) {
				t.Fatalf("scope/presence mismatch: %+v", got)
			}
		})
	}
}
