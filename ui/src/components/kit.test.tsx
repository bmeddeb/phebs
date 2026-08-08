import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'

afterEach(cleanup)
import { BaseProvider } from 'baseui'
import { Provider as StyletronProvider } from 'styletron-react'
import { Client as Styletron } from 'styletron-engine-monolithic'
import { CaveatCollapse, CitationChip, CitationPanel, EmptyState, ErrorNotice, IdentityText, LoadingBlock, RefusalCard, StateNotice, StatusChip, StatusWord } from './kit'
import type { ServiceRelationshipCitation } from '../api'
import { ModeContext, TOKENS, focusRing, lightTheme } from '../theme'

const engine = new Styletron()

function mount(node: React.ReactNode) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={lightTheme}>
        <ModeContext.Provider value={{ mode: 'light', toggle: () => {} }}>{node}</ModeContext.Provider>
      </BaseProvider>
    </StyletronProvider>,
  )
}

describe('StatusChip', () => {
  it('renders the label with its tone recorded', () => {
    mount(<StatusChip tone="amber">Partial</StatusChip>)
    const chip = screen.getByText('Partial')
    expect(chip.getAttribute('data-tone')).toBe('amber')
  })
  for (const tone of ['green', 'amber', 'red', 'blue', 'neutral'] as const) {
    it(`accepts the ${tone} tone`, () => {
      mount(<StatusChip tone={tone}>{tone}</StatusChip>)
      expect(screen.getByText(tone)).toBeTruthy()
    })
  }
  it('passes through title and role', () => {
    mount(<StatusChip tone="green" title="state detail" role="status">Current</StatusChip>)
    const chip = screen.getByRole('status')
    expect(chip.getAttribute('title')).toBe('state detail')
  })
})

describe('StatusWord', () => {
  it('pairs the dot with a visible state word', () => {
    mount(<StatusWord tone="red">Conflict</StatusWord>)
    expect(screen.getByText('Conflict')).toBeTruthy()
  })
})

describe('StateNotice', () => {
  it('renders title and body', () => {
    mount(<StateNotice tone="amber" title="Stale snapshot">Rows read from generation r3.</StateNotice>)
    expect(screen.getByText('Stale snapshot')).toBeTruthy()
    expect(screen.getByText('Rows read from generation r3.')).toBeTruthy()
  })
  it('renders body-only when no title is given', () => {
    mount(<StateNotice tone="blue">Plane unavailable.</StateNotice>)
    expect(screen.getByText('Plane unavailable.')).toBeTruthy()
  })
})

describe('LoadingBlock', () => {
  it('is a labeled status region', () => {
    mount(<LoadingBlock label="Loading API keys…" />)
    const region = screen.getByRole('status')
    expect(region.textContent).toContain('Loading API keys…')
  })
})

describe('ErrorNotice', () => {
  it('is an alert with the bounded message', () => {
    mount(<ErrorNotice>Repository status failed.</ErrorNotice>)
    expect(screen.getByRole('alert').textContent).toContain('Repository status failed.')
  })
  it('exposes a working retry path', () => {
    const onRetry = vi.fn()
    mount(<ErrorNotice onRetry={onRetry}>Poll failed.</ErrorNotice>)
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })
  it('honors a custom retry label', () => {
    mount(<ErrorNotice onRetry={() => {}} retryLabel="Retry folder">x</ErrorNotice>)
    expect(screen.getByRole('button', { name: 'Retry folder' })).toBeTruthy()
  })
})

describe('EmptyState', () => {
  it('names the absence instead of implying it', () => {
    mount(<EmptyState title="No commits are visible for this path at HEAD." detail="Visibility is authorization-bounded." />)
    expect(screen.getByText('No commits are visible for this path at HEAD.')).toBeTruthy()
    expect(screen.getByText('Visibility is authorization-bounded.')).toBeTruthy()
  })
})

describe('IdentityText', () => {
  it('renders identities in a code element', () => {
    mount(<IdentityText title="digest">sha256:ab12</IdentityText>)
    const code = screen.getByTitle('digest')
    expect(code.tagName).toBe('CODE')
    expect(code.textContent).toBe('sha256:ab12')
  })
})

describe('CaveatCollapse', () => {
  it('collapses without disappearing: summary always visible, body on expand', () => {
    mount(
      <CaveatCollapse summary="Static source evidence within the displayed snapshots">
        Establishes mechanics only; no runtime topology is implied.
      </CaveatCollapse>,
    )
    expect(screen.getByText('Static source evidence within the displayed snapshots')).toBeTruthy()
    const details = screen.getByText('Static source evidence within the displayed snapshots').closest('details')!
    expect(details.open).toBe(false)
    fireEvent.click(screen.getByText('Static source evidence within the displayed snapshots'))
    expect(screen.getByText('Establishes mechanics only; no runtime topology is implied.')).toBeTruthy()
  })
})

describe('CitationPanel', () => {
  const citation = {
    schema: 'phebs-service-relationship-citation-v1',
    repository: 'github.com/acme/orders',
    root_schema: 'phebs-relationship-root-v1',
    generation: `sha256:${'a'.repeat(64)}`,
    root_digest: `sha256:${'b'.repeat(64)}`,
    authority_digest: `sha256:${'0'.repeat(64)}`,
    projection: {
      kind: 'rpc',
      posting_digest: `sha256:${'c'.repeat(64)}`,
      class: 'resolved',
      plane: 'caller',
      source: { path: 'service/orders', unowned: false, claims: [] },
      digest: `sha256:${'d'.repeat(64)}`,
    },
    evidence: {
      kind: 'rpc',
      plane: 'caller',
      class: 'resolved',
      path: 'service/orders/caller.go',
      object_id: 'e'.repeat(40),
      content_digest: `sha256:${'f'.repeat(64)}`,
      span: { start_byte: 10, end_byte: 18, start_line: 2, end_line: 2 },
      source_role: 'caller',
      posting_digest: `sha256:${'c'.repeat(64)}`,
    },
    content: 'client.Get(order)',
  } satisfies ServiceRelationshipCitation

  it('renders one named dialog with identities and cited bytes', () => {
    const onClose = vi.fn()
    mount(<CitationPanel id="citation-test" loading={false} error="" citation={citation} onClose={onClose} />)
    expect(screen.getByRole('dialog', { name: 'Exact source citation' })).toBeTruthy()
    expect(screen.getByText('service/orders/caller.go · lines 2–2')).toBeTruthy()
    expect(screen.getByText('client.Get(order)')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Close citation' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('announces loading and fail-closed errors', () => {
    const mounted = mount(<CitationPanel id="citation-loading" loading error="" citation={null} onClose={() => {}} />)
    expect(screen.getByRole('status').textContent).toContain('Reading immutable source span')
    mounted.unmount()
    mount(<CitationPanel id="citation-error" loading={false} error="authority mismatch" citation={null} onClose={() => {}} />)
    expect(screen.getByRole('alert').textContent).toContain('authority mismatch')
  })
})

describe('RefusalCard', () => {
  it('presents the closed refusal shape exactly as delivered', () => {
    mount(<RefusalCard refusal={{
      schema: 'phebs-pipeline-refusal-v1',
      stage: 'extractor_execution',
      generation_kind: 'extraction_domain',
      classification: 'limit',
      dimension: 'source_read_bytes',
      observed: 536870912,
      limit: 268435456,
    }} />)
    const card = screen.getByRole('status')
    expect(card.getAttribute('title')).toBe('phebs-pipeline-refusal-v1')
    expect(card.textContent).toContain('Refused · extractor_execution · extraction_domain')
    expect(card.textContent).toContain('limit · dimension source_read_bytes')
    expect(card.textContent).toContain('536870912')
    expect(card.textContent).toContain('268435456')
  })
  it('omits scalars for non-limit classifications (canonical zeroes are not measurements)', () => {
    mount(<RefusalCard refusal={{
      schema: 'phebs-pipeline-refusal-v1',
      stage: 'candidate_strict_open',
      generation_kind: 'candidate',
      classification: 'invalid',
      dimension: 'unknown',
      observed: 0,
      limit: 0,
    }} />)
    const card = screen.getByRole('status')
    expect(card.textContent).toContain('invalid · dimension unknown')
    expect(card.textContent).not.toContain('observed')
  })
})

describe('CitationChip', () => {
  it('renders the citation as its path:line identity with dialog semantics', () => {
    const onOpen = vi.fn()
    mount(<CitationChip path="services/orders/kafka.go" span={{ start_line: 10, end_line: 10 }} onOpen={onOpen} />)
    const chip = screen.getByRole('button', { name: 'services/orders/kafka.go:10' })
    expect(chip.getAttribute('aria-haspopup')).toBe('dialog')
    fireEvent.click(chip)
    expect(onOpen).toHaveBeenCalledTimes(1)
  })
  it('names multi-line spans with their range', () => {
    mount(<CitationChip path="a.go" span={{ start_line: 3, end_line: 7 }} onOpen={() => {}} />)
    expect(screen.getByRole('button', { name: 'a.go:3\u20137' })).toBeTruthy()
  })
})

describe('CitationPanel keyboard contract (T43.7)', () => {
  const kbdCitation = {
    schema: 'phebs-service-relationship-citation-v1',
    repository: 'r', root_schema: 'phebs-relationship-root-v1', generation: `sha256:${'a'.repeat(64)}`,
    root_digest: `sha256:${'b'.repeat(64)}`, authority_digest: `sha256:${'0'.repeat(64)}`,
    projection: { kind: 'rpc', posting_digest: `sha256:${'c'.repeat(64)}`, class: 'resolved', plane: 'caller', source: { path: 'p', unowned: false, claims: [] }, digest: `sha256:${'d'.repeat(64)}` },
    evidence: { kind: 'rpc', plane: 'caller', class: 'resolved', path: 'p.go', object_id: 'e'.repeat(40), content_digest: `sha256:${'f'.repeat(64)}`, span: { start_byte: 0, end_byte: 4, start_line: 1, end_line: 1 }, source_role: 'caller', posting_digest: `sha256:${'c'.repeat(64)}` },
    content: 'call()',
  } satisfies ServiceRelationshipCitation
  it('focuses the panel on open and closes on Escape', () => {
    const onClose = vi.fn()
    mount(<CitationPanel id="kbd" loading={false} error="" citation={kbdCitation} onClose={onClose} />)
    const dialog = screen.getByRole('dialog', { name: 'Exact source citation' })
    expect(document.activeElement).toBe(dialog)
    fireEvent.keyDown(dialog, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })
  it('offers the fail-closed refresh path on error', () => {
    const onRefresh = vi.fn()
    mount(<CitationPanel id="rf" loading={false} error="citation response authority differs" citation={null} onClose={() => {}} onRefresh={onRefresh} />)
    fireEvent.click(screen.getByRole('button', { name: 'Refresh evidence rows' }))
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })
})

describe('focusRing', () => {
  it('derives from the accent token', () => {
    expect(focusRing(TOKENS.light).outline).toContain(TOKENS.light.accent)
  })
})
