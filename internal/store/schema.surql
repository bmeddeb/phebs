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
DEFINE FIELD IF NOT EXISTS status ON connection_sync_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed'];
DEFINE INDEX IF NOT EXISTS connection_sync_job_status ON connection_sync_job FIELDS status;

DEFINE TABLE IF NOT EXISTS indexing_job SCHEMALESS;
DEFINE FIELD IF NOT EXISTS status ON indexing_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed'];
DEFINE INDEX IF NOT EXISTS indexing_job_status ON indexing_job FIELDS status;

DEFINE TABLE IF NOT EXISTS user SCHEMALESS;
DEFINE TABLE IF NOT EXISTS api_key SCHEMALESS;
