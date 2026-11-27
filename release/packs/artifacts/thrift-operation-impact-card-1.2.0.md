# Claim card: phebs.thrift.impact.operation 1.2.0 (Thrift operation Impact)

Owner: Ben Meddeb. Distribution: default-on in the release package
(PHEBS-45, 2026-11-26; claim 1.1.0 inherits thriftrw/YARPC callers, PHEBS-46,
2026-11-26). Claim 1.2.0
(PHEBS-47, 2026-11-27) reads declarations from thriftrw's embedded IDL too. This card
supersedes `thrift-operation-impact-card-1.1.0.md`.

**Qualification.** Bounded integration tests and the `ci-static`, `ci-go` and
`ci-race` gates on the exact head, under Ben's 2026-11-08 acceptance rule. No
independent blind-label round, precision, recall, attribution accuracy,
completeness, capacity or scale claim is made.

**Scope.** The operation Impact report (`contract-impact-operation-v1`, shape
`operation_callers`) for one exact Thrift endpoint: a Contract Atlas identity
(repository, declaration lineage, operation) with `protocol=thrift`. The report
is one unmodified exact Thrift Caller Map page (`phebs.thrift.caller.go`):
resolved Go callers of Apache-Thrift-generated and thriftrw/YARPC clients
(claim 1.1.0 of `phebs.thrift.caller.go`) and explicit abstentions, with
readable citations. Its conclusion is `evidence_present`,
`exact_empty` (no matching static evidence in the exact filtered scope, never
an absence claim) or `unavailable`.

The surfaces are the protobuf operation report's, for Thrift endpoints:

- the authenticated `/api/operation_impact_report` route;
- the MCP tool `build_operation_impact_report`;
- the operation Impact view, reached from a Thrift operation in Contract Atlas
  or Caller Map;
- the capability `contract-impact-operation`.

**Implementation.** The operation report service unchanged, bound as
`build_operation_impact_report@contract-impact-operation-v1+thrift`. The claim
carries no extractor. It is admitted only beside the current released Thrift
Caller Map recipe (`thrift-contract@1.2.0` plus
`thrift-caller@1.6.0-released.1`) as the running extractors; an experimental
Thrift caller generation never backs it.

**Endpoints.** Each protocol is served only under its own claim: Thrift
endpoints under this claim, protobuf endpoints under
`phebs.contract.impact.operation`. A protocol whose claim is not released is
refused with 400 before any caller read. This supersedes, for Thrift endpoints
only, the "operation Impact stays protobuf-only" lines of
`thrift-caller-map-card-1.0.0.md` and `thrift-caller-comparison-card-1.0.0.md`;
those cards' claims are otherwise unchanged.

**Non-claims.**

- Field references, proposed change, implementations, WIRE verdict, matching
  syntactic calls and saved reports stay `not_in_supported_shape`.
- Kitex, fbthrift and other generators, and thriftrw/YARPC clients whose committed
  `.thrift` has a different SHA-1, are not resolved, so their callers
  are absent or unresolved.
- No name-matching consumer evidence (`thrift-consumer`, T58 NO-GO).
- No runtime impact, deployment, completeness or safe-retirement claim; an
  `exact_empty` report never proves caller absence.

**Limits.** The existing operation report, Caller Map, request and citation
bounds apply unchanged. No ceiling is widened, and no latency, memory,
service-count or repository-size envelope is claimed.

**Evidence.** `thrift-operation-impact-validation-1.2.0.json` records the
commands and results. The signed release binds the running binary, the claim
registry and these artifacts.

**Expiry and withdrawal.** The record expires 2027-01-12T23:59:59Z, the expiry
of the Thrift Caller Map it reads. Revalidate on report, caller, declaration or
resolver algorithm or schema changes, admission, source or authorization
changes, or a supported-bound change. Revoke the release ID and restart to
withdraw it; revoking the Thrift Caller Map also withdraws it. No experimental
switch is needed or used.
