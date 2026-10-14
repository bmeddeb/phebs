# T57.3 Topics composition quality protocol

Frozen before any served inventory is scored. Ben chose the combined claim, the
oracle-union bar and the release shape on 2026-10-13. The commit that adds this
file is the seal: nothing below changes after scoring starts, and a failed
round is recorded as failed rather than rescored.

## 1. Claim card (`phebs.kafka.topics`, combined)

**Claim.** With both Kafka planes released (`phebs.kafka.producer` and
`phebs.kafka.consumer`), the composed inventory served by
`GET /api/list_kafka_topics`, `GET /api/topic_inventory/{id}` and the MCP tools
`list_kafka_topics` / `read_topic_inventory` lists exactly the topic spellings
those two released recipes resolve in the caller's visible repositories. For
each topic and each plane, its site and file counts equal the planes' own
measured evidence, and the inventory's unresolved census equals the planes'
census.

**What this measures.** Composition only. Site-level quality is each plane's
own sealed result (T55.1 762/762, T56.1 640/640 with 639/640 class agreement),
and this claim inherits those results unchanged; it neither re-measures nor
widens them. Composition is deterministic over published evidence, so the
round measures it exactly against the planes' independent oracles rather than
sampling it.

**Non-claims.** Runtime topic activity, broker or cluster identity, environment
equality, complete topology, cross-repository topic identity beyond the source
spelling, topic-name precision for spellings the planes do not resolve, and
any UI. An absent topic is not proven absent. Producer-only and consumer-only
Topics are different products and are not claimed: with either plane not
released, the claim is withdrawn and the inventory is not served.

**Identity.** Pack `phebs.kafka.topics` 1.0.0, implementation identity
`list_kafka_topics@kafka-topic-inventory-v1` in `cmd/phebs` `releasedClaims`.
A schema change invalidates the release.

## 2. Population

The seven T55.3/T56.3 release-rehearsal repositories at their
`spike/t551/corpus.lock.json` commits: goka, kafka-pixy, watermill-kafka,
Burrow, go-dnscollector, plugins and tiflow. Disclosed: this population has
been seen. It served both plane releases. That is acceptable here, because
this round has no tunable component (the composition code is frozen at the
release commit), and a fresh population would add nothing that composition
can overfit.

The member view is goka alone, as in T56.3.

## 3. Truth

The independent T55.1 and T56.1 oracles (`spike/t551/oracle`, `-plane producer`
and `-plane consumer`), run over the same checkouts. For each plane, every
oracle row with `state` `resolved` and object `topic:<s>` is one site of topic
`<s>`, and its file is the pair (repository, path). Then:

- expected topics = the union, over both planes, of every `<s>`;
- expected sites for (topic, plane) = that plane's resolved rows for `<s>`;
- expected files for (topic, plane) = distinct (repository, path) among them;
- expected census = each plane's unresolved counts by class, exactly as T56.3.

A served row's plane counts only when its state is `served` with a nonzero
site count; `none` is that plane's measured zero.

## 4. Measurement

`spike/t553/release_rehearsal.py --topics` on the real release binary,
release-only (no experimental switch), pages the whole inventory at
`page_size` 100. Each page must say both planes are `released` and `served`,
and the run fails if a page is truncated, a topic repeats, or pagination ends
before `total`. This runs in every lifecycle phase (cold, restart, hard death,
backup→restore), for the administrator over all seven repositories and, in
the cold phase, for the member over goka. The cold phase also checks
anonymous denial and that the MCP first page equals the HTTP first page.

## 5. Thresholds (frozen)

All of the following, in every phase:

- 0 missing topics, 0 extra topics;
- 0 rows whose per-plane sites or files differ from §3;
- the census equals §3;
- the member inventory equals §3 computed over goka alone;
- capabilities are exactly `kafka-topic-inventory`, `kafka-topic-usage` and
  `service-catalog-v2`;
- anonymous gets 401, and MCP equals HTTP.

Any single difference fails the round.

**Revocation (final proof with the signed records).** Revoking the Topics
release alone keeps both planes serving their exact censuses, while the
inventory routes answer 404. Revoking the consumer release withdraws Topics
too: the producer keeps serving and the inventory answers 404.

## 6. Release shape

Three records signed with `ben-phebs-release-2026-10` for one binary, all
expiring 2027-01-11: Topics 1.0.0, producer 1.0.2 and consumer 1.0.1. The two
plane records re-bind unchanged claims, because a record admits one exact
binary. WIRE stays bound to `e485fc36` until its own revalidation.
