// Package store is the SurrealDB-backed state and job-queue layer (T1.2, T1.3).
//
// Store is an interface to keep the Postgres exit open (PLAN §3); Surreal is
// the only implementation. Job tables double as the polling queue — the
// claim statement lands with T1.3.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
)

var (
	ErrNotFound                            = errors.New("not found")
	ErrConflict                            = errors.New("conflict")
	ErrLeaseLost                           = errors.New("job lease lost")
	ErrResultLimit                         = errors.New("result limit exceeded")
	ErrInvalidCandidateManifestPublication = errors.New(
		"invalid candidate manifest publication",
	)
	ErrInvalidResolverCatalogPublication = errors.New(
		"invalid resolver catalog publication",
	)
)

type JobKind string

// Job kinds name their SurrealDB table directly.
const (
	JobSync  JobKind = "connection_sync_job"
	JobIndex JobKind = "indexing_job"
	JobFetch JobKind = "repo_fetch_job" // webhook-driven single-repo fetch (T7.4)
	// JobCandidate plans and publishes the commit-bound candidate manifest
	// that extraction must validate before starting (T30.4).
	JobCandidate JobKind = "candidate_manifest_job"
	JobExtract   JobKind = "extraction_job" // evidence extraction, chained after indexing (T12.2)
	// JobResolverCatalog materializes the immutable declaration/candidate-bound
	// catalog. T30.6f defines its durable queue without registering an adapter
	// worker; T30.6g supplies that consumer.
	JobResolverCatalog JobKind = "resolver_catalog_job"
	// JobCallerLeaf executes and reconciles the immutable caller-domain/leaf
	// artifacts for one exact candidate and resolver generation (T30.6h).
	// The target remains the repository: pair identity lives in the durable
	// outcome rows, while one pending successor is the lossless rescan signal.
	JobCallerLeaf JobKind = "caller_leaf_job"
	// JobInvestigate runs one preflighted Investigation Run (T16.4). Its
	// generic queue lease and the Run's publication lease are independent:
	// losing either fences the worker.
	JobInvestigate JobKind = "investigation_run_job"
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
	Name                string              `json:"name"` // unique, e.g. "github.com/foo/bar"
	DisplayName         string              `json:"display_name,omitempty"`
	CloneURL            string              `json:"clone_url"`
	WebURL              string              `json:"web_url,omitempty"`
	DefaultBranch       string              `json:"default_branch,omitempty"`
	IsFork              bool                `json:"is_fork"`
	IsArchived          bool                `json:"is_archived"`
	IsPublic            bool                `json:"is_public"`
	Metadata            map[string]any      `json:"metadata,omitempty"` // schemaless until T2.2 decides typing
	IndexedAt           *time.Time          `json:"indexed_at,omitempty"`
	IndexedCommitHash   string              `json:"indexed_commit_hash,omitempty"`
	IndexedRevisions    []IndexedRevision   `json:"indexed_revisions,omitempty"`
	IndexedAnalysisUnit *analysisunit.State `json:"-" cbor:"indexed_analysis_unit,omitempty"`
	// EvidenceRevision is an internal monotonic visibility fence. It is
	// intentionally absent from repository API responses while allowing
	// consumers that compose live evidence to detect a publication transition
	// even when commit and semantic unit digest are unchanged.
	EvidenceRevision int64      `json:"-" cbor:"evidence_revision,omitempty"`
	LatestJobStatus  string     `json:"latest_indexing_job_status,omitempty"`
	PushedAt         *time.Time `json:"pushed_at,omitempty"`
	ExternalID       string     `json:"external_id,omitempty"`
	ExternalHostType string     `json:"external_code_host_type,omitempty"`
	ExternalHostURL  string     `json:"external_code_host_url,omitempty"`
	Deleting         bool       `json:"deleting,omitempty"`
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
// and the repository name for repository-scoped jobs.
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
	Orphaned     bool                `json:"orphaned"` // no connection claims this repo
	Connections  []string            `json:"connections,omitempty"`
	LastIndexJob *Job                `json:"last_index_job,omitempty"`
	AnalysisUnit *analysisunit.State `json:"analysis_unit,omitempty"`
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

// APIKeyCapability is one reviewed immutable authority attached to a named
// bearer key at creation time. The registry is deliberately closed.
type APIKeyCapability string

const APIKeyCapabilityInvestigationWrite APIKeyCapability = "investigation:write"

// CanonicalAPIKeyCapabilities validates the closed registry, rejects duplicate
// authority, and returns a deterministic non-nil list for persistence and
// public metadata.
func CanonicalAPIKeyCapabilities(values []APIKeyCapability) ([]APIKeyCapability, error) {
	canonical := make([]APIKeyCapability, 0, len(values))
	seen := make(map[APIKeyCapability]struct{}, len(values))
	for _, value := range values {
		if value != APIKeyCapabilityInvestigationWrite {
			return nil, errors.New("unsupported API key capability")
		}
		if _, exists := seen[value]; exists {
			return nil, errors.New("duplicate API key capability")
		}
		seen[value] = struct{}{}
		canonical = append(canonical, value)
	}
	slices.Sort(canonical)
	return canonical, nil
}

// APIKey stores only a SHA-256 digest of a generated high-entropy bearer key.
// ID, Prefix, and reviewed capability names are safe for management UIs; Hash
// is never serialized. Capabilities have no store update path after creation.
type APIKey struct {
	ID           string             `json:"id"`
	UserID       string             `json:"user_id,omitempty"`
	Name         string             `json:"name"`
	Prefix       string             `json:"prefix"`
	Hash         string             `json:"-"`
	Capabilities []APIKeyCapability `json:"capabilities"`
	CreatedAt    time.Time          `json:"created_at"`
	LastUsedAt   *time.Time         `json:"last_used_at,omitempty"`
	ExpiresAt    *time.Time         `json:"expires_at,omitempty"`
	RevokedAt    *time.Time         `json:"revoked_at,omitempty"`
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
	// InventoryPolicy names the corpus-inventory contract this run was
	// produced under. Empty identifies a legacy run that predates gitlink
	// boundary accounting; older nonempty generations may also have superseded
	// symlink semantics. The worker replaces any noncurrent generation on the
	// next extraction job.
	InventoryPolicy string `json:"inventory_policy,omitempty"`
	// Gitlink boundaries: submodule pointers named by the trusted walker.
	// CorpusFileCount remains the count of regular blobs; boundaries are a
	// separate dimension. The digest binds every boundary (sorted
	// path\x00oid\x00 records, domain-separated); the sample is a bounded
	// display-safe subset, so GitlinkCount may exceed its length even when
	// the sample is not truncated.
	GitlinkCount           int      `json:"gitlink_count,omitempty"`
	GitlinkDigest          string   `json:"gitlink_digest,omitempty"`
	GitlinkSamplePaths     []string `json:"gitlink_sample_paths,omitempty"`
	GitlinkSampleTruncated bool     `json:"gitlink_sample_truncated,omitempty"`
	// Candidate publication scope is present on T30.5 strict-manifest runs.
	// The manifest digest binds the complete reusable publication, while the
	// remaining fields disclose the exact domain projection that was replayed.
	// Empty fields identify readable legacy/direct-corpus runs.
	ScopePosture                string `json:"scope_posture,omitempty"` // whole-repository | focused-local | repository-overlay
	CandidateManifestDigest     string `json:"candidate_manifest_digest,omitempty"`
	CandidatePlane              string `json:"candidate_plane,omitempty"` // local | repository | caller
	ScopeCorpusFileCount        int    `json:"scope_corpus_file_count,omitempty"`
	ScopeCorpusDeclaredBytes    int64  `json:"scope_corpus_declared_bytes,omitempty"`
	ScopeCorpusDigest           string `json:"scope_corpus_digest,omitempty"`
	PlannedFileCount            int    `json:"planned_file_count,omitempty"`
	PlannedRequiredFileCount    int    `json:"planned_required_file_count,omitempty"`
	PlannedDeclaredBytes        int64  `json:"planned_declared_bytes,omitempty"`
	PlannedScopeDigest          string `json:"planned_scope_digest,omitempty"`
	ExcludedSourceFileCount     int    `json:"excluded_source_file_count,omitempty"`
	ExcludedSourceRequiredCount int    `json:"excluded_source_required_count,omitempty"`
	ExcludedSourceDeclaredBytes int64  `json:"excluded_source_declared_bytes,omitempty"`
	ExcludedSCIPDocumentCount   int    `json:"excluded_scip_document_count,omitempty"`
	ExcludedSCIPDefinitionCount int    `json:"excluded_scip_definition_count,omitempty"`
	ExcludedSCIPOccurrenceCount int    `json:"excluded_scip_occurrence_count,omitempty"`
	TypedInputKind              string `json:"typed_input_kind,omitempty"`
	TypedInputPath              string `json:"typed_input_path,omitempty"`
	TypedInputObjectID          string `json:"typed_input_object_id,omitempty"`
	TypedInputDeclaredBytes     int64  `json:"typed_input_declared_bytes,omitempty"`
	TypedInputDigest            string `json:"typed_input_digest,omitempty"`
	TypedInputPresent           bool   `json:"typed_input_present"`
}

// ExtractionScope is the complete publication slot for one evidence domain.
// UnitDigest is empty only for the explicit whole-repository scope; a focused
// scope carries the canonical analysis-unit digest committed with the indexed
// source revision.
type ExtractionScope struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	UnitDigest string `json:"unit_digest"`
	Domain     string `json:"domain"`
}

// ExtractionRun is the atomic publication unit: rows written under a staged
// run are invisible; PublishExtractionRun flips status in one transaction and
// supersedes the prior published run for its exact ExtractionScope. A failed
// or killed run therefore never publishes a partial replacement set, and a
// scoped run cannot replace whole-repository evidence or another unit.
type ExtractionRun struct {
	ID          string           `json:"id"`
	Repo        string           `json:"repo"`
	Commit      string           `json:"commit"`
	UnitDigest  string           `json:"unit_digest"`
	Domain      string           `json:"domain"` // e.g. "proto-contract"
	Extractor   string           `json:"extractor"`
	Status      string           `json:"status"` // staged | published | superseded | aborted | deleting (internal)
	StartedAt   time.Time        `json:"started_at"`
	PublishedAt *time.Time       `json:"published_at,omitempty"`
	Coverage    CoverageManifest `json:"coverage"`
}

// EvidenceSweepProgress reports one bounded retention step. Logical run
// deletion is intentionally separate from physical proof-row deletion: a
// target-size run takes multiple resumable calls, and a caller must not treat
// the one-run candidate limit as a row limit.
type EvidenceSweepProgress struct {
	RunsMarkedDeleting      int `json:"runs_marked_deleting"`
	RunsDeleted             int `json:"runs_deleted"`
	AssociationRowsDeleted  int `json:"association_rows_deleted"`
	AssertionRowsDeleted    int `json:"assertion_rows_deleted"`
	AtomRowsDeleted         int `json:"atom_rows_deleted"`
	RetentionPhasesAdvanced int `json:"retention_phases_advanced"`
}

// PhysicalRowsDeleted excludes the extraction-run record itself.
func (p EvidenceSweepProgress) PhysicalRowsDeleted() int {
	return p.AssociationRowsDeleted + p.AssertionRowsDeleted + p.AtomRowsDeleted
}

// DidWork distinguishes a drained store from a metadata-only phase advance.
func (p EvidenceSweepProgress) DidWork() bool {
	return p.RunsMarkedDeleting+p.RunsDeleted+p.PhysicalRowsDeleted()+
		p.RetentionPhasesAdvanced > 0
}

// ExtractionAttempt is the durable latest-attempt marker for one exact
// ExtractionScope. Unlike an aborted ExtractionRun, it is not evidence and is
// not removed by proof-retention sweeps. It lets coverage reporting
// distinguish a healthy last publication from a newer staged or aborted
// replacement without borrowing an attempt from another commit or unit.
type ExtractionAttempt struct {
	RunID      string    `json:"run_id"`
	Repo       string    `json:"repo"`
	Commit     string    `json:"commit"`
	UnitDigest string    `json:"unit_digest"`
	Domain     string    `json:"domain"`
	Extractor  string    `json:"extractor"`
	Status     string    `json:"status"` // staged | published | aborted
	StartedAt  time.Time `json:"started_at"`
}

// MaxExtractionOutcomeReceiptBytes bounds the operational receipt an outcome
// may carry. The caller owns staying inside it: a published outcome commits in
// the same transaction as its publication, so an oversized diagnostic must
// never be able to fail a legitimate publish.
const MaxExtractionOutcomeReceiptBytes = 8 << 10

// ExtractionOutcomeReceiptSchema is the canonical bounded receipt embedded in
// a durable domain outcome. It is intentionally distinct from the enclosing
// T30.6a job report: publication commits before post-publication and cleanup
// timings exist, so pretending the nested job-report object were identical
// would make the durable receipt untruthful.
const ExtractionOutcomeReceiptSchema = "phebs-extraction-domain-outcome-v1"

// DomainOutcomeDisposition is the closed set of durable per-domain extraction
// outcomes (T30.6b). It is decided only by typed error classification, never
// from error text, and is deliberately independent of the non-authoritative
// T30.6a operational reason vocabulary: a receipt reason and a disposition may
// legitimately disagree.
type DomainOutcomeDisposition string

const (
	// DomainOutcomePublished names a committed generation for this exact scope.
	DomainOutcomePublished DomainOutcomeDisposition = "published"
	// DomainOutcomeUnavailablePrerequisite names a declared input this
	// generation cannot supply. It settles the generation and is never
	// re-executed, so only a typed prerequisite absence may map to it: an
	// over-broad mapping stalls the domain until its generation advances.
	DomainOutcomeUnavailablePrerequisite DomainOutcomeDisposition = "unavailable_prerequisite"
	// DomainOutcomeTerminalGenerationRefusal names a deterministic refusal that
	// re-running the identical generation cannot clear.
	DomainOutcomeTerminalGenerationRefusal DomainOutcomeDisposition = "terminal_generation_refusal"
	// DomainOutcomeRetryableFailure names a transient failure. The job requeues
	// while any configured domain holds it.
	DomainOutcomeRetryableFailure DomainOutcomeDisposition = "retryable_failure"
)

// ValidDomainOutcomeDisposition is the authoritative closed-set check. The
// schema assertion on the stored disposition is defence in depth only: an
// older writer reapplying its own schema can weaken a field assertion, which
// is exactly why the writer-generation guard is an EVENT and not an ASSERT.
func ValidDomainOutcomeDisposition(disposition DomainOutcomeDisposition) bool {
	switch disposition {
	case DomainOutcomePublished, DomainOutcomeUnavailablePrerequisite,
		DomainOutcomeTerminalGenerationRefusal, DomainOutcomeRetryableFailure:
		return true
	default:
		return false
	}
}

// Settled reports whether the disposition forbids re-execution within its own
// generation. A job is terminal only when every configured domain is settled;
// it requeues while any domain is retryable.
func (disposition DomainOutcomeDisposition) Settled() bool {
	switch disposition {
	case DomainOutcomePublished, DomainOutcomeUnavailablePrerequisite,
		DomainOutcomeTerminalGenerationRefusal:
		return true
	default:
		return false
	}
}

// ExtractionGenerationIdentity is everything beyond ExtractionScope that must
// change before a settled outcome may be re-executed. Scope identity lives in
// the record id; these fields are the generation the outcome was settled
// under, and any difference invalidates it.
//
// DependencyDigest is deliberately narrow: no extractor reads another domain's
// evidence today, so it binds only the writer/format/migration triple, the
// run's gitlink boundary digest, and the typed-input object identity. A real
// cross-domain dependency identity is later T30.6 work.
type ExtractionGenerationIdentity struct {
	CandidateManifestDigest string `json:"candidate_manifest_digest,omitempty"`
	CandidatePolicyDigest   string `json:"candidate_policy_digest,omitempty"`
	// CandidateControlRevision is the durable identity of the validated
	// publication controls. It advances after strict repair even when the
	// rebuilt semantic manifest digest is unchanged.
	CandidateControlRevision uint64 `json:"candidate_control_revision,omitempty"`
	Extractor                string `json:"extractor"`
	InventoryPolicy          string `json:"inventory_policy,omitempty"`
	TypedInputKind           string `json:"typed_input_kind,omitempty"`
	TypedInputObjectID       string `json:"typed_input_object_id,omitempty"`
	TypedInputDigest         string `json:"typed_input_digest,omitempty"`
	TypedInputPresent        bool   `json:"typed_input_present,omitempty"`
	DependencyDigest         string `json:"dependency_digest,omitempty"`
	// Digest is recomputed by the store from the fields above on every write
	// and every read comparison. A caller-supplied value is never trusted.
	Digest string `json:"digest"`
}

// ComputeExtractionGenerationDigest is the sole canonical generation
// identity. The store invokes it on every write and read; workers may use it
// only for equality checks and cannot select a caller-supplied digest.
func ComputeExtractionGenerationDigest(generation ExtractionGenerationIdentity) string {
	return hashIdentity(
		"extraction_generation_v1_",
		generation.CandidateManifestDigest,
		generation.CandidatePolicyDigest,
		strconv.FormatUint(generation.CandidateControlRevision, 10),
		generation.Extractor,
		generation.InventoryPolicy,
		generation.TypedInputKind,
		generation.TypedInputObjectID,
		generation.TypedInputDigest,
		strconv.FormatBool(generation.TypedInputPresent),
		generation.DependencyDigest,
	)
}

// SameExtractionGeneration compares canonical identities without trusting
// either value's cached Digest field.
func SameExtractionGeneration(
	left, right ExtractionGenerationIdentity,
) bool {
	return ComputeExtractionGenerationDigest(left) ==
		ComputeExtractionGenerationDigest(right)
}

// ExtractionDomainOutcome is the durable latest-only record of how one exact
// ExtractionScope resolved, and the sole authority for extraction terminality
// and retry. Like ExtractionAttempt it is not evidence and no proof-retention
// sweep removes it; unlike ExtractionAttempt it carries a typed disposition and
// the exact generation that disposition applies to, so a changed commit, unit,
// candidate manifest, policy, extractor, typed input, or dependency identity
// invalidates it rather than suppressing fresh work.
//
// Receipt is the deliberately opaque bounded canonical JSON of the matching
// pre-transition T30.6b domain receipt, carried the way
// ProofBundleRecord.Content is: the store persists and bounds the bytes and
// never interprets it as retry authority. It is distinct from the later
// T30.6a job report. No field here participates in publication visibility,
// and no outcome can make a mismatched historical publication current.
type ExtractionDomainOutcome struct {
	Scope       ExtractionScope              `json:"scope"`
	Disposition DomainOutcomeDisposition     `json:"disposition"`
	Generation  ExtractionGenerationIdentity `json:"generation"`
	// CandidateControlFailure is true only for a deterministic candidate
	// publication descriptor/member integrity refusal. Recording one also
	// schedules strict candidate reconciliation in the same transaction.
	CandidateControlFailure bool      `json:"candidate_control_failure,omitempty"`
	RunID                   string    `json:"run_id,omitempty"`
	ReceiptSchema           string    `json:"receipt_schema,omitempty"`
	Receipt                 string    `json:"receipt,omitempty"`
	RecordedAt              time.Time `json:"recorded_at"`
}

// Validate is the write-side trust boundary. It rejects an unknown
// disposition, an incomplete scope, a published outcome with no run identity,
// and an oversized receipt. It deliberately does not check Generation.Digest:
// the store recomputes that itself and never trusts the caller's value.
func (outcome ExtractionDomainOutcome) Validate() error {
	if !ValidDomainOutcomeDisposition(outcome.Disposition) {
		return fmt.Errorf(
			"extraction domain outcome disposition %q is not in the frozen set",
			outcome.Disposition)
	}
	if err := validateExtractionScope(outcome.Scope); err != nil {
		return fmt.Errorf("extraction domain outcome scope: %w", err)
	}
	if err := validateOutcomeGeneration(outcome.Generation); err != nil {
		return fmt.Errorf("extraction domain outcome generation: %w", err)
	}
	if outcome.Disposition == DomainOutcomePublished && outcome.RunID == "" {
		return errors.New("published extraction domain outcome requires a run id")
	}
	if outcome.CandidateControlFailure &&
		(outcome.Disposition != DomainOutcomeTerminalGenerationRefusal ||
			outcome.Generation.CandidateControlRevision == 0) {
		return errors.New(
			"candidate control failure requires a terminal candidate generation")
	}
	if len(outcome.Receipt) > MaxExtractionOutcomeReceiptBytes {
		return fmt.Errorf(
			"extraction domain outcome receipt is %d bytes, limit %d",
			len(outcome.Receipt), MaxExtractionOutcomeReceiptBytes)
	}
	if outcome.ReceiptSchema != ExtractionOutcomeReceiptSchema {
		return fmt.Errorf(
			"extraction domain outcome receipt schema must be %q",
			ExtractionOutcomeReceiptSchema,
		)
	}
	if outcome.Receipt == "" || !json.Valid([]byte(outcome.Receipt)) {
		return errors.New(
			"extraction domain outcome receipt must be canonical JSON")
	}
	return nil
}

func validateOutcomeGeneration(generation ExtractionGenerationIdentity) error {
	if strings.TrimSpace(generation.Extractor) == "" ||
		len(generation.Extractor) > 128 {
		return errors.New("extractor version is missing or unbounded")
	}
	hasCandidate := generation.CandidateManifestDigest != "" ||
		generation.CandidatePolicyDigest != "" ||
		generation.CandidateControlRevision != 0
	if hasCandidate {
		if !validSHA256Digest(generation.CandidateManifestDigest) ||
			!validSHA256Digest(generation.CandidatePolicyDigest) ||
			generation.CandidateControlRevision == 0 {
			return errors.New(
				"candidate generation requires manifest, policy, and control identities")
		}
	} else if generation.InventoryPolicy != "" &&
		strings.HasPrefix(generation.InventoryPolicy, "candidate-manifest-") {
		return errors.New(
			"candidate inventory policy requires a candidate generation")
	}
	if generation.InventoryPolicy == "" ||
		strings.TrimSpace(generation.InventoryPolicy) !=
			generation.InventoryPolicy ||
		len(generation.InventoryPolicy) > 256 {
		return errors.New("inventory policy is missing or unbounded")
	}
	if generation.TypedInputKind == "" {
		if generation.TypedInputObjectID != "" ||
			generation.TypedInputDigest != "" ||
			generation.TypedInputPresent {
			return errors.New(
				"non-applicable typed input carries an identity")
		}
	} else {
		if generation.TypedInputKind != "scip" {
			return errors.New("typed input kind is unsupported")
		}
		if generation.TypedInputPresent {
			if !validGitObjectID(generation.TypedInputObjectID) {
				return errors.New(
					"present typed input requires a canonical object id")
			}
		} else if generation.TypedInputObjectID != "" ||
			generation.TypedInputDigest != "" {
			return errors.New("absent typed input carries content identity")
		}
		if generation.TypedInputDigest != "" &&
			!validSHA256Digest(generation.TypedInputDigest) {
			return errors.New("typed input digest must be canonical sha256")
		}
	}
	if !validSHA256Digest(generation.DependencyDigest) {
		return errors.New("dependency digest must be canonical sha256")
	}
	return nil
}

// AssertionCursor is the stable tuple used by bounded published-assertion
// scans. It contains only fields already present in an authorized assertion.
type AssertionCursor struct {
	Predicate string `json:"predicate"`
	Subject   string `json:"subject"`
	Object    string `json:"object"`
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
}

// AssertionQuery filters published assertions. Repo is mandatory, and callers
// must identify a currently published run or one exact ExtractionScope.
type AssertionQuery struct {
	Predicate string
	Subject   string
	Object    string
	// ObjectPrefix is mutually exclusive with Object.
	ObjectPrefix string
	Lineage      string
	Repo         string
	RunID        string
	// Scope is the exact published slot to read. A query without RunID must
	// provide Scope; otherwise multiple historical commit/unit publications
	// could be mixed into one result.
	Scope *ExtractionScope
	Limit int // 0 = default cap
	// After resumes after this exact stable-order tuple.
	After *AssertionCursor
	// AllowTruncate returns at most Limit+1 rows instead of ErrResultLimit.
	// The extra row is a caller-visible continuation signal; callers must not
	// render it as part of the requested page.
	AllowTruncate bool
}

// ReverseAssertionCursor is the complete stable key for an exact reverse
// assertion page. The query scope fields prevent a continuation from being
// reused against another repository, run, predicate, or operation.
type ReverseAssertionCursor struct {
	Repo         string `json:"repo"`
	RunID        string `json:"run_id"`
	Predicate    string `json:"predicate"`
	Object       string `json:"object"`
	QueryLineage string `json:"query_lineage,omitempty"`
	RowLineage   string `json:"row_lineage,omitempty"`
	Subject      string `json:"subject"`
	AssertionID  string `json:"assertion_id"`
}

// ReverseAssertionQuery pages one exact predicate/object within one published
// run. Lineage is optional: an empty lineage returns every wire-object match;
// a non-empty lineage narrows the page to one declaration identity.
type ReverseAssertionQuery struct {
	Repo      string
	RunID     string
	Predicate string
	Object    string
	Lineage   string
	Limit     int // 0 = 50; maximum 100
	After     *ReverseAssertionCursor
}

// ReverseAssertionPage contains only renderable rows. Next is the stable key
// of the final returned row when another row exists.
type ReverseAssertionPage struct {
	Assertions []Assertion             `json:"assertions"`
	Next       *ReverseAssertionCursor `json:"next,omitempty"`
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
	BeginExtractionRun(ctx context.Context, scope ExtractionScope, extractor string) (*ExtractionRun, error)
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
	// previous published run for the same exact ExtractionScope.
	PublishExtractionRun(ctx context.Context, runID string, coverage CoverageManifest) error
	// PublishExtractionRunWithOutcome is the T30.6b worker transition:
	// publication, prior-run supersession, latest-attempt state, bounded
	// receipt, and published disposition commit atomically.
	PublishExtractionRunWithOutcome(
		ctx context.Context,
		runID string,
		coverage CoverageManifest,
		outcome ExtractionDomainOutcome,
	) error
	// RecordExtractionDomainOutcome records a non-published disposition while
	// preserving any prior visible publication. A candidate-control terminal
	// record also force-enqueues strict candidate reconciliation atomically.
	RecordExtractionDomainOutcome(
		ctx context.Context,
		outcome ExtractionDomainOutcome,
	) error
	// LatestExtractionDomainOutcome reads the latest repository/domain row
	// only when its exact scope matches. A caller compares Generation before
	// treating a settled row as current.
	LatestExtractionDomainOutcome(
		ctx context.Context,
		scope ExtractionScope,
	) (*ExtractionDomainOutcome, error)
	AbortExtractionRun(ctx context.Context, runID string) error
	// LatestPublishedRun returns ErrNotFound when the exact scope has never
	// published. It never falls back to another commit, unit, or whole-
	// repository evidence.
	LatestPublishedRun(ctx context.Context, scope ExtractionScope) (*ExtractionRun, error)
	// LatestExtractionAttempt returns the durable latest attempt marker. It
	// survives evidence sweeps so a failed replacement remains reportable.
	LatestExtractionAttempt(ctx context.Context, scope ExtractionScope) (*ExtractionAttempt, error)
	// ListAssertions reads assertions of published runs only. Repo is required;
	// callers must authorize that repository before invoking the method. A
	// result exceeding Limit fails with ErrResultLimit unless AllowTruncate
	// explicitly requests the bounded Limit+1 continuation shape.
	ListAssertions(ctx context.Context, q AssertionQuery) ([]Assertion, error)
	// ListReverseAssertions is the strict fleet-pagination primitive. It
	// requires an exact published run, predicate, and object; uses the
	// generation-bound compound index; and returns at most 100 rows plus an
	// explicit continuation key.
	ListReverseAssertions(ctx context.Context, q ReverseAssertionQuery) (*ReverseAssertionPage, error)
	// ResolveEvidence resolves one current or pinned-retained assertion support
	// id. Repo is a required authorization scope and must match the run and all
	// returned occurrences.
	ResolveEvidence(ctx context.Context, repo, runID, atomID string) (*EvidenceResolution, error)
	// PinRun idempotently exempts a published or superseded run from sweeps
	// (proof-bundle / checkpoint retention).
	PinRun(ctx context.Context, runID, kind string) error
	// SweepEvidence advances at most one UNPINNED aborted, stale-staged,
	// superseded, or already-deleting run by one bounded durable step. Shared
	// atoms survive while any association references them. The returned
	// logical-run and physical-row counts are deliberately separate.
	SweepEvidence(ctx context.Context, now time.Time, staleStagedAfter time.Duration) (EvidenceSweepProgress, error)
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
	SetRepoIndexedState(ctx context.Context, name, defaultCommit string, revisions []IndexedRevision, unit *analysisunit.State, at time.Time) error
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
	// EnsureJobSuccessor records that this exact active lease created its pending
	// crash-recovery successor. Any ordinary enqueue clears that provenance. Its
	// errors carry SuccessorRetry because transaction commit may be ambiguous;
	// the marker is safe even when validation proves that no row was created.
	EnsureJobSuccessor(ctx context.Context, job Job, force bool) (*Job, error)
	// FailJobWithSuccessor atomically fails only a successor still owned by this
	// exact active lease and cancels the active row. If a freshness event has
	// coalesced into the successor, it is preserved and only the active row fails.
	FailJobWithSuccessor(ctx context.Context, job Job, errMsg string) error
	ReleaseJob(ctx context.Context, job Job, errMsg string) error // back to pending without consuming an attempt
	CancelPendingJobs(ctx context.Context, kind JobKind, target string) (int, error)
	ReapStale(ctx context.Context, kind JobKind, staleAfter time.Duration, maxAttempts int) (int, error)

	Close(ctx context.Context) error
}

// EnqueuePending persists a freshness event without allowing duplicate pending
// successors. Index, candidate, and extraction workers use force to bypass
// their respective current-generation short circuits.
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
