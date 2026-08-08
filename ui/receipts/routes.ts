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
}

const T323_REPO = 'local/Users/ben/phebs-ux/spike/t323/t323-neutral-corpus.bundle'

export const ROUTES: ReceiptRoute[] = [
  { name: 'search', path: '/' },
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
  { name: 'search-service-authority-390', path: `/search?${q({ q: 'orders', scope: 'service', repository: T323_REPO, service_key: 'svc.fulfillment', authority: 'scope' })}`, viewport: { width: 390, height: 844 } },
  { name: 'repos', path: '/repos' },
  { name: 'service-directory', path: `/services?${q({ repository: T307_REPO })}` },
  { name: 'relationship-explorer', path: `/relationships?${q({ repository: T307_REPO })}` },
  { name: 'file', path: `/file?${q({ repo: T307_REPO, path: T307_FILE })}` },
  { name: 'history', path: `/history?${q({ repo: T307_REPO, path: T307_FILE })}` },
  { name: 'blame', path: `/blame?${q({ repo: T307_REPO, path: T307_FILE })}` },
  { name: 'commit', path: `/commit?${q({ repo: T307_REPO, ref: T307_COMMIT })}` },
  { name: 'contract-atlas', path: '/contracts' },
  { name: 'impact', path: '/impact' },
  { name: 'kafka-topics', path: '/topics' },
  { name: 'investigations', path: '/investigations' },
  { name: 'workbench', path: '/workbench' },
  { name: 'settings', path: '/settings' },
  { name: 'audit', path: '/audit', mask: ['main table'] },
  // Analytics content is usage-derived in whole (the harness's own searches
  // mutate it), so the body is masked and the receipt pins chrome only.
  { name: 'analytics', path: '/analytics', mask: ['main'] },
]

export const THEMES = ['light', 'dark'] as const

// Densities join the matrix when T43.11 lands the density control; until
// then every capture is the comfortable default.
export const DENSITIES = ['comfortable'] as const
