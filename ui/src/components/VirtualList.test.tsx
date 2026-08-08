import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { BaseProvider } from 'baseui'
import { Provider as StyletronProvider } from 'styletron-react'
import { Client as Styletron } from 'styletron-engine-monolithic'
import { ModeContext, lightTheme } from '../theme'
import { VirtualList } from './VirtualList'

afterEach(cleanup)

const engine = new Styletron()

// T43.11 recorded interaction budget: every list interaction at 10,000
// synthetic rows — a filter keystroke, a jump to the far end — must complete
// its React render inside this bound. The budget holds because the window
// keeps the DOM constant (~30 rows); rendering all 10,000 rows would exceed
// it by orders of magnitude. Real 10,000-service data remains Epic 41's.
const INTERACTION_BUDGET_MS = 150
const TOTAL_ROWS = 10_000
const ROW_HEIGHT = 44
const HEIGHT = 440

interface SyntheticRow { id: string; label: string; header?: boolean }

function makeRows(count: number): SyntheticRow[] {
  return Array.from({ length: count }, (_, index) => ({ id: `row-${index}`, label: `service-${index}` }))
}

function Harness({ items, onCommit = () => {}, selectedIndex = null }: { items: SyntheticRow[]; onCommit?: (item: SyntheticRow) => void; selectedIndex?: number | null }) {
  const [active, setActive] = useState(0)
  return (
    <StyletronProvider value={engine}>
      <BaseProvider theme={lightTheme}>
        <ModeContext.Provider value={{ mode: 'light', toggle: () => {} }}>
          <VirtualList<SyntheticRow>
            items={items}
            rowHeight={ROW_HEIGHT}
            height={HEIGHT}
            ariaLabel="Synthetic rows"
            listboxId="synthetic"
            activeIndex={active}
            selectedIndex={selectedIndex}
            onActiveChange={setActive}
            onCommit={onCommit}
            getKey={(item) => item.id}
            isHeader={(item) => item.header ?? false}
            renderRow={(item, _index, rowProps, isActive) => item.header ? (
              <div {...rowProps} data-testid="header">{item.label}</div>
            ) : (
              <div {...rowProps} data-kind="row" data-active={isActive || undefined}>{item.label}</div>
            )}
          />
        </ModeContext.Provider>
      </BaseProvider>
    </StyletronProvider>
  )
}

describe('VirtualList at ten thousand rows', () => {
  it('renders only the window, not the collection', () => {
    render(<Harness items={makeRows(TOTAL_ROWS)} />)
    const listbox = screen.getByRole('listbox', { name: 'Synthetic rows' })
    const options = listbox.querySelectorAll('[role="option"]')
    // viewport rows (10) + overscan (6 both sides) + partial-row slack.
    expect(options.length).toBeLessThanOrEqual(24)
    expect(options.length).toBeGreaterThan(0)
  })

  it('reports exact assistive counts under windowing', () => {
    render(<Harness items={makeRows(TOTAL_ROWS)} />)
    const first = screen.getByText('service-0')
    expect(first.getAttribute('aria-setsize')).toBe(String(TOTAL_ROWS))
    expect(first.getAttribute('aria-posinset')).toBe('1')
  })

  it('holds a filter interaction under the recorded budget', () => {
    const rows = makeRows(TOTAL_ROWS)
    const { rerender } = render(<Harness items={rows} />)
    // Simulate an instant-narrow keystroke: recompute over all 10,000 rows,
    // re-render the window.
    const started = performance.now()
    const narrowed = rows.filter((row) => row.label.includes('service-99'))
    rerender(<Harness items={narrowed} />)
    const elapsed = performance.now() - started
    expect(narrowed.length).toBeGreaterThan(0)
    expect(elapsed).toBeLessThan(INTERACTION_BUDGET_MS)
  })

  it('holds an end-jump interaction under the recorded budget', () => {
    render(<Harness items={makeRows(TOTAL_ROWS)} />)
    const listbox = screen.getByRole('listbox', { name: 'Synthetic rows' })
    const started = performance.now()
    fireEvent.keyDown(listbox, { key: 'End' })
    const elapsed = performance.now() - started
    expect(screen.getByText(`service-${TOTAL_ROWS - 1}`)).toBeTruthy()
    expect(listbox.getAttribute('aria-activedescendant')).toBe(`synthetic-row-${TOTAL_ROWS - 1}`)
    expect(elapsed).toBeLessThan(INTERACTION_BUDGET_MS)
  })
})

describe('VirtualList keyboard contract', () => {
  it('moves with arrows and commits with Enter', () => {
    const onCommit = vi.fn()
    render(<Harness items={makeRows(50)} onCommit={onCommit} />)
    const listbox = screen.getByRole('listbox', { name: 'Synthetic rows' })
    fireEvent.keyDown(listbox, { key: 'ArrowDown' })
    fireEvent.keyDown(listbox, { key: 'ArrowDown' })
    expect(listbox.getAttribute('aria-activedescendant')).toBe('synthetic-row-2')
    fireEvent.keyDown(listbox, { key: 'Enter' })
    expect(onCommit).toHaveBeenCalledWith(expect.objectContaining({ id: 'row-2' }), 2)
  })

  it('pages by the viewport and clamps at the edges', () => {
    render(<Harness items={makeRows(50)} />)
    const listbox = screen.getByRole('listbox', { name: 'Synthetic rows' })
    fireEvent.keyDown(listbox, { key: 'PageDown' })
    expect(listbox.getAttribute('aria-activedescendant')).toBe('synthetic-row-10')
    fireEvent.keyDown(listbox, { key: 'PageUp' })
    fireEvent.keyDown(listbox, { key: 'PageUp' })
    expect(listbox.getAttribute('aria-activedescendant')).toBe('synthetic-row-0')
  })

  it('skips presentation headers in both directions', () => {
    const items: SyntheticRow[] = [
      { id: 'h1', label: 'Current', header: true },
      { id: 'a', label: 'service-a' },
      { id: 'h2', label: 'Stale', header: true },
      { id: 'b', label: 'service-b' },
    ]
    render(<Harness items={items} />)
    const listbox = screen.getByRole('listbox', { name: 'Synthetic rows' })
    // Active starts on index 0 (a header): first ArrowDown lands on the
    // first real row, the next skips the interleaved header.
    fireEvent.keyDown(listbox, { key: 'ArrowDown' })
    expect(listbox.getAttribute('aria-activedescendant')).toBe('synthetic-row-1')
    fireEvent.keyDown(listbox, { key: 'ArrowDown' })
    expect(listbox.getAttribute('aria-activedescendant')).toBe('synthetic-row-3')
    fireEvent.keyDown(listbox, { key: 'ArrowUp' })
    expect(listbox.getAttribute('aria-activedescendant')).toBe('synthetic-row-1')
    const headers = screen.getAllByTestId('header')
    expect(headers.length).toBe(2)
    expect(headers[0].getAttribute('role')).toBe('presentation')
  })

  it('reports the committed selection, not keyboard focus', () => {
    render(<Harness items={makeRows(50)} selectedIndex={3} />)
    const listbox = screen.getByRole('listbox', { name: 'Synthetic rows' })
    fireEvent.keyDown(listbox, { key: 'ArrowDown' })
    // Focus moved to row 1; selection stays reported on row 3.
    expect(listbox.getAttribute('aria-activedescendant')).toBe('synthetic-row-1')
    expect(screen.getByText('service-1').getAttribute('aria-selected')).toBe('false')
    expect(screen.getByText('service-3').getAttribute('aria-selected')).toBe('true')
  })

  it('keeps the active descendant mounted when pointer scrolling moves the window', () => {
    render(<Harness items={makeRows(TOTAL_ROWS)} />)
    const listbox = screen.getByRole('listbox', { name: 'Synthetic rows' })
    fireEvent.keyDown(listbox, { key: 'End' })
    expect(listbox.getAttribute('aria-activedescendant')).toBe(`synthetic-row-${TOTAL_ROWS - 1}`)
    // Simulate a mouse scroll back to the top: the window moves, but the
    // active option must remain in the DOM — the reference never dangles.
    listbox.scrollTop = 0
    fireEvent.scroll(listbox)
    expect(screen.getByText('service-0')).toBeTruthy()
    expect(document.getElementById(`synthetic-row-${TOTAL_ROWS - 1}`)).toBeTruthy()
    // And it stays a single extra row, not a window extension.
    expect(listbox.querySelectorAll('[role="option"]').length).toBeLessThanOrEqual(25)
  })

  it('excludes presentation headers from ARIA positions', () => {
    const items: SyntheticRow[] = [
      { id: 'h1', label: 'Current', header: true },
      { id: 'a', label: 'service-a' },
      { id: 'h2', label: 'Stale', header: true },
      { id: 'b', label: 'service-b' },
      { id: 'c', label: 'service-c' },
    ]
    render(<Harness items={items} />)
    expect(screen.getByText('service-a').getAttribute('aria-posinset')).toBe('1')
    expect(screen.getByText('service-b').getAttribute('aria-posinset')).toBe('2')
    expect(screen.getByText('service-c').getAttribute('aria-posinset')).toBe('3')
    for (const label of ['service-a', 'service-b', 'service-c']) {
      expect(screen.getByText(label).getAttribute('aria-setsize')).toBe('3')
    }
  })

  it('Home and End land on navigable rows, not headers', () => {
    const items: SyntheticRow[] = [
      { id: 'h1', label: 'Current', header: true },
      ...makeRows(30),
    ]
    render(<Harness items={items} />)
    const listbox = screen.getByRole('listbox', { name: 'Synthetic rows' })
    fireEvent.keyDown(listbox, { key: 'End' })
    expect(listbox.getAttribute('aria-activedescendant')).toBe(`synthetic-row-${items.length - 1}`)
    fireEvent.keyDown(listbox, { key: 'Home' })
    expect(listbox.getAttribute('aria-activedescendant')).toBe('synthetic-row-1')
  })
})
