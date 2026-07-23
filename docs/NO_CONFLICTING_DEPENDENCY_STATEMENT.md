# Gate 0 no-conflicting-dependency statement — draft

*Owner worksheet for the final bullet of
[PILOT_CHARTER.md](./PILOT_CHARTER.md) Gate 0. Drafting this statement records
no clearance, resource commitment, partner agreement, environment authority,
source access, or Gate 0 decision. The completed statement is frozen and
signed only against the then-current pilot schedule and dependency estate.*

## 1. Decision requested

Gate 0 signatories decide whether the proposed six-week, read-only, advisory
pilot creates or relies on a conflicting pilot or production dependency.
Exactly one disposition is selected:

- `CLEAR` — no conflict exists;
- `CLEAR_WITH_CONDITIONS` — every condition has an owner, deadline, and
  verified non-interference boundary before the pilot clock starts;
- `CONFLICT` — the pilot cannot start under the proposed schedule or shape.

An unassigned condition, undocumented dependency, unresolved scheduling
collision, or inability to verify non-interference is `CONFLICT`, not an
implicit conditional clearance.

## 2. Record status

| Field | Value |
|---|---|
| Schema | `gate0-no-conflicting-dependency-v1-draft` |
| Owner | Ben Meddeb |
| Charter | v0.2 |
| Proposed RPC / `S0` / universe | `<Gate 0>` |
| Proposed pilot dates | `<Gate 0>` |
| Reviewers | sponsor, migration owner, build/catalog partner, Security, environment owner |
| Disposition | `<Gate 0: CLEAR | CLEAR_WITH_CONDITIONS | CONFLICT>` |
| Freeze digest | `<Gate 0>` |
| Authority before signature | none |

## 3. Non-dependency boundary

The completed record must confirm each statement below or name a blocking
exception.

1. No production service, deployment, build, release, incident response,
   migration milestone, API removal, or customer workflow depends on phebs or
   the pilot completing or remaining available.
2. No CI gate, required PR check, code-host write, deprecation decision, or
   migration approval consumes pilot output.
3. Pilot findings are advisory candidate evidence. Existing production and
   migration authorities remain authoritative throughout the pilot.
4. The pilot uses an isolated, approved environment and least-privilege,
   read-only source and metadata identities; it requires no production write
   credential or production topology change.
5. Pilot retention, proof pins, and reports never override revocation,
   mandatory deletion, legal policy, confidentiality, or teardown.
6. The current-workflow baseline, accuracy baseline, and partner catalogs do
   not depend on unsealed phebs predictions for their construction.
7. A stop, delay, teardown, or unfavorable result leaves every production and
   migration workflow able to continue without phebs.

The pilot may consume sanctioned people, host capacity, and read-only inputs;
those are declared resources, not production dependencies. They become a
conflict when their allocation displaces a committed production/pilot duty,
breaks an approval boundary, or cannot be withdrawn safely.

## 4. Resource and schedule inventory

The Owner obtains a dated answer and evidence reference for every row.

| Dependency / resource | Provider / owner | Required window and quantity | Existing commitment or freeze | Non-interference evidence | Result |
|---|---|---|---|---|---|
| Pilot-lead time | Ben Meddeb + sponsor | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<clear / condition / conflict>` |
| Migration-owner and reviewer time | migration owner | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |
| Build/catalog partner time | build/catalog partner | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |
| Independent label-review capacity | reviewer A + reviewer B | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |
| Security / OSS / Legal review | named reviewers | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |
| Isolated host and storage | environment owner | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |
| Read-only source access | source owner | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |
| Build/catalog/deployment inputs | metadata owners | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |
| OIDC / secrets / audit facilities | security + environment owners | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |
| Backup, restore, and teardown witness | environment owner + witness | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |
| Migration calendar / code freezes | migration owner | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |
| Other pilots or platform changes | sponsor + platform owners | `<Gate 0>` | `<Gate 0>` | `<Gate 0>` | `<...>` |

`None` is admissible only with a named authority and explanation. An
unanswered row blocks `CLEAR` and `CLEAR_WITH_CONDITIONS`.

## 5. Circularity and interference checks

Each check records `yes | no`, evidence, and reviewer.

| Check | Required answer |
|---|---|
| Does any existing production or migration decision require a favorable pilot result? | `no` |
| Must any production component change configuration, credentials, traffic, or deployment state for the pilot? | `no` |
| Does current-workflow or gold-label construction consume unsealed phebs predictions? | `no` |
| Does the partner need phebs output to enumerate the universe or supply the catalogs used to evaluate phebs? | `no` |
| Would stopping phebs prevent the migration team from using its current workflow? | `no` |
| Does pilot scheduling displace a committed release, incident, compliance, security, or migration duty? | `no`, or a resolved condition |
| Can every retained source-derived artifact be destroyed or transferred under the charter without harming production? | `yes` |
| Can access be revoked immediately without a production outage or correctness impact? | `yes` |
| Is every shared input versioned so later provider changes cannot silently alter the frozen comparison? | `yes` |

Any required-answer miss is a conflict unless the charter already defines a
bounded pre-start condition that fully removes it. Sponsor enthusiasm cannot
waive security, custody, provenance, deletion, or independence failures.

## 6. Conditions ledger

Used only for `CLEAR_WITH_CONDITIONS`.

| Condition ID | Conflict removed | Owner | Due before | Verification evidence | Status |
|---|---|---|---|---|---|
| `<id>` | `<specific conflict>` | `<name>` | `<RFC3339>` | `<immutable reference>` | `<open / verified>` |

Every condition must be `verified` before Gate 0 signatures. A condition that
can be satisfied only after the pilot begins is a conflict and requires a
revised schedule or charter.

## 7. Freshness and change control

The signed statement binds the exact charter version, schedule, roles,
resource inventory, RPC, `S0` plan, and dependency evidence. The Owner rechecks
it immediately before Gate 0 signature and again before the first retained
source clone. A material schedule, staffing, environment, authorization,
migration, or platform change voids `CLEAR` and requires a new version and
signatures. Weekly status reports surface newly discovered conflicts; the
pilot pauses rather than inheriting a production dependency silently.

## 8. Gate 0 decision record

| Field | Value |
|---|---|
| Disposition | `<CLEAR | CLEAR_WITH_CONDITIONS | CONFLICT>` |
| Conditions | `<verified IDs or none>` |
| Unresolved conflicts | `<list or none>` |
| Evidence-manifest digest | `<sha256>` |
| Statement digest | `<sha256>` |
| Timestamp | `<RFC3339>` |
| Sponsor | `<name / signature>` |
| Migration owner | `<name / signature>` |
| Build/catalog partner | `<name / signature>` |
| Security reviewer | `<name / signature>` |
| Environment owner | `<name / signature>` |
| Pilot lead | `Ben Meddeb / <signature>` |

Only `CLEAR`, or `CLEAR_WITH_CONDITIONS` with every condition already verified,
satisfies the charter bullet. This record satisfies no other Gate 0
requirement and cannot start the pilot by itself.
