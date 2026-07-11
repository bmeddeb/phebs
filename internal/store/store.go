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

var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrLeaseLost = errors.New("job lease lost")
)

type JobKind string

// Job kinds name their SurrealDB table directly.
const (
	JobSync  JobKind = "connection_sync_job"
	JobIndex JobKind = "indexing_job"
	JobFetch JobKind = "repo_fetch_job" // webhook-driven single-repo fetch (T7.4)
)

type JobStatus string

const (
	StatusPending  JobStatus = "pending"
	StatusClaimed  JobStatus = "claimed"
	StatusRunning  JobStatus = "running"
	StatusDone     JobStatus = "done"
	StatusFailed   JobStatus = "failed"
	StatusCanceled JobStatus = "canceled"
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
	Deleting          bool           `json:"deleting,omitempty"`
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
	Force       bool       `json:"force,omitempty"`
	LeaseToken  string     `json:"-" cbor:"lease_token,omitempty"`
}

// RepoStatus is a repo row annotated with connection membership and the
// most recent indexing job — the /api/repo-status shape.
type RepoStatus struct {
	Repo
	Orphaned     bool     `json:"orphaned"` // no connection claims this repo
	Connections  []string `json:"connections,omitempty"`
	LastIndexJob *Job     `json:"last_index_job,omitempty"`
}

// User is the shared identity behind local-password and OIDC logins. Secret
// material is represented only by a one-way password hash.
type User struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	NormalizedEmail string     `json:"normalized_email"`
	DisplayName     string     `json:"display_name,omitempty"`
	PasswordHash    string     `json:"-"`
	OIDCIssuer      string     `json:"-"`
	OIDCSubject     string     `json:"-"`
	IsAdmin         bool       `json:"is_admin"`
	Disabled        bool       `json:"disabled"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
}

// APIKey stores only a SHA-256 digest of a generated high-entropy bearer key.
// ID and Prefix are safe for management UIs; Hash is never serialized.
type APIKey struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id,omitempty"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Hash       string     `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// AuditEvent is one append-only record of an admin or user action (T10.1).
// Actor fields are empty for unauthenticated actions (e.g. failed logins).
type AuditEvent struct {
	ID         string    `json:"id"`
	Action     string    `json:"action"`               // "auth.login", "post-api-reindex", …
	Target     string    `json:"target,omitempty"`     // action-specific: repo name, key id, email
	ActorID    string    `json:"actor_id,omitempty"`   // user record id
	ActorEmail string    `json:"actor_email,omitempty"`
	APIKeyID   string    `json:"api_key_id,omitempty"`
	AuthMethod string    `json:"auth_method,omitempty"` // "session" | "api_key" | ""
	SourceIP   string    `json:"source_ip,omitempty"`
	Status     int       `json:"status"` // HTTP status the action resolved to
	CreatedAt  time.Time `json:"created_at"`
}

// AuditStore is separate from Store for the same reason as AuthStore: test
// doubles elsewhere must not grow methods. Append-only is enforced Go-side by
// not exposing update or single-row delete methods.
type AuditStore interface {
	AppendAuditEvent(ctx context.Context, event AuditEvent) error
	// ListAuditEvents returns events newest-first.
	ListAuditEvents(ctx context.Context, offset, limit int) ([]AuditEvent, error)
	// PruneAuditEvents deletes events created at or before cutoff (retention).
	PruneAuditEvents(ctx context.Context, cutoff time.Time) (int, error)
}

// UsageEvent is one local usage record (T10.2). phebs never phones home:
// events exist only in the local database and feed the local dashboard.
type UsageEvent struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // "search"
	ActorID    string    `json:"actor_id,omitempty"`
	APIKeyID   string    `json:"api_key_id,omitempty"`
	Repos      []string  `json:"repos,omitempty"` // distinct repos with matches, capped
	MatchCount int       `json:"match_count"`
	FileCount  int       `json:"file_count"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// AnalyticsStore records and reads local usage events; aggregation happens
// in the caller (single-node scale, see api.registerAnalytics).
type AnalyticsStore interface {
	RecordUsageEvent(ctx context.Context, event UsageEvent) error
	// ListUsageEvents returns events created after since, oldest first.
	ListUsageEvents(ctx context.Context, since time.Time) ([]UsageEvent, error)
	PruneUsageEvents(ctx context.Context, cutoff time.Time) (int, error)
}

// AuthStats drives the public auth status and the one-time setup gate.
type AuthStats struct {
	Users         int
	PasswordUsers int
	SetupComplete bool
}

// AuthStore is deliberately separate from Store so existing sync/search test
// doubles do not need authentication methods. Surreal implements both.
type AuthStore interface {
	AuthStats(ctx context.Context) (AuthStats, error)
	CreateFirstUser(ctx context.Context, user User) (*User, error)
	CreateUser(ctx context.Context, user User) (*User, error)
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUserByEmail(ctx context.Context, normalizedEmail string) (*User, error)
	// UpsertOIDCUser finds an exact issuer/subject identity or creates one;
	// an email collision never links accounts implicitly.
	UpsertOIDCUser(ctx context.Context, issuer, subject, email, normalizedEmail, displayName string, emailVerified bool) (*User, error)
	MarkUserLogin(ctx context.Context, id string, at time.Time) error

	CreateAPIKey(ctx context.Context, key APIKey) (*APIKey, error)
	GetAPIKey(ctx context.Context, id string) (*APIKey, error)
	ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error)
	RevokeAPIKey(ctx context.Context, id, userID string, at time.Time) error
	TouchAPIKey(ctx context.Context, id string, at time.Time) error
	SetLegacyAPIKey(ctx context.Context, hash string, at time.Time) error

	CommitAuthSession(ctx context.Context, tokenHash string, data []byte, expiry time.Time) error
	FindAuthSession(ctx context.Context, tokenHash string, now time.Time) ([]byte, bool, error)
	DeleteAuthSession(ctx context.Context, tokenHash string) error
	DeleteExpiredAuthSessions(ctx context.Context, now time.Time) (int, error)
}

// Store is the persistence boundary. IDs cross it as opaque strings so no
// SurrealDB types leak into callers.
type Store interface {
	UpsertRepo(ctx context.Context, r Repo) error
	GetRepo(ctx context.Context, name string) (*Repo, error) // ErrNotFound when absent
	ListRepos(ctx context.Context) ([]Repo, error)
	DeleteRepo(ctx context.Context, name string) error
	SetRepoDeleting(ctx context.Context, name string, deleting bool) error

	// SetRepoIndexed records a successful index run without touching the
	// sync-owned fields of the row. ClearRepoIndexState remains available for
	// repair tooling; normal forced rebuilds travel on Job.Force.
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
	// EnqueuePending atomically creates at most one pending job for target.
	// Calls made while a job is claimed/running create one pending successor;
	// force upgrades an existing pending job and is never downgraded.
	EnqueuePending(ctx context.Context, kind JobKind, target string, force bool) (*Job, error)
	ListJobs(ctx context.Context, kind JobKind, status JobStatus) ([]Job, error) // status "" = all
	ClaimJob(ctx context.Context, kind JobKind, who string) (*Job, error)        // ErrNotFound when nothing claimable
	SetJobStatus(ctx context.Context, job Job, status JobStatus, errMsg string) error
	HeartbeatJob(ctx context.Context, job Job) error
	RequeueJob(ctx context.Context, job Job, errMsg string, notBefore time.Time) error // attempts+1, back to pending
	ReleaseJob(ctx context.Context, job Job, errMsg string) error                      // back to pending without consuming an attempt
	CancelPendingJobs(ctx context.Context, kind JobKind, target string) (int, error)
	ReapStale(ctx context.Context, kind JobKind, staleAfter time.Duration, maxAttempts int) (int, error)

	Close(ctx context.Context) error
}

// EnqueuePending persists a freshness event without allowing duplicate pending
// successors. force is meaningful for index jobs and harmless for other kinds.
func EnqueuePending(ctx context.Context, st Store, kind JobKind, target string, force bool) error {
	_, err := st.EnqueuePending(ctx, kind, target, force)
	return err
}

// EnqueueUnlessInFlight is retained for existing callers. It now uses the
// lossless pending-successor behavior of EnqueuePending.
func EnqueueUnlessInFlight(ctx context.Context, st Store, kind JobKind, target string) error {
	return EnqueuePending(ctx, st, kind, target, false)
}

// EnqueueUnlessPending is the non-force compatibility alias.
func EnqueueUnlessPending(ctx context.Context, st Store, kind JobKind, target string) error {
	return EnqueuePending(ctx, st, kind, target, false)
}
