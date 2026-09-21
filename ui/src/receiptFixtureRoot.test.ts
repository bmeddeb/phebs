import { describe, expect, it } from 'vitest'
import {
  RECEIPT_FIXTURE_ROOT,
  T307_BUNDLE,
  T307_REPOSITORY,
  T323_BUNDLE,
  T323_REPOSITORY,
  receiptCohortReadiness,
  receiptFixtureRepoName,
} from '../receipts/fixtureRoot'
import { ROUTES } from '../receipts/routes'

// Neutral-demo repository identities are receipt-environment-bound, never
// checkout-bound: the staged bundles live at RECEIPT_FIXTURE_ROOT on every
// machine, and the server derives the same `local/...` names there. These
// assertions pin the exact deterministic strings so any reintroduction of
// checkout-derived names fails loudly instead of silently invalidating
// every committed baseline.
describe('deterministic receipt fixture identities', () => {
  it('fixture root is the fixed absolute POSIX path', () => {
    expect(RECEIPT_FIXTURE_ROOT).toBe('/tmp/phebs-receipts-fixtures')
  })

  it('derives the exact server RepoName for the staged bundles', () => {
    expect(receiptFixtureRepoName(T307_BUNDLE)).toBe(
      'local/tmp/phebs-receipts-fixtures/t307-neutral-service.bundle',
    )
    expect(receiptFixtureRepoName(T323_BUNDLE)).toBe(
      'local/tmp/phebs-receipts-fixtures/t323-neutral-corpus.bundle',
    )
  })

  it('every routed repository identity is fixture-root-bound', () => {
    const identities = ROUTES.flatMap((route) => {
      const query = route.path.split('?')[1] ?? ''
      return [...new URLSearchParams(query).entries()]
        .filter(([key]) => key === 'repo' || key === 'repository')
        .map(([, value]) => value)
    })
    expect(identities.length).toBeGreaterThan(0)
    for (const identity of identities) {
      // The markdown-preview route is served by a page-scoped synthetic
      // fixture that never enters instance repository state.
      if (identity === 'receipt-fixture/markdown-preview') continue
      expect(identity).toMatch(/^local\/tmp\/phebs-receipts-fixtures\//)
    }
  })

  it('no route leaks a checkout-derived identity', () => {
    for (const route of ROUTES) {
      expect(route.path).not.toMatch(/local\/(Users|home|root|private)\//)
    }
  })
})

const serviceInventory = (repository: string, keys: string[]) => ({
  schema: 'phebs-service-inventory-v1',
  repository: { repository, catalog_service_count: 5 },
  services: keys.map((key) => ({ key, disposition: 'accepted', status: 'current', removed: false })),
})

const readyObservation = () => ({
  repositories: [T307_REPOSITORY, T323_REPOSITORY].map((name) => ({
    name,
    indexed_commit_hash: 'a'.repeat(40),
    last_index_job_state: 'exact',
    last_index_job: { status: 'done' },
    last_extraction_job_state: 'exact',
    last_extraction_job: { status: 'done' },
  })),
  t307Services: serviceInventory(T307_REPOSITORY, ['orders-api', 'orders-events']),
  t323Services: serviceInventory(T323_REPOSITORY, ['svc.orders-api', 'svc.fulfillment']),
  t307Relationships: {
    schema: 'phebs-service-relationship-page-v1',
    query: { repositories: [T307_REPOSITORY], service_key: 'orders-api', view: 'all' },
    rows_state: 'nonempty',
    roots: [{ repository: T307_REPOSITORY, state: 'complete' }],
    rows: [{ repository: T307_REPOSITORY }],
    coverage: {
      authorized_repositories: 1,
      complete_roots: 1,
      failed_roots: 0,
      unavailable_roots: 0,
      truncated: false,
    },
  },
})

describe('receipt cohort readiness', () => {
  it('keeps empty and in-flight cohorts pending', () => {
    expect(receiptCohortReadiness({ repositories: [] }).state).toBe('pending')
    const observation = readyObservation()
    observation.repositories[0].last_extraction_job.status = 'running'
    expect(receiptCohortReadiness(observation).state).toBe('pending')
  })

  it('fails terminal jobs and unexpected repositories', () => {
    for (const status of ['failed', 'canceled']) {
      const observation = readyObservation()
      observation.repositories[0].last_extraction_job.status = status
      expect(receiptCohortReadiness(observation).state).toBe('failed')
    }
    const observation = readyObservation()
    observation.repositories.push({
      ...observation.repositories[0],
      name: 'github.com/unexpected/repository',
    })
    expect(receiptCohortReadiness(observation).state).toBe('failed')
  })

  it('accepts only the exact settled cohort and authorities', () => {
    expect(receiptCohortReadiness(readyObservation())).toEqual({
      state: 'ready',
      detail: 'receipt cohort is exact and settled',
    })
  })
})
