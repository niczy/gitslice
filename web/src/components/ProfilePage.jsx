import { useCallback, useEffect, useState } from 'react';
import { apiBaseUrl, fetchWithAuth } from '../utils/api.js';
import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card.jsx';
import { Input } from './ui/input.jsx';

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
        <Badge variant="secondary" className="eyebrow">Accounts</Badge>
        <h2>Profile</h2>
        <p>{username ? `Logged in as ${username}.` : 'Not logged in.'}</p>
      </div>

      <div className="profile-grid">
        <Card className="auth-card border-border/70">
          <CardHeader>
            <CardTitle className="text-xl">Account</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
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
            <div className="auth-actions">
              <Button type="button" variant="ghost" onClick={loadMe}>
                Refresh
              </Button>
              <Button type="button" variant="ghost" onClick={onLogout}>
                Logout
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="auth-card border-border/70">
          <CardHeader>
            <CardTitle className="text-xl">Organizations</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
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
                <Input
                  type="text"
                  value={orgName}
                  onChange={(e) => setOrgName(e.target.value)}
                  placeholder="e.g. Acme"
                />
              </label>
              {orgError && <div className="panel-error">{orgError}</div>}
              <div className="auth-actions">
                <Button type="submit" disabled={orgCreating || !orgName.trim()}>
                  {orgCreating ? 'Creating…' : 'Create organization'}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      </div>
    </section>
  );
}
