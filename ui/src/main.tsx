import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Client as Styletron } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import Root from './Root'
import './index.css'

const engine = new Styletron()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StyletronProvider value={engine}>
      <Root />
    </StyletronProvider>
  </StrictMode>,
)
