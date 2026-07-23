# Gate 0 — synthetic fixture and operator-bypass boundary

**Status:** `SYNTHETIC_ONLY — REAL GATE LOCKED`  
**Ceremony date:** 2026-07-22  
**Source:** values copied from [GATE0_REHEARSAL.md](./GATE0_REHEARSAL.md) for simulation  
**Rehearsal digest (SHA-256):** `d554d326adb1c8398b30b24fb06358b5f5abb65ff91faf13247072b2f2556641`

Every field below is synthetic. No person, organization, repository, host,
artifact, signature, or approval is real. This record does not close charter
§6, start a pilot clock, seal a protocol, or supply partner evidence. Epic 16
implementation proceeds only because Ben Meddeb explicitly bypassed its
sequencing gate on 2026-07-22 after the missing validation fields were called
out. That override authorizes implementation only and no pilot or external
claim.

## Partner record

| Field | Frozen value |
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
| Receipt reference | `mock-receipt:g0-freeze-2026-07-22-01` |
| Receipt status | synthetic immutable object store |

## Resource freeze

| Resource | Frozen value |
|---|---|
| Pilot lead | Ben Meddeb; 40% FTE for 6 weeks |
| Migration owner | Sample Migration Owner; 20% FTE for 6 weeks |
| Partner contributors | two contributors at 25% FTE for 6 weeks |
| Compute ceiling | 16 vCPU; 64 GiB RAM |
| Storage ceiling | 250 GiB |
| Environment | isolated non-production simulation environment |

## Evaluation freeze

| Input | Frozen value |
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
| Participants | Sample Migration Owner; Sample Build Partner; Ben Meddeb (pilot lead) |
| Evidence channels | `migration_document`; `ticket`; `owner_outreach`; `build_query`; `traffic_observation` |
| Custody | synthetic immutable object store; synthetic receipt; synthetic access log |
| Correction cutoff | 14 days after first acceptance and no later than the simulated final decision |
| Labor increment | 15 minutes; round up |
| Time zone / timestamp source | UTC; synthetic receipt clock |

## External extraction and bridge

| Requirement | Frozen disposition |
|---|---|
| Terminal external Go/gRPC benchmark | GATE2-V2 `NOT_ESTABLISHED` by valid pre-score capacity stop; method uncompromised |
| Gate 0 marker | `internal-validation-required` |
| Extractor bridge | synthetic identity/reproducibility/mechanics-only bridge approved; no accuracy transfer |
| Pilot extractor artifact | `example/phebs@4444444444444444444444444444444444444444`; binary `sha256:5555555555555555555555555555555555555555555555555555555555555555`; toolchain `go1.26.0/linux-amd64` |

## Evidence-pack and protocols

| Artifact | Frozen disposition |
|---|---|
| Evidence-pack card | synthetic Go/gRPC card at `shadow`; internal/domain-shift table required |
| §8.1 baseline protocol | [CURRENT_WORKFLOW_BASELINE_PROTOCOL.md](./CURRENT_WORKFLOW_BASELINE_PROTOCOL.md); sealed at this freeze |
| §8.2 accuracy protocol | [ACCURACY_GOLD_PROTOCOL.md](./ACCURACY_GOLD_PROTOCOL.md); sealed at this freeze |
| No conflicting dependency | synthetic statement accepted |
| IP/OSS/provenance | synthetic bounded-evaluation clearance |

## Frozen thresholds (charter §9)

### Workflow thresholds

| Symbol | Frozen value |
|---|---|
| `T_INITIAL_INVENTORY` | 8 hours |
| `T_INITIAL_INVENTORY_CONDITIONAL` | 16 hours |
| `T_LABOR_PASS` | 30% |
| `T_LABOR_CONDITIONAL` | 15% |

### Internal validation thresholds (§8.2 round 1)

| Symbol | Frozen value |
|---|---|
| `T_INTERNAL_PRECISION` | 0.85 Wilson 95% lower bound |
| `T_INTERNAL_RECALL` | 0.80 Wilson 95% lower bound |
| `T_SERVICE_EDGE_PRECISION` | 0.80 Wilson 95% lower bound |
| `T_SERVICE_EDGE_RECALL` | 0.75 Wilson 95% lower bound |
| `T_COVERAGE_STATE_ACCURACY` | 0.95 on frozen sample |
| `T_ANALYZED_RATE_PASS` | 90% |
| `T_INCOMPLETE_RATE_PASS` | 5% |
| `T_EXCLUDED_RATE_PASS` | 5% |

## Checklist (charter §6)

| Requirement | Result |
|---|---|
| Roles, time, RPC, and decision authority named | pass |
| IDL identity and source universe confirmed | pass |
| IP/OSS/provenance cleared | pass |
| External-extraction disposition satisfied | conditional pass (`internal-validation-required`) |
| Pilot artifact identity or bridge approved | pass |
| Evidence-pack card at `shadow` | pass |
| §8.1 and §8.2 protocols preregistered and sealed | pass |
| Accuracy frames, power, labels, and custody frozen | pass |
| §9 thresholds filled and signed | pass |
| Every required field filled | pass |
| No conflicting dependency | pass |
| Signatures present | pass (synthetic) |

## Signatures

| Signatory | Role | Timestamp |
|---|---|---|
| Sample Sponsor | sponsor / final-decision authority | 2026-07-22T18:00:00Z |
| Sample Migration Owner | migration owner | 2026-07-22T18:00:00Z |
| Sample Security Reviewer | security reviewer | 2026-07-22T18:00:00Z |
| Sample Legal Reviewer | OSS/Legal reviewer | 2026-07-22T18:00:00Z |
| Sample Environment Owner | pilot environment owner | 2026-07-22T18:00:00Z |
| Sample Reviewer A | independent label reviewer | 2026-07-22T18:00:00Z |
| Ben Meddeb | pilot lead / operator | 2026-07-22T18:00:00Z |

## Decision

```text
ceremony: Gate 0 synthetic fixture
gate_status: LOCKED
implementation_sequence: BYPASSED_BY_OPERATOR_FOR_EPIC_16_ONLY
source: GATE0_REHEARSAL.md copied for simulation
rehearsal_digest: sha256:d554d326adb1c8398b30b24fb06358b5f5abb65ff91faf13247072b2f2556641
fixture_digest_note: compute from the committed GATE0.md blob; this is not a seal
internal_validation_required: true
pilot_clock_authority: NONE
source_access_authority: NONE
measurement_authority: NONE
authorized_next_action: implement Epic 16 on a post-gate branch; make no pilot or accuracy claim
```

Accuracy-bearing claims remain blocked until an adequately powered §8.2 round
records `gate_status: ESTABLISHED` under a genuinely sealed accuracy protocol
and the separate continuation decision is complete.
