# Claim card: phebs.kafka.consumer 1.0.4 (site coverage)

1.0.4 re-binds this claim to binary `75803641` (T49.4: Contract Atlas MCP served with Atlas alone), in the
five-release selection `spike/t494/release`. Its served-path evidence is that binary's own rehearsal.

**Claim.** In a non-test Go file importing a round-one library, the
`kafka-consumer` extractor reports every consumer site of the frozen T23.1
KD4 shapes:

- a segmentio `ReaderConfig{Topic:}`, and each element of a literal
  `GroupTopics` list;
- sarama's receiver-untyped `.Consume(ctx, topics, handler)` and
  `.ConsumePartition(topic, …)`, counted only in files importing sarama.

Each site gets exactly one row at the exact span of its topic expression:
`CONSUMES_FROM_TOPIC topic:<literal>`, or `UNRESOLVED_KAFKA_CONSUMER
unresolved:<class>`. A file importing both sarama eras abstains
`ambiguous-library-import`.

The served surface (`find_kafka_topic_usage`, HTTP and MCP, capability
`kafka-topic-usage`) returns consumer evidence and the consumer census over
the caller's visible repositories. It names an unreleased producer plane
unavailable.

**Non-claims.**
- No runtime, cluster or broker identity.
- No dataflow or cross-file resolution.
- No franz-go or confluent-kafka-go.
- Group ids are detail, never identity.
- `Consume` and `ConsumePartition` are heuristic: the receiver type is not
  checked.
- No completeness of topics.
- Topic-name precision is unmeasured: no literal consumer topic exists in
  the measured population.
- Known minor inconsistency: a non-literal `GroupTopics` value is classed
  `non-literal-expr` rather than by its syntax (1 of 640 measured sites).

**Quality.** T56.1, sealed: 640 of 640 consumer sites covered across real
production history (17 repositories, 2,790 file versions), with class
agreement 639 of 640, 0 extra rows and 0 wrong topics.

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
must be revalidated on any `kafkago` change, library-path addition, or
census/surface schema change.

**Suspension.** Revoke the release_id. This withdraws the consumer plane
only; a released producer plane keeps serving.
Revoking this release also withdraws `phebs.kafka.topics`, whose combined
claim needs both planes.
