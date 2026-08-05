-- phebs schema (T1.2, PORT_MAP §5 P1 subset). Idempotent: IF NOT EXISTS
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

-- Commit-bound candidate planning is a distinct durable queue stage between
-- indexing and extraction (T30.4).
DEFINE TABLE IF NOT EXISTS candidate_manifest_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON candidate_manifest_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS candidate_manifest_job_status ON candidate_manifest_job FIELDS status;

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

DEFINE TABLE IF NOT EXISTS resolver_catalog_publication SCHEMAFULL;
DEFINE FIELD IF NOT EXISTS repository ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS head_commit ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS unit_digest ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS declarations ON resolver_catalog_publication TYPE array<object>
    ASSERT array::len($value) <= 16;
DEFINE FIELD IF NOT EXISTS declarations.*.domain ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS declarations.*.run_id ON resolver_catalog_publication TYPE string;
DEFINE FIELD IF NOT EXISTS declarations.*.generation_digest ON resolver_catalog_publication TYPE string;
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
DEFINE FIELD IF NOT EXISTS writer_schema ON resolver_catalog_publication TYPE string
    ASSERT $value = 'phebs-resolver-catalog-store-v1';
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
DEFINE FIELD OVERWRITE canonical_bytes ON caller_generation_admission TYPE int
    ASSERT $value >= 0;
DEFINE FIELD OVERWRITE staging_bytes ON caller_generation_admission TYPE int
    ASSERT $value >= 0;
DEFINE FIELD OVERWRITE peak_open_files ON caller_generation_admission TYPE int
    ASSERT $value >= 0;
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
DEFINE FIELD IF NOT EXISTS generation.digest ON caller_generation_publication TYPE string;
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

DEFINE TABLE IF NOT EXISTS investigation_run_job SCHEMALESS;
DEFINE FIELD OVERWRITE status ON investigation_run_job TYPE string
    ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS investigation_run_job_status ON investigation_run_job FIELDS status;

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

-- Investigations product domain (T16.1). Semantic rows are create-once in
-- the Go boundary; only Investigation and Watch projections expose checked
-- update methods. Run rows never store state: state is reconstructed from the
-- append-only event stream. event_revision is only a serialization token.
DEFINE TABLE IF NOT EXISTS investigation SCHEMALESS;
DEFINE FIELD OVERWRITE lifecycle ON investigation TYPE string
    ASSERT $value INSIDE ['draft', 'active', 'concluded', 'archived'];
DEFINE INDEX IF NOT EXISTS investigation_identity ON investigation FIELDS investigation_id UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_revision SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_revision_identity ON investigation_revision
    FIELDS investigation_id, seq UNIQUE;

-- T21.2 immutable canonical Change Briefs. The Go boundary exposes no
-- standalone write: one brief and one parent Revision are appended atomically,
-- and old Investigation revisions legitimately have no brief row.
DEFINE TABLE IF NOT EXISTS investigation_change_brief SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_change_brief_identity ON investigation_change_brief
    FIELDS brief_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_change_brief_revision ON investigation_change_brief
    FIELDS revision_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_change_brief_investigation ON investigation_change_brief
    FIELDS investigation_id, revision_id;

-- T21.3 idempotent Workbench creation/revision receipts. The receipt and its
-- Investigation/Revision/brief/audit graph commit together; preview rows are
-- never persisted.
DEFINE TABLE IF NOT EXISTS investigation_workbench_mutation SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_workbench_mutation_identity
    ON investigation_workbench_mutation FIELDS mutation_key UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_workbench_mutation_scope
    ON investigation_workbench_mutation FIELDS principal, idempotency_key UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_workbench_mutation_investigation
    ON investigation_workbench_mutation FIELDS investigation_id, revision_id;

-- T21.9 immutable, owner-authored Workbench Dispositions. These are not
-- ReviewItems or tasks: deterministic suggestions are embedded snapshots,
-- corrections append one uniquely superseding row, and the record itself is
-- the principal-scoped idempotency receipt.
DEFINE TABLE IF NOT EXISTS investigation_workbench_disposition SCHEMALESS;
DEFINE FIELD OVERWRITE category ON investigation_workbench_disposition TYPE string
    ASSERT $value INSIDE ['accepted', 'rejected', 'completed', 'reopened', 'waived'];
DEFINE INDEX IF NOT EXISTS investigation_workbench_disposition_identity
    ON investigation_workbench_disposition FIELDS disposition_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_workbench_disposition_receipt
    ON investigation_workbench_disposition FIELDS principal, idempotency_key UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_workbench_disposition_scope
    ON investigation_workbench_disposition FIELDS investigation_id, revision_id, created_at;
DEFINE INDEX IF NOT EXISTS investigation_workbench_disposition_suggestion
    ON investigation_workbench_disposition FIELDS investigation_id, suggestion_id, sequence UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_workbench_disposition_supersedes
    ON investigation_workbench_disposition FIELDS supersedes UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_run SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_run_identity ON investigation_run
    FIELDS revision_id, seq UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_run_idempotency ON investigation_run
    FIELDS revision_id, idempotency_key UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_run_event SCHEMALESS;
DEFINE FIELD OVERWRITE new_state ON investigation_run_event TYPE string
    ASSERT $value INSIDE ['queued', 'enumerating', 'analyzing', 'publishing', 'published', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS investigation_run_event_identity ON investigation_run_event FIELDS event_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_run_event_sequence ON investigation_run_event
    FIELDS run_id, sequence UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_run_artifact SCHEMALESS;
DEFINE FIELD OVERWRITE terminal_status ON investigation_run_artifact TYPE string
    ASSERT $value INSIDE ['published', 'failed', 'canceled'];
DEFINE INDEX IF NOT EXISTS investigation_run_artifact_identity ON investigation_run_artifact FIELDS artifact_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_run_artifact_run ON investigation_run_artifact FIELDS run_id;
DEFINE INDEX IF NOT EXISTS investigation_run_artifact_retention ON investigation_run_artifact
    FIELDS created_at, artifact_id;

-- T16.2 append-only artifact owners and releases. Acquiring an owner and
-- sweeping both serialize through the artifact's retention_revision token;
-- release and override rows preserve the policy audit after collection.
DEFINE TABLE IF NOT EXISTS investigation_artifact_owner SCHEMALESS;
DEFINE FIELD OVERWRITE owner_kind ON investigation_artifact_owner TYPE string
    ASSERT $value INSIDE ['investigation', 'baseline', 'dossier'];
DEFINE INDEX IF NOT EXISTS investigation_artifact_owner_identity ON investigation_artifact_owner
    FIELDS owner_key UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_artifact_owner_artifact ON investigation_artifact_owner
    FIELDS artifact_id, owner_key;

DEFINE TABLE IF NOT EXISTS investigation_artifact_owner_release SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_artifact_owner_release_identity ON investigation_artifact_owner_release
    FIELDS owner_key UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_artifact_retention_override SCHEMALESS;
DEFINE FIELD OVERWRITE policy ON investigation_artifact_retention_override TYPE string
    ASSERT $value INSIDE ['revocation', 'mandatory_deletion', 'legal_policy', 'approved_retention'];
DEFINE INDEX IF NOT EXISTS investigation_artifact_override_identity ON investigation_artifact_retention_override
    FIELDS override_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_artifact_override_artifact ON investigation_artifact_retention_override
    FIELDS artifact_id, override_id;

DEFINE TABLE IF NOT EXISTS investigation_decision SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_decision_identity ON investigation_decision FIELDS decision_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_decision_scope ON investigation_decision FIELDS investigation_id, claim_scope;
DEFINE INDEX IF NOT EXISTS investigation_decision_supersedes ON investigation_decision FIELDS supersedes UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_disposition SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_disposition_identity ON investigation_disposition FIELDS disposition_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_disposition_subject ON investigation_disposition FIELDS subject_kind, subject_id;
DEFINE INDEX IF NOT EXISTS investigation_disposition_supersedes ON investigation_disposition FIELDS supersedes UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_baseline_designation SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_baseline_identity ON investigation_baseline_designation FIELDS designation_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_baseline_scope ON investigation_baseline_designation FIELDS investigation_id, claim_scope, workflow_scope;
DEFINE INDEX IF NOT EXISTS investigation_baseline_supersedes ON investigation_baseline_designation FIELDS supersedes UNIQUE;

-- T16.3 authorization invariants. Grants are explicit owner-authorized
-- sharing edges checked at query time on every read. Grant generations and
-- the Investigation authz_revision form the cursor/read epoch; the internal
-- authz_write_revision serializes grant, revoke, transfer, and cursor writes.
-- Those mutations create their audit row in the same transaction.
DEFINE TABLE IF NOT EXISTS investigation_grant SCHEMALESS;
DEFINE FIELD OVERWRITE role ON investigation_grant TYPE string
    ASSERT $value INSIDE ['reader'];
DEFINE INDEX IF NOT EXISTS investigation_grant_identity ON investigation_grant FIELDS grant_key UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_grant_scope ON investigation_grant
    FIELDS investigation_id, principal UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_cursor SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_cursor_identity ON investigation_cursor FIELDS cursor_key UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_cursor_scope ON investigation_cursor
    FIELDS investigation_id, principal UNIQUE;

-- T16.4 guided creation idempotency and lease-fenced asynchronous execution.
-- The mapping is immutable and keeps a concurrent retry on the same
-- (principal, idempotency key) attached to one object graph. Run lease fields
-- remain on investigation_run as mutable attempt fencing data; state remains
-- derived exclusively from RunEvents.
DEFINE TABLE IF NOT EXISTS investigation_creation SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_creation_identity ON investigation_creation
    FIELDS creation_key UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_creation_scope ON investigation_creation
    FIELDS principal, idempotency_key UNIQUE;

-- T16.7 principal-scoped snapshot comparisons and compact edge history.
-- Snapshot records are immutable. Ledger rows are a rebuildable projection
-- that keeps first/last positive observations and traced removal tombstones;
-- RunArtifact sweeping never deletes either table.
DEFINE TABLE IF NOT EXISTS investigation_consumer_snapshot SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_consumer_snapshot_identity
    ON investigation_consumer_snapshot FIELDS snapshot_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_consumer_snapshot_artifact
    ON investigation_consumer_snapshot FIELDS principal, artifact_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_consumer_snapshot_sequence
    ON investigation_consumer_snapshot FIELDS investigation_id, principal, sequence UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_consumer_edge_ledger SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_consumer_edge_ledger_identity
    ON investigation_consumer_edge_ledger FIELDS ledger_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_consumer_edge_ledger_scope
    ON investigation_consumer_edge_ledger
    FIELDS investigation_id, principal, continuity_namespace, logical_relationship_id UNIQUE;

-- T16.8 derived Review projection. Projection receipts and ReviewItems are
-- immutable; superseded_by/superseded_at are the only lifecycle projection
-- fields. Per-principal acknowledgement reuses investigation_cursor so the
-- authorization epoch and transfer/revoke behavior remain one invariant.
DEFINE TABLE IF NOT EXISTS investigation_review_projection SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_review_projection_identity
    ON investigation_review_projection FIELDS projection_record_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_review_projection_source
    ON investigation_review_projection
    FIELDS principal, source_snapshot_id, projection_id, projection_version UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_review_item SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_review_item_identity
    ON investigation_review_item FIELDS review_item_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_review_item_scope
    ON investigation_review_item
    FIELDS investigation_id, principal, projection_id, projection_version, evaluated_at;
DEFINE INDEX IF NOT EXISTS investigation_review_item_projection_key
    ON investigation_review_item FIELDS investigation_id, principal, projection_key;

-- T16.9 immutable, recipient-scoped sealed dossiers. The primary artifact
-- acquires a Dossier retention owner in the same transaction as this row.
DEFINE TABLE IF NOT EXISTS investigation_dossier SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_dossier_identity
    ON investigation_dossier FIELDS dossier_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_dossier_scope
    ON investigation_dossier FIELDS investigation_id, principal, created_at;
DEFINE INDEX IF NOT EXISTS investigation_dossier_root
    ON investigation_dossier FIELDS root_digest UNIQUE;

DEFINE TABLE IF NOT EXISTS investigation_watch SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_watch_identity ON investigation_watch FIELDS watch_id UNIQUE;
DEFINE INDEX IF NOT EXISTS investigation_watch_owner ON investigation_watch FIELDS owner;

DEFINE TABLE IF NOT EXISTS investigation_watch_revision SCHEMALESS;
DEFINE INDEX IF NOT EXISTS investigation_watch_revision_identity ON investigation_watch_revision
    FIELDS watch_id, seq UNIQUE;
