import { StrictMode, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { Client as Styletron } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider } from 'baseui'
import App from './App.tsx'
import { ModeContext, lightTheme, darkTheme, type Mode } from './theme'
import './index.css'

const engine = new Styletron()

function initialMode(): Mode {
  const saved = localStorage.getItem('phebs-theme')
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function Root() {
  const [mode, setMode] = useState<Mode>(initialMode)
  const toggle = () => setMode((m) => (m === 'light' ? 'dark' : 'light'))

  useEffect(() => {
    localStorage.setItem('phebs-theme', mode)
    document.documentElement.style.colorScheme = mode
  }, [mode])

  return (
    <ModeContext value={{ mode, toggle }}>
      <BaseProvider theme={mode === 'dark' ? darkTheme : lightTheme}>
        <App />
      </BaseProvider>
    </ModeContext>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StyletronProvider value={engine}>
      <Root />
    </StyletronProvider>
  </StrictMode>,
)
