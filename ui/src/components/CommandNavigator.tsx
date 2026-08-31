import { useEffect, useMemo, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { href } from '../router'
import { FONTS, usePhebsTokens } from '../theme'
import { recentScopes, scopeParams, workbenchScopeParams, type ActiveScope } from '../scope'

// T43.9 global keyboard navigator (charter §3 keyboard-first). A command
// palette over navigation only: its items are built from the instance's
// advertised capabilities, the active scope, and recently seen scope
// identities — it issues no reads beyond navigation, and authority is
// re-verified by the scope bar on arrival.

export interface NavigatorSurface {
  label: string
  path: string
  // 'unknown' = the capability read is loading or failed; absence is not
  // established, so the destination stays listed (its gate page explains).
  available: boolean | 'unknown'
}

interface NavigatorItem {
  id: string
  label: string
  detail?: string
  target: string
}

export function CommandNavigator({ surfaces, scope, principal, onClose }: {
  surfaces: NavigatorSurface[]
  scope: ActiveScope | null
  // Authenticated principal keying the recent-scope store.
  principal: string
  onClose: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const openerRef = useRef<Element | null>(null)

  useEffect(() => {
    openerRef.current = document.activeElement
    inputRef.current?.focus()
    return () => {
      if (openerRef.current instanceof HTMLElement) openerRef.current.focus()
    }
  }, [])

  const items = useMemo<NavigatorItem[]>(() => {
    const result: NavigatorItem[] = []
    for (const surface of surfaces) {
      if (surface.available === false) continue
      result.push({
        id: `surface:${surface.path}`,
        label: surface.label,
        detail: surface.available === 'unknown' ? 'capability unknown' : undefined,
        target: href(surface.path),
      })
    }
    if (scope) {
      const name = scope.serviceKey || scope.repository
      result.push({ id: 'scope:directory', label: `Directory · ${name}`, detail: 'active scope', target: href('/services', scopeParams(scope)) })
      if (scope.serviceKey) {
        result.push({ id: 'scope:search', label: `Search · ${name}`, detail: 'active scope', target: href('/search', { ...scopeParams(scope), scope: 'service', q: '' }) })
        result.push({ id: 'scope:explorer', label: `Explorer · ${name}`, detail: 'active scope', target: href('/relationships', scopeParams(scope)) })
        result.push({ id: 'scope:workbench', label: `Workbench · ${name}`, detail: 'active scope', target: href('/workbench', workbenchScopeParams(scope)) })
      }
    }
    for (const recent of recentScopes(principal)) {
      if (scope && recent.repository === scope.repository && recent.serviceKey === scope.serviceKey) continue
      const name = recent.serviceKey || recent.repository
      result.push({
        id: `recent:${recent.repository}:${recent.serviceKey}`,
        label: `Directory · ${name}`,
        detail: 'recent scope',
        target: href('/services', scopeParams({ ...recent, serviceKey: recent.serviceKey, generation: recent.generation })),
      })
    }
    return result
  }, [surfaces, scope, principal])

  const matches = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return items
    return items.filter((item) =>
      item.label.toLowerCase().includes(needle) || item.detail?.toLowerCase().includes(needle))
  }, [items, query])

  const clamped = Math.min(active, Math.max(0, matches.length - 1))

  const go = (item: NavigatorItem | undefined) => {
    if (!item) return
    onClose()
    window.location.hash = item.target.slice(1)
  }

  return (
    <div
      role="presentation"
      onClick={onClose}
      className={css({ position: 'fixed', inset: 0, zIndex: 40, backgroundColor: 'rgba(0, 0, 0, 0.32)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', paddingTop: '12vh' })}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Go to surface or scope"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          // Modal contract: Escape closes from anywhere in the dialog, and
          // Tab cannot leave it — the input is the only tab stop.
          if (event.key === 'Escape') {
            event.preventDefault()
            onClose()
          } else if (event.key === 'Tab') {
            event.preventDefault()
            inputRef.current?.focus()
          }
        }}
        className={css({ width: 'min(560px, calc(100vw - 32px))', border: `1px solid ${tok.cardBorder}`, backgroundColor: tok.pageBg, boxShadow: tok.popoverShadow })}
      >
        <input
          ref={inputRef}
          role="combobox"
          aria-expanded="true"
          aria-controls="command-navigator-list"
          aria-activedescendant={matches[clamped] ? `command-navigator-item-${clamped}` : undefined}
          aria-label="Go to surface or scope"
          placeholder="Go to…"
          value={query}
          onChange={(event) => {
            setQuery(event.currentTarget.value)
            setActive(0)
          }}
          onKeyDown={(event) => {
            if (event.key === 'Escape') {
              event.preventDefault()
              onClose()
            } else if (event.key === 'ArrowDown') {
              event.preventDefault()
              setActive((current) => Math.min(current + 1, matches.length - 1))
            } else if (event.key === 'ArrowUp') {
              event.preventDefault()
              setActive((current) => Math.max(current - 1, 0))
            } else if (event.key === 'Enter') {
              event.preventDefault()
              go(matches[clamped])
            }
          }}
          className={css({ width: '100%', boxSizing: 'border-box', border: 0, borderBottom: `1px solid ${tok.cardBorder}`, padding: '13px 14px', backgroundColor: tok.pageBg, color: tok.textPrimary, fontFamily: FONTS.SANS, fontSize: '14px', lineHeight: '20px', ':focus': { outline: 'none' }, ':focus-visible': { outline: 'none' } })}
        />
        <ul
          id="command-navigator-list"
          role="listbox"
          aria-label="Destinations"
          className={css({ margin: 0, padding: '5px 0', listStyle: 'none', maxHeight: '46vh', overflowY: 'auto' })}
        >
          {matches.length === 0 && (
            <li className={css({ padding: '11px 14px', color: tok.textTertiary, fontSize: '12px' })}>
              No destination matches. Navigation only — nothing was searched.
            </li>
          )}
          {matches.map((item, index) => (
            <li
              key={item.id}
              id={`command-navigator-item-${index}`}
              role="option"
              aria-selected={index === clamped}
              onMouseEnter={() => setActive(index)}
              onClick={() => go(item)}
              className={css({
                display: 'flex',
                alignItems: 'baseline',
                gap: '10px',
                padding: '8px 14px',
                cursor: 'pointer',
                backgroundColor: index === clamped ? tok.selectedLineBg : 'transparent',
                borderLeft: `2px solid ${index === clamped ? tok.accent : 'transparent'}`,
              })}
            >
              <span className={css({ minWidth: 0, color: tok.textPrimary, fontSize: '13px', lineHeight: '19px', overflowWrap: 'anywhere' })}>{item.label}</span>
              {item.detail && <span className={css({ color: tok.textTertiary, fontSize: '11px', whiteSpace: 'nowrap' })}>{item.detail}</span>}
            </li>
          ))}
        </ul>
        <div role="status" className={css({ position: 'absolute', width: '1px', height: '1px', overflow: 'hidden', clipPath: 'inset(50%)' })}>
          {matches.length} destination{matches.length === 1 ? '' : 's'}
        </div>
      </div>
    </div>
  )
}
