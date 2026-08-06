# Protobuf and gRPC evidence-pack cards

Completed instances of the [evidence-pack card template](./EVIDENCE_PACK_CARD.md)
for the Protobuf/gRPC declaration, consumer, exact caller, and field-reference
packs. All four remain **experimental-dark**. T39.4 stopped before unsealing,
so no pack has a sealed statistical validation result, measured workflow
value, or complete target operating envelope. T39.3's security gate and
T39.1's neutral mechanics gate do not substitute for those missing results.

No PackRelease exists. These cards therefore authorize no shadow, advisory,
or released use and do not change the default-off production registration.

## Pack: Protobuf declared contracts

| Field | Value |
|---|---|
| Pack ID | `phebs.protobuf.contract` |
| Status | `experimental-dark` |
| Predicate(s) | Protobuf file/package/message/field/service/operation declarations and explicit extraction gaps |
| Language/framework | `.proto` source accepted by the bounded in-process declaration parser |
| Pack version | `3.0.0` (`proto-contract` extractor) |
| Extractor artifact | in-tree `internal/extract/extractors/protodecl`; the phebs release binary digest governs |
| Schema version | `t17-v1` |
| Release binding | none — no PackRelease exists |
| Owner | Ben Meddeb |
| Independent validation owner | none assigned; release-blocking |
| Validation | not measured under a sealed protocol; T39.4 `not_run` |
| Current decision | dark; no shadow, advisory, or release authority |

**Supported dark claim.** The parser reports declarations present in each
accepted file with exact immutable source evidence and explicit gaps. It does
not establish cross-repository lineage, generated-client use, runtime
registration, source-universe completeness, migration completion, or safety to
remove a contract. One malformed or unsupported declaration input cannot be
reworded as an empty contract.

## Pack: Go gRPC syntactic consumers

| Field | Value |
|---|---|
| Pack ID | `phebs.grpc.consumer.go` |
| Status | `experimental-dark` |
| Predicate(s) | `REGISTERS_GRPC_SERVICE`, `CALLS_OPERATION`, unresolved call/registration, and extraction gaps |
| Language/framework | Go source plus same-repository generated `*_grpc.pb.go` stubs |
| Pack version | `1.2.0` (`grpc-consumer` extractor) |
| Extractor artifact | in-tree `internal/extract/extractors/grpcgo`; the phebs release binary digest governs |
| Schema version | `t13-v1` |
| Release binding | none — no PackRelease exists |
| Owner | Ben Meddeb |
| Independent validation owner | none assigned; release-blocking |
| Validation | not measured under a sealed protocol; T39.4 `not_run` |
| Current decision | dark; no shadow, advisory, or release authority |

**Supported dark claim.** Registration evidence is name-bound to one
same-repository generated stub and call evidence is the conservative
method-name-unique syntactic projection. The tier and exact evidence remain
visible. The pack does not establish generated-symbol provenance, runtime
invocation, deployment, ownership, all callers, or completeness. Ambiguous,
oversized, and unsupported inputs remain unresolved or gaps.

## Pack: Exact Go gRPC callers

| Field | Value |
|---|---|
| Pack ID | `phebs.grpc.caller.go` |
| Status | `experimental-dark` |
| Predicate(s) | declaration-lineage-resolved `CALLS_OPERATION` and explicit unresolved caller evidence |
| Language/framework | Go SCIP occurrences joined to checked-in generated gRPC clients and exact declaration attribution |
| Pack version | `1.5.0` (`grpc-caller` extractor) |
| Extractor artifact | in-tree `internal/extract/extractors/gocaller`; the phebs release binary digest governs |
| Schema version | `t20-caller-v1` |
| Release binding | none — no PackRelease exists |
| Owner | Ben Meddeb |
| Independent validation owner | none assigned; release-blocking |
| Validation | not measured under a sealed protocol; T39.4 call-site and end-to-end gates `not_run` |
| Current decision | dark; no shadow, advisory, or release authority |

**Supported dark claim.** A resolved occurrence requires the SCIP call edge,
generated-client wire operation, and immutable declaration attribution to
agree on exactly one lineage and operation. Name-only, conflicting, dynamic,
missing, or unsupported evidence remains unresolved. Even a resolved static
call is not runtime execution, deployment reachability, current ownership, a
complete caller inventory, migration completion, or decommission authority.

## Pack: Protobuf field references

| Field | Value |
|---|---|
| Pack ID | `phebs.protobuf.field.go` |
| Status | `experimental-dark` |
| Predicate(s) | exact Protobuf field references and explicit SCIP/declaration attribution gaps |
| Language/framework | Go SCIP input joined to generated Protobuf source and declaration lineage |
| Pack version | `1.4.0` (`scip-proto-field` extractor) |
| Extractor artifact | in-tree `internal/extract/extractors/scipfield`; the phebs release binary digest governs |
| Schema version | `t13-v1` |
| Release binding | none — no PackRelease exists |
| Owner | Ben Meddeb |
| Independent validation owner | none assigned; release-blocking |
| Validation | no sealed target-domain accuracy result; T39.4 `not_run` |
| Current decision | dark; no shadow, advisory, or release authority |

**Supported dark claim.** The pack reports a reference only when generated
symbol identity and declaration lineage resolve the exact numbered field.
Uncertain lineage, missing generated inputs, unsupported symbols, and partial
processing remain visible gaps. A field reference does not establish runtime
use, semantic compatibility, migration completion, or safety to remove a
field.

## Shared T39.5 release and lifecycle decision

| Gate | Result | Authority |
|---|---|---|
| Claim/schema mechanics | pass for bounded software mechanics only | T39.1 |
| Authorization/security | pass for the named negative-case matrix only | T39.3 |
| Target operating envelope | stopped on the T39.2 incremental pipeline gate | T39.2 |
| Statistical quality, coverage, attribution, and reproduction | not measured; not a pass | T39.4 |
| Workflow value and owner acceptance | not measured; not a pass | T39.4 |
| Final release decision | **do not release; remain experimental-dark** | T39.5 |

The status is derived, not selected optimistically: required validation and
operating gates are absent or stopped, so `shadow`, `advisory`, and `released`
are ineligible. `suspended` is not the current status because these packs have
never been released.

Default-dark means the production configuration remains off and no ordinary
installation advertises these claims by default. An explicit development
opt-in does not change pack status or create release authority.

If a future PackRelease is ever approved, it suspends automatically on
validation expiry; extractor, dependency, schema, authority, or supported-
construct drift beyond the measured artifact; a confirmed authorization or
evidence-reproduction failure; a quality/coverage/workflow result below its
frozen threshold; or operation outside the measured envelope. Suspension
withdraws new claims and promotions but does not rewrite historical evidence.

Rollback removes or disables the exact PackRelease/opt-in and returns surfaces
to unavailable/default-dark. Historical artifacts retain their original pack,
validation, expiry, and status labels until ordinary authorized retention
removes them; rollback is not deletion and never strengthens an old result.

Revalidation requires a newly approved, digest-frozen Gate-0 package, sealed
independent gold and workflow baseline, a fresh unseen adequately powered
round, a complete target operating-envelope run, current security review, a
named independent validation owner and workflow owner, explicit expiry, and a
signed PackRelease. T39.R1 is additionally mandatory before any T39.2 rerun.
No threshold, scope, language, framework, service population, or workflow may
be widened after results are visible.

This decision creates no “all runtime callers,” numeric accuracy/completeness,
migration-complete, decommission-safe, SLO, or release claim. `GATE2-V2`
remains `NOT_ESTABLISHED`.
