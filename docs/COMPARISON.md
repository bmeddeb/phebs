# phebs compared to other code search tools

This page helps you decide whether phebs fits your situation. It compares phebs
with the tools people most often evaluate alongside it: Sourcegraph,
OpenGrok, livegrep, and GitHub's own code search.

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
| MCP server for agents | Included | Included (enterprise) | No | No |
| Change-impact / contract analysis | Experimental, default-dark | No | No | No |

## Sourcegraph

Sourcegraph is the most capable and most mature product in this space: code
search and navigation across very large fleets, batch changes, code insights,
SSO, and deep admin tooling, deployable self-hosted or air-gapped.

As of 2026 it is **enterprise-only and sales-led**: the free and Pro tiers were
retired in mid-2025, and public reporting puts the platform at roughly
$59/user/month (annual) with enterprise contracts reported to start near
$16K/year (verified 2026-08-24 against public pricing coverage; confirm with
Sourcegraph sales). phebs uses Sourcegraph's own zoekt engine for search, so
raw search quality is in the same family.

Choose Sourcegraph if you have an enterprise budget and need its breadth at
very large scale. Choose phebs if you want self-hosted code search and
navigation without a sales process, in a single Go binary you can read end to
end.

## OpenGrok

OpenGrok is the veteran: a Java webapp with a fast source cross-reference
engine, free under CDDL-1.0, and battle-tested on very large trees (it was
built for OpenSolaris). It has no meaningful notion of authentication,
permissions, or audit, an older UI, and no agent-facing API.

Choose OpenGrok if you want the simplest possible "browse and grep a big source
tree" appliance and its UI works for you. Choose phebs if you need multi-host
sync, auth and permissions, an OpenAPI/MCP surface, or a modern UI.

## livegrep

livegrep (Apache-2.0) does one thing extremely well: interactive regex search
over large codebases with instant results. It has a deliberately minimal UI and
no repository management, auth, or navigation.

Choose livegrep if regex-at-speed over a fixed corpus is the whole job. Choose
phebs when you also need browsing, history/blame, SCIP navigation, sync from
code hosts, and access control.

## GitHub code search

If every repository you care about already lives on GitHub and a hosted service
is acceptable, GitHub's built-in code search is good and costs nothing extra.
phebs exists for the cases where that is not true: code spread across GitLab,
Gitea, plain Git servers, and local checkouts; code that must not leave your
infrastructure; or evidence and audit requirements a hosted search box does not
satisfy.

## When phebs is not the right choice

Being explicit about limits is part of the project:

- **Scale.** phebs is single-node today. It is running a measured
  scale-convergence program (see the roadmap), but it makes no horizontal-scale
  or very-large-fleet claim yet. Sourcegraph is the proven option at that end.
- **Maturity.** Sourcegraph has a larger team, more connectors,
  and years of production hardening. phebs is a young, fast-moving project.
- **Revisions.** phebs treats HEAD as authoritative, with at most seven
  explicit branch/tag revisions per repository. If you need every branch of
  every repo indexed continuously, Sourcegraph fits better.
- **Experimental evidence packs.** Contract Atlas, Caller Map, Impact, Topics,
  and the Workbench are experimental and default-dark. They make no
  completeness or accuracy claim, and the retained external Go/gRPC validation
  gate is `NOT_ESTABLISHED`. Evaluate them as a direction, not a guarantee.

## Decision summary

- Need enterprise scale, batch changes, and have budget → **Sourcegraph**.
- Want a battle-tested read-only source browser → **OpenGrok**.
- Want pure interactive regex speed → **livegrep**.
- All code is on GitHub and hosted is fine → **GitHub code search**.
- Want Apache-2.0 self-hosted search, navigation, auth, audit, and MCP in one
  Go binary — with an experimental path toward static change-impact evidence →
  **phebs**.
