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

export default function LoginPage({ onLogin, onOAuthLogin, onLoggedIn, onCancel }) {
  const [value, setValue] = useState(() => currentUsername());
  const [usernameError, setUsernameError] = useState('');
  const [oauthError, setOAuthError] = useState('');
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (params.get('error')) {
      setOAuthError('OAuth sign-in failed or was cancelled. Try again or use a username.');
      window.history.replaceState(null, '', `${window.location.origin}${window.location.pathname}`);
    }
  }, []);

  const inlineValidation = useMemo(() => validateUsername(value), [value]);

  return (
    <section className="section auth-page" data-testid="login-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Accounts</Badge>
        <h1>Sign in</h1>
        <p>Use your provider account to continue, or use a username for local/dev workflows.</p>
      </div>

      <div className="auth-layout grid gap-4 lg:grid-cols-2">
        <Card className="auth-card auth-card--oauth border-border/70">
          <CardHeader>
            <Badge className="auth-priority-badge w-fit">Recommended</Badge>
            <CardTitle className="auth-card-title text-xl">Continue with OAuth</CardTitle>
            <CardDescription className="auth-card-subtitle">Best for normal usage across devices.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <p className="auth-trust-text">
            We use your provider identity to create your account and sign you in. We never receive your provider password.
            <a href="https://authjs.dev/reference/core/adapters#privacy" target="_blank" rel="noreferrer"> Privacy details</a>.
          </p>
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
          {oauthError && <div className="panel-error" role="alert">{oauthError}</div>}
          </CardContent>
        </Card>

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
                Fallback path for local testing and CLI-aligned flows. Press Enter to submit or Esc to cancel.
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
      </div>
    </section>
  );
}
