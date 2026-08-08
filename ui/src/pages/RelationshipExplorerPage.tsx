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
import { href, navigate, replaceRoute } from '../router'
import { FONTS, focusRing, useDensity, usePhebsTokens, type DensityName, type PhebsTokens } from '../theme'
import { CitationChip, CitationPanel, ClaimBoundary } from '../components/kit'
import { VirtualList, type VirtualListHandle, type VirtualRowProps } from '../components/VirtualList'
import { EXPLORER_DIAGRAM_ADDENDUM, RELATIONSHIP_CAVEAT_MIRROR } from '../caveats'
import { validateServiceRelationshipCitation, validateServiceRelationshipRoot } from '../components/serviceRelationshipCitation'
import { isAbortError } from '../util'

const PAGE_SIZE = 50
const PAGE_SCHEMA = 'phebs-service-relationship-page-v1'

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
  // Client-side view state (T43.11): URL-borne, never part of the wire
  // query. A row pin is the composite row identity — repository AND
  // projection digest, since projection digests carry no repository
  // identity and can collide across repositories. narrow and group read
  // only the delivered page.
  selectedRepo: string
  selectedRow: string
  narrow: string
  group: 'none' | 'class'
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
    // Client-side view state (selection, narrowing, grouping) never
    // re-issues the wire query.
    selectedRepo: '',
    selectedRow: '',
    narrow: '',
    group: 'none',
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
  const [reloadGeneration, setReloadGeneration] = useState(0)

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
  }, [queryRoute, reloadGeneration])

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
      // A row pin is page-scoped; new filters mean a new page.
      selectedRepo: '',
      selectedRow: '',
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
        const responseError = validateServiceRelationshipCitation(row, root, value)
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
          Enter one repository-scoped service key. Leave repository blank to search across every repository you are authorized to see.
        </div>
      ) : loading ? (
        <div role="status" aria-live="polite" className={css(statusBox(tok))}>Reading exact relationship roots and bounded source rows…</div>
      ) : error ? (
        <div role="alert" className={css({ ...statusBox(tok), color: tok.status.conflict.text })}>{error}</div>
      ) : page ? (
        <>
          <CoverageStrip page={page} />
          <div className={css({ display: 'grid', gridTemplateColumns: route.diagram ? 'minmax(0, 3fr) minmax(280px, 1fr)' : 'minmax(0, 1fr)', gap: '10px', alignItems: 'start', '@media screen and (max-width: 980px)': { gridTemplateColumns: '1fr' } })}>
            <ExactRows page={page} route={route} onCitation={openCitation} />
            {route.diagram && <PageDiagram page={page} route={route} />}
          </div>
          <PageNavigation route={route} page={page} />
        </>
      ) : null}

      {citation && (
        <CitationPanel
          id="relationship-explorer-citation"
          loading={citation.loading}
          error={citation.error ?? ''}
          citation={citation.value ?? null}
          onClose={closeCitation}
          onRefresh={() => {
            closeCitation()
            setReloadGeneration((generation) => generation + 1)
          }}
        />
      )}

      <div className={css({ marginTop: '12px' })}>
        <ClaimBoundary caveat={page?.caveat ?? RELATIONSHIP_CAVEAT_MIRROR}>
          <p style={{ margin: '6px 0 0' }}>{EXPLORER_DIAGRAM_ADDENDUM}</p>
        </ClaimBoundary>
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
      <div className={css({ flex: '1 1 260px', minHeight: '48px', padding: '8px 14px', display: 'flex', alignItems: 'center', color: partial ? tok.status.stale.text : tok.textTertiary, fontSize: '10.5px', lineHeight: '16px', '@media screen and (max-width: 720px)': { gridColumn: '1 / -1' } })}>
        {partial
          ? `Partial · ${page.coverage.truncated ? 'reference admission cap reached' : `${gaps} failed or unavailable root${gaps === 1 ? '' : 's'}`}`
          : 'Bounded exact roots · completeness beyond the published authority is not claimed'}
      </div>
    </section>
  )
}

// T43.11: exact rows render through the windowed list — fixed per-density
// row heights (the DENSITY token minima), one presentation at every width.
// A row is a summary line; its complete evidence — full paths, every
// attribution placement, the citation action — lives in the selected-row
// detail region below, pinned by projection digest in the URL.
const EXPLORER_ROW_HEIGHTS: Record<DensityName, number> = { comfortable: 44, dense: 32 }
const EXPLORER_LIST_HEIGHT = 480

type ExplorerListItem =
  | { kind: 'row'; row: ServiceRelationshipRow; pageIndex: number }
  | { kind: 'header'; label: string; count: number }

function narrowRows(rows: ServiceRelationshipRow[], narrow: string): { row: ServiceRelationshipRow; pageIndex: number }[] {
  const indexed = rows.map((row, pageIndex) => ({ row, pageIndex }))
  const needle = narrow.trim().toLowerCase()
  if (!needle) return indexed
  return indexed.filter(({ row }) => {
    const route = exactRoute(row)
    return row.evidence.path.toLowerCase().includes(needle) ||
      (row.evidence.operation || row.evidence.topic_spelling || row.lookup_key || '').toLowerCase().includes(needle) ||
      route.from.toLowerCase().includes(needle) ||
      route.to.toLowerCase().includes(needle) ||
      classificationLabel(row).toLowerCase().includes(needle)
  })
}

function groupRows(entries: { row: ServiceRelationshipRow; pageIndex: number }[], group: 'none' | 'class'): ExplorerListItem[] {
  if (group === 'none') return entries.map((entry) => ({ kind: 'row', ...entry }))
  const labels: string[] = []
  for (const entry of entries) {
    const label = classificationLabel(entry.row)
    if (!labels.includes(label)) labels.push(label)
  }
  const items: ExplorerListItem[] = []
  for (const label of labels) {
    const members = entries.filter((entry) => classificationLabel(entry.row) === label)
    items.push({ kind: 'header', label, count: members.length })
    for (const entry of members) items.push({ kind: 'row', ...entry })
  }
  return items
}

function ExactRows({ page, route, onCitation }: { page: ServiceRelationshipPage; route: ExplorerRoute; onCitation: (row: ServiceRelationshipRow) => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { density } = useDensity()
  const rowHeight = EXPLORER_ROW_HEIGHTS[density]
  const narrowed = useMemo(() => narrowRows(page.rows, route.narrow), [page.rows, route.narrow])
  const items = useMemo(() => groupRows(narrowed, route.group), [narrowed, route.group])
  const [activeIndex, setActiveIndex] = useState(0)
  useEffect(() => setActiveIndex(0), [items.length, route.cursor])
  const listRef = useRef<VirtualListHandle>(null)
  // Composite row identity: repository AND projection digest — digests can
  // collide across repositories.
  const pinMatches = (row: ServiceRelationshipRow) =>
    row.projection_digest === route.selectedRow && row.repository === route.selectedRepo
  const selectedIndex = items.findIndex((item) => item.kind === 'row' && pinMatches(item.row))
  const selected = selectedIndex >= 0 ? (items[selectedIndex] as { kind: 'row'; row: ServiceRelationshipRow; pageIndex: number }) : null
  useEffect(() => {
    if (selectedIndex >= 0) listRef.current?.scrollToIndex(selectedIndex)
    // Reveal the pinned row whenever the pin or the list shape changes.
  }, [route.selectedRow, route.selectedRepo, selectedIndex])
  const selectRow = (row: ServiceRelationshipRow) =>
    navigate('/relationships', explorerParams({ ...route, selectedRepo: row.repository, selectedRow: row.projection_digest }))
  if (page.rows_state === 'gap') {
    return <div className={css(statusBox(tok))}>One or more requested relationship roots are gaps. No empty result is inferred.</div>
  }
  if (page.rows.length === 0) {
    return <div className={css(statusBox(tok))}>Exact empty page for these filters. Root gaps and admitted truncation remain reported above.</div>
  }
  const columns = `44px minmax(0, 1.5fr) minmax(0, 1.1fr) minmax(0, 1.5fr) 96px`
  const cellText = { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const, minWidth: 0 }
  return (
    <section aria-labelledby="exact-relationship-rows" className={css({ minWidth: 0, border: `1px solid ${tok.cardBorder}`, borderRadius: '7px', overflow: 'hidden' })}>
      <h2 id="exact-relationship-rows" className={css({ margin: 0, padding: '10px 12px', backgroundColor: tok.bandBg, borderBottom: `1px solid ${tok.cardBorder}`, color: tok.textPrimary, fontSize: '12px', lineHeight: '17px' })}>Exact source rows</h2>
      <div className={css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1.4fr) minmax(0, 1fr)', gap: '8px', padding: '9px 12px', borderBottom: `1px solid ${tok.innerSep}`, backgroundColor: tok.bandBg, '@media screen and (max-width: 480px)': { gridTemplateColumns: '1fr' } })}>
        <label className={css({ display: 'grid', gap: '3px', fontSize: '10.5px', lineHeight: '14px', color: tok.textTertiary })}>
          Narrow loaded rows
          <input
            type="search"
            value={route.narrow}
            placeholder="Path, identity, route, or class"
            onChange={(event) => replaceRoute('/relationships', explorerParams({ ...route, narrow: event.currentTarget.value }))}
            className={css({ width: '100%', height: '30px', boxSizing: 'border-box', padding: '0 8px', border: `1px solid ${tok.cardBorder}`, borderRadius: '6px', backgroundColor: tok.pageBg, color: tok.textPrimary, fontSize: '12px', ':focus-visible': focusRing(tok) })}
          />
        </label>
        <label className={css({ display: 'grid', gap: '3px', fontSize: '10.5px', lineHeight: '14px', color: tok.textTertiary })}>
          Group
          <select
            value={route.group === 'class' ? 'class' : ''}
            onChange={(event) => navigate('/relationships', explorerParams({ ...route, group: event.currentTarget.value === 'class' ? 'class' : 'none' }))}
            className={css({ width: '100%', height: '30px', boxSizing: 'border-box', padding: '0 28px 0 8px', border: `1px solid ${tok.cardBorder}`, borderRadius: '6px', backgroundColor: tok.pageBg, color: tok.textPrimary, fontSize: '12px', ':focus-visible': focusRing(tok) })}
          >
            <option value="">None</option>
            <option value="class">Classification</option>
          </select>
        </label>
      </div>
      {route.narrow.trim() !== '' && (
        <div role="status" className={css({ padding: '7px 12px', borderBottom: `1px solid ${tok.innerSep}`, fontSize: '11px', lineHeight: '16px', color: tok.textSecondary })}>
          {narrowed.length} of {page.pagination.returned} loaded rows match · narrowing reads this page only
        </div>
      )}
      {narrowed.length === 0 ? (
        <div className={css({ padding: '24px 14px', color: tok.textSecondary, fontSize: '12px', lineHeight: '18px' })}>
          No loaded rows match this narrowing. It reads only this page&apos;s {page.pagination.returned} rows and makes no claim about other pages — the filters above query the exact evidence.
        </div>
      ) : (
        <>
          <div aria-hidden="true" className={css({ display: 'grid', gridTemplateColumns: columns, gap: '8px', padding: `5px 12px`, borderBottom: `1px solid ${tok.innerSep}`, backgroundColor: tok.bandBg, '@media screen and (max-width: 720px)': { gridTemplateColumns: '44px minmax(0, 1fr) 96px' } })}>
            {['ID', 'Evidence', 'Contract / topic', 'Service route', 'Class'].map((label, index) => (
              <span key={label} className={css({ color: tok.textTertiary, fontSize: '9.5px', lineHeight: '14px', fontWeight: 600, ...(index === 2 || index === 3 ? { '@media screen and (max-width: 720px)': { display: 'none' } } : {}) })}>{label}</span>
            ))}
          </div>
          <VirtualList<ExplorerListItem>
            ref={listRef}
            items={items}
            rowHeight={rowHeight}
            height={Math.min(EXPLORER_LIST_HEIGHT, items.length * rowHeight)}
            ariaLabel="Exact relationship source rows"
            listboxId="relationship-rows"
            activeIndex={activeIndex}
            selectedIndex={selectedIndex}
            onActiveChange={setActiveIndex}
            onCommit={(item) => { if (item.kind === 'row') selectRow(item.row) }}
            getKey={(item) => item.kind === 'header' ? `header:${item.label}` : `${item.row.repository}:${item.row.projection_digest}`}
            isHeader={(item) => item.kind === 'header'}
            renderRow={(item, _index, rowProps, active) => item.kind === 'header' ? (
              <div {...rowProps} className={css({ display: 'flex', alignItems: 'flex-end', gap: '7px', padding: '0 12px 4px', backgroundColor: tok.bandBg, borderBottom: `1px solid ${tok.innerSep}` })}>
                <span className={css({ fontSize: '10px', lineHeight: '14px', fontWeight: 600, letterSpacing: '0.07em', textTransform: 'uppercase', color: tok.textTertiary })}>{item.label}</span>
                <span className={css({ fontSize: '10px', lineHeight: '14px', color: tok.textTertiary, fontVariantNumeric: 'tabular-nums' })}>{item.count}</span>
              </div>
            ) : (
              <ExplorerRow item={item} rowProps={rowProps} active={active} selected={pinMatches(item.row)} columns={columns} cellText={cellText} onSelect={() => selectRow(item.row)} />
            )}
          />
        </>
      )}
      {route.selectedRow && !selected && narrowed.length > 0 && (
        <div role="status" className={css({ padding: '9px 12px', borderTop: `1px solid ${tok.innerSep}`, fontSize: '11px', lineHeight: '16px', color: tok.status.stale.text })}>
          The pinned row is not in this page&apos;s loaded rows. Selection is page-scoped; it may sit on another page or outside the current narrowing.
        </div>
      )}
      {selected && <RowDetail row={selected.row} id={rowID(selected.pageIndex)} route={route} onCitation={onCitation} />}
    </section>
  )
}

function ExplorerRow({ item, rowProps, active, selected, columns, cellText, onSelect }: {
  item: { row: ServiceRelationshipRow; pageIndex: number }
  rowProps: VirtualRowProps
  active: boolean
  selected: boolean
  columns: string
  cellText: Record<string, unknown>
  onSelect: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { row } = item
  const route = exactRoute(row)
  const subject = row.evidence.operation || row.evidence.topic_spelling || row.lookup_key || 'No resolved identity'
  const label = classificationLabel(row)
  const labelColor = label === 'Ambiguous' ? tok.status.conflict.text : label === 'Unowned' || label === 'Unsupported' || label === 'Unresolved' ? tok.status.stale.text : tok.textSecondary
  return (
    <div
      {...rowProps}
      onClick={onSelect}
      aria-label={`${rowID(item.pageIndex)} · ${row.evidence.path}:${row.evidence.span.start_line}–${row.evidence.span.end_line} · ${subject} · ${route.from} → ${route.to} · ${label}`}
      className={css({
        display: 'grid',
        gridTemplateColumns: columns,
        gap: '8px',
        alignItems: 'center',
        padding: '0 12px',
        cursor: 'pointer',
        backgroundColor: selected ? tok.selectedLineBg : active ? tok.hoverFill : 'transparent',
        boxShadow: selected ? `inset 2px 0 0 ${tok.accent}` : active ? `inset 2px 0 0 ${tok.gutter}` : 'none',
        borderBottom: `1px solid ${tok.innerSep}`,
        ':hover': { backgroundColor: selected ? tok.selectedLineBg : tok.hoverFill },
        '@media screen and (max-width: 720px)': { gridTemplateColumns: '44px minmax(0, 1fr) 96px' },
      })}
    >
      <span className={css(idBadge(tok))}>{rowID(item.pageIndex)}</span>
      <span title={row.evidence.path} className={css({ ...cellText, fontFamily: FONTS.MONO, fontSize: '10px', color: tok.textSecondary })}>{row.evidence.path}:{row.evidence.span.start_line}–{row.evidence.span.end_line}</span>
      <span title={subject} className={css({ ...cellText, fontSize: '10.5px', color: tok.textPrimary, '@media screen and (max-width: 720px)': { display: 'none' } })}>{subject}</span>
      <span title={`${route.from} → ${route.to}`} className={css({ ...cellText, fontFamily: FONTS.MONO, fontSize: '10px', color: tok.textSecondary, '@media screen and (max-width: 720px)': { display: 'none' } })}>{route.from} <span aria-hidden="true">→</span> {route.to}</span>
      <span className={css({ ...cellText, fontSize: '10px', fontWeight: 600, color: labelColor })}>{label}</span>
    </div>
  )
}

// The selected row's complete evidence: nothing the old table showed is
// dropped — full paths wrap, every attribution placement renders, and the
// citation action is a real, tabbable control.
function RowDetail({ row, id, route, onCitation }: { row: ServiceRelationshipRow; id: string; route: ExplorerRoute; onCitation: (row: ServiceRelationshipRow) => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const exact = exactRoute(row)
  const subject = row.evidence.operation || row.evidence.topic_spelling || row.lookup_key || 'No resolved identity'
  return (
    <section role="region" aria-label={`Row ${id} detail`} className={css({ borderTop: `2px solid ${tok.cardBorder}`, padding: '12px 14px' })}>
      <div className={css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '10px', flexWrap: 'wrap' })}>
        <div className={css({ display: 'flex', alignItems: 'baseline', gap: '8px' })}>
          <span className={css(idBadge(tok))}>{id}</span>
          <ClassificationCell row={row} />
        </div>
        <div className={css({ display: 'flex', alignItems: 'center', gap: '10px' })}>
          <CitationButton row={row} onCitation={onCitation} />
          <a href={explorerHref({ ...route, selectedRepo: '', selectedRow: '' })} className={css({ color: tok.textSecondary, fontSize: '10.5px', textDecoration: 'underline', ':hover': { color: tok.textPrimary }, ':focus-visible': focusRing(tok) })}>Clear selection</a>
        </div>
      </div>
      <div className={css({ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: '10px 16px', marginTop: '10px', '@media screen and (max-width: 640px)': { gridTemplateColumns: '1fr' } })}>
        <div>
          <DetailLabel>Evidence</DetailLabel>
          <code className={css(codeWrap(tok))}>{row.evidence.path}</code>
          <div className={css(meta(tok))}>lines {row.evidence.span.start_line}–{row.evidence.span.end_line} · {row.evidence.source_role} · {row.repository}</div>
        </div>
        <div>
          <DetailLabel>Contract / topic</DetailLabel>
          <div className={css({ color: tok.textPrimary, fontSize: '11px', lineHeight: '16px', overflowWrap: 'anywhere' })}>{subject}</div>
          <div className={css(meta(tok))}>{row.kind} · {row.plane}</div>
        </div>
        <div>
          <DetailLabel>Exact route</DetailLabel>
          <div className={css({ fontFamily: FONTS.MONO, fontSize: '10px', lineHeight: '16px', color: tok.textSecondary, overflowWrap: 'anywhere' })}>
            <strong className={css({ color: tok.textPrimary })}>{exact.from}</strong><span aria-hidden="true"> → </span><strong className={css({ color: tok.textPrimary })}>{exact.to}</strong>
          </div>
          <div className={css(meta(tok))}>{exact.posture}</div>
        </div>
        <div>
          <DetailLabel>Attribution</DetailLabel>
          <AttributionCell row={row} />
        </div>
      </div>
    </section>
  )
}

function DetailLabel({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <div className={css({ marginBottom: '4px', color: tok.textTertiary, fontSize: '9px', lineHeight: '13px', textTransform: 'uppercase', letterSpacing: '0.07em' })}>{children}</div>
}

interface ExactRoute { from: string; to: string; posture: string }

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
  const color = label === 'Ambiguous' ? tok.status.conflict.text : label === 'Unowned' || label === 'Unsupported' || label === 'Unresolved' ? tok.status.stale.text : tok.textSecondary
  return <div><span className={css({ color, fontSize: '10.5px', lineHeight: '15px', fontWeight: 600 })}>{label}</span><div className={css(meta(tok))}>{row.participation.join(' + ') || 'unattributed'}{row.evidence.reason ? ` · ${row.evidence.reason}` : ''}</div></div>
}

function CitationButton({ row, onCitation }: { row: ServiceRelationshipRow; onCitation: (row: ServiceRelationshipRow) => void }) {
  return <CitationChip path={row.evidence.path} span={row.evidence.span} onOpen={() => onCitation(row)} />
}

function PageDiagram({ page, route }: { page: ServiceRelationshipPage; route: ExplorerRoute }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <figure aria-labelledby="page-diagram-title" className={css({ margin: 0, border: `1px solid ${tok.cardBorder}`, borderRadius: '7px', overflow: 'hidden' })}>
      <figcaption id="page-diagram-title" className={css({ padding: '10px 12px', borderBottom: `1px solid ${tok.cardBorder}`, backgroundColor: tok.bandBg, color: tok.textSecondary, fontSize: '10.5px', lineHeight: '16px' })}>Current page only · the row list remains authoritative</figcaption>
      <ol className={css({ margin: 0, padding: 0, listStyle: 'none' })}>
        {page.rows.map((row, index) => {
          const exact = exactRoute(row)
          const id = rowID(index)
          return (
            <li key={`${row.repository}:${row.projection_digest}`} className={css({ display: 'grid', gridTemplateColumns: '42px minmax(0, 1fr) 32px minmax(0, 1fr)', alignItems: 'center', gap: '6px', minHeight: '70px', padding: '8px 10px', borderTop: index === 0 ? 'none' : `1px solid ${tok.innerSep}` })}>
              <button
                type="button"
                aria-label={`Select source row ${id}`}
                // Rows are windowed, so the target may not be in the DOM;
                // pinning it in the URL reveals and details it instead.
                onClick={() => navigate('/relationships', explorerParams({ ...route, selectedRepo: row.repository, selectedRow: row.projection_digest }))}
                className={css({ ...idLink(tok), border: 0, padding: 0, background: 'transparent', cursor: 'pointer', textAlign: 'left' })}
              >{id}</button>
              <DiagramNode>{exact.from}</DiagramNode>
              <span aria-label={exact.posture} title={exact.posture} className={css({ textAlign: 'center', color: tok.textTertiary, fontSize: '16px' })}>→</span>
              <DiagramNode>{exact.to}</DiagramNode>
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
      {route.cursor ? <a href={explorerHref({ ...route, cursor: '', selectedRepo: '', selectedRow: '' })} className={css(secondaryLink(tok))}>First exact page</a> : <span />}
      {page.pagination.next_cursor && <a href={explorerHref({ ...route, cursor: page.pagination.next_cursor, selectedRepo: '', selectedRow: '' })} className={css(primaryLink(tok))}>Next exact page</a>}
    </nav>
  )
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
    selectedRepo: params.get('sel_repo') ?? '',
    selectedRow: params.get('sel_row') ?? '',
    narrow: params.get('narrow') ?? '',
    group: params.get('group') === 'class' ? 'class' : 'none',
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
  // The pin is composite; both halves travel or neither does.
  if (route.selectedRow && route.selectedRepo) {
    values.sel_repo = route.selectedRepo
    values.sel_row = route.selectedRow
  }
  if (route.narrow) values.narrow = route.narrow
  if (route.group !== 'none') values.group = route.group
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
      page.roots.some((root) => !repositories.has(root.repository) || root.service_key !== route.serviceKey ||
        validateServiceRelationshipRoot(root) !== '')) {
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

function breadcrumb(tok: PhebsTokens) { return { color: tok.textSecondary, fontSize: '11px', textDecoration: 'none', ':hover': { color: tok.textPrimary }, ':focus-visible': focusRing(tok) } }function inputStyle(tok: PhebsTokens) { return { width: '100%', height: '36px', boxSizing: 'border-box' as const, padding: '0 10px', border: `1px solid ${tok.cardBorder}`, borderRadius: '5px', backgroundColor: tok.pageBg, color: tok.textPrimary, fontFamily: 'inherit', fontSize: '11.5px', ':focus': { borderColor: tok.accent }, ':focus-visible': focusRing(tok) } }
function primaryButton(tok: PhebsTokens) { return { minHeight: '36px', padding: '0 13px', border: '0', borderRadius: '5px', backgroundColor: tok.textPrimary, color: tok.pageBg, fontFamily: 'inherit', fontSize: '11px', fontWeight: 600, cursor: 'pointer', whiteSpace: 'nowrap' as const, ':hover': { opacity: 0.84 }, ':focus-visible': focusRing(tok) } }
function resetLink(tok: PhebsTokens) { return { minHeight: '32px', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', color: tok.selectedText, fontSize: '10.5px', textDecoration: 'none', ':hover': { textDecoration: 'underline' }, ':focus-visible': focusRing(tok) } }
function statusBox(tok: PhebsTokens) { return { minHeight: '90px', boxSizing: 'border-box' as const, display: 'flex', alignItems: 'center', padding: '20px', border: `1px solid ${tok.cardBorder}`, borderRadius: '7px', color: tok.textTertiary, fontSize: '11.5px', lineHeight: '18px' } }
function codeWrap(tok: PhebsTokens) { return { color: tok.textSecondary, fontFamily: FONTS.MONO, fontSize: '9.5px', lineHeight: '15px', whiteSpace: 'normal' as const, overflowWrap: 'anywhere' as const } }
function meta(tok: PhebsTokens) { return { marginTop: '3px', color: tok.textTertiary, fontSize: '9px', lineHeight: '13px', overflowWrap: 'anywhere' as const } }
function idLink(tok: PhebsTokens) { return { color: tok.selectedText, fontFamily: FONTS.MONO, fontSize: '10px', fontWeight: 600, textDecoration: 'none', ':hover': { textDecoration: 'underline' }, ':focus-visible': focusRing(tok) } }
function idBadge(tok: PhebsTokens) { return { color: tok.textSecondary, fontFamily: FONTS.MONO, fontSize: '10px', fontWeight: 600 } }
function secondaryLink(tok: PhebsTokens) { return { minHeight: '34px', display: 'inline-flex', alignItems: 'center', padding: '0 10px', border: `1px solid ${tok.cardBorder}`, borderRadius: '5px', color: tok.textSecondary, fontSize: '10.5px', textDecoration: 'none', ':focus-visible': focusRing(tok) } }
function primaryLink(tok: PhebsTokens) { return { ...secondaryLink(tok), border: 'none', backgroundColor: tok.textPrimary, color: tok.pageBg, fontWeight: 600 } }
