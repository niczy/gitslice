import { useEffect, useMemo, useState } from 'react';
import { currentUsername } from '../utils/api.js';

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
      window.history.replaceState(null, '', `${window.location.origin}${window.location.pathname}${window.location.hash}`);
    }
  }, []);

  const inlineValidation = useMemo(() => validateUsername(value), [value]);

  return (
    <section className="section auth-page" data-testid="login-page">
      <div className="section-header">
        <p className="eyebrow">Accounts</p>
        <h1>Sign in</h1>
        <p>Use your provider account to continue, or use a username for local/dev workflows.</p>
      </div>

      <div className="auth-layout">
        <div className="auth-card auth-card--oauth">
          <span className="auth-priority-badge">Recommended</span>
          <h2 className="auth-card-title">Continue with OAuth</h2>
          <p className="auth-card-subtitle">Best for normal usage across devices.</p>
          <p className="auth-trust-text">
            We use your provider identity to create your account and sign you in. We never receive your provider password.
            <a href="https://authjs.dev/reference/core/adapters#privacy" target="_blank" rel="noreferrer"> Privacy details</a>.
          </p>
          <div className="auth-provider-list">
            <button type="button" className="auth-provider auth-provider--google" onClick={() => onOAuthLogin?.('google')}>
              <span className="auth-provider-logo" aria-hidden="true">G</span>
              <span>Continue with Google</span>
            </button>
            <button type="button" className="auth-provider auth-provider--github" onClick={() => onOAuthLogin?.('github')}>
              <span className="auth-provider-logo" aria-hidden="true">GH</span>
              <span>Continue with GitHub</span>
            </button>
          </div>
          {oauthError && <div className="panel-error" role="alert">{oauthError}</div>}
        </div>

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
          <h2 className="auth-card-title">Username sign-in</h2>
          <p className="auth-card-subtitle">Fallback path for local testing and CLI-aligned flows. Press Enter to submit or Esc to cancel.</p>

          <label className="field">
            <span className="field-label">Username</span>
            <input
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

          <div className="auth-actions">
            <button type="submit" className="primary" disabled={loading || Boolean(inlineValidation)}>
              {loading ? 'Logging in…' : 'Login with username'}
            </button>
            <button type="button" className="ghost" onClick={onCancel}>
              Cancel (Esc)
            </button>
          </div>
        </form>
      </div>
    </section>
  );
}
