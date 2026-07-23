# Gate 0 extractor artifact bridge — draft worksheet

*Owner worksheet for [PILOT_CHARTER.md](./PILOT_CHARTER.md) Gate 0. This is
not an artifact-equivalence claim, accuracy claim, approval, executable build
instruction, or authority to run the pilot. It records a versioned comparison
between the closed external benchmark producer and the eventual pilot
artifact. Every pilot field and signature remains open until Gate 0.*

## 1. Required disposition

The charter permits either exact artifact equality or a separately approved
bridge. Exact equality is structurally unavailable here: the benchmark used
the typed `spike/t111` producer over hydrated Go module closures, while the
productized pilot extractor runs inside phebs' pure-reader SDK and deliberately
uses bounded syntactic resolution with abstention.

Therefore the only honest candidate disposition is:

> `BRIDGE_REQUIRED — IDENTITY / REPRODUCIBILITY / MECHANICS ONLY`

Approval of this bridge can establish which bytes and rule lineage enter the
pilot. It cannot transfer precision, recall, completeness, typed fidelity,
domain fit, attribution quality, or any other accuracy-bearing conclusion.
Charter v0.2 requires the pilot's sealed internal validation to establish
those claims.

## 2. Record status

| Field | Value |
|---|---|
| Schema | `gate0-extractor-bridge-v1-draft` |
| Owner | Ben Meddeb |
| Charter | v0.2; external disposition `internal-validation-required` |
| Benchmark terminal status | `NOT_ESTABLISHED` by accepted valid pre-score capacity stop |
| Bridge disposition | `BRIDGE_REQUIRED` |
| Pilot artifact | `<Gate 0>` |
| Independent reviewer | `<Gate 0>` |
| Gate 0 approval | `<not granted>` |
| Authority before approval | none |

## 3. Benchmark-bound producer lineage

The Gate 0 reviewer re-verifies these repository records rather than trusting
this transcription.

| Identity component | Bound value / record |
|---|---|
| Initial Stage-0 candidate source | `9429ab92e7831565606d718af47073ad996a1d01`; [Stage-0 record](../spike/t111/labeling/gate2-v2/stage0/STAGE0.md) and code-path inventory |
| Terminal P0-03 producer source lineage | `8f105581f2a231b4be9ac28fb7c238bcf11a37cd`; accepted P0-03 record in [PLAN.md](../PLAN.md) and [accepted executor review](../spike/t111/labeling/gate2-v2/stage2-prebuild-execute-review-r4.md) |
| Terminal `t111` binary | `sha256:7d6db7fca68981e05758ca41b0b0ae109d935ace71ef228d52b5184928e93f65` |
| Go toolchain | `go version go1.26.5 darwin/arm64`; `sha256:3f947495f00cb7f8088a5cfd694da8dc43869b33f5e7377b048fb18922ffb7e0` |
| Git toolchain | `git version 2.50.1 (Apple Git-155)`; `sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818` |
| Accepted prebuild evidence | [stage2-prebuild-evidence.json](../spike/t111/labeling/gate2-v2/stage2-prebuild-evidence.json), accepted at `fbd84744edb6791ac2e6af1c47e1ef6e009767cf` |
| Terminal outcome | [REPORT.md](../spike/t111/REPORT.md) and accepted [preparation-result review](../spike/t111/labeling/gate2-v2/stage2-preparation-result-review-r1.md) |

The Stage-0 and terminal source anchors differ because the sealed campaign
added reviewed closure and ceremony machinery. The final bridge package must
include the accepted lineage between them; it must not combine an early source
commit with a later binary digest as though they were one build identity.

No score, sample, label set, precision estimate, or recall estimate exists for
this producer under GATE2-V2.

## 4. Pilot artifact freeze

Every field is filled from one clean build before Gate 0 closes.

| Pilot component | Frozen value |
|---|---|
| phebs source commit | `<40-hex>` |
| Worktree state | `<clean; no untracked build inputs>` |
| `grpcgo` source tree digest | `<sha256>` |
| Extractor version / schema | `1.1.0 / t13-v1` or `<reviewed successor>` |
| phebs binary digest | `<sha256>` |
| Go version and executable digest | `<version; sha256>` |
| Git / child-tool identities | `<versions; sha256; or explained N/A>` |
| Build command and environment digest | `<immutable recipe>` |
| Module graph / `go.sum` digest | `<sha256>` |
| Pure-reader allowlist version/digest | `<sha256>` |
| Extractor registry/config | `experimental.provisional_proto_extraction=<frozen value>` plus `<digest>` |
| Test command, results, and log digest | `<Gate 0>` |
| Reproducible rebuild evidence | `<two clean builds or approved equivalent>` |

The artifact is the whole executed path, not only
`internal/extract/extractors/grpcgo`: registry selection, SDK limits, Git object
reader, worker publication, schemas, configuration, binary, and toolchain are
part of the identity. Any byte or material configuration change after approval
voids the bridge and triggers charter §14 change control.

## 5. Known implementation lineage

The productized path currently descends through:

- `619658b` — dark-scope syntactic port of the spike's Go/gRPC rules into the
  pure-reader SDK;
- `bd89691` — unresolved-fact publication, ambiguity hardening, helper/path
  collision handling, package-less service support, and deterministic
  evidence remediation;
- `<Gate 0 source commit>` — exact pilot freeze, including all later
  infrastructure and documentation changes that affect execution.

This lineage is evidence of development provenance only. It does not show
behavioral equivalence or accuracy.

## 6. Semantic and execution delta matrix

Each row receives an exact code/test reference and independent reviewer
disposition in the final package.

| Dimension | Benchmark producer | Pilot producer | Transfer boundary |
|---|---|---|---|
| Execution capability | typed Go analysis over hydrated module closures; controlled exec and module hydration in the ceremony | pure reader over supplied immutable Git objects; no builds, downloads, network, plugins, or dynamic loading | capability reduction is intentional; no typed-fidelity transfer |
| Client-call resolution | `go/types` receiver/interface resolution | method name resolves only when unique across the repository's generated-stub index; ambiguity abstains | candidate mechanics traceable; accuracy must be remeasured |
| Server registration | generated registration declarations and syntactic call recognition within the typed producer | generated `Register<X>Server` index plus syntactic registration scan | rule lineage may transfer; coverage/accuracy may not |
| Generated-stub index | producer-specific typed/module view | two-pass repository-local `*_grpc.pb.go` derived-name index; no blob retention across passes | new execution model; validate internally |
| Fact predicates | client call and server registration facts | `CALLS_OPERATION`, `REGISTERS_GRPC_SERVICE`, plus explicit unresolved facts | predicate intent transfers; emitted population does not |
| Confidence | benchmark exact/typed fact schema | `heuristic` client calls, `derived` registrations, `unresolved` abstentions | lower tiers are truth-telling, not equivalence |
| Ambiguity | typed resolver or fail-closed diagnostics | singleton-only resolution; duplicate FQN/helper/path candidates abstain | no guess transfer |
| Code role | validated precedence for vendor/mock/generated/test/production | precedence ported from the spike and unit-pinned | mechanical classifier lineage only |
| Evidence | benchmark fact files with pinned corpus coordinates | content-addressed atoms + snapshot occurrences with exact blob/span evidence | evidence integrity is separately testable; no accuracy transfer |
| Publication/coverage | sealed benchmark ceremony outputs | atomic extraction runs, coverage certificates, explicit failures and unresolved counts | product observability is new and must be bound |
| Corpus/domain | four public systems under the closed campaign | one authorized company RPC and frozen internal universe | external-to-internal domain transfer prohibited |
| Outcome | no selection, labeling, or score; `NOT_ESTABLISHED` | future sealed internal round | only the internal round can establish accuracy |

## 7. Required trace and verification package

Before approval, the Owner supplies:

1. a source-level rule map from each pilot rule ID and role classifier branch
   to its benchmark ancestor or marks it `new`;
2. an exact diff classification from the accepted terminal producer lineage
   through `619658b`, `bd89691`, and the pilot freeze;
3. clean-build receipts binding source, modules, binary, toolchain, registry,
   pure-reader allowlist, and configuration;
4. two-run byte-identical product evidence on the approved non-partner fixture
   corpus for facts, spans, tiers, roles, unresolved counts, and coverage;
5. focused regressions for generated-helper collisions, duplicate service
   FQNs, path-separated lineage, package-less services, malformed/oversized
   inputs, exact spans, and ambiguity abstention;
6. a guard proving no product output states or implies a measured accuracy or
   completeness claim;
7. a signed statement that pilot internal validation remains required and
   that this bridge was not used to construct its recall-positive frame or
   labels.

Passing these checks supports reproducibility and mechanics only. A failed
check blocks bridge approval; it is not converted into an unresolved pilot
accuracy question.

## 8. Allowed and prohibited conclusions

### Allowed after approval

- the named pilot bytes descend from documented benchmark rule lineage;
- the named pilot artifact reproducibly executes the bounded pure-reader
  mechanics described here;
- known semantic and capability differences are explicit inputs to the
  internal validation design.

### Prohibited

- the pilot extractor passed the external benchmark;
- typed resolution accuracy transfers to syntactic resolution;
- public-corpus behavior predicts company-code behavior;
- deterministic output implies correctness, recall, or completeness;
- evidence integrity implies relationship accuracy;
- an approved bridge satisfies the internal accuracy gate or unlocks Epic 16.

## 9. Gate 0 review decision

| Field | Value |
|---|---|
| Pilot artifact identity complete and reproducible | `<yes / no>` |
| Benchmark lineage independently reverified | `<yes / no>` |
| Delta matrix complete | `<yes / no>` |
| Required verification package passes | `<yes / no>` |
| Accuracy-transfer prohibition accepted | `<yes / no>` |
| Disposition | `<APPROVE_IDENTITY_ONLY | REVISE | REJECT>` |
| Conditions / unresolved findings | `<list or none>` |
| Worksheet digest | `<sha256>` |
| Timestamp | `<RFC3339>` |
| Independent reviewer | `<name / signature>` |
| Sponsor | `<name / signature>` |
| Migration owner | `<name / signature>` |
| Security reviewer | `<name / signature>` |
| Pilot lead | `Ben Meddeb / <signature>` |

Only `APPROVE_IDENTITY_ONLY` with every required field complete satisfies the
artifact-bridge arm of Gate 0. It carries the permanent condition
`internal-validation-required` and grants no other Gate 0 requirement,
measurement authority, or Epic 16 authority.
