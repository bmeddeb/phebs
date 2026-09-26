# phebs compared to other code search tools

One distinction organizes this page: raw search quality is not the
difference. phebs indexes with Sourcegraph's own zoekt engine, so search
sits in the same family as the most expensive product below. The
difference is everything that surrounds the search box — and whether a
sales process stands between you and it.

Facts about other products were last verified on **2026-08-24** against
public pricing pages and documentation. Vendors change their packaging;
follow the links.

| | [phebs](https://github.com/bmeddeb/phebs) | [Sourcegraph](https://sourcegraph.com/) | [OpenGrok](https://opengrok.github.io/) | [livegrep](https://livegrep.com/) | [GitHub code search](https://github.com/search) |
|---|---|---|---|---|---|
| License | Apache-2.0 | Proprietary | CDDL-1.0 | Apache-2.0 | Proprietary, hosted |
| Free self-hosted tier | Everything shipped | None (enterprise-only since 2025) | Everything | Everything | No (hosted only) |
| Deployment | One Go binary + supervised local children | Multi-service enterprise deployment | Java webapp + indexer | C++ backend + minimal web UI | N/A |
| Code navigation (go-to-def/refs) | Included (SCIP) | Included | Cross-reference engine | No | Limited |
| Auth, permissions, audit | Included (sessions, OIDC, revocable keys) | Included | No | No | Via GitHub |
| Agent surface | OpenAPI + MCP included | Enterprise tier only | No | No | REST/GraphQL API |
| Change-impact / contract evidence | Experimental, default-dark, coverage certificates | No | No | No | No |
| Price signal | Free, self-hosted | ~$59/user/mo; enterprise from ~$16K/yr | Free | Free | Included with GitHub |

**The search is the same family. The invoice is not.**

- Enterprise scale, batch changes, and budget to match → **Sourcegraph**.
- A battle-tested read-only source browser behind your own perimeter → **OpenGrok**.
- Pure interactive regex speed over a fixed corpus → **livegrep**.
- Everything already on GitHub, hosted is fine → **GitHub code search**.
- Apache-2.0 search, navigation, auth, audit, and MCP in one Go binary, with
  an experimental path from a match to bounded evidence → **phebs**.

## Where phebs loses

I wrote phebs, so read this section twice:

- **Scale.** Single-node today; no horizontal-scale or very-large-fleet
  claim yet (see the [roadmap](./ROADMAP.md)).
- **Maturity.** Sourcegraph has a larger team and years of production
  hardening.
- **Revisions.** HEAD is authoritative, with at most seven explicit
  branch/tag revisions per repository.
- **Evidence packs.** Contract Atlas, Caller Map, Impact, and Kafka topics
  are experimental, default-dark, and gate `NOT_ESTABLISHED`. A direction,
  not a guarantee.

Every tool on this page can find you a match. phebs is built for the next
question: what is the match allowed to mean?
