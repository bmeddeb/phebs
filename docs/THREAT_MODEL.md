# phebs pilot threat model and trust boundaries

*Draft v0.1 · pilot prerequisite item 1 · design phase only*

This document models the bounded pilot described by
[PILOT_CHARTER.md](./PILOT_CHARTER.md). It does not authorize an environment,
source ingestion, verification activity, Gate 1, Gate 2, or Epic 16. The
charter and the Investigation domain contract remain authoritative when this
draft is incomplete or ambiguous.

## Record status

| Field | Value |
|---|---|
| Owner | `TBD` |
| Security reviewer | `TBD` |
| State | `blocked_unassigned` |
| Required acceptance evidence | canonical artifact digest, reviewer identity, timestamp, decision, unresolved findings |
| Environment assumptions validated | none; design-phase document only |

The artifact may be drafted while unassigned, but it cannot become
`accepted` and cannot satisfy charter Gate 1 until the named Security reviewer
has reviewed it against the authorized pilot environment.

## 1. Scope and security objectives

The modeled system is a single-tenant, isolated phebs pilot processing one
frozen, principal-visible source universe and approved metadata inputs. The
security objectives are:

1. A principal learns nothing about source, objects, evidence, counts,
   denominators, services, owners, prior scopes, or result existence outside
   that principal's current authorization.
2. Facts, coverage, provenance, and eligibility publish atomically; partial,
   failed, canceled, or superseded work never appears complete.
3. Every claim is bound to immutable inputs, implementation identities, and a
   reproducible evidence chain. Human assertions and Decisions never rewrite
   extracted evidence.
4. Revocation, mandatory deletion, legal policy, and teardown override caches,
   pins, exports, and ordinary retention.
5. The pilot remains read-only and advisory toward source hosts and production
   systems. It cannot modify code, approve a migration, enforce a check, or
   broaden its own source universe.
6. Resource exhaustion, malformed corpus data, dependency failure, or
   operator error fails boundedly without data egress or weakened controls.

### Out of scope for this model

- Production multi-tenancy, anonymous access, billing, or public hosting.
- Fleet-mode peer trust and distributed-database correctness.
- Correctness of claims outside the frozen language, protocol, relationship,
  source universe, extractor version, and operating envelope.
- Security guarantees for an exported dossier after it leaves phebs. The
  export must be scoped and classified, but offline custody belongs to the
  recipient's approved handling controls.
- Validation-ceremony operational security — stage runners, sealed inputs,
  attempt markers, receipt custody, pinned-toolchain invocation, and
  module-cache sealing. These are governed by the sealed GATE2-V2 protocol
  and its stage records, not by this model; TM-09 does not cover them.

## 2. Protected assets

| Asset | Security property |
|---|---|
| Source mirror and immutable Git objects | confidentiality, integrity, authorized deletion |
| Zoekt shards, SCIP data, extracted facts, and caches | confidentiality, revision binding, rebuildability |
| Evidence atoms and occurrence associations | integrity; occurrence-level authorization; non-enumerability of shared atoms |
| Coverage, counts, exclusions, and attribution ledgers | non-disclosure, denominator integrity, reconciliation |
| Users, sessions, API-key hashes, OIDC links, and permission edges | confidentiality, integrity, prompt revocation |
| Pilot credentials and code-host tokens | confidentiality, least privilege, non-persistence in URLs/logs/artifacts |
| Run manifests, receipts, proof bundles, Decisions, and dossiers | immutability, provenance, current read authorization |
| Audit logs and metrics | integrity without sensitive names, queries, paths, or hidden counts |
| Pack, rule, schema, binary, toolchain, and dependency identities | authenticity, version binding, reproducibility |
| Gold labels, blinded samples, predictions, and reviewer assignments | custody, independence, disclosure ordering |
| Backups, restore media, temporary trees, and teardown records | complete inventory, access control, verifiable destruction |

## 3. Principals and trust assumptions

Human roles and their allowed actions are defined in charter §5. Charter
§5.2 is the authoritative machine-principal inventory and capability grant;
a machine principal absent there has no capability, and on any divergence
§5.2 wins. The table below records only the trust assumptions this model
analyzes:

| Principal | Trusted for | Not trusted for |
|---|---|---|
| Phebs server process | enforcing authenticated API/UI/MCP policy and coordinating bounded workers | granting access beyond its current permission inputs; making human Decisions |
| Supervised SurrealDB child | local state persistence under the server's isolated host identity | serving as an independent authorization authority |
| Git and zoekt child processes | bounded mirror/index operations with explicit arguments and scrubbed environment | interpreting policy; retaining credentials; executing corpus hooks |
| Extractor context | pure reads of explicitly supplied immutable objects within limits | network, corpus writes, dynamic loading, repository scripts, generators, or plugins |
| Managed indexing provider *(planned Epic 45; not currently registered)* | running one exact administrator-selected closed profile against one immutable private workspace and returning staged typed-index artifacts | interpreting policy; publishing authority; accepting browser-supplied commands, environment, credentials, cache URLs, raw build flags, ambient bazelrc files, shared output roots, or shared caches; treating its output as trusted |
| Approved identity provider | authenticating admitted identities | determining phebs object/evidence authorization by itself |
| Approved source/metadata providers | supplying versioned source, build, deployment, catalog, or ownership inputs | proving runtime behavior, current accountability, or cross-input identity equivalence |
| MCP client | presenting an authenticated request and rendering server-qualified output | strengthening conclusions, recreating negative wording, or treating an ID as authorization |

No machine principal is trusted merely because it runs on the pilot host.
Every credential, process boundary, input artifact, and child invocation is
explicitly scoped.

## 4. Trust boundaries and data flow

```text
approved human principal
  -> HTTPS/reverse-proxy boundary
  -> phebs authn + current authorization projection
  -> API / UI / stateless MCP
  -> query, evidence, Investigation, and proof services
  -> SurrealDB state + immutable local artifacts

approved source and metadata providers
  -> least-privilege ingress identity
  -> credential-scrubbed mirror / bounded adapter
  -> immutable snapshot and input manifest
  -> pure-reader extraction / isolated index child
  -> atomic staged publication

planned administrator-authorized managed indexing
  -> installed closed provider/profile
  -> exact private source workspace
  -> isolated build/index child with bounded capabilities
  -> untrusted staged SCIP bundle
  -> complete validation and atomic publication

authorized export request
  -> current principal projection and redaction
  -> immutable classified dossier
  -> recipient custody boundary (outside continuing phebs control)
```

The security boundaries requiring Gate-1 validation are:

1. **Ingress boundary:** external APIs, Git transport, identity provider, and
   metadata adapters into the isolated host.
2. **Corpus boundary:** untrusted repository bytes into parsers, Git readers,
   SCIP readers, child indexers, and any separately authorized managed build
   execution.
3. **Authorization boundary:** authenticated principal to object and evidence
   projections, including aggregates, refusals, pagination, and caches.
4. **Publication boundary:** staged worker output to visible immutable facts,
   coverage, provenance, and eligibility.
5. **Persistence boundary:** server and supervised children to local database,
   mirrors, indexes, logs, temporary files, backups, and restore media.
6. **Egress boundary:** HTTP/MCP responses, exports, logs, metrics, crash
   diagnostics, package/network access, and operator tooling leaving the host.
7. **Human-evaluation boundary:** predictions and coordinates to blinded
   reviewers, adjudicator, sponsor, and decision makers.

## 5. Threats, required controls, and verification obligations

| ID | Threat | Required control | Gate-1 or later evidence |
|---|---|---|---|
| TM-01 | Unknown and unauthorized identities produce distinguishable status, timing, body, or work | identical safe refusal; authorization before existence lookup and aggregation; bounded equalized work where practical | dual-case golden-response test plus timing/work review |
| TM-02 | Counts, coverage, exclusions, diffs, pagination, Review, or errors reveal hidden existence | compute only over current authorized projection; withhold rather than synthesize smaller totals; principal/snapshot/authorization-bound cursors | negative-test matrix covering every aggregate and cursor |
| TM-03 | Cached sessions, MCP state, proof IDs, bundles, or pins preserve access after revocation | stateless request authentication; reauthorize every read and occurrence; revoke cached projections/tokens; retention never grants access | revocation-after-pin/cache/session tests within frozen SLA |
| TM-04 | Credential material enters clone URLs, database rows, logs, command lines, crash output, or bundles | environment/secret-file injection; credential-free identifiers; startup audit; redaction and bounded diagnostics | credential canary scan across DB, mirror config, logs, process arguments, artifacts, and backups |
| TM-05 | Malicious corpus bytes trigger traversal, symlink escape, lazy fetch, replacement objects, hooks, code execution, parser exhaustion, or writes | canonical containment; literal paths; scrubbed Git environment; no lazy fetch/replacements/hooks; pure-reader capability boundary; per-file and aggregate budgets | adversarial corpus tests and process-capability inspection |
| TM-06 | A failed, canceled, superseded, or late worker publishes a partial or stale fact set | staged immutable output, publication lease, revision guard, atomic fact/coverage/provenance commit | kill/cancel/late-worker and revision-race tests |
| TM-07 | Shared content-addressed atoms become cross-repository existence or authorization oracles | expose only opaque authorized occurrence references; never enumerate atoms directly; filter before dedup-derived counts | two-principal vendored-blob test with disjoint visibility |
| TM-08 | Mutable refs, mirror advancement, stale metadata, or mixed writer versions corrupt provenance | resolve once to immutable IDs; bind snapshots and writer/format versions; exclusive migrations; fail closed on mismatch | advance-HEAD, rollback-writer, stale-input, and restart tests |
| TM-09 | Toolchain, dependency, pack, rule, schema, or child-binary substitution invalidates measured claims | digest-bound artifacts, reproducible builds, SBOM/scan, same-version reader/writer identities, signed release binding | independent digest verification and clean rebuild receipt |
| TM-10 | Network or telemetry egress exposes source, query, metadata, credentials, or results | default-deny egress; zero telemetry; allow only approved ingress/sync/auth endpoints and time-bounded dependency acquisition outside corpus parsing | host firewall/packet observation and log review |
| TM-11 | Logs, metrics, audit targets, traces, or operational errors disclose inaccessible names or data | allowlisted fields; no query text; safe error taxonomy; authorization-safe audit targets; bounded output | canary queries/paths and forbidden-principal log/metric inspection |
| TM-12 | Resource exhaustion causes host instability, uncontrolled disk growth, or weakened policy | CPU/memory/time/output/concurrency ceilings; disk watermark; bounded retries; stop conditions; isolated child for high-memory indexing | metadata estimate, representative slice, kill/OOM/disk-stop tests |
| TM-13 | Backup, restore, temporary files, caches, or teardown leave undeclared copies or restore stale permissions | complete data inventory; encrypted/restricted media; restore into isolation; credential rotation; authorization revalidation; witnessed destruction | restore transcript, artifact inventory, witness attestation |
| TM-14 | Operator, sponsor, reviewer, or partner role concentration breaks least privilege or blinded evaluation | named capabilities; separation rules; append-only audit; sealed disclosure timeline; no silent role substitution | role table, access roster, reviewer attestations, custody log |
| TM-15 | External metadata or human disposition is presented as direct source truth | typed evidence basis; source/version/time window; conflicts retained; no silent arbitration | mixed/conflicting-input fixture and rendered provenance review |
| TM-16 | Export or sharing leaks the creator's larger universe or hidden historical scope | recipient-specific current projection; no prior-scope counts; classified redacted export; reauthorization on reopen | creator/recipient differential tests and export inspection |
| TM-17 | A complete-looking empty result is produced from failed, partial, truncated, stale, or unenumerated analysis | claim-specific absence eligibility; reconciled independent universe; explicit blockers; server-owned qualification text | fixtures for zero, partial, stale, inaccessible, and truncated states |
| TM-18 | A managed indexing request turns repository BUILD/Starlark, generators, toolchains, a repository-controlled package-driver launcher, ambient rc/configuration, prehydrated dependencies, persistent Bazel state, caches, or child output into unbounded or cross-request host execution, credential/network access, source leakage, or typed-index authority | administrator-only request; separately merged source-free harness; installed closed profile; exact private workspace; pinned Phebs-owned planner/launcher/tool identities; scrubbed bounded environment; disabled ambient rc discovery; at most one fully resolved digest-bound operator configuration; immutable count/byte/digest-bounded prehydration bundle copied into request-private custody before execution; request-private bounded output user root/base and local caches; shared caches disabled; no network or remote cache initially; separately reviewed closed egress/credential policy before any later remote capability; Bazel-server/worker descendant supervision and shutdown proof; staged output treated as hostile; Bazel-native completeness plan, complete manifest validation, fenced atomic publication, and regenerate-on-restore before reader/provider registration | malicious-build, repository-launcher, ambient-rc, prehydration-substitution, cache/output-root cross-contamination, and profile-substitution fixtures; capability inspection; child/worker/server timeout/OOM/disk/cancel/hard-death tests; cleanup/restart/restore plus partial/stale/late-publication tests |

## 6. Control invariants

The following invariants are release-blocking:

- Authentication is necessary but never sufficient for object or evidence
  access.
- Authorization filtering precedes names, counts, aggregation, caching,
  pagination, diffs, proof resolution, and qualification.
- Unknown and unauthorized sensitive identities share one canonical refusal
  shape.
- A pure extractor never executes corpus-provided code, including generators,
  plugins, build scripts, hooks, or dynamically loaded artifacts.
- A managed indexing provider may execute repository build logic only after an
  explicit administrator request through an installed closed profile, inside
  the separate bounded boundary in TM-18. The initial boundary has no network
  or remote-cache access, ignores ambient rc files, and contains Bazel output,
  cache, server, and persistent-worker state inside request-private bounded
  custody. Only a pinned Phebs-owned planner/driver launcher may execute; one
  immutable digest-manifested offline bundle is verified and copied into that
  custody before repository code. Its staged output is untrusted and cannot
  become current authority until Bazel-native plan reconciliation, complete
  validation, and fenced atomic publication. Generated bundles are absent
  after restore until a newly fenced current-HEAD generation completes.
  That capability remains absent until Epic 45 implements and verifies the
  contract.
- Visible facts and their complete coverage/provenance publish as one atomic
  unit.
- A bundle, digest, proof ID, pin, cache entry, or prior access is not an
  authorization credential.
- No result states or implies “no consumers,” “safe to remove,” or “migration
  complete” outside its explicitly enumerated and eligible evidence scope.
- Confirmed unauthorized disclosure, uncontrolled egress, partial publication,
  provenance failure, or reviewer-custody compromise stops the pilot.

## 7. Open decisions before review

The named owner and Security reviewer must close or explicitly block each item:

- exact isolated-host topology, ingress path, TLS termination, and trusted
  proxy CIDRs;
- source-host, metadata, OIDC, package, time-sync, and administrative egress
  allowlist;
- secret store, rotation owner, token scopes, and emergency revocation path;
- approved MCP clients and whether any client-side transcript retention exists;
- log/metric destinations, field allowlists, retention, and operator access;
- resource ceilings, disk watermark, timeout/concurrency limits, and stop
  mechanism;
- authorization source, sync frequency, revocation SLA, and stale-edge policy;
- backup encryption/access, restore isolation, preservation policy, and
  teardown witness;
- label-review custody, disclosure mechanism, adjudicator, and role-conflict
  assessment;
- legal hold, mandatory deletion, and exported-dossier handling policy.

Until these are resolved and reviewed, this artifact remains a design draft
and Gate 1 remains closed.
