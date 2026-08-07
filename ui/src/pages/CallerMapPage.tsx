import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND, SIZE as BUTTON_SIZE } from 'baseui/button'
import { Input } from 'baseui/input'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import {
  fetchContractCallers,
  type CallerMapEndpoint,
  type CallerMapPage as CallerMapResponse,
  type CallerMapRow,
  type CallerMapUnitCandidate,
} from '../api'
import { ContractIcon, OpenIcon } from '../icons'
import { href } from '../router'
import { FONTS, toneFor, usePhebsTokens } from '../theme'
import { isAbortError } from '../util'
import ExactCallerCitation from './ExactCallerCitation'
import { AnalysisScopePanel } from '../components/AnalysisScopePanel'

const pageSize = 100
const maxCursorHistory = 500

type Freshness = 'any' | 'fresh' | 'stale'
type Resolution = 'any' | 'scip' | 'syntax' | 'unresolved'
type Ordering = 'source' | 'unit'

interface CallerFilters {
  unit: string
  owner: string
  pathPrefix: string
  codeRole: string
  tier: string
  freshness: Freshness
  resolution: Resolution
  ordering: Ordering
}

const emptyFilters: CallerFilters = {
  unit: '',
  owner: '',
  pathPrefix: '',
  codeRole: '',
  tier: '',
  freshness: 'any',
  resolution: 'any',
  ordering: 'source',
}

export default function CallerMapPage({
  params,
  comparisonAvailable = false,
}: {
  params: URLSearchParams
  comparisonAvailable?: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const endpoint = endpointFromParams(params)
  const endpointKey = endpoint
    ? [
        endpoint.protocol,
        endpoint.repository,
        endpoint.declaration_lineage,
        endpoint.operation,
      ].join('\u0000')
    : ''
  const [draftFilters, setDraftFilters] = useState<CallerFilters>(emptyFilters)
  const [appliedFilters, setAppliedFilters] = useState<CallerFilters>(emptyFilters)
  const [page, setPage] = useState<CallerMapResponse | null>(null)
  const [pageIndex, setPageIndex] = useState(0)
  const [cursors, setCursors] = useState<string[]>([''])
  const [paginationEndpointKey, setPaginationEndpointKey] = useState(endpointKey)
  const [reload, setReload] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [view, setView] = useState<'source' | 'group'>('source')
  const request = useRef<AbortController | null>(null)
  // On an endpoint prop change, derive the first-page cursor immediately.
  // The reset effect below then commits that state without issuing a
  // transient request for the new endpoint with the old endpoint's cursor.
  const cursor = paginationEndpointKey === endpointKey
    ? cursors[pageIndex] ?? ''
    : ''

  useEffect(() => {
    setCursors([''])
    setPageIndex(0)
    setPage(null)
    setPaginationEndpointKey(endpointKey)
  }, [endpointKey])

  useEffect(() => {
    if (!endpoint) return
    request.current?.abort()
    const controller = new AbortController()
    request.current = controller
    setLoading(true)
    setError('')
    setPage(null)
    fetchContractCallers(
      endpoint,
      {
        unit: appliedFilters.unit || undefined,
        owner: appliedFilters.owner || undefined,
        path_prefix: appliedFilters.pathPrefix || undefined,
        code_role: appliedFilters.codeRole || undefined,
        tier: appliedFilters.tier || undefined,
        freshness: appliedFilters.freshness,
        resolution: appliedFilters.resolution,
        ordering: appliedFilters.ordering,
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
  }, [endpointKey, appliedFilters, cursor, reload]) // eslint-disable-line react-hooks/exhaustive-deps

  const resetPagination = () => {
    setCursors([''])
    setPageIndex(0)
    // A first-page 409 leaves cursor === '' unchanged, so no effect dep
    // moves; the reload counter guarantees restart always refetches.
    setReload((generation) => generation + 1)
  }
  const applyFilters = (event: React.FormEvent) => {
    event.preventDefault()
    setAppliedFilters({
      ...draftFilters,
      unit: draftFilters.unit.trim(),
      owner: draftFilters.owner.trim(),
      pathPrefix: draftFilters.pathPrefix.trim(),
    })
    resetPagination()
  }
  const clearFilters = () => {
    setDraftFilters(emptyFilters)
    setAppliedFilters(emptyFilters)
    resetPagination()
  }
  const reviewUnresolved = () => {
    const next = { ...appliedFilters, resolution: 'unresolved' as const }
    setDraftFilters(next)
    setAppliedFilters(next)
    resetPagination()
  }
  const nextPage = () => {
    const next = page?.pagination.next_cursor
    if (!next || cursors.length >= maxCursorHistory) return
    setCursors((current) => [...current.slice(0, pageIndex + 1), next])
    setPageIndex((current) => current + 1)
  }
  const staleCursor = error.startsWith('409:')

  if (!endpoint) {
    return (
      <div className={css({ maxWidth: '920px', margin: '0 auto' })}>
        <Notification
          kind={NOTIFICATION_KIND.negative}
          overrides={{ Body: { style: { width: 'auto', margin: 0 } } }}
        >
          Caller Map requires a protocol, declaration repository, declaration
          lineage, and canonical operation selected from Contracts.
        </Notification>
        <a
          href="#/contracts"
          className={css({
            display: 'inline-flex',
            marginTop: '14px',
            color: tok.accent,
            fontSize: '13px',
          })}
        >
          Open Contract Atlas
        </a>
      </div>
    )
  }

  return (
    <div
      data-testid="caller-map-page"
      data-responsive-layout="desktop-table-mobile-cards"
      className={css({ maxWidth: '1320px', margin: '0 auto' })}
    >
      <EndpointHeader
        endpoint={endpoint}
        page={page}
        comparisonAvailable={comparisonAvailable}
      />

      <CallerFilterPanel
        draft={draftFilters}
        loading={loading}
        onChange={setDraftFilters}
        onApply={applyFilters}
        onClear={clearFilters}
      />

      {error && (
        <Notification
          kind={NOTIFICATION_KIND.negative}
          overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}
        >
          <div>
            <strong>{staleCursor ? 'Caller snapshot changed.' : 'Caller Map request failed.'}</strong>
            <div className={css({ marginTop: '4px', overflowWrap: 'anywhere' })}>
              {error}
            </div>
            <div className={css({ display: 'flex', gap: '8px', marginTop: '10px', flexWrap: 'wrap' })}>
              <Button
                type="button"
                size={BUTTON_SIZE.mini}
                kind={BUTTON_KIND.secondary}
                onClick={() => setReload((current) => current + 1)}
              >
                Retry this page
              </Button>
              {staleCursor && (
                <Button
                  type="button"
                  size={BUTTON_SIZE.mini}
                  kind={BUTTON_KIND.secondary}
                  onClick={resetPagination}
                >
                  Restart from first page
                </Button>
              )}
            </div>
          </div>
        </Notification>
      )}

      {loading && (
        <div
          role="status"
          aria-label="Loading Caller Map"
          className={css({
            minHeight: '220px',
            display: 'grid',
            placeItems: 'center',
            border: `1px solid ${tok.cardBorder}`,
            backgroundColor: tok.bandBg,
          })}
        >
          <Spinner $size="small" />
        </div>
      )}

      {!loading && page && (
        <>
          <GenerationStatus page={page} />
          <AnalysisScopePanel
            id="caller-map-analysis-scope"
            certificate={page.coverage}
            scopes={page.scope ? [page.scope] : []}
            callerGeneration={page.generation}
            eyebrow="Focused declarations and repository-overlay callers"
            compact
          />
          <PageProgress
            page={page}
            pageIndex={pageIndex}
            view={view}
            onView={setView}
            onReviewUnresolved={reviewUnresolved}
          />
          <CallerRows page={page} view={view} />
          <Pager
            page={page}
            pageIndex={pageIndex}
            canRememberNext={cursors.length < maxCursorHistory}
            onPrevious={() => setPageIndex((current) => Math.max(0, current - 1))}
            onNext={nextPage}
          />
          <div className={css({
            marginTop: '14px',
            padding: '11px 13px',
            border: `1px solid ${tok.cardBorder}`,
            color: tok.textTertiary,
            fontSize: '11px',
            lineHeight: '17px',
          })}>
            {page.caveat}
          </div>
        </>
      )}
    </div>
  )
}

function endpointFromParams(params: URLSearchParams): CallerMapEndpoint | null {
  const endpoint = {
    protocol: params.get('protocol')?.trim() ?? '',
    repository: params.get('repository')?.trim() ?? '',
    declaration_lineage: params.get('lineage')?.trim() ?? '',
    operation: params.get('operation')?.trim() ?? '',
  }
  return Object.values(endpoint).every(Boolean) ? endpoint : null
}

function EndpointHeader({
  endpoint,
  page,
  comparisonAvailable,
}: {
  endpoint: CallerMapEndpoint
  page: CallerMapResponse | null
  comparisonAvailable: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const declarationSources = page?.declaration?.sources ?? []
  return (
    <header className={css({
      display: 'flex',
      alignItems: 'flex-start',
      justifyContent: 'space-between',
      gap: '18px',
      marginBottom: '16px',
      '@media screen and (max-width: 720px)': { flexDirection: 'column' },
    })}>
      <div className={css({ minWidth: 0 })}>
        <div className={css({ display: 'flex', alignItems: 'center', gap: '9px' })}>
          <ContractIcon size={21} />
          <span className={css({
            color: tok.textTertiary,
            fontSize: '11px',
            fontWeight: 650,
            letterSpacing: '0.05em',
            textTransform: 'uppercase',
          })}>
            Caller Map
          </span>
        </div>
        <h1 className={css({
          margin: '7px 0 0',
          color: tok.textPrimary,
          fontFamily: FONTS.MONO,
          fontSize: '20px',
          lineHeight: '29px',
          fontWeight: 650,
          overflowWrap: 'anywhere',
        })}>
          {endpoint.operation}
        </h1>
        <div className={css({
          display: 'flex',
          gap: '6px',
          flexWrap: 'wrap',
          marginTop: '7px',
          color: tok.textTertiary,
          fontSize: '11px',
          lineHeight: '17px',
        })}>
          <Chip tone="blue">{endpoint.protocol}</Chip>
          <span>{endpoint.repository}</span>
          <span>· lineage {shortID(endpoint.declaration_lineage)}</span>
        </div>
      </div>
      <div className={css({ display: 'flex', gap: '8px', flexWrap: 'wrap' })}>
        {comparisonAvailable && (
          <a className={css(linkButtonStyle(tok))} href={href('/compare-callers', {
            old_protocol: endpoint.protocol,
            old_repository: endpoint.repository,
            old_lineage: endpoint.declaration_lineage,
            old_operation: endpoint.operation,
          })}>
            Compare replacement
          </a>
        )}
        <a className={css(linkButtonStyle(tok))} href={href('/contracts', {
          repository: endpoint.repository,
          protocol: endpoint.protocol,
          lineage: endpoint.declaration_lineage,
        })}>
          Back to Contracts
        </a>
        {declarationSources.map((source, index) => (
          <a
            key={`${source.path}:${source.start_line}:${index}`}
            className={css(linkButtonStyle(tok))}
            href={sourceHref(source)}
          >
            Declaration{declarationSources.length > 1 ? ` ${index + 1}` : ''} <OpenIcon size={12} />
          </a>
        ))}
        {page?.declaration?.sources_truncated && (
          <span className={css({ fontSize: '12px', color: tok.textTertiary })}>
            declaration citations truncated
          </span>
        )}
      </div>
    </header>
  )
}

function CallerFilterPanel({
  draft,
  loading,
  onChange,
  onApply,
  onClear,
}: {
  draft: CallerFilters
  loading: boolean
  onChange: (filters: CallerFilters) => void
  onApply: (event: React.FormEvent) => void
  onClear: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <form
      aria-label="Caller Map filters"
      onSubmit={onApply}
      className={css({
        display: 'grid',
        gridTemplateColumns: 'repeat(4, minmax(130px, 1fr))',
        gap: '9px',
        padding: '12px',
        marginBottom: '14px',
        border: `1px solid ${tok.cardBorder}`,
        backgroundColor: tok.bandBg,
        '@media screen and (max-width: 900px)': { gridTemplateColumns: '1fr 1fr' },
        '@media screen and (max-width: 560px)': { gridTemplateColumns: '1fr' },
      })}
    >
      <FilterInput
        label="Unit"
        value={draft.unit}
        placeholder="unit-checkout"
        onChange={(unit) => onChange({ ...draft, unit })}
      />
      <FilterInput
        label="Owner"
        value={draft.owner}
        placeholder="team-payments"
        onChange={(owner) => onChange({ ...draft, owner })}
      />
      <FilterInput
        label="Path prefix"
        value={draft.pathPrefix}
        placeholder="src/checkout/"
        onChange={(pathPrefix) => onChange({ ...draft, pathPrefix })}
      />
      <FilterSelect
        label="Code role"
        value={draft.codeRole}
        options={[
          ['', 'Any role'],
          ['production', 'Production'],
          ['test', 'Test'],
          ['mock', 'Mock'],
          ['generated', 'Generated'],
          ['vendor', 'Vendor'],
        ]}
        onChange={(codeRole) => onChange({ ...draft, codeRole })}
      />
      <FilterSelect
        label="Tier"
        value={draft.tier}
        options={[
          ['', 'Any tier'],
          ['exact', 'Exact'],
          ['derived', 'Derived'],
          ['heuristic', 'Heuristic'],
          ['unresolved', 'Unresolved'],
        ]}
        onChange={(tier) => onChange({ ...draft, tier })}
      />
      <FilterSelect
        label="Freshness"
        value={draft.freshness}
        options={[
          ['any', 'Any freshness'],
          ['fresh', 'Fresh'],
          ['stale', 'Stale'],
        ]}
        onChange={(freshness) => onChange({
          ...draft,
          freshness: freshness as Freshness,
        })}
      />
      <FilterSelect
        label="Resolution"
        value={draft.resolution}
        options={[
          ['any', 'Any resolution'],
          ['scip', 'SCIP'],
          ['syntax', 'Syntax'],
          ['unresolved', 'Unresolved'],
        ]}
        onChange={(resolution) => onChange({
          ...draft,
          resolution: resolution as Resolution,
        })}
      />
      <FilterSelect
        label="Server ordering"
        value={draft.ordering}
        options={[
          ['source', 'Source'],
          ['unit', 'Unit'],
        ]}
        onChange={(ordering) => onChange({
          ...draft,
          ordering: ordering as Ordering,
        })}
      />
      <div className={css({
        display: 'flex',
        gap: '7px',
        gridColumn: '1 / -1',
        justifyContent: 'flex-end',
        flexWrap: 'wrap',
      })}>
        <Button type="submit" size={BUTTON_SIZE.compact} isLoading={loading}>
          Apply filters
        </Button>
        <Button
          type="button"
          size={BUTTON_SIZE.compact}
          kind={BUTTON_KIND.tertiary}
          onClick={onClear}
        >
          Clear
        </Button>
      </div>
    </form>
  )
}

function FilterInput({
  label,
  value,
  placeholder,
  onChange,
}: {
  label: string
  value: string
  placeholder: string
  onChange: (value: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css(filterLabelStyle(tok.textSecondary))}>
      {label}
      <Input
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        placeholder={placeholder}
        aria-label={label}
        size="compact"
        overrides={{
          Root: { style: { borderRadius: 0 } },
          Input: { style: { fontSize: '12px' } },
        }}
      />
    </label>
  )
}

function FilterSelect({
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
    <label className={css(filterLabelStyle(tok.textSecondary))}>
      {label}
      <select
        aria-label={label}
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        className={css({
          height: '38px',
          boxSizing: 'border-box',
          padding: '0 30px 0 10px',
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: 0,
          backgroundColor: tok.pageBg,
          color: tok.textPrimary,
          fontSize: '12px',
          ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
        })}
      >
        {options.map(([optionValue, labelValue]) => (
          <option key={optionValue} value={optionValue}>{labelValue}</option>
        ))}
      </select>
    </label>
  )
}

function GenerationStatus({ page }: { page: CallerMapResponse }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const generation = page.generation
  if (!generation && !page.matching_rows_state) return null
  const exact = page.matching_rows_state === 'exact'
  return (
    <section
      data-testid="caller-map-generation"
      data-matching-rows-state={page.matching_rows_state ?? ''}
      className={css({
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'space-between',
        gap: '12px',
        flexWrap: 'wrap',
        padding: '10px 12px',
        marginBottom: '10px',
        border: `1px solid ${exact ? tok.status.current.solid : tok.status.stale.solid}`,
        backgroundColor: tok.bandBg,
      })}
    >
      <div>
        <div className={css({
          display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap',
          color: tok.textPrimary, fontSize: '11px', fontWeight: 650,
        })}>
          <Chip tone={exact ? 'green' : 'amber'}>
            {exact ? 'exact rows' : 'rows unavailable'}
          </Chip>
          <span>
            {exact
              ? 'Exact repository-overlay generation'
              : 'Complete caller generation unavailable'}
          </span>
        </div>
        {generation?.reason && (
          <div className={css({
            marginTop: '4px', color: tok.textTertiary, fontSize: '10px', lineHeight: '15px',
          })}>
            {generation.reason}. This is not evidence of zero callers.
          </div>
        )}
      </div>
      {generation && (
        <div className={css({
          color: tok.textTertiary,
          fontFamily: FONTS.MONO,
          fontSize: '9px',
          lineHeight: '15px',
          textAlign: 'right',
          overflowWrap: 'anywhere',
          '@media screen and (max-width: 560px)': { textAlign: 'left' },
        })}>
          <div>{generation.state} · revision {generation.publication_revision ?? 'unavailable'}</div>
          {generation.commit && <div>commit {shortID(generation.commit)}</div>}
          {generation.generation_digest && (
            <div>generation {shortID(generation.generation_digest)}</div>
          )}
        </div>
      )}
    </section>
  )
}

function PageProgress({
  page,
  pageIndex,
  view,
  onView,
  onReviewUnresolved,
}: {
  page: CallerMapResponse
  pageIndex: number
  view: 'source' | 'group'
  onView: (view: 'source' | 'group') => void
  onReviewUnresolved: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const start = page.rows.length === 0 ? 0 : pageIndex * pageSize + 1
  const end = pageIndex * pageSize + page.rows.length
  const unresolved = page.rows.filter(isUnresolved).length
  const unavailable = page.matching_rows_state === 'unavailable'
  const total = page.total_matching_rows
  return (
    <section className={css({
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      gap: '12px',
      padding: '11px 12px',
      border: `1px solid ${tok.cardBorder}`,
      borderBottom: 'none',
      backgroundColor: tok.bandBg,
      '@media screen and (max-width: 680px)': {
        alignItems: 'flex-start',
        flexDirection: 'column',
      },
    })}>
      <div>
        <div className={css({ color: tok.textPrimary, fontSize: '12px', fontWeight: 650 })}>
          {unavailable
            ? 'Caller totals unavailable'
            : total === 0
            ? 'No matching static evidence'
            : total === undefined
            ? 'Caller total unavailable'
            : `Rows ${start}–${end} of ${total}`}
        </div>
        <div className={css({ marginTop: '3px', color: tok.textTertiary, fontSize: '10px', lineHeight: '15px' })}>
          {unavailable
            ? 'No partial page or zero-caller conclusion is shown.'
            : page.pagination.complete
            ? `Exact snapshot exhausted on page ${pageIndex + 1}.`
            : `Traversed through at least ${end} rows; continue to exhaust this exact snapshot.`}
          {!unavailable && <> Current page: {unresolved} unresolved.</>}
        </div>
      </div>
      <div className={css({ display: 'flex', gap: '7px', flexWrap: 'wrap' })}>
        {unresolved > 0 && (
          <Button
            type="button"
            size={BUTTON_SIZE.mini}
            kind={BUTTON_KIND.secondary}
            onClick={onReviewUnresolved}
          >
            Review unresolved
          </Button>
        )}
        <button
          type="button"
          aria-pressed={view === 'source'}
          onClick={() => onView('source')}
          className={css(viewButtonStyle(tok, view === 'source'))}
        >
          Source view
        </button>
        <button
          type="button"
          aria-pressed={view === 'group'}
          onClick={() => onView('group')}
          className={css(viewButtonStyle(tok, view === 'group'))}
        >
          Unit view
        </button>
      </div>
    </section>
  )
}

function CallerRows({
  page,
  view,
}: {
  page: CallerMapResponse
  view: 'source' | 'group'
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [expandedCandidates, setExpandedCandidates] = useState('')
  if (page.rows.length === 0) {
    const unavailable = page.matching_rows_state === 'unavailable'
    return (
      <div className={css({
        padding: '28px',
        border: `1px solid ${tok.cardBorder}`,
        backgroundColor: tok.pageBg,
        color: tok.textTertiary,
        fontSize: '12px',
        lineHeight: '19px',
      })}>
        {unavailable
          ? 'Caller rows are unavailable because no exact complete repository-overlay generation is current. This is not zero callers and no partial rows are shown.'
          : 'No caller rows matched these filters within the exact generation. This does not establish runtime use, completeness, migration completion, or decommissioning safety.'}
      </div>
    )
  }
  if (view === 'group') {
    const groups = groupRows(page.rows)
    return (
      <div
        aria-label="Caller rows grouped by attributed unit"
        className={css({ border: `1px solid ${tok.cardBorder}` })}
      >
        {groups.map(([group, rows]) => (
          <details key={group} open className={css({
            borderBottom: `1px solid ${tok.cardBorder}`,
            ':last-child': { borderBottom: 'none' },
          })}>
            <summary className={css({
              display: 'flex',
              justifyContent: 'space-between',
              gap: '10px',
              padding: '10px 12px',
              cursor: 'pointer',
              backgroundColor: tok.bandBg,
              color: tok.textSecondary,
              fontSize: '11px',
              ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
            })}>
              <span className={css({ fontFamily: FONTS.MONO, overflowWrap: 'anywhere' })}>
                {group}
              </span>
              <span>{rows.length} on this page</span>
            </summary>
            {rows.map((row) => (
              <CallerRow
                key={rowIdentity(row)}
                row={row}
                expandedCandidates={expandedCandidates === rowIdentity(row)}
                onExpandCandidates={(expanded) =>
                  setExpandedCandidates(expanded ? rowIdentity(row) : '')}
              />
            ))}
          </details>
        ))}
      </div>
    )
  }
  const unresolved = page.rows.filter(isUnresolved)
  const resolved = page.rows.filter((row) => !isUnresolved(row))
  return (
    <div className={css({ border: `1px solid ${tok.cardBorder}` })}>
      {unresolved.length > 0 && (
        <RowSection
          title="Needs review"
          rows={unresolved}
          expandedCandidates={expandedCandidates}
          onExpandCandidates={setExpandedCandidates}
        />
      )}
      {resolved.length > 0 && (
        <RowSection
          title="Resolved caller evidence"
          rows={resolved}
          expandedCandidates={expandedCandidates}
          onExpandCandidates={setExpandedCandidates}
        />
      )}
    </div>
  )
}

function RowSection({
  title,
  rows,
  expandedCandidates,
  onExpandCandidates,
}: {
  title: string
  rows: CallerMapRow[]
  expandedCandidates: string
  onExpandCandidates: (identity: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section aria-label={title}>
      <h2 className={css({
        margin: 0,
        padding: '8px 12px',
        borderBottom: `1px solid ${tok.innerSep}`,
        backgroundColor: tok.bandBg,
        color: tok.textSecondary,
        fontSize: '10px',
        lineHeight: '15px',
        fontWeight: 650,
        letterSpacing: '0.04em',
        textTransform: 'uppercase',
      })}>
        {title} · {rows.length} on this page
      </h2>
      {rows.map((row) => (
        <CallerRow
          key={rowIdentity(row)}
          row={row}
          expandedCandidates={expandedCandidates === rowIdentity(row)}
          onExpandCandidates={(expanded) =>
            onExpandCandidates(expanded ? rowIdentity(row) : '')}
        />
      ))}
    </section>
  )
}

function CallerRow({
  row,
  expandedCandidates,
  onExpandCandidates,
}: {
  row: CallerMapRow
  expandedCandidates: boolean
  onExpandCandidates: (expanded: boolean) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const candidates = row.unit.candidates ?? []
  return (
    <article
      data-testid="caller-map-row"
      data-occurrence-id={rowIdentity(row)}
      className={css({
        display: 'grid',
        gridTemplateColumns: 'minmax(260px, 1.4fr) minmax(220px, 1fr)',
        gap: '14px',
        padding: '12px',
        borderBottom: `1px solid ${tok.innerSep}`,
        backgroundColor: tok.pageBg,
        ':last-child': { borderBottom: 'none' },
        '@media screen and (max-width: 720px)': {
          gridTemplateColumns: '1fr',
          gap: '9px',
        },
      })}
    >
      <div className={css({ minWidth: 0 })}>
        <ExactCallerCitation source={row.source} />
        <div className={css({
          display: 'flex',
          gap: '5px',
          flexWrap: 'wrap',
          marginTop: '7px',
        })}>
          <Chip tone={isUnresolved(row) ? 'amber' : 'green'}>
            {humanize(row.classification)}
          </Chip>
          <Chip tone="blue">{row.resolution}</Chip>
          <Chip>{row.tier}</Chip>
          {row.code_role && <Chip>{row.code_role}</Chip>}
          <Chip tone={row.fresh ? 'green' : 'amber'}>
            {row.fresh ? 'fresh' : 'stale'}
          </Chip>
        </div>
        {row.unresolved_reason && (
          <div className={css({
            marginTop: '7px',
            color: tok.status.stale.text,
            fontSize: '11px',
            lineHeight: '17px',
          })}>
            Needs review: {humanize(row.unresolved_reason)}
          </div>
        )}
        <details className={css({ marginTop: '7px', color: tok.textTertiary })}>
          <summary className={css({
            cursor: 'pointer',
            fontSize: '10px',
            ':focus-visible': { outline: `2px solid ${tok.accent}` },
          })}>
            {row.source.plane === 'repository-overlay'
              ? 'Repository-overlay identity and byte span'
              : 'Evidence identity and byte span'}
          </summary>
          <div className={css({
            marginTop: '5px',
            fontFamily: FONTS.MONO,
            fontSize: '9px',
            lineHeight: '15px',
            overflowWrap: 'anywhere',
          })}>
            bytes {row.source.start_byte}–{row.source.end_byte} ·{' '}
            {row.source.plane === 'repository-overlay' ? (
              <>
                record {row.source.assertion_id} · generation {row.source.run_id} · blob{' '}
                {row.source.atom_id}
              </>
            ) : (
              <>
                assertion {row.source.assertion_id} · run {row.source.run_id} · atom{' '}
                {row.source.atom_id}
              </>
            )}
          </div>
        </details>
      </div>
      <div className={css({ minWidth: 0 })}>
        <div className={css({
          display: 'flex',
          alignItems: 'center',
          gap: '6px',
          flexWrap: 'wrap',
        })}>
          <span className={css({ color: tok.textSecondary, fontSize: '11px', fontWeight: 650 })}>
            Unit attribution
          </span>
          <Chip tone={unitTone(row.unit.state)}>{humanize(row.unit.state)}</Chip>
        </div>
        <div className={css({
          marginTop: '5px',
          color: tok.textTertiary,
          fontFamily: FONTS.MONO,
          fontSize: '10px',
          lineHeight: '16px',
          overflowWrap: 'anywhere',
        })}>
          {row.unit_group}
        </div>
        {row.unit.reason && (
          <div className={css({ marginTop: '4px', color: tok.textTertiary, fontSize: '10px' })}>
            {humanize(row.unit.reason)}
          </div>
        )}
        {candidates.length === 1 && (
          <div className={css({ marginTop: '7px' })}>
            <div className={css({ color: tok.textSecondary, fontSize: '10px' })}>
              1 candidate
            </div>
            <div className={css({ marginTop: '6px' })}>
              <UnitCandidate candidate={candidates[0]} />
            </div>
          </div>
        )}
        {candidates.length > 1 && (
          <div className={css({ marginTop: '7px' })}>
            <button
              type="button"
              aria-expanded={expandedCandidates}
              onClick={() => onExpandCandidates(!expandedCandidates)}
              className={css({
                padding: 0,
                border: 0,
                backgroundColor: 'transparent',
                color: tok.textSecondary,
                cursor: 'pointer',
                fontSize: '10px',
                ':focus-visible': { outline: `2px solid ${tok.accent}` },
              })}
            >
              {expandedCandidates ? 'Hide' : 'Show'} {candidates.length} candidates
            </button>
            {expandedCandidates && (
              <div className={css({ display: 'grid', gap: '6px', marginTop: '6px' })}>
                {candidates.map((candidate) => (
                  <UnitCandidate key={candidate.id} candidate={candidate} />
                ))}
                {(row.unit.candidate_total ?? candidates.length) > candidates.length && (
                  <span className={css({ fontSize: '12px' })}>
                    Candidate list is bounded; {(row.unit.candidate_total ?? 0) - candidates.length} more not shown.
                  </span>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </article>
  )
}

function UnitCandidate({ candidate }: { candidate: CallerMapUnitCandidate }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const lines = [
    ['service', candidate.logical_services],
    ['owner', candidate.owners],
    ['deployable', candidate.deployables],
    ['target', candidate.build_targets],
  ] as const
  return (
    <div data-testid="caller-map-unit-candidate" className={css({
      padding: '7px 8px',
      borderLeft: `2px solid ${tok.cardBorder}`,
      backgroundColor: tok.bandBg,
      fontSize: '9px',
      lineHeight: '15px',
    })}>
      <div className={css({ color: tok.textPrimary, fontFamily: FONTS.MONO, overflowWrap: 'anywhere' })}>
        {candidate.id}
      </div>
      {lines.map(([label, values]) => values && values.length > 0 && (
        <div key={label} className={css({ color: tok.textTertiary, overflowWrap: 'anywhere' })}>
          {label}: {values.join(', ')}
        </div>
      ))}
    </div>
  )
}

function Pager({
  page,
  pageIndex,
  canRememberNext,
  onPrevious,
  onNext,
}: {
  page: CallerMapResponse
  pageIndex: number
  canRememberNext: boolean
  onPrevious: () => void
  onNext: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <nav
      aria-label="Caller Map pagination"
      className={css({
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: '10px',
        padding: '11px 12px',
        border: `1px solid ${tok.cardBorder}`,
        borderTop: 'none',
        backgroundColor: tok.bandBg,
      })}
    >
      <Button
        type="button"
        size={BUTTON_SIZE.compact}
        kind={BUTTON_KIND.secondary}
        disabled={pageIndex === 0}
        onClick={onPrevious}
      >
        Previous page
      </Button>
      <span className={css({ color: tok.textTertiary, fontSize: '10px' })}>
        Page {pageIndex + 1}
      </span>
      <Button
        type="button"
        size={BUTTON_SIZE.compact}
        kind={BUTTON_KIND.secondary}
        disabled={!page.pagination.next_cursor || !canRememberNext}
        onClick={onNext}
      >
        Next page
      </Button>
    </nav>
  )
}

function groupRows(rows: CallerMapRow[]): [string, CallerMapRow[]][] {
  const groups = new Map<string, CallerMapRow[]>()
  for (const row of rows) {
    groups.set(row.unit_group, [...(groups.get(row.unit_group) ?? []), row])
  }
  return [...groups].sort(([left], [right]) => left.localeCompare(right))
}

function isUnresolved(row: CallerMapRow) {
  return row.classification === 'extractor_abstention' ||
    row.resolution === 'unresolved'
}

function rowIdentity(row: CallerMapRow) {
  return [
    row.source.repository,
    row.source.run_id,
    row.source.assertion_id,
    row.source.atom_id,
  ].join(':')
}

function sourceHref(source: {
  repository: string
  commit: string
  path: string
  start_line: number
}) {
  return href('/file', {
    repo: source.repository,
    path: source.path,
    ref: source.commit,
    L: String(source.start_line),
  })
}

function unitTone(state: string): 'green' | 'amber' | 'neutral' {
  if (state === 'resolved') return 'green'
  if (state === 'ambiguous') return 'amber'
  return 'neutral'
}

function Chip({
  children,
  tone = 'neutral',
}: {
  children: React.ReactNode
  tone?: 'green' | 'blue' | 'amber' | 'red' | 'neutral'
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const colors = {
    green: toneFor('green', tok),
    blue: toneFor('blue', tok),
    amber: toneFor('amber', tok),
    red: toneFor('red', tok),
    neutral: toneFor('neutral', tok),
  }
  return (
    <span className={css({
      display: 'inline-flex',
      alignItems: 'center',
      minHeight: '18px',
      padding: '0 6px',
      border: `1px solid ${colors[tone].solid}`,
      color: colors[tone].text,
      backgroundColor: tok.pageBg,
      fontSize: '9px',
      lineHeight: '14px',
      fontWeight: 650,
      whiteSpace: 'nowrap',
    })}>
      {children}
    </span>
  )
}

function filterLabelStyle(color: string) {
  return {
    display: 'grid',
    gap: '5px',
    color,
    fontSize: '10px',
    lineHeight: '15px',
    fontWeight: 650,
  } as const
}

function viewButtonStyle(
  tok: ReturnType<typeof usePhebsTokens>,
  active: boolean,
) {
  return {
    minHeight: '30px',
    padding: '0 9px',
    border: `1px solid ${active ? tok.accent : tok.cardBorder}`,
    backgroundColor: active ? tok.selectedLineBg : tok.pageBg,
    color: active ? tok.selectedText : tok.textSecondary,
    fontSize: '10px',
    fontWeight: 650,
    cursor: 'pointer',
    ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
  } as const
}

function linkButtonStyle(tok: ReturnType<typeof usePhebsTokens>) {
  return {
    minHeight: '34px',
    boxSizing: 'border-box',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    gap: '5px',
    padding: '0 10px',
    border: `1px solid ${tok.cardBorder}`,
    color: tok.textSecondary,
    backgroundColor: tok.pageBg,
    fontSize: '11px',
    fontWeight: 600,
    textDecoration: 'none',
    whiteSpace: 'nowrap',
    ':hover': { borderColor: tok.accent, color: tok.accent },
    ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' },
  } as const
}

function humanize(value: string) {
  return value.replaceAll('_', ' ')
}

function shortID(value: string) {
  return value.length > 20 ? `${value.slice(0, 10)}…${value.slice(-7)}` : value
}
