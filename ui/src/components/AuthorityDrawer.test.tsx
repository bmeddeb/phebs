import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { BaseProvider } from 'baseui'
import { Provider as StyletronProvider } from 'styletron-react'
import { Client as Styletron } from 'styletron-engine-monolithic'
import type { SearchScopeReceipt, ServiceScopeAuthority } from '../api'
import { ModeContext, lightTheme } from '../theme'
import { AuthorityChipButton, AuthorityDrawer, type ScopeCitation } from './AuthorityDrawer'

afterEach(cleanup)

const engine = new Styletron()

const digest = (fill: string) => `sha256:${fill.repeat(64).slice(0, 64)}`

const authority: ServiceScopeAuthority = {
  schema: 'phebs-service-scope-authority-v1',
  predicate_policy: 'accepted-roles-v1',
  topology_policy: 'exact-v1',
  repository: 'example.invalid/mono',
  service_key: 'orders-api',
  status: 'stale',
  incarnation: 3,
  revision_selector: 'branch',
  revision_branch: 'main',
  revision_commit: 'abcdef1234567890abcdef1234567890abcdef12',
  expression_digest: digest('a'),
  current_catalog_generation: 'cat-r5',
  catalog_control_revision: 5,
  active_catalog_generation: 'cat-r4',
  active_source_generation: 'src-r4',
  active_desired_generation: 'des-r4',
  service_state_digest: digest('b'),
  service_state_revision: 9,
  state_summary_digest: digest('c'),
  state_summary_revision: 9,
  repository_source_generation: 'repo-src-r7',
  repository_search_generation: 'repo-search-r7',
  path_digest: digest('d'),
  path_count: 12,
  path_bytes: 640,
  predicate_atoms: 2,
  predicate_bytes: 64,
  digest: digest('e'),
}

const serviceReceipt: SearchScopeReceipt = {
  schema: 'phebs-search-scope-v1',
  kind: 'service',
  repository: 'example.invalid/mono',
  service_key: 'orders-api',
  service_status: 'stale',
  membership_policy: 'accepted-roles-union-shared-included-unowned-excluded-v1',
  expression_digest: digest('a'),
  service_authority: authority,
  revisions: [{ repository: 'example.invalid/mono', commit: 'abcdef1234567890abcdef1234567890abcdef12' }],
  result_set_digest: digest('f'),
  result_files: 2,
  result_matches: 9,
  digest: digest('0'),
}

const citations: ScopeCitation[] = [
  { repository: 'example.invalid/mono', label: 'services/orders/kafka.go', href: '#/file?repo=r&path=a' },
  { repository: 'example.invalid/mono', label: 'services/orders/service.go', href: '#/file?repo=r&path=b' },
]

const allCodeReceipt: SearchScopeReceipt = {
  ...serviceReceipt,
  kind: 'all_code',
  repository: undefined,
  service_key: undefined,
  service_status: undefined,
  service_authority: undefined,
  revisions: [
    { repository: 'example.invalid/mono', commit: 'abcdef1234567890abcdef1234567890abcdef12' },
    { repository: 'example.invalid/second', commit: '1234567890abcdef1234567890abcdef12345678' },
  ],
}

function mount(node: React.ReactNode) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={lightTheme}>
        <ModeContext.Provider value={{ mode: 'light', toggle: () => {} }}>{node}</ModeContext.Provider>
      </BaseProvider>
    </StyletronProvider>,
  )
}

describe('AuthorityChipButton', () => {
  it('wears the service status and exact generation, and opens as a dialog trigger', () => {
    const onOpen = vi.fn()
    mount(<AuthorityChipButton receipt={serviceReceipt} onOpen={onOpen} />)
    const chip = screen.getByRole('button', { name: 'stale · as of abcdef1' })
    expect(chip.getAttribute('aria-haspopup')).toBe('dialog')
    fireEvent.click(chip)
    expect(onOpen).toHaveBeenCalledTimes(1)
  })
  it('names an empty result set instead of faking a pin', () => {
    mount(<AuthorityChipButton receipt={{ ...serviceReceipt, revisions: [], result_files: 0, result_matches: 0 }} onOpen={() => {}} />)
    expect(screen.getByRole('button', { name: 'stale · no cited results' })).toBeTruthy()
  })
  it('counts revision pins for all-code scope', () => {
    mount(<AuthorityChipButton receipt={allCodeReceipt} onOpen={() => {}} />)
    expect(screen.getByRole('button', { name: '2 revision pins' })).toBeTruthy()
  })
})

describe('AuthorityDrawer', () => {
  it('is a real dialog with an accessible name', () => {
    mount(<AuthorityDrawer receipt={serviceReceipt} citations={citations} open onClose={() => {}} />)
    const dialog = screen.getByRole('dialog', { name: 'Search scope authority' })
    expect(dialog.getAttribute('aria-modal')).toBe('true')
  })
  it('presents the service authority generations, marking catalog lag', () => {
    mount(<AuthorityDrawer receipt={serviceReceipt} citations={citations} open onClose={() => {}} />)
    expect(screen.getByText('Service authority')).toBeTruthy()
    expect(screen.getByText('cat-r4')).toBeTruthy()
    expect(screen.getByText(/current is/)).toBeTruthy()
    expect(screen.getByText('cat-r5')).toBeTruthy()
    expect(screen.getByText('src-r4')).toBeTruthy()
    expect(screen.getByText('repo-search-r7')).toBeTruthy()
  })
  it('presents state, scope, revision pins, and digests only in the receipt section', () => {
    mount(<AuthorityDrawer receipt={serviceReceipt} citations={citations} open onClose={() => {}} />)
    expect(screen.getByText('stale')).toBeTruthy()
    expect(screen.getByText('orders-api')).toBeTruthy()
    expect(screen.getByText('Revision pins (1)')).toBeTruthy()
    const details = screen.getByText('Receipt').closest('details')!
    expect(details.open).toBe(false)
    for (const value of [digest('0'), digest('a'), digest('f'), digest('e'), digest('b')]) {
      expect(details.contains(screen.getByText(value))).toBe(true)
    }
  })
  it('renders citations as linked objects', () => {
    mount(<AuthorityDrawer receipt={serviceReceipt} citations={citations} open onClose={() => {}} />)
    expect(screen.getByText('Citations (2)')).toBeTruthy()
    const link = screen.getByText('services/orders/kafka.go').closest('a')!
    expect(link.getAttribute('href')).toBe('#/file?repo=r&path=a')
  })
  it('verifies the receipt against rendered results — consistent case', () => {
    mount(<AuthorityDrawer receipt={serviceReceipt} citations={citations} open onClose={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: 'Check receipt against rendered results' }))
    expect(screen.getByText('consistent')).toBeTruthy()
    expect(screen.getByText(/match the receipt count/)).toBeTruthy()
  })
  it('verifies the receipt against rendered results — mismatch case', () => {
    mount(<AuthorityDrawer receipt={serviceReceipt} citations={[citations[0]]} open onClose={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: 'Check receipt against rendered results' }))
    expect(screen.getByText('mismatch')).toBeTruthy()
    expect(screen.getByText(/counts 2 cited files but 1 are rendered/)).toBeTruthy()
  })
  it('copies digests and the full receipt JSON, reporting success only after the write resolves', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    mount(<AuthorityDrawer receipt={serviceReceipt} citations={citations} open onClose={() => {}} />)
    fireEvent.click(screen.getByText('Receipt'))
    fireEvent.click(screen.getByRole('button', { name: 'Copy Receipt digest' }))
    expect(writeText).toHaveBeenCalledWith(digest('0'))
    expect(await screen.findByRole('button', { name: 'Copied Receipt digest' })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Copy receipt JSON' }))
    expect(JSON.parse(writeText.mock.calls.at(-1)![0])).toEqual(serviceReceipt)
    expect(await screen.findByRole('button', { name: 'Receipt copied' })).toBeTruthy()
  })
  it('reports clipboard failure instead of claiming success', async () => {
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockRejectedValue(new Error('denied')) } })
    mount(<AuthorityDrawer receipt={serviceReceipt} citations={citations} open onClose={() => {}} />)
    fireEvent.click(screen.getByText('Receipt'))
    fireEvent.click(screen.getByRole('button', { name: 'Copy Receipt digest' }))
    expect(await screen.findByRole('button', { name: 'Copy failed: Receipt digest' })).toBeTruthy()
  })
  it('closes on escape', () => {
    const onClose = vi.fn()
    mount(<AuthorityDrawer receipt={serviceReceipt} citations={citations} open onClose={onClose} />)
    fireEvent.keyUp(document.body, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })
  it('states the all-code boundary without implying completeness', () => {
    mount(<AuthorityDrawer receipt={allCodeReceipt} open onClose={() => {}} />)
    expect(screen.getByText(/not a completeness claim/)).toBeTruthy()
    expect(screen.getByText('Revision pins (2)')).toBeTruthy()
    expect(screen.queryByText('Service authority')).toBeNull()
  })
})
