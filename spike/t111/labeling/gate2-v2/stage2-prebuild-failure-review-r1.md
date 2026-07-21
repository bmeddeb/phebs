# GATE2-V2 Stage-2 P0 failure review r1 — authorization t111-gate2-v2-p0-f47490d-01

**Verdict: FAILURE ROOT-CAUSED — two defects (one fired, one latent), three
remediation classes. P0-01 is terminally consumed; no retry under it. The
executor's fail-closed behavior was correct throughout.**

Reviewer: Claude (independent of the executor implementation and of both
launch attempts). All diagnostics ran read-only against the source corpus or
in session scratch paths; no ceremony, derived-root, hydration, extraction,
enumeration, selection, or disclosure operation was performed.

## Bindings

- Authorization: `t111-gate2-v2-p0-f47490d-01`
  `sha256:2ea8965ce408628c8181bc127cd03209fcec8bcbf6c89fbac0c74c63db11cc12`
- Terminal receipt: `~/.local/share/t111-gate2-v2-p0-f47490d-01-ceremony/terminal.json`
  `sha256:9968917dcdd341b7fd71…` — status ABORTED, failure REFUSED, exit 2,
  `evidence_receipt_sha256: null`, consumption marker
  `sha256:832c19f480c1422f5109375d90155a8c286e261ed42775b1dcbdf8e92ed7f7d0`
- Executor bytes at failure: `stage2_prebuild_execute.py`
  `sha256:27c14deeb715f40ee29533f836edfa514c6d2eb781a932c5773e2519b84d6133`
  (unchanged from the r2-accepted digest; the failure is not a byte drift)

## Timeline

1. Launch 1 (implementer harness, detached): the child died before Python
   userland initialized — empty stdout/stderr, no ceremony directory, no
   marker. Nothing consumed. The bound runtime was separately proven healthy
   and digest-matching, isolating the kill to the launch sandbox.
2. Launch 2 (operator terminal): consumed the authorization, completed
   source clone and the derived-lock rewrite (`sha256:d02cd5ef…`), then
   REFUSED at the first corpus step — Temporal local `git bundle create` —
   and sealed a terminal receipt with no evidence receipt. Correct
   fail-closed behavior.

## Finding F1 (fired): the sealed bundle recipe is unexecutable

The admission contract fixes `bundle_argv` to
`git -C <source> bundle create <bundle> <commit>` with a raw commit OID as
the sole revision. Git categorically refuses this shape: a raw SHA names no
ref, the bundle would contain no refs, and git exits
`fatal: Refusing to create empty bundle.` Reproduced byte-exactly (a) in the
temporal source corpus with the sealed head and (b) in a full-history
synthetic repository. The defect is universal — it fires for every fixture,
on any repository state, in any environment. Five review rounds (including
this reviewer's) verified sealed argv **shape equality** against the
authorization but never **executability** against a real git.

## Finding F2 (latent): all four corpus sources are shallow, and shallow
bundles lie

`--is-shallow-repository` is true for temporal, dapr, loki, and
online-boutique, with shallow boundaries strictly below the sealed Stage-1
heads — the depth-limited pre-flight fetch brought the heads in but not
their full history. Synthetic proof: with a ref-bearing recipe a shallow
source **successfully** creates a bundle that `git bundle verify` declares
"records a complete history", yet fetching from it fails connectivity
(`did not send all necessary objects`). Had F1 been the only fix, P0 would
have failed one step later — or worse, produced derived corpora that pass
bundle verification while missing ancestry that carry-forward classification
requires. `git bundle verify` must never be an acceptance oracle.

## Finding F3 (process): the decisive stderr was discarded

`_run_quiet` deliberately drops step stderr; the terminal receipt records
only `authorized local bundle creation failed`. The decisive one-line git
fatal had to be re-derived by reproduction. Durable refusal diagnostics are
a review requirement, not a convenience.

## Remediation requirements binding any successor authorization (P0-02)

- **R1 — ref-bearing bundle recipe.** Create `refs/gate2-v2/<fixture>` in
  the source (additive, namespaced) before bundling by that ref, or an
  equivalent ref-carrying spelling. The destination fetch's connectivity
  check remains the sole completeness arbiter.
- **R2 — unshallow the sources first.** All four corpus repositories must be
  unshallowed (mirror maintenance, before authorization), and admission must
  gain preconditions: source `--is-shallow-repository == false` and
  old-head→sealed-head ancestry present.
- **R3 — durable refusal diagnostics.** Terminal receipts capture bounded,
  sanitized stderr of the failing step.
- **R4 — executability review.** Every sealed argv class (bundle, init,
  fetch, checkout) must be executed against a real git in an isolated
  synthetic fixture as part of independent review; shape equality alone is
  insufficient. This is a standing review-method change.
- **R5 — fresh authority.** A new authorization ID and digest set; P0-01
  remains terminal history and its ceremony directory is preserved evidence.

No retry, no enumeration, no Stage-2 preparation is authorized by this
record. `gate_status` remains `PENDING`.
