# T58.1 Go gRPC consumer/registration site-coverage round

Protocol: [`docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md`](../../docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md).

**Nothing here is sealed and no measurement has run.** The exporter refuses to
start without a seal file binding the exact population digest, so the protocol's
rule that `export` must not touch the population before the seal is enforced by
the tool rather than by a comment.

## Layout

| Path | Owner | Role |
|---|---|---|
| `lock_corpus.py` | lead | clones the frozen §2 list at depth 1, applies the one pre-declared drop rule, writes `corpus.lock.json` |
| `corpus.lock.json` | lead | 13 members / 5 drops at exact commits, plus host facts |
| `enumerate_population.py` | lead | lists each unit's pinned `.go` tree into `population/units.jsonl.gz` |
| `population/` | lead | the enumerated units and their digests |
| `oracle/` | **separate author** | the independent truth, written from §1 and §3 alone |
| `export/main.go` | lead | runs the in-tree extractor over one whole unit tree |
| `score.py`, `test_score.py` | lead | joins on spans and applies the frozen §6 bars |

`oracle/` is authored by a different session that is forbidden from reading
`internal/extract/extractors/grpcgo`. The lead does not write it and does not
answer its clarifying questions, because answering from implementation knowledge
would rebuild the coupling the separation exists to prevent. The guarantee is
procedural, not technical — see the protocol's §3.

## Sequence

```bash
# 1. Lock the frozen population (already done; 13 members, 5 drops).
python3 spike/t581/lock_corpus.py

# 2. Enumerate units. Reads no Go file's contents; runs no extractor.
python3 spike/t581/enumerate_population.py
#    -> population/units.jsonl.gz, population/population.json
#    -> units_sha256 = sha256:95cc4cb4ca92dcd72551c40ccd6556d059ee5a9196ccca46e9112ac2b5ef5c62

# 3. Oracle pre-seal pass (separate author). Publishes the state table and the
#    scored-site count in protocol §3. If scored sites fall below the §6 floor
#    the round stops there under §2's stop rule.
go build -tags t581oracle -o /tmp/t581oracle ./spike/t581/oracle
/tmp/t581oracle -units spike/t581/population/units.jsonl.gz \
                -corpus ~/phebs-t581-corpus > sites.jsonl

# 4. Ben seals: publish the commit, the population digest and the tool digests
#    in a public gist, and write round/seal.json (shape below).

# 5. Only now may the extractor touch the population.
go build -tags t581export -o /tmp/t581export ./spike/t581/export
/tmp/t581export -units spike/t581/population/units.jsonl.gz -gunzip \
                -corpus ~/phebs-t581-corpus -seal spike/t581/round/seal.json \
                > candidate.jsonl

# 6. Score, then adjudicate every disagreement by reading the source (§4).
python3 spike/t581/score.py --sites sites.jsonl --candidate candidate.jsonl \
        [--adjudications adjudications.json] --out spike/t581/round/score.json
```

Tests: `go test -tags t581oracle ./spike/t581/oracle` and
`cd spike/t581 && python3 -m unittest test_score` (22 cases).

## `round/seal.json`

```json
{
  "schema": "t581-seal-v1",
  "units_sha256": "sha256:…",
  "protocol_sha256": "sha256:…",
  "sealed_utc": "YYYY-MM-DD",
  "gist": "https://gist.github.com/…"
}
```

The exporter recomputes the digest of the units file it was given and refuses if
it differs from `units_sha256`, so a population that moved after sealing cannot
be scored against a stale seal. Unknown fields are rejected.

## Records

| Stream | Schema | Grain |
|---|---|---|
| `population/units.jsonl.gz` | `t581-unit-v1` | one repository at one pinned commit, with its complete `.go` file set |
| oracle `sites.jsonl` | `t581-site-v1` | one candidate site |
| oracle `sites.jsonl` | `t581-out-of-domain-v1` | one **aggregate per unit**, not a row per candidate |
| exporter `candidate.jsonl` | `t581-candidate-v1` | one emitted **fact**, never one assertion |
| `round/score.json` | `t581-score-v1` | metrics, the §6 verdict, detail slices and the standing disclosures |

Exporting one row per fact rather than per assertion is load-bearing:
`ComputeAssertionID` excludes evidence references, so two same-file sites sharing
a claim identity collapse into one stored assertion carrying two atoms, and
`Coverage.UnresolvedCount` counts assertions. Scoring assertion counts would
understate the denominator and silently pass missed sites.

Out-of-domain candidates are aggregated per unit for the mirror-image reason: GD7
plus GD10 make every selector call with at least one argument in every non-stub
file a candidate, so a row each would be millions of records across 53,721 files.
§6 needs the count, and dropping the rows changes no scored outcome — a candidate
at a span the oracle never reported is already spurious. An aggregate is emitted
even at `total: 0`, so the scorer can name a unit whose record is missing.

## Why a unit is a repository, not a file

The extractor is two-pass and repository-scoped (GD6): pass 1 builds the service
and method index from every `*_grpc.pb.go` stub, pass 2 scans every other `.go`
file against it. A single file in isolation has no index, so every candidate in it
would be out of domain and the round would measure nothing. This is the structural
difference from T55.1, whose Kafka units were self-contained blobs, and it is why
the population is one pinned commit per repository rather than a history
enumeration.

## Cost

One unit costs a single `git cat-file --batch` child plus one two-pass extractor
run over that repository's `.go` files, held in memory for the unit and released
before the next. The locked population is 13 units and 53,721 `.go` files
(225 stubs), 1.7 GiB on disk; the largest member is `grafana/loki` at 13,856 files.
Nothing here touches a production path: these are offline one-shot readers with no
request, sync, startup, retry, publication, lock, cache or child cost in phebs.
