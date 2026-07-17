# phebs evidence-pack card template

*Reusable capability and validation template · version 0.1*

**Related documents:** [product vision](./VISION.md) ·
[pilot charter](./PILOT_CHARTER.md) · [adoption pitch](./PITCH.md) · [investigations](./INVESTIGATIONS.md)

## How to use this card

Create one completed card for every relationship type and materially different
extractor claim. A passing result for one card must not be cited as validation
for another predicate, language, framework, version, or corpus.

The card is both a product contract and a release gate. It defines exactly
what the pack can assert, what it cannot assert, how incomplete analysis is
represented, how the claim was measured, and when the pack must remain dark or
be withdrawn.

Delete this instruction section when publishing a completed card.

### Template release invariant

A card must not enter `released` status while any required field is blank,
contains a placeholder or unexplained `N/A`, reports a claimed property as
`not measured`, lacks required approval, has expired validation, exceeds its
operating envelope, or has an unresolved release-blocking risk. Exceptions may
change workflow disposition; they may not waive evidence integrity,
authorization, or validation gates.

---

## Pack: `<PACK_DISPLAY_NAME>`

| Field | Value |
|---|---|
| Pack ID | `<stable.pack.id>` |
| Status | `<design / experimental-dark / shadow / released / suspended / retired>` |
| Predicate(s) | `<PREDICATE_ONE, PREDICATE_TWO>` |
| Language/framework | `<language, framework, version boundaries>` |
| Pack version | `<semantic version>` |
| Extractor artifact | `<source commit, binary digest, toolchain digest>` |
| Schema version | `<fact/evidence schema version>` |
| Release binding | `<PackRelease id binding card, manifest, implementation, and validation digests>` |
| Owner | `<team/person>` |
| Independent validation owner | `<team/person>` |
| Last measured | `<date>` |
| Validation expires/review due | `<date or event-triggered rule>` |
| Exception authority | `<external policy owner; never the pack itself>` |
| Current decision | `<dark / advisory / eligible for named workflows>` |

## 1. Supported claim

State one narrow, falsifiable claim in the form:

> For `<declared source/build population>`, this pack identifies
> `<relationship>` when it is expressed through `<supported constructs>`, and
> emits `<typed fact>` with `<evidence and attribution>`.

**Supported claim:**

`<write the exact claim>`

### Unit of analysis

`<occurrence / target-contract edge / service-contract edge / handler-control
association / other>`

### Intended workflow decisions

- `<workflow and decision this fact may support>`
- `<workflow and decision this fact may support>`

## 2. Explicit non-claims

This pack does **not** establish:

- `<runtime execution, reachability, deployment, semantic correctness, etc.>`
- `<unsupported language/framework or dynamic behavior>`
- `<compliance, safety, exploitability, ownership correctness, etc.>`

State the most tempting overgeneralization explicitly:

> A fact from this pack must not be interpreted as `<prohibited inference>`.

## 3. Predicates and fact schema

| Predicate | Subject | Object | Qualifiers | Evidence required |
|---|---|---|---|---|
| `<PREDICATE>` | `<identity>` | `<identity>` | `<role/origin/reachability/etc.>` | `<blob/span/rule/etc.>` |

### Stable identity rules

- Hash algorithm and canonical serialization:
  `<algorithm; canonical field encoding/order; namespace/domain separator>`.
- Evidence identity:
  `H(namespace, blob_digest, byte_span, rule_id, extractor_version,
  adapter_version)`.
- Byte-span convention: `<for example, zero-based half-open byte offsets over
  the exact blob bytes>`.
- Occurrence identity binds evidence separately to repository/monorepo,
  path, snapshot, and visibility scope.
- `<domain-specific contract identity rule>`.
- `<service/target identity rule>`.

Content-derived identifiers are not exposed, compared, counted, or accepted as
query keys unless the requesting principal has an authorized occurrence
association. Cross-scope equality must not become an existence oracle.

### Example fact

```json
{
  "predicate": "<PREDICATE>",
  "subject": "<subject-id>",
  "object": "<object-id>",
  "qualifiers": {
    "target_role": "<role>",
    "source_origin": "<origin>",
    "reachability": "<state>"
  },
  "evidence_basis": "<evidenced-or-derived>",
  "semantic_resolution": "<resolved-or-ambiguous-or-unresolved>",
  "repository_or_monorepo": "<source-universe-id>",
  "snapshot": "<snapshot-id>",
  "visibility_scope_id": "<authorized-scope-id-or-digest>",
  "evidence_id": "<content-derived-id>",
  "occurrence": {
    "path": "<authorized/path>",
    "blob_digest": "<digest>",
    "byte_span_half_open": [0, 0]
  },
  "rule_id": "<rule-id>",
  "extractor_version": "<version>",
  "adapter_version": "<version>",
  "schema_version": "<version>",
  "extractor_binary_digest": "<digest>",
  "processing_state": "analyzed",
  "attribution_states": {
    "build_target": "<resolved-or-ambiguous-or-unresolved-or-not-applicable>",
    "deployable": "<resolved-or-ambiguous-or-unresolved-or-not-applicable>",
    "service": "<resolved-or-ambiguous-or-unresolved-or-not-applicable>",
    "owner": "<resolved-or-ambiguous-or-unresolved-or-not-applicable>"
  }
}
```

The example is illustrative unless explicitly marked as measured output.

## 4. Inputs and provenance

| Input | Required | Authority/provenance | Snapshot/freshness rule | Failure behavior |
|---|---|---|---|---|
| Source | `<yes/no>` | `<git/blob identity>` | `<pinning rule>` | `<partial/stop>` |
| Build graph | `<yes/no>` | `<system/adapter>` | `<version rule>` | `<unresolved/stop>` |
| Service catalog | `<yes/no>` | `<system/adapter>` | `<version rule>` | `<unresolved/stop>` |
| Deployment metadata | `<yes/no>` | `<system/adapter>` | `<time rule>` | `<unknown>` |
| Runtime observations | `<yes/no>` | `<system/adapter>` | `<window>` | `<independent signal only>` |
| Policy/control catalog | `<yes/no>` | `<owner/version>` | `<version rule>` | `<unknown>` |

Conflicting inputs are represented as `<conflict semantics>`. The pack does
not silently select one authority unless the published claim explicitly names
that authority.

## 5. Supported constructs

| Construct/pattern | Supported? | Rule/version | Notes |
|---|---|---|---|
| `<direct invocation>` | `<yes/no/partial>` | `<rule>` | `<notes>` |
| `<wrapper/alias>` | `<yes/no/partial>` | `<rule>` | `<notes>` |
| `<generated code>` | `<yes/no/partial>` | `<rule>` | `<notes>` |
| `<build tags/config variants>` | `<yes/no/partial>` | `<rule>` | `<notes>` |

## 6. Blind spots and unresolved semantics

| Blind spot | Detectable as unsupported? | Effect on claim | User-visible representation |
|---|---|---|---|
| `<reflection/dynamic name/etc.>` | `<yes/no/partial>` | `<possible false negative/etc.>` | `<unknown/excluded/note>` |

Unknown extractor blind spots are not converted into enumerated exclusions.
Measured bounds apply only to the sampled population and design assumptions;
they do not eliminate residual unknown risk or establish performance after
domain shift. Record `<monitoring, internal shadow, error-intake, and
revalidation obligations>`.

## 7. Coverage semantics

### Coverage denominator

`<define the independently enumerated eligible universe>`

### Processing states

- `analyzed` — `<definition>`
- `excluded` — `<definition and allowed reasons>`
- `partial` — `<definition>`
- `failed` — `<definition>`

Every eligible unit must reconcile to exactly one terminal processing state.

Reconciliation is an accounting guarantee, not a success claim. Freeze and
report outcome-rate gates separately:

| Measure | Denominator | Observed count/rate | Pass threshold | Conditional threshold | Stop threshold |
|---|---|---:|---:|---:|---:|
| `analyzed` | `<independently enumerated eligible units>` |  |  |  |  |
| `partial + failed` | `<same eligible-unit denominator>` |  |  |  |  |
| `excluded` | `<same eligible-unit denominator>` |  |  |  |  |

Every exclusion reason is enumerated and reviewed. A released claim may not
use terminal-state reconciliation to conceal a high partial, failed, or
excluded rate.

### Attribution states

- `resolved` — `<definition at each required mapping hop>`
- `ambiguous` — `<definition>`
- `unresolved` — `<definition>`
- `not_applicable` — `<definition>`

Processing coverage and attribution coverage use separately declared
denominators. A successful mapping must not remove failed processing units from
the processing denominator, and attribution coverage must not be calculated
only over successful mappings.

### Negative-result wording

Approved wording when the eligible universe is independently enumerated and
every unit reconciles to a terminal processing state:

> `<No supported facts were found within visibility scope V, snapshot S, and
> declared eligible universe U; known exclusions and failures are listed.>`

Required wording when any eligible unit is partial, failed, or unreconciled:

> `<Analysis is incomplete. No supported facts were found among analyzed
> units; partial, failed, excluded, and unresolved scope is listed
> separately.>`

Prohibited wording:

> `<No consumers exist / all services comply / the control is universally
> present.>`

Coverage certificates are scoped to the requesting principal's visible
universe and must not reveal inaccessible repository, path, target, service,
or remainder existence or counts.

### Absence blocker codes

The pack declares its blocker-code vocabulary (e.g. `UNITS_FAILED`,
`EXCLUSION_RATE_EXCEEDED`, `ATTRIBUTION_UNRESOLVED`,
`PACK_VALIDATION_EXPIRED`, `SCOPE_NOT_ENUMERATED`, `STALE_ANALYSIS`).
Absence eligibility in headers and envelopes is derived from this declared
set and the pack's decision rules — never from terminal accounting alone.

## 8. Decision semantics

The following axes are orthogonal and must not be collapsed into one status:

| Axis | Allowed states | Meaning |
|---|---|---|
| Evidence basis | `evidenced`, `derived` | whether the assertion is directly reproduced from evidence or deterministically computed |
| Semantic resolution | `resolved`, `ambiguous`, `unresolved` | whether the predicate itself has one supported interpretation |
| Processing state | `analyzed`, `excluded`, `partial`, `failed` | whether the eligible source unit was successfully processed |
| Attribution state, per hop | `resolved`, `ambiguous`, `unresolved`, `not_applicable` | whether target/deployable/service/owner identity was mapped |
| Decision conclusion | `<pack-specific states; for conformance, evidenced conforming/nonconforming/unknown>` | what a versioned decision rule permits a workflow to conclude |

Every pack must define its evidence basis precisely:

- `evidenced` — the assertion is directly supported by reproducible source
  evidence under the declared predicate and rule version;
- `derived` — the assertion is the deterministic output of a named,
  versioned rule over declared fact/input identities. Store the rule,
  complete input set, and derivation trace; human judgment is not `derived`.

Semantic `ambiguous` means available evidence supports multiple allowed
interpretations that the pack cannot distinguish. Semantic `unresolved` means
a required input or supported proof for the predicate is absent or failed.
These states do not imply that source processing or attribution succeeded.

### Policy or expectation identity

| Field | Value |
|---|---|
| Policy/expectation ID | `<stable ID>` |
| Version | `<version>` |
| Effective interval | `<start/end or open-ended rule>` |
| Authority/source | `<system, document, or owner>` |
| Policy owner | `<team/person>` |
| Evaluation rule | `<rule ID/version>` |

### Deterministic decision mapping

| Fact/coverage/input condition | Conclusion | Eligible action or workflow |
|---|---|---|
| `<recognized positive evidence + complete required inputs>` | `<evidenced conforming/nonconforming or named fact>` | `<allowed action>` |
| `<ambiguous or conflicting evidence>` | `unknown` | `<advisory/triage only>` |
| `<partial, failed, or unreconciled processing>` | `unknown` | `<rerun/escalate; no absence-based decision>` |
| `<required attribution unresolved>` | `unknown` | `<owner-resolution workflow only>` |
| `<policy/configuration unavailable or stale>` | `unknown` | `<refresh/escalate>` |

The completed table must be exhaustive for every evidence-basis,
semantic-resolution, processing, and attribution state, required external
input, and coverage condition used by a named workflow. The conclusion is
reproducible from the recorded facts, inputs, and rule version; it is not
selected ad hoc by the pack owner.

For conformance-shaped packs, use three-valued conclusions:

1. **Evidenced conforming** — recognized positive evidence satisfies the
   versioned policy under the supported semantics.
2. **Evidenced nonconforming** — recognized positive evidence establishes a
   versioned policy violation under the supported semantics.
3. **Unknown** — coverage, configuration, metadata, or semantics are
   insufficient.

Absence of a matching fact must not silently become compliance.

Human dispositions (`accepted`, `remediate`, `exempt`, `false positive`,
`unknown`) are stored separately from evidence and never rewrite the source
fact. Each disposition records its rationale, actor, accountable owner,
timestamp, expiry/review date, applicable policy version, and referenced fact
and coverage identities. An expired disposition returns to the policy-defined
default state.

### Diff and comparability semantics

The pack declares what makes two runs comparable (same revision, pack
version, and coverage class) and how an absent fact is cause-classified:
source deletion, extractor/pack version change, authorization change,
catalog change, failed analysis, or narrowed scope. Only comparable runs
may render an unqualified added/removed; every other cause renders as its
cause, and failed or inaccessible units never render as "removed."

### Challenge intake and error ledger

Evidence challenges (e.g. a `false attribution` disposition) enter the
pack's quality review: intake with triage duty and owner, adjudication
against the frozen evidence, and an append-only error ledger recording
confirmed errors by class. Challenges never modify facts; confirmed
errors feed the suspension triggers below.

## 9. Validation design

| Field | Definition |
|---|---|
| Preregistration artifact | `<immutable ID/digest and timestamp>` |
| Benchmark artifact | `<corpus/answer-key ID, digest, and custody>` |
| Target population | `<population to which the result may generalize>` |
| Sampling unit | `<exact unit>` |
| Precision frame | `<independently defined frame>` |
| Recall-positive frame | `<construction independent of candidate extractor>` |
| End-to-end relationship frame | `<independent frame for the claimed service/owner-level artifact>` |
| Cluster/strata handling | `<design and estimator>` |
| Planned sample/minimum positives | `<per metric and stratum>` |
| Achieved sample/effective sample | `<per metric and stratum>` |
| Weights | `<selection probabilities and analysis weights>` |
| Missing/unlabelable reviews | `<predeclared treatment and observed counts>` |
| Reviewers | `<independent reviewers and custody>` |
| Agreement/adjudication | `<overlap, metric, process>` |
| Multiplicity family | `<all confirmatory claims>` |
| Confidence method | `<one-sided interval/bound and joint coverage>` |
| Pass threshold | `<metric-specific threshold>` |
| Stop criteria | `<conditions that prevent release>` |
| Holdout/reuse policy | `<fresh unseen data after disclosed failure>` |
| Evidence reproduction design | `<sample, identity/version fields, threshold>` |
| Processing-state accuracy design | `<sample, labels, threshold>` |
| Processing outcome-rate gates | `<analyzed, partial/failed, and excluded pass/conditional/stop thresholds>` |
| Attribution accuracy/coverage design | `<each hop, denominators, thresholds>` |
| End-to-end relationship design | `<direct precision/recall frames, power, thresholds>` |

### Validation result

| Metric | N / effective N | Counts | Estimate | Bound, method, confidence | Threshold | Result |
|---|---:|---|---:|---|---:|---|
| `<precision>` |  | `<TP/FP/missing>` |  |  |  | `<pass/fail/not measured>` |
| `<recall>` |  | `<TP/FN/missing>` |  |  |  | `<pass/fail/not measured>` |
| `<evidence reproduction>` |  | `<success/failure/missing>` |  |  |  | `<pass/fail/not measured>` |
| `<processing-state accuracy>` |  | `<correct/incorrect/missing>` |  |  |  | `<pass/fail/not measured>` |
| `<analyzed completion rate>` |  | `<analyzed/eligible>` |  |  |  | `<pass/fail/not measured>` |
| `<partial + failed rate>` |  | `<partial+failed/eligible>` |  |  |  | `<pass/fail/not measured>` |
| `<excluded rate>` |  | `<excluded/eligible by reason>` |  |  |  | `<pass/fail/not measured>` |
| `<build-target attribution accuracy>` |  | `<correct/incorrect/missing>` |  |  |  | `<pass/fail/not measured>` |
| `<build-target attribution coverage>` |  | `<resolved/eligible/ambiguous/unresolved>` |  |  |  | `<pass/fail/not measured>` |
| `<deployable attribution accuracy>` |  | `<correct/incorrect/missing>` |  |  |  | `<pass/fail/not measured>` |
| `<deployable attribution coverage>` |  | `<resolved/eligible/ambiguous/unresolved>` |  |  |  | `<pass/fail/not measured>` |
| `<service attribution accuracy>` |  | `<correct/incorrect/missing>` |  |  |  | `<pass/fail/not measured>` |
| `<service attribution coverage>` |  | `<resolved/eligible/ambiguous/unresolved>` |  |  |  | `<pass/fail/not measured>` |
| `<owner attribution accuracy>` |  | `<correct/incorrect/missing>` |  |  |  | `<pass/fail/not measured>` |
| `<owner attribution coverage>` |  | `<resolved/eligible/ambiguous/unresolved>` |  |  |  | `<pass/fail/not measured>` |
| `<end-to-end relationship precision>` |  | `<TP/FP/missing>` |  |  |  | `<pass/fail/not measured>` |
| `<end-to-end relationship recall>` |  | `<TP/FN/missing>` |  |  |  | `<pass/fail/not measured>` |

Report stratum-level counts, selection weights, missing reviews, exclusions,
and minimum-positive checks in the attached result artifact. If a claimed
property is underpowered, not measured, or cannot be reproduced from the
named benchmark and analysis artifact, the release result is `fail`; it is
not converted to a conditional pass.

**Benchmark and extractor scope statement:**

`<This result applies only to pack version X, extractor artifact E, benchmark
B, population P, and supported constructs C.>`

### Internal/domain-shift evaluation

| Population/snapshot | Artifact IDs | Sample and method | Metric/bound | Threshold/result | Permitted decision scope |
|---|---|---|---|---|---|
| `<internal or shifted population>` | `<source, pack, extractor>` | `<N, effective N, selection>` | `<estimate, bound, confidence>` | `<threshold; pass/fail>` | `<dark/advisory/named workflow>` |

`<state required shadow cadence, drift trigger, and whether the result remains
advisory>`

## 10. Authorization, privacy, and retention

- Authorization is evaluated on occurrence/snapshot associations and every
  artifact/query path, not on globally deduplicated atoms alone.
- `<principal propagation and result-time authorization rule>`.
- `<coverage/count/diff non-disclosure rule>`.
- `<revocation propagation SLA>`.
- `<retention duration>`.
- Revocation, mandatory deletion, and legal policy override proof retention.
- `<audit logging and sensitive-field policy>`.

## 11. Query and artifact surface

| Surface | Available? | Contract/SLO | Authorization behavior |
|---|---|---|---|
| Assertion API | `<yes/no>` | `<schema/SLO>` | `<rule>` |
| UI | `<yes/no>` | `<view/export>` | `<rule>` |
| MCP | `<yes/no>` | `<tools/output bounds>` | `<end-user identity rule>` |
| Proof bundle | `<yes/no>` | `<format/version>` | `<rule>` |
| Snapshot diff | `<yes/no>` | `<semantics>` | `<rule>` |

Example supported questions:

- `<question>`
- `<question>`

Questions this pack must refuse or qualify:

- `<question and required qualification>`

## 12. Operating envelope

| Measure and unit | Workload/environment | Measured date and sample | Tested value/range | Supported limit and margin | Stop/degrade behavior |
|---|---|---|---:|---:|---|
| Cold processing `<unit>` |  |  |  |  |  |
| Incremental freshness `<unit>` |  |  |  |  |  |
| CPU `<unit>` |  |  |  |  |  |
| Memory `<unit>` |  |  |  |  |  |
| Storage growth `<unit>` |  |  |  |  |  |
| Backlog/catch-up `<unit>` |  |  |  |  |  |
| Query p95 `<unit>` |  |  |  |  |  |
| Recovery RPO/RTO `<unit>` |  |  |  |  |  |
| Maximum eligible universe `<units per run>` |  |  |  |  |  |

State whether the pack is eligible for interactive, PR-time, batch, incident,
or audit workflows. A pack must not be promoted into a latency/availability
class it has not measured. A supported limit must remain within the validated
workload and environmental range and include an explicit safety margin; an
unmeasured workload class is ineligible for release into that workflow.

## 13. Release, suspension, and retirement

### Status transitions

| From | To | Minimum requirement |
|---|---|---|
| design | experimental-dark | supported/non-claim boundaries, schema, coverage states, owner, and stop conditions documented |
| experimental-dark | shadow | security approval, frozen validation design, reproducible artifact, and complete authorization model |
| shadow | released | every claimed property measured and passing; operating envelope and expiry set; required approvals complete; no release-blocking risks |
| released | suspended | any automatic suspension trigger or expired validation |
| suspended | released | root cause resolved and every required revalidation/approval completed on the applicable artifact |
| any | retired | retirement plan approved; query and retained-artifact behavior documented |

The status field is derived from these gates; it is not a free-form owner
assertion. An unmeasured claimed property, expired result, missing approval,
blank operating limit, or release-blocking open risk prevents `released`.

### Release requirements

- every claimed extraction, coverage, attribution, and evidence-reproduction
  property has a passing result under the frozen design;
- processing-state accounting and the analyzed/partial/failed/excluded
  outcome-rate gates pass independently;
- every claimed end-to-end relationship has a direct passing quality result;
  component metrics or their product are not a substitute;
- `<additional validation gates>`;
- `<security/authorization gates>`;
- `<operating gates and workflow class>`;
- `<documentation and ownership gates>`;
- validation expiry/review date and automatic suspension owner recorded;
- exception authority is external to the pack and cannot waive evidence,
  authorization, or validation integrity.
- the executable pack manifest is machine-validated against this card at
  release through the signed PackRelease record binding both digests (plus
  implementation, binary, and validation digests); any divergence blocks
  release. Neither card nor manifest embeds the other's digest directly.

### Automatic suspension triggers

- extractor or dependency version changes outside the measured claim;
- confirmed-error accumulation in the challenge ledger exceeding the
  validated bound's allowance for the affected metric;
- benchmark or internal failures below threshold;
- authorization or evidence-reproduction failure;
- unsupported framework/language change invalidates the population claim;
- `<domain-specific trigger>`.

### Expiry semantics

Expired validation suspends new claims, promotions, and release status; it
does not delete history. Already-published facts and dossiers remain
queryable with the expired-validation status surfaced; dossiers
self-describe via their cited validation identity.

### Revalidation policy

`<when a new benchmark, holdout, or internal shadow round is required; expiry
date/event; artifact and dependency changes that trigger suspension>`

### Retirement behavior

`<query behavior, bundle retention, version availability, and user notice>`

## 14. Risks, open questions, and decisions

| ID | Type | Statement | Owner | Due | Status |
|---|---|---|---|---|---|
| `<R-1>` | `<risk/question/decision>` | `<statement>` | `<owner>` | `<date>` | `<open/closed>` |

## 15. Sign-off and history

### Release decision record

| Gate | Result | Evidence reference | Approver | Date |
|---|---|---|---|---|
| Claim and schema completeness | `<pass/fail>` |  |  |  |
| Statistical validation | `<pass/fail>` |  |  |  |
| Coverage and attribution | `<pass/fail>` |  |  |  |
| Authorization/security | `<pass/fail>` |  |  |  |
| Operating envelope | `<pass/fail>` |  |  |  |
| Workflow-owner acceptance | `<pass/fail>` |  |  |  |

| Decision field | Value |
|---|---|
| Final release decision | `<design / experimental-dark / shadow / released / suspended / retired>` |
| Decision artifact ID/digest | `<immutable reference>` |
| Effective and expiry dates | `<dates>` |

| Approval | Name | Date | Pack version |
|---|---|---|---|
| Pack owner |  |  |  |
| Independent validation owner |  |  |  |
| Security/authorization owner |  |  |  |
| Workflow owner |  |  |  |

| Version | Date | Change | Revalidation required? |
|---|---|---|---|
| 0.1 | `<date>` | initial design | yes |