// Canonical receipt fixture root.
//
// Screenshot receipts must render byte-identical repository identities on
// every machine: the neutral-demo repository names reach the pixels
// unmasked, and the committed baselines pin them. Deriving those names from
// the developer's checkout path makes the identity — and therefore every
// baseline — checkout-bound, so a macOS checkout and Ubuntu CI could never
// share one image set.
//
// Instead, receipt runs stage the neutral-demo bundles at this fixed,
// documented root before booting the dev instance
// (scripts/stage-receipt-fixtures.sh), and `make dev` / `make dev-api`
// honor the staged paths when the fixture env vars are pre-set. The
// server's production repository-identity rule is unchanged
// (internal/sync RepoName: `local/` + the absolute bundle path without its
// leading slash); only the receipt environment's input to that rule is
// fixed. The string is identical on macOS and Linux, so one baseline set
// serves every supported renderer — baselines are produced in the Ubuntu CI
// rendering environment (see ui/receipts/README.md).
//
// This constant is the single source of truth: the staging script extracts
// it from this file, and routes.ts derives every neutral-demo repository
// identity from it. Keep it an absolute POSIX path with no trailing slash.
export const RECEIPT_FIXTURE_ROOT = '/tmp/phebs-receipts-fixtures'

// Neutral-demo bundle file names, staged at RECEIPT_FIXTURE_ROOT.
export const T307_BUNDLE = 't307-neutral-service.bundle'
export const T323_BUNDLE = 't323-neutral-corpus.bundle'

// Mirror of the server's local-path RepoName rule for an absolute, clean
// POSIX bundle path: `local/` + the path without its leading slash.
// safeName applies no transformation (internal/sync/sync.go), so this is
// exact for the staged fixture paths.
export function receiptFixtureRepoName(bundleFileName: string): string {
  return `local/${RECEIPT_FIXTURE_ROOT.replace(/^\/+/, '')}/${bundleFileName}`
}

export const T307_REPOSITORY = receiptFixtureRepoName(T307_BUNDLE)
export const T323_REPOSITORY = receiptFixtureRepoName(T323_BUNDLE)

export interface ReceiptCohortObservation {
  repositories: unknown
  t307Services?: unknown
  t323Services?: unknown
  t307Relationships?: unknown
}

export interface ReceiptCohortReadiness {
  state: 'pending' | 'failed' | 'ready'
  detail: string
}

const asRecord = (value: unknown): Record<string, unknown> | undefined =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined

const failedJob = (row: Record<string, unknown>): string | undefined => {
  for (const field of ['last_index_job', 'last_extraction_job', 'last_resolver_job', 'last_caller_job']) {
    const status = asRecord(row[field])?.status
    if (status === 'failed' || status === 'canceled') return `${field}:${status}`
  }
  return undefined
}

function repositoryReadiness(value: unknown): ReceiptCohortReadiness {
  if (!Array.isArray(value)) return { state: 'failed', detail: 'repo-status response is not an array' }
  const rows = value.map(asRecord)
  if (rows.some((row) => row === undefined)) return { state: 'failed', detail: 'repo-status contains a malformed row' }

  const expected = new Set([T307_REPOSITORY, T323_REPOSITORY])
  for (const row of rows as Record<string, unknown>[]) {
    const name = row.name
    if (typeof name !== 'string' || !expected.has(name)) {
      return { state: 'failed', detail: `unexpected receipt repository ${String(name)}` }
    }
    const failure = failedJob(row)
    if (failure) return { state: 'failed', detail: `${name} ${failure}` }
  }
  if (rows.length !== expected.size) return { state: 'pending', detail: `repo rows ${rows.length}/${expected.size}` }
  if (new Set((rows as Record<string, unknown>[]).map((row) => row.name)).size !== expected.size) {
    return { state: 'failed', detail: 'repo-status contains a duplicate receipt repository' }
  }

  for (const row of rows as Record<string, unknown>[]) {
    const index = asRecord(row.last_index_job)
    const extraction = asRecord(row.last_extraction_job)
    if (row.last_index_job_state !== 'exact' || index?.status !== 'done' ||
      typeof row.indexed_commit_hash !== 'string' || !/^[0-9a-f]{40}$/.test(row.indexed_commit_hash)) {
      return { state: 'pending', detail: `${String(row.name)} indexing is not settled` }
    }
    if (row.last_extraction_job_state !== 'exact' || extraction?.status !== 'done') {
      return { state: 'pending', detail: `${String(row.name)} extraction is not settled` }
    }
    for (const field of ['last_resolver_job', 'last_caller_job']) {
      const job = asRecord(row[field])
      if (job && job.status !== 'done') {
        return { state: 'pending', detail: `${String(row.name)} ${field} is not settled` }
      }
    }
  }
  return { state: 'ready', detail: 'repositories indexed' }
}

function serviceReadiness(
  value: unknown,
  repository: string,
  requiredKeys: string[],
): ReceiptCohortReadiness {
  if (value === undefined) return { state: 'pending', detail: `${repository} services unavailable` }
  const inventory = asRecord(value)
  const authority = asRecord(inventory?.repository)
  const services = inventory?.services
  if (inventory?.schema !== 'phebs-service-inventory-v1' ||
    authority?.repository !== repository || authority.catalog_service_count !== 5 ||
    !Array.isArray(services)) {
    return { state: 'failed', detail: `${repository} service inventory is malformed or unexpected` }
  }
  const rows = services.map(asRecord)
  if (rows.some((row) => row === undefined)) {
    return { state: 'failed', detail: `${repository} service inventory contains a malformed row` }
  }
  const current = new Set((rows as Record<string, unknown>[])
    .filter((service) => service.disposition === 'accepted' && service.status === 'current' && service.removed === false)
    .map((service) => service.key))
  if (!requiredKeys.every((key) => current.has(key))) {
    return { state: 'pending', detail: `${repository} required services are not current` }
  }
  return { state: 'ready', detail: `${repository} services current` }
}

function relationshipReadiness(value: unknown): ReceiptCohortReadiness {
  if (value === undefined) return { state: 'pending', detail: 'relationship authority unavailable' }
  const page = asRecord(value)
  const query = asRecord(page?.query)
  const coverage = asRecord(page?.coverage)
  if (page?.schema !== 'phebs-service-relationship-page-v1' ||
    query?.service_key !== 'orders-api' ||
    query?.view !== 'all' ||
    !Array.isArray(query.repositories) || query.repositories.length !== 1 ||
    query.repositories[0] !== T307_REPOSITORY || !Array.isArray(page?.roots) ||
    !Array.isArray(page?.rows)) {
    return { state: 'failed', detail: 'relationship response is malformed or unexpected' }
  }
  if (page.rows_state !== 'nonempty' || page.roots.length !== 1 || page.rows.length === 0 ||
    coverage?.authorized_repositories !== 1 || coverage.complete_roots !== 1 ||
    coverage.failed_roots !== 0 || coverage.unavailable_roots !== 0 ||
    coverage.truncated !== false) {
    return { state: 'pending', detail: 'relationship authority is not complete' }
  }
  return { state: 'ready', detail: 'relationship authority complete' }
}

export function receiptCohortReadiness(observation: ReceiptCohortObservation): ReceiptCohortReadiness {
  const states = [
    repositoryReadiness(observation.repositories),
    serviceReadiness(observation.t307Services, T307_REPOSITORY, ['orders-api', 'orders-events']),
    serviceReadiness(observation.t323Services, T323_REPOSITORY, ['svc.orders-api', 'svc.fulfillment']),
    relationshipReadiness(observation.t307Relationships),
  ]
  return states.find((state) => state.state === 'failed') ??
    states.find((state) => state.state === 'pending') ??
    { state: 'ready', detail: 'receipt cohort is exact and settled' }
}
