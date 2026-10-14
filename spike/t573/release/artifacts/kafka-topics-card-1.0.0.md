# Claim card: phebs.kafka.topics 1.0.0 (combined composition)

**Claim.** With `phebs.kafka.producer` and `phebs.kafka.consumer` both
released, the composed inventory (`GET /api/list_kafka_topics`,
`GET /api/topic_inventory/{id}`, MCP `list_kafka_topics` and
`read_topic_inventory`, capability `kafka-topic-inventory`) lists exactly the
topic spellings the two released recipes resolve in the caller's visible
repositories. Each plane's site and file counts per topic equal the planes'
own evidence, and its unresolved census equals theirs. Each plane is reported
separately, with its own publication, coverage and quality state.

**Non-claims.**
- No runtime topic activity, broker, cluster or environment identity.
- No complete topology, and no cross-repository identity beyond the source
  spelling.
- An absent topic is not proven absent.
- Production Go code rarely spells topics literally. Over all 17 T55.1
  production repositories the inventory has 0 rows and is a census only.
- Site-level quality is each plane's own sealed result, inherited and not
  re-measured.
- No UI (T57.4).
- No producer-only or consumer-only product: with either plane unreleased,
  this claim is withdrawn.

**Quality.** Protocol `docs/TOPICS_QUALITY_PROTOCOL.md`.
- **Round 1** (sealed `562e1af7`, 7 production repositories): the census was
  exact, but the rows were vacuous at 0 topics, so not established.
- **Round 2** (sealed `e2f6e67b`, corpus locked `4a1d23b4`, 10 repositories
  from four frozen code searches): 41 of 41 topics matched the
  independent oracle union exactly. Every per-plane site and file count
  matched, as did the census, in all four phases and for the member.
- Disclosed: the round 2 population is mostly small student or demo projects,
  and one repository carries 27 of the 41 topics.

**Operating envelope (this host: 12 CPUs, 16 GB; 10 round 2 repositories).**

| Measure | Value |
|---|---|
| Cold start to both planes published | 47 s |
| Restart, or recovery after SIGKILL, to published | 3.9 s |
| Online backup | 138 MiB |
| Restore | 30 s |
| Data dir | 357 MiB |
| Inventory p95 (every page) | 361 ms |
| Topic query p95 | 350 ms |
| Peak session RSS | 409 MiB |

**Authorization.** Anonymous requests are refused (401). A member's inventory
covers only its visible repositories, and the MCP first page equals the HTTP
first page.

**Owner.** Ben Meddeb, with Claude as lead implementer.

**Reviewer and adjudicator.** Claude, as named by Ben. This is not an
independent human review.

**Expiry and revalidation.** The release expires 2027-01-11. Revalidate it on
any inventory schema change (`kafka-topic-inventory-v1`), any composition
change, or any re-release of either plane.

**Suspension.** Add the release_id to `release_selection.revoked` and restart.
That withdraws the inventory alone, and both planes keep serving. Revoking
either plane withdraws this claim too.
