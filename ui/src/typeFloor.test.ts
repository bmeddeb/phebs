import { readFileSync, readdirSync, statSync } from 'node:fs'
import { basename, join, relative, resolve } from 'node:path'
import ts from 'typescript'
import { describe, expect, it } from 'vitest'
import { TYPE } from './theme'

// T43R.1 makes the type floor a closed source contract. Interface text is at
// least 11px; the one 10px exception is TYPE.evidenceMetadata, whose semantic
// use is reviewed at each call site. Raw sub-floor values and relative sizes
// would turn that narrow exception into an unreviewable dialect.

const SRC = resolve(process.cwd(), 'src')
const BRAND_PATH = resolve(SRC, 'Brand.tsx')
const THEME_PATH = resolve(SRC, 'theme.ts')

function sourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) out.push(...sourceFiles(path))
    else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(path)
  }
  return out
}

function styleSourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) out.push(...styleSourceFiles(path))
    else if (/\.(?:tsx?|css)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(path)
  }
  return out
}

function propertyName(node: ts.PropertyName): string | undefined {
  if (ts.isIdentifier(node) || ts.isStringLiteral(node) || ts.isNumericLiteral(node)) return node.text
  if (ts.isComputedPropertyName(node) && ts.isStringLiteral(node.expression)) return node.expression.text
  return undefined
}

function lineOf(source: ts.SourceFile, node: ts.Node): number {
  return source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1
}

function expressionText(source: ts.SourceFile, node: ts.Node): string {
  return node.getText(source).replace(/\s+/g, ' ')
}

function isEvidenceMetadataObject(node: ts.Node): node is ts.PropertyAccessExpression {
  return ts.isPropertyAccessExpression(node) &&
    node.name.text === 'evidenceMetadata' &&
    ts.isIdentifier(node.expression) &&
    node.expression.text === 'TYPE'
}

function isEvidenceMetadataToken(node: ts.Expression): boolean {
  return ts.isPropertyAccessExpression(node) &&
    node.name.text === 'fontSize' &&
    isEvidenceMetadataObject(node.expression)
}

function isSafeFontSizeLiteral(value: string, tokenAllowed = false): boolean {
  const px = value.match(/^(\d+(?:\.\d+)?)px$/)
  if (px) {
    const size = Number(px[1])
    return size >= 11 || (tokenAllowed && size === 10)
  }
  const clamp = value.match(
    /^clamp\((\d+(?:\.\d+)?)px,\s*\d+(?:\.\d+)?(?:vw|vh|vmin|vmax|em|rem|%),\s*(\d+(?:\.\d+)?)px\)$/,
  )
  return !!clamp && Number(clamp[1]) >= 11 && Number(clamp[2]) >= Number(clamp[1])
}

function isBrandDecoration(
  path: string,
  source: ts.SourceFile,
  attribute: ts.JsxAttribute,
): boolean {
  if (path !== BRAND_PATH || !attribute.initializer ||
      !ts.isStringLiteral(attribute.initializer) || attribute.initializer.text !== '7') return false
  const opening = attribute.parent.parent
  if (!ts.isJsxOpeningElement(opening) || opening.tagName.getText(source) !== 'text') return false
  let ancestor: ts.Node | undefined = opening.parent
  while (ancestor) {
    if (ts.isJsxElement(ancestor) && ancestor.openingElement.tagName.getText(source) === 'svg') {
      const attributes = ancestor.openingElement.attributes.properties
      const value = (name: string) => attributes.find((candidate): candidate is ts.JsxAttribute =>
        ts.isJsxAttribute(candidate) && candidate.name.getText(source) === name)?.initializer
      const hidden = value('aria-hidden')
      const viewBox = value('viewBox')
      return hidden === undefined && !!attributes.find((candidate) =>
        ts.isJsxAttribute(candidate) && candidate.name.getText(source) === 'aria-hidden') &&
        !!viewBox && ts.isStringLiteral(viewBox) && viewBox.text === '0 0 48 36'
    }
    ancestor = ancestor.parent
  }
  return false
}

function literalFontSizes(path: string): string[] {
  const source = ts.createSourceFile(
    path,
    readFileSync(path, 'utf8'),
    ts.ScriptTarget.Latest,
    true,
    path.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  )
  const violations: string[] = []

  const inspectLiteral = (node: ts.Node, value: string, tokenAllowed: boolean) => {
    if (isSafeFontSizeLiteral(value, tokenAllowed)) return
    const px = value.match(/^(\d+(?:\.\d+)?)px$/)
    violations.push(`${basename(path)}:${lineOf(source, node)} uses ${px ? 'raw' : 'unreviewed font size'} ${value}`)
  }

  const visit = (node: ts.Node) => {
    if (isEvidenceMetadataObject(node)) {
      const directFontSize = ts.isPropertyAccessExpression(node.parent) &&
        node.parent.expression === node && node.parent.name.text === 'fontSize'
      if (!directFontSize) {
        violations.push(`${basename(path)}:${lineOf(source, node)} uses the whole evidence-metadata token`)
      }
    }
    if (ts.isShorthandPropertyAssignment(node) && node.name.text === 'fontSize') {
      violations.push(`${basename(path)}:${lineOf(source, node)} uses unreviewed fontSize shorthand`)
    }
    if (ts.isPropertyAssignment(node) && propertyName(node.name) === 'fontSize') {
      const tokenAllowed = path === THEME_PATH &&
        ts.isObjectLiteralExpression(node.parent) &&
        ts.isPropertyAssignment(node.parent.parent) &&
        propertyName(node.parent.parent.name) === 'evidenceMetadata'
      const inspect = (valueNode: ts.Expression) => {
        if (ts.isStringLiteral(valueNode) || ts.isNoSubstitutionTemplateLiteral(valueNode)) {
          inspectLiteral(valueNode, valueNode.text, tokenAllowed)
        } else if (ts.isNumericLiteral(valueNode) && Number(valueNode.text) < 11 && !tokenAllowed) {
          violations.push(`${basename(path)}:${lineOf(source, valueNode)} uses raw ${valueNode.text}`)
        } else if (ts.isNumericLiteral(valueNode)) {
          return
        } else if (isEvidenceMetadataToken(valueNode)) {
          return
        } else if (ts.isConditionalExpression(valueNode)) {
          inspect(valueNode.whenTrue)
          inspect(valueNode.whenFalse)
        } else if (ts.isParenthesizedExpression(valueNode) || ts.isAsExpression(valueNode) ||
          ts.isTypeAssertionExpression(valueNode) || ts.isNonNullExpression(valueNode) ||
          ts.isSatisfiesExpression(valueNode)) {
          inspect(valueNode.expression)
        } else if (ts.isBinaryExpression(valueNode) &&
          valueNode.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken) {
          inspect(valueNode.left)
          inspect(valueNode.right)
        } else if (path === BRAND_PATH && ts.isTemplateExpression(valueNode) &&
          valueNode.getText(source) === '`${safeWordmarkSize}px`') {
          return
        } else {
          violations.push(`${basename(path)}:${lineOf(source, valueNode)} uses unreviewed expression ${expressionText(source, valueNode)}`)
        }
      }
      inspect(node.initializer)
    }

    if (ts.isJsxAttribute(node) && ['fontSize', 'font-size'].includes(node.name.getText(source)) && !isBrandDecoration(path, source, node)) {
      if (node.initializer && ts.isStringLiteral(node.initializer)) {
        const unitless = node.initializer.text.match(/^(\d+(?:\.\d+)?)$/)
        if (unitless) {
          if (Number(unitless[1]) < 11) {
            violations.push(`${basename(path)}:${lineOf(source, node.initializer)} uses raw ${node.initializer.text}`)
          }
        } else {
          inspectLiteral(node.initializer, node.initializer.text, false)
        }
      } else if (node.initializer && ts.isJsxExpression(node.initializer) && node.initializer.expression) {
        const expression = node.initializer.expression
        if (ts.isNumericLiteral(expression) && Number(expression.text) < 11) {
          violations.push(`${basename(path)}:${lineOf(source, expression)} uses raw ${expression.text}`)
        } else if (!ts.isNumericLiteral(expression)) {
          violations.push(`${basename(path)}:${lineOf(source, expression)} uses unreviewed JSX expression ${expressionText(source, expression)}`)
        }
      } else {
        violations.push(`${basename(path)}:${lineOf(source, node)} uses an unreviewed JSX font size`)
      }
    }
    ts.forEachChild(node, visit)
  }

  visit(source)
  return violations
}

type OpeningElement = ts.JsxOpeningElement | ts.JsxSelfClosingElement

function nearestOpening(node: ts.Node): OpeningElement | undefined {
  let ancestor: ts.Node | undefined = node.parent
  while (ancestor) {
    if (ts.isJsxOpeningElement(ancestor) || ts.isJsxSelfClosingElement(ancestor)) return ancestor
    ancestor = ancestor.parent
  }
  return undefined
}

function literalAttribute(
  opening: OpeningElement,
  source: ts.SourceFile,
  name: string,
): string | undefined {
  const attribute = opening.attributes.properties.find((candidate): candidate is ts.JsxAttribute =>
    ts.isJsxAttribute(candidate) && candidate.name.getText(source) === name)
  return attribute?.initializer && ts.isStringLiteral(attribute.initializer)
    ? attribute.initializer.text
    : undefined
}

function interactiveAncestor(opening: OpeningElement, source: ts.SourceFile): string | undefined {
  const interactiveTags = new Set(['a', 'button', 'input', 'label', 'option', 'select', 'summary', 'textarea'])
  const passiveRoles = new Set([
    'article', 'cell', 'definition', 'generic', 'group', 'heading', 'img',
    'list', 'listitem', 'main', 'note', 'presentation', 'region', 'row',
    'rowgroup', 'status', 'table', 'term',
  ])
  let ancestor: ts.Node | undefined = opening
  while (ancestor) {
    const candidate = ts.isJsxOpeningElement(ancestor) || ts.isJsxSelfClosingElement(ancestor)
      ? ancestor
      : ts.isJsxElement(ancestor)
        ? ancestor.openingElement
        : undefined
    if (candidate) {
      const tag = candidate.tagName.getText(source)
      if (interactiveTags.has(tag)) return tag
      for (const property of candidate.attributes.properties) {
        if (ts.isJsxSpreadAttribute(property)) return 'unresolved spread props'
        const name = property.name.getText(source)
        if (name === 'role') {
          if (!property.initializer || !ts.isStringLiteral(property.initializer)) {
            return 'unresolved role'
          }
          if (!passiveRoles.has(property.initializer.text)) return `role=${property.initializer.text}`
        }
        if (name.startsWith('on') ||
            ['action', 'contentEditable', 'formAction', 'href', 'tabIndex', 'to'].includes(name)) {
          return name
        }
      }
    }
    ancestor = ancestor.parent
  }
  return undefined
}

function fixtureInteraction(sourceText: string): string | undefined {
  const source = ts.createSourceFile(
    'type-floor-fixture.tsx',
    sourceText,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  )
  let interaction: string | undefined
  const visit = (node: ts.Node) => {
    if (interaction) return
    if (isEvidenceMetadataToken(node as ts.Expression)) {
      const opening = nearestOpening(node)
      interaction = opening && interactiveAncestor(opening, source)
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return interaction
}

function evidenceMetadataLedger(): {
  markers: string[]
  tokens: string[]
  violations: string[]
} {
  const markers: string[] = []
  const tokens: string[] = []
  const violations: string[] = []
  for (const path of sourceFiles(SRC)) {
    const source = ts.createSourceFile(
      path,
      readFileSync(path, 'utf8'),
      ts.ScriptTarget.Latest,
      true,
      path.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
    )
    const file = relative(SRC, path)
    const visit = (node: ts.Node) => {
      if (ts.isJsxAttribute(node) && node.name.getText(source) === 'data-evidence-metadata') {
        if (!node.initializer || !ts.isStringLiteral(node.initializer)) {
          violations.push(`${file}:${lineOf(source, node)} has a non-literal evidence marker`)
        } else {
          markers.push(`${file}#${node.initializer.text}`)
        }
      }
      if (isEvidenceMetadataToken(node as ts.Expression)) {
        const assignment = node.parent
        if (!ts.isPropertyAssignment(assignment) ||
            propertyName(assignment.name) !== 'fontSize' || assignment.initializer !== node) {
          violations.push(`${file}:${lineOf(source, node)} is not a direct fontSize initializer`)
        }
        const opening = nearestOpening(node)
        const marker = opening && literalAttribute(opening, source, 'data-evidence-metadata')
        if (!opening || !marker) {
          violations.push(`${file}:${lineOf(source, node)} has no semantic evidence marker`)
        } else {
          tokens.push(`${file}#${marker}`)
          const interactive = interactiveAncestor(opening, source)
          if (interactive) {
            violations.push(`${file}:${lineOf(source, node)} is inside interactive ${interactive}`)
          }
        }
      }
      ts.forEachChild(node, visit)
    }
    visit(source)
  }
  return {
    markers: markers.sort(),
    tokens: tokens.sort(),
    violations,
  }
}

describe('charter §3 interface type floor', () => {
  it('defines one named 10px evidence-metadata exception below the caption floor', () => {
    expect(TYPE.caption.fontSize).toBe('11px')
    expect(TYPE.evidenceMetadata).toEqual({
      fontSize: '10px',
      lineHeight: '15px',
      fontWeight: 450,
    })
  })

  it('contains no raw sub-11px sizes or unreviewed relative/dynamic forms', () => {
    const violations = sourceFiles(SRC).flatMap(literalFontSizes)
    expect(violations, violations.join('\n')).toEqual([])
  })

  it('keeps raw CSS font declarations inside the same closed contract', () => {
    const violations = styleSourceFiles(SRC).flatMap((path) => {
      const source = readFileSync(path, 'utf8')
      const found: string[] = []
      for (const match of source.matchAll(/font-size\s*:\s*([^;}]+)/g)) {
        const value = match[1].trim()
        if (!isSafeFontSizeLiteral(value)) found.push(`${basename(path)} uses raw CSS font-size:${value}`)
      }
      if (/\bfont\s*:/.test(source)) found.push(`${basename(path)} uses font shorthand`)
      return found
    })
    expect(violations, violations.join('\n')).toEqual([])
  })

  it('retains an exact semantic review ledger for every 10px token declaration', () => {
    const expected = [
      'components/AnalysisScopePanel.tsx#analysis-scope-typed-index-qualifier',
      'pages/CallerComparisonPage.tsx#caller-comparison-coverage-digests',
      'pages/CallerComparisonPage.tsx#caller-comparison-level',
      'pages/CallerMapPage.tsx#caller-map-generation-identifiers',
      'pages/CallerMapPage.tsx#caller-map-source-identifiers',
      'pages/ContractAtlasPage.tsx#contract-atlas-coverage-full-digest',
      'pages/ContractAtlasPage.tsx#contract-atlas-coverage-short-digest',
      'pages/ContractAtlasPage.tsx#contract-atlas-declaration-run',
      'pages/ExactCallerCitation.tsx#exact-caller-citation-identifiers',
      'pages/ImpactPage.tsx#impact-evidence-byte-qualifier',
      'pages/ServiceDirectoryPage.tsx#service-directory-change-summary',
      'pages/SettingsPage.tsx#settings-lifecycle-attempt-time',
    ].sort()
    const ledger = evidenceMetadataLedger()
    expect(ledger.violations, ledger.violations.join('\n')).toEqual([])
    expect(ledger.tokens).toEqual(expected)
    expect(ledger.markers).toEqual(expected)
  })

  it('fails closed when an ancestor role or spread could hide interaction', () => {
    expect(fixtureInteraction(
      "const Fixture = () => <div role={'button'}><span style={{ fontSize: TYPE.evidenceMetadata.fontSize }} /></div>",
    )).toBe('unresolved role')
    expect(fixtureInteraction(
      'const Fixture = () => <div {...{ onClick: () => undefined }}><span style={{ fontSize: TYPE.evidenceMetadata.fontSize }} /></div>',
    )).toBe('unresolved spread props')
    expect(fixtureInteraction(
      'const Fixture = () => <div role="note"><span style={{ fontSize: TYPE.evidenceMetadata.fontSize }} /></div>',
    )).toBeUndefined()
  })

  it('pins the only dynamic interface font size to an explicit 11px clamp', () => {
    const brand = readFileSync(resolve(SRC, 'Brand.tsx'), 'utf8')
    expect(brand).toContain('const safeWordmarkSize = Math.max(11, wordmarkSize)')
    expect(brand.match(/fontSize: `\$\{safeWordmarkSize\}px`/g)).toHaveLength(1)
  })

  it('records the sole decorative SVG exemption as an aria-hidden scaled mark', () => {
    const brand = readFileSync(resolve(SRC, 'Brand.tsx'), 'utf8')
    expect(brand).toMatch(/<svg[^>]*viewBox="0 0 48 36"[^>]*aria-hidden>/)
    expect(brand.match(/fontSize="7"/g)).toHaveLength(1)
  })

  it('prevents visible SVG labels from shrinking below their declared size', () => {
    const map = readFileSync(resolve(SRC, 'components/ContractDependencyMap.tsx'), 'utf8')
    expect(map).toContain('minWidth: `${layout.width}px`')
  })
})
