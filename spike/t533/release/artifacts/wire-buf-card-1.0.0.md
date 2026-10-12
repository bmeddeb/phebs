# Claim card: phebs.protobuf.wire.buf 1.0.0

**Claim.** For a supported input, `check_wire_compatibility` (HTTP `POST
/api/check_wire_compatibility`, the MCP tool of the same name, and capability
`wire-compatibility`) never reports a change that breaks protobuf wire
compatibility as compatible.

A "breaking" verdict may be conservative in two disclosed classes:

1. string, bytes or message singular↔repeated
   (`FIELD_WIRE_COMPATIBLE_CARDINALITY`);
2. a field whose message type changes name while keeping an identical wire
   layout (`FIELD_WIRE_COMPATIBLE_TYPE`).

Outside those classes, conservative errors are at most 1%. The engine is
pinned Buf 1.72.0 with the `WIRE` policy, and the verdict covers exactly the
supplied bytes.

**Known undisclosed conservative errors.** Each is under 1% and appears in
the validation. All come from Buf's `FIELD_WIRE_COMPATIBLE_TYPE`:

- enum renames;
- `bool` → enum;
- bytes → message;
- a renamed message that only adds fields.

**Non-claims.**
- Runtime, deployment, application or semantic compatibility.
- JSON and text formats.
- Thrift.
- Imports the caller did not supply.
- Consumer impact: no field-reference evidence is joined.
- Completeness of any contract history.

**Supported input.** A compilable closure:

- at most 256 files per side, 4 MiB per file and 32 MiB per side;
- the preflight token and depth limits apply;
- a source that does not compile is refused (HTTP 400), never judged.

**Operating envelope (this host, 12 CPUs and 16 GB).**

- At most 4 concurrent sandboxed Buf children. Each runs under bubblewrap
  with a 512 MiB data limit and a 10 s CPU limit.
- A check waits up to 5 s for a slot, then is refused as retryable (HTTP 503).
- Measured over 235 real units:

| Clients | Checks/s | p95 | Peak server-tree RSS |
|---|---|---|---|
| 1 | 7.2 | 0.25 s | 318 MiB |
| 4 | 18.7 | 0.39 s | 559 MiB |
| 8 | 19.1 | 0.65 s | 536 MiB |

  No refusals at any level.
- Stateless: nothing is persisted, so backup, restore and generation gates
  do not apply.

**Authorization.** Any authenticated principal may call it. It reads no
repository, store or proof bundle, so a caller sees only what its own request
supplied. Anonymous and wrong-token requests are refused (401).

**Owner.** Ben Meddeb, with Claude as lead implementer.

**Reviewer.** Claude was the sole adjudicator Ben named. This is not an
independent human review.

**Expiry and revalidation.** The release expires 90 days after approval
(2027-01-10). It must be revalidated on any change to the pinned Buf version,
the `WIRE` policy, `internal/compat` verdict code, or the input limits.

**Suspension.** Add the release_id to `release_selection.revoked` and
restart. The surface is withdrawn and the startup log names it.
