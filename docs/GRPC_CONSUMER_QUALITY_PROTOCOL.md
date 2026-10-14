# T58.1 Go gRPC syntactic consumer/registration site-coverage quality protocol

Status: **unsealed draft — nothing enumerated, scored, sealed or predicted.**
Ben chose the site-coverage claim, a fresh blind population, the frozen
filename-convention identity and observed-corpus version recording on
2026-10-13. §2's named list, §3's oracle-independence posture and §6's
thresholds are **proposed and not yet frozen**; §9 lists what blocks the seal.
No result exists and this document authorizes no measurement.

## 1. Claim card (`phebs.grpc.consumer.go`, site coverage)

**Claim.** In a Go file enumerated by the `grpc-consumer` domain at one exact
repository commit, the extractor reports every site of the frozen GD7–GD12
shapes with the right state:

- a **resolved client call** — one `CALLS_OPERATION` row at the exact call
  expression, when the selector name matches exactly one entry of the
  repository's generated-stub method index;
- an **ambiguous client call** — one `UNRESOLVED_GRPC_CALL` row, when two or
  more entries bear that name;
- a **resolved server registration** — one `REGISTERS_GRPC_SERVICE` row, when
  the called name matches exactly one service binding behind a
  `Register<Short>Server` stub declaration;
- an **ambiguous server registration** — one `UNRESOLVED_GRPC_REGISTRATION`
  row, when two or more bindings match, including one service full name reached
  through two stubs;
- a **whole-file gap** — one `GRPC_EXTRACTION_GAP` row with reason
  `source_too_large` or `parse_error`.

Each site gets exactly one row spanning the whole call expression (or the whole
file, for a gap). The product value is "where does this code syntactically call
or register a gRPC operation".

**Non-claims.** These are the card's existing non-claims, plus the two that this
round's scope makes explicit:

- **The emitted operation and service identities are not scored.** A row's
  `/package.Service/Method` object and its registration full name are recorded
  by the round but are outside the claim, because Ben selected site coverage
  alone. A pass therefore says nothing about whether an identity is correct, and
  must never be cited as operation-identity accuracy. T13.1's deferred
  blind-evaluation clause (≥98% precision, ≥90% recall, ≥90% per-fixture)
  remains the promotion bar for any accuracy-bearing claim and is **not**
  satisfied or addressed here.
- **Out-of-domain silence is not abstention.** A candidate call whose selector
  name is absent from the stub index, or which passes zero arguments, produces
  no row and enters no denominator (GD10). The claim never asserts that every
  gRPC call in a repository is found — only that every call matching the frozen
  syntactic shapes is found. Wrapper indirection, `ClientConn.Invoke` /
  `NewStream` by string, `grpc.RegisterService(desc, impl)`, reflection and
  dynamically constructed registrations are outside the shapes entirely.
- No generated-symbol provenance, runtime invocation, deployment, ownership or
  completeness.
- No exact-caller identity: `grpc-consumer` is syntactic and Epic 47's
  `grpc-caller` domain owns resolved callers. Nothing in this round promotes,
  renames or relabels a name match as an exact caller.

## 2. Population (PROPOSED — pending Ben's approval)

**Unit.** One unit is **one repository at one pinned commit**, not one file
blob. This differs from T55.1 and is forced by the extractor's shape: pass 1
walks every `*_grpc.pb.go` stub in the repository to build a cross-file service
and method index, and pass 2 scans every other `.go` file against it (GD6). A
single file in isolation has no index and would score as out of domain, so a
per-blob population would measure nothing.

**Selection rule.** Public Go repositories whose non-test code contains at least
one same-repository generated `*_grpc.pb.go` stub and at least one call or
registration site against it, chosen by name from prior knowledge before any
phebs, extractor or oracle output was seen. A named repository is dropped only
for the pre-declared reason "no same-repository `*_grpc.pb.go` stub at the
pinned HEAD", and every drop is recorded in the lock with that reason. Pinned at
the default-branch HEAD of a depth-1 clone on the selection date, recorded as an
exact commit.

**Proposed named list (18).** Nothing has been run against any of these; they
are named from prior knowledge and are subject to Ben's approval before the lock
is written.

`containerd/containerd`, `etcd-io/etcd`, `spiffe/spire`, `hyperledger/fabric`,
`vitessio/vitess`, `thanos-io/thanos`, `argoproj/argo-cd`, `moby/buildkit`,
`tikv/client-go`, `grpc-ecosystem/grpc-gateway`, `hashicorp/nomad`,
`hashicorp/go-plugin`, `grafana/loki`, `goharbor/harbor`, `projectcontour/contour`,
`kubevirt/kubevirt`, `temporalio/temporal`, `envoyproxy/go-control-plane`

**Independence disclosures on that list.**

- All 17 T55.1 Kafka repositories are deliberately excluded, so the Kafka and
  gRPC populations share no member.
- `etcd-io/etcd` appears in T47.2's `grpc-caller` universe (PLAN records its 11
  Go symlinks), so it is **not** independent of Epic 47's population. Drop it if
  strict independence from Epic 47 is required.
- `vitessio/vitess` was in T53.3 round 2's Buf WIRE population — a different
  domain and extractor. `istio/istio`, the other member of that population, is
  not listed.
- No real-repository tuning set exists for `grpcgo`: its unit suite uses a
  synthetic in-memory corpus, and `spike/t111` names no GitHub repository. So
  there is no tuning pin to exclude, unlike the four T23.1 Kafka pins.

**Sizing and stop rule.** One unit per repository at its pinned HEAD; the
population is not expanded by history. If the scored-site count falls below the
§6 floor, the round **fails its own stop rule** and a replacement round requires
a newly pre-declared population frozen before enumeration. Re-scoping the
population after seeing a denominator is forbidden.

**Versions recorded (frozen as observed, no runtime gate).** The lock records,
per repository, the `protoc-gen-go`, `protoc-gen-go-grpc` and `google.golang.org/grpc`
versions observable at the pinned commit, plus the Go toolchain used for the run.
These describe the measured population only. The extractor inspects none of them:
stub identity is the `_grpc.pb.go` filename convention alone (GD1), so no
generator version gates recognition at runtime and none is added by this ticket.

### Host and clone discipline (DECIDED 2026-10-13)

The round runs on the **macOS arm64 development host**, not the Ubuntu Linux
ceremony host. Ben delegated the choice and it was decided on measurement rather
than estimate.

- **Depth-1 clones only.** §2 pins one commit per repository and forbids history
  expansion, so no full-history clone is needed. Measured depth-1 sizes on four
  large Go repositories *outside* the proposed population, used only as
  calibration: `hashicorp/consul` 136 MiB, `moby/moby` 159 MiB, `pingcap/tidb`
  217 MiB, `kubernetes/kubernetes` 396 MiB. Eighteen list members therefore cost
  roughly 4–8 GiB, against 93 GiB free at decision time. An earlier figure of
  30–60 GiB in this document's draft is **withdrawn**: it assumed full-history
  clones, which this round does not use.
- **Not the Linux host, deliberately.** That host is running T42.2 lifecycle work
  whose tests fail at *deadline* boundaries (600.02s / 600.02s / 180.02s), so
  adding clone and whole-tree extraction I/O there could perturb measurements
  that are already deadline-sensitive. AGENTS.md also requires serializing
  expensive host execution rather than running two heavy workloads at once.
- **The Linux ceremony baseline does not govern this round.** AGENTS.md's
  Ubuntu-amd64 mandate covers rehearsals, ceremonies and reruns of historical
  workloads. T58.1 is an offline quality measurement producing no signed custody,
  ceremony receipt or scale claim. If T58.3's release later needs a real-binary
  rehearsal, that is a separate decision carrying its own host facts.
- **Host-independence of the result.** The round is pure Go over pinned Git
  objects — `git ls-tree -r`, `git cat-file --batch`, `go/parser` — with no
  platform-specific custody, process or filesystem behavior, so the measured
  outcome is reproducible from the recorded commits on any host. The execution
  host identity and Go toolchain are still recorded in the round artifacts, so
  the result stays attributable.
- **Guards enforced rather than assumed.** The corpus lives outside the repository
  at `~/phebs-t581-corpus/` and is never committed. The cloner checks free space
  before each repository and aborts cleanly, recording where it stopped, if free
  space would fall below a 40 GiB floor. Symlinks are inventoried and never
  parsed, per the T47.2 etcd-symlink lesson. No expensive Go test run is
  concurrent with cloning or exporting.

## 3. Truth (GD1–GD16)

### Independence posture (PROPOSED — pending Ben's decision)

This round **cannot** claim T55.1's independence. There, T23.1 froze KD1–KD10
before T23.2 implemented them verbatim, so the oracle was written from rules
that predated the recognizer. For gRPC the implementation shipped first and no
numbered rule set pre-existed, so GD1–GD16 below were authored *after* the fact
from the pack card and the shipped recognition rules.

What this round therefore measures is **whether `grpcgo` correctly implements a
specification frozen now** — spec conformance, not accuracy against an
independently derived ground truth. That is still a real measurement (it is how
T53.3 round 1 caught nine genuine misses), but it must be stated as such and must
not borrow the Kafka rounds' independence language.

Proposed mitigation, restoring independence between oracle and extractor even
though the spec postdates the code: `spike/t581/oracle` is authored by a separate
agent session given **only** this document's §1 and §3 and the pack card, and
explicitly forbidden from reading `internal/extract/extractors/grpcgo`. Its tests
must pin every GD shape and state without reference to the extractor's code.

### Frozen rules

**Stub identity and the index**

- **GD1** — Stub identity is the filename suffix `_grpc.pb.go` and nothing else.
  No import path, module path, generator name or generator version is inspected
  anywhere in this domain.
- **GD2** — The candidate file set is every path ending `.go`. A `_grpc.pb.go`
  file feeds the index only and is never scanned for sites.
- **GD3** — A *service binding* is a `KeyValueExpr` in a stub whose key is the
  identifier `ServiceName` and whose value is a string literal matching
  `^[A-Za-z_]\w*(\.[A-Za-z_]\w*)*$`. Its short name is the substring after the
  last `.`. Bindings are collected per stub file.
- **GD4** — A *method entry* is a string literal in a stub matching the anchored
  full-method pattern `^/([A-Za-z_]\w*(\.[A-Za-z_]\w*)*)/([A-Za-z_]\w*)$`. The
  bare method name is the second capture. Anchoring is what keeps arbitrary
  string constants out of the index.
- **GD5** — A method entry is attributed to the service bindings in the same stub
  whose full name matches the entry's first capture exactly. **If none matches**,
  the entry is still indexed, attributed to a synthetic entry carrying that full
  name and the stub path. A method literal therefore populates the index even
  without a `ServiceName` binding.
- **GD6** — The index is **repository-scoped**: it aggregates GD3–GD5 and GD12
  across every stub in the same repository at the same commit. Entries are
  deduplicated on (full name, stub path, lineage). There is no cross-repository
  and no cross-commit aggregation.

**Client call sites**

- **GD7** — A *candidate client site* is a `CallExpr` in a non-stub `.go` file
  whose `Fun` is a `SelectorExpr`. A call whose `Fun` is a plain `Ident` is never
  a client site.
- **GD8** — A candidate client site is **resolved** when exactly one method entry
  bears its selector name, and **ambiguous** when two or more do.
- **GD9** — For a `SelectorExpr` call, registration is tested **first** (GD12).
  A selector name that is both a registration name and a method name is reported
  as a registration, never as a call.
- **GD10** — A candidate whose selector name is absent from the method index, or
  which passes **zero arguments**, is *out of domain*: no row and no denominator.
  This silence is deliberate and is not an abstention.

**Server registration sites**

- **GD11** — A *registration name* is the name of a top-level `FuncDecl` with no
  receiver in a stub, matching `Register<Short>Server` with a non-empty `<Short>`,
  bound to that same file's service bindings whose short name equals `<Short>`.
- **GD12** — A *candidate registration site* is a `CallExpr` in a non-stub `.go`
  file whose `Fun` is an `Ident` **or** a `SelectorExpr` whose name appears in the
  registration index. It is **resolved** when exactly one binding matches and
  **ambiguous** when two or more do. A candidate matching no registration name
  emits nothing.

**Whole-file gaps**

- **GD13** — A candidate file whose content exceeds **4 MiB** yields exactly one
  whole-file `source_too_large` gap spanning byte 0 to the file length, and
  contributes no sites. The file is still read, because the worker requires every
  candidate read. This applies to stubs and non-stubs alike.
- **GD14** — A **stub** that fails to parse yields one whole-file `parse_error`
  gap and contributes no index entries, so its services stay unresolvable
  repository-wide. A **non-stub** file that fails to parse yields one whole-file
  `parse_error` gap and no sites.
- **GD15** — An **empty** file (zero bytes) yields no gap row and no sites: it
  contains no possible site, so reading it is complete coverage even though
  `go/parser` cannot form a file AST for it.

**Attributes, not filters**

- **GD16** — Every enumerated file is scanned regardless of role. The code role
  `production|test|mock|generated|vendor` is recorded as an attribute with
  precedence vendor > mock > generated > test > production, and filters nothing.
  Test-role sites are therefore **in scope**, which differs from the Kafka
  rounds whose claim was scoped to non-test files.

### Rules most likely to surface a genuine disagreement

Two of the frozen rules are non-obvious control-flow facts rather than shape
descriptions, and an independently written oracle is most likely to diverge from
the extractor on exactly these. They are called out so an adjudicator treats a
disagreement there as a real finding about the rule or the implementation, not as
noise:

- **GD9**, the registration-before-call precedence for selector expressions. A
  name that is both a `Register<Short>Server` entry and a stub method name is
  reported as a registration and never falls through to the call check. No
  existing extractor test pins this precedence by name, so the oracle is what
  will exercise it.
- **GD5**, the synthetic service entry. A full-method literal whose service full
  name has no `ServiceName` binding in the same stub is still indexed. An oracle
  that requires a binding before indexing a method would silently move sites from
  resolved to out of domain, which looks like a coverage miss rather than a rule
  disagreement.

One boundary is frozen here more tightly than the existing extractor suite pins
it: `TestSpansCiteExactSource` confirms a span cites a call expression, but does
not pin that the span is the **whole** `CallExpr` from the start of its function
expression to its closing parenthesis. The oracle must implement the whole-node
boundary, so a one-sided disagreement there is a real finding about which
boundary the product should cite, not a scoring artifact.

### Row identity used for scoring

| Field | Value |
|---|---|
| Span | the whole `CallExpr`, byte offsets from the parser's token file; a gap spans the whole file |
| Subject | the repository-relative path |
| Resolved call | `CALLS_OPERATION`, object `/<service FQN>/<method>`, tier `heuristic`, detail `grpcgo-call-detail-v1` |
| Resolved registration | `REGISTERS_GRPC_SERVICE`, object `<service FQN>`, tier `derived`, detail `grpcgo-register-detail-v1` |
| Ambiguous call | `UNRESOLVED_GRPC_CALL`, object the **bare** method name, tier `unresolved`, no lineage, detail `grpcgo-call-ambiguity-v1` with `candidate_count` |
| Ambiguous registration | `UNRESOLVED_GRPC_REGISTRATION`, object the bare `Register…Server` name, tier `unresolved`, no lineage, detail `grpcgo-register-ambiguity-v1` with `candidate_count` |
| Gap | `GRPC_EXTRACTION_GAP`, object `source_too_large` or `parse_error`, tier `unresolved`, detail `grpcgo-gap-detail-v1` |

**Scored state vocabulary.** `resolved_call`, `ambiguous_call`,
`resolved_registration`, `ambiguous_registration`, `gap_source_too_large`,
`gap_parse_error`. Only the state is scored; object strings are recorded but, per
§1, are outside the claim.

## 4. Adjudication

The lead adjudicates every disagreement by reading the source at the exact span.
That covers each missed, duplicated or differently-stated site, and each
extractor row at no oracle span. An adjudicated truth replaces the oracle's for
that key, and `["not_a_site", null]` confirms a spurious extractor row. Because
the oracle and the extractor both implement GD1–GD16, a disagreement is either an
implementation defect in one of them or a genuine ambiguity in the frozen rules —
and a rule ambiguity is resolved by amending §3 **before** scoring, never by
adjudicating around it after the fact.

## 5. Measurement

`spike/t581/export` runs the in-tree `grpc-consumer` extractor once per unit
through the same `sdk.Corpus` interface the worker uses, walking that commit's
complete `.go` file set from a bare clone (`git ls-tree -r` plus
`git cat-file --batch`; no worktree is materialized). `spike/t581/oracle` is a
stdlib-only Go program implementing GD1–GD16 over the identical unit.
`spike/t581/score.py` joins rows on `(unit, path, start_byte, end_byte)` and
compares scored state.

Because a unit is a whole repository tree rather than one blob, a unit costs one
two-pass extractor run over every `.go` file in it. That is the structural cost
difference from T55.1 and is why the population is one commit per repository
rather than a history enumeration.

**Per-site, not per-assertion.** `ComputeAssertionID` hashes
(predicate, subject, object, lineage, repository) and excludes evidence
references, so two sites in one file sharing a claim identity — the ordinary case
for two ambiguous calls to the same bare method name — collapse into **one**
stored assertion carrying two supporting atoms, and `Coverage.UnresolvedCount`
counts distinct assertions rather than occurrences. The exporter must therefore
emit one line per emitted fact (per site span), as T55.1's did, and the scorer
must join on spans. Scoring against assertion counts would understate the
denominator and silently pass missed sites.

The exporter must not run on the round population before the round is sealed.

## 6. Thresholds (PROPOSED — pending Ben's freeze)

| Check | Proposed bar |
|---|---|
| Coverage: scored sites with exactly one row | ≥ 99% |
| State agreement: covered sites with the right scored state | ≥ 99% |
| Sites whose resolved/ambiguous classification is inverted | 0 |
| Call sites emitted as registrations, or the reverse (GD9) | 0 |
| Confirmed spurious rows per scored site | ≤ 1% |
| Pending after adjudication | 0 |
| Scored sites | ≥ 300 |
| Per-role state agreement, each of `production`, `test`, `generated` | ≥ 95% (reported; a role below this is a disclosed finding requiring an adjudication note, not an automatic failure) |

The bars mirror T55.1's §6, which Ben froze as proposed. They are **not** frozen
here and no measurement may run against them until Ben freezes them.

## 7. Seal

The commit holding this protocol, the population lock and its digests, the
oracle, exporter, enumerator and scorer digests, and the frozen §6 thresholds are
published in a public gist before `export` runs on the population. Sealing
records the sealer and the sole reviewer, and the draw or enumeration that
follows must reproduce the sealed counts exactly.

## 8. Result

**Not run.** No population has been enumerated, no oracle pass has been
recorded, no score exists and nothing is sealed.

## 9. Open decisions blocking the seal

1. **Ben:** approve, amend or replace the §2 named list, and decide whether
   `etcd-io/etcd` stays given its presence in T47.2's `grpc-caller` universe.
2. **Ben:** choose the §3 oracle-independence posture — a separate agent authoring
   the oracle from this document alone, or spec-conformance accepted without that
   separation.
3. **Ben:** freeze the §6 thresholds, including whether the per-role floors are
   scored or only disclosed.
4. **Agent, after 1–3:** write `spike/t581/{corpus.lock.json,
   enumerate_population.py, oracle, export, score.py, test_score.py, README.md}`
   and run the oracle's pre-seal pass to publish the state table and scored-site
   count in §3 before any extractor run. This document is already linked from
   `docs/README.md`.

**Decided, no longer open — the execution host.** Ben delegated it and §2's host
and clone discipline records the decision: the round runs on the macOS
development host over depth-1 clones, because the measured cost is roughly 4–8
GiB against 93 GiB free, while the Linux ceremony host is occupied by
deadline-sensitive T42.2 work and this round is not a rehearsal, ceremony or
historical rerun that the Linux baseline governs. The draft's earlier 30–60 GiB
estimate is withdrawn there with the measurement that replaced it.

## 10. Relationship to prior gRPC measurement

This is a **fresh round, not a T11.1 retry**, and it must not be recorded as one.

- `spike/t111/REPORT.md` is terminally **CLOSED** on a valid capacity stop with
  `GATE2-V2 NOT_ESTABLISHED` and explicitly no authority to retry. Its numbers
  measured the *typed* spike, which used `go/types` over hydrated module caches;
  `grpcgo.go`'s own header states the productized port is syntactic and lost that
  fidelity, so those numbers do not transfer and its superseded "PASS" figures
  must not be cited.
- T13.1's blind-evaluation clause was deferred and remains the promotion bar for
  any accuracy-bearing claim. This round makes no accuracy-bearing claim (§1).
- `spike/t394/results.json` records `go_grpc_call_site_quality` as
  `outcome not_run`, `value_state unmeasured`, `reason unsealed_preregistration`,
  `measurement_authorized false`. A sealed and scored T58.1 round is what would
  change that record, and only for the site-coverage claim.
- The pack card's `Independent validation owner: none assigned; release-blocking`
  and `Validation: not measured under a sealed protocol` stay exactly as they are
  until a round is sealed and scored. T58.3 owns registration and promotion.
