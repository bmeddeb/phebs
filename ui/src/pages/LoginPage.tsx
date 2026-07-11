import { useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND } from 'baseui/button'
import { Input } from 'baseui/input'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { useAuth } from '../auth'
import { usePhebsTokens } from '../theme'

export default function LoginPage() {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { status, error: statusError, login, setup } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [setupToken, setSetupToken] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const firstRun = status?.setup_required === true
  const localLogin = firstRun || status?.password_enabled === true

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!email || !password || (firstRun && !setupToken)) return
    setSubmitting(true)
    setError('')
    try {
      if (firstRun) await setup(email, password, setupToken)
      else await login(email, password)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className={css({
      minHeight: '100vh',
      display: 'grid',
      placeItems: 'center',
      padding: '24px',
      backgroundColor: tok.pageBg,
    })}>
      <div className={css({ width: '100%', maxWidth: '400px' })}>
        <div className={css({ fontSize: '28px', lineHeight: '34px', fontWeight: 700, color: tok.textPrimary, marginBottom: '28px' })}>
          phebs
        </div>
        <h1 className={css({ fontSize: '20px', lineHeight: '28px', fontWeight: 600, color: tok.textPrimary, marginTop: 0, marginBottom: '20px' })}>
          {firstRun ? 'Set up administrator' : 'Sign in'}
        </h1>
        {(error || statusError) && (
          <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}>
            {error || statusError}
          </Notification>
        )}
        <form onSubmit={submit} className={css({ display: 'grid', gap: '14px' })}>
          {localLogin && (
            <>
              <Input
                value={email}
                onChange={(event) => setEmail(event.currentTarget.value)}
                type="email"
                autoComplete="username"
                placeholder="Email"
                aria-label="Email"
              />
              <Input
                value={password}
                onChange={(event) => setPassword(event.currentTarget.value)}
                type="password"
                autoComplete={firstRun ? 'new-password' : 'current-password'}
                placeholder="Password"
                aria-label="Password"
              />
              {firstRun && (
                <Input
                  value={setupToken}
                  onChange={(event) => setSetupToken(event.currentTarget.value)}
                  type="password"
                  autoComplete="off"
                  placeholder="Setup token"
                  aria-label="Setup token"
                />
              )}
              <Button type="submit" isLoading={submitting}>
                {firstRun ? 'Create administrator' : 'Sign in'}
              </Button>
            </>
          )}
          {!firstRun && status?.oidc_enabled && (
            <Button
              type="button"
              kind={BUTTON_KIND.secondary}
              onClick={() => window.location.assign('/api/auth/oidc/start')}
            >
              Continue with SSO
            </Button>
          )}
        </form>
      </div>
    </main>
  )
}
