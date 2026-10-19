import type { Page, Route } from '@playwright/test'
import type { CallerMapPage, CallerMapRow } from '../src/api'
import { navigationFixture } from './navigationFixture'
import { atlasDeclarationDetail as detail, atlasDeclarationList as catalog, ATLAS_DECLARATION_PATH } from './atlasFixture'

export { ATLAS_DECLARATION_PATH }
export const CALLER_AUDIT_PATH = '/callers?' + new URLSearchParams({
  protocol: 'protobuf', repository: detail.repository, lineage: detail.declaration_lineage, operation: detail.operation,
})

export async function callerAtlasFixture(page: Page) {
  const fixture = await navigationFixture(page)
  const callers: URLSearchParams[] = []
  const lists: URLSearchParams[] = []
  const selections: URLSearchParams[] = []
  let badCatalog = false
  let badSelection = false
  let badCitation = false
  let holdSelection = false
  let held: Route | undefined
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    let value: unknown
    if (url.pathname === '/api/version') value = { version: 'caller-atlas-audit', capabilities: ['contract-atlas', 'contract-caller-map'] }
    else if (url.pathname === '/api/contract_atlas') {
      lists.push(url.searchParams)
      value = { ...catalog, query: badCatalog ? { protocol: 'thrift' } : Object.fromEntries(['repository', 'package', 'protocol', 'lineage'].filter(key => url.searchParams.has(key)).map(key => [key, url.searchParams.get(key)])),
        items: url.searchParams.has('package') ? [] : catalog.items }
    } else if (url.pathname === '/api/contract_atlas/operation') {
      selections.push(url.searchParams)
      if (holdSelection) { held = route; return }
      value = badSelection ? { ...detail, exact_authority: undefined, operation: '/foreign.Service/Other' } : detail
    } else if (url.pathname === '/api/contract_callers') {
      callers.push(url.searchParams)
      value = callerPage(url.searchParams)
    } else if (url.pathname === '/api/contract_callers/citation') {
      value = {
        schema_version: 'caller-map-citation-v1', generation: callerPage(new URLSearchParams()).generation,
        source: { ...row().source, ...(badCitation ? { commit: 'f'.repeat(40) } : {}) }, content: 'client.Get(ctx)',
      }
    } else { await route.fallback(); return }
    await route.fulfill({ json: value })
  })
  return {
    ...fixture, callers, lists, selections,
    badCatalog: (value: boolean) => { badCatalog = value },
    badSelection: (value: boolean) => { badSelection = value },
    badCitation: (value: boolean) => { badCitation = value },
    holdSelection: () => { holdSelection = true },
    selectionHeld: () => held !== undefined,
    releaseSelection: async () => { holdSelection = false; await held!.fulfill({ json: detail }); held = undefined },
  }
}

function row(): CallerMapRow {
  return {
    classification: 'resolved_caller', resolution: 'syntax', protocol: 'protobuf', operation: detail.operation,
    declaration_lineage: detail.declaration_lineage, tier: 'heuristic', code_role: 'production', fresh: true,
    unit_group: 'demo-client', unit: { state: 'resolved', candidates: [{ id: 'demo-client', logical_services: ['demo-client'], owners: ['team-demo'] }] },
    source: {
      repository: 'example.invalid/demo-client', commit: 'a'.repeat(40), path: 'client.go',
      object_id: 'b'.repeat(40), blob_digest: 'sha256:' + 'c'.repeat(64), plane: 'repository-overlay',
      start_byte: 0, end_byte: 15, start_line: 1, end_line: 1,
      assertion_id: 'caller-1', run_id: 'caller-audit', atom_id: 'caller-atom', citation: 'audit-exact-citation',
    },
  }
}

function callerPage(params: URLSearchParams): CallerMapPage {
  return {
    schema_version: 'caller-map-v2',
    query: {
      endpoint: { protocol: 'protobuf', repository: detail.repository, declaration_lineage: detail.declaration_lineage, operation: detail.operation },
      owner: params.get('owner') ?? undefined, freshness: 'any', resolution: 'any', ordering: 'source',
    },
    declaration: detail.declaration, rows: [row()], groups: [], total_matching_rows: 1,
    pagination: { complete: true }, matching_rows_state: 'exact',
    generation: { state: 'current', plane: 'repository-overlay', repository: row().source.repository,
      commit: 'a'.repeat(40), generation_digest: 'sha256:' + 'd'.repeat(64), publication_revision: 1 },
    caveat: 'Static source evidence within the displayed snapshot; no runtime or completeness conclusion.',
  }
}
