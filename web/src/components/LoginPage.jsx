import { useState } from 'react';
import { currentUsername } from '../utils/api.js';

// ---------------------------------------------------------------------------
// Login Page Component
// ---------------------------------------------------------------------------

export default function LoginPage({ onLogin, onLoggedIn, onCancel }) {
  const [value, setValue] = useState(() => currentUsername());
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  return (
    <section className="section auth-page" data-testid="login-page">
      <div className="section-header">
        <p className="eyebrow">Accounts</p>
        <h2>Login</h2>
        <p>Pick a username. This is fake auth: no password.</p>
      </div>

      <form
        className="auth-card"
        onSubmit={async (e) => {
          e.preventDefault();
          setError('');
          setLoading(true);
          try {
            await onLogin(value);
            onLoggedIn?.();
          } catch (err) {
            setError('Invalid username. Use 3-32 chars: letters, numbers, "_" or "-", starting with a letter/number.');
          } finally {
            setLoading(false);
          }
        }}
      >
        <label className="field">
          <span className="field-label">Username</span>
          <input
            type="text"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="e.g. nic"
            autoFocus
            spellCheck={false}
            autoComplete="off"
          />
        </label>
        {error && <div className="panel-error">{error}</div>}
        <div className="auth-actions">
          <button type="submit" className="primary" disabled={loading}>
            {loading ? 'Logging in…' : 'Login'}
          </button>
          <button type="button" className="ghost" onClick={onCancel}>
            Cancel
          </button>
        </div>
      </form>
    </section>
  );
}
