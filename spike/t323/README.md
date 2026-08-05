# T32.3 — Neutral service-authority and correctness corpus

> **Retained engineering record.** This directory contains a deterministic,
> public, independently-oracled fixture for later multi-service design and
> topology work. It is not current user guidance, a production catalog schema,
> a topology choice, or a scale claim. Current sequencing remains in
> [`docs/ROADMAP.md`](../../docs/ROADMAP.md).

**Completed:** 2026-08-04 · **Status:** COMPLETE · **Production changes:** none

T32.3 supplies the correctness and cardinality inputs required before T32.4
may compare whole-repository search with any bounded cohort design. It consumes
no phebs output when stating expected membership, search results,
relationships, currentness, or tombstones.

## Retained artifacts

- [`t323-neutral-corpus.bundle`](./t323-neutral-corpus.bundle) is a five-commit
  neutral Git history.
- [`catalogs/`](./catalogs/) contains the exact closed authority input at each
  logical revision. The same bytes are committed inside each Git tree at
  `.phebs/service-authority.json`.
- [`oracles/`](./oracles/) independently records exact membership, unowned
  paths, authority conflict, all-code/service query results, RPC/topic
  relationships, per-service currentness, restricted-scope behavior, and
  tombstones.
- [`profiles/services-1000.json`](./profiles/services-1000.json) and
  [`profiles/services-5000.json`](./profiles/services-5000.json) are complete
  generated catalog/file inventories for later catalog, scheduler, reader,
  descriptor, memory, disk, no-op, update, and GC measurements.
- [`receipt.json`](./receipt.json) pins every commit, Git tree, artifact size,
  and SHA-256 digest.

The `t323-neutral-authority-input-v1` shape is fixture vocabulary, not the
future T33 production contract. It deliberately keeps accepted authority,
proposal, conflict, malformed input, and tombstone states distinct so a later
writer cannot make a proposal or tie silently authoritative.

## Small corpus history

| Revision | Commit | Case frozen by the oracle |
|---|---|---|
| `r0-baseline` | `608f25174467e10db77a60f911ec545739a76b7c` | distinct services, central protobuf contract, generated Go, valid deterministic SCIP input, shared library, many-to-many membership, unowned source, restricted scope, proposal, conflicting authorities, and unsafe malformed authority |
| `r1-rename` | `3f8eae89e178cfaa595526ecd0b33aea6ea3a966` | `svc.billing` keeps stable identity while its primary placement and display name change |
| `r2-split` | `382ffc83ba625b1c0464269f6d7f252db4991942` | the prior orders identity becomes a split tombstone with API and worker successors |
| `r3-merge-partial` | `f50bb9c0e3bdcfbc7c004141d837a2a784744b2f` | worker and shipping identities merge into fulfillment; another service remains explicitly stale and fulfillment relationship publication is partial |
| `r4-removal` | `4ac5335893fc18a1243b60a005faa1f09268d858` | the renamed billing placement is deleted, its stable identity remains a removal tombstone, one RPC consumer becomes unresolved, and restricted service search is unavailable |

Each revision uses 14–15 regular files. Primary, supporting, shared, generated,
and typed memberships are explicit; the four unowned/proposal/conflict files
remain searchable by All code without becoming accepted service membership.
The shared source, central contract, generated client, and typed input each
belong to more than one logical service without being duplicated in the Git
tree.

The query oracle uses literal sentinels and exact ordered paths. Tests rescan
the authored bytes independently and prove that every exact or stale result
set is complete for its All code or service membership projection. The
standalone `ValidateSnapshot` check proves closed shape, membership binding,
and presence of every listed literal; completeness and set equality live in
the retained `assertExactQueries` test because that check rescans the authored
corpus. The relationship oracle separately fixes the RPC
`/neutral.commerce.v1.Orders/Create` and Kafka topic `orders.created.v1`, their
provider/consumer identities, evidence paths, and resolved or unresolved
state. A partial publication state never changes the expected complete
relationship; it changes only whether that answer is currently authoritative.

## Frozen load profiles

| Cardinality | 1,000 services | 5,000 services |
|---|---:|---:|
| File records | 3,151 | 15,751 |
| Distinct contents | 3,151 | 15,751 |
| Generated content bytes | 208,662 | 1,043,142 |
| Memberships | 5,000 | 25,000 |
| Memberships per role | 1,000 | 5,000 |
| Shared files (fan-out 10) | 100 | 500 |
| Contract files (fan-out 25) | 40 | 200 |
| Unowned source files | 10 | 50 |
| Maximum path fan-out | 25 | 25 |

Every service has one primary directory and one exact supporting contract,
shared file, generated file, and typed input. File records retain only path,
byte count, and content digest; their deterministic content algorithm lives in
`profile.go`. These profiles are synthetic load inputs. They establish no
target-corpus SLO, service-count limit, extraction accuracy, physical topology,
or production registration.

## Reproduction and verification

Normal tests verify and consume the retained artifacts; they do not mutate
them. Explicit maintenance may re-author everything with:

```sh
go run ./spike/t323/cmd/author
go test ./spike/t323/... -count=1
git bundle verify spike/t323/t323-neutral-corpus.bundle
```

The tests generate the catalog, oracle, and profile bytes twice; author two
fresh repositories with fixed Git identity and timestamps; require identical
commits, trees, bundle, receipt, and artifact digests; strict-decode every
closed shape; and compare the retained files byte-for-byte with fresh output.

The retained bundle was last authored with Git 2.54.0, and its digest pins that
pack encoding, while the SCIP blob pins the current protobuf deterministic
encoding. A Git upgrade may therefore change the bundle bytes while the commit
and tree identities remain stable; a protobuf upgrade may change a semantically
equivalent SCIP blob and thereby
change its tree and commit identities. Either case must fail loudly and
requires reviewed receipt re-authoring rather than an automatic re-pin.

This package is a development-only retained fixture. Production packages do
not import it; startup, restart, requests, queries, sync ticks, retries,
no-ops, publication transitions, and ordinary non-T32.3 package tests perform
no fixture generation, Git child, corpus scan, hash pass, store work, network
work, or retained mutation because of T32.3. Only explicit authoring and the
targeted T32.3 determinism test create Git children or generate the full
profiles, and those test runs use temporary directories without mutating the
retained artifacts.
