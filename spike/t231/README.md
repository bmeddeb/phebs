# T23.1 — Kafka topic-evidence validation spike

Charter: freeze the recognition, literal-or-abstain, identity, tier, and
declarations-plane rules the Epic 23 `kafkago` extractor must implement, by
proving or refuting each candidate rule against pinned real corpora with
executable gates. Spike code is throwaway; the decisions below and the
committed fixtures are the deliverable. Nothing here is imported by
production packages, and nothing here is or authorizes an accuracy claim —
GATE2-V2 remains `NOT_ESTABLISHED`.

## Corpora (corpus.lock.json)

| Name | Commit | Role |
|---|---|---|
| jaeger-v1 | `55e991a2` (v1.57.0) | production sarama: kafka storage producer + ingester consumer group, topics from CLI/viper configuration — the abstention-reality corpus |
| sarama | `b49108d3` | library corpus at the IBM import path: qualified literal examples (http_server) and the canonical Consume/ConsumePartition shapes |
| kafka-go | `2e0b3968` | segmentio corpus: Writer/WriterConfig/ReaderConfig/GroupTopics shapes; examples are entirely environment-driven |

Provenance is disclosed: sarama and kafka-go are library repositories, so
their examples witness recognition rules, not production shape claims;
jaeger v1 is the production witness. The jaeger v2 line (t191's pin) has
migrated its kafka components to franz-go — recorded in KD3 as a deferred
family, never silently admitted.

## Gates (all green at the pinned commits)

| Gate | Result |
|---|---|
| Synthetic recognition (offline) | every canonical positive binds with exact topic/binding/plane/shape (literal, same-file-const, Consume-slice, ConsumePartition, Writer, WriterConfig, ReaderConfig.Topic, GroupTopics with GroupID detail); every canonical negative abstains with its exact shape class |
| Identity rules (offline) | Kafka's own topic constraints (1–249 chars, `[a-zA-Z0-9._-]`) bound admission; object spelling `topic:<literal>` carries nothing else |
| K1 corpus survey | the import-path era split is real: jaeger v1 production is on `github.com/Shopify/sarama` (11 files), the sarama corpus on `github.com/IBM/sarama` (11), kafka-go on `github.com/segmentio/kafka-go` (4) — recognition must accept both sarama paths and record which |
| K2 sarama producer | the two hand-labeled http_server literals (`important`, `access_log`) bind exactly; jaeger's production writer (`Topic: w.topic`) abstains `selector-expr` with zero literal evidence |
| K3 sarama consumer | the consumergroup example's `strings.Split(...)` topics abstain `call-expr`; the jaeger ingester yields zero literal consumer evidence once KD5 excludes `_test.go` — its tests consume the invented fixture topic `morekuzambu`, the finding that froze the exclusion |
| K4 segmentio | kafka-go's own examples yield **zero** literal evidence — every topic is environment-driven — so the positive segmentio shapes are frozen by the synthetic fixtures (disclosed) |
| K5 declarations plane | zero qualified in-code topic declarations (`kafka.TopicConfig`) across all corpora — the round-one verdict rests on recorded absence |
| K6 census | 48 non-test files scanned: **2 literal evidence rows, 17 abstentions, 39 test-file evidence rows excluded by KD5** — sanitized counts recorded, no silent caps; abstention dominance is the production reality |

## Decision table (frozen for T23.2–T23.4)

| # | Decision |
|---|---|
| KD1 | Domains `kafka-producer` / `kafka-consumer` (reserved: `kafka-topic`); extractor `kafkago` 1.0.0; dark flag `experimental.provisional_kafka_extraction`. |
| KD2 | Object spelling `topic:<literal>`, admitted only when the literal satisfies Kafka's own naming bounds (1–249 chars of `[a-zA-Z0-9._-]`); an illegal literal abstains `invalid-topic-literal`. The object carries **no cluster, environment, runtime, or completeness claim**. Positive predicates `PRODUCES_TO_TOPIC` / `CONSUMES_FROM_TOPIC`; abstentions emit `UNRESOLVED_KAFKA_PRODUCER` / `UNRESOLVED_KAFKA_CONSUMER` with the shape class as detail and no topic. |
| KD3 | Round-one libraries: sarama under **both** import paths (`Shopify` era in production jaeger v1, `IBM` current — the row records which) and `segmentio/kafka-go`. franz-go is a **deferred family**: the reference OSS infrastructure (jaeger v2's kafka components) has migrated to it, and admitting it requires its own spike gate, not a silent extension. |
| KD4 | Recognition is qualified-selector shapes only — `alias.ProducerMessage{Topic:}`, `alias.Writer{Topic:}` / `alias.WriterConfig{Topic:}`, `alias.ReaderConfig{Topic:/GroupTopics:}`, `.Consume(ctx, []string{...}, handler)`, `.ConsumePartition(topic, …)` — where the alias resolves to a round-one import. Dot-imports are refused by omission: an unqualified composite has no in-file library proof (sarama's own in-package tests demonstrate the ambiguity). |
| KD5 | Document eligibility: imports a round-one library **and is not a `_test.go` file**. The jaeger ingester tests consume the invented topic `morekuzambu` — fixture literals are authored noise, not production topic evidence. The census records what exclusion leaves out (39 rows at the pins); nothing is silently dropped. |
| KD6 | Literal-or-abstain: a string literal or a **same-file `const`** resolves (`binding: literal` / `same-file-const`); everything else abstains with a frozen shape class — `selector-expr`, `call-expr`, `unresolved-ident`, `non-literal-expr`, `invalid-topic-literal`. A same-file `var` reports `unresolved-ident`: mutable state is not a declaration-strength spelling, and round one does not distinguish mutable from unknown. No dataflow, no cross-file resolution, no constant propagation. |
| KD7 | Consumer group ids are **detail, never identity**: `GroupID` rides on consumer evidence rows when it is itself literal/const, and the object spelling structurally cannot contain it. |
| KD8 | Declarations plane: **honestly empty in round one.** No corpus carries an in-code topic declaration; config files and schema-registry exports remain out of scope. Round one ships a topic-keyed producer/consumer index with no catalog/Atlas surface, and the UI says so plainly. |
| KD9 | Tier: composite-literal shapes are `derived`; `Consume`-by-arity rows are `heuristic` (the receiver's type is not resolvable without type-checking, and the rule is method-name + arity in a sarama-importing file). `exact` is structurally unavailable to static source — no wire evidence exists. |
| KD10 | Expected production posture: abstention-dominant. At the pins, 2 literal evidence rows vs 17 abstentions across 48 non-test files. The pack's surfaces must present `UNRESOLVED_*` volume as the honest norm, never as a defect to hide, and no surface may imply "all producers/consumers of topic X". |

## Running

```
go test ./spike/t231/                 # offline: synthetic recognition, identity, vocabulary
T231_FETCH=1 go test ./spike/t231/    # clones/pins corpora, runs K1–K6
T231_CORPUS_ROOT=<dir> go test ./spike/t231/   # reuse existing pinned clones
```

The corpus gates' counts above are single-pin observations of public OSS
repositories; they are sanitized shape/count records. The result is
not an accuracy claim about any extractor.
