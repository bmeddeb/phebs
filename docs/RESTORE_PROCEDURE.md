# Pilot restore procedure

*Draft v0.1 · pilot prerequisite item 5 · dependency preview only*

This document designs the pilot backup-and-restore procedure without operating
on a pilot environment. It does not authorize a backup, restore, credential
use, source access, or capacity check. The role/capability model in
[PILOT_CHARTER.md](./PILOT_CHARTER.md) must be accepted before this procedure
can be accepted, and a separately authorized environment is required before a
witnessed restore can run.

## Record status

| Field | Value |
| --- | --- |
| Owner | `TBD` |
| Independent reviewer | `TBD` |
| Restore operator | `TBD` |
| Independent witness | `TBD` |
| State | `blocked_unassigned` |
| Execution | prohibited until this design is accepted and the pilot environment is separately authorized |
| Required acceptance evidence | artifact digest, reviewer identity, timestamp, decision, unresolved findings |

## 1. Recovery objectives to approve

The pilot authorization record must replace every `TBD` below with a value,
source, uncertainty statement, and approver. This draft does not infer them.

| Objective | Authorized value | Source/approver |
| --- | --- | --- |
| Recovery point objective (maximum acceptable data loss) | TBD | TBD |
| Recovery time objective (maximum acceptable recovery time) | TBD | TBD |
| Backup frequency | TBD | TBD |
| Backup retention and deletion schedule | TBD | TBD |
| Approved backup destination | TBD | TBD |
| Encryption and key-custody policy | TBD | TBD |
| Restore destination and isolation controls | TBD | TBD |

Failure to approve any row blocks backup creation and the witnessed restore.

## 2. Recovery-set boundary

The backup manifest must classify every artifact as precious, derived, or
excluded. An unclassified artifact blocks publication of the backup.

### 2.1 Precious state

The recovery set must include, subject to the accepted retention and legal
policy:

- the SurrealDB export containing repository records, job state, users,
  authorization state, API-key hashes, sessions if explicitly approved,
  permission snapshots, audit records, local usage records, extraction runs,
  evidence atoms and associations, proof bundles, pins, decisions, retention
  state, and store-migration markers;
- the exact approved configuration with secret references preserved but secret
  values removed;
- the backup manifest, artifact digests, schema version, store-writer
  generation, evidence-format version, application binary digest, toolchain
  identity, export command/version record, and timestamps;
- the approved pilot manifest, receipts, authorization records, and teardown or
  continuation decisions needed to interpret the restored evidence chain; and
- the artifact-retention policy and any legal-hold or mandatory-deletion record
  in force when the backup is created.

The manifest must state whether volatile sessions and pending jobs are retained
or deliberately omitted. Omission is a reviewed decision, not an accidental
property of the export.

### 2.2 Derived state

Bare repository mirrors, whole-repository zoekt shards, commit-bound candidate
manifests/partition members, extracted temporary trees, module caches, build
caches, queues of reproducible derived work, and other scratch artifacts are
rebuildable. They are excluded by default to reduce credential, source, and
stale-index exposure. Focused zoekt publications are the current documented
exception: the production online backup preserves their exact bytes because
fresh builder identity/timestamps make rebuild bytes unsuitable for restore
equality. If an approved capacity or recovery objective requires any other
derived artifact in the backup, the manifest must identify it, justify it,
bind its digest, and apply the same authorization and deletion rules as
source-derived evidence.

After restore, derived state is rebuilt only from frozen, authorized inputs.
It must not be treated as current merely because it existed at backup time.
The supported import explicitly clears candidate-publication pointers before
the restored store is accepted, so normal startup must rebuild the excluded
candidate manifests and members before extraction resumes.

### 2.3 Credentials and external authority

Plaintext passwords, API keys, OIDC client secrets, code-host credentials,
session signing material, backup-encryption keys, and root database credentials
must not be embedded in the recovery set. Their references and required
capabilities may be recorded, but values remain in their approved custody
system.

A restored credential hash does not itself reauthorize access. Before ingress
opens, the security owner must decide which identities are revoked, rotated,
or reissued, and current permission state must be synchronized under separate
authorization.

## 3. Preconditions

The restore operator and independent witness must record that all of the
following are true before backup creation or restoration begins:

1. The role/capability model and this procedure are accepted with artifact
   digests, reviewers, timestamps, decisions, and resolved findings.
2. The backup/restore operator has only the approved capability and the witness
   is independent of that operator.
3. The destination is isolated from production and unapproved networks; source
   credentials and outbound access are absent at startup.
4. The exact supported phebs binary, SurrealDB version, store-writer generation,
   evidence-format version, and configuration schema are available and bound by
   digest.
5. The target has enough independently measured capacity for precious state,
   transient import space, derived rebuilds, logs, and the configured safety
   margin. The accepted sizing artifact supplies the values.
6. The backup manifest, digest set, encryption/key-custody record, and retention
   authorization are available to both operator and witness.
7. A stop channel and the identities authorized to halt the exercise are
   recorded.

Any false or unverifiable precondition is an abort, not a warning.

## 4. Backup creation design

T-P5.1 supplies the reviewed command shape below. The backup manifest pins the
exact phebs and SurrealDB versions and SHA-256 binary digests used by the
authorized environment; a restore refuses any different executable even when
its display version is equal.

1. Record exercise ID, operator, witness, authorization digest, binary and
   dependency versions, store-writer generation, evidence-format version, and
   wall-clock source.
2. Stop application writes or use a reviewed SurrealDB export mechanism proven
   to provide the required consistency for the exact deployed version. A plain
   filesystem copy is permitted only with phebs and the supervised database
   child stopped, as required by the
   [operations guide](./guides/OPERATIONS.md#shutdown).
3. Execute `phebs backup -config <exact-config> -output <new-private-path>`.
   Internally, the live runtime-bound SurrealDB executable runs `surreal export
   --endpoint <live-loopback-endpoint> --namespace phebs --database phebs --log
   none database.surql`, with its local root credentials injected through the
   child environment and never printed or placed in the manifest. Record the
   command exit status and the emitted manifest digest; the completed manifest
   pins that executable version/digest and records its UTC creation time and
   sanitized exact command template.
4. Inventory every staged artifact by relative path, byte length, media type,
   classification, and SHA-256 digest. Reject symlinks, special files, absolute
   paths, path traversal, case aliases, and anything not declared in Section 2.
5. Bind the inventory to the configuration digest, database/export identity,
   application digest, schema/writer/evidence versions, and the accepted
   authorization and retention records.
6. Apply `[APPROVED ENCRYPTION AND KEY-CUSTODY PROCEDURE]`; record the encrypted
   artifact digest and key identifier, never the key value.
7. Make the completed artifact durable, then publish it atomically to the
   approved backup destination. An interrupted or partially published artifact
   remains quarantined and cannot satisfy the RPO.
8. Have the witness independently recompute the published digest and compare it
   with the signed manifest. Record accept/reject and every finding.
9. Resume writes only if the backup operation itself did not alter store state
   and all stop conditions remain clear.

## 5. Restore procedure

The restore is fail-closed. It remains unavailable to pilot users until the
final authorization step.

1. **Open the witnessed exercise.** Record exercise ID, approved backup ID,
   destination, operator, witness, start time, and authorization digest.
2. **Isolate the destination.** Demonstrate that production routes, public
   ingress, code-host credentials, OIDC secrets, and unapproved egress are
   absent. Preserve that evidence in the transcript.
3. **Verify the backup before decryption/import.** Recompute the encrypted
   artifact digest; validate the manifest and authorization; reject an expired,
   revoked, undeclared, incomplete, or mandatory-deletion-conflicting backup.
4. **Decrypt into private staging.** Use the approved key-custody procedure.
   Recompute every plaintext artifact digest and reject extra, missing, renamed,
   aliased, traversal, symlink, or special-file entries.
5. **Restore configuration without active secrets.** Validate its schema and
   bind the restored copy to the manifest. Keep authentication and source
   integrations unable to contact external systems.
6. **Import precious database state.** Execute `phebs restore -config
   <exact-config> -backup <verified-private-path>` against an absent or empty
   configured `$DATA`. Internally, the manifest-pinned executable runs `surreal
   import --endpoint <isolated-loopback-endpoint> --namespace phebs --database
   phebs --log none database.surql` as the exclusive writer. Capture the phebs
   command identity, exit status, manifest digest, timestamps, and post-import
   database identity without recording credentials. A partial target after a
   failed import is quarantined; the command will not retry over it.
7. **Check version compatibility before application writes.** Confirm schema,
   migration markers, store-writer generation, and evidence-format version.
   Unknown versions, missing completion markers, mixed writers, an implicit
   rollback, or an unreviewed migration aborts the exercise.
8. **Start offline or loopback-only.** With external integrations disabled,
   verify process health, database health, authorization defaults, audit
   readability, retained evidence and pins, proof-bundle integrity, retention
   state, and the absence of partially published extraction runs.
9. **Reconcile authority.** Under security approval, revoke or rotate restored
   sessions and keys as required, establish fresh service credentials, and
   synchronize current permission state. Old bundle IDs and source-derived
   objects must remain subject to current authorization on read.
10. **Rebuild derived state.** Recreate mirrors, indexes, and extraction output
    only from the authorized frozen source universe and bound toolchain. Verify
    the current indexed commit, publication atomics, artifact digests, and
    coverage manifests. Do not silently reuse stale derived artifacts.
11. **Run bounded recovery checks.** Execute the accepted subset of the
    negative-test design, including cross-principal denial, revocation, current
    projection, refusal-shape equality, and partial-publication rejection. Run
    only checks authorized for the restore environment.
12. **Measure recovery.** Record achieved recovery point and elapsed recovery
    time against the accepted RPO/RTO, including rebuild time and all pauses.
13. **Witness and decide.** The witness independently verifies transcript
    completeness and critical digests. Findings remain open until resolved or
    explicitly accepted by the named reviewer.
14. **Open ingress only by separate decision.** The authorized security and
    pilot roles either approve access, keep the target quarantined for
    investigation, or order teardown. Successful import alone never opens
    ingress.

## 6. Abort and quarantine conditions

Stop immediately and quarantine the target if any of the following occurs:

- manifest, signature, digest, byte length, inventory, or authorization
  mismatch;
- an undeclared artifact, credential value, symlink, special file, traversal,
  alias, or unexpected source-derived object appears;
- the backup conflicts with revocation, mandatory deletion, legal hold, or the
  accepted retention policy;
- database, application, writer, schema, migration, or evidence-format identity
  is unsupported or differs from the accepted combination;
- import or application startup performs an unreviewed migration or writes
  before compatibility is established;
- any unapproved ingress, egress, external authentication, source access, or
  production route becomes reachable;
- authorization widens, revoked access returns, invisible-object existence
  leaks, or old proof bundles bypass current authorization;
- staged or partial evidence becomes visible as complete;
- the resource ceiling is exceeded or capacity no longer covers the approved
  safety margin; or
- the operator, witness, security owner, sponsor, migration owner, or required
  platform partner invokes the stop channel.

After an abort, preserve only the minimum approved diagnostic record. Destroy
the failed restore target, decrypted staging, temporary credentials, and
derived artifacts under witness, or retain them only under a new explicit
authorization.

## 7. Restore transcript

The witnessed exercise must produce one immutable transcript containing:

- exercise, authorization, backup, source, and destination identifiers;
- named operator, independent witness, reviewers, and stop authorities;
- command/tool/binary/config/schema/writer/evidence identities and digests;
- precondition evidence, network-isolation evidence, and capacity reference;
- manifest and independently recomputed encrypted/plaintext digests;
- captured command start/end times, sanitized output, exit status, and errors;
- import, compatibility, health, authority-reconciliation, rebuild, and
  negative-check results;
- measured recovery point and recovery time against approved objectives;
- every deviation, abort, finding, resolution, and teardown action; and
- witness attestation plus final `accept`, `reject`, or `conditional` decision.

The transcript itself is sensitive pilot evidence. Its access, retention,
mandatory-deletion behavior, and final digest must be recorded.

## 8. Acceptance boundary

Item 5 can move to `accepted` only when the owner and independent reviewer have
resolved the `TBD` fields, reviewed the manifest-version-pinned backup/import
mechanism above, bound this artifact's digest, and completed the acceptance
record in [PILOT_PREREQS.md](./PILOT_PREREQS.md). That accepts the procedure,
not a particular backup or environment.

Item 8 remains separately blocked until items 5 and 7 are accepted, pilot
environment access is explicitly authorized, the restore operator and
independent witness are named, and an actual restore produces the transcript
defined above.
