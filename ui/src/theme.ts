import { createContext, useContext } from 'react'
import { createLightTheme, createDarkTheme } from 'baseui'

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

export type Mode = 'light' | 'dark'

// PhebsTokens are the exact design-handoff colors that don't map to a baseui
// semantic token (match highlight, status dots, code gutter, deep-link line).
// Values are Base Web primitives; see docs handoff.
export interface PhebsTokens {
  pageBg: string
  fill: string
  hoverFill: string
  textPrimary: string
  textSecondary: string
  textTertiary: string
  gutter: string
  cardBorder: string
  innerSep: string
  kbdBorder: string
  accent: string
  selectedLineBg: string
  selectedText: string
  matchBg: string
  plainCode: string
  addedLineBg: string
  deletedLineBg: string
  statusGreen: string
  statusBlue: string
  statusRed: string
  statusAmber: string
}

const LIGHT: PhebsTokens = {
  pageBg: '#FFFFFF',
  fill: '#F3F3F3',
  hoverFill: '#F6F6F6',
  textPrimary: '#000000',
  textSecondary: '#4B4B4B',
  textTertiary: '#5E5E5E',
  gutter: '#A6A6A6',
  cardBorder: '#E8E8E8',
  innerSep: '#F3F3F3',
  kbdBorder: '#DDDDDD',
  accent: '#276EF1',
  selectedLineBg: '#EFF4FE',
  selectedText: '#175BCC',
  matchBg: '#FBE5B6',
  plainCode: '#000000',
  addedLineBg: '#ECF8F0',
  deletedLineBg: '#FFF0F2',
  statusGreen: '#0E8345',
  statusBlue: '#276EF1',
  statusRed: '#DE1135',
  statusAmber: '#FFC043',
}

const DARK: PhebsTokens = {
  pageBg: '#000000',
  fill: '#292929',
  hoverFill: '#161616',
  textPrimary: '#FFFFFF',
  textSecondary: '#C4C4C4',
  textTertiary: '#ABABAB',
  gutter: '#5D5D5D',
  cardBorder: '#383838',
  innerSep: '#292929',
  kbdBorder: '#383838',
  accent: '#5E8BDB',
  selectedLineBg: '#182946',
  selectedText: '#93B4EE',
  matchBg: '#4C3111',
  plainCode: '#DEDEDE',
  addedLineBg: '#10291A',
  deletedLineBg: '#35161B',
  statusGreen: '#5C9D70',
  statusBlue: '#5E8BDB',
  statusRed: '#DE5B5D',
  statusAmber: '#FFC043',
}

export const TOKENS: Record<Mode, PhebsTokens> = { light: LIGHT, dark: DARK }

interface ModeCtx {
  mode: Mode
  toggle: () => void
}

export const ModeContext = createContext<ModeCtx>({ mode: 'light', toggle: () => {} })

export const useMode = () => useContext(ModeContext)

/** Current-mode design tokens. */
export function usePhebsTokens(): PhebsTokens {
  return TOKENS[useMode().mode]
}
