import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import { SectionHelp } from './SectionHelp'
import { glossaryTerms } from '../glossary.generated'

const engine = new Client()
const matchingTerm = glossaryTerms.find((term) => term.id === 'matching_static_evidence')
const defaultViewport = {
  width: window.innerWidth,
  height: window.innerHeight,
}

function help(
  capabilities: ReadonlySet<string> = new Set(['contract-impact-report']),
) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <SectionHelp
          termId="matching_static_evidence"
          enabledCapabilities={capabilities}
        />
      </BaseProvider>
    </StyletronProvider>,
  )
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    value: defaultViewport.width,
  })
  Object.defineProperty(window, 'innerHeight', {
    configurable: true,
    value: defaultViewport.height,
  })
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: undefined,
  })
})

test('hover and keyboard focus expose generated short and expanded help', async () => {
  help()
  const trigger = screen.getByRole('button', { name: 'Help for Matching static evidence' })
  expect(trigger.getAttribute('title')).toBeNull()
  const description = document.getElementById(trigger.getAttribute('aria-describedby') ?? '')
  expect(description?.textContent).toBe(matchingTerm?.shortHelp)

  fireEvent.mouseEnter(trigger)
  const dialog = await screen.findByRole('dialog', { name: 'Matching static evidence help' })
  const content = within(dialog)
  expect(content.getByText(matchingTerm?.shortHelp ?? '')).toBeTruthy()
  expect(content.getByText(matchingTerm?.expandedHelp ?? '')).toBeTruthy()
  expect(content.getByText(matchingTerm?.evidenceBoundary ?? '')).toBeTruthy()
  expect(content.getByText(matchingTerm?.authorityBoundary ?? '')).toBeTruthy()
  expect(trigger.getAttribute('aria-expanded')).toBe('true')
  expect(window.getComputedStyle(dialog).maxWidth).toBe('calc(100vw - 24px)')
  expect(window.getComputedStyle(dialog).maxHeight).toBe('calc(100vh - 24px)')

  fireEvent.mouseLeave(trigger)
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

  fireEvent.focus(trigger)
  expect(await screen.findByRole('dialog')).toBeTruthy()
  fireEvent.blur(trigger)
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

test('hover remains open while the pointer crosses into the portaled dialog', async () => {
  help()
  const trigger = screen.getByRole('button', { name: 'Help for Matching static evidence' })
  fireEvent.mouseEnter(trigger)
  const dialog = await screen.findByRole('dialog')

  fireEvent.mouseLeave(trigger)
  fireEvent.mouseEnter(dialog)
  await new Promise((resolve) => window.setTimeout(resolve, 180))
  expect(screen.getByRole('dialog')).toBeTruthy()

  fireEvent.mouseLeave(dialog)
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

test('click pins help and Escape closes it with focus returned', async () => {
  help()
  const trigger = screen.getByRole('button', { name: 'Help for Matching static evidence' })
  fireEvent.click(trigger)
  await screen.findByRole('dialog')
  screen.getByRole('button', { name: 'Close Matching static evidence help' }).focus()
  fireEvent.keyDown(document, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(trigger))
  expect(screen.queryByRole('dialog')).toBeNull()
})

test('close button and outside click dismiss pinned help', async () => {
  help()
  const trigger = screen.getByRole('button', { name: 'Help for Matching static evidence' })
  fireEvent.click(trigger)
  const close = await screen.findByRole('button', { name: 'Close Matching static evidence help' })
  close.focus()
  fireEvent.click(close)
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(trigger))
  expect(screen.queryByRole('dialog')).toBeNull()

  fireEvent.blur(trigger)
  fireEvent.click(trigger)
  await screen.findByRole('dialog')
  fireEvent.pointerDown(document.body)
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

test('keeps the popover inside a narrow viewport and flips above its trigger', async () => {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 320 })
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: 240 })

  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.getAttribute('role') === 'dialog') {
      return DOMRect.fromRect({ x: 0, y: 0, width: 296, height: 180 })
    }
    if (this.getAttribute('aria-label') === 'Help for Matching static evidence') {
      return DOMRect.fromRect({ x: 300, y: 210, width: 22, height: 22 })
    }
    return DOMRect.fromRect()
  })

  help()
  fireEvent.click(screen.getByRole('button', { name: 'Help for Matching static evidence' }))

  const dialog = await screen.findByRole('dialog')
  expect(window.getComputedStyle(dialog).left).toBe('12px')
  expect(window.getComputedStyle(dialog).top).toBe('22px')
})

test('capability state adds only the canonical unavailable explanation', async () => {
  help(new Set())
  const trigger = screen.getByRole('button', { name: 'Help for Matching static evidence' })
  fireEvent.click(trigger)
  expect((await screen.findByRole('status')).textContent)
    .toBe(matchingTerm?.availability.unavailableHelp)

  cleanup()
  help(new Set(['contract-impact-report']))
  fireEvent.click(screen.getByRole('button', { name: 'Help for Matching static evidence' }))
  await screen.findByRole('dialog')
  expect(screen.queryByRole('status')).toBeNull()
})

test('reduced-motion preference disables popover transition timing', async () => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({
      matches: true,
      media: '(prefers-reduced-motion: reduce)',
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => true,
    }),
  })
  help()
  fireEvent.click(screen.getByRole('button', { name: 'Help for Matching static evidence' }))
  expect((await screen.findByRole('dialog')).getAttribute('data-reduced-motion')).toBe('true')
})
