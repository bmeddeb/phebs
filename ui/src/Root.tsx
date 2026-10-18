import { useEffect, useState } from 'react'
import { BaseProvider } from 'baseui'
import App from './App'
import { AuthProvider } from './auth'
import { darkTheme, lightTheme, DensityContext, ModeContext, PaletteContext, type DensityName, type Mode } from './theme'
import { DEFAULT_PALETTE, isPaletteName, type PaletteName } from './palette'

function readPreference(key: string): string | null {
  try { return localStorage.getItem(key) } catch { return null }
}

function savePreference(key: string, value: string) {
  // Browser privacy settings and quota failures must not disable the app.
  try { localStorage.setItem(key, value) } catch { /* Keep the in-memory preference. */ }
}

function initialMode(): Mode {
  const saved = readPreference('phebs-theme')
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

// Density has no media-query analogue: comfortable unless the user chose
// dense (T43.11).
function initialDensity(): DensityName {
  return readPreference('phebs-density') === 'dense' ? 'dense' : 'comfortable'
}

// Unknown stored values (e.g. a removed palette) fall back to the default
// rather than failing (T44.2).
function initialPalette(): PaletteName {
  const stored = readPreference('phebs-palette')
  return isPaletteName(stored) ? stored : DEFAULT_PALETTE
}

export default function Root() {
  const [mode, setMode] = useState<Mode>(initialMode)
  const toggle = () => setMode((current) => (current === 'light' ? 'dark' : 'light'))
  const [density, setDensity] = useState<DensityName>(initialDensity)
  const toggleDensity = () => setDensity((current) => (current === 'comfortable' ? 'dense' : 'comfortable'))
  const [palette, setPalette] = useState<PaletteName>(initialPalette)

  useEffect(() => {
    savePreference('phebs-theme', mode)
    document.documentElement.style.colorScheme = mode
  }, [mode])

  useEffect(() => {
    savePreference('phebs-density', density)
  }, [density])

  useEffect(() => {
    savePreference('phebs-palette', palette)
  }, [palette])

  return (
    <ModeContext value={{ mode, toggle }}>
      <DensityContext value={{ density, toggle: toggleDensity }}>
        <PaletteContext value={{ palette, setPalette }}>
          <BaseProvider theme={mode === 'dark' ? darkTheme : lightTheme}>
            <AuthProvider>
              <App />
            </AuthProvider>
          </BaseProvider>
        </PaletteContext>
      </DensityContext>
    </ModeContext>
  )
}
