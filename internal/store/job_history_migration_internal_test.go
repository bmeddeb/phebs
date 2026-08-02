package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const jobMigrationTerminalRowsPerKind = 96

type jobMigrationRowProbe struct {
	RecID      *models.RecordID `json:"id"`
	Target     string           `json:"target"`
	Status     JobStatus        `json:"status"`
	Attempts   int              `json:"attempts"`
	Error      string           `json:"error"`
	PendingKey any              `json:"pending_key"`
	ClaimedBy  any              `json:"claimed_by"`
	ClaimedAt  any              `json:"claimed_at"`
	Heartbeat  any              `json:"heartbeat_at"`
	FinishedAt any              `json:"finished_at"`
	LeaseToken any              `json:"lease_token"`
}

type jobMigrationMarkerProbe struct {
	Version     string    `json:"version"`
	CompletedAt time.Time `json:"completed_at"`
}

func newJobHistoryMigrationStore(t *testing.T) *Surreal {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	s, err := OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenLocalMemory: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Errorf("close migration store: %v", err)
		}
	})
	return s
}

func requireJobMigrationQuery[T any](
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	statement string,
	vars map[string]any,
) []surrealdb.QueryResult[T] {
	t.Helper()
	results, err := surrealdb.Query[T](ctx, s.db, statement, vars)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf("query statement %d: %s", index, result.Error.Message)
		}
	}
	return *results
}

func jobMigrationRecordID(kind JobKind, suffix string) models.RecordID {
	return models.NewRecordID(string(kind), "t306n-"+suffix)
}

func readJobMigrationRow(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	rid models.RecordID,
) jobMigrationRowProbe {
	t.Helper()
	results := requireJobMigrationQuery[[]jobMigrationRowProbe](
		t, ctx, s, "SELECT * FROM $rid LIMIT 1", map[string]any{"rid": rid},
	)
	for _, result := range results {
		if len(result.Result) == 1 {
			return result.Result[0]
		}
	}
	t.Fatalf("job migration row %s is missing", rid.String())
	return jobMigrationRowProbe{}
}

func readJobMigrationMarker(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
) (jobMigrationMarkerProbe, bool) {
	t.Helper()
	results := requireJobMigrationQuery[[]jobMigrationMarkerProbe](
		t, ctx, s,
		"SELECT version, completed_at FROM $marker LIMIT 1",
		map[string]any{"marker": jobActiveMigrationID()},
	)
	for _, result := range results {
		if len(result.Result) == 1 {
			return result.Result[0], true
		}
	}
	return jobMigrationMarkerProbe{}, false
}

func terminalJobHistoryCanonical(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	kind JobKind,
) ([]byte, int) {
	t.Helper()
	statement := fmt.Sprintf(`SELECT * FROM %s WITH INDEX %s_status
		WHERE status IN ['done', 'failed', 'canceled']
			AND target = $target ORDER BY id`, kind, kind)
	results := requireJobMigrationQuery[[]map[string]any](
		t, ctx, s, statement,
		map[string]any{"target": string(kind) + "/terminal"},
	)
	rows := make([]map[string]any, 0, jobMigrationTerminalRowsPerKind)
	for _, result := range results {
		rows = append(rows, result.Result...)
	}
	canonical, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal %s terminal rows: %v", kind, err)
	}
	return canonical, len(rows)
}

func seedJobMigrationRows(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	kind JobKind,
) {
	t.Helper()
	base := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	targetPrefix := string(kind) + "/"
	requireJobMigrationQuery[any](t, ctx, s, `
CREATE $pending_keep CONTENT {
	target: $duplicate_target, status: 'pending', attempts: 0,
	created_at: $pending_keep_at
};
CREATE $pending_duplicate CONTENT {
	target: $duplicate_target, status: 'pending', attempts: 0,
	created_at: $pending_duplicate_at
};
CREATE $active_requeue CONTENT {
	target: $requeue_target, status: 'running', attempts: 2,
	created_at: $active_requeue_at, claimed_by: 'legacy-worker',
	claimed_at: $active_requeue_at, heartbeat_at: $active_requeue_at
};
CREATE $pending_successor CONTENT {
	target: $successor_target, status: 'pending', attempts: 0,
	created_at: $pending_successor_at
};
CREATE $active_superseded CONTENT {
	target: $successor_target, status: 'claimed', attempts: 3,
	created_at: $active_superseded_at, claimed_by: 'legacy-worker',
	claimed_at: $active_superseded_at, heartbeat_at: $active_superseded_at
};
CREATE $active_leased CONTENT {
	target: $leased_target, status: 'running', attempts: 4,
	created_at: $active_leased_at, claimed_by: 'current-worker',
	claimed_at: $active_leased_at, heartbeat_at: $active_leased_at,
	lease_token: $lease
};`, map[string]any{
		"pending_keep":         jobMigrationRecordID(kind, "pending-keep"),
		"pending_duplicate":    jobMigrationRecordID(kind, "pending-duplicate"),
		"active_requeue":       jobMigrationRecordID(kind, "active-requeue"),
		"pending_successor":    jobMigrationRecordID(kind, "pending-successor"),
		"active_superseded":    jobMigrationRecordID(kind, "active-superseded"),
		"active_leased":        jobMigrationRecordID(kind, "active-leased"),
		"duplicate_target":     targetPrefix + "duplicate",
		"requeue_target":       targetPrefix + "requeue",
		"successor_target":     targetPrefix + "successor",
		"leased_target":        targetPrefix + "leased",
		"pending_keep_at":      base,
		"pending_duplicate_at": base.Add(time.Second),
		"active_requeue_at":    base.Add(2 * time.Second),
		"active_superseded_at": base.Add(3 * time.Second),
		"pending_successor_at": base.Add(4 * time.Second),
		"active_leased_at":     base.Add(5 * time.Second),
		"lease":                "0123456789abcdef0123456789abcdef",
	})

	terminalRows := make([]map[string]any, 0, jobMigrationTerminalRowsPerKind)
	terminalStatuses := [...]JobStatus{StatusDone, StatusFailed, StatusCanceled}
	for ordinal := range jobMigrationTerminalRowsPerKind {
		status := terminalStatuses[ordinal%len(terminalStatuses)]
		terminalRows = append(terminalRows, map[string]any{
			"status": string(status), "ordinal": ordinal,
			"error": fmt.Sprintf("retained diagnostic %s %d", kind, ordinal),
		})
	}
	requireJobMigrationQuery[any](t, ctx, s, `
FOR $row IN $rows {
	CREATE type::table($table) CONTENT {
		target: $terminal_target,
		status: $row.status,
		attempts: $row.ordinal,
		error: $row.error,
		created_at: $created_at,
		finished_at: $finished_at,
		diagnostic: {
			ordinal: $row.ordinal,
			owner: 'retained-terminal-history',
			bytes: <bytes> $row.error
		}
	} RETURN NONE
};`, map[string]any{
		"table":           string(kind),
		"rows":            terminalRows,
		"terminal_target": targetPrefix + "terminal",
		"created_at":      base.Add(-24 * time.Hour),
		"finished_at":     base.Add(-23 * time.Hour),
	})
}

func assertJobMigrationRowState(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	kind JobKind,
) {
	t.Helper()
	targetPrefix := string(kind) + "/"

	keeper := readJobMigrationRow(
		t, ctx, s, jobMigrationRecordID(kind, "pending-keep"),
	)
	if keeper.Status != StatusPending || keeper.PendingKey != targetPrefix+"duplicate" ||
		keeper.Attempts != 0 {
		t.Fatalf("%s pending keeper = %+v", kind, keeper)
	}
	duplicate := readJobMigrationRow(
		t, ctx, s, jobMigrationRecordID(kind, "pending-duplicate"),
	)
	if duplicate.Status != StatusCanceled || duplicate.PendingKey != nil ||
		duplicate.Error != "migration: duplicate pending job" || duplicate.FinishedAt == nil {
		t.Fatalf("%s pending duplicate = %+v", kind, duplicate)
	}
	requeued := readJobMigrationRow(
		t, ctx, s, jobMigrationRecordID(kind, "active-requeue"),
	)
	if requeued.Status != StatusPending || requeued.Attempts != 3 ||
		requeued.PendingKey != targetPrefix+"requeue" ||
		requeued.Error != "requeued: legacy claim without lease" ||
		requeued.ClaimedBy != nil || requeued.ClaimedAt != nil ||
		requeued.Heartbeat != nil || requeued.LeaseToken != nil {
		t.Fatalf("%s requeued active = %+v", kind, requeued)
	}
	successor := readJobMigrationRow(
		t, ctx, s, jobMigrationRecordID(kind, "pending-successor"),
	)
	if successor.Status != StatusPending || successor.PendingKey != targetPrefix+"successor" {
		t.Fatalf("%s pending successor = %+v", kind, successor)
	}
	superseded := readJobMigrationRow(
		t, ctx, s, jobMigrationRecordID(kind, "active-superseded"),
	)
	if superseded.Status != StatusCanceled || superseded.Attempts != 4 ||
		superseded.Error != "migration: unfenced job superseded" ||
		superseded.PendingKey != nil || superseded.FinishedAt == nil ||
		superseded.ClaimedBy != nil || superseded.ClaimedAt != nil ||
		superseded.Heartbeat != nil || superseded.LeaseToken != nil {
		t.Fatalf("%s superseded active = %+v", kind, superseded)
	}
	leased := readJobMigrationRow(
		t, ctx, s, jobMigrationRecordID(kind, "active-leased"),
	)
	if leased.Status != StatusRunning || leased.Attempts != 4 ||
		leased.LeaseToken != "0123456789abcdef0123456789abcdef" ||
		leased.ClaimedBy != "current-worker" {
		t.Fatalf("%s leased active changed = %+v", kind, leased)
	}
}

func TestJobActiveMigrationAllKindsPreservesTerminalHistory(t *testing.T) {
	s := newJobHistoryMigrationStore(t)
	ctx := t.Context()

	before := make(map[JobKind][]byte, len(durableJobKinds))
	for _, kind := range durableJobKinds {
		seedJobMigrationRows(t, ctx, s, kind)
		canonical, count := terminalJobHistoryCanonical(t, ctx, s, kind)
		if count != jobMigrationTerminalRowsPerKind {
			t.Fatalf("%s terminal count before = %d, want %d",
				kind, count, jobMigrationTerminalRowsPerKind)
		}
		before[kind] = canonical
	}
	requireJobMigrationQuery[any](t, ctx, s,
		"DELETE $marker RETURN NONE", map[string]any{"marker": jobActiveMigrationID()})

	if err := s.migrateLegacyJobs(ctx); err != nil {
		t.Fatalf("migrate all active job kinds: %v", err)
	}
	marker, ok := readJobMigrationMarker(t, ctx, s)
	if !ok || marker.Version != jobActiveMigrationVersion || marker.CompletedAt.IsZero() {
		t.Fatalf("active-job migration marker = %+v, present=%t", marker, ok)
	}

	for _, kind := range durableJobKinds {
		assertJobMigrationRowState(t, ctx, s, kind)
		after, count := terminalJobHistoryCanonical(t, ctx, s, kind)
		if count != jobMigrationTerminalRowsPerKind {
			t.Fatalf("%s terminal count after = %d, want %d",
				kind, count, jobMigrationTerminalRowsPerKind)
		}
		if !bytes.Equal(before[kind], after) {
			t.Fatalf("%s terminal rows changed during active migration", kind)
		}
	}
}

func TestJobActiveMigrationMarkerSurvivesKVReopen(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	directory := t.TempDir()

	initial, err := OpenLocal(ctx, directory)
	if err != nil {
		t.Fatalf("open initial surrealkv store: %v", err)
	}
	initialClosed := false
	t.Cleanup(func() {
		if !initialClosed {
			_ = initial.Close(context.Background())
		}
	})
	seedJobMigrationRows(t, ctx, initial, JobSync)
	terminalBefore, terminalCount := terminalJobHistoryCanonical(
		t, ctx, initial, JobSync,
	)
	if terminalCount != jobMigrationTerminalRowsPerKind {
		t.Fatalf("terminal count before close = %d, want %d",
			terminalCount, jobMigrationTerminalRowsPerKind)
	}
	requireJobMigrationQuery[any](t, ctx, initial,
		"DELETE $marker RETURN NONE", map[string]any{"marker": jobActiveMigrationID()})
	if err := initial.Close(ctx); err != nil {
		t.Fatalf("close initial surrealkv store: %v", err)
	}
	initialClosed = true

	migrated, err := OpenLocal(ctx, directory)
	if err != nil {
		t.Fatalf("reopen surrealkv store for active-job migration: %v", err)
	}
	migratedClosed := false
	t.Cleanup(func() {
		if !migratedClosed {
			_ = migrated.Close(context.Background())
		}
	})
	assertJobMigrationRowState(t, ctx, migrated, JobSync)
	marker, ok := readJobMigrationMarker(t, ctx, migrated)
	if !ok || marker.Version != jobActiveMigrationVersion || marker.CompletedAt.IsZero() {
		t.Fatalf("persisted active-job migration marker = %+v, present=%t", marker, ok)
	}
	terminalAfterMigration, terminalCount := terminalJobHistoryCanonical(
		t, ctx, migrated, JobSync,
	)
	if terminalCount != jobMigrationTerminalRowsPerKind ||
		!bytes.Equal(terminalBefore, terminalAfterMigration) {
		t.Fatalf("terminal rows changed across migration reopen: count=%d", terminalCount)
	}

	postMarkerRID := jobMigrationRecordID(JobSync, "post-marker-kv-reopen")
	requireJobMigrationQuery[any](t, ctx, migrated, `CREATE $rid CONTENT {
		target: 'post-marker-kv-reopen', status: 'running', attempts: 7,
		created_at: time::now(), claimed_by: 'legacy-after-marker',
		claimed_at: time::now(), heartbeat_at: time::now()
	}`, map[string]any{"rid": postMarkerRID})
	if err := migrated.Close(ctx); err != nil {
		t.Fatalf("close migrated surrealkv store: %v", err)
	}
	migratedClosed = true

	reopened, err := OpenLocal(ctx, directory)
	if err != nil {
		t.Fatalf("second surrealkv reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(context.Background()); err != nil {
			t.Errorf("close twice-reopened surrealkv store: %v", err)
		}
	})
	reopenedMarker, ok := readJobMigrationMarker(t, ctx, reopened)
	if !ok || reopenedMarker.Version != marker.Version ||
		!reopenedMarker.CompletedAt.Equal(marker.CompletedAt) {
		t.Fatalf("migration marker changed across second reopen: before=%+v after=%+v present=%t",
			marker, reopenedMarker, ok)
	}
	assertJobMigrationRowState(t, ctx, reopened, JobSync)
	postMarker := readJobMigrationRow(t, ctx, reopened, postMarkerRID)
	if postMarker.Status != StatusRunning || postMarker.Attempts != 7 ||
		postMarker.PendingKey != nil || postMarker.ClaimedBy != "legacy-after-marker" {
		t.Fatalf("completed migration repeated after reopen: %+v", postMarker)
	}
	terminalAfterSecondReopen, terminalCount := terminalJobHistoryCanonical(
		t, ctx, reopened, JobSync,
	)
	if terminalCount != jobMigrationTerminalRowsPerKind ||
		!bytes.Equal(terminalBefore, terminalAfterSecondReopen) {
		t.Fatalf("terminal rows changed across second reopen: count=%d", terminalCount)
	}
}

func TestJobActiveMigrationMarkerSkipsAndRefusesFutureVersion(t *testing.T) {
	s := newJobHistoryMigrationStore(t)
	ctx := t.Context()
	initial, ok := readJobMigrationMarker(t, ctx, s)
	if !ok || initial.Version != jobActiveMigrationVersion || initial.CompletedAt.IsZero() {
		t.Fatalf("initial active-job migration marker = %+v, present=%t", initial, ok)
	}

	rid := jobMigrationRecordID(JobSync, "post-marker-legacy")
	requireJobMigrationQuery[any](t, ctx, s, `CREATE $rid CONTENT {
		target: 'post-marker-legacy', status: 'pending', attempts: 0,
		created_at: time::now()
	}`, map[string]any{"rid": rid})
	if err := s.migrateLegacyJobs(ctx); err != nil {
		t.Fatalf("steady-state marker skip: %v", err)
	}
	unchanged := readJobMigrationRow(t, ctx, s, rid)
	if unchanged.Status != StatusPending || unchanged.PendingKey != nil {
		t.Fatalf("steady-state migration touched post-marker probe: %+v", unchanged)
	}
	afterSkip, ok := readJobMigrationMarker(t, ctx, s)
	if !ok || afterSkip.Version != initial.Version ||
		!afterSkip.CompletedAt.Equal(initial.CompletedAt) {
		t.Fatalf("steady-state marker changed: before=%+v after=%+v", initial, afterSkip)
	}

	const futureVersion = "t30.6n-active-jobs-v2"
	requireJobMigrationQuery[any](t, ctx, s,
		"UPDATE $marker SET version = $version RETURN NONE",
		map[string]any{"marker": jobActiveMigrationID(), "version": futureVersion})
	err := s.migrateLegacyJobs(ctx)
	if err == nil || !strings.Contains(err.Error(), `unsupported marker "`+futureVersion+`"`) {
		t.Fatalf("future active-job marker error = %v", err)
	}
	future, ok := readJobMigrationMarker(t, ctx, s)
	if !ok || future.Version != futureVersion ||
		!future.CompletedAt.Equal(initial.CompletedAt) {
		t.Fatalf("future marker was overwritten: %+v, present=%t", future, ok)
	}
}

func TestJobActiveMigrationRefusesMissingPendingIndexOnNonemptyTable(t *testing.T) {
	s := newJobHistoryMigrationStore(t)
	ctx := t.Context()
	rid := jobMigrationRecordID(JobIndex, "missing-pending-index")
	requireJobMigrationQuery[any](t, ctx, s, `
CREATE $rid CONTENT {
	target: 'retained-terminal', status: 'failed', attempts: 7,
	error: 'retained across refused migration', created_at: time::now(),
	finished_at: time::now()
};
DELETE $marker;
REMOVE INDEX indexing_job_pending_key ON TABLE indexing_job;`, map[string]any{
		"rid": rid, "marker": jobActiveMigrationID(),
	})

	err := s.migrateLegacyJobs(ctx)
	if err == nil || !strings.Contains(
		err.Error(),
		"nonempty indexing_job lacks its required pending-key index",
	) {
		t.Fatalf("missing pending-index migration error = %v", err)
	}
	if _, ok := readJobMigrationMarker(t, ctx, s); ok {
		t.Fatal("refused missing-index migration wrote completion marker")
	}
	row := readJobMigrationRow(t, ctx, s, rid)
	if row.Status != StatusFailed || row.Attempts != 7 ||
		row.Error != "retained across refused migration" {
		t.Fatalf("terminal row changed during missing-index refusal: %+v", row)
	}
}

func TestJobActiveMigrationInterruptedResumeIsIdempotent(t *testing.T) {
	s := newJobHistoryMigrationStore(t)
	ctx := t.Context()
	base := time.Date(2026, 7, 31, 14, 0, 0, 0, time.UTC)
	earlyRID := jobMigrationRecordID(JobSync, "interrupted-early")
	trapRID := jobMigrationRecordID(JobResolverCatalog, "interrupted-trap")
	requireJobMigrationQuery[any](t, ctx, s, `
CREATE $early CONTENT {
	target: 'interrupted-early', status: 'running', attempts: 0,
	created_at: $early_at, claimed_by: 'legacy', claimed_at: $early_at,
	heartbeat_at: $early_at
};
CREATE $trap CONTENT {
	target: 'interrupted-trap', status: 'running', attempts: 0,
	created_at: $trap_at, claimed_by: 'legacy', claimed_at: $trap_at,
	heartbeat_at: $trap_at
};
DELETE $marker;
DEFINE EVENT job_active_migration_interrupt ON TABLE resolver_catalog_job
	WHEN $event = 'UPDATE' AND $after.target = 'interrupted-trap'
	THEN { THROW 'intentional active-job migration interruption' };`, map[string]any{
		"early": earlyRID, "trap": trapRID,
		"early_at": base, "trap_at": base.Add(time.Second),
		"marker": jobActiveMigrationID(),
	})

	if err := s.migrateLegacyJobs(ctx); err == nil {
		t.Fatal("interrupted active-job migration unexpectedly succeeded")
	}
	if _, ok := readJobMigrationMarker(t, ctx, s); ok {
		t.Fatal("interrupted active-job migration wrote completion marker")
	}
	early := readJobMigrationRow(t, ctx, s, earlyRID)
	if early.Status != StatusPending || early.Attempts != 1 ||
		early.PendingKey != "interrupted-early" {
		t.Fatalf("early row before resume = %+v", early)
	}

	requireJobMigrationQuery[any](t, ctx, s,
		"REMOVE EVENT job_active_migration_interrupt ON TABLE resolver_catalog_job", nil)
	if err := s.migrateLegacyJobs(ctx); err != nil {
		t.Fatalf("resume active-job migration: %v", err)
	}
	early = readJobMigrationRow(t, ctx, s, earlyRID)
	if early.Status != StatusPending || early.Attempts != 1 ||
		early.PendingKey != "interrupted-early" {
		t.Fatalf("early row changed on resume = %+v", early)
	}
	trap := readJobMigrationRow(t, ctx, s, trapRID)
	if trap.Status != StatusPending || trap.Attempts != 1 ||
		trap.PendingKey != "interrupted-trap" {
		t.Fatalf("trapped row after resume = %+v", trap)
	}
	marker, ok := readJobMigrationMarker(t, ctx, s)
	if !ok || marker.Version != jobActiveMigrationVersion || marker.CompletedAt.IsZero() {
		t.Fatalf("resumed migration marker = %+v, present=%t", marker, ok)
	}
	if err := s.migrateLegacyJobs(ctx); err != nil {
		t.Fatalf("idempotent completed migration: %v", err)
	}
	afterRepeat := readJobMigrationRow(t, ctx, s, earlyRID)
	if afterRepeat.Attempts != 1 || afterRepeat.Status != StatusPending {
		t.Fatalf("completed migration repeated repair: %+v", afterRepeat)
	}
}

func TestJobActiveMigrationPlansUseStatusIndexes(t *testing.T) {
	s := newJobHistoryMigrationStore(t)
	ctx := t.Context()

	for _, kind := range durableJobKinds {
		t.Run(string(kind), func(t *testing.T) {
			if _, err := s.CreateJob(ctx, kind, "plan/"+string(kind)); err != nil {
				t.Fatalf("seed active row: %v", err)
			}
			statement := fmt.Sprintf(`SELECT id, target, status, attempts, created_at, lease_token
				FROM %s WITH INDEX %s_status
				WHERE status IN ['pending', 'claimed', 'running']
				LIMIT $limit EXPLAIN FULL`, kind, kind)
			results := requireJobMigrationQuery[any](t, ctx, s, statement,
				map[string]any{"limit": maxJobActiveMigrationRows + 1})
			plan, err := json.Marshal(results)
			if err != nil {
				t.Fatalf("marshal query plan: %v", err)
			}
			planText := string(plan)
			if !strings.Contains(planText, string(kind)+"_status") ||
				!strings.Contains(planText, "UnionIndexScan") ||
				!strings.Contains(planText, "Limit") {
				t.Fatalf("active query did not use expected indexed plan: %s", plan)
			}
			if strings.Contains(planText, "TableScan") ||
				strings.Contains(planText, "Sort") {
				t.Fatalf("active query plan contains an unbounded scan/sort: %s", plan)
			}
		})
	}
}
