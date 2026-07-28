# phebs web UI

The UI is a React 19 + TypeScript application embedded into the phebs Go
binary. It uses Vite, Base Web/Styletron, CodeMirror 6, a small hash router, and
Vitest with Testing Library.

User workflows and behavior belong in the
[product workflow guide](../docs/guides/WORKFLOWS.md). This file is for UI
contributors.

## Development loop

Use the repository-pinned Node version from `.node-version`.

In one terminal, start the API and fixture-backed development services:

```bash
make dev-api
```

In another:

```bash
cd ui
npm ci
npm run dev
```

Vite serves <http://127.0.0.1:5173> and proxies `/api` to
<http://127.0.0.1:3070>. Hash routes (`#/search`, `#/repos`, and so on) allow
the production UI to be served from the embedded static filesystem without a
server-side route fallback.

## Quality commands

From `ui/`:

```bash
npm test       # Vitest, jsdom, and Testing Library
npm run lint   # Oxlint
npm run build  # TypeScript project build + production Vite bundle
```

Repository-level equivalents:

```bash
make ui-test
make ui
make ci-ui
```

`ui/dist/` is generated. A normal `go build ./...` uses the placeholder UI;
`make build` and `make dev` build the production bundle and compile with the
`ui` build tag so `embed.go` includes it.

## Structure

| Path | Responsibility |
|---|---|
| `src/Root.tsx` | theme and authentication providers |
| `src/App.tsx` | application shell and route composition |
| `src/router.ts` | dependency-free hash navigation |
| `src/api.ts` | typed HTTP client and response shapes |
| `src/auth.tsx` / `authSession.ts` | session state and authenticated boundaries |
| `src/pages/` | route-level product surfaces |
| `src/components/` | shared evidence and interaction components |
| `src/theme.ts` | Base Web themes and phebs semantic tokens |
| `src/highlight.ts` / `lang.ts` | source-language presentation |
| `src/glossary.generated.ts` | generated Change Workbench vocabulary |

Tests live beside the component or page they cover.

## UI contracts

- Use the shared API client; do not recreate authorization, evidence,
  pagination, or conclusion logic in a page.
- Replace bounded pages rather than accumulating fleet-sized result sets in
  the DOM.
- Keep resolved evidence, name matches, unresolved candidates, coverage, and
  human records visually distinct.
- Preserve exact repository/commit/path/span links.
- Render failed, stale, partial, unsupported, inaccessible, and empty states
  explicitly.
- Use Base Web controls and semantic tokens rather than local hex colors or a
  second component system.
- Preserve keyboard operation, visible focus, landmarks, labels, live status,
  and the tested 390 px layout.
- Do not edit `src/glossary.generated.ts` directly. Change
  `internal/glossary/glossary.json`, run `go generate ./internal/glossary`, and
  verify with `make verify-glossary`.

Experimental/default-dark surfaces must retain their capability and evidence
caveats in UI tests and copy.
