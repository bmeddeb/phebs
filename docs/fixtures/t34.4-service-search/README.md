# T34.4 neutral All code/service-search demo

This companion catalog binds the retained T32.3 neutral corpus as a separate
whole-repository `make dev` cohort. The ordinary Git mirror and whole-index
worker publish one repository source/search generation; ordinary catalog
ingestion and lifecycle reconciliation then activate the accepted services.
No focused analysis unit or experimental evidence pack is enabled.

## Demo

Start a fresh development cohort with `make dev`, wait for the retained
`t323-neutral-corpus` repository to become current, then:

1. Open **Search** and select **All code**. Search for `Orders`; the result
   spans the neutral orders implementation, protobuf contract, and generated
   client surface under one All code receipt.
2. Open **Services** for that repository, choose **Orders API**, and follow
   **Search this service**. The query and service deep link survive reload,
   browser navigation, and scope changes.
3. Search for `Orders` again. The service receipt names the exact active
   catalog/source/search generations and the emitted repository/revision
   citations. Shared `go.mod` and `shared/trace/trace.go` belong to both
   accepted services; explicitly unowned files never enter a service result.
4. Return to **All code** and search for `package risk`. The unowned risk file
   is searchable there but excluded from either accepted service. The scope
   selector states this shared/unowned policy without implying completeness.
5. Change the catalog or rebuild the whole generation to observe the existing
   current/stale/unavailable lifecycle labels. A stale service continues to
   use its last complete active authority; an unavailable service refuses a
   scoped search rather than falling back to All code.

The selector is a keyboard-accessible fieldset and wraps at narrow viewport
widths. HTTP uses the same `scope`, `repository`, and `service_key` parameters;
MCP `search_code` exposes the same selector and returns the same receipt shape.

## Retained identity and bounds

The source bundle is `spike/t323/t323-neutral-corpus.bundle`: 6,771 bytes,
SHA-256 `8d70693ee440ff7683f8c3a39cc9b6565dd265cbc546d40e961759f2237617fa`,
with final commit `4ac5335893fc18a1243b60a005faa1f09268d858` and 14 regular files. The
catalog is 3,401 bytes with five lifecycle identities, thirteen membership
records, and six explicit unowned placements. Its accepted services exercise
primary, supporting, generated, typed, and many-to-many shared roles.

Search retains the existing 500-file response cap and 128-path service
predicate cap. The receipt holds at most one citation identity per emitted
file and never retains result chunks. It binds emitted citations, not
whole-corpus completeness.

This neutral product demonstration establishes no service relationship,
evidence, runtime-use, extraction accuracy/completeness, target SLO/scale,
migration-completion, decommission-safety, or release claim. `GATE2-V2`
remains `NOT_ESTABLISHED`.
