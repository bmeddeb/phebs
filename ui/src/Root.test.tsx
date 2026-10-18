import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider } from 'styletron-react'
import Root from './Root'
import { useDensity, useMode, usePalette } from './theme'

vi.mock('./auth', () => ({ AuthProvider: ({ children }: { children: React.ReactNode }) => children }))
vi.mock('./App', () => ({ default: PreferenceProbe }))

function PreferenceProbe() {
  const mode = useMode()
  const density = useDensity()
  const palette = usePalette()
  return <>
    <div>{mode.mode} {density.density} {palette.palette}</div>
    <button onClick={mode.toggle}>Toggle mode</button>
    <button onClick={density.toggle}>Toggle density</button>
    <button onClick={() => palette.setPalette('quiet')}>Choose Quiet</button>
  </>
}

function renderRoot() {
  return render(<Provider value={new Client()}><Root /></Provider>)
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  localStorage.clear()
})

test('blocked preference reads cannot prevent application startup', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new DOMException('Blocked', 'SecurityError') })
  renderRoot()
  expect(screen.getByText('light comfortable phebs')).toBeTruthy()
})

test('failed preference writes preserve working in-memory controls', () => {
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('Full', 'QuotaExceededError') })
  renderRoot()
  fireEvent.click(screen.getByRole('button', { name: 'Toggle mode' }))
  fireEvent.click(screen.getByRole('button', { name: 'Toggle density' }))
  fireEvent.click(screen.getByRole('button', { name: 'Choose Quiet' }))
  expect(screen.getByText('dark dense quiet')).toBeTruthy()
  expect(document.documentElement.style.colorScheme).toBe('dark')
})

test('available preference storage still restores and persists choices', () => {
  localStorage.setItem('phebs-theme', 'dark')
  localStorage.setItem('phebs-density', 'dense')
  localStorage.setItem('phebs-palette', 'classic')
  renderRoot()
  expect(screen.getByText('dark dense classic')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Toggle mode' }))
  expect(localStorage.getItem('phebs-theme')).toBe('light')
})
