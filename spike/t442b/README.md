# T44.2b · Truthful key-copy recovery

Settings announces copy success only after the browser clipboard promise
resolves. Missing, rejected and throwing clipboard calls preserve the token in
a labeled read-only house input and announce a manual-copy instruction. The
44px action is disabled during a pending write. Its token-keyed component keeps
old completions from marking a replacement token copied. The new-key card and
Appearance reload action explain copying a one-time token before leaving or
reloading. Reload still clears unfinished Settings state; no token is persisted.

Product/build checkpoint: `ad6cbe082f43ce7cab89a27cf18d59feb46ece15`, based on
`f26df0bf4922a722e7ceeecd6cb56229ee70eda6`. The later test-only checkpoint
`154fc1aa6156709e853cc81ea083f2feb30da608` changes native `Meta+a` to portable
`ControlOrMeta+a`. Installed Playwright maps these identically on Darwin;
Assessment B verifies the portable spelling separately. Product source, 118
production inputs and 165 built files remain identical across that test-only
change. [The binding](portable-test-binding.json) preserves both test hashes and
[the exact measured spec](runner/measured-spec.ts); [the plan](plan.json) retains
its original measured source/tool/canonical-baseline hashes.

The old-source regression fails as expected on missing failure feedback. Its
exact test snapshot is retained in `checks/baseline-regression.test.tsx`.
The initial build rejects an unsupported house-input `spellCheck` prop; that prop
is removed. Corrected candidate gates pass 373 tests across Settings, shared
copy controls, palettes and contrast, with zero skips, plus receipt types/lint
and production build. The portable test-only correction passes receipt types
and lint again. First failures and final command/result logs remain separate.

Eight selected native author cases and eight fresh cases pass in 42.995365s
and 22.723108s including clean owned-group teardown, with no retries. Four
clipboard flows cover both themes at 1280/390px: keyboard creation/copy,
missing/rejected access, full-token keyboard selection, disabled pending state
and deferred success. Four existing preview-failure flows cover the inline
reminder and same-URL/palette keyboard reload recovery. Each deliberately
produces one checked highlight-chunk HTTP503 error; other page/console/API
errors fail. The key-copy journeys have clean consoles.

[All 12 screenshot pairs](comparison.json) are byte-identical at the unchanged
0.001 ratio/0.2 comparator threshold, with no masks. Twenty-four capture-state
axe 4.12.1 audits have zero violations and incomplete checks. New frames are
clipboard failure/success and the explicit preview reminder; no historical
canonical PNG is rewritten. The default Appearance project gains four cases
(18 total); only the eight affected flows are selected for this bounded run.
An isolated design inspection passes two cases/four frames and clean audits in
9.983449s including joins, with every frame matching its author hash.

Independent source/cost review has no blocker. The [dual-agent critique](critique.md)
scores this narrow scope 37/40 with zero priority issues; the initial keyboard
recovery concern is corrected before capture. One CLI detector invocation finds
zero issues (the original tool result is recorded in B JSON; separate raw CLI
stdout/stderr files were not retained). [Assessment B](assessment-b/result.json) injects in three fresh
headless contexts, verifies portable full-token selection and keyboard retry,
and joins all owned groups cleanly in 13.837457s. There is no visible-overlay claim.

[Bundle accounting](bundle.json) is +792 raw/+237 individually deterministic-gzip
JavaScript bytes; Settings +792/+260 and initial entry +0/-3, no new dependency
or chunk. The substituted deferred clipboard fixture settles in 31–68 ms including
test interaction/resolution; this establishes no OS clipboard latency or SLO.
Steady-state query/request, sync, startup/restart, retry/no-op and publication
costs are unchanged. A displayed key has constant-size copy state; an explicit
copy performs at most one clipboard call and two state updates. No extra API,
timer, retry, lock, corpus read, store/schema, disk, persistent cache, Worker or
child is added. An unresolved browser promise can retain its closure/token
until settlement; the browser provides no cancellation guarantee here.

[Outcome](outcome.json) and [receipt](../../ui/receipts/t442b.json) index the raw
evidence. `author/`, `fresh/`, `assessment-a/` and `checks/` mirror outside QA
roots recorded in their reports; duplicate attachment copies are omitted.
Original absolute paths identify executed roots; replay requires explicit
root/output rebinding. `runner/` redirects imports to owned CDP contexts and
inserts scoped axe audits before four screenshot call sites, preserving behavior
assertions. Each main pass has a 180s ceiling including a 30s cleanup reserve,
one worker, zero retries and first-failure stop. The independent A pass has a 90s
ceiling. All recorded main/A Vite, Chrome and client groups join cleanly; private
browser profiles are removed. Raw logs are archived losslessly as deterministic
gzip with decoded hashes in `raw-log-archives.json`.

Qualification is installed Darwin arm64 Chrome 155.0.8059.82 with synthetic key
material and substituted browser clipboard APIs. It does not verify host OS
clipboard contents/permission dialogs, real backend authentication, screen-reader
speech, a fresh full canonical/Noble matrix, Linux #124, signing or release.
Production signing remains pending.
