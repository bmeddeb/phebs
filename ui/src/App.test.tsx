import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, test } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import { Header } from './App'

const engine = new Client()

function header(contractsAvailable: boolean) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <Header
          path="/"
          email="user@example.com"
          isAdmin={false}
          contractsAvailable={contractsAvailable}
          impactAvailable={false}
          investigationsAvailable={false}
          onLogout={() => {}}
        />
      </BaseProvider>
    </StyletronProvider>,
  )
}

afterEach(cleanup)

test('Contracts navigation is present only with the authenticated capability', () => {
  const dark = header(false)
  expect(screen.queryByRole('link', { name: 'Contracts' })).toBeNull()
  dark.unmount()

  header(true)
  expect(screen.getByRole('link', { name: 'Contracts' }).getAttribute('href'))
    .toBe('#/contracts')
})
