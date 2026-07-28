import { useEffect, useMemo, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND, SIZE as BUTTON_SIZE } from 'baseui/button'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { Spinner } from 'baseui/spinner'
import {
  fetchWorkbenchChecklist,
  fetchWorkbenchImpact,
  fetchWorkbenchImplementation,
  recordWorkbenchDisposition,
  WorkbenchAPIError,
  type WorkbenchChecklistEntry,
  type WorkbenchChecklistEvidenceInput,
  type WorkbenchChecklistPage,
  type WorkbenchDispositionCategory,
  type WorkbenchImpactFilters,
  type WorkbenchImpactPage,
  type WorkbenchImplementationAnchor,
  type WorkbenchImplementationPage,
} from '../api'
import { SectionHelp } from '../components/SectionHelp'
import { href } from '../router'
import { FONTS, usePhebsTokens, type PhebsTokens } from '../theme'
import { isAbortError } from '../util'
import type { WorkbenchEvidenceInput } from './workbenchEvidenceState'

const evidencePageSize = 25
const anchorLimit = 32
interface EvidenceStepProps {
  available: boolean
  investigationID: string
  revisionID: string
  evidence: WorkbenchEvidenceInput
  onEvidenceChange: (value: WorkbenchEvidenceInput) => void
}

interface EvidenceFailure {
  title: string
  detail: string
  status?: number
}

export function WorkbenchWhereStep({
  available,
  investigationID,
  revisionID,
  evidence,
  onEvidenceChange,
}: EvidenceStepProps) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [draftFilters, setDraftFilters] = useState(evidence.filters)
  const [draftCompatibilityRun, setDraftCompatibilityRun] =
    useState(evidence.compatibilityRun)
  const [page, setPage] = useState<WorkbenchImpactPage | null>(null)
  const [cursorHistory, setCursorHistory] = useState([''])
  const [pageIndex, setPageIndex] = useState(0)
  const [loading, setLoading] = useState(false)
  const [failure, setFailure] = useState<EvidenceFailure | null>(null)
  const [reload, setReload] = useState(0)
  const queryKey = JSON.stringify({
    investigationID,
    revisionID,
    compatibilityRun: evidence.compatibilityRun,
    filters: evidence.filters,
  })

  useEffect(() => {
    setDraftFilters(evidence.filters)
    setDraftCompatibilityRun(evidence.compatibilityRun)
  }, [evidence.compatibilityRun, evidence.filters])

  useEffect(() => {
    setCursorHistory([''])
    setPageIndex(0)
  }, [queryKey])

  const cursor = cursorHistory[pageIndex] ?? ''
  useEffect(() => {
    if (!available || !investigationID || !revisionID) return
    const controller = new AbortController()
    let active = true
    setLoading(true)
    setFailure(null)
    fetchWorkbenchImpact(
      investigationID,
      revisionID,
      {
        compatibilityRun: evidence.compatibilityRun || undefined,
        filters: evidence.filters,
        pageSize: evidencePageSize,
        cursor: cursor || undefined,
      },
      controller.signal,
    )
      .then((value) => {
        if (active) setPage(value)
      })
      .catch((cause) => {
        if (active && !isAbortError(cause)) {
          setPage(null)
          setFailure(evidenceFailure(cause, 'load impact evidence'))
        }
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [
    available,
    cursor,
    evidence.compatibilityRun,
    evidence.filters,
    investigationID,
    queryKey,
    reload,
    revisionID,
  ])

  if (!available) {
    return <EvidenceUnavailable step="Where" />
  }
  if (!investigationID || !revisionID) {
    return <CommitFirst step="Where" />
  }

  const applyFilters = () => {
    onEvidenceChange({
      ...evidence,
      compatibilityRun: draftCompatibilityRun.trim(),
      filters: canonicalFilters(draftFilters),
    })
    setReload((value) => value + 1)
  }
  const next = () => {
    const nextCursor = page?.pagination.next_cursor
    if (!nextCursor) return
    setCursorHistory((current) => [
      ...current.slice(0, pageIndex + 1),
      nextCursor,
    ])
    setPageIndex((value) => value + 1)
  }

  return (
    <section aria-labelledby="workbench-where-heading">
      <EvidenceHeading
        eyebrow="Bounded impact inventory"
        id="workbench-where-heading"
        title="Where is impact visible?"
        detail="Every row remains in its source evidence class. Empty or exhausted pages do not establish absence, completeness, migration completion, or retirement safety."
      />

      <details className={css(panelStyle(tok))}>
        <summary className={css(summaryStyle(tok))}>
          Evidence filters · exact revision
        </summary>
        <div className={css({
          display: 'grid',
          gridTemplateColumns: 'repeat(3, minmax(150px, 1fr))',
          gap: '12px',
          padding: '16px',
          '@media screen and (max-width: 760px)': {
            gridTemplateColumns: '1fr',
          },
        })}>
          <FilterField
            label="Unit"
            value={draftFilters.unit ?? ''}
            onChange={(unit) => setDraftFilters({ ...draftFilters, unit })}
          />
          <FilterField
            label="Owner"
            value={draftFilters.owner ?? ''}
            onChange={(owner) => setDraftFilters({ ...draftFilters, owner })}
          />
          <FilterField
            label="Path prefix"
            value={draftFilters.path_prefix ?? ''}
            mono
            onChange={(path_prefix) =>
              setDraftFilters({ ...draftFilters, path_prefix })}
          />
          <FilterSelect
            label="Freshness"
            value={draftFilters.freshness ?? 'any'}
            values={['any', 'fresh', 'stale']}
            onChange={(freshness) =>
              setDraftFilters({ ...draftFilters, freshness })}
          />
          <FilterSelect
            label="Resolution"
            value={draftFilters.resolution ?? 'any'}
            values={['any', 'scip', 'syntax', 'unresolved']}
            onChange={(resolution) =>
              setDraftFilters({ ...draftFilters, resolution })}
          />
          <FilterSelect
            label="Ordering"
            value={draftFilters.ordering ?? 'source'}
            values={['source', 'unit']}
            onChange={(ordering) =>
              setDraftFilters({ ...draftFilters, ordering })}
          />
          <FilterSelect
            label="Comparison level"
            value={draftFilters.level ?? 'occurrence'}
            values={['occurrence', 'unit']}
            onChange={(level) =>
              setDraftFilters({ ...draftFilters, level })}
          />
          <FilterField
            label="Compatibility run ID"
            value={draftCompatibilityRun}
            mono
            onChange={setDraftCompatibilityRun}
          />
          <div className={css({ alignSelf: 'end' })}>
            <Button
              type="button"
              size={BUTTON_SIZE.compact}
              onClick={applyFilters}
            >
              Apply evidence filters
            </Button>
          </div>
        </div>
      </details>

      {failure && (
        <EvidenceError
          failure={failure}
          onRetry={() => {
            setCursorHistory([''])
            setPageIndex(0)
            setReload((value) => value + 1)
          }}
        />
      )}
      {loading && <EvidenceLoading label="Loading exact impact evidence…" />}
      {!loading && page && (
        <div className={css({ display: 'grid', gap: '18px', marginTop: '18px' })}>
          <AnalysisScope scope={page.analysis_scope} />
          <ImpactInventory page={page} />
          <PageControls
            label="impact evidence"
            pageIndex={pageIndex}
            complete={page.pagination.complete}
            onPrevious={() => setPageIndex((value) => Math.max(0, value - 1))}
            onNext={next}
          />
          <Caveat>{page.caveat}</Caveat>
        </div>
      )}
    </section>
  )
}

export function WorkbenchHowStep({
  available,
  investigationID,
  revisionID,
  evidence,
  onEvidenceChange,
}: EvidenceStepProps) {
  const [css] = useStyletron()
  const [draftAnchors, setDraftAnchors] = useState(evidence.anchors)
  const [implementation, setImplementation] =
    useState<WorkbenchImplementationPage | null>(null)
  const [checklist, setChecklist] =
    useState<WorkbenchChecklistPage | null>(null)
  const [implementationCursors, setImplementationCursors] = useState([''])
  const [checklistCursors, setChecklistCursors] = useState([''])
  const [implementationPage, setImplementationPage] = useState(0)
  const [checklistPage, setChecklistPage] = useState(0)
  const [loading, setLoading] = useState(false)
  const [failure, setFailure] = useState<EvidenceFailure | null>(null)
  const [reload, setReload] = useState(0)
  const queryKey = JSON.stringify({
    investigationID,
    revisionID,
    evidence,
  })

  useEffect(() => {
    setDraftAnchors(evidence.anchors)
  }, [evidence.anchors])

  useEffect(() => {
    setImplementationCursors([''])
    setChecklistCursors([''])
    setImplementationPage(0)
    setChecklistPage(0)
  }, [queryKey])

  const implementationCursor =
    implementationCursors[implementationPage] ?? ''
  const checklistCursor = checklistCursors[checklistPage] ?? ''
  const checklistEvidence = useMemo<WorkbenchChecklistEvidenceInput>(() => ({
    compatibility_run_id: evidence.compatibilityRun || undefined,
    impact_filters: evidence.filters,
    anchors: evidence.anchors,
  }), [evidence])

  useEffect(() => {
    if (!available || !investigationID || !revisionID) return
    const controller = new AbortController()
    let active = true
    setLoading(true)
    setFailure(null)
    Promise.all([
      fetchWorkbenchImplementation(
        investigationID,
        revisionID,
        {
          anchors: evidence.anchors,
          pageSize: evidencePageSize,
          cursor: implementationCursor || undefined,
        },
        controller.signal,
      ),
      fetchWorkbenchChecklist(
        investigationID,
        revisionID,
        {
          evidence: checklistEvidence,
          pageSize: evidencePageSize,
          cursor: checklistCursor || undefined,
        },
        controller.signal,
      ),
    ])
      .then(([nextImplementation, nextChecklist]) => {
        if (!active) return
        setImplementation(nextImplementation)
        setChecklist(nextChecklist)
      })
      .catch((cause) => {
        if (active && !isAbortError(cause)) {
          setImplementation(null)
          setChecklist(null)
          setFailure(evidenceFailure(cause, 'load implementation evidence'))
        }
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [
    available,
    checklistCursor,
    checklistEvidence,
    evidence.anchors,
    implementationCursor,
    investigationID,
    queryKey,
    reload,
    revisionID,
  ])

  if (!available) {
    return <EvidenceUnavailable step="How" />
  }
  if (!investigationID || !revisionID) {
    return <CommitFirst step="How" />
  }

  const applyAnchors = () => {
    onEvidenceChange({
      ...evidence,
      anchors: canonicalAnchors(draftAnchors),
    })
    setReload((value) => value + 1)
  }

  return (
    <section aria-labelledby="workbench-how-heading">
      <EvidenceHeading
        eyebrow="Implementation review"
        id="workbench-how-heading"
        title="How might implementation proceed?"
        detail="Related source and history are review candidates, never edit recommendations. The checklist is a deterministic projection; only fixed-category human dispositions are stored."
      />

      <AnchorEditor
        anchors={draftAnchors}
        onChange={setDraftAnchors}
        onApply={applyAnchors}
      />

      {failure && (
        <EvidenceError
          failure={failure}
          onRetry={() => {
            setImplementationCursors([''])
            setChecklistCursors([''])
            setImplementationPage(0)
            setChecklistPage(0)
            setReload((value) => value + 1)
          }}
        />
      )}
      {loading && (
        <EvidenceLoading label="Loading related evidence and checklist…" />
      )}
      {!loading && implementation && checklist && (
        <div className={css({ display: 'grid', gap: '22px', marginTop: '18px' })}>
          <ImplementationEvidence page={implementation} />
          <PageControls
            label="implementation evidence"
            pageIndex={implementationPage}
            complete={implementation.pagination.complete}
            onPrevious={() =>
              setImplementationPage((value) => Math.max(0, value - 1))}
            onNext={() => {
              const nextCursor = implementation.pagination.next_cursor
              if (!nextCursor) return
              setImplementationCursors((current) => [
                ...current.slice(0, implementationPage + 1),
                nextCursor,
              ])
              setImplementationPage((value) => value + 1)
            }}
          />
          <Checklist
            investigationID={investigationID}
            revisionID={revisionID}
            evidence={checklistEvidence}
            page={checklist}
            onRecorded={() => {
              setChecklistCursors([''])
              setChecklistPage(0)
              setReload((value) => value + 1)
            }}
          />
          <PageControls
            label="checklist"
            pageIndex={checklistPage}
            complete={checklist.pagination.complete}
            onPrevious={() =>
              setChecklistPage((value) => Math.max(0, value - 1))}
            onNext={() => {
              const nextCursor = checklist.pagination.next_cursor
              if (!nextCursor) return
              setChecklistCursors((current) => [
                ...current.slice(0, checklistPage + 1),
                nextCursor,
              ])
              setChecklistPage((value) => value + 1)
            }}
          />
          <Caveat>{checklist.caveat}</Caveat>
        </div>
      )}
    </section>
  )
}

function AnalysisScope({
  scope,
}: {
  scope: WorkbenchImpactPage['analysis_scope']
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const enabledCapabilities = useMemo(() => {
    const capabilities = new Set(
      scope.capabilities
        .filter((capability) =>
          capability.state === 'enabled' ||
          capability.state === 'available')
        .map((capability) => capability.id),
    )
    if (scope.coverage.length > 0) {
      capabilities.add('coverage-certificate')
    }
    return capabilities
  }, [scope.capabilities, scope.coverage.length])
  return (
    <section aria-labelledby="analysis-scope-heading" className={css(panelStyle(tok))}>
      <div className={css(panelHeaderStyle(tok))}>
        <span>
          <span className={css(eyebrowStyle(tok))}>Authority before rows</span>
          <span className={css({
            display: 'flex',
            gap: '7px',
            alignItems: 'center',
          })}>
            <h3 id="analysis-scope-heading" className={css(panelTitleStyle())}>
              Analysis scope &amp; gaps
            </h3>
            <SectionHelp
              termId="analysis_scope_and_gaps"
              enabledCapabilities={enabledCapabilities}
            />
          </span>
        </span>
        <EvidenceBadge tone={scope.gaps.length ? 'amber' : 'neutral'}>
          {scope.gaps.length} gaps
        </EvidenceBadge>
      </div>
      <div className={css({
        display: 'grid',
        gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
        '@media screen and (max-width: 760px)': {
          gridTemplateColumns: '1fr',
        },
      })}>
        <ScopeColumn title="Capabilities">
          {scope.capabilities.length === 0 ? (
            <EvidenceEmpty>No capability rows were returned.</EvidenceEmpty>
          ) : scope.capabilities.map((capability) => (
            <ScopeRow
              key={capability.id}
              title={capability.id}
              state={capability.state}
              detail={capability.reason}
            />
          ))}
        </ScopeColumn>
        <ScopeColumn title="Coverage">
          {scope.coverage.length === 0 ? (
            <EvidenceEmpty>No coverage certificate was returned.</EvidenceEmpty>
          ) : scope.coverage.map((coverage) => (
            <ScopeRow
              key={`${coverage.capability}:${coverage.target}`}
              title={coverage.capability}
              state={`${coverage.certificate.repository_count} repositories`}
              detail={coverage.target}
            />
          ))}
        </ScopeColumn>
        <ScopeColumn title="Explicit gaps">
          {scope.gaps.length === 0 ? (
            <EvidenceEmpty>
              No gaps were returned in this bounded projection.
            </EvidenceEmpty>
          ) : scope.gaps.map((gap, index) => (
            <ScopeRow
              key={`${gap.capability}:${gap.code}:${index}`}
              title={gap.code}
              state={gap.state}
              detail={[gap.capability, gap.target].filter(Boolean).join(' · ')}
            />
          ))}
        </ScopeColumn>
      </div>
    </section>
  )
}

function ImpactInventory({ page }: { page: WorkbenchImpactPage }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const groupCount = page.atlas.length +
    page.callers.length +
    (page.comparison ? 1 : 0) +
    (page.compatibility ? 1 : 0) +
    (page.field_references ? 1 : 0) +
    (page.resource_planes.length > 0 ? 1 : 0)
  return (
    <section aria-labelledby="impact-inventory-heading" className={css(panelStyle(tok))}>
      <div className={css(panelHeaderStyle(tok))}>
        <span>
          <span className={css(eyebrowStyle(tok))}>
            Page-local evidence · {page.ticket_kind}
          </span>
          <h3 id="impact-inventory-heading" className={css(panelTitleStyle())}>
            Source-first impact inventory
          </h3>
        </span>
        <EvidenceBadge tone="neutral">
          {groupCount} evidence groups
        </EvidenceBadge>
      </div>
      {page.scenario_emphasis.length > 0 && (
        <div className={css({
          display: 'flex',
          gap: '6px',
          flexWrap: 'wrap',
          padding: '12px 16px',
          borderBottom: `1px solid ${tok.innerSep}`,
        })}>
          {page.scenario_emphasis.map((value) => (
            <EvidenceBadge key={value} tone="blue">{humanize(value)}</EvidenceBadge>
          ))}
        </div>
      )}
      {groupCount === 0 && (
        <EvidenceEmpty>
          No evidence groups are visible on this exact bounded page. This is
          not an absence or completeness result; inspect scope, gaps, and
          paging.
        </EvidenceEmpty>
      )}
      {page.atlas.map((atlas, index) => (
        <EvidenceGroup
          key={`atlas:${atlas.selection.role}:${index}`}
          title={`${humanize(atlas.selection.role)} declaration`}
          state={atlas.relationships_truncated || atlas.shape_truncated
            ? 'truncated'
            : atlas.selection.protocol}
        >
          {atlas.declaration.sources.map((source) => (
            <SourceEvidenceRow
              key={`${source.repository}:${source.commit}:${source.path}:${source.start_byte}`}
              source={source}
              classification="selected declaration"
              detail={atlas.selection.canonical_operation}
            />
          ))}
          {atlas.implementations.map((relationship, row) => (
            <ClaimEvidenceRow
              key={`implementation:${row}`}
              relationship={relationship}
              label="implementation"
            />
          ))}
          {atlas.name_matches.map((relationship, row) => (
            <ClaimEvidenceRow
              key={`name:${row}`}
              relationship={relationship}
              label="unproven name match"
            />
          ))}
          {atlas.extractor_abstentions.map((relationship, row) => (
            <ClaimEvidenceRow
              key={`abstention:${row}`}
              relationship={relationship}
              label="extractor abstention"
            />
          ))}
        </EvidenceGroup>
      ))}
      {page.callers.map((caller, index) => (
        <EvidenceGroup
          key={`caller:${caller.selection.role}:${index}`}
          title={`${humanize(caller.selection.role)} caller evidence`}
          state={`${caller.total_matching_rows} matching`}
        >
          {caller.resolved_callers.map((row, rowIndex) => (
            <CallerEvidenceRow
              key={`resolved:${row.source.assertion_id}:${rowIndex}`}
              row={row}
              label="resolved caller"
            />
          ))}
          {caller.extractor_abstentions.map((row, rowIndex) => (
            <CallerEvidenceRow
              key={`abstention:${row.source.assertion_id}:${rowIndex}`}
              row={row}
              label="extractor abstention"
            />
          ))}
          {caller.pagination.next_cursor && (
            <EvidenceNotice>
              Caller substream is partial on this page.
            </EvidenceNotice>
          )}
        </EvidenceGroup>
      ))}
      {page.comparison && (
        <EvidenceGroup
          title="Migration comparison"
          state={`${page.comparison.total_rows} rows`}
        >
          {page.comparison.rows.map((row) => (
            <div key={row.key}>
              <div className={css(evidenceRowStyle(tok))}>
                <EvidenceBadge tone={row.classification === 'unresolved'
                  ? 'amber'
                  : 'blue'}
                >
                  {humanize(row.classification)}
                </EvidenceBadge>
                <div className={css({ minWidth: 0 })}>
                  <div className={css(rowTitleStyle(tok))}>{row.key}</div>
                  <div className={css(rowDetailStyle(tok))}>
                    {row.old.occurrence_count} current ·{' '}
                    {row.replacement.occurrence_count} replacement occurrences
                  </div>
                </div>
              </div>
              {row.old.rows.map((source, index) => (
                <CallerEvidenceRow
                  key={`current:${source.source.assertion_id}:${index}`}
                  row={source}
                  label="current endpoint caller"
                />
              ))}
              {row.old.rows_truncated && (
                <EvidenceNotice>
                  Current endpoint citations are truncated for this comparison
                  row.
                </EvidenceNotice>
              )}
              {row.replacement.rows.map((source, index) => (
                <CallerEvidenceRow
                  key={`replacement:${source.source.assertion_id}:${index}`}
                  row={source}
                  label="replacement endpoint caller"
                />
              ))}
              {row.replacement.rows_truncated && (
                <EvidenceNotice>
                  Replacement endpoint citations are truncated for this
                  comparison row.
                </EvidenceNotice>
              )}
            </div>
          ))}
        </EvidenceGroup>
      )}
      {page.compatibility && (
        <EvidenceGroup
          title="Retained compatibility analysis"
          state={page.compatibility.status}
        >
          {page.compatibility.failure && (
            <EvidenceNotice>{page.compatibility.failure}</EvidenceNotice>
          )}
          {page.compatibility.violations.map((violation, index) => (
            <div key={`${violation.path}:${violation.start_line}:${index}`} className={css(evidenceRowStyle(tok))}>
              <SourceLink
                repository=""
                commit=""
                path={violation.path}
                line={violation.start_line}
                endLine={violation.end_line}
                disabled
              />
              <div className={css({ minWidth: 0 })}>
                <div className={css(rowTitleStyle(tok))}>{violation.rule}</div>
                <div className={css(rowDetailStyle(tok))}>{violation.message}</div>
              </div>
            </div>
          ))}
        </EvidenceGroup>
      )}
      {page.field_references && (
        <EvidenceGroup
          title="Stable affected-field references"
          state={`${page.field_references.total_rows} rows`}
        >
          {page.field_references.rows.map((row) => (
            <div key={row.assertion.id}>
              <div className={css(evidenceRowStyle(tok))}>
                <EvidenceBadge tone="blue">
                  field {row.field.field_number}
                </EvidenceBadge>
                <div className={css({ minWidth: 0 })}>
                  <div className={css(rowTitleStyle(tok))}>
                    {row.field.message}
                  </div>
                  <div className={css(rowDetailStyle(tok))}>
                    {row.assertion.repo} · {row.assertion.predicate}
                  </div>
                </div>
              </div>
              {row.evidence.flatMap((entry) =>
                entry.occurrences.map((occurrence) => (
                  <SourceEvidenceRow
                    key={occurrence.occurrence_id}
                    source={{
                      repository: occurrence.repo,
                      commit: occurrence.commit,
                      path: occurrence.path,
                      start_line: occurrence.start_line,
                      end_line: occurrence.end_line,
                    }}
                    classification="field reference"
                    detail={`${row.field.message}#${row.field.field_number}`}
                  />
                )))}
              {row.evidence.every((entry) => entry.occurrences.length === 0) && (
                <EvidenceNotice>
                  Field-reference evidence has no visible occurrence citation.
                </EvidenceNotice>
              )}
            </div>
          ))}
        </EvidenceGroup>
      )}
      <ResourcePlanes planes={page.resource_planes} />
    </section>
  )
}

function ImplementationEvidence({
  page,
}: {
  page: WorkbenchImplementationPage
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section aria-labelledby="implementation-evidence-heading" className={css(panelStyle(tok))}>
      <div className={css(panelHeaderStyle(tok))}>
        <span>
          <span className={css(eyebrowStyle(tok))}>Immutable source &amp; history</span>
          <h3
            id="implementation-evidence-heading"
            className={css(panelTitleStyle())}
          >
            Related implementation evidence
          </h3>
        </span>
        <EvidenceBadge tone="neutral">
          {page.rows.length} / {page.pagination.total_rows}
        </EvidenceBadge>
      </div>
      <CapabilityStrip
        capabilities={page.capabilities}
        gaps={page.gaps}
      />
      {page.rows.length === 0 ? (
        <EvidenceEmpty>
          No related implementation rows are visible on this page. This does
          not establish that no implementation exists.
        </EvidenceEmpty>
      ) : page.rows.map((row) => (
        <article key={row.id} className={css({
          padding: '15px 16px',
          borderBottom: `1px solid ${tok.innerSep}`,
        })}>
          <div className={css({
            display: 'grid',
            gridTemplateColumns: 'minmax(210px, 0.55fr) minmax(0, 1fr)',
            gap: '14px',
            '@media screen and (max-width: 680px)': {
              gridTemplateColumns: '1fr',
            },
          })}>
            <SourceLink
              repository={row.source.repository}
              commit={row.source.commit}
              path={row.source.path}
              line={row.source.start_line}
              endLine={row.source.end_line}
            />
            <div className={css({ minWidth: 0 })}>
              <div className={css({
                display: 'flex',
                gap: '6px',
                flexWrap: 'wrap',
                marginBottom: '6px',
              })}>
                <EvidenceBadge tone={row.review_state === 'selected'
                  ? 'green'
                  : 'blue'}
                >
                  {humanize(row.review_state)}
                </EvidenceBadge>
                <EvidenceBadge tone="neutral">{humanize(row.kind)}</EvidenceBadge>
                <EvidenceBadge tone="neutral">{humanize(row.code_role)}</EvidenceBadge>
              </div>
              <div className={css(rowTitleStyle(tok))}>
                {row.symbol || row.selection_input || row.source.path}
              </div>
              <div className={css(rowDetailStyle(tok))}>{row.selection_rule}</div>
            </div>
          </div>
          {(row.commit || row.diff) && (
            <details className={css({ marginTop: '10px' })}>
              <summary className={css(summaryStyle(tok))}>
                History evidence
              </summary>
              <div className={css({
                display: 'grid',
                gap: '8px',
                padding: '10px 0 0',
              })}>
                {row.commit && (
                  <div className={css(rowDetailStyle(tok))}>
                    <span className={css({ fontFamily: FONTS.MONO })}>
                      {shortDigest(row.commit.id)}
                    </span>
                    {' · '}{row.commit.subject}
                  </div>
                )}
                {row.diff && (
                  <pre className={css({
                    maxHeight: '260px',
                    margin: 0,
                    padding: '12px',
                    overflow: 'auto',
                    border: `1px solid ${tok.cardBorder}`,
                    backgroundColor: tok.pageBg,
                    color: tok.textSecondary,
                    fontFamily: FONTS.MONO,
                    fontSize: '10px',
                    lineHeight: '16px',
                    whiteSpace: 'pre-wrap',
                  })}>
                    {row.diff.patch_excerpt || 'No bounded patch excerpt.'}
                    {row.diff.patch_truncated && '\n… patch excerpt truncated'}
                  </pre>
                )}
              </div>
            </details>
          )}
        </article>
      ))}
      <Caveat>{page.caveat}</Caveat>
    </section>
  )
}

function Checklist({
  investigationID,
  revisionID,
  evidence,
  page,
  onRecorded,
}: {
  investigationID: string
  revisionID: string
  evidence: WorkbenchChecklistEvidenceInput
  page: WorkbenchChecklistPage
  onRecorded: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section aria-labelledby="workbench-checklist-heading" className={css(panelStyle(tok))}>
      <div className={css(panelHeaderStyle(tok))}>
        <span>
          <span className={css(eyebrowStyle(tok))}>Human state only by disposition</span>
          <h3 id="workbench-checklist-heading" className={css(panelTitleStyle())}>
            Evidence-backed checklist
          </h3>
        </span>
        <EvidenceBadge tone={
          page.evidence_snapshot.impact_truncated ||
          page.evidence_snapshot.implementation_truncated
            ? 'amber'
            : 'neutral'
        }>
          {page.entries.length} / {page.pagination.total_entries}
        </EvidenceBadge>
      </div>
      {(page.evidence_snapshot.impact_truncated ||
        page.evidence_snapshot.implementation_truncated) && (
        <EvidenceNotice>
          The suggestion projection reached an evidence-page ceiling. Remaining
          evidence is represented explicitly; this checklist is not complete.
        </EvidenceNotice>
      )}
      {page.entries.length === 0 ? (
        <EvidenceEmpty>
          No deterministic suggestions are visible for this exact evidence
          snapshot. Nothing is implicitly completed.
        </EvidenceEmpty>
      ) : page.entries.map((entry) => (
        <ChecklistEntry
          key={`${entry.suggestion.suggestion_id}:${entry.evidence_state}`}
          investigationID={investigationID}
          revisionID={revisionID}
          evidence={evidence}
          entry={entry}
          onRecorded={onRecorded}
        />
      ))}
    </section>
  )
}

function ChecklistEntry({
  investigationID,
  revisionID,
  evidence,
  entry,
  onRecorded,
}: {
  investigationID: string
  revisionID: string
  evidence: WorkbenchChecklistEvidenceInput
  entry: WorkbenchChecklistEntry
  onRecorded: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [category, setCategory] =
    useState<WorkbenchDispositionCategory>('accepted')
  const [rationale, setRationale] = useState('')
  const [recording, setRecording] = useState(false)
  const [failure, setFailure] = useState<EvidenceFailure | null>(null)
  const [recorded, setRecorded] = useState('')
  const rationaleRequired =
    category === 'rejected' ||
    category === 'reopened' ||
    category === 'waived'
  const action = entry.disposition ? 'Record correction' : 'Record disposition'

  useEffect(() => {
    setFailure(null)
    setRecorded('')
  }, [
    entry.disposition?.disposition_id,
    entry.evidence_state,
    entry.suggestion.content_digest,
  ])

  const record = async () => {
    if (rationaleRequired && !rationale.trim()) return
    const supersedes = entry.disposition?.disposition_id
    const idempotencyInput = JSON.stringify({
      suggestion: entry.suggestion.content_digest,
      category,
      rationale: rationale.trim(),
      supersedes,
    })
    setRecording(true)
    setFailure(null)
    setRecorded('')
    try {
      const disposition = await recordWorkbenchDisposition(
        investigationID,
        revisionID,
        {
          investigation_id: investigationID,
          expected_revision_id: revisionID,
          idempotency_key: dispositionIdempotencyKey(
            entry.suggestion.suggestion_id,
            idempotencyInput,
          ),
          evidence,
          suggestion: entry.suggestion,
          category,
          rationale: rationale.trim() || undefined,
          supersedes,
        },
      )
      setRecorded(
        `${humanize(disposition.category)} recorded as sequence ${disposition.sequence}.`,
      )
      onRecorded()
    } catch (cause) {
      if (!isAbortError(cause)) {
        setFailure(evidenceFailure(cause, 'record disposition'))
      }
    } finally {
      setRecording(false)
    }
  }

  return (
    <article className={css({
      display: 'grid',
      gridTemplateColumns: 'minmax(0, 1fr) minmax(240px, 0.38fr)',
      gap: '18px',
      padding: '16px',
      borderBottom: `1px solid ${tok.innerSep}`,
      '@media screen and (max-width: 760px)': {
        gridTemplateColumns: '1fr',
      },
    })}>
      <div className={css({ minWidth: 0 })}>
        <div className={css({
          display: 'flex',
          gap: '6px',
          flexWrap: 'wrap',
          marginBottom: '7px',
        })}>
          <EvidenceBadge tone={entry.evidence_state === 'stale' ? 'amber' : 'green'}>
            {entry.evidence_state} evidence
          </EvidenceBadge>
          <EvidenceBadge tone={entry.state === 'unaccepted' ? 'neutral' : 'blue'}>
            {humanize(entry.state)}
          </EvidenceBadge>
          <EvidenceBadge tone="neutral">
            {humanize(entry.suggestion.kind)}
          </EvidenceBadge>
        </div>
        <h4 className={css({
          margin: 0,
          fontSize: '14px',
          lineHeight: '21px',
        })}>
          {entry.suggestion.summary}
        </h4>
        <p className={css(rowDetailStyle(tok))}>
          {entry.suggestion.selection_rule}
        </p>
        <details>
          <summary className={css(summaryStyle(tok))}>
            {entry.suggestion.evidence.length} immutable evidence references
          </summary>
          <div className={css({ display: 'grid', gap: '7px', marginTop: '8px' })}>
            {entry.suggestion.evidence.map((reference) => (
              <div
                key={`${reference.plane}:${reference.kind}:${reference.id}`}
                className={css({ fontSize: '10px', color: tok.textTertiary })}
              >
                {reference.repository && reference.commit && reference.path ? (
                  <SourceLink
                    repository={reference.repository}
                    commit={reference.commit}
                    path={reference.path}
                    line={reference.start_line}
                    endLine={reference.end_line}
                  />
                ) : (
                  <span className={css({ fontFamily: FONTS.MONO })}>
                    {reference.plane} · {reference.kind} · {shortDigest(reference.id)}
                  </span>
                )}
              </div>
            ))}
          </div>
        </details>
        {entry.disposition && (
          <div className={css({
            marginTop: '10px',
            padding: '10px',
            borderLeft: `3px solid ${tok.statusBlue}`,
            backgroundColor: tok.bandBg,
          })}>
            <div className={css(rowTitleStyle(tok))}>
              Human-recorded {entry.disposition.category} · sequence{' '}
              {entry.disposition.sequence}
            </div>
            <div className={css(rowDetailStyle(tok))}>
              {entry.disposition.actor}
              {entry.disposition.rationale
                ? ` · ${entry.disposition.rationale}`
                : ''}
            </div>
          </div>
        )}
      </div>
      <div className={css({
        display: 'grid',
        alignContent: 'start',
        gap: '10px',
      })}>
        <label className={css(fieldLabelStyle(tok))}>
          Fixed disposition
          <select
            aria-label={`Disposition for ${entry.suggestion.summary}`}
            value={category}
            onChange={(event) =>
              setCategory(event.currentTarget.value as WorkbenchDispositionCategory)}
            className={css(controlStyle(tok))}
          >
            <option value="accepted">Accepted</option>
            <option value="rejected">Rejected</option>
            <option value="completed">Completed</option>
            <option value="reopened">Reopened</option>
            <option value="waived">Waived</option>
          </select>
        </label>
        <label className={css(fieldLabelStyle(tok))}>
          Rationale {rationaleRequired ? '(required)' : '(optional)'}
          <textarea
            aria-label={`Rationale for ${entry.suggestion.summary}`}
            value={rationale}
            rows={4}
            onChange={(event) => setRationale(event.currentTarget.value)}
            className={css(textareaStyle(tok))}
          />
        </label>
        <Button
          type="button"
          size={BUTTON_SIZE.compact}
          isLoading={recording}
          disabled={
            entry.evidence_state === 'stale' && !entry.disposition ||
            (rationaleRequired && !rationale.trim())
          }
          onClick={() => void record()}
        >
          {action}
        </Button>
        {entry.evidence_state === 'stale' && !entry.disposition && (
          <div className={css(rowDetailStyle(tok))}>
            A stale suggestion cannot receive a new root disposition.
          </div>
        )}
        {failure && (
          <EvidenceError
            failure={failure}
            onRetry={failure.status === 409 ? onRecorded : undefined}
            compact
          />
        )}
        {recorded && (
          <div role="status" aria-live="polite" className={css({
            color: tok.statusGreen,
            fontSize: '11px',
          })}>
            {recorded}
          </div>
        )}
      </div>
    </article>
  )
}

function AnchorEditor({
  anchors,
  onChange,
  onApply,
}: {
  anchors: WorkbenchImplementationAnchor[]
  onChange: (value: WorkbenchImplementationAnchor[]) => void
  onApply: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <details className={css(panelStyle(tok))}>
      <summary className={css(summaryStyle(tok))}>
        Explicit source anchors · {anchors.length} / {anchorLimit}
      </summary>
      {anchors.map((anchor, index) => (
        <fieldset
          key={index}
          className={css({
            display: 'grid',
            gridTemplateColumns: 'minmax(180px, 1fr) 170px minmax(180px, 1fr) 90px 90px 100px auto',
            gap: '8px',
            alignItems: 'end',
            margin: 0,
            padding: '12px 16px',
            border: 0,
            borderBottom: `1px solid ${tok.innerSep}`,
            '@media screen and (max-width: 980px)': {
              gridTemplateColumns: '1fr 1fr',
            },
            '@media screen and (max-width: 560px)': {
              gridTemplateColumns: '1fr',
            },
          })}
        >
          <legend className={css(visuallyHiddenStyle())}>
            Source anchor {index + 1}
          </legend>
          <FilterField
            label="Repository"
            ariaLabel={`Anchor ${index + 1} repository`}
            value={anchor.repository}
            mono
            onChange={(repository) =>
              onChange(replaceAt(anchors, index, { ...anchor, repository }))}
          />
          <FilterField
            label="Commit"
            ariaLabel={`Anchor ${index + 1} commit`}
            value={anchor.commit}
            mono
            onChange={(commit) =>
              onChange(replaceAt(anchors, index, { ...anchor, commit }))}
          />
          <FilterField
            label="Path"
            ariaLabel={`Anchor ${index + 1} path`}
            value={anchor.path}
            mono
            onChange={(path) =>
              onChange(replaceAt(anchors, index, { ...anchor, path }))}
          />
          <NumberField
            label="Line"
            ariaLabel={`Anchor ${index + 1} line`}
            value={anchor.line}
            onChange={(line) =>
              onChange(replaceAt(anchors, index, { ...anchor, line }))}
          />
          <NumberField
            label="Character"
            ariaLabel={`Anchor ${index + 1} character`}
            value={anchor.character}
            onChange={(character) =>
              onChange(replaceAt(anchors, index, { ...anchor, character }))}
          />
          <FilterSelect
            label="Encoding"
            ariaLabel={`Anchor ${index + 1} encoding`}
            value={anchor.encoding ?? 'utf-8'}
            values={['utf-8', 'utf-16', 'utf-32']}
            onChange={(encoding) => onChange(replaceAt(anchors, index, {
              ...anchor,
              encoding: encoding as WorkbenchImplementationAnchor['encoding'],
            }))}
          />
          <Button
            type="button"
            size={BUTTON_SIZE.mini}
            kind={BUTTON_KIND.tertiary}
            onClick={() =>
              onChange(anchors.filter((_value, candidate) => candidate !== index))}
          >
            Remove
          </Button>
        </fieldset>
      ))}
      <div className={css({
        display: 'flex',
        gap: '8px',
        flexWrap: 'wrap',
        padding: '12px 16px',
      })}>
        <Button
          type="button"
          size={BUTTON_SIZE.mini}
          kind={BUTTON_KIND.secondary}
          disabled={anchors.length >= anchorLimit}
          onClick={() => onChange([
            ...anchors,
            {
              repository: '',
              commit: '',
              path: '',
              line: 0,
              character: 0,
              encoding: 'utf-8',
            },
          ])}
        >
          Add source anchor
        </Button>
        <Button
          type="button"
          size={BUTTON_SIZE.mini}
          onClick={onApply}
        >
          Apply anchors
        </Button>
      </div>
    </details>
  )
}

function ResourcePlanes({
  planes,
}: {
  planes: WorkbenchImpactPage['resource_planes']
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (planes.length === 0) return null
  return (
    <EvidenceGroup title="Resource planes" state={`${planes.length} declared`}>
      {planes.map((plane) => (
        <div key={plane.id}>
          <div className={css(evidenceRowStyle(tok))}>
            <EvidenceBadge tone={
              plane.state === 'enabled'
                ? 'green'
                : plane.state === 'failed' || plane.state === 'stale'
                  ? 'amber'
                  : 'neutral'
            }>
              {humanize(plane.state)}
            </EvidenceBadge>
            <div className={css({ minWidth: 0 })}>
              <div className={css(rowTitleStyle(tok))}>{plane.label}</div>
              <div className={css(rowDetailStyle(tok))}>
                {plane.reason ||
                  `${plane.relationships.length} cited relationships`}
              </div>
            </div>
          </div>
          {plane.relationships.map((relationship, relationshipIndex) => (
            <div
              key={`${relationship.kind}:${relationship.subject}:${relationship.object}:${relationshipIndex}`}
            >
              <div className={css(evidenceRowStyle(tok))}>
                <EvidenceBadge tone="blue">
                  {humanize(relationship.classification)}
                </EvidenceBadge>
                <div className={css({ minWidth: 0 })}>
                  <div className={css(rowTitleStyle(tok))}>
                    {relationship.subject} → {relationship.object}
                  </div>
                  <div className={css(rowDetailStyle(tok))}>
                    {humanize(relationship.kind)}
                  </div>
                </div>
              </div>
              {relationship.sources.map((source) => (
                <SourceEvidenceRow
                  key={`${source.repository}:${source.commit}:${source.path}:${source.start_byte}`}
                  source={source}
                  classification={relationship.classification}
                  detail={`${relationship.subject} → ${relationship.object}`}
                />
              ))}
              {relationship.sources.length === 0 && (
                <EvidenceNotice>
                  This relationship has no visible source citation.
                </EvidenceNotice>
              )}
            </div>
          ))}
        </div>
      ))}
    </EvidenceGroup>
  )
}

function CapabilityStrip({
  capabilities,
  gaps,
}: {
  capabilities: WorkbenchImplementationPage['capabilities']
  gaps: WorkbenchImplementationPage['gaps']
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (capabilities.length === 0 && gaps.length === 0) return null
  return (
    <div className={css({
      display: 'flex',
      gap: '7px',
      flexWrap: 'wrap',
      padding: '12px 16px',
      borderBottom: `1px solid ${tok.innerSep}`,
    })}>
      {capabilities.map((capability) => (
        <EvidenceBadge
          key={capability.id}
          tone={capability.state === 'available' ? 'green' : 'neutral'}
        >
          {capability.id}: {capability.state}
        </EvidenceBadge>
      ))}
      {gaps.map((gap, index) => (
        <EvidenceBadge
          key={`${gap.capability}:${gap.code}:${index}`}
          tone="amber"
        >
          {gap.capability}: {gap.code}
        </EvidenceBadge>
      ))}
    </div>
  )
}

function EvidenceGroup({
  title,
  state,
  children,
}: {
  title: string
  state: string
  children: React.ReactNode
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section aria-label={title}>
      <div className={css({
        display: 'flex',
        justifyContent: 'space-between',
        gap: '10px',
        alignItems: 'center',
        padding: '10px 16px',
        borderTop: `1px solid ${tok.cardBorder}`,
        borderBottom: `1px solid ${tok.innerSep}`,
        backgroundColor: tok.bandBg,
      })}>
        <h4 className={css({
          margin: 0,
          fontSize: '12px',
          lineHeight: '18px',
        })}>
          {title}
        </h4>
        <EvidenceBadge tone="neutral">{state}</EvidenceBadge>
      </div>
      {children}
    </section>
  )
}

function SourceEvidenceRow({
  source,
  classification,
  detail,
}: {
  source: {
    repository: string
    commit: string
    path: string
    start_line?: number
    end_line?: number
  }
  classification: string
  detail: string
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css(evidenceRowStyle(tok))}>
      <SourceLink
        repository={source.repository}
        commit={source.commit}
        path={source.path}
        line={source.start_line}
        endLine={source.end_line}
      />
      <div className={css({ minWidth: 0 })}>
        <div className={css(rowTitleStyle(tok))}>{classification}</div>
        <div className={css(rowDetailStyle(tok))}>{detail}</div>
      </div>
    </div>
  )
}

function ClaimEvidenceRow({
  relationship,
  label,
}: {
  relationship: WorkbenchImpactPage['atlas'][number]['implementations'][number]
  label: string
}) {
  const sources = relationship.claim.sources
  if (sources.length === 0) {
    return <EvidenceNotice>{label}: no visible source citation.</EvidenceNotice>
  }
  return (
    <>
      {sources.map((source) => (
        <SourceEvidenceRow
          key={`${source.repository}:${source.commit}:${source.path}:${source.start_byte}`}
          source={source}
          classification={label}
          detail={[relationship.classification, relationship.reason]
            .filter(Boolean)
            .join(' · ')}
        />
      ))}
    </>
  )
}

function CallerEvidenceRow({
  row,
  label,
}: {
  row: WorkbenchImpactPage['callers'][number]['resolved_callers'][number]
  label: string
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const candidates = row.unit.candidates ?? []
  return (
    <div className={css(evidenceRowStyle(tok))}>
      <SourceLink
        repository={row.source.repository}
        commit={row.source.commit}
        path={row.source.path}
        line={row.source.start_line}
        endLine={row.source.end_line}
      />
      <div className={css({ minWidth: 0 })}>
        <div className={css({
          display: 'flex',
          alignItems: 'center',
          gap: '6px',
          flexWrap: 'wrap',
        })}>
          <span className={css(rowTitleStyle(tok))}>{label}</span>
          <EvidenceBadge tone={
            row.unit.state === 'ambiguous' ? 'amber' : 'neutral'
          }>
            unit {row.unit.state}
          </EvidenceBadge>
          <EvidenceBadge tone={row.fresh ? 'green' : 'amber'}>
            {row.fresh ? 'fresh' : 'stale'}
          </EvidenceBadge>
        </div>
        <div className={css(rowDetailStyle(tok))}>
          {row.resolution} · {row.tier}
          {row.unresolved_reason ? ` · ${row.unresolved_reason}` : ''}
        </div>
        {row.unit.state === 'ambiguous' && (
          <div className={css({
            marginTop: '5px',
            color: tok.statusAmber,
            fontSize: '10px',
            lineHeight: '15px',
          })}>
            {row.unit.candidate_total ?? candidates.length} candidate units
            {candidates.length
              ? `: ${candidates.map((candidate) => candidate.id).join(', ')}`
              : ''}
          </div>
        )}
      </div>
    </div>
  )
}

function SourceLink({
  repository,
  commit,
  path,
  line,
  endLine,
  disabled = false,
}: {
  repository: string
  commit: string
  path: string
  line?: number
  endLine?: number
  disabled?: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const label = `${path}${line
    ? `:${line}${endLine && endLine !== line ? `–${endLine}` : ''}`
    : ''}`
  if (disabled || !repository || !commit) {
    return (
      <span className={css({
        minWidth: 0,
        color: tok.textTertiary,
        fontFamily: FONTS.MONO,
        fontSize: '10px',
        overflowWrap: 'anywhere',
      })}>
        {label}
      </span>
    )
  }
  return (
    <a
      href={href('/file', {
        repo: repository,
        path,
        ref: commit,
        ...(line ? { L: String(line) } : {}),
      })}
      className={css({
        minWidth: 0,
        color: tok.accent,
        fontFamily: FONTS.MONO,
        fontSize: '10px',
        lineHeight: '16px',
        overflowWrap: 'anywhere',
        textDecoration: 'none',
        ':hover': { textDecoration: 'underline' },
        ':focus-visible': {
          outline: `2px solid ${tok.accent}`,
          outlineOffset: '2px',
        },
      })}
    >
      <span className={css({
        display: 'block',
        color: tok.textTertiary,
        fontSize: '9px',
      })}>
        {repository} @ {shortDigest(commit)}
      </span>
      {label}
    </a>
  )
}

function PageControls({
  label,
  pageIndex,
  complete,
  onPrevious,
  onNext,
}: {
  label: string
  pageIndex: number
  complete: boolean
  onPrevious: () => void
  onNext: () => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <nav
      aria-label={`${label} pages`}
      className={css({
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        gap: '10px',
        padding: '10px 0',
        color: tok.textTertiary,
        fontFamily: FONTS.MONO,
        fontSize: '10px',
      })}
    >
      <Button
        type="button"
        size={BUTTON_SIZE.mini}
        kind={BUTTON_KIND.secondary}
        disabled={pageIndex === 0}
        onClick={onPrevious}
      >
        Previous page
      </Button>
      <span>Page {pageIndex + 1} · {complete ? 'stream complete' : 'more bounded rows'}</span>
      <Button
        type="button"
        size={BUTTON_SIZE.mini}
        kind={BUTTON_KIND.secondary}
        disabled={complete}
        onClick={onNext}
      >
        Next page
      </Button>
    </nav>
  )
}

function EvidenceHeading({
  eyebrow,
  id,
  title,
  detail,
}: {
  eyebrow: string
  id: string
  title: string
  detail: string
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <header className={css({
      display: 'grid',
      gridTemplateColumns: 'minmax(0, 1fr) minmax(260px, 0.42fr)',
      gap: '24px',
      alignItems: 'end',
      marginBottom: '18px',
      paddingBottom: '15px',
      borderBottom: `1px solid ${tok.cardBorder}`,
      '@media screen and (max-width: 720px)': {
        gridTemplateColumns: '1fr',
        gap: '8px',
      },
    })}>
      <div>
        <div className={css(eyebrowStyle(tok))}>{eyebrow}</div>
        <h2 id={id} className={css({
          margin: '5px 0 0',
          fontSize: 'clamp(23px, 3vw, 34px)',
          lineHeight: 1.12,
          letterSpacing: '-0.03em',
        })}>
          {title}
        </h2>
      </div>
      <p className={css({
        margin: 0,
        color: tok.textTertiary,
        fontSize: '11px',
        lineHeight: '18px',
      })}>
        {detail}
      </p>
    </header>
  )
}

function EvidenceUnavailable({ step }: { step: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section aria-labelledby={`workbench-${step.toLowerCase()}-heading`}>
      <EvidenceHeading
        eyebrow="Capability unavailable"
        id={`workbench-${step.toLowerCase()}-heading`}
        title={`${step} evidence is not registered`}
        detail="The current server has not bound the shared Workbench evidence services. No fallback or inferred evidence is shown."
      />
      <div className={css(emptyStyle(tok))}>
        This surface remains dark unless the exact evidence capability is
        advertised.
      </div>
    </section>
  )
}

function CommitFirst({ step }: { step: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section aria-labelledby={`workbench-${step.toLowerCase()}-heading`}>
      <EvidenceHeading
        eyebrow="Exact revision required"
        id={`workbench-${step.toLowerCase()}-heading`}
        title={`Commit the draft before opening ${step}`}
        detail="Evidence readers bind one authorized current Investigation revision. Draft intent is not repository evidence."
      />
      <div className={css(emptyStyle(tok))}>
        Preview and create or append the Workbench, then return through its
        exact Investigation and Revision URL.
      </div>
    </section>
  )
}

function EvidenceLoading({ label }: { label: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div
      role="status"
      aria-live="polite"
      className={css({
        minHeight: '180px',
        display: 'grid',
        placeItems: 'center',
        marginTop: '18px',
        border: `1px solid ${tok.cardBorder}`,
        color: tok.textTertiary,
      })}
    >
      <span className={css({ display: 'grid', justifyItems: 'center', gap: '9px' })}>
        <Spinner $size="small" />
        {label}
      </span>
    </div>
  )
}

function EvidenceError({
  failure,
  onRetry,
  compact = false,
}: {
  failure: EvidenceFailure
  onRetry?: () => void
  compact?: boolean
}) {
  const [css] = useStyletron()
  return (
    <div className={css({ marginTop: compact ? 0 : '16px' })}>
      <Notification kind={NOTIFICATION_KIND.negative} closeable={false}>
        <strong>{failure.title}.</strong> {failure.detail}
      </Notification>
      {onRetry && (
        <Button
          type="button"
          size={BUTTON_SIZE.mini}
          kind={BUTTON_KIND.secondary}
          onClick={onRetry}
        >
          Restart exact evidence
        </Button>
      )}
    </div>
  )
}

function EvidenceBadge({
  tone,
  children,
}: {
  tone: 'green' | 'amber' | 'blue' | 'neutral'
  children: React.ReactNode
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const color = tone === 'green'
    ? tok.statusGreen
    : tone === 'amber'
      ? tok.statusAmber
      : tone === 'blue'
        ? tok.statusBlue
        : tok.textTertiary
  return (
    <span className={css({
      display: 'inline-flex',
      minHeight: '20px',
      boxSizing: 'border-box',
      alignItems: 'center',
      padding: '1px 6px',
      border: `1px solid ${color}`,
      color,
      fontFamily: FONTS.MONO,
      fontSize: '8px',
      lineHeight: '13px',
      letterSpacing: '0.03em',
      textTransform: 'uppercase',
    })}>
      {children}
    </span>
  )
}

function ScopeColumn({
  title,
  children,
}: {
  title: string
  children: React.ReactNode
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({
      minWidth: 0,
      padding: '13px 16px',
      borderRight: `1px solid ${tok.innerSep}`,
    })}>
      <h4 className={css({
        margin: '0 0 8px',
        color: tok.textTertiary,
        fontFamily: FONTS.MONO,
        fontSize: '9px',
        letterSpacing: '0.06em',
        textTransform: 'uppercase',
      })}>
        {title}
      </h4>
      <div className={css({ display: 'grid', gap: '8px' })}>{children}</div>
    </div>
  )
}

function ScopeRow({
  title,
  state,
  detail,
}: {
  title: string
  state: string
  detail?: string
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div>
      <div className={css({
        display: 'flex',
        gap: '6px',
        alignItems: 'center',
        flexWrap: 'wrap',
      })}>
        <span className={css(rowTitleStyle(tok))}>{title}</span>
        <EvidenceBadge tone={
          state === 'failed' || state === 'stale' ||
          state === 'unsupported' || state === 'ambiguous'
            ? 'amber'
            : 'neutral'
        }>
          {humanize(state)}
        </EvidenceBadge>
      </div>
      {detail && <div className={css(rowDetailStyle(tok))}>{detail}</div>}
    </div>
  )
}

function EvidenceEmpty({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return <div className={css(emptyStyle(tok))}>{children}</div>
}

function EvidenceNotice({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({
      padding: '10px 16px',
      borderBottom: `1px solid ${tok.innerSep}`,
      color: tok.statusAmber,
      fontSize: '10px',
      lineHeight: '16px',
    })}>
      {children}
    </div>
  )
}

function Caveat({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <p className={css({
      margin: 0,
      color: tok.textTertiary,
      fontSize: '10px',
      lineHeight: '16px',
    })}>
      {children}
    </p>
  )
}

function FilterField({
  label,
  ariaLabel,
  value,
  mono = false,
  onChange,
}: {
  label: string
  ariaLabel?: string
  value: string
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
        onChange={(event) => onChange(event.currentTarget.value)}
        className={css({
          ...controlStyle(tok),
          ...(mono ? { fontFamily: FONTS.MONO } : {}),
        })}
      />
    </label>
  )
}

function NumberField({
  label,
  ariaLabel,
  value,
  onChange,
}: {
  label: string
  ariaLabel: string
  value: number
  onChange: (value: number) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css(fieldLabelStyle(tok))}>
      {label}
      <input
        type="number"
        min={0}
        aria-label={ariaLabel}
        value={value}
        onChange={(event) =>
          onChange(Math.max(0, Number(event.currentTarget.value) || 0))}
        className={css(controlStyle(tok))}
      />
    </label>
  )
}

function FilterSelect({
  label,
  ariaLabel,
  value,
  values,
  onChange,
}: {
  label: string
  ariaLabel?: string
  value: string
  values: string[]
  onChange: (value: string) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <label className={css(fieldLabelStyle(tok))}>
      {label}
      <select
        aria-label={ariaLabel ?? label}
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        className={css(controlStyle(tok))}
      >
        {values.map((option) => (
          <option key={option} value={option}>{humanize(option)}</option>
        ))}
      </select>
    </label>
  )
}

function canonicalFilters(
  filters: WorkbenchImpactFilters,
): WorkbenchImpactFilters {
  return Object.fromEntries(
    Object.entries(filters)
      .map(([key, value]) => [key, value?.trim()])
      .filter((entry) => Boolean(entry[1])),
  )
}

function canonicalAnchors(
  anchors: WorkbenchImplementationAnchor[],
): WorkbenchImplementationAnchor[] {
  return anchors.map((anchor) => ({
    repository: anchor.repository.trim(),
    commit: anchor.commit.trim(),
    path: anchor.path.trim(),
    line: anchor.line,
    character: anchor.character,
    encoding: anchor.encoding ?? 'utf-8',
  }))
}

function replaceAt<T>(values: T[], index: number, value: T): T[] {
  const next = [...values]
  next[index] = value
  return next
}

function dispositionIdempotencyKey(
  suggestionID: string,
  input: string,
): string {
  let hash = 2166136261
  for (let index = 0; index < input.length; index += 1) {
    hash ^= input.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return `workbench-ui-${suggestionID.slice(0, 96)}-${(hash >>> 0).toString(16)}`
}

function evidenceFailure(cause: unknown, action: string): EvidenceFailure {
  if (cause instanceof WorkbenchAPIError) {
    if (cause.status === 404) {
      return {
        title: 'Evidence unavailable or permission changed',
        detail: 'The exact Workbench revision is unknown or no longer authorized. No hidden repository or Investigation identity was disclosed.',
        status: cause.status,
      }
    }
    if (cause.status === 409) {
      return {
        title: 'Evidence snapshot changed',
        detail: 'The revision, cursor, evidence snapshot, or active disposition changed. Restart from the first exact page before retrying.',
        status: cause.status,
      }
    }
    if (cause.status === 422) {
      return {
        title: 'Evidence bound reached',
        detail: cause.message,
        status: cause.status,
      }
    }
    return {
      title: `Could not ${action}`,
      detail: cause.message,
      status: cause.status,
    }
  }
  return {
    title: `Could not ${action}`,
    detail: cause instanceof Error ? cause.message : String(cause),
  }
}

function humanize(value: string): string {
  return value.replaceAll('_', ' ').replace(/\b\w/g, (letter) =>
    letter.toUpperCase())
}

function shortDigest(value: string): string {
  return value.length > 15 ? `${value.slice(0, 12)}…` : value
}

function panelStyle(tok: PhebsTokens) {
  return {
    border: `1px solid ${tok.cardBorder}`,
    backgroundColor: tok.pageBg,
  }
}

function panelHeaderStyle(tok: PhebsTokens) {
  return {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    gap: '12px',
    padding: '14px 16px',
    borderBottom: `1px solid ${tok.cardBorder}`,
  }
}

function panelTitleStyle() {
  return {
    margin: '4px 0 0',
    fontSize: '16px',
    lineHeight: '22px',
  }
}

function eyebrowStyle(tok: PhebsTokens) {
  return {
    color: tok.textTertiary,
    fontFamily: FONTS.MONO,
    fontSize: '8px',
    lineHeight: '13px',
    letterSpacing: '0.075em',
    textTransform: 'uppercase' as const,
  }
}

function fieldLabelStyle(tok: PhebsTokens) {
  return {
    display: 'grid',
    gap: '5px',
    color: tok.textSecondary,
    fontSize: '10px',
    fontWeight: 600,
    lineHeight: '15px',
  }
}

function controlStyle(tok: PhebsTokens) {
  return {
    width: '100%',
    minHeight: '34px',
    boxSizing: 'border-box' as const,
    padding: '7px 9px',
    border: `1px solid ${tok.cardBorder}`,
    borderRadius: '0px',
    backgroundColor: tok.pageBg,
    color: tok.textPrimary,
    fontSize: '11px',
    ':focus-visible': {
      outline: `2px solid ${tok.accent}`,
      outlineOffset: '1px',
    },
  }
}

function textareaStyle(tok: PhebsTokens) {
  return {
    ...controlStyle(tok),
    resize: 'vertical' as const,
    lineHeight: '17px',
  }
}

function summaryStyle(tok: PhebsTokens) {
  return {
    padding: '11px 14px',
    color: tok.textSecondary,
    cursor: 'pointer',
    fontSize: '10px',
    fontWeight: 650,
    ':focus-visible': {
      outline: `2px solid ${tok.accent}`,
      outlineOffset: '-2px',
    },
  }
}

function evidenceRowStyle(tok: PhebsTokens) {
  return {
    display: 'grid',
    gridTemplateColumns: 'minmax(190px, 0.45fr) minmax(0, 1fr)',
    gap: '14px',
    alignItems: 'start',
    padding: '12px 16px',
    borderBottom: `1px solid ${tok.innerSep}`,
    '@media screen and (max-width: 620px)': {
      gridTemplateColumns: '1fr',
      gap: '7px',
    },
  }
}

function rowTitleStyle(tok: PhebsTokens) {
  return {
    color: tok.textSecondary,
    fontSize: '11px',
    fontWeight: 650,
    lineHeight: '17px',
    overflowWrap: 'anywhere' as const,
  }
}

function rowDetailStyle(tok: PhebsTokens) {
  return {
    margin: '3px 0 0',
    color: tok.textTertiary,
    fontSize: '10px',
    lineHeight: '16px',
    overflowWrap: 'anywhere' as const,
  }
}

function emptyStyle(tok: PhebsTokens) {
  return {
    padding: '18px',
    color: tok.textTertiary,
    fontSize: '11px',
    lineHeight: '18px',
    textAlign: 'center' as const,
  }
}

function visuallyHiddenStyle() {
  return {
    position: 'absolute' as const,
    width: '1px',
    height: '1px',
    overflow: 'hidden',
    clip: 'rect(0 0 0 0)',
  }
}
