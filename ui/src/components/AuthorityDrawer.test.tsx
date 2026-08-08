import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { BaseProvider } from 'baseui'
import { Provider as StyletronProvider } from 'styletron-react'
import { Client as Styletron } from 'styletron-engine-monolithic'
import type { SearchScopeReceipt } from '../api'
import { ModeContext, lightTheme } from '../theme'
import { AuthorityChipButton, AuthorityDrawer } from './AuthorityDrawer'

afterEach(cleanup)

const engine = new Styletron()

const serviceReceipt: SearchScopeReceipt = {
  schema: 'phebs-search-scope-v1',
  kind: 'service',
  repository: 'example.invalid/mono',
  service_key: 'orders-api',
  service_status: 'stale',
  membership_policy: 'accepted-roles-union-shared-included-unowned-excluded-v1',
  expression_digest: 'sha256:expression',
  revisions: [{ repository: 'example.invalid/mono', commit: 'abcdef1234567890' }],
  result_set_digest: 'sha256:results',
  result_files: 3,
  result_matches: 9,
  digest: 'sha256:receipt',
}

const allCodeReceipt: SearchScopeReceipt = {
  ...serviceReceipt,
  kind: 'all_code',
  repository: undefined,
  service_key: undefined,
  service_status: undefined,
  revisions: [
    { repository: 'example.invalid/mono', commit: 'abcdef1234567890' },
    { repository: 'example.invalid/second', commit: '1234567890abcdef' },
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
  it('counts revision pins for all-code scope', () => {
    mount(<AuthorityChipButton receipt={allCodeReceipt} onOpen={() => {}} />)
    expect(screen.getByRole('button', { name: '2 revision pins' })).toBeTruthy()
  })
})

describe('AuthorityDrawer', () => {
  it('presents state, scope, generations, and result set — with digests only in the receipt section', () => {
    mount(<AuthorityDrawer receipt={serviceReceipt} open onClose={() => {}} />)
    expect(screen.getByText('Search scope authority')).toBeTruthy()
    expect(screen.getByText('stale')).toBeTruthy()
    expect(screen.getByText('orders-api')).toBeTruthy()
    expect(screen.getByText('abcdef1234567890')).toBeTruthy()
    expect(screen.getByText('3')).toBeTruthy()
    const details = screen.getByText('Receipt').closest('details')!
    expect(details.open).toBe(false)
    expect(details.contains(screen.getByText('sha256:receipt'))).toBe(true)
    expect(details.contains(screen.getByText('sha256:expression'))).toBe(true)
  })
  it('copies digests and the full receipt JSON', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    mount(<AuthorityDrawer receipt={serviceReceipt} open onClose={() => {}} />)
    fireEvent.click(screen.getByText('Receipt'))
    fireEvent.click(screen.getByRole('button', { name: 'Copy Receipt digest' }))
    expect(writeText).toHaveBeenCalledWith('sha256:receipt')
    fireEvent.click(screen.getByRole('button', { name: 'Copy receipt JSON' }))
    expect(JSON.parse(writeText.mock.calls.at(-1)![0])).toEqual(serviceReceipt)
  })
  it('closes on escape', () => {
    const onClose = vi.fn()
    mount(<AuthorityDrawer receipt={serviceReceipt} open onClose={onClose} />)
    fireEvent.keyUp(document.body, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })
  it('states the all-code boundary without implying completeness', () => {
    mount(<AuthorityDrawer receipt={allCodeReceipt} open onClose={() => {}} />)
    expect(screen.getByText(/not a completeness claim/)).toBeTruthy()
    expect(screen.getByText('Generations (2)')).toBeTruthy()
  })
})
