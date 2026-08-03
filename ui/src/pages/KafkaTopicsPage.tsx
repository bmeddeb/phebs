import { useCallback, useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button } from 'baseui/button'
import { Input } from 'baseui/input'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import {
  fetchKafkaTopicUsage,
  type BundleAssertion,
  type BundleEvidenceEntry,
  type KafkaTopicCensus,
  type ProofBundleEnvelope,
} from '../api'
import { FONTS, usePhebsTokens } from '../theme'
import { href, navigate } from '../router'
import { isAbortError } from '../util'
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

export default function KafkaTopicsPage({ params }: { params: URLSearchParams }) {
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
    if (!linkedTopic) return
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
          <details className={css({ marginTop: '16px', fontSize: '12px', color: tok.textTertiary })}>
            <summary>Bundle caveat ({envelope?.id})</summary>
            <p className={css({ maxWidth: '72ch' })}>{bundle.caveat}</p>
          </details>
        </div>
      )}
    </div>
  )
}

// CensusPanel is deliberately rendered before any evidence: the unresolved
// counts are the first-class honesty of this page, not a footnote. Per-plane
// publication counts keep a producer-only replacement from making consumer
// zeros look measured (and vice versa).
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
        <p className={css({ margin: '6px 0 0', fontSize: '13px', lineHeight: '20px', color: tok.textSecondary })}>
          No Kafka extraction run has published in the visible repository universe — unresolved counts are
          not meaningful yet, and an empty answer here establishes nothing.
        </p>
      </section>
    )
  }
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
      <p className={css({ margin: '6px 0 12px', fontSize: '13px', lineHeight: '20px', color: tok.textSecondary })}>
        {census.producer_published_runs === 0
          ? 'No producer extraction run has published; producer zeros are not meaningful.'
          : `${producerTruncated ? 'At least ' : ''}${producerTotal} producer source ${producerTotal === 1 ? 'site' : 'sites'} could not be resolved across ${census.producer_published_runs} published ${census.producer_published_runs === 1 ? 'run' : 'runs'}.`}
        {' '}
        {census.consumer_published_runs === 0
          ? 'No consumer extraction run has published; consumer zeros are not meaningful.'
          : `${consumerTruncated ? 'At least ' : ''}${consumerTotal} consumer source ${consumerTotal === 1 ? 'site' : 'sites'} could not be resolved across ${census.consumer_published_runs} published ${census.consumer_published_runs === 1 ? 'run' : 'runs'}.`}
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
                  className={css({ color: tok.accent, textDecoration: 'none', fontFamily: FONTS.MONO })}
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
