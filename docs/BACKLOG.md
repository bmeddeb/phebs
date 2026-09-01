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
2026-08-30 after neutral-47 passed the frozen mechanics gate. Epic 41 is now
the active scale track and separately targets
at least 8,000 accepted services and measures 10,000 accepted logical services,
and Epic 42 composes the physical-repository and service-cardinality envelopes.
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

## Epic 41 · Ten-thousand-service authority and sparse consumers *(active)*

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
T41.8 is complete on its ticket branch, and T41.9 is next after integration.

**T41.9 · Atomic v3 runtime registration and reverse transition** — add an
explicit operator/config opt-in only after dark catalog, state, search, and
relationship authority is fully reconciled and strict-current. A valid dark
candidate never auto-promotes. AC: one CAS-controlled runtime selector is the
linearization point across all v3 consumers; the prior v1/v2 authority remains
readable until that selector changes; crash immediately before/after activation
recovers to exactly old or new authority; startup verifies every selected root
before serving; older binaries refuse the selector safely; disablement builds
and reconciles the complete reverse target before a selector CAS—no pointer-only
downgrade or mixed summary; authorization, no-fallback, concurrent catalog/
state/relationship transition, rollback-floor, and backup/restore tests; the
registration changes no evidence release posture; full merge bar.

**T41.10 · Neutral 10,000-service closure gate** — exercise the T41.1 target on
the deliberately small physical corpus through ingestion, state reconcile/
activation, shared search, relationships, authorized HTTP/MCP/UI reads,
archive/restore, and lifecycle. AC: 10,000 accepted services publish and become
independently queryable; the floor of 8,000 accepted services is explicit; the
separate transition profile passes; exact-bound and one-over behavior match
T41.1; cold, no-op, point/page, one-service and 1% deltas, removal/re-add,
A→B→A, partial activation,
crash, stale worker, authorization, pressure, backup/restore, and collection
pass; receipt records root/member/cache/transaction/query/read/write/RSS/disk
cost without source identity; no operation performs service-count × repository
bytes work. The result establishes no large-repository envelope, target SLO,
supported customer limit, accuracy/completeness, release, migration, or
decommission claim.

## Epic 42 · Combined scale gate and topology decision *(scheduled after Epics 40–41)*

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

**T42.1 · Combined gate freeze and deterministic corpus** — construct the
minimal deterministic corpus/profile adapter that composes the exact T40 and
T41 authorities without materializing service-count × file-count bytes. Freeze
revision history, ownership/membership oracle, query and relationship oracle,
small-delta/A→B→A transitions, failure injection points, host/tool identities,
resource ceilings, stop/teardown rules, and receipt schema before execution.
Neutral chain, layered-DAG, bounded-fan-out, and hotspot families use fixed
seeds and closed expected edges. External runtime traces may inform aggregate
shape only after license review; their rows and opaque identities are not
vendored and cannot become source, service, placement, relationship, or result
authority.
AC: independent oracle generation does not consume phebs results; two builds
are byte-identical; physical owners, unique content, catalog membership and
unowned prefixes, inherited placement-claim duplications, logical memberships,
accepted fan-out, and every byte kind are separately reported; the exact main
commit and upstream receipts are digest-bound; no production behavior, cap, or
release state changes; independent plan review and full merge bar.

**T42.2 · Combined convergence, recovery, and pressure execution** — run the
frozen corpus through ordinary production workers and retain a closed receipt.
AC: one physical source/search/observation generation with at least 2,000,000
regular-file owners serves all 10,000 services; applicable extraction and
relationship roots become current; exact All code/service/relationship queries
match the oracle; cold, warm, small
physical delta, small logical delta, A→B→A, partial service failure,
interrupted publication, stale lease, process restart, backup/restore, reader
lease, lifecycle collection, and 80/90/75 pressure cases settle under bounded
work; measurements enumerate per-phase wall/RSS, children, physical/logical/
allocated bytes, store rows/transactions, member reads, cache behavior, and
reuse; any stop remains a stopped receipt with later phases `not_run`; no SLO
or release inference; full recovery and merge bars.

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

## Deliberate non-goals *(per historical PORT_MAP §7/§12)*

SCIM provisioning, multi-org RBAC / seats, and a cloned "Ask" chat app —
phebs stays **MCP-first** (agents bring their own chat) and **single-tenant**.
Kubernetes/Helm waits for the P6 fleet profile. Anonymous-access and
entitlement gating are deleted outright (config bool, no license backend).

---

## Standing rules

- Decisions land as dated ADR bullets in PLAN.md, same PR as the change.
- Every epic ends with a `make dev` demo state — no epic is "done" if it
  can't be shown end-to-end.
- Upstream repo is behavior reference only; `ee/` paths never opened.
- Personal hardware, personal time, no employer code or credentials.
