import { useEffect, useState } from 'react';

import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';
import { fetchRepoBindings } from '../utils/api.js';

export default function SettingsPage({ username, authSessionSource, onOpenProfile, onLogout }) {
  const [bindings, setBindings] = useState([]);
  const [bindingsLoading, setBindingsLoading] = useState(false);
  const [bindingsError, setBindingsError] = useState('');

  useEffect(() => {
    let cancelled = false;
    if (!username) {
      setBindings([]);
      setBindingsLoading(false);
      setBindingsError('');
      return () => {
        cancelled = true;
      };
    }

    setBindingsLoading(true);
    setBindingsError('');
    fetchRepoBindings()
      .then((nextBindings) => {
        if (!cancelled) {
          setBindings(nextBindings);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setBindings([]);
          setBindingsError(err?.message || 'Unable to load repo bindings.');
        }
      })
      .finally(() => {
        if (!cancelled) {
          setBindingsLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [username]);

  return (
    <section className="section space-y-4" data-testid="settings-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Account</Badge>
        <h2>Settings</h2>
        <p>Manage session details, inspect your GitHub repo bindings, and jump into profile details.</p>
      </div>

      {!username && <div className="panel-error">You need to log in before account settings are available.</div>}
      {username && (
        <>
          <div className="grid gap-4 md:grid-cols-2">
            <Card className="border-border/70">
              <CardContent className="space-y-4 pt-6">
                <div className="kv">
                  <div className="kv-row">
                    <span className="kv-key">Signed in as</span>
                    <span className="kv-val">{username}</span>
                  </div>
                  <div className="kv-row">
                    <span className="kv-key">Auth mode</span>
                    <span className="kv-val">{authSessionSource || 'unknown'}</span>
                  </div>
                </div>
                <div className="auth-actions">
                  <Button type="button" onClick={onOpenProfile}>Open Profile Details</Button>
                  <Button type="button" variant="ghost" onClick={onLogout}>Logout</Button>
                </div>
              </CardContent>
            </Card>

            <Card className="border-border/70">
              <CardContent className="space-y-4 pt-6">
                <div className="space-y-2">
                  <h3>GitHub bindings</h3>
                  <p className="status">Tracked remotes bound into your home slice for import, pull, and optional pushback.</p>
                </div>
                {bindingsLoading && <div className="panel-empty">Loading bindings…</div>}
                {!bindingsLoading && bindingsError && <div className="panel-error">{bindingsError}</div>}
                {!bindingsLoading && !bindingsError && bindings.length === 0 && (
                  <div className="panel-empty" data-testid="settings-empty-state">
                    No GitHub bindings yet. Use <code>gs repo import &lt;repo-url&gt; &lt;/absolute/path&gt;</code> to add one.
                  </div>
                )}
                {!bindingsLoading && !bindingsError && bindings.length > 0 && (
                  <div className="space-y-3" data-testid="settings-repo-bindings">
                    {bindings.map((binding) => (
                      <div key={binding.binding_id || binding.path} className="rounded-lg border border-border/70 bg-background/40 p-3">
                        <div className="kv">
                          <div className="kv-row">
                            <span className="kv-key">Path</span>
                            <span className="kv-val">{binding.path}</span>
                          </div>
                          <div className="kv-row">
                            <span className="kv-key">Repo</span>
                            <span className="kv-val">{binding.repo_url}</span>
                          </div>
                          <div className="kv-row">
                            <span className="kv-key">Branch</span>
                            <span className="kv-val">{binding.branch || 'default'}</span>
                          </div>
                          <div className="kv-row">
                            <span className="kv-key">Push enabled</span>
                            <span className="kv-val">{binding.push_enabled ? 'yes' : 'no'}</span>
                          </div>
                          <div className="kv-row">
                            <span className="kv-key">Last imported</span>
                            <span className="kv-val">{binding.last_imported_commit || 'not imported yet'}</span>
                          </div>
                          <div className="kv-row">
                            <span className="kv-key">Last pushed</span>
                            <span className="kv-val">{binding.last_pushed_commit || 'never'}</span>
                          </div>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          </div>
        </>
      )}
    </section>
  );
}
