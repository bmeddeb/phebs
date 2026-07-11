// Package store is the SurrealDB-backed state and job-queue layer (T1.2, T1.3).
//
// Store is an interface to keep the Postgres exit open (PLAN §3); Surreal is
// the only implementation. Job tables double as the polling queue — the
// claim statement lands with T1.3.
package store

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

type JobKind string

// Job kinds name their SurrealDB table directly.
const (
	JobSync  JobKind = "connection_sync_job"
	JobIndex JobKind = "indexing_job"
	JobFetch JobKind = "repo_fetch_job" // webhook-driven single-repo fetch (T7.4)
)

type JobStatus string

const (
	StatusPending JobStatus = "pending"
	StatusClaimed JobStatus = "claimed"
	StatusRunning JobStatus = "running"
	StatusDone    JobStatus = "done"
	StatusFailed  JobStatus = "failed"
)

// Repo mirrors the upstream Repo model's P1 fields (PORT_MAP §5).
type Repo struct {
	Name              string         `json:"name"` // unique, e.g. "github.com/foo/bar"
	DisplayName       string         `json:"display_name,omitempty"`
	CloneURL          string         `json:"clone_url"`
	WebURL            string         `json:"web_url,omitempty"`
	DefaultBranch     string         `json:"default_branch,omitempty"`
	IsFork            bool           `json:"is_fork"`
	IsArchived        bool           `json:"is_archived"`
	IsPublic          bool           `json:"is_public"`
	Metadata          map[string]any `json:"metadata,omitempty"` // schemaless until T2.2 decides typing
	IndexedAt         *time.Time     `json:"indexed_at,omitempty"`
	IndexedCommitHash string         `json:"indexed_commit_hash,omitempty"`
	LatestJobStatus   string         `json:"latest_indexing_job_status,omitempty"`
	PushedAt          *time.Time     `json:"pushed_at,omitempty"`
	ExternalID        string         `json:"external_id,omitempty"`
	ExternalHostType  string         `json:"external_code_host_type,omitempty"`
	ExternalHostURL   string         `json:"external_code_host_url,omitempty"`
}

// Job is one row in a job table. Target is the connection name for sync jobs
// and the repo name for indexing jobs.
type Job struct {
	ID          string     `json:"-"` // "table:key" record id, set on read
	Kind        JobKind    `json:"-"`
	Target      string     `json:"target"`
	Status      JobStatus  `json:"status"`
	Attempts    int        `json:"attempts"` // executions so far
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	NotBefore   *time.Time `json:"not_before,omitempty"` // backoff gate for claims
	ClaimedBy   string     `json:"claimed_by,omitempty"`
	ClaimedAt   *time.Time `json:"claimed_at,omitempty"`
	HeartbeatAt *time.Time `json:"heartbeat_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// RepoStatus is a repo row annotated with connection membership and the
// most recent indexing job — the /api/repo-status shape.
type RepoStatus struct {
	Repo
	Orphaned     bool     `json:"orphaned"` // no connection claims this repo
	Connections  []string `json:"connections,omitempty"`
	LastIndexJob *Job     `json:"last_index_job,omitempty"`
}

// Store is the persistence boundary. IDs cross it as opaque strings so no
// SurrealDB types leak into callers.
type Store interface {
	UpsertRepo(ctx context.Context, r Repo) error
	GetRepo(ctx context.Context, name string) (*Repo, error) // ErrNotFound when absent
	ListRepos(ctx context.Context) ([]Repo, error)
	DeleteRepo(ctx context.Context, name string) error

	// SetRepoIndexed records a successful index run without touching the
	// sync-owned fields of the row; ClearRepoIndexState wipes the recorded
	// commit so the next index run rebuilds (the force path).
	SetRepoIndexed(ctx context.Context, name, commitHash string, at time.Time) error
	ClearRepoIndexState(ctx context.Context, name string) error

	// SetRepoConnections replaces conn's membership set; PruneConnections
	// drops membership of connections no longer configured.
	SetRepoConnections(ctx context.Context, conn string, repos []string) error
	PruneConnections(ctx context.Context, keep []string) error
	RepoStatuses(ctx context.Context) ([]RepoStatus, error)
	// GetRepoConnections lists the connections claiming one repo (the
	// T7.4 fetch path resolves credentials through it).
	GetRepoConnections(ctx context.Context, repo string) ([]string, error)

	CreateJob(ctx context.Context, kind JobKind, target string) (*Job, error)
	ListJobs(ctx context.Context, kind JobKind, status JobStatus) ([]Job, error) // status "" = all
	ClaimJob(ctx context.Context, kind JobKind, who string) (*Job, error) // ErrNotFound when nothing claimable
	SetJobStatus(ctx context.Context, id string, status JobStatus, errMsg string) error
	HeartbeatJob(ctx context.Context, id string) error
	RequeueJob(ctx context.Context, id string, errMsg string, notBefore time.Time) error // attempts+1, back to pending
	ReapStale(ctx context.Context, kind JobKind, staleAfter time.Duration, maxAttempts int) (int, error)

	Close(ctx context.Context) error
}

// EnqueueUnlessInFlight creates a job unless one is already pending, claimed,
// or running for the same target.
func EnqueueUnlessInFlight(ctx context.Context, st Store, kind JobKind, target string) error {
	return enqueueUnless(ctx, st, kind, target,
		StatusPending, StatusClaimed, StatusRunning)
}

// EnqueueUnlessPending creates a job unless one is already *pending* for the
// same target. Unlike EnqueueUnlessInFlight it does enqueue while a job for
// the target is claimed/running — events that arrive mid-run (a push during
// an in-flight fetch) must not be lost to the dedup (T7.4 review).
func EnqueueUnlessPending(ctx context.Context, st Store, kind JobKind, target string) error {
	return enqueueUnless(ctx, st, kind, target, StatusPending)
}

func enqueueUnless(ctx context.Context, st Store, kind JobKind, target string, statuses ...JobStatus) error {
	for _, status := range statuses {
		jobs, err := st.ListJobs(ctx, kind, status)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			if j.Target == target {
				return nil
			}
		}
	}
	_, err := st.CreateJob(ctx, kind, target)
	return err
}
