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

## T47.2f rebinding review — 2026-10-10

Reviewer: independent read-only Codex agent `t472_independent_review`.
Exact reviewed commit: `8fcb532f` (the rebinding itself; the following test
and record commits hold every preparation byte exact).
Critical 0, high 0, medium 0, low 0. No actionable finding remains.

### Result

The rebinding claim verifies end to end. The census's single etcd
`derived_commit` line was reconciled at `f9ecd8dc…` (1,105 Go-suffixed paths,
1,094 mode-100644, and the 11 nonregular mode-120000 objects exactly equal to
the 11 `nonregular_go_paths` census entries; totals 7,696 / 7,685 unchanged).
The committed plan and bundle at the parent `d9646ef8` bound `75771dd7…`
(plan) and the pre-slice-A lock digest `e3874493…` while the committed lock
already hashed to `8c534042…`, so `binding_bundle.build_bundle`'s provenance
and commit predicates refused at the parent; at `8fcb532f` the plan binds
`8c534042…` (actual lock) and the bundle binds `0c60125b…` (actual plan).

No stale identity remains: zero `75771dd7` occurrences in all 11 artifact
files including decompressed gzip members; every etcd row carries
`f9ecd8dc…`. Counts are exact: 73,039 etcd sites, 499,771 total over 11
strata, recall 498,793, attribution 74 (8 etcd units) in 4 strata, excluded
978, declarations 60 files / 390 operations / 44 mapped / 16 abstained. Old
vs. new normalization shows zero non-commit line diffs in the sites, recall,
attribution and declarations files. Untouched members are byte-identical
parent vs. HEAD (blobs `c582474c…`, `33041761…`, `853f4b4b…`) with unchanged
bound digests. Every named digest in the plan, universe summary and bundle
equals the hash of its target file; the universe summary's
`roles_digest_files` inventory hashes are the only additional digests and the
empty-role invariant holds (`e3b0c442…` empty input). Eight candidate
identities remain null, 25 missing paths are named, `corpus_derived_commits`
is sorted `[e0f9f8ac…, f4034785…, f9ecd8dc…]`, and no known digest record is
null.

### Independent verification

- `git show --stat 8fcb532f`: exactly 8 `spike/t472/` files; no Go, docs or
  production change.
- Two complete regenerations (rc=0, bundle `missing=25`) reproduced all 5
  universe, 4 frames and the bundle files byte-for-byte against the checkout
  and against each other.
- The parent-state freshness/commits refusal was reproduced from `git show`
  bytes.
- 46 module tests and 4 derivation tests passed at the reviewed commit; the
  follow-up commit adds the committed-rebuild pin (47 at final HEAD).
- `make docs-check`, `make verify-glossary`, `git diff --check` and
  `go test`/`go vet ./spike/t472/gosites` passed. The tracked tree stayed
  clean before and after every run.

### Cost and limits

Cost remains offline: the rebinding re-ran the three serial generators and
adds one committing test that rehashes the committed preparation. No
production query/request, sync, startup/restart, retry/no-op, publication,
lock, cache or schema work.

Not checked: re-derivation of the etcd derived commit itself (slice A is
taken as given); independent reimplementation of the classifier/extraction
(relying on generator determinism, digests and unit tests); full repository
suite, hosted CI, candidate pipeline and the `/home/ben/phebs` checkout.

Verdict: rebinding review passes. A test/record-only commit trail requires
the final-HEAD preservation check before push. No human source judgment,
real score, seal, merge or release is established.
