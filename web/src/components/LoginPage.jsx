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
    <section className="mx-auto w-full max-w-6xl px-4 py-5 md:px-6" data-testid="login-page">
      <div className="section-header mb-4">
        <p className="eyebrow">Accounts</p>
        <h1 className="m-0 text-3xl font-bold tracking-tight text-slate-900">Sign in</h1>
        <p>Use your provider account to continue, or use a username for local/dev workflows.</p>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <div className="space-y-3 rounded-2xl border border-slate-200 bg-white p-4 md:p-5">
          <span className="inline-flex items-center rounded-full border border-emerald-200 bg-emerald-50 px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide text-emerald-700">
            Recommended
          </span>
          <h2 className="m-0 text-2xl font-semibold tracking-tight text-slate-900">Continue with OAuth</h2>
          <p className="m-0 text-sm text-slate-600">Best for normal usage across devices.</p>
          <p className="m-0 text-sm text-slate-600">
            We use your provider identity to create your account and sign you in. We never receive your provider password.
            <a href="https://authjs.dev/reference/core/adapters#privacy" target="_blank" rel="noreferrer"> Privacy details</a>.
          </p>
          <div className="grid gap-2">
            <button
              type="button"
              className="inline-flex w-full items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-left text-sm font-medium text-slate-700 transition-colors hover:bg-slate-50"
              onClick={() => onOAuthLogin?.('google')}
            >
              <span className="grid h-5 w-5 place-items-center rounded-md border border-slate-300 text-[11px] font-bold text-slate-700" aria-hidden="true">G</span>
              <span>Continue with Google</span>
            </button>
            <button
              type="button"
              className="inline-flex w-full items-center gap-2 rounded-lg border border-slate-300 bg-white px-3 py-2 text-left text-sm font-medium text-slate-700 transition-colors hover:bg-slate-50"
              onClick={() => onOAuthLogin?.('github')}
            >
              <span className="grid h-5 w-5 place-items-center rounded-md border border-slate-300 text-[11px] font-bold text-slate-700" aria-hidden="true">GH</span>
              <span>Continue with GitHub</span>
            </button>
          </div>
          {oauthError && <div className="panel-error" role="alert">{oauthError}</div>}
        </div>

        <form
          className="space-y-3 rounded-2xl border border-slate-200 bg-white p-4 md:p-5"
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
          <h2 className="m-0 text-2xl font-semibold tracking-tight text-slate-900">Username sign-in</h2>
          <p className="m-0 text-sm text-slate-600">Fallback path for local testing and CLI-aligned flows. Press Enter to submit or Esc to cancel.</p>

          <label className="grid gap-1">
            <span className="text-sm font-medium text-slate-700">Username</span>
            <input
              type="text"
              className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900"
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
            <span id="username-help" className="text-xs text-slate-500">
              3–32 chars. Start with a letter/number; use letters, numbers, "_", or "-".
            </span>
          </label>

          {(usernameError || (value && inlineValidation)) && (
            <div id="username-error" className="panel-error" role="alert">
              {usernameError || inlineValidation}
            </div>
          )}

          <div className="flex flex-wrap gap-2">
            <button
              type="submit"
              className="inline-flex items-center justify-center rounded-lg bg-emerald-600 px-3 py-2 text-sm font-semibold text-white transition-colors hover:bg-emerald-700 disabled:cursor-not-allowed disabled:opacity-60"
              disabled={loading || Boolean(inlineValidation)}
            >
              {loading ? 'Logging in…' : 'Login with username'}
            </button>
            <button
              type="button"
              className="inline-flex items-center justify-center rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 transition-colors hover:bg-slate-50"
              onClick={onCancel}
            >
              Cancel (Esc)
            </button>
          </div>
        </form>
      </div>
    </section>
  );
}
