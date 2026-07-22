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
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrLeaseLost   = errors.New("job lease lost")
	ErrResultLimit = errors.New("result limit exceeded")
)

type JobKind string

// Job kinds name their SurrealDB table directly.
const (
	JobSync    JobKind = "connection_sync_job"
	JobIndex   JobKind = "indexing_job"
	JobFetch   JobKind = "repo_fetch_job" // webhook-driven single-repo fetch (T7.4)
	JobExtract JobKind = "extraction_job" // evidence extraction, chained after indexing (T12.2)
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
	Name              string            `json:"name"` // unique, e.g. "github.com/foo/bar"
	DisplayName       string            `json:"display_name,omitempty"`
	CloneURL          string            `json:"clone_url"`
	WebURL            string            `json:"web_url,omitempty"`
	DefaultBranch     string            `json:"default_branch,omitempty"`
	IsFork            bool              `json:"is_fork"`
	IsArchived        bool              `json:"is_archived"`
	IsPublic          bool              `json:"is_public"`
	Metadata          map[string]any    `json:"metadata,omitempty"` // schemaless until T2.2 decides typing
	IndexedAt         *time.Time        `json:"indexed_at,omitempty"`
	IndexedCommitHash string            `json:"indexed_commit_hash,omitempty"`
	IndexedRevisions  []IndexedRevision `json:"indexed_revisions,omitempty"`
	LatestJobStatus   string            `json:"latest_indexing_job_status,omitempty"`
	PushedAt          *time.Time        `json:"pushed_at,omitempty"`
	ExternalID        string            `json:"external_id,omitempty"`
	ExternalHostType  string            `json:"external_code_host_type,omitempty"`
	ExternalHostURL   string            `json:"external_code_host_url,omitempty"`
	Deleting          bool              `json:"deleting,omitempty"`
}

// IndexedRevision is one atomically published zoekt branch. Selector is the
// user-facing rev: value, Branch is the exact name stored in shard metadata,
// and Commit is the immutable version search results must match.
type IndexedRevision struct {
	Selector string `json:"selector"`
	Branch   string `json:"branch"`
	Commit   string `json:"commit"`
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
	Action     string    `json:"action"`             // "auth.login", "post-api-reindex", …
	Target     string    `json:"target,omitempty"`   // action-specific: repo name, key id, email
	ActorID    string    `json:"actor_id,omitempty"` // user record id
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

// PermissionStore mirrors code-host ACLs as repo↔identity edges (T10.3).
// Identities are lower-cased "<host>:<login>" strings; the sync adapters
// write them and the per-user search pre-pass reads them.
type PermissionStore interface {
	// SetRepoPermissions transactionally replaces one repo's granted
	// identity set, so a shrunken host ACL revokes atomically.
	SetRepoPermissions(ctx context.Context, repo string, identities []string) error
	// ListPermittedRepos returns the repo names granted to any identity.
	ListPermittedRepos(ctx context.Context, identities []string) ([]string, error)
	DeleteRepoPermissions(ctx context.Context, repo string) error
}

// Confidence tiers are deterministic (PLAN ADR 2026-07-12); numeric
// confidence is deliberately absent until calibrated on blind holdouts.
const (
	TierExact      = "exact"
	TierDerived    = "derived"
	TierHeuristic  = "heuristic"
	TierUnresolved = "unresolved"
)

// EvidenceAtom is content identity: identical vendored blobs across commits
// and repositories dedupe to one atom. Repository placement — and therefore
// authorization — never lives here (SnapshotEvidence carries it).
type EvidenceAtom struct {
	ID                  string    `json:"atom_id"` // "ea_" + sha256 over the length-delimited fields
	SchemaVersion       string    `json:"schema_version"`
	BlobDigest          string    `json:"blob_digest"`
	StartByte           int       `json:"start_byte"`
	EndByte             int       `json:"end_byte"`
	RuleID              string    `json:"rule_id"`
	ExtractorVersion    string    `json:"extractor_version"`
	AdapterConfigDigest string    `json:"adapter_config_digest"`
	FactFingerprint     string    `json:"fact_fingerprint"`
	FirstSeen           time.Time `json:"first_seen"`
}

// SnapshotEvidence places one atom in one repository snapshot. last_verified
// for an atom derives from its associations; compacting associations never
// deletes shared atoms.
type SnapshotEvidence struct {
	ID              string    `json:"occurrence_id"` // stable placement identity; excludes the extraction run
	AtomID          string    `json:"atom_id"`
	Repo            string    `json:"repo"`
	Commit          string    `json:"commit"`
	Path            string    `json:"path"`
	StartLine       int       `json:"start_line"`
	EndLine         int       `json:"end_line"`
	VisibilityScope string    `json:"visibility_scope"` // "repo:<name>" — evaluated per caller at read time
	RunID           string    `json:"run_id"`
	ObservedAt      time.Time `json:"observed_at"`
}

// Assertion is one semantic claim carrying supporting AND contradicting atom
// references. Subject semantics are predicate-specific; the declaration-plane
// extractor currently uses the repository-relative source path.
type Assertion struct {
	ID        string `json:"id"` // stable semantic identity; excludes evidence references and the extraction run
	Predicate string `json:"predicate"`
	Subject   string `json:"subject"`
	Object    string `json:"object"`
	// Lineage separates same-named contracts from unrelated descriptor sets
	// (ADR 2026-07-12: the complete field key is (contract_lineage_id,
	// message_full_name, field_number) — encoded here as (Lineage, Object)).
	// Derived conservatively; two lineages merge only when identity
	// resolution proves they share one, never by name equality.
	Lineage       string   `json:"lineage,omitempty"`
	Tier          string   `json:"tier"`
	CodeRole      string   `json:"code_role,omitempty"`
	Repo          string   `json:"repo"`
	Supporting    []string `json:"supporting"` // atom ids
	Contradicting []string `json:"contradicting,omitempty"`
	RunID         string   `json:"run_id"`
	Detail        string   `json:"detail,omitempty"`
}

// CoverageManifest is the per-run honesty record: what was attempted, what
// failed, what stayed unresolved. Every conclusion built on this run's
// assertions cites it ("no blockers found within the stated evidence scope").
type CoverageManifest struct {
	Protocols          []string `json:"protocols,omitempty"`
	Failures           []string `json:"failures,omitempty"` // extraction failures, load errors
	CorpusFileCount    int      `json:"corpus_file_count"`
	CandidateFileCount int      `json:"candidate_file_count"`
	ReadFileCount      int      `json:"read_file_count"`
	ReadBytes          int64    `json:"read_bytes"`
	SourceScopeDigest  string   `json:"source_scope_digest,omitempty"` // digest of sorted (path, blob digest, length)
	UnresolvedCount    int      `json:"unresolved_count"`
	AssertionCount     int      `json:"assertion_count"`
	AtomCount          int      `json:"atom_count"`
}

// ExtractionRun is the atomic publication unit: rows written under a staged
// run are invisible; PublishExtractionRun flips status in one transaction and
// supersedes the prior published run for (repo, domain). A failed or killed
// run therefore never publishes a partial replacement set.
type ExtractionRun struct {
	ID          string           `json:"id"`
	Repo        string           `json:"repo"`
	Commit      string           `json:"commit"`
	Domain      string           `json:"domain"` // e.g. "proto-contract"
	Extractor   string           `json:"extractor"`
	Status      string           `json:"status"` // staged | published | superseded | aborted
	StartedAt   time.Time        `json:"started_at"`
	PublishedAt *time.Time       `json:"published_at,omitempty"`
	Coverage    CoverageManifest `json:"coverage"`
}

// ExtractionAttempt is the durable latest-attempt marker for one repository
// and domain. Unlike an aborted ExtractionRun, it is not evidence and is not
// removed by proof-retention sweeps. It lets coverage reporting distinguish a
// healthy last publication from a newer staged or aborted replacement.
type ExtractionAttempt struct {
	RunID     string    `json:"run_id"`
	Repo      string    `json:"repo"`
	Commit    string    `json:"commit"`
	Domain    string    `json:"domain"`
	Extractor string    `json:"extractor"`
	Status    string    `json:"status"` // staged | published | aborted
	StartedAt time.Time `json:"started_at"`
}

// AssertionQuery filters published assertions. Repo is mandatory; the other
// empty fields match anything within that caller-authorized repository.
type AssertionQuery struct {
	Predicate string
	Subject   string
	Object    string
	Lineage   string
	Repo      string
	RunID     string // empty = any published run for Repo
	Limit     int    // 0 = default cap
}

// EvidenceResolution is the click-through from one assertion support id to
// its immutable content span and repository occurrence. Callers authorize the
// repository before invoking ResolveEvidence; the store also binds repo, run,
// and atom in one current-or-pinned-retained query.
type EvidenceResolution struct {
	Atom        EvidenceAtom       `json:"atom"`
	Occurrences []SnapshotEvidence `json:"occurrences"`
}

// ProofBundleRecord is the store's deliberately opaque representation of one
// immutable, canonical proof bundle. The API owns the JSON schema; the store
// verifies the content-derived ID, persists the exact bytes, and pins RunIDs.
type ProofBundleRecord struct {
	ID           string   `json:"id"`
	Content      string   `json:"content"`
	Repositories []string `json:"repositories"`
	RunIDs       []string `json:"run_ids"`
	// RetainedAt is lifecycle metadata, deliberately outside Content and its
	// digest. Re-materializing identical content may refresh it without
	// changing the immutable bundle ID or bytes.
	RetainedAt time.Time `json:"retained_at"`
}

// ProofBundleStore persists immutable bundles separately from the evidence
// read/write interface. Retrieval does not authorize: callers must re-check
// every repository in Repositories before disclosing Content.
type ProofBundleStore interface {
	PutProofBundle(ctx context.Context, bundle ProofBundleRecord) error
	// GetProofBundle treats records retained at or before activeAfter as
	// absent. A nil cutoff disables expiry.
	GetProofBundle(ctx context.Context, id string, activeAfter *time.Time) (*ProofBundleRecord, error)
}

// ProofBundleRetentionStore owns only bundle/pin lifecycle. It deliberately
// cannot delete extraction evidence; SweepEvidence remains the sole evidence
// reclaimer after pins are released.
type ProofBundleRetentionStore interface {
	// SweepProofBundles atomically deletes at most one bundle retained at or
	// before activeAfter and only that bundle's proof-bundle:<id> pins.
	SweepProofBundles(ctx context.Context, activeAfter time.Time) (int, error)
}

// EvidenceStore is the T12.1 provenance layer, separate from Store per the
// narrow-interface house style. Assertion readers only ever see published
// runs; evidence resolution additionally permits pinned superseded runs.
type EvidenceStore interface {
	BeginExtractionRun(ctx context.Context, repo, commit, domain, extractor string) (*ExtractionRun, error)
	// AddEvidence atomically upserts content-keyed atoms and their occurrence
	// associations/assertions under a staged run. Every association and atom
	// reference must be present in the same self-contained batch. Caller
	// timestamps and run ids are ignored/filled by the store. Exact retries and
	// non-conflicting support unions are idempotent; attribute or contradiction
	// conflicts fail the transaction.
	AddEvidence(ctx context.Context, runID string, atoms []EvidenceAtom, assocs []SnapshotEvidence, asserts []Assertion) error
	// PublishExtractionRun atomically verifies that the repository still
	// exists, is not deleting, and is indexed at the run's commit; validates
	// caller counts against stored rows; publishes the run; and supersedes the
	// previous published run for the same (repo, domain).
	PublishExtractionRun(ctx context.Context, runID string, coverage CoverageManifest) error
	AbortExtractionRun(ctx context.Context, runID string) error
	// LatestPublishedRun returns ErrNotFound when the (repo, domain) pair has
	// never published.
	LatestPublishedRun(ctx context.Context, repo, domain string) (*ExtractionRun, error)
	// LatestExtractionAttempt returns the durable latest attempt marker. It
	// survives evidence sweeps so a failed replacement remains reportable.
	LatestExtractionAttempt(ctx context.Context, repo, domain string) (*ExtractionAttempt, error)
	// ListAssertions reads assertions of published runs only. Repo is required;
	// callers must authorize that repository before invoking the method. A
	// result exceeding Limit fails with ErrResultLimit rather than truncating.
	ListAssertions(ctx context.Context, q AssertionQuery) ([]Assertion, error)
	// ResolveEvidence resolves one current or pinned-retained assertion support
	// id. Repo is a required authorization scope and must match the run and all
	// returned occurrences.
	ResolveEvidence(ctx context.Context, repo, runID, atomID string) (*EvidenceResolution, error)
	// PinRun idempotently exempts a published or superseded run from sweeps
	// (proof-bundle / checkpoint retention).
	PinRun(ctx context.Context, runID, kind string) error
	// SweepEvidence deletes rows of aborted, stale-staged, and superseded
	// UNPINNED runs. Shared atoms survive while any association references
	// them. Each call considers at most one run; ingestion caps each run at
	// 10,000 association+assertion rows and 20,000 evidence-reference edges,
	// providing a hard row/payload work bound. Returns 0 or 1.
	SweepEvidence(ctx context.Context, now time.Time, staleStagedAfter time.Duration) (int, error)
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
	SetRepoIndexedRevisions(ctx context.Context, name, defaultCommit string, revisions []IndexedRevision, at time.Time) error
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
