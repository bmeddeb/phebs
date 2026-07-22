# phebs pilot authorization negative-test design

*Draft v0.1 · pilot prerequisite item 3 · design phase only*

This is a dependency-preview draft derived from
[THREAT_MODEL.md](./THREAT_MODEL.md), [PILOT_CHARTER.md §5](./PILOT_CHARTER.md#5-roles-and-authority),
the [Investigation domain contract](./INVESTIGATION_DOMAIN_CONTRACT.md), and
the [MCP envelope](./MCP_ENVELOPE.md). Items 1 and 2 are not assigned or
accepted, so this draft does not satisfy item 3, authorize test execution,
approve an environment, or close charter Gate 1.

## Record status

| Field | Value |
|---|---|
| Owner | `TBD` |
| Required reviewer | Security reviewer named in charter §5 |
| State | `blocked_unassigned` |
| Execution | prohibited until item 3 is accepted and the pilot environment is separately authorized |
| Required acceptance evidence | artifact digest, reviewer identity, timestamp, decision, unresolved findings |

The Security reviewer must author or materially extend this matrix rather
than merely approve a Pilot-lead-only design.

## 1. Test invariants

Every implementation of this design must prove all of the following:

1. For each sensitive identity surface, an **unknown identity input** and a
   distinct **existing-but-unauthorized identity input** are executed as two
   separate cases and compared byte-for-byte with the same golden refusal.
2. Authorization is applied before lookup, names, counts, aggregation,
   pagination, caching, proof resolution, comparison, export, and
   qualification.
3. Revocation invalidates sessions, cached projections, pins, proof access,
   bundles, cursors, Watches, and shared-object reads within the frozen SLA.
4. A principal never learns that another principal sees a different source
   universe, denominator, result set, historical scope, or object.
5. Failed, partial, canceled, stale, non-comparable, or truncated processing
   never becomes a complete or absence-eligible result.
6. Negative cases inspect response bytes, status, headers, timing/work class,
   logs, metrics, audit output, cache behavior, and retained artifacts. A body
   match alone is insufficient.

## 2. Canonical refusal oracle

The semantic source is
[`06-inaccessible-scope-refusal.json`](./fixtures/investigations/06-inaccessible-scope-refusal.json).
The checked-in presentation bytes have SHA-256
`8be063c052b4bb6fb138d0e398b79c53d344328e8f1f437fde7e236258e88dbf`.

For byte comparison the harness freezes the clock, request ID, tool name, and
semantic version to the fixture values, then serializes the parsed fixture as
UTF-8 JSON with recursively lexicographically sorted object keys, no
insignificant whitespace, and one trailing LF. For this fixture only, those
canonical bytes have SHA-256
`cfa307d02cdcc6c3e6da6193750bd50f509c1a3a69a0316c0063248aff21cf2f`:

```json
{"envelope_version":"1.0","errors":[],"generated_at":"2026-08-01T12:00:00Z","outcome":"refused","refusal":{"authoritative_text":"The requested object is not available to this principal.","code":"NOT_AVAILABLE"},"request_id":"01SYNTHETIC","tool":{"name":"find_contract_edges","semantic_version":"1.0"}}
```

This test-only fixture canonicalization does not select the platform's future
general canonical-JSON/signature algorithm. Any broader algorithm is a
separate versioned decision. Both paired executions must produce the bytes
above; it is not sufficient for the two live responses merely to equal each
other.

At the transport boundary the frozen oracle also requires:

- the same successful MCP execution classification (`isError: false`) and
  safe HTTP status for both cases;
- no cache, existence, object, scope, result-window, pack, validation,
  provenance, retry, or diagnostic headers whose values distinguish cases;
- no hidden identifier in logs, metrics, audit targets, traces, or errors;
- timing/work measurements classified against a preregistered tolerance and
  sample count. Timing is diagnostic unless the Security reviewer defines a
  binding threshold before execution.

## 3. Required principals and fixtures

The environment-specific manifest assigns opaque synthetic identities for:

| Symbol | Required state |
|---|---|
| `P_ADMIN` | administrator; sees the complete synthetic test universe |
| `P_MEMBER_A` | sees object/repository/evidence set A only |
| `P_MEMBER_B` | sees disjoint set B only |
| `P_REVOKED` | previously saw A; access and credentials are revoked during the test |
| `O_A` | sensitive object known to `P_ADMIN` and `P_MEMBER_A` |
| `O_B` | sensitive object known to `P_ADMIN` and `P_MEMBER_B` |
| `O_UNKNOWN` | well-formed opaque identity guaranteed absent from the fixture universe |
| `E_SHARED` | identical content atom placed independently in A and B |

All identities, repositories, names, counts, and source coordinates are
synthetic. A negative test must not use the sealed validation corpus or any
pilot source before environment authorization.

## 4. Paired non-disclosure matrix

For every row marked `golden`, execute case U with `O_UNKNOWN` and case F with
an existing identity forbidden to the acting principal. Each case is compared
independently with the §2 golden bytes.

| ID | Surface and setup | Case U / case F | Required oracle | Threats |
|---|---|---|---|---|
| NT-01 | Investigation/object read | unknown object / `P_MEMBER_B` reads `O_A` | golden; equal status, headers, work class, logs | TM-01, TM-02 |
| NT-02 | Contract-edge query | unknown referent / forbidden referent | golden; no facts, scope, coverage, pack, or result window | TM-01, TM-02, TM-17 |
| NT-03 | Evidence and proof resolution | unknown proof ID / proof for forbidden occurrence | golden; shared atom identity never exposed | TM-01, TM-07 |
| NT-04 | Coverage and counts | unknown analysis / forbidden analysis | golden; no zero, withheld total, denominator, or reason-count clue | TM-02, TM-17 |
| NT-05 | Snapshot comparison and diff | one unknown side / one forbidden side | golden; no comparability or prior-scope clue | TM-02, TM-16 |
| NT-06 | ReviewItem, Watch, Decision, and Baseline reads | unknown ID / forbidden ID | golden; no lifecycle or existence clue | TM-01, TM-02 |
| NT-07 | Dossier/export request | unknown object / forbidden object | golden; no export job, size, classification, or filename clue | TM-02, TM-16 |
| NT-08 | Search source/tree/history/SCIP routes | unknown repo / private forbidden repo | existing product 404-equivalent oracle; body and work-class equality recorded separately from MCP golden | TM-01, TM-05 |
| NT-09 | List and pagination entry points | universe with no unknown object / universe containing a forbidden object | authorized pages identical; no hidden total; tokens reveal no difference | TM-02 |
| NT-10 | Error and retry paths | malformed opaque unknown / equally malformed forbidden-correlated input | same safe validation boundary; no raw dependency or lookup detail | TM-01, TM-11 |

## 5. Stateful revocation and cross-principal matrix

| ID | Sequence | Required oracle | Threats |
|---|---|---|---|
| NT-11 | `P_REVOKED` reads A, then permission is revoked, then repeats through UI/API/MCP | every post-SLA read is golden/404-equivalent; no stale session-principal smearing | TM-03 |
| NT-12 | issue page token, then revoke access or transfer ownership | old token is invalid without disclosing which condition changed; no page data | TM-02, TM-03 |
| NT-13 | pin proof/run and create bundle, then revoke repository/object access | pin and retention survive only as policy permits; read is refused and no retained count leaks | TM-03, TM-13 |
| NT-14 | cache authorized evidence/result under `P_MEMBER_A`, then request same key as `P_MEMBER_B` | cache key cannot cross principal/visibility/authorization epoch; B sees only B projection | TM-03, TM-07 |
| NT-15 | place `E_SHARED` in A and B, revoke A, retain B | A cannot infer B placement; B remains readable; atom cannot be enumerated directly | TM-07 |
| NT-16 | creator with A+B shares object with recipient who sees B only | recipient projection names/counts only B and never indicates creator saw A | TM-16 |
| NT-17 | compare two snapshots across an authorization change | cause is not rendered as source removal; prior projection shown only to a principal authorized for both | TM-02, TM-16 |
| NT-18 | authenticate concurrent stateless MCP calls as A and B | every request uses its own principal; no session initiator or response cache smearing | TM-03 |

## 6. Integrity, completeness, and operational negatives

These cases do not use the fixture-06 golden for every response, but their
safe failure shapes and retained artifacts are frozen before execution.

| ID | Fault | Required oracle | Threats |
|---|---|---|---|
| NT-19 | cancel or fail a run before publication; allow a late worker to finish | no visible fact set; terminal diagnostic artifact cannot claim complete | TM-06, TM-17 |
| NT-20 | advance mirror HEAD after immutable snapshot resolution | reads stay on pinned objects or fail safely; no mixed revision | TM-08 |
| NT-21 | inject stale/failed/partial/excluded/truncated states | absence eligibility false with exact blocker; fixture 03/05/08 semantics preserved | TM-17 |
| NT-22 | inject credential canaries into approved secret fields | no canary in DB identifiers, mirror config, logs, metrics, process arguments, bundles, or backups | TM-04, TM-11 |
| NT-23 | malicious paths, symlinks, gitlinks, replacement refs, hooks, oversized blobs, and parser depth | bounded refusal/failure; no escape, lazy fetch, execution, or partial publish | TM-05, TM-12 |
| NT-24 | substitute binary/rule/schema/writer generation or roll back a writer | mismatch fails before publication/read promotion; old proof remains governed by compatible format rules only | TM-08, TM-09 |
| NT-25 | exhaust memory, disk watermark, timeout, output, or concurrency bound | bounded stop; policy and existing publication remain intact | TM-12 |
| NT-26 | inspect logs/metrics/audit after forbidden and canary requests | allowlisted fields only; no hidden names, query text, paths, counts, tokens, or excerpts | TM-04, TM-11 |
| NT-27 | inject mixed and conflicting external-metadata inputs | typed evidence basis retains every conflict with its source, version, and time window; rendering never silently arbitrates or presents metadata as direct source truth | TM-15 |

### Delegated threat obligations

Two threat-model evidence obligations are intentionally outside this test
matrix and remain blocking elsewhere:

- **TM-10** requires host-firewall and packet observation in the authorized
  pilot environment, plus log review. That is environment verification, not a
  design-only negative case.
- **TM-14** requires review of the named role/access roster, reviewer
  attestations, and custody log against charter §5.3. That is a governance and
  separation-of-duties review, not an implementation test.

Their absence from the NT rows is a delegation, not evidence of coverage or
acceptance.

## 7. Execution protocol and evidence

Prerequisite item 6 executes this design only after item 3 acceptance and
separate environment authorization.

1. Seal the test manifest: implementation commit, schemas, fixture digests,
   principals, permission graph, clocks/IDs, canonical encoder, route/tool
   inventory, timing plan, ordering seed and deterministic shuffling mechanism,
   and expected artifacts.
2. Prove fixture setup as administrator without exposing admin results to the
   paired-case executor or label reviewers.
3. Execute each case in a fresh randomized order using the sealed ordering seed
   and mechanism. For paired cases, retain U and F as separately identified
   executions while withholding the hidden classification from response
   comparison.
4. Capture response/status/header bytes, safe work counters, logs, metrics,
   audit rows, caches, and retained artifacts under approved handling.
5. Compare U and F individually against the golden, then compare all secondary
   channels. Any unexplained difference is a finding; rewriting the oracle
   after execution is prohibited.
6. Repeat every case once from a clean service restart. Revocation and restore
   cases additionally repeat across the applicable persistence boundary.
7. Publish a signed/digested result matrix containing pass/fail, evidence
   references, reviewer decision, and unresolved findings. No sensitive live
   identity appears in the shareable report.

## 8. Stop and acceptance conditions

Execution stops immediately on confirmed unauthorized disclosure, credential
egress, partial publication, non-reproducible setup, stale-principal cache
reuse, or loss of paired-case blindness. A stopped run preserves only approved
diagnostic evidence and begins the charter teardown path if retained pilot data
exists.

Item 3 can be accepted as a **design** only when its owner and Security
reviewer are named, dependencies 1 and 2 are accepted, the route/tool inventory
is complete, every open placeholder is resolved or explicitly blocking, and
the required acceptance record is committed. Design acceptance still does not
authorize item 6 execution.
