# Claim card: phebs.grpc.caller.go 1.1.0 (gRPC Caller Map)

Owner: Ben Meddeb. Distribution: default-on in the release package. Claim
1.1.0 (PHEBS-49, 2026-11-28) also relates protoc-gen-go-grpc clients to their
committed `.proto` through the descriptor protoc-gen-go embeds, with no
authored snapshot. This card supersedes `caller-map-card-1.0.0.md`, whose
retained qualification artifacts (`qualified-caller-map-*-1.0.0`) keep their
bytes and still describe the 1.0.0 measurements.

**Qualification.** Bounded integration tests and the `ci-static`, `ci-go` and
`ci-race` gates on the exact head, under Ben's 2026-11-08 acceptance rule. No
independent blind-label round, precision, recall, attribution accuracy,
completeness, capacity or scale claim is made.

**Scope.** Go callers of generated gRPC clients in the same repository as the
`.proto` declaration they were generated from. A resolved row needs a call to
one generated client method, one wire operation and one declaration lineage,
related in one of two ways:

- *Authored snapshot (claim 1.0.0).* Exactly one direct `generated_path` →
  `declaration_path` mapping (or generator invocation) for the generated file
  in the repository-root `generated-from-snapshot.json`. An explicit mapping
  or invocation root always governs the file.
- *Embedded descriptor (claim 1.1.0, PHEBS-49, Ben's option A).* For a client
  file no snapshot governs, all of the following:
  - the client file is protoc-gen-go-grpc (or legacy `plugins=grpc`) output
    with exactly one `// source: S` line;
  - its directory holds exactly one protoc-gen-go descriptor literal
    (`file_*_rawDesc`, or the legacy gzipped `fileDescriptor_*`) named S in
    the same package clause, and that descriptor declares every operation
    the client file calls;
  - exactly one `proto-contract`-declared `.proto` at S or ending in `/S`
    parses, without linking, to an equivalent descriptor: package, syntax or
    edition, imports, `go_package`, messages (fields by name, number, label,
    proto3 `optional`, scalar type or scoped type name; oneofs; nested
    enums), enums, and services with methods, request and response types and
    streaming flags;
  - that declaration has one lineage.

  Stale generated code (built from a `.proto` that has since changed),
  several equivalent committed copies, or no committed `.proto` is an
  explicit abstention, never a resolved caller. phebs never infers the
  relation from names, paths or directories.

Ambiguous, unsupported and unavailable inputs keep explicit gaps. The surfaces
are unchanged: the authenticated exact Caller Map, progress and citation
routes, the MCP tools `list_operation_callers` and
`read_operation_caller_citation`, the Caller Map view from a protobuf
operation in Contract Atlas, and the capability `contract-caller-map`.

**Recipe.** `proto-contract@4.0.0` with `grpc-caller@1.6.0-released.1`. The
caller algorithm is `grpc-caller` 1.6.0 unchanged. It resolves through the
shared resolver catalog at resolver pack version 1.7.0, whose materialization
policy names the descriptor rule
`protoc-gen-go-embedded-descriptor-equal-v1` (PHEBS-49).

**Non-claims.**

- No binding for generated code whose `.proto` is not committed, for stale
  generated code, or for YARPC, Connect or other non-gRPC protobuf clients.
- gRPC calls through struct fields declared in another file are not
  followed.
- No name-matching consumer evidence (T58 NO-GO).
- Caller comparison and operation Impact are their own claims
  (`phebs.grpc.caller.comparison`, `phebs.contract.impact.operation`).
- No runtime execution, deployment, complete inventory or safe-retirement
  claim. Empty results never prove caller absence.

**Limits.** At most 4,096 `*.pb.go` candidates per catalog, each within the
existing 4 MiB generated-source bound and the existing input blob budget; a
decoded descriptor is at most 4 MiB; declared `.proto` files pass the
existing protobuf preflight. Over the candidate bound one unsupported row is
written and nothing is bound. All other candidate, publication, reader,
request and citation bounds are unchanged, and no latency, memory,
service-count or repository-size envelope is claimed.

**Evidence.** `caller-map-validation-1.1.0.json` records the commands and
results. The signed release binds the running binary, the recipe registry
and these artifacts.

**Expiry and withdrawal.** The record expires 2027-01-15T23:59:59Z. Revalidate
on caller, declaration or resolver algorithm or schema changes,
admission/source/authorization changes, or a supported-bound change. Revoke
the release ID and restart to withdraw it; no experimental switch is needed
or used.
