# phebs — six-week monorepo pilot charter

*Proposed execution charter · July 2026 · version 0.2*

**Related documents:** [adoption pitch](./PITCH.md) ·
[product vision](./VISION.md) ·
[investigations](./INVESTIGATIONS.md) ·
[evidence-pack card template](./EVIDENCE_PACK_CARD.md)

Version 0.2 changes only the disposition of the external Go/gRPC benchmark.
GATE2-V2 ended before selection or scoring in an independently accepted,
protocol-defined capacity stop with its method uncompromised. That terminal
record is not an accuracy pass and transfers no accuracy claim. The pilot's
sealed internal validation in §8.2 is therefore the sole path to an
accuracy-bearing decision; every role, authorization, custody, safety, and
internal measurement requirement below remains unchanged.

## 1. Decision this charter governs

This charter governs a bounded, read-only, advisory evaluation of phebs on
one Go/gRPC contract in the company monorepo. It does not authorize a
production dependency, automated enforcement, an API removal, or expansion
to additional protocols or use cases.

The pilot answers one decision:

> Does phebs produce a sufficiently accurate, reproducible, permission-safe,
> owner-attributed consumer-candidate inventory to justify continued internal
> incubation?

The possible outcomes are **continue**, **continue conditionally**, or
**stop and tear down**. A successful pilot authorizes only the next decision;
it does not convert advisory evidence into a deprecation authority.

## 2. Proposed anchor

| Field | Proposed value |
|---|---|
| Migration | `userdevices → uberdevices v2` |
| Contract | legacy `userdevices.FindRelatedUserDevices` RPC |
| Language/protocol | Go/gRPC |
| Baseline | one pinned monorepo commit/tree digest (`S0`) |
| Change window | subsequent immutable snapshots processed during the pilot (`S1…Sn`) |
| Evaluation unit | `(canonical service, gRPC operation, monorepo snapshot, build configuration)` |

At the authorization meeting, the sponsor and migration owner must confirm
the canonical IDL identity, that the RPC remains active enough to provide a
meaningful workflow baseline, and that the available manual and runtime
evidence is sufficient for an independent shadow evaluation.

If the proposed RPC is rejected, the replacement is selected **before source
ingestion** and this charter is versioned and reapproved. The pilot must not
swap contracts after phebs results or gold labels have been revealed.

## 3. Scope

### 3.1 In scope

- Monorepo-wide candidate discovery for the selected RPC within the
  requester's authorized source universe.
- Typed Go/gRPC client-call and server-registration extraction.
- Attribution from occurrence → build target → deployable → canonical
  service → recorded owner.
- Classification by target role, source origin, behavioral role, and
  reachability.
- Explicit processing states (`analyzed`, `excluded`, `partial`, `failed`)
  and separate attribution states (`resolved`, `ambiguous`, `unresolved`,
  `not_applicable`).
- Comparison with an independently constructed internal baseline.
- Processing of subsequent commits to measure freshness and prototype
  snapshot diffs.
- Permission-filtered search and MCP retrieval under approved pilot clients.
- Measurement of cold/incremental resource use, evidence reproduction, and
  human workflow time.

### 3.2 Declared source and target universe

The frozen analysis manifest records:

- monorepo commit/tree digest;
- requester/visibility-scope identity or digest;
- included and excluded paths;
- eligible first-party Go build targets and the method used to enumerate
  them independently of the candidate extractor;
- build configuration, tags, generated inputs, dependency locks, and
  external target treatment;
- service-catalog, deployment, ownership, and build-graph snapshot versions;
- extractor, adapter, rule, schema, and phebs binary versions;
- known unsupported languages, frameworks, and dynamic constructs.

Coverage and attribution are reported on separate axes:

- **Processing state:** `analyzed`, `excluded`, `partial`, or `failed` for
  every independently enumerated eligible unit.
- **Attribution state:** `resolved`, `ambiguous`, `unresolved`, or
  `not_applicable` at each mapping hop (target, deployable, service, owner).

Every eligible unit must reconcile to exactly one terminal processing state;
every analyzed fact must reconcile to one attribution state at every required
hop. A high processing-coverage percentage cannot conceal attribution failure,
and an attribution percentage cannot exclude failed processing units from its
declared denominator without saying so.

A representative service slice may be used for the week-one operational
check. It cannot be used to support a completeness-shaped workflow claim.
The evaluated consumer-candidate inventory must account for the full
authorized eligible universe in a terminal processing state, and every
analyzed fact in a separate attribution state at each required hop.

### 3.3 External evidence channels

| Evidence channel | Permitted use | Claim it does not establish |
|---|---|---|
| Git/source snapshot | direct code occurrence and exact provenance | execution or deployment |
| Build graph | potential target and deployable reachability | runtime invocation |
| Service catalog/ownership | recorded service identity and owner | correctness or current accountability |
| Deployment metadata | independently supplied revision/deployable mapping | request-level execution |
| Traffic observations | recent observed callers within a declared window | dormant or unobserved consumers |
| Human disposition | reviewer judgment and workflow state | mutation of the underlying source fact |

Every external input remains labeled by source, snapshot or time window, and
adapter version. Conflicting inputs are reported; phebs does not silently
arbitrate between them.

### 3.4 Out of scope

- Automatic API removal, migration approval, code modification, or ticket
  creation.
- Production enforcement or required PR checks.
- Claims about languages, protocols, operations, or source universes not
  frozen in this charter.
- Runtime reachability, request volume, service health, or actual customer
  impact.
- Proto field-level lineage, semantic compatibility, privacy compliance, or
  error-behavior analysis.
- Evaluation of the broader workflows described in `VISION.md`.

## 4. Pilot hypotheses

| ID | Hypothesis |
|---|---|
| H1 — Discovery | phebs can produce a same-day initial candidate inventory for the selected RPC. |
| H2 — Evidence | sampled findings reproduce exactly from their manifest, blob digest, byte span, rule, extractor, adapter, schema, phebs binary, and toolchain versions. |
| H3 — Extraction | the measured Go/gRPC extractor remains within the preregistered internal accuracy gate on company code. |
| H4 — Attribution | a useful proportion of supported occurrences maps to build targets, deployables, services, and recorded owners without hiding unresolved mappings. |
| H5 — Workflow | generation plus human review consumes materially fewer hours than the reconstructed current workflow. |
| H6 — Authorization | search, MCP, coverage, counts, diffs, and bundles do not expose source or existence information outside the requesting principal's authorized universe. |
| H7 — Operations | cold ingestion, incremental processing, storage, freshness, query latency, recovery, and teardown fit the approved pilot resource envelope. |

## 5. Roles and authority

Every role is named before Gate 0 closes. One person may hold multiple partner
roles where independence requirements do not prohibit it. A blank role,
placeholder, or unexplained `N/A` blocks Gate 0.

| Role | Responsibility | Named person |
|---|---|---|
| Executive/engineering sponsor | authorizes resources, resolves organizational blockers, accepts final decision | `<name>` |
| Pilot lead | operates phebs, maintains manifest and evidence chain, reports results | Ben Meddeb |
| Migration owner | confirms contract, supplies current-workflow evidence, reviews usefulness | `<name>` |
| Build/catalog partner | supplies and explains build graph, deployable, catalog, and ownership metadata | `<name>` |
| Security reviewer | approves pre-ingestion controls and evaluates authorization isolation | `<name>` |
| OSS/Legal reviewers | resolve employment-invention, dependency, license, and provenance requirements | `<name(s)>` |
| Independent label reviewers | construct blinded internal gold labels and adjudicate under the frozen protocol | `<reviewer A>`, `<reviewer B>` |
| Pilot environment owner | provisions the isolated host and confirms teardown | `<name>` |

### 5.1 Capability model *(draft prerequisite item 2)*

This matrix narrows the responsibilities above into explicit capabilities.
It does not assign the remaining named people, authorize an environment, or
close Gate 0. `A` means authorizes or accepts, `E` executes, `P` provides an
input, `R` independently reviews or witnesses, and `—` means the role has no
capability by virtue of that role alone. Access still requires a named
principal, least-privilege credential, and current source/object
authorization.

| Capability | Sponsor | Pilot lead | Migration owner | Build/catalog partner | Security reviewer | OSS/Legal | Label reviewers | Environment owner |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| Approve bounded pilot resources and final decision | A | P | P | — | R | R | — | P |
| Confirm the frozen contract and workflow baseline | — | P | A/P | P | — | — | — | — |
| Approve IP, license, dependency, and provenance posture | — | P | — | — | R | A/R | — | — |
| Approve threat model, ingress/egress, secrets, logging, retention, and authorization controls | — | P | — | P | A/R | R | — | P |
| Provision or destroy the isolated pilot environment | — | — | — | — | R | — | — | E/A |
| Operate phebs and maintain manifests, receipts, and evidence chain | — | E | — | — | R | — | — | P |
| Authorize the first retained source clone after Gate 1 | A | P | A/P | — | R | R | — | E |
| Supply source-universe and current-workflow evidence | — | P | A/P | P | — | — | — | — |
| Supply versioned build, deployable, catalog, and ownership inputs | — | P | R | A/P | — | — | — | — |
| Author or materially extend authorization negative tests | — | P | — | P | A/R | — | — | P |
| Enumerate the independent accuracy population and prepare blinded kits | — | P | R | P | R | — | R | — |
| View unsealed phebs predictions before label freeze | — | E | — | — | — | — | — | — |
| Label and adjudicate the frozen blind sample | — | — | — | — | — | — | E/R | — |
| Review usefulness and accept the inventory as reviewable | — | P | A/R | P | — | — | — | — |
| Decide preserved-versus-destroyed pilot artifacts | P | P | P | — | A/R | R | — | A/E |
| Sign the final continue/conditional/stop record | A | P | A | P | R | R | — | P |

### 5.2 Machine principals

Human role assignment does not implicitly authorize a process. Each machine
principal receives a separate identity and the minimum capability below.

| Machine principal | Permitted capability | Explicit prohibition |
|---|---|---|
| Phebs service | authenticated request handling, current authorization projection, bounded queue coordination | source-host writes, human Decisions, policy expansion |
| Source-sync identity | read only the frozen authorized source universe | repository mutation, organization administration, unrelated repository discovery |
| Metadata-adapter identity | read only approved versioned build/catalog/deployment/ownership inputs | treating metadata as source truth or modifying provider state |
| OIDC client | authenticate admitted identities | object/evidence authorization decisions |
| SurrealDB child | loopback state persistence for the isolated server | remote/public listener or independent access grant |
| Git/zoekt children | bounded mirror/index operation with scrubbed environment | hooks, corpus scripts, credential persistence, policy decisions |
| Extractor context | pure reads of supplied immutable objects within budgets | network, corpus writes, dynamic loading, generators, plugins, or builds |
| MCP client credential | invoke approved read-only tools as one principal | credential sharing, server-side human actions, strengthening qualified conclusions |
| Backup/restore operator identity | create or restore approved encrypted/restricted artifacts under witness | undeclared copies, restore into a connected production environment |

### 5.3 Separation and custody rules

- The Pilot lead cannot serve as an Independent label reviewer or as the sole
  Security reviewer for controls or negative tests they implemented.
- Independent label reviewers cannot access unsealed predictions, candidate
  output, or the migration's existing consumer inventory before label freeze.
- The reviewer roster, adjudicator, assignment, overlap, disclosure edge, and
  substitutions are sealed and audited; silent substitution stops the pilot.
- The Security reviewer authors or materially extends the authorization test
  matrix and accepts the results; the Pilot lead may execute but cannot
  self-accept it.
- Population enumeration and blinded-kit preparation are executed either by a
  mechanical procedure sealed before any prediction exists or by a
  prediction-blind principal; the Security reviewer witnesses whichever
  applies. Viewing unsealed predictions disqualifies a principal from
  shaping the frame afterward.
- A witnessed restore uses a witness independent of the restore operator.
- The Environment owner and Security reviewer jointly decide the
  preserved-versus-destroyed boundary and attest teardown completeness.
- Sponsor authority cannot waive a security, validation, provenance,
  reviewer-custody, or mandatory-deletion stop condition.
- No pilot role grants production write, enforcement, migration approval,
  code-host mutation, or authority to broaden the frozen claim.

The sponsor records the sanctioned allocation of pilot-lead and partner time
before work begins. **Reviewer independence, defined:** label reviewers must
not have contributed to phebs, the candidate extractor, or the migration's
existing consumer inventory, and must have no access to unsealed phebs
predictions during labeling. Reviewer access and custody rules are
documented in the internal evaluation manifest.

## 6. Entry gates

### Gate 0 — authorization and freeze

All must be complete before the pilot clock begins:

- sponsor, roles, time allocation, proposed RPC, and decision authority named;
- canonical IDL identity and source universe confirmed;
- IP/OSS/provenance review either approved or explicitly cleared for the
  bounded evaluation;
- the terminal external Go/gRPC benchmark record is independently accepted
  and satisfies the external-extraction disposition in Section 9;
- the pilot extractor artifact (source commit, binary digest, toolchain
  digest) equals the benchmark-bound candidate artifact, or a versioned
  bridging statement records exactly what changed and is separately approved
  before the clock starts. If the external benchmark ended before scoring,
  the statement transfers only artifact identity, reproducibility, and
  mechanics — never accuracy;
- when the external-extraction disposition is conditional, the Gate 0 record
  states `internal-validation-required`: no accuracy-bearing claim,
  promotion, or Epic 16 continuation is permitted until an adequately powered
  §8.2 round records `gate_status: ESTABLISHED`;
- a completed evidence-pack card at status `shadow` exists for the Go/gRPC
  pack; the internal shadow evaluation fills its internal/domain-shift
  table as a required output;
- current-workflow baseline protocol and statistical accuracy-gold protocol
  preregistered as separate artifacts;
- the accuracy protocol defines the independently enumerated target
  population, precision frame, recall-positive frame, sampling unit,
  clustering/strata, missing-label handling, power calculation, reviewer
  custody, and distinct call-site, attribution-hop, and end-to-end
  service-operation edge labels;
- pass, conditional-pass, and stop thresholds in Section 9 filled and signed;
- every role, date, resource limit, `T_*` field, and required protocol field is
  filled; any blank, placeholder, or unexplained `N/A` blocks release;
- no conflicting pilot or production dependency is implied.

### Gate 1 — pre-ingestion safety and non-source feasibility

All must pass before any retained internal source copy is cloned, indexed, or
analyzed:

- threat model reviewed;
- reproducible binary and dependency/SBOM scan recorded;
- secrets storage, ingress, approved MCP clients, logging, and egress policy
  approved;
- result-time authorization approved, with a negative-test matrix authored
  or materially extended by the Security reviewer — not solely by the pilot
  lead — including persistent-artifact cases (saved results, counts and
  coverage non-disclosure, revocation after pinning);
- retention, revocation, legal-hold, teardown, and deletion behavior approved;
- isolated host and least-privilege identity provisioned;
- host resource ceiling and stop behavior recorded;
- metadata-only size/capacity estimates and authentication/connectivity
  preflight complete without retaining source content.

### Gate 2 — authorized scale checkpoint

Gate 2 occurs only after Gate 1 passes. It may create a retained source copy
under the approved controls.

- authorize and record the initial clone/mirror;
- verify monorepo accessibility and integrity;
- test representative-slice indexing and then the declared scale within the
  hard resource ceiling;
- verify authorization, logs, retention, and stop behavior on the retained
  copy;
- either approve baseline ingestion or stop and execute teardown.

Any confirmed unauthorized disclosure, unresolved provenance block, or failed
external extractor gate stops the pilot immediately. If a retained copy was
already created for Gate 2, teardown begins before baseline extraction.

## 7. Six-week execution sequence

| Week | Work | Exit evidence |
|---|---|---|
| 1 — Qualify | complete Gate 1 using non-source feasibility inputs; only then perform the Gate 2 authorized clone and scale checkpoint | signed safety checklist, retained-source authorization, scale checkpoint, go/stop record |
| 2 — Baseline | freeze `S0`; ingest authorized source; record cold cost; assemble and seal the workflow-time baseline and the separate accuracy-gold protocol inputs | analysis manifest, two baseline custody records, operating measurements |
| 3 — Extract and attribute | run dark extractor; map occurrences through build/catalog metadata; retain unresolved steps | candidate inventory, processing-coverage ledger, attribution report |
| 4 — Shadow evaluate | independent reviewers finalize gold labels; unseal predictions; score extraction and attribution separately | internal accuracy report, reviewer agreement, error taxonomy |
| 5 — Change and workflow | process `S1…Sn`; measure incremental freshness, prototype diff, review time, and evidence reproduction | change ledger, workflow timing, reproduction report |
| 6 — Decide and tear down | compare all gates; document risks and costs; decide stop/conditional/continue; destroy or transfer artifacts under approval | final report, signed decision, teardown or continuation record |

The schedule does not override a gate. A delayed prerequisite shortens or stops
the evaluation; it does not authorize skipping the control. The pilot lead
sends the sponsor a weekly one-page status — gate progress, spend against
resource ceilings, and open risks — so no result is first seen in week six.

## 8. Independent internal baselines

Two different baselines are required. Neither may be substituted for the
other, and both are constructed without access to unsealed phebs predictions.

### 8.1 Current-workflow time and usefulness baseline

This baseline measures the existing human process rather than extractor
accuracy.

1. Freeze the RPC, source scope, start event, end event, and participating
   roles.
2. Reconstruct or observe the manual inventory process using migration
   documents, tickets, owner outreach, build queries, and traffic evidence.
3. Record discovery, triage, routing, rework, and owner-review labor separately.
4. Record the point at which the migration owner considers the inventory
   reviewable and every later addition or correction.
5. Preserve each evidence channel separately; do not treat the resulting list
   as a statistically complete static gold set.

### 8.2 Statistical accuracy gold set

This baseline measures call-site extraction and attribution claims.

1. Freeze the RPC identity, source snapshot, candidate extractor artifact,
   independently enumerated eligible universe, and sampling design.
2. Define separate precision and recall sampling frames. The recall-positive
   frame must be constructed independently of the candidate extractor through
   a method documented in the preregistered protocol.
3. Define the sampling unit and clustering/strata so repeated generated or
   wrapper patterns cannot dominate the result.
4. Define distinct labels and denominators for source call sites, build-target
   mappings, deployable mappings, service mappings, owner-record resolution,
   and end-to-end `(canonical service, operation)` edges. Construct the
   end-to-end recall-positive frame independently; do not infer service-edge
   recall by multiplying call-site recall and attribution coverage.
5. Specify missing/unlabelable handling, reviewer overlap, adjudication,
   confidence method, power, multiplicity, and minimum sample size before
   unsealing.
6. Have reviewers label blind samples and freeze the adjudicated gold set and
   its commitment before phebs predictions are unsealed.
7. Score extraction and every attribution hop for **accuracy and coverage** as
   separate claims, then score end-to-end service-operation edge precision and
   recall directly against its independently constructed frame.
8. Record disagreements, wrappers, macros, build tags, generated sources,
   catalog conflicts, and unsupported patterns in an error taxonomy.

Traffic observations may corroborate or contradict a candidate, but they do
not define the static recall population and are never relabeled as source
facts.

## 9. Evaluation and decision gates

Values marked `T_*` are mandatory charter fields. They are agreed and frozen
at Gate 0; they must not be selected after results are visible.

| Gate | Pass | Conditional pass | Stop |
|---|---|---|---|
| External extraction | the independently accepted terminal record is `ESTABLISHED` and all published Go/gRPC benchmark gates pass | the independently accepted terminal record is `NOT_ESTABLISHED` solely because a protocol-defined capacity or feasibility stop occurred before selection, disclosure, or scoring, with method and custody uncompromised; Gate 0 records `internal-validation-required`, and no accuracy claim transfers | any measured threshold miss; integrity, custody, or reproducibility failure; `INVALID` or `ABORTED`; an unreviewed terminal outcome; or any attempt to reinterpret or retry a closed record |
| Internal call-site quality | meets `T_INTERNAL_PRECISION` and `T_INTERNAL_RECALL` under the frozen, adequately powered design | none; an underpowered or inconclusive round carries no accuracy claim | either bound misses its threshold, the design is underpowered, or a systematic failure invalidates the claim; remediation requires a fresh unseen round |
| Processing coverage | 100% of independently enumerated eligible units reconcile to a terminal processing state; sampled state accuracy ≥ `T_COVERAGE_STATE_ACCURACY` | none | unreconciled units, inaccurate state assignment, or silent dropping |
| Usable processing completion | analyzed rate ≥ `T_ANALYZED_RATE_PASS`; partial + failed rate ≤ `T_INCOMPLETE_RATE_PASS`; excluded rate ≤ `T_EXCLUDED_RATE_PASS` | analyzed rate ≥ `T_ANALYZED_RATE_CONDITIONAL`, partial + failed rate ≤ `T_INCOMPLETE_RATE_STOP`, and excluded rate ≤ `T_EXCLUDED_RATE_STOP`, with every miss explained and bounded | analyzed rate below `T_ANALYZED_RATE_CONDITIONAL`, either rate above its stop threshold, or exclusions conceal supported eligible units |
| Build-target attribution | accuracy ≥ `T_TARGET_ACCURACY`; coverage ≥ `T_TARGET_COVERAGE_PASS` | accuracy passes and `T_TARGET_COVERAGE_CONDITIONAL` ≤ coverage < `T_TARGET_COVERAGE_PASS`, with bounded remediation | accuracy fails, coverage < `T_TARGET_COVERAGE_CONDITIONAL`, or failures are hidden |
| Deployable attribution | accuracy ≥ `T_DEPLOYABLE_ACCURACY`; coverage ≥ `T_DEPLOYABLE_COVERAGE_PASS`; unresolved rate ≤ `T_DEPLOYABLE_UNRESOLVED_PASS` | accuracy passes, coverage ≥ `T_DEPLOYABLE_COVERAGE_CONDITIONAL`, unresolved rate ≤ `T_DEPLOYABLE_UNRESOLVED_STOP`, and at least one pass threshold is missed | accuracy fails, coverage falls below the conditional floor, unresolved rate exceeds its stop threshold, or conflicts are silently arbitrated |
| Service attribution | accuracy ≥ `T_SERVICE_ACCURACY`; coverage ≥ `T_SERVICE_COVERAGE_PASS`; unresolved rate ≤ `T_SERVICE_UNRESOLVED_PASS` | accuracy passes, coverage ≥ `T_SERVICE_COVERAGE_CONDITIONAL`, unresolved rate ≤ `T_SERVICE_UNRESOLVED_STOP`, and at least one pass threshold is missed | accuracy fails, coverage falls below the conditional floor, unresolved rate exceeds its stop threshold, or conflicts are silently arbitrated |
| Owner-record resolution | accuracy against the pinned ownership source ≥ `T_OWNER_ACCURACY`; coverage ≥ `T_OWNER_COVERAGE_PASS` | accuracy passes and coverage lies within the preregistered conditional band | accuracy fails or coverage < `T_OWNER_COVERAGE_CONDITIONAL` |
| End-to-end service-operation edge quality | direct service-edge precision ≥ `T_SERVICE_EDGE_PRECISION` and recall ≥ `T_SERVICE_EDGE_RECALL` under the frozen, adequately powered design | none; component gates cannot substitute for this result | either bound misses its threshold, the service-edge frame is not independent, or the design is underpowered; remediation requires a fresh unseen round |
| Evidence reproduction | 100% of sampled findings reproduce to the frozen blob/span, rule, extractor, adapter, schema, binary, and toolchain versions | none | any unexplained reproduction failure |
| Authorization isolation | zero unauthorized results or existence disclosures across the negative-test matrix; revocation meets `T_REVOCATION_SLA` | none | any confirmed unauthorized disclosure |
| Workflow improvement | reviewable inventory within `T_INITIAL_INVENTORY`; labor reduction ≥ `T_LABOR_PASS` | inventory time ≤ `T_INITIAL_INVENTORY_CONDITIONAL` and labor reduction ≥ `T_LABOR_CONDITIONAL`, with at least one pass threshold missed | either conditional threshold is missed or the migration owner rejects the artifact as unusable |
| Cold ingestion | time ≤ `T_COLD_TIME`; CPU ≤ `T_CPU_PASS`; RAM ≤ `T_RAM_PASS`; storage ≤ `T_STORAGE_PASS` | every measure remains within `T_COLD_TIME_STOP`, `T_CPU_MAX`, `T_RAM_MAX`, and `T_STORAGE_MAX`, with at least one pass threshold missed and a costed remediation | any hard ceiling is exceeded or a partial publication is exposed as complete |
| Incremental freshness | lag ≤ `T_INCREMENTAL_LAG`; backlog ≤ `T_BACKLOG_PASS` | lag ≤ `T_INCREMENTAL_LAG_STOP` and backlog ≤ `T_BACKLOG_MAX`, with demonstrated catch-up and costed remediation | either stop threshold is exceeded or stale output is presented as current |
| Query performance | p95 latency ≤ `T_QUERY_P95` under the frozen workload and output bounds | p95 latency ≤ `T_QUERY_P95_STOP`, with a costed remediation | latency exceeds the stop threshold, resources are exhausted, or authorization is bypassed |
| Recovery | recovery point and time meet `T_RECOVERY_RPO` and `T_RECOVERY_RTO` with no partial set exposed as complete | none | data loss beyond RPO, recovery beyond hard limit, or inconsistent publication |
| Teardown | all required data and credentials destroyed or transferred under written continuation approval | none | teardown cannot be verified |

### Gate values to freeze

| Symbol | Definition | Frozen value |
|---|---|---|
| `T_INTERNAL_PRECISION` | internal call-site precision threshold | `<value and confidence method>` |
| `T_INTERNAL_RECALL` | internal call-site recall threshold | `<value and confidence method>` |
| `T_COVERAGE_STATE_ACCURACY` | accuracy of processing-state assignments | `<percentage and sampling method>` |
| `T_ANALYZED_RATE_PASS` | minimum analyzed share for a pass | `<percentage>` |
| `T_ANALYZED_RATE_CONDITIONAL` | minimum analyzed share for conditional continuation | `<percentage>` |
| `T_INCOMPLETE_RATE_PASS` | maximum partial + failed share for a pass | `<percentage>` |
| `T_INCOMPLETE_RATE_STOP` | maximum partial + failed share for conditional continuation | `<percentage>` |
| `T_EXCLUDED_RATE_PASS` | maximum excluded share for a pass | `<percentage>` |
| `T_EXCLUDED_RATE_STOP` | maximum excluded share for conditional continuation | `<percentage>` |
| `T_TARGET_ACCURACY` | correctness of emitted build-target mappings | `<percentage and confidence method>` |
| `T_TARGET_COVERAGE_PASS` | build-target attribution pass coverage | `<percentage>` |
| `T_TARGET_COVERAGE_CONDITIONAL` | build-target attribution conditional floor | `<percentage>` |
| `T_DEPLOYABLE_ACCURACY` | correctness of emitted deployable mappings | `<percentage and confidence method>` |
| `T_DEPLOYABLE_COVERAGE_PASS` | deployable-attribution pass coverage | `<percentage>` |
| `T_DEPLOYABLE_COVERAGE_CONDITIONAL` | deployable-attribution conditional floor | `<percentage>` |
| `T_DEPLOYABLE_UNRESOLVED_PASS` | maximum unresolved/conflicting deployable-mapping rate for a pass | `<percentage>` |
| `T_DEPLOYABLE_UNRESOLVED_STOP` | maximum unresolved/conflicting deployable-mapping rate for conditional continuation | `<percentage>` |
| `T_SERVICE_ACCURACY` | correctness of emitted canonical-service mappings | `<percentage and confidence method>` |
| `T_SERVICE_COVERAGE_PASS` | canonical-service attribution pass coverage | `<percentage>` |
| `T_SERVICE_COVERAGE_CONDITIONAL` | canonical-service attribution conditional floor | `<percentage>` |
| `T_SERVICE_UNRESOLVED_PASS` | maximum unresolved/conflicting service-mapping rate for a pass | `<percentage>` |
| `T_SERVICE_UNRESOLVED_STOP` | maximum unresolved/conflicting service-mapping rate for conditional continuation | `<percentage>` |
| `T_OWNER_ACCURACY` | correctness against the pinned ownership metadata | `<percentage and confidence method>` |
| `T_OWNER_COVERAGE_PASS` | owner-resolution pass coverage | `<percentage>` |
| `T_OWNER_COVERAGE_CONDITIONAL` | owner-resolution conditional floor | `<percentage>` |
| `T_SERVICE_EDGE_PRECISION` | direct end-to-end service-operation edge precision threshold | `<value and confidence method>` |
| `T_SERVICE_EDGE_RECALL` | direct end-to-end service-operation edge recall threshold | `<value and confidence method>` |
| `T_REVOCATION_SLA` | maximum authorization-revocation propagation time | `<duration>` |
| `T_INITIAL_INVENTORY` | maximum time to first reviewable inventory | `<duration>` |
| `T_INITIAL_INVENTORY_CONDITIONAL` | maximum inventory time for conditional continuation | `<duration>` |
| `T_LABOR_PASS` | required reduction in discovery + triage + routing hours | `<percentage>` |
| `T_LABOR_CONDITIONAL` | conditional labor-reduction floor | `<percentage>` |
| `T_COLD_TIME` | maximum cold clone/index/extraction/publication time | `<duration>` |
| `T_COLD_TIME_STOP` | cold-processing hard time ceiling | `<duration>` |
| `T_CPU_PASS` | CPU pass allocation/consumption limit | `<value>` |
| `T_CPU_MAX` | maximum CPU allocation/consumption | `<value>` |
| `T_RAM_PASS` | memory pass limit | `<value>` |
| `T_RAM_MAX` | maximum memory | `<value>` |
| `T_STORAGE_PASS` | retained-storage pass limit | `<value>` |
| `T_STORAGE_MAX` | maximum retained storage | `<value>` |
| `T_INCREMENTAL_LAG` | maximum commit-to-published-census lag | `<duration>` |
| `T_INCREMENTAL_LAG_STOP` | incremental-lag stop threshold | `<duration>` |
| `T_BACKLOG_PASS` | maximum queued snapshots/work for a pass | `<value>` |
| `T_BACKLOG_MAX` | maximum queued snapshots/work | `<value>` |
| `T_QUERY_P95` | maximum p95 query latency under frozen workload | `<duration>` |
| `T_QUERY_P95_STOP` | p95 query-latency stop threshold | `<duration>` |
| `T_RECOVERY_RPO` | maximum acceptable data loss | `<duration/snapshots>` |
| `T_RECOVERY_RTO` | maximum recovery time | `<duration>` |

Any blank, placeholder, unexplained `N/A`, or internally inconsistent gate
value blocks Gate 0. Values are versioned with this charter and cannot be
relaxed after predictions or gold labels are unsealed. Every conditional
ceiling must be no stricter than its pass ceiling, every conditional floor no
higher than its pass floor, and every hard-stop resource ceiling must remain
within the host's approved safety limit.

### Operational definitions

- **Attribution accuracy** is measured against independently labeled mappings
  among emitted mappings. **Attribution coverage** uses all supported analyzed
  occurrences requiring that hop as its denominator. The unresolved rate uses
  the same declared denominator and is not computed only over successful
  mappings.
- **Processing coverage** uses the independently enumerated eligible unit
  population. Every unit must have one terminal processing state; attribution
  states are reported separately. **Usable processing completion** uses that
  same denominator: analyzed, excluded, partial, and failed counts and rates
  are always reported, and reconciliation alone does not imply successful
  analysis.
- **End-to-end service-operation edge quality** is measured directly against
  an independently constructed service-edge precision frame and
  recall-positive frame. Call-site recall multiplied by attribution coverage
  is diagnostic only and cannot satisfy this gate.
- The current-workflow and phebs workflow use the same RPC, source snapshot,
  authorized universe, and required review outcome.
- The workflow timer starts when the frozen question, source snapshot, and
  required inputs are available. It ends only when the migration owner accepts
  the inventory as reviewable. Rejected output, correction, rerun, and owner
  routing time remain inside the measurement.
- Labor includes pilot-lead, migration-owner, platform-partner, reviewer, and
  remediation time. One-time security/provisioning cost and repeat-query labor
  are reported separately rather than selectively excluded.
- Cold-start time and steady-state query time are separate metrics. A same-day
  repeat query cannot conceal a multi-day unreported ingestion cost.

### Negative-result language

A scoped negative statement is allowed only when the authorized eligible
universe was independently enumerated and every unit reconciles to a terminal
processing state. The statement must include the snapshot, visibility scope,
supported relationship, and known exclusions.

If any eligible unit is partial, failed, or unreconciled, the result must say:

> Analysis is incomplete. No supported facts were found among the analyzed
> units; partial, failed, excluded, and unresolved scope is listed separately.

## 10. Required outputs

The pilot ends with all of the following, even when it stops early:

- frozen analysis manifest and input provenance inventory;
- versioned source call-site/registration inventory with consumer candidates;
- build-target, deployable, service, and recorded-owner attribution report;
- processing-coverage ledger with analyzed, excluded, partial, and failed
  eligible units;
- attribution ledger with resolved, ambiguous, unresolved, and not-applicable
  states at each mapping hop;
- internal shadow-evaluation report and error taxonomy;
- workflow-time and current-practice comparison;
- authorization, revocation, evidence-reproduction, and operating-cost report;
- snapshot-diff prototype or documented reason it could not be produced;
- risk register and recommended remediation, where applicable;
- signed final decision;
- teardown attestation or written continuation/transfer approval.

All outputs distinguish direct source evidence, deterministic derivation,
external observation, metadata assertion, and human disposition.

## 11. Data handling and teardown

- Pilot data remains on approved company infrastructure and is accessible only
  to authorized pilot principals.
- The source mirror, indexes, content-addressed atoms, occurrence
  associations, manifests, logs, proof artifacts, caches, and credentials are
  included in the data inventory.
- Authorization is evaluated on occurrence/snapshot associations and every
  user-facing artifact; globally deduplicated atoms are not independently
  enumerable.
- Revocation, mandatory deletion, or legal policy overrides proof retention.
- Logs and metrics must not expose inaccessible repository, path, service, or
  target existence.
- At stop or pilot completion, the environment owner destroys all pilot data
  and credentials unless a written continuation decision identifies the new
  owner, retention period, access policy, and production-hardening work.
- The preserved-versus-destroyed boundary is decided jointly by the
  environment owner and the Security reviewer against the §10 output list;
  the teardown attestation enumerates every preserved artifact and its
  classification rationale.
- Teardown is verified and recorded; deleting only the source checkout is not
  sufficient.

## 12. Stop conditions

The pilot stops immediately on any of the following:

- a measured external extractor threshold miss, or an external benchmark
  integrity, custody, or reproducibility failure; an accepted pre-score
  capacity disposition under the Section 9 conditional row is not such a
  failure and transfers no accuracy claim;
- confirmed authorization or data-egress failure;
- unresolved IP, OSS, provenance, or policy prohibition;
- inability to construct an independent internal baseline;
- source universe cannot be enumerated sufficiently to support the stated
  claim;
- monorepo ingestion exceeds the approved hard resource ceiling;
- evidence publication corrupts or exposes a partial set as complete;
- reviewer independence or sealed evaluation is compromised;
- sponsor, migration owner, or required platform partner withdraws.

Stopping is an expected valid outcome. The final report records the last safe
stage, committed root cause, preserved non-sensitive measurements, and teardown
status. It does not weaken or remove a gate to manufacture a pass.

## 13. Final decision rubric

| Outcome | Conditions | Authorized next action |
|---|---|---|
| Continue | every safety gate and mandatory product gate passes | propose a separately approved incubation plan with named ownership and hardening work |
| Continue conditionally | every safety gate passes; only preregistered conditional product/operating gates remain | one time-bounded remediation round against unchanged claims; no production dependency |
| Stop | any safety/validation stop condition or material product failure | tear down; preserve only approved reports; no internal dependency |

The decision record states which claims were evaluated and prohibits
generalizing the result to another language, protocol, relationship type,
extractor version, or visibility scope.

## 14. Change control and sign-off

Any material change to the contract, source universe, extractor artifact,
reviewers, sampling design, thresholds, authorization model, or evidence
inputs creates a new charter version and requires reapproval before continuing.
Changes are never applied retroactively to already unsealed results.

| Approval | Name | Date | Charter version |
|---|---|---|---|
| Sponsor |  |  | 0.2 |
| Migration owner |  |  | 0.2 |
| Build/catalog partner |  |  | 0.2 |
| Security |  |  | 0.2 |
| Pilot lead | Ben Meddeb |  | 0.2 |
