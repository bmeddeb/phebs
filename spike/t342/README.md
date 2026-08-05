# T34.2 service-query compiler gate

This retained test gate applies the production `internal/servicequery`
compiler to the independently authored T32.3 corpus. It is validation input,
not a product surface or target-scale claim.

`go test ./spike/t342/ -count=1`:

- replays all 18 exact or explicitly stale service-search expectations across
  the five neutral catalog revisions through a real in-process zoekt reader;
- derives every placement from the T32.3 authority records, adapting only the
  production catalog's required exact supporting role beside a typed role;
- runs stale search against its last complete active catalog and search
  generation rather than relabeling the desired generation;
- covers primary, supporting, shared, generated, and typed paths while keeping
  matching unowned/out-of-service files outside the compiled query; and
- proves a 100-file higher-ranking outside population cannot consume a
  one-result/one-match budget before the service path predicate is applied.

Focused production-package tests separately pin the closed authority digest,
HEAD and tag selectors, escaped path semantics, role/path deduplication,
128-path and 64-KiB path ceilings, the 132,608-byte conservative compiled
predicate ceiling, the 16-KiB expression ceiling, exact active-reader
matching, and explicit unavailable and cross-repository refusal.

The gate creates temporary indexes only. It retains no generated shard or
measurement receipt and establishes no SLO, target scale, completeness,
migration completion, decommission safety, or release authority.
