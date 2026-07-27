import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider } from 'baseui'
import WorkbenchPage from './WorkbenchPage'
import {
  WorkbenchAPIError,
  type WorkbenchPreview,
  type WorkbenchTicketKind,
  type WorkbenchView,
} from '../api'
import { lightTheme, ModeContext } from '../theme'

const api = vi.hoisted(() => ({
  fetchWorkbench: vi.fn(),
  previewWorkbench: vi.fn(),
  createWorkbench: vi.fn(),
  reviseWorkbench: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api')>(),
  ...api,
}))

const investigationID = '01JWORKBENCH00000000000000'
const revisionID = 'rev_01JWORKBENCH00000000000000_000001'
const nextRevisionID = 'rev_01JWORKBENCH00000000000000_000002'
const repository = 'github.com/acme/contracts'
const commit = 'a'.repeat(40)
const engine = new Client()

function selection(role: 'current' | 'replacement' | 'analogous' = 'current') {
  return {
    role,
    protocol: 'protobuf',
    repository,
    declaration_lineage: role === 'replacement' ? 'lineage-v2' : 'lineage-v1',
    canonical_operation: role === 'replacement'
      ? '/demo.v2.Catalog/Get'
      : '/demo.v1.Catalog/Get',
  } as const
}

function view(
  kind: WorkbenchTicketKind = 'modify',
  revision = revisionID,
): WorkbenchView {
  const selections = kind === 'add'
    ? []
    : kind === 'migrate'
      ? [selection(), selection('replacement')]
      : [selection()]
  return {
    investigation: {
      investigation_id: investigationID,
      referent: 'ticket:T21.10',
      claim_family: 'change-workbench',
      title: `${kind} catalog contract`,
      owner: 'user:test',
      lifecycle: 'active',
      current_revision_id: revision,
      created_at: '2026-07-27T12:00:00Z',
      updated_at: '2026-07-27T12:00:00Z',
    },
    revision: {
      revision_id: revision,
      investigation_id: investigationID,
      seq: revision === revisionID ? 1 : 2,
      normalized_question: `How should we ${kind} the contract?`,
      decision_sought: 'Approve the exact bounded change.',
      declared_universe: JSON.stringify({
        repositories: [{ name: repository, commit }],
      }),
      snapshot_policy: 'exact indexed commits',
      build_configuration: 'repository default',
      pack_selection: 'contract-atlas,contract-compatibility',
      enumeration_method: 'exact Atlas selection',
      creator: 'user:test',
      created_at: '2026-07-27T12:00:00Z',
      content_digest: `sha256:${'b'.repeat(64)}`,
    },
    brief: {
      brief_id: 'brief_01JWORKBENCH00000000000000',
      schema_version: 'change-brief-v1',
      investigation_id: investigationID,
      revision_id: revision,
      ticket_kind: kind,
      problem: 'Callers rely on the current receipt shape.',
      desired_outcome: 'The selected contract changes deliberately.',
      success_criteria: ['The exact endpoint is reviewed.'],
      non_goals: ['No inferred rollout completion.'],
      assumptions: [],
      open_questions: [],
      what: {
        selections,
        proposal: kind === 'add' || kind === 'modify'
          ? {
              protocol: 'protobuf',
              files: [{
                path: 'proto/catalog.proto',
                content_hash: `sha256:${'c'.repeat(64)}`,
                size_bytes: 91,
              }],
              source_set_digest: `sha256:${'d'.repeat(64)}`,
            }
          : undefined,
      },
      creator: 'user:test',
      created_at: '2026-07-27T12:00:00Z',
      content_digest: `sha256:${'e'.repeat(64)}`,
    },
  }
}

function preview(
  ready = true,
  compatibility: WorkbenchPreview['compatibility'] = {
    status: 'unavailable',
    protocol: 'protobuf',
    reason: 'unsupported_protocol',
  },
): WorkbenchPreview {
  return {
    schema_version: 'change-workbench-preview-v1',
    operation: 'revise',
    ready,
    preview_digest: ready ? `sha256:${'f'.repeat(64)}` : undefined,
    investigation_id: investigationID,
    expected_revision_id: revisionID,
    next_revision_sequence: 2,
    referent: 'ticket:T21.10',
    claim_family: 'change-workbench',
    title: 'retire catalog contract',
    revision: {
      normalized_question: 'How should we retire the contract?',
      decision_sought: 'Approve the exact bounded change.',
      declared_universe: '{}',
      snapshot_policy: 'exact indexed commits',
      build_configuration: 'repository default',
      pack_selection: 'contract-atlas',
      enumeration_method: 'exact Atlas selection',
    },
    brief: {
      schema_version: 'change-brief-v1',
      ticket_kind: 'retire',
      problem: 'Callers rely on the current receipt shape.',
      desired_outcome: 'The selected contract changes deliberately.',
      success_criteria: ['The exact endpoint is reviewed.'],
      non_goals: [],
      assumptions: [],
      open_questions: [],
      what: { selections: [selection()] },
      creator: 'user:test',
    },
    authorization_digest: `sha256:${'1'.repeat(64)}`,
    repositories: [{ name: repository, commit }],
    endpoints: [],
    capabilities: [],
    compatibility,
    estimate: {
      repository_count: 1,
      endpoint_count: 1,
      proposal_files: 0,
      proposal_bytes: 0,
      capability_count: 1,
      analysis_units: 2,
    },
    blockers: ready ? [] : [{ code: 'selection_unresolved' }],
  }
}

function wrapped(params: URLSearchParams) {
  return (
    <StyletronProvider value={engine}>
      <ModeContext value={{ mode: 'light', toggle: () => {} }}>
        <BaseProvider theme={lightTheme}>
          <WorkbenchPage params={params} />
        </BaseProvider>
      </ModeContext>
    </StyletronProvider>
  )
}

function page(query = '') {
  return render(wrapped(new URLSearchParams(query)))
}

beforeEach(() => {
  api.fetchWorkbench.mockReset()
  api.previewWorkbench.mockReset()
  api.createWorkbench.mockReset()
  api.reviseWorkbench.mockReset()
  window.location.hash = '#/workbench'
})

afterEach(cleanup)

test('starts from bounded ticket text with a persistent accessible four-step rail', () => {
  page()
  expect(api.fetchWorkbench).not.toHaveBeenCalled()
  fireEvent.change(screen.getByLabelText('Workbench title'), {
    target: { value: 'Stabilize receipts' },
  })
  fireEvent.change(screen.getByLabelText('Ticket text'), {
    target: { value: 'Receipt identifiers drift across callers.' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Shape the Why' }))

  expect(screen.getByRole('heading', { name: 'Why this change?' })).toBeTruthy()
  const rail = screen.getByRole('navigation', { name: 'Workbench steps' })
  const links = Array.from(rail.querySelectorAll('a'))
  expect(links.map((link) => link.textContent?.trim())).toEqual([
    '01Why', '02What', '03Where', '04How',
  ])
  expect(screen.getByRole('link', { name: /Why/ }).getAttribute('aria-current'))
    .toBe('step')
  const what = screen.getByRole('link', { name: /What/ })
  expect(what.getAttribute('href')).toBe('#/workbench?step=what')
  what.focus()
  expect(document.activeElement).toBe(what)
  fireEvent.keyDown(what, { key: 'Enter' })
  expect(screen.getByText('Uncommitted draft')).toBeTruthy()
  expect(screen.getByTestId('workbench-shell').getAttribute('data-responsive-layout'))
    .toBe('desktop-rail-mobile-strip')
  const beforeUnload = new Event('beforeunload', { cancelable: true })
  window.dispatchEvent(beforeUnload)
  expect(beforeUnload.defaultPrevented).toBe(true)
  expect(api.previewWorkbench).not.toHaveBeenCalled()
  expect(api.createWorkbench).not.toHaveBeenCalled()
})

test('bounds ticket intake by the persisted UTF-8 problem byte ceiling', () => {
  page()
  fireEvent.change(screen.getByLabelText('Workbench title'), {
    target: { value: 'Bound the brief' },
  })
  fireEvent.change(screen.getByLabelText('Ticket text'), {
    target: { value: '€'.repeat(6_000) },
  })
  expect(screen.getByText('18,000 / 16,384 bytes')).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Shape the Why' })
    .hasAttribute('disabled')).toBe(true)
})

test('starts from an exact Contract Atlas operation without implicit reads or writes', () => {
  page(
    'source=atlas&step=what&protocol=protobuf&repository=' +
    'github.com%2Facme%2Fcontracts&lineage=lineage-v1&operation=' +
    '%2Fdemo.v1.Catalog%2FGet',
  )
  expect(screen.getByRole('heading', { name: 'What changes?' })).toBeTruthy()
  expect((screen.getByLabelText('Selection 1 repository') as HTMLInputElement).value)
    .toBe(repository)
  expect(
    (screen.getByLabelText('Selection 1 declaration lineage') as HTMLInputElement)
      .value,
  ).toBe('lineage-v1')
  expect(
    (screen.getByLabelText('Selection 1 canonical operation') as HTMLInputElement)
      .value,
  ).toBe('/demo.v1.Catalog/Get')
  expect((screen.getByLabelText('Repository universe') as HTMLTextAreaElement).value)
    .toBe(repository)
  expect(api.fetchWorkbench).not.toHaveBeenCalled()
  expect(api.previewWorkbench).not.toHaveBeenCalled()
  expect(api.createWorkbench).not.toHaveBeenCalled()
})

test.each<WorkbenchTicketKind>(['add', 'modify', 'migrate', 'retire'])(
  '%s reaches an exact revision-bound What view',
  async (kind) => {
    api.fetchWorkbench.mockResolvedValue(view(kind))
    page(
      `investigation_id=${investigationID}&revision_id=${revisionID}&step=what`,
    )
    expect(await screen.findByRole('heading', { name: 'What changes?' }))
      .toBeTruthy()
    expect(
      (screen.getByLabelText('Workbench change mode') as HTMLSelectElement).value,
    ).toBe(kind)
    expect(screen.getByRole('link', { name: /What/ }).getAttribute('href'))
      .toBe(
        `#/workbench?step=what&investigation_id=${investigationID}` +
        `&revision_id=${revisionID}`,
      )
    expect(screen.getByText(revisionID)).toBeTruthy()
    expect(screen.getByText('Revision exact')).toBeTruthy()
    expect(api.previewWorkbench).not.toHaveBeenCalled()
    expect(api.createWorkbench).not.toHaveBeenCalled()
    expect(api.reviseWorkbench).not.toHaveBeenCalled()
  },
)

test('back, forward, and step deep links retain the exact revision without refetching', async () => {
  api.fetchWorkbench.mockResolvedValue(view('retire'))
  const result = page(
    `investigation_id=${investigationID}&revision_id=${revisionID}&step=what`,
  )
  await screen.findByRole('heading', { name: 'What changes?' })
  result.rerender(wrapped(new URLSearchParams(
    `investigation_id=${investigationID}&revision_id=${revisionID}&step=where`,
  )))
  expect(screen.getByRole('heading', { name: 'Where is impact visible?' }))
    .toBeTruthy()
  result.rerender(wrapped(new URLSearchParams(
    `investigation_id=${investigationID}&revision_id=${revisionID}&step=what`,
  )))
  expect(screen.getByRole('heading', { name: 'What changes?' })).toBeTruthy()
  expect(api.fetchWorkbench).toHaveBeenCalledTimes(1)
  expect(api.previewWorkbench).not.toHaveBeenCalled()
  expect(api.reviseWorkbench).not.toHaveBeenCalled()
})

test('leaving an exact revision clears it from the unpinned Workbench home', async () => {
  api.fetchWorkbench.mockResolvedValue(view('retire'))
  const result = page(
    `investigation_id=${investigationID}&revision_id=${revisionID}&step=what`,
  )
  await screen.findByRole('heading', { name: 'What changes?' })
  result.rerender(wrapped(new URLSearchParams()))
  expect(screen.getByRole('heading', { name: 'Paste bounded ticket context' }))
    .toBeTruthy()
  expect(screen.queryByText(revisionID)).toBeNull()
  expect(api.fetchWorkbench).toHaveBeenCalledTimes(1)
})

test('refuses silent revision retarget and offers an explicit current link', async () => {
  api.fetchWorkbench.mockResolvedValue(view('modify', nextRevisionID))
  page(
    `investigation_id=${investigationID}&revision_id=${revisionID}&step=what`,
  )
  expect(await screen.findByRole('heading', { name: 'Revision changed' }))
    .toBeTruthy()
  expect(screen.getByText(/will not silently retarget/)).toBeTruthy()
  expect(
    screen.getByRole('link', { name: 'Open current revision explicitly' })
      .getAttribute('href'),
  ).toBe(
    `#/workbench?investigation_id=${investigationID}` +
    `&revision_id=${nextRevisionID}&step=what`,
  )
})

test('shows loading and permission-loss paths without disclosing identity', async () => {
  let rejectLoad!: (cause: unknown) => void
  api.fetchWorkbench.mockReturnValue(new Promise((_resolve, reject) => {
    rejectLoad = reject
  }))
  page(
    `investigation_id=${investigationID}&revision_id=${revisionID}&step=what`,
  )
  expect(screen.getByRole('status').textContent)
    .toContain('Loading exact Workbench revision')
  rejectLoad(new WorkbenchAPIError(404, 'secret repository'))
  expect(await screen.findByRole('heading', {
    name: 'Workbench unavailable or permission changed',
  })).toBeTruthy()
  expect(screen.getByText(/No repository or Investigation identity was disclosed/))
    .toBeTruthy()
  expect(screen.queryByText('secret repository')).toBeNull()
  expect(screen.getByRole('button', { name: 'Retry exact link' })).toBeTruthy()
})

test('preview is explicit, unavailable compatibility stays honest, and edits expire it', async () => {
  api.fetchWorkbench.mockResolvedValue(view('retire'))
  api.previewWorkbench.mockResolvedValue(preview())
  page(
    `investigation_id=${investigationID}&revision_id=${revisionID}&step=what`,
  )
  await screen.findByRole('heading', { name: 'What changes?' })
  expect(screen.getByText(/No preview has run/)).toBeTruthy()
  expect(api.previewWorkbench).not.toHaveBeenCalled()

  fireEvent.click(screen.getByRole('button', { name: 'Preview revision' }))
  expect(await screen.findByText('Ready')).toBeTruthy()
  expect(screen.getByText('unavailable')).toBeTruthy()
  expect(screen.getByText(/Unavailable is not a compatible verdict/))
    .toBeTruthy()
  expect(api.reviseWorkbench).not.toHaveBeenCalled()
  const append = screen.getByRole('button', { name: 'Append revision' })
  expect(append.hasAttribute('disabled')).toBe(false)

  fireEvent.change(screen.getByLabelText('Repository universe'), {
    target: { value: `${repository}\ngithub.com/acme/consumer` },
  })
  expect(screen.getByText('Preview drifted')).toBeTruthy()
  expect(screen.getByText('Preview expired after edits')).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Refresh preview' })).toBeTruthy()
  expect(append.hasAttribute('disabled')).toBe(true)
})

test('a ready preview is the only path to create or revise, and conflicts remain retryable', async () => {
  api.fetchWorkbench.mockResolvedValue(view('retire'))
  api.previewWorkbench.mockResolvedValue(preview())
  api.reviseWorkbench.mockRejectedValue(
    new WorkbenchAPIError(409, 'digest mismatch'),
  )
  page(
    `investigation_id=${investigationID}&revision_id=${revisionID}&step=what`,
  )
  await screen.findByRole('heading', { name: 'What changes?' })
  const append = screen.getByRole('button', { name: 'Append revision' })
  expect(append.hasAttribute('disabled')).toBe(true)
  fireEvent.click(screen.getByRole('button', { name: 'Preview revision' }))
  await screen.findByText('Ready')
  fireEvent.click(append)
  expect(await screen.findByText('Workbench revision conflict.', {
    exact: false,
  })).toBeTruthy()
  expect(api.reviseWorkbench).toHaveBeenCalledWith(
    investigationID,
    expect.objectContaining({
      preview_digest: preview().preview_digest,
      idempotency_key: `workbench-ui-${preview().preview_digest}`,
    }),
  )
  expect(screen.getByRole('button', { name: 'Preview revision' })).toBeTruthy()
})

test('client-side proposal limits refuse before preview or mutation', () => {
  page()
  fireEvent.change(screen.getByLabelText('Workbench title'), {
    target: { value: 'Add a bounded operation' },
  })
  fireEvent.change(screen.getByLabelText('Change mode'), {
    target: { value: 'add' },
  })
  fireEvent.change(screen.getByLabelText('Ticket text'), {
    target: { value: 'Add the operation.' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Shape the Why' }))
  fireEvent.click(screen.getByRole('link', { name: /What/ }))
  // The unit receives params directly, so exercise the exact What route with
  // the Atlas seed while preserving the same bounded proposal controls.
  cleanup()
  page(
    'source=atlas&step=what&protocol=protobuf&repository=' +
    'github.com%2Facme%2Fcontracts&lineage=lineage-v1&operation=' +
    '%2Fdemo.v1.Catalog%2FGet',
  )
  fireEvent.change(screen.getByLabelText('Proposal file 1 path'), {
    target: { value: 'proto/catalog.proto' },
  })
  fireEvent.change(screen.getByLabelText('Proposal file 1 source'), {
    target: { value: 'x'.repeat((4 << 20) + 1) },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Preview revision' }))
  expect(screen.getByText('Source limit refusal.', { exact: false })).toBeTruthy()
  expect(screen.getByText(/Nothing was sent or written/)).toBeTruthy()
  expect(api.previewWorkbench).not.toHaveBeenCalled()
  expect(api.createWorkbench).not.toHaveBeenCalled()
})

test('create uses the reviewed digest and navigates to the exact returned revision', async () => {
  api.previewWorkbench.mockResolvedValue({
    ...preview(),
    operation: 'create',
  })
  api.createWorkbench.mockResolvedValue(view('add'))
  page(
    'source=atlas&step=what&protocol=protobuf&repository=' +
    'github.com%2Facme%2Fcontracts&lineage=lineage-v1&operation=' +
    '%2Fdemo.v1.Catalog%2FGet',
  )
  fireEvent.change(screen.getByLabelText('Proposal file 1 path'), {
    target: { value: 'proto/catalog.proto' },
  })
  fireEvent.change(screen.getByLabelText('Proposal file 1 source'), {
    target: { value: 'syntax = "proto3";' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Preview revision' }))
  await screen.findByText('Ready')
  fireEvent.click(screen.getByRole('button', { name: 'Create Workbench' }))
  await waitFor(() => expect(api.createWorkbench).toHaveBeenCalledTimes(1))
  expect(api.createWorkbench).toHaveBeenCalledWith(expect.objectContaining({
    preview_digest: preview().preview_digest,
    idempotency_key: `workbench-ui-${preview().preview_digest}`,
  }))
  expect(window.location.hash).toBe(
    `#/workbench?investigation_id=${investigationID}` +
    `&revision_id=${revisionID}&step=what`,
  )
})
