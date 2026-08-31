import { act } from 'react'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { BaseProvider } from 'baseui'
import { Client as Styletron } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import * as api from '../api'
import type { CallerMapCitation, CallerMapSource } from '../api'
import { lightTheme, ModeContext } from '../theme'
import ExactCallerCitation from './ExactCallerCitation'

vi.mock('../api', () => ({ fetchCallerCitation: vi.fn() }))

const fetchCallerCitation = vi.mocked(api.fetchCallerCitation)
const engine = new Styletron()

function source(path: string, citation: string): CallerMapSource {
  return {
    repository: 'github.com/acme/orders',
    commit: '0123456789abcdef',
    path,
    object_id: `object:${path}`,
    blob_digest: `sha256:${path}`,
    plane: 'repository-overlay',
    start_byte: 10,
    end_byte: 30,
    start_line: 2,
    end_line: 3,
    assertion_id: `assertion:${path}`,
    run_id: 'run-1',
    atom_id: `atom:${path}`,
    citation,
  }
}

function response(selected: CallerMapSource, content: string): CallerMapCitation {
  return {
    schema_version: 'caller-map-citation-v1',
    generation: {
      state: 'current',
      plane: 'repository-overlay',
      repository: selected.repository,
      commit: selected.commit,
      generation_digest: 'sha256:generation',
      publication_revision: 1,
    },
    source: selected,
    content,
  }
}

function wrapped(selected: CallerMapSource) {
  return (
    <StyletronProvider value={engine}>
      <BaseProvider theme={lightTheme}>
        <ModeContext.Provider value={{ mode: 'light', toggle: () => {} }}>
          <ExactCallerCitation source={selected} />
        </ModeContext.Provider>
      </BaseProvider>
    </StyletronProvider>
  )
}

beforeEach(() => { fetchCallerCitation.mockReset() })
afterEach(cleanup)

test('hides completed citation bytes immediately when source identity changes', async () => {
  const first = source('internal/first.go', 'citation:first')
  const second = source('internal/second.go', 'citation:second')
  fetchCallerCitation.mockResolvedValue(response(first, 'return first'))
  const mounted = render(wrapped(first))
  fireEvent.click(screen.getByRole('button', { name: /internal\/first\.go:2/ }))
  expect((await screen.findByLabelText(/Exact cited bytes for .*internal\/first\.go:2/)).textContent).toBe('return first')

  mounted.rerender(wrapped(second))

  expect(screen.queryByTestId('caller-map-exact-citation')).toBeNull()
  expect(screen.getByRole('button', { name: /internal\/second\.go:2/ }).getAttribute('aria-expanded')).toBe('false')
})

test('ignores an old citation response that settles after source identity changes', async () => {
  const first = source('internal/first.go', 'citation:first')
  const second = source('internal/second.go', 'citation:second')
  let settle!: (value: CallerMapCitation) => void
  fetchCallerCitation.mockImplementation(() => new Promise((resolve) => { settle = resolve }))
  const mounted = render(wrapped(first))
  fireEvent.click(screen.getByRole('button', { name: /internal\/first\.go:2/ }))
  expect(screen.getByRole('status').textContent).toContain('Reading exact citation')

  mounted.rerender(wrapped(second))
  await act(async () => { settle(response(first, 'return stale')) })
  await waitFor(() => expect(screen.queryByRole('status')).toBeNull())

  expect(screen.queryByTestId('caller-map-exact-citation')).toBeNull()
  expect(screen.queryByText('return stale')).toBeNull()
})
