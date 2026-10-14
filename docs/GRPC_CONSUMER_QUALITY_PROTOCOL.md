# T58.1 Go gRPC syntactic consumer/registration site-coverage quality protocol

Status: **preregistration complete and unsealed — the corpus is locked and every
decision is frozen; no site has been enumerated, scored, sealed or predicted.**
Ben chose the site-coverage claim, a fresh blind population, the frozen
filename-convention identity and observed-corpus version recording on 2026-10-13;
on 2026-10-14 he approved and closed the 18-name list — which
`spike/t581/corpus.lock.json` then pinned at 13 members and 5 drops — chose the
separate-author oracle posture in §3, and froze the §6 thresholds. **No §9
decision remains open.** Nothing has been measured: no oracle has run, no
extractor has been run over the population, no result exists, and this document
authorizes no measurement until §7's seal is published.

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
- **A pass does not generalize to Go gRPC code at large.** The locked population
  is necessarily biased toward repositories that vendor modern split
  `*_grpc.pb.go` stubs in-tree, because that suffix is GD1's entire notion of stub
  identity. §2's lock result records two structural blind spots — combined-style
  `*.pb.go` stubs, and stubs living in external modules — and in both cases the
  affected call sites are *silently* out of domain rather than abstentions, so
  nothing in the evidence reports them as missing. The claim is therefore scoped to
  repositories whose gRPC stubs are same-repository split-generated files, and a
  passing score says nothing about code outside that class.
- No exact-caller identity: `grpc-consumer` is syntactic and Epic 47's
  `grpc-caller` domain owns resolved callers. Nothing in this round promotes,
  renames or relabels a name match as an exact caller.

## 2. Population (list FROZEN by Ben, 2026-10-14)

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

**Frozen named list (18) — approved by Ben, 2026-10-14.** These names were
chosen from prior knowledge before any phebs, extractor or oracle output was seen,
and the list is now **closed**: it may not be amended, extended or re-scoped for
this round. A repository leaves it only through the pre-declared drop rule below,
recorded in the lock with its reason. If the round fails its §2 stop rule, the
replacement round requires a newly pre-declared list frozen before enumeration.

`containerd/containerd`, `etcd-io/etcd`, `spiffe/spire`, `hyperledger/fabric`,
`vitessio/vitess`, `thanos-io/thanos`, `argoproj/argo-cd`, `moby/buildkit`,
`tikv/client-go`, `grpc-ecosystem/grpc-gateway`, `hashicorp/nomad`,
`hashicorp/go-plugin`, `grafana/loki`, `goharbor/harbor`, `projectcontour/contour`,
`kubevirt/kubevirt`, `temporalio/temporal`, `envoyproxy/go-control-plane`

**Pre-declared drop rule.** A named repository is dropped only if, at its pinned
HEAD, it contains no same-repository `*_grpc.pb.go` stub. The reason is recorded
verbatim in the lock's `dropped` array. No other reason may retire a member, and
in particular no result, score, site count or oracle disagreement may.

**Independence disclosures on that list.**

- All 17 T55.1 Kafka repositories are deliberately excluded, so the Kafka and
  gRPC populations share no member.
- `etcd-io/etcd` appears in T47.2's `grpc-caller` universe (PLAN records its 11
  Go symlinks), so it is **not** independent of Epic 47's population. This was
  disclosed before approval and **Ben chose to include it anyway** on 2026-10-14.
  The overlap is therefore a known, accepted property of this population and must
  be restated in any result: an etcd-site number is not independent of Epic 47's
  caller corpus. Its 11 Go symlinks are inventoried and never parsed, per §2's
  host and clone discipline.
- `vitessio/vitess` was in T53.3 round 2's Buf WIRE population — a different
  domain and extractor. `istio/istio`, the other member of that population, is
  not listed.
- No real-repository tuning set exists for `grpcgo`: its unit suite uses a
  synthetic in-memory corpus, and `spike/t111` names no GitHub repository. So
  there is no tuning pin to exclude, unlike the four T23.1 Kafka pins.

**Lock result (2026-10-14).** `spike/t581/corpus.lock.json`
(`t581-grpc-consumer-corpus-lock-v1`) records **13 members and 5 drops**, pinned
at exact commits, with 225 `*_grpc.pb.go` stub files and 43 symlinks inventoried
and never parsed (no stub-named symlink exists, so no member's stub count depends
on a link). The whole corpus is 1.7 GiB at depth 1 — well inside both the 4–8 GiB
estimate and the 40 GiB floor, which was never approached. Host facts are recorded
in the lock: Darwin arm64, go1.26.5, 92.9 GiB free at start, `stopped_before:
null`. `etcd-io/etcd` shows 3 stubs and 11 symlinks, matching the 11 etcd Go
symlinks T47.2 inventoried.

The five drops split into two causes, and both are **structural limits of this
domain that the round must disclose rather than let a passing score conceal**:

- **Combined-style stubs are invisible (2 drops).** `thanos-io/thanos` (9 files)
  and `argoproj/argo-cd` (17 files) *do* carry real gRPC service stubs with
  `RegisterXServer` functions and `grpc.ServiceDesc` literals — but in the older
  combined `*.pb.go` form that `protoc-gen-go` emitted before the gRPC plugin was
  split into `protoc-gen-go-grpc`. GD1 recognizes only the `_grpc.pb.go` suffix, so
  such a repository contributes **zero index entries**, and every call site in it
  falls through GD10 as silently out of domain: no row, no gap, no abstention.
- **Cross-module stubs are invisible (3 drops).** `tikv/client-go`,
  `goharbor/harbor` and `projectcontour/contour` have no same-repository `.pb.go`
  at all; their contracts live in external modules, which GD6's
  repository-scoped index cannot see by construction.

**This characterization was determined after the mechanical drop and did not
influence selection.** The list is closed and no dropped repository is re-added:
the drop rule was pre-declared, structural and applied blind, and re-scoping the
population after learning anything about it is exactly what §2 forbids. The
consequence is recorded instead as a limit on what a pass means — see §1.

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
ceremony host. This is now governed by policy as well as measurement: the
2026-10-13 machine tracks in AGENTS.md assign **T58.1 to the Mac track**, and
assign the Linux track the rehearsal corpora, release signing and Epic 58's
T58.3 registration. Ben delegated the choice before that policy landed and the
measurement below independently reached the same place.

**Track crossing, recorded as the policy requires.** T58.1 is Mac-track work that
writes into `spike/`, a Linux-track-owned directory. The crossing is authorized by
the policy naming T58.1 as Mac-track and by Ben flagging the ticket in chat, and
is recorded here and in the owning BACKLOG ticket. `spike/t581/` is offline
one-shot measurement tooling: it touches no `internal/`, `cmd/`, store schema,
`Makefile` or `go.mod` path the Linux track owns, and T58.3's registration and
release work stays with the Linux track.

- **Depth-1 clones only.** §2 pins one commit per repository and forbids history
  expansion, so no full-history clone is needed. Measured depth-1 sizes on four
  large Go repositories *outside* the proposed population, used only as
  calibration: `hashicorp/consul` 136 MiB, `moby/moby` 159 MiB, `pingcap/tidb`
  217 MiB, `kubernetes/kubernetes` 396 MiB. Eighteen list members therefore cost
  roughly 4–8 GiB, against 93 GiB free at decision time. An earlier figure of
  30–60 GiB in this document's draft is **withdrawn**: it assumed full-history
  clones, which this round does not use.
- **Not the Linux host.** The 2026-10-13 machine tracks give the Linux track the
  rehearsal corpora, release signing and Epic 58's T58.3 registration, and give
  T58.1 to the Mac track, so this corpus belongs here. AGENTS.md also requires
  serializing expensive host execution rather than running two heavy workloads at
  once; when the host choice was made Ben had T42.2 lifecycle work in progress on
  another machine, and no expensive Go run is concurrent with cloning or exporting
  here. An earlier draft of this section justified the choice by claiming clone I/O
  would perturb T42.2 deadline measurements *on the Linux host*; that premise is
  **withdrawn** as an unverified inference, because the same policy assigns the
  `TestT422Workspace*NativeComposition` tests to darwin and they were observed
  failing on this Mac, not there. The conclusion is unchanged and now rests on the
  track assignment and the measurement below.
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

### Independence posture (FROZEN by Ben, 2026-10-14)

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

**Frozen mitigation (Ben, 2026-10-14).** `spike/t581/oracle` is authored by a
separate agent session given **only** this document's §1 and §3 and the
`phebs.grpc.consumer.go` pack card, and explicitly forbidden from reading
`internal/extract/extractors/grpcgo`, its tests, or any phebs output. It must be
stdlib-only Go behind a `t581oracle` build tag, mirroring `t551oracle`, and its
tests must pin every GD shape and state without reference to the extractor's code.
The lead who froze GD1–GD16 does not author the oracle and does not answer its
clarifying questions, because answering them from implementation knowledge would
rebuild exactly the coupling this separation exists to prevent; an ambiguity is
left to surface as a disagreement at adjudication, where §4 requires it to be
resolved by amending §3 **before** scoring rather than adjudicated around.

**The limit of that guarantee, stated plainly.** The separation is procedural, not
technical: the oracle's author has filesystem access to this repository and is
instructed not to read the extractor, which cannot be enforced from here. So this
round's independence is weaker than T55.1's in kind and not merely in degree, and
any result must say so. What the separation does buy is real — an independently
written implementation of the frozen rules diverges from `grpcgo` wherever `grpcgo`
departs from the spec, which is the defect class this round exists to find.

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

## 6. Thresholds (frozen by Ben, 2026-10-14)

**Scored — the round fails if any of these misses.**

| Check | Bar |
|---|---|
| Coverage: scored sites with exactly one row | ≥ 99% |
| State agreement: covered sites with the right scored state | ≥ 99% |
| Sites whose resolved/ambiguous classification is inverted | 0 |
| Call sites emitted as registrations, or the reverse (GD9) | 0 |
| Confirmed spurious rows per scored site | ≤ 1% |
| Pending after adjudication | 0 |
| Scored sites | ≥ 300 |
| **Production-role state agreement** | **≥ 99%** |

**Reported and disclosed, not scored.**

| Report | Requirement |
|---|---|
| Per-repository scored-site counts | published in full, with an explicit concentration statement naming the largest member's share of the total |
| State agreement for `test`, `generated`, `mock` and `vendor` roles | published per role; any role below 95% is a disclosed finding requiring an adjudication note |
| Out-of-domain candidate counts | published, because GD10's silence is this claim's largest blind spot |

**Roles are the extractor's own, and this round does not verify them.** GD16 fixes
the five role names and their precedence but deliberately does not freeze how each
role is *detected*, so the oracle does not classify roles at all. The scorer groups
rows by the role the exporter reports and computes per-role agreement within those
groups. Every per-role number therefore inherits the extractor's role
classification: if a site's role is misclassified it lands in the wrong bucket and
can move a per-role figure without any site being missed or misstated. That is a
disclosed limit on the per-role disclosures, not on the aggregate coverage and
state-agreement bars, which are role-independent. The scored production-role floor
inherits the same caveat, so a production-role failure must be read as "sites the
extractor labelled production disagreed", and adjudication must say whether the
disagreement was coverage, state, or role.

The production-role floor is **scored** rather than disclosed because production
code is what the product serves, so a defect concentrated there must fail the round
instead of hiding inside an aggregate that test and generated files can dominate.
The per-repository counts are **disclosed** rather than floored because stub counts
in the locked population range from 1 (`spiffe/spire`) to 54
(`envoyproxy/go-control-plane`), so a distribution floor could fail the round for a
population-shape reason that says nothing about extractor quality; concentration is
named explicitly instead of barred.

These bars mirror T55.1's §6, which Ben froze as proposed and which passed at
762/762. They are frozen here **before any oracle pass**, which is stricter than
T55.1's sequence, where the oracle's pre-seal counts were already known when the
thresholds were frozen. No threshold may be revised after a count is seen: a round
that misses is a failed round, and a replacement requires a newly pre-declared
population under §2.

## 7. Seal

The commit holding this protocol, the population lock and its digests, the
oracle, exporter, enumerator and scorer digests, and the frozen §6 thresholds are
published in a public gist before `export` runs on the population. Sealing
records the sealer and the sole reviewer, and the draw or enumeration that
follows must reproduce the sealed counts exactly.

## 8. Result

**Not run.** No population has been enumerated, no oracle pass has been
recorded, no score exists and nothing is sealed.

## 9. Preregistration status and remaining sequence

**No decision remains open.** Every preregistration input is frozen:

- the **claim** (§1): site coverage only, with emitted identities recorded but not
  scored;
- the **population** (§2): 18 names approved and closed 2026-10-14, locked at 13
  members and 5 drops by `spike/t581/corpus.lock.json`, with `etcd-io/etcd`
  included and its T47.2 `grpc-caller` overlap disclosed and accepted;
- the **rules** (§3): GD1–GD16;
- the **oracle posture** (§3): a separate author working from §1 and §3 alone,
  forbidden from reading the extractor, with the procedural limit of that
  guarantee stated rather than glossed;
- the **thresholds** (§6): frozen 2026-10-14 before any oracle pass, with the
  production-role floor scored and per-repository concentration disclosed;
- the **host** (§2): this macOS development host over depth-1 clones.

**Remaining sequence, in this order.**

1. *Agent:* write `spike/t581/{enumerate_population.py, export/main.go, score.py,
   test_score.py, README.md}`. None of these may run the extractor over the
   population before the seal.
2. *Separate author:* write `spike/t581/oracle` from §1 and §3 alone.
3. *Agent:* run the oracle's **pre-seal pass** and publish its state table and
   scored-site count in §3, before any extractor run. If the scored-site count is
   below the §6 floor, the round stops there under §2's stop rule and a
   replacement round needs a newly pre-declared population.
4. *Ben:* seal per §7 — publish the commit, the population digest and the tool
   digests in a public gist **before** `export` touches the population.
5. *Agent:* run `export`, then `score`, then adjudicate every disagreement per §4,
   then record the result in §8 with every §6 disclosure.

**Not authorized by this document:** any extractor run over the population before
step 4; any revision to a threshold, rule or population member after a count is
seen; and any quality, coverage, release, scale or SLO claim.

**Previously open, now decided:**

- **The population list.** Ben approved the 18 frozen names on 2026-10-14 and
  chose to include `etcd-io/etcd` with its T47.2 `grpc-caller` overlap disclosed
  and accepted. §2 records the closure, the single pre-declared drop rule, and the
  requirement that any result restate the etcd overlap. `spike/t581/lock_corpus.py`
  clones the list at depth 1, applies only that drop rule, inventories symlinks
  without parsing them, enforces the free-space floor, and writes
  `spike/t581/corpus.lock.json`. It runs no phebs code, no extractor and no
  oracle, and reads no Go file's contents, so the lock precedes measurement as §7
  requires.
- **The execution host.** Ben delegated it and §2's host
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
