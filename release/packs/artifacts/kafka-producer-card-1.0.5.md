# Claim card: phebs.kafka.producer 1.0.5 (site coverage)

1.0.5 re-binds this claim to binary `75803641` (T49.4: Contract Atlas MCP served with Atlas alone), in the
five-release selection `spike/t494/release`. Its served-path evidence is that binary's own rehearsal.

**Claim.** In a non-test Go file importing a round-one library (sarama under
`github.com/Shopify/sarama` or `github.com/IBM/sarama`, or
`github.com/segmentio/kafka-go`), the `kafka-producer` extractor reports
every producer site of the frozen T23.1 KD4 shapes:

- `ProducerMessage{Topic:}`;
- `Writer{Topic:}` and `WriterConfig{Topic:}`;
- a `Message{Topic:}` passed directly to `WriteMessages`.

Each site gets exactly one row at the exact span of its Topic expression:

- `PRODUCES_TO_TOPIC topic:<literal>` for a string literal, or a same-file
  const with a literal initializer, within Kafka's naming bounds;
- otherwise `UNRESOLVED_KAFKA_PRODUCER unresolved:<class>` with the correct
  frozen shape class.

The served surface (`find_kafka_topic_usage`, HTTP and MCP, capability
`kafka-topic-usage`) returns literal-topic evidence and the per-class
unresolved census over the caller's visible repositories. It names the
consumer plane unavailable whenever that plane is not released.

**Non-claims.**
- No runtime, cluster, broker or environment identity.
- No dataflow or cross-file resolution.
- No franz-go or confluent-kafka-go.
- Sites with no Topic key are not reported.
- No completeness of topics: production topics are almost always
  configuration-driven, so unresolved sites dominate by design.
- Topic-name precision is effectively unmeasured: only 4 of 762 measured
  sites named a literal topic.
- No consumer evidence: the consumer plane is its own release (`phebs.kafka.consumer`).

**Quality.** T55.1, sealed: 762 of 762 producer sites across real
production history (17 repositories, 2,790 file versions) were covered with
the right state and class. There were 0 extra rows and 0 wrong topics.

**Operating envelope (this host: 12 CPUs, 16 GB; 10 T57.3 round-2 repositories; five releases on one binary).**

| Measure | Value |
|---|---|
| Cold start to both planes published | 44 s |
| Restart, or recovery after SIGKILL, to published | 3.9 s |
| Online backup | 138 MiB |
| Restore | 27 s |
| Data dir | 358 MiB |
| Topic query p95 | 333 ms |
| Inventory p95 (every page) | 347 ms |
| Peak session RSS | 353 MiB |

**Authorization.** Anonymous requests are refused (401). A member's census
covers only its visible repositories, and a bundle covering repositories it
cannot see is not readable to it (404).

**Owner.** Ben Meddeb, with Claude as lead implementer.

**Reviewer and adjudicator.** Claude, as named by Ben. This is not an
independent human review.

**Expiry and revalidation.** The release expires 90 days after approval. It
must be revalidated on any `kafkago` recognition or version change, on a
library-path addition, or on a census/surface schema change.

**Suspension.** Add the release_id to `release_selection.revoked` and
restart.
Revoking this release also withdraws `phebs.kafka.topics`, whose combined
claim needs both planes.
