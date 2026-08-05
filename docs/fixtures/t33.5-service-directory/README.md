# T33.5 neutral service-directory demo

This companion catalog turns the retained T30.7 neutral repository into a
five-service `make dev` directory without installing an API response fixture.
The ordinary operator-catalog reader binds the file to the indexed repository
census, the ordinary lifecycle reconciler publishes service rows, and the
same authorization-first HTTP service supplies both the React directory and
MCP tools.

The catalog intentionally contains a small closed lifecycle vocabulary:

- accepted `orders-api` and `orders-events` identities, initially unavailable
  until an exact active generation exists;
- an unavailable `returns-proposal` authority proposal;
- an explicit `billing-control` authority conflict; and
- a retained removed `legacy-orders` identity whose declared successor is
  `orders-api`.

The two accepted services reuse `api/orders.proto` and `go.mod`, exercising
many-to-many supporting/shared placement. Seven of the repository's nine
regular files are accepted; the outside caller and irrelevant bulk file remain
explicitly unowned. Proposal/conflict placement references are still present
for inspection but do not convert those files into accepted coverage.

## Demo

Start the ordinary development cohort with a fresh data directory, or retain
an existing directory to exercise exact retry/no-op behavior:

```sh
make dev
```

After the neutral repository is indexed and catalog reconciliation settles:

1. Open **Repos** and choose **Services** for
   `example.invalid/t307-neutral-service`.
2. Confirm the authority is `operator · t335-demo · v1`, the catalog contains
   five identities, and the repository summary reports seven accepted and two
   unowned files.
3. Open `orders-api` and `orders-events` to inspect their primary,
   supporting, generated, and shared path identities. These paths are metadata
   only; the directory does not read file bytes.
4. Enable removed identities and inspect `legacy-orders`. Its successor is
   catalog lineage, not evidence of a runtime relationship.
5. Filter to authority conflicts and inspect `billing-control`, then filter to
   proposals and inspect `returns-proposal`.
6. Reload an exact service URL and use browser back/forward across filters,
   detail selections, and bounded pages. The hash route retains repository,
   filter, cursor, and service identity.

## Bounds and non-claims

The retained catalog is 2,801 encoded bytes, five services, eleven membership
records, two unowned placements, and no typed placement. The UI requests 50
services per page, mounts only that page and one selected detail, and follows
the authorization/catalog/summary/incarnation-bound cursor supplied by T33.4.
The underlying store scans at most 500 lifecycle rows per continuation, so a
sparse filter can correctly return an empty page with a next cursor.

This is neutral product and operations input. It establishes no service
relationship, runtime-use, completeness, extraction-accuracy, target-scale,
migration-completion, decommission-safety, or release claim. `GATE2-V2`
remains `NOT_ESTABLISHED`.
