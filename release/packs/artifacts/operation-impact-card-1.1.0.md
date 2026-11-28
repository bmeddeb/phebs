# Claim card: phebs.contract.impact.operation 1.1.0 (protobuf operation Impact)

Owner: Ben Meddeb. Distribution: default-on in the release package. Claim
1.1.0 (PHEBS-49, 2026-11-28) inherits the gRPC Caller Map's embedded-descriptor
attribution. This card supersedes `operation-impact-card-1.0.0.md`, whose
retained qualification artifacts (`qualified-operation-impact-*-1.0.0`) keep
their bytes and still describe the 1.0.0 measurements.

**Qualification.** Bounded integration tests and the `ci-static`, `ci-go` and
`ci-race` gates on the exact head, under Ben's 2026-11-08 acceptance rule. No
independent blind-label round, precision, recall, attribution accuracy,
completeness, capacity or scale claim is made.

**Scope.** For one authenticated exact protobuf endpoint and explicit source
filters, `operation_callers` returns a bounded page of the gRPC Caller Map's
declaration-bound evidence (`phebs.grpc.caller.go` claim 1.1.0), with exact
source, generation and scope, unchanged row resolution and supplied-unit
uncertainty. `evidence_present`, exact filtered `exact_empty` and
`unavailable` stay distinct. The surfaces are unchanged:
`GET /api/operation_impact_report`, the MCP tool
`build_operation_impact_report` and the operation report view. Field,
proposed-change, implementation, WIRE, matching syntactic-call and saved-report
sections remain unavailable.

**Recipe.** The released gRPC Caller Map recipe (`proto-contract@4.0.0`,
`grpc-caller@1.6.0-released.1`, resolver pack 1.7.0 with
`protoc-gen-go-embedded-descriptor-equal-v1`), served by
`build_operation_impact_report`.

**Non-claims.** Everything the gRPC Caller Map 1.1.0 card excludes. No result
establishes runtime impact, global caller absence, migration completion or
safe retirement.

**Limits.** The existing serving bounds apply unchanged; no bound is widened.

**Evidence.** `operation-impact-validation-1.1.0.json` records the commands
and results. The signed release binds the running binary, the recipe
registry and these artifacts.

**Expiry and withdrawal.** The record expires 2027-01-16T23:59:59Z. Removing
the consumed Caller Map withdraws this claim on restart. Revoke the release
ID and restart to withdraw it.
