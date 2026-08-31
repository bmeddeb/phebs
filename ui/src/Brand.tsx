import { useStyletron } from 'baseui'
import { PhebsMark } from './icons'
import { FONTS, MOTION, animated, usePhebsTokens } from './theme'

interface BrandLockupProps {
  href?: string
  markSize?: number
  wordmarkSize?: number
  gap?: number
  className?: string
}

// T43.12f: the lockup is static. The former hover re-dock was decorative
// motion on a navigation element — not a state transition — and ran
// through WAAPI outside the motion contract; charter §3 removes it.
export function BrandLockup({
  href,
  markSize = 18,
  wordmarkSize = 17,
  gap = 8,
  className,
}: BrandLockupProps) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const safeWordmarkSize = Math.max(11, wordmarkSize)
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
      <PhebsMark size={markSize} />
      <span className={css({ fontFamily: FONTS.SANS, fontSize: `${safeWordmarkSize}px`, lineHeight: 1, fontWeight: 700 })}>
        phebs
      </span>
    </>
  )

  if (href) {
    return <a href={href} className={lockupClass}>{content}</a>
  }
  return <div className={lockupClass}>{content}</div>
}

// The loading choreography: the port appears, the approach dashes light,
// the dot docks and pulses, the wordmark settles, and the loop breathes
// out. State indication (authentication is loading), composed entirely
// through the motion helper — compositor properties only (the former
// stroke draw is now an opacity reveal), the loader cadence token, and
// the reduced-motion path by construction: with animations removed every
// element's attribute state is the complete settled logo.
export function BrandLoader() {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const fillBox = { transformBox: 'fill-box' as const, transformOrigin: 'center' }
  const loop = (frames: Record<string, { opacity?: number; transform?: string }>, easing: (typeof MOTION)['easeOut' | 'ease' | 'linear'] = MOTION.linear) =>
    css({ ...animated(frames, { duration: MOTION.loader, easing, loop: true }) })
  return (
    <div
      role="status"
      aria-label="Loading phebs"
      className={`${css({ color: tok.textPrimary })} ${loop({ '0%': { opacity: 1 }, '91.3%': { opacity: 1 }, '98.9%': { opacity: 0 }, '100%': { opacity: 0 } })}`}
    >
      <svg width="132" height="78" viewBox="0 0 48 36" fill="none" aria-hidden>
        <g transform="translate(10.75 1)">
          <path
            className={loop({ '0%': { opacity: 0 }, '19.6%': { opacity: 1 }, '100%': { opacity: 1 } }, MOTION.ease)}
            d="M21 8.5 V7.5 A4.5 4.5 0 0 0 16.5 3 H7.5 A4.5 4.5 0 0 0 3 7.5 V16.5 A4.5 4.5 0 0 0 7.5 21 H16.5 A4.5 4.5 0 0 0 21 16.5 V15.5"
            stroke="currentColor"
            strokeWidth="1.8"
            strokeLinecap="round"
          />
          <path
            className={loop({ '0%': { opacity: 0 }, '16.3%': { opacity: 0 }, '22.8%': { opacity: 1 }, '43.5%': { opacity: 1 }, '50%': { opacity: 0 }, '100%': { opacity: 0 } })}
            d="M26 12 H18.5"
            stroke={tok.gutter}
            strokeWidth="0.9"
            strokeDasharray="1.8 1.6"
          />
          <circle
            className={loop({ '0%': { transform: 'scale(0.293)', opacity: 0 }, '46.7%': { transform: 'scale(0.293)', opacity: 0 }, '50%': { transform: 'scale(0.293)', opacity: 0.4 }, '63%': { transform: 'scale(1)', opacity: 0 }, '100%': { transform: 'scale(1)', opacity: 0 } })}
            cx="15.5"
            cy="12"
            r="7.5"
            stroke={tok.accent}
            strokeWidth="0.5"
            opacity="0"
            style={fillBox}
          />
          <circle
            className={loop({
              '0%': { transform: 'translateX(11px) scale(1)', opacity: 0 },
              '23.9%': { transform: 'translateX(11px) scale(1)', opacity: 0 },
              '28.3%': { transform: 'translateX(8.5px) scale(1)', opacity: 1 },
              '42.4%': { transform: 'translateX(0) scale(1)', opacity: 1 },
              '46.7%': { transform: 'translateX(0) scale(1.25)', opacity: 1 },
              '51.1%': { transform: 'translateX(0) scale(1)', opacity: 1 },
              '100%': { transform: 'translateX(0) scale(1)', opacity: 1 },
            }, MOTION.easeOut)}
            cx="15.5"
            cy="12"
            r="1.9"
            fill={tok.accent}
            style={fillBox}
          />
        </g>
        <text
          className={loop({ '0%': { transform: 'translateY(1.5px)', opacity: 0 }, '56.5%': { transform: 'translateY(1.5px)', opacity: 0 }, '68.5%': { transform: 'translateY(0)', opacity: 1 }, '100%': { transform: 'translateY(0)', opacity: 1 } })}
          x="24"
          y="31.5"
          textAnchor="middle"
          fontFamily={FONTS.SANS}
          fontSize="7"
          fontWeight="700"
          fill="currentColor"
        >
          phebs
        </text>
      </svg>
    </div>
  )
}
