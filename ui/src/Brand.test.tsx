import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider } from 'baseui'
import { BrandLoader, BrandLockup } from './Brand'
import { lightTheme, ModeContext, TOKENS } from './theme'

const engine = new Client()

function view(node: React.ReactNode) {
  return render(
    <StyletronProvider value={engine}>
      <ModeContext value={{ mode: 'light', toggle: () => {} }}>
        <BaseProvider theme={lightTheme}>{node}</BaseProvider>
      </ModeContext>
    </StyletronProvider>,
  )
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

test('brand lockup uses the Context Port geometry and current theme colors', () => {
  view(<BrandLockup href="#/" />)

  const link = screen.getByRole('link', { name: 'phebs' })
  const paths = link.querySelectorAll('path')
  const circles = link.querySelectorAll('circle')
  expect(paths[0].getAttribute('d')).toContain('M21 8.5')
  expect(paths[0].getAttribute('stroke')).toBe('currentColor')
  expect(paths[1].getAttribute('stroke')).toBe(TOKENS.light.gutter)
  expect(circles[0].getAttribute('fill')).toBe(TOKENS.light.accent)
})

test('brand lockup is static — hover triggers no animation (T43.12f)', () => {
  view(<BrandLockup href="#/" />)
  const link = screen.getByRole('link', { name: 'phebs' })
  const spies = Array.from(link.querySelectorAll('circle, path')).map((node) => {
    const spy = vi.fn()
    Object.defineProperty(node, 'animate', { configurable: true, value: spy })
    return spy
  })
  fireEvent.mouseEnter(link)
  for (const spy of spies) expect(spy).not.toHaveBeenCalled()
})

test('brand loader renders without Web Animations support', () => {
  view(<BrandLoader />)
  expect(screen.getByRole('status', { name: 'Loading phebs' })).toBeTruthy()
})
