# T30.7 neutral focused-service demo

This retained development cohort is one small, neutral monorepo used by
`make dev` and `make dev-api`. The Make recipes bind its cloneable Git bundle
through the ordinary sync, focused indexing, candidate, evidence, resolver,
caller-generation, and store-derived Workbench pipelines. They install no
Contract Atlas response fixture, Investigation view fixture, synthetic
Workbench service, or authored Workbench answer.

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
make dev
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
   partitions plus the deliberate typed-index gap. Workbench is the real
   store-derived provisional service over the same catalog and caller
   authority; it has no retained answer fixture.

## Bounds and repeatability

The committed bundle has one repository, one commit, nine regular files,
eight candidate records (seven `base`, one `go_test`), one configured primary
directory, and four exact supporting paths.
Startup does not generate or mutate the fixture. A first run performs one local
bundle sync, one focused index build, one candidate tree walk, bounded local
domain extraction, one resolver materialization, and the bounded caller-leaf
sequence. Subsequent unchanged runs reuse the same commit, unit digest, policy
digest, manifests, outcomes, and complete caller generation; routine startup
does not add a second fixture cohort or widen the selected paths.

`cmd/author` deterministically recreates the one-commit bundle from the
reviewed `repo/` tree. Normal tests and development startup verify and consume
the committed bundle; they do not invoke the author. The fixture establishes
only the end-to-end demo shape. It makes no completeness, runtime-use,
migration-safety, retirement-safety, or external extraction-accuracy claim.
