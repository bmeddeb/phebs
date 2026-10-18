# T30.7 neutral focused-service demo

This retained development cohort is one small, neutral monorepo used by
`make dev` and `make dev-api`. Their explicit default `PHEBS_DEV_COHORT=whole`
binds the same source through ordinary whole indexing and source/search,
service, and relationship publication. Both the external caller and bulk
sentinel are searchable there; Search has a whole-repository receipt.

This README's focused walkthrough is selected explicitly with
`PHEBS_DEV_COHORT=focused`. It binds the cloneable bundle through ordinary sync,
focused indexing, candidate, evidence, resolver, and caller-generation pipelines. They pair it with
the retained [T33.5 service catalog](../t33.5-service-directory/README.md)
through ordinary catalog ingestion and lifecycle reconciliation. They install
no Contract Atlas or service-directory response fixture or authored workflow
answer.

The configured `orders-service` analysis unit has:

- primary path `service/orders`;
- supporting protobuf declaration `api/orders.proto`, generated gRPC source
  `gen/ordersv1/orders_grpc.pb.go`, its generated-from snapshot, and `go.mod`;
- no typed index, deliberately exposing the `repository-root-unbound` gap;
- real protobuf declaration and gRPC registration facts;
- real Kafka producer and consumer facts for `orders.created.v1`;
- one `_test.go` source containing `orders.test-only.v1`, which must be counted
  in the `go_test` lane and excluded from production evidence;
- one gRPC caller at `consumers/external/caller.go`, outside the focused shard
  but retained by the complete caller overlay; and
- `bulk/irrelevant.txt`, whose `T307_OUTSIDE_UNIT_BULK_NEEDLE` sentinel must
  remain absent from focused Search.

## Demo

Start with a fresh development data directory, or retain the existing one to
exercise the normal no-op/reconciliation path:

```sh
make dev PHEBS_DEV_COHORT=focused
```

After the repository's index and evidence jobs settle:

1. Search for `T307_FOCUSED_SERVICE_NEEDLE`; the result is
   `service/orders/service.go` and the adjacent scope panel names
   `orders-service`.
2. Search for `T307_OUTSIDE_UNIT_BULK_NEEDLE`; focused Search returns no
   result. Searching for `consumers/external/caller.go` likewise does not make
   that outside-unit file part of the focused shard.
3. Open Contracts and select `/demo.orders.v1.Orders/Create`; the declaration
   and in-unit server registration are ordinary extracted evidence.
4. Open Topics and select `orders.created.v1`; both the producer and consumer
   cite `service/orders/kafka.go`, while `orders.test-only.v1` is absent.
5. Open Caller Map for the Create operation; the complete repository overlay
   cites `consumers/external/caller.go` and labels it as overlay evidence rather
   than focused local evidence.
6. Confirm the scope diagnostics report the base and excluded `go_test`
   partitions plus the deliberate typed-index gap.
7. From Repos, open **Services** for the neutral repository and inspect its
   catalog metadata. Active service-search/relationship authority remains
   unavailable in focused mode: this index has no direct whole-search source
   generation. For the companion catalog's current-service walkthrough, use
   default `make dev` whole mode and wait for ordinary reindex/publication
   reconciliation (or use a fresh development data directory). This preserves
   the focused limitation; it does not implement focused service support.

## Bounds and repeatability

The committed bundle has one repository, one commit, nine regular files,
eight candidate records (seven `base`, one `go_test`), one configured primary
directory, and four exact supporting paths.
Startup does not generate or mutate the fixture. A first run performs one local
bundle sync, one focused index build, one candidate tree walk, bounded local
domain extraction, one resolver materialization, and the bounded caller-leaf
sequence. Subsequent unchanged focused runs reuse the same commit, unit digest, policy
digest, manifests, outcomes, and complete caller generation; routine startup
does not add a second fixture cohort or widen the selected paths.

`cmd/author` deterministically recreates the one-commit bundle from the
reviewed `repo/` tree. Normal tests and development startup verify and consume
the committed bundle; they do not invoke the author. The fixture establishes
only the end-to-end demo shape. It makes no completeness, runtime-use,
migration-safety, retirement-safety, or external extraction-accuracy claim.

The two modes consume exactly the retained source bytes and guards. Whole mode
refuses any configured analysis unit rather than removing it. Explicit empty or
unknown selectors refuse; direct T307 environment binding without a selector
retains historical focused behavior. Ordinary serve without demo bindings stays
unchanged. Historical focused receipts, including the unavailable service
pipeline recorded in `ui/receipts/t458b.json`, are not rewritten or relabeled.
