import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider } from 'baseui'
import InvestigationPage from './InvestigationPage'
import type {
  InvestigationEnvelope,
  InvestigationOutcome,
  InvestigationViewDocument,
  InvestigationViewSummary,
} from '../api'
import { lightTheme, ModeContext } from '../theme'
import fixture01 from '../../../docs/fixtures/investigations/01-complete-with-findings.json?raw'
import fixture02 from '../../../docs/fixtures/investigations/02-complete-zero-findings.json?raw'
import fixture03 from '../../../docs/fixtures/investigations/03-partial-failed-processing.json?raw'
import fixture04 from '../../../docs/fixtures/investigations/04-unresolved-attribution.json?raw'
import fixture05 from '../../../docs/fixtures/investigations/05-stale-pack-validation.json?raw'

const api = vi.hoisted(() => ({
  fetchInvestigationViews: vi.fn(),
  fetchInvestigationView: vi.fn(),
}))

vi.mock('../api', () => api)

const specs = [
  ['01', fixture01, 'Complete with findings', 'complete_with_findings'],
  ['02', fixture02, 'Complete with no supported facts', 'complete_zero_findings'],
  ['03', fixture03, 'Partial and failed processing', 'partial_failed_processing'],
  ['04', fixture04, 'Unresolved attribution', 'unresolved_attribution'],
  ['05', fixture05, 'Stale pack validation', 'stale_pack_validation'],
] as const

const documents = Object.fromEntries(specs.map(([id, raw, title, state]) => {
  const envelope = JSON.parse(raw) as InvestigationEnvelope
  return [id, {
    id,
    title,
    state,
    outcome: envelope.outcome,
    envelope,
  } satisfies InvestigationViewDocument]
})) as Record<string, InvestigationViewDocument>

const summaries: InvestigationViewSummary[] = specs.map(([id, _raw, title, state]) => ({
  id,
  title,
  state,
  outcome: documents[id].outcome as InvestigationOutcome,
}))

const engine = new Client()

function page(id: string) {
  return render(
    <StyletronProvider value={engine}>
      <ModeContext value={{ mode: 'light', toggle: () => {} }}>
        <BaseProvider theme={lightTheme}>
          <InvestigationPage params={new URLSearchParams(`id=${id}`)} />
        </BaseProvider>
      </ModeContext>
    </StyletronProvider>,
  )
}

beforeEach(() => {
  api.fetchInvestigationViews.mockReset().mockResolvedValue(summaries)
  api.fetchInvestigationView.mockReset().mockImplementation((id: string) => Promise.resolve(documents[id]))
  window.location.hash = ''
})

afterEach(cleanup)

test('fixture 01 renders evidenced findings, proof references, and expandable derivation', async () => {
  page('01')
  await screen.findByRole('heading', { name: documents['01'].envelope.scope.normalized_question })
  for (const label of ['What is evidenced', 'What remains unknown', 'What changed', 'What needs action']) {
    expect(screen.getByRole('heading', { name: label })).toBeTruthy()
  }
  expect(screen.getByText('2', { selector: 'div' })).toBeTruthy()
  fireEvent.click(screen.getByRole('tab', { name: 'Census' }))
  expect(await screen.findByText('syn-occ-frontend')).toBeTruthy()
  expect(screen.getByText('syn-occ-ledger')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Expand syn-occ-frontend' }))
  expect(screen.getAllByText('Resolved').length).toBeGreaterThan(0)
  fireEvent.click(screen.getByRole('tab', { name: 'Evidence' }))
  expect(await screen.findByText('syn-proof-frontend')).toBeTruthy()
  expect(screen.getByText('syn-proof-ledger')).toBeTruthy()
})

test('fixture 02 renders eligible bounded absence with authoritative wording and no write path', async () => {
  page('02')
  const eligibility = await screen.findByTestId('derived-eligibility')
  expect(within(eligibility).getByText('Eligible for bounded absence statement')).toBeTruthy()
  expect(within(eligibility).queryByRole('button')).toBeNull()
  expect(within(eligibility).queryByRole('combobox')).toBeNull()
  fireEvent.click(screen.getByRole('tab', { name: 'Census' }))
  expect(await screen.findByText(/No supported client call sites were found within visibility scope/)).toBeTruthy()
  expect(document.body.textContent).not.toContain('No results')
})

test('fixture 03 distinguishes incomplete analyzed scope and exposes processing blockers', async () => {
  page('03')
  const eligibility = await screen.findByTestId('derived-eligibility')
  expect(within(eligibility).getByText('Not eligible')).toBeTruthy()
  expect(within(eligibility).getByText('UNITS_FAILED · UNITS_PARTIAL')).toBeTruthy()
  fireEvent.click(screen.getByRole('tab', { name: 'Census' }))
  expect(await screen.findByText('No supported facts were found among analyzed units.')).toBeTruthy()
  expect(screen.getByText('5')).toBeTruthy()
  expect(screen.getByText('3')).toBeTruthy()
  expect(screen.getByText('PHEBS_SYNTHETIC_GRPC.GENERATED_CODE_EXCLUDED')).toBeTruthy()
})

test('fixture 04 keeps evidenced facts while rendering unresolved attribution separately', async () => {
  page('04')
  await screen.findByRole('heading', { name: documents['04'].envelope.scope.normalized_question })
  fireEvent.click(screen.getByRole('tab', { name: 'Census' }))
  expect((await screen.findAllByText('Unresolved')).length).toBeGreaterThanOrEqual(2)
  expect(screen.getByText('syn-occ-batch')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Expand syn-occ-batch' }))
  await waitFor(() => expect(screen.getAllByText('PHEBS_SYNTHETIC_GRPC.WRAPPER_INDIRECTION').length).toBe(2))
  expect(screen.getAllByText('1 proof ref')).toHaveLength(2)
})

test('fixture 05 renders refusal without claim-bearing census or coverage counts', async () => {
  page('05')
  expect(await screen.findByRole('heading', { name: 'Pack workflow unavailable' })).toBeTruthy()
  expect(screen.queryByRole('tablist', { name: 'Investigation views' })).toBeNull()
  expect(screen.getByRole('status').textContent).toContain('views are unavailable because this request was refused')
  expect(screen.getByText('This pack is suspended and cannot serve claim-bearing requests.')).toBeTruthy()
  expect(screen.getByText('PACK_VALIDATION_EXPIRED')).toBeTruthy()
  expect(screen.queryByRole('heading', { name: 'Consumer census' })).toBeNull()
  expect(screen.queryByRole('heading', { name: 'Coverage' })).toBeNull()
  expect(document.body.textContent).not.toContain('No results')
})

test('view selector routes between authorized Investigation projections', async () => {
  page('04')
  const selector = await screen.findByRole('combobox', { name: 'Investigation view' })
  fireEvent.change(selector, { target: { value: '02' } })
  expect(window.location.hash).toBe('#/investigations?id=02')
})
