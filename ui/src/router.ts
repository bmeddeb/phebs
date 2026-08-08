import { useEffect, useState } from 'react'

export const FOCUS_SEARCH = 'phebs-focus-search'

// Hash router: works from a static go:embed file server, no fallback route
// needed. Format: #/page?key=value.
export function useHashRoute(): [string, URLSearchParams] {
  const [hash, setHash] = useState(window.location.hash)
  useEffect(() => {
    const onChange = () => setHash(window.location.hash)
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])
  const h = hash.replace(/^#/, '') || '/'
  const qIndex = h.indexOf('?')
  const path = qIndex === -1 ? h : h.slice(0, qIndex)
  const params = new URLSearchParams(qIndex === -1 ? '' : h.slice(qIndex + 1))
  return [path, params]
}

export function navigate(path: string, params?: Record<string, string>) {
  const qs = params && Object.keys(params).length ? '?' + new URLSearchParams(params).toString() : ''
  window.location.hash = `#${path}${qs}`
}

export function href(path: string, params?: Record<string, string>): string {
  const qs = params && Object.keys(params).length ? '?' + new URLSearchParams(params).toString() : ''
  return `#${path}${qs}`
}
