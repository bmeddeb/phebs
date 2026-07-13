# T11.1 benchmark artifacts

The committed `sites*.jsonl`, `g34.*.jsonl`, and `g3h2.*.jsonl` files are
legacy v1 artifacts. They are retained only as disclosed development history:

- Gate 2 used an enriched case-control sample and cannot estimate eligible-
  population recall.
- Gate 3/4 site IDs collide and the sample does not prove the ticket's Gate 3
  end-to-end or Gate 4 recall/lineage criteria.
- Every legacy holdout coordinate is burned. It may not enter a replacement
  dev or holdout sample, even if its span, predicate, or asserted value changes.

Burning preserves blindness; it does not remove a unit from the ticket's AC
population. On these same pinned revisions, a diagnostic may exclude burned
coordinates but no gate may pass by dropping them from numerator and
denominator. A replacement gate must use refreshed locked revisions or a
reviewed estimator that incorporates the prior labeled outcomes.

Do not cite the legacy scores as gate results and do not silently migrate the
old labels into a new holdout.

Replacement generators write coordinate-only labeler artifacts. Source
context is materialized locally under the ignored `spike/t111/out/` tree.
Each scored generation must bind the exact corpus commits, extractor and
configuration, input fact files, generator, population, sample, split, key,
and labels. Missing or mismatched provenance means `NOT ESTABLISHED`.
