import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND, SIZE as BUTTON_SIZE } from 'baseui/button'
import {
  fetchCallerCitation,
  type CallerMapCitation,
  type CallerMapSource,
} from '../api'
import { StatusChip } from '../components/kit'
import { FONTS, usePhebsTokens } from '../theme'
import { isAbortError } from '../util'

/**
 * Reads only the immutable byte range authorized by a Caller Map citation.
 * Deliberately does not offer a whole-file fallback when that capability is
 * absent: an exact overlay occurrence is not generic repository-read access.
 */
export default function ExactCallerCitation({ source }: { source: CallerMapSource }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [citation, setCitation] = useState<CallerMapCitation | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const request = useRef<AbortController | null>(null)
  const exactCapability = source.plane === 'repository-overlay' &&
    Boolean(source.citation && source.object_id && source.blob_digest)

  useEffect(() => () => request.current?.abort(), [])

  const toggleCitation = () => {
    if (citation) {
      setCitation(null)
      setError('')
      return
    }
    if (!exactCapability || !source.citation) return
    request.current?.abort()
    const controller = new AbortController()
    request.current = controller
    setLoading(true)
    setError('')
    fetchCallerCitation(source.citation, controller.signal)
      .then((result) => {
        if (!exactCitationMatches(source, result)) {
          throw new Error('Exact citation response did not match the selected caller occurrence.')
        }
        setCitation(result)
      })
      .catch((cause) => {
        if (!isAbortError(cause)) {
          setError(cause instanceof Error ? cause.message : String(cause))
        }
      })
      .finally(() => {
        if (request.current === controller) {
          request.current = null
          setLoading(false)
        }
      })
  }

  const label = `${source.repository}/${source.path}:${source.start_line}`
  return (
    <div>
      <div className={css({
        display: 'flex', alignItems: 'center', gap: '7px', flexWrap: 'wrap',
      })}>
        <span className={css({
          color: tok.textPrimary,
          fontFamily: FONTS.MONO,
          fontSize: '12px',
          lineHeight: '18px',
          overflowWrap: 'anywhere',
        })}>
          {label}
        </span>
        {exactCapability ? (
          <Button
            type="button"
            size={BUTTON_SIZE.mini}
            kind={BUTTON_KIND.secondary}
            aria-expanded={citation !== null}
            disabled={loading}
            onClick={toggleCitation}
          >
            {loading ? 'Reading exact citation…' : citation ? 'Hide exact citation' : 'Read exact cited bytes'}
          </Button>
        ) : (
          <StatusChip tone="blue">exact citation unavailable</StatusChip>
        )}
      </div>
      <div className={css({
        marginTop: '3px', color: tok.textTertiary, fontSize: '9px', lineHeight: '14px',
      })}>
        Repository-overlay occurrence; source access is limited to its authorized byte range.
      </div>
      {error && (
        <div role="alert" className={css({
          marginTop: '7px', display: 'flex', alignItems: 'baseline', gap: '9px', flexWrap: 'wrap',
        })}>
          <span className={css({ color: tok.status.conflict.text, fontSize: '10px', lineHeight: '15px' })}>
            Exact citation unavailable: {error}
          </span>
          <button
            type="button"
            onClick={toggleCitation}
            className={css({ border: 0, padding: 0, background: 'transparent', color: tok.textPrimary, fontSize: '10px', fontWeight: 600, textDecoration: 'underline', cursor: 'pointer' })}
          >
            Retry citation read
          </button>
        </div>
      )}
      {citation && (
        <section
          data-testid="caller-map-exact-citation"
          className={css({
            marginTop: '8px',
            border: `1px solid ${tok.cardBorder}`,
            backgroundColor: tok.bandBg,
          })}
        >
          <div className={css({
            padding: '6px 8px',
            borderBottom: `1px solid ${tok.innerSep}`,
            color: tok.textTertiary,
            fontSize: '9px',
            lineHeight: '14px',
          })}>
            Exact cited bytes · repository-overlay · object {shortID(citation.source.object_id ?? '')}
          </div>
          <pre
            aria-label={`Exact cited bytes for ${label}`}
            className={css({
              margin: 0,
              padding: '8px',
              overflowX: 'auto',
              color: tok.textPrimary,
              fontFamily: FONTS.MONO,
              fontSize: '10px',
              lineHeight: '16px',
              whiteSpace: 'pre-wrap',
              overflowWrap: 'anywhere',
            })}
          >
            {citation.content}
          </pre>
        </section>
      )}
    </div>
  )
}

function exactCitationMatches(
  source: CallerMapSource,
  citation: CallerMapCitation,
) {
  const confirmed = citation.source
  return citation.schema_version === 'caller-map-citation-v1' &&
    citation.generation.plane === 'repository-overlay' &&
    confirmed.plane === 'repository-overlay' &&
    confirmed.repository === source.repository &&
    confirmed.commit === source.commit &&
    confirmed.path === source.path &&
    confirmed.object_id === source.object_id &&
    confirmed.blob_digest === source.blob_digest &&
    confirmed.start_byte === source.start_byte &&
    confirmed.end_byte === source.end_byte &&
    confirmed.start_line === source.start_line &&
    confirmed.end_line === source.end_line &&
    typeof citation.content === 'string'
}

function shortID(value: string) {
  return value.length <= 18 ? value : `${value.slice(0, 10)}…${value.slice(-7)}`
}
