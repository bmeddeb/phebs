import { useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND as BUTTON_KIND } from 'baseui/button'
import { Input } from 'baseui/input'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { useAuth } from '../auth'
import { BrandLockup } from '../Brand'
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
    const normalizedEmail = email.trim()
    const normalizedSetupToken = setupToken.trim()
    if (!normalizedEmail) {
      setError('Enter an email address.')
      return
    }
    if (!password) {
      setError('Enter a password.')
      return
    }
    if (firstRun && !normalizedSetupToken) {
      setError('Enter the setup token from the server log.')
      return
    }
    setSubmitting(true)
    setError('')
    try {
      if (firstRun) await setup(normalizedEmail, password, normalizedSetupToken)
      else await login(normalizedEmail, password)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className={css({
      minHeight: '100vh',
      boxSizing: 'border-box',
      display: 'grid',
      placeItems: 'center',
      padding: '24px',
      backgroundColor: tok.pageBg,
    })}>
      <div className={css({ width: '100%', maxWidth: '360px' })}>
        <div className={css({ marginBottom: '24px' })}>
          <BrandLockup markSize={24} wordmarkSize={24} gap={9} animate={false} />
        </div>
        <h1 className={css({ fontSize: '18px', lineHeight: '24px', fontWeight: 600, color: tok.textPrimary, marginTop: 0, marginBottom: '16px' })}>
          {firstRun ? 'Set up administrator' : 'Sign in'}
        </h1>
        {(error || statusError) && (
          <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto', marginLeft: 0, marginRight: 0 } } }}>
            {error || statusError}
          </Notification>
        )}
        <form noValidate onSubmit={submit} className={css({ display: 'grid', gap: '12px' })}>
          {localLogin && (
            <>
              <Input
                value={email}
                onChange={(event) => setEmail(event.currentTarget.value)}
                name="email"
                type="email"
                autoComplete={firstRun ? 'email' : 'username'}
                required
                placeholder="Email"
                aria-label="Email"
                overrides={{
                  Root: { style: { height: '44px', borderTopLeftRadius: '8px', borderTopRightRadius: '8px', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' } },
                  Input: { style: { fontSize: '15px' } },
                }}
              />
              <Input
                value={password}
                onChange={(event) => setPassword(event.currentTarget.value)}
                name="password"
                type="password"
                autoComplete={firstRun ? 'new-password' : 'current-password'}
                required
                placeholder="Password"
                aria-label="Password"
                overrides={{
                  Root: { style: { height: '44px', borderTopLeftRadius: '8px', borderTopRightRadius: '8px', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' } },
                  Input: { style: { fontSize: '15px' } },
                }}
              />
              {firstRun && (
                <Input
                  value={setupToken}
                  onChange={(event) => setSetupToken(event.currentTarget.value)}
                  name="setup_token"
                  type="password"
                  autoComplete="one-time-code"
                  required
                  placeholder="Setup token"
                  aria-label="Setup token"
                  overrides={{
                    Root: { style: { height: '44px', borderTopLeftRadius: '8px', borderTopRightRadius: '8px', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' } },
                    Input: { style: { fontSize: '15px' } },
                  }}
                />
              )}
              <Button
                type="submit"
                isLoading={submitting}
                overrides={{ BaseButton: { style: { height: '44px', fontSize: '15px', borderTopLeftRadius: '8px', borderTopRightRadius: '8px', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' } } }}
              >
                {firstRun ? 'Create administrator' : 'Sign in'}
              </Button>
            </>
          )}
          {!firstRun && localLogin && status?.oidc_enabled && (
            <div className={css({ display: 'flex', alignItems: 'center', gap: '10px', marginTop: '2px', marginBottom: '2px' })}>
              <span className={css({ flex: 1, height: '1px', backgroundColor: tok.innerSep })} />
              <span className={css({ fontSize: '11px', color: tok.gutter })}>or</span>
              <span className={css({ flex: 1, height: '1px', backgroundColor: tok.innerSep })} />
            </div>
          )}
          {!firstRun && status?.oidc_enabled && (
            <Button
              type="button"
              kind={BUTTON_KIND.secondary}
              onClick={() => window.location.assign('/api/auth/oidc/start')}
              overrides={{ BaseButton: { style: { height: '44px', fontSize: '15px', borderTopLeftRadius: '8px', borderTopRightRadius: '8px', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' } } }}
            >
              Continue with SSO
            </Button>
          )}
        </form>
      </div>
    </main>
  )
}
