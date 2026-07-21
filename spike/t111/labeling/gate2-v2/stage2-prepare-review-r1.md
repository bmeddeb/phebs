# Stage-2 glue — independent review, round 1

- **Candidate:** `spike/t111/labeling/gate2-v2/stage2_prepare.py`
  `sha256:0ffe7d111f015a0075748dcbf5c60d2564bff79cd34f6c06b7686c0c5801a7c2`
  (test file `stage2_prepare_test.py`
  `sha256:cb20454a1a746a30879836a1db6b3d69f2c71a40b88900c746b2702a82088c48`)
- **Reviewer:** the session agent (independent; the implementer was a prior
  session — implementer ≠ reviewer preserved).
- **Date:** 2026-07-20
- **Standard:** GATE2-V2 revision 3
  (`sha256:f9d7eb8682c9d9284c5d6418f458835c6df43530222d00d4450a87765d18ca65`)
  §3, §5, §6, and the 2026-07-17 PLAN.md clarification (d).
- **Verdict: FIX-FIRST.** Two required edits before any sealed-mode
  authorization; one recommended. Re-review on the fixed bytes. The design is
  sound; the findings are the small, well-scoped class FIX-FIRST exists for.

## Verified properties (checked, no findings)

1. **Sealed dependencies intact.** `carry_forward.py`
   `sha256:2bb3278f…`, `power_advisory.py` `sha256:8f59dd8e…`,
   `carry_forward_test.py` `sha256:68b8de20…` — all byte-identical to the
   Stage-0 manifest (STAGE0.md §8). The glue imports the sealed mapper and
   the sealed power script; it reimplements nothing.
2. **Sealed-mode gate matches clarification (d).** Refuses without a Stage-1
   receipt outside `--synthetic` (exit 2); the receipt must match the
   receipt schema, `ADMITTED` status, the sealed cutoff, and the sealed
   query digest; the check transitively re-verifies the sealed Stage-0
   query/constants against the STAGE0.md manifest before any action
   (`s1.load_sealed_inputs()`).
3. **Burn semantics are conservative in every drift direction.**
   `system-not-in-snapshot` → burn; a burned coordinate absent from every
   frame's membership counts against **all** frames (test-proven);
   membership in multiple frames counts in each; any key-format drift
   between enumeration membership and ledger coordinates degrades toward
   *more* burning, never less.
4. **The mapper cannot free on doubt.** All `classify` uncertainty paths
   burn; only positively-absent (file gone, no traced successor) frees;
   rename tracing runs un-pathspec'd per §6; git failures map to burn,
   never raise.
5. **Power is the sealed exact machinery.** `minimal_n` is the Stage-0
   script's binary search over the scorer-transcribed exact hypergeometric
   (`alpha_each = 1/80`, assurance 9/10); burns subtract from population
   and census clamps — conservative direction only; exhausted population →
   infeasible.
6. **No disclosure.** Stdout carries counts, rules, and digests only; the
   seed file carries only previously-disclosed coordinates — which is the
   carry-forward premise.
7. **Fences work.** Fresh-output-dir refusal (exit 3); 4/4 committed
   synthetic tests pass under the fire-time interpreter
   (`/usr/bin/python3`, 3.9.6), including the unmapped-burns-count-against-
   all-frames case and the receipt-binding refusal case.

## Finding 1 — REQUIRED: `new_commit` is not bound to the sealed Stage-1 heads

`--heads` supplies `new_commit` per fixture; `verify_receipt` validates the
receipt's schema/status/cutoff/query-digest but never compares
`heads[fixture]["new_commit"]` against `receipt["heads"][fixture]["head_oid"]`
— the receipt's sealed heads are checked for *presence only* (line 73) and
their content is otherwise unused.

GATE2-V2 §6 requires mapping coordinates "to the Stage-1 heads". A stale,
mistyped, or wrong `new_commit` produces silently wrong burn decisions —
including wrongful frees (pointing `new_commit` at a commit where the file
is absent yields `absent` → free for a site that persists at the true
sealed head) — inflating capacity and letting the round continue on
arithmetic that does not bind the sealed snapshot. This is the one
fail-open path in an otherwise fail-closed tool.

**Required edit:** in sealed mode (not `--synthetic`), require every
fixture in the heads input to satisfy
`heads[fixture]["new_commit"] == receipt["heads"][fixture]["head_oid"]`;
any mismatch, missing fixture, or extra fixture refuses (exit 2).
**Required test:** a sealed-mode run whose `new_commit` differs from the
receipt head must refuse.

## Finding 2 — REQUIRED: failure-path semantics collide with the capacity-stop exit code

`run()` has no catch-all. Malformed inputs (`load_json`, `int()`,
missing keys) raise uncaught → traceback, exit **1** — the same code as
"frames infeasible, round stops on capacity". A crash is not a capacity
determination, and the two must not share a signal; a crash after
`out.mkdir` also leaves a tombstone directory that blocks path reuse.

**Required edit:** wrap the run body; unexpected exceptions → one canonical
error line on stderr and a distinct exit code (4), before any result file
is written (writes already happen last).

## Finding 3 — RECOMMENDED: atomic output publication

Result files are written with plain `write_text` to final names; a crash
between the two writes leaves a partial output set. The Stage-1 runner set
the house standard (temp file, fsync, rename, directory fsync). Adopt it
for `census-v2-seed.jsonl` and `stage2-preparation.json`.

## Finding 4 — AUTHORIZATION CONDITIONS (not code)

The glue is deliberately parameter-free ("no thresholds, no sampling
rules"). Therefore the Stage-2 authorization entry must bind, by digest:

- the fixed `stage2_prepare.py` (post-re-review);
- the `--design` file — a reviewer must verify its values are the §5 design
  points (precision p = 995/1000, recall p = 95/100, per-fixture precision
  p = 97/100) and §10 thresholds (98/100, 90/100);
- the `--cardinalities` and `--frame-membership` files (outputs of the
  sealed frame-enumeration step);
- the `--heads` file — `new_commit` values equal to the Stage-1 receipt
  heads per Finding 1, `old_commit` values bound procedurally to the sealed
  cohort lineage records (no proportionate mechanical check exists);
- the `--ledger` file;
- already-bound identities: `carry_forward.py` and `power_advisory.py`
  (Stage-0 manifest), `stage1_snapshot.py` (accepted `7cd7c1d`), the sealed
  scorer `score.py`, and the Stage-1 receipt (sealing commit `cf70bb1`).

## Disposition

FIX-FIRST. On receipt of the fixed bytes this reviewer re-runs the full
checklist; ACCEPT from that re-review plus the Finding-4 bindings is the
complete input to the operator's Stage-2 authorization entry. Sealed-mode
execution remains BLOCKED until then.
