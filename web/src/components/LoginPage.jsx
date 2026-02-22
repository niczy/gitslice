import { useMemo, useState } from 'react';
import { currentUsername } from '../utils/api.js';

const USERNAME_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_-]{2,31}$/;

function validateUsername(value) {
  const trimmed = (value || '').trim();
  if (!trimmed) {
    return 'Username is required.';
  }
  if (!USERNAME_PATTERN.test(trimmed)) {
    return 'Use 3-32 chars: letters, numbers, "_" or "-", starting with a letter/number.';
  }
  return '';
}

// ---------------------------------------------------------------------------
// Login Page Component
// ---------------------------------------------------------------------------

export default function LoginPage({ onLogin, onOAuthLogin, oauthError, onDismissOAuthError, onLoggedIn, onCancel }) {
  const [value, setValue] = useState(() => currentUsername());
  const [submitError, setSubmitError] = useState('');
  const [touched, setTouched] = useState(false);
  const [loading, setLoading] = useState(false);
  const inlineError = useMemo(() => (touched ? validateUsername(value) : ''), [touched, value]);

  return (
    <section className="section auth-page" data-testid="login-page">
      <div className="section-header">
        <p className="eyebrow">Accounts</p>
        <h2>Sign in</h2>
        <p>Use your provider account to continue, or use a username for local/dev workflows.</p>
      </div>

      <div className="auth-layout">
        <div className="auth-card auth-card--oauth auth-card--recommended">
          <p className="auth-recommended-pill">Recommended</p>
          <h3 className="auth-card-title">Continue with OAuth</h3>
          <p className="auth-card-subtitle">Fastest path for normal usage across devices.</p>
          <p className="auth-trust-text">We only use your provider identity (name/email) to verify your account. <a href="/privacy" target="_blank" rel="noreferrer">Privacy policy</a>.</p>
          {oauthError && <div className="panel-error">{oauthError}</div>}
          <div className="auth-provider-list">
            <button type="button" className="auth-provider auth-provider--google" onClick={() => { onDismissOAuthError?.(); onOAuthLogin?.('google'); }}>
              <span className="auth-provider-logo" aria-hidden="true">G</span>
              <span>Continue with Google</span>
            </button>
            <button type="button" className="auth-provider auth-provider--github" onClick={() => { onDismissOAuthError?.(); onOAuthLogin?.('github'); }}>
              <span className="auth-provider-logo" aria-hidden="true">GH</span>
              <span>Continue with GitHub</span>
            </button>
          </div>
        </div>

        <form
          className="auth-card auth-card--username"
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              e.preventDefault();
              onCancel?.();
            }
          }}
          onSubmit={async (e) => {
            e.preventDefault();
            setSubmitError('');
            setTouched(true);
            const validationError = validateUsername(value);
            if (validationError) {
              return;
            }
            setLoading(true);
            try {
              await onLogin(value.trim());
              onLoggedIn?.();
            } catch {
              setSubmitError('Sign-in failed for that username. Please check the format and try again.');
            } finally {
              setLoading(false);
            }
          }}
        >
          <h3 className="auth-card-title">Username sign-in</h3>
          <p className="auth-card-subtitle">Fallback path for local testing and CLI-aligned flows.</p>

          <label className="field">
            <span className="field-label">Username</span>
            <input
              type="text"
              value={value}
              onChange={(e) => {
                setValue(e.target.value);
                if (!touched) setTouched(true);
                if (submitError) setSubmitError('');
              }}
              onBlur={() => setTouched(true)}
              placeholder="e.g. nic"
              autoFocus
              spellCheck={false}
              autoComplete="off"
              aria-invalid={Boolean(inlineError || submitError)}
              aria-describedby="username-help"
            />
          </label>
          <p id="username-help" className="field-help">3-32 characters, letters/numbers, optional "_" or "-".</p>

          {(inlineError || submitError) && <div className="panel-error">{inlineError || submitError}</div>}

          <div className="auth-actions">
            <button type="submit" className="primary" disabled={loading}>
              {loading ? 'Logging in…' : 'Login with username'}
            </button>
            <button type="button" className="ghost" onClick={onCancel}>
              Cancel (Esc)
            </button>
          </div>
          <p className="auth-keyboard-hint">Press Enter to submit. Press Escape to cancel.</p>
        </form>
      </div>
    </section>
  );
}
