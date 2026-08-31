import { useEffect, useMemo, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import {
  fetchServiceDetail,
  fetchServiceInventory,
  type ServiceDetail,
  type ServiceDisposition,
  type ServiceInventory,
  type ServiceMembershipRole,
  type ServiceRecord,
  type ServiceRelationshipView,
  type ServiceStatus,
} from '../api'
import ServiceOverview from '../components/ServiceOverview'
import { SectionHelp } from '../components/SectionHelp'
import { StateNotice, StatusWord } from '../components/kit'
import { VirtualList, type VirtualRowProps } from '../components/VirtualList'
import { href, navigate, replaceRoute } from '../router'
import { FONTS, TYPE, focusRing, useDensity, usePhebsTokens, type DensityName, type PhebsTokens, type ToneName } from '../theme'
import { boundedError, isAbortError, relTime } from '../util'

const PAGE_SIZE = 50
const SERVICE_CATALOG_HELP_CAPABILITIES: ReadonlySet<string> = new Set(['service-catalog-v2'])

// T43.11: fixed per-density row heights — the windowed list's geometry is
// declared, never measured, so it holds at ten-thousand-row catalogs. Both
// densities carry identical information; dense merges the identity and
// attribute lines.
const LIST_ROW_HEIGHTS: Record<DensityName, number> = { comfortable: 80, dense: 48 }
const LIST_HEIGHT = 620

type ListItem =
  | { kind: 'service'; service: ServiceRecord }
  | { kind: 'header'; label: string; count: number }

const STATE_ORDER: ServiceStatus[] = ['current', 'stale', 'conflict', 'unavailable', 'removed']

// Instant, client-side, and window-bounded: narrowing reads only the rows
// already delivered on this page. Absence claims stay with the server
// filters; the match line names the bound explicitly.
function narrowServices(services: ServiceRecord[], narrow: string): ServiceRecord[] {
  const needle = narrow.trim().toLowerCase()
  if (!needle) return services
  return services.filter((service) =>
    service.key.toLowerCase().includes(needle) ||
    service.display_name.toLowerCase().includes(needle) ||
    (service.reason ?? '').toLowerCase().includes(needle))
}

function groupItems(services: ServiceRecord[], group: 'none' | 'state'): ListItem[] {
  if (group === 'none') return services.map((service) => ({ kind: 'service', service }))
  const items: ListItem[] = []
  for (const state of STATE_ORDER) {
    const members = services.filter((service) => service.status === state)
    if (members.length === 0) continue
    items.push({ kind: 'header', label: titleCase(state), count: members.length })
    for (const service of members) items.push({ kind: 'service', service })
  }
  return items
}

interface DirectoryRoute {
  repository: string
  status?: ServiceStatus
  disposition?: ServiceDisposition
  includeRemoved: boolean
  cursor: string
  serviceKey: string
  relationshipView: ServiceRelationshipView
  relationshipCursor: string
  // Client-side view state over the loaded window (T43.11) — URL-borne per
  // the charter's deep-link discipline, never sent to the server.
  narrow: string
  group: 'none' | 'state'
}

export default function ServiceDirectoryPage({ params, relationshipsAvailable = false }: {
  params: URLSearchParams
  relationshipsAvailable?: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const route = directoryRoute(params)
  const [inventory, setInventory] = useState<ServiceInventory | null>(null)
  const [detail, setDetail] = useState<ServiceDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [inventoryError, setInventoryError] = useState('')
  const [detailError, setDetailError] = useState('')
  const [reload, setReload] = useState(0)
  const requestGeneration = useRef(0)

  useEffect(() => {
    if (!route.repository) {
      setInventory(null)
      setDetail(null)
      setLoading(false)
      setInventoryError('')
      setDetailError('')
      return
    }
    const generation = ++requestGeneration.current
    const controller = new AbortController()
    setLoading(true)
    setInventoryError('')
    setDetailError('')
    setInventory(null)
    setDetail(null)
    Promise.allSettled([
      fetchServiceInventory({
        repository: route.repository,
        status: route.status,
        disposition: route.disposition,
        include_removed: route.includeRemoved || undefined,
        page_size: PAGE_SIZE,
        cursor: route.cursor || undefined,
      }, controller.signal),
      route.serviceKey
        ? fetchServiceDetail(
            route.repository, route.serviceKey, controller.signal,
          )
        : Promise.resolve(null),
    ])
      .then(([inventoryResult, detailResult]) => {
        if (generation !== requestGeneration.current) return
        if (inventoryResult.status === 'rejected') {
          if (!isAbortError(inventoryResult.reason)) {
            setInventoryError(boundedError(inventoryResult.reason))
          }
          return
        }
        setInventory(inventoryResult.value)
        if (detailResult.status === 'rejected') {
          if (!isAbortError(detailResult.reason)) {
            setDetailError(boundedError(detailResult.reason))
          }
          return
        }
        setDetail(detailResult.value)
      })
      .finally(() => {
        if (generation === requestGeneration.current) setLoading(false)
      })
    return () => controller.abort()
  }, [
    route.repository,
    route.status,
    route.disposition,
    route.includeRemoved,
    route.cursor,
    route.serviceKey,
    reload,
  ])

  const setRoute = (changes: Partial<DirectoryRoute>) => {
    const next = { ...route, ...changes }
    navigate('/services', directoryParams(next))
  }

  if (!route.repository) {
    return (
      <section className={css({ maxWidth: '760px', margin: '48px auto 0' })}>
        <div className={css({ fontSize: '11px', lineHeight: '16px', letterSpacing: '0.1em', textTransform: 'uppercase', color: tok.textTertiary, marginBottom: '10px' })}>
          Repository context required
        </div>
        <div className={css({ display: 'flex', alignItems: 'center', gap: '8px' })}>
          <h1 className={css({ fontSize: '30px', lineHeight: '36px', letterSpacing: '-0.025em', margin: 0, color: tok.textPrimary })}>
            Service directory
          </h1>
          <SectionHelp
            termId="service_catalog_authority"
            enabledCapabilities={SERVICE_CATALOG_HELP_CAPABILITIES}
            triggerLabel="Service directory"
          />
        </div>
        <p className={css({ maxWidth: '620px', margin: '14px 0 24px', fontSize: '14px', lineHeight: '22px', color: tok.textSecondary })}>
          Choose a visible repository first. Service identities are local to one
          catalog and are never combined across repositories.
        </p>
        <a href="#/repos" className={css(primaryLink(tok))}>Choose a repository</a>
      </section>
    )
  }

  return (
    <section aria-labelledby="service-directory-title">
      <DirectoryHeader route={route} />

      {inventoryError && (
        <div className={css({ marginTop: '16px' })}>
          <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto' } } }}>
            <div className={css({ display: 'flex', alignItems: 'center', gap: '12px', flexWrap: 'wrap' })}>
              <span>{inventoryError}</span>
              <button type="button" onClick={() => setReload((value) => value + 1)} className={css(textButton(tok))}>
                Retry
              </button>
            </div>
          </Notification>
        </div>
      )}

      {loading && (
        <div role="status" aria-live="polite" className={css({ minHeight: '280px', display: 'grid', placeItems: 'center', color: tok.textSecondary })}>
          <div className={css({ display: 'flex', alignItems: 'center', gap: '10px', fontSize: '13px' })}>
            <Spinner $size="small" /> Reading exact catalog state…
          </div>
        </div>
      )}

      {!loading && !inventoryError && inventory && (
        <>
          <RepositorySummary inventory={inventory} />
          <div className={css({ display: 'grid', gridTemplateColumns: 'minmax(320px, 0.9fr) minmax(0, 1.45fr)', gap: '16px', marginTop: '16px', alignItems: 'start', '@media screen and (max-width: 900px)': { gridTemplateColumns: 'minmax(0, 1fr)' } })}>
            <div className={css({ minWidth: 0, '@media screen and (max-width: 900px)': { order: route.serviceKey ? 2 : 1 } })}>
              <ServiceList
                inventory={inventory}
                route={route}
                onRoute={setRoute}
              />
            </div>
            <div className={css({ minWidth: 0, '@media screen and (max-width: 900px)': { order: route.serviceKey ? 1 : 2 } })}>
              <ServiceDetailPanel
                detail={detail}
                detailError={detailError}
                selectedKey={route.serviceKey}
                route={route}
                relationshipsAvailable={relationshipsAvailable}
                onRetry={() => setReload((value) => value + 1)}
              />
            </div>
          </div>
          <SourceFreeBoundary />
        </>
      )}
    </section>
  )
}

function DirectoryHeader({ route }: { route: DirectoryRoute }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <header className={css({ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', gap: '20px', flexWrap: 'wrap' })}>
      <div>
        <div className={css({ display: 'flex', alignItems: 'center', gap: '7px', fontSize: '12px', lineHeight: '16px', color: tok.textTertiary, marginBottom: '7px' })}>
          <a href="#/repos" className={css({ color: tok.textSecondary, textDecoration: 'none', ':hover': { color: tok.textPrimary, textDecoration: 'underline' }, ':focus-visible': focusRing(tok) })}>Repositories</a>
          <span aria-hidden="true">/</span>
          <span className={css({ fontFamily: FONTS.MONO, overflowWrap: 'anywhere' })}>{route.repository}</span>
        </div>
        <div className={css({ display: 'flex', alignItems: 'center', gap: '8px' })}>
          <h1 id="service-directory-title" className={css({ margin: 0, fontSize: '24px', lineHeight: '30px', letterSpacing: '-0.02em', color: tok.textPrimary })}>
            Service directory
          </h1>
          <SectionHelp
            termId="service_catalog_authority"
            enabledCapabilities={SERVICE_CATALOG_HELP_CAPABILITIES}
            triggerLabel="Service directory"
          />
        </div>
      </div>
      <span className={css({ fontSize: '12px', lineHeight: '18px', color: tok.textTertiary, maxWidth: '420px', textAlign: 'right', '@media screen and (max-width: 720px)': { textAlign: 'left' } })}>
        Catalog authority plus exact static relationships · source content only on citation
      </span>
    </header>
  )
}

function RepositorySummary({ inventory }: { inventory: ServiceInventory }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const repository = inventory.repository
  const sharedOnPage = inventory.services.reduce(
    (total, service) => total + service.role_counts.shared, 0,
  )
  const attention = repository.stale_count + repository.unavailable_count +
    repository.conflict_count
  const metrics = [
    ['Catalog services', repository.catalog_service_count],
    ['Current', repository.current_count],
    ['Needs attention', attention],
    ['Unowned files', repository.unowned_file_count],
    ['Shared roles · page', sharedOnPage],
  ] as const
  return (
    <div className={css({ marginTop: '16px', border: `1px solid ${tok.cardBorder}`, borderRadius: '10px', overflow: 'hidden', backgroundColor: tok.pageBg })}>
      <div className={css({ display: 'grid', gridTemplateColumns: 'minmax(220px, 1.4fr) repeat(5, minmax(92px, 0.55fr))', '@media screen and (max-width: 980px)': { gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' }, '@media screen and (max-width: 560px)': { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' } })}>
        <div className={css({ padding: '14px 16px', borderRight: `1px solid ${tok.innerSep}`, '@media screen and (max-width: 980px)': { gridColumn: '1 / -1', borderRight: 'none', borderBottom: `1px solid ${tok.innerSep}` } })}>
          <div className={css({ fontSize: '11px', lineHeight: '14px', textTransform: 'uppercase', letterSpacing: '0.08em', color: tok.textTertiary })}>Authority</div>
          <div className={css({ marginTop: '4px', fontSize: '13px', lineHeight: '18px', color: tok.textPrimary, fontWeight: 600 })}>
            {repository.authority.id}
          </div>
          <div className={css({ fontSize: '11.5px', lineHeight: '17px', color: tok.textTertiary, fontFamily: FONTS.MONO, overflowWrap: 'anywhere' })}>
            {repository.authority.kind} · {repository.authority.version}
            {repository.authority.override_id && ` · override ${repository.authority.override_id}@${repository.authority.override_version}`}
          </div>
        </div>
        {metrics.map(([label, value]) => (
          <div key={label} className={css({ padding: '14px 12px', borderRight: `1px solid ${tok.innerSep}`, ':last-child': { borderRight: 'none' }, '@media screen and (max-width: 980px)': { borderTop: `1px solid ${tok.innerSep}` } })}>
            <div className={css({ fontSize: '20px', lineHeight: '24px', fontWeight: 600, fontVariantNumeric: 'tabular-nums', color: tok.textPrimary })}>{value}</div>
            <div className={css({ marginTop: '2px', fontSize: '11px', lineHeight: '14px', color: tok.textTertiary })}>{label}</div>
          </div>
        ))}
      </div>
      <div className={css({ padding: '8px 16px', borderTop: `1px solid ${tok.innerSep}`, backgroundColor: tok.bandBg, display: 'flex', gap: '14px', flexWrap: 'wrap', fontSize: '11px', lineHeight: '16px', color: tok.textTertiary, fontFamily: FONTS.MONO })}>
        <span>{repository.accepted_file_count}/{repository.source_file_count} accepted files</span>
        <span>catalog r{repository.catalog_control_revision}</span>
        <span>state r{repository.state_control_revision}</span>
        <span title={new Date(repository.state_updated_at).toLocaleString()}>updated {relTime(repository.state_updated_at)}</span>
      </div>
    </div>
  )
}

function ServiceList({ inventory, route, onRoute }: {
  inventory: ServiceInventory
  route: DirectoryRoute
  onRoute: (changes: Partial<DirectoryRoute>) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { density } = useDensity()
  const rowHeight = LIST_ROW_HEIGHTS[density]
  const filtered = Boolean(route.status || route.disposition || route.includeRemoved)
  const nextCursor = inventory.pagination.next_cursor ?? ''
  const narrowed = useMemo(
    () => narrowServices(inventory.services, route.narrow),
    [inventory.services, route.narrow],
  )
  const items = useMemo(() => groupItems(narrowed, route.group), [narrowed, route.group])
  const [activeIndex, setActiveIndex] = useState(0)
  useEffect(() => setActiveIndex(0), [items.length, route.cursor])
  // Committed selection (the URL's service_key), reported via
  // aria-selected — distinct from the keyboard focus position.
  const selectedIndex = useMemo(
    () => items.findIndex((item) => item.kind === 'service' && item.service.key === route.serviceKey),
    [items, route.serviceKey],
  )
  return (
    <div className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '10px', overflow: 'hidden', minWidth: 0 })}>
      <div className={css({ padding: '11px 12px', borderBottom: `1px solid ${tok.cardBorder}`, backgroundColor: tok.bandBg })}>
        <div className={css({ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: '10px', marginBottom: '9px' })}>
          <h2 className={css({ margin: 0, fontSize: '13px', lineHeight: '18px', color: tok.textPrimary })}>Services</h2>
          <span className={css({ fontSize: '11px', color: tok.textTertiary })}>{inventory.pagination.returned} in this page</span>
        </div>
        <div className={css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)', gap: '8px', '@media screen and (max-width: 480px)': { gridTemplateColumns: '1fr' } })}>
          <FilterSelect
            label="Lifecycle"
            value={route.status ?? ''}
            onChange={(value) => onRoute({ status: value as ServiceStatus || undefined, cursor: '', serviceKey: '' })}
            options={['current', 'stale', 'unavailable', 'conflict', 'removed']}
          />
          <FilterSelect
            label="Disposition"
            value={route.disposition ?? ''}
            onChange={(value) => onRoute({ disposition: value as ServiceDisposition || undefined, cursor: '', serviceKey: '' })}
            options={['accepted', 'proposal', 'conflict', 'rejected']}
          />
        </div>
        <label className={css({ display: 'inline-flex', alignItems: 'center', gap: '7px', marginTop: '9px', fontSize: '11.5px', lineHeight: '16px', color: tok.textSecondary, cursor: 'pointer' })}>
          <input
            type="checkbox"
            checked={route.includeRemoved}
            onChange={(event) => onRoute({ includeRemoved: event.currentTarget.checked, status: route.status === 'removed' && !event.currentTarget.checked ? undefined : route.status, cursor: '', serviceKey: '' })}
          />
          Include removed identities
        </label>
        <div className={css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1.4fr) minmax(0, 1fr)', gap: '8px', marginTop: '9px', '@media screen and (max-width: 480px)': { gridTemplateColumns: '1fr' } })}>
          <label className={css({ display: 'grid', gap: '3px', fontSize: '11px', lineHeight: '14px', color: tok.textTertiary })}>
            Narrow loaded rows
            <input
              type="search"
              value={route.narrow}
              placeholder="Key, name, or reason"
              onChange={(event) => replaceRoute('/services', directoryParams({ ...route, narrow: event.currentTarget.value }))}
              className={css({ width: '100%', height: '30px', boxSizing: 'border-box', padding: '0 8px', border: `1px solid ${tok.cardBorder}`, borderRadius: '6px', backgroundColor: tok.pageBg, color: tok.textPrimary, fontSize: '12px', ':focus-visible': focusRing(tok) })}
            />
          </label>
          <FilterSelect
            label="Group"
            value={route.group === 'state' ? 'state' : ''}
            onChange={(value) => onRoute({ group: value === 'state' ? 'state' : 'none' })}
            options={['state']}
            emptyLabel="None"
          />
        </div>
        {route.narrow.trim() !== '' && (
          <div role="status" className={css({ marginTop: '7px', fontSize: '11px', lineHeight: '16px', color: tok.textSecondary })}>
            {narrowed.length} of {inventory.pagination.returned} loaded rows match · narrowing reads this page only
          </div>
        )}
      </div>

      {inventory.services.length === 0 ? (
        <div className={css({ padding: '32px 18px', color: tok.textSecondary })}>
          <div className={css({ fontSize: '13px', lineHeight: '18px', fontWeight: 600, color: tok.textPrimary })}>
            {nextCursor ? 'No matches in this scan window' : filtered ? 'No services match these filters' : 'This catalog has no service records'}
          </div>
          <p className={css({ margin: '7px 0 0', fontSize: '12px', lineHeight: '18px', color: tok.textTertiary })}>
            {nextCursor
              ? 'Continue to scan the next bounded window; this empty page is not an absence claim.'
              : filtered
                ? 'Clear the filters to return to the complete visible inventory.'
                : 'The exact catalog publication is present, but its service collection is empty.'}
          </p>
          {filtered && !nextCursor && (
            <button type="button" onClick={() => onRoute({ status: undefined, disposition: undefined, includeRemoved: false, cursor: '', serviceKey: '' })} className={css({ ...textButton(tok), marginTop: '12px' })}>
              Clear filters
            </button>
          )}
        </div>
      ) : narrowed.length === 0 ? (
        <div className={css({ padding: '32px 18px', color: tok.textSecondary })}>
          <div className={css({ fontSize: '13px', lineHeight: '18px', fontWeight: 600, color: tok.textPrimary })}>
            No loaded rows match this narrowing
          </div>
          <p className={css({ margin: '7px 0 0', fontSize: '12px', lineHeight: '18px', color: tok.textTertiary })}>
            Narrowing reads only this page&apos;s {inventory.pagination.returned} loaded rows — it makes no claim about the rest of the catalog. Use the lifecycle and disposition filters or paging for the full inventory.
          </p>
          <button type="button" onClick={() => onRoute({ narrow: '' })} className={css({ ...textButton(tok), marginTop: '12px' })}>
            Clear narrowing
          </button>
        </div>
      ) : (
        <VirtualList<ListItem>
          items={items}
          rowHeight={rowHeight}
          height={Math.min(LIST_HEIGHT, items.length * rowHeight)}
          ariaLabel="Services"
          listboxId="service-directory-list"
          activeIndex={activeIndex}
          selectedIndex={selectedIndex}
          onActiveChange={setActiveIndex}
          onCommit={(item) => {
            if (item.kind === 'service') onRoute({ serviceKey: item.service.key, relationshipCursor: '' })
          }}
          getKey={(item) => item.kind === 'header' ? `header:${item.label}` : `${item.service.key}:${item.service.incarnation}`}
          isHeader={(item) => item.kind === 'header'}
          renderRow={(item, _index, rowProps, active) => item.kind === 'header' ? (
            <div {...rowProps} className={css({ display: 'flex', alignItems: 'flex-end', gap: '7px', padding: `0 12px ${density === 'dense' ? '5px' : '8px'}`, backgroundColor: tok.bandBg, borderBottom: `1px solid ${tok.innerSep}` })}>
              <span className={css({ fontSize: '11px', lineHeight: '15px', fontWeight: 600, letterSpacing: '0.07em', textTransform: 'uppercase', color: tok.textTertiary })}>{item.label}</span>
              <span className={css({ fontSize: '11px', lineHeight: '15px', color: tok.textTertiary, fontVariantNumeric: 'tabular-nums' })}>{item.count}</span>
            </div>
          ) : (
            <ServiceListRow service={item.service} route={route} rowProps={rowProps} active={active} density={density} />
          )}
        />
      )}

      {(route.cursor || nextCursor) && (
        <nav aria-label="Service pages" className={css({ minHeight: '44px', padding: '8px 12px', boxSizing: 'border-box', borderTop: `1px solid ${tok.cardBorder}`, backgroundColor: tok.bandBg, display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '10px' })}>
          {route.cursor ? (
            <a href={directoryHref(route, { cursor: '', serviceKey: '' })} className={css(textButtonLink(tok))}>First page</a>
          ) : <span />}
          {nextCursor && (
            <a href={directoryHref(route, { cursor: nextCursor, serviceKey: '' })} className={css(primaryLink(tok))}>Next page</a>
          )}
        </nav>
      )}
    </div>
  )
}

function FilterSelect({ label, value, options, onChange, emptyLabel = 'All' }: {
  label: string
  value: string
  options: string[]
  onChange: (value: string) => void
  emptyLabel?: string
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css({ display: 'grid', gap: '3px', fontSize: '11px', lineHeight: '14px', color: tok.textTertiary })}>
      {label}
      <select value={value} onChange={(event) => onChange(event.currentTarget.value)} className={css({ width: '100%', height: '30px', boxSizing: 'border-box', padding: '0 28px 0 8px', border: `1px solid ${tok.cardBorder}`, borderRadius: '6px', backgroundColor: tok.pageBg, color: tok.textPrimary, fontSize: '12px', ':focus-visible': focusRing(tok) })}>
        <option value="">{emptyLabel}</option>
        {options.map((option) => <option key={option} value={option}>{titleCase(option)}</option>)}
      </select>
    </label>
  )
}

// Fixed-height row (T43.11): the anatomy is constant per density so the
// windowed list's geometry never depends on row content. Both densities show
// the same facts — name, state, key, incarnation, disposition, roles, paths,
// reason (truncated in place; complete in the detail panel on selection).
function ServiceListRow({ service, route, rowProps, active, density }: {
  service: ServiceRecord
  route: DirectoryRoute
  rowProps: VirtualRowProps
  active: boolean
  density: DensityName
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const selected = route.serviceKey === service.key
  const roles = roleEntries(service).filter(([, count]) => count > 0)
  const dense = density === 'dense'
  // One text run with a single visible ellipsis: under width pressure the
  // facts truncate with a mark, never by silently clipping trailing items.
  // The full run is the accessible name and the hover title, and the detail
  // panel carries every fact on selection — density changes spacing, never
  // information.
  const facts = [
    service.disposition,
    ...roles.map(([role, count]) => `${role} ${count}`),
    ...(service.distinct_path_count > 0 ? [`${service.distinct_path_count} paths`] : []),
    ...(service.reason ? [service.reason] : []),
  ].join(' · ')
  const fullName = `${service.display_name} · ${service.key} · incarnation ${service.incarnation} · ${titleCase(service.status)} · ${facts}`
  const factsRun = (
    <span className={css({ minWidth: 0, flex: '1 1 0', fontSize: '11px', lineHeight: '15px', color: tok.textTertiary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>{facts}</span>
  )
  return (
    <a
      {...rowProps}
      href={directoryHref(route, { serviceKey: service.key, relationshipCursor: '' })}
      tabIndex={-1}
      aria-current={selected ? 'true' : undefined}
      // Explicit composed name: browsers are inconsistent computing
      // name-from-content for options whose children are block elements.
      aria-label={fullName}
      title={fullName}
      className={css({
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        gap: dense ? '3px' : '4px',
        padding: `0 ${dense ? '10px' : '12px'}`,
        overflow: 'hidden',
        textDecoration: 'none',
        color: tok.textPrimary,
        backgroundColor: selected ? tok.selectedLineBg : active ? tok.hoverFill : tok.pageBg,
        boxShadow: selected ? `inset 2px 0 0 ${tok.accent}` : active ? `inset 2px 0 0 ${tok.gutter}` : 'none',
        borderBottom: `1px solid ${tok.innerSep}`,
        ':hover': { backgroundColor: selected ? tok.selectedLineBg : tok.hoverFill },
      })}
    >
      <div className={css({ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: '10px', minWidth: 0 })}>
        <span className={css({ minWidth: 0, fontSize: dense ? '12px' : '13px', lineHeight: dense ? '16px' : '17px', fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>{service.display_name}</span>
        <StatusBadge service={service} />
      </div>
      {dense ? (
        <div className={css({ display: 'flex', alignItems: 'baseline', gap: '8px', minWidth: 0, whiteSpace: 'nowrap' })}>
          <code className={css({ flex: '0 1 auto', minWidth: 0, fontSize: '11px', lineHeight: '14px', color: tok.textTertiary, fontFamily: FONTS.MONO, overflow: 'hidden', textOverflow: 'ellipsis' })}>{service.key} · incarnation {service.incarnation}</code>
          {factsRun}
        </div>
      ) : (
        <>
          <div className={css({ fontSize: '11px', lineHeight: '15px', color: tok.textTertiary, fontFamily: FONTS.MONO, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>{service.key} · incarnation {service.incarnation}</div>
          <div className={css({ display: 'flex', minWidth: 0 })}>{factsRun}</div>
        </>
      )}
    </a>
  )
}

function ServiceDetailPanel({ detail, detailError, selectedKey, route, relationshipsAvailable, onRetry }: {
  detail: ServiceDetail | null
  detailError: string
  selectedKey: string
  route: DirectoryRoute
  relationshipsAvailable: boolean
  onRetry: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (!selectedKey) {
    return (
      <div className={css({ minHeight: '300px', border: `1px solid ${tok.cardBorder}`, borderRadius: '10px', display: 'grid', placeItems: 'center', padding: '28px', boxSizing: 'border-box', color: tok.textTertiary, textAlign: 'center' })}>
        <div>
          <div className={css({ fontSize: '13px', lineHeight: '18px', color: tok.textPrimary, fontWeight: 600 })}>Select a service identity</div>
          <div className={css({ marginTop: '6px', fontSize: '12px', lineHeight: '18px', maxWidth: '360px' })}>Exact lifecycle identities and bounded catalog placements will appear here.</div>
        </div>
      </div>
    )
  }
  if (detailError) {
    return (
      <aside role="alert" className={css({ minHeight: '220px', border: `1px solid ${tok.cardBorder}`, borderRadius: '10px', padding: '24px', boxSizing: 'border-box', color: tok.textSecondary })}>
        <div className={css({ fontSize: '11px', lineHeight: '14px', letterSpacing: '0.08em', textTransform: 'uppercase', color: tok.status.conflict.text })}>
          Service detail unavailable
        </div>
        <h2 className={css({ margin: '7px 0 0', fontSize: '17px', lineHeight: '23px', color: tok.textPrimary })}>
          The inventory is still current
        </h2>
        <p className={css({ margin: '9px 0 0', fontSize: '12px', lineHeight: '18px', overflowWrap: 'anywhere' })}>
          {detailError}
        </p>
        <div className={css({ display: 'flex', alignItems: 'center', gap: '8px', marginTop: '16px', flexWrap: 'wrap' })}>
          <button type="button" onClick={onRetry} className={css(textButton(tok))}>Retry detail</button>
          <a href={directoryHref(route, { serviceKey: '' })} className={css(textButtonLink(tok))}>Clear selection</a>
        </div>
      </aside>
    )
  }
  if (!detail) return null
  const service = detail.service
  return (
    <article className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '10px', overflow: 'hidden', minWidth: 0 })}>
      <header className={css({ padding: '16px', borderBottom: `1px solid ${tok.cardBorder}`, backgroundColor: tok.bandBg })}>
        <div className={css({ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: '14px' })}>
          <div className={css({ minWidth: 0 })}>
            <div className={css({ fontSize: '11px', lineHeight: '14px', color: tok.textTertiary, fontFamily: FONTS.MONO, overflowWrap: 'anywhere' })}>{service.key}</div>
            <h2 className={css({ margin: '3px 0 0', fontSize: '20px', lineHeight: '25px', letterSpacing: '-0.015em', color: tok.textPrimary })}>{service.display_name}</h2>
          </div>
          <div className={css({ display: 'flex', alignItems: 'center', gap: '8px' })}>
            <StatusBadge service={service} />
            <a href={directoryHref(route, { serviceKey: '' })} aria-label="Close service detail" className={css({ color: tok.textTertiary, textDecoration: 'none', fontSize: '18px', lineHeight: '22px', ':hover': { color: tok.textPrimary }, ':focus-visible': focusRing(tok) })}>×</a>
          </div>
        </div>
        <div className={css({ display: 'flex', flexWrap: 'wrap', gap: '6px', marginTop: '11px' })}>
          <SmallTag text={service.disposition} />
          <SmallTag text={service.origin} quiet />
          <SmallTag text={`incarnation ${service.incarnation}`} quiet />
          <SmallTag text={`state r${service.control_revision}`} quiet />
        </div>
        {(service.status === 'current' || service.status === 'stale') && (
          <a
            href={href('/search', {
              q: '', scope: 'service', repository: route.repository,
              service_key: service.key,
              ...(service.active_desired_generation ? { scope_generation: service.active_desired_generation } : {}),
            })}
            className={css({ ...primaryLink(tok), display: 'inline-flex', marginTop: '12px' })}
          >
            Search this service
          </a>
        )}
      </header>

      <ServiceStateNotice service={service} successors={detail.successors} />

      <div className={css({ padding: '16px' })}>
        <h3 className={css(sectionHeading(tok))}>Lifecycle identities</h3>
        <div className={css({ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: '9px 16px', '@media screen and (max-width: 520px)': { gridTemplateColumns: '1fr' } })}>
          <Identity label="Desired" value={service.desired_generation} />
          <Identity label="Active desired" value={service.active_desired_generation} />
          <Identity label="Desired source" value={service.desired_source_generation} />
          <Identity label="Active source" value={service.active_source_generation} />
          <Identity label="Desired catalog" value={service.desired_catalog_generation} />
          <Identity label="Active catalog" value={service.active_catalog_generation} />
        </div>

        <h3 className={css({ ...sectionHeading(tok), marginTop: '20px' })}>Source roles & deployable attribution</h3>
        <p className={css({ margin: '5px 0 0', fontSize: '11px', lineHeight: '16px', color: tok.textTertiary })}>
          Exact catalog roles attribute paths to this service identity. Shared, generated, typed, proposal, and rejected authority remain explicit; no runtime owner is inferred.
        </p>
        {detail.memberships.length === 0 ? (
          <div className={css({ padding: '16px 0 2px', fontSize: '12px', lineHeight: '18px', color: tok.textTertiary })}>No membership paths are attached to this authority record.</div>
        ) : (
          <div className={css({ marginTop: '8px', border: `1px solid ${tok.innerSep}`, borderRadius: '7px', overflow: 'hidden' })}>
            {detail.memberships.map((membership, index) => (
              <div key={`${membership.path}:${membership.role}:${membership.origin}`} className={css({ minHeight: '38px', display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) auto', alignItems: 'center', gap: '12px', padding: '7px 10px', boxSizing: 'border-box', borderBottom: index === detail.memberships.length - 1 ? 'none' : `1px solid ${tok.innerSep}`, '@media screen and (max-width: 520px)': { gridTemplateColumns: '1fr', gap: '5px' } })}>
                <code className={css({ minWidth: 0, fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '16px', color: tok.textSecondary, overflowWrap: 'anywhere' })}>{membership.path}</code>
                <div className={css({ display: 'flex', gap: '5px' })}>
                  <SmallTag text={membership.role} />
                  <SmallTag text={membership.origin} quiet />
                </div>
              </div>
            ))}
          </div>
        )}
        <div data-evidence-metadata="service-directory-change-summary" className={css({ marginTop: '12px', fontSize: TYPE.evidenceMetadata.fontSize, lineHeight: '16px', color: tok.textTertiary })}>
          Changed <time dateTime={service.changed_at} title={new Date(service.changed_at).toLocaleString()}>{relTime(service.changed_at)}</time> · {service.membership_count} role records across {service.distinct_path_count} paths
        </div>
      </div>
      <ServiceOverview
        detail={detail}
        relationshipsAvailable={relationshipsAvailable}
        activeView={route.relationshipView}
        cursor={route.relationshipCursor}
        viewHref={(view, cursor = '') => directoryHref(route, {
          relationshipView: view,
          relationshipCursor: cursor,
        })}
      />
    </article>
  )
}

function ServiceStateNotice({ service, successors }: { service: ServiceRecord; successors: string[] }) {
  const [css] = useStyletron()
  let title = ''
  let body = service.reason ?? ''
  let tone: ToneName = 'blue'
  if (service.status === 'stale') {
    title = 'Active generation is stale'
    body = 'The active identity does not equal the current desired identity. No freshness is inferred.'
    tone = 'amber'
  } else if (service.status === 'conflict' || service.disposition === 'conflict') {
    title = 'Authority conflict'
    body = service.reason || 'The catalog retains an explicit unresolved authority conflict.'
    tone = 'red'
  } else if (service.removed || service.status === 'removed') {
    title = 'Removed identity retained'
    body = successors.length
      ? `Authority successor${successors.length === 1 ? '' : 's'}: ${successors.join(', ')}. This is catalog lineage, not a runtime relationship.`
      : 'The tombstone remains inspectable and supplies no active service authority.'
    tone = 'neutral'
  } else if (service.status === 'unavailable') {
    title = 'No exact active generation'
    body = service.disposition === 'accepted'
      ? 'The desired service exists, but no exact active generation has been published.'
      : service.reason || 'This authority disposition does not create an active generation.'
    tone = 'blue'
  }
  if (!title) return null
  return (
    <div className={css({ margin: '14px 16px 0' })}>
      <StateNotice tone={tone} title={title}>{body}</StateNotice>
    </div>
  )
}

function SourceFreeBoundary() {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <aside className={css({ marginTop: '16px', padding: '12px 14px', border: `1px dashed ${tok.cardBorder}`, borderRadius: '8px', display: 'flex', alignItems: 'baseline', gap: '10px', flexWrap: 'wrap', color: tok.textTertiary })}>
      <strong className={css({ fontSize: '11px', lineHeight: '16px', color: tok.textSecondary })}>Source-free boundary</strong>
      <span className={css({ fontSize: '11px', lineHeight: '17px' })}>
        The inventory and catalog detail read authority metadata. The relationship overview reads exact derived rows; source bytes are loaded only after <strong>View citation</strong>. Paths are authority identities, and no runtime edges, completeness, or accuracy claim is made.
      </span>
    </aside>
  )
}

function Identity({ label, value }: { label: string; value?: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ minWidth: 0 })}>
      <div className={css({ fontSize: '11px', lineHeight: '14px', color: tok.textTertiary, textTransform: 'uppercase', letterSpacing: '0.06em' })}>{label}</div>
      <code title={value} className={css({ display: 'block', marginTop: '2px', fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '16px', color: value ? tok.textSecondary : tok.textTertiary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>
        {value ? shortIdentity(value) : 'unavailable'}
      </code>
    </div>
  )
}

function StatusBadge({ service }: { service: ServiceRecord }) {
  return <StatusWord tone={statusTone(service.status)}>{titleCase(service.status)}</StatusWord>
}

function SmallTag({ text, quiet = false }: { text: string; quiet?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <span className={css({ flexShrink: 0, padding: '2px 6px', borderRadius: '4px', backgroundColor: quiet ? tok.pageBg : tok.fill, border: quiet ? `1px solid ${tok.innerSep}` : 'none', color: quiet ? tok.textTertiary : tok.textSecondary, fontSize: '11px', lineHeight: '13px', whiteSpace: 'nowrap' })}>{text}</span>
}

function roleEntries(service: ServiceRecord): [ServiceMembershipRole, number][] {
  return [
    ['primary', service.role_counts.primary],
    ['supporting', service.role_counts.supporting],
    ['shared', service.role_counts.shared],
    ['generated', service.role_counts.generated],
    ['typed', service.role_counts.typed],
  ]
}

function directoryRoute(params: URLSearchParams): DirectoryRoute {
  const status = params.get('status') as ServiceStatus | null
  const disposition = params.get('disposition') as ServiceDisposition | null
  const requestedView = params.get('relationship_view')
  const relationshipView: ServiceRelationshipView =
    requestedView === 'callers' || requestedView === 'topics'
      ? requestedView
      : 'dependencies'
  return {
    repository: params.get('repository') ?? '',
    status: status || undefined,
    disposition: disposition || undefined,
    includeRemoved: params.get('include_removed') === 'true',
    cursor: params.get('cursor') ?? '',
    serviceKey: params.get('service_key') ?? '',
    relationshipView,
    relationshipCursor: params.get('relationship_cursor') ?? '',
    narrow: params.get('narrow') ?? '',
    group: params.get('group') === 'state' ? 'state' : 'none',
  }
}

function directoryParams(route: DirectoryRoute): Record<string, string> {
  const result: Record<string, string> = { repository: route.repository }
  if (route.status) result.status = route.status
  if (route.disposition) result.disposition = route.disposition
  if (route.includeRemoved) result.include_removed = 'true'
  if (route.cursor) result.cursor = route.cursor
  if (route.serviceKey) result.service_key = route.serviceKey
  if (route.relationshipView !== 'dependencies') result.relationship_view = route.relationshipView
  if (route.relationshipCursor) result.relationship_cursor = route.relationshipCursor
  if (route.narrow) result.narrow = route.narrow
  if (route.group !== 'none') result.group = route.group
  return result
}

function directoryHref(
  route: DirectoryRoute,
  changes: Partial<DirectoryRoute>,
): string {
  return href('/services', directoryParams({ ...route, ...changes }))
}

function statusTone(status: ServiceStatus): ToneName {
  switch (status) {
  case 'current': return 'green'
  case 'stale': return 'amber'
  case 'conflict': return 'red'
  case 'removed': return 'neutral'
  default: return 'blue'
  }
}

function shortIdentity(value: string): string {
  if (value.length <= 24) return value
  return `${value.slice(0, 15)}…${value.slice(-7)}`
}

function titleCase(value: string): string {
  return value ? value[0].toUpperCase() + value.slice(1).replaceAll('_', ' ') : value
}

function primaryLink(tok: PhebsTokens) {
  return {
    display: 'inline-flex', alignItems: 'center', minHeight: '30px', boxSizing: 'border-box' as const,
    padding: '0 10px', borderRadius: '6px', backgroundColor: tok.textPrimary,
    color: tok.pageBg, textDecoration: 'none', fontSize: '11.5px', fontWeight: 600,
    ':hover': { opacity: 0.84 }, ':focus-visible': focusRing(tok),
  }
}

function textButton(tok: PhebsTokens) {
  return {
    minHeight: '28px', padding: '0 8px', border: `1px solid ${tok.cardBorder}`,
    borderRadius: '6px', backgroundColor: tok.pageBg, color: tok.textSecondary,
    fontSize: '11.5px', cursor: 'pointer', ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill },
    ':focus-visible': focusRing(tok),
  }
}

function textButtonLink(tok: PhebsTokens) {
  return {
    ...textButton(tok), display: 'inline-flex', alignItems: 'center',
    boxSizing: 'border-box' as const, textDecoration: 'none',
  }
}

function sectionHeading(tok: PhebsTokens) {
  return { margin: '0', fontSize: '11px', lineHeight: '16px', textTransform: 'uppercase' as const, letterSpacing: '0.075em', color: tok.textTertiary, fontWeight: 600 }
}
