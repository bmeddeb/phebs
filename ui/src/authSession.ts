export const AUTH_REQUIRED_EVENT = 'phebs-auth-required'

let csrfToken = ''

export function setCSRFToken(token?: string) {
  csrfToken = token ?? ''
}

export function csrfHeaders(): Record<string, string> {
  return csrfToken ? { 'X-CSRF-Token': csrfToken } : {}
}

export function notifyAuthRequired() {
  window.dispatchEvent(new CustomEvent(AUTH_REQUIRED_EVENT))
}
