import { type FormEvent, useEffect, useMemo, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import {
  fetchServiceRelationshipCitation,
  fetchServiceRelationships,
  type ServiceRelationshipCitation,
  type ServiceRelationshipPage,
  type ServiceRelationshipPlacement,
  type ServiceRelationshipRow,
  type ServiceRelationshipView,
} from '../api'
import { href, navigate } from '../router'
import { FONTS, usePhebsTokens, type PhebsTokens } from '../theme'
import { isAbortError } from '../util'

const PAGE_SIZE = 50
const PAGE_SCHEMA = 'phebs-service-relationship-page-v1'
const CITATION_SCHEMA = 'phebs-service-relationship-citation-v1'

type Direction = 'all' | 'uses' | 'provided' | 'produces' | 'consumes'
type EvidenceKind = 'all' | 'rpc' | 'kafka'

interface ExplorerRoute {
  serviceKey: string
  repository: string
  direction: Direction
  evidence: EvidenceKind
  lookupKey: string
  cursor: string
  diagram: boolean
}

interface DraftFilters {
  serviceKey: string
  repository: string
  direction: Direction
  evidence: EvidenceKind
  lookupKey: string
}

interface RelationshipRequest {
  repository?: string
  service_key: string
  view: ServiceRelationshipView
  kind?: 'rpc' | 'kafka'
  plane?: string
  lookup_key?: string
  page_size: number
  cursor?: string
}

interface CitationState {
  row: ServiceRelationshipRow
  value?: ServiceRelationshipCitation
  error?: string
  loading: boolean
}

export default function RelationshipExplorerPage({ params }: { params: URLSearchParams }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const route = useMemo(() => explorerRoute(params), [params])
  const queryRoute = useMemo((): ExplorerRoute => ({
    serviceKey: route.serviceKey,
    repository: route.repository,
    direction: route.direction,
    evidence: route.evidence,
    lookupKey: route.lookupKey,
    cursor: route.cursor,
    diagram: false,
  }), [
    route.serviceKey,
    route.repository,
    route.direction,
    route.evidence,
    route.lookupKey,
    route.cursor,
  ])
  const [draft, setDraft] = useState<DraftFilters>(() => draftFromRoute(route))
  const [page, setPage] = useState<ServiceRelationshipPage | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [citation, setCitation] = useState<CitationState | null>(null)
  const citationController = useRef<AbortController | null>(null)

  useEffect(() => setDraft(draftFromRoute(route)), [route])

  useEffect(() => {
    citationController.current?.abort()
    citationController.current = null
    setCitation(null)
    if (!queryRoute.serviceKey) {
      setPage(null)
      setError('')
      setLoading(false)
      return
    }
    const controller = new AbortController()
    const request = relationshipRequest(queryRoute)
    setPage(null)
    setError('')
    setLoading(true)
    void fetchServiceRelationships(request, controller.signal)
      .then((value) => {
        if (controller.signal.aborted) return
        const responseError = validatePage(queryRoute, request, value)
        if (responseError) setError(responseError)
        else setPage(value)
      })
      .catch((cause) => {
        if (!controller.signal.aborted && !isAbortError(cause)) setError(boundedError(cause))
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [queryRoute])

  useEffect(() => () => citationController.current?.abort(), [])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const serviceKey = draft.serviceKey.trim()
    const repository = draft.repository.trim()
    const lookupKey = draft.lookupKey.trim()
    if (!serviceKey || serviceKey.length > 128 || repository.length > 4096 || lookupKey.length > 4096) {
      setError('service key is required and every filter must remain within its admitted bound')
      return
    }
    navigate('/relationships', explorerParams({
      ...route,
      serviceKey,
      repository,
      direction: draft.direction,
      evidence: draft.evidence,
      lookupKey,
      cursor: '',
    }))
  }

  const toggleDiagram = () => navigate('/relationships', explorerParams({ ...route, diagram: !route.diagram }))

  const openCitation = (row: ServiceRelationshipRow) => {
    if (!page) return
    const root = page.roots.find((candidate) => candidate.repository === row.repository)
    if (!root) {
      setCitation({ row, error: 'citation root is absent from the exact response', loading: false })
      return
    }
    citationController.current?.abort()
    const controller = new AbortController()
    citationController.current = controller
    setCitation({ row, loading: true })
    void fetchServiceRelationshipCitation(row.citation, controller.signal)
      .then((value) => {
        if (controller.signal.aborted) return
        const responseError = validateCitation(row, root, value)
        setCitation(responseError
          ? { row, error: responseError, loading: false }
          : { row, value, loading: false })
      })
      .catch((cause) => {
        if (!controller.signal.aborted && !isAbortError(cause)) {
          setCitation({ row, error: boundedError(cause), loading: false })
        }
      })
      .finally(() => {
        if (citationController.current === controller) citationController.current = null
      })
  }

  const closeCitation = () => {
    citationController.current?.abort()
    citationController.current = null
    setCitation(null)
  }

  return (
    <section aria-labelledby="relationship-explorer-title">
      <div className={css({ marginBottom: '16px' })}>
        <a href="#/repos" className={css(breadcrumb(tok))}>Repositories</a>
        <span aria-hidden="true" className={css({ margin: '0 8px', color: tok.gutter })}>/</span>
        <span className={css({ fontFamily: FONTS.MONO, fontSize: '11px', color: tok.textTertiary })}>relationships</span>
      </div>

      <div className={css({ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: '18px', flexWrap: 'wrap', marginBottom: '18px' })}>
        <div>
          <h1 id="relationship-explorer-title" className={css({ margin: 0, color: tok.textPrimary, fontSize: '27px', lineHeight: '34px', letterSpacing: '-0.025em' })}>Relationship explorer</h1>
          <p className={css({ margin: '5px 0 0', color: tok.textTertiary, fontSize: '12px', lineHeight: '18px' })}>Exact static source relationships · source evidence first</p>
        </div>
        <label className={css({ display: 'inline-flex', alignItems: 'center', gap: '7px', minHeight: '32px', color: tok.textSecondary, fontSize: '11px', cursor: route.serviceKey && page ? 'pointer' : 'default' })}>
          <input type="checkbox" checked={route.diagram} disabled={!route.serviceKey || !page} onChange={toggleDiagram} />
          Show page diagram
        </label>
      </div>

      <details open className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '7px', marginBottom: '10px', backgroundColor: tok.pageBg })}>
        <summary className={css({ display: 'none', padding: '12px 14px', cursor: 'pointer', color: tok.textPrimary, fontSize: '13px', fontWeight: 600, ':focus-visible': focusRing(tok), '@media screen and (max-width: 720px)': { display: 'list-item' } })}>Filters</summary>
        <form onSubmit={submit} className={css({ padding: '14px 16px 16px', display: 'grid', gridTemplateColumns: 'minmax(150px, 0.85fr) minmax(180px, 1fr) minmax(180px, 1.25fr) minmax(140px, 0.75fr) minmax(180px, 1fr) auto', gap: '12px', alignItems: 'end', '@media screen and (max-width: 1120px)': { gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' }, '@media screen and (max-width: 720px)': { gridTemplateColumns: '1fr', padding: '12px' } })}>
          <FilterField label="Service key" required>
            <input aria-label="Service key" required maxLength={128} value={draft.serviceKey} onChange={(event) => setDraft({ ...draft, serviceKey: event.target.value })} className={css(inputStyle(tok))} placeholder="orders-api" />
          </FilterField>
          <FilterField label="Repository">
            <input aria-label="Repository" maxLength={4096} value={draft.repository} onChange={(event) => setDraft({ ...draft, repository: event.target.value })} className={css(inputStyle(tok))} placeholder="All visible repositories" />
          </FilterField>
          <FilterField label="Direction">
            <select aria-label="Direction" value={draft.direction} onChange={(event) => setDraft({ ...draft, direction: event.target.value as Direction })} className={css(inputStyle(tok))}>
              <option value="all">All directions</option>
              <option value="uses">Uses / dependencies</option>
              <option value="provided">Provided / callers</option>
              <option value="produces">Produces topics</option>
              <option value="consumes">Consumes topics</option>
            </select>
          </FilterField>
          <FilterField label="Evidence">
            <select aria-label="Evidence" value={draft.evidence} onChange={(event) => setDraft({ ...draft, evidence: event.target.value as EvidenceKind })} className={css(inputStyle(tok))}>
              <option value="all">All evidence</option>
              <option value="rpc">RPC</option>
              <option value="kafka">Kafka</option>
            </select>
          </FilterField>
          <FilterField label="Contract or topic">
            <input aria-label="Contract or topic" maxLength={4096} value={draft.lookupKey} onChange={(event) => setDraft({ ...draft, lookupKey: event.target.value })} className={css(inputStyle(tok))} placeholder="Exact, optional" />
          </FilterField>
          <div className={css({ display: 'flex', alignItems: 'center', gap: '10px', '@media screen and (max-width: 720px)': { flexDirection: 'column', alignItems: 'stretch' } })}>
            <button type="submit" className={css(primaryButton(tok))}>Apply filters</button>
            <a href="#/relationships" className={css(resetLink(tok))}>Reset</a>
          </div>
        </form>
      </details>

      {!route.serviceKey ? (
        <div className={css({ border: `1px dashed ${tok.cardBorder}`, borderRadius: '7px', padding: '40px 18px', color: tok.textTertiary, fontSize: '12px', lineHeight: '19px' })}>
          Enter one exact repository-scoped service key. Leave repository blank to use the authorization-first visible-repository fallback.
        </div>
      ) : loading ? (
        <div role="status" aria-live="polite" className={css(statusBox(tok))}>Reading exact relationship roots and bounded source rows…</div>
      ) : error ? (
        <div role="alert" className={css({ ...statusBox(tok), color: tok.statusRed })}>{error}</div>
      ) : page ? (
        <>
          <CoverageStrip page={page} />
          <div className={css({ display: 'grid', gridTemplateColumns: route.diagram ? 'minmax(0, 3fr) minmax(280px, 1fr)' : 'minmax(0, 1fr)', gap: '10px', alignItems: 'start', '@media screen and (max-width: 980px)': { gridTemplateColumns: '1fr' } })}>
            <ExactRows page={page} onCitation={openCitation} />
            {route.diagram && <PageDiagram page={page} />}
          </div>
          <PageNavigation route={route} page={page} />
        </>
      ) : null}

      {citation && <CitationPanel state={citation} onClose={closeCitation} />}

      <div className={css({ marginTop: '12px', padding: '12px 14px', border: `1px dashed ${tok.cardBorder}`, borderRadius: '7px', color: tok.textTertiary, fontSize: '10.5px', lineHeight: '17px' })}>
        Static source evidence only. The table is authoritative for this exact page; the optional diagram adds no edge, transitivity, runtime topology, completeness, migration, or release claim.
      </div>
    </section>
  )
}

function FilterField({ label, required = false, children }: { label: string; required?: boolean; children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css({ display: 'grid', gap: '5px', minWidth: 0, color: tok.textSecondary, fontSize: '10.5px', lineHeight: '15px' })}>
      <span>{label}{required && <span aria-hidden="true"> · required</span>}</span>
      {children}
    </label>
  )
}

function CoverageStrip({ page }: { page: ServiceRelationshipPage }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const gaps = page.coverage.failed_roots + page.coverage.unavailable_roots
  const partial = page.coverage.truncated || gaps > 0
  const metrics = [
    ['Authorized repositories', page.coverage.authorized_repositories],
    ['Complete roots', page.coverage.complete_roots],
    ['Empty roots', page.coverage.empty_roots],
    ['Gaps', gaps],
    ['Scanned references', page.coverage.scanned_references],
    ['Returned rows', page.coverage.returned_rows],
  ] as const
  return (
    <section aria-label="Relationship coverage" className={css({ display: 'flex', alignItems: 'stretch', flexWrap: 'wrap', border: `1px solid ${tok.cardBorder}`, borderRadius: '7px', marginBottom: '10px', overflow: 'hidden', '@media screen and (max-width: 720px)': { display: 'grid', gridTemplateColumns: '1fr 1fr' } })}>
      {metrics.map(([label, value]) => (
        <div key={label} className={css({ minHeight: '48px', padding: '8px 14px', display: 'flex', alignItems: 'center', gap: '9px', borderRight: `1px solid ${tok.innerSep}`, fontSize: '10.5px', color: tok.textTertiary, '@media screen and (max-width: 720px)': { minWidth: 0, padding: '8px 10px', justifyContent: 'space-between', borderBottom: `1px solid ${tok.innerSep}` } })}>
          <span>{label}</span><strong className={css({ color: tok.textPrimary, fontSize: '15px', fontVariantNumeric: 'tabular-nums' })}>{value}</strong>
        </div>
      ))}
      <div className={css({ flex: '1 1 260px', minHeight: '48px', padding: '8px 14px', display: 'flex', alignItems: 'center', color: partial ? tok.statusAmber : tok.textTertiary, fontSize: '10.5px', lineHeight: '16px', '@media screen and (max-width: 720px)': { gridColumn: '1 / -1' } })}>
        {partial
          ? `Partial · ${page.coverage.truncated ? 'reference admission cap reached' : `${gaps} failed or unavailable root${gaps === 1 ? '' : 's'}`}`
          : 'Bounded exact roots · completeness beyond the published authority is not claimed'}
      </div>
    </section>
  )
}

function ExactRows({ page, onCitation }: { page: ServiceRelationshipPage; onCitation: (row: ServiceRelationshipRow) => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (page.rows_state === 'gap') {
    return <div className={css(statusBox(tok))}>One or more requested relationship roots are gaps. No empty result is inferred.</div>
  }
  if (page.rows.length === 0) {
    return <div className={css(statusBox(tok))}>Exact empty page for these filters. Root gaps and admitted truncation remain reported above.</div>
  }
  return (
    <section aria-labelledby="exact-relationship-rows" className={css({ minWidth: 0, border: `1px solid ${tok.cardBorder}`, borderRadius: '7px', overflow: 'hidden' })}>
      <h2 id="exact-relationship-rows" className={css({ margin: 0, padding: '10px 12px', backgroundColor: tok.bandBg, borderBottom: `1px solid ${tok.cardBorder}`, color: tok.textPrimary, fontSize: '12px', lineHeight: '17px' })}>Exact source rows</h2>
      <div role="region" aria-label="Exact relationship source table" className={css({ overflowX: 'auto', '@media screen and (max-width: 720px)': { display: 'none' } })}>
        <table className={css({ width: '100%', minWidth: '920px', borderCollapse: 'collapse', tableLayout: 'fixed' })}>
          <thead><tr>{['ID', 'Evidence', 'Contract / topic', 'Service route', 'Attribution', 'Classification', 'Citation'].map((label, index) => <th key={label} scope="col" className={css({ ...tableHead(tok), width: ['7%', '20%', '17%', '18%', '18%', '11%', '9%'][index] })}>{label}</th>)}</tr></thead>
          <tbody>{page.rows.map((row, index) => <DesktopRow key={`${row.repository}:${row.projection_digest}`} id={rowID(index)} row={row} onCitation={onCitation} />)}</tbody>
        </table>
      </div>
      <ol aria-label="Exact relationship source rows" className={css({ display: 'none', margin: 0, padding: 0, listStyle: 'none', '@media screen and (max-width: 720px)': { display: 'block' } })}>
        {page.rows.map((row, index) => <MobileRow key={`${row.repository}:${row.projection_digest}`} id={rowID(index)} row={row} onCitation={onCitation} />)}
      </ol>
    </section>
  )
}

function DesktopRow({ id, row, onCitation }: { id: string; row: ServiceRelationshipRow; onCitation: (row: ServiceRelationshipRow) => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const route = exactRoute(row)
  return (
    <tr id={`${id}-desktop`} className={css({ borderTop: `1px solid ${tok.innerSep}`, scrollMarginTop: '70px', ':hover': { backgroundColor: tok.hoverFill } })}>
      <td className={css(tableCell(tok))}><span className={css(idBadge(tok))}>{id}</span></td>
      <td className={css(tableCell(tok))}><EvidenceCell row={row} /></td>
      <td className={css(tableCell(tok))}><SubjectCell row={row} /></td>
      <td className={css(tableCell(tok))}><RouteCell route={route} /></td>
      <td className={css(tableCell(tok))}><AttributionCell row={row} /></td>
      <td className={css(tableCell(tok))}><ClassificationCell row={row} /></td>
      <td className={css(tableCell(tok))}><CitationButton row={row} onCitation={onCitation} /></td>
    </tr>
  )
}

function MobileRow({ id, row, onCitation }: { id: string; row: ServiceRelationshipRow; onCitation: (row: ServiceRelationshipRow) => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const route = exactRoute(row)
  return (
    <li id={id} className={css({ padding: '13px 12px', borderTop: `1px solid ${tok.innerSep}`, scrollMarginTop: '70px' })}>
      <div className={css({ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: '12px' })}>
        <span className={css(idBadge(tok))}>{id}</span>
        <ClassificationCell row={row} />
      </div>
      <div className={css({ marginTop: '8px' })}><EvidenceCell row={row} /></div>
      <div className={css({ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px', marginTop: '10px', '@media screen and (max-width: 430px)': { gridTemplateColumns: '1fr' } })}>
        <div><MobileLabel>Contract / topic</MobileLabel><SubjectCell row={row} /></div>
        <div><MobileLabel>Exact route</MobileLabel><RouteCell route={route} /></div>
      </div>
      <details className={css({ marginTop: '9px' })}><summary className={css({ color: tok.textSecondary, fontSize: '10.5px', cursor: 'pointer', ':focus-visible': focusRing(tok) })}>Attribution claims</summary><div className={css({ marginTop: '6px' })}><AttributionCell row={row} /></div></details>
      <div className={css({ marginTop: '11px' })}><CitationButton row={row} onCitation={onCitation} /></div>
    </li>
  )
}

function EvidenceCell({ row }: { row: ServiceRelationshipRow }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <><code className={css(codeWrap(tok))}>{row.evidence.path}</code><div className={css(meta(tok))}>lines {row.evidence.span.start_line}–{row.evidence.span.end_line} · {row.evidence.source_role} · {row.repository}</div></>
}

function SubjectCell({ row }: { row: ServiceRelationshipRow }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const subject = row.evidence.operation || row.evidence.topic_spelling || row.lookup_key || 'No resolved identity'
  return <><div className={css({ color: tok.textPrimary, fontSize: '11px', lineHeight: '16px', overflowWrap: 'anywhere' })}>{subject}</div><div className={css(meta(tok))}>{row.kind} · {row.plane}</div></>
}

interface ExactRoute { from: string; to: string; posture: string }

function RouteCell({ route }: { route: ExactRoute }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <div className={css({ fontFamily: FONTS.MONO, fontSize: '10px', lineHeight: '16px', color: tok.textSecondary, overflowWrap: 'anywhere' })}><strong className={css({ color: tok.textPrimary })}>{route.from}</strong><span aria-hidden="true"> → </span><strong className={css({ color: tok.textPrimary })}>{route.to}</strong><div className={css(meta(tok))}>{route.posture}</div></div>
}

function AttributionCell({ row }: { row: ServiceRelationshipRow }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const placements = [row.source, row.target].filter((value): value is ServiceRelationshipPlacement => Boolean(value))
  return <div className={css({ display: 'grid', gap: '5px' })}>{placements.map((placement) => <Placement key={placement.path} value={placement} />)}{placements.length === 0 && <span className={css(meta(tok))}>No catalog placement</span>}</div>
}

function Placement({ value }: { value: ServiceRelationshipPlacement }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <div><code className={css(codeWrap(tok))}>{value.path}</code><div className={css(meta(tok))}>{value.unowned ? 'unowned' : value.claims.length === 0 ? 'no claims' : value.claims.map((claim) => `${claim.service_key}:${claim.disposition}[${claim.roles.map((role) => `${role.role}/${role.origin}`).join(',')}]`).join(' · ')}</div></div>
}

function ClassificationCell({ row }: { row: ServiceRelationshipRow }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const label = classificationLabel(row)
  const color = label === 'Ambiguous' ? tok.statusRed : label === 'Unowned' || label === 'Unsupported' || label === 'Unresolved' ? tok.statusAmber : tok.textSecondary
  return <div><span className={css({ color, fontSize: '10.5px', lineHeight: '15px', fontWeight: 600 })}>{label}</span><div className={css(meta(tok))}>{row.participation.join(' + ') || 'unattributed'}{row.evidence.reason ? ` · ${row.evidence.reason}` : ''}</div></div>
}

function CitationButton({ row, onCitation }: { row: ServiceRelationshipRow; onCitation: (row: ServiceRelationshipRow) => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <button type="button" aria-haspopup="dialog" onClick={() => onCitation(row)} className={css(citationButton(tok))}>View citation</button>
}

function PageDiagram({ page }: { page: ServiceRelationshipPage }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <figure aria-labelledby="page-diagram-title" className={css({ margin: 0, border: `1px solid ${tok.cardBorder}`, borderRadius: '7px', overflow: 'hidden' })}>
      <figcaption id="page-diagram-title" className={css({ padding: '10px 12px', borderBottom: `1px solid ${tok.cardBorder}`, backgroundColor: tok.bandBg, color: tok.textSecondary, fontSize: '10.5px', lineHeight: '16px' })}>Current page only · table remains authoritative</figcaption>
      <ol className={css({ margin: 0, padding: 0, listStyle: 'none' })}>
        {page.rows.map((row, index) => {
          const route = exactRoute(row)
          const id = rowID(index)
          return (
            <li key={`${row.repository}:${row.projection_digest}`} className={css({ display: 'grid', gridTemplateColumns: '42px minmax(0, 1fr) 32px minmax(0, 1fr)', alignItems: 'center', gap: '6px', minHeight: '70px', padding: '8px 10px', borderTop: index === 0 ? 'none' : `1px solid ${tok.innerSep}` })}>
              <button
                type="button"
                aria-label={`Scroll to source row ${id}`}
                onClick={() => {
                  // The desktop table and mobile list both render the row; scroll the visible one.
                  const target = [document.getElementById(`${id}-desktop`), document.getElementById(id)]
                    .find((el): el is HTMLElement => el !== null && el.offsetParent !== null)
                  target?.scrollIntoView({ block: 'center' })
                }}
                className={css({ ...idLink(tok), border: 0, padding: 0, background: 'transparent', cursor: 'pointer', textAlign: 'left' })}
              >{id}</button>
              <DiagramNode>{route.from}</DiagramNode>
              <span aria-label={route.posture} title={route.posture} className={css({ textAlign: 'center', color: tok.textTertiary, fontSize: '16px' })}>→</span>
              <DiagramNode>{route.to}</DiagramNode>
            </li>
          )
        })}
      </ol>
    </figure>
  )
}

function DiagramNode({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <span className={css({ minWidth: 0, padding: '7px 6px', border: `1px solid ${tok.accent}`, borderRadius: '4px', color: tok.textPrimary, fontFamily: FONTS.MONO, fontSize: '9.5px', lineHeight: '14px', textAlign: 'center', overflowWrap: 'anywhere' })}>{children}</span>
}

function PageNavigation({ route, page }: { route: ExplorerRoute; page: ServiceRelationshipPage }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (!route.cursor && !page.pagination.next_cursor) return null
  return (
    <nav aria-label="Relationship pages" className={css({ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: '12px', marginTop: '10px' })}>
      {route.cursor ? <a href={explorerHref({ ...route, cursor: '' })} className={css(secondaryLink(tok))}>First exact page</a> : <span />}
      {page.pagination.next_cursor && <a href={explorerHref({ ...route, cursor: page.pagination.next_cursor })} className={css(primaryLink(tok))}>Next exact page</a>}
    </nav>
  )
}

function CitationPanel({ state, onClose }: { state: CitationState; onClose: () => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <aside role="dialog" aria-modal="false" aria-labelledby="explorer-citation-title" className={css({ marginTop: '10px', border: `1px solid ${tok.cardBorder}`, borderRadius: '7px', padding: '14px', backgroundColor: tok.bandBg })}>
      <div className={css({ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: '14px' })}>
        <div><h2 id="explorer-citation-title" className={css({ margin: 0, color: tok.textPrimary, fontSize: '12px', lineHeight: '17px' })}>Exact source citation</h2><div className={css(meta(tok))}>{state.row.evidence.path} · lines {state.row.evidence.span.start_line}–{state.row.evidence.span.end_line}</div></div>
        <button type="button" onClick={onClose} className={css(citationButton(tok))}>Close citation</button>
      </div>
      {state.loading && <div role="status" className={css({ marginTop: '10px', color: tok.textSecondary, fontSize: '11px' })}>Reading immutable source span…</div>}
      {state.error && <div role="alert" className={css({ marginTop: '10px', color: tok.statusRed, fontSize: '11px' })}>{state.error}</div>}
      {state.value && <><div className={css({ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0, 1fr))', gap: '8px', marginTop: '10px', '@media screen and (max-width: 720px)': { gridTemplateColumns: '1fr 1fr' } })}><Identity label="Generation" value={state.value.generation} /><Identity label="Root" value={state.value.root_digest} /><Identity label="Object" value={state.value.evidence.object_id} /><Identity label="Content" value={state.value.evidence.content_digest} /></div><pre tabIndex={0} className={css({ margin: '10px 0 0', maxHeight: '280px', overflow: 'auto', padding: '12px', border: `1px solid ${tok.cardBorder}`, borderRadius: '5px', backgroundColor: tok.pageBg, color: tok.plainCode, fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '17px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', ':focus-visible': focusRing(tok) })}>{state.value.content}</pre></>}
    </aside>
  )
}

function Identity({ label, value }: { label: string; value: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <div className={css({ minWidth: 0, color: tok.textTertiary, fontSize: '9.5px', lineHeight: '14px' })}>{label}<code title={value} className={css({ display: 'block', color: tok.textSecondary, fontFamily: FONTS.MONO, overflow: 'hidden', textOverflow: 'ellipsis' })}>{shortIdentity(value)}</code></div>
}

function MobileLabel({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <div className={css({ marginBottom: '4px', color: tok.textTertiary, fontSize: '9px', lineHeight: '13px', textTransform: 'uppercase', letterSpacing: '0.07em' })}>{children}</div>
}

function explorerRoute(params: URLSearchParams): ExplorerRoute {
  const direction = params.get('direction')
  const evidence = params.get('evidence')
  return {
    serviceKey: params.get('service_key') ?? '',
    repository: params.get('repository') ?? '',
    direction: direction === 'uses' || direction === 'provided' || direction === 'produces' || direction === 'consumes' ? direction : 'all',
    evidence: evidence === 'rpc' || evidence === 'kafka' ? evidence : 'all',
    lookupKey: params.get('lookup_key') ?? '',
    cursor: params.get('cursor') ?? '',
    diagram: params.get('diagram') === '1',
  }
}

function draftFromRoute(route: ExplorerRoute): DraftFilters {
  return { serviceKey: route.serviceKey, repository: route.repository, direction: route.direction, evidence: route.evidence, lookupKey: route.lookupKey }
}

function explorerParams(route: ExplorerRoute): Record<string, string> {
  const values: Record<string, string> = { service_key: route.serviceKey }
  if (route.repository) values.repository = route.repository
  if (route.direction !== 'all') values.direction = route.direction
  if (route.evidence !== 'all') values.evidence = route.evidence
  if (route.lookupKey) values.lookup_key = route.lookupKey
  if (route.cursor) values.cursor = route.cursor
  if (route.diagram) values.diagram = '1'
  return values
}

function explorerHref(route: ExplorerRoute): string {
  return href('/relationships', explorerParams(route))
}

function relationshipRequest(route: ExplorerRoute): RelationshipRequest {
  let view: ServiceRelationshipView = 'all'
  let kind: 'rpc' | 'kafka' | undefined
  let plane: string | undefined
  if (route.direction === 'uses') view = 'dependencies'
  if (route.direction === 'provided') view = 'callers'
  if (route.direction === 'produces' || route.direction === 'consumes') {
    view = 'topics'
    kind = 'kafka'
    plane = route.direction === 'produces' ? 'producer' : 'consumer'
  }
  if (route.evidence !== 'all') kind = route.evidence
  return {
    repository: route.repository || undefined,
    service_key: route.serviceKey,
    view,
    kind,
    plane,
    lookup_key: route.lookupKey || undefined,
    page_size: PAGE_SIZE,
    cursor: route.cursor || undefined,
  }
}

function validatePage(route: ExplorerRoute, request: RelationshipRequest, page: ServiceRelationshipPage): string {
  if (page.schema !== PAGE_SCHEMA || page.query.service_key !== route.serviceKey ||
      page.query.view !== request.view || (page.query.kind ?? '') !== (request.kind ?? '') ||
      (page.query.plane ?? '') !== (request.plane ?? '') ||
      (page.query.lookup_key ?? '') !== (request.lookup_key ?? '')) {
    return 'relationship response query authority differs from the requested filters'
  }
  if (page.query.repositories.length > 32 || (route.repository &&
      (page.query.repositories.length !== 1 || page.query.repositories[0] !== route.repository))) {
    return 'relationship response repository authority differs from the requested scope'
  }
  const repositories = new Set(page.query.repositories)
  if (repositories.size !== page.query.repositories.length || page.roots.length !== repositories.size ||
      page.roots.some((root) => !repositories.has(root.repository) || root.service_key !== route.serviceKey)) {
    return 'relationship response roots differ from the authorized repository set'
  }
  const coverage = page.coverage
  if (coverage.authorized_repositories !== repositories.size ||
      coverage.complete_roots + coverage.empty_roots + coverage.failed_roots + coverage.unavailable_roots !== repositories.size ||
      coverage.returned_rows !== page.rows.length || coverage.scanned_references < page.rows.length ||
      page.rows.length > PAGE_SIZE || page.pagination.returned !== page.rows.length || page.pagination.page_size !== PAGE_SIZE) {
    return 'relationship response coverage or page bounds are invalid'
  }
  const roots = new Map(page.roots.map((root) => [root.repository, root]))
  if (page.rows.some((row) => {
    const root = roots.get(row.repository)
    return !root || row.service_key !== route.serviceKey || row.service_incarnation !== root.service_incarnation ||
      row.service_generation !== root.service_generation || !row.citation
  })) {
    return 'relationship row authority differs from its exact root'
  }
  return ''
}

function validateCitation(row: ServiceRelationshipRow, root: ServiceRelationshipPage['roots'][number], value: ServiceRelationshipCitation): string {
  const span = value.evidence.span
  const expected = row.evidence.span
  if (value.schema !== CITATION_SCHEMA || value.repository !== row.repository ||
      value.generation !== root.generation || value.root_digest !== root.root_digest ||
      value.projection.digest !== row.projection_digest || value.projection.posting_digest !== row.posting_digest ||
      value.evidence.posting_digest !== row.evidence.posting_digest || value.evidence.path !== row.evidence.path ||
      value.evidence.object_id !== row.evidence.object_id || value.evidence.content_digest !== row.evidence.content_digest ||
      span.start_byte !== expected.start_byte || span.end_byte !== expected.end_byte ||
      span.start_line !== expected.start_line || span.end_line !== expected.end_line) {
    return 'citation response authority differs from the selected exact relationship row'
  }
  return ''
}

function exactRoute(row: ServiceRelationshipRow): ExactRoute {
  const counterparts = row.counterpart_services.length > 0 ? row.counterpart_services.join(' + ') : 'no accepted counterpart'
  const topic = row.evidence.topic_spelling || row.lookup_key || 'unresolved topic'
  if (row.kind === 'kafka') {
    return row.plane === 'consumer'
      ? { from: topic, to: row.service_key, posture: 'exact consumer source evidence' }
      : { from: row.service_key, to: topic, posture: 'exact producer source evidence' }
  }
  if (row.participation.includes('target')) return { from: counterparts, to: row.service_key, posture: 'exact RPC target participation' }
  return { from: row.service_key, to: counterparts, posture: 'exact RPC source participation' }
}

function classificationLabel(row: ServiceRelationshipRow): string {
  if (row.source.unowned || row.target?.unowned) return 'Unowned'
  const claims = [...row.source.claims, ...(row.target?.claims ?? [])]
  if (claims.some((claim) => claim.disposition === 'conflict')) return 'Ambiguous'
  if ([row.source, row.target].some((placement) => placement && placement.claims.filter((claim) => claim.disposition === 'accepted').length > 1)) return 'Shared'
  return titleCase(row.class)
}

function rowID(index: number): string { return `R-${String(index + 1).padStart(2, '0')}` }
function titleCase(value: string): string { return value ? value[0].toUpperCase() + value.slice(1).replaceAll('_', ' ') : value }
function boundedError(cause: unknown): string { const value = String(cause).replace(/^Error:\s*/, ''); return value.length <= 512 ? value : `${value.slice(0, 511)}…` }
function shortIdentity(value: string): string { return value.length <= 28 ? value : `${value.slice(0, 17)}…${value.slice(-8)}` }

function breadcrumb(tok: PhebsTokens) { return { color: tok.textSecondary, fontSize: '11px', textDecoration: 'none', ':hover': { color: tok.textPrimary }, ':focus-visible': focusRing(tok) } }
function focusRing(tok: PhebsTokens) { return { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' } }
function inputStyle(tok: PhebsTokens) { return { width: '100%', height: '36px', boxSizing: 'border-box' as const, padding: '0 10px', border: `1px solid ${tok.cardBorder}`, borderRadius: '5px', backgroundColor: tok.pageBg, color: tok.textPrimary, fontFamily: 'inherit', fontSize: '11.5px', ':focus': { borderColor: tok.accent }, ':focus-visible': focusRing(tok) } }
function primaryButton(tok: PhebsTokens) { return { minHeight: '36px', padding: '0 13px', border: '0', borderRadius: '5px', backgroundColor: tok.textPrimary, color: tok.pageBg, fontFamily: 'inherit', fontSize: '11px', fontWeight: 600, cursor: 'pointer', whiteSpace: 'nowrap' as const, ':hover': { opacity: 0.84 }, ':focus-visible': focusRing(tok) } }
function resetLink(tok: PhebsTokens) { return { minHeight: '32px', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', color: tok.selectedText, fontSize: '10.5px', textDecoration: 'none', ':hover': { textDecoration: 'underline' }, ':focus-visible': focusRing(tok) } }
function statusBox(tok: PhebsTokens) { return { minHeight: '90px', boxSizing: 'border-box' as const, display: 'flex', alignItems: 'center', padding: '20px', border: `1px solid ${tok.cardBorder}`, borderRadius: '7px', color: tok.textTertiary, fontSize: '11.5px', lineHeight: '18px' } }
function tableHead(tok: PhebsTokens) { return { padding: '8px 9px', textAlign: 'left' as const, backgroundColor: tok.bandBg, color: tok.textTertiary, fontSize: '9.5px', lineHeight: '14px', fontWeight: 600 } }
function tableCell(tok: PhebsTokens) { return { padding: '10px 9px', verticalAlign: 'top' as const, color: tok.textSecondary, fontSize: '10.5px', lineHeight: '16px', overflowWrap: 'anywhere' as const } }
function codeWrap(tok: PhebsTokens) { return { color: tok.textSecondary, fontFamily: FONTS.MONO, fontSize: '9.5px', lineHeight: '15px', whiteSpace: 'normal' as const, overflowWrap: 'anywhere' as const } }
function meta(tok: PhebsTokens) { return { marginTop: '3px', color: tok.textTertiary, fontSize: '9px', lineHeight: '13px', overflowWrap: 'anywhere' as const } }
function idLink(tok: PhebsTokens) { return { color: tok.selectedText, fontFamily: FONTS.MONO, fontSize: '10px', fontWeight: 600, textDecoration: 'none', ':hover': { textDecoration: 'underline' }, ':focus-visible': focusRing(tok) } }
function idBadge(tok: PhebsTokens) { return { color: tok.textSecondary, fontFamily: FONTS.MONO, fontSize: '10px', fontWeight: 600 } }
function citationButton(tok: PhebsTokens) { return { minHeight: '32px', padding: '0 8px', border: `1px solid ${tok.cardBorder}`, borderRadius: '5px', backgroundColor: tok.pageBg, color: tok.selectedText, fontFamily: 'inherit', fontSize: '10px', cursor: 'pointer', whiteSpace: 'nowrap' as const, ':hover': { backgroundColor: tok.hoverFill }, ':focus-visible': focusRing(tok) } }
function secondaryLink(tok: PhebsTokens) { return { minHeight: '34px', display: 'inline-flex', alignItems: 'center', padding: '0 10px', border: `1px solid ${tok.cardBorder}`, borderRadius: '5px', color: tok.textSecondary, fontSize: '10.5px', textDecoration: 'none', ':focus-visible': focusRing(tok) } }
function primaryLink(tok: PhebsTokens) { return { ...secondaryLink(tok), border: 'none', backgroundColor: tok.textPrimary, color: tok.pageBg, fontWeight: 600 } }
