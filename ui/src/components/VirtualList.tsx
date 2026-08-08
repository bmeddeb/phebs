import { forwardRef, useImperativeHandle, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { useStyletron } from 'baseui'
import { focusRing, usePhebsTokens } from '../theme'

// T43.11: windowed list for ten-thousand-row catalogs. The window is computed
// from a declared viewport height and a fixed per-row height (density-derived
// by the caller) — never measured — so rendering is deterministic in jsdom,
// receipts, and the browser alike. Only the visible slice plus overscan is in
// the DOM; aria-setsize/posinset keep assistive counts exact under windowing.
//
// The caller renders each row element itself (merging the provided RowProps),
// so rows stay real links where they are links today. Keyboard interaction
// lives on the listbox container: arrows, Home/End, PageUp/PageDown move the
// active row (skipping presentation headers), Enter commits it.

export interface VirtualRowProps {
  id: string
  role: 'option' | 'presentation'
  'aria-selected'?: boolean
  'aria-setsize'?: number
  'aria-posinset'?: number
  style: CSSProperties
}

export interface VirtualListHandle {
  scrollToIndex: (index: number) => void
  focus: () => void
}

interface VirtualListProps<T> {
  items: T[]
  rowHeight: number
  height: number
  overscan?: number
  ariaLabel: string
  listboxId: string
  activeIndex: number
  onActiveChange: (index: number) => void
  onCommit: (item: T, index: number) => void
  getKey: (item: T, index: number) => string
  // Presentation rows (e.g. group headers): rendered, never focusable,
  // skipped by keyboard navigation.
  isHeader?: (item: T) => boolean
  renderRow: (item: T, index: number, rowProps: VirtualRowProps, active: boolean) => ReactNode
}

function rowId(listboxId: string, index: number): string {
  return `${listboxId}-row-${index}`
}

export const VirtualList = forwardRef(function VirtualList<T>(
  { items, rowHeight, height, overscan = 6, ariaLabel, listboxId, activeIndex, onActiveChange, onCommit, getKey, isHeader, renderRow }: VirtualListProps<T>,
  ref: React.Ref<VirtualListHandle>,
) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [scrollTop, setScrollTop] = useState(0)
  const viewport = useRef<HTMLDivElement>(null)

  const total = items.length
  const first = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan)
  const last = Math.min(total - 1, Math.ceil((scrollTop + height) / rowHeight) + overscan)

  const scrollTo = (top: number) => {
    const bounded = Math.max(0, Math.min(top, total * rowHeight - height))
    if (viewport.current) viewport.current.scrollTop = bounded
    setScrollTop(bounded)
  }

  const reveal = (index: number) => {
    const rowTop = index * rowHeight
    if (rowTop < scrollTop) scrollTo(rowTop)
    else if (rowTop + rowHeight > scrollTop + height) scrollTo(rowTop + rowHeight - height)
  }

  useImperativeHandle(ref, () => ({
    scrollToIndex: (index: number) => {
      const bounded = Math.max(0, Math.min(index, total - 1))
      reveal(bounded)
      onActiveChange(bounded)
    },
    focus: () => viewport.current?.focus(),
  }))

  const isNavigable = (index: number) => index >= 0 && index < total && !(isHeader?.(items[index]) ?? false)

  // Nearest navigable row to `target`, searching `direction` first, then the
  // other way — so a page jump landing on a header settles on a real row.
  const nearestNavigable = (target: number, direction: 1 | -1): number => {
    const bounded = Math.max(0, Math.min(target, total - 1))
    for (let index = bounded; index >= 0 && index < total; index += direction) {
      if (isNavigable(index)) return index
    }
    for (let index = bounded; index >= 0 && index < total; index -= direction) {
      if (isNavigable(index)) return index
    }
    return activeIndex
  }

  const move = (index: number) => {
    if (index === activeIndex || !isNavigable(index)) return
    reveal(index)
    onActiveChange(index)
  }

  const onKeyDown = (event: React.KeyboardEvent) => {
    const pageRows = Math.max(1, Math.floor(height / rowHeight))
    if (event.key === 'ArrowDown') move(nearestNavigable(activeIndex + 1, 1))
    else if (event.key === 'ArrowUp') move(nearestNavigable(activeIndex - 1, -1))
    else if (event.key === 'Home') move(nearestNavigable(0, 1))
    else if (event.key === 'End') move(nearestNavigable(total - 1, -1))
    else if (event.key === 'PageDown') move(nearestNavigable(activeIndex + pageRows, 1))
    else if (event.key === 'PageUp') move(nearestNavigable(activeIndex - pageRows, -1))
    else if (event.key === 'Enter' && isNavigable(activeIndex)) onCommit(items[activeIndex], activeIndex)
    else return
    event.preventDefault()
  }

  const window: ReactNode[] = []
  for (let index = first; index <= last; index++) {
    const item = items[index]
    const header = isHeader?.(item) ?? false
    const style: CSSProperties = { position: 'absolute', top: index * rowHeight, left: 0, right: 0, height: rowHeight, boxSizing: 'border-box' }
    const rowProps: VirtualRowProps = header
      ? { id: rowId(listboxId, index), role: 'presentation', style }
      : {
        id: rowId(listboxId, index),
        role: 'option',
        'aria-selected': index === activeIndex,
        'aria-setsize': total,
        'aria-posinset': index + 1,
        style,
      }
    window.push(<div key={getKey(item, index)} style={{ display: 'contents' }}>{renderRow(item, index, rowProps, index === activeIndex)}</div>)
  }

  return (
    <div
      ref={viewport}
      role="listbox"
      id={listboxId}
      tabIndex={0}
      aria-label={ariaLabel}
      aria-activedescendant={isNavigable(activeIndex) ? rowId(listboxId, activeIndex) : undefined}
      onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
      onKeyDown={onKeyDown}
      className={css({ position: 'relative', height: `${height}px`, overflowY: 'auto', outline: 'none', ':focus-visible': focusRing(tok) })}
    >
      <div style={{ position: 'relative', height: total * rowHeight }}>{window}</div>
    </div>
  )
}) as <T>(props: VirtualListProps<T> & { ref?: React.Ref<VirtualListHandle> }) => ReactNode
