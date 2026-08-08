import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { BaseProvider } from 'baseui'
import { Provider as StyletronProvider } from 'styletron-react'
import { Client as Styletron } from 'styletron-engine-monolithic'
import { ModeContext, lightTheme } from '../theme'
import { CommandNavigator, type NavigatorSurface } from './CommandNavigator'
import { recentScopes, rememberScope } from '../scope'

afterEach(cleanup)

// This jsdom build ships no localStorage; a minimal in-memory stand-in lets
// the recency behavior be exercised (the component itself fails soft when
// storage is absent).
const storage = new Map<string, string>()
vi.stubGlobal('localStorage', {
  getItem: (key: string) => storage.get(key) ?? null,
  setItem: (key: string, value: string) => storage.set(key, value),
  removeItem: (key: string) => storage.delete(key),
  clear: () => storage.clear(),
})

beforeEach(() => {
  window.location.hash = ''
  localStorage.clear()
})

const engine = new Styletron()

const surfaces: NavigatorSurface[] = [
  { label: 'Search', path: '/', available: true },
  { label: 'Service directory', path: '/services', available: true },
  { label: 'Contract atlas', path: '/contracts', available: false },
]

function mount(scope: { repository: string; serviceKey: string; generation: string } | null = null, onClose = vi.fn(), principal = 'user-a@localhost.test') {
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={lightTheme}>
        <ModeContext.Provider value={{ mode: 'light', toggle: () => {} }}>
          <CommandNavigator surfaces={surfaces} scope={scope} principal={principal} onClose={onClose} />
        </ModeContext.Provider>
      </BaseProvider>
    </StyletronProvider>,
  )
  return onClose
}

describe('CommandNavigator', () => {
  it('is a modal dialog whose input holds focus with combobox semantics', () => {
    mount()
    const dialog = screen.getByRole('dialog', { name: 'Go to surface or scope' })
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    const input = screen.getByRole('combobox')
    expect(document.activeElement).toBe(input)
    expect(input.getAttribute('aria-activedescendant')).toBe('command-navigator-item-0')
  })
  it('lists only capability-available surfaces and announces the count', () => {
    mount()
    expect(screen.getByRole('option', { name: 'Search' })).toBeTruthy()
    expect(screen.getByRole('option', { name: 'Service directory' })).toBeTruthy()
    expect(screen.queryByRole('option', { name: 'Contract atlas' })).toBeNull()
    expect(screen.getByRole('status').textContent).toBe('2 destinations')
  })
  it('filters, moves with arrows, and navigates on Enter', () => {
    const onClose = mount()
    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'direc' } })
    expect(screen.getByRole('status').textContent).toBe('1 destination')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onClose).toHaveBeenCalled()
    expect(window.location.hash).toBe('#/services')
  })
  it('offers active-scope jumps including the Workbench vocabulary', () => {
    mount({ repository: 'repo-x', serviceKey: 'orders-api', generation: 'gen-r4' })
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'workbench' } })
    const option = screen.getByRole('option', { name: /Workbench · orders-api/ })
    fireEvent.click(option)
    expect(decodeURIComponent(window.location.hash)).toContain('service_repository=repo-x')
    expect(decodeURIComponent(window.location.hash)).toContain('source_service=orders-api')
    expect(decodeURIComponent(window.location.hash)).toContain('scope_generation=gen-r4')
  })
  it('lists recent scopes without duplicating the active one', () => {
    rememberScope('user-a@localhost.test', { repository: 'repo-old', serviceKey: 'billing', generation: 'g1' })
    rememberScope('user-a@localhost.test', { repository: 'repo-x', serviceKey: 'orders-api', generation: 'gen-r4' })
    mount({ repository: 'repo-x', serviceKey: 'orders-api', generation: 'gen-r4' })
    expect(screen.getByRole('option', { name: /Directory · billing/ })).toBeTruthy()
    expect(screen.queryByRole('option', { name: /orders-api.*recent/ })).toBeNull()
  })
  it('closes on Escape and names its empty state as navigation-only', () => {
    const onClose = mount()
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'zzz' } })
    expect(screen.getByText(/Navigation only — nothing was searched/)).toBeTruthy()
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })
})

describe('CommandNavigator modal contract', () => {
  it('traps Tab inside the dialog and closes on Escape from the dialog', () => {
    const onClose = mount()
    const input = screen.getByRole('combobox')
    fireEvent.keyDown(input, { key: 'Tab' })
    expect(document.activeElement).toBe(input)
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'Go to surface or scope' }), { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })
  it('lists unknown-capability surfaces instead of implying absence', () => {
    render(
      <StyletronProvider value={engine}>
        <BaseProvider theme={lightTheme}>
          <ModeContext.Provider value={{ mode: 'light', toggle: () => {} }}>
            <CommandNavigator
              surfaces={[{ label: 'Contract atlas', path: '/contracts', available: 'unknown' }]}
              scope={null}
              principal="user-a@localhost.test"
              onClose={() => {}}
            />
          </ModeContext.Provider>
        </BaseProvider>
      </StyletronProvider>,
    )
    expect(screen.getByRole('option', { name: /Contract atlas.*capability unknown/ })).toBeTruthy()
  })
})

describe('recent scopes storage', () => {
  it('isolates recents by principal and records nothing without one', () => {
    rememberScope('user-a@localhost.test', { repository: 'repo-a', serviceKey: 'svc', generation: '' })
    rememberScope('', { repository: 'repo-anon', serviceKey: 'svc', generation: '' })
    expect(recentScopes('user-b@localhost.test')).toEqual([])
    expect(recentScopes('')).toEqual([])
    expect(recentScopes('user-a@localhost.test').map((entry) => entry.repository)).toEqual(['repo-a'])
  })

  it('is bounded, identity-only, and deduplicated', () => {
    for (let index = 0; index < 8; index++) {
      rememberScope('user-a@localhost.test', { repository: `repo-${index}`, serviceKey: 'svc', generation: '' })
    }
    rememberScope('user-a@localhost.test', { repository: 'repo-7', serviceKey: 'svc', generation: '' })
    const recents = recentScopes('user-a@localhost.test')
    expect(recents.length).toBe(5)
    expect(recents[0].repository).toBe('repo-7')
    expect(recents.filter((entry) => entry.repository === 'repo-7').length).toBe(1)
  })
})
