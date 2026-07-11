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

DEFINE TABLE IF NOT EXISTS repo_fetch_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON repo_fetch_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS repo_fetch_job_status ON repo_fetch_job FIELDS status;
