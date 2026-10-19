import type { Page } from '@playwright/test'
import type { ContractCatalogClaim, ContractCatalogList, ContractCatalogOperation } from '../src/api'

const repository = 'example.invalid/atlas-receipt'
const commit = 'a'.repeat(40)
const lineage = `provisional_repo_path_v1_${'b'.repeat(64)}`
const run = 'atlas-receipt-declarations'
const operation = '/demo.v1.Catalog/Get'
const authority = {
  repository, domain: 'proto-contract', run_id: run, commit,
  plan_digest: `sha256:${'c'.repeat(64)}`, root_digest: `sha256:${'d'.repeat(64)}`,
}
const coverage = {
  schema_version: 'coverage-certificate-v3', domains: ['proto-contract'], repository_count: 1,
  repositories: [{ repository, indexed_commit: commit, scip_index: 'absent', runs: [{
    domain: 'proto-contract', status: 'unpublished', fresh: false,
    corpus_file_count: 1, candidate_file_count: 0, read_file_count: 0, read_bytes: 0,
    unresolved_count: 0, assertion_count: 0, atom_count: 0,
  }] }],
  digest: `sha256:${'e'.repeat(64)}`,
}
const caveat = 'Source declarations within the displayed snapshot; no runtime use or completeness conclusion.'
function declaration(service = false): ContractCatalogClaim {
  return {
    assertion_id: service ? 'decl-service' : 'decl-operation', run_id: run,
    predicate: service ? 'DECLARES_SERVICE' : 'DECLARES_OPERATION',
    object: service ? 'demo.v1.Catalog' : 'demo.v1.Catalog/Get', lineage, tier: 'exact',
    sources: [{ repository, commit, path: 'api/catalog.proto', start_byte: 39, end_byte: 91,
      start_line: 3, end_line: 3, assertion_id: service ? 'decl-service' : 'decl-operation',
      run_id: run, atom_id: 'atom-declaration' }],
    sources_truncated: false,
  }
}
export const atlasDeclarationList: ContractCatalogList = {
  schema_version: 'contract-catalog-v1', query: {},
  items: [
    { kind: 'service', protocol: 'protobuf', repository, declaration_lineage: lineage,
      package: 'demo.v1', service_fqn: 'demo.v1.Catalog', declaration: declaration(true) },
    { kind: 'operation', protocol: 'protobuf', repository, declaration_lineage: lineage,
      package: 'demo.v1', service_fqn: 'demo.v1.Catalog', method: 'Get', operation, declaration: declaration() },
  ],
  exact_authorities: [authority], pagination: { complete: true, truncated: false },
  coverage_digest: coverage.digest, coverage, caveat,
}
export const atlasDeclarationDetail: ContractCatalogOperation = {
  schema_version: 'contract-catalog-v1', repository, declaration_lineage: lineage,
  service_fqn: 'demo.v1.Catalog', method: 'Get', operation,
  declaration: declaration(), exact_authority: authority,
  fact_detail: { schema: 'proto-operation-detail-v1',
    request: { raw: 'Request', resolution: 'same_file' }, response: { raw: 'Response', resolution: 'same_file' },
    client_streaming: false, server_streaming: false },
  request: { raw: 'Request', state: 'resolved', fields: [], truncated: false },
  response: { raw: 'Response', state: 'resolved', fields: [], truncated: false },
  implementations: [], callers: [], unresolved_candidates: [],
  unavailable_relationships: ['implementations', 'callers', 'unresolved_candidates'],
  relationships_truncated: false, shape_truncated: false, coverage_digest: coverage.digest, coverage, caveat,
}

export const ATLAS_DECLARATION_PATH = '/contracts?' + new URLSearchParams({
  sel_repository: repository, sel_lineage: lineage, sel_operation: operation, sel_protocol: 'protobuf',
})

// Presentation fixture only; signed native-source integration is recorded
// separately under spike/t494. These bytes grant no publication authority.
export async function installAtlasDeclarationFixture(page: Page) {
  await page.route('**/api/version', route => route.fulfill({
    contentType: 'application/json', body: JSON.stringify({ version: 'atlas-receipt', capabilities: ['contract-atlas'] }),
  }))
  await page.route('**/api/contract_atlas**', route => {
    const url = new URL(route.request().url())
    const value = url.pathname.endsWith('/operation') ? atlasDeclarationDetail : atlasDeclarationList
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify(value) })
  })
}
