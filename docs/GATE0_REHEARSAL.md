# Gate 0 rehearsal — simulated, not authorization

**Status:** `SIMULATED_UNLOCKED`  
**Real Gate 0 status:** `LOCKED` ([GATE0.md](./GATE0.md) is a synthetic fixture)  
**Authority:** simulation track only

This artifact rehearses the charter §6 ceremony with wholly synthetic data.
No person, organization, repository, host, artifact, signature, or approval
below is real. Values were copied into a synthetic fixture on 2026-07-22. They
were not promoted into evidence or authority and remain unsuitable for any
pilot or external claim.

## Synthetic partner record

| Field | Simulated value |
|---|---|
| Partner organization | Example Partner Platform Group |
| Sponsor / final-decision authority | Sample Sponsor |
| Migration owner / usefulness authority | Sample Migration Owner |
| Build/catalog partner | Sample Build Partner |
| Security reviewer | Sample Security Reviewer |
| OSS/Legal reviewer | Sample Legal Reviewer |
| Pilot environment owner | Sample Environment Owner |
| Independent label reviewers | Sample Reviewer A; Sample Reviewer B |
| Gate 0 signatories | Sample Sponsor; Sample Migration Owner; Sample Security Reviewer; Sample Legal Reviewer; Sample Environment Owner; Sample Reviewer A |
| Receipt reference | `mock-receipt:g0-rehearsal-2026-07-22-01` |
| Receipt status | synthetic; not stored in or issued by an external system |

## Synthetic resource freeze

| Resource | Simulated value |
|---|---|
| Pilot lead | 40% FTE for 6 weeks |
| Migration owner | 20% FTE for 6 weeks |
| Partner contributors | two contributors at 25% FTE for 6 weeks |
| Compute ceiling | 16 vCPU; 64 GiB RAM |
| Storage ceiling | 250 GiB |
| Environment | isolated non-production rehearsal environment |

## Synthetic evaluation freeze

| Input | Simulated value |
|---|---|
| Canonical RPC | `/example.payment.v1.PaymentService/Authorize` |
| IDL snapshot | `example/idl@1111111111111111111111111111111111111111` |
| Source snapshot `S0` | `example/consumer@2222222222222222222222222222222222222222`; tree `sha256:3333333333333333333333333333333333333333333333333333333333333333` |
| Authorized universe | one synthetic Go repository; production source under `cmd/` and `internal/`; one `linux/amd64` build target; generated, test, docs, and tooling scope reported separately |
| Required inventory row identity | `(canonical_rpc, source_repository, source_commit, source_path, start_byte, end_byte, proposed_deployable)` |
| Required inventory fields | row identity; candidate status; proposed service/deployable/owner; evidence references; uncertainty; creator; creation time; supersession reference |
| Reviewability checklist | scope and required fields present; candidates routable; evidence and uncertainty visible; exclusions/unresolved scope visible; artifact usable by migration owner |
| Baseline mode | `historical_reconstruction` |
| Start event | frozen question, `S0`, universe, and all required inputs available |
| End event | first recorded migration-owner acceptance under the frozen checklist |
| Participants | Sample Migration Owner; Sample Build Partner; Sample Pilot Lead |
| Evidence channels | `migration_document`; `ticket`; `owner_outreach`; `build_query`; `traffic_observation` |
| Custody | synthetic immutable object store; synthetic receipt; synthetic access log |
| Correction cutoff | 14 days after first acceptance and no later than the simulated final decision |

## Synthetic thresholds

These values deliberately use the charter’s required types: elapsed-time
durations and labor-reduction percentages.

| Threshold | Simulated value |
|---|---|
| `T_INITIAL_INVENTORY` | 8 hours |
| `T_INITIAL_INVENTORY_CONDITIONAL` | 16 hours |
| `T_LABOR_PASS` | 30% |
| `T_LABOR_CONDITIONAL` | 15% |

## Synthetic remaining Gate 0 inputs

| Requirement | Simulated disposition |
|---|---|
| External extraction | accepted conditional pre-score capacity disposition |
| Required marker | `internal-validation-required` |
| Extractor bridge | synthetic identity/reproducibility/mechanics-only bridge approved; no accuracy transfer |
| Evidence-pack card | synthetic card at `shadow`; internal/domain-shift output required |
| Accuracy protocol | synthetic population, precision and recall-positive frames, direct end-to-end frame, sampling unit, strata, power, custody, and blinded reviewers frozen |
| IP/OSS/provenance | synthetic bounded-evaluation clearance |
| No conflicting dependency | synthetic statement accepted |
| Blank-field check | no blanks in this rehearsal |

## Rehearsal checklist

| Charter §6 requirement | Rehearsal result |
|---|---|
| Roles, time, RPC, and decision authority named | simulated pass |
| IDL identity and source universe confirmed | simulated pass |
| IP/OSS/provenance cleared | simulated pass |
| External-extraction disposition satisfied | simulated conditional pass |
| Pilot artifact identity or bridge approved | simulated pass |
| `internal-validation-required` recorded | simulated pass |
| Evidence-pack card at `shadow` | simulated pass |
| §8.1 and §8.2 protocols preregistered | simulated pass |
| Accuracy frames, power, labels, and custody frozen | simulated pass |
| §9 thresholds filled and signed | simulated pass |
| Every required field filled | simulated pass |
| No conflicting dependency | simulated pass |
| Signatures present | simulated pass |

## Simulated decision

```text
ceremony: Gate 0 rehearsal
rehearsal_status: SIMULATED_UNLOCKED
real_gate_status: LOCKED
copied_to_fixture: docs/GATE0.md
pilot_clock_authority: NONE
source_access_authority: NONE
measurement_authority: NONE
epic_16_authority: OPERATOR_BYPASS_FOR_IMPLEMENTATION_ONLY
authorized_next_action: replace synthetic values with real partner evidence
  before any pilot claim; Epic 16 implementation may proceed under PLAN.md bypass
```

The rehearsal supplied every synthetic value in the [GATE0.md](./GATE0.md)
fixture. Audit state lives in [GATE0_READINESS.md](./GATE0_READINESS.md).
