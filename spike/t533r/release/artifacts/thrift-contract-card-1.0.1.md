# Claim card: phebs.thrift.contract 1.0.1 (Thrift declaration identity)

This version re-signs the unchanged 1.0.0 claim into the five-release selection for binary
`2467bd17` (Thrift declarations, Kafka producer and consumer, Topics, and Buf WIRE). Its
quality and served-path evidence are the 1.0.0 record's, on the same binary.

**Claim.** For every `.thrift` file the Apache Thrift reference compiler
accepts, the `thrift-contract` extractor (`thriftdecl` 1.1.2) declares exactly
its services, operations, messages (including the synthetic `_args` and
`_result` structs) and fields, under the T19.1 identities. Field IDs are the
wire IDs. Each field's requiredness and same-file type reference match the
compiler's.

This release serves those declarations through Contract Atlas: the HTTP
routes `GET /api/contract_atlas` and `GET /api/contract_atlas/operation`, the
`/contracts` page, and the `contract-atlas` capability. Coverage names
`thrift-contract` alone. An operation lists implementations, callers and
unresolved candidates as `unavailable_relationships`, never as empty lists.

**Non-claims.**
- No include resolution: include-qualified references stay unresolved.
- Enums, constants and annotations are not declarations.
- Implicit or nonpositive field IDs gap the file, because Apache
  auto-assigns them.
- A file thriftrw cannot parse is one named `parse_error` gap. Its known
  causes are a keyword used as an identifier, or a namespace annotation.
- Containers nested more than one level deep abstain.
- No Thrift consumer, caller, implementation or field-reference evidence.
- No protobuf (Epic 49, T49.1b).
- No Atlas MCP tools: they register only beside Caller Map (T20.11).
- No fbthrift dialect.

**Quality.** T49.2 (`docs/THRIFT_DECLARATION_QUALITY_PROTOCOL.md`), against
the pinned Apache Thrift 0.19.0 compiler, which is a complete oracle:
- **Round 1** failed. It found the defects fixed in 1.1.0.
- **Round 2** passed: 1,353 declarations at 100%, field IDs 100%, reference
  state 99.86%.
- **Round 3** (fresh, 1,673 declarations) passed after adjudication:
  declarations 100%, field IDs 100%, reference state 99.86%.
- The one false shape claim round 3 found is fixed in 1.1.2.

**Served path.** In every lifecycle phase, the release binary's Atlas equalled
the oracle's 33 service and 120 operation declarations across the round-3
population.

**Operating envelope (this host: 12 CPUs, 16 GB; 10 T49.2 round-3 repositories).**

| Measure | Value |
|---|---|
| Cold start to Atlas equal to the oracle | 75 s |
| Restart, or recovery after SIGKILL, to equal | 1.4 s |
| Online backup | 222 MiB |
| Restore | 23 s |
| Data dir | 529 MiB |
| Whole Atlas listing p95 (every page, 153 items) | 988 ms |
| Peak session RSS | 771 MiB |

**Authorization.** Anonymous requests are refused (401). A member's Atlas
covers only its visible repositories, and a hidden repository's operation
answers 404.

**Owner.** Ben Meddeb, with Claude as lead implementer.

**Reviewer and adjudicator.** Claude, as named by Ben. This is not an
independent human review.

**Expiry and revalidation.** The release expires 2027-01-12. Revalidate it on
any `thriftdecl` change, any change to the Atlas schema or relationship
scoping, or any thriftrw upgrade.

**Suspension.** Add the release_id to `release_selection.revoked` and
restart. That withdraws Atlas, and the Kafka planes and Topics keep serving.
