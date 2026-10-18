import type { Page, Route } from '@playwright/test'
import type { ServiceDetail, ServiceInventory, ServiceRecord, ServiceRepository } from '../src/api'
import { navigationFixture, navigationRepositories as repos, navigationCommits as commits } from './navigationFixture'

export async function repositoryServiceFixture(page: Page) {
  const fixture = await navigationFixture(page)
  const folders: { repo: string; ref: string; path: string }[] = []
  const inventoryRequests: string[] = []
  const detailRequests: string[] = []
  let statusReads = 0
  let foreignInventory = false
  let foreignDetail = false
  let heldKey = ''
  const held: { route: Route; value: ServiceDetail }[] = []
  page.on('request', request => { if (new URL(request.url()).pathname === '/api/repo-status') statusReads++ })
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    const repo = url.searchParams.get('repository') ?? repos[0]
    let value: unknown
    if (url.pathname === '/api/version') value = { version: 'repository-service-fixture', capabilities: ['service-catalog-v2'] }
    else if (url.pathname === '/api/folder_contents') {
      const request = { repo: url.searchParams.get('repo') ?? '', ref: url.searchParams.get('ref') ?? '', path: url.searchParams.get('path') ?? '' }
      folders.push(request)
      if (request.ref !== commits[repos.indexOf(request.repo)]) fixture.unexpected.push('crossed folder revision')
      value = { entries: request.path ? [{ name: 'main.go', type: 'file' }] : [{ name: 'src', type: 'dir' }] }
    } else if (url.pathname === '/api/services') {
      inventoryRequests.push(repo)
      const services = [service(repo, 'billing', 'Billing API'), service(repo, 'orders', 'Orders API')]
      if (foreignInventory) services[0].repository = 'example.test/foreign'
      value = {
        schema: 'phebs-service-inventory-v1', repository: repository(repo),
        filters: { repository: repo }, services,
        pagination: { order: 'service_key:asc', page_size: 50, returned: 2 },
      } satisfies ServiceInventory
    } else if (url.pathname === '/api/service') {
      const key = url.searchParams.get('service_key') ?? ''
      detailRequests.push(key)
      const detail = {
        schema: 'phebs-service-detail-v1', repository: repository(repo),
        service: service(repo, foreignDetail ? 'foreign-service' : key, key === 'billing' ? 'Billing API' : 'Orders API'),
        successors: [], memberships: [{ path: `services/${key}`, role: 'primary', origin: 'base' }],
      } satisfies ServiceDetail
      if (key === heldKey) { held.push({ route, value: detail }); return }
      value = detail
    } else { await route.fallback(); return }
    await route.fulfill({ json: value })
  })
  return {
    ...fixture, folders, inventoryRequests, detailRequests,
    statusReads: () => statusReads,
    foreignInventory: (value: boolean) => { foreignInventory = value },
    foreignDetail: (value: boolean) => { foreignDetail = value },
    holdDetails: (key: string) => { heldKey = key },
    detailsHeld: () => held.length,
    releaseDetails: async () => {
      heldKey = ''
      for (const item of held.splice(0)) await item.route.fulfill({ json: item.value })
    },
  }
}

function repository(repo: string): ServiceRepository {
  return {
    repository: repo, source_kind: 'operator', source_commit: commits[repos.indexOf(repo)],
    source_file_count: 4, accepted_file_count: 2, unowned_file_count: 2,
    authority: { kind: 'operator', id: 'navigation-demo', version: 'v1' },
    catalog_digest: `sha256:${'a'.repeat(64)}`, catalog_generation: `sha256:${'b'.repeat(64)}`,
    catalog_control_revision: 2, state_control_revision: 7,
    catalog_service_count: 2, live_service_count: 2, current_count: 2, stale_count: 0,
    unavailable_count: 0, conflict_count: 0, tombstone_count: 0,
    published_at: '2026-10-18T12:00:00Z', state_updated_at: '2026-10-18T12:00:00Z',
  }
}

function service(repo: string, key: string, name: string): ServiceRecord {
  return {
    repository: repo, key, display_name: name, disposition: 'accepted', origin: 'base',
    successor_count: 0, incarnation: 1, status: 'current', removed: false,
    desired_generation: `sha256:${(key === 'billing' ? 'd' : 'c').repeat(64)}`, active_desired_generation: `sha256:${(key === 'billing' ? 'd' : 'c').repeat(64)}`,
    desired_source_generation: `sha256:${'d'.repeat(64)}`, active_source_generation: `sha256:${'d'.repeat(64)}`,
    desired_catalog_generation: `sha256:${'e'.repeat(64)}`, active_catalog_generation: `sha256:${'e'.repeat(64)}`,
    membership_count: 1, distinct_path_count: 1,
    role_counts: { primary: 1, supporting: 0, shared: 0, generated: 0, typed: 0 },
    state_digest: `sha256:${'3'.repeat(64)}`, control_revision: 4, changed_at: '2026-10-18T12:00:00Z',
  }
}
