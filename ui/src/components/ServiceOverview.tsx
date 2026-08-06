import { useEffect, useMemo, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import {
  fetchServiceRelationshipCitation,
  fetchServiceRelationships,
  type ServiceDetail,
  type ServiceRelationshipCitation,
  type ServiceRelationshipPage,
  type ServiceRelationshipPlacement,
  type ServiceRelationshipRow,
  type ServiceRelationshipView,
} from '../api'
import { FONTS, usePhebsTokens, type PhebsTokens } from '../theme'
import { isAbortError } from '../util'

const RELATIONSHIP_PAGE_SIZE = 25
const views: ServiceRelationshipView[] = ['dependencies', 'callers', 'topics']

interface ServiceOverviewProps {
  detail: ServiceDetail
  relationshipsAvailable: boolean
  activeView: ServiceRelationshipView
  cursor: string
  viewHref: (view: ServiceRelationshipView, cursor?: string) => string
}

type PageMap = Partial<Record<ServiceRelationshipView, ServiceRelationshipPage>>
type ErrorMap = Partial<Record<ServiceRelationshipView, string>>
interface PagedView {
  view: ServiceRelationshipView
  cursor: string
  page?: ServiceRelationshipPage
  error?: string
  loading: boolean
}

export default function ServiceOverview({
  detail,
  relationshipsAvailable,
  activeView,
  cursor,
  viewHref,
}: ServiceOverviewProps) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [basePages, setBasePages] = useState<PageMap>({})
  const [baseErrors, setBaseErrors] = useState<ErrorMap>({})
  const [baseLoading, setBaseLoading] = useState(false)
  const [pagedView, setPagedView] = useState<PagedView | null>(null)
  const [citation, setCitation] = useState<ServiceRelationshipCitation | null>(null)
  const [citationError, setCitationError] = useState('')
  const [citationLoading, setCitationLoading] = useState(false)
  const citationController = useRef<AbortController | null>(null)

  useEffect(() => {
    citationController.current?.abort()
    citationController.current = null
    setCitation(null)
    setCitationError('')
    if (!relationshipsAvailable) {
      setBasePages({})
      setBaseErrors({})
      setBaseLoading(false)
      return
    }
    const controller = new AbortController()
    setBaseLoading(true)
    setBasePages({})
    setBaseErrors({})
    void Promise.all(views.map(async (view) => {
      try {
        const page = await fetchServiceRelationships({
          repository: detail.repository.repository,
          service_key: detail.service.key,
          view,
          page_size: RELATIONSHIP_PAGE_SIZE,
        }, controller.signal)
        const responseError = validateRelationshipPage(detail, view, page)
        if (responseError) return { view, error: responseError }
        return { view, page }
      } catch (cause) {
        if (isAbortError(cause)) throw cause
        return { view, error: boundedError(cause) }
      }
    }))
      .then((results) => {
        const nextPages: PageMap = {}
        const nextErrors: ErrorMap = {}
        for (const result of results) {
          if ('page' in result) nextPages[result.view] = result.page
          else nextErrors[result.view] = result.error
        }
        setBasePages(nextPages)
        setBaseErrors(nextErrors)
      })
      .catch((cause) => {
        if (!isAbortError(cause)) {
          setBaseErrors({ dependencies: boundedError(cause) })
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setBaseLoading(false)
      })
    return () => controller.abort()
  }, [
    detail,
    relationshipsAvailable,
  ])

  useEffect(() => {
    if (!relationshipsAvailable || !cursor) {
      setPagedView(null)
      return
    }
    const controller = new AbortController()
    setPagedView({ view: activeView, cursor, loading: true })
    void fetchServiceRelationships({
      repository: detail.repository.repository,
      service_key: detail.service.key,
      view: activeView,
      page_size: RELATIONSHIP_PAGE_SIZE,
      cursor,
    }, controller.signal)
      .then((page) => {
        if (controller.signal.aborted) return
        const responseError = validateRelationshipPage(detail, activeView, page)
        setPagedView(responseError
          ? { view: activeView, cursor, error: responseError, loading: false }
          : { view: activeView, cursor, page, loading: false })
      })
      .catch((cause) => {
        if (!controller.signal.aborted && !isAbortError(cause)) {
          setPagedView({ view: activeView, cursor, error: boundedError(cause), loading: false })
        }
      })
    return () => controller.abort()
  }, [activeView, cursor, detail, relationshipsAvailable])

  useEffect(() => () => citationController.current?.abort(), [])

  const snapshot = useMemo(
    () => relationshipSnapshot(detail, relationshipsAvailable, baseLoading, basePages, baseErrors),
    [baseErrors, baseLoading, basePages, detail, relationshipsAvailable],
  )
  const pagedMatches = pagedView?.view === activeView && pagedView.cursor === cursor
  const activePage = cursor ? pagedMatches && !pagedView.loading ? pagedView.page : undefined : basePages[activeView]
  const activeError = cursor ? pagedMatches ? pagedView.error : undefined : baseErrors[activeView]
  const activeLoading = cursor ? !pagedMatches || pagedView.loading : baseLoading

  const readCitation = (
    row: ServiceRelationshipRow,
    root: ServiceRelationshipPage['roots'][number],
  ) => {
    citationController.current?.abort()
    const controller = new AbortController()
    citationController.current = controller
    setCitation(null)
    setCitationError('')
    setCitationLoading(true)
    void fetchServiceRelationshipCitation(row.citation, controller.signal)
      .then((value) => {
        if (controller.signal.aborted) return
        const responseError = validateCitation(row, root, value)
        if (responseError) setCitationError(responseError)
        else setCitation(value)
      })
      .catch((cause) => {
        if (!controller.signal.aborted && !isAbortError(cause)) setCitationError(boundedError(cause))
      })
      .finally(() => {
        if (citationController.current === controller) {
          citationController.current = null
          setCitationLoading(false)
        }
      })
  }

  return (
    <section aria-labelledby="service-overview-heading" className={css({ borderTop: `1px solid ${tok.cardBorder}` })}>
      <div className={css({ padding: '16px' })}>
        <div className={css({ display: 'flex', justifyContent: 'space-between', gap: '16px', alignItems: 'flex-start', flexWrap: 'wrap' })}>
          <div>
            <h3 id="service-overview-heading" className={css({ margin: 0, fontSize: '14px', lineHeight: '20px', color: tok.textPrimary })}>
              Exact relationship overview
            </h3>
            <p className={css({ margin: '4px 0 0', maxWidth: '650px', fontSize: '11px', lineHeight: '17px', color: tok.textTertiary })}>
              Static source relationships at one digest-bound publication. Counts open the exact rows below; citations read only their immutable source spans.
            </p>
          </div>
          <OverviewState state={snapshot.state} reason={snapshot.reason} />
        </div>

        <nav aria-label="Service relationship summaries" className={css({ display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))', border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', overflow: 'hidden', marginTop: '14px', '@media screen and (max-width: 620px)': { gridTemplateColumns: '1fr' } })}>
          {views.map((view, index) => (
            <SummaryLink
              key={view}
              view={view}
              page={basePages[view]}
              error={baseErrors[view]}
              active={activeView === view}
              href={viewHref(view)}
              border={index !== views.length - 1}
            />
          ))}
        </nav>

        <GapLedger detail={detail} pages={basePages} errors={baseErrors} snapshot={snapshot} />
      </div>

      <div className={css({ borderTop: `1px solid ${tok.cardBorder}` })}>
        <RelationshipTableHeader view={activeView} page={activePage} />
        {activeLoading && !activePage ? (
          <div role="status" aria-live="polite" className={css(emptyState(tok))}>Reading exact relationship rows…</div>
        ) : activeError ? (
          <div role="alert" className={css(emptyState(tok))}>
            <strong className={css({ color: tok.statusRed })}>Relationship view failed.</strong>{' '}{activeError}
          </div>
        ) : !relationshipsAvailable ? (
          <div className={css(emptyState(tok))}>
            Relationship readers are unsupported by this runtime. Catalog identity remains available above.
          </div>
        ) : activePage?.rows_state === 'gap' ? (
          <div className={css(emptyState(tok))}>
            This exact relationship root is unavailable or failed. No empty-state inference is made.
          </div>
        ) : activePage && activePage.rows.length === 0 ? (
          <div className={css(emptyState(tok))}>
            Exact empty publication: no {viewNoun(activeView).toLowerCase()} rows match this service and view.
          </div>
        ) : activePage ? (
          <div role="region" aria-label={`${viewNoun(activeView)} exact rows`} className={css({ overflowX: 'auto' })}>
            <table className={css({ width: '100%', borderCollapse: 'collapse', tableLayout: 'fixed', minWidth: '720px' })}>
              <thead>
                <tr>
                  {['Evidence', 'Contract / topic', 'Services & attribution', 'Classification', 'Citation'].map((label, index) => (
                    <th key={label} scope="col" className={css({ ...tableHead(tok), width: ['24%', '23%', '25%', '16%', '12%'][index] })}>{label}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {activePage.rows.map((row) => (
                  <RelationshipRowView
                    key={`${row.repository}:${row.projection_digest}`}
                    row={row}
                    onCitation={(selected) => readCitation(selected, activePage.roots[0])}
                  />
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        {activePage && (
          <nav aria-label={`${viewNoun(activeView)} pages`} className={css({ minHeight: '44px', padding: '8px 12px', boxSizing: 'border-box', borderTop: `1px solid ${tok.cardBorder}`, backgroundColor: tok.bandBg, display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: '10px' })}>
            {cursor ? <a href={viewHref(activeView)} className={css(quietLink(tok))}>First page</a> : <span />}
            {activePage.pagination.next_cursor && (
              <a href={viewHref(activeView, activePage.pagination.next_cursor)} className={css(primaryLink(tok))}>Next exact page</a>
            )}
          </nav>
        )}
      </div>

      {(citationLoading || citationError || citation) && (
        <CitationPanel
          loading={citationLoading}
          error={citationError}
          citation={citation}
          onClose={() => {
            citationController.current?.abort()
            citationController.current = null
            setCitation(null)
            setCitationError('')
            setCitationLoading(false)
          }}
        />
      )}
    </section>
  )
}

function SummaryLink({ view, page, error, active, href, border }: {
  view: ServiceRelationshipView
  page?: ServiceRelationshipPage
  error?: string
  active: boolean
  href: string
  border: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const value = error ? 'Failed' : page?.rows_state === 'gap' ? 'Gap' : page ? String(page.coverage.scanned_references) : '…'
  return (
    <a href={href} aria-current={active ? 'page' : undefined} className={css({ display: 'block', minHeight: '70px', boxSizing: 'border-box', padding: '12px 14px', color: tok.textPrimary, textDecoration: 'none', backgroundColor: active ? tok.selectedLineBg : tok.pageBg, borderRight: border ? `1px solid ${tok.cardBorder}` : 'none', boxShadow: active ? `inset 0 -2px 0 ${tok.accent}` : 'none', ':hover': { backgroundColor: active ? tok.selectedLineBg : tok.hoverFill }, ':focus-visible': focusRing(tok), '@media screen and (max-width: 620px)': { minHeight: '58px', borderRight: 'none', borderBottom: border ? `1px solid ${tok.cardBorder}` : 'none' } })}>
      <div className={css({ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: '10px' })}>
        <span className={css({ fontSize: '12px', lineHeight: '17px', fontWeight: 600 })}>{viewTitle(view)}</span>
        <strong className={css({ fontSize: '18px', lineHeight: '22px', fontVariantNumeric: 'tabular-nums', color: error ? tok.statusRed : tok.textPrimary })}>{value}</strong>
      </div>
      <span className={css({ display: 'block', marginTop: '4px', fontSize: '10.5px', lineHeight: '15px', color: tok.textTertiary })}>{viewDescription(view)}</span>
    </a>
  )
}

function RelationshipTableHeader({ view, page }: { view: ServiceRelationshipView; page?: ServiceRelationshipPage }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ padding: '11px 12px', backgroundColor: tok.bandBg, display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: '12px', flexWrap: 'wrap' })}>
      <div>
        <h4 className={css({ margin: 0, fontSize: '12px', lineHeight: '17px', color: tok.textPrimary })}>{viewTitle(view)}</h4>
        <span className={css({ fontSize: '10.5px', lineHeight: '15px', color: tok.textTertiary })}>{viewDescription(view)}</span>
      </div>
      {page?.coverage.truncated && <StateText label="Partial · admitted reference cap" color={tok.statusAmber} />}
    </div>
  )
}

function RelationshipRowView({ row, onCitation }: { row: ServiceRelationshipRow; onCitation: (row: ServiceRelationshipRow) => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const subject = row.evidence.operation || row.evidence.topic_spelling || row.lookup_key || 'No resolved contract identity'
  const placements = [row.source, row.target].filter((value): value is ServiceRelationshipPlacement => Boolean(value))
  return (
    <tr className={css({ borderTop: `1px solid ${tok.innerSep}`, ':hover': { backgroundColor: tok.hoverFill } })}>
      <td className={css(tableCell(tok))}>
        <code className={css(codeWrap(tok))}>{row.evidence.path}</code>
        <div className={css(metaLine(tok))}>lines {row.evidence.span.start_line}–{row.evidence.span.end_line} · {row.evidence.source_role}</div>
      </td>
      <td className={css(tableCell(tok))}>
        <div className={css({ fontSize: '11.5px', lineHeight: '17px', color: tok.textPrimary, overflowWrap: 'anywhere' })}>{subject}</div>
        <div className={css(metaLine(tok))}>{row.kind} · {row.plane}{row.evidence.declaration_path ? ` · ${row.evidence.declaration_path}` : ''}</div>
      </td>
      <td className={css(tableCell(tok))}>
        {row.counterpart_services.length > 0 ? (
          <div className={css({ display: 'flex', flexWrap: 'wrap', gap: '4px' })}>{row.counterpart_services.map((service) => <StateTag key={service} label={service} />)}</div>
        ) : <span className={css({ fontSize: '10.5px', color: tok.textTertiary })}>No accepted counterpart</span>}
        <div className={css({ display: 'grid', gap: '3px', marginTop: '6px' })}>
          {placements.map((placement) => <PlacementSummary key={placement.path} placement={placement} />)}
        </div>
      </td>
      <td className={css(tableCell(tok))}>
        <StateTag label={classificationLabel(row)} tone={classificationTone(row)} />
        <div className={css(metaLine(tok))}>{row.participation.join(' + ') || 'unattributed'}</div>
        {row.evidence.reason && <div className={css({ ...metaLine(tok), color: tok.statusAmber })}>{row.evidence.reason}</div>}
      </td>
      <td className={css(tableCell(tok))}>
        <button type="button" onClick={() => onCitation(row)} className={css(citationButton(tok))} aria-haspopup="dialog">View citation</button>
      </td>
    </tr>
  )
}

function PlacementSummary({ placement }: { placement: ServiceRelationshipPlacement }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const accepted = placement.claims.filter((claim) => claim.disposition === 'accepted')
  const posture = placement.unowned ? 'unowned' : accepted.length > 1 ? 'shared' : placement.claims.some((claim) => claim.disposition === 'conflict') ? 'ambiguous' : accepted.length === 1 ? 'owned' : 'non-accepted'
  return (
    <div className={css({ minWidth: 0 })}>
      <code className={css(codeWrap(tok))}>{placement.path}</code>
      <span className={css({ marginLeft: '5px', fontSize: '9.5px', lineHeight: '14px', color: posture === 'ambiguous' ? tok.statusRed : posture === 'unowned' ? tok.statusAmber : tok.textTertiary })}>{posture}</span>
      {placement.claims.length > 0 && (
        <div className={css(metaLine(tok))}>
          {placement.claims.map((claim) => `${claim.service_key}:${claim.disposition}[${claim.roles.map((role) => `${role.role}/${role.origin}`).join(',')}]`).join(' · ')}
        </div>
      )}
    </div>
  )
}

function GapLedger({ detail, pages, errors, snapshot }: {
  detail: ServiceDetail
  pages: PageMap
  errors: ErrorMap
  snapshot: SnapshotState
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const root = pages.dependencies?.roots[0] ?? pages.callers?.roots[0] ?? pages.topics?.roots[0]
  const gaps = []
  if (detail.service.status !== 'current') gaps.push(`catalog lifecycle: ${detail.service.status}`)
  if (detail.service.disposition !== 'accepted') gaps.push(`authority disposition: ${detail.service.disposition}`)
  if (Object.keys(errors).length > 0) gaps.push('one or more exact relationship reads failed')
  if (root?.state === 'failed' || root?.state === 'unavailable') gaps.push(`relationship root: ${root.state}${root.reason ? ` (${root.reason})` : ''}`)
  if (Object.values(pages).some((page) => page?.coverage.truncated)) gaps.push('relationship reference admission cap reached')
  if (snapshot.state === 'stale') gaps.push(snapshot.reason)
  if (gaps.length === 0) gaps.push('No explicit gap in the selected exact authority; completeness is still not claimed.')
  return (
    <details className={css({ marginTop: '12px', borderTop: `1px solid ${tok.innerSep}`, paddingTop: '10px' })}>
      <summary className={css({ cursor: 'pointer', fontSize: '11px', lineHeight: '16px', fontWeight: 600, color: tok.textSecondary, ':focus-visible': focusRing(tok) })}>Gaps and claim boundary</summary>
      <ul className={css({ margin: '7px 0 0', paddingLeft: '18px', color: tok.textTertiary, fontSize: '10.5px', lineHeight: '17px' })}>
        {gaps.map((gap) => <li key={gap}>{gap}</li>)}
        <li>Static source evidence only; no runtime topology, completeness, SLO, migration, or release claim.</li>
      </ul>
    </details>
  )
}

function CitationPanel({ loading, error, citation, onClose }: {
  loading: boolean
  error: string
  citation: ServiceRelationshipCitation | null
  onClose: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <aside role="dialog" aria-modal="false" aria-labelledby="relationship-citation-title" className={css({ borderTop: `1px solid ${tok.cardBorder}`, padding: '14px 16px 16px', backgroundColor: tok.bandBg })}>
      <div className={css({ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: '12px' })}>
        <div>
          <h4 id="relationship-citation-title" className={css({ margin: 0, fontSize: '12px', lineHeight: '17px', color: tok.textPrimary })}>Exact source citation</h4>
          {citation && <div className={css(metaLine(tok))}>{citation.evidence.path} · lines {citation.evidence.span.start_line}–{citation.evidence.span.end_line}</div>}
        </div>
        <button type="button" onClick={onClose} className={css(quietButton(tok))}>Close citation</button>
      </div>
      {loading && <div role="status" className={css({ marginTop: '10px', fontSize: '11px', color: tok.textSecondary })}>Reading immutable source span…</div>}
      {error && <div role="alert" className={css({ marginTop: '10px', fontSize: '11px', color: tok.statusRed })}>{error}</div>}
      {citation && (
        <>
          <div className={css({ marginTop: '10px', display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: '5px 16px', '@media screen and (max-width: 520px)': { gridTemplateColumns: '1fr' } })}>
            <CitationIdentity label="Generation" value={citation.generation} />
            <CitationIdentity label="Root" value={citation.root_digest} />
            <CitationIdentity label="Object" value={citation.evidence.object_id} />
            <CitationIdentity label="Content" value={citation.evidence.content_digest} />
          </div>
          <pre tabIndex={0} className={css({ margin: '10px 0 0', padding: '12px', maxHeight: '280px', overflow: 'auto', border: `1px solid ${tok.cardBorder}`, borderRadius: '6px', backgroundColor: tok.pageBg, color: tok.plainCode, fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '17px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', ':focus-visible': focusRing(tok) })}>{citation.content}</pre>
        </>
      )}
    </aside>
  )
}

function CitationIdentity({ label, value }: { label: string; value: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <div className={css({ minWidth: 0, fontSize: '10px', lineHeight: '15px', color: tok.textTertiary })}>{label} <code title={value} className={css({ fontFamily: FONTS.MONO, color: tok.textSecondary })}>{shortIdentity(value)}</code></div>
}

interface SnapshotState { state: string; reason: string }

function relationshipSnapshot(
  detail: ServiceDetail,
  available: boolean,
  loading: boolean,
  pages: PageMap,
  errors: ErrorMap,
): SnapshotState {
  if (!available) return { state: 'unsupported', reason: 'relationship runtime is not registered' }
  if (loading && Object.keys(pages).length === 0) return { state: 'loading', reason: 'reading exact roots' }
  if (Object.keys(errors).length > 0) return { state: 'failed', reason: 'one or more exact view reads failed' }
  if (views.some((view) => !pages[view])) return { state: 'unavailable', reason: 'an exact view is unavailable' }
  const roots = views.map((view) => pages[view]?.roots[0])
  if (roots.some((root) => !root)) return { state: 'unavailable', reason: 'relationship root receipt is absent' }
  const identities = new Set(roots.map((root) => `${root?.generation}\x00${root?.root_digest}`))
  if (identities.size !== 1) return { state: 'failed', reason: 'relationship views crossed publication identities; retry' }
  const failed = roots.find((root) => root?.state === 'failed')
  if (failed) return { state: 'failed', reason: failed.reason || 'service relationship partition failed' }
  const unavailable = roots.find((root) => root?.state === 'unavailable')
  if (unavailable) return { state: 'unavailable', reason: unavailable.reason || 'relationship root unavailable' }
  if (detail.service.status === 'stale') return { state: 'stale', reason: 'catalog active authority is stale against the desired generation' }
  if (detail.service.status === 'conflict') return { state: 'failed', reason: 'catalog authority is conflicting' }
  if (detail.service.status === 'unavailable' || detail.service.status === 'removed') {
    return { state: 'unavailable', reason: `catalog service state is ${detail.service.status}` }
  }
  const root = roots[0]
  if (root?.service_incarnation !== detail.service.incarnation || root.service_generation !== detail.service.desired_generation) {
    return { state: 'stale', reason: 'relationship authority does not match the current service incarnation and desired generation' }
  }
  if (Object.values(pages).some((page) => page?.coverage.truncated)) return { state: 'partial', reason: 'admitted relationship reference cap reached' }
  if (Object.values(pages).every((page) => page?.rows_state === 'empty')) return { state: 'empty', reason: 'exact root contains no matching relationship references' }
  return { state: 'current', reason: 'all views share the current service authority' }
}

function validateRelationshipPage(
  detail: ServiceDetail,
  view: ServiceRelationshipView,
  page: ServiceRelationshipPage,
): string {
  const repository = detail.repository.repository
  if (page.query.view !== view || page.query.service_key !== detail.service.key ||
      page.query.repositories.length !== 1 || page.query.repositories[0] !== repository) {
    return 'relationship response query authority differs from the requested service'
  }
  if (page.roots.length !== 1 || page.roots[0].repository !== repository ||
      page.roots[0].service_key !== detail.service.key) {
    return 'relationship response root authority differs from the requested service'
  }
  if (page.rows.length > RELATIONSHIP_PAGE_SIZE || page.pagination.returned !== page.rows.length ||
      page.coverage.returned_rows !== page.rows.length || page.coverage.scanned_references < page.rows.length) {
    return 'relationship response count or page bound is invalid'
  }
  const root = page.roots[0]
  if (page.rows.some((row) => row.repository !== repository || row.service_key !== detail.service.key ||
      row.service_incarnation !== root.service_incarnation || row.service_generation !== root.service_generation ||
      !row.citation)) {
    return 'relationship row authority differs from its exact root'
  }
  return ''
}

function validateCitation(
  row: ServiceRelationshipRow,
  root: ServiceRelationshipPage['roots'][number],
  citation: ServiceRelationshipCitation,
): string {
  const span = citation.evidence.span
  const expectedSpan = row.evidence.span
  if (citation.schema !== 'phebs-service-relationship-citation-v1' ||
      citation.repository !== row.repository || citation.generation !== root.generation ||
      citation.root_digest !== root.root_digest || citation.projection.digest !== row.projection_digest ||
      citation.projection.posting_digest !== row.posting_digest ||
      citation.evidence.posting_digest !== row.evidence.posting_digest ||
      citation.evidence.path !== row.evidence.path ||
      citation.evidence.object_id !== row.evidence.object_id ||
      citation.evidence.content_digest !== row.evidence.content_digest ||
      span.start_byte !== expectedSpan.start_byte || span.end_byte !== expectedSpan.end_byte ||
      span.start_line !== expectedSpan.start_line || span.end_line !== expectedSpan.end_line) {
    return 'citation response authority differs from the selected exact relationship row'
  }
  return ''
}

function OverviewState({ state, reason }: SnapshotState) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <span title={reason} className={css({ display: 'inline-flex', alignItems: 'center', gap: '6px', minHeight: '24px', padding: '0 8px', border: `1px solid ${tok.cardBorder}`, borderRadius: '5px', fontSize: '10.5px', lineHeight: '15px', color: tok.textSecondary })}><span aria-hidden="true" className={css({ width: '7px', height: '7px', borderRadius: '50%', backgroundColor: snapshotColor(state, tok) })} />{titleCase(state)} · {reason}</span>
}

function StateText({ label, color }: { label: string; color: string }) {
  const [css] = useStyletron()
  return <span className={css({ fontSize: '10.5px', lineHeight: '15px', color })}>{label}</span>
}

function StateTag({ label, tone = 'neutral' }: { label: string; tone?: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const color = tone === 'danger' ? tok.statusRed : tone === 'warning' ? tok.statusAmber : tone === 'good' ? tok.statusGreen : tok.textSecondary
  return <span className={css({ display: 'inline-flex', minHeight: '18px', alignItems: 'center', padding: '0 5px', border: `1px solid ${tok.cardBorder}`, borderRadius: '4px', color, backgroundColor: tok.pageBg, fontSize: '9.5px', lineHeight: '13px', overflowWrap: 'anywhere' })}>{label}</span>
}

function classificationLabel(row: ServiceRelationshipRow): string {
  if (row.source.unowned || row.target?.unowned) return 'Unowned'
  const claims = [...row.source.claims, ...(row.target?.claims ?? [])]
  if (claims.some((claim) => claim.disposition === 'conflict')) return 'Ambiguous'
  if ([row.source, row.target].some((placement) =>
    placement && placement.claims.filter((claim) => claim.disposition === 'accepted').length > 1,
  )) return 'Shared'
  return titleCase(row.class)
}

function classificationTone(row: ServiceRelationshipRow): string {
  const label = classificationLabel(row).toLowerCase()
  if (label === 'ambiguous' || label === 'conflict' || label === 'failed') return 'danger'
  if (label === 'unowned' || label === 'unresolved' || label === 'unsupported' || label === 'partial') return 'warning'
  if (label === 'resolved') return 'good'
  return 'neutral'
}

function viewTitle(view: ServiceRelationshipView): string {
  if (view === 'dependencies') return 'Contracts used & dependencies'
  if (view === 'callers') return 'Contracts provided, callers & dependents'
  return 'Topics'
}

function viewDescription(view: ServiceRelationshipView): string {
  if (view === 'dependencies') return 'RPC source participation and accepted counterpart services'
  if (view === 'callers') return 'RPC target participation and accepted caller services'
  return 'Kafka producer and consumer source evidence'
}

function viewNoun(view: ServiceRelationshipView): string {
  if (view === 'dependencies') return 'Contracts used and dependencies'
  if (view === 'callers') return 'Contracts provided, callers, and dependents'
  return 'Topics'
}

function snapshotColor(state: string, tok: PhebsTokens): string {
  if (state === 'current' || state === 'empty') return tok.statusGreen
  if (state === 'failed') return tok.statusRed
  if (state === 'partial' || state === 'stale') return tok.statusAmber
  if (state === 'unsupported') return tok.gutter
  return tok.statusBlue
}

function titleCase(value: string): string {
  return value ? value[0].toUpperCase() + value.slice(1).replaceAll('_', ' ') : value
}

function boundedError(cause: unknown): string {
  const message = String(cause).replace(/^Error:\s*/, '')
  return message.length <= 512 ? message : `${message.slice(0, 511)}…`
}

function shortIdentity(value: string): string {
  return value.length <= 30 ? value : `${value.slice(0, 18)}…${value.slice(-8)}`
}

function focusRing(tok: PhebsTokens) {
  return { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' }
}

function emptyState(tok: PhebsTokens) {
  return { padding: '28px 16px', fontSize: '11.5px', lineHeight: '18px', color: tok.textTertiary }
}

function tableHead(tok: PhebsTokens) {
  return { padding: '7px 9px', textAlign: 'left' as const, borderTop: `1px solid ${tok.cardBorder}`, backgroundColor: tok.bandBg, color: tok.textTertiary, fontSize: '9.5px', lineHeight: '14px', fontWeight: 600 }
}

function tableCell(tok: PhebsTokens) {
  return { padding: '9px', verticalAlign: 'top' as const, color: tok.textSecondary, fontSize: '10.5px', lineHeight: '16px', overflowWrap: 'anywhere' as const }
}

function codeWrap(tok: PhebsTokens) {
  return { fontFamily: FONTS.MONO, fontSize: '10px', lineHeight: '15px', color: tok.textSecondary, whiteSpace: 'normal' as const, overflowWrap: 'anywhere' as const }
}

function metaLine(tok: PhebsTokens) {
  return { marginTop: '3px', fontSize: '9.5px', lineHeight: '14px', color: tok.textTertiary, overflowWrap: 'anywhere' as const }
}

function citationButton(tok: PhebsTokens) {
  return { minHeight: '30px', padding: '0 8px', border: `1px solid ${tok.cardBorder}`, borderRadius: '5px', backgroundColor: tok.pageBg, color: tok.selectedText, fontSize: '10.5px', cursor: 'pointer', ':hover': { backgroundColor: tok.hoverFill }, ':focus-visible': focusRing(tok) }
}

function quietButton(tok: PhebsTokens) {
  return { minHeight: '30px', padding: '0 8px', border: `1px solid ${tok.cardBorder}`, borderRadius: '5px', backgroundColor: tok.pageBg, color: tok.textSecondary, fontSize: '10.5px', cursor: 'pointer', ':hover': { color: tok.textPrimary }, ':focus-visible': focusRing(tok) }
}

function quietLink(tok: PhebsTokens) {
  return { ...quietButton(tok), display: 'inline-flex', alignItems: 'center', boxSizing: 'border-box' as const, textDecoration: 'none' }
}

function primaryLink(tok: PhebsTokens) {
  return { display: 'inline-flex', minHeight: '30px', padding: '0 9px', alignItems: 'center', boxSizing: 'border-box' as const, borderRadius: '5px', backgroundColor: tok.textPrimary, color: tok.pageBg, textDecoration: 'none', fontSize: '10.5px', fontWeight: 600, ':hover': { opacity: 0.84 }, ':focus-visible': focusRing(tok) }
}
