import { useEffect, useRef, useState } from 'react';

import { Badge } from './ui/badge.jsx';
import CISettingsPanel from './CISettingsPanel.jsx';
import AccountSettingsPanel from './settings/AccountSettingsPanel.jsx';
import SettingsTabs from './settings/SettingsTabs.jsx';
import {
  createAgentKey,
  fetchAgentKeys,
  revokeAgentKey,
} from '../api/agents.js';
import {
  deleteAuthMethod,
  deleteAuthSession,
  fetchAuthContext,
  fetchAuthMethods,
  fetchAuthSessions,
} from '../utils/api.js';

export default function SettingsPage({
  username,
  authSessionSource,
  settingsSection = '',
  settingsRunnerId = '',
  onOpenProfile,
  onLogout,
  initialSettingsData = null,
}) {
  const activeSection = String(settingsSection || '').startsWith('ci') ? 'ci' : 'account';
  const hasInitialSettings = initialSettingsData?.username === username;
  const [loadedSettingsUsername, setLoadedSettingsUsername] = useState(() => (hasInitialSettings ? username : ''));
  const [authMethods, setAuthMethods] = useState(() => (hasInitialSettings ? initialSettingsData.authMethods || [] : []));
  const [authMethodsLoading, setAuthMethodsLoading] = useState(false);
  const [authMethodsError, setAuthMethodsError] = useState(() => (hasInitialSettings ? initialSettingsData.authMethodsError || '' : ''));
  const [removingMethodId, setRemovingMethodId] = useState('');
  const [authContext, setAuthContext] = useState(() => (hasInitialSettings ? initialSettingsData.authContext || null : null));
  const [authContextLoading, setAuthContextLoading] = useState(false);
  const [authContextError, setAuthContextError] = useState(() => (hasInitialSettings ? initialSettingsData.authContextError || '' : ''));
  const [sessions, setSessions] = useState(() => (hasInitialSettings ? initialSettingsData.sessions || [] : []));
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [sessionsError, setSessionsError] = useState(() => (hasInitialSettings ? initialSettingsData.sessionsError || '' : ''));
  const [revokingSessionId, setRevokingSessionId] = useState('');
  const [agentKeys, setAgentKeys] = useState(() => (hasInitialSettings ? initialSettingsData.agentKeys || [] : []));
  const [agentKeysLoading, setAgentKeysLoading] = useState(false);
  const [agentKeysError, setAgentKeysError] = useState(() => (hasInitialSettings ? initialSettingsData.agentKeysError || '' : ''));
  const [agentKeyName, setAgentKeyName] = useState('');
  const [agentKeyPublicKey, setAgentKeyPublicKey] = useState('');
  const [agentKeyFormError, setAgentKeyFormError] = useState('');
  const [agentKeySaving, setAgentKeySaving] = useState(false);
  const [revokingKeyId, setRevokingKeyId] = useState('');
  const clientRefreshUsernameRef = useRef('');

  useEffect(() => {
    let cancelled = false;
    if (!username) {
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
      setLoadedSettingsUsername('');
      return () => {
        cancelled = true;
      };
    }

    if (initialSettingsData?.username === username && loadedSettingsUsername !== username) {
      setAuthMethods(initialSettingsData.authMethods || []);
      setAuthMethodsLoading(false);
      setAuthMethodsError(initialSettingsData.authMethodsError || '');
      setAuthContext(initialSettingsData.authContext || null);
      setAuthContextLoading(false);
      setAuthContextError(initialSettingsData.authContextError || '');
      setSessions(initialSettingsData.sessions || []);
      setSessionsLoading(false);
      setSessionsError(initialSettingsData.sessionsError || '');
      setAgentKeys(initialSettingsData.agentKeys || []);
      setAgentKeysLoading(false);
      setAgentKeysError(initialSettingsData.agentKeysError || '');
      setLoadedSettingsUsername(username);
      return () => {
        cancelled = true;
      };
    }

    const hasSeededSettings = loadedSettingsUsername === username;
    if (hasSeededSettings) {
      if (clientRefreshUsernameRef.current === username) {
        return () => {
          cancelled = true;
        };
      }
      clientRefreshUsernameRef.current = username;
    } else {
      clientRefreshUsernameRef.current = username;
      setAuthMethodsLoading(true);
      setAuthMethodsError('');
      setAuthContextLoading(true);
      setAuthContextError('');
      setSessionsLoading(true);
      setSessionsError('');
      setAgentKeysLoading(true);
      setAgentKeysError('');
    }
    fetchAuthMethods()
      .then((nextMethods) => {
        if (!cancelled) {
          setAuthMethods(nextMethods);
          setLoadedSettingsUsername(username);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setAuthMethods([]);
          setAuthMethodsError(err?.message || 'Unable to load auth methods.');
          setLoadedSettingsUsername(username);
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
  }, [initialSettingsData, loadedSettingsUsername, username]);

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
        <p>Manage session details and enrolled agent keys from one account surface.</p>
      </div>

      {!username && <div className="panel-error">You need to log in before account settings are available.</div>}
      {username && (
        <>
          <SettingsTabs activeSection={activeSection} />
          {activeSection === 'ci' && (
            <CISettingsPanel
              username={username}
              settingsRunnerId={settingsRunnerId}
              initialSettingsData={initialSettingsData}
            />
          )}
          {activeSection === 'account' && (
            <AccountSettingsPanel
              agentKeyFormError={agentKeyFormError}
              agentKeyName={agentKeyName}
              agentKeyPublicKey={agentKeyPublicKey}
              agentKeySaving={agentKeySaving}
              agentKeys={agentKeys}
              agentKeysError={agentKeysError}
              agentKeysLoading={agentKeysLoading}
              authContext={authContext}
              authContextError={authContextError}
              authContextLoading={authContextLoading}
              authMethods={authMethods}
              authMethodsError={authMethodsError}
              authMethodsLoading={authMethodsLoading}
              authSessionSource={authSessionSource}
              onAgentKeyCreate={handleAgentKeyCreate}
              onAgentKeyNameChange={setAgentKeyName}
              onAgentKeyPublicKeyChange={setAgentKeyPublicKey}
              onAgentKeyRevoke={handleAgentKeyRevoke}
              onDeleteAuthMethod={handleDeleteAuthMethod}
              onLogout={onLogout}
              onOpenProfile={onOpenProfile}
              onRefreshAuthContext={refreshAuthContext}
              onRefreshAuthMethods={refreshAuthMethods}
              onRefreshSessions={refreshSessions}
              onSessionRevoke={handleSessionRevoke}
              removingMethodId={removingMethodId}
              revokingKeyId={revokingKeyId}
              revokingSessionId={revokingSessionId}
              sessions={sessions}
              sessionsError={sessionsError}
              sessionsLoading={sessionsLoading}
              username={username}
            />
          )}
        </>
      )}
    </section>
  );
}
