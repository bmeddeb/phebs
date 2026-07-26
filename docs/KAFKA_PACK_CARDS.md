# Kafka protocol-pack cards

Completed instances of the [evidence-pack card template](./EVIDENCE_PACK_CARD.md)
for the Epic 23 Kafka topic-evidence packs. Both packs are
**experimental-dark**: the release invariant's blank-field prohibitions bind
at `released` status, which neither pack holds or requests. The PackRelease
binding machinery remains design-only (see
[PACK_MANIFEST.md](./PACK_MANIFEST.md)); fields that depend on it say so
explicitly rather than simulating it.

Both packs share one T23.1-validated recognizer and one honesty posture:
**abstention dominates by design.** Production Kafka topics are
overwhelmingly configuration-driven — the spike's pinned production corpora
yielded 2 literal evidence rows against 17 abstentions — and every surface
built on these packs must present `UNRESOLVED_*` volume as the norm, never
imply "all producers/consumers of topic X".

---

## Pack: Kafka producer evidence

| Field | Value |
|---|---|
| Pack ID | `phebs.kafka.producer` |
| Status | `experimental-dark` |
| Predicate(s) | `PRODUCES_TO_TOPIC, UNRESOLVED_KAFKA_PRODUCER, KAFKA_EXTRACTION_GAP` |
| Language/framework | Go sources importing `github.com/Shopify/sarama`, `github.com/IBM/sarama`, or `github.com/segmentio/kafka-go`; stdlib `go/ast` parsing only |
| Pack version | `1.0.0` (`kafka-producer` extractor) |
| Extractor artifact | in-tree `internal/extract/extractors/kafkago`; no separate binary — the phebs release binary digest governs |
| Schema version | atom `t23-v1`; details `kafka-topic-evidence-detail-v1`, `kafka-topic-unresolved-detail-v1`, `kafkago-gap-detail-v1` |
| Release binding | none — experimental-dark; PackRelease machinery is design-only |
| Owner | Ben Meddeb |
| Independent validation owner | none — single-operator project; see validation section |
| Last measured | 2026-07-26 (T23.1 spike gates at pinned corpora) |
| Validation expires/review due | re-run `spike/t231` gates on any recognition-rule change, library-path addition, or corpus re-pin |
| Exception authority | operator (Ben Meddeb); the pack itself grants no exceptions |
| Current decision | dark — enabled only by `experimental.provisional_kafka_extraction` |

**Claim.** For each non-test Go file at the indexed commit importing a
round-one library, the pack asserts `PRODUCES_TO_TOPIC` for every
`sarama.ProducerMessage{Topic:}`, segmentio `Writer`/`WriterConfig`
composite, and `kafka.Message{Topic:}` passed directly to `WriteMessages`,
when the topic is a string literal or same-file `const` inside Kafka's
naming bounds. Objects are `topic:<literal>`; tier is `derived` for
composite shapes; subjects are file paths with `provisional_repo_path_v1`
lineage; spans are exact bytes of the topic expression.

**Non-claims.** No cluster, environment, broker, or runtime identity — a
topic object is a source spelling, nothing more. No completeness: a
configuration-driven producer emits only an `UNRESOLVED_KAFKA_PRODUCER`
abstention with its shape class, and dynamic topics are invisible. No
dataflow, no cross-file resolution, no constant propagation beyond a
same-file `const`. No franz-go recognition (recorded deferred family). No
accuracy percentage (GATE2-V2 remains `NOT_ESTABLISHED`).

**Incomplete analysis representation.** Non-literal topics emit
tier-`unresolved` `UNRESOLVED_KAFKA_PRODUCER` assertions whose object names
one of six frozen shape classes; oversized or unparseable files emit one
`KAFKA_EXTRACTION_GAP` per file. `_test.go` fixture literals are excluded
from recognition entirely (the T23.1 `morekuzambu` finding).

---

## Pack: Kafka consumer evidence

| Field | Value |
|---|---|
| Pack ID | `phebs.kafka.consumer` |
| Status | `experimental-dark` |
| Predicate(s) | `CONSUMES_FROM_TOPIC, UNRESOLVED_KAFKA_CONSUMER, KAFKA_EXTRACTION_GAP` |
| Language/framework | same round-one library set as the producer pack; stdlib `go/ast` parsing only |
| Pack version | `1.0.0` (`kafka-consumer` extractor) |
| Extractor artifact | in-tree `internal/extract/extractors/kafkago`; no separate binary — the phebs release binary digest governs |
| Schema version | atom `t23-v1`; details `kafka-topic-evidence-detail-v1`, `kafka-topic-unresolved-detail-v1`, `kafkago-gap-detail-v1` |
| Release binding | none — experimental-dark; PackRelease machinery is design-only |
| Owner | Ben Meddeb |
| Independent validation owner | none — single-operator project; see validation section |
| Last measured | 2026-07-26 (T23.1 spike gates at pinned corpora) |
| Validation expires/review due | re-run `spike/t231` gates on any recognition-rule change, library-path addition, or corpus re-pin |
| Exception authority | operator (Ben Meddeb); the pack itself grants no exceptions |
| Current decision | dark — enabled only by `experimental.provisional_kafka_extraction` |

**Claim.** For each eligible file, the pack asserts `CONSUMES_FROM_TOPIC`
for `ReaderConfig` `Topic` and `GroupTopics` entries (tier `derived`, with a
literal `GroupID` recorded as detail — never identity) and for the
receiver-untyped sarama `Consume(ctx, []string{...}, handler)` and
`ConsumePartition(topic, …)` call shapes (tier `heuristic` — the method-name
+ arity rule cannot see the receiver's type without type-checking). A file
importing both sarama eras abstains `ambiguous-library-import` rather than
naming an era nondeterministically.

**Non-claims.** Identical to the producer pack: no cluster/environment/
runtime identity, no completeness, no dataflow, no franz-go, no accuracy
percentage. Consumer-side `kafka.Message` values passed to `CommitMessages`
are offset bookkeeping and are never classified as production.

**Incomplete analysis representation.** Identical mechanism to the producer
pack, under `UNRESOLVED_KAFKA_CONSUMER`.

---

## Validation

Rule validation for both packs is the executable T23.1 spike
(`spike/t231/`): offline synthetic-shape and hand-label recognition suites
plus corpus gates K1–K6 over four exact public pins (jaeger v1.57.0,
IBM/sarama, segmentio/kafka-go, zeromicro/go-queue), with the decision table
KD1–KD10 frozen in `spike/t231/README.md`. The spike's counts are sanitized
single-pin observations; nothing in this card is or enables an accuracy
claim.
