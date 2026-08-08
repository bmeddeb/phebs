import { useEffect, useState } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import {
  fetchInvestigationView,
  fetchInvestigationViews,
  type InvestigationEnvelope,
  type InvestigationFact,
  type InvestigationViewDocument,
  type InvestigationViewSummary,
} from '../api'
import { LoadingBlock } from '../components/kit'
import { ChevronDown, ChevronRight, WarningIcon } from '../icons'
import { navigate } from '../router'
import { FONTS, usePhebsTokens } from '../theme'
import { isAbortError, relTime } from '../util'

type ViewTab = 'overview' | 'census' | 'coverage' | 'evidence'

const tabs: { id: ViewTab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'census', label: 'Census' },
  { id: 'coverage', label: 'Coverage' },
  { id: 'evidence', label: 'Evidence' },
]

export default function InvestigationPage({ params }: { params: URLSearchParams }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const requestedID = params.get('id') ?? ''
  const [summaries, setSummaries] = useState<InvestigationViewSummary[]>([])
  const [document, setDocument] = useState<InvestigationViewDocument | null>(null)
  const [tab, setTab] = useState<ViewTab>('overview')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    let active = true
    setLoading(true)
    setError('')
    fetchInvestigationViews(controller.signal)
      .then((views) => {
        if (!active) return
        setSummaries(views)
        const selected = views.some((view) => view.id === requestedID)
          ? requestedID
          : views[0]?.id ?? ''
        if (!selected) throw new Error('No authorized Investigation views are available.')
        return fetchInvestigationView(selected, controller.signal)
      })
      .then((view) => {
        if (active && view) setDocument(view)
      })
      .catch((cause) => {
        if (active && !isAbortError(cause)) setError(cause instanceof Error ? cause.message : String(cause))
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [requestedID])

  if (loading && !document) return <LoadingBlock label="Loading investigation views…" />
  if (error) {
    return (
      <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto', margin: 0 } } }}>
        {error}
      </Notification>
    )
  }
  if (!document) return null

  const envelope = document.envelope
  // On refusal every tab renders the same RefusalView; disable the inactive
  // tabs so the tablist doesn't promise views the refusal withholds.
  const refused = envelope.outcome === 'refused'
  return (
    <div className={css({ maxWidth: '1400px', margin: '0 auto', color: tok.textPrimary })}>
      <InvestigationHeader
        document={document}
        summaries={summaries}
        onSelect={(id) => navigate('/investigations', { id })}
      />
      {refused ? (
        <div role="status" className={css({ minHeight: '46px', display: 'flex', alignItems: 'center', borderBottom: `1px solid ${tok.cardBorder}`, color: tok.textTertiary, fontSize: '12px', lineHeight: '18px' })}>
          Investigation views are unavailable because this request was refused.
        </div>
      ) : (
        <div
          role="tablist"
          aria-label="Investigation views"
          className={css({
            display: 'flex',
            gap: '28px',
            minHeight: '46px',
            alignItems: 'stretch',
            borderBottom: `1px solid ${tok.cardBorder}`,
            overflowX: 'auto',
          })}
        >
          {tabs.map((item) => (
            <button
              key={item.id}
              type="button"
              role="tab"
              aria-selected={tab === item.id}
              onClick={() => setTab(item.id)}
              className={css({
                border: 0,
                borderBottom: tab === item.id ? `2px solid ${tok.accent}` : '2px solid transparent',
                padding: '0 2px',
                background: 'transparent',
                color: tab === item.id ? tok.accent : tok.textSecondary,
                fontFamily: FONTS.SANS,
                fontSize: '13px',
                fontWeight: tab === item.id ? 600 : 450,
                cursor: 'pointer',
                whiteSpace: 'nowrap',
                ':hover': { color: tok.textPrimary },
                ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
              })}
            >
              {item.label}
            </button>
          ))}
        </div>
      )}

      {envelope.outcome === 'refused' ? (
        <RefusalView envelope={envelope} />
      ) : (
        <>
          {tab === 'overview' && (
            <>
              <SummaryBand envelope={envelope} />
              <EligibilityView envelope={envelope} />
              <CensusView envelope={envelope} compact />
              <CoverageView envelope={envelope} compact />
              <EvidenceView envelope={envelope} compact />
            </>
          )}
          {tab === 'census' && <CensusView envelope={envelope} />}
          {tab === 'coverage' && <CoverageView envelope={envelope} />}
          {tab === 'evidence' && <EvidenceView envelope={envelope} />}
        </>
      )}
    </div>
  )
}

// The contract marks these fields omitempty (internal/investigation
// envelope.go FreshnessAnalysis): render only a present, parseable instant —
// an absent freshness time is stated by absence, never as an invalid date.
function FreshnessTime({ label, iso }: { label: string; iso?: string }) {
  if (!iso) return null
  const parsed = Date.parse(iso)
  if (!Number.isFinite(parsed)) return null
  return (
    <span>
      {label}{' '}
      <time dateTime={iso}>
        {relTime(iso)} · {new Date(parsed).toLocaleString()}
      </time>
    </span>
  )
}

function InvestigationHeader({
  document,
  summaries,
  onSelect,
}: {
  document: InvestigationViewDocument
  summaries: InvestigationViewSummary[]
  onSelect: (id: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const envelope = document.envelope
  const scope = envelope.scope
  const validation = envelope.validation
  const outcomeTone = envelope.outcome === 'ok'
    ? tok.status.current.text
    : envelope.outcome === 'partial'
      ? tok.status.stale.text
      : envelope.outcome === 'refused' || envelope.outcome === 'error'
        ? tok.status.conflict.text
        : undefined
  return (
    <header className={css({ paddingBottom: '14px' })}>
      <div className={css({ display: 'flex', gap: '20px', alignItems: 'flex-start', justifyContent: 'space-between', '@media screen and (max-width: 760px)': { flexDirection: 'column' } })}>
        <div className={css({ minWidth: 0 })}>
          <h1 className={css({ margin: 0, fontSize: '25px', lineHeight: '34px', fontWeight: 650, letterSpacing: '-0.015em', overflowWrap: 'anywhere' })}>
            {scope.normalized_question}
          </h1>
          <div className={css({ display: 'flex', gap: '18px', flexWrap: 'wrap', marginTop: '8px', color: tok.textTertiary, fontSize: '12px', lineHeight: '18px' })}>
            <Meta label="Referent" value={`${scope.claim.subject.kind}:${scope.claim.subject.id}`} mono />
            <Meta label="Claim family" value={humanize(scope.claim.claim_family)} />
            <Meta label="Decision sought" value={humanize(scope.claim.decision_sought)} />
            <FreshnessTime label="Analysis published" iso={envelope.freshness?.analysis.published} />
            <FreshnessTime label="Snapshot committed" iso={envelope.freshness?.analysis.snapshot_committed} />
          </div>
        </div>
        {summaries.length > 1 && (
          <label className={css({ display: 'grid', gap: '5px', flex: '0 0 270px', color: tok.textTertiary, fontSize: '11px', fontWeight: 500, '@media screen and (max-width: 760px)': { flex: '0 0 auto', width: '100%', maxWidth: '100%' } })}>
            Investigation view
            <select
              aria-label="Investigation view"
              value={document.id}
              onChange={(event) => onSelect(event.currentTarget.value)}
              className={css({
                height: '36px',
                border: `1px solid ${tok.cardBorder}`,
                borderRadius: '4px',
                padding: '0 10px',
                backgroundColor: tok.pageBg,
                color: tok.textPrimary,
                fontFamily: FONTS.SANS,
                fontSize: '12px',
                ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
              })}
            >
              {summaries.map((view) => <option key={view.id} value={view.id}>{view.title}</option>)}
            </select>
          </label>
        )}
      </div>
      <div
        className={css({
          display: 'grid',
          gridTemplateColumns: 'repeat(6, minmax(0, 1fr))',
          marginTop: '16px',
          borderTop: `1px solid ${tok.cardBorder}`,
          borderBottom: `1px solid ${tok.cardBorder}`,
          '@media screen and (max-width: 900px)': { gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' },
          '@media screen and (max-width: 560px)': { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' },
        })}
      >
        <RailItem label="Investigation" value={scope.investigation_id} mono />
        <RailItem label="Revision" value={scope.revision_id} mono />
        <RailItem label="Snapshot" value={scope.snapshot_id} mono />
        <RailItem label="Build" value={scope.build_configuration_id} mono />
        <RailItem label="Evidence pack" value={validation ? `${validation.pack_id} ${validation.pack_version}` : 'Unavailable'} mono />
        <RailItem label="Outcome" value={humanize(envelope.outcome)} tone={outcomeTone} />
      </div>
    </header>
  )
}

function SummaryBand({ envelope }: { envelope: InvestigationEnvelope }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const facts = envelope.payload?.data.facts ?? []
  const serviceUnknown = unknownFacts(facts, 'service')
  const ownerUnknown = unknownFacts(facts, 'owner')
  const blockerCount = envelope.absence_eligibility.blocker_codes.length
  const action = serviceUnknown + ownerUnknown > 0
    ? 'Resolve attribution'
    : blockerCount > 0
      ? 'Resolve blockers'
      : facts.length > 0
        ? 'Review findings'
        : envelope.absence_eligibility.eligible
          ? 'Review bounded absence'
          : 'Review coverage'
  const cells = [
    { title: 'What is evidenced', value: String(facts.length), detail: facts.length === 1 ? 'supported fact' : 'supported facts' },
    { title: 'What remains unknown', value: String(serviceUnknown + ownerUnknown), detail: `${serviceUnknown} service · ${ownerUnknown} owner` },
    { title: 'What changed', value: '—', detail: 'No comparison in this view' },
    { title: 'What needs action', value: action, detail: blockerCount > 0 ? `${blockerCount} blocker code${blockerCount === 1 ? '' : 's'}` : 'Derived from current evidence' },
  ]
  return (
    <section aria-label="Investigation overview" className={css({ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0, 1fr))', borderBottom: `1px solid ${tok.cardBorder}`, '@media screen and (max-width: 800px)': { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' }, '@media screen and (max-width: 480px)': { gridTemplateColumns: '1fr' } })}>
      {cells.map((cell) => (
        <div key={cell.title} className={css({ minHeight: '118px', boxSizing: 'border-box', padding: '20px 22px', borderRight: `1px solid ${tok.cardBorder}`, ':last-child': { borderRight: 0 } })}>
          <h2 className={css({ margin: 0, fontSize: '13px', lineHeight: '18px', fontWeight: 600 })}>{cell.title}</h2>
          <div className={css({ marginTop: '9px', color: tok.accent, fontSize: cell.value.length > 8 ? '18px' : '30px', lineHeight: cell.value.length > 8 ? '26px' : '34px', fontWeight: 650 })}>{cell.value}</div>
          <div className={css({ marginTop: '3px', color: tok.textTertiary, fontSize: '12px', lineHeight: '17px' })}>{cell.detail}</div>
        </div>
      ))}
    </section>
  )
}

function EligibilityView({ envelope }: { envelope: InvestigationEnvelope }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const eligibility = envelope.absence_eligibility
  let label = 'Not applicable'
  let detail = 'Supported facts or claim semantics make an absence statement inapplicable.'
  let tone = tok.textSecondary
  if (eligibility.applicable && eligibility.eligible) {
    label = 'Eligible for bounded absence statement'
    detail = eligibility.qualification?.authoritative_text ?? 'The current scope satisfies the pack rule.'
    tone = tok.status.current.text
  } else if (eligibility.applicable) {
    label = 'Not eligible'
    detail = eligibility.qualification?.authoritative_text ?? 'Coverage blockers prevent a bounded absence statement.'
    tone = tok.status.conflict.text
  }
  return (
    <section data-testid="derived-eligibility" className={css({ display: 'grid', gridTemplateColumns: '190px minmax(0, 1fr) minmax(220px, auto)', gap: '20px', alignItems: 'center', padding: '14px 0', borderBottom: `1px solid ${tok.cardBorder}`, '@media screen and (max-width: 760px)': { gridTemplateColumns: '1fr', gap: '7px' } })}>
      <div>
        <SectionLabel>Derived eligibility</SectionLabel>
        <div className={css({ color: tok.textTertiary, fontSize: '11px', marginTop: '2px' })}>Read-only</div>
      </div>
      <div>
        <div className={css({ color: tone, fontSize: '14px', lineHeight: '20px', fontWeight: 600 })}>{label}</div>
        <div className={css({ color: tok.textTertiary, fontSize: '12px', lineHeight: '18px', marginTop: '3px' })}>{detail}</div>
      </div>
      <div className={css({ color: tok.textSecondary, fontSize: '11px', lineHeight: '18px' })}>
        <div>Rule {eligibility.rule_version}</div>
        <div className={css({ fontFamily: FONTS.MONO, overflowWrap: 'anywhere', color: eligibility.blocker_codes.length ? tok.status.conflict.text : tok.textTertiary })}>
          {eligibility.blocker_codes.length ? eligibility.blocker_codes.join(' · ') : 'No blocker codes'}
        </div>
      </div>
    </section>
  )
}

function CensusView({ envelope, compact = false }: { envelope: InvestigationEnvelope; compact?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const facts = envelope.payload?.data.facts ?? []
  const [expanded, setExpanded] = useState<string[]>([])
  const coverage = envelope.coverage
  const toggle = (id: string) => setExpanded((current) => current.includes(id) ? current.filter((value) => value !== id) : [...current, id])
  const emptyText = coverage && (coverage.processing.failed > 0 || coverage.processing.partial > 0)
    ? 'No supported facts were found among analyzed units.'
    : envelope.absence_eligibility.qualification?.authoritative_text ??
      'No supported facts are present in the current authorized view.'
  return (
    <section className={sectionStyle(css, tok, compact)}>
      <SectionHeading>Consumer census</SectionHeading>
      <div className={css({ overflowX: 'auto', border: `1px solid ${tok.cardBorder}` })}>
        <table className={css(tableStyle())}>
          <thead>
            <tr>{['', 'Consumer occurrence', 'Predicate', 'Service', 'Owner', 'Semantic state', 'Evidence'].map((label, index) => <HeaderCell key={`${label}-${index}`}>{label}</HeaderCell>)}</tr>
          </thead>
          <tbody>
            {facts.length === 0 ? (
              <tr><td colSpan={7} className={css({ padding: '24px', textAlign: 'center', color: tok.textSecondary, fontSize: '13px', lineHeight: '20px' })}>{emptyText}</td></tr>
            ) : facts.flatMap((fact) => {
              const open = expanded.includes(fact.fact_id)
              return [
                <tr key={fact.fact_id} className={css({ borderTop: `1px solid ${tok.innerSep}`, ':hover': { backgroundColor: tok.hoverFill } })}>
                  <td className={css({ width: '34px', padding: '0 7px' })}>
                    <button type="button" aria-label={`${open ? 'Collapse' : 'Expand'} ${fact.subject.id}`} onClick={() => toggle(fact.fact_id)} className={disclosureStyle(css, tok)}>
                      {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                    </button>
                  </td>
                  <Cell mono>{fact.subject.id}</Cell>
                  <Cell mono>{fact.predicate}</Cell>
                  <AttributionCell fact={fact} hop="service" />
                  <AttributionCell fact={fact} hop="owner" />
                  <Cell>{humanize(fact.semantic_resolution.value)}</Cell>
                  <Cell mono>{fact.proof_references.length} proof ref{fact.proof_references.length === 1 ? '' : 's'}</Cell>
                </tr>,
                open ? <DerivationRow key={`${fact.fact_id}-detail`} fact={fact} /> : null,
              ]
            })}
          </tbody>
        </table>
      </div>
      {facts.length === 0 && coverage && (coverage.processing.failed > 0 || coverage.processing.partial > 0 || coverage.processing.excluded > 0) && (
        <ProcessingGaps envelope={envelope} />
      )}
    </section>
  )
}

function CoverageView({ envelope, compact = false }: { envelope: InvestigationEnvelope; compact?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const coverage = envelope.coverage
  if (!coverage) return null
  const counts = [
    ['Eligible', coverage.eligible_units, tok.accent],
    ['Analyzed', coverage.processing.analyzed, tok.accent],
    ['Failed', coverage.processing.failed, coverage.processing.failed ? tok.status.conflict.text : tok.status.current.text],
    ['Partial', coverage.processing.partial, coverage.processing.partial ? tok.status.stale.text : tok.status.current.text],
    ['Excluded', coverage.processing.excluded, coverage.processing.excluded ? tok.status.stale.text : tok.textSecondary],
  ] as const
  return (
    <section className={sectionStyle(css, tok, compact)}>
      <SectionHeading>Coverage</SectionHeading>
      <div className={css({ display: 'grid', gridTemplateColumns: 'repeat(5, minmax(0, 1fr))', border: `1px solid ${tok.cardBorder}`, '@media screen and (max-width: 680px)': { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' } })}>
        {counts.map(([label, value, color]) => (
          <div key={label} className={css({ padding: '13px 16px', borderRight: `1px solid ${tok.cardBorder}` })}>
            <div className={css({ color, fontSize: '21px', lineHeight: '25px', fontWeight: 600 })}>{value}</div>
            <div className={css({ color: tok.textTertiary, fontSize: '11px', marginTop: '2px' })}>{label}</div>
          </div>
        ))}
      </div>
      <div className={css({ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0, 1fr))', borderLeft: `1px solid ${tok.cardBorder}`, borderRight: `1px solid ${tok.cardBorder}`, borderBottom: `1px solid ${tok.cardBorder}`, '@media screen and (max-width: 680px)': { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' } })}>
        {Object.entries(coverage.attribution).map(([hop, value]) => {
          const complete = value.denominator === 0 || value.resolved + value.not_applicable === value.denominator
          return (
            <div key={hop} className={css({ padding: '13px 16px', borderRight: `1px solid ${tok.cardBorder}` })}>
              <div className={css({ display: 'flex', justifyContent: 'space-between', gap: '8px', fontSize: '12px' })}>
                <span>{humanize(hop)}</span>
                <span className={css({ fontFamily: FONTS.MONO, color: complete ? tok.status.current.text : tok.status.stale.text })}>{value.resolved}/{value.denominator}</span>
              </div>
              <div className={css({ height: '2px', marginTop: '8px', backgroundColor: tok.fill })}>
                <div className={css({ height: '2px', width: `${value.denominator ? Math.min(100, value.resolved / value.denominator * 100) : 100}%`, backgroundColor: complete ? tok.status.current.solid : tok.status.stale.solid })} />
              </div>
            </div>
          )
        })}
      </div>
      <div className={css({ display: 'flex', gap: '16px', flexWrap: 'wrap', paddingTop: '9px', color: tok.textTertiary, fontSize: '11px' })}>
        <span>Processing reconciled: {coverage.processing_reconciled ? 'yes' : 'no'}</span>
        <span>Gate: {humanize(coverage.outcome_gates.status)}</span>
        <span className={css({ fontFamily: FONTS.MONO })}>{coverage.outcome_gates.reason_codes.join(' · ') || 'No gate reason codes'}</span>
      </div>
    </section>
  )
}

function EvidenceView({ envelope, compact = false }: { envelope: InvestigationEnvelope; compact?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const facts = envelope.payload?.data.facts ?? []
  const rows = facts.flatMap((fact) => fact.proof_references.map((proof) => ({ fact, proof })))
  return (
    <section className={sectionStyle(css, tok, compact)}>
      <SectionHeading>Evidence</SectionHeading>
      <div className={css({ overflowX: 'auto', border: `1px solid ${tok.cardBorder}` })}>
        <table className={css(tableStyle())}>
          <thead><tr>{['Proof ID', 'Snapshot', 'Occurrence', 'Verification action', 'Evidence basis'].map((label) => <HeaderCell key={label}>{label}</HeaderCell>)}</tr></thead>
          <tbody>
            {rows.length === 0 ? (
              <tr><td colSpan={5} className={css({ padding: '24px', textAlign: 'center', color: tok.textTertiary, fontSize: '13px' })}>No published proof references are present in this view.</td></tr>
            ) : rows.map(({ fact, proof }) => (
              <tr key={`${fact.fact_id}:${proof.proof_id}`} className={css({ borderTop: `1px solid ${tok.innerSep}`, ':hover': { backgroundColor: tok.hoverFill } })}>
                <Cell mono>{proof.proof_id}</Cell>
                <Cell mono>{proof.snapshot_id}</Cell>
                <Cell mono>{proof.occurrence_id}</Cell>
                <Cell mono>{proof.verification_action}</Cell>
                <Cell>{humanize(fact.evidence_basis)}</Cell>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function RefusalView({ envelope }: { envelope: InvestigationEnvelope }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const validation = envelope.validation
  return (
    <section className={css({ marginTop: '22px', borderLeft: `4px solid ${tok.status.conflict.solid}`, padding: '17px 20px', backgroundColor: tok.bandBg })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '9px', color: tok.status.conflict.text })}>
        <WarningIcon size={17} />
        <h2 className={css({ margin: 0, fontSize: '16px', lineHeight: '22px', fontWeight: 650 })}>Pack workflow unavailable</h2>
      </div>
      <p className={css({ margin: '9px 0 17px', fontSize: '14px', lineHeight: '21px', color: tok.textPrimary })}>
        {envelope.refusal?.authoritative_text ?? 'This claim-bearing view is unavailable.'}
      </p>
      {validation && (
        <div className={css({ display: 'grid', gridTemplateColumns: 'repeat(5, minmax(0, 1fr))', borderTop: `1px solid ${tok.cardBorder}`, '@media screen and (max-width: 780px)': { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' } })}>
          <RailItem label="Pack" value={`${validation.pack_id} ${validation.pack_version}`} mono />
          <RailItem label="Status" value={humanize(validation.pack_status)} tone={tok.status.conflict.text} />
          <RailItem label="Workflow" value={humanize(validation.workflow_eligibility)} tone={tok.status.conflict.text} />
          <RailItem label="Expired" value={validation.expires_at.slice(0, 10)} mono />
          <RailItem label="Blocker" value={validation.applicability_blockers.join(' · ') || envelope.refusal?.code || 'Unavailable'} mono tone={tok.status.conflict.text} />
        </div>
      )}
    </section>
  )
}

function ProcessingGaps({ envelope }: { envelope: InvestigationEnvelope }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const coverage = envelope.coverage
  if (!coverage) return null
  const rows = [
    { outcome: 'Failed', count: coverage.processing.failed, code: 'UNITS_FAILED', tone: tok.status.conflict.text },
    { outcome: 'Partial', count: coverage.processing.partial, code: 'UNITS_PARTIAL', tone: tok.status.stale.text },
    ...Object.entries(coverage.exclusions_by_reason).map(([code, count]) => ({ outcome: 'Excluded', count, code, tone: tok.status.stale.text })),
  ].filter((row) => row.count > 0)
  return (
    <div className={css({ marginTop: '14px' })}>
      <SectionLabel>Processing gaps</SectionLabel>
      <table className={css({ ...tableStyle(), marginTop: '7px', border: `1px solid ${tok.cardBorder}` })}>
        <thead><tr>{['Outcome', 'Units', 'Code'].map((label) => <HeaderCell key={label}>{label}</HeaderCell>)}</tr></thead>
        <tbody>{rows.map((row) => (
          <tr key={row.code} className={css({ borderTop: `1px solid ${tok.innerSep}` })}>
            <Cell><span className={css({ color: row.tone })}>{row.outcome}</span></Cell>
            <Cell mono>{row.count}</Cell>
            <Cell mono>{row.code}</Cell>
          </tr>
        ))}</tbody>
      </table>
    </div>
  )
}

function DerivationRow({ fact }: { fact: InvestigationFact }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <tr className={css({ backgroundColor: tok.bandBg, borderTop: `1px solid ${tok.innerSep}` })}>
      <td />
      <td colSpan={6} className={css({ padding: '10px 12px 12px' })}>
        <div className={css({ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0, 1fr))', gap: '10px', '@media screen and (max-width: 720px)': { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' } })}>
          {Object.entries(fact.attribution_states).map(([hop, state]) => (
            <div key={hop}>
              <div className={css({ color: tok.textTertiary, fontSize: '10px', textTransform: 'uppercase', letterSpacing: '0.04em' })}>{humanize(hop)}</div>
              <div className={css({ marginTop: '3px', color: state.state === 'resolved' ? tok.status.current.text : tok.status.stale.text, fontSize: '12px' })}>{humanize(state.state)}</div>
              {state.reason_codes.length > 0 && <div className={css({ marginTop: '3px', fontFamily: FONTS.MONO, color: tok.textTertiary, fontSize: '10px', overflowWrap: 'anywhere' })}>{state.reason_codes.join(' · ')}</div>}
            </div>
          ))}
        </div>
      </td>
    </tr>
  )
}

function AttributionCell({ fact, hop }: { fact: InvestigationFact; hop: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const value = fact.attribution_states[hop]
  const unresolved = !value || value.state === 'unresolved' || value.state === 'ambiguous'
  return <Cell><span className={css({ color: unresolved ? tok.status.stale.text : tok.textSecondary })}>{unresolved ? 'Unresolved' : humanize(value.state)}</span></Cell>
}

function Meta({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <span><span className={css({ color: tok.textSecondary, fontWeight: 550 })}>{label}:</span> <span className={css({ fontFamily: mono ? FONTS.MONO : FONTS.SANS, overflowWrap: 'anywhere' })}>{value}</span></span>
}

function RailItem({ label, value, mono = false, tone }: { label: string; value: string; mono?: boolean; tone?: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ minWidth: 0, padding: '10px 12px', borderRight: `1px solid ${tok.cardBorder}` })}>
      <div className={css({ color: tok.textTertiary, fontSize: '10px', lineHeight: '14px' })}>{label}</div>
      <div className={css({ marginTop: '3px', color: tone ?? tok.textPrimary, fontFamily: mono ? FONTS.MONO : FONTS.SANS, fontSize: '11px', lineHeight: '16px', overflowWrap: 'anywhere' })}>{value}</div>
    </div>
  )
}

function SectionHeading({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  return <h2 className={css({ margin: '0 0 9px', fontSize: '18px', lineHeight: '25px', fontWeight: 650 })}>{children}</h2>
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  return <div className={css({ fontSize: '12px', lineHeight: '17px', fontWeight: 600 })}>{children}</div>
}

function HeaderCell({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <th className={css({ padding: '8px 10px', textAlign: 'left', color: tok.textTertiary, backgroundColor: tok.bandBg, fontSize: '10px', lineHeight: '14px', fontWeight: 600, whiteSpace: 'nowrap' })}>{children}</th>
}

function Cell({ children, mono = false }: { children: React.ReactNode; mono?: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <td className={css({ padding: '9px 10px', color: tok.textSecondary, fontFamily: mono ? FONTS.MONO : FONTS.SANS, fontSize: mono ? '11px' : '12px', lineHeight: '17px', whiteSpace: 'nowrap' })}>{children}</td>
}

function tableStyle() {
  return { width: '100%', borderCollapse: 'collapse' as const, tableLayout: 'auto' as const }
}

function sectionStyle(css: ReturnType<typeof useStyletron>[0], tok: ReturnType<typeof usePhebsTokens>, compact: boolean) {
  return css({
    paddingTop: compact ? '20px' : '24px',
    paddingBottom: compact ? '4px' : '8px',
    borderBottom: compact ? `1px solid ${tok.cardBorder}` : 0,
  })
}

function disclosureStyle(css: ReturnType<typeof useStyletron>[0], tok: ReturnType<typeof usePhebsTokens>) {
  return css({
    width: '26px',
    height: '26px',
    padding: 0,
    display: 'grid',
    placeItems: 'center',
    border: 0,
    background: 'transparent',
    color: tok.textTertiary,
    cursor: 'pointer',
    ':hover': { color: tok.textPrimary, backgroundColor: tok.fill },
    ':focus-visible': { outline: `2px solid ${tok.accent}` },
  })
}

function unknownFacts(facts: InvestigationFact[], hop: string): number {
  return facts.filter((fact) => {
    const state = fact.attribution_states[hop]?.state
    return state === 'unresolved' || state === 'ambiguous'
  }).length
}

function humanize(value: string): string {
  return value.replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase())
}
