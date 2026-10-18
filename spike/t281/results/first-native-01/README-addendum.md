# T28.1 first measured outcome — 2026-10-18

This addendum supplements the immutable source-preparation README without
changing its frozen bytes. The first candidate outcome is
**quality-gaps-no-production-adoption**. The candidate emitted three extra
unresolved sites in the seven public sample files. They remain in both original
outputs and the original score; there was no retry, source correction, label
change or threshold adjustment after execution.

Source contract checkpoint `c525a50f` precedes implementation checkpoint
`3c1305adfac166a6c5a1d5fbc4725ec6e064d05a`. The exact reviewed candidate contract
is `d9161c18a3c819bbb96c3109a28a716f4a0b39f06b67e1a9d9a04bd10243b275`.
All 96 input artifacts / 1,907,003 stored bytes and the 13 implementation
artifacts / 102,314 bytes stayed exact. The public archives retain all 793
files / 3,600,148 source bytes at their independently licensed Git/SHA pins.
Evaluator HEAD, tool/compiler identity and binary SHA are distinct from these
upstream identities; the nested module's actual Go build metadata is retained
without inventing a missing binary VCS field.

## First execution and operating cost

The single supplemental boundary selector passed in 0.322s (0.933s outer wall).
The inherited-cache offline Go build took 0.501s; this is not a cold-build
measurement. Two fresh child-free parser processes took 0.432s and 0.180s under
their unchanged 30s limits. Both exited zero with empty stderr and no stop.
Each emitted 484,258 identical bytes, SHA-256
`4f402423b30529252d4e776bf46c19237b1510b0c00ecfdeb7bbe37fa94942c5`.
There are 911 complete blob records, 16 binding records and one terminal record
per pass. This input includes evaluator-only control modules, separately from
the public 793-file population. The scorer verified 558 source-span round trips.

Observed direct-PID RSS peaks were 13,369,344 and 12,926,976 bytes; exact wait4
child peaks agreed. The unchanged observed stop was 1 GiB at 20ms. Sampling can
overshoot and is not a hard heap bound. Build direct-PID sampled RSS was
26,935,296 bytes and wait4 maximum 86,753,280 bytes; neither is aggregate compiler
family or parent-evaluator memory. Parent Python holds bounded source/output
bytes plus object overhead, which this receipt does not measure.

The reader makes one bounded module/core metadata admission, one Go metadata
pass, then one source/command/ACL pass; Go files are parsed twice. It hashes each
bounded source read and selected citations. Source/AST bytes are not cached
across files. A shared 16,384-entry / 8-MiB metadata budget excludes Go map,
slice, AST and encoding overhead. A compressed prefix index avoids per-key
corpus reads or scans. Per-blob 4-MiB encoding admission occurs after allocation;
batch output remains capped at 32 MiB. Facts stream rather than accumulating a
complete result slice. There is no production query, sync, retry/no-op,
startup, publication, worker, cache, lock, schema or database change.

All build/parser children were joined, their owned process groups were absent,
and private source/binary scratch was removed only afterward. No Redis,
miniredis, application, client or script was executed. There was no download,
runtime probe or production registration. The expensive execution slot was
released after this first terminal result. Exact costs, artifacts and
qualifications are in `outcome-cost.json` and `receipt.json`.

## Separate public measurements

The sample is seven purposively selected complete files, not a probability
sample. All 99 labelled sites occur at their exact frozen spans. The candidate
emitted 102 sites; the three extras prevent a complete-site census PASS.

| Population and frozen shape | Labelled / matched sites | Recognized commands | Key expressions / resolved spellings | Access rows | Known kind rows |
|---|---:|---:|---:|---:|---:|
| Asynq core API |35 /35|35|28 /0|28|25|
| Asynq opaque script wrappers |52 /52|0|Unmeasured behind wrappers|0|0|
| Eino core API |2 /2|2|2 /0|2|2|
| Eino unsupported exact version |8 /8|0|No admitted key facts|0|0|
| Eino unproved extension wrappers |2 /2|0|Unmeasured behind wrappers|0|0|

All 37 labelled core commands and all 30 exact key-expression positions/access
flag vectors match. All 27 known expected-kind observations and three generic
unknowns match the separate kind table. All 30 public spellings remain
unresolved as expected; zero does not establish a spelling/family success rate.
Explicit script pass-through and uniquely bindable public families have zero
labelled opportunities and remain unmeasured. The 52 opaque script-wrapper key
cardinalities remain unmeasured; no script-body semantics are inferred.

The complete known native census remains two FT.CREATE sites, one statically
named resource and one runtime name/prefix, zero atomically complete vectors,
zero admitted exact-version sites and zero emitted declarations. Both belong
to nearest-module v9.7.0; no parent v9.17.2 admission is inherited. There are zero
known TS.CREATE/TS.CREATERULE/XGROUP CREATE sites in the frozen literal census.
Hidden helpers/scripts remain outside that native discovery claim.

All seven public ACL principal/pattern pairs match their exact safe token
spans. The closed output schema, independent source joins and credential scan
passed; seven credential tokens were checked without retaining credential
tokens, spans or context. Two public password payloads coincide with permitted
usernames, so only exact safe-token provenance is admitted for that collision;
distinct synthetic payloads and hash payloads remain absent. No effective grant,
authentication, deployment or key-existence claim follows.

All 60 authored source controls plus two admission controls have zero score
mismatches. Six complete positive native projections match their prospectively
reviewed witnesses. The separate supplemental implementation test covers the
64-key boundary, canonical counts above it, negative semantic counts and
script predicate/access/kind isolation. Authored controls never enlarge the
public quality denominators. All emitted semantic/boundary refusals are in the
frozen vocabulary; atomic boundary controls publish zero facts.

## Exact three limitations

| Source | Extra emitted token | Reason and source diagnosis |
|---|---|---|
| Asynq `internal/rdb/inspect.go:2108` | `ClusterKeySlot`, bytes65713..65727 | `r.ClusterKeySlot(qname)` is an application wrapper on RDB. The candidate emits `unknown-client-adapter` because the method spelling is in its fixed API map even though its receiver does not prove that client API. |
| Eino `flow/agent/react/memory_example/memory/redis.go:84` | `Run`, bytes2268..2271 | `miniredis.Run()` is a constructor call. The unconditional wrapper-name branch emits `opaque-helper` without the required receiver identity. The source was parsed only. |
| Eino `quickstart/eino_assistant/pkg/redis/redis.go:42` | `Do`, bytes954..956 | `initOnce.Do(...)` is a sync.Once call, already a non-Redis negative in the frozen frontier census. The arbitrary Do frontier emits `unknown-client-adapter`. |

These extras carry zero key, native, access or kind facts. They do not invalidate
the exact 37/30/27 positive joins, but they fail the frozen complete-emission
contract. A future correction must qualify the candidate frontier by source
identity and preserve this first result. This spike does not implement such a
post-result correction or relabel the three extras as legitimate Redis sites.

## Manifest and object-mapping verdict

Production adoption is **NO-GO**. No manifest format, loader, pack or production
dependency is designed or registered here. Object-mapping reader feasibility is
**defer/no reader**: the two Eino constructors remain opaque wrappers, and this
Go-only scope cannot measure other-language model classes.

The manifest design decision is **inconclusive/defer; no authorization**.
The frozen criterion requires a persistent public structural gap after correct
admitted adapters. Both known native sites use unadmitted v9.7.0, so this run
does not establish that measured condition. Both source vectors contain
`config.Dimension` (lines176 and100 respectively), and the first also has runtime
name/prefix. These source observations motivate a possible later investigation;
they cannot substitute for correctly admitted native-site measurement or
authorize a manifest design. No version or source was patched to manufacture
admission, and no schema, loader or registration was authored.

## Acceptance disposition and remaining work

Pins/licenses, independent labels and decisions preceded candidate execution;
two fresh outputs/citations/censuses are byte-identical; the closed refusal and
credential-safety contracts hold; all first results remain retained. The spike
records an honest finite feasibility outcome, not a complete-sample quality
PASS, runtime claim, generalized accuracy or scale result.

Independent lead outcome/adoption review, owning spine/guide integration and
repository-required focused vet/lint are pending. No additional parser corpus
run is needed to document this result. Remediating the three frontier errors
would require a separately reviewed supplemental correction with the original
output/score retained; it is not silently part of this first measurement.

CPU is unmeasured. The retained first receipts contain wall time and qualified direct-child RSS only; the prospective contract’s CPU wording does not establish a CPU measurement.
