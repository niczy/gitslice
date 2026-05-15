import { useCallback, useEffect, useMemo, useState } from 'react';
import { KeyRound, Lock, Pencil, RefreshCw, Save, Trash2 } from 'lucide-react';

import {
  deleteSliceEnvKV,
  getSliceEnvRequirements,
  listSliceEnvKV,
  setSliceEnvSecret,
  setSliceEnvValue,
} from '../../utils/api.js';
import { Badge } from '../ui/badge.jsx';
import { Button } from '../ui/button.jsx';
import { Card, CardContent } from '../ui/card.jsx';
import { Input } from '../ui/input.jsx';

const COMMON_PROFILES = ['local', 'agent', 'ci', 'staging', 'production', 'default'];

function readField(value, camelName, snakeName, fallback = '') {
  return value?.[camelName] ?? value?.[snakeName] ?? fallback;
}

function normalizeKVEntry(entry) {
  const sliceId = readField(entry, 'sliceId', 'slice_id', '');
  const profile = readField(entry, 'profile', 'profile', 'default') || 'default';
  const key = readField(entry, 'key', 'key', '');
  const className = readField(entry, 'class', 'class', '');
  return {
    id: readField(entry, 'id', 'id', `${profile}:${sliceId || 'home'}:${className}:${key}`),
    homeId: readField(entry, 'homeId', 'home_id', ''),
    sliceId,
    sliceSlug: readField(entry, 'sliceSlug', 'slice_slug', ''),
    profile,
    key,
    className,
    value: readField(entry, 'value', 'value', ''),
    valueHash: readField(entry, 'valueHash', 'value_hash', ''),
    version: Number(readField(entry, 'version', 'version', 0) || 0),
    hasValue: Boolean(readField(entry, 'hasValue', 'has_value', false)),
  };
}

function normalizeRequirementFile(file) {
  return {
    path: readField(file, 'path', 'path', ''),
    mode: readField(file, 'mode', 'mode', ''),
    sensitive: Boolean(readField(file, 'sensitive', 'sensitive', false)),
    profile: readField(file, 'profile', 'profile', 'local') || 'local',
    requiredSecrets: readField(file, 'requiredSecrets', 'required_secrets', []) || [],
    optionalSecrets: readField(file, 'optionalSecrets', 'optional_secrets', []) || [],
    requiredValues: readField(file, 'requiredValues', 'required_values', []) || [],
    optionalValues: readField(file, 'optionalValues', 'optional_values', []) || [],
  };
}

function entryIdentity(entry) {
  return [
    entry.profile,
    entry.sliceId ? 'slice' : 'home',
    entry.className,
    entry.key,
  ].join(':');
}

function shortHash(hash) {
  if (!hash) {
    return '';
  }
  return hash.replace(/^sha256:/, '').slice(0, 10);
}

function keyConfigured(entries, className, key) {
  return entries.some((entry) => entry.className === className && entry.key === key && entry.hasValue);
}

function keyBadgeClass(configured) {
  return configured ? 'env-kv-key env-kv-key--configured' : 'env-kv-key env-kv-key--missing';
}

export function SliceEnvKVCard({ sliceId }) {
  const [profile, setProfile] = useState('local');
  const [requirements, setRequirements] = useState(null);
  const [requirementsLoading, setRequirementsLoading] = useState(false);
  const [requirementsError, setRequirementsError] = useState('');
  const [entries, setEntries] = useState([]);
  const [entriesLoading, setEntriesLoading] = useState(false);
  const [entriesError, setEntriesError] = useState('');
  const [refreshToken, setRefreshToken] = useState(0);
  const [className, setClassName] = useState('value');
  const [key, setKey] = useState('');
  const [value, setValue] = useState('');
  const [saving, setSaving] = useState(false);
  const [deletingEntry, setDeletingEntry] = useState('');
  const [formError, setFormError] = useState('');
  const [formSuccess, setFormSuccess] = useState('');

  useEffect(() => {
    if (!sliceId) {
      setRequirements(null);
      setRequirementsLoading(false);
      setRequirementsError('');
      return;
    }

    let active = true;
    const load = async () => {
      setRequirementsLoading(true);
      setRequirementsError('');
      try {
        const response = await getSliceEnvRequirements(sliceId);
        if (!active) {
          return;
        }
        setRequirements(response);
      } catch (error) {
        if (active) {
          setRequirementsError(error?.message || 'Unable to load environment requirements.');
        }
      } finally {
        if (active) {
          setRequirementsLoading(false);
        }
      }
    };

    load();
    return () => {
      active = false;
    };
  }, [sliceId]);

  const loadEntries = useCallback(async () => {
    if (!sliceId) {
      setEntries([]);
      return;
    }

    setEntriesLoading(true);
    setEntriesError('');
    try {
      const profilesToLoad = profile === 'default' ? ['default'] : [profile, 'default'];
      const responses = await Promise.all(
        profilesToLoad.map((currentProfile) => listSliceEnvKV(sliceId, {
          profile: currentProfile,
        })),
      );
      const merged = new Map();
      responses.forEach((response) => {
        (response?.entries || []).forEach((rawEntry) => {
          const entry = normalizeKVEntry(rawEntry);
          merged.set(entryIdentity(entry), entry);
        });
      });
      const sortedEntries = Array.from(merged.values()).sort((a, b) => {
        if (a.profile !== b.profile) {
          if (a.profile === profile) {
            return -1;
          }
          if (b.profile === profile) {
            return 1;
          }
          return a.profile.localeCompare(b.profile);
        }
        if (a.className !== b.className) {
          return a.className.localeCompare(b.className);
        }
        return a.key.localeCompare(b.key);
      });
      setEntries(sortedEntries);
    } catch (error) {
      setEntriesError(error?.message || 'Unable to load environment KV entries.');
    } finally {
      setEntriesLoading(false);
    }
  }, [profile, sliceId]);

  useEffect(() => {
    loadEntries();
  }, [loadEntries, refreshToken]);

  const requirementPayload = requirements?.requirements || {};
  const requirementProfiles = useMemo(() => {
    return Array.isArray(requirementPayload?.profiles) ? requirementPayload.profiles : [];
  }, [requirementPayload?.profiles]);

  const profileOptions = useMemo(() => {
    return Array.from(new Set([
      ...requirementProfiles,
      ...COMMON_PROFILES,
      profile,
    ].filter(Boolean)));
  }, [profile, requirementProfiles]);

  const requirementFiles = useMemo(() => {
    const allFiles = (requirementPayload?.files || []).map(normalizeRequirementFile);
    const directFiles = allFiles.filter((file) => file.profile === profile);
    if (profile === 'agent' && directFiles.length === 0) {
      return allFiles.filter((file) => file.profile === 'local');
    }
    return directFiles;
  }, [profile, requirementPayload?.files]);

  const missingRequiredCount = useMemo(() => {
    let count = 0;
    requirementFiles.forEach((file) => {
      file.requiredSecrets.forEach((requiredKey) => {
        if (!keyConfigured(entries, 'secret', requiredKey)) {
          count += 1;
        }
      });
      file.requiredValues.forEach((requiredKey) => {
        if (!keyConfigured(entries, 'value', requiredKey)) {
          count += 1;
        }
      });
    });
    return count;
  }, [entries, requirementFiles]);

  const resetForm = () => {
    setClassName('value');
    setKey('');
    setValue('');
  };

  const saveEntry = async (event) => {
    event.preventDefault();
    const trimmedKey = key.trim();
    if (!trimmedKey) {
      setFormError('Key is required.');
      return;
    }
    setSaving(true);
    setFormError('');
    setFormSuccess('');
    try {
      const payload = {
        profile,
        key: trimmedKey,
        value,
      };
      if (className === 'secret') {
        await setSliceEnvSecret(sliceId, payload);
      } else {
        await setSliceEnvValue(sliceId, payload);
      }
      setFormSuccess(`${trimmedKey} saved for ${profile}.`);
      resetForm();
      setRefreshToken((current) => current + 1);
    } catch (error) {
      setFormError(error?.message || 'Unable to save environment KV entry.');
    } finally {
      setSaving(false);
    }
  };

  const deleteEntry = async (entry) => {
    const id = entryIdentity(entry);
    setDeletingEntry(id);
    setFormError('');
    setFormSuccess('');
    try {
      await deleteSliceEnvKV(sliceId, {
        profile: entry.profile,
        className: entry.className,
        key: entry.key,
      });
      setFormSuccess(`${entry.key} deleted from ${entry.profile}.`);
      setRefreshToken((current) => current + 1);
    } catch (error) {
      setFormError(error?.message || 'Unable to delete environment KV entry.');
    } finally {
      setDeletingEntry('');
    }
  };

  const editEntry = (entry) => {
    setProfile(entry.profile || 'default');
    setClassName(entry.className === 'secret' ? 'secret' : 'value');
    setKey(entry.key);
    setValue(entry.className === 'secret' ? '' : entry.value);
    setFormError('');
    setFormSuccess('');
  };

  const foundRequirements = Boolean(requirements?.found);
  const issueList = requirements?.issues || [];
  const requirementPath = requirements?.requirementsPath || requirements?.requirements_path || '';
  const showingAgentFallback = profile === 'agent'
    && requirementFiles.length > 0
    && !requirementProfiles.includes('agent')
    && requirementProfiles.includes('local');

  return (
    <Card className="border-border/70">
      <CardContent className="pt-6">
        <div className="slice-settings-card-header">
          <div>
            <h4>Environment KV</h4>
            <p>Store server-side values used when checkout materializes untracked environment files.</p>
          </div>
          <Badge
            variant="outline"
            className={foundRequirements ? 'env-kv-status env-kv-status--ready' : 'env-kv-status'}
            data-testid="slice-env-kv-status"
          >
            {foundRequirements ? 'env.yaml found' : 'No env.yaml'}
          </Badge>
        </div>

        <div className="env-kv-toolbar">
          <label className="env-kv-field">
            <span>Profile</span>
            <select
              value={profile}
              onChange={(event) => {
                setProfile(event.target.value);
                setFormSuccess('');
                setFormError('');
              }}
              data-testid="slice-env-kv-profile"
            >
              {profileOptions.map((option) => (
                <option key={option} value={option}>{option}</option>
              ))}
            </select>
          </label>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setRefreshToken((current) => current + 1)}
            disabled={entriesLoading}
            data-testid="slice-env-kv-refresh"
          >
            <RefreshCw size={14} aria-hidden="true" />
            Refresh
          </Button>
        </div>

        {requirementsLoading && <div className="panel-empty">Loading environment requirements...</div>}
        {!requirementsLoading && requirementsError && <div className="panel-error">{requirementsError}</div>}

        {!requirementsLoading && !requirementsError && (
          <div className="env-kv-requirements">
            {requirementPath && (
              <div className="env-kv-path">
                <span>Requirements</span>
                <code>{requirementPath}</code>
              </div>
            )}
            {issueList.length > 0 && (
              <div className="panel-error">
                {issueList.map((issue) => issue?.message || issue?.code || 'Invalid environment requirements.').join(' ')}
              </div>
            )}
            {!foundRequirements && (
              <div className="panel-empty">No environment materialization file is tracked for this slice.</div>
            )}
            {foundRequirements && requirementFiles.length === 0 && (
              <div className="panel-empty">No materialized files are declared for the selected profile.</div>
            )}
            {showingAgentFallback && (
              <div className="env-kv-note">Agent profile is using the local profile requirements.</div>
            )}
            {foundRequirements && requirementFiles.length > 0 && (
              <div className="env-kv-file-list">
                {requirementFiles.map((file) => (
                  <div key={`${file.profile}:${file.path}`} className="env-kv-file-row">
                    <div className="env-kv-file-main">
                      <strong>{file.path}</strong>
                      <span>{file.mode || '0644'} {file.sensitive ? 'sensitive' : 'plain'}</span>
                    </div>
                    <div className="env-kv-key-list">
                      {file.requiredSecrets.map((requiredKey) => {
                        const configured = keyConfigured(entries, 'secret', requiredKey);
                        return (
                          <span key={`secret:${requiredKey}`} className={keyBadgeClass(configured)}>
                            <Lock size={12} aria-hidden="true" />
                            {requiredKey}
                          </span>
                        );
                      })}
                      {file.requiredValues.map((requiredKey) => {
                        const configured = keyConfigured(entries, 'value', requiredKey);
                        return (
                          <span key={`value:${requiredKey}`} className={keyBadgeClass(configured)}>
                            <KeyRound size={12} aria-hidden="true" />
                            {requiredKey}
                          </span>
                        );
                      })}
                    </div>
                  </div>
                ))}
              </div>
            )}
            {foundRequirements && requirementFiles.length > 0 && missingRequiredCount > 0 && (
              <div className="env-kv-note env-kv-note--warning">
                {missingRequiredCount} required {missingRequiredCount === 1 ? 'entry is' : 'entries are'} missing for {profile}.
              </div>
            )}
          </div>
        )}

        <form className="env-kv-form" onSubmit={saveEntry} data-testid="slice-env-kv-form">
          <div className="env-kv-form-grid">
            <label className="env-kv-field">
              <span>Class</span>
              <select value={className} onChange={(event) => setClassName(event.target.value)}>
                <option value="value">Value</option>
                <option value="secret">Secret</option>
              </select>
            </label>
            <label className="env-kv-field env-kv-field--key">
              <span>Key</span>
              <Input
                value={key}
                onChange={(event) => setKey(event.target.value)}
                placeholder="DATABASE_URL"
                data-testid="slice-env-kv-key"
              />
            </label>
          </div>
          <label className="env-kv-field">
            <span>{className === 'secret' ? 'Secret value' : 'Value'}</span>
            <Input
              type={className === 'secret' ? 'password' : 'text'}
              value={value}
              onChange={(event) => setValue(event.target.value)}
              placeholder={className === 'secret' ? 'Stored without readback' : 'development'}
              data-testid="slice-env-kv-value"
            />
          </label>
          <div className="env-kv-actions">
            <Button
              type="submit"
              size="sm"
              disabled={saving || !key.trim()}
              data-testid="slice-env-kv-save"
            >
              <Save size={14} aria-hidden="true" />
              {saving ? 'Saving...' : `Save ${className}`}
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={resetForm}>
              Clear
            </Button>
          </div>
          {formError && <div className="panel-error">{formError}</div>}
          {formSuccess && <div className="panel-success">{formSuccess}</div>}
        </form>

        <div className="env-kv-entry-section">
          <div className="env-kv-entry-header">
            <h5>Configured entries</h5>
            <span>{entries.length} total</span>
          </div>
          {entriesLoading && <div className="panel-empty">Loading configured entries...</div>}
          {!entriesLoading && entriesError && <div className="panel-error">{entriesError}</div>}
          {!entriesLoading && !entriesError && entries.length === 0 && (
            <div className="panel-empty">No values or secrets configured for this profile.</div>
          )}
          {!entriesLoading && !entriesError && entries.length > 0 && (
            <div className="env-kv-entry-list" data-testid="slice-env-kv-entry-list">
              {entries.map((entry) => {
                const id = entryIdentity(entry);
                const isSecret = entry.className === 'secret';
                return (
                  <div key={id} className="env-kv-entry-row">
                    <div className="env-kv-entry-icon" aria-hidden="true">
                      {isSecret ? <Lock size={14} /> : <KeyRound size={14} />}
                    </div>
                    <div className="env-kv-entry-main">
                      <div className="env-kv-entry-title">
                        <strong>{entry.key}</strong>
                        <span>{entry.className}</span>
                        <span>{entry.profile}</span>
                      </div>
                      <div className="env-kv-entry-value">
                        {isSecret ? (
                          <span>Configured{entry.version ? ` v${entry.version}` : ''}</span>
                        ) : (
                          <code>{entry.value}</code>
                        )}
                        {entry.valueHash && <small>{shortHash(entry.valueHash)}</small>}
                      </div>
                    </div>
                    <div className="env-kv-entry-actions">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => editEntry(entry)}
                        title={`Edit ${entry.key}`}
                        aria-label={`Edit ${entry.key}`}
                      >
                        <Pencil size={14} aria-hidden="true" />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => deleteEntry(entry)}
                        disabled={deletingEntry === id}
                        title={`Delete ${entry.key}`}
                        aria-label={`Delete ${entry.key}`}
                      >
                        <Trash2 size={14} aria-hidden="true" />
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
