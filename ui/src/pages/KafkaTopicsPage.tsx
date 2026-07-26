import { useEffect, useRef, useState } from 'react'
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

const qualification = 'Topic-centered source evidence: producers → topic → consumers. A topic is a source spelling — no cluster, environment, or runtime identity — and production topics are overwhelmingly configuration-driven, so unresolved sites dominate by design.'

interface TopicDetail {
  library?: string
  shape?: string
  binding?: string
  group_id?: string
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
  const [topic, setTopic] = useState(params.get('topic') ?? '')
  const [envelope, setEnvelope] = useState<ProofBundleEnvelope | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const requestGeneration = useRef(0)
  const activeRequest = useRef<AbortController | null>(null)
  const linkedTopic = params.get('topic') ?? ''

  useEffect(() => {
    if (!linkedTopic) return
    activeRequest.current?.abort()
    const generation = ++requestGeneration.current
    const controller = new AbortController()
    activeRequest.current = controller
    setLoading(true)
    setError('')
    fetchKafkaTopicUsage(linkedTopic, controller.signal)
      .then((result) => {
        if (generation === requestGeneration.current) setEnvelope(result)
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
  }, [linkedTopic])

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const value = topic.trim()
    if (!value) {
      setError('Enter one Kafka topic spelling.')
      return
    }
    setError('')
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
            <summary>Coverage certificate and caveat (bundle {envelope?.id})</summary>
            <p className={css({ maxWidth: '72ch' })}>{bundle.caveat}</p>
            <pre className={css({ overflow: 'auto', fontFamily: FONTS.mono, fontSize: '11px' })}>
              {JSON.stringify(bundle.coverage, null, 2)}
            </pre>
          </details>
        </div>
      )}
    </div>
  )
}

// CensusPanel is deliberately rendered before any evidence: the unresolved
// counts are the first-class honesty of this page, not a footnote.
function CensusPanel({ census }: { census: KafkaTopicCensus }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const producerTotal = censusTotal(census.producer)
  const consumerTotal = censusTotal(census.consumer)
  const classes = Object.keys(census.producer).sort()
  return (
    <section
      data-testid="unresolved-census"
      className={css({ border: `1px solid ${tok.cardBorder}`, padding: '14px', marginBottom: '16px' })}
    >
      <h2 className={css({ margin: 0, fontSize: '15px', fontWeight: 650, color: tok.textPrimary })}>
        Unresolved sites
      </h2>
      <p className={css({ margin: '6px 0 12px', fontSize: '13px', lineHeight: '20px', color: tok.textSecondary })}>
        {producerTotal} producer {producerTotal === 1 ? 'site' : 'sites'} and {consumerTotal} consumer{' '}
        {consumerTotal === 1 ? 'site' : 'sites'} could not be resolved from source — this view is not complete.
        Unresolved counts are topic-independent: a configuration-driven topic cannot be matched to any literal.
      </p>
      <table className={css({ borderCollapse: 'collapse', fontSize: '12px', fontFamily: FONTS.mono })}>
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
              <td className={css({ textAlign: 'right', padding: '2px 12px', color: tok.textSecondary })}>{census.producer[shapeClass]}</td>
              <td className={css({ textAlign: 'right', padding: '2px 0 2px 12px', color: tok.textSecondary })}>{census.consumer[shapeClass]}</td>
            </tr>
          ))}
        </tbody>
      </table>
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
    atoms.set(`${entry.repository} ${entry.run_id} ${entry.atom.atom_id}`, entry)
  }
  return (
    <section className={css({ border: `1px solid ${tok.cardBorder}`, padding: '14px', marginBottom: '16px' })}>
      <h2 className={css({ margin: '0 0 10px', fontSize: '15px', fontWeight: 650, color: tok.textPrimary })}>{title}</h2>
      {rows.length === 0 && (
        <p className={css({ margin: 0, fontSize: '13px', color: tok.textTertiary })}>{empty}</p>
      )}
      {rows.map((row) => {
        const detail = parseDetail(row.detail)
        const entry = row.supporting.length > 0
          ? atoms.get(`${row.repo} ${row.run_id} ${row.supporting[0]}`)
          : undefined
        const occurrence = entry?.occurrences[0]
        return (
          <div key={row.id} className={css({ padding: '6px 0', borderTop: `1px solid ${tok.cardBorder}`, fontSize: '13px' })}>
            {occurrence ? (
              <a
                className={css({ color: tok.link, textDecoration: 'none', fontFamily: FONTS.mono })}
                href={href('/file', { repo: occurrence.repo, path: occurrence.path, ref: occurrence.commit, L: String(occurrence.start_line) })}
              >
                {occurrence.repo}/{occurrence.path}:{occurrence.start_line}
              </a>
            ) : (
              <span className={css({ fontFamily: FONTS.mono, color: tok.textSecondary })}>{row.repo}/{row.subject}</span>
            )}
            <div className={css({ fontSize: '12px', color: tok.textTertiary, fontFamily: FONTS.mono })}>
              {[detail.library, detail.shape, detail.binding, detail.group_id ? `group ${detail.group_id}` : '', `tier ${row.tier}`]
                .filter(Boolean)
                .join(' · ')}
            </div>
          </div>
        )
      })}
    </section>
  )
}
