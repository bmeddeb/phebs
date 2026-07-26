import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, test } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import ContractDependencyMap from './ContractDependencyMap'
import {
  buildDependencyEdges,
  layoutDependencyEdges,
} from './contractDependency'
import type {
  ContractCatalogClaim,
  ContractCatalogOperation,
  ContractCatalogRelationship,
  ContractCatalogSource,
} from '../api'

const commit = 'a'.repeat(40)

function source(id: string, repository = 'github.com/acme/service'): ContractCatalogSource {
  return {
    repository,
    commit,
    path: `src/${id}.go`,
    start_byte: 10,
    end_byte: 20,
    start_line: 7,
    end_line: 7,
    assertion_id: `assertion-${id}`,
    run_id: `run-${id}`,
    atom_id: `atom-${id}`,
  }
}

function claim(
  id: string,
  sources: ContractCatalogSource[],
  lineage = 'declaration-lineage',
  tier = 'derived',
): ContractCatalogClaim {
  return {
    assertion_id: `assertion-${id}`,
    run_id: `run-${id}`,
    predicate: 'CALLS_OPERATION',
    object: '/demo.Catalog/Get',
    lineage,
    tier,
    code_role: 'production',
    sources,
    sources_truncated: false,
  }
}

function relationship(
  kind: ContractCatalogRelationship['kind'],
  id: string,
  sources: ContractCatalogSource[],
): ContractCatalogRelationship {
  return {
    kind,
    classification: kind === 'implementation'
      ? 'resolved_implementation'
      : kind === 'caller'
        ? 'unresolved_name_match'
        : 'extractor_abstention',
    reason: kind === 'implementation' ? undefined : 'DECLARATION_LINEAGE_UNPROVEN',
    claim: claim(id, sources, kind === 'implementation' ? 'declaration-lineage' : `lineage-${id}`),
  }
}

function operation(): ContractCatalogOperation {
  const declaration = claim('declaration', [source('declaration')], 'declaration-lineage', 'exact')
  return {
    schema_version: 'contract-atlas-v2',
    repository: 'github.com/acme/contracts',
    declaration_lineage: 'declaration-lineage',
    service_fqn: 'demo.Catalog',
    method: 'Get',
    operation: '/demo.Catalog/Get',
    declaration,
    fact_detail: {
      schema: 'proto-operation-detail-v1',
      request: { raw: 'Request', resolution: 'same_file' },
      response: { raw: 'Response', resolution: 'same_file' },
      client_streaming: false,
      server_streaming: false,
    },
    request: { raw: 'Request', state: 'resolved', fields: [], truncated: false },
    response: { raw: 'Response', state: 'resolved', fields: [], truncated: false },
    implementations: [
      relationship('implementation', 'registration', [source('registration-b'), source('registration-a')]),
    ],
    callers: [
      relationship('caller', 'caller', [source('caller', 'github.com/acme/client')]),
    ],
    unresolved_candidates: [
      relationship('unresolved_candidate', 'unknown', [source('unknown', 'github.com/acme/client')]),
    ],
    relationships_truncated: false,
    shape_truncated: false,
    coverage_digest: `sha256:${'d'.repeat(64)}`,
    coverage: {
      schema_version: 'coverage-certificate-v1',
      domains: ['grpc-consumer', 'proto-contract'],
      repository_count: 2,
      repositories: [],
      digest: `sha256:${'d'.repeat(64)}`,
    },
    caveat: 'provisional',
  }
}

const engine = new Client()

function page(value: ContractCatalogOperation) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <ContractDependencyMap operation={value} />
      </BaseProvider>
    </StyletronProvider>,
  )
}

afterEach(cleanup)

test('diagram and authoritative table expose the same deterministic edge identities and labels', () => {
  const value = operation()
  page(value)

  const diagram = screen.getAllByTestId('dependency-diagram-edge')
  const table = screen.getAllByTestId('dependency-table-edge')
  const diagramIDs = diagram.map((node) => node.getAttribute('data-edge-id')).sort()
  const tableIDs = table.map((node) => node.getAttribute('data-edge-id')).sort()
  expect(diagramIDs).toEqual(tableIDs)
  expect(tableIDs).toEqual(buildDependencyEdges(value).map((edge) => edge.id).sort())

  for (const edge of buildDependencyEdges(value)) {
    expect(screen.getAllByText(edge.label).length).toBeGreaterThan(0)
    expect(screen.getByRole('link', {
      name: `${edge.label}; ${edge.classification.replaceAll('_', ' ')}; open pinned source`,
    }).getAttribute('href')).toContain(`ref=${commit}`)
  }
  expect(screen.getByTestId('dependency-map-diagram').getAttribute('data-responsive-layout'))
    .toBe('desktop-diagram-mobile-table')
  expect(screen.getByTestId('dependency-map-mobile-summary').textContent)
    .toContain('authoritative table')
})

test('layout is input-order deterministic and ignores coverage-only repositories', () => {
  const first = operation()
  const reordered = {
    ...operation(),
    implementations: [...first.implementations].reverse().map((row) => ({
      ...row,
      claim: { ...row.claim, sources: [...row.claim.sources].reverse() },
    })),
    coverage: {
      ...first.coverage,
      repository_count: 3,
      repositories: [{
        repository: 'github.com/hidden/not-an-edge',
        scip_index: 'absent',
        runs: [],
      }],
    },
  }
  expect(layoutDependencyEdges(reordered)).toEqual(layoutDependencyEdges(first))
})

test('an empty relationship neighborhood names exact coverage and completeness without claiming absence', () => {
  const empty = {
    ...operation(),
    implementations: [],
    callers: [],
    unresolved_candidates: [],
  }
  page(empty)
  const message = screen.getByTestId('dependency-map-empty').textContent ?? ''
  expect(message).toContain('2 visible repositories')
  expect(message).toContain('complete')
  expect(message).toContain('not evidence of runtime absence')
  expect(screen.queryByTestId('dependency-diagram-edge')).toBeNull()
  expect(screen.queryByTestId('dependency-table-edge')).toBeNull()
})
