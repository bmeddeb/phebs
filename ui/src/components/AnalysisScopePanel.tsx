import { useMemo, useState, type ReactNode } from 'react'
import { useStyletron } from 'baseui'
import type {
  AnalysisScopeProjection,
  CallerMapGeneration,
  CoverageAnalysisUnit,
  CoverageCertificate,
  CoverageDomainDisposition,
  CoverageDomainOutcomeReceipt,
  CoverageRun,
} from '../api'
import { SectionHelp } from './SectionHelp'
import { FONTS, type PhebsTokens, usePhebsTokens } from '../theme'

const maximumMountedRepositories = 24
const maximumMountedRows = 24
const supportedCoverageSchemas = new Set([
  'coverage-certificate-v1',
  'coverage-certificate-v2',
  'coverage-certificate-v3',
])
const emptyCapabilities = new Set<string>()
const emptyCertificates: CoverageCertificate[] = []
const emptyScopes: AnalysisScopeProjection[] = []
const emptyStatusRows: AnalysisScopeStatusRow[] = []
const emptyGaps: AnalysisScopeGap[] = []

export interface AnalysisScopeStatusRow {
  label: string
  state: string
  detail?: string
}

export interface AnalysisScopeGap {
  repository?: string
  domain?: string
  state: string
  code?: string
  reason?: string
}

export interface AnalysisScopePanelProps {
  id: string
  title?: string
  eyebrow?: string
  headingLevel?: 2 | 3 | 4
  certificate?: CoverageCertificate
  certificates?: CoverageCertificate[]
  scopes?: AnalysisScopeProjection[]
  repository?: string
  callerGeneration?: CallerMapGeneration
  statusRows?: AnalysisScopeStatusRow[]
  gaps?: AnalysisScopeGap[]
  enabledCapabilities?: ReadonlySet<string>
  compact?: boolean
}

interface ScopeUnit {
  name: string
  digest: string
  primaryPaths: string[]
  supportingPaths: string[]
  typedIndexPosture: string
  typedIndexKind?: string
  typedIndexPath?: string
}

interface ScopeRepository {
  repository: string
  commit?: string
  posture: 'whole-repository' | 'focused'
  unit?: ScopeUnit
  runs: CoverageRun[]
}

export function AnalysisScopePanel({
  id,
  title = 'Analysis scope & gaps',
  eyebrow = 'Authority before rows',
  headingLevel = 2,
  certificate,
  certificates = emptyCertificates,
  scopes = emptyScopes,
  repository,
  callerGeneration,
  statusRows = emptyStatusRows,
  gaps = emptyGaps,
  enabledCapabilities = emptyCapabilities,
  compact = false,
}: AnalysisScopePanelProps) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [expandedRepository, setExpandedRepository] = useState('')
  const allCertificates = useMemo(
    () => certificate ? [certificate, ...certificates] : certificates,
    [certificate, certificates],
  )
  const repositories = useMemo(
    () => normalizeRepositories(allCertificates, scopes),
    [allCertificates, scopes],
  )
  const relevantRepositories = repository
    ? repositories.filter((candidate) => candidate.repository === repository)
    : repositories
  const mountedRepositories = relevantRepositories.slice(
    0,
    maximumMountedRepositories,
  )
  const omittedRepositories = Math.max(
    0,
    relevantRepositories.length - mountedRepositories.length,
  )
  const mountedStatusRows = statusRows.slice(0, maximumMountedRows)
  const omittedStatusRows = Math.max(0, statusRows.length - mountedStatusRows.length)
  const mountedGaps = gaps.slice(0, maximumMountedRows)
  const omittedGaps = Math.max(0, gaps.length - mountedGaps.length)
  const runs = relevantRepositories.flatMap((candidate) => candidate.runs)
  const focusedCount = relevantRepositories.filter(
    (candidate) => candidate.posture === 'focused',
  ).length
  const staleCount = runs.filter(
    (run) => run.status === 'published' && !run.fresh,
  ).length
  const refusalCount = runs.filter((run) =>
    run.outcome?.disposition === 'terminal_generation_refusal').length
  const unavailableCount = runs.filter((run) =>
    run.outcome?.disposition === 'unavailable_prerequisite').length
  const retryableCount = runs.filter((run) =>
    run.outcome?.disposition === 'retryable_failure').length
  const failedReplacementCount = runs.filter((run) =>
    Boolean(run.latest_attempt?.failure)).length
  const typedGapCount = relevantRepositories.filter((candidate) =>
    candidate.unit && candidate.unit.typedIndexPosture !== 'unit-bound').length
  const coverageCapabilities = useMemo(() => {
    const next = new Set(enabledCapabilities)
    if (allCertificates.some((candidate) =>
      supportedCoverageSchemas.has(candidate.schema_version))) {
      next.add('coverage-certificate')
    }
    return next
  }, [allCertificates, enabledCapabilities])
  const Heading = `h${headingLevel}` as 'h2' | 'h3' | 'h4'
  const hasCoverage = allCertificates.length > 0
  const hasContent = hasCoverage || relevantRepositories.length > 0 ||
    Boolean(callerGeneration) || statusRows.length > 0 || gaps.length > 0

  if (!hasContent) return null

  return (
    <section
      aria-labelledby={`${id}-heading`}
      data-testid="analysis-scope-panel"
      data-responsive-layout="desktop-columns-mobile-stack"
      className={css({
        marginBottom: compact ? '10px' : '16px',
        border: `1px solid ${tok.cardBorder}`,
        backgroundColor: tok.pageBg,
      })}
    >
      <header className={css({
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'space-between',
        gap: '14px',
        padding: compact ? '10px 12px' : '12px 14px',
        borderBottom: `1px solid ${tok.cardBorder}`,
        backgroundColor: tok.bandBg,
        '@media screen and (max-width: 680px)': {
          flexDirection: 'column',
          gap: '9px',
        },
      })}>
        <div className={css({ minWidth: 0 })}>
          <div className={css(eyebrowStyle(tok))}>{eyebrow}</div>
          <div className={css({
            display: 'flex',
            alignItems: 'center',
            gap: '7px',
            marginTop: '3px',
          })}>
            <Heading id={`${id}-heading`} className={css({
              margin: 0,
              color: tok.textPrimary,
              fontSize: compact ? '13px' : '15px',
              lineHeight: compact ? '19px' : '22px',
              fontWeight: 650,
            })}>
              {title}
            </Heading>
            <SectionHelp
              termId="analysis_scope_and_gaps"
              enabledCapabilities={coverageCapabilities}
              triggerLabel={title}
            />
          </div>
        </div>
        <div className={css({
          display: 'flex',
          justifyContent: 'flex-end',
          gap: '6px',
          flexWrap: 'wrap',
          '@media screen and (max-width: 680px)': { justifyContent: 'flex-start' },
        })}>
          {(relevantRepositories.length > 0 || hasCoverage) && (
            <ScopeChip tone="neutral">
              {relevantRepositories.length} {relevantRepositories.length === 1 ? 'repository' : 'repositories'}
            </ScopeChip>
          )}
          {focusedCount > 0 && <ScopeChip tone="blue">{focusedCount} focused</ScopeChip>}
          {staleCount > 0 && <ScopeChip tone="amber">{staleCount} stale</ScopeChip>}
          {refusalCount > 0 && <ScopeChip tone="red">{refusalCount} refused</ScopeChip>}
          {unavailableCount > 0 && <ScopeChip tone="amber">{unavailableCount} unavailable</ScopeChip>}
          {retryableCount > 0 && <ScopeChip tone="amber">{retryableCount} retrying</ScopeChip>}
          {failedReplacementCount > 0 && (
            <ScopeChip tone="red">
              {failedReplacementCount} failed replacement
              {failedReplacementCount === 1 ? '' : 's'}
            </ScopeChip>
          )}
          {typedGapCount > 0 && <ScopeChip tone="amber">{typedGapCount} typed-index gaps</ScopeChip>}
          {gaps.length > 0 && <ScopeChip tone="amber">{gaps.length} explicit gaps</ScopeChip>}
        </div>
      </header>

      {statusRows.length > 0 && (
        <div aria-label="Scope capability states" className={css(statusGridStyle(tok))}>
          {mountedStatusRows.map((row, index) => (
            <StatusRow key={`${row.label}:${row.state}:${index}`} row={row} />
          ))}
          {omittedStatusRows > 0 && (
            <MountBoundNotice>
              DOM bound reached: {omittedStatusRows} additional capability
              {omittedStatusRows === 1 ? ' state is' : ' states are'} not mounted.
            </MountBoundNotice>
          )}
        </div>
      )}

      {callerGeneration && (
        <CallerPlane generation={callerGeneration} />
      )}

      {(relevantRepositories.length > 0 || hasCoverage) && (
        <details
          data-testid={hasCoverage ? 'coverage-certificate-detail' : 'analysis-scope-detail'}
          className={css({ borderTop: `1px solid ${tok.innerSep}` })}
        >
          <summary className={css({
            minHeight: '40px',
            boxSizing: 'border-box',
            padding: '8px 12px',
            display: 'flex',
            alignItems: 'center',
            gap: '8px',
            cursor: 'pointer',
            color: tok.textSecondary,
            fontSize: '11px',
            ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill },
            ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
          })}>
            <span className={css({ fontWeight: 650 })}>
              {hasCoverage ? 'Coverage certificate' : 'Exact repository scope'}
            </span>
            {hasCoverage && (
              <SectionHelp
                termId="coverage_certificate"
                enabledCapabilities={coverageCapabilities}
              />
            )}
            <span className={css({
              minWidth: 0,
              marginLeft: 'auto',
              color: tok.gutter,
              fontFamily: FONTS.MONO,
              fontSize: '9px',
              overflowWrap: 'anywhere',
              textAlign: 'right',
              '@media screen and (max-width: 620px)': { display: 'none' },
            })}>
              {hasCoverage
                ? certificateIdentitySummary(allCertificates)
                : `${focusedCount} focused / ${relevantRepositories.length - focusedCount} whole`}
            </span>
          </summary>
          <div className={css({ borderTop: `1px solid ${tok.innerSep}` })}>
            {mountedRepositories.map((candidate, index) => {
              const expanded = expandedRepository === candidate.repository
              const detailID = `${id}-repository-${index}-${safeID(candidate.repository)}`
              return (
                <div key={candidate.repository} className={css({
                  borderBottom: `1px solid ${tok.innerSep}`,
                  ':last-child': { borderBottom: 'none' },
                })}>
                  <button
                    type="button"
                    aria-expanded={expanded}
                    aria-controls={expanded ? detailID : undefined}
                    onClick={() => setExpandedRepository(
                      expanded ? '' : candidate.repository,
                    )}
                    className={css({
                      width: '100%',
                      minHeight: '44px',
                      display: 'grid',
                      gridTemplateColumns: 'minmax(0, 1fr) auto',
                      gap: '12px',
                      alignItems: 'center',
                      padding: '8px 12px',
                      border: 'none',
                      backgroundColor: 'transparent',
                      color: tok.textPrimary,
                      textAlign: 'left',
                      cursor: 'pointer',
                      ':hover': { backgroundColor: tok.hoverFill },
                      ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
                      '@media screen and (max-width: 620px)': {
                        gridTemplateColumns: '1fr',
                        gap: '4px',
                      },
                    })}
                  >
                    <span className={css({ minWidth: 0 })}>
                      <span className={css({
                        display: 'block',
                        overflowWrap: 'anywhere',
                        fontSize: '11px',
                        fontWeight: 650,
                      })}>{candidate.repository}</span>
                      <span className={css({
                        display: 'block',
                        marginTop: '2px',
                        color: tok.textTertiary,
                        fontSize: '10px',
                      })}>
                        {candidate.unit
                          ? `service unit ${candidate.unit.name}`
                          : 'whole-repository scope'}
                      </span>
                    </span>
                    <span className={css({
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'flex-end',
                      gap: '6px',
                      flexWrap: 'wrap',
                      '@media screen and (max-width: 620px)': { justifyContent: 'flex-start' },
                    })}>
                      <ScopeChip tone={candidate.posture === 'focused' ? 'blue' : 'neutral'}>
                        {candidate.posture}
                      </ScopeChip>
                      {candidate.runs.some((run) => !run.fresh) && (
                        <ScopeChip tone="amber">stale or unpublished</ScopeChip>
                      )}
                      <span aria-hidden="true" className={css({ color: tok.gutter })}>
                        {expanded ? '▾' : '▸'}
                      </span>
                    </span>
                  </button>
                  {expanded && (
                    <RepositoryDetail id={detailID} repository={candidate} />
                  )}
                </div>
              )
            })}
            {mountedRepositories.length === 0 && hasCoverage && (
              <div
                role="status"
                data-testid="coverage-certificate-empty-universe"
                className={css({
                  padding: '10px 12px',
                  color: tok.textSecondary,
                  fontSize: '10px',
                  lineHeight: '16px',
                })}
              >
                Coverage certificate reports 0 repository rows for this answer.
              </div>
            )}
            {omittedRepositories > 0 && (
              <div role="status" className={css({
                padding: '9px 12px',
                color: tok.statusAmber,
                fontSize: '10px',
                lineHeight: '16px',
              })}>
                DOM bound reached: {omittedRepositories}{' '}
                {omittedRepositories === 1
                  ? 'additional repository is'
                  : 'additional repositories are'} not mounted. Narrow
                this surface by repository to inspect its exact scope.
              </div>
            )}
          </div>
        </details>
      )}

      {gaps.length > 0 && (
        <div aria-label="Explicit analysis gaps" className={css({
          display: 'grid',
          gap: '7px',
          padding: '10px 12px',
          borderTop: `1px solid ${tok.innerSep}`,
        })}>
          {mountedGaps.map((gap, index) => (
            <GapStatus
              key={`${gap.repository}:${gap.domain}:${gap.code}:${index}`}
              gap={gap}
            />
          ))}
          {omittedGaps > 0 && (
            <MountBoundNotice>
              DOM bound reached: {omittedGaps} additional analysis
              {omittedGaps === 1 ? ' gap is' : ' gaps are'} not mounted.
            </MountBoundNotice>
          )}
        </div>
      )}
    </section>
  )
}

function normalizeRepositories(
  certificates: CoverageCertificate[],
  scopes: AnalysisScopeProjection[],
): ScopeRepository[] {
  const repositories = new Map<string, ScopeRepository>()
  for (const certificate of certificates) {
    for (const source of certificate.repositories) {
      const current = repositories.get(source.repository)
      const runs = mergeRuns(current?.runs ?? [], source.runs)
      const posture = source.scope_posture ??
        (source.analysis_unit ? 'focused' : current?.posture ?? 'whole-repository')
      repositories.set(source.repository, {
        repository: source.repository,
        commit: source.indexed_commit || current?.commit,
        posture,
        unit: source.analysis_unit
          ? unitFromCertificate(source.analysis_unit)
          : current?.unit,
        runs,
      })
    }
  }
  for (const scope of scopes) {
    const current = repositories.get(scope.repository)
    repositories.set(scope.repository, {
      repository: scope.repository,
      commit: scope.commit || current?.commit,
      posture: scope.scope_posture,
      unit: scope.analysis_unit ? {
        name: scope.analysis_unit.name,
        digest: scope.analysis_unit.digest,
        primaryPaths: pathList(scope.analysis_unit.primary_paths),
        supportingPaths: pathList(scope.analysis_unit.supporting_paths),
        typedIndexPosture: scope.analysis_unit.typed_index_posture,
        typedIndexKind: scope.analysis_unit.typed_index?.kind,
        typedIndexPath: scope.analysis_unit.typed_index?.path,
      } : current?.unit,
      runs: current?.runs ?? [],
    })
  }
  return [...repositories.values()].sort((left, right) =>
    left.repository.localeCompare(right.repository))
}

function unitFromCertificate(unit: CoverageAnalysisUnit): ScopeUnit {
  return {
    name: unit.name,
    digest: unit.digest,
    primaryPaths: pathList(unit.primary_paths),
    supportingPaths: pathList(unit.supporting_paths),
    typedIndexPosture: unit.typed_index_posture,
    typedIndexKind: unit.typed_index_kind,
    typedIndexPath: unit.typed_index_path,
  }
}

function mergeRuns(left: CoverageRun[], right: CoverageRun[]): CoverageRun[] {
  const runs = new Map<string, CoverageRun>()
  for (const run of [...left, ...right]) {
    const key = [
      run.domain,
      run.run_id,
      run.outcome?.generation_digest,
      run.unit_digest,
    ].join('\u0000')
    runs.set(key, run)
  }
  return [...runs.values()].sort((first, second) =>
    first.domain.localeCompare(second.domain))
}

function pathList(paths: readonly string[] | null | undefined): string[] {
  return paths ? [...paths] : []
}

function RepositoryDetail({
  id,
  repository,
}: {
  id: string
  repository: ScopeRepository
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div id={id} className={css({
      display: 'grid',
      gridTemplateColumns: repository.runs.length > 0
        ? 'minmax(220px, 0.8fr) minmax(0, 1.2fr)'
        : '1fr',
      borderTop: `1px solid ${tok.innerSep}`,
      backgroundColor: tok.bandBg,
      '@media screen and (max-width: 760px)': { gridTemplateColumns: '1fr' },
    })}>
      <div className={css({ minWidth: 0, padding: '12px' })}>
        <div className={css(eyebrowStyle(tok))}>Exact selected scope</div>
        {repository.unit ? (
          <>
            <div className={css({
              marginTop: '5px',
              color: tok.textPrimary,
              fontSize: '12px',
              fontWeight: 650,
            })}>{repository.unit.name}</div>
            <IdentityLine label="unit" value={repository.unit.digest} />
            <IdentityLine label="commit" value={repository.commit} />
            <PathBlock label="Primary paths" paths={repository.unit.primaryPaths} />
            <PathBlock label="Supporting paths" paths={repository.unit.supportingPaths} />
            {repository.posture === 'whole-repository' && (
              <div className={css({
                marginTop: '9px',
                color: tok.statusAmber,
                fontSize: '10px',
                lineHeight: '15px',
              })}>
                This active unit still uses the retained whole-repository search
                posture. A focused replacement has not become current.
              </div>
            )}
            <div className={css({ marginTop: '9px' })}>
              <ScopeChip tone={
                repository.unit.typedIndexPosture === 'unit-bound'
                  ? 'green'
                  : 'amber'
              }>
                typed index {humanize(repository.unit.typedIndexPosture)}
              </ScopeChip>
              {repository.unit.typedIndexPath && (
                <div className={css({
                  marginTop: '4px',
                  color: tok.textTertiary,
                  fontFamily: FONTS.MONO,
                  fontSize: '9px',
                  overflowWrap: 'anywhere',
                })}>
                  {repository.unit.typedIndexKind} · {repository.unit.typedIndexPath}
                </div>
              )}
              {repository.unit.typedIndexPosture !== 'unit-bound' && (
                <div className={css({
                  marginTop: '4px',
                  color: tok.statusAmber,
                  fontSize: '10px',
                  lineHeight: '15px',
                })}>
                  Typed navigation is not bound to this unit; typed-index results
                  may be unavailable. This does not widen focused source scope.
                </div>
              )}
            </div>
          </>
        ) : (
          <div className={css({
            marginTop: '5px',
            color: tok.textSecondary,
            fontSize: '11px',
            lineHeight: '17px',
          })}>
            Whole-repository search and local-evidence scope. No focused service
            unit is committed for this repository.
            <IdentityLine label="commit" value={repository.commit} />
          </div>
        )}
      </div>
      {repository.runs.length > 0 && (
        <div className={css({
          minWidth: 0,
          padding: '12px',
          borderLeft: `1px solid ${tok.innerSep}`,
          '@media screen and (max-width: 760px)': {
            borderLeft: 'none',
            borderTop: `1px solid ${tok.innerSep}`,
          },
        })}>
          <div className={css(eyebrowStyle(tok))}>Durable domain outcomes</div>
          <div className={css({ display: 'grid', gap: '8px', marginTop: '7px' })}>
            {repository.runs.map((run, index) => (
              <DomainStatus
                key={`${run.domain}:${run.run_id}:${index}`}
                run={run}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function PathBlock({ label, paths }: { label: string; paths: string[] }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ marginTop: '9px' })}>
      <div className={css(eyebrowStyle(tok))}>{label} · {paths.length}</div>
      <pre className={css({
        margin: '4px 0 0',
        padding: '7px 8px',
        maxHeight: '150px',
        overflow: 'auto',
        whiteSpace: 'pre-wrap',
        overflowWrap: 'anywhere',
        border: `1px solid ${tok.innerSep}`,
        backgroundColor: tok.pageBg,
        color: tok.textSecondary,
        fontFamily: FONTS.MONO,
        fontSize: '9px',
        lineHeight: '14px',
      })}>{paths.length > 0 ? paths.join('\n') : 'none selected'}</pre>
    </div>
  )
}

function DomainStatus({ run }: { run: CoverageRun }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const disposition = run.outcome?.disposition
  const recordedFailureCount = run.failures?.length ?? 0
  const baseLabel = disposition ?? (run.status === 'published'
    ? run.fresh ? 'published' : 'stale publication'
    : run.latest_attempt?.failure ? 'failed replacement' : 'unpublished')
  const label = recordedFailureCount > 0
    ? `${baseLabel} · ${recordedFailureCount} recorded ${recordedFailureCount === 1 ? 'failure' : 'failures'}`
    : baseLabel
  const reason = run.outcome?.receipt?.reason || run.latest_attempt?.failure
  const candidate = run.candidate_scope
  return (
    <div className={css({
      minWidth: 0,
      padding: '8px 9px',
      border: `1px solid ${tok.innerSep}`,
      backgroundColor: tok.pageBg,
    })}>
      <div className={css({
        display: 'flex',
        alignItems: 'center',
        gap: '6px',
        flexWrap: 'wrap',
      })}>
        <span className={css({
          color: tok.textPrimary,
          fontFamily: FONTS.MONO,
          fontSize: '10px',
          fontWeight: 650,
        })}>{run.domain}</span>
        <ScopeChip tone={domainTone(disposition, run)}>{humanize(label)}</ScopeChip>
        {run.evidence_scope_posture && (
          <ScopeChip tone={run.evidence_scope_posture === 'focused-local' ? 'blue' : 'neutral'}>
            {run.evidence_scope_posture}
          </ScopeChip>
        )}
        {run.outcome?.candidate_control_failure && (
          <ScopeChip tone="red">candidate control refusal</ScopeChip>
        )}
      </div>
      {reason && <div className={css({
        marginTop: '4px',
        color: tok.textTertiary,
        fontSize: '10px',
        lineHeight: '15px',
        overflowWrap: 'anywhere',
      })}>{reason}</div>}
      {recordedFailureCount > 0 && <div className={css({
        marginTop: '4px',
        color: tok.statusAmber,
        fontSize: '10px',
        lineHeight: '15px',
        overflowWrap: 'anywhere',
      })}>
        Retained failure {recordedFailureCount === 1 ? 'class' : 'classes'}:{' '}
        {run.failures?.join('; ')}
      </div>}
      {candidate && (
        <div className={css({
          display: 'flex',
          gap: '5px 10px',
          flexWrap: 'wrap',
          marginTop: '6px',
          color: tok.textTertiary,
          fontFamily: FONTS.MONO,
          fontSize: '9px',
        })}>
          <span>{candidate.plane} plane</span>
          {candidate.base_source_file_count !== undefined && (
            <span>{candidate.base_source_file_count} base files</span>
          )}
          {candidate.excluded_go_test_file_count !== undefined && (
            <span>{candidate.excluded_go_test_file_count} excluded go_test</span>
          )}
          {candidate.typed_input && (
            <span className={css({
              color: candidate.typed_input.present ? tok.statusGreen : tok.statusAmber,
            })}>
              typed input {candidate.typed_input.present ? 'present' : 'missing'}
            </span>
          )}
        </div>
      )}
      {run.outcome && (
        <ReceiptSummary
          state={run.outcome.receipt_state}
          receipt={run.outcome.receipt}
        />
      )}
    </div>
  )
}

function ReceiptSummary({
  state,
  receipt,
}: {
  state: 'full' | 'schema_only' | 'legacy_exclusion_shape'
  receipt?: CoverageDomainOutcomeReceipt
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  // legacy_exclusion_shape receipts carry full recorded counts and timings;
  // only the excluded-source/SCIP counters predate their recording. They must
  // render as disclosed work, not as an absent receipt.
  if ((state !== 'full' && state !== 'legacy_exclusion_shape') || !receipt) {
    return (
      <div className={css({
        marginTop: '6px',
        color: tok.statusAmber,
        fontSize: '9px',
        lineHeight: '14px',
      })}>
        Schema-only bounded receipt; work counts and timings are unavailable.
      </div>
    )
  }
  return (
    <details className={css({ marginTop: '6px' })}>
      <summary className={css({
        cursor: 'pointer',
        color: tok.textSecondary,
        fontSize: '9px',
        ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
      })}>Bounded domain receipt</summary>
      <div className={css({
        display: 'grid',
        gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
        gap: '4px 12px',
        marginTop: '6px',
        color: tok.textTertiary,
        fontFamily: FONTS.MONO,
        fontSize: '9px',
        lineHeight: '14px',
        '@media screen and (max-width: 520px)': { gridTemplateColumns: '1fr' },
      })}>
        <span>{receipt.counts.candidate_files} candidate files</span>
        <span>{receipt.counts.opened_source_files} opened source files</span>
        <span>{receipt.counts.assertions} assertions</span>
        <span>{receipt.counts.unresolved} unresolved</span>
        <span>{receipt.bytes.opened_source} opened bytes</span>
        <span>{receipt.extractor_ms} ms extractor</span>
        <span>{receipt.staging_ms} ms staging</span>
        <span>limit {receipt.limits.domain_wall_ms} ms/domain</span>
      </div>
    </details>
  )
}

function CallerPlane({ generation }: { generation: CallerMapGeneration }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const progress = generation.partition_progress
  const recordCounts = generation.record_counts
  return (
    <div className={css({
      display: 'grid',
      gridTemplateColumns: 'minmax(0, 1fr) auto',
      gap: '12px',
      alignItems: 'center',
      padding: '10px 12px',
      borderTop: `1px solid ${tok.innerSep}`,
      '@media screen and (max-width: 620px)': { gridTemplateColumns: '1fr' },
    })}>
      <div className={css({ minWidth: 0 })}>
        <div className={css({ display: 'flex', gap: '6px', flexWrap: 'wrap', alignItems: 'center' })}>
          <ScopeChip tone={generation.state === 'current' ? 'green' : 'amber'}>
            {generation.state}
          </ScopeChip>
          <span className={css({ color: tok.textPrimary, fontSize: '11px', fontWeight: 650 })}>
            Repository-overlay callers
          </span>
          {progress && <ScopeChip tone={progress.state === 'complete' ? 'green' : 'amber'}>
            partitions {progress.state}
          </ScopeChip>}
        </div>
        {generation.reason && <div className={css({
          marginTop: '4px',
          color: tok.textTertiary,
          fontSize: '10px',
          lineHeight: '15px',
        })}>{generation.reason}</div>}
      </div>
      <div className={css({
        minWidth: 0,
        maxWidth: '100%',
        display: 'flex',
        justifyContent: 'flex-end',
        gap: '5px 12px',
        flexWrap: 'wrap',
        color: tok.textTertiary,
        fontFamily: FONTS.MONO,
        fontSize: '9px',
        overflowWrap: 'anywhere',
        '@media screen and (max-width: 620px)': { justifyContent: 'flex-start' },
      })}>
        <div data-testid="caller-generation-identity">
          <div>{generation.repository}</div>
          <div>
            {generation.state} · revision{' '}
            {generation.publication_revision ?? 'unavailable'}
          </div>
          {generation.commit && <div>commit {generation.commit}</div>}
          {generation.unit_digest && <div>unit {generation.unit_digest}</div>}
          {generation.generation_digest && (
            <div>generation {generation.generation_digest}</div>
          )}
        </div>
        {recordCounts && (
          <span>{recordCounts.candidate_records} candidates</span>
        )}
        {recordCounts && (
          <span>{recordCounts.base_records} base</span>
        )}
        {(recordCounts || generation.excluded_go_test_records !== undefined) && (
          <span>
            {recordCounts?.excluded_go_test_records ?? generation.excluded_go_test_records}
            {' '}excluded go_test
          </span>
        )}
        {progress && progress.state !== 'unavailable' && (
          <span>
            {progress.settled_pair_count}
            /{progress.total_pair_count ?? '?'}
            {' '}settled · {progress.succeeded_pair_count} succeeded ·{' '}
            {progress.refused_pair_count} refused
          </span>
        )}
      </div>
    </div>
  )
}

function StatusRow({ row }: { row: AnalysisScopeStatusRow }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({ minWidth: 0 })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap' })}>
        <span className={css({ color: tok.textPrimary, fontSize: '10px', fontWeight: 650 })}>
          {row.label}
        </span>
        <ScopeChip tone={statusTone(row.state)}>{humanize(row.state)}</ScopeChip>
      </div>
      {row.detail && <div className={css({
        marginTop: '3px',
        color: tok.textTertiary,
        fontSize: '9px',
        lineHeight: '14px',
        overflowWrap: 'anywhere',
      })}>{row.detail}</div>}
    </div>
  )
}

function GapStatus({ gap }: { gap: AnalysisScopeGap }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const subject = [gap.repository, gap.domain].filter(Boolean).join(' · ') ||
    'Analysis gap'
  return (
    <div className={css({ minWidth: 0 })}>
      <div className={css({
        display: 'flex',
        alignItems: 'center',
        gap: '6px',
        flexWrap: 'wrap',
      })}>
        <span className={css({
          color: tok.textPrimary,
          fontSize: '10px',
          fontWeight: 650,
        })}>{subject}</span>
        {gap.code && (
          <code className={css({
            color: tok.textTertiary,
            fontFamily: FONTS.MONO,
            fontSize: '9px',
          })}>{gap.code}</code>
        )}
        <ScopeChip tone={gapTone(gap.state)}>{humanize(gap.state)}</ScopeChip>
      </div>
      {gap.reason && <div className={css({
        marginTop: '3px',
        color: tok.textTertiary,
        fontSize: '9px',
        lineHeight: '14px',
        overflowWrap: 'anywhere',
      })}>{gap.reason}</div>}
    </div>
  )
}

function MountBoundNotice({ children }: { children: ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div role="status" className={css({
      color: tok.statusAmber,
      fontSize: '9px',
      lineHeight: '14px',
    })}>{children}</div>
  )
}

function IdentityLine({ label, value }: { label: string; value?: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  if (!value) return null
  return (
    <div className={css({
      marginTop: '3px',
      color: tok.textTertiary,
      fontFamily: FONTS.MONO,
      fontSize: '9px',
      overflowWrap: 'anywhere',
    })}>{label} {value}</div>
  )
}

function ScopeChip({
  children,
  tone,
}: {
  children: ReactNode
  tone: 'green' | 'blue' | 'amber' | 'red' | 'neutral'
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const color = tone === 'green' ? tok.statusGreen
    : tone === 'blue' ? tok.statusBlue
      : tone === 'amber' ? tok.statusAmber
        : tone === 'red' ? tok.statusRed
          : tok.textTertiary
  return <span data-tone={tone} className={css({
    display: 'inline-flex',
    alignItems: 'center',
    minHeight: '18px',
    padding: '1px 6px',
    border: `1px solid ${color}`,
    color,
    fontSize: '9px',
    lineHeight: '14px',
    fontWeight: 650,
    whiteSpace: 'nowrap',
  })}>{children}</span>
}

function domainTone(
  disposition: CoverageDomainDisposition | undefined,
  run: CoverageRun,
): 'green' | 'amber' | 'red' | 'neutral' {
  if (disposition === 'terminal_generation_refusal') return 'red'
  if (run.latest_attempt?.failure) return 'red'
  if ((run.failures?.length ?? 0) > 0) return 'amber'
  if (disposition === 'published' && run.fresh) return 'green'
  if (disposition || !run.fresh) return 'amber'
  return run.status === 'published' ? 'green' : 'neutral'
}

function gapTone(state: string): 'amber' | 'red' {
  if (state === 'failed' || state === 'terminal_generation_refusal') return 'red'
  return 'amber'
}

function statusTone(state: string): 'green' | 'amber' | 'red' | 'neutral' {
  if (state === 'enabled' || state === 'available' || state === 'current' ||
    state === 'covered' || state === 'exact') return 'green'
  if (state === 'failed' || state === 'terminal_generation_refusal') return 'red'
  if (state === 'stale' || state === 'unsupported' || state === 'processing' || state === 'unavailable') return 'amber'
  return 'neutral'
}

function shortIdentity(value: string): string {
  if (value.length <= 22) return value
  return `${value.slice(0, 14)}…${value.slice(-6)}`
}

function certificateIdentitySummary(certificates: CoverageCertificate[]): string {
  const mounted = certificates.slice(0, 3)
    .map((candidate) => shortIdentity(candidate.digest)).join(' · ')
  const omitted = certificates.length - 3
  return omitted > 0 ? `${mounted} · +${omitted}` : mounted
}

function safeID(value: string): string {
  return value.replaceAll(/[^A-Za-z0-9_-]/g, '-')
}

function humanize(value: string): string {
  return value.replaceAll('_', ' ').replaceAll('-', ' ')
}

function eyebrowStyle(tok: PhebsTokens) {
  return {
    color: tok.textTertiary,
    fontFamily: FONTS.MONO,
    fontSize: '8px',
    lineHeight: '13px',
    letterSpacing: '0.05em',
    textTransform: 'uppercase' as const,
  }
}

function statusGridStyle(tok: PhebsTokens) {
  return {
    display: 'grid',
    gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
    gap: '10px',
    padding: '10px 12px',
    borderTop: `1px solid ${tok.innerSep}`,
    '@media screen and (max-width: 760px)': { gridTemplateColumns: '1fr' },
  }
}
