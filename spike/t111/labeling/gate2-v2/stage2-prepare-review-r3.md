# GATE2-V2 Stage-2 preparation glue — independent review r3

**Verdict: ACCEPT — implementation binding only. Sealed-mode preparation
remains BLOCKED pending the operator's Stage-2 preparation authorization
binding these bytes and the Finding-4 input set. `gate_status` PENDING.**

Reviewer: Claude, independent of the R8 implementation. This supersedes the
r2 implementation binding, not its historical record.

## Accepted bytes (commit `d1301ee`)

- `stage2_prepare.py` `sha256:1e64fddc20756604ce12f636dfd96ab01f3b4dd123f478ae196902b8709059f4`
- `stage2_prepare_test.py` `sha256:ea677860be674889fa1ba11485b8417badc10f1c1687f7c3fb0f810cc15a8239`

## Checks, all PASS

- **Per-fixture exact power is computationally bound (Finding-4 closed).**
  `per_fixture_power` computes `minimal_n` for each of the two precision
  frames × four fixtures at the sealed `per_fixture_precision` design point;
  the design loader requires that entry with `0 ≤ threshold < p ≤ 1` and
  rejects inert metadata. Per-fixture feasibility joins the aggregate gates
  and fails closed together.
- **Deterministic per-fixture populations with a sum guard.** Populations
  are derived by grouping frame-membership keys on their `system:`
  component; the derived sum must equal the aggregate cardinality
  (client-call precision 5,921 → dapr 1,436, loki 163, online-boutique 19,
  temporal 4,303, independently reproduced) or the tool refuses. Per-fixture
  burns are likewise reconciled to the aggregate precision burns.
- **The old-commit selection rule is sealed, not a fire-time choice.**
  `SEALED_OLD_COMMITS` binds temporal `8224a537…`, dapr `08aebd8b…`, loki
  `1362d277…`, online-boutique `9a4616e7…` (equal to its sealed head — zero
  carry-forward diff), bound in turn to the prior carry-forward receipt
  `expansion-lineage.json` (`86d0a76a…`) and the sealed burn ledger. Heads
  not matching the rule are refused.
- **Sealed Git execution context.** `verify_git_execution_context()`
  refuses any ambient `GIT_*` override and a non-sealed git on PATH — a
  correct fail-closed hardening. (Note: this suite must run in a clean
  environment; an injected `GIT_EDITOR`/`GIT_DIR` legitimately trips the
  guard, which is the intended behavior, not a defect.)
- 19/19 tests pass under the bound CLT python3.9 in a clean environment.

## Boundary

This binds implementation bytes only. Before any sealed-mode run the
operator must commit a Stage-2 preparation authorization binding these exact
digests, the enum-02 cardinalities (`db2f702f…`) and membership
(`9bdabf95…`), the sealed design values including `per_fixture_precision`,
the heads under the sealed old-commit rule, and the burn ledger. No sample
selection or coordinate disclosure exists.
