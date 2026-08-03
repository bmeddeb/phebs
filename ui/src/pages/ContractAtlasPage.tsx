import { useEffect, useMemo, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND, SIZE as BUTTON_SIZE } from 'baseui/button'
import { Input } from 'baseui/input'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import {
  fetchContractCatalog,
  fetchContractOperation,
  type ContractCatalogClaim,
  type ContractCatalogFieldDetail,
  type ContractCatalogItem,
  type ContractCatalogList,
  type ContractCatalogMessage,
  type ContractCatalogOperation,
  type ContractCatalogSource,
  type ContractCatalogTypeReference,
  type CoverageCertificate,
  type CoverageRun,
} from '../api'
import {
  ChevronDown,
  ContractIcon,
  OpenIcon,
} from '../icons'
import { href } from '../router'
import { FONTS, usePhebsTokens } from '../theme'
import { isAbortError } from '../util'
import ContractDependencyMap from '../components/ContractDependencyMap'
import { AnalysisScopePanel } from '../components/AnalysisScopePanel'

interface CatalogFilters {
  repository: string
  package: string
  protocol: string
  lineage: string
}

interface ContractGroup {
  id: string
  repository: string
  lineage: string
  serviceFQN: string
  packageName: string
  declaration?: ContractCatalogItem
  operations: ContractCatalogItem[]
}

const emptyFilters: CatalogFilters = {
  repository: '',
  package: '',
  protocol: '',
  lineage: '',
}

const pageQualification = 'Browse provisional source declarations, bounded message shape, and separately classified implementation and caller evidence.'

export default function ContractAtlasPage({
  params,
  callerMapAvailable = false,
  workbenchAvailable = false,
}: {
  params: URLSearchParams
  callerMapAvailable?: boolean
  workbenchAvailable?: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const initialFilters = routeFilters(params)
  const [draftFilters, setDraftFilters] = useState<CatalogFilters>(initialFilters)
  const [appliedFilters, setAppliedFilters] = useState<CatalogFilters>(initialFilters)
  const [catalog, setCatalog] = useState<ContractCatalogList | null>(null)
  const [selectedID, setSelectedID] = useState('')
  const [selectedItem, setSelectedItem] = useState<ContractCatalogItem | null>(null)
  const [operation, setOperation] = useState<ContractCatalogOperation | null>(null)
  const [listLoading, setListLoading] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [listError, setListError] = useState('')
  const [detailError, setDetailError] = useState('')
  const listGeneration = useRef(0)
  const detailGeneration = useRef(0)
  const listRequest = useRef<AbortController | null>(null)
  const detailRequest = useRef<AbortController | null>(null)

  const loadCatalog = (
    filters: CatalogFilters,
    cursor = '',
    append = false,
  ) => {
    listRequest.current?.abort()
    const controller = new AbortController()
    const generation = ++listGeneration.current
    listRequest.current = controller
    setListLoading(true)
    setListError('')
    fetchContractCatalog(
      {
        repository: filters.repository || undefined,
        package: filters.package || undefined,
        protocol: filters.protocol || undefined,
        lineage: filters.lineage || undefined,
      },
      50,
      cursor,
      controller.signal,
    )
      .then((result) => {
        if (generation !== listGeneration.current) return
        setCatalog((current) => append && current
          ? { ...result, items: mergeCatalogItems(current.items, result.items) }
          : result)
      })
      .catch((cause) => {
        if (generation === listGeneration.current && !isAbortError(cause)) {
          setListError(cause instanceof Error ? cause.message : String(cause))
        }
      })
      .finally(() => {
        if (generation === listGeneration.current) {
          listRequest.current = null
          setListLoading(false)
        }
      })
  }

  const loadOperation = (item: ContractCatalogItem) => {
    if (!item.operation) return
    detailRequest.current?.abort()
    const controller = new AbortController()
    const generation = ++detailGeneration.current
    detailRequest.current = controller
    setSelectedID(catalogItemID(item))
    setSelectedItem(item)
    setOperation(null)
    setDetailLoading(true)
    setDetailError('')
    fetchContractOperation(
      item.repository,
      item.declaration_lineage,
      item.operation,
      controller.signal,
    )
      .then((result) => {
        if (generation === detailGeneration.current) setOperation(result)
      })
      .catch((cause) => {
        if (generation === detailGeneration.current && !isAbortError(cause)) {
          setDetailError(cause instanceof Error ? cause.message : String(cause))
        }
      })
      .finally(() => {
        if (generation === detailGeneration.current) {
          detailRequest.current = null
          setDetailLoading(false)
        }
      })
  }

  useEffect(() => {
    loadCatalog(initialFilters)
    return () => {
      listRequest.current?.abort()
      detailRequest.current?.abort()
    }
    // Route parameters seed the first load. In-page filter and selection
    // changes stay local so opening an operation does not restart pagination.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const groups = useMemo(
    () => groupCatalogItems(catalog?.items ?? []),
    [catalog?.items],
  )
  const duplicateNames = useMemo(() => {
    const counts = new Map<string, number>()
    for (const group of groups) {
      counts.set(group.serviceFQN, (counts.get(group.serviceFQN) ?? 0) + 1)
    }
    return counts
  }, [groups])

  const applyFilters = (event: React.FormEvent) => {
    event.preventDefault()
    const next = {
      repository: draftFilters.repository.trim(),
      package: draftFilters.package.trim(),
      protocol: draftFilters.protocol,
      lineage: draftFilters.lineage.trim(),
    }
    setAppliedFilters(next)
    setCatalog(null)
    setSelectedID('')
    setSelectedItem(null)
    setOperation(null)
    setDetailError('')
    loadCatalog(next)
  }

  const clearFilters = () => {
    setDraftFilters(emptyFilters)
    setAppliedFilters(emptyFilters)
    setCatalog(null)
    setSelectedID('')
    setSelectedItem(null)
    setOperation(null)
    setDetailError('')
    loadCatalog(emptyFilters)
  }

  return (
    <div className={css({ maxWidth: '1320px', margin: '0 auto' })}>
      <header className={css({
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'space-between',
        gap: '20px',
        marginBottom: '18px',
        '@media screen and (max-width: 720px)': { flexDirection: 'column', gap: '10px' },
      })}>
        <div>
          <div className={css({ display: 'flex', alignItems: 'center', gap: '10px', color: tok.textPrimary })}>
            <ContractIcon size={21} />
            <h1 className={css({ margin: 0, fontSize: '22px', lineHeight: '30px', fontWeight: 650 })}>
              Contract Atlas
            </h1>
          </div>
          <p className={css({ margin: '6px 0 0', maxWidth: '760px', color: tok.textTertiary, fontSize: '13px', lineHeight: '20px' })}>
            {pageQualification}
          </p>
        </div>
        {catalog && <CoverageDigest certificate={catalog.coverage} />}
      </header>

      <form
        aria-label="Contract catalog filters"
        onSubmit={applyFilters}
        className={css({
          display: 'grid',
          gridTemplateColumns: 'minmax(180px, 1.2fr) minmax(150px, 0.9fr) 138px minmax(190px, 1.4fr) auto',
          gap: '9px',
          alignItems: 'end',
          padding: '12px',
          marginBottom: '14px',
          border: `1px solid ${tok.cardBorder}`,
          backgroundColor: tok.bandBg,
          '@media screen and (max-width: 980px)': {
            gridTemplateColumns: '1fr 1fr',
          },
          '@media screen and (max-width: 560px)': {
            gridTemplateColumns: '1fr',
          },
        })}
      >
        <FilterInput
          label="Repository"
          value={draftFilters.repository}
          placeholder="github.com/org/service"
          onChange={(repository) => setDraftFilters((current) => ({ ...current, repository }))}
        />
        <FilterInput
          label="Package"
          value={draftFilters.package}
          placeholder="acme.billing.v1"
          onChange={(packageName) => setDraftFilters((current) => ({ ...current, package: packageName }))}
        />
        <label className={css(filterLabelStyle(tok.textSecondary))}>
          Protocol
          <select
            aria-label="Protocol"
            value={draftFilters.protocol}
            onChange={(event) => {
              // Read synchronously: currentTarget is nulled once dispatch
              // ends, but functional updaters run later, on the next render.
              const protocol = event.currentTarget.value
              setDraftFilters((current) => ({ ...current, protocol }))
            }}
            className={css({
              height: '38px',
              boxSizing: 'border-box',
              padding: '0 32px 0 10px',
              border: `1px solid ${tok.cardBorder}`,
              borderRadius: 0,
              backgroundColor: tok.pageBg,
              color: tok.textPrimary,
              fontSize: '13px',
              cursor: 'pointer',
              ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
            })}
          >
            <option value="">All protocols</option>
            <option value="protobuf">Protobuf</option>
            <option value="thrift">Thrift</option>
          </select>
        </label>
        <FilterInput
          label="Provisional lineage"
          value={draftFilters.lineage}
          placeholder="provisional_repo_path_v1_…"
          mono
          onChange={(lineage) => setDraftFilters((current) => ({ ...current, lineage }))}
        />
        <div className={css({
          display: 'flex',
          gap: '7px',
          '@media screen and (min-width: 561px) and (max-width: 980px)': { gridColumn: '1 / -1' },
        })}>
          <Button type="submit" size={BUTTON_SIZE.compact} isLoading={listLoading}>
            Apply
          </Button>
          <Button type="button" size={BUTTON_SIZE.compact} kind={BUTTON_KIND.tertiary} onClick={clearFilters}>
            Clear
          </Button>
        </div>
      </form>

      {listError && (
        <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}>
          {listError}
        </Notification>
      )}

      {catalog && <CoverageStrip certificate={catalog.coverage} pagination={catalog.pagination} />}
      {catalog && (
        <AnalysisScopePanel
          id="contract-atlas-analysis-scope"
          certificate={catalog.coverage}
          eyebrow="Scope of declared interfaces"
          compact
        />
      )}

      <div
        data-testid="contract-atlas-workspace"
        data-responsive-layout="desktop-split-mobile-stack"
        className={css({
        display: 'grid',
        gridTemplateColumns: 'minmax(300px, 390px) minmax(0, 1fr)',
        gap: '16px',
        alignItems: 'start',
        marginTop: '14px',
        '@media screen and (max-width: 860px)': { gridTemplateColumns: '1fr' },
        })}
      >
        <section aria-labelledby="catalog-results-heading" className={css({
          minWidth: 0,
          border: `1px solid ${tok.cardBorder}`,
          backgroundColor: tok.pageBg,
        })}>
          <div className={css({
            minHeight: '43px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: '10px',
            padding: '0 12px',
            borderBottom: `1px solid ${tok.cardBorder}`,
            backgroundColor: tok.bandBg,
          })}>
            <h2 id="catalog-results-heading" className={css({
              margin: 0,
              fontSize: '12px',
              lineHeight: '18px',
              fontWeight: 650,
              textTransform: 'uppercase',
              letterSpacing: '0.04em',
              color: tok.textSecondary,
            })}>
              Declared interfaces
            </h2>
            <span className={css({ fontFamily: FONTS.MONO, fontSize: '10px', color: tok.textTertiary })}>
              {catalog?.items.length ?? 0} rows
            </span>
          </div>

          {!catalog && listLoading && (
            <div className={css({ minHeight: '180px', display: 'grid', placeItems: 'center' })}>
              <Spinner $size="small" />
            </div>
          )}
          {catalog && groups.length === 0 && (
            <HonestEmpty>
              No declaration rows were returned for these filters within the exact coverage shown above. This does not establish absence.
            </HonestEmpty>
          )}
          {groups.map((group) => (
            <ContractGroupRow
              key={group.id}
              group={group}
              duplicate={(duplicateNames.get(group.serviceFQN) ?? 0) > 1}
              selectedID={selectedID}
              onSelect={loadOperation}
            />
          ))}

          {catalog?.pagination.next_cursor && (
            <div className={css({ padding: '12px', borderTop: `1px solid ${tok.cardBorder}` })}>
              <Button
                type="button"
                size={BUTTON_SIZE.compact}
                kind={BUTTON_KIND.secondary}
                isLoading={listLoading}
                onClick={() => loadCatalog(appliedFilters, catalog.pagination.next_cursor, true)}
                overrides={{ BaseButton: { style: { width: '100%' } } }}
              >
                Load next bounded page
              </Button>
              <div className={css({ marginTop: '7px', color: tok.textTertiary, fontSize: '10px', lineHeight: '15px' })}>
                Continuation reason: {humanize(catalog.pagination.reason || 'bounded page')}
              </div>
            </div>
          )}
        </section>

        <section aria-label="Selected operation detail" className={css({ minWidth: 0 })}>
          {detailLoading && (
            <div className={css({
              minHeight: '220px',
              display: 'grid',
              placeItems: 'center',
              border: `1px solid ${tok.cardBorder}`,
            })}>
              <Spinner $size="small" />
            </div>
          )}
          {detailError && (
            <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}>
              {detailError}
            </Notification>
          )}
          {!detailLoading && !detailError && !operation && (
            <div className={css({
              minHeight: '220px',
              display: 'grid',
              alignContent: 'center',
              justifyItems: 'start',
              padding: '28px',
              border: `1px solid ${tok.cardBorder}`,
              backgroundColor: tok.bandBg,
            })}>
              <div className={css({ color: tok.textPrimary, fontSize: '15px', fontWeight: 650 })}>
                Select an operation
              </div>
              <p className={css({ maxWidth: '520px', margin: '7px 0 0', color: tok.textTertiary, fontSize: '12px', lineHeight: '19px' })}>
                Open a method to inspect its pinned declaration, bounded request and response shapes, and separately qualified source relationships.
              </p>
            </div>
          )}
          {!detailLoading && operation && selectedItem && (
            <OperationDetail
              operation={operation}
              selected={selectedItem}
              callerMapAvailable={callerMapAvailable}
              workbenchAvailable={workbenchAvailable}
            />
          )}
        </section>
      </div>

      {catalog && (
        <div className={css({
          marginTop: '16px',
          padding: '11px 13px',
          border: `1px solid ${tok.cardBorder}`,
          color: tok.textTertiary,
          fontSize: '11px',
          lineHeight: '17px',
        })}>
          {catalog.caveat}
        </div>
      )}
    </div>
  )
}

function FilterInput({
  label,
  value,
  placeholder,
  mono = false,
  onChange,
}: {
  label: string
  value: string
  placeholder: string
  mono?: boolean
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
          Input: { style: { fontFamily: mono ? FONTS.MONO : FONTS.SANS, fontSize: '12px' } },
        }}
      />
    </label>
  )
}

function ContractGroupRow({
  group,
  duplicate,
  selectedID,
  onSelect,
}: {
  group: ContractGroup
  duplicate: boolean
  selectedID: string
  onSelect: (item: ContractCatalogItem) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const declaration = group.declaration?.declaration ?? group.operations[0]?.declaration
  return (
    <details open className={css({ borderBottom: `1px solid ${tok.cardBorder}` })}>
      <summary className={css({
        listStyle: 'none',
        display: 'grid',
        gridTemplateColumns: '14px minmax(0, 1fr)',
        gap: '7px',
        padding: '11px 12px 10px',
        cursor: 'pointer',
        ':hover': { backgroundColor: tok.hoverFill },
        ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
        '::-webkit-details-marker': { display: 'none' },
      })}>
        <span className={css({ color: tok.gutter, marginTop: '2px' })}>
          <ChevronDown size={13} />
        </span>
        <span className={css({ minWidth: 0 })}>
          <span className={css({
            display: 'flex',
            alignItems: 'center',
            gap: '6px',
            flexWrap: 'wrap',
            color: tok.textPrimary,
            fontFamily: FONTS.MONO,
            fontSize: '12px',
            lineHeight: '18px',
            overflowWrap: 'anywhere',
          })}>
            {group.serviceFQN}
            {duplicate && <StatusChip tone="amber">duplicate name</StatusChip>}
          </span>
          <span className={css({
            display: 'block',
            marginTop: '3px',
            color: tok.textTertiary,
            fontSize: '10px',
            lineHeight: '15px',
            overflowWrap: 'anywhere',
          })}>
            {group.repository} · {shortLineage(group.lineage)}
          </span>
        </span>
      </summary>
      <div role="list" aria-label={`${group.serviceFQN} operations`}>
        {group.operations.length === 0 && (
          <div className={css({ padding: '0 12px 11px 33px', color: tok.textTertiary, fontSize: '11px', lineHeight: '17px' })}>
            Empty service declaration · no RPC rows in this bounded result.
          </div>
        )}
        {group.operations.map((item) => {
          const active = selectedID === catalogItemID(item)
          return (
            <button
              role="listitem"
              type="button"
              key={catalogItemID(item)}
              aria-label={`Open ${item.operation}`}
              onClick={() => onSelect(item)}
              className={css({
                width: '100%',
                minHeight: '40px',
                boxSizing: 'border-box',
                display: 'grid',
                gridTemplateColumns: 'minmax(0, 1fr) auto',
                gap: '8px',
                alignItems: 'center',
                padding: '7px 12px 7px 33px',
                border: 'none',
                borderTop: `1px solid ${tok.innerSep}`,
                backgroundColor: active ? tok.selectedLineBg : tok.pageBg,
                color: active ? tok.selectedText : tok.textSecondary,
                textAlign: 'left',
                cursor: 'pointer',
                ':hover': { backgroundColor: active ? tok.selectedLineBg : tok.hoverFill },
                ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
              })}
            >
              <span className={css({ minWidth: 0 })}>
                <span className={css({ display: 'block', fontFamily: FONTS.MONO, fontSize: '12px', lineHeight: '17px' })}>
                  {item.method}
                </span>
                <span className={css({ display: 'block', color: tok.textTertiary, fontSize: '10px', lineHeight: '14px' })}>
                  {item.declaration.sources[0]?.path ?? 'source unavailable'}
                </span>
              </span>
              <StatusChip tone={tierTone(item.declaration.tier)}>{item.declaration.tier}</StatusChip>
            </button>
          )
        })}
        {declaration && (
          <div className={css({
            padding: '7px 12px 9px 33px',
            borderTop: `1px solid ${tok.innerSep}`,
            color: tok.textTertiary,
            fontSize: '10px',
            lineHeight: '15px',
          })}>
            declaration run {shortID(declaration.run_id)}
          </div>
        )}
      </div>
    </details>
  )
}

function OperationDetail({
  operation,
  selected,
  callerMapAvailable,
  workbenchAvailable,
}: {
  operation: ContractCatalogOperation
  selected: ContractCatalogItem
  callerMapAvailable: boolean
  workbenchAvailable: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const run = operation.coverage.repositories
    .find((repository) => repository.repository === operation.repository)
    ?.runs.find((candidate) => candidate.run_id === operation.declaration.run_id)
  const fresh = run?.fresh === true
  return (
    <article data-testid="contract-operation-detail" className={css({
      border: `1px solid ${tok.cardBorder}`,
      backgroundColor: tok.pageBg,
    })}>
      <header className={css({
        padding: '16px',
        borderBottom: `1px solid ${tok.cardBorder}`,
        backgroundColor: tok.bandBg,
      })}>
        <div className={css({
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          gap: '14px',
          '@media screen and (max-width: 620px)': { flexDirection: 'column' },
        })}>
          <div className={css({ minWidth: 0 })}>
            <div className={css({ display: 'flex', alignItems: 'center', gap: '7px', flexWrap: 'wrap' })}>
              <h2 className={css({
                margin: 0,
                color: tok.textPrimary,
                fontFamily: FONTS.MONO,
                fontSize: '17px',
                lineHeight: '24px',
                fontWeight: 650,
                overflowWrap: 'anywhere',
              })}>
                {operation.operation}
              </h2>
              <StatusChip tone={tierTone(operation.declaration.tier)}>
                {operation.declaration.tier}
              </StatusChip>
              <StatusChip tone={fresh ? 'green' : 'amber'}>
                {fresh ? 'current' : 'stale'}
              </StatusChip>
            </div>
            <div className={css({ marginTop: '6px', color: tok.textTertiary, fontSize: '11px', lineHeight: '17px', overflowWrap: 'anywhere' })}>
              {operation.repository} · lineage {shortLineage(operation.declaration_lineage)}
            </div>
          </div>
          <div className={css({ display: 'flex', gap: '8px', flexWrap: 'wrap' })}>
            {workbenchAvailable && (
              <a
                href={href('/workbench', {
                  source: 'atlas',
                  step: 'why',
                  protocol: selected.protocol,
                  repository: selected.repository,
                  lineage: selected.declaration_lineage,
                  operation: selected.operation ?? operation.operation,
                })}
                className={css(operationLinkStyle(tok))}
              >
                Start Workbench
              </a>
            )}
            {callerMapAvailable && (
              <a
                href={href('/callers', {
                  protocol: selected.protocol,
                  repository: selected.repository,
                  lineage: selected.declaration_lineage,
                  operation: selected.operation ?? operation.operation,
                })}
                className={css(operationLinkStyle(tok))}
              >
                View callers
              </a>
            )}
            <a
              href={href('/impact', { operation: operation.operation })}
              className={css(operationLinkStyle(tok))}
            >
              Analyze impact
            </a>
          </div>
        </div>
        <div className={css({
          display: 'flex',
          gap: '6px',
          flexWrap: 'wrap',
          marginTop: '11px',
        })}>
          {operation.fact_detail.oneway !== undefined ? (
            <StatusChip tone={operation.fact_detail.oneway ? 'blue' : 'neutral'}>
              {operation.fact_detail.oneway ? 'oneway' : 'request / response'}
            </StatusChip>
          ) : (
            <>
              <StatusChip tone={operation.fact_detail.client_streaming ? 'blue' : 'neutral'}>
                {operation.fact_detail.client_streaming ? 'client stream' : 'single request'}
              </StatusChip>
              <StatusChip tone={operation.fact_detail.server_streaming ? 'blue' : 'neutral'}>
                {operation.fact_detail.server_streaming ? 'server stream' : 'single response'}
              </StatusChip>
            </>
          )}
          {operation.shape_truncated && <StatusChip tone="amber">shape truncated</StatusChip>}
          {operation.relationships_truncated && <StatusChip tone="amber">relationships truncated</StatusChip>}
        </div>
      </header>

      <div className={css({ padding: '16px', display: 'grid', gap: '22px' })}>
        <EvidenceBlock title="Operation declaration" claim={operation.declaration} />

        {(operation.shape_truncated || operation.relationships_truncated) && (
          <BoundNotice>
            This detail crossed a server bound:
            {' '}
            {[
              operation.shape_truncated ? 'message shape' : '',
              operation.relationships_truncated
                ? humanize(operation.relationship_limit_reason || 'relationship limit')
                : '',
            ].filter(Boolean).join(', ')}.
            {' '}
            Omitted rows are not evidence of absence.
          </BoundNotice>
        )}

        <section>
          <SectionTitle>Request / response shape</SectionTitle>
          <div className={css({
            display: 'grid',
            gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)',
            gap: '12px',
            '@media screen and (max-width: 680px)': { gridTemplateColumns: '1fr' },
          })}>
            <MessagePanel label="Request" reference={operation.fact_detail.request} message={operation.request} />
            <MessagePanel label="Response" reference={operation.fact_detail.response} message={operation.response} />
          </div>
        </section>

        <ContractDependencyMap operation={operation} />

        <AnalysisScopePanel
          id="contract-operation-analysis-scope"
          certificate={operation.coverage}
          repository={operation.repository}
          eyebrow="Scope of this operation"
          headingLevel={3}
          compact
        />
        <CoverageDetails certificate={operation.coverage} />
        <div className={css({
          padding: '11px 12px',
          border: `1px solid ${tok.cardBorder}`,
          color: tok.textTertiary,
          fontSize: '11px',
          lineHeight: '17px',
        })}>
          {operation.caveat}
        </div>
      </div>
    </article>
  )
}

function MessagePanel({
  label,
  reference,
  message,
}: {
  label: string
  reference: ContractCatalogTypeReference
  message: ContractCatalogMessage
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  // The server decodes the pack's versioned message detail into this typed
  // field; protobuf declarations omit it.
  const messageKind = message.kind ?? ''
  return (
    <div className={css({ minWidth: 0, border: `1px solid ${tok.cardBorder}` })}>
      <div className={css({
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: '8px',
        padding: '9px 11px',
        borderBottom: `1px solid ${tok.cardBorder}`,
        backgroundColor: tok.bandBg,
      })}>
        <span className={css({ color: tok.textSecondary, fontSize: '11px', fontWeight: 650, textTransform: 'uppercase', letterSpacing: '0.04em' })}>
          {label}
        </span>
        <span className={css({ display: 'inline-flex', gap: '5px' })}>
          {(messageKind === 'union' || messageKind === 'exception') && (
            <StatusChip tone="blue">{messageKind}</StatusChip>
          )}
          <StatusChip tone={message.state === 'resolved' ? 'green' : message.state === 'cycle' ? 'blue' : 'amber'}>
            {humanize(message.state)}
          </StatusChip>
        </span>
      </div>
      <div className={css({ padding: '11px' })}>
        <div className={css({ color: tok.textPrimary, fontFamily: FONTS.MONO, fontSize: '12px', lineHeight: '18px', overflowWrap: 'anywhere' })}>
          {reference.raw}
        </div>
        <div className={css({ marginTop: '3px', color: tok.textTertiary, fontSize: '10px', lineHeight: '16px', overflowWrap: 'anywhere' })}>
          {reference.declaration || reference.reason || reference.resolution}
        </div>
        {message.truncated && (
          <div className={css({ marginTop: '8px' })}>
            <StatusChip tone="amber">{(message.truncation_reasons ?? ['bounded']).map(humanize).join(', ')}</StatusChip>
          </div>
        )}
        {message.fields && message.fields.length > 0 ? (
          <MessageTree message={message} depth={0} />
        ) : (
          <div className={css({ marginTop: '11px', color: tok.textTertiary, fontSize: '11px', lineHeight: '17px' })}>
            {message.state === 'resolved'
              ? 'No numbered fields were returned for this declaration.'
              : `Shape not expanded: ${humanize(message.reason || message.state)}.`}
          </div>
        )}
      </div>
    </div>
  )
}

function MessageTree({ message, depth }: { message: ContractCatalogMessage; depth: number }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <ol className={css({
      listStyle: 'none',
      margin: depth === 0 ? '11px 0 0' : '7px 0 0 11px',
      padding: depth === 0 ? 0 : '0 0 0 10px',
      borderLeft: depth === 0 ? 'none' : `1px solid ${tok.cardBorder}`,
    })}>
      {(message.fields ?? []).map((field) => (
        <li key={`${field.declaration.assertion_id}:${field.field_number}`} className={css({
          padding: '7px 0',
          borderTop: `1px solid ${tok.innerSep}`,
        })}>
          <div className={css({
            display: 'grid',
            gridTemplateColumns: 'minmax(0, 1fr) auto',
            gap: '8px',
            alignItems: 'start',
          })}>
            <span className={css({ minWidth: 0 })}>
              <span className={css({ color: tok.textPrimary, fontFamily: FONTS.MONO, fontSize: '11px', lineHeight: '16px' })}>
                {field.detail.name}
              </span>
              <span className={css({ display: 'block', color: tok.textTertiary, fontFamily: FONTS.MONO, fontSize: '10px', lineHeight: '15px', overflowWrap: 'anywhere' })}>
                {fieldTypeLabel(field.detail)} · field {field.field_number}
                {field.detail.oneof ? ` · oneof ${field.detail.oneof}` : ''}
              </span>
            </span>
            <SourceLink source={field.declaration.sources[0]} compact />
          </div>
          {field.nested && (
            field.nested.fields && field.nested.fields.length > 0
              ? <MessageTree message={field.nested} depth={depth + 1} />
              : (
                <div className={css({ margin: '6px 0 0 11px', color: tok.textTertiary, fontSize: '10px', lineHeight: '15px' })}>
                  {humanize(field.nested.state)}
                  {field.nested.reason ? ` · ${humanize(field.nested.reason)}` : ''}
                </div>
              )
          )}
        </li>
      ))}
    </ol>
  )
}

function EvidenceBlock({ title, claim }: { title: string; claim: ContractCatalogClaim }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section>
      <SectionTitle>{title}</SectionTitle>
      <div className={css({ overflowX: 'auto' })}>
        <table className={css(tableStyle('620px'))}>
          <thead>
            <tr>
              <HeaderCell>Source</HeaderCell>
              <HeaderCell>Claim</HeaderCell>
              <HeaderCell>Tier / role</HeaderCell>
              <HeaderCell>Run</HeaderCell>
            </tr>
          </thead>
          <tbody>
            {claim.sources.map((source) => (
              <tr key={sourceKey(source)} className={css({ borderBottom: `1px solid ${tok.innerSep}` })}>
                <Cell><SourceLink source={source} /></Cell>
                <Cell mono>{claim.predicate}<br />{claim.object}</Cell>
                <Cell>{claim.tier}{claim.code_role ? ` · ${claim.code_role}` : ''}</Cell>
                <Cell mono>{shortID(source.run_id)}</Cell>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {claim.sources_truncated && <div className={css({ marginTop: '7px' })}><StatusChip tone="amber">source locators truncated</StatusChip></div>}
    </section>
  )
}

function SourceLink({ source, compact = false }: { source?: ContractCatalogSource; compact?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (!source) return <span className={css({ color: tok.textTertiary })}>source unavailable</span>
  const sourceHref = href('/file', {
    repo: source.repository,
    path: source.path,
    ref: source.commit,
    L: String(source.start_line),
  })
  if (compact) {
    return (
      <a
        href={sourceHref}
        aria-label={`Open ${source.path} line ${source.start_line}`}
        title={`${source.path}:${source.start_line} · ${source.commit}`}
        className={css({
          display: 'inline-flex',
          color: tok.accent,
          ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' },
        })}
      >
        <OpenIcon size={12} />
      </a>
    )
  }
  return (
    <a
      href={sourceHref}
      className={css({
        display: 'inline-flex',
        alignItems: 'center',
        gap: '5px',
        color: tok.accent,
        fontSize: '11px',
        lineHeight: '17px',
        textDecoration: 'none',
        ':hover': { textDecoration: 'underline' },
        ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' },
      })}
    >
      <span>{source.repository}/{source.path}:{source.start_line}{source.end_line !== source.start_line ? `–${source.end_line}` : ''}</span>
      <OpenIcon size={12} />
    </a>
  )
}

function CoverageStrip({
  certificate,
  pagination,
}: {
  certificate: CoverageCertificate
  pagination: ContractCatalogList['pagination']
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const runs = certificate.repositories.flatMap((repository) => repository.runs)
  const published = runs.filter((run) => run.status === 'published')
  const fresh = published.filter((run) => run.fresh).length
  const stale = published.length - fresh
  const unavailable = runs.length - published.length
  const failed = runs.filter((run) => run.latest_attempt?.status === 'aborted' || (run.failures?.length ?? 0) > 0).length
  return (
    <div className={css({
      display: 'flex',
      alignItems: 'center',
      gap: '7px',
      flexWrap: 'wrap',
      padding: '9px 11px',
      border: `1px solid ${tok.cardBorder}`,
      backgroundColor: tok.pageBg,
    })}>
      <span className={css({ marginRight: '2px', color: tok.textSecondary, fontSize: '11px', fontWeight: 600 })}>
        Exact coverage
      </span>
      <StatusChip tone="neutral">{certificate.repository_count} visible repos</StatusChip>
      <StatusChip tone="green">{fresh} current runs</StatusChip>
      {stale > 0 && <StatusChip tone="amber">{stale} stale runs</StatusChip>}
      {unavailable > 0 && <StatusChip tone="neutral">{unavailable} unpublished</StatusChip>}
      {failed > 0 && <StatusChip tone="red">{failed} failed replacement{failed === 1 ? '' : 's'}</StatusChip>}
      <StatusChip tone={pagination.complete ? 'green' : 'amber'}>
        {pagination.complete ? 'page set complete' : humanize(pagination.reason || 'bounded page')}
      </StatusChip>
    </div>
  )
}

function CoverageDigest({ certificate }: { certificate: CoverageCertificate }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({
      maxWidth: '300px',
      color: tok.textTertiary,
      fontFamily: FONTS.MONO,
      fontSize: '10px',
      lineHeight: '15px',
      textAlign: 'right',
      overflowWrap: 'anywhere',
      '@media screen and (max-width: 720px)': { maxWidth: '100%', textAlign: 'left' },
    })}>
      coverage {shortID(certificate.digest)}
    </div>
  )
}

function CoverageDetails({ certificate }: { certificate: CoverageCertificate }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <details className={css({ borderTop: `1px solid ${tok.cardBorder}`, paddingTop: '12px' })}>
      <summary className={css({
        cursor: 'pointer',
        color: tok.textSecondary,
        fontSize: '12px',
        fontWeight: 600,
        ':hover': { color: tok.textPrimary },
        ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' },
      })}>
        Exact coverage certificate · {certificate.repository_count} visible repositories
      </summary>
      <div className={css({ marginTop: '10px', overflowX: 'auto' })}>
        <table className={css(tableStyle('700px'))}>
          <thead>
            <tr>
              <HeaderCell>Repository / domain</HeaderCell>
              <HeaderCell>State</HeaderCell>
              <HeaderCell>Indexed / evidence</HeaderCell>
              <HeaderCell>Assertions</HeaderCell>
              <HeaderCell>Unresolved</HeaderCell>
              <HeaderCell>Boundaries</HeaderCell>
              <HeaderCell>Latest attempt</HeaderCell>
            </tr>
          </thead>
          <tbody>
            {certificate.repositories.flatMap((repository) => repository.runs.map((run) => (
              <tr key={`${repository.repository}:${run.domain}`} className={css({ borderBottom: `1px solid ${tok.innerSep}` })}>
                <Cell>{repository.repository}<br /><span className={css({ color: tok.textTertiary })}>{run.domain}</span></Cell>
                <Cell>
                  <StatusChip tone={run.status === 'published' ? run.fresh ? 'green' : 'amber' : 'neutral'}>
                    {run.status === 'published' ? run.fresh ? 'current' : 'stale' : 'unpublished'}
                  </StatusChip>
                </Cell>
                <Cell mono>{shortID(repository.indexed_commit)} / {shortID(run.commit)}</Cell>
                <Cell mono>{run.assertion_count}</Cell>
                <Cell mono>{run.unresolved_count}</Cell>
                <Cell mono><BoundarySummary run={run} /></Cell>
                <Cell>{run.latest_attempt ? humanize(run.latest_attempt.status) : 'none'}</Cell>
              </tr>
            )))}
          </tbody>
        </table>
      </div>
      <div className={css({ marginTop: '8px', color: tok.textTertiary, fontFamily: FONTS.MONO, fontSize: '10px', overflowWrap: 'anywhere' })}>
        {certificate.digest}
      </div>
    </details>
  )
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <h3 className={css({
      margin: '0 0 9px',
      color: tok.textPrimary,
      fontSize: '13px',
      lineHeight: '20px',
      fontWeight: 650,
    })}>
      {children}
    </h3>
  )
}

function HeaderCell({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <th className={css({
      padding: '0 13px 7px 0',
      borderBottom: `1px solid ${tok.cardBorder}`,
      color: tok.textTertiary,
      fontFamily: FONTS.MONO,
      fontSize: '9px',
      lineHeight: '14px',
      fontWeight: 650,
      letterSpacing: '0.04em',
      textAlign: 'left',
      textTransform: 'uppercase',
      whiteSpace: 'nowrap',
    })}>
      {children}
    </th>
  )
}

function Cell({ children, mono = false }: { children: React.ReactNode; mono?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <td className={css({
      padding: '9px 13px 9px 0',
      color: tok.textSecondary,
      fontFamily: mono ? FONTS.MONO : FONTS.SANS,
      fontSize: mono ? '10px' : '11px',
      lineHeight: '17px',
      verticalAlign: 'top',
      overflowWrap: 'anywhere',
    })}>
      {children}
    </td>
  )
}

function StatusChip({ children, tone }: { children: React.ReactNode; tone: 'green' | 'amber' | 'red' | 'blue' | 'neutral' }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const color = tone === 'green' ? tok.statusGreen
    : tone === 'amber' ? tok.statusAmber
      : tone === 'red' ? tok.statusRed
        : tone === 'blue' ? tok.statusBlue
          : tok.textTertiary
  return (
    <span className={css({
      display: 'inline-flex',
      alignItems: 'center',
      minHeight: '18px',
      boxSizing: 'border-box',
      padding: '1px 6px',
      border: `1px solid ${color}`,
      color,
      fontSize: '9px',
      lineHeight: '13px',
      fontWeight: 650,
      letterSpacing: '0.02em',
      whiteSpace: 'nowrap',
    })}>
      {children}
    </span>
  )
}

function BoundNotice({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div role="status" className={css({
      padding: '9px 11px',
      borderLeft: `3px solid ${tok.statusAmber}`,
      backgroundColor: tok.bandBg,
      color: tok.textSecondary,
      fontSize: '11px',
      lineHeight: '18px',
    })}>
      {children}
    </div>
  )
}

function HonestEmpty({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ padding: '22px 14px', color: tok.textTertiary, fontSize: '11px', lineHeight: '18px' })}>
      {children}
    </div>
  )
}

function groupCatalogItems(items: ContractCatalogItem[]): ContractGroup[] {
  const groups = new Map<string, ContractGroup>()
  for (const item of items) {
    const id = [item.repository, item.declaration_lineage, item.service_fqn].join('\u0000')
    let group = groups.get(id)
    if (!group) {
      group = {
        id,
        repository: item.repository,
        lineage: item.declaration_lineage,
        serviceFQN: item.service_fqn,
        packageName: item.package ?? '',
        operations: [],
      }
      groups.set(id, group)
    }
    if (item.kind === 'service') group.declaration = item
    else group.operations.push(item)
  }
  const result = [...groups.values()]
  for (const group of result) {
    group.operations.sort((left, right) =>
      (left.method ?? '').localeCompare(right.method ?? '') ||
      left.declaration.assertion_id.localeCompare(right.declaration.assertion_id))
  }
  result.sort((left, right) =>
    left.serviceFQN.localeCompare(right.serviceFQN) ||
    left.repository.localeCompare(right.repository) ||
    left.lineage.localeCompare(right.lineage))
  return result
}

function mergeCatalogItems(
  current: ContractCatalogItem[],
  next: ContractCatalogItem[],
): ContractCatalogItem[] {
  const seen = new Set(current.map(catalogItemID))
  return [
    ...current,
    ...next.filter((item) => {
      const id = catalogItemID(item)
      if (seen.has(id)) return false
      seen.add(id)
      return true
    }),
  ]
}

function catalogItemID(item: ContractCatalogItem): string {
  return [
    item.repository,
    item.declaration_lineage,
    item.service_fqn,
    item.method ?? '',
    item.kind,
  ].join('\u0000')
}

function routeFilters(params: URLSearchParams): CatalogFilters {
  return {
    repository: params.get('repository') ?? '',
    package: params.get('package') ?? '',
    protocol: params.get('protocol') ?? '',
    lineage: params.get('lineage') ?? '',
  }
}

function fieldTypeLabel(detail: ContractCatalogFieldDetail): string {
  if (detail.map) return `map<${detail.map.key.raw}, ${detail.map.value.raw}>`
  const type = detail.type?.raw || 'unresolved'
  // 'singular' is protobuf's unmarked cardinality; 'default' is Thrift's
  // unmarked requiredness. Neither adds information next to the type.
  if (detail.cardinality === 'singular' || detail.cardinality === 'default') return type
  return `${detail.cardinality} ${type}`
}

function tierTone(tier: string): 'green' | 'amber' | 'red' | 'blue' | 'neutral' {
  if (tier === 'exact') return 'green'
  if (tier === 'derived') return 'blue'
  if (tier === 'heuristic') return 'amber'
  if (tier === 'unresolved') return 'red'
  return 'neutral'
}

function operationLinkStyle(tok: ReturnType<typeof usePhebsTokens>) {
  return {
    minHeight: '36px',
    boxSizing: 'border-box',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    padding: '0 13px',
    border: `1px solid ${tok.accent}`,
    color: tok.accent,
    backgroundColor: tok.pageBg,
    fontSize: '12px',
    fontWeight: 600,
    textDecoration: 'none',
    whiteSpace: 'nowrap',
    ':hover': { backgroundColor: tok.selectedLineBg },
    ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' },
  } as const
}

function tableStyle(minWidth: string) {
  return {
    width: '100%',
    minWidth,
    borderCollapse: 'collapse' as const,
    tableLayout: 'auto' as const,
  }
}

function filterLabelStyle(color: string) {
  return {
    minWidth: '0',
    display: 'grid',
    gap: '5px',
    color,
    fontSize: '10px',
    lineHeight: '14px',
    fontWeight: 650,
    letterSpacing: '0.03em',
    textTransform: 'uppercase' as const,
  }
}

function sourceKey(source: ContractCatalogSource): string {
  return [
    source.assertion_id,
    source.atom_id,
    source.repository,
    source.path,
    source.start_line,
  ].join(':')
}

function shortID(value?: string): string {
  if (!value) return '—'
  if (value.startsWith('sha256:')) return `${value.slice(0, 15)}…${value.slice(-6)}`
  return value.length > 18 ? `${value.slice(0, 12)}…${value.slice(-4)}` : value
}

const GITLINK_BOUNDARY_POLICY = 'gitlink-boundary-v2'

function BoundarySummary({ run }: { run: CoverageRun }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (run.inventory_policy !== GITLINK_BOUNDARY_POLICY) return <>unknown</>
  const paths = run.gitlink_sample_paths ?? []
  return (
    <>
      <div>{boundaryLabel(run)}</div>
      {paths.length > 0 && (
        <div className={css({ color: tok.textTertiary })}>{paths.join(', ')}</div>
      )}
      {run.gitlink_sample_truncated && (
        <div className={css({ color: tok.statusAmber })}>sample truncated</div>
      )}
    </>
  )
}

// boundaryLabel renders a run's gitlink-boundary state. A run without an
// understood inventory policy has unknown boundary semantics and must never
// read as zero.
function boundaryLabel(run: CoverageRun): string {
  if (run.inventory_policy !== GITLINK_BOUNDARY_POLICY) return 'unknown'
  const count = run.gitlink_count ?? 0
  return `${count} gitlink${count === 1 ? '' : 's'}`
}

function shortLineage(value: string): string {
  if (value.length <= 36) return value
  return `${value.slice(0, 22)}…${value.slice(-10)}`
}

function humanize(value: string): string {
  return value.toLowerCase().replaceAll('_', ' ')
}
