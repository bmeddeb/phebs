# T56.1 Kafka consumer site-coverage quality protocol

Status: **sealed and scored: passes** (2026-10-13); see §8. Ben chose the same
site-coverage claim and thresholds as T55.1. Claude seals, adjudicates and
scores. This round is independent of the producer result (Epic 56): no
producer number is reused.

## 1. Claim card (`phebs.kafka.consumer`, site coverage)

In a non-test Go file importing a round-one library, the `kafka-consumer`
extractor reports every consumer site of the frozen T23.1 KD4 shapes:

- a segmentio `ReaderConfig{Topic:}`, and each element of a literal
  `GroupTopics` list (any other `GroupTopics` value is one site);
- sarama's receiver-untyped `.Consume(ctx, topics, handler)`, one site per
  element of a literal list, else the topics argument;
- sarama's `.ConsumePartition(topic, partition, offset)`.

The receiver-untyped shapes count only in files importing sarama. A file
importing both sarama eras abstains `ambiguous-library-import` on them.

Each site gets exactly one row at the exact span of its topic expression:

- `CONSUMES_FROM_TOPIC topic:<literal>` for a literal or same-file const
  within Kafka's naming bounds;
- otherwise `UNRESOLVED_KAFKA_CONSUMER unresolved:<class>`.

Group ids are detail, never identity (KD7), and are not scored.

**Non-claims.** These are the same as the producer card. In addition,
`Consume` and `ConsumePartition` are `heuristic`: the receiver's type is not
checked.

## 2. Population

The population is the T55.1 population, unchanged: 2,790 distinct file
versions from the real history of 17 pre-registered production repositories
(`spike/t551/population`, `units_sha256` `c6ca32e1…`). It was selected by
import, not by plane.

## 3. Truth

The truth is the T55.1 oracle (`spike/t551/oracle`), extended with
`-plane consumer` from KD4/KD6/KD7/KD9 only, and fenced by tests for every
consumer shape, list form, the dual-era abstention, and a segmentio-only
`Consume`. Its pre-seal pass, without the extractor, finds **640 consumer
sites** in 570 units, all unresolved:

| Class | Sites |
|---|---|
| selector-expr | 274 |
| unresolved-ident | 252 |
| call-expr | 110 |
| non-literal-expr | 4 |

By shape the sites are 398 `Consume`, 189 `ConsumePartition`, 52
`ReaderConfig.Topic` and 1 `GroupTopics`.

## 4–5. Adjudication and measurement

These follow the T55.1 protocol §4–§5, with `export -plane consumer` and
`score.py`, which accepts either plane.

## 6. Thresholds (frozen by Ben, 2026-10-13)

| Check | Bar |
|---|---|
| Coverage | ≥ 99% |
| Class agreement | ≥ 99% |
| Wrong resolved topics | 0 |
| Confirmed spurious rows per scored site | ≤ 1% |
| Pending after adjudication | 0 |
| Scored sites | ≥ 300 |

## 7. Seal

The protocol commit, the population digest, the oracle, export and scorer
digests, and the thresholds are published in a public gist before
`export -plane consumer` runs on the population.

## 8. Result (2026-10-13): passes

**Seal.** Commit `3cc2dc5e` plus gist
<https://gist.github.com/Blandt/004c9c2ead67c04cff1e4b9800f09b74>,
published before the consumer extractor ran on the population. The receipts
are in `spike/t561/round/`.

**Score.**

| Measure | Value |
|---|---|
| Scored sites | 640 |
| Covered | 640 (100%) |
| Missed, duplicated, extra rows | 0 |
| Wrong topics | 0 |
| Class agreement | 639/640 (99.84%) |
| Pending after adjudication | 0 |

Every check passes. With zero misses over 640 sites, the 95% upper bound on
the per-site miss rate is about 0.5%.

**The one disagreement (adjudicated).** In kafka-konsumer
`pkg/kafka/consumer.go`, the code reads `GroupTopics:
kafkaConsumerCfg.Topics`.

- The extractor classes it `non-literal-expr`.
- KD6 classes abstentions by expression syntax, and the extractor itself
  classes the equivalent non-literal `Consume` topics argument that way (110
  agreeing `call-expr` sites). So the truth is `selector-expr`.

This is a minor extractor inconsistency for non-literal `GroupTopics` values.
Fixing it changes the shared `kafkago` version, which the signed producer
release binds. It is recorded for a later `kafkago` revision that re-signs
both planes.

**What it shows.** The consumer extractor finds every KD4 consumer site in
real production code, including the heuristic receiver-untyped shapes. It
emits no spurious sites over this population.

**What it does not show.**

- Topic precision: no consumer site in this population names a literal
  topic.
- The served path. That is T56.2/T56.3.
