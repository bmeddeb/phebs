# T21.1 — Change Workbench inventory and vocabulary contract

> **Retained engineering record.** This documents a completed executable design
> contract, not current user behavior or production authorization. See
> [`docs/RETAINED_RECORDS.md`](../../docs/RETAINED_RECORDS.md).

**Date:** 2026-07-26 · **Status:** COMPLETE · **Scope:** executable design
contract only; this package adds no production behavior and production packages
must not import it.

The contract freezes the add, modify, migrate, and retire journeys as four
Why → What → Where → How stories against the neutral
`example.invalid/marketplace` fixture. Each step inventories its inputs, shared
service calls, outputs, mutations, evidence sources, bounds, unsupported
planes, and required human decisions. Source anchors keep the twelve-service
inventory mechanically attached to the current implementation.

The sequence is confirmed:

- T21.1–T21.5 remain independent of Epic 20.
- T21.6 waits for T20.10 exact-identity reads.
- T21.8 inherits T20.10 through T21.6.
- T21.7 and the dependent impact/checklist surfaces wait for T20.13.
- T21.14 implementation closure waits for T20.14.
- Every production registration and use still requires both `ESTABLISHED` and
  an explicit pilot-continuation decision.

## Frozen inputs

| Input | Canonical digest |
|---|---|
| `../../internal/glossary/glossary.json` | `sha256:ce13b715607141f2833c8ade57b8aa552d89b68475c5884257972a7defcb3274` |
| `scenarios.json` | `sha256:922034e9f9a3cb40ff0d602b27a0245795a45949b2e012c6f8d7f75f145120f4` |

T21.4 promoted `glossary.json` without changing its canonical bytes or digest;
it now lives under `internal/glossary` as the canonical versioned input for
eight initial user terms. It records stable ids, short and expanded help,
evidence and authority boundaries, applicable modes and surfaces, wire
aliases, and capability predicates. The binding terminology trace is:

- `known_consumers` → **Matching static evidence**
- `unresolved_candidates` → **Could not resolve**
- `coverage-certificate-v1` / `coverage` → **Analysis scope & gaps**, with
  **Coverage certificate** retained as the advanced deterministic receipt

`contract.go` rejects unknown fields, unsafe or oversized UTF-8, duplicate
identities and sets, invalid capabilities, incomplete scenario rows, unknown
service calls, and weakened registration gates. `projection.go` retains the
original deterministic non-production previews. `internal/glossary` now owns
the production Go, TypeScript, schema, MANUAL, and MCP projections plus the
repository-wide drift guard.

Run the offline gates with:

```sh
go test ./spike/t211
```

The tests pin both digests, canonicalize semantically equal ordering, validate
all four projections, resolve every source anchor, enforce the dependency and
landing matrix, and exercise fail-closed input cases. They perform no network
access.
