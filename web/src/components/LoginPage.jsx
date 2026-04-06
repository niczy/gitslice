import { useEffect, useMemo, useState } from 'react';
import { currentUsername } from '../utils/api.js';
import { Button } from './ui/button.jsx';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card.jsx';
import { Input } from './ui/input.jsx';
import { Badge } from './ui/badge.jsx';

const USERNAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_-]{2,31}$/;

function validateUsername(value) {
  const trimmed = (value || '').trim();
  if (!trimmed) {
    return 'Username is required.';
  }
  if (!USERNAME_PATTERN.test(trimmed)) {
    return 'Use 3-32 characters: letters, numbers, "_" or "-", starting with a letter/number.';
  }
  return '';
}

// ---------------------------------------------------------------------------
// Login Page Component
// ---------------------------------------------------------------------------

export default function LoginPage({
  authProvider = 'local',
  allowDevLogin = true,
  onLogin,
  onOAuthLogin,
  onLoggedIn,
  onCancel,
}) {
  const [value, setValue] = useState(() => currentUsername());
  const [usernameError, setUsernameError] = useState('');
  const [oauthError, setOAuthError] = useState('');
  const [loading, setLoading] = useState(false);
  const isWorkOS = String(authProvider || '').trim().toLowerCase() === 'workos';

  useEffect(() => {
    if (typeof window === 'undefined') {
      return;
    }
    const params = new URLSearchParams(window.location.search);
    if (params.get('error')) {
      setOAuthError(
        isWorkOS
          ? 'WorkOS sign-in failed or was cancelled. Try again.'
          : 'OAuth sign-in failed or was cancelled. Try again or use a username.',
      );
      window.history.replaceState(null, '', `${window.location.origin}${window.location.pathname}`);
    }
  }, [isWorkOS]);

  const inlineValidation = useMemo(() => validateUsername(value), [value]);
  const signInTitle = isWorkOS ? 'Continue with WorkOS' : 'Continue with OAuth';
  const signInDescription = isWorkOS
    ? 'Recommended for normal usage. WorkOS handles the human sign-in flow.'
    : 'Best for normal usage across devices.';
  const signInBody = isWorkOS
    ? 'Use the hosted WorkOS sign-in flow to continue. Social login, passwords, and future SSO live there.'
    : 'We use your provider identity to create your account and sign you in. We never receive your provider password.';

  return (
    <section className="section auth-page" data-testid="login-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Accounts</Badge>
        <h1>Sign in</h1>
        <p>
          {allowDevLogin
            ? 'Use the primary sign-in flow to continue. Username login stays available only as an explicit local/dev fallback.'
            : 'Use the primary sign-in flow to continue.'}
        </p>
      </div>

      <div className={`auth-layout grid gap-4 ${allowDevLogin ? 'lg:grid-cols-2' : 'lg:grid-cols-1'}`}>
        <Card className="auth-card auth-card--oauth border-border/70">
          <CardHeader>
            <Badge className="auth-priority-badge w-fit">Recommended</Badge>
            <CardTitle className="auth-card-title text-xl">{signInTitle}</CardTitle>
            <CardDescription className="auth-card-subtitle">{signInDescription}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <p className="auth-trust-text">
              {signInBody}
              {!isWorkOS && (
                <>
                  <a href="https://authjs.dev/reference/core/adapters#privacy" target="_blank" rel="noreferrer"> Privacy details</a>.
                </>
              )}
            </p>
            {isWorkOS ? (
              <Button
                type="button"
                variant="outline"
                className="auth-provider w-full justify-start"
                onClick={() => onOAuthLogin?.('workos')}
              >
                <span className="auth-provider-logo" aria-hidden="true">W</span>
                <span>Continue with WorkOS</span>
              </Button>
            ) : (
              <div className="auth-provider-list space-y-2">
                <Button type="button" variant="outline" className="auth-provider auth-provider--google w-full justify-start" onClick={() => onOAuthLogin?.('google')}>
                  <span className="auth-provider-logo" aria-hidden="true">G</span>
                  <span>Continue with Google</span>
                </Button>
                <Button type="button" variant="outline" className="auth-provider auth-provider--github w-full justify-start" onClick={() => onOAuthLogin?.('github')}>
                  <span className="auth-provider-logo" aria-hidden="true">GH</span>
                  <span>Continue with GitHub</span>
                </Button>
              </div>
            )}
            {oauthError && <div className="panel-error" role="alert">{oauthError}</div>}
          </CardContent>
        </Card>

        {allowDevLogin && (
          <form
            className="auth-card auth-card--username"
            onSubmit={async (e) => {
              e.preventDefault();
              setUsernameError('');
              setLoading(true);
              try {
                const nextError = validateUsername(value);
                if (nextError) {
                  throw new Error(nextError);
                }
                await onLogin(value);
                onLoggedIn?.();
              } catch (err) {
                setUsernameError(err?.message || 'Invalid username.');
              } finally {
                setLoading(false);
              }
            }}
          >
            <Card className="border-border/70">
              <CardHeader>
                <CardTitle className="auth-card-title text-xl">Username sign-in</CardTitle>
                <CardDescription className="auth-card-subtitle">
                  Explicit fallback for local testing, dev environments, and CLI-aligned workflows. Press Enter to submit or Esc to cancel.
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <label className="field space-y-2">
                  <span className="field-label">Username</span>
                  <Input
                    type="text"
                    value={value}
                    onChange={(e) => {
                      const nextValue = e.target.value;
                      setValue(nextValue);
                      setUsernameError('');
                    }}
                    onBlur={() => setUsernameError(validateUsername(value))}
                    onKeyDown={(e) => {
                      if (e.key === 'Escape') {
                        e.preventDefault();
                        onCancel?.();
                      }
                    }}
                    placeholder="e.g. nic"
                    autoFocus
                    spellCheck={false}
                    autoComplete="off"
                    aria-invalid={Boolean(usernameError || (value && inlineValidation))}
                    aria-describedby="username-help username-error"
                  />
                  <span id="username-help" className="field-help">
                    3–32 chars. Start with a letter/number; use letters, numbers, "_", or "-".
                  </span>
                </label>

                {(usernameError || (value && inlineValidation)) && (
                  <div id="username-error" className="panel-error" role="alert">
                    {usernameError || inlineValidation}
                  </div>
                )}

                <div className="auth-actions flex items-center gap-2">
                  <Button type="submit" disabled={loading || Boolean(inlineValidation)}>
                    {loading ? 'Logging in…' : 'Login with username'}
                  </Button>
                  <Button type="button" variant="ghost" onClick={onCancel}>
                    Cancel (Esc)
                  </Button>
                </div>
              </CardContent>
            </Card>
          </form>
        )}
      </div>
    </section>
  );
}
