# Claim card: phebs.thrift.caller.comparison 1.0.0 (Thrift caller comparison)

Owner: Ben Meddeb. Distribution: default-on in the release package
(PHEBS-44, 2026-11-25).

**Qualification.** Bounded integration tests and the `ci-static`, `ci-go` and
`ci-race` gates on the exact head, under Ben's 2026-11-08 acceptance rule. No
independent blind-label round, precision, recall, attribution accuracy,
completeness, capacity or scale claim is made.

**Scope.** Caller comparison between two Thrift endpoints: an old and a
replacement operation, each an exact Contract Atlas identity (repository,
declaration lineage, operation) with `protocol=thrift`. Each side's evidence is
exactly the Thrift Caller Map's (`phebs.thrift.caller.go`): resolved Go callers
of Apache-Thrift-generated clients and explicit abstentions. The comparison
classifies callers neutrally as `old_only_evidence`, `both_evidence`,
`new_only_evidence` or `unresolved`, at occurrence or unit level, with per-side
generations, declarations and readable citations.

The surfaces are the existing protobuf caller comparison's, for Thrift pairs:

- the authenticated `/api/compare_operation_callers` route;
- the MCP tool `compare_operation_callers`;
- the Caller comparison view;
- the capability `contract-caller-comparison`.

**Implementation.** The exact caller comparison service
(`caller-comparison-v2`) unchanged, bound as
`compare_operation_callers@caller-comparison-v2+thrift-pairs`. The claim
carries no extractor. It is admitted only beside the current released Thrift
Caller Map recipe (`thrift-contract@1.1.2` plus
`thrift-caller@1.6.0-released.1`) as the running extractors; an experimental
Thrift caller generation never backs it.

**Pairs.** Under any released comparison claim a pair is compared only when
both endpoints share a protocol whose own claim is released: Thrift pairs under
this claim, protobuf pairs under `phebs.grpc.caller.comparison`. Mixed protobuf
and Thrift pairs answer 503 under every released claim. This supersedes, for
Thrift pairs only, the "caller comparison stays protobuf-only" line of
`thrift-caller-map-card-1.0.0.md`; that card's claim is otherwise unchanged.

**Non-claims.**

- No mixed protobuf/Thrift (migration) comparison.
- thriftrw, YARPC, Kitex, fbthrift and other generators are not resolved, so
  their callers are absent or unresolved on both sides.
- No name-matching consumer evidence (`thrift-consumer`, T58 NO-GO).
- Operation Impact stays protobuf-only.
- No migration-completeness, runtime-use, deployment or safe-retirement claim.
  `old_only_evidence` does not prove a caller still needs the old operation,
  and an empty side never proves caller absence.

**Limits.** The existing comparison, Caller Map, request and citation bounds
apply unchanged. No ceiling is widened, and no latency, memory, service-count
or repository-size envelope is claimed.

**Evidence.** `thrift-caller-comparison-validation-1.0.0.json` records the
commands and results. The signed release binds the running binary, the claim
registry and these artifacts.

**Expiry and withdrawal.** The record expires 2027-01-12T23:59:59Z, the expiry
of the Thrift Caller Map it reads. Revalidate on comparison, caller,
declaration or resolver algorithm or schema changes, admission, source or
authorization changes, or a supported-bound change. Revoke the release ID and
restart to withdraw it; revoking the Thrift Caller Map also withdraws it. No
experimental switch is needed or used.
