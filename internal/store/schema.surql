-- phebs schema (T1.2, PORT_MAP §5 P1 subset). Idempotent: IF NOT EXISTS
-- everywhere; applied on every Open.
-- ponytail: SCHEMALESS + Go-typed structs for now; tighten to per-field
-- DEFINEs once shapes stabilize (metadata typing decided in T2.2).

DEFINE TABLE IF NOT EXISTS repo SCHEMALESS;
DEFINE INDEX IF NOT EXISTS repo_name ON repo FIELDS name UNIQUE;

DEFINE TABLE IF NOT EXISTS connection SCHEMALESS;
DEFINE TABLE IF NOT EXISTS repo_connection SCHEMALESS;
DEFINE INDEX IF NOT EXISTS repo_connection_pair ON repo_connection FIELDS connection, repo UNIQUE;

DEFINE TABLE IF NOT EXISTS connection_sync_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON connection_sync_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS connection_sync_job_status ON connection_sync_job FIELDS status;

DEFINE TABLE IF NOT EXISTS indexing_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON indexing_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS indexing_job_status ON indexing_job FIELDS status;

DEFINE TABLE IF NOT EXISTS user SCHEMALESS;
DEFINE INDEX IF NOT EXISTS user_normalized_email ON user FIELDS normalized_email UNIQUE;
DEFINE INDEX IF NOT EXISTS user_oidc_identity ON user FIELDS oidc_issuer, oidc_subject UNIQUE;
DEFINE INDEX IF NOT EXISTS user_admin_slot ON user FIELDS admin_slot UNIQUE;
DEFINE TABLE IF NOT EXISTS api_key SCHEMALESS;
DEFINE INDEX IF NOT EXISTS api_key_hash ON api_key FIELDS hash UNIQUE;
DEFINE INDEX IF NOT EXISTS api_key_user ON api_key FIELDS user_id;

DEFINE TABLE IF NOT EXISTS auth_session SCHEMALESS;
DEFINE INDEX IF NOT EXISTS auth_session_expiry ON auth_session FIELDS expiry;

-- The singleton record is claimed in the same transaction as first-user
-- creation. It is the serialization point that prevents two concurrent setup
-- requests from both observing an empty user table.
DEFINE TABLE IF NOT EXISTS auth_setup SCHEMALESS;

-- Append-only audit trail (T10.1); the index keeps the retention sweep and
-- newest-first listing cheap.
DEFINE TABLE IF NOT EXISTS audit_event SCHEMALESS;
DEFINE INDEX IF NOT EXISTS audit_event_created ON audit_event FIELDS created_at;

-- Local usage events (T10.2) — zero telemetry, local dashboard only.
DEFINE TABLE IF NOT EXISTS usage_event SCHEMALESS;
DEFINE INDEX IF NOT EXISTS usage_event_created ON usage_event FIELDS created_at;

-- Fixed-id completion records let bounded data migrations skip table scans on
-- steady-state Open. A marker is written only after every row update commits.
DEFINE TABLE IF NOT EXISTS store_migration SCHEMALESS;

-- repo <-> code-host identity permission edges (T10.3), mirrored from host
-- ACLs; the search pre-pass compiles them into a per-user RepoSet.
DEFINE TABLE IF NOT EXISTS repo_permission SCHEMALESS;
DEFINE INDEX IF NOT EXISTS repo_permission_pair ON repo_permission FIELDS repo, identity UNIQUE;
DEFINE INDEX IF NOT EXISTS repo_permission_identity ON repo_permission FIELDS identity;

DEFINE TABLE IF NOT EXISTS repo_fetch_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON repo_fetch_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS repo_fetch_job_status ON repo_fetch_job FIELDS status;

DEFINE TABLE IF NOT EXISTS extraction_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON extraction_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS extraction_job_status ON extraction_job FIELDS status;

-- Contract-intelligence evidence layer (T12.1, PLAN ADRs 2026-07-12).
-- Atoms are content-keyed and shared across repos/commits; repository
-- placement (and therefore authorization) lives in snapshot_evidence.
-- Rows become visible only through their run's status: staged rows are
-- invisible to readers, publish is a single-transaction status flip.
DEFINE TABLE IF NOT EXISTS evidence_atom SCHEMALESS;
DEFINE INDEX IF NOT EXISTS evidence_atom_key ON evidence_atom FIELDS atom_id UNIQUE;

DEFINE TABLE IF NOT EXISTS snapshot_evidence SCHEMALESS;
DEFINE INDEX IF NOT EXISTS snapshot_evidence_atom ON snapshot_evidence FIELDS atom_id;
DEFINE INDEX IF NOT EXISTS snapshot_evidence_run ON snapshot_evidence FIELDS run_id;
-- association_key includes the extraction run; occurrence_id deliberately
-- does not. Retrying one staged batch is therefore idempotent without
-- confusing content identity with repository placement identity.
DEFINE INDEX IF NOT EXISTS snapshot_evidence_association_key ON snapshot_evidence FIELDS association_key UNIQUE;

DEFINE TABLE IF NOT EXISTS assertion SCHEMALESS;
DEFINE INDEX IF NOT EXISTS assertion_run ON assertion FIELDS run_id;
DEFINE INDEX IF NOT EXISTS assertion_pred_obj ON assertion FIELDS predicate, object;
DEFINE INDEX IF NOT EXISTS assertion_run_semantic ON assertion FIELDS assertion_key UNIQUE;

DEFINE TABLE IF NOT EXISTS extraction_run SCHEMALESS;
DEFINE INDEX IF NOT EXISTS extraction_run_scope ON extraction_run FIELDS repo, domain, status;
DEFINE INDEX IF NOT EXISTS extraction_run_run_id ON extraction_run FIELDS run_id;
DEFINE INDEX IF NOT EXISTS extraction_run_ambiguous_run_id ON extraction_run
    FIELDS evidence_migration_ambiguous_run_id;
-- Retention selects a tiny oldest eligible set. Keep status/quarantine and
-- age/order fields together so that LIMIT does not require sorting the entire
-- historical run table as it grows.
DEFINE INDEX IF NOT EXISTS extraction_run_retention ON extraction_run
    FIELDS retention_quarantined, status, started_at, run_id;
-- Only a published row carries published_key. Its unique index is installed
-- after Open migrates any pre-slot rows, alongside the pending-job indexes.

-- One durable, non-evidence row per (repository, domain) records the latest
-- extraction attempt even after aborted run evidence is swept.
DEFINE TABLE IF NOT EXISTS extraction_attempt SCHEMALESS;
DEFINE INDEX IF NOT EXISTS extraction_attempt_repo ON extraction_attempt FIELDS repo;
DEFINE INDEX IF NOT EXISTS extraction_attempt_run ON extraction_attempt FIELDS run_id UNIQUE;

-- Proof-aware retention: a pinned run (and everything it references) is
-- immune to sweeps regardless of supersession or age.
DEFINE TABLE IF NOT EXISTS evidence_pin SCHEMALESS;
DEFINE INDEX IF NOT EXISTS evidence_pin_run ON evidence_pin FIELDS run_id;
DEFINE INDEX IF NOT EXISTS evidence_pin_kind ON evidence_pin FIELDS kind;
DEFINE INDEX IF NOT EXISTS evidence_pin_identity ON evidence_pin FIELDS pin_key UNIQUE;

-- T14.1 immutable proof bundles. Content is canonical opaque JSON; the
-- content-derived record id prevents mutation, while evidence_pin retains all
-- extraction runs the bundle names. Reads re-authorize repositories in Go.
DEFINE TABLE IF NOT EXISTS proof_bundle SCHEMALESS;
DEFINE INDEX IF NOT EXISTS proof_bundle_retention ON proof_bundle
    FIELDS retained_at, bundle_id;
DEFINE INDEX IF NOT EXISTS proof_bundle_legacy_retention ON proof_bundle
    FIELDS created_at, bundle_id;
