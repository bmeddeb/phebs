package store

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

//go:embed schema.surql
var schema string

// Surreal implements Store over the official SDK (WebSocket RPC).
type Surreal struct {
	db   *surrealdb.DB
	stop func() // non-nil when we supervise a local child
}

var _ Store = (*Surreal)(nil)

// OpenLocal starts a supervised surreal child storing under dataDir and
// connects to it. This is the single-node default (dev and prod).
func OpenLocal(ctx context.Context, dataDir string) (*Surreal, error) {
	endpoint, stop, err := startLocal(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	s, err := Open(ctx, endpoint, "root", "root", "phebs", "phebs")
	if err != nil {
		stop()
		return nil, err
	}
	s.stop = stop
	return s, nil
}

// Open connects to a running SurrealDB, selects ns/db, and applies the
// schema idempotently. Server-mode path for the fleet profile.
func Open(ctx context.Context, endpoint, user, pass, namespace, database string) (*Surreal, error) {
	db, err := surrealdb.FromEndpointURLString(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", endpoint, err)
	}
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: user, Password: pass}); err != nil {
		return nil, fmt.Errorf("signin: %w", err)
	}
	if err := db.Use(ctx, namespace, database); err != nil {
		return nil, fmt.Errorf("use %s/%s: %w", namespace, database, err)
	}
	s := &Surreal{db: db}
	if err := s.applySchema(ctx); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return s, nil
}

func (s *Surreal) applySchema(ctx context.Context) error {
	results, err := surrealdb.Query[any](ctx, s.db, schema, nil)
	if err != nil {
		return err
	}
	for i, r := range *results {
		if r.Error != nil {
			return fmt.Errorf("statement %d: %s", i, r.Error.Message)
		}
	}
	if err := s.migrateLegacyJobs(ctx); err != nil {
		return err
	}
	if err := s.migrateEvidenceRuns(ctx); err != nil {
		return err
	}
	results, err = surrealdb.Query[any](ctx, s.db, pendingJobIndexes+evidenceIndexes, nil)
	if err != nil {
		return err
	}
	for i, r := range *results {
		if r.Error != nil {
			return fmt.Errorf("pending index statement %d: %s", i, r.Error.Message)
		}
	}
	return nil
}

const pendingJobIndexes = `
DEFINE INDEX IF NOT EXISTS connection_sync_job_pending_key ON connection_sync_job FIELDS pending_key UNIQUE;
DEFINE INDEX IF NOT EXISTS indexing_job_pending_key ON indexing_job FIELDS pending_key UNIQUE;
DEFINE INDEX IF NOT EXISTS repo_fetch_job_pending_key ON repo_fetch_job FIELDS pending_key UNIQUE;
DEFINE INDEX IF NOT EXISTS extraction_job_pending_key ON extraction_job FIELDS pending_key UNIQUE;`

const evidenceIndexes = `
DEFINE INDEX IF NOT EXISTS extraction_run_published_key ON extraction_run FIELDS published_key UNIQUE;`

// migrateLegacyJobs runs before the pending-key indexes are installed. Old
// rows had no lease or pending slot, and may contain an active job plus a
// successor. Keep the oldest pending row, cancel duplicates, then requeue only
// an unfenced active row that has no successor.
func (s *Surreal) migrateLegacyJobs(ctx context.Context) error {
	for _, kind := range []JobKind{JobSync, JobIndex, JobFetch, JobExtract} {
		jobs, err := s.ListJobs(ctx, kind, "")
		if err != nil {
			return fmt.Errorf("migrate %s: list: %w", kind, err)
		}
		pending := make(map[string]bool)
		for _, job := range jobs {
			if job.Status != StatusPending {
				continue
			}
			if pending[job.Target] {
				if err := s.updateLegacyJob(ctx, job.ID,
					`status = 'canceled', error = 'migration: duplicate pending job',
					 finished_at = time::now(), pending_key = NONE`, StatusPending); err != nil {
					return fmt.Errorf("migrate %s duplicate %s: %w", kind, job.ID, err)
				}
				continue
			}
			if err := s.updateLegacyJob(ctx, job.ID, "pending_key = $target", StatusPending,
				"target", job.Target); err != nil {
				return fmt.Errorf("migrate %s pending %s: %w", kind, job.ID, err)
			}
			pending[job.Target] = true
		}

		for _, job := range jobs {
			if (job.Status != StatusClaimed && job.Status != StatusRunning) || job.LeaseToken != "" {
				continue
			}
			if pending[job.Target] {
				if err := s.updateLegacyJob(ctx, job.ID,
					`status = 'canceled', attempts = $attempts,
					 error = 'migration: unfenced job superseded', finished_at = time::now(),
					 claimed_by = NONE, claimed_at = NONE, heartbeat_at = NONE,
					 lease_token = NONE, pending_key = NONE`, job.Status,
					"attempts", job.Attempts+1); err != nil {
					return fmt.Errorf("migrate %s superseded %s: %w", kind, job.ID, err)
				}
				continue
			}
			if err := s.updateLegacyJob(ctx, job.ID,
				`status = 'pending', attempts = $attempts,
				 error = 'requeued: legacy claim without lease', not_before = NONE,
				 claimed_by = NONE, claimed_at = NONE, heartbeat_at = NONE,
				 lease_token = NONE, pending_key = $target`, job.Status,
				"attempts", job.Attempts+1, "target", job.Target); err != nil {
				return fmt.Errorf("migrate %s active %s: %w", kind, job.ID, err)
			}
			pending[job.Target] = true
		}
	}
	return nil
}

// migrateEvidenceRuns installs the explicit string run_id used by bounded
// joins and assigns the one optional unique publication slot per repo/domain.
// Rows from the retracted implementation have no run_id or typed atom links;
// retire them fail-closed so a current extractor must republish them. Because
// historical runs predate ingestion bounds, quarantine them from automatic
// retention; pins and rows remain for audit or explicit administrator cleanup.
func (s *Surreal) migrateEvidenceRuns(ctx context.Context) error {
	const batch = 128
	for {
		results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db,
			`SELECT * FROM extraction_run
                WHERE store_schema_version = NONE OR store_schema_version != $schema
                   OR run_id = NONE OR run_id = ''
                   OR retention_quarantined = NONE
                   OR (status = 'published' AND published_key = NONE)
                   OR (status != 'published' AND published_key != NONE)
                ORDER BY repo, domain, published_at DESC, started_at DESC, id
                LIMIT $limit`, map[string]any{"limit": batch, "schema": evidenceStoreSchemaVersion})
		if err != nil {
			return fmt.Errorf("migrate evidence runs: list: %w", err)
		}
		var candidates []extractionRunRec
		for _, result := range *results {
			candidates = append(candidates, result.Result...)
		}
		if len(candidates) == 0 {
			return nil // steady-state Open performs only the bounded empty read
		}
		for _, row := range candidates {
			legacy := row.StoreSchema != evidenceStoreSchemaVersion
			run := row.run()
			if run.ID == "" {
				return errors.New("migrate evidence runs: row has no string record id")
			}
			status := run.Status
			key := ""
			if legacy {
				switch status {
				case "published":
					status = "superseded"
				case "staged":
					status = "aborted"
				}
			} else if status == "published" {
				existing, err := surrealdb.Query[[]extractionRunRec](ctx, s.db,
					`SELECT * FROM extraction_run
                            WHERE repo = $repo AND domain = $domain AND status = 'published'
                              AND published_key != NONE AND id != $rid LIMIT 1`,
					map[string]any{
						"repo": run.Repo, "domain": run.Domain, "rid": extractionRunID(run.ID),
					})
				if err != nil {
					return fmt.Errorf("migrate evidence run %s: publication lookup: %w", run.ID, err)
				}
				if len(firstExtractionRows(existing)) > 0 {
					status = "superseded"
				} else {
					key = publishedKey(run.Repo, run.Domain)
				}
			}
			setKey := "published_key = NONE"
			vars := map[string]any{
				"rid": extractionRunID(run.ID), "run_id": run.ID, "status": status,
				"store_schema_version":  evidenceStoreSchemaVersion,
				"retention_quarantined": legacy,
			}
			if key != "" {
				setKey = "published_key = $published_key"
				vars["published_key"] = key
			}
			if _, err := surrealdb.Query[any](ctx, s.db,
				"UPDATE $rid SET run_id = $run_id, status = $status, store_schema_version = $store_schema_version, retention_quarantined = $retention_quarantined, "+setKey+" RETURN NONE", vars); err != nil {
				return fmt.Errorf("migrate evidence run %s: %w", run.ID, err)
			}
		}
	}
}

func (s *Surreal) updateLegacyJob(ctx context.Context, id, set string, expected JobStatus, pairs ...any) error {
	vars := map[string]any{"id": id, "expected": string(expected)}
	for i := 0; i < len(pairs); i += 2 {
		vars[pairs[i].(string)] = pairs[i+1]
	}
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		"UPDATE type::record($id) SET "+set+" WHERE status = $expected RETURN AFTER", vars)
	if err != nil {
		return err
	}
	if len(firstNonEmpty(results)) == 0 {
		return fmt.Errorf("job %q changed during migration", id)
	}
	return nil
}

func (s *Surreal) Close(ctx context.Context) error {
	err := s.db.Close(ctx)
	if s.stop != nil {
		s.stop()
	}
	return err
}

// --- repos ---

// repoID builds the record id repo:⟨name⟩; passing it as a typed param
// sidesteps escaping of names like "github.com/foo/bar".
func repoID(name string) models.RecordID { return models.NewRecordID("repo", name) }

func (s *Surreal) UpsertRepo(ctx context.Context, r Repo) error {
	_, err := surrealdb.Query[any](ctx, s.db,
		`UPSERT $rid SET
			name = $name,
			display_name = $display_name,
			clone_url = $clone_url,
			web_url = $web_url,
			default_branch = $default_branch,
			is_fork = $is_fork,
			is_archived = $is_archived,
			is_public = $is_public,
			metadata = $metadata,
			pushed_at = $pushed_at,
			external_id = $external_id,
			external_code_host_type = $external_host_type,
			external_code_host_url = $external_host_url`,
		map[string]any{
			"rid":                repoID(r.Name),
			"name":               r.Name,
			"display_name":       r.DisplayName,
			"clone_url":          r.CloneURL,
			"web_url":            r.WebURL,
			"default_branch":     r.DefaultBranch,
			"is_fork":            r.IsFork,
			"is_archived":        r.IsArchived,
			"is_public":          r.IsPublic,
			"metadata":           r.Metadata,
			"pushed_at":          r.PushedAt,
			"external_id":        r.ExternalID,
			"external_host_type": r.ExternalHostType,
			"external_host_url":  r.ExternalHostURL,
		})
	return err
}

func (s *Surreal) GetRepo(ctx context.Context, name string) (*Repo, error) {
	results, err := surrealdb.Query[[]Repo](ctx, s.db,
		"SELECT * FROM $rid",
		map[string]any{"rid": repoID(name)})
	if err != nil {
		return nil, err
	}
	rows := (*results)[0].Result
	if len(rows) == 0 {
		return nil, fmt.Errorf("repo %q: %w", name, ErrNotFound)
	}
	return &rows[0], nil
}

func (s *Surreal) ListRepos(ctx context.Context) ([]Repo, error) {
	results, err := surrealdb.Query[[]Repo](ctx, s.db, "SELECT * FROM repo ORDER BY name", nil)
	if err != nil {
		return nil, err
	}
	return (*results)[0].Result, nil
}

func (s *Surreal) DeleteRepo(ctx context.Context, name string) error {
	_, err := surrealdb.Query[any](ctx, s.db,
		`BEGIN;
UPDATE extraction_run SET status = 'superseded', published_key = NONE
    WHERE repo = $name AND status = 'published' RETURN NONE;
UPDATE extraction_run SET status = 'aborted', published_key = NONE
    WHERE repo = $name AND status = 'staged' RETURN NONE;
UPDATE extraction_job SET status = 'canceled', error = 'repository deleting',
    finished_at = time::now(), pending_key = NONE
    WHERE target = $name AND status = 'pending' RETURN NONE;
DELETE repo_permission WHERE repo = $name RETURN NONE;
DELETE repo_connection WHERE repo = $name RETURN NONE;
DELETE $rid RETURN NONE;
COMMIT;`, map[string]any{"rid": repoID(name), "name": name})
	return err
}

func (s *Surreal) SetRepoDeleting(ctx context.Context, name string, deleting bool) error {
	results, err := surrealdb.Query[[]Repo](ctx, s.db,
		"UPDATE $rid SET deleting = $deleting RETURN AFTER",
		map[string]any{"rid": repoID(name), "deleting": deleting})
	if err != nil {
		return err
	}
	if len((*results)[0].Result) == 0 {
		return fmt.Errorf("repo %q: %w", name, ErrNotFound)
	}
	return nil
}

func (s *Surreal) SetRepoIndexed(ctx context.Context, name, commitHash string, at time.Time) error {
	results, err := surrealdb.Query[[]Repo](ctx, s.db,
		`UPDATE $rid SET indexed_commit_hash = $hash, indexed_at = $at, latest_indexing_job_status = 'done' RETURN AFTER`,
		map[string]any{"rid": repoID(name), "hash": commitHash, "at": at})
	if err != nil {
		return err
	}
	if len((*results)[0].Result) == 0 {
		return fmt.Errorf("repo %q: %w", name, ErrNotFound)
	}
	return nil
}

func (s *Surreal) ClearRepoIndexState(ctx context.Context, name string) error {
	results, err := surrealdb.Query[[]Repo](ctx, s.db,
		"UPDATE $rid SET indexed_commit_hash = NONE, indexed_at = NONE RETURN AFTER",
		map[string]any{"rid": repoID(name)})
	if err != nil {
		return err
	}
	if len((*results)[0].Result) == 0 {
		return fmt.Errorf("repo %q: %w", name, ErrNotFound)
	}
	return nil
}

// --- connection membership + status ---

func (s *Surreal) SetRepoConnections(ctx context.Context, conn string, repos []string) error {
	_, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
DELETE repo_connection WHERE connection = $conn;
FOR $r IN $repos { CREATE repo_connection CONTENT { connection: $conn, repo: $r } };
COMMIT;`,
		map[string]any{"conn": conn, "repos": repos})
	return err
}

func (s *Surreal) PruneConnections(ctx context.Context, keep []string) error {
	_, err := surrealdb.Query[any](ctx, s.db,
		"DELETE repo_connection WHERE connection NOT IN $keep",
		map[string]any{"keep": keep})
	return err
}

func (s *Surreal) GetRepoConnections(ctx context.Context, repo string) ([]string, error) {
	memb, err := surrealdb.Query[[]struct {
		Connection string `json:"connection"`
	}](ctx, s.db, "SELECT connection FROM repo_connection WHERE repo = $repo",
		map[string]any{"repo": repo})
	if err != nil {
		return nil, err
	}
	var conns []string
	for _, m := range (*memb)[0].Result {
		conns = append(conns, m.Connection)
	}
	return conns, nil
}

// RepoStatuses joins repos, membership, and latest indexing jobs in Go.
// ponytail: three queries + maps, no server-side joins; revisit if repo
// counts make it slow.
func (s *Surreal) RepoStatuses(ctx context.Context) ([]RepoStatus, error) {
	repos, err := s.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	memb, err := surrealdb.Query[[]struct {
		Connection string `json:"connection"`
		Repo       string `json:"repo"`
	}](ctx, s.db, "SELECT connection, repo FROM repo_connection", nil)
	if err != nil {
		return nil, err
	}
	conns := map[string][]string{}
	for _, m := range (*memb)[0].Result {
		conns[m.Repo] = append(conns[m.Repo], m.Connection)
	}
	jobs, err := s.ListJobs(ctx, JobIndex, "")
	if err != nil {
		return nil, err
	}
	latest := map[string]*Job{}
	for i := range jobs {
		j := jobs[i]
		if cur, ok := latest[j.Target]; !ok || j.CreatedAt.After(cur.CreatedAt) {
			latest[j.Target] = &j
		}
	}

	statuses := make([]RepoStatus, len(repos))
	for i, r := range repos {
		statuses[i] = RepoStatus{
			Repo:         r,
			Connections:  conns[r.Name],
			Orphaned:     len(conns[r.Name]) == 0,
			LastIndexJob: latest[r.Name],
		}
	}
	return statuses, nil
}

// --- jobs ---

// jobRec pairs Job's fields with the SurrealDB record id for decoding.
type jobRec struct {
	Job
	RecID *models.RecordID `json:"id"`
}

func (j jobRec) toJob(kind JobKind) Job {
	out := j.Job
	out.Kind = kind
	if j.RecID != nil {
		out.ID = j.RecID.String()
	}
	return out
}

func (s *Surreal) CreateJob(ctx context.Context, kind JobKind, target string) (*Job, error) {
	created := time.Now().UTC()
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		`CREATE type::table($table) CONTENT {
			target: $target, status: 'pending', attempts: 0,
			created_at: $created, pending_key: $target, force: false
		}`,
		map[string]any{"table": string(kind), "target": target, "created": created})
	if err != nil {
		return nil, err
	}
	rows := (*results)[0].Result
	if len(rows) == 0 {
		return nil, fmt.Errorf("create %s: empty result", kind)
	}
	out := rows[0].toJob(kind)
	return &out, nil
}

const enqueuePendingSQL = `
BEGIN;
LET $pending = (SELECT id, created_at FROM type::table($table)
    WHERE pending_key = $target AND status = 'pending'
    ORDER BY created_at LIMIT 1)[0].id;
RETURN IF $pending != NONE THEN
    (UPDATE $pending SET force = IF $force THEN true ELSE force END RETURN AFTER)
ELSE
    (CREATE type::table($table) CONTENT {
        target: $target,
        status: 'pending',
        attempts: 0,
        created_at: time::now(),
        pending_key: $target,
        force: $force
    } RETURN AFTER)
END;
COMMIT;`

const maxQueueRetries = 64

// EnqueuePending atomically ensures that target has one pending job. An
// already-running job is deliberately ignored: the pending row is its single
// successor, preserving events that arrive after the worker took its snapshot.
func (s *Surreal) EnqueuePending(ctx context.Context, kind JobKind, target string, force bool) (*Job, error) {
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]jobRec](ctx, s.db, enqueuePendingSQL,
			map[string]any{"table": string(kind), "target": target, "force": force})
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, err
		}
		rows := firstNonEmpty(results)
		if len(rows) == 0 {
			return nil, fmt.Errorf("enqueue %s %q: empty result", kind, target)
		}
		job := rows[0].toJob(kind)
		return &job, nil
	}
}

func (s *Surreal) ListJobs(ctx context.Context, kind JobKind, status JobStatus) ([]Job, error) {
	sql := "SELECT * FROM type::table($table) ORDER BY created_at"
	vars := map[string]any{"table": string(kind)}
	if status != "" {
		sql = "SELECT * FROM type::table($table) WHERE status = $status ORDER BY created_at"
		vars["status"] = string(status)
	}
	results, err := surrealdb.Query[[]jobRec](ctx, s.db, sql, vars)
	if err != nil {
		return nil, err
	}
	rows := (*results)[0].Result
	jobs := make([]Job, len(rows))
	for i, r := range rows {
		jobs[i] = r.toJob(kind)
	}
	return jobs, nil
}

// claimSQL is the T1.3 spike winner: optimistic conditional update, no
// explicit transaction. The UPDATE re-checks status = 'pending' so a lost
// race returns empty instead of double-claiming; losing costs one cheap read
// versus a server-side transaction abort (see PLAN.md 2026-07-09 ADR).
const claimSQL = `
LET $cand = (SELECT id, created_at FROM type::table($table)
    WHERE status = 'pending' AND (not_before = NONE OR not_before <= time::now())
    ORDER BY created_at LIMIT 1)[0].id;
RETURN IF $cand != NONE THEN
    (UPDATE $cand SET status = 'claimed', claimed_by = $who, lease_token = $lease, pending_key = NONE,
     claimed_at = time::now(), heartbeat_at = time::now()
     WHERE status = 'pending' RETURN AFTER)
ELSE [] END;`

// ClaimJob atomically claims the oldest pending job of kind for who. It
// retries internally on lost races and returns ErrNotFound once no pending
// jobs remain.
func (s *Surreal) ClaimJob(ctx context.Context, kind JobKind, who string) (*Job, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lease, err := newLeaseToken()
		if err != nil {
			return nil, err
		}
		res, err := surrealdb.Query[[]jobRec](ctx, s.db, claimSQL,
			map[string]any{"table": string(kind), "who": who, "lease": lease})
		if err != nil {
			if isRetryable(err) {
				continue
			}
			return nil, err
		}
		rows := firstNonEmpty(res)
		if len(rows) > 0 {
			j := rows[0].toJob(kind)
			return &j, nil
		}
		n, err := s.countPending(ctx, kind)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("no pending %s: %w", kind, ErrNotFound)
		}
	}
}

func newLeaseToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate job lease: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}

// firstNonEmpty returns the first statement result carrying rows; the RETURN
// statement's position differs with and without BEGIN..COMMIT.
func firstNonEmpty(res *[]surrealdb.QueryResult[[]jobRec]) []jobRec {
	for _, r := range *res {
		if len(r.Result) > 0 {
			return r.Result
		}
	}
	return nil
}

func isRetryable(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "conflict") || strings.Contains(msg, "retry")
}

func isRetryableEnqueue(err error) bool {
	if isRetryable(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "already contains")
}

func (s *Surreal) countPending(ctx context.Context, kind JobKind) (int, error) {
	res, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](ctx, s.db,
		"SELECT count() AS count FROM type::table($table) WHERE status = 'pending' AND (not_before = NONE OR not_before <= time::now()) GROUP ALL",
		map[string]any{"table": string(kind)})
	if err != nil {
		return 0, err
	}
	rows := (*res)[0].Result
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Count, nil
}

// HeartbeatJob refreshes a running lease. A zero-row update is never benign:
// the row may have been reaped and reclaimed, so the old worker must stop.
func (s *Surreal) HeartbeatJob(ctx context.Context, job Job) error {
	return s.updateLease(ctx, job,
		"UPDATE type::record($id) SET heartbeat_at = time::now() WHERE status = 'running' AND lease_token = $lease AND claimed_by = $who RETURN AFTER",
		nil)
}

// RequeueJob returns a failed execution to the queue with attempts+1 and a
// backoff gate; claim state is cleared.
func (s *Surreal) RequeueJob(ctx context.Context, job Job, errMsg string, notBefore time.Time) error {
	return s.returnToPending(ctx, job, errMsg, notBefore, true, nil)
}

// ReleaseJob is the shutdown path: work returns to pending immediately without
// being counted as a failed attempt.
func (s *Surreal) ReleaseJob(ctx context.Context, job Job, errMsg string) error {
	return s.returnToPending(ctx, job, errMsg, time.Time{}, false, nil)
}

type heartbeatFence struct {
	observed time.Time
	cutoff   time.Time
}

// Compare heartbeat instants as epoch nanoseconds. Plain time.Time query
// parameters do not round-trip as Surreal datetime values for equality.
const returnToPendingSQL = `
BEGIN;
LET $owned = (SELECT id FROM type::record($id)
    WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who
    AND ($reaping = false OR (time::nano(heartbeat_at) = $heartbeat_nanos AND heartbeat_at < $cutoff)))[0].id;
LET $successor = (SELECT id, created_at FROM type::table($table)
    WHERE pending_key = $target AND status = 'pending'
    ORDER BY created_at LIMIT 1)[0].id;
LET $merged = IF $owned != NONE AND $successor != NONE THEN
    (UPDATE $successor SET
        force = IF $force THEN true ELSE force END,
        attempts = IF $increment THEN $attempts ELSE attempts END,
        error = $err,
        not_before = IF $increment THEN $nb ELSE NONE END
     RETURN AFTER)
ELSE [] END;
RETURN IF $owned = NONE THEN []
ELSE IF $successor != NONE THEN
    (UPDATE type::record($id) SET status = 'canceled', error = $superseded,
        finished_at = time::now(), lease_token = NONE, pending_key = NONE
     WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who
     AND ($reaping = false OR (time::nano(heartbeat_at) = $heartbeat_nanos AND heartbeat_at < $cutoff))
     RETURN AFTER)
ELSE
    (UPDATE type::record($id) SET status = 'pending', attempts = $attempts, error = $err,
        not_before = IF $increment THEN $nb ELSE NONE END,
        claimed_by = NONE, claimed_at = NONE, heartbeat_at = NONE,
        lease_token = NONE, finished_at = NONE, pending_key = $target
     WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who
     AND ($reaping = false OR (time::nano(heartbeat_at) = $heartbeat_nanos AND heartbeat_at < $cutoff))
     RETURN AFTER)
END;
COMMIT;`

func (s *Surreal) returnToPending(ctx context.Context, job Job, errMsg string, notBefore time.Time, increment bool, fence *heartbeatFence) error {
	if job.ID == "" || job.LeaseToken == "" || job.ClaimedBy == "" {
		return fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
	}
	vars := map[string]any{
		"id":              job.ID,
		"table":           string(job.Kind),
		"target":          job.Target,
		"lease":           job.LeaseToken,
		"who":             job.ClaimedBy,
		"force":           job.Force,
		"attempts":        job.Attempts + btoi(increment),
		"increment":       increment,
		"err":             errMsg,
		"superseded":      "superseded by pending successor: " + errMsg,
		"nb":              notBefore,
		"reaping":         fence != nil,
		"heartbeat_nanos": int64(0),
		"cutoff":          time.Time{},
	}
	if fence != nil {
		vars["heartbeat_nanos"] = fence.observed.UnixNano()
		vars["cutoff"] = fence.cutoff
	}
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]jobRec](ctx, s.db, returnToPendingSQL, vars)
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return err
		}
		if !queryContainsJob(results, job.ID) {
			return fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
		}
		return nil
	}
}

func queryContainsJob(results *[]surrealdb.QueryResult[[]jobRec], id string) bool {
	for _, result := range *results {
		for _, row := range result.Result {
			if row.RecID != nil && row.RecID.String() == id {
				return true
			}
		}
	}
	return false
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Surreal) CancelPendingJobs(ctx context.Context, kind JobKind, target string) (int, error) {
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		`UPDATE type::table($table) SET status = 'canceled', error = 'repository deleting',
		 finished_at = time::now(), not_before = NONE, pending_key = NONE
		 WHERE target = $target AND status = 'pending' RETURN AFTER`,
		map[string]any{"table": string(kind), "target": target})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, result := range *results {
		n += len(result.Result)
	}
	return n, nil
}

// ReapStale rescues jobs whose worker died: claimed/running rows without a
// recent heartbeat go back to pending (attempts+1), or to failed once
// maxAttempts is exhausted. Returns how many rows it touched.
// ponytail: staleness cutoff computed on the Go clock vs server-side
// heartbeats — same host today (supervised child); revisit for fleet mode.
func (s *Surreal) ReapStale(ctx context.Context, kind JobKind, staleAfter time.Duration, maxAttempts int) (int, error) {
	cutoff := time.Now().UTC().Add(-staleAfter)
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		`SELECT * FROM type::table($table)
		 WHERE status IN ['claimed', 'running'] AND heartbeat_at != NONE AND heartbeat_at < $cutoff
		 ORDER BY heartbeat_at`,
		map[string]any{"table": string(kind), "cutoff": cutoff})
	if err != nil {
		return 0, err
	}
	rows := (*results)[0].Result
	n, varErr := 0, error(nil)
	for _, row := range rows {
		job := row.toJob(kind)
		err := s.reapOne(ctx, job, cutoff, maxAttempts)
		switch {
		case err == nil:
			n++
		case errors.Is(err, ErrLeaseLost):
			// The worker recovered or another reaper won the race.
		default:
			if varErr == nil {
				varErr = err
			}
		}
	}
	return n, varErr
}

func (s *Surreal) reapOne(ctx context.Context, job Job, cutoff time.Time, maxAttempts int) error {
	if job.HeartbeatAt == nil {
		return fmt.Errorf("job %q has no observed heartbeat: %w", job.ID, ErrLeaseLost)
	}
	fence := &heartbeatFence{observed: *job.HeartbeatAt, cutoff: cutoff}
	if job.Attempts+1 >= maxAttempts {
		return s.updateLease(ctx, job,
			`UPDATE type::record($id) SET status = 'failed',
			 error = 'reaped: stale claim, attempts exhausted', finished_at = time::now(),
			 lease_token = NONE, pending_key = NONE
			 WHERE status IN ['claimed', 'running'] AND lease_token = $lease AND claimed_by = $who
			 AND time::nano(heartbeat_at) = $heartbeat_nanos AND heartbeat_at < $cutoff
			 RETURN AFTER`,
			map[string]any{"heartbeat_nanos": fence.observed.UnixNano(), "cutoff": fence.cutoff})
	}
	return s.returnToPending(ctx, job, "reaped: stale claim", time.Now().UTC(), true, fence)
}

func (s *Surreal) SetJobStatus(ctx context.Context, job Job, status JobStatus, errMsg string) error {
	var sql string
	switch status {
	case StatusRunning:
		sql = `UPDATE type::record($id) SET status = 'running', error = $err
			WHERE status = 'claimed' AND lease_token = $lease AND claimed_by = $who RETURN AFTER`
	case StatusDone, StatusFailed:
		sql = `UPDATE type::record($id) SET status = $status, error = $err,
			finished_at = time::now(), lease_token = NONE, pending_key = NONE
			WHERE status = 'running' AND lease_token = $lease AND claimed_by = $who RETURN AFTER`
	default:
		return fmt.Errorf("invalid leased job transition to %q", status)
	}
	return s.updateLease(ctx, job, sql,
		map[string]any{"status": string(status), "err": errMsg})
}

func (s *Surreal) updateLease(ctx context.Context, job Job, sql string, extra map[string]any) error {
	if job.ID == "" || job.LeaseToken == "" || job.ClaimedBy == "" {
		return fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
	}
	vars := map[string]any{"id": job.ID, "lease": job.LeaseToken, "who": job.ClaimedBy}
	for key, value := range extra {
		vars[key] = value
	}
	results, err := surrealdb.Query[[]jobRec](ctx, s.db, sql, vars)
	if err != nil {
		return err
	}
	if len(firstNonEmpty(results)) == 0 {
		return fmt.Errorf("job %q: %w", job.ID, ErrLeaseLost)
	}
	return nil
}
