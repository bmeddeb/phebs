# T44.2a · Appearance preview recovery

The Settings preview now names a failed lazy language/tokenizer load, keeps its
exact plain specimen, and offers an explicit **Reload page** action. The existing
mode/palette and disposed-effect fences also apply to failure. Browser palette
preferences survive reload; unfinished Settings state and a newly displayed
one-time API token do not. The owning workflow guide says to finish edits and
copy new keys before reloading.

Source checkpoint: `a40a4e6d0149b18c35822821500e8e146f54636f`, based on
`9b3468f00b706bde161e01cb2aa090b979e36108`. Source hashes, all 118 production
inputs, 165 built files and 130 unchanged canonical baseline hashes are bound in
[the plan](plan.json). Later owning documentation/evidence commits do not alter
those source/build bytes.

- The old source fails the new unavailable-state regression as expected. Its
  source is restored in `finally`; the current candidate passes 300 focused
  checks across seven files (144 intentionally excluded by the named filter).
  [Unit commands/results](checks/result.json) retain the expected red and green.
- Receipt types/lint and the production typecheck/build pass;
  [static commands/results](checks/static-result.json) retain exact commands,
  ceilings, log hashes and clean joins.
- Documentation/glossary checks pass. The initial whitespace check finds
  whitespace in exact raw tool output; those four logs are archived as
  deterministic gzip with decoded hashes in `checks/raw-log-archives.json`,
  preserving their bytes. The final staged whitespace check passes.
- Native author 14/14 and fresh 12/12 pass in 25.793828 and 15.551865 seconds,
  including joined teardown. Eight healthy frames cover both widths, themes and
  densities; four unavailable frames cover both themes at 1280/390px. Every
  capture-state axe 4.12.1 audit has zero violations and incomplete checks.
- The four failure journeys inject HTTP503 only for the lazy highlight chunk,
  preserve plain text/current palette, and use actual Tab/type-ahead/Enter to
  reload and recover at the same URL. Each deliberately produces one browser
  resource error; exact expected URL/type/status checks reject other console,
  page or unexpected API errors. This is fault evidence, not a claim of a
  literally empty console during an injected network failure.
- [All 12 author/fresh comparisons](comparison.json) are byte-identical with no
  masks or retries at the existing 0.001 ratio/0.2 comparator threshold. The
  [eight healthy frames also exactly match](healthy-regression.json) the previous
  native Appearance receipt. No canonical baseline is reauthored.
- An independent fresh-context design inspection passes two failure/recovery
  cases in 9.870343 seconds including joined cleanup. Independent source/cost
  review has no blocker. The scoped design score is 34/40; the documented ordinary
  reload state-loss consequence remains a nonblocking concern. The
  [scoped critique](critique.md) records the independent assessments.
- [Independent detector/browser evidence](assessment-b/report.md) finds zero
  CLI issues and completes mutable injection in three fresh headless contexts,
  each reporting no anti-patterns. No visible-browser overlay is claimed. Its
  first Chrome drain check raises `EPERM` after the root exits 0; that stopped
  outcome is retained. A read-only follow-up proves all four owned groups absent
  without a rerun or kill, then removes the private profile.

[Bundle accounting](bundle.json) is +566 raw/+221 individually deterministic-gzip
JavaScript bytes, no new chunks/dependency. Settings is +566/+113, the initial
entry +0/+2. Eight keyboard choices settle in 31–47 ms on this host; these neutral
fixture timings establish mechanics only. Healthy rendering still tokenizes
four fixed lines. Failure adds one bounded local state update and recovery row;
there is no automatic retry, API request, timer, lock, corpus read, persistent
cache, schema/store, sync or publication work. Explicit reload repeats ordinary
application/Settings startup reads.

The [outcome](outcome.json) and [receipt](../../ui/receipts/t442a.json) index raw
reports and custody. `author/`, `fresh/`, `assessment-a/` and `checks/` mirror the
original outside-checkout QA roots recorded in their reports. `runner/` preserves
the harness: imports are redirected to the owned CDP fixture and axe is inserted
before the two screenshot call sites; product behavior assertions are unchanged.
The run uses the existing owned-process cleanup, a 180-second ceiling per main
pass and a 30-second cleanup reserve, one worker and no retries. All Vite, Chrome
and client process groups join cleanly; private browser profiles are removed.
The assessment-A wrapper has a 90-second ceiling. Historical runner absolute paths
identify their executed roots; a replay needs explicit root/output rebinding.

Qualification is installed Darwin arm64 Chrome 155.0.8059.82 with neutral mocked
reader APIs. No Noble renderer equivalence, fresh full canonical matrix, real
backend authentication, live screen-reader speech, Linux #124, validation-label,
release or production-signing result is claimed. Production signing stays pending.
