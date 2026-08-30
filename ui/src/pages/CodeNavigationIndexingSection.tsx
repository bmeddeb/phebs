import { useStyletron } from 'baseui'
import { StateNotice, StatusChip } from '../components/kit'
import { FONTS, usePhebsTokens } from '../theme'

/**
 * Capability-dark presentation boundary for managed code-navigation indexing.
 *
 * This component intentionally has no inputs or actions: provider authority is
 * not registered yet, so the Settings surface may only state the build truth.
 */
export function CodeNavigationIndexingSection() {
  const [css] = useStyletron()
  const tok = usePhebsTokens()

  return (
    <section
      id="code-navigation-indexing"
      aria-labelledby="code-navigation-indexing-heading"
      data-responsive-layout="desktop-columns-mobile-stack"
      className={css({ marginBottom: '32px' })}
    >
      <header className={css({ marginBottom: '12px' })}>
        <h1
          id="code-navigation-indexing-heading"
          className={css({
            margin: 0,
            color: tok.textPrimary,
            fontSize: '20px',
            lineHeight: '28px',
            fontWeight: 600,
          })}
        >
          Code navigation indexing
        </h1>
        <p className={css({
          margin: '4px 0 0',
          color: tok.textTertiary,
          fontSize: '12px',
          lineHeight: '18px',
        })}>
          Provider availability for managed SCIP generation.
        </p>
      </header>

      <article
        data-provider-id="bazel"
        aria-labelledby="bazel-indexing-provider-heading"
        className={css({
          minWidth: 0,
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: '8px',
          backgroundColor: tok.pageBg,
          overflow: 'hidden',
        })}
      >
        <div className={css({
          display: 'grid',
          gridTemplateColumns: 'minmax(0, 1fr) auto',
          alignItems: 'start',
          gap: '12px',
          padding: '12px 14px',
          borderBottom: `1px solid ${tok.innerSep}`,
          backgroundColor: tok.bandBg,
          '@media screen and (max-width: 390px)': {
            gridTemplateColumns: 'minmax(0, 1fr)',
            gap: '8px',
          },
        })}>
          <div className={css({ minWidth: 0 })}>
            <div className={css({
              color: tok.textTertiary,
              fontFamily: FONTS.MONO,
              fontSize: '11px',
              lineHeight: '16px',
              fontWeight: 600,
              letterSpacing: '0.04em',
              textTransform: 'uppercase',
            })}>
              01 · Bazel first
            </div>
            <h2
              id="bazel-indexing-provider-heading"
              className={css({
                margin: '3px 0 0',
                color: tok.textPrimary,
                fontSize: '13px',
                lineHeight: '18px',
                fontWeight: 600,
              })}
            >
              Bazel
            </h2>
          </div>
          <div className={css({
            justifySelf: 'end',
            '@media screen and (max-width: 390px)': { justifySelf: 'start' },
          })}>
            <StatusChip tone="blue" role="status">Unavailable</StatusChip>
          </div>
        </div>

        <div className={css({ display: 'grid', gap: '10px', padding: '12px 14px' })}>
          <StateNotice tone="blue" title="Current build boundary">
            Managed generation is not registered in this build. Committed SCIP artifacts remain the only code-navigation source.
          </StateNotice>
          <p className={css({
            margin: 0,
            color: tok.textSecondary,
            fontSize: '12px',
            lineHeight: '18px',
          })}>
            No repository support, indexing profile, or build command is inferred.
          </p>
        </div>
      </article>
    </section>
  )
}
