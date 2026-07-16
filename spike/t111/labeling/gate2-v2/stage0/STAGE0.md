# GATE2-V2 Stage 0 — sealed tooling and feasibility record

Protocol: `spike/t111/labeling/GATE2-V2.md` revision 3
(`sha256:f9d7eb8682c9d9284c5d6418f458835c6df43530222d00d4450a87765d18ca65`),
approved 2026-07-16 (PLAN.md §12 decision). Per that approval, **this stage
authorizes nothing beyond itself**: Stage 1 requires a separate dated
acceptance of these artifacts.

Implementer: the session agent (reviewer role for these artifacts passes to
the second agent, preserving implementer ≠ reviewer).

## 0. Stops and root causes preceding this record

Stage 0 stopped fail-closed four times before any artifact was sealed; all
four causes are structural lessons, none consumed anything:

1. **Cross-ceremony source contamination.** Commit `5eaf5f2` (frozen-label
   repair) had downgraded `label_prep.py`, `gate34_common.py`,
   `corpus.lock.json`, and the root `go.sum` to historical versions for the
   terminated estimator path; the producer's bound-manifest self-check
   refused the mixed tree. Repaired by revert (commit on this branch);
   lesson: terminated-ceremony repairs must be reverted when the path ends.
2. **Toolchain auto-resolution.** A rebuild under `GOTOOLCHAIN=auto`
   permitted shimming toward the cached go1.26.4 (the Attempt-3 killer).
   All producer operations now force `GOTOOLCHAIN=local` with
   `GOROOT=/opt/homebrew/Cellar/go/1.26.5/libexec` and
   `PATH=/opt/homebrew/Cellar/go/1.26.5/bin:/usr/bin:/bin` — sealed here as
   REQUIRED build-recipe constants.
3. **Harness-environment traps.** `timeout` does not exist in the pinned
   PATH (instant exit 127 reads as a silent kill); `t111` resolves
   `corpus.lock.json` relative to the repo root; the loki corpus checkout
   carried 555 lines of `go.sum` contamination from the terminated closure
   work (restored pristine); hydrate's final phase is minutes of silent
   seal-and-rebuild that impatient wrappers had been killing.
4. **Producer defect: unsealed cache at hydrate exit.** hydrate's final
   `buildBoundHarnesses` phase wrote the module cache after the corpus
   loop's deferred reseal, so every hydrate exited with `gomodcache` at
   0755 and the producer's sealed-cache admission refused all later runs.
   Fixed in commit `9429ab92` (3 lines, `spike/t111/main.go`: reseal after
   the bound build). **This fix is producer source and part of the
   candidate identity; the reviewing agent must accept it.**

## 1. Candidate producer identity

From `.module-cache/manifest.json` after the post-fix hydrate (exit 0,
cache sealed `dr-xr-xr-x` at rest):

- `source_head`: `9429ab92e7831565606d718af47073ad996a1d01` (= branch HEAD
  at build; later Stage-0 commits add artifacts only, no producer source)
- `source_clean`: `true`
- Go: `go version go1.26.5 darwin/arm64`,
  `go_sha256: sha256:3f947495f00cb7f8088a5cfd694da8dc43869b33f5e7377b048fb18922ffb7e0`
- artifacts: `t111`, `typedcalloracle` (fresh binary digests in the
  manifest itself, which is the binding record)
- build recipe constants: `GOTOOLCHAIN=local`, absolute `GOROOT`/`PATH`
  above, `GOPROXY=off GOFLAGS=-mod=readonly` for offline rebuilds.

## 2. Closure declarations and two-run fact proofs (package 1)

The declared module closure record is `spike/t111/corpus.lock.json`
(digest in the code-path inventory) plus the sealed module cache; hydrate
reports every corpus hydrated **network-free** from that declaration. The
determinism proof regenerates each fixture's facts twice with the bound
producer at the current corpus heads:

- `closure-proof-runs.tsv` — per-run `facts.jsonl` digests for temporal,
  dapr, loki, online-boutique; the acceptance criterion is run1 == run2
  for every fixture (verified in §6).

**Recorded finding (out of Gate-2 scope):** `t111 identity -system loki`
fails at the current loki head parsing
`operator/hack/large-rules-infra.yaml` (service-identity manifest scan).
Identity artifacts feed Gate 3, not Gate 2; flagged for the Gate-3 owner,
not repaired here.

## 3. Advisory power analysis (package 2)

`power_advisory.py` imports `lower_population_total` /
`hypergeom_probability` **from the sealed scorer** — the import is the
mechanical transcription §5 requires. Family partition: four estimand
families ({client_call, registration} × {precision, recall}),
`alpha_each = 1/80`, matching the sealed commitment's `family_size = 4`.
Output: `power-advisory.json` (ADVISORY; Stage-2 exact is sole authority).

Headline proxy results (Attempt-3 cardinalities; per-fixture proxies from
the sealed prefix-2 typed-call counts, zero census help assumed):

| Frame | population | census | minimal n | note |
|---|---|---|---|---|
| client-call precision | 5383 | 2690 | **267** | |
| client-call recall | 5463 | 2770 | **116** | |
| registration precision | 127 | 127 | 0 | full census, exact |
| registration recall | 281 | 281 | 0 | full census, exact |
| per-fixture: temporal | 3825 | 0 | 151 | |
| per-fixture: dapr | 1447 | 0 | 148 | |
| per-fixture: loki | 177 | 0 | 99 | |
| per-fixture: online-boutique | 19 | 0 | 19 | full census, exact |

Every frame is feasible at the design points; the worst-case labeling
effort is on the order of ~700 sites (vs Attempt 3's 5,744). The §5
label-mass floors (200 positive / 100 hard-negative recall units) bind at
Stage 2 against actual cardinalities.

## 4. Carry-forward mapper (package 3)

`carry_forward.py` — burn-on-doubt per §6: identical-normalized burns;
traced correspondence burns (including git rename tracking, which must run
un-pathspec'd or renames misreport as deletions); uncertainty burns by
default; only a positively-absent site (deleted file, no traced successor)
is free. `carry_forward_test.py`: five synthetic-fixture tests, all green,
including the uncertain-defaults-to-burn branch.

## 5. Snapshot package (package 4)

`snapshot-query.graphql` (four fixtures, one non-paginated request) and
`snapshot-constants.json` (proposed cutoff `2026-07-20T16:00:00Z`,
`max_clock_skew_seconds: 120`, no-retry rule). **Not fired.**

## 6. Two-run proof results

run1 == run2 for every fixture — **all four proofs pass**:

| Fixture | facts.jsonl digest (both runs) |
|---|---|
| temporal | `sha256:1dbb603b23bbf16dd4f4a79b67dde900138990a93e98d0d6d7ff90e2668a0ed8` |
| dapr | `sha256:51dc2db1fb81f05b69b5a0a316b73a9923e0728b74701b47d490b6cee5faf19b` |
| loki | `sha256:d4eb731ef1fb0f99ebbf9b25e7e2553f9edd107891977112fa97919c78879f61` |
| online-boutique | `sha256:aeb5f9538b639793831c0282b977a247427a327eae70971423d9d2eba7915034` |

loki — the fixture whose sealed facts were unreproducible under the first
protocol — now reproduces byte-identically from its declared closure: the
§4 admission rule is demonstrated, not asserted.

## 7. Code-path digest inventory (package 5)

`code-path-inventory.tsv` — byte digests at the recording HEAD for every
population/ceremony-shaping code path (§4 list) plus the Stage-0 artifacts
themselves.

## 8. Complete artifact manifest

Every Stage-0 artifact by byte digest, recursively. `fixtures/` holds
the preserved run-1/run-2 fact evidence; `power-advisory.err` is the
power run's empty stderr record (zero errors) and is retained as
evidence. `STAGE0.md` itself is excluded by rule: the record's own
bytes are bound by the sealing git commit object, not by a
self-referential hash.

| Artifact | sha256 |
|---|---|
| `carry_forward.py` | `sha256:2bb3278fc086b8ce17dcb818959bdac63949112420622426499085882f58c589` |
| `carry_forward_test.py` | `sha256:68b8de20b3a79d07e560560216e44c7b5039a1720f70f5e5a5f0b03e9f48bd9c` |
| `closure-proof-runs.tsv` | `sha256:99ade5439d1f10823ca2965beafec5db3d90bbe59d206f589e40bdd2dc2c00a5` |
| `code-path-inventory.tsv` | `sha256:a5d8e5635f57585b60ad9692dd41334d19661a8ca068f20a31ecad022327441e` |
| `fixtures/run1/dapr.facts.jsonl` | `sha256:51dc2db1fb81f05b69b5a0a316b73a9923e0728b74701b47d490b6cee5faf19b` |
| `fixtures/run1/loki.facts.jsonl` | `sha256:d4eb731ef1fb0f99ebbf9b25e7e2553f9edd107891977112fa97919c78879f61` |
| `fixtures/run1/online-boutique.facts.jsonl` | `sha256:aeb5f9538b639793831c0282b977a247427a327eae70971423d9d2eba7915034` |
| `fixtures/run1/temporal.facts.jsonl` | `sha256:1dbb603b23bbf16dd4f4a79b67dde900138990a93e98d0d6d7ff90e2668a0ed8` |
| `fixtures/run2/dapr.facts.jsonl` | `sha256:51dc2db1fb81f05b69b5a0a316b73a9923e0728b74701b47d490b6cee5faf19b` |
| `fixtures/run2/loki.facts.jsonl` | `sha256:d4eb731ef1fb0f99ebbf9b25e7e2553f9edd107891977112fa97919c78879f61` |
| `fixtures/run2/online-boutique.facts.jsonl` | `sha256:aeb5f9538b639793831c0282b977a247427a327eae70971423d9d2eba7915034` |
| `fixtures/run2/temporal.facts.jsonl` | `sha256:1dbb603b23bbf16dd4f4a79b67dde900138990a93e98d0d6d7ff90e2668a0ed8` |
| `power-advisory.err` | `sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `power-advisory.json` | `sha256:1e623533f308b7017dd672c5f3c8b97ea101b01da5a2699128cdf245196a62e1` |
| `power_advisory.py` | `sha256:8f59dd8e2256419a299fb61992e912b29582a7d946ffc909572ce674ea9d66c2` |
| `snapshot-constants.json` | `sha256:5908318e1c1b25d59bf0d78f5b4027b50bb52e28d4ff0f529486c75d4380dc76` |
| `snapshot-query.graphql` | `sha256:8e9f76872c955e0bad76dfde432e846fbc7c340dfd23bba7a67fda14a55d897b` |
