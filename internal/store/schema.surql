-- phebs schema (T1.2). Idempotent: IF NOT EXISTS
-- everywhere; applied on every Open.
-- ponytail: SCHEMALESS + Go-typed structs for now; tighten to per-field
-- DEFINEs once shapes stabilize (metadata typing decided in T2.2).

DEFINE TABLE IF NOT EXISTS repo SCHEMALESS;
DEFINE INDEX IF NOT EXISTS repo_name ON repo FIELDS name UNIQUE;
DEFINE FIELD IF NOT EXISTS indexed_analysis_unit ON repo TYPE option<object>;

DEFINE TABLE IF NOT EXISTS connection SCHEMALESS;
DEFINE TABLE IF NOT EXISTS repo_connection SCHEMALESS;
DEFINE INDEX IF NOT EXISTS repo_connection_pair ON repo_connection FIELDS connection, repo UNIQUE;

DEFINE TABLE IF NOT EXISTS connection_sync_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON connection_sync_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS connection_sync_job_status ON connection_sync_job FIELDS status;
DEFINE INDEX IF NOT EXISTS connection_sync_job_lifecycle ON connection_sync_job FIELDS status, finished_at;

DEFINE TABLE IF NOT EXISTS indexing_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON indexing_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS indexing_job_status ON indexing_job FIELDS status;
DEFINE INDEX IF NOT EXISTS indexing_job_lifecycle ON indexing_job FIELDS status, finished_at;

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

-- T33.2: precious immutable service-catalog generations and the one atomic
-- current authority pointer per repository. Unlike resolver catalogs, the
-- canonical JSON is database authority and survives backup/restore directly.
DEFINE TABLE IF NOT EXISTS service_catalog_generation SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS schema ON service_catalog_generation TYPE string
    ASSERT $value = 'phebs-service-catalog-publication-v1';
DEFINE FIELD IF NOT EXISTS repository ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS source_kind ON service_catalog_generation TYPE string
    ASSERT $value INSIDE ['committed', 'operator', 'analysis-unit-v1'];
DEFINE FIELD IF NOT EXISTS source_path ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS source_commit ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS source_census_digest ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS source_file_count ON service_catalog_generation TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS accepted_file_count ON service_catalog_generation TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS unowned_file_count ON service_catalog_generation TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS authority_kind ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS authority_id ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS authority_version ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS override_id ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS override_version ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS catalog_digest ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS legacy_analysis_unit_digest ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS generation_digest ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS catalog_json ON service_catalog_generation TYPE string;
DEFINE FIELD IF NOT EXISTS recorded_at ON service_catalog_generation TYPE datetime;

-- One point row per selected authority/override version prevents a
-- same-version/different-canonical-byte replacement without scanning immutable
-- generation history.
DEFINE TABLE IF NOT EXISTS service_catalog_authority_version SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS repository ON service_catalog_authority_version TYPE string;
DEFINE FIELD IF NOT EXISTS authority_kind ON service_catalog_authority_version TYPE string;
DEFINE FIELD IF NOT EXISTS authority_id ON service_catalog_authority_version TYPE string;
DEFINE FIELD IF NOT EXISTS authority_version ON service_catalog_authority_version TYPE string;
DEFINE FIELD IF NOT EXISTS override_id ON service_catalog_authority_version TYPE string;
DEFINE FIELD IF NOT EXISTS override_version ON service_catalog_authority_version TYPE string;
DEFINE FIELD IF NOT EXISTS catalog_digest ON service_catalog_authority_version TYPE string;
DEFINE FIELD IF NOT EXISTS recorded_at ON service_catalog_authority_version TYPE datetime;

DEFINE TABLE IF NOT EXISTS service_catalog_current SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS repository ON service_catalog_current TYPE string;
DEFINE FIELD IF NOT EXISTS generation_digest ON service_catalog_current TYPE string;
DEFINE FIELD IF NOT EXISTS control_revision ON service_catalog_current TYPE int ASSERT $value >= 1;
DEFINE FIELD IF NOT EXISTS published_at ON service_catalog_current TYPE datetime;
DEFINE INDEX IF NOT EXISTS service_catalog_current_repository
    ON service_catalog_current FIELDS repository UNIQUE;

-- T33.3: one current lifecycle row per repository-local service key. The
-- service-local desired digest excludes sibling records; incarnation and the
-- mutable control revision independently fence removal/re-add and A -> B -> A.
DEFINE TABLE IF NOT EXISTS service_state_current SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS schema ON service_state_current TYPE string
    ASSERT $value = 'phebs-service-state-v1';
DEFINE FIELD IF NOT EXISTS repository ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS service_key ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS display_name ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS disposition ON service_state_current TYPE string
    ASSERT $value INSIDE ['accepted', 'proposal', 'conflict', 'rejected'];
DEFINE FIELD IF NOT EXISTS origin ON service_state_current TYPE string
    ASSERT $value INSIDE ['base', 'override'];
DEFINE FIELD IF NOT EXISTS reason ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS successors ON service_state_current TYPE array<string>
    ASSERT array::len($value) <= 4000;
DEFINE FIELD IF NOT EXISTS incarnation ON service_state_current TYPE int ASSERT $value >= 1;
DEFINE FIELD IF NOT EXISTS desired_generation ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS desired_source_generation ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS desired_catalog_generation ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS active_desired_generation ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS active_source_generation ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS active_catalog_generation ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS active_search_generation ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS status ON service_state_current TYPE string
    ASSERT $value INSIDE ['current', 'stale', 'unavailable', 'conflict', 'removed'];
DEFINE FIELD IF NOT EXISTS removed ON service_state_current TYPE bool;
DEFINE FIELD IF NOT EXISTS state_digest ON service_state_current TYPE string;
DEFINE FIELD IF NOT EXISTS control_revision ON service_state_current TYPE int ASSERT $value >= 1;
DEFINE FIELD IF NOT EXISTS changed_at ON service_state_current TYPE datetime;
DEFINE INDEX IF NOT EXISTS service_state_repository_key
    ON service_state_current FIELDS repository, service_key UNIQUE;
DEFINE INDEX IF NOT EXISTS service_state_repository_removed
    ON service_state_current FIELDS repository, removed;

-- The point summary is the transition serialization row and carries current
-- counts without scanning historical catalog generations or old tombstones.
DEFINE TABLE IF NOT EXISTS service_state_repository SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS schema ON service_state_repository TYPE string
    ASSERT $value = 'phebs-service-state-repository-v1';
DEFINE FIELD IF NOT EXISTS repository ON service_state_repository TYPE string;
DEFINE FIELD IF NOT EXISTS catalog_generation ON service_state_repository TYPE string;
DEFINE FIELD IF NOT EXISTS catalog_control_revision ON service_state_repository TYPE int
    ASSERT $value >= 1;
DEFINE FIELD IF NOT EXISTS catalog_service_count ON service_state_repository TYPE int
    ASSERT $value >= 0 AND $value <= 4000;
DEFINE FIELD IF NOT EXISTS live_service_count ON service_state_repository TYPE int
    ASSERT $value >= 0 AND $value <= 4000;
DEFINE FIELD IF NOT EXISTS current_count ON service_state_repository TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS stale_count ON service_state_repository TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS unavailable_count ON service_state_repository TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS conflict_count ON service_state_repository TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS tombstone_count ON service_state_repository TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS summary_digest ON service_state_repository TYPE string;
DEFINE FIELD IF NOT EXISTS control_revision ON service_state_repository TYPE int ASSERT $value >= 1;
DEFINE FIELD IF NOT EXISTS updated_at ON service_state_repository TYPE datetime;
DEFINE INDEX IF NOT EXISTS service_state_repository_name
    ON service_state_repository FIELDS repository UNIQUE;

-- T35.1: immutable generation plans, bounded fan-out chunks, one current
-- pointer per repository/stage, and one repository-wide concurrency token row.
DEFINE TABLE IF NOT EXISTS generation_schedule SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS schema ON generation_schedule TYPE string ASSERT $value = 'phebs-generation-schedule-v1';
DEFINE FIELD IF NOT EXISTS digest ON generation_schedule TYPE string;
DEFINE FIELD IF NOT EXISTS repository ON generation_schedule TYPE string;
DEFINE FIELD IF NOT EXISTS stage ON generation_schedule TYPE string;
DEFINE FIELD IF NOT EXISTS generation ON generation_schedule TYPE string;
DEFINE FIELD IF NOT EXISTS resource_class ON generation_schedule TYPE string ASSERT $value INSIDE ['cpu', 'io', 'memory', 'extraction'];
DEFINE FIELD IF NOT EXISTS total_items ON generation_schedule TYPE int ASSERT $value > 0 AND $value <= 80000000;
DEFINE FIELD IF NOT EXISTS chunk_items ON generation_schedule TYPE int ASSERT $value > 0 AND $value <= 4096;
DEFINE FIELD IF NOT EXISTS total_chunks ON generation_schedule TYPE int ASSERT $value > 0 AND $value <= 1000000;
DEFINE FIELD IF NOT EXISTS max_attempts ON generation_schedule TYPE int ASSERT $value > 0 AND $value <= 8;
DEFINE FIELD IF NOT EXISTS repository_tokens ON generation_schedule TYPE int ASSERT $value > 0 AND $value <= 8;
DEFINE FIELD IF NOT EXISTS next_offset ON generation_schedule TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS materialized ON generation_schedule TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS pending ON generation_schedule TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS running ON generation_schedule TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS succeeded ON generation_schedule TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS failed ON generation_schedule TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS status ON generation_schedule TYPE string ASSERT $value INSIDE ['active', 'superseded', 'settled'];
DEFINE FIELD IF NOT EXISTS created_at ON generation_schedule TYPE datetime;
DEFINE FIELD IF NOT EXISTS updated_at ON generation_schedule TYPE datetime;
DEFINE FIELD IF NOT EXISTS last_claimed_at ON generation_schedule TYPE option<datetime>;
DEFINE INDEX IF NOT EXISTS generation_schedule_digest ON generation_schedule FIELDS digest UNIQUE;
DEFINE INDEX IF NOT EXISTS generation_schedule_ready ON generation_schedule FIELDS resource_class, status, last_claimed_at;
DEFINE INDEX IF NOT EXISTS generation_schedule_repository_stage ON generation_schedule FIELDS repository, status, stage;

DEFINE TABLE IF NOT EXISTS generation_schedule_current SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS repository ON generation_schedule_current TYPE string;
DEFINE FIELD IF NOT EXISTS stage ON generation_schedule_current TYPE string;
DEFINE FIELD IF NOT EXISTS schedule_digest ON generation_schedule_current TYPE string;
DEFINE FIELD IF NOT EXISTS generation ON generation_schedule_current TYPE string;
DEFINE FIELD IF NOT EXISTS updated_at ON generation_schedule_current TYPE datetime;
DEFINE INDEX IF NOT EXISTS generation_schedule_current_scope ON generation_schedule_current FIELDS repository, stage UNIQUE;

DEFINE TABLE IF NOT EXISTS generation_schedule_repository SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS repository ON generation_schedule_repository TYPE string;
DEFINE FIELD IF NOT EXISTS running ON generation_schedule_repository TYPE int ASSERT $value >= 0 AND $value <= 8;
DEFINE FIELD IF NOT EXISTS last_claimed_at ON generation_schedule_repository TYPE option<datetime>;
DEFINE FIELD IF NOT EXISTS updated_at ON generation_schedule_repository TYPE datetime;
DEFINE INDEX IF NOT EXISTS generation_schedule_repository_name ON generation_schedule_repository FIELDS repository UNIQUE;

DEFINE TABLE IF NOT EXISTS generation_schedule_chunk SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS identity ON generation_schedule_chunk TYPE string;
DEFINE FIELD IF NOT EXISTS schedule_digest ON generation_schedule_chunk TYPE string;
DEFINE FIELD IF NOT EXISTS repository ON generation_schedule_chunk TYPE string;
DEFINE FIELD IF NOT EXISTS stage ON generation_schedule_chunk TYPE string;
DEFINE FIELD IF NOT EXISTS generation ON generation_schedule_chunk TYPE string;
DEFINE FIELD IF NOT EXISTS resource_class ON generation_schedule_chunk TYPE string ASSERT $value INSIDE ['cpu', 'io', 'memory', 'extraction'];
DEFINE FIELD IF NOT EXISTS offset ON generation_schedule_chunk TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS length ON generation_schedule_chunk TYPE int ASSERT $value > 0 AND $value <= 4096;
DEFINE FIELD IF NOT EXISTS attempt ON generation_schedule_chunk TYPE int ASSERT $value >= 0 AND $value < 8;
DEFINE FIELD IF NOT EXISTS priority ON generation_schedule_chunk TYPE int ASSERT $value INSIDE [0, 1, 2];
DEFINE FIELD IF NOT EXISTS status ON generation_schedule_chunk TYPE string ASSERT $value INSIDE ['pending', 'running', 'done', 'failed', 'canceled'];
DEFINE FIELD IF NOT EXISTS not_before ON generation_schedule_chunk TYPE option<datetime>;
DEFINE FIELD IF NOT EXISTS claimed_by ON generation_schedule_chunk TYPE string;
DEFINE FIELD IF NOT EXISTS claimed_at ON generation_schedule_chunk TYPE option<datetime>;
DEFINE FIELD IF NOT EXISTS heartbeat_at ON generation_schedule_chunk TYPE option<datetime>;
DEFINE FIELD IF NOT EXISTS finished_at ON generation_schedule_chunk TYPE option<datetime>;
DEFINE FIELD IF NOT EXISTS error ON generation_schedule_chunk TYPE string;
DEFINE FIELD IF NOT EXISTS lease_token ON generation_schedule_chunk TYPE string;
DEFINE INDEX IF NOT EXISTS generation_schedule_chunk_identity ON generation_schedule_chunk FIELDS identity UNIQUE;
DEFINE INDEX IF NOT EXISTS generation_schedule_chunk_ready ON generation_schedule_chunk FIELDS schedule_digest, status, priority, not_before, offset;
DEFINE INDEX IF NOT EXISTS generation_schedule_chunk_stale ON generation_schedule_chunk FIELDS resource_class, status, heartbeat_at;

-- T35.3: durable bounded-maintenance cursors. Owners retain their own root
-- proofs; this table stores only opaque bounded progress and fair rotation.
DEFINE TABLE IF NOT EXISTS lifecycle_cursor SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS key ON lifecycle_cursor TYPE string;
DEFINE FIELD IF NOT EXISTS cursor ON lifecycle_cursor TYPE string;
DEFINE FIELD IF NOT EXISTS revision ON lifecycle_cursor TYPE int ASSERT $value >= 1;
DEFINE FIELD IF NOT EXISTS updated_at ON lifecycle_cursor TYPE datetime;
DEFINE INDEX IF NOT EXISTS lifecycle_cursor_key ON lifecycle_cursor FIELDS key UNIQUE;

-- repo <-> code-host identity permission edges (T10.3), mirrored from host
-- ACLs; the search pre-pass compiles them into a per-user RepoSet.
DEFINE TABLE IF NOT EXISTS repo_permission SCHEMALESS;
DEFINE INDEX IF NOT EXISTS repo_permission_pair ON repo_permission FIELDS repo, identity UNIQUE;
DEFINE INDEX IF NOT EXISTS repo_permission_identity ON repo_permission FIELDS identity;

DEFINE TABLE IF NOT EXISTS repo_fetch_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON repo_fetch_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS repo_fetch_job_status ON repo_fetch_job FIELDS status;
DEFINE INDEX IF NOT EXISTS repo_fetch_job_lifecycle ON repo_fetch_job FIELDS status, finished_at;

DEFINE TABLE IF NOT EXISTS extraction_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON extraction_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS extraction_job_status ON extraction_job FIELDS status;
DEFINE INDEX IF NOT EXISTS extraction_job_lifecycle ON extraction_job FIELDS status, finished_at;

-- Commit-bound candidate planning is a distinct durable queue stage between
-- indexing and extraction (T30.4).
DEFINE TABLE IF NOT EXISTS candidate_manifest_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON candidate_manifest_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS candidate_manifest_job_status ON candidate_manifest_job FIELDS status;
DEFINE INDEX IF NOT EXISTS candidate_manifest_job_lifecycle ON candidate_manifest_job FIELDS status, finished_at;

-- One strict current pointer per repository. Candidate member files remain
-- derived filesystem artifacts; this row is the atomic authority that binds
-- their content identity to the indexed HEAD, committed unit, and policy.
DEFINE TABLE IF NOT EXISTS candidate_manifest_publication SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS repository ON candidate_manifest_publication TYPE string;
DEFINE FIELD IF NOT EXISTS head_commit ON candidate_manifest_publication TYPE string;
DEFINE FIELD IF NOT EXISTS unit_digest ON candidate_manifest_publication TYPE string;
DEFINE FIELD IF NOT EXISTS policy_digest ON candidate_manifest_publication TYPE string;
DEFINE FIELD IF NOT EXISTS manifest_digest ON candidate_manifest_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation_digest ON candidate_manifest_publication TYPE string;
DEFINE FIELD IF NOT EXISTS manifest_path ON candidate_manifest_publication TYPE string;
DEFINE FIELD IF NOT EXISTS control_revision ON candidate_manifest_publication TYPE int
    DEFAULT 1 ASSERT $value >= 1;
DEFINE FIELD IF NOT EXISTS published_at ON candidate_manifest_publication TYPE datetime;
DEFINE INDEX IF NOT EXISTS candidate_manifest_publication_repository
    ON candidate_manifest_publication FIELDS repository UNIQUE;

-- T30.6f: the resolver catalog has its own durable replacement queue and one
-- strict current pointer per repository. Catalog bytes remain derived
-- filesystem artifacts, while this row is the atomic current authority.
DEFINE TABLE IF NOT EXISTS resolver_catalog_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON resolver_catalog_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS resolver_catalog_job_status ON resolver_catalog_job FIELDS status;
DEFINE INDEX IF NOT EXISTS resolver_catalog_job_lifecycle ON resolver_catalog_job FIELDS status, finished_at;

DEFINE TABLE IF NOT EXISTS resolver_catalog_publication SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS repository ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS head_commit ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS unit_digest ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS declarations ON resolver_catalog_publication TYPE array<object>
    ASSERT array::len($value) <= 16;
DEFINE FIELD IF NOT EXISTS declarations.*.domain ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS declarations.*.run_id ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS declarations.*.generation_digest ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS declarations.*.authority_schema ON resolver_catalog_publication TYPE option<string>;
DEFINE FIELD IF NOT EXISTS declarations.*.plan_digest ON resolver_catalog_publication TYPE option<string>;
DEFINE FIELD IF NOT EXISTS declarations.*.root_digest ON resolver_catalog_publication TYPE option<string>;
DEFINE FIELD IF NOT EXISTS declaration_set_digest ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS candidate_manifest_digest ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS source_lane_policy ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS resolver_packs ON resolver_catalog_publication TYPE array<object>
    ASSERT array::len($value) <= 16;
DEFINE FIELD IF NOT EXISTS resolver_packs.*.name ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS resolver_packs.*.version ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS resolver_pack_set_digest ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS catalog_policy_digest ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation_digest ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS manifest_digest ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS manifest_path ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS control_revision ON resolver_catalog_publication TYPE int
    DEFAULT 1 ASSERT $value >= 1;
DEFINE FIELD OVERWRITE writer_schema ON resolver_catalog_publication TYPE string
    ASSERT $value IN ['phebs-resolver-catalog-store-v1', 'phebs-resolver-catalog-store-v2'];
DEFINE FIELD IF NOT EXISTS authority_digest ON resolver_catalog_publication TYPE option<string>;
DEFINE FIELD IF NOT EXISTS published_at ON resolver_catalog_publication TYPE datetime;
DEFINE INDEX IF NOT EXISTS resolver_catalog_publication_repository
    ON resolver_catalog_publication FIELDS repository UNIQUE;

-- T30.6h: repository-keyed caller-leaf execution queue. One immutable outcome
-- row exists per complete-generation/pair; the admission row is aggregate
-- state only and is not a product-visible caller publication.
DEFINE TABLE IF NOT EXISTS caller_leaf_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON caller_leaf_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS caller_leaf_job_status ON caller_leaf_job FIELDS status;
DEFINE INDEX IF NOT EXISTS caller_leaf_job_lifecycle ON caller_leaf_job FIELDS status, finished_at;

DEFINE TABLE IF NOT EXISTS caller_leaf_outcome SCHEMALESS;
DEFINE FIELD OVERWRITE repository ON caller_leaf_outcome TYPE string;
DEFINE FIELD OVERWRITE generation ON caller_leaf_outcome TYPE object;
DEFINE FIELD OVERWRITE generation_digest ON caller_leaf_outcome TYPE string;
DEFINE FIELD OVERWRITE pair ON caller_leaf_outcome TYPE object;
DEFINE FIELD OVERWRITE domain ON caller_leaf_outcome TYPE string;
DEFINE FIELD OVERWRITE leaf_prefix ON caller_leaf_outcome TYPE string;
DEFINE FIELD OVERWRITE disposition ON caller_leaf_outcome TYPE string
    ASSERT $value INSIDE ['succeeded', 'terminal_generation_refusal'];
DEFINE FIELD OVERWRITE receipt ON caller_leaf_outcome TYPE option<object>;
DEFINE FIELD OVERWRITE refusal ON caller_leaf_outcome TYPE option<object>;
DEFINE FIELD OVERWRITE writer_schema ON caller_leaf_outcome TYPE string
    ASSERT $value = 'phebs-caller-leaf-store-v1';
DEFINE FIELD OVERWRITE recorded_at ON caller_leaf_outcome TYPE datetime;
DEFINE INDEX IF NOT EXISTS caller_leaf_outcome_generation
    ON caller_leaf_outcome FIELDS repository, generation_digest, domain, leaf_prefix;

DEFINE TABLE IF NOT EXISTS caller_generation_admission SCHEMALESS;
DEFINE FIELD OVERWRITE repository ON caller_generation_admission TYPE string;
DEFINE FIELD OVERWRITE generation ON caller_generation_admission TYPE object;
DEFINE FIELD OVERWRITE generation_digest ON caller_generation_admission TYPE string;
DEFINE FIELD OVERWRITE disposition ON caller_generation_admission TYPE string
    ASSERT $value INSIDE ['admitted', 'terminal_generation_refusal'];
DEFINE FIELD OVERWRITE pair_set_digest ON caller_generation_admission TYPE string;
DEFINE FIELD OVERWRITE pair_count ON caller_generation_admission TYPE int
    ASSERT $value >= 0;
DEFINE FIELD OVERWRITE artifact_count ON caller_generation_admission TYPE int
    ASSERT $value >= 0;
DEFINE FIELD OVERWRITE result_count ON caller_generation_admission TYPE int
    ASSERT $value >= 0;
DEFINE FIELD OVERWRITE abstention_count ON caller_generation_admission TYPE int
    ASSERT $value >= 0;
DEFINE FIELD OVERWRITE coverage_record_count ON caller_generation_admission TYPE option<int>
    ASSERT $value = NONE OR $value >= 0;
DEFINE FIELD OVERWRITE covered_candidate_count ON caller_generation_admission TYPE option<int>
    ASSERT $value = NONE OR $value >= 0;
DEFINE FIELD OVERWRITE canonical_bytes ON caller_generation_admission TYPE int
    ASSERT $value >= 0;
DEFINE FIELD OVERWRITE staging_bytes ON caller_generation_admission TYPE int
    ASSERT $value >= 0;
DEFINE FIELD OVERWRITE peak_open_files ON caller_generation_admission TYPE int
    ASSERT $value >= 0;
DEFINE FIELD OVERWRITE refusals ON caller_generation_admission TYPE option<array<object>>;
DEFINE FIELD OVERWRITE writer_schema ON caller_generation_admission TYPE string
    ASSERT $value = 'phebs-caller-leaf-store-v1';
DEFINE FIELD OVERWRITE recorded_at ON caller_generation_admission TYPE datetime;
DEFINE INDEX IF NOT EXISTS caller_generation_admission_repository
    ON caller_generation_admission FIELDS repository, generation_digest;

-- T30.6i: exactly one complete caller-generation pointer is visible per
-- repository. Immutable pair artifacts remain multi-generation filesystem
-- state; this row is the atomic authority over one exact ordered receipt set.
DEFINE TABLE IF NOT EXISTS caller_generation_publication SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS repository ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation ON caller_generation_publication TYPE object;
DEFINE FIELD IF NOT EXISTS generation.repository ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.head_commit ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.unit_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.declaration_set_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.candidate_manifest_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.candidate_policy_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.candidate_control_revision ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS generation.resolver_generation_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.resolver_manifest_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.resolver_control_revision ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS generation.resolver_writer_schema ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.source_lane_policy ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.caller_policy_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.extractor_set_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS generation.upstream_digest ON caller_generation_publication TYPE option<string>;
DEFINE FIELD IF NOT EXISTS generation.digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS upstream ON caller_generation_publication TYPE option<string>;
DEFINE FIELD IF NOT EXISTS generation_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs ON caller_generation_publication TYPE array<object>
    ASSERT array::len($value) <= 16384;
DEFINE FIELD IF NOT EXISTS pairs.*.pair ON caller_generation_publication TYPE object;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.domain ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.extractor_version ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.leaf_adapter_version ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.leaf_ordinal ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.leaf_prefix ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.leaf_prefix_bits ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.candidate_member_name ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.candidate_record_count ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.candidate_declared_bytes ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.candidate_content_bytes ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.candidate_content_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs.*.pair.digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt ON caller_generation_publication TYPE object;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.artifact_name ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.artifact_count ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.result_count ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.abstention_count ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.coverage_record_count ON caller_generation_publication TYPE option<int>;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.covered_candidate_count ON caller_generation_publication TYPE option<int>;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.canonical_bytes ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.staging_bytes ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.content_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.metadata_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.excluded_go_test_records ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.source_blob_reads ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.source_blob_bytes ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.receipt.out_of_leaf_reads ON caller_generation_publication TYPE int;
DEFINE FIELD IF NOT EXISTS pairs.*.record_count ON caller_generation_publication TYPE int
    ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS pair_payload_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pair_set_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS pair_count ON caller_generation_publication TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS artifact_count ON caller_generation_publication TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS result_count ON caller_generation_publication TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS abstention_count ON caller_generation_publication TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS coverage_record_count ON caller_generation_publication TYPE option<int>
    ASSERT $value = NONE OR $value >= 0;
DEFINE FIELD IF NOT EXISTS covered_candidate_count ON caller_generation_publication TYPE option<int>
    ASSERT $value = NONE OR $value >= 0;
DEFINE FIELD IF NOT EXISTS canonical_bytes ON caller_generation_publication TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS staging_bytes ON caller_generation_publication TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS peak_open_files ON caller_generation_publication TYPE int ASSERT $value >= 0;
DEFINE FIELD IF NOT EXISTS manifest_digest ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS manifest_path ON caller_generation_publication TYPE string;
DEFINE FIELD IF NOT EXISTS publication_revision ON caller_generation_publication TYPE int
    ASSERT $value >= 1;
DEFINE FIELD IF NOT EXISTS publication_incarnation ON caller_generation_publication TYPE string
    ASSERT string::len($value) = 71
        AND string::starts_with($value, 'sha256:')
        AND string::is_hexadecimal(string::slice($value, 7))
        AND string::lowercase($value) = $value;
DEFINE FIELD IF NOT EXISTS writer_schema ON caller_generation_publication TYPE string
    ASSERT $value = 'phebs-caller-generation-publication-store-v1';
DEFINE FIELD IF NOT EXISTS published_at ON caller_generation_publication TYPE datetime;
DEFINE INDEX IF NOT EXISTS caller_generation_publication_repository
    ON caller_generation_publication FIELDS repository UNIQUE;

-- Generation-named immutable writer guards cannot be weakened by an older
-- binary reapplying field definitions. DELETE remains available to bounded
-- reconciliation and repository removal.
DEFINE EVENT IF NOT EXISTS caller_leaf_outcome_writer_v1
    ON TABLE caller_leaf_outcome
    WHEN $event != 'DELETE'
      AND ($after.writer_schema ?? '') != 'phebs-caller-leaf-store-v1'
    THEN {
        THROW 'phebs-permanent: retired caller-leaf writer generation'
    };
DEFINE EVENT IF NOT EXISTS caller_generation_admission_writer_v1
    ON TABLE caller_generation_admission
    WHEN $event != 'DELETE'
      AND ($after.writer_schema ?? '') != 'phebs-caller-leaf-store-v1'
    THEN {
        THROW 'phebs-permanent: retired caller-leaf writer generation'
    };
DEFINE EVENT IF NOT EXISTS caller_generation_publication_writer_v1
    ON TABLE caller_generation_publication
    WHEN $event != 'DELETE'
      AND ($after.writer_schema ?? '') != 'phebs-caller-generation-publication-store-v1'
    THEN {
        THROW 'phebs-permanent: retired caller-generation publication writer generation'
    };

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

-- T40.7: one point-addressed idempotency/accounting receipt per trusted
-- worker chunk. The extraction run owns aggregate charged counters; this
-- bounded ledger independently reconstructs fact/chunk totals at publication.
DEFINE TABLE IF NOT EXISTS evidence_chunk SCHEMALESS;
DEFINE INDEX IF NOT EXISTS evidence_chunk_run ON evidence_chunk FIELDS run_id;
DEFINE INDEX IF NOT EXISTS evidence_chunk_identity ON evidence_chunk FIELDS run_id, chunk_id UNIQUE;

DEFINE TABLE IF NOT EXISTS extraction_run SCHEMALESS;
DEFINE INDEX IF NOT EXISTS extraction_run_scope ON extraction_run FIELDS repo, domain, status;
DEFINE INDEX IF NOT EXISTS extraction_run_scope_v8 ON extraction_run
    FIELDS repo, commit, unit_digest, domain, status;
DEFINE INDEX IF NOT EXISTS extraction_run_run_id ON extraction_run FIELDS run_id;
DEFINE INDEX IF NOT EXISTS extraction_run_ambiguous_run_id ON extraction_run
    FIELDS evidence_migration_ambiguous_run_id;
-- Retention selects a tiny oldest eligible set. Keep status/quarantine and
-- age/order fields together so that LIMIT does not require sorting the entire
-- historical run table as it grows.
DEFINE INDEX IF NOT EXISTS extraction_run_retention ON extraction_run
    FIELDS retention_quarantined, status, started_at, run_id;

-- T40.10: one latest partitioned domain authority per repository/domain. The
-- referenced run remains invisible to legacy published-run readers; T40.11
-- consumers require this exact root and run together. The selected staged run
-- is sealed against further appends and root-pinned against the legacy sweep;
-- T40.11 installs the complete historical pin-aware lifecycle owner.
DEFINE TABLE IF NOT EXISTS extraction_domain_root SCHEMALESS;
DEFINE INDEX IF NOT EXISTS extraction_domain_root_scope ON extraction_domain_root
    FIELDS repository, domain UNIQUE;
DEFINE INDEX IF NOT EXISTS extraction_domain_root_run ON extraction_domain_root
    FIELDS run_id;
-- Only a published row carries published_key. Its unique index is installed
-- after Open migrates any pre-slot rows, alongside the pending-job indexes.

-- One durable, non-evidence row per exact extraction scope records the latest
-- extraction attempt even after aborted run evidence is swept.
DEFINE TABLE IF NOT EXISTS extraction_attempt SCHEMALESS;
DEFINE INDEX IF NOT EXISTS extraction_attempt_repo ON extraction_attempt FIELDS repo;
DEFINE INDEX IF NOT EXISTS extraction_attempt_scope_v8 ON extraction_attempt
    FIELDS repo, commit, unit_digest, domain;
DEFINE INDEX IF NOT EXISTS extraction_attempt_run ON extraction_attempt FIELDS run_id UNIQUE;

-- T30.6b: one durable, non-evidence row per repository/domain records how its
-- latest exact scope resolved and the exact generation the resolution applies
-- to. It is the authority for extraction terminality and retry; it is not
-- evidence, takes no part in publication visibility, and no proof-retention
-- sweep removes it.
-- Latest-only is structural: the record id is the repository/domain hash, so a
-- commit or unit advance makes the old row ineligible without growing one row
-- per generation. Go remains the primary trust boundary; these field types and
-- assertions are defence in depth against raw writes.
DEFINE TABLE IF NOT EXISTS extraction_domain_outcome SCHEMALESS;
DEFINE FIELD OVERWRITE repo ON extraction_domain_outcome TYPE string;
DEFINE FIELD OVERWRITE commit ON extraction_domain_outcome TYPE string;
DEFINE FIELD OVERWRITE unit_digest ON extraction_domain_outcome TYPE string;
DEFINE FIELD OVERWRITE domain ON extraction_domain_outcome TYPE string;
DEFINE FIELD OVERWRITE disposition ON extraction_domain_outcome TYPE string
    ASSERT $value INSIDE ['published', 'unavailable_prerequisite',
        'terminal_generation_refusal', 'retryable_failure'];
DEFINE FIELD OVERWRITE generation ON extraction_domain_outcome TYPE object;
DEFINE FIELD OVERWRITE generation.candidate_manifest_digest
    ON extraction_domain_outcome TYPE option<string>;
DEFINE FIELD OVERWRITE generation.candidate_policy_digest
    ON extraction_domain_outcome TYPE option<string>;
DEFINE FIELD OVERWRITE generation.candidate_control_revision
    ON extraction_domain_outcome TYPE option<int>;
DEFINE FIELD OVERWRITE generation.extractor
    ON extraction_domain_outcome TYPE string;
DEFINE FIELD OVERWRITE generation.inventory_policy
    ON extraction_domain_outcome TYPE string;
DEFINE FIELD OVERWRITE generation.typed_input_kind
    ON extraction_domain_outcome TYPE option<string>;
DEFINE FIELD OVERWRITE generation.typed_input_object_id
    ON extraction_domain_outcome TYPE option<string>;
DEFINE FIELD OVERWRITE generation.typed_input_digest
    ON extraction_domain_outcome TYPE option<string>;
DEFINE FIELD OVERWRITE generation.typed_input_present
    ON extraction_domain_outcome TYPE option<bool>;
DEFINE FIELD OVERWRITE generation.dependency_digest
    ON extraction_domain_outcome TYPE string;
DEFINE FIELD OVERWRITE generation.digest
    ON extraction_domain_outcome TYPE string;
DEFINE FIELD OVERWRITE candidate_control_failure
    ON extraction_domain_outcome TYPE bool DEFAULT false;
DEFINE FIELD OVERWRITE run_id ON extraction_domain_outcome TYPE option<string>;
DEFINE FIELD OVERWRITE receipt_schema ON extraction_domain_outcome TYPE string
    ASSERT $value = 'phebs-extraction-domain-outcome-v1';
DEFINE FIELD OVERWRITE receipt ON extraction_domain_outcome TYPE string
    ASSERT string::len($value) <= 8192;
DEFINE FIELD OVERWRITE recorded_at ON extraction_domain_outcome TYPE datetime;
DEFINE FIELD OVERWRITE store_schema_version ON extraction_domain_outcome TYPE string;
DEFINE FIELD OVERWRITE evidence_migration_version
    ON extraction_domain_outcome TYPE string;
DEFINE INDEX IF NOT EXISTS extraction_domain_outcome_repo ON extraction_domain_outcome
    FIELDS repo;
DEFINE INDEX IF NOT EXISTS extraction_domain_outcome_scope ON extraction_domain_outcome
    FIELDS repo, commit, unit_digest, domain;
DEFINE INDEX IF NOT EXISTS extraction_domain_outcome_control ON extraction_domain_outcome
    FIELDS repo, candidate_control_failure, generation.candidate_control_revision;

-- Proof-aware retention: a pinned run (and everything it references) is
-- immune to sweeps regardless of supersession or age.
DEFINE TABLE IF NOT EXISTS evidence_pin SCHEMALESS;
DEFINE FIELD OVERWRITE kind ON evidence_pin TYPE string;
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
