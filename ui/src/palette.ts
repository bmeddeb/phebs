// T44.2: the curated syntax-palette registry. A palette defines all seven
// roles in both themes. Every value passes the AA gate in theme.test.ts —
// ≥4.5:1 against the code background (pageBg) AND the anchor-line tint
// (selectedLineBg) in its mode; the high-contrast palette holds ≥7:1
// against the page. This module carries data only (no CodeMirror imports),
// so the shell can read names and colors without touching the lazy
// highlighting chunks.

export type PaletteName = 'phebs' | 'quiet' | 'classic' | 'high-contrast'

export type RoleName = 'keyword' | 'func' | 'type' | 'string' | 'comment' | 'number' | 'operator'

export interface RoleStyle {
  color: string
  fontStyle?: string
}

export interface SyntaxPalette {
  label: string
  light: Record<RoleName, RoleStyle>
  dark: Record<RoleName, RoleStyle>
}

const italic = (color: string): RoleStyle => ({ color, fontStyle: 'italic' })

export const PALETTES: Record<PaletteName, SyntaxPalette> = {
  // The design-handoff palette. T44.2's gate found its original comment
  // grays sub-AA (3.64:1 light, 4.3:1 dark) — corrected here, with the
  // light number nudged for the anchor-line tint.
  phebs: {
    label: 'Phebs',
    light: {
      keyword: { color: '#7C3EC3' },
      func: { color: '#175BCC' },
      type: { color: '#016974' },
      string: { color: '#166C3B' },
      comment: italic('#6A6A6A'),
      number: { color: '#95530A' },
      operator: { color: '#5E5E5E' },
    },
    dark: {
      keyword: { color: '#BDA7E4' },
      func: { color: '#93B4EE' },
      type: { color: '#72C1CD' },
      string: { color: '#8FC19C' },
      comment: italic('#919191'),
      number: { color: '#DEA85E' },
      operator: { color: '#ABABAB' },
    },
  },
  // Near-monochrome reading palette: ink levels with two restrained
  // accents (indigo declarations, warm literals).
  quiet: {
    label: 'Quiet',
    light: {
      keyword: { color: '#3F3F74' },
      func: { color: '#44403C' },
      type: { color: '#57534E' },
      string: { color: '#7C2D12' },
      comment: italic('#6E6B64'),
      number: { color: '#8A3D0D' },
      operator: { color: '#625F58' },
    },
    dark: {
      keyword: { color: '#A6ACE0' },
      func: { color: '#D6D3D1' },
      type: { color: '#B8B2AC' },
      string: { color: '#DBA179' },
      comment: italic('#98958E'),
      number: { color: '#E0A055' },
      operator: { color: '#9C9A94' },
    },
  },
  // Traditional editor hues: blue keywords, warm strings, green comments.
  classic: {
    label: 'Classic',
    light: {
      keyword: { color: '#1414CC' },
      func: { color: '#6E5518' },
      type: { color: '#1A6E7E' },
      string: { color: '#A31515' },
      comment: italic('#00611C'),
      number: { color: '#08744B' },
      operator: { color: '#444444' },
    },
    dark: {
      keyword: { color: '#6FAADC' },
      func: { color: '#DCDCAA' },
      type: { color: '#4EC9B0' },
      string: { color: '#CE9178' },
      comment: italic('#93B383'),
      number: { color: '#B5CEA8' },
      operator: { color: '#D4D4D4' },
    },
  },
  // Maximal separation at ≥7:1 against the page in both modes.
  'high-contrast': {
    label: 'High contrast',
    light: {
      keyword: { color: '#0522A5' },
      func: { color: '#00457A' },
      type: { color: '#00514E' },
      string: { color: '#8C0000' },
      comment: italic('#4A4A4A'),
      number: { color: '#703600' },
      operator: { color: '#1A1A1A' },
    },
    dark: {
      keyword: { color: '#A8BEFF' },
      func: { color: '#7FDFFF' },
      type: { color: '#5FE6D2' },
      string: { color: '#FFA8A8' },
      comment: italic('#BDBDBD'),
      number: { color: '#FFBE7A' },
      operator: { color: '#F2F2F2' },
    },
  },
}

export const DEFAULT_PALETTE: PaletteName = 'phebs'

export const PALETTE_NAMES = Object.keys(PALETTES) as PaletteName[]

export function isPaletteName(value: string | null): value is PaletteName {
  return value !== null && value in PALETTES
}
