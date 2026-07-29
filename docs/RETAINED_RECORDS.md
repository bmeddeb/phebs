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

These directories preserve executable gates, locked inputs, synthetic
fixtures, and decision tables used by their completed tickets. They may be
maintained when a reproducibility defect is found, but they do not become
current behavior documentation and do not inherit T11.1’s sealed status.
Production packages must not import spike packages.

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
