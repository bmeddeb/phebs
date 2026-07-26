import { useEffect, useMemo, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button } from 'baseui/button'
import { Input } from 'baseui/input'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import { Textarea } from 'baseui/textarea'
import {
  fetchFieldImpact,
  fetchOperationImpact,
  fetchSavedImpact,
  postChangeImpact,
  type CompatibilityFile,
  type ContractImpactReport,
  type ImpactCoverageRow,
  type ImpactEvidenceRow,
} from '../api'
import { ImpactIcon, OpenIcon } from '../icons'
import { SectionHelp } from '../components/SectionHelp'
import { type GlossaryTermId } from '../glossary.generated'
import { FONTS, usePhebsTokens } from '../theme'
import { href, navigate } from '../router'
import { isAbortError } from '../util'

type Mode = 'operation' | 'field' | 'change'

const qualification = 'Build a bounded, evidence-backed report of resolved evidence, matching calls, and extractor abstentions for an operation, field, or contract change.'

export default function ImpactPage({
  params,
  compatibilityAvailable,
  capabilities,
}: {
  params: URLSearchParams
  compatibilityAvailable: boolean
  capabilities: readonly string[]
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [mode, setMode] = useState<Mode>('operation')
  const [operation, setOperation] = useState(params.get('operation') ?? '')
  const [lineage, setLineage] = useState('')
  const [message, setMessage] = useState('')
  const [fieldNumber, setFieldNumber] = useState('')
  const [before, setBefore] = useState('[]')
  const [after, setAfter] = useState('[]')
  const [report, setReport] = useState<ContractImpactReport | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const requestGeneration = useRef(0)
  const activeRequest = useRef<AbortController | null>(null)
  const savedID = params.get('bundle') ?? ''

  useEffect(() => {
    if (!savedID) return
    activeRequest.current?.abort()
    const generation = ++requestGeneration.current
    const controller = new AbortController()
    activeRequest.current = controller
    setLoading(true)
    setError('')
    fetchSavedImpact(savedID, controller.signal)
      .then((result) => {
        if (generation === requestGeneration.current) setReport(result)
      })
      .catch((cause) => {
        if (generation === requestGeneration.current && !isAbortError(cause)) setError(String(cause))
      })
      .finally(() => {
        if (generation === requestGeneration.current) {
          activeRequest.current = null
          setLoading(false)
        }
      })
    return () => controller.abort()
  }, [savedID])

  const selectMode = (next: Mode) => {
    setMode(next)
    setError('')
  }

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    activeRequest.current?.abort()
    const generation = ++requestGeneration.current
    const controller = new AbortController()
    activeRequest.current = controller
    setLoading(true)
    setError('')
    try {
      let result: ContractImpactReport
      if (mode === 'operation') {
        if (!operation.trim()) throw new Error('Enter a canonical /package.Service/Method operation.')
        result = await fetchOperationImpact(operation.trim(), controller.signal)
      } else if (mode === 'field') {
        const number = Number(fieldNumber)
        if (!lineage.trim() || !message.trim() || !Number.isInteger(number) || number <= 0) {
          throw new Error('Enter a lineage, message, and positive field number.')
        }
        result = await fetchFieldImpact(lineage.trim(), message.trim(), number, controller.signal)
      } else {
        if (!compatibilityAvailable) throw new Error('Contract compatibility is unavailable on this server.')
        if (!lineage.trim()) throw new Error('Enter the canonical contract lineage.')
        result = await postChangeImpact({
          lineage: lineage.trim(),
          before: parseFiles(before, 'Before'),
          after: parseFiles(after, 'After'),
        }, controller.signal)
      }
      if (generation !== requestGeneration.current) return
      setReport(result)
      navigate('/impact', { bundle: result.bundle_id })
    } catch (cause) {
      if (generation === requestGeneration.current && !isAbortError(cause)) {
        setError(cause instanceof Error ? cause.message : String(cause))
      }
    } finally {
      if (generation === requestGeneration.current) {
        activeRequest.current = null
        setLoading(false)
      }
    }
  }

  return (
    <div className={css({ maxWidth: '1040px', margin: '0 auto' })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '10px', color: tok.textPrimary })}>
        <ImpactIcon size={20} />
        <h1 className={css({ margin: 0, fontSize: '22px', lineHeight: '30px', fontWeight: 650 })}>Contract impact</h1>
      </div>
      <p className={css({ margin: '6px 0 20px', fontSize: '13px', lineHeight: '20px', color: tok.textTertiary })}>
        {qualification}
      </p>

      <form onSubmit={submit} className={css({ border: `1px solid ${tok.cardBorder}`, marginBottom: '16px' })}>
        <div role="tablist" aria-label="Impact report kind" className={css({ display: 'flex', borderBottom: `1px solid ${tok.cardBorder}`, overflowX: 'auto' })}>
          <ModeTab selected={mode === 'operation'} onClick={() => selectMode('operation')}>Operation</ModeTab>
          <ModeTab selected={mode === 'field'} onClick={() => selectMode('field')}>Field</ModeTab>
          {compatibilityAvailable && <ModeTab selected={mode === 'change'} onClick={() => selectMode('change')}>Contract change</ModeTab>}
        </div>
        <div className={css({ padding: '14px', display: 'grid', gap: '12px' })}>
          {mode === 'operation' && (
            <div className={css({ display: 'flex', gap: '10px', '@media screen and (max-width: 620px)': { flexDirection: 'column' } })}>
              <div className={css({ flex: 1 })}>
                <Input
                  value={operation}
                  onChange={(event) => setOperation(event.currentTarget.value)}
                  aria-label="Canonical operation"
                  placeholder="/package.Service/Method"
                  overrides={{ Input: { style: { fontFamily: FONTS.MONO, fontSize: '13px' } } }}
                />
              </div>
              <Button type="submit" isLoading={loading}>Build report</Button>
            </div>
          )}
          {mode === 'field' && (
            <>
              <div className={css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr) 150px', gap: '10px', '@media screen and (max-width: 720px)': { gridTemplateColumns: '1fr' } })}>
                <Input value={lineage} onChange={(event) => setLineage(event.currentTarget.value)} aria-label="Contract lineage" placeholder="Contract lineage" />
                <Input value={message} onChange={(event) => setMessage(event.currentTarget.value)} aria-label="Message full name" placeholder="package.Message" />
                <Input value={fieldNumber} onChange={(event) => setFieldNumber(event.currentTarget.value)} aria-label="Field number" placeholder="Field number" type="number" />
              </div>
              <div><Button type="submit" isLoading={loading}>Build report</Button></div>
            </>
          )}
          {mode === 'change' && compatibilityAvailable && (
            <>
              <Input value={lineage} onChange={(event) => setLineage(event.currentTarget.value)} aria-label="Contract lineage" placeholder="Canonical contract lineage" />
              <div className={css({ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px', '@media screen and (max-width: 720px)': { gridTemplateColumns: '1fr' } })}>
                <FileInput label="Before files (JSON)" value={before} onChange={setBefore} />
                <FileInput label="After files (JSON)" value={after} onChange={setAfter} />
              </div>
              <div className={css({ display: 'flex', alignItems: 'center', gap: '12px', flexWrap: 'wrap' })}>
                <Button type="submit" isLoading={loading}>Build report</Button>
                <span className={css({ color: tok.textTertiary, fontSize: '12px' })}>Each value is a JSON array of {'{"path":"...proto","content":"..."}'} objects.</span>
              </div>
            </>
          )}
        </div>
      </form>

      {error && (
        <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}>
          {error}
        </Notification>
      )}
      {loading && !report && <Spinner $size="small" />}
      {report && <ReportView report={report} capabilities={capabilities} />}
    </div>
  )
}

function parseFiles(value: string, label: string): CompatibilityFile[] {
  let parsed: unknown
  try {
    parsed = JSON.parse(value)
  } catch {
    throw new Error(`${label} files must be valid JSON.`)
  }
  if (!Array.isArray(parsed) || parsed.length === 0 || parsed.some((file) =>
    typeof file !== 'object' || file === null ||
    typeof (file as Record<string, unknown>).path !== 'string' ||
    typeof (file as Record<string, unknown>).content !== 'string')) {
    throw new Error(`${label} files must be a non-empty array of path/content objects.`)
  }
  return parsed as CompatibilityFile[]
}

function ModeTab({ selected, onClick, children }: { selected: boolean; onClick: () => void; children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <button
      type="button"
      role="tab"
      aria-selected={selected}
      onClick={onClick}
      className={css({
        minHeight: '42px', padding: '0 16px', border: 'none', borderBottom: selected ? `2px solid ${tok.accent}` : '2px solid transparent',
        backgroundColor: 'transparent', color: selected ? tok.accent : tok.textSecondary, fontSize: '13px', fontWeight: selected ? 600 : 450,
        cursor: 'pointer', whiteSpace: 'nowrap', ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill },
        ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
      })}
    >
      {children}
    </button>
  )
}

function FileInput({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css({ display: 'grid', gap: '6px', color: tok.textSecondary, fontSize: '12px', fontWeight: 500 })}>
      {label}
      <Textarea
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        aria-label={label}
        overrides={{ Input: { style: { minHeight: '150px', fontFamily: FONTS.MONO, fontSize: '12px', lineHeight: '18px' } } }}
      />
    </label>
  )
}

function ReportView({
  report,
  capabilities,
}: {
  report: ContractImpactReport
  capabilities: readonly string[]
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const enabledCapabilities = useMemo(() => {
    const enabled = new Set(capabilities)
    if (report.coverage.schema_version === 'coverage-certificate-v1') {
      enabled.add('coverage-certificate')
    }
    return enabled
  }, [capabilities, report.coverage.schema_version])
  const conclusionColor = report.compatibility
    ? report.compatibility.compatible ? tok.statusGreen : tok.statusRed
    : tok.accent
  return (
    <div data-testid="impact-report">
      <section className={css({ border: `1px solid ${conclusionColor}`, padding: '12px 14px', marginBottom: '20px' })}>
        <div className={css({ color: conclusionColor, fontSize: '14px', fontWeight: 600, lineHeight: '20px' })}>{report.conclusion.text}</div>
        <div className={css({ display: 'flex', flexWrap: 'wrap', gap: '4px 16px', minWidth: 0, marginTop: '5px', fontSize: '11px', lineHeight: '17px', color: tok.textTertiary, fontFamily: FONTS.MONO })}>
          <span className={css({ minWidth: 0, overflowWrap: 'anywhere' })}>bundle {report.bundle_id}</span>
          <span className={css({ minWidth: 0, overflowWrap: 'anywhere' })}>coverage {report.conclusion.coverage_digest}</span>
        </div>
      </section>

      {report.compatibility && <CompatibilitySection report={report} />}
      <EvidenceSection
        title="Resolved evidence"
        rows={report.resolved_evidence}
        empty="No declaration-resolved or field-reference evidence was found within the stated evidence scope; this does not establish absence."
        enabledCapabilities={enabledCapabilities}
      />
      <EvidenceSection
        title="Matching call evidence"
        termId="matching_static_evidence"
        rows={report.matching_call_evidence}
        empty="No operation-name call evidence was found within the stated evidence scope; this does not establish absence."
        enabledCapabilities={enabledCapabilities}
      />
      <EvidenceSection
        title="Extractor abstentions"
        termId="could_not_resolve"
        rows={report.extractor_abstentions}
        empty="No extractor abstention evidence was recorded within the stated evidence scope."
        enabledCapabilities={enabledCapabilities}
        unresolved
      />
      <CoverageSection report={report} enabledCapabilities={enabledCapabilities} />

      <div className={css({ border: `1px solid ${tok.cardBorder}`, padding: '12px 14px', marginTop: '20px', fontSize: '12px', lineHeight: '18px', color: tok.textSecondary })}>
        {report.caveat}
      </div>
    </div>
  )
}

function CompatibilitySection({ report }: { report: ContractImpactReport }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const result = report.compatibility
  if (!result) return null
  return (
    <section className={css({ marginBottom: '22px' })}>
      <SectionHeading>Compatibility classification</SectionHeading>
      <div className={css({ fontSize: '13px', color: result.compatible ? tok.statusGreen : tok.statusRed, marginBottom: '8px' })}>
        Buf {result.extraction_run.policy}: {result.compatible ? 'compatible for the committed inputs' : 'breaking findings for the committed inputs'}
      </div>
      {result.violations.length > 0 && (
        <div className={css({ overflowX: 'auto' })}>
          <table className={css(tableStyle())}>
            <thead><tr>{['Input span', 'Rule', 'Finding', 'Affected field'].map((heading) => <HeaderCell key={heading}>{heading}</HeaderCell>)}</tr></thead>
            <tbody>
              {result.violations.map((violation, index) => (
                <tr key={`${violation.path}:${violation.start_line}:${violation.rule}:${index}`} className={css({ borderBottom: `1px solid ${tok.innerSep}` })}>
                  <Cell mono>{violation.snapshot}/{violation.path}:{violation.start_line}</Cell>
                  <Cell mono>{violation.rule}</Cell>
                  <Cell>{violation.message}</Cell>
                  <Cell mono>{violation.affected_field ? `${violation.affected_field.message}#${violation.affected_field.field_number}` : '—'}</Cell>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function EvidenceSection({
  title,
  termId,
  rows,
  empty,
  enabledCapabilities,
  unresolved = false,
}: {
  title: string
  termId?: GlossaryTermId
  rows: ImpactEvidenceRow[]
  empty: string
  enabledCapabilities: ReadonlySet<string>
  unresolved?: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section className={css({ marginBottom: '22px' })}>
      <SectionHeading termId={termId} enabledCapabilities={enabledCapabilities}>{title}</SectionHeading>
      {rows.length === 0 ? (
        <div className={css({ fontSize: '13px', lineHeight: '20px', color: tok.textTertiary, padding: '4px 0' })}>{empty}</div>
      ) : (
        <div className={css({ overflowX: 'auto' })}>
          <table className={css(tableStyle())}>
            <thead><tr>{['Pinned evidence', 'Protocol / domain', unresolved ? 'Reason unresolved' : 'Classification', 'Tier', 'Code role', 'Freshness'].map((heading) => <HeaderCell key={heading}>{heading}</HeaderCell>)}</tr></thead>
            <tbody>
              {rows.map((row) => <EvidenceRow key={`${row.domain}:${row.assertion_id}:${row.evidence_atom_id}:${row.repository}:${row.path}:${row.start_line}`} row={row} unresolved={unresolved} />)}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function EvidenceRow({ row, unresolved }: { row: ImpactEvidenceRow; unresolved: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const sourceHref = href('/file', { repo: row.repository, path: row.path, ref: row.commit, L: String(row.start_line) })
  return (
    <tr className={css({ borderBottom: `1px solid ${tok.innerSep}`, ':hover': { backgroundColor: tok.hoverFill } })}>
      <Cell>
        <a href={sourceHref} className={css({ display: 'inline-flex', alignItems: 'center', gap: '5px', color: tok.accent, textDecoration: 'none', ':hover': { textDecoration: 'underline' } })}>
          <span>{row.repository}/{row.path}:{row.start_line}{row.end_line !== row.start_line ? `–${row.end_line}` : ''}</span>
          <OpenIcon size={13} />
        </a>
        <div className={css({ marginTop: '3px', fontFamily: FONTS.MONO, fontSize: '10px', color: tok.gutter })}>
          {shortCommit(row.commit)} · bytes {row.start_byte}–{row.end_byte} · {row.evidence_atom_id}
        </div>
      </Cell>
      <Cell>
        <div>{row.protocol || 'unknown'}</div>
        <div className={css({ marginTop: '2px', color: tok.textTertiary, fontFamily: FONTS.MONO, fontSize: '10px' })}>
          {row.domain}
        </div>
      </Cell>
      <Cell>{unresolved ? row.reason || row.classification : [row.classification, row.reason].filter(Boolean).join(' · ')}</Cell>
      <Cell mono>{row.tier}</Cell>
      <Cell>{row.code_role || '—'}</Cell>
      <Cell><span className={css({ color: row.fresh ? tok.statusGreen : tok.statusAmber })}>{row.fresh ? 'current' : 'stale'}</span></Cell>
    </tr>
  )
}

function CoverageSection({
  report,
  enabledCapabilities,
}: {
  report: ContractImpactReport
  enabledCapabilities: ReadonlySet<string>
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const stateCounts = new Map<string, number>()
  for (const row of report.coverage_rows) {
    stateCounts.set(row.state, (stateCounts.get(row.state) ?? 0) + 1)
  }
  const stateSummary = [...stateCounts.entries()]
    .map(([state, count]) => `${count} ${stateLabel(state)}`)
    .join(' · ')
  return (
    <section>
      <SectionHeading
        termId="analysis_scope_and_gaps"
        enabledCapabilities={enabledCapabilities}
      >
        Analysis scope &amp; gaps
      </SectionHeading>
      <div className={css({
        fontSize: '12px',
        lineHeight: '18px',
        color: tok.textTertiary,
        margin: '-4px 0 10px',
      })}>
        {report.coverage.repository_count} visible repositories · {report.coverage.domains.join(', ')}
        {stateSummary ? ` · ${stateSummary}` : ''}
      </div>
      <div className={css({
        marginBottom: '12px',
        color: tok.textSecondary,
        fontSize: '13px',
        lineHeight: '20px',
      })}>
        These states qualify the adjacent evidence. They are not an accuracy,
        completeness, runtime-use, or safe-to-change score.
      </div>
      <details
        data-testid="coverage-certificate-detail"
        className={css({
          border: `1px solid ${tok.cardBorder}`,
          color: tok.textTertiary,
          fontSize: '12px',
        })}
      >
        <summary className={css({
          minHeight: '40px',
          boxSizing: 'border-box',
          padding: '8px 10px',
          display: 'flex',
          alignItems: 'center',
          gap: '8px',
          cursor: 'pointer',
          color: tok.textSecondary,
          ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill },
          ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' },
        })}>
          <span className={css({ fontWeight: 650 })}>Coverage certificate</span>
          <SectionHelp
            termId="coverage_certificate"
            enabledCapabilities={enabledCapabilities}
          />
          <span className={css({
            minWidth: 0,
            marginLeft: 'auto',
            overflowWrap: 'anywhere',
            textAlign: 'right',
            fontFamily: FONTS.MONO,
            fontSize: '10px',
            color: tok.gutter,
            '@media screen and (max-width: 620px)': {
              display: 'none',
            },
          })}>
            {report.coverage.digest}
          </span>
        </summary>
        <div className={css({ padding: '10px', borderTop: `1px solid ${tok.cardBorder}` })}>
          <div className={css({ overflowX: 'auto' })}>
            <table className={css(tableStyle())}>
              <thead><tr>{['Repository / domain', 'State', 'Indexed / evidence revision', 'Assertions', 'Unresolved', 'Failure / unsupported reason'].map((heading) => <HeaderCell key={heading}>{heading}</HeaderCell>)}</tr></thead>
              <tbody>{report.coverage_rows.map((row) => <CoverageRow key={`${row.repository}:${row.domain}`} row={row} />)}</tbody>
            </table>
          </div>
          <details className={css({ marginTop: '10px' })}>
            <summary className={css({ cursor: 'pointer', ':hover': { color: tok.textPrimary } })}>Canonical certificate JSON</summary>
            <pre className={css({ margin: '8px 0 0', padding: '12px', overflowX: 'auto', backgroundColor: tok.bandBg, color: tok.textSecondary, fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '17px', border: `1px solid ${tok.cardBorder}` })}>
              {JSON.stringify(report.coverage, null, 2)}
            </pre>
          </details>
        </div>
      </details>
    </section>
  )
}

function CoverageRow({ row }: { row: ImpactCoverageRow }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const stateColor = row.state === 'covered' ? tok.statusGreen
    : row.state === 'stale' || row.state === 'processing' || row.state === 'covered_with_failed_replacement' ? tok.statusAmber
      : tok.statusRed
  return (
    <tr className={css({ borderBottom: `1px solid ${tok.innerSep}` })}>
      <Cell><div>{row.repository}</div><div className={css({ color: tok.textTertiary, marginTop: '2px' })}>{row.domain}</div></Cell>
      <Cell><span className={css({ color: stateColor })}>{stateLabel(row.state)}</span></Cell>
      <Cell mono>{shortCommit(row.indexed_commit)} / {shortCommit(row.evidence_commit)}</Cell>
      <Cell mono>{row.assertion_count}</Cell>
      <Cell mono>{row.unresolved_count}</Cell>
      <Cell>{row.reason || row.failures?.join('; ') || '—'}</Cell>
    </tr>
  )
}

function SectionHeading({
  children,
  termId,
  enabledCapabilities,
}: {
  children?: React.ReactNode
  termId?: GlossaryTermId
  enabledCapabilities?: ReadonlySet<string>
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({
      minHeight: '22px',
      margin: '0 0 10px',
      display: 'flex',
      alignItems: 'center',
      gap: '7px',
    })}>
      <h2 className={css({
        margin: 0,
        color: tok.textPrimary,
        fontSize: '15px',
        lineHeight: '22px',
        fontWeight: 650,
      })}>
        {children}
      </h2>
      {termId && enabledCapabilities ? (
        <SectionHelp termId={termId} enabledCapabilities={enabledCapabilities} />
      ) : null}
    </div>
  )
}

function HeaderCell({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <th className={css({ textAlign: 'left', padding: '0 14px 8px 0', color: tok.textTertiary, fontFamily: FONTS.MONO, fontSize: '10px', lineHeight: '15px', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.03em', whiteSpace: 'nowrap', borderBottom: `1px solid ${tok.cardBorder}` })}>{children}</th>
}

function Cell({ children, mono = false }: { children: React.ReactNode; mono?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <td className={css({ padding: '10px 14px 10px 0', color: tok.textSecondary, fontFamily: mono ? FONTS.MONO : undefined, fontSize: mono ? '11px' : '12px', lineHeight: '18px', verticalAlign: 'top' })}>{children}</td>
}

function tableStyle() {
  return { width: '100%', minWidth: '760px', borderCollapse: 'collapse' as const, tableLayout: 'auto' as const }
}

function shortCommit(value?: string): string {
  return value ? value.slice(0, 8) : '—'
}

function stateLabel(state: string): string {
  return state.replaceAll('_', ' ')
}
