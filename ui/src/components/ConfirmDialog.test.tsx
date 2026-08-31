import { useState } from 'react'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { BaseProvider } from 'baseui'
import { Client as Styletron } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { ModeContext, lightTheme } from '../theme'
import { ConfirmDialog } from './ConfirmDialog'

const engine = new Styletron()

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function mount(node: React.ReactNode) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={lightTheme}>
        <ModeContext.Provider value={{ mode: 'light', toggle: () => {} }}>
          {node}
        </ModeContext.Provider>
      </BaseProvider>
    </StyletronProvider>,
  )
}

function dialog(
  overrides: Partial<React.ComponentProps<typeof ConfirmDialog>> = {},
) {
  const props = {
    isOpen: true,
    title: 'Discard unsaved edits?',
    detail: 'Leaving discards the unsaved Why and What fields.',
    cancelLabel: 'Keep editing',
    confirmLabel: 'Discard edits and leave',
    onCancel: vi.fn(),
    onConfirm: vi.fn(),
    ...overrides,
  }
  mount(<ConfirmDialog {...props} />)
  return props
}

describe('ConfirmDialog', () => {
  it('is an alert dialog named and described by its visible content', () => {
    dialog()
    const alert = screen.getByRole('alertdialog', {
      name: 'Discard unsaved edits?',
      description: 'Leaving discards the unsaved Why and What fields.',
    })
    const title = screen.getByRole('heading', {
      name: 'Discard unsaved edits?',
    })
    expect(alert.getAttribute('aria-labelledby')).toBe(title.id)
    expect(alert.getAttribute('aria-describedby')).toBe(
      screen.getByText('Leaving discards the unsaved Why and What fields.').id,
    )
    expect(alert.getAttribute('aria-label')).toBeNull()
    expect(alert.getAttribute('aria-modal')).toBe('true')
  })

  it('puts the safe action first and gives it initial focus', async () => {
    dialog()
    const alert = screen.getByRole('alertdialog')
    const buttons = within(alert).getAllByRole('button')
    expect(buttons.map((button) => button.textContent)).toEqual([
      'Keep editing',
      'Discard edits and leave',
      '',
    ])
    expect(buttons[2].getAttribute('aria-label')).toBe('Close')
    await waitFor(() => expect(document.activeElement).toBe(buttons[0]))
  })

  it('confirms only through the explicit confirm action', () => {
    const props = dialog()
    fireEvent.click(screen.getByRole('button', { name: 'Discard edits and leave' }))
    expect(props.onConfirm).toHaveBeenCalledTimes(1)
    expect(props.onCancel).not.toHaveBeenCalled()
  })

  it('routes Escape, backdrop, and close-button dismissal through cancel', () => {
    const escape = dialog()
    fireEvent.keyUp(document.body, { key: 'Escape' })
    expect(escape.onCancel).toHaveBeenCalledTimes(1)
    expect(escape.onConfirm).not.toHaveBeenCalled()

    cleanup()
    const backdrop = dialog()
    const alert = screen.getByRole('alertdialog')
    fireEvent.mouseDown(alert.parentElement!)
    expect(backdrop.onCancel).toHaveBeenCalledTimes(1)
    expect(backdrop.onConfirm).not.toHaveBeenCalled()

    cleanup()
    const close = dialog()
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(close.onCancel).toHaveBeenCalledTimes(1)
    expect(close.onConfirm).not.toHaveBeenCalled()
  })

  it('contains focus while open and returns it to the trigger after cancel', async () => {
    const onCancel = vi.fn()

    function Harness() {
      const [isOpen, setOpen] = useState(false)
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>Review leave</button>
          <button type="button">Outside action</button>
          <ConfirmDialog
            isOpen={isOpen}
            title="Discard unsaved edits?"
            detail="Leaving discards the current draft."
            cancelLabel="Keep editing"
            confirmLabel="Discard edits and leave"
            onCancel={() => {
              onCancel()
              setOpen(false)
            }}
            onConfirm={() => {}}
          />
        </>
      )
    }

    mount(<Harness />)
    const trigger = screen.getByRole('button', { name: 'Review leave' })
    trigger.focus()
    fireEvent.click(trigger)
    const cancel = await screen.findByRole('button', { name: 'Keep editing' })
    await waitFor(() => expect(document.activeElement).toBe(cancel))

    const outside = screen.getByRole('button', { name: 'Outside action' })
    outside.focus()
    await waitFor(() => expect(document.activeElement).toBe(cancel))

    fireEvent.click(cancel)
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('alertdialog')).toBeNull()
    await waitFor(() => expect(document.activeElement).toBe(trigger), {
      timeout: 1_000,
    })
  })

  it('bounds the panel and scrollable detail for narrow viewports', () => {
    dialog({
      detail: 'A very long detail '.repeat(100),
      cancelLabel: 'Keep editing on this investigation',
      confirmLabel: 'Discard every unsaved edit and leave',
    })
    const alert = screen.getByRole('alertdialog')
    const styles = window.getComputedStyle(alert)
    expect(styles.width).toBe('440px')
    expect(styles.maxWidth).toBe('calc(100vw - 24px)')
    expect(styles.maxHeight).toBe('calc(100vh - 24px)')
    expect(window.getComputedStyle(screen.getByText(/A very long detail/)).overflowY)
      .toBe('auto')
    for (const action of within(alert).getAllByRole('button').slice(0, 2)) {
      expect(window.getComputedStyle(action).whiteSpace).toBe('normal')
    }
  })
})
