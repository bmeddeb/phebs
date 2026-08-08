import { useEffect, useState } from 'react'
import { BaseProvider } from 'baseui'
import App from './App'
import { AuthProvider } from './auth'
import { darkTheme, lightTheme, DensityContext, ModeContext, type DensityName, type Mode } from './theme'

function initialMode(): Mode {
  const saved = localStorage.getItem('phebs-theme')
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

// Density has no media-query analogue: comfortable unless the user chose
// dense (T43.11).
function initialDensity(): DensityName {
  return localStorage.getItem('phebs-density') === 'dense' ? 'dense' : 'comfortable'
}

export default function Root() {
  const [mode, setMode] = useState<Mode>(initialMode)
  const toggle = () => setMode((current) => (current === 'light' ? 'dark' : 'light'))
  const [density, setDensity] = useState<DensityName>(initialDensity)
  const toggleDensity = () => setDensity((current) => (current === 'comfortable' ? 'dense' : 'comfortable'))

  useEffect(() => {
    localStorage.setItem('phebs-theme', mode)
    document.documentElement.style.colorScheme = mode
  }, [mode])

  useEffect(() => {
    localStorage.setItem('phebs-density', density)
  }, [density])

  return (
    <ModeContext value={{ mode, toggle }}>
      <DensityContext value={{ density, toggle: toggleDensity }}>
        <BaseProvider theme={mode === 'dark' ? darkTheme : lightTheme}>
          <AuthProvider>
            <App />
          </AuthProvider>
        </BaseProvider>
      </DensityContext>
    </ModeContext>
  )
}
