# Bounded T25 documentation comparison

This standalone suite captures the authenticated documentation index,
Getting Started guide, and explicit repository-only landing page at 1280px
and 390px in light/dark: four tests and twelve unmasked viewport captures.
Documentation has no density mode. It uses the real auth/session/store
fixture, without mocked APIs, remote images, or the ordinary product matrix.

Author and compare in the existing Playwright 1.62.1 Noble receipt image,
`mcr.microsoft.com/playwright:v1.62.1-noble@sha256:c091b21d9fae78c76e85cd4356431e9b018402f172a214fc7d7a5e9a7e29d8ac`,
with its package-matched Chromium. Preserve the existing zero retries,
single worker, 1x device scale, and 0.001 maximum pixel-difference ratio.
Native Chrome screenshots in `receipt.json` remain separate functional
evidence and must not become these canonical baselines.

For each pass, start a new instance of the bounded fixture from the exact
source tree, using a fresh private temporary ready-file path:

```sh
PHEBS_DOCS_BROWSER_READY=/private/temporary/ready \
  go test -tags ui ./cmd/phebs \
  -run '^TestDocumentationRenderedBrowserFixture$' -count=1 -timeout=3m -v
```

The test creates fresh store/auth state, writes its random listener URL to
the ready file, and waits at most two minutes. Point the existing local
renderer TCP bridge at that listener and set `PHEBS_DOCS_RECEIPTS_URL` to
the bridge's container-visible URL. Use the receipt runtime's already
installed `@playwright/test` package; no dependency download is necessary.
Then, inside that digest-pinned renderer with the repository mounted:

```sh
playwright test --config spike/t251/canonical.config.cjs \
  --update-snapshots=all --reporter=list,json
```

Create the ready file's `.stop` sibling in a `finally`/trap cleanup and join
the Go process, even on browser failure. Each fixture invocation independently
creates/destroys its temporary store. Start another fresh fixture and repeat
the command **without** `--update-snapshots=all` for comparison. Do not retry
an unexpected comparison result or weaken masks/thresholds. Capture renderer
identity, exact source and embedded-document hashes, author/comparison reports,
PNG digests, wall times, failures, and joined child/server/browser teardown
in a separate canonical result receipt. Review the authored PNGs before
committing them; do not edit pixels.

Current status: static preparation only. No canonical screenshot pass is
claimed by this document.
