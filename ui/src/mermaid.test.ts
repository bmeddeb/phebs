import { beforeEach, expect, it, vi } from 'vitest'
import { TOKENS } from './theme'

const mermaid = vi.hoisted(() => ({
  initialize: vi.fn(),
  registerLayoutLoaders: vi.fn(),
  render: vi.fn(async (_id: string, _source: string, _host?: HTMLElement) => ({ svg: '<svg><text>safe</text></svg>' })),
}))

vi.mock('mermaid', () => ({ default: mermaid }))
vi.mock('@mermaid-js/layout-elk', () => ({ default: [] }))

import { renderMermaid } from './mermaid'

beforeEach(() => {
  mermaid.initialize.mockClear()
  mermaid.render.mockReset()
  mermaid.render.mockResolvedValue({ svg: '<svg><text>safe</text></svg>' })
})

it('refuses a flowchart image before Mermaid can allocate or fetch it', async () => {
  const source = 'flowchart TD\nA@{ img: "https://example.invalid/pixel.png", label: "x" }'
  await expect(renderMermaid(source, 'light', TOKENS.light)).rejects.toThrow(
    'diagram external resources are not permitted',
  )
  expect(mermaid.render).not.toHaveBeenCalled()
})

it('refuses protocol- and root-relative sequence icons before Mermaid touches the DOM', async () => {
  await expect(renderMermaid(
    'sequenceDiagram\nparticipant A@{ "icon": "//example.invalid/pixel.png" }\nA->>A: x',
    'light',
    TOKENS.light,
  )).rejects.toThrow('diagram external resources are not permitted')
  await expect(renderMermaid(
    'sequenceDiagram\nparticipant A@{ icon: "/local.png" }\nA->>A: x',
    'light',
    TOKENS.light,
  )).rejects.toThrow('diagram external resources are not permitted')
  expect(mermaid.render).not.toHaveBeenCalled()
})

it('refuses decoded properties, encoded CSS, and positional C4 sprites before render', async () => {
  const refused = [
    String.raw`flowchart TD
A@{ "\u0069mg": "\u002f\u002fevil/x.png" }`,
    String.raw`flowchart TD
A-->B
classDef ex fill:\75rl\28//example.invalid/pixel.svg\29
class A ex`,
    'C4Context\nPerson(a, "A", "", "/c4.png")',
  ]
  for (const source of refused) {
    await expect(renderMermaid(source, 'light', TOKENS.light)).rejects.toThrow(
      'diagram external resources are not permitted',
    )
  }
  expect(mermaid.render).not.toHaveBeenCalled()
})

it('still renders a resource-free diagram through the configured wrapper', async () => {
  await expect(renderMermaid('flowchart TD\nA-->B', 'light', TOKENS.light)).resolves.toContain('<text>safe</text>')
  expect(mermaid.render).toHaveBeenCalledTimes(1)
  expect(mermaid.render.mock.calls[0][2]).toBeInstanceOf(HTMLElement)
  expect(document.querySelector('[data-phebs-mermaid-host]')).toBeNull()
})

it('removes its live layout host when Mermaid rejects', async () => {
  mermaid.render.mockImplementation(async (_id: string, _source: string, host?: HTMLElement) => {
    host?.append(document.createElement('div'))
    throw new Error('parse failed')
  })
  for (let attempt = 0; attempt < 3; attempt += 1) {
    await expect(renderMermaid(`flowchart TD\nA--${attempt}`, 'light', TOKENS.light)).rejects.toThrow('parse failed')
    expect(document.querySelector('[data-phebs-mermaid-host]')).toBeNull()
  }
})

it('serializes renders and removes aborted stale work before it starts', async () => {
  let releaseFirst!: (value: { svg: string }) => void
  mermaid.render.mockImplementationOnce(() => new Promise((resolve) => {
    releaseFirst = resolve
  }))

  const first = renderMermaid('flowchart TD\nA-->B', 'light', TOKENS.light)
  const staleController = new AbortController()
  const stale = renderMermaid('flowchart TD\nB-->C', 'light', TOKENS.light, staleController.signal)
  const current = renderMermaid('flowchart TD\nC-->D', 'light', TOKENS.light)
  await Promise.resolve()
  expect(mermaid.render).toHaveBeenCalledTimes(1)

  const refused = expect(stale).rejects.toMatchObject({ name: 'AbortError' })
  staleController.abort()
  await refused
  releaseFirst({ svg: '<svg><path marker-end="url(#pointEnd)"></path></svg>' })
  await expect(first).resolves.toContain('pointEnd')
  await expect(current).resolves.toContain('<text>safe</text>')
  expect(mermaid.render).toHaveBeenCalledTimes(2)
  expect(mermaid.render.mock.calls.map((call) => call[1])).toEqual([
    'flowchart TD\nA-->B',
    'flowchart TD\nC-->D',
  ])
})
