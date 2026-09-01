// T44.3 closure: one closed budget shared by the File-page admission check,
// the parser worker, and the returned-result firewall. The source endpoint may
// return up to 10 MiB; preview remains deliberately smaller and isolated.
export const MARKDOWN_PREVIEW_MAX_UNITS = 131_072
export const MARKDOWN_PARSE_TIMEOUT_MS = 1_000
export const MAX_RENDERED_DIAGRAMS = 20

// At most one prose segment can surround each admitted diagram. Once the
// diagram cap is reached, every later fence remains ordinary prose/code, so
// the worker can never return more than 2N+1 segments.
export const MAX_MARKDOWN_SEGMENTS = MAX_RENDERED_DIAGRAMS * 2 + 1

// marked output may expand source through structural tags. Bound aggregate
// returned strings before DOMPurify or React sees them; the worker and caller
// both enforce this value.
export const MAX_MARKDOWN_WORKER_RESULT_UNITS = 524_288
