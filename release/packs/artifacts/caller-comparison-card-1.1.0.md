# Claim card: phebs.grpc.caller.comparison 1.1.0 (protobuf caller comparison)

Owner: Ben Meddeb. Distribution: default-on in the release package. Claim
1.1.0 (PHEBS-49, 2026-11-28) inherits the gRPC Caller Map's embedded-descriptor
attribution. This card supersedes `caller-comparison-card-1.0.0.md`, whose
retained qualification artifacts (`qualified-caller-comparison-*-1.0.0`) keep
their bytes and still describe the 1.0.0 measurements.

**Qualification.** Bounded integration tests and the `ci-static`, `ci-go` and
`ci-race` gates on the exact head, under Ben's 2026-11-08 acceptance rule. No
independent blind-label round, precision, recall, attribution accuracy,
completeness, capacity or scale claim is made.

**Scope.** Exact static caller comparison between two protobuf/gRPC
endpoints, each an exact Contract Atlas identity. Each side's evidence is
exactly the gRPC Caller Map's (`phebs.grpc.caller.go` claim 1.1.0): resolved Go
callers related through an authored snapshot or, since claim 1.1.0, through
an embedded descriptor equal to exactly one committed `.proto`, and explicit
abstentions. Callers are classified neutrally as `old_only_evidence`,
`both_evidence`, `new_only_evidence` or `unresolved`, at occurrence or unit
level, with per-side generations, declarations and readable citations. The
surfaces are unchanged: the authenticated `/api/compare_operation_callers`
route, the MCP tool `compare_operation_callers` and the caller comparison
view. Unsupported pairs and unavailable, stale, failed, partial, ambiguous or
overflow inputs keep their refusal or gap states.

**Recipe.** The released gRPC Caller Map recipe (`proto-contract@4.0.0`,
`grpc-caller@1.6.0-released.1`, resolver pack 1.7.0 with
`protoc-gen-go-embedded-descriptor-equal-v1`), served by
`compare_operation_callers@caller-comparison-v2`.

**Non-claims.** Everything the gRPC Caller Map 1.1.0 card excludes. No
runtime use, migration completion, complete inventory or safe-retirement
claim. Empty results never prove caller absence.

**Limits.** The existing serving bounds apply unchanged; no bound is widened.

**Evidence.** `caller-comparison-validation-1.1.0.json` records the commands
and results. The signed release binds the running binary, the recipe
registry and these artifacts.

**Expiry and withdrawal.** The record expires 2027-01-15T23:59:59Z. Removing
the consumed Caller Map withdraws this claim on restart. Revoke the release
ID and restart to withdraw it.
