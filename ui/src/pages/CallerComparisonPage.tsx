import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND, SIZE as BUTTON_SIZE } from 'baseui/button'
import { Input } from 'baseui/input'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import {
  fetchCallerComparison,
  fetchContractCatalog,
  type CallerComparisonPage as ComparisonResponse,
  type CallerComparisonRow,
  type CallerComparisonSide,
  type CallerComparisonSnapshot,
  type CallerMapEndpoint,
  type CallerMapRow,
  type ContractCatalogItem,
  type ContractCatalogList,
} from '../api'
import { OpenIcon } from '../icons'
import { href } from '../router'
import { FONTS, usePhebsTokens } from '../theme'
import { isAbortError } from '../util'
import ExactCallerCitation from './ExactCallerCitation'
import { ClaimBoundary, StatusChip } from '../components/kit'
import { COMPARISON_UNAVAILABLE_ADDENDUM } from '../caveats'

const pageSize = 100
const maxCursorHistory = 500

type ComparisonFilters = {
  unit: string
  owner: string
  pathPrefix: string
  codeRole: string
  tier: string
  freshness: 'any' | 'fresh' | 'stale'
  resolution: 'any' | 'scip' | 'syntax' | 'unresolved'
  ordering: 'source' | 'unit'
  level: 'occurrence' | 'unit'
  classification: '' | 'old_only_evidence' | 'both_evidence' | 'new_only_evidence' | 'unresolved'
}

const emptyFilters: ComparisonFilters = {
  unit: '',
  owner: '',
  pathPrefix: '',
  codeRole: '',
  tier: '',
  freshness: 'any',
  resolution: 'any',
  ordering: 'source',
  level: 'occurrence',
  classification: '',
}

export default function CallerComparisonPage({ params }: { params: URLSearchParams }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const oldEndpoint = endpointFromParams(params, 'old')
  const replacementEndpoint = endpointFromParams(params, 'replacement')

  if (!oldEndpoint) {
    return (
      <div className={css({ maxWidth: '920px', margin: '0 auto' })}>
        <Notification
          kind={NOTIFICATION_KIND.negative}
          overrides={{ Body: { style: { width: 'auto', margin: 0 } } }}
        >
          Caller comparison requires an exact old endpoint selected from Caller Map.
        </Notification>
        <a href="#/contracts" className={css({
          display: 'inline-flex',
          marginTop: '14px',
          color: tok.accent,
          fontSize: '13px',
        })}>
          Open Contract Atlas
        </a>
      </div>
    )
  }
  if (!replacementEndpoint) {
    return <ReplacementPicker oldEndpoint={oldEndpoint} />
  }
  return (
    <ComparisonResults
      oldEndpoint={oldEndpoint}
      replacementEndpoint={replacementEndpoint}
    />
  )
}

function ReplacementPicker({ oldEndpoint }: { oldEndpoint: CallerMapEndpoint }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [draftRepository, setDraftRepository] = useState('')
  const [repository, setRepository] = useState('')
  const [protocol, setProtocol] = useState(oldEndpoint.protocol)
  const [catalog, setCatalog] = useState<ContractCatalogList | null>(null)
  const [cursors, setCursors] = useState<string[]>([''])
  const [pageIndex, setPageIndex] = useState(0)
  const [reload, setReload] = useState(0)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const request = useRef<AbortController | null>(null)
  const cursor = cursors[pageIndex] ?? ''

  useEffect(() => {
    request.current?.abort()
    const controller = new AbortController()
    request.current = controller
    setLoading(true)
    setError('')
    setCatalog(null)
    fetchContractCatalog(
      { repository: repository || undefined, protocol: protocol || undefined },
      pageSize,
      cursor,
      controller.signal,
    )
      .then(setCatalog)
      .catch((cause) => {
        if (!isAbortError(cause)) {
          setError(cause instanceof Error ? cause.message : String(cause))
        }
      })
      .finally(() => {
        if (request.current === controller) {
          request.current = null
          setLoading(false)
        }
      })
    return () => controller.abort()
  }, [repository, protocol, cursor, reload])

  const operations = (catalog?.items ?? []).filter(
    (item) => item.kind === 'operation' && item.operation,
  )
  const reset = () => {
    setCursors([''])
    setPageIndex(0)
    // A stale first page already has the empty cursor and page zero. Move an
    // explicit generation as well so "Restart" always performs a new read.
    setReload((generation) => generation + 1)
  }
  return (
    <div className={css({ maxWidth: '1120px', margin: '0 auto' })}>
      <header className={css({ marginBottom: '16px' })}>
        <div className={css({
          color: tok.textTertiary,
          fontSize: '11px',
          fontWeight: 650,
          letterSpacing: '0.05em',
          textTransform: 'uppercase',
        })}>
          Migration comparison
        </div>
        <h1 className={css({ margin: '7px 0 0', color: tok.textPrimary, fontSize: '22px' })}>
          Select a replacement endpoint
        </h1>
        <EndpointIdentity label="Old endpoint" endpoint={oldEndpoint} />
      </header>
      <form
        aria-label="Replacement endpoint filters"
        onSubmit={(event) => {
          event.preventDefault()
          reset()
          setRepository(draftRepository.trim())
        }}
        className={css({
          display: 'grid',
          gridTemplateColumns: 'minmax(220px, 1fr) 180px auto',
          gap: '9px',
          alignItems: 'end',
          padding: '12px',
          border: `1px solid ${tok.cardBorder}`,
          backgroundColor: tok.bandBg,
          '@media screen and (max-width: 680px)': { gridTemplateColumns: '1fr' },
        })}
      >
        <label className={css(labelStyle(tok.textSecondary))}>
          Repository
          <Input
            aria-label="Replacement repository"
            value={draftRepository}
            onChange={(event) => setDraftRepository(event.currentTarget.value)}
            placeholder="github.com/acme/contracts"
            size="compact"
            overrides={{ Root: { style: { borderRadius: 0 } } }}
          />
        </label>
        <label className={css(labelStyle(tok.textSecondary))}>
          Protocol
          <select
            aria-label="Replacement protocol"
            value={protocol}
            onChange={(event) => {
              setProtocol(event.currentTarget.value)
              reset()
            }}
            className={css(selectStyle(tok))}
          >
            <option value="">All protocols</option>
            <option value="protobuf">Protobuf</option>
            <option value="thrift">Thrift</option>
          </select>
        </label>
        <Button type="submit" size={BUTTON_SIZE.compact}>Search</Button>
      </form>
      {error && (
        <Notification
          kind={NOTIFICATION_KIND.negative}
          overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}
        >
          <div>
            Replacement discovery failed. {error}
            <div className={css({ marginTop: '8px' })}>
              <Button
                type="button"
                size={BUTTON_SIZE.mini}
                kind={BUTTON_KIND.secondary}
                onClick={() => setReload((value) => value + 1)}
              >
                Retry
              </Button>
            </div>
          </div>
        </Notification>
      )}
      {loading && <Loading label="Loading replacement endpoints" />}
      {!loading && catalog && (
        <>
          <div className={css({
            marginTop: '14px',
            border: `1px solid ${tok.cardBorder}`,
            backgroundColor: tok.pageBg,
          })}>
            {operations.length === 0 ? (
              <div className={css({ padding: '22px', color: tok.textTertiary, fontSize: '12px' })}>
                No operations on this catalog page. Continue or narrow the repository.
              </div>
            ) : operations.map((operation) => (
              <a
                key={catalogItemID(operation)}
                href={comparisonHref(oldEndpoint, itemEndpoint(operation))}
                className={css({
                  display: 'grid',
                  gridTemplateColumns: 'minmax(260px, 1fr) minmax(180px, 0.7fr)',
                  gap: '12px',
                  padding: '11px 12px',
                  borderBottom: `1px solid ${tok.innerSep}`,
                  color: tok.textPrimary,
                  textDecoration: 'none',
                  ':last-child': { borderBottom: 'none' },
                  ':hover': { backgroundColor: tok.selectedLineBg },
                  ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
                  '@media screen and (max-width: 680px)': { gridTemplateColumns: '1fr' },
                })}
              >
                <span className={css({ fontFamily: FONTS.MONO, fontSize: '12px', overflowWrap: 'anywhere' })}>
                  {operation.operation}
                </span>
                <span className={css({ color: tok.textTertiary, fontSize: '10px', overflowWrap: 'anywhere' })}>
                  {operation.protocol} · {operation.repository} · {shortID(operation.declaration_lineage)}
                </span>
              </a>
            ))}
          </div>
          <Pager
            pageIndex={pageIndex}
            nextCursor={catalog.pagination.next_cursor ?? ''}
            canRememberNext={cursors.length < maxCursorHistory}
            onPrevious={() => setPageIndex((value) => Math.max(0, value - 1))}
            onNext={() => {
              const next = catalog.pagination.next_cursor
              if (!next || cursors.length >= maxCursorHistory) return
              setCursors((values) => [...values.slice(0, pageIndex + 1), next])
              setPageIndex((value) => value + 1)
            }}
          />
        </>
      )}
    </div>
  )
}

function ComparisonResults({
  oldEndpoint,
  replacementEndpoint,
}: {
  oldEndpoint: CallerMapEndpoint
  replacementEndpoint: CallerMapEndpoint
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [draft, setDraft] = useState<ComparisonFilters>(emptyFilters)
  const [filters, setFilters] = useState<ComparisonFilters>(emptyFilters)
  const [page, setPage] = useState<ComparisonResponse | null>(null)
  const [cursors, setCursors] = useState<string[]>([''])
  const [pageIndex, setPageIndex] = useState(0)
  const [reload, setReload] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const request = useRef<AbortController | null>(null)
  const endpointKey = [
    ...Object.values(oldEndpoint),
    ...Object.values(replacementEndpoint),
  ].join('\u0000')
  const [paginationEndpointKey, setPaginationEndpointKey] = useState(endpointKey)
  // Reset the effective cursor during the first render of a changed endpoint
  // pair, before effects can issue a request with the preceding pair's cursor.
  const cursor = paginationEndpointKey === endpointKey
    ? cursors[pageIndex] ?? ''
    : ''

  useEffect(() => {
    setCursors([''])
    setPageIndex(0)
    setPaginationEndpointKey(endpointKey)
  }, [endpointKey])

  useEffect(() => {
    request.current?.abort()
    const controller = new AbortController()
    request.current = controller
    setLoading(true)
    setError('')
    setPage(null)
    fetchCallerComparison(
      oldEndpoint,
      replacementEndpoint,
      {
        unit: filters.unit || undefined,
        owner: filters.owner || undefined,
        path_prefix: filters.pathPrefix || undefined,
        code_role: filters.codeRole || undefined,
        tier: filters.tier || undefined,
        freshness: filters.freshness,
        resolution: filters.resolution,
        ordering: filters.ordering,
        level: filters.level,
        classification: filters.classification || undefined,
      },
      pageSize,
      cursor,
      controller.signal,
    )
      .then(setPage)
      .catch((cause) => {
        if (!isAbortError(cause)) {
          setError(cause instanceof Error ? cause.message : String(cause))
        }
      })
      .finally(() => {
        if (request.current === controller) {
          request.current = null
          setLoading(false)
        }
      })
    return () => controller.abort()
  }, [endpointKey, filters, cursor, reload]) // eslint-disable-line react-hooks/exhaustive-deps

  const reset = () => {
    setCursors([''])
    setPageIndex(0)
    // A stale first page already has the empty cursor and page zero. Move an
    // explicit generation as well so "Restart" always performs a new read.
    setReload((generation) => generation + 1)
  }
  const stale = error.startsWith('409:')
  return (
    <div
      data-testid="caller-comparison-page"
      data-responsive-layout="desktop-columns-mobile-cards"
      className={css({ maxWidth: '1320px', margin: '0 auto' })}
    >
      <header className={css({ marginBottom: '15px' })}>
        <div className={css({
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          gap: '12px',
          '@media screen and (max-width: 680px)': { flexDirection: 'column' },
        })}>
          <div>
            <div className={css({
              color: tok.textTertiary,
              fontSize: '11px',
              fontWeight: 650,
              letterSpacing: '0.05em',
              textTransform: 'uppercase',
            })}>
              Migration comparison
            </div>
            <h1 className={css({ margin: '7px 0 0', color: tok.textPrimary, fontSize: '22px' })}>
              Static caller evidence
            </h1>
          </div>
          <a
            href={comparisonHref(oldEndpoint)}
            className={css(linkStyle(tok))}
          >
            Change replacement
          </a>
        </div>
        <div className={css({
          display: 'grid',
          gridTemplateColumns: '1fr 1fr',
          gap: '10px',
          marginTop: '12px',
          '@media screen and (max-width: 680px)': { gridTemplateColumns: '1fr' },
        })}>
          <EndpointIdentity label="Old endpoint" endpoint={oldEndpoint} snapshot={page?.old} />
          <EndpointIdentity label="Replacement endpoint" endpoint={replacementEndpoint} snapshot={page?.replacement} />
        </div>
      </header>
      <ComparisonFilterPanel
        draft={draft}
        loading={loading}
        onChange={setDraft}
        onApply={(event) => {
          event.preventDefault()
          setFilters({
            ...draft,
            unit: draft.unit.trim(),
            owner: draft.owner.trim(),
            pathPrefix: draft.pathPrefix.trim(),
          })
          reset()
        }}
        onClear={() => {
          setDraft(emptyFilters)
          setFilters(emptyFilters)
          reset()
        }}
      />
      {error && (
        <Notification
          kind={NOTIFICATION_KIND.negative}
          overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}
        >
          <div>
            <strong>{stale ? 'Comparison snapshot changed.' : 'Comparison request failed.'}</strong>
            <div className={css({ marginTop: '4px', overflowWrap: 'anywhere' })}>{error}</div>
            <div className={css({ display: 'flex', gap: '8px', marginTop: '9px' })}>
              <Button
                type="button"
                size={BUTTON_SIZE.mini}
                kind={BUTTON_KIND.secondary}
                onClick={() => setReload((value) => value + 1)}
              >
                Retry this page
              </Button>
              {stale && (
                <Button
                  type="button"
                  size={BUTTON_SIZE.mini}
                  kind={BUTTON_KIND.secondary}
                  onClick={reset}
                >
                  Restart from first page
                </Button>
              )}
            </div>
          </div>
        </Notification>
      )}
      {loading && <Loading label="Loading caller comparison" />}
      {!loading && page && (
        <>
          <ComparisonProgress page={page} pageIndex={pageIndex} />
          <ComparisonRows page={page} onRefreshRows={() => setReload((current) => current + 1)} />
          {page.matching_rows_state !== 'unavailable' && (
            <Pager
              pageIndex={pageIndex}
              nextCursor={page.pagination.next_cursor ?? ''}
              canRememberNext={cursors.length < maxCursorHistory}
              onPrevious={() => setPageIndex((value) => Math.max(0, value - 1))}
              onNext={() => {
                const next = page.pagination.next_cursor
                if (!next || cursors.length >= maxCursorHistory) return
                setCursors((values) => [...values.slice(0, pageIndex + 1), next])
                setPageIndex((value) => value + 1)
              }}
            />
          )}
          <div className={css({ marginTop: '12px' })}>
            <ClaimBoundary caveat={page.caveat} />
          </div>
        </>
      )}
    </div>
  )
}

function ComparisonFilterPanel({
  draft,
  loading,
  onChange,
  onApply,
  onClear,
}: {
  draft: ComparisonFilters
  loading: boolean
  onChange: (filters: ComparisonFilters) => void
  onApply: (event: React.FormEvent) => void
  onClear: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <form
      aria-label="Caller comparison filters"
      onSubmit={onApply}
      className={css({
        display: 'grid',
        gridTemplateColumns: 'repeat(5, minmax(130px, 1fr))',
        gap: '9px',
        padding: '12px',
        marginBottom: '14px',
        border: `1px solid ${tok.cardBorder}`,
        backgroundColor: tok.bandBg,
        '@media screen and (max-width: 980px)': { gridTemplateColumns: '1fr 1fr' },
        '@media screen and (max-width: 560px)': { gridTemplateColumns: '1fr' },
      })}
    >
      <TextFilter label="Unit" value={draft.unit} onChange={(unit) => onChange({ ...draft, unit })} />
      <TextFilter label="Owner" value={draft.owner} onChange={(owner) => onChange({ ...draft, owner })} />
      <TextFilter label="Path prefix" value={draft.pathPrefix} onChange={(pathPrefix) => onChange({ ...draft, pathPrefix })} />
      <SelectFilter label="Level" value={draft.level} options={[
        ['occurrence', 'Occurrence'],
        ['unit', 'Unit'],
      ]} onChange={(level) => onChange({ ...draft, level: level as ComparisonFilters['level'] })} />
      <SelectFilter label="Classification" value={draft.classification} options={[
        ['', 'All classifications'],
        ['old_only_evidence', 'Old only evidence'],
        ['both_evidence', 'Both evidence'],
        ['new_only_evidence', 'New only evidence'],
        ['unresolved', 'Unresolved'],
      ]} onChange={(classification) => onChange({
        ...draft,
        classification: classification as ComparisonFilters['classification'],
      })} />
      <SelectFilter label="Code role" value={draft.codeRole} options={[
        ['', 'Any role'], ['production', 'Production'], ['test', 'Test'],
        ['mock', 'Mock'], ['generated', 'Generated'], ['vendor', 'Vendor'],
      ]} onChange={(codeRole) => onChange({ ...draft, codeRole })} />
      <SelectFilter label="Tier" value={draft.tier} options={[
        ['', 'Any tier'], ['exact', 'Exact'], ['derived', 'Derived'],
        ['heuristic', 'Heuristic'], ['unresolved', 'Unresolved'],
      ]} onChange={(tier) => onChange({ ...draft, tier })} />
      <SelectFilter label="Freshness" value={draft.freshness} options={[
        ['any', 'Any freshness'], ['fresh', 'Fresh'], ['stale', 'Stale'],
      ]} onChange={(freshness) => onChange({
        ...draft,
        freshness: freshness as ComparisonFilters['freshness'],
      })} />
      <SelectFilter label="Resolution" value={draft.resolution} options={[
        ['any', 'Any resolution'], ['scip', 'SCIP'], ['syntax', 'Syntax'],
        ['unresolved', 'Unresolved'],
      ]} onChange={(resolution) => onChange({
        ...draft,
        resolution: resolution as ComparisonFilters['resolution'],
      })} />
      <SelectFilter label="Ordering" value={draft.ordering} options={[
        ['source', 'Source'], ['unit', 'Unit'],
      ]} onChange={(ordering) => onChange({
        ...draft,
        ordering: ordering as ComparisonFilters['ordering'],
      })} />
      <div className={css({
        display: 'flex',
        gap: '7px',
        gridColumn: '1 / -1',
        justifyContent: 'flex-end',
      })}>
        <Button type="submit" size={BUTTON_SIZE.compact} isLoading={loading}>Apply filters</Button>
        <Button type="button" size={BUTTON_SIZE.compact} kind={BUTTON_KIND.tertiary} onClick={onClear}>Clear</Button>
      </div>
    </form>
  )
}

function ComparisonProgress({ page, pageIndex }: { page: ComparisonResponse; pageIndex: number }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const start = page.rows.length === 0 ? 0 : pageIndex * pageSize + 1
  const end = pageIndex * pageSize + page.rows.length
  const unavailable = page.matching_rows_state === 'unavailable'
  return (
    <div
      data-testid="caller-comparison-progress"
      data-matching-rows-state={page.matching_rows_state ?? 'legacy'}
      data-tone={unavailable ? 'blue' : 'neutral'}
      className={css({
      padding: '10px 12px',
      border: `1px solid ${unavailable ? tok.status.unavailable.solid : tok.cardBorder}`,
      borderBottom: 'none',
      backgroundColor: tok.bandBg,
      color: tok.textSecondary,
      fontSize: '11px',
    })}>
      {unavailable
        ? 'Comparison rows and totals unavailable'
        : page.total_rows === 0
        ? 'No comparison rows matched both exact generation snapshots'
        : page.total_rows === undefined
        ? 'Comparison total unavailable'
        : `Rows ${start}–${end} of ${page.total_rows}`}
      <span className={css({ marginLeft: '8px', color: tok.textTertiary })}>
        {unavailable
          ? 'No partial page, classification, or zero-caller conclusion is shown.'
          : page.pagination.complete
          ? `Exact snapshot exhausted on page ${pageIndex + 1}.`
          : `Traversed through at least ${end}; continue this exact snapshot.`}
      </span>
    </div>
  )
}

function ComparisonRows({ page, onRefreshRows }: { page: ComparisonResponse; onRefreshRows: () => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const rows = page.rows
  if (rows.length === 0) {
    const unavailable = page.matching_rows_state === 'unavailable'
    return (
      <div className={css({
        padding: '26px',
        border: `1px solid ${tok.cardBorder}`,
        color: tok.textTertiary,
        fontSize: '12px',
        lineHeight: '18px',
      })}>
        {unavailable
          ? `Comparison evidence is unavailable. ${COMPARISON_UNAVAILABLE_ADDENDUM}`
          : 'No comparison rows matched these filters within both exact generation snapshots. This does not establish runtime completion, absence, or decommissioning safety.'}
      </div>
    )
  }
  return (
    <div className={css({ border: `1px solid ${tok.cardBorder}` })}>
      {rows.map((row) => (
        <article
          key={row.key}
          data-testid="caller-comparison-row"
          data-comparison-key={row.key}
          className={css({
            padding: '12px',
            borderBottom: `1px solid ${tok.innerSep}`,
            ':last-child': { borderBottom: 'none' },
          })}
        >
          <div className={css({
            display: 'flex',
            alignItems: 'center',
            gap: '7px',
            flexWrap: 'wrap',
          })}>
            <StatusChip tone={CLASSIFICATION_TONE[row.classification]}>
              {CLASSIFICATION_LABEL[row.classification]}
            </StatusChip>
            <span className={css({ color: tok.textTertiary, fontSize: '10px' })}>
              {row.level}
            </span>
          </div>
          <div className={css({
            marginTop: '6px',
            color: tok.textPrimary,
            fontFamily: FONTS.MONO,
            fontSize: '10px',
            lineHeight: '16px',
            overflowWrap: 'anywhere',
          })}>
            {row.key}
          </div>
          {row.unit && (
            <div className={css({ marginTop: '5px', color: tok.textTertiary, fontSize: '10px' })}>
              unit {row.unit.id}
              {row.unit.logical_services?.length ? ` · ${row.unit.logical_services.join(', ')}` : ''}
              {row.unit.owners?.length ? ` · ${row.unit.owners.join(', ')}` : ''}
            </div>
          )}
          <div className={css({
            display: 'grid',
            gridTemplateColumns: '1fr 1fr',
            gap: '10px',
            marginTop: '9px',
            '@media screen and (max-width: 680px)': { gridTemplateColumns: '1fr' },
          })}>
            <ComparisonSide label="Old evidence" side={row.old} onRefreshRows={onRefreshRows} />
            <ComparisonSide label="Replacement evidence" side={row.replacement} onRefreshRows={onRefreshRows} />
          </div>
        </article>
      ))}
    </div>
  )
}

function ComparisonSide({ label, side, onRefreshRows }: { label: string; side: CallerComparisonSide; onRefreshRows: () => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section aria-label={label} className={css({
      padding: '9px',
      border: `1px solid ${tok.cardBorder}`,
      backgroundColor: tok.bandBg,
    })}>
      <div className={css({ color: tok.textSecondary, fontSize: '10px', fontWeight: 650 })}>
        {label} · {side.occurrence_count}
      </div>
      {side.rows.length === 0 ? (
        <div className={css({ marginTop: '5px', color: tok.textTertiary, fontSize: '10px' })}>
          No retained occurrence sample on this side; runtime absence is not inferred.
        </div>
      ) : side.rows.map((row) => (
        <div key={rowIdentity(row)} className={css({ marginTop: '6px' })}>
          <ExactCallerCitation source={row.source} onRefreshRows={onRefreshRows} />
        </div>
      ))}
      {side.rows_truncated && (
        <div className={css({ marginTop: '5px', color: tok.status.stale.text, fontSize: '9px' })}>
          Citation sample is bounded; {side.occurrence_count - side.rows.length} more occurrence(s).
        </div>
      )}
    </section>
  )
}

function EndpointIdentity({
  label,
  endpoint,
  snapshot,
}: {
  label: string
  endpoint: CallerMapEndpoint
  snapshot?: CallerComparisonSnapshot
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const declarationSource = snapshot?.declaration?.sources[0]
  const exact = snapshot?.matching_rows_state === 'exact'
  const generation = snapshot?.generation
  return (
    <section className={css({
      padding: '10px 11px',
      border: `1px solid ${tok.cardBorder}`,
      backgroundColor: tok.bandBg,
      minWidth: 0,
    })}>
      <div className={css({
        color: tok.textTertiary,
        fontSize: '9px',
        fontWeight: 650,
        textTransform: 'uppercase',
      })}>
        {label}
      </div>
      <div className={css({
        marginTop: '4px',
        color: tok.textPrimary,
        fontFamily: FONTS.MONO,
        fontSize: '11px',
        lineHeight: '17px',
        overflowWrap: 'anywhere',
      })}>
        {endpoint.operation}
      </div>
      <div className={css({ marginTop: '3px', color: tok.textTertiary, fontSize: '9px', overflowWrap: 'anywhere' })}>
        {endpoint.protocol} · {endpoint.repository} · {shortID(endpoint.declaration_lineage)}
      </div>
      {generation && (
        <div
          data-testid="caller-comparison-generation"
          data-matching-rows-state={snapshot?.matching_rows_state ?? ''}
          data-tone={exact ? 'green' : 'blue'}
          className={css({
            display: 'grid',
            gap: '3px',
            marginTop: '7px',
            paddingTop: '6px',
            borderTop: `1px solid ${tok.innerSep}`,
            color: tok.textTertiary,
            fontSize: '9px',
            lineHeight: '14px',
          })}
        >
          <span className={css({ color: exact ? tok.status.current.text : tok.status.unavailable.text, fontWeight: 650 })}>
            {exact ? 'exact rows' : 'rows unavailable'} · generation {generation.state}
          </span>
          {generation.reason && (
            <span>{generation.reason}. This side does not support an absence conclusion.</span>
          )}
          <span className={css({ fontFamily: FONTS.MONO, overflowWrap: 'anywhere' })}>
            revision {generation.publication_revision ?? 'unavailable'}
            {generation.commit ? ` · commit ${shortID(generation.commit)}` : ''}
            {generation.generation_digest
              ? ` · generation ${shortID(generation.generation_digest)}`
              : ''}
          </span>
        </div>
      )}
      {!generation && snapshot && (snapshot.coverage_digest || snapshot.attribution_digest) && (
        <div className={css({ marginTop: '5px', color: tok.textTertiary, fontFamily: FONTS.MONO, fontSize: '8px' })}>
          {snapshot.coverage_digest && `coverage ${shortID(snapshot.coverage_digest)}`}
          {snapshot.coverage_digest && snapshot.attribution_digest && ' · '}
          {snapshot.attribution_digest && `attribution ${shortID(snapshot.attribution_digest)}`}
        </div>
      )}
      {declarationSource && (
        <a
          aria-label={`${label} declaration`}
          href={href('/file', {
            repo: declarationSource.repository,
            path: declarationSource.path,
            ref: declarationSource.commit,
            L: String(declarationSource.start_line),
          })}
          className={css({
            display: 'inline-flex',
            alignItems: 'center',
            gap: '4px',
            marginTop: '6px',
            color: tok.accent,
            fontSize: '9px',
            textDecoration: 'none',
            ':hover': { textDecoration: 'underline' },
            ':focus-visible': { outline: `2px solid ${tok.accent}` },
          })}
        >
          Declaration <OpenIcon size={10} />
        </a>
      )}
    </section>
  )
}

function TextFilter({
  label,
  value,
  onChange,
}: {
  label: string
  value: string
  onChange: (value: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css(labelStyle(tok.textSecondary))}>
      {label}
      <Input
        aria-label={label}
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        size="compact"
        overrides={{ Root: { style: { borderRadius: 0 } } }}
      />
    </label>
  )
}

function SelectFilter({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: string
  options: [string, string][]
  onChange: (value: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css(labelStyle(tok.textSecondary))}>
      {label}
      <select
        aria-label={label}
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        className={css(selectStyle(tok))}
      >
        {options.map(([optionValue, optionLabel]) => (
          <option key={optionValue} value={optionValue}>{optionLabel}</option>
        ))}
      </select>
    </label>
  )
}

function Pager({
  pageIndex,
  nextCursor,
  canRememberNext,
  onPrevious,
  onNext,
}: {
  pageIndex: number
  nextCursor: string
  canRememberNext: boolean
  onPrevious: () => void
  onNext: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <nav aria-label="Comparison pagination" className={css({
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      gap: '10px',
      padding: '10px 12px',
      border: `1px solid ${tok.cardBorder}`,
      borderTop: 'none',
      backgroundColor: tok.bandBg,
    })}>
      <Button type="button" size={BUTTON_SIZE.compact} kind={BUTTON_KIND.secondary} disabled={pageIndex === 0} onClick={onPrevious}>
        Previous page
      </Button>
      <span className={css({ color: tok.textTertiary, fontSize: '10px' })}>Page {pageIndex + 1}</span>
      <Button type="button" size={BUTTON_SIZE.compact} kind={BUTTON_KIND.secondary} disabled={!nextCursor || !canRememberNext} onClick={onNext}>
        Next page
      </Button>
    </nav>
  )
}

function Loading({ label }: { label: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div role="status" aria-label={label} className={css({
      minHeight: '200px',
      display: 'grid',
      placeItems: 'center',
      border: `1px solid ${tok.cardBorder}`,
      backgroundColor: tok.bandBg,
    })}>
      <Spinner $size="small" />
    </div>
  )
}

const CLASSIFICATION_TONE: Record<CallerComparisonRow['classification'], 'amber' | 'green' | 'accent' | 'red'> = {
  old_only_evidence: 'amber',
  both_evidence: 'green',
  new_only_evidence: 'accent',
  unresolved: 'red',
}

// Sanctioned copy fix (audit F23): human-readable labels instead of raw enum keys.
const CLASSIFICATION_LABEL: Record<CallerComparisonRow['classification'], string> = {
  old_only_evidence: 'Old plane only',
  both_evidence: 'Both planes',
  new_only_evidence: 'New plane only',
  unresolved: 'Unresolved',
}

function endpointFromParams(params: URLSearchParams, prefix: string): CallerMapEndpoint | null {
  const endpoint = {
    protocol: params.get(`${prefix}_protocol`)?.trim() ?? '',
    repository: params.get(`${prefix}_repository`)?.trim() ?? '',
    declaration_lineage: params.get(`${prefix}_lineage`)?.trim() ?? '',
    operation: params.get(`${prefix}_operation`)?.trim() ?? '',
  }
  return Object.values(endpoint).every(Boolean) ? endpoint : null
}

function itemEndpoint(item: ContractCatalogItem): CallerMapEndpoint {
  return {
    protocol: item.protocol,
    repository: item.repository,
    declaration_lineage: item.declaration_lineage,
    operation: item.operation ?? '',
  }
}

function comparisonHref(oldEndpoint: CallerMapEndpoint, replacement?: CallerMapEndpoint) {
  const params: Record<string, string> = {
    old_protocol: oldEndpoint.protocol,
    old_repository: oldEndpoint.repository,
    old_lineage: oldEndpoint.declaration_lineage,
    old_operation: oldEndpoint.operation,
  }
  if (replacement) {
    params.replacement_protocol = replacement.protocol
    params.replacement_repository = replacement.repository
    params.replacement_lineage = replacement.declaration_lineage
    params.replacement_operation = replacement.operation
  }
  return href('/compare-callers', params)
}

function catalogItemID(item: ContractCatalogItem) {
  return [
    item.protocol,
    item.repository,
    item.declaration_lineage,
    item.operation,
  ].join('\u0000')
}

function rowIdentity(row: CallerMapRow) {
  return [
    row.source.repository,
    row.source.run_id,
    row.source.assertion_id,
    row.source.atom_id,
  ].join(':')
}

function shortID(value: string) {
  return value.length <= 18 ? value : `${value.slice(0, 10)}…${value.slice(-7)}`
}

function labelStyle(color: string) {
  return {
    display: 'grid',
    gap: '5px',
    color,
    fontSize: '10px',
    fontWeight: 650,
  } as const
}

function selectStyle(tok: ReturnType<typeof usePhebsTokens>) {
  return {
    height: '38px',
    boxSizing: 'border-box',
    padding: '0 28px 0 9px',
    border: `1px solid ${tok.cardBorder}`,
    borderRadius: 0,
    backgroundColor: tok.pageBg,
    color: tok.textPrimary,
    fontSize: '11px',
    ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
  } as const
}

function linkStyle(tok: ReturnType<typeof usePhebsTokens>) {
  return {
    minHeight: '34px',
    display: 'inline-flex',
    alignItems: 'center',
    padding: '0 11px',
    border: `1px solid ${tok.accent}`,
    color: tok.accent,
    fontSize: '11px',
    textDecoration: 'none',
    whiteSpace: 'nowrap',
    ':hover': { backgroundColor: tok.selectedLineBg },
    ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' },
  } as const
}
