# Retained engineering records

These files explain how decisions were reached or provide deterministic test
inputs. They are not current user instructions, active roadmap items, or
product claims.

## Record classes

### Sealed validation evidence

- [T11.1 / GATE2-V2 report](../spike/t111/REPORT.md)
- [T11.1 sealed record index](../spike/t111/labeling/README.md)

The complete tracked `spike/t111/` tree is sealed. Do not rewrite, relocate, or
delete it. `make docs-check` pins its tree digest. A later document may cite
the terminal `NOT_ESTABLISHED` result but cannot reinterpret it as accuracy
evidence.

### Retained validation and design spikes

- [T19.1 Thrift protocol-pack spike](../spike/t191/README.md)
- [T20.1 monorepo correctness/scale spike](../spike/t201/README.md)
- [T21.1 Workbench inventory and vocabulary contract](../spike/t211/README.md)
- [T22.1 Thrift field-reference spike](../spike/t221/README.md)
- [T23.1 Kafka topic-evidence spike](../spike/t231/README.md)
- [T30.1 focused-index and shard-set spike](../spike/t301/README.md)
- [T30.6m historical-publication retention decision and owner inventory](../spike/t306m/README.md)

These directories preserve executable gates, locked inputs, synthetic
fixtures, and decision tables used by their completed tickets. They may be
maintained when a reproducibility defect is found, but they do not become
current behavior documentation and do not inherit T11.1’s sealed status.
Production packages must not import spike packages.

The corrected T30.6m record separates its selected unbounded-retention posture
for historical evidence and adjacent candidate/caller residue from the mixed
evidence-pin lifecycles, the unchanged configured proof-bundle lifecycle,
other unchanged owner lifecycles, and incidental growth in the eight durable
job tables and exact 24-table Investigation/Workbench domain. It authorizes no
cleanup. T30.6n owns bounded
job reads and startup-migration repair. T30.6o owns the authorization-first
status shell, fixed 52-component registry, zero-scan unavailable projection,
and warning; T30.6p owns 21 core SurrealDB components, T30.6q owns the exact 24
Investigation/Workbench tables, and T30.6r owns seven bounded derived store and
filesystem components and completes the status surface. Database index
bootstrap in T30.6p/T30.6q must be bounded and restart-resumable, and T30.6r
directory scans must be bounded. None of these tickets changes deletion,
configuration, or owner lifecycle semantics.

### Deterministic product fixtures

- [Change Workbench closure fixture](./fixtures/change-workbench/README.md)
- [Investigation envelope fixtures](./fixtures/investigations/README.md)
- [Thrift field-reference fixture](./fixtures/thrift-field/README.md)

Fixtures prove bounded software behavior. Synthetic or authored evidence is
not public-corpus accuracy or completeness evidence.

### Design handoff

- [Context Port brand and rail UI handoff](./design_handoff_phebs_brand_and_ui/README.md)
- [Design token notes](./design_handoff_phebs_brand_and_ui/notes/tokens.md)

The handoff records the origin of the current visual language. Its prototypes
and support files are references, not production code or an active UI
specification; current UI behavior belongs in the user guides and tests.

### Planning history

- [Completed ticket archive](./BACKLOG_COMPLETED.md)
- [Append-only architecture and decision ledger](../PLAN.md)

The active [roadmap](./ROADMAP.md) and [backlog](./BACKLOG.md) supersede old
sequencing statements without erasing their history.

## Preservation rules

- Keep sealed evidence byte-identical.
- Keep receipts, locks, digests, and fixture provenance with the artifact they
  bind.
- Do not quote a historical status as current product behavior; link the
  active authority.
- Do not turn a spike observation, synthetic fixture, or design prototype into
  a product, scale, or accuracy claim.
- Update this index when a new retained record class or entry point is added.
