import { useEffect, useState } from 'react';

import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';
import { createAgentKey, fetchAgentKeys, fetchRepoBindings, revokeAgentKey } from '../utils/api.js';

export default function SettingsPage({ username, authSessionSource, onOpenProfile, onLogout }) {
  const [bindings, setBindings] = useState([]);
  const [bindingsLoading, setBindingsLoading] = useState(false);
  const [bindingsError, setBindingsError] = useState('');
  const [agentKeys, setAgentKeys] = useState([]);
  const [agentKeysLoading, setAgentKeysLoading] = useState(false);
  const [agentKeysError, setAgentKeysError] = useState('');
  const [agentKeyName, setAgentKeyName] = useState('');
  const [agentKeyPublicKey, setAgentKeyPublicKey] = useState('');
  const [agentKeyFormError, setAgentKeyFormError] = useState('');
  const [agentKeySaving, setAgentKeySaving] = useState(false);
  const [revokingKeyId, setRevokingKeyId] = useState('');

  useEffect(() => {
    let cancelled = false;
    if (!username) {
      setBindings([]);
      setBindingsLoading(false);
      setBindingsError('');
      setAgentKeys([]);
      setAgentKeysLoading(false);
      setAgentKeysError('');
      return () => {
        cancelled = true;
      };
    }

    setBindingsLoading(true);
    setBindingsError('');
    setAgentKeysLoading(true);
    setAgentKeysError('');
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
    fetchAgentKeys()
      .then((nextKeys) => {
        if (!cancelled) {
          setAgentKeys(nextKeys);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setAgentKeys([]);
          setAgentKeysError(err?.message || 'Unable to load agent keys.');
        }
      })
      .finally(() => {
        if (!cancelled) {
          setAgentKeysLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [username]);

  async function refreshAgentKeys() {
    setAgentKeysLoading(true);
    setAgentKeysError('');
    try {
      const nextKeys = await fetchAgentKeys();
      setAgentKeys(nextKeys);
    } catch (err) {
      setAgentKeys([]);
      setAgentKeysError(err?.message || 'Unable to load agent keys.');
    } finally {
      setAgentKeysLoading(false);
    }
  }

  async function handleAgentKeyCreate(event) {
    event.preventDefault();
    setAgentKeyFormError('');
    if (!agentKeyName.trim() || !agentKeyPublicKey.trim()) {
      setAgentKeyFormError('Key name and public key are required.');
      return;
    }
    setAgentKeySaving(true);
    try {
      await createAgentKey({
        name: agentKeyName.trim(),
        publicKeyText: agentKeyPublicKey.trim(),
      });
      setAgentKeyName('');
      setAgentKeyPublicKey('');
      await refreshAgentKeys();
    } catch (err) {
      setAgentKeyFormError(err?.message || 'Unable to add agent key.');
    } finally {
      setAgentKeySaving(false);
    }
  }

  async function handleAgentKeyRevoke(keyId) {
    setAgentKeysError('');
    setRevokingKeyId(keyId);
    try {
      await revokeAgentKey(keyId);
      await refreshAgentKeys();
    } catch (err) {
      setAgentKeysError(err?.message || 'Unable to revoke agent key.');
    } finally {
      setRevokingKeyId('');
    }
  }

  return (
    <section className="section space-y-4" data-testid="settings-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Account</Badge>
        <h2>Settings</h2>
        <p>Manage session details, enrolled agent keys, and GitHub repo bindings from one account surface.</p>
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

          <Card className="border-border/70">
            <CardContent className="space-y-4 pt-6">
              <div className="space-y-2">
                <h3>Agent keys</h3>
                <p className="status">Enroll public keys for non-interactive <code>gs auth login --key</code> flows, then revoke them here when a machine should lose access.</p>
              </div>

              <form className="grid gap-4 rounded-lg border border-border/70 bg-background/40 p-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_auto]" data-testid="settings-agent-key-form" onSubmit={handleAgentKeyCreate}>
                <label className="space-y-2">
                  <span className="text-sm font-medium text-foreground">Key name</span>
                  <input
                    className="h-11 w-full rounded-md border border-border bg-background px-3 text-sm text-foreground outline-none transition focus:border-ring focus:ring-2 focus:ring-ring/40"
                    data-testid="settings-agent-key-name"
                    placeholder="codex-laptop"
                    value={agentKeyName}
                    onChange={(event) => setAgentKeyName(event.target.value)}
                  />
                </label>
                <label className="space-y-2">
                  <span className="text-sm font-medium text-foreground">Public key</span>
                  <textarea
                    className="min-h-28 w-full rounded-md border border-border bg-background px-3 py-3 text-sm text-foreground outline-none transition focus:border-ring focus:ring-2 focus:ring-ring/40"
                    data-testid="settings-agent-key-public-key"
                    placeholder="Paste the .pub file generated by gs auth keygen"
                    value={agentKeyPublicKey}
                    onChange={(event) => setAgentKeyPublicKey(event.target.value)}
                  />
                </label>
                <div className="flex items-end">
                  <Button data-testid="settings-agent-key-submit" type="submit" disabled={agentKeySaving}>
                    {agentKeySaving ? 'Adding…' : 'Add key'}
                  </Button>
                </div>
              </form>
              {agentKeyFormError && <div className="panel-error">{agentKeyFormError}</div>}

              {agentKeysLoading && <div className="panel-empty">Loading agent keys…</div>}
              {!agentKeysLoading && agentKeysError && <div className="panel-error">{agentKeysError}</div>}
              {!agentKeysLoading && !agentKeysError && agentKeys.length === 0 && (
                <div className="panel-empty" data-testid="settings-agent-keys-empty">
                  No agent keys yet. Run <code>gs auth keygen --out ~/.config/gitslice/agent_ed25519</code> and paste the generated <code>.pub</code> file here.
                </div>
              )}
              {!agentKeysLoading && !agentKeysError && agentKeys.length > 0 && (
                <div className="space-y-3" data-testid="settings-agent-keys">
                  {agentKeys.map((key) => (
                    <div key={key.id} className="rounded-lg border border-border/70 bg-background/40 p-3">
                      <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
                        <div className="kv flex-1">
                          <div className="kv-row">
                            <span className="kv-key">Name</span>
                            <span className="kv-val">{key.name}</span>
                          </div>
                          <div className="kv-row">
                            <span className="kv-key">Key ID</span>
                            <span className="kv-val">{key.id}</span>
                          </div>
                          <div className="kv-row">
                            <span className="kv-key">Fingerprint</span>
                            <span className="kv-val break-all">{key.fingerprint}</span>
                          </div>
                          <div className="kv-row">
                            <span className="kv-key">Last used</span>
                            <span className="kv-val">{key.last_used_at || 'never'}</span>
                          </div>
                          <div className="kv-row">
                            <span className="kv-key">State</span>
                            <span className="kv-val">{key.revoked ? 'revoked' : 'active'}</span>
                          </div>
                        </div>
                        <div className="flex items-center gap-2">
                          <Badge variant={key.revoked ? 'outline' : 'secondary'}>{key.revoked ? 'Revoked' : 'Active'}</Badge>
                          <Button
                            type="button"
                            variant="destructive"
                            size="sm"
                            disabled={key.revoked || revokingKeyId === key.id}
                            onClick={() => handleAgentKeyRevoke(key.id)}
                            data-testid={`settings-agent-key-revoke-${key.id}`}
                          >
                            {revokingKeyId === key.id ? 'Revoking…' : 'Revoke'}
                          </Button>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </>
      )}
    </section>
  );
}
