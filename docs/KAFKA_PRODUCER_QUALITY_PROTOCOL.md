# T55.1 Kafka producer site-coverage quality protocol

Status: **sealed and scored: passes** (2026-10-13). Ben chose the
site-coverage claim and froze the §6 thresholds as proposed. Claude sealed,
adjudicated and scored. The result is in §8.

## 1. Claim card (`phebs.kafka.producer`, site coverage)

**Claim.** In a non-test Go file that imports a round-one library (sarama
under either import path, or segmentio/kafka-go), the `kafka-producer`
extractor reports every producer site of the frozen T23.1 KD4 shapes:

- `ProducerMessage{Topic:}`;
- `Writer{Topic:}` and `WriterConfig{Topic:}`;
- a `Message{Topic:}` passed directly to `WriteMessages`.

Each site gets exactly one row at the exact span of its Topic expression:

- `PRODUCES_TO_TOPIC topic:<literal>` when the topic is a string literal, or
  a same-file const with a literal initializer, within Kafka's naming bounds
  (KD2/KD6);
- otherwise `UNRESOLVED_KAFKA_PRODUCER unresolved:<class>` with the correct
  frozen shape class.

The product value is "where does this code produce to Kafka". It is honest
that production topics are almost always configuration-driven.

**Non-claims.** These are the card's existing non-claims, plus one more:
sites with no Topic key (the topic is chosen per message) are not claimed
and are only reported. In detail:

- No runtime, cluster or broker identity.
- No dataflow or cross-file resolution.
- No franz-go or confluent-kafka-go.
- No completeness of topics.

## 2. Population

The population is real production history: 17 public repositories,
pre-registered by name before any phebs or oracle output was seen. They are
listed in `spike/t551/corpus.lock.json`, which also records 3 candidates
dropped because they no longer import a round-one library at HEAD. The four
T23.1 tuning pins are excluded.

`spike/t551/enumerate_population.py` collects every distinct version (blob)
of an eligible file from first-parent history: **2,790 units**. Units are
read back from full-history bare clones, and their digests are checked.

## 3. Truth

`spike/t551/oracle` is a stdlib-only Go program written from KD1–KD10 and
the card alone. It shares no code with `kafkago` or the T23.1 prototype
recognizer, and was written without reading their recognition code. Its
tests pin every KD shape and class.

Its pre-seal pass, without the extractor, finds 873 sites in 618 units:

| State | Sites |
|---|---|
| Unresolved: selector | 402 |
| Unresolved: identifier | 341 |
| Unresolved: call | 13 |
| Unresolved: other expression | 2 |
| No topic field | 111 |
| Resolved literal | 4 |

That leaves **762 scored sites**.

## 4. Adjudication

Claude adjudicates every disagreement by reading the source. That covers
each missed, duplicated or differently classed site, and each extractor row
at no oracle span. An adjudicated truth replaces the oracle's for that key,
and `["not_a_site", null]` confirms a spurious extractor row.

## 5. Measurement

`spike/t551/export` runs the in-tree `kafka-producer` extractor over each
unit's exact blob, through the `sdk.Corpus` interface the worker uses.
`spike/t551/score.py` joins rows on `(unit, start_byte, end_byte)`.

## 6. Thresholds (frozen by Ben, 2026-10-13)

| Check | Bar |
|---|---|
| Coverage: scored sites with exactly one row | ≥ 99% |
| Class agreement: covered sites with the right state and object or class | ≥ 99% |
| Wrong resolved topics | 0 |
| Confirmed spurious rows per scored site | ≤ 1% |
| Pending after adjudication | 0 |
| Scored sites | ≥ 300 |

## 7. Seal

The commit holding this protocol, the population digest, the oracle, export,
enumerator and scorer digests, and the frozen thresholds are published in a
public gist before `export` runs on the population.

## 8. Result (2026-10-13): passes

**Seal.** Commit `814fc0e2` was pushed to the public repository. The seal
values were published at
<https://gist.github.com/Blandt/78a8678412666759038795eb854f16e9> before the
extractor ran on the population. The receipts are in `spike/t551/round/`.

**Run.** The extractor ran over all 2,790 units in 5.4 s and emitted 762
rows: 4 `PRODUCES_TO_TOPIC` (`topic:important`, `topic:access_log`) and 758
`UNRESOLVED_KAFKA_PRODUCER`. By class, the unresolved rows are:

| Class | Rows |
|---|---|
| selector-expr | 402 |
| unresolved-ident | 341 |
| call-expr | 13 |
| non-literal-expr | 2 |

**Score.** Every check passes:

- 762 scored sites: 762 covered with the right state and object or class;
- 0 missed, 0 duplicated, 0 differently classed;
- 0 extra rows and 0 wrong topics;
- coverage and class agreement are both 100%;
- nothing needed adjudication, so pending is 0.

With zero misses over 762 sites, the 95% upper bound on the per-site miss
rate is about 0.4% (rule of three).

**What it does and does not show.**

- It shows that the extractor finds every KD4 producer site in real
  production code and classifies it correctly. That is the "where does this
  code produce to Kafka" claim.
- It does not show topic precision. Only 4 sites name a literal topic, which
  confirms KD10: production topics are configuration-driven.
- The 111 sites with no Topic key (the topic is chosen per message) are
  outside the claim, and the extractor reports none of them.
- The measurement is at the extractor. The served `find_kafka_topic_usage`
  path and its unresolved census are T55.2/T55.3 scope.
- The oracle and the extractor implement the same frozen KD table. A
  misreading shared by both would not show up here.

## 9. Release 1.0.0 (2026-10-13): signed and proven on the real binary

The signed record is `spike/t553/release/selection/release.json`
(`rel-phebs.kafka.producer-1.0.0`, key `ben-phebs-release-2026-10`). It binds:

- the card, manifest and validation in `spike/t553/release/artifacts/`;
- release binary commit `71924817` (`sha256:8c02e391…`, reproduced byte for
  byte by a second clean build);
- validation expiry 2027-01-11.

**Code added.** The recipe is bound in `packRecipes`. The real-binary run
then found that topic usage read legacy runs only, while production
extraction publishes partitioned roots. The census reported 0 published
runs over live producer data. Topic usage and its census now read each
repository's legacy published run, or else its fenced current partitioned
root, as T49.3 does for Atlas. Bundles name the roots in
`exact_authorities`, a root that moves forces a retry, and a stale root is
unavailable, never partial.

**Rehearsal (`receipts/rehearsal.json`).** This ran on the identical binary
and code path with a throwaway signing key, because the record's validation
had to contain these results. It used seven T55.1 corpus repositories at
their locked commits, release-only, with no experimental switch. In every
phase the served census equalled the independent oracle's per-class counts
(selector-expr 1, unresolved-ident 17).

| Phase | Ready | To published | Peak session RSS |
|---|---|---|---|
| Cold | 2.5 s | 48.8 s | 509 MiB |
| Restart | 1.5 s | 3.3 s | 248 MiB |
| Hard death (SIGKILL of the session) | 1.3 s | 3.3 s | 238 MiB |
| Backup → restore (backup 165 MiB, restore 29.8 s) | 1.5 s | 55.5 s | 250 MiB |

The cold phase also checked:

- capabilities are exactly `kafka-topic-usage` and the service catalog;
- anonymous requests get 401;
- an OIDC member's census is limited to its one visible repository and
  matches the oracle there;
- the member gets 404 on an admin bundle covering hidden repositories;
- MCP works;
- topic query latency is p50 108 ms and p95 114 ms.

**Final proof with Ben's key (`receipts/final.json`).** A release-only
start served exactly `kafka-topic-usage` plus the service catalog, the
census equalled the oracle, and the consumer plane was named unavailable.
With the release id revoked, the surface was gone (404).

**Not established.** No independent human review; Claude was the
adjudicator and reviewer Ben named. Topic-name precision (only 4 literal
sites were measured). The consumer plane. The envelope covers this host and
seven repositories only.
