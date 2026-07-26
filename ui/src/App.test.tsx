import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, test } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import { Header } from './App'

const engine = new Client()

function header(contractsAvailable: boolean, topicsAvailable = false) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <Header
          path="/"
          email="user@example.com"
          isAdmin={false}
          contractsAvailable={contractsAvailable}
          impactAvailable={false}
          topicsAvailable={topicsAvailable}
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

test('Topics navigation is present only with the kafka-topic-usage capability', () => {
  const dark = header(false, false)
  expect(screen.queryByRole('link', { name: 'Topics' })).toBeNull()
  dark.unmount()

  header(false, true)
  expect(screen.getByRole('link', { name: 'Topics' }).getAttribute('href'))
    .toBe('#/topics')
})
