import { useStyletron } from 'baseui'
import type {
  ContractCatalogOperation,
  ContractCatalogSource,
} from '../api'
import { href } from '../router'
import { FONTS, usePhebsTokens } from '../theme'
import {
  layoutDependencyEdges,
  relationshipName,
  type PositionedDependencyEdge,
} from './contractDependency'

export default function ContractDependencyMap({
  operation,
}: {
  operation: ContractCatalogOperation
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const layout = layoutDependencyEdges(operation)
  const edges = layout.edges
  const complete = !operation.relationships_truncated

  return (
    <section aria-labelledby="focused-dependency-heading">
      <div className={css({
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'space-between',
        gap: '12px',
        flexWrap: 'wrap',
        marginBottom: '9px',
      })}>
        <div>
          <h3 id="focused-dependency-heading" className={css({
            margin: 0,
            color: tok.textPrimary,
            fontSize: '13px',
            lineHeight: '20px',
            fontWeight: 650,
          })}>
            Focused dependency neighborhood
          </h3>
          <p className={css({
            margin: '3px 0 0',
            maxWidth: '760px',
            color: tok.textTertiary,
            fontSize: '10px',
            lineHeight: '16px',
          })}>
            One source-evidence hop around this declaration. Registration,
            name-bound caller, and abstention rows are not runtime edges.
          </p>
        </div>
        <span className={css({
          color: tok.textTertiary,
          fontFamily: FONTS.MONO,
          fontSize: '10px',
          lineHeight: '16px',
        })}>
          {edges.length} edges · {complete ? 'complete bounded set' : 'truncated bounded set'}
        </span>
      </div>

      {edges.length === 0 ? (
        <div
          role="status"
          data-testid="dependency-map-empty"
          className={css({
            padding: '15px 16px',
            border: `1px solid ${tok.cardBorder}`,
            backgroundColor: tok.bandBg,
            color: tok.textSecondary,
            fontSize: '11px',
            lineHeight: '18px',
          })}
        >
          No implementation, caller, or unresolved-candidate edge was returned
          for this operation within {operation.coverage.repository_count} visible
          repositories and coverage {shortDigest(operation.coverage_digest)}.
          {' '}
          {complete
            ? 'The bounded relationship set is complete; this is still not evidence of runtime absence.'
            : 'The bounded relationship set is truncated, so omitted rows may exist.'}
        </div>
      ) : (
        <>
          <div
            data-testid="dependency-map-diagram"
            data-responsive-layout="desktop-diagram-mobile-table"
            className={css({
              overflowX: 'auto',
              border: `1px solid ${tok.cardBorder}`,
              backgroundColor: tok.bandBg,
              '@media screen and (max-width: 760px)': { display: 'none' },
            })}
          >
            <svg
              role="img"
              aria-labelledby="dependency-map-title dependency-map-description"
              viewBox={`0 0 ${layout.width} ${layout.height}`}
              className={css({
                display: 'block',
                width: '100%',
                minWidth: '760px',
                height: 'auto',
              })}
            >
              <title id="dependency-map-title">
                One-hop source-evidence map for {operation.operation}
              </title>
              <desc id="dependency-map-description">
                Registration evidence appears left of the declared operation.
                Caller evidence and unresolved candidates appear right. The
                accessible table following this diagram contains the same edge
                identities and labels.
              </desc>
              <rect
                x={layout.centerX - 135}
                y={layout.centerY - 38}
                width="270"
                height="76"
                rx="3"
                fill={tok.pageBg}
                stroke={tok.accent}
                strokeWidth="2"
              />
              <text
                x={layout.centerX}
                y={layout.centerY - 7}
                textAnchor="middle"
                fill={tok.textTertiary}
                fontSize="10"
                fontFamily={FONTS.MONO}
              >
                DECLARED OPERATION
              </text>
              <text
                x={layout.centerX}
                y={layout.centerY + 13}
                textAnchor="middle"
                fill={tok.textPrimary}
                fontSize="12"
                fontWeight="600"
                fontFamily={FONTS.MONO}
              >
                {truncate(operation.operation, 36)}
              </text>
              {layout.edges.map((edge) => (
                <DependencyDiagramEdge
                  key={edge.id}
                  edge={edge}
                  centerX={layout.centerX}
                  centerY={layout.centerY}
                />
              ))}
            </svg>
          </div>
          <div
            data-testid="dependency-map-mobile-summary"
            className={css({
              display: 'none',
              padding: '9px 11px',
              border: `1px solid ${tok.cardBorder}`,
              borderBottom: 'none',
              backgroundColor: tok.bandBg,
              color: tok.textSecondary,
              fontSize: '10px',
              lineHeight: '16px',
              '@media screen and (max-width: 760px)': { display: 'block' },
            })}
          >
            Compact view: {edges.length} one-hop source-evidence edges. The
            authoritative table below preserves every identity and label.
          </div>
        </>
      )}

      <div className={css({
        marginTop: edges.length === 0 ? '10px' : '12px',
        overflowX: 'auto',
      })}>
        <table
          aria-label="Authoritative focused dependency relationships"
          className={css({
            width: '100%',
            minWidth: '760px',
            borderCollapse: 'collapse',
          })}
        >
          <caption className={css({
            padding: '0 0 7px',
            color: tok.textTertiary,
            fontSize: '10px',
            lineHeight: '16px',
            textAlign: 'left',
          })}>
            Authoritative relationship table · exact edge identities rendered
            in the diagram
          </caption>
          <thead>
            <tr>
              <DependencyHeader>Relationship</DependencyHeader>
              <DependencyHeader>Pinned source evidence</DependencyHeader>
              <DependencyHeader>Classification</DependencyHeader>
              <DependencyHeader>Own lineage / tier</DependencyHeader>
            </tr>
          </thead>
          <tbody>
            {edges.map((edge) => (
              <tr
                key={edge.id}
                data-testid="dependency-table-edge"
                data-edge-id={edge.id}
                className={css({
                  borderBottom: `1px solid ${tok.innerSep}`,
                  ':hover': { backgroundColor: tok.hoverFill },
                })}
              >
                <DependencyCell>
                  <span className={css({
                    color: tok.textPrimary,
                    fontSize: '11px',
                    fontWeight: 600,
                  })}>
                    {relationshipName(edge.kind)}
                  </span>
                  <span className={css({
                    display: 'block',
                    marginTop: '2px',
                    color: tok.textTertiary,
                    fontFamily: FONTS.MONO,
                    fontSize: '9px',
                    overflowWrap: 'anywhere',
                  })}>
                    {edge.id}
                  </span>
                </DependencyCell>
                <DependencyCell>
                  <a
                    href={sourceHref(edge.source)}
                    className={css({
                      color: tok.accent,
                      fontSize: '11px',
                      textDecoration: 'none',
                      ':hover': { textDecoration: 'underline' },
                      ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '2px' },
                    })}
                  >
                    {edge.label}
                  </a>
                </DependencyCell>
                <DependencyCell>
                  {humanize(edge.classification)}
                  {edge.reason ? (
                    <span className={css({
                      display: 'block',
                      marginTop: '2px',
                      color: tok.textTertiary,
                      fontSize: '10px',
                    })}>
                      {humanize(edge.reason)}
                    </span>
                  ) : null}
                </DependencyCell>
                <DependencyCell mono>
                  {edge.lineage || 'unresolved'}
                  <br />
                  {edge.tier}{edge.codeRole ? ` · ${edge.codeRole}` : ''}
                </DependencyCell>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className={css({
        marginTop: '8px',
        color: tok.textTertiary,
        fontSize: '10px',
        lineHeight: '16px',
      })}>
        Scope: {operation.coverage.repository_count} visible repositories ·
        coverage {shortDigest(operation.coverage_digest)} ·
        {complete ? ' relationship projection complete within its bound' : ' relationship projection truncated'}
      </div>
    </section>
  )
}

function DependencyDiagramEdge({
  edge,
  centerX,
  centerY,
}: {
  edge: PositionedDependencyEdge
  centerX: number
  centerY: number
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const nodeWidth = 240
  const nodeHeight = 48
  const nodeCenterY = edge.y + nodeHeight / 2
  const left = edge.side === 'left'
  const lineStartX = left ? edge.x + nodeWidth : edge.x
  const lineEndX = left ? centerX - 135 : centerX + 135
  const tone = edge.kind === 'implementation'
    ? tok.status.current.solid
    : edge.kind === 'caller'
      ? tok.status.unavailable.solid
      : tok.status.stale.solid
  return (
    <a
      href={sourceHref(edge.source)}
      data-testid="dependency-diagram-edge"
      data-edge-id={edge.id}
      aria-label={`${edge.label}; ${humanize(edge.classification)}; open pinned source`}
      className={css({
        ':focus': { outline: `3px solid ${tok.accent}`, outlineOffset: '2px' },
      })}
    >
      <line
        x1={lineStartX}
        y1={nodeCenterY}
        x2={lineEndX}
        y2={centerY}
        stroke={tone}
        strokeWidth="1.5"
        strokeDasharray={edge.kind === 'unresolved_candidate' ? '5 4' : undefined}
      />
      <circle cx={lineEndX} cy={centerY} r="3" fill={tone} />
      <rect
        x={edge.x}
        y={edge.y}
        width={nodeWidth}
        height={nodeHeight}
        rx="3"
        fill={tok.pageBg}
        stroke={tone}
        strokeWidth="1.5"
      />
      <text
        x={edge.x + 10}
        y={edge.y + 18}
        fill={tok.textPrimary}
        fontSize="10"
        fontWeight="600"
      >
        {relationshipName(edge.kind)} · {humanize(edge.classification)}
      </text>
      <text
        x={edge.x + 10}
        y={edge.y + 35}
        fill={tok.textTertiary}
        fontSize="9"
        fontFamily={FONTS.MONO}
      >
        {truncate(edge.label, 43)}
      </text>
    </a>
  )
}

function DependencyHeader({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <th scope="col" className={css({
      padding: '7px 9px',
      borderBottom: `1px solid ${tok.cardBorder}`,
      backgroundColor: tok.bandBg,
      color: tok.textTertiary,
      fontSize: '9px',
      fontWeight: 650,
      letterSpacing: '0.04em',
      textAlign: 'left',
      textTransform: 'uppercase',
    })}>
      {children}
    </th>
  )
}

function DependencyCell({
  children,
  mono = false,
}: {
  children: React.ReactNode
  mono?: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <td className={css({
      padding: '8px 9px',
      color: tok.textSecondary,
      fontFamily: mono ? FONTS.MONO : 'inherit',
      fontSize: mono ? '9px' : '10px',
      lineHeight: '15px',
      verticalAlign: 'top',
      overflowWrap: 'anywhere',
    })}>
      {children}
    </td>
  )
}

function sourceHref(source: ContractCatalogSource): string {
  return href('/file', {
    repo: source.repository,
    path: source.path,
    ref: source.commit,
    L: String(source.start_line),
  })
}

function shortDigest(value: string): string {
  if (!value) return 'unavailable'
  return value.length > 22 ? `${value.slice(0, 15)}…${value.slice(-6)}` : value
}

function truncate(value: string, maximum: number): string {
  return value.length > maximum ? `${value.slice(0, maximum - 1)}…` : value
}

function humanize(value: string): string {
  return value.toLowerCase().replaceAll('_', ' ')
}
