import { createContext, useContext } from 'react'
import { createLightTheme, createDarkTheme } from 'baseui'
import { DEFAULT_PALETTE, type PaletteName } from './palette'

// A crisp mono stack — the stock baseui token falls back to Lucida Console.
const MONO = 'ui-monospace, "SF Mono", Menlo, Monaco, "Cascadia Code", monospace'
const SANS = 'system-ui, "Helvetica Neue", Helvetica, Arial, sans-serif'

const monoOverride = {
  typography: {
    MonoParagraphXSmall: { fontFamily: MONO },
    MonoParagraphSmall: { fontFamily: MONO },
    MonoParagraphMedium: { fontFamily: MONO },
    MonoParagraphLarge: { fontFamily: MONO },
    MonoLabelSmall: { fontFamily: MONO },
    MonoLabelMedium: { fontFamily: MONO },
  },
}

// createLightTheme/createDarkTheme deep-merge overrides onto the stock theme.
export const lightTheme = createLightTheme(monoOverride)
export const darkTheme = createDarkTheme(monoOverride)

export const FONTS = { MONO, SANS }

// Counts and other numerals align in columns (charter §3).
export const NUMERIC = { fontVariantNumeric: 'tabular-nums' } as const

// Type scale (charter §3). caption is the floor: nothing persistent renders
// below 11px. Adoption is enforced through the T43.3 kit; new work uses these
// directly.
export const TYPE = {
  display: { fontSize: '20px', lineHeight: '28px', fontWeight: 600 },
  title: { fontSize: '17px', lineHeight: '24px', fontWeight: 600 },
  heading: { fontSize: '13px', lineHeight: '18px', fontWeight: 600 },
  body: { fontSize: '13px', lineHeight: '20px', fontWeight: 450 },
  small: { fontSize: '12px', lineHeight: '18px', fontWeight: 450 },
  caption: { fontSize: '11px', lineHeight: '16px', fontWeight: 450 },
} as const

export const SPACE = { xs: '4px', sm: '8px', md: '12px', lg: '16px', xl: '20px', xxl: '28px' } as const

// Two densities (charter §3): density changes spacing and row height, never
// information. Consumed by the directory/explorer surfaces via T43.11.
export const DENSITY = {
  comfortable: { rowMinHeight: '44px', cellPadY: '10px', cellPadX: '12px' },
  dense: { rowMinHeight: '32px', cellPadY: '5px', cellPadX: '10px' },
} as const

// Motion tokens (charter §3): 120–200ms elements, ≤300ms panels; motion
// communicates state transitions only. Under prefers-reduced-motion, replace
// movement with opacity or immediate state via REDUCED_MOTION overrides.
export const MOTION = {
  element: '160ms',
  panel: '260ms',
  // Indicator cadence (pulsing dots, shimmer, sweeps) — repetition, not a
  // transition, so it sits outside the 120–300ms transition windows.
  pulse: '1.4s',
  // The brand loader's full choreography loop (T43.12f) — the one long
  // cadence in the product, and it plays only while authentication loads.
  loader: '4.6s',
  easeOut: 'cubic-bezier(0.16, 1, 0.3, 1)',
  ease: 'cubic-bezier(0.4, 0, 0.2, 1)',
  // For indicator loops only: a continuous sweep must not throb, so its
  // easing is constant. Transitions never use it.
  linear: 'linear',
} as const

// ————— Charter §3 motion helpers (T43.12f) —————
// Motion is a closed system: every animation and transition in the product
// is composed through these helpers. They admit compositor properties only
// (opacity and transform, typed), token durations and easings only
// (literal-union types), and embed the reduced-motion path by
// construction, so a motion without one cannot be written.
// ui/src/motion.test.ts bans WAAPI and raw motion properties outside this
// module.

export interface CompositorFrame {
  opacity?: number | string
  transform?: string
}

type MotionDuration = (typeof MOTION)['element' | 'panel' | 'pulse' | 'loader']
type MotionEasing = (typeof MOTION)['easeOut' | 'ease' | 'linear']

interface AnimatedOptions {
  duration: MotionDuration
  easing?: MotionEasing
  loop?: boolean
  // Extra static styles under prefers-reduced-motion (e.g. hiding a
  // shimmer overlay). The animation itself is always removed.
  reduced?: Record<string, string | number>
}

export function animated(frames: Record<string, CompositorFrame>, opts: AnimatedOptions) {
  return {
    animationName: frames,
    animationDuration: opts.duration,
    animationTimingFunction: opts.easing ?? MOTION.easeOut,
    ...(opts.loop ? { animationIterationCount: 'infinite' } : {}),
    [REDUCED_MOTION]: { animationName: 'none', ...(opts.reduced ?? {}) },
  }
}

export function transitioned(properties: ('opacity' | 'transform')[], duration: MotionDuration = MOTION.element) {
  return {
    transitionProperty: properties.join(', '),
    transitionDuration: duration,
    transitionTimingFunction: MOTION.easeOut,
    [REDUCED_MOTION]: { transitionDuration: '0ms' },
  }
}

// Base Web's drawer animates its own transform; this pins any drawer
// layer (container and backdrop alike) to the panel token and the
// reduced path so the layers settle together.
export function panelTransition() {
  return {
    transitionDuration: MOTION.panel,
    transitionTimingFunction: MOTION.ease,
    [REDUCED_MOTION]: { transitionDuration: '0ms' },
  }
}
export const REDUCED_MOTION = '@media (prefers-reduced-motion: reduce)'

export type Mode = 'light' | 'dark'

// Closed five-state status vocabulary (charter §3), colored once for every
// surface. `text` passes WCAG AA (≥4.5:1) on pageBg, bandBg, fill, and
// hoverFill in its mode — verified in theme.test.ts. `solid` is for dots,
// borders, and fills, never running text.
export type StatusState = 'current' | 'stale' | 'conflict' | 'removed' | 'unavailable'
export interface StatusTone { text: string; solid: string }

const LIGHT_STATUS: Record<StatusState, StatusTone> = {
  current: { text: '#0B6B38', solid: '#0E8345' },
  stale: { text: '#8A6116', solid: '#FFC043' },
  conflict: { text: '#C50F30', solid: '#DE1135' },
  removed: { text: '#5E5E5E', solid: '#A6A6A6' },
  unavailable: { text: '#1E5BD6', solid: '#276EF1' },
}

const DARK_STATUS: Record<StatusState, StatusTone> = {
  current: { text: '#6FAE82', solid: '#5C9D70' },
  stale: { text: '#FFC043', solid: '#FFC043' },
  conflict: { text: '#E57779', solid: '#DE5B5D' },
  removed: { text: '#ABABAB', solid: '#5D5D5D' },
  unavailable: { text: '#7BA0E4', solid: '#5E8BDB' },
}

// Page-local status words collapse into the closed vocabulary; a word not in
// the map is a vocabulary violation, not a styling choice — extend the map
// deliberately or fix the word.
const STATUS_WORDS: Record<string, StatusState> = {
  current: 'current',
  ok: 'current',
  done: 'current',
  stale: 'stale',
  partial: 'stale',
  unresolved: 'stale',
  unowned: 'stale',
  unsupported: 'stale',
  unpublished: 'stale',
  conflict: 'conflict',
  ambiguous: 'conflict',
  error: 'conflict',
  failed: 'conflict',
  refused: 'conflict',
  removed: 'removed',
  none: 'removed',
  unavailable: 'unavailable',
}

/** Tone for a status word, collapsed into the closed five-state vocabulary. */
export function statusToneFor(word: string, tok: PhebsTokens): StatusTone | undefined {
  const state = STATUS_WORDS[word.toLowerCase()]
  return state ? tok.status[state] : undefined
}

// Legacy page-local tone names, dispatched once. Pages keep choosing the tone
// word; this is the single place a tone name becomes a color pair.
export type ToneName = 'green' | 'amber' | 'red' | 'blue' | 'neutral'
const TONE_STATE: Record<ToneName, StatusState> = {
  green: 'current',
  amber: 'stale',
  red: 'conflict',
  blue: 'unavailable',
  neutral: 'removed',
}
export function toneFor(tone: ToneName, tok: PhebsTokens): StatusTone {
  return tok.status[TONE_STATE[tone]]
}

// PhebsTokens are the exact design-handoff colors that don't map to a baseui
// semantic token (match highlight, status dots, code gutter, deep-link line).
// Values are Base Web primitives; see docs handoff.
export interface PhebsTokens {
  pageBg: string
  fill: string
  hoverFill: string
  bandBg: string
  textPrimary: string
  textSecondary: string
  textTertiary: string
  gutter: string
  cardBorder: string
  innerSep: string
  kbdBorder: string
  accent: string
  onAccent: string
  popoverShadow: string
  selectedLineBg: string
  selectedText: string
  matchBg: string
  plainCode: string
  addedLineBg: string
  deletedLineBg: string
  status: Record<StatusState, StatusTone>
}

const LIGHT: PhebsTokens = {
  pageBg: '#FFFFFF',
  fill: '#F3F3F3',
  hoverFill: '#F6F6F6',
  bandBg: '#FAFAFA',
  textPrimary: '#000000',
  textSecondary: '#4B4B4B',
  textTertiary: '#5E5E5E',
  gutter: '#A6A6A6',
  cardBorder: '#E8E8E8',
  innerSep: '#F3F3F3',
  kbdBorder: '#DDDDDD',
  accent: '#276EF1',
  onAccent: '#FFFFFF',
  popoverShadow: '0 12px 32px rgba(0, 0, 0, 0.24)',
  selectedLineBg: '#EFF4FE',
  selectedText: '#175BCC',
  matchBg: '#FBE5B6',
  plainCode: '#000000',
  addedLineBg: '#ECF8F0',
  deletedLineBg: '#FFF0F2',
  status: LIGHT_STATUS,
}

const DARK: PhebsTokens = {
  pageBg: '#000000',
  fill: '#292929',
  hoverFill: '#161616',
  bandBg: '#161616',
  textPrimary: '#FFFFFF',
  textSecondary: '#C4C4C4',
  textTertiary: '#ABABAB',
  gutter: '#5D5D5D',
  cardBorder: '#383838',
  innerSep: '#292929',
  kbdBorder: '#383838',
  accent: '#5E8BDB',
  onAccent: '#FFFFFF',
  popoverShadow: '0 12px 32px rgba(0, 0, 0, 0.24)',
  selectedLineBg: '#182946',
  selectedText: '#93B4EE',
  matchBg: '#4C3111',
  plainCode: '#DEDEDE',
  addedLineBg: '#10291A',
  deletedLineBg: '#35161B',
  status: DARK_STATUS,
}

export const TOKENS: Record<Mode, PhebsTokens> = { light: LIGHT, dark: DARK }

interface ModeCtx {
  mode: Mode
  toggle: () => void
}

export const ModeContext = createContext<ModeCtx>({ mode: 'light', toggle: () => {} })

export const useMode = () => useContext(ModeContext)

// T43.11 product density control. One product-wide state, two values;
// density changes spacing and row height, never information.
export type DensityName = keyof typeof DENSITY

interface DensityCtx {
  density: DensityName
  toggle: () => void
}

export const DensityContext = createContext<DensityCtx>({ density: 'comfortable', toggle: () => {} })

export const useDensity = () => useContext(DensityContext)

// T44.2 syntax-palette preference. Product-wide, persisted per browser
// like density; consumed by every highlighting surface.
interface PaletteCtx {
  palette: PaletteName
  setPalette: (palette: PaletteName) => void
}

export const PaletteContext = createContext<PaletteCtx>({ palette: DEFAULT_PALETTE, setPalette: () => {} })

export const usePalette = () => useContext(PaletteContext)

/** Current-mode design tokens. */
export function usePhebsTokens(): PhebsTokens {
  return TOKENS[useMode().mode]
}

/** Shared focus ring — the canonical form of the former per-page clones. */
export function focusRing(tok: PhebsTokens) {
  return { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' }
}
