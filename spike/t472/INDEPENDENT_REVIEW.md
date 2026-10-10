# T47.2b fresh independent source review — 2026-10-10

Reviewer: independent read-only Codex agent `t472_independent_review`.
Exact source commit: `1b9afefcfa47f94d49e87cbd64eb5192d7b1f6df`.
Critical 0, high 0, medium 0, low 0. No actionable finding remains.

## Result

This change preserves every supported unresolved operation/reason alternative
at one source sampling site and scores the complete alternative set against
independent truth. It retains the conflict checks that prevent contradictory
claims from disappearing.

The connected ambiguity finding is closed end to end. A synthetic
three-operation case with different reasons produces identical output under
all six assertion orders and remains one sampling unit. Full alternative sets
score correctly; missing, extra and incorrectly paired alternatives score
wrong. Same-operation lineage/reason conflicts, wrong roles, conflicting
resolved operations and resolved/unresolved mixtures still refuse before
harness deduplication.

All nine original corrections remain closed: canonical citation joins; full
eligible call population; independent unresolved truth; full-sheet uncertainty
and complete samples; operation/lineage edge metrics; individual interface
coordinates and role strata; production dialect adaptation; per-stratum
processing gates; and structural/digest/byte validation.

## Independent verification

- 46 source/frame/scoring/bundle tests and 3 derivation tests passed.
- Additional synthetic ordering, completeness and conflict probes passed.
- The uncached production ambiguity fixture passed.
- Documentation, glossary and whitespace gates passed.
- Fresh generation reproduced all 4 frame files and the bundle byte-for-byte.
- Actual protocol, parameters, census, lock and seven machinery digests match.

Universe and frame population bytes, approved parameters, corpus/census,
frozen machinery, production code and Go build inputs remain unchanged.
The tracked tree stayed clean. No population narrowing or threshold change
was found.

## Cost and limits

Cost remains offline: alternative validation, collection and deterministic
ordering retain memory proportional to alternatives. They add no source read
or child and no production request, sync, startup/restart, retry/no-op,
publication, lock, cache or schema work.

Verdict: preparation source review passes. A review-record-only documentation
commit requires a final-HEAD preservation check.

Not rerun: unchanged full Go gates, repository/store, live extraction,
operating or non-Linux gates. No human source judgment, real score, seal,
merge or release is established.
