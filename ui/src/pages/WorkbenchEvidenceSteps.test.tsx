import { useState } from 'react'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider } from 'baseui'
import {
  WorkbenchHowStep,
  WorkbenchWhereStep,
} from './WorkbenchEvidenceSteps'
import {
  defaultWorkbenchEvidenceInput,
  type WorkbenchEvidenceInput,
} from './workbenchEvidenceState'
import {
  WorkbenchAPIError,
  type CallerMapCitation,
  type CallerMapGeneration,
  type WorkbenchChecklistPage,
  type WorkbenchDisposition,
  type WorkbenchImpactPage,
  type WorkbenchServiceImpact,
  type WorkbenchImplementationPage,
} from '../api'
import { lightTheme, ModeContext } from '../theme'

const api = vi.hoisted(() => ({
  fetchCallerCitation: vi.fn(),
  fetchWorkbenchImpact: vi.fn(),
  fetchWorkbenchImplementation: vi.fn(),
  fetchWorkbenchChecklist: vi.fn(),
  recordWorkbenchDisposition: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api')>(),
  ...api,
}))

const investigationID = '01JWORKBENCH00000000000000'
const revisionID = 'rev_01JWORKBENCH00000000000000_000001'
const repository = 'github.com/acme/contracts'
const commit = 'a'.repeat(40)
const unitDigest = `sha256:${'6'.repeat(64)}`
const engine = new Client()

function callerGeneration(
  state: CallerMapGeneration['state'] = 'current',
): CallerMapGeneration {
  return {
    state,
    reason: state === 'current'
      ? undefined
      : `complete caller generation is ${state}`,
    plane: 'repository-overlay',
    repository,
    commit: state === 'missing' ? undefined : commit,
    unit_digest: state === 'missing' ? undefined : unitDigest,
    generation_digest: state === 'missing'
      ? undefined
      : `sha256:${'e'.repeat(64)}`,
    declaration_set_digest: `sha256:${'f'.repeat(64)}`,
    candidate_manifest_digest: `sha256:${'1'.repeat(64)}`,
    resolver_manifest_digest: `sha256:${'2'.repeat(64)}`,
    pair_set_digest: `sha256:${'3'.repeat(64)}`,
    manifest_digest: `sha256:${'4'.repeat(64)}`,
    publication_revision: state === 'missing' ? undefined : 7,
    record_counts: state === 'current'
      ? {
          candidate_records: 5,
          base_records: 3,
          excluded_go_test_records: 2,
        }
      : undefined,
    partition_progress: state === 'current'
      ? {
          state: 'complete',
          settled_pair_count: 2,
          succeeded_pair_count: 2,
          refused_pair_count: 0,
          total_pair_count: 2,
        }
      : undefined,
  }
}

function impactPage(
  path = 'src/callers/first.ts',
  complete = false,
): WorkbenchImpactPage {
  return {
    schema_version: 'workbench-impact-inventory-v3',
    investigation_id: investigationID,
    revision_id: revisionID,
    ticket_kind: 'migrate',
    scenario_emphasis: ['caller_impact', 'unit_ambiguity'],
    atlas: [],
    callers: [{
      selection: {
        role: 'current',
        protocol: 'protobuf',
        repository,
        declaration_lineage: 'catalog-v1',
        canonical_operation: '/demo.v1.Catalog/Get',
      },
      query: {} as never,
      scope: {
        repository,
        commit,
        scope_posture: 'focused',
        analysis_unit: {
          schema: 'analysis-unit-v1',
          name: 'catalog-service',
          digest: unitDigest,
          primary_paths: ['src/catalog/**'],
          supporting_paths: ['proto/catalog.proto'],
          primary_path_count: 1,
          supporting_path_count: 1,
          search_index_posture: 'focused',
          typed_index_posture: 'unit-bound',
          typed_index: {
            kind: 'scip',
            path: 'proto/catalog.proto',
          },
        },
      },
      generation: callerGeneration(),
      matching_rows_state: 'exact',
      declaration: {
        assertion_id: 'declaration-1',
        run_id: 'run-1',
        predicate: 'declares_operation',
        object: '/demo.v1.Catalog/Get',
        tier: 'exact',
        sources: [],
        sources_truncated: false,
      },
      resolved_callers: [{
        classification: 'resolved_caller',
        resolution: 'scip',
        protocol: 'protobuf',
        operation: '/demo.v1.Catalog/Get',
        tier: 'exact',
        fresh: false,
        unit_group: 'ambiguous',
        unit: {
          state: 'ambiguous',
          candidates: [
            { id: '//src/catalog' },
            { id: '//src/shared' },
          ],
          candidate_total: 2,
        },
        source: {
          repository,
          commit,
          path,
          object_id: '1'.repeat(40),
          blob_digest: `sha256:${'2'.repeat(64)}`,
          plane: 'repository-overlay',
          start_byte: 100,
          end_byte: 130,
          start_line: 42,
          end_line: 44,
          assertion_id: `caller:${path}`,
          run_id: 'run-1',
          atom_id: 'atom-1',
          citation: `exact-citation:${path}`,
        },
      }],
      extractor_abstentions: [],
      total_matching_rows: 2,
      pagination: { complete: true },
      caveat: 'Caller evidence is bounded.',
    }],
    resource_planes: [{
      id: 'runtime',
      label: 'Runtime traffic',
      state: 'unsupported',
      reason: 'No runtime telemetry reader is bound.',
      relationships: [],
    }, {
      id: 'deployment',
      label: 'Deployment inventory',
      state: 'failed',
      reason: 'The bounded inventory read failed.',
      relationships: [],
    }],
    analysis_scope: {
      coverage: [],
      capabilities: [{
        id: 'contract-callers',
        state: 'available',
      }, {
        id: 'contract-atlas',
        state: 'enabled',
      }],
      gaps: [{
        capability: 'runtime-traffic',
        target: repository,
        state: 'unsupported',
        code: 'reader_not_bound',
      }],
    },
    pagination: {
      complete,
      next_cursor: complete ? undefined : 'impact-next',
    },
    caveat: 'Impact evidence is bounded and does not prove completeness.',
  }
}

function emptyImpactPage(): WorkbenchImpactPage {
  return {
    ...impactPage('unused.ts', false),
    callers: [],
    resource_planes: [],
    scenario_emphasis: [],
  }
}

function serviceImpact(): WorkbenchServiceImpact {
  const root = {
    repository,
    state: 'complete' as const,
    generation: `sha256:${'a'.repeat(64)}`,
    root_digest: `sha256:${'b'.repeat(64)}`,
    authority_digest: `sha256:${'c'.repeat(64)}`,
    service_key: 'catalog-api',
    service_incarnation: 4,
    service_generation: `sha256:${'d'.repeat(64)}`,
    reference_count: 1,
  }
  const row = {
    repository,
    service_key: 'catalog-api',
    service_incarnation: 4,
    service_generation: root.service_generation,
    kind: 'rpc',
    plane: 'grpc',
    class: 'resolved',
    lookup_key: '/demo.v1.Catalog/Get',
    participation: ['source'],
    counterpart_services: ['checkout-api'],
    projection_digest: `sha256:${'e'.repeat(64)}`,
    posting_digest: `sha256:${'f'.repeat(64)}`,
    source: {
      path: 'src/catalog/client.go',
      unowned: true,
      claims: [],
    },
    evidence: {
      kind: 'rpc',
      plane: 'grpc',
      class: 'resolved',
      path: 'src/catalog/client.go',
      object_id: '1'.repeat(40),
      content_digest: `sha256:${'1'.repeat(64)}`,
      span: { start_byte: 10, end_byte: 20, start_line: 14, end_line: 15 },
      source_role: 'production',
      operation: '/demo.v1.Catalog/Get',
      candidate_operations: [],
      resolver_record_digests: [],
      posting_digest: `sha256:${'f'.repeat(64)}`,
    },
  }
  return {
    source: {
      role: 'source',
      selection: {
        role: 'current',
        protocol: 'protobuf',
        repository,
        declaration_lineage: 'catalog-v1',
        canonical_operation: '/demo.v1.Catalog/Get',
      },
      snapshot: {
        schema: 'phebs-service-relationship-snapshot-v1',
        query: {
          repositories: [repository],
          service_key: 'catalog-api',
          view: 'all',
          kind: 'rpc',
          lookup_key: '/demo.v1.Catalog/Get',
        },
        rows_state: 'nonempty',
        roots: [root],
        rows: [row],
        coverage: {
          authorized_repositories: 1,
          complete_roots: 1,
          empty_roots: 0,
          failed_roots: 0,
          unavailable_roots: 0,
          scanned_references: 1,
          returned_rows: 1,
          truncated: false,
        },
        truncated: false,
        caveat: 'Static source evidence only.',
      },
    },
    authority: {
      schema: 'phebs-relationship-proof-coverage-v1',
      visibility: {
        principal: 'user:test',
        authorization_provider: 'fixture',
        permission_snapshot: `sha256:${'2'.repeat(64)}`,
        visible_repository_set_digest: `sha256:${'3'.repeat(64)}`,
      },
      visible_repository_count: 1,
      exact_root_count: 1,
      gap_count: 0,
      state: 'exact',
      roots: [root],
      digest: `sha256:${'4'.repeat(64)}`,
    },
    caveat: 'No implicit write or migration-complete conclusion.',
  }
}

function implementationPage(
  path = 'src/implementation/catalog.ts',
): WorkbenchImplementationPage {
  return {
    schema_version: 'workbench-implementation-v1',
    investigation_id: investigationID,
    revision_id: revisionID,
    ticket_kind: 'migrate',
    rows: [{
      id: `implementation:${path}`,
      kind: 'implementation',
      code_role: 'production',
      boundary: 'server',
      review_state: 'selected',
      selection_rule: 'exact immutable source anchor',
      selection_input: `${repository}@${commit}:${path}`,
      symbol: 'CatalogService.get',
      source: {
        repository,
        commit,
        path,
        start_line: 18,
        end_line: 31,
      },
      commit: {
        id: commit,
        parent_ids: ['d'.repeat(40)],
        subject: 'Implement catalog lookup',
      },
      diff: {
        base: 'd'.repeat(40),
        head: commit,
        digest: `sha256:${'e'.repeat(64)}`,
        patch_excerpt: '+ return catalog.get(id)',
        patch_truncated: false,
        files: [{
          status: 'modified',
          path,
        }],
      },
    }],
    capabilities: [{
      id: 'history',
      state: 'available',
    }],
    gaps: [{
      capability: 'ownership',
      state: 'unsupported',
      code: 'reader_not_bound',
    }],
    pagination: {
      total_rows: 1,
      complete: true,
    },
    snapshot_digest: `sha256:${'f'.repeat(64)}`,
    caveat: 'Related source is not an edit recommendation.',
  }
}

function disposition(
  category: WorkbenchDisposition['category'] = 'accepted',
): WorkbenchDisposition {
  return {
    schema_version: 'workbench-disposition-v1',
    disposition_id: 'disposition-1',
    investigation_id: investigationID,
    revision_id: revisionID,
    suggestion: suggestion('review-current'),
    category,
    actor: 'user:test',
    authority: 'human',
    sequence: 1,
    created_at: '2026-07-27T12:00:00Z',
    content_digest: `sha256:${'1'.repeat(64)}`,
  }
}

function suggestion(id: string) {
  return {
    schema_version: 'workbench-suggestion-v1',
    suggestion_id: id,
    investigation_id: investigationID,
    revision_id: revisionID,
    kind: 'review_implementation',
    summary: id === 'review-current'
      ? 'Review the cited catalog implementation'
      : 'Recheck stale caller evidence',
    selection_rule: 'deterministic exact evidence projection',
    evidence_snapshot_digest: `sha256:${'2'.repeat(64)}`,
    evidence: [{
      plane: 'implementation',
      kind: 'source',
      id: `${id}:source`,
      digest: `sha256:${'3'.repeat(64)}`,
      repository,
      commit,
      path: 'src/implementation/catalog.ts',
      start_line: 18,
      end_line: 31,
    }],
    content_digest: `sha256:${id === 'review-current'
      ? '4'.repeat(64)
      : '5'.repeat(64)}`,
  }
}

function checklistPage(): WorkbenchChecklistPage {
  return {
    schema_version: 'workbench-checklist-v1',
    investigation_id: investigationID,
    revision_id: revisionID,
    ticket_kind: 'migrate',
    evidence_snapshot: {
      impact_digest: `sha256:${'6'.repeat(64)}`,
      implementation_digest: `sha256:${'7'.repeat(64)}`,
      combined_digest: `sha256:${'8'.repeat(64)}`,
      impact_truncated: false,
      implementation_truncated: false,
    },
    entries: [{
      suggestion: suggestion('review-current'),
      evidence_state: 'current',
      state: 'accepted',
      disposition: disposition(),
      disposition_history: [disposition()],
    }, {
      suggestion: suggestion('review-stale'),
      evidence_state: 'stale',
      state: 'unaccepted',
      disposition_history: [],
    }],
    pagination: {
      total_entries: 2,
      complete: true,
    },
    snapshot_digest: `sha256:${'9'.repeat(64)}`,
    caveat: 'Checklist state is only human Disposition state.',
  }
}

function EvidenceHarness({
  step,
}: {
  step: 'where' | 'how'
}) {
  const [evidence, setEvidence] = useState<WorkbenchEvidenceInput>(
    defaultWorkbenchEvidenceInput,
  )
  const component = step === 'where'
    ? (
        <WorkbenchWhereStep
          available
          investigationID={investigationID}
          revisionID={revisionID}
          evidence={evidence}
          onEvidenceChange={setEvidence}
        />
      )
    : (
        <WorkbenchHowStep
          available
          investigationID={investigationID}
          revisionID={revisionID}
          evidence={evidence}
          onEvidenceChange={setEvidence}
        />
      )
  return (
    <StyletronProvider value={engine}>
      <ModeContext value={{ mode: 'light', toggle: () => {} }}>
        <BaseProvider theme={lightTheme}>{component}</BaseProvider>
      </ModeContext>
    </StyletronProvider>
  )
}

beforeEach(() => {
  for (const mock of Object.values(api)) mock.mockReset()
  api.fetchCallerCitation.mockImplementation(
    async (token: string): Promise<CallerMapCitation> => {
      const path = token.replace('exact-citation:', '')
      const source = impactPage(path, true).callers[0].resolved_callers[0].source
      return {
        schema_version: 'caller-map-citation-v1',
        generation: callerGeneration(),
        source,
        content: `client.Get(${path})`,
      }
    },
  )
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

test('Where keeps gaps adjacent, reads exact overlay citations, and replaces pages', async () => {
  api.fetchWorkbenchImpact.mockImplementation(
    (_investigation, _revision, options) =>
      Promise.resolve(options.cursor
        ? impactPage('src/callers/second.ts', true)
        : impactPage()),
  )
  render(<EvidenceHarness step="where" />)

  expect(await screen.findByRole('heading', {
    name: 'Analysis scope & gaps',
  })).toBeTruthy()
  fireEvent.click(screen.getByRole('button', {
    name: 'Help for Analysis scope & gaps',
  }))
  expect(screen.getByRole('dialog', {
    name: 'Analysis scope & gaps help',
  }).textContent).toContain(
    'what phebs examined, what evidence was available, and what remained unsupported or unresolved',
  )
  // The served capability rows (contract-atlas enabled) reach the glossary
  // help: the term must not claim its supporting capabilities are absent.
  expect(screen.getByRole('dialog', {
    name: 'Analysis scope & gaps help',
  }).textContent).not.toContain(
    'unavailable because no supporting contract or coverage capability is enabled',
  )
  fireEvent.keyDown(document, { key: 'Escape' })
  expect(screen.getByText('reader_not_bound')).toBeTruthy()
  expect(screen.getByText('No runtime telemetry reader is bound.')).toBeTruthy()
  expect(screen.getByText('The bounded inventory read failed.')).toBeTruthy()
  expect(screen.getByRole('heading', {
    name: 'Caller evidence scope',
  })).toBeTruthy()
  fireEvent.click(screen.getByRole('button', {
    name: 'Help for Caller evidence scope',
  }))
  expect(screen.getByRole('dialog', {
    name: 'Caller evidence scope help',
  }).textContent).not.toContain(
    'unavailable because no supporting contract or coverage capability is enabled',
  )
  fireEvent.keyDown(document, { key: 'Escape' })
  expect(screen.getByText('service unit catalog-service')).toBeTruthy()
  expect(screen.getByText('5 candidates')).toBeTruthy()
  expect(screen.getByText('3 base')).toBeTruthy()
  expect(screen.getByText(/2 excluded go_test/)).toBeTruthy()
  expect(screen.getByText(/2\/2 settled/)).toBeTruthy()
  const callerIdentity = screen.getByTestId('caller-generation-identity')
  expect(callerIdentity.textContent).toContain(`commit ${commit}`)
  expect(callerIdentity.textContent).toContain(`unit ${unitDigest}`)
  expect(callerIdentity.textContent).toContain(`generation sha256:${'e'.repeat(64)}`)
  expect(screen.queryByText('src/catalog/**')).toBeNull()
  const callerScopeSection = screen.getByRole('heading', {
    name: 'Caller evidence scope',
  }).closest('section')
  expect(callerScopeSection).not.toBeNull()
  const callerScope = within(callerScopeSection as HTMLElement)
  const callerScopeDetails = callerScope.getByTestId('analysis-scope-detail')
  fireEvent.click(within(callerScopeDetails).getByText('Exact repository scope'))
  fireEvent.click(within(callerScopeDetails).getByRole('button', {
    name: new RegExp(repository),
  }))
  expect(callerScope.getByText('src/catalog/**')).toBeTruthy()
  expect(callerScope.getByText('proto/catalog.proto')).toBeTruthy()
  expect(screen.getByText(/2 candidate units/)).toBeTruthy()
  expect(screen.getByText('stale')).toBeTruthy()
  expect(screen.getByText(`${repository}/src/callers/first.ts:42`)).toBeTruthy()
  expect(screen.queryByRole('link', {
    name: /src\/callers\/first\.ts/,
  })).toBeNull()
  fireEvent.click(screen.getByRole('button', {
    name: 'Read exact cited bytes',
  }))
  expect(await screen.findByText('client.Get(src/callers/first.ts)'))
    .toBeTruthy()
  expect(api.fetchCallerCitation).toHaveBeenCalledWith(
    'exact-citation:src/callers/first.ts',
    expect.any(AbortSignal),
  )

  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  expect(await screen.findByText(
    `${repository}/src/callers/second.ts:42`,
  )).toBeTruthy()
  expect(screen.queryByText(
    `${repository}/src/callers/first.ts:42`,
  )).toBeNull()
  expect(api.fetchWorkbenchImpact).toHaveBeenLastCalledWith(
    investigationID,
    revisionID,
    expect.objectContaining({
      pageSize: 25,
      cursor: 'impact-next',
    }),
    expect.any(AbortSignal),
  )
})

test('Where applies exact service scope and renders affected and unowned evidence', async () => {
  const page = impactPage('src/callers/current.ts', true)
  page.service_impact = serviceImpact()
  api.fetchWorkbenchImpact.mockResolvedValue(page)
  render(<EvidenceHarness step="where" />)

  fireEvent.change(screen.getByLabelText('Source service'), {
    target: { value: 'catalog-api' },
  })
  fireEvent.change(screen.getByLabelText('Repository scope'), {
    target: { value: repository },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Apply service scope' }))

  expect(await screen.findByRole('heading', {
    name: 'Exact affected services',
  })).toBeTruthy()
  expect(screen.getByText('src/catalog/client.go')).toBeTruthy()
  expect(screen.getByText('catalog-api → checkout-api')).toBeTruthy()
  expect(screen.getAllByText('Unowned').length).toBeGreaterThan(0)
  expect(screen.getByText(/Preview authority/).textContent)
    .toContain('invalidates when either exact service/root authority changes')
  expect(screen.getByRole('link', { name: 'Open exact sources' }).getAttribute('href'))
    .toContain('service_key=catalog-api')
  expect(api.fetchWorkbenchImpact).toHaveBeenLastCalledWith(
    investigationID,
    revisionID,
    expect.objectContaining({
      filters: expect.objectContaining({
        service_repository: repository,
        source_service: 'catalog-api',
      }),
    }),
    expect.any(AbortSignal),
  )
})

test('Where refuses a service row whose root differs from final authority', async () => {
  const page = impactPage('src/callers/current.ts', true)
  const service = serviceImpact()
  if (service.source) {
    service.source.snapshot.roots[0] = {
      ...service.source.snapshot.roots[0],
      root_digest: `sha256:${'9'.repeat(64)}`,
    }
  }
  page.service_impact = service
  api.fetchWorkbenchImpact.mockResolvedValue(page)

  const { rerender } = render(<EvidenceHarness step="where" />)
  rerender(<EvidenceHarness step="where" />)
  fireEvent.change(screen.getByLabelText('Source service'), {
    target: { value: 'catalog-api' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Apply service scope' }))

  expect(await screen.findByText(/Service impact response refused/)).toBeTruthy()
  expect(screen.queryByText('src/catalog/client.go')).toBeNull()
})

test('Where renders a typed caller-generation gap without zero or classifications', async () => {
  const page = impactPage('src/callers/hidden.ts', true)
  page.callers[0] = {
    ...page.callers[0],
    generation: callerGeneration('stale'),
    matching_rows_state: 'unavailable',
    declaration: undefined,
    total_matching_rows: undefined,
    resolved_callers: [],
    extractor_abstentions: [],
    pagination: { complete: true },
  }
  api.fetchWorkbenchImpact.mockResolvedValue(page)

  render(<EvidenceHarness step="where" />)

  expect(await screen.findByText('stale · rows unavailable')).toBeTruthy()
  expect(screen.getByText(/Caller rows and totals are unavailable/)).toBeTruthy()
  expect(screen.getByText(/not evidence of zero callers/i)).toBeTruthy()
  expect(screen.queryByText(/0 exact matches/)).toBeNull()
  expect(screen.queryByText('resolved caller')).toBeNull()
  expect(screen.queryByRole('button', {
    name: 'Read exact cited bytes',
  })).toBeNull()
})

test('Where preserves comparison, field, and resource-plane source citations', async () => {
  const page = impactPage('src/callers/current.ts', true)
  const callerImpact = page.callers[0]
  const currentCaller = callerImpact.resolved_callers[0]
  const currentEndpoint = {
    protocol: 'protobuf',
    repository,
    declaration_lineage: 'catalog-v1',
    operation: '/demo.v1.Catalog/Get',
  }
  page.callers = []
  page.comparison = {
    schema_version: 'caller-comparison-v2',
    query: {
      old: currentEndpoint,
      replacement: {
        ...currentEndpoint,
        operation: '/demo.v2.Catalog/Get',
      },
      freshness: 'any',
      resolution: 'any',
      ordering: 'source',
      level: 'unit',
    },
    old: {
      endpoint: currentEndpoint,
      declaration: callerImpact.declaration,
      generation: callerGeneration(),
      matching_rows_state: 'exact',
    },
    replacement: {
      endpoint: {
        ...currentEndpoint,
        operation: '/demo.v2.Catalog/Get',
      },
      declaration: callerImpact.declaration,
      generation: callerGeneration(),
      matching_rows_state: 'exact',
    },
    rows: [{
      level: 'unit',
      key: `${repository}://src/catalog`,
      classification: 'old_only_evidence',
      unit: { id: '//src/catalog' },
      old: {
        occurrence_count: 1,
        rows: [currentCaller],
        rows_truncated: false,
      },
      replacement: {
        occurrence_count: 0,
        rows: [],
        rows_truncated: false,
      },
    }],
    total_rows: 1,
    matching_rows_state: 'exact',
    pagination: { complete: true },
    caveat: 'Comparison evidence is bounded.',
  }
  page.field_references = {
    schema_version: 'field-reference-read-v1',
    rows: [{
      field: {
        lineage: 'catalog-v1',
        message: 'demo.v1.Catalog',
        field_number: 7,
      },
      assertion: {
        id: 'field-assertion-1',
        predicate: 'REFERENCES_PROTO_FIELD',
        subject: 'src/models/catalog.go:200-207',
        object: 'demo.v1.Catalog#7',
        lineage: 'catalog-v1',
        tier: 'exact',
        repo: repository,
        run_id: 'field-run-1',
      },
      evidence: [{
        repository,
        run_id: 'field-run-1',
        atom: {
          atom_id: 'field-atom-1',
          schema_version: 'field-reference-v1',
          blob_digest: `sha256:${'d'.repeat(64)}`,
          start_byte: 200,
          end_byte: 207,
          rule_id: 'field-reference',
          extractor_version: '1.0.0',
          adapter_config_digest: `sha256:${'e'.repeat(64)}`,
          fact_fingerprint: `sha256:${'f'.repeat(64)}`,
          first_seen: '2026-07-27T12:00:00Z',
        },
        occurrences: [{
          occurrence_id: 'field-occurrence-1',
          atom_id: 'field-atom-1',
          repo: repository,
          commit,
          path: 'src/models/catalog.go',
          start_line: 55,
          end_line: 55,
          visibility_scope: 'visible',
          run_id: 'field-run-1',
          observed_at: '2026-07-27T12:00:00Z',
        }],
      }],
    }],
    total_rows: 1,
    pagination: { complete: true },
    coverage_digest: `sha256:${'5'.repeat(64)}`,
    caveat: 'Field-reference evidence is bounded.',
  }
  page.resource_planes = [{
    id: 'deployment',
    label: 'Deployment inventory',
    state: 'enabled',
    relationships: [{
      kind: 'deploys',
      subject: 'catalog-api',
      object: 'catalog-v1',
      classification: 'declared deployment',
      sources: [{
        repository,
        commit,
        path: 'deploy/catalog.yaml',
        start_byte: 80,
        end_byte: 104,
        start_line: 12,
        end_line: 13,
        assertion_id: 'deployment-assertion-1',
        run_id: 'deployment-run-1',
        atom_id: 'deployment-atom-1',
      }],
    }],
  }]
  api.fetchWorkbenchImpact.mockResolvedValue(page)

  render(<EvidenceHarness step="where" />)

  expect(await screen.findByText('3 evidence groups')).toBeTruthy()
  for (const expected of [
    /src\/models\/catalog\.go:55/,
    /deploy\/catalog\.yaml:12–13/,
  ]) {
    expect(screen.getByRole('link', { name: expected })).toBeTruthy()
  }
  expect(screen.getByText(`${repository}/src/callers/current.ts:42`))
    .toBeTruthy()
  expect(screen.queryByRole('link', {
    name: /src\/callers\/current\.ts/,
  })).toBeNull()
  expect(screen.getByRole('button', { name: 'Read exact cited bytes' }))
    .toBeTruthy()
  expect(screen.getAllByTestId('workbench-caller-generation')).toHaveLength(2)
  expect(screen.getAllByText('5 candidates')).toHaveLength(2)
  expect(screen.getAllByText(/2\/2 settled/)).toHaveLength(2)
  const endpointIdentities = screen.getAllByTestId('caller-generation-identity')
  expect(endpointIdentities).toHaveLength(2)
  for (const identity of endpointIdentities) {
    expect(identity.textContent).toContain(`unit ${unitDigest}`)
    expect(identity.textContent).toContain(`generation sha256:${'e'.repeat(64)}`)
  }
  expect(screen.getAllByText('catalog-api → catalog-v1')).toHaveLength(2)
})

test('Where renders an unavailable comparison without totals or classifications', async () => {
  const page = impactPage('unused.ts', true)
  const endpoint = {
    protocol: 'protobuf',
    repository,
    declaration_lineage: 'catalog-v1',
    operation: '/demo.v1.Catalog/Get',
  }
  page.callers = []
  page.comparison = {
    schema_version: 'caller-comparison-v2',
    query: {
      old: endpoint,
      replacement: {
        ...endpoint,
        operation: '/demo.v2.Catalog/Get',
      },
      freshness: 'any',
      resolution: 'any',
      ordering: 'source',
      level: 'occurrence',
    },
    old: {
      endpoint,
      generation: callerGeneration('stale'),
      matching_rows_state: 'unavailable',
    },
    replacement: {
      endpoint: {
        ...endpoint,
        operation: '/demo.v2.Catalog/Get',
      },
      generation: callerGeneration(),
      matching_rows_state: 'exact',
    },
    rows: [],
    total_rows: undefined,
    matching_rows_state: 'unavailable',
    pagination: { complete: true },
    caveat: 'Comparison is unavailable until both generations are current.',
  }
  api.fetchWorkbenchImpact.mockResolvedValue(page)

  render(<EvidenceHarness step="where" />)

  expect(await screen.findByText(
    'Repository-overlay migration comparison',
  )).toBeTruthy()
  expect(screen.getByText(/Comparison rows, totals, and classifications are unavailable/))
    .toBeTruthy()
  expect(screen.getByText(/not evidence of zero callers or migration completion/))
    .toBeTruthy()
  expect(screen.queryByText('0 exact rows')).toBeNull()
  expect(screen.queryByText('Old Only Evidence')).toBeNull()
  for (const title of ['Current endpoint', 'Replacement endpoint']) {
    fireEvent.click(screen.getByRole('button', {
      name: `Help for ${title}`,
    }))
    expect(screen.getByRole('dialog', {
      name: `${title} help`,
    }).textContent).not.toContain(
      'unavailable because no supporting contract or coverage capability is enabled',
    )
    fireEvent.keyDown(document, { key: 'Escape' })
  }
})

test('Where exposes honest empty and cursor invalidation states with restart', async () => {
  api.fetchWorkbenchImpact
    .mockResolvedValueOnce(emptyImpactPage())
    .mockRejectedValueOnce(new WorkbenchAPIError(409, 'cursor changed'))
    .mockResolvedValueOnce({
      ...emptyImpactPage(),
      pagination: { complete: true },
    })
  render(<EvidenceHarness step="where" />)

  expect(await screen.findByText(/No evidence groups are visible/)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  expect(await screen.findByText('Evidence snapshot changed.', {
    exact: false,
  })).toBeTruthy()
  expect(screen.queryByText(/No evidence groups are visible/)).toBeNull()
  fireEvent.click(screen.getByRole('button', {
    name: 'Restart exact evidence',
  }))
  expect(await screen.findByText(/No evidence groups are visible/)).toBeTruthy()
  expect(api.fetchWorkbenchImpact).toHaveBeenCalledTimes(3)
})

test('Where preserves typed empty scope and zero-gap statements', async () => {
  const page = emptyImpactPage()
  page.analysis_scope = {
    coverage: [],
    capabilities: [],
    gaps: [],
  }
  api.fetchWorkbenchImpact.mockResolvedValue(page)

  render(<EvidenceHarness step="where" />)

  expect(await screen.findByText('No capability rows were returned.')).toBeTruthy()
  expect(screen.getByText('No focused-local coverage certificate was returned.'))
    .toBeTruthy()
  expect(screen.getByText('No gaps were returned in this bounded projection.'))
    .toBeTruthy()
})

test('Where preserves non-disclosure when exact evidence becomes unavailable', async () => {
  api.fetchWorkbenchImpact.mockRejectedValue(
    new WorkbenchAPIError(404, 'secret repository identity'),
  )
  render(<EvidenceHarness step="where" />)

  expect(await screen.findByText(
    'Evidence unavailable or permission changed.',
    { exact: false },
  )).toBeTruthy()
  expect(screen.getByText(/No hidden repository or Investigation identity/))
    .toBeTruthy()
  expect(screen.queryByText('secret repository identity')).toBeNull()
})

test('How separates deterministic suggestions from retryable human state', async () => {
  api.fetchWorkbenchImplementation.mockResolvedValue(implementationPage())
  const refreshedDisposition = {
    ...disposition(),
    disposition_id: 'disposition-2',
    sequence: 2,
    supersedes: 'disposition-1',
  }
  const refreshedChecklist = checklistPage()
  refreshedChecklist.entries[0] = {
    ...refreshedChecklist.entries[0],
    disposition: refreshedDisposition,
    disposition_history: [
      disposition(),
      refreshedDisposition,
    ],
  }
  api.fetchWorkbenchChecklist
    .mockResolvedValueOnce(checklistPage())
    .mockResolvedValue(refreshedChecklist)
  api.recordWorkbenchDisposition
    .mockRejectedValueOnce(new WorkbenchAPIError(409, 'active state changed'))
    .mockResolvedValueOnce({
      ...disposition('rejected'),
      disposition_id: 'disposition-3',
      rationale: 'Outside the bounded change.',
      sequence: 3,
      supersedes: 'disposition-2',
    })
  render(<EvidenceHarness step="how" />)

  expect(await screen.findByRole('heading', {
    name: 'Related implementation evidence',
  })).toBeTruthy()
  fireEvent.click(screen.getByText('History evidence'))
  expect(document.body.textContent).toContain('Implement catalog lookup')
  expect(document.body.textContent).toContain('+ return catalog.get(id)')
  expect(screen.getByText('Human-recorded accepted', { exact: false }))
    .toBeTruthy()
  expect(screen.getByText('current evidence')).toBeTruthy()
  expect(screen.getByText('stale evidence')).toBeTruthy()
  const staleAction = screen.getAllByRole('button', {
    name: 'Record disposition',
  })[0]
  expect(staleAction.hasAttribute('disabled')).toBe(true)

  const category = screen.getByLabelText(
    'Disposition for Review the cited catalog implementation',
  )
  fireEvent.change(category, { target: { value: 'rejected' } })
  const correction = screen.getByRole('button', { name: 'Record correction' })
  expect(correction.hasAttribute('disabled')).toBe(true)
  const rationale = screen.getByLabelText(
    'Rationale for Review the cited catalog implementation',
  )
  expect(rationale.getAttribute('maxlength')).toBeNull()
  fireEvent.change(rationale, {
    target: { value: 'Outside the bounded change.' },
  })
  fireEvent.click(correction)
  expect(await screen.findByText('Evidence snapshot changed.', {
    exact: false,
  })).toBeTruthy()
  fireEvent.click(screen.getByRole('button', {
    name: 'Restart exact evidence',
  }))
  expect(await screen.findByText('Human-recorded accepted · sequence 2'))
    .toBeTruthy()
  fireEvent.change(screen.getByLabelText(
    'Disposition for Review the cited catalog implementation',
  ), { target: { value: 'rejected' } })
  fireEvent.change(screen.getByLabelText(
    'Rationale for Review the cited catalog implementation',
  ), {
    target: { value: 'Outside the bounded change.' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Record correction' }))
  expect(await screen.findByText('Rejected recorded as sequence 3.'))
    .toBeTruthy()

  expect(api.recordWorkbenchDisposition).toHaveBeenLastCalledWith(
    investigationID,
    revisionID,
    expect.objectContaining({
      investigation_id: investigationID,
      expected_revision_id: revisionID,
      category: 'rejected',
      rationale: 'Outside the bounded change.',
      supersedes: 'disposition-2',
      evidence: {
        impact_filters: {
          freshness: 'any',
          resolution: 'any',
          ordering: 'source',
          level: 'occurrence',
        },
        anchors: [],
      },
    }),
  )
  const mutation = api.recordWorkbenchDisposition.mock.calls.at(-1)?.[2]
  expect(mutation).not.toHaveProperty('assignee')
  expect(mutation).not.toHaveProperty('due_date')
  expect(mutation).not.toHaveProperty('priority')
  expect(mutation).not.toHaveProperty('custom_state')
  expect(screen.queryByText(/assigned/i)).toBeNull()
  expect(screen.queryByText(/due date/i)).toBeNull()
})

test('How renders honest empty evidence and checklist without completion claims', async () => {
  api.fetchWorkbenchImplementation.mockResolvedValue({
    ...implementationPage(),
    rows: [],
    pagination: { total_rows: 0, complete: true },
  })
  api.fetchWorkbenchChecklist.mockResolvedValue({
    ...checklistPage(),
    entries: [],
    pagination: { total_entries: 0, complete: true },
  })
  render(<EvidenceHarness step="how" />)

  expect(await screen.findByText(/No related implementation rows/))
    .toBeTruthy()
  expect(screen.getByText(/No deterministic suggestions/)).toBeTruthy()
  expect(screen.getByText(/Nothing is implicitly completed/)).toBeTruthy()
  expect(api.recordWorkbenchDisposition).not.toHaveBeenCalled()
})

test('How replaces implementation pages without accumulating prior DOM rows', async () => {
  api.fetchWorkbenchImplementation.mockImplementation(
    (_investigation, _revision, options) => Promise.resolve({
      ...implementationPage(options.cursor
        ? 'src/implementation/second.ts'
        : 'src/implementation/first.ts'),
      pagination: {
        total_rows: 2,
        complete: Boolean(options.cursor),
        next_cursor: options.cursor ? undefined : 'implementation-next',
      },
    }),
  )
  api.fetchWorkbenchChecklist.mockResolvedValue({
    ...checklistPage(),
    entries: [],
    pagination: { total_entries: 0, complete: true },
  })
  render(<EvidenceHarness step="how" />)

  expect(await screen.findByText('src/implementation/first.ts:18–31'))
    .toBeTruthy()
  const pages = screen.getByRole('navigation', {
    name: 'implementation evidence pages',
  })
  fireEvent.click(within(pages).getByRole('button', { name: 'Next page' }))
  expect(await screen.findByText('src/implementation/second.ts:18–31'))
    .toBeTruthy()
  expect(screen.queryByText('src/implementation/first.ts:18–31')).toBeNull()
  expect(api.fetchWorkbenchImplementation).toHaveBeenLastCalledWith(
    investigationID,
    revisionID,
    expect.objectContaining({ cursor: 'implementation-next' }),
    expect.any(AbortSignal),
  )
})
