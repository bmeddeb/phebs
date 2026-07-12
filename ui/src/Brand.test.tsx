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
  expect(circles[1].getAttribute('fill')).toBe(TOKENS.light.accent)
})

test('brand lockup docks on hover and skips motion when reduced motion is enabled', () => {
  let reduce = false
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: reduce })))
  view(<BrandLockup href="#/" />)

  const link = screen.getByRole('link', { name: 'phebs' })
  const [pulse, dot] = Array.from(link.querySelectorAll('circle'))
  const pulseAnimate = vi.fn((
    _keyframes: Keyframe[] | PropertyIndexedKeyframes | null,
    _options?: number | KeyframeAnimationOptions,
  ) => ({ cancel: vi.fn() }) as unknown as Animation)
  const dotAnimate = vi.fn((
    _keyframes: Keyframe[] | PropertyIndexedKeyframes | null,
    _options?: number | KeyframeAnimationOptions,
  ) => ({ cancel: vi.fn() }) as unknown as Animation)
  Object.defineProperty(pulse, 'animate', { configurable: true, value: pulseAnimate })
  Object.defineProperty(dot, 'animate', { configurable: true, value: dotAnimate })

  fireEvent.mouseEnter(link)
  expect(dotAnimate).toHaveBeenCalledTimes(1)
  expect(dotAnimate.mock.calls[0][1]).toMatchObject({ duration: 550 })
  expect(pulseAnimate).toHaveBeenCalledTimes(1)
  expect(pulseAnimate.mock.calls[0][1]).toMatchObject({ duration: 450, delay: 400 })

  reduce = true
  fireEvent.mouseEnter(link)
  expect(dotAnimate).toHaveBeenCalledTimes(1)
  expect(pulseAnimate).toHaveBeenCalledTimes(1)
})

test('brand loader renders without Web Animations support', () => {
  view(<BrandLoader />)
  expect(screen.getByRole('status', { name: 'Loading phebs' })).toBeTruthy()
})
