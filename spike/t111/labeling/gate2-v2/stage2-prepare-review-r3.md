# GATE2-V2 Stage-2 preparation verifier — independent review r3 (request)

**Verdict: PENDING INDEPENDENT REVIEW.**

This is a fresh implementation and trust-closure review request, not an
authorization or worksheet acceptance. The pre-authorization Finding-4 audit
found that the r2-accepted glue did not compute the protocol's per-fixture
precision power and left `old_commit` as a procedural choice. R8 changes the
executable bytes and result schema, so it cannot inherit r2 authority. The r2
record remains immutable historical lineage only. Preparation, selection,
disclosure, and ceremony access remain blocked.

## Candidate bindings

- `spike/t111/labeling/gate2-v2/stage2_prepare.py`: `sha256:1e64fddc20756604ce12f636dfd96ab01f3b4dd123f478ae196902b8709059f4`
- `spike/t111/labeling/gate2-v2/stage2_prepare_test.py`: `sha256:ea677860be674889fa1ba11485b8417badc10f1c1687f7c3fb0f810cc15a8239`
- `spike/t111/labeling/gate2-v2/stage2-prepare-review-r2.md`: `sha256:fc2e5395756d5cdac9a9cf1c3c09cb1ba0a6113d625cf92c2cfc0b1807e919d9`
- `spike/t111/labeling/expansion-lineage.json`: `sha256:9c821523db05bf959f9e01e05529d18bfede1f3c505d54019fb9c33d602dc430`
- `spike/t111/labeling/burn-ledger.json`: `sha256:4e6e2382361f1a0223562d4cbac921f39944ceb36c916912fa0ca5c259e3044a`
- `spike/t111/labeling/GATE2-V2.md`: `sha256:f9d7eb8682c9d9284c5d6418f458835c6df43530222d00d4450a87765d18ca65`
- `spike/t111/labeling/gate2-v2/stage0/carry_forward.py`: `sha256:2bb3278fc086b8ce17dcb818959bdac63949112420622426499085882f58c589`
- `spike/t111/labeling/gate2-v2/stage0/power_advisory.py`: `sha256:8f59dd8e2256419a299fb61992e912b29582a7d946ffc909572ce674ea9d66c2`
- `spike/t111/score.py`: `sha256:a1a04f51dee7d2044bd3433dadc2ef53f74519135f76e478810b1bc9366dece4`
- `spike/t111/label_prep.py`: `sha256:53c83c4e4e5ca3370b52d3dd42a2fa125ecbafdeba09ae396dac1d72e6015d07`
- `spike/t111/gate34_common.py`: `sha256:1e837da18b761fe1dd56e68322cc154124da704074295315325cd7647a89fb88`
- `spike/t111/label_protocol.py`: `sha256:bcafdee6cab0b4ddd98ddbd56896546a47abe2c164be955ace28542686f329ee`
- `spike/t111/label_adjudication.py`: `sha256:be1724b08b79e6877167dd3ebf99630a90cc18781e2aa83bb92e58fac42cf231`
- `spike/t111/reviewer_kits.py`: `sha256:64289ec04c295a939d62ae5770323aba134c04498bb72df45a0d2178e17c2ab7`
- `spike/t111/labeling/gate2-v2/stage1_snapshot.py`: `sha256:487dcc78f33ba4e08626b35d9500e78eb66276d48b984393f36bccd6636779a1`
- `spike/t111/labeling/gate2-v2/stage0/STAGE0.md`: `sha256:11e8faef6a5dfd671fb25bd7414c6380e31ec4e41f990b5d2ff2c2628704e1a6`
- `spike/t111/labeling/gate2-v2/stage0/snapshot-query.graphql`: `sha256:8e9f76872c955e0bad76dfde432e846fbc7c340dfd23bba7a67fda14a55d897b`
- `spike/t111/labeling/gate2-v2/stage0/snapshot-constants.json`: `sha256:5908318e1c1b25d59bf0d78f5b4027b50bb52e28d4ff0f529486c75d4380dc76`
- `spike/t111/labeling/gate2-v2/stage1/receipt.json`: `sha256:bbea9b7cae0189ed0a94ea58657c1ac229be245be653196711c2e2f73d8040ef`
- `spike/t111/labeling/gate2-v2/stage1/response.json`: `sha256:85cb9c6f0589afc6c00468e13eb20e82d45a3430135e0a9ea0fcb334453aa20e`
- `spike/t111/labeling/gate2-v2/stage2-enumeration-authorization.json`: `sha256:3c756c24d507028c5346d69caa03c333c08db024e4e4626b6a7b1f818b23e385`
- `spike/t111/labeling/gate2-v2/stage2-enumeration-receipt-review-r1.md`: `sha256:a44746d6b3b904d0be824775cb9aab4cc5bb48449b8e347bbfdb26c21d2a0e17`

Historical r2 acceptance landed at
`105eaeb309edf59fa4c8b494cc15f135005f344b`; the candidate must be reviewed
as the complete `105eaeb..candidate` preparation diff, without changing any
sealed dependency above.

## Accepted enumeration inputs to bind later

Enum-02 was independently accepted at
`0d736c8209160a36edb0948d4f72ccb4820c10fd`. The future Finding-4 worksheet,
which does not exist yet, must bind these already-produced artifacts without
copying or disclosing their coordinate contents:

- enumeration receipt: `sha256:5d2b7a01529691fb3f75b1b55a5f52fdae1ffdad970b44c53e5bc21114554546`
- terminal receipt: `sha256:ad9aee01c7a024b6d4c21fbf597b8a41a4e4e95bcf234bb60a919feffaf08a5f`
- enum-02 `cardinalities.json` digest:
  `sha256:db2f702f14f8bf62534d0c4eb0abb9b5d83e8ddbb0431b45b4e836739d5baf90`
- enum-02 `frame-membership.json` digest:
  `sha256:9bdabf9567d2732db23444d55072ee8503dd6ebb2838818e4d80a621f0bb4b3e`

## R8 review requirements

1. **Diff ground truth and retained properties.** Recompute both candidate
   digests and review the complete diff from `105eaeb`. Reconfirm receipt/head
   admission, the 0/1/2/3/4 exit map, no output before fallible computation,
   and atomic two-file directory publication. R1–R3 remain inherited only
   where the new diff leaves their behavior intact.
2. **Sealed prior-lineage rule.** Independently recompute the tracked
   `expansion-lineage.json` file digest and its v2 domain-separated binding.
   Confirm it binds the exact ledger digest above and fixes the Stage-1
   fixtures' old side to: Dapr
   `08aebd8b2effa2ed939ad5531e25ff8b21a36ef1`; Loki
   `1362d2770ee2abba5e130d67cf30bcc4eefa0da0`; online-boutique
   `9a4616e77f0f9cbcbecaf27d711c38890dda1404`; Temporal
   `8224a5375112079ad905c4ea829420306431462c`. Verify that sealed mode rejects
   any other old commit or mirror path and, before ledger parsing, requires a
   non-shallow repository containing both commits with old→new ancestry.
   Admission and mapping must share `/usr/bin/git`; a different PATH-resolved
   executable or repository/object/config-affecting `GIT_*` override refuses.
3. **Population derivation.** Confirm the exact four-frame cardinality and
   membership envelope, unique canonical `system:path:line:line` keys, zero
   enumeration census, aggregate population equal to membership length, and
   each precision frame's four derived fixture populations summing exactly
   to its aggregate population.
4. **No inert design fields.** Confirm that the design object has exactly five
   consumed rules, each with exactly `p` and `threshold`: the four aggregate
   frames plus `per_fixture_precision`. Review the future design values against
   §5/§10: precision `995/1000`→`98/100`, recall
   `95/100`→`90/100`, and per-fixture precision
   `97/100`→`90/100`. Missing, extra, malformed, or out-of-domain fields must
   refuse rather than be ignored.
5. **Burn and power arithmetic.** Confirm that mapped and unmapped burns retain
   the accepted burn-on-doubt behavior; precision burns reconcile exactly
   between aggregate and fixture views; net fixture populations reconcile to
   aggregate net populations; and the sealed `minimal_n` implementation runs
   for four aggregate cells plus both precision families × four fixtures.
   `all_frames_feasible` and exit 0 must require all twelve cells; any one
   infeasible cell publishes the auditable v3 result and exits 1.
6. **Selection contract boundary.** Confirm the v3 artifact exposes every
   aggregate and fixture minimum. A later selector must satisfy every fixture
   minimum and then top up as needed to satisfy its aggregate frame minimum;
   consuming only the aggregate minimum is forbidden. R8 authorizes no
   selection implementation or execution.
7. **Enumeration chain and non-disclosure.** Reverify enum-02 authorization,
   terminal, receipt, accepted site-counted populations, and strictly later
   independent review. Hash-only inspection is sufficient for membership;
   no coordinate may enter the review record.
8. **Independent execution evidence.** Run
   `/usr/bin/python3 -I -S -B spike/t111/labeling/gate2-v2/stage2_prepare_test.py`
   under Python 3.9.6. The implementer reports 19/19 passing, including both
   precision families' fixture gates, population-sum refusal, ignored-design
   refusal, sealed-old-commit refusal, non-shallow/ancestry checks, lineage and
   ledger digest refusals, Git identity/environment refusal, distinct crash
   exit, and atomic publication fault injection. Do not run preparation against
   the sealed inputs.

## Acceptance choreography

If and only if every check passes, a later independent-review commit must
replace the verdict above with exactly one `**Verdict: ACCEPT.**` line, retain
exact path/digest binding lines for the accepted closure, and add the sole
strict `GATE2-V2 Stage-2 preparation verifier, r3` PLAN anchor. Only after that
acceptance may a separate worksheet commit bind canonical heads/design inputs
and a fresh output namespace. The strict preparation-authorization PLAN marker
must remain absent until a later reviewed promotion. `gate_status` remains
`PENDING` throughout.
