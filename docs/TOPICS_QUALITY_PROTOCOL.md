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

## 7. Result (2026-10-13): census exact; rows not established

The rehearsal ran release-only, under a throwaway key, on binary `dc7632ee`
(`sha256:256a85f4…`, reproduced byte for byte by a second clean build). The
receipt is `spike/t573/release/receipts/rehearsal.json`.

- **Census: passes.** In every phase (cold, restart, hard death,
  backup→restore), the inventory's unresolved census equalled both oracles
  exactly. It did so for the administrator and for the goka-only member.
  Capabilities were exactly the three in §5, anonymous got 401, and the MCP
  first page equalled the HTTP first page.
- **Rows: vacuous, so not established.** The oracle union is **0 topics**,
  and so was the served inventory. A 0 = 0 match measures nothing about rows.
  Running the oracles over all 17 T55.1 corpus repositories at their locked
  commits also finds 0 resolved topic spellings in either plane. Every site is
  unresolved or has no topic field, because production topics are
  config-driven, the same finding behind the T55.1/T56.1 site-coverage claims.
  No population we already hold can measure inventory rows.
- **Operating.** Cold start reached published in 52.6 s. Restart and hard
  death each recovered in 3.6 s. The backup was 166 MiB, restore took 31.4 s,
  and the data dir was 473 MiB. Inventory p95 was 252 ms (every page,
  20 runs) and topic-usage p95 was 249 ms. Peak session RSS was 519 MiB.

Per §5 nothing fails, but the claim in §1 is not established on rows. No
record is signed with Ben's key on this result. The next step is Ben's call:
a fresh population with literal topics, or a narrower census-only claim.

## 8. Round 2: a fresh literal-topic population (frozen by Ben's choice, 2026-10-13)

Ben chose a fresh population after §7. The claim (§1), truth (§3),
measurement (§4) and thresholds (§5) are unchanged. Only the population
changes, and one power floor is added. This section is frozen before any
candidate is selected, and the corpus lock that follows it is committed
before phebs runs on any candidate.

**Selection, run once.** `gh search code` with `--language go --limit 100`
for each of these queries, in this order:

1. `"kafka.WriterConfig" "Topic:"`
2. `"kafka.ReaderConfig" "Topic:"`
3. `"sarama.ProducerMessage" "Topic:"`
4. `"ConsumePartition(\""`

Candidates are repositories in order of first appearance across the four
result lists. Excluded:

- the 17 T55.1 corpus repositories;
- the T23.1 tuning pins: jaegertracing/jaeger, IBM/sarama, Shopify/sarama,
  segmentio/kafka-go and zeromicro/go-queue;
- forks and archived repositories.

**Pinning.** Each candidate is pinned at the default-branch HEAD of a depth-1
clone on the selection date.

**Eligibility, applied in candidate order.** A candidate is dropped, with the
reason recorded, when:

- its checkout exceeds 200 MiB or 5,000 `.go` files (this host's envelope);
- or the truth side (the T55.1/T56.1 oracles) finds no resolved topic in
  either plane at the pinned commit.

No phebs output on any candidate is consulted. The first 10 eligible
candidates are the population, and the first of them is the member
repository.

**Power floor.** The oracle union over the population must hold at least 20
topics. Below that, the round is underpowered and is recorded as not
established, never as a pass.

**Result rule.** §5 applies unchanged: any single difference fails. Because
the rows now carry literal spellings, a served topic absent from the oracle,
or a mismatched per-plane count, is also the planes' first topic-name
precision evidence. It is reported as such, without re-scoring their sealed
site results.

## 9. Round 2 result and release (2026-10-13): passes; Topics 1.0.0 released

**Population.** The four frozen searches returned 77 unique repositories. In
order, 30 candidates were examined before 10 were eligible. The 20 drops
were:

- 17 with no resolved topic in either plane;
- 2 excluded by the T55.1 corpus or a T23.1 pin;
- 1 exceeding the host envelope.

The lock is `spike/t573/round2/corpus.lock.json`, committed at `4a1d23b4`
before phebs ran on any candidate. The oracle union holds 41 distinct topics:
34 producer and 31 consumer. The fourth query returned no results as run.
Disclosed: the population is mostly small student or demo projects, and
swiggy-2022-bootcamp/training-i-plus-plus carries 27 of the 41 topics.

**Rehearsal.** Run under a throwaway key, on the same binary `dc7632ee`; the
receipt is `spike/t573/round2/rehearsal.json`.
- In all four phases, 41 of 41 served topics equalled the oracle union. Every
  per-plane site and file count matched, missing 0, extra 0, and the census
  was exact.
- The member's inventory equalled the oracle over its one repository
  (1 topic).
- Anonymous requests got 401, and the MCP first page equalled the HTTP first
  page.
- No topic-name precision defect appeared in either plane.

| Phase | Ready | To published | Peak RSS |
|---|---|---|---|
| Cold | 2.5 s | 46.8 s | 409 MiB |
| Restart | 1.5 s | 3.9 s | 250 MiB |
| Hard death (SIGKILL) | 1.5 s | 3.9 s | 250 MiB |
| Backup → restore (backup 138 MiB, restore 29.8 s) | 1.5 s | 57.7 s | 302 MiB |

Inventory p95 is 361 ms, paging every page; topic usage p95 is 350 ms.

**Release.** `spike/t573/release/selection/` holds three records signed with
`ben-phebs-release-2026-10` for binary `dc7632ee` (`sha256:256a85f4…`,
reproduced byte for byte by a second clean build). All three expire
2027-01-11:

- `rel-phebs.kafka.topics-1.0.0`;
- `rel-phebs.kafka.producer-1.0.2`;
- `rel-phebs.kafka.consumer-1.0.1`.

The plane records re-bind unchanged claims. Their served-path evidence is the
round 1 rehearsal over the T56.3 seven, on this binary.

**Final proof with Ben's key (`spike/t573/release/receipts/final.json`).**
- With all three records, the inventory served 41 of 41 exactly.
- With only the Topics release revoked, both planes served exact censuses and
  the inventory answered 404.
- With only the consumer release revoked, the producer served and Topics was
  withdrawn (404). The real binary logged: `release selection:
  phebs.kafka.topics withdrawn: its combined claim needs both Kafka planes
  released, have [producer]`.

**Not established.**
- No independent human review.
- Topic rows at production scale: production Go code in the T55.1 corpus
  spells no literal topic.
- The envelope covers only this host and these populations.
