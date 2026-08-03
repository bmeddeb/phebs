import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react'
import { createPortal } from 'react-dom'
import { useStyletron } from 'baseui'
import {
  glossaryTerms,
  type GlossaryTerm,
  type GlossaryTermId,
} from '../glossary.generated'
import { usePhebsTokens } from '../theme'

const termById = new Map<GlossaryTermId, GlossaryTerm>(
  glossaryTerms.map((term) => [term.id, term]),
)

const viewportGutter = 12
const popoverGap = 8
const popoverWidth = 360
const hoverBridgeMilliseconds = 120

export interface SectionHelpProps {
  termId: GlossaryTermId
  enabledCapabilities: ReadonlySet<string>
  triggerLabel?: string
}

interface Position {
  left: number
  top: number
}

export function SectionHelp({
  termId,
  enabledCapabilities,
  triggerLabel,
}: SectionHelpProps) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [hovered, setHovered] = useState(false)
  const [focused, setFocused] = useState(false)
  const [pinned, setPinned] = useState(false)
  const [dismissed, setDismissed] = useState(false)
  const [position, setPosition] = useState<Position | null>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const dialogRef = useRef<HTMLDivElement>(null)
  const hoverCloseTimer = useRef<number | null>(null)
  const suppressFocusOpen = useRef(false)
  const reactId = useId()
  const id = `section-help-${reactId.replaceAll(':', '')}`
  const shortHelpId = `${id}-short`
  const term = termById.get(termId)

  const isOpen = Boolean(term) && !dismissed && (hovered || focused || pinned)
  const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true

  const cancelHoverClose = () => {
    if (hoverCloseTimer.current !== null) {
      window.clearTimeout(hoverCloseTimer.current)
      hoverCloseTimer.current = null
    }
  }

  const openFromHover = () => {
    cancelHoverClose()
    setHovered(true)
    setDismissed(false)
  }

  const scheduleHoverClose = () => {
    cancelHoverClose()
    hoverCloseTimer.current = window.setTimeout(() => {
      hoverCloseTimer.current = null
      setHovered(false)
    }, hoverBridgeMilliseconds)
  }

  const dismiss = (returnFocus: boolean) => {
    cancelHoverClose()
    setHovered(false)
    setFocused(false)
    setPinned(false)
    setDismissed(true)
    if (returnFocus) {
      suppressFocusOpen.current = true
      window.setTimeout(() => {
        triggerRef.current?.focus()
        suppressFocusOpen.current = false
      }, 0)
    }
  }

  const updatePosition = () => {
    const trigger = triggerRef.current
    const dialog = dialogRef.current
    if (!trigger || !dialog) return
    const triggerRect = trigger.getBoundingClientRect()
    const dialogRect = dialog.getBoundingClientRect()
    const width = Math.min(popoverWidth, Math.max(0, window.innerWidth - viewportGutter * 2))
    const height = Math.min(dialogRect.height, Math.max(0, window.innerHeight - viewportGutter * 2))
    const maximumLeft = Math.max(viewportGutter, window.innerWidth - width - viewportGutter)
    const left = Math.min(Math.max(triggerRect.left, viewportGutter), maximumLeft)
    const below = triggerRect.bottom + popoverGap
    const above = triggerRect.top - height - popoverGap
    const top = below + height <= window.innerHeight - viewportGutter || above < viewportGutter
      ? Math.min(below, Math.max(viewportGutter, window.innerHeight - height - viewportGutter))
      : above
    setPosition({ left, top })
  }

  useLayoutEffect(() => {
    if (!isOpen) {
      setPosition(null)
      return
    }
    updatePosition()
  }, [isOpen])

  useEffect(() => {
    return () => cancelHoverClose()
  }, [])

  useEffect(() => {
    if (!isOpen) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        dismiss(true)
      }
    }
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target
      if (!(target instanceof Node) ||
        triggerRef.current?.contains(target) ||
        dialogRef.current?.contains(target)) {
        return
      }
      dismiss(false)
    }
    const onViewportChange = () => updatePosition()
    document.addEventListener('keydown', onKeyDown)
    document.addEventListener('pointerdown', onPointerDown)
    window.addEventListener('resize', onViewportChange)
    window.addEventListener('scroll', onViewportChange, true)
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.removeEventListener('pointerdown', onPointerDown)
      window.removeEventListener('resize', onViewportChange)
      window.removeEventListener('scroll', onViewportChange, true)
    }
  }, [isOpen])

  if (!term) return null

  const available = isAvailable(term, enabledCapabilities)
  const popover = isOpen ? createPortal(
    <div
      ref={dialogRef}
      id={id}
      role="dialog"
      aria-label={`${triggerLabel ?? term.label} help`}
      data-reduced-motion={reducedMotion ? 'true' : 'false'}
      onMouseEnter={openFromHover}
      onMouseLeave={scheduleHoverClose}
      className={css({
        position: 'fixed',
        boxSizing: 'border-box',
        zIndex: 40,
        left: position ? `${position.left}px` : `${viewportGutter}px`,
        top: position ? `${position.top}px` : `${viewportGutter}px`,
        width: `${popoverWidth}px`,
        maxWidth: `calc(100vw - ${viewportGutter * 2}px)`,
        maxHeight: `calc(100vh - ${viewportGutter * 2}px)`,
        overflowY: 'auto',
        padding: '14px',
        border: `1px solid ${tok.cardBorder}`,
        borderRadius: '8px',
        backgroundColor: tok.pageBg,
        boxShadow: '0 12px 32px rgba(0, 0, 0, 0.24)',
        color: tok.textSecondary,
        fontSize: '12px',
        lineHeight: '18px',
        opacity: position ? 1 : 0,
        transform: position ? 'translateY(0)' : 'translateY(-4px)',
        transitionProperty: reducedMotion ? 'none' : 'opacity, transform',
        transitionDuration: reducedMotion ? '0ms' : '100ms',
        '@media (prefers-reduced-motion: reduce)': {
          transitionProperty: 'none',
          transitionDuration: '0ms',
        },
      })}
    >
      <div className={css({
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'space-between',
        gap: '12px',
        marginBottom: '8px',
      })}>
        <div className={css({
          color: tok.textPrimary,
          fontSize: '13px',
          lineHeight: '18px',
          fontWeight: 650,
        })}>
          {term.label}
        </div>
        <button
          type="button"
          aria-label={`Close ${term.label} help`}
          onClick={() => dismiss(true)}
          className={css({
            width: '24px',
            height: '24px',
            flexShrink: 0,
            margin: '-4px -4px 0 0',
            padding: 0,
            border: 'none',
            borderRadius: '5px',
            backgroundColor: 'transparent',
            color: tok.textTertiary,
            cursor: 'pointer',
            fontSize: '17px',
            lineHeight: '24px',
            ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary },
            ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
          })}
        >
          <span aria-hidden="true">×</span>
        </button>
      </div>
      <p className={css({ margin: '0 0 9px', color: tok.textPrimary })}>
        {term.shortHelp}
      </p>
      <p className={css({ margin: '0 0 10px' })}>{term.expandedHelp}</p>
      <Boundary label="Evidence boundary">{term.evidenceBoundary}</Boundary>
      <Boundary label="Authority boundary">{term.authorityBoundary}</Boundary>
      {!available ? (
        <div
          role="status"
          className={css({
            marginTop: '10px',
            paddingTop: '9px',
            borderTop: `1px solid ${tok.innerSep}`,
            color: tok.statusAmber,
          })}
        >
          {term.availability.unavailableHelp}
        </div>
      ) : null}
    </div>,
    document.body,
  ) : null

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        aria-label={`Help for ${triggerLabel ?? term.label}`}
        aria-describedby={shortHelpId}
        aria-haspopup="dialog"
        aria-expanded={isOpen}
        aria-controls={isOpen ? id : undefined}
        onBlur={() => setFocused(false)}
        onClick={(event) => {
          event.stopPropagation()
          setPinned(true)
          setDismissed(false)
        }}
        onFocus={() => {
          if (suppressFocusOpen.current) {
            suppressFocusOpen.current = false
            return
          }
          setFocused(true)
          setDismissed(false)
        }}
        onMouseEnter={openFromHover}
        onMouseLeave={scheduleHoverClose}
        className={css({
          width: '22px',
          height: '22px',
          flexShrink: 0,
          padding: 0,
          display: 'inline-grid',
          placeItems: 'center',
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: '999px',
          backgroundColor: 'transparent',
          color: tok.textTertiary,
          cursor: 'help',
          fontSize: '12px',
          lineHeight: 1,
          fontWeight: 700,
          ':hover': { borderColor: tok.accent, color: tok.accent, backgroundColor: tok.hoverFill },
          ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' },
        })}
      >
        <span aria-hidden="true">?</span>
      </button>
      <span
        id={shortHelpId}
        className={css({
          position: 'absolute',
          width: '1px',
          height: '1px',
          padding: 0,
          margin: '-1px',
          overflow: 'hidden',
          clip: 'rect(0, 0, 0, 0)',
          whiteSpace: 'nowrap',
          border: 0,
        })}
      >
        {term.shortHelp}
      </span>
      {popover}
    </>
  )
}

function Boundary({ label, children }: { label: string; children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <p className={css({ margin: '7px 0 0', color: tok.textTertiary })}>
      <strong className={css({ color: tok.textSecondary, fontWeight: 650 })}>{label}:</strong>{' '}
      {children}
    </p>
  )
}

function isAvailable(term: GlossaryTerm, enabledCapabilities: ReadonlySet<string>): boolean {
  if (term.availability.requiresAll.some((capability) => !enabledCapabilities.has(capability))) {
    return false
  }
  return term.availability.requiresAny.length === 0 ||
    term.availability.requiresAny.some((capability) => enabledCapabilities.has(capability))
}
