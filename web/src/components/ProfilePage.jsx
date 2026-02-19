import { useCallback, useEffect, useState } from 'react';
import { apiBaseUrl, fetchWithAuth } from '../utils/api.js';

// ---------------------------------------------------------------------------
// Profile Page Component
// ---------------------------------------------------------------------------

function formatTimestamp(value) {
  if (!value) {
    return 'unknown';
  }
  if (typeof value === 'number') {
    const millis = value < 1_000_000_000_000 ? value * 1000 : value;
    return new Date(millis).toLocaleString();
  }
  return new Date(value).toLocaleString();
}

export default function ProfilePage({ username, onLogout, onRequireLogin }) {
  const [me, setMe] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [orgName, setOrgName] = useState('');
  const [orgCreating, setOrgCreating] = useState(false);
  const [orgError, setOrgError] = useState('');

  const loadMe = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const resp = await fetchWithAuth(`${apiBaseUrl}/v1/me`);
      if (resp.status === 401) {
        onRequireLogin?.();
        return;
      }
      if (!resp.ok) throw new Error('bad');
      setMe(await resp.json());
    } catch {
      setError('Unable to load profile.');
    } finally {
      setLoading(false);
    }
  }, [onRequireLogin]);

  useEffect(() => {
    loadMe();
  }, [loadMe]);

  return (
    <section className="section auth-page" data-testid="profile-page">
      <div className="section-header">
        <p className="eyebrow">Accounts</p>
        <h2>Profile</h2>
        <p>{username ? `Logged in as ${username}.` : 'Not logged in.'}</p>
      </div>

      <div className="profile-grid">
        <div className="auth-card">
          <h3 style={{ marginTop: 0 }}>Account</h3>
          {loading && <div className="status">Loading…</div>}
          {error && <div className="panel-error">{error}</div>}
          {!loading && !error && me?.user && (
            <div className="kv">
              <div className="kv-row">
                <span className="kv-key">Username</span>
                <span className="kv-val">{me.user.username}</span>
              </div>
              <div className="kv-row">
                <span className="kv-key">Created</span>
                <span className="kv-val">
                  {formatTimestamp(me.user.createdAt || me.user.created_at)}
                </span>
              </div>
            </div>
          )}
          <div className="auth-actions" style={{ marginTop: '16px' }}>
            <button type="button" className="ghost" onClick={loadMe}>
              Refresh
            </button>
            <button type="button" className="ghost" onClick={onLogout}>
              Logout
            </button>
          </div>
        </div>

        <div className="auth-card">
          <h3 style={{ marginTop: 0 }}>Organizations</h3>
          {!loading && !error && (me?.organizations || []).length === 0 && (
            <div className="panel-empty">No organizations yet.</div>
          )}
          {!loading && !error && (me?.organizations || []).length > 0 && (
            <ul className="org-list">
              {(me.organizations || []).map((o) => (
                <li key={o.slug} className="org-item">
                  <div className="org-item-title">
                    <span className="org-name">{o.name}</span>
                    <span className="org-slug">{o.slug}</span>
                  </div>
                  <div className="org-item-meta">Created by {o.createdBy || o.created_by || 'unknown'}</div>
                </li>
              ))}
            </ul>
          )}

          <form
            className="org-create"
            onSubmit={async (e) => {
              e.preventDefault();
              setOrgError('');
              setOrgCreating(true);
              try {
                const resp = await fetchWithAuth(`${apiBaseUrl}/v1/orgs`, {
                  method: 'POST',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({ name: orgName }),
                });
                if (!resp.ok) throw new Error('bad');
                setOrgName('');
                await loadMe();
              } catch {
                setOrgError('Unable to create organization.');
              } finally {
                setOrgCreating(false);
              }
            }}
          >
            <label className="field">
              <span className="field-label">New organization</span>
              <input
                type="text"
                value={orgName}
                onChange={(e) => setOrgName(e.target.value)}
                placeholder="e.g. Acme"
              />
            </label>
            {orgError && <div className="panel-error">{orgError}</div>}
            <div className="auth-actions">
              <button type="submit" className="primary" disabled={orgCreating || !orgName.trim()}>
                {orgCreating ? 'Creating…' : 'Create organization'}
              </button>
            </div>
          </form>
        </div>
      </div>
    </section>
  );
}
