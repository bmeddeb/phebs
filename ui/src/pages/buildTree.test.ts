import { describe, expect, it } from 'vitest'
import { buildTree, type TreeNode } from './FilePage'

const child = (n: TreeNode, name: string, dir: boolean) =>
  n.children.find((c) => c.name === name && c.dir === dir)

describe('buildTree', () => {
  it('merges shared dirs into one nested structure', () => {
    const root = buildTree(['a/b/c.go', 'a/b/d.go', 'a/e.md'])
    expect(root.children.length).toBe(1)
    const a = child(root, 'a', true)!
    expect(a.path).toBe('a')
    expect(a.children.length).toBe(2)
    const b = child(a, 'b', true)!
    expect(b.path).toBe('a/b')
    expect(b.children.map((c) => c.name)).toEqual(['c.go', 'd.go'])
    expect(child(a, 'e.md', false)?.path).toBe('a/e.md')
  })

  it('sorts dirs before files, then localeCompare per level', () => {
    const root = buildTree(['zeta.go', 'Beta/x', 'alpha/x', 'Apple.md'])
    expect(root.children.map((c) => [c.name, c.dir])).toEqual([
      ['alpha', true],
      ['Beta', true],
      ['Apple.md', false],
      ['zeta.go', false],
    ])
  })

  it('gives root-level files path === name and dir === false', () => {
    const root = buildTree(['README.md', 'main.go'])
    for (const c of root.children) {
      expect(c.path).toBe(c.name)
      expect(c.dir).toBe(false)
      expect(c.children).toEqual([])
    }
  })

  it('keeps a file and dir with the same name as distinct nodes', () => {
    const root = buildTree(['x', 'x/y'])
    expect(root.children.length).toBe(2)
    const dir = child(root, 'x', true)!
    const file = child(root, 'x', false)!
    expect(file.children).toEqual([])
    expect(dir.children.map((c) => c.path)).toEqual(['x/y'])
    // dir sorts before file
    expect(root.children[0]).toBe(dir)
  })

  it('returns an empty root for no paths', () => {
    const root = buildTree([])
    expect(root).toEqual({ name: '', path: '', dir: true, children: [] })
  })

  it('accumulates full slash-joined path at every depth', () => {
    const root = buildTree(['a/b/c/d.txt'])
    const want = ['a', 'a/b', 'a/b/c', 'a/b/c/d.txt']
    let cur = root
    for (const p of want) {
      expect(cur.children.length).toBe(1)
      cur = cur.children[0]
      expect(cur.path).toBe(p)
    }
  })
})
