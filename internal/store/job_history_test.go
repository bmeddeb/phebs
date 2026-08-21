package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func newJobHistoryStore(t *testing.T) *Surreal {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	s, err := OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenLocalMemory: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

func requireJobHistoryQuery(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	statement string,
	vars map[string]any,
) {
	t.Helper()
	results, err := surrealdb.Query[any](ctx, s.db, statement, vars)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf("statement %d: %s", index, result.Error.Message)
		}
	}
}

func seedJobHistoryRows(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	kind JobKind,
	prefix string,
	count int,
	status JobStatus,
	createdAt any,
) {
	t.Helper()
	var statement strings.Builder
	statement.WriteString("BEGIN;\n")
	for index := range count {
		_, _ = fmt.Fprintf(&statement, `CREATE type::record($table, '%s-%04d') CONTENT {
			target: $target, status: $status, attempts: 0, created_at: $created
		} RETURN NONE;
`, prefix, index)
	}
	statement.WriteString("COMMIT;")
	requireJobHistoryQuery(t, ctx, s, statement.String(), map[string]any{
		"table": string(kind), "target": prefix + "-target",
		"status": string(status), "created": createdAt,
	})
}

type jobHistoryScanPlan struct {
	Operator   string
	Table      string
	RecordID   string
	Direction  string
	Limit      any
	OutputRows any
}

type jobHistoryPlanInspection struct {
	Scans         []jobHistoryScanPlan
	Limits        []jobHistoryScanPlan
	SortOperators []string
}

func inspectJobHistoryPlan(value any) jobHistoryPlanInspection {
	var inspection jobHistoryPlanInspection
	var visit func(any)
	visit = func(node any) {
		switch typed := node.(type) {
		case *[]surrealdb.QueryResult[any]:
			for _, result := range *typed {
				visit(result.Result)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		case map[string]any:
			operator, _ := typed["operator"].(string)
			if strings.Contains(operator, "Sort") {
				inspection.SortOperators = append(inspection.SortOperators, operator)
			}
			if operator == "TableScan" || operator == "RecordIdScan" {
				attributes, _ := typed["attributes"].(map[string]any)
				metrics, _ := typed["metrics"].(map[string]any)
				table, _ := attributes["table"].(string)
				direction, _ := attributes["direction"].(string)
				inspection.Scans = append(inspection.Scans, jobHistoryScanPlan{
					Operator: operator, Table: table, Direction: direction,
					RecordID: fmt.Sprint(attributes["record_id"]), Limit: attributes["limit"],
					OutputRows: metrics["output_rows"],
				})
			}
			if operator == "Limit" {
				attributes, _ := typed["attributes"].(map[string]any)
				metrics, _ := typed["metrics"].(map[string]any)
				inspection.Limits = append(inspection.Limits, jobHistoryScanPlan{
					Operator: operator, Limit: attributes["limit"],
					OutputRows: metrics["output_rows"],
				})
			}
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return inspection
}

func requireBoundedJobHistoryPlan(
	t *testing.T,
	ctx context.Context,
	s *Surreal,
	kind JobKind,
	after *models.RecordID,
) {
	t.Helper()
	statement := `SELECT ` + boundedJobHistoryFields + `
		FROM type::table($table) ORDER BY id LIMIT $scan_limit EXPLAIN FULL`
	vars := map[string]any{
		"table": string(kind), "scan_limit": jobHistoryScanRows + 1,
		"max_target_characters":     MaxJobHistoryTargetCharacters,
		"max_error_characters":      MaxJobHistoryErrorCharacters,
		"max_claimed_by_characters": MaxJobHistoryClaimedByCharacters,
	}
	if after != nil {
		statement = `SELECT ` + boundedJobHistoryFields + `
			FROM ` + after.String() + `>..
			ORDER BY id LIMIT $scan_limit EXPLAIN FULL`
	}
	results, err := surrealdb.Query[any](ctx, s.db, statement, vars)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range *results {
		if result.Error != nil {
			t.Fatalf("statement %d: %s", index, result.Error.Message)
		}
	}

	inspection := inspectJobHistoryPlan(results)
	encoded, _ := json.Marshal(results)
	if len(inspection.SortOperators) != 0 {
		t.Fatalf("bounded history plan contains sort %v: %s", inspection.SortOperators, encoded)
	}
	if len(inspection.Scans) != 1 {
		t.Fatalf("bounded history plan scans = %+v, want one: %s",
			inspection.Scans, encoded)
	}
	scan := inspection.Scans[0]
	if after == nil {
		if scan.Operator != "TableScan" || scan.Table != string(kind) ||
			scan.Direction != "Forward" || fmt.Sprint(scan.Limit) != "$scan_limit" ||
			fmt.Sprint(scan.OutputRows) != fmt.Sprint(jobHistoryScanRows+1) {
			t.Fatalf("first-page scan = %+v, want bounded forward table scan: %s", scan, encoded)
		}
		return
	}
	if scan.Operator != "RecordIdScan" ||
		!strings.HasPrefix(scan.RecordID, string(kind)+":") ||
		len(inspection.Limits) != 1 ||
		fmt.Sprint(inspection.Limits[0].Limit) != "$scan_limit" ||
		fmt.Sprint(inspection.Limits[0].OutputRows) != fmt.Sprint(jobHistoryScanRows+1) {
		t.Fatalf("continuation plan = scans:%+v limits:%+v, want record seek then limit %d: %s",
			inspection.Scans, inspection.Limits, jobHistoryScanRows+1, encoded)
	}
}

func TestListJobsPagePlansUseBoundedFirstScanAndContinuationSeek(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := t.Context()
	seedJobHistoryRows(
		t, ctx, s, JobIndex, "plan", jobHistoryScanRows+5,
		StatusDone, time.Now().UTC(),
	)

	requireBoundedJobHistoryPlan(t, ctx, s, JobIndex, nil)
	after := models.NewRecordID(string(JobIndex), "plan-0000")
	requireBoundedJobHistoryPlan(t, ctx, s, JobIndex, &after)
}

func TestReapStaleUsesBoundedActiveBatchWithoutSort(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := t.Context()
	stale := time.Now().UTC().Add(-time.Hour)
	seedJobHistoryRows(
		t, ctx, s, JobSync, "reap-batch", maxJobReapRows+1,
		StatusRunning, stale,
	)
	requireJobHistoryQuery(t, ctx, s, `UPDATE connection_sync_job SET
		claimed_by = 'bounded-reaper', lease_token = '0123456789abcdef0123456789abcdef',
		heartbeat_at = $stale RETURN NONE`, map[string]any{"stale": stale})

	statement := fmt.Sprintf(`SELECT id, target, status, attempts, heartbeat_at,
		claimed_by, lease_token, force FROM %s WITH INDEX %s_status
		WHERE status IN ['claimed', 'running']
			AND heartbeat_at != NONE AND heartbeat_at < $cutoff
		LIMIT $limit EXPLAIN FULL`, JobSync, JobSync)
	results, err := surrealdb.Query[any](ctx, s.db, statement, map[string]any{
		"cutoff": time.Now().UTC(), "limit": maxJobReapRows,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	planText := string(plan)
	if !strings.Contains(planText, string(JobSync)+"_status") ||
		!strings.Contains(planText, "UnionIndexScan") ||
		!strings.Contains(planText, "Limit") ||
		strings.Contains(planText, "TableScan") ||
		strings.Contains(planText, "Sort") {
		t.Fatalf("stale-reap plan is not bounded and index-backed: %s", plan)
	}

	if count, err := s.ReapStale(ctx, JobSync, time.Minute, 1); err != nil ||
		count != maxJobReapRows {
		t.Fatalf("first stale-reap batch = %d, %v; want %d", count, err, maxJobReapRows)
	}
	if count, err := s.ReapStale(ctx, JobSync, time.Minute, 1); err != nil || count != 1 {
		t.Fatalf("second stale-reap batch = %d, %v; want 1", count, err)
	}
	if count, err := s.ReapStale(ctx, JobSync, time.Minute, 1); err != nil || count != 0 {
		t.Fatalf("exhausted stale-reap batch = %d, %v; want 0", count, err)
	}
}

func TestListJobsPageAutogeneratedRecordIDCursorRoundTrip(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := t.Context()
	target := "example.com/history/generated"
	created, err := s.CreateJob(ctx, JobIndex, target)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := surrealdb.Query[[]jobRec](ctx, s.db,
		`SELECT id FROM type::table($table) WHERE target = $target LIMIT 1`,
		map[string]any{"table": string(JobIndex), "target": target})
	if err != nil {
		t.Fatal(err)
	}
	if len(*rows) != 1 || (*rows)[0].Error != nil || len((*rows)[0].Result) != 1 ||
		(*rows)[0].Result[0].RecID == nil {
		t.Fatalf("generated record lookup = %+v", rows)
	}
	generatedID, ok := (*rows)[0].Result[0].RecID.ID.(string)
	if !ok || generatedID == "" {
		t.Fatalf("generated record id = %#v", (*rows)[0].Result[0].RecID.ID)
	}
	successorID := generatedID + "z"
	requireJobHistoryQuery(t, ctx, s, `CREATE $rid CONTENT {
		target: $target, status: 'done', attempts: 1, created_at: $created,
		finished_at: $created
	} RETURN NONE`, map[string]any{
		"rid":    models.NewRecordID(string(JobIndex), successorID),
		"target": target + "/successor", "created": time.Now().UTC(),
	})

	first, err := s.ListJobsPage(ctx, JobPageQuery{Kind: JobIndex, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Jobs) != 1 || first.Jobs[0].ID != created.ID || first.Next == nil ||
		first.Next.ID != generatedID {
		t.Fatalf("first generated-id page = %+v; generated id = %q", first, generatedID)
	}
	second, err := s.ListJobsPage(ctx, JobPageQuery{
		Kind: JobIndex, Limit: 1, After: first.Next,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSuccessorRecord := models.NewRecordID(string(JobIndex), successorID)
	wantSuccessor := wantSuccessorRecord.String()
	if len(second.Jobs) != 1 || second.Jobs[0].ID != wantSuccessor || second.Next != nil {
		t.Fatalf("page after generated id = %+v; want %s", second, wantSuccessor)
	}
}

func TestListJobsPageBoundsAndSanitizesErrors(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := t.Context()
	longError := strings.Repeat("界", MaxJobHistoryErrorCharacters+17)
	longTarget := strings.Repeat("界", MaxJobHistoryTargetCharacters+17)
	longClaimedBy := strings.Repeat("界", MaxJobHistoryClaimedByCharacters+17)
	longRID := models.NewRecordID(string(JobIndex), "error-long")
	poisonRID := models.NewRecordID(string(JobIndex), "error-poison")
	wideRID := models.NewRecordID(string(JobIndex), "wide-fields")
	requireJobHistoryQuery(t, ctx, s, `CREATE $long_rid CONTENT {
		target: 'long', status: 'failed', attempts: 1, error: $long_error,
		created_at: $created, finished_at: $created
	};
	CREATE $poison_rid CONTENT {
		target: 'poison', status: 'failed', attempts: 1, error: {not: 'a string'},
		created_at: $created, finished_at: $created
	};
	CREATE $wide_rid CONTENT {
		target: $long_target, status: 'failed', attempts: 1,
		claimed_by: $long_claimed_by, lease_token: 'must-not-leave-authority-read',
		created_at: $created, finished_at: $created
	};`, map[string]any{
		"long_rid": longRID, "poison_rid": poisonRID, "wide_rid": wideRID,
		"long_error": longError, "long_target": longTarget,
		"long_claimed_by": longClaimedBy, "created": time.Now().UTC(),
	})

	page, err := s.ListJobsPage(ctx, JobPageQuery{
		Kind: JobIndex, Status: StatusFailed, Limit: 10,
	})
	if err != nil {
		t.Fatalf("decode bounded errors: %v", err)
	}
	if len(page.Jobs) != 3 || page.Next != nil {
		t.Fatalf("bounded error page = %+v", page)
	}
	byID := make(map[string]Job, len(page.Jobs))
	for _, job := range page.Jobs {
		byID[job.ID] = job
	}
	bounded := byID[longRID.String()]
	if bounded.Error != strings.Repeat("界", MaxJobHistoryErrorCharacters) ||
		utf8.RuneCountInString(bounded.Error) != MaxJobHistoryErrorCharacters ||
		!bounded.ErrorTruncated {
		t.Fatalf("bounded error = %d characters, truncated=%t",
			utf8.RuneCountInString(bounded.Error), bounded.ErrorTruncated)
	}
	poison := byID[poisonRID.String()]
	if poison.Error != "" || poison.ErrorTruncated {
		t.Fatalf("poisoned error projection = %+v", poison)
	}
	wide := byID[wideRID.String()]
	if wide.Target != strings.Repeat("界", MaxJobHistoryTargetCharacters) ||
		!wide.TargetTruncated ||
		wide.ClaimedBy != strings.Repeat("界", MaxJobHistoryClaimedByCharacters) ||
		!wide.ClaimedByTruncated || wide.LeaseToken != "" {
		t.Fatalf("bounded variable-width fields = %+v", wide)
	}

	raw, err := surrealdb.Query[[]struct {
		Error string `json:"error"`
	}](ctx, s.db, `SELECT error FROM $rid`, map[string]any{"rid": longRID})
	if err != nil {
		t.Fatal(err)
	}
	if len(*raw) != 1 || (*raw)[0].Error != nil || len((*raw)[0].Result) != 1 ||
		(*raw)[0].Result[0].Error != longError {
		t.Fatalf("stored raw error changed: %+v", raw)
	}
}

func TestListJobsPageCapKeysetAndScopeAllKinds(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := context.Background()
	kinds := []JobKind{
		JobSync, JobIndex, JobFetch, JobCandidate, JobExtract,
		JobResolverCatalog, JobCallerLeaf, JobInvestigate,
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			seedJobHistoryRows(
				t, ctx, s, kind, "page", MaxJobHistoryPageRows+1,
				StatusDone, time.Now().UTC(),
			)
			first, err := s.ListJobsPage(ctx, JobPageQuery{
				Kind: kind, Status: StatusDone, Limit: MaxJobHistoryPageRows,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Jobs) != MaxJobHistoryPageRows || first.Next == nil {
				t.Fatalf("first page = %d jobs, next=%+v", len(first.Jobs), first.Next)
			}
			if first.Next.Kind != kind || first.Next.Status != StatusDone || first.Next.ID == "" {
				t.Fatalf("first cursor = %+v", first.Next)
			}
			seen := make(map[string]bool, MaxJobHistoryPageRows+1)
			for _, job := range first.Jobs {
				if job.Kind != kind || job.Status != StatusDone || seen[job.ID] {
					t.Fatalf("invalid or duplicate first-page job: %+v", job)
				}
				seen[job.ID] = true
			}

			second, err := s.ListJobsPage(ctx, JobPageQuery{
				Kind: kind, Status: StatusDone, Limit: MaxJobHistoryPageRows,
				After: first.Next,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(second.Jobs) != 1 || second.Next != nil || seen[second.Jobs[0].ID] {
				t.Fatalf("second page = %+v", second)
			}
			seen[second.Jobs[0].ID] = true
			if len(seen) != MaxJobHistoryPageRows+1 {
				t.Fatalf("unique jobs = %d", len(seen))
			}

			for _, limit := range []int{0, MaxJobHistoryPageRows + 1} {
				if _, err := s.ListJobsPage(ctx, JobPageQuery{
					Kind: kind, Status: StatusDone, Limit: limit,
				}); err == nil {
					t.Fatalf("limit %d accepted", limit)
				}
			}
			if _, err := s.ListJobsPage(ctx, JobPageQuery{
				Kind: kind, Status: "unknown", Limit: 1,
			}); err == nil {
				t.Fatal("unknown status accepted")
			}

			wrongKind := *first.Next
			wrongKind.Kind = JobKind("wrong_job")
			wrongStatus := *first.Next
			wrongStatus.Status = StatusFailed
			badID := *first.Next
			badID.ID = " "
			injectedID := *first.Next
			injectedID.ID = "cursor;DELETE indexing_job"
			for _, cursor := range []*JobCursor{&wrongKind, &wrongStatus, &badID, &injectedID} {
				if _, err := s.ListJobsPage(ctx, JobPageQuery{
					Kind: kind, Status: StatusDone, Limit: 1, After: cursor,
				}); err == nil {
					t.Fatalf("out-of-scope cursor accepted: %+v", cursor)
				}
			}
		})
	}

	if _, err := s.ListJobsPage(ctx, JobPageQuery{
		Kind: "unknown_job", Limit: 1,
	}); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestListJobsPageSparseFilterReturnsEmptyContinuation(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := context.Background()
	seedJobHistoryRows(
		t, ctx, s, JobExtract, "sparse", jobHistoryScanRows+1,
		StatusPending, time.Now().UTC(),
	)

	first, err := s.ListJobsPage(ctx, JobPageQuery{
		Kind: JobExtract, Status: StatusCanceled, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Jobs) != 0 || first.Next == nil {
		t.Fatalf("first sparse page = %+v; want empty continuation", first)
	}
	second, err := s.ListJobsPage(ctx, JobPageQuery{
		Kind: JobExtract, Status: StatusCanceled, Limit: 10, After: first.Next,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Jobs) != 0 || second.Next != nil {
		t.Fatalf("second sparse page = %+v; want exhausted empty page", second)
	}
}

func requireRepoJobStatus(t *testing.T, ctx context.Context, s *Surreal, name string) RepoStatus {
	t.Helper()
	statuses, err := s.RepoStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range statuses {
		if status.Name == name {
			return status
		}
	}
	t.Fatalf("repository %q missing from statuses", name)
	return RepoStatus{}
}

func TestRepoStatusesProjectionUnavailableToExactAndLive(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := context.Background()
	repository := "example.com/projection/live"
	if err := s.UpsertRepo(ctx, Repo{Name: repository, CloneURL: "https://example.com/live.git"}); err != nil {
		t.Fatal(err)
	}

	status := requireRepoJobStatus(t, ctx, s, repository)
	if status.LastIndexJobState != JobProjectionUnavailable || status.LastIndexJob != nil {
		t.Fatalf("initial projection = %+v", status)
	}
	created, err := s.CreateJob(ctx, JobIndex, repository)
	if err != nil {
		t.Fatal(err)
	}
	status = requireRepoJobStatus(t, ctx, s, repository)
	if status.LastIndexJobState != JobProjectionExact || status.LastIndexJob == nil ||
		status.LastIndexJob.ID != created.ID || status.LastIndexJob.Status != StatusPending {
		t.Fatalf("created projection = %+v", status)
	}

	claimed, err := s.ClaimJob(ctx, JobIndex, "projection-worker")
	if err != nil {
		t.Fatal(err)
	}
	status = requireRepoJobStatus(t, ctx, s, repository)
	if status.LastIndexJob == nil || status.LastIndexJob.Status != StatusClaimed ||
		status.LastIndexJob.ClaimedBy != "projection-worker" {
		t.Fatalf("claimed projection = %+v", status.LastIndexJob)
	}
	if err := s.SetJobStatus(ctx, *claimed, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatJob(ctx, *claimed); err != nil {
		t.Fatal(err)
	}
	status = requireRepoJobStatus(t, ctx, s, repository)
	if status.LastIndexJob == nil || status.LastIndexJob.Status != StatusRunning ||
		status.LastIndexJob.HeartbeatAt == nil {
		t.Fatalf("running projection = %+v", status.LastIndexJob)
	}
	if err := s.SetJobStatus(ctx, *claimed, StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	status = requireRepoJobStatus(t, ctx, s, repository)
	if status.LastIndexJob == nil || status.LastIndexJob.Status != StatusDone ||
		status.LastIndexJob.FinishedAt == nil {
		t.Fatalf("terminal projection = %+v", status.LastIndexJob)
	}
}

func TestRepoStatusesProjectionIgnoresRestoredRowsAndTracksEnqueueWriter(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := context.Background()
	repository := "example.com/projection/restore"
	if err := s.UpsertRepo(ctx, Repo{Name: repository, CloneURL: "https://example.com/order.git"}); err != nil {
		t.Fatal(err)
	}
	legacy := models.NewRecordID(string(JobIndex), "restored-terminal")
	requireJobHistoryQuery(t, ctx, s, `CREATE $rid CONTENT {
		target: $target, status: 'done', attempts: 1, created_at: $created,
		finished_at: $created
	} RETURN NONE`, map[string]any{
		"rid": legacy, "target": repository,
		"created": time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
	})

	status := requireRepoJobStatus(t, ctx, s, repository)
	if status.LastIndexJobState != JobProjectionUnavailable || status.LastIndexJob != nil {
		t.Fatalf("restored row established projection authority: %+v", status)
	}
	created, err := s.EnqueuePending(ctx, JobIndex, repository, false)
	if err != nil {
		t.Fatal(err)
	}
	status = requireRepoJobStatus(t, ctx, s, repository)
	if status.LastIndexJobState != JobProjectionExact || status.LastIndexJob == nil ||
		status.LastIndexJob.ID != created.ID {
		t.Fatalf("enqueue projection = %+v; want %s", status.LastIndexJob, created.ID)
	}
	createdRID, err := models.ParseRecordID(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireJobHistoryQuery(t, ctx, s,
		"UPDATE $rid SET status = 'failed', error = 'projected failure' RETURN NONE",
		map[string]any{"rid": createdRID},
	)
	status = requireRepoJobStatus(t, ctx, s, repository)
	if status.LastIndexJob == nil || status.LastIndexJob.ID != created.ID ||
		status.LastIndexJob.Status != StatusFailed || status.LastIndexJob.Error != "projected failure" {
		t.Fatalf("mutated projection = %+v", status.LastIndexJob)
	}
}

func TestRepoStatusesProjectionTracksCrashRecoverySuccessor(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := context.Background()
	repository := "example.com/projection/successor"
	if err := s.UpsertRepo(ctx, Repo{Name: repository, CloneURL: "https://example.com/successor.git"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueuePending(ctx, JobIndex, repository, false); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimJob(ctx, JobIndex, "projection-successor-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *claimed, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	successor, err := s.EnsureJobSuccessor(ctx, *claimed, true)
	if err != nil {
		t.Fatal(err)
	}
	status := requireRepoJobStatus(t, ctx, s, repository)
	if status.LastIndexJobState != JobProjectionExact || status.LastIndexJob == nil ||
		status.LastIndexJob.ID != successor.ID || status.LastIndexJob.Status != StatusPending {
		t.Fatalf("successor projection = %+v; want %s", status.LastIndexJob, successor.ID)
	}
}

func TestRepoStatusesProjectsOnlyClosedLatestExtractionFailure(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := context.Background()
	repository := "example.com/projection/extraction"
	if err := s.UpsertRepo(ctx, Repo{Name: repository, CloneURL: "https://example.com/extraction.git"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueuePending(ctx, JobExtract, repository, false); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimJob(ctx, JobExtract, "extraction-projection-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *claimed, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	refusal := pipelinerefusal.Limit(
		fmt.Errorf("private repository detail"),
		pipelinerefusal.StageDomainInventory,
		pipelinerefusal.GenerationExtractionDomain,
		pipelinerefusal.DimensionCandidateMemberBytes,
		792_000_000, 67_108_864,
	)
	if err := s.SetJobStatus(ctx, *claimed, StatusFailed, refusal.Error()); err != nil {
		t.Fatal(err)
	}
	status := requireRepoJobStatus(t, ctx, s, repository)
	if status.LastExtractionJobState != JobProjectionExact || status.LastExtractionJob == nil ||
		status.LastExtractionJob.Status != StatusFailed || status.LastExtractionJob.Attempts != 0 ||
		status.LastExtractionJob.Refusal == nil ||
		status.LastExtractionJob.Refusal.Dimension != pipelinerefusal.DimensionCandidateMemberBytes ||
		status.LastExtractionJob.Refusal.Observed != 792_000_000 ||
		status.LastExtractionJob.Refusal.Limit != 67_108_864 {
		t.Fatalf("extraction projection = %+v", status.LastExtractionJob)
	}
	if strings.Contains(fmt.Sprintf("%+v", status.LastExtractionJob), "private") {
		t.Fatalf("extraction projection leaked raw error: %+v", status.LastExtractionJob)
	}
}

func TestRepoStatusesDoesNotDecodeTerminalHistory(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := context.Background()
	current := "example.com/status/current"
	legacy := "example.com/status/legacy"
	for _, repository := range []string{current, legacy} {
		if err := s.UpsertRepo(ctx, Repo{
			Name: repository, CloneURL: "https://" + repository + ".git",
		}); err != nil {
			t.Fatal(err)
		}
	}
	currentJob, err := s.CreateJob(ctx, JobIndex, current)
	if err != nil {
		t.Fatal(err)
	}
	seedJobHistoryRows(
		t, ctx, s, JobIndex, "poison", jobHistoryScanRows+1,
		StatusDone, map[string]any{"not": "a datetime"},
	)

	statuses, err := s.RepoStatuses(ctx)
	if err != nil {
		t.Fatalf("RepoStatuses decoded terminal history: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %d, want 2 current repositories", len(statuses))
	}
	currentStatus := requireRepoJobStatus(t, ctx, s, current)
	if currentStatus.LastIndexJobState != JobProjectionExact ||
		currentStatus.LastIndexJob == nil || currentStatus.LastIndexJob.ID != currentJob.ID {
		t.Fatalf("current projection = %+v", currentStatus)
	}
	legacyStatus := requireRepoJobStatus(t, ctx, s, legacy)
	if legacyStatus.LastIndexJobState != JobProjectionUnavailable || legacyStatus.LastIndexJob != nil {
		t.Fatalf("legacy projection = %+v", legacyStatus)
	}
}

// The caller-leaf orchestration job projection mirrors the extraction one:
// unavailable without a post-cutover row, exact and live through the pending
// -> claimed -> running -> failed lifecycle, so evidence can distinguish a
// dead caller pipeline from a live slow one.
func TestRepoStatusesProjectsCallerJobLifecycle(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := context.Background()
	repository := "example.com/projection/caller"
	if err := s.UpsertRepo(ctx, Repo{Name: repository, CloneURL: "https://example.com/caller.git"}); err != nil {
		t.Fatal(err)
	}
	if status := requireRepoJobStatus(t, ctx, s, repository); status.LastCallerJobState != JobProjectionUnavailable ||
		status.LastCallerJob != nil {
		t.Fatalf("pre-creation caller projection = %+v", status.LastCallerJob)
	}
	if _, err := s.EnqueuePending(ctx, JobCallerLeaf, repository, false); err != nil {
		t.Fatal(err)
	}
	if status := requireRepoJobStatus(t, ctx, s, repository); status.LastCallerJobState != JobProjectionExact ||
		status.LastCallerJob == nil || status.LastCallerJob.Status != StatusPending {
		t.Fatalf("pending caller projection = %+v", status.LastCallerJob)
	}
	claimed, err := s.ClaimJob(ctx, JobCallerLeaf, "caller-projection-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *claimed, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *claimed, StatusFailed, "pair execution died"); err != nil {
		t.Fatal(err)
	}
	status := requireRepoJobStatus(t, ctx, s, repository)
	if status.LastCallerJobState != JobProjectionExact || status.LastCallerJob == nil ||
		status.LastCallerJob.Status != StatusFailed || status.LastCallerJob.Attempts != 0 {
		t.Fatalf("failed caller projection = %+v", status.LastCallerJob)
	}
	// Domain transactions create caller successors outside the generic queue;
	// the projection must follow them too, or a stale done/failed row would
	// masquerade as the pipeline's current state (repo reactivation exercises
	// the shared projectCallerJobSQL fragment).
	if err := s.SetRepoDeleting(ctx, repository, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoDeleting(ctx, repository, false); err != nil {
		t.Fatal(err)
	}
	status = requireRepoJobStatus(t, ctx, s, repository)
	if status.LastCallerJobState != JobProjectionExact || status.LastCallerJob == nil ||
		status.LastCallerJob.Status != StatusPending {
		t.Fatalf("reactivation caller projection = %+v", status.LastCallerJob)
	}
	// Simulate a pre-projection pending row at the domain-transaction boundary:
	// reactivation must repair the exact coalesced row, not wait for a new job.
	requireJobHistoryQuery(t, ctx, s, `UPDATE $repository UNSET
		latest_caller_job, latest_caller_job_created_at,
		latest_caller_job_projection_version RETURN NONE`, map[string]any{
		"repository": repoID(repository),
	})
	if err := s.SetRepoDeleting(ctx, repository, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoDeleting(ctx, repository, false); err != nil {
		t.Fatal(err)
	}
	status = requireRepoJobStatus(t, ctx, s, repository)
	if status.LastCallerJobState != JobProjectionExact || status.LastCallerJob == nil ||
		status.LastCallerJob.Status != StatusPending {
		t.Fatalf("coalesced reactivation projection = %+v", status.LastCallerJob)
	}
}

func TestCallerJobProjectionRepairsCoalescedPreCutoverPending(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := context.Background()
	repository := "example.com/projection/caller-upgrade"
	if state, projection, err := s.GetCallerJobProjection(ctx, repository); state != JobProjectionUnavailable ||
		projection != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing repository projection = %s, %+v, %v", state, projection, err)
	}
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.com/caller-upgrade.git",
	}); err != nil {
		t.Fatal(err)
	}
	requireJobHistoryQuery(t, ctx, s, `
		CREATE caller_leaf_job CONTENT {
			target: $repository, status: 'pending', attempts: 0,
			created_at: time::now(), pending_key: $repository, force: false
		} RETURN NONE`, map[string]any{"repository": repository})
	state, projection, err := s.GetCallerJobProjection(ctx, repository)
	if err != nil || state != JobProjectionUnavailable || projection != nil {
		t.Fatalf("pre-cutover projection = %s, %+v, %v", state, projection, err)
	}
	if _, err := s.EnqueuePending(ctx, JobCallerLeaf, repository, true); err != nil {
		t.Fatal(err)
	}
	state, projection, err = s.GetCallerJobProjection(ctx, repository)
	if err != nil || state != JobProjectionExact || projection == nil ||
		projection.Status != StatusPending || projection.Attempts != 0 {
		t.Fatalf("repaired projection = %s, %+v, %v", state, projection, err)
	}
	jobs, err := s.ListJobs(ctx, JobCallerLeaf, "")
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("coalesced jobs = %+v, %v", jobs, err)
	}
}

// The resolver-catalog job projection mirrors the caller one across the
// generic queue lifecycle, the one-repository operational read, and a domain
// transaction (candidate-manifest publication fans out the resolver
// successor), so evidence can distinguish a caller successor that will never
// be minted from one that is merely late.
func TestRepoStatusesProjectsResolverJobLifecycle(t *testing.T) {
	s := newJobHistoryStore(t)
	ctx := context.Background()
	repository := "example.com/projection/resolver"
	if err := s.UpsertRepo(ctx, Repo{Name: repository, CloneURL: "https://example.com/resolver.git"}); err != nil {
		t.Fatal(err)
	}
	if status := requireRepoJobStatus(t, ctx, s, repository); status.LastResolverJobState != JobProjectionUnavailable ||
		status.LastResolverJob != nil {
		t.Fatalf("pre-creation resolver projection = %+v", status.LastResolverJob)
	}
	if state, projection, err := s.GetResolverJobProjection(ctx, repository); err != nil ||
		state != JobProjectionUnavailable || projection != nil {
		t.Fatalf("pre-creation resolver read = %v %+v %v", state, projection, err)
	}
	if _, err := s.EnqueuePending(ctx, JobResolverCatalog, repository, false); err != nil {
		t.Fatal(err)
	}
	if status := requireRepoJobStatus(t, ctx, s, repository); status.LastResolverJobState != JobProjectionExact ||
		status.LastResolverJob == nil || status.LastResolverJob.Status != StatusPending {
		t.Fatalf("pending resolver projection = %+v", status.LastResolverJob)
	}
	claimed, err := s.ClaimJob(ctx, JobResolverCatalog, "resolver-projection-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *claimed, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *claimed, StatusFailed, "caller successor was never minted"); err != nil {
		t.Fatal(err)
	}
	state, projection, err := s.GetResolverJobProjection(ctx, repository)
	if err != nil || state != JobProjectionExact || projection == nil ||
		projection.Status != StatusFailed || projection.Attempts != 0 {
		t.Fatalf("failed resolver read = %v %+v %v", state, projection, err)
	}
	// Simulate a pre-projection pending row at the generic queue boundary: an
	// exact enqueue must repair the coalesced row rather than wait for a new
	// job. Domain fan-out creation and dead-pipeline repair are covered by the
	// candidate-manifest integration test.
	if _, err := s.EnqueuePending(ctx, JobResolverCatalog, repository, false); err != nil {
		t.Fatal(err)
	}
	requireJobHistoryQuery(t, ctx, s, `UPDATE $repository UNSET
		latest_resolver_job, latest_resolver_job_created_at,
		latest_resolver_job_projection_version RETURN NONE`, map[string]any{
		"repository": repoID(repository),
	})
	if state, projection, err := s.GetResolverJobProjection(ctx, repository); err != nil ||
		state != JobProjectionUnavailable || projection != nil {
		t.Fatalf("unset resolver read = %v %+v %v", state, projection, err)
	}
	if _, err := s.EnqueuePending(ctx, JobResolverCatalog, repository, true); err != nil {
		t.Fatal(err)
	}
	state, projection, err = s.GetResolverJobProjection(ctx, repository)
	if err != nil || state != JobProjectionExact || projection == nil ||
		projection.Status != StatusPending || projection.Attempts != 0 {
		t.Fatalf("repaired resolver read = %v %+v %v", state, projection, err)
	}
	jobs, err := s.ListJobs(ctx, JobResolverCatalog, StatusPending)
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("coalesced pending resolver jobs = %+v, %v", jobs, err)
	}
}
