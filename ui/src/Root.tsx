import { useEffect, useState } from 'react'
import { BaseProvider } from 'baseui'
import App from './App'
import { AuthProvider } from './auth'
import { darkTheme, lightTheme, ModeContext, type Mode } from './theme'

function initialMode(): Mode {
  const saved = localStorage.getItem('phebs-theme')
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export default function Root() {
  const [mode, setMode] = useState<Mode>(initialMode)
  const toggle = () => setMode((current) => (current === 'light' ? 'dark' : 'light'))

  useEffect(() => {
    localStorage.setItem('phebs-theme', mode)
    document.documentElement.style.colorScheme = mode
  }, [mode])

  return (
    <ModeContext value={{ mode, toggle }}>
      <BaseProvider theme={mode === 'dark' ? darkTheme : lightTheme}>
        <AuthProvider>
          <App />
        </AuthProvider>
      </BaseProvider>
    </ModeContext>
  )
}
