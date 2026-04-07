import { useEffect, useState } from 'react';

import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';
import {
  createAgentKey,
  deleteAuthMethod,
  deleteAuthSession,
  fetchAgentKeys,
  fetchAuthContext,
  fetchAuthMethods,
  fetchAuthSessions,
  fetchRepoBindings,
  linkCurrentWorkOSAuthMethod,
  revokeAgentKey,
} from '../utils/api.js';

function formatAuthMethodType(value) {
  const normalized = String(value || '').trim();
  switch (normalized) {
    case 'AUTH_METHOD_TYPE_PASSWORD':
    case '1':
      return 'password';
    case 'AUTH_METHOD_TYPE_OAUTH':
    case '2':
      return 'oauth';
    case 'AUTH_METHOD_TYPE_SAML':
    case '3':
      return 'saml';
    default:
      return normalized || 'unknown';
  }
}

export default function SettingsPage({ username, authSessionSource, onOpenProfile, onLogout }) {
  const [bindings, setBindings] = useState([]);
  const [bindingsLoading, setBindingsLoading] = useState(false);
  const [bindingsError, setBindingsError] = useState('');
  const [authMethods, setAuthMethods] = useState([]);
  const [authMethodsLoading, setAuthMethodsLoading] = useState(false);
  const [authMethodsError, setAuthMethodsError] = useState('');
  const [linkingWorkOS, setLinkingWorkOS] = useState(false);
  const [removingMethodId, setRemovingMethodId] = useState('');
  const [authContext, setAuthContext] = useState(null);
  const [authContextLoading, setAuthContextLoading] = useState(false);
  const [authContextError, setAuthContextError] = useState('');
  const [sessions, setSessions] = useState([]);
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [sessionsError, setSessionsError] = useState('');
  const [revokingSessionId, setRevokingSessionId] = useState('');
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
      setAuthMethods([]);
      setAuthMethodsLoading(false);
      setAuthMethodsError('');
      setAuthContext(null);
      setAuthContextLoading(false);
      setAuthContextError('');
      setSessions([]);
      setSessionsLoading(false);
      setSessionsError('');
      setAgentKeys([]);
      setAgentKeysLoading(false);
      setAgentKeysError('');
      return () => {
        cancelled = true;
      };
    }

    setBindingsLoading(true);
    setBindingsError('');
    setAuthMethodsLoading(true);
    setAuthMethodsError('');
    setAuthContextLoading(true);
    setAuthContextError('');
    setSessionsLoading(true);
    setSessionsError('');
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
    fetchAuthMethods()
      .then((nextMethods) => {
        if (!cancelled) {
          setAuthMethods(nextMethods);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setAuthMethods([]);
          setAuthMethodsError(err?.message || 'Unable to load auth methods.');
        }
      })
      .finally(() => {
        if (!cancelled) {
          setAuthMethodsLoading(false);
        }
      });
    fetchAuthContext()
      .then((nextContext) => {
        if (!cancelled) {
          setAuthContext(nextContext);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setAuthContext(null);
          setAuthContextError(err?.message || 'Unable to load auth context.');
        }
      })
      .finally(() => {
        if (!cancelled) {
          setAuthContextLoading(false);
        }
      });
    fetchAuthSessions()
      .then((nextSessions) => {
        if (!cancelled) {
          setSessions(nextSessions);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setSessions([]);
          setSessionsError(err?.message || 'Unable to load sessions.');
        }
      })
      .finally(() => {
        if (!cancelled) {
          setSessionsLoading(false);
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

  async function refreshSessions() {
    setSessionsLoading(true);
    setSessionsError('');
    try {
      const nextSessions = await fetchAuthSessions();
      setSessions(nextSessions);
    } catch (err) {
      setSessions([]);
      setSessionsError(err?.message || 'Unable to load sessions.');
    } finally {
      setSessionsLoading(false);
    }
  }

  async function refreshAuthMethods() {
    setAuthMethodsLoading(true);
    setAuthMethodsError('');
    try {
      const nextMethods = await fetchAuthMethods();
      setAuthMethods(nextMethods);
    } catch (err) {
      setAuthMethods([]);
      setAuthMethodsError(err?.message || 'Unable to load auth methods.');
    } finally {
      setAuthMethodsLoading(false);
    }
  }

  async function refreshAuthContext() {
    setAuthContextLoading(true);
    setAuthContextError('');
    try {
      const nextContext = await fetchAuthContext();
      setAuthContext(nextContext);
    } catch (err) {
      setAuthContext(null);
      setAuthContextError(err?.message || 'Unable to load auth context.');
    } finally {
      setAuthContextLoading(false);
    }
  }

  async function handleLinkCurrentWorkOS() {
    setAuthMethodsError('');
    setLinkingWorkOS(true);
    try {
      await linkCurrentWorkOSAuthMethod();
      await refreshAuthMethods();
    } catch (err) {
      setAuthMethodsError(err?.message || 'Unable to link WorkOS sign-in.');
    } finally {
      setLinkingWorkOS(false);
    }
  }

  async function handleDeleteAuthMethod(methodId) {
    setAuthMethodsError('');
    setRemovingMethodId(methodId);
    try {
      await deleteAuthMethod(methodId);
      await refreshAuthMethods();
    } catch (err) {
      setAuthMethodsError(err?.message || 'Unable to remove auth method.');
    } finally {
      setRemovingMethodId('');
    }
  }

  async function handleSessionRevoke(sessionId, current) {
    setSessionsError('');
    setRevokingSessionId(sessionId);
    try {
      await deleteAuthSession(sessionId);
      if (current) {
        await onLogout?.();
        return;
      }
      await refreshSessions();
    } catch (err) {
      setSessionsError(err?.message || 'Unable to revoke session.');
    } finally {
      setRevokingSessionId('');
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
                <div className="kv" data-testid="settings-auth-context">
                  <div className="kv-row">
                    <span className="kv-key">Signed in as</span>
                    <span className="kv-val">{username}</span>
                  </div>
                  <div className="kv-row">
                    <span className="kv-key">Browser sign-in</span>
                    <span className="kv-val">{authSessionSource || 'unknown'}</span>
                  </div>
                  {authContextLoading && (
                    <div className="kv-row">
                      <span className="kv-key">API credential</span>
                      <span className="kv-val">loading…</span>
                    </div>
                  )}
                  {!authContextLoading && authContextError && (
                    <div className="kv-row">
                      <span className="kv-key">API credential</span>
                      <span className="kv-val text-destructive">{authContextError}</span>
                    </div>
                  )}
                  {!authContextLoading && !authContextError && authContext && (
                    <>
                      <div className="kv-row">
                        <span className="kv-key">API credential</span>
                        <span className="kv-val">{authContext.auth_source || authContext.authSource || 'unknown'}</span>
                      </div>
                      <div className="kv-row">
                        <span className="kv-key">Session ID</span>
                        <span className="kv-val break-all">{authContext.session_id || authContext.sessionId || 'none'}</span>
                      </div>
                      <div className="kv-row">
                        <span className="kv-key">Agent key</span>
                        <span className="kv-val">{authContext.agent_key_id || authContext.agentKeyId || 'none'}</span>
                      </div>
                      <div className="kv-row">
                        <span className="kv-key">WorkOS linked</span>
                        <span className="kv-val">{authContext.workos_linked || authContext.workosLinked ? 'yes' : 'no'}</span>
                      </div>
                    </>
                  )}
                </div>
                <div className="auth-actions">
                  <Button type="button" onClick={onOpenProfile}>Open Profile Details</Button>
                  <Button type="button" variant="ghost" onClick={refreshAuthContext}>Refresh auth context</Button>
                  <Button type="button" variant="ghost" onClick={refreshAuthMethods}>Refresh auth methods</Button>
                  <Button type="button" variant="ghost" onClick={refreshSessions}>Refresh sessions</Button>
                  <Button type="button" variant="ghost" onClick={onLogout}>Logout</Button>
                </div>
              </CardContent>
            </Card>

            <Card className="border-border/70">
              <CardContent className="space-y-4 pt-6">
                <div className="space-y-2">
                  <h3>Linked sign-in methods</h3>
                  <p className="status">
                    WorkOS is the primary human sign-in path. Local password methods still appear here when they exist.
                  </p>
                </div>
                {authMethodsLoading && <div className="panel-empty">Loading auth methods…</div>}
                {!authMethodsLoading && authMethodsError && <div className="panel-error">{authMethodsError}</div>}
                {!authMethodsLoading && !authMethodsError && authMethods.length === 0 && (
                  <div className="panel-empty" data-testid="settings-auth-methods-empty">
                    No linked human sign-in methods yet.
                  </div>
                )}
                {!authMethodsLoading && !authMethodsError && authMethods.length > 0 && (
                  <div className="space-y-3" data-testid="settings-auth-methods">
                    {authMethods.map((method) => (
                      <div key={method.id} className="rounded-lg border border-border/70 bg-background/40 p-3">
                        <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
                          <div className="kv flex-1">
                            <div className="kv-row">
                              <span className="kv-key">Provider</span>
                              <span className="kv-val">{method.provider || formatAuthMethodType(method.type)}</span>
                            </div>
                            <div className="kv-row">
                              <span className="kv-key">Type</span>
                              <span className="kv-val">{formatAuthMethodType(method.type)}</span>
                            </div>
                            <div className="kv-row">
                              <span className="kv-key">Email</span>
                              <span className="kv-val">{method.email || 'not set'}</span>
                            </div>
                            <div className="kv-row">
                              <span className="kv-key">Linked</span>
                              <span className="kv-val">{method.linked_at || method.linkedAt || 'unknown'}</span>
                            </div>
                          </div>
                          <div className="flex items-center gap-2">
                            <Badge variant="outline">{method.provider || formatAuthMethodType(method.type)}</Badge>
                            <Button
                              type="button"
                              variant="destructive"
                              size="sm"
                              disabled={removingMethodId === method.id}
                              onClick={() => handleDeleteAuthMethod(method.id)}
                              data-testid={`settings-auth-method-delete-${method.id}`}
                            >
                              {removingMethodId === method.id ? 'Removing…' : 'Remove'}
                            </Button>
                          </div>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
                {authSessionSource === 'workos' && !authMethods.some((method) => method.id === 'oauth:workos') && (
                  <div className="auth-actions">
                    <Button
                      type="button"
                      onClick={handleLinkCurrentWorkOS}
                      disabled={linkingWorkOS}
                      data-testid="settings-auth-method-link-workos"
                    >
                      {linkingWorkOS ? 'Linking…' : 'Link current WorkOS login'}
                    </Button>
                  </div>
                )}
              </CardContent>
            </Card>

            <Card className="border-border/70">
              <CardContent className="space-y-4 pt-6">
                <div className="space-y-2">
                  <h3>Gitslice sessions</h3>
                  <p className="status">
                    Web, CLI, device, and agent sessions use local Gitslice API credentials here.
                  </p>
                </div>
                {sessionsLoading && <div className="panel-empty">Loading sessions…</div>}
                {!sessionsLoading && sessionsError && <div className="panel-error">{sessionsError}</div>}
                {!sessionsLoading && !sessionsError && sessions.length === 0 && (
                  <div className="panel-empty" data-testid="settings-sessions-empty">
                    No Gitslice sessions yet.
                  </div>
                )}
                {!sessionsLoading && !sessionsError && sessions.length > 0 && (
                  <div className="space-y-3" data-testid="settings-sessions">
                    {sessions.map((session) => (
                      <div key={session.id} className="rounded-lg border border-border/70 bg-background/40 p-3">
                        <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
                          <div className="kv flex-1">
                            <div className="kv-row">
                              <span className="kv-key">Session ID</span>
                              <span className="kv-val break-all">{session.id}</span>
                            </div>
                            <div className="kv-row">
                              <span className="kv-key">Device</span>
                              <span className="kv-val">{session.device_info || session.deviceInfo || 'unknown'}</span>
                            </div>
                            <div className="kv-row">
                              <span className="kv-key">Last seen</span>
                              <span className="kv-val">{session.last_seen_at || session.lastSeenAt || 'unknown'}</span>
                            </div>
                            <div className="kv-row">
                              <span className="kv-key">Agent key</span>
                              <span className="kv-val">{session.agent_key_id || session.agentKeyId || 'none'}</span>
                            </div>
                          </div>
                          <div className="flex items-center gap-2">
                            <Badge variant={session.current ? 'secondary' : 'outline'}>
                              {session.current ? 'Current' : 'Active'}
                            </Badge>
                            <Button
                              type="button"
                              variant="destructive"
                              size="sm"
                              disabled={revokingSessionId === session.id}
                              onClick={() => handleSessionRevoke(session.id, Boolean(session.current))}
                              data-testid={`settings-session-revoke-${session.id}`}
                            >
                              {revokingSessionId === session.id
                                ? 'Revoking…'
                                : session.current
                                  ? 'Revoke and sign out'
                                  : 'Revoke'}
                            </Button>
                          </div>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
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
          </div>
        </>
      )}
    </section>
  );
}
