import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND, SIZE as BUTTON_SIZE } from 'baseui/button'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import {
  createWorkbench,
  fetchContractCatalog,
  fetchWorkbench,
  previewWorkbench,
  reviseWorkbench,
  WorkbenchAPIError,
  type ContractCatalogItem,
  type WorkbenchContractSelection,
  type WorkbenchPlan,
  type WorkbenchPreview,
  type WorkbenchProposalCommitment,
  type WorkbenchProposalSource,
  type WorkbenchSelectionRole,
  type WorkbenchTicketKind,
  type WorkbenchView,
} from '../api'
import { href, navigate } from '../router'
import { FONTS, usePhebsTokens, type PhebsTokens } from '../theme'
import { isAbortError } from '../util'
import { StatusChip } from '../components/kit'
import {
  WorkbenchHowStep,
  WorkbenchWhereStep,
} from './WorkbenchEvidenceSteps'
import {
  workbenchEvidenceInputFromRoute,
  workbenchServiceRouteParams,
  type WorkbenchEvidenceInput,
} from './workbenchEvidenceState'

const ticketTextLimit = 16 << 10
const selectionLimit = 3
const proposalFileLimit = 256
const proposalFileBytes = 4 << 20
const proposalAggregateBytes = 32 << 20

type WorkbenchStep = 'why' | 'what' | 'where' | 'how'

const steps: { id: WorkbenchStep; eyebrow: string; label: string }[] = [
  { id: 'why', eyebrow: '01', label: 'Why' },
  { id: 'what', eyebrow: '02', label: 'What' },
  { id: 'where', eyebrow: '03', label: 'Where' },
  { id: 'how', eyebrow: '04', label: 'How' },
]

interface PageFailure {
  title: string
  detail: string
  status?: number
}

export default function WorkbenchPage({
  params,
  evidenceAvailable = false,
}: {
  params: URLSearchParams
  evidenceAvailable?: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const investigationID = params.get('investigation_id') ?? ''
  const revisionID = params.get('revision_id') ?? ''
  const requestedStep = params.get('step')
  const step = isWorkbenchStep(requestedStep) ? requestedStep : 'why'
  const routeKey = `${investigationID}\u0000${revisionID}`
  const seedKey = params.toString()
  const atlasSeed = useMemo(
    () => planFromAtlasRoute(params),
    // URLSearchParams is recreated by the hash router. Its canonical bytes
    // are the stable seed identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [seedKey],
  )
	const routeEvidence = useMemo(
		() => workbenchEvidenceInputFromRoute(params),
		// See atlasSeed: canonical route bytes are the seed identity.
		// eslint-disable-next-line react-hooks/exhaustive-deps
		[seedKey],
	)
  const [plan, setPlan] = useState<WorkbenchPlan | null>(atlasSeed)
  const [view, setView] = useState<WorkbenchView | null>(null)
  const [storedProposal, setStoredProposal] =
    useState<WorkbenchProposalCommitment | null>(null)
  const [baselineFingerprint, setBaselineFingerprint] = useState('')
  const [loading, setLoading] = useState(Boolean(investigationID))
  const [failure, setFailure] = useState<PageFailure | null>(null)
  const [reload, setReload] = useState(0)
  const [preview, setPreview] = useState<WorkbenchPreview | null>(null)
  const [previewFingerprint, setPreviewFingerprint] = useState('')
  const [previewing, setPreviewing] = useState(false)
  const [committing, setCommitting] = useState(false)
  const [actionFailure, setActionFailure] = useState<PageFailure | null>(null)
  const [evidence, setEvidence] = useState<WorkbenchEvidenceInput>(
    routeEvidence,
  )
  const previousRouteKey = useRef(routeKey)

  useEffect(() => {
    setEvidence(routeEvidence)
  }, [routeEvidence, routeKey])

  useEffect(() => {
    if (!investigationID && atlasSeed) {
      setPlan((current) => current ?? atlasSeed)
    }
  }, [atlasSeed, investigationID])

  useEffect(() => {
    const leftRevisionRoute = !investigationID &&
      previousRouteKey.current !== '\u0000'
    previousRouteKey.current = routeKey
    if (!investigationID) {
      if (leftRevisionRoute) {
        setPlan(atlasSeed)
        setView(null)
        setStoredProposal(null)
        setBaselineFingerprint('')
        setPreview(null)
        setPreviewFingerprint('')
        setActionFailure(null)
      }
      setLoading(false)
      setFailure(null)
      return
    }
    if (!revisionID) {
      setLoading(false)
      setFailure({
        title: 'Exact revision required',
        detail: 'This Workbench link does not name an Investigation revision. Resume it again to create an exact deep link.',
      })
      return
    }
    const controller = new AbortController()
    let active = true
    setLoading(true)
    setFailure(null)
    fetchWorkbench(investigationID, controller.signal)
      .then((nextView) => {
        if (!active) return
        if (nextView.revision.revision_id !== revisionID) {
          setView(nextView)
          setPlan(null)
          setStoredProposal(null)
          setBaselineFingerprint('')
          setFailure({
            title: 'Revision changed',
            detail: `This link pins ${revisionID}, but the current authorized revision is ${nextView.revision.revision_id}. The page will not silently retarget it.`,
            status: 409,
          })
          return
        }
        const nextPlan = planFromView(nextView)
        setView(nextView)
        setPlan(nextPlan)
        setStoredProposal(nextView.brief.what.proposal ?? null)
        setBaselineFingerprint(planFingerprint(nextPlan))
        setPreview(null)
        setPreviewFingerprint('')
        setActionFailure(null)
      })
      .catch((cause) => {
        if (active && !isAbortError(cause)) {
          setView(null)
          setPlan(null)
          setStoredProposal(null)
          setBaselineFingerprint('')
          setFailure(workbenchFailure(cause, 'load'))
        }
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [routeKey, reload, investigationID, revisionID, atlasSeed])

  const fingerprint = plan ? planFingerprint(plan) : ''
  const dirty = plan !== null && (
    baselineFingerprint === '' || fingerprint !== baselineFingerprint
  )
  const previewDrifted = preview !== null &&
    previewFingerprint !== fingerprint

  useEffect(() => {
    if (!dirty) return
    const preventLoss = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    const confirmInAppLeave = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0 ||
        event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
      const target = event.target instanceof Element
        ? event.target.closest<HTMLAnchorElement>('a[href]')
        : null
      if (!target || target.target === '_blank') return
      const destination = new URL(target.href, window.location.href)
      const current = window.location
      if (destination.origin !== current.origin ||
        destination.pathname !== current.pathname ||
        destination.search !== current.search ||
        !destination.hash.startsWith('#/')) return
      const destinationPath = destination.hash
        .slice(1)
        .split('?', 1)[0]
      if (destinationPath === '/workbench') {
        const queryIndex = destination.hash.indexOf('?')
        const destinationParams = new URLSearchParams(
          queryIndex === -1 ? '' : destination.hash.slice(queryIndex + 1),
        )
        const preservesExactRevision = !investigationID ||
          (
            destinationParams.get('investigation_id') === investigationID &&
            destinationParams.get('revision_id') === revisionID
          )
        if (preservesExactRevision) return
      }
      if (!window.confirm(
        'Leave the Workbench and discard unsaved Why or What edits?',
      )) {
        event.preventDefault()
        event.stopPropagation()
      }
    }
    window.addEventListener('beforeunload', preventLoss)
    document.addEventListener('click', confirmInAppLeave, true)
    return () => {
      window.removeEventListener('beforeunload', preventLoss)
      document.removeEventListener('click', confirmInAppLeave, true)
    }
  }, [dirty, investigationID, revisionID])

  const previewPlan = async () => {
    if (!plan) return
    const canonical = canonicalPlan(plan)
    const limitFailure = proposalLimitFailure(canonical)
    setActionFailure(null)
    if (limitFailure) {
      setPreview(null)
      setPreviewFingerprint('')
      setActionFailure({
        title: 'Source limit refusal',
        detail: limitFailure,
        status: 413,
      })
      return
    }
    setPlan(canonical)
    setPreviewing(true)
    try {
      const nextPreview = await previewWorkbench(canonical)
      setPreview(nextPreview)
      setPreviewFingerprint(planFingerprint(canonical))
    } catch (cause) {
      if (!isAbortError(cause)) {
        setPreview(null)
        setPreviewFingerprint('')
        setActionFailure(workbenchFailure(cause, 'preview'))
      }
    } finally {
      setPreviewing(false)
    }
  }

  const commitPlan = async () => {
    if (!plan || !preview?.ready || !preview.preview_digest ||
      previewDrifted) return
    const canonical = canonicalPlan(plan)
    const mutation = {
      plan: canonical,
      preview_digest: preview.preview_digest,
      idempotency_key: `workbench-ui-${preview.preview_digest}`,
    }
    setCommitting(true)
    setActionFailure(null)
    try {
      const committed = canonical.investigation_id
        ? await reviseWorkbench(canonical.investigation_id, mutation)
        : await createWorkbench(mutation)
      navigate('/workbench', {
        investigation_id: committed.investigation.investigation_id,
        revision_id: committed.revision.revision_id,
        step: 'what',
			...workbenchServiceRouteParams(evidence.filters),
      })
    } catch (cause) {
      if (!isAbortError(cause)) {
        setPreview(null)
        setPreviewFingerprint('')
        setActionFailure(workbenchFailure(cause, 'commit'))
      }
    } finally {
      setCommitting(false)
    }
  }

  if (loading) {
    return (
      <div
        role="status"
        aria-live="polite"
        className={css({
          minHeight: '55vh',
          display: 'grid',
          placeItems: 'center',
          color: tok.textSecondary,
        })}
      >
        <span className={css({ display: 'grid', justifyItems: 'center', gap: '12px' })}>
          <Spinner $size="small" />
          Loading exact Workbench revision…
        </span>
      </div>
    )
  }

  if (failure) {
    return (
      <FailurePage
        failure={failure}
        view={view}
        onRetry={() => setReload((value) => value + 1)}
      />
    )
  }

  if (!plan) {
    return (
      <WorkbenchStart
        onStart={(nextPlan) => {
          setPlan(nextPlan)
          setView(null)
          setStoredProposal(null)
          setBaselineFingerprint('')
          setPreview(null)
          setPreviewFingerprint('')
          navigate('/workbench', {
				source: 'ticket',
				step: 'why',
				...workbenchServiceRouteParams(evidence.filters),
			})
        }}
      />
    )
  }

  return (
    <div
      data-testid="workbench-shell"
      data-responsive-layout="desktop-rail-mobile-strip"
      className={css({
        width: '100%',
        maxWidth: '1480px',
        margin: '0 auto',
        color: tok.textPrimary,
      })}
    >
      <WorkbenchHeader
        plan={plan}
        view={view}
        dirty={dirty}
        previewDrifted={previewDrifted}
      />
      <div
        className={css({
          display: 'grid',
          gridTemplateColumns: '220px minmax(0, 1fr)',
          gap: '28px',
          alignItems: 'start',
          '@media screen and (max-width: 820px)': {
            gridTemplateColumns: '1fr',
            gap: '18px',
          },
        })}
      >
        <StepRail
          step={step}
          investigationID={investigationID}
          revisionID={revisionID}
		  serviceRoute={workbenchServiceRouteParams(evidence.filters)}
        />
        <main className={css({ minWidth: 0 })}>
          {step === 'why' && (
            <WhyEditor
              plan={plan}
              lockedIdentity={view !== null}
              onChange={setPlan}
            />
          )}
          {step === 'what' && (
            <WhatEditor
              plan={plan}
              storedProposal={storedProposal}
              onChange={setPlan}
            />
          )}
          {step === 'where' && (
            <WorkbenchWhereStep
              available={evidenceAvailable}
              investigationID={investigationID}
              revisionID={revisionID}
              evidence={evidence}
              onEvidenceChange={setEvidence}
            />
          )}
          {step === 'how' && (
            <WorkbenchHowStep
              available={evidenceAvailable}
              investigationID={investigationID}
              revisionID={revisionID}
              evidence={evidence}
              onEvidenceChange={setEvidence}
            />
          )}

          {(step === 'why' || step === 'what') && (
            <PreviewDock
              plan={plan}
              preview={preview}
              previewDrifted={previewDrifted}
              previewing={previewing}
              committing={committing}
              failure={actionFailure}
              onPreview={() => void previewPlan()}
              onCommit={() => void commitPlan()}
            />
          )}
        </main>
      </div>
    </div>
  )
}

function WorkbenchStart({
  onStart,
}: {
  onStart: (plan: WorkbenchPlan) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [ticketText, setTicketText] = useState('')
  const [title, setTitle] = useState('')
  const [kind, setKind] = useState<WorkbenchTicketKind>('modify')
  const [resumeID, setResumeID] = useState('')
  const [resumeLoading, setResumeLoading] = useState(false)
  const [resumeFailure, setResumeFailure] = useState<PageFailure | null>(null)
  const ticketBytes = new TextEncoder().encode(ticketText).length

  const resume = async (event: React.FormEvent) => {
    event.preventDefault()
    const id = resumeID.trim()
    if (!id) return
    setResumeLoading(true)
    setResumeFailure(null)
    try {
      const view = await fetchWorkbench(id)
      navigate('/workbench', {
        investigation_id: view.investigation.investigation_id,
        revision_id: view.revision.revision_id,
        step: 'what',
      })
    } catch (cause) {
      if (!isAbortError(cause)) {
        setResumeFailure(workbenchFailure(cause, 'load'))
      }
    } finally {
      setResumeLoading(false)
    }
  }

  return (
    <div className={css({ maxWidth: '1180px', margin: '12px auto 0' })}>
      <header className={css({
        display: 'grid',
        gridTemplateColumns: 'minmax(0, 1.4fr) minmax(280px, 0.6fr)',
        gap: '32px',
        alignItems: 'end',
        padding: '34px 0 24px',
        borderBottom: `1px solid ${tok.cardBorder}`,
        '@media screen and (max-width: 760px)': {
          gridTemplateColumns: '1fr',
          gap: '12px',
          paddingTop: '16px',
        },
      })}>
        <div>
          <div className={css(eyebrowStyle(tok))}>Change Workbench · synthetic preview</div>
          <h1 className={css({
            maxWidth: '760px',
            margin: '8px 0 0',
            fontSize: '22px',
            lineHeight: '30px',
            fontWeight: 650,
          })}>
            Plan one contract change against cited evidence.
          </h1>
        </div>
        <p className={css({
          margin: 0,
          color: tok.textTertiary,
          fontSize: '13px',
          lineHeight: '20px',
        })}>
          Previewing is read-only. Creating or revising happens only after an
          explicit confirmation against the exact preview digest.
        </p>
      </header>

      <div className={css({
        display: 'grid',
        gridTemplateColumns: 'minmax(0, 1.45fr) minmax(280px, 0.55fr)',
        gap: '28px',
        marginTop: '28px',
        '@media screen and (max-width: 820px)': { gridTemplateColumns: '1fr' },
      })}>
        <form
          aria-labelledby="start-ticket-heading"
          onSubmit={(event) => {
            event.preventDefault()
            if (!ticketText.trim() || !title.trim() ||
              ticketBytes > ticketTextLimit) return
            onStart(planFromTicket(title, ticketText, kind))
          }}
          className={css(panelStyle(tok))}
        >
          <div className={css({ padding: '20px', borderBottom: `1px solid ${tok.cardBorder}` })}>
            <div className={css(eyebrowStyle(tok))}>Start new</div>
            <h2 id="start-ticket-heading" className={css(sectionHeadingStyle())}>
              Paste bounded ticket context
            </h2>
          </div>
          <div className={css({ display: 'grid', gap: '16px', padding: '20px' })}>
            <TextField
              label="Workbench title"
              value={title}
              maxLength={512}
              placeholder="Stabilize checkout receipt identifiers"
              onChange={setTitle}
            />
            <label className={css(fieldLabelStyle(tok))}>
              Change mode
              <select
                aria-label="Change mode"
                value={kind}
                onChange={(event) =>
                  setKind(event.currentTarget.value as WorkbenchTicketKind)}
                className={css(controlStyle(tok))}
              >
                <option value="add">Add</option>
                <option value="modify">Modify</option>
                <option value="migrate">Migrate</option>
                <option value="retire">Retire</option>
              </select>
            </label>
            <label className={css(fieldLabelStyle(tok))}>
              Ticket text
              <textarea
                aria-label="Ticket text"
                aria-invalid={ticketBytes > ticketTextLimit}
                value={ticketText}
                rows={12}
                placeholder="Paste the problem, desired outcome, constraints, and known contract identity. Phebs does not infer completion from this text."
                onChange={(event) => setTicketText(event.currentTarget.value)}
                className={css(textareaStyle(tok))}
              />
              <span className={css(fieldHintStyle(tok))}>
                {ticketBytes.toLocaleString()} / {ticketTextLimit.toLocaleString()} bytes
              </span>
              {ticketBytes > ticketTextLimit && (
                <span role="alert" className={css({
                  color: tok.status.conflict.text,
                  fontSize: '11px',
                  lineHeight: '17px',
                })}>
                  Ticket text exceeds the 16 KiB UTF-8 limit. Nothing was
                  shaped; shorten it to continue.
                </span>
              )}
            </label>
            <Button
              type="submit"
              size={BUTTON_SIZE.compact}
              disabled={!ticketText.trim() || !title.trim() ||
                ticketBytes > ticketTextLimit}
            >
              Shape the Why
            </Button>
          </div>
        </form>

        <form
          aria-labelledby="resume-heading"
          onSubmit={resume}
          className={css(panelStyle(tok))}
        >
          <div className={css({ padding: '20px', borderBottom: `1px solid ${tok.cardBorder}` })}>
            <div className={css(eyebrowStyle(tok))}>Resume</div>
            <h2 id="resume-heading" className={css(sectionHeadingStyle())}>
              Open an Investigation
            </h2>
          </div>
          <div className={css({ display: 'grid', gap: '14px', padding: '20px' })}>
            <TextField
              label="Investigation ID"
              value={resumeID}
              maxLength={26}
              mono
              placeholder="01J…"
              onChange={setResumeID}
            />
            <Button
              type="submit"
              size={BUTTON_SIZE.compact}
              kind={BUTTON_KIND.secondary}
              isLoading={resumeLoading}
              disabled={!resumeID.trim()}
            >
              Resume current revision
            </Button>
            {resumeFailure && (
              <InlineFailure failure={resumeFailure} />
            )}
            <p className={css({
              margin: 0,
              color: tok.textTertiary,
              fontSize: '11px',
              lineHeight: '17px',
            })}>
              Resume first resolves the authorized current revision, then puts
              that exact revision ID in the URL. Unknown and unauthorized IDs
              have the same response.
            </p>
          </div>
        </form>
      </div>
    </div>
  )
}

function WorkbenchHeader({
  plan,
  view,
  dirty,
  previewDrifted,
}: {
  plan: WorkbenchPlan
  view: WorkbenchView | null
  dirty: boolean
  previewDrifted: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <header className={css({
      display: 'grid',
      gridTemplateColumns: 'minmax(0, 1fr) auto',
      gap: '22px',
      alignItems: 'end',
      padding: '12px 0 20px',
      marginBottom: '24px',
      borderBottom: `1px solid ${tok.cardBorder}`,
      '@media screen and (max-width: 680px)': {
        gridTemplateColumns: '1fr',
        gap: '12px',
      },
    })}>
      <div className={css({ minWidth: 0 })}>
        <div className={css(eyebrowStyle(tok))}>
          Change Workbench · {plan.brief.ticket_kind}
        </div>
        <h1 className={css({
          margin: '6px 0 0',
          fontSize: 'clamp(26px, 3vw, 40px)',
          lineHeight: 1.08,
          letterSpacing: '-0.035em',
          overflowWrap: 'anywhere',
        })}>
          {plan.title}
        </h1>
        <div className={css({
          display: 'flex',
          gap: '14px',
          flexWrap: 'wrap',
          marginTop: '10px',
          color: tok.textTertiary,
          fontSize: '11px',
          lineHeight: '16px',
        })}>
          <span>Investigation <Mono>{view?.investigation.investigation_id ?? 'not created'}</Mono></span>
          <span>Revision <Mono>{view?.revision.revision_id ?? 'draft'}</Mono></span>
          <span>Sequence <Mono>{view?.revision.seq ?? '—'}</Mono></span>
        </div>
      </div>
      <div
        role="status"
        aria-live="polite"
        className={css({
          display: 'flex',
          gap: '7px',
          flexWrap: 'wrap',
          justifyContent: 'flex-end',
          '@media screen and (max-width: 680px)': { justifyContent: 'flex-start' },
        })}
      >
        <StatusChip tone={dirty ? 'amber' : 'green'}>
          {dirty ? (view ? 'Unsaved edits' : 'Uncommitted draft') : 'Revision exact'}
        </StatusChip>
        {previewDrifted && <StatusChip tone="red">Preview drifted</StatusChip>}
      </div>
    </header>
  )
}

function StepRail({
  step,
  investigationID,
  revisionID,
  serviceRoute,
}: {
  step: WorkbenchStep
  investigationID: string
  revisionID: string
  serviceRoute: Record<string, string>
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <nav
      aria-label="Workbench steps"
      className={css({
        position: 'sticky',
        top: '76px',
        display: 'grid',
        borderTop: `1px solid ${tok.cardBorder}`,
        '@media screen and (max-width: 820px)': {
          position: 'static',
          gridTemplateColumns: 'repeat(4, minmax(110px, 1fr))',
          overflowX: 'auto',
          borderBottom: `1px solid ${tok.cardBorder}`,
        },
        '@media screen and (max-width: 480px)': {
          gridTemplateColumns: 'repeat(4, minmax(0, 1fr))',
          overflowX: 'visible',
        },
      })}
    >
      {steps.map((item) => {
        const current = item.id === step
        const values: Record<string, string> = {
          step: item.id,
          ...serviceRoute,
        }
        if (investigationID && revisionID) {
          values.investigation_id = investigationID
          values.revision_id = revisionID
        }
        return (
          <a
            key={item.id}
            href={href('/workbench', values)}
            aria-current={current ? 'step' : undefined}
            className={css({
              minHeight: '66px',
              boxSizing: 'border-box',
              display: 'grid',
              gridTemplateColumns: '30px minmax(0, 1fr)',
              gap: '8px',
              alignItems: 'center',
              padding: '10px 8px',
              borderBottom: `1px solid ${tok.cardBorder}`,
              borderLeft: current ? `3px solid ${tok.accent}` : '3px solid transparent',
              color: current ? tok.textPrimary : tok.textTertiary,
              backgroundColor: current ? tok.bandBg : 'transparent',
              textDecoration: 'none',
              ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary },
              ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
              '@media screen and (max-width: 820px)': {
                borderLeft: 0,
                borderBottom: current ? `3px solid ${tok.accent}` : '3px solid transparent',
              },
              '@media screen and (max-width: 480px)': {
                gridTemplateColumns: '18px minmax(0, 1fr)',
                gap: '4px',
                paddingLeft: '5px',
                paddingRight: '5px',
              },
            })}
          >
            <span className={css({
              fontFamily: FONTS.MONO,
              fontSize: '11px',
              color: current ? tok.accent : tok.textTertiary,
            })}>
              {item.eyebrow}
            </span>
            <span className={css({ fontSize: '14px', fontWeight: current ? 650 : 500 })}>
              {item.label}
            </span>
          </a>
        )
      })}
    </nav>
  )
}

function WhyEditor({
  plan,
  lockedIdentity,
  onChange,
}: {
  plan: WorkbenchPlan
  lockedIdentity: boolean
  onChange: (plan: WorkbenchPlan) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const updateBrief = (
    field: keyof WorkbenchPlan['brief'],
    value: WorkbenchPlan['brief'][keyof WorkbenchPlan['brief']],
  ) => onChange({ ...plan, brief: { ...plan.brief, [field]: value } })
  const updateRevision = (
    field: keyof WorkbenchPlan['revision'],
    value: string,
  ) => onChange({
    ...plan,
    revision: { ...plan.revision, [field]: value },
  })
  return (
    <section aria-labelledby="workbench-why-heading">
      <SectionHeader
        eyebrow="Human intent"
        title="Why this change?"
        detail="These fields are authored context. Evidence may qualify them later, but phebs does not invent or satisfy the outcome."
      />
      <div className={css({ display: 'grid', gap: '16px' })}>
        {!lockedIdentity && (
          <TextField
            label="Title"
            value={plan.title}
            maxLength={64 << 10}
            onChange={(title) => onChange({ ...plan, title })}
          />
        )}
        <TextAreaField
          label="Problem"
          value={plan.brief.problem}
          maxLength={16 << 10}
          rows={6}
          onChange={(value) => updateBrief('problem', value)}
        />
        <TextAreaField
          label="Desired outcome"
          value={plan.brief.desired_outcome}
          maxLength={16 << 10}
          rows={5}
          onChange={(value) => updateBrief('desired_outcome', value)}
        />
        <ListField
          label="Success criteria"
          hint="One human-owned criterion per line; at least one is required."
          values={plan.brief.success_criteria}
          onChange={(value) => updateBrief('success_criteria', value)}
        />
        <div className={css({
          display: 'grid',
          gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
          gap: '16px',
          '@media screen and (max-width: 650px)': { gridTemplateColumns: '1fr' },
        })}>
          <ListField
            label="Non-goals"
            values={plan.brief.non_goals}
            onChange={(value) => updateBrief('non_goals', value)}
          />
          <ListField
            label="Assumptions"
            values={plan.brief.assumptions}
            onChange={(value) => updateBrief('assumptions', value)}
          />
        </div>
        <ListField
          label="Open questions"
          values={plan.brief.open_questions}
          onChange={(value) => updateBrief('open_questions', value)}
        />
        <TextField
          label="External reference"
          value={plan.brief.external_reference ?? ''}
          maxLength={2048}
          placeholder="Optional inert ticket URL or identifier"
          onChange={(value) => updateBrief('external_reference', value)}
        />
        <details className={css(panelStyle(tok))}>
          <summary className={css(detailsSummaryStyle(tok))}>
            Analysis contract
          </summary>
          <div className={css({
            display: 'grid',
            gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
            gap: '15px',
            padding: '18px',
            '@media screen and (max-width: 680px)': { gridTemplateColumns: '1fr' },
          })}>
            <TextAreaField
              label="Normalized question"
              value={plan.revision.normalized_question}
              rows={3}
              onChange={(value) => updateRevision('normalized_question', value)}
            />
            <TextAreaField
              label="Decision sought"
              value={plan.revision.decision_sought}
              rows={3}
              onChange={(value) => updateRevision('decision_sought', value)}
            />
            <TextField
              label="Snapshot policy"
              value={plan.revision.snapshot_policy}
              onChange={(value) => updateRevision('snapshot_policy', value)}
            />
            <TextField
              label="Build configuration"
              value={plan.revision.build_configuration}
              onChange={(value) => updateRevision('build_configuration', value)}
            />
            <TextField
              label="Enumeration method"
              value={plan.revision.enumeration_method}
              onChange={(value) => updateRevision('enumeration_method', value)}
            />
          </div>
        </details>
      </div>
    </section>
  )
}

function WhatEditor({
  plan,
  storedProposal,
  onChange,
}: {
  plan: WorkbenchPlan
  storedProposal: WorkbenchProposalCommitment | null
  onChange: (plan: WorkbenchPlan) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const sources = plan.brief.what.proposal_sources ?? []
  const proposalRequired = plan.brief.ticket_kind === 'add' ||
    plan.brief.ticket_kind === 'modify'
  const updateWhat = (what: WorkbenchPlan['brief']['what']) =>
    onChange({ ...plan, brief: { ...plan.brief, what } })
  const updateSelection = (
    index: number,
    next: WorkbenchContractSelection,
  ) => {
    const selections = [...plan.brief.what.selections]
    selections[index] = next
    updateWhat({ ...plan.brief.what, selections })
  }
  const updateSource = (index: number, next: WorkbenchProposalSource) => {
    const proposalSources = [...sources]
    proposalSources[index] = next
    updateWhat({ ...plan.brief.what, proposal_sources: proposalSources })
  }
  const setKind = (kind: WorkbenchTicketKind) => {
    const selections = plan.brief.what.selections.map((selection, index) => ({
      ...selection,
      role: roleForMode(kind, index),
    }))
    const what = { ...plan.brief.what, selections }
    if (kind === 'migrate' || kind === 'retire') {
      what.proposal_protocol = undefined
      what.proposal_sources = []
    } else {
      what.proposal_protocol = what.proposal_protocol || 'protobuf'
      if (!what.proposal_sources?.length) {
        what.proposal_sources = [{ path: '', content: '' }]
      }
    }
    onChange({
      ...plan,
      brief: { ...plan.brief, ticket_kind: kind, what },
    })
  }
  const compatibilityRequested =
    plan.capabilities.includes('contract-compatibility')
  return (
    <section aria-labelledby="workbench-what-heading">
      <SectionHeader
        eyebrow="Exact contract scope"
        title="What changes?"
        detail="Selections are complete protocol, repository, lineage, and operation identities. Proposal bytes are preview inputs only and are never repository evidence."
      />
      <div className={css({ display: 'grid', gap: '20px' })}>
        <div className={css({
          display: 'grid',
          gridTemplateColumns: 'minmax(180px, 0.35fr) minmax(0, 1fr)',
          gap: '16px',
          '@media screen and (max-width: 660px)': { gridTemplateColumns: '1fr' },
        })}>
          <label className={css(fieldLabelStyle(tok))}>
            Change mode
            <select
              aria-label="Workbench change mode"
              value={plan.brief.ticket_kind}
              onChange={(event) =>
                setKind(event.currentTarget.value as WorkbenchTicketKind)}
              className={css(controlStyle(tok))}
            >
              <option value="add">Add</option>
              <option value="modify">Modify</option>
              <option value="migrate">Migrate</option>
              <option value="retire">Retire</option>
            </select>
          </label>
          <ListField
            label="Repository universe"
            hint="One currently visible indexed repository per line."
            values={plan.repositories}
            onChange={(repositories) => onChange({ ...plan, repositories })}
          />
        </div>

        <div className={css(panelStyle(tok))}>
          <div className={css(panelHeaderStyle(tok))}>
            <span>
              <span className={css(eyebrowStyle(tok))}>Bound identities</span>
              <h3 className={css({ margin: '4px 0 0', fontSize: '16px' })}>
                Contract selections
              </h3>
            </span>
            <Button
              type="button"
              size={BUTTON_SIZE.mini}
              kind={BUTTON_KIND.secondary}
              disabled={
                plan.brief.what.selections.length >= selectionLimit
              }
              onClick={() => {
                const index = plan.brief.what.selections.length
                updateWhat({
                  ...plan.brief.what,
                  selections: [
                    ...plan.brief.what.selections,
                    emptySelection(roleForMode(plan.brief.ticket_kind, index)),
                  ],
                })
              }}
            >
              Add endpoint
            </Button>
          </div>
          {plan.brief.what.selections.length === 0 ? (
            <HonestEmpty>
              No endpoint is selected. Add requires only proposed source;
              modify, migrate, and retire require exact current/replacement
              identities before preview can be ready.
            </HonestEmpty>
          ) : (
            <div className={css({ display: 'grid' })}>
              {plan.brief.what.selections.map((selection, index) => (
                <SelectionEditor
                  key={`${index}:${selection.role}`}
                  index={index}
                  selection={selection}
                  onChange={(next) => updateSelection(index, next)}
                  onRemove={() => updateWhat({
                    ...plan.brief.what,
                    selections: plan.brief.what.selections.filter(
                      (_value, candidate) => candidate !== index,
                    ),
                  })}
                />
              ))}
            </div>
          )}
        </div>

        {proposalRequired && (
          <div className={css(panelStyle(tok))}>
            <div className={css(panelHeaderStyle(tok))}>
              <span>
                <span className={css(eyebrowStyle(tok))}>Caller-owned bytes</span>
                <h3 className={css({ margin: '4px 0 0', fontSize: '16px' })}>
                  Proposed contract source
                </h3>
              </span>
              <Button
                type="button"
                size={BUTTON_SIZE.mini}
                kind={BUTTON_KIND.secondary}
                disabled={sources.length >= proposalFileLimit}
                onClick={() => updateWhat({
                  ...plan.brief.what,
                  proposal_sources: [...sources, { path: '', content: '' }],
                })}
              >
                Add file
              </Button>
            </div>
            <div className={css({
              display: 'grid',
              gridTemplateColumns: '180px minmax(0, 1fr)',
              gap: '14px',
              padding: '16px 18px',
              borderBottom: `1px solid ${tok.cardBorder}`,
              '@media screen and (max-width: 620px)': { gridTemplateColumns: '1fr' },
            })}>
              <label className={css(fieldLabelStyle(tok))}>
                Proposal protocol
                <select
                  aria-label="Proposal protocol"
                  value={plan.brief.what.proposal_protocol ?? 'protobuf'}
                  onChange={(event) => updateWhat({
                    ...plan.brief.what,
                    proposal_protocol: event.currentTarget.value,
                  })}
                  className={css(controlStyle(tok))}
                >
                  <option value="protobuf">Protobuf</option>
                  <option value="thrift">Thrift</option>
                </select>
              </label>
              <label className={css({
                ...fieldLabelStyle(tok),
                gridTemplateColumns: '18px minmax(0, 1fr)',
                alignItems: 'start',
              })}>
                <input
                  type="checkbox"
                  checked={compatibilityRequested}
                  onChange={(event) => {
                    const capabilities = event.currentTarget.checked
                      ? [...new Set([...plan.capabilities, 'contract-compatibility'])]
                      : plan.capabilities.filter(
                        (value) => value !== 'contract-compatibility',
                      )
                    onChange({ ...plan, capabilities })
                  }}
                />
                <span>
                  Request compatibility capability
                  <span className={css({
                    display: 'block',
                    marginTop: '3px',
                    color: tok.textTertiary,
                    fontSize: '11px',
                    fontWeight: 400,
                    lineHeight: '15px',
                  })}>
                    Unavailable capability or protocol remains explicit in the
                    preview; it is never rendered as a compatible verdict.
                  </span>
                </span>
              </label>
            </div>
            {storedProposal && sources.length === 0 && (
              <StoredProposal value={storedProposal} />
            )}
            {sources.length === 0 && !storedProposal && (
              <HonestEmpty>
                This mode requires proposal source. Add at least one bounded
                file before preview.
              </HonestEmpty>
            )}
            {sources.map((source, index) => (
              <ProposalSourceEditor
                key={index}
                index={index}
                source={source}
                onChange={(next) => updateSource(index, next)}
                onRemove={() => updateWhat({
                  ...plan.brief.what,
                  proposal_sources: sources.filter(
                    (_value, candidate) => candidate !== index,
                  ),
                })}
              />
            ))}
          </div>
        )}
      </div>
    </section>
  )
}

function SelectionEditor({
  index,
  selection,
  onChange,
  onRemove,
}: {
  index: number
  selection: WorkbenchContractSelection
  onChange: (selection: WorkbenchContractSelection) => void
  onRemove: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [discoveryOpen, setDiscoveryOpen] = useState(false)
  const discoveryTrigger = useRef<HTMLButtonElement>(null)
  const update = (
    field: keyof WorkbenchContractSelection,
    value: string,
  ) => onChange({ ...selection, [field]: value })
  return (
    <>
      <fieldset className={css({
        display: 'grid',
        gridTemplateColumns: '130px 130px minmax(180px, 0.8fr) minmax(220px, 1fr) minmax(220px, 1fr) auto',
        gap: '10px',
        alignItems: 'end',
        margin: 0,
        padding: '15px 18px',
        border: 0,
        borderBottom: `1px solid ${tok.innerSep}`,
        '@media screen and (max-width: 1160px)': {
          gridTemplateColumns: '1fr 1fr',
        },
        '@media screen and (max-width: 560px)': {
          gridTemplateColumns: '1fr',
        },
      })}>
        <legend className={css({
          position: 'absolute',
          width: '1px',
          height: '1px',
          overflow: 'hidden',
          clip: 'rect(0 0 0 0)',
        })}>
          Contract selection {index + 1}
        </legend>
        <label className={css(fieldLabelStyle(tok))}>
          Role
          <select
            aria-label={`Selection ${index + 1} role`}
            value={selection.role}
            onChange={(event) => update('role', event.currentTarget.value)}
            className={css(controlStyle(tok))}
          >
            <option value="current">Current</option>
            <option value="replacement">Replacement</option>
            <option value="analogous">Analogous</option>
          </select>
        </label>
        <label className={css(fieldLabelStyle(tok))}>
          Protocol
          <select
            aria-label={`Selection ${index + 1} protocol`}
            value={selection.protocol}
            onChange={(event) => update('protocol', event.currentTarget.value)}
            className={css(controlStyle(tok))}
          >
            <option value="protobuf">Protobuf</option>
            <option value="thrift">Thrift</option>
          </select>
        </label>
        <TextField
          label="Repository"
          ariaLabel={`Selection ${index + 1} repository`}
          value={selection.repository}
          mono
          onChange={(value) => update('repository', value)}
        />
        <TextField
          label="Declaration lineage"
          ariaLabel={`Selection ${index + 1} declaration lineage`}
          value={selection.declaration_lineage}
          mono
          onChange={(value) => update('declaration_lineage', value)}
        />
        <TextField
          label="Canonical operation"
          ariaLabel={`Selection ${index + 1} canonical operation`}
          value={selection.canonical_operation}
          mono
          onChange={(value) => update('canonical_operation', value)}
        />
        <div className={css({
          display: 'grid',
          gap: '7px',
          justifyItems: 'stretch',
        })}>
          <Button
            type="button"
            size={BUTTON_SIZE.mini}
            kind={BUTTON_KIND.secondary}
            aria-haspopup="dialog"
            aria-expanded={discoveryOpen}
            aria-controls={`selection-${index + 1}-endpoint-discovery`}
            aria-label={`Discover endpoint for selection ${index + 1}`}
            ref={discoveryTrigger}
            onClick={() => setDiscoveryOpen((value) => !value)}
          >
            Discover
          </Button>
          <Button
            type="button"
            size={BUTTON_SIZE.mini}
            kind={BUTTON_KIND.tertiary}
            onClick={onRemove}
          >
            Remove
          </Button>
        </div>
      </fieldset>
      {discoveryOpen && (
        <EndpointDiscovery
          id={`selection-${index + 1}-endpoint-discovery`}
          selectionNumber={index + 1}
          triggerRef={discoveryTrigger}
          onClose={() => setDiscoveryOpen(false)}
          onSelect={(item) => {
            onChange({
              ...selection,
              protocol: item.protocol,
              repository: item.repository,
              declaration_lineage: item.declaration_lineage,
              canonical_operation: item.operation ?? '',
            })
            setDiscoveryOpen(false)
          }}
        />
      )}
    </>
  )
}

function EndpointDiscovery({
  id,
  selectionNumber,
  triggerRef,
  onClose,
  onSelect,
}: {
  id: string
  selectionNumber: number
  triggerRef: React.RefObject<HTMLButtonElement | null>
  onClose: () => void
  onSelect: (item: ContractCatalogItem) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const panelRef = useRef<HTMLDivElement>(null)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const [items, setItems] = useState<ContractCatalogItem[]>([])
  const [cursors, setCursors] = useState([''])
  const [pageIndex, setPageIndex] = useState(0)
  const [nextCursor, setNextCursor] = useState('')
  const [loading, setLoading] = useState(true)
  const [failure, setFailure] = useState('')
  const cursor = cursors[pageIndex] ?? ''

  const close = useCallback((returnFocus: boolean) => {
    onClose()
    if (returnFocus) {
      window.setTimeout(() => triggerRef.current?.focus(), 0)
    }
  }, [onClose, triggerRef])

  useEffect(() => {
    headingRef.current?.focus()
    const dismiss = (event: PointerEvent) => {
      const target = event.target
      if (!(target instanceof Node) ||
        panelRef.current?.contains(target) ||
        triggerRef.current?.contains(target)) return
      close(false)
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      close(true)
    }
    document.addEventListener('pointerdown', dismiss)
    document.addEventListener('keydown', escape)
    return () => {
      document.removeEventListener('pointerdown', dismiss)
      document.removeEventListener('keydown', escape)
    }
  }, [close, triggerRef])

  useEffect(() => {
    const controller = new AbortController()
    let active = true
    setLoading(true)
    setFailure('')
    fetchContractCatalog({}, 10, cursor, controller.signal)
      .then((page) => {
        if (!active) return
        setItems(page.items.filter(
          (item) => item.kind === 'operation' && Boolean(item.operation),
        ))
        setNextCursor(page.pagination.next_cursor ?? '')
      })
      .catch((cause) => {
        if (!active || isAbortError(cause)) return
        setItems([])
        setNextCursor('')
        setFailure(cause instanceof Error ? cause.message : String(cause))
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [cursor])

  return (
    <div
      id={id}
      ref={panelRef}
      role="dialog"
      aria-modal="false"
      aria-labelledby={`${id}-heading`}
      aria-describedby={`${id}-help`}
      className={css({
        display: 'grid',
        gap: '12px',
        margin: '0 18px 16px',
        padding: '16px',
        border: `1px solid ${tok.cardBorder}`,
        borderTop: 0,
        borderRadius: '0 0 10px 10px',
        backgroundColor: tok.bandBg,
        minWidth: 0,
      })}
    >
      <div className={css({
        display: 'grid',
        gridTemplateColumns: 'minmax(0, 1fr) auto',
        gap: '12px',
        alignItems: 'start',
      })}>
        <div>
          <div className={css(eyebrowStyle(tok))}>Contract Atlas · read only</div>
          <h4
            id={`${id}-heading`}
            ref={headingRef}
            tabIndex={-1}
            className={css({ margin: '4px 0 0', fontSize: '15px' })}
          >
            Choose discovered endpoint for selection {selectionNumber}
          </h4>
        </div>
        <Button
          type="button"
          size={BUTTON_SIZE.mini}
          kind={BUTTON_KIND.tertiary}
          onClick={() => close(true)}
        >
          Close endpoint discovery
        </Button>
      </div>
      <p id={`${id}-help`} className={css({
        margin: 0,
        color: tok.textTertiary,
        fontSize: '11px',
        lineHeight: '17px',
      })}>
        Selecting a row copies its complete protocol, repository, declaration
        lineage, and canonical operation identity. Shared names stay separate;
        choosing a name never upgrades name-only evidence to a resolved caller.
      </p>
      {loading && (
        <div role="status" aria-live="polite" className={css({
          display: 'flex',
          alignItems: 'center',
          gap: '9px',
          color: tok.textSecondary,
          fontSize: '12px',
        })}>
          <Spinner $size="small" />
          Loading one bounded Atlas page…
        </div>
      )}
      {failure && (
        <Notification kind={NOTIFICATION_KIND.negative} closeable={false}>
          Endpoint discovery failed. No identity was changed. {failure}
        </Notification>
      )}
      {!loading && !failure && items.length === 0 && (
        <HonestEmpty>
          This Atlas page contains no operation rows. That does not establish
          that no endpoints exist; use the bounded page controls.
        </HonestEmpty>
      )}
      {!loading && !failure && items.length > 0 && (
        <ul className={css({
          display: 'grid',
          gap: '8px',
          margin: 0,
          padding: 0,
          listStyle: 'none',
          minWidth: 0,
        })}>
          {items.map((item) => (
            <li
              key={[
                item.protocol,
                item.repository,
                item.declaration_lineage,
                item.operation,
              ].join('\u0000')}
              className={css({
                display: 'grid',
                gridTemplateColumns: 'minmax(0, 1fr) auto',
                gap: '12px',
                alignItems: 'center',
                padding: '10px 11px',
                border: `1px solid ${tok.innerSep}`,
                borderRadius: '7px',
                minWidth: 0,
                '@media screen and (max-width: 560px)': {
                  gridTemplateColumns: '1fr',
                },
              })}
            >
              <span className={css({ minWidth: 0 })}>
                <strong className={css({
                  display: 'block',
                  overflowWrap: 'anywhere',
                  fontFamily: FONTS.MONO,
                  fontSize: '11px',
                })}>
                  {item.operation}
                </strong>
                <span className={css({
                  display: 'block',
                  marginTop: '3px',
                  color: tok.textTertiary,
                  overflowWrap: 'anywhere',
                  fontSize: '11px',
                  lineHeight: '15px',
                })}>
                  {item.protocol} · {item.repository} · {item.declaration_lineage}
                </span>
              </span>
              <Button
                type="button"
                size={BUTTON_SIZE.mini}
                kind={BUTTON_KIND.secondary}
                aria-label={[
                  `Use endpoint ${item.operation}`,
                  `from ${item.repository}`,
                  `(${item.protocol}; lineage ${item.declaration_lineage})`,
                ].join(' ')}
                onClick={() => {
                  onSelect(item)
                  window.setTimeout(() => triggerRef.current?.focus(), 0)
                }}
              >
                Use endpoint
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className={css({
        display: 'flex',
        gap: '8px',
        justifyContent: 'space-between',
        alignItems: 'center',
        flexWrap: 'wrap',
      })}>
        <span className={css({
          color: tok.textTertiary,
          fontFamily: FONTS.MONO,
          fontSize: '11px',
        })}>
          Atlas page {pageIndex + 1}; prior result rows are not retained.
        </span>
        <span className={css({ display: 'flex', gap: '8px' })}>
          <Button
            type="button"
            size={BUTTON_SIZE.mini}
            kind={BUTTON_KIND.tertiary}
            disabled={loading || pageIndex === 0}
            onClick={() => setPageIndex((value) => Math.max(0, value - 1))}
          >
            Previous endpoints
          </Button>
          <Button
            type="button"
            size={BUTTON_SIZE.mini}
            kind={BUTTON_KIND.tertiary}
            disabled={loading || !nextCursor}
            onClick={() => {
              if (!nextCursor) return
              setCursors((current) => [
                ...current.slice(0, pageIndex + 1),
                nextCursor,
              ])
              setPageIndex((value) => value + 1)
            }}
          >
            Next endpoints
          </Button>
        </span>
      </div>
    </div>
  )
}

function ProposalSourceEditor({
  index,
  source,
  onChange,
  onRemove,
}: {
  index: number
  source: WorkbenchProposalSource
  onChange: (source: WorkbenchProposalSource) => void
  onRemove: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const sourceBytes = new TextEncoder().encode(source.content).length
  return (
    <fieldset className={css({
      display: 'grid',
      gap: '10px',
      margin: 0,
      padding: '16px 18px',
      border: 0,
      borderBottom: `1px solid ${tok.innerSep}`,
    })}>
      <legend className={css({
        padding: '0 4px',
        color: tok.textSecondary,
        fontSize: '11px',
        fontWeight: 650,
      })}>
        Proposal file {index + 1}
      </legend>
      <div className={css({
        display: 'grid',
        gridTemplateColumns: 'minmax(0, 1fr) auto',
        gap: '10px',
        alignItems: 'end',
      })}>
        <TextField
          label="Path"
          ariaLabel={`Proposal file ${index + 1} path`}
          value={source.path}
          maxLength={4096}
          mono
          placeholder="proto/service.proto"
          onChange={(path) => onChange({ ...source, path })}
        />
        <Button
          type="button"
          size={BUTTON_SIZE.mini}
          kind={BUTTON_KIND.tertiary}
          onClick={onRemove}
        >
          Remove
        </Button>
      </div>
      <label className={css(fieldLabelStyle(tok))}>
        Source
        <textarea
          aria-label={`Proposal file ${index + 1} source`}
          aria-invalid={sourceBytes > proposalFileBytes}
          value={source.content}
          rows={10}
          spellCheck={false}
          onChange={(event) =>
            onChange({ ...source, content: event.currentTarget.value })}
          className={css({
            ...textareaStyle(tok),
            fontFamily: FONTS.MONO,
            fontSize: '12px',
          })}
        />
        <span className={css(fieldHintStyle(tok))}>
          {sourceBytes.toLocaleString()} / {proposalFileBytes.toLocaleString()} bytes
        </span>
        {sourceBytes > proposalFileBytes && (
          <span role="alert" className={css({
            color: tok.status.conflict.text,
            fontSize: '11px',
            lineHeight: '17px',
          })}>
            This file exceeds the 4 MiB UTF-8 limit. It will be refused before
            preview; nothing has been sent or written.
          </span>
        )}
      </label>
    </fieldset>
  )
}

function StoredProposal({
  value,
}: {
  value: WorkbenchProposalCommitment
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({
      display: 'grid',
      gap: '8px',
      padding: '14px 18px',
      borderBottom: `1px solid ${tok.innerSep}`,
      backgroundColor: tok.bandBg,
    })}>
      <div className={css({ color: tok.textSecondary, fontSize: '11px', fontWeight: 650 })}>
        Retained proposal commitment · source bytes are not stored
      </div>
      <div className={css({ display: 'flex', gap: '7px', flexWrap: 'wrap' })}>
        <StatusChip tone="neutral">{value.protocol}</StatusChip>
        <StatusChip tone="neutral">{value.files.length} files</StatusChip>
        <StatusChip tone="neutral">{shortDigest(value.source_set_digest)}</StatusChip>
      </div>
      {value.files.map((file) => (
        <div
          key={file.path}
          className={css({
            display: 'flex',
            justifyContent: 'space-between',
            gap: '12px',
            color: tok.textTertiary,
            fontFamily: FONTS.MONO,
            fontSize: '11px',
          })}
        >
          <span>{file.path}</span>
          <span>{file.size_bytes.toLocaleString()} B · {shortDigest(file.content_hash)}</span>
        </div>
      ))}
    </div>
  )
}

function PreviewDock({
  plan,
  preview,
  previewDrifted,
  previewing,
  committing,
  failure,
  onPreview,
  onCommit,
}: {
  plan: WorkbenchPlan
  preview: WorkbenchPreview | null
  previewDrifted: boolean
  previewing: boolean
  committing: boolean
  failure: PageFailure | null
  onPreview: () => void
  onCommit: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const canCommit = preview?.ready === true &&
    Boolean(preview.preview_digest) && !previewDrifted
  return (
    <aside
      aria-labelledby="workbench-preview-heading"
      className={css({
        marginTop: '28px',
        border: `1px solid ${tok.cardBorder}`,
        backgroundColor: tok.bandBg,
      })}
    >
      <div className={css({
        display: 'flex',
        justifyContent: 'space-between',
        gap: '18px',
        alignItems: 'center',
        padding: '15px 18px',
        borderBottom: `1px solid ${tok.cardBorder}`,
        '@media screen and (max-width: 620px)': {
          alignItems: 'stretch',
          flexDirection: 'column',
        },
      })}>
        <div>
          <div className={css(eyebrowStyle(tok))}>Explicit boundary</div>
          <h2 id="workbench-preview-heading" className={css({
            margin: '4px 0 0',
            fontSize: '16px',
            lineHeight: '22px',
          })}>
            Preview, then commit
          </h2>
        </div>
        <div className={css({ display: 'flex', gap: '8px', flexWrap: 'wrap' })}>
          <Button
            type="button"
            size={BUTTON_SIZE.compact}
            kind={BUTTON_KIND.secondary}
            isLoading={previewing}
            onClick={onPreview}
          >
            {previewDrifted ? 'Refresh preview' : 'Preview revision'}
          </Button>
          <Button
            type="button"
            size={BUTTON_SIZE.compact}
            disabled={!canCommit}
            isLoading={committing}
            onClick={onCommit}
          >
            {plan.investigation_id ? 'Append revision' : 'Create Workbench'}
          </Button>
        </div>
      </div>

      <div aria-live="polite" className={css({ padding: '16px 18px' })}>
        {failure && <InlineFailure failure={failure} />}
        {!failure && !preview && (
          <p className={css({ margin: 0, color: tok.textTertiary, fontSize: '11px', lineHeight: '17px' })}>
            No preview has run. Opening steps and editing fields never invokes
            this operation automatically.
          </p>
        )}
        {preview && (
          <div className={css({ display: 'grid', gap: '14px' })}>
            <div className={css({ display: 'flex', gap: '7px', flexWrap: 'wrap' })}>
              <StatusChip tone={preview.ready && !previewDrifted ? 'green' : 'amber'}>
                {previewDrifted ? 'Preview expired after edits' : preview.ready ? 'Ready' : 'Blocked'}
              </StatusChip>
              <StatusChip tone="neutral">
                revision {preview.next_revision_sequence}
              </StatusChip>
              <StatusChip tone="neutral">
                {preview.estimate.analysis_units} bounded units
              </StatusChip>
              {preview.preview_digest && (
                <StatusChip tone="neutral">{shortDigest(preview.preview_digest)}</StatusChip>
              )}
            </div>
            {preview.blockers.length > 0 && (
              <div>
                <h3 className={css(minorHeadingStyle(tok))}>Preview blockers</h3>
                <ul className={css({ margin: '7px 0 0', paddingLeft: '20px', color: tok.textSecondary, fontSize: '11px', lineHeight: '18px' })}>
                  {preview.blockers.map((blocker) => (
                    <li key={`${blocker.code}:${blocker.input ?? ''}`}>
                      <Mono>{blocker.code}</Mono>
                      {blocker.input ? ` · ${blocker.input}` : ''}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            <div className={css({
              display: 'grid',
              gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
              gap: '10px',
              '@media screen and (max-width: 700px)': {
                gridTemplateColumns: '1fr',
              },
            })}>
              <PreviewMetric label="Repositories" value={preview.estimate.repository_count} />
              <PreviewMetric label="Exact endpoints" value={preview.estimate.endpoint_count} />
              <PreviewMetric label="Proposal bytes" value={preview.estimate.proposal_bytes} />
            </div>
            <div className={css({
              paddingTop: '12px',
              borderTop: `1px solid ${tok.cardBorder}`,
            })}>
              <h3 className={css(minorHeadingStyle(tok))}>Compatibility</h3>
              <div className={css({ display: 'flex', gap: '7px', flexWrap: 'wrap', marginTop: '7px' })}>
                <StatusChip tone={preview.compatibility.status === 'available' ? 'green' : 'amber'}>
                  {preview.compatibility.status}
                </StatusChip>
                {preview.compatibility.protocol && (
                  <StatusChip tone="neutral">{preview.compatibility.protocol}</StatusChip>
                )}
                {preview.compatibility.reason && (
                  <StatusChip tone="neutral">
                    {humanize(preview.compatibility.reason)}
                  </StatusChip>
                )}
              </div>
              {preview.compatibility.status === 'unavailable' && (
                <p className={css({ margin: '7px 0 0', color: tok.textTertiary, fontSize: '11px', lineHeight: '16px' })}>
                  Unavailable is not a compatible verdict. The selected
                  protocol, capability, or current endpoint does not support
                  this preview.
                </p>
              )}
            </div>
          </div>
        )}
      </div>
    </aside>
  )
}

function PreviewMetric({ label, value }: { label: string; value: number }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({
      padding: '11px 12px',
      border: `1px solid ${tok.cardBorder}`,
      backgroundColor: tok.pageBg,
    })}>
      <div className={css(eyebrowStyle(tok))}>{label}</div>
      <div className={css({ marginTop: '4px', fontSize: '20px', fontWeight: 670 })}>
        {value.toLocaleString()}
      </div>
    </div>
  )
}

function FailurePage({
  failure,
  view,
  onRetry,
}: {
  failure: PageFailure
  view: WorkbenchView | null
  onRetry: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section className={css({
      maxWidth: '760px',
      margin: '48px auto',
      borderTop: `3px solid ${tok.status.conflict.solid}`,
      padding: '24px 0',
    })}>
      <div className={css(eyebrowStyle(tok))}>
        Workbench unavailable{failure.status ? ` · HTTP ${failure.status}` : ''}
      </div>
      <h1 className={css({ margin: '8px 0 0', fontSize: '30px', letterSpacing: '-0.025em' })}>
        {failure.title}
      </h1>
      <p className={css({ margin: '12px 0 0', color: tok.textSecondary, fontSize: '13px', lineHeight: '21px' })}>
        {failure.detail}
      </p>
      <div className={css({ display: 'flex', gap: '9px', flexWrap: 'wrap', marginTop: '20px' })}>
        <Button type="button" size={BUTTON_SIZE.compact} kind={BUTTON_KIND.secondary} onClick={onRetry}>
          Retry exact link
        </Button>
        {view && (
          <a
            href={href('/workbench', {
              investigation_id: view.investigation.investigation_id,
              revision_id: view.revision.revision_id,
              step: 'what',
            })}
            className={css(anchorButtonStyle(tok))}
          >
            Open current revision explicitly
          </a>
        )}
        <a href="#/workbench" className={css(anchorButtonStyle(tok))}>
          Workbench home
        </a>
      </div>
    </section>
  )
}

function InlineFailure({ failure }: { failure: PageFailure }) {
  return (
    <Notification
      kind={NOTIFICATION_KIND.negative}
      overrides={{ Body: { style: { width: 'auto', margin: 0 } } }}
    >
      <strong>{failure.title}.</strong> {failure.detail}
    </Notification>
  )
}

function SectionHeader({
  eyebrow,
  title,
  detail,
}: {
  eyebrow: string
  title: string
  detail: string
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const id = `workbench-${title.split(' ')[0].toLowerCase()}-heading`
  return (
    <header className={css({ marginBottom: '20px' })}>
      <div className={css(eyebrowStyle(tok))}>{eyebrow}</div>
      <h2 id={id} className={css({
        margin: '5px 0 0',
        fontSize: '25px',
        lineHeight: '32px',
        letterSpacing: '-0.025em',
      })}>
        {title}
      </h2>
      <p className={css({
        maxWidth: '780px',
        margin: '7px 0 0',
        color: tok.textTertiary,
        fontSize: '12px',
        lineHeight: '19px',
      })}>
        {detail}
      </p>
    </header>
  )
}

function TextField({
  label,
  ariaLabel,
  value,
  placeholder,
  maxLength = 64 << 10,
  mono = false,
  onChange,
}: {
  label: string
  ariaLabel?: string
  value: string
  placeholder?: string
  maxLength?: number
  mono?: boolean
  onChange: (value: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css(fieldLabelStyle(tok))}>
      {label}
      <input
        aria-label={ariaLabel ?? label}
        value={value}
        maxLength={maxLength}
        placeholder={placeholder}
        onChange={(event) => onChange(event.currentTarget.value)}
        className={css({
          ...controlStyle(tok),
          fontFamily: mono ? FONTS.MONO : FONTS.SANS,
        })}
      />
    </label>
  )
}

function TextAreaField({
  label,
  value,
  rows,
  maxLength = 64 << 10,
  onChange,
}: {
  label: string
  value: string
  rows: number
  maxLength?: number
  onChange: (value: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css(fieldLabelStyle(tok))}>
      {label}
      <textarea
        aria-label={label}
        value={value}
        rows={rows}
        maxLength={maxLength}
        onChange={(event) => onChange(event.currentTarget.value)}
        className={css(textareaStyle(tok))}
      />
    </label>
  )
}

function ListField({
  label,
  hint,
  values,
  onChange,
}: {
  label: string
  hint?: string
  values: string[]
  onChange: (values: string[]) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css(fieldLabelStyle(tok))}>
      {label}
      <textarea
        aria-label={label}
        value={values.join('\n')}
        rows={4}
        onChange={(event) => onChange(event.currentTarget.value.split('\n'))}
        className={css(textareaStyle(tok))}
      />
      {hint && <span className={css(fieldHintStyle(tok))}>{hint}</span>}
    </label>
  )
}

function HonestEmpty({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({
      padding: '18px',
      color: tok.textTertiary,
      fontSize: '11px',
      lineHeight: '18px',
    })}>
      {children}
    </div>
  )
}

function Mono({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  return <span className={css({ fontFamily: FONTS.MONO })}>{children}</span>
}

function planFromTicket(
  title: string,
  ticketText: string,
  kind: WorkbenchTicketKind,
): WorkbenchPlan {
  const problem = ticketText.trim()
  const proposalRequired = kind === 'add' || kind === 'modify'
  return {
    referent: `ticket:${title.trim()}`,
    claim_family: 'change-workbench',
    title: title.trim(),
    revision: {
      normalized_question: problem,
      decision_sought: 'Approve the scoped contract change.',
      snapshot_policy: 'exact indexed commits',
      build_configuration: 'repository default',
      enumeration_method: 'selected contracts and bounded evidence',
    },
    brief: {
      ticket_kind: kind,
      problem,
      desired_outcome: '',
      success_criteria: [''],
      non_goals: [],
      assumptions: [],
      open_questions: [],
      what: {
        selections: [],
        proposal_protocol: proposalRequired ? 'protobuf' : undefined,
        proposal_sources: proposalRequired
          ? [{ path: '', content: '' }]
          : [],
      },
    },
    repositories: [],
    capabilities: ['contract-atlas'],
  }
}

function planFromAtlasRoute(params: URLSearchParams): WorkbenchPlan | null {
  if (params.get('source') !== 'atlas') return null
  const protocol = params.get('protocol') ?? ''
  const repository = params.get('repository') ?? ''
  const lineage = params.get('lineage') ?? ''
  const operation = params.get('operation') ?? ''
  if (!protocol || !repository || !lineage || !operation) return null
  return {
    referent: `${protocol}:${operation}`,
    claim_family: 'change-workbench',
    title: `Change ${operation}`,
    revision: {
      normalized_question: `What must change for ${operation}?`,
      decision_sought: 'Approve the scoped contract change.',
      snapshot_policy: 'exact indexed commits',
      build_configuration: 'repository default',
      enumeration_method: 'exact Atlas selection and bounded evidence',
    },
    brief: {
      ticket_kind: 'modify',
      problem: '',
      desired_outcome: '',
      success_criteria: [''],
      non_goals: [],
      assumptions: [],
      open_questions: [],
      what: {
        selections: [{
          role: 'current',
          protocol,
          repository,
          declaration_lineage: lineage,
          canonical_operation: operation,
        }],
        proposal_protocol: protocol,
        proposal_sources: [{ path: '', content: '' }],
      },
    },
    repositories: [repository],
    capabilities: ['contract-atlas'],
  }
}

function planFromView(view: WorkbenchView): WorkbenchPlan {
  let repositories: string[] = []
  try {
    const decoded = JSON.parse(view.revision.declared_universe) as {
      repositories?: { name?: string }[]
    }
    repositories = (decoded.repositories ?? [])
      .map((repository) => repository.name ?? '')
      .filter(Boolean)
  } catch {
    repositories = []
  }
  return {
    investigation_id: view.investigation.investigation_id,
    expected_revision_id: view.revision.revision_id,
    referent: view.investigation.referent,
    claim_family: view.investigation.claim_family,
    title: view.investigation.title,
    revision: {
      normalized_question: view.revision.normalized_question,
      decision_sought: view.revision.decision_sought,
      snapshot_policy: view.revision.snapshot_policy,
      build_configuration: view.revision.build_configuration,
      enumeration_method: view.revision.enumeration_method,
    },
    brief: {
      ticket_kind: view.brief.ticket_kind,
      problem: view.brief.problem,
      desired_outcome: view.brief.desired_outcome,
      success_criteria: [...view.brief.success_criteria],
      non_goals: [...view.brief.non_goals],
      assumptions: [...view.brief.assumptions],
      open_questions: [...view.brief.open_questions],
      external_reference: view.brief.external_reference,
      what: {
        selections: view.brief.what.selections.map((selection) => ({
          ...selection,
        })),
        proposal_protocol: view.brief.what.proposal?.protocol,
        proposal_sources: [],
      },
    },
    repositories,
    capabilities: view.revision.pack_selection
      .split(',')
      .map((value) => value.trim())
      .filter(Boolean),
  }
}

function canonicalPlan(plan: WorkbenchPlan): WorkbenchPlan {
  const lines = (values: string[], preserveOne = false) => {
    const normalized = values.map((value) => value.trim()).filter(Boolean)
    return normalized.length || !preserveOne ? normalized : ['']
  }
  return {
    ...plan,
    investigation_id: plan.investigation_id?.trim() || undefined,
    expected_revision_id: plan.expected_revision_id?.trim() || undefined,
    referent: plan.referent.trim(),
    claim_family: plan.claim_family.trim(),
    title: plan.title.trim(),
    revision: {
      normalized_question: plan.revision.normalized_question.trim(),
      decision_sought: plan.revision.decision_sought.trim(),
      snapshot_policy: plan.revision.snapshot_policy.trim(),
      build_configuration: plan.revision.build_configuration.trim(),
      enumeration_method: plan.revision.enumeration_method.trim(),
    },
    brief: {
      ...plan.brief,
      problem: plan.brief.problem.trim(),
      desired_outcome: plan.brief.desired_outcome.trim(),
      success_criteria: lines(plan.brief.success_criteria, true),
      non_goals: lines(plan.brief.non_goals),
      assumptions: lines(plan.brief.assumptions),
      open_questions: lines(plan.brief.open_questions),
      external_reference: plan.brief.external_reference?.trim() || undefined,
      what: {
        selections: plan.brief.what.selections.map((selection) => ({
          role: selection.role,
          protocol: selection.protocol.trim(),
          repository: selection.repository.trim(),
          declaration_lineage: selection.declaration_lineage.trim(),
          canonical_operation: selection.canonical_operation.trim(),
        })),
        proposal_protocol:
          plan.brief.what.proposal_protocol?.trim() || undefined,
        proposal_sources: (plan.brief.what.proposal_sources ?? []).map(
          (source) => ({ path: source.path.trim(), content: source.content }),
        ),
      },
    },
    repositories: lines(plan.repositories),
    capabilities: [...new Set(lines(plan.capabilities))].sort(),
  }
}

function proposalLimitFailure(plan: WorkbenchPlan): string {
  const sources = plan.brief.what.proposal_sources ?? []
  if (sources.length > proposalFileLimit) {
    return `Proposal contains ${sources.length} files; the reviewed limit is ${proposalFileLimit}. Nothing was sent or written.`
  }
  let total = 0
  for (const [index, source] of sources.entries()) {
    const size = new TextEncoder().encode(source.content).length
    if (size > proposalFileBytes) {
      return `Proposal file ${index + 1} is ${size.toLocaleString()} bytes; the per-file limit is ${proposalFileBytes.toLocaleString()}. Nothing was sent or written.`
    }
    total += size
    if (total > proposalAggregateBytes) {
      return `Proposal sources total ${total.toLocaleString()} bytes; the aggregate limit is ${proposalAggregateBytes.toLocaleString()}. Nothing was sent or written.`
    }
  }
  return ''
}

function planFingerprint(plan: WorkbenchPlan): string {
  return JSON.stringify(plan)
}

function roleForMode(
  kind: WorkbenchTicketKind,
  index: number,
): WorkbenchSelectionRole {
  if (kind === 'add') return 'analogous'
  if (kind === 'migrate' && index === 1) return 'replacement'
  return index === 0 ? 'current' : 'analogous'
}

function emptySelection(
  role: WorkbenchSelectionRole,
): WorkbenchContractSelection {
  return {
    role,
    protocol: 'protobuf',
    repository: '',
    declaration_lineage: '',
    canonical_operation: '',
  }
}

function workbenchFailure(cause: unknown, action: string): PageFailure {
  if (cause instanceof WorkbenchAPIError) {
    if (cause.status === 404) {
      return {
        title: 'Workbench unavailable or permission changed',
        detail: 'The requested Investigation is unknown or no longer authorized. No repository or Investigation identity was disclosed.',
        status: cause.status,
      }
    }
    if (cause.status === 409) {
      return {
        title: 'Workbench revision conflict',
        detail: 'The preview or expected revision changed. Refresh the exact revision and run a new preview before retrying.',
        status: cause.status,
      }
    }
    if (cause.status === 413) {
      return {
        title: 'Source limit refusal',
        detail: 'The server refused the bounded input. Reduce the proposal source set and preview again; nothing was written.',
        status: cause.status,
      }
    }
    if (cause.status === 422) {
      return {
        title: 'Workbench input rejected',
        detail: cause.message,
        status: cause.status,
      }
    }
    return {
      title: `Workbench ${action} failed`,
      detail: cause.message,
      status: cause.status,
    }
  }
  return {
    title: `Workbench ${action} failed`,
    detail: cause instanceof Error ? cause.message : String(cause),
  }
}

function isWorkbenchStep(value: string | null): value is WorkbenchStep {
  return value === 'why' || value === 'what' ||
    value === 'where' || value === 'how'
}

function humanize(value: string): string {
  return value.replaceAll('_', ' ').replace(/\b\w/g, (letter) =>
    letter.toUpperCase())
}

function shortDigest(value: string): string {
  return value.length > 20 ? `${value.slice(0, 17)}…` : value
}

function eyebrowStyle(tok: PhebsTokens) {
  return {
    color: tok.textTertiary,
    fontFamily: FONTS.MONO,
    fontSize: '11px',
    lineHeight: '14px',
    letterSpacing: '0.08em',
    textTransform: 'uppercase' as const,
  }
}

function sectionHeadingStyle() {
  return {
    margin: '5px 0 0',
    fontSize: '20px',
    lineHeight: '27px',
    letterSpacing: '-0.02em',
  }
}

function fieldLabelStyle(tok: PhebsTokens) {
  return {
    display: 'grid',
    gap: '6px',
    color: tok.textSecondary,
    fontSize: '11px',
    lineHeight: '16px',
    fontWeight: 620,
  }
}

function fieldHintStyle(tok: PhebsTokens) {
  return {
    color: tok.textTertiary,
    fontSize: '11px',
    lineHeight: '14px',
    fontWeight: 400,
  }
}

function controlStyle(tok: PhebsTokens) {
  return {
    width: '100%',
    height: '38px',
    boxSizing: 'border-box' as const,
    padding: '0 10px',
    border: `1px solid ${tok.cardBorder}`,
    borderRadius: '0px',
    backgroundColor: tok.pageBg,
    color: tok.textPrimary,
    fontFamily: FONTS.SANS,
    fontSize: '12px',
    ':focus-visible': {
      outline: `2px solid ${tok.accent}`,
      outlineOffset: '1px',
    },
  }
}

function textareaStyle(tok: PhebsTokens) {
  return {
    width: '100%',
    boxSizing: 'border-box' as const,
    resize: 'vertical' as const,
    padding: '10px',
    border: `1px solid ${tok.cardBorder}`,
    borderRadius: '0px',
    backgroundColor: tok.pageBg,
    color: tok.textPrimary,
    fontFamily: FONTS.SANS,
    fontSize: '12px',
    lineHeight: '19px',
    ':focus-visible': {
      outline: `2px solid ${tok.accent}`,
      outlineOffset: '1px',
    },
  }
}

function panelStyle(tok: PhebsTokens) {
  return {
    border: `1px solid ${tok.cardBorder}`,
    backgroundColor: tok.pageBg,
  }
}

function panelHeaderStyle(tok: PhebsTokens) {
  return {
    minHeight: '58px',
    boxSizing: 'border-box' as const,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: '14px',
    padding: '11px 18px',
    borderBottom: `1px solid ${tok.cardBorder}`,
    backgroundColor: tok.bandBg,
  }
}

function detailsSummaryStyle(tok: PhebsTokens) {
  return {
    padding: '14px 18px',
    color: tok.textSecondary,
    fontSize: '12px',
    fontWeight: 650,
    cursor: 'pointer',
    ':hover': { backgroundColor: tok.hoverFill },
    ':focus-visible': {
      outline: `2px solid ${tok.accent}`,
      outlineOffset: '-2px',
    },
  }
}

function minorHeadingStyle(tok: PhebsTokens) {
  return {
    margin: '0px',
    color: tok.textSecondary,
    fontSize: '11px',
    lineHeight: '15px',
    letterSpacing: '0.06em',
    textTransform: 'uppercase' as const,
  }
}

function anchorButtonStyle(tok: PhebsTokens) {
  return {
    minHeight: '32px',
    boxSizing: 'border-box' as const,
    display: 'inline-flex',
    alignItems: 'center',
    padding: '0 12px',
    border: `1px solid ${tok.cardBorder}`,
    color: tok.textPrimary,
    backgroundColor: tok.pageBg,
    fontSize: '12px',
    textDecoration: 'none',
    ':hover': { backgroundColor: tok.hoverFill },
    ':focus-visible': {
      outline: `2px solid ${tok.accent}`,
      outlineOffset: '1px',
    },
  }
}
