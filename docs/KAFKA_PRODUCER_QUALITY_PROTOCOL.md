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
