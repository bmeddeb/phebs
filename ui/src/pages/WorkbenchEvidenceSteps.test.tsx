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
  type WorkbenchChecklistPage,
  type WorkbenchDisposition,
  type WorkbenchImpactPage,
  type WorkbenchImplementationPage,
} from '../api'
import { lightTheme, ModeContext } from '../theme'

const api = vi.hoisted(() => ({
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
const engine = new Client()

function impactPage(
  path = 'src/callers/first.ts',
  complete = false,
): WorkbenchImpactPage {
  return {
    schema_version: 'workbench-impact-v1',
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
          start_byte: 100,
          end_byte: 130,
          start_line: 42,
          end_line: 44,
          assertion_id: `caller:${path}`,
          run_id: 'run-1',
          atom_id: 'atom-1',
        },
      }],
      extractor_abstentions: [],
      total_matching_rows: 2,
      pagination: { complete: true },
      coverage_digest: `sha256:${'b'.repeat(64)}`,
      attribution_digest: `sha256:${'c'.repeat(64)}`,
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
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

test('Where keeps gaps adjacent, cites exact source, and replaces pages', async () => {
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
  fireEvent.keyDown(document, { key: 'Escape' })
  expect(screen.getByText('reader_not_bound')).toBeTruthy()
  expect(screen.getByText('No runtime telemetry reader is bound.')).toBeTruthy()
  expect(screen.getByText('The bounded inventory read failed.')).toBeTruthy()
  expect(screen.getByText(/2 candidate units/)).toBeTruthy()
  expect(screen.getByText('stale')).toBeTruthy()
  const source = screen.getByRole('link', {
    name: /src\/callers\/first\.ts:42–44/,
  })
  expect(source.getAttribute('href')).toBe(
    `#/file?repo=${encodeURIComponent(repository)}` +
    '&path=src%2Fcallers%2Ffirst.ts' +
    `&ref=${commit}&L=42`,
  )

  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  expect(await screen.findByText('src/callers/second.ts:42–44')).toBeTruthy()
  expect(screen.queryByText('src/callers/first.ts:42–44')).toBeNull()
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
    schema_version: 'caller-comparison-v1',
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
      coverage_digest: `sha256:${'1'.repeat(64)}`,
      attribution_digest: `sha256:${'2'.repeat(64)}`,
    },
    replacement: {
      endpoint: {
        ...currentEndpoint,
        operation: '/demo.v2.Catalog/Get',
      },
      declaration: callerImpact.declaration,
      coverage_digest: `sha256:${'3'.repeat(64)}`,
      attribution_digest: `sha256:${'4'.repeat(64)}`,
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
    pagination: { complete: true },
    coverage: {} as never,
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
    /src\/callers\/current\.ts:42–44/,
    /src\/models\/catalog\.go:55/,
    /deploy\/catalog\.yaml:12–13/,
  ]) {
    expect(screen.getByRole('link', { name: expected })).toBeTruthy()
  }
  expect(screen.getAllByText('catalog-api → catalog-v1')).toHaveLength(2)
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
