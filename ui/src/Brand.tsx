import { useEffect, useRef } from 'react'
import { useStyletron } from 'baseui'
import { PhebsMark } from './icons'
import { FONTS, usePhebsTokens } from './theme'

interface BrandLockupProps {
  href?: string
  markSize?: number
  wordmarkSize?: number
  gap?: number
  className?: string
  animate?: boolean
}

const reducedMotion = () =>
  window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true

export function BrandLockup({
  href,
  markSize = 18,
  wordmarkSize = 17,
  gap = 8,
  className,
  animate = true,
}: BrandLockupProps) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const dotRef = useRef<SVGCircleElement>(null)
  const pulseRef = useRef<SVGCircleElement>(null)

  const dock = () => {
    if (!animate || reducedMotion()) return
    const dot = dotRef.current
    const pulse = pulseRef.current
    if (typeof dot?.animate === 'function') {
      dot.getAnimations?.().forEach((animation) => animation.cancel())
      dot.animate(
        [
          { transform: 'translateX(11px)', opacity: 0 },
          { transform: 'translateX(-0.8px)', opacity: 1, offset: 0.72 },
          { transform: 'translateX(0)', opacity: 1 },
        ],
        { duration: 550, easing: 'cubic-bezier(0.22,1,0.36,1)' },
      )
    }
    if (typeof pulse?.animate === 'function') {
      pulse.getAnimations?.().forEach((animation) => animation.cancel())
      pulse.animate(
        [
          { transform: 'scale(0.25)', opacity: 0.45 },
          { transform: 'scale(1)', opacity: 0 },
        ],
        { duration: 450, delay: 400, easing: 'ease-out', fill: 'backwards' },
      )
    }
  }

  const lockupClass = `${className ?? ''} ${css({
    display: 'inline-flex',
    alignItems: 'center',
    gap: `${gap}px`,
    flexShrink: 0,
    color: tok.textPrimary,
    textDecoration: 'none',
    letterSpacing: '0',
    ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '3px', borderRadius: '3px' },
  })}`.trim()
  const content = (
    <>
      <PhebsMark size={markSize} dotRef={dotRef} pulseRef={pulseRef} />
      <span className={css({ fontFamily: FONTS.SANS, fontSize: `${wordmarkSize}px`, lineHeight: 1, fontWeight: 700 })}>
        phebs
      </span>
    </>
  )

  if (href) {
    return <a href={href} className={lockupClass} onMouseEnter={dock}>{content}</a>
  }
  return <div className={lockupClass} onMouseEnter={dock}>{content}</div>
}

export function BrandLoader() {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const rootRef = useRef<HTMLDivElement>(null)
  const portRef = useRef<SVGPathElement>(null)
  const approachRef = useRef<SVGPathElement>(null)
  const dotRef = useRef<SVGCircleElement>(null)
  const pulseRef = useRef<SVGCircleElement>(null)
  const wordRef = useRef<SVGTextElement>(null)

  useEffect(() => {
    if (reducedMotion()) return
    const options: KeyframeAnimationOptions = { duration: 4600, iterations: Infinity }
    const animations = [
      rootRef.current?.animate?.(
        [{ opacity: 1, offset: 0 }, { opacity: 1, offset: 0.913 }, { opacity: 0, offset: 0.989 }, { opacity: 0, offset: 1 }],
        options,
      ),
      portRef.current?.animate?.(
        [{ strokeDashoffset: 100, offset: 0 }, { strokeDashoffset: 0, offset: 0.196 }, { strokeDashoffset: 0, offset: 1 }],
        { ...options, easing: 'cubic-bezier(0.65,0,0.35,1)' },
      ),
      approachRef.current?.animate?.(
        [{ opacity: 0, offset: 0 }, { opacity: 0, offset: 0.163 }, { opacity: 1, offset: 0.228 }, { opacity: 1, offset: 0.435 }, { opacity: 0, offset: 0.5 }, { opacity: 0, offset: 1 }],
        options,
      ),
      dotRef.current?.animate?.(
        [
          { transform: 'translateX(11px) scale(1)', opacity: 0, offset: 0 },
          { transform: 'translateX(11px) scale(1)', opacity: 0, offset: 0.239 },
          { transform: 'translateX(8.5px) scale(1)', opacity: 1, offset: 0.283 },
          { transform: 'translateX(0) scale(1)', opacity: 1, offset: 0.424 },
          { transform: 'translateX(0) scale(1.25)', opacity: 1, offset: 0.467 },
          { transform: 'translateX(0) scale(1)', opacity: 1, offset: 0.511 },
          { transform: 'translateX(0) scale(1)', opacity: 1, offset: 1 },
        ],
        { ...options, easing: 'cubic-bezier(0.22,1,0.36,1)' },
      ),
      pulseRef.current?.animate?.(
        [{ transform: 'scale(0.293)', opacity: 0, offset: 0 }, { transform: 'scale(0.293)', opacity: 0, offset: 0.467 }, { transform: 'scale(0.293)', opacity: 0.4, offset: 0.5 }, { transform: 'scale(1)', opacity: 0, offset: 0.63 }, { transform: 'scale(1)', opacity: 0, offset: 1 }],
        options,
      ),
      wordRef.current?.animate?.(
        [{ transform: 'translateY(1.5px)', opacity: 0, offset: 0 }, { transform: 'translateY(1.5px)', opacity: 0, offset: 0.565 }, { transform: 'translateY(0)', opacity: 1, offset: 0.685 }, { transform: 'translateY(0)', opacity: 1, offset: 1 }],
        options,
      ),
    ].filter((animation): animation is Animation => animation !== undefined)

    return () => animations.forEach((animation) => animation.cancel())
  }, [])

  return (
    <div ref={rootRef} role="status" aria-label="Loading phebs" className={css({ color: tok.textPrimary })}>
      <svg width="132" height="78" viewBox="0 0 48 36" fill="none" aria-hidden>
        <g transform="translate(10.75 1)">
          <path
            ref={portRef}
            d="M21 8.5 V7.5 A4.5 4.5 0 0 0 16.5 3 H7.5 A4.5 4.5 0 0 0 3 7.5 V16.5 A4.5 4.5 0 0 0 7.5 21 H16.5 A4.5 4.5 0 0 0 21 16.5 V15.5"
            pathLength="100"
            strokeDasharray="100"
            strokeDashoffset="0"
            stroke="currentColor"
            strokeWidth="1.8"
            strokeLinecap="round"
          />
          <path ref={approachRef} d="M26 12 H18.5" stroke={tok.gutter} strokeWidth="0.9" strokeDasharray="1.8 1.6" />
          <circle ref={pulseRef} cx="15.5" cy="12" r="7.5" stroke={tok.accent} strokeWidth="0.5" opacity="0" style={{ transformBox: 'fill-box', transformOrigin: 'center' }} />
          <circle ref={dotRef} cx="15.5" cy="12" r="1.9" fill={tok.accent} style={{ transformBox: 'fill-box', transformOrigin: 'center' }} />
        </g>
        <text ref={wordRef} x="24" y="31.5" textAnchor="middle" fontFamily={FONTS.SANS} fontSize="7" fontWeight="700" fill="currentColor">
          phebs
        </text>
      </svg>
    </div>
  )
}
