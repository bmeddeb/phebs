// The receipts manifest: every routed surface with a deterministic address
// on the make-dev neutral cohorts. Baselines are environment-bound
// engineering records: repository names embed this checkout's path and the
// t307 bundle pin, and relative-time copy binds to the dev instance's index
// generation — rebuild the instance, re-review, and refresh explicitly.

// Date.now() is fixed to this instant during capture so age copy cannot
// drift within an instance generation.
export const FROZEN_NOW = new Date('2026-08-07T20:00:00Z')

const T307_REPO = 'local/Users/ben/phebs-ux/docs/fixtures/t30.7-neutral-service/t307-neutral-service.bundle'
const T307_COMMIT = 'b7f443ed7e89dbaede855a6cfd30767bbe13dfbb'
const T307_FILE = 'api/orders.proto'

const q = (params: Record<string, string>) => new URLSearchParams(params).toString()

export interface ReceiptRoute {
  name: string
  path: string
  // Regions that mutate BECAUSE a receipts run happens (e.g. the audit log
  // records the harness's own login) are masked, never baselined.
  mask?: string[]
  // Per-route viewport override; default is the project's desktop viewport.
  viewport?: { width: number; height: number }
  // Open every <details> in main before capture so collapsed disclosures
  // (ClaimBoundary establishes/does-not-establish bodies) are pinned too.
  expand?: boolean
  // Terminal-state condition: capture only once none of these strings appear
  // in main. Guards transitional instance states (e.g. post-restart index
  // rebuilds) that are real but not the surface's settled truth.
  awaitAbsent?: string[]
  // Interactions performed after readiness, before capture. Each selector
  // MUST match — a missing element fails the capture instead of silently
  // producing a baseline of the wrong state.
  click?: string[]
  // Keyboard chords pressed after readiness (e.g. the command navigator's
  // Control+k), before capture.
  press?: string[]
}

const T323_REPO = 'local/Users/ben/phebs-ux/spike/t323/t323-neutral-corpus.bundle'

export const ROUTES: ReceiptRoute[] = [
  { name: 'search', path: '/' },
  // T43.9: the keyboard navigator, opened by its own chord.
  { name: 'command-navigator', path: '/', press: ['Control+k'] },
  // Single-repository query: multi-repo streams render groups in arrival
  // order (nondeterministic — tracked for a stable-ordering fix), so the
  // results + authority-chip surface is pinned on a one-repo result set.
  { name: 'search-results', path: '/search?q=%22T323_ORDERS_API_PRODUCER%22' },
  // T43.5 states: the open authority drawer (disclosure is URL state), the
  // service-scoped receipt chip, the open service drawer with the service
  // authority generations, and the drawer at 390px.
  { name: 'search-results-authority', path: '/search?q=%22T323_ORDERS_API_PRODUCER%22&authority=scope' },
  { name: 'search-service', path: `/search?${q({ q: 'orders', scope: 'service', repository: T323_REPO, service_key: 'svc.fulfillment' })}` },
  { name: 'search-service-authority', path: `/search?${q({ q: 'orders', scope: 'service', repository: T323_REPO, service_key: 'svc.fulfillment', authority: 'scope' })}` },
  // The scope context bar's own 390px state (T43.8): scoped service search
  // without the drawer overlaying it.
  { name: 'search-service-390', path: `/search?${q({ q: 'orders', scope: 'service', repository: T323_REPO, service_key: 'svc.fulfillment' })}`, viewport: { width: 390, height: 844 } },
  { name: 'search-service-authority-390', path: `/search?${q({ q: 'orders', scope: 'service', repository: T323_REPO, service_key: 'svc.fulfillment', authority: 'scope' })}`, viewport: { width: 390, height: 844 } },
  // A server restart queues index rebuilds; repos is captured only in its
  // terminal all-indexed state (twice this trap produced mid-reindex
  // baselines that were rejected in review).
  { name: 'repos', path: '/repos', awaitAbsent: ['Indexing…', 'Cloning'] },
  { name: 'service-directory', path: `/services?${q({ repository: T307_REPO })}` },
  { name: 'relationship-explorer', path: `/relationships?${q({ repository: T307_REPO })}` },
  // T43.6 data-backed states: the service-scoped explorer (coverage strip,
  // gap band, server-delivered ClaimBoundary — collapsed and expanded) and
  // the topic-usage answer (census-leads line, census table, bundle caveat),
  // including a 390px expanded state.
  { name: 'relationship-explorer-service', path: `/relationships?${q({ repository: T307_REPO, service_key: 'orders-api' })}` },
  { name: 'relationship-explorer-service-expanded', path: `/relationships?${q({ repository: T307_REPO, service_key: 'orders-api' })}`, expand: true },
  { name: 'kafka-topic-usage', path: `/topics?${q({ topic: 'orders.created.v1' })}` },
  { name: 'kafka-topic-usage-expanded-390', path: `/topics?${q({ topic: 'orders.created.v1' })}`, expand: true, viewport: { width: 390, height: 844 } },
  { name: 'file', path: `/file?${q({ repo: T307_REPO, path: T307_FILE })}` },
  { name: 'history', path: `/history?${q({ repo: T307_REPO, path: T307_FILE })}` },
  { name: 'blame', path: `/blame?${q({ repo: T307_REPO, path: T307_FILE })}` },
  { name: 'commit', path: `/commit?${q({ repo: T307_REPO, ref: T307_COMMIT })}` },
  { name: 'contract-atlas', path: '/contracts' },
  // Identity-required routes are still complete routed surfaces. These
  // receipts pin their honest required-input states until deterministic
  // endpoint fixtures are added with the owning workflow tickets.
  { name: 'caller-map-required-identity', path: '/callers' },
  { name: 'caller-comparison-required-identity', path: '/compare-callers' },
  { name: 'impact', path: '/impact' },
  { name: 'kafka-topics', path: '/topics' },
  { name: 'investigations', path: '/investigations' },
  { name: 'workbench', path: '/workbench' },
  // Only the lifecycle VALUES are live host telemetry (disk pressure, owner
  // turns, the capacity badge); mask those precisely so the section's
  // heading, layout, policy sentence, and metric anatomy stay compared.
  {
    name: 'settings',
    path: '/settings',
    mask: ['[data-volatile="lifecycle"]', 'section[aria-labelledby="lifecycle-heading"] [role="status"]'],
  },
  // The run mutates audit values, not table anatomy. Mask only the dynamic
  // cell contents so headers, rows, spacing, borders, and status-chip layout
  // remain under visual comparison.
  { name: 'audit', path: '/audit', mask: ['main tbody td > *'] },
  // Analytics content is usage-derived in whole (the harness's own searches
  // mutate it), so the body is masked and the receipt pins chrome only.
  { name: 'analytics', path: '/analytics', mask: ['main'] },
]

export const THEMES = ['light', 'dark'] as const

// Densities join the matrix when T43.11 lands the density control; until
// then every capture is the comfortable default.
export const DENSITIES = ['comfortable'] as const
