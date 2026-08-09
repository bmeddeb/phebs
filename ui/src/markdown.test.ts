import { describe, expect, it } from 'vitest'
import { renderMarkdown, segmentMarkdown } from './markdown'

// T44.3: the renderer is the trust boundary over untrusted repository
// content. These are adversarial: every case asserts the dangerous
// construct does NOT survive, alongside proof the safe content renders.

describe('renderMarkdown structure', () => {
  it('renders headings, emphasis, lists, and code', () => {
    const html = renderMarkdown('# Title\n\nSome **bold** and `code`.\n\n- one\n- two')
    expect(html).toContain('<h1>Title</h1>')
    expect(html).toContain('<strong>bold</strong>')
    expect(html).toContain('<code>code</code>')
    expect(html).toContain('<li>one</li>')
  })

  it('renders GFM tables', () => {
    const html = renderMarkdown('| a | b |\n|---|---|\n| 1 | 2 |')
    expect(html).toContain('<table>')
    expect(html).toContain('<td>1</td>')
  })
})

describe('renderMarkdown trust boundary', () => {
  it('strips raw script tags', () => {
    const html = renderMarkdown('ok\n\n<script>window.__pwned = 1</script>')
    expect(html).not.toContain('<script')
    expect(html).not.toContain('__pwned')
  })

  it('strips inline event handlers', () => {
    const html = renderMarkdown('<p onclick="steal()">click</p>')
    expect(html).not.toContain('onclick')
    expect(html).not.toContain('steal')
  })

  it('neutralizes javascript: links', () => {
    const html = renderMarkdown('[x](javascript:alert(1))')
    expect(html).not.toContain('javascript:')
  })

  it('drops data: URI links', () => {
    const html = renderMarkdown('[x](data:text/html,<script>alert(1)</script>)')
    expect(html).not.toContain('data:')
    expect(html).not.toContain('<script')
  })

  it('forces safe rel/target on surviving links', () => {
    const html = renderMarkdown('[docs](https://example.com)')
    expect(html).toContain('href="https://example.com"')
    expect(html).toContain('rel="noopener noreferrer nofollow"')
    expect(html).toContain('target="_blank"')
  })

  it('does not fetch images — keeps alt as a placeholder, drops src', () => {
    const html = renderMarkdown('![a diagram](./secret-relative.png)')
    expect(html).not.toContain('src=')
    expect(html).not.toContain('secret-relative.png')
    expect(html).toContain('🖼 a diagram')
  })

  it('strips style tags and attributes', () => {
    const html = renderMarkdown('<style>body{display:none}</style>\n\n<p style="position:fixed">x</p>')
    expect(html).not.toContain('<style')
    expect(html).not.toContain('position:fixed')
  })

  it('strips iframes and objects', () => {
    const html = renderMarkdown('<iframe src="https://evil.example"></iframe>\n\n<object data="x"></object>')
    expect(html).not.toContain('<iframe')
    expect(html).not.toContain('<object')
  })

  it('drops img srcset even if an image tag were allowed', () => {
    const html = renderMarkdown('<img src="x" srcset="https://evil.example 2x" alt="a">')
    expect(html).not.toContain('srcset')
    expect(html).not.toContain('evil.example')
  })

  it('strips author class attributes (no UI redressing via app classes) (T44.3f)', () => {
    const html = renderMarkdown('<p class="css-selectedLineBg spoof">x</p>')
    expect(html).not.toContain('css-selectedLineBg')
    expect(html).not.toContain('class="spoof"')
    expect(html).toContain('x')
  })

  it('strips aria-* attributes despite DOMPurify defaulting them on (T44.3f)', () => {
    const html = renderMarkdown('<p aria-label="injected label" aria-hidden="true">visible</p>')
    expect(html).not.toContain('aria-label')
    expect(html).not.toContain('aria-hidden')
    expect(html).toContain('visible')
  })

  it('renders images as an inert text placeholder with no attributes at all', () => {
    const html = renderMarkdown('![alt text](x.png)')
    expect(html).toContain('🖼 alt text')
    expect(html).not.toContain('src=')
    // With class stripped from the allowlist and the placeholder carrying
    // no attributes, no class survives anywhere in rendered output.
    expect(html).not.toContain('class=')
  })
})

describe('segmentMarkdown (T44.4)', () => {
  it('splits top-level mermaid fences out of the prose', () => {
    const segments = segmentMarkdown('# Doc\n\nintro\n\n```mermaid\ngraph TD\nA-->B\n```\n\nafter')
    expect(segments.map((segment) => segment.kind)).toEqual(['prose', 'mermaid', 'prose'])
    const [before, fence, after] = segments
    expect(before.kind === 'prose' && before.html).toContain('<h1>Doc</h1>')
    expect(fence.kind === 'mermaid' && fence.source).toBe('graph TD\nA-->B')
    expect(after.kind === 'prose' && after.html).toContain('after')
    // The diagram source never rides the HTML pipeline.
    expect(before.kind === 'prose' ? before.html : '').not.toContain('graph TD')
  })

  it('returns one prose segment when no fence exists', () => {
    const segments = segmentMarkdown('# Just prose\n\ntext')
    expect(segments.length).toBe(1)
    expect(segments[0].kind).toBe('prose')
  })

  it('leaves non-mermaid and nested fences as ordinary code blocks', () => {
    const segments = segmentMarkdown('```go\nfunc main() {}\n```\n\n> quote\n> ```mermaid\n> graph TD\n> ```')
    expect(segments.every((segment) => segment.kind === 'prose')).toBe(true)
    const html = segments.map((segment) => segment.kind === 'prose' ? segment.html : '').join('')
    expect(html).toContain('func main()')
  })

  it('sanitizes each prose segment exactly like renderMarkdown', () => {
    const segments = segmentMarkdown('<script>bad()</script>\n\n```mermaid\ngraph TD\n```\n\n<p onclick="x()">y</p>')
    const html = segments.filter((segment) => segment.kind === 'prose').map((segment) => segment.kind === 'prose' ? segment.html : '').join('')
    expect(html).not.toContain('<script')
    expect(html).not.toContain('onclick')
  })
})
