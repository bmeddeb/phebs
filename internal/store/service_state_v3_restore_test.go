package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

// This observes the concrete native submission, not affected rows or native KV
// transaction metrics. The three source-owned statements have exact vectors.
type restoreStateV3TestConnection struct {
	connection.Connection
	writes   []int
	before   func(context.Context, string, map[string]any) error
	after    func() error
	response *cbor.RawMessage
}

func (conn *restoreStateV3TestConnection) Send(ctx context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	if conn.response != nil {
		return &connection.RPCResponse[cbor.RawMessage]{Result: conn.response}, nil
	}
	write := false
	if method == "query" && len(params) == 2 {
		statement, _ := params[0].(string)
		vars, _ := params[1].(map[string]any)
		rows := 0
		switch statement {
		case restoreStateV3DeleteFutureSQL:
			rows = len(vars["future_ids"].([]models.RecordID))
		case restoreStateV3PairsSQL:
			rows = 2 * len(vars["updates"].([]map[string]any))
		case restoreStateV3SummarySQL:
			rows = 1 + len(vars["summary_preimage_ids"].([]models.RecordID))
		default:
			if strings.HasPrefix(statement, "BEGIN;") {
				// The only other writes on this observed connection are the
				// existing closed exact-ID unselected-table recipe.
				rows = len(vars["ids"].([]models.RecordID))
			}
		}
		if rows != 0 {
			if rows > 512 {
				return nil, fmt.Errorf("restore submitted %d records", rows)
			}
			write = true
			conn.writes = append(conn.writes, rows)
			if conn.before != nil {
				if err := conn.before(ctx, statement, vars); err != nil {
					return nil, err
				}
			}
		}
	}
	response, err := conn.Connection.Send(ctx, method, params...)
	if err == nil && write && conn.after != nil {
		if err := conn.after(); err != nil {
			return nil, err
		}
	}
	return response, err
}

func restoreStateV3PureFixture(t *testing.T, count int) (ServiceRuntimeSelector, servicecatalog.RepositoryState, []restoreClearStep) {
	t.Helper()
	plan, changes, summary := serviceStateV3HeadroomFixture(t, serviceStateV3Activate, count)
	selector := ServiceRuntimeSelector{
		Schema: ServiceRuntimeSelectorSchema, Repository: summary.Repository, Backend: ServiceRuntimeV3,
		CatalogRootDigest: plan.CatalogRoot, CatalogControlRevision: 1,
		StateControlRevision: summary.ControlRevision, StateSummaryDigest: summary.SummaryDigest,
		SearchGenerationDigest:       plan.SearchGeneration,
		RelationshipGenerationDigest: selectorTestDigest("c"), RelationshipRootDigest: selectorTestDigest("d"),
		ControlRevision: 1, ChangedAt: summary.UpdatedAt,
	}
	selector.Digest = serviceRuntimeSelectorDigest(selector)
	preimages, future := make([]map[string]any, 0, count), make([]map[string]any, 0, count)
	for _, change := range changes {
		state := change.update.State
		content := serviceStateContent(state)
		content["id"] = models.NewRecordID("service_state_v3_preimage", state.ServiceKey)
		content["visible_from"], content["snapshot_revision"], content["snapshot_digest"] = uint64(1), summary.ControlRevision, summary.SummaryDigest
		preimages = append(preimages, content)
		future = append(future, map[string]any{
			"id": serviceStateV3ID(summary.Repository, state.ServiceKey), "repository": summary.Repository,
			"service_key": state.ServiceKey, "control_revision": uint64(2), "state_digest": state.StateDigest,
			"visible_from": summary.ControlRevision + 1,
		})
	}
	savedSummary := serviceRepositoryStateContent(*summary)
	savedSummary["id"] = models.NewRecordID("service_state_v3_repository_preimage", "prior")
	savedSummary["snapshot_revision"], savedSummary["snapshot_digest"] = summary.ControlRevision, summary.SummaryDigest
	return selector, *summary, []restoreClearStep{
		{contains: "FROM service_state_v3_repository_preimage", rows: []any{savedSummary}},
		{contains: "FROM service_state_v3_preimage", rows: preimages},
		{contains: "FROM service_state_v3_current", rows: future},
		{contains: "FROM $summary_rid", rows: []any{}},
	}
}

func TestRestoreSelectedStateV3PrevalidationAndEmpty(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func([]restoreClearStep)
	}{
		{"invalid_last_preimage", func(steps []restoreClearStep) { steps[1].rows.([]map[string]any)[256]["state_digest"] = "bad" }},
		{"duplicate_key", func(steps []restoreClearStep) {
			rows := steps[1].rows.([]map[string]any)
			rows[256] = rows[255]
		}},
		{"missing_future_target", func(steps []restoreClearStep) { steps[2].rows = steps[2].rows.([]map[string]any)[1:] }},
		{"wrong_future_native_id", func(steps []restoreClearStep) {
			steps[2].rows.([]map[string]any)[0]["id"] = models.NewRecordID("service_state_v3_current", "wrong")
		}},
		{"invalid_summary", func(steps []restoreClearStep) {
			steps[0].rows.([]any)[0].(map[string]any)["snapshot_revision"] = uint64(99)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			selector, summary, steps := restoreStateV3PureFixture(t, 257)
			test.edit(steps)
			s, conn := restoreClearScript(t, steps[:3], nil)
			if err := s.restoreSelectedServiceStateV3Snapshot(t.Context(), selector, summary); !errors.Is(err, ErrInvalidServiceStateV3) || conn.calls != 3 {
				t.Fatalf("prevalidation calls=%d error=%v", conn.calls, err)
			}
		})
	}
	selector, summary, steps := restoreStateV3PureFixture(t, 1)
	for index := range steps[:3] {
		steps[index].rows = []any{}
	}
	s, conn := restoreClearScript(t, steps[:3], nil)
	if err := s.restoreSelectedServiceStateV3Snapshot(t.Context(), selector, summary); err != nil || conn.calls != 3 {
		t.Fatalf("empty restore calls=%d error=%v", conn.calls, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.restoreSelectedServiceStateV3Snapshot(ctx, selector, summary); !errors.Is(err, context.Canceled) || conn.calls != 3 {
		t.Fatalf("canceled restore calls=%d error=%v", conn.calls, err)
	}
}

func TestRestoreSelectedStateV3ExactPayloadPages(t *testing.T) {
	for _, count := range []int{1, 256, 257, 512} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			selector, summary, steps := restoreStateV3PureFixture(t, count)
			want := []int{count}
			steps = append(steps, restoreClearStep{contains: restoreStateV3DeleteFutureSQL})
			for left := count; left > 0; left -= 256 {
				want = append(want, 2*min(left, 256))
				steps = append(steps, restoreClearStep{contains: restoreStateV3PairsSQL})
			}
			want = append(want, 2)
			steps = append(steps, restoreClearStep{contains: restoreStateV3SummarySQL})
			_, script := restoreClearScript(t, steps, nil)
			conn := &restoreStateV3TestConnection{Connection: script}
			db, err := surrealdb.FromConnection(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close(context.Background()) })
			s := &Surreal{db: db}
			if err := s.restoreSelectedServiceStateV3Snapshot(t.Context(), selector, summary); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(conn.writes, want) || script.calls != len(steps) {
				t.Fatalf("payloads=%v want=%v calls=%d", conn.writes, want, script.calls)
			}
		})
	}
}

func TestRestoreSelectedStateV3CensusCaps(t *testing.T) {
	for index := range 3 {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			selector, summary, steps := restoreStateV3PureFixture(t, 1)
			maximum := servicecatalogv3.MaxTotalServices * 2
			if index == 0 {
				maximum = 1
			}
			steps[index].rows = make([]any, maximum+1)
			s, conn := restoreClearScript(t, steps[:index+1], nil)
			if err := s.restoreSelectedServiceStateV3Snapshot(t.Context(), selector, summary); !errors.Is(err, ErrInvalidServiceStateV3) || conn.calls != index+1 {
				t.Fatalf("census overflow calls=%d error=%v", conn.calls, err)
			}
		})
	}
}

func TestDiscardUnselectedStateV3UsesExactClearPages(t *testing.T) {
	const repository = "example.com/acme/unselected"
	steps := []restoreClearStep{
		{contains: "SELECT repository, service_key", rows: []map[string]any{{"repository": repository, "service_key": "a"}}},
		{contains: "SELECT repository FROM service_state_v3_repository", rows: []any{}},
	}
	for _, table := range []string{
		"service_state_v3_current", "service_state_v3_repository",
		"service_state_v3_preimage", "service_state_v3_repository_preimage",
	} {
		// Raw restore retains native composite-ID compatibility; no state DTO
		// is used to invent a target from the repository/service-key census.
		ids := []models.RecordID{models.NewRecordID(table, []any{repository, "one"})}
		steps = append(steps,
			restoreClearStep{contains: "backend = 'v3'", rows: ids},
			restoreClearStep{contains: "FOR $rid IN $ids", ids: ids},
			restoreClearStep{contains: "backend = 'v3'", rows: []models.RecordID{}},
		)
	}
	s, conn := restoreClearScript(t, steps, nil)
	if err := s.discardUnselectedServiceStateV3ForRestore(t.Context(), nil); err != nil || conn.calls != len(steps) {
		t.Fatalf("unselected clear calls=%d error=%v", conn.calls, err)
	}
}

func TestRestoreSelectedStateV3CensusEnvelope(t *testing.T) {
	for _, test := range []struct {
		name    string
		results any
		valid   bool
	}{
		{"nil", nil, false},
		{"missing", []any{}, false},
		{"multiple", []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{}}, {Status: "OK", Result: []any{}}}, false},
		{"unknown_status", []surrealdb.QueryResult[any]{{Status: "unknown", Result: []any{}}}, false},
		{"error_status", []surrealdb.QueryResult[any]{{Status: "ERR", Result: "failed"}}, false},
		{"null_result", []surrealdb.QueryResult[any]{{Status: "OK", Result: nil}}, false},
		{"actual_empty", []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{}}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, script := restoreClearScript(t, nil, nil)
			raw := restoreClearRaw(t, test.results)
			conn := &restoreStateV3TestConnection{Connection: script, response: &raw}
			db, err := surrealdb.FromConnection(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close(context.Background()) })
			s := &Surreal{db: db}
			rows, err := s.restoreStateV3RawRows(t.Context(), "SELECT id FROM neutral", nil, 1)
			if (err == nil) != test.valid || len(rows) != 0 || len(conn.writes) != 0 {
				t.Fatalf("census envelope accepted=%v want=%v error=%v", err == nil, test.valid, err)
			}
		})
	}
}

func restoreStateV3NativeObserved(ctx context.Context, t *testing.T, dataDir string) (*Surreal, *restoreStateV3TestConnection) {
	t.Helper()
	runtime, err := ReadLocalRuntime(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.ParseRequestURI(runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	conn := &restoreStateV3TestConnection{Connection: gorillaws.New(connection.NewConfig(endpoint))}
	db, err := surrealdb.FromConnection(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := db.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Use(ctx, "phebs", "phebs"); err != nil {
		t.Fatal(err)
	}
	return &Surreal{db: db}, conn
}

func restoreStateV3NativeSelected(ctx context.Context, t *testing.T, s *Surreal, suffix string, count, added int) (ServiceRuntimeSelector, servicecatalog.RepositoryState) {
	t.Helper()
	repository, commit := "example.com/acme/restore-"+suffix, strings.Repeat("7", 40)
	seedServiceCatalogV3RepoContext(ctx, t, s, repository, commit)
	services := serviceStateV3HeadroomServices(count)
	generation := serviceStateV3Generation(t, repository, commit, "a", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3PlanContext(ctx, t, s, reconcile)
	search := selectorTestDigest("8")
	activation, err := s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3PlanContext(ctx, t, s, activation)
	pointer, err := s.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	reference := ServiceCatalogV3RelationshipReference{
		Repository: repository, RelationshipGenerationDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(repository))),
		RelationshipRootDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(repository+"/root"))), CatalogRootDigest: pointer.RootDigest,
		CatalogControlRevision: pointer.ControlRevision, StateControlRevision: summary.ControlRevision,
		StateSummaryDigest: summary.SummaryDigest,
	}
	if err := s.PinServiceCatalogV3RelationshipReference(ctx, reference); err != nil {
		t.Fatal(err)
	}
	selected, err := s.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{
		Repository: repository,
		Target: ServiceRuntimeTarget{
			CatalogRootDigest: pointer.RootDigest, CatalogControlRevision: pointer.ControlRevision,
			StateControlRevision: summary.ControlRevision, StateSummaryDigest: summary.SummaryDigest,
			SearchGenerationDigest: search, RelationshipGenerationDigest: reference.RelationshipGenerationDigest,
			RelationshipRootDigest: reference.RelationshipRootDigest,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := range services {
		services[index].DisplayName += " B"
	}
	for index := range added {
		services = append(services, servicecatalog.Service{
			Key: fmt.Sprintf("zz-added-%04d", index), DisplayName: "Added",
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		})
	}
	generation = serviceStateV3Generation(t, repository, commit, "b", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	reconcile, err = s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3PlanContext(ctx, t, s, reconcile)
	return selected, summary
}

func TestRestoreSelectedStateV3NativePagesAndFences(t *testing.T) {
	deadline := time.Now().Add(4 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	s, err := OpenLocalMemory(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer closeCancel()
		if err := s.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	observed, conn := restoreStateV3NativeObserved(ctx, t, dataDir)
	query := func(statement string, vars map[string]any) {
		t.Helper()
		if _, err := surrealdb.Query[any](ctx, s.db, statement, vars); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("genuine_selected_300", func(t *testing.T) {
		selector, summary := restoreStateV3NativeSelected(ctx, t, s, "pages", 300, 213)
		before, err := s.GetServiceStateV3PointSnapshot(ctx, selector.Repository, "service-0000", selector.StateControlRevision, selector.StateSummaryDigest)
		if err != nil {
			t.Fatal(err)
		}
		if err := observed.RestoreSelectedServiceStateV3ForRestore(ctx); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(conn.writes, []int{512, 1, 512, 88, 2}) {
			t.Fatalf("actual submitted payloads=%v", conn.writes)
		}
		after, err := s.GetServiceStateV3Point(ctx, selector.Repository, before.ServiceKey)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("restored state differs: %+v, %v", after, err)
		}
		got, err := s.GetServiceStateV3SummaryPoint(ctx, selector.Repository)
		if err != nil || !sameServiceStateV3Summary(got, summary) {
			t.Fatalf("restored summary differs: %+v, %v", got, err)
		}
		if err := observed.restoreSelectedServiceStateV3Snapshot(ctx, selector, summary); err != nil || len(conn.writes) != 5 {
			t.Fatalf("settled restore wrote: %v %v", conn.writes, err)
		}
	})
	for _, kind := range []string{"selector", "future", "preimage", "target", "summary", "cancel", "lost_reply"} {
		t.Run(kind, func(t *testing.T) {
			selector, summary := restoreStateV3NativeSelected(ctx, t, s, kind, 1, 0)
			conn.writes, conn.before, conn.after = nil, nil, nil
			localCtx, localCancel := context.WithCancel(ctx)
			defer localCancel()
			injected := errors.New("injected lost native reply")
			if kind == "cancel" || kind == "lost_reply" {
				conn.after = func() error {
					conn.after = nil
					if kind == "cancel" {
						localCancel()
						return nil
					}
					return injected
				}
			} else {
				conn.before = func(_ context.Context, statement string, vars map[string]any) error {
					if (kind == "preimage" || kind == "target") && statement != restoreStateV3PairsSQL {
						return nil
					}
					if kind == "summary" && statement != restoreStateV3SummarySQL {
						return nil
					}
					conn.before = nil
					switch kind {
					case "selector":
						query("UPDATE $rid SET control_revision += 1;", map[string]any{"rid": vars["selector_rid"]})
					case "future":
						query("UPDATE $rid SET control_revision += 1;", map[string]any{"rid": vars["future_ids"].([]models.RecordID)[0]})
					case "preimage":
						update := vars["updates"].([]map[string]any)[0]
						query("UPDATE $rid SET reason = 'changed';", map[string]any{"rid": update["preimage_rid"]})
					case "target":
						update := vars["updates"].([]map[string]any)[0]
						query("UPSERT $rid CONTENT $content;", map[string]any{"rid": update["rid"], "content": update["content"]})
					case "summary":
						query("UPDATE $rid SET control_revision += 1;", map[string]any{"rid": vars["summary_rid"]})
					}
					return nil
				}
			}
			err := observed.restoreSelectedServiceStateV3Snapshot(localCtx, selector, summary)
			if err == nil || len(conn.writes) == 0 {
				t.Fatalf("missing retained-prefix refusal: writes=%v error=%v", conn.writes, err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) || kind == "lost_reply" && !errors.Is(err, injected) {
				t.Fatalf("wrong terminal failure: %v", err)
			}
			if kind == "cancel" || kind == "lost_reply" {
				current, currentErr := surrealdb.Query[[]models.RecordID](ctx, s.db,
					"SELECT VALUE id FROM service_state_v3_current WHERE repository = $repository;",
					map[string]any{"repository": selector.Repository})
				preimages, preimageErr := surrealdb.Query[[]models.RecordID](ctx, s.db,
					"SELECT VALUE id FROM service_state_v3_preimage WHERE repository = $repository;",
					map[string]any{"repository": selector.Repository})
				if currentErr != nil || preimageErr != nil || len(firstDomainRows(current)) != 0 ||
					len(firstDomainRows(preimages)) != 1 || !slices.Equal(conn.writes, []int{1}) {
					t.Fatalf("committed delete prefix not retained: writes=%v errors=%v,%v", conn.writes, currentErr, preimageErr)
				}
			}
			// A failed target remains observable, never silently cleared or
			// reimported. Its saved summary preimage is not consumed on failure.
			rows, readErr := surrealdb.Query[[]models.RecordID](ctx, s.db,
				"SELECT VALUE id FROM service_state_v3_repository_preimage WHERE repository = $repository;",
				map[string]any{"repository": selector.Repository})
			if readErr != nil || len(firstDomainRows(rows)) != 1 {
				t.Fatalf("failed target lost summary custody: %v", readErr)
			}
		})
	}
}
