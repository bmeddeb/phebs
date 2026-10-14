import { useCallback, useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button } from 'baseui/button'
import { Input } from 'baseui/input'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import {
  composeKafkaTopicInventory,
  fetchKafkaTopicUsage,
  fetchTopicInventoryPage,
  type BundleAssertion,
  type BundleEvidenceEntry,
  type KafkaTopicCensus,
  type ProofBundleEnvelope,
  type TopicInventoryReport,
  type TopicPlaneState,
  type TopicPlaneStateName,
  type TopicRowPlane,
} from '../api'
import { FONTS, usePhebsTokens, type ToneName } from '../theme'
import { href, navigate, replaceRoute } from '../router'
import { isAbortError } from '../util'
import { CENSUS_LEADS_LINE } from '../caveats'
import { ClaimBoundary, EmptyState, ErrorNotice, IdentityText, LoadingBlock, StatusChip } from '../components/kit'
import { AnalysisScopePanel } from '../components/AnalysisScopePanel'

const qualification = 'Topic-centered source evidence: producers → topic → consumers. A topic is a source spelling — no cluster, environment, or runtime identity — and production topics are overwhelmingly configuration-driven, so unresolved sites dominate by design.'

interface TopicDetail {
  libraries?: string[]
  shapes?: string[]
  bindings?: string[]
  group_ids?: string[]
}

function parseDetail(raw?: string): TopicDetail {
  if (!raw) return {}
  try {
    return JSON.parse(raw) as TopicDetail
  } catch {
    return {}
  }
}

function censusTotal(counts: Record<string, number>): number {
  return Object.values(counts).reduce((sum, value) => sum + value, 0)
}

export default function KafkaTopicsPage({ params, inventoryAvailable = false }: {
  params: URLSearchParams
  inventoryAvailable?: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const linkedTopic = params.get('topic') ?? ''
  const [topic, setTopic] = useState(linkedTopic)
  const [envelope, setEnvelope] = useState<ProofBundleEnvelope | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const requestGeneration = useRef(0)
  const activeRequest = useRef<AbortController | null>(null)

  const runQuery = useCallback((value: string) => {
    activeRequest.current?.abort()
    const generation = ++requestGeneration.current
    const controller = new AbortController()
    activeRequest.current = controller
    setLoading(true)
    setError('')
    fetchKafkaTopicUsage(value, controller.signal)
      .then((result) => {
        if (generation === requestGeneration.current) setEnvelope(result)
      })
      .catch((cause) => {
        if (generation === requestGeneration.current && !isAbortError(cause)) {
          // Never leave a previous topic's answer under a failed query's
          // error banner — a stale result under the wrong heading misleads.
          setEnvelope(null)
          setError(String(cause))
        }
      })
      .finally(() => {
        if (generation === requestGeneration.current) {
          activeRequest.current = null
          setLoading(false)
        }
      })
    return controller
  }, [])

  useEffect(() => {
    setTopic(linkedTopic)
    if (!linkedTopic) {
      // Leaving a topic for the inventory must not keep its answer on screen.
      activeRequest.current?.abort()
      requestGeneration.current++
      setEnvelope(null)
      setLoading(false)
      return
    }
    const controller = runQuery(linkedTopic)
    return () => controller.abort()
  }, [linkedTopic, runQuery])

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const value = topic.trim()
    if (!value) {
      setError('Enter one Kafka topic spelling.')
      return
    }
    setError('')
    if (value === linkedTopic) {
      // The hash would not change, so no effect would fire: re-query
      // directly (retry after a transient failure, or refresh after new
      // extraction publishes).
      runQuery(value)
      return
    }
    navigate('/topics', { topic: value })
  }

  const bundle = envelope?.bundle
  const producers = (bundle?.assertions ?? []).filter((row) => row.predicate === 'PRODUCES_TO_TOPIC')
  const consumers = (bundle?.assertions ?? []).filter((row) => row.predicate === 'CONSUMES_FROM_TOPIC')

  return (
    <div className={css({ maxWidth: '1040px', margin: '0 auto' })}>
      <h1 className={css({ margin: 0, fontSize: '22px', lineHeight: '30px', fontWeight: 650, color: tok.textPrimary })}>
        Kafka topics
      </h1>
      <p className={css({ margin: '6px 0 20px', fontSize: '13px', lineHeight: '20px', color: tok.textTertiary })}>
        {qualification}
      </p>

      <form onSubmit={submit} className={css({ border: `1px solid ${tok.cardBorder}`, marginBottom: '16px', padding: '14px', display: 'flex', gap: '10px' })}>
        <div className={css({ flex: 1 })}>
          <Input
            value={topic}
            onChange={(event) => setTopic(event.currentTarget.value)}
            aria-label="Kafka topic"
            placeholder="orders-v1"
            size="compact"
          />
        </div>
        <Button type="submit" size="compact" disabled={loading}>Query</Button>
      </form>

      {error && (
        <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto' } } }}>
          {error}
        </Notification>
      )}
      {loading && <Spinner $size="small" aria-label="Loading topic usage" />}

      {!linkedTopic && inventoryAvailable && (
        <TopicInventory inventory={params.get('inventory') ?? ''} cursor={params.get('cursor') ?? ''} />
      )}
      {linkedTopic && inventoryAvailable && (
        <p className={css({ margin: '0 0 12px', fontSize: '12px' })}>
          <a href={href('/topics')} className={css({ color: tok.accent })}>Back to the topic inventory</a>
        </p>
      )}

      {bundle && !loading && (
        <div data-testid="topic-usage">
          {bundle.unresolved_census && <CensusPanel census={bundle.unresolved_census} />}
          <AnalysisScopePanel
            id="kafka-topic-analysis-scope"
            certificate={bundle.coverage}
            eyebrow="Scope of this topic answer"
            compact
          />
          <EvidenceTable
            title={`Producers of topic:${bundle.query.topic ?? ''}`}
            rows={producers}
            evidence={bundle.evidence}
            empty="No producer with a source-literal spelling of this topic is visible."
          />
          <EvidenceTable
            title={`Consumers of topic:${bundle.query.topic ?? ''}`}
            rows={consumers}
            evidence={bundle.evidence}
            empty="No consumer with a source-literal spelling of this topic is visible."
          />
          <div className={css({ marginTop: '16px' })}>
            <ClaimBoundary summary="Bundle caveat" caveat={bundle.caveat}>
              <p className={css({ margin: 0 })}>Bundle <IdentityText>{envelope?.id}</IdentityText></p>
            </ClaimBoundary>
          </div>
        </div>
      )}
    </div>
  )
}

// CensusPanel is deliberately rendered before any evidence: the unresolved
// counts are the first-class honesty of this page, not a footnote. Per-plane
// publication counts keep a producer-only replacement from making consumer
// zeros look measured (and vice versa). The ordering is sanctioned by the
// charter §1 census exception (adjudicated in T43.1) and named on-surface
// by CENSUS_LEADS_LINE.
function CensusPanel({ census }: { census: KafkaTopicCensus }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (census.producer_published_runs === 0 && census.consumer_published_runs === 0) {
    return (
      <section
        data-testid="unresolved-census"
        className={css({ border: `1px solid ${tok.cardBorder}`, padding: '14px', marginBottom: '16px' })}
      >
        <h2 className={css({ margin: 0, fontSize: '15px', fontWeight: 650, color: tok.textPrimary })}>
          Unresolved sites
        </h2>
        <p className={css({ margin: '3px 0 0', fontSize: '11px', lineHeight: '16px', color: tok.textTertiary })}>
          {CENSUS_LEADS_LINE}
        </p>
        <p className={css({ margin: '6px 0 0', fontSize: '13px', lineHeight: '20px', color: tok.textSecondary })}>
          No Kafka extraction run has published in the visible repository universe — unresolved counts are
          not meaningful yet, and an empty answer here establishes nothing.
        </p>
      </section>
    )
  }
  const unavailable = new Set(census.unavailable_planes ?? [])
  const planeLine = (plane: 'producer' | 'consumer', published: number, total: number, truncatedPlane: boolean) =>
    unavailable.has(plane)
      ? `The ${plane} plane is unavailable on this server (not released); its zeros are not measured.`
      : published === 0
        ? `No ${plane} extraction run has published; ${plane} zeros are not meaningful.`
        : `${truncatedPlane ? 'At least ' : ''}${total} ${plane} source ${total === 1 ? 'site' : 'sites'} could not be resolved across ${published} published ${published === 1 ? 'run' : 'runs'}.`
  const producerTotal = censusTotal(census.producer)
  const consumerTotal = censusTotal(census.consumer)
  const truncated = new Set(census.truncated ?? [])
  const producerTruncated = Array.from(truncated).some((entry) => entry.startsWith('producer:'))
  const consumerTruncated = Array.from(truncated).some((entry) => entry.startsWith('consumer:'))
  const classes = Array.from(new Set([...Object.keys(census.producer), ...Object.keys(census.consumer)])).sort()
  const count = (plane: string, counts: Record<string, number>, shapeClass: string) =>
    `${truncated.has(`${plane}:${shapeClass}`) ? '≥' : ''}${counts[shapeClass] ?? 0}`
  return (
    <section
      data-testid="unresolved-census"
      className={css({ border: `1px solid ${tok.cardBorder}`, padding: '14px', marginBottom: '16px' })}
    >
      <h2 className={css({ margin: 0, fontSize: '15px', fontWeight: 650, color: tok.textPrimary })}>
        Unresolved sites
      </h2>
      <p className={css({ margin: '3px 0 0', fontSize: '11px', lineHeight: '16px', color: tok.textTertiary })}>
        {CENSUS_LEADS_LINE}
      </p>
      <p className={css({ margin: '6px 0 12px', fontSize: '13px', lineHeight: '20px', color: tok.textSecondary })}>
        {planeLine('producer', census.producer_published_runs, producerTotal, producerTruncated)}
        {' '}
        {planeLine('consumer', census.consumer_published_runs, consumerTotal, consumerTruncated)}
        {' '}This view is not complete. Counts are topic-independent: a configuration-driven topic cannot be
        matched to any literal. Whole-file extraction gaps are reported separately in the coverage certificate.
      </p>
      <table className={css({ borderCollapse: 'collapse', fontSize: '12px', fontFamily: FONTS.MONO })}>
        <thead>
          <tr>
            <th className={css({ textAlign: 'left', padding: '4px 12px 4px 0', color: tok.textTertiary })}>shape class</th>
            <th className={css({ textAlign: 'right', padding: '4px 12px', color: tok.textTertiary })}>producer</th>
            <th className={css({ textAlign: 'right', padding: '4px 0 4px 12px', color: tok.textTertiary })}>consumer</th>
          </tr>
        </thead>
        <tbody>
          {classes.map((shapeClass) => (
            <tr key={shapeClass}>
              <td className={css({ padding: '2px 12px 2px 0', color: tok.textSecondary })}>{shapeClass}</td>
              <td className={css({ textAlign: 'right', padding: '2px 12px', color: tok.textSecondary })}>{count('producer', census.producer, shapeClass)}</td>
              <td className={css({ textAlign: 'right', padding: '2px 0 2px 12px', color: tok.textSecondary })}>{count('consumer', census.consumer, shapeClass)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {truncated.size > 0 && (
        <p className={css({ margin: '10px 0 0', fontSize: '12px', color: tok.textTertiary })}>
          ≥ marks lower bounds: at least one repository exceeded the bounded per-plane census query limit.
        </p>
      )}
    </section>
  )
}

function EvidenceTable({ title, rows, evidence, empty }: {
  title: string
  rows: BundleAssertion[]
  evidence: BundleEvidenceEntry[]
  empty: string
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const atoms = new Map<string, BundleEvidenceEntry>()
  for (const entry of evidence) {
    atoms.set(`${entry.repository} ${entry.run_id} ${entry.atom.atom_id}`, entry)
  }
  return (
    <section className={css({ border: `1px solid ${tok.cardBorder}`, padding: '14px', marginBottom: '16px' })}>
      <h2 className={css({ margin: '0 0 10px', fontSize: '15px', fontWeight: 650, color: tok.textPrimary })}>{title}</h2>
      {rows.length === 0 && (
        <p className={css({ margin: 0, fontSize: '13px', color: tok.textTertiary })}>{empty}</p>
      )}
      {rows.map((row) => {
        const detail = parseDetail(row.detail)
        // Every supporting atom and every occurrence is a citation: one
        // assertion may merge several source sites, and omitting any of
        // them would imply a completeness the page explicitly disclaims.
        const citations = row.supporting.flatMap((atomID) => {
          const entry = atoms.get(`${row.repo} ${row.run_id} ${atomID}`)
          return (entry?.occurrences ?? []).map((occurrence) => ({
            key: `${atomID} ${occurrence.occurrence_id}`,
            occurrence,
          }))
        })
        return (
          <div key={row.id} className={css({ padding: '6px 0', borderTop: `1px solid ${tok.cardBorder}`, fontSize: '13px' })}>
            {citations.length === 0 && (
              <span className={css({ fontFamily: FONTS.MONO, color: tok.textSecondary })}>{row.repo}/{row.subject}</span>
            )}
            {citations.map(({ key, occurrence }) => (
              <div key={key}>
                <a
                  className={css({ color: tok.accent, textDecoration: 'none', fontFamily: FONTS.MONO, overflowWrap: 'anywhere' })}
                  href={href('/file', { repo: occurrence.repo, path: occurrence.path, ref: occurrence.commit, L: String(occurrence.start_line) })}
                >
                  {occurrence.repo}/{occurrence.path}:{occurrence.start_line}
                </a>
              </div>
            ))}
            <div className={css({ fontSize: '12px', color: tok.textTertiary, fontFamily: FONTS.MONO })}>
              {[
                (detail.libraries ?? []).join('+'),
                (detail.shapes ?? []).join('+'),
                (detail.bindings ?? []).join('+'),
                detail.group_ids?.length ? `group ${detail.group_ids.join('+')}` : '',
                `tier ${row.tier}`,
              ].filter(Boolean).join(' · ')}
            </div>
          </div>
        )
      })}
    </section>
  )
}

// ---- T57.4: the composed topic inventory --------------------------------

const PLANE_STATES: ReadonlySet<string> = new Set(['served', 'none', 'unpublished', 'unavailable'])
const INVENTORY_SCHEMA = 'kafka-topic-inventory-v1'

// Charter §2 fail-closed rendering: a crossed or inconsistent answer renders
// as a bounded error, never as partial rows.
function inventoryProblem(report: TopicInventoryReport, bundleID: string): string {
  if (report.schema_version !== INVENTORY_SCHEMA) return `unexpected inventory schema ${report.schema_version}`
  if (report.bundle_id !== bundleID) return 'the answer names a different inventory than the one requested'
  const planes = report.planes.map((plane) => plane.plane).sort().join(',')
  if (planes !== 'consumer,producer') return 'the answer does not report exactly the producer and consumer planes'
  if (report.planes.some((plane) => !PLANE_STATES.has(plane.state))) return 'a plane carries an unknown state'
  if (report.pagination.returned !== report.topics.length) return 'the page row count disagrees with its pagination'
  for (const row of report.topics) {
    for (const cell of [row.producer, row.consumer]) {
      if (!PLANE_STATES.has(cell.state) || cell.sites < 0 || cell.files < 0) return `topic ${row.topic} carries an invalid plane cell`
      if (cell.state !== 'served' && (cell.sites > 0 || cell.files > 0)) return `topic ${row.topic} counts sites on an unserved plane`
    }
  }
  return ''
}

const stateTone: Record<TopicPlaneStateName, ToneName> = {
  served: 'green',
  none: 'neutral',
  unpublished: 'amber',
  unavailable: 'blue',
}

const stateMeaning: Record<TopicPlaneStateName, string> = {
  served: 'measured',
  none: 'measured zero',
  unpublished: 'admitted; nothing published yet',
  unavailable: 'not released on this server',
}

function TopicInventory({ inventory, cursor }: { inventory: string; cursor: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [report, setReport] = useState<TopicInventoryReport | null>(null)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    setReport(null)
    setError('')
    const load = inventory
      ? fetchTopicInventoryPage(inventory, cursor, controller.signal).then((page) => {
        const problem = inventoryProblem(page, inventory)
        if (problem) throw new Error(problem)
        setReport(page)
      })
      // Compose once, then address the immutable bundle from the URL so
      // reload and back/forward reproduce the same inventory.
      : composeKafkaTopicInventory(controller.signal).then((envelope) => replaceRoute('/topics', { inventory: envelope.id }))
    load.catch((cause) => {
      if (!isAbortError(cause)) setError(String(cause))
    })
    return () => controller.abort()
  }, [inventory, cursor, attempt])

  const section = css({ border: `1px solid ${tok.cardBorder}`, padding: '14px', marginBottom: '16px' })
  if (error) {
    return (
      <section className={section} data-testid="topic-inventory">
        <ErrorNotice onRetry={() => setAttempt((value) => value + 1)}>Topic inventory unavailable: {error}</ErrorNotice>
      </section>
    )
  }
  if (!report) return <LoadingBlock label={inventory ? 'Loading topic inventory' : 'Composing topic inventory'} />

  const { pagination } = report
  return (
    <div data-testid="topic-inventory">
      <section className={section}>
        <div className={css({ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: '12px', flexWrap: 'wrap' })}>
          <h2 className={css({ margin: 0, fontSize: '15px', fontWeight: 650, color: tok.textPrimary })}>Topic inventory</h2>
          <button
            type="button"
            onClick={() => navigate('/topics')}
            className={css({ border: 0, padding: 0, background: 'transparent', color: tok.accent, fontSize: '12px', cursor: 'pointer', textDecoration: 'underline' })}
          >
            Recompose from current evidence
          </button>
        </div>
        <p className={css({ margin: '4px 0 12px', fontSize: '13px', lineHeight: '20px', color: tok.textSecondary })}>
          Topic spellings resolved in source across the repositories visible to you. Open a topic for its sites.
        </p>
        <div className={css({ display: 'grid', gap: '10px', gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))', marginBottom: '12px' })}>
          {report.planes.map((plane) => <PlaneCard key={plane.plane} plane={plane} />)}
        </div>
        {(report.truncated?.length ?? 0) > 0 && (
          <p role="note" className={css({ margin: '0 0 10px', fontSize: '12px', color: tok.textSecondary })}>
            Counts are lower bounds: the bounded scan was clipped ({report.truncated!.join(', ')}).
          </p>
        )}
        {report.topics.length === 0 ? (
          <EmptyState
            title="No literal topic spelling resolved in scope"
            detail="This is not an absence claim. Production topics are usually configuration-driven; the unresolved census below is the answer."
          />
        ) : (
          <div className={css({ overflowX: 'auto' })}>
            <table className={css({ width: '100%', minWidth: '520px', borderCollapse: 'collapse', fontSize: '13px' })}>
              <caption className={css({ textAlign: 'left', fontSize: '12px', color: tok.textTertiary, paddingBottom: '6px' })}>
                {pagination.returned} of {pagination.total} topics on this page, ordered {pagination.order}
              </caption>
              <thead>
                <tr>
                  {['Topic', 'Producer', 'Consumer'].map((label) => (
                    <th key={label} scope="col" className={css({ textAlign: 'left', padding: '4px 12px 4px 0', fontSize: '12px', color: tok.textTertiary, fontWeight: 600 })}>{label}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {report.topics.map((row) => (
                  <tr key={row.topic} className={css({ borderTop: `1px solid ${tok.cardBorder}`, verticalAlign: 'top' })}>
                    <td className={css({ padding: '6px 12px 6px 0' })}>
                      <a href={href('/topics', { topic: row.topic })} className={css({ color: tok.accent, fontFamily: FONTS.MONO, overflowWrap: 'anywhere' })}>
                        {row.topic}
                      </a>
                    </td>
                    <td className={css({ padding: '6px 12px 6px 0' })}><PlaneCell plane="producer" cell={row.producer} /></td>
                    <td className={css({ padding: '6px 0' })}><PlaneCell plane="consumer" cell={row.consumer} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {pagination.next_cursor && (
          <div className={css({ marginTop: '10px' })}>
            <Button size="compact" kind="secondary" onClick={() => navigate('/topics', { inventory: report.bundle_id, cursor: pagination.next_cursor! })}>
              Next page
            </Button>
          </div>
        )}
      </section>
      {report.unresolved_census && <CensusPanel census={report.unresolved_census} />}
      <AnalysisScopePanel id="kafka-topic-inventory-scope" certificate={report.coverage} eyebrow="Scope of this inventory" compact />
      <div className={css({ marginBottom: '16px' })}>
        <ClaimBoundary summary="Inventory caveat" caveat={report.caveat}>
          <p className={css({ margin: 0 })}>Inventory bundle <IdentityText>{report.bundle_id}</IdentityText></p>
        </ClaimBoundary>
      </div>
    </div>
  )
}

function PlaneCard({ plane }: { plane: TopicPlaneState }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const measured = plane.state === 'served' || plane.state === 'none'
  return (
    <div data-testid={`plane-${plane.plane}`} className={css({ border: `1px solid ${tok.cardBorder}`, padding: '10px 12px' })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap' })}>
        <span className={css({ fontSize: '13px', fontWeight: 650, color: tok.textPrimary })}>{plane.plane === 'producer' ? 'Producer' : 'Consumer'} plane</span>
        <StatusChip tone={stateTone[plane.state]}>{plane.state}</StatusChip>
        <span className={css({ fontSize: '11px', color: tok.textTertiary })}>{plane.released ? 'released' : 'experimental'}</span>
      </div>
      <p className={css({ margin: '6px 0 0', fontSize: '12px', lineHeight: '18px', color: tok.textSecondary })}>
        {measured
          ? `${plane.topic_count} ${plane.topic_count === 1 ? 'topic' : 'topics'}, ${plane.site_count} ${plane.site_count === 1 ? 'site' : 'sites'} across ${plane.published_runs} published ${plane.published_runs === 1 ? 'run' : 'runs'} in ${plane.repositories} ${plane.repositories === 1 ? 'repository' : 'repositories'}.`
          : `${stateMeaning[plane.state]}: this plane's zeros are not measured.`}
      </p>
      {plane.quality && (
        <details className={css({ marginTop: '6px', fontSize: '12px', lineHeight: '18px', color: tok.textSecondary })}>
          <summary className={css({ cursor: 'pointer' })}>Quality card {plane.quality.card}</summary>
          <p className={css({ margin: '4px 0' })}>{plane.quality.claim}</p>
          <p className={css({ margin: '4px 0' })}>Measured: {plane.quality.measured.join('; ')}</p>
          <p className={css({ margin: '4px 0' })}>Not measured: {plane.quality.unmeasured.join('; ')}</p>
          <p className={css({ margin: '4px 0', fontFamily: FONTS.MONO })}>{plane.quality.protocol}</p>
        </details>
      )}
    </div>
  )
}

function PlaneCell({ plane, cell }: { plane: string; cell: TopicRowPlane }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (cell.state !== 'served') {
    return <span title={stateMeaning[cell.state]}><StatusChip tone={stateTone[cell.state]}>{cell.state === 'none' ? 'none · measured zero' : cell.state}</StatusChip></span>
  }
  const citations = cell.citations ?? []
  return (
    <div>
      <span className={css({ fontVariantNumeric: 'tabular-nums', color: tok.textSecondary })}>
        {cell.sites} {cell.sites === 1 ? 'site' : 'sites'} · {cell.files} {cell.files === 1 ? 'file' : 'files'}
      </span>
      {citations.length > 0 && (
        <details className={css({ fontSize: '12px', marginTop: '2px' })}>
          <summary className={css({ cursor: 'pointer', color: tok.textTertiary })}>{plane} citations</summary>
          {citations.map((citation) => (
            <div key={citation.assertion_id}>
              <a
                className={css({ color: tok.accent, fontFamily: FONTS.MONO, overflowWrap: 'anywhere' })}
                href={href('/file', { repo: citation.repository, path: citation.path, ref: citation.commit })}
              >
                {citation.repository}/{citation.path}@{citation.commit.slice(0, 12)}
              </a>
            </div>
          ))}
          {(cell.omitted_citations ?? 0) > 0 && (
            <div className={css({ color: tok.textTertiary })}>{cell.omitted_citations} more not shown</div>
          )}
        </details>
      )}
    </div>
  )
}
