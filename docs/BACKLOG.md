# phebs · active backlog

T31.1 bounded pipeline diagnostics and Epic 32's complete microservice v2
contract/validation gate are retained in the completed backlog. T32.5 recorded
a conditional implementation GO on 2026-08-04 without authorizing runtime
registration or release. T33.1's canonical service-catalog contract, T33.2's
catalog ingestion/v1 migration, T33.3 independent service state, T33.4
authorized catalog reads, and T33.5's service directory/neutral demo are
retained in the completed backlog, closing Epic 33. T34.1's immutable
repository source/search generation, T34.2's exact service-query compiler,
T34.3's publication migration/recovery, and T34.4's shared search product/demo
are also complete, closing Epic 34. T35.1's generation-scoped scheduler and
T35.2's pin-aware lifecycle decision and T35.3's bounded sweep/capacity
control and T35.4's lifecycle recovery demo are complete, closing Epic 35.
T36.1's bounded immutable Git reader and source-partition contract is complete.
T36.2's shared Go source-observation contract, T36.3's content-addressed
observation publication, and T36.4's authorized progress/neutral multi-pack
demo are complete, closing Epic 36. T37.1's namespace-sharded declaration and
resolver catalog, T37.2's RPC caller postings, T37.3's Kafka topic postings,
and T37.4's service projections and atomic relationship roots are complete;
T37.5's exact readers, comparison, proof/Workbench integration, and neutral
demo are also complete, closing Epic 37. T38.1's exact service overview,
T38.2's cross-service explorer, T38.3's service-aware Impact/Workbench,
T38.4's strict MCP parity, and T38.5's neutral product closure are complete,
closing Epic 38. T39.1's neutral correctness, scale-admission, and recovery
gate and T39.2's honestly stopped authorized target run are retained in the
completed backlog. T39.3's security/lifecycle gate is also retained there.
T39.4's evidence/workflow gate stopped before unsealing and T39.5 retained the
no-release decision, closing Epic 39. T39.R1's mirror-lock contention closure
is also complete without authorizing or superseding a target rerun. A
source-free diagnostic from a later unfrozen very-large-monorepo run is
retained as engineering evidence, not as a scale pass. Epic 40 closed on
2026-08-30 after neutral-47 passed the frozen mechanics gate. T41.10 is
integrated at main `d92b6673db6d4b582c2223536fe52358629ae60e`, closing Epic
41 after separately proving the 8,000-service floor and 10,000 accepted
logical-service target. T42.1 is integrated through closure
`ea9dd555e5b19a752255fb099ae43721b4df971f`, with exact implementation
`8ca0d92410e3763b5c6c6664b26dc44ef2773edf`; canonical source-free
`spike/t421/plan.json` is 199,561 bytes at
`sha256:96ba209147858c8f38b922fcaf8766dc6d796051d2e8b0999960ed2e114faf34`.
T42.2 runner implementation is authorized, but its readiness audit found
frozen authority/work-contract conflicts. T42.1r1 prospective correction is
approved, with local checkpoint integration explicitly requested despite its
known-red complete constructor. T42.1r2/r3 are integrated; T42.1r4's separate
namespace/terminal-recovery production fix is implemented and component-tested.
T42.1r5's fixture-only correction now passes complete constructor acceptance;
all 62 top-level normal tests and the targeted race gates pass. The final
T42.1r1 correction now passes all 91 top-level normal tests, affected race gates,
and independent review; V2 is sealed. Ben authorized local fast-forward
integration of the sealed artifact and reviewed stack, followed by T42.2 runner
implementation. Host/tool execution freeze and ceremony execution remain
separately unauthorized and unestablished.
Epic 43 runs in parallel as the charter-governed presentation track: it
applies [DESIGN_CHARTER.md](./DESIGN_CHARTER.md) to every product surface,
starts at T43.1, and may not touch a scale plane, authority, or claim.
Epics 25–28 remain drafted and unscheduled; none is an implicit next ticket.
Epic 30's service-scoped
monorepo program completed on 2026-08-02, including the scope-aware UI,
operations guidance, and neutral ordinary-worker demo in T30.7. Its retained
completion receipt also records the post-review compatibility closure:
immutable v1/v2 proof bytes remain readable and the production exact Caller
Map envelope is validated through the strict MCP boundary. Completed
Epics 0–24, Epics 29–39, and P5 hardening are retained in the
[completed backlog](./BACKLOG_COMPLETED.md). Current posture and decision
points are summarized in [ROADMAP.md](./ROADMAP.md).

New work starts here only after its product boundary, dependencies, acceptance
criteria, and dated [PLAN.md](../PLAN.md) decision are reviewed. Tickets remain
PR-sized and dependency-ordered for a stacked workflow.

## Epic 40 · Very-large-monorepo derived-pipeline convergence *(completed 2026-08-30)*

Make the source-observation, candidate, extraction, and downstream generation
pipeline converge under a neutral repository shape with at least two million
regular-file owners. Search already demonstrated that it can publish a shared
physical generation beyond 1.6 million owners in one unfrozen environment;
that observation does not establish comfortable headroom, a supported scale,
or derived-pipeline completion. This epic closes the semantic and bounded-work
gaps that allowed committed search authority to coexist with opaque
observation-plan refusal, repeated repository-wide manifest validation, and
nonpublishing extraction work.

### Boundary

- Search publication and derived evidence are independent authority planes. A
  committed, validated search generation remains current when observation,
  extraction, resolver, or relationship work is unavailable or fails. The
  exact derived generation remains visibly pending, failed, or unavailable.
- The initial neutral envelope has at least 2,000,000 regular-file physical
  owners, at least 1,000,000 repository-lane placements and separately at
  least 1,000,000 caller-lane placements, more than 8 GiB of declared
  repository-lane Go bytes, and at least 32,768 IDL candidate inputs. T40.1
  separately
  freezes each plane's placements, unique blobs, declared bytes, and encoded
  bytes; overlapping repository/caller planes are never summed as unique
  source. Exact structural and semantic profiles precede any production
  aggregate cap change.
- Existing per-blob, per-member, per-child, per-request, per-evidence-chunk,
  and per-domain safety limits remain fixed unless the owning ticket measures
  that exact dimension and records a reduce-first decision. Increasing a
  timeout, retry count, or monolithic buffer is not an implementation.
- Generation controls stay small; corpus-sized records live in ordered,
  digest-bound members. Cold validation may stream a complete generation once;
  hot point reads, no-ops, retries, and status reads may not reopen or hash the
  complete corpus.
- A file-local unsupported syntax becomes an explicit classified gap when the
  pack contract permits per-file independence. It may not silently disappear
  or fail unrelated files. A corrupt control, inconsistent member, unknown
  authority, or aggregate-bound violation still refuses the complete domain.
- This epic does not multiply work by service count, raise the 4,000-service
  catalog cap, select cohorts or P6, authorize a private rerun, or change the
  T39 `DO_NOT_RELEASE` decision. Epic 41 owns service cardinality; Epic 42 owns
  the combined gate.

**T40.13 · Neutral two-million-owner convergence gate** — run the frozen T40.1
profiles through ordinary sync/index, candidate, observation, extraction,
resolver, relationship, recovery, and lifecycle workers with a deliberately
small at-most-100-accepted-service correctness control whose exact membership
and unowned-prefix counts stay within the existing v2 12,000-path cap. AC: at
least 2,000,000 regular-file physical owners publish one shared search
generation; the receipt names T40.5's effective blob-reader mode and proves no
silent batch disablement or go-git fallback; all applicable
derived partitions settle and publish, with deliberately absent inputs and
unsupported syntax remaining explicit; cold, warm no-op, one-partition delta,
A→B→A, interruption, stale worker, pressure, archive/restore, collection, and
authorized query cases pass exact oracles; the receipt reports wall/RSS,
allocated/logical bytes, children, reads, writes, transactions, retries, and
reuse by phase using closed source-free scalars; predetermined stop rules
choose continue, reduce, cohort experiment, or P6 investigation without
silently raising a bound. The result is mechanics evidence only—no target SLO,
service-cardinality, accuracy/completeness, release, private-rerun, migration,
or decommission claim. Epic 40 closes only on an independently reviewed receipt
and demo.

Implementation note (2026-08-08): the prospective `spike/t4013` plan,
isolated-custody preparer, ordinary-worker executor, exact control/product
oracles, stopped-run teardown, and source-free receipt validator are in review.
The frozen host prerequisite is 24 GiB memory and 120 GiB available disk so A
and B can coexist inside T40.5's unchanged replacement/rollback and hard
watermark envelopes. The initial host audit observed roughly 53 GiB available
and correctly blocked authoring. A later pre-freeze re-audit observed roughly
163 GiB available, above the unchanged prerequisite; no giant authoring or
execution has begun yet, no result exists, and the ticket remains open.

Measured outcome (2026-08-08): the one authorized frozen run bound commit
`b1b4e808e1987b3bf28e4afac21cc83b72aa27f2` and stopped at the cold exact
oracle. Preflight passed the unchanged host prerequisites; no cold authority
or successful cold phase metrics were retained, and every later mechanics
case is explicitly `not_run`. Exact teardown removed derived and scratch
custody. The source-free receipt selects `reduce`, authorizes no rerun or bound
change, and awaits independent review before any ticket/Epic closure.

Independent-review disposition (2026-08-08): review blocked the stronger
`reduce` classification because the consumed run retained neither failed-cold
meters nor executable digests. Its corrected source-free receipt is therefore
`stopped` with an `unclassified`, unsubstantiated decision; teardown remains
proved. The harness corrective pass binds clean archived source and executable
hashes, preserves failed-phase meters before classification, removes synthetic
reuse, proves a specifically started stale lease is fenced, requires nonempty
query/citation evidence, and closes stopped-receipt phase/decision validation.
These fixes authorize no rerun. A new ceremony requires separate review and a
new plan bound to a new exact execution commit.

Second review disposition (2026-08-08): the retained stopped receipt remains
byte-identical and unclassified. Prospective execution now preserves a captured
observation across server-shutdown errors, retains meter-finalization failures
as sticky classification blockers, requires executable digests after successful
preflight with an exact-byte exception only for the historical receipt, and
maps only typed exact-oracle, pressure, direct-recovery, or review-ceiling
causes to frozen decisions; other operational failures remain unclassified.
The stale-worker case uses semantic B, incrementally consumes diagnostics once,
rejects already-settled attempts, proves the selected live schedule's plans bind
to B's source digest, then requires that same attempt to settle `stale_fenced`
after semantic A supersedes it. No rerun or ticket/Epic closure is authorized.

Third review disposition (2026-08-08): log reports now discover candidates
only. B binding is proved by the production extraction validator, including
recovery schedule identity, generation ranges, descriptors, and domain plans.
The cursor drains through current EOF with a reused buffer and retains only
unmatched attempts. A read-only connection to the supervised store then
requires the exact B attempt to remain `running` on both sides of the semantic-A
source transition before its terminal `stale_fenced` report can satisfy the
oracle. Early healthy settlement or a probe race remains an unclassified
operational stop rather than `reduce`. The retained artifacts are unchanged and
no rerun or closure is authorized.

Large-host runner preparation (2026-08-08): a macOS ceremony driver now binds
the default `~/phebs` checkout to isolated `~/phebs-t4013-ceremony` custody,
checks the frozen host prerequisites, and keeps freeze/review separate from
execution. A reviewed plan digest plus explicit confirmation is required
before any giant authoring. Exact plan-bound cleanup removes both custody and
the credential-bearing prepared manifest on completed or stopped execution;
the return bundle allowlists and signs only the plan, source-free observation,
validated receipt, transfer manifest, checksums, and public signer material.
The driver authorizes no run by itself and accepts no private target corpus.
Its focused harness/Buf dependency checksums are sealed before runner use, and
preflight rechecks checkout cleanliness after hydration and tests.
The 1.6-million-file/5,000-service repository remains a separate combined-scale
target replay after the service-cap program, not an Epic 40 neutral substitute.

Large-host plan-review correction (2026-08-08): the first runner freeze,
`t40r1-neutral-01` at plan digest
`sha256:eb8430b97a543182e89c07b117cb7105e13ee4592171aa0992c7989f8c31ab8b`,
was stopped before custody or execution because the v1 plan retained measured
binary hashes only prospectively and did not independently freeze the host Go,
Git, or SurrealDB executables. The prospective v2 plan now binds ordered
version-plus-SHA-256 identities for the Go driver, compiler, linker, Git, and
supervised SurrealDB. Preparation verifies them before and after giant
authoring; execution verifies them before work, before classification, and
after teardown. Drift cannot substantiate a decision. Historical v1 plan and
receipt bytes remain unchanged; a new commit, independent plan review, and
fresh `t40r1-neutral-02` identifier are required.

Large-host Take 2 disposition and Take 3 correction (2026-08-09): signed
source-free receipt
`sha256:27722137720b409348caeaeda0b5d3f8532fe399726fe307c3b98a17cb771d15`
is a valid `unclassified` preflight stop, not scale evidence. The frozen-source
extractor treated the trailing slash on ordinary Git directory headers as a
noncanonical filesystem path; `.claude/`, the first archive entry, therefore
stopped before any measured binary build or cold work. Teardown destroyed
custody, and independent verification reproduced the receipt byte-for-byte.
Take 3 trims only the canonical directory-marker slash before the slash-level
clean/path-within checks; absolute, parent, doubled-separator, backslash,
symlink, and entry-type violations remain refused and table-tested. The bundle
verifier also normalizes BSD/macOS `wc -c` whitespace. `neutral-01` and
`neutral-02` are permanently non-reusable. Each future ceremony ID receives a
distinct signing key, and ignore rules cover accidental in-repository custody,
keys, whole-ceremony ZIPs, and transfer archives. Because the Take 2 private
key was included in an operator-created whole-root ZIP, it is retired for all
future evidence; only the generated source-free tarball may cross machines.
Take 3 requires a new commit, signer, `t40r1-neutral-03` plan, and independent
review. No rerun, scale, topology, release, or Epic-closure claim is implied.

Large-host Take 3 disposition and Take 4 correction (2026-08-08): Take 3 plan
`sha256:84b2cc6608ac50e1ebca9e4cc89b7fb7d24317376c1252008706bb3347998ef4`
at commit `b8856df15ee599e1eba71aded618cdcab1acb3c3` passed preflight, then the
first structural server missed the executor's implicit 90-second health wait.
The signed source-free package
`sha256:33f437197e94a93aff578db4e28376d05ebceafa1077e26a72c994c9a1f1642d`
and receipt
`sha256:32fd7aacfbfa0c5378568407abae56ea3c95d16d52caa1cef1d70b5bc7446a3c`
verify exactly, retain successful custody destruction, and correctly remain
`unclassified`; because the launch meter was registered only after health, the
retained evidence cannot identify the stalled startup stage or complete
process accounting. Take 4 advances prospectively to v3 plan/observation/
receipt schemas, freezes a 15-minute per-server readiness deadline inside the
unchanged eight-hour total ceiling, registers the meter at process launch, and
retains only closed source-free startup stage/error/count/digest facts. Raw
logs and all custody still remain private and are destroyed. V1/v2 artifacts
stay byte-verifiable. `neutral-03` is permanently retired; Take 4 requires a
new commit, signer, `t40r1-neutral-04` plan, and independent review. This is no
scale, topology, SLO, release, rerun, or Epic-closure result.

Large-host Take 4 disposition and Take 5 correction (2026-08-09): signed v3
package `sha256:7d241322a814f6bcdd4c14a3d0b69c8b0e490e2ff84444c859b7fb250584415d`
and receipt
`sha256:d6532ac1753d093597f897748f7629f46a3d69fa35dbe7217226882ef92223f3`
pass signatures, checksums, strict validation, and byte reconstruction. The
structural server reached `http_ready` and the cold meter retained its RSS,
allocated bytes, children, orchestration transactions, and retries, but all
3,600 readiness probes were classified `response`: the ceremony decoder
rejected Huma's legitimate top-level `$schema` transport field before reading
`status=ok`. The result correctly remains an `unclassified` operational stop;
it establishes no convergence or resource refusal. Take 5 consumes exactly one
bounded loopback Huma schema link before applying the unchanged strict
application decoder; arrays remain schema-free, while missing/duplicate/
foreign schema links, duplicate or unknown application fields, malformed or
trailing JSON, primitives, and oversized bodies fail closed. Real Huma object
and array responses and every ceremony target type are tested. `neutral-04`
is permanently retired. Take 5 requires a new commit, signer,
`t40r1-neutral-05` plan, and independent review; no claim or production bound
changes.

Large-host Take 5 disposition and Take 6 diagnostic correction (2026-08-09):
signed v3 package
`sha256:35a12c261542f78d9a638cd27e20a65af4630aede3ad46ce471f4b2d02f909a0`,
observation
`sha256:da505a4d3ad3c08f5551b811cdf20d9e3d443b5b3a23333f70b4c454f087221d`,
and receipt
`sha256:b4fd42257a9695d263ebcd547df0b1c7f149569fe4f0c0a49d436178e989094f`
pass signatures, checksums, strict validation, and byte reconstruction. The
Take 5 response correction worked: structural startup became healthy in
13,826 ms. Cold convergence then stopped after 7,214,033 ms with 3,712,614,400
peak RSS bytes, 20,080,287,744 allocated bytes, and successful custody
destruction. Those values cross no signed review ceiling; six aggregate job
requeue reports do not prove a per-unit attempt breach. The generic
operational receipt identifies neither the last convergence stage nor whether
its published control identity changed, and correctly remains `unclassified`.
Take 6 adds v4 schemas, signs the unchanged
two-hour full-convergence and 20-minute revalidation deadlines, and retains
only a bounded closed last-stage/change-count/first-and-last-progress-digest
record per exact-snapshot wait. It does not extend a deadline, retain corpus
content, or change production behavior. `neutral-05` is permanently retired;
Take 6 requires a fresh commit, signer, `t40r1-neutral-06` plan, and independent
review. No scale, topology, SLO, release, private-rerun, or Epic-closure claim
changes.

Large-host Take 6 disposition and Take 7 calculated deadline (2026-08-09):
signed v4 plan
`sha256:3f4111537ee2027d53a774a40d90b14d331cd9ba680c9f2388671560b07495bf`
at commit `6490e3e0d41c46662d7ac4d3fef4ab8118000407`, package
`sha256:f367d8ce9ca26c255c7a086e4bafd4ab35d007a0002a19407676c1d528c7c59c`,
observation
`sha256:00941d7f0717e5aa8ce2a9620f4e602b27a929323f51724f3f4c0771f46b9479`,
and receipt
`sha256:02ecfff24e47b787c47473e20b3c3d1249871530ce4cc1d6825096265b591b41`
verify exactly. Structural startup was healthy in 18,825 ms. The cold wait then
reached its exact 7,200,000-ms deadline after 1,440 probes, six progress-control
changes, different first/last digests, and a last stage of
`observation_publication`. Peak RSS was 4,022,009,856 bytes and allocated
custody was 20,082,331,648 bytes; no frozen resource ceiling was crossed and
teardown destroyed custody. V4 does not retain change timing or typed schedule
counters, so it cannot calculate remaining work or prove that any unspecified
extension would pass. Take 7 prospectively selects four hours: exactly twice
the censored interval and half of the unchanged eight-hour total ceiling.
It does not reserve the other half: prior work also consumes the parent, and
later mechanics and teardown receive only the actual remaining wall. V5 retains
first stage, stage changes, last-change wall, and the final bounded source-free
observation planning/schedule/publication counters already read by the oracle.
The 20-minute revalidation deadline and all production behavior remain
unchanged. `neutral-06` is permanently retired; Take 7 requires a fresh commit,
signer, `t40r1-neutral-07` plan, independent review, and explicit execution
approval. No release, SLO, topology, private replay, or Epic-closure claim
changes.

Large-host Take 7 retirement and Take 8 correction (2026-08-09):
`neutral-07` was signed against an earlier source commit and is permanently
consumed without execution. It cannot be reused for corrected bytes. Take 8
retires that ID in the driver and selects `t40r1-neutral-08` with a new signer.
The v5 four-hour diagnostic and eight-hour parent ceiling remain unchanged.
The parent timeout is now an explicit frozen cause, and
`review_ceiling_crossed` wins over a simultaneous meter-finalization failure;
measurement unavailability still wins over non-parent failures, including
meter-dependent RSS or allocation ceiling claims. Take 8
requires a fresh exact commit, independently reviewed signed plan, and explicit
execution approval. No production, release, SLO, topology, private replay, or
Epic-closure claim changes.

Large-host Take 8 disposition and Take 9 observability correction
(2026-08-09): v5 plan
`sha256:a5e6867a94c594be4c69e36f1f42b3449e5f4ae139832c2336b04519093804e6`
at commit `a019ec3b399d9b0459b1399b0191b6873c99a557`, package
`sha256:6b7cc1fb775f10d07145f5b01c4d384ad0a0f141e08817034ba815f0cc3caaf9`,
observation
`sha256:b74c8e7f34ee391298bc6b6243814478b6f2839441737d60170c90516d4db2e5`,
and receipt
`sha256:607c7b42eb991613a4312af1f36304c2264d24d34fe50d734e040ee3f30ac6d8`
verify exactly. The honest `unclassified` cold stop reached its four-hour child
deadline without crossing the eight-hour parent or resource ceilings. Its last
typed observation snapshot was at 1,315,017 ms—63 of 64 partitions succeeded,
one running—and no later typed snapshot was retained for about 3h38m. This is
a post-21m55 observation gap, not a confirmed 21m55 visibility loss. The
14,400,001-ms last-change and final `repository_visibility` stage are the
canceled terminal inspection recorded before the phase fence, not evidence of
forward progress. Take 9 keeps all deadlines unchanged, retires `neutral-08`,
selects `neutral-09`, treats cancellation only as a terminal result, and gives
post-health process exit the distinct `server_exited_during_convergence`
result. The process channel is checked before every inspection and actively
cancels an in-flight inspection, so known exit starts no probe and terminal
selection does not wait for the 30-second client timeout or an already-started
bounded synchronous filesystem/control read. HTTP observes cancellation; a
local read drains behind cancellation fences, and teardown joins that worker
before custody deletion. Each of at most 32 retained
transition records binds wall time, closed stage, failure class, and progress
digest. Only `pending` or `complete` inspections advance the separately bound
last-successful probe and timestamp; transport, status, response, and control
failures appear only in the timeline. A 33rd transition stops
unclassified with `convergence_transition_limit_exceeded` instead of
truncating evidence. These two source-free terminal identities remain named if
meter finalization also fails; the eight-hour parent still has first
precedence, and measurement unavailability still governs all other failures.
V6 preserves v1-v5 bytes and adds no production read,
lock, cache, scheduler, publication, corpus, memory, disk, or child work. The
ceremony adds one inspection worker and buffered result channel per sequential
probe; exit/cancellation leaves at most one bounded local read to drain before
teardown.
A new signer, independently reviewed plan, and explicit execution approval are
still required; no release, SLO, topology, private replay, or Epic-closure
claim changes.

Large-host Take 9 disposition and Take 10 progress correction (2026-08-10):
v6 plan
`sha256:09f3d7325dbbc1d3e2d6323bd43b0348731f5ad2a34df7b2f33b564d0cf00e28`
at commit `17cf54b85c45cbf25f01f30d910ffb7eaade40ec`, package
`sha256:bc8b7cea48d241bb780cf18da5523a677b5c49efe1b3f19931aa07e0040d6139`,
observation
`sha256:438c1c71f9677b92fa74d209ff797a8bcd8d6cc2e3c4b3993013aa25b1608c0b`,
and receipt
`sha256:4353c49a42204dea6a7f08e1982b66c61a750bcf4bfec1cc27dc4ed9f41b76ae`
verify exactly. The honest `unclassified` cold stop reached its four-hour
deadline below all resource ceilings and destroyed custody. Its last successful
typed progress at 1,345,019 ms reported 63/64 succeeded and one running; at
1,352,032 ms the endpoint entered one unchanged `status` tuple for the
remaining 2,880 completed inspections. V6 cannot recover the numeric status,
so the evidence proves a persistent progress-surface failure but neither its
409/500 cause nor underlying pipeline nonconvergence. It also exposed a
deterministic oracle bug: successful publication removes the marker that had
been the only source of the projected execution target, while the ceremony
requires that settled schedule. Take 10 retires `neutral-09`, selects
`neutral-10`, and introduces v7 without changing any deadline or ceiling.
V7 retains the exact bounded HTTP status, one closed reason (`409_stale`,
`409_control_absent`, `500_store`, `500_projection`, closed common statuses, or
`status_other`), and the last completed inspection identity/time; it never
retains a body or raw cause. Current progress preserves one settled current
schedule binding after marker removal, including recovery identities, and
removes it when a new source supersedes it. The hot current progress read adds
one small binding control read; no member/source read, lock, cache invalidation,
store transaction, child, poll, production bound, or claim changes. A new
signer, independently reviewed plan, and explicit execution approval remain
required.

Large-host Take 10 disposition and Take 11 host diagnostic correction
(2026-08-10): signed v7 plan
`sha256:c236269dcdfc0dbb580476fd9cda024f40ebb3485bd55b5c01a59d8b46ad9951`
at commit `dd523dd5da0995c0810c2bfcfbe070119b146038` was consumed by its
approved manual execute attempt, which stopped in `t4013-prepare` at the
pre-custody host-toolchain recheck. It produced no prepared manifest, authored
corpus, server, source-free observation, receipt, or scale/convergence result.
The original closed error did not identify the differing tool; exact
re-observation now reproduces the signed plan, so the transient mismatch cannot
be reconstructed. The following EXIT trap also referenced function-local state
after scope exit under `set -u`, but no custody existed to leak. Take 11
retires `neutral-10`, selects `neutral-11`, and preserves v7, the twelve phases,
all inputs and stop rules, the 24-GiB memory and 120-GiB available-disk
prerequisites, 15-minute readiness, four-hour convergence, 20-minute
revalidation, eight-hour parent, 20-GiB peak-RSS, 96-GiB allocated-data, and
five-attempt ceilings. A mismatch now reports only its closed tool name, and
the EXIT trap owns shell-escaped literal cleanup paths rather than scoped
variables. This is ceremony-only admission/cleanup work with no production
steady-state cost or claim change. A fresh commit, signer, independently
reviewed plan, and explicit execution approval remain required.

Large-host Take 11 disposition and Take 12 projection correction
(2026-08-10): the independently verified v7 execution at exact commit
`b0f5f67741eb7dc0a64e071fbd94eacd2d87b8a5` stopped `unclassified` at the
four-hour cold-convergence deadline. Startup was healthy in 12,570 ms; the last
successful probe at 19m00s reported 64 materialized partitions, 62 succeeded,
and two running. At 19m07s progress changed to HTTP 500 `500_projection` and
remained there through the final inspection at 3h59m55s. Peak RSS was
3,584,458,752 bytes and allocated data was 20,082,884,608 bytes, below the
frozen ceilings; later gates did not run, and verified teardown destroyed
custody and the prepared manifest. The evidence proves a persistent projection
failure but not underlying partition terminality. Exact-source bounded
reproduction found that mutable pointer identity races could be classified as
invalid and that cold-open cancellation was collapsed into invalid member
mismatch; v7 cannot identify whether either sustained the observed status.
Take 12 retires `neutral-11`, selects `neutral-12`, and introduces v8 without
changing any input, phase, deadline, ceiling, stop rule, or claim. Its merge
bar is: mutable pointer replacement and crossed pointer/cache snapshots are
409 stale while stable malformed controls and immutable corruption remain 500;
context cancellation survives cold validation; one provisional cache entry
pins and shares each current-generation cold open; failed opens remain
retryable; authorized errors expose and the source-free receipt retains only
the closed control/publication/planning/schedule/response projection substage;
v1-v7 bytes remain valid; focused tests, docs checks, glossary verification,
lint, and steady-state-cost review pass. This correction does not itself
authorize Take 12 execution or close T40.13.

Large-host Take 12 disposition and Take 13 repository-index terminal correction
(2026-08-10): the independently verified v8 execution at exact commit
`d91f3f2e01b6997b30984130aea6a58d33364dc5` stopped `unclassified` at the
four-hour cold-convergence deadline after 2,880 unchanged `repository_index`
probes. Startup was healthy in 13,031 ms; three Git children, three index
children, and two retry reports do not establish whether the third attempt
remained active or failed. Peak RSS was 3,244,900,352 bytes and allocated data
was 325,836,800 bytes, below the frozen ceilings; later gates did not run, and
verified teardown destroyed custody and the prepared manifest. Exact-source
investigation proved the ceremony read `/api/repos`, whose legacy fields cannot
show queue progress or terminal failure before successful publication, while
the existing bounded `/api/repo-status` current-record projection already can.
Take 13 retires `neutral-12`, selects `neutral-13`, and introduces v9 without
changing any input, phase, deadline, ceiling, stop rule, or claim. Its merge bar
is: ceremony polling consumes repository status; its digest includes only
indexed commit, closed projection state/status, and attempts; raw error, target,
worker, and timestamps are excluded; failed/canceled before publication stop
unclassified as `repository_index_terminal`; unavailable and active states
remain pending; exact publication proceeds; v1-v8 bytes remain valid; focused
tests, docs checks, glossary verification, lint, and steady-state-cost review
pass. This correction changes no production path and does not authorize Take
13 execution, close T40.13, or unblock Epic 41.

Large-host Take 13 disposition and Take 14 bounded teardown correction
(2026-08-11): v9 plan
`sha256:7aaea5863cc88a34f202bd6226d0b983a1dc47893e9d45e7211c6a091c5d83cf`
at exact commit `e8a1e7eaa112010f5a7b9115a8a3fbfd2b770217` emitted the operational
`repository_index_terminal` signal, then its first stopped-run custody removal
returned wrapped `ENOTEMPTY`. No observation or receipt was sealed. The
independent plan-bound fallback removed custody and the prepared manifest, but
the unsealed signal is not an official classification or production failure
proof. Take 14 retires `neutral-13`, selects `neutral-14`, and preserves v9 and
all frozen inputs, phases, ceilings, rules, and claims. Its merge bar is: every
custody teardown uses the same exact-scope helper; only `ENOTEMPTY`/`EEXIST`
receive at most ten retries after the initial removal with 100-ms spacing; a
250-ms stable-absence fence is required; scope, symlink, permission, stat,
unexpected errors, and exhaustion still fail closed; injected late-writer and
non-transient tests pass; stopped execution remains receiptable after transient
cleanup; docs, glossary, lint, and steady-state-cost review pass. This changes
no production path and does not authorize Take 14 execution, close T40.13, or
unblock Epic 41.

Large-host Take 14 disposition and Take 15 harness closure
(2026-08-11): the independently verified v9 run at exact commit
`c1232d0a4e797eedbef129c178e5281913f20daf` stopped sealed and unclassified as
`repository_index_terminal` after all three index attempts. Startup was healthy
in 11,273 ms; cold stopped at 291,458 ms with three index children, two retries,
3,479,928,832 bytes peak RSS, no publication, and verified teardown. Exact
neutral-corpus reproduction proved the pinned child succeeds on all 2,000,002
files in 78.54 seconds; the ceremony's 250-ms sync poll had also created an
83-ms job-heartbeat deadline, whose timeout canceled each child. A five-second
heartbeat completed and published the same index. Review also proved the
pressure phase authored no pressure or fresh capacity sample, stopped disk
evidence lost transient allocation, and interruption polling recursively
rescanned derived members while suppressing traversal errors. Take 15 retires
`neutral-14` and introduces v10. Its merge bar is: default runner heartbeats are
`max(interval/3, 5s)` with four-heartbeat stale recovery and explicit overrides
preserved; the ordinary 15-second default is unchanged; v10 freezes an 82%
pressure target and 80-GiB custody-ballast bound inside the unchanged 96-GiB
ceiling; one ordinary-server restart observes a complete real `collect` cycle,
unchanged authority, ballast removal, and a complete `normal` cycle; a
constant-cost one-second capacity sampler retains transient allocation with
direct phase-fence gauges; interruption scans only the three package roots,
their one frozen repository and known fixed-depth controls, and fails closed
on errors without descending into immutable generations; pressure targeting
selects the exact 82%-reporting byte band and its startup label remains v10-only;
a terminal raw index error reduces only to closed
`lease_heartbeat` or `other`; v1-v9 bytes remain valid; `neutral-14` is permanently
retired; focused tests, docs, glossary, lint, and steady-state-cost review pass.
The runner correction changes no request, sync scan, handler, child,
publication, cache, lock, memory, disk, or topology asymptotic; fast-poll
configurations write fewer heartbeats and use a bounded 20-second crash-recovery
window. All other changes are ceremony-only. This does not authorize Take 15,
close T40.13, or unblock Epic 41.

Take 15 investigation disposition (2026-08-11): the independently verified
v10 ceremony stopped unclassified at its four-hour cold-convergence deadline.
Its last successful progress response showed 64 materialized partitions, 62
succeeded, and two running; seven seconds later the response became persistent
HTTP 500 `500_projection_response`. Exact-source investigation found a
production projection defect: defensive copies converted a valid non-nil empty
`unsupported_reasons` receipt into nil, which the receipt validator correctly
refused. The fix preserves nil versus empty through both copy boundaries and
adds an all-valid-Go publication through the real progress reader plus an exact
HTTP `[]` regression. A valid publication pointer proves complete member and
observation validation, but not the destroyed schedule's eventual 64/64
terminal status because pointer installation precedes final chunk settlement.
This correction authorizes no rerun, ticket/Epic closure, topology or bound
change, scale/SLO claim, or release.

Take 16 readiness investigation (2026-08-11): review of the phases that Take
15 never reached found a deterministic ceremony-only recovery blocker. Both
recovery exercises invoked the production live-backup command after graceful
server shutdown had removed the runtime descriptor and stopped its endpoint.
Take 16 retires `neutral-15` and introduces v11 without changing the v10
corpus, phase order, deadlines, pressure target, ceilings, rules, or nonclaims.
Its merge bar is: interruption starts one explicitly measured semantic backup
server, revalidates unchanged A authority, creates backup while live, then
stops, restores, verifies, interrupts, and restarts; archive restore backs up
the already-running structural server before stop; recovery command roots,
descendants, and transient allocation are measured; concurrent server/command
RSS is conservatively summed; v1-v10 startup/wait inventories remain closed;
`neutral-15` is permanently retired; focused recovery, exact schema, harness,
production recovery, docs, glossary, lint, and steady-state-cost gates pass.
The later-phase audit and bounded real-binary rehearsal are now complete. They
found production integration defects in addition to the harness order: the v2
observation inventory rejected normal A→B replacement; whole-repository
partition roots were not a resolver declaration source; settled empty domains
had no resolver authority; downstream events could be lost behind retry
backoff; and readiness used filesystem success pointers rather than the exact
store authority for terminal/empty domain roots. The repaired path also restores
partition and whole-search lifecycle controls, uses one bounded Git batch child
per active extraction partition, leaves focused analysis units on their legacy
lane, and keeps whole repositories on the T40 partitioned lane. A semantic
real-binary rehearsal passed in 105.98s. A structural cold A→B→A-return
rehearsal passed in 161.45s. Both included live backup, offline restore, exact
restored convergence/lifecycle, and authorized query; the semantic query also
required its citation. Focused production/store packages passed, including the
21m19s real-Surreal store suite under an explicit 30-minute package bound.
This remains readiness, not execution authorization. Freeze still requires a
clean exact commit, independent plan review, and Ben's explicit approval.

Take 16 measured outcome (2026-08-12): the approved v11 ceremony at exact
commit `e9ceb4351c001fd0c09b9db98d5ced9f5d37dac4` stopped honestly at the
exact four-hour cold-convergence deadline as `unclassified`
`convergence_deadline_expired`. Startup was healthy in 11,272 ms; the last
successful progress probe at 2,035,015 ms reported 64 materialized
observation partitions, 62 succeeded, and two running; 6,619 ms later the
inspection became a closed `extraction_publication` control failure and never
recovered. Peak RSS and allocated data stayed below their frozen ceilings, no
authority changed, no later gate ran, teardown destroyed custody and the
prepared manifest, and the source-free package verified. The custody-bound
live log, observed before teardown and never sealed, reported three exhausted
attempts on `partitioned extraction publication limit exceeded` at partition
41 with `domain result aggregate exceeds its frozen limit`. The cause is
re-derivable source-free from the frozen generator and record encoder:
2,000,000 repository-plane records × 396 encoded bytes = 792,000,000
aggregate candidate-member bytes across 489 members; ordinals 0-40 accumulate
66,502,656 within the 67,108,864 allowed, and ordinal 41 reaches 68,124,672.
T40.9 derived its 64-MiB member-byte aggregate as the next binary boundary
above the semantic profile's 39,182,336 canonical extractor-output bytes and
applied it to cumulative candidate-member input controls, a population no
retained artifact measures; the retained T40.8 maximum member times the same
ticket's frozen 489-member shape already requires 466,685,952 bytes, so the
contract was unsatisfiable as frozen.

Reduce-first disposition (2026-08-12): the refusal satisfies the frozen
`reduce` trigger — a frozen production bound refused before complete
authority — and its correction proceeds reduce-first in two tickets. The
first is a readiness ticket with no production-bound change: record this
source-free derivation; convert aggregate-limit errors to closed
`pipelinerefusal` data; classify the deterministic planning refusal terminal,
eliminating the two futile rebuilds; add a bounded extraction status channel
carrying generation state, total/pending/running/succeeded/failed partition
counts, current-authority state, and the closed terminal-refusal dimension,
observed value, and limit; teach the ceremony harness to retain those
source-free fields and terminate on a terminal extraction refusal; and
preserve raw Take 16 evidence and v1-v11 validation unchanged. The second is
a separately reviewed contract ticket: split the dual-use bound so the
per-partition reservation backstop keeps its existing protected value while
aggregate candidate-member input becomes a distinct limit; derive that limit
from the frozen generator (489 × 4,096 × 512 = 1,025,507,328; next
established binary boundary 1 GiB); keep the 64-MiB canonical and encoded
extractor-output ceilings; introduce a versioned v2 plan contract while
persisted v1 plans continue validating against their original v1 limits,
with recovery, restart, backup/restore, archive, lifecycle, and downstream
validators dispatching by schema version; and pin the 792,000,000-byte
profile as a source-free regression fixture. Before any Take 17 freeze the
serialized-extraction throughput risk must be measured: roughly 1,956 chunks
across the four `.go`-enumerating domains share the single repository token,
pricing roughly 6.3 seconds per partition inside the remaining cold window;
feasibility comes from the measured latency distribution, not only the mean,
and neither concurrency nor the four-hour deadline may be silently increased.
No production constant changes with this record; Take 17 freezes only after
both tickets pass their merge bars and independent review. This disposition
authorizes no rerun, bound change, T40.13 or Epic 40 closure, scale/SLO
claim, release, or Epic 41 progression.

Reduce-first readiness implementation (2026-08-12): the first ticket is
complete on its scale branch without changing any production bound. Every
domain-result aggregate refusal now carries one validated closed
`pipelinerefusal` measurement; candidate-member bytes are pre-summed before
reservation so the neutral fixture reports the exact 792,000,000 observed
bytes against 67,108,864. That deterministic planning refusal is terminal on
its first execution and its durable error can be decoded only when it is the
exact canonical closed receipt. A post-cutover latest-extraction-job pointer
projects only status, attempts, and an optional validated refusal. The new
authorization-first `/api/extraction-progress` response adds bounded schedule
and current-domain counts; its reader performs two exact schedule point reads,
one small generation-control read, and at most one current-pointer read per
domain, with an authority recheck around the response and no candidate member,
source blob, observation member, result, or evidence-payload read. Take-plan
v12 preserves v1-v11 validation and retains only that closed progress plus the
closed job/refusal projection; a terminal limit refusal stops immediately as
substantiated `reduce`, while another terminal extraction job remains
`unclassified`. Exact-bound/one-over, 792,000,000-byte, terminal/no-retry,
authorization-first, real-store projection, HTTP, schema-history, and ceremony
classification tests pass. The separate split/versioned aggregate-contract
ticket and measured throughput gate remain open; this implementation alone
does not authorize a Take 17 freeze or execution.

Versioned aggregate-contract disposition (2026-08-12): the second ticket is
complete. Persisted `phebs-extraction-domain-result-plan-v1` controls retain
their exact wire, digest domain, and 64-MiB aggregate validation. New plans use
`phebs-extraction-domain-result-plan-v2`: the per-partition member reservation
backstop remains 64 MiB, while a distinct aggregate candidate-member input
limit is 1 GiB, derived from 489 × 4,096 × 512 = 1,025,507,328 bytes and the
next established binary boundary. The exact 792,000,000-byte neutral profile
is admitted by v2 and still refused by v1; exact-bound, one-over, cross-version
tamper, decode, recovery, downstream, and real-binary tests pin dispatch.

The representative throughput gate also closes. Five fresh runs used one exact
4,096-record partition for each of the four serialized `.go` domains, with the
frozen 4,608-byte structural blobs and 512 reuse classes. Total extraction
after observation became current was 13.190564417, 10.275793666,
10.251969458, 10.122023667, and 10.106630500 seconds. Every poll observed four
separate one-partition completions. Conservatively repeating the slowest entire
four-domain sample 489 times prices 1,956 work items at 6,450.185999913 seconds
(107.503 minutes), below the roughly 3.4-hour extraction budget without raising
concurrency or the deadline. The complete representative A→B→A-return plus
backup/restore rehearsal passed. That rehearsal also found and closed a
full-server OpenAPI startup panic caused by observation and extraction response
types sharing the Huma component name `Progress`; the extraction wire now has
the distinct defined type `ExtractionProgress`, with both routes registered in
one regression. Take 16 is permanently retired. Freeze remains a separate
commit-bound action and execution still requires explicit approval.

Repository-scale correction to that throughput disposition (2026-08-12):
Take 17 proved the 4,098-file representative extrapolation nonconservative, and
the subsequent exact 2,000,002-file structural diagnostic closes the question
against the real shape. Observation reached current at 1,782,660 ms. Thirty-two
successful, non-reused extraction attempts measured 95,650 ms minimum,
97,899 ms p50, 101,815 ms p95, and 102,067 ms maximum. Across the actual 1,956
serialized work items, p50 and p95 project observation-plus-extraction
completion at 53.687 and 55.815 hours, exceeding the unchanged four-hour
deadline by 49.687 and 51.815 hours.
Source acquisition accounted for 3,136,191 of 3,149,203 aggregate runtime
milliseconds (99.587%); extractor execution, result installation, assembly,
and scheduler settlement are not the wall. Exact-source review binds the cause:
every `GitSparseSource.AcquirePartition` reopens the current candidate via
`Provider.OpenCurrentPublication`, which strictly validates the complete
792,000,000-byte candidate publication before selecting one bounded sparse
partition. Take 18 must not freeze. A versioned readiness correction must share
one generation-scoped strict candidate open across attempts while preserving
current-pointer refencing, stale replacement, cancellation, failure eviction,
restart/recovery, bounded per-partition reads, and exact old-generation release.
The diagnostic custody and temporary plan were destroyed. This record changes
no concurrency, deadline, threshold, topology, claim, release posture, or Epic
41 sequencing.

The generation-scoped strict-open correction is implemented on the Take 17
disposition lineage (2026-08-12). `Provider.OpenCurrentPublication` retains at
most one already validated immutable candidate generation per repository and
single-flights concurrent opens for the same exact `candidate.State`. Every
caller still reads and confirms the persisted pointer before using the handle
and repeats that fence after the open/cache lookup; a pointer change during an
open is rejected and evicted. A generation change replaces the cache entry and
drops its only retained reference to the old publication. A waiting caller may
cancel independently; an opener cancellation, nil result, or validation error
is not cached, so recovery retries from immutable bytes. Restart begins with an
empty process-local cache and reconstructs the exact current generation. The
steady-state extraction attempt keeps the existing four bounded pointer reads,
one short cache-mutex acquisition, bounded sparse root/domain/partition control
reads, and admitted Git-object reads; it no longer rereads and hashes all 489
candidate members. Memory retains one already bounded manifest/unit view per
repository and no candidate-member contents, Git blobs, file descriptors, or
repository lock. Candidate, sparse, plan, result, and evidence schemas and
historical bytes remain unchanged. Take 18 remains unauthorized until a fresh
exact repository-scale timing diagnostic at the committed correction leaves
credible headroom inside the unchanged four-hour window.

That exact remeasurement passed at committed source
`cd5dc7fd669b3f5a902995d0bbaebbea58195604` (2026-08-12). Observation became
current at 1,813,056 ms. The exact schedule retained 1,956 total partitions;
36 attempts completed between bounded status probes with zero failures,
terminal refusals, or reuse. Runtime was 427 ms minimum, 434 ms p50, 442 ms
p95, and 445 ms maximum. Projected p50 and p95 observation-plus-extraction
completion are 2,661,960 ms (44.366 minutes) and 2,677,608 ms (44.627 minutes),
leaving 11,738,040 ms
(3h15m38.040s) and 11,722,392 ms (3h15m22.392s) inside the unchanged
14,400,000-ms window. Across all 36 attempts, source acquisition consumed
1,026 ms, executor work 13,650 ms, result work 294 ms, assembly 278 ms, and
total runtime 15,633 ms; the repeated full-candidate wall is closed. The
diagnostic custody and temporary plan were destroyed and about 156 GiB free
disk restored. The correction is ready for independent review and a fresh
Take 18 freeze decision. This diagnostic is not a ceremony pass and does not
itself authorize execution, Epic 40 closure, Epic 41 progression, a release,
or any public scale/SLO claim.

The timing headroom is deliberately phase-local. Resolver publication,
relationship publication, delta/replacement, recovery, authenticated product
replay, and the remaining frozen ceremony phases have never executed at the
two-million-owner shape inside a ceremony and are not included in this
projection. Take 18 exists to measure those phases end to end under the frozen
total-wall and resource ceilings. The result therefore supports freezing a
new attempt; it does not predict that attempt's terminal classification.

Take 18 disposition and Take 19 readiness (2026-08-12): the verified v13
source-free result stopped `unclassified` at the cold convergence deadline.
Its structural profile nevertheless converged end to end in 3,910,284 ms with
1,956/1,956 extraction attempts complete, zero failure or terminal refusal,
and complete relationship publication. The semantic profile entered failed
observation planning at 115,006 ms and remained observably failed through the
last successful probe at 14,395,007 ms; the harness lacked a terminal rule for
that lane and waited to 14,400,003 ms. Peak RSS and allocation remained below
the frozen ceilings, all later phases were skipped, verification passed, and
custody was destroyed. The sealed evidence has no closed planning-failure
class, so the retained record does not speculate about cause.

Exact-source reproduction independently establishes the correction: the
semantic profile's 262,144 unique blobs exceed the legacy v1 observation
generation's 250,000-record limit but fit selected v2's 4,000,000-record limit;
despite v2 selection, production still required a fresh v1 publication before
v2 could be scheduled. Take 19 readiness removes that hidden prerequisite.
Selected v2 now plans directly from current source authority, leaves v1 as
historical authority, recovers a settled-lost-completion schedule with a new
immutable identity, and marks only closed deterministic v2 limit/invalid
refusals terminal. Stale generations, cancellation, transient store failure,
and downstream notification remain retryable.

The selected v2 progress contract performs bounded source, pointer/root, and
one-item schedule reads, with one exact failed-chunk projection only after
terminal settlement; only a canonical closed `pipelinerefusal` may escape and
raw durable error text is excluded. It rechecks source, pointer, and schedule
around the response and scans no members. Ceremony schema v14 requires this
route, retains its bounded last decoded status/refusal, and terminates within
one probe: a closed limit selects substantiated `reduce`, while another
terminal result remains `unclassified`. Historical v1-v13 bytes and validation
remain closed. Startup avoids the obsolete v1 generation and adds no new work
beyond the already selected v2 one-item schedule; request cost remains constant
in corpus size. No production bound, deadline, concurrency, topology, service
cap, lifecycle policy, or release posture changes. The full merge bar and an
independent review remain mandatory before a separately approved Take 19
freeze; neither T40.13 nor Epic 40 is closed by this readiness work.

Independent-review remediation (2026-08-13): the first review refused a Take
19 freeze. V14 terminal observation and extraction probes now retain their
typed bounded projections before terminal/error probes are excluded from
successful convergence counters, and an end-to-end stopped-observation test
pins sealing plus receipt validation for both terminal classes. `reduce` now
requires a canonical validated refusal on the exact selected route; malformed,
wrong-stage, wrong-dimension, and wrong-limit receipts remain unclassified.
The four new stop codes are v14-only, and freeze preparation rejects a frozen
profile expected to refuse the required selected-v2 observation route.

Production now terminalizes invalid v2 inventory failures only where the caller
owns a deterministic immutable-input boundary. Invalid states from mutable
pointer, publication, or collection windows remain retryable. A selected-v2
runtime that claims a pre-cutover v1 schedule establishes v2 ownership and
settles the obsolete row without reopening source or running a v1 census, and
v2 binding/enqueue uses the same mutation fence as collection. CI race coverage
includes observation publication; stale progress-fence and downstream callback
tests are retained. The Take 18 findings record now carries the exact closed
refusal tuple and explicit attempt counts.

This closes the code/evidence blockers only. Before Take 19 may freeze, the
exact 262,144-blob semantic profile must be measured at committed source against
the unchanged four-hour cold, eight-hour total, 20-GiB peak-RSS, and 96-GiB
allocated-data ceilings. The miniature readiness rehearsal is not that record.

Exact semantic fit result (2026-08-13): do not freeze Take 19. On committed
source `bcfd01c871a6c37e4dda7d03d8bfdb7bdb3b4b57`, the ordinary binary completed
repository indexing and the selected-v2 262,144-record observation publication
without the legacy v1 refusal, entered extraction at about 24 minutes, and
reached `repository_visibility`; it did not complete before the 14,400,000-ms
cold deadline and the diagnostic ended at 14,462,200 ms. No typed production
refusal or resource crossing appeared. The workspace remained around 2.6 GB
and combined live RSS around 0.15 GB near the end, far below the unchanged
96-GiB allocation and 20-GiB RSS ceilings. Custody and credentials were
destroyed and disk returned to baseline.

The retained diagnostic did not seal final extraction counters on its timeout
path, so no unfinished-partition count or specific production bottleneck is
claimed. Next readiness work must make that timeout evidence bounded and
source-free, remeasure the semantic extraction/relationship tail, and reduce
the measured work without raising the cold deadline, concurrency, production
bounds, topology, or resource ceilings. Take 19 remains unfrozen.

The corrected bounded capture at committed source
`5d776ef5a982a11987789ea24cf914506d4fd2bc` closes that evidence gap but still
refuses freeze. At the exact cold deadline, extraction was settled: 226 of 264
partitions succeeded, 38 failed, and none remained pending or running;
relationship publication never started. The 290 retained attempt reports
contained 226 completions, 32 retryable failures, and 32 terminal refusals.
Executor work dominated at 3,495,321 ms total and 300,002 ms maximum, while
source acquisition totaled 5,526 ms. The maximum aligns with the frozen
five-minute partition deadline, but the source-free record lacks the closed
refusal dimension/observed/limit tuple required to select a governed decision.
Peak RSS was 1,674,674,176 bytes and allocated data 2,618,982,400 bytes, both
below ceilings; cleanup destroyed custody and removed the temporary tree.
Before another fit run or Take 19 freeze, retain and validate the per-partition
closed refusal tuple and independently review whether it requires corpus
reduction or a production correction. No deadline, concurrency, limit, or
topology change is authorized.

Partition-refusal attribution readiness (2026-08-13): the production executor
now closes deterministic partition failures at domain inventory, extractor
execution, or evidence staging, including the existing encoded-byte staging
reservation. Diagnostics-enabled partition timing v2 projects the six closed
refusal scalars only on terminal attempts while accepting retained v1 reports;
the source-free aggregate retains at most 32 canonical summaries plus an
explicit unknown count and proves complete terminal-attempt accounting. A
settled schedule with failed partitions stops immediately as unclassified even
after ordinary-job collection, while exact failed/canceled jobs retain
precedence and active work remains pending. No limit, deadline, concurrency,
topology, resource ceiling, oracle, lifecycle policy, or release posture moved.
The next prerequisite is a fresh committed-source semantic fit diagnostic and
independent reduce-or-correct review; Take 19 remains unfrozen.

Complete-domain oracle correction (2026-08-13): pre-freeze review found that
the ceremony inspector sums all nine enabled extraction-domain roots while the
final oracle still compared the result with the frozen IDL-only 49,152-fact,
98,304-row aggregate. The two 65,536-input Kafka producer families add exactly
131,072 facts, so a complete semantic publication is 180,224 facts and 360,448
rows. Ceremony finalization now pins those totals, all nine published domains
for both profiles, and zero structural facts/rows; tests reject the stale
IDL-only value, missing domains, and unexpected structural evidence. No
production behavior, limit, deadline, concurrency, topology, resource ceiling,
or claim changed. The exact semantic fit rerun and independent review remain
mandatory before Take 19 freeze.

Exact semantic fact-limit result (2026-08-14): do not freeze Take 19. At exact
commit `430523356c821facc63746635f1821784b1ec870`, selected-v2 observation
completed and extraction settled after 5,262,005 ms with 225 of 264 partitions
succeeded, 39 failed, and none pending or running. The validated 293-attempt
timing floor contains 225 completions, 36 retry failures, and 32 terminal
refusals. Every terminal refusal is the same closed tuple:
`evidence_staging` / `extraction_domain` / `limit` / `facts`, limit 768,
maximum observed 769. T40.9's 49,152-fact per-domain aggregate distributes to
768 across 64 Kafka partitions, while the frozen corpus has two 65,536-input
Kafka-producer families occupying 32 full partitions. Peak RSS and allocated
data remained below ceilings; relationship publication did not start and
cleanup destroyed custody. This selects a governed reduce-or-correct review,
not a one-dimensional bound bump. The owning correction must first measure the
complete Kafka result's facts, rows, references, canonical bytes, and encoded
bytes, preserve historical v1/v2 validation, and pass independent review.
Take 19 remains unfrozen and Epic 41 remains blocked.

All-dimension and deadline-attribution prerequisite (2026-08-14): the retained
source-free Kafka replay now binds the frozen author to the real producer
extractor and production chunk encoder. Exact output is 131,072 facts, 262,144
rows, 131,072 references, and 101,386,432 canonical/encoded bytes; emitting
partitions carry 4,096 facts, 8,192 rows, 4,096 references, and
2,873,414–3,463,238 bytes. Because the unchanged allocator reserves across all
64 candidate partitions, the measured minimum equal reservations are 262,144
facts, 524,288 rows, 262,144 references, and 256 MiB canonical/encoded, with a
separate unchanged 64-MiB per-partition backstop. No limit changed. The prior
fit also leaves seven exhausted nonterminal partitions after subtracting its
32 closed refusals, with a 300,002-ms executor maximum at the five-minute
deadline. Timing v3 now retains only domain, closed deadline/canceled/other
class, and fixed duration buckets while preserving v1/v2. A fresh exact
committed-source diagnostic must seal that distribution before any output
contract or partition-shape correction; Take 19 remains unfrozen.

Exact deadline attribution (2026-08-14): the committed-source v3 diagnostic at
`9acf808cc8cc86e06184ae92e7cca578f450a05d` settled in 5,088,005 ms with
226 of 264 partitions succeeded and 38 failed. Its 290 timing reports reconcile
as 226 completions, 32 retryable failures, and the same 32 Kafka-producer fact
refusals. All retryable failures are deadlines: 12 proto-contract attempts and
20 thrift-contract attempts, with zero canceled, other, or unknown failures.
The six exhausted nonterminal partitions are therefore two proto and four
thrift ordinary IDL member partitions, not typed-input work. The next governed
step is a controlled, versioned diagnostic combining the measured Kafka output
contract with domain-specific smaller IDL member packing. The five-minute
deadline and shared 4,096-record pack remain unchanged; Take 19 is unfrozen.

The required production-binary rehearsal then found and closed three recovery
compatibility gaps before freeze: dependency-low validation now accepts the
canonical `unavailable_prerequisite` explicit-gap authority already accepted
by downstream consumers; the caller resolver adapter preserves partitioned
declaration authority schema, plan digest, and root digest; and the schema-full
caller publication table declares the v2 generation's optional
`upstream_digest` while retaining legacy rows. It also proved that lifecycle
may collect successful planning and ordinary extraction-job rows before a
later probe. A current immutable v2 observation publication now retains its
settled one-item planning proof, and the ceremony accepts a collected ordinary
job only after typed extraction authority is current. Non-current extraction
without an exact job projection remains pending and exact terminal jobs remain
fail-closed. The complete rehearsal passed semantic cold/restore and structural
A/B/A-return cold/restore, lifecycle, and authorized queries in 283.90 seconds
(91.50 seconds semantic; 180.45 seconds structural). These fixes add no corpus
scan, limit, concurrency, deadline, topology, or lifecycle-policy change. The
next action is independent review; freeze and execution remain separate.

Corrected semantic fit result (2026-08-14): do not freeze Take 19. At exact
commit `1dcf8daf179eff17bd9e74e8b8a0eb65d60bcbae`, observation completed and
extraction entered at 1,455,004 ms, but the extraction job failed after two
identical attempts before any of the expected 272 partitions materialized.
Source inspection closes the mechanism: result-plan v3 admits Kafka
reservations of 262,144 facts, 524,288 rows, and 262,144 references, while
the store-side partitioned-run and published-domain envelopes still reject
anything above the original v2 maxima of 49,152, 98,304, and 98,304. Peak RSS
and allocation remained below ceilings and cleanup destroyed private custody.
The store envelope must become contract/domain-versioned so only
`kafka-producer` v3 can use the measured maxima and historical v1/v2 controls
remain exact. Independent review and a new complete semantic fit are required;
Take 19 remains unfrozen and Epic 41 remains blocked.

Store-envelope contract binding (2026-08-14): the correction is implemented.
The store run and published-domain envelopes now dispatch on the exact
(`kafka-producer`, `phebs-extraction-domain-result-plan-v3`) binding: only
that pair admits the measured 262,144-fact, 524,288-row, and
262,144-reference aggregate, and every other domain or an absent/historical
schema keeps the v1/v2 maxima of 49,152, 98,304, and 98,304. Run creation
validates the binding before an identity exists and persists the plan schema;
the add-evidence second-line check dispatches on the persisted pair; a
published domain above the historical maxima must prove the binding from its
retained canonical plan bytes. Tests reject v3-sized controls without the
exact binding, reject one-over-v3 with it, cover persisted historical
controls, and stage a v3-bound run end to end. Historical reads pay no new
decode. This authorizes no freeze: independent review and one fresh complete
exact semantic fit remain mandatory; Take 19 and Epic 41 remain blocked.

Independent review and integration disposition (2026-08-14): the store
envelope correction is accepted. Review added regression guards that prove the
reconciler forwards the candidate plan schema and that the duplicated
store/candidate v3 schema, domain, and fact/row/reference maxima remain exact.
It also preserved T30.4's historical zero-override candidate identity after
the later 2,048-record Proto/Thrift policy addition. Focused, static, vet,
lint, race, exact-Node UI, documentation, glossary, and shell gates pass. The
full Go run retains only three failures reproduced unchanged at `main@8ca176e`:
the pre-existing T30.6 retention-budget drift and T32.3/T32.4 bundle-identity
drift. This baseline-qualified acceptance does not call repository CI wholly
green and does not absorb those failures into T40.R1. After integration, one
fresh complete committed-source semantic fit and independent evidence review
remain mandatory before a separate Take 19 freeze decision.

Post-envelope exact semantic result (2026-08-14): do not freeze Take 19. At
exact commit `7632918dff47a09f465c8b328c0555ccfc53e10d`, observation completed,
extraction entered at 1,440,504 ms, and the schedule settled unsuccessfully at
9,009,505 ms with 232 of 264 partitions succeeded and 32 failed; relationship
publication never began. The 394-report timing floor contains 232 completions,
162 deadline failures, zero terminal refusals, and zero reuse. Kafka producer
completed 40 attempts and failed 122; Proto and Thrift contract each failed 20.
Only five Kafka failures reached 300 seconds; Kafka's other 117 and all 40 IDL
failures ended below 60 seconds, aligning with the pinned client's 30-second
WebSocket request boundary. The retained production writer measurement covers
only a sequential 12,500-fact run, not Kafka v3's actual 131,072-fact run under
two-worker contention. The predicted 272-partition re-shard also did not occur:
the 2,048-record policy is focused-local only, while this exact route uses the
selected shared whole-repository physical generation and therefore retained
264 work items. Resource ceilings passed and cleanup destroyed custody.

Before another exact fit, add a source-free current-schema writer measurement
at the exact Kafka actual shape and production contention, retain bounded
append/accounting phase attribution, and reduce the chunk transaction's
serialized per-row run-counter work beneath the existing request timeout.
Separately version a whole-repository execution-partition subrange contract so
domain-specific packing reaches the shared-shard route without changing the
global candidate pack or repurposing focused-local identity. Both corrections
must preserve historical/recovery behavior, pass steady-state-cost and
independent review, and change no deadline, timeout, concurrency, topology, or
production bound. Take 19 remains unfrozen and Epic 41 remains blocked.

Focused iteration ladder (2026-08-14): use two opt-in diagnostics before
paying for another exact semantic fit. The whole-repository shape diagnostic
reduces the failure to five source-free Proto records in one shared member and
currently returns `[5]` instead of the intended `[2,2,1]` in 0.18 seconds. The
writer diagnostic uses the production disk-backed store, exact Kafka v3
reservations and actual 131,072-fact/262,144-row/131,072-reference population,
512 production-sized chunks, and two workers; it attributes append versus
accounting requests and stops at the first failure. Ordinary CI skips both.
These are edit-loop gates, not scale evidence: after they pass, historical,
recovery, maximum-shape, steady-state-cost, integrated exact-fit, and
independent-review gates remain required. No freeze or rerun is authorized.

Scoped writer result (2026-08-14): the exact production-store diagnostic is
retained at `spike/t4013/take19-kafka-writer-failure-point.json`. It stopped
after 2,169,379 ms with 145 append attempts, 143 completed chunks, 36,608
facts, 73,216 rows, and 36,608 references. The first closed failure was an
append deadline at 30,007 ms; the sibling append canceled. Forty append phases
were at or above 30 seconds and the maximum was 348,077 ms. All 143 accounting
reads completed in at most 3 ms with no failure. Peak SurrealDB RSS was
456,048,640 bytes and cleanup left no child. This selects a chunk-bounded exact
run-counter charge in place of the per-row shared-run updates, with atomic
rollback, replay/conflict, exact accounting, concurrency, maximum-shape,
publication, recovery, and historical checks. Re-run this focused gate after
the correction; do not raise a timeout, deadline, concurrency, or bound.

Writer correction result (2026-08-14): the append transaction now retains one
initial extraction-run serialization/fact/chunk update, computes exact bounded
row/reference deltas from only the submitted record IDs, performs three bulk
atom/association/assertion writes, applies one final guarded aggregate run
charge, and creates the chunk receipt under the same commit. A 256-fact chunk
uses exactly two shared run-row updates instead of as many as 513 and three
bulk writes instead of 768 per-record write statements. Overlap, replay,
conflict, one-over-limit rollback, maximum chunk, concurrency, publication,
recovery, and historical coverage pass, as does the full store suite. The
retained after-fix receipt
`spike/t4013/take19-kafka-writer-aggregate-fix.json` has SHA-256
`c6041cf5ff599cff9b01281286a3324845c578254b5851161fef888e3199963d`:
all 512 two-worker chunks completed in 41,625 ms with exact 131,072 facts,
262,144 rows, and 131,072 references. All appends were below one second (262-ms
maximum), all accounting reads were below one second (3-ms maximum), and every
failure class was zero. This clears the focused writer correction only. The
whole-repository execution-subrange correction, combined independent review,
and one integrated exact semantic fit remain required; Take 19 remains
unfrozen and Epic 41 remains blocked.

Execution-subrange correction result (2026-08-14): the shared candidate-v4
repository members remain physically packed at the unchanged 4,096-record
ceiling. Proto and Thrift local-domain identities now separately bind
`whole-repository-execution-subrange-v1` and a 2,048-record execution maximum.
Only the unitless shared-repository sparse route uses that authority: one
construction scan emits contiguous domain-relative subranges, and each
digest-bound partition records its exact policy, bound, start, and end.
Focused local publications retain their physical `MaxRecords` packing and do
not acquire subrange fields. Historical policy/partition JSON omits the new
optional fields byte-for-byte. The ordinary five-record proof produces exact
`[2,2,1]` coverage, rejects overlap/gap/wrong-bound/missing-authority forgeries,
survives result-plan binding, and accounts for one build read versus three
execution reads; a maximum 4,096-record member produces exactly two 2,048
subranges. No new candidate or source artifact is retained. Runtime
deliberately rereads the immutable shared member once per subrange, with that
full-byte cost reserved in the sparse descriptor and all existing aggregate
partition/index ceilings unchanged. This clears the focused shape correction
only. Combined independent review and one integrated exact semantic fit remain
required; Take 19 stays unfrozen and Epic 41 remains blocked.

Combined post-integration review (2026-08-14): the two corrections are
accepted for one committed-source exact semantic diagnostic. The review adds a
production `GitSparseSource` proof that five selected Git paths cross exact
`[2,2,1]` leases once while the one shared immutable member is read and charged
three times at execution. The exact fit record advances to v4 and refuses
readiness unless its final authoritative snapshot contains exactly 272
applicable and 272 settled extraction partitions with zero retry exhaustion;
the old converged 264 shape cannot pass. A fresh focused Kafka run completed
512/512 chunks and exact 131,072 facts, 262,144 rows, and 131,072 references in
41,375 ms; maximum append was 292 ms, maximum accounting was 3 ms, and every
failure class was zero. Package, store-T40.7, race, vet, lint, documentation,
glossary, historical, recovery, and maximum-shape gates remain green, subject
to the already named T30.6 and T32.3/T32.4 repository-baseline failures. The
new tests and diagnostic oracle add no production work. The next action is the
integrated exact fit, not a ceremony; its result still needs separate evidence
review before any freeze decision. Take 19 remains unfrozen and Epic 41 remains
blocked.

Integrated caller-generation refusal (2026-08-14): the committed-source exact
diagnostic at `1da4ada7` proves the merged corrections reached production.
Observation reached 262,144 records and extraction became current at exact
272/272 materialized and succeeded partitions, zero failures, and 9/9 current
domains. Relationship publication then stopped behind a durable terminal
caller admission: 40 successful queue jobs settled 192 pair outcomes, of which
38 succeeded and 154 were `terminal_generation_refusal` (58 `grpc-caller`, 96
`thrift-caller`). The 38 successful gRPC artifacts independently total 100,306
abstentions, 306 over the frozen 100,000 aggregate maximum; the largest
successful pair is 4,094 against the 4,096 per-pair maximum. The outcome schema
does not retain a typed dimension/observed/limit tuple for refused pairs, so it
cannot prove why the other 154 pairs refused. The exact harness also does not
surface terminal caller admission; the run was stopped after 3,573,161 ms
rather than wait irrecoverably for four hours. Receipt
`spike/t4013/take19-integrated-caller-refusal.json` has SHA-256
`c8134b908665f22c385a7483c99661800d19c6db82f3195434e6018729131f02`.
Before selecting a fix, retain a bounded typed caller refusal, make the
diagnostic terminal on refused admission, and add a scoped exact caller-lane
gate that classifies all refused pairs and remeasures every aggregate
dimension. Do not raise caller limits in isolation. Take 19 remains unfrozen
and Epic 41 remains blocked.

Typed caller attribution and scoped result (2026-08-14): the prerequisite is
implemented without moving a production bound. Caller pair execution,
artifact sealing/install, and generation admission now close exact typed
refusals. Terminal outcomes retain those receipts; terminal admissions expose
at most 32 canonical source-free summaries through the authorization-first
exact Caller Map HTTP/MCP progress object. More than 32 distinct measurements
collapse only the excess summary population into an explicit typed `unknown`;
exact outcome authority remains intact. Historical admitted rows remain valid,
while untyped historical terminal state is rejected and rebuilt through the
existing exact-generation recovery path. The T40.13 inspector now stops as
soon as complete failed caller progress proves the frozen aggregate-abstention
refusal and leaves every other terminal shape unclassified.

The retained scoped receipt
`spike/t4013/take19-caller-failure-point.json` has SHA-256
`f320e8f588a4e20e8f553373ae0891d52d2c280c7b13aa10327a1b62cd629304`.
Production `ExecutePair` replay at the first and last input of each frozen
source family plus every control and both protocols proves equal endpoint
receipts and that zero resolver descriptors produce one abstention per
candidate/protocol. Semantic exact output is 524,290 abstentions and
105,906,544 canonical/staging bytes, failing only the 100,000 aggregate count.
Structural exact output is 4,000,002 abstentions and 844,000,368
canonical/staging bytes, failing count by 3,900,002, canonical bytes by
307,129,456, and staging bytes by 298,740,848. All per-pair bounds fit. This
rules out both a count-only increase and smaller caller leaves. The next ticket
must design a versioned compact aggregate no-resolver/no-direct coverage
representation that preserves candidate coverage and explicit gaps, including
publication, recovery, lifecycle, and historical compatibility. Take 19
remains unfrozen and Epic 41 remains blocked.

Compact caller-coverage correction (2026-08-15): the selected versioned repair
is implemented without raising the result, abstention, canonical/staging,
source-byte, timeout, concurrency, or candidate-packing limits. New caller
generations bind `direct-syntax-compact-coverage-v2`. Only an exact
zero-descriptor protocol resolver may replace per-candidate abstentions with
one pair/member-bound coverage certificate. Its no-direct count and sorted
catalog-owned, domain-unselected, excluded-`go_test`, and resolver-generated
gap counts must partition the complete immutable member; it cannot mix with
result or abstention rows and performs no Git blob read. Descriptor-present
execution is unchanged. Historical V1 policy/receipt/manifest bytes omit the
new optional fields and remain readable; normal successor/reconciliation owns
the transition to current V2 authority. Coverage counts are preserved through
outcome, admission, complete publication, recovery, HTTP, and MCP.

The retained scoped receipt
`spike/t4013/take19-caller-compact-coverage.json` has SHA-256
`b0486178f8d4af6fd2be03e72ffa49c1075bbd4cb2fe0043d4c90a6a983e2799`.
Production `ExecutePair` over one maximal 4,096-record member for each direct
protocol, using the maximum legal member-name length and all four gap kinds,
emits one 955-byte coverage record, zero results/abstentions, and zero source or
out-of-leaf reads. The unchanged 16,384-pair ceiling conservatively bounds
generation content at 15,646,720 bytes. Exact logical coverage is
4,000,002 for structural-2m-v1 and 524,290 for semantic-262144-v1, both within
the V2 policy's 67,108,864 covered-candidate maximum. This clears the scoped
caller gate only. Full validation and independent review are required before a
separately authorized integrated exact diagnostic; Take 19 remains unfrozen
and Epic 41 remains blocked.

The subsequently authorized `t40r1-neutral-19` run used exact source commit
`6f02dc2ae6c15b400d6d9f358f558e358d3025ea` and reviewed plan
`sha256:e392bacb787c27f5032874831fab35426cd6412f04aab115e695a33f90ff281e`.
Its outer process sequence advanced from structural server execution to
semantic cold convergence and stopped on a terminal caller generation before publication. The executor
recorded bounded stage `caller_generation` and outcome
`caller_generation_terminal`, but the v14 source-free validator had not
enumerated those new values or their terminal-transition coherence. It rejected
the observation, sealed no receipt, and teardown destroyed all custody and
private material. The run therefore establishes no structural completion,
does not classify the caller cause, and does not validate the compact
correction. The harness-only closure admits the two
already-produced caller terminal outcomes for v14, keeps earlier contracts
closed, and regression-tests complete stopped-observation sealing. A fresh
reviewed plan and separately authorized rerun are required; Take 19 remains
unfrozen and Epic 41 remains blocked.

The next authorized `t40r1-neutral-20` run used exact source commit
`9a9052e74a18abf0bb47f54b08f208a6d4769742` and reviewed plan
`sha256:2813a57a862ed0f498a5522eeb25d4ea616c8e0456cbb4cf13c8da6889e24dad`.
Structural cold convergence completed, and semantic execution stopped when
exact Caller Map authority reported a terminal caller generation before
publication. The v14 observation now sealed correctly as an unclassified
`pipeline/caller_generation_terminal` stop with successful teardown, but
receipt construction rejected it because the stopped-receipt switch omitted
both caller classifier codes. No receipt, signed inventory, or transfer bundle
was sealed; custody and private material were destroyed. The harness closure
adds the two v14 receipt identities and drives terminal probing through
classification, observation validation, receipt construction, and receipt
decode as one parity regression. Take 20 cannot be retroactively sealed because
the verifier requires the clean exact frozen source commit. A fresh reviewed
plan and separately authorized execution remain required; Take 19 is unfrozen
and Epic 41 remains blocked.

The focused caller-terminal witness now clears the caller-owned boundary
without repeating the ceremony. At the retained semantic cardinality of
262,145 candidates, 96 candidate leaves, two protocols, and 192 pairs, the
zero-descriptor V2 path completed 194 production worker turns against a
supervised disk-backed SurrealDB store. All 192 outcomes succeeded; admission
and complete publication were current with 192 coverage records, 524,290
covered candidates, zero results/abstentions/refusals, and 106,856 canonical
bytes, and the shared product reader reopened the generation as `current`.
The source-free witness is `spike/t4013/caller-terminal-witness.json`
(`sha256:b6d0b265e6b3e2a80698fe54ea2f8a0fab43a0b3ccca871777d674349cfe7be3`).
It rules out the compact executor, artifact/store admission, publication, and
reader only after empty resolver authority is supplied. It does not recover
Take 20's destroyed resolver population or distinguish its terminal admission
from a deterministic authority/publication rejection. Before another full
ceremony, classify that actual product-read origin and retain the closed
progress/refusal scalars. No production fix, rerun, freeze, scale/SLO, release,
or Epic 41 progression follows from this scoped pass.

The next one-boundary-upstream witness replaces that supplied resolver with a
production-built, sealed, installed, store-published, and strict-opened
resolver catalog. It publishes one real protobuf declaration evidence run,
materializes the canonical three resolver members over the same 262,145-record
candidate projection, and proves both protocol views contain zero descriptors.
The unchanged 192-pair worker run again admits and publishes current authority;
the shared exact reader and authorized Caller Map then return a current,
declaration-backed, exact zero-row page whose pair, candidate, and coverage
scalars equal the durable publication. The retained source-free record is
`spike/t4013/caller-terminal-upstream-witness.json`
(`sha256:8409977bd830fe69880b870719e2b1a4cbb6c9648555c4d1b88a668ce2cec5db`).
This clears resolver materialization and product projection only for the
closed zero-descriptor state. It deliberately still begins before production
partitioned observation/extraction authority and cannot reconstruct Take 20's
destroyed resolver population or failed read. Move that exact authority/origin
boundary next; do not infer a production fix, rerun, freeze, scale/SLO,
release, Epic closure, or Epic 41 progression.

The partitioned-authority witness moves that boundary through one real current
observation-v2 generation and the exact current two-partition empty result
roots for both required caller domains. Its first red run found a product
parity defect rather than a caller-execution failure: the worker bound the
canonical aggregate upstream authority, all 192 pairs settled, and publication
completed, but the publication summary retained only the upstream digest.
Product reconstruction consequently built a historical no-upstream semantic
generation and exact Caller Map classified the valid filesystem generation as
failed. The correction shares worker/reader authority derivation, retains the
canonical upstream payload once on the complete publication and summary while
keeping only its digest in outcome/admission identities, reconstructs and
validates the exact semantic generation, and repeats compact upstream checks at
acquire and result fences. A transitional digest-only pointer is admitted only
to startup inventory so existing queue-before-clear replacement can retire it.

The retained source-free record is
`spike/t4013/caller-terminal-partitioned-authority-witness.json`
(`sha256:e2f222bb799e0d10fdbec223e78c75840f64bf41877b90dbe385d1a43fc9790e`).
It records one observed record, two candidate control records, two current
partitions for each of gRPC and Thrift, exact upstream digest binding, and
current zero-row Caller Map parity over the retained 262,145-record, 96-leaf,
192-pair semantic replay. The physical control population is deliberately not
the semantic population, so this does not prove exact candidate-member bytes
or distribution, Take 20's destroyed resolver descriptors/failed read, a scale
pass, or ceremony readiness. Publication writes one at-most-64-KiB canonical
control instead of copying it to as many as 16,384 outcomes. An ordinary open
performs compact observation/domain authority reads before selection and after
lease acquisition; reopen and result fences perform one, and paired reads
deduplicate identical authority. Those reads are pointer/root/pointer plus one
small control per required caller domain, with no corpus/source/shard scan,
new child, lock, cache, or unbounded hash. Startup remains bounded by 1,024
summaries (64 MiB worst-case added control bytes). Move next through the
production physical candidate-plan/provider membership boundary; do not start
a new ceremony. No rerun, freeze, scale/SLO, release, Epic closure, or Epic 41
progression follows.

The physical-provider witness now replaces the synthetic caller plan with one
real Git-backed candidate-v4 publication installed under the production
candidate root. Its first attempt correctly failed because the diagnostic had
not created the empty output directory required by `candidate.Build`; creating
that fixture-owned directory was sufficient, and no production change was
needed. `candidatejob.Provider` then bound the exact store pointer, manifest
digest, and control revision and `candidate.OpenCallerPlanContext` exposed one
immutable caller leaf containing one physical record. Both gRPC and Thrift
replayed that exact leaf. The worker completed four turns, including the final
no-op, with three plan opens; both pairs succeeded and admission/publication/
reader/authorized Caller Map stayed exact and current with one candidate, two
coverage records, and 1,338 canonical/staging bytes.

The retained source-free record is
`spike/t4013/caller-terminal-physical-provider-witness.json`
(`sha256:e1b73fcc2d783d4d0c90158f7562bb672f1c5726c962b84d2b0c22a77dbd6bd0`).
It binds the leaf name, ordinal, prefix, record/byte counts, content digest,
per-domain replays, and store/product parity. This clears the production
physical provider/member seam only for the small zero-descriptor control. It
does not prove a physical 96-leaf/262,145-record distribution,
descriptor-present resolver or Git-blob execution, Take 20's destroyed state,
a scale pass, or ceremony readiness. Production cost is unchanged: each
active job turn reads/parses the bounded manifest and validates its leaf
envelopes; each unsettled pair reads and hashes exactly one selected bounded
leaf under the existing repository work lock. Admission repeats the plan open
without a leaf replay, and an empty-queue turn opens neither. Retry repeats the
same bounded work. No query/request, sync/startup, publication/lifecycle,
cache, child, new lock, source/shard scan, or ceiling changes. If diagnosis
continues, use a provider-only retained 96-leaf physical distribution or move
to descriptor-present Git-blob execution; do not start another ceremony. No
rerun, freeze, scale/SLO, release, Epic closure, or Epic 41 progression follows.

The provider-only 96-leaf physical-distribution diagnostic now authors 261,769
deterministic regular Git paths over one shared 32-byte blob and runs the exact
production candidate-v4 build, store-pointer publication, `candidatejob.Provider`
open, and caller-plan replay. It retains exactly 96 immutable leaves: 32 at six
prefix bits and 64 at seven, with 1,953–4,096 records per leaf. Both gRPC and
Thrift replay all leaves and all records with equal digests; manifest digest,
control revision, and provider leaf envelopes match exactly. The source-free
receipt is `spike/t4013/caller-provider-96leaf-physical-distribution.json`
(`sha256:48e1b1928cb167611577017f155c4b6ced5d858787e3fc58441c877db024cdc4`).
It records 192 leaf replays, 523,538 visits, 187,950,142 member-read bytes, and
117,543,780 peak spool bytes. The retained rerun measured 47.725s for build,
0.680ms for provider open, and 1.879s for both replays; those values are
observations, not ceilings or an SLO. The earlier 262,145-record synthetic
shape is not a physical-distribution promise: this exact path family produces
98 leaves at that cardinality, while the retained 261,769-path control produces
exactly 96.
The opt-in diagnostic changes no production path or cost and invokes no
resolver materialization, descriptor-present/Git-blob execution, caller
outcome/publication, product request, startup/sync/retry/lifecycle work, or
ceremony. This clears only the provider multi-leaf distribution seam. Move
next to the descriptor-present resolver/Git-blob pair-execution boundary; do
not start another ceremony. No rerun, scale/SLO, release, Epic closure, or
Epic 41 progression follows.

The descriptor-present physical-pair diagnostic now carries one neutral gRPC
call across the next boundary. A real seven-file Git commit produces six
caller records in four candidate-v4 leaves. Production resolver
materialization reads four named immutable inputs totaling 713 bytes and
publishes a current two-member catalog with one exact descriptor binding
`example.invalid/root/gen/grpc.OrdersClient.Get` to `/orders.Orders/Get`, its
declaration lineage, and the generated object. The production provider selects
the one-record `10` leaf, and `ExecutePair` uses its default `gitobj.ReadBlob`
path to read only the 121-byte consumer source, emit one `CALLS_OPERATION`,
seal/install the artifact, and reread that exact result. Abstentions, compact
coverage, and out-of-leaf reads are zero. The source-free receipt is
`spike/t4013/descriptor-present-git-blob-pair.json`
(`sha256:1952ebce6ed4b0b3dcafa35962b1375a65565b625525ac70551c4f8555d7288e`).
The retained rerun measured 66.0ms candidate build, 66.8ms resolver build,
0.426ms resolver open, and 18.0ms pair execute/seal; these are observations,
not ceilings or an SLO. The opt-in diagnostic changes no production path. It
does not drive the caller worker queue, outcome/admission/complete publication,
product reads, startup/recovery/lifecycle, Thrift, ambiguity, Take 20's
destroyed state, or the 96-leaf scale shape. Move next by composing this
descriptor-present pair through production worker outcome, admission,
publication, and exact product parity; do not start another ceremony. No
rerun, scale/SLO, release, Epic closure, or Epic 41 progression follows.

That composed descriptor-present boundary now passes. The opt-in diagnostic
publishes current observation-v2 authority and the exact required gRPC domain
root, retaining its zero sparse partitions as the explicit usable
`unavailable_prerequisite` gap. It clones the exact neutral commit into the
managed mirror and lets the production worker settle all four physical pairs:
four successful outcomes/artifacts, admitted and current complete publication,
one resolved caller result, five abstentions, and no refusal or compact
coverage. The shared reader rederives the same canonical upstream payload and
authorized exact Caller Map returns one current exact row whose classification,
operation, lineage, path, object, blob, tier, and code role all match. The
source-free receipt is
`spike/t4013/descriptor-present-product-parity.json`
(`sha256:a11683cf3a3ab77800de66fec23970182e34df6edd54763c2c683b1588b67ede`).
The declaration fixture uses the catalog's canonical no-leading-slash object
while retaining the slash-prefixed resolver/caller transport operation; the
earlier direct receipt remains byte-identical. This changes no production path
or cost and does not compose descriptor-present execution with the retained
96-leaf physical distribution, reproduce Take 20, exercise Thrift/ambiguity,
pass scale, or authorize ceremony/release/Epic closure/Epic 41. If diagnosis
continues, the next scoped boundary is a descriptor-present physical
multi-leaf worker/product run, not another ceremony.

That exact physical multi-leaf run now reproduces and classifies the terminal
boundary. The neutral corpus has 261,770 regular files over 72 Git blobs and
production candidate-v4 yields 261,769 caller records in exactly 96 leaves,
with the same 32 six-bit/64 seven-bit distribution as the retained provider
control. One descriptor-backed consumer in leaf 0 resolves exactly. The worker
then performs the descriptor-present direct path and reaches 100,245
abstentions against the frozen 100,000 aggregate after 38 successful artifacts;
the remaining 58 outcomes carry one exact `caller_generation_admission` /
`caller` / `limit` / `caller_generation_abstentions` refusal. Terminal admission
publishes no complete generation. The shared reader and authorized exact
Caller Map return the same failed generation, unavailable totals, complete
96/38/58 progress, and refusal tuple. The source-free receipt is
`spike/t4013/descriptor-present-96leaf-product.json`
(`sha256:17512e7d9c8f46c312051bcfaf27a57d08a10df8662e7f70755475f1d596736d`).

Do not raise the abstention limit or start another ceremony. The selected
reduce-first correction is to compact a descriptor-present pair only after its
exact scan emits no caller or unresolved fact, retaining one count-bearing
coverage record; any pair with a resolved or unresolved fact remains fully
materialized. Preserve exact candidate/gap accounting and historical bytes.
Before another scoped run, require schema/backward-compatibility, mixed
result/unresolved, maximum-shape, terminal recovery, complete publication,
reader/Caller Map parity, and steady-state-cost coverage. This diagnostic
changes no production behavior, is not a scale pass or Take 20 reconstruction,
and authorizes no ceremony, release, Epic closure, or Epic 41 progression.

The reduce-first correction and scoped rerun now pass. New caller generations
bind `direct-syntax-zero-fact-coverage-v3` and
`phebs-direct-caller-leaf-policy-v3`; V1/V2 identities and bytes remain
readable and exact. After the unchanged descriptor-present source scan, a pair
with no resolved or unresolved caller fact replaces only its input-abstention
rows with one `phebs-caller-leaf-coverage-v2` record. The record binds the
immutable pair/member, partitions all candidates across the closed V3 count
vocabulary, and retains exact source-read count/bytes. A result or unresolved
fact prevents compaction and retains the full artifact. Existing startup
reconciliation queues a V3 replacement before clearing a V2 current pointer.

Focused schema, historical reconstruction, all-gap, mixed result/unresolved,
4,096-record maximum-shape, store receipt, recovery/replacement, complete
publication, and exact product-parity tests pass. The physical rerun kept the
same 261,770-file/261,769-candidate/96-leaf distribution and settled all 96
pairs successfully in 98 turns. Admission and publication contain one result,
4,055 abstentions, and 95 coverage records covering 257,713 candidates;
receipts retain 261,764 Git reads totaling 24,082,313 bytes and zero
out-of-leaf reads. Exact Caller Map returns the one current resolved row and
the same pair/result/abstention/coverage scalars. The retained V2 receipt is
`spike/t4013/descriptor-present-96leaf-product-v2.json`
(`sha256:43e3a82e1c3897bd62f14150a1c0d9352d396030cc4f0bd1a1959f1f282b029b`);
the failed V1 receipt remains byte-exact.

Steady-state cost remains source-bound: a descriptor-present pair performs the
same bounded Git reads, hashes, parses, direct scan, repository lock, and
temporary staging writes. A zero-fact completion adds constant counters and
one truncate/seek/coverage write; result/unresolved pairs add a constant final
check. No bound, retry cadence, query, sync tick, startup scan cardinality,
publication/lifecycle ceiling, cache, child, or topology changes. This closes
the scoped caller refusal only. It is not T40.13 or Epic 40 closure, a scale/SLO
pass, Take 20 reconstruction, ceremony authorization, release, or Epic 41
progression. The next action is independent evidence/code review before any
separate fresh-ceremony freeze decision.

Independent review found and the follow-up correction closes two receipt
validation holes before any ceremony decision. Current V3 compact receipts now
carry their exact `no_resolver_descriptors` or `zero_caller_facts` reason.
Receipt-only and durable-store validation reject a no-resolver receipt that
claims any source read or byte. Coverage-v2 zero-fact records now embed the
exact source-byte total, and artifact verification requires byte-for-byte
agreement with the receipt in addition to the existing derived read-count
identity. Historical V1/V2 JSON remains unchanged through omitted fields.

The same review correction pins the retained partitioned-authority and
physical-provider opt-in generators to historical V2, makes an unrecognized
future input-only abstention reason retain the materialized artifact rather
than fail compaction, derives the selected-candidate count from stage-owned
accounting, centralizes ordered gap construction, and documents an explicit
70-minute Go-test timeout around the 65-minute diagnostic parent. It changes no
production timeout, bound, topology, query, source read, or authority claim.
The scoped refusal remains closed, but ceremony, scale/SLO, release, T40.13,
Epic 40, and Epic 41 remain unapproved.

The corrected exact 96-leaf run then passed in 3,569.10 seconds with unchanged
96/96 success, publication/product parity, result/abstention/coverage/source
scalars, and retained source-free receipt bytes. Caller artifact disk measured
1,003,478 bytes, 6,175 bytes above the prior corrected-oracle run; that bounded
constant-record increase is an observation, not a ceiling or SLO.

Independent re-review authorizes a fresh freeze after one custody fence. The
pre-correction V3 commit `ab5f28f` was present in pushed `main` through
`c911586` for the recorded 2026-08-15 18:12:14–20:12:06 -0700 interval, so the
earlier “unpushed” statement is superseded. Project custody records no
deployment, startup, ceremony, or durable execution from that interval. Any
other data-directory custody that cannot prove the same absence is ineligible
for corrected V3 pending a separately reviewed purge/rebuild; a one-line
writer-version bump is not such a purge because the existing guard refuses a
nonempty version mismatch. The fresh ceremony's creation-exclusive isolated
directory satisfies the fence.

After this ledger change is fast-forwarded to `main`, Ben authorizes the
`t40r1-neutral-21` freeze step only. It must bind the exact resulting `main`
commit and the corrected 96/96 receipt
`spike/t4013/descriptor-present-96leaf-product-v2.json`
(`sha256:43e3a82e1c3897bd62f14150a1c0d9352d396030cc4f0bd1a1959f1f282b029b`).
Execution remains separately approval-gated after plan-digest review. A
pilot/design-partner requirement for the 96-leaf scale shape or an explicit
charter decision changing the 100,000 caller-abstention ceiling invalidates
the freeze and requires re-review. This is no scale/SLO pass, release,
T40.13/Epic 40 closure, or Epic 41 progression.

`t40r1-neutral-21` subsequently executed and stopped honestly at the exact
four-hour semantic deadline. Its signed source-free package is
`sha256:93b54e57c071e2341a3f50f6a62a859b9af9d41c76010ae9f55dfd28eb4806be`.
The final schedule held 266 pending, one running, five succeeded, and zero
failed of 272 materialized partitions after all six observed handlers had
completed; the custody-bound log recorded concurrent SurrealDB expansion and
completion conflicts. The single repository token remained consumed and stale
reaping never restored progress. The correction gives expansion and every
lease transition the existing 64-attempt explicit-conflict retry bound,
reconciles ambiguous completion, separates reaping from expansion, and gives
scheduler store calls a five-second context. Fresh V14 freezes now stamp their
actual UTC date while historical bytes remain unchanged. The retained physical
schedule diagnostic `spike/t4013/generation-schedule-recovery.json`
(`sha256:def6cd63f0bc7a7b97af753922812da90110787db4da0c5deccade66adab5f7c`)
settled the exact 272/272 one-token shape in 1,887 ms with no leaked token or
surfaced store error and 21 recovered expansion conflicts. Independent review
remains required before another ceremony. The fresh exact semantic
production-path diagnostic at corrected source
`abbd218712015cc9802b5d9b1c1e8168641f5732` completed all 272 extraction
schedules and 272 partition timings with zero `completion_failed` events and
one internally recovered schedule-store conflict. It was intentionally
interrupted after that scoped boundary while downstream Caller publication
inspection continued, so it is not a full-fit or ceremony pass. The retained
source-free receipt is
`spike/t4013/generation-schedule-production-replay.json`
(`sha256:b562f032d9737b246e456e1d0682e002ff2245cdd5f6e4c230a72971912885d0`);
interrupted custody was destroyed. No scale/SLO, release, T40.13/Epic 40
closure, or Epic 41 progression follows.

`t40r1-neutral-23` subsequently stopped honestly at the semantic four-hour
deadline after moving the boundary past the repaired scheduler: all 272
extraction partitions completed and scheduler-settled with zero failed or
terminal-refused executions. The last 3.5 hours were one unchanged
caller-generation HTTP 404. Exact-source review pins that stop to the ceremony
observer added at `2fb09a0`: it queried `t401-neutral` `/neutral.Service/Ping`,
but the frozen semantic protobuf templates declare messages only. Once a
caller generation is current, exact Caller Map correctly reaches endpoint
declaration lookup and returns 404; missing/stale/failed generations instead
return typed gap pages first. A new authorization-first, 32-KiB
`/api/caller-generation-progress` read exposes the exact generation and bounded
partition state plus digest/count scope authority without endpoint evidence or
selected paths, shares the exact reader and all
publication/authorization fences, and replaces only the ceremony probe. Tests
pin current, missing, stale, failed, and refused classification; caller-digest
revalidation; maximum response shape; current progress with declarations absent;
and the unchanged Caller Map 404. The signed neutral-23 receipt and package remain immutable
`unclassified` evidence. A new freeze/execution still requires separate review
and authorization; no scale/SLO, release, T40.13/Epic 40 closure, or Epic 41
progression follows.

`t40r1-neutral-24` subsequently completed and scheduler-settled all 272
semantic extraction partitions, reached exact current caller authority, and
then retained a missing relationship root through the four-hour deadline. The
source-free package remains immutable `unclassified` evidence. Exact
production-model replay pins two Kafka projection fences hidden behind that
absence: each frozen logical bucket contains 65,536 postings against the old
50,000-member policy, and their exact 147,324,928-byte resident charge exceeds
the old 128-MiB Kafka fence. The correction admits exactly 65,536 postings for
the current policy while continuing to validate the historical 50,000 policy,
raises the relationship Kafka operational charge to 160 MiB inside the
unchanged one-GiB worker class, and terminalizes deterministic Kafka bounds.
The partitioned relationship schedule now binds the exact builder-policy
digest: a duplicate same-policy reconcile retains its closed refusal without
another attempt, while a policy or bound change produces a recovery target;
historical v2 bindings remain valid.
The scoped end-to-end diagnostic published 131,072 Kafka postings and 131,072
relationship projections with complete authority. While a relationship root
is absent, the ceremony now reads a bounded source-free current schedule:
missing/active remains pending and settled failure terminates immediately.
Focused/race/docs gates and independent review remain mandatory before a later
integration/freeze request; no scale/SLO, release, T40.13/Epic 40 closure, or
Epic 41 progression follows.

### T40.13 pre-freeze remediation sequence *(planned 2026-08-22 · prerequisite stack complete)*

The exact post-commit review of `03422ddd07a0b4e6aa0ce26c5b375c682ab565d3`
found ceremony-crash, custody-loss, orphan-process, and returned-evidence trust
failures after the host module, process, and capacity refusals were cleared.
T40.13 remains the final neutral convergence gate. The following prerequisites
are PR-sized, stacked in order, and do not authorize a freeze or use a ceremony
as their test.

**T40.13j · Overflow-safe ceremony arithmetic** *(medium · needs T40.13i)* —
make every phase, total-wall, byte, count, and resource aggregation refuse
before signed integer overflow can create a false pass. AC: shared checked-add
and checked-multiply paths cover observation construction and receipt
validation; `math.MaxInt64` boundary tables fail closed before comparison;
ordinary values and historical receipts remain exact; no arbitrary saturation
or wider unbounded representation enters a wire contract; the cost is constant
per already-visited scalar.

T40.13j is complete. Shared checked arithmetic now guards every T40.13 phase,
wall, byte, count, resource, timing, construction, and receipt aggregation;
MaxInt64 boundary tests fail closed before comparison, while ordinary values and
historical receipts remain exact. No wire widening or saturation was added.

**T40.13k · Complete executor-admission accounting** *(medium · needs
T40.13j)* — make V25's completeness claim cover the work performed before phase
one. AC: checkout/toolchain verification, filesystem probes, and other
admission children contribute wall, child, and peak-RSS facts to an explicit
phase-zero meter; a failed or unavailable meter makes
`phase_accounting_complete` false and refuses completed evidence; one tiny
injected admission child tree is visible in the receipt; probe/meter failure
retains custody and cannot be classified as a complete
measurement; the meter remains bounded and starts no extra corpus read or
production child.

T40.13k is complete. V25 Execute now meters admission before phase one with
bounded process-tree wall, peak-RSS, and child-lifetime facts, merges those facts
into the preflight phase, and refuses completed evidence when the meter is
unavailable or fails. T40.13l is now the only next scale ticket inside the
still-open T40.13 gate and closes low-risk cost-first refusal ordering.

T40.13l is complete. Immutable duplicate-ID, signer/key, custody-marker,
missing-run, and dirty-checkout refusals now precede bounded verifier,
toolchain, cache, and host gates where the fact is already available; required
semantic verification still runs after cheap admission succeeds. The shell
regression records call order and proves expensive commands are not invoked on
each refusal.

**T40.13l · Cost-first operator gates** *(low operational risk · needs
T40.13k)* — reject immutable bad inputs before running the bounded but costly
package/toolchain gate. AC: duplicate freeze IDs, missing runs, surviving seal
custody, malformed archive inventories, missing trust anchors, and mismatched
keys, wrong confirmation or plan digest, occupied run locks or ports,
insufficient disk, and a dirty checkout refuse before Go tests or full tool
hashing wherever that fact is already immutable and available; seal/verify
still run every required semantic gate after cheap admission succeeds;
repeated host hashing
is removed only under T40.13e's fixed immutable-snapshot bound and never changes
identity correctness; shell tests record call order and prove
expensive commands were not invoked on each refusal; no timeout, bound,
authentication, cleanup, or evidence predicate is weakened.

After T40.13l, the original T40.13 gate still requires one clean exact commit,
all bounded package and real-binary rehearsals, independent code and plan
review with complete coverage or a reviewed manual equivalent for all seven
OCR timeouts, a passing bounded full `internal/store` package gate rather than
only its isolated formerly timed-out test, a fresh unconsumed ceremony
identifier, and Ben's separate freeze and execution authorizations. A green
prerequisite stack is readiness evidence, not Epic 40 closure.

The complete review of clean commit
`97576e3319b565ab3af3fb407b7a361e552ee974` returned `FIX-FIRST`: metric
aggregation could still replace a simultaneous operation/measurement error,
and T40.13e's executed-byte wording exceeded its pathname checks and omitted
the driver's authority utilities from the committed inventory. The stacked
remediation joins both errors at every shared boundary and explicitly selects
a dedicated, single-operator host envelope. The fixed attestation prohibits
package, OS, tool, and other same-UID mutation from preflight through packaging;
path/content/tree checks remain defense in depth. The shell driver is the only
supported V25 ceremony admission path; direct `cmd/t4013-*` binaries remain
low-level harness/library interfaces. The Bash interpreter/builtins plus
`awk`, `basename`, `chmod`, `cmp`, `cp`, `date`, `df`, `dirname`, `du`, `env`,
`find`, `grep`, `lsof`, `mkdir`, `mktemp`, `pgrep`, `ps`, `readlink`, `rm`,
`rmdir`, `sed`, `shasum`, `sort`, `ssh-keygen`, `sysctl`, `tar`, `uname`,
`uniq`, and `wc` are the enumerated trusted shell TCB. This supersedes the
atomic executed-tool claim
without weakening a production trust boundary: an adversarial same-UID model
would require a new snapshot/fd-execution ticket. Re-review and every gate
above remain required; no ceremony identifier is consumed by this correction.
Independent final re-review accepted exact clean commit
`7696f047e8e936d96887af736c707991f494a94b` with both prior findings closed and
no critical, high, or medium finding. That acceptance closes only the
code/review prerequisite; the complete bounded gates, fresh unconsumed
identifier, and Ben's separate freeze and execution authorizations remain.
The subsequent exact-commit production-path rehearsal refused broad
`go mod download all` hydration at 2.3 GiB under the unchanged 2-GiB cache
bound. V25 now hydrates only the exact admitted tool dependency closures; the
four-tool closure measured 1.5 GiB and built offline without cache growth.
This correction reopens the exact-commit regression and independent-review
prerequisites without authorizing identifier consumption or ceremony.
The next rehearsal attempt also exposed that its closed harness bound only
Git-core and therefore could not supply exact SurrealDB authority. It now
reuses the complete V25 host observation and plan-binding boundary before
controls or builds; the corrected rehearsal and independent review remain open.
Real convergence then reproduced Darwin's child-exit race between `ps` and
`kern.proc.pid`. T40.13a now permits at most two fresh complete retries only for a typed
disappeared child, with all attempts sharing the existing two-second deadline;
every other failure remains sticky and no failed attempt contributes evidence.
The Darwin sampler correction closes those three rehearsal blockers without
widening retry or weakening identity equality. Bounded native parent traversal
now accepts PID, PPID, start identity, command class, and RSS only from one
`PROC_PIDTASKALLINFO` record; a child gone before that record is absent from the
accepted sampled-lifetime observation. A root-exit marker crossing discards the
whole attempt and permits exactly one fresh handoff observation under the same
two-second deadline; an already-observed exit requires no descendants. The
corrected real-binary rehearsal, bounded package/race suites, complete
`internal/store` gate, and exact-clean-commit independent review now pass on
`afa297966f7129bf7930c0834e8808c3992f35c5` with no critical, high, or medium
finding. Integration, dedicated-host preflight, and separate freeze and
execution authorizations remain mandatory. No ceremony identifier is selected
or consumed.

Neutral-35 is consumed as a verified signed V25 measurement stop. At exact
source `158dc6c9d87c26e4e7fc6a2f2ce38cc900da2119` and plan
`sha256:d9c1a646a7722c0d6496d1866c3a1450cbcbdfbf5c17c340d324173fe2ea543c`,
the structural observer reached `complete` at 3,660,371 ms and extraction
reported 1,956 of 1,956 partitions completed with zero failures. Process
accounting then retained 12 failed samples sharing one sticky same-identity
class-change cause. Because the exact transition direction was not present in
source-free V25 evidence, cold is correctly failed as
`failed_phase_measurement_unavailable`, later phases are `not_run`, and the
decision is unsubstantiated `unclassified`; this is neither a pipeline failure
nor a scale pass. Teardown destroyed derived and scratch custody. The verified
source-free package is
`sha256:a1b1114e74010bedaac65cf037b5135895dcb6b1cf0bcb4753888b9aeaadf7c1`.

T40.13m is complete on exact clean code commit
`97772bb69fba77feb06fa79317b401d1e0815575`. The complete package/race,
real-launcher, readiness, full `internal/store`, repository, and deterministic
exec-transition gates pass. Fresh independent review reports critical 0, high
0, medium 0, and no actionable low finding after its rejected findings were
corrected. Integration and exact-main dedicated-host preflight remain next;
no fresh ceremony identifier is selected, consumed, or frozen.

**T40.13m · Bounded executable-image epoch accounting** *(high gate readiness ·
complete; integration requested)* — replace the false assumption that a
kernel process lifetime has one immutable executable class. AC: across two
complete coherent snapshots, the exact same PID, kernel start token, and parent
with a new normalized class consumes one new sampled executable-image epoch,
increments the destination Git/index/other count once, updates the active
epoch, and remains inside the existing 8,192 cumulative ceiling; a same-class
repeat is free, while changed identity or absence/reappearance remains a new
sampled lifetime. Fresh plan/observation/receipt contracts advance to V26 and
retain six optional source-free old-class-to-new-class transition counters;
V1–V25 bytes validate only with those counters absent/zero. Receipt creation,
resume, and rebuild require the exact observation schema selected by the plan;
each direction must be funded by both its source and destination class epochs,
not only by the aggregate child count. Same-snapshot
candidate/kernel class disagreement, parent/start drift, malformed/unreadable
identity, duplicate/cycle, root handoff, 128 descendants, 250-ms cadence,
shared two-second deadline, and no-partial-commit rules remain fail-closed.
Tests must include a deterministic Darwin same-PID shell-to-real-Git exec,
synthetic transition/repeat/reversal/identity/bound cases, within-snapshot
class mismatch, all six direction/source/destination cases, metric
merge/overflow, V25/V26 receipt validation, and the real V26 shell/receipt
dispatch. The
correction adds no probe, retry, deadline, production work, or bound increase.
Full bounded package/race, real-launcher, readiness rehearsal, repository/store,
docs, glossary, shell, and independent-review gates remain required before
integration. `t40r1-neutral-36` is an unselected candidate only; freeze and
execution remain separately authorized.

Neutral-36 measured outcome (2026-08-24): `t40r1-neutral-36` is now consumed as
an immutable signed V26 measurement stop. Its source-free evidence binds exact
source `acc5a23f046229c580b972bcbb0107f2f7062882`, plan
`sha256:e2403ee87df84383e47b5b78a1f7fc1085425da3ec1b5af5f3214fa4e03ca9e7`,
observation
`sha256:141750ff0ae7da9af7e006bfb59cc260ff973abe02509e2e269474dea7c8d22d`,
receipt
`sha256:9d9ec605ad90ccd1010a920cb86c405656851349d85ccb0ac2243b18606e6ee6`,
and package
`sha256:e5ec0c04338b17d91064c160f34a1a78b6ba174773107bfd592d2bf80f0e0677`.
Preflight, cold, warm no-op, delta B, and return A succeeded. Interruption
selected an attempt-zero `extraction-partitions` chunk, returned source to A,
and failed after 6,059,839 ms at `restart_start` with the generic
`failed_phase_measurement_unavailable` oracle code and an unsubstantiated
`unclassified` decision. The sealed contract does not retain the failing data
gauge or internal error, so it cannot establish a process/pipeline cause or a
scale pass. Recovery verification and the remaining mechanics phases did not
run. Teardown completed in 187,542 ms with neither derived data nor scratch
source retained. The identifier and frozen plan may not be reused, and this
outcome authorizes no private rerun.

**T40.13n · Coherent restart accounting and typed data-gauge evidence** *(high
gate readiness · complete; requests integration only; needs T40.13m and neutral-36)* —
make a stopped V27 restart retain enough source-free evidence to distinguish a
bounded data-gauge deadline without weakening measurement completeness. AC: a
successfully finished first-server meter may return one private
`dataMeasurementBoundary` containing only its raw end allocated bytes and
canonical workspace; the immediately following restart consumes it once and
only for the same workspace. Without that handoff, restart performs an
allocated-only gauge before launch and starts both its allocation sampler and
wall clock at that prelaunch boundary. A failed prelaunch gauge creates no
expected or active meter; after launch, the meter is tracked before health so a
health failure retains a complete inventory. Fresh V27 plan/observation/receipt
schemas admit at most one `data_measurement_failure` in observation/receipt
bytes with exact schema
`t4013-data-measurement-failure-v1`, `scope=custody`,
`gauge=allocated|logical`, `reason=deadline`, and `deadline_ms=30000`; no path,
command, output, identity, or raw error may enter evidence, and V1–V26 require
the field absent. The existing per-gauge 30-second bound remains unchanged.
Archive/restore preserves the peak logical and allocated gauges already merged
from backup, restore, and restarted-server meters rather than overwriting them
with a terminal re-gauge. Tests cover same-workspace one-shot consumption,
missing/mismatched/reused boundaries, prelaunch and post-launch failure
inventory, singular-diagnostic vocabulary/history/receipt rebuilding,
archive-peak preservation, simultaneous failures, and a real readiness path
with two finished start meters, no active meters, and two healthy source-free
startup records. That exact readiness boundary performs five sequential strict
gauge boundaries across the two meters—one more than the prior rehearsal—with
at most three `/usr/bin/du` attempts per boundary, the unchanged 30-second
per-boundary cap, at most 15 child attempts, and at most 150 seconds aggregate.
Relative to the prior rehearsal it also performs one additional private server
launch/health cycle with the existing process and allocation samplers. The real
interruption path falls from 20 to 16 gauge boundaries (at most 60 to 48 child
attempts); archive/restore falls from 18 to 15 (at most 54 to 45). Production
ceremony server count is unchanged, but each V27 server start begins one
allocation-sampler goroutine before the existing bounded four-executable
revalidation and launch, probing capacity at 1 Hz during that prelaunch window.
Observation, receipt, and teardown-checkpoint decoding adds one bounded raw-JSON
presence check to preserve historical absent-field/null semantics; V27 receipt
decode also performs one bounded canonical JSON re-encode and byte comparison
to refuse duplicate keys. This adds no
product request/query, sync, publication, source/corpus/shard read,
persistent schema, topology, or service-bound work. Integration, exact-main
preflight, and separate freeze and execution approvals remain required. No
fresh identifier is selected.

The first V27 readiness attempt found a separate production recovery defect:
all exact A extraction pointers had been reactivated while the current
operational schedule still targeted settled B, so bounded progress could never
become `current`. The shared runtime now checks an exact reused/completed
generation against any active or settled predecessor. A nonzero mismatch uses
the existing immutable transition enqueue. A zero-applicable mismatch instead
retires the exact current schedule projection without writing a binding or
inventing a partition; active history becomes superseded, settled history stays
settled, both remain lifecycle-owned, exact domain pointers stay authoritative,
and operational progress returns the established `unavailable` state. Focused
regressions cover settled-B → reused-A current progress and active/settled
zero-work reuse/new-publication retirement with zero repeat source/extractor
work. The corrected real-binary rehearsal passed structural A→B→A/restore,
semantic interruption/restore, and stale-worker recovery. An exact active
target retains its early return. Absent-schedule
reuse adds a second current-schedule query and no binding read. Settled reuse
adds that query plus two pointer-sized binding reads across initial target
resolution and the repeated coherence check. On a nonzero mismatch, reuse
totals three schedule-query/binding-read pairs: two before enqueue and one
confirming pair inside it. Completed reconciliation totals two: one before
enqueue and one inside. Enqueue then adds one bounded binding write and the
existing schedule transaction under the reconciler shard lock. A zero-work
mismatch stops before enqueue: reuse performs two pairs and completed or new
reconciliation performs one, followed by the exact retirement transaction's
current/schedule point reads, active-only status update, and current-row
deletion. A concurrent successor makes it stale without mutation. Immutable
completed controls may be replayed only through the
existing bounded chunks; no source/corpus member or extractor is reopened, and
concurrency, retention, memory, disk, API shape, persistent schema, topology,
and service bounds are unchanged.

The final exact working tree passed the complete T40.13 package (97.258s), its
full race package (109.786s), real-launcher custody proof (60.902s), 20 repeated
V27/schema/accounting runs (248.065s), semantic interruption/restore (124.67s),
stale-worker recovery (31.30s), structural A→B→A/offline-restore recovery
(138.58s), and full `internal/store` twice (1065.618s standalone and 1109.512s
inside an uncached repository run). Module verification, vet, lint, docs,
glossary, shell syntax, and whitespace also passed; every `internal/` package
was green in the uncached run. One earlier structural attempt was invalidated
by a host-native process-sampler `EPERM` after healthy startup; its diagnostic
root is retained and no process survives, and the same exact tree passed the
bounded structural rerun. The repository aggregate remains honestly red only
on four inherited retained-artifact assertions: T30.6m still binds 52 rather
than 54 retention components, the host Git 2.50.1 cannot reproduce T32.3 bytes
pinned by Git 2.54, and T32.4 still binds the pre-repin T32.3 digests. Each
failure reproduced unchanged at base
`acc5a23f046229c580b972bcbb0107f2f7062882`; none is a T40.13n regression, and
their separate fixture repair is not folded into this ticket. Independent
review of exact source commit
`b5d6b74da8644811c5e1bfffd658b73661797ee2` found no functional issue and one
low cost-record issue: the confirming nonzero pair belongs inside enqueue and
zero work has fewer pre-retirement pairs. The synchronized source-identical
documentation correction above resolves it; no finding remains. T40.13n
requests integration only and selects no identifier.

**T40.13o · Neutral-35/36/37 evidence and partial-publication recovery
closure** *(high gate readiness · independently reviewed; exact-tree
host-clean structural confirmation pending; needs T40.13n and neutral-37)* — reconcile the three consumed
runs without collapsing their distinct outcomes, close the V27 typed-nil and
partial-publication recovery defects, and make a future stopped receipt retain
bounded source-free attribution. Neutral-35 was the 63.325-minute V25 cold
measurement stop at exact source
`158dc6c9d87c26e4e7fc6a2f2ce38cc900da2119` and plan
`sha256:d9c1a646a7722c0d6496d1866c3a1450cbcbdfbf5c17c340d324173fe2ea543c`.
Neutral-36 was the 327.939-minute V26 stop at
`interruption/restart_start`, exact source
`acc5a23f046229c580b972bcbb0107f2f7062882`, and plan
`sha256:e2403ee87df84383e47b5b78a1f7fc1085425da3ec1b5af5f3214fa4e03ca9e7`;
its generic measurement-unavailable evidence establishes neither a pipeline
failure nor a scale pass. Neutral-37 was the 317.565-minute V27 stop at
`interruption/partial_verification`, exact source
`3d6ecf294e655c9121ea57cdec24b23b91a1cf4e`, and plan
`sha256:52b6c9d519358d84c34cbdb5b49bc44eff22005298e4a281ed3a598d82896f5b`.
It proved the selected lease was requeued, then completed exact clean teardown;
its controlling signed attribution is
`recovery/direct_recovery_failed/p6_investigation/substantiated`. Its signed
V27 bytes do not identify a retained partial owner/kind, prove a simultaneous
capture failure, or distinguish a partial-clear timeout from a scanner error.
The V26 meter-ordering defect predated neutral-35; the chronology must not say
that each run's repair introduced the next defect.

AC: both callers of `dataMeasurementDeadlineCause` reject a typed-nil pointer;
V26 and V27 classify every ordinary nondeadline cause identically, and a
sanitized measured-command failure cannot panic. Relationship and resolver
publication consult cancellation before the commit point, completely validate
new/reused bytes before creating `publishing.json`, and finish the bounded
marker→pointer→marker-removal sequence once the marker can exist; no redundant
full-generation validation remains after the marker. Before workers, startup
validates and atomically moves every package-owned raw generation, restore, and
sparse extraction stage into a retired namespace, then durably syncs the
parent. It deletes or drains nothing and preserves bytes and modification time.
The real lifecycle owner alone moves an eligible retired stage to collecting
and syncs the parent when it is at least 24 hours old or older than the newest
two for that repository and kind. Collecting stages then drain unconditionally
across bounded turns and restarts under all controller limits. Raw stages
created after startup remain untouched and make the report lower-bound. Fresh
plan/observation/receipt contracts advance to V28. A
stopped `interruption/partial_verification` may retain exactly one paired
`retained_partial_owner` and `retained_partial_kind`; owner is one of
`observation_publication`, `extraction_publication`,
`relationship_publication`, `resolver_namespace`, `rpc_caller_postings`, or
`kafka_topic_postings`, and kind is `publishing_marker` or `stage_directory`.
V1–V27 require both fields absent, while V28 rejects incomplete, null,
duplicate-hidden, mixed-case-hidden, unknown, or outcome-incoherent fields.
The ceremony scan is fixed-order and bounded across those six roots, reveals no
path/raw error/content, and intentionally excludes extraction candidate stages;
production recovery/lifecycle still owns every package stage.

Startup checks cancellation before each raw-stage collision preflight and each
rename. If cancellation follows a completed rename, the changed parent is still
synced before return, so the bounded partial prefix is restartable and no later
entry is mutated after cancellation is observed.

Steady-state cost: publication removes one redundant post-marker full
validation and adds only one constant pre-commit cancellation check. V28 adds
one ceremony-only bounded six-root scan per existing partial-verification poll.
Extraction startup acquires and holds the existing shared lifecycle-mutation
lock once for one bounded pass: at most 2,000,000 charged work operations,
stats, and stage candidates, eight scanner-charged peak descriptors, and
510,000,000 name bytes. The eight-descriptor budget excludes the one existing
mutation-lock descriptor. It reads only
names, types, and metadata, then renames and syncs; it reads no stage content,
hashes nothing, and deletes nothing. Startup inventories at most 4,096 regular
plus 4,096 sparse repository namespaces and may retain those 8,192 bounded
identities while it works. Each scheduled lifecycle turn acquires that existing
shared lock once and holds it while it inventories at most 4,096 repositories
in one publication or sparse phase. Either path accepts at
most 20,000 direct entries from one selected repository directory and retains
at most one additional entry only to detect overflow. Each lifecycle turn also
admits at most 64 stage candidates, sixteen removals, 256 stats (including
descriptor-open stats), eight peak descriptors, and 1 MiB of names. A clean completed
pass is exact and idle; post-start raw stages make it lower-bound without
creating permanent five-second backlog. Scheduled lifecycle alone performs
eligibility selection, collecting promotion and sync, and bounded drain work.
New non-reused extraction-generation construction performs one synchronous
result-directory fsync per accepted domain, from zero through 64 serial syncs,
before the existing final staging-directory sync and rename. Exact reuse/no-op
performs none; a failed creation retry that rebuilds an absent generation
repeats the same bounded sync work. Restore already performed its per-domain
syncs. Those new syncs extend the existing one-of-64 reconciler shard-mutex hold.
No new lock primitive is added. There is no added product query/request,
repository sync tick, corpus/shard/content read, hash pass, cache, worker, or child.
The pre-review tree passed 20 deterministic V28/typed-nil repetitions (1.495s), the
complete T40.13 package (103.560s), full package race (113.820s), focused
publication race, the real-launcher custody proof (62.074s), and readiness.
The first complete readiness attempt ran 233.93s: semantic and stale-worker
passed, while structural alone was invalidated after healthy startup by
host-native process-sampler `EPERM`. No PID/session survived; diagnostic root
`/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t4013-readiness-403545186`
is retained, and the one bounded structural rerun passed in 194.515s. Every
`internal/` package passed uncached, including standalone `internal/store` in
983.068s; module, vet, lint, docs, glossary, shell, and whitespace gates passed.
The repository aggregate was not duplicated because its inherited
T30.6m/T32.3/T32.4 retained-fixture reds are already reproduced at the base and
the ticket's complete internal/store bar is green. Independent review of exact
commit `704c2360e75e8a7d7068cbf3cd49b492a84cb50d` reported critical 0, high 0,
medium 1, and low 1: the startup cancellation gap and omitted 0–64 sync cost
above. Both are corrected. The corrected tree passed the cancellation regression
20 times (0.597s), full extraction normal/race (7.560s/9.354s), lifecycle
(0.615s), command (12.288s), vet, lint, docs, glossary, shell, format, and
whitespace. Two corrected-tree structural confirmations were invalidated after
healthy `http_ready` by the same host-native sampler `EPERM` (82.405s and
80.245s). PIDs 79356 and 81088 do not survive; diagnostic roots
`/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t4013-readiness-2026572958`
and
`/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t4013-readiness-180141300`
remain retained. The bounded rule permits no third retry. Fresh review of exact
corrected source commit `710f66f440464c4dabf1723f98134cb941c07232`
found critical/high/medium 0 and one low lock-cost wording gap. Source-identical
documentation commit `c4dfdabbd594b5f841b92058923343382d6cf5aa`
corrected it and passed exact re-review with critical/high/medium/low all zero.
No code-review finding remains; a later host-clean structural confirmation is
the only remaining regression gate. No
integration, fresh identifier, rerun, freeze, execution, release,
T40.13/Epic-40 closure, topology/bound change, or scale/SLO claim is authorized.

**T40.13p · Neutral-38 warm-restart oracle coherence** *(high gate readiness ·
needs T40.13o and neutral-38)* — preserve neutral-38 as the exact V28
`warm_noop` stop at source `b79406d12f517caed08f07120ca91b0ac1fbe471`
and plan
`sha256:da1804e13afb7b04a45a462552b75627ebb3a6e58bbe95c03c4fbad8080d2506`.
Preflight and the 79m56.010s cold phase passed. Warm restart stopped after
5.069s solely because its phase meter sampled two Git lifetimes; the healthy
startup record sampled the same two. Authority was unchanged, index children
and publication writes/transactions were zero, two controls were reused, and
exact teardown retained no derived or scratch-source custody. The cumulative
`member_reads=2,001,958` field is snapshot cardinality accounting, not a warm
phase delta or rejection predicate.

AC: fresh plan, observation, and receipt contracts advance to V29 while V1–V28
remain exact. V29 warm reuse requires phase Git children to equal the paired
healthy `warm-noop` startup Git children. Any sampled post-health Git lifetime,
index child, publication write/transaction, snapshot or authority change, or
missing control reuse fails. The completed-receipt validator applies the same
predicate. The real-binary 4,096-file structural readiness path calls the
production `warmNoop` implementation after cold convergence. Driver, durable
receipt, toolchain, inspection, and execution-contract registries recognize
V29. Production boot freshness remains unchanged and intentional.

Steady-state cost: production requests, startup, sync ticks, retry/no-op,
publication, lifecycle, locks, caches, corpus/member reads, memory, disk, and
child processes gain no work. Ceremony execution adds fixed startup-record and
bounded schema lookups, three closed identity/outcome checks, two checked
three-counter sums against the existing 8,192-lifetime ceiling, and one scalar
equality over already-retained startup/phase counts. Readiness adds one bounded small-fixture
restart/health/revalidation cycle. Full exact-tree gates, independent review,
integration, exact-main preflight, fresh-ID freeze, frozen-plan review, and
exact-ID/digest execution authorization remain required.

Gate status (2026-08-25): the corrected content passed 20 V29 repetitions
(1.333s), complete package (159.565s), full package race (145.292s), real
launcher custody (85.128s), all 69 non-store `internal/` packages, standalone
`internal/store` (1,256.530s), module, vet, lint, docs, glossary, shell, and
whitespace. Independent review of exact commit
`06b6e61e2316b33b5cad326e9efa2c9b97194309` reported critical/high/medium/low
all zero. Its two earlier medium findings—failure-path readiness custody and a
forged over-8,192 warm process inventory—and one low cost wording finding were
corrected before that commit.

Readiness remains open. One pre-review structural run passed the production
warm boundary with startup Git children 3 equal to phase Git children 3. For
the final content, one confirmation stopped before launch on a transient fresh
module-cache network timeout at retained root
`/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t4013-readiness-2696656312`.
After exact module reachability recovered, its one unchanged retry reached
healthy `http_ready` and then stopped on the deliberately sticky Darwin native
sampler `EPERM`; root
`/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t4013-readiness-2146731620`
is retained and PID 66440 is gone. Neither attempt is readiness evidence, no
process survives, and no further retry was attempted. A later host-clean
complete exact-commit readiness pass is mandatory before a merge request.
Integration, exact-main preflight, neutral-39 freeze/review/execution, release,
T40.13/Epic-40 closure, topology/bound changes, and scale/SLO claims remain
unauthorized.

**T40.13q · Neutral-39 diagnostic, lifecycle, and restore evidence integrity**
*(high gate readiness · needs T40.13p and neutral-39)* — retain neutral-39 as
the source-free status-1 execution sealed in package
`sha256:681aef5bb4ebe77c63ed564f5dfe499609a76738c3172b7a58e9c9f87d6a43cb`.
Its wrapper monitor last reported `prepare/admission` with unknown custody age;
the later terminal-summary `jq` expression failed on shell quoting. The monitor
bug did not disturb execution, but no raw private error survived source-free
custody. Do not attribute this run to sampler `EPERM` or name a failed ceremony
phase that its returned evidence does not prove.

AC: fresh plans, observations, receipts, driver paths, and readiness advance to
V30 while V1–V29 remain byte- and predicate-exact. If startup log inspection and
health/stage observation succeeded but process sampling failed, V30 retains the
valid startup evidence and uses zero only as the established unavailable-counter
sentinel, never as a measured-zero claim, with explicit
`process_sampling_unavailable=true`. A process-only failure seals
`failed_phase_process_sampling_unavailable`; an allocation-only failure seals
`failed_phase_allocation_sampling_unavailable`. Simultaneous or otherwise mixed
measurement failure remains `failed_phase_measurement_unavailable`.

V30 warm restart incrementally tracks phase-local candidate and job lifecycle
reports within the existing revalidation deadline. A candidate report with
`decision=warm_noop|cold_reuse|marker_recovery` and `outcome=done` is necessary
but does not complete the boundary. Released, failed, or requeued work rejects;
claimed, started, deferred, and yielded reports keep that job unresolved until
it later reports `event=done,outcome=success`; every attempt re-inspects exact
authority; and one existing five-second convergence interval must add no
candidate or job report after every job resolves. At that bounded quiescent
boundary the phase meter finishes once. Its single finished `PhaseMetrics`
process snapshot refreshes the paired startup process counters without another
sampler read; log/stage/wall facts refresh there. The atomic warm→delta handoff
transfers that process boundary with the exact log EOF and performs a bounded
post-reset tail scan: any candidate/job report or partial tail refuses the
boundary, complete unrelated lines advance the warm EOF, and later exact
claimed/started reports remain delta before startup/phase Git equality. This
admits
health-before-sync/reuse without admitting sampled post-boundary Git; V1–V29
warm semantics remain exact.

V30 ceremony servers enable synchronous exact candidate and job lifecycle sinks
for the sync, fetch, candidate, extraction, resolver, caller, and index runners.
Any encode, size-cap, sink, or panic failure latches cancellation; after all
workers join, the server returns a nonzero terminal error. Without the closed
exact-control environment, ordinary runtime reporting remains advisory.

Phase 7 fences immediately after ballast removal. Every lifecycle owner must
have `AttemptedAt` strictly after that fence in one coherent sorted cycle, and
one final exact-normal capacity observation must follow the latest owner. It
then re-inspects and requires protected authority unchanged. This ceremony gate
does not claim capacity stayed normal throughout the owner cycle and does not
wait for production hourly-idle eligibility. Independently, after production
capacity moves from collect/refuse to normal, exact-normal observations must
immediately bracket the sorted cycle-start owner and every owner result through
the ensuing coherent sorted cycle must be error-free and drained before hourly
idle.

Phase 8 uses the V30 restore comparator in execution and readiness. It may
discard only `IndexedCommit`, `RelationshipGeneration`, and
`RelationshipRootDigest`; it must compare `CallerGeneration`,
`RelationshipSemanticDigest` and all other product content.

Phase 9 restarts the ceremony server and waits for a phase-local cycle in which
all sorted frozen 14 owners are fresh and `state=ok`. The 13 owners other than
`durable-jobs` must be exact and drained; `durable-jobs` must truthfully report
`lower_bound` and may retain backlog because live writers prevent an exact
oracle. Exact capacity must be observed after the latest owner, current stable
authority must remain unchanged, and one bounded row per owner seals only
name/state/completeness/scanned/deleted/backlog. The restored readiness wait
requires the same owner-specific cycle and the V30 restore comparator. This
live phase does not claim eligible deletion or individually prove rollback,
active-lease, marker, or store-pin preservation; mandatory
`internal/lifecycle`, `internal/store`, and publication regression gates remain
the proof for those root protections.

Steady-state cost: no product query/request, repository sync tick, retry/no-op,
publication transition, corpus/shard scan, lock,
cache, persistent schema, memory/disk bound, or child-process behavior changes.
Production lifecycle scheduling adds a pressure-transition recovery suffix plus
one full cycle at the existing five-second cadence. Only while capacity stays
exact-normal and every owner result is error-free and drained is that at most 28
turns for the exact 14-owner inventory and at most 64 under the existing
at-most-32-owner controller bound. Any owner error/backlog or unavailable/
non-exact capacity removes the 28/64 turn bound and retains five-second runner
cadence. Healthy normal hourly cadence is unchanged. Neither ceremony lifecycle
waiter claims the 28/64 bound; each remains governed by its fixed phase deadline.
Phase 9 adds one restart, existing status polling, and a 14-row source-free
projection. Truthful `durable-jobs` backlog does not block either waiter; an
owner error or backlog in one of the 13 required drained rows keeps owner
progression at five-second cadence until the deadline. Validation is fixed
scalar work plus 13 exact/drained row predicates and one `durable-jobs`
lower-bound predicate. Warm
restart adds incremental phase-local candidate/job lifecycle parsing,
exact-authority reinspection per attempt, one existing five-second quiet
interval, one finished-metrics startup refresh without a second process sample,
one bounded post-reset log-tail scan, and an atomic process/log-EOF handoff
under the unchanged revalidation deadline. Phase 7 adds one existing
protected-authority inspection after recovery. Only
exact-control server mode adds synchronous per-report log writes for candidate
output and the seven job runners. Ordinary startup adds one closed
exact-control environment lookup and branch; when absent it allocates no report
channel or sink and adds no persistent work. Ordinary steady-state reporting is
unchanged. No new production request, job, child, or deadline is added.

T40.13 Phase-8 cadence correction (2026-08-26; supersedes only the pressure
cadence wording above): the first focused pressure rehearsal expired with a
truthful observation-v2 backlog. Exact retained full structural generations
contain 1,546/1,547 deletion units, so the larger tree needs 97 selected-owner
turns at the unchanged sixteen-delete cap. Fourteen-owner rotation at one
second still cannot fit the fixed ten-minute recovery deadline. Ordinary
backlog/error/capacity retry remains five seconds and healthy idle remains one
hour; only `collect`/`refuse` or the existing latched pressure recovery caps the
serial turn delay at 250 milliseconds. The exact larger tree then has 341.25
seconds of scheduled inter-turn delay, and the deterministic regression
conservatively reserves no more than 350 seconds after alignment and fresh
cycles. This is headroom rather than an SLO or ceremony pass. Elevated mode
offers at most four bounded turn starts per second before sweep duration until
a clean recovery cycle, a 20x scheduling-frequency increase; all
candidate/delete/query/stat/descriptor/metadata limits,
fair order, lock scope, concurrency, schemas, and V30 evidence predicates are
unchanged. A real focused rerun, complete gates, and independent review remain
required.

The separately run human Phase-7 `semantic-stale-worker` selector passed in
32.22 seconds for the subtest, 197.17 seconds for the top-level readiness test,
and 197.835 seconds for the package command. Its retained terminal record does
not bind exact HEAD and clean-checkout proof, so exact candidate attribution
remains open. The corrected Phase-8 wrapper subsequently passed from exact
clean commit `37fba2896f500104fa8283914ed19b8a003e3a24`: the subtest took
252.48 seconds, the top-level readiness test 310.35 seconds, and the package
command 311.030 seconds. That commit is integrated into `main`; a later host
check found no matching Phase-8 diagnostic path or live rehearsal/Surreal
process. These focused results supersede only the earlier Phase-7/8 run
requirements. Prior-phase handoff, full-corpus/root-volume scale, signed
custody, release, Epic closure, and scale/SLO claims remain open.

Human Phase 9 is `archive_restore` (`phaseOrder[8]`), not the zero-based
collection index. Its new opt-in small structural rehearsal directly exercises
the production live-backup, stop, offline-restore, restored-boundary,
reconvergence/comparator, meter, and shutdown coordinator. The first
working-tree run passed in 88.70 seconds for the subtest and 147.917 seconds for
the package command, removed its current-run private workspace, and left no
matching process. Older retained readiness roots remain untouched. Because it
preceded the immutable implementation commit, an unchanged exact-clean-commit
rerun remains mandatory. Human Phase 10 collection, signed custody, full scale,
release, Epic closure, and scale/SLO claims remain open.

Human Phase 10 then passed its unchanged selector from exact clean commit
`15487bbf15b602b04d81fbae6b989777b5cac44d`: the subtest took 147.89 seconds,
the top-level readiness test 205.45 seconds, and the package command 206.121
seconds. It emitted the required production boundary marker, removed its
successful workspace, retained no diagnostic root, and left no matching
process. That commit is integrated into and pushed as `main`. This closes only
the Phase-10 exact-rerun requirement; the earlier Phase-9 exact-clean rerun and
all full-custody, scale, release, and Epic-closure gates remain open.

Human Phase 11 now has a separately opt-in real-binary `authorized-query`
rehearsal. It enters cleanly with semantic A converged and stopped plus
structural A-return converged and live, then calls the unchanged production V30
coordinator. A pass requires the semantic restart, both stable-authority
revalidations, the fixed unauthorized/search/service/relationship/citation
oracles, exact two-meter accounting, and shutdown. Existing tests retain retry
and source-free failure-projection coverage; no production code, sparse image,
ballast, archive, or fixed lock is added. Its immutable exact-clean run remains
pending and will not prove Phase-8/9/10 handoff residue, full-scale custody,
release, Epic closure, or scale/SLO claim.

The unchanged human Phase-11 selector subsequently passed from exact clean
commit `c2e6eed8faab01854f3af94264ec3054487c877e`. It emitted the required
authorized-query boundary marker; the subtest took 44.46 seconds, the top-level
readiness test 104.93 seconds, and the package command 105.614 seconds. Both
stable-authority waits, the fixed query/citation oracles, exact control-read and
two-meter accounting, the mandatory query-member minimum, listener transfer,
restart, cleanup, and shutdown passed. No current-run diagnostic root or
matching process remained. This closes only the focused Phase-11 exact-run
requirement. The earlier Phase-9 exact-clean rerun, prior-phase handoff, full custody, complete
ceremony, release, Epic closure, and scale/SLO claims remain open. Ben
authorized fast-forward integration after this source-identical result record;
preflight, freeze, execution, and push remain separate.

Human Phase 12 (`teardown`, `phaseOrder[11]`) now has a separate opt-in
real-binary rehearsal whose unique parent keeps evidence, the run-root lock,
prepared publication, and a sentinel outside `custody/`, its only recursive
data-deletion target. Named external protocol artifacts are still retired, and
successful test cleanup removes the parent. It builds both bounded profiles in custody, uses the existing
receipt-valid V30 completed-prefix fixture for Phases 1–11, takes real
prepare→execute supervision, and launches one live structural Phebs/Surreal
session before calling the unchanged production teardown coordinator. A pass
requires session shutdown, exact data gauges, terminal checkpoint retirement,
durable custody absence, terminal observation publication, completed receipt validation,
supervision/prepared/checkpoint retirement, sibling preservation, and lock
custody through simulated Execute return. Existing tests remain authoritative
for checkpoint-before-delete ordering, negative failure, and resume paths; no production code is added. Complete
gates, independent review, an immutable commit, and an unchanged exact-clean
human run remain pending. This isolated fixture does not prove Phases 1–11 or
their handoff, full-scale/signed custody, a complete ceremony, release, Epic
closure, or a scale/SLO claim.

The first exact-clean Phase-12 attempt at commit
`cbbb873d251b56c0a2cd645ab02c99ee3a60d90a` stopped before prepared
publication, supervision, or supervised Phebs/Surreal server launch: the
synthetic manifest copies retained the bounded projection-profile names instead of the frozen ceremony
identities. Its 207 MiB retained root had no matching process and was purged
after review. The correction maps only the copied control labels through the
existing frozen constants and adds a fast schema invariant without relaxing
production validation or changing authored bytes. At that point, corrected
exact gates, re-review, and the exact-clean human run remained pending.

The corrected Phase-12 selector then passed unchanged from exact clean commit
`81d0a7a73214dbfa906e01eb3a8d611e8e950b2a`. It emitted both required
terminal lines; the test took 87.79 seconds and the package command 88.348
seconds. Its shutdown, exact gauges, terminal checkpoint retirement, durable
absence, terminal observation and completed-receipt validation, external
protocol retirement, sibling preservation, lock lifetime/reacquisition,
frozen-host validation, and three clean-checkout assertions passed. Successful
cleanup left no matching temporary root or process. This closes only the
focused Phase-12 exact-run requirement. The exact package gate separately
passed the deterministic checkpoint-before-delete ordering regression. At that
point, the Phase-7 and Phase-9 exact-clean reruns, prior-phase handoff, complete
ceremony, release, Epic closure, and scale/SLO claims remained open;
integration, preflight, freeze, execution, and push remain
separate actions requiring their own authorization.

The unchanged Phase-7 `semantic-stale-worker` selector then passed under
fixed-HEAD and clean-worktree guards at exact commit
`ce6212974f40fc452a124345c751a2b5bd473f9f`. It emitted the required boundary
marker; the subtest took 32.52 seconds, the top-level readiness test 91.40
seconds, and the package command 92.025 seconds. Pending and HTTP-409 lines were
bounded nonterminal convergence observations superseded by the terminal pass.
Successful cleanup removed the current workspace, and a separate check found
no matching rehearsal process. This closes only Phase-7 exact attribution.
Phase-9 exact-clean attribution, prior-phase handoff, complete ceremony,
release, Epic closure, and scale/SLO claims remain open; the source-identical
record grants no integration, preflight, freeze, execution, or push authority.

The unchanged Phase-9 `structural-archive-restore` selector then passed under
fixed-HEAD and clean-worktree guards at exact commit
`0d4cd82132bca5a0c48d1d1df9e377a0720c4bb9`. It emitted the required boundary
marker; the subtest took 89.04 seconds, the top-level readiness test 148.55
seconds, and the package command 149.292 seconds. The final active
relationship-progress row was a bounded nonterminal convergence observation
superseded by the terminal pass. Exact two-meter/two-server accounting,
shutdown, and successful current-workspace cleanup passed; a separate check
found no matching rehearsal process. This closes Phase-9 exact attribution and
the separately identified Phase-7/9 reruns. Cross-phase handoff, complete
signed ceremony, release, Epic closure, and scale/SLO claims remain open.
Integration, exact-main gates, fresh-ID selection, freeze and frozen-plan
review, execution, and push remain separate actions requiring their own
authorization.

Gate status (2026-08-25): the pre-review focused and bounded regressions,
complete package (104.564s), full package race (129.985s), real-launcher proof
(115.754s), complete readiness rehearsal (884.324s), every `internal/` package
including `internal/store` (1081.231s) and `internal/sync` (61.915s), module,
vet, repository-pinned lint, documentation, glossary, shell, and whitespace
gates passed. Independent review of exact commit
`4b40beb28e1549a4d269a7a7e0d9ed604c775c4b` recorded 0 critical, 0 high, 3
medium, and 3 low findings. The correction tree latches non-exact capacity for
the whole lifecycle cycle, refuses exact-mode stale reap changes/errors,
preserves same-phase process-sampling evidence through review-ceiling teardown,
continues warm confirmation from the settled cursor, allocates no absent
exact-report state, and corrects the owner count to fourteen. Exact correction
commit `ec4f2500d1b68dcbe539667d5833fdf694bc5adc` passed the complete machine
gate: package 101.806s, race 116.836s, real launcher 64.460s, readiness 820.702s,
command 12.869s, full `internal/store` 1019.180s, `internal/sync` 64.082s, and
all static/documentation checks. Re-review recorded 0 critical, 0 high, 0
medium, and 2 low findings: deterministic warm-cursor close on two
pre-confirmation failures and the two contradictory startup-cost sentences.
The next correction keeps the proven FD under unconditional phase-meter finish
cleanup and fixes those sentences; focused normal/race, docs, and glossary pass.
A new immutable commit, complete gate, and fresh independent review remain
pending. No merge, exact-main preflight, fresh-ID freeze, execution, release,
T40.13/Epic-40 closure, topology/bound change, or scale/SLO claim has passed or
been authorized.

Final exact source commit `50df638ad065814f4a9ea75c4f7493a622df3de0`
closes the cleanup-only safety-metric loss and passed independent review with
critical/high/medium/low all zero. Complete package (106.392s), full race
(134.820s), real-launcher custody (61.472s), command (19.006s), module,
compile-only, vet, pinned lint, docs, glossary, shell, and whitespace gates are
green. Readiness is not green: one complete run reached healthy structural
`http_ready` and then retained a Darwin root-sampler `EPERM`, while semantic
(302.46s) and stale-worker (31.04s) passed; one bounded unchanged confirmation
reproduced the structural refusal. Root denial remains sticky because accepting
a later sample could omit a short-lived descendant during the blind interval.
No process survives; diagnostic root
`/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t4013-readiness-1359799332`
is retained. The exact full `internal/...` run also timed out in
`internal/store` (1320.596s) during fresh `OpenLocal` schema application; the
isolated exact subtest passed in 11.349s, all completed
internal packages passed, and no SurrealDB child survives. A later host-clean
complete readiness plus full internal/store run remains mandatory. No merge,
exact-main preflight, identifier selection, freeze, execution, release,
T40.13/Epic-40 closure, topology/bound change, or scale/SLO claim is authorized.

**T40.13r · Darwin denied-descendant sampler availability**
*(high gate readiness · needs T40.13q and the retained readiness refusal)* —
supersede the earlier `root-sampler EPERM` attribution. Retained custody proves
that PID 554 was a still-parented descendant of the healthy Phebs startup root,
not the root itself; it did not retain that descendant's executable identity.
A bounded live-host reproduction establishes the causal mechanism without
inventing the missing historical field: the compatibility monitor's 50-ms
`/bin/ps` child is setuid-root on Darwin, and coherent task-all-info inspection
of that live child returns the same `EPERM` while short identity remains
readable. The fresh ticket also closes the sibling Darwin ceremony-session
inventory, which could otherwise create the same helper under admission
accounting.

AC: remove `/bin/ps` from the Darwin compatibility memory monitor and from
Darwin private-session custody, not from the Linux implementation or frozen
historical tool inventories. Compatibility monitoring must enumerate at most
128 process-group members through a 129-slot native inventory, validate each
accepted PID and group through one coherent task-all-info record, sum resident
bytes with checked arithmetic, and retain the existing 50-ms cadence, 512-MiB
limit, and three-consecutive-failure kill. Darwin session custody must enumerate
at most 8,192 non-kernel host PIDs through an 8,194-slot native inventory that
also accommodates Darwin PID 0 and one overflow sentinel, select the target
through `getsid`, revalidate the session after short-status inspection, omit
only vanished processes, privilege-free short-record-confirmed zombies, or a
process coherently confirmed outside the target session, and retain the existing
1,024-member session bound, shutdown deadlines, and signaling behavior.
Duplicate, malformed, permission, inventory, or arithmetic failures remain
closed. The T40 root and descendant sampler remains unchanged: root or
still-parented denied-descendant `EPERM` is sticky, while only the frozen
short-identity proof of disappearance or reparenting may omit a denied child.
Retrying, ignoring, or otherwise reclassifying that denial is forbidden.
V1-V30 evidence bytes and predicates do not change.

Steady-state cost: each Darwin sandboxed compatibility command replaces one
setuid child and one full-host text scan every 50 ms with one bounded
process-group list call and at most 128 fixed task-info reads. Each ceremony
session inspection replaces one full-host `ps` child/text parse with one
bounded native inventory, at most 8,192 initial `getsid` calls, and at most
1,025 short-status/confirming-`getsid` pairs (1,024 accepted members plus one
overflow sentinel); existing 10-ms shutdown polling remains.
No product search/query, repository sync, store/schema, authority, publication,
lock, cache, corpus/shard read, service bound, or ceremony deadline changes.
Focused native/repetition/race, real compatibility, session-custody, package,
static/documentation, and independent exact-diff review must pass before one
host-clean exact-commit readiness rehearsal. The earlier exact candidate's
serial full `internal/store` pass removes that unrelated blocker, but does not
substitute for review or readiness of this changed tree. No identifier, merge,
exact-main preflight, freeze, execution, release, Epic closure, or scale/SLO
claim follows.

Gate status (2026-08-26): exact implementation commit
`9bee810cd692d831993ff2e4784fb067f628b768` passed focused native/custody
repetitions, changed-package normal/race, module, vet, pinned lint,
documentation, glossary, shell, and whitespace gates. Independent exact-commit
review reported critical/high/medium/low all zero. The unsandboxed host-clean
structural rehearsal passed in 373.567s and completed exact teardown. The
serial `internal/...` command reached the default ten-minute package alarm
while `internal/store` opened a fresh engine, with every completed package
green; the exact standalone full `internal/store` package then passed under
its 30-minute allowance in 993.780s. No SurrealDB child or port-65499 listener
survives. The T40.13r review, readiness, and full-store blockers are closed and
the source-identical branch is eligible for a separate integration request;
merge, exact-main preflight, identifier selection, freeze, execution, release,
Epic closure, and scale/SLO claims remain unauthorized.

Independent review of the first gate record found one medium preservation gap:
the 373.567s structural leg alone was not the inherited complete-readiness
suite. Without changing any built source, the remaining `semantic` and
`semantic-stale-worker` legs then passed together in 390.868s. The prior
structural result is preserved across the documentation-only commit because no
compiled, embedded, fixture, or harness input changed. All three readiness
legs are now green for implementation commit
`9bee810cd692d831993ff2e4784fb067f628b768`, and no rehearsal process or
port-65499 listener survives. Fresh exact-HEAD documentation re-review remains
the final T40.13r branch-close check; every authorization boundary above stays
unchanged.

**T40.13s · Same-generation observation-stage recovery and retained-partial sealing**
*(high ceremony correctness · needs the retained neutral-40 stop)* — preserve
`t40r1-neutral-40` as an honest phase-6 stop. Its retained custody identifies
an `observation_publication` `stage_directory`; it establishes neither a scale
pass nor a later-phase result. The production collision path validated an
already-current immutable generation and advanced the same authority, but left
the redundant `.stage-*` live. Separately, the V30 stopped-observation validator
rejected the retained-partial fields that its V28 schema introduced.

AC: after an exact same-generation collision validates, completion must first
durably write the root, remove the marker, and sync the publication directory;
it then same-parent-renames only the redundant stage to the existing
`collecting-stage-*` lifecycle namespace and syncs again. A mismatched or
invalid generation still refuses, recursive deletion remains outside the
publication fence, and current/rollback authority is unchanged. Valid stopped
retained-partial attribution is accepted for V28 and later while V1-V27 remain
closed. Focused tests must prove the redundant live stage is gone, its retired
directory is ordinary and non-symlink, V28/V29/V30 receipt round trips, and the
historical V27 refusal.

Steady-state cost: only a successful same-generation collision adds one
same-filesystem rename, one publication-directory sync, and later bounded
lifecycle deletion. When worker retry or startup recovery reaches that
collision, the rename and sync extend its existing exclusive mutation-lock hold;
they add no lock acquisition. New-generation publication, collision-free
retry/recovery, ordinary no-op/reuse, query, sync tick, store/schema, authority
reads, caches, corpus or shard reads, memory bounds, and child processes are
unchanged. The harness change is one in-memory version comparison during
stopped-observation validation. Focused package/race, documentation,
whitespace, and independent review gates precede any small phase-6 rehearsal.
Retained neutral-40 custody must not be re-executed or purged without separate
review; no merge, fresh-ID freeze, execution, release, Epic closure, or
scale/SLO claim follows.

Gate status (2026-08-26): exact implementation commit
`0e5eba0109e632b9a1bd8f24c9f876aca5146e68` passed both affected packages,
the complete observation-publication race package, focused harness race, 20
focused repetitions, vet, documentation, glossary, and whitespace. Independent
working-tree review found critical/high/medium 0 and one low cost-wording gap;
the committed tree includes that correction. The opt-in real-binary semantic
rehearsal then passed on the exact clean commit: phase-6 first/restart meters
were 8,889/6,961 ms with 219,816,960 logical and 220,921,856 allocated bytes;
the interruption boundary, partial-state clear, backup/restore, restored
lifecycle, and authorized query all passed. The semantic subtest took 295.38s
and the top-level rehearsal test including private tool builds took 456.36s;
the package command completed in 456.991s. Its temporary workspace was removed
and no matching process remains. T40.13s is eligible for the separately
authorized fast-forward merge; phases 7–11, a new freeze, full ceremony,
release, Epic closure, and scale/SLO evidence remain unestablished.

**T40.13t · Bounded full-custody data-gauge accounting**
*(high ceremony correctness · needs the sealed neutral-41 stop)* — preserve
`t40r1-neutral-41` as an honest V30 harness-accounting stop. Exact source
`a28e0573f0089c22dda610ad1bf065328d47865d`, frozen plan
`sha256:8799f5e63f61b44ecea7b3e08f607922715589a0832b0b2802f75824ad9fd507`,
and sealed source-free package
`sha256:8b29e86c7227752964addd1c5dc06c729ed53288d0371b6926c78dc4dc555423`
prove Phases 1–6 passed. Phase 7 completed the stale fence and convergence,
then its final exact allocated-data gauge exceeded the inherited 30,000-ms
deadline. Phases 8–11 did not run. Teardown was clean, and the separately
reviewed pressure reservation was durably retired.

AC: advance only fresh plan/observation/receipt execution to V31; V1–V30 bytes
and predicates remain exact. Keep `du` as the sole strict logical and allocated
meter. Give each exact whole-custody gauge a 300,000-ms deadline, so one
allocated/logical pair reserves at most 10 minutes, and propagate that same
strict bound through every caller. Add a closed source-free
`t4013-data-measurement-failure-v2` diagnostic that identifies only the exact
gauge, `reason=deadline`, and `deadline_ms=300000`; do not retain a path or raw
error. Retire consumed identifier `t40r1-neutral-41` and admit 42. Focused tests
must pin the timeout, v2 projection/round trip, every strict caller, historical
V1–V30 behavior, and the identifier fence. One exact full-profile Phase-7
rehearsal must pass through the terminal gauge before any new freeze.
Neutral-41's clean teardown left no physical custody to remeasure. Its logical
owner counts and Phase-6 byte maxima do not bound the real Phase-7 filesystem
entry, directory, hardlink, cache, or concurrent-work shape, so a synthetic
file-count/byte-floor proxy cannot satisfy this gate.

Steady-state cost: no production request/query, sync tick, startup/restart,
retry/no-op, publication, lifecycle/store, lock, cache, schema, corpus/shard
read, hashing, memory/disk, or production child changes. Each complete pair
retains two serial gauges. Each gauge permits the unchanged maximum of three
serial `/usr/bin/du` attempts inside its one deadline: a healthy first-attempt
pair launches two children, while a completed retrying pair may launch six and
repeat the metadata traversal six times. Only the pair's maximum reserve rises
from 60 seconds to 600 seconds. Meter or measured-command begin and finish can
consume two pairs; the V27 restart start consumes one allocated-only gauge plus
its finish pair. All complete early when `du` returns and remain within the
unchanged 12-hour ceremony ceiling. The diagnostic adds bounded scalar JSON
only on failure. No merge, freeze, execution, release, T40.13/Epic-40 closure,
or scale/SLO claim follows.

Implementation now includes the missing exact full-profile Phase-7 runner.
The opt-in protocol requires an independently supplied reviewed 40-hex commit,
materializes and hash-checks that commit's
`run-phase7-full-profile-replay.sh` with fixed closed Git, and executes it under
`env -i` Bash; direct live-wrapper invocation is unsupported. It requires a
reviewed exact-clean HEAD and dedicated-host attestation, authors both frozen profiles, and calls
the same `executeThroughStaleWorker` prefix used by production `Execute`.
Pressure and later coordinators are not called. The deliberate V31-only
pressure-boundary stop exists solely to reuse the production resumable
stopped-teardown protocol; its cleanup observation is bound into a separate
source-free replay record that requires phases 0–6 succeeded, the Phase-7
terminal logical and allocated gauges nonzero, `pressure_started=false`, and
custody plus supervision retired. A late server-stop error cannot use the
deliberate boundary identity. The supported fixed `/usr/bin/env -i` outer
bootstrap materializes the reviewed wrapper with absolute host utilities; the
wrapper then binds Git and Go and creates a fresh
owner-only shared clone detached at the reviewed commit under closed Git
config/attributes/excludes/fsmonitor/replacement-object/hooks controls, requires the replay parent outside the original
checkout, and compiles and runs exclusively from that source. It rejects
modified, untracked, or ignored inputs in the private clone before and after
execution, clears ambient Go/workspace/overlay controls, uses fresh private
build/module caches, gives the Go child the same closed Git controls, and
forwards INT/TERM/HUP to the child process group. Clone, checkout, and Go each
run under an identity-pinning sentinel; stopped exact group inspection admits
release through a parent-held FIFO descriptor only when that sentinel is the
sole live stopped member, while every other result
retains the sentinel/control root/fixed lock. A nested launcher installs
terminating traps before it emits ready; the parent retries an interrupted
ready read and forwards a latched signal only after consuming ready, so a
signal before or immediately after readiness cannot cross into an unstarted
workload. Each boundary adds
one sentinel shell, one nested launcher shell, two FIFOs, one
parent-held read/write release descriptor, one parent read-only notification
descriptor, three empty-marker creates, one status write plus rename, two
notification writes, one release write, one marker unlink, and normally one but at most 100
host process snapshots plus roughly one second of bounded quiescence waits.
The sentinel is the notification FIFO's sole writer during the workload, which
closes that descriptor, so completion or hard death wakes the blocking parent
read with a record or EOF and launches no polling process. Exact job comparison
adds one short command-substitution Bash child at drain entry and one more only
when a signal handler enters. The same
sentinel rooted in the fixed lock supervises recursive private-cache/source
retirement, for four child boundaries total.
Regressions prove an ignored live `_test.go`, assume-unchanged and
skip-worktree modifications, a hidden live wrapper, poisoned Bash/Git state,
and poisoned local fsmonitor are not compiled or executed, and an ignored in-checkout parent is
refused before authoring. Cancellation or cleanup
uncertainty retains the private roots and fixed lock. After result hashing and
cache retirement, success atomically writes an exact completion marker inside
the retained fixed lock, so hard death cannot reopen admission before terminal
output. Only a zero-status wrapper with terminal PASS and a matching marker
admits the result; the lock then requires separately reviewed retirement.
In-test preparation is capped at four hours, execution retains its
independent 12-hour ceiling, and the test alarm is 20 hours from binary start;
module download/compilation happens before that alarm. Lightweight classification,
result-shape, skip, shell, and
package gates do not satisfy the AC: exact-main `d18fde43` was rejected before
execution because its live-worktree build could consume hidden inputs. A new
immutable correction, independent review, the exact expensive replay, and
review of its source-free result remain mandatory before any freeze.

**T40.13u · V32 progress retry-conflict transition accounting**
*(high ceremony correctness · needs the retained V31 full-profile stop)* —
preserve the failed full-profile Phase-7 replay as coherent evidence, not a
pipeline stall. The command ran for 23,469.777 seconds. Its stale-worker wait
ran for 2,540,013 ms across 509 probes and 155 progress changes, but exhausted
the 32-entry diagnostic inventory because 15 extraction-progress HTTP 409s,
then classified `status_other`, alternated with fresh pending projections.
The active schedule still reported 272 materialized, 70 succeeded, 202 pending,
and zero failed partitions. The source-free cleanup observation digest is
`sha256:c69ce4124464f22934a2cd5972898ad1a7143604dbe1fcabdddcefa2689d675d`.
The replay did not reach the later explicit stale-chunk fence and establishes
no Phase-7 pass.

AC: advance only fresh plan/observation/receipt bytes to V32 and the fresh
full-profile replay result to v2; keep V1–V31 bytes, classification, and
validation exact. Under the V32 inspection contract only, classify seven
closed snapshot/authority 409 details as the existing `409_stale` reason: two
from observation progress, two from extraction progress, and three from caller
generation progress. Bind every classification to its exact endpoint. Count
every recognized conflict with exact first/last wall times while holding at
most one latest transition. Another recognized conflict replaces the hold; a
same-stage pending probe clears it and lets the existing progress coalescer
bridge the conflict. Before any non-recognized class/stage, or before recording
the wait, materialize the hold. This must make final-409 and all-409 waits
sealable without raising the fixed 32-transition bound. Observation
control-absence, unknown 409s, transport, response, 5xx, 503, terminal,
non-progress stages, and overflow inspections remain distinct and fail closed.
Also name the exact extraction-progress `Read` 500 as `500_store` and `Invalid`
500 as `500_response` under V32 only; neither enters the retry hold, and both
retain their ordinary transition cost. The same details remain `status_other`
on V1–V31 and on unrelated endpoints.
At 31 existing
entries, a held conflict may become entry 32 and the next terminal remains the
overflow inspection; preserve diagnostic-limit priority and its typed terminal
projection. Relax relationship-tail equality only when the counted recognized
conflict is the retained tail or the overflow inspection at the aggregate's
exact last-conflict wall time; conflict-free and causally unrelated diagnostic
limits retain the V31 fence. Tests must pin all three endpoint/stage sets, the
incident-shaped 409/pending sequence, V31 historical overflow,
final/all-conflict materialization, cross-stage hold replacement, non-benign
flushes, 31/32 boundaries, summary forgery rejection, schema coupling, current
driver dispatch, and the existing last-inspection XOR.

Steady-state cost: no product request/query, worker, sync tick,
startup/restart, retry/no-op, publication, lifecycle/store mutation, lock,
cache, corpus/shard read, hashing, disk/memory ceiling, polling interval,
deadline, or child changes. Each V32 HTTP error classification performs one
closed endpoint/detail match, and each convergence inspection performs one
closed stage/class/status/reason predicate. A recognized conflict updates an
`int64` count, two durations, and one fixed transition value; recording or a
non-recognized next probe appends at most that one value before the existing bounded
slice clone. The receipt adds at most three scalar JSON fields. V31 retains its
old accounting. Observation decode, receipt decode, and teardown-checkpoint
resume each add one in-memory field-presence pass over already-read evidence
(at most 256 KiB for observations/receipts and 260 KiB for checkpoints) and at
most 16 convergence waits; they add no I/O or child. Focused/package/race/vet/docs gates and independent review are
required before a merge request. A new immutable candidate, exact replay,
freeze, ceremony, release, Epic closure, and scale/SLO claim remain separate
and unauthorized.

Exact result (2026-08-28): the clean immutable candidate
`968311621f389643365587f4ae588ba83c832e68` passed the dedicated V32
full-profile replay in 21,281.087 seconds. All seven phases through
`stale_worker` succeeded with exact oracles; the deliberate boundary stopped
before `pressure`, published a v2 source-free result, and completed clean
custody/supervision retirement. Five convergence waits counted six recognized
progress-retry conflicts and all converged with five to seven retained
transitions, directly exercising the correction without changing the
32-transition ceiling. The exact replay result is retained at
`spike/t4013/t4013u-v32-full-profile-phase7-replay.json`; it binds the plan,
cleanup observation, and result with respective SHA-256 digests
`8784172854b86275d55705e920e6bf6e0499910e3d254c961a41639a0f5a3005`,
`6eaef4eb7cea706c2e9b5874a5e09e0e3978e6cdb6363fd316263c9650a8a426`,
and `0e17da4500e8000713ca8e3abc6f97041772b3d78bdb2bf3661589f5e5b84c75`.
This satisfies T40.13u's expensive replay and result-review AC only; integration,
exact-main preflight, freeze, the complete ceremony, release, Epic closure,
and scale/SLO evidence remain separate.

**T40.13v · Restore-epoch downstream job projection authority**
*(high production recovery correctness · needs the sealed neutral-42 stop)* —
preserve `t40r1-neutral-42` as an honest Phase-9 archive/restore stop. Exact
source `4496d5e12ebc026e2a12e8011505207f6582aaf1`, plan
`sha256:6818fa92a235ecad3978b48e3a6d6d4f67eba9e9647035d5eb2cd134207ae080`,
and source-free package
`sha256:9bb96d6c0dc059f6f34573c0b4469f8968eaf8fe3b89009ab39312ce5f94ec74`
show that restore correctly discarded restartable generation schedules but
left repo-level extraction, resolver, and caller job projections in the
imported control epoch. The unavailable restored schedule plus the retained
historical failed extraction projection caused the current terminal oracle to
stop before a restored successor could own that projection. This is neither a
Phase-9 pass nor evidence of a new pipeline failure.

AC: the restore-only generation-control reset must unset all three downstream
job pointers, their ordering timestamps, and their writer-version markers in
one transaction. Preserve durable job history and the independent index-job
projection. A subsequent generic enqueue must project an exact coalesced
pending extraction, resolver, or caller row even when that row predates the
imported failed projection; candidate publication must do the same for its
exact returned extraction successor. Do not scan, delete, rewrite, requeue, or
backfill job history, and do not weaken the current terminal oracle. Retained
plan/observation/receipt/store schemas and wire bytes stay exact.

Tests must reproduce an older pending row behind a newer failed projection,
prove all downstream projections become unavailable while history and the
index projection survive, and prove generic and candidate writers rebind the
same pending row. The real backup/offline-restore test must cross the production
reset and rebind path. Focused normal/race, recovery, vet, docs, whitespace,
independent exact-candidate review, and the small Phase-9 archive/restore
rehearsal are the merge bar. A full ceremony is not a regression test.

Steady-state cost: one restore-only repository-table update in the existing
generation-control transaction; no extra round trip, lock, history scan, row
deletion, backfill, or child. A coalesced generic extraction enqueue and a
candidate publication each add at most one guarded repo-record point update in
their existing transaction. Ordinary queries, sync, non-restore startup,
lifecycle cadence, evidence bytes, caches, corpus/shard reads, memory/disk
ceilings, and every deadline remain unchanged. Merge, freeze, full ceremony,
release, Epic closure, and scale/SLO evidence remain separately unauthorized.

Exact result (2026-08-29): implementation commit
`d6fe7d41fef76750cf6454baf0fd2161c4c82378` passed focused store/recovery
normal and race coverage, the complete recovery package, module verification,
vet, focused lint, documentation, glossary, format, and whitespace. Independent
review traced the restore caller and every extraction writer and reported all
severity counts zero. The exact-clean real-binary Phase-9 rehearsal then
observed the restored schedule move from unavailable to an active extraction
successor, crossed a benign 409, and passed the archive/restore authority
boundary in 90.55 seconds; the top-level test and package completed in 228.48
and 229.179 seconds. This closes T40.13v's focused correction gate only. Merge,
exact-main preflight, freeze, complete ceremony, release, Epic closure, and
scale/SLO evidence remain separate.

**T40.13w · Retire consumed neutral-42 ceremony identifier**
*(low-risk harness integrity · needs the sealed neutral-42 disposition)* —
advance the permanent review-stopped fence from 41 through 42 before freezing
neutral-43. The extant neutral-42 directory is only an incidental overwrite
guard; later reviewed housekeeping must not make that consumed identifier
admissible again.

AC: `t40r1-neutral-42` is rejected by the launcher's constant-time numeric
fence and `t40r1-neutral-43` is the first accepted sentinel. Preserve every
other ID validation, plan/evidence byte, phase, deadline, and custody behavior.
Run the focused driver regression, shell syntax, documentation, glossary, and
whitespace gates plus independent review before fast-forward integration.
Repeat exact-main prospective preflight after integration, then freeze and stop
for plan review; do not execute. The ticket deletes no prior evidence,
reservation, custody, or control root and adds no production or ceremony work.

**T40.13x · Phase-11 cold exact-reader readiness and attribution**
*(high production correctness · needs the sealed neutral-43 stop)* — preserve
`t40r1-neutral-43` as an honest Phase-11 stop. Exact source
`c992e54497d3468841bfd40d53005237f1a2ba29`, plan
`sha256:08343c47f0684b0178aaa12ccf7179bf85fd4eca1e6aa4f1e2f61f32238b9f80`,
and source-free package
`sha256:0ecacfe9dda34ec601c9391ac73d9b9b3ba84b7481588041541477ea9e26c1ee`
show exact success through collection and clean teardown, followed by one
status-500 structural authorized search. The source-free receipt deliberately
does not retain the server's private reason, so this stop does not establish a
stale root, a publication-marker fossil, cold whole-reader validation, or any
other root cause. The committed compact summary is
`sha256:76c7515ffb926b945b4bec01c9e4913c765bcd5a90add407754d405b1369ebe4`.

AC: permanently reject neutral-43 and preserve a committed source-free digest
summary. Add one bounded production-path discriminator that crosses
archive/restore, the Phase-10 restart, fresh lifecycle collection, and the
first Phase-11 search. It must record publication-marker presence, exact
root/store revision equality, and the first versus later search outcome so the
cold-reader boundary is distinguishable from stale publication authority.
Correct the shared readiness boundary rather than retrying generic status 500
or relaxing exact marker/root/content validation. Keep the existing ten-second
product query budget and retained schemas exact unless a separately justified
schema change is unavoidable.

Tests must make the pre-correction cold failure reproducible in seconds, prove
the selected shared correction, and keep stale-root and active-publication
refusals fail-closed. Add one combined Phase-9→10→11 focused rehearsal; the
existing isolated Phase-9 and Phase-11 selectors are insufficient because
Phase 10 restarts the structural server. Focused normal/race, vet, docs,
glossary, shell, whitespace, and independent exact-candidate review form the
merge bar. A complete ceremony is not a regression test.

Steady-state cost: identifier and evidence changes add no runtime work. The
discriminator is test-only. The production correction may perform only the
already-required exact generation validation at a lifecycle/readiness boundary;
it must add no second corpus/shard pass per restart, no request-time fallback
scan, no new polling loop, child, persistent lock, or relaxed deadline. Query,
sync tick, retry/no-op, publication, lifecycle mutation, cache invalidation,
and disk/memory ceilings otherwise remain unchanged. Merge, freeze, complete
ceremony, release, Epic closure, and scale/SLO evidence remain separately
unauthorized.

Implementation result (2026-08-29): the production reader now returns one
typed warming sentinel only when the request deadline leaves the same
cache-owned shared validation or exact-reader load active or races its
same-generation successful completion. REST exposes that state as one fixed
409 without private cause; service search does not queue a
repair for it. Startup remains lazy, the request and cache-task deadlines stay
at ten seconds and ten minutes, and every exact marker/root/content fence stays
mandatory. V32 retains three attempts and defers only its final exact-warming
attempt when the first search reported warming and the second attempt remained
retryable, inside a twenty-minute context. V1–V31 keep their historical
classifier, context, retry cadence, and wait-cancellation evidence. Focused
normal and race tests passed across search, API, and harness packages. The
extended real-binary selector crossed archive/restore, the Phase-10 restart,
fresh collection, unchanged pre/post search authority, and both Phase-11
profiles while recording the first and later structural searches as
single-attempt successes. The corrected selected subtest took 184.94 seconds
and the package 238.435 seconds. Complete affected-package tests, focused race,
vet, repository-pinned lint, module verification, docs, glossary, shell, and
whitespace passed. Two final independent reviews reported all severity counts
zero. Integration, freeze, execution, release, Epic closure, and scale/SLO
evidence remain separate.

**T40.13y · Completed Phase-9 through Phase-12 handoff rehearsal**
*(medium-risk harness evidence · needs T40.13x)* — close the remaining bounded
late-phase seam before spending another full ceremony. Preserve the existing
standalone Phase-12 selector, but add one V32 real-binary run that crosses
archive/restore, the structural collection restart, both authorized-query
profiles, and completed custody retirement without stopping the structural
server before teardown.

AC: the fixture must retain only the existing receipt-valid synthetic Phase
1–8 prefix and global full-profile oracle. It must clone and truncate the
startup/wait inventories through pressure, clear every synthetic Phase 9–12
field, and prove that truncated observation cannot produce a completed
receipt. Real Phase 9–11 execution must append exactly three healthy startups,
four converged waits, fresh collection evidence, and exact succeeded phase
records. Phase 12 must own all tracked-server shutdown, both nonzero custody
gauges, pre-delete validation, checkpoint-before-delete, durable exact-custody
absence, terminal observation/receipt publication, supervision/prepared/
checkpoint retirement, sibling and module preservation, run-root-lock lifetime,
and terminal clean-HEAD proof.

Run the fast splice and teardown-order regressions, complete T40.13 package,
focused race/vet/docs/glossary/shell/whitespace gates, the exact-clean opt-in
rehearsal under a 30-minute context and 35-minute command alarm, and independent
review. On failure retain the complete private parent; do not rerun or purge
until process absence and the external checkpoint/stage/supervision commit
point are reviewed. No production code, schema, full-corpus proxy, or fault
injection is authorized by this ticket.

Steady-state cost: zero. The test authors the two existing bounded profiles and
builds the private toolchain once; it starts the semantic seed plus the real
late-phase server sequence, runs backup/restore, one fresh lifecycle cycle,
fixed queries, two serial teardown gauges, recursive private-custody deletion,
and bounded protocol retirement. This does not bound full-custody deletion,
replay Phases 1–8, prove signed ceremony evidence, establish a complete
ceremony or scale/SLO result, or authorize freeze, execution, release, or Epic
closure.

Result (2026-08-29): exact clean implementation commit
`1f589e3ece12d60a625fa28fbad06156419359a5` passed the selector in 244.12
seconds for the test and 244.816 seconds for the package. The required Phase
9–12 custody-retirement marker, completed receipt, durable custody absence,
protocol retirement, clean checkout, private-parent removal, process absence,
and free port 65499 were observed. Complete normal/race, vet, pinned-lint,
module, docs, glossary, and whitespace gates passed; independent corrected-diff
review reported no finding. Integration and every full-ceremony claim remain
separate.

**T40.13z · Neutral-44 pressure stop and consumed-ID retirement**
*(low-risk harness integrity · needs the sealed neutral-44 disposition)* —
preserve `t40r1-neutral-44` as an honest Phase-8 pressure stop. Exact source
`f6b731543d136c2801bcbc85db53dc9e8fd7498f`, plan
`sha256:4dbcf229862fe2d569c64ee7ca14581808ab5fe245867f16e40bdbcbddc0c6db`,
observation
`sha256:0bef1ba01d2a92e8f06dc87c502c5468252407ba70005b10f3770b60d333425b`,
receipt
`sha256:61d8198fab6d0f4ce98837f92c565deb1ca0720c0412b3a56d08dabfbf3d051a`,
manifest
`sha256:88d20d3fc397df1cb43e412ab457a77b12734036cdf4572f426630e128e3b148`,
freeze
`sha256:8c5614b47ea9ffa503fd3c6128685902e42d5dc44f4ca1c1bb8bc516e8a7d659`,
and source-free package
`sha256:a20d9a8b895ed0a727fe616f1d68ff994d9530409d8688ffbd55e168eccf1655`
show that preflight through stale worker succeeded before pressure returned
`lifecycle/production_pressure_gate_refused`, `reduce`, and `substantiated`.
No `pressure-restart` or ballast creation occurred; archive/restore,
collection, authorized query, and the full-size T40.13x Phase-11 confirmation
did not run. Teardown completed without derived or scratch-source custody.

AC: permanently reject neutral-44 and admit neutral-45 first. Preserve the
sealed distinction between the at-most 230,893,179-byte nominal zero-workspace
preflight slack and an exact Phase-8 cause: prepared allocation, the Phase-8
capacity sample, and the selected inner refusal arm are absent. Do not change
V32 or widen the 80/96-GiB custody ceilings.

After reviewed cleanup, use the existing owner-only, same-volume, fully
allocated stable-reservation protocol, keep availability inside
`181,130,218,415..194,540,402,299`, and target at most `190,000,000,000`
decimal bytes. Pass the focused driver test, shell syntax, docs, glossary, and
whitespace gates plus independent review. Integration remains a separate
authorization; only exact-main preflight and neutral-45 freeze may follow it,
and freeze stops for plan review. Execution remains unauthorized.

Steady-state cost: the existing constant-time consumed-ID comparison and one
test row are retained.
Documentation and the host reservation add no production request, query, sync,
retry, publication, lifecycle/store mutation, child, lock, schema, cache, or
persistent product state. The reservation is host preparation, not evidence or
ceremony custody.

**T40.13aa · Neutral-45 typed exact-oracle correction**
*(high ceremony correctness · needs the sealed neutral-45 disposition)* —
preserve `t40r1-neutral-45` as an honest V32 post-query finalization stop.
Exact source `49cc5afb1cb3bc8846080588742ff9141d15af90`, plan
`sha256:4b6edd87951e7c7016094489a77c9bd4be324de9c7e0ac55ebce70aa429cfef7`,
observation
`sha256:98bf4348cb0dea39409f67be932555e7bda0efb4a950186ff6c43b9fae9a71b1`,
receipt
`sha256:7615b170d9ebd8b5e52846e3014032311091ca733217aeffdedda41489873798`,
and package
`sha256:0b6c398e56b87c888c34cfb65ec4253ce0cfbaa7f18ff6e9957b85a7486d665e`
bind the finalizer's type error: publication unsupported blobs are zero, while
131,072 is the separately modeled observation-gap count from the two
65,536-input semantic gap families. All eleven production phase bodies ran;
stopped teardown is exact and custody-free.

AC: rename the private snapshot counter to unsupported blobs; require
structural and semantic publication unsupported blobs to equal zero in V32;
derive and assert the separate 131,072 gap-classification oracle; project
`Explicit.GapFacts` and `NoSilentEmpty` from that value; retain V1–V31
execution and completed-receipt validation exactly; and report all ten V32
final scalar mismatches deterministically. Extend the existing finalizer
derivation/table, full-scale `TestExactSemanticColdTiming`
(`262144/0/180224/360448/9`), and the T40.13y initial, post-restore,
post-collection, and post-query projection checks. Add no `exact_totals`
receipt section, V33, or permanent copied neutral-45 fixture. Retire 45 and
first admit 46. Require package/race, semantic-scale, late-phase, vet, lint,
docs, glossary, shell, whitespace, and zero-finding exact-commit review before
integration; freeze and execution remain separate authorizations.

Gate record (2026-08-30): exact implementation commit
`06df07ec4a7e3db7145874e0d1d9208883908bad` passed the full package and race
package, vet, pinned lint, docs, glossary, shell, formatting, and whitespace.
Its unchanged host-network late-phase selector passed the real Phase 9–12
handoff and custody retirement in 246.48 seconds after one pre-launch isolated
module-cache DNS failure. Its semantic-scale v5 record passed with exact totals
`262144/0/180224/360448/9`, 2,083,618 ms cold wall, 3,427,647,488-byte peak RSS,
and 3,346,604,032 allocated bytes; exact cleanup completed. Fresh exact-commit
review and independent OCR found zero issues. The implementation is eligible
for an explicit integration request, not an implicit merge or freeze.

Steady-state cost: no product path changes. V32 ceremony completion retains
ten scalar comparisons and formats mismatch strings only on refusal; V1–V31
retain their prior branch. The exact semantic and late-phase gates are opt-in
test work only.

**T40.13ab · Neutral-46 interruption disposition and fresh-ID fence**
*(small ceremony integrity · needs the retained neutral-46 controls)* — keep
neutral-46 as an unsealed operator-interrupted freeze. It entered Execute and
was interrupted before any teardown checkpoint or source-free observation;
there is no resumable or sealable receipt. Exact lock/process review authorized
deletion of only its private `custody/` unit. Frozen evidence, prepared control,
live-phase supervision record, operation lock, signer, and reservation remain.

AC: advance the permanent stopped-ID fence through 46 and first admit 47;
record the exact plan/freeze/signer and retained-control digests; forbid
re-execution, seal, or fabricated observation for 46; preserve V32 and every
product/resource bound; pass the focused driver, shell, documentation,
glossary, whitespace, and independent-review gates before exact-main
integration. Neutral-47 preflight/freeze and execution remain separate
boundaries.

Steady-state cost: one existing integer comparison changes its constant and the
test table adds one row. No production request, query, worker, retry,
publication, lifecycle/store mutation, child, lock, cache, or persistent state
changes.

**T40.13ac ✅ · Neutral-47 completed gate disposition**
*(2026-08-30 · documentation-only · needs the sealed neutral-47 package)* —
record the independently reviewed T40.13 mechanics pass without expanding its
claims. Exact source
`fb88c1d7fed7f32c1c3dd07303268366535cfa0c` produced V32 outcome
`completed` and substantiated decision `continue/all_exact_mechanics_passed`.
The signed source-free package is
`sha256:7130d80bd6c4b59ae8d4cfe0fdefd456d6287a6aef35781577b53ce2acb6c2e0`.

AC (met): independently recompute the package digest; verify the freeze and
evidence signatures, checksum inventory, and returned bundle; match the exact
source, plan, freeze, observation, receipt, manifest, and signer identities;
require all 12 phases and 16 checks to pass exact oracles, all 11 startups to
be healthy, all 14 waits to converge, and completed teardown to retain neither
derived nor scratch-source custody. The structural profile published 2,000,002
physical owners with 1,956/1,956 settled partitions and nine domains; the
semantic profile published 294,914 physical owners with 272/272 settled
partitions and nine domains. Interruption recovered the selected lease as
`requeued`; pressure, archive/restore, collection, and authorized query all
passed their frozen checks.

Outcome: T40.13 is complete. The receipt remains mechanics evidence only and
explicitly establishes no target SLO, service scale, accuracy, completeness,
migration, or decommissioning, and authorizes neither release nor private
rerun. Ben's separate 2026-08-30 disposition closes Epic 40, advances Epic 41,
and retires only the exact unheld host-pressure ballast after revalidating the
signed neutral-47 package. It changes none of those evidence nonclaims.

Steady-state cost: documentation only; no compiled, embedded, fixture, corpus,
harness, request, worker, retry, publication, lifecycle, store, child, lock,
cache, schema, bound, or persistent product state changes.

## Epic 41 · Ten-thousand-service authority and sparse consumers *(completed 2026-09-02)*

Raise logical-service capacity through segmented authority and bounded state/
publication design, not a constants-only change. The required floor is 8,000
accepted services; the planned accepted neutral target is 10,000, and an
accepted-only 12,500-service shape is the ceiling comparator. Total service
records across every disposition are a separate admitted dimension. The
large-monorepo diagnostic selected no service catalog and provides no evidence
for this epic.

### Boundary

- T41.1 creates a new schema/package/receipt and leaves the digest-bound T32.3
  1,000/5,000-service v1 generator and artifacts byte-identical. Accepted-only
  fan-out-20 profiles freeze candidate
  services/memberships/distinct-path tuples of 8,000/48,000/25,280,
  10,000/60,000/31,600, and 12,500/75,000/39,500. A separate small semantic
  profile owns proposal/conflict/rejected, successor, tombstone, and re-add
  cases so those records never distort the accepted-cardinality arithmetic.
- Service key, incarnation, desired/active identity, dispositions, role
  vocabulary, per-service path envelope, and accepted fan-out 20 remain exact.
  Aggregate total records, memberships, distinct paths, successor edges,
  logical bytes, and encoded publication bytes are independent caps. Total
  claims per placement remains separately bounded even when accepted fan-out
  is small.
- `phebs-service-catalog-v3` uses a small immutable root, service-key-range
  members, and placement-path-range members containing both accepted/
  nonaccepted claims and unowned state. The two member views must agree exactly.
  v1/v2 bytes remain readable; migration is side-by-side and lazy.
- Point service and path reads are member-local; pages read only intersecting
  members; full reconcile/relationship work streams each view once. No query
  repeatedly decodes a 10,000-service catalog and no state transition places
  every service in one database transaction.
- v3 ingestion stays runtime-dark until state, sparse backend, transport, and
  relationship consumers all exist. Partial reconcile is never authority for
  service reads; later activation may make individual services current under
  exact summary CAS fences. A successor catalog cannot skip an unsettled
  reconcile generation.
- Epic 41's closure uses a deliberately small at-most-50,000-regular-file
  physical corpus. Passing it is not large-repository evidence, a supported
  service count, target SLO, accuracy, release, or P6 claim. Epic 42 composes
  both dimensions.

**T41.1R1 ✅ · SCIP typed-range toolchain compatibility prerequisite** — migrate
the six current synthetic occurrence producers reported by the upgraded SCIP
bindings from deprecated repeated-int ranges to SCIP's typed range oneof.
Keep deliberate legacy-read fixtures unchanged and do not re-author retained
indexes, bundles, receipts, or evidence. AC: exact source coordinates and
reader results remain unchanged; current producers contain no lint-visible
deprecated range assignment; focused code-navigation, extraction, T20.1, and
T22.1 tests pass; repository lint and documentation gates pass; production
steady-state cost is zero; independent review and full merge bar. This repair
integrated as toolchain maintenance before Epic 40 closed and did not itself
advance T41.1 or Epic 41.

**T41.1 ✅ · Production-aligned 10,000-service profiles and cap decision** — add
new `t411-service-load-profile-v1` accepted-only 8,000-, 10,000-, and
12,500-service profiles using typed-requires-supporting and fan-out 20, plus a
separate small transition/authority profile. Reuse the deterministic identity
discipline of T32.3 without changing or copying its retained bytes; service
authority is explicit and never inferred from generated directories or import
edges. Shared/generated fixture paths are assigned to deterministic groups of
at most 20 accepted services rather than one global shared membership. Freeze
accepted and total service
records independently; exact memberships, paths, roles, successor graphs,
canonical bytes, encoded publication bytes, fixture content, total
claims-per-placement, and empty/mixed/dense relationship distributions. AC:
10,000 accepted services is the pass target and anything below 8,000 cannot
close the program; the 12,500 accepted-only comparator selects a measured hard
total-record ceiling or exact pre-growth refusal. Draft reduce-only caps are
12,500 total service records, 75,000 memberships, 40,000 distinct paths,
12,500 aggregate successor edges, 512 successors for one service, 16 MiB
logical canonical bytes, and 32 MiB encoded publication members; accepted
fan-out 20, total claims per placement 4,000, and existing per-service/path
limits do not increase. One maximum service combines maximum path and successor
bytes, and one maximum path carries the total-claim bound; either must fit its
2-MiB catalog member. The same maximum path must also fit the existing 1-MiB
relationship-projection wire; otherwise T41.1 reduces the total-claim cap or
selects an explicit bucketed claim representation. Two builds are byte-identical;
old T32.3 artifacts/digests are unchanged;
the receipt records RSS, wall, allocation, serialization, projection,
store-transaction, filesystem, and lifecycle estimates by byte kind; no
runtime registration or production cap change; dated PLAN decision and full
merge bar.

The reviewed 2026-08-25 measurement retains the reduce-only aggregate
limits and selects the 12,500 total-record hard pre-growth refusal: the
accepted-only comparator is 9,468,819 logical canonical bytes and 19,250,171
encoded bytes across 45 members, with a 9,193-byte root and 1,550,808-byte
largest ordinary member. The maximum combined service and placement members
are 158,158 and 1,528,421 bytes. A maximum two-sided 4,000-claim relationship
projection is 3,052,846 bytes and cannot fit the existing 1-MiB wire, so T41.1
selects `placement-claim-buckets-v1` with at most 512 claims/eight buckets;
the measured maximum bucket is 408,942 bytes. Epic 40's later closure removes
the dependency gate and the T41.1 merge bar is accepted. This ticket changes no
production constant or runtime registration. T41.2–T41.7 are integrated;
T41.8 is integrated. T41.8a's catalog-absence and exhausted-result recovery is
integrated; T41.9 is integrated. T41.10 is integrated at main
`d92b6673db6d4b582c2223536fe52358629ae60e` from exact
implementation `7a06e5dc24d1c9b5370ebf6111fd6aa926eb6b07` with canonical source-free
PASS receipt `sha256:e751ea4c16284a5f3e69e7b7dde3b2bcaa9274f242d1cf4914bc2757c3b2e680`
and closes Epic 41. T42.1 is integrated; T42.2 runner implementation is
authorized but held for the approved T42.1r1 contract correction. Ceremony
execution remains unauthorized and unexecuted.

## Epic 42 · Combined scale gate and topology decision *(V2 sealed; local integration authorized)*

Compose the independently proven physical and logical dimensions. A system
that handles two million files with no service catalog, or 10,000 services over
a tiny fixture, has not met the product target. Epic 42 proves that one shared
repository generation can support both without multiplying source work by
service count and makes the next topology decision from prospectively frozen
evidence.

### Boundary

- The gate uses only deterministic neutral/public inputs and source-free
  receipts. A private target replay remains separately authorized and cannot be
  smuggled into these tickets.
- The minimum combined pass shape is at least 2,000,000 regular-file physical
  owners and 10,000 accepted services, with explicit shared/generated/typed/unowned
  placements and bounded RPC/Kafka relationships. Logical memberships may not
  multiply physical indexing, Git reads, observation parses, or stored source
  bytes.
- Exact correctness, authorization, publication, recovery, lifecycle, and
  nonclaim gates outrank elapsed time. The plan freezes stop rules and host/tool
  identity before measurement; a failure selects a named follow-up rather than
  an in-run threshold change.
- This gate freezes source transitions and proves eventual bounded convergence;
  it does not generate or claim sustained commit-velocity, queue catch-up, or
  freshness-under-cadence evidence. That operating gate remains owned by a
  separately authorized successor to the stopped T39.2 operating gate.

**T42.1 ✅ · Combined gate freeze and deterministic corpus** — integrated
through closure `ea9dd555e5b19a752255fb099ae43721b4df971f`, with exact
implementation `8ca0d92410e3763b5c6c6664b26dc44ef2773edf`.
Canonical source-free `spike/t421/plan.json` is 199,561 bytes at
`sha256:96ba209147858c8f38b922fcaf8766dc6d796051d2e8b0999960ed2e114faf34`;
an independent second build and strict decode/re-encode were byte-identical.
The recorded merge bar and independent zero-finding review are preserved.
The later T42.2 call-graph audit below identifies execution-readiness defects
not established by those contract fixtures and bounded production replays.
This retained plan is not an exact-main execution freeze or a combined pass.

**T42.1r1 · Ordinary-path contract correction (V2 sealed; local integration authorized)** —
supersede, never mutate, the retained v1 plan. AC: production-derived identity
table and complete constructor-fixture acceptance; formula-based workload,
census, watcher, executable-image and preparation budgets; five phase-derived
server epochs with separately admitted config bytes; Go-only observation
inventory; result-preserving native recovery preparation and counterexamples;
real-server logical restart plus authorized search/caller/relationship reads
with no unexpected blob materialization; fresh independent exact-source review;
and a new canonical plan/digest. Production code and ceremony execution remain
out of scope. A future live catalog-reload feature is a contract-revision
trigger: this restart-based receipt cannot establish live-reload behavior.
The first real-server attempt exposed the native declaration-visibility defect;
the separately integrated T42.1r2 reader/cutover now closes that prerequisite
with an exact-source real upgrade and logical-restart pass. The original draft
is retained at `afcaef1d`; correction resumes on
`codex/t42.1r1-contract-correction` atop main `3b16f721`. The full constructor
witness remains unpassed. The resumed audit requires exact cold/B/return-A
schedule lineage, independently checked per-epoch startup timing, and recovery
preparation from genuinely completed files rather than modeled settled counters
over an unfinished generation. The separately reviewed T42.1r3 hook is now
integrated; wiring genuine worker completion into the constructor witness and
accounting for the hook's exact control reads remain. No new canonical plan or
freeze is ready.

The September 3 correction now wires actual worker/store completion and both
same-instance preparations; full acceptance is blocked on the proposed T42.1r4
production prerequisite below. V2
preparation records file/query attempts, cold opens and member visits, enqueue
write attempts, and the exact remaining scoped phase-control total. Existing
caps remain unchanged. Native 512-record projection-sort arithmetic supplies a
cold-read floor, not a measured event ledger. T42 execution readiness must still
prove the actual scoped event ledger and complete non-preparation inspection
budget, including cadence/retries; the old topology proxy is not that meter.
Constructor archive/injection execution and resource readings remain modeled.
No production, UI, retained artifact, integration, push or freeze changes.

Ben subsequently requested commit and local fast-forward merge of this
checkpoint despite the disclosed constructor failure. This integration
exception does not close T42.1r1, waive its remaining acceptance/review gates,
approve T42.1r4 implementation, or authorize a push, freeze or ceremony. Retain
the known-red regression and the original unmerged lineage at `afcaef1d`.

**T42.1r2 ✅ · Partitioned-native declaration reader and versioned repair** —
integrated locally through closure `529cb1d5e5d65274f41851d93ad2df6bfaf3e3fe`,
with exact implementation `7da08fb67b75087f8501e2d8990bfcba65d41b20`;
separate production prerequisite to the unmerged T42.1r1 contract correction,
formerly on `codex/t42.1r2-partitioned-declaration-reader`. Correct silent resolver
declaration omission for native staged/sealed domain publications without
changing writers, schema, legacy visibility, contract counts, or leaf scope.
AC: exact current-root/plan/run/candidate/repository fence on every bounded
page; not-found for missing/superseded initial authority and typed conflict
for invalidated continuation; no fallback-on-empty; visibility-faithful
resolver/replay doubles; real store empty/stale/quarantined/foreign/bounded
page coverage and superseded-root refusal; small real-server logical restart
plus authorized search/caller/relationship regression; steady-state-cost pass
and independent exact-source review. Validation and any upgrade disposition
must be explicit before branch closure. No ceremony authorization.

The 10,000-descriptor native replay and real store normal/race checks pass.
The corrected fresh-data ordinary-server regression passes in 100.25 seconds:
authorized scoped search, Caller Map, and relationships survive a logical
restart, with one census, zero source-blob commands, and zero index children;
the settled reader window contains watcher commands only. These are
working-tree observations, not an exact-clean-commit or existing-upgrade
proof. Four-file independent cross-review reports zero findings; external
review is partial (three files complete without findings, store reader
timeout). Ben subsequently approved the global `1.1.0` to `1.1.1` resolver-pack
cutover and its bounded resolver/caller rebuilds. The existing startup
queue-before-clear path owns that replacement. Its single exact-clean-source
upgrade regression now passes in 179.87 seconds: the actual pre-fix binary
produces zero declarations, 1.1.1 repairs the same data and all three authorized
readers pass. Resolver/caller identities change while source/search/catalog,
candidate, and extraction/observation authority stay unchanged. Upgrade work is five
blob commands and zero index children; later logical restart work is one
census and zero blob/index work. The fresh 10,000-descriptor replay and
canonical plan/receipt checks pass. Independent full exact-source manual
review reports `0/0/0/0`; external cutover review is complete with zero
findings, without reclassifying the earlier external store timeout. Cleanup
is verified and retained contract bytes are unchanged. Ben approved local
fast-forward integration; the clean temporary branch/worktree was removed,
with all commits retained on main. No push, deployment, freeze, or ceremony
was performed, and T42.1r1 remains the next contract prerequisite.

**T42.1r3 ✅ · Exact completed-generation recovery preparation (integrated)** —
separate default-inactive production primitive integrated locally through
`3b16f721783d443b57c76505ec37fa31a8bac5aa`; the T42.1r1 contract draft stays isolated.
Normal completed same-target reconciliation must continue reusing its settled
schedule. AC: a gated call on the same live reconciler/runtime validates exact
predecessor, authority, all completed result/bitmap/root controls and matching
store roots; holds the existing reconcile → selected assembly → exclusive
publication lock order; creates one native predecessor-bound schedule; and
preserves canonical results, sealed evidence, immutable/store authority and
pins. Checkpoint preparation changes only one completion bit and the selected
local current/root controls, with durable prefixes and no rollback on uncertain
enqueue. Require genuine completed real-store tests, stale/corrupt/canceled
refusals, bounded write/cost accounting and independent review. No ordinary
caller, API/CLI/environment transport, epoch, job reset, fallback or cap change.
This library primitive does not claim binary trigger arming or a ceremony pass;
the future admitted transport must use the actual live instance and arm before
new workers can pass the selected trigger. Corrected contract acceptance,
freeze and execution remain separate gates.

Exact implementation `0adada19bb06914aea901874a18268802e1ec939` passes complete
runtime normal/race, affected dependency/command gates, repository compilation,
scoped vet/pinned lint, module, glossary and whitespace checks. Independent
exact-commit manual review of all eight files reports `0/0/0/0`. The genuine
one-partition real-store regression uses native extraction and real scheduler
settlement; both preparations recover the same evidence/results without new
source work. Counterexamples reject foreign plans, stale post-enqueue authority
and coherently relabeled generation identities. The known baseline missing
UI screenshot is the only docs-check failure. This is neither binary-control
transport/trigger coverage nor a ceremony pass. Ben approved the local
fast-forward merge; the verified-clean merged worktree and branch were removed,
with all commits retained on main. No push, freeze or ceremony was performed.

**T42.1r4 · Bounded RPC namespace lookup (implemented; acceptance hold)** —
separate production prerequisite, not part of the contract-only T42.1r1 draft.
The native constructor reaches the ordinary RPC namespace cache, where the
frozen overlay requires 20,002 distinct protocol/import keys against a 16,384
limit. Of these, 10,002 are successful-empty lookups answered from the immutable
resolver root without member I/O; they still consume cache/read budget today.
AC: correct bounded lookup/admission at the shared production seam without
widening caps, reducing corpus inputs or changing exact outputs/root authority;
account negative probes, actual member reads and retained memory separately;
test many absent namespaces, positive-read boundaries, oversized-root refusal, exact output
preservation, corruption and cancellation; review policy/identity compatibility
and per-query/tick/startup/retry/publication cost. Existing runtime retains this
refusal as a closed same-target terminal failure: select and test an explicit
build-policy-target revision so old failures can recover, while preserving
historical publication reads, reuse and archive compatibility. Do not blindly
bump the RPC artifact policy. Require the ordinary full
component regression under native resident limits, independent review and
separate integration before resuming complete T42.1r1 constructor acceptance.
Ben approved implementation on `codex/t42.1r4-bounded-namespace-lookup` from
checkpoint main `a57a4bd9`. Both selected-V2 and ordinary catalog-V3 scheduling
must bind the operational policy change; preserve legacy binding readability
and shadow-V1 continuation. Exact-current direct-V3 publications remain reusable; old
failed/active direct schedules may use existing predecessor-bound replacement.
The new genuine selected-V2 publication check exposes an unchanged catalog-vs-
namespace generation comparison that schedules redundant work. Keep its actual
behavior and historical artifact readability explicit; that separate baseline
defect is not fixed here and supplies no V2 no-op claim.
Affected full normal/race and the native full-overlay component regression
pass; its two runs take 301.80s and 404.22s with unchanged counts and limits.
Independent working-tree reviews report no remaining findings in scope.
The host-permitted complete constructor clears native construction in its
926.30s run, then fails the modeled `pressure_80` row: its helper hardcodes
server epoch 2 instead of the corrected post-restart epoch 4. Follow up in the
contract fixture with phase-table derivation and a cheap V1/V2 pressure test;
leave the validator, production code and retained artifacts unchanged. This
is not a pressure execution or capacity finding. Complete constructor
acceptance and exact-commit review remain open; the known missing UI screenshot
is the only docs-check failure.
The same contract-fixture follow-up must isolate the unkeyed test freeze cache
by exact plan/commit inputs and check both V1/V2 orders. The constructor's
private binding helper assumes prior admission; require real freeze admission
so a cached wrong-version profile is rejected before expensive native work.
No main merge, push, corrected freeze or ceremony is authorized.

**T42.1r5 · Fixture epoch and exact admission (implemented; validated; unmerged)** —
test/document-only follow-up on `codex/t42.1r5-fixture-epoch-admission`, stacked
above local T42.1r4 checkpoint `1ba00f7d`. Derive modeled pressure epochs from
the admitted phase table, not a version-specific constant. Replace unkeyed
freeze/binding caches with one exact-plan/commit cache of publicly admitted
bindings; derive modeled tool provenance from those commits and isolate
returned mutable fields. Refuse reuse of the native authority/recovery witness
for a different exact plan. AC: both V1/V2 orders, changed input keys, copy
isolation, public admission refusal, and all pressure phases plus stale-epoch
counterexamples pass without native construction; then rerun complete
constructor acceptance and affected normal/race gates. Preserve retained V1
bytes, native identity derivation, oracle predicates, caps and production code.
The corrected complete constructor passes in 786.40s, both version orders pass
under race, and the final ownership/pressure race selector passes in 802.655s.
Working-tree source/documentation review has no findings; all 62 top-level
normal tests pass in 1799.598s, without failures or skipped tests. Docs-check
retains only the known UI screenshot gap. This is constructor-fixture evidence,
not an ordinary pressure or failure-injection run.
No new canonical plan, main merge, push, freeze or ceremony is authorized.

**T42.1r6 · Scoped native read accounting (implemented; validation recorded)** — separate
production prerequisite on `codex/t42.1r6-native-read-accounting` from integrated
T42.1r4/r5 commit `d776e1d7`. AC: default-inactive, operation-context-scoped
control-file attempts, native read-query/enqueue retry attempts and candidate
artifact/projection decoded visits; fixed source-free counters; failed-read
charging, independent metadata exclusion, cold/warm distinctions, isolated
concurrent scopes, bounded overflow and sticky invalid/late-event refusals;
genuine one-partition real-store preparation counts and existing preservation
checks; affected normal/race gates, steady-state-cost pass and independent
review. No ordinary callback/HTTP/MCP/full-inspector coverage is inferred from
the smaller native fixture. No public transport, production semantic change,
retained-artifact rewrite, phase-cap change, freeze or execution. T42.1r1 must
subsequently derive the complete prospective inspection budgets; its zero
warm/logical control-read caps cannot support honest inspection. Wider scope
coverage and the replacement contract remain open, not satisfied by this slice.
The genuine real-store preparation observes 13/14 control-file attempts,
nine read-query attempts, zero member visits and one enqueue attempt for its
two modes; preservation checks remain green. Complete ledger/candidate/
extraction normal and race packages, scoped store accounting normal/race,
repository compilation, scoped vet and pinned lint, module verification and
glossary pass. The post-enqueue accounting refusal preserves the committed
successor and checkpoint prefix without retry or rollback. Docs-check retains
only the known UI screenshot gap. No full store package, full repository runtime
suite, ordinary-server meter, corrected contract or ceremony pass is claimed.

The next bounded slice now carries that ledger across the authenticated real
HTTP boundary for `GET /api/extraction-progress` only. A separate closed exact
mode admits the legacy config principal and canonical sequential request
ordinals, applies server-owned limits derived from the production domain and
schedule-retry ceilings, and emits one identical source-free trailer/log
report. It is default-inactive and terminally refuses gaps, overlap, unsupported
authenticated marked routes, accounting overflow, incomplete handlers, and
report failure; anonymous headers on public routes cannot trip the latch.
The cumulative per-epoch call inventory remains open, so no smaller ordinal
quota is claimed yet. Authentication reads, other HTTP routes, MCP,
client-process inspector reads, phase call inventories and corrected budgets
remain open. The retained plan, receipt and current phase caps stay
byte-identical; no freeze or execution is authorized.

The final transport bytes pass complete command/extraction-publication normal
and race suites, the real-store HTTP-to-runtime regression, all-package compile,
affected vet/pinned lint, module, glossary, formatting and whitespace.
Independent review passes plus the final correction re-review leave no finding
at any severity. The only
docs-check failure remains the pre-existing UI-owned missing
`ui/receipts/fixtures/service-boundary.png`. Complete per-epoch request
inventory, other read surfaces and corrected phase budgets remain the next
prerequisite; this validation does not authorize a freeze or ceremony.

The next bounded r6 slice covers the ordinary candidate provider and observation
fence callback, plus direct extraction `Current`/`Status`/`Progress` inspections.
Native fixtures verify cold versus warm cache costs and scoped refusal; the
one-partition real-store Progress observation is three file controls and two
SDK reads. Ordinary preparation costs are now composed from those measured
components and the source call graph, not claimed as a full preparation replay.
Public-request attribution, full inspector coverage and phase cadence/retry
budget derivation remain open. No retained plan or phase cap changes.
The expanded seven-package normal/race, scoped store normal/race, compilation,
vet, pinned lint, module, glossary and whitespace gates pass. Independent
source/cost review is clear; docs-check retains only the known UI screenshot gap.

The next compact-inspector slice now derives every operational phase and all
five server epochs from the existing phase/deadline/epoch tables. It selects
health `H`, cheap exact extraction progress `X`, one final coherent authority
and semantic fence `F`, lifecycle status `L` only where lifecycle evidence is
required, transition-local reads `R`, and final HTTP/MCP pages `Q`; product
queries use `F,Q,F`. Five-second inspector attempts and 250-ms startup attempts
include one possible deadline-tie call. At that pre-T slice, the maximum
accounted server calls by epoch were 5,763/2,881/5,762/3,366/5,802, but this was
not yet the final
ordinal ceiling because `F/R/Q` native costs and protocol overhead remain open.
Stale-lease recovery keeps X active at the same five-second cadence until its
same-generation schedule is ready; it is not an exact-one inspection.
V2 execution admission now includes the closed exact-read mode, and the
post-auth wrapper admits lifecycle status with a zero-read ledger around its
existing in-memory snapshot. Current zero/255/4,096 read caps are explicitly
known incomplete and cannot be frozen; retained V1 and archive no-census reuse
remain exact. Focused inventory, plan round-trip, environment and exact-route
tests pass. Complete instrumentation, replacement phase budgets, full gates,
review and a new canonical plan remain required; no execution is authorized.

Source review then found two prospective-contract defects before budget
authoring. The compact graph was test-only and omitted archive/restore from its
transition-local `R` class; V2 now binds the graph, both cadences, every phase
row and each epoch aggregate by one canonical digest and rejects a changed
digest. The hidden-repository query also combined `all_code` with a repository,
which production rejects as malformed before authorization. Corrected V2 uses
the ordinary service-search selector and charges the real existence-hiding
path's one repository point read in each transport, with zero member,
service-runtime, generation or search reads. Retained V1 bytes and validation
remain exact. Build, focused V1/V2 plan, digest-mutation, selector/read-arithmetic,
receipt-mutation and compact-inventory tests pass; exact visible-Q and final
F/R costs, replacement phase budgets, full gates and final review remain open.

The source trace now derives the Q control planes without pretending its member
plane is complete. The exclusive plan-order HTTP-then-MCP corridor requires the
shared-current All-code reader. Startup's current service-runtime pin already
warms the shared relationship generation cache; the separate catalog root and
member cache stays cold until the first visible service detail. Therefore the
two transports compose to `C=160`, `S=164`, `K=324`, `W=0`. Those formulas and
cache/order predicates are digest-bound. A member visit is one validated
canonical record delivered or visited; rereads count, wrappers/cache hits do
not, and warm or empty visible results may be zero. V2 receipt validation now
admits that zero while retaining positive control work; V1 remains exact. Q
member charge sites, the full transport regression, F and most R readers remain
open, so these control formulas do not authorize revised caps or a freeze.

The follow-up contract audit removes two cross-process cache assumptions:
logical delta B and process restart each begin a new server epoch and therefore
cannot reuse the preceding epoch's immutable-member cache. F preserves its
activation projection through a bounded store authority read and a private
catalog cache isolated from Q. The single M atom is now the ledger's existing
application-record decode from a bounded immutable member payload, before later
framing/canonical/semantic or consumer refusal. The closed unit list covers the
native candidate, source, catalog, relationship, posting and caller-leaf
formats; roots, pointers, receipts/descriptors, response wrappers, derived
objects and cache hits are zero, while rereads count. V2 also names the F reader
as private exact-current authority instead of the bounded public product reader;
retained V1 keeps its historical label. The digest-bound inventory and mutation
regression carry those rules.
Exact F/R and Q-member totals, production charge sites and phase-cap replacement
remain open; the Q C/S formulas are unchanged and no freeze is authorized.
The V2 receipt predicate now rejects zero M on the first HTTP catalog miss or
either relationship transport, while requiring zero on the corresponding MCP
cache hit and other warm/empty cases. This supersedes the earlier same-day
validated-record wording; the decoded-application-record atom above is final.

The next r6 implementation slice places that atom at the shared native decode
points for repository source, caller leaves, service-catalog members, V3
relationship members, and RPC/Kafka posting members. Charges happen before
later framing/canonical/semantic refusal and before delivery or cache insertion;
physical rereads count, while controls, descriptors, wrappers, derived values,
current checks and cache hits stay zero. `CatalogContext` is the sole new public
primitive and keeps the existing full-catalog validation cancellable; `Catalog`
remains its ordinary background wrapper. This is leaf coverage only: exact F/Q
totals, routes, transport regression, phase caps and freeze remain open.
All affected current-source package suites pass normally and under race, with
repository compilation, scoped vet, pinned 2.12.2 lint, module verification,
glossary and whitespace green. Independent review found one catalog framing
ordering defect: whole-input preflight rejected trailing JSON before the typed
decode could charge its records. The shared decoder now bounds and preflights
the first value, performs the typed decode and charge, then applies trailing and
canonical checks; strict collection preflight still requires whole-input EOF.
The new trailing-value regression passes normally and under race, and exact
correction re-review reports critical/high/medium/low `0/0/0/0`. Docs-check
retains only the known UI-owned missing screenshot. F must use
`CatalogContext`, Q must use the catalog cache APIs, and the T42 relationship
path must stay V3-only; enforcing those call-graph bindings belongs to the
still-open F/Q transport slice.

The first bounded F foundation slice adds one private complete current-V3
relationship snapshot. It acquires and pins the exact current generation,
decodes every service and repository member once, validates the complete
cross-member joins, retains only the deterministic projections, and confirms
the current pointer before returning. Any later failure returns no partial
snapshot. Each physical call charges one C per service/repository member file
and exactly `service_count + projection_fragment_count` M visits; there is no
semantic result cache, so a reread charges again. Retained projections reuse the
builder's canonical-JSON-plus-512-byte charge and 512 MiB admission fence;
that is not a whole-process RSS claim. Ordinary publication validation passes
no collector and therefore gains no retained projections or semantic marshal.
Focused completion/ownership, exact-bound, final-member corruption, M-limit,
cancellation, and pointer-supersession regressions pass. Independent correction
re-review reports critical/high/medium/low `0/0/0/0`. The complete
relationship-publication package passes normally in 32.125s and under race in
207.095s; repository compilation, scoped vet/pinned lint, glossary and
whitespace pass. Docs-check reaches only the unchanged UI-owned missing
screenshot. The selected catalog and remaining F authority/authorization/
posting inputs, private cache, exact totals, handler, phase caps and replacement
freeze remain open.

The selected-catalog F1 foundation now reads a complete supplied exact V3 root
through every service and placement member, owns the member buffers, and uses
the shared `CatalogContext` validator so the cold path charges the exact decoded
service, membership, inherited-placement, and placement M atoms. It immediately
reduces the validated catalog to the eight source-free identities required by
the phase-state projection and discards the raw generation and catalog. A
private one-slot process-epoch cache is keyed by the full selected runtime
selector and is isolated from Q. A cold miss is only prepared: the later whole-F
reader must finish its reauthorization and final authority confirmations before
committing it. Failure or cancellation never inserts or displaces the prior
slot; a committed same-selector hit is zero M, and a new process is cold. The
shared five-set derivation remains checked by the independent authored-plan
oracle and an explicit A/B/A-return comparison of logical, semantic, source,
catalog, membership, placement, unowned-prefix, and service-query identities.
The frozen cold term is 101,605 M and the warm term is zero. Focused normal and
race cache tests cover no-early-insert, final-fence commit, selector-keyed
replacement, caller ownership, cancellation, limit refusal/retry, and epoch
reset. F1 adds no route and no ordinary or Q cache work. Initial/final
authorization, selector and selected-state reads, fresh root acquisition,
remaining F planes, exact S/C totals, handler, phase caps, ordinal ceiling,
replacement freeze, and execution remain open.

Independent review found one medium steady-state-cost defect in the first F1
projection: it repeatedly scanned the claims already accumulated for a path.
Because the input is sorted by service key then path, the corrected single pass
checks only the last claim. Correction re-review is critical/high/medium/low
`0/0/0/0`. Complete normal catalog/projection/command suites and the exact
focused race/parity gate pass; repository-wide compilation, scoped vet, pinned
lint with zero issues, module verification, glossary, formatting, and
whitespace also pass. The complete `spike/t421` suite exceeded a 30-minute
cumulative package allowance in the inherited Kafka relationship-component
filesystem sync; that exact test passes alone in 296.036s and leaves no test
process. Docs-check reaches only the known UI-owned missing
`ui/receipts/fixtures/service-boundary.png`.

F2 now supplies the complete private final-authority reader. One authenticated,
strictly ordered exact-mode GET authorizes first and coherently binds the
repository commit/tree, runtime selector, selected state, exact activation
plan/schedule/unit, catalog, search/source, observation, candidate, every
extraction plan/root, complete V3 relationship, resolver namespace, RPC/Kafka
components and caller publication. It then rechecks every mutable authority and
repository authorization. The source-side order-independent candidate proof
over path/object/declared-bytes/required must equal the proof reconstructed from
every physical candidate-member execution partition, so equal cardinality
cannot hide member substitution. Resolver validation opens every namespace
member; RPC and Kafka full walks must exactly compose the relationship
projection multiset by kind, plane, class, lookup key and posting digest.

The exact per-call formulas are
`C=29+3D+T+V_f+R_f+U+I_s+I_c+I_r+5I_l`,
`S=30+6D+3A+G*I_g+H`,
`M=V+E+R_m+Z+2N*I_s+P*I_c+K*I_g+J*I_l`, and `W=0`.
`V_f` is the V3 relationship service-plus-repository-member-file count and is
unconditional because each F validates the complete semantic snapshot.
`P` is one candidate repository-plus-caller artifact traversal, every input
record reread by the existing 512-record binary-carry and final-fold merges,
and one final projection scan. Whole-repository exact F admits no local
projections.
Empty-reader-cache cold is
`C=37+3D+T+V_f+R_f+U`, `S=30+6D+3A+G`, and
`M=2N+P+K+V+E+R_m+Z+J`; fully warm is
`C=29+3D+T+V_f+R_f+U`, `S=30+6D+3A`, and `M=V+E+R_m+Z`.
The derived no-slack request maxima are
`C/S/M/W=18,469/528/589,656,064/0`; the member ceiling includes the exact
`B512(2*candidate.MaxCorpusEntries)=313,296,640` merge-input term and
limit-plus-one refuses.
The receipt records actual cache posture, including any relationship cache miss,
rather than assuming startup warming.

Source and catalog misses remain private pending values. After all final fences,
the caller lease is explicitly and error-propagatingly released; only a complete
body and clean ledger allow the source/catalog pair to commit under the fixed
source-before-catalog lock order. Release is serialized with concurrent caller
publication transitions by the existing registry token and may wait for that
bounded transition or perform last-reader retired-byte cleanup; it is not
request-cancellable, and cleanup failure refuses F. Exact mode alone installs
the server root as `BaseContext`, allowing terminal exact failure to cancel
another active read without warming its caches. Ordinary mode retains the
original handler and nil root context.

Focused accounting, candidate-proof, component-composition, maximum/refusal,
source-free-shape, cancellation and cache-order tests pass normally and under
race. Complete `cmd/phebs` and resolver-namespace normal suites, the complete
resolver-namespace race suite, all-package compilation, scoped vet, module
verification, whitespace, and glossary checks pass; docs-check reaches only
the pre-existing UI-owned missing
`ui/receipts/fixtures/service-boundary.png`. Final independent review reports
critical/high/medium/low `0/0/0/0`. This F2 result does
not supply R/Q totals, replacement phase caps, the final ordinal, a superseding
plan/freeze, ceremony authority, a public product endpoint, release,
accuracy/completeness, supported scale, SLO, topology, migration or
decommission evidence.

Post-X review found that the single F call could still observe the prior
phase's complete authority while the next phase was legitimately building its
replacement. The corrected compact corridor inserts `T`, a private source-free
tail-readiness read, after X and before F. T freshly reads the selected runtime,
the exact V3 relationship pointer and immutable root, the exact resolver
namespace root, and the current caller summary; it compares the caller against
the resolver namespace's underlying resolver-catalog generation/manifest, then
reconfirms the relationship pointer and selected runtime. F uses those same
typed resolver-catalog identities rather than exposing or comparing the
enclosing namespace generation/root. If an immutable relationship or resolver
root disappears, T polls only when a second pointer read proves supersession;
an unchanged pointer naming absent bytes fails closed as corruption.

One ready T is exactly `C/S/M/W=4/4/0/0`. A deep attempt decodes at most the
existing 256-KiB relationship root and 8-MiB resolver root, hashes the bounded
caller current summary, and performs no write, child, new lock, persistent
cache, or invalidation. At the unchanged five-second cadence, a four-hour phase
admits at most 2,881 attempts. Adding T to every operational phase derives
five-epoch accounted-request maxima of
`11,529/5,763/11,526/6,254/8,689`; these are inspector request counts, not phase
native-read caps.

Current selectors deliberately remain on A until B publication exists, so T
cannot infer the phase target from current production state. The compact
inventory therefore hashes an explicit transition table against the prior
accepted F identity: logical B requires both relationship identities to change
while both caller identities remain equal; physical B and return A require both
pairs to change; warm, stale, restart, pressure, lifecycle, and product phases
require equality; archive accepts either a wholly equal or wholly replaced
relationship pair while retaining caller identity only after its existing
archive comparison R oracle completes. A counterexample proves the transition
predicate rejects old logical A. The future runner
must retain the prior accepted F identity and enforce this table; this branch
does not yet implement that runner.

Focused T normal/race, missing-root/supersession, identity-alias, phase-table,
old-A counterexample, inventory, contract, fixture-scope, split-member, and
whitespace checks pass. The first production-shaped attempt exposed an
impossible comparison between the relationship's nine-domain upstream digest
and the caller's two-adapter upstream digest; T and F now bind the shared
underlying resolver-catalog identity while each reader independently validates
its own upstream scope. The next attempt exposed a black-box-oracle error:
member partitions are execution partitions, so repeated backing members are
physically decoded and charged once per execution. The exact frozen structural
Go sentinel also replaces the non-candidate placeholder, and a cheap production
policy preflight now proves all nine frozen candidate counts before launch.

The source-equivalent production/exercised-regression real-server path then
passed in 465.56 seconds. Ready
T recorded `C/S/M/W=4/4/0/0`; cold F recorded
`10,763/126/415,836/0`; warm F recorded `10,761/90/251,021/0`; the
candidate/relationship/caller cache-miss tuple was `0/1/0`. The deterministic
late-selector race refused stale success, latched a nonzero terminal exact-mode
exit, and bounded cleanup left no helper or Surreal process. R/Q totals,
replacement phase caps, the final ordinal, superseding plan/freeze, execution,
merge, release, and scale claims remain open.

The next exact-tree F/Q bar corrects F's candidate strict-open arithmetic and
closes the real product corridor. Candidate work includes both direct input
walks, every 512-record binary-carry/final-fold merge input, and the final
projection scan; whole-repository F admits no local projections. The resulting
candidate maximum is 353,296,640 M and the corrected F request maximum is
589,656,064 M. Q remains exactly `C/S/W=160/164/0`; Q-M is the overflow-checked
plan-order sum of the HTTP and MCP `member_reads` retained by each query result,
not a frozen scalar. The real-server bar independently derives every page's M
from the selected immutable catalog/relationship/component member receipts.
Repository identity is part of declaration lineage and projection digests, so
otherwise equal repositories can pack the same projections into different
relationship members and produce different exact Q-M totals. No literal total
is admissible and no production defect follows. The two-server witness covers
cold F, all 38 query reports, warm F, late-selector conflict, terminal nonzero
stop, and bounded clean teardown. R, cumulative phase bounds, final ordinal,
corrected-plan freeze, execution, release, and scale/SLO claims remain open.

The physical-replacement R slice corrects the retained delete-A contradiction
at the production root. One ordinary A-to-B publication leaves B current and A
prior; both lifecycle turns are exact/drained with zero scanned and deleted,
and A is reprobed through the retained exact reader after only its separate
generation pin is released. The operation is one report with derived
`C/S/M/W=41/0/(2N)/0`; frozen N is 2,031,604, so M is 4,063,208. Its phase
R inventory binds that subtotal and epoch one's shared report inventory becomes
11,530. The generic phase read caps remain unchanged so partial R closure cannot
hide still-open inspector work. The production regression also proves the required order: pin
A before publication, then open A after B settles because publication changes
A's hard-link ctime. V1 bytes remain exact, no second physical pass is invented,
and all other R classes remain explicitly open. Exact-reader construction is
ten-minute bounded and holds one of two process-wide sessions until close; the
single R report runs serially. Cumulative phase accounting,
the final ordinal, replacement freeze, runner execution, release, and scale/SLO
claims remain open pending the completed validation record.

The logical-activation R slice binds the real offset-9 service-member commit
and its recovered successor to two source-free exact snapshots. A
default-inactive controller hook runs after the target transaction commits but
before its attempt-0 lease returns to the scheduler; repository-token `1`
prevents a next claim across that hook. The hit requires the prior selector,
its unchanged physical search generation, a running plan at chunk 10, an active
fully materialized schedule with nine
succeeded/one running, and the leased target. Recovery requires the final
selector, the same plan/schedule/unit identities, an activated plan, settled
all-success schedule, and a done, unleased, released/reclaimed stale-priority
attempt-0 target with internal failure provenance. The real-store regression
rejects malformed worker, lease, defer, claim/heartbeat/finish ordering, and
retained-error shapes. It also proves the real controller callback observes the
changed member only after commit under the transition lock, while a zero-row
same-lease replay cannot report. The regression refuses the released target as
a hit and the production intermediate
where plan/selector are final while the last schedule lease remains active.
V2 requires exactly one same-attempt requeue for the released target; V1 keeps
its frozen zero. The final activation-authority reader admits either clean
attempt-0 completion or this exact stale-success residue and rejects mixed
priority/error states.

Each snapshot performs an initial selector, exact-plan, immutable-schedule,
exact-unit, and final selector-confirmation query, so the two-report subtotal
is exactly report-scoped `C/S/M/W=0/10/0/0`; epoch two's report maximum remains
5,765 because the report count is still two. The forced V2 stop separately
pays existing pipeline recovery outside that subtotal: one scheduler release
transaction, one bounded claim-candidate read plus one later claim transaction,
one replay plan point read returning zero member rows and changes, one completion
transaction, and the controller's existing settled advance/no-op handoff. The
replay also creates the existing per-claim heartbeat goroutine, ticker, and
channel and emits configured lifecycle reports; its plan-point work normally
finishes before a heartbeat write and adds no concurrency class. V1 bytes remain
unchanged and other non-`none` R classes remain nullable/open. The default path
adds only the nil-hook branch.
An installed exact-control callback runs while the existing shared
filesystem-mutation lock, controller mutex, target lease, and repository token
remain held; its bounded read/report I/O must honor the operation context and
own timeout/report failure without retrying the report or transaction. The
later scheduler replay is idempotent and cannot re-report.
Cumulative phase accounting, final ordinal, replacement freeze, runner
execution, release, and scale/SLO claims remain open.

The return-A relationship-marker R slice binds the production publication
commit point and startup recovery point instead of borrowing the later F
result. Exact control reports the hit only after `publishing.json` owns the
complete target and the target rename/removal plus repository-directory sync
has finished, but before `current.json` moves. Its reader takes the current
relationship pointer, canonical marker, exact target root, current-pointer
confirmation, and marker confirmation. It requires the logical-B pointer and
marker to remain unchanged, the root to equal the marker pointer, and both the
marker-named stage and `publishing.json.tmp` to be absent. The stage check is
mandatory even when an identical target generation already existed: target
existence alone would otherwise admit the earlier marker-installed window
while the private stage still remained.

After restart, `RecoverV3` reports only after installing that same pointer,
removing any marker temporary and the marker, and syncing the repository
directory. The recovered reader repeats the same five-control shape against
the retained target identity and requires the return-A pointer to remain
unchanged, the marker to be absent twice, the exact root to remain present, and
the temporary to be absent. These independent confirmations make the reader
self-contained across the publication/recovery callbacks. Metadata probes are
charged zero but refuse symlinks, wrong types, residue, or I/O ambiguity. There
is no selector read: the service-runtime selector is still logical B at both
callbacks and moves to return A only when startup recovery has returned and
the controller later advances. There is likewise no caller, application-member,
or store read. The later accepted F remains the complete return-A authority
proof.

V2 records the marker output as target `Generation` and `Unit`, respectively
`marker.Pointer.GenerationDigest` and `marker.Pointer.RootDigest`. Its `Plan`
is the stable semantic work identity `binding.TargetGeneration`, while
`Schedule` is the actually claimed immutable row `chunk.ScheduleDigest`.
The production hook separately strict-validates the binding and requires
`chunk.Generation == binding.ScheduleGeneration`; it never aliases either
schedule identity with the published relationship generation. V2's
`AuthorityAtHitSHA256` is an exact mixed current authority: clone final
return-A authority, then substitute logical-B caller generation/root and
relationship generation/root/provenance before applying the existing authority
identity recipe. Retained V1 target mapping, hit authority, validation, and
bytes remain exact.

Each event snapshot performs five control reads and nothing else. Return-A R
is therefore exactly two reports with report-scoped
`C/S/M/W=10/0/0/0`; epoch three's shared report maximum rises from 11,526 to
11,528. The synchronous hit callback extends the existing relationship
filesystem-mutation lock, publication-transition mutex, claimed schedule
lease, and repository token. Its deliberate interruption or a report failure
leaves the durably owned marker and target intact, latches exact cancellation,
and terminates nonzero rather than retrying or advancing the pointer. The
recovery callback extends the existing exclusive startup mutation hold. A
recovery-report failure happens only after current installation and marker
removal are durably synced, so it cannot roll back or recreate residue; startup
fails closed, and a later restart sees no marker and cannot report recovery a
second time. Ordinary publication and recovery add only inactive nil-hook
branches—no ledger, allocation, I/O, goroutine, new lock class, or persistent
state. The reader is one-shot, source-free, nonpolling, uncached, child-free,
and write-free.

This slice does not implement the runner, replace the whole-phase sums or final
ordinal, freeze a corrected plan, execute a ceremony, or establish release,
scale, or SLO evidence. Stale-lease, process-restart, pressure,
archive/restore, and lifecycle R remain open. T42.2 stays next only after this
slice and the remaining T42.1 correction gates close.

The stale-lease R slice closes only
`prepared-stale-lease-schedule-and-result`. Exact control installs the
generation scheduler's default-nil pre-heartbeat gate so one selected
attempt-0 chunk can cross the existing stale cutoff without being refreshed.
The store's optional observer brackets the existing heartbeat-fenced durable
stale requeue: the hit callback runs immediately before mutation and the
second observer callback runs immediately after the successful requeue. That
second callback is synchronization, not recovery evidence. The recovered
report remains unavailable until the reclaimed same-attempt chunk completes
successfully.

Both event readers are self-confirming and source-free. Each reads the exact
preparation binding, extraction generation, domain plan, and canonical
partition result once from disk, then reads the current generation schedule
and exact target chunk twice from the store. At the hit, the attempt-0 chunk
must still be in an active current schedule at priority 0, running, claimed and
leased, with the same stale heartbeat selected by the reaper. At recovery, the
schedule must be settled with no pending, running, or failed chunks, and that
exact row must be priority 2, done and unleased, retaining the bounded
stale-reap provenance. The recovery-schedule generation is a distinct prepared
identity whose binding maps to the immutable target extraction generation and
predecessor schedule; the exact plan and result belong to that target
generation. The stale-lease phase authority must remain byte-exact return-A
authority; a mixed or moving authority, merely pending requeue, new attempt,
different schedule, plan, generation, or result refuses.

The V2 receipt therefore owns one hit and one recovered report, each exactly
`C/S/M/W=4/4/0/0`, for subtotal `8/8/0/0`; epoch three's shared exact-report
maximum advances from 11,528 to 11,530. V1 remains byte-exact and gains no
subtotal. Limit-plus-one, malformed state, callback failure, and incomplete
completion all fail closed. The readers are synchronous, one-shot,
nonpolling, uncached, child-free, member-free, and write-free.

Ordinary scheduler, reaper, and completion paths add only default-inactive nil
branches. Exact mode deliberately extends the selected stale window and runs
the bounded callbacks on those existing paths; it adds no global hook, store
schema, ledger, persistent state, goroutine, lock class, or production I/O.
Process-restart, pressure, archive/restore, and lifecycle R, whole-phase sums,
the final ordinal, corrected-plan freeze, runner execution, release, and
scale/SLO evidence remain open. This slice does not authorize T42.2.

The prepared-checkpoint hard-restart slice closes the prospective
`prepared-checkpoint-hard-restart` R reader/accounting contract. Exact control
installs one default-nil runtime hook after canonical-result reuse is validated
and before domain assembly. The old epoch-three process emits the hit report;
the reaper's store-owned Hit and requeue events remain private synchronization;
and the new epoch-four process may emit recovery only after the same attempt-0
chunk completes successfully. Neither reaper event is an R report. This ticket
does not provide the controller, runner, or hard kill.

Each event uses one self-confirming source-free reader. It reads the prepared
binding, exact target generation, domain plan, canonical result, completion
control, exact root attempt, and current pointer once, between two independently
confirmed current-schedule/exact-chunk store pairs. The hit requires an active
priority-0 attempt-0 row still running and leased, a durable canonical result,
the selected completion bit clear, and both root and current pointer absent.
Heartbeat renewal is intentionally excluded from the checkpoint fingerprint;
every fixed scheduler field and the opaque private lease-token identity remain
fenced. At recovery, the same attempt-0 chunk must be priority 2, done and
unleased in a settled schedule, with no retry successor and its final row token
cleared. The new-epoch controller privately compares the killed-token
fingerprint from the reaper Hit with the recovered callback's new-claim
fingerprint; the prepared hit fingerprint may remain in-process, and raw tokens
are never evidence. The canonical result bytes must be identical, and the
completion bit, exact root, and exact current pointer must all be restored.
Distinct old/new process identities and byte-exact protected return-A authority
are mandatory.

V2 maps the target generation to the immutable extraction generation, the plan
to that generation's domain plan, the schedule to the preparation recovery
schedule, and the unit plus selector bounds to the canonical result. The
distinct recovery-schedule generation remains bound to that target and its
predecessor schedule. The exact scheduler-row identity is derived from that
schedule, the target's generation-global partition offset, and attempt 0;
mixed identity roles refuse.

The two V2 reports each cost exactly `C/S/M/W=7/4/0/0`, for aggregate
`14/8/0/0`. The hit raises epoch-three requests `11,530→11,531` and transition
`calls/C/S` `4/18/8→5/25/12`; recovery raises epoch four `6,254→6,255` and
`0/0/0→1/7/4`. V1 bytes and semantics stay exact and gain no subtotal. The
seven charged controls include the expected absent root/current attempts at
the hit; four one-row store reads fence each report. The compact policy token is
`p2C7S4`. Its `meta=0` spelling only shortens the former `metadata=0` label to
stay inside the existing plan-byte cap and changes no semantics.

Readers are synchronous, one-shot, nonpolling, uncached, child-free,
member-free, and write-free. Each report takes the existing per-plan assembly
mutex once through its existing context-bounded acquisition around
completion/root/current inspection and releases it before the second store
confirmation and any report or wait; no new lock class is added.
Limit-plus-one, malformed or moving state, hook, reaper-synchronization, and
report errors fail exact mode closed. Hit failure
returns before assembly and preserves the prepared checkpoint. Recovery-report
failure occurs after successful completion and exact root/current durability,
cannot roll those facts back, and is surfaced by the scheduler report path.
Ordinary reused-result handling adds only one inactive nil branch and no global
hook, schema, state, goroutine, lock class, cache, retry, lock acquisition, or
I/O. Pressure,
archive/restore, and lifecycle R, whole-phase sums, final ordinal, corrected-plan
freeze, actual hard-kill/runner execution, release, and scale/SLO evidence
remain open. This slice does not authorize T42.2.

The next prerequisite corrects the prospective lifecycle inventory before any
pressure-cycle R claim. V1 retains its exact historical fourteen-owner bytes.
V2 adds the already-production `catalog-v3-generations` and
`relationship-v3-namespaces` owners to the sorted work envelope, making all
pressure and lifecycle cycles require the complete sixteen-owner rotation.
Missing either row now fails the shared receipt validator. The lifecycle R
class is renamed `fresh-sixteen-owner-cycle`.

No runtime collector or second authority is added. The two extra source-free
rows were already produced by the ordinary lifecycle controller; this changes
only the prospective contract. An equivalent compact physical/logical R policy
grammar recovers the needed plan space, leaving 262,101 bytes under the
unchanged 262,144-byte cap. Targeted retained-V1, corrected-plan, inventory,
missing-owner and byte-limit tests pass. Pressure, archive/restore and lifecycle
R, complete phase sums, final ordinal, replacement freeze, execution, release,
and scale/SLO evidence remain open.

The pressure-80 V2 R reader slice then adds an exact-only one-shot collector
over the existing serial `lifecycle.Run` owner and capacity callbacks. After
its phase-local fence it cumulatively admits at most 4,096 turns and retains
only scalar work totals, the final sixteen source-free owner rows, and final
capacity. Success requires one complete sorted sixteen-owner cycle with every
row `state=ok` and drained: all fifteen non-`durable-jobs` owners are exact,
`durable-jobs` is lower-bound with backlog false, and every paired capacity
sample is exact-normal, with the final one following the latest owner.
Ownerless, unpaired, malformed, out-of-order, overflow, and limit callbacks
fail closed. It does not consult
`StatusMonitor`, start another scanner, retain source or persistent state, or
perform a lifecycle turn of its own. V2 Unix-millisecond evidence may be
nondecreasing because the serial callback order is authoritative. V1 keeps its
strict historical timestamps, and the reader invents none.

After the later controller removes ballast and establishes its fence, the
pressure-80 reader makes exactly one direct call to the existing
`Gate.Check(ctx, 0)`. It requires exact collect pressure at 80%, the same total
capacity, increased used bytes, and decreased available bytes, without a
second lifecycle turn. The cycle and collect observations are exactly two R
reports, each `C/S/M/W=0/0/0/0`; epoch-four report calls advance `1→3`, its
requests `6,255→6,257`, and the overall request inventory
`43,770→43,772`. The compact token `;80=2xC0S0M0W0` leaves the plan at
262,115 of 262,144 bytes.

Ordinary mode remains unchanged. The collector is constructed only by later
exact mode and uses one mutex and one capacity-one result channel; it adds no
I/O, cache, child, schema, persistent work, source retention, or independent
scan. The pressure-80 read adds only its direct capacity metadata probe. No
current server wiring is claimed. The later controller/runner must fail if
lifecycle is disabled, arm and wake the existing potentially hour-idle runner,
and own the ballast and authority fences, event ordinals, and report path.
Deletion and prepared-residue proof; pressure-90, pressure-75, archive, and
lifecycle R; cross-phase sums; the final ordinal; replacement freeze;
execution; release; and scale/SLO evidence remain open.

The next V2 R slice binds pressure 90 to one source-free report from the same
production `Gate` that successfully reported pressure 80. After the
controller's post-ballast fence, the one-shot reader performs exactly one
direct `Gate.Check(ctx, 0)` and succeeds only on the typed
`ErrPressureRefusal` with exact refuse pressure at 90%. Total bytes must remain
the same positive pressure-volume total, used bytes must increase, available
bytes must decrease, and the observation time must not precede either the
pressure-80 capacity or the ballast fence. A missing pressure-80 predecessor,
different gate, cancellation, raw/untyped error, malformed or noncontiguous
capacity, or second attempt fails closed. It performs no lifecycle turn or
status read. Same-gate identity does not prove this direct call was the first
90% latch probe.

The pressure-90 report is exactly `C/S/M/W=0/0/0/0`. Epoch-four transition
calls advance `3→4`, epoch-four requests `6,257→6,258`, overall requests
`43,772→43,773`, and `;90=1xC0S0M0W0` leaves the plan at 262,129 of
262,144 bytes. Retained V1 remains byte-exact and keeps pressure-90 read
accounting absent. Ordinary mode is unchanged; the reader reuses the existing
pressure-80 collector and gate and adds only one direct capacity metadata
probe, with no cache, child, schema, persistent state, source retention,
goroutine, lock class, or separately scheduled work. Current server/controller
wiring remains open and must park the existing runner between pressure 80 and
90, prevent any intervening `Gate.Check`, own ballast/authority/event/report
fences, and latch report failures.
Pressure-75, archive and lifecycle R, cross-phase sums, final ordinal, and
replacement freeze remain open. This establishes no phase pass and authorizes
no merge, freeze, execution, release, scale result, or SLO.

The subsequent V2 R slice binds pressure 75 to three source-free reports inside
one exclusive pressure-80→90→75 corridor on the same production `Gate`. The
first direct check must retain the typed refusal at exact 75% after the target
ballast mutation, with the same positive total and a contiguous decrease from
pressure 90. After the remaining ballast is removed, the existing callback
collector is re-armed behind a fresh fence and must complete a new sorted
sixteen-owner cycle only after a paired exact-normal sample at most 74% precedes
its cycle start and the same-total exact-normal condition follows every owner.
An immediate cycle without that pre-start sample is rejected. The fifteen
non-job owners are exact and drained; the lower-bound `durable-jobs` row may
truthfully retain backlog. The third direct check must observe the same gate as
normal at most 74%, with nondecreasing time and no regression from the cycle's
final capacity.
Missing predecessors, a different gate, cancellation, raw/untyped errors,
malformed or noncontiguous capacity, callback-order failures, and repeated reads
fail closed. The helper proves same-gate identity but not exclusive control of
all calls; retained V1 stays byte-exact and keeps pressure-75 accounting absent.

Each report is exactly `C/S/M/W=0/0/0/0`; pressure-75's existing `L=1..241`
status-call bound remains separate. Epoch-four calls advance `4→7`, its requests
`6,258→6,261`, and total requests `43,773→43,776`. The token
`;75=3xC0S0M0W0` leaves 262,143 of 262,144 plan bytes, so any later R slice needs
separately justified compaction rather than more capacity. Exact mode adds two
direct capacity metadata probes and one bounded collector reset/callback cycle,
without a new scanner, status authority, cache, child, schema, persistent state,
source retention, goroutine, or lock class. Ordinary query/request, sync,
startup/restart, retry/no-op, publication, and lifecycle cadence remain
unchanged because controller/server wiring is still absent. That wiring must
fail when lifecycle is disabled, wake the existing runner, enforce the exclusive
same-gate corridor, and own ballast, authority, event, deletion/residue, and
report-failure fences. Permitted durable-job backlog proves no production runner
hourly-idle state. Archive and lifecycle R, cross-phase sums, final ordinal, and
replacement freeze remain open. This is no phase pass and authorizes no merge,
freeze, execution, release, scale result, or SLO.

The final transition-local V2 R slice binds archive/restore to exactly one
production `ReadArchiveTransitionManifest` report and lifecycle collection to
exactly one production `AwaitFresh` callback-cycle report. Archive reads and
strictly validates only the at-most-1-MiB manifest, confirms the digest used by
both archive commands, rejects omissions, and projects its component/report
inventory without reading artifact bytes. The lifecycle report is one new
sorted sixteen-owner cycle after archive/restore. Its fifteen non-job owners
are exact and drained; `durable-jobs` remains lower-bound and may truthfully
retain backlog, so this does not establish production hourly idle. Existing
archive and lifecycle semantic validation is unchanged. V1 keeps both read
subtotals nil. Every V2 transition result now owns a subtotal; product queries
remain exclusively under Q accounting.

Archive R is `1xC1S0M0W0`; lifecycle R is `1xC0S0M0W0`. The exact compact
policy token is `;80/90/75/lc=2/1/3/1xC0S0M0W0;ar=1xC1S0M0W0`: the positional
zip maps `80,90,75,lifecycle_collection` to `2,1,3,1`, and `ar` means
`archive_restore`; prior pressure semantics do not change. Epoch-five
transition calls become 2, control reads become 1, and requests advance
`8,689→8,691`; total requests advance `43,776→43,778`. The compact plan is
exactly 262,144 bytes, without raising its cap. Exact archive work adds one
bounded manifest read/allocation/parse and one bounded canonical
re-encode/SHA-256 of the decoded at-most-1-MiB manifest, with no
store/member/write/artifact read; exact lifecycle work adds one bounded,
zero-I/O callback cycle and no new scanner. Ordinary query/request, sync,
startup/restart, retry/no-op,
publication, lifecycle cadence, store/schema, cache, source/corpus/shard read,
hashing, disk allocation, child, and persistent work remain unchanged.
Controller binding, archive custody, destroy/empty-target ordering, exclusive
phase ownership, report-failure latching, phase passes, cross-phase sums, final
ordinal, replacement freeze, and execution remain open. This authorizes no
merge, freeze, execution, release, scale result, or SLO.

The next V2 slice closes the prospective whole-phase scoped read maxima. It
replaces only `ControlReads.Maximum` and `MemberReads.Maximum` in the existing
work-envelope rows, preserves their conservative minima and every unrelated
safety ceiling, and reuses the already-closed X/T/F/L/R/Q and recovery-
preparation inventories. `K` is C+S. The exact `K/M` rows are:
`preflight 0/0`; `cold 448266/589656064`; `warm_noop 19146/589656064`;
`physical_delta_b 448307/593719272`; `logical_delta_b` and `return_a`
`448276/589656064`; `stale_lease 448675/942952704`; `process_restart`
`448682/942952704`; each pressure phase and `lifecycle_collection`
`19146/589656064`; `archive_restore 448267/589656064`; `product_queries`
`38467/1628855928`; and `teardown 0/0`.

These are checked admission maxima for the explicitly scoped ledger, not total
pipeline-I/O predictions or observed costs. The construction derives X from
the frozen nine-domain profile and store retry ceiling, T/R/Q from the compact
inventory, F from its production maximum, and stale/restart preparation from
the existing one-shot bounds. Q-M is the checked plan-order route ceiling
`449543800`; a cold preparation admits the existing whole-repository candidate
maximum `353296640`. Equivalent policy abbreviations define `R:p/l` as
physical/logical and make the phase formula explicit. The larger numeric rows
add 56 plan bytes while that policy compaction saves 60, leaving 262,140 of the
unchanged 262,144-byte cap. Plan construction adds only bounded arithmetic over
the existing fifteen phases, query cases, and two preparations. No production
request, worker, storage, lock, cache, child, schema, or persistent work changes.
Runtime phase ownership, report-to-meter equality and failure latching, the
final ordinal, replacement freeze, execution, release, and scale/SLO evidence
remain open. This authorizes no merge, freeze, or execution.

The audited final-ordinal slice sets one accepted ceiling of 11,531 for the
shared API-and-MCP exact-read sequence in each live serve process/server epoch.
The five epoch request maxima are `11,530/5,765/11,531/6,261/8,691`.
Each fresh admitted process starts at ordinal 1; 11,531 is accepted and 11,532
is refused. The checked ceremony sum `43,778` is not a per-process cap. The
existing 20-byte input guard remains bounded decimal parsing only, and ordinary
mode is unchanged. Because this ceiling is derived from the already-closed
compact inventory, it changes zero canonical plan bytes or digest; the plan
remains 262,140 of 262,144 bytes. The controller must still reset only for the five
admitted launches, reject unmodeled restarts, bind the gap-free report stream to
phase meters, and route/reserve R ordinals. Replacement freeze, execution,
merge, release, scale, and SLO claims remain open and unauthorized.

Validation closes on exact source
`a2831a172c05fa8ff4852d780dd594509b601173`. The first aggregate invocation
mistakenly retained a 30-minute allowance and timed out at 1,800.546 seconds in
`TestPlanRejectsNoncanonicalSourceBearingAndMutatedInputs/ceiling` during
expected frozen-tree hashing. No assertion had failed before the timeout, no
process survived, and that command is not a pass. The repository-documented
`go test -timeout 60m ./spike/t421 -count=1` subsequently passed all 91
top-level tests in 1,802.076 seconds. The exact three-test tail passed in 19.904
seconds; exact focused `cmd/phebs` race and affected `spike/t421` race selectors
passed in 4.029 and 584.284 seconds. The 60-minute pass supersedes only the
inadequate allowance and does not relabel the earlier timeout.

Retained V1 is unchanged at 199,561 bytes and
`sha256:96ba209147858c8f38b922fcaf8766dc6d796051d2e8b0999960ed2e114faf34`.
Two separate-process unsealed in-memory V2 builds each produced 262,140 bytes
at `sha256:23a9daa56be7c7fd870bd729a8c099c0cedcd54ae9963032a07a809b53dbf944`
and passed strict byte-identical round trips. No author or seal ran and no
canonical artifact was written. Compilation, vet, repository-pinned lint with
zero issues, module verification, glossary, explicit five-script `bash -n`,
gofmt-diff, and whitespace pass. Docs-check retains only the known UI-owned
missing `ui/receipts/fixtures/service-boundary.png`. Cleanup found no Go, test,
SurrealDB, Phebs, or Zoekt process and no port-65499 listener. Independent
exact-source review reports critical/high/medium/low `0/0/0/1` for the cost
sentence only; the docs-only correction re-review reports `0/0/0/0`.
Validation is closed, but
artifact authoring, artifact sealing, and merge remain open and each separately
requires Ben's explicit request. This validation-only record changes no runtime
or retained artifact; no
freeze, execution, release, scale result, or SLO is established or authorized.

On 2026-09-04 Ben authorized V2 authoring/sealing and local integration of the
reviewed stack. The create-only author succeeded from clean source
`1b4456fe791b9e10816989f20fd1cdbcdb96c34c`: `spike/t421/plan-v2.json` is
262,140 bytes at
`sha256:2275b8cadca8f4e76a46db6d943380d1533a41da70a71c7009850e2c0229b422`.
Its source/supersession bindings, strict round trip, independent second build,
and unchanged V1 identity/round trip pass. Only `source_commit` differs from the
validated preview; compiled source and its recorded gates are unchanged.
Independent artifact/provenance review reports zero findings. This supersedes
the historical author/seal/local-merge holds above. Ben authorized local
fast-forward integration of the sealed artifact and reviewed stack; the
artifact and docs add no runtime cost.

**T42.2 · Combined convergence, recovery, and pressure execution** — run the
frozen corpus through ordinary production workers and retain a closed receipt.
Runner implementation was authorized on 2026-09-02 and branched as
`codex/t42.2-combined-ceremony`; no ceremony was launched. Runner implementation
resumes after the authorized local integration of sealed T42.1r1 V2. Its later
host/tool execution freeze follows implementation and review; ceremony execution
still requires separate authorization. The historical V1 contract
incorrectly requires resolver/caller identity replacement on a
logical-only change; permits at most 64 cold Git children despite 10,002
ordinary resolver blob children alone; assigns zero Git children to phases
with the required three-second Git watcher; and lacks a target-preparation
transition and publication-write budget for post-completion stale/checkpoint
recovery. Logical delta also demands 10,002 resolver reads with zero Git
reads/children. The owning 2026-09-02 PLAN decision records the hold. Retained
plan bytes, production identities, and caps remain unchanged. Native failure
controls, full server telemetry, private admission verifiers, and the signed
launcher remain unimplemented, not passed prerequisites.

AC: one shared physical source/search/observation generation per frozen
physical revision, each with at least 2,000,000 regular-file owners, serves all
10,000 services without a service-count multiplier; applicable extraction and
relationship roots become current; exact All code/service/relationship queries
match the oracle; cold, warm, small
physical delta, small logical delta, A→B→A, partial service failure,
interrupted publication, stale lease, process restart, backup/restore, reader
lease, lifecycle collection, and 80/90/75 pressure cases settle under bounded
work; measurements enumerate per-phase wall/RSS, children, physical/logical/
allocated bytes, store rows/transactions, member reads, cache behavior, and
reuse; every operational phase that observes an admitted server binds the exact
admitted execution profile, server epoch, process image, process identity, and
launch event; any stop remains a stopped receipt with later phases `not_run`;
no SLO or release inference; full recovery and merge bars. T42.1 completion
alone does not authorize this execution.

**T42.2a · Fail-closed generation-chunk telemetry** — first implementation
slice on `codex/t42.2a-exact-chunk-reports`, after integrated V2 `9ac960c9`.
Bind all three live generation schedulers only in T42 exact-read mode to the
existing synchronous sink and process-lifetime failure latch. AC: ordinary
and historical T40 reporting remain advisory; encode/cap/sink/panic failures
are bounded and terminal in exact mode; failed start reporting never enters
the chunk handler; settled failure preserves the actual durable outcome;
workers join before terminal error; focused normal/race, affected-package,
static and independent review gates pass. No retained plan changes. Real
checkout/tool and profile admission, controller/phase routing, remaining native
telemetry and failure controls, signed launcher, and readiness rehearsal remain
before an execution freeze. T42.2 and T42.3 remain open; no ceremony is run by
this slice.

Implementation `57198444b3df4dcdf22a51c576952e1064073601` is committed and
independently reviewed with critical/high/medium/low `0/0/0/0`. Full affected
normal and race gates, repository compilation, module verification, scoped vet,
repository-pinned lint, glossary and whitespace checks pass. Docs-check remains
red only for the existing presentation-owned missing `service-boundary.png`;
no fixture or merge-bar exception is invented here. The branch is unmerged.
Next implementation is real checkout/tool admission, then closed profile
admission and controller/phase wiring; no placeholder freeze CLI is supplied.

**T42.2b · Strict checkout observation prerequisite** — stacked on T42.2a in
`codex/t42.2b-checkout-tool-admission`. Measure externally selected commit/tree
lineage using bounded closed-environment Git reads; compare raw tracked bytes
and executable modes rather than trusting Git's stat cache or clean filters;
refuse hidden/unmerged, ignored/untracked, grafted/shallow and unsupported
inputs. Repeat authority/index checks at return. AC: real temporary-Git
positive/refusal cases, focused normal/race, compilation/static/documentation
and independent review gates. This slice deliberately issues no
`CheckoutAdmissionBinding`: exact-reference tool builds, executable provenance,
closed profile, remaining telemetry/native controls, controller and signed
launcher still precede freeze. No fake author/executor binary or ceremony
command is added. The slice is unmerged and changes neither sealed plan.

Exact implementation `5e8f8cf0e521f09d374d4896c35d8646209cd19b` passed five
focused normal repetitions (36.717s), focused race (9.709s), repository
compilation, module verification, scoped vet/pinned lint, glossary and
whitespace. Independent exact-commit review reports `0/0/0/0` across all
severity levels. Docs-check retains only the existing presentation-owned
missing `service-boundary.png`; no waiver or cross-track repair is made.
Both retained plan digests are unchanged. This is checkout-observation closure
only; next is real exact-reference tool admission, not an execution freeze.

**T42.2c · Exact-reference Go tool builds** — stacked on T42.2b in
`codex/t42.2c-reference-tool-builds`. Rebuild the four implemented Go roles in
private exact raw-source custody with independent Git metadata and a closed
offline environment; independently verify cached module/descriptor bytes
against source `go.sum`, and require complete binary and BuildInfo equality.
Recheck original/private source, SDK, tool images and module inputs, and clean
owned scratch custody on every return. AC: real source snapshot and Go-build
positive/refusal tests, ignored-source contamination, local-filter and ambient
control isolation, cache tampering, cancellation, focused normal/race, affected
static/compilation/documentation and independent exact-commit review gates.
No author/executor stub, private freeze binding, full tool inventory or ceremony
command is supplied. Remaining external-tool/profile/host admission, real
author/executor/controller, native controls, launcher and rehearsal still gate
freeze. The slice is unmerged and preserves both sealed plans.

Exact implementation `aa72212ef57151d6e0381c56818ccdad4b5a15d7` passed the
combined checkout/reference normal (32.697s) and race (29.967s) selectors,
separate build-metadata normal/race checks, and shared SDK-reader normal/race
checks. The real Go regression rebuilds a neutral focused-index package and
rejects metadata-identical ignored-source contamination. The opt-in repository
graph replay independently verified 483 module rows and 175 downloaded
directories, including frozen Buf/Zoekt sums (10.759s). Repository compilation,
module verification, scoped vet/pinned lint, glossary and whitespace pass;
independent exact-commit review reports `0/0/0/0`. Docs-check retains only the
known presentation-owned missing image, without a waiver. Full real tool
inventory admission, retained-plan suite replay, and ceremony remain unperformed.

**T42.2d · External image observation** — stacked on T42.2c in
`codex/t42.2d-external-tool-identities`. Observe all six V2 external roles from
explicit trusted Darwin/arm64 images: Git, Go, SurrealDB, hdiutil, ssh-keygen,
and sh. Screen bounded native executable headers and repeat contextual image
hashes. Use private closed read-only version probes for Git/Go/SurrealDB; reject
the Git shim by actual core-image equality. Fixed platform image-only roles
retain the honest `bound executable` descriptor and are never launched.
AC: real native positive cases, script/wrong-image and invalid-input refusal,
bounded output, closed environment, cancellation, focused normal/race,
compilation/static/docs and independent exact-commit review gates. This is an
image observation, not vendor authentication, native delegation/helper closure,
immutable custody, full tool admission or a freeze binding. Host/profile
admission, real author/executor/controller, remaining native controls, signed
launcher and rehearsal remain open. No plan bytes, main merge or ceremony.

Exact implementation `6ec99eb544c5c43a0399ad7097f3a8b961a00173` passed three
focused native-image/public-observer/probe repetitions (13.139s) and race
(5.933s), including all six real external images with explicit SurrealDB,
script/shim/wrong-role refusal, closed environment, stream caps, cancellation
and success/failure cleanup. Shared version and host-reader normal/race gates,
repository compilation, scoped vet/pinned lint, module verification, glossary
and whitespace pass. Independent exact-commit review reports `0/0/0/0`.
Docs-check retains only the known presentation-owned missing image, without a
waiver. The full retained-plan/corpus and store suites were not rerun for these
new observers and pure shared exports. Neither sealed plan changed; no full
tool inventory/private binding, integration or execution is claimed.

**T42.2 full-admission/runner hold (2026-09-04).** The full implementation
request exposed an unadmitted process-event capability: V2 requires every
descendant executable-image epoch, including same-PID exec transitions, not
sampled lower bounds. Existing snapshots miss short-lived processes; Darwin
kqueue aggregates events and does not supply recursive fork tracking. Direct
launch reports and known Git aliases do not account for every native helper.
Endpoint Security entails new capability/host authorization; the macOS-27
descendants API still requires its entitlement and is absent from the installed
macOS-26 SDK. Ben must route a separately admitted complete observer or a
prospective accounting-contract design/review. No new contract is authored or
sealed by this hold. Unfinished implementation was removed; T42.2a–d remain
intact and unmerged. Temporary native APFS checks resolved exact 96-GiB
geometry with `-layout NONE`, then detached and removed both diagnostic images.
This is no freeze, rehearsal, execution or Epic closure. The owning PLAN
decision records the evidence and unchanged authority boundary.

**T42.1r7 · Prospective V3 process-accounting correction (design/review)** —
Ben routed the full-runner hold to the V3 draft in `PLAN.md`, not a new native
observer capability. Exact controlled-dispatch admissions replace complete
native executable-image history; native process/RSS observations remain
explicitly sampled observations, not simultaneous peak bounds. Teardown attests
only its scoped owned-handle, lease, recorded-session, detach and exact-removal
facts. This narrows evidence, not functional authority, and preserves V1/V2
bytes and replay. AC: source-owned launch inventory and checked attempt
budgets; bounded fail-closed producer
transport and phase/hard-death closure; explicit V3 routing with no V1 fallback;
unchanged artifact caps with V3-only compact encoding; complete constructor,
failure/custody rehearsal, exact-tree gates and independent review. This draft
does not implement or seal V3. Complete admission/runner writing precedes a
separate author/seal decision; integration, exact-main preflight, host/tool
freeze and execution remain open. T42.2a–d and the historical hold are retained
on the local stack, not merged; `GATE2-V2` and `DO_NOT_RELEASE` stay unchanged.

Draft validation: only the four owning spine documents changed; glossary,
whitespace and retained V1/V2 SHA-256 checks pass. Docs-check still fails only
the known presentation-owned missing `ui/receipts/fixtures/service-boundary.png`,
without a waiver. No runtime, constructor, launcher or ceremony gate is claimed
for this documentation-only draft.

Implementation continuation (2026-09-05): the unsealed V3 constructor,
versioned measurements/receipt/runtime validation, checked operational attempt
budgets, bounded sampled native gauge and fail-closed local dispatch primitive
are implemented. Production launch sites remain unwired; ordinary runtime is
unchanged. The complete constructor/receipt/launcher acceptance bar above is
still open: actual producer/site/active/transport/stage/cadence limits, protected
dispatch, bootstrap, author/executor, private admission, scoped cleanup and
full returned-package/readiness replay must be completed before a separate
author/seal decision. This is not a freeze command or Epic closure.

Exact foundation source `2ac77b9d80ea57667c9553cd796710fe9762c769`
passed independent review with critical/high/medium/low `0/0/0/0`. Its clean,
unchanged full V3 constructor/receipt replay passed in 828.390s: the successful,
positive-incomplete-prefix and observed-RSS-overshoot-then-unavailable receipts
strictly round-trip at 330,512, 311,547 and 311,540 bytes. The fixture reuses the
actual retained V2 source/observation/extraction/relationship constructors;
process measurements, successful-start evidence, signatures and returned-package
bindings remain modeled. It is not live failure injection, a complete signed
package-size proof or launcher readiness. The unsealed canonical V3 plan is
165,038 bytes, below the 192-KiB authoring target; retained V1/V2 bytes are exact.

The exact-source historical/V3 contract selector passed in 161.986s, the full
V3 plan constructor/inventory/native race selector in 324.758s, and focused
accounting, dispatch and historical Darwin native checks passed normal/race.
Repository compilation, module verification, scoped vet/pinned lint, glossary
and whitespace pass. Docs-check still fails only the presentation-owned missing
`ui/receipts/fixtures/service-boundary.png`, without a waiver. Recorded fixture
processes, its owned process group and both database listeners are gone. No full
repository/store suite, real admission/launcher rehearsal, merge, seal, freeze
or ceremony is claimed. Production bootstrap and phase coordination, followed
by complete protected admission/author/executor/launcher wiring, remain next.

**T42.2f · Remote producer accounting coordination** — stacked on the reviewed
V3 foundation in `codex/t42.2f-production-dispatch`. Implements a bounded inherited
phase-control socket beside unchanged DA01 admission/settlement. Quiesced owners
use Pause-all → controller fence → producer checkpoints → independent semantic
checks → controller advance → Resume-all before reopening entry. Parked calls
receive no admission ordinal; settlement remains available; loss, malformed
frames, illegal order and deadline exhaustion fail closed. AC: real socket and
inherited-child handoffs, paused cancellation/waiter bounds, ACK-to-Start and
persistent-handle regressions, lost checkpoint/resume ACKs, strict protocol
refusal, affected normal/race/static gates and independent review. This proves
only accounting coordination, not worker/authority quiet or private admission.

Production bootstrap remains disabled: no existing issuer establishes immutable
tool/input custody for the actual command. Per-attempt whole-image hashing is
unbudgeted work and a writable-path metadata cache is not an immutable-content
proof. Protected admission and the sixteen production launch sites, actual
frozen transport/stage/cadence bounds, full author/executor/launcher and readiness
remain before author/seal or freeze. The implementation refines the unsealed
single-descriptor draft to two explicit bounded endpoints; retained V1/V2 stay
exact and no production behavior, integration or ceremony changes.

Exact clean implementation `676031a9749c7b484e3ba0bac64c9a606a654218`
passed independent source, documentation and cost review with
critical/high/medium/low all zero. It passed the full dispatch-admission package
ten times (5.267s) and race five
times (4.516s), including real inherited-descriptor custody and duplicate
receiver refusal. Selected V3 canonical/version-routing/runtime and typed
production-inventory checks passed in 0.766s. Repository compilation, affected
vet, pinned v2.12.2 lint, module verification, glossary and whitespace passed;
the retained V1/V2 plan hashes are unchanged. Docs-check remains red only on the
existing presentation-owned `ui/receipts/fixtures/service-boundary.png` link;
no UI change or waiver is included. The full constructor/receipt fixture was
not rerun for this isolated, production-unwired accounting slice; its earlier
exact-source result remains separately attributed above. Full production
admission and launcher readiness remain open, not waived by these gates.

**T42.2g · Protected direct-input custody** — stacked on T42.2f in
`codex/t42.2g-immutable-input-custody`. Create bounded fresh private copies of
direct images/fixed inputs, close every writing descriptor, protect their bytes
and directory entries, and hash only after protection. AC: real native copy
execution, write/rename/link refusal, source/copy isolation, read-only retained
descriptors, metadata/path/cancellation refusal, exact partial-custody retention,
descriptor-only close, focused normal/race/static gates and independent review.
Darwin immutable flags do not revoke pre-opened writers; they are not a defense
against hostile same-user flag clearing. Never flag installed tools or broad
user directories. Unsupported platforms fail closed.

This slice proves protected direct bytes, not full tool/input provenance,
closed native Git helpers, profile admission or bootstrap. Initial fixed inputs
and a later backup's publication-to-import custody remain separate. Release
read descriptors without thaw/deletion so the eventual launcher preserves
non-forced detach before source/data removal. Actual frozen flow limits,
production wiring and full author/executor/launcher readiness still precede
author/seal or freeze. Retained V1/V2 and `main` remain unchanged.

Exact clean source commit `6e24436e67d01ecde217f1cd38f43bf4eef2e3a6`
passed independent source/test/documentation/cost review with critical, high,
medium and low counts all zero. Exact-commit Darwin custody normal 10×
(6.343s), race 5× (4.652s), selected V3 canonical/profile and typed dispatch
inventory/budget regressions (26.000s), and dispatch-admission 3× (1.529s)
passed. Repository compilation, Linux custody-package cross-compilation,
affected vet, repository-pinned lint, module verification, glossary and
whitespace gates passed. The byte-ceiling regression exercises the real
remaining-byte guard with a small allowance rather than allocating a 2-GiB
fixture. Tests cleared only their independently held, identity-verified fixture
flags and verified exact removal; no fixture custody remains. V1/V2 plan bytes
remain unchanged. `make docs-check` remains red only on the existing
presentation-owned missing `ui/receipts/fixtures/service-boundary.png`; no UI
file was changed or failure waived. The complete corpus, full production
admission/bootstrap and launcher rehearsal were not rerun or established by
this isolated unwired primitive. This source-identical record grants no merge,
seal, freeze, execution or scale claim.

**T42.2h · Verified direct-tool custody** — stacked on T42.2g in
`codex/t42.2h-verified-tool-custody`. Connect real independent tool verification
to protected private copies: copy/protect first, run the existing verifier on
those bytes, then recheck custody before publishing a role-bound opaque handle.
Accept no caller-authored identity or verification assertion. AC: real tiny
reference-build and native external-probe success, metadata-identical wrong
image refusal, source/copy isolation, role/zero-value/drift/close refusal,
closed retained custody after failed verification, exact fixture cleanup,
focused normal/race/static gates and independent source/cost review.

This supports the four actual Go tool roles and copied SurrealDB direct images.
Real copied Go and Apple Git probes lost their GOROOT/helper locations while
protected metadata remained exact; both roles now refuse before copying until
their actual resource-location recipes are independently admitted. It neither
relocates/adopts Git's helpers or the Go SDK nor copies the three fixed-system
roles. Unknown/unimplemented roles refuse before work.
Existing reference/probe scratch and repeated input observations remain their
own bounded costs. No full twelve-tool/profile binding, source-free evidence
of native helper closure, production bootstrap, launch permission or launcher
readiness is issued. These remain required before author/seal or freeze;
protected direct-image custody alone does not waive them. V1/V2 and `main`
remain unchanged.

Exact clean source `3fd88e8225c59be3d6bd102718cf024a6ea6207d` passed
independent source/test/documentation/cost review with all severity counts zero.
The complete focused custody/reference selector passed normal (27.702s) and
race (33.622s), explicitly enabling the real 233,283,488-byte SurrealDB image's
version probes. The tiny committed Go fixture independently rebuilt and
executed its protected image and rejected a metadata-identical injected image.
No database server or ceremony was launched; exact FD-owned fixture cleanup
verified removal. Selected V3 canonical/profile, typed inventory and budget
regressions passed in 27.594s. Repository compilation, Linux package
cross-compilation, affected vet, pinned lint, module verification, glossary,
whitespace and unchanged V1/V2 byte checks passed. `make docs-check` still
fails only on the inherited presentation-owned missing
`ui/receipts/fixtures/service-boundary.png`; no UI file changed or failure was
waived. Full corpus/production admission/launcher readiness were not established
by this isolated connection. Ben's subsequent merge/push request remains held
at that documentation gate; local and remote `main` still match
`9ac960c95eb7783ee06525aa097e637333104284` and no stack branch was pushed.

Authorized merge-gate handoff (2026-09-05): Ben explicitly approved the narrow
presentation fixture repair and subsequent merge/push. Exact clean repair
`004148eaaac617b972a95bbbe10f277f21dc82b0` changes only the inert image target,
its regression test and the owning ADR; it reuses an existing tracked receipt
PNG, preserves the alt text and byte-identical sanitized preview HTML, and
emits neither an image element nor a source attribute. No renderer, retained
screenshot, asset or design-charter byte changes. Independent exact-commit
source/test/docs/cost review reports critical/high/medium/low `0/0/0/0`.
All 47 focused Markdown/preview/FilePage/receipt-manifest tests pass under
pinned Node 24.18.0, as do UI lint and TypeScript checks. An initial ambient
Node 26 run also passed but does not replace the pinned result. Documentation
and glossary gates now pass without a checker waiver. Extra exact-source race
checks passed: dispatch admission (1.821s), generation scheduler (1.382s),
T42.2 command telemetry (1.748s), native process records (1.674s), and the
combined checkout/reference/external-tool/protected-custody/V3 canonical,
full frozen round-trip/profile/dispatch-inventory selector (377.268s), with
real SurrealDB version probes explicitly enabled. Module verification,
affected vet, pinned lint, whitespace and retained V1/V2 byte checks pass.
These supersede only the fixture and local integration holds. The approved
fast-forward merge/push includes the reviewed partial T42.2 slices and unsealed
V3 accounting foundation, not full admission/bootstrap/launcher acceptance,
V3 author/seal, host freeze, ceremony, Epic 42 closure, release or a scale claim.
Hosted CI remains a separate post-push observation; no full store/corpus or
ceremony rerun is claimed by this fixture-only repair.

**T42.2 delegated completion sequence (2026-09-05).** Ben authorized agent
decisions and orchestration through the next exact freeze, including reviewed
integration/push and V3 author/seal when the existing gates pass. The lead owns
Git/shared spine edits, independent review remains non-authoring, and PLAN
owns the design and dependency order. Execution remains excluded.

- **T42.2i · Protected Git resource admission** — closed core/helper recipe,
  child-only environment and real protected local Git commands. AC: actual
  core/resource identity and custody checks; positive, drift, unsupported and
  cancellation tests; bounded failure custody; independent source/cost review.
- **T42.2j · Protected Go build-input admission** — exact SDK/module/source
  custody before reference execution. AC: real offline rebuild, independently
  verified module bytes, missing/tampered resource refusal, owned cleanup and
  explicit tree/build cost bounds; no mutable-root or installed-flag shortcut.
- **T42.2k · Admitted production bootstrap and dispatch** — private inherited
  authority, protected recipes and all sixteen owned launch sites. AC: real
  small Phebs/Surreal launch/native-command/stop rehearsal, endpoint isolation,
  ordinary disabled-path preservation, exact failure and phase-fence tests.
  The narrow rehearsal issues no full-tool freeze binding.
- **T42.2l · Real V3 author/executor and full private admission** — assemble
  existing phase/read/injection machinery and genuine checkout/profile issuers.
  AC: all fifteen phases and five epochs implemented, owner drainage truthful,
  actual flow-derived numerical limits, twelve real tools and no placeholders.
- **T42.2m · Signed launcher and custody closure** — finite outer-stage recipes,
  source-free return firewall and scoped lease/session/volume teardown.
  AC: real healthy and held-lease/orphan/busy-detach/path-replacement failure
  rehearsals, bounded complete packages, no successful seal after uncertainty.
- **T42.2n · Exact-tree acceptance** — complete relevant normal/race, static,
  docs/glossary, retained-version, real readiness and independent-review gates.
  AC: immutable source attribution, no open finding, explicit steady-state and
  admission cost record, no surviving owned rehearsal process/custody ambiguity.
- **T42.2o · V3 seal and exact-main freeze** — canonical author/seal after full
  acceptance, reviewed integration, exact-main preflight, real host/tool/profile
  admission and authenticated freeze. AC: independent signature/byte replay,
  retained V1/V2 unchanged, exact invocation and custody/expiry handoff; no
  ceremony execution, Epic closure, release or scale claim.

**Acceptance hold — whole-work accounting.** Two independent audits confirmed
that the unchanged phase-wide store contract includes ordinary archive/import.
Before the isolated headroom correction, full changed reconciliation/activation
transactions submitted 513/514 explicit records; current native restore can
still import batches of 1,000, exceeding the
512-row limit. Successful chunk totals are not native attempted-transaction
accounting. The native import path also has no admitted before-statement
census/ACK, so rollback/interruption can erase the distinction between different
attempted prefixes. PLAN owns the source-backed counterexample and the required
separately reviewed production restore/instrumentation boundary; no alternate
import, new format/tool/engine, raised bound or guessed counters are selected.
The single changed-state logical member-nine target itself fits and must not be
repacked. Full Go write-site coverage and other phase work-event collectors are
also missing. Complete executor/launcher acceptance, author/seal and freeze
remain unready regardless of passing partial custody/control tests.

An independent scope review clarified that this acceptance hold does not
require new blanket permission: Ben's existing delegation covers reviewed
scale-path correctness prerequisites preserving the actual frozen guarantees.
The isolated `codex/t42.2l-store-headroom` slice will preserve immutable
512-row members and the exact member-nine hook while reserving transaction
space for the plan and optional summary. Its merge bar includes prefix-specific
counts and summary digests, stale-plan/lease/selector/preimage fences, interrupted
prefix replay and exact submitted-row tests. A separate same-format native
archive replay design remains under review; no implementation or complete
phase-work accounting is implied by that design investigation.

The same whole-work review also proves a distinct source-level mismatch:
successful logical-B state reconciliation/activation currently needs at least
246 claim/state/completion write transactions, exceeding the frozen phase
maximum of 170 before other writers. The row-headroom repair does not reduce
that count. PLAN retains the derivation; a separately reviewed ordinary
work-elision design and complete phase fit remain prerequisites, with no
counter omission, boundary shift or raised ceiling authorized.

The state-headroom implementation and focused tests now pass independent
source review with all severity counts zero. Review caught and corrected a
near-maximum summary-revision refusal in the second prebuilt prefix; a nil-store
regression proves that refusal occurs before any first write. Pure payload,
target-shape and prevalidation selectors passed. The first native selector
failed in 13.354s because its fixture assumed a reaped priority-two lease would
be claimed before untouched priority-zero future units. The corrected fixture
uses actual claim/refuse/release transitions, proves future ordinals leave
the plan unchanged, and the identical native selector passed in 15.437s.
It covers prefix restart/repair/reap, cancellation, stale zero-update CAS,
activation lease loss, old-selector readability and removal refill. These
results are not a new full native member-nine rehearsal or exact-tree/store
acceptance; immutable-commit broader gates and final cost review remain next.

Exact headroom commit `99ab9547710c0f19fb6c4dc7b92e4876c1b0e53f` then passed
fresh independent source/test/cost/documentation review with all severities
zero. Its clean detached full-store gate passed in 1,213.721 seconds under the
existing 30-minute allowance; pure headroom race tests (3.522s), the pure fresh
activation-report race test (1.756s), store/command vet, pinned lint (zero
issues), documentation, glossary and whitespace also passed. Native focused
race and the real activation-report hook remain separate pending gates.
A further source audit proves
that no-removal alone would still leave at least 195 logical-phase writes:
126 state transactions plus 69 unconditional startup field overwrites. The
`codex/t42.2l-schema-batches` prerequisite now groups each of the five existing
DDL batches in its own native transaction while preserving migration ordering
and schema repair. AC: closed trusted-schema shape/count guard, fresh/populated
reapply, missing-index repair, failure/metadata rollback and reopen, retained
guard metadata retention, cancellation and no later migration after failure, scoped and
broader store gates plus independent cost review. No-removal and complete
phase-work accounting remain separate pending work.

The schema source/test/cost/manual review closed with all severity counts zero
after adding a four-minute cooperative test deadline below its five-minute
outer limit. One initial test command stopped at compilation during the
concurrent no-removal edit, before any database launch. On the subsequently
compile-stable working tree, the native schema selector passed in 11.727s:
fresh 490-result base transaction, idempotent reapply, populated late unique-index
failure, explicit THROW rollback, metadata retention on the same connection,
fresh connection and reopened engine, prevention of later migration, and
successful repair. Guard evidence is metadata retention, not guard invocation.
No database child remained. Pure shape/transport/cancellation tests passed
three repetitions. The clean detached exact commit
`e8dd1d789855509307ffbd5967a7e36be6247a79` then passed all schema selectors
including native atomicity/self-healing (10.427s), pure schema race (1.582s),
pinned store lint (zero issues), documentation (0.540s), glossary and whitespace.
The preceding exact headroom commit separately passed its native prefix
restart/removal race (18.819s) and real committed-activation hook normal
(15.861s) and race (29.360s). These complete the named focused headroom gates;
combined broader store/command gates remain next, not a ceremony or freeze pass.

The next `codex/t42.2l-no-removal` slice uses actual live-key membership to
avoid unnecessary removal-only scheduler turns for genuinely new reconcile
plans. AC: closed capped projection/candidate validation, exact create and
final transaction fences, real-removal fallback, no malformed-data fallback,
same-layout active/repair/restore behavior, candidate/summary/prior-plan races,
unchanged member-nine activation and prefix restart gates, normal/race/native
checks and independent steady-state-cost review. The first native selector
failed in 2.478s on an introduced extra SQL END before any state-member commit;
all four engines joined. After correcting the expression, the core 11-case
suite and restore-state test passed, while two test setups failed in 2.885s:
the real catalog publisher correctly refused an unsettled successor, and the
failure loop overclaimed an already-settled schedule. Corrected tests retain
that public publisher refusal, exercise stale prior progress only at the
private creation boundary, and stop on actual settlement. The complete new
selector then passed in 2.748s with clean child teardown, including actual
repair-layout preservation and the production restore-state/clear sequence.
No archive import was performed by that store-level restore test.

Independent review closed with all severity counts zero after adding a
four-minute cooperative operation context (capped at the outer deadline minus
one minute) through minimal shared test-helper variants; ordinary helper
signatures and independent cleanup remain unchanged. The timeout-only
correction passed three pure repetitions in 0.504s; exact committed native/race,
member-nine and combined broader gates remain next. Result limits deliberately
do not imply history-independent scans or predecode response-byte bounds.
No whole-phase transaction fit is established.

Exact no-removal commit `7bdf8ce6bd7599f4631208f9ab258c43d9a6b425` passed
the combined no-removal, prefix-headroom and actual member-nine selectors in
19.272s normal and 37.987s race. Store/command vet, pinned lint (zero issues),
documentation (0.513s), glossary, whitespace, module verification and repository
compilation passed. Its isolated exact-commit full-store run passed in
1055.691s; this is a store regression gate, not full parent readiness or a
ceremony result.

A separate three-file test-bootstrap correction now runs the previously
bypassed cleanup defers before os.Exit. Independent source/cost review reports
all severities zero. Actual uncached no-tests fallback-build gates passed for
compatibility (3.681s), indexer (4.239s) and search (3.943s); each fresh owned
TMPDIR was empty and all matching processes joined. Only those three verified
empty wrapper directories were removed. A read-only historical inventory found
437 matching roots using 6.876 GiB, of which five were created during this goal's
time window; none were deleted on timestamp/prefix evidence alone. This closes
normal-return temporary-tool leakage, not hard-death custody or the unchanged
120-GiB host backing-space gate.

The shared read-only idle-claim correction passed independent source/test/cost
review with all severities zero. Pure normal repetitions (five, 0.602s), race
repetitions (three, 1.620s), vet, pinned lint and whitespace passed. Separate
native eligibility/concurrency and transaction-diagnostic gates passed in
1.327s and 1.219s respectively, with both owned engines joined. All eight job
kinds, future-only and oldest-due selection, preserved selection-time
eligibility, actual lost conditional update, and 24-job/four-poller unique
drain were checked. Native scoped counters changed from baseline R53/W250 to
R54/W250 after the empty claim; SELECT, CreateJob and positive claim controls
then reached R55/W250, R55/W251 and R56/W252. Thus empty claims submit no write
transaction while positive claims preserve one read/one write. The bootstrap
W250 is not an already-migrated restart floor. Full attempted-write/row coverage
and exact-commit broader gates remain separate; no phase-budget pass is claimed.

Exact idle-claim commit `9817d0b96f3b73647f9e363ca08bc00068445c03`
subsequently passed its clean detached pure race selectors three times (1.727s),
store/command vet, repository-pinned lint (zero issues), documentation (0.574s),
glossary and whitespace. These close those scoped exact-tree gates, not the
still-open whole-phase collector.

The `codex/t42.2l-restore-clears` prerequisite now bounds the four offline
derived-state clears while preserving their native record-ID and writer-marker
semantics. Independent review caught one medium compatibility narrowing in
repository name/revision witnesses; raw CBOR now preserves the former native
comparison and arithmetic, including numeric/composite names and non-integer
revisions. The corrected native page/pairing gate passed in 2.312s, including
comparison with the actual former recipe and invalid-arithmetic rollback.
Corrected pure normal and race selectors passed three repetitions (0.527s and
1.652s), with vet, pinned lint and whitespace green. Fresh independent
source/test/cost re-review of helper `3694752c6a4793fa1ad3ab7c23acdbf7be5b2c44548a5ad633309212854d2530`
and tests `1104e9b86521441f2c17b6a20024f6e5510238c19a8e338cbd45ea7ecd1898f4`
closed all severity counts at zero. That native gate uses a schemaless fixture
to preserve malformed/raw-value compatibility; existing production-schema
restore-clear gates, exact committed checks and complete Restore remain separate.
All owned native processes/connections joined. Selected-state restoration,
native archive replay and durable whole-phase write accounting are not closed
by these four page recipes.

The unchanged corrected source then passed native page/pairing race (3.648s),
eight existing production-schema selectors (47.637s), and the nested resolver
restore-clear/queue-kind selector (1.179s). These cover generation clearing,
downstream projection reset/rebind, malformed caller pointers/leaves, inactive
writer refusal, malformed candidate clearing, candidate rebind and no-removal
after restore. All engines/connections joined. Independent review of the
owning PLAN/BACKLOG/manual cost and scope record reported all severities zero;
documentation (0.677s), glossary and whitespace passed. Exact immutable-commit
attribution and complete Restore remain the next gates.

Exact clear commit `4e2291350e07bf756ff74c6a624ee8dc7d3ca576` then passed
the clean detached focused normal/race selectors (2.618s/4.181s), eight
production-schema selectors (48.378s), nested resolver restore-clear (1.240s),
store vet, pinned lint (zero issues), documentation (0.561s), glossary and
whitespace. The checkout remained clean and every owned native process and
connection joined. This closes those exact clear gates only.

The following `codex/t42.2l-bounded-native-replay` slice implements ordinary
same-format protected restore replay for the proven 3.2.0 export subset.
Native feasibility first disproved two assumptions: aborted imports return
HTTP 200 with failed statement/COMMIT results, and a fresh import engine needs
namespace/database definitions before the first table. The implementation now
strictly validates every native result and submits those two real metadata
transactions explicitly. A fresh-scope proof passed in 1.443s. The corrected
native transaction gate passed in 1.514s, preserving all 513 values across
512/1-record units, ordinary guard behavior and rollback after a failed write.
Native completed counters are diagnostic, not attempted-prefix evidence.

The first full-owned replay failed before its first table on missing namespace;
the corrected complete owned-schema replay passed in 22.756s, submitting
716 definitions and 15 records in 718 archive units after the two metadata
transactions. It checked all 82 tables before repair and retained the neutral
repository after actual reopen. Both engines and all SDK connections joined.
Final source-only time-field parsing, terminal no-op and duplicate cleanup
corrections then passed pure normal and race selectors three times (2.558s and
8.312s), vet, repository-pinned lint and whitespace. The native result predates
those explicitly bounded corrections; independent final source/cost review,
actual complete six-artifact Restore and exact committed gates remain open.
Unsupported ordinary exports still use native fallback before submission;
passing an older Restore test alone therefore does not establish protected-path
coverage. Full exact-mode admission must refuse unsupported forms before target
creation and still needs genuine parent-retained SDK/HTTP attempted-prefix
accounting. No freeze, phase-wide ceiling pass or new archive format is claimed.

Final replay source/test/cost/documentation review reported zero findings at
all severities. A test-only bridge now makes the existing complete six-artifact
test recognize and hash its actual Create-produced database artifact before
ordinary Restore on the exact supported version/platform, closes that descriptor,
and repeats the existing six-artifact digest assertion afterward. This proves
recognized input plus deterministic source routing, not an independent runtime
branch trace; other versions/platforms explicitly log that replay is unselected.
Its small exact test diff passed independent review with all severities zero
and compile-only testing (0.488s). It adds one database parse/hash and one
six-artifact read/hash assertion to that test, no production work.
Documentation (0.630s), glossary and whitespace also passed. Complete immutable
candidate native/Restore gates remain pending; these review results do not
supersede them.

The clean immutable replay commit
`042621b7765dee79df637fa62cbdbbf5258dc0e4` then failed its complete recovery
package in 56.524s. The six-artifact test stopped at its new preflight assertion
after 15.75s: its actual owned export contains an unsupported literal delimiter
at byte 16,617, before import began. The later focused backup/restore test
passed, but cannot supersede that failure. All native/SDK processes joined and
the detached checkout remains unchanged. The next correction must isolate and
native-test that real export form; neither permissive parsing nor silent
fallback is a protected replay pass.

The source-identical diagnostic reproduced that refusal in 15.771s and retained
the actual private native export. It proved an unquoted digit-leading
alphanumeric generation record ID, not an arbitrary SQL expression. The narrow
`codex/t42.2l-native-record-ids` correction reuses the literal identifier-tail
scanner after its numeric prefix. Pure normal/race selectors passed three
times (2.425s/9.027s), and the exact retained export preflight recognized
752 units, 716 definitions and 1,109 records. The corrected native string-ID/
value and rollback probe passed in 1.29s; complete six-artifact backup,
recognized preflight, ordinary Restore and startup exact-search recovery passed
in 38.88s (combined command 40.739s). All engines/connections joined; vet,
pinned lint and whitespace passed. Independent source/test/cost review reported
all severities zero. Temporary diagnostic code was removed; the private export
remains retained for audit. Exact immutable-commit full-package gates and owning
documentation review remain next, not a whole-ceremony or store-meter claim.

The owning digit-ID PLAN/backlog/manual record then passed independent review
with all severity counts zero; documentation (0.628s), glossary and whitespace
also passed. Corrected immutable-commit gates remain next.

Exact corrected replay commit `2801f55d2923e9823bdcdd492df2622217dfc0ee`
then passed the complete recovery package with native import probes enabled:
normal 82.330s and race 87.837s. These include actual six-artifact backup,
recognized replay, ordinary Restore, startup exact-search recovery, focused
publication-byte preservation and refusal tests. Vet, pinned lint (zero issues),
documentation (0.561s), glossary and whitespace passed in the clean detached
checkout. This closes the corrected scoped recovery regression gates, not
whole-phase accounting or complete executor/launcher readiness.

The next `codex/t42.2l-store-failure-truth` slice selects the existing V3
failure surface for an atomic incomplete-store metric family and a closed
typed store-submission refusal. PLAN owns the exact linearization decision:
prevalidation/budget refusal does not fabricate an attempt or cap+1; an actual
source submission committed by the parent remains in its positive prefix even
if its ACK/native reply is lost, without claiming native start/commit.
AC: V3-only all-or-none store family, preserved primary evidence and positive
counts, truthful reduction with incomplete resource evidence, unchanged
substantiated topology precedence, clean-teardown refusal and passed-phase
rejection, retained V1/V2 and numerical/measurement contracts unchanged,
focused normal/race and independent source/cost review. Implementation and
exact-tree gates remain pending, and this is not the complete SDK/HTTP collector.

The scoped V3 validator implementation passed final focused normal and race
selectors three times (0.936s/5.599s), vet, repository-pinned lint and whitespace.
These are section-validation and canonical-wire regressions: they preserve a
positive 170-transaction/512-row-maximum prefix, reject partial families and
invented cap-plus-one work, preserve a primary resource crossing and independent
topology precedence, and require failed teardown checks for missing store
evidence. They are not a full constructor or native collector gate. Final
independent source/cost/manual review and immutable-commit checks remain pending.

Fresh independent review of the exact V3 source, tests and owning PLAN/backlog/
manual record then closed critical/high/medium/low at zero. It confirmed the
atomic three-name family, ten-name accepted list, six closed reasons, exact
88-byte secondary evidence and 28-byte unsealed trigger additions, retained
primary/positive evidence, historical routing and exact teardown checks.
Immutable-commit gates and actual transport/coverage remain separate.

Exact store-failure commit `404f2888b06dcf66f86b8a72c958f4700db9237c`
then passed clean detached focused normal/race selectors three times
(0.592s/2.216s), vet, pinned lint (zero issues), documentation (0.526s),
glossary and whitespace. The checkout remained clean. This closes the scoped
immutable V3 failure-vocabulary gates, not the actual store submission channel.

The same immutable store-failure commit separately passed retained V1/V2
canonical-byte/validation selectors (11.247s) and the updated unsealed V3
canonical contract selector (0.495s). The retained plan digests remain exact.

The isolated SDK-control diagnostic first refused real native metric labels
before any operation (1.145s). After admitting those observed labels, its
BasicAuth scrape-only read control changed 9 to 10 with writes fixed at 14
(1.352s); actual bearer authentication reproduced that read contamination
(0.982s). Neither run reached causal SignIn/Use measurement. All owned engines
and connections joined. The next explicit write-only probe preserves those
failures, reports read attribution unavailable, requires unchanged writes on
both sides of each operation and checks target existence independently before
target SQL. Independent source/cost review of corrected test hash
`c8edd9e658dd19e5e93740df5ab279ff5b639c691d4fc677c6db844a10f77eac`
reported all severities zero. Its pure normal/race selectors passed three
times (0.474s/1.575s), with pinned lint and whitespace clean. A prior low
cleanup-reserve finding was corrected to one minute and independently closed.
Owning documentation review and one immutable-source native diagnostic remain
pending; no write-free SDK control classification or complete meter is claimed.

Independent owning documentation review also closed all severities at zero.
The eleven windows and 46 GETs count explicit diagnostic work only; native
startup/version/readiness and SDK connection-bootstrap traffic still occur
outside those windows. No native diagnostic result is established by this
review.

Exact clean SDK-control commit
`758639fb0194cb5ccf86b1ef9eaf77e9c00b2ea7` then passed the write-only native
diagnostic (1.385s) and one unchanged confirmation (1.265s). Both retained
flat-write scrape controls around all eleven windows: fresh root SignIn added
one completed native write-mode transaction, fresh Use added five, and repeat
fresh Use added two. Independent ROOT inspection immediately after fresh Use
proved the namespace existed. Existing root SignIn added one, existing and
repeat Use added two each, and all five measured INFO operations added zero.
Read attribution remains explicitly unavailable. These are completed native
KV-mode observations, not SQL transaction, submitted-row or hard-death
attempted-prefix accounting; one RPC must not silently stand in for all native
work. All owned engines and connections joined. The same immutable tree passed
pure normal/race selectors three times (0.632s/1.638s), vet, pinned lint,
documentation (0.563s), glossary and whitespace; its detached checkout stayed
clean. The production store collector still requires a defensible unit and
control-submission boundary before implementation can establish completeness.

The shared extraction-pin acquisition correction now checks an exact existing
pin and its still-valid canonical staged/sealed/nonquarantined run in one
read-only query. It preserves the initial pin timestamp and existing unrooted
protection; absent/mismatched pins retain the original fenced UPSERT after one
extra read. Cancellation, read errors and malformed response counts refuse.
All publication/recovery callers and retention consumers were traced; existing
mutation locks and explicit unpin ordering are unchanged, with no timestamp
TTL introduced. This removes repeat pin writes at their common source, not by
skipping startup recovery. The first native check failed in 1.403s because its
expected-one count for the original UPSERT was unproven; it never measured the
repeat. It does not prove the initial scalar lookup caused native writes. The
revised top-level SELECT returns and verifies at most one exact native pin ID;
controlled native normal/race checks (1.395s/2.659s) measured lookup zero,
original UPSERT four and public reacquisition zero completed native writes.
These KV observations do not convert one logical transaction into four.
Pure normal/race selectors passed three times (0.582s/1.723s), the existing
exact accounted-publication compatibility case passed (1.290s), and vet,
pinned lint and whitespace were clean. Independent source/test/cost review of
production hash
`9b8376a33e318c600ea6fbadd63fc9418854df6bbe626d9cc2b9a2b62b14855f`
and test hash
`aadc0c752d18e474f8ce5b75fa76ff79d54e1098da2ce2ad1d2f740b6cfc3648`
reported all severity counts zero. All owned engines and selectors joined.
Immutable-tree and owning documentation gates remain pending; no complete
phase-budget fit follows.

Exact pin commit `c463050504e4dad7d21642bb4d546a17f40716a7` then passed
detached-clean native/pure normal (1.406s), race (3.000s), existing exact
accounted-publication compatibility (1.131s), vet, pinned lint, documentation
(0.551s), glossary and whitespace. Its checkout stayed clean and no engine
or selector survived. The owning guide's one low query-versus-record cost
phrase was corrected before that commit; exact documentation re-review closed
all severity counts at zero. These close the scoped pin gates only.

The supervised-local initializer now owns explicit namespace and database
definitions through the same pinned SDK. It sends genuine null for the
namespace-only selection; final Use selects the already-defined fixed scope.
Generic remote/borrowed Open is unchanged. Independent source/test review at
`ddc53912331c85bb0f3ff22b42db1cb0b6f32c90eba809e7b5cb3c95bbee98bb`
and `fca03dcdb39eda670d593ba5eaa6cc0b5850c1c728407ddd02c6d40abc325f9a`
closed all severity counts at zero after adding a pre-child cancellation check
to the bounded native test. Real local initialization/reinitialization passed
in 1.320s, preserving a neutral repository and SDK Begin/SELECT/Cancel support.
The final isolated native handshake/Begin probe at
`08f8cb55b1492eb2bb1898411f149e5bebb46a255ad532265d50b5af765cc80e`
passed in 1.351s; absent auth produced native NotAllowed and wrong auth an HTTP
401 handshake refusal. It retained the earlier 1.379s SQL-fixture failure and
1.586s corrected intermediate pass. All owned processes joined. PLAN records
the actual source recipe, three extra local RPCs/two definition attempts and
the diagnostic's distinct completed-KV semantics. Final owning documentation
and immutable-tree gates remain pending; the real store collector and freeze
are not established by these prerequisites.

Exact local-scope commit `930b7b1817514fd0fd204f9eae73e627a3568fb6`
then passed detached-clean local initialization and native handshake/Begin
selectors in normal (2.093s) and race (4.513s) modes, vet, pinned lint,
documentation (0.553s), glossary and whitespace. The checkout remained clean;
all owned engines and selector processes joined. Independent owning
documentation review closed all severity counts at zero. These close only
the local-scope and diagnostic gates, not the full store accounting boundary.

The bounded store-submission reducer is implemented independently of its
forthcoming transport/SDK integration. It retains only fixed per-phase counts
and live slots, preserves every accepted source attempt on failure, counts
zero-row explicit Begin, enforces cumulative 512 rows per transaction and
refuses live transaction carry across phases. Rejected descriptors and budgets
add no invented counters. Global numeric tokens distinguish producer lifetimes;
uncertain native completion retains reservations and latches incomplete.
Independent source/test/cost review at
`857f6a0f9c6a2ffde804f02f4096cdc38953e417b30abfb9c3c7814cfc161b85`
and `e4ccd4a62cdfa5e0547e3b9b231d304a9cc1c31b83b2bd9138066247d46802ed`
reported all severity counts zero. Normal twenty-repeat/race five-repeat gates
passed (0.238s/1.248s), with a second root run (0.360s/1.298s), vet, pinned
lint and whitespace clean. Its policy inputs are not genuine frozen admission;
the parent channel, whole SDK-call drain, complete source descriptors, runtime
work collectors, exact-tree gates and freeze remain open.

Exact reducer commit `a09d725b2fc3d6479fad7b70085e343c1f0f6506`
then passed detached-clean normal twenty-repeat (0.254s), race five-repeat
(1.286s), vet, pinned lint, documentation (0.522s), glossary and whitespace
gates. Independent owning documentation review reported all severity counts
zero. These close the pure reducer slice only; authenticated channel and
native source descriptors remain unimplemented acceptance prerequisites.

The online v3 state writer now reserves actual missing selected preimages and
their summary alongside current-state/plan/changed-summary payloads. The
existing selected 512-service transition demonstrates the original gap:
511 current UPSERTs plus 511 preimage CREATEs, one summary preimage and one
plan target submitted 1,024 records. The corrected shared writer preserves
the same member and creates at most three exact-payload prefixes, repeating
target predicates before any mutation. Independent source/test review at
`028caf88baf5a75da9c9470cafceb7399d9654c77f864b59270e93e908660103`,
`32a3cf2cc9faca997f7da0ab83a05942861b5bd4c978e32439a004de9f7f3c15`
and `bbcce09d340358721c09faa4abef1c893e241b9733e93c7a56e6f8567a2e3c6a`
reported all severity counts zero. Pure packing/prevalidation passed in
0.849s. One bounded native engine exercised dense selected reconcile,
activation and removal, actual preimage inventories, omitted/stale target
refusal, cancellation and genuine new-lease continuation: test 4.57s,
package 5.487s, with all owned processes joined. Native race then passed in
12.912s (test 11.26s), with clean process teardown. Final self-review tightened
the four-result census to require actual arrays, not NULL absence evidence;
its SDK-codec regression rejects null/missing/extra/unknown-status envelopes.
Corrected source `30fff2b6e9d6f14b917d7d6bda1940c9b09db9f4d3019cf5f02da552936b4916`
and test `6299433a41ee8666aca796259dd7baf50b9e7bc0d619fca512a0445a3c2885ed`
passed pure normal three-repeat (1.324s), race three-repeat (11.478s), vet,
pinned lint and whitespace. Independent owning documentation review closed all
severity counts at zero. Corrected native, unchanged headroom/member-nine
compatibility and immutable-tree gates remain pending. PLAN records repeated
planning, metadata census and extended existing-lock costs; this closes no
whole-phase 170-transaction fit or freeze.

Exact selected-preimage commit
`74dda9d2b88141e918572f90e60899e67efd2b8f` passed the complete selected
preimage/payload and ninth-unit compatibility selectors in 24.312s normal and
51.680s race from a clean detached checkout. Vet, pinned lint (zero findings),
documentation (0.637s), glossary and whitespace passed; the checkout remained
clean. These are scoped immutable-tree gates, not a full store-package,
phase-wide accounting or ceremony pass.

The SA01 store-prefix transport is implemented over the existing owned socket
primitive. It binds each mechanical producer and phase with a unique nonce,
retains parent-accepted submissions before ACK, refuses malformed/replayed or
uncertain traffic, and joins each receiver after exact terminal EOF. It retains
fixed live slots, not submission history. Native UUID ownership, full SDK decode
and read-tail lifetimes, genuine process admission and actual runtime selection
remain separate unfinished requirements. Its conservative wire reservation is
not allocated memory or an admitted frozen source recipe; PLAN records both
the formula and its 62.15-GiB worst-case reservation at current phase ceilings.
Independent review of all nine source/test files and the subsequent immutable
capacity accessor reported zero findings at every severity. Real socket-pair
tests cover concurrency, one-owner construction, lost ACKs, retained prefixes,
phase refusal, close protocol, cancellation and deterministic descriptor
cleanup. Two initial test-only failures were corrected: a transient
descriptor-directory entry was counted (5.536s), and a fake parent closed
before the child's required half-close (5.575s); pinned lint also caught two
unchecked test cleanup errors, now handled. Stable normal five-repeat/race
five-repeat checks passed in 2.092s/2.920s. The final accessor tree passed normal
three-repeat/race three-repeat in 1.225s/2.345s, vet, pinned lint and whitespace.
Owning documentation and immutable-tree gates remain pending. This closes no
whole-store collector, full execution profile or freeze.

The SA01 owning-doc review found one low precision issue: its protocol-complete
flag observes terminal EOF, while explicit Wait/Close separately proves receiver
join. The ADR now distinguishes those boundaries; documentation (0.481s),
glossary and whitespace pass on the corrected wording.

Fresh independent review closed that low finding and left all severity counts
zero. Exact transport commit `de757d18d16bb02722cfde4628808b3cc33c7878`
passed detached-clean package normal five-repeat (1.876s), race five-repeat
(3.044s), vet, pinned lint (zero findings), documentation (0.527s), glossary
and whitespace. The checkout remained clean; these close only the mechanical
channel slice, not its genuine runtime/SDK admission or whole-store counters.

Selected-state restore now preserves the original rollback target count while
paging actual future deletions at 512 and current/preimage pairs at 256, then
writing the final selected summary. It reuses the existing raw clear for four
unselected tables. The original six-artifact interrupted fixture has 516 actual
targets, not a hypothetical maximum. A larger genuine A300-to-B513 native
case submitted exact payloads `[512,1,512,88,2]` and preserved the selected
state/summary. Its first package run failed in 2.236s only because seven small
test cases reused another repository's relationship identity; the large case
passed in 0.71s. Correcting those test identities passed all eight cases in
2.336s. A separate pure test's composite-ID expected integer normalization
mismatch (0.886s) was corrected without changing production behavior.
Independent review requested strict one-result/OK/array census evidence and a
bounded, error-reporting native test cleanup; both are corrected. Final source
`fd3e10b9fda8fc917f78016201c2d4f28f38fead22a6156b6eaabff337c069bc`
and test `97e5a430c36e780e6121bfdb2f616c159906ae5bf50d9e4563a1bbecefbb6e94`
have no remaining source/test findings. Normal three-repeat/race three-repeat
pure checks passed in 1.257s/5.770s; final native normal/race passed in
2.488s/5.741s, including selector, future, preimage, target and summary drift,
post-commit cancellation and lost-reply retention. Both SDK connections and the
engine joined. Vet, pinned lint and whitespace pass. Independent owning-doc
review reported zero findings at all severities; documentation, glossary and
whitespace passed. Immutable-tree and complete six-artifact compatibility gates remain pending;
neither phase-wide submission accounting nor freeze follows from this fix.

Exact selected-restore commit `3e4ded3838b789ffa87b5e20580acd9834b4de80`
passed detached-clean selected rollback/clear normal (2.542s) and race (6.582s)
gates, then the complete six-artifact recovery package in normal (79.547s) and
race (95.584s) modes. Vet, pinned lint (zero findings), documentation (0.549s),
glossary and whitespace passed, with the checkout clean before and after.
This closes this restore fix's immutable-tree and archive compatibility gates,
not the full store collector or phase-wide ceremony acceptance.

Shared authentication-session expiry now performs one actual read-only probe
before the unchanged predicate deletion. An exact empty array submits no write;
positive cleanup returns the actual deletion count and preserves renewal
semantics. Independent source/test/cost review reported all severity counts
zero at source `034ceb99a4ee2124cc39702a44982aa132d1be513f64b81715819bc9cff9228d`
and test `857b8cfff2b51e4b8ab68f4fea787268b675e98b7c0a9ccf19b0eaea505cc565`.
Pure three-repeat checks passed in 0.606s. Independent owning-doc review
reported all severity counts zero; documentation (0.718s), glossary and
whitespace passed. Native compatibility and immutable-tree gates remain pending. Positive bulk expiry
remains outside the established exact source-descriptor boundary; this is no
assumed zero-write recipe or phase-five transaction-fit claim.

Exact authentication-expiry commit
`d48d9d3356daae088c49a48e6b7444a28d678d9f` passed detached-clean native
expiry and existing API-key/session compatibility in normal (1.899s) and race
(2.683s), then the complete auth package in normal (0.803s) and race (5.401s).
Vet, pinned lint (zero findings), documentation (0.617s), glossary and
whitespace passed; the checkout remained clean and no native engine survives.
These close that guard's scoped compatibility and immutable-tree gates only.

The complete catalog/lifecycle/selector source inventory found five genuine
missing-marker multi-definition calls and an always-reached orphan scan that
submitted empty conditional deletion transactions for owned records. Reusing
the existing batch helper preserves 78 exact definitions across five explicit
transactions. Actual owner probes now avoid healthy orphan deletions, while
positive eligibility retains the original native fence and successful-delete
cap. Root independently reviewed both source/test deltas with no findings.
The orphan selectors passed pure normal three-repeat/race three-repeat in
0.624s/1.595s and existing genuine startup repair/orphan compatibility in
1.170s. These prove dispatch and compatibility separately, not native phase
counts. The DDL test fixture initially failed compilation on two SDK-shape
assumptions, then a missing mock codec caused failures in 0.484s and the
already-started 0.453s/0.621s repeats; these test-only setup errors were
corrected without changing production definitions. Corrected pure three-repeat
normal/race passed in 0.484s/1.707s. Existing real schema atomicity,
self-healing/reopen and catalog/lifecycle migration repair tests passed in
11.708s across four serial engine epochs, all joined. Vet, pinned lint and
whitespace pass. PLAN records actual fresh/marker-present costs and the
worst-case extra orphan probes. Independent owning-doc review reported all
severity counts zero; documentation (0.665s), glossary and whitespace passed.
Immutable-tree gates remain pending; neither a complete 170-transaction phase
fit nor freeze follows.

Exact startup-recipe commit `b12d533e70c53e4eee7786044607c68953fe7f7b`
passed detached-clean schema/batch/migration/orphan/startup selectors in normal
(13.664s) and race (14.683s) modes. Vet, pinned lint (zero findings),
documentation (0.616s), glossary and whitespace passed; the checkout remained
clean. This closes the combined startup correction's scoped immutable gates,
not the remaining state migrations or a full execution profile.

The private typed-SDK adapter core now retains selected reads through decode,
counts accepted logical attempts before native forwarding, preserves unknown
outcomes and refuses unbound controls. Its initial source
`fda22a05ef5691a0e600a3f79204c1f7dd89169dfa8b8a34ba5b6a5b6fd40af1`
passed normal three-repeat/race three-repeat in 0.533s/1.672s, then independent
review found two medium issues: the SDK exposes mutable transaction UUID
pointers, and two local owners could each admit forty reads for one client.
Fixed slots now bind the actual SDK object plus copied UUID/connection and
forward only the copied UUID; the client permits one irreversible ALL-call
owner claim. A prior self-review also removed an owner/SDK mutex inversion and
joined selected-call cancellation bridges; ordinary nil-owner calls remain
direct. Final adapter source
`d70ccefc840452067dd52648d45eafee73891e1c35f694d3f0629f4c53f8f99a`
and test `a42a2c6bf6acfa31318a841986a654b89bb72b2f777f536004e5011633e152a5`
passed normal three-repeat/race three-repeat in 0.648s/1.671s, vet, pinned lint
and whitespace, with fresh independent severity counts all zero. The client
claim source/test `002a3a01`/`0a5ee0f7` passed its focused five-repeat normal/race
gates in 0.262s/1.287s and separate all-zero review. Root's combined working-tree
checks passed the transport package (0.576s) and adapter selectors (0.441s).
These tests use real SA01 sockets/reducer and the real SDK codec with a scripted
native transport, not an engine or complete production meter. Independent
owning-doc review reported all severity counts zero; documentation (0.696s),
glossary and whitespace passed. Immutable-tree gates, actual
source/factory/bootstrap/HTTP integration and native lifecycle proof remain
pending.

Exact adapter-core commit `f0e0eaeee387d152d2af9ac2c7ee084f93bbf89f`
passed detached-clean transport normal five-repeat/race five-repeat in
1.941s/3.050s and typed adapter selectors in 0.530s/1.678s. Vet, pinned lint
(zero findings), documentation (0.535s), glossary and whitespace passed; the
checkout remained clean. These close the core's scoped immutable gates only.

The state-schema prerequisite now batches its existing definitions and repairs
missing visible revisions in actual native-ID pages of at most 512, retaining
the existing compatibility latch and marker order. Each positive write repeats
the sorted census in its transaction. Pure page/order/failure selectors passed
three repetitions in normal (0.610s) and race (1.774s) modes. The first native
selector passed in 2.131s: existing schema idempotence, snapshot backfill and a
514-row fixture covering numeric/composite IDs, a committed-page lost reply,
explicit later resumption, a changed-page refusal and final exact content.
The late-schema failure test now exercises a real native unique-index failure,
not a formatter rejection. No automatic failed-write retry or total migration
row cap was introduced, and no engine survived. An earlier compile-only attempt
was blocked by concurrent unfinished private-control symbols and executed no
test or child; the preliminary package lint encountered only the separately
owned in-progress control branch. Independent source/test/owning-doc review
reported all severity counts zero; documentation (0.635s), glossary and whitespace
passed. Immutable-tree gates remain pending; PLAN records the added
census/transaction costs and limitations.

Exact migration commit `3bfeba4f23a193858add42aafc5461368eeb3ca5`
passed detached-clean pure normal/race three-repeat in 0.671s/1.773s and its
three native schema/backfill selectors in 2.193s/3.192s. Vet, pinned lint (zero
findings), documentation (0.566s), glossary and whitespace passed; no engine
survived and the checkout remained clean. This closes the source prerequisite's
scoped immutable gates, not actual selected accounting or complete phase fit.

The mechanical inherited store-channel bootstrap is independently reviewed with
all severity counts zero. Its distinct selector requires FD5 and the exact
parent-returned store record beside existing FD3/FD4, completes real SA01 Attach
before either PB01 ACK, retains CLOEXEC descriptor ownership and the dispatch
lifetime, and permits one opaque handoff. Live/unclaimed store closure refuses;
it does not assert that SDK reads are drained. The existing socket-pair primitive
is shared through one leaf package to avoid a store/dispatch import cycle.
Whole dispatch/store-transport normal three-repeat passed in 9.767s/1.197s,
race three-repeat in 14.720s/2.198s; focused withheld-Attach cancellation passed
three repeats in 0.348s. Vet, pinned lint and whitespace passed. The first
complete normal/race dispatch runs failed in 10.190s/13.855s only because the
new test attempted a deadline on an unsupported raw os.File; adopting its
diagnostic socket as the production net.UnixConn corrected the fixture.
That failure changed no production code or native engine. Independent review
also verified the unchanged legacy canonical omission and maximal 284-byte
record increment. No helper remains. Owning-doc review found one low lifetime
wording gap: the store cancellation context is retained, while only the
bootstrap callback/channel is temporary. PLAN now distinguishes them.
Documentation (0.606s), glossary and whitespace passed. Corrected owning-doc
re-review reported all severity counts zero; documentation (0.546s), glossary
and whitespace passed again. Immutable gates, genuine issuer/SDK-owner phase
linkage and full execution proof remain pending.

Exact inherited-channel commit `6d01f0c2a751f44e1098262dd56c48a3e22f2120`
passed detached-clean dispatch/store-transport normal three-repeat in
10.023s/1.208s and race three-repeat in 14.669s/2.520s. Vet, pinned lint (zero
findings), documentation (0.500s), glossary and whitespace passed, with a clean
checkout afterward. This closes that mechanical bootstrap slice's scoped gates.

The private local-store factory now places the final SDK gate before connection
initialization and executes its fixed five native controls/definitions on that
same connection. The first version passed focused normal/race three-repeat in
0.522s/1.847s, then independent review found one medium issue: selected Use
discarded its result shape. A pure pinned-codec check (0.646s) proved that null,
undefined and missing fields collapse to the same pointer before SDK Send.
The correction preserves the exact envelope's raw result only in selected mode
and validates actual null at both Use steps; all other typed decoding and
ordinary configuration remain on the SDK codec. Initial real-WebSocket
correction checks passed in 0.550s. The selected factory still refuses the first
unannotated schema call after an authentic five-call/two-transaction/two-row
prefix; no native-engine or complete startup claim follows. Final correction
core/factory/ordinary-initialization selectors passed three-repeat normal/race
in 0.583s/2.024s, plus vet, pinned lint (zero findings) and whitespace. The
narrow lint suppression preserves the pinned SDK's exact deprecated error-wire
type rather than substituting semantics. Documentation (0.796s), glossary and
whitespace passed. Fresh correction/owning-doc review reported all severity
counts zero at factory `5dfa483c`, tests `71977ab2`, initializer `067f8a8a` and
core `47c399ae`. Immutable gates remain pending.

Auth and request-store annotation buckets passed independent source/test review
with all severity counts zero. Their twenty plus nine Query sites comprise
twelve reads, eleven actual one-RID writes and six explicit unsupported recipes;
all SQL strings and ordinary behavior remain unchanged. The auth tests passed
pure three-repeat normal/race in 0.610s/1.737s and complete TestAuth normal/race
in 11.958s/13.160s, with vet, pinned lint and whitespace clean. Request-store
tests passed pure three-repeat in 0.648s/1.632s and the four existing native
audit/usage/permission selectors in 2.718s/4.046s; vet passed. That preliminary
lint encountered only the then-in-progress factory's exact SDK error type;
the factory's later narrowly documented correction passed pinned lint with zero
findings. No native engine survived. Tests retain genuine SA01 ACK-before-native
prefixes, actual record operands, unsupported refusal and ordinary compatibility;
auth additionally has closed AST source coverage. Accepted source/test hashes
are auth `8248b019`/`1313e331`, audit `f91183d2`, usage `d6e527e1`, permission
`c019b5cd` and request test `c6a48a22`. Integrated exact-tree acceptance remains
required; this is not a complete ceremony meter or a freeze.

The integrated store-accounting bucket now reuses one concrete SDK owner for
authenticated bootstrap, factories and phase control. Independent rehome,
phase bridge, state/catalog and lifecycle/selector/reference source reviews
reported all severity counts zero. Rehome/bridge complete dispatch and store
transport normal three-repeat passed in 9.997s/1.424s, race three-repeat in
15.139s/2.713s; affected-package vet and pinned lint passed. These are scoped
working-tree results, not native engine or full runner evidence.

The shared empty-startup migration checks passed the combined focused/native
selector normally in 19.214s and under race in 21.103s. The subsequent
active-job/schema accounting checks passed normal in 0.603s and race
three-repeat in 1.836s. The same-schema eight-index batch, actual evidence
migration recipes, genuine factory binding, complete native selected opening
and owning documentation still require their combined review/gates. The
earlier first-unannotated-schema refusal is superseded by the actual metered
488-definition submission, not by a complete-startup assertion. Main remains
unmerged and V3 remains unsealed/unfrozen.

The first actual selected full-initialization test failed in 1.085s after an
accepted one-transaction/one-row namespace definition prefix. Its engine
joined. A bounded native reply diagnostic then passed in 1.220s and showed
that both Use replies are scope maps, not the null assumed by the earlier
mocked positive fixtures. Correct exact-scope validation and another complete
selected startup/reopen test remain required; earlier null-fixture green
results do not establish native startup compatibility.

A second bounded native diagnostic (2.420s) identified the exact two-field
28/32-byte scope maps, with native tagged NONE only for namespace-only Use.
The corrected selected validator and fixtures reject wrong or incomplete
scope maps. The next full selected opening advanced past the controls but
failed in 1.310s at the unannotated resolver writer migration, retaining
27 transactions, 722 rows and a 488-row maximum. The engine joined. This
establishes native control compatibility, not complete selected startup;
remaining startup source coverage is the next gate.

The three writer migrations now close that startup gate. Strict native marker
decoding and exact empty-set fences passed the actual selected full opening
and reopen in 1.110s: fresh 36 transactions/743 rows and reopen 7/499, both
maximum 488 with 82 tables. Both remain inside each unchanged 170-transaction
and 87,040-row phase budget. The engine joined. This supersedes the preceding
incomplete startup record only, not full controlled-server or runner acceptance.

Queue source/test review is clean; pure three-repeat normal/race passed in
0.632s/2.413s and seven native selectors passed in 18.376s/21.049s. Vet and
repository-pinned 2.12.2 lint passed; the separate unpinned lint result is not
substituted for that gate. Generation's initial native gates failed in 49.854s
and 52.645s, first on LET result arity and then on the existing-schedule
census. A single 9.069s diagnostic isolated an absent IF-wrapped ID array.
The corrected unconditional native SELECT preserves every predicate and
strict absence refusal; its replacing-schedule confirmation passed in 9.583s,
then all eight native selectors passed normal/race in 51.855s/51.745s.
Generation pure race three-repeat, vet and pinned lint also pass. All owned
engines joined. These are source-scoped working-tree results: independent
integrated review, full runner/readiness, merge, seal and freeze remain open.

Connection membership's independent source/test/cost review reported all
severity counts zero. Repository deletion/reactivation now uses a native
branch/ID observation with an exact in-transaction echo. Combined pure normal
passed in 0.632s and race three-repeat in 1.720s. Four actual membership,
rollback, reactivation and stale-census selectors passed normal/race in
2.373s/4.193s. All engine cleanups joined. Independent review of the new
deletion-state path and the combined exact-tree gates remain required.

Independent deletion-state source/test/cost review is now clean at all
severities. First-user enrollment's earlier conservative unsupported recipe
is replaced with its two actual fixed operands, without SQL or behavior
changes. The initial source assertion failed in 0.550s because it still
required every auth write to have one operand; its correction names only the
existing two-target first-user transaction. Auth accounting three-repeat
normal/race then passed in 0.637s/1.767s. This is not yet a complete authenticated
server rehearsal or full runner proof.

The first three-writer native compatibility selector failed in 27.002s:
the new read-only refusals preserved the guard but lost the established
future/mixed-generation error vocabulary. Restoring the original permanent
messages, without weakening the checks, passed the combined three-selector
and actual selected startup/auth test in 27.488s. Fresh startup's 36/743
transaction/row prefix became 37/745 after the first-user submission; reopen's
7/499 became 8/501 after a known conflicting first-user attempt. Both kept
maximum 488, 82 tables and complete final SA closure, and the engine joined.
The separate ordinary first-user atomicity selector passed in 1.363s.

Complete store-accounting and dispatch-admission packages then passed normal
in 0.641s/3.816s and race in 1.690s/5.908s, with scoped vet clean. These are
working-tree transport and store proofs, not an exact-tree full runner gate.

Cross-bucket review then reopened one medium source/operand consistency
finding in the repository state writers, including the previously reviewed
deletion-state path: an inactive native branch still contained fixed write
operands in the submitted SQL while its descriptor omitted them. Bounded
source recipes now specialize those write fragments from their exact echoed
observation, preserving the original authority guards; inactive mutations
must actually be absent, and a pending successor submits only its selected
UPDATE or CREATE body. Ordinary overflow keeps its original transaction.
The first corrected deletion-state pure three-repeat passed in 0.680s;
fresh independent review and native confirmation are required. Earlier green
results and the earlier zero-finding review do not close this reopened issue.

The corrected deletion-state source and tests then passed independent review
with all severity counts zero. Corrected pure race three-repeat passed in
1.736s; the four native membership/deletion/reactivation selectors passed
normal/race in 2.351s/3.562s. The index-state correction likewise passed exact
source/test review with all severities zero and six native race selectors in
21.607s. Its initial native command failed in 19.596s only on a new fixture's
missing required status; all five existing cases passed. The next 1.264s run
proved the transition but failed its test-only ORDER BY projection. Correcting
those fixture errors, without changing production source or lowering the
assertions, passed the new native fixture in 1.268s. All engines joined.
Equivalent inactive operands in queue projections/coalescers were identified
and remain under correction; these scoped results do not close that issue.

Atomic DeleteRepo's actual eighteen-vector census, stale-set refusal and late
rollback passed normal/race in 2.157s/3.322s across three native selectors.
Pure checks, vet and pinned lint passed, and independent source/test/cost review
reported all severities zero. One earlier race invocation failed compilation
only while a neighboring shared source constant was being edited; no engine
started for that invocation. Final native runs joined. This proves the selected
cleanup recipe's tested branches, not all eighteen positive tables or a full
controlled-server run.

The queue medium finding is now corrected: submitted SQL includes only the
chosen pending/terminal mutation and source-known repository projection.
Independent exact source/test review reports all severities zero. Eight pure
selectors passed three-repeat normal/race in 0.702s/2.296s; seven native
selectors passed normal/race in 19.005s/20.774s. Vet, pinned lint and whitespace
passed, and all engines joined. The two unused legacy projection constants
were then moved byte-identically into the existing parity test; runtime queue
source stayed unchanged and parity three-repeat passed in 0.637s. This closes
the scoped queue finding, not the full store or runner admission.

Fourteen lifecycle-cursor/core/derived-retention source annotations preserve
the original SQL, payload and native call count. Independent review reports
all severities zero; focused three-repeat normal/race passed in 0.540s/1.693s,
and six existing native selectors passed in 10.521s/13.066s with joined
engines. The subsequent catalog scan and fixed evidence-sweep annotations
passed combined pure three-repeat in 0.603s. Positive dynamic cleanup and
full native selected-server closure remain open; no empty scan substitutes
for those requirements.

The four existing evidence-retention native selectors subsequently passed
normal/race in 26.249s/27.544s; final combined maintenance accounting checks
passed race three-repeat in 1.802s. Vet, pinned lint, documentation, glossary
and whitespace checks passed, with no SurrealDB process or port-65499
listener remaining. These preserve ordinary shared-atom, pin, cardinality,
restart and invalid-phase behavior; the new selected dynamic sweep vectors
remain a separate required implementation, not an inferred native pass.

The lead session was stopped on 2026-09-06 with the two newest T42.2l rows
(partitioned extraction runtime call capture; caller and resolver runtime
admission) implemented but unrecorded. Independent source/test/cost review of
those slices reported critical 0, high 2, medium 2, low 1. Both high findings
and one medium were un-ADR'd next-slice starts made after the last recorded
gate, not defects in the recorded rows: a bounded evidence sweep-chunk census
that contradicted the maintenance row above and broke
`TestEvidenceSweepFixedAccounting/dynamic`, and a census-fenced rewrite of
candidate-manifest publication/clear with no row, tests or native fences.
Both were reverted to their last gated source; the removed source is retained
outside the tracked tree as `t42.2l-inflight-reverted.patch` (checkout
`.cowork-reverted/` and the session record) for their owning slices, which remain
required: selected dynamic sweep vectors must fit a 512-submitted-operand page
(a full 512-row page plus two fixed operands cannot), and candidate-manifest
publication/clear stay unannotated until their own recipe, tests and fences
land. The remaining medium and low findings are closed in source and the owning
row: the two table-wide caller-leaf clears are explicitly unsupported in
selected mode with a refusal test, and resolver publication/clear census
conflicts are recorded as job-retried. The corrected tree's gates are recorded
below once run; nothing here is a merge, seal or freeze.

The corrected working tree then passed its gates on 2026-09-06 with Go 1.26.5
and SurrealDB 3.2.0 on PATH: repository build; the combined focused selectors
for both slices plus the sweep, lifecycle/retention, candidate-manifest and new
table-wide-clear refusal tests, three-repeat normal in 36.217s and race in
38.154s; the storeaccounting, dispatchadmission and lifecycle packages normal
in 0.687s/3.800s/4.635s, with storeaccounting/dispatchadmission race
three-repeat in 2.592s/15.151s; documentation (0.490s), glossary and
whitespace including untracked files; and the repository-pinned 2.12.2 static
gate — vet, lint with zero issues and compile-all across 111 packages — in 40s,
after the version guard refused the PATH's unpinned 2.13.1. No SurrealDB
process or port-65499 listener remained. Fresh independent source/test/cost
review of the corrected slices confirmed all five earlier findings closed and
reported critical 0, high 0, medium 0, low 1: the owning row's "typed conflict"
wording overstated a `phebs-conflict` query error that is not `ErrConflict`;
the row now says so. These are working-tree results on the still-uncommitted
bucket, not an exact-commit, full-store, native selected-server or runner
gate; after its explicit-path commit, the exact-tree gates, integration, seal
and freeze remain open.

The subsequent pre-merge review of `a9bb6289` reproduced two submitted-operand
mismatches: a 511-state reconcile prefix retained an uncounted summary UPSERT,
and an exhausted generation retry counted three while retaining four mutation
operands. The correction omits inactive fixed summary/preimage SQL, supplies
only selected preimage RIDs to their CREATE loop, and counts all four retry
operands without changing native retry semantics. Existing prefix packing and
all transaction fences remain. The owning ADR records the bounded extra
preimage point reads and SQL/vector assembly. Regression checks inspect actual
submitted SQL and CBOR beside the real SDK/SA01 acknowledged prefix, including
511/512 states, no-ops, mixed preimages, exhausted and stale retries.
The final correction source passed the combined accounting/headroom/census and
native preimage/retry/exhaustion selectors normally (35.808s) and under race
(47.190s). Complete generationscheduler and storeaccounting packages passed
(0.593s each), with affected-package vet, pinned 2.12.2 lint (zero issues),
documentation, glossary and whitespace checks clean. These are working-tree,
scoped correction results, not a full-store or full-stack acceptance run.
Nothing was committed, merged or pushed; freeze remains unestablished.

The lead correction was subsequently bookmarked locally as `3ffa56e9` on
2026-09-06, without merge or push. The merge-bar recovery mismatch was the
enqueue's already-charged prior-schedule census, not an extra publication:
the one-domain fixture now expects ten store reads in both modes. Prospective
V3 derives five retryable reads, admitting 24–339 reads for nine domains and
adding 64 to each recovery phase's control-read maximum. Native retry depth
remains 64. The retained V2 275 ceiling fits only 51 fully charged attempts
(274 reads; 52 needs 279); V1/V2 bytes and validation stay unchanged.
The owning ADR records the existing read cost and added bounded V3 derivation.
The complete extractionpublication package passed normally (16.929s) and under
race (20.038s). Focused recovery/phase-bound and V3 canonical/retained-byte
checks passed normally (0.915s) and under race (2.444s); documentation and CI
contract checks also passed. Full-stack gates and independent review remain
pending; no merge, push or freeze is implied.

The following test-budget slice raises `make test`/`ci-go` to a 60-minute
per-package alarm and the enclosing full-Go CI job to 90 minutes. It retains
the whole suite and all individual-test, race and ceremony deadlines; a small
CI contract check covers both allowances. The earlier 25-minute T42.1 package
timeout interrupted an individually in-budget test. Ben's reported
2,071.970-second pass is additional evidence of the aggregate mismatch, not
an exact-candidate normal/race result. Normal and full race gates remain due.

The subsequent full run at `a2464e74` passed 108 packages, including T42.1 in
2,082.128s, with only the inherited `t211`/`t306m`/`t324` failures already
reproduced at main. The complete named seven-package race gate also passed;
these results do not waive baseline failures. The operations timeout wording
was corrected separately in `29a8bbd5`. Independent review of the 17-file
correction range found one medium issue: V3's serialized preparation policy
still described four retryable reads while its numerical bounds used five.
The approved follow-up replaces only that V3 formula and extends the existing
regression to check canonical policy, 24–339 bounds, unchanged phase ceilings
and refusal of the old policy. V1/V2 retained bytes stay exact; the owning ADR
records the bounded constructor allocation and changed prospective identity.
The new policy regression failed before the fix and passed afterward. Focused
normal checks including the full V3 plan round trip and retained V1 validation
passed in 37.264s; focused race passed in 4.000s, and native-fixture input
compatibility/canonical artifact routing under race passed in 2.167s. V3's
canonical plan remains 165,333 bytes, within its authoring cap. Vet, pinned
lint, docs, glossary and whitespace checks passed. The earlier full normal
and named race records remain attributed to `a2464e74`, not rerun or relabeled
for this policy-only follow-up. This is not a fresh independent full-stack
review, integration or freeze result.

Ben supplied Claude's seven-reviewer report for the full 81-commit range
`6e38ce97..48d3cba8`: 251 files, reported code findings 0 critical, 0 high,
4 medium and 25 low. This supplies the previously missing stack-wide source
review coverage, not new machine gates. The report independently confirms
the final V3 policy correction without changing retained V1/V2 bytes.
Ben then approved fixes for M-A1/M-B1/M-C1a while retaining M-C1b's existing
fail-closed selected read-cancellation contract; waivers remain separate.
The correction adds exact-native-census-conflict retries to both index-state
writers, omits the absent prior-plan UPDATE, and refuses advancement until
an ending producer closes. All three new regressions failed before their
respective fixes and passed afterward. The owning ADRs record retry costs,
submitted SQL and phase-ordering costs, and the selected-read blast radius.
Full corrected-tree gates and independent correction review remain pending.

The missing author lifetime-guard ADR for `8e4d1a0` is now recorded without
backdating a gate. Claude's report identifies `1b9a8af` as a non-compiling
test window corrected by the following `a9e84e3`; it is not an accepted test
candidate. Other missing historical gate/review records are not reconstructed
as successful runs. The report's restore/store scope-ratification request
remains Ben's decision; this correction does not ratify that expansion.
Prior interactive approvals for the prospective V3 five-read correction and
60-minute test allowance remain distinct from autonomous-delegation claims.
No inherited-failure waiver is issued: the retained candidate and base
`6e38ce97` logs show four `t211` glossary-validation failures on the unaccepted
`relationship_explorer` surface, `t306m`'s `TestT306MFollowupSplit` allocation
comparison and `TestT306MStatusBoundAndWarning` budget disagreement, and
`t324`'s `TestRetainedReceiptIsClosedAndInputBound` input-hash mismatch.
These observations do not prove an additional Git-version assertion failed.
Retained evidence is unchanged and its repair is not folded into this slice.

The first strengthened native conflict assertion exposed the engine's
`An error occurred:` prefix and the SDK's joined aborted-statement errors;
the initial single-error matcher therefore did not suffice. The correction
now traverses the actual typed error tree, accepts only a known QueryError
tree containing the exact conflict and rejects unknown leaves before or
after it. The real-engine stale-projection/rollback check then passed, as did
ordinary/selected retry, fresh-census, exhaustion and uncertainty regressions.
Final focused normal checks passed for store/storeaccounting/dispatchadmission
in 3.824s/0.427s/2.771s; three-repeat race checks passed in
20.409s/2.780s/9.125s. Complete storeaccounting, dispatchadmission and indexer
packages passed normally in 0.814s/3.663s/21.414s. Final ci-static passed
module-wide vet, pinned 2.12.2 lint with zero issues, glossary and package
compilation. Documentation and whitespace checks passed, with retained
V1/V2 plan digests unchanged. These are uncommitted correction-tree results,
not a full-store normal run, full ci-go/ci-race rerun, or independent review.
The review skill's pre-commit confirmation remains pending; no new immutable
candidate, waiver, merge, push or freeze is claimed.

The backing-space prerequisite separately reclaimed exactly 952 source-proven,
rebuildable Phebs Go cache archives after independent cleanup-script review
reported all severity counts zero. The fixed manifest digest was
`ab1850ec760ea7f892107905967b1e153e593a803a9ea9a3879f2bd4e4ebf5a5`.
Every regular archive's complete content hash, identity and main-checkout
scale-package provenance were verified in a validate-only pass and again
before deletion during a joined, quiet build window. The operation removed
25,785,404,468 logical bytes and 25,787,416,576 allocated bytes; observed
available space afterward was 134,961,928 KiB, about 128.7 GiB. It selected no
source, executable tool, presentation archive, dependency archive, retained
evidence or worktree. This trusted-owner quiet-window check does not claim an
atomic hostile-same-UID unlink defense; cache reuse can require rebuilding.
Historical temporary-tool roots remain untouched. This is point-in-time
headroom above the unchanged 120-GiB floor, not host freeze admission.

Scoped gate updates: exact literal-config correction 726e8953 passed independent
source/test/cost review with all severity counts zero; its clean detached
config/main pinned lint reported zero issues, and docs (0.515s), glossary and
whitespace passed. Independent exact owner-control review of 0834fc42 and
system-image review of ac58c8cb also closed all severity counts at zero. The
system-image three-repeat native tests passed in 0.644s, race in 1.757s, with
vet, pinned lint and whitespace clean. These supersede only their scoped
pending-review/static statements below.

Exact transport correction 91af48e6 independently re-reviewed with all severity
counts zero, then passed the actual full-population author rehearsal from its
clean detached source. Genuine source/SDK/module custody admitted 67,420 entries,
55,703 files and 1,352,680,289 bytes in 6m4.959s. Supplied/reference author builds
took 47.062s/2m13.488s. Actual A, B and A-return CLI/census/checkpoint/join took
19.870s/14.890s/14.809s with 4/3/3 dispatch attempts and 1,280/1,024/1,024 reserved
DA01 bytes. The complete test passed in 658.86s (package 659.491s), including
natural child closure, empty recorded sessions and successful owned cleanup.
This closes that three-author rehearsal only, not full controller composition,
all-tool admission, executor/launcher, host freeze or a ceremony.

An earlier complete receipt selector at exact 4c786ff4 exceeded its mistakenly
short ten-minute package alarm while its existing twenty-minute native physical
identity fixture was still materializing the A-return resolver. Retained V1/V2
bytes and the V3 frozen round-trip passed; all three physical generations had
executed 56 extraction chunks and nine current domain roots. Package status was
failure (660.662s), not a complete receipt or later freeze-test pass. No matching
engine/test process or port-65499 listener remained before the separate author
rehearsal. One new exact-clean selector uses a thirty-minute outer package alarm
that covers the unchanged fixture deadline; no frozen deadline is raised.
That clean selector at exact 35b7efa7418ac7af79f89341e4622eec9a9b906a
subsequently passed in 957.727s. Retained V1/V2 bytes, full V3 round-trip,
all stopped-prefix cases and later freeze validators passed. Its modeled
successful receipt is 330,512 bytes under the 524,288-byte cap; positive
incomplete and overshoot/unavailable prefixes are 311,547 and 311,540 bytes.
The native identity fixture executes three physical generations with 56
extraction chunks and nine roots each; its stale/restart constructor reuse
does not perform actual lease or hard-death injection. This is a constructor
and validator gate, not full executor readiness or a ceremony.

These tickets are pending, not acceptance evidence. Agents may refine an
in-scope implementation choice in the owning ADR, but cannot relax an AC,
invent a measurement or expand the frozen program to force completion.
The pure operational-flow constructor now maps the unchanged plan to eleven
producers, 120 sites, seven roles and fifteen phases, with 547,195 admissions
and a 140,090,368-byte DA01 ceiling. Its conservative 61-active limit applies
only to the selected HEAD-only closed HTTP recipe, which remains to be wired;
it does not admit arbitrary token-authorized HTTP/MCP batches. Actual producer
and phase ownership, PC01/PB01/log/outer-stage budgets and full admission remain
separate gates. Independent review found one medium malformed-history panic;
the correction guards the shared validator and tests physical lengths zero,
one, two and four through both the direct fast path and constructor. Exact
correction review and source-attributed tests remain required. Corrected source,
tests, arithmetic and owning-cost re-review subsequently closed all severity
counts at zero. Three normal repeats (1.617s), three race repeats (20.650s), vet,
repository-pinned lint, docs (0.591s), glossary and whitespace pass.
Shared author composition now uses a genuine local root producer and the same
controller for actual direct author Start/Handle.Wait and nested four/three/three
Git admissions. Authenticated terminal authors acknowledge one complete Pause
before natural producer-local Close; legacy standalone checkpoint semantics and
canonical bootstrap bytes remain exact. Phase/site checks occur inside admission,
while completed-author row validation does not claim whole-controller completion.
Independent source/test/resource and owning-documentation/cost review found all
severity counts zero; the protected shared-author rehearsal remains a gate.
Full dispatch normal three repeats passed in 3.427s; owned author/inventory normal
three repeats in 5.962s; author/shared race three in 6.858s, author command race
three in 12.090s, new dispatch/FD race five in 7.283s and bounded-close race three
in 4.836s. Vet, repository-pinned lint 2.12.2 and whitespace pass. A mistakenly
broad author race selector included inherited full V1 regeneration and exceeded
its 90-second package alarm; it was not a pass, launched no engine, and the
subsequent selectors were restricted to the owned custody/command tests.
An additional source-owner cost readback corrected the site-check lock location
and distinguished the added custody-mutex metadata hold from short controller/
Client holds; it also records ordinary forwarding and changed-channel allocation.
These are source-identical wording corrections, not new passing runtime evidence.
Exact V3 HTTP now refuses unknown or unmarked product/MCP routes after genuine
token/owner reservation and before normal authentication/handler work. Existing
marked MCP parsing still rejects batches with one bounded decode. This closes
the arbitrary-HTTP caveat of the 61-active construction, not full phase/request
sequencing. Independent source/test review found all severity counts zero.
Five semantic/request-owner normal repeats passed in 1.826s, three race repeats
in 2.772s, vet and repository-pinned lint pass. Ordinary nil-semantic serving
is unchanged; immutable source/docs/cost attribution remains required.
The actual inherited lifecycle mechanics test subsequently passed five normal
repeats in 2.088s and three race repeats in 4.859s, with vet, pinned lint and
independent source/test review all clean. It uses sixteen supplied owners,
cursor/capacity and a captured failure callback; it does not prove native store,
physical pressure, production lifetime failure or complete custody.
One separate opt-in protected shared-author rehearsal is implemented and reviewed
by the lead with no source/test/cost finding. It requires actual A/B/A-return
through one root producer, thirteen admissions and fourteen mechanical phase
handoffs, with seven never-launched producers explicitly canceled unused. Its
opt-in-absent compile/skip (0.553s), vet, pinned lint and whitespace pass; genuine
execution remains pending on a clean immutable tree. Empty accounting phases
do not establish any server, archive, pressure, recovery or full ceremony phase.
That genuine shared-author rehearsal subsequently passed from exact clean
5737c5876b64b099b6d0711f6e285672fc4a7f23 in 670.18s (package 670.814s).
Source/SDK/module custody admitted 67,458 entries, 55,741 files and
1,352,969,322 bytes in 6m8.762s; supplied/reference builds took
49.461s/2m15.475s. Actual shared A/B/A-return runs took
20.753s/15.597s/14.835s and retained cumulative attempts 5/9/13.
The final complete mechanical snapshot has three root-author and ten nested
Git admissions, 5,632 reserved wire bytes and all other roles zero. Native
joins, source continuity and exact owned cleanup passed; a subsequent host
check found no matching test, author or Surreal process. This supersedes only
the pending shared-author execution gate, not the empty semantic phases.

The epoch-one current/prior retention helper now captures the actual A identity
only after successful warm F reporting, acquires its real pin in phase four
before B through one fixed non-R POST, and implements the original single C41
R with two concrete owner sweeps and actual A/B/held-A queries. It adds no fair
runner turn or R request. Lead independent source/test/integration/cost review
found no remaining finding. A first tiny native test exposed the helper's invalid
comparison of artifact-file and source-record units; the corrected helper passed
in 1.098s and confirmed C41/S0/M4/W0 for two actual source files. Final retention
normal three repeats passed in 2.754s, race three in 6.793s, with vet, pinned
2.12.2 lint and whitespace clean. Five inherited PB01/PC01 tests prove actual
private-token/auth/F-tail/pin and terminal-error mechanics using a supplied F
payload; they do not claim full F/native-R success or production lifetime
custody. Native positive deletion, cancellation and sink-panic prefixes remain
visible and terminal. Full admitted phase choreography, parent log/IPC budgets
and exact-tree integration gates remain open.
Five-epoch input custody now derives three canonical catalogs and five strict
private configurations from the genuine admitted author's protected plan/source,
using two existing flat custodians, four writable roots and five independently
reserved loopback listeners. It preserves one shared source/data/key and the
A/B/A-return/A-return/A-return logical sequence. The shared logical-catalog
helper preserves valid historical generation inputs; retained byte replay remains
an exact-tree gate. Actual generated catalog identities total 22,755,996 bytes;
the tiny fixture configurations total 4,446 bytes, not a promise about later
private-path lengths. Focused final tests passed in 1.441s, race three in
37.931s, with build, vet, pinned lint and whitespace clean. These gates prove
catalog identities, strict config refusals and real immutable staging/listener
mechanics; they do not fabricate an admitted author for constructor success.
Independent source/test/cost review subsequently found all severity counts zero.
An exact-clean extension of the genuine shared-author rehearsal remains required.
That extension reuses the same protected build fixture,
checks actual input bytes/native protection, releases and rebinds the five
reservations in order, and registers exact cleanup only after all owned closes.
It launches no server and establishes no full runtime/profile/freeze authority.
Lead review found one low rehearsal cleanup gap: a failed input-reader Close
could report an error and still return a cleanup-registration closure. Both
reader and constructor close failures now stop that return and retain custody;
corrected source/test/cleanup review found no remaining finding. The opt-in-absent
compile/skip passed in 0.473s. Retained V1/V2 canonical byte replay separately
passed in 0.614s, docs in 0.544s, glossary and whitespace passed. The genuine
extended constructor rehearsal still requires exact-clean execution.
That extended rehearsal subsequently passed at exact clean
95369beb7e1d80229c2dbc5902c9a283d933c53f in 674.12s (package 674.748s).
Actual source custody admitted 67,470 entries, 55,753 files and 1,353,092,023
bytes in 6m2.727s; supplied/reference builds took 52.073s/2m23.937s. The
three shared authors took 20.071s/14.954s/14.994s and again closed with thirteen
admissions and 5,632 reserved wire bytes. The genuine epoch constructor,
protected byte verification and five sequential native listener-release probes
passed in 937.981ms: catalogs total 22,755,996 bytes and actual private configs
1,264/1,264/1,278/1,278/1,278 bytes (6,362 total). Exact owned cleanup passed;
the subsequent host check found no matching rehearsal/author/Surreal process
or port-65499 listener. This closes the protected constructor/listener gate at
that source, not native Phebs startup, five-server readiness, the later
compatibility-selection change or the whole-work accounting hold.
The actual nine-domain startup trace also exposed an attempted Buf sandbox
validation despite V3's zero compatibility budget. The selected decoded V3
launch now explicitly leaves compatibility unavailable before discovery or
validation; ordinary and older exact modes keep their original behavior and
warnings. The shared sandbox refusal remains sticky. Its V3-only optional
profile posture adds 67 bytes; tests pin complete prior V1/V2 config hashes
and reject posture drift even after caller digest recomputation. Independent
source/test/cost review found all severity counts zero. Command normal five
repeats passed in 1.019s, race three in 1.950s; profile normal three in 0.645s,
race three in 1.894s; affected vet, pinned lint and whitespace pass. This
selection removes a substantiated startup refusal, not the full parent,
whole-work accounting, real five-server readiness or freeze prerequisites.
Final focused regression confirmation on exact implementation 3fe4d838
(with only the later hold/gate documentation uncommitted) passed the complete
`internal/dispatchadmission` package normal/race in 1.283s/3.482s,
the `cmd/phebs` `TestT422`-prefix selector in 2.956s/8.419s, and the epoch-config,
compatibility-profile and retained canonical-byte selector in 1.392s/13.890s.
Affected vet and pinned 2.12.2 lint pass with zero issues; docs (0.552s),
glossary and whitespace pass. These are focused gates, not full repository,
store, real five-server or launcher acceptance. Independent review of the
accounting-hold text corrected two low wording issues (native-import source
attribution and full *changed* state members), then closed every severity at
zero. Local and freshly queried remote main remain 6e38ce97; no integration,
push, seal, freeze or ceremony occurred. The post-gate host has no matching
rehearsal/test/author/Surreal process or port-65499 listener. Its approximately
110 GiB free backing space is also below the unchanged 120 GiB admission floor;
no unrelated user cache or retained diagnostic custody was removed to mask it.
The terminal-close primitive now closes one fully joined producer without
globally fencing other same-phase work. Checkpoint/Advance still require the
global fence, and binding, phase, sequence, ordinal and empty-local-active-map
checks remain exact. Real serial child lifetimes alongside another producer's
live child, pending/carried refusal, lost ACK and immutable-terminal tests passed
five focused normal repeats (0.387s), ten race repeats (1.383s), vet and pinned
lint. Full package gates and independent immutable review remain required; this
primitive alone supplies neither shared author composition nor archive readiness.
Its full dispatch package subsequently passed five normal repeats (5.521s) and
three race repeats (4.892s); immutable independent review remains open.
Root's independent exact source/test/cost review of 03827a78 found no code
finding. Its owning cost wording is corrected here: terminal Close updates
bounded producer state, not the admission-only digest. The authenticated flag
test's prior unfenced-help refusal is now a valid producer-local close; the test
explicitly checks that success without global completion and preserves actual
cleanup-error precedence through a mismatched authenticated DA01 binding. Three
normal repeats (0.991s), three race repeats (2.481s), vet, pinned lint and
whitespace pass. No parser/runtime code changed in that correction.
The semantic snapshot now exposes the actual ordinary-owner drainage fact,
separately from its immutable request identity. Initial registration/token and
pending drainage stay false; completed drain stays true through preparation
requests, and reopening/cancellation clear it. Shared snapshot tests passed ten
normal repeats (1.459s) and five race repeats (2.172s); request-identity tests
passed twenty normal repeats (0.716s) and ten race repeats (1.792s), with combined
vet, pinned lint and whitespace clean. Independent immutable review remains;
this is no claim of request drainage, native inactivity or authority readiness.
Controlled lifecycle turns now deliver the actual returned Tick result before
honoring post-Tick cancellation, preserving completed deletion counts without
emitting a capacity probe or successful cycle. The ordinary nil-collector path
is unchanged. Positive canceled-result and existing control/owner regressions
passed five normal repeats (1.987s), three race repeats (2.481s), vet, pinned
lint and whitespace. Independent exact review and the main bounded source-free
attempt-prefix sink remain gates; a panic before Tick returns remains unavailable.
Independent exact source/test/docs/cost review of 1c26e9cc and 17a68099 closed
all severity counts at zero.
Main now keeps the native lifecycle runner parked in all five exact epochs and
exposes five initial Parks, three native drives and seven exact R observations
through a closed authenticated sequence. Four capacity probes remain inside
their zero-C/S/M/W R scopes; the three native owner cycles remain outside R.
The same reporter retains actual returned-Tick/deletion prefixes before bounded
source-free log output, including post-Tick cancellation and positive overshoot.
Unit/refusal/context/report selectors passed five normal repeats (20.694s),
three race repeats (19.282s), final focused normal ten repeats (0.606s), race
five repeats (1.763s), vet, pinned lint and whitespace. Independent source/test
review found all severity counts zero; immutable owning-documentation review
and one real inherited-bootstrap mechanics success remain required. Exact
3b038a45 source/test/docs/cost review subsequently closed all severities at zero.
Supplied
test owners/capacity do not establish physical pressure or the native store/graph.
The separate physical-delta two-reader retention hook remains unwired.
Main now consumes the authenticated bounded V3 semantic launch record through
an actual inherited Unix socket before config/source path opening. It checks
the same config bytes once, keeps the fixed epoch/phase membership and one
shared exact-read state, and binds accepted HTTP requests to their reserved
producer/input/phase/window tuple through auth and report tails. No native
preparation route, plan admission or full readiness is claimed. Focused normal
three repeats passed in 14.303s, broad focused race three repeats in 20.737s,
and final inherited-socket completion/cancellation race repeats in 2.462s;
vet, pinned lint, formatting and whitespace pass. Independent review remains
required before this slice closes.
Independent inlet review found one medium literal-config bypass through YAML
decoding and path defaults, plus one low setup-cost omission. ParseLiteral now
rejects decoded interpolation and requires an explicit canonical absolute data
directory before any ambient expansion; ordinary Parse/recovery behavior is
preserved. The owning cost includes three setup locks and nine override lookups.
Normal three repeats passed in 0.240s (config) and 0.604s (main); broad race three
repeats passed in 1.421s and 22.081s. Vet, config lint and whitespace pass; main
lint must be rerun on the committed correction without unrelated unbound work,
and independent exact correction review remains required.
Independent review of the lifecycle rendezvous found one medium clock-backstep
gap: an observation rejected as older than its fence could still represent an
actual native sweep. The correction now bounds actual turns before execution
and refuses any completed pair the collector did not count. Always-stale and
stale-then-current regressions pass with exactly one real turn/probe and no
reuse. Normal five repeats (1.871s), race three repeats (2.318s), vet, pinned lint
and whitespace pass; independent correction re-review remains required.
Independent exact correction review of fd81e2d1 closed all lifecycle severity
counts at zero. Separate full-author rehearsal review found only one low
failure-path nil cleanup; the plan-input defer now checks a returned handle
before Close. Neither source review is an execution or freeze result.
The owner-control transition now permits consecutive parked phases without
reopening ordinary work. Actual preparation requests still join before the
retained fences permit Pause; mechanical-only control is unchanged. The real
three-phase socket and existing drainage selectors passed five normal repeats
(1.307s), three race repeats (1.936s), vet, pinned lint and whitespace. Independent
review remains required; this adds no phase execution or readiness evidence.
The implementation stack splits T42.2k into bootstrap/dispatch, semantic-owner
control and genuine-parent custody slices, and T42.2l into the current-revision
core, authenticated request/CLI and complete executor/admission slices. These
are PR-sized dependency splits, not extra acceptance or freeze authority.
The fixed platform-image custody slice now holds the three source-owned macOS
tool paths on their actual read-only native volume, reusing exact image
observation and metadata continuity without copying or modifying OS files.
Real three-role observation/close tests and sticky wrong-role/context/path/
volume/descriptor refusals pass; each caller still owns command and session
joins. This is not a twelve-tool issuer, command admission or freeze. Independent
source/cost review and exact committed static gates remain required.

T42.2i's implementation protects seven actual core/helper copies and supplies
the Git-child-only resource environment. Real protected init/fast-import,
revision/tree reads, local clone and incremental fetch preserve exact commits,
trees and content; the clone produced zero loose/three packed objects and the
incremental fetch three loose/three packed objects, without changing GC or
fetch policy. The five-repeat normal selector passed in 13.483s, three-repeat
race in 11.147s and pinned lint with zero issues; alias, drift, closed/invalid
custody, environment, cancellation and exact fixture cleanup regressions pass.
Exact implementation `f5e121c476e9eee9b7269c46dcbfd33c4221cf8e` passed
independent manual source/documentation/cost review with all severity counts
zero. Its clean detached tree passed new normal (4.277s) and race (5.262s)
selectors, repository build, module verification, scoped vet, pinned lint,
documentation and glossary checks. An optional secondary review tool timed out
with zero completed files; that attempt is not passing review evidence and was
not retried. This closes the scoped Git-resource slice only, not full
input/bootstrap admission, author/executor/launcher readiness or a freeze.

T42.2j now implements protected SDK/module/source custody and reference builds
that consume those exact trees. The real native SDK plus tiny committed source
and one independently verified offline module passed normal (94.967s) and
race (112.763s) acceptance, including poisoned ambient settings, forged original
ziphash and original-cache mutation after copy. The race fixture retained
12,903 inventory entries, 11,536 regular files and 208,496,524 logical bytes;
construction took 87.604s and the protected reference recipe 15.052s. Bounded
tree/path/module, symlink, ancestor, hardlink, FIFO, sparse/aggregate overflow,
partial cancellation and constant-descriptor checks pass; pinned lint reports
zero issues and affected vet passes. These are pre-review implementation
results, not the full repository module footprint, full admission or freeze
readiness. Immutable-source independent review and exact-tree gates remain.
Exact source `5a67267aa7daa640d4a0e4bd36db9c740a3997d2` subsequently passed
independent review with all severity counts zero and clean detached normal
(114.883s), race (120.013s), repository build, module verification, scoped vet,
pinned lint, documentation and glossary gates. Its reference-build timer begins
after the build mutex, initial inventory check and selected-image copy; the
owning outer stage must separately bound the whole public call and cleanup.
These results close the scoped build-input slice, not full-repository admission.

T42.2k's early bootstrap and sixteen source-owned production adapters are
implemented, not yet the genuine parent launch/rehearsal. Real inherited
Phebs/author probe processes pass both-endpoint binding, alias/swapped endpoint,
wrong recipe/site/zero budget, cancellation and bounded Close regressions. An
isolated command-boundary test exposed an absent-FD path that could adopt the Go
runtime's poller descriptor; native socket/parent checks now precede ownership,
and the exact refusal returns normally. Exact version-output overflow kills and
joins only its owned child. Dispatch package normal five repeats (4.022s),
race three repeats (3.830s), command-boundary three repeats (1.173s), Linux
cross-build and pinned lint pass. The seven affected Git/source/candidate/
extraction/repository/catalog/sync packages also passed full normal and race
checks; the largest race result was extraction at 84.275s. The strengthened
typed inventory checks each fixed site constant and counts internal forwarding
boundaries. These intermediate results do not replace exact-tree independent
review, full store/command gates, genuine protected-parent Phebs/Surreal
readiness, semantic owner quiescence or full admission/freeze.
Independent review of exact `8e4d1a0a9733e52ef03dc232806e2f551e99bb55`
against the protected-input base found zero critical/high/low and two medium
findings: pre-Start admission refusal bypassed exec-owned pipe cleanup at eight
pipe sites, and serve's exiting flag parser bypassed lifetime cleanup on help
or invalid arguments. Both correction paths are active; neither is waived.

The next semantic-owner slice preserves complete job persistence/report and
HTTP/auth/session tails, with a live-owner drain before mechanical Pause.
Observation and next-phase preparation windows remain dispatch-enabled and
private-token-bound; fencing joins complete requests before accounting closure.
The focused owner-control selector passed normal (0.487s) and ten race repeats
(3.532s), including successive real children while an owner drains, late tails,
phase/window-token refusal and bounded missing-registration/refusal paths.
Owner hooks across store, generation, authentication, lifecycle and sync passed
focused normal/race; whole maintenance and all-connection resync regressions
passed five normal repeats (command 0.887s, sync 1.007s) and three race repeats
(command 1.912s, sync 2.201s). Pinned lint across the seven affected packages
reported zero issues. These are intermediate source gates; immutable review,
genuine Phebs/Surreal handoff and full preparation/executor gates remain open.
The serve parser finding is corrected through standard non-exiting parsing and
owned main return. Real authenticated help/error regressions preserve exit
0/2 only after successful cleanup, while unfenced cleanup failure returns 1.
Five focused normal repeats passed across dispatch/store/generation/auth/
lifecycle/sync/command (0.292/1.198/0.629/0.896/2.021/2.947/3.452s); the
ordinary/authenticated parser race selector passed three repeats in 12.975s.
The broader owner-hook ordinary regressions passed full command (140.162s),
store runner (135.888s), auth (0.810s), lifecycle (4.611s), sync (67.278s) and
generation scheduler (1.004s). Full store and independent corrected-source
review are not implied by the runner selector.
Independent exact semantic-owner review of
`9a19d34755d3100a77fa57a87d210b1ff0049a3b` found zero critical/high/low
and one medium token-check/request-slot race: a descheduled request could pass
an old token check before drainage and acquire a slot after a later window
opened. One shared atomic entry helper now validates the token and reserves the
bound owner slot in client-to-owner lock order, with no handler work under
either lock. The deterministic interleaving and existing control selectors
passed twenty race repeats in 5.734s; command owner/flag boundaries passed three
normal repeats in 1.532s. Exact corrected-source re-review remains required.
The genuine parent serve slice now accepts only linked protected build/tool
custody, owns a native source-mutation lease, generates its protected config
and performs real inherited bootstrap. Its fixed tiny source and conservative
local command/wire limits are expressly not a full-profile issuer. Source,
parser, lease and local-budget tests passed three normal repeats in 2.786s and
three race repeats in 4.380s; scoped vet and pinned lint passed. The opt-in
full-repository protected Phebs/Zoekt/Surreal indexed-query and complete-stop
rehearsal is written but unrun pending an immutable corrected candidate. Native
session, full-source admission and genuine readiness remain unestablished.
Exact parent review identified a medium healthy-clone custody conflict and a
low receiver-bound wording issue. The parent now generates an escaped file
URL, preserving ordinary watched local transport without the plain-path clone's
source/mirror hardlink aliases. Actual packed/loose clone and retained control
checks passed five race repeats in 7.076s. The receiver's five-second timer now
explicitly initiates cancellation before a cooperative join; it cannot interrupt
native metadata calls, and caller expiry never releases uncertain custody.
Independent corrected-source review and the real full-repository rehearsal
remain required.
The shared pre-Start pipe leak is corrected at all eight affected streaming
sites. Explicit owners close every pipe end even when admission never enters
exec.Start; normal successful streams still join/settle before parent-end
closure. Sixty-four retained commands per refusal/failure/cancellation mode,
borrowed stdio, setup errors, real zero-budget refusal, ordinary/admitted echo,
double Start, first-close failure and canceled-child join regressions pass
without forced GC. All six affected packages passed normal and race; full
dispatch-admission race, focused new-test race five repeats, exact sixteen-site
inventory, scoped vet, pinned lint, format and whitespace pass. These are
working-tree scoped gates; independent immutable corrected-source review and
the real protected Phebs/Surreal rehearsal remain required.
Independent exact pipe review of
`74ec2da442e55f8b150f9218d4f64b5550ce5fc8` found zero critical/high/low
and one medium setup-boundary leak: clearing command stdio then setting up the
same owned pipe again could overwrite the original pair. The eight actual
adapters do not do this, but the shared owner now rejects it and closes the
original pair. Both-stream retained-original-FD regressions are included;
independent exact correction review remains required.
Exact `b93f68cc40fdf8ccab4e9b81fcb28890837388ce` re-review closed the pipe
finding with all severity counts zero. Its clean detached light gates passed
full dispatch normal/race (1.264/2.512s), T42.2 command normal/race
(0.893/4.469s), sixteen-site inventory (1.220s), docs (0.454s), glossary,
module verification, scoped vet and pinned lint with zero issues.
The actual opt-in rehearsal then refused in 0.03s at protected Git admission:
ambient selection found the macOS Git shim, so no protected copy or server was
created; its empty private diagnostic root remains. A subsequent PATH-only
selection failed the test driver's clang/resolv link before the test began.
The test now requires explicit native Git selection without changing compiler
discovery. Neither attempt establishes full-source admission or server readiness.
After explicit native Git selection, exact `31703f1e67c345982a1d8a8f5413524de06faed9`
passed Git custody but refused full Go-input admission in 73.80s, before any
reference build or server launch. Its exact source/SDK and first module-control
copies remain retained. The first generated-module .info contains an explicit
zero timestamp accepted unchanged by both the selected Go 1.26.5 implementation
and a real offline module lookup. The normalizer now preserves that observed
value while refusing missing/null time; all version, shape and independently
verified h1 boundaries remain. Corrected-source review and a new bounded real
admission attempt remain required, with no source-admission or readiness pass.
Exact correction `a438a51eb9d25be45413ec1b75990060529261db` then passed
independent review with all severity counts zero, focused exact-tree race three
repeats (1.967s), pinned lint, docs (0.489s), glossary and whitespace. Its one
new bounded genuine-parent rehearsal uses the explicit native Git input;
completion is not yet established.
That exact a438a51e rehearsal stopped after 127.91s during module admission,
again before reference build/server launch. Its retained prefix establishes the
next cause: a historical module with only a committed go.mod sum had an ambient
cached content directory; the constructor incorrectly refused that irrelevant
directory after admitting its valid descriptor. The corrected path skips content
and ziphash admission without a committed content h1. Required missing content
still fails the actual offline graph/build, and verified-content tamper checks
remain unchanged. Exact review and a new bounded rehearsal remain required.
The module-content correction and its test-only delayed cleanup registration
passed independent review with all severity counts zero. Exact
`b62024847fd85ccf88597cc7617636059b59c502` passed targeted race three repeats
(2.024s), pinned lint, docs (0.441s), glossary and whitespace; its next genuine
parent rehearsal is running, not yet a readiness result.
That exact `b62024847fd85ccf88597cc7617636059b59c502` genuine-parent
rehearsal then passed in 807.55s (808.083s package): complete protected source/
SDK/module custody admitted 67,406 entries, 55,689 files and 1,352,503,293 bytes
in 7m4.495s; supplied Phebs/Zoekt builds took 43.390s/12.948s, independent
reference admissions took 2m11.333s/1m58.661s, and actual Phebs/Surreal indexed
query plus owner-drained stop took 31.335s. The actual prefix admitted 16 calls
(Git 13, Surreal 2, Zoekt 1, compatibility 0), reserved 4,224 DA01 bytes and
closed with native root/session joins. Successful exact fixture cleanup passed.
This closes the small launch gate for those source bytes, not later semantic
server wiring, full executor/launcher acceptance, integration or freeze.

The next bootstrap slice adds a closed, digest-bound semantic-launch selector
for Phebs owner-control mode, while retaining empty-mode rehearsal and author
separation. A locked copied snapshot exposes only its parent-bound input and
current producer/phase/window; selected failure never falls back to ordinary
execution. Inherited normal/author/semantic/refusal paths pass race three repeats
(1.752s), scoped vet and pinned lint. Main input/config/epoch validation, native
phase wiring and exact review remain open; this is not semantic admission or a
freeze by itself.
Exact semantic-bootstrap source `fcb0bb905b567e4dd801bde8beec33d3cb8a8288`
passed independent review with all severity counts zero; the final expanded
inherited/snapshot race three-repeat selector passed in 1.681s. The earlier
1.752s run covered the inherited selector before the additional snapshot unit
test. The prospective V3 marker-contract correction through `a9e84e3a` also
passed independent review with all severity counts zero.
The lifecycle control seam now parks/drives the same existing runner and Gate,
synchronously arms its existing collector, and preserves cadence/cursor/recovery
state. Its exact-only controlled first wake and bounded pending-turn delay do
not change ordinary hourly/backlog behavior. The combined existing/new normal
three-repeat selector passed in 13.598s and race three-repeat in 14.635s before
two narrow cancellation/panic guards. Final new-control normal five-repeat and
race three-repeat selectors then passed in 1.830s and 2.330s, with vet, pinned
lint and whitespace clean. One root inspection caught the panic-defer nil-error
reply; the corrected real controlled-runner regression preserves panic while
returning explicit failure. Independent exact review, final combined gates and
authenticated main/parent phase wiring remain open.

T42.2l's current-revision author core now reuses the frozen source generators
and one admitted native Git boundary. Actual tiny A → B → A-return replay
records exactly 4/7/10 accepted commands, independently checks parents, trees
and streamed census, and proves future commits absent at earlier boundaries.
Missing/changed refs, config/root drift, cancellation and failed stream paths
retain source and join every started command. Five normal repeats passed in
5.617s, three race repeats in 5.762s; affected vet and pinned lint pass. The
bounded response is below 4 KiB for the tiny fixture. Cross-process parent-bound
input/previous-result continuity, actual CLI, full-population execution and
independent exact-source review remain open; no future phase or ceremony ran.
The authenticated request/resume and actual author CLI are now implemented.
One private digest-bound request names the protected V3 plan and exact native
parent-owned root, and binds only the preceding response actually received by
the parent. The command has no unbound or tiny execution mode; tests use the
same private resume machinery for tiny actual A/B/A-return subprocesses, not a
full-population CLI run. Normal three repeats passed in 17.334s (author),
0.658s (dispatch) and 1.118s (CLI); race three repeats passed in 145.998s,
2.859s and 2.754s respectively. Full-shape response bounds passed in 0.514s.
The actual author's exact-source reference-role routing and wrong-command
refusal passed three repeats in 0.667s; the absent executor remains refused.
Independent review, real parent author custody, full CLI execution and complete
executor/admission/launcher acceptance remain open.
Independent exact-core review of `23281900b5f0e3c7879eeac1b28808989bd87ce1`
found zero critical/high/medium code findings and one low owning-cost omission.
PLAN now distinguishes the two new addition-generator passes and their bounded
temporary T41/catalog/mapping allocations from the small retained descriptors;
no source or frozen bytes changed for this correction.
Independent exact request/CLI review of
`8edb2b0573d144307273a798ac2c7b66765df428` found zero critical/high/medium
and one low cost/cancellation wording issue. The owning row now records full
plan regeneration and addition hashing on every author process, with context
checks around rather than within the existing contextless generators. Actual
parent process deadlines and uncertain-custody retention remain mandatory.
The genuine parent author vertical now binds the observed plan/integration/
execution ancestry, owns the source lease and fresh source/home/tmp roots,
and selects actual A → B → A-return CLI calls internally. Each successful
operation requires the real canonical response, checkpoint acknowledgement,
natural close, one native Wait, empty recorded session, exact stdout EOF and
parent-rechecked source controls. Failure retains its accounting/source prefix
and cannot advance or retry. Light normal three repeats (4.618s), race three
repeats (6.815s), final race (3.199s), related request/inventory regressions
(4.149s), vet, pinned lint, formatting and whitespace pass. Independent exact
review and genuine reference rebuild/full-population CLI execution remain open;
this is not full executor/admission/launcher acceptance or a freeze.
Independent parent-author review found no critical/high/medium code issue and
one low owning-cost omission: the source pre/post continuity reads, their author
mutex hold and the 16-byte observed plan-source field. The owning cost now lists
the five successful continuity passes and their bounded controls/inventories.
Source-identical re-review remains required.
The exact parent-author cost re-review through `c097fc3c` closed all severity
counts at zero. An opt-in actual full-population author rehearsal now composes
genuine input/reference admission, a private unsealed V3 plan and all three
actual CLI calls; its default disabled selector compiles/skips cleanly and lint
passes, but no full CLI run is claimed. A separate inherited-FD test of the main
semantic inlet exposed a shared pending author-stdio defect: inherited standard
files do not automatically support Go deadlines. Both author stdin/stdout and
parent endpoint ownership must use genuine pollable native transport before
the full CLI rehearsal; no deadline is waived or false readiness recorded.
That shared transport correction is implemented in both the actual author and
its parent: four owned Unix socket pairs, pollable duplicates, joined
cancellation callbacks, request half-close and post-Wait exact response EOF.
Actual inherited fd0/fd1 completion/cancellation and wrong-descriptor regressions
pass. Author normal three repeats took 0.848s and race three repeats 12.031s;
parent transport race three repeats took 3.213s; vet, pinned lint and whitespace
pass. Independent review and the real full-population CLI run remain required.
Independent transport review found one medium cancellation/deadline ordering
gap in the parent. Both future deadline setters now check the caller context
before admitting I/O; cancellation cannot be overwritten and followed by a
blocked operation. Deterministic socket regressions, normal five repeats
(4.080s), race three repeats (5.967s), vet, lint and whitespace pass, and the
independent correction source review found no remaining issue. Exact immutable
attribution and full CLI execution remain required.
Two independent call-graph reviews identified and resolved the return-A epoch
conflict prospectively for V3: its server already runs in epoch three when the
marker is produced, and epoch four belongs to process_restart. V3 now explicitly
requires same-attempt publication unwind, exact selected-marker recovery under
the existing exclusive mutation fence, recovered C5 observation before selector
advancement, and the actual controller Advance before one successful completion.
This is not startup/crash recovery; the later hard-death proof is unchanged.
V1/V2, two C5 reports, target/result predicates, zero requeues, one success,
deadlines and all numerical bounds remain exact. Native guard/recovery costs
remain separate from the read subtotal. Runtime wiring, counterexamples,
independent exact review and complete receipt/size gates remain open.
The selected native recovery primitive is now implemented over the existing
recovery engine. It checks the captured exact binding/prior pointer/marker,
installed complete target and actual live attempt before mutation, preserves
the durable result if its observer fails, and refuses replay after marker
consumption. Retained ordinary stage/startup/historical recovery stays unchanged.
The final selected/retained normal selector passed in 33.082s, expanded selected
and transition/runtime race in 37.009s, with package vet and pinned lint clean.
Its owning cost separates the extra bounded binding/pointer/metadata and healthy-S2/worst-S65
lease checks from existing full-generation recovery and the C5 observations.
Independent exact review and main same-attempt/Advance/error wiring remain open.
The concrete main marker helper now calls actual HandleV3, exact selected
recovery under the exclusive fence and actual Advance after release. Its two
read barriers cannot release on body production alone; a separate successful
after-report callback is required. Normal five repeats (0.900s), race three
repeats (1.792s), vet, lint and whitespace pass for helper/barrier/refusal tests.
Independent source read-through found no blocker in that bounded slice; actual
main routing, after-report integration and a successful live graph through the
scheduler's own completion remain open. No engine or ceremony ran for this gate.
The actual epoch-three worker and both fixed HTTP routes are now wired to that
helper. The shared response finalizer invokes its completion only after body,
ledger and synchronous report, preserves legacy final-cache ordering, and
latches native completion failures in the existing state. Focused normal three
repeats (16.957s), broader read/semantic/marker race three repeats (25.482s),
final marker/legacy route race three repeats (7.368s), vet, pinned lint, format
and whitespace pass. Independent immutable review and the successful actual
graph through native Advance/scheduler completion remain required.
Independent exact source/test/docs/cost reviews of e260da34 and 35b7efa7 each
closed all severity counts at zero. The actual full graph through selector
readiness and scheduler completion remains unestablished; Advance may truthfully
return pending, and source review is not that native readiness result.

The initial delegated-run host observation found 74 GiB backing space available
against the unchanged 120-GiB freeze prerequisite. Safe bounded implementation
continues; freeze remains closed until real host admission satisfies that bound.
Only validated disposable task build scratch may be reclaimed without a new
scope decision; retained evidence, validation lineages and user data remain.
The later bounded cleanup used Go's own cache cleaner on 73 explicitly
validated Phebs task build caches, including four whose regular cached Go test
executables were independently identified. It removed only reproducible cache
contents: no source, protected/retained binary, proof, module cache, worktree or
user-global build cache was removed. The resulting host observation was
122 GiB available. Cache contents can be regenerated; admission must remeasure
the unchanged 120-GiB prerequisite, and this observation is not host freeze.

**T42.3 · Scale posture decision and neutral product closure** — independently
review the T42.2 receipt, replay representative All code → service →
relationship → Workbench → proof → MCP flows, and record one decision:
single-node envelope retained, bounded cohort experiment required, P6
investigation required, or program stopped. AC: UI/API/MCP authority receipts
remain exact across pagination and concurrent publication; operations status
shows source-free progress/refusal/pressure; every unsupported/partial/stale
state remains visible; topology selection follows the frozen rule and cannot
raise an admission bound; docs distinguish mechanics, neutral scale evidence,
target operating evidence, validation, and release. Epic 42 closes with a
source-free digest-pinned demo receipt, but `GATE2-V2` and `DO_NOT_RELEASE`
remain unchanged absent a separate sealed validation and release program. The
closure cannot be cited as commit-cadence, catch-up, or freshness evidence.

## Epic 43 · Charter-governed product experience *(completed 2026-08-08 → [BACKLOG_COMPLETED.md](./BACKLOG_COMPLETED.md))*

Twelve charter-gated presentation tickets, T43.1–T43.12, closed by the
T43.12 motion pass and charter closure. The closure record
(`spike/t431/CLOSURE.md`) binds all 38 T43.1 ledger findings to their
resolutions — zero blockers open — and re-scores the T43.1 instrument at
37/40 against the 23/40 baseline. Receipts: 118 baselines (29 routed
surfaces × 2 themes × 2 densities + the themed sign-in pair), cardinality
derived from the manifest by test. Post-closure residue, PR-sized and
unscheduled:

**T43R.1 ✅ · Type-floor adjudication (F14)** *(completed 2026-08-30 →
[BACKLOG_COMPLETED.md](./BACKLOG_COMPLETED.md))* — the closed interface floor,
implementation and explicit retained-receipt merge exception are recorded in
the completed backlog.

**T43R.2 ✅ · Commit diff density (F34)** *(completed 2026-08-30 →
[BACKLOG_COMPLETED.md](./BACKLOG_COMPLETED.md))* — the per-file Commit diff
presentation, bounded cost, responsive evidence, and explicit retained-receipt
merge exception are recorded in the completed backlog.

**T43R.3 ✅ · House confirm dialog (S1)** *(completed 2026-08-31 →
[BACKLOG_COMPLETED.md](./BACKLOG_COMPLETED.md))* — the exact link-guard and
dialog boundary, bounded cost, responsive review, and retained 32-route/
130-baseline receipt evidence are recorded in the completed backlog.

**T43R.4 ✅ · Bounded errors everywhere (S2)** *(completed 2026-08-31 →
[BACKLOG_COMPLETED.md](./BACKLOG_COMPLETED.md))* — the shared 512-unit
failure contract, History/Analytics state truth, bounded cost, targeted
responsive evidence, and exact retained-receipt nonclaim are recorded in the
completed backlog.

**T43R.5 ✅ · SectionHelp deployment (S3)** *(completed 2026-08-31 →
[BACKLOG_COMPLETED.md](./BACKLOG_COMPLETED.md))* — the canonical catalog/Git
help deployment, bounded cost, exact authority corrections, responsive review,
and explicit retained-receipt-gate completion exception are recorded in the
completed backlog.

## Epic 44 · Reading surfaces and instance chrome *(planned 2026-08-08 · presentation track)*

Four operator-requested reading and chrome improvements, planned with the
Epic 43 governance intact: charter-gated, presentation-only, per-ticket
impeccable critique, per-ticket bundle and interaction cost records.
Decisions settled at planning: the highlight palette is a Settings-level
preference (never per-code-block chrome); markdown defaults to source with
preview opt-in; the instance surfaces become three separate header icons.
Skills deployment: `ui-ux-pro-max` palette curation (T44.2) and
navigation/icon guidance (T44.5); `emil-design-eng` segmented control and
icon-button details (T44.3/T44.5); `frontend-design` for the preview — the
product's first Read-mode surface (T44.3); `impeccable` remains the
critique instrument; `/security-review` gates T44.3 and T44.4.

**T44.1 ✅ · Proto and citation highlighting** *(completed 2026-08-31 →
[BACKLOG_COMPLETED.md](./BACKLOG_COMPLETED.md))* — Protobuf/Thrift language
loading, shared bounded citation highlighting, exact-caller identity and race
fences, bundle/interaction cost, and the explicit retained-receipt-gate
completion exception are recorded in the completed backlog.

**T44.2 · Highlight palette preference** *(needs T44.1)* — a curated
palette registry over the single highlight module. AC: the current
palette ships as the default plus at least three curated alternatives,
each defining all seven roles in both themes; every palette passes the
≥4.5:1 code-background contrast gate, pinned in theme.test alongside the
status tones; a Settings · Appearance card (select plus live code
specimen) governs the choice, persisted per browser like density and
never in the URL; the viewer, search chunks, and citation spans re-color
without reload; receipts capture the default palette only, plus the
Appearance card in the settings baselines; bundle and interaction cost
recorded.
*Shipped 2026-08-08.* Registry in `ui/src/palette.ts` (Phebs default +
Quiet, Classic, High contrast); `PaletteContext` persists the choice
per browser, unknown values fall back to default. The AA gate lives in
theme.test — every role × palette × mode ≥4.5:1 on pageBg and the
anchor-line tint, high-contrast ≥7:1 on the page — and it caught the
original default's sub-AA comment grays, now corrected. Viewer, search
chunks, and citation source re-color live; a Settings · Appearance
card with a lazy live specimen governs it. Bundle: palette data is a
0.6 kB gzip chunk; the specimen and citation share the existing lazy
highlight/lang chunks (no CodeMirror in the initial or settings
chunk). Receipts: settings re-baselined with the card at the default
palette. Suite 491/491. Review follow-up (T44.2f): the AA gate now
covers the search-match background (matchBg) too — matched syntax text
renders on it — which caught five comment/operator grays across the
default and Quiet palettes failing on the amber/brown match tint;
corrected with sub-perceptible lightness nudges that clear all three
surfaces. The persisted-value guard uses an own-property check, not
`in`, so inherited names (constructor, __proto__, toString) fall back
to the default instead of indexing to undefined; pinned by
palette.test.ts.

Closure audit (2026-08-31): Settings token rows are now bound to their exact
theme/palette identity and fall back to the plain specimen during every new
lazy tokenization, so old colors cannot be relabelled or stranded after a
failure. Same-mounted File, Search, relationship-citation, and exact-caller
tests prove live recoloring without restarting or refetching their underlying
work. The AA matrix now covers `pageBg`, `bandBg`, `hoverFill`,
`selectedLineBg`, and `matchBg`. The receipt harness explicitly seeds the
Phebs default and waits for specimen readiness on both Settings routes. The
complete 753-test UI suite, lint, typecheck/build, and desktop/390px browser
checks pass; the production JavaScript delta is +114 raw/+62 individually
gzipped bytes with no dependency or chunk. Twelve local browser-driven
transitions settled in 56–109 ms (60-ms median), recorded as mechanics rather
than an SLO. The manifest stays at 32 authenticated routes/130 PNGs and no
baseline byte changes. The targeted eight-comparison retained Settings run
stopped before comparison because the stored session was stale and receipt
credentials were absent. T44.2 therefore remains open: no clean retained
comparison, baseline refresh, completion, merge, or integration is claimed.

Retained-comparison follow-up (2026-08-31): receipt setup now validates a
saved Secure loopback session through Chromium, matching the browser path that
captures the receipts instead of incorrectly rejecting it through an API-only
request context. The authenticated retry against the current branch UI reached
all eight Settings comparisons. All four 390px theme/density variants pass.
The four desktop variants differ by 1,200 pixels in light mode and 1,242 in
dark mode, confined to the noncanonical operator identity and two retained
pre-T43R.1 label bands; the Code Navigation region has zero differing pixels,
and the visible palette specimen is pixel-identical to the retained specimen
and already highlighted in every variant. No baseline byte changed. This
supersedes the authentication blocker only: T44.2 remains open without a clean
retained comparison, baseline refresh, completion, merge, or integration.

**T44.3 · Markdown source and preview** — a Markdown | Preview segmented
control on the file viewer for markdown files. AC: view state in the URL
(`view=preview`), source is the default, and `?L=` line deep-links force
source; rendering via marked + DOMPurify in a lazy chunk loaded only on
first preview activation; sanitization is a hard boundary over untrusted
repository content (no script or raw-HTML pass-through; link policy
applied) and `/security-review` passes before merge; repo-relative images
render as named placeholders in v1 — deferred honestly, never
half-fetched; the control is keyboard-complete and announced; receipts
add a markdown-preview fixture route in both themes and densities;
bundle record names the lazy chunk size with the main chunk unchanged. *Implementation landed 2026-08-08; the receipt clause is
deferred honestly.* Renderer in `ui/src/markdown.ts` (marked 18 +
DOMPurify 3, own instance, strict allowlists, link/image hardening),
consumed by FilePage's MarkdownPreview via dangerouslySetInnerHTML only
after sanitize; source default, `?view=preview` opt-in, `?L=` forces
source. Independent adversarial security review found no HIGH/MEDIUM
exploitable issues; the isolated-instance defense-in-depth note was
applied. 10 adversarial sanitization unit tests + component view-fork
tests; live-verified against a real README (headings, lists, safe
links with rel/target, no image fetch). Bundle: 23 kB gzip lazy chunk,
loaded only on first preview; initial chunk unchanged. Receipt
deferred: no frozen markdown fixture exists (all corpus .md are in the
non-baseline-stable upstream zoekt repo) — the receipt route lands when
a frozen fixture does; behavior is pinned by tests meanwhile. Suite
565/565.
Review follow-up (T44.3f): the prose styles were scoped under a fixed
`.phebs-md` wrapper (they had leaked as global bare-tag rules — 12,
reproduced live, now zero); the sanitizer dropped `class` and set
ALLOW_ARIA_ATTR:false (author redressing/aria injection, both pinned by
adversarial tests); and synchronous rendering is bounded to 128 KiB
before the lazy import so a large document offers source rather than
freezing the tab. The image placeholder no longer depends on a
surviving class (DOMPurify 3.4.13 re-cleans hook-inserted nodes) — it
is now inert 🖼+alt text. Suite 570/570. The review header cited four
findings but only three reached the track (the third truncated, the
fourth absent); any fourth is a separate follow-up.

Closure follow-up (2026-08-31): the deferred receipt is closed by a
page-scoped synthetic repository fixture that intercepts only its exact
auth/status/source/folder identities and fails closed on an unexpected
fixture identity, without adding a repository to the shared cohort. The
manifest is now 33 routes/134 PNGs; all four new theme/density baselines were
reviewed and pass a fresh isolated comparison. Preview parsing moved to a
one-shot Worker with a 131,072-UTF-16-unit input cap, 1-s deadline,
524,288-returned-unit cap, at most 41 validated segments, and unconditional
termination. The
parser lexes once and preserves document-wide reference definitions across
Mermaid fences. The sanitizer remains main-thread and isolated; empty,
relative, and unsafe links become prose, images become neutral named
placeholders, and returned segments are validated before sanitization.
FilePage binds loaded bytes and preview state to the exact repository, path,
and revision so a late response cannot appear under a new route. Preview
prose uses a 72-character reading measure; the mobile view control and
diagram-source disclosure have 44px hit regions. The complete 774-test UI
suite, lint, typecheck/build, 4/4 targeted retained comparisons, and
1280px/390px browser checks pass. The browser checks cover keyboard URL
state, Back/Forward without a source reread, exact source identity, safe
Mermaid rendering, resource refusal, accessible source disclosure, no
external request, no stranded Mermaid host, a clean console, and no document
overflow. The full unrelated authenticated matrix was not rerun because the
saved session does not match the current backend and credentials were absent;
the page-scoped route is the canonical isolated T44.3 contract. T44.3 is
complete on this branch; T44.2 remains its stacked integration dependency.

**T44.4 · Mermaid rendering with ELK** *(needs T44.3)* — mermaid fences
in preview render as diagrams. AC: mermaid 11 with the ELK layout as the
default, themed from the design tokens, `securityLevel: 'strict'`, no
click handlers; mermaid and the ELK engine load as one async chunk
fetched only when a rendered document actually contains a fence; a
failing fence renders its source as a highlighted code block with a
one-line error — never a blank; diagrams stay out of the screenshot
matrix (SVG output is not render-stable) and are pinned by unit tests
over structural properties of the rendered SVG; bundle record names the
lazy chunk size.
*Shipped 2026-08-08.* Fences are split out at the marked-lexer level
(`segmentMarkdown`) before the HTML pipeline — the T44.3f sanitizer
strips class so fences can't be found post-sanitize, and diagram source
must never ride the HTML path; only top-level fences convert, nested
ones stay code blocks. Renderer in `ui/src/mermaid.ts` (mermaid 11 +
@mermaid-js/layout-elk), one async chunk imported only when a rendered
document has a fence; posture in `ui/src/mermaidConfig.ts` pinned by
tests without loading mermaid — strict mode, htmlLabels false, ELK
layout, no auto-start, themed both modes. Verified live: an ELK
flowchart renders with 0 foreignObjects/scripts/onclicks in light and
dark; a failing fence keeps its source with a one-line reason.
Diagrams excluded from the matrix (SVG not pixel-stable), pinned by
segmentation/config/fence-path tests. No screenshot receipt was
added: a markdown fixture had to be a real corpus repository to render,
and adding one to the shared dev instance corrupted unrelated
cross-repo receipts (repos gained a row; the authored
search-results-authority query matched the fixture's prose) — the wrong
trade, so it was withdrawn. T44.3's markdown receipt therefore stays
deferred until a dedicated isolated-corpus harness exists; the surface
is pinned by segmentation/config/fence-path tests and verified live
this session (ELK flowchart in both themes; 0 foreignObjects, scripts,
or click handlers). Bundle (corrected in T44.4f — the earlier ≈16 kB
figure was wrong): the mermaid entry chunk is ~12 kB gzip, but
rendering a fence dynamically pulls the ELK layout engine (~431 kB
gzip) plus per-diagram and shared chunks — all lazy, fetched only when
a document actually contains a fence.
Review follow-up (T44.4f): closed a directive-override bypass —
untrusted `%%{init}%%` directives and `config:` frontmatter could flip
layout to dagre and htmlLabels to true (restoring foreignObject HTML),
defeating the ELK/SVG-text contract. The renderer now refuses
directive/config-frontmatter fences before rendering (shown as
source), validates the rendered SVG for no foreignObject/script/event
handler after, and locks policy keys via mermaid's `secure` list.
Aggregate work is bounded: at most 20 fences render, the rest stay
source and never import the engine. Mermaid cannot render in jsdom, so
the predicates and the wrapper refusal are unit-tested on the exact
override payloads and verified live (normal fence → ELK diagram, 0
foreignObjects; hostile fence → refused, source shown).

T44.3 closure hardening (2026-08-31): the post-SVG check was not early enough
for Mermaid 11.16.1 paths that allocate live resources while drawing. The
wrapper now refuses every property bag, the positional-sprite C4 family,
authored styles/backslash escapes, raw/entity HTML, Markdown images, KaTeX,
URL schemes, and imports before render; config frontmatter is title-only.
Returned SVG permits fragment-local paint servers/symbol references and
refuses every other URL/href, resource element, foreignObject, script, and
event attribute. Mermaid's global renderer is serialized to one active plus
20 pending jobs, queued stale work is abortable, and every attempt owns one
offscreen layout host removed on both success and failure. A successful
diagram keeps a native keyboard-reachable source disclosure; refused and
failed diagrams keep their source. The entry chunk is 39,440 raw/13,310
deterministic-gzip bytes at this branch (+1,715/+709 over the stacked base);
the large inherited ELK/per-diagram assets remain lazy and fence-gated.

**T44.5 · Header instance-surface icons** *(last — re-captures the full
matrix once)* — Audit, Analytics, and Settings are instance surfaces,
not corpus surfaces; they leave the text nav. AC: three separate icon
buttons right of the nav (admin gating unchanged), each with
aria-label, title, and an aria-current active treatment; a separator
between the instance icons and the session cluster; the corpus nav
drops to seven text items; command-navigator entries and route titles
unchanged; the 390px strip verified; the workflows guide's header
description updated; the full receipt matrix re-captured in one
reviewed update.

## Epic 45 · Bazel-first managed SCIP generation *(planned after Epics 40–42 · cross-track)*

Phebs is purpose-built for massive, service-dense monorepositories. This epic
targets Bazel-managed Go repositories with at least 5,000 logical services and
source trees in the 12-GB class. As defined in
`docs/SIZING_ASSUMPTIONS.md`, that means 5,000 simultaneously accepted service
incarnations in one exact repository/catalog generation and at least
12,000,000,000 declared Git blob bytes across unique regular source paths
admitted by one frozen HEAD indexing profile, with generated, vendored, test,
excluded, gitlink, and external-repository treatment reported separately. The
separate neutral headroom target remains 10,000 accepted services over the
two-million-eligible-Go-path profile. These are design requirements, not
claims that the current release has passed a target-corpus scale, freshness,
accuracy, or operating gate.

The shipped committed root and focused unit-bound one-blob readers remain exact
compatibility paths. Their 64-MiB blob and monolithic semantic/cache limits are
current scoped refusal boundaries, not the selected product-scale typed-index
topology. Epic 45 must make managed generation and serving partitionable,
resumable, and complete-by-manifest without multiplying source/type work by
service count or requiring one whole-repository package graph or one in-memory
SCIP snapshot.

### Boundary

- Bazel is the first managed indexing provider. The initial feasibility route
  is the upstream-documented `scip-go` Go Packages Driver Protocol with the
  `rules_go` `gopackagesdriver`, but only after a separately sealed
  Bazel-native plan identifies the configured target and package universe. The
  driver is package-loading input, not target-universe or completeness
  authority; the route remains a path to validate against the exact target
  repository/configuration, not a pre-established scale claim.
- A provider is an out-of-process, closed-profile executor, not a Go dynamic
  plugin or an extractor capability. Repository BUILD/Starlark/tool execution
  is privileged code execution and is administrator-only initially.
- The browser may select only the repository's current authoritative HEAD,
  installed provider descriptors, and named profiles; the resolved immutable
  commit is read-only evidence, not a historical-revision selector. It never
  supplies an executable path, shell fragment, arbitrary environment,
  credential, cache URL, or raw Bazel flags.
- `GOPACKAGESDRIVER` resolves only to a pinned Phebs-owned audited executable
  or launcher. Repository-provided scripts, launchers, executable paths, and
  environment cannot enter the driver boundary.
- The initial Bazel provider has default-deny network egress and no remote
  cache. Operators supply one immutable digest-manifested dependency/toolchain
  bundle; preflight verifies its count, bytes, and digests and copies it into
  request-private custody before repository code can run. Missing, oversized,
  or mismatched material refuses without execution. No mutable shared cache is
  mounted or reused. Any later network or cache access requires a separate
  dated decision over a closed destination allowlist, credential boundary,
  repository-code exfiltration controls, and new negative evidence.
- One bounded Bazel-native planner, selected and frozen by T45.1a, is the sole
  producer of the configured-target universe and target-to-package-load-unit
  map. Its exact profile semantics must cover configuration transitions,
  aliases, tests, generated/cgo sources, external repositories, and
  many-target-to-one-package mappings. `scip-go`, `gopackagesdriver`, partial
  package output, and filesystem discovery cannot manufacture either layer.
- Bazel `aquery` may contribute bounded planning or diagnostic evidence about
  configured actions/artifacts. It is neither Go-package/service authority nor
  an unmeasured required whole-graph production input.
- A Phebs-managed SCIP bundle contains conforming SCIP `Index` members plus a
  Phebs root/manifest. SCIP field-wise encoding is not called native sharding:
  routing, retry, completeness, lifecycle, and atomic publication are Phebs
  contracts.
- Service identity, service membership, Bazel target/package identity, and
  physical SCIP member identity remain separate. One service is never silently
  turned into one virtual repository or one physical member.
- Completeness binds four independently digested layers: the ordered configured
  Bazel-target universe; the sealed target-to-Go-package-load-unit plan; each
  package unit's canonical document set; and deterministic
  document-to-SCIP-member assignment. **Required unit** means only a stable Go
  package-load unit in that plan. Target, service, repository, document, and
  physical-member identities cannot substitute for it.
- Partial work is progress only. A durable attempt manifest records every
  complete, failed, predeclared-excluded, or unsupported unit, but only an
  attempt whose required units all complete may publish a current bundle.
  Failed or unsupported required units leave the attempt non-current and the
  prior complete generation current; when none exists, navigation remains
  unavailable. A closed profile may qualify current coverage only with
  exclusions frozen before execution and may never relabel runtime failure as
  exclusion.
- Disabled or unconfigured managed indexing adds no child, workspace, scan,
  query, write, poll, lock, cache, or retained artifact. Every enabled-path
  ticket records cold, retry, cancellation, warm reuse/no-op, publication,
  startup/recovery, lifecycle, query, memory, disk, descriptor, and descendant
  process costs.
- Backend implementation belongs to the scale track; Settings implementation
  belongs to the presentation track and requires the flagged handoff in
  `AGENTS.md`. No ticket crosses that ownership boundary silently.

**T45.1a · Closed feasibility harness and Bazel-native plan authority** —
before any command executes in the authorized target repository, ship and
independently review the source-free spike harness as its own ticket. It uses
one exact disposable workspace; a closed scrubbed environment; disabled
ambient system/workspace/home bazelrc discovery with at most one
operator-copied, fully resolved and digest-bound profile configuration;
request-private bounded Bazel roots/caches; default-deny network and no remote
cache; process-tree supervision; wall/RSS/disk/descriptor/output limits;
Bazel-server/persistent-worker shutdown; and exact cleanup or retained-custody
proof. A digest-manifested offline dependency/toolchain bundle is count/byte/
digest verified and copied into that private custody before fixture execution;
a missing, oversized, or mismatched bundle refuses before repository code.
`GOPACKAGESDRIVER` may name only the pinned Phebs-owned launcher and its closed
argv; repository-controlled launchers, executable paths, environment, and rc
files are rejected. On source-free neutral fixtures, select and freeze one
bounded Bazel-native planner: configured-target enumeration through exact
`cquery` output plus a pinned Phebs-owned aspect/provider projection for stable
target-to-Go-package-load-unit and canonical-document mapping. Configuration
transitions, aliases, tests, generated/proto/cgo sources, external repositories,
and many-target-to-one-package edges must be canonical and deterministic;
`aquery`, `scip-go`, `gopackagesdriver`, filesystem discovery, and partial
package output are not authority. AC: freeze the minimal spike-only request and
receipt identity, planner/launcher/profile/tool/prehydration digests, exact
argv and environment allowlists, mapping encodings and bounds, hostile-output
handling, resource/custody evidence, and PASS/STOP criteria; fixtures prove an
unrelated broken target remains outside the requested universe and every
missing/extra/ambiguous target, package, document, or edge refuses. T45.1b
cannot begin until this ticket is merged and independently accepted; inability
to produce the exact plan records `STOP`, not a best-effort fallback.

**T45.1b · Target failure receipt and Bazel/scip-go feasibility gate** *(needs
T45.1a PASS)* — reproduce the failed SCIP attempt without retaining private
source, names, paths, commands, credentials, or raw errors in the repository;
classify whether failure occurs during workspace/tool preflight, Bazel-native
planning, package loading/type checking, global relationship construction,
SCIP serialization, validation, or current Phebs admission. Through the merged
T45.1a harness, first produce and seal the target repository's exact
configured-target/package/document plan, then run the exact pinned
`rules_go` `gopackagesdriver` plus `scip-go` path only through the Phebs-owned
launcher and only over package patterns derived from that plan. Exercise at
least three authorized bounded cohorts: ordinary Go, generated-proto/cgo
posture, and high shared-dependency fan-out, with explicit module/version
identity, named Bazel configuration, and initially reduced `skip-tests`/
`skip-implementations` posture. Include one unrelated broken target outside
the frozen requested universe; it must neither execute nor enter coverage. A
broken required target is terminal and non-current. AC: record exact current
HEAD commit and every T45.1a identity/digest; requested/succeeded/failed/
excluded/unsupported target and package-unit counts; prove driver results equal
the sealed configured-target → package-unit → canonical-document mapping;
record proposed member assignment, output bytes/documents/occurrences/symbols,
process-tree peak RSS, wall, child count, descriptors, private-cache bytes, and
retained scratch/cleanup state; compare definitions, references, hover, and
cross-cohort symbol identity with an independently reviewed small oracle; prove
that `--keep_going`, partial driver output, or one successful cohort cannot
create a complete claim; and record one `GO`, `REDUCE`, or `STOP` decision for
T45.2. The spike changes no runtime behavior, admission cap, UI, release
posture, or current typed-index authority.

**T45.2 · Closed provider/profile and execution-authority contract** *(needs
T45.1b GO)* — generalize the accepted spike identities into versioned provider
descriptors, operator-owned named
profiles, immutable request identity, capability reporting, and bounded
structured refusal/progress vocabulary. One request binds repository
incarnation, the current authoritative HEAD source generation and its resolved
exact commit, provider, profile/config digest, configured-target-universe
digest, Bazel/rules_go/Go/indexer/planner/launcher identities, immutable
prehydration-bundle digest, resource policy, and idempotency key; the planned
successor identity also binds the sealed target-to-package-load-unit map
digest. Historical commit selection remains outside this epic. AC: strict decoding rejects
unknown, duplicate, missing, unsafe, or oversized fields before mutation; only
administrators may plan or execute; ordinary repository visibility grants no
host execution; profiles contain no browser-supplied arbitrary command data;
the driver executable/launcher and argv are Phebs-owned closed fields and no
repository-controlled executable can satisfy them;
ambient bazelrc discovery is disabled; any single admitted operator-copied rc
is fully resolved, closed-field validated, and digest-bound; prehydration
inventory is immutable, bounded, digest-verified, and copied before execution;
output roots and any request-private local cache are identity-bound and
lifecycle-owned; tool
replacement and equal-value A→B→A profile transitions cannot alias;
threat model and configuration guide distinguish the managed child from pure
extractors; disabled steady state is zero-work.

**T45.3 · Immutable Phebs SCIP-bundle and atomic-publication contract** *(needs
T45.2)* — define a small exact generation root over bounded conforming SCIP
`Index` members, document-routing members, symbol-routing members, and complete
coverage outcomes. A durable attempt manifest binds exact
source/provider/profile/tool authority, ordered staged member
names/digests/bytes/counts, configured-target-universe digest, sealed
target-to-package-plan digest, complete package/document/member mapping, and
every terminal complete/failed/predeclared-excluded/unsupported target and
package-unit classification. A separate
publishable bundle root exists only when every required unit is complete and
every exclusion was frozen by the exact profile before execution. AC: every
root, member, document path, symbol key, collection count, encoded byte kind, and
aggregate dimension has a measured reduce-first bound; canonical builds are
byte-identical; duplicate documents, conflicting metadata, unsafe paths,
missing/extra/corrupt configured targets, package units, target/package edges,
package/document edges, document/member assignments, members, or routes,
divergent routing, incomplete coverage, and stale source refuse before
publication; **required unit** is encoded only as the stable package-load-unit
identity and cannot be decoded as a target, service, repository, document, or
member; failed or unsupported required units produce only the terminal
non-current attempt manifest; one atomic fenced
pointer preserves the prior generation until complete replacement; the legacy
committed blob source remains strict-readable side by side; the schema calls
this a Phebs bundle, never a native SCIP shard format.

**T45.4 · Managed exact-workspace executor and durable generation scheduling**
*(needs T45.2–T45.3)* — materialize or bind an exact private build workspace
rather than running Bazel against the bare mirror; execute providers under
pinned executable identities, process-group/descendant supervision, bounded
environment and output, timeout/RSS/descriptor/disk limits, default-deny
network egress, and no remote cache. Dependencies and toolchains arrive only
through the request-bound immutable prehydration bundle: preflight capacity-
checks and digest-verifies a bounded copy into private custody, and any miss or
mismatch refuses before repository execution. Disable ambient
system/workspace/home bazelrc discovery;
admit only the exact closed profile configuration; place output user root,
output base, repository/disk/action caches, server state, and persistent-worker
state inside request-private bounded custody; disable shared caches; and prove
server/worker/descendant shutdown plus lifecycle accounting before cleanup or
resume. Adding any network/shared-cache path requires its own dated security and
cost decision. Add a dedicated durable typed-index request/job
and bounded generation schedule rather than overloading `JobIndex` or encoding
options in a queue target. AC: preflight/planning, member execution,
validation, and publication have independent durable states; one repository
token and initially one execution slot bound concurrency; heartbeat, retry,
fresh successor, coalescing, cancellation, server shutdown, hard death, stale
worker, and exact same-generation resume are covered; late workers cannot
publish across a source/profile/tool fence; cancellation kills descendants and
cleans or inventories scratch without retiring the prior current generation;
capacity refusal occurs before materialization and staging growth. Generated
bundles use **regenerate-on-restore**, not backup transport: backup records only
bounded request/profile intent and never a generated current pointer or member
bytes; restore publishes generated navigation as unavailable, revalidates the
current HEAD/profile/tool authority, and may then enqueue a distinct exact
successor. Startup/recovery and lifecycle inventory every request, attempt,
staging root, bundle root/member, private workspace/cache, prehydration copy,
server/worker state, and current pointer; stale or orphaned bytes cannot route
and are reclaimed only through the bounded owner. Exact backup/restore,
startup, hard-death, pressure, pin, and cleanup tests pass before a reader or
provider may register.

**T45.5 · Generated typed-index source and routed code-navigation reader**
*(needs T45.3–T45.4)* — introduce an exact typed-index publication resolver that
preserves the current Git-blob source and adds generated bundle authority.
Route a document lookup to bounded document members and a symbol lookup to the
exact bounded symbol/member postings; do not scan every member per request.
Cache members independently under explicit byte/count limits and pin active
readers across publication/lifecycle transitions. AC: definition, references,
hover, implementations/relationships, position encodings, deterministic
truncation, unavailable paths, and source conversion retain legacy behavior;
cross-member and shared-library fixtures equal a trusted monolithic small
oracle; malformed one-member and routing attacks fail closed without poisoning
unrelated repositories; cold/warm query costs, maximum member fan-out, held
locks, cache invalidation, and worst-case resident memory are recorded.
SCIP-derived evidence remains unavailable for generated bundles until T45.9.

**T45.6 · Bazel managed provider** *(needs T45.1b–T45.5)* — implement the first
provider using the exact validated `rules_go` `gopackagesdriver`/`scip-go`
route. Preflight detects and reports, but never guesses authority from,
`MODULE.bazel`/WORKSPACE posture, compatible rules_go, toolchain/configuration,
and installed profile. Planning invokes the frozen Bazel-native planner from
T45.1a over the named profile, seals the configured-target/package/document
map, then derives bounded driver package cohorts from that map; the driver
cannot add, remove, or relabel plan authority. AC: canary
and dry-run modes perform no publication; no default run expands to `//...` or `./...` without
an explicit measured profile; tests/implementations default to the T45.1b
posture until separately widened; generated source, cgo, external repos,
configuration transitions, build failures, and driver limitations are explicit
outcomes; unchanged exact commit/profile/tool requests exact-reuse without
another Bazel/scip-go child; changed authority creates a distinct successor;
target-corpus execution repeats the frozen T45.1b cost and correctness gates
before runtime registration.

**T45.7 · Additional managed input options** *(needs T45.2–T45.5; may follow
Bazel closure)* — add only separately validated providers behind the same
publication contract: a standard Go module/`go.work` `scip-go` provider and an
operator-supplied existing-artifact import. AC: provider order keeps Bazel
first for a Bazel-detected/configured repository; module discovery is bounded
and exact rather than recursive best effort; import copies into immutable
staging and verifies bytes/metadata/commit/coverage rather than trusting a
mutable external path; unavailable providers are omitted or explicitly
unavailable, never rendered as functioning controls; no generic shell-command
provider or dynamic Go plugin is introduced.

**T45.8a · Capability-dark Settings boundary** *(presentation-track handoff;
may precede T45.2)* — add an administrator-only **Code navigation indexing**
section that states the current build truth without consuming or implying a
managed-indexing authority. The single provider card is ordered `01 · Bazel
first`, carries the closed blue `Unavailable` state, and says that managed
generation is not registered and committed SCIP artifacts remain the only
code-navigation source. AC: the component makes no request, poll, or mutation;
renders no repository, profile, target/config, resource, canary, dry-run,
executable, environment, credential, cache, raw-flag, plan, retry, or generate
control; does not call or relabel `/api/reindex`; infers no repository support,
index absence, or build configuration; is absent for ordinary users; uses text
and color for state; stacks without document overflow at 390 px; adds focused
unit coverage and a deterministic Settings 390 receipt. This is a presentation
foothold only: it registers no provider/capability, links no File-page state,
and does not satisfy T45.8b or authorize T45.2–T45.7.

**T45.8b · Authorized API and Settings workflow** *(needs T45.4–T45.6;
presentation-track handoff)* — replace T45.8a's fixed unavailable boundary with
an administrator-only **Code navigation
indexing** Settings section with repository/current-HEAD selection and the
resolved exact commit shown read-only,
provider cards in configured order with Bazel first, named target/config and
resource profiles, canary/dry-run, and explicit generation. Add bounded APIs
for provider descriptors, exact repository typed-index/job status, plan, and
enqueue; mutation carries CSRF, audit target, expected revision, request digest,
and idempotency. AC: the UI shows absent/current/stale/planning/indexing/
validating/publishing/failed/canceled states; polls one cheap durable status
projection while retaining last-good state; never renders raw child output or
private paths; retry is exact and a stale browser request is rejected; the
File-page unavailable state links administrators to the preselected Settings
section while ordinary users retain non-actionable `available: false`; mobile,
keyboard, reduced-motion, light/dark, and bounded-error gates pass.

**T45.9 · Generated-SCIP evidence integration and product-scale closure**
*(needs T45.5, T45.6, and T45.8b; T45.7 is optional)* — decide separately
whether generated bundle members may feed SCIP-derived evidence. If GO,
replace the one-blob extractor capability
with a bounded exact member iterator whose facts still cite immutable source
reads and whose coverage binds the complete bundle root; if STOP, keep code
navigation and evidence postures explicitly independent. Then run one neutral
and one separately authorized target closure covering cold generation, bounded
failure, restart/resume, stale-source fencing, cross-member queries, warm
reuse/no-op, pressure/lifecycle, the already implemented regenerate-on-restore
contract, Settings/API parity, and clean teardown. AC: every design-target claim
names exact corpus/config/tool/host evidence; 5,000+ services and 12-GB-class
source remain the required product class, while a supported numeric envelope
is recorded only from retained measurements; incomplete/error states remain
visible; no release or `DO_NOT_RELEASE` change occurs without a separate
validation decision. The closure receipt uses the exact accepted-service and
admitted-source-byte dimensions in `docs/SIZING_ASSUMPTIONS.md`; aggregate
filesystem size or total catalog rows cannot substitute for them.

## Epic 25 · Embedded documentation browser *(drafted 2026-07-27 · unscheduled nice-to-have)*

Serve the repository's markdown documentation, rendered, from the phebs binary
itself. The tracked `docs/` tree stays the single source of truth: plain
markdown, still rendered identically by GitHub, with no external docs
toolchain, static-site generator, or content fork.

### Boundary

- One new pure-Go dependency (`goldmark` plus its GFM extension); no new
  runtime children and no build-pipeline stage.
- Served behind the existing session/API-key authentication like the UI; no
  anonymous surface and no new capability.
- `docs/fixtures/` and `docs/design_handoff_phebs_brand_and_ui/` are excluded
  from the embedded set; retained records remain repository-only.
- No docs versioning, search, or navigation chrome: `docs/README.md` and
  `docs/MANUAL.md` are the navigation, exactly as on GitHub.

**T25.1 · Rendered docs at their markdown URLs** — embed the tracked docs
markdown, docs SVGs, and `config.example.yaml`, plus root `README.md` and
`PLAN.md`, via `go:embed` (same build-tag pattern as `ui/`); render GFM to
HTML with goldmark once at startup; serve each page inside one branded HTML
shell at its repo-relative path under an authenticated docs route, so every
tracked relative link works unrewritten (`.md` URLs return HTML, SVG and YAML
pass through). AC: every local link the T24.1 contract test validates also
resolves in the served site; excluded fixture and design-handoff bytes are
absent from the binary; the route requires authentication; `make dev` demo —
open the served `docs/README.md`, follow a link into a task guide and the
architecture SVG; dated PLAN ADR bullet in the same PR; full merge bar.

## Epic 26 · SQL schema-set evidence *(drafted 2026-07-27 · unscheduled — spike first)*

Outline relational models from committed repository bytes: declaration-first,
usage-second, joined only through committed binding artifacts. Round one
scopes PostgreSQL and MySQL current-schema snapshots, including a requested
schema-only dump, plus schemas and authored queries bound by a committed sqlc
manifest. A dump alone is sufficient for a citable catalog when its committed
header establishes the dialect; the manifest is the stronger artifact that
deterministically ties an engine, schema files, and authored query files
together without inference.

### Boundary

- Pure reader over committed blobs at the indexed commit. No database
  connection, no `information_schema` introspection, no migration execution,
  no runtime naming resolution.
- PostgreSQL and MySQL are separate dialect lanes. A sqlc `engine` value or a
  recognized dump-generator header establishes the dialect; grammar sniffing
  never does. Headerless standalone schema files require a committed binding
  manifest. No assertion or measurement merges identities across dialects.
- Three evidence planes, no more:
  - `sql-schema` — current-schema declarations from a committed schema-only
    dump or from inputs explicitly listed by a committed sqlc manifest; the
    only source of `DECLARES_TABLE` / `DECLARES_COLUMN` / key, index, and
    FK-edge facts. Evidence records whether the source is an authored schema
    input or a captured dump; either proves only what the committed bytes say,
    never the live database. A dump without a query-binding manifest produces
    the catalog but no usage join.
  - `sql-query` — authored `.sql` query definitions producing read/write
    relation references. Generated `.sql.go` is never a primary usage source;
    it duplicates the authored query and joins later via generated-from
    evidence.
  - `sql-migration-event` — per-statement migration history
    (`MIGRATION_CREATES_TABLE`, `MIGRATION_ADDS_COLUMN`,
    `MIGRATION_DROPS_TABLE`, …) with **no current-shape claim**. No fold in
    round one: a folded "current schema" has no single citable blob under the
    extractor's streaming contract, and repeated alterations of one column
    need occurrence-distinct identities because assertion identity excludes
    detail.
- Shared join lineage `sql_schema_set_v1(repo, manifest-or-migration-root,
  dialect)`. An unqualified table literal stays a named-table reference until
  exactly one declaration set selects it; multiple candidate roots refuse as
  `ambiguous-schema-set`, never a repo-wide guess.
- Identifier canonicalization is dialect-specific. PostgreSQL quoted and
  unquoted identifiers retain their distinct rules. MySQL source spelling and
  quoting are preserved; absent committed server configuration, phebs never
  assumes a `lower_case_table_names` value or joins case variants.
- A repository with no admitted schema artifact reports
  `schema-artifact-missing` and may produce a checklist action to request a
  schema-only dump. The request and any human-supplied capture metadata are
  workflow state, not schema evidence; facts begin only when immutable dump
  bytes are available to the reader.
- Column references get their own eventual surface
  (`find_sql_column_references` over `(schema lineage, relation, column)`);
  the existing numeric wire-field surface stays byte-stable and untouched.
- Out of scope, stated as decisions: Redis (Epic 28 scopes deterministic
  declaration islands and provable key usage; a universal keyspace model
  stays out); raw document-store dialects (Epic 27
  instead proposes one employer-neutral normalized manifest); Cassandra/CQL
  and explicit-only ORM packs (separate later spikes with their own decision
  tables); any ER visualization (rendering is commodity once facts exist);
  the Workbench SQL resource plane (stays `unsupported` until a proven
  operation → query → table join exists).
- Same posture as every pack: experimental-dark, provisional lineage, honest
  abstention, no accuracy/completeness/runtime claim; production registration
  sits behind the documented validation and pilot-continuation gate.

### Safety boundary

- Spike artifacts live under `spike/t261/` as retained records; production
  packages must not import spike packages.
- A requested dump enters the pure-reader boundary only after it is committed
  to an indexed repository, including a dedicated evidence repository if the
  application source repository cannot own it. Ad hoc uploads and mutable
  filesystem paths are outside round one.
- Dump admission is schema-only and fails closed on data-bearing `COPY`,
  `INSERT`, `REPLACE`, or `LOAD DATA` sections. PostgreSQL dump wrappers and
  MySQL versioned comments, delimiters, engine/charset/collation clauses,
  prefix indexes, and generated-column syntax are parsed or reported by
  frozen abstention classes, never silently discarded. Repository placement
  and an optional human-asserted capture time do not establish database,
  cluster, environment, or runtime identity.
- Public corpus only; no employer names, code, schemas, or infrastructure.

**T26.1 · Pinned-corpus SQL evidence spike** — pin the public Hatchet
repository at `559b5021e418f12ded175f950b709b7fa66be5a5` (214 SQL migrations,
5 sqlc schema inputs, 36 authored query files, matching generated Go; FKs,
composite keys, CTEs, PL/pgSQL, triggers, partitions, and dynamic `EXECUTE`
for credible positives *and* honest abstentions). Pin the public
`ntppool/monitor` repository at
`e03c40a06ae8f9bd4906001c2ede0c7296ec8e96` for the MySQL lane: its committed
MySQL-compatible dump, sqlc `engine: mysql` manifest, authored query file, and
generated Go exercise FKs, composite keys, views, backticks, versioned
comments, engine/charset/collation clauses, joins, index hints, and
`ON DUPLICATE KEY UPDATE`. Add one version-pinned PostgreSQL-generated
schema-only dump of a committed public fixture, retaining the exact generator
command, version, input digest, and output digest; together the real MySQL
dump and generated PostgreSQL dump validate the requested-dump path without
executing corpus SQL in production. Hand-label both dumps and all manifest
schema inputs completely, plus a preregistered stratified random sample of
migration and query files; freeze the sampling rule and all four measurement
denominators in the spike README **before** the first parser run. Evaluate at
least one imported parser candidate and one bounded subset grammar for each
dialect against identical gates (parser bounds, panic safety on the full
corpus, binary-size and build-time impact, byte-exact source positions,
deterministic double-run output). Freeze a decision table covering:
authored-schema versus captured-dump source classification; dialect admission
from committed metadata; schema-only admission and data-section refusal;
missing artifact/request-dump state; PostgreSQL and MySQL identifier
canonicalization (never generic lowercasing or an assumed
`lower_case_table_names` value); schema-set lineage and ambiguous-root
refusal; the exact `DECLARES_*` versus `MIGRATION_*` predicate split; FK
identity including composite-column order and multiple FKs between the same
table pair; common read/write semantics for `SELECT`, `INSERT … SELECT`,
`UPDATE`, and CTEs plus PostgreSQL `DELETE … USING` and MySQL multi-table
delete / `ON DUPLICATE KEY UPDATE`; generated-source deduplication;
occurrence-distinct migration-event identity; single-pass versus bounded
two-pass reads under the SDK's one-blob citation contract; and a frozen
per-dialect unresolved vocabulary (unsupported statement, dynamic identifier,
ambiguous schema set, unknown declaration, procedural SQL, generated
duplicate, parser gap). AC: locked corpus pins, generated-dump receipt, labels,
and measurements committed under `spike/t261/`; the decision table answers
every frozen question or records an explicit deferral; the measurement report
states four separate denominators for each dialect — declaration objects
parsed vs. hand labels, migration statements recognized vs. gaps by syntax
class, query table sites recognized vs. unresolved, and uniquely bound joins
vs. name-only/missing/ambiguous — with no cross-dialect blended percentage;
double-run bytes identical in both lanes; no production code paths changed
and no pack registered.

## Epic 27 · Document schema-manifest evidence *(drafted 2026-07-27 · unscheduled — spike first)*

Make schema-on-write document-store structure useful during everyday ticket
work without teaching phebs a private vendor or employer dialect. Round one
admits one project-owned, strict JSON interchange artifact,
`phebs-document-schema-v1`, produced by a repository author or an out-of-tree
adapter and committed at the indexed revision. It supplies an Atlas-shaped
catalog — schema set → table → nested field — plus ordered key, index,
association, and materialized-view declarations with exact citations.

The first workflow is deliberately declaration-only: locate the table a ticket
mentions, inspect its nested fields and exact type spellings, verify primary
and partition-key membership and order, follow explicitly declared
associations/views, and cite the committed snapshot. If the artifact is absent,
phebs reports a request-schema-export checklist state. It does not infer a
model from client code.

### Boundary

- Pure reader over one committed manifest blob at the indexed commit. No
  service connection, administrative API, runtime introspection, schema
  registry call, generator execution, or mutable upload.
- The manifest has a closed, versioned vocabulary for tables, flattened nested
  field paths, exact source type spellings, ordered primary and partition
  keys, ordered indexes, associations, and materialized views. A field path
  is a JSON array of typed segment objects — a field segment carrying one
  exact name, or a traversal segment from the closed set {array element, map
  value} — never a delimiter-split or escaped string, so every exact field
  name, including `[]`, remains representable. Primary-key, view-key, and
  index entries may carry an explicit `asc`/`desc` direction (default
  ascending); partition keys are path-only and carry no direction. An index
  column may carry an optional positive prefix length whose unit is defined
  by the declaring store — phebs cites it and never interprets or converts
  it. An index names an opaque, citable `kind` spelling and is owned by
  either a table or a view. A view declares its own output namespace:
  ordered output fields with exact names, each carrying zero or more source
  `(table, field-path)` references; view keys and view-owned indexes
  reference output paths only, and duplicate output names fail closed. Type
  and kind spellings are citable opaque declarations in round one; phebs
  does not invent cross-store equivalence.
- Lineage is
  `document_schema_set_v1(repo, manifest_path, format_version)`. Exact table
  spelling and exact field-path segments remain part of identity. Display
  labels, capture timestamps, source revision strings, repository placement,
  and adapter names never establish service, cluster, database, environment,
  ownership, deployment, or runtime identity.
- Every table declares a non-empty ordered primary key. An optional partition
  key must be a non-empty ordered path-prefix of that primary key, with
  direction excluded from the prefix comparison; an absent
  partition key means undeclared, never an assumed default. Associations are
  optional — some stores declare none — and the association graph may
  contain cycles, including self-associations: associations are declared
  edges phebs never traverses. View source references must form an acyclic
  dependency graph; a view-dependency cycle fails closed. Every referenced
  field path must resolve exactly within the same manifest; duplicate tables,
  fields, key entries, indexes, associations, or view identities fail closed.
- Associations are explicit logical edges, not foreign keys and not proof of
  referential integrity. A materialized view is a declared derived resource,
  not proof that it is deployed, fresh, populated, or read by code.
- An `authored` manifest records repository intent. A `captured` manifest
  records only the structure present in the supplied export. Both are
  committed declarations; neither is evidence of the live store. The source
  class is mandatory and the two classes are never silently merged.
- A repository with no admitted manifest reports
  `document-schema-artifact-missing` and may produce a checklist action to
  request a schema-only export in this neutral format. The request, adapter
  instructions, and human capture metadata are workflow state rather than
  schema evidence.
- The eventual catalog gets its own table and field identity surfaces; the
  numeric protocol field reader and Contract Atlas service identities remain
  unchanged. The Workbench document-store resource plane stays `unsupported`
  until a separate public contract can prove operation → usage → table
  relationships. Declaration-only catalog rows cannot populate that
  relationship registry.
- Raw proprietary schema files, private client APIs, private adapters,
  inferred client-code usage, query semantics, live-schema comparison, and
  cross-manifest current-shape folding are outside round one. An organization
  may maintain an adapter elsewhere, but only its normalized committed output
  crosses the phebs evidence boundary.
- Same posture as every pack: experimental-dark, provisional lineage, honest
  abstention, no accuracy/completeness/runtime claim; production registration
  sits behind the documented validation and pilot-continuation gate.

### Safety boundary

- Spike artifacts live under `spike/t271/` as retained records; production
  packages must not import spike packages.
- Strict decoding rejects unknown and duplicate object keys, invalid UTF-8,
  control text, invalid or unnormalized paths, unresolved references, cycles
  where the contract forbids them, unsupported format versions, trailing
  bytes, and any record/sample/default/example/value payload. The manifest is
  structural metadata only.
- Freeze limits before implementation for manifest bytes, tables, fields per
  table, path depth and segment bytes, type bytes, key width, indexes,
  associations, views, aggregate declarations, and emitted citations.
  Limit failures are typed abstentions; no partial catalog is emitted. phebs
  validates only these frozen bounds — store-vendor and deployment limits are
  never encoded or enforced.
- A captured export enters the reader only after immutable placement in an
  indexed repository, including a dedicated evidence repository if the
  application source repository cannot own it. No employer artifacts, names,
  code, credentials, hosts, or infrastructure enter phebs fixtures or retained
  records.

**T27.1 · Neutral document-schema contract spike** — freeze the
`phebs-document-schema-v1` JSON Schema, a neutral synthetic fixture family,
and one independently authored public-reference derivation before
implementing the reader. The positive synthetic fixtures cover nested
objects/lists/maps traversed through typed array-element and map-value
segments, a literal field exactly named `[]`, opaque scalar type spellings,
composite primary keys including a descending key column, path-only
partition-key prefixes, an undeclared partition key, multiple ordered
indexes including a store-defined prefix-length column, distinct opaque
index kinds, and a view-owned index over output paths, two associations
between the same table pair, a self-association, a table with no
associations, a materialized view with multi-source output fields, a
zero-source declared output field, and its own ordered keys over output
names, authored and captured source classes, and exact non-ASCII names. The
negative synthetic fixtures cover every strict-decoder, reference-integrity,
view-dependency-cycle, key-order, malformed-segment-object, duplicate
view-output-name, direction-on-partition-key, invalid direction or
prefix-length, data-payload, and size/depth refusal.

Pin the Apache-2.0 public `jeffreyscarpenter/cassandra-guide` repository at
commit `bed58e7e51768f9f30dd435e74f31e5b3b2649a4` and hand-derive one captured
neutral manifest from `resources/reservation.cql` (Git blob
`2c1c098ff706f63f28be772514e7b7a664878774`, 2,218 bytes, SHA-256
`38992bd1f0882ebefe7e16570d05886ade150bf2036ea6119ab08fc11b3bb39e`).
The source independently exercises a nested user-defined type, set/list/map
collections, a map value containing the nested type, scalar and composite
partition/clustering keys, and a materialized view. Retain the source pin,
license and digest receipt, plus a complete declaration-by-declaration
CQL-source → neutral-manifest derivation table with exact source spans and
explicit decisions for collection traversal, path-only partition prefixes,
default key direction, and view output fields. Freeze that derivation before
the reader's first run. The CQL file is an external oracle only: phebs neither
parses nor admits CQL in Epic 27, and this fixture makes no Cassandra-pack
claim. Synthetic fixtures remain authoritative for neutral-contract features
the public source does not contain.

Hand-label every synthetic declaration and refusal and every declaration in
the derived public manifest. Freeze a decision table covering
schema-set/table/field identity; source-class semantics; exact-name handling;
typed field-path segment representation; type, index-kind, and
prefix-length-unit opacity; key, index, association, and view semantics —
including per-column direction, path-only partition prefixes, view-owned
indexes, view output namespaces and their source references,
association-cycle legality versus view-dependency acyclicity, and
undeclared-partition-key meaning; source positions for every emitted object
and edge; missing-artifact/request-export state; single-snapshot versus
explicit two-snapshot comparison; canonical ordering; typed unresolved
vocabulary; and all parser/output bounds — phebs's own, with no vendor-limit
validation. Run a bounded reader twice over every fixture and compare
byte-identical facts, citations, catalog projection, refusal census, and
snapshot comparison. AC: schema, synthetic and public-derived fixtures,
source/digest/license receipt, complete derivation table, labels, decision
table, bounds, and measurements committed under `spike/t271/`; byte-exact
UTF-8 source spans round-trip to the cited JSON; the derived manifest is fully
accounted for against the pinned CQL without a parser; malformed, cyclic,
data-bearing, and oversized inputs fail without partial facts or panics; the
comparison distinguishes added/removed/changed declarations without calling
either snapshot live or current; the retained README demonstrates the
locate-table → inspect-key/field → copy-citation ticket workflow and the
missing-artifact checklist; no private dialect, adapter, CQL parser,
production code path, pack registration, or runtime claim is added.

## Epic 28 · Redis keyspace evidence *(drafted 2026-07-27 · unscheduled — spike first)*

A general Redis keyspace model is out of scope; deterministic Redis
declaration islands and provable key usage are in scope. Redis repositories
divide by the artifacts they commit, so round one is two spike lanes —
native declaration islands and key-specification-driven usage binding — and
the neutral keyspace manifest is explicitly deferred behind the spike's gap
measurement: it is drafted only if native declarations leave the expected
gap, entering as the same request-artifact workflow as the SQL dump and the
document-schema manifest.

### Boundary

- Pure reader over committed blobs at the indexed commit. No server
  connection, no `TYPE`/`SCAN` probing, no RDB/AOF inputs, no runtime state,
  and no claim that any cluster, database, or key exists.
- Lane one, native declaration islands: committed ACL key-pattern files and
  recognizable literal command vectors that declare indexes (`FT.CREATE`),
  time series (`TS.CREATE`, `TS.CREATERULE`), and stream groups
  (`XGROUP CREATE`). Core-command semantics come from the pinned command
  metadata; the named module subset uses bounded phebs-authored recognizers
  derived from public command documentation, not copied module source or
  metadata. An index schema is only the indexed projection over declared key
  prefixes, never a complete value model; a series or group declaration names
  a resource, not its entry fields. Most such declarations execute from
  application startup code, so this lane shares the command-vector machinery
  and its recognizer limits — the spike's headline number is what fraction of
  these declarations exist as recognizable committed literals at all.
- Redis object-mapping model classes are the strongest committed declaration
  artifact but sit across a language frontier; round one measures feasibility
  only and commits to no model-class reader.
- Lane two, usage binding over the Redis 7.2.15 command key specifications
  pinned below. Only provable command vectors bind: literal raw commands or
  arrays; one `github.com/redis/go-redis/v9` major-version adapter whose
  method-to-command table is validated against each pinned corpus's exact
  dependency version; literal or same-file-constant key arguments; and
  bounded format expressions whose segment structure is statically known.
  Other client majors and unproved wrapper mappings abstain.
- Explicit `FCALL`/`EVAL` key lists can emit
  `PASSES_REDIS_KEY_TO_SCRIPT`: they prove only which keys the caller supplies
  under the command contract. They never inherit the script's read, write, or
  expected-value-kind semantics, and round one does not analyze function or
  script bodies.
- Access modes adopt the key-specification vocabulary verbatim — `RO`, `RW`,
  `OW`, `RM` with access/update/insert/delete detail — never flattened to
  read/write. `variable_flags` are resolved only when the admitted argument
  vector proves the applicable branch; otherwise access mode abstains as
  `variable-access-mode`. An incomplete specification may support a fact only
  when the exact key position and applicable flags are fixed for that admitted
  vector; every omitted or unknown position remains an
  `incomplete-key-specification` gap.
- Expected value kind is not present in the key specifications. It comes from
  a separate, frozen command-family table hand-labeled from public command
  documentation. A table entry may assert only what the code expects — for
  example, a hash command expects a hash — never runtime existence or type.
  Missing or argument-dependent entries yield
  `unknown-expected-value-kind` without discarding separately provable
  key/access evidence.
- ACL key patterns are a separate authorization-intent evidence class; they
  never declare key existence or value structure and are never merged with
  declaration or usage rows. An ACL fact may identify the exact username token
  as its authorization principal, but it proves neither authentication nor
  deployment. Only username-token and `%R~`/`%W~`/`%RW~`/`~` pattern-token
  spans may be emitted or cited. Cleartext-password tokens, password-hash
  tokens, command grants, and line-context snippets are neither retained nor
  rendered; diagnostics name only structural refusal classes.
- Frozen unresolved vocabulary: `dynamic-concatenation`, `opaque-helper`,
  `runtime-namespace`, `unknown-client-adapter`,
  `unsupported-core-command-after-pin`, `unsupported-module-command`,
  `incomplete-key-specification`, `variable-access-mode`,
  `unknown-expected-value-kind`, and `ambiguous-key-family`.
- The deferred `phebs-redis-keyspace-v1` manifest, if the gap measurement
  justifies it, declares key families as ordered structured binary-safe
  segments (literal, parameter, hash-tag grouping — typed segments, never
  format strings), with intended value kind, optional field intent, expiry
  intent, associated index/group/series resources, and authored/captured
  source class. Hash-tag validation uses the exact documented byte semantics
  without claiming a cluster exists. A concrete key or statically known
  format binds only when exactly one declared family matches; overlaps
  refuse as `ambiguous-key-family`, never precedence guessing.
- Out of scope as decisions: universal keyspace modeling; runtime
  introspection; RDB/AOF or any data-bearing input; the Workbench Redis
  resource plane (stays `unsupported`).
- Same posture as every pack: experimental-dark, provisional lineage, honest
  abstention, no accuracy/completeness/runtime claim; production
  registration sits behind the documented validation and pilot-continuation
  gate.

### Safety boundary

- Spike artifacts live under `spike/t281/` as retained records; production
  packages must not import spike packages.
- The only admitted upstream command-metadata bytes are Redis 7.2.15 commit
  `316753259b4db132cf494292a1b3a702d9e9ddb2`: BSD-3-Clause `COPYING` blob
  `a381681a1c2524ed586c6a87dfeb9ccdf1e86ded` and the 392 JSON files under
  `src/commands/` tree `59da020b9c7d8847fa0f7012b1fa2b3a09f47297`.
  T28.1 records per-file SHA-256 digests and a license receipt before use.
  Core commands added after that pin are unsupported.
- The named declaration subset (`FT.CREATE`, `TS.CREATE`,
  `TS.CREATERULE`) uses bounded phebs-authored recognizers derived from public
  command documentation. No Redis module implementation or metadata bytes
  enter the repository; every other module command is
  `unsupported-module-command`. Phebs parses committed source artifacts and
  neither links, embeds, starts, nor connects to Redis.
- ACL readers are token-streaming and output-safe by construction. Retained
  fixtures and test snapshots must prove that credentials adjacent to a
  pattern cannot enter facts, citations, diagnostics, logs, or rendered
  context.
- Public corpus only; no employer names, code, schemas, credentials, hosts, or
  infrastructure.

**T28.1 · Two-lane Redis evidence spike** — preregister these immutable public
inputs and their exact file scopes before the first recognizer run:
MIT-licensed `hibiken/asynq` commit
`d135f1439bee74e989b7f9b41ecd542cc87f024a` with
`github.com/redis/go-redis/v9` v9.14.1 for the broad usage census;
Apache-2.0 `cloudwego/eino-examples` commit
`6dc0d214c0eb392babf2d001e9be85f57ac10952` with go-redis v9.17.2 for
literal/constant raw-command and `FT.CREATE` declaration shapes; and the
Redis 7.2.15 pin above for command metadata plus the public ACL safety
fixtures. Record the hand-label sampling rule, source/license receipts, and
all denominators before execution. Measure separately, with no blended
percentage: declaration coverage (index/series/group sites with exactly
bindable identities versus dynamic construction — the manifest go/no-go
number); ACL username/pattern pairs parsed and credential-token non-retention;
recognized command sites versus unresolved by frozen shape class; resolved
key arguments; resolved access modes; expected-kind coverage; script-key
pass-through rows; and uniquely bound key spellings or families. Freeze a
decision table covering command-vector recognition and declaration
partial-fact atomicity; the go-redis method-to-command rule at both exact
versions with no cross-major claim; format-expression bounds;
incomplete-specification and `variable_flags` handling; exact access
vocabulary; the separately sourced expected-kind table;
`PASSES_REDIS_KEY_TO_SCRIPT`; ACL token-only citations; hash-tag byte
semantics; the clean-room named-module boundary; the object-mapping
feasibility verdict; and the explicit manifest go/no-go criterion. AC: pins,
receipts, labels, decision table, and measurements committed under
`spike/t281/`; double-run facts, citations, censuses, and retained outputs are
byte-identical; every refusal lands in the frozen vocabulary; an output scan
proves ACL credential tokens absent; no production code path changed and no
pack registered.

## Deliberate non-goals

SCIM provisioning, multi-org RBAC / seats, and a built-in chat app —
phebs stays **MCP-first** (agents bring their own chat) and **single-tenant**.
Kubernetes/Helm waits for the P6 fleet profile. Anonymous-access and
entitlement gating are deleted outright (config bool, no license backend).

---

## Standing rules

- Decisions land as dated ADR bullets in PLAN.md, same PR as the change.
- Every epic ends with a `make dev` demo state — no epic is "done" if it
  can't be shown end-to-end.
- Independent application code; respect dependency licenses and preserve notices.
- Personal hardware, personal time, no employer code or credentials.
