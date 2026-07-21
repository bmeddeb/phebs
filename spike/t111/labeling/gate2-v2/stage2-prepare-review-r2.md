# Stage-2 glue — independent review, round 2 (re-review of the fixed bytes)

- **Candidate:** `spike/t111/labeling/gate2-v2/stage2_prepare.py`
  `sha256:6db4eeedf8c188356d4295ddb1fdef7ccf9ad38adc709504a2385375c9633b70`
  (test file `stage2_prepare_test.py`
  `sha256:500e5a7c902282ca87dca0576a000fd775e793a39ae71ecc3047a0389fa589c9`)
- **Reviewer:** the session agent (independent; the r1 findings were remediated
  by a prior session — implementer ≠ reviewer preserved). This is the re-review
  r1 assigned to this reviewer ("on receipt of the fixed bytes this reviewer
  re-runs the full checklist").
- **Date:** 2026-07-21
- **Standard:** GATE2-V2 revision 3
  (`sha256:f9d7eb8682c9d9284c5d6418f458835c6df43530222d00d4450a87765d18ca65`,
  confirmed on disk) §3, §5, §6; the 2026-07-17 PLAN.md clarification (d); the
  r1 review (`stage2-prepare-review-r1.md`) findings as the fix specification.
- **Verdict: ACCEPT.** Both required edits are implemented correctly and
  test-proven; the recommended edit is adopted and exceeded. The r1 verified
  properties re-verify clean. This ACCEPT plus the Finding-4 bindings below is
  the complete input to the operator's Stage-2 authorization entry. Sealed-mode
  execution remains BLOCKED until that entry lands; this review executed no
  Stage-2 run (synthetic tests and read-only validator probes only).

## Ground truth established before review

1. **Lineage.** HEAD (`ff82f8a`) bytes of both files hash exactly to the
   r1-reviewed digests (`0ffe7d11…`, `cb20454a…`); the fixed bytes are
   worktree modifications on top, so the diff under review is exactly the
   remediation, nothing else.
2. **No Stage-2 execution.** No `census-v2-seed.jsonl`,
   `stage2-preparation.json`, output directory, or staging remnant exists
   anywhere in the spike tree; `stage0/`, `stage1/`, `stage1_snapshot.py`, and
   `spike/t111/score.py` are worktree-clean. The only other worktree changes
   (`.idea/`, untracked `AGENTS.md`, `.idea/.name`) are unrelated and were
   preserved.
3. **Record consistency.** The PLAN.md worktree change is exactly the
   2026-07-21 remediation record, and it names the same candidate digests
   verified here.

## Finding 1 (REQUIRED, r1) — head binding: FIXED, verified three ways

`verify_receipt` now validates the receipt's heads envelope, not just its
presence: `heads` must be an object whose fixture set **exactly equals** the
sealed Stage-0 constants (`["temporal","dapr","loki","online-boutique"]`,
`snapshot-constants.json`), and every `head_oid` must fullmatch the sealed
runner's `OID_RE`. New `verify_heads` then requires the `--heads` fixture set
to exactly equal the receipt's and every `new_commit` to equal its sealed
`head_oid` — any mismatch, missing, extra, or malformed entry raises
`Stage2Error` (exit 2) **before** `carry_forward` runs. The wrong-head
fail-open path from r1 (wrongful frees via `absent` at a stale head) is
closed: the mapper's new side is now mechanically the admitted snapshot.

- **Required test present:** `test_refuses_new_commit_not_matching_admitted_head`
  (exit 2, `new_commit does not match`, no output directory), plus
  fixture-set coverage (extra fixture, missing fixture).
- **Independent probes against the real admitted receipt**
  (`stage1/receipt.json`, `sha256:bbea9b7c…`, matching the PLAN.md Stage-1
  ADMITTED entry): the real validator chain (`verify_receipt` →
  `verify_heads`) **accepts** the admitted receipt with heads whose
  `new_commit` values equal the sealed head oids; a single flipped
  `new_commit` **refuses** (`heads 'loki' new_commit does not match the
  sealed Stage-1 head`); a dropped fixture **refuses** (`heads fixtures do
  not exactly match`). Probes were read-only validation calls — no
  enumeration, mapping, or power ran.

## Finding 2 (REQUIRED, r1) — failure semantics: FIXED

`main()` wraps the run body: `Stage2Error` → one canonical stderr line, exit
2; any other exception → one canonical stderr line
(`stage2: unexpected failure: {TypeName}`), exit **4**. The exit-code map is
now 0 pass / 1 capacity stop / 2 refusal / 3 output collision / 4 unexpected —
a crash can no longer read as a capacity determination. The tombstone source
is removed structurally: `out.mkdir` no longer happens up front; the final
output directory comes into existence only as the atomic publication rename,
and all fallible computation precedes publication.
`test_unexpected_error_has_distinct_exit_and_no_output` proves exit 4 with
exact stderr bytes and no output directory.

## Finding 3 (RECOMMENDED, r1) — atomic publication: ADOPTED, exceeded

`publish_outputs` stages both payloads as `O_EXCL` temp files with a full
write loop and per-file fsync, renames them to final names inside a hidden
sibling staging directory, fsyncs that directory, then atomically renames the
staging directory onto the fresh output path and fsyncs the parent. The
complete two-file set therefore appears in one atomic edge — stronger than
the Stage-1 house standard (which atomizes each file).
`test_output_pair_is_not_published_if_directory_rename_fails` injects failure
at the final rename and proves no final output directory appears (the
complete pair remains in hidden staging). The end-to-end test additionally
asserts no `.*.tmp` residue in a published output.

## r1 verified properties — re-run, all hold

1. **Sealed dependencies intact.** `carry_forward.py`
   `sha256:2bb3278fc086b8ce17dcb818959bdac63949112420622426499085882f58c589`,
   `power_advisory.py`
   `sha256:8f59dd8e2256419a299fb61992e912b29582a7d946ffc909572ce674ea9d66c2`,
   `carry_forward_test.py`
   `sha256:68b8de20b3a79d07e560560216e44c7b5039a1720f70f5e5a5f0b03e9f48bd9c`
   — all byte-identical to the STAGE0.md §8 manifest.
2. **Sealed-mode gate matches clarification (d).** Unchanged in its binding
   core (schema, ADMITTED status, sealed cutoff, sealed query digest, with
   `s1.load_sealed_inputs()` transitively re-verifying the sealed Stage-0
   query/constants against the manifest before any action); strengthened by
   the heads-envelope validation above.
3–5. **Burn semantics, mapper conservatism, sealed power machinery.** The
   diff touches none of `ledger_coordinates`, `carry_forward`,
   `burns_per_frame`, or `power`; the sealed mapper and power script are
   digest-verified above. The r1 analysis carries over unchanged.
6. **No disclosure.** Stdout remains counts, rules, and digests only.
7. **Fences.** Fresh-output-dir refusal (exit 3) intact; **9/9 synthetic
   tests pass under the fire-time interpreter (`/usr/bin/python3`, 3.9.6)**,
   run by this reviewer, including the receipt-binding refusal cases, the
   exit-4 case, and the publication fault-injection case.

## Observations (non-blocking; no edit required)

- A crash before the final rename leaves the hidden `.…​.tmp` staging
  directory; the next run with the same `--out` then refuses with exit 3 and
  the "output already exists" message although the visible output is absent.
  Fail-closed and self-documenting at the filesystem level; recovery is
  operator removal of the stale staging directory. Cosmetic message
  imprecision only.
- `verify_heads` binds `new_commit` mechanically; `old_commit` and
  `repo_dir` remain procedurally bound (Finding 4), as r1 specified — no
  proportionate mechanical check exists.
- The candidate bytes are currently **uncommitted worktree bytes**. The
  commit that lands them must reproduce exactly
  `sha256:6db4eeed…` / `sha256:500e5a7c…`; a commit carrying any other bytes
  does not inherit this ACCEPT.

## Finding 4 — authorization binding set (input to the operator's entry)

The glue remains parameter-free. The Stage-2 authorization entry must bind,
by digest:

- the accepted `stage2_prepare.py`
  `sha256:6db4eeedf8c188356d4295ddb1fdef7ccf9ad38adc709504a2385375c9633b70`
  (this review);
- the `--design` file — a reviewer must verify its values are the §5 design
  points (precision p = 995/1000, recall p = 95/100, per-fixture precision
  p = 97/100) and §10 thresholds (98/100, 90/100);
- the `--cardinalities` and `--frame-membership` files (outputs of the
  sealed frame-enumeration step);
- the `--heads` file — `new_commit` values equal to the Stage-1 receipt
  heads (now machine-enforced), `old_commit` values bound procedurally to
  the sealed cohort lineage records;
- the `--ledger` file;
- already-bound identities: `carry_forward.py` `sha256:2bb3278f…` and
  `power_advisory.py` `sha256:8f59dd8e…` (Stage-0 manifest, re-verified
  above); `stage1_snapshot.py`
  `sha256:487dcc78f33ba4e08626b35d9500e78eb66276d48b984393f36bccd6636779a1`
  (byte-identical to the accepted `7cd7c1d` bytes, re-verified); the sealed
  scorer `score.py` (on-disk
  `sha256:a1a04f51dee7d2044bd3433dadc2ef53f74519135f76e478810b1bc9366dece4`,
  last touched at `0f1d7af`, worktree-clean); the Stage-1 receipt
  `sha256:bbea9b7cae0189ed0a94ea58657c1ac229be245be653196711c2e2f73d8040ef`
  (sealing commit `cf70bb1`).

## Disposition

ACCEPT. The candidate is eligible for sealed-mode authorization; the
operator's Stage-2 authorization entry binding the Finding-4 set is the sole
remaining gate. Nothing in this review executed Stage 2, disclosed a
coordinate, or modified a sealed artifact.
