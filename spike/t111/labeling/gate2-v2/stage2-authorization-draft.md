# GATE2-V2 Stage 2 — authorization entry (DRAFT, not an authorization)

**Status: DRAFT for operator review and sign-off. Nothing on this page
authorizes anything. Sealed-mode execution of `stage2_prepare.py` remains
BLOCKED until the operator signs this entry and it lands as a dated PLAN.md
decision row with every «FILL» cell completed.**

Prepared 2026-07-21 by the session agent (r1/r2 reviewer), per the r2
disposition: "the operator's Stage-2 authorization entry binding the
Finding-4 set is the sole remaining gate."

## 1. Authority chain (each link dated and sealed)

1. GATE2-V2 revision 3, `sha256:f9d7eb8682c9d9284c5d6418f458835c6df43530222d00d4450a87765d18ca65`
   — approved 2026-07-16 (§12 decision, commit `e36680e`).
2. Stage-0 artifacts independently ACCEPTED 2026-07-16 (sealing commits
   `45445a4`, `9aa8256`, `26e1784`; 17-artifact manifest in `stage0/STAGE0.md`).
3. Stage-1 snapshot runner ACCEPTED at `7cd7c1d` (2026-07-16); snapshot
   ADMITTED 2026-07-20 (sealing commit `cf70bb1`; receipt
   `sha256:bbea9b7cae0189ed0a94ea58657c1ac229be245be653196711c2e2f73d8040ef`).
4. Stage-2 glue review: r1 FIX-FIRST (2026-07-20) → remediation (2026-07-21)
   → **r2 ACCEPT** (2026-07-21, `stage2-prepare-review-r2.md`).

## 2. Scope of authorization (Stage 2 only, per §3)

On sign-off this entry authorizes exactly:

1. **Frame enumeration** at the sealed Stage-1 heads by Stage-0-inventory-bound
   code, producing the `--cardinalities` and `--frame-membership` inputs;
2. **Assembly** of the `--heads` and `--design` inputs under the constraints
   in §3 below;
3. **One sealed-mode run** of the accepted `stage2_prepare.py` against the
   bound manifest (§4), under the execution conditions (§5);
4. **Sealing** of the Stage-2 inputs and outputs and a dated Stage-2 result
   record row (§6).

Nothing else is authorized. Stage 3 (selection, disclosure, labeling)
remains gated on the Stage-2 sealed artifacts. Stage 2 crosses no disclosure
edge: the census-v2 seed carries only previously-disclosed coordinates, and
stdout carries counts, rules, and digests only.

## 3. Input constraints (verification duties before signing)

- **`--heads`**: per fixture, `new_commit` must equal the Stage-1 receipt
  head oid — temporal `f95c865cc08c1ac075a709d525977e17103e6417`, dapr
  `f4d431123309a2bd11fcc32523661b6b14e8462b`, loki
  `562a762ab1d07985edc561920d74e792f4a6aab9`, online-boutique
  `9a4616e77f0f9cbcbecaf27d711c38890dda1404` (machine-enforced by the
  accepted tool; refusal is exit 2). `old_commit` values are bound
  procedurally to the sealed cohort lineage records — the `source_commits`
  of the bound burn-ledger cohorts; the signing operator records the
  derivation. `repo_dir` points at the local corpus mirrors
  (`spike/t111/corpus/<fixture>`).
- **`--design`**: exactly the §5 design points — client-call precision
  p = 995/1000, client-call recall p = 95/100, per-fixture precision
  p = 97/100 — and the §10 thresholds (98/100, 90/100), expressed as the
  tool's `"num/den"` rationals per frame. A reviewer verifies values against
  GATE2-V2 §5/§10 before signing.
- **`--cardinalities` / `--frame-membership`**: outputs of the enumeration
  step (§2.1) only; digests filled after enumeration. Their frames must be
  the four estimand frames of the sealed scorer's partition
  (`alpha_each = 1/80` machinery is inside the bound `power_advisory.py`).

## 4. Binding manifest

### Bound now (digests verified in r2, 2026-07-21)

| Input | sha256 | Provenance |
|---|---|---|
| `stage2_prepare.py` | `6db4eeedf8c188356d4295ddb1fdef7ccf9ad38adc709504a2385375c9633b70` | r2 ACCEPT |
| `stage2_prepare_test.py` | `500e5a7c902282ca87dca0576a000fd775e793a39ae71ecc3047a0389fa589c9` | r2 companion |
| `stage0/carry_forward.py` | `2bb3278fc086b8ce17dcb818959bdac63949112420622426499085882f58c589` | Stage-0 manifest |
| `stage0/power_advisory.py` | `8f59dd8e2256419a299fb61992e912b29582a7d946ffc909572ce674ea9d66c2` | Stage-0 manifest |
| `stage1_snapshot.py` | `487dcc78f33ba4e08626b35d9500e78eb66276d48b984393f36bccd6636779a1` | = accepted `7cd7c1d` bytes |
| `spike/t111/score.py` | `a1a04f51dee7d2044bd3433dadc2ef53f74519135f76e478810b1bc9366dece4` | Stage-0 inventory |
| `stage1/receipt.json` | `bbea9b7cae0189ed0a94ea58657c1ac229be245be653196711c2e2f73d8040ef` | sealing commit `cf70bb1` |
| `labeling/burn-ledger.json` (`--ledger`) | `4e6e2382361f1a0223562d4cbac921f39944ceb36c916912fa0ca5c259e3044a` | = estimator authorization's bound `burn_ledger_file`; structure verified 2026-07-21 (3 cohorts, 9,470 coordinates, four fixture systems only) |
| Glue landing commit | «FILL — commit must carry `stage2_prepare.py` bytes hashing to `6db4eeed…` and test bytes to `500e5a7c…` verbatim, per r2» | r2 condition |

### To bind at signing (digests filled by the operator)

| Input | sha256 | Constraint |
|---|---|---|
| `--cardinalities` | «FILL» | enumeration output (§2.1) |
| `--frame-membership` | «FILL» | enumeration output (§2.1) |
| `--heads` | «FILL» | §3 constraints; `new_commit` machine-checked |
| `--design` | «FILL» | §3 values verified by a reviewer |

## 5. Execution conditions (fail-closed)

- Interpreter: `/usr/bin/python3` (3.9.6, the fire-time interpreter of the
  accepted test suite); working directory `spike/t111/labeling/gate2-v2`.
- Sealed mode: `--receipt spike/t111/labeling/gate2-v2/stage1/receipt.json`;
  no `--synthetic`. Output `--out` must be fresh (refusal is exit 3).
- Exit semantics: **0** = all frames feasible → seal artifacts (§6);
  **1** = capacity stop → the round stops here with a committed root cause,
  before any human sees a coordinate (§3 of the protocol); **2/3/4** =
  refusal/collision/unexpected → stop with root cause.
- **No retry** of any non-zero exit without a new reviewed entry. Nothing is
  retried in place (§3).

## 6. Sealing and result record

After the run, regardless of exit: record the input manifest digests, the
command, the exit code, and the output digests
(`census-v2-seed.jsonl`, `stage2-preparation.json`, when published) as the
Stage-2 artifact seal, and append a dated PLAN.md Stage-2 result row
(`gate_status` semantics per precedent: a completed feasible preparation
leaves Gate 2 PENDING; ESTABLISHED is decided only at Stage 4).

## 7. PLAN.md row template (paste-ready once every cell is filled)

```
| «DATE» | GATE2-V2 Stage-2 authorization — one sealed-mode preparation run | **Stage-2 inputs bound; one sealed-mode run of `stage2_prepare.py` authorized — enumeration, carry-forward, exact power per §3. Nothing further.** Accepted glue `stage2_prepare.py` `sha256:6db4eeed…` (r2 ACCEPT 2026-07-21) at landing commit «FILL»; bound inputs: ledger `sha256:4e6e2382…`, receipt `sha256:bbea9b7c…` (`cf70bb1`), cardinalities «FILL», frame-membership «FILL», heads «FILL» (new_commit = Stage-1 receipt oids, machine-enforced; old_commit from ledger cohort lineage), design «FILL» (§5 points, §10 thresholds, reviewer-verified); already-bound identities per the manifest (`stage2-authorization-draft.md` §4). Execution: `/usr/bin/python3` 3.9.6, sealed mode, fresh out, no retry of any non-zero exit without a new reviewed entry. Stage 3 (selection, disclosure, labeling) remains gated on the sealed Stage-2 artifacts. |
```

## 8. Operator sign-off

- [ ] Every «FILL» cell completed and independently re-hashed
- [ ] `--design` values verified against §5/§10 by a reviewer
- [ ] `--heads` `old_commit` lineage derivation recorded
- [ ] Glue landing commit verified to reproduce both accepted digests
- [ ] PLAN.md row appended (§7); this draft's status line updated to SIGNED
      with date

Signed: ______________________  Date: ____________
