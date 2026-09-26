# phebs compared to other code search tools

> What do you actually give up — and what do you stop paying for — when your
> code search is one free, self-hosted Go binary instead of an enterprise
> platform?

The tools people evaluate alongside phebs — Sourcegraph, OpenGrok, livegrep,
GitHub's own code search — overlap with it on search and little else. The
overlap hides the real difference. Raw search quality is not the difference:
phebs indexes with Sourcegraph's own zoekt engine, so search sits in the same
family as the most expensive product in this comparison. The difference is
everything that surrounds the search box: navigation, authentication, audit,
an API and MCP surface for agents, and — experimentally — static contract
evidence that reports its own coverage. And whether a sales process stands
between you and any of it.

My situation: I wrote phebs. That makes me the worst source on its flaws and
the most informed source on why it exists. The "when phebs is not the right
choice" section below is where I try to correct for the first half of that.

External facts about other products are dated where they appear and were last
verified on **2026-08-24** against public pricing pages and documentation.
Vendors change their packaging; follow the links before making a decision.

## The short version

| | phebs | Sourcegraph | OpenGrok | livegrep |
|---|---|---|---|---|
| License | Apache-2.0 | Proprietary | CDDL-1.0 | Apache-2.0 |
| Free self-hosted tier | Everything shipped | None (enterprise-only since 2025) | Everything | Everything |
| Deployment | One Go binary + supervised local children | Multi-service enterprise deployment | Java webapp + indexer | C++ backend + minimal web UI |
| Code navigation (go-to-def/refs) | Included (SCIP) | Included | Cross-reference engine | No |
| Auth, permissions, audit | Included | Included | No | No |
| Agent surface (OpenAPI + MCP) | Included | Enterprise tier only | No | No |
| Change-impact / contract evidence | Experimental, default-dark, with coverage certificates | No | No | No |

Read the phebs column as a list of things you don't have to assemble
yourself: no sidecar for navigation, no plugin for auth, no license tier for
agent access.

## Sourcegraph

Sourcegraph is the most capable and most mature product in this space:
search and navigation across very large fleets, batch changes, code insights,
SSO, and deep admin tooling, deployable self-hosted or air-gapped. If your
problem is genuinely enterprise-scale, it's the proven answer and this page
won't talk you out of it.

Here is the distinction the feature matrix buries. The engine under
Sourcegraph's search is zoekt.

phebs ships zoekt.

**The search is the same family. The invoice is not.**

As of 2026, Sourcegraph is enterprise-only and sales-led: the free and Pro
tiers were retired in mid-2025, and public reporting puts the platform at
roughly $59/user/month (annual) with enterprise contracts reported to start
near $16K/year (verified 2026-08-24 against public pricing coverage; confirm
with [Sourcegraph](https://sourcegraph.com/) sales). Against that, phebs is
Apache-2.0 with the full surface — search, SCIP navigation, auth with
optional OIDC, permissions, audit, OpenAPI, and a stateless MCP server —
shipped in one binary you can read end to end.

Choose Sourcegraph if you have an enterprise budget, very large fleets, and
need batch changes and code insights. Choose phebs if you want the search
quality without the procurement process — and a deployment whose architecture
you can hold in your head.

## OpenGrok

OpenGrok is the veteran: a Java webapp with a fast source cross-reference
engine, free under CDDL-1.0, battle-tested on very large trees — it was built
for OpenSolaris. For "browse and grep a big source tree," it still works.

What it doesn't have is a boundary. No authentication, no permissions, no
audit. For a public corpus that's a simplification; for a private one it's a
gap you fence off at the network layer yourself. phebs includes browser
sessions, revocable API keys, optional OIDC, per-repository permissions, and
an audit trail in the same package — plus the agent-facing APIs OpenGrok
predates entirely.

Choose OpenGrok if you want a read-only source-browsing appliance behind
your own perimeter and its UI works for you. Choose phebs if the corpus is
private and the people searching it aren't all the same person.

## livegrep

livegrep does one thing as well as anything in this comparison: interactive
regex search over a large codebase, instant as you type. It's Apache-2.0,
deliberately minimal, and honest about it.

Speed is one row of the table. Behind the search box, phebs adds repository
sync from GitHub, GitLab, Gitea, and plain Git servers, browsing, file
history and blame, SCIP go-to-definition, access control, and an MCP
surface — the things that turn a search result into an answer with
provenance.

Choose livegrep if regex-at-speed over a fixed corpus is the whole job.
Choose phebs when the corpus moves and the answer has to hold up in review.

## GitHub code search

If every repository you care about already lives on GitHub and a hosted
service is acceptable, GitHub's built-in code search is good and costs
nothing extra. There's no reason to self-host what already works.

phebs exists for everything outside that sentence: code spread across GitLab,
Gitea, plain Git servers, and local checkouts; code that must not leave your
infrastructure; and evidence and audit requirements a hosted search box
doesn't satisfy. A hosted search box finds matches. It doesn't tell you what
a match is allowed to mean — phebs' experimental evidence packs separate
resolved callers from name matches and unresolved candidates, and ship a
coverage certificate that bounds the answer. That posture is experimental and
default-dark, and it's pointed in a direction no product in this comparison
points.

## When phebs is not the right choice

Being explicit about limits is part of the project. Skip this section and the
rest of the page deserves the discount you should already be applying to it.

- **Scale.** phebs is single-node today. It's running a measured
  scale-convergence program (see the [roadmap](./ROADMAP.md)), but it makes
  no horizontal-scale or very-large-fleet claim yet. Sourcegraph is the
  proven option at that end.
- **Maturity.** Sourcegraph has a larger team, more connectors, and years of
  production hardening. phebs is a young, fast-moving project.
- **Revisions.** phebs treats HEAD as authoritative, with at most seven
  explicit branch/tag revisions per repository. If you need every branch of
  every repo indexed continuously, Sourcegraph fits better.
- **Experimental evidence packs.** Contract Atlas, Caller Map, Impact, and
  Kafka topics are experimental and default-dark. They make no completeness
  or accuracy claim, and the retained external Go/gRPC validation gate is
  `NOT_ESTABLISHED`. Evaluate them as a direction, not a guarantee.

## The decision, one more time

- Enterprise scale, batch changes, and budget to match →
  [Sourcegraph](https://sourcegraph.com/).
- A battle-tested read-only source browser →
  [OpenGrok](https://opengrok.github.io/).
- Pure interactive regex speed → [livegrep](https://livegrep.com/).
- Everything on GitHub, hosted is fine →
  [GitHub code search](https://github.com/search).
- Apache-2.0 search, navigation, auth, audit, and MCP in one Go binary, with
  an experimental path from a match to bounded evidence → **phebs**.

The opening question was what you give up and what you stop paying for.
Against Sourcegraph you stop paying for the search engine you were already
getting. Against OpenGrok and livegrep you stop assembling the boundary they
leave to you. Against GitHub you keep your code, and the evidence, inside
your own infrastructure.

Every tool on this page can find you a match. phebs is built for the next
question: what is the match allowed to mean?
