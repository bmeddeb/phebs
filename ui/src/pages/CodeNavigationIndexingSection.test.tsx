import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { BaseProvider } from 'baseui'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { lightTheme } from '../theme'
import { CodeNavigationIndexingSection } from './CodeNavigationIndexingSection'

const engine = new Client()

function renderSection() {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={lightTheme}>
        <CodeNavigationIndexingSection />
      </BaseProvider>
    </StyletronProvider>,
  )
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

test('renders the Bazel-first capability-dark indexing boundary (T45.8a)', () => {
  renderSection()

  const section = screen.getByRole('region', { name: 'Code navigation indexing' })
  expect(section.id).toBe('code-navigation-indexing')
  expect(section.getAttribute('data-responsive-layout')).toBe('desktop-columns-mobile-stack')

  const providers = within(section).getAllByRole('article')
  expect(providers).toHaveLength(1)
  const provider = within(section).getByRole('article', { name: 'Bazel' })
  expect(provider.getAttribute('data-provider-id')).toBe('bazel')
  const card = within(provider)
  expect(card.getByRole('heading', { name: 'Bazel' })).toBeTruthy()
  expect(card.getByText('01 · Bazel first')).toBeTruthy()

  const status = card.getByRole('status')
  expect(status.getAttribute('data-tone')).toBe('blue')
  expect(status.textContent).toBe('Unavailable')

  expect(card.getByText(
    'Managed generation is not registered in this build. Committed SCIP artifacts remain the only code-navigation source.',
  )).toBeTruthy()
  expect(card.getByText(
    'No repository support, indexing profile, or build command is inferred.',
  )).toBeTruthy()
})

test('does not expose provider configuration, generation controls, or network work (T45.8a)', () => {
  const fetchSpy = vi.spyOn(globalThis, 'fetch')
  renderSection()

  for (const role of ['button', 'link', 'combobox', 'textbox', 'checkbox', 'radio', 'switch', 'slider', 'spinbutton', 'menuitem'] as const) {
    expect(screen.queryAllByRole(role)).toHaveLength(0)
  }
  const section = screen.getByRole('region', { name: 'Code navigation indexing' })
  expect(section.querySelectorAll('a[href], button, input, select, textarea, [contenteditable="true"], [tabindex]:not([tabindex="-1"])')).toHaveLength(0)
  expect(fetchSpy).not.toHaveBeenCalled()
})
