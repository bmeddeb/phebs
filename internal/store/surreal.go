package store

import (
	"context"
	_ "embed"
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
		"UPSERT $rid CONTENT $repo",
		map[string]any{"rid": repoID(r.Name), "repo": r})
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
		"DELETE $rid", map[string]any{"rid": repoID(name)})
	return err
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
	job := Job{Target: target, Status: StatusPending, CreatedAt: time.Now().UTC()}
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		"CREATE type::table($table) CONTENT $job",
		map[string]any{"table": string(kind), "job": job})
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
    (UPDATE $cand SET status = 'claimed', claimed_by = $who, claimed_at = time::now(), heartbeat_at = time::now()
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
		res, err := surrealdb.Query[[]jobRec](ctx, s.db, claimSQL,
			map[string]any{"table": string(kind), "who": who})
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

// HeartbeatJob refreshes the liveness stamp of an in-flight job. A no-op if
// the job already reached a terminal state (races with completion are benign).
func (s *Surreal) HeartbeatJob(ctx context.Context, id string) error {
	_, err := surrealdb.Query[any](ctx, s.db,
		"UPDATE type::record($id) SET heartbeat_at = time::now() WHERE status IN ['claimed', 'running']",
		map[string]any{"id": id})
	return err
}

// RequeueJob returns a failed execution to the queue with attempts+1 and a
// backoff gate; claim state is cleared.
func (s *Surreal) RequeueJob(ctx context.Context, id string, errMsg string, notBefore time.Time) error {
	results, err := surrealdb.Query[[]jobRec](ctx, s.db,
		`UPDATE type::record($id) SET status = 'pending', attempts += 1, error = $err,
		 not_before = $nb, claimed_by = NONE, claimed_at = NONE, heartbeat_at = NONE RETURN AFTER`,
		map[string]any{"id": id, "err": errMsg, "nb": notBefore})
	if err != nil {
		return err
	}
	if len((*results)[0].Result) == 0 {
		return fmt.Errorf("job %q: %w", id, ErrNotFound)
	}
	return nil
}

// ReapStale rescues jobs whose worker died: claimed/running rows without a
// recent heartbeat go back to pending (attempts+1), or to failed once
// maxAttempts is exhausted. Returns how many rows it touched.
// ponytail: staleness cutoff computed on the Go clock vs server-side
// heartbeats — same host today (supervised child); revisit for fleet mode.
func (s *Surreal) ReapStale(ctx context.Context, kind JobKind, staleAfter time.Duration, maxAttempts int) (int, error) {
	cutoff := time.Now().UTC().Add(-staleAfter)
	vars := map[string]any{"table": string(kind), "cutoff": cutoff, "max": maxAttempts}
	results, err := surrealdb.Query[[]jobRec](ctx, s.db, `
UPDATE type::table($table) SET status = 'pending', attempts += 1, error = 'reaped: stale claim',
    claimed_by = NONE, claimed_at = NONE, heartbeat_at = NONE
    WHERE status IN ['claimed', 'running'] AND heartbeat_at != NONE AND heartbeat_at < $cutoff AND attempts + 1 < $max
    RETURN AFTER;
UPDATE type::table($table) SET status = 'failed', error = 'reaped: stale claim, attempts exhausted', finished_at = time::now()
    WHERE status IN ['claimed', 'running'] AND heartbeat_at != NONE AND heartbeat_at < $cutoff
    RETURN AFTER;`, vars)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range *results {
		n += len(r.Result)
	}
	return n, nil
}

func (s *Surreal) SetJobStatus(ctx context.Context, id string, status JobStatus, errMsg string) error {
	sql := "UPDATE type::record($id) SET status = $status, error = $err"
	if status == StatusDone || status == StatusFailed {
		sql += ", finished_at = time::now()"
	}
	results, err := surrealdb.Query[[]jobRec](ctx, s.db, sql,
		map[string]any{"id": id, "status": string(status), "err": errMsg})
	if err != nil {
		return err
	}
	if len((*results)[0].Result) == 0 {
		return fmt.Errorf("job %q: %w", id, ErrNotFound)
	}
	return nil
}
